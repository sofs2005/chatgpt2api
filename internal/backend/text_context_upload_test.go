package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt2api/internal/contextoffload"
)

func TestUploadTextContextFileUsesChatGPTFileFlow(t *testing.T) {
	var createBody map[string]any
	var processBody map[string]any
	var putHeaders http.Header
	var putBody string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == textFileCreatePath:
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":     "success",
				"upload_url": server.URL + "/blob/raw",
				"file_id":    "file_context",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/blob/raw":
			putHeaders = r.Header.Clone()
			data, _ := io.ReadAll(r.Body)
			putBody = string(data)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == textFileProcessPath:
			if err := json.NewDecoder(r.Body).Decode(&processBody); err != nil {
				t.Fatalf("decode process body: %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = io.WriteString(w, `data: {"event":"file.processing.started","progress":0}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"event":"file.processing.file_ready","progress":20}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"event":"file.indexing.completed","progress":null}`+"\n\n")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, AccessToken: "token", httpClient: server.Client()}
	ref, err := client.uploadTextContextFile(context.Background(), contextoffload.File{
		Filename:    "history.txt",
		ContentType: "text/plain",
		Text:        "hello context",
	}, ChatRequirements{Token: "req-token"}, time.Second)
	if err != nil {
		t.Fatalf("uploadTextContextFile() error = %v", err)
	}

	if ref.FileID != "file_context" || ref.FileName != "history.txt" || ref.MIMEType != "text/plain" || ref.Size != len("hello context") {
		t.Fatalf("ref = %#v", ref)
	}
	if createBody["file_name"] != "history.txt" || createBody["mime_type"] != "text/plain" || createBody["use_case"] != "my_files" {
		t.Fatalf("unexpected create body: %#v", createBody)
	}
	if createBody["store_in_library"] != true || createBody["library_persistence_mode"] != "opportunistic" {
		t.Fatalf("create body missing library fields: %#v", createBody)
	}
	if putBody != "hello context" {
		t.Fatalf("put body = %q", putBody)
	}
	if putHeaders.Get("Content-Type") != "text/plain" || putHeaders.Get("x-ms-blob-type") != "BlockBlob" || putHeaders.Get("x-ms-version") != "2020-04-08" {
		t.Fatalf("unexpected put headers: %#v", putHeaders)
	}
	if processBody["file_id"] != "file_context" || processBody["index_for_retrieval"] != true || processBody["entry_surface"] != "chat_composer" {
		t.Fatalf("unexpected process body: %#v", processBody)
	}
	metadata, ok := processBody["metadata"].(map[string]any)
	if !ok || metadata["store_in_library"] != true || metadata["is_temporary_chat"] != false {
		t.Fatalf("unexpected process metadata: %#v", processBody["metadata"])
	}
}

func TestTextConversationPayloadsIncludeAttachments(t *testing.T) {
	var prepareBody map[string]any
	var startBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			if err := json.NewDecoder(r.Body).Decode(&prepareBody); err != nil {
				t.Fatalf("decode prepare body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"conduit_token": "conduit"})
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			if err := json.NewDecoder(r.Body).Decode(&startBody); err != nil {
				t.Fatalf("decode start body: %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, AccessToken: "token", httpClient: server.Client()}
	attachments := []TextAttachmentRef{{FileID: "file_context", FileName: "history.txt", MIMEType: "text/plain", Size: 12}}
	conduit, err := client.prepareTextConversation(context.Background(), []map[string]any{{"role": "user", "content": "short prompt"}}, ChatRequirements{Token: "req-token"}, "gpt-5", attachments)
	if err != nil {
		t.Fatalf("prepareTextConversation() error = %v", err)
	}
	if conduit != "conduit" {
		t.Fatalf("conduit = %q, want conduit", conduit)
	}
	resp, err := client.startTextConversation(context.Background(), []map[string]any{{"role": "user", "content": "short prompt"}}, ChatRequirements{Token: "req-token"}, conduit, "gpt-5", attachments)
	if err != nil {
		t.Fatalf("startTextConversation() error = %v", err)
	}
	resp.Body.Close()

	prepareAttachments := prepareBody["attachments"].([]any)
	if len(prepareAttachments) != 1 || prepareAttachments[0].(map[string]any)["file_id"] != "file_context" {
		t.Fatalf("prepare attachments = %#v", prepareBody["attachments"])
	}
	messages := startBody["messages"].([]any)
	metadata := messages[0].(map[string]any)["metadata"].(map[string]any)
	messageAttachments := metadata["attachments"].([]any)
	if len(messageAttachments) != 1 {
		t.Fatalf("message attachments = %#v", metadata["attachments"])
	}
	attachment := messageAttachments[0].(map[string]any)
	if attachment["id"] != "file_context" || attachment["mimeType"] != "text/plain" || attachment["name"] != "history.txt" {
		t.Fatalf("message attachment = %#v", attachment)
	}
}

func TestWaitTextContextIndexingRequiresCompletedEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := waitTextContextIndexing(ctx, strings.NewReader(`data: {"event":"file.processing.file_ready"}`+"\n\n"))
	if err == nil {
		t.Fatalf("waitTextContextIndexing() error = nil, want missing completed error")
	}
}

func TestWaitTextContextIndexingDetectsCompletedEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := waitTextContextIndexing(ctx, strings.NewReader(`data: {"event":"file.processing.file_ready"}`+"\n\n"+`data: {"event":"file.indexing.completed"}`+"\n\n"))
	if err != nil {
		t.Fatalf("waitTextContextIndexing() error = %v", err)
	}
}
