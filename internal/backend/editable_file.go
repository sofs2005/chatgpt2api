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
	urlpkg "net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	util.LogProgress(ctx, "正在初始化上游会话环境")
	if err := c.bootstrap(ctx); err != nil {
		return EditableFileExportResult{}, err
	}
	util.LogProgress(ctx, "正在获取上游 Chat Requirements")
	reqs, err := c.getChatRequirements(ctx)
	if err != nil {
		return EditableFileExportResult{}, err
	}
	refs := make([]uploadedImageRef, 0, len(base64Images))
	for i, raw := range base64Images {
		util.LogProgress(ctx, fmt.Sprintf("正在上传输入图片 %d/%d", i+1, len(base64Images)))
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
	util.LogProgress(ctx, "正在准备可编辑文件会话")
	conduitToken, err := c.prepareEditableFileConversation(ctx, prompt, reqs, refs)
	if err != nil {
		return EditableFileExportResult{}, err
	}
	util.LogProgress(ctx, "正在启动上游可编辑文件生成")
	resp, err := c.startEditableFileConversation(ctx, kind, prompt, conduitToken, reqs, refs)
	if err != nil {
		return EditableFileExportResult{}, err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, officialStreamPath); err != nil {
		return EditableFileExportResult{}, err
	}
	util.LogProgress(ctx, "正在读取上游 SSE 响应")
	conversationID, assets, err := collectEditableFileAssets(ctx, resp.Body)
	if err != nil {
		return EditableFileExportResult{}, err
	}
	if conversationID != "" {
		util.LogProgress(ctx, "已获取上游会话 ID："+conversationID)
	}
	if len(assets) == 0 {
		if conversationID == "" {
			return EditableFileExportResult{}, fmt.Errorf("upstream completed without editable file asset")
		}
		util.LogProgress(ctx, "SSE 未包含文件内容，开始轮询会话详情")
		targets, err := c.waitEditableFileTargets(ctx, conversationID, kind)
		if err != nil {
			return EditableFileExportResult{ConversationID: conversationID}, err
		}
		if len(targets) == 0 {
			return EditableFileExportResult{ConversationID: conversationID}, fmt.Errorf("upstream completed without editable file asset")
		}
		util.LogProgress(ctx, fmt.Sprintf("已找到 %d 个可下载文件，开始下载", len(targets)))
		assets = make([]editableAsset, 0, len(targets))
		for _, target := range targets {
			util.LogProgress(ctx, "正在下载文件："+target.FileName)
			data, err := c.downloadEditableArtifact(ctx, conversationID, target)
			if err != nil {
				return EditableFileExportResult{ConversationID: conversationID}, err
			}
			assets = append(assets, editableAsset{FileName: target.FileName, Data: data})
		}
	}
	util.LogProgress(ctx, fmt.Sprintf("正在写入 %d 个文件到本地", len(assets)))
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return EditableFileExportResult{}, err
	}
	var primaryPath string
	var zipPath string
	writtenPaths := make([]string, 0, len(assets))
	for i, asset := range assets {
		name := safeEditableFileName(asset.FileName, kind, i+1)
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, asset.Data, 0o644); err != nil {
			return EditableFileExportResult{}, err
		}
		writtenPaths = append(writtenPaths, path)
		artifact := editableArtifact{FileName: name}
		if primaryPath == "" && editableArtifactIsPrimary(kind, artifact) {
			primaryPath = path
		}
		if zipPath == "" && editableArtifactIsZip(artifact) {
			zipPath = path
		}
	}
	if primaryPath == "" && len(writtenPaths) > 0 {
		primaryPath = writtenPaths[0]
	}
	if zipPath == "" && len(assets) > 1 {
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

type editableArtifact struct {
	AttachmentID string
	FileID       string
	FileName     string
	MIMEType     string
	CreateTime   float64
	AuthorRole   string
	SandboxPath  string
	MessageID    string
}

var editableSandboxPathRE = regexp.MustCompile(`(?:sandbox:)?(/mnt/data/[^\s"'\)\]]+\.(?:pptx?|psd|zip))`)

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

func editableArtifactsFromAny(value any) []editableArtifact {
	var artifacts []editableArtifact
	var walk func(any)
	walk = func(current any) {
		switch v := current.(type) {
		case map[string]any:
			if artifact, ok := editableArtifactFromMap(v, nil); ok {
				artifacts = append(artifacts, artifact)
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
	return artifacts
}

func editableArtifactsFromConversation(conversation map[string]any, kind string) []editableArtifact {
	if len(conversation) == 0 {
		return nil
	}
	mapping, _ := conversation["mapping"].(map[string]any)
	if len(mapping) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(mapping))
	for _, raw := range mapping {
		node, _ := raw.(map[string]any)
		if len(node) == 0 {
			continue
		}
		message, _ := node["message"].(map[string]any)
		if len(message) == 0 {
			continue
		}
		items = append(items, message)
	}
	sort.Slice(items, func(i, j int) bool {
		return editableMessageCreateTime(items[i]) < editableMessageCreateTime(items[j])
	})
	artifacts := make(map[string]editableArtifact)
	for _, message := range items {
		artifactList := editableArtifactsFromMessage(message, kind)
		for _, artifact := range artifactList {
			key := firstNonEmpty(artifact.AttachmentID, artifact.FileID, artifact.FileName, artifact.SandboxPath)
			if key == "" {
				continue
			}
			artifacts[key] = mergeEditableArtifact(artifacts[key], artifact)
		}
	}
	result := make([]editableArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, artifact)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreateTime == result[j].CreateTime {
			return result[i].FileName < result[j].FileName
		}
		return result[i].CreateTime < result[j].CreateTime
	})
	return result
}

func pickEditableTargetArtifacts(artifacts []editableArtifact, kind string) []editableArtifact {
	var primary []editableArtifact
	var zip []editableArtifact
	for _, artifact := range artifacts {
		if editableArtifactIsZip(artifact) {
			zip = append(zip, artifact)
			continue
		}
		if editableArtifactIsPrimary(kind, artifact) {
			primary = append(primary, artifact)
		}
	}
	if len(primary) == 0 {
		return nil
	}
	result := make([]editableArtifact, 0, 2)
	result = append(result, primary[0])
	if len(zip) > 0 {
		result = append(result, zip[0])
	}
	return result
}

type editableConversationPollRetryError struct {
	Delay time.Duration
}

func (e editableConversationPollRetryError) Error() string {
	return "editable conversation poll rate limited"
}

func (c *Client) waitEditableFileTargets(ctx context.Context, conversationID, kind string) ([]editableArtifact, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, nil
	}
	timeout := time.NewTimer(20 * time.Minute)
	defer timeout.Stop()
	delay := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("timed out waiting for editable file assets")
		default:
		}
		util.LogProgress(ctx, "正在轮询上游会话详情")
		conversation, err := c.fetchEditableConversationDetail(ctx, conversationID)
		if err != nil {
			if retry, ok := err.(editableConversationPollRetryError); ok {
				delay = retry.Delay
				util.LogProgress(ctx, "会话详情暂不可用，稍后重试")
			} else {
				return nil, err
			}
		} else {
			artifacts := editableArtifactsFromConversation(conversation, kind)
			targets := pickEditableTargetArtifacts(artifacts, kind)
			if len(targets) > 0 {
				util.LogProgress(ctx, fmt.Sprintf("会话详情中找到 %d 个目标文件", len(targets)))
				return targets, nil
			}
			util.LogProgress(ctx, fmt.Sprintf("会话详情暂未发现目标文件，已扫描到 %d 个候选项", len(artifacts)))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("timed out waiting for editable file assets")
		case <-time.After(delay):
		}
		delay = 5 * time.Second
	}
}

func (c *Client) fetchEditableConversationDetail(ctx context.Context, conversationID string) (map[string]any, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("conversation_id is required for editable file polling")
	}
	path := "/backend-api/conversation/" + urlpkg.PathEscape(conversationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range c.headers(path, map[string]string{
		"Accept":                "application/json",
		"Referer":               c.BaseURL + "/c/" + urlpkg.PathEscape(conversationID),
		"X-OpenAI-Target-Route": "/backend-api/conversation/{conversation_id}",
		"X-OpenAI-Target-Path":  path,
		"Cache-Control":         "no-cache",
		"Pragma":                "no-cache",
	}) {
		req.Header.Set(key, value)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, upstreamTransportError(path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		io.Copy(io.Discard, resp.Body)
		return nil, editableConversationPollRetryError{Delay: 5 * time.Second}
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusLocked || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusInternalServerError || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout {
		io.Copy(io.Discard, resp.Body)
		return nil, editableConversationPollRetryError{Delay: 5 * time.Second}
	}
	if err := ensureOK(resp, path); err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *Client) downloadEditableArtifact(ctx context.Context, conversationID string, artifact editableArtifact) ([]byte, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("conversation_id is required for editable file download")
	}
	if artifact.SandboxPath != "" && artifact.MessageID != "" {
		if data, err := c.downloadEditableInterpreterAsset(ctx, conversationID, artifact.MessageID, artifact.SandboxPath); err == nil {
			return data, nil
		}
	}
	for _, attachmentID := range uniqueNonEmptyStrings(artifact.AttachmentID, artifact.FileID) {
		if data, err := c.downloadEditableAttachmentAsset(ctx, conversationID, attachmentID); err == nil {
			return data, nil
		}
	}
	for _, fileID := range uniqueNonEmptyStrings(artifact.FileID, artifact.AttachmentID) {
		if data, err := c.downloadEditableFileAsset(ctx, fileID); err == nil {
			return data, nil
		}
	}
	if artifact.SandboxPath != "" && artifact.MessageID != "" {
		return nil, fmt.Errorf("editable file asset %s could not be downloaded", artifact.FileName)
	}
	return nil, fmt.Errorf("editable file asset %s could not be downloaded", artifact.FileName)
}

func (c *Client) downloadEditableInterpreterAsset(ctx context.Context, conversationID, messageID, sandboxPath string) ([]byte, error) {
	path := "/backend-api/conversation/" + urlpkg.PathEscape(conversationID) + "/interpreter/download"
	query := urlpkg.Values{}
	query.Set("message_id", messageID)
	query.Set("sandbox_path", sandboxPath)
	return c.downloadEditableEndpoint(ctx, path+"?"+query.Encode(), map[string]string{"Accept": "application/json, */*"})
}

func (c *Client) downloadEditableAttachmentAsset(ctx context.Context, conversationID, attachmentID string) ([]byte, error) {
	path := "/backend-api/conversation/" + urlpkg.PathEscape(conversationID) + "/attachment/" + urlpkg.PathEscape(attachmentID) + "/download"
	return c.downloadEditableEndpoint(ctx, path, map[string]string{"Accept": "application/json, */*"})
}

func (c *Client) downloadEditableFileAsset(ctx context.Context, fileID string) ([]byte, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, fmt.Errorf("file_id is required for editable file download")
	}
	path := "/backend-api/files/download/" + urlpkg.PathEscape(fileID)
	query := urlpkg.Values{}
	query.Set("post_id", "")
	query.Set("inline", "false")
	if data, err := c.downloadEditableEndpoint(ctx, path+"?"+query.Encode(), map[string]string{"Accept": "application/json, */*"}); err == nil {
		return data, nil
	}
	return c.downloadEditableEndpoint(ctx, "/backend-api/files/"+urlpkg.PathEscape(fileID)+"/download", map[string]string{"Accept": "application/json, */*"})
}

func (c *Client) downloadEditableEndpoint(ctx context.Context, path string, extra map[string]string) ([]byte, error) {
	target := path
	parsed, err := urlpkg.Parse(target)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		base, baseErr := urlpkg.Parse(c.BaseURL)
		if baseErr != nil {
			return nil, baseErr
		}
		parsed = base.ResolveReference(parsed)
		target = parsed.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if c.isChatGPTBackendURL(parsed) {
		for key, value := range c.headers(parsed.EscapedPath(), extra) {
			req.Header.Set(key, value)
		}
	} else if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
		for key, value := range extra {
			req.Header.Set(key, value)
		}
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, upstreamTransportError(path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if location := strings.TrimSpace(resp.Header.Get("Location")); location != "" {
			return c.downloadEditableURL(ctx, location)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, upstreamHTTPError(path, resp.StatusCode, data)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if downloadURL := editableDownloadURLFromBody(data); downloadURL != "" {
		return c.downloadEditableURL(ctx, downloadURL)
	}
	return data, nil
}

func (c *Client) downloadEditableURL(ctx context.Context, target string) ([]byte, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("editable download url is empty")
	}
	parsed, err := urlpkg.Parse(target)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		base, baseErr := urlpkg.Parse(c.BaseURL)
		if baseErr != nil {
			return nil, baseErr
		}
		parsed = base.ResolveReference(parsed)
		target = parsed.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if c.isChatGPTBackendURL(parsed) {
		for key, value := range c.headers(parsed.EscapedPath(), map[string]string{"Accept": "*/*"}) {
			req.Header.Set(key, value)
		}
	} else if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "*/*")
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, upstreamTransportError("editable_download", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, upstreamHTTPError("editable_download", resp.StatusCode, data)
	}
	return io.ReadAll(resp.Body)
}

func editableDownloadURLFromBody(data []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err == nil {
		if url := firstNonEmpty(util.Clean(payload["download_url"]), util.Clean(payload["downloadUrl"]), util.Clean(payload["url"])); url != "" {
			return url
		}
	}
	text := strings.TrimSpace(string(data))
	if text == "" || strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return ""
	}
	if parsed, err := urlpkg.ParseRequestURI(text); err == nil && parsed.Scheme != "" {
		return text
	}
	return ""
}

func uniqueNonEmptyStrings(values ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func editableArtifactsFromMessage(message map[string]any, kind string) []editableArtifact {
	if len(message) == 0 {
		return nil
	}
	createTime := editableMessageCreateTime(message)
	authorRole := strings.ToLower(strings.TrimSpace(util.Clean(message["author"])))
	if author, ok := message["author"].(map[string]any); ok {
		authorRole = strings.ToLower(strings.TrimSpace(util.Clean(author["role"])))
	}
	messageID := firstNonEmpty(util.Clean(message["id"]), util.Clean(message["message_id"]))
	var artifacts []editableArtifact
	if metadata, ok := message["metadata"].(map[string]any); ok {
		artifacts = append(artifacts, editableArtifactsFromAttachmentContainer(metadata, createTime, authorRole, messageID)...)
	}
	artifacts = append(artifacts, editableArtifactsFromAttachmentContainer(message, createTime, authorRole, messageID)...)
	if text := editableMessageText(message); text != "" {
		for _, path := range editableSandboxPaths(text) {
			artifacts = append(artifacts, editableArtifact{
				FileName:    filepath.Base(path),
				CreateTime:  createTime,
				AuthorRole:  authorRole,
				SandboxPath: path,
				MessageID:   messageID,
			})
		}
	}
	return artifacts
}

func editableArtifactsFromAttachmentContainer(value map[string]any, createTime float64, authorRole, messageID string) []editableArtifact {
	var artifacts []editableArtifact
	for _, key := range []string{"attachments", "files", "assets"} {
		raw, ok := value[key]
		if !ok {
			continue
		}
		artifacts = append(artifacts, editableArtifactsFromAny(raw)...)
	}
	if artifact, ok := editableArtifactFromMap(value, nil); ok {
		artifact.CreateTime = createTime
		artifact.AuthorRole = authorRole
		artifact.MessageID = messageID
		artifacts = append(artifacts, artifact)
	}
	return artifacts
}

func editableArtifactFromMap(value map[string]any, seen map[string]struct{}) (editableArtifact, bool) {
	name := firstNonEmpty(util.Clean(value["file_name"]), util.Clean(value["name"]), util.Clean(value["filename"]))
	if name == "" {
		return editableArtifact{}, false
	}
	artifact := editableArtifact{
		AttachmentID: firstNonEmpty(util.Clean(value["attachment_id"]), util.Clean(value["id"]), util.Clean(value["file_id"])),
		FileID:       firstNonEmpty(util.Clean(value["file_id"]), util.Clean(value["library_file_id"]), util.Clean(value["id"])),
		FileName:     name,
		MIMEType:     firstNonEmpty(util.Clean(value["mime_type"]), util.Clean(value["mimeType"]), util.Clean(value["content_type"])),
		SandboxPath:  firstNonEmpty(util.Clean(value["sandbox_path"]), util.Clean(value["path"])),
	}
	if artifact.FileID == "" && artifact.AttachmentID != "" {
		artifact.FileID = artifact.AttachmentID
	}
	if artifact.SandboxPath == "" {
		if path := editableSandboxPathFromValue(value); path != "" {
			artifact.SandboxPath = path
		}
	}
	if artifact.FileID == "" && artifact.SandboxPath == "" {
		return editableArtifact{}, false
	}
	if seen != nil {
		key := firstNonEmpty(artifact.AttachmentID, artifact.FileID, artifact.FileName, artifact.SandboxPath)
		if key != "" {
			if _, ok := seen[key]; ok {
				return editableArtifact{}, false
			}
			seen[key] = struct{}{}
		}
	}
	return artifact, true
}

func mergeEditableArtifact(existing, incoming editableArtifact) editableArtifact {
	if existing.AttachmentID == "" {
		existing.AttachmentID = incoming.AttachmentID
	}
	if existing.FileID == "" {
		existing.FileID = incoming.FileID
	}
	if existing.FileName == "" {
		existing.FileName = incoming.FileName
	}
	if existing.MIMEType == "" {
		existing.MIMEType = incoming.MIMEType
	}
	if existing.SandboxPath == "" {
		existing.SandboxPath = incoming.SandboxPath
	}
	if existing.MessageID == "" {
		existing.MessageID = incoming.MessageID
	}
	if incoming.CreateTime > existing.CreateTime {
		existing.CreateTime = incoming.CreateTime
	}
	if existing.AuthorRole == "" {
		existing.AuthorRole = incoming.AuthorRole
	}
	return existing
}

func editableMessageCreateTime(message map[string]any) float64 {
	for _, key := range []string{"update_time", "create_time"} {
		switch value := message[key].(type) {
		case float64:
			if value > 0 {
				return value
			}
		case int:
			if value > 0 {
				return float64(value)
			}
		case int64:
			if value > 0 {
				return float64(value)
			}
		case json.Number:
			if parsed, err := strconv.ParseFloat(value.String(), 64); err == nil && parsed > 0 {
				return parsed
			}
		case string:
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func editableMessageText(message map[string]any) string {
	content, _ := message["content"].(map[string]any)
	if len(content) == 0 {
		return ""
	}
	parts, _ := content["parts"].([]any)
	if len(parts) == 0 {
		if text := util.Clean(content["text"]); text != "" {
			return text
		}
		if text := util.Clean(content["content"]); text != "" {
			return text
		}
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		if text := editablePlainText(part); text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(text)
		}
	}
	return strings.TrimSpace(b.String())
}

func editablePlainText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"text", "content", "value", "body"} {
			if text := util.Clean(v[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func editableSandboxPaths(text string) []string {
	matches := editableSandboxPathRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	paths := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func editableSandboxPathFromValue(value map[string]any) string {
	for _, key := range []string{"sandbox_path", "sandboxPath", "path"} {
		if path := util.Clean(value[key]); path != "" {
			return path
		}
	}
	for _, key := range []string{"text", "content", "body"} {
		if text := util.Clean(value[key]); text != "" {
			if path := firstEditableSandboxPath(text); path != "" {
				return path
			}
		}
	}
	for _, raw := range value {
		switch nested := raw.(type) {
		case string:
			if path := firstEditableSandboxPath(nested); path != "" {
				return path
			}
		case map[string]any:
			if path := editableSandboxPathFromValue(nested); path != "" {
				return path
			}
		case []any:
			for _, item := range nested {
				if path := editableSandboxPathFromAny(item); path != "" {
					return path
				}
			}
		}
	}
	return ""
}

func editableSandboxPathFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return firstEditableSandboxPath(v)
	case map[string]any:
		return editableSandboxPathFromValue(v)
	case []any:
		for _, item := range v {
			if path := editableSandboxPathFromAny(item); path != "" {
				return path
			}
		}
	}
	return ""
}

func firstEditableSandboxPath(text string) string {
	matches := editableSandboxPathRE.FindStringSubmatch(text)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func editableArtifactIsZip(artifact editableArtifact) bool {
	name := strings.ToLower(strings.TrimSpace(artifact.FileName))
	if strings.HasSuffix(name, ".zip") {
		return true
	}
	mime := strings.ToLower(strings.TrimSpace(artifact.MIMEType))
	return mime == "application/zip" || mime == "application/x-zip-compressed"
}

func editableArtifactIsPrimary(kind string, artifact editableArtifact) bool {
	name := strings.ToLower(strings.TrimSpace(artifact.FileName))
	mime := strings.ToLower(strings.TrimSpace(artifact.MIMEType))
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "psd":
		return strings.HasSuffix(name, ".psd") || mime == "image/vnd.adobe.photoshop" || mime == "application/vnd.adobe.photoshop"
	default:
		return strings.HasSuffix(name, ".ppt") || strings.HasSuffix(name, ".pptx") || mime == "application/vnd.openxmlformats-officedocument.presentationml.presentation" || mime == "application/vnd.ms-powerpoint"
	}
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
