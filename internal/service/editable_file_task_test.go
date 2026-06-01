package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

func TestEditableFileTaskServiceSubmitListAndPathSecurity(t *testing.T) {
	store := newTestStorageBackend(t).(storage.JSONDocumentBackend)
	dataDir := t.TempDir()
	var calls int32
	svc := NewEditableFileTaskService(store, dataDir, func(ctx context.Context, kind, prompt string, base64Images []string, outputDir string) (EditableFileRunResult, error) {
		atomic.AddInt32(&calls, 1)
		if kind != "ppt" {
			t.Fatalf("runner kind = %q, want ppt", kind)
		}
		if prompt != "make deck" {
			t.Fatalf("runner prompt = %q, want make deck", prompt)
		}
		if len(base64Images) != 0 {
			t.Fatalf("runner base64Images = %#v, want empty", base64Images)
		}
		if err := os.WriteFile(filepath.Join(outputDir, "deck.pptx"), []byte("ppt-bytes"), 0o644); err != nil {
			return EditableFileRunResult{}, err
		}
		return EditableFileRunResult{
			ConversationID: "conv-1",
			PrimaryPath:    filepath.Join(outputDir, "deck.pptx"),
		}, nil
	})

	alice := Identity{ID: "alice", Role: AuthRoleUser}
	bob := Identity{ID: "bob", Role: AuthRoleUser}

	first, err := svc.Submit(context.Background(), alice, "ppt", "make deck", nil, "task-1")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	second, err := svc.Submit(context.Background(), alice, "ppt", "different prompt", nil, "task-1")
	if err != nil {
		t.Fatalf("second Submit() error = %v", err)
	}
	if first["id"] != second["id"] {
		t.Fatalf("idempotent task id mismatch: %#v %#v", first, second)
	}

	waitForEditableFileTaskStatus(t, svc, alice, "task-1", TaskStatusSuccess)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("runner calls = %d, want 1", got)
	}

	items, missing := editableFileTaskListResult(t, svc.List(alice, []string{"task-1", "missing"}))
	if len(items) != 1 || len(missing) != 1 || missing[0] != "missing" {
		t.Fatalf("alice list = %#v / %#v", items, missing)
	}
	item := items[0]
	if item["status"] != TaskStatusSuccess || item["kind"] != "ppt" || item["taskId"] != "task-1" {
		t.Fatalf("alice item = %#v", item)
	}
	result := util.StringMap(item["result"])
	if result["conversation_id"] != "conv-1" {
		t.Fatalf("result conversation_id = %#v", result)
	}
	primaryPath := util.Clean(result["primary_path"])
	if primaryPath == "" {
		t.Fatalf("result primary_path missing: %#v", result)
	}
	abs, err := svc.PublicFilePath(primaryPath)
	if err != nil {
		t.Fatalf("PublicFilePath() error = %v", err)
	}
	if !strings.HasSuffix(abs, filepath.FromSlash(primaryPath)) {
		t.Fatalf("PublicFilePath() = %q, want suffix %q", abs, primaryPath)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("result file not found: %v", err)
	}

	otherItems, otherMissing := editableFileTaskListResult(t, svc.List(bob, []string{"task-1"}))
	if len(otherItems) != 0 || len(otherMissing) != 1 {
		t.Fatalf("bob list = %#v / %#v", otherItems, otherMissing)
	}

	for _, path := range []string{"../secret.txt", "/absolute.txt", "nested/../../secret.txt", ""} {
		if _, err := svc.PublicFilePath(path); err == nil {
			t.Fatalf("PublicFilePath(%q) error = nil, want path rejection", path)
		}
	}
}

func TestEditableFileTaskServiceExposesProgressLogs(t *testing.T) {
	store := newTestStorageBackend(t).(storage.JSONDocumentBackend)
	svc := NewEditableFileTaskService(store, t.TempDir(), func(ctx context.Context, kind, prompt string, base64Images []string, outputDir string) (EditableFileRunResult, error) {
		util.LogProgress(ctx, "正在轮询上游会话文件")
		return EditableFileRunResult{}, nil
	})
	identity := Identity{ID: "alice", Role: AuthRoleUser}
	if _, err := svc.Submit(context.Background(), identity, "ppt", "make deck", nil, "task-log"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitForEditableFileTaskStatus(t, svc, identity, "task-log", TaskStatusSuccess)
	items, _ := editableFileTaskListResult(t, svc.List(identity, []string{"task-log"}))
	logs, ok := items[0]["logs"].([]map[string]any)
	if !ok || len(logs) < 3 {
		t.Fatalf("logs = %#v, want progress entries", items[0]["logs"])
	}
	joined := ""
	for _, log := range logs {
		joined += util.Clean(log["message"]) + "\n"
	}
	for _, want := range []string{"任务已入队", "任务开始执行", "正在轮询上游会话文件", "任务执行成功"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("logs missing %q: %#v", want, logs)
		}
	}
}

func TestEditableFileTaskServiceWritesProgressToRuntimeLogger(t *testing.T) {
	store := newTestStorageBackend(t).(storage.JSONDocumentBackend)
	dataDir := t.TempDir()
	logger, err := NewLogger(dataDir, func() []string { return []string{"info", "warning", "error"} })
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	svc := NewEditableFileTaskService(store, dataDir, func(ctx context.Context, kind, prompt string, base64Images []string, outputDir string) (EditableFileRunResult, error) {
		util.LogProgress(ctx, "正在轮询上游会话文件")
		return EditableFileRunResult{}, nil
	})
	svc.SetLogger(logger)
	identity := Identity{ID: "alice", Role: AuthRoleUser}
	if _, err := svc.Submit(context.Background(), identity, "ppt", "make deck", nil, "task-log-runtime"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitForEditableFileTaskStatus(t, svc, identity, "task-log-runtime", TaskStatusSuccess)
	if err := logger.Close(); err != nil {
		t.Fatalf("logger.Close() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "logs", "server.log"))
	if err != nil {
		t.Fatalf("ReadFile(server.log) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"editable file task progress", "task-log-runtime", "正在轮询上游会话文件"} {
		if !strings.Contains(text, want) {
			t.Fatalf("server.log missing %q: %s", want, text)
		}
	}
}

func TestEditableFileTaskServiceRestartsQueuedTasksAsErrors(t *testing.T) {
	store := newTestStorageBackend(t).(storage.JSONDocumentBackend)
	if err := store.SaveJSONDocument(editableFileTasksDocument, map[string]any{
		"tasks": []map[string]any{
			{"id": "queued", "owner_id": "alice", "status": TaskStatusQueued, "kind": "ppt", "created_at": "2026-01-01 00:00:00", "updated_at": "2026-01-01 00:00:00"},
			{"id": "running", "owner_id": "alice", "status": TaskStatusRunning, "kind": "psd", "created_at": "2026-01-01 00:00:00", "updated_at": "2026-01-01 00:00:00"},
		},
	}); err != nil {
		t.Fatalf("SaveJSONDocument() error = %v", err)
	}

	svc := NewEditableFileTaskService(store, t.TempDir(), func(context.Context, string, string, []string, string) (EditableFileRunResult, error) {
		return EditableFileRunResult{}, nil
	})

	items, missing := editableFileTaskListResult(t, svc.List(Identity{ID: "alice", Role: AuthRoleUser}, []string{"queued", "running"}))
	if len(items) != 2 || len(missing) != 0 {
		t.Fatalf("restored list = %#v / %#v", items, missing)
	}
	for _, item := range items {
		if item["status"] != TaskStatusError {
			t.Fatalf("restored task status = %#v, want error", item["status"])
		}
		if !strings.Contains(util.Clean(item["error"]), "重启") {
			t.Fatalf("restored task error = %#v", item["error"])
		}
	}
}

func TestEditableFileTaskServiceRejectsInvalidEditableKindsAndPrompt(t *testing.T) {
	svc := NewEditableFileTaskService(newTestStorageBackend(t).(storage.JSONDocumentBackend), t.TempDir(), func(context.Context, string, string, []string, string) (EditableFileRunResult, error) {
		return EditableFileRunResult{}, nil
	})
	identity := Identity{ID: "alice", Role: AuthRoleUser}
	for name, submit := range map[string]func() error{
		"kind": func() error {
			_, err := svc.Submit(context.Background(), identity, "doc", "prompt", nil, "")
			return err
		},
		"prompt": func() error { _, err := svc.Submit(context.Background(), identity, "ppt", "   ", nil, ""); return err },
		"psd images": func() error {
			_, err := svc.Submit(context.Background(), identity, "psd", "prompt", nil, "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := submit(); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func waitForEditableFileTaskStatus(t *testing.T, svc *EditableFileTaskService, identity Identity, id, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items, _ := editableFileTaskListResult(t, svc.List(identity, []string{id}))
		if len(items) == 1 && items[0]["status"] == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s status %s", id, want)
}

func editableFileTaskListResult(t *testing.T, got map[string]any) ([]map[string]any, []string) {
	t.Helper()
	items, ok := got["items"].([]map[string]any)
	if !ok {
		t.Fatalf("items type = %T", got["items"])
	}
	missing, ok := got["missing_ids"].([]string)
	if !ok {
		t.Fatalf("missing_ids type = %T", got["missing_ids"])
	}
	return items, missing
}
