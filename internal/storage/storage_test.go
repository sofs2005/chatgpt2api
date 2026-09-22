package storage

import (
	"database/sql"
	"encoding/json"
	"os"
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

func TestDatabaseBackendRoundTripsAccountFingerprint(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	backend, err := NewDatabaseBackend("sqlite:///" + filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	defer backend.db.Close()

	want := map[string]any{
		"access_token": "token-1",
		"type":         "Plus",
		"fp": map[string]any{
			"version":                1,
			"user-agent":             "Mozilla/5.0",
			"sec-ch-ua":              "\"Not:A-Brand\";v=\"99\"",
			"oai-device-id":          "device-1",
			"oai-session-id":         "session-1",
		},
		"custom_note": "keep-me",
	}
	if err := backend.SaveAccounts([]map[string]any{want}); err != nil {
		t.Fatalf("SaveAccounts() error = %v", err)
	}

	accounts, err := backend.LoadAccounts()
	if err != nil {
		t.Fatalf("LoadAccounts() error = %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("LoadAccounts() len = %d, want 1", len(accounts))
	}
	if got := accounts[0]["access_token"]; got != "token-1" {
		t.Fatalf("LoadAccounts()[0][access_token] = %#v", got)
	}
	if got := accounts[0]["type"]; got != "Plus" {
		t.Fatalf("LoadAccounts()[0][type] = %#v", got)
	}
	if got := accounts[0]["custom_note"]; got != "keep-me" {
		t.Fatalf("LoadAccounts()[0][custom_note] = %#v", got)
	}
	fp, ok := accounts[0]["fp"].(map[string]any)
	if !ok {
		t.Fatalf("LoadAccounts()[0][fp] = %#v, want map", accounts[0]["fp"])
	}
	if got := fp["version"]; got != float64(1) {
		t.Fatalf("LoadAccounts()[0][fp][version] = %#v", got)
	}
	if got := fp["user-agent"]; got != "Mozilla/5.0" {
		t.Fatalf("LoadAccounts()[0][fp][user-agent] = %#v", got)
	}
	if got := fp["sec-ch-ua"]; got != "\"Not:A-Brand\";v=\"99\"" {
		t.Fatalf("LoadAccounts()[0][fp][sec-ch-ua] = %#v", got)
	}
	if got := fp["oai-device-id"]; got != "device-1" {
		t.Fatalf("LoadAccounts()[0][fp][oai-device-id] = %#v", got)
	}
	if got := fp["oai-session-id"]; got != "session-1" {
		t.Fatalf("LoadAccounts()[0][fp][oai-session-id] = %#v", got)
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

func TestSQLiteEnablesIncrementalAutoVacuum(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	backend, err := NewDatabaseBackend("sqlite:///" + filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	defer backend.db.Close()

	var mode int
	if err := backend.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("read auto_vacuum: %v", err)
	}
	if mode != autoVacuumIncremental {
		t.Fatalf("auto_vacuum = %d, want %d", mode, autoVacuumIncremental)
	}
}

// 已有库打开时应迁移到 INCREMENTAL 并回收历史空闲页。
// 没有这一步时，删除只标记空闲，文件永不收缩。
func TestSQLiteVacuumMigratesExistingDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	dsn := "sqlite:///" + filepath.ToSlash(dbPath)

	// 用关闭 auto_vacuum 的连接建库并塞入数据，模拟历史库。
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE blob_store (id INTEGER PRIMARY KEY, data TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	payload := strings.Repeat("x", 4096)
	for i := 0; i < 400; i++ {
		if _, err := raw.Exec(`INSERT INTO blob_store (data) VALUES (?)`, payload); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if _, err := raw.Exec(`DELETE FROM blob_store`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	grown, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	backend, err := NewDatabaseBackend(dsn)
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	defer backend.db.Close()

	var mode, free int
	if err := backend.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("read auto_vacuum: %v", err)
	}
	if mode != autoVacuumIncremental {
		t.Fatalf("auto_vacuum = %d, want %d", mode, autoVacuumIncremental)
	}
	if err := backend.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatalf("read freelist_count: %v", err)
	}
	if free != 0 {
		t.Fatalf("freelist_count = %d, want 0 after migration VACUUM", free)
	}

	shrunk, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if shrunk.Size() >= grown.Size() {
		t.Fatalf("file size = %d, want smaller than %d after VACUUM", shrunk.Size(), grown.Size())
	}
}

// 删除日志后应主动回收空闲页，否则保留期清理不释放磁盘。
func TestDeleteLogsBeforeReclaimsFreePages(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	backend, err := NewDatabaseBackend("sqlite:///" + filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	defer backend.db.Close()

	payload := strings.Repeat("y", 4096)
	for i := 0; i < 400; i++ {
		if err := backend.AppendLog(map[string]any{
			"time":    "2026-04-30 10:00:00",
			"type":    "event",
			"summary": "bulk",
			"detail":  map[string]any{"payload": payload},
		}); err != nil {
			t.Fatalf("AppendLog() error = %v", err)
		}
	}

	deleted, err := backend.DeleteLogsBefore("2026-05-01")
	if err != nil {
		t.Fatalf("DeleteLogsBefore() error = %v", err)
	}
	if deleted != 400 {
		t.Fatalf("DeleteLogsBefore() = %d, want 400", deleted)
	}

	var free int
	if err := backend.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatalf("read freelist_count: %v", err)
	}
	if free != 0 {
		t.Fatalf("freelist_count = %d, want 0 after reclaim", free)
	}
}
