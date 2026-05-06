package backend

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpstreamHTTPErrorSummarizesCloudflareChallenge(t *testing.T) {
	err := upstreamHTTPError("bootstrap", 403, []byte(`<html><body><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</body></html>`))
	got := err.Error()
	if !strings.Contains(got, "bootstrap failed: status=403") {
		t.Fatalf("error missing context/status: %q", got)
	}
	if !strings.Contains(got, "upstream returned Cloudflare challenge page") {
		t.Fatalf("error missing challenge summary: %q", got)
	}
	if strings.Contains(got, "<html>") || strings.Contains(got, "window._cf_chl_opt") {
		t.Fatalf("error leaked challenge HTML: %q", got)
	}
}

func TestUpstreamHTTPErrorSummarizesGenericHTML(t *testing.T) {
	err := upstreamHTTPError("auth_models", 502, []byte(`<!doctype html><html><body>bad gateway</body></html>`))
	got := err.Error()
	if got != "auth_models failed: status=502, upstream returned HTML error page" {
		t.Fatalf("upstreamHTTPError() = %q", got)
	}
}

func TestUpstreamHTTPErrorKeepsPlainBodyDetail(t *testing.T) {
	err := upstreamHTTPError("auth_models", 400, []byte(`{"error":"bad request"}`))
	got := err.Error()
	if got != `auth_models failed: status=400, body={"error":"bad request"}` {
		t.Fatalf("upstreamHTTPError() = %q", got)
	}
}

func TestUpstreamTransportErrorSummarizesSurfHandshakeFailure(t *testing.T) {
	err := upstreamTransportError("bootstrap", errString(`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`))
	got := err.Error()
	want := "bootstrap failed: upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy"
	if got != want {
		t.Fatalf("upstreamTransportError() = %q, want %q", got, want)
	}
}

func TestApplyBrowserFingerprintPreservesAccountProfile(t *testing.T) {
	client := &Client{fp: map[string]string{
		"user-agent":     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"impersonate":    "edge101",
		"oai-device-id":  "device-1",
		"oai-session-id": "session-1",
	}}
	client.applyBrowserFingerprint()
	if client.fp["user-agent"] != "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0" {
		t.Fatalf("user-agent = %q", client.fp["user-agent"])
	}
	if client.fp["sec-ch-ua"] != `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"` {
		t.Fatalf("sec-ch-ua = %q", client.fp["sec-ch-ua"])
	}
	if client.fp["sec-ch-ua-full-version"] != `"143.0.0.0"` {
		t.Fatalf("sec-ch-ua-full-version = %q", client.fp["sec-ch-ua-full-version"])
	}
	if client.fp["impersonate"] != "edge101" {
		t.Fatalf("impersonate = %q", client.fp["impersonate"])
	}
	if client.fp["oai-device-id"] != "device-1" || client.fp["oai-session-id"] != "session-1" {
		t.Fatalf("device/session should be preserved: %#v", client.fp)
	}
}

func TestImageHeadersCarryAllRequirementTokens(t *testing.T) {
	client := &Client{BaseURL: "https://chatgpt.com", fp: map[string]string{}, userAgent: browserUserAgent}
	client.applyBrowserFingerprint()
	headers := client.imageHeaders("/backend-api/f/conversation", ChatRequirements{
		Token:          "chat-token",
		ProofToken:     "proof-token",
		TurnstileToken: "turnstile-token",
		SOToken:        "so-token",
	}, "conduit-token", "text/event-stream")
	for key, want := range map[string]string{
		"OpenAI-Sentinel-Chat-Requirements-Token": "chat-token",
		"OpenAI-Sentinel-Proof-Token":             "proof-token",
		"OpenAI-Sentinel-Turnstile-Token":         "turnstile-token",
		"OpenAI-Sentinel-SO-Token":                "so-token",
		"X-Conduit-Token":                         "conduit-token",
	} {
		if got := headers[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if headers["X-Oai-Turn-Trace-Id"] == "" {
		t.Fatal("missing turn trace id")
	}
}

func TestImageModelSlugSupportsSelection(t *testing.T) {
	client := &Client{}
	for _, tc := range []struct {
		model string
		want  string
	}{
		{model: "", want: "gpt-image-2"},
		{model: "auto", want: "gpt-image-2"},
		{model: "gpt-image-2", want: "gpt-image-2"},
		{model: "codex-gpt-image-2", want: "codex-gpt-image-2"},
		{model: "gpt-5", want: "gpt-image-2"},
		{model: "gpt-5-3-mini", want: "gpt-image-2"},
		{model: "gpt-5.4", want: "gpt-image-2"},
		{model: "gpt-5.5", want: "gpt-image-2"},
		{model: "unknown", want: "gpt-image-2"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			if got := client.imageModelSlug(tc.model); got != tc.want {
				t.Fatalf("imageModelSlug(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

func TestConversationPayloadEmbedsOpenAIMessageHistoryInSingleUserMessage(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayload([]map[string]any{
		{"role": "user", "content": "你好，你是什么模型？"},
		{"role": "assistant", "content": "你好！我是一个由OpenAI开发的语言模型，叫做GPT-4。"},
		{"role": "user", "content": "我之前说了什么？"},
	}, "auto", "Asia/Shanghai")

	if payload["parent_message_id"] != "client-created-root" {
		t.Fatalf("parent_message_id = %q, want client-created-root", payload["parent_message_id"])
	}
	messages, ok := payload["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("messages = %T, want []map[string]any", payload["messages"])
	}
	if len(messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(messages))
	}
	author := messages[0]["author"].(map[string]any)
	if author["role"] != "user" {
		t.Fatalf("message role = %q, want user", author["role"])
	}
	content := messages[0]["content"].(map[string]any)
	parts := content["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts length = %d, want 1", len(parts))
	}
	prompt, ok := parts[0].(string)
	if !ok {
		t.Fatalf("prompt = %T, want string", parts[0])
	}
	for _, want := range []string{
		"Conversation history:",
		"User: 你好，你是什么模型？",
		"Assistant: 你好！我是一个由OpenAI开发的语言模型，叫做GPT-4。",
		"Current user message:\n我之前说了什么？",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestConversationPayloadKeepsSingleUserMessagePrompt(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayload([]map[string]any{
		{"role": "user", "content": "hello"},
	}, "auto", "Asia/Shanghai")

	messages := payload["messages"].([]map[string]any)
	content := messages[0]["content"].(map[string]any)
	parts := content["parts"].([]any)
	if parts[0] != "hello" {
		t.Fatalf("prompt = %q, want hello", parts[0])
	}
}

func TestConversationPayloadKeepsSystemHintsEmpty(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayload([]map[string]any{
		{"role": "user", "content": "draw\n\n输出为 16:9 横屏构图"},
	}, "gpt-5.5", "Asia/Shanghai")

	if payload["model"] != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", payload["model"])
	}
	hints, ok := payload["system_hints"].([]any)
	if !ok {
		t.Fatalf("system_hints = %T, want []any", payload["system_hints"])
	}
	if len(hints) != 0 {
		t.Fatalf("system_hints = %#v, want empty", hints)
	}
	messages := payload["messages"].([]map[string]any)
	content := messages[0]["content"].(map[string]any)
	parts := content["parts"].([]any)
	if !strings.Contains(parts[0].(string), "输出为 16:9") {
		t.Fatalf("prompt = %q, want image generation hint", parts[0])
	}
}

func TestSolveTurnstileTokenInterpretsEncodedProgram(t *testing.T) {
	program := `[[3,"ok"]]`
	key := "secret"
	dx := base64.StdEncoding.EncodeToString([]byte(xorTurnstileString(program, key)))
	if got := solveTurnstileToken(dx, key); got != "b2s=" {
		t.Fatalf("solveTurnstileToken() = %q", got)
	}
}

func TestFileDownloadURLUsesConversationScopedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/files/download/file_abc" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("conversation_id"); got != "conv-1" {
			t.Fatalf("conversation_id = %q", got)
		}
		if got := r.URL.Query().Get("inline"); got != "false" {
			t.Fatalf("inline = %q", got)
		}
		if got := r.Header.Get("X-OpenAI-Target-Path"); got != "/backend-api/files/download/file_abc" {
			t.Fatalf("target path = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"download_url":"https://files.example/image.png"}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:     server.URL,
		AccessToken: "token-1",
		httpClient:  server.Client(),
		fp:          map[string]string{},
	}
	got := client.fileDownloadURL(context.Background(), "conv-1", "file_abc")
	if got != "https://files.example/image.png" {
		t.Fatalf("fileDownloadURL() = %q", got)
	}
}

func TestDownloadImageBytesAuthenticatesChatGPTBackendURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Origin"); got != serverURL(r) {
			t.Fatalf("Origin = %q", got)
		}
		if got := r.Header.Get("X-OpenAI-Target-Path"); got != "/backend-api/files/stream" {
			t.Fatalf("target path = %q", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-data"))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:     server.URL,
		AccessToken: "token-1",
		httpClient:  server.Client(),
		fp:          map[string]string{},
		userAgent:   browserUserAgent,
	}
	got, err := client.DownloadImageBytes(context.Background(), []string{server.URL + "/backend-api/files/stream"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != "png-data" {
		t.Fatalf("DownloadImageBytes() = %#v", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
