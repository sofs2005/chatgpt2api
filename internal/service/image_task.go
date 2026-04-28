package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/util"
)

const (
	TaskStatusQueued  = "queued"
	TaskStatusRunning = "running"
	TaskStatusSuccess = "success"
	TaskStatusError   = "error"
)

type ImageTaskHandler func(context.Context, Identity, map[string]any) (map[string]any, error)

type ImageTaskService struct {
	mu              sync.RWMutex
	path            string
	generation      ImageTaskHandler
	edit            ImageTaskHandler
	retentionGetter func() int
	tasks           map[string]map[string]any
}

func NewImageTaskService(path string, generation ImageTaskHandler, edit ImageTaskHandler, retentionGetter func() int) *ImageTaskService {
	s := &ImageTaskService{path: path, generation: generation, edit: edit, retentionGetter: retentionGetter, tasks: map[string]map[string]any{}}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	s.mu.Lock()
	s.tasks = s.loadLocked()
	changed := s.recoverUnfinishedLocked()
	if s.cleanupLocked() || changed {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	return s
}

func (s *ImageTaskService) SubmitGeneration(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, baseURL string) (map[string]any, error) {
	payload := map[string]any{"prompt": prompt, "model": model, "n": 1, "size": size, "response_format": "url", "base_url": baseURL}
	return s.submit(ctx, identity, clientTaskID, "generate", payload)
}

func (s *ImageTaskService) SubmitEdit(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, baseURL string, images any) (map[string]any, error) {
	payload := map[string]any{"prompt": prompt, "images": images, "model": model, "n": 1, "size": size, "response_format": "url", "base_url": baseURL}
	return s.submit(ctx, identity, clientTaskID, "edit", payload)
}

func (s *ImageTaskService) ListTasks(identity Identity, taskIDs []string) map[string]any {
	owner := ownerID(identity)
	requested := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		if id = strings.TrimSpace(id); id != "" {
			requested = append(requested, id)
		}
	}
	s.mu.Lock()
	if s.cleanupLocked() {
		_ = s.saveLocked()
	}
	var items []map[string]any
	var missing []string
	if len(requested) == 0 {
		for _, task := range s.tasks {
			if task["owner_id"] == owner {
				items = append(items, publicTask(task))
			}
		}
		sort.Slice(items, func(i, j int) bool { return util.Clean(items[i]["updated_at"]) > util.Clean(items[j]["updated_at"]) })
	} else {
		for _, id := range requested {
			task := s.tasks[taskKey(owner, id)]
			if task == nil {
				missing = append(missing, id)
			} else {
				items = append(items, publicTask(task))
			}
		}
	}
	s.mu.Unlock()
	if missing == nil {
		missing = []string{}
	}
	return map[string]any{"items": items, "missing_ids": missing}
}

func (s *ImageTaskService) submit(ctx context.Context, identity Identity, clientTaskID, mode string, payload map[string]any) (map[string]any, error) {
	taskID := strings.TrimSpace(clientTaskID)
	if taskID == "" {
		return nil, fmt.Errorf("client_task_id is required")
	}
	owner := ownerID(identity)
	key := taskKey(owner, taskID)
	now := util.NowLocal()
	s.mu.Lock()
	cleaned := s.cleanupLocked()
	if existing := s.tasks[key]; existing != nil {
		if cleaned {
			_ = s.saveLocked()
		}
		result := publicTask(existing)
		s.mu.Unlock()
		return result, nil
	}
	task := map[string]any{"id": taskID, "owner_id": owner, "status": TaskStatusQueued, "mode": mode, "model": firstNonEmpty(util.Clean(payload["model"]), "gpt-image-2"), "size": util.Clean(payload["size"]), "created_at": now, "updated_at": now}
	s.tasks[key] = task
	_ = s.saveLocked()
	result := publicTask(task)
	s.mu.Unlock()
	go s.runTask(context.Background(), key, mode, identity, payload)
	return result, nil
}

func (s *ImageTaskService) runTask(ctx context.Context, key, mode string, identity Identity, payload map[string]any) {
	s.updateTask(key, map[string]any{"status": TaskStatusRunning, "error": ""})
	handler := s.generation
	if mode == "edit" {
		handler = s.edit
	}
	result, err := handler(ctx, identity, payload)
	if err != nil {
		s.updateTask(key, map[string]any{"status": TaskStatusError, "error": err.Error(), "data": []any{}})
		return
	}
	data := util.AsMapSlice(result["data"])
	if len(data) == 0 {
		message := firstNonEmpty(util.Clean(result["message"]), "image task returned no image data")
		s.updateTask(key, map[string]any{"status": TaskStatusError, "error": message, "data": []any{}})
		return
	}
	s.updateTask(key, map[string]any{"status": TaskStatusSuccess, "data": data, "error": ""})
}

func (s *ImageTaskService) updateTask(key string, updates map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil {
		return
	}
	for k, v := range updates {
		task[k] = v
	}
	task["updated_at"] = util.NowLocal()
	_ = s.saveLocked()
}

func (s *ImageTaskService) loadLocked() map[string]map[string]any {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return map[string]map[string]any{}
	}
	var raw any
	if json.Unmarshal(data, &raw) != nil {
		return map[string]map[string]any{}
	}
	if obj, ok := raw.(map[string]any); ok {
		raw = obj["tasks"]
	}
	tasks := map[string]map[string]any{}
	for _, item := range anyList(raw) {
		task, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := util.Clean(task["id"])
		owner := util.Clean(task["owner_id"])
		if id == "" || owner == "" {
			continue
		}
		status := util.Clean(task["status"])
		if status != TaskStatusQueued && status != TaskStatusRunning && status != TaskStatusSuccess && status != TaskStatusError {
			status = TaskStatusError
		}
		mode := "generate"
		if task["mode"] == "edit" {
			mode = "edit"
		}
		normalized := map[string]any{"id": id, "owner_id": owner, "status": status, "mode": mode, "model": firstNonEmpty(util.Clean(task["model"]), "gpt-image-2"), "size": util.Clean(task["size"]), "created_at": firstNonEmpty(util.Clean(task["created_at"]), util.NowLocal()), "updated_at": firstNonEmpty(util.Clean(task["updated_at"]), util.Clean(task["created_at"]), util.NowLocal())}
		if data := util.AsMapSlice(task["data"]); data != nil {
			normalized["data"] = data
		}
		if errText := util.Clean(task["error"]); errText != "" {
			normalized["error"] = errText
		}
		tasks[taskKey(owner, id)] = normalized
	}
	return tasks
}

func (s *ImageTaskService) saveLocked() error {
	items := make([]map[string]any, 0, len(s.tasks))
	for _, task := range s.tasks {
		items = append(items, task)
	}
	sort.Slice(items, func(i, j int) bool { return util.Clean(items[i]["updated_at"]) > util.Clean(items[j]["updated_at"]) })
	data, err := json.MarshalIndent(map[string]any{"tasks": items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *ImageTaskService) recoverUnfinishedLocked() bool {
	changed := false
	for _, task := range s.tasks {
		if task["status"] == TaskStatusQueued || task["status"] == TaskStatusRunning {
			task["status"] = TaskStatusError
			task["error"] = "服务已重启，未完成的图片任务已中断"
			task["updated_at"] = util.NowLocal()
			changed = true
		}
	}
	return changed
}

func (s *ImageTaskService) cleanupLocked() bool {
	days := 30
	if s.retentionGetter != nil {
		days = s.retentionGetter()
	}
	if days < 1 {
		days = 1
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	removed := false
	for key, task := range s.tasks {
		status := task["status"]
		if status != TaskStatusSuccess && status != TaskStatusError {
			continue
		}
		if parseTaskTime(task["updated_at"]).Before(cutoff) {
			delete(s.tasks, key)
			removed = true
		}
	}
	return removed
}

func publicTask(task map[string]any) map[string]any {
	item := map[string]any{"id": task["id"], "status": task["status"], "mode": task["mode"], "model": task["model"], "size": task["size"], "created_at": task["created_at"], "updated_at": task["updated_at"]}
	if task["data"] != nil {
		item["data"] = task["data"]
	}
	if util.Clean(task["error"]) != "" {
		item["error"] = task["error"]
	}
	return item
}

func ownerID(identity Identity) string {
	if identity.ID == "" {
		return "anonymous"
	}
	return identity.ID
}

func taskKey(owner, id string) string {
	return owner + ":" + id
}

func parseTaskTime(value any) time.Time {
	text := util.Clean(value)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", time.RFC3339Nano} {
		if t, err := time.Parse(layout, text); err == nil {
			return t
		}
	}
	return time.Unix(0, 0)
}
