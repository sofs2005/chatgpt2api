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

// 本客户端自有路由的模型名。文本模型不在此列：它们由上游实时列表提供
// （见 protocol.Engine.ListChatModels），写死具体版本号只会在上游改名后过期。
const (
	ImageModelAuto  = "auto"
	ImageModelGPT   = "gpt-image-2"
	ImageModelCodex = "codex-gpt-image-2"
)

var ImageModels = map[string]struct{}{
	ImageModelGPT:   {},
	ImageModelCodex: {},
}

// ModelIDs 是 /v1/models 的兜底清单，只列本客户端自有路由的图片模型。
// 文本模型来自上游，不在本地重复声明。
var ModelIDs = []string{
	ImageModelGPT,
	ImageModelCodex,
	ImageModelAuto,
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

// IsOptionalModel reports whether model is a plain text model the client may pass
// through to upstream /f/conversation.
//
// The check is deliberately a shape test rather than a fixed list: upstream renames
// these models every few weeks (gpt-5-5 → gpt-6), so a hardcoded list is stale the
// moment it is written. The shape also keeps upstream-internal codenames out of the
// picker — slugs like "i-5-mini-m" appear in server metadata but are not user models.
// The image tool slugs (gpt-image-2, codex-gpt-image-2) are excluded: they route to
// the image pipeline, not to plain conversation.
func IsOptionalModel(model string) bool {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" || trimmed == ImageModelGPT || trimmed == ImageModelCodex {
		return false
	}
	parts := strings.Split(trimmed, "-")
	if len(parts) < 2 || parts[0] != "gpt" {
		return false
	}
	// Version segment must start with a digit (5, 6, 5.1), not an internal letter code.
	if parts[1] == "" || parts[1][0] < '0' || parts[1][0] > '9' {
		return false
	}
	for _, part := range parts[2:] {
		if part == "" {
			return false
		}
		for _, r := range part {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' {
				return false
			}
		}
	}
	return true
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

// OutboundTimeZoneName 是发往上游的身份时区名。
//
// 上游请求体里的 timezone、PoW 探针的本地时间、请求头语言三者必须同源自洽，
// 否则同一份浏览器身份会自报互相矛盾的语言与时区，构成风控可识别的信号。
// 美国太平洋时区在语言上对应 en-US（见 OutboundLocaleTag）。
//
// 用它而不是 time.LoadLocation("America/Los_Angeles")：后者依赖运行环境里的
// tzdata，精简镜像缺这份数据时会静默回退到 UTC，把 -480 配上 UTC 的本地时间串。
const OutboundTimeZoneName = "America/Los_Angeles"

// OutboundLocaleTag 是发往上游的身份语言（navigator.language / OAI-Language）。
const OutboundLocaleTag = "en-US"

// OutboundLocaleList 是 navigator.languages 的取值。
const OutboundLocaleList = "en-US,en"

// OutboundAcceptLanguage 是出站请求统一的 Accept-Language。
//
// 真实浏览器的 Accept-Language 来自浏览器语言设置，对同一份浏览器身份的
// 所有请求取值相同。它必须与 OAI-Language、PoW 配置里的 navigator.language
// 保持一致，否则同一份身份会自报不同语言。
const OutboundAcceptLanguage = "en-US,en;q=0.9"

// PacificTimeZone 返回给定时刻美国太平洋时区（PST/PDT）的 Location。
//
// 夏令时偏移随日期变化：3 月第二个周日到 11 月第一个周日之间是 PDT(-420)，
// 其余时间是 PST(-480)。时区名沿用 IANA 的 PST8PDT，Go 对它的已知行为是
// 「名字恒为 PST8PDT、偏移按美国规则换算」——与上游前端 Date.toString() 给出的
// "GMT-0700 (Pacific Daylight Time)" 在关键部分（偏移）一致，因此可以直接用。
//
// 不用 time.LoadLocation 的理由见 OutboundTimeZoneName 的注释。
func PacificTimeZone(t time.Time) *time.Location {
	year := t.Year()
	// 切换时刻按 UTC 钉死，避免「切换当天凌晨」那一两小时判错：
	// 开始于 3 月第二个周日 02:00 PST（=10:00 UTC），
	// 结束于 11 月第一个周日 02:00 PDT（=09:00 UTC）。
	start := nthSunday(year, time.March, 2).Add(10 * time.Hour)
	end := nthSunday(year, time.November, 1).Add(9 * time.Hour)
	utc := t.UTC()
	if !utc.Before(start) && utc.Before(end) {
		return time.FixedZone("PDT", -7*3600)
	}
	return time.FixedZone("PST", -8*3600)
}

// nthSunday 返回某年第 n 个周日的 UTC 日期（仅日期参与比较）。
func nthSunday(year int, month time.Month, n int) time.Time {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	offset := (7 - int(first.Weekday())) % 7
	return first.AddDate(0, 0, offset+7*(n-1))
}

// OutboundTimeZoneOffsetMinutes 返回给定时刻发往上游的 UTC 偏移（分钟）。
func OutboundTimeZoneOffsetMinutes(t time.Time) int {
	_, offset := t.In(PacificTimeZone(t)).Zone()
	return offset / 60
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

// IsResponsesImageToolModel reports whether model is acceptable as the image_generation
// tool model on /v1/responses.
//
// It accepts the client's own image slugs (auto, gpt-image-2, codex-gpt-image-2) plus any
// user-facing text model: responseImageGenerationModel maps text models onto the official
// image flow. Like IsOptionalModel this is a shape test, so it follows upstream renames
// (gpt-5-5 → gpt-6) without a release.
func IsResponsesImageToolModel(model string) bool {
	trimmed := strings.TrimSpace(model)
	if _, ok := ImageGenerationModels[trimmed]; ok {
		return true
	}
	return IsOptionalModel(trimmed)
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
