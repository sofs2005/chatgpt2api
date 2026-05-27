package service

import (
	"fmt"
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

type browserHeaderMetadata struct {
	secCHUA         string
	fullVersion     string
	fullVersionList string
}

func NewBrowserFingerprint() map[string]any {
	metadata := BrowserMetadataFromUserAgent(DefaultBrowserUserAgent)
	return map[string]any{
		"version":                     1,
		"impersonate":                 DefaultBrowserImpersonationProfile,
		"user-agent":                  DefaultBrowserUserAgent,
		"sec-ch-ua":                   metadata.secCHUA,
		"sec-ch-ua-mobile":            DefaultBrowserSecCHUAMobile,
		"sec-ch-ua-platform":          DefaultBrowserSecCHUAPlatform,
		"sec-ch-ua-arch":              DefaultBrowserSecCHUAArch,
		"sec-ch-ua-bitness":           DefaultBrowserSecCHUABitness,
		"sec-ch-ua-full-version":      quoteBrowserHeaderValue(metadata.fullVersion),
		"sec-ch-ua-full-version-list": metadata.fullVersionList,
		"sec-ch-ua-platform-version":  DefaultBrowserSecCHUAPlatformVersion,
		"oai-device-id":               util.NewUUID(),
		"oai-session-id":              util.NewUUID(),
	}
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
