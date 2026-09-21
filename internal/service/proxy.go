package service

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"chatgpt2api/internal/util"

	"github.com/enetx/g"
	"github.com/enetx/surf"
)

type ProxyConfig interface {
	Proxy() string
}

// BrowserAcceptLanguage 是出站请求统一的 Accept-Language。
//
// 真实浏览器的 Accept-Language 来自浏览器语言设置，对同一份浏览器身份的所有请求
// 取值相同。它必须与 OAI-Language、PoW 配置里的 navigator.language 保持一致，
// 否则同一份身份会自报不同语言，是风控可识别的矛盾信号。
//
// surf 的 Impersonate() 会把 Accept-Language 硬编码成 en-US，且它的请求中间件
// 优先级为 0，晚于调用方设置的头。因此这里在更高优先级上再写回统一值，
// 保证「我们自己设的语言」最终生效。
const BrowserAcceptLanguage = "zh-CN,zh;q=0.9,en;q=0.8"

type ProxyService struct {
	config ProxyConfig
}

func NewProxyService(config ProxyConfig) *ProxyService {
	return &ProxyService{config: config}
}

func HTTPClientForProxy(proxy string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: transportForProxy(proxy)}
}

func (s *ProxyService) HTTPClient(timeout time.Duration) *http.Client {
	return HTTPClientForProxy(s.config.Proxy(), timeout)
}

func (s *ProxyService) BrowserHTTPClient(timeout time.Duration) *http.Client {
	return browserHTTPClient(s.config.Proxy(), timeout)
}

// AccountProxy 返回账号绑定的代理；未绑定时返回空串，表示使用全局代理。
func AccountProxy(account map[string]any) string {
	if account == nil {
		return ""
	}
	return util.Clean(account["proxy"])
}

// BrowserHTTPClientForProxy 用显式代理构建浏览器指纹 client。
// proxy 为空（账号未绑定）时回落到全局代理，保持既有部署行为不变。
func (s *ProxyService) BrowserHTTPClientForProxy(proxy, profile string, timeout time.Duration) *http.Client {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" && s != nil && s.config != nil {
		proxy = s.config.Proxy()
	}
	return browserHTTPClientForProfile(proxy, profile, timeout)
}

// BrowserHTTPClientForAccount 为账号构建浏览器指纹 client。
//
// 账号绑定了自己的代理时必须优先使用它：cf_clearance 与签发时的出口 IP 强绑定，
// 出口 IP 漂移会让凭证失效并反向触发 Cloudflare 风控。绑定代理让出口 IP 稳定，
// 是复用 cf_clearance 的前提。
//
// 账号的**所有**出站请求都必须走同一个 IP：session 刷新携带的正是该账号的
// cf_clearance，若它从全局代理发出，凭证会因 IP 不符当场作废。
func (s *ProxyService) BrowserHTTPClientForAccount(account map[string]any, profile string, timeout time.Duration) *http.Client {
	return s.BrowserHTTPClientForProxy(AccountProxy(account), profile, timeout)
}

// accountProxyKey 是请求上下文里承载账号级代理的键。
//
// session 刷新的 httpDo 只能看到 *http.Request，拿不到 access token，
// 因此账号绑定的代理随请求一起传递，由发请求的一方从上下文取出。
type accountProxyKey struct{}

// WithAccountProxy 把账号级代理绑定到请求上下文；代理为空时原样返回。
func WithAccountProxy(ctx context.Context, proxy string) context.Context {
	if ctx == nil || strings.TrimSpace(proxy) == "" {
		return ctx
	}
	return context.WithValue(ctx, accountProxyKey{}, strings.TrimSpace(proxy))
}

// AccountProxyFromContext 取出请求上下文里的账号级代理；未绑定时返回空串。
func AccountProxyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(accountProxyKey{}).(string)
	return value
}

func (s *ProxyService) Test(candidate string, timeout time.Duration) map[string]any {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		candidate = s.config.Proxy()
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return map[string]any{"ok": false, "status": 0, "latency_ms": 0, "error": "proxy url is required"}
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") {
		return map[string]any{"ok": false, "status": 0, "latency_ms": 0, "error": "invalid proxy url"}
	}
	client := browserHTTPClientForProfile(candidate, "", timeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/", nil)
	for key, value := range BrowserHeadersForFingerprint(nil) {
		req.Header.Set(key, value)
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		message := err.Error()
		if detail, ok := util.SummarizeUpstreamConnectionError(message); ok {
			message = detail
		}
		return map[string]any{"ok": false, "status": 0, "latency_ms": latency, "error": message}
	}
	defer resp.Body.Close()
	ok := resp.StatusCode < 500
	var message any
	if !ok {
		message = resp.Status
	}
	return map[string]any{"ok": ok, "status": resp.StatusCode, "latency_ms": latency, "error": message}
}

func browserHTTPClient(proxy string, timeout time.Duration) *http.Client {
	return browserHTTPClientForProfile(proxy, "", timeout)
}

func browserHTTPClientForProfile(proxy, profile string, timeout time.Duration) *http.Client {
	builder := surf.NewClient().
		Builder().
		SecureTLS()
	builder = applyBrowserProfile(builder, profile).
		Session().
		Timeout(timeout)

	// Impersonate() 的请求中间件优先级为 0，会把 Accept-Language 固定成 en-US。
	// 用优先级 1 的中间件在其之后写回统一语言，使出站语言与 OAI-Language、
	// PoW 的 navigator.language 保持一致，避免同一身份自报不同语言。
	builder = builder.With(func(req *surf.Request) error {
		req.GetRequest().Header.Set("Accept-Language", BrowserAcceptLanguage)
		return nil
	}, 1)

	if proxy = strings.TrimSpace(proxy); proxy != "" {
		builder = builder.Proxy(g.String(proxy))
	}

	client, err := builder.Build().Result()
	if err != nil {
		return &http.Client{Timeout: timeout, Transport: transportForProxy(proxy)}
	}
	return client.Std()
}

func applyBrowserProfile(builder *surf.Builder, profile string) *surf.Builder {
	impersonate := builder.Impersonate()
	normalized := strings.ToLower(strings.TrimSpace(profile))
	switch {
	case strings.Contains(normalized, "android"):
		impersonate = impersonate.Android()
	case strings.Contains(normalized, "ios"), strings.Contains(normalized, "iphone"), strings.Contains(normalized, "ipad"):
		impersonate = impersonate.IOS()
	case strings.Contains(normalized, "mac"), strings.Contains(normalized, "darwin"):
		impersonate = impersonate.MacOS()
	case strings.Contains(normalized, "linux"):
		impersonate = impersonate.Linux()
	default:
		impersonate = impersonate.Windows()
	}
	if strings.Contains(normalized, "firefox") || strings.Contains(normalized, "ff") {
		return impersonate.Firefox()
	}
	return impersonate.Chrome()
}

func transportForProxy(candidate string) *http.Transport {
	transport := baseTransport()
	if candidate == "" {
		return transport
	}
	proxyURL, err := url.Parse(candidate)
	if err != nil || proxyURL.Host == "" {
		return transport
	}
	return transportForProxyURL(proxyURL)
}

func transportForProxyURL(proxyURL *url.URL) *http.Transport {
	transport := baseTransport()
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(proxyURL)
	case "socks5", "socks5h":
		transport.Proxy = nil
		transport.DialContext = socks5DialContext(proxyURL)
	}
	return transport
}

func baseTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

func socks5DialContext(proxyURL *url.URL) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := &net.Dialer{}
		conn, err := dialer.DialContext(ctx, network, proxyURL.Host)
		if err != nil {
			return nil, err
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
			defer func() {
				_ = conn.SetDeadline(time.Time{})
			}()
		}
		if err := socks5Handshake(ctx, conn, proxyURL, address); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
}

func socks5Handshake(ctx context.Context, conn net.Conn, proxyURL *url.URL, target string) error {
	methods := []byte{0x00}
	username := ""
	password := ""
	if proxyURL.User != nil {
		username = proxyURL.User.Username()
		password, _ = proxyURL.User.Password()
		if len(username) > 255 || len(password) > 255 {
			return fmt.Errorf("socks credentials are too long")
		}
		methods = append(methods, 0x02)
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(conn, response); err != nil {
		return err
	}
	if response[0] != 0x05 {
		return fmt.Errorf("invalid socks version %d", response[0])
	}
	switch response[1] {
	case 0x00:
	case 0x02:
		if username == "" && password == "" {
			return fmt.Errorf("socks proxy requires username/password authentication")
		}
		auth := []byte{0x01, byte(len(username))}
		auth = append(auth, []byte(username)...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, []byte(password)...)
		if _, err := conn.Write(auth); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, response); err != nil {
			return err
		}
		if response[1] != 0x00 {
			return fmt.Errorf("socks authentication failed")
		}
	default:
		return fmt.Errorf("socks proxy rejected authentication methods")
	}
	address, err := socks5Address(ctx, proxyURL.Scheme, target)
	if err != nil {
		return err
	}
	request := []byte{0x05, 0x01, 0x00}
	request = append(request, address...)
	if _, err := conn.Write(request); err != nil {
		return err
	}
	return readSocks5ConnectResponse(conn)
}

func socks5Address(ctx context.Context, scheme, target string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid target port %q", portText)
	}
	var out []byte
	if strings.EqualFold(scheme, "socks5h") {
		if len(host) > 255 {
			return nil, fmt.Errorf("target host is too long")
		}
		out = append(out, 0x03, byte(len(host)))
		out = append(out, []byte(host)...)
	} else if ip := net.ParseIP(host); ip != nil {
		out = appendSOCKSIP(out, ip)
	} else {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no address found for %s", host)
		}
		out = appendSOCKSIP(out, ips[0].IP)
	}
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	out = append(out, portBytes[:]...)
	return out, nil
}

func appendSOCKSIP(out []byte, ip net.IP) []byte {
	if v4 := ip.To4(); v4 != nil {
		out = append(out, 0x01)
		return append(out, v4...)
	}
	out = append(out, 0x04)
	return append(out, ip.To16()...)
}

func readSocks5ConnectResponse(conn net.Conn) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("invalid socks version %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("socks connect failed: %s", socks5Status(header[1]))
	}
	toRead := 0
	switch header[3] {
	case 0x01:
		toRead = net.IPv4len
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return err
		}
		toRead = int(length[0])
	case 0x04:
		toRead = net.IPv6len
	default:
		return fmt.Errorf("invalid socks address type %d", header[3])
	}
	if _, err := io.CopyN(io.Discard, conn, int64(toRead+2)); err != nil {
		return err
	}
	return nil
}

func socks5Status(code byte) string {
	switch code {
	case 0x01:
		return "general failure"
	case 0x02:
		return "connection not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "ttl expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("status %d", code)
	}
}
