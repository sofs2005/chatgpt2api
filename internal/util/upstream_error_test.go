package util

import "testing"

func TestSummarizeUpstreamConnectionError(t *testing.T) {
	cases := []string{
		`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`,
		"curl: (35) OpenSSL SSL_connect: SSL_ERROR_SYSCALL",
		"TLS connect error: connection reset by peer",
		"error: OPENSSL_INTERNAL:WRONG_VERSION_NUMBER",
	}
	for _, input := range cases {
		got, ok := SummarizeUpstreamConnectionError(input)
		if !ok {
			t.Fatalf("SummarizeUpstreamConnectionError(%q) did not match", input)
		}
		if got != UpstreamConnectionFailureMessage {
			t.Fatalf("summary = %q, want %q", got, UpstreamConnectionFailureMessage)
		}
	}

	if got, ok := SummarizeUpstreamConnectionError("upstream returned 500"); ok || got != "" {
		t.Fatalf("non-connection summary = %q, %v", got, ok)
	}
}

// surf 会把 dial 层失败和 TLS 失败都包成 http/2 request failed / HTTP/1.1 fallback failed，
// 因此必须先识别代理不可达，否则会被误报成 TLS 握手问题并触发无意义重试。
func TestSummarizeUpstreamConnectionErrorClassifiesProxyUnreachable(t *testing.T) {
	cases := []string{
		`socks connect tcp 10.6.6.90:1070->chatgpt.com:443: dial tcp 10.6.6.90:1070: connect: connection refused; ` +
			`surf: HTTP/2 request failed: socks connect tcp 10.6.6.90:1070->chatgpt.com:443: connect: connection refused`,
		`Get "https://chatgpt.com/": surf: HTTP/2 request failed: dial tcp 127.0.0.1:7890: connect: connection refused`,
		"dial tcp 10.0.0.9:1080: connect: no route to host",
		"network is unreachable",
	}
	for _, input := range cases {
		if got := ClassifyUpstreamConnectionError(input); got != UpstreamConnectionProxyUnreachable {
			t.Fatalf("ClassifyUpstreamConnectionError(%q) = %v, want proxy unreachable", input, got)
		}
		summary, ok := SummarizeUpstreamConnectionError(input)
		if !ok || summary != UpstreamProxyUnreachableMessage {
			t.Fatalf("SummarizeUpstreamConnectionError(%q) = %q, %v; want proxy message", input, summary, ok)
		}
	}

	// 归类结果必须稳定：对已归一化的文案再分类不能退回 TLS 分支。
	if got := ClassifyUpstreamConnectionError(UpstreamProxyUnreachableMessage); got != UpstreamConnectionProxyUnreachable {
		t.Fatalf("re-classifying normalized proxy message = %v, want proxy unreachable", got)
	}
	if got := ClassifyUpstreamConnectionError(UpstreamConnectionFailureMessage); got != UpstreamConnectionTLSHandshake {
		t.Fatalf("re-classifying normalized TLS message = %v, want TLS handshake", got)
	}
	if got := ClassifyUpstreamConnectionError("upstream returned 500"); got != UpstreamConnectionNone {
		t.Fatalf("unrelated error classified as %v, want none", got)
	}
}
