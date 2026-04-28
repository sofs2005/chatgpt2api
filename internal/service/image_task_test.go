package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImageTaskServiceIdempotencyOwnerIsolationAndCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	handlerCalls := make(chan map[string]any, 4)
	handler := func(ctx context.Context, identity Identity, payload map[string]any) (map[string]any, error) {
		handlerCalls <- payload
		return map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil
	}
	svc := NewImageTaskService(path, handler, handler, func() int { return 30 })

	alice := Identity{ID: "alice", Name: "Alice", Role: "user"}
	bob := Identity{ID: "bob", Name: "Bob", Role: "user"}

	first, err := svc.SubmitGeneration(context.Background(), alice, "task-1", "draw", "gpt-image-2", "1024x1024", "https://base.test")
	if err != nil {
		t.Fatalf("SubmitGeneration() error = %v", err)
	}
	second, err := svc.SubmitGeneration(context.Background(), alice, "task-1", "different", "gpt-image-2", "1024x1024", "https://base.test")
	if err != nil {
		t.Fatalf("second SubmitGeneration() error = %v", err)
	}
	if first["id"] != second["id"] {
		t.Fatalf("idempotent task id mismatch: %#v %#v", first, second)
	}
	waitForTaskStatus(t, svc, alice, "task-1", TaskStatusSuccess)
	select {
	case <-handlerCalls:
	default:
		t.Fatal("handler was not called")
	}
	if len(handlerCalls) != 0 {
		t.Fatalf("handler calls after duplicate = %d extra, want 0", len(handlerCalls))
	}
	if got := svc.ListTasks(bob, []string{"task-1"}); len(got["items"].([]map[string]any)) != 0 {
		t.Fatalf("bob can see alice task: %#v", got)
	}
	if got := svc.ListTasks(bob, []string{"task-1"}); len(got["missing_ids"].([]string)) != 1 {
		t.Fatalf("bob missing ids = %#v", got)
	}
}

func TestImageTaskServiceRestoresUnfinishedTasksAsErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_tasks.json")
	raw := map[string]any{"tasks": []map[string]any{
		{"id": "queued", "owner_id": "alice", "status": TaskStatusQueued, "mode": "generate", "created_at": "2026-01-01 00:00:00", "updated_at": "2026-01-01 00:00:00"},
		{"id": "running", "owner_id": "alice", "status": TaskStatusRunning, "mode": "edit", "created_at": "2026-01-01 00:00:00", "updated_at": "2026-01-01 00:00:00"},
	}}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	svc := NewImageTaskService(path, failingImageTaskHandler, failingImageTaskHandler, func() int { return 30 })
	got := svc.ListTasks(Identity{ID: "alice"}, []string{"queued", "running"})
	items := got["items"].([]map[string]any)
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	for _, item := range items {
		if item["status"] != TaskStatusError {
			t.Fatalf("unfinished task was not restored as error: %#v", item)
		}
		if item["error"] == nil {
			t.Fatalf("restored task missing error text: %#v", item)
		}
	}
}

func failingImageTaskHandler(context.Context, Identity, map[string]any) (map[string]any, error) {
	return nil, errors.New("unexpected handler call")
}

func waitForTaskStatus(t *testing.T, svc *ImageTaskService, identity Identity, taskID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := svc.ListTasks(identity, []string{taskID})
		items := got["items"].([]map[string]any)
		if len(items) == 1 && items[0]["status"] == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach status %s", taskID, want)
}
