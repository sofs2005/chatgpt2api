package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"chatgpt2api/internal/util"
)

func ParseSessionCookies(input string) (map[string]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}

	var parsed any
	if err := json.Unmarshal([]byte(input), &parsed); err == nil {
		return SessionCookieStringMap(parsed), nil
	}

	cookies := map[string]string{}
	for _, part := range strings.Split(input, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if isAllowedSessionCookieName(name) && value != "" {
			cookies[name] = value
		}
	}
	if len(cookies) == 0 {
		return nil, fmt.Errorf("no supported ChatGPT cookies found")
	}
	return cookies, nil
}

func SessionCookieStringMap(raw any) map[string]string {
	cookies := map[string]string{}
	add := func(name, value string) {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if isAllowedSessionCookieName(name) && value != "" {
			cookies[name] = value
		}
	}

	switch input := raw.(type) {
	case map[string]string:
		for name, value := range input {
			add(name, value)
		}
	case map[string]any:
		for name, value := range input {
			add(name, util.Clean(value))
		}
	case []any:
		for _, item := range input {
			cookie, ok := item.(map[string]any)
			if !ok {
				continue
			}
			add(util.Clean(cookie["name"]), util.Clean(cookie["value"]))
		}
	}
	if len(cookies) == 0 {
		return nil
	}
	return cookies
}

// Cloudflare cookie 的时效语义并不相同，因此按用途分三类，各自使用独立窗口。
//
// 分类依据是「这个 cookie 丢了会怎样」：
//   - 挑战凭证（cf_clearance、cf_chl_*）：CF 挑战通过后签发的通行证。丢了必然 403，
//     所以窗口必须贴近 CF 的实际有效期，不能按分钟级丢弃。
//   - 短期令牌（__cf_bm）：Bot Management 令牌，生命周期本就只有约 30 分钟。
//   - 长期标识（_cfuvid、__cflb）：访客唯一 ID 与负载均衡亲和性，属于稳定标识，
//     本就不该过期；丢弃反而让服务端看起来像「从未出现过的全新访客」。
const (
	// cloudflareClearanceWindow 是挑战凭证在「账号未绑定固定出口 IP」时的保守窗口。
	// 此时出口 IP 可能漂移，而 cf_clearance 强绑定签发时的 IP，过期凭证会反向触发风控。
	cloudflareClearanceWindow = 2 * time.Hour
	// cloudflareTokenWindow 是 __cf_bm 这类短期令牌的窗口，对齐 CF 自身的生命周期。
	cloudflareTokenWindow = 30 * time.Minute
)

// AccountSessionCookiesForRequest 返回该账号本次请求应当携带的 session cookie。
//
// 挑战凭证类 cookie 的时间窗口只在账号「没有绑定固定出口 IP」时才生效：
// 绑定了账号级代理意味着出口 IP 稳定，cf_clearance 的 IP 前提成立，此时不应再按时间丢弃。
func AccountSessionCookiesForRequest(account map[string]any, now time.Time) map[string]string {
	cookies := SessionCookieStringMap(account["session_cookies"])
	if len(cookies) == 0 {
		return nil
	}
	updatedAt := SessionCookieStringMap(account["session_cookie_updated_at"])
	stableExitIP := accountHasStableExitIP(account)
	filtered := map[string]string{}
	for name, value := range cookies {
		window, limited := cloudflareCookieFreshWindow(name, stableExitIP)
		if limited {
			updated, err := time.Parse(time.RFC3339, updatedAt[name])
			if err != nil || now.Sub(updated) > window {
				continue
			}
		}
		filtered[name] = value
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// cloudflareCookieFreshWindow 返回该 cookie 适用的新鲜度窗口。
// limited 为 false 表示该 cookie 不受时间窗口约束，总是发送。
func cloudflareCookieFreshWindow(name string, stableExitIP bool) (time.Duration, bool) {
	switch name {
	case "cf_clearance":
		if stableExitIP {
			return 0, false
		}
		return cloudflareClearanceWindow, true
	case "__cf_bm":
		return cloudflareTokenWindow, true
	case "_cfuvid", "__cflb":
		// 长期访客标识与负载均衡亲和性，丢弃只会让身份更像新访客。
		return 0, false
	}
	switch {
	case strings.HasPrefix(name, "cf_chl_"):
		if stableExitIP {
			return 0, false
		}
		return cloudflareClearanceWindow, true
	case strings.HasPrefix(name, "__cf"):
		return cloudflareTokenWindow, true
	}
	return 0, false
}

// accountHasStableExitIP 判断账号是否绑定了固定的出口代理。
// 绑定后 cf_clearance 的 IP 前提成立，可以长期复用而无需按时间丢弃。
func accountHasStableExitIP(account map[string]any) bool {
	return util.Clean(account["proxy"]) != ""
}

func SessionCookieUpdatedAtForCookies(cookies map[string]string, now time.Time) map[string]string {
	updatedAt := map[string]string{}
	stamp := now.UTC().Format(time.RFC3339)
	for name := range cookies {
		if isCloudflareSessionCookieName(name) {
			updatedAt[name] = stamp
		}
	}
	if len(updatedAt) == 0 {
		return nil
	}
	return updatedAt
}

// isCloudflareSessionCookieName 判断 cookie 是否属于 Cloudflare 命名空间。
// 它只回答「要不要打时间戳」，具体窗口由 cloudflareCookieFreshWindow 决定。
func isCloudflareSessionCookieName(name string) bool {
	return name == "cf_clearance" ||
		name == "__cf_bm" ||
		name == "__cflb" ||
		name == "_cfuvid" ||
		strings.HasPrefix(name, "cf_chl_") ||
		strings.HasPrefix(name, "__cf")
}

func isAllowedSessionCookieName(name string) bool {
	switch name {
	case "__Secure-next-auth.session-token",
		"__Secure-next-auth.callback-url",
		"__Host-next-auth.csrf-token",
		"cf_clearance",
		"__cf_bm",
		"__cflb",
		"_cfuvid",
		"_puid",
		"_account_is_fedramp",
		"oai-did",
		"oai-sc",
		"oai-chat-web-route",
		"oai-client-auth-info",
		"oai-gn",
		"oai-hlib",
		"__Secure-oai-is":
		return true
	}
	return strings.HasPrefix(name, "__Secure-next-auth.session-token.") ||
		strings.HasPrefix(name, "cf_chl_") ||
		strings.HasPrefix(name, "__cf")
}
