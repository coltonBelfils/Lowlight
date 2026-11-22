package lsp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/lsp"
)

const resultFeaturesSrc = `// A cache lookup outcome.
define Lookup result [
	// The cached item.
	value: string,
	// No entry for the key.
	Missing: string,
	// The caller may not read the key.
	Forbidden,
]

bind find = fn(k: string) Lookup:
	if(k == "a"): value("found") else: Missing(k.copy())

bind load = fn(k: string) Result[string]: {
	let item = find(k):
		when Missing(why): func <- err(Error.new(why))
		default Forbidden: "denied"
	value(item)
}

bind main = fn(): {
	let r! = load("a")
	println(r! ?? "none")
}
`

type resultHarness struct {
	t   *testing.T
	h   *rpcHarness
	uri string
	src string
}

func newResultHarness(t *testing.T, src string) *resultHarness {
	t.Helper()
	h := newHarness(t)
	root := repoRoot(t)
	os.Setenv("LOWLIGHT_ROOT", root)
	t.Cleanup(func() { os.Unsetenv("LOWLIGHT_ROOT") })
	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(root)}, &initRes)
	h.notify("initialized", map[string]any{})
	uri := lsp.PathToURI(filepath.Join(t.TempDir(), "results.ll"))
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "lowlight", Version: 1, Text: src},
	})
	return &resultHarness{t: t, h: h, uri: uri, src: src}
}

// at returns the position of the n-th (0-based) occurrence of `needle`, offset by `delta` bytes.
func (rh *resultHarness) at(needle string, n, delta int) lsp.Position {
	rh.t.Helper()
	idx := -1
	from := 0
	for i := 0; i <= n; i++ {
		j := strings.Index(rh.src[from:], needle)
		if j < 0 {
			rh.t.Fatalf("needle %q #%d not found", needle, n)
		}
		idx = from + j
		from = idx + 1
	}
	return lsp.OffsetToLSP(rh.src, idx+delta)
}

func (rh *resultHarness) hover(pos lsp.Position) string {
	var hov lsp.Hover
	rh.h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri}, Position: pos,
	}, &hov)
	return hov.Contents.Value
}

func (rh *resultHarness) definitionLine(pos lsp.Position) int {
	var loc lsp.Location
	rh.h.request("textDocument/definition", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri}, Position: pos,
	}, &loc)
	if loc.URI == "" {
		return -1
	}
	return loc.Range.Start.Line
}

// Hover and go-to-definition resolve result declarations, bare chain patterns (`when Missing`),
// and bare member constructors (`value(...)`, `err(...)`).
func TestLSPResultHoverAndDefinition(t *testing.T) {
	rh := newResultHarness(t, resultFeaturesSrc)
	if diags := rh.h.waitDiagnostics(rh.uri); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	if md := rh.hover(rh.at("Lookup result", 0, 1)); !strings.Contains(md, "define Lookup result") || !strings.Contains(md, "A cache lookup outcome") {
		t.Errorf("hover on result declaration: %q", md)
	}
	if md := rh.hover(rh.at("Missing(why)", 0, 1)); !strings.Contains(md, "Missing") || !strings.Contains(md, "No entry for the key") {
		t.Errorf("hover on chain pattern `Missing`: %q", md)
	}
	if md := rh.hover(rh.at("Forbidden: \"denied\"", 0, 1)); !strings.Contains(md, "Forbidden") || !strings.Contains(md, "may not read") {
		t.Errorf("hover on chain pattern `Forbidden`: %q", md)
	}
	if md := rh.hover(rh.at("value(\"found\")", 0, 1)); !strings.Contains(md, "value") || !strings.Contains(md, "The cached item") {
		t.Errorf("hover on bare constructor `value`: %q", md)
	}
	if md := rh.hover(rh.at("err(Error", 0, 1)); !strings.Contains(md, "err") || !strings.Contains(md, "Result") {
		t.Errorf("hover on bare constructor `err`: %q", md)
	}
	if md := rh.hover(rh.at("r! ??", 0, 0)); !strings.Contains(md, "r!") || !strings.Contains(md, "Result[string]") {
		t.Errorf("hover on `r!`: %q", md)
	}

	// Lines are 0-based: `Missing: string,` is line 5 and `value: string,` line 3.
	if got := rh.definitionLine(rh.at("Missing(why)", 0, 1)); got != 5 {
		t.Errorf("definition of chain pattern `Missing` = line %d, want 5", got)
	}
	if got := rh.definitionLine(rh.at("value(\"found\")", 0, 1)); got != 3 {
		t.Errorf("definition of bare `value` = line %d, want 3", got)
	}
	if got := rh.definitionLine(rh.at("Missing(k.copy", 0, 1)); got != 5 {
		t.Errorf("definition of bare `Missing(...)` = line %d, want 5", got)
	}
}

func (rh *resultHarness) complete(pos lsp.Position) []string {
	var list lsp.CompletionList
	rh.h.request("textDocument/completion", lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri}, Position: pos,
	}, &list)
	var labels []string
	for _, it := range list.Items {
		labels = append(labels, it.Label)
	}
	return labels
}

func containsAll(have []string, want ...string) []string {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	var missing []string
	for _, w := range want {
		if !set[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// Completion offers a chain subject's failure names after `when`, a result's bare members where
// a result is expected, and the `result` declaration kind after `define Name`.
func TestLSPResultCompletion(t *testing.T) {
	src := `define Lookup result [
	value: string,
	Missing: string,
	Forbidden,
]

bind find = fn(k: string) Lookup:
	Lookup.Forbidden

bind use = fn(k: string) Lookup: {
	let item = find(k):
		when /*ARM*/
	/*RET*/
}

define Other /*KIND*/
`
	marks := map[string]int{}
	clean := src
	for _, m := range []string{"/*ARM*/", "/*RET*/", "/*KIND*/"} {
		i := strings.Index(clean, m)
		marks[m] = i
		clean = clean[:i] + clean[i+len(m):]
	}
	rh := newResultHarness(t, clean)
	_ = rh.h.waitDiagnostics(rh.uri)

	if miss := containsAll(rh.complete(lsp.OffsetToLSP(clean, marks["/*ARM*/"])), "Missing", "Forbidden"); len(miss) > 0 {
		t.Errorf("chain arm completion missing %v", miss)
	}
	if miss := containsAll(rh.complete(lsp.OffsetToLSP(clean, marks["/*RET*/"])), "value", "Missing", "Forbidden"); len(miss) > 0 {
		t.Errorf("result-expected completion missing %v", miss)
	}
	if miss := containsAll(rh.complete(lsp.OffsetToLSP(clean, marks["/*KIND*/"])), "result"); len(miss) > 0 {
		t.Errorf("declaration kind completion missing %v", miss)
	}
}

// A result variable named without `!` (or a non-result named with it) gets a rename quick fix.
func TestLSPResultBangQuickFix(t *testing.T) {
	src := `bind load = fn() Result[int]: Result.value(1)

bind main = fn(): {
	let r = load()
	let n! = 3
	println(r ?? n!)
}
`
	rh := newResultHarness(t, src)
	diags := rh.h.waitDiagnostics(rh.uri)
	var actions []lsp.CodeAction
	rh.h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
		Range:        lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 7}},
		Context:      lsp.CodeActionContext{Diagnostics: diags},
	}, &actions)
	var titles []string
	for _, a := range actions {
		titles = append(titles, a.Title)
	}
	if miss := containsAll(titles, "Rename 'r' to 'r!'", "Rename 'n!' to 'n' (remove '!')"); len(miss) > 0 {
		t.Errorf("missing quick fixes %v; have %v", miss, titles)
	}
	for _, a := range actions {
		if a.Title == "Rename 'r' to 'r!'" {
			out := applyTextEdits(src, a.Edit.Changes[rh.uri])
			if !strings.Contains(out, "let r! = load()") || !strings.Contains(out, "println(r! ?? n!)") {
				t.Errorf("rename did not update every use:\n%s", out)
			}
		}
	}
}

// Chain patterns and bare members are highlighted as enum members.
func TestLSPResultSemanticTokens(t *testing.T) {
	rh := newResultHarness(t, resultFeaturesSrc)
	_ = rh.h.waitDiagnostics(rh.uri)
	var toks lsp.SemanticTokens
	rh.h.request("textDocument/semanticTokens/full", lsp.SemanticTokensParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
	}, &toks)
	const enumMember = 9 // index of "enumMember" in the legend
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
	for _, c := range []struct {
		needle string
		n      int
	}{
		{"Missing(why)", 0}, {"Forbidden: \"denied\"", 0}, {"value(\"found\")", 0}, {"Missing(k.copy", 0}, {"err(Error", 0},
	} {
		if got := typeAt(rh.at(c.needle, c.n, 0)); got != enumMember {
			t.Errorf("token %q: type %d, want enumMember (%d)", c.needle, got, enumMember)
		}
	}
}

// "Generate missing match arms" demotes an exhaustive `default Pattern:` to `when`, puts the new
// last pattern on `default`, and uses bare failure names inside a result chain.
func TestLSPGenerateMissingArmsForMatchAndChain(t *testing.T) {
	src := `define Status enum [
	Active,
	Paused,
	Stopped,
]

define Lookup result [
	value: string,
	Missing: string,
	Forbidden,
	Expired: int,
]

bind label = fn(s: Status) string:
	match(s):
		when Status.Active: "a"
		default Status.Paused: "p"

bind find = fn(k: string) Lookup: Lookup.Forbidden

bind main = fn(): {
	let v = find("b"):
		when Missing(m): m.copy()
		default Forbidden: "x"
	println(v + label(Status.Active))
}
`
	rh := newResultHarness(t, src)
	diags := rh.h.waitDiagnostics(rh.uri)
	out := src
	for _, line := range []int{15, 22} { // the `match(s):` line and the chain line (0-based)
		var actions []lsp.CodeAction
		rh.h.request("textDocument/codeAction", lsp.CodeActionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: rh.uri},
			Range:        lsp.Range{Start: lsp.Position{Line: line}, End: lsp.Position{Line: line + 2}},
			Context:      lsp.CodeActionContext{Diagnostics: diags},
		}, &actions)
		found := false
		for _, a := range actions {
			if a.Title == "Generate missing match arms (exhaustive 'default Pattern')" {
				found = true
				out = applyTextEdits(out, a.Edit.Changes[rh.uri])
				break
			}
		}
		if !found {
			t.Fatalf("no exhaustive arm action near line %d; diags: %+v", line, diags)
		}
		// Re-sync so the second action is computed against the updated text.
		rh.h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
			TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: rh.uri, Version: 2 + line},
			ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: out}},
		})
		diags = rh.h.waitDiagnostics(rh.uri)
	}
	for _, want := range []string{
		"\t\twhen Status.Paused: \"p\"\n\t\tdefault Status.Stopped: panic(\"todo\")\n",
		"\t\twhen Forbidden: \"x\"\n\t\tdefault Expired(n): panic(\"todo\")\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if len(diags) != 0 {
		t.Errorf("generated arms left diagnostics: %+v\n%s", diags, out)
	}
}

// Hovers render doc sections as small headings, keep `[`Symbol`]` links in code style, hide
// another package's private fields, and show a parameter's own documentation.
func TestLSPDocumentationHovers(t *testing.T) {
	src := `// Adds two numbers.
//
// - ` + "`a`" + `: The first addend.
// - ` + "`b`" + `: The second addend.
//
// # Example
// ` + "```" + `
// let x = add(1, 2)
// ` + "```" + `
bind add = fn(a: int, b: int) int: a + b

bind main = fn(): {
	let e = Error.new("x")
	println(add(1, 2) + e.message().len())
}
`
	rh := newResultHarness(t, src)
	_ = rh.h.waitDiagnostics(rh.uri)
	md := rh.hover(rh.at("add(1, 2) +", 0, 1))
	for _, want := range []string{"#### Example", "```lowlight\nlet x = add(1, 2)"} {
		if !strings.Contains(md, want) {
			t.Errorf("function hover missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "\n# Example") {
		t.Errorf("doc heading should be demoted:\n%s", md)
	}
	if md := rh.hover(rh.at("a + b", 0, 0)); !strings.Contains(md, "(a: int)") || !strings.Contains(md, "The first addend.") {
		t.Errorf("parameter hover: %q", md)
	}
	md = rh.hover(rh.at("Error.new", 0, 1))
	if strings.Contains(md, "_message") || !strings.Contains(md, "private fields") {
		t.Errorf("struct hover should hide another package's private fields:\n%s", md)
	}
	if !strings.Contains(md, "[`ErrorFrame`](") {
		t.Errorf("doc links should keep code style:\n%s", md)
	}
}
