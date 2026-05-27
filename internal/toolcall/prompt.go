package toolcall

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func BuildPrompt(tools any, policy ChoicePolicy) string {
	if policy.Mode == ChoiceNone {
		return ""
	}

	catalog := NewBridgeCatalog(tools, policy)
	if len(catalog.Tools) == 0 {
		return ""
	}

	lines := []string{
		"=== MANDATORY TOOL CALL INSTRUCTIONS ===",
		"These are gateway bridge tools, not native platform tools.",
		"Use the bridge slot names below when emitting tool calls.",
		fmt.Sprintf("Bridge-call slots available: %s", strings.Join(catalog.BridgeNames(), ", ")),
		"XML call example: <tool_calls><invoke name=\"bridge-0\"><parameter name=\"path\"><![CDATA[value]]></parameter></invoke></tool_calls>",
		"Use the bridge slot list above as authoritative; the example is illustrative.",
	}

	switch policy.Mode {
	case ChoiceRequired:
		lines = append(lines, "MANDATORY: this turn MUST include at least one tool call")
	case ChoiceForced:
		if policy.Name != "" {
			lines = append(lines, fmt.Sprintf("MANDATORY: this turn MUST call the exact bridge slot \"%s\" for client tool \"%s\"", catalog.BridgeNames()[0], policy.Name))
		}
	}

	for _, tool := range catalog.Tools {
		line := fmt.Sprintf("- %s: %s", tool.BridgeName, strings.TrimSpace(tool.Description))
		if hint := compactParameterHint(tool.Schema); hint != "" {
			if tool.Description != "" {
				line += " "
			}
			line += hint
		}
		lines = append(lines, strings.TrimSpace(line))
	}

	return strings.Join(lines, "\n")
}

func compactParameterHint(schema any) string {
	m, ok := schema.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}

	props, _ := m["properties"].(map[string]any)
	keys := sortedMapKeys(props)
	if len(keys) == 0 {
		return ""
	}

	hint := "input keys: " + strings.Join(keys, ", ")
	if required := sortedStringValues(m["required"]); len(required) > 0 {
		hint += "; required: " + strings.Join(required, ", ")
	}
	return hint
}

func sortedMapKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringValues(v any) []string {
	var values []string
	switch items := v.(type) {
	case []string:
		values = append(values, items...)
	case []any:
		values = make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := item.(string); ok && s != "" {
				values = append(values, s)
			}
		}
	default:
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	return values
}

func ExtractToolMeta(tool map[string]any) (string, string, any) {
	if fn, ok := tool["function"].(map[string]any); ok {
		return strings.TrimSpace(asString(fn["name"])), strings.TrimSpace(asString(fn["description"])), firstNonNil(fn["parameters"], fn["inputSchema"], fn["schema"])
	}
	return strings.TrimSpace(asString(tool["name"])), strings.TrimSpace(asString(tool["description"])), firstNonNil(tool["input_schema"], tool["inputSchema"], tool["schema"], tool["parameters"])
}

func ToolNames(tools any) []string {
	metas := toolMetas(tools)
	out := make([]string, 0, len(metas))
	for _, meta := range metas {
		if meta.Name != "" {
			out = append(out, meta.Name)
		}
	}
	return out
}

type ToolMeta struct {
	Name        string
	Description string
	Schema      any
}

func toolMetas(tools any) []ToolMeta {
	items := toToolMaps(tools)
	out := make([]ToolMeta, 0, len(items))
	for _, item := range items {
		name, description, schema := ExtractToolMeta(item)
		if name == "" {
			continue
		}
		out = append(out, ToolMeta{Name: name, Description: description, Schema: schema})
	}
	return out
}

func toToolMaps(tools any) []map[string]any {
	switch v := tools.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{v}
	default:
		return nil
	}
}

func compactJSON(v any) string {
	if v == nil {
		return ""
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(buf)
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
