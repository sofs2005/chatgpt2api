package protocol

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

type ImageConfig interface {
	ImagesDir() string
	ImageMetadataDir() string
	BaseURL() string
	CleanupOldImages() int
}

type Engine struct {
	Accounts *service.AccountService
	Config   ImageConfig
	Storage  storage.JSONDocumentBackend
	Proxy    *service.ProxyService
	Logger   *service.Logger

	ListModelsFunc func(context.Context) (map[string]any, error)

	responseContextMu sync.Mutex
	ResponseContexts  *ResponseContextStore
}

type ConversationRequest struct {
	Model              string
	Prompt             string
	Messages           []map[string]any
	Images             []string
	N                  int
	Size               string
	Quality            string
	ResponseFormat     string
	BaseURL            string
	OwnerID            string
	OwnerName          string
	MessageAsError     bool
	RequirePaidAccount bool
}

func (r ConversationRequest) Normalized() ConversationRequest {
	r.Quality = ImageQualityForModel(r.Model, r.Quality)
	return r
}

func ImageQualityForModel(model, quality string) string {
	if strings.TrimSpace(model) == util.ImageModelCodex {
		return ""
	}
	return strings.TrimSpace(quality)
}

type ConversationState struct {
	Text           string
	ConversationID string
	FileIDs        []string
	SedimentIDs    []string
	Blocked        bool
	ToolInvoked    *bool
	TurnUseCase    string
}

type ConversationEvent map[string]any

type ImageOutput struct {
	Kind              string
	Model             string
	Index             int
	Total             int
	Created           int64
	Text              string
	UpstreamEventType string
	Data              []map[string]any
}

type ImageGenerationError struct {
	Message    string
	StatusCode int
	Type       string
	Code       string
	Param      any
}

func (e *ImageGenerationError) Error() string { return e.Message }

func (e *ImageGenerationError) OpenAIError() map[string]any {
	return map[string]any{"error": map[string]any{"message": e.Message, "type": e.Type, "param": e.Param, "code": e.Code}}
}

func NewImageGenerationError(message string) *ImageGenerationError {
	return &ImageGenerationError{Message: message, StatusCode: 502, Type: "server_error", Code: "upstream_error"}
}

func imageStreamErrorMessage(message string) string {
	text := strings.TrimSpace(message)
	lower := strings.ToLower(text)
	if strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare challenge") {
		return "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy"
	}
	if detail, ok := util.SummarizeUpstreamConnectionError(text); ok {
		return detail
	}
	if text == "" {
		return "image generation failed"
	}
	return text
}

func (o ImageOutput) Chunk() map[string]any {
	chunk := map[string]any{
		"object":              "image.generation.chunk",
		"created":             o.Created,
		"model":               o.Model,
		"index":               o.Index,
		"total":               o.Total,
		"progress_text":       o.Text,
		"upstream_event_type": o.UpstreamEventType,
		"data":                []map[string]any{},
	}
	switch o.Kind {
	case "message":
		chunk["object"] = "image.generation.message"
		chunk["message"] = o.Text
		delete(chunk, "progress_text")
		delete(chunk, "upstream_event_type")
	case "result":
		chunk["object"] = "image.generation.result"
		chunk["data"] = o.Data
		delete(chunk, "progress_text")
		delete(chunk, "upstream_event_type")
	}
	return chunk
}

func (e *Engine) TextBackend(accessToken string) *backend.Client {
	return backend.NewClient(accessToken, e.Accounts, e.Proxy)
}

func (e *Engine) ListModels(ctx context.Context) (map[string]any, error) {
	result, err := e.listModels(ctx)
	if err != nil {
		return nil, err
	}
	data := util.AsMapSlice(result["data"])
	seen := map[string]struct{}{}
	for _, item := range data {
		if id := util.Clean(item["id"]); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, model := range util.ModelList() {
		if _, ok := seen[model]; !ok {
			data = append(data, map[string]any{"id": model, "object": "model", "created": 0, "owned_by": "chatgpt2api", "permission": []any{}, "root": model, "parent": nil})
		}
	}
	result["data"] = data
	return result, nil
}

func (e *Engine) listModels(ctx context.Context) (map[string]any, error) {
	if e != nil && e.ListModelsFunc != nil {
		return e.ListModelsFunc(ctx)
	}
	return backend.NewClient("", e.Accounts, e.Proxy).ListModels(ctx)
}

func (e *Engine) StreamTextDeltas(ctx context.Context, client *backend.Client, request ConversationRequest) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		events, convErr := e.ConversationEvents(ctx, client, request.Messages, request.Model, request.Prompt, nil, "", "")
		for event := range events {
			if event["type"] != "conversation.delta" {
				continue
			}
			delta := util.Clean(event["delta"])
			if delta == "" {
				continue
			}
			select {
			case out <- delta:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
		if err := <-convErr; err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	return out, errCh
}

func (e *Engine) CollectText(ctx context.Context, client *backend.Client, request ConversationRequest) (string, error) {
	deltas, errCh := e.StreamTextDeltas(ctx, client, request)
	var parts []string
	for delta := range deltas {
		parts = append(parts, delta)
	}
	return strings.Join(parts, ""), <-errCh
}

func (e *Engine) ConversationEvents(ctx context.Context, client *backend.Client, messages []map[string]any, model, prompt string, images []string, size, quality string) (<-chan ConversationEvent, <-chan error) {
	out := make(chan ConversationEvent)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		normalized := NormalizeMessages(messages, nil)
		if len(normalized) == 0 && prompt != "" {
			normalized = []map[string]any{{"role": "user", "content": prompt}}
		}
		imageModel := util.IsImageModel(model) || (prompt != "" && util.IsImageGenerationModel(model))
		historyText := ""
		historyMessages := []string{}
		finalPrompt := prompt
		systemHints := []string{}
		streamImages := []string(nil)
		if imageModel {
			finalPrompt = BuildImageContextPrompt(normalized, prompt, size, quality)
			systemHints = []string{"picture_v2"}
			streamImages = images
		} else {
			historyText = AssistantHistoryText(normalized)
			historyMessages = AssistantHistoryMessages(normalized)
		}
		payloads, upstreamErr := client.StreamConversation(ctx, normalized, model, finalPrompt, streamImages, systemHints)
		iterErr := IterConversationPayloads(ctx, payloads, historyText, historyMessages, out)
		upErr := <-upstreamErr
		if iterErr != nil {
			errCh <- iterErr
			return
		}
		errCh <- upErr
	}()
	return out, errCh
}

func IterConversationPayloads(ctx context.Context, payloads <-chan string, historyText string, historyMessages []string, out chan<- ConversationEvent) error {
	state := &ConversationState{}
	historyIndex := 0
	for payload := range payloads {
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			event := conversationBaseEvent("conversation.done", state)
			event["done"] = true
			select {
			case out <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
			break
		}
		var raw any
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			UpdateConversationState(state, payload, nil)
			event := conversationBaseEvent("conversation.raw", state)
			event["payload"] = payload
			out <- event
			continue
		}
		eventMap, ok := raw.(map[string]any)
		if !ok {
			event := conversationBaseEvent("conversation.event", state)
			event["raw"] = raw
			out <- event
			continue
		}
		UpdateConversationState(state, payload, eventMap)
		if historyIndex < len(historyMessages) && EventAssistantText(eventMap, historyText) == historyMessages[historyIndex] {
			historyIndex++
			state.Text = ""
			continue
		}
		nextText := AssistantText(eventMap, state.Text, historyText)
		if nextText != state.Text {
			delta := nextText
			if strings.HasPrefix(nextText, state.Text) {
				delta = nextText[len(state.Text):]
			}
			state.Text = nextText
			event := conversationBaseEvent("conversation.delta", state)
			event["raw"] = eventMap
			event["delta"] = delta
			out <- event
			continue
		}
		event := conversationBaseEvent("conversation.event", state)
		event["raw"] = eventMap
		out <- event
	}
	return nil
}

func (e *Engine) StreamImageOutputsWithPool(ctx context.Context, request ConversationRequest) (<-chan ImageOutput, <-chan error) {
	request = request.Normalized()
	out := make(chan ImageOutput)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if !util.IsImageGenerationModel(request.Model) {
			errCh <- &ImageGenerationError{Message: "unsupported image model,supported models: " + util.ImageGenerationModelNames(), StatusCode: 502, Type: "server_error", Code: "upstream_error"}
			return
		}
		emitted := false
		lastError := ""
		var allowAccount func(map[string]any) bool
		if request.RequirePaidAccount {
			allowAccount = service.IsPaidImageAccount
		}
		for index := 1; index <= request.N; index++ {
			for {
				token, err := e.Accounts.GetAvailableAccessTokenFor(ctx, allowAccount)
				if err != nil {
					if emitted {
						errCh <- nil
						return
					}
					if request.RequirePaidAccount {
						errCh <- NewImageGenerationError("当前没有可用的 Paid 图片账号，1080P/2K/4K 等高分辨率出图需要 Plus / Pro / Team 账号")
						return
					}
					errCh <- NewImageGenerationError(err.Error())
					return
				}
				emittedForToken := false
				returnedMessage := false
				returnedResult := false
				rateLimitedForToken := false
				rateLimitMessage := ""
				client := backend.NewClient(token, e.Accounts, e.Proxy)
				outputs, imageErr := e.StreamImageOutputs(ctx, client, request, index, request.N)
				for output := range outputs {
					if output.Kind == "message" && service.IsAccountRateLimitedErrorMessage(output.Text) {
						rateLimitedForToken = true
						rateLimitMessage = output.Text
						lastError = output.Text
						continue
					}
					if output.Kind == "message" && request.MessageAsError {
						e.Accounts.MarkImageResult(token, false)
						errCh <- &ImageGenerationError{Message: firstNonEmpty(output.Text, "Image generation returned a text response instead of image data."), StatusCode: 400, Type: "invalid_request_error", Code: "image_generation_text_response"}
						return
					}
					emitted = true
					emittedForToken = true
					returnedMessage = output.Kind == "message"
					returnedResult = returnedResult || output.Kind == "result"
					out <- output
				}
				err = <-imageErr
				if err == nil {
					if rateLimitedForToken {
						e.Accounts.MarkImageResult(token, false)
						e.Accounts.ApplyAccountErrorMessage(token, "image_stream", rateLimitMessage)
						continue
					}
					if returnedMessage || !returnedResult {
						e.Accounts.MarkImageResult(token, false)
						errCh <- nil
						return
					}
					e.Accounts.MarkImageResult(token, true)
					break
				}
				e.Accounts.MarkImageResult(token, false)
				lastError = err.Error()
				if normalized, handled := e.Accounts.ApplyAccountErrorMessage(token, "image_stream", lastError); handled {
					lastError = normalized
					if service.IsAccountRateLimitedErrorMessage(err.Error()) || !emittedForToken {
						continue
					}
				}
				if !emittedForToken && IsTokenInvalidError(lastError) {
					continue
				}
				errCh <- NewImageGenerationError(imageStreamErrorMessage(lastError))
				return
			}
		}
		if !emitted {
			errCh <- NewImageGenerationError(imageStreamErrorMessage(lastError))
			return
		}
		errCh <- nil
	}()
	return out, errCh
}

func (e *Engine) StreamImageOutputs(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
	out := make(chan ImageOutput)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		var last ConversationEvent
		events, convErr := e.ConversationEvents(ctx, client, request.Messages, request.Model, request.Prompt, request.Images, request.Size, request.Quality)
		for event := range events {
			last = event
			if event["type"] == "conversation.delta" {
				out <- ImageOutput{Kind: "progress", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: util.Clean(event["delta"]), UpstreamEventType: "conversation.delta"}
				continue
			}
			if event["type"] == "conversation.event" {
				rawType := ""
				if raw := util.StringMap(event["raw"]); raw != nil {
					rawType = util.Clean(raw["type"])
				}
				out <- ImageOutput{Kind: "progress", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), UpstreamEventType: rawType}
			}
		}
		if err := <-convErr; err != nil {
			errCh <- err
			return
		}
		conversationID := util.Clean(last["conversation_id"])
		fileIDs := util.AsStringSlice(last["file_ids"])
		sedimentIDs := util.AsStringSlice(last["sediment_ids"])
		message := strings.TrimSpace(util.Clean(last["text"]))
		toolInvoked, _ := last["tool_invoked"].(bool)
		hasToolInvoked := last["tool_invoked"] != nil
		isTextResponse := (hasToolInvoked && !toolInvoked) || last["turn_use_case"] == "text"
		if message != "" && len(fileIDs) == 0 && len(sedimentIDs) == 0 && (util.ToBool(last["blocked"]) || isTextResponse) {
			out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: message}
			errCh <- nil
			return
		}
		imageURLs := client.ResolveConversationImageURLs(ctx, conversationID, fileIDs, sedimentIDs, true)
		if len(imageURLs) > 0 {
			bytesItems, err := client.DownloadImageBytes(ctx, imageURLs)
			if err != nil {
				errCh <- err
				return
			}
			var imageItems []map[string]any
			for _, data := range bytesItems {
				imageItems = append(imageItems, map[string]any{"b64_json": base64.StdEncoding.EncodeToString(data)})
			}
			result := e.FormatImageResult(imageItems, request.Prompt, request.ResponseFormat, request.BaseURL, request.OwnerID, request.OwnerName, time.Now().Unix(), "")
			data := util.AsMapSlice(result["data"])
			if len(data) > 0 {
				out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: data}
			}
			errCh <- nil
			return
		}
		if message != "" {
			out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: message}
		}
		errCh <- nil
	}()
	return out, errCh
}

func (e *Engine) CollectImageOutputs(outputs <-chan ImageOutput, errCh <-chan error) (map[string]any, error) {
	var created int64
	var data []map[string]any
	message := ""
	var progress []string
	for output := range outputs {
		if created == 0 {
			created = output.Created
		}
		switch output.Kind {
		case "progress":
			if output.Text != "" {
				progress = append(progress, output.Text)
			}
		case "message":
			message = output.Text
		case "result":
			data = append(data, output.Data...)
		}
	}
	streamErr := <-errCh
	if created == 0 {
		created = time.Now().Unix()
	}
	result := map[string]any{"created": created, "data": data}
	if len(data) == 0 {
		if text := firstNonEmpty(message, strings.TrimSpace(strings.Join(progress, ""))); text != "" {
			result["message"] = text
		}
	}
	if streamErr != nil {
		if imageErr, ok := streamErr.(*ImageGenerationError); ok && imageErr.Code == "image_generation_text_response" {
			result["output_type"] = "text"
		}
		if result["message"] == nil {
			result["message"] = streamErr.Error()
		}
		return result, streamErr
	}
	return result, nil
}

func (e *Engine) FormatImageResult(items []map[string]any, prompt, responseFormat, baseURL, ownerID, ownerName string, created int64, message string) map[string]any {
	var data []map[string]any
	for _, item := range items {
		b64 := util.Clean(item["b64_json"])
		if b64 == "" {
			continue
		}
		revised := firstNonEmpty(util.Clean(item["revised_prompt"]), prompt)
		imageBytes, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		urlValue := e.SaveImageBytesForOwner(imageBytes, baseURL, ownerID, ownerName)
		if responseFormat == "b64_json" {
			data = append(data, map[string]any{"b64_json": b64, "url": urlValue, "revised_prompt": revised})
		} else {
			data = append(data, map[string]any{"url": urlValue, "revised_prompt": revised})
		}
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	result := map[string]any{"created": created, "data": data}
	if message != "" && len(data) == 0 {
		result["message"] = message
	}
	return result
}

func (e *Engine) SaveImageBytes(imageData []byte, baseURL string) string {
	return e.SaveImageBytesForOwner(imageData, baseURL, "", "")
}

func (e *Engine) SaveImageBytesForOwner(imageData []byte, baseURL, ownerID, ownerName string) string {
	e.Config.CleanupOldImages()
	sum := md5.Sum(imageData)
	filename := fmt.Sprintf("%d_%s.png", time.Now().Unix(), hex.EncodeToString(sum[:]))
	relativeDir := filepath.Join(time.Now().Format("2006"), time.Now().Format("01"), time.Now().Format("02"))
	rel := filepath.Join(relativeDir, filename)
	filePath := filepath.Join(e.Config.ImagesDir(), rel)
	_ = os.MkdirAll(filepath.Dir(filePath), 0o755)
	_ = os.WriteFile(filePath, imageData, 0o644)
	e.writeImageOwnerMetadata(rel, ownerID, ownerName)
	if baseURL == "" {
		baseURL = e.Config.BaseURL()
	}
	return strings.TrimRight(baseURL, "/") + "/images/" + filepath.ToSlash(rel)
}

func (e *Engine) writeImageOwnerMetadata(rel, ownerID, ownerName string) {
	ownerID = strings.TrimSpace(ownerID)
	ownerName = strings.TrimSpace(ownerName)
	if e == nil || e.Config == nil || ownerID == "" {
		return
	}
	value := map[string]any{"owner_id": ownerID, "updated_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if ownerName != "" {
		value["owner_name"] = ownerName
	}
	if e.Storage != nil {
		_ = e.Storage.SaveJSONDocument(imageOwnerDocumentName(rel), value)
		return
	}
	metaPath := filepath.Join(e.Config.ImageMetadataDir(), filepath.FromSlash(filepath.ToSlash(rel))+".json")
	_ = os.MkdirAll(filepath.Dir(metaPath), 0o755)
	data, err := json.Marshal(value)
	if err == nil {
		_ = os.WriteFile(metaPath, data, 0o644)
	}
}

func imageOwnerDocumentName(rel string) string {
	return "image_metadata/" + filepath.ToSlash(rel) + ".json"
}

func IsTokenInvalidError(message string) bool {
	return service.IsAccountInvalidErrorMessage(message)
}

func MessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			switch x := item.(type) {
			case string:
				parts = append(parts, x)
			case map[string]any:
				t := util.Clean(x["type"])
				if t == "text" || t == "input_text" || t == "output_text" {
					parts = append(parts, util.Clean(x["text"]))
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func NormalizeMessages(messages any, system any) []map[string]any {
	var normalized []map[string]any
	if text := MessageText(system); text != "" {
		normalized = append(normalized, map[string]any{"role": "system", "content": text})
	}
	if list, ok := messages.([]map[string]any); ok {
		for _, message := range list {
			normalized = append(normalized, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": MessageText(message["content"])})
		}
		return normalized
	}
	if list, ok := messages.([]any); ok {
		for _, raw := range list {
			if message, ok := raw.(map[string]any); ok {
				normalized = append(normalized, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": MessageText(message["content"])})
			}
		}
	}
	return normalized
}

func AssistantHistoryText(messages []map[string]any) string {
	var parts []string
	for _, item := range messages {
		if item["role"] == "assistant" {
			parts = append(parts, util.Clean(item["content"]))
		}
	}
	return strings.Join(parts, "")
}

func AssistantHistoryMessages(messages []map[string]any) []string {
	var out []string
	for _, item := range messages {
		if item["role"] == "assistant" && util.Clean(item["content"]) != "" {
			out = append(out, util.Clean(item["content"]))
		}
	}
	return out
}

const maxFreeGeneratePixels = 1577536

func RequiresPaidImageSize(size string) bool {
	return false
}

func imageSizeDimensions(size string) (int, int, bool) {
	matches := regexp.MustCompile(`^(\d+)x(\d+)$`).FindStringSubmatch(strings.ToLower(strings.TrimSpace(size)))
	if len(matches) != 3 {
		return 0, 0, false
	}
	width := util.ToInt(matches[1], 0)
	height := util.ToInt(matches[2], 0)
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func BuildImagePrompt(prompt, size, quality string) string {
	prompt = strings.TrimSpace(prompt)
	var hintsList []string
	hints := map[string]string{
		"1:1":  "输出为 1:1 正方形构图，主体居中，适合正方形画幅。",
		"3:2":  "输出为 3:2 横版构图，适合摄影、产品展示和横向叙事画幅。",
		"16:9": "输出为 16:9 横屏构图，适合宽画幅展示。",
		"21:9": "输出为 21:9 超宽横版构图，适合电影感全景和宽银幕画幅。",
		"9:16": "输出为 9:16 竖屏构图，适合竖版画幅展示。",
		"4:3":  "输出为 4:3 比例，兼顾宽度与高度，适合展示画面细节。",
		"3:4":  "输出为 3:4 比例，纵向构图，适合人物肖像或竖向场景。",
	}
	if size != "" {
		if width, height, ok := imageSizeDimensions(size); ok {
			hintsList = append(hintsList, fmt.Sprintf("输出图片目标分辨率为 %d x %d 像素，并严格按该尺寸对应的宽高比构图。", width, height))
		} else if hint, ok := hints[size]; ok {
			hintsList = append(hintsList, hint)
		} else {
			hintsList = append(hintsList, "输出图片，目标尺寸或宽高比为 "+size+"。")
		}
	}
	qualityHints := map[string]string{
		"low":    "画质使用 Low 档，优先更快出图，细节可以适度简化。",
		"medium": "画质使用 Medium 档，在速度、细节和整体完成度之间保持平衡。",
		"high":   "画质使用 High 档，提升细节、纹理、光影和整体完成度。",
	}
	if hint, ok := qualityHints[strings.ToLower(strings.TrimSpace(quality))]; ok {
		hintsList = append(hintsList, hint)
	}
	if len(hintsList) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(hintsList, "\n")
}

func CountMessageTokens(messages []map[string]any, model string) int {
	total := 3
	for _, message := range messages {
		total += 3
		for key, value := range message {
			if text, ok := value.(string); ok {
				total += CountTextTokens(text, model)
				if key == "name" {
					total++
				}
			}
		}
	}
	return total
}

func CountTextTokens(text, model string) int {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0
	}
	return (len(runes) + 3) / 4
}

func EncodeImages(images []UploadedImage) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		if len(image.Data) > 0 {
			out = append(out, base64.StdEncoding.EncodeToString(image.Data))
		}
	}
	return out
}

type UploadedImage struct {
	Data        []byte
	Filename    string
	ContentType string
}

func AssistantText(event map[string]any, currentText, historyText string) string {
	for _, candidate := range []any{event, event["v"]} {
		m := util.StringMap(candidate)
		message := util.StringMap(m["message"])
		if len(message) == 0 {
			continue
		}
		author := util.StringMap(message["author"])
		if strings.ToLower(util.Clean(author["role"])) != "assistant" {
			continue
		}
		text := AssistantMessageText(message)
		if text != "" {
			return StripHistory(text, historyText)
		}
	}
	return ApplyTextPatch(event, currentText, historyText)
}

func EventAssistantText(event map[string]any, historyText string) string {
	for _, candidate := range []any{event, event["v"]} {
		m := util.StringMap(candidate)
		message := util.StringMap(m["message"])
		author := util.StringMap(message["author"])
		if author["role"] == "assistant" {
			return StripHistory(AssistantMessageText(message), historyText)
		}
	}
	return ""
}

func AssistantMessageText(message map[string]any) string {
	content := util.StringMap(message["content"])
	parts, _ := content["parts"].([]any)
	var out []string
	for _, part := range parts {
		if text, ok := part.(string); ok {
			out = append(out, text)
		}
	}
	return strings.Join(out, "")
}

func StripHistory(text, historyText string) string {
	for historyText != "" && strings.HasPrefix(text, historyText) {
		text = text[len(historyText):]
	}
	return text
}

func ApplyTextPatch(event map[string]any, currentText, historyText string) string {
	if event["p"] == "/message/content/parts/0" {
		return ApplyPatchOp(event, currentText, historyText)
	}
	if value, ok := event["v"].(string); ok && currentText != "" && event["p"] == nil && event["o"] == nil {
		return currentText + value
	}
	if event["o"] == "patch" {
		text := currentText
		for _, raw := range anyList(event["v"]) {
			if op, ok := raw.(map[string]any); ok {
				text = ApplyTextPatch(op, text, historyText)
			}
		}
		return text
	}
	text := currentText
	for _, raw := range anyList(event["v"]) {
		if op, ok := raw.(map[string]any); ok {
			text = ApplyTextPatch(op, text, historyText)
		}
	}
	return text
}

func ApplyPatchOp(operation map[string]any, currentText, historyText string) string {
	value := util.Clean(operation["v"])
	switch operation["o"] {
	case "append":
		return currentText + value
	case "replace":
		return StripHistory(value, historyText)
	default:
		return currentText
	}
}

func UpdateConversationState(state *ConversationState, payload string, event map[string]any) {
	conversationID, fileIDs, sedimentIDs := ExtractConversationIDs(payload)
	if conversationID != "" && state.ConversationID == "" {
		state.ConversationID = conversationID
	}
	if event != nil && IsImageToolEvent(event) {
		state.FileIDs = appendUnique(state.FileIDs, fileIDs...)
		state.SedimentIDs = appendUnique(state.SedimentIDs, sedimentIDs...)
	}
	if event == nil {
		return
	}
	if id := util.Clean(event["conversation_id"]); id != "" {
		state.ConversationID = id
	}
	value := util.StringMap(event["v"])
	if id := util.Clean(value["conversation_id"]); id != "" {
		state.ConversationID = id
	}
	if event["type"] == "moderation" {
		moderation := util.StringMap(event["moderation_response"])
		if moderation["blocked"] == true {
			state.Blocked = true
		}
	}
	if event["type"] == "server_ste_metadata" {
		metadata := util.StringMap(event["metadata"])
		if toolInvoked, ok := metadata["tool_invoked"].(bool); ok {
			state.ToolInvoked = &toolInvoked
		}
		if value := util.Clean(metadata["turn_use_case"]); value != "" {
			state.TurnUseCase = value
		}
	}
}

func ExtractConversationIDs(payload string) (string, []string, []string) {
	conversation := ""
	if match := regexp.MustCompile(`"conversation_id"\s*:\s*"([^"]+)"`).FindStringSubmatch(payload); len(match) > 1 {
		conversation = match[1]
	}
	fileIDs := regexp.MustCompile(`(file[-_][A-Za-z0-9]+)`).FindAllString(payload, -1)
	sedimentMatches := regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(payload, -1)
	var sediments []string
	for _, match := range sedimentMatches {
		if len(match) > 1 {
			sediments = append(sediments, match[1])
		}
	}
	return conversation, fileIDs, sediments
}

func IsImageToolEvent(event map[string]any) bool {
	value := util.StringMap(event["v"])
	message := util.StringMap(event["message"])
	if len(message) == 0 {
		message = util.StringMap(value["message"])
	}
	metadata := util.StringMap(message["metadata"])
	author := util.StringMap(message["author"])
	return author["role"] == "tool" && metadata["async_task_type"] == "image_gen"
}

func conversationBaseEvent(eventType string, state *ConversationState) ConversationEvent {
	var tool any
	if state.ToolInvoked != nil {
		tool = *state.ToolInvoked
	}
	return ConversationEvent{
		"type":            eventType,
		"text":            state.Text,
		"conversation_id": state.ConversationID,
		"file_ids":        state.FileIDs,
		"sediment_ids":    state.SedimentIDs,
		"blocked":         state.Blocked,
		"tool_invoked":    tool,
		"turn_use_case":   state.TurnUseCase,
	}
}

func anyList(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return nil
}

func appendUnique(base []string, values ...string) []string {
	seen := map[string]struct{}{}
	for _, item := range base {
		seen[item] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		base = append(base, value)
	}
	return base
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
