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
	"sync"
	"testing"
	"time"
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
