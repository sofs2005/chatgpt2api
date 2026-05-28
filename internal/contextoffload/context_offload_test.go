package contextoffload

import (
	"strings"
	"testing"
)

func tinyOptions() Options {
	return Options{
		InlineMaxChars:        80,
		ForceFileMaxChars:     160,
		LatestUserMaxChars:    60,
		SummaryMaxChars:       40,
		ContextPromptMaxChars: 120,
	}
}

func TestPlanContextKeepsSmallRequestInline(t *testing.T) {
	messages := []map[string]any{{"role": "user", "content": "short request"}}

	plan := PlanContext(messages, nil, nil, tinyOptions())

	if plan.Mode != ModeInline {
		t.Fatalf("Mode = %q, want %q", plan.Mode, ModeInline)
	}
	if len(plan.Files) != 0 {
		t.Fatalf("Files = %#v, want none", plan.Files)
	}
	if got := plan.InlineMessages[0]["content"]; got != "short request" {
		t.Fatalf("inline content = %#v", got)
	}
}

func TestPlanContextMovesLongHistoryToFile(t *testing.T) {
	messages := []map[string]any{
		{"role": "assistant", "content": strings.Repeat("prior ", 30)},
		{"role": "tool", "content": strings.Repeat("tool output\n", 15)},
		{"role": "user", "content": "continue now"},
	}

	plan := PlanContext(messages, nil, nil, tinyOptions())

	if plan.Mode != ModeFile {
		t.Fatalf("Mode = %q, want file", plan.Mode)
	}
	if len(plan.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(plan.Files))
	}
	if !strings.Contains(plan.Files[0].Text, "## Message 1 [assistant]") {
		t.Fatalf("history file missing assistant message: %s", plan.Files[0].Text)
	}
	if !strings.Contains(plan.Files[0].Text, "## Message 3 [user]\ncontinue now") {
		t.Fatalf("history file missing latest user message: %s", plan.Files[0].Text)
	}
	if strings.Contains(plan.Files[0].Text, "## Current User Task") {
		t.Fatalf("history file should not label an executable current task: %s", plan.Files[0].Text)
	}
	if !strings.Contains(plan.InlineMessages[0]["content"].(string), "history.txt") {
		t.Fatalf("inline prompt missing attachment instruction: %s", plan.InlineMessages[0]["content"])
	}
}

func TestPlanContextMovesHugeLatestUserToFile(t *testing.T) {
	latest := strings.Repeat("current task line ", 20)
	messages := []map[string]any{{"role": "user", "content": latest}}

	plan := PlanContext(messages, nil, nil, tinyOptions())

	if len(plan.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(plan.Files))
	}
	inline := plan.InlineMessages[0]["content"].(string)
	if strings.Contains(inline, latest[:80]) {
		t.Fatalf("inline prompt contains oversized latest user text: %s", inline)
	}
	if !strings.Contains(plan.Files[0].Text, strings.TrimSpace(latest)) {
		t.Fatalf("history file missing oversized latest task")
	}
}

func TestPlanContextKeepsCurrentTaskInlineWhenHistoryFileIsGenerated(t *testing.T) {
	messages := []map[string]any{
		{"role": "assistant", "content": strings.Repeat("prior ", 10)},
		{"role": "user", "content": "current task"},
	}
	options := tinyOptions()
	options.ContextPromptMaxChars = 240

	plan := PlanContext(messages, nil, nil, options)

	if plan.Mode != ModeHybrid {
		t.Fatalf("Mode = %q, want hybrid", plan.Mode)
	}
	if len(plan.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(plan.Files))
	}
	if strings.Contains(plan.Files[0].Text, "## Current User Task") {
		t.Fatalf("history file should not label an executable current task: %s", plan.Files[0].Text)
	}
	inline := plan.InlineMessages[0]["content"].(string)
	if !strings.Contains(inline, "Current User Task:\ncurrent task") {
		t.Fatalf("inline prompt missing latest user task: %s", inline)
	}
	if !strings.Contains(inline, "history.txt") {
		t.Fatalf("inline prompt missing history attachment note: %s", inline)
	}
}

func TestPlanContextExtractsCurrentTaskFromUntrustedMetadataWrapper(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "刚刚我问你什么"},
		{"role": "assistant", "content": strings.Repeat("prior answer ", 12)},
		{"role": "user", "content": "System (untrusted): [2026-05-19 08:01:14 GMT+8] 心跳信息。\n\n当前轮问题：请只回答 banana。\n\nConversation info (untrusted metadata):\n```json\n{\"chat_id\":\"wechat:telphy\"}\n```"},
	}
	options := tinyOptions()
	options.ContextPromptMaxChars = 240

	plan := PlanContext(messages, nil, nil, options)

	if plan.LatestUserText != "当前轮问题：请只回答 banana。" {
		t.Fatalf("LatestUserText = %q, want sanitized current task", plan.LatestUserText)
	}
	inline := plan.InlineMessages[0]["content"].(string)
	if strings.Contains(inline, "System (untrusted)") || strings.Contains(inline, "Conversation info") {
		t.Fatalf("inline prompt leaked metadata wrapper: %s", inline)
	}
	if !strings.Contains(inline, "Current User Task:\n当前轮问题：请只回答 banana。") {
		t.Fatalf("inline prompt missing sanitized task: %s", inline)
	}
}

func TestPlanContextSkipsMetadataOnlyLatestUserForCurrentTask(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "刚刚我问你什么"},
		{"role": "assistant", "content": strings.Repeat("prior answer ", 12)},
		{"role": "user", "content": "Conversation info (untrusted metadata):\n```json\n{\"chat_id\":\"wechat:telphy\"}\n```"},
	}
	options := tinyOptions()
	options.ContextPromptMaxChars = 240

	plan := PlanContext(messages, nil, nil, options)

	if plan.LatestUserText != "刚刚我问你什么" {
		t.Fatalf("LatestUserText = %q, want previous real user task", plan.LatestUserText)
	}
	inline := plan.InlineMessages[0]["content"].(string)
	if !strings.Contains(inline, "Current User Task:\n刚刚我问你什么") {
		t.Fatalf("inline prompt missing previous real user task: %s", inline)
	}
}

func TestPlanContextKeepsBridgeToolInstructionsInline(t *testing.T) {
	messages := []map[string]any{{"role": "user", "content": "use a tool"}}
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "Read",
			"description": "Read file contents.",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string"}}, "required": []any{"file_path"}},
		},
	}}
	options := tinyOptions()
	options.ContextPromptMaxChars = 1000

	plan := PlanContext(messages, tools, nil, options)

	inline := plan.InlineMessages[0]["content"].(string)
	if !strings.Contains(inline, "<invoke name=\"bridge-0\"") {
		t.Fatalf("inline prompt missing bridge invoke format: %s", inline)
	}
	if !strings.Contains(inline, "input keys: file_path; required: file_path") {
		t.Fatalf("inline prompt missing compact parameter hint: %s", inline)
	}
	if strings.Contains(inline, "<tool_call><tool_name>") {
		t.Fatalf("inline prompt contains legacy tool call format: %s", inline)
	}
}

func TestPlanContextCreatesToolsFileUnlessChoiceNone(t *testing.T) {
	messages := []map[string]any{{"role": "user", "content": "use a tool"}}
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "Read",
			"description": strings.Repeat("Read file contents. ", 8),
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string"}}},
		},
	}}

	plan := PlanContext(messages, tools, nil, tinyOptions())

	if len(plan.Files) != 1 {
		t.Fatalf("len(Files) = %d, want tools file", len(plan.Files))
	}
	if plan.Files[0].Purpose != "tools" || !strings.Contains(plan.Files[0].Text, "Bridge-call slots available: bridge-0") {
		t.Fatalf("unexpected tools file: %#v", plan.Files[0])
	}
	if strings.Contains(plan.Files[0].Text, "Tool: Read") {
		t.Fatalf("tools file leaked legacy tool block: %#v", plan.Files[0])
	}

	nonePlan := PlanContext(messages, tools, "none", tinyOptions())
	if len(nonePlan.Files) != 0 {
		t.Fatalf("tool_choice none generated files: %#v", nonePlan.Files)
	}
}

func TestFallbackPreservesCurrentTaskWhenPossible(t *testing.T) {
	messages := []map[string]any{
		{"role": "assistant", "content": strings.Repeat("prior ", 40)},
		{"role": "user", "content": "current task"},
	}
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "Read"}}}
	plan := PlanContext(messages, tools, nil, tinyOptions())

	fallback, err := plan.FallbackInlineMessages()
	if err != nil {
		t.Fatalf("FallbackInlineMessages() error = %v", err)
	}
	if got := fallback[len(fallback)-1]["content"]; got != "current task" {
		t.Fatalf("latest fallback content = %#v", got)
	}
	if len(fallback) < 2 || !strings.Contains(fallback[len(fallback)-2]["content"].(string), "桥接工具槽位：bridge-0") {
		t.Fatalf("fallback missing bridge tool rule: %#v", fallback)
	}
	if strings.Contains(fallback[len(fallback)-2]["content"].(string), "可用工具名称") {
		t.Fatalf("fallback leaked legacy tool names: %#v", fallback[len(fallback)-2])
	}
}

func TestFallbackErrorsForHugeCurrentTask(t *testing.T) {
	messages := []map[string]any{{"role": "user", "content": strings.Repeat("huge current ", 20)}}
	plan := PlanContext(messages, nil, nil, tinyOptions())

	_, err := plan.FallbackInlineMessages()
	if err == nil {
		t.Fatalf("FallbackInlineMessages() error = nil, want error")
	}
}
