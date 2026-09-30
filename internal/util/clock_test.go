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

// 太平洋时区必须按日期在 PST(-480) 与 PDT(-420) 之间切换，
// 写死单一偏移会在换季时让请求体与 PoW 探针自报的时区互相矛盾。
func TestPacificTimeZoneFollowsDaylightSaving(t *testing.T) {
	cases := []struct {
		name       string
		year       int
		month      int
		day        int
		hourUTC    int
		wantOffset int
	}{
		{"冬季为 PST", 2026, 1, 15, 12, -480},
		{"夏季为 PDT", 2026, 7, 15, 12, -420},
		{"春季切换前一日仍是 PST", 2026, 3, 7, 12, -480},
		{"春季切换当日进入 PDT", 2026, 3, 8, 12, -420},
		{"秋季切换当日 08:00 UTC 仍是 PDT", 2026, 11, 1, 8, -420},
		{"秋季切换当日 12:00 UTC 已是 PST", 2026, 11, 1, 12, -480},
		{"秋季切换后一日是 PST", 2026, 11, 2, 12, -480},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(tc.year, time.Month(tc.month), tc.day, tc.hourUTC, 0, 0, 0, time.UTC)
			if got := OutboundTimeZoneOffsetMinutes(at); got != tc.wantOffset {
				t.Fatalf("offset at %s = %d, want %d", at.Format("2006-01-02 15:04"), got, tc.wantOffset)
			}
		})
	}
}

// 夏令时切换日期本身要落在正确的周日上：3 月第二个周日、11 月第一个周日。
func TestNthSundayPicksTheRightDay(t *testing.T) {
	cases := []struct {
		month int
		n     int
		want  string
	}{
		{3, 2, "2026-03-08"},
		{11, 1, "2026-11-01"},
	}
	for _, tc := range cases {
		got := nthSunday(2026, time.Month(tc.month), tc.n)
		if got.Format("2006-01-02") != tc.want {
			t.Fatalf("nthSunday(2026, %d, %d) = %s, want %s", tc.month, tc.n, got.Format("2006-01-02"), tc.want)
		}
		if got.Weekday() != time.Sunday {
			t.Fatalf("nthSunday(2026, %d, %d) = %s, not a Sunday", tc.month, tc.n, got.Weekday())
		}
	}
}
