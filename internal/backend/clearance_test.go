package backend

import (
	"net/http"
	"strings"
	"testing"

	"chatgpt2api/internal/service"
)

// cf_clearance 绑定签发时的 UA，只回注 cookie 而沿用账号原 UA 会被判为凭证盗用。
// 因此覆盖集必须整套一致：UA 与 Sec-Ch-Ua* 同代，且不得残留账号指纹里的版本。
func TestClearanceRequestHeadersReplaceWholeBrowserIdentity(t *testing.T) {
	bundle := service.ClearanceBundle{UA: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.7339.80 Safari/537.36"}
	headers := service.ClearanceRequestHeaders(bundle)

	if headers["User-Agent"] != bundle.UA {
		t.Fatalf("User-Agent = %q, want the clearance UA", headers["User-Agent"])
	}
	if !strings.Contains(headers["Sec-Ch-Ua"], `"Google Chrome";v="140"`) {
		t.Fatalf("Sec-Ch-Ua = %q, want Chrome 140 (the clearance UA's brand/version)", headers["Sec-Ch-Ua"])
	}
	if !strings.Contains(headers["Sec-Ch-Ua"], `"Chromium";v="140"`) {
		t.Fatalf("Sec-Ch-Ua = %q, want Chromium 140", headers["Sec-Ch-Ua"])
	}
	// 仓库的账号指纹常量是 145；若它漏进覆盖集，就会出现「UA 说 140、客户端提示说 145」。
	if strings.Contains(headers["Sec-Ch-Ua"], `v="145"`) {
		t.Fatalf("Sec-Ch-Ua = %q, must not keep the account fingerprint version", headers["Sec-Ch-Ua"])
	}
	if headers["Sec-Ch-Ua-Full-Version"] != `"140.0.7339.80"` {
		t.Fatalf("Sec-Ch-Ua-Full-Version = %q", headers["Sec-Ch-Ua-Full-Version"])
	}
	if !strings.Contains(headers["Sec-Ch-Ua-Full-Version-List"], `"Google Chrome";v="140.0.7339.80"`) {
		t.Fatalf("Sec-Ch-Ua-Full-Version-List = %q", headers["Sec-Ch-Ua-Full-Version-List"])
	}
}

// Edge 的 UA 同时含 Chrome/ 与 Edg/；品牌串必须报 Edge，不能因为先匹配到
// Chrome 就把身份写成 Google Chrome。
func TestClearanceRequestHeadersDetectEdgeBrand(t *testing.T) {
	bundle := service.ClearanceBundle{UA: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.3485.54"}
	headers := service.ClearanceRequestHeaders(bundle)

	if !strings.Contains(headers["Sec-Ch-Ua"], `"Microsoft Edge";v="140"`) {
		t.Fatalf("Sec-Ch-Ua = %q, want the Edge brand", headers["Sec-Ch-Ua"])
	}
	if strings.Contains(headers["Sec-Ch-Ua"], `"Google Chrome"`) {
		t.Fatalf("Sec-Ch-Ua = %q, must not report Chrome for an Edge UA", headers["Sec-Ch-Ua"])
	}
}

// 解析不出 Chrome/Edge 版本时回落到仓库常量，而不是发出空头。
// 空 Sec-Ch-Ua 比不覆盖更容易识别：真实浏览器绝不会缺这一项。
func TestClearanceRequestHeadersFallBackForUnparsableUA(t *testing.T) {
	bundle := service.ClearanceBundle{UA: "Mozilla/5.0 (X11; Linux x86_64; rv:148.0) Gecko/20100101 Firefox/148.0"}
	headers := service.ClearanceRequestHeaders(bundle)

	if headers["Sec-Ch-Ua"] != service.DefaultBrowserSecCHUA {
		t.Fatalf("Sec-Ch-Ua = %q, want the repo constant for a UA without Chrome/Edge version", headers["Sec-Ch-Ua"])
	}
	if headers["User-Agent"] != bundle.UA {
		t.Fatalf("User-Agent = %q, want the clearance UA", headers["User-Agent"])
	}
}

// 没有 UA 就没有可对齐的身份，此时必须完全不覆盖，而不是写空值。
func TestClearanceRequestHeadersEmptyWithoutUA(t *testing.T) {
	headers := service.ClearanceRequestHeaders(service.ClearanceBundle{})
	if len(headers) != 0 {
		t.Fatalf("headers = %v, want empty when the bundle has no UA", headers)
	}
}

// 只回注 CF 凭证：账号自己的 cookie 必须原样保留，否则会把登录态打掉。
func TestApplyClearanceCookiesKeepsAccountCookies(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/me", nil)
	req.AddCookie(&http.Cookie{Name: "oai-sc", Value: "account-session"})
	req.AddCookie(&http.Cookie{Name: "cf_clearance", Value: "stale"})

	applyClearanceCookies(req, service.ClearanceBundle{Cookies: map[string]string{
		"cf_clearance": "fresh",
		"__cf_bm":      "bm",
	}})

	values := map[string]string{}
	for _, cookie := range req.Cookies() {
		values[cookie.Name] = cookie.Value
	}
	if values["cf_clearance"] != "fresh" {
		t.Fatalf("cf_clearance = %q, want the newly solved value", values["cf_clearance"])
	}
	if values["__cf_bm"] != "bm" {
		t.Fatalf("__cf_bm = %q, want it injected", values["__cf_bm"])
	}
	if values["oai-sc"] != "account-session" {
		t.Fatalf("oai-sc = %q, want the account cookie preserved", values["oai-sc"])
	}
	// 过期凭证不能重复出现：同名 cookie 出现两次时服务端取值不确定。
	if count := strings.Count(strings.Join(req.Header.Values("Cookie"), ";"), "cf_clearance="); count != 1 {
		t.Fatalf("cf_clearance appears %d times in %q", count, req.Header.Get("Cookie"))
	}
}

// 挑战判定优先采信 Cloudflare 自己的标记，避免把业务 403 也送去开浏览器。
func TestIsClearanceChallengeResponse(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header string
		want   bool
	}{
		{name: "cf-mitigated header on 200", status: 200, header: "challenge", want: true},
		{name: "cf-mitigated header case", status: 200, header: "Challenge", want: true},
		{name: "403 without header", status: 403, want: true},
		{name: "503 without header", status: 503, want: true},
		{name: "403 with a non-challenge marker falls back to status", status: 403, header: "block", want: true},
		{name: "401 is not a challenge", status: 401, want: false},
		{name: "500 is not a challenge", status: 500, want: false},
		{name: "200 is not a challenge", status: 200, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Header: http.Header{}}
			if tc.header != "" {
				resp.Header.Set("cf-mitigated", tc.header)
			}
			if got := service.IsClearanceChallengeResponse(resp); got != tc.want {
				t.Fatalf("service.IsClearanceChallengeResponse(%d, %q) = %v, want %v", tc.status, tc.header, got, tc.want)
			}
		})
	}
	if service.IsClearanceChallengeResponse(nil) {
		t.Fatal("nil response must not be treated as a challenge")
	}
}
