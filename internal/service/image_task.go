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
	TaskStatusQueued    = "queued"
	TaskStatusRunning   = "running"
	TaskStatusSuccess   = "success"
	TaskStatusError     = "error"
	TaskStatusCancelled = "cancelled"
)

type ImageTaskHandler func(context.Context, Identity, map[string]any) (map[string]any, error)

type ImageTaskService struct {
	mu                  sync.RWMutex
	path                string
	generation          ImageTaskHandler
	edit                ImageTaskHandler
	retentionGetter     func() int
	concurrentLimit     func() int
	userConcurrentLimit func() int
	userRPMLimit        func() int
	runningImages       int
	tasks               map[string]map[string]any
	cancels             map[string]context.CancelFunc
	ownerSubmitTimes    map[string][]time.Time
}

type ImageTaskLimitError struct {
	Message string
}

func (e ImageTaskLimitError) Error() string {
	return e.Message
}

func NewImageTaskService(path string, generation ImageTaskHandler, edit ImageTaskHandler, retentionGetter func() int, limitGetters ...func() int) *ImageTaskService {
	s := &ImageTaskService{path: path, generation: generation, edit: edit, retentionGetter: retentionGetter, tasks: map[string]map[string]any{}, cancels: map[string]context.CancelFunc{}, ownerSubmitTimes: map[string][]time.Time{}}
	if len(limitGetters) > 0 {
		s.concurrentLimit = limitGetters[0]
	}
	if len(limitGetters) > 1 {
		s.userConcurrentLimit = limitGetters[1]
	}
	if len(limitGetters) > 2 {
		s.userRPMLimit = limitGetters[2]
	}
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

func (s *ImageTaskService) SubmitGeneration(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, n int, messages any) (map[string]any, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	payload := map[string]any{"prompt": prompt, "model": model, "n": normalizedImageTaskCount(n), "size": size, "quality": quality, "response_format": "url", "base_url": baseURL}
	if messages != nil {
		payload["messages"] = messages
	}
	return s.submit(ctx, identity, clientTaskID, "generate", payload)
}

func (s *ImageTaskService) SubmitEdit(ctx context.Context, identity Identity, clientTaskID, prompt, model, size, quality, baseURL string, images any, n int, messages any) (map[string]any, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	payload := map[string]any{"prompt": prompt, "images": images, "model": model, "n": normalizedImageTaskCount(n), "size": size, "quality": quality, "response_format": "url", "base_url": baseURL}
	if messages != nil {
		payload["messages"] = messages
	}
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
	items := make([]map[string]any, 0)
	missing := make([]string, 0)
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
	return map[string]any{"items": items, "missing_ids": missing}
}

func (s *ImageTaskService) CancelTask(identity Identity, clientTaskID string) (map[string]any, error) {
	taskID := strings.TrimSpace(clientTaskID)
	if taskID == "" {
		return nil, fmt.Errorf("client_task_id is required")
	}
	key := taskKey(ownerID(identity), taskID)
	now := util.NowLocal()
	var cancel context.CancelFunc
	s.mu.Lock()
	task := s.tasks[key]
	if task == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("image task not found")
	}
	if isActiveTaskStatus(util.Clean(task["status"])) {
		task["status"] = TaskStatusCancelled
		task["error"] = "任务已终止"
		if task["data"] == nil {
			task["data"] = []any{}
		}
		task["updated_at"] = now
		cancel = s.cancels[key]
		delete(s.cancels, key)
		_ = s.saveLocked()
	}
	result := publicTask(task)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return result, nil
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
	count := imageTaskCount(payload)
	if err := s.checkUserImageLimitsLocked(identity, owner, count, time.Now()); err != nil {
		if cleaned {
			_ = s.saveLocked()
		}
		s.mu.Unlock()
		return nil, err
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	task := map[string]any{"id": taskID, "owner_id": owner, "status": TaskStatusQueued, "mode": mode, "model": firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto), "size": util.Clean(payload["size"]), "quality": util.Clean(payload["quality"]), "count": count, "created_at": now, "updated_at": now}
	s.tasks[key] = task
	s.cancels[key] = cancel
	_ = s.saveLocked()
	result := publicTask(task)
	s.mu.Unlock()
	go s.runTask(taskCtx, key, mode, identity, payload)
	return result, nil
}

func (s *ImageTaskService) runTask(ctx context.Context, key, mode string, identity Identity, payload map[string]any) {
	defer s.removeTaskCancel(key)
	slots, ok := s.acquireImageSlots(ctx, imageTaskCount(payload))
	if !ok {
		s.updateActiveTask(key, map[string]any{"status": TaskStatusCancelled, "error": "任务已终止", "data": []any{}})
		return
	}
	defer s.releaseImageSlots(slots)
	if !s.updateActiveTask(key, map[string]any{"status": TaskStatusRunning, "error": ""}) {
		return
	}
	handler := s.generation
	if mode == "edit" {
		handler = s.edit
	}
	result, err := handler(ctx, identity, payload)
	if err != nil {
		status := TaskStatusError
		message := err.Error()
		if ctx.Err() != nil {
			status = TaskStatusCancelled
			message = "任务已终止"
		}
		updates := map[string]any{"status": status, "error": message, "data": taskResultData(result)}
		if outputType := util.Clean(result["output_type"]); outputType != "" {
			updates["output_type"] = outputType
		}
		s.updateActiveTask(key, updates)
		return
	}
	data := util.AsMapSlice(result["data"])
	if len(data) == 0 {
		message := firstNonEmpty(util.Clean(result["message"]), "image task returned no image data")
		updates := map[string]any{"status": TaskStatusError, "error": message, "data": []any{}}
		if outputType := util.Clean(result["output_type"]); outputType != "" {
			updates["output_type"] = outputType
		}
		s.updateActiveTask(key, updates)
		return
	}
	s.updateActiveTask(key, map[string]any{"status": TaskStatusSuccess, "data": data, "error": ""})
}

func (s *ImageTaskService) acquireImageSlots(ctx context.Context, requested int) (int, bool) {
	if requested < 1 {
		requested = 1
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		limit := s.imageConcurrentLimit()
		slots := requested
		if slots > limit {
			slots = limit
		}
		s.mu.Lock()
		if s.runningImages+slots <= limit {
			s.runningImages += slots
			s.mu.Unlock()
			return slots, true
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, false
		case <-ticker.C:
		}
	}
}

func (s *ImageTaskService) releaseImageSlots(slots int) {
	if slots < 1 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runningImages -= slots
	if s.runningImages < 0 {
		s.runningImages = 0
	}
}

func (s *ImageTaskService) imageConcurrentLimit() int {
	limit := 4
	if s.concurrentLimit != nil {
		limit = s.concurrentLimit()
	}
	if limit < 1 {
		return 1
	}
	return limit
}

func (s *ImageTaskService) checkUserImageLimitsLocked(identity Identity, owner string, requested int, now time.Time) error {
	if identity.Role != AuthRoleUser {
		return nil
	}
	if requested < 1 {
		requested = 1
	}
	if limit := s.userImageConcurrentLimit(); limit > 0 && s.activeOwnerImageCountLocked(owner)+requested > limit {
		return ImageTaskLimitError{Message: fmt.Sprintf("用户并发限制已达到（最多 %d 张处理中）", limit)}
	}
	if limit := s.userImageRPMLimit(); limit > 0 {
		cutoff := now.Add(-time.Minute)
		times := s.ownerSubmitTimes[owner]
		kept := times[:0]
		for _, item := range times {
			if item.After(cutoff) {
				kept = append(kept, item)
			}
		}
		if len(kept) >= limit {
			s.ownerSubmitTimes[owner] = kept
			return ImageTaskLimitError{Message: fmt.Sprintf("用户 RPM 速率限制已达到（每分钟最多 %d 次）", limit)}
		}
		s.ownerSubmitTimes[owner] = append(kept, now)
	}
	return nil
}

func (s *ImageTaskService) activeOwnerImageCountLocked(owner string) int {
	count := 0
	for _, task := range s.tasks {
		if task["owner_id"] != owner || !isActiveTaskStatus(util.Clean(task["status"])) {
			continue
		}
		count += normalizedImageTaskCount(util.ToInt(task["count"], 1))
	}
	return count
}

func (s *ImageTaskService) userImageConcurrentLimit() int {
	if s.userConcurrentLimit == nil {
		return 0
	}
	limit := s.userConcurrentLimit()
	if limit < 1 {
		return 0
	}
	return limit
}

func (s *ImageTaskService) userImageRPMLimit() int {
	if s.userRPMLimit == nil {
		return 0
	}
	limit := s.userRPMLimit()
	if limit < 1 {
		return 0
	}
	return limit
}

func (s *ImageTaskService) updateActiveTask(key string, updates map[string]any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[key]
	if task == nil {
		return false
	}
	if !isActiveTaskStatus(util.Clean(task["status"])) {
		return false
	}
	for k, v := range updates {
		task[k] = v
	}
	task["updated_at"] = util.NowLocal()
	_ = s.saveLocked()
	return true
}

func (s *ImageTaskService) removeTaskCancel(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, key)
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
		if status != TaskStatusQueued && status != TaskStatusRunning && status != TaskStatusSuccess && status != TaskStatusError && status != TaskStatusCancelled {
			status = TaskStatusError
		}
		mode := "generate"
		if task["mode"] == "edit" {
			mode = "edit"
		}
		count := normalizedImageTaskCount(util.ToInt(task["count"], 1))
		normalized := map[string]any{"id": id, "owner_id": owner, "status": status, "mode": mode, "model": firstNonEmpty(util.Clean(task["model"]), util.ImageModelAuto), "size": util.Clean(task["size"]), "quality": util.Clean(task["quality"]), "count": count, "created_at": firstNonEmpty(util.Clean(task["created_at"]), util.NowLocal()), "updated_at": firstNonEmpty(util.Clean(task["updated_at"]), util.Clean(task["created_at"]), util.NowLocal())}
		if data := util.AsMapSlice(task["data"]); data != nil {
			normalized["data"] = data
		}
		if errText := util.Clean(task["error"]); errText != "" {
			normalized["error"] = errText
		}
		if outputType := util.Clean(task["output_type"]); outputType != "" {
			normalized["output_type"] = outputType
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
		if status != TaskStatusSuccess && status != TaskStatusError && status != TaskStatusCancelled {
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
	if quality := util.Clean(task["quality"]); quality != "" {
		item["quality"] = quality
	}
	if task["data"] != nil {
		item["data"] = task["data"]
	}
	if util.Clean(task["error"]) != "" {
		item["error"] = task["error"]
	}
	if util.Clean(task["output_type"]) != "" {
		item["output_type"] = task["output_type"]
	}
	return item
}

func ownerID(identity Identity) string {
	if owner := util.Clean(identity.OwnerID); owner != "" {
		return owner
	}
	if id := util.Clean(identity.ID); id != "" {
		return id
	}
	return "anonymous"
}

func taskKey(owner, id string) string {
	return owner + ":" + id
}

func normalizedImageTaskCount(n int) int {
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}

func imageTaskCount(payload map[string]any) int {
	return normalizedImageTaskCount(util.ToInt(payload["n"], 1))
}

func taskResultData(result map[string]any) []map[string]any {
	if result == nil {
		return []map[string]any{}
	}
	data := util.AsMapSlice(result["data"])
	if data == nil {
		return []map[string]any{}
	}
	return data
}

func isActiveTaskStatus(status string) bool {
	return status == TaskStatusQueued || status == TaskStatusRunning
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
