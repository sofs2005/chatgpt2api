package protocol

import (
	"regexp"
	"strings"
	"sync"

	"chatgpt2api/internal/util"
)

const (
	maxStoredResponseContexts = 32
	maxContextMessages        = 16
	maxContextImages          = 4
	maxContextMessageChars    = 2000
)

type ResponseContext struct {
	Messages []map[string]any
	Images   []string
}

type ResponseContextStore struct {
	mu    sync.RWMutex
	max   int
	order []string
	items map[string]ResponseContext
}

func NewResponseContextStore(max int) *ResponseContextStore {
	if max < 1 {
		max = maxStoredResponseContexts
	}
	return &ResponseContextStore{max: max, items: map[string]ResponseContext{}}
}

func (s *ResponseContextStore) Get(id string) (ResponseContext, bool) {
	return s.GetScoped("", id)
}

func (s *ResponseContextStore) GetScoped(scope, id string) (ResponseContext, bool) {
	if s == nil || strings.TrimSpace(id) == "" {
		return ResponseContext{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctx, ok := s.items[responseContextStoreKey(scope, id)]
	if !ok {
		return ResponseContext{}, false
	}
	return cloneResponseContext(ctx), true
}

func (s *ResponseContextStore) Set(id string, ctx ResponseContext) {
	s.SetScoped("", id, ctx)
}

func (s *ResponseContextStore) SetScoped(scope, id string, ctx ResponseContext) {
	if s == nil || strings.TrimSpace(id) == "" {
		return
	}
	key := responseContextStoreKey(scope, id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]ResponseContext{}
	}
	if s.max < 1 {
		s.max = maxStoredResponseContexts
	}
	if _, exists := s.items[key]; !exists {
		s.order = append(s.order, key)
	}
	s.items[key] = trimResponseContext(ctx)
	for len(s.order) > s.max {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.items, oldest)
	}
}

func (e *Engine) responseContextStore() *ResponseContextStore {
	if e == nil {
		return NewResponseContextStore(maxStoredResponseContexts)
	}
	e.responseContextMu.Lock()
	defer e.responseContextMu.Unlock()
	if e.ResponseContexts == nil {
		e.ResponseContexts = NewResponseContextStore(maxStoredResponseContexts)
	}
	return e.ResponseContexts
}

func (e *Engine) responseContextFromPrevious(raw any) (ResponseContext, error) {
	return e.responseContextFromPreviousScoped("", raw)
}

func (e *Engine) responseContextFromPreviousScoped(scope string, raw any) (ResponseContext, error) {
	id := strings.TrimSpace(util.Clean(raw))
	if id == "" {
		return ResponseContext{}, nil
	}
	ctx, ok := e.responseContextStore().GetScoped(scope, id)
	if !ok {
		return ResponseContext{}, HTTPError{Status: 400, Message: "previous_response_id not found or expired"}
	}
	return ctx, nil
}

func (e *Engine) rememberResponseContextEvents(events <-chan map[string]any, base ResponseContext) <-chan map[string]any {
	return e.rememberResponseContextEventsScoped("", events, base)
}

func (e *Engine) rememberResponseContextEventsScoped(scope string, events <-chan map[string]any, base ResponseContext) <-chan map[string]any {
	out := make(chan map[string]any)
	go func() {
		defer close(out)
		for event := range events {
			if event["type"] == "response.completed" {
				if response := util.StringMap(event["response"]); len(response) > 0 {
					id := util.Clean(response["id"])
					if id != "" {
						e.responseContextStore().SetScoped(scope, id, ResponseContextWithOutput(base, util.AsMapSlice(response["output"])))
					}
				}
			}
			out <- event
		}
	}()
	return out
}

func responseContextStoreKey(scope, id string) string {
	id = strings.TrimSpace(id)
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return id
	}
	return scope + "\x00" + id
}

func ResponseContextWithOutput(base ResponseContext, output []map[string]any) ResponseContext {
	next := cloneResponseContext(base)
	for _, item := range output {
		switch util.Clean(item["type"]) {
		case "message":
			role := firstNonEmpty(util.Clean(item["role"]), "assistant")
			if text := responseContentText(item["content"]); text != "" {
				next.Messages = append(next.Messages, map[string]any{"role": role, "content": text})
			}
		case "image_generation_call":
			if image := util.Clean(item["result"]); image != "" {
				next.Images = append(next.Images, image)
			}
			if revised := util.Clean(item["revised_prompt"]); revised != "" {
				next.Messages = append(next.Messages, map[string]any{"role": "assistant", "content": "Generated image: " + revised})
			}
		}
	}
	return trimResponseContext(next)
}

func MergeResponseContext(previous ResponseContext, messages []map[string]any, images []string) ResponseContext {
	next := cloneResponseContext(previous)
	next.Messages = append(next.Messages, NormalizeMessages(messages, nil)...)
	next.Images = append(next.Images, images...)
	return trimResponseContext(next)
}

func BuildImageContextPrompt(messages []map[string]any, fallbackPrompt, size, quality string) string {
	normalized := NormalizeMessages(messages, nil)
	currentPrompt, currentIndex := latestUserPromptWithIndex(normalized)
	fallbackPrompt = strings.TrimSpace(fallbackPrompt)
	if fallbackPrompt != "" {
		currentPrompt = fallbackPrompt
		if index := findLastUserMessageIndex(normalized, fallbackPrompt); index >= 0 {
			currentIndex = index
		}
	}
	currentPrompt = sanitizeImageContextText(currentPrompt)
	if len(normalized) == 0 || currentPrompt == "" {
		return BuildImagePrompt(currentPrompt, size, quality)
	}

	var history []string
	for index, message := range normalized {
		if index == currentIndex {
			continue
		}
		text := sanitizeImageContextText(util.Clean(message["content"]))
		if text == "" {
			continue
		}
		history = append(history, roleLabel(util.Clean(message["role"]))+": "+text)
	}
	if len(history) == 0 {
		return BuildImagePrompt(currentPrompt, size, quality)
	}
	if len(history) > maxContextMessages {
		history = history[len(history)-maxContextMessages:]
	}
	prompt := "请延续同一个图片生成对话，并把历史上下文用于理解代词、主体、风格、构图和连续修改意图。不要把历史说明文字原样画进画面，除非当前请求明确要求。\n\n历史上下文:\n" +
		strings.Join(history, "\n") +
		"\n\n当前请求:\n" + currentPrompt
	return BuildImagePrompt(prompt, size, quality)
}

func LatestUserPrompt(messages []map[string]any) string {
	prompt, _ := latestUserPromptWithIndex(NormalizeMessages(messages, nil))
	return prompt
}

func latestUserPromptWithIndex(messages []map[string]any) (string, int) {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.ToLower(util.Clean(messages[index]["role"])) != "user" {
			continue
		}
		if text := strings.TrimSpace(util.Clean(messages[index]["content"])); text != "" {
			return text, index
		}
	}
	return "", -1
}

func findLastUserMessageIndex(messages []map[string]any, prompt string) int {
	prompt = strings.TrimSpace(prompt)
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.ToLower(util.Clean(messages[index]["role"])) != "user" {
			continue
		}
		if strings.TrimSpace(util.Clean(messages[index]["content"])) == prompt {
			return index
		}
	}
	return -1
}

func responseContentText(content any) string {
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
		case "text", "input_text", "output_text":
			if text := strings.TrimSpace(util.Clean(item["text"])); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func trimResponseContext(ctx ResponseContext) ResponseContext {
	messages := make([]map[string]any, 0, len(ctx.Messages))
	for _, message := range NormalizeMessages(ctx.Messages, nil) {
		text := sanitizeImageContextText(util.Clean(message["content"]))
		if text == "" {
			continue
		}
		if len([]rune(text)) > maxContextMessageChars {
			runes := []rune(text)
			text = string(runes[:maxContextMessageChars]) + "...(truncated)"
		}
		messages = append(messages, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": text})
	}
	if len(messages) > maxContextMessages {
		messages = messages[len(messages)-maxContextMessages:]
	}

	var images []string
	for _, image := range ctx.Images {
		if image = strings.TrimSpace(image); image != "" {
			images = append(images, image)
		}
	}
	if len(images) > maxContextImages {
		images = images[len(images)-maxContextImages:]
	}
	return ResponseContext{Messages: messages, Images: images}
}

func cloneResponseContext(ctx ResponseContext) ResponseContext {
	return ResponseContext{Messages: cloneMessages(ctx.Messages), Images: append([]string(nil), ctx.Images...)}
}

func cloneMessages(messages []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		out = append(out, util.CopyMap(message))
	}
	return out
}

func sanitizeImageContextText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = regexp.MustCompile(`!\[[^\]]*]\(data:image/[^)]+\)`).ReplaceAllString(text, "[generated image]")
	text = regexp.MustCompile(`data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=\s]+`).ReplaceAllString(text, "[image data]")
	text = regexp.MustCompile(`[A-Za-z0-9+/]{800,}={0,2}`).ReplaceAllString(text, "[long encoded data]")
	return strings.TrimSpace(text)
}

func roleLabel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system":
		return "System"
	case "assistant":
		return "Assistant"
	case "tool":
		return "Tool"
	default:
		return "User"
	}
}
