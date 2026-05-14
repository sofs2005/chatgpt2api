package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const (
	LogTypeEvent = "event"
)

type LogService struct {
	mu    sync.Mutex
	path  string
	store storage.LogBackend
}

type LogQuery struct {
	Username      string
	Module        string
	Method        string
	Summary       string
	Status        string
	IPAddress     string
	OperationType string
	LogLevel      string
	StartDate     string
	EndDate       string
	StartTime     string
	EndTime       string
	Limit         int
}

type LogGovernanceSummary struct {
	Total      int    `json:"total"`
	OldestTime string `json:"oldest_time,omitempty"`
	LatestTime string `json:"latest_time,omitempty"`
}

type LogCleanupResult struct {
	RetentionDays int    `json:"retention_days"`
	CutoffDate    string `json:"cutoff_date"`
	Deleted       int    `json:"deleted"`
	Remaining     int    `json:"remaining"`
}

type userUsageDay struct {
	Calls     int
	Success   int
	Failure   int
	QuotaUsed int
}

type userUsageAccumulator struct {
	Calls     int
	Success   int
	Failure   int
	QuotaUsed int
	Daily     map[string]*userUsageDay
}

func NewLogService(dataDir string, backend ...storage.Backend) *LogService {
	path := filepath.Join(dataDir, filepath.FromSlash(storage.LogEventsDocumentName))
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return &LogService{path: path, store: firstLogStore(backend)}
}

func (s *LogService) Add(summary string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	item := map[string]any{
		"time":    util.NowLocal(),
		"type":    LogTypeEvent,
		"summary": summary,
		"detail":  detail,
	}
	if s.store != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.store.AppendLog(item)
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

func (s *LogService) List(startDate, endDate string, limit int) []map[string]any {
	return s.Search(LogQuery{StartDate: startDate, EndDate: endDate, Limit: limit})
}

func (s *LogService) Search(query LogQuery) []map[string]any {
	limit := normalizedLogLimit(query.Limit)
	startDate, endDate := logQueryDateBounds(query)
	items, ok := s.loadLogItems(startDate, endDate)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, min(limit, len(items)))
	for _, item := range items {
		if !matchLogQuery(item, query) {
			continue
		}
		out = append(out, publicLogItem(item))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *LogService) GovernanceSummary() LogGovernanceSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.governanceSummaryLocked()
}

func (s *LogService) CleanupOlderThan(retentionDays int) (LogCleanupResult, error) {
	if retentionDays < 1 || retentionDays > 3650 {
		return LogCleanupResult{}, errors.New("retention days must be between 1 and 3650")
	}
	cutoffDate := time.Now().AddDate(0, 0, -retentionDays+1).Format("2006-01-02")

	s.mu.Lock()
	defer s.mu.Unlock()

	deleted, err := s.deleteLogsBeforeLocked(cutoffDate)
	if err != nil {
		return LogCleanupResult{}, err
	}
	return LogCleanupResult{
		RetentionDays: retentionDays,
		CutoffDate:    cutoffDate,
		Deleted:       deleted,
		Remaining:     s.governanceSummaryLocked().Total,
	}, nil
}

func (s *LogService) StartRetentionCleaner(ctx context.Context, retentionGetter func() int, interval time.Duration, logger *Logger) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if retentionGetter == nil {
		retentionGetter = func() int { return 7 }
	}
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				result, err := s.CleanupOlderThan(retentionGetter())
				if err != nil {
					if logger != nil {
						logger.Warning("log retention cleanup failed", "error", err)
					}
				} else if result.Deleted > 0 && logger != nil {
					logger.Info("log retention cleanup completed", "deleted", result.Deleted, "remaining", result.Remaining, "retention_days", result.RetentionDays)
				}
				timer.Reset(interval)
			}
		}
	}()
}

func (s *LogService) governanceSummaryLocked() LogGovernanceSummary {
	items, ok := s.loadLogItems("", "")
	summary := LogGovernanceSummary{}
	if !ok {
		return summary
	}
	summary.Total = len(items)
	for _, item := range items {
		logTime := util.Clean(item["time"])
		if logTime == "" {
			continue
		}
		if summary.LatestTime == "" || logTime > summary.LatestTime {
			summary.LatestTime = logTime
		}
		if summary.OldestTime == "" || logTime < summary.OldestTime {
			summary.OldestTime = logTime
		}
	}
	return summary
}

func (s *LogService) deleteLogsBeforeLocked(day string) (int, error) {
	if s.store != nil {
		if maintenance, ok := s.store.(storage.LogMaintenanceBackend); ok {
			return maintenance.DeleteLogsBefore(day)
		}
	}
	return s.deleteFileLogsBeforeLocked(day)
}

func (s *LogService) deleteFileLogsBeforeLocked(day string) (int, error) {
	day = strings.TrimSpace(day)
	if day == "" {
		return 0, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	kept := make([]string, 0, len(lines))
	removed := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var item map[string]any
		if json.Unmarshal([]byte(line), &item) == nil {
			itemDay := logDay(item)
			if itemDay != "" && itemDay < day {
				removed++
				continue
			}
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		return 0, nil
	}
	next := []byte{}
	if len(kept) > 0 {
		next = []byte(strings.Join(kept, "\n") + "\n")
	}
	if err := os.WriteFile(s.path, next, 0o644); err != nil {
		return 0, err
	}
	return removed, nil
}

func (s *LogService) loadLogItems(startDate, endDate string) ([]map[string]any, bool) {
	if s.store != nil {
		items, err := s.store.QueryLogs(startDate, endDate, 0)
		if err == nil {
			return items, true
		}
	}
	file, err := os.Open(s.path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	out := make([]map[string]any, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		var item map[string]any
		if json.Unmarshal([]byte(lines[i]), &item) != nil {
			continue
		}
		if !matchLogDate(item, startDate, endDate) {
			continue
		}
		out = append(out, item)
	}
	return out, true
}

func normalizedLogLimit(limit int) int {
	if limit <= 0 {
		return 200
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func logQueryDateBounds(query LogQuery) (string, string) {
	startDate := strings.TrimSpace(query.StartDate)
	endDate := strings.TrimSpace(query.EndDate)
	if start := normalizeLogTimeFilter(query.StartTime, false); len(start) >= 10 {
		startDate = start[:10]
	}
	if end := normalizeLogTimeFilter(query.EndTime, true); len(end) >= 10 {
		endDate = end[:10]
	}
	return startDate, endDate
}

func matchLogQuery(item map[string]any, query LogQuery) bool {
	if !matchLogDate(item, strings.TrimSpace(query.StartDate), strings.TrimSpace(query.EndDate)) {
		return false
	}
	logTime := util.Clean(item["time"])
	if start := normalizeLogTimeFilter(query.StartTime, false); start != "" && logTime < start {
		return false
	}
	if end := normalizeLogTimeFilter(query.EndTime, true); end != "" && logTime > end {
		return false
	}
	if !containsFold(logActor(item), query.Username) {
		return false
	}
	if !containsFold(logModule(item), query.Module) {
		return false
	}
	if !containsFold(util.Clean(item["summary"]), query.Summary) {
		return false
	}
	if method := strings.TrimSpace(query.Method); method != "" && strings.ToUpper(logDetailString(item, "method")) != strings.ToUpper(method) {
		return false
	}
	if status := strings.TrimSpace(query.Status); status != "" && logStatus(item) != status {
		return false
	}
	if !containsFold(logDetailString(item, "ip_address"), query.IPAddress) {
		return false
	}
	if !containsFold(logOperationType(item), query.OperationType) {
		return false
	}
	if level := strings.TrimSpace(query.LogLevel); level != "" && logLevel(item) != strings.ToLower(level) {
		return false
	}
	return true
}

func matchLogDate(item map[string]any, startDate, endDate string) bool {
	day := logDay(item)
	if strings.TrimSpace(startDate) != "" && day < strings.TrimSpace(startDate) {
		return false
	}
	if strings.TrimSpace(endDate) != "" && day > strings.TrimSpace(endDate) {
		return false
	}
	return true
}

func normalizeLogTimeFilter(value string, endOfDay bool) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "T", " "))
	if value == "" {
		return ""
	}
	if len(value) == len("2006-01-02") {
		if endOfDay {
			return value + " 23:59:59"
		}
		return value + " 00:00:00"
	}
	if len(value) == len("2006-01-02 15:04") {
		if endOfDay {
			return value + ":59"
		}
		return value + ":00"
	}
	if len(value) > len("2006-01-02 15:04:05") {
		return value[:len("2006-01-02 15:04:05")]
	}
	return value
}

func logActor(item map[string]any) string {
	var parts []string
	for _, key := range []string{"username", "key_name", "subject_id", "key_id"} {
		if value := logDetailString(item, key); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

func logModule(item map[string]any) string {
	if value := logDetailString(item, "module"); value != "" {
		return value
	}
	return "系统日志"
}

func logOperationType(item map[string]any) string {
	if value := logDetailString(item, "operation_type"); value != "" {
		return value
	}
	return ""
}

func logLevel(item map[string]any) string {
	if value := logDetailString(item, "log_level"); value != "" {
		return strings.ToLower(value)
	}
	if logOutcome(item) == "failed" {
		return "warning"
	}
	return "info"
}

func logStatus(item map[string]any) string {
	return util.Clean(util.StringMap(item["detail"])["status"])
}

func logOutcome(item map[string]any) string {
	detail := util.StringMap(item["detail"])
	if outcome := util.Clean(detail["outcome"]); outcome != "" {
		return outcome
	}
	status := util.Clean(detail["status"])
	switch status {
	case "success", "failed":
		return status
	}
	code := util.ToInt(detail["status"], 0)
	if code >= 400 {
		return "failed"
	}
	if code > 0 {
		return "success"
	}
	return ""
}

func logDetailString(item map[string]any, key string) string {
	return util.Clean(util.StringMap(item["detail"])[key])
}

func containsFold(value, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(filter))
}

func (s *LogService) UserUsageStats(days int) map[string]map[string]any {
	dates := usageDates(days)
	out := map[string]map[string]any{}
	if len(dates) == 0 {
		return out
	}
	startDate := dates[0]
	endDate := dates[len(dates)-1]
	byUser := map[string]*userUsageAccumulator{}
	if s.store != nil {
		items, err := s.store.QueryLogs(startDate, endDate, 0)
		if err == nil {
			for _, item := range items {
				accumulateUserUsageLog(byUser, item, startDate, endDate)
			}
			for userID, acc := range byUser {
				out[userID] = userUsageStatsMap(acc, dates)
			}
			return out
		}
	}
	file, err := os.Open(s.path)
	if err != nil {
		return out
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var item map[string]any
		if json.Unmarshal([]byte(scanner.Text()), &item) != nil {
			continue
		}
		accumulateUserUsageLog(byUser, item, startDate, endDate)
	}
	for userID, acc := range byUser {
		out[userID] = userUsageStatsMap(acc, dates)
	}
	return out
}

func accumulateUserUsageLog(byUser map[string]*userUsageAccumulator, item map[string]any, startDate, endDate string) {
	if !isUsageLog(item) {
		return
	}
	day := logDay(item)
	if day == "" || day < startDate || day > endDate {
		return
	}
	detail := util.StringMap(item["detail"])
	userID := util.Clean(detail["subject_id"])
	if userID == "" {
		userID = util.Clean(detail["key_id"])
	}
	if userID == "" {
		return
	}
	acc := byUser[userID]
	if acc == nil {
		acc = newUserUsageAccumulator()
		byUser[userID] = acc
	}
	outcome := logOutcome(item)
	quotaUsed := logQuotaUsed(detail, outcome)
	acc.Calls++
	acc.QuotaUsed += quotaUsed
	if outcome == "success" {
		acc.Success++
	} else if outcome == "failed" {
		acc.Failure++
	}
	daily := acc.Daily[day]
	if daily == nil {
		daily = &userUsageDay{}
		acc.Daily[day] = daily
	}
	daily.Calls++
	daily.QuotaUsed += quotaUsed
	if outcome == "success" {
		daily.Success++
	} else if outcome == "failed" {
		daily.Failure++
	}
}

func publicLogItem(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))
	for key, value := range item {
		if key == "type" {
			continue
		}
		out[key] = value
	}
	return out
}

func isUsageLog(item map[string]any) bool {
	detail := util.StringMap(item["detail"])
	if util.Clean(detail["endpoint"]) == "" {
		return false
	}
	return logOutcome(item) != ""
}

func ZeroUserUsageStats(days int) map[string]any {
	return userUsageStatsMap(newUserUsageAccumulator(), usageDates(days))
}

func usageDates(days int) []string {
	if days <= 0 {
		days = 14
	}
	if days > 90 {
		days = 90
	}
	start := time.Now().AddDate(0, 0, -days+1)
	dates := make([]string, 0, days)
	for i := 0; i < days; i++ {
		dates = append(dates, start.AddDate(0, 0, i).Format("2006-01-02"))
	}
	return dates
}

func newUserUsageAccumulator() *userUsageAccumulator {
	return &userUsageAccumulator{Daily: map[string]*userUsageDay{}}
}

func userUsageStatsMap(acc *userUsageAccumulator, dates []string) map[string]any {
	if acc == nil {
		acc = newUserUsageAccumulator()
	}
	curve := make([]map[string]any, 0, len(dates))
	for _, date := range dates {
		day := acc.Daily[date]
		if day == nil {
			day = &userUsageDay{}
		}
		curve = append(curve, map[string]any{
			"date":       date,
			"calls":      day.Calls,
			"success":    day.Success,
			"failure":    day.Failure,
			"quota_used": day.QuotaUsed,
		})
	}
	return map[string]any{
		"call_count":    acc.Calls,
		"success_count": acc.Success,
		"failure_count": acc.Failure,
		"quota_used":    acc.QuotaUsed,
		"usage_curve":   curve,
	}
}

func logDay(item map[string]any) string {
	day := util.Clean(item["time"])
	if len(day) < 10 {
		return ""
	}
	return day[:10]
}

func logQuotaUsed(detail map[string]any, outcome string) int {
	if outcome != "success" {
		return 0
	}
	if urls := util.AsStringSlice(detail["urls"]); len(urls) > 0 {
		return len(urls)
	}
	endpoint := util.Clean(detail["endpoint"])
	switch endpoint {
	case "/v1/images/generations", "/v1/images/edits", "/api/creation-tasks/image-generations", "/api/creation-tasks/image-edits":
		return 1
	default:
		return 0
	}
}

type Logger struct {
	levels func() []string
	logger *slog.Logger
	file   *os.File
}

func NewLogger(dataDir string, levels func() []string) (*Logger, error) {
	path := filepath.Join(dataDir, "logs", "server.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	writer := io.MultiWriter(os.Stdout, file)
	return &Logger{
		levels: levels,
		logger: slog.New(slog.NewJSONHandler(writer, nil)),
		file:   file,
	}, nil
}

func (l *Logger) enabled(level string) bool {
	if l == nil {
		return false
	}
	var levels []string
	if l.levels != nil {
		levels = l.levels()
	}
	if len(levels) == 0 {
		return level == "info" || level == "warning" || level == "error"
	}
	for _, item := range levels {
		if item == level {
			return true
		}
	}
	return false
}

func (l *Logger) Debug(message string, attrs ...any)   { l.print("debug", message, attrs...) }
func (l *Logger) Info(message string, attrs ...any)    { l.print("info", message, attrs...) }
func (l *Logger) Warning(message string, attrs ...any) { l.print("warning", message, attrs...) }
func (l *Logger) Error(message string, attrs ...any)   { l.print("error", message, attrs...) }

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *Logger) print(level string, message string, attrs ...any) {
	if !l.enabled(level) {
		return
	}
	if strings.TrimSpace(message) == "" {
		message = level
	}
	l.logger.Log(context.Background(), slogLevel(level), message, sanitizeSlogAttrs(attrs)...)
}

func slogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func sanitizeSlogAttrs(attrs []any) []any {
	out := make([]any, 0, len(attrs))
	for i := 0; i < len(attrs); i++ {
		key, ok := attrs[i].(string)
		if ok && i+1 < len(attrs) {
			out = append(out, key, sanitizeLogField(key, attrs[i+1]))
			i++
			continue
		}
		out = append(out, SanitizeLogValue(attrs[i]))
	}
	return out
}

func SanitizeLogValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, value := range x {
			out[key] = sanitizeLogField(key, value)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = SanitizeLogValue(item)
		}
		return out
	case []map[string]any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = SanitizeLogValue(item)
		}
		return out
	case error:
		return x.Error()
	case string:
		if strings.HasPrefix(strings.TrimSpace(x), "data:") && strings.Contains(x, ";base64,") {
			return maskBase64(x)
		}
		return x
	default:
		return v
	}
}

func sanitizeLogField(key string, value any) any {
	if s, ok := value.(string); ok && sensitiveLogKey(key) {
		return maskString(s, 10)
	}
	if s, ok := value.(string); ok && base64LogKey(key) {
		return maskBase64(s)
	}
	return SanitizeLogValue(value)
}

func sensitiveLogKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	switch lower {
	case "authorization", "password", "secret", "token", "access_token", "accesstoken", "refresh_token", "refreshtoken", "session_token", "sessiontoken", "session_json", "sessionjson", "api_key", "key", "dx":
		return true
	default:
		return strings.Contains(lower, "password") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "authorization") ||
			strings.HasSuffix(lower, "_token") ||
			strings.HasPrefix(lower, "token_")
	}
}

func base64LogKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	return lower == "b64_json" || strings.Contains(lower, "base64")
}

func maskString(value string, keep int) string {
	if len(value) <= keep {
		return value
	}
	return value[:keep] + "..."
}

func maskBase64(value string) string {
	if idx := strings.Index(value, ","); strings.HasPrefix(value, "data:") && idx >= 0 {
		return value[:idx+1] + maskString(value[idx+1:], 24)
	}
	return maskString(value, 24)
}
