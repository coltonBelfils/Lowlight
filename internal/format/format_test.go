package format_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/diag"
	"lowlight/internal/driver"
	"lowlight/internal/format"
	"lowlight/internal/parser"
	"lowlight/internal/sema"
)

func mustFormat(t *testing.T, src string, opts format.Options) string {
	t.Helper()
	out, err := format.Format("test.ll", src, opts)
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	// Assert idempotency!
	out2, err := format.Format("test.ll", out, opts)
	if err != nil {
		t.Fatalf("second format error: %v\nsource was:\n%s", err, out)
	}
	if out != out2 {
		t.Fatalf("formatter not idempotent!\n--- pass 1 ---\n%s--- pass 2 ---\n%s", out, out2)
	}
	return out
}

func TestSingleExpressionUnwrappingAndNextLineMultiline(t *testing.T) {
	src := `bind add = fn(a: int, b: int) int: {
    a + b
}

bind classify = fn(x: int) string: {
    match (x):
        when 0: {
            "zero"
        }
        default: {
            "nonzero"
        }
}

bind clamp = fn(x: int) int: {
    if (x < 0): {
        0
    } else if (x > 10): {
        10
    } else: {
        x
    }
}

bind ptr_read = fn(p: ptr[int]) int: {
    unsafe {
        *p
    }
}

bind sum_loop = fn(n: int) int: {
    let total = 0
    for (i in 0 .. n): {
        total += i
    }
    total
}

bind keep_void_semi = fn(x: int): {
    add(x, 1);
}

bind keep_labeled = fn() int: {
    outer: {
        outer <- 5
    }
}
`
	want := `bind add = fn(a: int, b: int) int: a + b

bind classify = fn(x: int) string:
	match(x):
		when 0: "zero"
		default: "nonzero"

bind clamp = fn(x: int) int:
	if(x < 0): 0
	else if(x > 10): 10
	else: x

bind ptr_read = fn(p: ptr[int]) int: unsafe *p

bind sum_loop = fn(n: int) int: {
	let total = 0
	for(i in 0..n): total += i
	total
}

bind keep_void_semi = fn(x: int): {
	add(x, 1);
}

bind keep_labeled = fn() int: outer: {
	outer <- 5
}
`
	got := mustFormat(t, src, format.DefaultOptions())
	if got != want {
		t.Fatalf("mismatch!\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestControlFlowAndOperatorFormatting(t *testing.T) {
	src := `bind check = fn(a: int, b: int, c: int) bool: {
    let x = (a + b) * c
    let y = a - (b - c)
    let ok = (a < b) and (b < c) or (c == 0)
    while (x > 0): {
        x -= 1
    }
    if (ok): {
        func <- true
    } else: {
        x == y
    }
}
`
	got := mustFormat(t, src, format.DefaultOptions())
	if !strings.Contains(got, "while(x > 0): x -= 1") {
		t.Fatalf("expected unwrapped while without space before (, got:\n%s", got)
	}
	if !strings.Contains(got, "if(ok): func <- true") {
		t.Fatalf("expected unwrapped if unwind without space before (, got:\n%s", got)
	}
	if !strings.Contains(got, "let x = (a + b) * c") || !strings.Contains(got, "let y = a - (b - c)") {
		t.Fatalf("expected preserved necessary parentheses, got:\n%s", got)
	}
	if !strings.Contains(got, "((a < b) and (b < c))") && !strings.Contains(got, "(a < b and b < c)") && !strings.Contains(got, "(a < b) and (b < c)") {
		t.Logf("formatted bool expr:\n%s", got)
	}
}

func TestImportSortingAndHeaderPinning(t *testing.T) {
	src := `// Module doc summary.
// Multi-line module doc.

import "./zeta.ll"
import "std/strings.ll"
import "./alpha.ll" as a
import "std/fs.ll"

bind x = 1
`
	want := `// Module doc summary.
// Multi-line module doc.

import "std/fs.ll"
import "std/strings.ll"

import "./alpha.ll" as a
import "./zeta.ll"

bind x = 1
`
	got := mustFormat(t, src, format.DefaultOptions())
	if got != want {
		t.Fatalf("import sort mismatch!\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestConfigurableIndentAndLineWidthAndConfigFile(t *testing.T) {
	src := `bind f = fn(alpha: int, beta: int, gamma: int, delta: int) int: {
    let xs = [alpha, beta, gamma, delta]
    xs[0] + alpha + beta
}
`
	optsSpaces := format.Options{IndentStyle: "spaces", TabWidth: 2, LineWidth: 40}
	got := mustFormat(t, src, optsSpaces)
	if strings.Contains(got, "\t") {
		t.Fatalf("expected spaces indent, got tabs:\n%s", got)
	}
	if !strings.Contains(got, "  let xs = ") {
		t.Fatalf("expected 2-space indent, got:\n%s", got)
	}
	// Narrow width should wrap parameter list with trailing comma.
	if !strings.Contains(got, "alpha: int,\n") {
		t.Fatalf("expected narrow line width parameter wrapping, got:\n%s", got)
	}

	// Verify `.lowlightfmt` loading
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, ".lowlightfmt")
	if err := os.WriteFile(cfgPath, []byte("indent = spaces\ntab_width = 4\nline_width = 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(tmp, "pkg")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	loaded := format.LoadConfig(sub)
	if loaded.IndentStyle != "spaces" || loaded.TabWidth != 4 || loaded.LineWidth != 0 {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
}

func TestRawLiteralsAndCommentPreservation(t *testing.T) {
	src := `// File header.

bind hex = 0xFF_00
bind bin = 0b1010
bind greet = fn(name: string) string: "hi, $name ${name.len()}!"
bind raw = ` + "`" + `hello\n$world` + "`" + `

/*
 * Block doc comment.
 */
define Point struct [
    // X axis.
    x: int,
    y: int, // Y axis.
]

bind keep_comment_block = fn(x: int) int: {
    // Keep braces around internal comment.
    x + 1
}
`
	got := mustFormat(t, src, format.DefaultOptions())
	for _, sub := range []string{
		"0xFF_00",
		"0b1010",
		`"hi, $name ${name.len()}!"`,
		"`hello\\n$world`",
		"// X axis.",
		"y: int, // Y axis.",
		"// Keep braces around internal comment.",
	} {
		if !strings.Contains(got, sub) {
			t.Fatalf("expected %q in formatted output:\n%s", sub, got)
		}
	}
}

func TestFormatStandardLibraryFilesParseAndSemaAndIdempotent(t *testing.T) {
	root := filepath.Join("..", "..")
	stdDir := filepath.Join(root, "std")
	var files []string
	if err := filepath.Walk(stdDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".ll") && filepath.Base(path) != "json.ll" {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no std .ll files found")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out1, err := format.Format(f, string(raw), format.DefaultOptions())
		if err != nil {
			t.Fatalf("Format(%s) failed: %v", f, err)
		}
		out2, err := format.Format(f, out1, format.DefaultOptions())
		if err != nil {
			t.Fatalf("Format pass 2(%s) failed: %v\n%s", f, err, out1)
		}
		if out1 != out2 {
			t.Fatalf("Format(%s) not idempotent!\n--- out1 ---\n%s\n--- out2 ---\n%s", f, out1, out2)
		}
		rep := diag.NewReporter()
		_ = parser.ParseFile(f, out1, rep)
		if rep.HasErrors() {
			t.Fatalf("formatted %s failed to re-parse:\n%s\nformatted:\n%s", f, rep.Format(), out1)
		}
		repSema := diag.NewReporter()
		_ = sema.CheckForIDE(f, map[string]string{f: out1}, root, repSema)
		if repSema.HasErrors() {
			t.Fatalf("formatted %s failed sema:\n%s\nformatted:\n%s", f, repSema.Format(), out1)
		}
	}
}

func TestFormatCompileAndRunEquivalence(t *testing.T) {
	src := `
define Point struct [ x: int, y: int ]

bind Point.sum = fn(self) int: { self.x + self.y }

bind main = fn() int: {
    let p = Point[ x: (1 + 2) * 3, y: 10 - (4 - 1) ]
    let acc = 0
    for (i in 0 .. 4): { acc += i }
    let label = match (acc):
        when 6: { "six" }
        default: { "other" }
    let r = if (p.sum() > 10): { p.sum() } else: { 0 }
    println("${label}:${r}")
    0
}
`
	tmp := t.TempDir()
	entry := filepath.Join(tmp, "main.ll")
	if err := os.WriteFile(entry, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..")

	var origOut, origErr bytes.Buffer
	code1, err1 := driver.Run(driver.Options{
		EntryPath:   entry,
		SrcOverride: src,
		ProjectRoot: root,
	}, nil, &origOut, &origErr)
	if err1 != nil || code1 != 0 {
		t.Fatalf("original run failed (%d, %v): %s", code1, err1, origErr.String())
	}

	formatted, err := format.Format(entry, src, format.DefaultOptions())
	if err != nil {
		t.Fatalf("Format failed: %v", err)
	}

	var fmtOut, fmtErr bytes.Buffer
	code2, err2 := driver.Run(driver.Options{
		EntryPath:   entry,
		SrcOverride: formatted,
		ProjectRoot: root,
	}, nil, &fmtOut, &fmtErr)
	if err2 != nil || code2 != 0 {
		t.Fatalf("formatted run failed (%d, %v): %s\nformatted:\n%s", code2, err2, fmtErr.String(), formatted)
	}

	if origOut.String() != fmtOut.String() {
		t.Fatalf("execution output mismatch:\norig: %q\nfmt:  %q", origOut.String(), fmtOut.String())
	}
	if origOut.String() != "six:16\n" {
		t.Fatalf("unexpected program output: %q", origOut.String())
	}
}

func TestCoalesceAssociativityFormatting(t *testing.T) {
	src := `bind check = fn(a?: ?int, b?: ?int) int: {
    let r1 = a? ?? b? ?? 0
    let r2 = a? ?? (b? ?? 0)
    let l1 = (a? ?? b?) ?? 0
    let u1 = a? ?? b? ?? func <- -1
    let u2 = (a? ?? b?) ?? func <- -2
    let e1 = id(a? ?? b? ?? 0)
    r1 + r2 + l1 + u1 + u2 + e1
}
`
	want := `bind check = fn(a?: ?int, b?: ?int) int: {
	let r1 = a? ?? b? ?? 0
	let r2 = a? ?? b? ?? 0
	let l1 = (a? ?? b?) ?? 0
	let u1 = a? ?? b? ?? func <- -1
	let u2 = (a? ?? b?) ?? func <- -2
	let e1 = id(a? ?? b? ?? 0)
	r1 + r2 + l1 + u1 + u2 + e1
}
`
	got := mustFormat(t, src, format.DefaultOptions())
	if got != want {
		t.Fatalf("coalesce formatting mismatch!\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestNonAssociativeComparisonParensPreserved(t *testing.T) {
	src := `bind check = fn(a: int, b: int, c: int, f: bool) bool: {
    let e1 = (a == b) == f
    let e2 = f == (a != b)
    let r1 = (a < b) <= c
    let r2 = a < (b <= c)
    let m1 = a < b == f
    e1 and e2 and r1 and r2 and m1
}
`
	want := `bind check = fn(a: int, b: int, c: int, f: bool) bool: {
	let e1 = (a == b) == f
	let e2 = f == (a != b)
	let r1 = (a < b) <= c
	let r2 = a < (b <= c)
	let m1 = a < b == f
	e1 and e2 and r1 and r2 and m1
}
`
	got := mustFormat(t, src, format.DefaultOptions())
	if got != want {
		t.Fatalf("comparison parens mismatch!\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestSingleItemMultilineOmitsTrailingComma(t *testing.T) {
	src := `define Box struct [
    value: int,
]

bind main = fn(): {
    let str = ` + "`\"abc\"`" + `
    println(
        "jsoned: ${match(str.len()):
            when 1: "one"
            default: "other"}",
    )
    println(
        "first",
        "second",
    )
    let single_vec = [
        Box[
            value: 1,
        ],
    ]
}
`
	want := `define Box struct [
	value: int
]

bind main = fn(): {
	let str = ` + "`\"abc\"`" + `
	println(
		"jsoned: ${match(str.len()):
			when 1: "one"
			default: "other"}"
	)
	println(
		"first",
		"second",
	)
	let single_vec = [
		Box[
			value: 1
		]
	]
}
`
	got := mustFormat(t, src, format.DefaultOptions())
	if got != want {
		t.Fatalf("single-item multiline formatting mismatch!\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// A single-line `if` body that overflows LineWidth stays on the header line and wraps its
// call arguments; the second pass must not then move the (now multiline) call to its own line.
func TestOverflowingIfBodyIsIdempotent(t *testing.T) {
	src := `bind check = fn(idx: int, len: int): {
	if(idx < 0 or idx > len): panic("List.insert: index $idx out of bounds for length ${len} and some more text")
	let ok = true
}
`
	want := `bind check = fn(idx: int, len: int): {
	if(idx < 0 or idx > len): panic(
		"List.insert: index $idx out of bounds for length $len and some more text"
	)
	let ok = true
}
`
	if got := mustFormat(t, src, format.DefaultOptions()); got != want {
		t.Fatalf("unexpected output:\n%s\nwant:\n%s", got, want)
	}
}

// A call whose single list-literal argument overflows LineWidth must pick the compact
// `f([ ... ])` layout on the first pass, the same layout the second pass chooses. The trial
// render of the argument used to start at column 0, so deep nesting flipped the decision.
func TestOverflowingSingleCompositeArgIsIdempotent(t *testing.T) {
	src := `bind main = fn(): {
	let m = #[
		"key": [
			Json.Object(#[
				"b": Json.Array([Json.Number(Number.Int(1)), Json.Number(Number.Float(2.5)), Json.Bool(true), Json.Null]),
				"c": 1,
			]),
		],
	]
}
`
	want := `bind main = fn(): {
	let m = #[
		"key": [
			Json.Object(#[
				"b": Json.Array([
					Json.Number(Number.Int(1)),
					Json.Number(Number.Float(2.5)),
					Json.Bool(true),
					Json.Null,
				]),
				"c": 1,
			])
		]
	]
}
`
	if got := mustFormat(t, src, format.DefaultOptions()); got != want {
		t.Fatalf("unexpected output:\n%s\nwant:\n%s", got, want)
	}
}

// Result declarations, `!` names, and result chains (including `default panic` on one line and
// as a terminal arm) print back unchanged.
func TestResultDeclarationsAndChainsRoundTrip(t *testing.T) {
	src := `define Lookup result [
	value: string,
	Missing: string,
	Forbidden,
]

bind describe = fn(k: string) string: {
	let r! = find(k)
	let s = r!:
		when Missing(why): "missing:" + why // why it failed
		when Forbidden: func <- "denied"
		default panic
	let t = find(k): default panic
	let u = find(k):
		default Missing(own why): why
	if(r! as v): println(v) else: println("no")
	s + t + u + (find(k) ?? "fallback")
}
`
	if got := mustFormat(t, src, format.DefaultOptions()); got != src {
		t.Fatalf("unexpected output:\n%s\nwant:\n%s", got, src)
	}
}

func TestComptimeAndConstRoundTrip(t *testing.T) {
	src := `comptime if(LINUX): {
	const SEP = '/'

	extern "unistd.h"
} else comptime if(MACOS): {
	const SEP = '/'
} else: {
	const SEP = '\\'
}

comptime if(DEBUG): const LOG_LEVEL = 3
else: const LOG_LEVEL = 1

const LIMIT = 4

define Matrix[T, R: const int, C: const int] struct [
	data: [R: [C: T]]
]

const Matrix[T, R, C].zeros = fn() Matrix[T, R, C]: Matrix[T, R, C][
	data: comptime make_zero()
]

const double_arr[N: const int] = fn(arr: [N: int]) [N: int]: {
	const LOCAL = 2
	comptime for(i in 0..N) [N: int]: arr[i] * LOCAL
}

const demo = fn(v: [int]) int: {
	let sliced = v[0..2].len()
	let z = zeros[8]()
	let x = comptime (1 + 2)
	let y = comptime {
		const A = 10
		A + x
	}
	let picked = comptime if(LIMIT > 2): 100
	else comptime if(LIMIT == 2): 50
	else: 0
	outer: comptime for(i in 0..3): if(i == 1): next outer <-
	let matched = comptime match(LIMIT):
		when 4: 40
		default: 0
	let m: Matrix[int, 2 + 2, 4] = Matrix[int, 2 + 2, 4][data: z]
	sliced + picked + matched + y + m.data.len()
}

extern "opendir\$INODE64" as os_opendir: fn(path: ptr[u8]) ptr[u8]

bind mixed_if = fn(flag: bool) int: {
	let a = if(flag): {
		comptime if(LIMIT > 2): 1 else: 2
	} else: {
		comptime if(LIMIT > 2): 3 else: 4
	}
	let b = comptime if(LIMIT > 2): {
		if(flag): 5 else: 6
	} else: 7
	a + b
}
`
	out1 := mustFormat(t, src, format.DefaultOptions())
	out2 := mustFormat(t, out1, format.DefaultOptions())
	if out1 != out2 {
		t.Fatalf("comptime round-trip not idempotent!\n--- out1 ---\n%s--- out2 ---\n%s", out1, out2)
	}
}
