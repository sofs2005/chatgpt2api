package contextoffload

import (
	"encoding/json"
	"fmt"
	"strings"

	tooladapter "chatgpt2api/internal/toolcall"
	"chatgpt2api/internal/util"
)

const (
	ModeInline = "inline"
	ModeHybrid = "hybrid"
	ModeFile   = "file"
)

const attachmentNote = "完整对话上下文已作为附件上传。请将 history.txt 作为历史参考，并优先执行下方 Current User Task。"

type Options struct {
	InlineMaxChars    int
	ForceFileMaxChars int
}

type File struct {
	Filename    string
	ContentType string
	Text        string
	Purpose     string
}

type Plan struct {
	Mode             string
	InlineMessages   []map[string]any
	Files            []File
	LatestUserText   string
	SummaryText      string
	ToolFallbackText string
}

func DefaultOptions() Options {
	return Options{
		InlineMaxChars:    70000,
		ForceFileMaxChars: 120000,
	}
}

func PlanContext(messages []map[string]any, tools any, choice any, options Options) Plan {
	options = normalizeOptions(options)
	estimated := estimateChars(messages, tools)
	historyEstimated := estimateChars(messages, nil)
	latest, _ := latestUserMessage(messages)
	toolsText := toolsText(tools, choice)
	historyNeedsFile := historyEstimated > options.InlineMaxChars || historyEstimated > options.ForceFileMaxChars
	needsFile := historyNeedsFile || toolsText != ""
	if !needsFile {
		return Plan{Mode: ModeInline, InlineMessages: cloneMessages(messages), LatestUserText: latest}
	}

	history := ""
	if historyNeedsFile {
		history = historyText(messages, -1)
	}

	files := make([]File, 0, 2)
	if strings.TrimSpace(history) != "" {
		files = append(files, File{Filename: "history.txt", ContentType: "text/plain", Text: "# Conversation Context\n\n" + strings.TrimSpace(history) + "\n", Purpose: "history"})
	}
	if strings.TrimSpace(toolsText) != "" {
		files = append(files, File{Filename: "tools.txt", ContentType: "text/plain", Text: toolsText, Purpose: "tools"})
	}

	mode := ModeHybrid
	if historyNeedsFile && estimated > options.ForceFileMaxChars {
		mode = ModeFile
	}
	summary := strings.TrimSpace(history)
	return Plan{
		Mode:             mode,
		InlineMessages:   buildInlineMessages(latest, strings.TrimSpace(history) != "", toolsText),
		Files:            files,
		LatestUserText:   latest,
		SummaryText:      summary,
		ToolFallbackText: toolFallbackText(tools, choice),
	}
}

func (p Plan) NeedsUpload() bool {
	return len(p.Files) > 0
}

func (p Plan) FallbackInlineMessages() ([]map[string]any, error) {
	messages := make([]map[string]any, 0, 2)
	if strings.TrimSpace(p.SummaryText) != "" {
		messages = append(messages, map[string]any{"role": "user", "content": "上下文附件上传失败，以下是可用的历史摘要：\n" + p.SummaryText})
	}
	if strings.TrimSpace(p.ToolFallbackText) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": p.ToolFallbackText})
	}
	if strings.TrimSpace(p.LatestUserText) != "" {
		messages = append(messages, map[string]any{"role": "user", "content": p.LatestUserText})
	}
	if len(messages) == 0 {
		messages = append(messages, map[string]any{"role": "user", "content": "上下文附件上传失败，请基于可用上下文继续。"})
	}
	return messages, nil
}

func normalizeOptions(options Options) Options {
	defaults := DefaultOptions()
	if options.InlineMaxChars <= 0 {
		options.InlineMaxChars = defaults.InlineMaxChars
	}
	if options.ForceFileMaxChars <= 0 {
		options.ForceFileMaxChars = defaults.ForceFileMaxChars
	}
	return options
}

func estimateChars(messages []map[string]any, tools any) int {
	total := 0
	for _, message := range messages {
		total += len(util.Clean(message["role"])) + len(messageText(message["content"])) + 24
	}
	for _, item := range toolMaps(tools) {
		data, _ := json.Marshal(item)
		total += len(data)
	}
	return total
}

func latestUserMessage(messages []map[string]any) (string, int) {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.EqualFold(util.Clean(messages[index]["role"]), "user") {
			text := strings.TrimSpace(userTaskText(messages[index]["content"]))
			if text != "" {
				return text, index
			}
		}
	}
	return "", -1
}

func historyText(messages []map[string]any, skipIndex int) string {
	var parts []string
	for index, message := range messages {
		if index == skipIndex {
			continue
		}
		role := firstNonEmpty(util.Clean(message["role"]), "unknown")
		text := strings.TrimSpace(messageTextForRole(role, message["content"]))
		if text == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("## Message %d [%s]\n%s\n", index+1, role, text))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func buildInlineMessages(latest string, hasHistoryFile bool, toolsText string) []map[string]any {
	var lines []string
	if hasHistoryFile {
		lines = append(lines, attachmentNote)
	}
	if strings.TrimSpace(latest) != "" {
		lines = append(lines, "Current User Task:\n"+latest)
	} else if hasHistoryFile {
		lines = append(lines, "当前用户任务已包含在 history.txt 的最后一个 user 消息中。")
	}
	if strings.TrimSpace(toolsText) != "" {
		lines = append(lines, "可用工具说明也在 tools.txt；必须优先遵守以下桥接工具规则，不要把 history.txt 或 tools.txt 当作本地路径读取。\n\n"+strings.TrimSpace(toolsText))
	}
	content := strings.Join(lines, "\n\n")
	return []map[string]any{{"role": "user", "content": content}}
}

func toolsText(tools any, choice any) string {
	policy := tooladapter.PolicyFromToolChoice(choice)
	if policy.Mode == tooladapter.ChoiceNone {
		return ""
	}
	prompt := tooladapter.BuildPrompt(tools, policy)
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	return "# Available Tools\n\n" + prompt + "\n"
}

func toolFallbackText(tools any, choice any) string {
	policy := tooladapter.PolicyFromToolChoice(choice)
	if policy.Mode == tooladapter.ChoiceNone {
		return ""
	}
	catalog := tooladapter.NewBridgeCatalog(tools, policy)
	if len(catalog.Tools) == 0 {
		return ""
	}
	return "上下文附件上传失败。可用桥接工具槽位：" + strings.Join(catalog.BridgeNames(), ", ") + "。请阅读 tools.txt，并按其中的桥接工具说明输出 XML：<tool_calls><invoke name=\"bridge-0\"><parameter name=\"ARG\"><![CDATA[value]]></parameter></invoke></tool_calls>"
}

func toolMaps(tools any) []map[string]any {
	switch typed := tools.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{typed}
	default:
		return nil
	}
}

func messageText(content any) string {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text)
	}
	if data, err := json.Marshal(content); err == nil {
		return strings.TrimSpace(string(data))
	}
	return util.Clean(content)
}

func messageTextForRole(role string, content any) string {
	if strings.EqualFold(role, "user") {
		return userTaskText(content)
	}
	return messageText(content)
}

func userTaskText(content any) string {
	if text, ok := content.(string); ok {
		return sanitizeUserText(text)
	}
	parts, ok := content.([]any)
	if !ok {
		return sanitizeUserText(messageText(content))
	}
	chunks := make([]string, 0, len(parts))
	for _, part := range parts {
		item, ok := part.(map[string]any)
		if !ok {
			continue
		}
		switch util.Clean(item["type"]) {
		case "text", "input_text", "output_text":
			if text := sanitizeUserText(util.Clean(item["text"])); text != "" {
				chunks = append(chunks, text)
			}
		case "tool_result":
			if text := strings.TrimSpace(util.Clean(item["content"])); text != "" {
				chunks = append(chunks, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(chunks, "\n"))
}

func sanitizeUserText(text string) string {
	cleaned := strings.TrimSpace(text)
	if cleaned == "" {
		return ""
	}
	if strings.HasPrefix(cleaned, "System:") {
		return ""
	}
	if startsWithAny(cleaned, "## Memory Recall", "## Compiled Wiki") {
		parts := splitParagraphs(cleaned)
		for len(parts) > 0 && startsWithAny(parts[0], "## Memory Recall", "## Compiled Wiki") {
			parts = parts[1:]
		}
		cleaned = strings.TrimSpace(strings.Join(parts, "\n\n"))
		if cleaned == "" {
			return ""
		}
	}
	if containsAny(cleaned, "A new session was started via /new or /reset.", "If runtime-provided startup context is included for this first turn") {
		return ""
	}
	if startsWithAny(cleaned, "Sender (untrusted metadata):", "Conversation info (untrusted metadata):", "System (untrusted):") {
		cleaned = stripUntrustedMetadata(cleaned)
		if cleaned == "" {
			return ""
		}
	}
	lowered := strings.ToLower(cleaned)
	if strings.Contains(lowered, "the following skills provide specialized instructions for specific tasks.") && strings.Contains(lowered, "use the read tool to load a skill's file when the task matches its name.") && strings.Contains(lowered, "<available_skills>") {
		if end := strings.Index(lowered, "</available_skills>"); end >= 0 {
			cleaned = strings.TrimSpace(cleaned[end+len("</available_skills>"):])
		}
	}
	if isAgentRuntimeProse(cleaned) {
		cleaned = stripAgentRuntimeProse(cleaned)
	}
	return strings.TrimSpace(cleaned)
}

func stripUntrustedMetadata(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	kept := make([]string, 0, len(lines))
	for index := 0; index < len(lines); {
		line := strings.TrimSpace(lines[index])
		if startsWithAny(line, "Sender (untrusted metadata):", "Conversation info (untrusted metadata):", "System (untrusted):") {
			index++
			for index < len(lines) && strings.TrimSpace(lines[index]) == "" {
				index++
			}
			if index < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index]), "```") {
				index++
				for index < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[index]), "```") {
					index++
				}
				if index < len(lines) {
					index++
				}
			}
			continue
		}
		kept = append(kept, lines[index])
		index++
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func isAgentRuntimeProse(text string) bool {
	lowered := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lowered, "you are a personal assistant running inside") || strings.Contains(lowered, "tool availability (filtered by policy):") || strings.Contains(lowered, "## tooling") || strings.Contains(lowered, "the opencode system prompt may describe native or built-in tool syntax.")
}

func stripAgentRuntimeProse(text string) string {
	cleaned := strings.TrimSpace(text)
	lowered := strings.ToLower(cleaned)
	if index := strings.Index(lowered, "tool availability (filtered by policy):"); index >= 0 {
		if end := strings.Index(cleaned[index:], "\n\n"); end >= 0 {
			cleaned = strings.TrimSpace(cleaned[index+end+2:])
		}
	}
	lowered = strings.ToLower(cleaned)
	if strings.HasPrefix(lowered, "you are a personal assistant running inside ") {
		if end := strings.Index(cleaned, "\n\n"); end >= 0 {
			cleaned = strings.TrimSpace(cleaned[end+2:])
		} else {
			cleaned = ""
		}
	}
	return cleaned
}

func splitParagraphs(text string) []string {
	var parts []string
	var current []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				parts = append(parts, strings.TrimSpace(strings.Join(current, "\n")))
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		parts = append(parts, strings.TrimSpace(strings.Join(current, "\n")))
	}
	return parts
}

func startsWithAny(text string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func cloneMessages(messages []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		out = append(out, util.CopyMap(message))
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
