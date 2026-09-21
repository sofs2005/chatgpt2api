package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt2api/internal/util"
)

func TestSOCKS5AddressModes(t *testing.T) {
	t.Run("socks5h keeps hostname for proxy-side DNS", func(t *testing.T) {
		got, err := socks5Address(context.Background(), "socks5h", "chatgpt.com:443")
		if err != nil {
			t.Fatalf("socks5Address() error = %v", err)
		}
		wantPrefix := []byte{0x03, byte(len("chatgpt.com"))}
		if string(got[:len(wantPrefix)]) != string(wantPrefix) {
			t.Fatalf("address prefix = %#v, want %#v", got[:len(wantPrefix)], wantPrefix)
		}
		if host := string(got[2 : 2+len("chatgpt.com")]); host != "chatgpt.com" {
			t.Fatalf("host = %q", host)
		}
		if got[len(got)-2] != 0x01 || got[len(got)-1] != 0xbb {
			t.Fatalf("port bytes = %#v", got[len(got)-2:])
		}
	})

	t.Run("socks5 sends numeric ip when target is ip literal", func(t *testing.T) {
		got, err := socks5Address(context.Background(), "socks5", net.JoinHostPort("127.0.0.1", "8080"))
		if err != nil {
			t.Fatalf("socks5Address() error = %v", err)
		}
		want := []byte{0x01, 127, 0, 0, 1, 0x1f, 0x90}
		if string(got) != string(want) {
			t.Fatalf("address = %#v, want %#v", got, want)
		}
	})
}

func TestBrowserHTTPClientKeepsSessionAndTimeout(t *testing.T) {
	client := browserHTTPClient("", 2*time.Second)
	if client == nil {
		t.Fatal("browserHTTPClient() returned nil")
	}
	if client.Jar == nil {
		t.Fatal("browserHTTPClient() should enable a cookie jar for browser-like sessions")
	}
	if client.Timeout != 2*time.Second {
		t.Fatalf("Timeout = %s, want %s", client.Timeout, 2*time.Second)
	}
}

func TestBrowserHTTPClientPreservesCallerAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Origin"); got != "https://chatgpt.com" {
			t.Fatalf("Origin = %q", got)
		}
		if got := r.Header.Get("Referer"); got != "https://chatgpt.com/" {
			t.Fatalf("Referer = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Fatal("User-Agent should be populated by browser impersonation")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := browserHTTPClient("", 2*time.Second)
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token-1")
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Referer", "https://chatgpt.com/")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestProxyTestUsesBrowserFingerprintHeaders(t *testing.T) {
	cert, roots := mustChatGPTCertificate(t)
	t.Setenv("GODEBUG", "x509usefallbackroots=1")
	x509.SetFallbackRoots(roots)
	var seenMu sync.Mutex
	var seenUserAgent string
	var seenSecCHUA string

	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		seenUserAgent = r.Header.Get("User-Agent")
		seenSecCHUA = r.Header.Get("Sec-Ch-Ua")
		seenMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	target.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	target.StartTLS()
	defer target.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Fatalf("proxy method = %s, want CONNECT", r.Method)
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("proxy response writer does not support hijacking")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		upstream, err := net.Dial("tcp", target.Listener.Addr().String())
		if err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		go tunnelConn(conn, upstream)
	}))
	defer proxy.Close()

	service := NewProxyService(proxyConfigFunc(func() string { return "" }))
	result := service.Test(proxy.URL, 5*time.Second)

	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("result[ok] = %v, want true", result["ok"])
	}
	if status, _ := result["status"].(int); status != http.StatusNoContent {
		t.Fatalf("result[status] = %v, want %d", result["status"], http.StatusNoContent)
	}

	seenMu.Lock()
	defer seenMu.Unlock()
	if seenUserAgent == "" {
		t.Fatal("User-Agent should be sent to the upstream request")
	}
	if seenSecCHUA == "" {
		t.Fatal("Sec-Ch-Ua should be sent to the upstream request")
	}
}

// 出站浏览器的身份必须自洽：UA、Sec-Ch-Ua、Sec-Ch-Ua-Full-Version 的主版本
// 必须一致，且 Accept-Language 必须是我们统一设置的值。
//
// 这是针对真实出站路径的测试。此前的测试只断言头「非空」（TestProxyTestUsesBrowserFingerprintHeaders）
// 或只断言 headers() 返回的 map 内容，都绕过了 surf impersonate 中间件的覆盖，
// 因此「Sec-Ch-Ua 说 145、Sec-Ch-Ua-Full-Version 说 148」这类矛盾长期未被发现。
//
// 这里使用明文 HTTP 直连 httptest：被测机制是 surf 的 impersonate 请求中间件
// （在 TransportAdapter.RoundTrip 中先于传输层执行），与协议无关，
// 因此无需 TLS 即可完整复现头覆盖行为，也避免了 SetFallbackRoots 的进程级一次性限制。
func TestBrowserHTTPClientEmitsSelfConsistentIdentityHeaders(t *testing.T) {
	var seenMu sync.Mutex
	var seen http.Header

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		seen = r.Header.Clone()
		seenMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	// 逐个校验池中每种指纹：出站身份都必须自洽。
	for family, versions := range BrowserFamilyVersionPools() {
		for _, version := range versions {
			t.Run(family+version, func(t *testing.T) {
				fp := BrowserFingerprintFromFamilyVersion(family, version)
				headers := BrowserHeadersForFingerprint(fp)

				client := browserHTTPClientForProfile("", util.Clean(fp["impersonate"]), 5*time.Second)
				req, err := http.NewRequest(http.MethodGet, target.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				for key, value := range headers {
					req.Header.Set(key, value)
				}
				req.Header.Set("Accept-Language", BrowserAcceptLanguage)
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("request failed: %v", err)
				}
				defer resp.Body.Close()
				_, _ = io.Copy(io.Discard, resp.Body)

				seenMu.Lock()
				got := seen.Clone()
				seenMu.Unlock()

				userAgent := got.Get("User-Agent")
				secCHUA := got.Get("Sec-Ch-Ua")
				fullVersion := strings.Trim(got.Get("Sec-Ch-Ua-Full-Version"), `"`)
				if userAgent == "" || secCHUA == "" || fullVersion == "" {
					t.Fatalf("incomplete identity headers: UA=%q CH=%q FullVersion=%q", userAgent, secCHUA, fullVersion)
				}

				wantMajor := browserMajorVersion(version)
				uaMajor := identityMajorFromUserAgent(userAgent)
				// Firefox 的 Sec-Ch-Ua 不含 Chromium 品牌，按族选择对应品牌。
				brand := "Chromium"
				if family == "firefox" {
					brand = "Firefox"
				}
				chMajor := identityMajorFromClientHint(secCHUA, brand)
				fullMajor := browserMajorVersion(fullVersion)

				if uaMajor != wantMajor {
					t.Fatalf("User-Agent major = %q, want %q (UA=%q)", uaMajor, wantMajor, userAgent)
				}
				if chMajor != wantMajor {
					t.Fatalf("Sec-Ch-Ua major = %q, want %q (CH=%q)", chMajor, wantMajor, secCHUA)
				}
				if fullMajor != wantMajor {
					t.Fatalf("Sec-Ch-Ua-Full-Version major = %q, want %q (FullVersion=%q)", fullMajor, wantMajor, fullVersion)
				}
				if gotLanguage := got.Get("Accept-Language"); gotLanguage != BrowserAcceptLanguage {
					t.Fatalf("Accept-Language = %q, want %q", gotLanguage, BrowserAcceptLanguage)
				}
			})
		}
	}
}

// identityMajorFromUserAgent 取 UA 中的浏览器主版本。
// Chromium 系优先看 Chrome/，Firefox 看 Firefox/。
func identityMajorFromUserAgent(userAgent string) string {
	if version := browserRegexpVersion(userAgent, `Chrome/([0-9]+(?:\.[0-9]+){0,3})`); version != "" {
		return browserMajorVersion(version)
	}
	if version := browserRegexpVersion(userAgent, `Firefox/([0-9]+(?:\.[0-9]+){0,3})`); version != "" {
		return browserMajorVersion(version)
	}
	return ""
}

// identityMajorFromClientHint 从 Sec-Ch-Ua 中取指定品牌的版本。
func identityMajorFromClientHint(secCHUA, brand string) string {
	pattern := `"` + regexp.QuoteMeta(brand) + `";v="([0-9]+)`
	return browserRegexpVersion(secCHUA, pattern)
}

func mustChatGPTCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	caPriv, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caSerialLimit := new(big.Int).Lsh(big.NewInt(1), 62)
	caSerial, err := crand.Int(crand.Reader, caSerialLimit)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			CommonName:   "chatgpt2api test CA",
			Organization: []string{"chatgpt2api"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(crand.Reader, &caTemplate, &caTemplate, &caPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	serverPriv, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverSerial, err := crand.Int(crand.Reader, caSerialLimit)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := x509.Certificate{
		SerialNumber: serverSerial,
		Subject: pkix.Name{
			CommonName: "chatgpt.com",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"chatgpt.com"},
	}
	serverDER, err := x509.CreateCertificate(crand.Reader, &serverTemplate, caCert, &serverPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatal(err)
	}
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	serverKey, err := x509.MarshalECPrivateKey(serverPriv)
	if err != nil {
		t.Fatal(err)
	}
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKey})
	cert, err := tls.X509KeyPair(serverPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return cert, pool
}

type proxyConfigFunc func() string

func (f proxyConfigFunc) Proxy() string { return f() }

func tunnelConn(left, right net.Conn) {
	defer left.Close()
	defer right.Close()
	go func() {
		_, _ = io.Copy(right, left)
		_ = right.Close()
	}()
	_, _ = io.Copy(left, right)
}
