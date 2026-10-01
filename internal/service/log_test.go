package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"chatgpt2api/internal/util"
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
	if err := logs.Add("GET /api/profile", map[string]any{
		"username":       "admin",
		"module":         "profile",
		"method":         "GET",
		"path":           "/api/profile",
		"status":         200,
		"operation_type": "查询",
		"log_level":      "info",
	}); err != nil {
		t.Fatalf("Add(noisy get audit event) error = %v", err)
	}
	if err := logs.Add("POST /api/settings", map[string]any{
		"username":       "admin",
		"module":         "settings",
		"method":         "POST",
		"path":           "/api/settings",
		"status":         200,
		"operation_type": "提交",
		"log_level":      "info",
	}); err != nil {
		t.Fatalf("Add(write audit event) error = %v", err)
	}

	all := logs.Search(LogQuery{Limit: 10})
	if len(all) != 5 {
		t.Fatalf("Search(all) length = %d, want 5: %#v", len(all), all)
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

	meaningful := logs.Search(LogQuery{View: LogViewMeaningful, Limit: 10})
	if summaries := logSummaries(meaningful); !reflect.DeepEqual(summaries, []string{"POST /api/settings", "GET /api/settings", "文生图调用完成", "新增账号"}) {
		t.Fatalf("Search(meaningful) summaries = %#v", summaries)
	}
	business := logs.Search(LogQuery{View: LogViewBusiness, Limit: 10})
	if summaries := logSummaries(business); !reflect.DeepEqual(summaries, []string{"文生图调用完成", "新增账号"}) {
		t.Fatalf("Search(business) summaries = %#v", summaries)
	}

	usage := logs.UserUsageStats(1)["alice-key"]
	if usage == nil || usage["call_count"] != 1 || usage["success_count"] != 1 || usage["quota_used"] != 1 {
		t.Fatalf("UserUsageStats(new call log shape) = %#v", usage)
	}
}

func logSummaries(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, util.Clean(item["summary"]))
	}
	return out
}

func TestSanitizeLogValueMasksSessionCredentials(t *testing.T) {
	accessToken := "access-token-secret"
	sessionToken := "session-token-secret"
	sanitized := SanitizeLogValue(map[string]any{
		"session_json": `{"accessToken":"` + accessToken + `","sessionToken":"` + sessionToken + `"}`,
		"accessToken":  accessToken,
		"sessionToken": sessionToken,
	})

	item, ok := sanitized.(map[string]any)
	if !ok {
		t.Fatalf("SanitizeLogValue() = %#v", sanitized)
	}
	text := item["session_json"].(string) + item["accessToken"].(string) + item["sessionToken"].(string)
	if strings.Contains(text, accessToken) || strings.Contains(text, sessionToken) {
		t.Fatalf("sanitized log value leaked credentials: %#v", sanitized)
	}
}

func TestNormalizeDiagnosticDetailRedactsSignedURLs(t *testing.T) {
	signed := "https://files.oaiusercontent.com/download/abc?sig=SECRET_SIGNATURE&token=topsecret"
	detail := NormalizeDiagnosticDetail(map[string]any{
		"download_url":  signed,
		"authorization": "Bearer sk-secret-token",
	}, DiagnosticFields{
		EventKind:     EventKindUpstream,
		Stage:         "image_download",
		Severity:      "error",
		Outcome:       "failed",
		UpstreamHost:  "files.oaiusercontent.com",
		UpstreamPath:  "/download/abc",
		ErrorCause:    "tls: handshake failure",
		UpstreamStage: "image_download",
	})

	if detail["download_url"] != "https://files.oaiusercontent.com/download/abc" {
		t.Fatalf("download_url = %#v, want query stripped", detail["download_url"])
	}
	if detail["event_kind"] != EventKindUpstream || detail["stage"] != "image_download" || detail["upstream_stage"] != "image_download" {
		t.Fatalf("diagnostic fields = %#v", detail)
	}
	if detail["outcome"] != "failed" || detail["log_level"] != "error" || detail["error_cause"] != "tls: handshake failure" {
		t.Fatalf("diagnostic outcome fields = %#v", detail)
	}
	serialized := fmt.Sprintf("%#v", detail)
	for _, secret := range []string{"SECRET_SIGNATURE", "topsecret", "sk-secret-token"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("sanitized detail leaked %q: %s", secret, serialized)
		}
	}
}

func TestUpstreamCauseTextUnwrapsInnermostCause(t *testing.T) {
	cause := errors.New("read tcp 10.0.0.1: connection reset by peer")
	err := fmt.Errorf("image_download failed: %w", cause)
	if got := UpstreamCauseText(err); got != cause.Error() {
		t.Fatalf("UpstreamCauseText() = %q, want %q", got, cause.Error())
	}
	// 摘要与 cause 相同时不应重复写入日志。
	if got := UpstreamCauseText(cause); got != "" {
		t.Fatalf("UpstreamCauseText(single) = %q, want empty", got)
	}
	if got := UpstreamCauseText(nil); got != "" {
		t.Fatalf("UpstreamCauseText(nil) = %q, want empty", got)
	}
}

func TestMeaningfulViewExcludesSuccessfulReadOnlyAudit(t *testing.T) {
	readOnlyAudit := map[string]any{"summary": "GET /api/logs", "detail": map[string]any{"method": "GET", "path": "/api/logs", "status": 200, "event_kind": EventKindAudit}}
	if isMeaningfulLogItem(readOnlyAudit) {
		t.Fatalf("successful read-only audit should be filtered out of meaningful view")
	}
	failedRead := map[string]any{"summary": "GET /api/logs", "detail": map[string]any{"method": "GET", "path": "/api/logs", "status": 500, "event_kind": EventKindAudit, "outcome": "failed"}}
	if !isMeaningfulLogItem(failedRead) {
		t.Fatalf("failed read-only audit should stay in meaningful view")
	}
	business := map[string]any{"summary": "文生图调用失败", "detail": map[string]any{"method": "POST", "path": "/v1/images/generations", "status": 502, "event_kind": EventKindBusiness}}
	if !isMeaningfulLogItem(business) {
		t.Fatalf("business events should stay in meaningful view")
	}
}

func TestLogServiceSearchFiltersByStageAndEventKind(t *testing.T) {
	logs := NewLogService(newTestStorageBackend(t))
	if err := logs.Add("文生图调用失败", map[string]any{
		"method":     "POST",
		"path":       "/v1/images/generations",
		"status":     502,
		"event_kind": EventKindUpstream,
		"stage":      "image_download",
	}); err != nil {
		t.Fatalf("Add(image failure) error = %v", err)
	}
	if err := logs.Add("文生图调用完成", map[string]any{
		"method":     "POST",
		"path":       "/v1/images/generations",
		"status":     200,
		"event_kind": EventKindBusiness,
		"stage":      "image_prepare",
	}); err != nil {
		t.Fatalf("Add(image success) error = %v", err)
	}

	if items := logs.Search(LogQuery{Stage: "image_download", View: LogViewAll}); len(items) != 1 {
		t.Fatalf("Search(stage=image_download) = %d items, want 1", len(items))
	}
	if items := logs.Search(LogQuery{EventKind: EventKindUpstream, View: LogViewAll}); len(items) != 1 {
		t.Fatalf("Search(event_kind=upstream) = %d items, want 1", len(items))
	}
	if items := logs.Search(LogQuery{Stage: "IMAGE_DOWNLOAD", View: LogViewAll}); len(items) != 1 {
		t.Fatalf("stage filter should be case-insensitive, got %d items", len(items))
	}
	if items := logs.Search(LogQuery{Stage: "bootstrap", View: LogViewAll}); len(items) != 0 {
		t.Fatalf("Search(stage=bootstrap) = %d items, want 0", len(items))
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

func TestLogServiceRetentionCleanerRunsImmediately(t *testing.T) {
	logs := NewLogService(newTestStorageBackend(t))
	for _, item := range []map[string]any{
		{"time": "2000-01-01 00:00:00", "type": "event", "summary": "旧调用", "detail": map[string]any{"status": "success"}},
		{"time": time.Now().Format("2006-01-02 15:04:05"), "type": "event", "summary": "新日志", "detail": map[string]any{"status": 200}},
	} {
		if err := logs.store.AppendLog(item); err != nil {
			t.Fatalf("AppendLog() error = %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs.StartRetentionCleaner(ctx, func() int { return 1 }, time.Hour, nil)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		items := logs.Search(LogQuery{Limit: 10})
		if len(items) == 1 && items[0]["summary"] == "新日志" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("retention cleaner did not remove old logs, remaining = %#v", logs.Search(LogQuery{Limit: 10}))
}

// 成功请求只走 debug，而 debug 默认不开启，此前会从日志文件里整体消失。
// Request 必须让文件拿到全量记录，无论级别配置如何。
func TestLoggerRequestAlwaysRecordsAccessToFile(t *testing.T) {
	dataDir := t.TempDir()
	logger, err := NewLogger(dataDir, func() []string { return []string{"info", "warning", "error"} })
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	logger.Request("debug", "http request", "path", "/api/profile", "status", 200)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "logs", "server.log"))
	if err != nil {
		t.Fatalf("ReadFile(server.log) error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "/api/profile") {
		t.Fatalf("successful request missing from server.log: %q", text)
	}
	if !strings.Contains(text, `"level":"DEBUG"`) {
		t.Fatalf("debug request should keep its level: %q", text)
	}
}

// stdout 只应拿到非 debug 请求：成功请求高频，进容器日志会淹没异常。
func TestLoggerRequestKeepsStdoutQuietForDebug(t *testing.T) {
	dataDir := t.TempDir()
	logger, err := NewLogger(dataDir, func() []string { return []string{"info", "warning", "error"} })
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	// 捕获 stdout，确认 debug 请求只落文件、不进 stdout。
	orig := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = writer
	logger.stdout = slog.New(slog.NewJSONHandler(writer, nil))

	logger.Request("debug", "http request", "path", "/api/ok", "status", 200)
	logger.Request("error", "http request", "path", "/api/boom", "status", 500)
	_ = writer.Close()
	os.Stdout = orig

	captured, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(stdout) error = %v", err)
	}
	text := string(captured)
	if strings.Contains(text, "/api/ok") {
		t.Fatalf("debug request leaked into stdout: %s", text)
	}
	if !strings.Contains(text, "/api/boom") {
		t.Fatalf("error request missing from stdout: %s", text)
	}
}

// captureStdout 在回调期间接管 stdout 与 logger.stdout，返回捕获到的文本。
func captureStdout(t *testing.T, logger *Logger, run func()) string {
	t.Helper()
	orig := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = writer
	logger.stdout = slog.New(slog.NewJSONHandler(writer, nil))
	run()
	_ = writer.Close()
	os.Stdout = orig

	captured, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(stdout) error = %v", err)
	}
	return string(captured)
}

// 挑战解开必须能从 stdout 直接看见，不能只躺在 server.log 里。
//
// 用户要看的是「CF 挑战到底有没有用」：兜底成功是唯一能回答它的信号，
// 而成功路径此前一律压 debug，容器日志里根本看不到求解跑过。
func TestLoggerUpstreamLogsSolvedClearanceToStdout(t *testing.T) {
	dataDir := t.TempDir()
	logger, err := NewLogger(dataDir, func() []string { return []string{"info", "warning", "error"} })
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	text := captureStdout(t, logger, func() {
		logger.Upstream("text", map[string]any{
			"stage": "clearance", "ok": true, "status": 200, "challenge_passed": true,
		})
	})
	if !strings.Contains(text, "clearance solved") {
		t.Fatalf("solved clearance missing from stdout: %s", text)
	}
	if !strings.Contains(text, `"route":"text"`) {
		t.Fatalf("route missing from solved clearance record: %s", text)
	}
}

// 兜底压根没跑时不该刷 stdout：没撞上挑战的正常请求占绝大多数，
// 把它们放进容器日志会把真正需要看的求解失败淹掉。
func TestLoggerUpstreamKeepsSkippedClearanceOutOfStdout(t *testing.T) {
	dataDir := t.TempDir()
	logger, err := NewLogger(dataDir, func() []string { return []string{"info", "warning", "error"} })
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	text := captureStdout(t, logger, func() {
		logger.Upstream("text", map[string]any{
			"stage": "clearance", "ok": false, "skipped": "clearance disabled",
		})
	})
	if strings.Contains(text, "clearance") {
		t.Fatalf("skipped clearance leaked into stdout: %s", text)
	}
}

// 求解报错是 warning，文件与 stdout 都要有。
func TestLoggerUpstreamLogsFailedClearanceToStdout(t *testing.T) {
	dataDir := t.TempDir()
	logger, err := NewLogger(dataDir, func() []string { return []string{"info", "warning", "error"} })
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	text := captureStdout(t, logger, func() {
		logger.Upstream("official_image", map[string]any{
			"stage": "clearance", "ok": false, "error": "flaresolverr unreachable",
		})
	})
	if !strings.Contains(text, "clearance failed") {
		t.Fatalf("failed clearance missing from stdout: %s", text)
	}
	if !strings.Contains(text, "flaresolverr unreachable") {
		t.Fatalf("clearance failure reason missing from stdout: %s", text)
	}
}

// 非 clearance 阶段的失败同样必须进 stdout。
//
// 这条是回归测试：此前 skipped 用 fmt.Sprint(attrs["skipped"]) 取值，属性缺失时
// 拿到的是 "<nil>" 而不是空串，于是 `skipped != ""` 恒为真，所有上游阶段失败都被
// 当成「被跳过」压进 debug——"upstream stage failed" 那条 warning 从来没出现过。
func TestLoggerUpstreamLogsGenericStageFailureToStdout(t *testing.T) {
	dataDir := t.TempDir()
	logger, err := NewLogger(dataDir, func() []string { return []string{"info", "warning", "error"} })
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	text := captureStdout(t, logger, func() {
		logger.Upstream("official_image", map[string]any{
			"stage": "bootstrap", "ok": false, "error": "upstream returned 403",
		})
	})
	if !strings.Contains(text, "upstream stage failed") {
		t.Fatalf("generic stage failure missing from stdout: %s", text)
	}
	if !strings.Contains(text, `"stage":"bootstrap"`) {
		t.Fatalf("stage missing from the failure record: %s", text)
	}
}
