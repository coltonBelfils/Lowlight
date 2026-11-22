package lsp_test

import (
	"strings"
	"testing"

	"lowlight/internal/lsp"
)

// Each quick fix built from the compiler's diagnostic data produces the expected edit and leaves
// a file that checks cleanly.
func TestLSPDataDrivenQuickFixes(t *testing.T) {
	cases := []struct {
		name, src, title, want string
	}{
		{"convert", "bind half = (n: int) float: n / 2\nbind main = (): println(half(3))\n",
			"Convert to `float` with `float(...)`", "float(n / 2)"},
		{"to_string", "bind label = (n: int) string: n + 1\nbind main = (): println(label(3))\n",
			"Convert with `.to_string()`", "(n + 1).to_string()"},
		{"unwrap_optional", "bind find = () ?int: none\nbind main = (): {\n\tlet n: int = find()\n\tprintln(n)\n}\n",
			"Unwrap with `?? 0`", "find() ?? 0"},
		{"handle_result", "bind load = () Result[int]: Result.value(1)\nbind main = (): {\n\tlet n: int = load()\n\tprintln(n)\n}\n",
			"Unwrap with `default panic`", "load(): default panic"},
		{"wrap_value", "bind load = () Result[int]: 1\nbind main = (): println(load() ?? 0)\n",
			"Wrap in `value(...)`", "value(1)"},
		{"wrap_failure", "bind load = () Result[int]: Error.new(\"x\")\nbind main = (): println(load() ?? 0)\n",
			"Wrap in `err(...)`", "err(Error.new(\"x\"))"},
		{"rename_function", "bind main = (): {\n\tprintn(\"hi\")\n}\n",
			"Change to `println`", "\tprintln(\"hi\")"},
		{"rename_suffix", "bind find = () ?int: none\nbind main = (): {\n\tlet n? = find()\n\tprintln(n ?? 0)\n}\n",
			"Change to `n?`", "println(n? ?? 0)"},
		{"rename_method", "bind main = (): {\n\tlet s = \"abc\"\n\tprintln(s.lenght())\n}\n",
			"Change to `len`", "s.len()"},
		{"bind_to_let", "bind main = (): {\n\tbind x = 1\n\tx = 2\n\tprintln(x)\n}\n",
			"Declare `x` with `let`", "\tlet x = 1"},
		{"truthiness_int", "bind main = (): {\n\tlet n = 3\n\tif(n): println(n)\n}\n",
			"Compare with `!= 0`", "if(n != 0)"},
		{"truthiness_string", "bind main = (): {\n\tlet s = \"a\"\n\tif(s): println(s)\n}\n",
			"Test `not ….is_empty()`", "if(not s.is_empty())"},
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

// "Implement X for T" writes real bodies for Printable / Equatable / Copyable on simple structs,
// and a bare enum or result line expands into a `match` / result chain over every case.
func TestLSPGeneratedBodiesAndCaseExpansion(t *testing.T) {
	src := `define Point struct [
	x: int,
	name: string,
]

define Status enum [
	Active,
	Stopped,
]

define Lookup result [
	value: string,
	Missing: string,
	Forbidden,
]

bind find = (k: string) Lookup: Lookup.Forbidden

bind main = (): {
	let s = Status.Active
	s
	find("a")
}
`
	rh := newResultHarness(t, src)
	_ = rh.h.waitDiagnostics(rh.uri)
	actionsAt := func(needle string) map[string]lsp.CodeAction {
		pos := rh.at(needle, 0, 0)
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
	point := actionsAt("Point struct")
	for title, want := range map[string]string{
		"Implement Printable for Point": `bind Point.to_string = (self) string: "Point[x: ${self.x}, name: \"${self.name}\"]"`,
		"Implement Equatable for Point": "bind Point.eq = (self, other: Point) bool: self.x == other.x and self.name == other.name",
		"Implement Copyable for Point":  "bind Point.copy = (self) Point: Point[x: self.x, name: self.name.copy()]",
	} {
		a, ok := point[title]
		if !ok {
			t.Fatalf("missing %q", title)
		}
		if out := applyTextEdits(src, a.Edit.Changes[rh.uri]); !strings.Contains(out, want) {
			t.Errorf("%s produced:\n%s\nwant %q", title, out, want)
		}
	}
	status := actionsAt("Status enum")
	if a, ok := status["Implement Printable for Status"]; !ok {
		t.Errorf("missing Printable for Status")
	} else if out := applyTextEdits(src, a.Edit.Changes[rh.uri]); !strings.Contains(out, "bind Status.to_string = (self) string:\n\tmatch(self):\n\t\twhen Active: \"Active\"\n\t\tdefault Stopped: \"Stopped\"") {
		t.Errorf("enum to_string:\n%s", out)
	}
	if a, ok := actionsAt("\ts\n")["Expand into `match` on every variant of `Status`"]; !ok {
		t.Errorf("missing match expansion")
	} else if out := applyTextEdits(src, a.Edit.Changes[rh.uri]); !strings.Contains(out, "\tmatch(s):\n\t\twhen Active: panic(\"todo\")\n\t\tdefault Stopped: panic(\"todo\")\n") {
		t.Errorf("match expansion:\n%s", out)
	}
	if a, ok := actionsAt("find(\"a\")\n}")["Handle every failure of `Lookup`"]; !ok {
		t.Errorf("missing chain expansion")
	} else if out := applyTextEdits(src, a.Edit.Changes[rh.uri]); !strings.Contains(out, "\tfind(\"a\"):\n\t\twhen Missing(_): panic(\"todo\")\n\t\tdefault Forbidden: panic(\"todo\")\n") {
		t.Errorf("chain expansion:\n%s", out)
	}
}
