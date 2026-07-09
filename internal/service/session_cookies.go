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

const cloudflareCookieFreshWindow = 30 * time.Minute

func AccountSessionCookiesForRequest(account map[string]any, now time.Time) map[string]string {
	cookies := SessionCookieStringMap(account["session_cookies"])
	if len(cookies) == 0 {
		return nil
	}
	updatedAt := SessionCookieStringMap(account["session_cookie_updated_at"])
	filtered := map[string]string{}
	for name, value := range cookies {
		if isCloudflareSessionCookieName(name) {
			updated, err := time.Parse(time.RFC3339, updatedAt[name])
			if err != nil || now.Sub(updated) > cloudflareCookieFreshWindow {
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
