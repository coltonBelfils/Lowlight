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
	src := `bind add = (a: int, b: int) int: {
    a + b
}

bind classify = (x: int) string: {
    match (x):
        when 0: {
            "zero"
        }
        default: {
            "nonzero"
        }
}

bind clamp = (x: int) int: {
    if (x < 0): {
        0
    } else if (x > 10): {
        10
    } else: {
        x
    }
}

bind ptr_read = (p: ptr[int]) int: {
    unsafe {
        *p
    }
}

bind sum_loop = (n: int) int: {
    let total = 0
    for (i in 0 .. n): {
        total += i
    }
    total
}

bind keep_void_semi = (x: int): {
    add(x, 1);
}

bind keep_labeled = () int: {
    outer: {
        outer <- 5
    }
}
`
	want := `bind add = (a: int, b: int) int: a + b

bind classify = (x: int) string:
	match(x):
		when 0: "zero"
		default: "nonzero"

bind clamp = (x: int) int:
	if(x < 0): 0
	else if(x > 10): 10
	else: x

bind ptr_read = (p: ptr[int]) int: unsafe *p

bind sum_loop = (n: int) int: {
	let total = 0
	for(i in 0..n): total += i
	total
}

bind keep_void_semi = (x: int): {
	add(x, 1);
}

bind keep_labeled = () int: outer: {
	outer <- 5
}
`
	got := mustFormat(t, src, format.DefaultOptions())
	if got != want {
		t.Fatalf("mismatch!\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestControlFlowAndOperatorFormatting(t *testing.T) {
	src := `bind check = (a: int, b: int, c: int) bool: {
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
	src := `bind f = (alpha: int, beta: int, gamma: int, delta: int) int: {
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
bind greet = (name: string) string: "hi, $name ${name.len()}!"
bind raw = ` + "`" + `hello\n$world` + "`" + `

/*
 * Block doc comment.
 */
define Point struct [
    // X axis.
    x: int,
    y: int, // Y axis.
]

bind keep_comment_block = (x: int) int: {
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

bind Point.sum = (self) int: { self.x + self.y }

bind main = () int: {
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

