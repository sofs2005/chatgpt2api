package backend

// This file reimplements the ChatGPT sentinel bytecode VM that ships in
// https://chatgpt.com/sentinel/<build>/sdk.js. The payload returned by
// chat-requirements (turnstile.dx, so.collector_dx, so.snapshot_dx) is a
// base64 blob; XOR-decoding it with the request's own "p" yields a JSON array
// of instructions. Each instruction is [opcode, ...operands].
//
// Registers 0..35 are pre-seeded with the opcode handlers themselves. A program
// opens with [8, X, 8] ("copy") to alias a handler into a float-named register,
// which is why decoded programs show calls like [40.25, ...] — 40.25 is a
// register holding a handler, never an opcode.
//
// The single most important detail: handlers receive the RAW operands, exactly
// as the instruction wrote them. Each handler decides for itself which operands
// are register ids (resolved with get) and which are literals, and the variadic
// tails of the call-like opcodes are passed through unresolved so a handler can
// forward them to another handler. Register 9 is the instruction queue itself,
// so a program may replace the whole queue mid-run (the turnstile payload uses
// this to chain a second program).
//
// The VM is a pure interpreter: it touches only the browser surface it reads
// for fingerprinting. There is no runtime key, no WASM and no anti-tamper.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// vmOpcode ids, mirroring the SDK's constant table.
const (
	opRunner     = 0 // the sub-runner (Tn / qt): runs a payload and resolves with its output
	opXor        = 1
	opSet        = 2
	opReturn     = 3
	opReject     = 4
	opAppend     = 5
	opMember     = 6
	opCall       = 7
	opCopy       = 8
	opQueue      = 9 // the instruction queue
	opWindow     = 10
	opFindScript = 11
	opSelf       = 12
	opCallVoid   = 13
	opJSONParse  = 14
	opJSONString = 15
	opXorKey     = 16
	opCallAsync  = 17
	opAtob       = 18
	opBtoa       = 19
	opCondEq     = 20
	opAbsDiff    = 21
	opSchedule   = 22
	opDefined    = 23
	opBind       = 24
	opNoop25     = 25
	opNoop26     = 26
	opRemove     = 27
	opNoop28     = 28
	opLessThan   = 29
	opDefAsync   = 30
	opMultiply   = 33
	opPromise    = 34
	opDivide     = 35
)

// vmValue is any JS-ish value: string, float64, bool, nil, []any, map[string]any
// or a vmFunc (a handler stored in a register).
type vmValue = any

// vmFunc is a handler. It receives the raw operand list and writes into the
// register map through the vm it closes over.
type vmFunc func(v *sentinelVM, args []vmValue) vmValue

type sentinelVM struct {
	regs    map[float64]vmValue
	key     string // register 16: XOR key = the request's "p"
	window  vmValue
	profile sentinelProfile
	steps   int

	result  string
	settled bool
	failed  error
}

// solveSentinelPayload decodes a dx blob and runs it, returning the program's
// output. key is the request's own "p" value.
func solveSentinelPayload(dx, key string) (string, error) {
	return solveSentinelPayloadFor(dx, key, defaultSentinelProfile(""))
}

// solveSentinelPayloadFor is solveSentinelPayload with an explicit identity, so
// a caller can pin the browser surface the fingerprint programs observe.
func solveSentinelPayloadFor(dx, key string, profile sentinelProfile) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dx))
	if err != nil {
		return "", fmt.Errorf("sentinel dx: base64: %w", err)
	}
	decoded := xorSentinelString(string(raw), key)
	var program [][]vmValue
	dec := json.NewDecoder(strings.NewReader(decoded))
	dec.UseNumber()
	if err := dec.Decode(&program); err != nil {
		return "", fmt.Errorf("sentinel dx: decode program: %w", err)
	}
	vm := newSentinelVMFor(key, profile)
	return vm.run(program)
}

func newSentinelVM(key string) *sentinelVM {
	return newSentinelVMFor(key, defaultSentinelProfile(""))
}

func newSentinelVMFor(key string, profile sentinelProfile) *sentinelVM {
	vm := &sentinelVM{
		regs:    map[float64]vmValue{},
		key:     key,
		profile: profile,
		window:  newSentinelWindowFor(profile),
	}
	vm.installHandlers()
	return vm
}

// installHandlers seeds registers 0..35 with the opcode handlers, matching the
// SDK's Sn map. Programs copy these into float-named registers on entry.
func (v *sentinelVM) installHandlers() {
	h := map[int]vmFunc{
		opRunner: func(v *sentinelVM, a []vmValue) vmValue { return v.opRunner(a) },
		opXor: func(v *sentinelVM, a []vmValue) vmValue {
			v.set(a[0], xorSentinelString(v.str(v.get(a[0])), v.str(v.get(a[1]))))
			return nil
		},
		opSet: func(v *sentinelVM, a []vmValue) vmValue { v.set(a[0], a[1]); return nil },
		// return/reject are only ever reached through a call, so their operand
		// arrives already resolved — using it directly is what the SDK does.
		opReturn: func(v *sentinelVM, a []vmValue) vmValue {
			v.settle(base64.StdEncoding.EncodeToString([]byte(v.str(a[0]))))
			return nil
		},
		opReject: func(v *sentinelVM, a []vmValue) vmValue {
			v.settle(base64.StdEncoding.EncodeToString([]byte(v.str(a[0]))))
			return nil
		},
		opAppend: func(v *sentinelVM, a []vmValue) vmValue { return v.opAppend(a) },
		opMember: func(v *sentinelVM, a []vmValue) vmValue {
			v.set(a[0], v.member(v.get(a[1]), v.str(v.get(a[2]))))
			return nil
		},
		opCall:       func(v *sentinelVM, a []vmValue) vmValue { return v.opCall(a) },
		opCopy:       func(v *sentinelVM, a []vmValue) vmValue { v.set(a[0], v.get(a[1])); return nil },
		opFindScript: func(v *sentinelVM, a []vmValue) vmValue { return v.opFindScript(a) },
		opCallVoid:   func(v *sentinelVM, a []vmValue) vmValue { return v.opCallVoid(a) },
		opJSONParse:  func(v *sentinelVM, a []vmValue) vmValue { return v.opJSONParse(a) },
		opJSONString: func(v *sentinelVM, a []vmValue) vmValue { return v.opJSONString(a) },
		opCallAsync:  func(v *sentinelVM, a []vmValue) vmValue { return v.opCallAsync(a) },
		opAtob:       func(v *sentinelVM, a []vmValue) vmValue { v.set(a[0], v.decodeBase64(v.get(a[0]))); return nil },
		opBtoa: func(v *sentinelVM, a []vmValue) vmValue {
			v.set(a[0], base64.StdEncoding.EncodeToString([]byte(v.str(v.get(a[0])))))
			return nil
		},
		opCondEq:   func(v *sentinelVM, a []vmValue) vmValue { return v.opCondEq(a) },
		opAbsDiff:  func(v *sentinelVM, a []vmValue) vmValue { return v.opAbsDiff(a) },
		opSchedule: func(v *sentinelVM, a []vmValue) vmValue { return v.opSchedule(a) },
		opDefined:  func(v *sentinelVM, a []vmValue) vmValue { return v.opDefined(a) },
		opBind:     func(v *sentinelVM, a []vmValue) vmValue { return v.opBind(a) },
		opNoop25:   func(v *sentinelVM, a []vmValue) vmValue { return nil },
		opNoop26:   func(v *sentinelVM, a []vmValue) vmValue { return nil },
		opRemove:   func(v *sentinelVM, a []vmValue) vmValue { return v.opRemove(a) },
		opNoop28:   func(v *sentinelVM, a []vmValue) vmValue { return nil },
		opLessThan: func(v *sentinelVM, a []vmValue) vmValue {
			v.set(a[0], v.num(v.get(a[1])) < v.num(v.get(a[2])))
			return nil
		},
		opDefAsync: func(v *sentinelVM, a []vmValue) vmValue { return v.opDefAsync(a) },
		opMultiply: func(v *sentinelVM, a []vmValue) vmValue {
			v.set(a[0], v.num(v.get(a[1]))*v.num(v.get(a[2])))
			return nil
		},
		opPromise: func(v *sentinelVM, a []vmValue) vmValue { return v.opPromise(a) },
		opDivide:  func(v *sentinelVM, a []vmValue) vmValue { return v.opDivide(a) },
	}
	for id, fn := range h {
		v.regs[float64(id)] = fn
	}
	v.regs[opQueue] = []vmValue{}
	v.regs[opWindow] = v.window
	v.regs[opXorKey] = v.key
	v.regs[opSelf] = v.regs
}

func (v *sentinelVM) run(program [][]vmValue) (string, error) {
	queue := make([]vmValue, len(program))
	for i, ins := range program {
		queue[i] = ins
	}
	v.regs[opQueue] = queue
	if err := v.drain(); err != nil {
		return "", err
	}
	if v.failed != nil {
		return "", v.failed
	}
	if !v.settled {
		return "", fmt.Errorf("sentinel vm: program produced no result")
	}
	return v.result, nil
}

func (v *sentinelVM) settle(out string) {
	if v.settled {
		return
	}
	v.settled = true
	v.result = out
}

// drain executes the instruction queue until it empties or the program settles.
// The queue lives in register 9 and a program may replace it wholesale, so every
// iteration re-reads the register rather than holding a private slice.
func (v *sentinelVM) drain() error {
	for {
		queue, ok := v.regs[opQueue].([]vmValue)
		if !ok || len(queue) == 0 {
			return nil
		}
		ins, ok := queue[0].([]vmValue)
		if !ok {
			return fmt.Errorf("sentinel vm: instruction is not an array: %v", queue[0])
		}
		v.regs[opQueue] = queue[1:]
		if len(ins) == 0 {
			continue
		}
		fn, ok := v.get(ins[0]).(vmFunc)
		if !ok {
			return fmt.Errorf("sentinel vm: no handler for opcode %v", ins[0])
		}
		v.steps++
		if v.steps > 2_000_000 {
			return fmt.Errorf("sentinel vm: step limit reached")
		}
		if r := fn(v, ins[1:]); r != nil {
			if err, isErr := r.(error); isErr {
				v.failed = err
				return nil
			}
		}
		if v.settled {
			return nil
		}
	}
}

// --- register helpers ---

func (v *sentinelVM) get(key vmValue) vmValue {
	return v.regs[v.regKey(key)]
}

func (v *sentinelVM) set(key vmValue, value vmValue) {
	v.regs[v.regKey(key)] = value
}

func (v *sentinelVM) regKey(key vmValue) float64 {
	switch k := key.(type) {
	case float64:
		return k
	case json.Number:
		f, _ := k.Float64()
		return f
	case int:
		return float64(k)
	case string:
		if f, err := strconv.ParseFloat(k, 64); err == nil {
			return f
		}
	}
	return math.NaN()
}

// str mirrors JS String(): numbers keep their literal form, nil is "undefined".
func (v *sentinelVM) str(value vmValue) string {
	switch x := value.(type) {
	case nil:
		return "undefined"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return formatJSNumber(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return formatJSNumber(f)
		}
		return x.String()
	case []vmValue:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, v.str(item))
		}
		return strings.Join(parts, ",")
	case *sentinelObject:
		// JS String(obj) is "[object Object]"; the fingerprint programs never
		// rely on that, but an object must not leak Go's %v rendering.
		data, err := json.Marshal(x)
		if err != nil {
			return "[object Object]"
		}
		return string(data)
	case vmFunc:
		return "function () { [native code] }"
	default:
		return fmt.Sprint(value)
	}
}

func (v *sentinelVM) num(value vmValue) float64 {
	switch x := value.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case int:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return math.NaN()
}

// formatJSNumber renders a float the way JS Number#toString does for the values
// the programs carry (integer-valued floats lose the fraction).
func formatJSNumber(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.Trunc(f) == f && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func (v *sentinelVM) decodeBase64(value vmValue) string {
	raw, err := base64.StdEncoding.DecodeString(v.str(value))
	if err != nil {
		return ""
	}
	return string(raw)
}

// jsAdd mirrors the JS "+" operator for the operand types the programs use.
func (v *sentinelVM) jsAdd(left, right vmValue) vmValue {
	if _, ok := left.(string); ok {
		return v.str(left) + v.str(right)
	}
	if _, ok := right.(string); ok {
		return v.str(left) + v.str(right)
	}
	return v.num(left) + v.num(right)
}

// --- opcode handlers ---

// opRunner implements opcode 0: run a payload held in a register and settle with
// its output. The payload is [key, program] where key XORs the encoded program.
func (v *sentinelVM) opRunner(a []vmValue) vmValue {
	if len(a) < 1 {
		return nil
	}
	payload, ok := v.get(a[0]).([]vmValue)
	if !ok || len(payload) < 2 {
		return nil
	}
	key := v.str(payload[0])
	raw, err := base64.StdEncoding.DecodeString(v.str(payload[1]))
	if err != nil {
		return err
	}
	var program [][]vmValue
	dec := json.NewDecoder(strings.NewReader(xorSentinelString(string(raw), key)))
	dec.UseNumber()
	if err := dec.Decode(&program); err != nil {
		return err
	}
	queue := make([]vmValue, len(program))
	for i, ins := range program {
		queue[i] = ins
	}
	saved := v.regs[opQueue]
	v.regs[opQueue] = queue
	err = v.drain()
	v.regs[opQueue] = saved
	return err
}

func (v *sentinelVM) opAppend(a []vmValue) vmValue {
	if len(a) < 2 {
		return nil
	}
	current := v.get(a[0])
	if list, ok := current.([]vmValue); ok {
		v.set(a[0], append(list, v.get(a[1])))
		return nil
	}
	v.set(a[0], v.jsAdd(current, v.get(a[1])))
	return nil
}

func (v *sentinelVM) opCall(a []vmValue) vmValue {
	if len(a) < 1 {
		return nil
	}
	args := make([]vmValue, 0, len(a)-1)
	for _, arg := range a[1:] {
		args = append(args, v.get(arg))
	}
	return v.invoke(v.get(a[0]), args)
}

func (v *sentinelVM) opCallVoid(a []vmValue) vmValue {
	if len(a) < 2 {
		return nil
	}
	// The SDK forwards the variadic tail unresolved.
	return v.invoke(v.get(a[1]), a[2:])
}

func (v *sentinelVM) opCallAsync(a []vmValue) vmValue {
	if len(a) < 2 {
		return nil
	}
	args := make([]vmValue, 0, len(a)-2)
	for _, arg := range a[2:] {
		args = append(args, v.get(arg))
	}
	result := v.invoke(v.get(a[1]), args)
	v.set(a[0], result)
	return nil
}

func (v *sentinelVM) opJSONParse(a []vmValue) vmValue {
	if len(a) < 2 {
		return nil
	}
	var out vmValue
	dec := json.NewDecoder(strings.NewReader(v.str(v.get(a[1]))))
	dec.UseNumber()
	if dec.Decode(&out) == nil {
		v.set(a[0], out)
	}
	return nil
}

func (v *sentinelVM) opJSONString(a []vmValue) vmValue {
	if len(a) < 2 {
		return nil
	}
	if data, err := json.Marshal(v.get(a[1])); err == nil {
		v.set(a[0], string(data))
	}
	return nil
}

func (v *sentinelVM) opFindScript(a []vmValue) vmValue {
	// The SDK scans document.scripts for a src matching the pattern and falls
	// back to a literal path when nothing matches. Both branches feed the same
	// `.split("/").pop()`, so either way the reported value is the SDK file name.
	if len(a) < 2 {
		v.set(a[0], nil)
		return nil
	}
	pattern := v.str(v.get(a[1]))
	if src := v.matchScript(pattern); src != "" {
		v.set(a[0], src)
		return nil
	}
	v.set(a[0], "/sdk.js")
	return nil
}

// matchScript returns the src of the first document.scripts entry matching the
// pattern, or "" when none does.
func (v *sentinelVM) matchScript(pattern string) string {
	document, ok := v.member(v.window, "document").(*sentinelObject)
	if !ok {
		return ""
	}
	scripts, ok := document.values["scripts"].([]vmValue)
	if !ok {
		return ""
	}
	for _, entry := range scripts {
		el, ok := entry.(*sentinelObject)
		if !ok {
			continue
		}
		src := v.str(el.values["src"])
		if scriptPatternMatches(pattern, src) {
			return src
		}
	}
	return ""
}

// scriptPatternMatches tests a src against the SDK's script-matching pattern.
// Go's regexp is RE2 and has no lookahead, so the boundary assertion the
// pattern uses ((?=[?#]|$)) is rewritten as a consuming group — the caller only
// cares whether the src matches, not where the match ends.
func scriptPatternMatches(pattern, src string) bool {
	if pattern == "" {
		return false
	}
	re, err := regexp.Compile(strings.ReplaceAll(pattern, "(?=", "(?:"))
	if err != nil {
		return false
	}
	return re.MatchString(src)
}

func (v *sentinelVM) opCondEq(a []vmValue) vmValue {
	if len(a) < 3 || !vmStrictEqual(v.get(a[0]), v.get(a[1])) {
		return nil
	}
	return v.invoke(v.get(a[2]), a[3:])
}

func (v *sentinelVM) opAbsDiff(a []vmValue) vmValue {
	if len(a) < 4 {
		return nil
	}
	if math.Abs(v.num(v.get(a[0]))-v.num(v.get(a[1]))) <= v.num(v.get(a[2])) {
		return nil
	}
	return v.invoke(v.get(a[3]), a[4:])
}

func (v *sentinelVM) opSchedule(a []vmValue) vmValue {
	if len(a) < 1 {
		return nil
	}
	// a[0] is the destination, a[1:] are the raw instructions to run.
	saved := v.regs[opQueue]
	v.regs[opQueue] = append([]vmValue{}, a[1:]...)
	err := v.drain()
	v.regs[opQueue] = saved
	if err != nil {
		v.set(a[0], err.Error())
	}
	return nil
}

func (v *sentinelVM) opDefined(a []vmValue) vmValue {
	if len(a) < 2 || v.get(a[0]) == nil {
		return nil
	}
	return v.invoke(v.get(a[1]), a[2:])
}

func (v *sentinelVM) opBind(a []vmValue) vmValue {
	if len(a) < 3 {
		return nil
	}
	// The programs bind browser methods (canvas.getContext, gl.getExtension, ...);
	// the receiver travels with the resolved member so a later call dispatches it.
	v.set(a[0], sentinelMember{recv: v.get(a[1]), name: v.str(v.get(a[2]))})
	return nil
}

func (v *sentinelVM) opRemove(a []vmValue) vmValue {
	if len(a) < 2 {
		return nil
	}
	current := v.get(a[0])
	if list, ok := current.([]vmValue); ok {
		target := v.get(a[1])
		out := list[:0]
		for _, item := range list {
			if !vmStrictEqual(item, target) {
				out = append(out, item)
			}
		}
		v.set(a[0], out)
		return nil
	}
	v.set(a[0], v.num(current)-v.num(v.get(a[1])))
	return nil
}

// opDefAsync defines register a[0] as a callable that, when invoked, runs the
// raw instruction list in a[1:] against the current register file.
func (v *sentinelVM) opDefAsync(a []vmValue) vmValue {
	if len(a) < 2 {
		return nil
	}
	target := a[0]
	body := append([]vmValue{}, a[1:]...)
	v.set(target, vmFunc(func(v *sentinelVM, args []vmValue) vmValue {
		saved := v.regs[opQueue]
		v.regs[opQueue] = body
		err := v.drain()
		v.regs[opQueue] = saved
		return err
	}))
	return nil
}

func (v *sentinelVM) opPromise(a []vmValue) vmValue {
	if len(a) < 2 {
		return nil
	}
	v.set(a[0], v.get(a[1]))
	return nil
}

func (v *sentinelVM) opDivide(a []vmValue) vmValue {
	if len(a) < 3 {
		return nil
	}
	den := v.num(v.get(a[2]))
	if den == 0 {
		v.set(a[0], float64(0))
		return nil
	}
	v.set(a[0], v.num(v.get(a[1]))/den)
	return nil
}

// xorSentinelString XORs text with key cyclically over UTF-8 bytes, matching the
// SDK's Pn(). It is used both to decode a dx blob and by opcode 1.
func xorSentinelString(text, key string) string {
	if key == "" {
		return text
	}
	keyBytes := []byte(key)
	out := make([]byte, len(text))
	for i := 0; i < len(text); i++ {
		out[i] = text[i] ^ keyBytes[i%len(keyBytes)]
	}
	return string(out)
}

// vmStrictEqual mirrors JS === for the primitives the programs compare.
func vmStrictEqual(a, b vmValue) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case json.Number:
		y, ok := b.(json.Number)
		return ok && x.String() == y.String()
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	}
	return false
}

// sentinelScriptSrc is the sentinel SDK's own script URL for this identity. The
// SDK is served from /sentinel/<build>/sdk.js, so the path is derived from the
// client build number rather than hardcoded.
func sentinelScriptSrc(profile sentinelProfile) string {
	if profile.BuildNumber == "" {
		return "https://chatgpt.com/backend-api/sentinel/sdk.js"
	}
	return "https://chatgpt.com/sentinel/" + profile.BuildNumber + "/sdk.js"
}
