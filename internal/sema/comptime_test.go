package sema

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/diag"
	"lowlight/internal/types"
)

func checkComptimeSrc(t *testing.T, src string) (*CheckedProgram, *diag.Reporter) {
	t.Helper()
	rep := diag.NewReporter()
	prog := Check("comptime_test.ll", src, "", rep)
	return prog, rep
}

func assertComptimeError(t *testing.T, name, src, wantSubstr string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		_, rep := checkComptimeSrc(t, src)
		if !rep.HasErrors() {
			t.Fatalf("expected error containing %q, got none", wantSubstr)
		}
		if !strings.Contains(rep.Format(), wantSubstr) {
			t.Fatalf("expected diagnostic containing %q, got:\n%s", wantSubstr, rep.Format())
		}
	})
}

func TestComptimeCoreInterpreterControlFlowAndUnwinds(t *testing.T) {
	src := `
define LogItem struct [
	id: int,
	drops: List[int],
]

// Custom drop appends id to a shared List? Wait: List value semantics mean &List[int] parameter
// doesn't store in struct; test LIFO Drop via returning accumulated Drop count/behavior via
// block/func unwinds, match, result chains, and local cl closures.

define ParseErr result [
	value: int,
	BadNum: string,
	Empty,
]

const parse_or_default = fn(flag: bool) int: {
	let r! = if(flag): ParseErr.value(42) else: ParseErr.BadNum("oops")
	r!:
		when BadNum(msg): func <- -10 - msg.len()
		default: 0
}

const control_flow_suite = fn() int: {
	let sum = 0
	// for range + next <- + labeled block unwind
	let blk_res = blk: {
		for(i in 0..10): {
			if(i == 2): next <-
			if(i == 6): blk <- sum
			sum += i
		}
		-1
	}

	// value-yielding for with for <- and else
	let loop_list = for(i in 0..5): {
		if(i == 3): for <- [i * 10]
		i
	} else: [-1]
	let loop_val = loop_list[0]

	// while with else
	let w = 0
	let while_res = while(w < 3): {
		w += 1
		0
	} else: 100

	// collecting for -> [int], #[int], #[string: int]
	let squares = for(k in 1..=4) [int]: k * k
	let uniq = for(k in [1, 2, 2, 3]) #[int]: k
	let m = for(k in ["a", "bb"]) #[string: int]: MapEntry[string, int][key: k.copy(), value: k.len()]

	// local cl closure capturing immutable local
	let base = 10
	let add_k = cl(delta: int) int: base + delta
	let c1 = add_k(5)
	let c2 = add_k(7)

	// match (0-arg, range, contains, unwrap)
	let mval = match():
		when base == 10 and squares.len() == 4: 1000
		default: 0

	let opt?: ?int = 9
	let opt_bonus = if(opt? as v): {
		let dup = v
		dup * 2
	} else: 0

	let fallback = opt? ?? 0
	blk_res + loop_val + while_res + squares[3] + uniq.len() + (m["bb"] ?? 0) + c1 + c2 + mval + opt_bonus + fallback + parse_or_default(false) + parse_or_default(true)
}

const OUT = control_flow_suite()
`
	prog, rep := checkComptimeSrc(t, src)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}
	sym := prog.EntryMod.Symbols["OUT"]
	if sym == nil || !sym.IsConstInt {
		t.Fatalf("expected constant integer OUT, got %#v", sym)
	}
	// Expected:
	// blk_res: 0 + 1 + 3 + 4 + 5 = 13
	// loop_val: 30
	// while_res: 100
	// squares[3]: 16
	// uniq.len(): 3
	// m["bb"] ?? 0: 2
	// c1 + c2: 15 + 17 = 32
	// mval: 1000
	// opt_bonus: 18
	// fallback: 9
	// parse_or_default(false): -10 - 4 = -14
	// parse_or_default(true): 42
	// Total = 13 + 30 + 100 + 16 + 3 + 2 + 32 + 1000 + 18 + 9 - 14 + 42 = 1251
	if sym.ConstIntVal != 1251 {
		t.Fatalf("OUT = %d, want 1251", sym.ConstIntVal)
	}
}

func TestComptimeArithmeticParityFloatHashAndCollections(t *testing.T) {
	src := `
const WRAP_U8: u8 = u8(200) + u8(100)
const SUB_U8: u8 = u8(1) - u8(2)
const WRAP_I8: i8 = i8(127) + i8(1)
const NEG_MIN_I8: i8 = -i8(-128)

const H_POS = (0.0).hash()
const H_NEG = (-0.0).hash()
const BITS_ONE = (1.0).to_bits()
const F32_BITS = f32(1.0).to_bits()

const CHECK_ALL = fn() int: {
	// List, Map, Set, string methods via evaluator hooks
	let xs = List[int].new()
	xs.push(10)
	xs.push(20)
	let _ = xs.set(0, 99)
	let popped? = xs.pop()

	let mp = Map[string, int].new()
	mp.insert("alpha", 11)
	mp.insert("beta", 22)

	let st1 = Set[int].new()
	let _ = st1.insert(1)
	let _ = st1.insert(2)
	let st2 = Set[int].new()
	let _ = st2.insert(2)
	let _ = st2.insert(3)
	let inter = st1.intersection(st2)

	let raw = "  a,bb,ccc  "
	let parts = raw.trim().split(",")

	assert(H_POS == H_NEG, "-0.0 hash matches +0.0 hash")
	assert(BITS_ONE == u64(0x3FF0000000000000), "IEEE-754 1.0 bits")
	assert(F32_BITS == u32(0x3F800000), "IEEE-754 f32 1.0 bits")

	xs.len() + (popped? ?? 0) + (mp.get("alpha") ?? 0) + inter.len() + parts.len() + int(WRAP_U8) + int(SUB_U8) + int(WRAP_I8) + int(NEG_MIN_I8)
}

const TOTAL = CHECK_ALL()
`
	prog, rep := checkComptimeSrc(t, src)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}

	assertIntSym := func(name string, want int64) {
		t.Helper()
		sym := prog.EntryMod.Symbols[name]
		if sym == nil || sym.ConstVal == nil {
			t.Fatalf("missing evaluated symbol %q", name)
		}
		cv := sym.ConstVal.(*ctVal)
		if cv.asInt64() != want {
			t.Fatalf("%s = %d, want %d", name, cv.asInt64(), want)
		}
	}

	assertIntSym("WRAP_U8", 44)
	assertIntSym("SUB_U8", 255)
	assertIntSym("WRAP_I8", -128)
	assertIntSym("NEG_MIN_I8", -128)
	// TOTAL = 1 + 20 + 11 + 1 + 3 + 44 + 255 - 128 - 128 = 79
	assertIntSym("TOTAL", 79)
}

func TestComptimeLIFODropAndCallerLocation(t *testing.T) {
	src := `
define Tracer struct [
	id: int,
]

const Tracer.drop = fn(&self): {
	assert(self.id > 0, "tracer id must stay positive on drop")
}

// Test caller_file / caller_line / caller_location with @track_caller
@track_caller
const here_line = fn() int: caller_line()

const L1 = here_line()

const dropped_ok = fn() bool: {
	// Also verify custom Drop runs in comptime without failing
	let t1 = Tracer[id: 1]
	let t2 = Tracer[id: 2]
	t1.id + t2.id == 3
}

const D = dropped_ok()
`
	prog, rep := checkComptimeSrc(t, src)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}
	l1 := prog.EntryMod.Symbols["L1"]
	if l1 == nil || !l1.IsConstInt || l1.ConstIntVal != 14 {
		t.Fatalf("L1 = %#v, want line 14", l1)
	}
}

func TestComptimeSizeOfAlignOfMultiPassAndCrossModule(t *testing.T) {
	dir := t.TempDir()
	defsDir := filepath.Join(dir, "defs")
	if err := os.MkdirAll(defsDir, 0755); err != nil {
		t.Fatal(err)
	}
	modA := filepath.Join(defsDir, "defs.ll")
	mainFile := filepath.Join(dir, "main.ll")

	defsSrc := `
const SCALE = BASE * 2
const BASE = 4
`
	mainSrc := `
import "./defs" as defs

define Buffer struct [
	raw: [type.size_of[Header](): u8],
]

define Header struct [
	a: u64,
	b: u8,
]

define Flags enum [
	Read = 1 << 0,
	Write = 1 << defs.BASE,
]

const double = fn(x: int) int: x * 2
const ARR_LEN = double(defs.SCALE)

const HDR_SIZE = type.size_of[Header]()
const HDR_ALIGN = type.align_of[Header]()
const HDR_PLAIN = type.is_plain[Header]()
const STR_PLAIN = type.is_plain[string]()
const WRITE_FLAG = int(Flags.Write)

bind main = fn(): {
	let buf = [ARR_LEN: 0]
	let b = Buffer[raw: [type.size_of[Header](): u8(0)]]
	const LOCAL_K = 10
	let computed = comptime {
		let sum = 0
		for(i in 0..LOCAL_K): sum += i
		sum
	}
}
`
	if err := os.WriteFile(modA, []byte(defsSrc), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainFile, []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}

	rep := diag.NewReporter()
	prog := Check(mainFile, "", dir, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}

	bufTy := prog.EntryMod.Types["Buffer"].(*types.Struct)
	arrTy := bufTy.Fields[0].Type.(*types.StackArray)
	if arrTy.Size != 16 {
		t.Fatalf("Buffer.raw size = %d, want 16 (Header u64+u8 padded to align 8)", arrTy.Size)
	}

	if sym := prog.EntryMod.Symbols["HDR_SIZE"]; sym == nil || sym.ConstIntVal != 16 {
		t.Fatalf("HDR_SIZE = %#v, want 16", sym)
	}
	if sym := prog.EntryMod.Symbols["HDR_ALIGN"]; sym == nil || sym.ConstIntVal != 8 {
		t.Fatalf("HDR_ALIGN = %#v, want 8", sym)
	}
	if sym := prog.EntryMod.Symbols["ARR_LEN"]; sym == nil || sym.ConstIntVal != 16 {
		t.Fatalf("ARR_LEN = %#v, want 16", sym)
	}
	if sym := prog.EntryMod.Symbols["WRITE_FLAG"]; sym == nil || sym.ConstIntVal != 16 {
		t.Fatalf("WRITE_FLAG = %#v, want 16", sym)
	}
	if cv := prog.EntryMod.Symbols["HDR_PLAIN"].ConstVal.(*ctVal); !cv.b {
		t.Fatalf("expected Header to be plain")
	}
	if cv := prog.EntryMod.Symbols["STR_PLAIN"].ConstVal.(*ctVal); cv.b {
		t.Fatalf("expected string not to be plain")
	}
}

func TestComptimeColoringErrorsAndTraps(t *testing.T) {
	assertComptimeError(t, "literal u8 overflow", `
const B: u8 = 256
`, "out of range for u8")

	assertComptimeError(t, "LIFO drop order verification", `
define Bomb struct [
	tag: string,
]
const Bomb.drop = fn(&self): {
	assert(false, self.tag)
}
const trigger_lifo = fn() int: {
	let first = Bomb[tag: "dropped_first_var"]
	let second = Bomb[tag: "dropped_second_var_lifo"]
	0
}
const X = trigger_lifo()
`, "dropped_second_var_lifo")

	assertComptimeError(t, "div by zero trap", `
const BAD = fn() int: 10 / 0
const X = BAD()
`, "division by zero")

	assertComptimeError(t, "bad shift count", `
const X = 1 << 64
`, "shift amount")

	assertComptimeError(t, "invalid char code point", `
const C = char(0xD800)
`, "invalid char code point")

	assertComptimeError(t, "oob slice trap", `
const S = "abc"[0..5]
`, "out of bounds")

	assertComptimeError(t, "assert trap with call trace", `
const inner = fn(): assert(false, "boom")
const outer = fn() int: { inner(); 1 }
const X = outer()
`, "called from `outer`")

	assertComptimeError(t, "compile_error custom message", `
@comptime_only
const fail_it = fn() int: {
	comp.compile_error("bad target config")
	0
}
const X = fail_it()
`, "bad target config")

	assertComptimeError(t, "compile_error rejected in runtime code", `
bind main = fn(): {
	comp.compile_error("nope")
}
`, "`@comptime_only` builtin `comp.compile_error`")

	// comp.log emits non-fatal Note diagnostic cleanly
	_, logRep := checkComptimeSrc(t, `
@comptime_only
const checked = fn() int: {
	comp.log("hello from comp.log")
	42
}
const LOG_VAL = checked()
`)
	if logRep.HasErrors() {
		t.Fatalf("unexpected error with comp.log:\n%s", logRep.Format())
	}
	if !strings.Contains(logRep.Format(), "note: hello from comp.log") {
		t.Fatalf("expected comp.log note diagnostic, got:\n%s", logRep.Format())
	}

	assertComptimeError(t, "comptime_only rejected from runtime", `
@comptime_only
const helper = fn() int: 1
bind main = fn(): {
	let _ = helper()
}
`, "cannot call `@comptime_only`")

	assertComptimeError(t, "comptime_only rejected on bind", `
@comptime_only
bind helper = fn() int: 1
`, "only be applied to a `const`")

	assertComptimeError(t, "bind-only call rejected in const fn", `
bind rt = fn() int: 1
const bad = fn() int: rt()
`, "runtime-only (`bind`) function")

	assertComptimeError(t, "unsafe rejected in const fn", `
const bad = fn() int: unsafe { 1 }
`, "`unsafe` is not allowed")

	assertComptimeError(t, "runtime local rejected inside comptime block", `
bind main = fn(): {
	let r = 5
	let _ = comptime (r + 1)
}
`, "cannot reference runtime variable")

	assertComptimeError(t, "step limit exceeded", `
const spin = fn() int: {
	let i = 0
	for(i < 2000000): i += 1
	i
}
const X = spin()
`, "exceeded step limit")

	// Raising @comptime_limit succeeds (400k iterations ~ 2M steps > default 1M, < 3M)
	_, rep := checkComptimeSrc(t, `
@comptime_limit(3000000)
const spin = fn() int: {
	let i = 0
	for(i < 400000): i += 1
	i
}
const X = spin()

@comptime_limit(steps: 3_000_000)
const spin_named = fn() int: {
	let i = 0
	for(i < 400000): i += 1
	i
}
const Y = spin_named()

// Raising memory limit via named argument allows allocations above the 64 MB default
@comptime_limit(steps: 5_000_000, memory: 350_000_000)
const big_str_over_64mb = fn() int: {
	let s = "0123456789abcdef" // 16 B
	for(i in 0..22): { // grows past 64 MB (default budget rejects this)
		s.push(s.copy())
	}
	s.len()
}
const Z = big_str_over_64mb()
`)
	if rep.HasErrors() {
		t.Fatalf("expected @comptime_limit(steps: ..., memory: ...) to succeed, got:\n%s", rep.Format())
	}

	assertComptimeError(t, "over-64mb string fails without memory limit override", `
@comptime_limit(steps: 5_000_000)
const big_str_default_fail = fn() int: {
	let s = "0123456789abcdef"
	for(i in 0..22): {
		s.push(s.copy())
	}
	s.len()
}
const Z = big_str_default_fail()
`, "memory budget (64 MB")

	assertComptimeError(t, "unknown @comptime_limit named arg rejected", `
@comptime_limit(bogus: 100)
const f = fn() int: 1
`, "unknown argument")

	// Recursion depth limit exceeded
	assertComptimeError(t, "recursion depth limit exceeded", `
const rec = fn(n: int) int: if(n <= 0): 0 else: 1 + rec(n - 1)
const X = rec(1500)
`, "stack depth exceeded")
}

func TestComptimeCrossBoundaryValidation(t *testing.T) {
	// Allowed runtime-crossing values (including @comptime_only Map/Set/Drop)
	_, rep := checkComptimeSrc(t, `
define Node struct [
	val: int,
	next?: ?Node,
]
define CustomDrop struct [ id: int ]
const CustomDrop.drop = fn(&self): {}

@comptime_only
const ONLY_MAP = #["a": 1, "b": 2]

@comptime_only
const ONLY_SET = #[10, 20]

@comptime_only
const ONLY_DROP = CustomDrop[id: 7]

const OK_LIST_STRS = for(i in 0..3) [string]: "k$i"
const OK_NONE_BOXED?: ?Node = none
const OK_OPT_VAL?: ?int = 42
const OK_FN = fn(x: int) int: x + 1

bind main = fn(): {
	let xs = comptime {
		let m = #["x": 10, "y": 20]
		[(m["x"] ?? 0), (m["y"] ?? 0)]
	}
	let n = comptime ONLY_MAP["b"] ?? 0
}
`)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}

	assertComptimeError(t, "reject non-comptime_only top const Map", `
const M = #["a": 1]
`, "cannot cross from compile time to runtime")

	assertComptimeError(t, "reject non-comptime_only top const Set", `
const S = #[1, 2, 3]
`, "cannot cross from compile time to runtime")

	assertComptimeError(t, "reject custom Drop struct crossing", `
define Locker struct [ id: int ]
const Locker.drop = fn(&self): {}
const L = Locker[id: 1]
`, "with custom `Drop` cannot cross")

	assertComptimeError(t, "reject non-none boxed optional crossing", `
define Node struct [ v: int, next?: ?Node ]
const BAD_BOX?: ?Node = Node[v: 1, next?: none]
`, "non-none boxed optional")

	assertComptimeError(t, "reject Map escaping comptime block to runtime", `
bind main = fn(): {
	let m = comptime #["a": 1]
}
`, "cannot cross from compile time to runtime")

	assertComptimeError(t, "reject Set in local const inside runtime fn", `
bind main = fn(): {
	const S = #[1, 2]
}
`, "cannot cross from compile time to runtime")

	assertComptimeError(t, "reject Arena in empty crossing list", `
const A_LIST: [Arena] = []
`, "Arena` cannot cross")

	assertComptimeError(t, "reject ptr in empty crossing list", `
const P_LIST: [ptr[u8]] = []
`, "pointer type")
}

func TestComptimeReviewPreDecisionFixes(t *testing.T) {
	// B1 (to_string infinite recursion on List/Map/Set), B2 (& references, chars, Map.update),
	// B3 (float/f32 formatting parity via format_float_prec), B7 (@comptime_limit with underscores).
	_, rep := checkComptimeSrc(t, `
define Boxed struct [ count: int, label: string ]

const inc = fn(x: &int): { x += 1 }
const add_prefix = fn(b: &Boxed, p: string): {
	b.count += 10
	b.label = p + b.label
}
const double_entry = fn(v: &int): { v *= 2 }
const read_entry = fn(v: int) int: v * 2

@comptime_only
const test_fixes = fn() bool: {
	// B1: List/Map/Set to_string
	assert([1, 2, 3].to_string() == "[1, 2, 3]")
	assert(#["a": 1, "b": 2].to_string() == "#[a: 1, b: 2]")
	assert(#[10, 20].to_string() == "#[10, 20]")

	// B2: &int, &Struct field, &string, chars() UTF-8, Map.update/with with lambda & fn
	let n = 41
	inc(&n)
	assert(n == 42)

	let bx = Boxed[count: 5, label: "ok"]
	add_prefix(&bx, "pre:")
	assert(bx.count == 15 and bx.label == "pre:ok")

	let cs = "héllo".chars()
	assert(cs.len() == 5 and cs[1] == 'é')

	let m = #["x": 3, "y": 5]
	let updated = m.update("x", cl(v: &int): { v += 7 })
	assert(updated and (m["x"] ?? 0) == 10)
	assert(m.update("y", double_entry) and (m["y"] ?? 0) == 10)
	let via_with = m.with("x", read_entry) ?? 0
	assert(via_with == 20)

	// B3: runtime-identical float / f32 formatting
	assert("${0.1 + 0.2}" == "0.3")
	assert("${1.0 / 3.0}" == "0.333333")
	assert("${1.0 / 0.0}" == "inf")
	assert("${-1.0 / 0.0}" == "-inf")
	assert("${0.0 / 0.0}" == "nan")
	let f: f32 = f32(0.5)
	assert("${f}" == "0.5")

	true
}

@comptime_limit(50_000_000)
const big_loop = fn() int: {
	let s = 0
	for(i in 0..10): { s += i }
	s
}

const OK_ALL = test_fixes()
const OK_LIMIT = big_loop()
`)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}

	// B5: string doubling via s.push(s) and s += s aborts cleanly on 64 MB budget
	assertComptimeError(t, "string push doubling hits memory budget", `
const blow_up_push = fn() string: {
	let s = "0123456789abcdef"
	for(i in 0..40): {
		s.push(s)
	}
	s
}
const X = blow_up_push()
`, "memory budget")

	assertComptimeError(t, "string add_assign doubling hits memory budget", `
const blow_up_add_assign = fn() string: {
	let s = "0123456789abcdef"
	for(i in 0..40): {
		s += s
	}
	s
}
const X = blow_up_add_assign()
`, "memory budget")

	// B8: collapsed recursion frames, no runtime main frame, Related populated
	_, recRep := checkComptimeSrc(t, `
const deep = fn(n: int) int: {
	if(n <= 0): {
		assert(false, "bottom")
		func <- 0
	}
	deep(n - 1)
}
bind main = fn(): {
	let _ = comptime deep(10)
}
`)
	if !recRep.HasErrors() {
		t.Fatal("expected comptime assertion error in deep recursion")
	}
	recDiags := recRep.Diagnostics()
	if len(recDiags) == 0 || len(recDiags[0].Related) == 0 {
		t.Fatalf("expected Related trace entries on comptime abort, got: %+v", recDiags)
	}
	if strings.Contains(recDiags[0].Message, "\n  note:") {
		t.Fatalf("expected clean single-line primary Message, got: %q", recDiags[0].Message)
	}
	recFormatted := recRep.Format()
	if !strings.Contains(recFormatted, "more calls to `deep`") {
		t.Fatalf("expected collapsed recursion note, got:\n%s", recFormatted)
	}
	if strings.Contains(recFormatted, "from `main`") {
		t.Fatalf("unexpected runtime `main` frame in comptime stack trace:\n%s", recFormatted)
	}

	// B8: std/ abort lifts primary span to user call site
	_, stdRep := checkComptimeSrc(t, `
const fail_std = fn() int: {
	let xs = [1, 2]
	xs.must_get(99)
}
const X = fail_std()
`)
	if !stdRep.HasErrors() {
		t.Fatal("expected out of bounds error from must_get")
	}
	stdDiags := stdRep.Diagnostics()
	if strings.Contains(stdDiags[0].Span.Start.File, "std/") {
		t.Fatalf("expected primary diagnostic span lifted out of std/, got %s", stdDiags[0].Span.Start.File)
	}

	// B9: cycle reporting prints ordered chain A -> B -> A and distinguishes const vs bind
	_, cycRep := checkComptimeSrc(t, `
const A: int = B + 1
const B: int = A + 1
`)
	if !cycRep.HasErrors() {
		t.Fatal("expected cycle error between const A and B")
	}
	cycDiags := cycRep.Diagnostics()
	if !strings.Contains(cycDiags[0].Message, "`const`") || !strings.Contains(cycDiags[0].Message, "A -> B -> A") {
		t.Fatalf("expected const cycle chain A -> B -> A, got: %q", cycDiags[0].Message)
	}
	if len(cycDiags[0].Related) < 2 {
		t.Fatalf("expected Related cycle spans, got: %+v", cycDiags[0].Related)
	}

	// H2 / B6: Conditional generic `const` functions allow `bind` types at runtime while rejecting
	// them in comptime contexts with chained Related root causes.
	_, condOkRep := checkComptimeSrc(t, `
define P struct [ n: int ]
const P.to_string = fn(self) string: "P${self.n}"

define U struct [ n: int ]
bind U.to_string = fn(self) string: "U${self.n}"

const show[T: Printable] = fn(x: T) string: "<${x.to_string()}>"
const nested[T: Printable] = fn(x: T) string: show(x)

const CT_P = show(P[n: 3])
const CT_NESTED_P = nested(P[n: 4])

bind main = fn(): {
	let _ = show(U[n: 3])
	let _ = nested(U[n: 5])
	let _ = show(5)
}
`)
	if condOkRep.HasErrors() {
		t.Fatalf("expected conditional generic `show[U]` in runtime `main` and `show[P]` in `const` to succeed, got:\n%s", condOkRep.Format())
	}

	_, condFailRep := checkComptimeSrc(t, `
define U struct [ n: int ]
bind U.to_string = fn(self) string: "U${self.n}"

const show[T: Printable] = fn(x: T) string: "<${x.to_string()}>"
const nested[T: Printable] = fn(x: T) string: show(x)

const BAD = nested(U[n: 3])
`)
	if !condFailRep.HasErrors() {
		t.Fatal("expected calling downgraded `nested[U]` in `const BAD` to fail")
	}
	cfDiags := condFailRep.Diagnostics()
	if len(cfDiags[0].Related) < 3 {
		t.Fatalf("expected chained Related notes (nested -> show -> U.to_string), got: %+v", cfDiags[0].Related)
	}
	if !strings.Contains(condFailRep.Format(), "U.to_string") {
		t.Fatalf("expected diagnostic trace to name root cause `U.to_string`, got:\n%s", condFailRep.Format())
	}
	if !strings.Contains(condFailRep.Format(), "nested[U]") || !strings.Contains(condFailRep.Format(), "show[U]") || strings.Contains(condFailRep.Format(), "ll_s_") {
		t.Fatalf("B12: expected user-facing `nested[U]` / `show[U]` without mangled `ll_s_`, got:\n%s", condFailRep.Format())
	}

	// H11.5: `@comptime_only` functions cannot cross as runtime `fn` pointers or baked tables
	assertComptimeError(t, "reject @comptime_only fn in baked table", `
@comptime_only
const ct_fn = fn(x: int) int: x + 1
const TBL = [ct_fn]
`, "`@comptime_only` function")

	assertComptimeError(t, "reject @comptime_only fn value in runtime code", `
@comptime_only
const ct_fn = fn(x: int) int: x + 1
bind main = fn(): {
	let _ = ct_fn
}
`, "`@comptime_only`")
}

func TestComptimeSimulatedHeapStdCoreAndH11Fixes(t *testing.T) {
	// Custom hash/eq in const Map & Set, float.to_string parity, Resource List/Map/Set drops,
	// Error/Result at comptime, recursive depth > 256 (H8 = 1024), and user unsafe rejection.
	src := `
define Key struct [ k: int ]
const Key.hash = fn(self) u64: u64(self.k * 31)
const Key.eq = fn(self, other: Key) bool: self.k == other.k
const Key.copy = fn(self) Key: Key[k: self.k]

const deep_sum = fn(n: int) int: if(n <= 0): 0 else: 1 + deep_sum(n - 1)

const test_heap_core = fn() bool: {
	// Real std/core float formatting & parsing parity
	assert((0.1 + 0.2).to_string() == "0.3")
	let f = parse_float("12.5") ?? 0.0
	assert(f == 12.5)
	assert((parse_int_radix("ff", 16) ?? 0) == 255)

	// String slicing, push, split, join
	let s = "abc"
	s.push(s.copy())
	assert(s == "abcabc")
	let parts = "x,y,z".split(",")
	assert(join_strings(parts, "|") == "x|y|z")

	// Map & Set with custom hash/eq
	let m: Map[Key, List[string]] = #[]
	m.insert(Key[k: 1], ["alpha", "beta"])
	m.insert(Key[k: 2], ["gamma"])
	let removed? = m.remove(Key[k: 1])
	assert((removed? ?? []).to_string() == "[alpha, beta]")
	assert(m.len() == 1)

	let set_k: Set[Key] = #[Key[k: 10], Key[k: 10], Key[k: 20]]
	assert(set_k.len() == 2)
	assert(set_k.contains(Key[k: 20]))

	// Nested List[List[string]] move-out vs LIFO drop
	let outer: [[string]] = [["one", "two"], ["three"]]
	let taken = outer.must_remove(0)
	assert(taken.len() == 2 and outer.len() == 1)

	// Error & Result at comptime
	let e = Error.new("boom").wrap("context")
	assert(e.summary().contains("context"))

	// Recursive depth > 256 works up to 1024 (H8)
	assert(deep_sum(400) == 400)
	true
}

const OK_ALL = comptime test_heap_core()
const DEPTH_OK = deep_sum(400)

bind main = fn(): {
	assert(OK_ALL and DEPTH_OK == 400)
}
`
	rep := diag.NewReporter()
	prog := Check("comptime_heap_test.ll", src, "", rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}
	_ = prog

	// String interpolation `"$x"` must follow the same constness rules as `x.to_string()`
	assertComptimeError(t, "interp follows runtime-only to_string error", `
define RuntimeOnlyPrint struct [ v: int ]
bind RuntimeOnlyPrint.to_string = fn(self) string: "x"
const bad_interp = fn(x: RuntimeOnlyPrint) string: "${x}"
`, "RuntimeOnlyPrint.to_string")

	interpCondRep := diag.NewReporter()
	_ = Check("interp_cond.ll", `
define R struct [ v: int ]
bind R.to_string = fn(self) string: "r"
define P struct [ v: int ]
const P.to_string = fn(self) string: "p"
const fmt_any[T: Printable] = fn(x: T) string: "[${x}]"
const GOOD = fmt_any(P[v: 1])
const BAD = fmt_any(R[v: 1])
`, "", interpCondRep)
	if !interpCondRep.HasErrors() || !strings.Contains(interpCondRep.Format(), "R.to_string") {
		t.Fatalf("expected `fmt_any(R)` to fail citing `R.to_string`, got:\n%s", interpCondRep.Format())
	}

	// H11.6: `@comptime_only` types are rejected in runtime signatures, globals, and runtime construction
	assertComptimeError(t, "reject @comptime_only struct in bind fn signature", `
@comptime_only
define MetaInfo struct [ count: int ]
bind bad_fn = fn(m: MetaInfo) int: m.count
`, "`@comptime_only` type MetaInfo")

	assertComptimeError(t, "reject @comptime_only struct in non-@comptime_only global", `
@comptime_only
define MetaInfo struct [ count: int ]
const G = MetaInfo[count: 3]
`, "`@comptime_only` type MetaInfo")

	assertComptimeError(t, "reject @comptime_only struct construction in runtime body", `
@comptime_only
define MetaInfo struct [ count: int ]
bind main = fn(): {
	let _ = MetaInfo[count: 1]
}
`, "`@comptime_only` struct")

	// H11.6 valid usage: inside `@comptime_only` functions/globals and `comptime` blocks
	validMetaRep := diag.NewReporter()
	_ = Check("valid_meta_type.ll", `
@comptime_only
define MetaInfo struct [ count: int ]
@comptime_only
const make_meta = fn(n: int) MetaInfo: MetaInfo[count: n]
const TOTAL = comptime make_meta(42).count
`, "", validMetaRep)
	if validMetaRep.HasErrors() {
		t.Fatalf("unexpected errors for valid @comptime_only type usage:\n%s", validMetaRep.Format())
	}

	// B10: Compile-time Map operations with `string` keys and `string` values
	_, b10Rep := checkComptimeSrc(t, `
const b10_a = fn() int: {
	let m = #["a": 1]
	let v? = m.remove("a")
	assert((v? ?? 0) == 1)
	m.len()
}
const b10_b = fn() int: {
	let m = #[1: "x"]
	let v? = m.remove(1)
	assert((v? ?? "") == "x")
	m.len()
}
const b10_c = fn() string: {
	let m = #["a": "x"]
	m.insert("a", "y")
	m["a"] ?? ""
}
const b10_d = fn() int: {
	let m = Map[string, int].new()
	m.push(MapEntry[string, int][key: "k", value: 1])
	m.push(MapEntry[string, int][key: "k", value: 2])
	m["k"] ?? 0
}
const b10_comp = fn() string: {
	let m = for(k in ["x", "y"]) #[string: string]: MapEntry[string, string][key: k.copy(), value: "${k}!"]
	let old? = m.remove("x")
	m.insert("y", "updated")
	"${old? ?? ""}:${m["y"] ?? ""}:${m.len()}"
}
const R_A = b10_a()
const R_B = b10_b()
const R_C = b10_c()
const R_D = b10_d()
const R_COMP = b10_comp()
bind main = fn(): {
	assert(R_A == 0 and R_B == 0 and R_C == "y" and R_D == 2 and R_COMP == "x!:updated:1")
}
`)
	if b10Rep.HasErrors() {
		t.Fatalf("B10 string Map comptime error:\n%s", b10Rep.Format())
	}

	// B11: Real target-size memory accounting + freeing credits handle 200k list + 50k map under 64 MB
	_, b11Rep := checkComptimeSrc(t, `
@comptime_limit(50_000_000)
const big_containers = fn() int: {
	let xs: [int] = []
	for(i in 0..200000): xs.push(i * 3)
	let m = Map[int, int].new()
	for(i in 0..50000): m.insert(i, i)
	xs.len() + m.len()
}
const BIG = big_containers()
bind main = fn(): assert(BIG == 250000)
`)
	if b11Rep.HasErrors() {
		t.Fatalf("B11 200k list + 50k map exceeded budget unexpectedly:\n%s", b11Rep.Format())
	}
}

func BenchmarkComptimeCollectionsAndRecursion(b *testing.B) {
	src := `
const sum_to = fn(n: int) int: {
	let xs: [int] = []
	for(i in 0..n): xs.push(i)
	let acc = 0
	for(v in xs): acc += v
	acc
}
const OUT = sum_to(200)
`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rep := diag.NewReporter()
		_ = Check("bench.ll", src, "", rep)
		if rep.HasErrors() {
			b.Fatal(rep.Format())
		}
	}
}

func checkComptimeTargetSrc(t *testing.T, triple, src string) (*CheckedProgram, *diag.Reporter) {
	t.Helper()
	rep := diag.NewReporter()
	prog := CheckWithOptions(CheckOptions{
		EntryPath:    "target_test.ll",
		SrcOverride:  src,
		TargetTriple: triple,
	}, rep)
	return prog, rep
}

func TestPhase4CompTargetAndTopComptimeIf(t *testing.T) {
	// 1. Cross-target `comp.TARGET` introspection + top-level `comptime if` pruning
	topSrc := `
const WANT_64 = comp.TARGET.ptr_bits == 64

comptime if(comp.TARGET.os == comp.TargetOs.linux): {
	const PLATFORM_CODE = 1
	define PlatformInfo struct [ tag: int ]
	extern "unistd.h"
} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	const PLATFORM_CODE = 2
	define PlatformInfo struct [ mach_port: i32 ]
	bind darwin_only = fn() int: 77
} else comptime if(comp.TARGET.os == comp.TargetOs.windows): {
	const PLATFORM_CODE = 3
	define PlatformInfo struct [ handle: i64 ]
} else: {
	const PLATFORM_CODE = 4
	define PlatformInfo struct [ tag: int ]
}

comptime if(WANT_64): {
	comptime if(comp.TARGET.arch == comp.TargetArch.amd64): const ARCH_KIND = 10
	else: const ARCH_KIND = 20
} else: {
	const ARCH_KIND = 32
}

const DESCR: string = comptime "${comp.TARGET.os}:${comp.TARGET.arch}:${comp.TARGET.endian}:${comp.TARGET.ptr_bits}:${comp.TARGET.is_unix}"

// User can still define their own Target without colliding with comp.Target!
define Target struct [ x: int ]

bind main = fn(): {
	let mine = Target[x: 5]
	assert(mine.x == 5)
	assert(PLATFORM_CODE > 0 and ARCH_KIND > 0)
}
`
	cases := []struct {
		triple       string
		wantPlatform int64
		wantArch     int64
		wantDescr    string
		wantHeader   string
		hasDarwinFn  bool
	}{
		{"x86_64-unknown-linux-gnu", 1, 10, "TargetOs.linux:TargetArch.amd64:Endian.little:64:true", "unistd.h", false},
		{"aarch64-apple-darwin", 2, 20, "TargetOs.darwin:TargetArch.arm64:Endian.little:64:true", "", true},
		{"i686-pc-windows-msvc", 3, 32, "TargetOs.windows:TargetArch.x86:Endian.little:32:false", "", false},
		{"wasm32-unknown-unknown", 4, 32, "TargetOs.wasm:TargetArch.wasm32:Endian.little:32:false", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.triple, func(t *testing.T) {
			prog, rep := checkComptimeTargetSrc(t, tc.triple, topSrc)
			if rep.HasErrors() {
				t.Fatalf("unexpected errors for %s:\n%s", tc.triple, rep.Format())
			}
			pc := prog.EntryMod.Symbols["PLATFORM_CODE"]
			if pc == nil || pc.ConstIntVal != tc.wantPlatform {
				t.Fatalf("PLATFORM_CODE = %+v, want %d", pc, tc.wantPlatform)
			}
			ak := prog.EntryMod.Symbols["ARCH_KIND"]
			if ak == nil || ak.ConstIntVal != tc.wantArch {
				t.Fatalf("ARCH_KIND = %+v, want %d", ak, tc.wantArch)
			}
			descr := prog.EntryMod.Symbols["DESCR"]
			if descr == nil || descr.ConstVal == nil || descr.ConstVal.(*ctVal).s != tc.wantDescr {
				t.Fatalf("DESCR = %+v, want %q", descr, tc.wantDescr)
			}
			hasDarwin := prog.EntryMod.Symbols["darwin_only"] != nil
			if hasDarwin != tc.hasDarwinFn {
				t.Fatalf("darwin_only present = %v, want %v", hasDarwin, tc.hasDarwinFn)
			}
			hasHdr := len(prog.ExternHeaders) > 0 && prog.ExternHeaders[0] == tc.wantHeader
			if tc.wantHeader != "" && !hasHdr {
				t.Fatalf("expected extern header %q, got %v", tc.wantHeader, prog.ExternHeaders)
			}
			if tc.wantHeader == "" && len(prog.ExternHeaders) != 0 {
				t.Fatalf("expected untaken branch extern headers pruned, got %v", prog.ExternHeaders)
			}
		})
	}

	// 2. Untaken top-level branch may contain platform-specific code / compile_error cleanly.
	_, pruneOkRep := checkComptimeTargetSrc(t, "aarch64-apple-darwin", `
comptime if(comp.TARGET.os == comp.TargetOs.linux): {
	extern NO_SUCH_LINUX_SYM: fn(bad: LinuxMissingType)
	const NEVER_HIT = comp.compile_error("not linux")
} else: {
	const OK = 99
}
`)
	if pruneOkRep.HasErrors() {
		t.Fatalf("untaken branch should not be typechecked or evaluated:\n%s", pruneOkRep.Format())
	}

	// 3. `import` inside top-level `comptime if` is ALWAYS an error (H4 Option 3), even in untaken branches!
	_, impTakenRep := checkComptimeTargetSrc(t, "aarch64-apple-darwin", `
comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	import "std/fs"
}
`)
	if !impTakenRep.HasErrors() || !strings.Contains(impTakenRep.Format(), "`import` is not allowed inside top-level `comptime if`") {
		t.Fatalf("expected import inside taken top-level comptime if to fail, got:\n%s", impTakenRep.Format())
	}

	_, impUntakenRep := checkComptimeTargetSrc(t, "aarch64-apple-darwin", `
comptime if(comp.TARGET.os == comp.TargetOs.linux): {
	import "std/fs"
}
`)
	if !impUntakenRep.HasErrors() || !strings.Contains(impUntakenRep.Format(), "`import` is not allowed inside top-level `comptime if`") {
		t.Fatalf("expected import inside untaken top-level comptime if to fail, got:\n%s", impUntakenRep.Format())
	}

	// 4. `comp.TARGET`, `comp.TargetOs`, `comp.Target` must NOT be exposed bare without `comp.` or usable in runtime code.
	assertComptimeError(t, "bare TargetOs not in prelude", `
const X = TargetOs.linux
`, "undeclared")
	assertComptimeError(t, "comp.TARGET rejected in runtime code", `
bind main = fn(): {
	let _ = comp.TARGET.is_unix
}
`, "cannot use `@comptime_only` constant `comp.TARGET`")
	assertComptimeError(t, "comp.TargetOs rejected in runtime code", `
bind main = fn(): {
	let _ = comp.TargetOs.linux
}
`, "cannot use `@comptime_only` enum")
	assertComptimeError(t, "top comptime if requires bool", `
comptime if(123): const BAD = 1
`, "must have type `bool`")
}

func TestPhase4ConstGenericParams(t *testing.T) {
	// 1. Struct, method, array size expressions, type identity, inference, and all allowed const types
	src := `
const ROWS = 2 + 1
const COLS = 4

@derive(Printable)
define Matrix[R: const int, C: const int] struct [
	data: [R * C: float],
]

const Matrix[R, C].rows = fn(self) int: R
const Matrix[R, C].cols = fn(self) int: C
const Matrix[R, C].scale_sum[K: const int] = fn(self) int: (R + C) * K
const Matrix[R, C].eq = fn(self, other: Matrix[R, C]) bool: {
	for(i in 0..(R * C)): {
		if(self.data[i] != other.data[i]): func <- false
	}
	true
}

define Mode enum [ fast, safe ]
define Tagged[B: const bool, Ch: const char, M: const Mode, S: const string, U: const u8] struct [
	tag: int = int(U),
]

define Pair[T, N: const int] struct [
	items: [N: T],
]

define RawBytes[T] struct [
	bytes: [type.size_of[T](): u8],
]

const zeros[N: const int] = fn() [N: int]: [N: 0]
const arr_len[T, N: const int] = fn(xs: [N: T]) int: xs.len()
const sum_int_arr[N: const int] = fn(xs: [N: int]) int: {
	let s = 0
	for(x in xs): s += x
	s
}
const tag_label[S: const string] = fn() string: "${S}_ok"
const make_adder[K: const int] = fn() fn(int) int: fn(x: int) int: x + K

const CT_MAT_ROWS = Matrix[ROWS, COLS][data: [12: 1.0]].rows()
const CT_MAT_SCALE = Matrix[2, 3][data: [6: 0.0]].scale_sum[5]()
const CT_ZEROS = zeros[4]().len()
const CT_INFER_FN = sum_int_arr([3: 10, 20, 30])
const CT_INFER_MIXED = arr_len([2: "a", "b"])
const CT_STR_LABEL_A = tag_label["a b"]()
const CT_STR_LABEL_B = tag_label["a_b"]()
const CT_RAW_SZ = type.size_of[RawBytes[Matrix[2, 2]]]()

bind main = fn(): {
	let m1: Matrix[2 + 2, 4] = Matrix[4, 4][data: [16: 2.0]]
	let m2: Matrix[4, 4] = m1
	assert(m1 == m2 and m2.rows() == 4 and m2.cols() == 4 and m2.scale_sum[2]() == 16)
	let p = Pair[items: [3: 1, 2, 3]]
	assert(p.items.len() == 3)
	let t = Tagged[true, 'Z', Mode.safe, "hdr v1", u8(42)][]
	assert(t.tag == 42)
	let add7 = make_adder[7]()
	assert(add7(5) == 12)
	assert(CT_MAT_ROWS == 3 and CT_MAT_SCALE == 25 and CT_ZEROS == 4 and CT_INFER_FN == 60 and CT_INFER_MIXED == 2)
	assert(CT_STR_LABEL_A == "a b_ok" and CT_STR_LABEL_B == "a_b_ok" and CT_RAW_SZ == 32)
}
`
	prog, rep := checkComptimeSrc(t, src)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors in TestPhase4ConstGenericParams:\n%s", rep.Format())
	}
	if sym := prog.EntryMod.Symbols["CT_MAT_ROWS"]; sym == nil || sym.ConstIntVal != 3 {
		t.Fatalf("CT_MAT_ROWS = %+v, want 3", sym)
	}
	if sym := prog.EntryMod.Symbols["CT_MAT_SCALE"]; sym == nil || sym.ConstIntVal != 25 {
		t.Fatalf("CT_MAT_SCALE = %+v, want 25", sym)
	}
	if sym := prog.EntryMod.Symbols["CT_INFER_FN"]; sym == nil || sym.ConstIntVal != 60 {
		t.Fatalf("CT_INFER_FN = %+v, want 60", sym)
	}
	if sym := prog.EntryMod.Symbols["CT_RAW_SZ"]; sym == nil || sym.ConstIntVal != 32 {
		t.Fatalf("CT_RAW_SZ = %+v, want 32", sym)
	}
	// Verify H11.10 distinct deterministic SHA-256 mangling for `"a b"` vs `"a_b"`
	gf := prog.EntryMod.GenericFuncs["tag_label"]
	if gf == nil || len(gf.Instances) != 2 {
		t.Fatalf("expected 2 distinct instances of tag_label, got %+v", gf)
	}
	for k, fi := range gf.Instances {
		for i := 0; i < len(fi.CName); i++ {
			b := fi.CName[i]
			if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_') {
				t.Fatalf("non-identifier char %q in mangled CName %q (key %q)", b, fi.CName, k)
			}
		}
	}

	// 2. Reject invalid const parameter types and mismatched generic arguments
	assertComptimeError(t, "reject float const param", `
define Bad[F: const float] struct [ x: int ]
`, "cannot have type float")
	assertComptimeError(t, "reject optional const param", `
define Bad[O: const ?int] struct [ x: int ]
`, "cannot have type ?int")
	assertComptimeError(t, "reject tagged enum const param", `
define E enum [ a: int, b ]
define Bad[V: const E] struct [ x: int ]
`, "cannot have type E")
	assertComptimeError(t, "reject struct const param", `
define Pt struct [ x: int ]
define Bad[P: const Pt] struct [ x: int ]
`, "cannot have type Pt")
	assertComptimeError(t, "reject type param as const type", `
define Bad[T, V: const T] struct [ x: int ]
`, "cannot use type parameter")
	assertComptimeError(t, "reject value passed to type param", `
define Box[T] struct [ x: T ]
bind main = fn(): { let _: Box[42] = Box[42][x: 1] }
`, "expected a type argument")
	assertComptimeError(t, "reject type passed to const param", `
define Mat[N: const int] struct [ x: [N: int] ]
bind main = fn(): { let _: Mat[int] = Mat[4][x: [4: 0]] }
`, "got type int")
	assertComptimeError(t, "reject u8 const overflow", `
const id_u8[K: const u8] = fn() u8: K
const BAD = id_u8[256]()
`, "out of range for u8")
	assertComptimeError(t, "reject non-positive stack array from const param", `
const zeros[N: const int] = fn() [N: int]: [N: 0]
const BAD = zeros[0]()
`, "positive compile-time integer constant")
	assertComptimeError(t, "enforce monomorph recursion limit on const generics", `
const boom[N: const int] = fn() int: 1 + boom[N + 1]()
bind main = fn(): { let _ = boom[0]() }
`, "recursion depth limit")
}

func TestPhase4LocalComptimeControlFlow(t *testing.T) {
	// 1. Valid local comptime if, comptime match, and comptime for cases
	src := `
@comptime_only
define Cfg enum [
	num: int,
	text: string,
	off,
]

define Shape enum [
	circle: int,
	square: int,
]

const pick_size[T] = fn() int: {
	comptime if(type.size_of[T]() == 8):
		100
	else comptime if(type.size_of[T]() == 1):
		10
	else:
		0
}

const eval_nested_for = fn() int: {
	let acc = 0
	comptime for(i in 1..=3): {
		comptime for(j in 0..i): {
			acc += i * 10 + j
		}
	}
	acc
}

const CT_PAIRS = comptime for(i in 0..4) [int]: {
	comptime if(i == 1): {
		next <-
	} else: {
		i * 10
	}
}

@comptime_only
const CFG_VAL: Cfg = Cfg.num(7)

const MATCHED_NUM = comptime match(CFG_VAL):
	when Cfg.num(v): v * 6
	when Cfg.text(s): s.len()
	when Cfg.off: undefined_should_not_be_checked()
	default: 0

const OS_CODE = comptime match(comp.TARGET.os):
	when comp.TargetOs.darwin: 1
	when comp.TargetOs.linux: 2
	when comp.TargetOs.windows: 3
	when comp.TargetOs.freebsd: 4
	when comp.TargetOs.wasm: 5
	when comp.TargetOs.unknown: 6
	default: 0

const CT_FACT = eval_nested_for()

@comptime_only
const CT_MAP = {
	let mp = Map[string, int].new()
	mp.insert("a", 2)
	mp.insert("b", 3)
	mp
}

const CT_MAP_SUM = {
	let s = 0
	comptime for(k, v in CT_MAP): {
		s += k.len() + v
	}
	s
}

const CT_VOID_ELSE = {
	let x = 0
	comptime for(i in 0..2): {
		x += i + 1
	} else: {
		x += 100
	}
	x
}

const CT_EMPTY_COL = comptime for(i in 0..0) [int]: i else: [5, 9]

bind main = fn(): {
	comptime if(false): {
		totally_nonexistent_fn(1, 2, 3)
	}
	let mut_acc = 0
	comptime if(true): {
		mut_acc += 1
	}
	let a = pick_size[int]()
	let b = pick_size[bool]()
	let c = comptime match(Shape.circle(5)):
		when Shape.circle(r): r * r
		when Shape.square(_): nope_untaken()
		default: 0
	let rt_sum = eval_nested_for()
	comptime for(i, ch in "xy"): {
		mut_acc += i + int(ch)
	}
	assert(a == 100 and b == 10 and c == 25 and MATCHED_NUM == 42 and rt_sum == CT_FACT and mut_acc > 0)
}
`
	prog, rep := checkComptimeTargetSrc(t, "aarch64-apple-darwin", src)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}
	checkSymInt := func(name string, want int64) {
		t.Helper()
		sym := prog.EntryMod.Symbols[name]
		if sym == nil || sym.ConstVal == nil {
			t.Fatalf("missing comptime constant %s", name)
		}
		sd := ExportStaticData(sym.ConstVal, sym.Type)
		if sd == nil || sd.SignedVal != want {
			t.Fatalf("%s = %+v, want %d", name, sd, want)
		}
	}
	checkSymInt("MATCHED_NUM", 42)
	checkSymInt("OS_CODE", 1)
	checkSymInt("CT_FACT", 144)
	checkSymInt("CT_MAP_SUM", 7)
	checkSymInt("CT_VOID_ELSE", 103)

	if sdPairs := ExportStaticData(prog.EntryMod.Symbols["CT_PAIRS"].ConstVal, prog.EntryMod.Symbols["CT_PAIRS"].Type); sdPairs == nil || len(sdPairs.Elems) != 3 {
		t.Fatalf("expected 3 elements in CT_PAIRS, got %+v", sdPairs)
	}
	if sdEmpty := ExportStaticData(prog.EntryMod.Symbols["CT_EMPTY_COL"].ConstVal, prog.EntryMod.Symbols["CT_EMPTY_COL"].Type); sdEmpty == nil || len(sdEmpty.Elems) != 2 || sdEmpty.Elems[0].SignedVal != 5 || sdEmpty.Elems[1].SignedVal != 9 {
		t.Fatalf("expected [5, 9] in CT_EMPTY_COL, got %+v", sdEmpty)
	}

	// 2. H11.1 errors: expression `comptime if` without `else`, optional unwrap, runtime cond in const fn
	assertComptimeError(t, "comptime if expression requires else", `
bind main = fn(): { let _ = comptime if(true): 1 }
`, "must include an `else` branch")
	assertComptimeError(t, "comptime if cannot implicitly return ?T without else", `
bind main = fn(): { let _: ?int = comptime if(true): 1 }
`, "must include an `else` branch")
	assertComptimeError(t, "comptime if explicit ?int still requires else", `
bind main = fn(): { let _ = comptime if(true) ?int: 1 }
`, "must include an `else` branch")
	assertComptimeError(t, "comptime if reject block tail value without else", `
const bad = fn() int: {
	comptime if(true): 42
}
`, "must include an `else` branch")
	assertComptimeError(t, "comptime if reject optional unwrap", `
bind main = fn(): {
	const x: ?int = 5
	comptime if(x?): println("ok")
}
`, "not supported on `comptime if`")
	assertComptimeError(t, "comptime if in const fn rejects runtime param", `
const bad = fn(x: int) int: {
	comptime if(x > 0): 1 else: 2
}
bind main = fn(): { let _ = bad(1) }
`, "cannot reference runtime variable")

	// 3. H11.2 errors: `comptime match` still checks exhaustiveness across untaken arms & rejects runtime scrutinees
	assertComptimeError(t, "comptime match requires exhaustiveness even when taken arm matches", `
define Color enum [ red, green, blue ]
bind main = fn(): {
	let _ = comptime match(Color.red):
		when Color.red: 1
		when Color.green: 2
		default Color.green: 0
}
`, "missing variant(s): Color.blue")
	assertComptimeError(t, "comptime match rejects runtime param in const fn", `
const bad = fn(x: int) int: {
	comptime match(x):
		when 1: 10
		default: 0
}
bind main = fn(): { let _ = bad(1) }
`, "cannot reference runtime variable")
	assertComptimeError(t, "comptime match rejects @comptime_only bound payload crossing to runtime", `
@comptime_only
define Boxed enum [ hold: comp.TargetOs, empty ]
bind main = fn(): {
	let _ = comptime match(Boxed.hold(comp.TARGET.os)):
		when Boxed.hold(os): os
		when Boxed.empty: comp.TargetOs.unknown
		default: comp.TargetOs.unknown
}
`, "cannot cross into runtime")

	// 4. H15 errors: `comptime for` unroll limit, Cond loop, mut var, runtime sequence
	assertComptimeError(t, "comptime for rejects over 1024 iterations", `
bind main = fn(): {
	comptime for(i in 0..1025): assert(i >= 0)
}
`, "exceeds unroll limit")
	assertComptimeError(t, "comptime for rejects conditional while-style loop", `
bind main = fn(): {
	comptime for(true): for <-
}
`, "cannot be statically unrolled")
	assertComptimeError(t, "comptime for rejects mut loop var", `
bind main = fn(): {
	const xs = [1, 2]
	comptime for(&x in xs): {
		let _ = x
	}
}
`, "cannot be mutable")
	assertComptimeError(t, "comptime for rejects runtime sequence in const fn", `
const bad = fn(n: int) void: {
	comptime for(i in 0..n): {
		let _ = i
	}
}
bind main = fn(): { bad(3) }
`, "cannot reference runtime variable")

	// 5. H15 cumulative unroll budget adjusted by @comptime_limit & plain-data unroll lint
	assertComptimeError(t, "comptime for enforces cumulative unrolled statement budget across loops", `
bind main = fn(): {
	let acc = 0
	comptime for(i in 0..40): {
		comptime for(j in 0..40): {
			acc += i + j
		}
	}
}
`, "exceeds")

	_, okLimitRep := checkComptimeTargetSrc(t, "aarch64-apple-darwin", `
@comptime_limit(4000)
bind main = fn(): {
	let acc = 0
	comptime for(i in 0..40): {
		comptime for(j in 0..40): {
			acc += i + j
		}
	}
}
`)
	if okLimitRep.HasErrors() {
		t.Fatalf("@comptime_limit(4000) should allow 1600 unrolled iterations:\n%s", okLimitRep.Format())
	}

	_, lintRep := checkComptimeTargetSrc(t, "aarch64-apple-darwin", `
bind main = fn(): {
	let acc = 0
	comptime for(i in 0..32): {
		acc += i
	}
	assert(acc > 0)
}
`)
	if lintRep.HasErrors() || !strings.Contains(lintRep.Format(), "prefer a regular runtime `for` loop") {
		t.Fatalf("expected plain-data comptime for warning lint, got:\n%s", lintRep.Format())
	}
}

func TestComptimeEmbedStrAndBytes(t *testing.T) {
	outer := t.TempDir()
	projDir := filepath.Join(outer, "proj")
	subDir := filepath.Join(projDir, "sub")
	assetsDir := filepath.Join(projDir, "assets")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	secretFile := filepath.Join(outer, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("TOP_SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	helloTxt := filepath.Join(subDir, "hello.txt")
	helloBytes := []byte("hello, embed!\nline2")
	if err := os.WriteFile(helloTxt, helloBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	bannerTxt := filepath.Join(assetsDir, "banner.txt")
	if err := os.WriteFile(bannerTxt, []byte("PROJECT_BANNER"), 0o644); err != nil {
		t.Fatal(err)
	}
	binFile := filepath.Join(projDir, "blob.bin")
	binData := []byte{0xff, 0xfe, 0x00, '7', 0x80, 0x41}
	if err := os.WriteFile(binFile, binData, 0o644); err != nil {
		t.Fatal(err)
	}
	bigFile := filepath.Join(projDir, "big.bin")
	if err := os.WriteFile(bigFile, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(projDir, "lowlight.toml"), []byte("[package]\nname = \"proj\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "sub.ll"), []byte(`
const SUB_MSG = comp.embed_str("hello.txt")
const PROJ_BANNER = comp.embed_str("../assets/banner.txt")
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(projDir, "main.ll")
	mainSrc := `
import "proj/sub"

const RAW_BYTES = comp.embed_bytes("blob.bin")
const EMPTY_STR = comp.embed_str("empty.txt")

bind main = fn(): {
	assert(sub.SUB_MSG == "hello, embed!\nline2")
	assert(sub.PROJ_BANNER == "PROJECT_BANNER")
	assert(RAW_BYTES.len() == 6 and RAW_BYTES[0] == u8(255) and RAW_BYTES[2] == u8(0))
	assert(EMPTY_STR == "")
}
`
	if err := os.WriteFile(filepath.Join(projDir, "empty.txt"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := diag.NewReporter()
	prog := Check(mainPath, "", projDir, rep)
	if rep.HasErrors() {
		t.Fatalf("valid embed failed:\n%s", rep.Format())
	}
	if len(prog.EmbeddedFiles) != 4 {
		t.Fatalf("expected 4 tracked embedded files, got %d: %+v", len(prog.EmbeddedFiles), prog.EmbeddedFiles)
	}
	realHello, _ := filepath.EvalSymlinks(helloTxt)
	wantSum := sha256.Sum256(helloBytes)
	wantHex := hex.EncodeToString(wantSum[:])
	foundHello := false
	for _, ef := range prog.EmbeddedFiles {
		if ef.Path == filepath.Clean(realHello) {
			foundHello = true
			if ef.SHA256 != wantHex {
				t.Fatalf("embedded file SHA mismatch: got %s, want %s", ef.SHA256, wantHex)
			}
		}
	}
	if !foundHello {
		t.Fatalf("expected %s in EmbeddedFiles, got %+v", realHello, prog.EmbeddedFiles)
	}

	// Symlink escape inside projDir pointing to secretFile outside projDir
	escapeLink := filepath.Join(projDir, "escape.txt")
	_ = os.Symlink(secretFile, escapeLink)

	// Standalone/project negative tests
	badCases := []struct {
		name string
		src  string
		want string
	}{
		{"empty path", `const X = comp.embed_str("")`, "must not be empty"},
		{"nul byte in path", `const X = comp.embed_str("blob\0.bin")`, "NUL byte"},
		{"abs unix path", `const X = comp.embed_str("/etc/passwd")`, "absolute path"},
		{"abs win path", `const X = comp.embed_bytes("C:\\secret.bin")`, "absolute path"},
		{"dotdot escape", `const X = comp.embed_str("../secret.txt")`, "escapes package sandbox"},
		{"backslash traversal escape", `const X = comp.embed_str("..\\secret.txt")`, "escapes package sandbox"},
		{"symlink escape", `const X = comp.embed_str("escape.txt")`, "escapes package sandbox"},
		{"directory target", `const X = comp.embed_str("assets")`, "is a directory"},
		{"invalid utf8", `const X = comp.embed_str("blob.bin")`, "not valid UTF-8"},
		{"bare embed_str", `const X = embed_str("empty.txt")`, "comp.embed_str"},
		{"bare embed_bytes", `const X = embed_bytes("blob.bin")`, "comp.embed_bytes"},
		{"runtime call forbidden", `bind main = fn(): { let _ = comp.embed_str("empty.txt") }`, "`@comptime_only` builtin `comp.embed_str`"},
		{"memory budget exceeded", `
@comptime_limit(memory: 1024)
const BIG = comp.embed_bytes("big.bin")
`, "exceeds remaining compile-time memory budget"},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			r := diag.NewReporter()
			_ = Check(mainPath, tc.src, projDir, r)
			if !r.HasErrors() || !strings.Contains(r.Format(), tc.want) {
				t.Fatalf("expected error containing %q, got:\n%s", tc.want, r.Format())
			}
		})
	}

	// Dependency confinement test: third-party dep CANNOT embed outside its own pkg.Dir.
	depRoot := filepath.Join(outer, "vendor_dep")
	depProj := filepath.Join(outer, "app")
	if err := os.MkdirAll(depRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(depProj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depProj, "project_secret.txt"), []byte("APP_SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depProj, "lowlight.toml"), []byte("[package]\nname = \"app\"\nversion = \"0.1.0\"\n[dependencies]\nevil = { path = \"../vendor_dep\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depRoot, "evil.ll"), []byte(`
const LEAK = comp.embed_str("../app/project_secret.txt")
`), 0o644); err != nil {
		t.Fatal(err)
	}
	appMain := filepath.Join(depProj, "main.ll")
	if err := os.WriteFile(appMain, []byte(`
import "evil"
bind main = fn(): { let _ = evil.LEAK }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	depRep := diag.NewReporter()
	_ = Check(appMain, "", depProj, depRep)
	if !depRep.HasErrors() || !strings.Contains(depRep.Format(), "escapes package sandbox") {
		t.Fatalf("expected dependency embed escape error, got:\n%s", depRep.Format())
	}
}

func TestComptimeFreezeCoWBorrowsAndSliceParity(t *testing.T) {
	src := `
const TOP_LIST: [int] = [1, 2, 3, 4]
const TOP_BYTES: [u8] = [u8(10), u8(20), u8(30)]
const TOP_STR: string = "hello" + "_world"

const read_str_len = fn(s: string) int: s.len()
const read_list_len = fn(xs: [int]) int: xs.len()

const check_all = fn(flag: bool) int: {
	assert(TOP_LIST.cap() == 0)
	assert(TOP_BYTES.cap() == 0)

	let local_sum = {
		const lc = [10, 20, 30]
		const ls = "abc" + "def"
		assert(lc.cap() == 0)
		lc[0] + lc[2] + ls.len()
	}
	assert(local_sum == 46)

	// Conditional/block results passed to ModeRead or ExprStmt must not corrupt or double-free.
	let sa = "hel" + "lo"
	let sb = "wor" + "ld!"
	let slen = read_str_len(if(flag): sa.copy() else: sb.copy())
	assert(slen == 5)
	assert(sa == "hello" and sb == "world!")
	if(flag): sa else: sb

	let la = [1, 2, 3]
	let lb = [4, 5, 6, 7]
	let llen = read_list_len(if(flag): la.copy() else: lb.copy())
	assert(llen == 3)
	assert(la.len() == 3 and la[0] == 1 and la[2] == 3)
	assert(lb.len() == 4 and lb[0] == 4 and lb[3] == 7)

	// Slice view vs owned slice cap() parity.
	assert(la[1..3].cap() == 0)
	let owned_slice = la[1..3]
	assert(owned_slice.cap() == 2 and owned_slice.len() == 2)
	assert(TOP_LIST[1..3].cap() == 0)
	let sub_l = la[0..2]
	assert(sub_l.len() == 2 and sub_l[0] == 1 and sub_l[1] == 2)
	assert(sa[1..4] == "ell")

	// Empty / expression-statement temporary cleanup doesn't affect live locals.
	{ la.copy() }
	local_sum
}

const RES = check_all(true)

bind main = fn(): {
	let x = RES
	let y = TOP_LIST[1]
	let z = int(TOP_BYTES[2])
	let s = TOP_STR.copy()
	assert(x == 46 and y == 2 and z == 30 and s == "hello_world")
}
`
	prog, rep := checkComptimeSrc(t, src)
	if rep.HasErrors() {
		t.Fatalf("unexpected comptime errors:\n%s", rep.Format())
	}

	// Verify local `const` ConstVarVals were frozen and NOT freed when leaving their block.
	fnInst := prog.EntryMod.Symbols["check_all"].FuncInst
	if fnInst == nil || fnInst.BodyMaps == nil {
		t.Fatalf("missing FuncInst or BodyMaps for check_all")
	}
	foundLC := false
	foundLS := false
	for sym, cv := range fnInst.BodyMaps.ConstVarVals {
		if sym.Name == "lc" {
			foundLC = true
			if capV := cv.st.fields["cap"]; capV == nil || capV.asInt64() != 0 {
				t.Fatalf("expected frozen local const lc cap==0, got %+v", capV)
			}
			elems, ok := listStructElems(cv)
			if !ok || len(elems) != 3 || elems[0].asInt64() != 10 || elems[2].asInt64() != 30 {
				t.Fatalf("local const lc corrupted/freed after block exit: ok=%v len=%d", ok, len(elems))
			}
		}
		if sym.Name == "ls" {
			foundLS = true
			if cv.sCap != 0 || cv.s != "abcdef" || (cv.sAlloc != nil && cv.sAlloc.freed) {
				t.Fatalf("local const ls corrupted/freed: sCap=%d s=%q alloc=%+v", cv.sCap, cv.s, cv.sAlloc)
			}
		}
	}
	if !foundLC || !foundLS {
		t.Fatalf("expected both lc and ls in ConstVarVals, foundLC=%v foundLS=%v", foundLC, foundLS)
	}

	// Verify evalIndexSet CoW copies frozen backing storage instead of mutating const TOP_LIST / TOP_BYTES.
	a := &Analyzer{Prog: prog, coreMod: prog.CoreMod, corePkg: prog.CorePkg}
	st := newCtState(a, prog.EntryMod, 1000)

	topListRaw := prog.EntryMod.Symbols["TOP_LIST"].ConstVal.(*ctVal)
	mutList := topListRaw.clone()
	if sig := st.evalIndexSet(mutList, newCtSignedInt(1, types.TypeInt), newCtSignedInt(999, types.TypeInt), diag.Span{}); sig.kind != sigNone {
		t.Fatalf("evalIndexSet CoW on frozen List[int] failed: %+v", sig)
	}
	origElems, _ := listStructElems(topListRaw)
	mutElems, _ := listStructElems(mutList)
	if origElems[1].asInt64() != 2 || mutElems[1].asInt64() != 999 {
		t.Fatalf("CoW failed for List[int]: orig[1]=%d mut[1]=%d", origElems[1].asInt64(), mutElems[1].asInt64())
	}

	topBytesRaw := prog.EntryMod.Symbols["TOP_BYTES"].ConstVal.(*ctVal)
	mutBytes := topBytesRaw.clone()
	if sig := st.evalIndexSet(mutBytes, newCtSignedInt(1, types.TypeInt), newCtInt(77, types.TypeU8), diag.Span{}); sig.kind != sigNone {
		t.Fatalf("evalIndexSet CoW on frozen List[u8] failed: %+v", sig)
	}
	origBs, _, _ := listStructBytes(topBytesRaw)
	mutBs, _, _ := listStructBytes(mutBytes)
	if origBs[1] != 20 || mutBs[1] != 77 {
		t.Fatalf("CoW failed for List[u8]: orig[1]=%d mut[1]=%d", origBs[1], mutBs[1])
	}
}

