package toolcall

import (
	"encoding/json"
	"strings"
)

func BuildPrompt(tools any, policy ChoicePolicy) string {
	if policy.Mode == ChoiceNone {
		return ""
	}

	metas := toolMetas(tools)
	if policy.Mode == ChoiceForced && policy.Name != "" {
		filtered := metas[:0]
		for _, meta := range metas {
			if meta.Name == policy.Name {
				filtered = append(filtered, meta)
			}
		}
		metas = filtered
	}
	if len(metas) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Use XML tool calls when needed.\n")
	if policy.Mode == ChoiceRequired || policy.Mode == ChoiceForced {
		b.WriteString("You must call a tool before answering.\n")
	}
	b.WriteString("Format: <tool_calls><tool_call><tool_name>NAME</tool_name><parameters>{JSON}</parameters></tool_call></tool_calls>\n")
	for _, meta := range metas {
		b.WriteString("Tool: ")
		b.WriteString(meta.Name)
		b.WriteByte('\n')
		if meta.Description != "" {
			b.WriteString("Description: ")
			b.WriteString(meta.Description)
			b.WriteByte('\n')
		}
		b.WriteString("<tool_calls><tool_call><tool_name>")
		b.WriteString(meta.Name)
		b.WriteString("</tool_name><parameters>")
		if schemaJSON := compactJSON(meta.Schema); schemaJSON != "" {
			b.WriteString(schemaJSON)
		} else {
			b.WriteString("{}")
		}
		b.WriteString("</parameters></tool_call></tool_calls>\n")
	}
	return strings.TrimSpace(b.String())
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
