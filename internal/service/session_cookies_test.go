package service

import (
	"reflect"
	"testing"
	"time"
)

func TestAccountSessionCookiesForRequestSkipsStaleCloudflareCookies(t *testing.T) {
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
			"cf_clearance": now.Add(-31 * time.Minute).Format(time.RFC3339),
			"__cf_bm":      now.Add(-5 * time.Minute).Format(time.RFC3339),
		},
	}

	cookies := AccountSessionCookiesForRequest(account, now)
	want := map[string]string{
		"__cf_bm": "fresh-bm",
		"oai-did": "did-cookie",
		"oai-sc":  "sc-cookie",
	}
	if !reflect.DeepEqual(cookies, want) {
		t.Fatalf("AccountSessionCookiesForRequest() = %#v, want %#v", cookies, want)
	}
}
