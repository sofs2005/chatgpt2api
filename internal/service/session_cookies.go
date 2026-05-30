package service

import (
	"encoding/json"
	"fmt"
	"strings"

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

func isAllowedSessionCookieName(name string) bool {
	switch name {
	case "__Secure-next-auth.session-token", "__Host-next-auth.csrf-token", "cf_clearance", "__cf_bm", "oai-did", "oai-sc", "__Secure-oai-is":
		return true
	}
	return strings.HasPrefix(name, "__Secure-next-auth.session-token.") ||
		strings.HasPrefix(name, "cf_chl_") ||
		strings.HasPrefix(name, "__cf")
}
