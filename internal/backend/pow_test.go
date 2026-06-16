package backend

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// 验证 buildPOWConfig 已对齐 ChatGPT 网页最新版 PoW 格式（上游 commit 86a4977）。
func TestBuildPOWConfigMatchesLatestWebFormat(t *testing.T) {
	config := buildPOWConfig("UA/1.0", []string{"https://chatgpt.com/sdk.js"}, "c/x/_")

	if len(config) != 25 {
		t.Fatalf("config length = %d, want 25", len(config))
	}
	// 最新版浏览器占位标志：index 3 为 1（旧版为 0）。
	if got, ok := config[3].(int); !ok || got != 1 {
		t.Fatalf("config[3] = %v, want int 1", config[3])
	}
	// 尾部 7 个字段（index 18-24）必须全部为 0。
	for i := 18; i <= 24; i++ {
		if got, ok := config[i].(int); !ok || got != 0 {
			t.Fatalf("config[%d] = %v, want int 0", i, config[i])
		}
	}
	// document key（index 11）必须来自最新版集合。
	validDocKeys := map[string]bool{
		"__reactContainer$fzelfjyxej8": true,
		"_reactListening5dehydibo78":   true,
		"location":                     true,
	}
	docKey, _ := config[11].(string)
	if !validDocKeys[docKey] {
		t.Fatalf("config[11] document key = %q, not in latest set", docKey)
	}
	// 屏幕分辨率求和（index 0）必须来自真实分辨率池：
	// 1920+1080=3000, 1440+900=2340, 2560+1440=4000, 3840+2160=6000。
	validRes := map[int]bool{3000: true, 2340: true, 4000: true, 6000: true}
	res, ok := config[0].(int)
	if !ok || !validRes[res] {
		t.Fatalf("config[0] resolution sum = %v, not a real resolution sum", config[0])
	}
}

// 验证 legacy requirements token 已简化为直接 base64 编码 config（不再求解 PoW）。
func TestBuildLegacyRequirementsTokenIsPlainBase64Config(t *testing.T) {
	token := buildLegacyRequirementsToken("UA/1.0", []string{"https://chatgpt.com/sdk.js"}, "c/x/_")
	const prefix = "gAAAAAC"
	if !strings.HasPrefix(token, prefix) {
		t.Fatalf("token = %q, want prefix %q", token, prefix)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(token, prefix))
	if err != nil {
		t.Fatalf("token payload not base64: %v", err)
	}
	var config []any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("token payload not a JSON config array: %v", err)
	}
	if len(config) != 25 {
		t.Fatalf("decoded config length = %d, want 25", len(config))
	}
}
