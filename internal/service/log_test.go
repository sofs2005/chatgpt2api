package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogServiceUsesUnifiedLogsDirectory(t *testing.T) {
	dir := t.TempDir()
	logs := NewLogService(dir)

	if err := logs.Add("新增账号", map[string]any{"module": "accounts", "operation_type": "新增", "added": 1}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "logs", "events.jsonl")); err != nil {
		t.Fatalf("expected unified log file under data/logs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "logs.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("root logs.jsonl should not be used, stat error = %v", err)
	}

	items := logs.List("", "", 10)
	if len(items) != 1 {
		t.Fatalf("List() length = %d, want 1", len(items))
	}
	if items[0]["summary"] != "新增账号" {
		t.Fatalf("List()[0] = %#v", items[0])
	}
	if _, ok := items[0]["type"]; ok {
		t.Fatalf("List()[0] should not expose log type: %#v", items[0])
	}
}

func TestLogServiceSearchFiltersUnifiedLogs(t *testing.T) {
	dir := t.TempDir()
	logs := NewLogService(dir)

	if err := logs.Add("新增账号", map[string]any{"module": "accounts", "operation_type": "新增", "added": 1}); err != nil {
		t.Fatalf("Add(account event) error = %v", err)
	}
	if err := logs.Add("文生图调用完成", map[string]any{
		"key_name":    "alice",
		"key_id":      "alice-key",
		"method":      "POST",
		"path":        "/v1/images/generations",
		"module":      "images",
		"endpoint":    "/v1/images/generations",
		"duration_ms": 120,
		"status":      200,
		"outcome":     "success",
		"log_level":   "info",
	}); err != nil {
		t.Fatalf("Add(call event) error = %v", err)
	}
	if err := logs.Add("GET /api/settings", map[string]any{
		"username":       "admin",
		"module":         "settings",
		"method":         "GET",
		"path":           "/api/settings",
		"status":         403,
		"ip_address":     "127.0.0.1",
		"operation_type": "查询",
		"log_level":      "warning",
	}); err != nil {
		t.Fatalf("Add(audit event) error = %v", err)
	}

	all := logs.Search(LogQuery{Limit: 10})
	if len(all) != 3 {
		t.Fatalf("Search(all) length = %d, want 3: %#v", len(all), all)
	}
	for _, item := range all {
		if _, ok := item["type"]; ok {
			t.Fatalf("Search(all) should not expose log type: %#v", all)
		}
	}

	filtered := logs.Search(LogQuery{
		Username:      "admin",
		Module:        "settings",
		Method:        "GET",
		Summary:       "/api/settings",
		Status:        "403",
		IPAddress:     "127.0.0.1",
		OperationType: "查询",
		LogLevel:      "warning",
		Limit:         10,
	})
	if len(filtered) != 1 || filtered[0]["summary"] != "GET /api/settings" {
		t.Fatalf("Search(filtered) = %#v", filtered)
	}

	callLogs := logs.Search(LogQuery{Username: "alice", Module: "images", Method: "POST", Status: "200", LogLevel: "info", Limit: 10})
	if len(callLogs) != 1 || callLogs[0]["summary"] != "文生图调用完成" {
		t.Fatalf("Search(call) = %#v", callLogs)
	}
	if _, ok := callLogs[0]["type"]; ok {
		t.Fatalf("Search(call) should not expose log type: %#v", callLogs)
	}
	usage := logs.UserUsageStats(1)["alice-key"]
	if usage == nil || usage["call_count"] != 1 || usage["success_count"] != 1 || usage["quota_used"] != 1 {
		t.Fatalf("UserUsageStats(new call log shape) = %#v", usage)
	}
}

func TestSanitizeLogValueMasksSessionCredentials(t *testing.T) {
	accessToken := "access-token-secret"
	sessionToken := "session-token-secret"
	sanitized := SanitizeLogValue(map[string]any{
		"session_json": `{"accessToken":"` + accessToken + `","sessionToken":"` + sessionToken + `"}`,
		"accessToken":  accessToken,
		"sessionToken": sessionToken,
	})

	item, ok := sanitized.(map[string]any)
	if !ok {
		t.Fatalf("SanitizeLogValue() = %#v", sanitized)
	}
	text := item["session_json"].(string) + item["accessToken"].(string) + item["sessionToken"].(string)
	if strings.Contains(text, accessToken) || strings.Contains(text, sessionToken) {
		t.Fatalf("sanitized log value leaked credentials: %#v", sanitized)
	}
}

func TestLogServiceCleansOldLogs(t *testing.T) {
	dir := t.TempDir()
	logs := NewLogService(dir)

	if err := logs.Add("旧调用", map[string]any{"status": "success"}); err != nil {
		t.Fatalf("Add(old) error = %v", err)
	}
	if err := logs.Add("新日志", map[string]any{"status": 200}); err != nil {
		t.Fatalf("Add(new) error = %v", err)
	}

	path := filepath.Join(dir, "logs", "events.jsonl")
	data := []byte(`{"time":"2000-01-01 00:00:00","type":"event","summary":"旧调用","detail":{"status":"success"}}` + "\n" +
		`{"time":"` + time.Now().Format("2006-01-02 15:04:05") + `","type":"event","summary":"新日志","detail":{"status":200}}` + "\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("rewrite logs: %v", err)
	}

	result, err := logs.CleanupOlderThan(1)
	if err != nil {
		t.Fatalf("CleanupOlderThan() error = %v", err)
	}
	if result.Deleted != 1 || result.Remaining != 1 {
		t.Fatalf("CleanupOlderThan() = %#v, want deleted 1 remaining 1", result)
	}
	items := logs.Search(LogQuery{Limit: 10})
	if len(items) != 1 || items[0]["summary"] != "新日志" {
		t.Fatalf("remaining logs = %#v", items)
	}
}
