package protocol

import (
	"testing"

	"chatgpt2api/internal/backend"
)

func TestHasWebSearchTool(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want bool
	}{
		{"tools list", map[string]any{"tools": []any{map[string]any{"type": "web_search"}}}, true},
		{"tools preview", map[string]any{"tools": []any{map[string]any{"type": "web_search_preview"}}}, true},
		{"tool_choice", map[string]any{"tool_choice": map[string]any{"type": "web_search_preview_2025_03_11"}}, true},
		{"other tool", map[string]any{"tools": []any{map[string]any{"type": "function"}}}, false},
		{"empty", map[string]any{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasWebSearchTool(tc.body); got != tc.want {
				t.Fatalf("HasWebSearchTool() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsWebSearchChatRequest(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want bool
	}{
		{"web_search tool", map[string]any{"tools": []any{map[string]any{"type": "web_search"}}}, true},
		{"web_search_options", map[string]any{"web_search_options": map[string]any{}}, true},
		{"search model exact", map[string]any{"model": "gpt-4o-search-preview"}, true},
		{"search model suffixed", map[string]any{"model": "gpt-5-search-api-2025"}, true},
		{"plain model", map[string]any{"model": "gpt-4o"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsWebSearchChatRequest(tc.body); got != tc.want {
				t.Fatalf("IsWebSearchChatRequest() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasUnsupportedTools(t *testing.T) {
	allowed := WebSearchToolTypes
	body := map[string]any{"tools": []any{
		map[string]any{"type": "web_search"},
		map[string]any{"type": "function"},
	}}
	if !HasUnsupportedTools(body, allowed) {
		t.Fatalf("expected unsupported tool detected")
	}
	clean := map[string]any{"tools": []any{map[string]any{"type": "web_search"}}}
	if HasUnsupportedTools(clean, allowed) {
		t.Fatalf("expected no unsupported tool")
	}
}

func TestWebSearchMessageText(t *testing.T) {
	if got := WebSearchMessageText("  hello  "); got != "hello" {
		t.Fatalf("string text = %q", got)
	}
	list := []any{
		"first",
		map[string]any{"text": "second"},
		map[string]any{"input_text": "third"},
		map[string]any{"image_url": "ignored"},
	}
	if got := WebSearchMessageText(list); got != "first\nsecond\nthird" {
		t.Fatalf("list text = %q", got)
	}
}

func TestSearchQueryFromMessages(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "be helpful"},
		{"role": "user", "content": "first question"},
		{"role": "assistant", "content": "an answer"},
		{"role": "user", "content": "latest question"},
	}
	if got := SearchQueryFromMessages(messages); got != "latest question" {
		t.Fatalf("query = %q", got)
	}
	if got := SearchQueryFromMessages([]map[string]any{{"role": "assistant", "content": "x"}}); got != "" {
		t.Fatalf("expected empty query, got %q", got)
	}
}

func TestCleanSearchText(t *testing.T) {
	// 私有区注解控制字符：U+E200 起始、U+E201 结束、U+E202 字段分隔。
	annStart := string(rune(0xE200))
	annEnd := string(rune(0xE201))
	annSep := string(rune(0xE202))
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{
			"url annotation",
			"see " + annStart + "url" + annSep + "Example" + annSep + "https://example.com" + annEnd + " now",
			"see Example (https://example.com) now",
		},
		{
			"trailing unclosed",
			"answer text " + annStart + "url" + annSep + "partial",
			"answer text",
		},
		{
			"space before punctuation",
			"word " + annStart + "cite" + annSep + "turn0" + annSep + "Title" + annEnd + " .",
			"word Title.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanSearchText(tc.in); got != tc.want {
				t.Fatalf("CleanSearchText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizedSources(t *testing.T) {
	sources := []backend.SearchSource{
		{Title: "A", URL: "https://a.com", Snippet: "sa"},
		{Title: "dup", URL: "https://a.com", Snippet: "dup"},
		{Title: "B", URL: "", Snippet: "no url"},
		{Title: "C", URL: "https://c.com"},
	}
	got := NormalizedSources(sources)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].URL != "https://a.com" || got[1].URL != "https://c.com" {
		t.Fatalf("urls = %#v", got)
	}
}

func TestTextWithURLCitations(t *testing.T) {
	answer := "The capital is Paris."
	sources := []backend.SearchSource{
		{Title: "Wiki", URL: "https://wiki.com"},
	}
	text, annotations := TextWithURLCitations(answer, sources)
	if want := "The capital is Paris.\n\nSources:\n1. Wiki - https://wiki.com"; text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
	if len(annotations) != 1 {
		t.Fatalf("annotations len = %d", len(annotations))
	}
	ann := annotations[0]
	if ann.Type != "url_citation" || ann.URL != "https://wiki.com" || ann.Title != "Wiki" {
		t.Fatalf("annotation = %#v", ann)
	}
	if text[ann.StartIndex:ann.EndIndex] != "https://wiki.com" {
		t.Fatalf("annotation span = %q", text[ann.StartIndex:ann.EndIndex])
	}
}
