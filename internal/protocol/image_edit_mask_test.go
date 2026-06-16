package protocol

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"chatgpt2api/internal/backend"
)

func TestHandleImageEditsCompositesMaskAlphaIntoInputImage(t *testing.T) {
	imageData := encodeNRGBAForMaskTest(t, []color.NRGBA{
		{R: 255, G: 0, B: 0, A: 255},
		{R: 0, G: 255, B: 0, A: 255},
	})
	maskData := encodeNRGBAForMaskTest(t, []color.NRGBA{
		{R: 0, G: 0, B: 0, A: 0},
		{R: 0, G: 0, B: 0, A: 255},
	})
	var captured ConversationRequest
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		captured = request
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "result", Model: request.Model, Created: time.Now().Unix(), Data: []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString([]byte("ok"))}}}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	_, _, err := engine.HandleImageEdits(context.Background(), map[string]any{
		"prompt": "edit",
		"model":  "gpt-image-2",
		"mask": []UploadedImage{{
			Data:        maskData,
			Filename:    "mask.png",
			ContentType: "image/png",
		}},
	}, []UploadedImage{{
		Data:        imageData,
		Filename:    "input.png",
		ContentType: "image/png",
	}})
	if err != nil {
		t.Fatalf("HandleImageEdits() error = %v", err)
	}
	if len(captured.InputImages) != 1 {
		t.Fatalf("InputImages = %#v, want one composited image", captured.InputImages)
	}
	if captured.InputImages[0].ContentType != "image/png" {
		t.Fatalf("ContentType = %q, want image/png", captured.InputImages[0].ContentType)
	}
	img, err := png.Decode(bytes.NewReader(captured.InputImages[0].Data))
	if err != nil {
		t.Fatalf("decode composited image: %v", err)
	}
	_, _, _, a0 := img.At(0, 0).RGBA()
	_, _, _, a1 := img.At(1, 0).RGBA()
	if a0 != 0 || a1 != 0xffff {
		t.Fatalf("alpha = (%#x, %#x), want (0, 0xffff)", a0, a1)
	}
}

func encodeNRGBAForMaskTest(t *testing.T, pixels []color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, len(pixels), 1))
	for x, pixel := range pixels {
		img.SetNRGBA(x, 0, pixel)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buf.Bytes()
}
