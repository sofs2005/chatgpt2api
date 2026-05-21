package contextoffload

import (
	"encoding/json"
	"errors"
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

const attachmentNote = "完整对话上下文已作为附件上传。请阅读 history.txt，并优先执行其中的 Current User Task。"

type Options struct {
	InlineMaxChars        int
	ForceFileMaxChars     int
	LatestUserMaxChars    int
	SummaryMaxChars       int
	ContextPromptMaxChars int
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
	LatestTooLong    bool
	SummaryText      string
	ToolFallbackText string
}

func DefaultOptions() Options {
	return Options{
		InlineMaxChars:        70000,
		ForceFileMaxChars:     120000,
		LatestUserMaxChars:    24000,
		SummaryMaxChars:       1200,
		ContextPromptMaxChars: 4000,
	}
}

func PlanContext(messages []map[string]any, tools any, choice any, options Options) Plan {
	options = normalizeOptions(options)
	estimated := estimateChars(messages, tools)
	historyEstimated := estimateChars(messages, nil)
	latest, latestIndex := latestUserMessage(messages)
	latestTooLong := len(latest) > options.LatestUserMaxChars
	toolsText := toolsText(tools, choice)
	historyNeedsFile := historyEstimated > options.InlineMaxChars || latestTooLong || historyEstimated > options.ForceFileMaxChars
	needsFile := historyNeedsFile || toolsText != ""
	if !needsFile {
		return Plan{Mode: ModeInline, InlineMessages: cloneMessages(messages), LatestUserText: latest}
	}

	history := ""
	if historyNeedsFile {
		history = historyText(messages, latestIndex)
		if latest != "" {
			if history != "" {
				history += "\n\n"
			}
			history += "## Current User Task\n" + latest + "\n"
		}
	}

	files := make([]File, 0, 2)
	if strings.TrimSpace(history) != "" {
		files = append(files, File{Filename: "history.txt", ContentType: "text/plain", Text: "# Conversation Context\n\n" + strings.TrimSpace(history) + "\n", Purpose: "history"})
	}
	if strings.TrimSpace(toolsText) != "" {
		files = append(files, File{Filename: "tools.txt", ContentType: "text/plain", Text: toolsText, Purpose: "tools"})
	}

	mode := ModeHybrid
	if historyNeedsFile && (estimated > options.ForceFileMaxChars || latestTooLong) {
		mode = ModeFile
	}
	summary := strings.TrimSpace(history)
	if len(summary) > options.SummaryMaxChars {
		summary = summary[:options.SummaryMaxChars]
	}
	return Plan{
		Mode:             mode,
		InlineMessages:   buildInlineMessages(mode, latest, latestTooLong, strings.TrimSpace(history) != "", toolsText != "", choice, options),
		Files:            files,
		LatestUserText:   latest,
		LatestTooLong:    latestTooLong,
		SummaryText:      summary,
		ToolFallbackText: toolFallbackText(tools, choice),
	}
}

func (p Plan) NeedsUpload() bool {
	return len(p.Files) > 0
}

func (p Plan) FallbackInlineMessages() ([]map[string]any, error) {
	if p.LatestTooLong {
		return nil, errors.New("latest user message is too large for inline fallback after context attachment upload failed")
	}
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
	if options.LatestUserMaxChars <= 0 {
		options.LatestUserMaxChars = defaults.LatestUserMaxChars
	}
	if options.SummaryMaxChars <= 0 {
		options.SummaryMaxChars = defaults.SummaryMaxChars
	}
	if options.ContextPromptMaxChars <= 0 {
		options.ContextPromptMaxChars = defaults.ContextPromptMaxChars
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
			text := strings.TrimSpace(messageText(messages[index]["content"]))
			if text != "" {
				return text, index
			}
		}
	}
	return "", -1
}

func historyText(messages []map[string]any, latestIndex int) string {
	var parts []string
	for index, message := range messages {
		if index == latestIndex {
			continue
		}
		text := strings.TrimSpace(messageText(message["content"]))
		if text == "" {
			continue
		}
		role := firstNonEmpty(util.Clean(message["role"]), "unknown")
		parts = append(parts, fmt.Sprintf("## Message %d [%s]\n%s\n", index+1, role, text))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func buildInlineMessages(mode, latest string, latestTooLong bool, hasHistoryFile bool, hasToolsFile bool, choice any, options Options) []map[string]any {
	var lines []string
	if hasHistoryFile {
		lines = append(lines, attachmentNote)
	}
	if hasToolsFile {
		lines = append(lines, "可用工具说明在 tools.txt。")
	}
	if hasHistoryFile && (latestTooLong || mode == ModeFile) {
		lines = append(lines, "当前用户任务也在 history.txt 的 Current User Task 小节中，正文不重复粘贴长任务。")
	} else if strings.TrimSpace(latest) != "" {
		lines = append(lines, "Current User Task:\n"+latest)
	}
	if tooladapter.PolicyFromToolChoice(choice).Mode != tooladapter.ChoiceNone && hasToolsFile {
		lines = append(lines, "工具调用必须输出且只输出 XML：<tool_calls><tool_call><tool_name>TOOL_NAME</tool_name><parameters>{JSON}</parameters></tool_call></tool_calls>")
	}
	content := strings.Join(lines, "\n\n")
	if len(content) > options.ContextPromptMaxChars {
		content = content[:options.ContextPromptMaxChars]
	}
	return []map[string]any{{"role": "user", "content": content}}
}

func toolsText(tools any, choice any) string {
	if tooladapter.PolicyFromToolChoice(choice).Mode == tooladapter.ChoiceNone {
		return ""
	}
	items := toolMaps(tools)
	if len(items) == 0 {
		return ""
	}
	var blocks []string
	for _, item := range items {
		name, description, schema := tooladapter.ExtractToolMeta(item)
		if strings.TrimSpace(name) == "" {
			continue
		}
		schemaJSON, _ := json.Marshal(schema)
		blocks = append(blocks, "Tool: "+name+"\nDescription: "+description+"\nParameters: "+string(schemaJSON))
	}
	if len(blocks) == 0 {
		return ""
	}
	return "# Available Tools\n\n" + strings.Join(blocks, "\n\n") + "\n"
}

func toolFallbackText(tools any, choice any) string {
	if tooladapter.PolicyFromToolChoice(choice).Mode == tooladapter.ChoiceNone {
		return ""
	}
	var names []string
	for _, item := range toolMaps(tools) {
		name, _, _ := tooladapter.ExtractToolMeta(item)
		if strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "上下文附件上传失败。可用工具名称：" + strings.Join(names, ", ") + "。工具调用必须输出且只输出 XML：<tool_calls><tool_call><tool_name>TOOL_NAME</tool_name><parameters>{JSON}</parameters></tool_call></tool_calls>"
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
