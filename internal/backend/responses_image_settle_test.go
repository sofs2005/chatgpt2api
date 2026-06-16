package backend

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const settleTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="

// 二次确认开启时，poll 必须看到相同的 file_ids 连续命中两次（首轮 settle，再轮确认）才返回。
func TestResolveOfficialImageResultsDoubleConfirmsWithSettle(t *testing.T) {
	imageBytes, err := base64.StdEncoding.DecodeString(settleTestPNG)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}

	conversationFetches := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-settle":
			conversationFetches++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{"node-1":{"message":{"author":{"role":"tool"},"metadata":{"async_task_type":"image_gen"},"content":{"content_type":"multimodal_text","parts":[{"content_type":"image_asset_pointer","asset_pointer":"file-service://file_ready"}]}}}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_ready":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/file_ready.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/file_ready.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	client.SetImagePollOptions(true, true, 5*time.Millisecond)

	results, err := client.resolveOfficialImageResults(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
	}, ResponsesImageEvent{
		ConversationID: "conv-settle",
	})
	if err != nil {
		t.Fatalf("resolveOfficialImageResults() error = %v", err)
	}
	if conversationFetches != 2 {
		t.Fatalf("conversation fetches = %d, want 2 (settle double-confirm)", conversationFetches)
	}
	if len(results) != 1 || results[0].Result != settleTestPNG {
		t.Fatalf("results = %#v, want one confirmed image", results)
	}
}

// 关闭 settle 与 check_before_hit 且 SSE 已带 id 时，跳过会话轮询直接解析下载。
func TestResolveOfficialImageResultsSkipsPollWhenSettleAndCheckDisabled(t *testing.T) {
	imageBytes, err := base64.StdEncoding.DecodeString(settleTestPNG)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-skip":
			t.Fatalf("conversation endpoint polled despite skip-poll optimization")
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_ready":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/file_ready.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/file_ready.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	client.SetImagePollOptions(false, false, 0)

	results, err := client.resolveOfficialImageResults(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
	}, ResponsesImageEvent{
		ConversationID: "conv-skip",
		FileIDs:        []string{"file_ready"},
	})
	if err != nil {
		t.Fatalf("resolveOfficialImageResults() error = %v", err)
	}
	if len(results) != 1 || results[0].Result != settleTestPNG {
		t.Fatalf("results = %#v, want one directly-resolved image", results)
	}
}
