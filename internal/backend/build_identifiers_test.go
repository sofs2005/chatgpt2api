package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 上游边缘同时在线多个构建，HTML 里的 data-build 是本次命中的真值。
func TestParsePOWResourcesReadsDataBuildFromRootElement(t *testing.T) {
	html := `<html lang="en-US" data-build="prod-980a55fc7f96eb70ab707f04eca80e3f613c9ed1" data-seq="11447364">` +
		`<script src="/cdn/assets/root-he0delv3.js"></script></html>`

	sources, dataBuild := parsePOWResources(html)
	if dataBuild != "prod-980a55fc7f96eb70ab707f04eca80e3f613c9ed1" {
		t.Fatalf("dataBuild = %q", dataBuild)
	}
	if len(sources) != 1 || sources[0] != "/cdn/assets/root-he0delv3.js" {
		t.Fatalf("sources = %v", sources)
	}
}

// 旧版 HTML 把构建号放在 c/<hash>/_ 形式的脚本路径里，必须优先于 data-build。
func TestParsePOWResourcesPrefersScriptPathBuild(t *testing.T) {
	html := `<html data-build="prod-aaaa"><script src="/c/abc123/_/app.js"></script></html>`

	_, dataBuild := parsePOWResources(html)
	if dataBuild != "c/abc123/_" {
		t.Fatalf("dataBuild = %q, want c/abc123/_", dataBuild)
	}
}

// statsig 载荷里的 web_build_number 是 OAI-Client-Build-Number 的真值来源。
// 它在内联 JSON 中被转义，两种引号形式都要能命中。
func TestParseWebBuildNumberHandlesEscapedJSON(t *testing.T) {
	cases := map[string]string{
		`...,\"web_build_number\":11447364.0,\"account_user_id\":...`: "11447364",
		`"web_build_number":11447364`:                                 "11447364",
		`<html data-build="prod-x"></html>`:                           "",
	}
	for html, want := range cases {
		if got := parseWebBuildNumber(html); got != want {
			t.Fatalf("parseWebBuildNumber(%q) = %q, want %q", html, got, want)
		}
	}
}

// bootstrap 必须把请求头构建标识对齐到本次命中的构建。
// 若不同步，请求头会报旧构建而 PoW 指纹报新构建，构成可识别的身份矛盾。
func TestBootstrapAlignsClientIdentifiersWithServedBuild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html data-build="prod-7cd4936392a4f28a46116db77ca6bfbe51533e4a">` +
			`<script>{"web_build_number\":11447364.0,"}</script></html>`))
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	client.ClientVersion = "prod-stale"
	client.ClientBuildNumber = "1"

	if err := client.bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap() error = %v", err)
	}
	if client.ClientVersion != "prod-7cd4936392a4f28a46116db77ca6bfbe51533e4a" {
		t.Fatalf("ClientVersion = %q", client.ClientVersion)
	}
	if client.ClientBuildNumber != "11447364" {
		t.Fatalf("ClientBuildNumber = %q", client.ClientBuildNumber)
	}
}

// 构建号解析不到时必须保留原值，不能写入空串把头变空。
func TestBootstrapKeepsBuildNumberWhenAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html data-build="prod-7cd4936392a4f28a46116db77ca6bfbe51533e4a"></html>`))
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	client.ClientBuildNumber = "11018478"

	if err := client.bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap() error = %v", err)
	}
	if client.ClientBuildNumber != "11018478" {
		t.Fatalf("ClientBuildNumber = %q, want unchanged", client.ClientBuildNumber)
	}
}

// 上游前端 Hc() 的默认头里固定带 x-openai-web-frontend: core_web。
func TestHeadersIncludeWebFrontendMarker(t *testing.T) {
	client := &Client{BaseURL: "https://chatgpt.com", ClientVersion: "prod-x", ClientBuildNumber: "1"}
	client.fp = client.buildFingerprint()
	client.applyBrowserFingerprint()
	client.userAgent = client.fp["user-agent"]

	headers := client.headers("/backend-api/models", nil)
	if got := headers["x-openai-web-frontend"]; got != "core_web" {
		t.Fatalf("x-openai-web-frontend = %q, want core_web", got)
	}
	if !strings.HasPrefix(headers["OAI-Client-Version"], "prod-") {
		t.Fatalf("OAI-Client-Version = %q", headers["OAI-Client-Version"])
	}
}
