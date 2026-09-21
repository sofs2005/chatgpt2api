package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// 验证 PoW 载荷内部自洽，且与请求其余部分使用同一身份。
//
// 同一份浏览器身份在请求体、请求头与 PoW 探针里必须自报一致的语言与时区。
// 此前载荷固定写 en-US / EST，而请求体用的是 Asia/Shanghai（-480），
// 头里是 OAI-Language: zh-CN，三者互相矛盾。
func TestBuildPOWConfigIdentityIsSelfConsistent(t *testing.T) {
	config := buildPOWConfig("UA/1.0", []string{"https://chatgpt.com/sdk.js"}, "c/x/_")

	// index 7/8 是 navigator.language 与 languages，必须与请求身份一致。
	if got := config[7]; got != "zh-CN" {
		t.Fatalf("config[7] language = %v, want zh-CN", got)
	}
	if got := config[8]; got != "zh-CN,zh,en" {
		t.Fatalf("config[8] languages = %v, want zh-CN,zh,en", got)
	}

	// index 1 是本地时间字符串，时区必须与请求体的 Asia/Shanghai 一致。
	localTime, _ := config[1].(string)
	if !strings.Contains(localTime, "GMT+0800") {
		t.Fatalf("config[1] local time = %q, want GMT+0800 offset", localTime)
	}
	if strings.Contains(localTime, "Eastern Standard Time") {
		t.Fatalf("config[1] local time = %q, still reports the old EST zone", localTime)
	}

	// index 13 是 performance.now()，应为页面存活毫秒数（量级远小于 Unix 毫秒）。
	uptime, ok := config[13].(float64)
	if !ok {
		t.Fatalf("config[13] = %v, want float64", config[13])
	}
	if uptime > 1e10 {
		t.Fatalf("config[13] = %v, looks like absolute Unix millis rather than page uptime", uptime)
	}

	// index 17 是绝对秒级时间戳，必须落在合理区间。
	stamp, ok := config[17].(float64)
	if !ok {
		t.Fatalf("config[17] = %v, want float64", config[17])
	}
	if stamp < 1e9 {
		t.Fatalf("config[17] = %v, want a real unix-seconds timestamp", stamp)
	}

	// navigator 探针中的 hardwareConcurrency 必须与核数字段一致。
	core, ok := config[16].(int)
	if !ok {
		t.Fatalf("config[16] = %v, want int core count", config[16])
	}
	probe, _ := config[10].(string)
	if strings.HasPrefix(probe, "hardwareConcurrency−") {
		want := fmt.Sprintf("hardwareConcurrency−%d", core)
		if probe != want {
			t.Fatalf("config[10] = %q, want %q to match config[16]", probe, want)
		}
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
