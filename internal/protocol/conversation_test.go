package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if got := imageStreamErrorMessage(""); got != "image generation failed" {
		t.Fatalf("empty error = %q", got)
	}
}

func TestIsTransientImageStreamErrorMessage(t *testing.T) {
	transient := []string{
		"responses SSE read error: stream error: stream ID 1; INTERNAL_ERROR; received from peer",
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
