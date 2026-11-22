package lsp_test

import (
	"strings"
	"testing"

	"lowlight/internal/lsp"
)

const bareEnumMatchSrc = `// A simple expression node.
define Expr enum [
	// An empty node.
	Empty,
	// A text node.
	Text: string,
]

bind make_res = fn(ok: bool) Result[string]:
	if (ok): value("ok") else: err(Error.new("bad"))

bind describe = fn(e: own Expr, r!: own Result[string]) string: {
	let part = match (own e):
		when Empty: "empty"
		default Text(own s): s
	let out = match (own r!):
		when value(own o): o
		default err(own err_val): err_val.message()
	part + ":" + out
}

bind main = fn(): {
	let e = Expr.Text("hi")
	let r! = make_res(true)
	println(describe(own e, own r!))
}
`

// Hover and go-to-definition resolve bare unit variants, bare payload variants, and pattern
// bindings at both offset 0 and offset 1.
func TestLSPBareEnumMatchHoverAndDefinition(t *testing.T) {
	rh := newResultHarness(t, bareEnumMatchSrc)
	if diags := rh.h.waitDiagnostics(rh.uri); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	// Lines (0-based):
	// 3: Empty,
	// 5: Text: string,
	for _, delta := range []int{0, 1} {
		if md := rh.hover(rh.at("Empty: \"empty\"", 0, delta)); !strings.Contains(md, "Empty") || !strings.Contains(md, "An empty node") {
			t.Errorf("hover on bare unit variant (delta=%d): %q", delta, md)
		}
		if md := rh.hover(rh.at("Text(own s)", 0, delta)); !strings.Contains(md, "Text") || !strings.Contains(md, "A text node") {
			t.Errorf("hover on bare payload variant (delta=%d): %q", delta, md)
		}
		if md := rh.hover(rh.at("value(own o)", 0, delta)); !strings.Contains(md, "value") || !strings.Contains(md, "Result") {
			t.Errorf("hover on bare Result.value (delta=%d): %q", delta, md)
		}
		if md := rh.hover(rh.at("err(own err_val)", 0, delta)); !strings.Contains(md, "err") || !strings.Contains(md, "Result") {
			t.Errorf("hover on bare Result.err (delta=%d): %q", delta, md)
		}
		if got := rh.definitionLine(rh.at("Empty: \"empty\"", 0, delta)); got != 3 {
			t.Errorf("definition of bare `Empty` (delta=%d) = line %d, want 3", delta, got)
		}
		if got := rh.definitionLine(rh.at("Text(own s)", 0, delta)); got != 5 {
			t.Errorf("definition of bare `Text` (delta=%d) = line %d, want 5", delta, got)
		}
	}

	// Pattern bindings `s` and `o`.
	if md := rh.hover(rh.at("own s): s", 0, len("own "))); !strings.Contains(md, "s") || !strings.Contains(md, "string") {
		t.Errorf("hover on binding `s`: %q", md)
	}
	if md := rh.hover(rh.at("own o): o", 0, len("own "))); !strings.Contains(md, "o") || !strings.Contains(md, "string") {
		t.Errorf("hover on binding `o`: %q", md)
	}
}

// Find references and rename across bare unit variants, bare payload variants, qualified variants,
// and bare pattern bindings.
func TestLSPBareEnumMatchReferencesAndRename(t *testing.T) {
	src := `define Expr enum [
	Empty,
	Text: string,
]

bind use_it = fn(e: own Expr, r!: own Result[string]) string: {
	let a = match (own e):
		when Empty: "empty"
		default Text(own s): s
	let b = match (own r!):
		when value(own o): o
		default err(own err_val): err_val.message()
	a + b
}

bind main = fn(): {
	let e1 = Expr.Empty
	let e2 = Expr.Text("hi")
	println(use_it(own e1, Result.value("x")))
	println(use_it(own e2, Result.value("y")))
}
`
	rh := newResultHarness(t, src)
	if diags := rh.h.waitDiagnostics(rh.uri); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	// 1. References & rename on payload variant `Text` (declaration, bare match pattern, qualified Expr.Text).
	var textRefs []lsp.Location
	rh.h.request("textDocument/references", lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Position:     rh.at("Text(own s)", 0, 1),
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}, &textRefs)
	if len(textRefs) != 3 {
		t.Errorf("expected 3 references for `Text` (decl, bare pattern, qualified ctor), got %d: %+v", len(textRefs), textRefs)
	}

	var textRename lsp.WorkspaceEdit
	rh.h.request("textDocument/rename", lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Position:     rh.at("Text(own s)", 0, 1),
		NewName:      "Str",
	}, &textRename)
	renamedText := applyTextEdits(src, textRename.Changes[rh.uri])
	for _, want := range []string{"Str: string,", "default Str(own s): s", `Expr.Str("hi")`} {
		if !strings.Contains(renamedText, want) {
			t.Errorf("variant payload rename missing %q in:\n%s", want, renamedText)
		}
	}

	// 2. References & rename on unit variant `Empty` (declaration, bare match pattern, qualified Expr.Empty).
	var emptyRefs []lsp.Location
	rh.h.request("textDocument/references", lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Position:     rh.at("Empty: \"empty\"", 0, 1),
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}, &emptyRefs)
	if len(emptyRefs) != 3 {
		t.Errorf("expected 3 references for `Empty` (decl, bare pattern, qualified ctor), got %d: %+v", len(emptyRefs), emptyRefs)
	}

	var emptyRename lsp.WorkspaceEdit
	rh.h.request("textDocument/rename", lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Position:     rh.at("Empty: \"empty\"", 0, 1),
		NewName:      "Blank",
	}, &emptyRename)
	renamedEmpty := applyTextEdits(src, emptyRename.Changes[rh.uri])
	for _, want := range []string{"\tBlank,\n", "when Blank: \"empty\"", "Expr.Blank"} {
		if !strings.Contains(renamedEmpty, want) {
			t.Errorf("variant unit rename missing %q in:\n%s", want, renamedEmpty)
		}
	}

	// 3. Rename on pattern binding `o` in `when value(own o): o`.
	var bindRename lsp.WorkspaceEdit
	rh.h.request("textDocument/rename", lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Position:     rh.at("own o): o", 0, len("own ")),
		NewName:      "val",
	}, &bindRename)
	renamedBind := applyTextEdits(src, bindRename.Changes[rh.uri])
	if !strings.Contains(renamedBind, "when value(own val): val") {
		t.Errorf("pattern binding rename did not update decl & body:\n%s", renamedBind)
	}
}

// Bare unit & payload variants emit `enumMember` semantic tokens, and pattern bindings emit `variable`.
func TestLSPBareEnumMatchSemanticTokens(t *testing.T) {
	rh := newResultHarness(t, bareEnumMatchSrc)
	_ = rh.h.waitDiagnostics(rh.uri)
	var toks lsp.SemanticTokens
	rh.h.request("textDocument/semanticTokens/full", lsp.SemanticTokensParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
	}, &toks)
	const (
		semVariable   = 7
		semEnumMember = 9
	)
	type tok struct{ line, col, length, typ int }
	var decoded []tok
	line, col := 0, 0
	for i := 0; i+4 < len(toks.Data); i += 5 {
		if toks.Data[i] > 0 {
			line += int(toks.Data[i])
			col = int(toks.Data[i+1])
		} else {
			col += int(toks.Data[i+1])
		}
		decoded = append(decoded, tok{line, col, int(toks.Data[i+2]), int(toks.Data[i+3])})
	}
	typeAt := func(p lsp.Position) int {
		for _, d := range decoded {
			if d.line == p.Line && d.col == p.Character {
				return d.typ
			}
		}
		return -1
	}
	for _, needle := range []string{"Empty: \"empty\"", "Text(own s)", "value(own o)", "err(own err_val)"} {
		if got := typeAt(rh.at(needle, 0, 0)); got != semEnumMember {
			t.Errorf("token %q: type %d, want enumMember (%d)", needle, got, semEnumMember)
		}
	}
	for _, c := range []struct {
		needle string
		delta  int
	}{
		{"own s): s", len("own ")},
		{"own o): o", len("own ")},
		{"own err_val): err_val", len("own ")},
	} {
		if got := typeAt(rh.at(c.needle, 0, c.delta)); got != semVariable {
			t.Errorf("binding %q: type %d, want variable (%d)", c.needle, got, semVariable)
		}
	}
}

// Completion inside `match(e):` offers bare enum variants (including `value` and `err` for Result[T])
// at `when |` / `default |`, and offers pattern bindings inside the arm body.
func TestLSPBareEnumMatchCompletion(t *testing.T) {
	completeAt := func(srcWithMarker string) []string {
		idx := strings.Index(srcWithMarker, "/*CURSOR*/")
		if idx < 0 {
			t.Fatal("missing /*CURSOR*/ marker")
		}
		clean := srcWithMarker[:idx] + srcWithMarker[idx+len("/*CURSOR*/"):]
		rh := newResultHarness(t, clean)
		_ = rh.h.waitDiagnostics(rh.uri)
		return rh.complete(lsp.OffsetToLSP(clean, idx))
	}

	enumWhenSrc := `define Expr enum [
	Empty,
	Text: string,
]

bind f = fn(e: Expr) string:
	match (e):
		when /*CURSOR*/
`
	if miss := containsAll(completeAt(enumWhenSrc), "Empty", "Text"); len(miss) > 0 {
		t.Errorf("match enum `when` completion missing %v", miss)
	}

	resDefSrc := `bind f = fn(r!: Result[string]) string:
	match (r!):
		default /*CURSOR*/
`
	if miss := containsAll(completeAt(resDefSrc), "value", "err"); len(miss) > 0 {
		t.Errorf("match result `default` completion missing %v", miss)
	}

	bodySrc := `define Expr enum [
	Empty,
	Text: string,
]

bind f = fn(e: own Expr) string:
	match (own e):
		when Text(own msg): m/*CURSOR*/
		default: ""
`
	if miss := containsAll(completeAt(bodySrc), "msg"); len(miss) > 0 {
		t.Errorf("bare pattern arm body completion missing %v", miss)
	}

	multiSrc := `define Expr enum [
	Empty,
	Text: string,
]

bind f = fn(e: Expr, r!: Result[int]) string:
	match (e, r!):
		when Empty, /*CURSOR*/
`
	if miss := containsAll(completeAt(multiSrc), "value", "err"); len(miss) > 0 {
		t.Errorf("multi-arg match slot 1 completion missing %v", miss)
	}
}

// "Generate missing match arms" preserves bare style when the match already uses bare enum patterns.
func TestLSPGenerateMissingArmsBareEnumMatch(t *testing.T) {
	src := `define Status enum [
	Active,
	Paused,
	Stopped,
]

bind label = fn(s: Status) string:
	match (s):
		when Active: "a"
		default Paused: "p"
`
	rh := newResultHarness(t, src)
	diags := rh.h.waitDiagnostics(rh.uri)
	var actions []lsp.CodeAction
	rh.h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Range:        lsp.Range{Start: lsp.Position{Line: 7}, End: lsp.Position{Line: 9}},
		Context:      lsp.CodeActionContext{Diagnostics: diags},
	}, &actions)
	var out string
	found := false
	for _, a := range actions {
		if a.Title == "Generate missing match arms (exhaustive 'default Pattern')" {
			found = true
			out = applyTextEdits(src, a.Edit.Changes[rh.uri])
			break
		}
	}
	if !found {
		t.Fatalf("missing exhaustive arm action; diags: %+v", diags)
	}
	want := "\t\twhen Paused: \"p\"\n\t\tdefault Stopped: panic(\"todo\")\n"
	if !strings.Contains(out, want) {
		t.Fatalf("expected %q in:\n%s", want, out)
	}
	rh.h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: rh.uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: out}},
	})
	if rem := rh.h.waitDiagnostics(rh.uri); len(rem) != 0 {
		t.Fatalf("generated bare arms left diagnostics: %+v\n%s", rem, out)
	}
}
