package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt2api/internal/storage"
)

type testAccountConfig struct{}

func (testAccountConfig) AutoRemoveInvalidAccounts() bool     { return false }
func (testAccountConfig) AutoRemoveRateLimitedAccounts() bool { return false }
func (testAccountConfig) Proxy() string                       { return "" }

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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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

func TestSummarizeRefreshErrorBodyPrefersJSONMessage(t *testing.T) {
	got := summarizeRefreshErrorBody([]byte(`{"error":{"message":"You've reached the image generation limit"}}`))
	if got != "body=You've reached the image generation limit" {
		t.Fatalf("summarizeRefreshErrorBody() = %q", got)
	}
}

func newTestAccountService(t *testing.T) *AccountService {
	t.Helper()
	dir := t.TempDir()
	return NewAccountService(
		storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json")),
		testAccountConfig{},
		NewProxyService(testAccountConfig{}),
		NewLogService(dir),
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

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
