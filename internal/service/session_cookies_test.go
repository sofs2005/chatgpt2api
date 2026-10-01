package service

import (
	"net/http"
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

// ApplyResponseCookies 处理的是响应轮换下来的 cookie。两条容易写错的边界：
// 删除指令（MaxAge<0 或空值）必须真的把 cookie 去掉，而不是写成空值发出去；
// 非 Cloudflare 的 cookie（session-token 分片）不该被打上新鲜度时间戳。
func TestApplyResponseCookiesHandlesDeletionAndScope(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	current := map[string]string{
		"cf_clearance":                       "old-cf",
		"__Secure-next-auth.session-token.0": "stale-chunk-0",
		"oai-did":                            "device-1",
	}

	merged, updatedAt := ApplyResponseCookies(current, []*http.Cookie{
		{Name: "__Secure-next-auth.session-token.0", Value: "rotated-chunk-0"},
		{Name: "cf_clearance", Value: "new-cf"},
		{Name: "oai-did", MaxAge: -1},
		{Name: "irrelevant", Value: "ignored"},
	}, now)

	if merged["__Secure-next-auth.session-token.0"] != "rotated-chunk-0" {
		t.Fatalf("session cookie not rotated: %#v", merged)
	}
	if merged["cf_clearance"] != "new-cf" {
		t.Fatalf("cf_clearance not rotated: %#v", merged)
	}
	if _, ok := merged["oai-did"]; ok {
		t.Fatalf("deleted cookie still present: %#v", merged)
	}
	if _, ok := merged["irrelevant"]; ok {
		t.Fatalf("non-whitelisted cookie was stored: %#v", merged)
	}
	// 只有 CF 凭证进入新鲜度记录；session-token 没有窗口可谈。
	if _, ok := updatedAt["__Secure-next-auth.session-token.0"]; ok {
		t.Fatalf("session cookie should not be stamped: %#v", updatedAt)
	}
	if updatedAt["cf_clearance"] != now.UTC().Format(time.RFC3339) {
		t.Fatalf("cf_clearance stamp = %#v", updatedAt)
	}
}

// 没有轮换的输入保持原集合与 nil，让调用方原样保留旧记录。
func TestApplyResponseCookiesReturnsOriginalWithoutChanges(t *testing.T) {
	current := map[string]string{"cf_clearance": "cf", "oai-did": "d"}

	merged, updatedAt := ApplyResponseCookies(current, nil, time.Now())
	if !reflect.DeepEqual(merged, current) || updatedAt != nil {
		t.Fatalf("no-op case = %#v / %#v", merged, updatedAt)
	}
	if _, ok := merged["oai-did"]; !ok {
		t.Fatalf("merged = %#v", merged)
	}

	// 上游重复下发同一个 cf_clearance 也算一次确认，时间戳重新起算——
	// 与 rememberSessionCookies 的语义一致，窗口不因「值没变」而白白流逝。
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	merged, updatedAt = ApplyResponseCookies(current, []*http.Cookie{{Name: "cf_clearance", Value: "cf"}}, now)
	if updatedAt["cf_clearance"] != now.UTC().Format(time.RFC3339) {
		t.Fatalf("re-sent cookie should restamp: %#v", updatedAt)
	}
	if merged["cf_clearance"] != "cf" {
		t.Fatalf("merged = %#v", merged)
	}
}
