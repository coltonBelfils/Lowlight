package lsp_test

import (
	"strings"
	"testing"

	"lowlight/internal/lsp"
)

func TestLSPNewQuickFixes(t *testing.T) {
	cases := []struct {
		name, src, title, want string
	}{
		{
			name:  "tuple_index",
			src:   "bind main = fn(): {\n\tlet t = +[10, \"hi\"]\n\tprintln(t[0])\n}\n",
			title: "Replace `[0]` with `.0`",
			want:  "println(t.0)",
		},
		{
			name:  "drop_quest_dot_field",
			src:   "define Point struct [x: int]\nbind get_pt = fn() Point: Point[x: 1]\nbind main = fn(): println(get_pt()?.x)\n",
			title: "Replace `?.` with `.`",
			want:  "println(get_pt().x)",
		},
		{
			name:  "drop_quest_dot_method",
			src:   "bind get_str = fn() string: \"abc\"\nbind main = fn(): println(get_str()?.len())\n",
			title: "Replace `?.` with `.`",
			want:  "println(get_str().len())",
		},
		{
			name:  "remove_parens_unit_variant",
			src:   "@derive(Equatable)\ndefine Status enum [Active, Stopped]\nbind main = fn(): {\n\tlet s = Status.Active()\n\tprintln(s == Status.Active)\n}\n",
			title: "Remove `()`",
			want:  "let s = Status.Active\n",
		},
		{
			name:  "remove_parens_unit_result",
			src:   "define Done result [value, err: string]\nbind f = fn(ok: bool) Done: if(ok): value() else: err(\"no\")\nbind main = fn(): f(true): default panic\n",
			title: "Remove `()`",
			want:  "if(ok): value else:",
		},
		{
			name:  "append_copy_borrowed",
			src:   "bind take = fn(s: own string): println(s)\nbind use = fn(s: string): take(s)\nbind main = fn(): use(\"hi\")\n",
			title: "Append `.copy()`",
			want:  "take(s.copy())",
		},
		{
			name:  "append_copy_field",
			src:   "define User struct [name: string]\nbind take = fn(s: own string): println(s)\nbind main = fn(): {\n\tlet u = User[name: \"ada\".copy()]\n\ttake(u.name)\n\tprintln(u.name)\n}\n",
			title: "Append `.copy()`",
			want:  "take(u.name.copy())",
		},
		{
			name:  "strip_ll_import",
			src:   "import \"std/math.ll\"\nbind main = fn(): println(math.clamp_int(5, 0, 10))\n",
			title: "Change import to \"std/math\"",
			want:  "import \"std/math\"",
		},
		{
			name:  "insert_colon_before_brace",
			src:   "bind main = fn(): {\n\tlet x = 1\n\tif(x > 0) {\n\t\tprintln(x)\n\t}\n}\n",
			title: "Insert `:` before `{`",
			want:  "if(x > 0): {",
		},
		{
			name:  "else_to_default_in_match",
			src:   "bind main = fn(): {\n\tlet x = 1\n\tlet y = match(x):\n\t\twhen 1: 10\n\t\telse: 20\n\tprintln(y)\n}\n",
			title: "Replace `else` with `default`",
			want:  "\t\tdefault: 20",
		},
		{
			name:  "strip_opt_suffix_in_if_as",
			src:   "bind find = fn() ?int: 42\nbind main = fn(): {\n\tif(find() as v?): println(v)\n}\n",
			title: "Remove `?` (`v`)",
			want:  "if(find() as v): println(v)",
		},
		{
			name:  "nonzero_bool_mismatch",
			src:   "bind main = fn(): {\n\tlet n = 3\n\tlet ok: bool = n\n\tprintln(ok)\n}\n",
			title: "Compare with `!= 0`",
			want:  "let ok: bool = n != 0",
		},
		{
			name:  "to_string_printable_struct",
			src:   "@derive(Printable)\ndefine Point struct [x: int]\nbind main = fn(): {\n\tlet p = Point[x: 1]\n\tlet s: string = p\n\tprintln(s)\n}\n",
			title: "Convert with `.to_string()`",
			want:  "let s: string = p.to_string()",
		},
		{
			name:  "redundant_cl_wrapper",
			src:   "bind main = fn(): {\n\tlet k = 2\n\tlet f = cl(x: int) int: x + k\n\tlet g = cl(f)\n\tprintln(g(1))\n}\n",
			title: "Remove redundant `cl(...)`",
			want:  "let g = f\n",
		},
		{
			name:  "rename_module_call_typo",
			src:   "import \"std/math\"\nbind main = fn(): println(math.sinn(1.0))\n",
			title: "Change to `sin`",
			want:  "math.sin(1.0)",
		},
		{
			name:  "rename_enum_variant_typo",
			src:   "@derive(Equatable)\ndefine Color enum [Red, Blue]\nbind main = fn(): println(Color.Redd == Color.Blue)\n",
			title: "Change to `Red`",
			want:  "Color.Red == Color.Blue",
		},
		{
			name:  "null_to_none",
			src:   "bind main = fn(): {\n\tlet x?: ?int = null\n\tprintln(x? ?? 0)\n}\n",
			title: "Change to `none`",
			want:  "let x?: ?int = none",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rh := newResultHarness(t, tc.src)
			diags := rh.h.waitDiagnostics(rh.uri)
			var actions []lsp.CodeAction
			rh.h.request("textDocument/codeAction", lsp.CodeActionParams{
				TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
				Range:        lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 20}},
				Context:      lsp.CodeActionContext{Diagnostics: diags},
			}, &actions)
			var titles []string
			for _, a := range actions {
				titles = append(titles, a.Title)
				if a.Title != tc.title {
					continue
				}
				out := applyTextEdits(tc.src, a.Edit.Changes[rh.uri])
				if !strings.Contains(out, tc.want) {
					t.Fatalf("fix produced:\n%s\nwant it to contain %q", out, tc.want)
				}
				rh.h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
					TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: rh.uri, Version: 2},
					ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: out}},
				})
				if after := rh.h.waitDiagnostics(rh.uri); len(after) != 0 {
					t.Fatalf("after the fix:\n%s\ndiagnostics remain: %+v", out, after)
				}
				return
			}
			t.Fatalf("no %q action; diagnostics %+v; actions %v", tc.title, diags, titles)
		})
	}
}

func TestLSPMustUsePreservesSigils(t *testing.T) {
	src := `@must_use
bind find = fn() ?int: 42

@must_use
bind load = fn() Result[int]: Result.value(1)

bind main = fn(): {
	find()
	load()
	println("done")
}
`
	rh := newResultHarness(t, src)
	diags := rh.h.waitDiagnostics(rh.uri)
	if len(diags) != 2 {
		t.Fatalf("expected 2 @must_use warnings, got %+v", diags)
	}
	for i, wantTitle := range []string{"Bind return value ('let res? = ...')", "Bind return value ('let res! = ...')"} {
		var actions []lsp.CodeAction
		rh.h.request("textDocument/codeAction", lsp.CodeActionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
			Range:        diags[i].Range,
			Context:      lsp.CodeActionContext{Diagnostics: []lsp.Diagnostic{diags[i]}},
		}, &actions)
		found := false
		for _, a := range actions {
			if a.Title == wantTitle {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in actions for diag %d, got %+v", wantTitle, i, actions)
		}
	}
}

func TestLSPToggleBindLetAndPropagateRefactorings(t *testing.T) {
	src := `bind load = fn() Result[int]: Result.value(1)

bind run = fn() Result[int]: {
	let fixed = 10
	let mut_val = 20
	mut_val += 1
	let xs = [1, 2]
	xs.push(3)
	bind imm = 5
	let v = load(): default panic
	value(fixed + mut_val + xs.len() + imm + v)
}

bind main = fn(): println(run() ?? 0)
`
	rh := newResultHarness(t, src)
	if diags := rh.h.waitDiagnostics(rh.uri); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	actionsAt := func(needle string) map[string]lsp.CodeAction {
		pos := rh.at(needle, 0, 1)
		var actions []lsp.CodeAction
		rh.h.request("textDocument/codeAction", lsp.CodeActionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
			Range:        lsp.Range{Start: pos, End: pos},
		}, &actions)
		out := map[string]lsp.CodeAction{}
		for _, a := range actions {
			out[a.Title] = a
		}
		return out
	}

	// `fixed` is never mutated -> offer `let` -> `bind`
	fixedActs := actionsAt("let fixed")
	if a, ok := fixedActs["Convert `let` to `bind` on `fixed` (never mutated)"]; !ok {
		t.Fatalf("expected let->bind action for fixed, got %v", fixedActs)
	} else if out := applyTextEdits(src, a.Edit.Changes[rh.uri]); !strings.Contains(out, "bind fixed = 10") {
		t.Errorf("expected bind fixed = 10, got:\n%s", out)
	}

	// `mut_val` and `xs` ARE mutated -> must NOT offer `let` -> `bind`
	if _, ok := actionsAt("let mut_val")["Convert `let` to `bind` on `mut_val` (never mutated)"]; ok {
		t.Errorf("must not offer let->bind for mutated mut_val")
	}
	if _, ok := actionsAt("let xs")["Convert `let` to `bind` on `xs` (never mutated)"]; ok {
		t.Errorf("must not offer let->bind for mutated xs")
	}

	// `imm` is `bind` -> offer `bind` -> `let`
	if a, ok := actionsAt("bind imm")["Convert `bind` to `let` on `imm` (make mutable)"]; !ok {
		t.Errorf("expected bind->let action for imm")
	} else if out := applyTextEdits(src, a.Edit.Changes[rh.uri]); !strings.Contains(out, "let imm = 5") {
		t.Errorf("expected let imm = 5, got:\n%s", out)
	}

	// `default panic` -> offer propagate failure
	propActs := actionsAt("default panic")
	a, ok := propActs["Propagate failure with `default err`"]
	if !ok {
		t.Fatalf("expected propagate failure action, got %v", propActs)
	}
	out := applyTextEdits(src, a.Edit.Changes[rh.uri])
	if !strings.Contains(out, "default err(own e): func <- err(e.wrap(\"todo\"))") {
		t.Errorf("unexpected propagated result chain:\n%s", out)
	}
	rh.h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: rh.uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: out}},
	})
	if after := rh.h.waitDiagnostics(rh.uri); len(after) != 0 {
		t.Fatalf("propagated result chain left diagnostics: %+v\n%s", after, out)
	}
}

func TestLSPInlayHintsHoverAndLocalSigHelp(t *testing.T) {
	src := `define Lookup result [
	value: int,
	Missing: string,
]

define Config struct [port: int]

bind bump = fn(x: &int): { x += 1 }
bind connect = fn(port: int): println(port)

bind main = fn(): {
	let items = ["a", "b"]
	for(i, item in items): println("${i}: ${item}")

	let maybe?: ?int = 7
	if(maybe? as val): println(val)

	let r!: Lookup = Lookup.Missing("nope")
	let _ = r!:
		default Missing(why): {
			println(why)
			0
		}

	let x = 1
	bump(&x)
	let cfg = Config[port: 8080]
	connect(cfg.port)

	let adder = fn(lhs: int, rhs: int) int: lhs + rhs
	println(adder(1, 2))
}
`
	rh := newResultHarness(t, src)
	if diags := rh.h.waitDiagnostics(rh.uri); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	// 1. Inlay hints for loop vars, `if ... as`, pattern bindings, and param elision
	var hints []lsp.InlayHint
	rh.h.request("textDocument/inlayHint", lsp.InlayHintParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Range:        lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 40}},
	}, &hints)

	hasHint := func(needle string, kind lsp.InlayHintKind) bool {
		for _, h := range hints {
			if h.Kind == kind && h.Label == needle {
				return true
			}
		}
		return false
	}
	if !hasHint(": int", lsp.InlayHintType) || !hasHint(": string", lsp.InlayHintType) {
		t.Errorf("expected loop/if-as/pattern type inlay hints, got %+v", hints)
	}
	// Verify `val` in `if(maybe? as val)` and `why` in `Missing(why)` got type hints at their exact lines.
	valLine := rh.at("as val", 0, 3).Line
	whyLine := rh.at("Missing(why)", 0, 8).Line
	forLine := rh.at("for(i, item", 0, 4).Line
	foundVal, foundWhy, foundForInt, foundForStr := false, false, false, false
	for _, h := range hints {
		if h.Kind != lsp.InlayHintType {
			continue
		}
		if h.Position.Line == valLine && h.Label == ": int" {
			foundVal = true
		}
		if h.Position.Line == whyLine && h.Label == ": string" {
			foundWhy = true
		}
		if h.Position.Line == forLine && h.Label == ": int" {
			foundForInt = true
		}
		if h.Position.Line == forLine && h.Label == ": string" {
			foundForStr = true
		}
	}
	if !foundVal || !foundWhy || !foundForInt || !foundForStr {
		t.Errorf("missing expected type inlay hints: val=%v why=%v forInt=%v forStr=%v\nhints=%+v",
			foundVal, foundWhy, foundForInt, foundForStr, hints)
	}
	// Parameter hints should be elided for `bump(&x)` and `connect(cfg.port)`.
	for _, h := range hints {
		if h.Kind == lsp.InlayHintParameter && (h.Label == "x: " || h.Label == "port: ") {
			t.Errorf("expected parameter hint %q to be elided for matching &x / cfg.port, got %+v", h.Label, h)
		}
	}

	// 2. Hover on `item` in `for(i, item in items)` and `val` in `if(maybe? as val)`
	if md := rh.hover(rh.at("item in items", 0, 1)); !strings.Contains(md, "item: string") {
		t.Errorf("expected hover on loop variable `item`, got %q", md)
	}
	if md := rh.hover(rh.at("as val", 0, 3)); !strings.Contains(md, "val: int") {
		t.Errorf("expected hover on `if ... as val`, got %q", md)
	}

	// 3. Signature help on local `fn` variable `adder(1, 2)`
	var sig lsp.SignatureHelp
	rh.h.request("textDocument/signatureHelp", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Position:     rh.at("adder(1, 2)", 0, len("adder(1, ")),
	}, &sig)
	if len(sig.Signatures) == 0 || !strings.Contains(sig.Signatures[0].Label, "lhs: int, rhs: int") || sig.ActiveParameter != 1 {
		t.Errorf("unexpected local fn signature help: %+v", sig)
	}
}
