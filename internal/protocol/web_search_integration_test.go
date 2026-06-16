package protocol

import (
	"context"
	"testing"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/util"
)

func testSearchResult() backend.SearchResult {
	return backend.SearchResult{
		ConversationID: "conv-search",
		Answer:         "Search answer",
		Sources: []backend.SearchSource{
			{Title: "Example", URL: "https://example.com/article", Snippet: "snippet"},
		},
	}
}

func TestHandleChatCompletionsRoutesWebSearchRequest(t *testing.T) {
	calledQuery := ""
	engine := &Engine{SearchFunc: func(ctx context.Context, query string) (backend.SearchResult, error) {
		calledQuery = query
		return testSearchResult(), nil
	}}

	response, stream, err := engine.HandleChatCompletions(context.Background(), map[string]any{
		"model": "gpt-5-search-api",
		"messages": []any{
			map[string]any{"role": "user", "content": "first"},
			map[string]any{"role": "user", "content": "latest query"},
		},
	})
	if err != nil || stream != nil {
		t.Fatalf("HandleChatCompletions() response=%#v stream=%#v err=%v", response, stream, err)
	}
	if calledQuery != "latest query" {
		t.Fatalf("called query = %q, want latest query", calledQuery)
	}
	message := response["choices"].([]map[string]any)[0]["message"].(map[string]any)
	if got := util.Clean(message["content"]); got != "Search answer\n\nSources:\n1. Example - https://example.com/article" {
		t.Fatalf("content = %q", got)
	}
	annotations := message["annotations"].([]map[string]any)
	if len(annotations) != 1 {
		t.Fatalf("annotations = %#v", annotations)
	}
	citation := annotations[0]["url_citation"].(map[string]any)
	if citation["url"] != "https://example.com/article" || citation["title"] != "Example" {
		t.Fatalf("citation = %#v", citation)
	}
}

func TestHandleChatCompletionsStreamsWebSearchRequest(t *testing.T) {
	engine := &Engine{SearchFunc: func(ctx context.Context, query string) (backend.SearchResult, error) {
		return testSearchResult(), nil
	}}

	response, stream, err := engine.HandleChatCompletions(context.Background(), map[string]any{
		"model":    "gpt-5",
		"stream":   true,
		"tools":    []any{map[string]any{"type": "web_search"}},
		"messages": []any{map[string]any{"role": "user", "content": "stream query"}},
	})
	if err != nil || response != nil || stream == nil {
		t.Fatalf("HandleChatCompletions() response=%#v stream=%#v err=%v", response, stream, err)
	}
	items := collectStreamItems(stream.Items)
	if err := <-stream.Err; err != nil {
		t.Fatalf("stream err = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("stream items len = %d, want 2: %#v", len(items), items)
	}
	delta := items[0]["choices"].([]map[string]any)[0]["delta"].(map[string]any)
	if delta["role"] != "assistant" || util.Clean(delta["content"]) == "" {
		t.Fatalf("first delta = %#v", delta)
	}
	if finish := items[1]["choices"].([]map[string]any)[0]["finish_reason"]; finish != "stop" {
		t.Fatalf("finish = %#v, want stop", finish)
	}
}

func TestHandleResponsesRoutesWebSearchTool(t *testing.T) {
	calledQuery := ""
	engine := &Engine{SearchFunc: func(ctx context.Context, query string) (backend.SearchResult, error) {
		calledQuery = query
		return testSearchResult(), nil
	}}

	response, stream, err := engine.HandleResponses(context.Background(), map[string]any{
		"model": "gpt-5",
		"input": "response query",
		"tools": []any{map[string]any{"type": "web_search_preview"}},
	})
	if err != nil || stream != nil {
		t.Fatalf("HandleResponses() response=%#v stream=%#v err=%v", response, stream, err)
	}
	if calledQuery != "response query" {
		t.Fatalf("called query = %q, want response query", calledQuery)
	}
	output := util.AsMapSlice(response["output"])
	if len(output) != 2 {
		t.Fatalf("output len = %d, want search call + message: %#v", len(output), output)
	}
	if output[0]["type"] != "web_search_call" || output[0]["status"] != "completed" {
		t.Fatalf("search output = %#v", output[0])
	}
	content := output[1]["content"].([]map[string]any)
	if util.Clean(content[0]["text"]) != "Search answer\n\nSources:\n1. Example - https://example.com/article" {
		t.Fatalf("response text = %#v", content[0]["text"])
	}
	annotations := content[0]["annotations"].([]map[string]any)
	if len(annotations) != 1 {
		t.Fatalf("annotations = %#v", annotations)
	}
}

func collectStreamItems(items <-chan map[string]any) []map[string]any {
	out := []map[string]any{}
	for item := range items {
		out = append(out, item)
	}
	return out
}
