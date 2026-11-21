package lsp_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"lowlight/internal/format"
	"lowlight/internal/lsp"
)

// applyTextEdits applies LSP edits (non-overlapping) to src.
func applyTextEdits(src string, edits []lsp.TextEdit) string {
	type span struct {
		start, end int
		text       string
	}
	var spans []span
	for _, e := range edits {
		spans = append(spans, span{lsp.LSPToOffset(src, e.Range.Start), lsp.LSPToOffset(src, e.Range.End), e.NewText})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	out := src
	for _, sp := range spans {
		out = out[:sp.start] + sp.text + out[sp.end:]
	}
	return out
}

// codegenFixtures are well-formatted sources that trigger the LSP's code-generating actions.
var codegenFixtures = map[string]string{
	"arms.ll": `define Status enum [
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

bind label = (s: Status) string:
	match(s):
		when Status.Active: "a"
		default Status.Paused: "p"

bind find = (k: string) Lookup: Lookup.Forbidden

bind main = (): {
	let r = find("a")
	let v = find("b"):
		when Missing(m): m
		default Forbidden: "x"
	println(v)
}
`,
	"shapes.ll": `define Rect struct [
	w: float,
	h: float,
]

define Shape interface [
	area: (self) float,
	scale: (&self, by: float),
]

define Status enum [
	Active,
	Paused,
	Stopped,
]

define Lookup result [
	value: string,
	Missing: string,
	Forbidden,
]

@must_use
bind make_code = () int: 42

let bad_global = 10

bind describe = (s: Status) string:
	match(s):
		when Status.Active: "active"
		default: "other"

bind find = (k: string) Lookup:
	if(k == "a"): Lookup.value("a") else: Lookup.Forbidden

bind use_all = (flag: bool, items: [int]) int: {
	let r = Rect[]
	let p = null_ptr[u8]()
	make_code()
	let sum = 1 + 2 * 3
	let total = items.len() + sum
	let looked = find("a"):
		when Missing(m): m
		default panic
	if(flag): {
		println("yes")
		10
	} else: 20
}
`,
}

// Every code action the LSP offers on formatted code must leave a file that the formatter would
// not change, so generated code already looks like formatted Lowlight. (Wrapping a body in a
// block is exempt: it deliberately opens a block for the user to type into.)
func TestCodeActionsProduceFormattedCode(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)
	os.Setenv("LOWLIGHT_ROOT", root)
	t.Cleanup(func() { os.Unsetenv("LOWLIGHT_ROOT") })
	wsDir := t.TempDir()
	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(wsDir)}, &initRes)
	h.notify("initialized", map[string]any{})

	for name, src := range codegenFixtures {
		if got, err := format.Format(name, src, format.DefaultOptions()); err != nil || got != src {
			t.Fatalf("fixture %s is not formatted (err=%v):\n%s", name, err, got)
		}
		path := filepath.Join(wsDir, name)
		uri := lsp.PathToURI(path)
		h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
			TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "lowlight", Version: 1, Text: src},
		})
		diags := h.waitDiagnostics(uri)

		seen := map[string]bool{}
		lines := strings.Split(src, "\n")
		off := 0
		for _, line := range lines {
			// Probe every word start on the line, plus the whole line as a selection.
			var probes [][2]int
			for i := 0; i < len(line); i++ {
				if (i == 0 || !isWordByte(line[i-1])) && isWordByte(line[i]) {
					probes = append(probes, [2]int{off + i, off + i})
				}
			}
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				lead := len(line) - len(strings.TrimLeft(line, "\t "))
				probes = append(probes, [2]int{off + lead, off + len(line)})
			}
			for _, pr := range probes {
				var actions []lsp.CodeAction
				h.request("textDocument/codeAction", lsp.CodeActionParams{
					TextDocument: lsp.TextDocumentIdentifier{URI: uri},
					Range:        lsp.Range{Start: lsp.OffsetToLSP(src, pr[0]), End: lsp.OffsetToLSP(src, pr[1])},
					Context:      lsp.CodeActionContext{Diagnostics: diags},
				}, &actions)
				for _, a := range actions {
					if a.Edit == nil || len(a.Edit.Changes[uri]) == 0 {
						continue
					}
					out := applyTextEdits(src, a.Edit.Changes[uri])
					if seen[a.Title+"\x00"+out] {
						continue
					}
					seen[a.Title+"\x00"+out] = true
					formatted, err := format.Format(name, out, format.DefaultOptions())
					if a.Title == "Wrap body in '{ ... }' block" && err == nil {
						continue // opens a block to type into; the formatter collapses it if left as-is
					}
					if err != nil {
						t.Errorf("%s: action %q produced unparsable code: %v\n%s", name, a.Title, err, out)
						continue
					}
					if formatted != out {
						t.Errorf("%s: action %q produced unformatted code:\n--- got ---\n%s\n--- formatted ---\n%s", name, a.Title, out, formatted)
					}
				}
			}
			off += len(line) + 1
		}
	}
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
