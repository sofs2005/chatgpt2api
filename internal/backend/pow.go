package backend

import (
	"bytes"
	"crypto/sha3"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"time"

	"chatgpt2api/internal/util"
)

const defaultPOWScript = "https://chatgpt.com/backend-api/sentinel/sdk.js"

var (
	scriptSrcRE = regexp.MustCompile(`(?is)<script[^>]+src=["']([^"']+)["']`)

	// dataBuildRE 匹配 <html data-build="prod-<sha>">。
	// 上游把构建号直接写在根元素上，这是 OAI-Client-Version 的真值来源。
	dataBuildRE = regexp.MustCompile(`(?is)<html[^>]*data-build=["']([^"']*)["']`)

	// webBuildNumberRE 匹配 statsig 载荷里的 web_build_number，即
	// OAI-Client-Build-Number 的真值来源。它只出现在 HTML 内联脚本中，
	// 且被 JSON 转义，因此两种引号形式都要覆盖。
	webBuildNumberRE = regexp.MustCompile(`web_build_number\\?"?\s*:\s*([0-9]+)`)
)

// parsePOWResources 从 bootstrap 的 HTML 中提取 PoW 需要的脚本源与构建标识。
//
// dataBuild 同时用于 OAI-Client-Version 头与 PoW 配置数组，因此这两个位置
// 必须取自同一份 HTML：上游边缘同时在线多个构建，若各取各的就会拼出
// 「头说 A 版本、指纹说 B 版本」这种服务端可识别的矛盾身份。
func parsePOWResources(html string) ([]string, string) {
	matches := scriptSrcRE.FindAllStringSubmatch(html, -1)
	sources := make([]string, 0, len(matches))
	dataBuild := ""
	for _, match := range matches {
		src := match[1]
		sources = append(sources, src)
		if dataBuild == "" {
			if hit := regexp.MustCompile(`c/[^/]*/_`).FindString(src); hit != "" {
				dataBuild = hit
			}
		}
	}
	if len(sources) == 0 {
		sources = []string{defaultPOWScript}
	}
	if dataBuild == "" {
		if match := dataBuildRE.FindStringSubmatch(html); len(match) > 1 {
			dataBuild = match[1]
		}
	}
	return sources, dataBuild
}

// parseWebBuildNumber 从 bootstrap 的 HTML 中提取 statsig 的 web_build_number。
// 找不到时返回空串，调用方应保留原有构建号而不是写入空值。
func parseWebBuildNumber(html string) string {
	if match := webBuildNumberRE.FindStringSubmatch(html); len(match) > 1 {
		return match[1]
	}
	return ""
}

func buildLegacyRequirementsToken(userAgent string, scriptSources []string, dataBuild string) string {
	config := buildPOWConfig(userAgent, scriptSources, dataBuild)
	return "gAAAAAC" + base64.StdEncoding.EncodeToString(mustMarshal(config))
}

func buildProofToken(seed, difficulty, userAgent string, scriptSources []string, dataBuild string) (string, error) {
	config := buildPOWConfig(userAgent, scriptSources, dataBuild)
	answer, solved := powGenerate(seed, difficulty, config, 500000)
	if !solved {
		return "", fmt.Errorf("failed to solve proof token: difficulty=%s", difficulty)
	}
	// Upstream appends the "~S" marker to every solved proof token: it shows up on
	// the finalize request body and on the OpenAI-Sentinel-Proof-Token header of
	// every ping/conversation call. The requirements token (gAAAAAC) has no marker.
	return "gAAAAAB" + answer + "~S", nil
}

func buildPOWConfig(userAgent string, scriptSources []string, dataBuild string) []any {
	if len(scriptSources) == 0 {
		scriptSources = []string{defaultPOWScript}
	}
	windowKeys := []string{
		"0", "window", "self", "document", "name", "location", "customElements", "history", "navigation",
		"innerWidth", "innerHeight", "scrollX", "scrollY", "visualViewport", "screenX", "screenY", "outerWidth",
		"outerHeight", "devicePixelRatio", "screen", "chrome", "navigator", "onresize", "performance", "crypto",
		"indexedDB", "sessionStorage", "localStorage", "scheduler", "alert", "atob", "btoa", "fetch", "matchMedia",
		"postMessage", "queueMicrotask", "requestAnimationFrame", "setInterval", "setTimeout", "caches",
		"__NEXT_DATA__", "__BUILD_MANIFEST", "__NEXT_PRELOADREADY",
	}
	documentKeys := []string{"__reactContainer$fzelfjyxej8", "_reactListening5dehydibo78", "location"}
	cores := []int{8, 16, 24, 32}
	// 先定硬件核数，再据此构造 navigator 探针。
	// 若二者独立随机，会出现「navigator.hardwareConcurrency 报 32、但载荷核数字段是 8」
	// 这种同一份配置内部自相矛盾的组合。
	core := randomChoiceInt(cores)
	navigatorKeys := powNavigatorKeys(core)
	screenResolutions := [][2]int{{1920, 1080}, {1440, 900}, {2560, 1440}, {3840, 2160}}
	resolution := screenResolutions[rand.Intn(len(screenResolutions))]
	now := time.Now()
	// index 13 对应浏览器 performance.now()：页面加载以来的单调毫秒数。
	// 这里用进程启动以来的毫秒数近似，避免填入绝对 Unix 毫秒（量级差 7 个数量级）。
	pageUptimeMillis := float64(time.Since(powProcessStart).Nanoseconds()) / 1e6
	return []any{
		resolution[0] + resolution[1],
		powLocalTimeString(now),
		4294705152,
		1,
		userAgent,
		randomChoice(scriptSources),
		dataBuild,
		powLocaleTag,
		powLocaleList,
		rand.Float64(),
		randomChoice(navigatorKeys),
		randomChoice(documentKeys),
		randomChoice(windowKeys),
		pageUptimeMillis,
		util.NewUUID(),
		"",
		core,
		// index 17 对应秒级时间戳（与 index 13 的单调毫秒不同，是绝对时间）。
		float64(now.Unix()),
		0, 0, 0, 0, 0, 0,
		0, // 0 = edge/chrome, 1 = firefox
	}
}

// PoW 载荷中的语言与时间必须与请求其余部分保持同一身份。
//
// 请求体用 util.OutboundTimeZoneName / util.OutboundTimeZoneOffsetMinutes(now)，
// 请求头用 util.OutboundLocaleTag / util.OutboundAcceptLanguage；此处三者同源。
// 若任何一处回落到别的语言或时区（哪怕是偏移量写死导致的换季错位），
// 同一份身份就会自报不同的语言与时区，构成可识别的矛盾信号。
const (
	powLocaleTag  = util.OutboundLocaleTag
	powLocaleList = util.OutboundLocaleList
)

// outboundTimezoneOffsetMinutes 返回当前时刻发往上游的 UTC 偏移（分钟）。
//
// 必须按时刻算而不是写死：太平洋时区换季时会从 -480 变成 -420，
// 写死一个值会让「timezone 名」与「偏移量」在换季后互相矛盾。
func outboundTimezoneOffsetMinutes() int {
	return util.OutboundTimeZoneOffsetMinutes(time.Now())
}

// powProcessStart 是 PoW 载荷中 performance.now 近似的单调基准。
var powProcessStart = time.Now()

// powLocalTimeString 生成与请求时区一致的本地时间字符串。
func powLocalTimeString(now time.Time) string {
	local := now.In(util.PacificTimeZone(now))
	_, offset := local.Zone()
	label := "Pacific Standard Time"
	if offset != -8*3600 {
		label = "Pacific Daylight Time"
	}
	return local.Format("Mon Jan 02 2006 15:04:05") + " " + formatGMTOffset(offset) + " (" + label + ")"
}

// formatGMTOffset 把秒级偏移格式化成 "GMT-0700" 这样的字符串。
func formatGMTOffset(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	return fmt.Sprintf("GMT%s%02d%02d", sign, offsetSeconds/3600, (offsetSeconds%3600)/60)
}

// powNavigatorKeys 构造 navigator 探针池，使 hardwareConcurrency 探针的取值
// 与载荷核数字段一致。
func powNavigatorKeys(core int) []string {
	keys := []string{
		"registerProtocolHandler−function registerProtocolHandler() { [native code] }",
		"storage−[object StorageManager]", "locks−[object LockManager]", "appCodeName−Mozilla",
		"permissions−[object Permissions]", "share−function share() { [native code] }", "webdriver−false",
		"managed−[object NavigatorManagedData]", "canShare−function canShare() { [native code] }",
		"vendor−Google Inc.", "mediaDevices−[object MediaDevices]", "vibrate−function vibrate() { [native code] }",
		"storageBuckets−[object StorageBucketManager]", "mediaCapabilities−[object MediaCapabilities]",
		"cookieEnabled−true", "virtualKeyboard−[object VirtualKeyboard]", "product−Gecko",
		"presentation−[object Presentation]", "onLine−true", "mimeTypes−[object MimeTypeArray]",
		"credentials−[object CredentialsContainer]", "serviceWorker−[object ServiceWorkerContainer]",
		"keyboard−[object Keyboard]", "gpu−[object GPU]", "doNotTrack", "serial−[object Serial]",
		"pdfViewerEnabled−true", "language−" + powLocaleTag, "geolocation−[object Geolocation]",
		"userAgentData−[object NavigatorUAData]", "getUserMedia−function getUserMedia() { [native code] }",
		"sendBeacon−function sendBeacon() { [native code] }",
		fmt.Sprintf("hardwareConcurrency−%d", core),
		"windowControlsOverlay−[object WindowControlsOverlay]",
	}
	return keys
}

func powGenerate(seed, difficulty string, config []any, limit int) (string, bool) {
	target, err := hex.DecodeString(difficulty)
	if err != nil {
		target = []byte{0x0f, 0xff, 0xff}
	}
	diffLen := len(difficulty) / 2
	seedBytes := []byte(seed)
	part1 := mustMarshal(config[:3])
	part1 = append(part1[:len(part1)-1], ',')
	part2 := mustMarshal(config[4:9])
	part2 = append([]byte(","), part2[1:len(part2)-1]...)
	part2 = append(part2, ',')
	part3 := mustMarshal(config[10:])
	part3 = append([]byte(","), part3[1:]...)
	for i := 0; i < limit; i++ {
		finalJSON := bytes.Join([][]byte{
			part1,
			[]byte(fmt.Sprint(i)),
			part2,
			[]byte(fmt.Sprint(i >> 1)),
			part3,
		}, nil)
		encoded := []byte(base64.StdEncoding.EncodeToString(finalJSON))
		hash := sha3.Sum512(append(seedBytes, encoded...))
		if bytes.Compare(hash[:diffLen], target) <= 0 {
			return string(encoded), true
		}
	}
	return "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + base64.StdEncoding.EncodeToString([]byte(`"`+seed+`"`)), false
}

func mustMarshal(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

func randomChoice(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[rand.Intn(len(items))]
}

func randomChoiceInt(items []int) int {
	if len(items) == 0 {
		return 0
	}
	return items[rand.Intn(len(items))]
}
