package protocol

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

type testProtocolImageConfig struct {
	root           string
	settleEnabled  bool
	checkBeforeHit bool
	settleSecs     float64
}

func (c testProtocolImageConfig) ImageSettleEnabled() bool {
	return c.settleEnabled
}

func (c testProtocolImageConfig) ImageCheckBeforeHitEnabled() bool {
	return c.checkBeforeHit
}

func (c testProtocolImageConfig) ImageSettleSecs() float64 {
	return c.settleSecs
}

type testProtocolProxyConfig struct{}

func (testProtocolProxyConfig) Proxy() string { return "" }

func (c testProtocolImageConfig) ImagesDir() string {
	path := filepath.Join(c.root, "images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testProtocolImageConfig) ImageMetadataDir() string {
	path := filepath.Join(c.root, "image_metadata")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testProtocolImageConfig) BaseURL() string {
	return "https://example.test"
}

func testPNGDataURL(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestResponsesInputImageKeepsRemoteURLAndRawBase64(t *testing.T) {
	remote := responsesInputImage("https://example.test/input.png")
	if remote.URL != "https://example.test/input.png" || len(remote.Data) != 0 {
		t.Fatalf("remote responsesInputImage() = %#v, want URL without data", remote)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte("raw image"))
	raw := responsesInputImage(encoded)
	if string(raw.Data) != "raw image" || raw.ContentType != "image/png" || raw.URL != "" {
		t.Fatalf("raw responsesInputImage() = %#v, want decoded png data", raw)
	}
}

func TestCountMessageTokensCountsTextContentParts(t *testing.T) {
	messages := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "text", "text": "again"},
		},
	}}

	got := CountMessageTokens(messages, "gpt-5")
	want := 3 + 3 + CountTextTokens("user", "gpt-5") + CountTextTokens("look", "gpt-5") + CountTextTokens("again", "gpt-5")
	if got != want {
		t.Fatalf("CountMessageTokens() = %d, want %d", got, want)
	}
}

func TestCountMessageTokensCountsImageURLLowDetail(t *testing.T) {
	messages := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/image.png", "detail": "low"}},
		},
	}}

	got := CountMessageTokens(messages, "gpt-5")
	base := 3 + 3 + CountTextTokens("user", "gpt-5") + CountTextTokens("look", "gpt-5")
	want := base + 85
	if got != want {
		t.Fatalf("CountMessageTokens() = %d, want %d", got, want)
	}
}

func TestCountMessageTokensImageURLTopLevelDetailFallback(t *testing.T) {
	messages := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "image_url", "detail": "low", "image_url": map[string]any{"url": "https://example.test/image.png"}},
		},
	}}

	got := CountMessageTokens(messages, "gpt-5")
	base := 3 + 3 + CountTextTokens("user", "gpt-5")
	want := base + 85
	if got != want {
		t.Fatalf("CountMessageTokens() = %d, want %d", got, want)
	}
}

func TestCountMessageTokensCountsImageURLDataURLDimensions(t *testing.T) {
	messages := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": testPNGDataURL(t, 1, 1), "detail": "high"}},
		},
	}}

	got := CountMessageTokens(messages, "gpt-5")
	base := 3 + 3 + CountTextTokens("user", "gpt-5") + CountTextTokens("look", "gpt-5")
	want := base + 255
	if got != want {
		t.Fatalf("CountMessageTokens() = %d, want %d", got, want)
	}
}

func TestCompletionResponseIncludesImagePromptTokens(t *testing.T) {
	messages := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": testPNGDataURL(t, 1, 1), "detail": "high"}},
		},
	}}

	response := CompletionResponse("gpt-5", "ok", 123, messages)
	usage := response["usage"].(map[string]any)
	promptTokens := usage["prompt_tokens"].(int)
	completionTokens := usage["completion_tokens"].(int)
	totalTokens := usage["total_tokens"].(int)

	wantPrompt := CountMessageTokens(messages, "gpt-5")
	wantCompletion := CountTextTokens("ok", "gpt-5")
	if promptTokens != wantPrompt {
		t.Fatalf("prompt_tokens = %d, want %d", promptTokens, wantPrompt)
	}
	if completionTokens != wantCompletion {
		t.Fatalf("completion_tokens = %d, want %d", completionTokens, wantCompletion)
	}
	if totalTokens != wantPrompt+wantCompletion {
		t.Fatalf("total_tokens = %d, want %d", totalTokens, wantPrompt+wantCompletion)
	}
}

func TestTokenCountMessagesPreservesContentPartsAndPrependsToolPrompt(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/image.png", "detail": "low"}},
		}}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}},
	}

	messages := TokenCountMessages(body["messages"], ChatToolPrompt(body))
	if len(messages) != 2 {
		t.Fatalf("TokenCountMessages() len = %d, want 2: %#v", len(messages), messages)
	}
	if messages[0]["role"] != "system" || !strings.Contains(messages[0]["content"].(string), "Bridge-call slots available: bridge-0") {
		t.Fatalf("system tool prompt not prepended: %#v", messages[0])
	}
	if messages[1]["role"] != "user" {
		t.Fatalf("default role = %#v, want user", messages[1]["role"])
	}
	parts, ok := messages[1]["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content parts were not preserved: %#v", messages[1]["content"])
	}
	imagePart, ok := parts[1].(map[string]any)
	if !ok || imagePart["type"] != "image_url" {
		t.Fatalf("image_url part was not preserved: %#v", parts[1])
	}

	normalized := NormalizeMessages(body["messages"], ChatToolPrompt(body))
	if normalized[1]["content"] != "look" {
		t.Fatalf("NormalizeMessages() content = %#v, want text-only look", normalized[1]["content"])
	}
}

func TestCompletionResponseIncludesImagePromptTokensWithTokenCountMessages(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5",
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": testPNGDataURL(t, 1, 1), "detail": "high"}},
		}}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}},
	}
	rawMessages, err := ChatMessagesFromBody(body)
	if err != nil {
		t.Fatalf("ChatMessagesFromBody() error = %v", err)
	}
	usageMessages := TokenCountMessages(rawMessages, ChatToolPrompt(body))
	normalizedMessages := NormalizeMessages(rawMessages, ChatToolPrompt(body))

	response, err := CompletionResponseWithTools("gpt-5", "ok", 123, usageMessages, body["tools"], body["tool_choice"])
	if err != nil {
		t.Fatalf("CompletionResponseWithTools() error = %v", err)
	}
	usage := response["usage"].(map[string]any)
	promptTokens := usage["prompt_tokens"].(int)
	wantPrompt := CountMessageTokens(usageMessages, "gpt-5")
	if promptTokens != wantPrompt {
		t.Fatalf("prompt_tokens = %d, want %d", promptTokens, wantPrompt)
	}
	if promptTokens <= CountMessageTokens(normalizedMessages, "gpt-5") {
		t.Fatalf("prompt_tokens = %d, want greater than normalized text-only count", promptTokens)
	}
}

func TestCountMessageTokensCountsHighDetailDataURLAfterShortSideScaling(t *testing.T) {
	messages := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": testPNGDataURL(t, 2048, 2048), "detail": "high"}},
		},
	}}

	got := CountMessageTokens(messages, "gpt-5")
	base := 3 + 3 + CountTextTokens("user", "gpt-5")
	want := base + 765
	if got != want {
		t.Fatalf("CountMessageTokens() = %d, want %d", got, want)
	}
}

func TestCountMessageTokensImageURLFallbackDoesNotFetchRemote(t *testing.T) {
	messages := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://127.0.0.1:1/not-fetched.png"}},
		},
	}}

	got := CountMessageTokens(messages, "gpt-5")
	base := 3 + 3 + CountTextTokens("user", "gpt-5")
	want := base + 765
	if got != want {
		t.Fatalf("CountMessageTokens() = %d, want %d", got, want)
	}
}

func TestCountMessageTokensIgnoresUnknownContentParts(t *testing.T) {
	messages := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "file", "file": map[string]any{"file_id": "file_123", "size": 1024}},
		},
	}}

	got := CountMessageTokens(messages, "gpt-5")
	want := 3 + 3 + CountTextTokens("user", "gpt-5")
	if got != want {
		t.Fatalf("CountMessageTokens() = %d, want %d", got, want)
	}
}

func TestFormatImageResultStoresOwnerName(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))

	result := engine.FormatImageResult(
		[]map[string]any{{"b64_json": imageData}},
		"draw",
		"url",
		"https://example.test",
		"linuxdo:41499",
		"Cassianvale",
		123,
		"",
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResult() data = %#v", result["data"])
	}
	imageURL, _ := items[0]["url"].(string)
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	if rel == imageURL || rel == "" {
		t.Fatalf("image url = %q", imageURL)
	}

	data, err := os.ReadFile(filepath.Join(config.ImageMetadataDir(), filepath.FromSlash(rel)+".json"))
	if err != nil {
		t.Fatalf("ReadFile(metadata) error = %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("Unmarshal(metadata) error = %v", err)
	}
	if meta["owner_id"] != "linuxdo:41499" || meta["owner_name"] != "Cassianvale" {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestFormatImageResultEncodesRequestedOutputFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(0, 1, color.NRGBA{B: 255, A: 255})
	src.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	compression := 25

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes())}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "jpeg", Compression: &compression},
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "jpeg" {
		t.Fatalf("output_format = %#v, want jpeg", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".jpg") {
		t.Fatalf("image url = %q, want .jpg suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	converted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(converted))
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("decoded format = %q, want jpeg", format)
	}
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	if _, err := os.Stat(filepath.Join(config.ImagesDir(), filepath.FromSlash(rel))); err != nil {
		t.Fatalf("stored jpeg missing: %v", err)
	}
}

func TestFormatImageResultRequestedFormatOverridesUpstreamFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(0, 1, color.NRGBA{B: 255, A: 255})
	src.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	compression := 30

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{
			"b64_json":      base64.StdEncoding.EncodeToString(encoded.Bytes()),
			"output_format": "png",
		}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "jpeg", Compression: &compression},
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "jpeg" {
		t.Fatalf("output_format = %#v, want requested jpeg", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".jpg") {
		t.Fatalf("image url = %q, want .jpg suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	converted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(converted))
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("decoded format = %q, want jpeg", format)
	}
}

func TestFormatImageResultTrustsCodexUpstreamOutputFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	upstreamBytes := []byte("RIFF\x10\x00\x00\x00WEBPcodex-upstream-bytes")
	compression := 40
	options := imageResultOutputOptions(
		ConversationRequest{Model: "codex-gpt-image-2", OutputFormat: "jpeg", OutputCompression: &compression},
		backend.ResponsesImageEvent{OutputFormat: "webp"},
	)
	if !options.TrustUpstreamFormat {
		t.Fatal("Codex result options should trust upstream format")
	}
	if options.Format != "webp" {
		t.Fatalf("Codex result format = %q, want upstream webp", options.Format)
	}
	if options.Compression != nil {
		t.Fatalf("Codex result compression = %#v, want nil", *options.Compression)
	}

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{
			"b64_json":      base64.StdEncoding.EncodeToString(upstreamBytes),
			"output_format": "webp",
		}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		options,
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "webp" {
		t.Fatalf("output_format = %#v, want upstream webp", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".webp") {
		t.Fatalf("image url = %q, want .webp suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	returnedBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if !bytes.Equal(returnedBytes, upstreamBytes) {
		t.Fatalf("returned bytes = %q, want upstream bytes %q", returnedBytes, upstreamBytes)
	}
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	storedBytes, err := os.ReadFile(filepath.Join(config.ImagesDir(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("ReadFile(stored image) error = %v", err)
	}
	if !bytes.Equal(storedBytes, upstreamBytes) {
		t.Fatalf("stored bytes = %q, want upstream bytes %q", storedBytes, upstreamBytes)
	}
}

func TestFormatImageResultIgnoresWebPOutputCompression(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(0, 1, color.NRGBA{B: 255, A: 255})
	src.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	compression := 90

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{
			"b64_json":           base64.StdEncoding.EncodeToString(encoded.Bytes()),
			"output_format":      "webp",
			"output_compression": 10,
		}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "webp", Compression: &compression},
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "webp" {
		t.Fatalf("output_format = %#v, want webp", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".webp") {
		t.Fatalf("image url = %q, want .webp suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	converted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	headerLen := min(len(converted), 12)
	header := converted[:headerLen]
	if !bytes.HasPrefix(header, []byte("RIFF")) || !bytes.Contains(header, []byte("WEBP")) {
		t.Fatalf("converted bytes are not webp: %x", header)
	}
}

func TestImageStreamErrorMessage(t *testing.T) {
	cloudflare := `bootstrap failed: status=403, body=<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`
	if got := imageStreamErrorMessage(cloudflare); got != "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy" {
		t.Fatalf("cloudflare challenge error = %q", got)
	}

	cases := []string{
		"curl: (35) OpenSSL SSL_connect: SSL_ERROR_SYSCALL",
		"TLS connect error: connection reset by peer",
		"error: OPENSSL_INTERNAL:WRONG_VERSION_NUMBER",
		`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`,
	}
	for _, input := range cases {
		if got := imageStreamErrorMessage(input); got != "upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy" {
			t.Fatalf("imageStreamErrorMessage(%q) = %q", input, got)
		}
	}
	if got := imageStreamErrorMessage("upstream returned 500"); got != "upstream returned 500" {
		t.Fatalf("non-connection error = %q", got)
	}
	flowControl := "connection error: FLOW_CONTROL_ERROR"
	if got := imageStreamErrorMessage(flowControl); got != "upstream image stream interrupted by HTTP/2 flow control; retry the request or change proxy if it repeats" {
		t.Fatalf("flow control error = %q", got)
	}
	if got := imageStreamErrorMessage(""); got != "upstream image request failed without error detail" {
		t.Fatalf("empty error = %q", got)
	}
}

func TestHandleImageGenerationsReturnsUpstreamTextResponse(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: "你好！我是 ChatGPT。", UpstreamEventType: "image_text_response"}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	result, _, err := engine.HandleImageGenerations(context.Background(), map[string]any{
		"prompt": "你好，你是什么模型？",
		"model":  "gpt-image-2",
	})
	if err == nil {
		t.Fatal("HandleImageGenerations() error = nil, want text-response image error")
	}
	var imageErr *ImageGenerationError
	if !errors.As(err, &imageErr) {
		t.Fatalf("HandleImageGenerations() error = %T %v, want ImageGenerationError", err, err)
	}
	if imageErr.Code != "image_generation_text_response" || imageErr.Message != "你好！我是 ChatGPT。" {
		t.Fatalf("image error = %#v", imageErr)
	}
	if result["output_type"] != "text" {
		t.Fatalf("output_type = %#v, want text in %#v", result["output_type"], result)
	}
	if result["message"] != "你好！我是 ChatGPT。" {
		t.Fatalf("message = %#v, want upstream text", result["message"])
	}
}

func TestHandleImageGenerationsReturnsArbitraryUpstreamImageText(t *testing.T) {
	const upstreamText = "上游返回的任何非排队文本都应该原样返回。"
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: upstreamText, UpstreamEventType: "image_text_response"}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	result, _, err := engine.HandleImageGenerations(context.Background(), map[string]any{
		"prompt": "draw",
		"model":  "gpt-image-2",
	})
	if err == nil {
		t.Fatal("HandleImageGenerations() error = nil, want text-response image error")
	}
	if result["output_type"] != "text" || result["message"] != upstreamText {
		t.Fatalf("result = %#v, want arbitrary upstream text response", result)
	}
}

func TestRunSingleImageOutputReportsProgressSteps(t *testing.T) {
	var mu sync.Mutex
	var steps []string
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"b64_json": "image"}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}
	req := ConversationRequest{Prompt: "draw", Model: "gpt-image-2", N: 1}
	req.ReportStep = func(step string) {
		mu.Lock()
		steps = append(steps, step)
		mu.Unlock()
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), req)
	if _, err := engine.CollectImageOutputs(outputs, errCh); err != nil {
		t.Fatalf("CollectImageOutputs() error = %v", err)
	}

	mu.Lock()
	got := append([]string(nil), steps...)
	mu.Unlock()
	// 对齐上游 progress_callback 的步骤序列。
	want := []string{"getting_account", "image_stream_resolve_start", "receiving_image"}
	if len(got) != len(want) {
		t.Fatalf("progress steps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestRunSingleImageOutputAttachesConversationIDToTimeoutError(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		// 先发出带 conversation_id 的进度事件，再以超时错误结束，模拟生图轮询超时后可续轮询的场景。
		out <- ImageOutput{Kind: "progress", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), ConversationID: "conv-timeout-123", UpstreamEventType: "image_generation"}
		close(out)
		errCh <- errors.New("context deadline exceeded")
		close(errCh)
		return out, errCh
	}
	req := ConversationRequest{Prompt: "draw", Model: "gpt-image-2", N: 1}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), req)
	for range outputs {
	}
	err := <-errCh
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	var imageErr *ImageGenerationError
	if !errors.As(err, &imageErr) {
		t.Fatalf("error = %T %v, want *ImageGenerationError", err, err)
	}
	if imageErr.ConversationID != "conv-timeout-123" {
		t.Fatalf("ImageGenerationError.ConversationID = %q, want conv-timeout-123", imageErr.ConversationID)
	}
	if got := imageErr.ImageConversationID(); got != "conv-timeout-123" {
		t.Fatalf("ImageConversationID() = %q, want conv-timeout-123", got)
	}
}

func testPNGBase64(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestEngineImageResumeTokenRememberAndTake(t *testing.T) {
	engine := &Engine{}
	engine.rememberImageResumeToken("conv-token", "tok-abc")

	token, ok := engine.takeImageResumeToken("conv-token")
	if !ok || token != "tok-abc" {
		t.Fatalf("takeImageResumeToken() = %q, %v, want tok-abc, true", token, ok)
	}
	// 取出即消费：再次取出应失败，避免令牌被无限复用。
	if _, ok := engine.takeImageResumeToken("conv-token"); ok {
		t.Fatal("takeImageResumeToken() second call should fail after consumption")
	}
}

func TestEngineResumeImagePollReturnsImageData(t *testing.T) {
	engine := &Engine{Config: testProtocolImageConfig{root: t.TempDir()}}
	engine.rememberImageResumeToken("conv-resume", "tok-1")

	var gotToken, gotConversationID, gotPrompt string
	engine.ResumeImagePollFunc = func(ctx context.Context, token string, request ConversationRequest, conversationID string) ([]backend.ResponsesImageEvent, error) {
		gotToken = token
		gotConversationID = conversationID
		gotPrompt = request.Prompt
		return []backend.ResponsesImageEvent{{Result: testPNGBase64(t), OutputFormat: "png", Created: 100}}, nil
	}

	data, err := engine.ResumeImagePoll(context.Background(), ConversationRequest{Prompt: "draw a cat", ResponseFormat: "b64_json"}, "conv-resume", 5*time.Second)
	if err != nil {
		t.Fatalf("ResumeImagePoll() error = %v", err)
	}
	if gotToken != "tok-1" {
		t.Fatalf("injected token = %q, want tok-1", gotToken)
	}
	if gotConversationID != "conv-resume" {
		t.Fatalf("injected conversationID = %q, want conv-resume", gotConversationID)
	}
	if gotPrompt != "draw a cat" {
		t.Fatalf("injected prompt = %q, want draw a cat", gotPrompt)
	}
	if len(data) != 1 {
		t.Fatalf("data length = %d, want 1", len(data))
	}
	if util.Clean(data[0]["url"]) == "" {
		t.Fatalf("data[0][url] empty, want saved image URL: %#v", data[0])
	}
	if util.Clean(data[0]["b64_json"]) == "" {
		t.Fatalf("data[0][b64_json] empty for b64_json response format: %#v", data[0])
	}
	// 令牌应已被消费，避免重复续轮询。
	if _, ok := engine.takeImageResumeToken("conv-resume"); ok {
		t.Fatal("resume token should be consumed after ResumeImagePoll")
	}
}

func TestEngineResumeImagePollFailsWithoutToken(t *testing.T) {
	engine := &Engine{Config: testProtocolImageConfig{root: t.TempDir()}}

	if _, err := engine.ResumeImagePoll(context.Background(), ConversationRequest{Prompt: "draw"}, "conv-missing", time.Second); err == nil {
		t.Fatal("ResumeImagePoll() expected error when resume token missing")
	}
}

func TestEngineResumeImagePollEmptyResultFails(t *testing.T) {
	engine := &Engine{Config: testProtocolImageConfig{root: t.TempDir()}}
	engine.rememberImageResumeToken("conv-empty", "tok-1")
	engine.ResumeImagePollFunc = func(ctx context.Context, token string, request ConversationRequest, conversationID string) ([]backend.ResponsesImageEvent, error) {
		return nil, nil
	}

	if _, err := engine.ResumeImagePoll(context.Background(), ConversationRequest{Prompt: "draw"}, "conv-empty", time.Second); err == nil {
		t.Fatal("ResumeImagePoll() expected error when no image resolved")
	}
}

func TestRunSingleImageOutputRetriesConnectionTimeoutWithBackoff(t *testing.T) {
	var mu sync.Mutex
	var sleeps []time.Duration
	attempts := 0
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.imageRetrySleep = func(d time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n <= 2 {
			close(out)
			errCh <- fmt.Errorf("curl: (28) Operation timed out after 30000 ms")
			close(errCh)
			return out, errCh
		}
		out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"b64_json": "image"}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{Prompt: "draw", Model: "gpt-image-2", N: 1})
	if _, err := engine.CollectImageOutputs(outputs, errCh); err != nil {
		t.Fatalf("CollectImageOutputs() error = %v, want success after connection-timeout retries", err)
	}

	mu.Lock()
	got := append([]time.Duration(nil), sleeps...)
	mu.Unlock()
	// 对齐上游连接超时退避：第 n 次重试等待 min(3*n, 9)s -> 3s, 6s
	want := []time.Duration{3 * time.Second, 6 * time.Second}
	if len(got) != len(want) {
		t.Fatalf("connection-timeout backoff sleeps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sleep[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestRunSingleImageOutputRetriesTLSHandshakeWithBackoff(t *testing.T) {
	var mu sync.Mutex
	var sleeps []time.Duration
	attempts := 0
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.imageRetrySleep = func(d time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n <= 2 {
			close(out)
			errCh <- fmt.Errorf("curl: (35) OpenSSL SSL_connect: SSL_ERROR_SYSCALL")
			close(errCh)
			return out, errCh
		}
		out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"b64_json": "image"}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{Prompt: "draw", Model: "gpt-image-2", N: 1})
	if _, err := engine.CollectImageOutputs(outputs, errCh); err != nil {
		t.Fatalf("CollectImageOutputs() error = %v, want success after TLS retries", err)
	}

	mu.Lock()
	got := append([]time.Duration(nil), sleeps...)
	mu.Unlock()
	// 对齐上游 TLS 退避：第 n 次重试等待 min(2*n, 10)s -> 2s, 4s
	want := []time.Duration{2 * time.Second, 4 * time.Second}
	if len(got) != len(want) {
		t.Fatalf("tls backoff sleeps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sleep[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestImageConversationFallbackReferenceUsedOnlyForNewUpstreamSession(t *testing.T) {
	fallback := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("fallback"))
	sessions := service.NewImageConversationSessionService(filepath.Join(t.TempDir(), "sessions.json"))
	sessions.Bind(service.ImageConversationSession{
		OwnerID:                 "owner-1",
		FrontendConversationID:  "front-1",
		AccessToken:             "bound-token",
		UpstreamConversationID:  "conv-1",
		UpstreamParentMessageID: "msg-1",
	})
	engine := &Engine{
		ImageConversationSessions: sessions,
		ImageTokenProvider:        func(context.Context) (string, error) { return "bound-token", nil },
		ImageClientFactory:        func(string) *backend.Client { return nil },
	}
	var continuedRequest ConversationRequest
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		continuedRequest = request
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), ConversationID: "conv-1", MessageID: "msg-2", Data: []map[string]any{{"b64_json": "image"}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}
	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{Prompt: "continue", Model: "gpt-image-2", N: 1, OwnerID: "owner-1", FrontendConversationID: "front-1", Images: []string{"current"}, FallbackReferenceImage: fallback})
	if _, err := engine.CollectImageOutputs(outputs, errCh); err != nil {
		t.Fatalf("CollectImageOutputs() error = %v", err)
	}
	if continuedRequest.UpstreamConversationID != "conv-1" || continuedRequest.UpstreamParentMessageID != "msg-1" {
		t.Fatalf("continuation pointers = %q/%q", continuedRequest.UpstreamConversationID, continuedRequest.UpstreamParentMessageID)
	}
	if got := strings.Join(continuedRequest.Images, ","); got != "current" {
		t.Fatalf("continued request images = %q, want current only", got)
	}

	engine.ImageConversationSessions = service.NewImageConversationSessionService(filepath.Join(t.TempDir(), "sessions.json"))
	engine.ImageTokenProvider = func(context.Context) (string, error) { return "new-token", nil }
	var newRequest ConversationRequest
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		newRequest = request
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), ConversationID: "conv-new", MessageID: "msg-new", Data: []map[string]any{{"b64_json": "image"}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}
	outputs, errCh = engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{Prompt: "new", Model: "gpt-image-2", N: 1, OwnerID: "owner-1", FrontendConversationID: "front-2", Images: []string{"current"}, FallbackReferenceImage: fallback})
	if _, err := engine.CollectImageOutputs(outputs, errCh); err != nil {
		t.Fatalf("CollectImageOutputs() new session error = %v", err)
	}
	if newRequest.UpstreamConversationID != "" || newRequest.UpstreamParentMessageID != "" {
		t.Fatalf("new request continuation pointers = %q/%q, want empty", newRequest.UpstreamConversationID, newRequest.UpstreamParentMessageID)
	}
	if len(newRequest.Images) != 2 || newRequest.Images[0] != "current" || newRequest.Images[1] != fallback {
		t.Fatalf("new request images = %#v, want current plus fallback", newRequest.Images)
	}
}

func TestStreamResponsesImageOutputsCompletesWithUpstreamRefusalText(t *testing.T) {
	const upstreamText = "非常抱歉，生成的图片可能违反了关于裸露、色情或情色内容的防护限制。如果你认为此判断有误，请重试或修改提示语。"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation/prepare":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"title_generation\",\"title\":\"正在处理图片\",\"conversation_id\":\"conv-refused\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-refused\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-refused":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{
				"assistant-text":{"message":{"author":{"role":"assistant"},"create_time":3,"content":{"content_type":"text","parts":["` + upstreamText + `"]},"status":"finished_successfully","recipient":"all","metadata":{"model_slug":"gpt-5-5"}}}
			}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(token string) *backend.Client {
			client := backend.NewClient(token, nil, service.NewProxyService(testProtocolProxyConfig{}))
			client.BaseURL = server.URL
			return client
		},
	}

	outputs, imageErr := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Prompt: "edit",
		Model:  "gpt-image-2",
		N:      1,
	})
	result, err := engine.CollectImageOutputs(outputs, imageErr)
	if err != nil {
		t.Fatalf("CollectImageOutputs() err = %v", err)
	}
	if result["output_type"] != "text" || result["message"] != upstreamText {
		t.Fatalf("result = %#v, want upstream refusal text as text output", result)
	}
}

func TestIsFinalImageTextEventIgnoresImageGenMetadataWithResultIDs(t *testing.T) {
	toolFalse := false
	event := backend.ResponsesImageEvent{
		Type:           "server_ste_metadata",
		Text:           "Here is the generated image.",
		ToolInvoked:    &toolFalse,
		TurnUseCase:    "image gen",
		SedimentIDs:    []string{"file_image"},
		ConversationID: "conv-image",
	}

	if isFinalImageTextEvent(event) {
		t.Fatalf("isFinalImageTextEvent(%#v) = true, want false for image generation metadata", event)
	}
}

func TestIsFinalImageTextEventWaitsForBackendTextMarkerOnImageGenRefusal(t *testing.T) {
	event := backend.ResponsesImageEvent{
		Type:        "message_stream_complete",
		Text:        "非常抱歉，生成的图片可能违反了关于裸露、色情或情色内容的防护限制。如果你认为此判断有误，请重试或修改提示语。",
		TurnUseCase: "image gen",
	}

	if isFinalImageTextEvent(event) {
		t.Fatalf("isFinalImageTextEvent(%#v) = true, want false before backend marks final text", event)
	}

	event.Type = "image_text_response"
	if !isFinalImageTextEvent(event) {
		t.Fatalf("isFinalImageTextEvent(%#v) = false, want true after backend marks final text", event)
	}
}

func TestIsFinalImageTextEventKeepsQueuedImageNoticePending(t *testing.T) {
	event := backend.ResponsesImageEvent{
		Type:        "message_stream_complete",
		Text:        "正在处理图片，目前有很多人在创建图片，因此可能需要一点时间。图片准备好后我们会通知你。",
		TurnUseCase: "image gen",
	}

	if isFinalImageTextEvent(event) {
		t.Fatalf("isFinalImageTextEvent(%#v) = true, want false for queued image notice", event)
	}
}

func TestIsTransientImageStreamErrorMessage(t *testing.T) {
	transient := []string{
		"responses SSE read error: stream error: stream ID 1; INTERNAL_ERROR; received from peer",
		"connection error: FLOW_CONTROL_ERROR",
		"http2: client connection lost",
		"unexpected EOF",
		"connection reset by peer",
		"stream closed",
		"bootstrap failed: upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy",
		`bootstrap failed: Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`,
	}
	for _, input := range transient {
		if !isTransientImageStreamErrorMessage(input) {
			t.Fatalf("isTransientImageStreamErrorMessage(%q) = false, want true", input)
		}
	}

	stable := []string{
		"upstream returned Cloudflare challenge page",
		"You've reached the image generation limit for now.",
		"invalid size: expected WIDTHxHEIGHT",
		"auth_chat_requirements failed: status=401",
	}
	for _, input := range stable {
		if isTransientImageStreamErrorMessage(input) {
			t.Fatalf("isTransientImageStreamErrorMessage(%q) = true, want false", input)
		}
	}
}

func waitForImageWorkerStarts(started <-chan struct{}, want int) bool {
	for i := 0; i < want; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			return false
		}
	}
	return true
}

func TestStreamImageOutputsWithPoolRunsRequestedImagesConcurrently(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}

	var mu sync.Mutex
	started := 0
	maxActive := 0
	workerStarted := make(chan struct{}, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		go func() {
			defer close(out)
			defer close(errCh)
			mu.Lock()
			started++
			if started > maxActive {
				maxActive = started
			}
			mu.Unlock()
			workerStarted <- struct{}{}
			<-release
			out <- ImageOutput{
				Kind:    "result",
				Model:   request.Model,
				Index:   index,
				Total:   total,
				Created: int64(index),
				Data:    []map[string]any{{"url": imageURLForIndex(index)}},
			}
			mu.Lock()
			started--
			mu.Unlock()
			errCh <- nil
		}()
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "gpt-image-2",
		N:     4,
	})

	if !waitForImageWorkerStarts(workerStarted, 4) {
		t.Fatalf("timed out waiting for 4 image workers to start")
	}
	mu.Lock()
	gotActive := maxActive
	mu.Unlock()
	if gotActive != 4 {
		t.Fatalf("max concurrent image workers = %d, want 4", gotActive)
	}

	closeRelease()
	for range outputs {
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() err = %v", err)
	}
}

func TestStreamImageOutputsWithPoolHonorsImageOutputSlotAcquirer(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}

	var mu sync.Mutex
	active := 0
	maxActive := 0
	workerStarted := make(chan struct{}, 3)
	release := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)
	slots := make(chan struct{}, 2)
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		go func() {
			defer close(out)
			defer close(errCh)
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			workerStarted <- struct{}{}
			<-release
			out <- ImageOutput{
				Kind:    "result",
				Model:   request.Model,
				Index:   index,
				Total:   total,
				Created: int64(index),
				Data:    []map[string]any{{"url": imageURLForIndex(index)}},
			}
			mu.Lock()
			active--
			mu.Unlock()
			errCh <- nil
		}()
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "gpt-image-2",
		N:     3,
		AcquireImageOutputSlot: func(ctx context.Context, index int) (func(), error) {
			select {
			case slots <- struct{}{}:
				return func() { <-slots }, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})

	if !waitForImageWorkerStarts(workerStarted, 2) {
		t.Fatalf("timed out waiting for 2 image workers to start")
	}
	mu.Lock()
	gotActive := maxActive
	mu.Unlock()
	if gotActive != 2 {
		t.Fatalf("max concurrent image workers = %d, want 2", gotActive)
	}

	closeRelease()
	for range outputs {
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() err = %v", err)
	}
}

func TestStreamImageOutputsWithPoolHoldsImageLeaseDuringUpstream(t *testing.T) {
	engine, accounts := newImageLeaseTestEngine(t, "token-1")
	started := make(chan struct{})
	releaseUpstream := make(chan struct{})
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		go func() {
			defer close(out)
			defer close(errCh)
			close(started)
			<-releaseUpstream
			out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": imageURLForIndex(index)}}}
			errCh <- nil
		}()
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{Model: "gpt-image-2", N: 1})
	done := make(chan error, 1)
	go func() {
		for range outputs {
		}
		done <- <-errCh
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not start")
	}
	if _, err := accounts.AcquireTextAccessToken(nil); err == nil {
		t.Fatal("expected text lease acquire to fail while image lease is held")
	}
	close(releaseUpstream)
	if err := <-done; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() err = %v", err)
	}
	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() after image stream release error = %v", err)
	}
	lease.Release()
}

func TestStreamImageOutputsWithPoolReleasesImageLeaseOnError(t *testing.T) {
	engine, accounts := newImageLeaseTestEngine(t, "token-1")
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		close(out)
		errCh <- errors.New("upstream boom")
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{Model: "gpt-image-2", N: 1})
	for range outputs {
	}
	if err := <-errCh; err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want upstream error")
	}
	lease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() after image error release error = %v", err)
	}
	lease.Release()
}

func TestStreamImageOutputsWithPoolPreferredImageLeaseFallsBackWhenBusy(t *testing.T) {
	engine, accounts := newImageLeaseTestEngine(t, "preferred-token", "fallback-token")
	engine.ImageConversationSessions = service.NewImageConversationSessionService(filepath.Join(t.TempDir(), "sessions.json"))
	engine.ImageConversationSessions.Bind(service.ImageConversationSession{
		OwnerID:                 "owner-1",
		FrontendConversationID:  "front-1",
		AccessToken:             "preferred-token",
		UpstreamConversationID:  "conv-1",
		UpstreamParentMessageID: "msg-1",
	})
	preferredLease, err := accounts.AcquireTextAccessToken(map[string]struct{}{"fallback-token": {}})
	if err != nil {
		t.Fatalf("AcquireTextAccessToken(preferred) error = %v", err)
	}
	defer preferredLease.Release()

	usedToken := ""
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		usedToken = client.AccessToken
		if request.UpstreamConversationID != "" || request.UpstreamParentMessageID != "" {
			t.Errorf("fallback request used preferred session pointers %q/%q", request.UpstreamConversationID, request.UpstreamParentMessageID)
		}
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": imageURLForIndex(index)}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{Model: "gpt-image-2", N: 1, OwnerID: "owner-1", FrontendConversationID: "front-1"})
	for range outputs {
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() err = %v", err)
	}
	if usedToken != "fallback-token" {
		t.Fatalf("used token = %q, want fallback-token", usedToken)
	}
}

func TestStreamImageOutputsWithPoolChargeFailureReleasesImageReservation(t *testing.T) {
	engine, accounts := newImageLeaseTestEngine(t, "token-1")
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": imageURLForIndex(index)}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model:             "gpt-image-2",
		N:                 1,
		ChargeImageOutput: func(int) error { return errors.New("billing denied") },
	})
	for range outputs {
	}
	if err := <-errCh; err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want billing error")
	}
	lease, err := accounts.GetAvailableImageAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() after charge failure error = %v", err)
	}
	lease.Release()
}

func TestStreamImageOutputsWithPoolCanceledSendReleasesImageReservation(t *testing.T) {
	engine, accounts := newImageLeaseTestEngine(t, "token-1")
	upstreamSent := make(chan struct{})
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": imageURLForIndex(index)}}}
		close(out)
		errCh <- nil
		close(errCh)
		close(upstreamSent)
		return out, errCh
	}

	ctx, cancel := context.WithCancel(context.Background())
	outputs, errCh := engine.StreamImageOutputsWithPool(ctx, ConversationRequest{Model: "gpt-image-2", N: 1})
	select {
	case <-upstreamSent:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("upstream did not send output")
	}
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want context cancellation")
	}
	for range outputs {
	}
	lease, err := accounts.GetAvailableImageAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAvailableImageAccessToken() after canceled send error = %v", err)
	}
	lease.Release()
}

func TestStreamImageOutputsWithPoolDoesNotUseProviderWhenAccountServiceHasNoLease(t *testing.T) {
	engine, accounts := newImageLeaseTestEngine(t, "token-1")
	busyLease, err := accounts.AcquireTextAccessToken(nil)
	if err != nil {
		t.Fatalf("AcquireTextAccessToken() error = %v", err)
	}
	defer busyLease.Release()
	providerCalled := false
	engine.ImageTokenProvider = func(context.Context) (string, error) {
		providerCalled = true
		return "provider-token", nil
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{Model: "gpt-image-2", N: 1})
	for range outputs {
	}
	if err := <-errCh; err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want no available account error")
	}
	if providerCalled {
		t.Fatal("ImageTokenProvider was called while AccountService was configured")
	}
}

func newImageLeaseTestEngine(t *testing.T, tokens ...string) (*Engine, *service.AccountService) {
	t.Helper()
	engine, accounts := newTextLeaseTestEngine(t, tokens...)
	for _, token := range tokens {
		accounts.UpdateAccount(token, map[string]any{"status": "正常", "quota": 5, "type": "Plus"})
	}
	server := newImageLeaseAccountServer(t)
	t.Cleanup(server.Close)
	setAccountServiceRemoteBaseURL(t, accounts, server.URL)
	return engine, accounts
}

func newImageLeaseAccountServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/me":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"user@example.test","id":"user-1"}`))
		case "/backend-api/conversation/init":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"default_model_slug":"gpt-5","limits_progress":[{"feature_name":"image_gen","remaining":5,"reset_after":"2026-05-20T00:00:00Z"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func setAccountServiceRemoteBaseURL(t *testing.T, accounts *service.AccountService, baseURL string) {
	t.Helper()
	field := reflect.ValueOf(accounts).Elem().FieldByName("remoteBaseURL")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetString(baseURL)
}

func TestStreamImageOutputsWithPoolDoesNotRotateOnGenericUnauthorized(t *testing.T) {
	usedTokens := []string(nil)
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) {
			token := fmt.Sprintf("token-%d", len(usedTokens)+1)
			usedTokens = append(usedTokens, token)
			return token, nil
		},
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		close(out)
		errCh <- fmt.Errorf("auth_chat_requirements failed: status=401, body={\"detail\":\"challenge_required\"}")
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "gpt-image-2",
		N:     1,
	})
	for range outputs {
	}
	err := <-errCh
	if err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want upstream error")
	}
	if len(usedTokens) != 1 {
		t.Fatalf("used tokens = %#v, want one token without pool rotation", usedTokens)
	}
	if !strings.Contains(err.Error(), "challenge_required") {
		t.Fatalf("error = %q, want original upstream detail", err.Error())
	}
}

func TestStreamImageOutputsWithPoolReportsCodexUnauthorizedPermission(t *testing.T) {
	usedTokens := []string(nil)
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) {
			token := fmt.Sprintf("token-%d", len(usedTokens)+1)
			usedTokens = append(usedTokens, token)
			return token, nil
		},
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		close(out)
		errCh <- fmt.Errorf("/backend-api/codex/responses failed: status=401, body={\"detail\":\"Unauthorized\"}")
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "codex-gpt-image-2",
		N:     1,
	})
	for range outputs {
	}
	err := <-errCh
	if err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want permission error")
	}
	if len(usedTokens) != 1 {
		t.Fatalf("used tokens = %#v, want one token without pool rotation", usedTokens)
	}
	if !strings.Contains(err.Error(), "codex-gpt-image-2 需要 Plus / Team / Pro 账号") {
		t.Fatalf("error = %q, want Codex permission guidance", err.Error())
	}
}

func TestCollectImageOutputsKeepsImageOrderByIndex(t *testing.T) {
	outputs := make(chan ImageOutput, 2)
	errCh := make(chan error, 1)
	outputs <- ImageOutput{
		Kind:    "result",
		Index:   2,
		Total:   2,
		Created: 2,
		Data:    []map[string]any{{"url": "https://example.test/second.png"}},
	}
	outputs <- ImageOutput{
		Kind:    "result",
		Index:   1,
		Total:   2,
		Created: 1,
		Data:    []map[string]any{{"url": "https://example.test/first.png"}},
	}
	close(outputs)
	errCh <- nil
	close(errCh)

	result, err := (&Engine{}).CollectImageOutputs(outputs, errCh)
	if err != nil {
		t.Fatalf("CollectImageOutputs() err = %v", err)
	}
	data := result["data"].([]map[string]any)
	if len(data) != 2 {
		t.Fatalf("data len = %d, want 2", len(data))
	}
	if data[0]["url"] != "https://example.test/first.png" || data[1]["url"] != "https://example.test/second.png" {
		t.Fatalf("data order = %#v, want first then second", data)
	}
}

func imageURLForIndex(index int) string {
	switch index {
	case 1:
		return "https://example.test/image-1.png"
	case 2:
		return "https://example.test/image-2.png"
	case 3:
		return "https://example.test/image-3.png"
	case 4:
		return "https://example.test/image-4.png"
	default:
		return "https://example.test/image.png"
	}
}
