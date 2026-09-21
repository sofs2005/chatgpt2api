package service

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"chatgpt2api/internal/util"
)

const (
	DefaultBrowserUserAgent              = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
	DefaultBrowserSecCHUA                = `"Not:A-Brand";v="99", "Google Chrome";v="145", "Chromium";v="145"`
	DefaultBrowserSecCHUAFullVersion     = `"145.0.0.0"`
	DefaultBrowserSecCHUAFullVersionList = `"Not:A-Brand";v="99.0.0.0", "Google Chrome";v="145.0.0.0", "Chromium";v="145.0.0.0"`
	DefaultBrowserSecCHUAMobile          = "?0"
	DefaultBrowserSecCHUAPlatform        = `"Windows"`
	DefaultBrowserSecCHUAPlatformVersion = `"19.0.0"`
	DefaultBrowserSecCHUAArch            = `"x86"`
	DefaultBrowserSecCHUABitness         = `"64"`
	DefaultBrowserImpersonationProfile   = "chrome145"
)

// browserFamilyVersionPools 只包含「TLS 指纹层能真正兑现」的浏览器与版本。
//
// 出站请求的 TLS/HTTP2 指纹由 surf 提供，而 surf 仅实现两套浏览器指纹
// （surf@v1.0.199/profiles 下只有 chrome 与 firefox）：Chrome 对应
// HelloChrome_145，Firefox 对应 HelloFirefox_148。且 Impersonate().Chrome()
// 不接收版本参数——impersonate 字符串里的版本号会被完全丢弃。
//
// 因此池中若出现 chrome146/147/148、edge*、safari*，实际出站仍会是
// 「Chrome 145 的 TLS + 被 surf 改写成 Chrome 145 的 UA」，而账号指纹里
// 未被 surf 覆盖的 Sec-Ch-Ua-Full-Version 仍然是原版本号，于是出现
// 「Sec-Ch-Ua 说 145、Sec-Ch-Ua-Full-Version 说 148」这种自相矛盾信号。
// 收紧到 surf 能兑现的集合后，UA / Client-Hints / TLS / HTTP2 全部同源一致。
//
// 版本号必须与 surf 内置的 UA 严格对应（chromeUserAgent 为 Chrome/145.0.0.0，
// firefoxUserAgent 为 Firefox/148.0），否则仍会产生矛盾。
var browserFamilyVersionPools = map[string][]string{
	"chrome":  []string{"145"},
	"firefox": []string{"148"},
}

type browserFamilyWeight struct {
	family string
	weight int
}

var browserFamilySelectionWeights = []browserFamilyWeight{
	{family: "chrome", weight: 70},
	{family: "firefox", weight: 30},
}

type browserHeaderMetadata struct {
	secCHUA         string
	fullVersion     string
	fullVersionList string
}

type browserFingerprintTemplate struct {
	family          string
	version         string
	userAgent       string
	secCHUA         string
	fullVersion     string
	fullVersionList string
	impersonate     string
}

func BrowserFamilyVersionPools() map[string][]string {
	out := make(map[string][]string, len(browserFamilyVersionPools))
	for family, versions := range browserFamilyVersionPools {
		out[family] = append([]string(nil), versions...)
	}
	return out
}

func NewBrowserFingerprint() map[string]any {
	return BrowserFingerprintFromFamilyVersion("chrome", "145")
}

func NewAccountBrowserFingerprint(random *rand.Rand) map[string]any {
	family, version := randomBrowserFamilyVersion(random)
	return BrowserFingerprintFromFamilyVersion(family, version)
}

// newAccountFingerprintForDevice 生成账号指纹；deviceID 非空时沿用它作为设备身份。
//
// 注册链路用它把「注册时实际使用的设备 id」带进账号，避免入库时另起一套身份，
// 与注册阶段写入的 oai-did cookie 脱节。设备 id 为空时退化为随机指纹。
func newAccountFingerprintForDevice(random *rand.Rand, deviceID string) map[string]any {
	fp := NewAccountBrowserFingerprint(random)
	if deviceID = strings.TrimSpace(deviceID); deviceID != "" {
		fp["oai-device-id"] = deviceID
	}
	return fp
}

func randomBrowserFamilyVersion(random *rand.Rand) (string, string) {
	family := randomBrowserFamily(random)
	versions := browserFamilyVersionPools[family]
	if len(versions) == 0 {
		return "chrome", "145"
	}
	if random == nil {
		return family, versions[0]
	}
	return family, versions[random.Intn(len(versions))]
}

func randomBrowserFamily(random *rand.Rand) string {
	if random == nil {
		return "chrome"
	}
	total := 0
	for _, choice := range browserFamilySelectionWeights {
		total += choice.weight
	}
	if total <= 0 {
		return "chrome"
	}
	slot := random.Intn(total)
	for _, choice := range browserFamilySelectionWeights {
		if slot < choice.weight {
			return choice.family
		}
		slot -= choice.weight
	}
	return "chrome"
}

func BrowserFingerprintFromFamilyVersion(family, version string) map[string]any {
	template, ok := browserFingerprintTemplateFromFamilyVersion(family, version)
	if !ok {
		template = browserFingerprintTemplate{
			family:          "chrome",
			version:         "145",
			userAgent:       DefaultBrowserUserAgent,
			secCHUA:         DefaultBrowserSecCHUA,
			fullVersion:     strings.Trim(DefaultBrowserSecCHUAFullVersion, `"`),
			fullVersionList: DefaultBrowserSecCHUAFullVersionList,
			impersonate:     DefaultBrowserImpersonationProfile,
		}
	}
	return template.toFingerprint()
}

func (t browserFingerprintTemplate) toFingerprint() map[string]any {
	return map[string]any{
		"version":                     1,
		"browser-family":              t.family,
		"browser-version":             t.version,
		"impersonate":                 t.impersonate,
		"user-agent":                  t.userAgent,
		"sec-ch-ua":                   t.secCHUA,
		"sec-ch-ua-mobile":            DefaultBrowserSecCHUAMobile,
		"sec-ch-ua-platform":          DefaultBrowserSecCHUAPlatform,
		"sec-ch-ua-arch":              DefaultBrowserSecCHUAArch,
		"sec-ch-ua-bitness":           DefaultBrowserSecCHUABitness,
		"sec-ch-ua-full-version":      quoteBrowserHeaderValue(t.fullVersion),
		"sec-ch-ua-full-version-list": t.fullVersionList,
		"sec-ch-ua-platform-version":  DefaultBrowserSecCHUAPlatformVersion,
		"oai-device-id":               util.NewUUID(),
		"oai-session-id":              util.NewUUID(),
	}
}

func browserFingerprintTemplateFromFamilyVersion(family, version string) (browserFingerprintTemplate, bool) {
	family = strings.ToLower(strings.TrimSpace(family))
	version = strings.TrimSpace(version)
	if family == "" || version == "" || !browserFamilyVersionInPool(family, version) {
		return browserFingerprintTemplate{}, false
	}

	switch family {
	case "chrome":
		return chromiumBrowserFingerprintTemplate(family, version), true
	case "firefox":
		return firefoxBrowserFingerprintTemplate(family, version), true
	default:
		return browserFingerprintTemplate{}, false
	}
}

// chromiumBrowserFingerprintTemplate 只服务 Chrome：指纹池不含 edge，
// 因为 surf 没有 Edge 的 TLS 实现，Edge 身份无法兑现。
func chromiumBrowserFingerprintTemplate(family, version string) browserFingerprintTemplate {
	major := browserMajorVersion(version)
	fullVersion := browserNormalizeFullVersion(version)
	return browserFingerprintTemplate{
		family:          family,
		version:         version,
		userAgent:       fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36", major),
		secCHUA:         fmt.Sprintf(`"Not:A-Brand";v="99", "Google Chrome";v="%s", "Chromium";v="%s"`, major, major),
		fullVersion:     fullVersion,
		fullVersionList: fmt.Sprintf(`"Not:A-Brand";v="99.0.0.0", "Google Chrome";v="%s", "Chromium";v="%s"`, fullVersion, fullVersion),
		impersonate:     "chrome" + major,
	}
}

func firefoxBrowserFingerprintTemplate(family, version string) browserFingerprintTemplate {
	major := browserMajorVersion(version)
	fullVersion := browserNormalizeFullVersion(version)
	return browserFingerprintTemplate{
		family:          family,
		version:         version,
		userAgent:       fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:%s.0) Gecko/20100101 Firefox/%s.0", major, major),
		secCHUA:         fmt.Sprintf(`"Not A(Brand";v="99", "Firefox";v="%s"`, major),
		fullVersion:     fullVersion,
		fullVersionList: fmt.Sprintf(`"Not A(Brand";v="99.0.0.0", "Firefox";v="%s"`, fullVersion),
		impersonate:     "firefox" + major,
	}
}

func browserFamilyVersionInPool(family, version string) bool {
	versions, ok := browserFamilyVersionPools[family]
	if !ok {
		return false
	}
	for _, candidate := range versions {
		if candidate == version {
			return true
		}
	}
	return false
}

func NormalizeBrowserFingerprint(raw any) (map[string]any, bool) {
	base := NewBrowserFingerprint()
	input, ok, keyNormalized := normalizeBrowserFingerprintInput(raw)
	if !ok || input == nil {
		return base, true
	}

	normalized := make(map[string]any, len(input)+len(base))
	changed := keyNormalized
	for key, value := range input {
		normalized[key] = value
	}

	userAgent := util.Clean(normalized["user-agent"])
	if userAgent == "" {
		userAgent = DefaultBrowserUserAgent
		normalized["user-agent"] = userAgent
		changed = true
	}
	metadata := BrowserMetadataFromUserAgent(userAgent)

	setString := func(key, value string) {
		if util.Clean(normalized[key]) == "" {
			normalized[key] = value
			changed = true
		}
	}

	if util.ToInt(normalized["version"], 0) != 1 {
		normalized["version"] = 1
		changed = true
	}

	setString("impersonate", defaultImpersonationProfileForUserAgent(userAgent))
	setString("sec-ch-ua", metadata.secCHUA)
	setString("sec-ch-ua-mobile", DefaultBrowserSecCHUAMobile)
	setString("sec-ch-ua-platform", DefaultBrowserSecCHUAPlatform)
	setString("sec-ch-ua-arch", DefaultBrowserSecCHUAArch)
	setString("sec-ch-ua-bitness", DefaultBrowserSecCHUABitness)
	setString("sec-ch-ua-full-version", quoteBrowserHeaderValue(metadata.fullVersion))
	setString("sec-ch-ua-full-version-list", metadata.fullVersionList)
	setString("sec-ch-ua-platform-version", DefaultBrowserSecCHUAPlatformVersion)
	setString("oai-device-id", util.NewUUID())
	setString("oai-session-id", util.NewUUID())

	return normalized, changed
}

func BrowserFingerprintStringMap(raw any) map[string]string {
	fp, _ := NormalizeBrowserFingerprint(raw)
	stringsMap := make(map[string]string, len(fp))
	for key, value := range fp {
		if clean := util.Clean(value); clean != "" {
			stringsMap[strings.ToLower(strings.TrimSpace(key))] = clean
		}
	}
	return stringsMap
}

func BrowserHeadersForFingerprint(raw any) map[string]string {
	values := BrowserFingerprintStringMap(raw)
	headers := map[string]string{
		"User-Agent":                  firstNonEmpty(values["user-agent"], DefaultBrowserUserAgent),
		"Sec-Ch-Ua":                   firstNonEmpty(values["sec-ch-ua"], DefaultBrowserSecCHUA),
		"Sec-Ch-Ua-Mobile":            firstNonEmpty(values["sec-ch-ua-mobile"], DefaultBrowserSecCHUAMobile),
		"Sec-Ch-Ua-Platform":          firstNonEmpty(values["sec-ch-ua-platform"], DefaultBrowserSecCHUAPlatform),
		"Sec-Ch-Ua-Arch":              firstNonEmpty(values["sec-ch-ua-arch"], DefaultBrowserSecCHUAArch),
		"Sec-Ch-Ua-Bitness":           firstNonEmpty(values["sec-ch-ua-bitness"], DefaultBrowserSecCHUABitness),
		"Sec-Ch-Ua-Full-Version":      firstNonEmpty(values["sec-ch-ua-full-version"], DefaultBrowserSecCHUAFullVersion),
		"Sec-Ch-Ua-Full-Version-List": firstNonEmpty(values["sec-ch-ua-full-version-list"], DefaultBrowserSecCHUAFullVersionList),
		"Sec-Ch-Ua-Platform-Version":  firstNonEmpty(values["sec-ch-ua-platform-version"], DefaultBrowserSecCHUAPlatformVersion),
	}
	if value := values["oai-device-id"]; value != "" {
		headers["OAI-Device-Id"] = value
	}
	if value := values["oai-session-id"]; value != "" {
		headers["OAI-Session-Id"] = value
	}
	return headers
}

func normalizeBrowserFingerprintInput(raw any) (map[string]any, bool, bool) {
	switch input := raw.(type) {
	case map[string]any:
		if input == nil {
			return nil, false, false
		}
		normalized := make(map[string]any, len(input))
		keyNormalized := false
		for key, value := range input {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			if normalizedKey != key {
				keyNormalized = true
			}
			normalized[normalizedKey] = value
		}
		return normalized, true, keyNormalized
	case map[string]string:
		if input == nil {
			return nil, false, false
		}
		normalized := make(map[string]any, len(input))
		keyNormalized := false
		for key, value := range input {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			if normalizedKey != key {
				keyNormalized = true
			}
			normalized[normalizedKey] = strings.TrimSpace(value)
		}
		return normalized, true, keyNormalized
	default:
		return nil, false, false
	}
}

func BrowserMetadataFromUserAgent(userAgent string) browserHeaderMetadata {
	chromeVersion := browserRegexpVersion(userAgent, `Chrome/([0-9]+(?:\.[0-9]+){0,3})`)
	edgeVersion := browserRegexpVersion(userAgent, `Edg[A-Z]*/([0-9]+(?:\.[0-9]+){0,3})`)
	if edgeVersion != "" {
		edgeMajor := browserMajorVersion(edgeVersion)
		chromiumVersion := firstNonEmpty(chromeVersion, edgeVersion)
		chromiumMajor := browserMajorVersion(chromiumVersion)
		return browserHeaderMetadata{
			secCHUA:         fmt.Sprintf(`"Microsoft Edge";v="%s", "Chromium";v="%s", "Not A(Brand";v="24"`, edgeMajor, chromiumMajor),
			fullVersion:     browserNormalizeFullVersion(edgeVersion),
			fullVersionList: fmt.Sprintf(`"Microsoft Edge";v="%s", "Chromium";v="%s", "Not A(Brand";v="24.0.0.0"`, browserNormalizeFullVersion(edgeVersion), browserNormalizeFullVersion(chromiumVersion)),
		}
	}
	if chromeVersion != "" {
		major := browserMajorVersion(chromeVersion)
		full := browserNormalizeFullVersion(chromeVersion)
		return browserHeaderMetadata{
			secCHUA:         fmt.Sprintf(`"Not:A-Brand";v="99", "Google Chrome";v="%s", "Chromium";v="%s"`, major, major),
			fullVersion:     full,
			fullVersionList: fmt.Sprintf(`"Not:A-Brand";v="99.0.0.0", "Google Chrome";v="%s", "Chromium";v="%s"`, full, full),
		}
	}
	return browserHeaderMetadata{
		secCHUA:         DefaultBrowserSecCHUA,
		fullVersion:     strings.Trim(DefaultBrowserSecCHUAFullVersion, `"`),
		fullVersionList: DefaultBrowserSecCHUAFullVersionList,
	}
}

func defaultImpersonationProfileForUserAgent(userAgent string) string {
	if browserRegexpVersion(userAgent, `Edg[A-Z]*/([0-9]+(?:\.[0-9]+){0,3})`) != "" {
		return "edge" + browserMajorVersion(browserRegexpVersion(userAgent, `Edg[A-Z]*/([0-9]+(?:\.[0-9]+){0,3})`))
	}
	if chromeVersion := browserRegexpVersion(userAgent, `Chrome/([0-9]+(?:\.[0-9]+){0,3})`); chromeVersion != "" {
		return "chrome" + browserMajorVersion(chromeVersion)
	}
	return DefaultBrowserImpersonationProfile
}

func browserRegexpVersion(value, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(value)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func browserMajorVersion(version string) string {
	if before, _, ok := strings.Cut(version, "."); ok {
		return before
	}
	return version
}

func browserNormalizeFullVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return strings.Trim(DefaultBrowserSecCHUAFullVersion, `"`)
	}
	parts := strings.Split(version, ".")
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	return strings.Join(parts[:4], ".")
}

func quoteBrowserHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.Trim(DefaultBrowserSecCHUAFullVersion, `"`)
	}
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value
	}
	return `"` + value + `"`
}
