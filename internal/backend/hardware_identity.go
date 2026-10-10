package backend

import (
	"hash/fnv"
	"strings"
)

// hardwareIdentity is the machine-level identity a browser exposes through
// navigator and screen. Every surface that describes the same machine is
// rendered from one instance of it, so the PoW probe array, the sentinel
// fingerprint programs and client_contextual_info can never disagree about the
// screen size, core count or GPU.
//
// It is derived from the account's device id instead of being drawn per
// request: a browser does not swap its monitor between two API calls, and two
// independent draws for one account would produce a pair of fingerprints that
// contradict each other.
type hardwareIdentity struct {
	Resolution     [2]int
	Core           int
	DeviceMemory   int
	PixelRatio     float64
	Platform       string
	Vendor         string
	WebGLVendor    string
	WebGLRenderer  string
	MaxTextureSize int
}

// hardwareResolutionPool holds real desktop resolutions. Inventing a monitor
// size, or pairing a 4K screen with an integrated GPU, is exactly the kind of
// incoherence a fingerprint check looks for.
var hardwareResolutionPool = [][2]int{
	{1920, 1080},
	{2560, 1440},
	{3840, 2160},
	{1440, 900},
	{1600, 900},
	{1366, 768},
}

var hardwareCorePool = []int{4, 8, 12, 16, 24, 32}

// hardwareMemoryPool stays at or below 8: Chrome caps navigator.deviceMemory
// there, so a larger value would itself be a tell.
var hardwareMemoryPool = []int{4, 8}

var hardwarePixelRatioPool = []float64{1, 1.25, 1.5, 2}

// hardwareGPU keeps the renderer string, the vendor string and the max texture
// size together, because a program that reads all three must see one card.
type hardwareGPU struct {
	vendor   string
	renderer string
	maxTex   int
}

var hardwareGPUPool = []hardwareGPU{
	{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)", 16384},
	{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce GTX 1660 SUPER Direct3D11 vs_5_0 ps_5_0, D3D11)", 16384},
	{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 4070 Direct3D11 vs_5_0 ps_5_0, D3D11)", 16384},
	{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)", 16384},
	{"Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 580 Direct3D11 vs_5_0 ps_5_0, D3D11)", 16384},
}

// screenTaskbarHeight is what the taskbar takes off screen.availHeight on a
// Windows desktop; availWidth is not reduced because the taskbar is horizontal.
const screenTaskbarHeight = 40

// hardwareIdentityForSeed maps a stable seed (the account's device id) onto one
// machine identity. An empty seed yields a fixed identity rather than a random
// one, so a client built without a fingerprint still reports something coherent.
func hardwareIdentityForSeed(seed string) hardwareIdentity {
	seed = strings.TrimSpace(seed)
	gpu := hardwareGPUPool[seedIndex(seed, "gpu", len(hardwareGPUPool))]
	return hardwareIdentity{
		Resolution:     hardwareResolutionPool[seedIndex(seed, "resolution", len(hardwareResolutionPool))],
		Core:           hardwareCorePool[seedIndex(seed, "core", len(hardwareCorePool))],
		DeviceMemory:   hardwareMemoryPool[seedIndex(seed, "memory", len(hardwareMemoryPool))],
		PixelRatio:     hardwarePixelRatioPool[seedIndex(seed, "pixel-ratio", len(hardwarePixelRatioPool))],
		Platform:       "Win32",
		Vendor:         "Google Inc.",
		WebGLVendor:    gpu.vendor,
		WebGLRenderer:  gpu.renderer,
		MaxTextureSize: gpu.maxTex,
	}
}

// seedIndex maps a (seed, facet) pair onto [0, n) with a stable hash. Each facet
// of one identity is drawn independently, but a given seed always lands on the
// same combination.
func seedIndex(seed, facet string, n int) int {
	if n <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(facet))
	return int(h.Sum64() % uint64(n))
}

// viewport is the window's inner size (window.innerWidth / innerHeight). It is a
// windowed browser rather than a maximized one: a real 2560x1440 desktop reports
// roughly 1180x753, which is this ratio.
func (h hardwareIdentity) viewport() (int, int) {
	return h.Resolution[0] * 46 / 100, h.Resolution[1] * 52 / 100
}

// avail is screen.availWidth / availHeight: the work area the OS leaves after
// the taskbar. availWidth is untouched because a Windows taskbar is horizontal.
func (h hardwareIdentity) avail() (int, int) {
	return h.Resolution[0], h.Resolution[1] - screenTaskbarHeight
}
