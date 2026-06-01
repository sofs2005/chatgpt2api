package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestDecodeEditableFileImageSupportsBase64AndDataURL(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	for name, input := range map[string]string{
		"base64":   encoded,
		"data-url": "data:image/png;base64," + encoded,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeEditableFileImage(input)
			if err != nil {
				t.Fatalf("DecodeEditableFileImage() error = %v", err)
			}
			if got.ContentType != "image/png" {
				t.Fatalf("ContentType = %q, want image/png", got.ContentType)
			}
			if got.Extension != "png" {
				t.Fatalf("Extension = %q, want png", got.Extension)
			}
			if got.Width != 1 || got.Height != 1 {
				t.Fatalf("dimensions = %dx%d, want 1x1", got.Width, got.Height)
			}
			if got.FileName != "image.png" {
				t.Fatalf("FileName = %q, want image.png", got.FileName)
			}
			if len(got.Data) == 0 {
				t.Fatal("decoded image data is empty")
			}
		})
	}
}

func TestBuildEditablePayloadsIncludeMultimodalPointers(t *testing.T) {
	ref := uploadedImageRef{
		FileID:   "file_abc",
		FileName: "input.png",
		FileSize: 123,
		MIMEType: "image/png",
		Width:    640,
		Height:   480,
	}

	prepare := BuildEditablePreparePayload("  make slides  ", []uploadedImageRef{ref})
	if got := prepare["model"]; got != editableFileModel {
		t.Fatalf("prepare model = %#v, want %q", got, editableFileModel)
	}
	if got := prepare["thinking_effort"]; got != "extended" {
		t.Fatalf("prepare thinking_effort = %#v, want extended", got)
	}
	partial := prepare["partial_query"].(map[string]any)
	content := partial["content"].(map[string]any)
	if got := content["content_type"]; got != "multimodal_text" {
		t.Fatalf("prepare content_type = %#v, want multimodal_text", got)
	}
	parts := content["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("prepare parts = %#v, want 2 items", parts)
	}
	if parts[0] != "make slides" {
		t.Fatalf("prepare prompt part = %#v, want trimmed prompt", parts[0])
	}
	imagePart := parts[1].(map[string]any)
	if imagePart["content_type"] != "image_asset_pointer" || imagePart["asset_pointer"] != "sediment://file_abc" || imagePart["library_file_id"] != "file_abc" {
		t.Fatalf("prepare image part = %#v", imagePart)
	}

	conversation := BuildEditableConversationPayload("ppt", "make slides", "conduit-token", []uploadedImageRef{ref})
	if got := conversation["model"]; got != editableFileModel {
		t.Fatalf("conversation model = %#v, want %q", got, editableFileModel)
	}
	if got := conversation["thinking_effort"]; got != "extended" {
		t.Fatalf("conversation thinking_effort = %#v, want extended", got)
	}
	if got := conversation["conduit_token"]; got != "conduit-token" {
		t.Fatalf("conversation conduit_token = %#v, want conduit-token", got)
	}
	messages := conversation["messages"].([]any)
	message := messages[0].(map[string]any)
	metadata := message["metadata"].(map[string]any)
	if got := metadata["editable_file_kind"]; got != "ppt" {
		t.Fatalf("editable_file_kind = %#v, want ppt", got)
	}
	attachments := metadata["attachments"].([]map[string]any)
	if len(attachments) != 1 || attachments[0]["library_file_id"] != "file_abc" {
		t.Fatalf("attachments = %#v", attachments)
	}
	content = message["content"].(map[string]any)
	parts = content["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("conversation parts = %#v, want 2 items", parts)
	}
	if parts[1].(map[string]any)["asset_pointer"] != "sediment://file_abc" {
		t.Fatalf("conversation asset pointer = %#v", parts[1])
	}
}

func TestCollectEditableFileAssetsParsesConversationAndEmbeddedData(t *testing.T) {
	payload := map[string]any{
		"conversation_id": "conv-1",
		"message": map[string]any{
			"file_name": "deck.pptx",
			"data":      base64.StdEncoding.EncodeToString([]byte("hello world")),
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	conversationID, assets, err := collectEditableFileAssets(context.Background(), strings.NewReader("data: "+string(data)+"\n\n"))
	if err != nil {
		t.Fatalf("collectEditableFileAssets() error = %v", err)
	}
	if conversationID != "conv-1" {
		t.Fatalf("conversationID = %q, want conv-1", conversationID)
	}
	if len(assets) != 1 {
		t.Fatalf("assets = %#v, want 1 item", assets)
	}
	if assets[0].FileName != "deck.pptx" || string(assets[0].Data) != "hello world" {
		t.Fatalf("asset = %#v", assets[0])
	}
}

func TestCollectEditableFileAssetsIgnoresInvalidLines(t *testing.T) {
	conversationID, assets, err := collectEditableFileAssets(context.Background(), strings.NewReader("event: update\n\ndata: not-json\n\ndata: [DONE]\n"))
	if err != nil {
		t.Fatalf("collectEditableFileAssets() error = %v", err)
	}
	if conversationID != "" || len(assets) != 0 {
		t.Fatalf("conversationID=%q assets=%#v, want empty results", conversationID, assets)
	}
}

func TestEditableFileAssetDataPrefersEmbeddedBase64(t *testing.T) {
	asset := editableAssetData(map[string]any{"file_name": "x.bin", "text": "Zm9v", "content": "bar"})
	if string(asset) != "foo" {
		t.Fatalf("editableAssetData() = %q, want foo", string(asset))
	}
	if got := editableAssetData(map[string]any{"file_name": "x.bin", "text": " plain text "}); string(got) != "plain text" {
		t.Fatalf("editableAssetData() text = %q, want plain text", string(got))
	}
	if got := editableAssetData(map[string]any{"file_name": "x.bin"}); got != nil {
		t.Fatalf("editableAssetData() = %#v, want nil", got)
	}
}

func TestEditableFileAssetDeduplicationUsesSeenSet(t *testing.T) {
	seen := map[string]struct{}{}
	asset, ok := editableAssetFromMap(map[string]any{"file_name": "deck.pptx", "data": "hello"}, seen)
	if !ok || string(asset.Data) != "hello" {
		t.Fatalf("first asset = %#v ok=%v", asset, ok)
	}
	if _, ok := editableAssetFromMap(map[string]any{"file_name": "deck.pptx", "data": "hello"}, seen); ok {
		t.Fatal("duplicate asset should be ignored")
	}
}

func TestEditableFileConversationIDSearchesNestedValues(t *testing.T) {
	value := map[string]any{"v": map[string]any{"message": map[string]any{"metadata": map[string]any{"conversation_id": "conv-nested"}}}}
	if got := editableConversationID(value); got != "conv-nested" {
		t.Fatalf("editableConversationID() = %q, want conv-nested", got)
	}
}

func TestEditableFileCollectFromNestedAssetMap(t *testing.T) {
	value := map[string]any{"message": map[string]any{"content": map[string]any{"assets": []any{map[string]any{"file_name": "nested.psd", "base64": base64.StdEncoding.EncodeToString([]byte("data"))}}}}}
	assets := editableAssetsFromValue(context.Background(), value, map[string]struct{}{})
	if len(assets) != 1 || assets[0].FileName != "nested.psd" || string(assets[0].Data) != "data" {
		t.Fatalf("assets = %#v", assets)
	}
}

func TestEditableFileCollectWithReaderSupportsMultipleEvents(t *testing.T) {
	stream := strings.Join([]string{
		"data: {\"conversation_id\":\"conv-a\"}",
		"",
		"data: {\"message\":{\"file_name\":\"first.pptx\",\"data\":\"" + base64.StdEncoding.EncodeToString([]byte("first")) + "\"}}",
		"",
		"data: {\"message\":{\"file_name\":\"second.pptx\",\"data\":\"" + base64.StdEncoding.EncodeToString([]byte("second")) + "\"}}",
		"",
	}, "\n")
	conversationID, assets, err := collectEditableFileAssets(context.Background(), strings.NewReader(stream))
	if err != nil {
		t.Fatalf("collectEditableFileAssets() error = %v", err)
	}
	if conversationID != "conv-a" {
		t.Fatalf("conversationID = %q, want conv-a", conversationID)
	}
	if len(assets) != 2 {
		t.Fatalf("assets = %#v, want 2 items", assets)
	}
	if string(assets[0].Data) != "first" || string(assets[1].Data) != "second" {
		t.Fatalf("asset data = %#v", assets)
	}
}

func TestEditableFileCollectDoesNotRequireDownloadURL(t *testing.T) {
	asset, ok := editableAssetFromMap(map[string]any{"file_name": "deck.pptx", "url": "https://example.test/deck.pptx"}, map[string]struct{}{})
	if ok {
		t.Fatalf("asset with no embedded data should be ignored: %#v", asset)
	}
}
