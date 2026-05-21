package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"chatgpt2api/internal/contextoffload"
	"chatgpt2api/internal/util"
)

const (
	textFileCreatePath  = "/backend-api/files"
	textFileProcessPath = "/backend-api/files/process_upload_stream"
)

type TextAttachmentRef struct {
	FileID   string
	FileName string
	MIMEType string
	Size     int
}

func (r TextAttachmentRef) PrepareAttachment() map[string]any {
	return map[string]any{"file_id": r.FileID}
}

func (r TextAttachmentRef) MessageAttachment() map[string]any {
	return map[string]any{"id": r.FileID, "mimeType": r.MIMEType, "name": r.FileName, "size": r.Size}
}

func buildTextPrepareAttachments(refs []TextAttachmentRef) []map[string]any {
	attachments := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		attachments = append(attachments, ref.PrepareAttachment())
	}
	return attachments
}

func buildTextMessageAttachments(refs []TextAttachmentRef) []map[string]any {
	attachments := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		attachments = append(attachments, ref.MessageAttachment())
	}
	return attachments
}

func (c *Client) uploadTextContextFiles(ctx context.Context, files []contextoffload.File, reqs ChatRequirements, timeout time.Duration) ([]TextAttachmentRef, error) {
	refs := make([]TextAttachmentRef, 0, len(files))
	for _, file := range files {
		ref, err := c.uploadTextContextFile(ctx, file, reqs, timeout)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (c *Client) uploadTextContextFile(ctx context.Context, file contextoffload.File, reqs ChatRequirements, timeout time.Duration) (TextAttachmentRef, error) {
	filename := strings.TrimSpace(file.Filename)
	if filename == "" {
		filename = "context.txt"
	}
	mimeType := firstNonEmpty(strings.TrimSpace(file.ContentType), "text/plain")
	content := []byte(file.Text)
	createResp, err := c.createTextContextFile(ctx, filename, mimeType, len(content), reqs)
	if err != nil {
		return TextAttachmentRef{}, err
	}
	if err := c.putTextContextFile(ctx, createResp.UploadURL, mimeType, content); err != nil {
		return TextAttachmentRef{}, err
	}
	if err := c.processTextContextFile(ctx, createResp.FileID, filename, reqs, timeout); err != nil {
		return TextAttachmentRef{}, err
	}
	return TextAttachmentRef{FileID: createResp.FileID, FileName: filename, MIMEType: mimeType, Size: len(content)}, nil
}

type textContextFileCreateResponse struct {
	Status    string `json:"status"`
	UploadURL string `json:"upload_url"`
	FileID    string `json:"file_id"`
}

func (c *Client) createTextContextFile(ctx context.Context, filename, mimeType string, size int, reqs ChatRequirements) (textContextFileCreateResponse, error) {
	payload := map[string]any{
		"file_name":                filename,
		"file_size":                size,
		"use_case":                 "my_files",
		"timezone_offset_min":      -480,
		"reset_rate_limits":        false,
		"mime_type":                mimeType,
		"store_in_library":         true,
		"library_persistence_mode": "opportunistic",
	}
	resp, err := c.postJSON(ctx, textFileCreatePath, payload, c.officialHeaders(textFileCreatePath, reqs, "", "application/json"), false)
	if err != nil {
		return textContextFileCreateResponse{}, err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, textFileCreatePath); err != nil {
		return textContextFileCreateResponse{}, err
	}
	var data textContextFileCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return textContextFileCreateResponse{}, err
	}
	if strings.TrimSpace(data.FileID) == "" || strings.TrimSpace(data.UploadURL) == "" {
		return textContextFileCreateResponse{}, fmt.Errorf("%s failed: missing file_id or upload_url", textFileCreatePath)
	}
	return data, nil
}

func (c *Client) putTextContextFile(ctx context.Context, uploadURL, mimeType string, content []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("x-ms-version", "2020-04-08")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return upstreamTransportError("text_context_blob_put", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(resp.Body)
		return upstreamHTTPError("text_context_blob_put", resp.StatusCode, data)
	}
	return nil
}

func (c *Client) processTextContextFile(ctx context.Context, fileID, filename string, reqs ChatRequirements, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	processCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload := map[string]any{
		"file_id":                  fileID,
		"use_case":                 "my_files",
		"index_for_retrieval":      true,
		"file_name":                filename,
		"library_persistence_mode": "opportunistic",
		"metadata": map[string]any{
			"store_in_library":  true,
			"is_temporary_chat": false,
		},
		"entry_surface": "chat_composer",
	}
	resp, err := c.postJSON(processCtx, textFileProcessPath, payload, c.officialHeaders(textFileProcessPath, reqs, "", "text/event-stream"), true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, textFileProcessPath); err != nil {
		return err
	}
	return waitTextContextIndexing(processCtx, resp.Body)
}

func waitTextContextIndexing(ctx context.Context, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		switch util.Clean(event["event"]) {
		case "file.indexing.completed":
			return nil
		case "file.processing.failed", "file.indexing.failed":
			return fmt.Errorf("%s failed: %s", textFileProcessPath, util.Clean(event["message"]))
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%s failed: indexing did not complete", textFileProcessPath)
}
