package lsp

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/sema"
)

// Quick fixes for `fn`/`cl` function literals and for missing `@derive` interfaces.

// replaceKeywordAt returns a quick fix that replaces the two-letter keyword `from` at the
// diagnostic's start with `to`.
func replaceKeywordAt(uri, src string, d Diagnostic, from, to, title string, preferred bool) (CodeAction, bool) {
	off := LSPToOffset(src, d.Range.Start)
	if off+len(from) > len(src) || src[off:off+len(from)] != from {
		return CodeAction{}, false
	}
	return CodeAction{
		Title:       title,
		Kind:        "quickfix",
		Diagnostics: []Diagnostic{d},
		IsPreferred: preferred,
		Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{
			Range:   Range{Start: d.Range.Start, End: OffsetToLSP(src, off+len(from))},
			NewText: to,
		}}}},
	}, true
}

func (s *Server) collectFnClQuickFixes(uri, src string, d Diagnostic, addAction func(CodeAction)) {
	msg := d.Message
	switch {
	case strings.Contains(msg, "an `fn` can't capture variables"):
		if act, ok := replaceKeywordAt(uri, src, d, "fn", "cl", "Make it a closure (`cl`)", true); ok {
			addAction(act)
		}
	case strings.Contains(msg, "captures nothing, so it could be an `fn(...)`"):
		if act, ok := replaceKeywordAt(uri, src, d, "cl", "fn", "Make it a function (`fn`)", true); ok {
			addAction(act)
		}
	case strings.Contains(msg, "a top-level function is written `fn(...)`"),
		strings.Contains(msg, "is written `fn(...)`: it can't capture variables"):
		if act, ok := replaceKeywordAt(uri, src, d, "cl", "fn", "Change `cl` to `fn`", true); ok {
			addAction(act)
		}
	case strings.Contains(msg, "starts with `fn` or `cl`"):
		at := Range{Start: d.Range.Start, End: d.Range.Start}
		for i, kw := range []string{"fn", "cl"} {
			addAction(CodeAction{
				Title:       fmt.Sprintf("Insert `%s`", kw),
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: i == 0,
				Edit:        &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{Range: at, NewText: kw}}}},
			})
		}
	}
}

// Diagnostics that mean "type X lacks interface I", with the type in the first group.
var missingIfacePatterns = []struct {
	re    *regexp.Regexp
	iface string // "" means the interface is the second group
}{
	{regexp.MustCompile("equality `(?:==|!=)` is not defined on type (\\S+)"), "Equatable"},
	{regexp.MustCompile("ordering operator `[<>=]+` is not defined on (\\S+)"), "Comparable"},
	{regexp.MustCompile("cannot match value pattern of type `([^`]+)` because it does not support `==`"), "Equatable"},
	{regexp.MustCompile("element type `([^`]+)` does not support equality"), "Equatable"},
	{regexp.MustCompile("(?:interpolated value of )?type (\\S+) does not implement `Printable`"), "Printable"},
	{regexp.MustCompile("type (\\S+) does not implement `Copyable`"), "Copyable"},
	{regexp.MustCompile("map value type (\\S+) does not support `\\.copy\\(\\)`"), "Copyable"},
	{regexp.MustCompile("(?:set element|map key) type (\\S+) is not hashable"), "Hashable"},
	{regexp.MustCompile("requires (\\S+) to implement `(Copyable|Printable)`"), ""},
	{regexp.MustCompile("type (\\S+) does not satisfy interface (Equatable|Comparable|Hashable|Copyable|Printable)\\b"), ""},
}

var deriveConflictRe = regexp.MustCompile("^(\\w+) derives (\\w+) but also defines `\\w+` by hand")

// findTypeDecl finds the struct or enum declaration named name in a non-std module.
func findTypeDecl(prog *sema.CheckedProgram, name string) (*sema.Module, ast.Decl, []ast.Attribute) {
	if prog == nil {
		return nil, nil, nil
	}
	for _, m := range prog.Modules {
		if m.File == nil || (m.Pkg != nil && m.Pkg.IsStd) {
			continue
		}
		for _, d := range m.File.Decls {
			switch td := d.(type) {
			case *ast.StructDecl:
				if td.Name == name {
					return m, td, td.Attributes
				}
			case *ast.EnumDecl:
				if td.Name == name {
					return m, td, td.Attributes
				}
			}
		}
	}
	return nil, nil, nil
}

// moduleSource returns the text of m's file: the open buffer for the current file, else disk.
func moduleSource(m *sema.Module, filePath, src string) (string, bool) {
	if m.Path == filePath {
		return src, true
	}
	b, err := os.ReadFile(m.Path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (s *Server) collectDeriveQuickFixes(filePath, src string, prog *sema.CheckedProgram, d Diagnostic, addAction func(CodeAction)) {
	msg := d.Message
	if m := deriveConflictRe.FindStringSubmatch(msg); m != nil {
		s.addRemoveDeriveFix(filePath, src, prog, d, m[1], m[2], addAction)
		return
	}
	for _, p := range missingIfacePatterns {
		m := p.re.FindStringSubmatch(msg)
		if m == nil {
			continue
		}
		iface := p.iface
		if iface == "" {
			iface = m[2]
		}
		name := strings.Trim(m[1], "`.,;:()")
		if i := strings.IndexByte(name, '['); i >= 0 {
			name = name[:i]
		}
		if name == "" || name[0] < 'A' || name[0] > 'Z' {
			return // primitives, lists, sets, and tuples can't take `@derive`
		}
		s.addDeriveFix(filePath, src, prog, d, name, iface, addAction)
		return
	}
}

// deriveInsertEdit adds iface to the `@derive` of decl (in text), or adds a `@derive(iface)`
// line above the declaration. It reports false if decl already derives iface.
func deriveInsertEdit(text string, decl ast.Decl, attrs []ast.Attribute, iface string) (TextEdit, bool) {
	if a := findDeriveAttr(attrs); a != nil {
		for _, arg := range a.Args {
			if arg == iface {
				return TextEdit{}, false
			}
		}
		closeOff := a.Span.End.Offset - 1
		if closeOff < 0 || closeOff >= len(text) || text[closeOff] != ')' {
			return TextEdit{}, false
		}
		pos := OffsetToLSP(text, closeOff)
		sep := ", "
		if len(a.Args) == 0 {
			sep = ""
		}
		return TextEdit{Range: Range{Start: pos, End: pos}, NewText: sep + iface}, true
	}
	startOff := decl.GetSpan().Start.Offset
	for _, a := range attrs {
		if a.Span.Start.Offset < startOff {
			startOff = a.Span.Start.Offset
		}
	}
	lineStart := strings.LastIndex(text[:startOff], "\n") + 1
	indent := text[lineStart:startOff]
	if strings.TrimSpace(indent) != "" {
		indent = ""
		lineStart = startOff
	}
	pos := OffsetToLSP(text, lineStart)
	return TextEdit{Range: Range{Start: pos, End: pos}, NewText: indent + "@derive(" + iface + ")\n"}, true
}

// derivesInterface reports whether attrs contain `@derive(...)` listing iface.
func derivesInterface(attrs []ast.Attribute, iface string) bool {
	if a := findDeriveAttr(attrs); a != nil {
		for _, arg := range a.Args {
			if arg == iface {
				return true
			}
		}
	}
	return false
}

func (s *Server) addDeriveFix(filePath, src string, prog *sema.CheckedProgram, d Diagnostic, name, iface string, addAction func(CodeAction)) {
	m, decl, attrs := findTypeDecl(prog, name)
	if decl == nil {
		return
	}
	text, ok := moduleSource(m, filePath, src)
	if !ok {
		return
	}
	edit, ok := deriveInsertEdit(text, decl, attrs, iface)
	if !ok {
		return
	}
	title := fmt.Sprintf("Add `@derive(%s)` to `%s`", iface, name)
	if findDeriveAttr(attrs) != nil {
		title = fmt.Sprintf("Add `%s` to `%s`'s `@derive`", iface, name)
	}
	addAction(CodeAction{
		Title:       title,
		Kind:        "quickfix",
		Diagnostics: []Diagnostic{d},
		IsPreferred: true,
		Edit:        &WorkspaceEdit{Changes: map[string][]TextEdit{PathToURI(m.Path): {edit}}},
	})
}

// deriveActions offers "Derive I for T" for each derivable interface that T neither derives nor
// implements by hand.
func deriveActions(uri, src string, mod *sema.Module, decl ast.Decl, name string, attrs []ast.Attribute) []CodeAction {
	var out []CodeAction
	for _, di := range DerivableInterfaces {
		if derivesInterface(attrs, di.Name) || hasTypeMethodDecl(mod.File, name, di.Method) {
			continue
		}
		edit, ok := deriveInsertEdit(src, decl, attrs, di.Name)
		if !ok {
			continue
		}
		out = append(out, CodeAction{
			Title: fmt.Sprintf("Derive %s for %s", di.Name, name),
			Kind:  "refactor.rewrite",
			Edit:  &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {edit}}},
		})
	}
	return out
}

func (s *Server) addRemoveDeriveFix(filePath, src string, prog *sema.CheckedProgram, d Diagnostic, name, iface string, addAction func(CodeAction)) {
	m, _, attrs := findTypeDecl(prog, name)
	a := findDeriveAttr(attrs)
	if a == nil {
		return
	}
	text, ok := moduleSource(m, filePath, src)
	if !ok {
		return
	}
	var keep []string
	for _, arg := range a.Args {
		if arg != iface {
			keep = append(keep, arg)
		}
	}
	start, end := a.Span.Start.Offset, a.Span.End.Offset
	newText := "@derive(" + strings.Join(keep, ", ") + ")"
	if len(keep) == 0 {
		// Remove the whole attribute line.
		newText = ""
		lineStart := strings.LastIndex(text[:start], "\n") + 1
		if strings.TrimSpace(text[lineStart:start]) == "" && end < len(text) && text[end] == '\n' {
			start, end = lineStart, end+1
		}
	}
	addAction(CodeAction{
		Title:       fmt.Sprintf("Remove `%s` from `@derive` (keep the hand-written method)", iface),
		Kind:        "quickfix",
		Diagnostics: []Diagnostic{d},
		IsPreferred: true,
		Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{PathToURI(m.Path): {{
			Range:   Range{Start: OffsetToLSP(text, start), End: OffsetToLSP(text, end)},
			NewText: newText,
		}}}},
	})
}

func findDeriveAttr(attrs []ast.Attribute) *ast.Attribute {
	for i := range attrs {
		if attrs[i].Name == "derive" {
			return &attrs[i]
		}
	}
	return nil
}
