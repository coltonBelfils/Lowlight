package lsp

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/docgen"
	"lowlight/internal/lexer"
	"lowlight/internal/sema"
	"lowlight/internal/token"
)

var docCommentLinkRegex = regexp.MustCompile(`(?:\[([^\[\]]+)\])?\[` + "`" + `([^` + "`" + `\n\r]+)` + "`" + `\]`)

func (s *Server) getDocIndex(prog *sema.CheckedProgram, filePath string) *docgen.SymbolIndex {
	if prog == nil {
		return nil
	}
	s.mu.RLock()
	idx, ok := s.docIndices[prog]
	s.mu.RUnlock()
	if ok && idx != nil {
		return idx
	}

	overlays := s.snapshotOverlays()
	rootDir := s.projectRootFor(filePath)
	built := docgen.BuildSymbolIndex(rootDir, prog, nil, overlays)

	s.mu.Lock()
	// Prune orphaned cached indices when cachedProgs has shrunk/rotated.
	if len(s.docIndices) > 32 {
		live := make(map[*sema.CheckedProgram]bool, len(s.cachedProgs))
		for _, p := range s.cachedProgs {
			if p != nil {
				live[p] = true
			}
		}
		for k := range s.docIndices {
			if !live[k] {
				delete(s.docIndices, k)
			}
		}
	}
	s.docIndices[prog] = built
	s.mu.Unlock()
	return built
}

func (s *Server) renderDocMarkdown(
	prog *sema.CheckedProgram,
	currentModPath string,
	currentType string,
	doc string,
	declFile string,
	declLine int,
	includeViewSource bool,
) string {
	doc = strings.TrimSpace(doc)
	idx := s.getDocIndex(prog, currentModPath)

	var body string
	if doc != "" {
		body = polishDocMarkdown(resolveDocTextLinks(doc, idx, currentModPath, currentType))
	}

	var parts []string
	if body != "" {
		parts = append(parts, body)
	}
	if includeViewSource && declFile != "" && declLine > 0 {
		absFile := declFile
		if a, err := filepath.Abs(declFile); err == nil {
			absFile = a
		}
		parts = append(parts, fmt.Sprintf("[View source](%s#L%d)", PathToURI(absFile), declLine))
	}
	return strings.Join(parts, "\n\n")
}

// polishDocMarkdown adapts doc-comment Markdown for editor popups: section headings
// (`# Panics`, `# Example`) become small `####` headings instead of page-sized ones, and
// unlabeled code fences are highlighted as Lowlight.
func polishDocMarkdown(md string) string {
	lines := strings.Split(md, "\n")
	inFence := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			if !inFence && t == "```" {
				lines[i] = strings.Replace(l, "```", "```lowlight", 1)
			}
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if h := strings.TrimLeft(t, "#"); len(h) < len(t) && len(t)-len(h) <= 6 && strings.HasPrefix(h, " ") {
			lines[i] = "#### " + strings.TrimSpace(h)
		}
	}
	return strings.Join(lines, "\n")
}

func resolveDocTextLinks(doc string, idx *docgen.SymbolIndex, currentModPath, currentType string) string {
	links := docgen.ParseDocLinks(doc)
	if len(links) == 0 {
		return doc
	}
	var sb strings.Builder
	cursor := 0
	for _, lnk := range links {
		if lnk.Start < cursor || lnk.End > len(doc) {
			continue
		}
		sb.WriteString(doc[cursor:lnk.Start])
		replacement := "`" + lnk.Label + "`"
		if idx != nil {
			if entry, isBuiltin := idx.ResolveLink(lnk.Target, currentModPath, currentType); entry != nil && entry.FilePath != "" {
				line := entry.Line
				if line <= 0 {
					line = 1
				}
				absPath := entry.FilePath
				if a, err := filepath.Abs(absPath); err == nil {
					absPath = a
				}
				label := lnk.Label
				if strings.HasPrefix(doc[lnk.Start:], "[`") {
					label = "`" + label + "`" // keep the code style of [`Symbol`] links
				}
				replacement = fmt.Sprintf("[%s](%s#L%d)", label, PathToURI(absPath), line)
			} else if isBuiltin {
				replacement = "`" + lnk.Label + "`"
			}
		}
		sb.WriteString(replacement)
		cursor = lnk.End
	}
	sb.WriteString(doc[cursor:])
	return sb.String()
}

// parseParamDocs extracts `- `param`: description` entries from Iteration B doc comments.
func parseParamDocs(doc string) map[string]string {
	if doc == "" {
		return nil
	}
	res := make(map[string]string)
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	curParam := ""
	var curDesc []string

	flush := func() {
		if curParam != "" && len(curDesc) > 0 {
			res[curParam] = strings.TrimSpace(strings.Join(curDesc, " "))
		}
		curParam = ""
		curDesc = nil
	}

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "```") {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "- `") || strings.HasPrefix(trimmed, "* `") {
			flush()
			rest := trimmed[3:]
			if closeTick := strings.IndexByte(rest, '`'); closeTick > 0 {
				pName := strings.TrimSpace(rest[:closeTick])
				afterTick := strings.TrimSpace(rest[closeTick+1:])
				if strings.HasPrefix(afterTick, ":") {
					desc := strings.TrimSpace(afterTick[1:])
					curParam = pName
					if desc != "" {
						curDesc = append(curDesc, desc)
					}
					continue
				}
			}
		}
		if curParam != "" && (strings.HasPrefix(raw, "  ") || strings.HasPrefix(raw, "\t")) {
			curDesc = append(curDesc, trimmed)
			continue
		}
		if curParam != "" {
			flush()
		}
	}
	flush()
	if len(res) == 0 {
		return nil
	}
	return res
}

// findDocLinkAtOffset returns the target symbol and LSP range when `offset` is inside a `[`Symbol`]`
// or `[label][`Symbol`]` link inside a `//` or `/* ... */` comment.
func findDocLinkAtOffset(filePath, src string, offset int) (target string, rng Range, ok bool) {
	if offset < 0 || offset > len(src) {
		return "", Range{}, false
	}
	winStart := offset - 256
	if winStart < 0 {
		winStart = 0
	}
	winEnd := offset + 256
	if winEnd > len(src) {
		winEnd = len(src)
	}
	if !strings.Contains(src[winStart:winEnd], "[`") {
		return "", Range{}, false
	}
	rep := diag.NewReporter()
	_, comments := lexer.LexWithComments(filePath, src, rep)
	for _, c := range comments {
		if offset < c.Span.Start.Offset || offset > c.Span.End.Offset {
			continue
		}
		cStart := c.Span.Start.Offset
		cEnd := c.Span.End.Offset
		if cEnd > len(src) {
			cEnd = len(src)
		}
		if cStart >= cEnd {
			continue
		}
		raw := src[cStart:cEnd]
		relOff := offset - cStart
		matches := docCommentLinkRegex.FindAllStringSubmatchIndex(raw, -1)
		for _, m := range matches {
			mStart, mEnd := m[0], m[1]
			if relOff >= mStart && relOff <= mEnd {
				tgt := strings.TrimSpace(raw[m[4]:m[5]])
				if tgt == "" {
					continue
				}
				return tgt, Range{
					Start: OffsetToLSP(src, cStart+mStart),
					End:   OffsetToLSP(src, cStart+mEnd),
				}, true
			}
		}
	}
	return "", Range{}, false
}

// enclosingTypeAtOffset returns the nominal type name associated with a declaration or doc comment at `offset`.
func enclosingTypeAtOffset(f *ast.File, offset int) string {
	if f == nil {
		return ""
	}
	within := func(docSp, declSp diag.Span) bool {
		start := declSp.Start.Offset
		if docSp.Start.Line > 0 && docSp.Start.Offset < start {
			start = docSp.Start.Offset
		}
		end := declSp.End.Offset
		return offset >= start && offset <= end
	}
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.StructDecl:
			if within(decl.DocSpan, decl.Span) {
				return decl.Name
			}
		case *ast.EnumDecl:
			if within(decl.DocSpan, decl.Span) {
				return decl.Name
			}
		case *ast.InterfaceDecl:
			if within(decl.DocSpan, decl.Span) {
				return decl.Name
			}
		case *ast.DistinctTypeDecl:
			if within(decl.DocSpan, decl.Span) {
				return decl.Name
			}
		case *ast.MethodDecl:
			if within(decl.DocSpan, decl.Span) {
				return decl.RecvType
			}
		}
	}
	return ""
}

func (s *Server) hoverForDocEntry(prog *sema.CheckedProgram, entry *docgen.SymbolEntry, isBuiltin bool, target string, currentModPath string) string {
	if entry != nil {
		var blocks []string
		if entry.Signature != "" {
			blocks = append(blocks, fmt.Sprintf("```lowlight\n%s\n```", entry.Signature))
		} else {
			blocks = append(blocks, fmt.Sprintf("**%s** `%s`", entry.Kind, entry.FullName))
		}
		curType := ""
		if dot := strings.IndexByte(entry.FullName, '.'); dot > 0 {
			curType = entry.FullName[:dot]
		} else if entry.Kind == docgen.KindStruct || entry.Kind == docgen.KindEnum || entry.Kind == docgen.KindInterface {
			curType = entry.Name
		}
		if rendered := s.renderDocMarkdown(prog, entry.FilePath, curType, entry.Doc, entry.FilePath, entry.Line, true); rendered != "" {
			blocks = append(blocks, rendered)
		}
		return strings.Join(blocks, "\n\n")
	}
	if isBuiltin {
		if bf := LookupBuiltinFunc(target); bf != nil {
			return fmt.Sprintf("```lowlight\n%s\n```\n%s", bf.Signature, bf.Summary)
		}
		if bt := LookupBuiltinTypeDoc(target); bt != nil {
			return fmt.Sprintf("```lowlight\ntype %s // %s\n```\n%s", bt.Name, bt.Detail, bt.Summary)
		}
	}
	_ = currentModPath
	return ""
}

// findDeclIdentRange finds the exact identifier token span for `name` within a declaration span,
// ignoring identical names appearing inside initializer expressions or block bodies.
func findDeclIdentRange(filePath, src string, declSpan diag.Span, name string) (Range, bool) {
	if name == "" || declSpan.Start.Line <= 0 {
		return Range{}, false
	}
	sOff := declSpan.Start.Offset
	if sOff < 0 {
		sOff = 0
	}
	if sOff >= len(src) {
		return Range{}, false
	}
	eOff := declSpan.End.Offset
	if eOff <= sOff || eOff > len(src) {
		eOff = len(src)
	}
	if maxEnd := sOff + 512; eOff > maxEnd {
		eOff = maxEnd
	}
	toks := lexer.Lex(filePath, src[sOff:eOff], diag.NewReporter())
	for _, tk := range toks {
		if tk.Kind == token.EOF || tk.AutoSemi {
			continue
		}
		if tk.Kind == token.Assign || tk.Kind == token.LBrace {
			break
		}
		if (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == name {
			absStart := sOff + tk.Span.Start.Offset
			absEnd := sOff + tk.Span.End.Offset
			absLine := declSpan.Start.Line + tk.Span.Start.Line - 1
			lineCol := tk.Span.Start.Col
			if tk.Span.Start.Line == 1 {
				lineCol = declSpan.Start.Col + tk.Span.Start.Col - 1
			}
			startPos := PosToLSP(src, diag.Pos{
				Offset: absStart,
				Line:   absLine,
				Col:    lineCol,
			})
			endPos := Position{
				Line:      startPos.Line,
				Character: startPos.Character + utf16Len(src[absStart:absEnd]),
			}
			return Range{Start: startPos, End: endPos}, true
		}
	}
	return Range{}, false
}

func appendRenderedDoc(baseMD, renderedDoc string) string {
	if renderedDoc == "" {
		return baseMD
	}
	if baseMD == "" {
		return renderedDoc
	}
	return baseMD + "\n\n---\n\n" + renderedDoc
}
