package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientSearchRunsOfficialSearchFlow(t *testing.T) {
	prepareSeen := false
	requirementsSeen := false
	streamSeen := false
	conversationFetches := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			prepareSeen = true
			if got := r.Header.Get("X-Conduit-Token"); got != "no-token" {
				t.Fatalf("prepare X-Conduit-Token = %q, want no-token", got)
			}
			if got := r.Header.Get("Sec-Ch-Ua"); got == "" {
				t.Fatalf("prepare missing browser fingerprint header")
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode prepare payload: %v", err)
			}
			if got := payload["parent_message_id"]; got != "client-created-root" {
				t.Fatalf("prepare parent_message_id = %#v", got)
			}
			if got := payload["model"]; got != searchModel {
				t.Fatalf("prepare model = %#v, want %q", got, searchModel)
			}
			hints, ok := payload["system_hints"].([]any)
			if !ok || len(hints) != 1 || hints[0] != "search" {
				t.Fatalf("prepare system_hints = %#v, want [search]", payload["system_hints"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			requirementsSeen = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			streamSeen = true
			if got := r.Header.Get("X-Conduit-Token"); got != "conduit-token" {
				t.Fatalf("stream X-Conduit-Token = %q, want conduit-token", got)
			}
			if got := r.Header.Get("OpenAI-Sentinel-Chat-Requirements-Token"); got != "req-token" {
				t.Fatalf("stream requirements token = %q, want req-token", got)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode stream payload: %v", err)
			}
			if got := payload["parent_message_id"]; got != "client-created-root" {
				t.Fatalf("stream parent_message_id = %#v", got)
			}
			if got, _ := payload["force_use_search"].(bool); !got {
				t.Fatalf("stream force_use_search = %#v, want true", payload["force_use_search"])
			}
			if got := payload["client_reported_search_source"]; got != "conversation_composer_web_icon" {
				t.Fatalf("stream search source = %#v", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"conversation_id\":\"conv-search\"}\n\ndata: [DONE]\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-search":
			conversationFetches++
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "__Secure-next-auth.session-token=session-1") {
				t.Fatalf("conversation Cookie = %q, want session cookie", got)
			}
			if got := r.Header.Get("Sec-Ch-Ua"); got == "" {
				t.Fatalf("conversation missing browser fingerprint header")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"mapping": {
					"old": {"message": {"id":"old-msg","author":{"role":"assistant"},"create_time":1,"content":{"content_type":"text","parts":["old"]}}},
					"latest": {"message": {"id":"msg-2","author":{"role":"assistant"},"create_time":2,"metadata":{"finish_details":{"type":"finished_successfully"},"citations":[{"title":"Example","url":"https://example.com/article","snippet":"snippet","type":"webpage"}]},"content":{"content_type":"text","parts":["Answer with https://extra.example/path."]}}}
				}
			}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	client.sessionCookies = map[string]string{"__Secure-next-auth.session-token": "session-1"}
	client.SetSearchPollOptions(500*time.Millisecond, 5*time.Millisecond)

	result, err := client.Search(context.Background(), "latest news")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !prepareSeen || !requirementsSeen || !streamSeen {
		t.Fatalf("flow seen prepare=%v requirements=%v stream=%v", prepareSeen, requirementsSeen, streamSeen)
	}
	if conversationFetches != 1 {
		t.Fatalf("conversation fetches = %d, want 1", conversationFetches)
	}
	if result.ConversationID != "conv-search" || result.AssistantMessageID != "msg-2" || result.Status != "finished_successfully" {
		t.Fatalf("result metadata = %#v", result)
	}
	if result.Answer != "Answer with https://extra.example/path." {
		t.Fatalf("answer = %q", result.Answer)
	}
	if len(result.Sources) != 2 {
		t.Fatalf("sources len = %d, want 2: %#v", len(result.Sources), result.Sources)
	}
	if result.Sources[0].Title != "Example" || result.Sources[0].URL != "https://example.com/article" || result.Sources[0].Snippet != "snippet" || result.Sources[0].SourceType != "webpage" {
		t.Fatalf("first source = %#v", result.Sources[0])
	}
	if result.Sources[1].URL != "https://extra.example/path" {
		t.Fatalf("regex source = %#v", result.Sources[1])
	}
}

func TestExtractSearchResultReadsStringContent(t *testing.T) {
	result := extractSearchResult("conv-text", map[string]any{
		"mapping": map[string]any{
			"latest": map[string]any{"message": map[string]any{
				"id":          "msg-string",
				"author":      map[string]any{"role": "assistant"},
				"create_time": float64(3),
				"metadata":    map[string]any{"status": "in_progress"},
				"content":     "string answer",
			}},
		},
	})
	if result.Answer != "string answer" {
		t.Fatalf("answer = %q, want string answer", result.Answer)
	}
}
