package service

import (
	"testing"
	"time"
)

func TestLogServiceStoresLogsInDatabase(t *testing.T) {
	logs := NewLogService(newTestStorageBackend(t))

	if err := logs.Add("新增账号", map[string]any{"module": "accounts", "operation_type": "新增", "added": 1}); err != nil {
		t.Fatalf("Add() error = %v", err)
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
	logs := NewLogService(newTestStorageBackend(t))

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

func TestLogServiceUserUsageStatsForUsersFiltersResults(t *testing.T) {
	logs := NewLogService(newTestStorageBackend(t))

	if err := logs.Add("Alice 调用", map[string]any{
		"key_id":   "alice-key",
		"endpoint": "/v1/images/generations",
		"status":   200,
	}); err != nil {
		t.Fatalf("Add(alice) error = %v", err)
	}
	if err := logs.Add("Bob 调用", map[string]any{
		"key_id":   "bob-key",
		"endpoint": "/v1/images/generations",
		"status":   200,
	}); err != nil {
		t.Fatalf("Add(bob) error = %v", err)
	}

	usage := logs.UserUsageStatsForUsers(1, []string{"alice-key"})
	if usage["alice-key"] == nil {
		t.Fatalf("missing requested user usage: %#v", usage)
	}
	if usage["bob-key"] != nil {
		t.Fatalf("returned unrequested user usage: %#v", usage)
	}
}

func TestLogServiceCleansOldLogs(t *testing.T) {
	logs := NewLogService(newTestStorageBackend(t))

	for _, item := range []map[string]any{
		{"time": "2000-01-01 00:00:00", "type": "event", "summary": "旧调用", "detail": map[string]any{"status": "success"}},
		{"time": time.Now().Format("2006-01-02 15:04:05"), "type": "event", "summary": "新日志", "detail": map[string]any{"status": 200}},
	} {
		if err := logs.store.AppendLog(item); err != nil {
			t.Fatalf("AppendLog() error = %v", err)
		}
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
