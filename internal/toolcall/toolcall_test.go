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
			name:   "any string",
			choice: "any",
			want:   ChoicePolicy{Mode: ChoiceRequired},
		},
		{
			name: "openai required object",
			choice: map[string]any{
				"type": "required",
			},
			want: ChoicePolicy{Mode: ChoiceRequired},
		},
		{
			name: "uppercase required object",
			choice: map[string]any{
				"type": "REQUIRED",
			},
			want: ChoicePolicy{Mode: ChoiceRequired},
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

func TestBridgeCatalogMapsSlotsToOriginalToolNames(t *testing.T) {
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "read_file", "description": "Read a file", "parameters": map[string]any{"type": "object"}}},
		{"type": "function", "function": map[string]any{"name": "search", "description": "Search docs", "parameters": map[string]any{"type": "object"}}},
	}

	catalog := NewBridgeCatalog(tools, ChoicePolicy{Mode: ChoiceAuto})

	if got := catalog.BridgeNames(); !reflect.DeepEqual(got, []string{"bridge-0", "bridge-1"}) {
		t.Fatalf("BridgeNames() = %#v", got)
	}
	if got := catalog.OriginalName("bridge-1"); got != "search" {
		t.Fatalf("OriginalName(bridge-1) = %q, want search", got)
	}
	if got := catalog.AllowedParseNames(); !reflect.DeepEqual(got, []string{"bridge-0", "bridge-1", "read_file", "search"}) {
		t.Fatalf("AllowedParseNames() = %#v", got)
	}
}

func TestBridgeCatalogForcedToolKeepsOnlyForcedSlot(t *testing.T) {
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "read_file", "description": "Read a file", "parameters": map[string]any{"type": "object"}}},
		{"type": "function", "function": map[string]any{"name": "search", "description": "Search docs", "parameters": map[string]any{"type": "object"}}},
	}

	catalog := NewBridgeCatalog(tools, ChoicePolicy{Mode: ChoiceForced, Name: "search"})

	if got := catalog.BridgeNames(); !reflect.DeepEqual(got, []string{"bridge-0"}) {
		t.Fatalf("BridgeNames() = %#v", got)
	}
	if got := catalog.OriginalName("bridge-0"); got != "search" {
		t.Fatalf("OriginalName(bridge-0) = %q, want search", got)
	}
}

func TestBridgeCatalogReportsMissingForcedTool(t *testing.T) {
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "read_file", "description": "Read a file", "parameters": map[string]any{"type": "object"}}},
		{"type": "function", "function": map[string]any{"name": "search", "description": "Search docs", "parameters": map[string]any{"type": "object"}}},
	}

	catalog := NewBridgeCatalog(tools, ChoicePolicy{Mode: ChoiceForced, Name: "write_file"})

	if catalog.MissingForcedName != "write_file" {
		t.Fatalf("MissingForcedName = %q, want write_file", catalog.MissingForcedName)
	}
	if err := catalog.ValidationError(); err == nil || err.Error() != "tool_choice forced write_file but no matching tool was found" {
		t.Fatalf("ValidationError() = %v", err)
	}
}

func TestBridgeCatalogAvoidsBridgeNameCollisions(t *testing.T) {
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "bridge-0", "parameters": map[string]any{"type": "object"}}},
		{"type": "function", "function": map[string]any{"name": "search", "parameters": map[string]any{"type": "object"}}},
	}

	catalog := NewBridgeCatalog(tools, ChoicePolicy{Mode: ChoiceAuto})

	if got := catalog.BridgeNames(); !reflect.DeepEqual(got, []string{"bridge-0-slot", "bridge-1"}) {
		t.Fatalf("BridgeNames() = %#v", got)
	}
	if got := catalog.AllowedParseNames(); !reflect.DeepEqual(got, []string{"bridge-0-slot", "bridge-1", "bridge-0", "search"}) {
		t.Fatalf("AllowedParseNames() = %#v", got)
	}

	resolved := catalog.ResolveCalls([]ParsedCall{{Name: "bridge-0-slot", Input: map[string]any{"path": "a.go"}}})
	if resolved[0].Name != "bridge-0" {
		t.Fatalf("resolved name = %q, want bridge-0", resolved[0].Name)
	}
}

func TestResolveBridgeCallsRestoresOriginalToolName(t *testing.T) {
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "read_file", "parameters": map[string]any{"type": "object"}}}}
	catalog := NewBridgeCatalog(tools, ChoicePolicy{Mode: ChoiceAuto})
	calls := []ParsedCall{{Name: "bridge-0", Input: map[string]any{"path": "a.go"}}}

	resolved := catalog.ResolveCalls(calls)

	if resolved[0].Name != "read_file" {
		t.Fatalf("resolved name = %q, want read_file", resolved[0].Name)
	}
	if got := resolved[0].Input["path"]; got != "a.go" {
		t.Fatalf("path = %#v, want a.go", got)
	}
}

func TestBuildPromptIncludesBridgeToolInstructions(t *testing.T) {
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
					"required": []any{"path"},
				},
			},
		},
	}

	prompt := BuildPrompt(tools, ChoicePolicy{Mode: ChoiceRequired})

	for _, want := range []string{
		"=== MANDATORY TOOL CALL INSTRUCTIONS ===",
		"gateway bridge tools",
		"Bridge-call slots available: bridge-0",
		`<tool_calls><invoke name="bridge-0">`,
		`<parameter name="path"><![CDATA[value]]></parameter>`,
		"MANDATORY: this turn MUST include at least one tool call",
		"- bridge-0: Read a file input keys: path; required: path",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BuildPrompt() missing %q in %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "Tool: read_file") {
		t.Fatalf("BuildPrompt() leaked legacy tool block: %q", prompt)
	}
}

func TestBuildPromptForcedToolMentionsExactBridgeSlotAndOriginalName(t *testing.T) {
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "read_file", "description": "Read a file", "parameters": map[string]any{"type": "object"}}},
		{"type": "function", "function": map[string]any{"name": "search", "description": "Search docs", "parameters": map[string]any{"type": "object"}}},
	}

	prompt := BuildPrompt(tools, ChoicePolicy{Mode: ChoiceForced, Name: "search"})

	for _, want := range []string{
		`MUST call the exact bridge slot "bridge-0" for client tool "search"`,
		"Bridge-call slots available: bridge-0",
		"- bridge-0: Search docs",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BuildPrompt() missing %q in %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "bridge-1") || strings.Contains(prompt, "Read a file") {
		t.Fatalf("forced prompt exposed non-forced tools: %q", prompt)
	}
}

func TestBuildPromptForcedToolUsesBridgeZeroWhenOriginalNameCollides(t *testing.T) {
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "bridge-0", "description": "Reserved name", "parameters": map[string]any{"type": "object"}}},
	}

	prompt := BuildPrompt(tools, ChoicePolicy{Mode: ChoiceForced, Name: "bridge-0"})

	for _, want := range []string{
		`MUST call the exact bridge slot "bridge-0" for client tool "bridge-0"`,
		"Bridge-call slots available: bridge-0",
		"- bridge-0: Reserved name",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BuildPrompt() missing %q in %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "bridge-0-slot") {
		t.Fatalf("forced prompt used a fallback slot name: %q", prompt)
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
