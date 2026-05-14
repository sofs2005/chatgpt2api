package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	tooladapter "chatgpt2api/internal/toolcall"
	"chatgpt2api/internal/util"
)

type StreamResult struct {
	Items <-chan map[string]any
	Err   <-chan error
	Kind  string
}

const ImageOutputSlotAcquirerPayloadKey = "image_output_slot_acquirer"

// ImageOutputChargePayloadKey names the per-image-output billing charge hook
// carried through the request body. The value must be an ImageOutputCharger
// (or a compatible func(index int) error). The hook runs before each image
// is persisted to disk and can veto the save by returning an error; returning
// a service.BillingLimitError propagates as the request-level error.
const ImageOutputChargePayloadKey = "image_output_charge"

const xmlToolRule = "Tool output adapter: when calling tools, output ONLY this XML and no prose/markdown:\n<tool_calls><tool_call><tool_name>TOOL_NAME</tool_name><parameters><PARAM><![CDATA[value]]></PARAM></parameters></tool_call></tool_calls>"

func (e *Engine) HandleImageGenerations(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	prompt := util.Clean(body["prompt"])
	if prompt == "" {
		return nil, nil, HTTPError{Status: 400, Message: "prompt is required"}
	}
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	n, err := ParseImageCount(body["n"])
	if err != nil {
		return nil, nil, err
	}
	size := util.Clean(body["size"])
	quality := util.Clean(body["quality"])
	outputFormat := NormalizeImageOutputFormat(util.Clean(body["output_format"]))
	outputCompression, hasOutputCompression := normalizedImageOutputCompression(body["output_compression"])
	responseFormat := firstNonEmpty(util.Clean(body["response_format"]), "b64_json")
	baseURL := util.Clean(body["base_url"])
	request := ConversationRequest{Prompt: prompt, Model: model, Messages: NormalizeMessages(util.AsMapSlice(body["messages"]), nil), N: n, Size: size, Quality: quality, Background: util.Clean(body["background"]), Moderation: util.Clean(body["moderation"]), Style: util.Clean(body["style"]), OutputFormat: outputFormat, ResponseFormat: responseFormat, BaseURL: baseURL, OwnerID: util.Clean(body["owner_id"]), OwnerName: util.Clean(body["owner_name"]), MessageAsError: true, AcquireImageOutputSlot: imageOutputSlotAcquirer(body), ChargeImageOutput: imageOutputCharger(body)}
	if partialImages, ok := normalizedPositiveInt(body["partial_images"]); ok {
		request.PartialImages = &partialImages
	}
	if hasOutputCompression && SupportsImageOutputCompression(outputFormat) {
		request.OutputCompression = &outputCompression
	}
	applyImageToolOptionsToRequest(&request, ImageToolOptionsFromPayload(body))
	request = request.Normalized()
	outputs, errCh := e.StreamImageOutputsWithPool(ctx, request)
	if util.ToBool(body["stream"]) {
		return nil, &StreamResult{Items: StreamImageChunks(outputs), Err: errCh, Kind: "openai"}, nil
	}
	result, err := e.CollectImageOutputsWithProgress(outputs, errCh, imageOutputProgressCallback(body))
	return result, nil, err
}

func (e *Engine) HandleImageEdits(ctx context.Context, body map[string]any, images []UploadedImage) (map[string]any, *StreamResult, error) {
	encoded := EncodeImages(images)
	if len(encoded) == 0 {
		return nil, nil, &ImageGenerationError{Message: "image is required", StatusCode: 502, Type: "server_error", Code: "upstream_error"}
	}
	size := util.Clean(body["size"])
	request := ConversationRequest{
		Prompt:                 util.Clean(body["prompt"]),
		Model:                  firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto),
		N:                      util.ToInt(body["n"], 1),
		Size:                   size,
		Quality:                util.Clean(body["quality"]),
		Background:             util.Clean(body["background"]),
		Moderation:             util.Clean(body["moderation"]),
		Style:                  util.Clean(body["style"]),
		OutputFormat:           NormalizeImageOutputFormat(util.Clean(body["output_format"])),
		ResponseFormat:         firstNonEmpty(util.Clean(body["response_format"]), "b64_json"),
		BaseURL:                util.Clean(body["base_url"]),
		OwnerID:                util.Clean(body["owner_id"]),
		OwnerName:              util.Clean(body["owner_name"]),
		Messages:               NormalizeMessages(util.AsMapSlice(body["messages"]), nil),
		Images:                 encoded,
		InputImageMask:         responseImageMask(body["input_image_mask"]),
		MessageAsError:         true,
		AcquireImageOutputSlot: imageOutputSlotAcquirer(body),
		ChargeImageOutput:      imageOutputCharger(body),
	}
	if partialImages, ok := normalizedPositiveInt(body["partial_images"]); ok {
		request.PartialImages = &partialImages
	}
	applyImageToolOptionsToRequest(&request, ImageToolOptionsFromPayload(body))
	if SupportsImageOutputCompression(request.OutputFormat) {
		if compression, ok := normalizedImageOutputCompression(body["output_compression"]); ok {
			request.OutputCompression = &compression
		}
	}
	request = request.Normalized()
	outputs, errCh := e.StreamImageOutputsWithPool(ctx, request)
	if util.ToBool(body["stream"]) {
		return nil, &StreamResult{Items: StreamImageChunks(outputs), Err: errCh, Kind: "openai"}, nil
	}
	result, err := e.CollectImageOutputsWithProgress(outputs, errCh, imageOutputProgressCallback(body))
	return result, nil, err
}

func imageOutputProgressCallback(body map[string]any) ImageOutputProgressCallback {
	switch callback := body["image_output_callback"].(type) {
	case ImageOutputProgressCallback:
		return callback
	case func([]map[string]any):
		return callback
	default:
		return nil
	}
}

func imageOutputSlotAcquirer(body map[string]any) ImageOutputSlotAcquirer {
	switch acquire := body[ImageOutputSlotAcquirerPayloadKey].(type) {
	case ImageOutputSlotAcquirer:
		return acquire
	case func(context.Context, int) (func(), error):
		return acquire
	default:
		return nil
	}
}

func imageOutputCharger(body map[string]any) ImageOutputCharger {
	switch charge := body[ImageOutputChargePayloadKey].(type) {
	case ImageOutputCharger:
		return charge
	case func(int) error:
		return charge
	default:
		return nil
	}
}

func StreamImageChunks(outputs <-chan ImageOutput) <-chan map[string]any {
	out := make(chan map[string]any)
	go func() {
		defer close(out)
		for output := range outputs {
			out <- output.Chunk()
		}
	}()
	return out
}

func (e *Engine) textBackendWithRetry(exhaustedTokens map[string]struct{}) (*backend.Client, string, bool) {
	token, ok := e.Accounts.GetTextAccessTokenWithRetry(exhaustedTokens)
	if !ok {
		return nil, "", false
	}
	return e.TextBackend(token), token, true
}

func (e *Engine) markTextTokenExpiredForRetry(accessToken string, err error, exhaustedTokens map[string]struct{}) bool {
	if err == nil || !service.IsAccountTokenExpiredErrorMessage(err.Error()) {
		return false
	}
	exhaustedTokens[accessToken] = struct{}{}
	if _, shouldRetry := e.Accounts.HandleTokenExpiredOnRequest(accessToken); shouldRetry {
		return true
	}
	e.Accounts.ApplyAccountError(accessToken, "text_stream", err)
	return false
}

func (e *Engine) streamTextDeltasWithTokenRetry(ctx context.Context, firstClient *backend.Client, request ConversationRequest) (<-chan string, <-chan error) {
	out := make(chan string)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		exhaustedTokens := map[string]struct{}{}
		client := firstClient
		var lastErr error
		for attempt := 0; attempt < service.MaxTokenSwitchAttempts; attempt++ {
			if client == nil {
				var ok bool
				client, _, ok = e.textBackendWithRetry(exhaustedTokens)
				if !ok {
					break
				}
			}
			deltas, upstreamErr := e.StreamTextDeltas(ctx, client, request)
			sent := false
			for delta := range deltas {
				sent = true
				select {
				case out <- delta:
				case <-ctx.Done():
					errOut <- ctx.Err()
					return
				}
			}
			err := <-upstreamErr
			if err == nil {
				errOut <- nil
				return
			}
			lastErr = err
			if sent || !e.markTextTokenExpiredForRetry(client.AccessToken, err, exhaustedTokens) {
				errOut <- err
				return
			}
			client = nil
		}
		if lastErr != nil {
			errOut <- lastErr
			return
		}
		errOut <- fmt.Errorf("no available text access token")
	}()
	return out, errOut
}

func (e *Engine) collectTextWithTokenRetry(ctx context.Context, request ConversationRequest) (string, error) {
	deltas, errCh := e.streamTextDeltasWithTokenRetry(ctx, nil, request)
	var parts []string
	for delta := range deltas {
		parts = append(parts, delta)
	}
	return strings.Join(parts, ""), <-errCh
}

func (e *Engine) collectVisionTextWithTokenRetry(ctx context.Context, messages []map[string]any, model string, images []backend.VisionImage) (string, error) {
	exhaustedTokens := map[string]struct{}{}
	var lastErr error
	for attempt := 0; attempt < service.MaxTokenSwitchAttempts; attempt++ {
		client, token, ok := e.textBackendWithRetry(exhaustedTokens)
		if !ok {
			break
		}
		text, err := e.CollectVisionText(ctx, client, messages, model, images)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !e.markTextTokenExpiredForRetry(token, err, exhaustedTokens) {
			return "", err
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no available text access token")
}

func (e *Engine) streamVisionDeltasWithTokenRetry(ctx context.Context, firstClient *backend.Client, messages []map[string]any, model string, images []backend.VisionImage) (<-chan string, <-chan error) {
	out := make(chan string)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		exhaustedTokens := map[string]struct{}{}
		client := firstClient
		var lastErr error
		for attempt := 0; attempt < service.MaxTokenSwitchAttempts; attempt++ {
			if client == nil {
				var ok bool
				client, _, ok = e.textBackendWithRetry(exhaustedTokens)
				if !ok {
					break
				}
			}
			deltas, upstreamErr := client.StreamMultimodalConversation(ctx, messages, model, images)
			sent := false
			for delta := range deltas {
				sent = true
				select {
				case out <- delta:
				case <-ctx.Done():
					errOut <- ctx.Err()
					return
				}
			}
			err := <-upstreamErr
			if err == nil {
				errOut <- nil
				return
			}
			lastErr = err
			if sent || !e.markTextTokenExpiredForRetry(client.AccessToken, err, exhaustedTokens) {
				errOut <- err
				return
			}
			client = nil
		}
		if lastErr != nil {
			errOut <- lastErr
			return
		}
		errOut <- fmt.Errorf("no available text access token")
	}()
	return out, errOut
}

func (e *Engine) HandleChatCompletions(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	if util.ToBool(body["stream"]) {
		var items <-chan map[string]any
		var errCh <-chan error
		if IsImageChatRequest(body) {
			items, errCh = e.ImageChatEvents(ctx, body)
		} else if HasVisionImages(body) {
			model, messages, images, err := VisionChatParts(body)
			if err != nil {
				return nil, nil, err
			}
			items, errCh = e.StreamVisionChatCompletionWithTools(ctx, e.TextBackend(e.Accounts.GetTextAccessToken()), messages, model, images, body["tools"], body["tool_choice"])
		} else {
			model, messages, err := TextChatParts(body)
			if err != nil {
				return nil, nil, err
			}
			items, errCh = e.StreamTextChatCompletionWithTools(ctx, e.TextBackend(e.Accounts.GetTextAccessToken()), messages, model, body["tools"], body["tool_choice"])
		}
		return nil, &StreamResult{Items: items, Err: errCh, Kind: "openai"}, nil
	}
	if IsImageChatRequest(body) {
		return e.ImageChatResponse(ctx, body)
	}
	if HasVisionImages(body) {
		model, messages, images, err := VisionChatParts(body)
		if err != nil {
			return nil, nil, err
		}
		result, err := e.VisionChatResponse(ctx, body, model, messages, images)
		if err != nil {
			return nil, nil, err
		}
		return result, nil, nil
	}
	model, messages, err := TextChatParts(body)
	if err != nil {
		return nil, nil, err
	}
	text, err := e.collectTextWithTokenRetry(ctx, ConversationRequest{Model: model, Messages: messages})
	if err != nil {
		return nil, nil, err
	}
	result, err := CompletionResponseWithTools(model, text, 0, messages, body["tools"], body["tool_choice"])
	if err != nil {
		return nil, nil, err
	}
	return result, nil, nil
}

func CompletionChunk(model string, delta map[string]any, finishReason any, completionID string, created int64) map[string]any {
	if completionID == "" {
		completionID = "chatcmpl-" + util.NewHex(32)
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	return map[string]any{"id": completionID, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finishReason}}}
}

func CompletionResponse(model, content string, created int64, messages []map[string]any) map[string]any {
	if created == 0 {
		created = time.Now().Unix()
	}
	promptTokens, completionTokens := 0, 0
	if len(messages) > 0 {
		promptTokens = CountMessageTokens(messages, model)
		completionTokens = CountTextTokens(content, model)
	}
	return map[string]any{
		"id": "chatcmpl-" + util.NewHex(32), "object": "chat.completion", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": promptTokens, "completion_tokens": completionTokens, "total_tokens": promptTokens + completionTokens},
	}
}

func CompletionResponseWithTools(model, content string, created int64, messages []map[string]any, tools any, choice any) (map[string]any, error) {
	policy := tooladapter.PolicyFromToolChoice(choice)
	toolNames := tooladapter.ToolNames(tools)
	if policy.Mode == tooladapter.ChoiceNone {
		return CompletionResponse(model, content, created, messages), nil
	}
	if err := validateToolChoice(policy, toolNames); err != nil {
		return nil, HTTPError{Status: 400, Message: err.Error()}
	}
	if len(toolNames) == 0 {
		return CompletionResponse(model, content, created, messages), nil
	}
	calls, visible, err := tooladapter.Parse(content, toolNames, policy)
	if err != nil {
		return nil, HTTPError{Status: 400, Message: err.Error()}
	}
	calls = tooladapter.NormalizeForSchemas(calls, tools)
	if len(calls) == 0 {
		return CompletionResponse(model, visible, created, messages), nil
	}
	response := CompletionResponse(model, "", created, messages)
	choiceMap := response["choices"].([]map[string]any)[0]
	message := choiceMap["message"].(map[string]any)
	message["content"] = nil
	message["tool_calls"] = tooladapter.FormatOpenAI(calls)
	choiceMap["finish_reason"] = "tool_calls"
	return response, nil
}

func streamChatCompletionEvents(ctx context.Context, model string, deltas <-chan string, upstreamErr <-chan error, tools any, choice any) (<-chan map[string]any, <-chan error) {
	policy := tooladapter.PolicyFromToolChoice(choice)
	toolNames := tooladapter.ToolNames(tools)
	if err := preflightToolChoiceWithoutToolsError(policy, toolNames); err != nil {
		return streamHTTPError(err)
	}
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		id := "chatcmpl-" + util.NewHex(32)
		created := time.Now().Unix()
		toolMode := len(toolNames) > 0 && policy.Mode != tooladapter.ChoiceNone
		current := ""
		streamedLen := 0
		sentRole := false

		send := func(chunk map[string]any) bool {
			select {
			case out <- chunk:
				return true
			case <-ctx.Done():
				errOut <- ctx.Err()
				return false
			}
		}
		sendText := func(text string) bool {
			if text == "" {
				return true
			}
			if !sentRole {
				sentRole = true
				return send(CompletionChunk(model, map[string]any{"role": "assistant", "content": text}, nil, id, created))
			}
			return send(CompletionChunk(model, map[string]any{"content": text}, nil, id, created))
		}
		sendRoleIfNeeded := func() bool {
			if sentRole {
				return true
			}
			sentRole = true
			return send(CompletionChunk(model, map[string]any{"role": "assistant", "content": ""}, nil, id, created))
		}

		for deltaText := range deltas {
			current += deltaText
			visible := current
			if toolMode {
				visible = safeRawToolVisiblePrefix(current)
			}
			if len(visible) >= streamedLen {
				next := visible[streamedLen:]
				if next != "" {
					if !sendText(next) {
						return
					}
					streamedLen = len(visible)
				}
			}
		}
		if upstreamErr != nil {
			if err := <-upstreamErr; err != nil {
				errOut <- err
				return
			}
		}
		if toolMode {
			calls, _, err := tooladapter.Parse(current, toolNames, policy)
			if err != nil {
				errOut <- HTTPError{Status: 400, Message: err.Error()}
				return
			}
			calls = tooladapter.NormalizeForSchemas(calls, tools)
			if len(calls) > 0 {
				if !sendRoleIfNeeded() {
					return
				}
				if !send(CompletionChunk(model, map[string]any{"tool_calls": tooladapter.FormatOpenAIStream(calls)}, "tool_calls", id, created)) {
					return
				}
				errOut <- nil
				return
			}
			if streamedLen <= len(current) {
				if !sendText(current[streamedLen:]) {
					return
				}
			}
		}
		if !sendRoleIfNeeded() {
			return
		}
		if !send(CompletionChunk(model, map[string]any{}, "stop", id, created)) {
			return
		}
		errOut <- nil
	}()
	return out, errOut
}

func streamHTTPError(err error) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		errOut <- HTTPError{Status: 400, Message: err.Error()}
	}()
	return out, errOut
}

func safeRawToolVisiblePrefix(text string) string {
	masked := maskFencedToolBlocks(text)
	markerStart := -1
	for _, marker := range toolMarkupMarkers {
		if pos := strings.Index(masked, marker); pos >= 0 && (markerStart < 0 || pos < markerStart) {
			markerStart = pos
		}
	}
	if markerStart >= 0 {
		return text[:markerStart]
	}
	for keep := len(text) - 1; keep >= 0; keep-- {
		suffix := text[keep:]
		for _, marker := range toolMarkupMarkers {
			if strings.HasPrefix(marker, suffix) {
				return text[:keep]
			}
		}
	}
	return text
}

var toolMarkupMarkers = []string{"<tool_calls", "<tool_call", "<function_call", "<invoke"}

func maskFencedToolBlocks(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	inFence := false
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "```") {
			inFence = !inFence
			b.WriteString("   ")
			i += 3
			continue
		}
		if inFence {
			b.WriteByte(' ')
		} else {
			b.WriteByte(text[i])
		}
		i++
	}
	return b.String()
}

func (e *Engine) StreamTextChatCompletion(ctx context.Context, client *backend.Client, messages []map[string]any, model string) (<-chan map[string]any, <-chan error) {
	deltas, errCh := e.streamTextDeltasWithTokenRetry(ctx, client, ConversationRequest{Model: model, Messages: messages})
	return streamChatCompletionEvents(ctx, model, deltas, errCh, nil, nil)
}

func (e *Engine) StreamTextChatCompletionWithTools(ctx context.Context, client *backend.Client, messages []map[string]any, model string, tools any, choice any) (<-chan map[string]any, <-chan error) {
	if err := preflightToolChoiceWithoutToolsError(tooladapter.PolicyFromToolChoice(choice), tooladapter.ToolNames(tools)); err != nil {
		return streamHTTPError(err)
	}
	deltas, errCh := e.streamTextDeltasWithTokenRetry(ctx, client, ConversationRequest{Model: model, Messages: messages})
	return streamChatCompletionEvents(ctx, model, deltas, errCh, tools, choice)
}

func (e *Engine) StreamVisionChatCompletion(ctx context.Context, client *backend.Client, messages []map[string]any, model string, images []UploadedImage) (<-chan map[string]any, <-chan error) {
	return e.StreamVisionChatCompletionWithTools(ctx, client, messages, model, images, nil, nil)
}

func (e *Engine) StreamVisionChatCompletionWithTools(ctx context.Context, client *backend.Client, messages []map[string]any, model string, images []UploadedImage, tools any, choice any) (<-chan map[string]any, <-chan error) {
	if err := preflightToolChoiceWithoutToolsError(tooladapter.PolicyFromToolChoice(choice), tooladapter.ToolNames(tools)); err != nil {
		return streamHTTPError(err)
	}
	visionImages := make([]backend.VisionImage, len(images))
	for i, img := range images {
		visionImages[i] = backend.VisionImage{
			Data:        img.Data,
			ContentType: img.ContentType,
			FileName:    img.Filename,
		}
	}
	deltas, errCh := e.streamVisionDeltasWithTokenRetry(ctx, client, messages, model, visionImages)
	return streamChatCompletionEvents(ctx, model, deltas, errCh, tools, choice)
}

func (e *Engine) VisionChatResponse(ctx context.Context, body map[string]any, model string, messages []map[string]any, images []UploadedImage) (map[string]any, error) {
	visionImages := make([]backend.VisionImage, len(images))
	for i, img := range images {
		visionImages[i] = backend.VisionImage{
			Data:        img.Data,
			ContentType: img.ContentType,
			FileName:    img.Filename,
		}
	}
	text, err := e.collectVisionTextWithTokenRetry(ctx, messages, model, visionImages)
	if err != nil {
		return nil, err
	}
	return CompletionResponseWithTools(model, text, 0, messages, body["tools"], body["tool_choice"])
}

func ChatMessagesFromBody(body map[string]any) ([]map[string]any, error) {
	if messages := util.AsMapSlice(body["messages"]); len(messages) > 0 {
		return messages, nil
	}
	if prompt := strings.TrimSpace(util.Clean(body["prompt"])); prompt != "" {
		return []map[string]any{{"role": "user", "content": prompt}}, nil
	}
	return nil, HTTPError{Status: 400, Message: "messages or prompt is required"}
}

func ChatToolPrompt(body map[string]any) string {
	return tooladapter.BuildPrompt(body["tools"], tooladapter.PolicyFromToolChoice(body["tool_choice"]))
}

func TextChatParts(body map[string]any) (string, []map[string]any, error) {
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	messages, err := ChatMessagesFromBody(body)
	if err != nil {
		return "", nil, err
	}
	return model, NormalizeMessages(messages, ChatToolPrompt(body)), nil
}

func IsImageChatRequest(body map[string]any) bool {
	if util.IsImageModel(util.Clean(body["model"])) {
		return true
	}
	for _, item := range anyList(body["modalities"]) {
		if strings.ToLower(util.Clean(item)) == "image" {
			return true
		}
	}
	return false
}

func HasVisionImages(body map[string]any) bool {
	if IsImageChatRequest(body) {
		return false
	}
	for _, msg := range util.AsMapSlice(body["messages"]) {
		if len(ExtractImagesFromMessageContent(msg["content"])) > 0 {
			return true
		}
	}
	return false
}

func ExtractVisionImages(body map[string]any) []UploadedImage {
	var images []UploadedImage
	for _, msg := range util.AsMapSlice(body["messages"]) {
		images = append(images, ExtractImagesFromMessageContent(msg["content"])...)
	}
	return images
}

func VisionChatParts(body map[string]any) (string, []map[string]any, []UploadedImage, error) {
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	rawMessages, err := ChatMessagesFromBody(body)
	if err != nil {
		return "", nil, nil, err
	}
	messages := NormalizeMessages(rawMessages, ChatToolPrompt(body))
	images := ExtractVisionImages(body)
	return model, messages, images, nil
}

func (e *Engine) ImageChatResponse(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	model, prompt, n, images, messages, err := ChatImageArgs(body)
	if err != nil {
		return nil, nil, err
	}
	size := util.Clean(body["size"])
	request := ConversationRequest{Prompt: prompt, Model: model, Messages: messages, N: n, Size: size, Quality: util.Clean(body["quality"]), Background: util.Clean(body["background"]), Moderation: util.Clean(body["moderation"]), Style: util.Clean(body["style"]), ResponseFormat: "b64_json", OwnerID: util.Clean(body["owner_id"]), OwnerName: util.Clean(body["owner_name"]), Images: EncodeImages(images), InputImageMask: responseImageMask(body["input_image_mask"]), AcquireImageOutputSlot: imageOutputSlotAcquirer(body), ChargeImageOutput: imageOutputCharger(body)}
	if partialImages, ok := normalizedPositiveInt(body["partial_images"]); ok {
		request.PartialImages = &partialImages
	}
	applyImageOutputOptionsToRequest(&request, ImageOutputOptionsFromPayload(body))
	applyImageToolOptionsToRequest(&request, ImageToolOptionsFromPayload(body))
	outputs, errCh := e.StreamImageOutputsWithPool(ctx, request.Normalized())
	result, err := e.CollectImageOutputs(outputs, errCh)
	if err != nil {
		return nil, nil, err
	}
	return CompletionResponse(model, ImageResultContent(result), int64(util.ToInt(result["created"], 0)), nil), nil, nil
}

func (e *Engine) ImageChatEvents(ctx context.Context, body map[string]any) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		model, prompt, n, images, messages, err := ChatImageArgs(body)
		if err != nil {
			errOut <- err
			return
		}
		size := util.Clean(body["size"])
		request := ConversationRequest{Prompt: prompt, Model: model, Messages: messages, N: n, Size: size, Quality: util.Clean(body["quality"]), Background: util.Clean(body["background"]), Moderation: util.Clean(body["moderation"]), Style: util.Clean(body["style"]), ResponseFormat: "b64_json", OwnerID: util.Clean(body["owner_id"]), OwnerName: util.Clean(body["owner_name"]), Images: EncodeImages(images), InputImageMask: responseImageMask(body["input_image_mask"]), AcquireImageOutputSlot: imageOutputSlotAcquirer(body), ChargeImageOutput: imageOutputCharger(body)}
		if partialImages, ok := normalizedPositiveInt(body["partial_images"]); ok {
			request.PartialImages = &partialImages
		}
		applyImageOutputOptionsToRequest(&request, ImageOutputOptionsFromPayload(body))
		applyImageToolOptionsToRequest(&request, ImageToolOptionsFromPayload(body))
		outputs, errCh := e.StreamImageOutputsWithPool(ctx, request.Normalized())
		id := "chatcmpl-" + util.NewHex(32)
		created := time.Now().Unix()
		sentRole := false
		sentText := ""
		for output := range outputs {
			content := ""
			switch output.Kind {
			case "progress":
				content = output.Text
				sentText += content
			case "result":
				content = BuildChatImageMarkdownContent(map[string]any{"data": output.Data})
			case "message":
				content = output.Text
				content = strings.TrimPrefix(content, sentText)
			}
			if content == "" {
				continue
			}
			if !sentRole {
				sentRole = true
				out <- CompletionChunk(model, map[string]any{"role": "assistant", "content": content}, nil, id, created)
			} else {
				out <- CompletionChunk(model, map[string]any{"content": content}, nil, id, created)
			}
		}
		if err := <-errCh; err != nil {
			errOut <- err
			return
		}
		if !sentRole {
			out <- CompletionChunk(model, map[string]any{"role": "assistant", "content": ""}, nil, id, created)
		}
		out <- CompletionChunk(model, map[string]any{}, "stop", id, created)
		errOut <- nil
	}()
	return out, errOut
}

func applyImageOutputOptionsToRequest(request *ConversationRequest, options ImageOutputOptions) {
	if request == nil {
		return
	}
	request.OutputFormat = options.Format
	request.OutputCompression = options.Compression
}

func applyImageToolOptionsToRequest(request *ConversationRequest, options ImageToolOptions) {
	if request == nil {
		return
	}
	request.Background = options.Background
	request.Moderation = options.Moderation
	request.Style = options.Style
	request.PartialImages = options.PartialImages
	request.InputImageMask = options.InputImageMask
}

func ChatImageArgs(body map[string]any) (string, string, int, []UploadedImage, []map[string]any, error) {
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	rawMessages, err := ChatMessagesFromBody(body)
	if err != nil {
		return "", "", 0, nil, nil, err
	}
	messages := NormalizeMessages(rawMessages, nil)
	prompt := LatestUserPrompt(messages)
	if prompt == "" {
		prompt = ExtractChatPrompt(body)
	}
	if prompt == "" {
		return "", "", 0, nil, nil, HTTPError{Status: 400, Message: "prompt is required"}
	}
	n, err := ParseImageCount(body["n"])
	if err != nil {
		return "", "", 0, nil, nil, err
	}
	images := ExtractChatContextImages(body)
	return model, prompt, n, images, messages, nil
}

func ImageResultContent(result map[string]any) string {
	if data := util.AsMapSlice(result["data"]); len(data) > 0 {
		return BuildChatImageMarkdownContent(result)
	}
	return firstNonEmpty(util.Clean(result["message"]), "Image generation completed.")
}

func ParseImageCount(raw any) (int, error) {
	value := util.ToInt(raw, 1)
	if value < 1 || value > 4 {
		return 0, HTTPError{Status: 400, Message: "n must be between 1 and 4"}
	}
	return value, nil
}

func BuildChatImageMarkdownContent(imageResult map[string]any) string {
	var parts []string
	for index, item := range util.AsMapSlice(imageResult["data"]) {
		b64 := util.Clean(item["b64_json"])
		if b64 != "" {
			parts = append(parts, fmt.Sprintf("![image_%d](data:image/png;base64,%s)", index+1, b64))
		}
	}
	if len(parts) == 0 {
		return "Image generation completed."
	}
	return strings.Join(parts, "\n\n")
}

func ExtractChatPrompt(body map[string]any) string {
	if prompt := strings.TrimSpace(util.Clean(body["prompt"])); prompt != "" {
		return prompt
	}
	messages := NormalizeMessages(util.AsMapSlice(body["messages"]), nil)
	if prompt := LatestUserPrompt(messages); prompt != "" {
		return prompt
	}
	for _, message := range util.AsMapSlice(body["messages"]) {
		if strings.ToLower(util.Clean(message["role"])) != "user" {
			continue
		}
		if prompt := ExtractPromptFromMessageContent(message["content"]); prompt != "" {
			return prompt
		}
	}
	return ""
}

func ExtractChatImages(body map[string]any) []UploadedImage {
	messages := util.AsMapSlice(body["messages"])
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(util.Clean(messages[i]["role"])) != "user" {
			continue
		}
		images := ExtractImagesFromMessageContent(messages[i]["content"])
		if len(images) > 0 {
			return images
		}
	}
	return nil
}

func ExtractChatContextImages(body map[string]any) []UploadedImage {
	var images []UploadedImage
	for _, message := range util.AsMapSlice(body["messages"]) {
		images = append(images, ExtractImagesFromMessageContent(message["content"])...)
	}
	if len(images) > maxContextImages {
		images = images[len(images)-maxContextImages:]
	}
	return images
}

func ExtractPromptFromMessageContent(content any) string {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text)
	}
	var parts []string
	for _, raw := range anyList(content) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch util.Clean(item["type"]) {
		case "text":
			if text := strings.TrimSpace(util.Clean(item["text"])); text != "" {
				parts = append(parts, text)
			}
		case "input_text":
			if text := strings.TrimSpace(firstNonEmpty(util.Clean(item["text"]), util.Clean(item["input_text"]))); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func ExtractImagesFromMessageContent(content any) []UploadedImage {
	if text, ok := content.(string); ok {
		return ExtractImagesFromText(text)
	}
	var images []UploadedImage
	for _, raw := range anyList(content) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		itemType := util.Clean(item["type"])
		imageURL := ""
		if itemType == "image_url" {
			if obj, ok := item["image_url"].(map[string]any); ok {
				imageURL = util.Clean(obj["url"])
			} else {
				imageURL = util.Clean(item["image_url"])
			}
		}
		if itemType == "input_image" {
			imageURL = util.Clean(item["image_url"])
		}
		if strings.HasPrefix(imageURL, "data:") {
			header, data, _ := strings.Cut(imageURL, ",")
			mime := strings.TrimPrefix(strings.Split(header, ";")[0], "data:")
			bytes, err := base64.StdEncoding.DecodeString(data)
			if err == nil {
				images = append(images, UploadedImage{Data: bytes, Filename: "image.png", ContentType: firstNonEmpty(mime, "image/png")})
			}
		}
	}
	return images
}

func ExtractImagesFromText(text string) []UploadedImage {
	var images []UploadedImage
	re := regexp.MustCompile(`data:(image/[A-Za-z0-9.+-]+);base64,([A-Za-z0-9+/=]+)`)
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		bytes, err := base64.StdEncoding.DecodeString(match[2])
		if err == nil {
			images = append(images, UploadedImage{Data: bytes, Filename: "image.png", ContentType: firstNonEmpty(match[1], "image/png")})
		}
	}
	return images
}

func (e *Engine) HandleResponses(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	return e.HandleResponsesScoped(ctx, body, "")
}

func (e *Engine) HandleResponsesScoped(ctx context.Context, body map[string]any, scope string) (map[string]any, *StreamResult, error) {
	events, errCh, err := e.ResponseEventsScoped(ctx, body, scope)
	if err != nil {
		return nil, nil, err
	}
	if util.ToBool(body["stream"]) {
		return nil, &StreamResult{Items: events, Err: errCh, Kind: "openai"}, nil
	}
	completed := map[string]any{}
	for event := range events {
		if event["type"] == "response.completed" {
			if response, ok := event["response"].(map[string]any); ok {
				completed = response
			}
		}
	}
	if err := <-errCh; err != nil {
		return nil, nil, err
	}
	if len(completed) == 0 {
		return nil, nil, fmt.Errorf("response generation failed")
	}
	return completed, nil, nil
}

func (e *Engine) ResponseEvents(ctx context.Context, body map[string]any) (<-chan map[string]any, <-chan error, error) {
	return e.ResponseEventsScoped(ctx, body, "")
}

func (e *Engine) ResponseEventsScoped(ctx context.Context, body map[string]any, scope string) (<-chan map[string]any, <-chan error, error) {
	previous, err := e.responseContextFromPreviousScoped(scope, body["previous_response_id"])
	if err != nil {
		return nil, nil, err
	}
	responseModel := firstNonEmpty(util.Clean(body["model"]), "auto")
	currentMessages := MessagesFromInput(body["input"], body["instructions"])
	baseContext := MergeResponseContext(previous, currentMessages, nil)
	if !HasResponseImageGenerationTool(body) {
		events, errCh := e.StreamTextResponseWithMessages(ctx, responseModel, baseContext.Messages)
		events = e.rememberResponseContextEventsScoped(scope, events, baseContext)
		return events, errCh, nil
	}
	request, prompt, err := ResponseImageGenerationRequest(body, scope, &previous)
	if err != nil {
		return nil, nil, err
	}
	request.AcquireImageOutputSlot = imageOutputSlotAcquirer(body)
	request.ChargeImageOutput = imageOutputCharger(body)
	var currentImages []string
	if inputImages := ExtractResponseImages(body["input"]); len(inputImages) > 0 {
		currentImages = EncodeImages(inputImages)
	}
	baseContext = MergeResponseContext(previous, currentMessages, currentImages)
	request.Messages = baseContext.Messages
	outputs, errCh := e.StreamImageOutputsWithPool(ctx, request)
	events, responseErr := StreamImageResponse(outputs, prompt, responseModel)
	events = e.rememberResponseContextEventsScoped(scope, events, baseContext)
	return events, combineErrorChannels(errCh, responseErr), nil
}

func ResponseImageGenerationRequest(body map[string]any, scope string, previous *ResponseContext) (ConversationRequest, string, error) {
	responseModel := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	if !util.IsResponsesImageToolModel(responseModel) {
		return ConversationRequest{}, "", HTTPError{Status: 400, Message: "unsupported image_generation model: " + responseModel}
	}
	messages := MessagesFromInput(body["input"], body["instructions"])
	prompt := LatestUserPrompt(messages)
	if prompt == "" {
		return ConversationRequest{}, "", HTTPError{Status: 400, Message: "input text is required"}
	}
	n, err := ParseImageCount(body["n"])
	if err != nil {
		return ConversationRequest{}, "", err
	}
	tool := ResponseImageGenerationTool(body)
	size := firstNonEmpty(util.Clean(tool["size"]), util.Clean(body["size"]), "auto")
	inputImages := ExtractResponseImages(body["input"])
	if len(inputImages) > 0 && util.Clean(tool["size"]) == "" && util.Clean(body["size"]) == "" {
		size = "auto"
	}
	images := []string(nil)
	if previous != nil {
		images = append(images, previous.Images...)
	}
	images = append(images, EncodeImages(inputImages)...)
	if len(images) > maxContextImages {
		images = images[len(images)-maxContextImages:]
	}
	toolModel := firstNonEmpty(util.Clean(tool["model"]), responseModel)
	if !util.IsResponsesImageToolModel(toolModel) {
		return ConversationRequest{}, "", HTTPError{Status: 400, Message: "unsupported image_generation model: " + toolModel}
	}
	outputFormat := NormalizeImageOutputFormat(firstNonEmpty(util.Clean(tool["output_format"]), util.Clean(body["output_format"])))
	partialImages, hasPartialImages := normalizedPositiveInt(firstNonNil(tool["partial_images"], body["partial_images"]))
	request := ConversationRequest{
		Prompt:         prompt,
		Model:          responseImageGenerationModel(toolModel),
		Messages:       messages,
		N:              n,
		Size:           size,
		Quality:        firstNonEmpty(util.Clean(tool["quality"]), util.Clean(body["quality"])),
		Background:     firstNonEmpty(util.Clean(tool["background"]), util.Clean(body["background"])),
		Moderation:     firstNonEmpty(util.Clean(tool["moderation"]), util.Clean(body["moderation"])),
		Style:          firstNonEmpty(util.Clean(tool["style"]), util.Clean(body["style"])),
		OutputFormat:   outputFormat,
		ResponseFormat: firstNonEmpty(util.Clean(tool["response_format"]), util.Clean(body["response_format"]), "b64_json"),
		OwnerID:        scope,
		OwnerName:      util.Clean(body["owner_name"]),
		Images:         images,
		InputImageMask: responseImageMask(firstNonNil(tool["input_image_mask"], body["input_image_mask"])),
	}
	if hasPartialImages {
		request.PartialImages = &partialImages
	}
	if SupportsImageOutputCompression(outputFormat) {
		if compression, ok := normalizedImageOutputCompression(firstNonNil(tool["output_compression"], body["output_compression"])); ok {
			request.OutputCompression = &compression
		}
	}
	return request.Normalized(), prompt, nil
}

func responseImageGenerationModel(model string) string {
	model = strings.TrimSpace(model)
	if util.IsImageGenerationModel(model) {
		if model == util.ImageModelAuto {
			return util.ImageModelGPT
		}
		return model
	}
	return util.ImageModelGPT
}

func (e *Engine) StreamTextResponse(ctx context.Context, body map[string]any) (<-chan map[string]any, <-chan error) {
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	messages := MessagesFromInput(body["input"], body["instructions"])
	return e.StreamTextResponseWithMessages(ctx, model, messages)
}

func (e *Engine) StreamTextResponseWithMessages(ctx context.Context, model string, messages []map[string]any) (<-chan map[string]any, <-chan error) {
	deltas, errCh := e.streamTextDeltasWithTokenRetry(ctx, e.TextBackend(e.Accounts.GetTextAccessToken()), ConversationRequest{Model: model, Messages: messages})
	return streamTextResponseEvents(ctx, model, deltas, errCh)
}

func streamTextResponseEvents(ctx context.Context, model string, deltas <-chan string, upstreamErr <-chan error) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errOut := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errOut)
		responseID := "resp_" + util.NewHex(32)
		itemID := "msg_" + util.NewHex(32)
		created := time.Now().Unix()
		full := ""
		send := func(item map[string]any) bool {
			select {
			case out <- item:
				return true
			case <-ctx.Done():
				errOut <- ctx.Err()
				return false
			}
		}
		if !send(ResponseCreated(responseID, model, created)) {
			return
		}
		if !send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": TextOutputItem("", itemID, "in_progress")}) {
			return
		}
		for delta := range deltas {
			full += delta
			if !send(map[string]any{"type": "response.output_text.delta", "item_id": itemID, "output_index": 0, "content_index": 0, "delta": delta}) {
				return
			}
		}
		if upstreamErr != nil {
			if err := <-upstreamErr; err != nil {
				errOut <- err
				return
			}
		}
		if !send(map[string]any{"type": "response.output_text.done", "item_id": itemID, "output_index": 0, "content_index": 0, "text": full}) {
			return
		}
		item := TextOutputItem(full, itemID, "completed")
		if !send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}) {
			return
		}
		if !send(ResponseCompleted(responseID, model, created, []map[string]any{item})) {
			return
		}
		errOut <- nil
	}()
	return out, errOut
}

func combineErrorChannels(first, second <-chan error) <-chan error {
	out := make(chan error, 1)
	go func() {
		defer close(out)
		var firstErr error
		var secondErr error
		if first != nil {
			firstErr = <-first
		}
		if second != nil {
			secondErr = <-second
		}
		if firstErr != nil {
			out <- firstErr
			return
		}
		out <- secondErr
	}()
	return out
}

func StreamImageResponse(outputs <-chan ImageOutput, prompt, model string) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		responseID := "resp_" + util.NewHex(32)
		created := time.Now().Unix()
		out <- ResponseCreated(responseID, model, created)
		for output := range outputs {
			if output.Kind == "message" {
				item := TextOutputItem(output.Text, "", "completed")
				out <- map[string]any{"type": "response.output_text.delta", "item_id": item["id"], "output_index": 0, "content_index": 0, "delta": output.Text}
				out <- map[string]any{"type": "response.output_text.done", "item_id": item["id"], "output_index": 0, "content_index": 0, "text": output.Text}
				out <- map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}
				out <- ResponseCompleted(responseID, model, created, []map[string]any{item})
				errCh <- nil
				return
			}
			if output.Kind != "result" {
				continue
			}
			items := ImageOutputItems(prompt, output.Data, "")
			if len(items) > 0 {
				item := items[0]
				out <- map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}
				out <- ResponseCompleted(responseID, model, created, []map[string]any{item})
				errCh <- nil
				return
			}
		}
		errCh <- fmt.Errorf("upstream image stream completed without image output")
	}()
	return out, errCh
}

func ResponseCreated(id, model string, created int64) map[string]any {
	return map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "created_at": created, "status": "in_progress", "error": nil, "incomplete_details": nil, "model": model, "output": []any{}, "parallel_tool_calls": false}}
}

func ResponseCompleted(id, model string, created int64, output []map[string]any) map[string]any {
	return map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "object": "response", "created_at": created, "status": "completed", "error": nil, "incomplete_details": nil, "model": model, "output": output, "parallel_tool_calls": false}}
}

func TextOutputItem(text, itemID, status string) map[string]any {
	if itemID == "" {
		itemID = "msg_" + util.NewHex(32)
	}
	return map[string]any{"id": itemID, "type": "message", "status": status, "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}}}
}

func ImageOutputItems(prompt string, data []map[string]any, itemID string) []map[string]any {
	var out []map[string]any
	for _, item := range data {
		b64 := util.Clean(item["b64_json"])
		if b64 == "" {
			continue
		}
		id := itemID
		if id == "" {
			id = fmt.Sprintf("ig_%d", len(out)+1)
		}
		out = append(out, map[string]any{"id": id, "type": "image_generation_call", "status": "completed", "result": b64, "revised_prompt": firstNonEmpty(util.Clean(item["revised_prompt"]), prompt)})
	}
	return out
}

func HasResponseImageGenerationTool(body map[string]any) bool {
	return len(ResponseImageGenerationTool(body)) > 0
}

func ResponseImageGenerationTool(body map[string]any) map[string]any {
	for _, raw := range anyList(body["tools"]) {
		if tool, ok := raw.(map[string]any); ok && util.Clean(tool["type"]) == "image_generation" {
			return tool
		}
	}
	if choice := util.StringMap(body["tool_choice"]); choice != nil && util.Clean(choice["type"]) == "image_generation" {
		return choice
	}
	return nil
}

func responseImageMask(value any) string {
	item := util.StringMap(value)
	imageURL := util.Clean(item["image_url"])
	if imageURL == "" {
		imageURL = util.Clean(value)
	}
	if !strings.HasPrefix(imageURL, "data:") {
		return ""
	}
	return imageURL
}

func ExtractResponsePrompt(input any) string {
	return LatestUserPrompt(responseInputMessages(input))
}

func ExtractResponseImage(input any) *UploadedImage {
	images := ExtractResponseImages(input)
	if len(images) == 0 {
		return nil
	}
	return &images[0]
}

func ExtractResponseImages(input any) []UploadedImage {
	var images []UploadedImage
	var walk func(any)
	walk = func(value any) {
		if text, ok := value.(string); ok {
			images = append(images, ExtractImagesFromText(text)...)
			return
		}
		if list := anyList(value); list != nil {
			for _, raw := range list {
				walk(raw)
			}
			return
		}
		item, ok := value.(map[string]any)
		if !ok {
			return
		}
		switch util.Clean(item["type"]) {
		case "input_image":
			imageURL := util.Clean(item["image_url"])
			if strings.HasPrefix(imageURL, "data:") {
				images = append(images, ExtractImagesFromMessageContent([]any{item})...)
			}
		case "image_generation_call":
			if result := util.Clean(item["result"]); result != "" {
				if data, err := base64.StdEncoding.DecodeString(result); err == nil {
					images = append(images, UploadedImage{Data: data, Filename: "generated.png", ContentType: "image/png"})
				}
			}
		}
		if item["content"] != nil {
			images = append(images, ExtractImagesFromMessageContent(item["content"])...)
		}
	}
	walk(input)
	if len(images) > maxContextImages {
		images = images[len(images)-maxContextImages:]
	}
	return images
}

func MessagesFromInput(input any, instructions any) []map[string]any {
	var messages []map[string]any
	if system := strings.TrimSpace(util.Clean(instructions)); system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	messages = append(messages, responseInputMessages(input)...)
	return NormalizeMessages(messages, nil)
}

func responseInputMessages(input any) []map[string]any {
	if text, ok := input.(string); ok {
		if strings.TrimSpace(text) != "" {
			return []map[string]any{{"role": "user", "content": strings.TrimSpace(text)}}
		}
		return nil
	}
	if item, ok := input.(map[string]any); ok {
		if message, ok := responseMessageFromItem(item); ok {
			return []map[string]any{message}
		}
		return nil
	}
	list := anyList(input)
	allTyped := len(list) > 0
	for _, raw := range list {
		item, ok := raw.(map[string]any)
		allTyped = allTyped && ok && item["type"] != nil && item["role"] == nil
	}
	if allTyped {
		var parts []string
		for _, raw := range list {
			if item, ok := raw.(map[string]any); ok {
				if text := responseContentText([]any{item}); text != "" {
					parts = append(parts, text)
				}
			}
		}
		if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
			return []map[string]any{{"role": "user", "content": text}}
		}
		return nil
	}
	var messages []map[string]any
	for _, raw := range list {
		if item, ok := raw.(map[string]any); ok {
			if message, ok := responseMessageFromItem(item); ok {
				messages = append(messages, message)
			}
		}
	}
	return messages
}

func responseMessageFromItem(item map[string]any) (map[string]any, bool) {
	switch util.Clean(item["type"]) {
	case "input_text":
		if text := strings.TrimSpace(util.Clean(item["text"])); text != "" {
			return map[string]any{"role": "user", "content": text}, true
		}
	case "output_text":
		if text := strings.TrimSpace(util.Clean(item["text"])); text != "" {
			return map[string]any{"role": "assistant", "content": text}, true
		}
	case "image_generation_call":
		if prompt := strings.TrimSpace(util.Clean(item["revised_prompt"])); prompt != "" {
			return map[string]any{"role": "assistant", "content": "Generated image: " + prompt}, true
		}
	}
	if util.Clean(item["type"]) == "message" || item["role"] != nil || item["content"] != nil {
		role := firstNonEmpty(util.Clean(item["role"]), "user")
		if text := responseContentText(item["content"]); text != "" {
			return map[string]any{"role": role, "content": text}, true
		}
	}
	return nil, false
}

func (e *Engine) HandleMessages(ctx context.Context, body map[string]any) (map[string]any, *StreamResult, error) {
	request := MessageRequestFromBody(e, body)
	if util.ToBool(body["stream"]) {
		items, errCh := e.StreamAnthropicEvents(ctx, request)
		return nil, &StreamResult{Items: items, Err: errCh, Kind: "anthropic"}, nil
	}
	items, errCh := e.StreamTextChatCompletion(ctx, e.TextBackend(e.Accounts.GetTextAccessToken()), request.Messages, request.Model)
	text := CollectChatContent(items)
	if err := <-errCh; err != nil {
		return nil, nil, err
	}
	response, err := MessageResponseWithChoice(request.Model, text, CountMessageTokens(request.Messages, request.Model), CountTextTokens(text, request.Model), request.Tools, request.ToolChoice)
	if err != nil {
		return nil, nil, err
	}
	return response, nil, nil
}

type MessageRequest struct {
	Messages   []map[string]any
	Model      string
	Tools      any
	ToolChoice any
}

func MessageRequestFromBody(e *Engine, body map[string]any) MessageRequest {
	payload := util.CopyMap(body)
	policy := tooladapter.PolicyFromToolChoice(payload["tool_choice"])
	payload["messages"] = PreprocessMessages(payload["messages"])
	payload["system"] = MergeSystem(payload["system"], tooladapter.BuildPrompt(payload["tools"], policy))
	return MessageRequest{Messages: NormalizeMessages(payload["messages"], payload["system"]), Model: firstNonEmpty(util.Clean(payload["model"]), "auto"), Tools: payload["tools"], ToolChoice: payload["tool_choice"]}
}

func BuildToolPrompt(tools any) string {
	return tooladapter.BuildPrompt(tools, tooladapter.ChoicePolicy{Mode: tooladapter.ChoiceAuto})
}

func MergeSystem(system any, extra string) any {
	system = CompactSystem(system)
	if hasClaudeCodeSystem(system) {
		extra = xmlToolRule
	}
	if extra == "" {
		return system
	}
	if text, ok := system.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text) + "\n\n" + extra
	}
	if list, ok := system.([]any); ok {
		return append(list, map[string]any{"type": "text", "text": extra})
	}
	return extra
}

func CompactSystem(system any) any {
	switch typed := system.(type) {
	case string:
		return compactSystemText(typed)
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			if block, ok := item.(map[string]any); ok && util.Clean(block["type"]) == "text" {
				copied := util.CopyMap(block)
				copied["text"] = compactSystemText(util.Clean(block["text"]))
				result = append(result, copied)
				continue
			}
			result = append(result, item)
		}
		return result
	default:
		return system
	}
}

func compactSystemText(text string) string {
	return text
}

func compactMessageText(text string) string {
	return text
}

func hasClaudeCodeSystem(system any) bool {
	switch typed := system.(type) {
	case string:
		return strings.Contains(typed, "You are Claude Code")
	case []any:
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if ok && strings.Contains(util.Clean(block["text"]), "You are Claude Code") {
				return true
			}
		}
	}
	return false
}

func PreprocessMessages(messages any) any {
	list := anyList(messages)
	if list == nil {
		return messages
	}
	var out []any
	for _, raw := range list {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		item := util.CopyMap(message)
		if text, ok := item["content"].(string); ok {
			item["content"] = compactMessageText(text)
		} else if blocks := anyList(item["content"]); blocks != nil {
			processed := make([]any, 0, len(blocks))
			for _, block := range blocks {
				processed = append(processed, preprocessBlock(block))
			}
			item["content"] = processed
		}
		out = append(out, item)
	}
	return out
}

func preprocessBlock(block any) any {
	item, ok := block.(map[string]any)
	if !ok {
		return block
	}
	switch util.Clean(item["type"]) {
	case "text":
		copied := util.CopyMap(item)
		copied["text"] = compactMessageText(util.Clean(item["text"]))
		return copied
	case "tool_use":
		data, _ := json.Marshal(item["input"])
		return map[string]any{"type": "text", "text": fmt.Sprintf("<tool_calls><tool_call><tool_name>%s</tool_name><parameters>%s</parameters></tool_call></tool_calls>", util.Clean(item["name"]), string(data))}
	case "tool_result":
		return map[string]any{"type": "text", "text": fmt.Sprintf("Tool result %s: %s", util.Clean(item["tool_use_id"]), util.Clean(item["content"]))}
	default:
		return block
	}
}

func MessageResponse(model, text string, inputTokens, outputTokens int, tools any) map[string]any {
	response, err := MessageResponseWithChoice(model, text, inputTokens, outputTokens, tools, nil)
	if err != nil {
		return map[string]any{"id": "msg_" + util.NewUUID(), "type": "message", "role": "assistant", "model": model, "content": []map[string]any{{"type": "text", "text": StripToolMarkup(text)}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens}}
	}
	return response
}

func MessageResponseWithChoice(model, text string, inputTokens, outputTokens int, tools any, choice any) (map[string]any, error) {
	content, stopReason, err := ContentBlocksWithChoice(text, tools, choice)
	if err != nil {
		return nil, HTTPError{Status: 400, Message: err.Error()}
	}
	return map[string]any{"id": "msg_" + util.NewUUID(), "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": stopReason, "stop_sequence": nil, "usage": map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens}}, nil
}

func ContentBlocks(text string, tools any) ([]map[string]any, string) {
	content, stopReason, err := ContentBlocksWithChoice(text, tools, nil)
	if err != nil {
		return []map[string]any{{"type": "text", "text": StripToolMarkup(text)}}, "end_turn"
	}
	return content, stopReason
}

func noToolsForRequiredChoiceError(policy tooladapter.ChoicePolicy) error {
	if policy.Mode == tooladapter.ChoiceRequired || policy.Mode == tooladapter.ChoiceForced {
		return fmt.Errorf("tool_choice %s requires at least one available tool", policy.Mode)
	}
	return nil
}

func validateToolChoice(policy tooladapter.ChoicePolicy, toolNames []string) error {
	if len(toolNames) == 0 {
		return noToolsForRequiredChoiceError(policy)
	}
	if policy.Mode == tooladapter.ChoiceForced && policy.Name != "" && !hasToolName(toolNames, policy.Name) {
		return fmt.Errorf("tool_choice forced %s is not an available tool", policy.Name)
	}
	return nil
}

func hasToolName(toolNames []string, name string) bool {
	name = strings.TrimSpace(name)
	for _, toolName := range toolNames {
		if strings.TrimSpace(toolName) == name {
			return true
		}
	}
	return false
}

func preflightToolChoiceWithoutToolsError(policy tooladapter.ChoicePolicy, toolNames []string) error {
	return validateToolChoice(policy, toolNames)
}

func ContentBlocksWithChoice(text string, tools any, choice any) ([]map[string]any, string, error) {
	policy := tooladapter.PolicyFromToolChoice(choice)
	toolNames := tooladapter.ToolNames(tools)
	if policy.Mode == tooladapter.ChoiceNone {
		return []map[string]any{{"type": "text", "text": text}}, "end_turn", nil
	}
	if err := validateToolChoice(policy, toolNames); err != nil {
		return nil, "", err
	}
	if len(toolNames) == 0 {
		return []map[string]any{{"type": "text", "text": text}}, "end_turn", nil
	}

	calls, visible, err := tooladapter.Parse(text, toolNames, policy)
	if err != nil {
		return nil, "", err
	}
	calls = tooladapter.NormalizeForSchemas(calls, tools)
	if len(calls) == 0 {
		return []map[string]any{{"type": "text", "text": visible}}, "end_turn", nil
	}

	var content []map[string]any
	if visible != "" {
		content = append(content, map[string]any{"type": "text", "text": visible})
	}
	content = append(content, tooladapter.FormatAnthropic(calls)...)
	return content, "tool_use", nil
}

var streamTextChatCompletionForAnthropic = func(ctx context.Context, e *Engine, request MessageRequest) (<-chan map[string]any, <-chan error) {
	return e.StreamTextChatCompletion(ctx, e.TextBackend(e.Accounts.GetTextAccessToken()), request.Messages, request.Model)
}

func (e *Engine) StreamAnthropicEvents(ctx context.Context, request MessageRequest) (<-chan map[string]any, <-chan error) {
	policy := tooladapter.PolicyFromToolChoice(request.ToolChoice)
	toolNames := tooladapter.ToolNames(request.Tools)
	if err := validateToolChoice(policy, toolNames); err != nil {
		return streamHTTPError(err)
	}
	chunks, errCh := streamTextChatCompletionForAnthropic(ctx, e, request)
	out := make(chan map[string]any)
	outErr := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(outErr)
		messageID := "msg_" + util.NewUUID()
		current := ""
		streamedLen := 0
		toolMode := len(toolNames) > 0 && policy.Mode != tooladapter.ChoiceNone
		toolStarted := false
		textOpen := false
		out <- map[string]any{"type": "message_start", "message": map[string]any{"id": messageID, "type": "message", "role": "assistant", "model": request.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": CountMessageTokens(request.Messages, request.Model), "output_tokens": 0}}}
		if !toolMode {
			textOpen = true
			out <- map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}
		}
		for chunk := range chunks {
			choice := firstChoice(chunk)
			delta := util.StringMap(choice["delta"])
			textDelta, _ := delta["content"].(string)
			if textDelta == "" {
				textDelta = util.Clean(delta["content"])
			}
			if textDelta != "" {
				current += textDelta
				if !toolStarted {
					visible := current
					if toolMode {
						visible = safeRawToolVisiblePrefix(current)
					}
					if len(visible) >= streamedLen {
						next := visible[streamedLen:]
						if next != "" {
							if !textOpen {
								textOpen = true
								out <- map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}
							}
							streamedLen = len(visible)
							out <- map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": next}}
						}
					}
					toolStarted = toolMode && visible != current
				}
			}
			if choice["finish_reason"] != nil {
				content, stopReason, err := ContentBlocksWithChoice(current, request.Tools, request.ToolChoice)
				if err != nil {
					outErr <- HTTPError{Status: 400, Message: err.Error()}
					return
				}
				if stopReason == "end_turn" && streamedLen <= len(current) {
					remaining := current[streamedLen:]
					if remaining != "" {
						if !textOpen {
							textOpen = true
							out <- map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}
						}
						out <- map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": remaining}}
						streamedLen = len(current)
					}
				}
				if textOpen {
					out <- map[string]any{"type": "content_block_stop", "index": 0}
				}
				if stopReason == "tool_use" {
					startIndex := 0
					if textOpen {
						startIndex = 1
						content = toolUseBlocks(content)
					}
					outBufferedBlocks(out, content, startIndex)
				}
				out <- map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": CountTextTokens(current, request.Model)}}
				break
			}
		}
		if err := <-errCh; err != nil {
			outErr <- err
			return
		}
		out <- map[string]any{"type": "message_stop", "created": time.Now().Unix()}
		outErr <- nil
	}()
	return out, outErr
}

func toolUseBlocks(content []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(content))
	for _, block := range content {
		if block["type"] == "tool_use" {
			out = append(out, block)
		}
	}
	return out
}

func outBufferedBlocks(out chan<- map[string]any, content []map[string]any, startIndex int) {
	for offset, block := range content {
		index := startIndex + offset
		if block["type"] == "tool_use" {
			data, _ := json.Marshal(block["input"])
			out <- map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": block["id"], "name": block["name"], "input": map[string]any{}}}
			out <- map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(data)}}
		} else {
			out <- map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}}
			out <- map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": block["text"]}}
		}
		out <- map[string]any{"type": "content_block_stop", "index": index}
	}
}

func CollectChatContent(chunks <-chan map[string]any) string {
	var parts []string
	for chunk := range chunks {
		choice := firstChoice(chunk)
		delta := util.StringMap(choice["delta"])
		if content := util.Clean(delta["content"]); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "")
}

func firstChoice(chunk map[string]any) map[string]any {
	choices := anyList(chunk["choices"])
	if len(choices) == 0 {
		return map[string]any{}
	}
	if choice, ok := choices[0].(map[string]any); ok {
		return choice
	}
	return map[string]any{}
}

type ToolCall struct {
	Name  string
	Input map[string]any
}

func StripToolMarkup(text string) string {
	return tooladapter.StripMarkup(text)
}

func StreamableText(text string) string {
	return tooladapter.StreamableText(text)
}

func ParseToolCalls(text string) []ToolCall {
	calls, _, err := tooladapter.Parse(text, nil, tooladapter.ChoicePolicy{Mode: tooladapter.ChoiceAuto})
	if err != nil {
		return nil
	}
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ToolCall{Name: call.Name, Input: call.Input})
	}
	return out
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func normalizedPositiveInt(value any) (int, bool) {
	if value == nil || strings.TrimSpace(util.Clean(value)) == "" {
		return 0, false
	}
	n := util.ToInt(value, 0)
	if n < 1 {
		return 0, false
	}
	return n, true
}

type HTTPError struct {
	Status  int
	Message string
}

func (e HTTPError) Error() string { return e.Message }
