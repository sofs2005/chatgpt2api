package backend

// sentinelWindow models the slice of the browser surface the sentinel programs
// read while collecting fingerprints. It is deliberately a plain value tree:
// the VM only ever reads properties, calls a handful of methods, and
// stringifies results. Values that a real browser would derive from the live
// environment (WebGL renderer strings, canvas pixels, timing) are populated
// from a profile so a request can present one coherent identity.

import (
	"encoding/json"
	"math/rand"
	"strings"
	"time"

	"chatgpt2api/internal/util"
)

// sentinelProfile carries the environment-derived values a fingerprint program
// reads. Keeping them in one struct lets a caller pin a coherent identity
// (user agent, screen, renderer) instead of inventing per-request noise.
type sentinelProfile struct {
	UserAgent        string
	Language         string
	Platform         string
	Vendor           string
	HardwareConc     int
	DeviceMemory     int
	ScreenWidth      int
	ScreenHeight     int
	AvailWidth       int
	AvailHeight      int
	AvailLeft        int
	AvailTop         int
	ColorDepth       int
	PixelRatio       float64
	ViewportWidth    int
	ViewportHeight   int
	Href             string
	BuildNumber      string
	WebGLVendor      string
	WebGLRenderer    string
	MaxTextureSize   int
	LocalStorageKeys []string
	CFConnectingIP   string
	CFIPCity         string
	CFIPLatitude     float64
	CFIPLongitude    float64
	UserRegion       string
	// SessionObserverOwner fills window.__oai_so_owner. The SO programs only
	// check that it is defined, so a fixed id is enough.
	SessionObserverOwner string
}

// defaultSentinelProfile mirrors the identity the rest of the backend sends for
// a client that has no fingerprint of its own.
func defaultSentinelProfile(userAgent string) sentinelProfile {
	return sentinelProfileFor(hardwareIdentityForSeed(""), userAgent, "")
}

// sentinelProfileFor renders one machine identity into the environment values
// the sentinel fingerprint programs read. Locale and timezone come from the same
// util constants the request body and headers use, and the screen/GPU/cores come
// from the shared hardwareIdentity, so the fingerprint cannot contradict the PoW
// payload or client_contextual_info in the same request.
func sentinelProfileFor(hw hardwareIdentity, userAgent, buildNumber string) sentinelProfile {
	viewportWidth, viewportHeight := hw.viewport()
	availWidth, availHeight := hw.avail()
	return sentinelProfile{
		UserAgent:            userAgent,
		Language:             util.OutboundLocaleTag,
		Platform:             hw.Platform,
		Vendor:               hw.Vendor,
		HardwareConc:         hw.Core,
		DeviceMemory:         hw.DeviceMemory,
		ScreenWidth:          hw.Resolution[0],
		ScreenHeight:         hw.Resolution[1],
		AvailWidth:           availWidth,
		AvailHeight:          availHeight,
		ColorDepth:           24,
		PixelRatio:           hw.PixelRatio,
		ViewportWidth:        viewportWidth,
		ViewportHeight:       viewportHeight,
		Href:                 "https://chatgpt.com/",
		BuildNumber:          buildNumber,
		WebGLVendor:          hw.WebGLVendor,
		WebGLRenderer:        hw.WebGLRenderer,
		MaxTextureSize:       hw.MaxTextureSize,
		SessionObserverOwner: "145595fb-49b5-4aa6-afaf-79e04f09396d",
		LocalStorageKeys: []string{
			"STATSIG_LOCAL_STORAGE_INTERNAL_STORE_V4",
			"STATSIG_LOCAL_STORAGE_STABLE_ID",
			"client-correlated-secret",
			"oai/apps/capExpiresAt",
			"oai-did",
			"STATSIG_LOCAL_STORAGE_LOGGING_REQUEST",
			"UiState.isNavigationCollapsed.1",
		},
	}
}

// localeListValues renders util.OutboundLocaleList ("en-US,en") into the array
// navigator.languages exposes.
func localeListValues() []vmValue {
	parts := strings.Split(util.OutboundLocaleList, ",")
	out := make([]vmValue, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// sentinelObject is a small ordered property bag standing in for a JS object.
type sentinelObject struct {
	keys   []string
	values map[string]vmValue
	// hidden marks properties that resolve through member() but are not own
	// enumerable properties, so Object.keys() skips them the way it skips
	// prototype members in a real DOM object.
	hidden map[string]bool
}

func newSentinelObject() *sentinelObject {
	return &sentinelObject{values: map[string]vmValue{}}
}

func (o *sentinelObject) set(key string, value vmValue) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// setHidden installs a non-enumerable property. DOM members such as
// Storage.getItem live on the prototype in a browser, so a fingerprint that
// enumerates the object must not see them.
func (o *sentinelObject) setHidden(key string, value vmValue) {
	o.set(key, value)
	if o.hidden == nil {
		o.hidden = map[string]bool{}
	}
	o.hidden[key] = true
}

// ownKeys returns the enumerable property names in insertion order.
func (o *sentinelObject) ownKeys() []string {
	out := make([]string, 0, len(o.keys))
	for _, k := range o.keys {
		if o.hidden[k] {
			continue
		}
		out = append(out, k)
	}
	return out
}

// MarshalJSON renders the object the way JSON.stringify would: insertion order,
// values as-is. Without this the unexported fields marshal to "{}", which is
// what the fingerprint program's final JSON.stringify call would then send.
func (o *sentinelObject) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// sentinelMember is a method resolved off a receiver, carrying the receiver so
// a later call dispatches to the right object.
type sentinelMember struct {
	recv vmValue
	name string
}

// boundMember is produced by opcode 24 (bind): a member whose receiver is fixed.
type boundMember = sentinelMember

// newSentinelWindow builds the window value tree the programs navigate.
func newSentinelWindow() vmValue {
	profile := defaultSentinelProfile("")
	return newSentinelWindowFor(profile)
}

// sentinelClock is the wall clock the fingerprint programs read through
// performance.now / Date.now. Keeping it at package scope lets a solve pin a
// stable origin so repeated solves of one identity stay byte-identical.
var sentinelClockOrigin = time.Now()

func newSentinelWindowFor(p sentinelProfile) vmValue {
	canvas := newSentinelObject()
	canvas.set("width", float64(p.ViewportWidth))
	canvas.set("height", float64(p.ViewportHeight))
	canvas.set("getContext", sentinelMember{recv: canvas, name: "getContext"})
	canvas.set("toDataURL", sentinelMember{recv: canvas, name: "toDataURL"})

	document := newSentinelObject()
	// The page's own script tags. findScript scans these for the sentinel SDK,
	// so the SDK's own src has to be present for the scan to be faithful.
	script := newSentinelElement("script")
	script.set("src", sentinelScriptSrc(p))
	document.set("scripts", []vmValue{script})
	document.set("currentScript", script)
	document.set("cookie", "")
	document.set("hidden", false)
	document.set("visibilityState", "visible")
	document.set("createElement", sentinelMember{recv: document, name: "createElement"})
	document.set("body", newSentinelElement("body"))
	document.set("documentElement", newSentinelElement("html"))
	navigator := newSentinelObject()
	navigator.set("userAgent", p.UserAgent)
	navigator.set("language", p.Language)
	// navigator.languages is the full preference list, not just the primary tag:
	// a real browser reports "en-US,en" here while navigator.language is "en-US".
	navigator.set("languages", localeListValues())
	navigator.set("platform", p.Platform)
	navigator.set("vendor", p.Vendor)
	navigator.set("hardwareConcurrency", float64(p.HardwareConc))
	navigator.set("deviceMemory", float64(p.DeviceMemory))
	navigator.set("webdriver", false)
	navigator.set("maxTouchPoints", float64(0))

	screen := newSentinelObject()
	screen.set("width", float64(p.ScreenWidth))
	screen.set("height", float64(p.ScreenHeight))
	screen.set("availWidth", float64(p.AvailWidth))
	screen.set("availHeight", float64(p.AvailHeight))
	screen.set("availLeft", float64(p.AvailLeft))
	screen.set("availTop", float64(p.AvailTop))
	screen.set("colorDepth", float64(p.ColorDepth))
	screen.set("pixelDepth", float64(p.ColorDepth))

	localStorage := newSentinelObject()
	// Chrome's Storage exposes each entry as an own enumerable property whose
	// name is the key, so Object.keys(localStorage) yields the key names — not
	// numeric indices. Values are not part of the fingerprint, so empty strings
	// are enough; only the names must match. length/key/getItem stay hidden on
	// the prototype.
	for _, key := range p.LocalStorageKeys {
		localStorage.set(key, "")
	}
	localStorage.setHidden("length", float64(len(p.LocalStorageKeys)))
	localStorage.setHidden("key", sentinelMember{recv: localStorage, name: "key"})
	localStorage.setHidden("getItem", sentinelMember{recv: localStorage, name: "getItem"})
	localStorage.setHidden("setItem", sentinelMember{recv: localStorage, name: "setItem"})
	localStorage.setHidden("removeItem", sentinelMember{recv: localStorage, name: "removeItem"})

	performance := newSentinelObject()
	performance.set("now", sentinelMember{recv: performance, name: "now"})
	performance.set("timeOrigin", float64(sentinelClockOrigin.UnixMilli()))
	performance.set("memory", newSentinelObject())

	history := newSentinelObject()
	history.set("length", float64(1))
	history.set("state", nil)

	location := newSentinelObject()
	location.set("href", p.Href)
	location.set("origin", "https://chatgpt.com")
	location.set("pathname", "/")
	location.set("protocol", "https:")
	location.set("host", "chatgpt.com")
	location.set("hostname", "chatgpt.com")
	// document.location is the same Location object as window.location; the
	// fingerprint reads it off document, so it must carry the same fields.
	document.set("location", location)

	object := newSentinelObject()
	object.set("create", sentinelMember{recv: object, name: "create"})
	object.set("keys", sentinelMember{recv: object, name: "keys"})

	reflect := newSentinelObject()
	reflect.set("set", sentinelMember{recv: reflect, name: "set"})
	reflect.set("get", sentinelMember{recv: reflect, name: "get"})

	date := newSentinelObject()
	date.set("now", sentinelMember{recv: date, name: "now"})

	math := newSentinelObject()
	math.set("random", sentinelMember{recv: math, name: "random"})
	math.set("floor", sentinelMember{recv: math, name: "floor"})
	math.set("imul", sentinelMember{recv: math, name: "imul"})
	math.set("round", sentinelMember{recv: math, name: "round"})

	weakSet := newSentinelObject()
	weakSet.set("add", sentinelMember{recv: weakSet, name: "add"})
	weakSet.set("has", sentinelMember{recv: weakSet, name: "has"})

	window := newSentinelObject()
	window.set("navigator", navigator)
	window.set("document", document)
	window.set("screen", screen)
	window.set("localStorage", localStorage)
	window.set("performance", performance)
	window.set("history", history)
	window.set("location", location)
	window.set("innerWidth", float64(p.ViewportWidth))
	window.set("innerHeight", float64(p.ViewportHeight))
	window.set("outerWidth", float64(p.ScreenWidth))
	window.set("outerHeight", float64(p.ScreenHeight))
	window.set("devicePixelRatio", p.PixelRatio)
	window.set("Reflect", reflect)
	window.set("Object", object)
	window.set("Math", math)
	window.set("Date", date)
	window.set("JSON", newSentinelObject())
	window.set("WeakSet", weakSet)
	window.set("__reactRouterContext", newRouterContext(p))
	// The session-observer programs read window.__oai_so_owner to learn which
	// page instance owns the observer; without it they abort before producing a
	// token. Any stable id works — it only has to be defined.
	window.set("__oai_so_owner", p.SessionObserverOwner)
	return window
}

// ownKeyList renders an object's enumerable keys the way Object.keys() does.
func ownKeyList(o *sentinelObject) []vmValue {
	keys := o.ownKeys()
	out := make([]vmValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, k)
	}
	return out
}

// newRouterContext models the Cloudflare-injected bootstrap the fingerprint
// program reads out of window.__reactRouterContext.state.loaderData.root.
func newRouterContext(p sentinelProfile) *sentinelObject {
	bootstrap := newSentinelObject()
	bootstrap.set("cfConnectingIp", p.CFConnectingIP)
	bootstrap.set("cfIpCity", p.CFIPCity)
	bootstrap.set("userRegion", p.UserRegion)
	bootstrap.set("cfIpLatitude", p.CFIPLatitude)
	bootstrap.set("cfIpLongitude", p.CFIPLongitude)
	bootstrap.set("cfIpLongitudeText", formatJSNumber(p.CFIPLongitude))
	bootstrap.set("cfIpLatitudeText", formatJSNumber(p.CFIPLatitude))
	bootstrap.set("buildNumber", p.BuildNumber)

	root := newSentinelObject()
	root.set("clientBootstrap", bootstrap)
	loaderData := newSentinelObject()
	loaderData.set("root", root)
	state := newSentinelObject()
	state.set("loaderData", loaderData)
	ctx := newSentinelObject()
	ctx.set("state", state)
	return ctx
}

func newSentinelElement(tag string) *sentinelObject {
	el := newSentinelObject()
	el.set("tagName", strings.ToUpper(tag))
	el.set("innerText", "")
	el.set("style", newSentinelObject())
	el.set("children", []vmValue{})
	el.set("appendChild", sentinelMember{recv: el, name: "appendChild"})
	el.set("removeChild", sentinelMember{recv: el, name: "removeChild"})
	el.set("setAttribute", sentinelMember{recv: el, name: "setAttribute"})
	el.set("getBoundingClientRect", sentinelMember{recv: el, name: "getBoundingClientRect"})
	return el
}

// member resolves obj[name]. It handles the value tree plus the few native
// behaviours the programs depend on.
func (v *sentinelVM) member(obj vmValue, name string) vmValue {
	switch o := obj.(type) {
	case *sentinelObject:
		if value, ok := o.values[name]; ok {
			return value
		}
		return nil
	case map[string]any:
		if value, ok := o[name]; ok {
			return value
		}
		return nil
	case []vmValue:
		if name == "length" {
			return float64(len(o))
		}
		return nil
	case string:
		if name == "length" {
			return float64(len(o))
		}
		return nil
	}
	return nil
}

// invoke calls a resolved value with arguments. Handlers, bound members and a
// few native methods are the only callables the programs use.
func (v *sentinelVM) invoke(callee vmValue, args []vmValue) vmValue {
	switch fn := callee.(type) {
	case vmFunc:
		return fn(v, args)
	case sentinelMember:
		return v.invokeMember(fn.recv, fn.name, args)
	}
	return nil
}

func (v *sentinelVM) invokeMember(recv vmValue, name string, args []vmValue) vmValue {
	// String and array methods are reached through a bind, so they never land on
	// a sentinelObject receiver. The fingerprint uses them to turn a script src
	// into its file name, which is part of the reported environment.
	switch r := recv.(type) {
	case string:
		if name == "split" {
			sep := ""
			if len(args) > 0 {
				sep = v.str(args[0])
			}
			parts := strings.Split(r, sep)
			out := make([]vmValue, len(parts))
			for i, part := range parts {
				out[i] = part
			}
			return out
		}
		return nil
	case []vmValue:
		if name == "pop" {
			if len(r) == 0 {
				return nil
			}
			return r[len(r)-1]
		}
		return nil
	}
	obj, ok := recv.(*sentinelObject)
	if !ok {
		return nil
	}
	switch name {
	case "create":
		// Object.create(proto) returns a fresh plain object. The programs use it to
		// build the fingerprint bag they hand to return.
		return newSentinelObject()
	case "keys":
		if len(args) > 0 {
			if obj, ok := args[0].(*sentinelObject); ok {
				return ownKeyList(obj)
			}
		}
		return []vmValue{}
	case "set":
		// Reflect.set(target, key, value)
		if len(args) >= 3 {
			if obj, ok := args[0].(*sentinelObject); ok {
				obj.set(v.str(args[1]), args[2])
				return true
			}
		}
		return false
	case "get":
		if len(args) >= 2 {
			return v.member(args[0], v.str(args[1]))
		}
		return nil
	case "createElement":
		tag := "div"
		if len(args) > 0 {
			tag = v.str(args[0])
		}
		return newSentinelElement(tag)
	case "getContext":
		return v.newCanvasContext()
	case "toDataURL":
		return "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	case "appendChild", "removeChild", "setAttribute":
		if len(args) > 0 {
			return args[0]
		}
		return nil
	case "getBoundingClientRect":
		rect := newSentinelObject()
		rect.set("width", float64(100))
		rect.set("height", float64(20))
		rect.set("top", float64(0))
		rect.set("left", float64(0))
		return rect
	case "key":
		// Storage.key(i) returns the i-th entry name, or null past the end.
		if len(args) > 0 {
			idx := int(v.num(args[0]))
			if idx >= 0 && idx < len(obj.keys) && !obj.hidden[obj.keys[idx]] {
				return obj.keys[idx]
			}
		}
		return nil
	case "getItem":
		return nil
	case "setItem", "removeItem":
		return nil
	case "now":
		// Both performance.now() and Date.now() route here. A monotonically
		// increasing millisecond clock is what the observer programs sample.
		return float64(time.Since(sentinelClockOrigin).Milliseconds())
	case "random":
		return rand.Float64()
	case "floor":
		if len(args) > 0 {
			return float64(int64(v.num(args[0])))
		}
		return float64(0)
	case "round":
		if len(args) > 0 {
			return float64(int64(v.num(args[0]) + 0.5))
		}
		return float64(0)
	case "imul":
		if len(args) >= 2 {
			return float64(int32(uint32(int64(v.num(args[0]))) * uint32(int64(v.num(args[1])))))
		}
		return float64(0)
	case "add", "has":
		return true
	case "getExtension":
		// The renderer strings only exist behind WEBGL_debug_renderer_info; other
		// extensions are reported as present so the program's feature probing
		// matches a real context.
		ext := newSentinelObject()
		ext.set("UNMASKED_VENDOR_WEBGL", float64(0x9245))
		ext.set("UNMASKED_RENDERER_WEBGL", float64(0x9246))
		return ext
	case "getParameter":
		return v.webglParameter(args)
	case "getSupportedExtensions":
		return []vmValue{"WEBGL_debug_renderer_info", "EXT_texture_filter_anisotropic", "WEBGL_lose_context"}
	case "getImageData":
		return newImageData()
	case "measureText":
		text := newSentinelObject()
		text.set("width", float64(100))
		return text
	case "fillText", "fillRect", "clearRect":
		return nil
	}
	_ = obj
	return nil
}

// newImageData returns a canvas ImageData whose pixel buffer is stable for a
// given profile, so repeated solves of one identity hash identically.
func newImageData() *sentinelObject {
	const size = 16 * 16 * 4
	data := make([]vmValue, size)
	for i := range data {
		data[i] = float64((i*31 + 7) % 256)
	}
	img := newSentinelObject()
	img.set("data", data)
	img.set("width", float64(16))
	img.set("height", float64(16))
	return img
}

// newCanvasContext returns a WebGL-ish context. The programs read the renderer
// strings via getExtension/getParameter and read 2D pixels via getImageData.
func (v *sentinelVM) newCanvasContext() vmValue {
	ctx := newSentinelObject()
	ctx.set("getExtension", sentinelMember{recv: ctx, name: "getExtension"})
	ctx.set("getParameter", sentinelMember{recv: ctx, name: "getParameter"})
	ctx.set("getSupportedExtensions", sentinelMember{recv: ctx, name: "getSupportedExtensions"})
	ctx.set("getImageData", sentinelMember{recv: ctx, name: "getImageData"})
	ctx.set("fillText", sentinelMember{recv: ctx, name: "fillText"})
	ctx.set("measureText", sentinelMember{recv: ctx, name: "measureText"})
	return ctx
}

// webglParameter answers gl.getParameter for the two UNMASKED_* pnames the
// fingerprint reads. The programs pass the pname constant (0x9245 / 0x9246),
// not the name string, so map by value.
func (v *sentinelVM) webglParameter(args []vmValue) vmValue {
	if len(args) == 0 {
		return nil
	}
	switch v.num(args[0]) {
	case 0x9245: // UNMASKED_VENDOR_WEBGL
		return v.profile.WebGLVendor
	case 0x9246: // UNMASKED_RENDERER_WEBGL
		return v.profile.WebGLRenderer
	case 0x0d33: // MAX_TEXTURE_SIZE
		return float64(v.profile.MaxTextureSize)
	}
	return float64(0)
}
