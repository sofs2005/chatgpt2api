package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"chatgpt2api/internal/util"
)

const (
	ResponsesImageMainModel      = "gpt-5.4-mini"
	ResponsesImageCodexToolModel = "gpt-5.4-mini"

	codexResponsesPath       = "/backend-api/codex/responses"
	codexResponsesUserAgent  = "codex-tui/0.128.0 (Mac OS 26.3.1; arm64) iTerm.app/3.6.9 (codex-tui; 0.128.0)"
	codexResponsesOriginator = "codex-tui"

	responsesImageDefaultSize  = "1024x1024"
	responsesImageSizeMultiple = 16
	responsesImageMaxEdge      = 3840
	responsesImageMaxRatio     = 3
	responsesImageMinPixels    = 655360
	responsesImageMaxPixels    = 8294400
)

type ResponsesInputImage struct {
	Data        []byte
	ContentType string
}

type ResponsesImageRequest struct {
	Prompt            string
	Model             string
	Size              string
	Quality           string
	Background        string
	Moderation        string
	Style             string
	OutputFormat      string
	OutputCompression *int
	PartialImages     *int
	InputImages       []ResponsesInputImage
	InputImageMask    *ResponsesInputImage
}

type ResponsesImageEvent struct {
	Type              string
	ItemID            string
	Result            string
	PartialImage      string
	PartialImageIndex int
	RevisedPrompt     string
	OutputFormat      string
	Background        string
	Size              string
	Quality           string
	Model             string
	Created           int64
	Raw               map[string]any
}

func (c *Client) StreamResponsesImage(ctx context.Context, request ResponsesImageRequest) (<-chan ResponsesImageEvent, <-chan error) {
	out := make(chan ResponsesImageEvent)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if strings.TrimSpace(c.AccessToken) == "" {
			errCh <- fmt.Errorf("access_token is required for codex responses image route")
			return
		}
		accountID := c.chatGPTAccountID()
		headers, err := c.responsesImageHeaders(accountID)
		if err != nil {
			errCh <- err
			return
		}
		payload, err := buildResponsesImagePayload(request)
		if err != nil {
			errCh <- err
			return
		}
		resp, err := c.postRaw(ctx, codexResponsesPath, payload, headers, true)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			errCh <- upstreamHTTPError(codexResponsesPath, resp.StatusCode, data)
			return
		}
		errCh <- iterResponsesImageSSE(ctx, resp.Body, out)
	}()
	return out, errCh
}

func (c *Client) responsesImageHeaders(accountID string) (map[string]string, error) {
	if strings.TrimSpace(c.AccessToken) == "" {
		return nil, fmt.Errorf("access_token is required for codex responses image route")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("chatgpt_account_id is required for codex responses image route")
	}
	if c.sessionID == "" {
		c.sessionID = util.NewUUID()
	}
	return map[string]string{
		"Authorization":         "Bearer " + c.AccessToken,
		"Chatgpt-Account-Id":    accountID,
		"Content-Type":          "application/json",
		"Accept":                "text/event-stream",
		"User-Agent":            codexResponsesUserAgent,
		"Originator":            codexResponsesOriginator,
		"Session_id":            c.sessionID,
		"Connection":            "Keep-Alive",
		"X-OpenAI-Target-Path":  codexResponsesPath,
		"X-OpenAI-Target-Route": codexResponsesPath,
	}, nil
}

func buildResponsesImagePayload(request ResponsesImageRequest) ([]byte, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	content := []any{map[string]any{"type": "input_text", "text": prompt}}
	for _, image := range request.InputImages {
		if len(image.Data) == 0 {
			continue
		}
		content = append(content, map[string]any{"type": "input_image", "image_url": imageDataURL(image)})
	}
	tool := map[string]any{"type": "image_generation", "action": "generate"}
	if len(request.InputImages) > 0 {
		tool["action"] = "edit"
	}
	if model := normalizeResponsesImageToolModel(request.Model); model != "" {
		tool["model"] = model
	}
	if size := normalizeResponsesImageToolSize(request.Size); size != "" {
		tool["size"] = size
	}
	for key, value := range map[string]string{
		"quality":       request.Quality,
		"background":    request.Background,
		"moderation":    request.Moderation,
		"style":         request.Style,
		"output_format": request.OutputFormat,
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			tool[key] = trimmed
		}
	}
	if request.OutputCompression != nil {
		tool["output_compression"] = *request.OutputCompression
	}
	if request.PartialImages != nil {
		tool["partial_images"] = *request.PartialImages
	}
	if request.InputImageMask != nil && len(request.InputImageMask.Data) > 0 {
		tool["input_image_mask"] = map[string]any{"image_url": imageDataURL(*request.InputImageMask)}
	}
	payload := map[string]any{
		"model":               ResponsesImageMainModel,
		"input":               []any{map[string]any{"role": "user", "content": content}},
		"tools":               []any{tool},
		"tool_choice":         map[string]any{"type": "image_generation"},
		"instructions":        "You generate and edit images for the user.",
		"stream":              true,
		"store":               false,
		"parallel_tool_calls": true,
		"include":             []string{"reasoning.encrypted_content"},
	}
	return json.Marshal(payload)
}

func normalizeResponsesImageToolModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", util.ImageModelAuto, "gpt-image-1", util.ImageModelGPT:
		return ""
	case util.ImageModelCodex:
		return ResponsesImageCodexToolModel
	case ResponsesImageCodexToolModel:
		return ResponsesImageCodexToolModel
	case util.ImageModelGPT54:
		return util.ImageModelGPT54
	case util.ImageModelGPT55:
		return util.ImageModelGPT55
	case "gpt-5-5-thinking":
		return "gpt-5-5-thinking"
	default:
		return ""
	}
}

func normalizeResponsesImageToolSize(size string) string {
	normalized := strings.ToLower(strings.TrimSpace(size))
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "×", "x")
	if normalized == "" || normalized == "auto" {
		return ""
	}
	switch normalized {
	case "1080p":
		return normalizeResponsesImageDimensions(1080, 1080)
	case "2k":
		return normalizeResponsesImageDimensions(2048, 2048)
	case "4k":
		return normalizeResponsesImageDimensions(3840, 3840)
	}
	if width, height, ok := parseResponsesImageDimensions(normalized); ok {
		if width < 128 && height < 128 {
			return responsesImageSizeFromRatio(float64(width), float64(height))
		}
		return normalizeResponsesImageDimensions(width, height)
	}
	if ratioWidth, ratioHeight, ok := parseResponsesImageRatio(normalized); ok {
		return responsesImageSizeFromRatio(ratioWidth, ratioHeight)
	}
	return ""
}

func responsesImageSizeFromRatio(ratioWidth, ratioHeight float64) string {
	if ratioWidth <= 0 || ratioHeight <= 0 {
		return ""
	}
	if ratioWidth == ratioHeight {
		return responsesImageDefaultSize
	}
	if ratioWidth > ratioHeight {
		return normalizeResponsesImageDimensions(1536, int(math.Round(1536*ratioHeight/ratioWidth)))
	}
	return normalizeResponsesImageDimensions(int(math.Round(1536*ratioWidth/ratioHeight)), 1536)
}

func normalizeResponsesImageDimensions(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	normalizedWidth := roundToResponsesImageMultiple(width)
	normalizedHeight := roundToResponsesImageMultiple(height)

	scaleToFit := func(scale float64) {
		normalizedWidth = floorToResponsesImageMultiple(float64(normalizedWidth) * scale)
		normalizedHeight = floorToResponsesImageMultiple(float64(normalizedHeight) * scale)
	}
	scaleToFill := func(scale float64) {
		normalizedWidth = ceilToResponsesImageMultiple(float64(normalizedWidth) * scale)
		normalizedHeight = ceilToResponsesImageMultiple(float64(normalizedHeight) * scale)
	}

	for range 4 {
		maxEdge := max(normalizedWidth, normalizedHeight)
		if maxEdge > responsesImageMaxEdge {
			scaleToFit(float64(responsesImageMaxEdge) / float64(maxEdge))
		}

		if normalizedWidth > normalizedHeight*responsesImageMaxRatio {
			normalizedWidth = floorToResponsesImageMultiple(float64(normalizedHeight * responsesImageMaxRatio))
		} else if normalizedHeight > normalizedWidth*responsesImageMaxRatio {
			normalizedHeight = floorToResponsesImageMultiple(float64(normalizedWidth * responsesImageMaxRatio))
		}

		pixels := normalizedWidth * normalizedHeight
		if pixels > responsesImageMaxPixels {
			scaleToFit(math.Sqrt(float64(responsesImageMaxPixels) / float64(pixels)))
		} else if pixels < responsesImageMinPixels {
			scaleToFill(math.Sqrt(float64(responsesImageMinPixels) / float64(pixels)))
		}
	}

	return fmt.Sprintf("%dx%d", normalizedWidth, normalizedHeight)
}

func parseResponsesImageDimensions(value string) (int, int, bool) {
	match := regexp.MustCompile(`^(\d+)x(\d+)$`).FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(match[1])
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.Atoi(match[2])
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func parseResponsesImageRatio(value string) (float64, float64, bool) {
	match := regexp.MustCompile(`^(\d+(?:\.\d+)?):(\d+(?:\.\d+)?)$`).FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	width, err := strconv.ParseFloat(match[1], 64)
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.ParseFloat(match[2], 64)
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func roundToResponsesImageMultiple(value int) int {
	return max(responsesImageSizeMultiple, int(math.Round(float64(value)/responsesImageSizeMultiple))*responsesImageSizeMultiple)
}

func floorToResponsesImageMultiple(value float64) int {
	return max(responsesImageSizeMultiple, int(math.Floor(value/responsesImageSizeMultiple))*responsesImageSizeMultiple)
}

func ceilToResponsesImageMultiple(value float64) int {
	return max(responsesImageSizeMultiple, int(math.Ceil(value/responsesImageSizeMultiple))*responsesImageSizeMultiple)
}

func imageDataURL(image ResponsesInputImage) string {
	contentType := strings.TrimSpace(image.ContentType)
	if contentType == "" {
		contentType = "image/png"
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
}

func iterResponsesImageSSE(ctx context.Context, reader io.Reader, out chan<- ResponsesImageEvent) error {
	payloads := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(payloads)
		errCh <- iterSSEPayloads(ctx, reader, payloads)
	}()
	for payload := range payloads {
		event, ok, err := parseResponsesImagePayload(payload)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		select {
		case out <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return <-errCh
}

func parseResponsesImagePayload(payload string) (ResponsesImageEvent, bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return ResponsesImageEvent{}, false, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return ResponsesImageEvent{}, false, err
	}
	eventType := util.Clean(data["type"])
	event := ResponsesImageEvent{Type: eventType, Created: time.Now().Unix(), Raw: data}
	switch eventType {
	case "response.image_generation_call.partial_image":
		event.PartialImage = util.Clean(data["partial_image_b64"])
		event.PartialImageIndex = util.ToInt(data["partial_image_index"], 0)
		event.OutputFormat = util.Clean(data["output_format"])
		event.Background = util.Clean(data["background"])
		return event, event.PartialImage != "", nil
	case "response.output_item.done":
		item := util.StringMap(data["item"])
		if util.Clean(item["type"]) != "image_generation_call" {
			return event, false, nil
		}
		mergeResponsesImageItem(&event, item)
		return event, event.Result != "", nil
	case "response.completed":
		response := util.StringMap(data["response"])
		for _, raw := range anySlice(response["output"]) {
			item, ok := raw.(map[string]any)
			if !ok || util.Clean(item["type"]) != "image_generation_call" {
				continue
			}
			mergeResponsesImageItem(&event, item)
			return event, event.Result != "", nil
		}
		return event, false, nil
	case "error":
		message := util.Clean(data["message"])
		if message == "" {
			message = util.Clean(util.StringMap(data["error"])["message"])
		}
		if message == "" {
			message = "codex responses image route returned an error"
		}
		return event, false, fmt.Errorf("%s", message)
	default:
		return event, false, nil
	}
}

func mergeResponsesImageItem(event *ResponsesImageEvent, item map[string]any) {
	event.ItemID = util.Clean(item["id"])
	event.Result = firstNonEmpty(util.Clean(item["result"]), util.Clean(item["b64_json"]))
	event.RevisedPrompt = util.Clean(item["revised_prompt"])
	event.OutputFormat = util.Clean(item["output_format"])
	event.Background = util.Clean(item["background"])
	event.Size = util.Clean(item["size"])
	event.Quality = util.Clean(item["quality"])
	event.Model = util.Clean(item["model"])
}

func anySlice(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

func (c *Client) chatGPTAccountID() string {
	if c == nil || c.lookup == nil || strings.TrimSpace(c.AccessToken) == "" {
		return ""
	}
	account := c.lookup.GetAccount(c.AccessToken)
	return firstNonEmpty(
		util.Clean(account["chatgpt_account_id"]),
		util.Clean(account["account_id"]),
		util.Clean(account["user_id"]),
	)
}
