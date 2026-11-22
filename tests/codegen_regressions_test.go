package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// Optional chaining through a self-referential (boxed) optional field used to emit
// `getelementptr inbounds ptr, ...` in the LLVM backend, which clang rejects.
func TestOptionalChainThroughBoxedSelfReferentialField(t *testing.T) {
	t.Parallel()
	src := `
define Node struct [ val: int, name: string, next?: ?Node ]
bind Node.label = fn(self) string: "${self.name}#${self.val}"
bind main = fn(): {
	let list = Node[val: 1, name: "a", next?: Node[val: 2, name: "b", next?: Node[val: 3, name: "c", next?: none]]]
	println("${list.next?.val ?? -1}")
	println("${list.next?.next?.val ?? -1}")
	println("${list.next?.next?.name ?? "-"}")
	println("${list.next?.next?.next?.val ?? -1}")
	println("${list.next?.next?.next?.name ?? "-"}")
	println("${list.next?.label() ?? "-"}")
	let n? = list.next?.next?.name.len()
	println("${n? ?? -1}")
}
`
	want := "2\n3\nc\n-1\n-\nb#2\n1"
	for _, release := range []bool{false, true} {
		out := runInlineStrict(t, "optchain_boxed_self_ref", src, release)
		if got := strings.TrimSpace(out); got != want {
			t.Fatalf("release=%v: unexpected output:\n%s\nwant:\n%s", release, got, want)
		}
	}
}

// Emitted LLVM IR and C must be byte-identical across runs (reproducible builds, stable
// object-cache keys, and Stage 2/3 self-hosting parity). Type collection used to walk a
// pointer-keyed Go map, which reordered function-type helpers from run to run.
func TestCodegenOutputIsDeterministic(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	cases := []struct {
		path     string
		testMode bool
	}{
		{"examples/mini_lexer_eval.ll", false},
		{"examples/generics_and_pool.ll", false},
		{"examples/interfaces_shapes.ll", false},
		{"tests/core_test.ll", true},
		{"std/http/http.ll", true},
	}
	const runs = 5
	for _, tc := range cases {
		for _, backend := range []string{"llvm", "c11"} {
			var first string
			for i := 0; i < runs; i++ {
				opts := driver.Options{
					EntryPath:   filepath.Join(root, tc.path),
					ProjectRoot: root,
					TestMode:    tc.testMode,
				}
				var out string
				var err error
				if backend == "llvm" {
					out, _, err = driver.EmitLLVM(opts)
				} else {
					out, _, err = driver.EmitC(opts)
				}
				if err != nil {
					t.Fatalf("%s [%s]: emit failed: %v", tc.path, backend, err)
				}
				if i == 0 {
					first = out
				} else if out != first {
					t.Fatalf("%s [%s]: emitted code differs between run 1 and run %d", tc.path, backend, i+1)
				}
			}
		}
	}
}

// Several top-level list constants used to emit duplicate `alloca` names in `main` with the
// LLVM backend, because each global's temporaries are numbered separately.
func TestMultipleListConstantsInOneProgram(t *testing.T) {
	t.Parallel()
	src := `
bind A: [int] = [1, 2]
bind B: [int] = [3]
bind C: [string] = ["x", "y"]

bind main = fn(): println(A.len() + B.len() + C.len())
`
	if out := strings.TrimSpace(runInlineStrict(t, "multi_list_consts", src, false)); out != "5" {
		t.Fatalf("unexpected output %q", out)
	}
}
