package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/HugoSmits86/nativewebp"

	"chatgpt2api/internal/util"
)

const editableFileModel = "gpt-5-5-thinking"

type EditableFileImage struct {
	Data        []byte
	ContentType string
	Extension   string
	Width       int
	Height      int
	FileName    string
}

type EditableFileExportResult struct {
	ConversationID string
	PrimaryPath    string
	ZipPath        string
}

func DecodeEditableFileImage(value string) (EditableFileImage, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return EditableFileImage{}, fmt.Errorf("image is required")
	}
	contentType := ""
	dataPart := value
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		header, data, ok := strings.Cut(value, ",")
		if !ok {
			return EditableFileImage{}, fmt.Errorf("invalid data url")
		}
		dataPart = data
		contentType = strings.TrimPrefix(strings.Split(header, ";")[0], "data:")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataPart))
	if err != nil {
		return EditableFileImage{}, err
	}
	if len(data) == 0 {
		return EditableFileImage{}, fmt.Errorf("image data is empty")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return EditableFileImage{}, fmt.Errorf("image decode failed: %w", err)
	}
	if contentType == "" || !strings.HasPrefix(contentType, "image/") {
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "image/" + firstNonEmpty(format, "png")
	}
	ext := editableImageExtension(contentType, format)
	return EditableFileImage{Data: data, ContentType: contentType, Extension: ext, Width: cfg.Width, Height: cfg.Height, FileName: "image." + ext}, nil
}

func BuildEditableUploadCreatePayload(img EditableFileImage, fileName string) map[string]any {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		fileName = img.FileName
	}
	return map[string]any{
		"file_name":                fileName,
		"file_size":                len(img.Data),
		"use_case":                 "multimodal",
		"store_in_library":         true,
		"library_persistence_mode": "opportunistic",
		"width":                    img.Width,
		"height":                   img.Height,
	}
}

func BuildEditablePreparePayload(prompt string, refs []uploadedImageRef) map[string]any {
	contentType := "text"
	parts := []any{strings.TrimSpace(prompt)}
	if len(refs) > 0 {
		contentType = "multimodal_text"
		parts = editableMessageParts(prompt, refs)
	}
	return map[string]any{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     util.NewUUID(),
		"model":                 editableFileModel,
		"thinking_effort":       "extended",
		"client_prepare_state":  "success",
		"timezone_offset_min":   -480,
		"timezone":              "Asia/Shanghai",
		"conversation_mode":     map[string]any{"kind": "primary_assistant"},
		"system_hints":          []any{},
		"partial_query": map[string]any{
			"id":      util.NewUUID(),
			"author":  map[string]any{"role": "user"},
			"content": map[string]any{"content_type": contentType, "parts": parts},
		},
		"supports_buffering":  true,
		"supported_encodings": []any{"v1"},
		"client_contextual_info": map[string]any{
			"app_name": "chatgpt.com",
		},
	}
}

func BuildEditableConversationPayload(kind, prompt, conduitToken string, refs []uploadedImageRef) map[string]any {
	contentType := "text"
	parts := []any{strings.TrimSpace(prompt)}
	if len(refs) > 0 {
		contentType = "multimodal_text"
		parts = editableMessageParts(prompt, refs)
	}
	metadata := map[string]any{
		"developer_mode_connector_ids": []any{},
		"selected_github_repos":        []any{},
		"selected_all_github_repos":    false,
		"serialization_metadata":       map[string]any{"custom_symbol_offsets": []any{}},
		"editable_file_kind":           strings.ToLower(strings.TrimSpace(kind)),
	}
	if len(refs) > 0 {
		metadata["attachments"] = editableAttachments(refs)
	}
	return map[string]any{
		"action": "next",
		"messages": []any{map[string]any{
			"id":          util.NewUUID(),
			"author":      map[string]any{"role": "user"},
			"create_time": float64(time.Now().UnixNano()) / 1e9,
			"content":     map[string]any{"content_type": contentType, "parts": parts},
			"metadata":    metadata,
		}},
		"parent_message_id":                    util.NewUUID(),
		"model":                                editableFileModel,
		"thinking_effort":                      "extended",
		"client_prepare_state":                 "sent",
		"conduit_token":                        conduitToken,
		"timezone_offset_min":                  -480,
		"timezone":                             "Asia/Shanghai",
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []any{},
		"supports_buffering":                   true,
		"supported_encodings":                  []any{"v1"},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"force_use_sse":                        true,
		"client_contextual_info": map[string]any{
			"is_dark_mode":      false,
			"time_since_loaded": 1200,
			"page_height":       1072,
			"page_width":        1724,
			"pixel_ratio":       1.2,
			"screen_height":     1440,
			"screen_width":      2560,
			"app_name":          "chatgpt.com",
		},
	}
}

func (c *Client) ExportEditableFile(ctx context.Context, kind, prompt string, base64Images []string, outputDir string) (EditableFileExportResult, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "ppt" && kind != "psd" {
		return EditableFileExportResult{}, fmt.Errorf("kind must be ppt or psd")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return EditableFileExportResult{}, fmt.Errorf("prompt is required")
	}
	if c.AccessToken == "" {
		return EditableFileExportResult{}, fmt.Errorf("access_token is required for editable file export")
	}
	if err := c.bootstrap(ctx); err != nil {
		return EditableFileExportResult{}, err
	}
	reqs, err := c.getChatRequirements(ctx)
	if err != nil {
		return EditableFileExportResult{}, err
	}
	refs := make([]uploadedImageRef, 0, len(base64Images))
	for i, raw := range base64Images {
		img, err := DecodeEditableFileImage(raw)
		if err != nil {
			return EditableFileExportResult{}, err
		}
		fileName := fmt.Sprintf("editable_input_%d.%s", i+1, img.Extension)
		ref, err := c.uploadEditableImage(ctx, img, fileName)
		if err != nil {
			return EditableFileExportResult{}, err
		}
		refs = append(refs, ref)
	}
	conduitToken, err := c.prepareEditableFileConversation(ctx, prompt, reqs, refs)
	if err != nil {
		return EditableFileExportResult{}, err
	}
	resp, err := c.startEditableFileConversation(ctx, kind, prompt, conduitToken, reqs, refs)
	if err != nil {
		return EditableFileExportResult{}, err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, officialStreamPath); err != nil {
		return EditableFileExportResult{}, err
	}
	conversationID, assets, err := collectEditableFileAssets(ctx, resp.Body)
	if err != nil {
		return EditableFileExportResult{}, err
	}
	if len(assets) == 0 {
		return EditableFileExportResult{ConversationID: conversationID}, fmt.Errorf("upstream completed without editable file asset")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return EditableFileExportResult{}, err
	}
	var primaryPath string
	for i, asset := range assets {
		name := safeEditableFileName(asset.FileName, kind, i+1)
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, asset.Data, 0o644); err != nil {
			return EditableFileExportResult{}, err
		}
		if primaryPath == "" {
			primaryPath = path
		}
	}
	zipPath := ""
	if len(assets) > 1 {
		zipPath = filepath.Join(outputDir, "editable_files.zip")
		if err := writeEditableZip(zipPath, assets, kind); err != nil {
			return EditableFileExportResult{}, err
		}
	}
	return EditableFileExportResult{ConversationID: conversationID, PrimaryPath: primaryPath, ZipPath: zipPath}, nil
}

func editableMessageParts(prompt string, refs []uploadedImageRef) []any {
	parts := make([]any, 0, len(refs)+1)
	parts = append(parts, strings.TrimSpace(prompt))
	for _, ref := range refs {
		parts = append(parts, map[string]any{
			"content_type":    "image_asset_pointer",
			"asset_pointer":   "sediment://" + ref.FileID,
			"width":           ref.Width,
			"height":          ref.Height,
			"size_bytes":      ref.FileSize,
			"library_file_id": ref.FileID,
		})
	}
	return parts
}

func editableAttachments(refs []uploadedImageRef) []map[string]any {
	attachments := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		attachments = append(attachments, map[string]any{
			"id":              ref.FileID,
			"mimeType":        ref.MIMEType,
			"name":            ref.FileName,
			"size":            ref.FileSize,
			"width":           ref.Width,
			"height":          ref.Height,
			"library_file_id": ref.FileID,
		})
	}
	return attachments
}

func (c *Client) uploadEditableImage(ctx context.Context, img EditableFileImage, fileName string) (uploadedImageRef, error) {
	path := "/backend-api/files"
	resp, err := c.postJSON(ctx, path, BuildEditableUploadCreatePayload(img, fileName), c.headers(path, map[string]string{"Content-Type": "application/json", "Accept": "application/json"}), false)
	if err != nil {
		return uploadedImageRef{}, err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, path); err != nil {
		return uploadedImageRef{}, err
	}
	var uploaded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		return uploadedImageRef{}, err
	}
	uploadURL := util.Clean(uploaded["upload_url"])
	fileID := firstNonEmpty(util.Clean(uploaded["file_id"]), util.Clean(uploaded["id"]), util.Clean(uploaded["library_file_id"]))
	if uploadURL == "" || fileID == "" {
		return uploadedImageRef{}, fmt.Errorf("file upload metadata incomplete")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(img.Data))
	for key, value := range map[string]string{"Content-Type": img.ContentType, "x-ms-blob-type": "BlockBlob", "x-ms-version": "2020-04-08", "Origin": c.BaseURL, "Referer": c.BaseURL + "/", "User-Agent": c.userAgent, "Accept": "application/json, text/plain, */*"} {
		req.Header.Set(key, value)
	}
	uploadResp, err := c.do(req)
	if err != nil {
		return uploadedImageRef{}, upstreamTransportError("editable_image_upload", err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode < 200 || uploadResp.StatusCode >= 300 {
		data, _ := io.ReadAll(uploadResp.Body)
		return uploadedImageRef{}, upstreamHTTPError("editable_image_upload", uploadResp.StatusCode, data)
	}
	finalizePath := "/backend-api/files/" + fileID + "/uploaded"
	finalizeResp, err := c.postJSON(ctx, finalizePath, map[string]any{}, c.headers(finalizePath, map[string]string{"Content-Type": "application/json", "Accept": "application/json"}), false)
	if err != nil {
		return uploadedImageRef{}, err
	}
	defer finalizeResp.Body.Close()
	if err := ensureOK(finalizeResp, finalizePath); err != nil {
		return uploadedImageRef{}, err
	}
	return uploadedImageRef{FileID: fileID, FileName: fileName, FileSize: len(img.Data), MIMEType: img.ContentType, Width: img.Width, Height: img.Height}, nil
}

func (c *Client) prepareEditableFileConversation(ctx context.Context, prompt string, reqs ChatRequirements, refs []uploadedImageRef) (string, error) {
	resp, err := c.postJSON(ctx, officialPreparePath, BuildEditablePreparePayload(prompt, refs), c.officialHeaders(officialPreparePath, reqs, "", "*/*"), false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, officialPreparePath); err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	return util.Clean(data["conduit_token"]), nil
}

func (c *Client) startEditableFileConversation(ctx context.Context, kind, prompt, conduitToken string, reqs ChatRequirements, refs []uploadedImageRef) (*http.Response, error) {
	payload := BuildEditableConversationPayload(kind, prompt, conduitToken, refs)
	return c.postJSON(ctx, officialStreamPath, payload, c.officialHeaders(officialStreamPath, reqs, conduitToken, "text/event-stream"), true)
}

type editableAsset struct {
	FileName string
	Data     []byte
}

func collectEditableFileAssets(ctx context.Context, reader io.Reader) (string, []editableAsset, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, err
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	var conversationID string
	var assets []editableAsset
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event any
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		if id := editableConversationID(event); id != "" {
			conversationID = id
		}
		for _, asset := range editableAssetsFromValue(ctx, event, seen) {
			assets = append(assets, asset)
		}
	}
	return conversationID, assets, nil
}

func editableConversationID(value any) string {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"conversation_id", "conversationId"} {
			if id := util.Clean(v[key]); id != "" {
				return id
			}
		}
		for _, nested := range v {
			if id := editableConversationID(nested); id != "" {
				return id
			}
		}
	case []any:
		for _, nested := range v {
			if id := editableConversationID(nested); id != "" {
				return id
			}
		}
	case []map[string]any:
		for _, nested := range v {
			if id := editableConversationID(nested); id != "" {
				return id
			}
		}
	}
	return ""
}

func editableAssetsFromValue(ctx context.Context, value any, seen map[string]struct{}) []editableAsset {
	if err := ctx.Err(); err != nil {
		return nil
	}
	var assets []editableAsset
	var walk func(any)
	walk = func(current any) {
		if err := ctx.Err(); err != nil {
			return
		}
		switch v := current.(type) {
		case map[string]any:
			if asset, ok := editableAssetFromMap(v, seen); ok {
				assets = append(assets, asset)
			}
			for _, nested := range v {
				walk(nested)
			}
		case []any:
			for _, nested := range v {
				walk(nested)
			}
		case []map[string]any:
			for _, nested := range v {
				walk(nested)
			}
		}
	}
	walk(value)
	return assets
}

func editableAssetFromMap(value map[string]any, seen map[string]struct{}) (editableAsset, bool) {
	name := firstNonEmpty(util.Clean(value["file_name"]), util.Clean(value["name"]), util.Clean(value["filename"]))
	if name == "" {
		return editableAsset{}, false
	}
	data := editableAssetData(value)
	if len(data) == 0 {
		return editableAsset{}, false
	}
	if seen != nil {
		key := name + "\x00" + string(data)
		if _, ok := seen[key]; ok {
			return editableAsset{}, false
		}
		seen[key] = struct{}{}
	}
	return editableAsset{FileName: name, Data: data}, true
}

func editableAssetData(value map[string]any) []byte {
	for _, key := range []string{"data", "b64_json", "base64", "bytes", "body", "text", "content"} {
		raw, ok := value[key]
		if !ok {
			continue
		}
		switch data := raw.(type) {
		case []byte:
			if len(data) > 0 {
				return append([]byte(nil), data...)
			}
		case string:
			trimmed := strings.TrimSpace(data)
			if trimmed == "" {
				continue
			}
			if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(decoded) > 0 {
				return decoded
			}
			return []byte(trimmed)
		}
	}
	return nil
}

func looksLikeEditableFileURL(value string) bool {
	lower := strings.ToLower(value)
	for _, ext := range []string{".ppt", ".pptx", ".psd", ".zip"} {
		if strings.Contains(lower, ext) {
			return true
		}
	}
	return false
}

func writeEditableZip(path string, assets []editableAsset, kind string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	zw := zip.NewWriter(file)
	defer zw.Close()
	for i, asset := range assets {
		writer, err := zw.Create(safeEditableFileName(asset.FileName, kind, i+1))
		if err != nil {
			return err
		}
		if _, err := writer.Write(asset.Data); err != nil {
			return err
		}
	}
	return nil
}

func safeEditableFileName(name, kind string, index int) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "." || name == "" || strings.Contains(name, "..") {
		ext := ".pptx"
		if strings.ToLower(strings.TrimSpace(kind)) == "psd" {
			ext = ".psd"
		}
		name = fmt.Sprintf("editable_%d%s", index, ext)
	}
	return name
}

func editableImageExtension(contentType, format string) string {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch contentType {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/png":
		return "png"
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "jpg":
		return "jpg"
	case "webp", "gif", "png":
		return strings.ToLower(strings.TrimSpace(format))
	default:
		return "png"
	}
}
