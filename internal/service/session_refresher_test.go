package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionRefresherRejectsEmptySessionToken(t *testing.T) {
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("httpDo should not be called for empty session token")
		return nil, nil
	})

	_, err := refresher.RefreshSession(context.Background(), "access-token", "")
	if err == nil || !strings.Contains(err.Error(), "session_token is empty") {
		t.Fatalf("expected empty session token error, got %v", err)
	}
}

func TestSessionRefresherReturnsValidatedUser(t *testing.T) {
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z","user":{"id":"user-123","email":"user@example.com","name":"User Name"}}`)),
		}, nil
	})

	result, err := refresher.RefreshSession(context.Background(), "old-access", "old-session")
	if err != nil {
		t.Fatalf("RefreshSession() error = %v", err)
	}
	if result.AccessToken != "new-access" || result.SessionToken != "new-session" || result.Expires != "2026-05-12T00:00:00Z" {
		t.Fatalf("RefreshSession() tokens = %#v", result)
	}
	if result.User.ID != "user-123" || result.User.Email != "user@example.com" || result.User.Name != "User Name" {
		t.Fatalf("RefreshSession() user = %#v", result.User)
	}
}

func TestSessionRefresherSendsBrowserCookiesAndHeaders(t *testing.T) {
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("User-Agent"); got != "Browser UA" {
			t.Fatalf("User-Agent = %q, want Browser UA", got)
		}
		if got := req.Header.Get("Sec-Ch-Ua"); got != `"Chromium";v="145"` {
			t.Fatalf("Sec-Ch-Ua = %q", got)
		}
		if got := req.Header.Get("OAI-Device-Id"); got != "device-1" {
			t.Fatalf("OAI-Device-Id = %q", got)
		}
		for name, want := range map[string]string{
			"__Secure-next-auth.session-token": "session-cookie",
			"cf_clearance":                     "cf-cookie",
			"__cf_bm":                          "bm-cookie",
			"oai-did":                          "did-cookie",
		} {
			cookie, err := req.Cookie(name)
			if err != nil || cookie.Value != want {
				t.Fatalf("cookie %s = %#v err %v, want %q", name, cookie, err, want)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})

	_, err := refresher.RefreshSessionWithContext(context.Background(), "old-access", "session-cookie", SessionRefreshContext{
		Cookies: map[string]string{
			"cf_clearance": "cf-cookie",
			"__cf_bm":      "bm-cookie",
			"oai-did":      "did-cookie",
		},
		Headers: map[string]string{
			"User-Agent":    "Browser UA",
			"Sec-Ch-Ua":     `"Chromium";v="145"`,
			"OAI-Device-Id": "device-1",
		},
	})
	if err != nil {
		t.Fatalf("RefreshSessionWithContext() error = %v", err)
	}
}

func TestSessionRefresherDeduplicatesConcurrentRefreshes(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})

	const waiters = 5
	var wg sync.WaitGroup
	results := make(chan SessionRefreshData, waiters)
	errs := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := refresher.RefreshSession(context.Background(), "old-access", "old-session")
			results <- data
			errs <- err
		}()
	}

	waitForCondition(t, func() bool {
		return atomic.LoadInt32(&calls) == 1 && refresher.IsRefreshing("old-access")
	})
	close(release)
	wg.Wait()
	close(results)
	close(errs)

	if calls := atomic.LoadInt32(&calls); calls != 1 {
		t.Fatalf("expected one upstream refresh, got %d", calls)
	}
	if refresher.IsRefreshing("old-access") {
		t.Fatalf("refresh should be cleared after completion")
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("refresh returned error: %v", err)
		}
	}
	for result := range results {
		if result.AccessToken != "new-access" || result.SessionToken != "new-session" || result.Expires != "2026-05-12T00:00:00Z" {
			t.Fatalf("unexpected refresh result: %#v", result)
		}
	}
}

func TestSessionRefresherCarriesAccountProxyToRequest(t *testing.T) {
	var got string
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		got = AccountProxyFromContext(req.Context())
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})

	_, err := refresher.RefreshSessionWithContext(context.Background(), "old-access", "old-session", SessionRefreshContext{
		Proxy: "http://user:pass@proxy.example:8080",
	})
	if err != nil {
		t.Fatalf("RefreshSessionWithContext() error = %v", err)
	}
	// cf_clearance 与签发时的出口 IP 绑定，刷新必须从账号自己的代理发出。
	if got != "http://user:pass@proxy.example:8080" {
		t.Fatalf("account proxy = %q, want the bound proxy", got)
	}
}

func TestSessionRefresherWithoutAccountProxyLeavesContextEmpty(t *testing.T) {
	var got string
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		got = AccountProxyFromContext(req.Context())
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})

	_, err := refresher.RefreshSession(context.Background(), "old-access", "old-session")
	if err != nil {
		t.Fatalf("RefreshSession() error = %v", err)
	}
	if got != "" {
		t.Fatalf("account proxy = %q, want empty when the account binds no proxy", got)
	}
}

// profile 必须随请求抵达发送方：httpDo 拿不到 access token，
// 只能从请求上下文取账号的指纹 profile，否则会回落到硬编码的 chrome。
func TestSessionRefresherCarriesAccountProfileToRequest(t *testing.T) {
	var got string
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		got = AccountProfileFromContext(req.Context())
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})

	_, err := refresher.RefreshSessionWithContext(context.Background(), "old-access", "old-session", SessionRefreshContext{
		Profile: "firefox148",
	})
	if err != nil {
		t.Fatalf("RefreshSessionWithContext() error = %v", err)
	}
	if got != "firefox148" {
		t.Fatalf("account profile = %q, want the account's own fingerprint profile", got)
	}
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition was not met before deadline")
}

// challengeResponse 造一个 Cloudflare 挑战响应。
func challengeResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Cf-Mitigated": []string{"challenge"}},
		Body:       io.NopCloser(strings.NewReader(`<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`)),
	}
}

// 刷新链路命中 CF 挑战时必须动用兜底：session 端点与 bootstrap 一样要先过
// Cloudflare，而挑战不是靠重试能自愈的，换一份 cf_clearance 才有可能。
// 此前这条链路是唯一没有兜底的路径，同一个挑战在生图被吸收、在刷新却直接失败。
func TestSessionRefresherSolvesCloudflareChallengeWithClearance(t *testing.T) {
	const clearanceUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	var replays []http.Header
	var overrides []map[string]string
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		replays = append(replays, req.Header.Clone())
		overrides = append(overrides, IdentityOverrideFromContext(req.Context()))
		// 只有带上兜底解出的凭证才放行，这样断言的是「重放确实换了凭证」，
		// 而不是靠调用次数猜。
		if cookie, err := req.Cookie("cf_clearance"); err == nil && cookie.Value == "solved-cf" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z"}`)),
			}, nil
		}
		return challengeResponse(), nil
	})

	var fallbacks int32
	data, err := refresher.RefreshSessionWithContext(context.Background(), "old-access", "old-session", SessionRefreshContext{
		Cookies: map[string]string{"cf_clearance": "stale-cf"},
		Headers: map[string]string{"User-Agent": DefaultBrowserUserAgent},
		ClearanceFallback: func(context.Context) (ClearanceBundle, ClearanceOutcome, error) {
			atomic.AddInt32(&fallbacks, 1)
			return ClearanceBundle{
				UA:      clearanceUA,
				Cookies: map[string]string{"cf_clearance": "solved-cf"},
			}, ClearanceOutcome{Attempted: true, Solved: true, Proxy: "socks5://***:1080"}, nil
		},
	})
	if err != nil {
		t.Fatalf("RefreshSessionWithContext() error = %v", err)
	}
	if data.AccessToken != "new-access" || data.SessionToken != "new-session" {
		t.Fatalf("refresh data = %#v, want the replayed response to win", data)
	}
	if got := atomic.LoadInt32(&fallbacks); got != 1 {
		t.Fatalf("clearance fallbacks = %d, want exactly 1 replay", got)
	}
	if len(replays) != 2 {
		t.Fatalf("requests = %d, want the challenge plus one replay", len(replays))
	}
	// cf_clearance 绑定签发时的 UA：只回注 cookie 不换 UA 会被判为凭证盗用。
	if ua := replays[1].Get("User-Agent"); ua != clearanceUA {
		t.Fatalf("replay User-Agent = %q, want the clearance UA %q", ua, clearanceUA)
	}
	if ch := replays[1].Get("Sec-Ch-Ua"); !strings.Contains(ch, `"Google Chrome";v="131"`) {
		t.Fatalf("replay Sec-Ch-Ua = %q, want client hints matching the clearance UA", ch)
	}
	// 覆盖必须走 context：surf 的 impersonate 中间件会在发送前改回 profile 自己的
	// 值，只在请求上 Set 头会被静默丢弃，兜底等于没做。
	if len(overrides) != 2 {
		t.Fatalf("recorded overrides = %d, want 2 requests", len(overrides))
	}
	if overrides[0] != nil {
		t.Fatalf("first request override = %#v, want none before the challenge", overrides[0])
	}
	if got := overrides[1]["User-Agent"]; got != clearanceUA {
		t.Fatalf("replay identity override = %#v, want the clearance UA", overrides[1])
	}
	// 兜底结果要带出去，否则日志里「解开了」和「求解报错」长得一样。
	if !data.Clearance.Attempted || !data.Clearance.Solved {
		t.Fatalf("clearance outcome = %#v, want attempted and solved", data.Clearance)
	}
}

// 兜底绝不能变成「每次刷新都跑一次 FlareSolverr」：只有真的撞上挑战才触发，
// 每次求解都要开一次真实浏览器。
func TestSessionRefresherSkipsClearanceOnSuccessAndOnNonChallengeErrors(t *testing.T) {
	cases := []struct {
		name string
		resp *http.Response
	}{
		{name: "success", resp: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session"}`)),
		}},
		// 500 是上游抖动，不是挑战：白跑一次浏览器求解毫无意义。
		{name: "server error", resp: &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`{"detail":"boom"}`)),
		}},
		// 401 是 session 真的失效了，求解也救不回来。
		{name: "unauthorized", resp: &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`{"detail":"authentication token is expired"}`)),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var fallbacks int32
			refresher := NewSessionRefresher(func(*http.Request) (*http.Response, error) {
				return tc.resp, nil
			})
			data, err := refresher.RefreshSessionWithContext(context.Background(), "old-access", "old-session", SessionRefreshContext{
				ClearanceFallback: func(context.Context) (ClearanceBundle, ClearanceOutcome, error) {
					atomic.AddInt32(&fallbacks, 1)
					return ClearanceBundle{}, ClearanceOutcome{Attempted: true}, nil
				},
			})
			if got := atomic.LoadInt32(&fallbacks); got != 0 {
				t.Fatalf("clearance fallbacks = %d, want none outside a challenge", got)
			}
			if tc.name == "success" {
				if err != nil {
					t.Fatalf("RefreshSessionWithContext() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("RefreshSessionWithContext() error = nil, want the upstream status surfaced")
			}
			// 「没撞上挑战」与「撞上挑战但没兜底」不能混：前者无事可做，
			// 后者要去看 .env 有没有配 FlareSolverr。
			if data.Clearance.Skipped != "" {
				t.Fatalf("clearance outcome = %#v, want no skip reason when no challenge occurred", data.Clearance)
			}
		})
	}
}

// 未部署 FlareSolverr 时行为与引入兜底之前一致（照样失败），但原因必须可读：
// 「撞上挑战却没有兜底」要能让运维看出该去配 .env。
func TestSessionRefresherReportsChallengeWithoutFallback(t *testing.T) {
	refresher := NewSessionRefresher(func(*http.Request) (*http.Response, error) {
		return challengeResponse(), nil
	})

	data, err := refresher.RefreshSessionWithContext(context.Background(), "old-access", "old-session", SessionRefreshContext{})
	if err == nil {
		t.Fatal("RefreshSessionWithContext() error = nil, want the challenge reported as a failure")
	}
	if !strings.Contains(err.Error(), "Cloudflare challenge") {
		t.Fatalf("error = %v, want the shared challenge message so callers can classify it", err)
	}
	if data.Clearance.Skipped != "clearance disabled" {
		t.Fatalf("clearance outcome = %#v, want the missing-fallback reason recorded", data.Clearance)
	}
}

// 求解失败仍要如实上报挑战，并把求解的原因带上：兜底只是尽力而为的补救，
// 不能因为它自己失败就把上游的真实回应换成另一句话。
func TestSessionRefresherReportsClearanceSolveFailure(t *testing.T) {
	refresher := NewSessionRefresher(func(*http.Request) (*http.Response, error) {
		return challengeResponse(), nil
	})

	data, err := refresher.RefreshSessionWithContext(context.Background(), "old-access", "old-session", SessionRefreshContext{
		ClearanceFallback: func(context.Context) (ClearanceBundle, ClearanceOutcome, error) {
			return ClearanceBundle{}, ClearanceOutcome{Attempted: true, Proxy: "socks5://***:1080", Error: "flaresolverr status=error: timeout"}, errors.New("flaresolverr status=error: timeout")
		},
	})
	if err == nil {
		t.Fatal("RefreshSessionWithContext() error = nil, want the challenge surfaced")
	}
	if !strings.Contains(err.Error(), "Cloudflare challenge") || !strings.Contains(err.Error(), "clearance fallback") {
		t.Fatalf("error = %v, want both the challenge and the fallback failure", err)
	}
	if !data.Clearance.Attempted || data.Clearance.Error == "" {
		t.Fatalf("clearance outcome = %#v, want the solve failure carried out for logging", data.Clearance)
	}
}
