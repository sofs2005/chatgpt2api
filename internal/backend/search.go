package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"chatgpt2api/internal/util"
)

const (
	searchModel            = "gpt-5-5"
	searchTimeoutSecs      = 300 * time.Second
	searchPollIntervalSecs = 3 * time.Second
)

var (
	searchDoneStatus = map[string]bool{
		"finished_successfully":       true,
		"finished_partial_completion": true,
	}
	searchRetryStatus = map[int]bool{
		http.StatusNotFound:            true,
		http.StatusConflict:            true,
		http.StatusLocked:              true,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
		http.StatusGatewayTimeout:      true,
	}
	searchConversationIDRe = regexp.MustCompile(`"conversation_id"\s*:\s*"([^"]+)"`)
	searchURLRe            = regexp.MustCompile(`https?://[^\s"'<>）)\]}]+`)
)

// SearchSource 表示一条 web 搜索来源。
type SearchSource struct {
	Title      string
	URL        string
	Snippet    string
	SourceType string
}

// SearchResult 表示一次 web 搜索的最终结果。
type SearchResult struct {
	ConversationID     string
	Status             string
	Answer             string
	Sources            []SearchSource
	AssistantMessageID string
	CreateTime         float64
}

// SetSearchPollOptions 配置 web 搜索结果轮询，主要用于测试或调优等待策略。
func (c *Client) SetSearchPollOptions(timeout, pollInterval time.Duration) {
	if timeout < 0 {
		timeout = 0
	}
	if pollInterval < 0 {
		pollInterval = 0
	}
	c.searchTimeout = timeout
	c.searchPollInterval = pollInterval
}

// Search 通过 ChatGPT 官方会话接口执行一次强制 web 搜索。
func (c *Client) Search(ctx context.Context, query string) (SearchResult, error) {
	if strings.TrimSpace(c.AccessToken) == "" {
		return SearchResult{}, fmt.Errorf("access_token is required for search")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchResult{}, fmt.Errorf("search query is required")
	}
	timeout := c.searchTimeout
	if timeout == 0 {
		timeout = searchTimeoutSecs
	}
	pollInterval := c.searchPollInterval
	if pollInterval == 0 {
		pollInterval = searchPollIntervalSecs
	}
	conduitToken, err := c.prepareSearchConversation(ctx, query, searchModel)
	if err != nil {
		return SearchResult{}, err
	}
	if err := c.bootstrap(ctx); err != nil {
		return SearchResult{}, err
	}
	conversationID, err := c.runSearchConversation(ctx, query, conduitToken, searchModel)
	if err != nil {
		return SearchResult{}, err
	}
	return c.waitSearchResult(ctx, conversationID, timeout, pollInterval)
}

func (c *Client) prepareSearchConversation(ctx context.Context, query, model string) (string, error) {
	payload := map[string]any{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     "client-created-root",
		"model":                 model,
		"client_prepare_state":  "success",
		"timezone_offset_min":   -480,
		"timezone":              "Asia/Shanghai",
		"conversation_mode":     map[string]any{"kind": "primary_assistant"},
		"system_hints":          []any{"search"},
		"partial_query": map[string]any{
			"id":      util.NewUUID(),
			"author":  map[string]any{"role": "user"},
			"content": map[string]any{"content_type": "text", "parts": []any{query}},
		},
		"supports_buffering":  true,
		"supported_encodings": []any{"v1"},
		"client_contextual_info": map[string]any{
			"app_name": "chatgpt.com",
		},
	}
	headers := c.headers(officialPreparePath, map[string]string{
		"Accept":          "*/*",
		"Content-Type":    "application/json",
		"X-Conduit-Token": "no-token",
	})
	resp, err := c.postJSON(ctx, officialPreparePath, payload, headers, false)
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
	token := util.Clean(data["conduit_token"])
	if token == "" {
		return "", fmt.Errorf("missing conduit_token")
	}
	return token, nil
}

func (c *Client) runSearchConversation(ctx context.Context, query, conduitToken, model string) (string, error) {
	reqs, err := c.getChatRequirements(ctx)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"action": "next",
		"messages": []any{
			map[string]any{
				"id":          util.NewUUID(),
				"author":      map[string]any{"role": "user"},
				"create_time": float64(time.Now().UnixNano()) / 1e9,
				"content": map[string]any{
					"content_type": "text",
					"parts":        []any{query},
				},
				"metadata": map[string]any{
					"developer_mode_connector_ids": []any{},
					"selected_github_repos":        []any{},
					"selected_all_github_repos":    false,
					"system_hints":                 []any{"search"},
					"serialization_metadata":       map[string]any{"custom_symbol_offsets": []any{}},
				},
			},
		},
		"parent_message_id":                    "client-created-root",
		"model":                                model,
		"client_prepare_state":                 "success",
		"timezone_offset_min":                  -480,
		"timezone":                             "Asia/Shanghai",
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []any{},
		"supports_buffering":                   true,
		"supported_encodings":                  []any{"v1"},
		"force_use_search":                     true,
		"client_reported_search_source":        "conversation_composer_web_icon",
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"client_contextual_info": map[string]any{
			"is_dark_mode":      false,
			"time_since_loaded": 36,
			"page_height":       925,
			"page_width":        886,
			"pixel_ratio":       2,
			"screen_height":     1440,
			"screen_width":      2560,
			"app_name":          "chatgpt.com",
		},
	}
	resp, err := c.postJSON(ctx, officialStreamPath, payload, c.officialHeaders(officialStreamPath, reqs, conduitToken, "text/event-stream"), true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, officialStreamPath); err != nil {
		return "", err
	}
	out := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		errCh <- iterSSEPayloads(ctx, resp.Body, out)
	}()
	conversationID := ""
	for payload := range out {
		if payload == "[DONE]" {
			break
		}
		if conversationID == "" {
			conversationID = findSearchValue(payload, "conversation_id")
		}
	}
	if err := <-errCh; err != nil {
		return "", err
	}
	if conversationID == "" {
		return "", fmt.Errorf("conversation_id not found in stream")
	}
	return conversationID, nil
}

func (c *Client) waitSearchResult(ctx context.Context, conversationID string, timeout, pollInterval time.Duration) (SearchResult, error) {
	deadline := time.Now().Add(timeout)
	var lastResult *SearchResult
	lastAnswer := ""
	stableHits := 0
	for time.Now().Before(deadline) || timeout == 0 {
		conversation, status, err := c.getSearchConversation(ctx, conversationID)
		if err != nil {
			if !searchRetryStatus[status] {
				return SearchResult{}, err
			}
		} else {
			result := extractSearchResult(conversationID, conversation)
			lastResult = &result
			if result.Answer != "" {
				if searchDoneStatus[result.Status] {
					return result, nil
				}
				if result.Answer == lastAnswer {
					stableHits++
				} else {
					stableHits = 0
				}
				lastAnswer = result.Answer
				if stableHits >= 2 {
					return result, nil
				}
			}
		}
		if timeout == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return SearchResult{}, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	if lastResult != nil {
		return *lastResult, nil
	}
	return SearchResult{}, fmt.Errorf("timed out waiting for search result: %s", conversationID)
}

func (c *Client) getSearchConversation(ctx context.Context, conversationID string) (map[string]any, int, error) {
	path := "/backend-api/conversation/" + conversationID
	headers := c.headers(path, map[string]string{"Accept": "*/*"})
	headers["Referer"] = c.BaseURL + "/c/" + conversationID
	headers["X-OpenAI-Target-Route"] = "/backend-api/conversation/{conversation_id}"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, 0, upstreamTransportError(path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, upstreamHTTPError(path, resp.StatusCode, data)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, resp.StatusCode, err
	}
	return payload, resp.StatusCode, nil
}

func extractSearchResult(conversationID string, conversation map[string]any) SearchResult {
	message := latestSearchAssistantMessage(conversation)
	metadata := mapValue(message["metadata"])
	finishDetails := mapValue(metadata["finish_details"])
	answer := searchMessageText(message)
	sources := extractSearchSources(message)
	for _, rawURL := range searchURLRe.FindAllString(answer, -1) {
		url := cleanSearchURL(rawURL)
		if url != "" && !hasSearchSourceURL(sources, url) {
			sources = append(sources, SearchSource{URL: url})
		}
	}
	return SearchResult{
		ConversationID:     conversationID,
		Status:             strings.TrimSpace(firstNonEmpty(util.Clean(finishDetails["type"]), util.Clean(metadata["status"]), findSearchValue(message, "status"))),
		Answer:             answer,
		Sources:            sources,
		AssistantMessageID: util.Clean(message["id"]),
		CreateTime:         floatValue(message["create_time"]),
	}
}

func latestSearchAssistantMessage(conversation map[string]any) map[string]any {
	mapping := mapValue(conversation["mapping"])
	var latest map[string]any
	latestTime := -1.0
	for _, rawNode := range mapping {
		node := mapValue(rawNode)
		message := mapValue(node["message"])
		author := mapValue(message["author"])
		if util.Clean(author["role"]) != "assistant" {
			continue
		}
		created := floatValue(message["create_time"])
		if latest == nil || created > latestTime {
			latest = message
			latestTime = created
		}
	}
	if latest == nil {
		return map[string]any{}
	}
	return latest
}

func searchMessageText(message any) string {
	rawContent := mapValue(message)["content"]
	parts := make([]string, 0)
	if text, ok := rawContent.(string); ok {
		parts = append(parts, text)
	}
	content := mapValue(rawContent)
	if text, ok := content["text"].(string); ok {
		parts = append(parts, text)
	}
	if rawParts, ok := content["parts"].([]any); ok {
		for _, rawPart := range rawParts {
			switch part := rawPart.(type) {
			case string:
				parts = append(parts, part)
			case map[string]any:
				for _, key := range []string{"text", "summary", "content"} {
					if value := util.Clean(part[key]); value != "" {
						parts = append(parts, value)
					}
				}
			}
		}
	}
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part); text != "" {
			clean = append(clean, text)
		}
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}

func extractSearchSources(payload any) []SearchSource {
	sources := make([]SearchSource, 0)
	for _, obj := range walkSearchDicts(payload) {
		metadata := mapValue(obj["metadata"])
		url := cleanSearchURL(firstNonEmpty(util.Clean(obj["url"]), util.Clean(obj["link"]), util.Clean(obj["source_url"]), util.Clean(metadata["url"])))
		if url == "" || hasSearchSourceURL(sources, url) {
			continue
		}
		sources = append(sources, SearchSource{
			Title:      strings.TrimSpace(firstNonEmpty(util.Clean(obj["title"]), util.Clean(obj["name"]), util.Clean(obj["source"]))),
			URL:        url,
			Snippet:    strings.TrimSpace(firstNonEmpty(util.Clean(obj["snippet"]), util.Clean(obj["text"]), util.Clean(obj["description"]))),
			SourceType: strings.TrimSpace(firstNonEmpty(util.Clean(obj["type"]), util.Clean(obj["source_type"]))),
		})
	}
	return sources
}

func findSearchValue(payload any, key string) string {
	switch value := payload.(type) {
	case string:
		if key == "conversation_id" {
			if match := searchConversationIDRe.FindStringSubmatch(value); len(match) > 1 {
				return match[1]
			}
		}
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) != nil {
			return ""
		}
		return findSearchValue(decoded, key)
	case map[string]any:
		if found := util.Clean(value[key]); found != "" {
			return found
		}
		for _, item := range value {
			if found := findSearchValue(item, key); found != "" {
				return found
			}
		}
	case []any:
		for _, item := range value {
			if found := findSearchValue(item, key); found != "" {
				return found
			}
		}
	}
	return ""
}

func walkSearchDicts(payload any) []map[string]any {
	items := make([]map[string]any, 0)
	switch value := payload.(type) {
	case map[string]any:
		items = append(items, value)
		for _, item := range value {
			items = append(items, walkSearchDicts(item)...)
		}
	case []any:
		for _, item := range value {
			items = append(items, walkSearchDicts(item)...)
		}
	}
	return items
}

func cleanSearchURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), ".,;，。；")
}

func hasSearchSourceURL(sources []SearchSource, url string) bool {
	for _, source := range sources {
		if source.URL == url {
			return true
		}
	}
	return false
}

func mapValue(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func floatValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}
