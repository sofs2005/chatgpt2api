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
	if !strings.Contains(plan.Files[0].Text, "## Current User Task\ncontinue now") {
		t.Fatalf("history file missing current task: %s", plan.Files[0].Text)
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
	if plan.Files[0].Purpose != "tools" || !strings.Contains(plan.Files[0].Text, "Tool: Read") {
		t.Fatalf("unexpected tools file: %#v", plan.Files[0])
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
	if len(fallback) < 2 || !strings.Contains(fallback[len(fallback)-2]["content"].(string), "Read") {
		t.Fatalf("fallback missing tool rule: %#v", fallback)
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
