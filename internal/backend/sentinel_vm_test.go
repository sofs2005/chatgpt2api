package backend

import "testing"

// The sentinel SDK resolves its own script URL with
// findScript(pattern).src.split("/").pop(). Both the scan and the fallback feed
// the same chain, so the reported file name is "sdk.js" either way.

func TestSentinelFindScriptMatchesDocumentScript(t *testing.T) {
	profile := defaultSentinelProfile("")
	profile.BuildNumber = "20260810913b"
	vm := newSentinelVMFor("p", profile)
	vm.set(1.0, `/sentinel/[^/]+/sdk\.js(?=[?#]|$)`)
	vm.opFindScript([]vmValue{2.0, 1.0})
	if got, want := vm.str(vm.get(2.0)), sentinelScriptSrc(profile); got != want {
		t.Fatalf("findScript() = %q, want %q", got, want)
	}
}

func TestSentinelFindScriptFallsBackToSdkPath(t *testing.T) {
	vm := newSentinelVMFor("p", defaultSentinelProfile(""))
	vm.set(1.0, `/no-such-script\.js$`)
	vm.opFindScript([]vmValue{2.0, 1.0})
	if got := vm.str(vm.get(2.0)); got != "/sdk.js" {
		t.Fatalf("findScript() fallback = %q, want %q", got, "/sdk.js")
	}
}

func TestSentinelScriptSrcSplitPopYieldsSdkFileName(t *testing.T) {
	vm := newSentinelVMFor("p", defaultSentinelProfile(""))
	src := sentinelScriptSrc(vm.profile)
	parts, ok := vm.invokeMember(src, "split", []vmValue{"/"}).([]vmValue)
	if !ok {
		t.Fatalf("split(%q, %q) did not return an array", src, "/")
	}
	if got := vm.invokeMember(parts, "pop", nil); got != "sdk.js" {
		t.Fatalf("split(...).pop() = %v, want %q", got, "sdk.js")
	}
}

func TestSentinelScriptPatternRewritesLookahead(t *testing.T) {
	// Go's regexp is RE2 and rejects (?=...), so the pattern the SDK ships has to
	// be rewritten before it can be compiled.
	if !scriptPatternMatches(`/sentinel/[^/]+/sdk\.js(?=[?#]|$)`, "https://chatgpt.com/sentinel/20260810913b/sdk.js") {
		t.Fatal("pattern with lookahead did not match the SDK src")
	}
	if scriptPatternMatches(`/sentinel/[^/]+/sdk\.js(?=[?#]|$)`, "https://chatgpt.com/sentinel/20260810913b/sdk.js.map") {
		t.Fatal("pattern matched a src the lookahead should reject")
	}
}
