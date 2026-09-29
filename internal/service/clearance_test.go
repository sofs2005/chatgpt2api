package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClearanceConfig struct {
	enabled bool
	url     string
	timeout int
	ttl     int
}

func (c testClearanceConfig) ClearanceEnabled() bool       { return c.enabled }
func (c testClearanceConfig) FlareSolverrURL() string      { return c.url }
func (c testClearanceConfig) ClearanceTimeoutSeconds() int { return c.timeout }
func (c testClearanceConfig) ClearanceTTLSeconds() int     { return c.ttl }

// flareSolverrStub 返回一个模拟 FlareSolverr /v1 的服务端。
func flareSolverrStub(t *testing.T, calls *int32, body func(req map[string]any) map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		atomic.AddInt32(calls, 1)
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body(req))
	}))
}

func TestClearanceDisabledWithoutFlareSolverrURL(t *testing.T) {
	svc := NewClearanceService(testClearanceConfig{enabled: true})
	if svc.Enabled() {
		t.Fatal("Enabled() = true without flaresolverr url")
	}
	if _, err := svc.Refresh(context.Background(), ""); err == nil {
		t.Fatal("Refresh() should fail when disabled")
	}
}

// 挑战成波出现，同一出口只允许一次求解在途，否则 FlareSolverr 会被打爆。
func TestClearanceSingleFlightPerProxy(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"userAgent":"UA/1","cookies":[{"name":"cf_clearance","value":"cf-1","expires":0}]}}`))
	}))
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, err := svc.Refresh(context.Background(), "socks5://127.0.0.1:1080"); err != nil {
				t.Errorf("Refresh() error = %v", err)
			}
		}()
	}
	// 等所有调用进入在途状态后再放行。
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("flaresolverr calls = %d, want 1 (single-flight per proxy)", got)
	}
}

func TestClearanceCacheKeyedByProxy(t *testing.T) {
	var calls int32
	server := flareSolverrStub(t, &calls, func(req map[string]any) map[string]any {
		return map[string]any{"status": "ok", "solution": map[string]any{
			"userAgent": "UA/1",
			"cookies":   []any{map[string]any{"name": "cf_clearance", "value": "cf-1"}},
		}}
	})
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	ctx := context.Background()
	if _, err := svc.Refresh(ctx, "socks5://a:1080"); err != nil {
		t.Fatalf("Refresh(a) error = %v", err)
	}
	// 同出口命中缓存。
	if _, err := svc.Refresh(ctx, "socks5://a:1080"); err != nil {
		t.Fatalf("Refresh(a) cached error = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls after cache hit = %d, want 1", got)
	}
	// cf_clearance 绑定出口 IP，不同出口必须各自求解。
	if _, err := svc.Refresh(ctx, "socks5://b:1080"); err != nil {
		t.Fatalf("Refresh(b) error = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls after different proxy = %d, want 2", got)
	}
}

func TestClearanceInvalidateDropsCache(t *testing.T) {
	var calls int32
	server := flareSolverrStub(t, &calls, func(req map[string]any) map[string]any {
		return map[string]any{"status": "ok", "solution": map[string]any{
			"userAgent": "UA/1",
			"cookies":   []any{map[string]any{"name": "cf_clearance", "value": "cf-1"}},
		}}
	})
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	ctx := context.Background()
	if _, err := svc.Refresh(ctx, "socks5://a:1080"); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	svc.Invalidate("socks5://a:1080", ClearanceTargetHost)
	if _, err := svc.Refresh(ctx, "socks5://a:1080"); err != nil {
		t.Fatalf("Refresh() after invalidate error = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2 after invalidate", got)
	}
}

// 只回注 Cloudflare 自己签发的凭证，账号级 cookie 必须原样保留本地副本。
func TestClearanceKeepsOnlyCloudflareCookies(t *testing.T) {
	var calls int32
	server := flareSolverrStub(t, &calls, func(req map[string]any) map[string]any {
		return map[string]any{"status": "ok", "solution": map[string]any{
			"userAgent": "UA/1",
			"cookies": []any{
				map[string]any{"name": "cf_clearance", "value": "cf-1"},
				map[string]any{"name": "__cf_bm", "value": "bm-1"},
				map[string]any{"name": "_cfuvid", "value": "uv-1"},
				map[string]any{"name": "__cflb", "value": "lb-1"},
				map[string]any{"name": "oai-sc", "value": "should-be-dropped"},
				map[string]any{"name": "__Secure-next-auth.session-token.0", "value": "dropped"},
			},
		}}
	})
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	bundle, err := svc.Refresh(context.Background(), "")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	for _, want := range []string{"cf_clearance", "__cf_bm", "_cfuvid", "__cflb"} {
		if _, ok := bundle.Cookies[want]; !ok {
			t.Fatalf("missing cloudflare cookie %q in %v", want, bundle.Cookies)
		}
	}
	for _, dropped := range []string{"oai-sc", "__Secure-next-auth.session-token.0"} {
		if _, ok := bundle.Cookies[dropped]; ok {
			t.Fatalf("account cookie %q must not be taken from the browser container", dropped)
		}
	}
}

// 求解必须走触发账号的出口，否则 cf_clearance 的签发 IP 与使用 IP 不符。
func TestClearanceForwardsProxyToFlareSolverr(t *testing.T) {
	var seen map[string]any
	var calls int32
	server := flareSolverrStub(t, &calls, func(req map[string]any) map[string]any {
		seen = req
		return map[string]any{"status": "ok", "solution": map[string]any{
			"userAgent": "UA/1",
			"cookies":   []any{map[string]any{"name": "cf_clearance", "value": "cf-1"}},
		}}
	})
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	if _, err := svc.Refresh(context.Background(), "socks5://user:pass@127.0.0.1:1080"); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if seen["url"] != "https://chatgpt.com/" {
		t.Fatalf("url = %v", seen["url"])
	}
	proxy, ok := seen["proxy"].(map[string]any)
	if !ok || proxy["url"] != "socks5://user:pass@127.0.0.1:1080" {
		t.Fatalf("proxy = %v, want the triggering account's egress", seen["proxy"])
	}
}

// 浏览器容器没有真的过挑战时不能写缓存，否则后续请求会拿着垃圾凭证重试。
func TestClearanceRejectsResponseWithoutCookies(t *testing.T) {
	var calls int32
	server := flareSolverrStub(t, &calls, func(req map[string]any) map[string]any {
		return map[string]any{"status": "ok", "solution": map[string]any{"userAgent": "UA/1", "cookies": []any{}}}
	})
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	if _, err := svc.Refresh(context.Background(), ""); err == nil {
		t.Fatal("Refresh() should fail when no cloudflare cookie is returned")
	}
	if _, ok := svc.Cached("", ClearanceTargetHost); ok {
		t.Fatal("failed solve must not be cached")
	}
}

func TestClearanceSurfacesFlareSolverrError(t *testing.T) {
	var calls int32
	server := flareSolverrStub(t, &calls, func(req map[string]any) map[string]any {
		return map[string]any{"status": "error", "message": "Challenge not detected!"}
	})
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	if _, err := svc.Refresh(context.Background(), ""); err == nil {
		t.Fatal("Refresh() should fail on flaresolverr error status")
	}
}

func TestClearanceExpiresByCookieExpiry(t *testing.T) {
	var calls int32
	server := flareSolverrStub(t, &calls, func(req map[string]any) map[string]any {
		expires := float64(time.Now().Add(30 * time.Second).Unix())
		return map[string]any{"status": "ok", "solution": map[string]any{
			"userAgent": "UA/1",
			"cookies": []any{
				map[string]any{"name": "cf_clearance", "value": "cf-1", "expires": expires},
				map[string]any{"name": "__cf_bm", "value": "bm-1", "expires": float64(time.Now().Add(10 * time.Hour).Unix())},
			},
		}}
	})
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	bundle, err := svc.Refresh(context.Background(), "")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	// 取最早到期的 cookie 作为缓存上限，避免用中途过期的凭证重试。
	if remaining := time.Until(bundle.ExpiresAt); remaining > time.Minute {
		t.Fatalf("ExpiresAt = %v, want bounded by the earliest cookie expiry", bundle.ExpiresAt)
	}
}

func TestClearanceRejectsAlreadyExpiredCookie(t *testing.T) {
	var calls int32
	server := flareSolverrStub(t, &calls, func(req map[string]any) map[string]any {
		return map[string]any{"status": "ok", "solution": map[string]any{
			"userAgent": "UA/1",
			"cookies": []any{
				map[string]any{"name": "cf_clearance", "value": "cf-1", "expires": float64(time.Now().Add(-time.Hour).Unix())},
			},
		}}
	})
	defer server.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: server.URL, ttl: 3600})
	if _, err := svc.Refresh(context.Background(), ""); err == nil {
		t.Fatal("Refresh() should fail on an already expired clearance")
	}
}

func TestMaskProxyURLHidesCredentials(t *testing.T) {
	cases := map[string]string{
		"socks5://user:secret@10.0.0.1:1080": "socks5://***@10.0.0.1:1080",
		"http://10.0.0.1:8080":               "http://10.0.0.1:8080",
		"":                                   "",
		"not a url":                          "***",
	}
	for input, want := range cases {
		if got := MaskProxyURL(input); got != want {
			t.Fatalf("MaskProxyURL(%q) = %q, want %q", input, got, want)
		}
	}
	if got := MaskProxyList([]string{"socks5://u:p@h:1"}); len(got) != 1 || got[0] != "socks5://***@h:1" {
		t.Fatalf("MaskProxyList = %v", got)
	}
	if got := MaskProxyList(nil); got == nil || len(got) != 0 {
		t.Fatalf("MaskProxyList(nil) = %v, want empty slice", got)
	}
}
