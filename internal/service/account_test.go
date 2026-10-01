package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt2api/internal/util"
)

type testAccountConfig struct {
	textMode      string
	imageMode     string
	proxy         string
	removeInvalid bool
	removeRateLmt bool
}

func (c testAccountConfig) AutoRemoveInvalidAccounts() bool     { return c.removeInvalid }
func (c testAccountConfig) AutoRemoveRateLimitedAccounts() bool { return c.removeRateLmt }
func (c testAccountConfig) TextAccountScheduleMode() string {
	if c.textMode == "" {
		return "load_balance"
	}
	return c.textMode
}
func (c testAccountConfig) ImageAccountScheduleMode() string {
	if c.imageMode == "" {
		return "load_balance"
	}
	return c.imageMode
}
func (c testAccountConfig) Proxy() string { return c.proxy }

func TestFetchRemoteInfoUsesAccountBoundProxy(t *testing.T) {
	// 账号绑定的代理必须优先于全局代理：cf_clearance 与签发时的出口 IP 绑定，
	// 若账号请求走全局代理，凭证会因 IP 不符当场作废。
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{proxy: "http://global-proxy.example:8080"})
	accounts.remoteBaseURL = unusedLocalBaseURL(t)
	var gotProxy string
	accounts.browserHTTPClient = func(proxy, _ string, _ time.Duration) *http.Client {
		gotProxy = proxy
		return &http.Client{Timeout: time.Second}
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"proxy": "http://account-proxy.example:3128"})

	if _, err := accounts.FetchRemoteInfo(context.Background(), "token-1"); err == nil {
		t.Fatal("FetchRemoteInfo() error = nil, want the stub client to fail")
	}
	if gotProxy != "http://account-proxy.example:3128" {
		t.Fatalf("account proxy = %q, want the account-bound proxy", gotProxy)
	}
}

func TestFetchRemoteInfoFallsBackToGlobalProxy(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{proxy: "http://global-proxy.example:8080"})
	accounts.remoteBaseURL = unusedLocalBaseURL(t)
	var gotProxy string
	accounts.browserHTTPClient = func(proxy, _ string, _ time.Duration) *http.Client {
		gotProxy = proxy
		return &http.Client{Timeout: time.Second}
	}
	accounts.AddAccounts([]string{"token-1"})

	if _, err := accounts.FetchRemoteInfo(context.Background(), "token-1"); err == nil {
		t.Fatal("FetchRemoteInfo() error = nil, want the stub client to fail")
	}
	// 未绑定账号级代理时保持既有部署行为：留空让 ProxyService 回落到全局代理。
	if gotProxy != "" {
		t.Fatalf("account proxy = %q, want empty so the global proxy applies", gotProxy)
	}
}

// unusedLocalBaseURL 返回一个本机未监听的地址。
// 上例只关心「传给 client 构造器的代理是谁」，用不到真实上游；
// 指向本地可避免测试依赖外网。
func unusedLocalBaseURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local port: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return "http://" + address
}

func TestFetchRemoteInfoBootstrapsBeforeAccountRefresh(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	bootstrapped := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		switch r.URL.Path {
		case "/":
			if auth := r.Header.Get("Authorization"); auth != "" {
				t.Errorf("bootstrap request leaked authorization header %q", auth)
			}
			bootstrapped = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			if !bootstrapped {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
				t.Errorf("Authorization = %q, want bearer token", got)
			}
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			if !bootstrapped {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			writeJSON(t, w, map[string]any{
				"default_model_slug": "gpt-5",
				"limits_progress": []map[string]any{{
					"feature_name": "image_gen",
					"remaining":    7,
					"reset_after":  "2026-05-01T00:00:00Z",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	info, err := accounts.FetchRemoteInfo(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("FetchRemoteInfo() error = %v", err)
	}
	if info["email"] != "user@example.com" || info["quota"] != 7 {
		t.Fatalf("FetchRemoteInfo() = %#v", info)
	}
	if info["chatgpt_account_id"] != "user-1" {
		t.Fatalf("chatgpt_account_id = %#v, want user-1", info["chatgpt_account_id"])
	}
	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	mu.Unlock()
	wantPaths := []string{"/", "/backend-api/me", "/backend-api/conversation/init"}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("request paths = %#v, want %#v", gotPaths, wantPaths)
	}
}

func TestRunUpstreamAccountActionsCallsConfirmedEndpoints(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()

		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/settings/account_user_setting":
			if r.Method != http.MethodPatch {
				t.Fatalf("memory method = %s, want PATCH", r.Method)
			}
			if r.URL.Query().Get("feature") != "sunshine" || r.URL.Query().Get("value") != "false" {
				t.Fatalf("memory query = %s", r.URL.RawQuery)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode memory body: %v", err)
			}
			if body["sunshine"] != false {
				t.Fatalf("memory body = %#v", body)
			}
			writeJSON(t, w, map[string]any{"sunshine": false})
		case "/backend-api/conversations":
			if r.Method != http.MethodPatch {
				t.Fatalf("conversations method = %s, want PATCH", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode conversations body: %v", err)
			}
			if body["is_visible"] != false {
				t.Fatalf("conversations body = %#v", body)
			}
			writeJSON(t, w, map[string]any{"success": true, "message": nil})
		case "/backend-api/files/library":
			if r.Method != http.MethodPost {
				t.Fatalf("files library method = %s, want POST", r.Method)
			}
			writeJSON(t, w, map[string]any{
				"items":  []map[string]any{{"file_id": "file_000000005ab871f5bef9279d29e84758", "file_name": "desktop.ini"}},
				"cursor": nil,
			})
		case "/backend-api/files/file_000000005ab871f5bef9279d29e84758":
			if r.Method != http.MethodDelete {
				t.Fatalf("file delete method = %s, want DELETE", r.Method)
			}
			writeJSON(t, w, map[string]any{"success": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	result := accounts.RunUpstreamAccountActions(context.Background(), []string{"token-1"}, UpstreamAccountActionOptions{
		DisableMemory:     true,
		HideConversations: true,
		DeleteFiles:       true,
		FilePageLimit:     50,
	})
	if result["succeeded"] != 1 || result["failed"] != 0 {
		t.Fatalf("RunUpstreamAccountActions() = %#v", result)
	}
	details := result["results"].([]map[string]any)
	actions := details[0]["actions"].(map[string]any)
	deleteFiles := actions["delete_files"].(map[string]any)
	if deleteFiles["files_deleted"] != 1 {
		t.Fatalf("delete_files = %#v, want one deleted file", deleteFiles)
	}
	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	mu.Unlock()
	wantPaths := []string{
		"GET /",
		"PATCH /backend-api/settings/account_user_setting?feature=sunshine&value=false",
		"PATCH /backend-api/conversations",
		"POST /backend-api/files/library",
		"DELETE /backend-api/files/file_000000005ab871f5bef9279d29e84758",
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("request paths = %#v, want %#v", gotPaths, wantPaths)
	}
}

func TestNormalizeAccountPreservesChatGPTAccountID(t *testing.T) {
	normalized := normalizeAccount(map[string]any{
		"access_token":       "token-1",
		"chatgpt_account_id": " acct-123 ",
	})
	if normalized["chatgpt_account_id"] != "acct-123" {
		t.Fatalf("chatgpt_account_id = %#v, want acct-123", normalized["chatgpt_account_id"])
	}
	public := publicAccounts([]map[string]any{normalized})
	if public[0]["chatgpt_account_id"] != "acct-123" {
		t.Fatalf("public chatgpt_account_id = %#v, want acct-123", public[0]["chatgpt_account_id"])
	}
}

func TestExtractFeatureQuotaReadsFileUploadQuota(t *testing.T) {
	limits := []any{
		map[string]any{"feature_name": "image_gen", "remaining": float64(4), "reset_after": "2026-05-08T01:48:36Z"},
		map[string]any{"feature_name": "file_upload", "remaining": float64(3), "reset_after": "2026-05-09T01:48:36Z"},
	}

	quota, restoreAt, unknown := extractQuotaAndRestoreAt(limits)
	if quota != 4 || restoreAt != "2026-05-08T01:48:36Z" || unknown {
		t.Fatalf("image quota = %d / %#v / unknown=%v", quota, restoreAt, unknown)
	}

	fileQuota, fileRestoreAt, fileUnknown := extractFeatureQuota(limits, "file_upload")
	if fileQuota != 3 || fileRestoreAt != "2026-05-09T01:48:36Z" || fileUnknown {
		t.Fatalf("file upload quota = %d / %#v / unknown=%v", fileQuota, fileRestoreAt, fileUnknown)
	}
}

func TestExtractFeatureQuotaReportsMissingFeatureAsUnknown(t *testing.T) {
	limits := []any{map[string]any{"feature_name": "image_gen", "remaining": float64(4)}}

	quota, restoreAt, unknown := extractFeatureQuota(limits, "file_upload")
	if quota != 0 || restoreAt != nil || !unknown {
		t.Fatalf("missing file_upload = %d / %#v / unknown=%v, want unknown", quota, restoreAt, unknown)
	}
}

func TestFileUploadQuotaStaysOutOfImageQuotaAndStatus(t *testing.T) {
	normalized := normalizeAccount(map[string]any{
		"access_token":              "token-1",
		"quota":                     5,
		"file_upload_quota":         0,
		"file_upload_restore_at":    "2026-05-09T01:48:36Z",
		"file_upload_quota_unknown": false,
		"status":                    "正常",
	})
	if normalized["quota"] != 5 {
		t.Fatalf("quota = %#v, want the image quota untouched", normalized["quota"])
	}
	if normalized["status"] != "正常" {
		t.Fatalf("status = %#v, want 正常 (exhausted upload quota must not limit the account)", normalized["status"])
	}
	if normalized["file_upload_quota"] != 0 {
		t.Fatalf("file_upload_quota = %#v, want 0", normalized["file_upload_quota"])
	}

	public := publicAccounts([]map[string]any{normalized})[0]
	if public["quota"] != 5 {
		t.Fatalf("public quota = %#v, want 5", public["quota"])
	}
	if public["fileUploadQuota"] != 0 || public["fileUploadRestoreAt"] != "2026-05-09T01:48:36Z" {
		t.Fatalf("public upload quota = %#v / %#v", public["fileUploadQuota"], public["fileUploadRestoreAt"])
	}
	if public["fileUploadQuotaUnknown"] != false {
		t.Fatalf("public fileUploadQuotaUnknown = %#v, want false", public["fileUploadQuotaUnknown"])
	}
}

func TestNormalizeAccountMarksLegacyRecordUploadQuotaUnknown(t *testing.T) {
	normalized := normalizeAccount(map[string]any{"access_token": "token-1", "quota": 5})

	if normalized["file_upload_quota_unknown"] != true {
		t.Fatalf("legacy record upload quota unknown = %#v, want true", normalized["file_upload_quota_unknown"])
	}
	if _, ok := normalized["file_upload_quota"]; ok {
		t.Fatalf("legacy record should not invent an upload quota: %#v", normalized["file_upload_quota"])
	}
}

func TestFetchRemoteInfoSummarizesForbiddenChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	_, err := accounts.FetchRemoteInfo(context.Background(), "token-1")
	if err == nil {
		t.Fatal("FetchRemoteInfo() error = nil")
	}
	if got := err.Error(); !strings.Contains(got, "/backend-api/me failed: HTTP 403") || !strings.Contains(got, "upstream returned Cloudflare challenge page") {
		t.Fatalf("FetchRemoteInfo() error = %q", got)
	}
}

// 刷新链路的 bootstrap 必须与生图链路一样对瞬时 Cloudflare 挑战重试。
// 此前刷新一次 403 就直接失败，而同样一次 bootstrap 的生图有重试，
// 导致同一个瞬时挑战在生图被吸收、在批量刷新却大面积误报失败。
func TestBootstrapRemoteRetriesTransientChallenge(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		current := attempts
		mu.Unlock()

		switch r.URL.Path {
		case "/":
			// 首次返回 CF 挑战，第二次放行：模拟瞬时挑战。
			if current == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			writeJSON(t, w, map[string]any{"limits_progress": []map[string]any{{
				"feature_name": "image_gen",
				"remaining":    7,
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["refreshed"] != 1 || result["failed"] != 0 {
		t.Fatalf("refresh result = %#v, want the transient challenge retried into success", result)
	}
	if got := attempts; got < 2 {
		t.Fatalf("bootstrap attempts = %d, want at least 2 (one retry after the 403)", got)
	}
}

// 非瞬时失败（如 404）不应重试：重试只该覆盖 403/429 这类可能自愈的状态。
func TestBootstrapRemoteDoesNotRetryNonRetryableStatus(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html>not found</html>"))
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["failed"] != 1 {
		t.Fatalf("refresh result = %#v, want the 404 reported as a failure", result)
	}
	if got := attempts; got != 1 {
		t.Fatalf("bootstrap attempts = %d, want exactly 1 for a non-retryable status", got)
	}
}

// clearanceBootstrapServer 造一个「一直挑战、解出凭证后才放行」的上游。
//
// 首次请求按 challenge 参数决定是否挑战；一旦请求带上了 solvedClearance，
// 之后一律放行——这样测试就能断言「重放确实带了新凭证」，
// 而不是靠调用次数猜。replays 记录每次 bootstrap 请求的头，供断言重放身份。
func clearanceBootstrapServer(t *testing.T, challengeOnce bool, solvedClearance string, bootstrapCalls, meCalls *int32, replays *[]http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			if replays != nil {
				*replays = append(*replays, r.Header.Clone())
			}
			attempt := atomic.AddInt32(bootstrapCalls, 1)
			cleared := false
			if cookie, err := r.Cookie("cf_clearance"); err == nil && cookie.Value == solvedClearance {
				cleared = true
			}
			if (challengeOnce && attempt == 1) || !cleared {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			atomic.AddInt32(meCalls, 1)
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			writeJSON(t, w, map[string]any{"limits_progress": []map[string]any{{
				"feature_name": "image_gen",
				"remaining":    7,
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
}

// refreshAccountsWithClearance 用「上游 + 可解挑战的 FlareSolverr」跑一次刷新。
//
// useSurfClient 为真时走真实的 surf 指纹 client：出站身份头必须经它才算数，
// 用 server.Client() 会绕过 impersonate 中间件，测不出身份覆盖是否生效。
func refreshAccountsWithClearance(t *testing.T, upstream *httptest.Server, flaresolverrURL string, useSurfClient bool) map[string]any {
	t.Helper()
	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = upstream.URL
	if useSurfClient {
		accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
			return browserHTTPClientForProfile("", DefaultBrowserImpersonationProfile, 5*time.Second)
		}
	} else {
		accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
			return upstream.Client()
		}
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})
	if flaresolverrURL != "" {
		accounts.proxy.SetClearance(NewClearanceService(testClearanceConfig{
			enabled: true,
			url:     flaresolverrURL,
			ttl:     3600,
		}))
	}
	return accounts.RefreshAccounts(context.Background(), []string{"token-1"})
}

// clearanceDetailOf 取出刷新结果里某个账号的 clearance 子对象。
func clearanceDetailOf(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	details, ok := result["results"].([]map[string]any)
	if !ok || len(details) == 0 {
		t.Fatalf("results = %#v, want at least one refresh detail", result["results"])
	}
	detail, ok := details[0]["clearance"].(map[string]any)
	if !ok {
		t.Fatalf("refresh detail = %#v, want a clearance sub-object", details[0])
	}
	return detail
}

// 刷新链路命中 CF 挑战时必须动用 clearance 兜底：退避重试只是把同一份旧凭证
// 再送几次，挑战不会因此自愈，用户看到的就是「几秒后失败」。
//
// 这里走真实 surf client，一并验证重放确实换掉了出站 UA——cf_clearance 绑定
// 签发时的 UA，只回注 cookie 而沿用旧 UA 会被上游判为凭证盗用，兜底等于没做。
func TestRefreshAccountsSolvesCloudflareChallengeWithClearance(t *testing.T) {
	const clearanceUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	var bootstrapCalls, meCalls, solverCalls int32
	var replays []http.Header
	upstream := clearanceBootstrapServer(t, false, "solved-cf", &bootstrapCalls, &meCalls, &replays)
	defer upstream.Close()
	solver := flareSolverrStub(t, &solverCalls, func(req map[string]any) map[string]any {
		return map[string]any{"status": "ok", "solution": map[string]any{
			"userAgent": clearanceUA,
			"cookies":   []any{map[string]any{"name": "cf_clearance", "value": "solved-cf"}},
		}}
	})
	defer solver.Close()

	result := refreshAccountsWithClearance(t, upstream, solver.URL, true)
	if result["refreshed"] != 1 || result["failed"] != 0 {
		t.Fatalf("refresh result = %#v, want the challenge solved into a success", result)
	}
	if got := atomic.LoadInt32(&solverCalls); got != 1 {
		t.Fatalf("flaresolverr calls = %d, want 1", got)
	}
	// bootstrap 的退避重试会各打一次，兜底重放再打一次；关键是它确实重放到了。
	if got := atomic.LoadInt32(&bootstrapCalls); got != 4 {
		t.Fatalf("bootstrap calls = %d, want 4 (3 retries + 1 clearance replay)", got)
	}
	// 解出的凭证必须随 sessionCookies 注入后续请求：若只用来重放 bootstrap，
	// 拿到 /me 的这一跳仍会带着被挑战的旧凭证。
	if got := atomic.LoadInt32(&meCalls); got != 1 {
		t.Fatalf("/backend-api/me calls = %d, want 1 replay carrying the solved clearance", got)
	}
	if len(replays) != 4 {
		t.Fatalf("recorded bootstrap requests = %d, want 4", len(replays))
	}
	// 前三次仍是账号自己的身份。
	if ua := replays[0].Get("User-Agent"); ua != DefaultBrowserUserAgent {
		t.Fatalf("first bootstrap User-Agent = %q, want the account fingerprint", ua)
	}
	// 重放必须整套换成签发凭证的那个浏览器：UA 与 Sec-Ch-Ua 都要对上。
	if ua := replays[3].Get("User-Agent"); ua != clearanceUA {
		t.Fatalf("replay User-Agent = %q, want the clearance UA %q", ua, clearanceUA)
	}
	if ch := replays[3].Get("Sec-Ch-Ua"); !strings.Contains(ch, `"Google Chrome";v="131"`) {
		t.Fatalf("replay Sec-Ch-Ua = %q, want client hints matching the clearance UA", ch)
	}
	// 兜底解开了必须能被日志读出来。这里断言的是「现解」而不是「命中缓存」：
	// 两者都是成功路径，但成本差一次真实浏览器，混为一谈就没法判断兜底有没有
	// 真的落到 FlareSolverr 上。
	clearance := clearanceDetailOf(t, result)
	if clearance["attempted"] != true || clearance["solved"] != true {
		t.Fatalf("clearance detail = %#v, want attempted and solved", clearance)
	}
}

// 命中缓存与现解一次都是成功路径，但成本差一次真实浏览器，日志必须分得开：
// 只看「有没有报错」会把复用旧凭证说成刚解开的。
func TestClearanceOutcomeDistinguishesCacheFromSolve(t *testing.T) {
	var solverCalls int32
	solver := flareSolverrStub(t, &solverCalls, func(req map[string]any) map[string]any {
		return map[string]any{"status": "ok", "solution": map[string]any{
			"userAgent": DefaultBrowserUserAgent,
			"cookies":   []any{map[string]any{"name": "cf_clearance", "value": "solved-cf"}},
		}}
	})
	defer solver.Close()

	svc := NewClearanceService(testClearanceConfig{enabled: true, url: solver.URL, ttl: 3600})
	ctx := context.Background()

	_, first, err := svc.RefreshWithOutcome(ctx, "socks5://a:1080")
	if err != nil {
		t.Fatalf("first RefreshWithOutcome() error = %v", err)
	}
	if !first.Attempted || !first.Solved {
		t.Fatalf("first outcome = %#v, want attempted and solved", first)
	}
	if first.Proxy == "" {
		t.Fatalf("first outcome = %#v, want a masked proxy", first)
	}

	_, second, err := svc.RefreshWithOutcome(ctx, "socks5://a:1080")
	if err != nil {
		t.Fatalf("cached RefreshWithOutcome() error = %v", err)
	}
	if !second.Attempted || second.Solved {
		t.Fatalf("cached outcome = %#v, want attempted without solved", second)
	}
	if got := atomic.LoadInt32(&solverCalls); got != 1 {
		t.Fatalf("flaresolverr calls = %d, want 1 (the second call must reuse the cache)", got)
	}
}

// 未部署 FlareSolverr 时行为必须与引入兜底之前完全一致：如实上报挑战失败，
// 并且不为一次注定无解的请求多打上游。
func TestRefreshAccountsSkipsClearanceFallbackWhenUnconfigured(t *testing.T) {
	var bootstrapCalls, meCalls int32
	upstream := clearanceBootstrapServer(t, false, "solved-cf", &bootstrapCalls, &meCalls, nil)
	defer upstream.Close()

	// 全程挑战：重试 3 次耗尽后，因为没有 clearance 可用，直接上报失败。
	result := refreshAccountsWithClearance(t, upstream, "", false)
	if result["failed"] != 1 {
		t.Fatalf("refresh result = %#v, want the challenge reported as a failure", result)
	}
	if got := atomic.LoadInt32(&bootstrapCalls); got != 3 {
		t.Fatalf("bootstrap calls = %d, want 3 retries and no replay", got)
	}
	if got := atomic.LoadInt32(&meCalls); got != 0 {
		t.Fatalf("/backend-api/me calls = %d, want 0 when the bootstrap never succeeds", got)
	}
	// 「撞上挑战却没有兜底」必须与「压根没撞上挑战」分开：前者要去看 .env，
	// 后者什么都不用做，而两者在结果里都是「刷新失败」。
	if got := util.Clean(clearanceDetailOf(t, result)["skipped"]); got != "clearance disabled" {
		t.Fatalf("clearance skipped = %q, want the disabled reason", got)
	}
}

// FlareSolverr 不可达时兜底失败，仍须如实上报挑战，且不污染刷新结果的语义。
func TestRefreshAccountsReportsChallengeWhenClearanceSolveFails(t *testing.T) {
	var bootstrapCalls, meCalls int32
	upstream := clearanceBootstrapServer(t, false, "solved-cf", &bootstrapCalls, &meCalls, nil)
	defer upstream.Close()

	// 保留端口 9（discard）：连接必然被拒，模拟 FlareSolverr 挂了。
	result := refreshAccountsWithClearance(t, upstream, "http://127.0.0.1:9", false)
	if result["failed"] != 1 {
		t.Fatalf("refresh result = %#v, want the challenge reported as a failure", result)
	}
	details, ok := result["results"].([]map[string]any)
	if !ok || len(details) != 1 {
		t.Fatalf("results = %#v, want one refresh detail", result["results"])
	}
	if details[0]["cf_challenge"] != true {
		t.Fatalf("refresh detail = %#v, want cf_challenge still marked", details[0])
	}
	// 求解失败的原因必须落到结果里：只报「还是挑战」等于没说，
	// 运维分不清是 FlareSolverr 挂了还是凭证解出来没用。
	clearance := clearanceDetailOf(t, result)
	if clearance["attempted"] != true || clearance["solved"] != false {
		t.Fatalf("clearance detail = %#v, want an attempted but unsolved fallback", clearance)
	}
	if util.Clean(clearance["error"]) == "" {
		t.Fatalf("clearance detail = %#v, want the flaresolverr failure reason", clearance)
	}
	if got := atomic.LoadInt32(&bootstrapCalls); got != 3 {
		t.Fatalf("bootstrap calls = %d, want 3 retries and no successful replay", got)
	}
}

// session 刷新的 TLS profile 必须来自账号指纹，而不是硬编码的 chrome。
// 请求头来自账号指纹、TLS 指纹来自 profile，二者不同源会让 firefox 账号发出
// 「UA 与 Sec-Ch-Ua 说 Chrome、Sec-Ch-Ua-Full-Version 说 Firefox」的矛盾身份。
func TestSessionRefreshUsesAccountProfile(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{
		"status":        "正常",
		"quota":         5,
		"session_token": "session-token",
	})

	// 把账号指纹改成 firefox，模拟池中权重 30% 的那一类账号。
	account := accounts.GetAccount("token-1")
	fp := util.StringMap(account["fp"])
	for key, value := range BrowserFingerprintFromFamilyVersion("firefox", "148") {
		fp[key] = value
	}
	accounts.UpdateAccount("token-1", map[string]any{"fp": fp})

	ctx := accounts.sessionRefreshContext("token-1", nil)
	if ctx.Profile != "firefox148" {
		t.Fatalf("session refresh profile = %q, want firefox148 from the account fingerprint", ctx.Profile)
	}

	// 身份必须自洽：Client-Hints 报的版本要与 UA 同源。
	userAgent := ctx.Headers["User-Agent"]
	if !strings.Contains(userAgent, "Firefox/148") {
		t.Fatalf("User-Agent = %q, want a Firefox 148 identity", userAgent)
	}
	if got := ctx.Headers["Sec-Ch-Ua-Full-Version"]; !strings.Contains(got, "148") {
		t.Fatalf("Sec-Ch-Ua-Full-Version = %q, want 148 to match the User-Agent", got)
	}
}

// session 刷新与 bootstrap 共用同一套 CF cookie 新鲜度判定：
// 超出窗口的 cf_clearance 继续发送只会被 CF 判定为可疑。
func TestSessionRefreshDropsStaleClearance(t *testing.T) {
	now := time.Now().UTC()
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{
		"status":        "正常",
		"quota":         5,
		"session_token": "session-token",
		"session_cookies": map[string]string{
			"cf_clearance": "stale-cf",
			"oai-did":      "did-cookie",
		},
		"session_cookie_updated_at": map[string]string{
			// 超过 2 小时窗口，且该账号未绑定代理。
			"cf_clearance": now.Add(-3 * time.Hour).Format(time.RFC3339),
		},
	})

	ctx := accounts.sessionRefreshContext("token-1", nil)
	if _, ok := ctx.Cookies["cf_clearance"]; ok {
		t.Fatalf("session refresh cookies = %#v, want the stale cf_clearance dropped", ctx.Cookies)
	}
	if ctx.Cookies["oai-did"] != "did-cookie" {
		t.Fatalf("session refresh cookies = %#v, want non-CF cookies kept", ctx.Cookies)
	}
}

// CF 挑战是出口 IP / 指纹 / cookie 上下文的问题，不是账号本身的问题，
// 因此刷新只做标注、绝不改账号状态：status=限流 会在
// auto_remove_rate_limited_accounts 开启时直接删除账号。
func TestRefreshAccountsMarksCloudflareChallengeWithoutChangingStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 所有请求都返回挑战页，模拟出口 IP 被 CF 拦截。
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`))
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["failed"] != 1 {
		t.Fatalf("refresh result = %#v, want the challenge reported as a failure", result)
	}

	// 账号必须原样保留：状态与额度都不变。
	account := accounts.GetAccount("token-1")
	if account == nil {
		t.Fatal("GetAccount() = nil, want the account kept despite the challenge")
	}
	if account["status"] != "正常" || account["quota"] != 5 {
		t.Fatalf("account = %#v, want status/quota unchanged by a Cloudflare challenge", account)
	}

	// 刷新结果里应当标注这次失败是挑战，便于使用者区分「网络问题」与「账号问题」。
	details, ok := result["results"].([]map[string]any)
	if !ok || len(details) != 1 {
		t.Fatalf("results = %#v, want one refresh detail", result["results"])
	}
	if details[0]["cf_challenge"] != true {
		t.Fatalf("refresh detail = %#v, want cf_challenge marked", details[0])
	}
}

func TestRefreshAccountsReturnsEmptyErrorsArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			writeJSON(t, w, map[string]any{
				"default_model_slug": "gpt-5",
				"limits_progress": []map[string]any{{
					"feature_name": "image_gen",
					"remaining":    7,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["refreshed"] != 1 {
		t.Fatalf("refreshed = %#v, want 1", result["refreshed"])
	}
	if result["total"] != 1 || result["failed"] != 0 {
		t.Fatalf("refresh summary = total %#v failed %#v, want 1/0", result["total"], result["failed"])
	}
	if _, ok := result["duration_ms"].(int64); !ok {
		t.Fatalf("duration_ms type = %T, want int64", result["duration_ms"])
	}
	details, ok := result["results"].([]map[string]any)
	if !ok || len(details) != 1 {
		t.Fatalf("results = %#v, want one refresh detail", result["results"])
	}
	if details[0]["success"] != true || details[0]["account_id"] == "" || details[0]["message"] != "刷新成功" {
		t.Fatalf("refresh detail = %#v, want successful account result", details[0])
	}
	if details[0]["email"] != "user@example.com" || details[0]["quota"] != 7 {
		t.Fatalf("refresh detail account fields = %#v", details[0])
	}
	errors, ok := result["errors"].([]map[string]string)
	if !ok {
		t.Fatalf("errors type = %T, want []map[string]string", result["errors"])
	}
	if errors == nil || len(errors) != 0 {
		t.Fatalf("errors = %#v, want empty non-nil slice", errors)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var payload struct {
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if string(payload.Errors) != "[]" {
		t.Fatalf("encoded errors = %s, want []", payload.Errors)
	}
}

func TestListAccountsIncludesCookieCompleteness(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-complete", "token-core-only", "token-partial", "token-empty"})
	accounts.UpdateAccount("token-complete", map[string]any{
		"session_cookies": map[string]string{
			"cf_clearance": "cf-cookie",
			"__cf_bm":      "bm-cookie",
			"oai-did":      "did-cookie",
			"oai-sc":       "sc-cookie",
		},
	})
	// 仅核心 oai-did，增强型 Cookie 全缺 —— 仍应视为完整。
	accounts.UpdateAccount("token-core-only", map[string]any{
		"session_cookies": map[string]string{
			"oai-did": "did-cookie",
		},
	})
	// 有增强型 Cookie 但缺核心 oai-did —— 视为部分。
	accounts.UpdateAccount("token-partial", map[string]any{
		"session_cookies": map[string]string{
			"cf_clearance": "cf-cookie",
			"__cf_bm":      "bm-cookie",
		},
	})

	items := accounts.ListAccounts()
	if len(items) != 4 {
		t.Fatalf("items len = %d, want 4", len(items))
	}
	indexed := map[string]map[string]any{}
	for _, item := range items {
		indexed[util.Clean(item["access_token"])] = item
	}

	complete := indexed["token-complete"]
	if complete == nil {
		t.Fatal("token-complete account missing")
	}
	if complete["cookieStatus"] != "完整" {
		t.Fatalf("complete cookieStatus = %#v, want 完整", complete["cookieStatus"])
	}
	if missing, ok := complete["missingCookies"].([]string); !ok || len(missing) != 0 {
		t.Fatalf("complete missingCookies = %#v, want empty []string", complete["missingCookies"])
	}

	coreOnly := indexed["token-core-only"]
	if coreOnly == nil {
		t.Fatal("token-core-only account missing")
	}
	if coreOnly["cookieStatus"] != "完整" {
		t.Fatalf("core-only cookieStatus = %#v, want 完整", coreOnly["cookieStatus"])
	}
	if missing, ok := coreOnly["missingCookies"].([]string); !ok || len(missing) != 0 {
		t.Fatalf("core-only missingCookies = %#v, want empty []string", coreOnly["missingCookies"])
	}

	partial := indexed["token-partial"]
	if partial == nil {
		t.Fatal("token-partial account missing")
	}
	if partial["cookieStatus"] != "部分" {
		t.Fatalf("partial cookieStatus = %#v, want 部分", partial["cookieStatus"])
	}
	missing, ok := partial["missingCookies"].([]string)
	if !ok {
		t.Fatalf("partial missingCookies type = %T, want []string", partial["missingCookies"])
	}
	if !reflect.DeepEqual(missing, []string{"oai-did", "oai-sc"}) {
		t.Fatalf("partial missingCookies = %#v, want [oai-did oai-sc]", missing)
	}

	empty := indexed["token-empty"]
	if empty == nil {
		t.Fatal("token-empty account missing")
	}
	if empty["cookieStatus"] != "无" {
		t.Fatalf("empty cookieStatus = %#v, want 无", empty["cookieStatus"])
	}
	missing, ok = empty["missingCookies"].([]string)
	if !ok {
		t.Fatalf("empty missingCookies type = %T, want []string", empty["missingCookies"])
	}
	if !reflect.DeepEqual(missing, []string{"oai-did", "cf_clearance", "__cf_bm", "oai-sc"}) {
		t.Fatalf("empty missingCookies = %#v, want all key cookies", missing)
	}
}

func TestRefreshAccountsUsesStoredCookiesForQuotaRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			if cookie, err := r.Cookie("cf_clearance"); err != nil || cookie.Value != "cf-cookie" {
				t.Fatalf("bootstrap cf_clearance = %#v err %v, want cf-cookie", cookie, err)
			}
			http.SetCookie(w, &http.Cookie{Name: "__cflb", Value: "route-cookie", Path: "/"})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			if cookie, err := r.Cookie("cf_clearance"); err != nil || cookie.Value != "cf-cookie" {
				t.Fatalf("me cf_clearance = %#v err %v, want cf-cookie", cookie, err)
			}
			if cookie, err := r.Cookie("__cflb"); err != nil || cookie.Value != "route-cookie" {
				t.Fatalf("me __cflb = %#v err %v, want route-cookie", cookie, err)
			}
			http.SetCookie(w, &http.Cookie{Name: "__cf_bm", Value: "fresh-bm", Path: "/"})
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			if cookie, err := r.Cookie("cf_clearance"); err != nil || cookie.Value != "cf-cookie" {
				t.Fatalf("init cf_clearance = %#v err %v, want cf-cookie", cookie, err)
			}
			writeJSON(t, w, map[string]any{
				"default_model_slug": "gpt-5",
				"limits_progress": []map[string]any{{
					"feature_name": "image_gen",
					"remaining":    7,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	now := time.Now().UTC()
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{
		"session_cookies": map[string]string{"cf_clearance": "cf-cookie"},
		"session_cookie_updated_at": map[string]string{
			"cf_clearance": now.Format(time.RFC3339),
		},
	})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["refreshed"] != 1 || result["failed"] != 0 {
		t.Fatalf("refresh result = %#v, want success", result)
	}
	cookies := SessionCookieStringMap(accounts.GetAccount("token-1")["session_cookies"])
	if cookies["cf_clearance"] != "cf-cookie" || cookies["__cflb"] != "route-cookie" || cookies["__cf_bm"] != "fresh-bm" {
		t.Fatalf("stored session_cookies = %#v", cookies)
	}
	updatedAt, ok := accounts.GetAccount("token-1")["session_cookie_updated_at"].(map[string]string)
	if !ok {
		t.Fatalf("session_cookie_updated_at type = %T, want map[string]string", accounts.GetAccount("token-1")["session_cookie_updated_at"])
	}
	for _, name := range []string{"__cflb", "__cf_bm"} {
		if _, err := time.Parse(time.RFC3339, updatedAt[name]); err != nil {
			t.Fatalf("session_cookie_updated_at[%s] = %q, want RFC3339 timestamp", name, updatedAt[name])
		}
	}
}

// 未绑定出口代理时，超出挑战凭证窗口的 cf_clearance 不再发送；
// oai-did 属于长期身份 cookie，不受该窗口影响。
func TestRefreshAccountsSkipsStaleCloudflareCookies(t *testing.T) {
	var bootstrapCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			bootstrapCookie = r.Header.Get("Cookie")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			writeJSON(t, w, map[string]any{"default_model_slug": "gpt-5"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	now := time.Now().UTC()
	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{
		"session_cookies": map[string]string{
			"cf_clearance": "stale-cf",
			"oai-did":      "did-cookie",
		},
		"session_cookie_updated_at": map[string]string{
			"cf_clearance": now.Add(-3 * time.Hour).Format(time.RFC3339),
		},
	})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["refreshed"] != 1 || result["failed"] != 0 {
		t.Fatalf("refresh result = %#v, want success", result)
	}
	if strings.Contains(bootstrapCookie, "cf_clearance=stale-cf") {
		t.Fatalf("bootstrap Cookie header = %q, should skip stale cf_clearance", bootstrapCookie)
	}
	if !strings.Contains(bootstrapCookie, "oai-did=did-cookie") {
		t.Fatalf("bootstrap Cookie header = %q, missing oai-did", bootstrapCookie)
	}
}

func TestRefreshAccountStateMarksUnauthorizedInitAsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(t, w, map[string]any{"detail": "token_invalidated"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	account, err := accounts.RefreshAccountState(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("RefreshAccountState() error = %v", err)
	}
	if account == nil {
		t.Fatal("RefreshAccountState() account = nil, want updated invalid account")
	}
	if account["status"] != "异常" {
		t.Fatalf("status = %#v, want 异常", account["status"])
	}
	if account["quota"] != 0 {
		t.Fatalf("quota = %#v, want 0", account["quota"])
	}
}

func TestAddAccountFromSessionUpdatesExistingUserWhenAccessTokenRotates(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.refresher = NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access-token","sessionToken":"new-session-token","expires":"2026-05-12T00:00:00Z","user":{"id":"user-123","email":"user@example.com","name":"New Name"}}`)),
		}, nil
	})
	accounts.AddAccounts([]string{"old-access-token"})
	accounts.UpdateAccount("old-access-token", map[string]any{
		"user_id": "user-123",
		"email":   "user@example.com",
		"name":    "Old Name",
		"type":    "Plus",
		"quota":   7,
		"status":  "禁用",
	})

	result, err := accounts.AddAccountFromSession(`{
		"accessToken":"new-access-token",
		"sessionToken":"new-session-token",
		"expires":"2026-05-12T00:00:00Z",
		"user":{"id":"user-123","email":"user@example.com","name":"New Name"}
	}`)
	if err != nil {
		t.Fatalf("AddAccountFromSession() error = %v", err)
	}
	if result["added"] != 0 || result["updated"] != 1 {
		t.Fatalf("AddAccountFromSession() result = %#v, want updated existing account", result)
	}
	if old := accounts.GetAccount("old-access-token"); old != nil {
		t.Fatalf("old token account still exists: %#v", old)
	}
	updated := accounts.GetAccount("new-access-token")
	if updated == nil {
		t.Fatalf("new token account missing")
	}
	if len(accounts.items) != 1 {
		t.Fatalf("account count = %d, want 1: %#v", len(accounts.items), accounts.items)
	}
	if updated["session_token"] != "new-session-token" || updated["session_expires"] != "2026-05-12T00:00:00Z" {
		t.Fatalf("session fields not updated: %#v", updated)
	}
	if updated["type"] != "Plus" || updated["quota"] != 7 || updated["status"] != "禁用" {
		t.Fatalf("existing account metadata not preserved: %#v", updated)
	}
	if updated["name"] != "New Name" || updated["email"] != "user@example.com" || updated["user_id"] != "user-123" {
		t.Fatalf("session identity fields not updated: %#v", updated)
	}
}

func TestAddAccountFromSessionUsesValidatedIdentityForMatching(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.refresher = NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"validated-access-token","sessionToken":"validated-session-token","expires":"2026-05-13T00:00:00Z","user":{"id":"validated-user","email":"validated@example.com","name":"Validated Name"}}`)),
		}, nil
	})
	accounts.AddAccounts([]string{"old-access-token"})
	accounts.UpdateAccount("old-access-token", map[string]any{
		"user_id": "validated-user",
		"email":   "validated@example.com",
		"type":    "Plus",
		"status":  "异常",
	})

	result, err := accounts.AddAccountFromSession(`{
		"accessToken":"submitted-access-token",
		"sessionToken":"submitted-session-token",
		"expires":"2026-05-12T00:00:00Z",
		"user":{"id":"attacker-user","email":"attacker@example.com","name":"Attacker Name"}
	}`)
	if err != nil {
		t.Fatalf("AddAccountFromSession() error = %v", err)
	}
	if result["updated"] != 1 || result["added"] != 0 {
		t.Fatalf("AddAccountFromSession() result = %#v, want validated identity update", result)
	}
	if len(accounts.items) != 1 {
		t.Fatalf("account count = %d, want 1: %#v", len(accounts.items), accounts.items)
	}
	updated := accounts.GetAccount("validated-access-token")
	if updated == nil {
		t.Fatalf("validated token account missing")
	}
	if updated["user_id"] != "validated-user" || updated["email"] != "validated@example.com" || updated["name"] != "Validated Name" {
		t.Fatalf("submitted identity was used instead of validated identity: %#v", updated)
	}
	if updated["status"] != "正常" || updated["type"] != "Plus" {
		t.Fatalf("validated account metadata not preserved: %#v", updated)
	}
}

func TestAddAccountFromSessionValidatesSessionBeforeRecoveringAbnormalAccount(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.refresher = NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"refreshed-access-token","sessionToken":"refreshed-session-token","expires":"2026-05-13T00:00:00Z","user":{"id":"user-123","email":"user@example.com","name":"Recovered Name"}}`)),
		}, nil
	})
	accounts.AddAccounts([]string{"old-access-token"})
	accounts.UpdateAccount("old-access-token", map[string]any{
		"user_id": "user-123",
		"email":   "user@example.com",
		"type":    "Plus",
		"quota":   0,
		"status":  "异常",
	})

	result, err := accounts.AddAccountFromSession(`{
		"accessToken":"submitted-access-token",
		"sessionToken":"submitted-session-token",
		"expires":"2026-05-12T00:00:00Z",
		"user":{"id":"user-123","email":"user@example.com","name":"Recovered Name"}
	}`)
	if err != nil {
		t.Fatalf("AddAccountFromSession() error = %v", err)
	}
	if result["updated"] != 1 {
		t.Fatalf("AddAccountFromSession() result = %#v, want updated existing account", result)
	}
	if old := accounts.GetAccount("old-access-token"); old != nil {
		t.Fatalf("old token account still exists: %#v", old)
	}
	updated := accounts.GetAccount("refreshed-access-token")
	if updated == nil {
		t.Fatalf("refreshed token account missing")
	}
	if len(accounts.items) != 1 {
		t.Fatalf("account count = %d, want 1: %#v", len(accounts.items), accounts.items)
	}
	if updated["session_token"] != "refreshed-session-token" || updated["session_expires"] != "2026-05-13T00:00:00Z" {
		t.Fatalf("validated session fields not stored: %#v", updated)
	}
	if updated["status"] != "正常" || updated["type"] != "Plus" {
		t.Fatalf("abnormal account not recovered with metadata preserved: %#v", updated)
	}
}

func TestAddAccountFromSessionRecoversAbnormalAccountWhenAccessTokenMatches(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.refresher = NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"same-access-token","sessionToken":"fresh-session-token","expires":"2026-05-13T00:00:00Z","user":{"id":"user-123","email":"user@example.com","name":"Recovered Name"}}`)),
		}, nil
	})
	accounts.AddAccounts([]string{"same-access-token"})
	accounts.UpdateAccount("same-access-token", map[string]any{
		"user_id": "user-123",
		"email":   "user@example.com",
		"type":    "Plus",
		"status":  "异常",
	})

	result, err := accounts.AddAccountFromSession(`{
		"accessToken":"same-access-token",
		"sessionToken":"submitted-session-token",
		"expires":"2026-05-12T00:00:00Z",
		"user":{"id":"user-123","email":"user@example.com","name":"Recovered Name"}
	}`)
	if err != nil {
		t.Fatalf("AddAccountFromSession() error = %v", err)
	}
	if result["updated"] != 1 {
		t.Fatalf("AddAccountFromSession() result = %#v, want updated existing account", result)
	}
	updated := accounts.GetAccount("same-access-token")
	if updated == nil {
		t.Fatalf("same token account missing")
	}
	if updated["status"] != "正常" || updated["session_token"] != "fresh-session-token" || updated["session_expires"] != "2026-05-13T00:00:00Z" {
		t.Fatalf("account not recovered with validated session fields: %#v", updated)
	}
}

func TestAddAccountFromSessionStoresAndUsesBrowserCookies(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.refresher = NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		for name, want := range map[string]string{
			"__Secure-next-auth.session-token": "browser-session-token",
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
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"browser-access-token","sessionToken":"browser-session-token","expires":"2026-05-12T00:00:00Z","user":{"id":"browser-user","email":"browser@example.com","name":"Browser User"}}`)),
		}, nil
	})

	result, err := accounts.AddAccountFromSession(`{
		"accessToken":"browser-access-token",
		"sessionToken":"browser-session-token",
		"expires":"2026-05-12T00:00:00Z",
		"user":{"id":"browser-user","email":"browser@example.com","name":"Browser User"}
	}`, `{"cf_clearance":"cf-cookie","__cf_bm":"bm-cookie","oai-did":"did-cookie"}`)
	if err != nil {
		t.Fatalf("AddAccountFromSession() error = %v", err)
	}
	if result["added"] != 1 {
		t.Fatalf("AddAccountFromSession() result = %#v, want added account", result)
	}
	account := accounts.GetAccount("browser-access-token")
	if account == nil {
		t.Fatalf("browser-access-token account missing")
	}
	cookies := SessionCookieStringMap(account["session_cookies"])
	if cookies["cf_clearance"] != "cf-cookie" || cookies["__cf_bm"] != "bm-cookie" || cookies["oai-did"] != "did-cookie" {
		t.Fatalf("stored session_cookies = %#v", account["session_cookies"])
	}
	updatedAt, ok := account["session_cookie_updated_at"].(map[string]string)
	if !ok {
		t.Fatalf("session_cookie_updated_at type = %T, want map[string]string", account["session_cookie_updated_at"])
	}
	for _, name := range []string{"cf_clearance", "__cf_bm"} {
		if _, err := time.Parse(time.RFC3339, updatedAt[name]); err != nil {
			t.Fatalf("session_cookie_updated_at[%s] = %q, want RFC3339 timestamp", name, updatedAt[name])
		}
	}
	if updatedAt["oai-did"] != "" {
		t.Fatalf("session_cookie_updated_at should not track stable oai-did: %#v", updatedAt)
	}
}

func TestAddAccountFromSessionRejectsInvalidSessionWithoutMutatingExistingAccount(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.refresher = NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`{"detail":"invalid session"}`)),
		}, nil
	})
	accounts.AddAccounts([]string{"old-access-token"})
	accounts.UpdateAccount("old-access-token", map[string]any{
		"user_id":       "user-123",
		"email":         "user@example.com",
		"type":          "Plus",
		"quota":         3,
		"status":        "异常",
		"session_token": "old-session-token",
	})

	_, err := accounts.AddAccountFromSession(`{
		"accessToken":"submitted-access-token",
		"sessionToken":"bad-session-token",
		"expires":"2026-05-12T00:00:00Z",
		"user":{"id":"user-123","email":"user@example.com","name":"Bad Session"}
	}`)
	if err == nil || !strings.Contains(err.Error(), "session token validation failed") {
		t.Fatalf("AddAccountFromSession() error = %v, want validation failure", err)
	}
	if len(accounts.items) != 1 {
		t.Fatalf("account count = %d, want unchanged single account: %#v", len(accounts.items), accounts.items)
	}
	if created := accounts.GetAccount("submitted-access-token"); created != nil {
		t.Fatalf("invalid session created new account: %#v", created)
	}
	unchanged := accounts.GetAccount("old-access-token")
	if unchanged == nil {
		t.Fatalf("old account missing after invalid session import")
	}
	if unchanged["status"] != "异常" || unchanged["session_token"] != "old-session-token" || unchanged["quota"] != 3 {
		t.Fatalf("old account mutated after invalid session import: %#v", unchanged)
	}
}

func TestApplyAccountErrorMessageDoesNotMarkGenericUnauthorizedAsInvalid(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "image_stream", "auth_chat_requirements failed: status=401, body={\"detail\":\"challenge_required\"}")
	if handled {
		t.Fatalf("handled = true message = %q, want generic unauthorized ignored", message)
	}
	account := accounts.GetAccount("token-1")
	if account["status"] != "正常" || account["quota"] != 5 {
		t.Fatalf("account = %#v, want unchanged normal account", account)
	}
}

func TestApplyAccountErrorMessageDoesNotMarkGenericTooManyRequestsAsLimited(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5, "image_quota_unknown": true})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "image_stream", "auth_chat_requirements failed: status=429, body={\"detail\":\"too many requests\"}")
	if handled {
		t.Fatalf("handled = true message = %q, want generic upstream 429 ignored", message)
	}
	account := accounts.GetAccount("token-1")
	if account["status"] != "正常" || account["quota"] != 5 || account["image_quota_unknown"] != true {
		t.Fatalf("account = %#v, want unchanged normal account", account)
	}
}

func TestRefreshAccountsUsesStoredBrowserCookiesForSessionRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(t, w, map[string]any{"detail": "authentication token is expired"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.refresher = NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		cookie, err := req.Cookie("cf_clearance")
		if err != nil || cookie.Value != "cf-cookie" {
			t.Fatalf("cf_clearance = %#v err %v, want cf-cookie", cookie, err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access-token","sessionToken":"new-session-token","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})
	accounts.AddAccounts([]string{"expired-access-token"})
	accounts.UpdateAccount("expired-access-token", map[string]any{
		"status":          "正常",
		"quota":           5,
		"session_token":   "refresh-session-token",
		"session_cookies": map[string]string{"cf_clearance": "cf-cookie"},
		// 时间戳不可省：session 刷新与 bootstrap 共用同一套新鲜度判定，
		// 缺少时间戳的 cf_clearance 会被判定为无法确认有效性而丢弃。
		"session_cookie_updated_at": map[string]string{
			"cf_clearance": time.Now().UTC().Format(time.RFC3339),
		},
	})

	result := accounts.RefreshAccounts(context.Background(), []string{"expired-access-token"})
	if result["session_refreshed"] != 1 || result["session_failed"] != 0 {
		t.Fatalf("refresh result = %#v, want session refresh success", result)
	}
	// session 续期只累加 session_refreshed，refreshed 保持 0，errors 也为空。
	// 前端若只读 refreshed，就会把一次成功的续期报成「刷新成功 0 个账户」。
	// 这里把该形状固定下来：消费方必须同时读这两个计数。
	if result["refreshed"] != 0 {
		t.Fatalf("refreshed = %#v, want 0 for a pure session refresh", result["refreshed"])
	}
	if errors := result["errors"].([]map[string]string); len(errors) != 0 {
		t.Fatalf("errors = %#v, want empty so the refresh counts as a success", errors)
	}
	if account := accounts.GetAccount("new-access-token"); account == nil {
		t.Fatalf("new-access-token account missing after session refresh")
	}
}

// 续期后的账号信息拉取失败时，续期本身仍算成功，但必须如实标注额度等展示值
// 还是旧的。此前这条错误被静默丢弃：界面显示「刷新成功」，表格里的上传额度
// 却纹丝不动，使用者无从判断是没刷上还是刷新没生效。
func TestRefreshAccountsFlagsStaleInfoWhenSessionRefreshCannotFetchQuota(t *testing.T) {
	var meCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			// 续期前 token 过期；续期后上游仍拒绝，信息拉取失败。
			if atomic.AddInt32(&meCalls, 1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				writeJSON(t, w, map[string]any{"detail": "authentication token is expired"})
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			writeJSON(t, w, map[string]any{"detail": "upstream unavailable"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.refresher = NewSessionRefresher(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access-token","sessionToken":"new-session-token","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})
	accounts.AddAccounts([]string{"expired-access-token"})
	accounts.UpdateAccount("expired-access-token", map[string]any{
		"status":                    "正常",
		"quota":                     5,
		"file_upload_quota":         80,
		"file_upload_quota_unknown": false,
		"session_token":             "refresh-session-token",
	})

	result := accounts.RefreshAccounts(context.Background(), []string{"expired-access-token"})
	if result["session_refreshed"] != 1 || result["refreshed"] != 0 {
		t.Fatalf("refresh result = %#v, want a pure session refresh", result)
	}
	if result["info_stale"] != 1 {
		t.Fatalf("info_stale = %#v, want 1 so the UI can say the numbers are old", result["info_stale"])
	}
	details := result["results"].([]map[string]any)
	if len(details) != 1 || details[0]["info_stale"] != true {
		t.Fatalf("results = %#v, want a single entry flagged info_stale", details)
	}
	// 额度未被本次刷新改写：上游没给新值，旧值原样保留正是「展示值仍是上次结果」。
	if account := accounts.GetAccount("new-access-token"); util.ToInt(account["file_upload_quota"], -1) != 80 {
		t.Fatalf("file_upload_quota = %#v, want the previous value kept", account["file_upload_quota"])
	}
}

func TestRefreshAccountsMarksRateLimitedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			w.WriteHeader(http.StatusTooManyRequests)
			writeJSON(t, w, map[string]any{"error": map[string]any{"message": "You've reached the image generation limit"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	result := accounts.RefreshAccounts(context.Background(), []string{"token-1"})
	if result["refreshed"] != 0 {
		t.Fatalf("refreshed = %#v, want 0", result["refreshed"])
	}
	errors, ok := result["errors"].([]map[string]string)
	if !ok || len(errors) != 1 {
		t.Fatalf("errors = %#v, want one error", result["errors"])
	}
	if errors[0]["error"] != "检测到限流" {
		t.Fatalf("error = %q, want 检测到限流", errors[0]["error"])
	}
	details, ok := result["results"].([]map[string]any)
	if !ok || len(details) != 1 {
		t.Fatalf("results = %#v, want one refresh detail", result["results"])
	}
	if details[0]["success"] != false || details[0]["status"] != "error" || details[0]["message"] != "检测到限流" {
		t.Fatalf("refresh detail = %#v, want failed rate-limit result", details[0])
	}
	if details[0]["account_status"] != "限流" || details[0]["quota"] != 0 {
		t.Fatalf("refresh detail account state = %#v, want limited quota 0", details[0])
	}
	account := accounts.GetAccount("token-1")
	if account["status"] != "限流" {
		t.Fatalf("status = %#v, want 限流", account["status"])
	}
	if account["quota"] != 0 {
		t.Fatalf("quota = %#v, want 0", account["quota"])
	}
	if account["image_quota_unknown"] != false {
		t.Fatalf("image_quota_unknown = %#v, want false", account["image_quota_unknown"])
	}
}

func TestGetAvailableAccessTokenReservesKnownImageQuota(t *testing.T) {
	accounts := newTestAccountService(t)
	server := newAccountQuotaServer(t, map[string]any{
		"email": "user@example.com",
		"id":    "user-1",
	}, []map[string]any{{
		"feature_name": "image_gen",
		"remaining":    1,
	}})
	defer server.Close()
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 1})

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("first GetAvailableAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("first token = %q, want token-1", token)
	}

	if token, err := accounts.GetAvailableAccessToken(context.Background()); err == nil {
		t.Fatalf("second GetAvailableAccessToken() = %q, want no available image quota", token)
	}

	accounts.MarkImageResult("token-1", false)
	token, err = accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() after failed result error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token after failed result = %q, want token-1", token)
	}

	accounts.MarkImageResult("token-1", true)
	if token, err := accounts.GetAvailableAccessToken(context.Background()); err == nil {
		t.Fatalf("GetAvailableAccessToken() after quota consumed = %q, want no available image quota", token)
	}
}

func TestGetAvailableAccessTokenLimitsUnknownImageQuotaToOneInFlight(t *testing.T) {
	accounts := newTestAccountService(t)
	server := newAccountQuotaServer(t, map[string]any{
		"email":     "plus@example.com",
		"id":        "user-1",
		"plan_type": "plus",
	}, nil)
	defer server.Close()
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 0, "image_quota_unknown": true, "type": "Plus"})

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("first GetAvailableAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("first token = %q, want token-1", token)
	}

	if token, err := accounts.GetAvailableAccessToken(context.Background()); err == nil {
		t.Fatalf("second GetAvailableAccessToken() = %q, want no available image quota", token)
	}

	accounts.MarkImageResult("token-1", false)
	token, err = accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() after release error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token after release = %q, want token-1", token)
	}
	accounts.MarkImageResult("token-1", false)
}

func TestGetAvailableAccessTokenAllowsFreeUnknownImageQuota(t *testing.T) {
	accounts := newTestAccountService(t)
	server := newAccountQuotaServer(t, map[string]any{
		"email":     "free@example.com",
		"id":        "user-1",
		"plan_type": "free",
	}, nil)
	defer server.Close()
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"free-token"})
	accounts.UpdateAccount("free-token", map[string]any{"status": "正常", "quota": 0, "image_quota_unknown": true, "type": "Free"})

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() error = %v", err)
	}
	if token != "free-token" {
		t.Fatalf("token = %q, want free-token", token)
	}
	account := accounts.GetAccount("free-token")
	if account["status"] != "正常" || account["type"] != "Free" || account["image_quota_unknown"] != true {
		t.Fatalf("free unknown quota account = %#v, want available Free account with unknown image quota", account)
	}
	accounts.MarkImageResult("free-token", false)
}

func TestGetAvailableAccessTokenReportsRefreshFailure(t *testing.T) {
	accounts := newTestAccountService(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			http.Error(w, "temporary upstream failure", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 1})

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err == nil {
		t.Fatalf("GetAvailableAccessToken() token = %q, want refresh error", token)
	}
	if !strings.Contains(err.Error(), "/backend-api/me failed: HTTP 502") {
		t.Fatalf("GetAvailableAccessToken() error = %q, want refresh failure detail", err.Error())
	}
}

func TestGetAvailableAccessTokenUsesCachedAccountOnConnectionRefreshFailure(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New(`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`)
			}),
		}
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 1, "type": "Plus"})

	token, err := accounts.GetAvailableAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableAccessToken() error = %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token = %q, want cached token-1", token)
	}
}

func TestGetTextAccessTokenUsesCoolingBurstWithoutResettingCooldown(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{textMode: "fill_first"})
	accounts.AddAccounts([]string{"free-a", "free-b"})
	accounts.UpdateAccount("free-a", map[string]any{"status": "正常", "type": "Free"})
	accounts.UpdateAccount("free-b", map[string]any{"status": "正常", "type": "Free"})

	firstToken := ""
	for i := 0; i < 10; i++ {
		token := accounts.GetTextAccessToken()
		if token == "" {
			t.Fatalf("GetTextAccessToken() call %d = empty, want a free token", i+1)
		}
		if i == 0 {
			firstToken = token
		}
		if token != firstToken {
			t.Fatalf("GetTextAccessToken() call %d = %q, want %q", i+1, token, firstToken)
		}
	}

	secondToken := ""
	for i := 0; i < 10; i++ {
		token := accounts.GetTextAccessToken()
		if token == "" {
			t.Fatalf("GetTextAccessToken() call %d = empty, want a free token", i+11)
		}
		if i == 0 {
			secondToken = token
			if secondToken == firstToken {
				t.Fatalf("GetTextAccessToken() after first cooldown = %q, want a different token", token)
			}
		}
		if token != secondToken {
			t.Fatalf("GetTextAccessToken() call %d = %q, want %q", i+11, token, secondToken)
		}
	}

	burstToken := accounts.GetTextAccessToken()
	if burstToken != firstToken && burstToken != secondToken {
		t.Fatalf("GetTextAccessToken() after both cooling = %q, want %q or %q", burstToken, firstToken, secondToken)
	}
	burstUntil := accounts.freeTextCooldownUntil[burstToken]
	if burstUntil.IsZero() {
		t.Fatalf("cooldown until for %q was not recorded", burstToken)
	}
	otherToken := firstToken
	if burstToken == firstToken {
		otherToken = secondToken
	}
	for i := 1; i < 10; i++ {
		if token := accounts.GetTextAccessToken(); token != burstToken {
			t.Fatalf("GetTextAccessToken() burst call %d = %q, want %q", i+1, token, burstToken)
		}
	}
	if got := accounts.freeTextCooldownUntil[burstToken]; !got.Equal(burstUntil) {
		t.Fatalf("cooldown until for %q changed from %v to %v", burstToken, burstUntil, got)
	}

	accounts.freeTextCooldownUntil[otherToken] = time.Now().Add(-time.Second)
	if token := accounts.GetTextAccessToken(); token != otherToken {
		t.Fatalf("GetTextAccessToken() after burst expiry = %q, want %q", token, otherToken)
	}
}

func TestGetTextAccessTokenKeepsPaidAccountsAvailableAfterSoftLimit(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"plus-token"})
	accounts.UpdateAccount("plus-token", map[string]any{"status": "正常", "type": "Plus"})

	for i := 0; i < 12; i++ {
		if token := accounts.GetTextAccessToken(); token != "plus-token" {
			t.Fatalf("GetTextAccessToken() call %d = %q, want plus-token", i+1, token)
		}
	}
}

func TestReserveNextCandidateTokenCanFilterPaidAccounts(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"free-token", "plus-token"})
	accounts.UpdateAccount("free-token", map[string]any{"status": "正常", "quota": 5, "type": "Free"})
	accounts.UpdateAccount("plus-token", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})

	reservation, err := accounts.reserveNextCandidateToken(map[string]struct{}{}, IsPaidImageAccount)
	if err != nil {
		t.Fatalf("reserveNextCandidateToken() error = %v", err)
	}
	if reservation.token != "plus-token" {
		t.Fatalf("reserved token = %q, want plus-token", reservation.token)
	}
	accounts.releaseImageReservation(reservation.token)

	_, err = accounts.reserveNextCandidateToken(map[string]struct{}{"plus-token": struct{}{}}, IsPaidImageAccount)
	if err == nil {
		t.Fatal("reserveNextCandidateToken() error = nil, want no available paid token")
	}
}

func TestApplyAccountErrorMessageDetectsImageStreamFailures(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-invalid", "token-limited"})
	accounts.UpdateAccount("token-invalid", map[string]any{"status": "正常", "quota": 5})
	accounts.UpdateAccount("token-limited", map[string]any{"status": "正常", "quota": 5, "image_quota_unknown": true})

	message, handled := accounts.ApplyAccountErrorMessage("token-invalid", "image_stream", "auth_chat_requirements failed: status=401, body={\"detail\":\"token_invalidated\"}")
	if !handled || message != "检测到封号" {
		t.Fatalf("invalid handled = %v message = %q, want 检测到封号", handled, message)
	}
	if account := accounts.GetAccount("token-invalid"); account["status"] != "异常" || account["quota"] != 0 {
		t.Fatalf("invalid account = %#v, want status 异常 quota 0", account)
	}

	message, handled = accounts.ApplyAccountErrorMessage("token-limited", "image_stream", "You've reached the image generation limit for now.")
	if !handled || message != "检测到限流" {
		t.Fatalf("limited handled = %v message = %q, want 检测到限流", handled, message)
	}
	if account := accounts.GetAccount("token-limited"); account["status"] != "限流" || account["quota"] != 0 || account["image_quota_unknown"] != false {
		t.Fatalf("limited account = %#v, want status 限流 quota 0 known quota", account)
	}
}

func TestApplyAccountErrorMessageIgnoresBootstrapFailures(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "quota": 5})

	message, handled := accounts.ApplyAccountErrorMessage("token-1", "refresh_accounts", "bootstrap failed: HTTP 429, body=too many requests")
	if handled {
		t.Fatalf("handled = true message = %q, want ignored bootstrap failure", message)
	}
	account := accounts.GetAccount("token-1")
	if account["status"] != "正常" || account["quota"] != 5 {
		t.Fatalf("account = %#v, want unchanged normal account", account)
	}
}

func TestStartAccountRefreshWatcherSkipsAccountBeforeRestoreTime(t *testing.T) {
	var mu sync.Mutex
	meCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/backend-api/me" {
			mu.Lock()
			meCalls++
			mu.Unlock()
		}
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			writeJSON(t, w, map[string]any{
				"default_model_slug": "gpt-5",
				"limits_progress": []map[string]any{{
					"feature_name": "image_gen",
					"remaining":    0,
					"reset_after":  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{
		"status":     "限流",
		"quota":      0,
		"restore_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	accounts.StartAccountRefreshWatcher(ctx, 20*time.Millisecond)
	time.Sleep(80 * time.Millisecond)

	mu.Lock()
	got := meCalls
	mu.Unlock()
	if got != 0 {
		t.Fatalf("refresh watcher refreshed account before restore time: /backend-api/me calls = %d, want 0", got)
	}
}

func TestSummarizeRefreshErrorBodyPrefersJSONMessage(t *testing.T) {
	got := summarizeRefreshErrorBody([]byte(`{"error":{"message":"You've reached the image generation limit"}}`))
	if got != "body=You've reached the image generation limit" {
		t.Fatalf("summarizeRefreshErrorBody() = %q", got)
	}
}

func TestAcquireTextAccessTokenLoadBalanceUsesLeastUsedIdlePaid(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"paid-1", "paid-2", "paid-3"})
	accounts.UpdateAccount("paid-1", map[string]any{"status": "正常", "type": "Plus"})
	accounts.UpdateAccount("paid-2", map[string]any{"status": "正常", "type": "Plus"})
	accounts.UpdateAccount("paid-3", map[string]any{"status": "正常", "type": "Plus"})
	accounts.textRequestCount["paid-1"] = 9
	accounts.textRequestCount["paid-2"] = 1
	accounts.textRequestCount["paid-3"] = 8

	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() error = %v", err)
	}
	defer lease.Release()
	if lease.Token != "paid-2" {
		t.Fatalf("token = %q, want least-used paid-2", lease.Token)
	}
	if accounts.textRequestCount["paid-2"] != 2 {
		t.Fatalf("paid-2 count = %d, want 2", accounts.textRequestCount["paid-2"])
	}
}

func TestAcquireTextAccessTokenLoadBalanceConsidersFreeAccounts(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"paid-1", "free-1"})
	accounts.UpdateAccount("paid-1", map[string]any{"status": "正常", "type": "Plus"})
	accounts.UpdateAccount("free-1", map[string]any{"status": "正常", "type": "Free"})
	accounts.textRequestCount["paid-1"] = 9
	accounts.textRequestCount["free-1"] = 0

	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() error = %v", err)
	}
	defer lease.Release()
	if lease.Token != "free-1" {
		t.Fatalf("token = %q, want least-used free-1 across all idle accounts", lease.Token)
	}
	if accounts.textRequestCount["free-1"] != 1 {
		t.Fatalf("free-1 count = %d, want 1", accounts.textRequestCount["free-1"])
	}
}

func TestAccountLeaseBusyTokenBlocksImageWhileTextInFlight(t *testing.T) {
	accounts := newTestAccountService(t)
	server := newAccountQuotaServer(t, map[string]any{"email": "user@example.com", "id": "user-1", "plan_type": "plus"}, []map[string]any{{
		"feature_name": "image_gen",
		"remaining":    5,
	}})
	defer server.Close()
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client { return server.Client() }
	accounts.AddAccounts([]string{"shared-token"})
	accounts.UpdateAccount("shared-token", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})

	textLease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() error = %v", err)
	}
	if textLease.Token != "shared-token" {
		t.Fatalf("text token = %q, want shared-token", textLease.Token)
	}
	if lease, err := accounts.GetAvailableImageAccessTokenFor(context.Background(), func(account map[string]any) bool {
		return util.Clean(account["access_token"]) == "shared-token"
	}); err == nil {
		lease.Release()
		t.Fatalf("GetAvailableImageAccessTokenFor() succeeded while text lease busy")
	}

	textLease.Release()
	imageLease, err := accounts.GetAvailableImageAccessTokenFor(context.Background(), func(account map[string]any) bool {
		return util.Clean(account["access_token"]) == "shared-token"
	})
	if err != nil {
		t.Fatalf("GetAvailableImageAccessTokenFor() after release error = %v", err)
	}
	if imageLease.Token != "shared-token" {
		t.Fatalf("image token = %q, want shared-token", imageLease.Token)
	}
	imageLease.Release()
	accounts.MarkImageResult("shared-token", false)
}

func TestSelectWeightedImageTokenPrefersHigherQuota(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.random = rand.New(rand.NewSource(1))
	accounts.AddAccounts([]string{"quota-high", "quota-low", "quota-unknown"})
	accounts.UpdateAccount("quota-high", map[string]any{"status": "正常", "quota": 20, "type": "Plus"})
	accounts.UpdateAccount("quota-low", map[string]any{"status": "正常", "quota": 1, "type": "Plus"})
	accounts.UpdateAccount("quota-unknown", map[string]any{"status": "正常", "quota": 0, "image_quota_unknown": true, "type": "Plus"})

	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		lease, reservation, err := accounts.acquireImageCandidateLease(nil, nil)
		if err != nil {
			t.Fatalf("acquireImageCandidateLease() error = %v", err)
		}
		counts[lease.Token]++
		lease.Release()
		accounts.releaseImageReservation(reservation.token)
	}
	if counts["quota-high"] <= counts["quota-low"]*5 || counts["quota-high"] <= counts["quota-unknown"]*5 {
		t.Fatalf("weighted counts = %#v, want high quota selected much more often", counts)
	}
	if imageAccountWeight(accounts.GetAccount("quota-unknown")) != 1 {
		t.Fatalf("unknown quota weight = %d, want 1", imageAccountWeight(accounts.GetAccount("quota-unknown")))
	}
}

func TestImageAccountWeightUsesRemainingImageSlots(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"quota-high", "quota-low", "quota-unknown"})
	accounts.UpdateAccount("quota-high", map[string]any{"status": "正常", "quota": 20, "type": "Plus"})
	accounts.UpdateAccount("quota-low", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})
	accounts.UpdateAccount("quota-unknown", map[string]any{"status": "正常", "quota": 0, "image_quota_unknown": true, "type": "Plus"})
	accounts.imageReservations["quota-high"] = 19

	accounts.mu.Lock()
	defer accounts.mu.Unlock()
	if got := accounts.imageAccountWeightLocked(accounts.items[0]); got != 1 {
		t.Fatalf("high quota remaining weight = %d, want 1", got)
	}
	if got := accounts.imageAccountWeightLocked(accounts.items[1]); got != 5 {
		t.Fatalf("low quota remaining weight = %d, want 5", got)
	}
	if got := accounts.imageAccountWeightLocked(accounts.items[2]); got != 1 {
		t.Fatalf("unknown quota weight = %d, want 1", got)
	}
}

func TestFillFirstTextAndImageStickyAreIndependentAndSkipBusy(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{textMode: "fill_first", imageMode: "fill_first"})
	accounts.random = rand.New(rand.NewSource(3))
	accounts.AddAccounts([]string{"token-a", "token-b"})
	accounts.UpdateAccount("token-a", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})
	accounts.UpdateAccount("token-b", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})

	firstText, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("first AcquireTextAccessToken() error = %v", err)
	}
	firstTextToken := firstText.Token
	firstText.Release()
	secondText, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("second AcquireTextAccessToken() error = %v", err)
	}
	if secondText.Token != firstTextToken {
		t.Fatalf("second text token = %q, want sticky %q", secondText.Token, firstTextToken)
	}
	secondText.Release()

	textSticky := accounts.stickyTextToken
	imageLease, reservation, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		t.Fatalf("acquireImageCandidateLease() error = %v", err)
	}
	imageSticky := imageLease.Token
	imageLease.Release()
	accounts.releaseImageReservation(reservation.token)
	if accounts.stickyTextToken != textSticky {
		t.Fatalf("image selection changed text sticky: got %q want %q", accounts.stickyTextToken, textSticky)
	}
	if accounts.stickyImageToken != imageSticky {
		t.Fatalf("stickyImageToken = %q, want %q", accounts.stickyImageToken, imageSticky)
	}

	busyText, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("busy AcquireTextAccessToken() error = %v", err)
	}
	if busyText.Token != firstTextToken {
		busyText.Release()
		t.Fatalf("busy text token = %q, want sticky %q", busyText.Token, firstTextToken)
	}
	otherText, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		busyText.Release()
		t.Fatalf("AcquireTextAccessToken() with sticky busy error = %v", err)
	}
	if otherText.Token == firstTextToken {
		otherText.Release()
		busyText.Release()
		t.Fatalf("text scheduler reused busy sticky token %q", firstTextToken)
	}
	otherText.Release()
	busyText.Release()

	firstImage, firstReservation, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		t.Fatalf("first acquireImageCandidateLease() error = %v", err)
	}
	if firstImage.Token != imageSticky {
		firstImage.Release()
		accounts.releaseImageReservation(firstReservation.token)
		t.Fatalf("first image token = %q, want sticky %q", firstImage.Token, imageSticky)
	}
	secondImage, secondReservation, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		firstImage.Release()
		accounts.releaseImageReservation(firstReservation.token)
		t.Fatalf("second acquireImageCandidateLease() with sticky busy error = %v", err)
	}
	if secondImage.Token == imageSticky {
		secondImage.Release()
		accounts.releaseImageReservation(secondReservation.token)
		firstImage.Release()
		accounts.releaseImageReservation(firstReservation.token)
		t.Fatalf("image scheduler reused busy sticky token %q", imageSticky)
	}
	secondImage.Release()
	accounts.releaseImageReservation(secondReservation.token)
	firstImage.Release()
	accounts.releaseImageReservation(firstReservation.token)
}

func TestFillFirstTextRoundRobinWhenStickyBusy(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{textMode: "fill_first"})
	accounts.random = rand.New(rand.NewSource(42))
	accounts.AddAccounts([]string{"token-a", "token-b", "token-c"})
	accounts.UpdateAccount("token-a", map[string]any{"status": "正常", "type": "Plus"})
	accounts.UpdateAccount("token-b", map[string]any{"status": "正常", "type": "Plus"})
	accounts.UpdateAccount("token-c", map[string]any{"status": "正常", "type": "Plus"})

	// First acquire establishes sticky token
	first, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("first AcquireTextAccessToken() error = %v", err)
	}
	stickyToken := first.Token
	first.Release()

	// Verify sticky is reused when idle
	second, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("second AcquireTextAccessToken() error = %v", err)
	}
	if second.Token != stickyToken {
		second.Release()
		t.Fatalf("expected sticky reuse, got %q want %q", second.Token, stickyToken)
	}
	// Keep second busy so sticky is occupied
	busyLease := second

	// Third acquire should round-robin to a different account and set it as new sticky
	third, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		busyLease.Release()
		t.Fatalf("third AcquireTextAccessToken() error = %v", err)
	}
	if third.Token == stickyToken {
		third.Release()
		busyLease.Release()
		t.Fatalf("round-robin should skip busy sticky token %q", stickyToken)
	}
	newStickyToken := third.Token
	// Keep third busy too so both original sticky and new sticky are occupied
	busyLease2 := third

	// Fourth acquire: both sticky tokens are busy, should pick the remaining account
	fourth, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		busyLease.Release()
		busyLease2.Release()
		t.Fatalf("fourth AcquireTextAccessToken() error = %v", err)
	}
	if fourth.Token == stickyToken || fourth.Token == newStickyToken {
		fourth.Release()
		busyLease.Release()
		busyLease2.Release()
		t.Fatalf("round-robin should pick remaining account, got %q (busy sticky=%q, busy new sticky=%q)", fourth.Token, stickyToken, newStickyToken)
	}
	fourth.Release()
	busyLease2.Release()
	busyLease.Release()

	// After all released, original sticky should be preferred again (it re-enters candidates)
	fifth, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("fifth AcquireTextAccessToken() error = %v", err)
	}
	defer fifth.Release()
	// The current sticky is whatever was last set during the fourth acquire's fallback.
	// With fill_first semantics, any idle account in candidates is valid; just verify no error.
	if fifth.Token == "" {
		t.Fatalf("fifth acquire returned empty token")
	}
}

func TestFillFirstImageRoundRobinWhenStickyBusy(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{imageMode: "fill_first"})
	accounts.random = rand.New(rand.NewSource(99))
	accounts.AddAccounts([]string{"img-a", "img-b", "img-c"})
	accounts.UpdateAccount("img-a", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})
	accounts.UpdateAccount("img-b", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})
	accounts.UpdateAccount("img-c", map[string]any{"status": "正常", "quota": 5, "type": "Plus"})

	// First acquire establishes sticky
	firstLease, firstRes, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		t.Fatalf("first acquireImageCandidateLease() error = %v", err)
	}
	stickyToken := firstLease.Token
	firstLease.Release()
	accounts.releaseImageReservation(firstRes.token)

	// Occupy sticky so it becomes busy
	busyLease, busyRes, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		t.Fatalf("busy acquireImageCandidateLease() error = %v", err)
	}
	if busyLease.Token != stickyToken {
		busyLease.Release()
		accounts.releaseImageReservation(busyRes.token)
		t.Fatalf("expected sticky %q, got %q", stickyToken, busyLease.Token)
	}

	// Next acquire should round-robin to a different account and establish new sticky
	secondLease, secondRes, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		busyLease.Release()
		accounts.releaseImageReservation(busyRes.token)
		t.Fatalf("second acquireImageCandidateLease() error = %v", err)
	}
	if secondLease.Token == stickyToken {
		secondLease.Release()
		accounts.releaseImageReservation(secondRes.token)
		busyLease.Release()
		accounts.releaseImageReservation(busyRes.token)
		t.Fatalf("image round-robin should skip busy sticky %q", stickyToken)
	}
	newStickyToken := secondLease.Token
	secondLease.Release()
	accounts.releaseImageReservation(secondRes.token)

	// Third acquire: new sticky is idle, so fill_first should reuse it (not rotate again)
	thirdLease, thirdRes, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		busyLease.Release()
		accounts.releaseImageReservation(busyRes.token)
		t.Fatalf("third acquireImageCandidateLease() error = %v", err)
	}
	if thirdLease.Token != newStickyToken {
		thirdLease.Release()
		accounts.releaseImageReservation(thirdRes.token)
		busyLease.Release()
		accounts.releaseImageReservation(busyRes.token)
		t.Fatalf("fill_first should stick to new sticky %q when idle, got %q", newStickyToken, thirdLease.Token)
	}
	thirdLease.Release()
	accounts.releaseImageReservation(thirdRes.token)
	busyLease.Release()
	accounts.releaseImageReservation(busyRes.token)

	// After all released, original sticky should be reusable again
	fourthLease, fourthRes, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		t.Fatalf("fourth acquireImageCandidateLease() error = %v", err)
	}
	// With fill_first semantics, any idle account in candidates is valid; just verify no error.
	if fourthLease.Token == "" {
		fourthLease.Release()
		accounts.releaseImageReservation(fourthRes.token)
		t.Fatalf("fourth acquire returned empty token")
	}
	fourthLease.Release()
	accounts.releaseImageReservation(fourthRes.token)
}

func TestAcquireTextAccessTokenSkipsRateLimitedAccounts(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"limited-paid", "normal-free"})
	accounts.UpdateAccount("limited-paid", map[string]any{"status": "限流", "type": "Plus"})
	accounts.UpdateAccount("normal-free", map[string]any{"status": "正常", "type": "Free"})

	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() error = %v", err)
	}
	defer lease.Release()
	if lease.Token != "normal-free" {
		t.Fatalf("text token = %q, want normal-free", lease.Token)
	}
}

func TestRefreshAccountViaSessionMigratesBusyTokenCounts(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"old-token", "new-token"})
	accounts.UpdateAccount("old-token", map[string]any{"status": "刷新中", "session_token": "old-session"})
	accounts.busyTokens["old-token"] = 2
	accounts.busyTokens["new-token"] = 3

	if !accounts.RefreshAccountViaSession("old-token", SessionRefreshData{AccessToken: "new-token", SessionToken: "new-session", Expires: "2026-05-20T00:00:00Z"}) {
		t.Fatal("RefreshAccountViaSession() = false")
	}
	if _, ok := accounts.busyTokens["old-token"]; ok {
		t.Fatalf("old busy token still present: %#v", accounts.busyTokens)
	}
	if got := accounts.busyTokens["new-token"]; got != 5 {
		t.Fatalf("new busy count = %d, want 5", got)
	}

	accounts.releaseBusyToken("old-token")
	if got := accounts.busyTokens["new-token"]; got != 4 {
		t.Fatalf("new busy count after first old release = %d, want 4", got)
	}
	if accounts.busyTokenAliases["old-token"] != "new-token" {
		t.Fatalf("old token alias removed before all old leases released: %#v", accounts.busyTokenAliases)
	}
	accounts.releaseBusyToken("old-token")
	if got := accounts.busyTokens["new-token"]; got != 3 {
		t.Fatalf("new busy count after second old release = %d, want 3", got)
	}
	if _, ok := accounts.busyTokenAliases["old-token"]; ok {
		t.Fatalf("old token alias still present after all old leases released: %#v", accounts.busyTokenAliases)
	}
}

func TestRefreshAccountViaSessionAllowsOldLeaseToReleaseMigratedBusyToken(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{textMode: "fill_first"})
	accounts.AddAccounts([]string{"old-token", "new-token"})
	accounts.UpdateAccount("old-token", map[string]any{"status": "正常", "type": "Plus", "session_token": "old-session"})
	accounts.UpdateAccount("new-token", map[string]any{"status": "正常", "type": "Plus"})
	accounts.stickyTextToken = "old-token"

	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() error = %v", err)
	}
	if lease.Token != "old-token" {
		lease.Release()
		t.Fatalf("lease token = %q, want old-token", lease.Token)
	}

	if !accounts.RefreshAccountViaSession("old-token", SessionRefreshData{AccessToken: "new-token", SessionToken: "new-session", Expires: "2026-05-20T00:00:00Z"}) {
		lease.Release()
		t.Fatal("RefreshAccountViaSession() = false")
	}
	if got := accounts.busyTokens["new-token"]; got != 1 {
		lease.Release()
		t.Fatalf("new token busy count after migration = %d, want 1", got)
	}

	lease.Release()
	lease.Release()
	if got := accounts.busyTokens["new-token"]; got != 0 {
		t.Fatalf("new token busy count after old lease release = %d, want 0", got)
	}
	if _, ok := accounts.busyTokenAliases["old-token"]; ok {
		t.Fatalf("old token alias still present after lease release: %#v", accounts.busyTokenAliases)
	}

	next, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() after release error = %v", err)
	}
	defer next.Release()
	if next.Token != "new-token" {
		t.Fatalf("next lease token = %q, want new-token", next.Token)
	}
}

func TestRefreshAccountViaSessionMigratesImageReservationsWithOldTokenAlias(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"old-token", "new-token"})
	accounts.UpdateAccount("old-token", map[string]any{"status": "刷新中", "type": "Plus", "session_token": "old-session", "quota": 1})
	accounts.UpdateAccount("new-token", map[string]any{"status": "正常", "type": "Plus", "quota": 10})
	accounts.imageReservations["old-token"] = 2
	accounts.imageReservations["new-token"] = 3
	accounts.textRequestCount["old-token"] = 2
	accounts.textRequestCount["new-token"] = 3

	if !accounts.RefreshAccountViaSession("old-token", SessionRefreshData{AccessToken: "new-token", SessionToken: "new-session", Expires: "2026-05-20T00:00:00Z"}) {
		t.Fatal("RefreshAccountViaSession() = false")
	}
	if _, ok := accounts.imageReservations["old-token"]; ok {
		t.Fatalf("old image reservation still present: %#v", accounts.imageReservations)
	}
	if got := accounts.imageReservations["new-token"]; got != 5 {
		t.Fatalf("new image reservation count after migration = %d, want 5", got)
	}
	if got := accounts.textRequestCount["new-token"]; got != 5 {
		t.Fatalf("new text request count after migration = %d, want 5", got)
	}

	accounts.MarkImageResult("old-token", false)
	updated := accounts.GetAccount("new-token")
	if updated == nil {
		t.Fatal("new token account missing after MarkImageResult(false)")
	}
	if got := util.ToInt(updated["fail"], 0); got != 1 {
		t.Fatalf("fail count after old MarkImageResult(false) = %d, want 1", got)
	}
	if got := util.ToInt(updated["success"], 0); got != 0 {
		t.Fatalf("success count after old MarkImageResult(false) = %d, want 0", got)
	}
	if got := util.ToInt(updated["quota"], -1); got != 1 {
		t.Fatalf("quota after old MarkImageResult(false) = %d, want 1", got)
	}
	if updated["status"] != "正常" {
		t.Fatalf("status after old MarkImageResult(false) = %#v, want 正常", updated["status"])
	}
	if util.Clean(updated["last_used_at"]) == "" {
		t.Fatalf("last_used_at after old MarkImageResult(false) = %#v, want populated", updated["last_used_at"])
	}
	if got := accounts.imageReservations["new-token"]; got != 4 {
		t.Fatalf("new image reservation count after old MarkImageResult = %d, want 4", got)
	}
	if accounts.imageReservationAliases["old-token"] != "new-token" {
		t.Fatalf("old image reservation alias removed before all old reservations released: %#v", accounts.imageReservationAliases)
	}

	accounts.MarkImageResult("old-token", true)
	updated = accounts.GetAccount("new-token")
	if updated == nil {
		t.Fatal("new token account missing after MarkImageResult(true)")
	}
	if got := util.ToInt(updated["fail"], 0); got != 1 {
		t.Fatalf("fail count after old MarkImageResult(true) = %d, want 1", got)
	}
	if got := util.ToInt(updated["success"], 0); got != 1 {
		t.Fatalf("success count after old MarkImageResult(true) = %d, want 1", got)
	}
	if got := util.ToInt(updated["quota"], -1); got != 0 {
		t.Fatalf("quota after old MarkImageResult(true) = %d, want 0", got)
	}
	if updated["status"] != "限流" {
		t.Fatalf("status after old MarkImageResult(true) = %#v, want 限流", updated["status"])
	}
	if util.Clean(updated["last_used_at"]) == "" {
		t.Fatalf("last_used_at after old MarkImageResult(true) = %#v, want populated", updated["last_used_at"])
	}
	if got := accounts.imageReservations["new-token"]; got != 3 {
		t.Fatalf("new image reservation count after second old MarkImageResult = %d, want 3", got)
	}
	if _, ok := accounts.imageReservationAliases["old-token"]; ok {
		t.Fatalf("old image reservation alias still present after all old reservations released: %#v", accounts.imageReservationAliases)
	}
}

func TestUpdateAccountFromSessionImportMigratesImageReservationOldRelease(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"old-token", "new-token"})
	accounts.UpdateAccount("old-token", map[string]any{"status": "正常", "type": "Plus", "user_id": "user-1"})
	accounts.UpdateAccount("new-token", map[string]any{"status": "正常", "type": "Plus"})
	accounts.imageReservations["old-token"] = 1
	accounts.imageReservations["new-token"] = 1
	accounts.textRequestCount["old-token"] = 2
	accounts.textRequestCount["new-token"] = 3

	if !accounts.UpdateAccountFromSessionImport("old-token", "new-token", map[string]any{"session_token": "new-session"}, true) {
		t.Fatal("UpdateAccountFromSessionImport() = false")
	}
	if got := accounts.imageReservations["new-token"]; got != 2 {
		t.Fatalf("new image reservation count after import migration = %d, want 2", got)
	}
	if got := accounts.textRequestCount["new-token"]; got != 5 {
		t.Fatalf("new text request count after import migration = %d, want 5", got)
	}

	accounts.releaseImageReservation("old-token")
	if got := accounts.imageReservations["new-token"]; got != 1 {
		t.Fatalf("new image reservation count after old release = %d, want 1", got)
	}
	if _, ok := accounts.imageReservationAliases["old-token"]; ok {
		t.Fatalf("old image reservation alias still present after old release: %#v", accounts.imageReservationAliases)
	}
}

func TestUpdateAccountFromSessionImportAllowsOldLeaseToReleaseMigratedBusyToken(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{textMode: "fill_first"})
	accounts.AddAccounts([]string{"old-token", "new-token"})
	accounts.UpdateAccount("old-token", map[string]any{"status": "正常", "type": "Plus", "user_id": "user-1"})
	accounts.UpdateAccount("new-token", map[string]any{"status": "正常", "type": "Plus"})
	accounts.stickyTextToken = "old-token"

	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() error = %v", err)
	}
	if lease.Token != "old-token" {
		lease.Release()
		t.Fatalf("lease token = %q, want old-token", lease.Token)
	}

	if !accounts.UpdateAccountFromSessionImport("old-token", "new-token", map[string]any{"session_token": "new-session"}, true) {
		lease.Release()
		t.Fatal("UpdateAccountFromSessionImport() = false")
	}
	if got := accounts.busyTokens["new-token"]; got != 1 {
		lease.Release()
		t.Fatalf("new token busy count after import migration = %d, want 1", got)
	}

	lease.Release()
	if got := accounts.busyTokens["new-token"]; got != 0 {
		t.Fatalf("new token busy count after old lease release = %d, want 0", got)
	}
}

func TestSetAccountsEnabledByIDsDisablesSchedulingWithoutChangingStatus(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "type": "Plus", "quota": 5})

	id := accountIDFromToken("token-1")
	result := accounts.SetAccountsEnabledByIDs([]string{id}, false)
	if result["updated"] != 1 || result["skipped"] != 0 {
		t.Fatalf("disable result = %#v, want updated=1 skipped=0", result)
	}

	account := accounts.GetAccount("token-1")
	if account["status"] != "正常" {
		t.Fatalf("status after disable = %#v, want 正常", account["status"])
	}
	if account["enabled"] != false {
		t.Fatalf("enabled after disable = %#v, want false", account["enabled"])
	}
	if IsImageAccountAvailable(account) {
		t.Fatal("disabled account should not be available for image scheduling")
	}
	if _, err := accounts.AcquireTextAccessToken(nil); err == nil {
		t.Fatal("disabled account should not be available for text scheduling")
	}

	result = accounts.SetAccountsEnabledByIDs([]string{id}, true)
	if result["updated"] != 1 || result["skipped"] != 0 {
		t.Fatalf("enable result = %#v, want updated=1 skipped=0", result)
	}

	account = accounts.GetAccount("token-1")
	if account["status"] != "正常" {
		t.Fatalf("status after enable = %#v, want 正常", account["status"])
	}
	if account["enabled"] != true {
		t.Fatalf("enabled after enable = %#v, want true", account["enabled"])
	}
	if !IsImageAccountAvailable(account) {
		t.Fatal("enabled account should be available for image scheduling")
	}
	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() after enable error = %v", err)
	}
	lease.Release()
}

func TestSetAccountsEnabledByIDsIsIdempotent(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1", "token-2"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "type": "Plus", "quota": 5})
	accounts.UpdateAccount("token-2", map[string]any{"status": "正常", "type": "Plus", "quota": 5, "enabled": false})

	result := accounts.SetAccountsEnabledByIDs([]string{accountIDFromToken("token-1"), accountIDFromToken("token-2")}, false)
	if result["updated"] != 1 || result["skipped"] != 1 {
		t.Fatalf("batch disable result = %#v, want updated=1 skipped=1", result)
	}

	result = accounts.SetAccountsEnabledByIDs([]string{accountIDFromToken("token-1"), accountIDFromToken("token-2")}, false)
	if result["updated"] != 0 || result["skipped"] != 2 {
		t.Fatalf("repeat disable result = %#v, want updated=0 skipped=2", result)
	}

	result = accounts.SetAccountsEnabledByIDs([]string{accountIDFromToken("token-1"), accountIDFromToken("token-2")}, true)
	if result["updated"] != 2 || result["skipped"] != 0 {
		t.Fatalf("batch enable result = %#v, want updated=2 skipped=0", result)
	}

	result = accounts.SetAccountsEnabledByIDs([]string{accountIDFromToken("token-1"), accountIDFromToken("token-2")}, true)
	if result["updated"] != 0 || result["skipped"] != 2 {
		t.Fatalf("repeat enable result = %#v, want updated=0 skipped=2", result)
	}
}

func TestLegacyDisabledStatusRemainsUnschedulableAndPubliclyDisabled(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.mu.Lock()
	accounts.items = []map[string]any{{
		"access_token":        "legacy-token",
		"type":                "Plus",
		"status":              "禁用",
		"quota":               5,
		"image_quota_unknown": false,
		"limits_progress":     []any{},
		"chatgpt_account_id":  nil,
		"default_model_slug":  nil,
		"restore_at":          nil,
		"success":             0,
		"fail":                0,
	}}
	accounts.mu.Unlock()

	account := accounts.GetAccount("legacy-token")
	if IsImageAccountAvailable(account) {
		t.Fatal("legacy status=禁用 account should not be available for image scheduling")
	}
	if _, err := accounts.AcquireTextAccessToken(nil); err == nil {
		t.Fatal("legacy status=禁用 account should not be available for text scheduling")
	}

	items := accounts.ListAccounts()
	if len(items) != 1 {
		t.Fatalf("ListAccounts() length = %d, want 1", len(items))
	}
	if items[0]["enabled"] != false {
		t.Fatalf("public enabled for legacy disabled account = %#v, want false", items[0]["enabled"])
	}
	if items[0]["status"] != "禁用" {
		t.Fatalf("public status for legacy disabled account = %#v, want 禁用", items[0]["status"])
	}
}

func TestAddAccountsDefaultsToEnabledAndListsIt(t *testing.T) {
	accounts := newTestAccountService(t)
	result := accounts.AddAccounts([]string{"token-1"})
	if result["added"] != 1 || result["skipped"] != 0 {
		t.Fatalf("AddAccounts() = %#v, want added=1 skipped=0", result)
	}

	account := accounts.GetAccount("token-1")
	if account["enabled"] != true {
		t.Fatalf("enabled after add = %#v, want true", account["enabled"])
	}
	if got := util.Clean(account["browser-family"]); got == "" {
		t.Fatal("browser-family should be set on new accounts")
	}
	if got := util.Clean(account["browser-version"]); got == "" {
		t.Fatal("browser-version should be set on new accounts")
	}
	fp, ok := account["fp"].(map[string]any)
	if !ok {
		t.Fatalf("account fp = %#v, want map", account["fp"])
	}
	if got := util.Clean(fp["browser-family"]); got != util.Clean(account["browser-family"]) {
		t.Fatalf("fp browser-family = %q, want %q", got, util.Clean(account["browser-family"]))
	}
	if got := util.Clean(fp["browser-version"]); got != util.Clean(account["browser-version"]) {
		t.Fatalf("fp browser-version = %q, want %q", got, util.Clean(account["browser-version"]))
	}
	if pools := BrowserFamilyVersionPools(); func() bool {
		versions, ok := pools[util.Clean(account["browser-family"])]
		if !ok {
			return false
		}
		for _, candidate := range versions {
			if candidate == util.Clean(account["browser-version"]) {
				return true
			}
		}
		return false
	}() == false {
		t.Fatalf("browser-family/version = %q/%q, want a real pooled version", account["browser-family"], account["browser-version"])
	}
	items := accounts.ListAccounts()
	if len(items) != 1 {
		t.Fatalf("ListAccounts() length = %d, want 1", len(items))
	}
	if items[0]["enabled"] != true {
		t.Fatalf("public enabled after add = %#v, want true", items[0]["enabled"])
	}
}

func TestGetAccountGeneratesAndPersistsFingerprint(t *testing.T) {
	backend := &accountStorageSpy{accounts: []map[string]any{{
		"access_token": "token-1",
		"type":         "Plus",
		"status":       "正常",
	}}}
	accounts := NewAccountService(backend, testAccountConfig{}, nil, NewLogService())
	if backend.saveCount != 0 {
		t.Fatalf("NewAccountService() saveCount = %d, want 0", backend.saveCount)
	}

	account := accounts.GetAccount("token-1")
	fp, ok := account["fp"].(map[string]any)
	if !ok {
		t.Fatalf("account fp = %#v, want map", account["fp"])
	}
	if got := util.Clean(fp["sec-ch-ua-full-version-list"]); !strings.Contains(got, browserNormalizeFullVersion(util.Clean(fp["browser-version"]))) {
		t.Fatalf("sec-ch-ua-full-version-list = %q, want to contain %q", got, browserNormalizeFullVersion(util.Clean(fp["browser-version"])))
	}
	if util.Clean(fp["oai-device-id"]) == "" || util.Clean(fp["oai-session-id"]) == "" {
		t.Fatalf("generated fp missing device/session: %#v", fp)
	}
	if util.Clean(fp["browser-family"]) == "" || util.Clean(fp["browser-version"]) == "" {
		t.Fatalf("generated fp missing browser family/version: %#v", fp)
	}
	if util.Clean(account["browser-family"]) != util.Clean(fp["browser-family"]) || util.Clean(account["browser-version"]) != util.Clean(fp["browser-version"]) {
		t.Fatalf("account browser family/version mismatch: account=%#v fp=%#v", account, fp)
	}
	if backend.saveCount != 1 {
		t.Fatalf("GetAccount() saveCount = %d, want 1", backend.saveCount)
	}
	savedFP, ok := backend.saved[0]["fp"].(map[string]any)
	if !ok {
		t.Fatalf("saved fp = %#v, want map", backend.saved[0]["fp"])
	}
	if savedFP["oai-device-id"] != fp["oai-device-id"] || savedFP["oai-session-id"] != fp["oai-session-id"] {
		t.Fatalf("saved fp = %#v, returned fp = %#v", savedFP, fp)
	}
}

func TestGetAccountGeneratesAndPersistsFingerprintForDisabledAccount(t *testing.T) {
	backend := &accountStorageSpy{accounts: []map[string]any{{
		"access_token": "token-1",
		"type":         "Plus",
		"status":       "禁用",
	}}}
	accounts := NewAccountService(backend, testAccountConfig{}, nil, NewLogService())

	account := accounts.GetAccount("token-1")
	if account == nil {
		t.Fatal("GetAccount() = nil, want account")
	}
	fp, ok := account["fp"].(map[string]any)
	if !ok {
		t.Fatalf("account fp = %#v, want map", account["fp"])
	}
	if got := util.Clean(fp["sec-ch-ua-full-version-list"]); !strings.Contains(got, browserNormalizeFullVersion(util.Clean(fp["browser-version"]))) {
		t.Fatalf("sec-ch-ua-full-version-list = %q, want to contain %q", got, browserNormalizeFullVersion(util.Clean(fp["browser-version"])))
	}
	if util.Clean(fp["oai-device-id"]) == "" || util.Clean(fp["oai-session-id"]) == "" {
		t.Fatalf("generated fp missing device/session: %#v", fp)
	}
	if util.Clean(fp["browser-family"]) == "" || util.Clean(fp["browser-version"]) == "" {
		t.Fatalf("generated fp missing browser family/version: %#v", fp)
	}
	if util.Clean(account["browser-family"]) != util.Clean(fp["browser-family"]) || util.Clean(account["browser-version"]) != util.Clean(fp["browser-version"]) {
		t.Fatalf("account browser family/version mismatch: account=%#v fp=%#v", account, fp)
	}
	if backend.saveCount != 1 {
		t.Fatalf("GetAccount() saveCount = %d, want 1", backend.saveCount)
	}
	savedFP, ok := backend.saved[0]["fp"].(map[string]any)
	if !ok {
		t.Fatalf("saved fp = %#v, want map", backend.saved[0]["fp"])
	}
	if savedFP["oai-device-id"] != fp["oai-device-id"] || savedFP["oai-session-id"] != fp["oai-session-id"] {
		t.Fatalf("saved fp = %#v, returned fp = %#v", savedFP, fp)
	}
}

// 池外的浏览器身份（此处 edge/143，surf 无法兑现 Edge TLS）迁移入池，
// 但账号的稳定身份 oai-device-id / oai-session-id 必须原样保留，
// 否则会与注册时写入的 oai-did cookie 脱节，造成新的身份撕裂。
func TestGetAccountMigratesOutOfPoolFingerprintAndKeepsIdentity(t *testing.T) {
	backend := &accountStorageSpy{accounts: []map[string]any{{
		"access_token": "token-1",
		"type":         "Plus",
		"status":       "正常",
		"fp": map[string]any{
			"version":         1,
			"browser-family":  "edge",
			"browser-version": "143",
			"impersonate":     "edge101",
			"user-agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
			"oai-device-id":   "device-1",
			"oai-session-id":  "session-1",
		},
	}}}
	accounts := NewAccountService(backend, testAccountConfig{}, nil, NewLogService())

	first := accounts.GetAccount("token-1")
	second := accounts.GetAccount("token-1")
	firstFP := first["fp"].(map[string]any)
	secondFP := second["fp"].(map[string]any)
	if firstFP["oai-device-id"] != "device-1" || firstFP["oai-session-id"] != "session-1" {
		t.Fatalf("first fp changed device/session: %#v", firstFP)
	}
	if secondFP["oai-device-id"] != "device-1" || secondFP["oai-session-id"] != "session-1" {
		t.Fatalf("second fp changed device/session: %#v", secondFP)
	}
	// edge 在 surf 中无可兑现的 TLS 指纹，迁移后落到 chrome 且必须在池内。
	if util.Clean(firstFP["browser-family"]) != "chrome" || util.Clean(firstFP["browser-version"]) != "145" {
		t.Fatalf("first fp family/version = %#v, want chrome/145", firstFP)
	}
	if !browserFamilyVersionInPool(util.Clean(firstFP["browser-family"]), util.Clean(firstFP["browser-version"])) {
		t.Fatalf("first fp family/version = %#v, want an in-pool version", firstFP)
	}
	// UA 与 Client-Hints 必须与迁移后的版本自洽。
	if got := util.Clean(firstFP["user-agent"]); !strings.Contains(got, "Chrome/145.0.0.0") {
		t.Fatalf("user-agent = %q, want Chrome/145.0.0.0", got)
	}
	if got := util.Clean(firstFP["sec-ch-ua-full-version"]); got != `"145.0.0.0"` {
		t.Fatalf("sec-ch-ua-full-version = %q, want \"145.0.0.0\"", got)
	}
	if util.Clean(secondFP["browser-family"]) != util.Clean(firstFP["browser-family"]) || util.Clean(secondFP["browser-version"]) != util.Clean(firstFP["browser-version"]) {
		t.Fatalf("second fp family/version = %#v, want stable %#v", secondFP, firstFP)
	}
}

func TestUpdateAccountFromSessionImportPreservesFingerprint(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"old-token"})
	before := accounts.GetAccount("old-token")
	beforeFP := before["fp"].(map[string]any)

	ok := accounts.UpdateAccountFromSessionImport("old-token", "new-token", map[string]any{
		"session_token": "session-1",
		"type":          "Plus",
		"status":        "正常",
	}, true)
	if !ok {
		t.Fatal("UpdateAccountFromSessionImport() = false, want true")
	}
	after := accounts.GetAccount("new-token")
	afterFP := after["fp"].(map[string]any)
	if afterFP["oai-device-id"] != beforeFP["oai-device-id"] || afterFP["oai-session-id"] != beforeFP["oai-session-id"] {
		t.Fatalf("fingerprint changed across token migration: before=%#v after=%#v", beforeFP, afterFP)
	}
	if util.Clean(afterFP["browser-family"]) != util.Clean(beforeFP["browser-family"]) || util.Clean(afterFP["browser-version"]) != util.Clean(beforeFP["browser-version"]) {
		t.Fatalf("family/version changed across token migration: before=%#v after=%#v", beforeFP, afterFP)
	}
}

func TestLoadAccountsDoesNotPersistEnabledForLegacyRecord(t *testing.T) {
	backend := &accountStorageSpy{accounts: []map[string]any{{
		"access_token":        "legacy-token",
		"type":                "Plus",
		"status":              "禁用",
		"quota":               5,
		"image_quota_unknown": false,
	}}}

	accounts := NewAccountService(backend, testAccountConfig{}, nil, NewLogService())
	if backend.saveCount != 0 {
		t.Fatalf("NewAccountService() saved legacy account %d times, want 0", backend.saveCount)
	}
	account := accounts.GetAccount("legacy-token")
	if _, ok := account["enabled"]; ok {
		t.Fatalf("legacy account gained enabled field during load: %#v", account)
	}
	if backend.saveCount != 1 {
		t.Fatalf("GetAccount() saved legacy account %d times, want 1 for fingerprint normalization", backend.saveCount)
	}

	items := accounts.ListAccounts()
	if len(items) != 1 {
		t.Fatalf("ListAccounts() length = %d, want 1", len(items))
	}
	if items[0]["enabled"] != false {
		t.Fatalf("public enabled for legacy account = %#v, want false", items[0]["enabled"])
	}
	if backend.saveCount != 1 {
		t.Fatalf("ListAccounts() saved legacy account %d times, want 1", backend.saveCount)
	}
}

func TestExplicitEnabledOverridesLegacyDisabledStatus(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.mu.Lock()
	accounts.items = []map[string]any{{
		"access_token":        "legacy-enabled-token",
		"enabled":             true,
		"type":                "Plus",
		"status":              "禁用",
		"quota":               5,
		"image_quota_unknown": false,
	}}
	accounts.mu.Unlock()

	account := accounts.GetAccount("legacy-enabled-token")
	if !IsImageAccountAvailable(account) {
		t.Fatal("explicit enabled=true account should be available for image scheduling even with legacy status=禁用")
	}
	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() error = %v", err)
	}
	if lease.Token != "legacy-enabled-token" {
		lease.Release()
		t.Fatalf("text lease token = %q, want legacy-enabled-token", lease.Token)
	}
	lease.Release()

	items := accounts.ListAccounts()
	if items[0]["enabled"] != true || items[0]["status"] != "禁用" {
		t.Fatalf("public account = %#v, want enabled=true with status=禁用 preserved", items[0])
	}
}

func TestSetAccountsEnabledByIDsClearsReservationsAndStickyState(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "type": "Plus", "quota": 5})
	accounts.mu.Lock()
	accounts.busyTokens["token-1"] = 2
	accounts.busyTokenAliases["busy-alias"] = "token-1"
	accounts.busyTokenAliasRefs["busy-alias"] = 1
	accounts.imageReservations["token-1"] = 1
	accounts.imageReservationAliases["image-alias"] = "token-1"
	accounts.imageReservationAliasRefs["image-alias"] = 1
	accounts.stickyTextToken = "token-1"
	accounts.stickyImageToken = "token-1"
	accounts.mu.Unlock()

	result := accounts.SetAccountsEnabledByIDs([]string{accountIDFromToken("token-1")}, false)
	if result["updated"] != 1 || result["skipped"] != 0 {
		t.Fatalf("disable result = %#v, want updated=1 skipped=0", result)
	}

	accounts.mu.Lock()
	defer accounts.mu.Unlock()
	if _, ok := accounts.busyTokens["token-1"]; ok {
		t.Fatalf("busy token not cleared: %#v", accounts.busyTokens)
	}
	if _, ok := accounts.busyTokenAliases["busy-alias"]; ok {
		t.Fatalf("busy alias not cleared: %#v", accounts.busyTokenAliases)
	}
	if _, ok := accounts.imageReservations["token-1"]; ok {
		t.Fatalf("image reservation not cleared: %#v", accounts.imageReservations)
	}
	if _, ok := accounts.imageReservationAliases["image-alias"]; ok {
		t.Fatalf("image alias not cleared: %#v", accounts.imageReservationAliases)
	}
	if accounts.stickyTextToken != "" || accounts.stickyImageToken != "" {
		t.Fatalf("sticky tokens = text %q image %q, want cleared", accounts.stickyTextToken, accounts.stickyImageToken)
	}
}

type accountStorageSpy struct {
	accounts  []map[string]any
	saveCount int
	saved     []map[string]any
}

func (s *accountStorageSpy) LoadAccounts() ([]map[string]any, error) {
	return copyAccountItems(s.accounts), nil
}

func (s *accountStorageSpy) SaveAccounts(accounts []map[string]any) error {
	s.saveCount++
	s.saved = copyAccountItems(accounts)
	return nil
}

func (s *accountStorageSpy) LoadAuthKeys() ([]map[string]any, error) {
	return nil, nil
}

func (s *accountStorageSpy) SaveAuthKeys([]map[string]any) error {
	return nil
}

func (s *accountStorageSpy) HealthCheck() map[string]any {
	return map[string]any{"status": "ok"}
}

func (s *accountStorageSpy) Info() map[string]any {
	return map[string]any{}
}

func copyAccountItems(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, util.CopyMap(item))
	}
	return out
}

func TestNormalizeBrowserFingerprintFillsMissingFields(t *testing.T) {
	fp, changed := NormalizeBrowserFingerprint(map[string]any{
		"user-agent":     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"oai-device-id":  "device-1",
		"oai-session-id": "session-1",
	})
	if !changed {
		t.Fatal("NormalizeBrowserFingerprint() changed = false, want true for missing fields")
	}
	for _, key := range []string{
		"version",
		"impersonate",
		"user-agent",
		"sec-ch-ua",
		"sec-ch-ua-mobile",
		"sec-ch-ua-platform",
		"sec-ch-ua-arch",
		"sec-ch-ua-bitness",
		"sec-ch-ua-full-version",
		"sec-ch-ua-full-version-list",
		"sec-ch-ua-platform-version",
		"oai-device-id",
		"oai-session-id",
	} {
		if _, ok := fp[key]; !ok {
			t.Fatalf("normalized fingerprint missing %s: %#v", key, fp)
		}
	}
	if fp["oai-device-id"] != "device-1" || fp["oai-session-id"] != "session-1" {
		t.Fatalf("device/session changed: %#v", fp)
	}
	if fp["sec-ch-ua"] != `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"` {
		t.Fatalf("sec-ch-ua = %#v", fp["sec-ch-ua"])
	}
}

func TestNormalizeBrowserFingerprintRegeneratesInvalidFingerprint(t *testing.T) {
	fp, changed := NormalizeBrowserFingerprint("not-a-map")
	if !changed {
		t.Fatal("NormalizeBrowserFingerprint() changed = false, want true for invalid input")
	}
	if fp["version"] != 1 {
		t.Fatalf("version = %#v, want 1", fp["version"])
	}
	if got := util.Clean(fp["sec-ch-ua-full-version-list"]); !strings.Contains(got, browserNormalizeFullVersion(util.Clean(fp["browser-version"]))) {
		t.Fatalf("sec-ch-ua-full-version-list = %q, want to contain %q", got, browserNormalizeFullVersion(util.Clean(fp["browser-version"])))
	}
	if util.Clean(fp["oai-device-id"]) == "" || util.Clean(fp["oai-session-id"]) == "" {
		t.Fatalf("device/session missing in regenerated fingerprint: %#v", fp)
	}
}

func TestNormalizeBrowserFingerprintAcceptsStringMap(t *testing.T) {
	fp, changed := NormalizeBrowserFingerprint(map[string]string{
		" User-Agent ":   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"oai-device-id":  " device-1 ",
		"oai-session-id": " session-1 ",
	})
	if !changed {
		t.Fatal("NormalizeBrowserFingerprint() changed = false, want true for missing fields in string map")
	}
	if got := util.Clean(fp["user-agent"]); got != "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0" {
		t.Fatalf("user-agent = %q", got)
	}
	if got := util.Clean(fp["oai-device-id"]); got != "device-1" {
		t.Fatalf("oai-device-id = %q, want device-1", got)
	}
	if got := util.Clean(fp["oai-session-id"]); got != "session-1" {
		t.Fatalf("oai-session-id = %q, want session-1", got)
	}
	if got := util.Clean(fp["sec-ch-ua"]); got != `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"` {
		t.Fatalf("sec-ch-ua = %q", got)
	}
	values := BrowserFingerprintStringMap(map[string]string{
		" User-Agent ":   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"oai-device-id":  " device-1 ",
		"oai-session-id": " session-1 ",
	})
	if got := values["sec-ch-ua-full-version"]; got != `"143.0.0.0"` {
		t.Fatalf("BrowserFingerprintStringMap()[sec-ch-ua-full-version] = %q, want %q", got, `"143.0.0.0"`)
	}
}

func TestNormalizeBrowserFingerprintReportsKeyNormalizationAsChange(t *testing.T) {
	userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0"
	fp, changed := NormalizeBrowserFingerprint(map[string]any{
		" Version ":                     1,
		" Impersonate ":                 "edge143",
		" User-Agent ":                  userAgent,
		" Sec-CH-UA ":                   `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`,
		" Sec-CH-UA-Mobile ":            "?0",
		" Sec-CH-UA-Platform ":          `"Windows"`,
		" Sec-CH-UA-Arch ":              `"x86"`,
		" Sec-CH-UA-Bitness ":           `"64"`,
		" Sec-CH-UA-Full-Version ":      `"143.0.0.0"`,
		" Sec-CH-UA-Full-Version-List ": `"Microsoft Edge";v="143.0.0.0", "Chromium";v="143.0.0.0", "Not A(Brand";v="24.0.0.0"`,
		" Sec-CH-UA-Platform-Version ":  `"19.0.0"`,
		" OAI-Device-ID ":               "device-1",
		" OAI-Session-ID ":              "session-1",
	})
	if !changed {
		t.Fatal("NormalizeBrowserFingerprint() changed = false, want true when keys are normalized")
	}
	for _, key := range []string{
		"version",
		"impersonate",
		"user-agent",
		"sec-ch-ua",
		"sec-ch-ua-mobile",
		"sec-ch-ua-platform",
		"sec-ch-ua-arch",
		"sec-ch-ua-bitness",
		"sec-ch-ua-full-version",
		"sec-ch-ua-full-version-list",
		"sec-ch-ua-platform-version",
		"oai-device-id",
		"oai-session-id",
	} {
		if _, ok := fp[key]; !ok {
			t.Fatalf("normalized fingerprint missing canonical key %s: %#v", key, fp)
		}
	}
}

func TestBrowserHeadersForFingerprintUsesNormalizedValues(t *testing.T) {
	fp, _ := NormalizeBrowserFingerprint(map[string]any{
		"user-agent":     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"oai-device-id":  "device-1",
		"oai-session-id": "session-1",
	})
	headers := BrowserHeadersForFingerprint(fp)
	for key, want := range map[string]string{
		"User-Agent":         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"Sec-Ch-Ua":          `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`,
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": `"Windows"`,
		"OAI-Device-Id":      "device-1",
		"OAI-Session-Id":     "session-1",
	} {
		if got := headers[key]; got != want {
			t.Fatalf("headers[%s] = %q, want %q", key, got, want)
		}
	}
}

func TestBrowserHeadersForFingerprintAcceptsStringMap(t *testing.T) {
	headers := BrowserHeadersForFingerprint(map[string]string{
		" User-Agent ":   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"oai-device-id":  " device-1 ",
		"oai-session-id": " session-1 ",
	})
	for key, want := range map[string]string{
		"User-Agent":                  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"Sec-Ch-Ua":                   `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`,
		"Sec-Ch-Ua-Mobile":            "?0",
		"Sec-Ch-Ua-Platform":          `"Windows"`,
		"Sec-Ch-Ua-Full-Version":      `"143.0.0.0"`,
		"Sec-Ch-Ua-Full-Version-List": `"Microsoft Edge";v="143.0.0.0", "Chromium";v="143.0.0.0", "Not A(Brand";v="24.0.0.0"`,
		"OAI-Device-Id":               "device-1",
		"OAI-Session-Id":              "session-1",
	} {
		if got := headers[key]; got != want {
			t.Fatalf("headers[%s] = %q, want %q", key, got, want)
		}
	}
}

// 指纹池只包含 TLS 指纹层（surf）能真正兑现的浏览器与版本。
// 池中出现 surf 无法兑现的族/版本，会让 UA 与 Client-Hints 版本号互相矛盾。
func TestBrowserFamilyVersionPoolsContainVerifiedVersions(t *testing.T) {
	want := map[string][]string{
		"chrome":  []string{"145"},
		"firefox": []string{"148"},
	}
	if got := BrowserFamilyVersionPools(); !reflect.DeepEqual(got, want) {
		t.Fatalf("BrowserFamilyVersionPools() = %#v, want %#v", got, want)
	}
}

// 池中每个族/版本都必须能映射到 surf 内置的 UA 主版本，
// 否则出站会出现「Sec-Ch-Ua 与 UA 版本不一致」的矛盾信号。
func TestBrowserFamilyVersionPoolsMatchSurfImpersonation(t *testing.T) {
	surfUserAgentMajor := map[string]string{
		"chrome":  "145",
		"firefox": "148",
	}
	for family, versions := range BrowserFamilyVersionPools() {
		wantMajor, ok := surfUserAgentMajor[family]
		if !ok {
			t.Fatalf("family %q has no surf impersonation backing", family)
		}
		for _, version := range versions {
			if version != wantMajor {
				t.Fatalf("family %q version %q does not match surf UA major %q", family, version, wantMajor)
			}
		}
	}
}

func TestBrowserFingerprintFromFamilyVersionBuildsExpectedFamilies(t *testing.T) {
	tests := []struct {
		name                string
		family              string
		version             string
		wantFamily          string
		wantVersion         string
		wantImpersonate     string
		wantUserAgent       string
		wantSecCHUAContains string
		wantFullVersion     string
	}{
		{name: "chrome145", family: "chrome", version: "145", wantFamily: "chrome", wantVersion: "145", wantImpersonate: "chrome145", wantUserAgent: "Chrome/145.0.0.0", wantSecCHUAContains: `Google Chrome";v="145`, wantFullVersion: `"145.0.0.0"`},
		{name: "firefox148", family: "firefox", version: "148", wantFamily: "firefox", wantVersion: "148", wantImpersonate: "firefox148", wantUserAgent: "Firefox/148.0", wantSecCHUAContains: `Firefox";v="148`, wantFullVersion: `"148.0.0.0"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := BrowserFingerprintFromFamilyVersion(tt.family, tt.version)
			if got := util.Clean(fp["browser-family"]); got != tt.wantFamily {
				t.Fatalf("browser-family = %q, want %q", got, tt.wantFamily)
			}
			if got := util.Clean(fp["browser-version"]); got != tt.wantVersion {
				t.Fatalf("browser-version = %q, want %q", got, tt.wantVersion)
			}
			if got := util.Clean(fp["impersonate"]); got != tt.wantImpersonate {
				t.Fatalf("impersonate = %q, want %q", got, tt.wantImpersonate)
			}
			if got := util.Clean(fp["user-agent"]); !strings.Contains(got, tt.wantUserAgent) {
				t.Fatalf("user-agent = %q, want to contain %q", got, tt.wantUserAgent)
			}
			if got := util.Clean(fp["sec-ch-ua"]); !strings.Contains(got, tt.wantSecCHUAContains) {
				t.Fatalf("sec-ch-ua = %q, want to contain %q", got, tt.wantSecCHUAContains)
			}
			if got := util.Clean(fp["sec-ch-ua-full-version"]); got != tt.wantFullVersion {
				t.Fatalf("sec-ch-ua-full-version = %q, want %q", got, tt.wantFullVersion)
			}
			if got := util.Clean(fp["sec-ch-ua-full-version-list"]); !strings.Contains(got, browserNormalizeFullVersion(util.Clean(fp["browser-version"]))) {
				t.Fatalf("sec-ch-ua-full-version-list = %q, want to contain %q", got, browserNormalizeFullVersion(util.Clean(fp["browser-version"])))
			}
			if util.Clean(fp["oai-device-id"]) == "" || util.Clean(fp["oai-session-id"]) == "" {
				t.Fatalf("fingerprint missing generated ids: %#v", fp)
			}
		})
	}
}

// 被移除的族（edge/safari）不再产生指纹，必须回落到默认 chrome145，
// 而不是生成一套 surf 无法兑现、UA 与 TLS 相互矛盾的身份。
func TestBrowserFingerprintFromFamilyVersionRejectsRemovedFamilies(t *testing.T) {
	for _, family := range []string{"edge", "safari"} {
		t.Run(family, func(t *testing.T) {
			fp := BrowserFingerprintFromFamilyVersion(family, "148")
			if got := util.Clean(fp["browser-family"]); got != "chrome" {
				t.Fatalf("browser-family = %q, want chrome fallback", got)
			}
			if got := util.Clean(fp["impersonate"]); got != "chrome145" {
				t.Fatalf("impersonate = %q, want chrome145 fallback", got)
			}
		})
	}
}

func TestBrowserFingerprintFromFamilyVersionFallsBackToDefault(t *testing.T) {
	fp := BrowserFingerprintFromFamilyVersion("bogus", "999")
	if got := util.Clean(fp["browser-family"]); got != "chrome" {
		t.Fatalf("browser-family = %q, want chrome", got)
	}
	if got := util.Clean(fp["browser-version"]); got != "145" {
		t.Fatalf("browser-version = %q, want 145", got)
	}
	if got := util.Clean(fp["impersonate"]); got != "chrome145" {
		t.Fatalf("impersonate = %q, want chrome145", got)
	}
	if got := util.Clean(fp["user-agent"]); !strings.Contains(got, "Chrome/145.0.0.0") {
		t.Fatalf("user-agent = %q, want Chrome/145.0.0.0", got)
	}
}

func newTestAccountService(t *testing.T) *AccountService {
	t.Helper()
	return newTestAccountServiceWithConfig(t, testAccountConfig{})
}

func accountLogSummaries(accounts *AccountService) []string {
	items := accounts.logs.Search(LogQuery{Limit: 200, View: LogViewAll})
	summaries := make([]string, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, util.Clean(item["summary"]))
	}
	return summaries
}

func TestUpdateAccountDoesNotWriteLog(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})

	accounts.UpdateAccount("token-1", map[string]any{"status": "正常", "email": "alice@example.com"})

	for _, summary := range accountLogSummaries(accounts) {
		if summary == "更新账号" {
			t.Fatalf("UpdateAccount() wrote a %q log; account status churn must stay out of the log list", summary)
		}
	}
}

// testJWT 造一个只有 payload 有意义的三段式 token，用来验证基于 exp 的过期判定。
func testJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(raw) + ".sig"
}

func TestDecodeAccessTokenExpiry(t *testing.T) {
	token := testJWT(t, map[string]any{"exp": 1785334952})
	got, ok := DecodeAccessTokenExpiry(token)
	if !ok {
		t.Fatal("DecodeAccessTokenExpiry() did not parse a well-formed JWT exp")
	}
	if want := time.Unix(1785334952, 0); !got.Equal(want) {
		t.Fatalf("DecodeAccessTokenExpiry() = %s, want %s", got, want)
	}

	for name, bad := range map[string]string{
		"empty":         "",
		"not a jwt":     "opaque-token-value",
		"payload junk":  "aaa.!!!not-base64!!!.ccc",
		"no exp claim":  testJWT(t, map[string]any{"sub": "user-1"}),
		"exp not a num": testJWT(t, map[string]any{"exp": "soon"}),
	} {
		if _, ok := DecodeAccessTokenExpiry(bad); ok {
			t.Fatalf("DecodeAccessTokenExpiry(%s) reported ok; unparseable tokens must fall back to message matching", name)
		}
	}
}

// 上游把「过期」改报成 401 Could not parse your authentication token 之后，
// 三个分类器曾经全部落空，账号既不刷新也不改状态。这条串必须被认出来。
func TestIsAccountTokenExpiredErrorMessageMatchesUpstreamRephrasing(t *testing.T) {
	message := "/backend-api/me failed: HTTP 401, body=Could not parse your authentication token. Please try signing in again."
	if !IsAccountTokenExpiredErrorMessage(message) {
		t.Fatal("IsAccountTokenExpiredErrorMessage() missed the 401 could-not-parse wording")
	}
}

func TestListRefreshableTokensIncludesExpiredToken(t *testing.T) {
	accounts := newTestAccountService(t)
	expired := testJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	valid := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	accounts.AddAccounts([]string{expired, valid, "opaque-token-value"})
	accounts.UpdateAccount(expired, map[string]any{"status": "正常", "session_token": "session-expired"})
	accounts.UpdateAccount(valid, map[string]any{"status": "正常", "session_token": "session-valid"})
	// 不透明 token 解不出 exp，只能靠文案匹配兜底，后台轮询不该凭猜测去刷它。
	accounts.UpdateAccount("opaque-token-value", map[string]any{"status": "正常", "session_token": "session-opaque"})

	got := accounts.listRefreshableTokens(time.Now())
	if len(got) != 1 || got[0] != expired {
		t.Fatalf("listRefreshableTokens() = %#v, want only the expired token", got)
	}
}

// 没有 session_token 就续不了，后台轮询不该对它发无谓的请求。
func TestListRefreshableTokensSkipsExpiredTokenWithoutSession(t *testing.T) {
	accounts := newTestAccountService(t)
	expired := testJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	accounts.AddAccounts([]string{expired})
	accounts.UpdateAccount(expired, map[string]any{"status": "正常"})

	if got := accounts.listRefreshableTokens(time.Now()); len(got) != 0 {
		t.Fatalf("listRefreshableTokens() = %#v, want none for an unrefreshable expired token", got)
	}
}

// 提前量内即将过期的 token 也应被续期，避免「判定有效但请求发出时刚好过期」。
func TestListRefreshableTokensIncludesTokenInsideSkew(t *testing.T) {
	accounts := newTestAccountService(t)
	soon := testJWT(t, map[string]any{"exp": time.Now().Add(time.Minute).Unix()})
	accounts.AddAccounts([]string{soon})
	accounts.UpdateAccount(soon, map[string]any{"status": "正常", "session_token": "session-soon"})

	got := accounts.listRefreshableTokens(time.Now())
	if len(got) != 1 || got[0] != soon {
		t.Fatalf("listRefreshableTokens() = %#v, want the token expiring inside the skew", got)
	}
}

// 续期失败后账号会被写成异常。session_token 已死时再试也不会成功，
// 后台轮询必须停手，否则每轮都拿一个注定失败的账号去打上游。
func TestListRefreshableTokensSkipsExpiredTokenAlreadyMarkedAbnormal(t *testing.T) {
	accounts := newTestAccountService(t)
	expired := testJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	accounts.AddAccounts([]string{expired})
	accounts.UpdateAccount(expired, map[string]any{"status": "异常", "session_token": "session-dead"})

	if got := accounts.listRefreshableTokens(time.Now()); len(got) != 0 {
		t.Fatalf("listRefreshableTokens() = %#v, want none for an already-failed refresh", got)
	}
}

func TestAccountAutoMaintenanceDoesNotWriteLogs(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{removeInvalid: true, removeRateLmt: true})
	accounts.AddAccounts([]string{"token-1", "token-2", "token-3"})

	accounts.RemoveInvalidToken("token-1")
	if accounts.GetAccount("token-1") != nil {
		t.Fatal("RemoveInvalidToken() did not remove the account; test config is not exercising the removal path")
	}
	accounts.UpdateAccount("token-2", map[string]any{"status": "限流"})
	accounts.UpdateAccountFromSessionImport("token-3", "token-3-rotated", map[string]any{"status": "正常"}, true)

	forbidden := map[string]struct{}{
		"更新账号":        {},
		"更新Session账号": {},
		"刷新账号token":   {},
		"自动移除限流账号":    {},
		"自动移除异常账号":    {},
	}
	for _, summary := range accountLogSummaries(accounts) {
		if _, bad := forbidden[summary]; bad {
			t.Fatalf("auto maintenance wrote a %q log; internal account churn must stay out of the log list", summary)
		}
	}
}

func newTestAccountServiceWithConfig(t *testing.T, cfg testAccountConfig) *AccountService {
	t.Helper()
	backend := newTestStorageBackend(t)
	return NewAccountService(
		backend,
		cfg,
		NewProxyService(cfg),
		NewLogService(backend),
	)
}

func newAccountQuotaServer(t *testing.T, mePayload map[string]any, limits []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			writeJSON(t, w, mePayload)
		case "/backend-api/conversation/init":
			payload := map[string]any{"default_model_slug": "gpt-5"}
			if limits != nil {
				payload["limits_progress"] = limits
			}
			writeJSON(t, w, payload)
		default:
			http.NotFound(w, r)
		}
	}))
}

// --- quota=0 候选过滤测试 ---

// 注：文本账号无 quota 递减机制，textCandidatesLocked/textCandidatesByPaidLocked 不过滤 quota=0。
// 旧版 JSON 账号 quota 字段缺失时 normalizeAccount 会默认设为 0，若过滤则误杀所有旧账号。

func TestImageCandidatesLockedFiltersQuotaZero(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{imageMode: "fill_first"})
	accounts.AddAccounts([]string{"img-ok", "img-exhausted", "img-unknown"})
	accounts.UpdateAccount("img-ok", map[string]any{"status": "正常", "quota": 2})
	accounts.UpdateAccount("img-exhausted", map[string]any{"status": "正常", "quota": 0})
	accounts.UpdateAccount("img-unknown", map[string]any{"status": "正常", "quota": 0, "image_quota_unknown": true, "type": "Plus"})

	accounts.mu.Lock()
	candidates := accounts.imageCandidatesLocked(nil, nil)
	accounts.mu.Unlock()

	// img-exhausted (quota=0, known quota) 应被过滤；img-unknown (image_quota_unknown=true) 应保留
	tokens := make(map[string]bool)
	for _, c := range candidates {
		tokens[util.Clean(c["access_token"])] = true
	}
	if !tokens["img-ok"] {
		t.Error("img-ok should be in candidates")
	}
	if tokens["img-exhausted"] {
		t.Error("img-exhausted (quota=0, known) should NOT be in candidates")
	}
	if !tokens["img-unknown"] {
		t.Error("img-unknown (image_quota_unknown=true) should still be in candidates")
	}
}

func TestMarkImageResultClearsStickyOnQuotaExhaustion(t *testing.T) {
	accounts := newTestAccountServiceWithConfig(t, testAccountConfig{imageMode: "fill_first"})
	accounts.AddAccounts([]string{"img-1"})
	accounts.UpdateAccount("img-1", map[string]any{"status": "正常", "quota": 1, "type": "Free"})

	// 模拟占用 sticky token
	accounts.mu.Lock()
	accounts.stickyImageToken = "img-1"
	accounts.mu.Unlock()

	// 直接使用 acquireImageCandidateLease 获取 token
	lease, _, err := accounts.acquireImageCandidateLease(nil, nil)
	if err != nil {
		t.Fatalf("acquireImageCandidateLease() error = %v", err)
	}
	if lease.Token != "img-1" {
		t.Fatalf("lease token = %q, want img-1", lease.Token)
	}
	// 只释放 busyTokens（lease.Release），reservation 由 MarkImageResult 内部释放
	lease.Release()

	// 标记成功 → quota 从 1 递减到 0 → 应清除 sticky
	accounts.MarkImageResult("img-1", true)

	accounts.mu.Lock()
	sticky := accounts.stickyImageToken
	accounts.mu.Unlock()

	if sticky != "" {
		t.Fatalf("sticky token = %q after quota exhaustion, want empty", sticky)
	}

	// 验证账号状态
	account := accounts.GetAccount("img-1")
	if account["status"] != "限流" {
		t.Fatalf("account status = %q, want 限流", account["status"])
	}
	if util.ToInt(account["quota"], 0) != 0 {
		t.Fatalf("account quota = %v, want 0", account["quota"])
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

// 导入账号的 oai-did cookie 来自真实浏览器，而 fp[oai-device-id] 由入库时随机生成，
// 二者从入库那刻起就不同。出站请求会同时发送 cookie 与 OAI-Device-Id 头，
// 上游因此看到两个不同的设备身份——必须对齐到 cookie 侧。
func TestEnsureAccountFingerprintAlignsDeviceIDWithCookie(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})

	accounts.UpdateAccount("token-1", map[string]any{
		"session_cookies": map[string]string{
			"oai-did":      "browser-device-id",
			"cf_clearance": "clearance-value",
		},
	})

	account := accounts.GetAccount("token-1")
	fp := account["fp"].(map[string]any)
	if got := util.Clean(fp["oai-device-id"]); got != "browser-device-id" {
		t.Fatalf("fp[oai-device-id] = %q, want browser-device-id", got)
	}
	// 设备身份对齐不应丢掉 cookie 本身。
	cookies := SessionCookieStringMap(account["session_cookies"])
	if cookies["oai-did"] != "browser-device-id" {
		t.Fatalf("oai-did cookie = %q, want browser-device-id", cookies["oai-did"])
	}
}

// 没有 oai-did cookie 时不得凭空改写指纹（注册链路走 AddAccountsWithDeviceID）。
func TestEnsureAccountFingerprintKeepsDeviceIDWithoutCookie(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	before := util.Clean(accounts.GetAccount("token-1")["fp"].(map[string]any)["oai-device-id"])

	accounts.UpdateAccount("token-1", map[string]any{
		"session_cookies": map[string]string{"__cf_bm": "bm-value"},
	})

	after := util.Clean(accounts.GetAccount("token-1")["fp"].(map[string]any)["oai-device-id"])
	if after != before {
		t.Fatalf("oai-device-id = %q, want unchanged %q", after, before)
	}
}

// 指纹迁移会改写浏览器身份，而 cf_clearance 绑定的是旧身份。
// 继续携带等于发出「凭证说身份 A、请求说身份 B」的矛盾信号，
// 因此迁移时必须连同时间戳一起丢弃；与身份无关的 CF cookie 保留。
func TestFingerprintMigrationDropsFingerprintBoundClearance(t *testing.T) {
	backend := &accountStorageSpy{accounts: []map[string]any{{
		"access_token": "token-1",
		"type":         "Plus",
		"status":       "正常",
		"fp": map[string]any{
			"version":         1,
			"browser-family":  "edge",
			"browser-version": "143",
			"impersonate":     "edge101",
			"user-agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
			"oai-device-id":   "device-1",
			"oai-session-id":  "session-1",
		},
		"session_cookies": map[string]any{
			"cf_clearance": "old-clearance",
			"cf_chl_2":     "challenge-token",
			"__cf_bm":      "bm-value",
			"_cfuvid":      "visitor-id",
		},
		"session_cookie_updated_at": map[string]any{
			"cf_clearance": "2026-09-22T00:00:00Z",
			"cf_chl_2":     "2026-09-22T00:00:00Z",
			"__cf_bm":      "2026-09-22T00:00:00Z",
			"_cfuvid":      "2026-09-22T00:00:00Z",
		},
	}}}
	accounts := NewAccountService(backend, testAccountConfig{}, nil, NewLogService())

	account := accounts.GetAccount("token-1")
	cookies := SessionCookieStringMap(account["session_cookies"])
	if _, ok := cookies["cf_clearance"]; ok {
		t.Fatalf("cf_clearance should be dropped on migration: %#v", cookies)
	}
	if _, ok := cookies["cf_chl_2"]; ok {
		t.Fatalf("cf_chl_* should be dropped on migration: %#v", cookies)
	}
	if cookies["__cf_bm"] != "bm-value" || cookies["_cfuvid"] != "visitor-id" {
		t.Fatalf("identity-independent cookies should survive: %#v", cookies)
	}
	updatedAt := SessionCookieStringMap(account["session_cookie_updated_at"])
	if _, ok := updatedAt["cf_clearance"]; ok {
		t.Fatalf("cf_clearance timestamp should be dropped: %#v", updatedAt)
	}
	if _, ok := updatedAt["cf_chl_2"]; ok {
		t.Fatalf("cf_chl_* timestamp should be dropped: %#v", updatedAt)
	}
	if updatedAt["__cf_bm"] != "2026-09-22T00:00:00Z" {
		t.Fatalf("__cf_bm timestamp should survive: %#v", updatedAt)
	}
}

// 账号已在池内时不得丢弃 cf_clearance——迁移只发生在身份被改写时。
func TestInPoolFingerprintKeepsClearance(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"token-1"})
	accounts.UpdateAccount("token-1", map[string]any{
		"session_cookies": map[string]string{
			"cf_clearance": "fresh-clearance",
			"oai-did":      "device-1",
		},
		"session_cookie_updated_at": map[string]string{"cf_clearance": "2026-09-22T00:00:00Z"},
	})

	account := accounts.GetAccount("token-1")
	cookies := SessionCookieStringMap(account["session_cookies"])
	if cookies["cf_clearance"] != "fresh-clearance" {
		t.Fatalf("cf_clearance = %q, want fresh-clearance for in-pool fingerprint", cookies["cf_clearance"])
	}
}

// 续期成功时上游会轮换 __Secure-next-auth.session-token（并且该 cookie 按 NextAuth
// 的约定分片下发），但轮换只被记成了账号的 session_token 字段，没有回写进
// session_cookies。后续请求发的是 session_cookies，于是永远带着续期前那一片旧
// cookie：拿新 bearer 配旧 session，上游按凭证不一致拒绝，紧接着的信息拉取就 401——
// 这正是「续期成功、额度却拉不回来」的成因。
func TestRefreshAccountsKeepsRotatedSessionCookie(t *testing.T) {
	var mu sync.Mutex
	var meCookies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			mu.Lock()
			meCookies = append(meCookies, r.Header.Get("Cookie"))
			call := len(meCookies)
			mu.Unlock()
			if call == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				writeJSON(t, w, map[string]any{"detail": "authentication token is expired"})
				return
			}
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			writeJSON(t, w, map[string]any{"limits_progress": []map[string]any{{"feature_name": "image_gen", "remaining": 7}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.refresher = NewSessionRefresher(func(*http.Request) (*http.Response, error) {
		// 上游轮换 session cookie：真实响应走 Set-Cookie 下发，且按 NextAuth 的
		// 约定是分片命名（__Secure-next-auth.session-token.0/.1）。
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Set-Cookie": []string{
					"__Secure-next-auth.session-token.0=rotated-chunk-0; Path=/; Domain=.chatgpt.com; Secure; HttpOnly",
					"__Secure-next-auth.session-token.1=rotated-chunk-1; Path=/; Domain=.chatgpt.com; Secure; HttpOnly",
				},
			},
			Body: io.NopCloser(strings.NewReader(`{"accessToken":"new-access-token","sessionToken":"rotated-chunk-1","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})
	accounts.AddAccounts([]string{"expired-access-token"})
	accounts.UpdateAccount("expired-access-token", map[string]any{
		"status":        "正常",
		"quota":         5,
		"session_token": "stale-chunk-1",
		"session_cookies": map[string]string{
			"oai-did":                            "device-1",
			"__Secure-next-auth.session-token.0": "stale-chunk-0",
			"__Secure-next-auth.session-token.1": "stale-chunk-1",
		},
		"session_cookie_updated_at": map[string]string{"cf_clearance": time.Now().UTC().Format(time.RFC3339)},
	})

	result := accounts.RefreshAccounts(context.Background(), []string{"expired-access-token"})
	if result["session_refreshed"] != 1 {
		t.Fatalf("refresh result = %#v, want a session refresh", result)
	}
	mu.Lock()
	cookies := append([]string(nil), meCookies...)
	mu.Unlock()
	if len(cookies) < 2 {
		t.Fatalf("me calls = %d, want the post-renewal info fetch to happen", len(cookies))
	}
	after := cookies[1]
	// 两个分片都要是轮换后的值：只换了一片，出站仍是新旧混搭。
	if strings.Contains(after, "stale-chunk-0") || strings.Contains(after, "stale-chunk-1") {
		t.Fatalf("post-renewal /backend-api/me sent a pre-rotation session cookie: %q", after)
	}
	if !strings.Contains(after, "rotated-chunk-0") || !strings.Contains(after, "rotated-chunk-1") {
		t.Fatalf("post-renewal /backend-api/me did not carry the rotated session token: %q", after)
	}
	// 非 session 的 cookie 不能被顺手丢掉。
	if !strings.Contains(after, "oai-did=device-1") {
		t.Fatalf("post-renewal /backend-api/me lost the device cookie: %q", after)
	}
}

// 上游在 session 已失效时也会返回 200，但给出的还是原来那个（已过期的）
// accessToken。此前这条链路只看「请求成没成功」，于是把这种空转换记成续期成功：
// 账号被标成正常，而 token 依旧过期——自动续期此后不再管它（refreshableExpiredToken
// 只挑过期的），实时请求则一直失败。用户看到的就是「刷新成功 1 个」加上一句
// 额度拉取失败，而账号 JWT 的 exp 根本没动。
func TestRefreshAccountsRejectsUnchangedExpiredToken(t *testing.T) {
	expired := testJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			// 续期没换到新 token，这一跳注定 401。
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(t, w, map[string]any{"detail": "authentication token is expired"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, string, time.Duration) *http.Client {
		return server.Client()
	}
	accounts.refresher = NewSessionRefresher(func(*http.Request) (*http.Response, error) {
		// 200，但 accessToken 与请求里那个一模一样。
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"` + expired + `","sessionToken":"same-session","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})
	accounts.AddAccounts([]string{expired})
	accounts.UpdateAccount(expired, map[string]any{
		"status":        "过期待刷新",
		"quota":         5,
		"session_token": "refresh-session-token",
	})

	result := accounts.RefreshAccounts(context.Background(), []string{expired})
	if result["session_refreshed"] != 0 {
		t.Fatalf("session_refreshed = %#v, want 0: an unchanged token is not a renewal", result["session_refreshed"])
	}
	if result["failed"] != 1 {
		t.Fatalf("failed = %#v, want the no-op renewal reported as a failure", result["failed"])
	}
	// 账号不能被标成正常：标正常就等于把它从自动续期里摘出去，
	// 而它的 token 仍然过期。
	if account := accounts.GetAccount(expired); util.Clean(account["status"]) != "异常" {
		t.Fatalf("status = %#v, want 异常 so the account stays visible as broken", account["status"])
	}
	// 也不能悄悄把 access_token 换成同一个值还宣称刷新过。
	errors, _ := result["errors"].([]map[string]string)
	if len(errors) != 1 || !strings.Contains(errors[0]["error"], "未换发新 token") {
		t.Fatalf("errors = %#v, want the no-rotation reason surfaced", result["errors"])
	}
}

// TestListAccountsExposesTokenExpiry 固定列表里的 token 到期字段。
//
// 没有它，判断「这个账号的 token 是不是死的」只能自己解 JWT——这正是排查
// 「续期没换到 token」时最耗时的一步。
func TestListAccountsExposesTokenExpiry(t *testing.T) {
	expiresAt := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	live := testJWT(t, map[string]any{"exp": expiresAt.Unix()})
	// 距离过期不足 5 分钟：调度侧已按不可用处理（tokenExpired 带提前量），
	// 面板必须给出同样的结论，否则这段窗口就是「状态正常但请求失败」。
	almostExpired := testJWT(t, map[string]any{"exp": time.Now().Add(time.Minute).Unix()})
	expired := testJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})

	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{live, almostExpired, expired, "not-a-jwt"})

	byToken := map[string]map[string]any{}
	for _, item := range accounts.ListAccounts() {
		byToken[util.Clean(item["access_token"])] = item
	}

	if got := util.Clean(byToken[live]["tokenExpiresAt"]); got != expiresAt.UTC().Format(time.RFC3339) {
		t.Fatalf("tokenExpiresAt = %q, want %q", got, expiresAt.UTC().Format(time.RFC3339))
	}
	if byToken[live]["tokenExpired"] != false {
		t.Fatalf("tokenExpired for a live token = %#v, want false", byToken[live]["tokenExpired"])
	}
	for name, token := range map[string]string{"almost expired": almostExpired, "expired": expired} {
		if byToken[token]["tokenExpired"] != true {
			t.Fatalf("tokenExpired for %s token = %#v, want true", name, byToken[token]["tokenExpired"])
		}
	}
	// 非 JWT 解不出 exp：留空 + false，不能编一个时间也不能当成已过期。
	if got := util.Clean(byToken["not-a-jwt"]["tokenExpiresAt"]); got != "" {
		t.Fatalf("tokenExpiresAt for a non-JWT token = %q, want empty", got)
	}
	if byToken["not-a-jwt"]["tokenExpired"] != false {
		t.Fatalf("tokenExpired for a non-JWT token = %#v, want false", byToken["not-a-jwt"]["tokenExpired"])
	}
}
