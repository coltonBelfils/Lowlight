package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
	"lowlight/internal/format"
)

func runBareEnumProgram(t *testing.T, name, entryPath, srcOverride string, backend driver.BackendKind) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rc, err := driver.Run(driver.Options{
		EntryPath:   entryPath,
		SrcOverride: srcOverride,
		ProjectRoot: projectRoot(t),
		Backend:     backend,
		ExtraCFlags: strictCFlags,
		Env:         []string{"LOWLIGHT_CHECK_LEAKS=1", "ASAN_OPTIONS=detect_leaks=0"},
	}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("[%s %s] build failed: %v\nstderr:\n%s", name, backend, err, stderr.String())
	}
	return rc, stdout.String(), stderr.String()
}

func TestBareEnumMatchE2E(t *testing.T) {
	t.Parallel()

	src := `
import "std/core"

define Status enum [
	Pending,
	Active,
	Suspended,
	Closed,
]

@derive(Equatable)
define Color enum [
	Red,
	Green,
	Blue,
]

define Expr enum [
	Empty,
	Null,
	Num: int,
	Text: string,
	Opt: ?int,
	Res: Result[int],
	Pair: [int],
]

define Maybe[T] enum [
	Nothing,
	Just: T,
]

define Lookup result [
	value: string,
	Missing: string,
	Forbidden,
]

bind Expr.is_empty_or_null = fn(self) bool:
	match (self):
		when Empty, Null: true
		default: false

bind expr_eq = fn(a: Expr, b: Expr) bool:
	match (a, b):
		when (Empty, Empty), (Null, Null): true
		when Num(x), Num(y): x == y
		when Text(s1), Text(s2): s1 == s2
		default: false

bind make_res = fn(ok: bool) Result[string]:
	if (ok): Result.value("hello")
	else: Result.err(Error.new("boom"))

bind parse_lookup = fn(ok: bool) Lookup:
	if (ok): Lookup.value("found")
	else: Lookup.Missing("nope")

bind describe_status = fn(s: Status) string:
	match (s):
		when Pending, Suspended: "idle"
		when Status.Active: "running"
		default Closed: "closed"

bind describe_local = fn(x: int) string: {
	define LocalKind enum [
		Small,
		Big: int,
		empty,
	]
	let empty = true
	let k = if (x == 0): LocalKind.empty
		else if (x < 10): LocalKind.Small
		else: LocalKind.Big(x)
	match (k):
		when empty: if (empty): "local_empty" else: "never"
		when Small: "local_small"
		default Big(n): "local_big:$n"
}

bind main = fn(): {
	// 1. Pure tag enums (single-arg, multi-pattern OR, mixed bare + qualified, exhaustive default)
	println(describe_status(Status.Pending))
	println(describe_status(Status.Active))
	println(describe_status(Status.Suspended))
	println(describe_status(Status.Closed))

	// In-scope variable pattern alongside bare enum matching
	let target = Color.Green
	let c = Color.Green
	let color_tag = match (c):
		when target: "matched_var"
		default: "other"
	println(color_tag)

	// Multi-arg pure tag enum with bare patterns
	let pair_tag = match (Color.Blue, Status.Closed):
		when (Red, Pending), (Green, Active): "rg"
		when Blue, Closed: "bc"
		default: "rest"
	println(pair_tag)

	// 2. Tagged union: unit, read, &mut, own, _, ?ident, !ident, Resource move without leaks
	let e1 = Expr.Text("hi")
	let r_text = match (e1):
		when Empty, Null: "empty"
		when Text(s): "text:" + s
		default: "other"
	println(r_text)

	let mut_expr = Expr.Pair([1, 2])
	match (mut_expr):
		when Pair(&xs): xs.push(3)
		default: {}
	let pair_len = match (mut_expr):
		when Pair(xs): xs.len()
		default: -1
	println("len=$pair_len")

	let own_expr = Expr.Text("owned")
	let moved = match (own own_expr):
		when Text(own s): s + "!"
		default: "none"
	println(moved)

	let opt_expr = Expr.Opt(42)
	let opt_out = match (opt_expr):
		when Opt(n?): n? ?? 0
		default: -1
	println("opt=$opt_out")

	let res_expr = Expr.Res(Result.value(99))
	let res_out = match (res_expr):
		when Res(own r!): r! ?? -1
		default: -2
	println("res=$res_out")

	let wild_expr = Expr.Num(7)
	let wild_out = match (wild_expr):
		when Num(_): "num"
		default: "other"
	println(wild_out)

	// 3. Multi-arg tagged union
	println("eq1=${expr_eq(Expr.Empty, Expr.Empty)}")
	println("eq2=${expr_eq(Expr.Num(5), Expr.Num(5))}")
	println("eq3=${expr_eq(Expr.Num(5), Expr.Empty)}")

	// 4. Result[T] and user-defined result matched with bare value(...) and err(...)
	let items: [string] = []
	match (make_res(true)):
		when value(own o): items.push(own o)
		default err(own e): items.push(e.message())
	match (make_res(false)):
		when value(own o): items.push(own o)
		default err(own e): items.push(e.message())
	println("items=${items[0]},${items[1]}")

	let lk = match (parse_lookup(false)):
		when value(s): s.copy()
		when Missing(m): "missing:" + m
		default Forbidden: "forbidden"
	println(lk)

	// 5. Generic enum
	let m1 = Maybe[string].Just("gen")
	let m2 = Maybe[string].Nothing
	let gm = match (m1, m2):
		when Just(s), Nothing: s.copy()
		default: "other"
	println(gm)

	// 6. Local enum inside block + unrelated local bool sharing variant name
	println(describe_local(0))
	println(describe_local(3))
	println(describe_local(25))

	// 7. Arena ref[Enum]
	let arena = Arena.new()
	let eref = arena.alloc(Expr.Num(123))
	let arena_val = match (eref):
		when Num(n): n
		default: 0
	println("arena=$arena_val")

	// 8. Coexistence of bare variant pattern and MethodPattern
	let em = Expr.Null
	let co = match (em):
		when Num(n): "num:$n"
		when is_empty_or_null(): "method_empty"
		default: "other"
	println(co)
}
`
	want := strings.Join([]string{
		"idle",
		"running",
		"idle",
		"closed",
		"matched_var",
		"bc",
		"text:hi",
		"len=3",
		"owned!",
		"opt=42",
		"res=99",
		"num",
		"eq1=true",
		"eq2=true",
		"eq3=false",
		"items=hello,boom",
		"missing:nope",
		"gen",
		"local_empty",
		"local_small",
		"local_big:25",
		"arena=123",
		"method_empty",
	}, "\n")

	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		entry := filepath.Join(t.TempDir(), "bare_enum.ll")
		rc, stdout, stderr := runBareEnumProgram(t, "bare_enum_e2e", entry, src, backend)
		if rc != 0 {
			t.Fatalf("[%s] exit code %d\nstdout:\n%s\nstderr:\n%s", backend, rc, stdout, stderr)
		}
		if got := strings.TrimSpace(stdout); got != want {
			t.Fatalf("[%s] unexpected output:\n%s\nwant:\n%s", backend, got, want)
		}
	}
}

func TestBareEnumMatchCrossPackageE2E(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	pkgSrc := `
define Token enum [
	Ident: string,
	Eof,
	_Hidden: int,
	_Internal,
]

bind Token.make_internal = fn() Token: Token._Internal
`
	writeTestFile(t, tmp, "pkg_tok/tok.ll", pkgSrc)

	mainSrc := `
import "./pkg_tok" as tok

bind describe = fn(t: tok.Token) string:
	match (t):
		when Ident(s): "ident:" + s
		when Eof: "eof"
		default: "internal"

bind main = fn(): {
	println(describe(tok.Token.Ident("abc")))
	println(describe(tok.Token.Eof))
	println(describe(tok.Token.make_internal()))
}
`
	entry := writeTestFile(t, tmp, "main.ll", mainSrc)
	want := "ident:abc\neof\ninternal"
	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		rc, stdout, stderr := runBareEnumProgram(t, "bare_enum_pkg", entry, "", backend)
		if rc != 0 {
			t.Fatalf("[%s] exit code %d\nstdout:\n%s\nstderr:\n%s", backend, rc, stdout, stderr)
		}
		if got := strings.TrimSpace(stdout); got != want {
			t.Fatalf("[%s] unexpected output:\n%s\nwant:\n%s", backend, got, want)
		}
	}
}

func TestBareEnumMatchNegativeDiagnostics(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)

	tmpPkg := t.TempDir()
	writeTestFile(t, tmpPkg, "pkg_secret/secret.ll", `
define Secret enum [
	Public: int,
	Open,
	_Hidden: int,
	_Closed,
]
`)

	cases := []struct {
		name string
		dir  string
		src  string
		want string
	}{
		{
			name: "dup_bare_bare",
			src: `
define Status enum [A, B]
bind main = fn(): {
	let s = Status.A
	let x = match (s):
		when A: 1
		when A: 2
		default: 3
}
`,
			want: "duplicate enum variant `Status.A`",
		},
		{
			name: "dup_bare_qualified",
			src: `
define Status enum [A, B]
bind main = fn(): {
	let s = Status.A
	let x = match (s):
		when Status.A: 1
		when A: 2
		default: 3
}
`,
			want: "duplicate enum variant `Status.A`",
		},
		{
			name: "dup_payload_bare_qualified",
			src: `
define Tok enum [Num: int, Empty]
bind main = fn(): {
	let t = Tok.Num(1)
	let x = match (t):
		when Num(n): n
		when Tok.Num(m): m
		default: 0
}
`,
			want: "duplicate enum variant `Tok.Num`",
		},
		{
			name: "non_exhaustive_single",
			src: `
define Status enum [A, B, C]
bind main = fn(): {
	let s = Status.A
	let x = match (s):
		when A: 1
		default B: 2
}
`,
			want: "missing variant(s): Status.C",
		},
		{
			name: "non_exhaustive_multi",
			src: `
define S enum [A, B]
bind main = fn(): {
	let x = match (S.A, S.B):
		when A, A: 1
		when A, B: 2
		default B, A: 3
}
`,
			want: "missing combination(s): S.B, S.B",
		},
		{
			name: "payload_without_parens",
			src: `
define Tok enum [Str: string, Empty]
bind main = fn(): {
	let t = Tok.Empty
	let x = match (t):
		when Str: 1
		default: 0
}
`,
			want: "carries a payload of type string; match with `Str(val)` or `Str(_)`",
		},
		{
			name: "unit_with_parens",
			src: `
define Tok enum [Str: string, Empty]
bind main = fn(): {
	let t = Tok.Empty
	let x = match (t):
		when Empty(): 1
		default: 0
}
`,
			want: "has no payload; match with `when Empty:`",
		},
		{
			name: "wrong_binding_count_zero",
			src: `
define Tok enum [Str: string, Empty]
bind main = fn(): {
	let t = Tok.Empty
	let x = match (t):
		when Str(): 1
		default: 0
}
`,
			want: "expects 1 payload binding, got 0",
		},
		{
			name: "wrong_binding_count_two",
			src: `
define Tok enum [Str: string, Empty]
bind main = fn(): {
	let t = Tok.Empty
	let x = match (t):
		when Str(a, b): 1
		default: 0
}
`,
			want: "expects 1 payload binding, got 2",
		},
		{
			name: "invalid_binding_literal",
			src: `
define Tok enum [Num: int, Empty]
bind main = fn(): {
	let t = Tok.Empty
	let x = match (t):
		when Num(42): 1
		default: 0
}
`,
			want: "invalid payload binding in variant pattern `Num(...)`",
		},
		{
			name: "invalid_binding_own_wildcard",
			src: `
define Tok enum [Num: int, Empty]
bind main = fn(): {
	let t = Tok.Empty
	let x = match (t):
		when Num(own _): 1
		default: 0
}
`,
			want: "invalid payload binding in variant pattern `Num(...)`",
		},
		{
			name: "invalid_binding_self",
			src: `
define Tok enum [Num: int, Empty]
bind Tok.f = fn(self) int:
	match (self):
		when Num(self): 1
		default: 0
bind main = fn(): {}
`,
			want: "invalid payload binding in variant pattern `Num(...)`",
		},
		{
			name: "unexported_unit_outside_pkg",
			dir:  tmpPkg,
			src: `
import "./pkg_secret" as sec
bind main = fn(): {
	let s = sec.Secret.Open
	let x = match (s):
		when _Closed: 1
		default: 0
}
`,
			want: "cannot match unexported enum variant \"_Closed\"",
		},
		{
			name: "unexported_payload_outside_pkg",
			dir:  tmpPkg,
			src: `
import "./pkg_secret" as sec
bind main = fn(): {
	let s = sec.Secret.Public(1)
	let x = match (s):
		when _Hidden(n): n
		default: 0
}
`,
			want: "cannot match unexported enum variant \"_Hidden\"",
		},
		{
			name: "ambiguous_unit_var",
			src: `
define Status enum [Active, Closed]
bind main = fn(): {
	let Active = Status.Closed
	let s = Status.Active
	let x = match (s):
		when Active: 1
		default: 0
}
`,
			want: "ambiguous: it names both a variant of Status and a variable in scope",
		},
		{
			name: "ambiguous_payload_method",
			src: `
define Tok enum [Num: int, Empty]
bind Tok.Num = fn(self, x: int) bool: true
bind main = fn(): {
	let t = Tok.Empty
	let x = match (t):
		when Num(n): 1
		default: 0
}
`,
			want: "ambiguous: it names both a variant and a method of Tok",
		},
		{
			name: "typo_unknown_variant",
			src: `
define Status enum [Active, Paused, Closed]
bind main = fn(): {
	let s = Status.Active
	let x = match (s):
		when Actve: 1
		default: 0
}
`,
			want: "enum Status has no variant \"Actve\"",
		},
		{
			name: "multi_pattern_or_with_binding",
			src: `
define Tok enum [Num: int, Alt: int, Empty]
bind main = fn(): {
	let t = Tok.Num(1)
	let x = match (t):
		when Num(n), Alt(n): n
		default: 0
}
`,
			want: "cannot bind variables in a multi-pattern arm",
		},
		{
			name: "own_from_arena_ref",
			src: `
define Box enum [Text: string, Empty]
bind main = fn(): {
	let arena = Arena.new()
	let bref = arena.alloc(Box.Text("hi"))
	let s = match (bref):
		when Text(own v): v
		default: ""
}
`,
			want: "cannot move `own` resource payload out of arena reference",
		},
		{
			name: "mut_from_immutable_bind",
			src: `
define Box enum [Text: string, Empty]
bind f = fn(b: Box) string:
	match (b):
		when Text(&v): v.copy()
		default: ""
bind main = fn(): {}
`,
			want: "mutably destructure",
		},
		{
			name: "conflicting_mut_and_own_multi_arg",
			src: `
define Box enum [Text: string, Empty]
bind main = fn(): {
	let b = Box.Text("hi")
	let out = match (b, b):
		when Text(&x), Text(own y): y
		default: ""
}
`,
			want: "conflicting borrows/moves of variable \"b\"",
		},
		{
			name: "missing_opt_suffix_on_binding",
			src: `
define Box enum [Opt: ?int, Empty]
bind main = fn(): {
	let b = Box.Opt(1)
	let out = match (b):
		when Opt(v): v ?? 0
		default: 0
}
`,
			want: "must end with `?`",
		},
		{
			name: "missing_bang_suffix_on_binding",
			src: `
define Box enum [Res: Result[int], Empty]
bind main = fn(): {
	let b = Box.Res(Result.value(1))
	let out = match (b):
		when Res(r): r ?? 0
		default: 0
}
`,
			want: "must end with `!`",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := tc.dir
			if dir == "" {
				dir = t.TempDir()
			}
			_, _, err := driver.Check(driver.Options{
				EntryPath:   filepath.Join(dir, tc.name+".ll"),
				SrcOverride: tc.src,
				ProjectRoot: root,
			})
			if err == nil {
				t.Fatalf("expected compile error containing %q, got success", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got:\n%v", tc.want, err)
			}
		})
	}
}

func TestFormatBareEnumMatch(t *testing.T) {
	t.Parallel()
	src := `bind parse = fn(r!: Result[string], a: Json, b: Json) bool: {
	match(r!):
		when value(own o): println(o)
		default err(own e): println(e.message())
	match(a, b):
		when (Empty, Empty), (Null, Null): true
		when String(&s), String(os): s == os
		default: false
}
`
	first, err := format.Format("test.ll", src, format.DefaultOptions())
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	second, err := format.Format("test.ll", first, format.DefaultOptions())
	if err != nil {
		t.Fatalf("second format failed: %v", err)
	}
	if first != second {
		t.Fatalf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	for _, want := range []string{
		"when value(own o): println(o)",
		"default err(own e): println(e.message())",
		"when (Empty, Empty), (Null, Null): true",
		"when String(&s), String(os): s == os",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, first)
		}
	}
}

func TestBareEnumMatchBlockAndLoopBodies(t *testing.T) {
	t.Parallel()
	src := `
define Kind enum [
	neg,
	bin,
	loop_for,
	loop_while,
]

bind run_kind = fn(k: Kind) int: {
	match(k):
		when neg: {
			bind x = 10
			-x
		}
		when loop_while: {
			let acc = 0
			let n = 0
			while(n < 3): {
				acc += n
				n += 1
			}
			acc
		}
		when loop_for: (for(i in 0..2) [int]: i).len()
		default bin: {
			bind y = 7
			y * 2
		}
}

bind run_loop_headers = fn(k: Kind):
	match(k):
		when neg: for(i in 0..2): {
			println(i)
		}
		when loop_for: while(false): {
			println("never")
		}
		default: {}

bind check_zero_arg = fn(flag: bool) int:
	match():
		when flag: {
			bind a = 42
			a
		}
		default: {
			0
		}

bind check_in_block = fn(n: int, xs: [int]) int:
	match(n):
		when in xs: {
			bind ok = 99
			ok
		}
		default: {
			-1
		}

bind main = fn(): {
	println("neg=${run_kind(Kind.neg)}")
	println("bin=${run_kind(Kind.bin)}")
	println("while=${run_kind(Kind.loop_while)}")
	run_loop_headers(Kind.loop_while)
	println("zero=${check_zero_arg(true)}")
	println("in=${check_in_block(2, [1, 2, 3])}")
}
`
	want := "neg=-10\nbin=14\nwhile=3\nzero=42\nin=99"
	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		entry := filepath.Join(t.TempDir(), "bare_block_body.ll")
		rc, stdout, stderr := runBareEnumProgram(t, "bare_block_body", entry, src, backend)
		if rc != 0 {
			t.Fatalf("[%s] exit code %d\nstdout:\n%s\nstderr:\n%s", backend, rc, stdout, stderr)
		}
		if got := strings.TrimSpace(stdout); got != want {
			t.Fatalf("[%s] unexpected output:\n%s\nwant:\n%s", backend, got, want)
		}
	}
}
