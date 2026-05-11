package util

import "strings"

const UpstreamConnectionFailureMessage = "upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy"

func SummarizeUpstreamConnectionError(message string) (string, bool) {
	text := strings.TrimSpace(message)
	if text == "" {
		return "", false
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, strings.ToLower(UpstreamConnectionFailureMessage)) ||
		strings.Contains(lower, "utls.handshakecontext") ||
		strings.Contains(lower, "http/2 request failed") ||
		strings.Contains(lower, "http/1.1 fallback failed") ||
		strings.Contains(lower, "tls connect error") ||
		strings.Contains(lower, "openssl_internal") ||
		strings.Contains(lower, "curl: (35)") ||
		((strings.Contains(lower, "tls") || strings.Contains(lower, "handshake")) && strings.Contains(lower, "eof")) {
		return UpstreamConnectionFailureMessage, true
	}
	return "", false
}
