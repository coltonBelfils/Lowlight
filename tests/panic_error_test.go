package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// runPanicking runs `src` (expected to panic) and returns its exit code, stdout, and stderr.
func runPanicking(t *testing.T, name, src string, backend driver.BackendKind) (int, string, string) {
	t.Helper()
	entry := filepath.Join(t.TempDir(), name+".ll")
	var stdout, stderr bytes.Buffer
	rc, err := driver.Run(driver.Options{
		EntryPath:   entry,
		SrcOverride: src,
		ProjectRoot: projectRoot(t),
		Backend:     backend,
	}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("[%s %s] build failed: %v\nstderr:\n%s", name, backend, err, stderr.String())
	}
	return rc, stdout.String(), stderr.String()
}

// `panic` shares the `Error` type with `Result`: `panic(err)`, `unwrap`, and `expect` all print
// the full error chain and trace attributed to the user's call site (SPECIFICATION.md §9.6.1).
func TestPanicWithErrorSharesResultFormatting(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "panic_error",
			body: `panic(Error.coded("bad_state", "queue drained").wrap("worker loop"))`,
			want: []string{
				"lowlight panic at panic_error.ll:6:2: [bad_state] worker loop: queue drained",
				"trace:",
				"— worker loop",
				"— queue drained",
			},
		},
		{
			name: "unwrap_err",
			body: `let v = load().unwrap()`,
			want: []string{
				"lowlight panic at unwrap_err.ll:6:10: [not_found] called Result.unwrap on an error: config missing",
				"at unwrap_err.ll:6:10 — called Result.unwrap on an error",
				"at unwrap_err.ll:3:",
			},
		},
		{
			name: "expect_err",
			body: `let v = load().expect("loading config")`,
			want: []string{
				"lowlight panic at expect_err.ll:6:10: [not_found] loading config: config missing",
				"at expect_err.ll:6:10 — loading config",
			},
		},
		{
			name: "panic_string",
			body: `panic("plain message")`,
			want: []string{"lowlight panic at panic_string.ll:6:2: plain message"},
		},
	}
	for _, tc := range cases {
		src := "\nbind load = () Result[int]:\n" +
			"\tResult[int].fail(Error.coded(\"not_found\", \"config missing\"))\n" +
			"bind main = (): {\n" +
			"\tprintln(\"before\")\n" +
			"\t" + tc.body + "\n" +
			"}\n"
		for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
			rc, stdout, stderr := runPanicking(t, tc.name, src, backend)
			if rc != 1 {
				t.Fatalf("[%s %s] exit code = %d, want 1\nstderr:\n%s", tc.name, backend, rc, stderr)
			}
			if stdout != "before\n" {
				t.Fatalf("[%s %s] stdout = %q, want %q (stdout must be flushed before the panic)", tc.name, backend, stdout, "before\n")
			}
			for _, w := range tc.want {
				if !strings.Contains(stderr, w) {
					t.Fatalf("[%s %s] stderr missing %q:\n%s", tc.name, backend, w, stderr)
				}
			}
		}
	}
}

// `panic` accepts only a `string` message or an `Error`.
func TestPanicRejectsOtherArgumentTypes(t *testing.T) {
	for _, body := range []string{`panic(42)`, `panic([1, 2])`, `panic("a", "b")`} {
		src := "bind main = (): {\n\t" + body + "\n}\n"
		_, _, err := driver.Check(driver.Options{
			EntryPath:   filepath.Join(t.TempDir(), "bad_panic.ll"),
			SrcOverride: src,
			ProjectRoot: projectRoot(t),
		})
		if err == nil || !strings.Contains(err.Error(), "`panic` expects") {
			t.Fatalf("%s: expected a `panic` argument error, got %v", body, err)
		}
	}
}
