package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt2api/internal/util"
)

type testAccountConfig struct {
	textMode  string
	imageMode string
}

func (testAccountConfig) AutoRemoveInvalidAccounts() bool     { return false }
func (testAccountConfig) AutoRemoveRateLimitedAccounts() bool { return false }
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
func (testAccountConfig) Proxy() string { return "" }

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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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

func TestGetTextAccessTokenEnforcesFreeCooldown(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"free-token"})
	accounts.UpdateAccount("free-token", map[string]any{"status": "正常", "type": "Free"})

	for i := 0; i < 10; i++ {
		if token := accounts.GetTextAccessToken(); token != "free-token" {
			t.Fatalf("GetTextAccessToken() call %d = %q, want free-token", i+1, token)
		}
	}
	if token := accounts.GetTextAccessToken(); token != "" {
		t.Fatalf("GetTextAccessToken() after free cooldown exhaustion = %q, want empty token", token)
	}
	if token, ok := accounts.GetTextAccessTokenWithRetry(nil); ok || token != "" {
		t.Fatalf("GetTextAccessTokenWithRetry() after free cooldown exhaustion = %q %v, want no token", token, ok)
	}

	accounts.textCooldownUntil = time.Now().Add(-time.Second)
	if token := accounts.GetTextAccessToken(); token != "free-token" {
		t.Fatalf("GetTextAccessToken() after cooldown expiry = %q, want free-token", token)
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

func TestStartLimitedWatcherSkipsAccountBeforeRestoreTime(t *testing.T) {
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
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
	accounts.StartLimitedWatcher(ctx, 20*time.Millisecond)
	time.Sleep(80 * time.Millisecond)

	mu.Lock()
	got := meCalls
	mu.Unlock()
	if got != 0 {
		t.Fatalf("limited watcher refreshed account before restore time: /backend-api/me calls = %d, want 0", got)
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
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client { return server.Client() }
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

	if !accounts.RefreshAccountViaSession("old-token", "new-token", "new-session", "2026-05-20T00:00:00Z") {
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

	if !accounts.RefreshAccountViaSession("old-token", "new-token", "new-session", "2026-05-20T00:00:00Z") {
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

	if !accounts.RefreshAccountViaSession("old-token", "new-token", "new-session", "2026-05-20T00:00:00Z") {
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
	items := accounts.ListAccounts()
	if len(items) != 1 {
		t.Fatalf("ListAccounts() length = %d, want 1", len(items))
	}
	if items[0]["enabled"] != true {
		t.Fatalf("public enabled after add = %#v, want true", items[0]["enabled"])
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

	items := accounts.ListAccounts()
	if len(items) != 1 {
		t.Fatalf("ListAccounts() length = %d, want 1", len(items))
	}
	if items[0]["enabled"] != false {
		t.Fatalf("public enabled for legacy account = %#v, want false", items[0]["enabled"])
	}
	if backend.saveCount != 0 {
		t.Fatalf("ListAccounts() saved legacy account %d times, want 0", backend.saveCount)
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

func newTestAccountService(t *testing.T) *AccountService {
	t.Helper()
	return newTestAccountServiceWithConfig(t, testAccountConfig{})
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

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
