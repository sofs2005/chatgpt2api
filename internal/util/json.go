package util

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	ImageModelAuto      = "auto"
	ImageModelGPT       = "gpt-image-2"
	ImageModelCodex     = "codex-gpt-image-2"
	ImageModelGPT5      = "gpt-5"
	ImageModelGPT53Mini = "gpt-5-3-mini"
	ImageModelGPT54     = "gpt-5-4"
	ImageModelGPT55     = "gpt-5-5"
	ImageModelGPT55Mini = "gpt-5-5-mini"
	ImageModelGPT56     = "gpt-5-6"
	ImageModelGPT56Mini = "gpt-5-6-mini"
)

var ImageModels = map[string]struct{}{
	ImageModelGPT:   {},
	ImageModelCodex: {},
}

var ModelIDs = []string{
	ImageModelGPT,
	ImageModelCodex,
	ImageModelAuto,
	ImageModelGPT5,
	ImageModelGPT53Mini,
	ImageModelGPT54,
	ImageModelGPT55,
	ImageModelGPT55Mini,
	ImageModelGPT56,
	ImageModelGPT56Mini,
}

var ImageGenerationModelIDs = []string{
	ImageModelGPT,
	ImageModelCodex,
	ImageModelAuto,
}

var ImageGenerationModels = map[string]struct{}{}

func init() {
	for _, model := range ImageGenerationModelIDs {
		ImageGenerationModels[model] = struct{}{}
	}
}

var ResponsesImageToolModels = map[string]struct{}{
	ImageModelAuto:      {},
	ImageModelGPT:       {},
	ImageModelCodex:     {},
	ImageModelGPT5:      {},
	ImageModelGPT53Mini: {},
	ImageModelGPT54:     {},
	ImageModelGPT55:     {},
	ImageModelGPT55Mini: {},
	ImageModelGPT56:     {},
	ImageModelGPT56Mini: {},
}

func Clean(v any) string {
	return strings.TrimSpace(fmt.Sprint(ValueOr(v, "")))
}

func ValueOr(v any, fallback any) any {
	if v == nil {
		return fallback
	}
	return v
}

func StringMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func CopyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func AsStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s := Clean(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func AsMapSlice(v any) []map[string]any {
	switch x := v.(type) {
	case []map[string]any:
		return x
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func ToInt(v any, fallback int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		n, err := x.Int64()
		if err == nil {
			return int(n)
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err == nil {
			return n
		}
	}
	return fallback
}

func ToBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	default:
		return v != nil
	}
}

func DecodeJSON(r io.Reader, out any) error {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	return dec.Decode(out)
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func ErrorPayload(message string) map[string]any {
	return map[string]any{"error": message}
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]any{"detail": ErrorPayload(message)})
}

func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func NewHex(n int) string {
	if n <= 0 {
		n = 16
	}
	buf := make([]byte, (n+1)/2)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)[:n]
}

func SHA256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func SHA1Short(text string, n int) string {
	sum := sha1.Sum([]byte(text))
	hexed := hex.EncodeToString(sum[:])
	if n > 0 && n < len(hexed) {
		return hexed[:n]
	}
	return hexed
}

func RandomTokenURL(n int) string {
	if n <= 0 {
		n = 24
	}
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func B64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func B64Decode(text string) ([]byte, error) {
	if idx := strings.Index(text, ","); strings.HasPrefix(text, "data:") && idx >= 0 {
		text = text[idx+1:]
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(text))
}

func CompactJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return string(data)
	}
	return buf.String()
}

// DisplayTimeLayout 是后台面板展示时间戳的格式，也是各服务落库时间的格式。
const DisplayTimeLayout = "2006-01-02 15:04:05"

// DisplayTimeZoneName 是后台面板展示时间戳所用时区。
//
// 面板面向中文用户，时间一律按东八区展示，与部署机、容器 TZ 无关：
// 容器没设 TZ 时是 UTC，运维照着 UTC 时间找日志会白费半天。写死时区而不是
// 跟随 time.Local，行为才可预测、可测试。
//
// 注意：这里只管「给人看的时间」。发往上游的身份时区是另一件事，
// 见 OutboundTimeZone。
const DisplayTimeZoneName = "Asia/Shanghai"

// DisplayTimeZone 是 DisplayTimeZoneName 对应的 Location。
var DisplayTimeZone = time.FixedZone(DisplayTimeZoneName, 8*3600)

// OutboundTimeZoneName 是发往上游请求体 timezone 字段的 IANA 时区名。
//
// 它与 OutboundLocaleTag 必须指向同一个地区：语言报 zh-CN、时区报
// America/Los_Angeles 这种组合真实浏览器不会产生——浏览器语言设置与系统
// 时区虽相互独立，但同一份身份不会既在中国时区又只认英文。风控把这种
// 跨地区的组合当作脚本特征。
const OutboundTimeZoneName = "Asia/Shanghai"

// OutboundLocaleTag 是发往上游的身份语言（navigator.language / OAI-Language）。
const OutboundLocaleTag = "zh-CN"

// OutboundLocaleList 是 navigator.languages 的取值。
const OutboundLocaleList = "zh-CN,zh,en"

// OutboundAcceptLanguage 是出站请求统一的 Accept-Language。
//
// 真实浏览器的 Accept-Language 来自浏览器语言设置，对同一份浏览器身份的
// 所有请求取值相同。它必须与 OAI-Language、PoW 配置里的 navigator.language
// 保持一致，否则同一份身份会自报不同语言。
const OutboundAcceptLanguage = "zh-CN,zh;q=0.9,en;q=0.8"

// WebAcceptLanguage 是 chatgpt.com 主站 API 请求的 Accept-Language。
//
// 比 OutboundAcceptLanguage 多一个 en-US 备选，与实抓的网页端请求头逐字一致。
// 单独列出而不是复用统一值，是为了让主站请求头与已知可用状态保持完全相同。
const WebAcceptLanguage = "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7"

// RegisterAcceptLanguage 是注册/登录流程的 Accept-Language。
//
// 该流程打的是 OpenAI 认证域而非 chatgpt.com 主站，两者是不同上游：
// 认证域一直使用英文，不要与主站的中文身份混为一谈。
const RegisterAcceptLanguage = "en-US,en;q=0.9"

// outboundTimeZoneOffsetSeconds 是东八区偏移：中国不实行夏令时，全年恒定。
const outboundTimeZoneOffsetSeconds = 8 * 3600

// OutboundTimeZone 返回发往上游的身份时区。
//
// 写死偏移而不是 time.LoadLocation("Asia/Shanghai")：后者依赖运行环境里的
// tzdata，精简镜像缺这份数据时会静默回退到 UTC，把偏移配上 UTC 的本地时间串。
func OutboundTimeZone() *time.Location {
	return time.FixedZone(OutboundTimeZoneName, outboundTimeZoneOffsetSeconds)
}

// OutboundTimeZoneOffsetMinutes 返回发往上游 timezone_offset_min 的取值。
//
// 取 -480 而不是 +480，与实抓一致：jshook/docs/api-endpoints.md 的原始实抓
// （2026-05-07）记录 `timezone: "Asia/Shanghai"` 配 `timezone_offset_min: -480`。
// 文档 request-completion-flow.md 里的 `-new Date().getTimezoneOffset()` 是
// 另一条更早的推测性记录，与实抓值互相冲突且从未对拍过；在有新的实抓之前
// 以实抓值为准。改动此值前先按 AGENTS.md 重新抓一份。
func OutboundTimeZoneOffsetMinutes() int {
	return -outboundTimeZoneOffsetSeconds / 60
}

func NowLocal() string {
	return time.Now().In(DisplayTimeZone).Format(DisplayTimeLayout)
}

func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func AnonymizeToken(token any) string {
	value := Clean(token)
	if value == "" {
		return "token:empty"
	}
	return "token:" + SHA256Hex(value)[:10]
}

func ParseCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func IsImageModel(model string) bool {
	_, ok := ImageModels[strings.TrimSpace(model)]
	return ok
}

func IsImageGenerationModel(model string) bool {
	_, ok := ImageGenerationModels[strings.TrimSpace(model)]
	return ok
}

func IsResponsesImageToolModel(model string) bool {
	_, ok := ResponsesImageToolModels[strings.TrimSpace(model)]
	return ok
}

func ModelList() []string {
	return append([]string(nil), ModelIDs...)
}

func ImageGenerationModelList() []string {
	return append([]string(nil), ImageGenerationModelIDs...)
}

func ImageGenerationModelNames() string {
	return strings.Join(ImageGenerationModelIDs, ", ")
}
