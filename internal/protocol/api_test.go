package protocol

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

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
	if err := <-errCh; err == nil || err.Error() != "image generation failed" {
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
