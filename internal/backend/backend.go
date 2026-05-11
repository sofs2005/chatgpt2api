package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

const (
	DefaultClientVersion     = "prod-be885abbfcfe7b1f511e88b3003d9ee44757fbad"
	DefaultClientBuildNumber = "5955942"

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
}

type ChatRequirements struct {
	Token          string
	ProofToken     string
	TurnstileToken string
	SOToken        string
	Raw            map[string]any
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

func (c *Client) StreamConversation(ctx context.Context, messages []map[string]any, model, prompt string) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if len(messages) == 0 {
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
		if c.AccessToken != "" {
			conduitToken, prepareErr := c.prepareTextConversation(ctx, messages, reqs, model)
			if prepareErr == nil {
				resp, startErr := c.startTextConversation(ctx, messages, reqs, conduitToken, model)
				if startErr == nil {
					defer resp.Body.Close()
					if ensureOK(resp, officialStreamPath) == nil {
						errCh <- iterSSEPayloads(ctx, resp.Body, out)
						return
					}
				}
			}
		}
		path, timezoneName := c.chatTarget()
		payload := c.conversationPayload(messages, model, timezoneName)
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

func textModelSlug(model string) string {
	switch strings.TrimSpace(model) {
	case "auto", "":
		return "auto"
	default:
		return strings.TrimSpace(model)
	}
}

func (c *Client) prepareTextConversation(ctx context.Context, messages []map[string]any, reqs ChatRequirements, model string) (string, error) {
	prompt := conversationPrompt(messages)
	payload := map[string]any{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     util.NewUUID(),
		"model":                 textModelSlug(model),
		"client_prepare_state":  "success",
		"timezone_offset_min":   -480,
		"timezone":              "Asia/Shanghai",
		"conversation_mode":     map[string]any{"kind": "primary_assistant"},
		"system_hints":          []any{},
		"partial_query": map[string]any{
			"id":      util.NewUUID(),
			"author":  map[string]any{"role": "user"},
			"content": map[string]any{"content_type": "text", "parts": []any{prompt}},
		},
		"supports_buffering":  true,
		"supported_encodings": []any{"v1"},
		"client_contextual_info": map[string]any{
			"app_name": "chatgpt.com",
		},
	}
	resp, err := c.postJSON(ctx, officialPreparePath, payload, c.officialHeaders(officialPreparePath, reqs, "", "*/*"), false)
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

func (c *Client) startTextConversation(ctx context.Context, messages []map[string]any, reqs ChatRequirements, conduitToken, model string) (*http.Response, error) {
	prompt := conversationPrompt(messages)
	payload := map[string]any{
		"action": "next",
		"messages": []any{
			map[string]any{
				"id":          util.NewUUID(),
				"author":      map[string]any{"role": "user"},
				"create_time": float64(time.Now().UnixNano()) / 1e9,
				"content": map[string]any{
					"content_type": "text",
					"parts":        []any{prompt},
				},
				"metadata": map[string]any{
					"developer_mode_connector_ids": []any{},
					"selected_github_repos":        []any{},
					"selected_all_github_repos":    false,
					"serialization_metadata":       map[string]any{"custom_symbol_offsets": []any{}},
				},
			},
		},
		"parent_message_id":                    util.NewUUID(),
		"model":                                textModelSlug(model),
		"client_prepare_state":                 "sent",
		"timezone_offset_min":                  -480,
		"timezone":                             "Asia/Shanghai",
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []any{},
		"supports_buffering":                   true,
		"supported_encodings":                  []any{"v1"},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
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
	return c.postJSON(ctx, officialStreamPath, payload, c.officialHeaders(officialStreamPath, reqs, conduitToken, "text/event-stream"), true)
}

// VisionImage represents an image to be uploaded for multimodal vision understanding.
type VisionImage struct {
	Data        []byte
	ContentType string
	FileName    string
}

func (c *Client) uploadVisionImages(ctx context.Context, images []VisionImage) ([]uploadedImageRef, error) {
	refs := make([]uploadedImageRef, 0, len(images))
	for i, img := range images {
		fileName := img.FileName
		if fileName == "" {
			fileName = fmt.Sprintf("image_%d.png", i)
		}
		ref, err := c.uploadImage(ctx, ResponsesInputImage{Data: img.Data, ContentType: img.ContentType}, fileName)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func buildVisionParts(prompt string, refs []uploadedImageRef) []any {
	parts := []any{prompt}
	for _, ref := range refs {
		parts = append(parts, map[string]any{
			"content_type":  "image_asset_pointer",
			"asset_pointer": "file-service://" + ref.FileID,
			"width":         ref.Width,
			"height":        ref.Height,
			"size_bytes":    ref.FileSize,
		})
	}
	return parts
}

func buildVisionAttachments(refs []uploadedImageRef) []map[string]any {
	attachments := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		attachments = append(attachments, map[string]any{
			"id":       ref.FileID,
			"mimeType": ref.MIMEType,
			"name":     ref.FileName,
			"size":     ref.FileSize,
			"width":    ref.Width,
			"height":   ref.Height,
		})
	}
	return attachments
}

func (c *Client) prepareMultimodalConversation(ctx context.Context, messages []map[string]any, reqs ChatRequirements, model string, refs []uploadedImageRef) (string, error) {
	prompt := conversationPrompt(messages)
	payload := map[string]any{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     util.NewUUID(),
		"model":                 textModelSlug(model),
		"client_prepare_state":  "success",
		"timezone_offset_min":   -480,
		"timezone":              "Asia/Shanghai",
		"conversation_mode":     map[string]any{"kind": "primary_assistant"},
		"system_hints":          []any{},
		"partial_query": map[string]any{
			"id":      util.NewUUID(),
			"author":  map[string]any{"role": "user"},
			"content": map[string]any{"content_type": "multimodal_text", "parts": buildVisionParts(prompt, refs)},
		},
		"supports_buffering":  true,
		"supported_encodings": []any{"v1"},
		"client_contextual_info": map[string]any{
			"app_name": "chatgpt.com",
		},
	}
	resp, err := c.postJSON(ctx, officialPreparePath, payload, c.officialHeaders(officialPreparePath, reqs, "", "*/*"), false)
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

func (c *Client) startMultimodalConversation(ctx context.Context, messages []map[string]any, reqs ChatRequirements, conduitToken, model string, refs []uploadedImageRef) (*http.Response, error) {
	prompt := conversationPrompt(messages)
	attachments := buildVisionAttachments(refs)
	payload := map[string]any{
		"action": "next",
		"messages": []any{
			map[string]any{
				"id":          util.NewUUID(),
				"author":      map[string]any{"role": "user"},
				"create_time": float64(time.Now().UnixNano()) / 1e9,
				"content": map[string]any{
					"content_type": "multimodal_text",
					"parts":        buildVisionParts(prompt, refs),
				},
				"metadata": map[string]any{
					"developer_mode_connector_ids": []any{},
					"selected_github_repos":        []any{},
					"selected_all_github_repos":    false,
					"serialization_metadata":       map[string]any{"custom_symbol_offsets": []any{}},
					"attachments":                  attachments,
				},
			},
		},
		"parent_message_id":                    util.NewUUID(),
		"model":                                textModelSlug(model),
		"client_prepare_state":                 "sent",
		"timezone_offset_min":                  -480,
		"timezone":                             "Asia/Shanghai",
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []any{},
		"supports_buffering":                   true,
		"supported_encodings":                  []any{"v1"},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
			"force_use_sse":                       true,
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
	return c.postJSON(ctx, officialStreamPath, payload, c.officialHeaders(officialStreamPath, reqs, conduitToken, "text/event-stream"), true)
}

func (c *Client) StreamMultimodalConversation(ctx context.Context, messages []map[string]any, model string, images []VisionImage) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if c.AccessToken == "" {
			errCh <- fmt.Errorf("vision requires authentication")
			return
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
		refs, err := c.uploadVisionImages(ctx, images)
		if err != nil {
			errCh <- err
			return
		}
		conduitToken, err := c.prepareMultimodalConversation(ctx, messages, reqs, model, refs)
		if err != nil {
			errCh <- err
			return
		}
		resp, err := c.startMultimodalConversation(ctx, messages, reqs, conduitToken, model, refs)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if err := ensureOK(resp, officialStreamPath); err != nil {
			errCh <- err
			return
		}
		errCh <- iterMultimodalSSEPayloads(ctx, resp.Body, out)
	}()
	return out, errCh
}

func (c *Client) conversationPayload(messages []map[string]any, model, timezoneName string) map[string]any {
	conversationMessages := []map[string]any{conversationUserMessage(conversationPrompt(messages))}
	return map[string]any{
		"action": "next", "messages": conversationMessages, "model": model, "parent_message_id": "client-created-root",
		"conversation_mode": map[string]any{"kind": "primary_assistant"}, "conversation_origin": nil,
		"force_paragen": false, "force_paragen_model_slug": "", "force_rate_limit": false, "force_use_sse": true,
		"history_and_training_disabled": true, "reset_rate_limits": false, "suggestions": []any{}, "supported_encodings": []any{"v1"},
		"enable_message_followups": true, "supports_buffering": true,
		"system_hints": []any{}, "timezone": timezoneName, "timezone_offset_min": -480,
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
			if len(buf) > 0 {
				line := strings.TrimSpace(string(buf))
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
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func iterMultimodalSSEPayloads(ctx context.Context, reader io.Reader, out chan<- string) error {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 2048)
	processLine := func(line string) error {
		if !strings.HasPrefix(line, "data:") {
			return nil
		}
		payload := strings.TrimSpace(line[5:])
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var event map[string]any
		if json.Unmarshal([]byte(payload), &event) != nil {
			return nil
		}
		if isComplete, _ := event["is_complete"].(bool); isComplete {
			return nil
		}
		for _, text := range extractMultimodalText(event) {
			select {
			case out <- text:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

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
				if err := processLine(line); err != nil {
					return err
				}
			}
		}
		if err == io.EOF {
			if len(buf) > 0 {
				line := strings.TrimSpace(string(buf))
				if err := processLine(line); err != nil {
					return err
				}
			}
			return nil
		}
		if err != nil {
			if len(buf) > 0 {
				line := strings.TrimSpace(string(buf))
				_ = processLine(line)
			}
			return err
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func extractMultimodalText(event map[string]any) []string {
	if v, ok := event["v"]; ok {
		switch val := v.(type) {
		case string:
			if val != "" {
				return []string{val}
			}
		case []any:
			var texts []string
			for _, item := range val {
				if op, ok := item.(map[string]any); ok {
					if op["o"] == "append" {
						if s, ok := op["v"].(string); ok && strings.TrimSpace(s) != "" {
							texts = append(texts, s)
						}
					}
				}
			}
			if len(texts) > 0 {
				return texts
			}
		case map[string]any:
			if texts := extractPartsText(val); len(texts) > 0 {
				return texts
			}
		}
	}
	if event["o"] == "append" {
		if s, ok := event["v"].(string); ok && strings.TrimSpace(s) != "" {
			return []string{s}
		}
	}
	if msg, ok := event["message"].(map[string]any); ok {
		if texts := extractPartsText(msg); len(texts) > 0 {
			return texts
		}
	}
	return nil
}

func extractPartsText(message map[string]any) []string {
	content, _ := message["content"].(map[string]any)
	if content == nil {
		return nil
	}
	parts, _ := content["parts"].([]any)
	var texts []string
	for _, part := range parts {
		if text, ok := part.(string); ok && text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}
