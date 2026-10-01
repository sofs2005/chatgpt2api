package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/util"
)

// ClearanceTargetHost 是 cf_clearance 的签发目标。
// cf_clearance 绑定签发时的出口 IP 与 User-Agent，换其一即失效，
// 因此缓存键必须同时包含出口与目标站点。
const ClearanceTargetHost = "chatgpt.com"

const defaultClearanceTTL = time.Hour

// clearanceCookieNames 是 FlareSolverr 解挑战后允许回注的 cookie。
//
// 只收 Cloudflare 自己签发的凭证：账号级应用 cookie（oai-sc、
// __Secure-next-auth.*）由正常请求链路维护，从浏览器容器顺带捞回来会引入
// 与账号本地状态冲突的副本。
var clearanceCookieNames = map[string]bool{
	"cf_clearance": true,
	"__cf_bm":      true,
	"_cfuvid":      true,
	"__cflb":       true,
}

// IsClearanceChallengeResponse 判断响应是否是一次 Cloudflare 挑战拦截。
//
// 只凭状态码会把普通的业务 403 也当成挑战，白跑一次浏览器求解；因此优先采信
// Cloudflare 自己的标记（cf-mitigated），没有标记时再回落到状态码。
func IsClearanceChallengeResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("cf-mitigated")), "challenge") {
		return true
	}
	return util.IsCloudflareChallengeStatus(resp.StatusCode)
}

// ClearanceConfig 是 cf_clearance 兜底所需配置。
type ClearanceConfig interface {
	ClearanceEnabled() bool
	FlareSolverrURL() string
	ClearanceTimeoutSeconds() int
	ClearanceTTLSeconds() int
}

// ClearanceBundle 是一次挑战求解的结果。
//
// UA 必须随 Cookies 一起使用：cf_clearance 绑定签发时的 User-Agent，
// 只回注 cookie 而继续用原来的 UA 会被判定为凭证盗用。
type ClearanceBundle struct {
	UA         string
	Cookies    map[string]string
	ProxyURL   string
	TargetHost string
	ExpiresAt  time.Time
}

// ClearanceService 通过 FlareSolverr 主动获取 cf_clearance。
//
// 本仓库原本只被动收集响应里的 Set-Cookie，挑战命中时没有兜底手段；
// 这里补上「真实浏览器代打」这条路径。
type ClearanceService struct {
	config ClearanceConfig
	client *http.Client

	mu       sync.Mutex
	bundles  map[string]clearanceEntry
	inflight map[string]*clearanceCall
}

type clearanceEntry struct {
	bundle ClearanceBundle
}

type clearanceCall struct {
	done   chan struct{}
	bundle ClearanceBundle
	err    error
}

func NewClearanceService(config ClearanceConfig) *ClearanceService {
	return &ClearanceService{
		config:   config,
		client:   &http.Client{Timeout: 60 * time.Second},
		bundles:  map[string]clearanceEntry{},
		inflight: map[string]*clearanceCall{},
	}
}

// Enabled 报告 clearance 兜底是否开启；未配置 FlareSolverr 地址时视为关闭。
func (s *ClearanceService) Enabled() bool {
	if s == nil || s.config == nil || !s.config.ClearanceEnabled() {
		return false
	}
	return strings.TrimSpace(s.config.FlareSolverrURL()) != ""
}

func (s *ClearanceService) ttl() time.Duration {
	if s == nil || s.config == nil {
		return defaultClearanceTTL
	}
	seconds := s.config.ClearanceTTLSeconds()
	if seconds <= 0 {
		return defaultClearanceTTL
	}
	return time.Duration(seconds) * time.Second
}

func clearanceCacheKey(proxyURL, host string) string {
	return strings.TrimSpace(proxyURL) + "|" + strings.TrimSpace(host)
}

// Cached 返回未过期的缓存凭证；不存在或已过期时返回 false。
func (s *ClearanceService) Cached(proxyURL, host string) (ClearanceBundle, bool) {
	if s == nil {
		return ClearanceBundle{}, false
	}
	key := clearanceCacheKey(proxyURL, host)
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.bundles[key]
	if !ok {
		return ClearanceBundle{}, false
	}
	if !entry.bundle.ExpiresAt.IsZero() && time.Now().After(entry.bundle.ExpiresAt) {
		delete(s.bundles, key)
		return ClearanceBundle{}, false
	}
	return entry.bundle, true
}

// Invalidate 作废某个出口的凭证。
//
// 出口 IP 变化后旧 cf_clearance 必然失效，必须显式丢弃，
// 否则会拿着废凭证反复重试，反而放大风控信号。
func (s *ClearanceService) Invalidate(proxyURL, host string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.bundles, clearanceCacheKey(proxyURL, host))
	s.mu.Unlock()
}

// ClearanceOutcome 描述一次 cf_clearance 兜底的结果。
//
// 兜底有三个成本与含义都不同的分支：未启用（没配 FlareSolverr）、命中缓存（复用
// 未过期的凭证）、现解一次（真的开了浏览器）。三者最终都表现为「还是 Cloudflare
// 挑战」，但处置方式完全不同，只报「兜底失败」等于什么也没说。
type ClearanceOutcome struct {
	// Attempted 为真表示确实动用了兜底（含命中缓存）。
	Attempted bool
	// Solved 为真表示这次现解了一次，即 FlareSolverr 真的被调用且求解成功。
	Solved bool
	// Skipped 记录「撞上挑战却没有兜底」的原因；为空表示不存在这种情况。
	// 它与 Attempted=false 不是一回事：后者可能只是根本没触发过挑战。
	Skipped string
	// Proxy 是本次求解使用的出口，已脱敏。cf_clearance 绑定签发 IP，
	// 排查时必须知道解的是哪个出口。
	Proxy string
	// Error 是求解失败的原因；为空表示求解成功或未求解。
	Error string
}

// CachedClearance 报告该出口是否已有未过期的 cf_clearance。
//
// 命中缓存与现解一次对调用方是两种完全不同的成本：前者是一次内存查找，后者要经
// FlareSolverr 开真实浏览器、可能数秒到数十秒。不区分就无从判断兜底到底有没有
// 真的落到浏览器上。
func (s *ClearanceService) CachedClearance(proxyURL string) (ClearanceBundle, bool) {
	return s.Cached(proxyURL, ClearanceTargetHost)
}

// RefreshWithOutcome 与 Refresh 等价，额外回报这次是否真的向 FlareSolverr 求解。
//
// 缓存判定刻意放在 Refresh 之前：Refresh 内部也会查缓存，但调用方需要知道命中的
// 是哪一条路径，而单看它的返回值无法区分「缓存命中」与「现解成功」。
func (s *ClearanceService) RefreshWithOutcome(ctx context.Context, proxyURL string) (ClearanceBundle, ClearanceOutcome, error) {
	outcome := ClearanceOutcome{Proxy: MaskProxyURL(proxyURL)}
	if !s.Enabled() {
		return ClearanceBundle{}, outcome, fmt.Errorf("clearance disabled")
	}
	_, cached := s.CachedClearance(proxyURL)
	bundle, err := s.Refresh(ctx, proxyURL)
	outcome.Attempted = true
	if err != nil {
		outcome.Error = err.Error()
		return bundle, outcome, err
	}
	// 未命中缓存却拿到了结果，说明这次真的求解了一次；命中缓存则只是复用。
	outcome.Solved = !cached
	return bundle, outcome, nil
}

// Refresh 获取（或复用）指定出口的 cf_clearance。
//
// 同一出口只允许一次求解在途：挑战往往成波出现，若每个请求都去打
// FlareSolverr，会把它变成新的瓶颈，也会因为并发开浏览器而拖慢整条链路。
func (s *ClearanceService) Refresh(ctx context.Context, proxyURL string) (ClearanceBundle, error) {
	if !s.Enabled() {
		return ClearanceBundle{}, fmt.Errorf("clearance disabled")
	}
	host := ClearanceTargetHost
	key := clearanceCacheKey(proxyURL, host)
	if bundle, ok := s.Cached(proxyURL, host); ok {
		return bundle, nil
	}
	s.mu.Lock()
	// 双检：拿到锁之前可能已有同键求解完成。
	if entry, ok := s.bundles[key]; ok {
		if entry.bundle.ExpiresAt.IsZero() || time.Now().Before(entry.bundle.ExpiresAt) {
			s.mu.Unlock()
			return entry.bundle, nil
		}
		delete(s.bundles, key)
	}
	if call, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.bundle, call.err
		case <-ctx.Done():
			return ClearanceBundle{}, ctx.Err()
		}
	}
	call := &clearanceCall{done: make(chan struct{})}
	s.inflight[key] = call
	s.mu.Unlock()

	bundle, err := s.solve(ctx, proxyURL, host)

	s.mu.Lock()
	if err == nil {
		s.bundles[key] = clearanceEntry{bundle: bundle}
	}
	delete(s.inflight, key)
	s.mu.Unlock()

	call.bundle, call.err = bundle, err
	close(call.done)
	return bundle, err
}

// solve 调用 FlareSolverr 解一次挑战。
func (s *ClearanceService) solve(ctx context.Context, proxyURL, host string) (ClearanceBundle, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(s.config.FlareSolverrURL()), "/") + "/v1"
	timeout := 60 * time.Second
	if seconds := s.config.ClearanceTimeoutSeconds(); seconds > 0 {
		timeout = time.Duration(seconds) * time.Second
	}
	payload := map[string]any{
		"cmd":        "request.get",
		"url":        "https://" + host + "/",
		"maxTimeout": timeout.Milliseconds(),
	}
	// 必须走与触发账号相同的出口：cf_clearance 绑定签发 IP，
	// 从别的出口拿到的凭证放到该账号上等于当场作废。
	if trimmed := strings.TrimSpace(proxyURL); trimmed != "" {
		payload["proxy"] = map[string]any{"url": trimmed}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ClearanceBundle{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ClearanceBundle{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return ClearanceBundle{}, fmt.Errorf("flaresolverr request failed: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ClearanceBundle{}, fmt.Errorf("flaresolverr status=%d", resp.StatusCode)
	}
	return parseClearanceSolution(data, proxyURL, host, s.ttl(), time.Now())
}

// parseClearanceSolution 解析 FlareSolverr 的 /v1 响应。
func parseClearanceSolution(data []byte, proxyURL, host string, ttl time.Duration, now time.Time) (ClearanceBundle, error) {
	var payload struct {
		Status   string `json:"status"`
		Message  string `json:"message"`
		Solution struct {
			UserAgent string `json:"userAgent"`
			Cookies   []struct {
				Name    string  `json:"name"`
				Value   string  `json:"value"`
				Expires float64 `json:"expires"`
			} `json:"cookies"`
		} `json:"solution"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ClearanceBundle{}, fmt.Errorf("flaresolverr response is not JSON: %w", err)
	}
	if status := strings.ToLower(strings.TrimSpace(payload.Status)); status != "ok" {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "unknown"
		}
		return ClearanceBundle{}, fmt.Errorf("flaresolverr status=%s: %s", payload.Status, message)
	}
	cookies := map[string]string{}
	// 取所有 CF cookie 里最早的有效期作为缓存上限，避免用中途过期的凭证重试。
	earliest := now.Add(ttl)
	for _, cookie := range payload.Solution.Cookies {
		name := strings.TrimSpace(cookie.Name)
		if !clearanceCookieNames[name] || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		cookies[name] = cookie.Value
		if cookie.Expires > 0 {
			expiry := time.Unix(int64(cookie.Expires), 0)
			if expiry.Before(earliest) {
				earliest = expiry
			}
		}
	}
	if len(cookies) == 0 {
		return ClearanceBundle{}, fmt.Errorf("flaresolverr returned no cloudflare cookies")
	}
	bundle := ClearanceBundle{
		UA:         strings.TrimSpace(payload.Solution.UserAgent),
		Cookies:    cookies,
		ProxyURL:   strings.TrimSpace(proxyURL),
		TargetHost: host,
		ExpiresAt:  earliest,
	}
	if !bundle.ExpiresAt.After(now) {
		return ClearanceBundle{}, fmt.Errorf("flaresolverr returned an already expired clearance")
	}
	return bundle, nil
}

// MaskProxyURL 对出口地址做脱敏，日志与状态接口只能看到这个形式。
func MaskProxyURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "***"
	}
	host := parsed.Host
	if parsed.User != nil {
		host = "***@" + host
	}
	return parsed.Scheme + "://" + host
}

// MaskProxyList 对一组出口地址做脱敏，供管理接口回显。
func MaskProxyList(list []string) []string {
	if len(list) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, MaskProxyURL(item))
	}
	return out
}

// ClearanceCookieValues 返回可直接注入 Cookie 头的凭证副本。
func (b ClearanceBundle) ClearanceCookieValues() map[string]string {
	out := map[string]string{}
	for name, value := range b.Cookies {
		if util.Clean(value) != "" {
			out[name] = value
		}
	}
	return out
}
