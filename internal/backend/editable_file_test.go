package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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

func TestExportEditableFileDownloadsArtifactsFromConversationDetail(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"conversation_id\":\"conv-1\"}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{"node-1":{"message":{"id":"msg-1","create_time":100,"author":{"role":"assistant"},"metadata":{"attachments":[{"id":"file-deck","file_id":"file-deck","name":"deck.pptx","mime_type":"application/vnd.openxmlformats-officedocument.presentationml.presentation"},{"id":"file-zip","file_id":"file-zip","name":"assets.zip","mime_type":"application/zip"}]}}}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-1/attachment/file-deck/download":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/deck.pptx"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-1/attachment/file-zip/download":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/assets.zip"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/deck.pptx":
			_, _ = w.Write([]byte("deck-bytes"))
		case r.Method == http.MethodGet && r.URL.Path == "/download/assets.zip":
			_, _ = w.Write([]byte("zip-bytes"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := newTestBackendClient(server).ExportEditableFile(ctx, "ppt", "make slides", nil, t.TempDir())
	if err != nil {
		t.Fatalf("ExportEditableFile() error = %v", err)
	}
	if result.ConversationID != "conv-1" {
		t.Fatalf("ConversationID = %q, want conv-1", result.ConversationID)
	}
	primaryData, err := os.ReadFile(result.PrimaryPath)
	if err != nil {
		t.Fatalf("ReadFile(primary) error = %v", err)
	}
	if string(primaryData) != "deck-bytes" {
		t.Fatalf("primary data = %q, want deck-bytes", string(primaryData))
	}
	zipData, err := os.ReadFile(result.ZipPath)
	if err != nil {
		t.Fatalf("ReadFile(zip) error = %v", err)
	}
	if string(zipData) != "zip-bytes" {
		t.Fatalf("zip data = %q, want zip-bytes", string(zipData))
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

func TestEditableArtifactsFromConversationFindsAttachmentFileIDs(t *testing.T) {
	conversation := map[string]any{
		"mapping": map[string]any{
			"node-1": map[string]any{"message": map[string]any{
				"id":          "msg-1",
				"create_time": 100.0,
				"author":      map[string]any{"role": "assistant"},
				"metadata": map[string]any{"attachments": []any{
					map[string]any{"id": "file-deck", "file_id": "file-deck", "name": "deck.pptx", "mime_type": "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
					map[string]any{"id": "file-zip", "file_id": "file-zip", "name": "assets.zip", "mime_type": "application/zip"},
				}},
			}},
		},
	}

	artifacts := editableArtifactsFromConversation(conversation, "ppt")
	targets := pickEditableTargetArtifacts(artifacts, "ppt")
	if len(targets) != 2 {
		t.Fatalf("targets = %#v, want primary and zip", targets)
	}
	if targets[0].FileID != "file-deck" || targets[0].FileName != "deck.pptx" {
		t.Fatalf("primary target = %#v", targets[0])
	}
	if targets[1].FileID != "file-zip" || targets[1].FileName != "assets.zip" {
		t.Fatalf("zip target = %#v", targets[1])
	}
}

func TestEditableArtifactsFromConversationFindsSandboxPaths(t *testing.T) {
	conversation := map[string]any{
		"mapping": map[string]any{
			"node-1": map[string]any{"message": map[string]any{
				"id":          "msg-psd",
				"create_time": 200.0,
				"author":      map[string]any{"role": "tool"},
				"content": map[string]any{"content_type": "text", "parts": []any{
					"created sandbox:/mnt/data/poster.psd and /mnt/data/layers.zip",
				}},
			}},
		},
	}

	artifacts := editableArtifactsFromConversation(conversation, "psd")
	targets := pickEditableTargetArtifacts(artifacts, "psd")
	if len(targets) != 2 {
		t.Fatalf("targets = %#v, want psd and zip", targets)
	}
	if targets[0].SandboxPath != "/mnt/data/poster.psd" || targets[0].MessageID != "msg-psd" {
		t.Fatalf("primary target = %#v", targets[0])
	}
	if targets[1].SandboxPath != "/mnt/data/layers.zip" || targets[1].MessageID != "msg-psd" {
		t.Fatalf("zip target = %#v", targets[1])
	}
}

func TestPickEditableTargetArtifactsRequiresZipAndUsesLatestArtifacts(t *testing.T) {
	if targets := pickEditableTargetArtifacts([]editableArtifact{{FileName: "deck.pptx", MIMEType: "application/vnd.openxmlformats-officedocument.presentationml.presentation"}}, "ppt"); len(targets) != 0 {
		t.Fatalf("targets = %#v, want none until zip is available", targets)
	}

	artifacts := []editableArtifact{
		{FileName: "old.pptx", FileID: "old-deck", MIMEType: "application/vnd.openxmlformats-officedocument.presentationml.presentation", CreateTime: 100},
		{FileName: "old.zip", FileID: "old-zip", MIMEType: "application/zip", CreateTime: 101},
		{FileName: "new.pptx", FileID: "new-deck", MIMEType: "application/vnd.openxmlformats-officedocument.presentationml.presentation", CreateTime: 200},
		{FileName: "new.zip", FileID: "new-zip", MIMEType: "application/zip", CreateTime: 201},
	}
	targets := pickEditableTargetArtifacts(artifacts, "ppt")
	if len(targets) != 2 {
		t.Fatalf("targets = %#v, want latest primary and zip", targets)
	}
	if targets[0].FileID != "new-deck" || targets[1].FileID != "new-zip" {
		t.Fatalf("targets = %#v, want latest artifacts", targets)
	}
}

func TestEditableArtifactsFromConversationFindsDeepAssetPointerObjects(t *testing.T) {
	message := map[string]any{
		"id":          "msg-asset-pointer",
		"create_time": 300.0,
		"author":      map[string]any{"role": "assistant"},
		"content": map[string]any{"parts": []any{map[string]any{"outputs": []any{
			map[string]any{"asset_pointer": "file-service://file-deck", "title": "deck.pptx", "mime_type": "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
			map[string]any{"asset_pointer": "sediment://file-zip", "title": "assets.zip", "mime_type": "application/zip"},
		}}}},
	}
	conversation := map[string]any{
		"mapping": map[string]any{
			"node-1": map[string]any{"message": message},
		},
	}

	artifacts := editableArtifactsFromConversation(conversation, "ppt")
	targets := pickEditableTargetArtifacts(artifacts, "ppt")
	if len(targets) != 2 {
		t.Fatalf("artifacts = %#v targets = %#v, want primary and zip", artifacts, targets)
	}
	if targets[0].FileID != "file-deck" || targets[0].FileName != "deck.pptx" || targets[0].MessageID != "msg-asset-pointer" {
		t.Fatalf("primary target = %#v", targets[0])
	}
	if targets[1].FileID != "file-zip" || targets[1].FileName != "assets.zip" || targets[1].MessageID != "msg-asset-pointer" {
		t.Fatalf("zip target = %#v", targets[1])
	}
}

func TestDownloadEditableArtifactSkipsJSONErrorBodies(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-1/attachment/file-psd/download":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"error","error_code":"file_not_found","error_type":"GetDownloadLinkError","error_message":null}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file-psd":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/person.psd"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/person.psd":
			_, _ = w.Write([]byte("psd-bytes"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	data, err := newTestBackendClient(server).downloadEditableArtifact(context.Background(), "conv-1", editableArtifact{
		AttachmentID: "file-psd",
		FileID:       "file-psd",
		FileName:     "person.psd",
	})
	if err != nil {
		t.Fatalf("downloadEditableArtifact() error = %v", err)
	}
	if string(data) != "psd-bytes" {
		t.Fatalf("download data = %q, want psd-bytes", string(data))
	}
}

func TestPickEditableTargetArtifactsUsesSandboxPathExtensions(t *testing.T) {
	artifacts := []editableArtifact{
		{FileName: "file-deck", FileID: "file-deck", SandboxPath: "/mnt/data/final_deck.pptx", CreateTime: 100},
		{FileName: "file-zip", FileID: "file-zip", SandboxPath: "/mnt/data/final_deck_assets.zip", CreateTime: 101},
	}

	targets := pickEditableTargetArtifacts(artifacts, "ppt")
	if len(targets) != 2 {
		t.Fatalf("targets = %#v, want primary and zip", targets)
	}
	if targets[0].FileID != "file-deck" || targets[1].FileID != "file-zip" {
		t.Fatalf("targets = %#v, want sandbox-path matched artifacts", targets)
	}
}

func TestEditableArtifactsFromConversationIgnoresStdoutAsFileIdentity(t *testing.T) {
	message := map[string]any{
		"id":          "msg-stdout",
		"create_time": 400.0,
		"author":      map[string]any{"role": "tool"},
		"content": map[string]any{"parts": []any{map[string]any{"outputs": []any{
			map[string]any{"id": "stdout", "name": "stdout", "path": "stdout", "mime_type": "application/vnd.openxmlformats-officedocument.presentationml.presentation", "text": "created sandbox:/mnt/data/final_deck.pptx"},
			map[string]any{"id": "stdout", "name": "stdout", "path": "stdout", "mime_type": "application/zip", "text": "created sandbox:/mnt/data/final_deck_assets.zip"},
		}}}},
	}
	conversation := map[string]any{"mapping": map[string]any{"node-1": map[string]any{"message": message}}}

	artifacts := editableArtifactsFromConversation(conversation, "ppt")
	targets := pickEditableTargetArtifacts(artifacts, "ppt")
	if len(targets) != 2 {
		t.Fatalf("artifacts = %#v targets = %#v, want primary and zip", artifacts, targets)
	}
	if targets[0].FileName != "final_deck.pptx" || targets[0].FileID != "" || targets[0].AttachmentID != "" || targets[0].SandboxPath != "/mnt/data/final_deck.pptx" {
		t.Fatalf("primary target = %#v, want sandbox file without stdout identity", targets[0])
	}
	if targets[1].FileName != "final_deck_assets.zip" || targets[1].FileID != "" || targets[1].AttachmentID != "" || targets[1].SandboxPath != "/mnt/data/final_deck_assets.zip" {
		t.Fatalf("zip target = %#v, want sandbox file without stdout identity", targets[1])
	}
}
