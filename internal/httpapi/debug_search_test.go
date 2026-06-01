package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt2api/internal/protocol"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

func TestHandleDebugSearchRejectsUnauthorizedAndEmptyQuery(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	unauthorizedReq := httptest.NewRequest(http.MethodPost, "/api/debug/search", strings.NewReader(`{"query":"find debug traces"}`))
	unauthorizedRes := httptest.NewRecorder()
	app.Handler().ServeHTTP(unauthorizedRes, unauthorizedReq)
	if unauthorizedRes.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body = %s", unauthorizedRes.Code, unauthorizedRes.Body.String())
	}

	emptyReq := httptest.NewRequest(http.MethodPost, "/api/debug/search", strings.NewReader(`{"model":"auto"}`))
	emptyReq.Header.Set("Authorization", adminAuthHeader(t, app))
	emptyRes := httptest.NewRecorder()
	app.Handler().ServeHTTP(emptyRes, emptyReq)
	if emptyRes.Code != http.StatusBadRequest {
		t.Fatalf("empty query status = %d body = %s", emptyRes.Code, emptyRes.Body.String())
	}
}

func TestHandleDebugSearchReturnsPayloadAndLogsSuccess(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	app.engine.HandleChatCompletionsFunc = func(context.Context, map[string]any) (map[string]any, *protocol.StreamResult, error) {
		return map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "debug answer"}}},
		}, nil, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/debug/search", strings.NewReader(`{"query":"find debug traces","model":"gpt-test"}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("debug search status = %d body = %s", res.Code, res.Body.String())
	}

	logs := app.logs.Search(service.LogQuery{Limit: 10})
	businessLog := findLogBySummary(logs, "Search 调试调用完成")
	if businessLog == nil {
		t.Fatalf("expected debug search success log, got %#v", logs)
	}
	detail := util.StringMap(businessLog["detail"])
	if util.Clean(detail["status"]) != "200" || util.Clean(detail["outcome"]) != "success" {
		t.Fatalf("unexpected success log detail = %#v", detail)
	}
	if auditLog := findHTTPAuditLogByPath(logs, "/api/debug/search"); auditLog != nil {
		t.Fatalf("debug search success should not also create generic audit log: %#v", auditLog)
	}
}

func TestHandleDebugSearchReturnsPayloadAndLogsFailure(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	app.engine.Accounts = nil

	req := httptest.NewRequest(http.MethodPost, "/api/debug/search", strings.NewReader(`{"query":"find debug traces"}`))
	req.Header.Set("Authorization", adminAuthHeader(t, app))
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("debug search status = %d body = %s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("debug search json = %v body = %s", err, res.Body.String())
	}
	payload := util.StringMap(body["payload"])
	if payload["query"] != "find debug traces" {
		t.Fatalf("payload query = %#v", payload["query"])
	}
	if payload["model"] != "auto" {
		t.Fatalf("payload model = %#v", payload["model"])
	}
	if strings.TrimSpace(util.Clean(body["error"])) == "" {
		t.Fatalf("missing error body = %#v", body)
	}

	logs := app.logs.Search(service.LogQuery{Module: "debug", Summary: "Search 调试", Limit: 10})
	if len(logs) == 0 {
		t.Fatalf("expected debug search log entry, got %#v", app.logs.Search(service.LogQuery{Limit: 10}))
	}
	if summary := strings.TrimSpace(util.Clean(logs[0]["summary"])); !strings.Contains(summary, "Search 调试") {
		t.Fatalf("unexpected log summary = %#v", logs[0])
	}
	detail := util.StringMap(logs[0]["detail"])
	if util.Clean(detail["status"]) != "502" {
		t.Fatalf("unexpected log status = %#v", logs[0])
	}
	if auditLog := findHTTPAuditLogByPath(app.logs.Search(service.LogQuery{Limit: 10}), "/api/debug/search"); auditLog != nil {
		t.Fatalf("debug search should not also create generic audit log: %#v", auditLog)
	}
}
