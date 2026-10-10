package backend

import (
	"testing"

	"chatgpt2api/internal/util"
)

// 同一份机器身份必须同时驱动 PoW 载荷、sentinel 指纹与 client_contextual_info。
// 三者若各自取值，同一请求里就会出现「PoW 报 4K、上下文报 1080p」这类矛盾，
// 这正是本次改动要消除的信号。
func TestHardwareIdentityIsSharedAcrossSurfaces(t *testing.T) {
	hw := hardwareIdentityForSeed("device-abc")
	profile := sentinelProfileFor(hw, "UA/1.0", "20260810913b")
	config := buildPOWConfig(hw, "UA/1.0", []string{"https://chatgpt.com/sdk.js"}, "c/x/_")
	contextual := conversationContextualInfoFor(hw)

	// 屏幕分辨率：PoW index 0 是宽高之和，其余两处是分开的宽高。
	if got, want := config[0], hw.Resolution[0]+hw.Resolution[1]; got != want {
		t.Fatalf("PoW resolution sum = %v, want %d", got, want)
	}
	if profile.ScreenWidth != hw.Resolution[0] || profile.ScreenHeight != hw.Resolution[1] {
		t.Fatalf("sentinel screen = %dx%d, want %dx%d", profile.ScreenWidth, profile.ScreenHeight, hw.Resolution[0], hw.Resolution[1])
	}
	if contextual["screen_width"] != hw.Resolution[0] || contextual["screen_height"] != hw.Resolution[1] {
		t.Fatalf("contextual screen = %vx%v, want %dx%d", contextual["screen_width"], contextual["screen_height"], hw.Resolution[0], hw.Resolution[1])
	}

	// 核数：PoW index 16 与 sentinel navigator.hardwareConcurrency 必须一致。
	if got := config[16]; got != hw.Core {
		t.Fatalf("PoW core = %v, want %d", got, hw.Core)
	}
	if profile.HardwareConc != hw.Core {
		t.Fatalf("sentinel hardwareConcurrency = %d, want %d", profile.HardwareConc, hw.Core)
	}

	// 窗口尺寸：sentinel 的 innerWidth/Height 与上下文的 page_width/height 同源。
	if contextual["page_width"] != profile.ViewportWidth || contextual["page_height"] != profile.ViewportHeight {
		t.Fatalf("contextual page = %vx%v, sentinel viewport = %dx%d",
			contextual["page_width"], contextual["page_height"], profile.ViewportWidth, profile.ViewportHeight)
	}
	if contextual["pixel_ratio"] != profile.PixelRatio {
		t.Fatalf("contextual pixel_ratio = %v, sentinel = %v", contextual["pixel_ratio"], profile.PixelRatio)
	}
}

// 同一账号每次请求必须得到同一台机器：机器身份按 device id 派生，而不是每次
// 重新随机。否则同一账号连续两次请求会自报两块不同的显卡。
func TestHardwareIdentityIsStablePerDevice(t *testing.T) {
	first := hardwareIdentityForSeed("device-abc")
	second := hardwareIdentityForSeed("device-abc")
	if first != second {
		t.Fatalf("same seed produced different identities: %+v vs %+v", first, second)
	}
	if other := hardwareIdentityForSeed("device-xyz"); other == first {
		t.Fatalf("different seeds collided on the same identity: %+v", other)
	}
}

// 机器身份内部的取值必须来自真实硬件池，而不是任意拼凑。
func TestHardwareIdentityUsesRealisticValues(t *testing.T) {
	validResolutions := map[[2]int]bool{}
	for _, r := range hardwareResolutionPool {
		validResolutions[r] = true
	}
	validCores := map[int]bool{}
	for _, c := range hardwareCorePool {
		validCores[c] = true
	}
	validGPUs := map[string]bool{}
	for _, g := range hardwareGPUPool {
		validGPUs[g.renderer] = true
	}

	for _, seed := range []string{"", "a", "device-1", "device-2", "device-3", "device-4", "device-5", "device-6", "device-7"} {
		hw := hardwareIdentityForSeed(seed)
		if !validResolutions[hw.Resolution] {
			t.Fatalf("seed %q resolution %v not in the real pool", seed, hw.Resolution)
		}
		if !validCores[hw.Core] {
			t.Fatalf("seed %q core %d not in the real pool", seed, hw.Core)
		}
		if !validGPUs[hw.WebGLRenderer] {
			t.Fatalf("seed %q renderer %q not in the real pool", seed, hw.WebGLRenderer)
		}
		// Chrome 把 deviceMemory 上限压在 8，报更大的值本身就是破绽。
		if hw.DeviceMemory > 8 {
			t.Fatalf("seed %q deviceMemory %d exceeds the Chrome cap of 8", seed, hw.DeviceMemory)
		}
		// availHeight 是屏幕高度减去任务栏；availWidth 不受水平任务栏影响。
		availWidth, availHeight := hw.avail()
		if availWidth != hw.Resolution[0] {
			t.Fatalf("seed %q availWidth %d, want %d", seed, availWidth, hw.Resolution[0])
		}
		if availHeight != hw.Resolution[1]-screenTaskbarHeight {
			t.Fatalf("seed %q availHeight %d, want %d", seed, availHeight, hw.Resolution[1]-screenTaskbarHeight)
		}
	}
}

// sentinel 指纹的语言/语言列表必须与请求身份同源，不能再写死 zh-CN。
func TestSentinelProfileLocaleMatchesOutboundIdentity(t *testing.T) {
	profile := sentinelProfileFor(hardwareIdentityForSeed("device-abc"), "UA/1.0", "")
	if profile.Language != util.OutboundLocaleTag {
		t.Fatalf("sentinel language = %q, want %q", profile.Language, util.OutboundLocaleTag)
	}
	window := newSentinelWindowFor(profile)
	navigator := window.(*sentinelObject).values["navigator"].(*sentinelObject)
	language := navigator.values["language"]
	if language != profile.Language {
		t.Fatalf("navigator.language = %v, want %q", language, profile.Language)
	}
	languages, ok := navigator.values["languages"].([]vmValue)
	if !ok || len(languages) == 0 {
		t.Fatalf("navigator.languages = %#v, want a non-empty array", navigator.values["languages"])
	}
	if languages[0] != profile.Language {
		t.Fatalf("navigator.languages[0] = %v, want %q", languages[0], profile.Language)
	}
}
