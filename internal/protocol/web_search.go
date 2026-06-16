package protocol

import (
	"regexp"
	"strconv"
	"strings"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/util"
)

// WebSearchToolTypes 是被识别为 web 搜索的工具类型集合。
var WebSearchToolTypes = map[string]bool{
	"web_search":                    true,
	"web_search_preview":            true,
	"web_search_preview_2025_03_11": true,
}

// searchChatModelPrefixes 是被识别为搜索请求的模型前缀。
var searchChatModelPrefixes = []string{
	"gpt-4o-search-preview",
	"gpt-4o-mini-search-preview",
	"gpt-5-search-api",
}

// 私有区注解控制字符：U+E200 起始、U+E201 结束、U+E202 字段分隔。
const (
	annStartChar = string(rune(0xE200))
	annEndChar   = string(rune(0xE201))
	annSepChar   = string(rune(0xE202))
)

// SearchAnnotation 表示一条 url_citation 注解。
type SearchAnnotation struct {
	Type       string
	StartIndex int
	EndIndex   int
	URL        string
	Title      string
}

func webSearchToolType(tool any) string {
	if m, ok := tool.(map[string]any); ok {
		return strings.TrimSpace(util.Clean(m["type"]))
	}
	return ""
}

// HasWebSearchTool 判断请求体是否声明了 web 搜索工具。
func HasWebSearchTool(body map[string]any) bool {
	if tools, ok := body["tools"].([]any); ok {
		for _, tool := range tools {
			if WebSearchToolTypes[webSearchToolType(tool)] {
				return true
			}
		}
	}
	return WebSearchToolTypes[webSearchToolType(body["tool_choice"])]
}

// IsWebSearchChatRequest 判断聊天请求是否应走 web 搜索流程。
func IsWebSearchChatRequest(body map[string]any) bool {
	if HasWebSearchTool(body) {
		return true
	}
	if _, ok := body["web_search_options"].(map[string]any); ok {
		return true
	}
	model := strings.TrimSpace(util.Clean(body["model"]))
	for _, prefix := range searchChatModelPrefixes {
		if model == prefix || strings.HasPrefix(model, prefix+"-") {
			return true
		}
	}
	return false
}

// HasUnsupportedTools 判断请求体是否包含 allowed 之外的工具类型。
func HasUnsupportedTools(body map[string]any, allowed map[string]bool) bool {
	tools, ok := body["tools"].([]any)
	if !ok {
		return false
	}
	for _, tool := range tools {
		if _, isMap := tool.(map[string]any); !isMap {
			continue
		}
		if !allowed[webSearchToolType(tool)] {
			return true
		}
	}
	return false
}

// WebSearchMessageText 从消息 content 中提取纯文本。
func WebSearchMessageText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			text := ""
			switch iv := item.(type) {
			case string:
				text = strings.TrimSpace(iv)
			case map[string]any:
				text = strings.TrimSpace(util.Clean(iv["text"]))
				if text == "" {
					text = strings.TrimSpace(util.Clean(iv["input_text"]))
				}
			}
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

// SearchQueryFromMessages 返回最后一条 user 消息的文本，作为搜索 query。
func SearchQueryFromMessages(messages []map[string]any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if strings.ToLower(strings.TrimSpace(util.Clean(message["role"]))) != "user" {
			continue
		}
		if text := WebSearchMessageText(message["content"]); text != "" {
			return text
		}
	}
	return ""
}

var (
	searchAnnotationRe       = regexp.MustCompile(annStartChar + "([^" + annEndChar + "]*)" + annEndChar)
	searchTrailingAnnotation = regexp.MustCompile(annStartChar + "[^" + annEndChar + "]*$")
	searchSpaceBeforePunct   = regexp.MustCompile(`\s+([.,;:!?])`)
)

func readableAnnotationPart(parts []string) string {
	for _, part := range parts {
		value := strings.TrimSpace(part)
		lower := strings.ToLower(value)
		if value == "" {
			continue
		}
		if strings.HasPrefix(lower, "turn") || strings.HasPrefix(lower, "source") {
			continue
		}
		if isAllDigits(value) {
			continue
		}
		return value
	}
	return ""
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// CleanSearchText 清理搜索答案里的私有区注解控制字符，还原可读文本。
func CleanSearchText(text string) string {
	text = searchAnnotationRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := searchAnnotationRe.FindStringSubmatch(match)[1]
		rawParts := strings.Split(inner, annSepChar)
		parts := make([]string, len(rawParts))
		for i, p := range rawParts {
			parts[i] = strings.TrimSpace(p)
		}
		kind := ""
		if len(parts) > 0 {
			kind = strings.ToLower(parts[0])
		}
		data := parts[1:]
		if kind == "url" {
			label := ""
			if len(data) > 0 {
				label = data[0]
			}
			url := ""
			if len(data) > 1 {
				url = data[1]
			}
			if label != "" && (strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")) {
				return label + " (" + url + ")"
			}
			if label != "" {
				return label
			}
			return url
		}
		return readableAnnotationPart(data)
	})
	text = searchTrailingAnnotation.ReplaceAllString(text, "")
	text = searchSpaceBeforePunct.ReplaceAllString(text, "$1")
	return strings.TrimSpace(text)
}

// NormalizedSources 去重并清理来源列表（按 URL 去重，丢弃空 URL）。
func NormalizedSources(sources []backend.SearchSource) []backend.SearchSource {
	output := make([]backend.SearchSource, 0, len(sources))
	seen := make(map[string]bool)
	for _, item := range sources {
		url := strings.TrimSpace(item.URL)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		output = append(output, backend.SearchSource{
			Title:   strings.TrimSpace(item.Title),
			URL:     url,
			Snippet: strings.TrimSpace(item.Snippet),
		})
	}
	return output
}

// TextWithURLCitations 在答案后追加 Sources 列表，并生成 url_citation 注解。
func TextWithURLCitations(answer string, sources []backend.SearchSource) (string, []SearchAnnotation) {
	text := CleanSearchText(answer)
	annotations := make([]SearchAnnotation, 0)
	normalized := NormalizedSources(sources)
	if len(normalized) > 0 {
		text = strings.TrimRight(text, " \t\r\n")
		if text != "" {
			text += "\n\n"
		}
		text += "Sources:\n"
		for index, source := range normalized {
			title := source.Title
			if title == "" {
				title = source.URL
			}
			text += strconv.Itoa(index+1) + ". " + title
			if source.URL != "" {
				if source.Title != "" {
					text += " - "
				}
				start := len(text)
				text += source.URL
				annotations = append(annotations, SearchAnnotation{
					Type:       "url_citation",
					StartIndex: start,
					EndIndex:   len(text),
					URL:        source.URL,
					Title:      firstNonEmpty(source.Title, source.URL),
				})
			}
			text += "\n"
		}
	}
	return strings.TrimSpace(text), annotations
}
