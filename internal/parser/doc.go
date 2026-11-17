package parser

import (
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
)

func attachDocComments(file *ast.File, comments []lexer.Comment) {
	if file == nil || len(comments) == 0 {
		return
	}

	// Build line-indexed standalone comment groups and same-line trailing comments.
	trailingByLine := make(map[int]lexer.Comment)
	var standalone []lexer.Comment
	for _, c := range comments {
		if c.OnSameLineAsPrev {
			trailingByLine[c.Span.Start.Line] = c
		} else {
			standalone = append(standalone, c)
		}
	}

	// 1. Module-level doc at top of file (before first declaration).
	if len(file.Decls) > 0 {
		firstStart := declStartLineWithAttrs(file.Decls[0])
		var top []lexer.Comment
		for _, c := range standalone {
			if c.Span.End.Line < firstStart {
				top = append(top, c)
			} else {
				break
			}
		}
		if len(top) > 0 {
			// Split `top` into contiguous blocks separated by blank lines.
			blocks := splitCommentBlocks(top)
			firstBlock := blocks[0]
			lastBlock := blocks[len(blocks)-1]
			firstIsImportOrExtern := false
			switch file.Decls[0].(type) {
			case *ast.ImportDecl, *ast.ExternHeaderDecl:
				firstIsImportOrExtern = true
			}
			// If the first block is separated by a blank line from the first declaration,
			// OR the first declaration is an import/extern header, treat the first block as module doc.
			if len(blocks) > 1 || lastBlock[len(lastBlock)-1].Span.End.Line+1 < firstStart || firstIsImportOrExtern {
				if text, span, ok := normalizeDocBlock(firstBlock, true); ok {
					file.Doc = text
					file.DocSpan = span
				}
			}
		}
	} else if len(standalone) > 0 {
		blocks := splitCommentBlocks(standalone)
		if text, span, ok := normalizeDocBlock(blocks[0], true); ok {
			file.Doc = text
			file.DocSpan = span
		}
	}

	// 2. Attach docs to top-level declarations and their sub-items.
	for _, d := range file.Decls {
		// Do not attach module-header doc to an import or extern header.
		switch decl := d.(type) {
		case *ast.ImportDecl, *ast.ExternHeaderDecl:
			continue
		case *ast.StructDecl:
			decl.Doc, decl.DocSpan = findLeadingDoc(standalone, declStartLineWithAttrs(decl), file.DocSpan)
			for i := range decl.Fields {
				f := &decl.Fields[i]
				doc, sp := findLeadingDoc(standalone, f.Span.Start.Line, diag.Span{})
				if doc == "" {
					doc, sp = findTrailingDoc(trailingByLine, f.Span.End.Line)
				}
				f.Doc = doc
				f.DocSpan = sp
			}
		case *ast.EnumDecl:
			decl.Doc, decl.DocSpan = findLeadingDoc(standalone, declStartLineWithAttrs(decl), file.DocSpan)
			for i := range decl.Variants {
				v := &decl.Variants[i]
				doc, sp := findLeadingDoc(standalone, v.Span.Start.Line, diag.Span{})
				if doc == "" {
					doc, sp = findTrailingDoc(trailingByLine, v.Span.End.Line)
				}
				v.Doc = doc
				v.DocSpan = sp
			}
		case *ast.InterfaceDecl:
			decl.Doc, decl.DocSpan = findLeadingDoc(standalone, declStartLineWithAttrs(decl), file.DocSpan)
			for i := range decl.Methods {
				m := &decl.Methods[i]
				doc, sp := findLeadingDoc(standalone, m.Span.Start.Line, diag.Span{})
				if doc == "" {
					doc, sp = findTrailingDoc(trailingByLine, m.Span.End.Line)
				}
				m.Doc = doc
				m.DocSpan = sp
			}
		case *ast.DistinctTypeDecl:
			decl.Doc, decl.DocSpan = findLeadingDoc(standalone, declStartLineWithAttrs(decl), file.DocSpan)
		case *ast.MethodDecl:
			decl.Doc, decl.DocSpan = findLeadingDoc(standalone, declStartLineWithAttrs(decl), file.DocSpan)
		case *ast.VarDecl:
			decl.Doc, decl.DocSpan = findLeadingDoc(standalone, declStartLineWithAttrs(decl), file.DocSpan)
		case *ast.ExternFnDecl:
			decl.Doc, decl.DocSpan = findLeadingDoc(standalone, declStartLineWithAttrs(decl), file.DocSpan)
		}
	}
}

func declStartLineWithAttrs(d ast.Decl) int {
	line := d.GetSpan().Start.Line
	if ad, ok := d.(ast.AttributedDecl); ok {
		for _, a := range ad.GetAttributes() {
			if a.Span.Start.Line > 0 && a.Span.Start.Line < line {
				line = a.Span.Start.Line
			}
		}
	}
	return line
}

func splitCommentBlocks(comments []lexer.Comment) [][]lexer.Comment {
	if len(comments) == 0 {
		return nil
	}
	var blocks [][]lexer.Comment
	cur := []lexer.Comment{comments[0]}
	for i := 1; i < len(comments); i++ {
		prev := cur[len(cur)-1]
		if comments[i].Span.Start.Line == prev.Span.End.Line+1 && comments[i].IsBlock == prev.IsBlock {
			cur = append(cur, comments[i])
		} else {
			blocks = append(blocks, cur)
			cur = []lexer.Comment{comments[i]}
		}
	}
	blocks = append(blocks, cur)
	return blocks
}

func findLeadingDoc(standalone []lexer.Comment, targetLine int, moduleDocSpan diag.Span) (string, diag.Span) {
	if targetLine <= 1 || len(standalone) == 0 {
		return "", diag.Span{}
	}
	// Find the last standalone comment ending immediately on targetLine - 1.
	endIdx := -1
	for i := len(standalone) - 1; i >= 0; i-- {
		c := standalone[i]
		if c.Span.End.Line == targetLine-1 {
			endIdx = i
			break
		}
		if c.Span.End.Line < targetLine-1 {
			break
		}
	}
	if endIdx < 0 {
		return "", diag.Span{}
	}

	startIdx := endIdx
	for startIdx > 0 {
		prev := standalone[startIdx-1]
		cur := standalone[startIdx]
		if prev.Span.End.Line+1 == cur.Span.Start.Line && prev.IsBlock == cur.IsBlock {
			startIdx--
		} else {
			break
		}
	}

	group := standalone[startIdx : endIdx+1]
	fullSpan := diag.Span{
		Start: group[0].Span.Start,
		End:   group[len(group)-1].Span.End,
	}
	if moduleDocSpan.Start.Line > 0 && fullSpan.Start.Offset == moduleDocSpan.Start.Offset && fullSpan.End.Offset == moduleDocSpan.End.Offset {
		return "", diag.Span{}
	}

	text, span, ok := normalizeDocBlock(group, false)
	if !ok {
		return "", diag.Span{}
	}
	return text, span
}

func findTrailingDoc(trailingByLine map[int]lexer.Comment, line int) (string, diag.Span) {
	c, ok := trailingByLine[line]
	if !ok {
		return "", diag.Span{}
	}
	text, span, ok := normalizeDocBlock([]lexer.Comment{c}, false)
	if !ok {
		return "", diag.Span{}
	}
	return text, span
}

func isBannerDividerLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 {
		return false
	}
	for _, r := range trimmed {
		switch r {
		case '=', '-', '*', '_':
		default:
			return false
		}
	}
	return true
}

func normalizeDocBlock(group []lexer.Comment, allowBanner bool) (string, diag.Span, bool) {
	if len(group) == 0 {
		return "", diag.Span{}, false
	}
	span := diag.Span{
		Start: group[0].Span.Start,
		End:   group[len(group)-1].Span.End,
	}

	var rawLines []string
	if len(group) == 1 && group[0].IsBlock {
		body := strings.ReplaceAll(group[0].Text, "\r\n", "\n")
		lines := strings.Split(body, "\n")
		for i, ln := range lines {
			if i > 0 {
				trimmed := strings.TrimLeft(ln, " \t")
				if strings.HasPrefix(trimmed, "* ") {
					ln = trimmed[2:]
				} else if trimmed == "*" {
					ln = ""
				} else if strings.HasPrefix(trimmed, "*") {
					ln = strings.TrimPrefix(trimmed, "*")
				}
			} else {
				ln = strings.TrimPrefix(ln, " ")
			}
			rawLines = append(rawLines, strings.TrimRight(ln, " \t"))
		}
	} else {
		for _, c := range group {
			txt := strings.TrimPrefix(c.Text, " ")
			rawLines = append(rawLines, strings.TrimRight(txt, " \t"))
		}
	}

	// If a standalone declaration comment is framed as a section banner box (`// ---\n// Title\n// ---`),
	// ignore it as a declaration doc unless module-level banners are allowed.
	if !allowBanner && len(rawLines) >= 2 && isBannerDividerLine(rawLines[0]) && isBannerDividerLine(rawLines[len(rawLines)-1]) {
		return "", diag.Span{}, false
	}

	var cleaned []string
	for _, ln := range rawLines {
		if isBannerDividerLine(ln) {
			continue
		}
		cleaned = append(cleaned, ln)
	}

	for len(cleaned) > 0 && strings.TrimSpace(cleaned[0]) == "" {
		cleaned = cleaned[1:]
	}
	for len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	if len(cleaned) == 0 {
		return "", diag.Span{}, false
	}
	return strings.Join(cleaned, "\n"), span, true
}
