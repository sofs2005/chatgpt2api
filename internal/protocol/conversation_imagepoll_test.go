package protocol

import (
	"testing"
	"time"

	"chatgpt2api/internal/service"
)

func TestNewImageClientAppliesPollOptionsFromConfig(t *testing.T) {
	engine := &Engine{
		Proxy: service.NewProxyService(testProtocolProxyConfig{}),
		Config: testProtocolImageConfig{
			root:           t.TempDir(),
			settleEnabled:  true,
			checkBeforeHit: false,
			settleSecs:     1.5,
			modelSlug:      "gpt-5-6",
		},
	}

	// 空 token 可绕过账号指纹查询，专注校验轮询配置注入（与 token 无关）。
	client := engine.newImageClient("")
	opts := client.ImagePollOptions()
	if !opts.SettleEnabled {
		t.Fatalf("SettleEnabled = false, want true")
	}
	if opts.CheckBeforeHit {
		t.Fatalf("CheckBeforeHit = true, want false")
	}
	if opts.SettleSecs != 1500*time.Millisecond {
		t.Fatalf("SettleSecs = %v, want 1.5s", opts.SettleSecs)
	}
	if got := client.ImageModelSlug(); got != "gpt-5-6" {
		t.Fatalf("ImageModelSlug() = %q, want %q", got, "gpt-5-6")
	}
}
