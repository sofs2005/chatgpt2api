package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"chatgpt2api/internal/contextoffload"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

const (
	// DefaultClientVersion 是上游前端构建标识的兜底值。
	//
	// 真实取值由 refreshBuildIdentifiers 从上游首页的 data-build 解析得到：
	// data-build 是构建自报的版本，服务端同时在线多个构建（边缘缓存不一致），
	// 因此写死常量必然与本次请求实际命中的构建错开，构成可识别的身份不一致。
	// 这里的常量只在解析失败时使用，不应作为长期真值。
	DefaultClientVersion     = "prod-980a55fc7f96eb70ab707f04eca80e3f613c9ed1"
	DefaultClientBuildNumber = "11447364"
)

type AccountLookup interface {
	GetAccount(accessToken string) map[string]any
}

type AccountCookieStore interface {
	UpdateAccount(accessToken string, updates map[string]any) map[string]any
}

type Client struct {
	BaseURL           string
	ClientVersion     string
	ClientBuildNumber string
	AccessToken       string

	lookup         AccountLookup
	proxy          *service.ProxyService
	httpClient     *http.Client
	fp             map[string]string
	userAgent      string
	deviceID       string
	sessionID      string
	powSources     []string
	powDataBuild   string
	sessionCookies map[string]string

	imageSettleEnabled         bool
	imageCheckBeforeHitEnabled bool
	imageSettleSecs            time.Duration
	imageModelSlug             string
	searchTimeout              time.Duration
	searchPollInterval         time.Duration
	diagnostic                 func(stage string, attrs map[string]any)
	textAttachmentCache        *TextAttachmentCache

	// clearance 是 cf_clearance 兜底；nil 或未启用时行为与原来完全一致。
	clearance *service.ClearanceService
}

// SetClearanceService 注入 cf_clearance 兜底服务；nil 表示关闭。
func (c *Client) SetClearanceService(clearance *service.ClearanceService) {
	c.clearance = clearance
}

type ChatRequirements struct {
	Token          string
	ProofToken     string
	TurnstileToken string
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

		imageSettleEnabled:         true,
		imageCheckBeforeHitEnabled: true,
		imageSettleSecs:            2 * time.Second,
		searchTimeout:              searchTimeoutSecs,
		searchPollInterval:         searchPollIntervalSecs,
	}
	c.fp = c.buildFingerprint()
	c.applyBrowserFingerprint()
	c.userAgent = c.fp["user-agent"]
	c.deviceID = c.fp["oai-device-id"]
	c.sessionID = c.fp["oai-session-id"]
	c.initAccountCookies()
	// 账号绑定了自己的代理时优先使用，使 cf_clearance 的签发 IP 与出口 IP 保持一致。
	c.httpClient = proxy.BrowserHTTPClientForAccount(c.accountForFingerprint(), c.fp["impersonate"], 300*time.Second)
	// cf_clearance 兜底随出口走，从 ProxyService 取用，避免装配层逐处传递。
	c.clearance = proxy.Clearance()
	return c
}

// accountForFingerprint 返回当前 access token 对应的账号数据。
// 账号不存在（如匿名链路）时返回 nil，调用方按「未绑定代理」处理。
func (c *Client) accountForFingerprint() map[string]any {
	if c == nil || c.AccessToken == "" || c.lookup == nil {
		return nil
	}
	return c.lookup.GetAccount(c.AccessToken)
}

// ImagePollOptions 暴露当前生图轮询配置，供装配层校验与可观测使用。
type ImagePollOptions struct {
	SettleEnabled  bool
	CheckBeforeHit bool
	SettleSecs     time.Duration
}

// SetImagePollOptions 配置官方生图轮询的「二次确认」与「先 check 再 hit」语义。
// settleEnabled 开启时，发现 file_ids 后等待 settleSecs 再确认同一批 id 出现两次才返回；
// checkBeforeHit 开启时，必须经会话轮询确认 id（而非仅凭 SSE 事件）。
func (c *Client) SetImagePollOptions(settleEnabled, checkBeforeHit bool, settleSecs time.Duration) {
	c.imageSettleEnabled = settleEnabled
	c.imageCheckBeforeHitEnabled = checkBeforeHit
	if settleSecs < 0 {
		settleSecs = 0
	}
	c.imageSettleSecs = settleSecs
}

// ImagePollOptions 返回当前生效的生图轮询配置。
func (c *Client) ImagePollOptions() ImagePollOptions {
	return ImagePollOptions{
		SettleEnabled:  c.imageSettleEnabled,
		CheckBeforeHit: c.imageCheckBeforeHitEnabled,
		SettleSecs:     c.imageSettleSecs,
	}
}

// SetImageModelSlug 覆盖官方生图链路发给上游的 model slug。
// 空字符串表示使用默认值 auto，即由服务端自动路由到当前生图模型。
func (c *Client) SetImageModelSlug(slug string) {
	c.imageModelSlug = strings.TrimSpace(slug)
}

// ImageModelSlug 返回当前生效的官方生图 model slug。
func (c *Client) ImageModelSlug() string {
	return c.imageModelSlug
}

// SetDiagnosticLogger 注册上游阶段诊断回调；nil 表示关闭诊断。
// 回调只应收到脱敏后的结构化字段，绝不包含 token、cookie、prompt、
// 请求体、base64 或带签名 query 的完整 URL。
func (c *Client) SetDiagnosticLogger(fn func(stage string, attrs map[string]any)) {
	c.diagnostic = fn
}

// reportStage 向诊断回调上报一个上游阶段结果。
// attrs 可为空；ok 为 false 表示阶段失败，调用方应同时给出 error 字段。
func (c *Client) reportStage(stage string, ok bool, attrs map[string]any) {
	if c == nil || c.diagnostic == nil {
		return
	}
	payload := map[string]any{}
	for key, value := range attrs {
		payload[key] = value
	}
	payload["stage"] = stage
	payload["ok"] = ok
	c.diagnostic(stage, payload)
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
	resp, err := c.do(req)
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

func (c *Client) StreamConversation(ctx context.Context, messages []map[string]any, model, prompt string, tools any, choice any) (<-chan string, <-chan error) {
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
			plan := contextoffload.PlanContext(messages, tools, choice, contextoffload.DefaultOptions())
			offloadMessages := plan.InlineMessages
			var attachments []TextAttachmentRef
			if plan.NeedsUpload() {
				var uploadErr error
				attachments, uploadErr = c.uploadTextContextFiles(ctx, plan.Files, reqs, 60*time.Second)
				if uploadErr != nil {
					fallback, fallbackErr := plan.FallbackInlineMessages()
					if fallbackErr != nil {
						errCh <- fmt.Errorf("context attachment upload failed: %w", uploadErr)
						return
					}
					offloadMessages = fallback
					attachments = nil
				}
			}
			conduitToken, prepareErr := c.prepareTextConversation(ctx, offloadMessages, reqs, model, attachments)
			if prepareErr == nil {
				resp, startErr := c.startTextConversation(ctx, offloadMessages, reqs, conduitToken, model, attachments)
				if startErr == nil {
					defer resp.Body.Close()
					if ensureOK(resp, officialStreamPath) == nil {
						errCh <- iterSSEPayloads(ctx, resp.Body, out)
						return
					}
				}
			}
		}
		path := c.chatTarget()
		payload := c.conversationPayload(messages, model)
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
	account := c.accountForFingerprint()
	fp := service.BrowserFingerprintStringMap(account["fp"])
	for _, key := range []string{"user-agent", "impersonate", "oai-device-id", "oai-session-id", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "sec-ch-ua-arch", "sec-ch-ua-bitness", "sec-ch-ua-full-version", "sec-ch-ua-full-version-list", "sec-ch-ua-platform-version"} {
		if value := util.Clean(account[key]); value != "" {
			fp[key] = value
		}
	}
	return fp
}

func (c *Client) applyBrowserFingerprint() {
	c.fp = service.BrowserFingerprintStringMap(c.fp)
}

func (c *Client) initAccountCookies() {
	c.sessionCookies = map[string]string{}
	if c.AccessToken == "" || c.lookup == nil {
		return
	}
	account := c.lookup.GetAccount(c.AccessToken)
	for name, value := range service.AccountSessionCookiesForRequest(account, time.Now()) {
		c.sessionCookies[name] = value
	}
}

func (c *Client) addAccountCookies(req *http.Request) {
	if req == nil || len(c.sessionCookies) == 0 || !c.isAccountCookieURL(req) {
		return
	}
	names := make([]string, 0, len(c.sessionCookies))
	for name := range c.sessionCookies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if value := c.sessionCookies[name]; value != "" {
			req.AddCookie(&http.Cookie{Name: name, Value: value})
		}
	}
}

func (c *Client) isAccountCookieURL(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	host := req.URL.Hostname()
	if host == "" {
		return false
	}
	base, err := url.Parse(c.BaseURL)
	if err == nil && host == base.Hostname() {
		return true
	}
	return host == "chatgpt.com" || strings.HasSuffix(host, ".chatgpt.com")
}

func (c *Client) rememberAccountCookies(resp *http.Response) {
	if resp == nil || c.AccessToken == "" || len(resp.Cookies()) == 0 {
		return
	}
	merged := map[string]string{}
	for name, value := range c.sessionCookies {
		merged[name] = value
	}
	updatedAt := map[string]string{}
	if c.lookup != nil {
		account := c.lookup.GetAccount(c.AccessToken)
		for name, value := range service.SessionCookieStringMap(account["session_cookie_updated_at"]) {
			updatedAt[name] = value
		}
	}
	changed := false
	updatedAtChanged := false
	now := time.Now()
	for _, cookie := range resp.Cookies() {
		allowed := service.SessionCookieStringMap(map[string]string{cookie.Name: firstNonEmpty(cookie.Value, "x")})
		if len(allowed) == 0 {
			continue
		}
		if cookie.MaxAge < 0 || cookie.Value == "" {
			if _, ok := merged[cookie.Name]; ok {
				delete(merged, cookie.Name)
				changed = true
			}
			if _, ok := updatedAt[cookie.Name]; ok {
				delete(updatedAt, cookie.Name)
				updatedAtChanged = true
			}
			continue
		}
		if merged[cookie.Name] != cookie.Value {
			merged[cookie.Name] = cookie.Value
			changed = true
		}
		if stamped := service.SessionCookieUpdatedAtForCookies(map[string]string{cookie.Name: cookie.Value}, now); len(stamped) > 0 {
			for name, value := range stamped {
				if updatedAt[name] != value {
					updatedAt[name] = value
					updatedAtChanged = true
				}
			}
		}
	}
	if !changed && !updatedAtChanged {
		return
	}
	if changed {
		c.sessionCookies = merged
	}
	if store, ok := c.lookup.(AccountCookieStore); ok {
		updates := map[string]any{"session_cookies": merged}
		if len(updatedAt) > 0 {
			updates["session_cookie_updated_at"] = updatedAt
		}
		store.UpdateAccount(c.AccessToken, updates)
	}
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	accountCookieURL := c.isAccountCookieURL(req)
	if accountCookieURL {
		c.addAccountCookies(req)
	}
	resp, err := c.httpClient.Do(req)
	if err == nil && accountCookieURL {
		c.rememberAccountCookies(resp)
	}
	if err != nil || !accountCookieURL || !service.IsClearanceChallengeResponse(resp) {
		return resp, err
	}
	return c.retryWithFreshClearance(req, resp)
}

// retryWithFreshClearance 在命中 Cloudflare 挑战时，用该账号出口现取一份
// cf_clearance，并以 FlareSolverr 实际使用的 UA 重放一次请求。
//
// 挑战判定绑定出口 IP 与浏览器指纹，所以这里**不换账号、不换出口**：换出口会
// 让新拿到的 cf_clearance 当场失效。两处必须同时替换——
//   - Cookie 头：注入新解出的 cf_clearance 等 CF 凭证；
//   - User-Agent 与 Sec-Ch-Ua*：cf_clearance 绑定签发时的 UA，
//     只回注 cookie 而沿用旧 UA 会被判为凭证盗用。
//
// 重放的请求体来自 req.GetBody，调用方负责构造；流式请求体不可重放时放弃重试。
//
// 注意这里覆盖的是**本次重放的请求头**，不写回账号持久指纹：FlareSolverr 的 UA
// 是浏览器容器的实际版本，写回会让账号长期自报一个与 TLS 指纹（surf chrome145）
// 不匹配的版本，把一次性修复变成长期撕裂。凭证也只持久化 cookie（见
// rememberAccountCookies），UA 只在本次重放生效。
func (c *Client) retryWithFreshClearance(req *http.Request, resp *http.Response) (*http.Response, error) {
	if req == nil {
		return resp, nil
	}
	// 从这里起的每个放弃分支都要上报：此前它们静默 return，日志里看不出
	// 兜底是没有配置、还是配置了但被跳过，两种情况的表现完全一样。
	if !c.clearance.Enabled() {
		c.reportStage("clearance", false, map[string]any{"skipped": "clearance disabled"})
		return resp, nil
	}
	// 无 body（GET 等）可直接重放；有 body 但拿不到副本的（流式上传）放弃。
	replayable := req.Body == nil || req.GetBody != nil
	if !replayable {
		c.reportStage("clearance", false, map[string]any{"skipped": "request body is not replayable"})
		return resp, nil
	}
	// 出口必须与请求实际使用的那个一致：cf_clearance 绑定签发 IP，
	// 用别的出口求解等于拿到一张当场作废的凭证。
	proxyURL := c.proxy.EgressProxy(service.AccountProxy(c.accountForFingerprint()))
	ctx := req.Context()
	bundle, err := c.clearance.Refresh(ctx, proxyURL)
	if err != nil {
		c.reportStage("clearance", false, map[string]any{"error": err})
		return resp, nil
	}
	// 从这里开始原响应作废：先归还连接，再重放。
	// 之后的失败必须返回错误而不是返回这个已关闭的响应，否则调用方会读到空 body。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()

	var body io.ReadCloser
	if req.GetBody != nil {
		body, err = req.GetBody()
		if err != nil {
			c.reportStage("clearance", false, map[string]any{"error": err})
			return nil, upstreamTransportError("clearance", err)
		}
	}
	// 身份覆盖走 context：surf 的 impersonate 中间件会在发送前把 UA 与 Sec-Ch-Ua
	// 改回 profile 自己的值，在这里 Set 头会被静默丢弃，等于没换身份。
	ctx = service.WithIdentityOverride(ctx, service.ClearanceRequestHeaders(bundle))
	retry, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), body)
	if err != nil {
		c.reportStage("clearance", false, map[string]any{"error": err})
		return nil, upstreamTransportError("clearance", err)
	}
	retry.Header = req.Header.Clone()
	if retry.Header == nil {
		retry.Header = http.Header{}
	}
	applyClearanceCookies(retry, bundle)

	// 这次重放不再触发二次兜底，避免与上游来回拉锯。
	retryResp, retryErr := c.httpClient.Do(retry)
	if retryErr != nil {
		c.reportStage("clearance", false, map[string]any{"error": retryErr})
		return nil, upstreamTransportError("clearance", retryErr)
	}
	c.rememberAccountCookies(retryResp)
	// ok 必须按「挑战过没过」判定，不能只表示「重放跑完了」：重放本身总是能跑完，
	// 拿回来的仍可能是 403（例如 FlareSolverr 解的出口与实际请求出口不一致，
	// cf_clearance 当场作废）。此前无条件上报 ok=true，日志里 403 与 200 长得一样，
	// 兜底到底有没有用根本看不出来。
	passed := !service.IsClearanceChallengeResponse(retryResp)
	c.reportStage("clearance", passed, map[string]any{
		"status":           retryResp.StatusCode,
		"challenge_passed": passed,
	})
	return retryResp, nil
}

// applyClearanceCookies 把 CF 凭证写进请求的 Cookie 头，保留已有的其它 cookie。
func applyClearanceCookies(req *http.Request, bundle service.ClearanceBundle) {
	values := bundle.ClearanceCookieValues()
	if len(values) == 0 {
		return
	}
	if existing := req.Cookies(); len(existing) > 0 {
		names := make([]string, 0, len(existing))
		for _, cookie := range existing {
			names = append(names, cookie.Name)
		}
		sort.Strings(names)
		req.Header.Del("Cookie")
		for _, name := range names {
			if _, replaced := values[name]; replaced {
				continue
			}
			for _, cookie := range existing {
				if cookie.Name == name {
					req.AddCookie(cookie)
					break
				}
			}
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		req.AddCookie(&http.Cookie{Name: name, Value: values[name]})
	}
}

func (c *Client) headers(path string, extra map[string]string) map[string]string {
	headers := map[string]string{
		"User-Agent":                  c.userAgent,
		"Origin":                      c.BaseURL,
		"Referer":                     c.BaseURL + "/",
		"Accept-Language":             util.OutboundAcceptLanguage,
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
		"OAI-Language":                util.OutboundLocaleTag,
		"OAI-Client-Version":          c.ClientVersion,
		"OAI-Client-Build-Number":     c.ClientBuildNumber,
		// 上游前端 Hc() 默认头里固定带这个标记，缺失即为可识别的客户端差异。
		"x-openai-web-frontend": "core_web",
		"X-OpenAI-Target-Path":  path,
		"X-OpenAI-Target-Route": path,
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
		"Accept-Language":           util.OutboundAcceptLanguage,
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

// bootstrap 执行上游 bootstrap 请求。
// 重试策略由 util.RetryBootstrap 统一提供，与账号刷新链路共用同一份实现。
func (c *Client) bootstrap(ctx context.Context) error {
	return util.RetryBootstrap(ctx, func(attempt int) (error, bool) {
		return c.bootstrapOnce(ctx, attempt)
	})
}

// bootstrapOnce 执行一次 bootstrap。第二个返回值表示失败是否值得重试。
func (c *Client) bootstrapOnce(ctx context.Context, attempt int) (error, bool) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/", nil)
	for key, value := range c.bootstrapHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := c.do(req)
	if err != nil {
		c.reportStage("bootstrap", false, map[string]any{"error": err, "attempt": attempt})
		return upstreamTransportError("bootstrap", err), false
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		attrs := map[string]any{
			"status":  resp.StatusCode,
			"attempt": attempt,
			"body":    summarizeUpstreamErrorBody(data),
		}
		// CF 的诊断头是判断「挑战 / 封禁 / 正常拒绝」的最直接依据：
		// 没有它时，403 的三种成因在日志里长得完全一样。
		for name, key := range cloudflareDiagnosticHeaders {
			if value := strings.TrimSpace(resp.Header.Get(name)); value != "" {
				attrs[key] = value
			}
		}
		c.reportStage("bootstrap", false, attrs)
		return upstreamHTTPError("bootstrap", resp.StatusCode, data), util.IsRetryableBootstrapStatus(resp.StatusCode)
	}
	c.powSources, c.powDataBuild = parsePOWResources(string(data))
	if len(c.powSources) == 0 {
		c.powSources = []string{defaultPOWScript}
	}
	c.refreshBuildIdentifiers(string(data))
	return nil, false
}

// refreshBuildIdentifiers 用本次 bootstrap 命中的构建对齐 OAI-Client-Version
// 与 OAI-Client-Build-Number。
//
// 上游边缘同时在线多个构建（同一套请求头也可能命中不同构建），而 PoW 配置里的
// data-build 取自同一份 HTML。若请求头仍用编译期常量，就会出现「头报旧构建、
// 指纹报新构建」的矛盾，这是服务端可识别的信号，所以两者必须同步刷新。
func (c *Client) refreshBuildIdentifiers(html string) {
	if version := strings.TrimSpace(c.powDataBuild); version != "" {
		c.ClientVersion = version
	}
	if build := parseWebBuildNumber(html); build != "" {
		c.ClientBuildNumber = build
	}
}

func (c *Client) getChatRequirements(ctx context.Context) (ChatRequirements, error) {
	path := "/backend-anon/sentinel/chat-requirements"
	contextName := "noauth_chat_requirements"
	if c.AccessToken != "" {
		path = "/backend-api/sentinel/chat-requirements"
		contextName = "auth_chat_requirements"
	}
	p := buildLegacyRequirementsToken(c.hardware(), c.userAgent, c.powSources, c.powDataBuild)
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
	reqs, err := c.buildRequirements(payload, p)
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

// buildRequirements 解析 chat-requirements 响应。sourceP 是本次请求体里的
// `p`：turnstile.dx 与 so.*_dx 都用它做循环 XOR 解码，缺了它 VM 程序解不出来。
func (c *Client) buildRequirements(data map[string]any, sourceP string) (ChatRequirements, error) {
	if arkose := util.StringMap(data["arkose"]); util.ToBool(arkose["required"]) {
		return ChatRequirements{}, fmt.Errorf("chat requirements requires arkose token, which is not implemented")
	}
	proofToken := ""
	proof := util.StringMap(data["proofofwork"])
	if util.ToBool(proof["required"]) {
		token, err := buildProofToken(c.hardware(), util.Clean(proof["seed"]), util.Clean(proof["difficulty"]), c.userAgent, c.powSources, c.powDataBuild)
		if err != nil {
			return ChatRequirements{}, err
		}
		proofToken = token
	}
	turnstileToken := ""
	turnstile := util.StringMap(data["turnstile"])
	if util.ToBool(turnstile["required"]) {
		dx := util.Clean(turnstile["dx"])
		if dx == "" {
			// 上游对 required=true 但缺 dx 的情形是抛错而不是降级；静默跳过只会换来
			// 一个缺 Turnstile 头的请求，反而更可疑。
			return ChatRequirements{}, fmt.Errorf("chat requirements requested a turnstile challenge without a payload")
		}
		token, err := solveSentinelPayloadFor(dx, sourceP, c.sentinelProfile())
		if err != nil {
			return ChatRequirements{}, fmt.Errorf("solve turnstile: %w", err)
		}
		turnstileToken = token
	}
	return ChatRequirements{Token: util.Clean(data["token"]), ProofToken: proofToken, TurnstileToken: turnstileToken, Raw: data}, nil
}

// sentinelProfile 把本客户端的身份摊平成 sentinel 指纹程序要读的环境值。
//
// 机器身份（屏幕、核数、显存、GPU）与 PoW 载荷、client_contextual_info 同源，
// 都来自 c.hardware()；语言与时区取自 util 的共享常量，与请求头、请求体同源。
// 这样同一请求里三个位置报出的屏幕、核数、语言、时区不会互相矛盾。
func (c *Client) sentinelProfile() sentinelProfile {
	return sentinelProfileFor(c.hardware(), c.userAgent, c.ClientBuildNumber)
}

// hardware 返回本客户端的机器身份，按账号的 oai-device-id 稳定派生：
// 同一账号每次请求都得到同一台机器，而不是每次请求重新掷一次骰子。
// device id 缺失时退化为固定身份（见 hardwareIdentityForSeed）。
func (c *Client) hardware() hardwareIdentity {
	return hardwareIdentityForSeed(c.deviceID)
}

// clientContextualInfo 是网页端在搜索链路里上报的客户端上下文。屏幕与窗口
// 尺寸取自共享机器身份，因此与 PoW 载荷、sentinel 指纹同源。
func (c *Client) clientContextualInfo() map[string]any {
	return clientContextualInfoFor(c.hardware())
}

func clientContextualInfoFor(hw hardwareIdentity) map[string]any {
	pageWidth, pageHeight := hw.viewport()
	return map[string]any{
		"is_dark_mode":      false,
		"time_since_loaded": 1200,
		"page_height":       pageHeight,
		"page_width":        pageWidth,
		"pixel_ratio":       hw.PixelRatio,
		"screen_height":     hw.Resolution[1],
		"screen_width":      hw.Resolution[0],
		"app_name":          "chatgpt.com",
	}
}

// conversationContextualInfo 是网页端在 /f/conversation 里上报的客户端上下文。
// 屏幕与窗口尺寸取自共享机器身份，因此与 PoW 载荷、sentinel 指纹同源，
// 不会在同一请求里互相矛盾。
func (c *Client) conversationContextualInfo() map[string]any {
	return conversationContextualInfoFor(c.hardware())
}

func conversationContextualInfoFor(hw hardwareIdentity) map[string]any {
	pageWidth, pageHeight := hw.viewport()
	return map[string]any{
		"is_dark_mode":                     false,
		"time_since_loaded":                1200,
		"page_height":                      pageHeight,
		"page_width":                       pageWidth,
		"pixel_ratio":                      hw.PixelRatio,
		"screen_height":                    hw.Resolution[1],
		"screen_width":                     hw.Resolution[0],
		"app_name":                         "chatgpt.com",
		"has_web_push_capabilities":        true,
		"web_push_notification_permission": "granted",
	}
}

func (c *Client) chatTarget() string {
	// 匿名与已登录走同一条身份：时区、语言、PoW 探针三者必须自洽，
	// 按登录态切时区只会让同一份身份前后矛盾。登录态只决定打哪个端点。
	if c.AccessToken != "" {
		return "/backend-api/conversation"
	}
	return "/backend-anon/conversation"
}

func textModelSlug(model string) string {
	switch strings.TrimSpace(model) {
	case "auto", "":
		return "auto"
	default:
		return strings.TrimSpace(model)
	}
}

func (c *Client) prepareTextConversation(ctx context.Context, messages []map[string]any, reqs ChatRequirements, model string, attachments []TextAttachmentRef) (string, error) {
	prompt := conversationPrompt(messages)
	payload := map[string]any{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     util.NewUUID(),
		"model":                 textModelSlug(model),
		// prepare 阶段上游只发 none/sent；success 是最终 /f/conversation 的值。
		"client_prepare_state":     "none",
		"client_prepare_dispatch":  "debounced",
		"client_prepare_source":    "composer_editor_state",
		"timezone_offset_min":      outboundTimezoneOffsetMinutes(),
		"timezone":                 util.OutboundTimeZoneName,
		"conversation_mode":        map[string]any{"kind": "primary_assistant"},
		"system_hints":             []any{},
		"model_response_contracts": officialModelResponseContracts(),
		"local_function_names":     officialLocalFunctionNames(),
		"partial_query": map[string]any{
			"id":      util.NewUUID(),
			"author":  map[string]any{"role": "user"},
			"content": map[string]any{"content_type": "text", "parts": []any{prompt}},
		},
		"supports_buffering":     true,
		"supported_encodings":    []any{"v1"},
		"client_contextual_info": officialClientContextualInfo(),
	}
	if len(attachments) > 0 {
		payload["attachments"] = buildTextPrepareAttachments(attachments)
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

func (c *Client) startTextConversation(ctx context.Context, messages []map[string]any, reqs ChatRequirements, conduitToken, model string, attachments []TextAttachmentRef) (*http.Response, error) {
	prompt := conversationPrompt(messages)
	metadata := map[string]any{
		"developer_mode_connector_ids": []any{},
		"selected_github_repos":        []any{},
		"selected_all_github_repos":    false,
		"automation_creation_attribution": map[string]any{
			"origin":  "conversation",
			"flow_id": util.NewUUID(),
		},
		"submission_mode":        "manual_send",
		"serialization_metadata": map[string]any{"custom_symbol_offsets": []any{}},
	}
	if len(attachments) > 0 {
		metadata["attachments"] = buildTextMessageAttachments(attachments)
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
					"parts":        []any{prompt},
				},
				"metadata": metadata,
			},
		},
		"parent_message_id":                    util.NewUUID(),
		"model":                                textModelSlug(model),
		"client_prepare_state":                 "sent",
		"timezone_offset_min":                  outboundTimezoneOffsetMinutes(),
		"timezone":                             util.OutboundTimeZoneName,
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"genui_state_snapshots":                []any{},
		"system_hints":                         []any{},
		"model_response_contracts":             officialModelResponseContracts(),
		"local_function_names":                 officialLocalFunctionNames(),
		"supports_buffering":                   true,
		"supported_encodings":                  []any{"v1"},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"client_contextual_info":               c.conversationContextualInfo(),
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
		"timezone_offset_min":   outboundTimezoneOffsetMinutes(),
		"timezone":              util.OutboundTimeZoneName,
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
		"timezone_offset_min":                  outboundTimezoneOffsetMinutes(),
		"timezone":                             util.OutboundTimeZoneName,
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []any{},
		"supports_buffering":                   true,
		"supported_encodings":                  []any{"v1"},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"force_use_sse":                        true,
		"client_contextual_info":               c.clientContextualInfo(),
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

func (c *Client) conversationPayload(messages []map[string]any, model string) map[string]any {
	conversationMessages := []map[string]any{conversationUserMessage(conversationPrompt(messages))}
	return map[string]any{
		"action": "next", "messages": conversationMessages, "model": model, "parent_message_id": "client-created-root",
		"conversation_mode": map[string]any{"kind": "primary_assistant"}, "conversation_origin": nil,
		"force_paragen": false, "force_paragen_model_slug": "", "force_rate_limit": false, "force_use_sse": true,
		"history_and_training_disabled": true, "reset_rate_limits": false, "suggestions": []any{}, "supported_encodings": []any{"v1"},
		"enable_message_followups": true, "supports_buffering": true,
		"system_hints": []any{}, "timezone": util.OutboundTimeZoneName, "timezone_offset_min": outboundTimezoneOffsetMinutes(),
		"variant_purpose": "comparison_implicit", "websocket_request_id": util.NewUUID(),
		"client_contextual_info": c.conversationContextualInfo(),
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
	resp, err := c.do(req)
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

// UpstreamError 描述一次上游调用失败。
// Error() 只返回对外稳定的摘要文本，原始 transport error 通过 Unwrap() 保留，
// 便于日志与诊断拿到真实原因，同时不影响既有对外错误文案与测试断言。
type UpstreamError struct {
	// Context 标记失败阶段，例如 bootstrap、prepare、image_download。
	Context string
	// Message 是对外展示的摘要文本。
	Message string
	// Status 是上游 HTTP 状态码，transport 失败时为 0。
	Status int
	// Cause 是原始错误，可为空。
	Cause error
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *UpstreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// UpstreamStage 返回失败阶段，供诊断日志与错误链查询使用。
func (e *UpstreamError) UpstreamStage() string {
	if e == nil {
		return ""
	}
	return e.Context
}

// UpstreamStageOf 从错误链中取出 UpstreamError 的阶段标记。
func UpstreamStageOf(err error) string {
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Context
	}
	return ""
}

func upstreamHTTPError(context string, status int, body []byte) error {
	detail := summarizeUpstreamErrorBody(body)
	message := fmt.Sprintf("%s failed: status=%d", context, status)
	if detail != "" {
		message = fmt.Sprintf("%s failed: status=%d, %s", context, status, detail)
	}
	return &UpstreamError{Context: context, Message: message, Status: status}
}

func upstreamTransportError(context string, err error) error {
	if err == nil {
		return nil
	}
	if detail, ok := util.SummarizeUpstreamConnectionError(err.Error()); ok {
		return &UpstreamError{
			Context: context,
			Message: fmt.Sprintf("%s failed: %s", context, detail),
			Cause:   err,
		}
	}
	return &UpstreamError{
		Context: context,
		Message: fmt.Sprintf("%s failed: %v", context, err),
		Cause:   err,
	}
}

// summarizeUpstreamErrorBody 把上游错误响应体压缩成一行可读的诊断文案。
//
// HTML 分支刻意保留响应体的特征片段，而不是只回一句「HTML error page」：
// 上游被拒时返回的 HTML 分好几类（Cloudflare 挑战页、CF 的简单拒绝页、
// 网关错误页），它们的处置方式完全不同，只报一句归一化文案会让线上
// 只剩「被拒了」这个信息，无从判断该换出口、刷新凭证还是改请求。
// 特征片段取自 <title> 与 CF 的判定标记，足以区分类别，且不含请求内容。
func summarizeUpstreamErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	if util.IsCloudflareChallengeBody(lower) {
		return util.CloudflareChallengeMessage + htmlBodyHint(text)
	}
	if looksLikeHTMLBody(lower) {
		return "upstream returned HTML error page" + htmlBodyHint(text)
	}
	const maxBodyDetail = 2048
	if len(text) > maxBodyDetail {
		return "body=" + text[:maxBodyDetail] + "...(truncated)"
	}
	return "body=" + text
}

// htmlBodyHint 从 HTML 响应体里提取一小段可用于区分错误类别的特征。
//
// 只取 <title> 的内容，取不到就回落到 CF 判定标记的存在性。不截取正文，
// 避免把上游页面里的内容（可能含账号相关文案）写进日志。
func htmlBodyHint(text string) string {
	if match := htmlTitleRE.FindStringSubmatch(text); len(match) > 1 {
		if title := strings.TrimSpace(util.Clean(match[1])); title != "" {
			return " (title=" + truncateHint(title, 120) + ")"
		}
	}
	lower := strings.ToLower(text)
	marks := make([]string, 0, 4)
	for _, mark := range []string{"cf_chl", "challenge-platform", "cf-mitigated", "just a moment"} {
		if strings.Contains(lower, mark) {
			marks = append(marks, mark)
		}
	}
	if len(marks) == 0 {
		return ""
	}
	return " (marks=" + strings.Join(marks, ",") + ")"
}

// truncateHint 按字符截断日志提示文案，避免按字节切断多字节字符。
func truncateHint(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// htmlTitleRE 匹配 HTML 文档标题，用于错误页分类。
var htmlTitleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// cloudflareDiagnosticHeaders 把 CF 的诊断响应头映射成日志字段名。
//
// cf-ray 是向 Cloudflare 侧追查该请求的唯一凭据；cf-mitigated 直接标明
// 是「challenge」还是硬拒绝；cf-cache-status 与 server 用来区分命中 CDN
// 与否。这些都不含凭证，可以安全落日志。
var cloudflareDiagnosticHeaders = map[string]string{
	"cf-ray":          "cf_ray",
	"cf-mitigated":    "cf_mitigated",
	"cf-cache-status": "cf_cache_status",
	"server":          "server",
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
