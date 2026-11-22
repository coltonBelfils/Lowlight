package lsp_test

import (
	"strings"
	"testing"

	"lowlight/internal/lsp"
)

func (rh *resultHarness) actions() []lsp.CodeAction {
	diags := rh.h.waitDiagnostics(rh.uri)
	var actions []lsp.CodeAction
	rh.h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Range:        lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 40}},
		Context:      lsp.CodeActionContext{Diagnostics: diags},
	}, &actions)
	return actions
}

// Quick fixes for `fn`/`cl` mistakes and missing `@derive` interfaces produce code that checks.
func TestLSPFnClAndDeriveQuickFixes(t *testing.T) {
	cases := []struct {
		name, src, title, want string
	}{
		{"fn_captures", "bind main = fn(): {\n\tlet k = 2\n\tlet f = fn(x: int) int: x * k\n\tprintln(f(3))\n}\n",
			"Make it a closure (`cl`)", "let f = cl(x: int) int: x * k"},
		{"cl_captures_nothing", "bind main = fn(): {\n\tlet f = cl(x: int) int: x * 2\n\tprintln(f(3))\n}\n",
			"Make it a function (`fn`)", "let f = fn(x: int) int: x * 2"},
		{"top_level_cl", "bind twice = cl(x: int) int: x * 2\nbind main = fn(): println(twice(3))\n",
			"Change `cl` to `fn`", "bind twice = fn(x: int) int: x * 2"},
		{"legacy_syntax", "bind twice = (x: int) int: x * 2\nbind main = fn(): println(twice(3))\n",
			"Insert `fn`", "bind twice = fn(x: int) int: x * 2"},
		{"derive_eq", "define P struct [x: int]\nbind main = fn(): println(P[x: 1] == P[x: 1])\n",
			"Add `@derive(Equatable)` to `P`", "@derive(Equatable)\ndefine P struct [x: int]"},
		{"derive_extend", "@derive(Equatable)\ndefine P struct [x: int]\nbind main = fn(): println(P[x: 1])\n",
			"Add `Printable` to `P`'s `@derive`", "@derive(Equatable, Printable)\ndefine P struct [x: int]"},
		{"derive_ordering", "define P struct [x: int]\nbind main = fn(): println(P[x: 1] < P[x: 2])\n",
			"Add `@derive(Comparable)` to `P`", "@derive(Comparable)\ndefine P struct [x: int]"},
		{"derive_conflict", "@derive(Equatable, Printable)\ndefine P struct [x: int]\nbind P.eq = fn(self, other: P) bool: true\nbind main = fn(): println(P[x: 1] == P[x: 1])\n",
			"Remove `Equatable` from `@derive` (keep the hand-written method)", "@derive(Printable)\ndefine P struct [x: int]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rh := newResultHarness(t, tc.src)
			var titles []string
			for _, a := range rh.actions() {
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
				for _, d := range rh.h.waitDiagnostics(rh.uri) {
					if d.Severity == 1 {
						t.Fatalf("after the fix:\n%s\nerrors remain: %+v", out, d)
					}
				}
				return
			}
			t.Fatalf("no %q action; actions %v", tc.title, titles)
		})
	}
}

// Attributes complete after `@`, interface names complete inside `@derive(`, both have hover
// docs, and a derived method's definition is its `@derive` attribute.
func TestLSPAttributesAndDerive(t *testing.T) {
	src := `@derive(Equatable, Printable)
define Point struct [
	x: int,
]

@derive(Eq)
define Other struct [
	y: int,
]

@tr
bind main = fn(): {
	let p = Point[x: 1]
	println(p.to_string())
	println(p == p)
}
`
	rh := newResultHarness(t, src)
	_ = rh.h.waitDiagnostics(rh.uri)

	if missing := containsAll(rh.complete(rh.at("@tr", 0, 3)), "track_caller"); len(missing) > 0 {
		t.Errorf("attribute completion after `@tr` is missing %v", missing)
	}
	if missing := containsAll(rh.complete(rh.at("@derive(Eq)", 0, len("@derive(Eq"))), "Equatable"); len(missing) > 0 {
		t.Errorf("@derive completion missing %v", missing)
	}
	if h := rh.hover(rh.at("derive(Equatable", 0, 1)); !strings.Contains(h, "@derive") || !strings.Contains(h, "Comparable") {
		t.Errorf("hover on @derive = %q", h)
	}
	if h := rh.hover(rh.at("Printable)", 0, 1)); !strings.Contains(h, "to_string") {
		t.Errorf("hover on a derived interface name = %q", h)
	}
	if got := rh.definitionLine(rh.at("to_string()", 0, 1)); got != 0 {
		t.Errorf("definition of derived `to_string` = line %d, want 0 (the @derive line)", got)
	}
}

// "Derive I for T" is offered on a struct for interfaces it neither derives nor implements, and
// "Implement I" isn't offered for one it already derives.
func TestLSPDeriveCodeActions(t *testing.T) {
	src := "@derive(Equatable)\ndefine Point struct [\n\tx: int,\n]\n\nbind main = fn(): println(Point[x: 1] == Point[x: 1])\n"
	rh := newResultHarness(t, src)
	_ = rh.h.waitDiagnostics(rh.uri)
	pos := rh.at("Point struct", 0, 0)
	var actions []lsp.CodeAction
	rh.h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Range:        lsp.Range{Start: pos, End: pos},
	}, &actions)
	byTitle := map[string]lsp.CodeAction{}
	for _, a := range actions {
		byTitle[a.Title] = a
	}
	if _, ok := byTitle["Implement Equatable for Point"]; ok {
		t.Errorf("offered to implement Equatable, which Point derives")
	}
	if _, ok := byTitle["Derive Equatable for Point"]; ok {
		t.Errorf("offered to derive Equatable again")
	}
	a, ok := byTitle["Derive Hashable for Point"]
	if !ok {
		t.Fatalf("missing `Derive Hashable for Point`; got %v", byTitle)
	}
	if out := applyTextEdits(src, a.Edit.Changes[rh.uri]); !strings.Contains(out, "@derive(Equatable, Hashable)") {
		t.Errorf("derive action produced:\n%s", out)
	}
}
