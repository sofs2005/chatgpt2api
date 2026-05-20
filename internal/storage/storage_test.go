package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseBackendStoresDocumentsAndLogs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	backend, err := NewDatabaseBackend("sqlite:///" + filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	defer backend.db.Close()

	if err := backend.SaveAccounts([]map[string]any{{"access_token": "token-1", "type": "Plus"}}); err != nil {
		t.Fatalf("SaveAccounts() error = %v", err)
	}
	if err := backend.SaveAuthKeys([]map[string]any{{"id": "key-1", "key": "sk-test"}}); err != nil {
		t.Fatalf("SaveAuthKeys() error = %v", err)
	}
	if err := backend.SaveJSONDocument("announcements.json", []map[string]any{{"id": "a1", "content": "hello"}}); err != nil {
		t.Fatalf("SaveJSONDocument() error = %v", err)
	}
	if err := backend.AppendLog(map[string]any{
		"time":    "2026-04-30 10:00:00",
		"type":    "event",
		"summary": "ok",
		"detail":  map[string]any{"status": "success"},
	}); err != nil {
		t.Fatalf("AppendLog() error = %v", err)
	}
	if err := backend.AppendLog(map[string]any{
		"time":    "2026-04-29 10:00:00",
		"type":    "event",
		"summary": "skip",
	}); err != nil {
		t.Fatalf("AppendLog() error = %v", err)
	}

	accounts, err := backend.LoadAccounts()
	if err != nil {
		t.Fatalf("LoadAccounts() error = %v", err)
	}
	if len(accounts) != 1 || accounts[0]["access_token"] != "token-1" {
		t.Fatalf("LoadAccounts() = %#v", accounts)
	}

	doc, err := backend.LoadJSONDocument("announcements.json")
	if err != nil {
		t.Fatalf("LoadJSONDocument() error = %v", err)
	}
	items, ok := doc.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("LoadJSONDocument() = %#v", doc)
	}

	logs, err := backend.QueryLogs("2026-04-30", "2026-04-30", 10)
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if len(logs) != 1 || logs[0]["summary"] != "ok" {
		t.Fatalf("QueryLogs() = %#v", logs)
	}

	health := backend.HealthCheck()
	if health["document_count"] != 1 || health["log_count"] != 2 {
		t.Fatalf("HealthCheck() = %#v", health)
	}
}

func TestDatabaseBackendQueryLogsEmptyReturnsJSONArray(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	backend, err := NewDatabaseBackend("sqlite:///" + filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	defer backend.db.Close()

	logs, err := backend.QueryLogs("2026-04-30", "2026-04-30", 10)
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if logs == nil {
		t.Fatal("QueryLogs() returned nil slice, want empty slice")
	}
	data, err := json.Marshal(map[string]any{"items": logs})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"items":[]}` {
		t.Fatalf("marshaled logs = %s, want {\"items\":[]}", data)
	}
}

func TestDatabaseBackendDeletesLogsBeforeDay(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	backend, err := NewDatabaseBackend("sqlite:///" + filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	defer backend.db.Close()

	for _, item := range []map[string]any{
		{"time": "2026-04-28 10:00:00", "type": "event", "summary": "old"},
		{"time": "2026-04-29 10:00:00", "type": "event", "summary": "cutoff"},
		{"time": "2026-04-30 10:00:00", "type": "event", "summary": "new"},
	} {
		if err := backend.AppendLog(item); err != nil {
			t.Fatalf("AppendLog() error = %v", err)
		}
	}

	deleted, err := backend.DeleteLogsBefore("2026-04-29")
	if err != nil {
		t.Fatalf("DeleteLogsBefore() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteLogsBefore() deleted = %d, want 1", deleted)
	}
	logs, err := backend.QueryLogs("", "", 0)
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("remaining logs = %#v, want 2", logs)
	}
}

func TestNewBackendFromEnvDefaultsToSQLiteProjectDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STORAGE_BACKEND", "")
	t.Setenv("DATABASE_URL", "")

	backend, err := NewBackendFromEnv(dir)
	if err != nil {
		t.Fatalf("NewBackendFromEnv() error = %v", err)
	}
	database, ok := backend.(*DatabaseBackend)
	if !ok {
		t.Fatalf("NewBackendFromEnv() returned %T, want *DatabaseBackend", backend)
	}
	defer database.db.Close()
	if database.driver != "sqlite" {
		t.Fatalf("driver = %q, want sqlite", database.driver)
	}
	want := filepath.ToSlash(filepath.Join(dir, "chatgpt2api.db"))
	if database.dsn != want {
		t.Fatalf("dsn = %q, want %q", database.dsn, want)
	}
}

func TestNewBackendFromEnvRejectsJSONBackend(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "json")
	t.Setenv("DATABASE_URL", "")

	_, err := NewBackendFromEnv(t.TempDir())
	if err == nil {
		t.Fatal("NewBackendFromEnv() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "unknown storage backend: json") {
		t.Fatalf("NewBackendFromEnv() error = %v", err)
	}
}

func TestDocumentNameValidation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	backend, err := NewDatabaseBackend("sqlite:///" + filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	defer backend.db.Close()

	for _, name := range []string{"../x.json", "/x.json", "a/../x.json", "C:/x.json"} {
		t.Run(name, func(t *testing.T) {
			if err := backend.SaveJSONDocument(name, map[string]any{}); err == nil {
				t.Fatalf("SaveJSONDocument(%q) succeeded, want error", name)
			}
		})
	}
}
