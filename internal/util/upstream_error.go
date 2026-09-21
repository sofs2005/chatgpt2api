package util

import "strings"

const UpstreamConnectionFailureMessage = "upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy"

// UpstreamProxyUnreachableMessage 描述 TCP 层就没连上代理的情形。
// 它与 TLS 握手失败是两类问题：前者是代理地址/端口或代理进程本身不可用，
// 后者才需要排查证书与指纹。混用会把排查方向指向错误的地方。
const UpstreamProxyUnreachableMessage = "upstream proxy refused the connection (connection refused); check the proxy address/port is correct and the proxy is running"

// CloudflareChallengeMessage 是 Cloudflare 挑战页的统一对外文案。
const CloudflareChallengeMessage = "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy"

// IsCloudflareChallengeBody 判断响应体是否是 Cloudflare 挑战页。
//
// 这是唯一的判定入口：此前该特征匹配在 backend / service / protocol 三处各写一份，
// 新增特征时必须同步多处，容易漏改。所有调用方都应使用这里。
// 传入的应当是已小写的响应体文本。
//
// 这里允许裸 "cloudflare" 命中：挑战页 HTML 本身包含大量 cloudflare 标识，
// 而响应体不会嵌入代理地址等无关文本。
func IsCloudflareChallengeBody(lower string) bool {
	return strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare")
}

// IsCloudflareChallengeMessage 判断错误文案是否表示 Cloudflare 挑战拦截。
// 用于重试决策：挑战拦截可以靠换账号（换一套 cookie 与指纹）自愈。
//
// 与 IsCloudflareChallengeBody 的区别：这里刻意不匹配裸 "cloudflare"，
// 因为错误文案里可能嵌入代理主机名等无关文本（例如 proxy.cloudflare.example），
// 裸匹配会把普通失败误判成挑战拦截，导致无谓的换号重试。
func IsCloudflareChallengeMessage(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, CloudflareChallengeMessage) {
		return true
	}
	return strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare challenge")
}

// UpstreamConnectionFailureClass 是 transport 错误的归类结果。
type UpstreamConnectionFailureClass int

const (
	UpstreamConnectionNone UpstreamConnectionFailureClass = iota
	// UpstreamConnectionProxyUnreachable 表示 TCP 层未建立连接：端口拒绝、路由不可达。
	// 这属于配置问题，重试不会自愈。
	UpstreamConnectionProxyUnreachable
	// UpstreamConnectionTLSHandshake 表示 TCP 已建立但 TLS 握手失败。
	UpstreamConnectionTLSHandshake
)

// ClassifyUpstreamConnectionError 判断 transport 错误属于哪一类。
// 判定顺序很重要：surf 的 `http/2 request failed` / `http/1.1 fallback failed`
// 会同时出现在 TCP 失败与 TLS 失败的错误里，因此必须先识别 dial 层失败，
// 否则代理端口拒绝连接会被误报成 TLS 握手问题。
func ClassifyUpstreamConnectionError(message string) UpstreamConnectionFailureClass {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return UpstreamConnectionNone
	}
	if isProxyUnreachableError(lower) {
		return UpstreamConnectionProxyUnreachable
	}
	if strings.Contains(lower, strings.ToLower(UpstreamConnectionFailureMessage)) ||
		strings.Contains(lower, "utls.handshakecontext") ||
		strings.Contains(lower, "http/2 request failed") ||
		strings.Contains(lower, "http/1.1 fallback failed") ||
		strings.Contains(lower, "tls connect error") ||
		strings.Contains(lower, "openssl_internal") ||
		strings.Contains(lower, "curl: (35)") ||
		((strings.Contains(lower, "tls") || strings.Contains(lower, "handshake")) && strings.Contains(lower, "eof")) {
		return UpstreamConnectionTLSHandshake
	}
	return UpstreamConnectionNone
}

func isProxyUnreachableError(lower string) bool {
	// 已归一化的文案要能被再次识别，否则二次分类会退回 TLS 分支。
	if strings.Contains(lower, strings.ToLower(UpstreamProxyUnreachableMessage)) {
		return true
	}
	for _, token := range []string{
		"connection refused",
		"no route to host",
		"network is unreachable",
		"host is unreachable",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return strings.Contains(lower, "dial tcp") && strings.Contains(lower, "connect:")
}

func SummarizeUpstreamConnectionError(message string) (string, bool) {
	switch ClassifyUpstreamConnectionError(message) {
	case UpstreamConnectionProxyUnreachable:
		return UpstreamProxyUnreachableMessage, true
	case UpstreamConnectionTLSHandshake:
		return UpstreamConnectionFailureMessage, true
	default:
		return "", false
	}
}
