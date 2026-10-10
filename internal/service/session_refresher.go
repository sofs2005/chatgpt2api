package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/util"
)

// SessionRefresher refreshes tokens through /api/auth/session with a uTLS client.
type SessionRefresher struct {
	mu        sync.Mutex
	inFlight  map[string]*refreshCall // key: access_token, deduplicates refreshes
	semaphore chan struct{}           // concurrency control (max 5 concurrent)
	httpDo    func(req *http.Request) (*http.Response, error)
}

type refreshCall struct {
	done   chan struct{}
	result refreshResult
}

type SessionRefreshData struct {
	AccessToken  string
	SessionToken string
	Expires      string
	User         SessionRefreshUser
	// SetCookies 是本次响应经由 Set-Cookie 轮换的 cookie，按原样保留（含 MaxAge）。
	//
	// 必须回传给账号：NextAuth 轮换 __Secure-next-auth.session-token 走的是
	// Set-Cookie 而不是响应体的 sessionToken 字段，而账号后续请求发的是
	// session_cookies。只记 sessionToken 字段不回写 cookie，出站就会变成
	// 「Bearer 是新的、Cookie 里那一片 session-token 还是旧的」，上游按凭证
	// 不一致拒绝——续期明明成功，紧跟着的信息拉取却 401。
	SetCookies []*http.Cookie
	// Clearance 记录本次刷新是否动用了 CF 兜底，以及兜底的结果。
	//
	// 兜底整条路径都在 doRefresh 里静默 return，不把它带出来，「没配 FlareSolverr」
	// 「命中缓存」「真解了一次」「求解报错」四种情况在日志里完全一样——都是刷新失败。
	Clearance ClearanceOutcome
}

type SessionRefreshUser struct {
	ID    string
	Name  string
	Email string
}

type refreshResult struct {
	accessToken    string
	sessionToken   string
	sessionExpires string
	user           SessionRefreshUser
	setCookies     []*http.Cookie
	clearance      ClearanceOutcome
	err            error
}

type SessionRefreshContext struct {
	Cookies map[string]string
	Headers map[string]string
	// Proxy 是账号绑定的代理。刷新请求携带该账号的 cf_clearance，
	// 而 cf_clearance 与签发时的出口 IP 强绑定，必须从同一个 IP 发出。
	Proxy string
	// Profile 是账号的浏览器指纹 profile（如 chrome145 / firefox148）。
	//
	// 刷新请求的 header 来自账号指纹，TLS/HTTP2 指纹则由 profile 决定，
	// 二者必须同源：若这里回落到硬编码的 chrome profile，firefox 账号就会发出
	// 「UA 与 TLS 说 Chrome、Sec-Ch-Ua-Full-Version 说 Firefox」的矛盾身份。
	Profile string
	// ClearanceFallback 在命中 Cloudflare 挑战时现取一份 cf_clearance 供重放。
	//
	// 由 AccountService 注入：求解要拿到该账号的出口与持久化凭证，而 SessionRefresher
	// 只认请求上下文，拿不到 access token。为 nil 表示没有兜底（未配置 FlareSolverr），
	// 此时行为与引入兜底之前完全一致。
	ClearanceFallback func(ctx context.Context) (ClearanceBundle, ClearanceOutcome, error)
}

const (
	maxConcurrentRefreshes = 5
	refreshTimeout         = 15 * time.Second
	sessionEndpoint        = "https://chatgpt.com/api/auth/session"
	// clearanceSolveTimeout 是 CF 兜底求解的预算。
	//
	// 必须比 refreshTimeout 宽：求解要经 FlareSolverr 开一次真实浏览器，常以秒计。
	// 只重放一次（不循环兜底），避免与上游来回拉锯——与 bootstrap 链路同策略。
	clearanceSolveTimeout = 60 * time.Second
)

func NewSessionRefresher(httpDo func(req *http.Request) (*http.Response, error)) *SessionRefresher {
	return &SessionRefresher{
		inFlight:  make(map[string]*refreshCall),
		semaphore: make(chan struct{}, maxConcurrentRefreshes),
		httpDo:    httpDo,
	}
}

func (r *SessionRefresher) RefreshSession(ctx context.Context, accessToken, sessionToken string) (SessionRefreshData, error) {
	return r.RefreshSessionWithContext(ctx, accessToken, sessionToken, SessionRefreshContext{})
}

func (r *SessionRefresher) RefreshSessionWithContext(ctx context.Context, accessToken, sessionToken string, requestContext SessionRefreshContext) (SessionRefreshData, error) {
	if sessionToken == "" {
		return SessionRefreshData{}, fmt.Errorf("session_token is empty")
	}

	// Deduplicate in-flight refreshes for the same access token.
	r.mu.Lock()
	if call, ok := r.inFlight[accessToken]; ok {
		r.mu.Unlock()
		select {
		case <-call.done:
			return call.result.sessionData(), call.result.err
		case <-ctx.Done():
			return SessionRefreshData{}, ctx.Err()
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	r.inFlight[accessToken] = call
	r.mu.Unlock()

	finish := func(result refreshResult) (SessionRefreshData, error) {
		call.result = result
		close(call.done)
		r.mu.Lock()
		delete(r.inFlight, accessToken)
		r.mu.Unlock()
		return result.sessionData(), result.err
	}

	// Acquire the refresh concurrency slot.
	select {
	case r.semaphore <- struct{}{}:
		defer func() { <-r.semaphore }()
	case <-ctx.Done():
		return finish(refreshResult{err: ctx.Err()})
	}

	// Execute the refresh request.
	return finish(r.doRefresh(ctx, sessionToken, requestContext))
}

func (r refreshResult) sessionData() SessionRefreshData {
	return SessionRefreshData{
		AccessToken:  r.accessToken,
		SessionToken: r.sessionToken,
		Expires:      r.sessionExpires,
		User:         r.user,
		SetCookies:   r.setCookies,
		Clearance:    r.clearance,
	}
}

func hasSessionTokenCookie(cookies map[string]string) bool {
	for name, value := range cookies {
		if value != "" && (name == "__Secure-next-auth.session-token" || strings.HasPrefix(name, "__Secure-next-auth.session-token.")) {
			return true
		}
	}
	return false
}

func (r *SessionRefresher) doRefresh(ctx context.Context, sessionToken string, requestContext SessionRefreshContext) refreshResult {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()

	req := buildSessionRequest(ctx, sessionToken, requestContext, requestContext.Headers, requestContext.Cookies)
	resp, err := r.httpDo(req)
	if err != nil {
		return refreshResult{err: fmt.Errorf("http request: %w", err)}
	}
	body, readErr := readResponseBody(resp)
	if readErr != nil {
		return refreshResult{err: readErr}
	}

	// 这次续期是不是靠兜底过的，只有这里知道；成功路径也必须把它带出去，
	// 否则日志里看不出「这次刷新动了 FlareSolverr」。
	var clearance ClearanceOutcome
	if resp.StatusCode != http.StatusOK {
		// 挑战判定必须用完整响应（含响应头）：cf-mitigated 是 Cloudflare 自己的
		// 标记，比状态码准。只凭状态码会把普通业务 403 也当成挑战，白跑一次浏览器。
		challenged := IsClearanceChallengeResponse(resp) || util.IsCloudflareChallengeBody(strings.ToLower(string(body)))
		if !challenged {
			return refreshResult{err: sessionStatusError(resp.StatusCode, body)}
		}
		// 「撞上挑战却没有兜底」与「压根没撞上挑战」必须分得开：前者要去看 .env
		// 有没有配 FlareSolverr，后者什么都不用做，而两者的表现都是刷新失败。
		if requestContext.ClearanceFallback == nil {
			return refreshResult{clearance: ClearanceOutcome{Skipped: "clearance disabled"}, err: sessionStatusError(resp.StatusCode, body)}
		}
		// 兜底只在确属挑战时触发，绝不无条件跑：挑战不是靠重试能自愈的，
		// 换一份 cf_clearance 才有可能，而每次求解都要开一次真实浏览器。
		replayed, fallback, replayErr := r.replayWithFreshClearance(ctx, sessionToken, requestContext)
		clearance = fallback
		if replayErr != nil {
			return refreshResult{clearance: clearance, err: fmt.Errorf("session endpoint returned %d: %s (clearance fallback: %v)", resp.StatusCode, util.CloudflareChallengeMessage, replayErr)}
		}
		resp, body = replayed.resp, replayed.body
		if resp.StatusCode != http.StatusOK {
			return refreshResult{clearance: clearance, err: sessionStatusError(resp.StatusCode, body)}
		}
	}

	var session struct {
		AccessToken  string `json:"accessToken"`
		Expires      string `json:"expires"`
		SessionToken string `json:"sessionToken"`
		User         struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		return refreshResult{clearance: clearance, err: fmt.Errorf("parse session response: %w", err)}
	}

	if session.AccessToken == "" {
		return refreshResult{clearance: clearance, err: fmt.Errorf("session response missing accessToken")}
	}

	// Keep the previous sessionToken when the response omits a replacement.
	newSessionToken := session.SessionToken
	if newSessionToken == "" {
		newSessionToken = sessionToken
	}

	return refreshResult{
		accessToken:    session.AccessToken,
		sessionToken:   newSessionToken,
		sessionExpires: session.Expires,
		setCookies:     resp.Cookies(),
		clearance:      clearance,
		user: SessionRefreshUser{
			ID:    session.User.ID,
			Name:  session.User.Name,
			Email: session.User.Email,
		},
	}
}

// sessionStatusError 把非 200 的响应统一成可读错误。
//
// CF 挑战页是一大段 HTML，直接截断塞进错误里既不可读，也无法被上层识别为
// 「挑战」而非「账号问题」，因此统一成共享文案。
func sessionStatusError(status int, body []byte) error {
	if util.IsCloudflareChallengeBody(strings.ToLower(string(body))) {
		return fmt.Errorf("session endpoint returned %d: %s", status, util.CloudflareChallengeMessage)
	}
	preview := string(body)
	if len(preview) > 300 {
		preview = preview[:300]
	}
	return fmt.Errorf("session endpoint returned %d: %s", status, preview)
}

func readResponseBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

type sessionReplay struct {
	resp *http.Response
	body []byte
}

// replayWithFreshClearance 现取一份 cf_clearance，并用它自洽的身份重放一次刷新请求。
//
// 身份覆盖走 WithIdentityOverride 而不是在请求上 Set 头：surf 的 impersonate
// 中间件会在发送前把 UA 与 Sec-Ch-Ua 改回 profile 自己的值，直接 Set 会被静默丢弃。
// 而 cf_clearance 绑定签发时的 UA，只回注 cookie 不换 UA 会被上游判为凭证盗用。
//
// 出口由注入的兜底自己解析（账号绑定代理优先），这里不参与：用别的出口求解
// 等于拿到一张当场作废的凭证。
func (r *SessionRefresher) replayWithFreshClearance(ctx context.Context, sessionToken string, requestContext SessionRefreshContext) (sessionReplay, ClearanceOutcome, error) {
	solveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clearanceSolveTimeout)
	defer cancel()

	bundle, outcome, err := requestContext.ClearanceFallback(solveCtx)
	if err != nil {
		return sessionReplay{}, outcome, err
	}
	headers := requestContext.Headers
	cookies := requestContext.Cookies
	if len(bundle.Cookies) > 0 {
		cookies = mergeCookieMaps(cookies, bundle.ClearanceCookieValues())
	}
	// 身份覆盖必须同时走 context：surf 的 impersonate 中间件会在发送前把 UA 与
	// Sec-Ch-Ua 改回 profile 自己的值，只在这里 Set 头会被静默丢弃。直接 Set 保留
	// 一份是为了让不走 impersonate 的 client（测试、降级路径）也能带上同一套身份。
	replayCtx := ctx
	if override := ClearanceRequestHeaders(bundle); len(override) > 0 {
		headers = mergeStringMaps(headers, override)
		replayCtx = WithIdentityOverride(ctx, override)
	}

	req := buildSessionRequest(replayCtx, sessionToken, requestContext, headers, cookies)
	resp, err := r.httpDo(req)
	if err != nil {
		return sessionReplay{}, outcome, fmt.Errorf("http request: %w", err)
	}
	body, err := readResponseBody(resp)
	if err != nil {
		return sessionReplay{}, outcome, err
	}
	return sessionReplay{resp: resp, body: body}, outcome, nil
}

// buildSessionRequest 构造一次 /api/auth/session GET。
//
// 只重放一次、没有请求体，因此这里重建请求而不是克隆：克隆一个已发出的请求
// 还要处理 Body 是否可重放，GET 没有这层负担。URL 是常量，构造不会失败。
func buildSessionRequest(ctx context.Context, sessionToken string, requestContext SessionRefreshContext, headers, cookies map[string]string) *http.Request {
	ctx = WithAccountProxy(ctx, requestContext.Proxy)
	ctx = WithAccountProfile(ctx, requestContext.Profile)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sessionEndpoint, nil)
	if err != nil {
		// Only reachable when the constant endpoint is malformed.
		panic(fmt.Sprintf("build session request: %v", err))
	}

	req.Header.Set("User-Agent", DefaultBrowserUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	for name, value := range cookies {
		if value != "" {
			req.AddCookie(&http.Cookie{Name: name, Value: value, Domain: ".chatgpt.com", Path: "/", Secure: true})
		}
	}
	if !hasSessionTokenCookie(cookies) {
		req.AddCookie(&http.Cookie{
			Name:     "__Secure-next-auth.session-token",
			Value:    sessionToken,
			Domain:   ".chatgpt.com",
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	return req
}

func mergeStringMaps(base, override map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}

// mergeCookieMaps 合并 cookie，并丢弃被替换掉的分片。
//
// __Secure-next-auth.session-token 可能是分片的（.0/.1）：本地那份若与兜底
// 取回的不是同一次会话，两套分片同时发出会被上游判为凭证不一致。
func mergeCookieMaps(base, override map[string]string) map[string]string {
	merged := mergeStringMaps(base, override)
	for name := range override {
		prefix := name + "."
		for existing := range merged {
			if existing != name && strings.HasPrefix(existing, prefix) {
				delete(merged, existing)
			}
		}
	}
	return merged
}

// IsRefreshing reports whether the given token is being refreshed.
func (r *SessionRefresher) IsRefreshing(accessToken string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.inFlight[accessToken]
	return ok
}
