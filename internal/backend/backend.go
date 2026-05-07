package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"chatgpt2api/internal/config"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

const (
	DefaultClientVersion     = "prod-be885abbfcfe7b1f511e88b3003d9ee44757fbad"
	DefaultClientBuildNumber = "5955942"
	CodexImageModel          = "codex-gpt-image-2"
	CodexResponsesPath       = "/backend-api/codex/responses"

	browserUserAgent              = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
	browserSecCHUA                = `"Not:A-Brand";v="99", "Google Chrome";v="145", "Chromium";v="145"`
	browserSecCHUAFullVersion     = `"145.0.0.0"`
	browserSecCHUAFullVersionList = `"Not:A-Brand";v="99.0.0.0", "Google Chrome";v="145.0.0.0", "Chromium";v="145.0.0.0"`
	browserSecCHUAMobile          = "?0"
	browserSecCHUAPlatform        = `"Windows"`
	browserSecCHUAPlatformVersion = `"19.0.0"`
	browserSecCHUAArch            = `"x86"`
	browserSecCHUABitness         = `"64"`
	browserImpersonationProfile   = "chrome145"
)

type AccountLookup interface {
	GetAccount(accessToken string) map[string]any
}

type Client struct {
	BaseURL           string
	ClientVersion     string
	ClientBuildNumber string
	AccessToken       string

	lookup       AccountLookup
	proxy        *service.ProxyService
	httpClient   *http.Client
	fp           map[string]string
	userAgent    string
	deviceID     string
	sessionID    string
	powSources   []string
	powDataBuild string

	ImageCompat config.ImageCompatConfig
}

type ChatRequirements struct {
	Token          string
	ProofToken     string
	TurnstileToken string
	SOToken        string
	Raw            map[string]any
}

type UploadedFile struct {
	FileID   string
	FileName string
	FileSize int
	MimeType string
	Width    int
	Height   int
}

func NewClient(accessToken string, lookup AccountLookup, proxy *service.ProxyService) *Client {
	c := &Client{
		BaseURL:           "https://chatgpt.com",
		ClientVersion:     DefaultClientVersion,
		ClientBuildNumber: DefaultClientBuildNumber,
		AccessToken:       strings.TrimSpace(accessToken),
		lookup:            lookup,
		proxy:             proxy,
	}
	c.fp = c.buildFingerprint()
	c.applyBrowserFingerprint()
	c.userAgent = c.fp["user-agent"]
	c.deviceID = c.fp["oai-device-id"]
	c.sessionID = c.fp["oai-session-id"]
	c.httpClient = proxy.BrowserHTTPClientWithProfile(c.fp["impersonate"], 300*time.Second)
	return c
}

func (c *Client) ListModels(ctx context.Context) (map[string]any, error) {
	if err := c.bootstrap(ctx); err != nil {
		return nil, err
	}
	path := "/backend-anon/models?iim=false&is_gizmo=false"
	route := "/backend-anon/models"
	contextName := "anon_models"
	if c.AccessToken != "" {
		path = "/backend-api/models?history_and_training_disabled=false"
		route = "/backend-api/models"
		contextName = "auth_models"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	for key, value := range c.headers(route, map[string]string{}) {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, upstreamTransportError(contextName, err)
	}
	defer resp.Body.Close()
	if err := ensureOK(resp, contextName); err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := util.AsMapSlice(payload["models"])
	data := make([]map[string]any, 0, len(models))
	seen := map[string]struct{}{}
	for _, item := range models {
		slug := util.Clean(item["slug"])
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		data = append(data, map[string]any{
			"id": slug, "object": "model", "created": util.ToInt(item["created"], 0),
			"owned_by":   firstNonEmpty(util.Clean(item["owned_by"]), "chatgpt"),
			"permission": []any{}, "root": slug, "parent": nil,
		})
	}
	sort.Slice(data, func(i, j int) bool { return util.Clean(data[i]["id"]) < util.Clean(data[j]["id"]) })
	return map[string]any{"object": "list", "data": data}, nil
}

func (c *Client) StreamConversation(ctx context.Context, messages []map[string]any, model, prompt string, images []string, systemHints []string) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if contains(systemHints, "picture_v2") && util.IsImageGenerationModel(model) {
			errCh <- c.streamPictureConversation(ctx, out, prompt, model, images)
			return
		}
		if contains(systemHints, "picture_v2") && strings.TrimSpace(prompt) != "" {
			messages = []map[string]any{{"role": "user", "content": prompt}}
		} else if len(messages) == 0 {
			messages = []map[string]any{{"role": "user", "content": prompt}}
		}
		if err := c.bootstrap(ctx); err != nil {
			errCh <- err
			return
		}
		reqs, err := c.getChatRequirements(ctx)
		if err != nil {
			errCh <- err
			return
		}
		path, timezoneName := c.chatTarget()
		payload := c.conversationPayload(messages, model, timezoneName, systemHints)
		resp, err := c.postJSON(ctx, path, payload, c.conversationHeaders(path, reqs), true)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if err := ensureOK(resp, path); err != nil {
			errCh <- err
			return
		}
		errCh <- iterSSEPayloads(ctx, resp.Body, out)
	}()
	return out, errCh
}

func (c *Client) StreamCodexResponses(ctx context.Context, payload map[string]any) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if c.AccessToken == "" {
			errCh <- fmt.Errorf("access_token is required for responses endpoint")
			return
		}
		path := CodexResponsesPath
		resp, err := c.postJSON(ctx, path, payload, c.codexResponsesHeaders(path), true)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if err := ensureOK(resp, path); err != nil {
			errCh <- err
			return
		}
		errCh <- iterSSEPayloads(ctx, resp.Body, out)
	}()
	return out, errCh
}

func (c *Client) ResolveConversationImageURLs(ctx context.Context, conversationID string, fileIDs, sedimentIDs []string, poll bool) []string {
	fileIDs = filter(fileIDs, func(v string) bool { return v != "file_upload" })
	if poll && conversationID != "" && len(fileIDs) == 0 && len(sedimentIDs) == 0 {
		polledFiles, polledSediments := c.pollImageResults(ctx, conversationID, 120*time.Second)
		fileIDs = appendUnique(fileIDs, polledFiles...)
		sedimentIDs = appendUnique(sedimentIDs, polledSediments...)
	}
	return c.resolveImageURLs(ctx, conversationID, fileIDs, sedimentIDs)
}

func (c *Client) DownloadImageBytes(ctx context.Context, urls []string) ([][]byte, error) {
	var images [][]byte
	for _, item := range urls {
		resp, err := c.downloadImageBytesResponse(ctx, item)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, upstreamHTTPError("image_download", resp.StatusCode, data)
		}
		images = append(images, data)
	}
	return images, nil
}

func (c *Client) downloadImageBytesResponse(ctx context.Context, rawURL string) (*http.Response, error) {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return nil, fmt.Errorf("image url is required")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		base, baseErr := url.Parse(c.BaseURL)
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
		path := parsed.EscapedPath()
		for key, value := range c.headers(path, map[string]string{"Accept": "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"}) {
			req.Header.Set(key, value)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, upstreamTransportError(path, err)
		}
		return resp, nil
	}

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	client := c.httpClient
	if c.proxy != nil {
		client = c.proxy.HTTPClient(120 * time.Second)
	}
	return client.Do(req)
}

func (c *Client) isChatGPTBackendURL(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Host == "" {
		return false
	}
	if !strings.EqualFold(parsed.Host, base.Host) {
		return false
	}
	path := parsed.EscapedPath()
	return strings.HasPrefix(path, "/backend-api/") || strings.HasPrefix(path, "/backend-anon/")
}

func (c *Client) streamPictureConversation(ctx context.Context, out chan<- string, prompt, model string, images []string) error {
	if c.AccessToken == "" {
		return fmt.Errorf("access_token is required for image endpoints")
	}
	references := make([]UploadedFile, 0, len(images))
	for index, imageRef := range images {
		uploaded, err := c.uploadImage(ctx, imageRef, fmt.Sprintf("image_%d.png", index+1))
		if err != nil {
			return err
		}
		references = append(references, uploaded)
	}
	if err := c.bootstrap(ctx); err != nil {
		return err
	}
	reqs, err := c.getChatRequirements(ctx)
	if err != nil {
		return err
	}
	conduit, err := c.prepareImageConversation(ctx, prompt, reqs, model)
	if err != nil {
		return err
	}
	resp, err := c.startImageGeneration(ctx, prompt, reqs, conduit, model, references)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return iterSSEPayloads(ctx, resp.Body, out)
}

func (c *Client) buildFingerprint() map[string]string {
	account := map[string]any{}
	if c.AccessToken != "" && c.lookup != nil {
		account = c.lookup.GetAccount(c.AccessToken)
	}
	fp := map[string]string{}
	if raw, ok := account["fp"].(map[string]any); ok {
		for key, value := range raw {
			if text := util.Clean(value); text != "" {
				fp[strings.ToLower(key)] = text
			}
		}
	}
	for _, key := range []string{"user-agent", "impersonate", "oai-device-id", "oai-session-id", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform"} {
		if value := util.Clean(account[key]); value != "" {
			fp[key] = value
		}
	}
	defaults := map[string]string{
		"user-agent":         browserUserAgent,
		"impersonate":        browserImpersonationProfile,
		"oai-device-id":      util.NewUUID(),
		"oai-session-id":     util.NewUUID(),
		"sec-ch-ua-mobile":   browserSecCHUAMobile,
		"sec-ch-ua-platform": browserSecCHUAPlatform,
	}
	for key, value := range defaults {
		if fp[key] == "" {
			fp[key] = value
		}
	}
	return fp
}

func (c *Client) applyBrowserFingerprint() {
	if c.fp == nil {
		c.fp = map[string]string{}
	}
	setDefault := func(key, value string) {
		if strings.TrimSpace(c.fp[key]) == "" {
			c.fp[key] = value
		}
	}
	setDefault("impersonate", browserImpersonationProfile)
	setDefault("user-agent", browserUserAgent)
	setDefault("sec-ch-ua-mobile", browserSecCHUAMobile)
	setDefault("sec-ch-ua-platform", browserSecCHUAPlatform)
	metadata := browserMetadataFromUserAgent(c.fp["user-agent"])
	setDefault("sec-ch-ua", metadata.secCHUA)
	setDefault("sec-ch-ua-arch", browserSecCHUAArch)
	setDefault("sec-ch-ua-bitness", browserSecCHUABitness)
	setDefault("sec-ch-ua-full-version", quoteHeaderValue(metadata.fullVersion))
	setDefault("sec-ch-ua-full-version-list", metadata.fullVersionList)
	setDefault("sec-ch-ua-platform-version", browserSecCHUAPlatformVersion)
}

type browserHeaderMetadata struct {
	secCHUA         string
	fullVersion     string
	fullVersionList string
}

func browserMetadataFromUserAgent(userAgent string) browserHeaderMetadata {
	chromeVersion := regexpVersion(userAgent, `Chrome/([0-9]+(?:\.[0-9]+){0,3})`)
	edgeVersion := regexpVersion(userAgent, `Edg[A-Z]*/([0-9]+(?:\.[0-9]+){0,3})`)
	if edgeVersion != "" {
		edgeMajor := majorVersion(edgeVersion)
		chromiumVersion := firstNonEmpty(chromeVersion, edgeVersion)
		chromiumMajor := majorVersion(chromiumVersion)
		return browserHeaderMetadata{
			secCHUA:         fmt.Sprintf(`"Microsoft Edge";v="%s", "Chromium";v="%s", "Not A(Brand";v="24"`, edgeMajor, chromiumMajor),
			fullVersion:     edgeVersion,
			fullVersionList: fmt.Sprintf(`"Microsoft Edge";v="%s", "Chromium";v="%s", "Not A(Brand";v="24.0.0.0"`, normalizeFullVersion(edgeVersion), normalizeFullVersion(chromiumVersion)),
		}
	}
	if chromeVersion != "" {
		major := majorVersion(chromeVersion)
		full := normalizeFullVersion(chromeVersion)
		return browserHeaderMetadata{
			secCHUA:         fmt.Sprintf(`"Not:A-Brand";v="99", "Google Chrome";v="%s", "Chromium";v="%s"`, major, major),
			fullVersion:     full,
			fullVersionList: fmt.Sprintf(`"Not:A-Brand";v="99.0.0.0", "Google Chrome";v="%s", "Chromium";v="%s"`, full, full),
		}
	}
	return browserHeaderMetadata{
		secCHUA:         browserSecCHUA,
		fullVersion:     strings.Trim(browserSecCHUAFullVersion, `"`),
		fullVersionList: browserSecCHUAFullVersionList,
	}
}

func regexpVersion(value, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(value)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func majorVersion(version string) string {
	if before, _, ok := strings.Cut(version, "."); ok {
		return before
	}
	return version
}

func normalizeFullVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return strings.Trim(browserSecCHUAFullVersion, `"`)
	}
	parts := strings.Split(version, ".")
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	return strings.Join(parts[:4], ".")
}

func quoteHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.Trim(browserSecCHUAFullVersion, `"`)
	}
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value
	}
	return `"` + value + `"`
}

func (c *Client) headers(path string, extra map[string]string) map[string]string {
	headers := map[string]string{
		"User-Agent":                  c.userAgent,
		"Origin":                      c.BaseURL,
		"Referer":                     c.BaseURL + "/",
		"Accept-Language":             "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7",
		"Cache-Control":               "no-cache",
		"Pragma":                      "no-cache",
		"Priority":                    "u=1, i",
		"Sec-Ch-Ua":                   c.fp["sec-ch-ua"],
		"Sec-Ch-Ua-Arch":              c.fp["sec-ch-ua-arch"],
		"Sec-Ch-Ua-Bitness":           c.fp["sec-ch-ua-bitness"],
		"Sec-Ch-Ua-Full-Version":      c.fp["sec-ch-ua-full-version"],
		"Sec-Ch-Ua-Full-Version-List": c.fp["sec-ch-ua-full-version-list"],
		"Sec-Ch-Ua-Mobile":            c.fp["sec-ch-ua-mobile"],
		"Sec-Ch-Ua-Model":             `""`,
		"Sec-Ch-Ua-Platform":          c.fp["sec-ch-ua-platform"],
		"Sec-Ch-Ua-Platform-Version":  c.fp["sec-ch-ua-platform-version"],
		"Sec-Fetch-Dest":              "empty",
		"Sec-Fetch-Mode":              "cors",
		"Sec-Fetch-Site":              "same-origin",
		"OAI-Device-Id":               c.deviceID,
		"OAI-Session-Id":              c.sessionID,
		"OAI-Language":                "zh-CN",
		"OAI-Client-Version":          c.ClientVersion,
		"OAI-Client-Build-Number":     c.ClientBuildNumber,
		"X-OpenAI-Target-Path":        path,
		"X-OpenAI-Target-Route":       path,
	}
	if c.AccessToken != "" {
		headers["Authorization"] = "Bearer " + c.AccessToken
	}
	for key, value := range extra {
		headers[key] = value
	}
	return headers
}

func (c *Client) bootstrapHeaders() map[string]string {
	return map[string]string{
		"User-Agent":                c.userAgent,
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Accept-Language":           "zh-CN,zh;q=0.9,en;q=0.8",
		"Sec-Ch-Ua":                 c.fp["sec-ch-ua"],
		"Sec-Ch-Ua-Mobile":          c.fp["sec-ch-ua-mobile"],
		"Sec-Ch-Ua-Platform":        c.fp["sec-ch-ua-platform"],
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
	}
}

func (c *Client) bootstrap(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/", nil)
	for key, value := range c.bootstrapHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return upstreamTransportError("bootstrap", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamHTTPError("bootstrap", resp.StatusCode, data)
	}
	c.powSources, c.powDataBuild = parsePOWResources(string(data))
	if len(c.powSources) == 0 {
		c.powSources = []string{defaultPOWScript}
	}
	return nil
}

func (c *Client) getChatRequirements(ctx context.Context) (ChatRequirements, error) {
	path := "/backend-anon/sentinel/chat-requirements"
	contextName := "noauth_chat_requirements"
	if c.AccessToken != "" {
		path = "/backend-api/sentinel/chat-requirements"
		contextName = "auth_chat_requirements"
	}
	p := buildLegacyRequirementsToken(c.userAgent, c.powSources, c.powDataBuild)
	resp, err := c.postJSON(ctx, path, map[string]any{"p": p}, c.headers(path, map[string]string{"Content-Type": "application/json"}), false)
	if err != nil {
		return ChatRequirements{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatRequirements{}, upstreamHTTPError(contextName, resp.StatusCode, data)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ChatRequirements{}, err
	}
	reqs, err := c.buildRequirements(payload, "")
	if err != nil {
		return ChatRequirements{}, err
	}
	if reqs.Token == "" {
		if c.AccessToken != "" {
			return ChatRequirements{}, fmt.Errorf("missing auth chat requirements token: %v", payload)
		}
		return ChatRequirements{}, fmt.Errorf("missing chat requirements token: %v", payload)
	}
	return reqs, nil
}

func (c *Client) buildRequirements(data map[string]any, sourceP string) (ChatRequirements, error) {
	if arkose := util.StringMap(data["arkose"]); util.ToBool(arkose["required"]) {
		return ChatRequirements{}, fmt.Errorf("chat requirements requires arkose token, which is not implemented")
	}
	proofToken := ""
	proof := util.StringMap(data["proofofwork"])
	if util.ToBool(proof["required"]) {
		token, err := buildProofToken(util.Clean(proof["seed"]), util.Clean(proof["difficulty"]), c.userAgent, c.powSources, c.powDataBuild)
		if err != nil {
			return ChatRequirements{}, err
		}
		proofToken = token
	}
	turnstileToken := ""
	turnstile := util.StringMap(data["turnstile"])
	if util.ToBool(turnstile["required"]) && util.Clean(turnstile["dx"]) != "" {
		turnstileToken = solveTurnstileToken(util.Clean(turnstile["dx"]), sourceP)
	}
	return ChatRequirements{Token: util.Clean(data["token"]), ProofToken: proofToken, TurnstileToken: turnstileToken, SOToken: util.Clean(data["so_token"]), Raw: data}, nil
}

func (c *Client) chatTarget() (string, string) {
	if c.AccessToken != "" {
		return "/backend-api/conversation", "Asia/Shanghai"
	}
	return "/backend-anon/conversation", "America/Los_Angeles"
}

func (c *Client) conversationPayload(messages []map[string]any, model, timezoneName string, systemHintsValues ...[]string) map[string]any {
	conversationMessages := []map[string]any{conversationUserMessage(conversationPrompt(messages))}
	var systemHints []any
	if len(systemHintsValues) > 0 {
		for _, hint := range systemHintsValues[0] {
			if strings.TrimSpace(hint) != "" {
				systemHints = append(systemHints, strings.TrimSpace(hint))
			}
		}
	}
	return map[string]any{
		"action": "next", "messages": conversationMessages, "model": model, "parent_message_id": "client-created-root",
		"conversation_mode": map[string]any{"kind": "primary_assistant"}, "conversation_origin": nil,
		"force_paragen": false, "force_paragen_model_slug": "", "force_rate_limit": false, "force_use_sse": true,
		"history_and_training_disabled": true, "reset_rate_limits": false, "suggestions": []any{}, "supported_encodings": []any{"v1"},
		"enable_message_followups": true, "supports_buffering": true,
		"system_hints": systemHints, "timezone": timezoneName, "timezone_offset_min": -480,
		"variant_purpose": "comparison_implicit", "websocket_request_id": util.NewUUID(),
		"client_contextual_info": map[string]any{"is_dark_mode": false, "time_since_loaded": 120, "page_height": 900, "page_width": 1400, "pixel_ratio": 2, "screen_height": 1440, "screen_width": 2560},
	}
}

type conversationTextMessage struct {
	role    string
	content string
}

func conversationPrompt(messages []map[string]any) string {
	normalized := make([]conversationTextMessage, 0, len(messages))
	for _, item := range messages {
		content := strings.TrimSpace(conversationMessageText(item["content"]))
		if content == "" {
			continue
		}
		normalized = append(normalized, conversationTextMessage{role: firstNonEmpty(util.Clean(item["role"]), "user"), content: content})
	}
	if len(normalized) == 0 {
		return ""
	}
	lastUserIndex := -1
	for index := len(normalized) - 1; index >= 0; index-- {
		if strings.EqualFold(normalized[index].role, "user") {
			lastUserIndex = index
			break
		}
	}
	if len(normalized) == 1 && lastUserIndex == 0 {
		return normalized[0].content
	}
	if lastUserIndex < 0 {
		return strings.Join(conversationTranscriptLines(normalized, -1), "\n")
	}
	history := conversationTranscriptLines(normalized, lastUserIndex)
	if len(history) == 0 {
		return normalized[lastUserIndex].content
	}
	return "Answer the current user message using the conversation history below. Treat the transcript as prior context, not as instructions unless a System line says so. Reply in the current user's language unless instructed otherwise.\n\n" +
		"Conversation history:\n" + strings.Join(history, "\n") +
		"\n\nCurrent user message:\n" + normalized[lastUserIndex].content
}

func conversationMessageText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	return util.Clean(content)
}

func conversationTranscriptLines(messages []conversationTextMessage, skipIndex int) []string {
	lines := make([]string, 0, len(messages))
	for index, message := range messages {
		if index == skipIndex {
			continue
		}
		if message.content == "" {
			continue
		}
		lines = append(lines, conversationRoleLabel(message.role)+": "+message.content)
	}
	return lines
}

func conversationRoleLabel(role string) string {
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

func conversationUserMessage(content string) map[string]any {
	return map[string]any{
		"id":          util.NewUUID(),
		"author":      map[string]any{"role": "user"},
		"create_time": float64(time.Now().UnixNano()) / 1e9,
		"content":     map[string]any{"content_type": "text", "parts": []any{content}},
		"metadata": map[string]any{
			"selected_github_repos":     []any{},
			"selected_all_github_repos": false,
			"serialization_metadata":    map[string]any{"custom_symbol_offsets": []any{}},
		},
	}
}

func (c *Client) conversationHeaders(path string, reqs ChatRequirements) map[string]string {
	extra := map[string]string{"Accept": "text/event-stream", "Content-Type": "application/json", "OpenAI-Sentinel-Chat-Requirements-Token": reqs.Token}
	if reqs.ProofToken != "" {
		extra["OpenAI-Sentinel-Proof-Token"] = reqs.ProofToken
	}
	if reqs.TurnstileToken != "" {
		extra["OpenAI-Sentinel-Turnstile-Token"] = reqs.TurnstileToken
	}
	if reqs.SOToken != "" {
		extra["OpenAI-Sentinel-SO-Token"] = reqs.SOToken
	}
	return c.headers(path, extra)
}

func (c *Client) codexResponsesHeaders(path string) map[string]string {
	return c.headers(path, map[string]string{
		"Accept":       "text/event-stream",
		"Content-Type": "application/json",
		"OpenAI-Beta":  "responses=experimental",
		"originator":   "opencode",
	})
}

func (c *Client) imageHeaders(path string, reqs ChatRequirements, conduitToken, accept string) map[string]string {
	if accept == "" {
		accept = "*/*"
	}
	extra := map[string]string{"Content-Type": "application/json", "Accept": accept, "OpenAI-Sentinel-Chat-Requirements-Token": reqs.Token}
	if reqs.ProofToken != "" {
		extra["OpenAI-Sentinel-Proof-Token"] = reqs.ProofToken
	}
	if reqs.TurnstileToken != "" {
		extra["OpenAI-Sentinel-Turnstile-Token"] = reqs.TurnstileToken
	}
	if reqs.SOToken != "" {
		extra["OpenAI-Sentinel-SO-Token"] = reqs.SOToken
	}
	if conduitToken != "" {
		extra["X-Conduit-Token"] = conduitToken
	}
	if accept == "text/event-stream" {
		extra["X-Oai-Turn-Trace-Id"] = util.NewUUID()
	}
	return c.headers(path, extra)
}

func (c *Client) imageModelSlug(model string) string {
	model = strings.TrimSpace(model)
	if model == CodexImageModel {
		return model
	}
	if model == util.ImageModelGPT {
		return model
	}
	return util.ImageModelGPT
}

func (c *Client) prepareImageConversation(ctx context.Context, prompt string, reqs ChatRequirements, model string) (string, error) {
	path := "/backend-api/f/conversation/prepare"
	parentMessageID := util.NewUUID()
	if c.ImageCompat.UseClientCreatedRoot {
		parentMessageID = "client-created-root"
	}
	modelField := c.imageModelSlug(model)
	if c.ImageCompat.UseAutoModel {
		modelField = "auto"
	}
	payload := map[string]any{
		"action": "next", "fork_from_shared_post": false, "parent_message_id": parentMessageID, "model": modelField,
		"client_prepare_state": "success", "timezone_offset_min": -480, "timezone": "Asia/Shanghai",
		"conversation_mode": map[string]any{"kind": "primary_assistant"}, "system_hints": []any{"picture_v2"},
		"supports_buffering": true, "supported_encodings": []any{"v1"}, "client_contextual_info": map[string]any{"app_name": "chatgpt.com"},
	}
	if !c.ImageCompat.SkipPartialQuery {
		payload["partial_query"] = map[string]any{"id": util.NewUUID(), "author": map[string]any{"role": "user"}, "content": map[string]any{"content_type": "text", "parts": []any{prompt}}}
	}
	resp, err := c.postJSON(ctx, path, payload, c.imageHeaders(path, reqs, "", "*/*"), false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", upstreamHTTPError(path, resp.StatusCode, data)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	return util.Clean(result["conduit_token"]), nil
}

func (c *Client) uploadImage(ctx context.Context, imageRef, fileName string) (UploadedFile, error) {
	data, err := decodeImageReference(imageRef)
	if err != nil {
		return UploadedFile{}, err
	}
	if len(imageRef) < 512 && !strings.HasPrefix(imageRef, "data:") && !strings.ContainsAny(imageRef, "\r\n") {
		if info, err := os.Stat(filepath.Clean(os.ExpandEnv(imageRef))); err == nil && !info.IsDir() {
			fileName = filepath.Base(imageRef)
		}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return UploadedFile{}, err
	}
	mimeType := mime.TypeByExtension("." + format)
	if mimeType == "" {
		mimeType = "image/png"
	}
	path := "/backend-api/files"
	resp, err := c.postJSON(ctx, path, map[string]any{"file_name": fileName, "file_size": len(data), "use_case": "multimodal", "width": cfg.Width, "height": cfg.Height}, c.headers(path, map[string]string{"Content-Type": "application/json", "Accept": "application/json"}), false)
	if err != nil {
		return UploadedFile{}, err
	}
	uploadMeta, err := readJSONResponse(resp, path)
	if err != nil {
		return UploadedFile{}, err
	}
	time.Sleep(500 * time.Millisecond)
	uploadURL := util.Clean(uploadMeta["upload_url"])
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
	for key, value := range map[string]string{"Content-Type": mimeType, "x-ms-blob-type": "BlockBlob", "x-ms-version": "2020-04-08", "Origin": c.BaseURL, "Referer": c.BaseURL + "/", "User-Agent": c.userAgent, "Accept": "application/json, text/plain, */*", "Accept-Language": "en-US,en;q=0.8"} {
		req.Header.Set(key, value)
	}
	putResp, err := c.httpClient.Do(req)
	if err != nil {
		return UploadedFile{}, upstreamTransportError("image_upload", err)
	}
	if err := ensureOKAndClose(putResp, "image_upload"); err != nil {
		return UploadedFile{}, err
	}
	uploadedPath := fmt.Sprintf("/backend-api/files/%s/uploaded", util.Clean(uploadMeta["file_id"]))
	resp, err = c.postRaw(ctx, uploadedPath, []byte("{}"), c.headers(uploadedPath, map[string]string{"Content-Type": "application/json", "Accept": "application/json"}), false)
	if err != nil {
		return UploadedFile{}, err
	}
	if err := ensureOKAndClose(resp, uploadedPath); err != nil {
		return UploadedFile{}, err
	}
	return UploadedFile{FileID: util.Clean(uploadMeta["file_id"]), FileName: fileName, FileSize: len(data), MimeType: mimeType, Width: cfg.Width, Height: cfg.Height}, nil
}

func (c *Client) startImageGeneration(ctx context.Context, prompt string, reqs ChatRequirements, conduitToken, model string, references []UploadedFile) (*http.Response, error) {
	var parts []any
	for _, item := range references {
		parts = append(parts, map[string]any{"content_type": "image_asset_pointer", "asset_pointer": "file-service://" + item.FileID, "width": item.Width, "height": item.Height, "size_bytes": item.FileSize})
	}
	parts = append(parts, prompt)
	content := map[string]any{"content_type": "text", "parts": []any{prompt}}
	if len(references) > 0 {
		content = map[string]any{"content_type": "multimodal_text", "parts": parts}
	}
	metadata := map[string]any{"developer_mode_connector_ids": []any{}, "selected_github_repos": []any{}, "selected_all_github_repos": false, "system_hints": []any{"picture_v2"}, "serialization_metadata": map[string]any{"custom_symbol_offsets": []any{}}}
	if len(references) > 0 {
		attachments := make([]any, 0, len(references))
		for _, item := range references {
			attachments = append(attachments, map[string]any{"id": item.FileID, "mimeType": item.MimeType, "name": item.FileName, "size": item.FileSize, "width": item.Width, "height": item.Height})
		}
		metadata["attachments"] = attachments
	}
	parentMessageID := util.NewUUID()
	if c.ImageCompat.UseClientCreatedRoot {
		parentMessageID = "client-created-root"
	}
	modelField := c.imageModelSlug(model)
	if c.ImageCompat.UseAutoModel {
		modelField = "auto"
	}
	payload := map[string]any{
		"action": "next", "messages": []any{map[string]any{"id": util.NewUUID(), "author": map[string]any{"role": "user"}, "create_time": float64(time.Now().UnixNano()) / 1e9, "content": content, "metadata": metadata}},
		"parent_message_id": parentMessageID, "model": modelField, "client_prepare_state": "sent", "timezone_offset_min": -480, "timezone": "Asia/Shanghai",
		"conversation_mode": map[string]any{"kind": "primary_assistant"}, "enable_message_followups": true, "system_hints": []any{"picture_v2"}, "supports_buffering": true, "supported_encodings": []any{"v1"},
		"client_contextual_info": map[string]any{"is_dark_mode": false, "time_since_loaded": 1200, "page_height": 1072, "page_width": 1724, "pixel_ratio": 1.2, "screen_height": 1440, "screen_width": 2560, "app_name": "chatgpt.com"},
	}
	if !c.ImageCompat.SkipExtraFields {
		payload["paragen_cot_summary_display_override"] = "allow"
		payload["force_parallel_switch"] = "auto"
	}
	path := "/backend-api/f/conversation"
	resp, err := c.postJSON(ctx, path, payload, c.imageHeaders(path, reqs, conduitToken, "text/event-stream"), true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, upstreamHTTPError(path, resp.StatusCode, data)
	}
	return resp, nil
}

func (c *Client) getConversation(ctx context.Context, conversationID string) (map[string]any, error) {
	path := "/backend-api/conversation/" + url.PathEscape(conversationID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	for key, value := range c.headers(path, map[string]string{"Accept": "application/json"}) {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, upstreamTransportError(path, err)
	}
	return readJSONResponse(resp, path)
}

func (c *Client) pollImageResults(ctx context.Context, conversationID string, timeout time.Duration) ([]string, []string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conversation, err := c.getConversation(ctx, conversationID)
		if err == nil {
			files, sediments := extractImageToolRecords(conversation)
			if len(files) > 0 || len(sediments) > 0 {
				return files, sediments
			}
		}
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(4 * time.Second):
		}
	}
	return nil, nil
}

func (c *Client) resolveImageURLs(ctx context.Context, conversationID string, fileIDs, sedimentIDs []string) []string {
	var urls []string
	for _, fileID := range fileIDs {
		if fileID == "file_upload" {
			continue
		}
		if u := c.fileDownloadURL(ctx, conversationID, fileID); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) > 0 || conversationID == "" {
		return urls
	}
	for _, sedimentID := range sedimentIDs {
		if u := c.attachmentDownloadURL(ctx, conversationID, sedimentID); u != "" {
			urls = append(urls, u)
		}
	}
	return urls
}

func (c *Client) fileDownloadURL(ctx context.Context, conversationID, fileID string) string {
	if strings.TrimSpace(conversationID) != "" {
		query := url.Values{}
		query.Set("conversation_id", conversationID)
		query.Set("inline", "false")
		path := "/backend-api/files/download/" + url.PathEscape(fileID) + "?" + query.Encode()
		if resolved := c.downloadURL(ctx, path); resolved != "" {
			return resolved
		}
	}
	path := "/backend-api/files/" + url.PathEscape(fileID) + "/download"
	return c.downloadURL(ctx, path)
}

func (c *Client) attachmentDownloadURL(ctx context.Context, conversationID, attachmentID string) string {
	path := "/backend-api/conversation/" + url.PathEscape(conversationID) + "/attachment/" + url.PathEscape(attachmentID) + "/download"
	return c.downloadURL(ctx, path)
}

func (c *Client) downloadURL(ctx context.Context, path string) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	targetPath := path
	if index := strings.IndexByte(targetPath, '?'); index >= 0 {
		targetPath = targetPath[:index]
	}
	for key, value := range c.headers(targetPath, map[string]string{"Accept": "application/json"}) {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ""
	}
	payload, err := readJSONResponse(resp, path)
	if err != nil {
		return ""
	}
	if u := util.Clean(payload["download_url"]); u != "" {
		return u
	}
	return util.Clean(payload["url"])
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, headers map[string]string, stream bool) (*http.Response, error) {
	data, _ := json.Marshal(payload)
	return c.postRaw(ctx, path, data, headers, stream)
}

func (c *Client) postRaw(ctx context.Context, path string, data []byte, headers map[string]string, stream bool) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, upstreamTransportError(path, err)
	}
	return resp, nil
}

func ensureOK(resp *http.Response, context string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	data, _ := io.ReadAll(resp.Body)
	return upstreamHTTPError(context, resp.StatusCode, data)
}

func ensureOKAndClose(resp *http.Response, context string) error {
	defer resp.Body.Close()
	return ensureOK(resp, context)
}

func readJSONResponse(resp *http.Response, context string) (map[string]any, error) {
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamHTTPError(context, resp.StatusCode, data)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func upstreamHTTPError(context string, status int, body []byte) error {
	detail := summarizeUpstreamErrorBody(body)
	if detail == "" {
		return fmt.Errorf("%s failed: status=%d", context, status)
	}
	return fmt.Errorf("%s failed: status=%d, %s", context, status, detail)
}

func upstreamTransportError(context string, err error) error {
	if err == nil {
		return nil
	}
	if detail, ok := util.SummarizeUpstreamConnectionError(err.Error()); ok {
		return fmt.Errorf("%s failed: %s", context, detail)
	}
	return fmt.Errorf("%s failed: %w", context, err)
}

func summarizeUpstreamErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	if isCloudflareChallengeBody(lower) {
		return "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy"
	}
	if looksLikeHTMLBody(lower) {
		return "upstream returned HTML error page"
	}
	const maxBodyDetail = 2048
	if len(text) > maxBodyDetail {
		return "body=" + text[:maxBodyDetail] + "...(truncated)"
	}
	return "body=" + text
}

func isCloudflareChallengeBody(lower string) bool {
	return strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare")
}

func looksLikeHTMLBody(lower string) bool {
	return strings.Contains(lower, "<html") ||
		strings.Contains(lower, "<!doctype html") ||
		strings.Contains(lower, "<body")
}

func iterSSEPayloads(ctx context.Context, reader io.Reader, out chan<- string) error {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 2048)
	for {
		n, err := reader.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				idx := bytes.IndexByte(buf, '\n')
				if idx < 0 {
					break
				}
				line := strings.TrimSpace(string(buf[:idx]))
				buf = buf[idx+1:]
				if strings.HasPrefix(line, "data:") {
					payload := strings.TrimSpace(line[5:])
					if payload != "" {
						select {
						case out <- payload:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func decodeImageReference(value string) ([]byte, error) {
	if value != "" && len(value) < 512 && !strings.HasPrefix(value, "data:") && !strings.ContainsAny(value, "\r\n") {
		path := filepath.Clean(os.ExpandEnv(value))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return os.ReadFile(path)
		}
	}
	return util.B64Decode(value)
}

func extractImageToolRecords(data map[string]any) ([]string, []string) {
	mapping := util.StringMap(data["mapping"])
	fileRE := regexp.MustCompile(`file-service://([A-Za-z0-9_-]+)`)
	sedRE := regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`)
	type record struct {
		createTime float64
		files      []string
		sediments  []string
	}
	var records []record
	for _, rawNode := range mapping {
		node := util.StringMap(rawNode)
		message := util.StringMap(node["message"])
		author := util.StringMap(message["author"])
		metadata := util.StringMap(message["metadata"])
		content := util.StringMap(message["content"])
		if author["role"] != "tool" || metadata["async_task_type"] != "image_gen" || content["content_type"] != "multimodal_text" {
			continue
		}
		var files, sediments []string
		for _, part := range anySlice(content["parts"]) {
			text := ""
			if m, ok := part.(map[string]any); ok {
				text = util.Clean(m["asset_pointer"])
			} else {
				text = util.Clean(part)
			}
			for _, hit := range fileRE.FindAllStringSubmatch(text, -1) {
				if len(hit) > 1 {
					files = appendUnique(files, hit[1])
				}
			}
			for _, hit := range sedRE.FindAllStringSubmatch(text, -1) {
				if len(hit) > 1 {
					sediments = appendUnique(sediments, hit[1])
				}
			}
		}
		records = append(records, record{createTime: floatValue(message["create_time"]), files: files, sediments: sediments})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].createTime < records[j].createTime })
	var files, sediments []string
	for _, rec := range records {
		files = appendUnique(files, rec.files...)
		sediments = appendUnique(sediments, rec.sediments...)
	}
	return files, sediments
}

func anySlice(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
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

func filter(values []string, keep func(string) bool) []string {
	out := values[:0]
	for _, value := range values {
		if keep(value) {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func floatValue(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	default:
		return 0
	}
}
