package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const editableFileTasksDocument = "editable_file_tasks"
const editableFileTaskMaxLogs = 80

type EditableFileRunResult struct {
	ConversationID string
	PrimaryPath    string
	ZipPath        string
}

type EditableFileRunner func(ctx context.Context, kind, prompt string, base64Images []string, outputDir string) (EditableFileRunResult, error)

type EditableFileTaskService struct {
	mu        sync.RWMutex
	store     storage.JSONDocumentBackend
	dataDir   string
	filesDir  string
	runner    EditableFileRunner
	tasks     map[string]map[string]any
	clientIDs map[string]string
}

func NewEditableFileTaskService(store storage.JSONDocumentBackend, dataDir string, runner EditableFileRunner) *EditableFileTaskService {
	filesDir := filepath.Join(dataDir, "files")
	_ = os.MkdirAll(filesDir, 0o755)
	s := &EditableFileTaskService{store: store, dataDir: dataDir, filesDir: filesDir, runner: runner, tasks: map[string]map[string]any{}, clientIDs: map[string]string{}}
	s.mu.Lock()
	s.tasks = s.loadLocked()
	for key, task := range s.tasks {
		if clientTaskID := util.Clean(task["client_task_id"]); clientTaskID != "" {
			s.clientIDs[taskKey(util.Clean(task["owner_id"]), clientTaskID)] = key
		}
	}
	if s.recoverUnfinishedLocked() {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	return s
}

func (s *EditableFileTaskService) Submit(ctx context.Context, identity Identity, kind, prompt string, base64Images []string, clientTaskID string) (map[string]any, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "ppt" && kind != "psd" {
		return nil, fmt.Errorf("kind must be ppt or psd")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if kind == "psd" && len(cleanEditableImages(base64Images)) == 0 {
		return nil, fmt.Errorf("psd generation requires at least one image")
	}
	if s == nil || s.runner == nil {
		return nil, fmt.Errorf("editable file runner is not configured")
	}
	owner := ownerID(identity)
	clientTaskID = strings.TrimSpace(clientTaskID)
	clientKey := ""
	if clientTaskID != "" {
		clientKey = taskKey(owner, clientTaskID)
	}
	now := util.NowLocal()
	s.mu.Lock()
	if clientKey != "" {
		if key := s.clientIDs[clientKey]; key != "" {
			if existing := s.tasks[key]; existing != nil {
				item := publicEditableFileTask(existing)
				s.mu.Unlock()
				return item, nil
			}
		}
	}
	id := clientTaskID
	if id == "" {
		id = "eft_" + util.NewHex(24)
	}
	key := taskKey(owner, id)
	if existing := s.tasks[key]; existing != nil {
		item := publicEditableFileTask(existing)
		s.mu.Unlock()
		return item, nil
	}
	task := map[string]any{
		"id":              id,
		"taskId":          id,
		"owner_id":        owner,
		"client_task_id":  clientTaskID,
		"status":          TaskStatusQueued,
		"kind":            kind,
		"created_at":      now,
		"updated_at":      now,
		"elapsed_seconds": 0,
		"logs":            []map[string]any{{"time": now, "message": "任务已入队"}},
	}
	s.tasks[key] = task
	if clientKey != "" {
		s.clientIDs[clientKey] = key
	}
	_ = s.saveLocked()
	item := publicEditableFileTask(task)
	s.mu.Unlock()
	go s.runTask(context.Background(), key, kind, prompt, cleanEditableImages(base64Images))
	return item, nil
}

func (s *EditableFileTaskService) List(identity Identity, ids []string) map[string]any {
	owner := ownerID(identity)
	requested := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			requested = append(requested, id)
		}
	}
	s.mu.RLock()
	items := make([]map[string]any, 0)
	missing := make([]string, 0)
	if len(requested) == 0 {
		for _, task := range s.tasks {
			if util.Clean(task["owner_id"]) == owner {
				items = append(items, publicEditableFileTask(task))
			}
		}
		sort.Slice(items, func(i, j int) bool { return util.Clean(items[i]["updated_at"]) > util.Clean(items[j]["updated_at"]) })
	} else {
		for _, id := range requested {
			if task := s.tasks[taskKey(owner, id)]; task != nil {
				items = append(items, publicEditableFileTask(task))
			} else {
				missing = append(missing, id)
			}
		}
	}
	s.mu.RUnlock()
	return map[string]any{"items": items, "missing_ids": missing}
}

func (s *EditableFileTaskService) PublicFilePath(relative string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("editable file task service is not configured")
	}
	rel := strings.TrimSpace(filepath.ToSlash(relative))
	if rel == "" {
		return "", fmt.Errorf("file path is required")
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") || filepath.IsAbs(filepath.FromSlash(cleaned)) || strings.ContainsRune(cleaned, 0) {
		return "", fmt.Errorf("invalid file path")
	}
	root, err := filepath.Abs(s.filesDir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(cleaned)))
	if err != nil {
		return "", err
	}
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid file path")
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("file not found")
	}
	return target, nil
}

func (s *EditableFileTaskService) runTask(ctx context.Context, key, kind, prompt string, base64Images []string) {
	started := time.Now()
	if !s.markRunning(key) {
		return
	}
	runCtx := util.WithProgressLogger(ctx, func(message string) {
		s.appendTaskLog(key, message)
	})
	outputDir := filepath.Join(s.filesDir, time.Now().Format("2006"), time.Now().Format("01"), time.Now().Format("02"), strings.ReplaceAll(key, ":", "_"))
	_ = os.MkdirAll(outputDir, 0o755)
	util.LogProgress(runCtx, "正在调用上游导出链路")
	result, err := s.runner(runCtx, kind, prompt, base64Images, outputDir)
	updates := map[string]any{"elapsed_seconds": int(time.Since(started).Seconds())}
	if err != nil {
		updates["status"] = TaskStatusError
		updates["error"] = err.Error()
		s.updateTask(key, updates)
		s.appendTaskLog(key, "任务执行失败："+err.Error())
		return
	}
	updates["status"] = TaskStatusSuccess
	updates["error"] = ""
	updates["result"] = editableResultMap(result, s.filesDir)
	s.updateTask(key, updates)
	s.appendTaskLog(key, "任务执行成功")
}

func (s *EditableFileTaskService) markRunning(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil || util.Clean(task["status"]) != TaskStatusQueued {
		return false
	}
	task["status"] = TaskStatusRunning
	now := util.NowLocal()
	task["updated_at"] = now
	appendEditableTaskLogLocked(task, now, "任务开始执行")
	_ = s.saveLocked()
	return true
}

func (s *EditableFileTaskService) appendTaskLog(key, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil {
		return
	}
	now := util.NowLocal()
	appendEditableTaskLogLocked(task, now, message)
	task["updated_at"] = now
	_ = s.saveLocked()
}

func appendEditableTaskLogLocked(task map[string]any, timestamp, message string) {
	logs := editableTaskLogs(task["logs"])
	logs = append(logs, map[string]any{"time": timestamp, "message": message})
	if len(logs) > editableFileTaskMaxLogs {
		logs = logs[len(logs)-editableFileTaskMaxLogs:]
	}
	task["logs"] = logs
}

func editableTaskLogs(raw any) []map[string]any {
	logs := make([]map[string]any, 0)
	for _, item := range anyList(raw) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		message := util.Clean(entry["message"])
		if message == "" {
			continue
		}
		logs = append(logs, map[string]any{
			"time":    util.Clean(entry["time"]),
			"message": message,
		})
	}
	return logs
}

func (s *EditableFileTaskService) updateTask(key string, updates map[string]any) {
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

func (s *EditableFileTaskService) loadLocked() map[string]map[string]any {
	raw := loadStoredJSON(s.store, editableFileTasksDocument)
	if obj, ok := raw.(map[string]any); ok {
		raw = obj["tasks"]
	}
	tasks := map[string]map[string]any{}
	for _, item := range anyList(raw) {
		task, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := firstNonEmpty(util.Clean(task["id"]), util.Clean(task["taskId"]))
		owner := util.Clean(task["owner_id"])
		kind := strings.ToLower(util.Clean(task["kind"]))
		if id == "" || owner == "" || (kind != "ppt" && kind != "psd") {
			continue
		}
		status := util.Clean(task["status"])
		if status != TaskStatusQueued && status != TaskStatusRunning && status != TaskStatusSuccess && status != TaskStatusError {
			status = TaskStatusError
		}
		normalized := map[string]any{
			"id":              id,
			"taskId":          id,
			"owner_id":        owner,
			"client_task_id":  util.Clean(task["client_task_id"]),
			"status":          status,
			"kind":            kind,
			"created_at":      firstNonEmpty(util.Clean(task["created_at"]), util.NowLocal()),
			"updated_at":      firstNonEmpty(util.Clean(task["updated_at"]), util.Clean(task["created_at"]), util.NowLocal()),
			"elapsed_seconds": util.ToInt(task["elapsed_seconds"], 0),
		}
		if errText := util.Clean(task["error"]); errText != "" {
			normalized["error"] = errText
		}
		if result := util.StringMap(task["result"]); len(result) > 0 {
			normalized["result"] = result
		}
		if logs := editableTaskLogs(task["logs"]); len(logs) > 0 {
			normalized["logs"] = logs
		}
		tasks[taskKey(owner, id)] = normalized
	}
	return tasks
}

func (s *EditableFileTaskService) saveLocked() error {
	items := make([]map[string]any, 0, len(s.tasks))
	for _, task := range s.tasks {
		items = append(items, task)
	}
	sort.Slice(items, func(i, j int) bool { return util.Clean(items[i]["updated_at"]) > util.Clean(items[j]["updated_at"]) })
	if s.store == nil {
		return fmt.Errorf("storage document backend is required")
	}
	return s.store.SaveJSONDocument(editableFileTasksDocument, map[string]any{"tasks": items})
}

func (s *EditableFileTaskService) recoverUnfinishedLocked() bool {
	changed := false
	for _, task := range s.tasks {
		if task["status"] == TaskStatusQueued || task["status"] == TaskStatusRunning {
			task["status"] = TaskStatusError
			task["error"] = "服务已重启，未完成的任务已中断"
			now := util.NowLocal()
			task["updated_at"] = now
			appendEditableTaskLogLocked(task, now, "服务已重启，未完成的任务已中断")
			changed = true
		}
	}
	return changed
}

func publicEditableFileTask(task map[string]any) map[string]any {
	item := map[string]any{
		"id":              task["id"],
		"taskId":          firstNonEmpty(util.Clean(task["taskId"]), util.Clean(task["id"])),
		"status":          task["status"],
		"kind":            task["kind"],
		"created_at":      task["created_at"],
		"updated_at":      task["updated_at"],
		"elapsed_seconds": util.ToInt(task["elapsed_seconds"], 0),
	}
	if result := util.StringMap(task["result"]); len(result) > 0 {
		item["result"] = result
	}
	if errText := util.Clean(task["error"]); errText != "" {
		item["error"] = errText
	}
	if logs := editableTaskLogs(task["logs"]); len(logs) > 0 {
		item["logs"] = logs
	}
	return item
}

func editableResultMap(result EditableFileRunResult, root string) map[string]any {
	out := map[string]any{}
	if result.ConversationID != "" {
		out["conversation_id"] = result.ConversationID
	}
	if rel := editableRelativePath(root, result.PrimaryPath); rel != "" {
		out["primary_path"] = rel
	}
	if rel := editableRelativePath(root, result.ZipPath); rel != "" {
		out["zip_path"] = rel
	}
	return out
}

func cleanEditableImages(images []string) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		if image = strings.TrimSpace(image); image != "" {
			out = append(out, image)
		}
	}
	return out
}

func editableRelativePath(root, abs string) string {
	root = strings.TrimSpace(root)
	abs = strings.TrimSpace(abs)
	if root == "" || abs == "" {
		return ""
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	pathAbs, err := filepath.Abs(abs)
	if err != nil {
		return ""
	}
	if pathAbs == rootAbs {
		return ""
	}
	if !strings.HasPrefix(pathAbs, rootAbs+string(os.PathSeparator)) {
		return ""
	}
	return filepath.ToSlash(strings.TrimPrefix(pathAbs, rootAbs+string(os.PathSeparator)))
}
