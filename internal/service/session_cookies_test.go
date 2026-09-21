package service

import (
	"reflect"
	"testing"
	"time"
)

// 挑战凭证（cf_clearance）在账号未绑定出口代理时，只在保守窗口内发送；
// 短期令牌 __cf_bm 按自身生命周期过期；长期标识 _cfuvid 不受时间窗口约束。
func TestAccountSessionCookiesForRequestClassifiesCloudflareCookies(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	account := map[string]any{
		"session_cookies": map[string]string{
			"cf_clearance": "stale-cf",
			"__cf_bm":      "fresh-bm",
			"_cfuvid":      "unknown-visitor",
			"oai-did":      "did-cookie",
			"oai-sc":       "sc-cookie",
		},
		"session_cookie_updated_at": map[string]string{
			"cf_clearance": now.Add(-3 * time.Hour).Format(time.RFC3339),
			"__cf_bm":      now.Add(-5 * time.Minute).Format(time.RFC3339),
		},
	}

	cookies := AccountSessionCookiesForRequest(account, now)
	want := map[string]string{
		"__cf_bm": "fresh-bm",
		"_cfuvid": "unknown-visitor",
		"oai-did": "did-cookie",
		"oai-sc":  "sc-cookie",
	}
	if !reflect.DeepEqual(cookies, want) {
		t.Fatalf("AccountSessionCookiesForRequest() = %#v, want %#v", cookies, want)
	}
}

// 账号绑定固定出口代理后，cf_clearance 的 IP 前提成立，不再按时间丢弃。
func TestAccountSessionCookiesForRequestKeepsClearanceWithStableProxy(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	account := map[string]any{
		"proxy": "http://127.0.0.1:8080",
		"session_cookies": map[string]string{
			"cf_clearance": "old-but-bound-cf",
			"oai-did":      "did-cookie",
		},
		"session_cookie_updated_at": map[string]string{
			"cf_clearance": now.Add(-30 * time.Hour).Format(time.RFC3339),
		},
	}

	cookies := AccountSessionCookiesForRequest(account, now)
	want := map[string]string{
		"cf_clearance": "old-but-bound-cf",
		"oai-did":      "did-cookie",
	}
	if !reflect.DeepEqual(cookies, want) {
		t.Fatalf("AccountSessionCookiesForRequest() = %#v, want %#v", cookies, want)
	}
}

// 窗口内的 cf_clearance 必须继续发送，避免把仍有效的通行证误丢。
func TestAccountSessionCookiesForRequestKeepsFreshClearance(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	account := map[string]any{
		"session_cookies": map[string]string{
			"cf_clearance": "fresh-cf",
			"oai-did":      "did-cookie",
		},
		"session_cookie_updated_at": map[string]string{
			"cf_clearance": now.Add(-31 * time.Minute).Format(time.RFC3339),
		},
	}

	cookies := AccountSessionCookiesForRequest(account, now)
	if cookies["cf_clearance"] != "fresh-cf" {
		t.Fatalf("cf_clearance = %q, want it kept inside the clearance window", cookies["cf_clearance"])
	}
}

func TestCloudflareCookieFreshWindowClassification(t *testing.T) {
	tests := []struct {
		name         string
		cookie       string
		stableExitIP bool
		wantLimited  bool
		wantWindow   time.Duration
	}{
		{name: "clearance without stable ip", cookie: "cf_clearance", wantLimited: true, wantWindow: cloudflareClearanceWindow},
		{name: "clearance with stable ip", cookie: "cf_clearance", stableExitIP: true, wantLimited: false},
		{name: "challenge cookie", cookie: "cf_chl_2", wantLimited: true, wantWindow: cloudflareClearanceWindow},
		{name: "bot management token", cookie: "__cf_bm", wantLimited: true, wantWindow: cloudflareTokenWindow},
		{name: "visitor id is long lived", cookie: "_cfuvid", wantLimited: false},
		{name: "load balancer affinity is long lived", cookie: "__cflb", wantLimited: false},
		{name: "non cloudflare cookie", cookie: "oai-did", wantLimited: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window, limited := cloudflareCookieFreshWindow(tt.cookie, tt.stableExitIP)
			if limited != tt.wantLimited {
				t.Fatalf("limited = %v, want %v", limited, tt.wantLimited)
			}
			if limited && window != tt.wantWindow {
				t.Fatalf("window = %s, want %s", window, tt.wantWindow)
			}
		})
	}
}
