package protocol

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt2api/internal/backend"
)

type testProtocolImageConfig struct {
	root string
}

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

func (c testProtocolImageConfig) CleanupOldImages() int {
	return 0
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
	if got := imageStreamErrorMessage(""); got != "image generation failed" {
		t.Fatalf("empty error = %q", got)
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

func TestStreamImageOutputsWithPoolRunsRequestedImagesConcurrently(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}

	var mu sync.Mutex
	started := 0
	maxActive := 0
	release := make(chan struct{})
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

	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	gotActive := maxActive
	mu.Unlock()
	if gotActive != 4 {
		t.Fatalf("max concurrent image workers = %d, want 4", gotActive)
	}

	close(release)
	for range outputs {
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() err = %v", err)
	}
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
