package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogServiceUsesUnifiedLogsDirectory(t *testing.T) {
	dir := t.TempDir()
	logs := NewLogService(dir)

	if err := logs.Add(LogTypeAccount, "新增账号", map[string]any{"added": 1}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "logs", "events.jsonl")); err != nil {
		t.Fatalf("expected unified log file under data/logs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "logs.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("root logs.jsonl should not be used, stat error = %v", err)
	}

	items := logs.List(LogTypeAccount, "", "", 10)
	if len(items) != 1 {
		t.Fatalf("List() length = %d, want 1", len(items))
	}
	if items[0]["summary"] != "新增账号" {
		t.Fatalf("List()[0] = %#v", items[0])
	}
}

func TestLogServiceSearchFiltersUnifiedLogs(t *testing.T) {
	dir := t.TempDir()
	logs := NewLogService(dir)

	if err := logs.Add(LogTypeAccount, "新增账号", map[string]any{"added": 1}); err != nil {
		t.Fatalf("Add(account) error = %v", err)
	}
	if err := logs.Add(LogTypeCall, "文生图调用完成", map[string]any{
		"key_name":    "alice",
		"endpoint":    "/v1/images/generations",
		"duration_ms": 120,
		"status":      "success",
	}); err != nil {
		t.Fatalf("Add(call) error = %v", err)
	}
	if err := logs.Add(LogTypeAudit, "GET /api/settings", map[string]any{
		"username":       "admin",
		"module":         "settings",
		"method":         "GET",
		"path":           "/api/settings",
		"status":         403,
		"ip_address":     "127.0.0.1",
		"operation_type": "查询",
		"log_level":      "warning",
	}); err != nil {
		t.Fatalf("Add(audit) error = %v", err)
	}

	all := logs.Search(LogQuery{Limit: 10})
	if len(all) != 3 {
		t.Fatalf("Search(all) length = %d, want 3: %#v", len(all), all)
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

	callLogs := logs.Search(LogQuery{Username: "alice", Module: "调用", Limit: 10})
	if len(callLogs) != 1 || callLogs[0]["type"] != LogTypeCall {
		t.Fatalf("Search(call) = %#v", callLogs)
	}
}
