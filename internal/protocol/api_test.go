package protocol

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func ptrInt(value int) *int {
	return &value
}

func TestChatAndResponsesImageParsing(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	body := map[string]any{
		"model": "gpt-image-2",
		"messages": []any{
			map[string]any{"role": "system", "content": "ignore"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "画一张图"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + imageData}},
			}},
		},
		"n": 2,
	}

	model, prompt, n, images, messages, err := ChatImageArgs(body)
	if err != nil {
		t.Fatalf("ChatImageArgs() error = %v", err)
	}
	if model != "gpt-image-2" || prompt != "画一张图" || n != 2 {
		t.Fatalf("ChatImageArgs() = model %q prompt %q n %d", model, prompt, n)
	}
	if len(messages) != 2 || messages[1]["content"] != "画一张图" {
		t.Fatalf("messages = %#v", messages)
	}
	if len(images) != 1 || string(images[0].Data) != "png-bytes" || images[0].ContentType != "image/png" {
		t.Fatalf("images = %#v", images)
	}

	responseInput := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "生成封面"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + imageData},
		}},
	}
	if prompt := ExtractResponsePrompt(responseInput); prompt != "生成封面" {
		t.Fatalf("ExtractResponsePrompt() = %q", prompt)
	}
	if image := ExtractResponseImage(responseInput); image == nil || string(image.Data) != "png-bytes" {
		t.Fatalf("ExtractResponseImage() = %#v", image)
	}
}

func TestImageRequestDefaultsToAutoModel(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "画一张图"},
		},
	}
	model, prompt, n, _, _, err := ChatImageArgs(body)
	if err != nil {
		t.Fatalf("ChatImageArgs() error = %v", err)
	}
	if model != "auto" || prompt != "画一张图" || n != 1 {
		t.Fatalf("ChatImageArgs() = model %q prompt %q n %d", model, prompt, n)
	}
}

func TestTextModelDoesNotForceImageChatRoute(t *testing.T) {
	if IsImageChatRequest(map[string]any{"model": "gpt-5", "messages": []any{map[string]any{"role": "user", "content": "hello"}}}) {
		t.Fatal("gpt-5 text chat should not be routed as an image request without image modality")
	}
	if !IsImageChatRequest(map[string]any{"model": "gpt-5", "modalities": []any{"image"}, "messages": []any{map[string]any{"role": "user", "content": "draw"}}}) {
		t.Fatal("gpt-5 with image modality should be routed as an image request")
	}
}

func TestImageChatRouteStillWinsWhenToolsPresent(t *testing.T) {
	body := map[string]any{
		"model": "gpt-image-2",
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "把这张图改成海报"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))}},
		}}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "lookup_style",
				"description": "lookup a style preset",
				"parameters":  map[string]any{"type": "object"},
			},
		}},
	}

	if !IsImageChatRequest(body) {
		t.Fatal("image model with function tools should still use the image chat route")
	}
	if HasVisionImages(body) {
		t.Fatal("image model with function tools should not be reclassified as a vision request")
	}
}

func TestMessageResponseUsesSharedToolUseBlocks(t *testing.T) {
	tools := []any{map[string]any{
		"name":        "read_file",
		"description": "read a file",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
	}}
	content := `<tool_calls><tool_call><tool_name>read_file</tool_name><parameters>{"path":"internal/app.go"}</parameters></tool_call></tool_calls>`

	response := MessageResponse("claude", content, 10, 5, tools)
	if response["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %#v, want tool_use", response["stop_reason"])
	}
	blocks := response["content"].([]map[string]any)
	if len(blocks) != 1 || blocks[0]["type"] != "tool_use" || blocks[0]["name"] != "read_file" {
		t.Fatalf("content blocks = %#v, want one read_file tool_use", blocks)
	}
}

func TestMessageRequestFromBodyHonorsToolChoiceNone(t *testing.T) {
	tools := []any{map[string]any{"name": "read_file", "description": "read a file"}}
	body := map[string]any{
		"model":       "claude",
		"tool_choice": "none",
		"tools":       tools,
		"messages":    []any{map[string]any{"role": "user", "content": "hello"}},
	}

	request := MessageRequestFromBody(&Engine{}, body)
	if request.ToolChoice != "none" {
		t.Fatalf("ToolChoice = %#v, want none", request.ToolChoice)
	}
	for _, message := range request.Messages {
		if strings.Contains(message["content"].(string), "Tool:") {
			t.Fatalf("messages included injected tool prompt: %#v", request.Messages)
		}
	}
}

func TestCompletionResponseWithToolCalls(t *testing.T) {
	tools := []any{map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "read_file",
			"description": "read a file",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
		},
	}}
	content := `<tool_calls><tool_call><tool_name>read_file</tool_name><parameters>{"path":"internal/app.go"}</parameters></tool_call></tool_calls>`

	response, err := CompletionResponseWithTools("gpt-5", content, 123, []map[string]any{{"role": "user", "content": "read"}}, tools, nil)
	if err != nil {
		t.Fatalf("CompletionResponseWithTools() error = %v", err)
	}
	choice := response["choices"].([]map[string]any)[0]
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %#v, want tool_calls", choice["finish_reason"])
	}
	message := choice["message"].(map[string]any)
	if message["content"] != nil {
		t.Fatalf("message.content = %#v, want nil", message["content"])
	}
	toolCalls := message["tool_calls"].([]map[string]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v, want one entry", toolCalls)
	}
	function := toolCalls[0]["function"].(map[string]any)
	if function["name"] != "read_file" || !strings.Contains(function["arguments"].(string), "internal/app.go") {
		t.Fatalf("function tool call = %#v", function)
	}
}

func TestCompletionResponseRequiredToolErrorsWithoutCall(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}}
	_, err := CompletionResponseWithTools("gpt-5", "plain text", 123, nil, tools, "required")
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "tool_choice required") {
		t.Fatalf("HTTPError = %#v, want status 400 with tool_choice required", httpErr)
	}
}

func TestCompletionResponseForcedToolRejectsExtraCall(t *testing.T) {
	tools := []any{
		map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}},
		map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
	}
	content := `<tool_calls><tool_call><tool_name>read_file</tool_name><parameters>{}</parameters></tool_call><tool_call><tool_name>search</tool_name><parameters>{}</parameters></tool_call></tool_calls>`
	choice := map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}

	_, err := CompletionResponseWithTools("gpt-5", content, 123, nil, tools, choice)
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "model produced search") {
		t.Fatalf("HTTPError = %#v, want forced tool mismatch", httpErr)
	}
}

func TestCompletionResponseForcedToolErrorsWhenToolMissing(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "search"}}}
	choice := map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}

	_, err := CompletionResponseWithTools("gpt-5", "plain text", 123, nil, tools, choice)
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "not an available tool") {
		t.Fatalf("HTTPError = %#v, want missing forced tool error", httpErr)
	}
}

func TestStreamChatCompletionEventsEmitsFinalToolCalls(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}}
	deltas := make(chan string, 2)
	errs := make(chan error, 1)
	deltas <- `<tool_calls><tool_call><tool_name>read_file</tool_name>`
	deltas <- `<parameters>{"path":"internal/app.go"}</parameters></tool_call></tool_calls>`
	close(deltas)
	errs <- nil
	close(errs)

	events, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, tools, nil)
	var chunks []map[string]any
	for event := range events {
		chunks = append(chunks, event)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("streamChatCompletionEvents() error = %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d, want role and final tool_calls chunks: %#v", len(chunks), chunks)
	}
	roleChoice := chunks[0]["choices"].([]map[string]any)[0]
	roleDelta := roleChoice["delta"].(map[string]any)
	if roleDelta["role"] != "assistant" {
		t.Fatalf("first delta = %#v, want assistant role", roleDelta)
	}
	finalChoice := chunks[len(chunks)-1]["choices"].([]map[string]any)[0]
	if finalChoice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %#v, want tool_calls", finalChoice["finish_reason"])
	}
	finalDelta := finalChoice["delta"].(map[string]any)
	toolCalls := finalDelta["tool_calls"].([]map[string]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v, want one entry", toolCalls)
	}
	function := toolCalls[0]["function"].(map[string]any)
	if function["name"] != "read_file" || !strings.Contains(function["arguments"].(string), "internal/app.go") {
		t.Fatalf("function tool call = %#v", function)
	}
}

func TestStreamChatCompletionEventsRequiredToolErrorsWithoutTools(t *testing.T) {
	deltas := make(chan string, 1)
	errs := make(chan error, 1)
	deltas <- "normal text"
	close(deltas)
	errs <- nil
	close(errs)

	events, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, nil, "required")
	var chunks []map[string]any
	for event := range events {
		chunks = append(chunks, event)
	}
	if len(chunks) != 0 {
		t.Fatalf("streamed chunks = %#v, want none", chunks)
	}
	var httpErr HTTPError
	if err := <-errCh; !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "tool_choice required") {
		t.Fatalf("HTTPError = %#v, want status 400 with tool_choice required", httpErr)
	}
}

func TestStreamChatCompletionEventsForcedToolErrorsWhenToolMissing(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "search"}}}
	choice := map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}
	events, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", nil, nil, tools, choice)
	var chunks []map[string]any
	for event := range events {
		chunks = append(chunks, event)
	}
	if len(chunks) != 0 {
		t.Fatalf("streamed chunks = %#v, want none", chunks)
	}
	var httpErr HTTPError
	if err := <-errCh; !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "not an available tool") {
		t.Fatalf("HTTPError = %#v, want missing forced tool error", httpErr)
	}
}

func TestStreamTextChatCompletionWithToolsRequiredToolErrorsBeforeUpstream(t *testing.T) {
	events, errCh := (&Engine{}).StreamTextChatCompletionWithTools(context.Background(), nil, nil, "gpt-5", nil, "required")
	var chunks []map[string]any
	for event := range events {
		chunks = append(chunks, event)
	}
	if len(chunks) != 0 {
		t.Fatalf("streamed chunks = %#v, want none", chunks)
	}
	var httpErr HTTPError
	if err := <-errCh; !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "tool_choice required") {
		t.Fatalf("HTTPError = %#v, want status 400 with tool_choice required", httpErr)
	}
}

func TestStreamVisionChatCompletionWithToolsRequiredToolErrorsBeforeUpstream(t *testing.T) {
	events, errCh := (&Engine{}).StreamVisionChatCompletionWithTools(context.Background(), nil, nil, "gpt-5", nil, nil, "required")
	var chunks []map[string]any
	for event := range events {
		chunks = append(chunks, event)
	}
	if len(chunks) != 0 {
		t.Fatalf("streamed chunks = %#v, want none", chunks)
	}
	var httpErr HTTPError
	if err := <-errCh; !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "tool_choice required") {
		t.Fatalf("HTTPError = %#v, want status 400 with tool_choice required", httpErr)
	}
}

func TestStreamTextChatCompletionWithToolsForcedToolErrorsBeforeUpstream(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "search"}}}
	choice := map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}
	events, errCh := (&Engine{}).StreamTextChatCompletionWithTools(context.Background(), nil, nil, "gpt-5", tools, choice)
	var chunks []map[string]any
	for event := range events {
		chunks = append(chunks, event)
	}
	if len(chunks) != 0 {
		t.Fatalf("streamed chunks = %#v, want none", chunks)
	}
	var httpErr HTTPError
	if err := <-errCh; !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "not an available tool") {
		t.Fatalf("HTTPError = %#v, want missing forced tool error", httpErr)
	}
}

func TestStreamChatCompletionEventsReturnsPlainTextWhenNoCall(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}}
	deltas := make(chan string, 2)
	errs := make(chan error, 1)
	deltas <- "hello "
	deltas <- "world"
	close(deltas)
	errs <- nil
	close(errs)

	events, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, tools, nil)
	text := ""
	finishReason := any(nil)
	for event := range events {
		choice := event["choices"].([]map[string]any)[0]
		delta := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			text += content
		}
		if choice["finish_reason"] != nil {
			finishReason = choice["finish_reason"]
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("streamChatCompletionEvents() error = %v", err)
	}
	if text != "hello world" {
		t.Fatalf("streamed text = %q, want hello world", text)
	}
	if finishReason != "stop" {
		t.Fatalf("finish_reason = %#v, want stop", finishReason)
	}
}

func TestStreamChatCompletionEventsFlushesHeldMarkerWhenNoToolCall(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}}
	deltas := make(chan string, 2)
	errs := make(chan error, 1)
	deltas <- "\nhello "
	deltas <- "<tool_call"
	close(deltas)
	errs <- nil
	close(errs)

	events, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, tools, nil)
	text := ""
	finishReason := any(nil)
	for event := range events {
		choice := event["choices"].([]map[string]any)[0]
		delta := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			text += content
		}
		if choice["finish_reason"] != nil {
			finishReason = choice["finish_reason"]
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("streamChatCompletionEvents() error = %v", err)
	}
	if text != "\nhello <tool_call" {
		t.Fatalf("streamed text = %q, want \\nhello <tool_call", text)
	}
	if finishReason != "stop" {
		t.Fatalf("finish_reason = %#v, want stop", finishReason)
	}
}

func TestStreamChatCompletionEventsFlushesHeldMarkerInSingleDeltaWhenNoToolCall(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}}
	deltas := make(chan string, 1)
	errs := make(chan error, 1)
	deltas <- "\nhello <tool_call"
	close(deltas)
	errs <- nil
	close(errs)

	events, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, tools, nil)
	text := ""
	finishReason := any(nil)
	for event := range events {
		choice := event["choices"].([]map[string]any)[0]
		delta := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			text += content
		}
		if choice["finish_reason"] != nil {
			finishReason = choice["finish_reason"]
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("streamChatCompletionEvents() error = %v", err)
	}
	if text != "\nhello <tool_call" {
		t.Fatalf("streamed text = %q, want \\nhello <tool_call", text)
	}
	if finishReason != "stop" {
		t.Fatalf("finish_reason = %#v, want stop", finishReason)
	}
}

func TestStreamChatCompletionEventsDoesNotLeakSplitToolMarkup(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}}
	deltas := make(chan string, 3)
	errs := make(chan error, 1)
	deltas <- "visible "
	deltas <- "<"
	deltas <- `tool_calls><tool_call><tool_name>read_file</tool_name><parameters>{"path":"a.go"}</parameters></tool_call></tool_calls>`
	close(deltas)
	errs <- nil
	close(errs)

	events, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, tools, nil)
	text := ""
	finishReason := any(nil)
	var toolCalls []map[string]any
	for event := range events {
		choice := event["choices"].([]map[string]any)[0]
		delta := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			text += content
		}
		if rawToolCalls, ok := delta["tool_calls"].([]map[string]any); ok {
			toolCalls = rawToolCalls
		}
		if choice["finish_reason"] != nil {
			finishReason = choice["finish_reason"]
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("streamChatCompletionEvents() error = %v", err)
	}
	if text != "visible " {
		t.Fatalf("streamed text = %q, want visible ", text)
	}
	if strings.Contains(text, "<") || strings.Contains(text, "tool_calls") {
		t.Fatalf("streamed text leaked tool markup: %q", text)
	}
	if finishReason != "tool_calls" {
		t.Fatalf("finish_reason = %#v, want tool_calls", finishReason)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v, want one entry", toolCalls)
	}
}

func TestStreamChatCompletionEventsDoesNotLeakSplitSingularToolMarkup(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}}
	deltas := make(chan string, 3)
	errs := make(chan error, 1)
	deltas <- "visible "
	deltas <- "<"
	deltas <- `tool_call><tool_name>read_file</tool_name><parameters>{"path":"a.go"}</parameters></tool_call>`
	close(deltas)
	errs <- nil
	close(errs)

	events, errCh := streamChatCompletionEvents(context.Background(), "gpt-5", deltas, errs, tools, nil)
	text := ""
	finishReason := any(nil)
	var toolCalls []map[string]any
	for event := range events {
		choice := event["choices"].([]map[string]any)[0]
		delta := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			text += content
		}
		if rawToolCalls, ok := delta["tool_calls"].([]map[string]any); ok {
			toolCalls = rawToolCalls
		}
		if choice["finish_reason"] != nil {
			finishReason = choice["finish_reason"]
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("streamChatCompletionEvents() error = %v", err)
	}
	if text != "visible " {
		t.Fatalf("streamed text = %q, want visible ", text)
	}
	if strings.Contains(text, "<") || strings.Contains(text, "tool_call") {
		t.Fatalf("streamed text leaked tool markup: %q", text)
	}
	if finishReason != "tool_calls" {
		t.Fatalf("finish_reason = %#v, want tool_calls", finishReason)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v, want one entry", toolCalls)
	}
}

func TestChatPartsInjectToolPromptForTextAndVision(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}}}
	textBody := map[string]any{
		"model":    "gpt-5",
		"messages": []any{map[string]any{"role": "user", "content": "read"}},
		"tools":    tools,
	}
	_, textMessages, err := TextChatParts(textBody)
	if err != nil {
		t.Fatalf("TextChatParts() error = %v", err)
	}
	if len(textMessages) == 0 || textMessages[0]["role"] != "system" || !strings.Contains(textMessages[0]["content"].(string), "Tool: read_file") {
		t.Fatalf("text messages missing tool prompt: %#v", textMessages)
	}

	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	visionBody := map[string]any{
		"model": "gpt-5",
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "read this image"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + imageData}},
		}}},
		"tools": tools,
	}
	_, visionMessages, images, err := VisionChatParts(visionBody)
	if err != nil {
		t.Fatalf("VisionChatParts() error = %v", err)
	}
	if len(visionMessages) == 0 || visionMessages[0]["role"] != "system" || !strings.Contains(visionMessages[0]["content"].(string), "Tool: read_file") {
		t.Fatalf("vision messages missing tool prompt: %#v", visionMessages)
	}
	if len(images) != 1 || string(images[0].Data) != "png-bytes" {
		t.Fatalf("VisionChatParts() images = %#v", images)
	}
}

func TestListModelsUsesInjectedLister(t *testing.T) {
	called := false
	engine := &Engine{
		ListModelsFunc: func(context.Context) (map[string]any, error) {
			called = true
			return map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "custom-model", "object": "model"},
				},
			}, nil
		},
	}

	result, err := engine.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if !called {
		t.Fatal("ListModelsFunc was not called")
	}
	data, _ := result["data"].([]map[string]any)
	if len(data) == 0 || data[0]["id"] != "custom-model" {
		t.Fatalf("ListModels() data = %#v", result["data"])
	}
}

func TestImageContextPromptIncludesHistory(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "保持水彩风格"},
		{"role": "user", "content": "画一只白猫"},
		{"role": "assistant", "content": "Generated image: 白猫坐在窗边"},
		{"role": "user", "content": "把它改成夜晚"},
	}
	prompt := BuildImageContextPrompt(messages, LatestUserPrompt(messages), "1:1", "high")
	for _, want := range []string{"保持水彩风格", "画一只白猫", "白猫坐在窗边", "当前请求:\n把它改成夜晚", "输出为 1:1", "画质使用 High 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("context prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildImagePromptIncludesThreeTwoAndQualityHints(t *testing.T) {
	prompt := BuildImagePrompt("画一张产品照片", "3:2", "medium")
	for _, want := range []string{"画一张产品照片", "3:2 横版构图", "画质使用 Medium 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("image prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildImagePromptIncludesExactResolutionHint(t *testing.T) {
	prompt := BuildImagePrompt("画一张城市海报", "3840x2160", "high")
	for _, want := range []string{"画一张城市海报", "3840 x 2160 像素", "画质使用 High 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("image prompt missing %q: %s", want, prompt)
		}
	}
}

func TestCodexImageModelDropsQualityHint(t *testing.T) {
	request := ConversationRequest{
		Model:   "codex-gpt-image-2",
		Prompt:  "画一张产品照片",
		Size:    "3:2",
		Quality: "high",
	}.Normalized()
	if request.Quality != "" {
		t.Fatalf("Normalized() quality = %q, want empty for codex image model", request.Quality)
	}
	prompt := BuildImagePrompt(request.Prompt, request.Size, request.Quality)
	if strings.Contains(prompt, "画质使用") {
		t.Fatalf("codex image prompt included quality hint: %s", prompt)
	}
	if !strings.Contains(prompt, "3:2 横版构图") {
		t.Fatalf("codex image prompt should still include size hint: %s", prompt)
	}
}

func TestGPTImageModelKeepsQualityHint(t *testing.T) {
	request := ConversationRequest{
		Model:   "gpt-image-2",
		Prompt:  "画一张产品照片",
		Size:    "3:2",
		Quality: " high ",
	}.Normalized()
	if request.Quality != "high" {
		t.Fatalf("Normalized() quality = %q, want high", request.Quality)
	}
	prompt := BuildImagePrompt(request.Prompt, request.Size, request.Quality)
	if !strings.Contains(prompt, "画质使用 High 档") {
		t.Fatalf("gpt image prompt missing quality hint: %s", prompt)
	}
}

func TestNormalizeImageGenerationSizeTiers(t *testing.T) {
	tests := []struct {
		size string
		want string
	}{
		{size: "", want: ""},
		{size: "16:9", want: "16:9"},
		{size: "1080p", want: "1080x1080"},
		{size: " 2K ", want: "2048x2048"},
		{size: "4k", want: "2880x2880"},
		{size: "1536x2048", want: "1536x2048"},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			if got := NormalizeImageGenerationSize(tt.size); got != tt.want {
				t.Fatalf("NormalizeImageGenerationSize(%q) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

func TestConversationRequestNormalizesResolutionTierSize(t *testing.T) {
	request := ConversationRequest{
		Model: "gpt-5.5",
		Size:  "2k",
	}.Normalized()
	if request.Size != "2048x2048" {
		t.Fatalf("Normalized() size = %q, want 2048x2048", request.Size)
	}
}

func TestBuildImagePromptNormalizesResolutionTierHint(t *testing.T) {
	prompt := BuildImagePrompt("画一张城市海报", "2k", "")
	if !strings.Contains(prompt, "2048 x 2048") {
		t.Fatalf("BuildImagePrompt() did not expand 2k tier to exact pixels: %s", prompt)
	}
}

func TestResponsesInputKeepsAssistantAndGeneratedImageContext(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("previous-image"))
	input := []any{
		map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": "上一轮说明"},
		}},
		map[string]any{"type": "image_generation_call", "status": "completed", "result": imageData, "revised_prompt": "一只红色猫"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "把它改成蓝色"},
		}},
	}
	messages := MessagesFromInput(input, "保持同一角色")
	if len(messages) != 4 {
		t.Fatalf("MessagesFromInput() = %#v", messages)
	}
	if messages[0]["role"] != "system" || messages[1]["role"] != "assistant" || messages[2]["role"] != "assistant" || messages[3]["role"] != "user" {
		t.Fatalf("unexpected message roles: %#v", messages)
	}
	if got := LatestUserPrompt(messages); got != "把它改成蓝色" {
		t.Fatalf("LatestUserPrompt() = %q", got)
	}
	images := ExtractResponseImages(input)
	if len(images) != 1 || string(images[0].Data) != "previous-image" {
		t.Fatalf("ExtractResponseImages() = %#v", images)
	}
}

func TestResponseImageGenerationRequestMapsTextModelToOfficialImageFlow(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": "生成封面",
		"tools": []any{
			map[string]any{"type": "image_generation", "size": "16:9", "quality": "high", "output_format": "webp", "output_compression": 37},
		},
	}
	request, prompt, err := ResponseImageGenerationRequest(body, "linuxdo:1", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if prompt != "生成封面" {
		t.Fatalf("prompt = %q, want 生成封面", prompt)
	}
	if request.Model != "gpt-image-2" {
		t.Fatalf("model = %q, want official gpt-image-2 image model", request.Model)
	}
	if !request.SupportsImageGenerationModel() {
		t.Fatal("request should support image generation")
	}
	if request.Size != "16:9" || request.Quality != "high" {
		t.Fatalf("request size/quality = %q/%q, want 16:9/high", request.Size, request.Quality)
	}
	if request.ResponseFormat != "b64_json" {
		t.Fatalf("response format = %q, want b64_json", request.ResponseFormat)
	}
	if request.OutputFormat != "webp" {
		t.Fatalf("output format = %q, want webp", request.OutputFormat)
	}
	if request.OutputCompression != nil {
		t.Fatalf("output compression = %#v, want nil for webp", request.OutputCompression)
	}
}

func TestResponseImageGenerationRouteStillWinsWhenOtherToolsPresent(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "生成一张横版产品图"},
		}}},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":       "lookup_product",
					"parameters": map[string]any{"type": "object"},
				},
			},
			map[string]any{"type": "image_generation", "model": "gpt-image-2", "size": "16:9"},
		},
	}

	if !HasResponseImageGenerationTool(body) {
		t.Fatal("responses body with image_generation plus function tools should use the image generation route")
	}
	request, prompt, err := ResponseImageGenerationRequest(body, "linuxdo:1", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if prompt != "生成一张横版产品图" {
		t.Fatalf("prompt = %q, want 生成一张横版产品图", prompt)
	}
	if request.Model != "gpt-image-2" || !request.UsesResponsesImageRoute() {
		t.Fatalf("request route/model = %q responses=%v, want gpt-image-2 responses image route", request.Model, request.UsesResponsesImageRoute())
	}
}

func TestResponseImageGenerationRequestKeepsJPEGOutputCompression(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": "生成封面",
		"tools": []any{
			map[string]any{"type": "image_generation", "output_format": "jpeg", "output_compression": 37},
		},
	}
	request, _, err := ResponseImageGenerationRequest(body, "linuxdo:1", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if request.OutputFormat != "jpeg" {
		t.Fatalf("output format = %q, want jpeg", request.OutputFormat)
	}
	if request.OutputCompression == nil || *request.OutputCompression != 37 {
		t.Fatalf("output compression = %#v, want 37", request.OutputCompression)
	}
}

func TestResponseImageGenerationRequestPreservesOfficialToolOptions(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "生成封面"},
				map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))},
			}},
		},
		"tools": []any{
			map[string]any{
				"type":               "image_generation",
				"model":              "gpt-image-2",
				"size":               "16:9",
				"quality":            "high",
				"background":         "transparent",
				"moderation":         "auto",
				"style":              "vivid",
				"output_format":      "webp",
				"output_compression": 37,
				"partial_images":     2,
				"input_image_mask":   map[string]any{"image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("mask"))},
			},
		},
	}

	request, _, err := ResponseImageGenerationRequest(body, "admin", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if request.Model != "gpt-image-2" || !request.UsesResponsesImageRoute() {
		t.Fatalf("request route/model = %q responses=%v", request.Model, request.UsesResponsesImageRoute())
	}
	if request.Background != "transparent" || request.Moderation != "auto" || request.Style != "vivid" {
		t.Fatalf("tool options = background:%q moderation:%q style:%q", request.Background, request.Moderation, request.Style)
	}
	if request.PartialImages == nil || *request.PartialImages != 2 {
		t.Fatalf("PartialImages = %#v, want 2", request.PartialImages)
	}
	if request.InputImageMask == "" {
		t.Fatal("InputImageMask was not preserved")
	}
}

func TestCodexImageModelStillUsesSeparateCodexImageRoute(t *testing.T) {
	request := ConversationRequest{Model: "codex-gpt-image-2"}
	if !request.UsesResponsesImageRoute() {
		t.Fatal("codex-gpt-image-2 should still use the dedicated responses image route")
	}
}

func TestResponseImageGenerationRequestKeepsPreviousContextOutOfOfficialPrompt(t *testing.T) {
	previous := ResponseContext{
		Messages: []map[string]any{
			{"role": "assistant", "content": "Generated image: 白猫坐在窗边"},
			{"role": "user", "content": "把它改成夜晚"},
		},
	}
	body := map[string]any{
		"model": "gpt-5.5",
		"input": "把它改成蓝色",
		"tools": []any{
			map[string]any{"type": "image_generation", "model": "gpt-image-2", "size": "16:9", "quality": "high"},
		},
	}

	request, prompt, err := ResponseImageGenerationRequest(body, "admin", &previous)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if prompt != "把它改成蓝色" {
		t.Fatalf("prompt = %q, want 当前输入原文", prompt)
	}
	if len(request.Messages) == 0 {
		t.Fatal("request.Messages should keep response context for storage/continuation bookkeeping")
	}
	finalPrompt := buildResponsesImagePrompt(request.Prompt, request.Size, request.Model)
	for _, unwanted := range []string{"历史上下文", "当前请求:", "白猫坐在窗边", "画质使用 High 档"} {
		if strings.Contains(finalPrompt, unwanted) {
			t.Fatalf("official image prompt unexpectedly contains %q: %s", unwanted, finalPrompt)
		}
	}
	if !strings.Contains(finalPrompt, "16:9 横屏构图") {
		t.Fatalf("finalPrompt missing size hint: %s", finalPrompt)
	}
}

func TestBuildResponsesImagePromptKeepsOfficialRouteCloseToRawPrompt(t *testing.T) {
	prompt := buildResponsesImagePrompt("画一张产品照片", "16:9", "gpt-image-2")
	if !strings.Contains(prompt, "16:9 横屏构图") {
		t.Fatalf("buildResponsesImagePrompt() missing size hint: %s", prompt)
	}
	for _, unwanted := range []string{"画质使用", "透明背景", "整体风格偏向", "历史上下文", "当前请求:"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("buildResponsesImagePrompt() unexpectedly contains %q: %s", unwanted, prompt)
		}
	}
}

func TestBuildResponsesImagePromptLeavesCodexPromptUntouched(t *testing.T) {
	const raw = "画一张产品照片"
	if prompt := buildResponsesImagePrompt(raw, "16:9", "codex-gpt-image-2"); prompt != raw {
		t.Fatalf("buildResponsesImagePrompt() = %q, want %q", prompt, raw)
	}
}

func TestResponseImageGenerationRequestAcceptsCodexImageToolAlias(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": "生成封面",
		"tools": []any{
			map[string]any{"type": "image_generation", "model": "codex-gpt-image-2"},
		},
	}
	request, _, err := ResponseImageGenerationRequest(body, "admin", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if request.Model != "codex-gpt-image-2" {
		t.Fatalf("request model = %q, want codex-gpt-image-2", request.Model)
	}
	if !request.UsesResponsesImageRoute() {
		t.Fatal("codex-gpt-image-2 image_generation request should use the dedicated responses image route")
	}
}

func TestResponseImageGenerationRequestUsesToolImageModel(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": "生成封面",
		"tools": []any{
			map[string]any{"type": "image_generation", "model": "gpt-image-2", "size": "2048x2048"},
		},
	}
	request, _, err := ResponseImageGenerationRequest(body, "admin", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if request.Model != "gpt-image-2" {
		t.Fatalf("request model = %q, want image tool model gpt-image-2", request.Model)
	}
}

func TestResponseImageGenerationToolAcceptsTypedToolSlice(t *testing.T) {
	body := map[string]any{
		"model": "gpt-image-2",
		"input": "生成封面",
		"tools": []map[string]any{
			{"type": "image_generation", "size": "2880x2880", "output_format": "png"},
		},
	}
	if !HasResponseImageGenerationTool(body) {
		t.Fatal("typed []map[string]any tools should route as responses image generation")
	}
	request, prompt, err := ResponseImageGenerationRequest(body, "admin", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if prompt != "生成封面" {
		t.Fatalf("prompt = %q, want 生成封面", prompt)
	}
	if request.Model != "gpt-image-2" {
		t.Fatalf("model = %q, want gpt-image-2", request.Model)
	}
	if request.Size != "2880x2880" {
		t.Fatalf("request size = %q, want 2880x2880", request.Size)
	}
}

func TestResponseImageGenerationRequestDefaultsImageModelForAuto(t *testing.T) {
	body := map[string]any{
		"model": "auto",
		"input": "生成封面",
		"tools": []any{
			map[string]any{"type": "image_generation"},
		},
	}
	request, _, err := ResponseImageGenerationRequest(body, "", nil)
	if err != nil {
		t.Fatalf("ResponseImageGenerationRequest() error = %v", err)
	}
	if request.Model != "gpt-image-2" {
		t.Fatalf("model = %q, want gpt-image-2 official image route", request.Model)
	}
	if request.Size != "auto" {
		t.Fatalf("size = %q, want auto", request.Size)
	}
}

func TestToolCallParsing(t *testing.T) {
	text := `先处理
<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path><![CDATA[internal/app.go]]></path><limit>5</limit></parameters></tool_call></tool_calls>`
	calls := ParseToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("ParseToolCalls() = %#v", calls)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("tool name = %q", calls[0].Name)
	}
	if calls[0].Input["path"] != "internal/app.go" || calls[0].Input["limit"] != float64(5) {
		t.Fatalf("tool input = %#v", calls[0].Input)
	}
	if visible := StreamableText(text); visible != "先处理" {
		t.Fatalf("StreamableText() = %q", visible)
	}
	if stripped := StripToolMarkup(text); stripped != "先处理" {
		t.Fatalf("StripToolMarkup() = %q", stripped)
	}

	fenced := "```xml\n<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path>hidden</path></parameters></tool_call></tool_calls>\n```"
	if calls := ParseToolCalls(fenced); len(calls) != 0 {
		t.Fatalf("ParseToolCalls() parsed fenced XML = %#v", calls)
	}

	repeated := `<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path>a.go</path><path>b.go</path></parameters></tool_call></tool_calls>`
	calls = ParseToolCalls(repeated)
	if len(calls) != 1 {
		t.Fatalf("ParseToolCalls() repeated fields = %#v", calls)
	}
	paths, ok := calls[0].Input["path"].([]any)
	if !ok || len(paths) != 2 || paths[0] != "a.go" || paths[1] != "b.go" {
		t.Fatalf("repeated path input = %#v", calls[0].Input["path"])
	}
}

func TestStreamAnthropicEventsDoesNotDuplicateStreamedTextBeforeToolUse(t *testing.T) {
	const visible = "先处理"
	const toolXML = `<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path>internal/app.go</path></parameters></tool_call></tool_calls>`

	oldStreamTextChatCompletion := streamTextChatCompletionForAnthropic
	streamTextChatCompletionForAnthropic = func(ctx context.Context, e *Engine, request MessageRequest) (<-chan map[string]any, <-chan error) {
		chunks := make(chan map[string]any, 3)
		errs := make(chan error, 1)
		chunks <- CompletionChunk(request.Model, map[string]any{"role": "assistant", "content": visible + "\n"}, nil, "chatcmpl_test", 1)
		chunks <- CompletionChunk(request.Model, map[string]any{"content": toolXML}, nil, "chatcmpl_test", 1)
		chunks <- CompletionChunk(request.Model, map[string]any{}, "stop", "chatcmpl_test", 1)
		close(chunks)
		errs <- nil
		close(errs)
		return chunks, errs
	}
	defer func() { streamTextChatCompletionForAnthropic = oldStreamTextChatCompletion }()
	engine := &Engine{}
	request := MessageRequest{
		Model: "auto",
		Tools: []any{map[string]any{
			"name":        "read_file",
			"description": "read a file",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
		}},
	}

	events, errCh := engine.StreamAnthropicEvents(context.Background(), request)
	textStarts := 0
	toolStarts := 0
	textDelta := ""
	toolIndex := -1
	for event := range events {
		if event["type"] == "content_block_start" {
			block := event["content_block"].(map[string]any)
			switch block["type"] {
			case "text":
				textStarts++
			case "tool_use":
				toolStarts++
				toolIndex = event["index"].(int)
			}
		}
		if event["type"] == "content_block_delta" && event["index"] == 0 {
			delta := event["delta"].(map[string]any)
			if delta["type"] == "text_delta" {
				textDelta += delta["text"].(string)
			}
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamAnthropicEvents() error = %v", err)
	}
	if textStarts != 1 || toolStarts != 1 {
		t.Fatalf("content block starts = text:%d tool_use:%d, want 1/1", textStarts, toolStarts)
	}
	if strings.TrimSpace(textDelta) != visible {
		t.Fatalf("streamed text delta = %q, want %q", textDelta, visible)
	}
	if toolIndex != 1 {
		t.Fatalf("tool_use index = %d, want 1", toolIndex)
	}
}

func TestStreamAnthropicEventsDoesNotLeakSplitToolMarkup(t *testing.T) {
	const toolXMLTail = `tool_calls><tool_call><tool_name>read_file</tool_name><parameters>{"path":"a.go"}</parameters></tool_call></tool_calls>`

	oldStreamTextChatCompletion := streamTextChatCompletionForAnthropic
	streamTextChatCompletionForAnthropic = func(ctx context.Context, e *Engine, request MessageRequest) (<-chan map[string]any, <-chan error) {
		chunks := make(chan map[string]any, 3)
		errs := make(chan error, 1)
		chunks <- CompletionChunk(request.Model, map[string]any{"role": "assistant", "content": "visible <"}, nil, "chatcmpl_test", 1)
		chunks <- CompletionChunk(request.Model, map[string]any{"content": toolXMLTail}, nil, "chatcmpl_test", 1)
		chunks <- CompletionChunk(request.Model, map[string]any{}, "stop", "chatcmpl_test", 1)
		close(chunks)
		errs <- nil
		close(errs)
		return chunks, errs
	}
	defer func() { streamTextChatCompletionForAnthropic = oldStreamTextChatCompletion }()

	events, errCh := (&Engine{}).StreamAnthropicEvents(context.Background(), MessageRequest{
		Model: "auto",
		Tools: []any{map[string]any{"name": "read_file", "description": "read a file"}},
	})
	text := ""
	toolStarts := 0
	for event := range events {
		if event["type"] == "content_block_start" {
			block := event["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolStarts++
			}
		}
		if event["type"] == "content_block_delta" {
			delta := event["delta"].(map[string]any)
			if delta["type"] == "text_delta" {
				text += delta["text"].(string)
			}
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamAnthropicEvents() error = %v", err)
	}
	if text != "visible " {
		t.Fatalf("streamed text = %q, want visible ", text)
	}
	if strings.Contains(text, "<") || strings.Contains(text, "tool_calls") {
		t.Fatalf("streamed text leaked tool markup: %q", text)
	}
	if toolStarts != 1 {
		t.Fatalf("tool_use starts = %d, want 1", toolStarts)
	}
}

func TestStreamAnthropicEventsFlushesHeldMarkerWhenNoToolCall(t *testing.T) {
	oldStreamTextChatCompletion := streamTextChatCompletionForAnthropic
	streamTextChatCompletionForAnthropic = func(ctx context.Context, e *Engine, request MessageRequest) (<-chan map[string]any, <-chan error) {
		chunks := make(chan map[string]any, 2)
		errs := make(chan error, 1)
		chunks <- CompletionChunk(request.Model, map[string]any{"role": "assistant", "content": "\nhello <tool_call"}, nil, "chatcmpl_test", 1)
		chunks <- CompletionChunk(request.Model, map[string]any{}, "stop", "chatcmpl_test", 1)
		close(chunks)
		errs <- nil
		close(errs)
		return chunks, errs
	}
	defer func() { streamTextChatCompletionForAnthropic = oldStreamTextChatCompletion }()

	events, errCh := (&Engine{}).StreamAnthropicEvents(context.Background(), MessageRequest{
		Model: "auto",
		Tools: []any{map[string]any{"name": "read_file", "description": "read a file"}},
	})
	text := ""
	stopReason := any(nil)
	for event := range events {
		if event["type"] == "content_block_delta" {
			delta := event["delta"].(map[string]any)
			if delta["type"] == "text_delta" {
				text += delta["text"].(string)
			}
		}
		if event["type"] == "message_delta" {
			delta := event["delta"].(map[string]any)
			stopReason = delta["stop_reason"]
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamAnthropicEvents() error = %v", err)
	}
	if text != "\nhello <tool_call" {
		t.Fatalf("streamed text = %q, want \\nhello <tool_call", text)
	}
	if stopReason != "end_turn" {
		t.Fatalf("stop_reason = %#v, want end_turn", stopReason)
	}
}

func TestStreamAnthropicEventsRequiredToolErrorsWithoutTools(t *testing.T) {
	events, errCh := (&Engine{}).StreamAnthropicEvents(context.Background(), MessageRequest{Model: "auto", ToolChoice: "required"})
	var emitted []map[string]any
	for event := range events {
		emitted = append(emitted, event)
	}
	if len(emitted) != 0 {
		t.Fatalf("events = %#v, want none", emitted)
	}
	var httpErr HTTPError
	if err := <-errCh; !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "tool_choice required") {
		t.Fatalf("HTTPError = %#v, want status 400 with tool_choice required", httpErr)
	}
}

func TestStreamAnthropicEventsForcedToolErrorsWhenToolMissing(t *testing.T) {
	tools := []any{map[string]any{"name": "search"}}
	choice := map[string]any{"type": "tool", "name": "read_file"}
	events, errCh := (&Engine{}).StreamAnthropicEvents(context.Background(), MessageRequest{Model: "auto", Tools: tools, ToolChoice: choice})
	var emitted []map[string]any
	for event := range events {
		emitted = append(emitted, event)
	}
	if len(emitted) != 0 {
		t.Fatalf("events = %#v, want none", emitted)
	}
	var httpErr HTTPError
	if err := <-errCh; !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "not an available tool") {
		t.Fatalf("HTTPError = %#v, want missing forced tool error", httpErr)
	}
}

func TestMessageResponseRequiredToolErrorsWithoutTools(t *testing.T) {
	_, err := MessageResponseWithChoice("claude", "plain text", 10, 2, nil, "required")
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "tool_choice required") {
		t.Fatalf("HTTPError = %#v, want status 400 with tool_choice required", httpErr)
	}
}

func TestMessageResponseForcedToolErrorsWhenToolMissing(t *testing.T) {
	tools := []any{map[string]any{"name": "search"}}
	choice := map[string]any{"type": "tool", "name": "read_file"}
	_, err := MessageResponseWithChoice("claude", "plain text", 10, 2, tools, choice)
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "not an available tool") {
		t.Fatalf("HTTPError = %#v, want missing forced tool error", httpErr)
	}
}

func TestCompletionResponseRequiredToolErrorsWithoutTools(t *testing.T) {
	_, err := CompletionResponseWithTools("gpt-5", "plain text", 123, nil, nil, "required")
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.Status != 400 || !strings.Contains(httpErr.Message, "tool_choice required") {
		t.Fatalf("HTTPError = %#v, want status 400 with tool_choice required", httpErr)
	}
}

func TestStreamImageResponseErrorsWhenNoImageOutput(t *testing.T) {
	outputs := make(chan ImageOutput)
	close(outputs)
	events, errCh := StreamImageResponse(outputs, "draw", "gpt-image-2")
	var count int
	for range events {
		count++
	}
	if count != 1 {
		t.Fatalf("event count = %d, want response.created only", count)
	}
	if err := <-errCh; err == nil || err.Error() != "upstream image stream completed without image output" {
		t.Fatalf("err = %v", err)
	}
}

func TestCollectImageOutputsMarksTextResponse(t *testing.T) {
	outputs := make(chan ImageOutput)
	close(outputs)
	errCh := make(chan error, 1)
	errCh <- &ImageGenerationError{Message: "text response", StatusCode: 400, Type: "invalid_request_error", Code: "image_generation_text_response"}
	close(errCh)

	result, err := (&Engine{}).CollectImageOutputs(outputs, errCh)
	if err == nil {
		t.Fatal("CollectImageOutputs() err = nil, want text response error")
	}
	if result["output_type"] != "text" {
		t.Fatalf("output_type = %#v, want text in %#v", result["output_type"], result)
	}
	if result["message"] != "text response" {
		t.Fatalf("message = %#v, want text response", result["message"])
	}
}

func TestStreamTextResponseEventsPropagatesUpstreamError(t *testing.T) {
	deltas := make(chan string, 1)
	upstreamErr := make(chan error, 1)
	deltas <- "partial"
	close(deltas)
	upstreamErr <- errors.New("upstream failed")
	close(upstreamErr)

	events, errCh := streamTextResponseEvents(context.Background(), "auto", deltas, upstreamErr)
	var types []string
	for event := range events {
		if eventType, ok := event["type"].(string); ok {
			types = append(types, eventType)
		}
	}
	if err := <-errCh; err == nil || err.Error() != "upstream failed" {
		t.Fatalf("err = %v, want upstream failed", err)
	}
	for _, eventType := range types {
		if eventType == "response.completed" || eventType == "response.output_text.done" {
			t.Fatalf("unexpected completion event after upstream error: %v", types)
		}
	}
}

func TestHandleImageGenerationsValidatesPromptAndCount(t *testing.T) {
	engine := &Engine{}
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{name: "empty prompt", body: map[string]any{"n": 1}, want: "prompt is required"},
		{name: "too many images", body: map[string]any{"prompt": "draw", "n": 5}, want: "n must be between 1 and 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := engine.HandleImageGenerations(context.Background(), tc.body)
			var httpErr HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("err = %T %v, want HTTPError", err, err)
			}
			if httpErr.Status != 400 || httpErr.Message != tc.want {
				t.Fatalf("HTTPError = %#v, want status 400 message %q", httpErr, tc.want)
			}
		})
	}
}

func TestApplyImageOutputOptionsToRequest(t *testing.T) {
	jpegRequest := ConversationRequest{}
	applyImageOutputOptionsToRequest(&jpegRequest, ImageOutputOptions{Format: "jpeg", Compression: ptrInt(25)})
	jpegRequest = jpegRequest.Normalized()
	if jpegRequest.OutputFormat != "jpeg" || jpegRequest.OutputCompression == nil || *jpegRequest.OutputCompression != 25 {
		t.Fatalf("jpeg output options = %#v/%#v, want jpeg/25", jpegRequest.OutputFormat, jpegRequest.OutputCompression)
	}

	pngRequest := ConversationRequest{}
	applyImageOutputOptionsToRequest(&pngRequest, ImageOutputOptions{Format: "png", Compression: ptrInt(25)})
	pngRequest = pngRequest.Normalized()
	if pngRequest.OutputFormat != "png" || pngRequest.OutputCompression != nil {
		t.Fatalf("png output options = %#v/%#v, want png/nil", pngRequest.OutputFormat, pngRequest.OutputCompression)
	}

	webpRequest := ConversationRequest{}
	applyImageOutputOptionsToRequest(&webpRequest, ImageOutputOptions{Format: "webp", Compression: ptrInt(25)})
	webpRequest = webpRequest.Normalized()
	if webpRequest.OutputFormat != "webp" || webpRequest.OutputCompression != nil {
		t.Fatalf("webp output options = %#v/%#v, want webp/nil", webpRequest.OutputFormat, webpRequest.OutputCompression)
	}
}

func TestMergeSystemUsesCompactToolRuleForClaudeCode(t *testing.T) {
	merged := MergeSystem("You are Claude Code, an agent.", "Available tools:\nTool: read_file\n\nTool use rules:\nverbose")
	text, ok := merged.(string)
	if !ok {
		t.Fatalf("MergeSystem() = %T, want string", merged)
	}
	if strings.Contains(text, "Available tools:") {
		t.Fatalf("MergeSystem() kept verbose tool prompt: %q", text)
	}
	if !strings.Contains(text, "Tool output adapter") || !strings.Contains(text, "<tool_calls>") {
		t.Fatalf("MergeSystem() missing compact XML rule: %q", text)
	}
}
