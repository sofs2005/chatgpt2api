package util

import (
	"testing"
	"time"
)

// 面板时间戳必须与部署机、容器 TZ 无关，固定东八区。
func TestNowLocalUsesFixedDisplayTimeZone(t *testing.T) {
	got := NowLocal()
	if len(got) != len("2006-01-02 15:04:05") {
		t.Fatalf("NowLocal() = %q, want the display layout", got)
	}
	if DisplayTimeZoneName != "Asia/Shanghai" {
		t.Fatalf("DisplayTimeZoneName = %q, want Asia/Shanghai", DisplayTimeZoneName)
	}
	if _, offset := time.Now().In(DisplayTimeZone).Zone(); offset != 8*3600 {
		t.Fatalf("DisplayTimeZone offset = %d, want %d", offset, 8*3600)
	}
}

// 出站身份的语言与时区必须指向同一地区。
//
// 语言报 zh-CN、时区报 America/Los_Angeles 这种跨地区组合是脚本特征：
// 浏览器语言设置与系统时区虽相互独立，但同一份身份不会既在中国时区
// 又只认英文。上游对这种组合直接返回 403。
func TestOutboundIdentityStaysInOneRegion(t *testing.T) {
	if OutboundTimeZoneName != "Asia/Shanghai" {
		t.Fatalf("OutboundTimeZoneName = %q, want Asia/Shanghai", OutboundTimeZoneName)
	}
	if OutboundLocaleTag != "zh-CN" {
		t.Fatalf("OutboundLocaleTag = %q, want zh-CN", OutboundLocaleTag)
	}
	if got := OutboundTimeZone().String(); got != OutboundTimeZoneName {
		t.Fatalf("OutboundTimeZone() = %q, want %q", got, OutboundTimeZoneName)
	}
	// 东八区全年恒定，不受夏令时影响，任意时刻偏移都必须是 +480 分钟。
	for _, at := range []time.Time{
		time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	} {
		if _, offset := at.In(OutboundTimeZone()).Zone(); offset != 8*3600 {
			t.Fatalf("offset at %s = %d, want %d", at.Format("2006-01-02"), offset, 8*3600)
		}
	}
}

// timezone_offset_min 的取值必须与实抓一致。
//
// jshook/docs/api-endpoints.md 的原始实抓（2026-05-07）记录
// `timezone: "Asia/Shanghai"` 配 `timezone_offset_min: -480`。
// 该字段的符号在文档里存在两套互相矛盾的说法，改动前需重新抓包确认，
// 因此这里把当前取值钉死，避免被顺手「修正」成正数。
func TestOutboundTimeZoneOffsetMatchesCapture(t *testing.T) {
	if got := OutboundTimeZoneOffsetMinutes(); got != -480 {
		t.Fatalf("OutboundTimeZoneOffsetMinutes() = %d, want -480 (captured value)", got)
	}
}
