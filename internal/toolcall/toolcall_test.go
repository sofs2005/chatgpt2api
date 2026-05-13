package toolcall

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseXMLToolCallsWithCDATAAndNumbers(t *testing.T) {
	text := "先处理\n<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path><![CDATA[internal/app.go]]></path><limit>5</limit></parameters></tool_call></tool_calls>"

	calls, visible, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if visible != "先处理" {
		t.Fatalf("visible = %q, want 先处理", visible)
	}
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1 (%#v)", len(calls), calls)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("calls[0].Name = %q, want read_file", calls[0].Name)
	}
	if got := calls[0].Input["path"]; got != "internal/app.go" {
		t.Fatalf("path = %#v, want internal/app.go", got)
	}
	if got := calls[0].Input["limit"]; got != float64(5) {
		t.Fatalf("limit = %#v, want float64(5)", got)
	}
}

func TestParseDirectSingularToolCall(t *testing.T) {
	text := "prefix\n<tool_call><tool_name>read_file</tool_name><parameters><path>a.go</path></parameters></tool_call>"

	calls, visible, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if visible != "prefix" {
		t.Fatalf("visible = %q, want prefix", visible)
	}
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1 (%#v)", len(calls), calls)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("calls[0].Name = %q, want read_file", calls[0].Name)
	}
	if got := calls[0].Input["path"]; got != "a.go" {
		t.Fatalf("path = %#v, want a.go", got)
	}
}

func TestParseIgnoresFencedXML(t *testing.T) {
	text := "```xml\n<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path>a.go</path></parameters></tool_call></tool_calls>\n```"

	calls, visible, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("len(calls) = %d, want 0 (%#v)", len(calls), calls)
	}
	if visible != text {
		t.Fatalf("visible = %q, want original fenced text", visible)
	}
}

func TestParseAndStripMarkupPreserveIdenticalFencedXML(t *testing.T) {
	markup := "<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path>a.go</path></parameters></tool_call></tool_calls>"
	text := "示例：\n```xml\n" + markup + "\n```\n实际调用：\n" + markup + "\n结束"

	calls, visible, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1 (%#v)", len(calls), calls)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("calls[0].Name = %q, want read_file", calls[0].Name)
	}
	wantVisible := "示例：\n```xml\n" + markup + "\n```\n实际调用：\n\n结束"
	if visible != wantVisible {
		t.Fatalf("visible = %q, want %q", visible, wantVisible)
	}

	stripped := StripMarkup(text)
	if stripped != wantVisible {
		t.Fatalf("StripMarkup() = %q, want %q", stripped, wantVisible)
	}
}

func TestParseRepeatedFieldsAsArray(t *testing.T) {
	text := "<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path>a.go</path><path>b.go</path></parameters></tool_call></tool_calls>"

	calls, visible, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if visible != "" {
		t.Fatalf("visible = %q, want empty", visible)
	}
	want := []any{"a.go", "b.go"}
	if !reflect.DeepEqual(calls[0].Input["path"], want) {
		t.Fatalf("path = %#v, want %#v", calls[0].Input["path"], want)
	}
}

func TestParseFunctionCallWithJSONObjectParams(t *testing.T) {
	text := `<function_call><name>read_file</name><arguments>{"path":"a.go","limit":2}</arguments></function_call>`

	calls, visible, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if visible != "" {
		t.Fatalf("visible = %q, want empty", visible)
	}
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	if got := calls[0].Input["path"]; got != "a.go" {
		t.Fatalf("path = %#v, want a.go", got)
	}
	if got := calls[0].Input["limit"]; got != float64(2) {
		t.Fatalf("limit = %#v, want float64(2)", got)
	}
}

func TestParseInvokeWithXMLParams(t *testing.T) {
	text := `<invoke name="read_file"><params><path>a.go</path><limit>3</limit></params></invoke>`

	calls, _, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("calls = %#v", calls)
	}
	if got := calls[0].Input["limit"]; got != float64(3) {
		t.Fatalf("limit = %#v, want float64(3)", got)
	}
}

func TestRequiredPolicyErrorsWhenNoToolCall(t *testing.T) {
	_, _, err := Parse("plain answer", []string{"read_file"}, ChoicePolicy{Mode: ChoiceRequired})
	if err == nil || err.Error() != "tool_choice required but no valid tool call was produced" {
		t.Fatalf("err = %v", err)
	}
}

func TestForcedPolicyRejectsUnknownTool(t *testing.T) {
	text := "<tool_calls><tool_call><tool_name>write_file</tool_name><parameters><path>a.go</path></parameters></tool_call></tool_calls>"

	_, _, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceForced, Name: "read_file"})
	if err == nil || err.Error() != "tool_choice forced read_file but model produced write_file" {
		t.Fatalf("err = %v", err)
	}
}

func TestForcedPolicyRejectsExtraToolCall(t *testing.T) {
	text := "<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path>a.go</path></parameters></tool_call><tool_call><tool_name>search</tool_name><parameters><query>go</query></parameters></tool_call></tool_calls>"

	_, _, err := Parse(text, []string{"read_file", "search"}, ChoicePolicy{Mode: ChoiceForced, Name: "read_file"})
	if err == nil || err.Error() != "tool_choice forced read_file but model produced search" {
		t.Fatalf("err = %v", err)
	}
}

func TestParseScansPastUnknownToolMarkup(t *testing.T) {
	text := "before <tool_call><tool_name>unknown</tool_name><parameters>{}</parameters></tool_call> middle <tool_call><tool_name>read_file</tool_name><parameters><path>a.go</path></parameters></tool_call> after"

	calls, visible, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if visible != "before  middle  after" {
		t.Fatalf("visible = %q, want stripped text", visible)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("calls = %#v, want one read_file call", calls)
	}
	if got := calls[0].Input["path"]; got != "a.go" {
		t.Fatalf("path = %#v, want a.go", got)
	}
}

func TestParseScansPastMalformedToolMarkup(t *testing.T) {
	text := "bad <tool_call><tool_name>broken</tool_name> ok <tool_call><tool_name>read_file</tool_name><parameters><path>a.go</path></parameters></tool_call>"

	calls, _, err := Parse(text, []string{"read_file"}, ChoicePolicy{Mode: ChoiceAuto})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("calls = %#v, want one read_file call", calls)
	}
}

func TestStreamableTextStopsBeforeToolMarkup(t *testing.T) {
	text := "prefix\n<tool_calls><tool_call>"
	if got := StreamableText(text); got != "prefix" {
		t.Fatalf("StreamableText() = %q, want prefix", got)
	}
}

func TestPolicyFromToolChoice(t *testing.T) {
	tests := []struct {
		name   string
		choice any
		want   ChoicePolicy
	}{
		{
			name:   "none string",
			choice: "none",
			want:   ChoicePolicy{Mode: ChoiceNone},
		},
		{
			name: "openai forced function",
			choice: map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": "read_file",
				},
			},
			want: ChoicePolicy{Mode: ChoiceForced, Name: "read_file"},
		},
		{
			name: "uppercase openai forced function",
			choice: map[string]any{
				"type": "FUNCTION",
				"function": map[string]any{
					"name": "read_file",
				},
			},
			want: ChoicePolicy{Mode: ChoiceForced, Name: "read_file"},
		},
		{
			name: "anthropic forced tool",
			choice: map[string]any{
				"type": "tool",
				"name": "search",
			},
			want: ChoicePolicy{Mode: ChoiceForced, Name: "search"},
		},
		{
			name: "uppercase anthropic forced tool",
			choice: map[string]any{
				"type": "TOOL",
				"name": "search",
			},
			want: ChoicePolicy{Mode: ChoiceForced, Name: "search"},
		},
		{
			name: "anthropic any object",
			choice: map[string]any{
				"type": "any",
			},
			want: ChoicePolicy{Mode: ChoiceRequired},
		},
		{
			name: "anthropic auto object",
			choice: map[string]any{
				"type": "auto",
			},
			want: ChoicePolicy{Mode: ChoiceAuto},
		},
		{
			name: "anthropic none object",
			choice: map[string]any{
				"type": "none",
			},
			want: ChoicePolicy{Mode: ChoiceNone},
		},
		{
			name:   "required string",
			choice: "required",
			want:   ChoicePolicy{Mode: ChoiceRequired},
		},
		{
			name:   "uppercase none string",
			choice: "NONE",
			want:   ChoicePolicy{Mode: ChoiceNone},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PolicyFromToolChoice(tt.choice); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("PolicyFromToolChoice() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildPromptIncludesCompactToolSpec(t *testing.T) {
	tools := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "read_file",
				"description": "Read a file",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	prompt := BuildPrompt(tools, ChoicePolicy{Mode: ChoiceRequired})
	for _, want := range []string{"Tool: read_file", "Description: Read a file", "<tool_calls>", "<parameters>"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BuildPrompt() missing %q in %q", want, prompt)
		}
	}
	if strings.Contains(strings.ToLower(prompt), "few-shot") {
		t.Fatalf("BuildPrompt() should stay compact, got %q", prompt)
	}
}

func TestNormalizeForSchemasStringifiesObjectForStringSchema(t *testing.T) {
	calls := []ParsedCall{{
		Name: "read_file",
		Input: map[string]any{
			"payload": map[string]any{"a": float64(1)},
		},
	}}
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name": "read_file",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"payload": map[string]any{"type": "string"},
				},
			},
		},
	}}

	normalized := NormalizeForSchemas(calls, tools)
	if got := normalized[0].Input["payload"]; got != `{"a":1}` {
		t.Fatalf("normalized payload = %#v, want JSON string", got)
	}
}

func TestFormatOpenAI(t *testing.T) {
	calls := []ParsedCall{{
		Name:  "read_file",
		Input: map[string]any{"path": "a.go", "limit": 2},
	}}

	got := FormatOpenAI(calls)
	if len(got) != 1 {
		t.Fatalf("len(FormatOpenAI()) = %d, want 1", len(got))
	}
	if got[0]["type"] != "function" {
		t.Fatalf("type = %#v, want function", got[0]["type"])
	}
	id, ok := got[0]["id"].(string)
	if !ok {
		t.Fatalf("id = %#v, want string", got[0]["id"])
	}
	if !strings.HasPrefix(id, "call_") {
		t.Fatalf("id = %q, want prefix call_", id)
	}
	function, _ := got[0]["function"].(map[string]any)
	if function["name"] != "read_file" {
		t.Fatalf("name = %#v, want read_file", function["name"])
	}
	if function["arguments"] != `{"limit":2,"path":"a.go"}` {
		t.Fatalf("arguments = %#v, want JSON string", function["arguments"])
	}
}

func TestFormatOpenAIStream(t *testing.T) {
	calls := []ParsedCall{{
		Name:  "read_file",
		Input: map[string]any{"path": "a.go"},
	}}

	got := FormatOpenAIStream(calls)
	if len(got) != 1 {
		t.Fatalf("len(FormatOpenAIStream()) = %d, want 1", len(got))
	}
	if got[0]["index"] != 0 {
		t.Fatalf("index = %#v, want 0", got[0]["index"])
	}
	delta, _ := got[0]["function"].(map[string]any)
	if delta["arguments"] != `{"path":"a.go"}` {
		t.Fatalf("arguments = %#v, want JSON string", delta["arguments"])
	}
}

func TestFormatAnthropic(t *testing.T) {
	calls := []ParsedCall{{
		Name:  "search",
		Input: map[string]any{"query": "golang"},
	}}

	got := FormatAnthropic(calls)
	if len(got) != 1 {
		t.Fatalf("len(FormatAnthropic()) = %d, want 1", len(got))
	}
	if got[0]["type"] != "tool_use" {
		t.Fatalf("type = %#v, want tool_use", got[0]["type"])
	}
	if got[0]["name"] != "search" {
		t.Fatalf("name = %#v, want search", got[0]["name"])
	}
	if _, ok := got[0]["id"].(string); !ok {
		t.Fatalf("id = %#v, want string", got[0]["id"])
	}
	input, _ := got[0]["input"].(map[string]any)
	if !reflect.DeepEqual(input, map[string]any{"query": "golang"}) {
		t.Fatalf("input = %#v, want %#v", input, map[string]any{"query": "golang"})
	}
}
