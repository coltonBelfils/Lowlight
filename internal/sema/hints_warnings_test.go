package sema_test

import (
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/diag"
	"lowlight/internal/sema"
)

func checkSrcWithStd(t *testing.T, src string) *diag.Reporter {
	t.Helper()
	rep := diag.NewReporter()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	_ = sema.Check("test.ll", src, root, rep)
	return rep
}

func TestCompilerWarnings(t *testing.T) {
	t.Run("unreachable_code", func(t *testing.T) {
		src := `bind f = fn(x: int) int: {
	if (x < 0): {
		func <- 0
		println("never")
		println("also never")
	}
	x
}
bind main = fn(): println(f(1))
`
		rep := checkSrc(src)
		if rep.HasErrors() {
			t.Fatalf("unexpected errors:\n%s", rep.Format())
		}
		var warns []diag.Diagnostic
		for _, d := range rep.Diagnostics() {
			if d.Severity == diag.Warning {
				warns = append(warns, d)
			}
		}
		if len(warns) != 1 || !strings.Contains(warns[0].Message, "unreachable code") {
			t.Fatalf("expected exactly 1 unreachable code warning, got: %+v", warns)
		}
	})

	t.Run("unreachable_tail", func(t *testing.T) {
		src := `bind f = fn() int: {
	panic("boom")
	42
}
bind main = fn(): println(f())
`
		rep := checkSrc(src)
		if rep.HasErrors() {
			t.Fatalf("unexpected errors:\n%s", rep.Format())
		}
		out := rep.Format()
		if !strings.Contains(out, "warning: unreachable code") {
			t.Fatalf("expected unreachable code warning, got:\n%s", out)
		}
	})

	t.Run("self_assignment", func(t *testing.T) {
		src := `define Box struct [val: int]
bind main = fn(): {
	let x = 1
	x = x
	let b = Box[val: 2]
	b.val = b.val
	println(x + b.val)
}
`
		rep := checkSrc(src)
		if rep.HasErrors() {
			t.Fatalf("unexpected errors:\n%s", rep.Format())
		}
		var warns int
		for _, d := range rep.Diagnostics() {
			if d.Severity == diag.Warning && strings.Contains(d.Message, "to itself") {
				warns++
			}
		}
		if warns != 2 {
			t.Fatalf("expected 2 self-assignment warnings, got %d:\n%s", warns, rep.Format())
		}
	})

	t.Run("self_comparison_skips_float_and_tests", func(t *testing.T) {
		src := `bind check_int = fn(x: int) bool: x == x
bind check_lt = fn(x: int) bool: x < x
bind check_nan = fn(f: float) bool: f != f

@test("reflexive eq in test is ok")
bind test_refl = fn(): {
	let a = 1
	assert(a == a)
}

bind main = fn(): {
	println(check_int(1))
	println(check_lt(1))
	println(check_nan(1.0))
}
`
		rep := checkSrc(src)
		if rep.HasErrors() {
			t.Fatalf("unexpected errors:\n%s", rep.Format())
		}
		var warns []string
		for _, d := range rep.Diagnostics() {
			if d.Severity == diag.Warning {
				warns = append(warns, d.Message)
			}
		}
		if len(warns) != 2 {
			t.Fatalf("expected 2 self-comparison warnings (not float NaN or @test), got %v", warns)
		}
	})
}

func TestCompilerHintsAndTypoSuggestions(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantMsgs []string
	}{
		{
			name:     "null_hint",
			src:      "bind main = fn(): { let x? = null }\n",
			wantMsgs: []string{"undeclared identifier", "Lowlight uses `none`"},
		},
		{
			name:     "break_hint",
			src:      "bind main = fn(): { for(i in 0..3): { break } }\n",
			wantMsgs: []string{"undeclared identifier", "for <-"},
		},
		{
			name:     "continue_hint",
			src:      "bind main = fn(): { for(i in 0..3): { continue } }\n",
			wantMsgs: []string{"undeclared identifier", "next <-"},
		},
		{
			name:     "none_on_non_optional",
			src:      "bind main = fn(): { let x = 1; println(x == none) }\n",
			wantMsgs: []string{"cannot compare non-optional", "only optional types"},
		},
		{
			name:     "none_on_result",
			src:      "bind load = fn() Result[int]: Result.value(1)\nbind main = fn(): { let r! = load(); println(r! == none) }\n",
			wantMsgs: []string{"cannot compare non-optional", "result chain"},
		},
		{
			name:     "deref_arena_ref",
			src:      "define Node struct [v: int]\nbind main = fn(): { let a = Arena.new(); let r = a.alloc(Node[v: 1]); unsafe { println(*r) } }\n",
			wantMsgs: []string{"cannot dereference non-pointer type", "dereferences automatically"},
		},
		{
			name:     "module_call_typo",
			src:      "import \"std/math\"\nbind main = fn(): println(math.sinn(1.0))\n",
			wantMsgs: []string{"module \"math\" has no function or type \"sinn\"", "did you mean `math.sin`"},
		},
		{
			name:     "module_symbol_typo",
			src:      "import \"std/math\"\nbind main = fn(): println(math.PII)\n",
			wantMsgs: []string{"module \"math\" has no exported symbol \"PII\"", "did you mean `math.PI`?"},
		},
		{
			name:     "enum_variant_typo",
			src:      "define Color enum [Red, Green, Blue]\nbind main = fn(): println(Color.Redd)\n",
			wantMsgs: []string{"enum Color has no variant \"Redd\"", "did you mean `Color.Red`?"},
		},
		{
			name:     "enum_call_typo",
			src:      "define Val enum [Num: int]\nbind main = fn(): println(Val.Numm(1))\n",
			wantMsgs: []string{"enum Val has no variant or method \"Numm\"", "did you mean `Val.Num`?"},
		},
		{
			name:     "tuple_bracket_index",
			src:      "bind main = fn(): { let t = +[1, \"a\"]; println(t[0]) }\n",
			wantMsgs: []string{"tuple indexing with brackets is not supported", "dot index: `.0`"},
		},
		{
			name:     "void_var_hint",
			src:      "bind noop = fn(): {}\nbind main = fn(): { let x = noop() }\n",
			wantMsgs: []string{"`void` non-value expression", "without `let`/`bind`"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := checkSrcWithStd(t, tc.src)
			if !rep.HasErrors() {
				t.Fatalf("expected error, got none")
			}
			out := rep.Format()
			for _, w := range tc.wantMsgs {
				if !strings.Contains(out, w) {
					t.Errorf("expected diagnostic output to contain %q, got:\n%s", w, out)
				}
			}
		})
	}
}
