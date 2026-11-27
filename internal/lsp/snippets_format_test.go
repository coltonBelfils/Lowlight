package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"lowlight/internal/format"
	"lowlight/internal/lsp"
)

var (
	snippetDefault = regexp.MustCompile(`\$\{\d+:([^}]*)\}`)
	snippetBare    = regexp.MustCompile(`\$\d+`)
)

// expandSnippet substitutes placeholder defaults, so `${1:name}` becomes `name`. A bare tab stop on
// its own line becomes two statements (a block the user is meant to type into), and any other
// bare tab stop becomes `x`.
func expandSnippet(body string) string {
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(l); snippetBare.MatchString(t) && snippetBare.FindString(t) == t {
			ind := l[:len(l)-len(strings.TrimLeft(l, "\t "))]
			lines = append(lines, ind+"let x = 1", ind+"println(x)")
			continue
		}
		lines = append(lines, l)
	}
	out := snippetDefault.ReplaceAllString(strings.Join(lines, "\n"), "$1")
	out = strings.ReplaceAll(out, "$2.wrap", "e.wrap")
	return snippetBare.ReplaceAllString(out, "x")
}

// snippetIsFormatted reports whether the expanded snippet is exactly what the formatter prints,
// checking expression snippets inside a function body.
func snippetIsFormatted(t *testing.T, label, body string) {
	t.Helper()
	code := expandSnippet(body)
	topLevel := strings.HasPrefix(code, "bind ") || strings.HasPrefix(code, "const ") || strings.HasPrefix(code, "define ") || strings.HasPrefix(code, "extern ") || strings.HasPrefix(code, "@")
	src := code + "\n"
	if !topLevel {
		src = "bind main = fn(): {\n\tlet x = 1\n\t" + strings.ReplaceAll(code, "\n", "\n\t") + "\n}\n"
	}
	got, err := format.Format("snippet.ll", src, format.DefaultOptions())
	if err != nil {
		t.Errorf("snippet %q does not parse: %v\n%s", label, err, src)
		return
	}
	if got != src {
		t.Errorf("snippet %q is not formatted:\n--- snippet ---\n%s--- formatted ---\n%s", label, src, got)
	}
}

// Completion snippets from the LSP and the VS Code extension expand to formatted Lowlight.
func TestSnippetsAreFormatted(t *testing.T) {
	for _, sn := range lsp.BuiltinSnippets {
		snippetIsFormatted(t, "lsp:"+sn.Label, sn.Snippet)
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "editors", "vscode", "lowlight", "snippets", "lowlight.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vs map[string]struct {
		Body []string `json:"body"`
	}
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatal(err)
	}
	for name, sn := range vs {
		snippetIsFormatted(t, "vscode:"+name, strings.Join(sn.Body, "\n"))
	}
}
