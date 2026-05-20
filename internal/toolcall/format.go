package toolcall

import (
	"fmt"
)

func openAIToolCallID(index int, name string) string {
	return fmt.Sprintf("call_%d_%s", index, name)
}

func FormatOpenAI(calls []ParsedCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for i, call := range calls {
		out = append(out, map[string]any{
			"id":   openAIToolCallID(i, call.Name),
			"type": "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": compactJSON(call.Input),
			},
		})
	}
	return out
}

func FormatOpenAIStream(calls []ParsedCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for i, call := range calls {
		out = append(out, map[string]any{
			"index": i,
			"id":    openAIToolCallID(i, call.Name),
			"type":  "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": compactJSON(call.Input),
			},
		})
	}
	return out
}

func FormatAnthropic(calls []ParsedCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for i, call := range calls {
		out = append(out, map[string]any{
			"type":  "tool_use",
			"id":    fmt.Sprintf("toolu_%d_%s", i, call.Name),
			"name":  call.Name,
			"input": cloneMap(call.Input),
		})
	}
	return out
}
