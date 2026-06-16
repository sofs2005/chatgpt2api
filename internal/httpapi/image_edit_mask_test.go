package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"chatgpt2api/internal/protocol"
)

func TestReadMultipartImageBodyReadsMaskFiles(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("prompt", "edit"); err != nil {
		t.Fatalf("WriteField(prompt) error = %v", err)
	}
	writeMultipartFileForMaskTest(t, writer, "image", "input.png", []byte("image-bytes"))
	writeMultipartFileForMaskTest(t, writer, "mask", "mask.png", []byte("mask-bytes"))
	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart writer error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	body, images, err := readMultipartImageBody(req)
	if err != nil {
		t.Fatalf("readMultipartImageBody() error = %v", err)
	}
	if len(images) != 1 || string(images[0].Data) != "image-bytes" {
		t.Fatalf("images = %#v", images)
	}
	masks, ok := body["mask"].([]protocol.UploadedImage)
	if !ok || len(masks) != 1 || string(masks[0].Data) != "mask-bytes" {
		t.Fatalf("mask = %#v", body["mask"])
	}
}

func writeMultipartFileForMaskTest(t *testing.T, writer *multipart.Writer, field, filename string, data []byte) {
	t.Helper()
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile(%s) error = %v", field, err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("Write(%s) error = %v", field, err)
	}
}
