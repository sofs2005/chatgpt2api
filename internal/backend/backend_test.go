package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ptrInt(value int) *int {
	return &value
}

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

func TestBuildResponsesImageRequestPreservesStructuredToolFields(t *testing.T) {
	request := ResponsesImageRequest{
		Prompt:            "生成封面",
		Model:             "gpt-image-2",
		Size:              "2048x2048",
		Quality:           "high",
		Background:        "transparent",
		OutputFormat:      "webp",
		OutputCompression: ptrInt(37),
		PartialImages:     ptrInt(2),
		InputImages: []ResponsesInputImage{
			{Data: []byte("png-bytes"), ContentType: "image/png"},
		},
		InputImageMask: &ResponsesInputImage{Data: []byte("mask-bytes"), ContentType: "image/png"},
	}

	payload, err := buildResponsesImagePayload(request)
	if err != nil {
		t.Fatalf("buildResponsesImagePayload() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("payload json error = %v", err)
	}

	if body["model"] != ResponsesImageMainModel {
		t.Fatalf("main model = %#v, want %q", body["model"], ResponsesImageMainModel)
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	for key, want := range map[string]any{
		"type":               "image_generation",
		"action":             "edit",
		"size":               "2048x2048",
		"quality":            "high",
		"background":         "transparent",
		"output_format":      "webp",
		"output_compression": float64(37),
		"partial_images":     float64(2),
	} {
		if got := tool[key]; got != want {
			t.Fatalf("tool[%s] = %#v, want %#v in %#v", key, got, want, tool)
		}
	}
	if _, ok := tool["model"]; ok {
		t.Fatalf("tool model = %#v, want omitted for official gpt-image-2 route", tool["model"])
	}
	mask := tool["input_image_mask"].(map[string]any)
	if got := mask["image_url"].(string); !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("mask image_url = %q", got)
	}
	input := body["input"].([]any)[0].(map[string]any)
	content := input["content"].([]any)
	if len(content) != 2 || content[0].(map[string]any)["type"] != "input_text" || content[1].(map[string]any)["type"] != "input_image" {
		t.Fatalf("input content = %#v", content)
	}
}

func TestBuildResponsesImageRequestMapsCodexAliasToCodexToolModel(t *testing.T) {
	payload, err := buildResponsesImagePayload(ResponsesImageRequest{
		Prompt: "生成封面",
		Model:  "codex-gpt-image-2",
	})
	if err != nil {
		t.Fatalf("buildResponsesImagePayload() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("payload json error = %v", err)
	}
	if body["model"] != ResponsesImageMainModel {
		t.Fatalf("main model = %#v, want %q", body["model"], ResponsesImageMainModel)
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	if got := tool["model"]; got != ResponsesImageCodexToolModel {
		t.Fatalf("tool model = %#v, want %q in %#v", got, ResponsesImageCodexToolModel, tool)
	}
}

func TestBuildResponsesImageRequestNormalizesToolSize(t *testing.T) {
	tests := []struct {
		name string
		size string
		want any
	}{
		{name: "auto omitted", size: "auto", want: nil},
		{name: "square aspect ratio becomes codex size", size: "1:1", want: "1024x1024"},
		{name: "landscape aspect ratio becomes codex size", size: "3:2", want: "1536x1024"},
		{name: "portrait aspect ratio becomes codex size", size: "2:3", want: "1024x1536"},
		{name: "x separated ratio becomes codex size", size: "16x9", want: "1536x864"},
		{name: "1080p tier becomes valid multiple", size: "1080x1080", want: "1088x1088"},
		{name: "oversized dimensions are clamped", size: "8192x8192", want: "2880x2880"},
		{name: "unknown size omitted", size: "poster", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := buildResponsesImagePayload(ResponsesImageRequest{
				Prompt: "生成封面",
				Size:   tt.size,
			})
			if err != nil {
				t.Fatalf("buildResponsesImagePayload() error = %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(payload, &body); err != nil {
				t.Fatalf("payload json error = %v", err)
			}
			tool := body["tools"].([]any)[0].(map[string]any)
			if got := tool["size"]; got != tt.want {
				t.Fatalf("tool size = %#v, want %#v in %#v", got, tt.want, tool)
			}
		})
	}
}

func TestNormalizeResponsesImageToolModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "empty official default is omitted", model: "", want: ""},
		{name: "auto official default is omitted", model: "auto", want: ""},
		{name: "gpt image 2 official default is omitted", model: "gpt-image-2", want: ""},
		{name: "codex alias maps to codex model", model: "codex-gpt-image-2", want: ResponsesImageCodexToolModel},
		{name: "explicit codex upstream model is preserved", model: "gpt-5.4-mini", want: "gpt-5.4-mini"},
		{name: "unknown values are omitted", model: "unknown", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeResponsesImageToolModel(tt.model); got != tt.want {
				t.Fatalf("normalizeResponsesImageToolModel(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestResponsesImageHeadersUseCodexRoute(t *testing.T) {
	client := &Client{AccessToken: "token-1", fp: map[string]string{}, userAgent: browserUserAgent, sessionID: "session-1"}
	headers, err := client.responsesImageHeaders("acct-1")
	if err != nil {
		t.Fatalf("responsesImageHeaders() error = %v", err)
	}
	if headers["Authorization"] != "Bearer token-1" {
		t.Fatalf("Authorization = %q", headers["Authorization"])
	}
	if headers["Chatgpt-Account-Id"] != "acct-1" {
		t.Fatalf("Chatgpt-Account-Id = %q", headers["Chatgpt-Account-Id"])
	}
	if headers["Originator"] != "codex-tui" || headers["Accept"] != "text/event-stream" {
		t.Fatalf("codex headers = %#v", headers)
	}
	if !strings.Contains(headers["User-Agent"], "codex-tui/0.128.0") || !strings.Contains(headers["User-Agent"], "iTerm.app") {
		t.Fatalf("User-Agent = %q, want Codex TUI user agent", headers["User-Agent"])
	}
	if headers["Session_id"] == "" {
		t.Fatalf("Session_id missing in %#v", headers)
	}
}

func TestStreamResponsesImageUsesCodexResponsesRouteForCodexModel(t *testing.T) {
	imageB64 := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	var seenPath string
	var seenToolModel any
	var seenUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenUserAgent = r.Header.Get("User-Agent")
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Errorf("Authorization = %q, want Bearer token-1", got)
		}
		if got := r.Header.Get("Chatgpt-Account-Id"); got != "acct-1" {
			t.Errorf("Chatgpt-Account-Id = %q, want acct-1", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		} else {
			tool := body["tools"].([]any)[0].(map[string]any)
			seenToolModel = tool["model"]
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"type":"image_generation_call","result":"` + imageB64 + `","output_format":"png"}}

`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:     server.URL,
		AccessToken: "token-1",
		httpClient:  server.Client(),
		lookup: testAccountLookup{
			"token-1": {"chatgpt_account_id": "acct-1"},
		},
	}
	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
		Model:  "codex-gpt-image-2",
	})
	var gotResult string
	for event := range events {
		gotResult = event.Result
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if seenPath != codexResponsesPath {
		t.Fatalf("path = %q, want %q", seenPath, codexResponsesPath)
	}
	if seenToolModel != ResponsesImageCodexToolModel {
		t.Fatalf("tool model = %#v, want %q", seenToolModel, ResponsesImageCodexToolModel)
	}
	if !strings.Contains(seenUserAgent, "codex-tui/0.128.0") {
		t.Fatalf("User-Agent = %q, want Codex TUI", seenUserAgent)
	}
	if gotResult != imageB64 {
		t.Fatalf("result = %q, want %q", gotResult, imageB64)
	}
}

type testAccountLookup map[string]map[string]any

func (l testAccountLookup) GetAccount(accessToken string) map[string]any {
	return l[accessToken]
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

type errString string

func (e errString) Error() string { return string(e) }
