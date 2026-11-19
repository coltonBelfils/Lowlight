package format

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/token"
)

type printer struct {
	src              string
	lines            []string
	opts             Options
	comments         []lexer.Comment
	used             []bool
	buf              strings.Builder
	indent           int
	suppressComments bool
}

func newPrinter(src string, comments []lexer.Comment, opts Options) *printer {
	return &printer{
		src:      src,
		lines:    strings.Split(src, "\n"),
		opts:     opts,
		comments: comments,
		used:     make([]bool, len(comments)),
	}
}

func (p *printer) cloneTrial(indent int, allowComments bool) *printer {
	return &printer{
		src:              p.src,
		lines:            p.lines,
		opts:             p.opts,
		indent:           indent,
		suppressComments: !allowComments,
	}
}

func (p *printer) finish() string {
	s := p.buf.String()
	s = strings.TrimRight(s, " \t\n\r")
	if s == "" {
		return ""
	}
	return s + "\n"
}

func (p *printer) write(s string) {
	p.buf.WriteString(s)
}

func (p *printer) writeIndent() {
	p.buf.WriteString(p.opts.IndentString(p.indent))
}

func (p *printer) writeIndentLevel(level int) {
	p.buf.WriteString(p.opts.IndentString(level))
}

func (p *printer) newline() {
	p.buf.WriteByte('\n')
}

func (p *printer) ensureTrailingBlankLine() {
	s := p.buf.String()
	if s == "" || strings.HasSuffix(s, "\n\n") {
		return
	}
	if strings.HasSuffix(s, "\n") {
		p.buf.WriteByte('\n')
	} else {
		p.buf.WriteString("\n\n")
	}
}

func (p *printer) currentColVisualLen() int {
	s := p.buf.String()
	idx := strings.LastIndexByte(s, '\n')
	line := s
	if idx >= 0 {
		line = s[idx+1:]
	}
	return p.opts.VisualLen(line)
}

func (p *printer) fitsLineWidth(addition string) bool {
	if p.opts.LineWidth <= 0 {
		return !strings.ContainsRune(addition, '\n')
	}
	if strings.ContainsRune(addition, '\n') {
		return false
	}
	return p.currentColVisualLen()+p.opts.VisualLen(addition) <= p.opts.LineWidth
}

func (p *printer) trySingleLine(fn func(*printer)) (string, bool) {
	sub := p.cloneTrial(p.indent, false)
	fn(sub)
	out := sub.buf.String()
	return out, !strings.ContainsRune(out, '\n')
}

func (p *printer) tryFormatIndented(indent int, fn func(*printer)) string {
	sub := p.cloneTrial(indent, false)
	fn(sub)
	return sub.buf.String()
}

// -----------------------------------------------------------------------------
// Comments & Blank Line Helpers
// -----------------------------------------------------------------------------

func declStartPos(d ast.Decl) diag.Pos {
	sp := d.GetSpan().Start
	if ha, ok := d.(interface{ GetAttributes() []ast.Attribute }); ok {
		if attrs := ha.GetAttributes(); len(attrs) > 0 && attrs[0].Span.Start.Offset < sp.Offset {
			sp = attrs[0].Span.Start
		}
	}
	return sp
}

func declStartLineWithDoc(d ast.Decl) int {
	ln := declStartPos(d).Line
	var docStart int
	switch v := d.(type) {
	case *ast.ExternFnDecl:
		docStart = v.DocSpan.Start.Line
	case *ast.StructDecl:
		docStart = v.DocSpan.Start.Line
	case *ast.EnumDecl:
		docStart = v.DocSpan.Start.Line
	case *ast.InterfaceDecl:
		docStart = v.DocSpan.Start.Line
	case *ast.DistinctTypeDecl:
		docStart = v.DocSpan.Start.Line
	case *ast.MethodDecl:
		docStart = v.DocSpan.Start.Line
	case *ast.VarDecl:
		docStart = v.DocSpan.Start.Line
	}
	if docStart > 0 && docStart < ln {
		return docStart
	}
	return ln
}

func (p *printer) hasCommentsInRange(startOffset, endOffset int) bool {
	if p.suppressComments {
		return false
	}
	for i, c := range p.comments {
		if p.used[i] {
			continue
		}
		if c.Span.Start.Offset >= startOffset && c.Span.End.Offset <= endOffset {
			return true
		}
	}
	return false
}

func (p *printer) hasCommentsStrictlyInsideBlock(blk *ast.BlockExpr) bool {
	if p.suppressComments || blk == nil {
		return false
	}
	for i, c := range p.comments {
		if p.used[i] {
			continue
		}
		if c.Span.Start.Offset > blk.Span.Start.Offset && c.Span.End.Offset < blk.Span.End.Offset {
			return true
		}
		if c.Span.Start.Line == blk.Span.End.Line && c.Span.Start.Offset > blk.Span.Start.Offset {
			return true
		}
	}
	return false
}

func (p *printer) emitCommentText(c lexer.Comment, indent int) {
	if !c.IsBlock {
		p.write(strings.TrimRight(c.Raw, " \t\r"))
		return
	}
	if !strings.ContainsRune(c.Raw, '\n') {
		p.write(strings.TrimRight(c.Raw, " \t\r"))
		return
	}
	rawLines := strings.Split(strings.TrimRight(c.Raw, " \t\r"), "\n")
	indentStr := p.opts.IndentString(indent)
	for i, rl := range rawLines {
		trimmedRight := strings.TrimRight(rl, " \t\r")
		if i == 0 {
			p.write(trimmedRight)
			continue
		}
		p.newline()
		stripped := strings.TrimLeft(trimmedRight, " \t")
		if stripped == "" {
			continue
		}
		p.write(indentStr)
		if strings.HasPrefix(stripped, "*") {
			p.write(" ")
		}
		p.write(stripped)
	}
}

// emitCommentsBefore emits all unused comments ending at or before maxOffset.
// If hadPrevNode is true and there was a blank line before the first comment, or between comments,
// a single blank line is preserved. If returnTrailingBlank is true, reports whether a blank line
// separated the last emitted comment from the target node at targetStartLine.
func (p *printer) emitCommentsBefore(maxOffset int, targetStartLine int, hadPrevNode bool) (blankBeforeNode bool) {
	if p.suppressComments {
		return false
	}
	prevEndLine := 0
	emittedAny := false
	for i, c := range p.comments {
		if p.used[i] {
			continue
		}
		if c.Span.End.Offset > maxOffset {
			continue
		}
		p.used[i] = true
		if emittedAny {
			if c.Span.Start.Line > prevEndLine+1 {
				p.newline()
			}
		} else if hadPrevNode {
			p.newline()
		}
		p.writeIndent()
		p.emitCommentText(c, p.indent)
		p.newline()
		prevEndLine = c.Span.End.Line
		emittedAny = true
	}
	if emittedAny && targetStartLine > prevEndLine+1 {
		return true
	}
	return false
}

func (p *printer) emitInlineCommentOnLine(line int) {
	if p.suppressComments || line <= 0 {
		return
	}
	for i, c := range p.comments {
		if p.used[i] {
			continue
		}
		if c.Span.Start.Line == line && c.OnSameLineAsPrev {
			p.used[i] = true
			p.write(" ")
			p.emitCommentText(c, p.indent)
		}
	}
}

func (p *printer) hadBlankLineBetween(prevEndLine, currStartLine int) bool {
	if prevEndLine <= 0 || currStartLine <= 0 {
		return false
	}
	return currStartLine-prevEndLine > 1
}

// -----------------------------------------------------------------------------
// Top-level File, Imports & Declarations
// -----------------------------------------------------------------------------

type importItem struct {
	decl     *ast.ImportDecl
	leading  []int
	trailing int // -1 if none
	stdGroup bool
}

func (p *printer) printFile(f *ast.File) {
	if f == nil {
		return
	}
	if len(f.Decls) == 0 {
		_ = p.emitCommentsBefore(len(p.src)+1, 0, false)
		return
	}

	idx := 0
	lastEmittedLine := 0
	wroteSomething := false

	// Sort and group leading contiguous ImportDecls while preserving top-of-file module header comments.
	if _, isImp := f.Decls[0].(*ast.ImportDecl); isImp {
		importEndIdx := 0
		for importEndIdx < len(f.Decls) {
			if _, ok := f.Decls[importEndIdx].(*ast.ImportDecl); !ok {
				break
			}
			importEndIdx++
		}
		if importEndIdx > 0 {
			p.emitImportBlock(f.Decls[:importEndIdx], &wroteSomething, &lastEmittedLine)
			idx = importEndIdx
		}
	}

	var prevDecl ast.Decl
	for ; idx < len(f.Decls); idx++ {
		d := f.Decls[idx]
		startPos := declStartPos(d)
		startLine := declStartLineWithDoc(d)
		hadBlankBefore := p.hadBlankLineBetween(lastEmittedLine, startLine)

		// Decide separation from previous declaration before comments are emitted.
		separateMajor := false
		if wroteSomething {
			if imp, isImp := d.(*ast.ImportDecl); isImp {
				_ = imp
				if prevDecl == nil {
					separateMajor = true
				} else if _, prevImp := prevDecl.(*ast.ImportDecl); !prevImp {
					separateMajor = true
				} else if hadBlankBefore {
					separateMajor = true
				}
			} else if isCompactDecl(prevDecl) && isCompactDecl(d) && !hadBlankBefore && !p.hasCommentsBetweenOffsets(prevDecl.GetSpan().End.Offset, startPos.Offset) {
				// Keep compact 1-line `bind` constants / `extern` decls grouped unless separated by blank line or comment.
				separateMajor = false
			} else if isOneLineBind(prevDecl, p) && isOneLineBind(d, p) && !hadBlankBefore && !p.hasCommentsBetweenOffsets(prevDecl.GetSpan().End.Offset, startPos.Offset) {
				separateMajor = false
			} else if _, isPrevImp := prevDecl.(*ast.ImportDecl); isPrevImp || prevDecl == nil {
				separateMajor = true
			} else {
				separateMajor = true
			}
		}

		// Collect comments before this declaration.
		if p.hasCommentsBeforeOffset(startPos.Offset) {
			if wroteSomething {
				p.ensureTrailingBlankLine()
			}
			blankAfterComment := p.emitCommentsBefore(startPos.Offset, startPos.Line, false)
			if blankAfterComment {
				p.newline()
			}
		} else if separateMajor && wroteSomething {
			p.ensureTrailingBlankLine()
		}

		p.printDecl(d)
		p.emitInlineCommentOnLine(d.GetSpan().End.Line)
		p.newline()
		wroteSomething = true
		lastEmittedLine = d.GetSpan().End.Line
		prevDecl = d
	}

	// Trailing comments at EOF.
	if p.hasCommentsBeforeOffset(len(p.src) + 1) {
		if wroteSomething {
			p.ensureTrailingBlankLine()
		}
		_ = p.emitCommentsBefore(len(p.src)+1, 0, false)
	}
}

func (p *printer) hasCommentsBeforeOffset(maxOffset int) bool {
	if p.suppressComments {
		return false
	}
	for i, c := range p.comments {
		if !p.used[i] && c.Span.End.Offset <= maxOffset {
			return true
		}
	}
	return false
}

func (p *printer) hasCommentsBetweenOffsets(minOffset, maxOffset int) bool {
	if p.suppressComments {
		return false
	}
	for i, c := range p.comments {
		if !p.used[i] && c.Span.Start.Offset >= minOffset && c.Span.End.Offset <= maxOffset {
			return true
		}
	}
	return false
}

func (p *printer) emitImportBlock(decls []ast.Decl, wroteSomething *bool, lastEmittedLine *int) {
	items := make([]importItem, 0, len(decls))
	// Associate comments tightly attached above each import (no blank line) and same-line inline comments.
	for _, d := range decls {
		imp := d.(*ast.ImportDecl)
		impStartOff := declStartPos(imp).Offset
		impStartLine := imp.Span.Start.Line

		var leading []int
		// Walk backward among unused comments before impStartOff with no blank line gap.
		targetLine := impStartLine
		for i := len(p.comments) - 1; i >= 0; i-- {
			if p.used[i] {
				continue
			}
			c := p.comments[i]
			if c.Span.End.Offset > impStartOff {
				continue
			}
			if c.OnSameLineAsPrev {
				continue
			}
			if c.Span.End.Line == targetLine-1 {
				leading = append(leading, i)
				targetLine = c.Span.Start.Line
			} else if c.Span.End.Line < targetLine-1 {
				break
			}
		}
		// Reverse leading into source order and mark provisionally claimed so earlier imports don't steal them.
		for l, r := 0, len(leading)-1; l < r; l, r = l+1, r-1 {
			leading[l], leading[r] = leading[r], leading[l]
		}
		for _, ci := range leading {
			p.used[ci] = true
		}

		trailing := -1
		for i, c := range p.comments {
			if !p.used[i] && c.Span.Start.Line == imp.Span.End.Line && c.OnSameLineAsPrev {
				trailing = i
				p.used[i] = true
				break
			}
		}

		items = append(items, importItem{
			decl:     imp,
			leading:  leading,
			trailing: trailing,
			stdGroup: strings.HasPrefix(imp.Path, "std/") || imp.Path == "std",
		})
	}

	// If the very first comment block at the top of the file was directly above import[0] (no blank line before import[0]),
	// wait: module doc/banner blocks with no blank line between them and `import` (or separated by banner divider lines)
	// should remain pinned at the top of the file rather than moving when imports sort!
	// Specifically, if item[0] has leading comments starting at line 1 and containing a banner or multiple lines,
	// or simply any comment at top of file ending above import[0]: keep comments from line 1..N pinned ONLY if separated
	// or if there is a banner (`===` / `---`) inside them.
	if len(items) > 0 && len(items[0].leading) > 0 {
		hasBanner := false
		for _, ci := range items[0].leading {
			if isBannerComment(p.comments[ci].Text) {
				hasBanner = true
				break
			}
		}
		if p.comments[items[0].leading[0]].Span.Start.Line <= 2 || hasBanner {
			// Restore them as file-header comments instead of attaching to sorted import[0].
			for _, ci := range items[0].leading {
				p.used[ci] = false
			}
			items[0].leading = nil
		}
	}

	// Emit remaining unclaimed comments before the first import (file doc / banners).
	firstImpOff := declStartPos(decls[0]).Offset
	if p.hasCommentsBeforeOffset(firstImpOff) {
		blank := p.emitCommentsBefore(firstImpOff, declStartPos(decls[0]).Line, false)
		*wroteSomething = true
		if blank {
			p.newline()
		}
	}

	var stdItems, localItems []importItem
	for _, it := range items {
		if it.stdGroup {
			stdItems = append(stdItems, it)
		} else {
			localItems = append(localItems, it)
		}
	}
	sortImports := func(s []importItem) {
		sort.SliceStable(s, func(i, j int) bool {
			if s[i].decl.Path != s[j].decl.Path {
				return s[i].decl.Path < s[j].decl.Path
			}
			return s[i].decl.Alias < s[j].decl.Alias
		})
	}
	sortImports(stdItems)
	sortImports(localItems)

	emitGroup := func(group []importItem) {
		for _, it := range group {
			for _, ci := range it.leading {
				p.writeIndent()
				p.emitCommentText(p.comments[ci], p.indent)
				p.newline()
			}
			p.printImportDecl(it.decl)
			if it.trailing >= 0 {
				p.write(" ")
				p.emitCommentText(p.comments[it.trailing], p.indent)
			}
			p.newline()
			*wroteSomething = true
			if it.decl.Span.End.Line > *lastEmittedLine {
				*lastEmittedLine = it.decl.Span.End.Line
			}
		}
	}

	if len(stdItems) > 0 {
		emitGroup(stdItems)
	}
	if len(stdItems) > 0 && len(localItems) > 0 {
		p.newline()
	}
	if len(localItems) > 0 {
		emitGroup(localItems)
	}
}

func isBannerComment(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if len(t) >= 3 && (strings.Trim(t, "=") == "" || strings.Trim(t, "-") == "") {
			return true
		}
	}
	return false
}

func isCompactDecl(d ast.Decl) bool {
	switch d.(type) {
	case *ast.ExternHeaderDecl, *ast.ExternFnDecl, *ast.DistinctTypeDecl:
		return true
	}
	return false
}

func isOneLineBind(d ast.Decl, p *printer) bool {
	vd, ok := d.(*ast.VarDecl)
	if !ok {
		return false
	}
	if len(vd.Attributes) > 0 {
		return false
	}
	trial, okSingle := p.trySingleLine(func(sub *printer) {
		sub.printVarDecl(vd)
	})
	return okSingle && (p.opts.LineWidth <= 0 || p.opts.VisualLen(trial) <= p.opts.LineWidth)
}

func (p *printer) printAttributes(attrs []ast.Attribute) {
	for _, a := range attrs {
		p.writeIndent()
		if a.Span.Start.Offset >= 0 && a.Span.End.Offset <= len(p.src) && a.Span.Start.Offset < a.Span.End.Offset {
			raw := strings.TrimSpace(p.src[a.Span.Start.Offset:a.Span.End.Offset])
			if strings.HasPrefix(raw, "@"+a.Name) && !strings.ContainsRune(raw, '\n') {
				p.write(raw)
				p.newline()
				continue
			}
		}
		p.write("@")
		p.write(a.Name)
		if len(a.Args) > 0 {
			p.write("(")
			for i, arg := range a.Args {
				if i > 0 {
					p.write(", ")
				}
				if a.Name == "test" || a.Name == "deprecated" || a.Name == "lang" || (!isValidPlainIdent(arg) && !isPlainIntLit(arg)) {
					p.write(strconv.Quote(arg))
				} else {
					p.write(arg)
				}
			}
			p.write(")")
		}
		p.newline()
	}
}

func isPlainIntLit(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (p *printer) printDecl(d ast.Decl) {
	switch v := d.(type) {
	case *ast.ImportDecl:
		p.printAttributes(v.Attributes)
		p.printImportDecl(v)
	case *ast.ExternHeaderDecl:
		p.printAttributes(v.Attributes)
		p.writeIndent()
		p.write("extern ")
		p.write(strconv.Quote(v.Header))
	case *ast.ExternFnDecl:
		p.printAttributes(v.Attributes)
		p.printExternFnDecl(v)
	case *ast.StructDecl:
		p.printAttributes(v.Attributes)
		p.printStructDecl(v)
	case *ast.EnumDecl:
		p.printAttributes(v.Attributes)
		p.printEnumDecl(v)
	case *ast.InterfaceDecl:
		p.printAttributes(v.Attributes)
		p.printInterfaceDecl(v)
	case *ast.DistinctTypeDecl:
		p.printAttributes(v.Attributes)
		p.writeIndent()
		p.write("define ")
		p.write(v.Name)
		p.write(" ")
		p.printTypeExpr(v.Base)
	case *ast.MethodDecl:
		p.printAttributes(v.Attributes)
		p.printMethodDecl(v)
	case *ast.VarDecl:
		p.printAttributes(v.Attributes)
		p.writeIndent()
		p.printVarDecl(v)
	}
}

func (p *printer) printImportDecl(v *ast.ImportDecl) {
	p.writeIndent()
	p.write("import ")
	p.write(strconv.Quote(v.Path))
	if v.Alias != "" {
		p.write(" as ")
		p.write(v.Alias)
	}
}

func (p *printer) printExternFnDecl(v *ast.ExternFnDecl) {
	p.writeIndent()
	p.write("extern ")
	if isValidPlainIdent(v.Name) || token.LookupKeyword(v.Name) != token.Ident {
		p.write(v.Name)
	} else {
		p.write(strconv.Quote(v.Name))
	}
	if v.Alias != "" {
		p.write(" as ")
		p.write(v.Alias)
	}
	p.write(": ")
	p.printParamList(v.Params)
	if v.Ret != nil {
		p.write(" ")
		p.printTypeExpr(v.Ret)
	}
}

func isValidPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func (p *printer) printTypeParams(tps []ast.TypeParam) {
	if len(tps) == 0 {
		return
	}
	p.write("[")
	for i, tp := range tps {
		if i > 0 {
			p.write(", ")
		}
		p.write(tp.Name)
		if len(tp.Bounds) > 0 {
			p.write(": ")
			p.write(strings.Join(tp.Bounds, " + "))
		}
	}
	p.write("]")
}

func (p *printer) printStructDecl(v *ast.StructDecl) {
	p.writeIndent()
	p.write("define ")
	if v.Packed && ast.FindAttribute(v.Attributes, "packed") == nil {
		p.write("packed ")
	}
	p.write(v.Name)
	p.printTypeParams(v.TypeParams)
	p.write(" struct [")
	if len(v.Fields) == 0 && !p.hasCommentsInRange(v.Span.Start.Offset, v.Span.End.Offset) {
		p.write("]")
		return
	}
	p.newline()
	p.indent++
	for _, f := range v.Fields {
		if f.Span.Start.Offset > 0 {
			_ = p.emitCommentsBefore(f.Span.Start.Offset, f.Span.Start.Line, false)
		}
		p.writeIndent()
		p.write(f.Name)
		p.write(": ")
		p.printTypeExpr(f.Type)
		if f.Default != nil {
			p.write(" = ")
			p.printExpr(f.Default)
		}
		if len(v.Fields) > 1 {
			p.write(",")
		}
		p.emitInlineCommentOnLine(f.Span.End.Line)
		p.newline()
	}
	_ = p.emitCommentsBefore(v.Span.End.Offset, v.Span.End.Line, false)
	p.indent--
	p.writeIndent()
	p.write("]")
}

func (p *printer) printEnumDecl(v *ast.EnumDecl) {
	p.writeIndent()
	p.write("define ")
	p.write(v.Name)
	p.printTypeParams(v.TypeParams)
	p.write(" enum ")
	if v.BaseType != nil {
		p.printTypeExpr(v.BaseType)
		p.write(" ")
	}
	p.write("[")
	if len(v.Variants) == 0 && !p.hasCommentsInRange(v.Span.Start.Offset, v.Span.End.Offset) {
		p.write("]")
		return
	}
	p.newline()
	p.indent++
	for _, ev := range v.Variants {
		if ev.Span.Start.Offset > 0 {
			_ = p.emitCommentsBefore(ev.Span.Start.Offset, ev.Span.Start.Line, false)
		}
		p.writeIndent()
		p.write(ev.Name)
		if ev.PayloadType != nil {
			p.write(": ")
			p.printTypeExpr(ev.PayloadType)
		} else if ev.Value != nil {
			p.write(" = ")
			p.printExpr(ev.Value)
		}
		if len(v.Variants) > 1 {
			p.write(",")
		}
		p.emitInlineCommentOnLine(ev.Span.End.Line)
		p.newline()
	}
	_ = p.emitCommentsBefore(v.Span.End.Offset, v.Span.End.Line, false)
	p.indent--
	p.writeIndent()
	p.write("]")
}

func (p *printer) printInterfaceDecl(v *ast.InterfaceDecl) {
	p.writeIndent()
	p.write("define ")
	p.write(v.Name)
	p.printTypeParams(v.TypeParams)
	p.write(" interface [")
	if len(v.Methods) == 0 && !p.hasCommentsInRange(v.Span.Start.Offset, v.Span.End.Offset) {
		p.write("]")
		return
	}
	p.newline()
	p.indent++
	for _, m := range v.Methods {
		if m.Span.Start.Offset > 0 {
			_ = p.emitCommentsBefore(m.Span.Start.Offset, m.Span.Start.Line, false)
		}
		p.writeIndent()
		p.write(m.Name)
		p.write(": ")
		p.printMethodParamList(m.RecvMode, nil, m.Params)
		if m.Ret != nil {
			p.write(" ")
			p.printTypeExpr(m.Ret)
		}
		if len(v.Methods) > 1 {
			p.write(",")
		}
		p.emitInlineCommentOnLine(m.Span.End.Line)
		p.newline()
	}
	_ = p.emitCommentsBefore(v.Span.End.Offset, v.Span.End.Line, false)
	p.indent--
	p.writeIndent()
	p.write("]")
}

func (p *printer) printMethodParamList(mode ast.ReceiverMode, explicitTy ast.TypeExpr, params []ast.Param) {
	var parts []string
	if mode != ast.ReceiverMode(-1) {
		var recv strings.Builder
		if explicitTy != nil {
			recv.WriteString("self: ")
			switch mode {
			case ast.RecvMut:
				recv.WriteByte('&')
			case ast.RecvOwn:
				recv.WriteString("own ")
			}
			recv.WriteString(p.formatTypeStr(explicitTy))
		} else {
			switch mode {
			case ast.RecvMut:
				recv.WriteString("&self")
			case ast.RecvOwn:
				recv.WriteString("own self")
			default:
				recv.WriteString("self")
			}
		}
		parts = append(parts, recv.String())
	}
	for _, pr := range params {
		parts = append(parts, p.formatParamStr(pr))
	}
	p.write("(")
	p.writeCommaListParts(parts, false, p.indent)
	p.write(")")
}

func (p *printer) printMethodDecl(v *ast.MethodDecl) {
	p.writeIndent()
	p.write("bind ")
	if v.RecvModule != "" {
		p.write(v.RecvModule)
		p.write(".")
	}
	p.write(v.RecvType)
	if len(v.RecvTypeParams) > 0 {
		p.write("[")
		p.write(strings.Join(v.RecvTypeParams, ", "))
		p.write("]")
	}
	p.write(".")
	p.write(v.Name)
	p.printTypeParams(v.TypeParams)
	p.write(" = ")
	p.printMethodParamList(v.RecvMode, v.RecvExplicitTy, v.Params)
	if v.Ret != nil {
		p.write(" ")
		p.printTypeExpr(v.Ret)
	}
	p.write(":")
	p.printColonBody(v.Span.Start.Offset, v.Body, false)
}

func (p *printer) printVarDecl(v *ast.VarDecl) {
	if v.IsBind {
		p.write("bind ")
	} else {
		p.write("let ")
	}
	p.write(v.Name)
	p.printTypeParams(v.TypeParams)
	if v.Type != nil {
		p.write(": ")
		p.printTypeExpr(v.Type)
	}
	p.write(" = ")
	p.printExpr(v.Value)
}

// -----------------------------------------------------------------------------
// Parameters & Types
// -----------------------------------------------------------------------------

func (p *printer) formatParamStr(pr ast.Param) string {
	var sb strings.Builder
	if pr.Name != "" {
		sb.WriteString(pr.Name)
		sb.WriteString(": ")
	}
	sb.WriteString(p.formatTypeStr(pr.Type))
	return sb.String()
}

func (p *printer) printParamList(params []ast.Param) {
	parts := make([]string, len(params))
	for i, pr := range params {
		parts[i] = p.formatParamStr(pr)
	}
	p.write("(")
	p.writeCommaListParts(parts, false, p.indent)
	p.write(")")
}

func (p *printer) formatTypeStr(t ast.TypeExpr) string {
	sub := p.cloneTrial(p.indent, false)
	sub.printTypeExpr(t)
	return sub.buf.String()
}

func (p *printer) printTypeExpr(t ast.TypeExpr) {
	if t == nil {
		return
	}
	switch v := t.(type) {
	case *ast.NamedType:
		if v.Module != "" {
			p.write(v.Module)
			p.write(".")
		}
		p.write(v.Name)
		if len(v.TypeArgs) > 0 {
			p.write("[")
			for i, ta := range v.TypeArgs {
				if i > 0 {
					p.write(", ")
				}
				p.printTypeExpr(ta)
			}
			p.write("]")
		}
	case *ast.OptionalType:
		p.write("?")
		p.printTypeExpr(v.Elem)
	case *ast.MutBorrowType:
		p.write("&")
		p.printTypeExpr(v.Elem)
	case *ast.OwnType:
		p.write("own ")
		p.printTypeExpr(v.Elem)
	case *ast.VectorType:
		p.write("[")
		p.printTypeExpr(v.Elem)
		p.write("]")
	case *ast.StackArrayType:
		p.write("[")
		p.printExpr(v.Size)
		p.write(": ")
		p.printTypeExpr(v.Elem)
		p.write("]")
	case *ast.SetType:
		p.write("#[")
		p.printTypeExpr(v.Elem)
		p.write("]")
	case *ast.MapType:
		p.write("#[")
		p.printTypeExpr(v.Key)
		p.write(": ")
		p.printTypeExpr(v.Value)
		p.write("]")
	case *ast.TupleType:
		p.write("+[")
		for i, el := range v.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.printTypeExpr(el)
		}
		p.write("]")
	case *ast.FuncType:
		p.printParamList(v.Params)
		if v.Ret != nil {
			p.write(" ")
			p.printTypeExpr(v.Ret)
		}
	}
}

// -----------------------------------------------------------------------------
// Body Unwrapping & Next-Line Tabbed Layout
// -----------------------------------------------------------------------------

// unwrapBody simplifies a single-expression or single-statement BlockExpr when safe.
// allowStmtBody is true inside if/for/while/match arms (which parse with parseActionBody),
// and false on function/lambda/method bodies (which parse with parseExpr).
func (p *printer) unwrapBody(body ast.Expr, allowStmtBody bool) ast.Expr {
	for {
		blk, ok := body.(*ast.BlockExpr)
		if !ok || blk.Label != "" || blk.Unsafe {
			return body
		}
		if p.hasCommentsStrictlyInsideBlock(blk) {
			return body
		}
		if len(blk.Stmts) == 0 && blk.Tail != nil {
			body = blk.Tail
			continue
		}
		if allowStmtBody && len(blk.Stmts) == 1 && blk.Tail == nil {
			switch st := blk.Stmts[0].(type) {
			case *ast.UnwindStmt:
				return &ast.StmtBodyExpr{Stmt: st, Span: st.Span}
			case *ast.AssignStmt:
				return &ast.StmtBodyExpr{Stmt: st, Span: st.Span}
			}
		}
		return body
	}
}

// unwrapBodyIfElseSafe unwraps rawBody unless hasOuterElse is true and the unwrapped body
// ends with an unbraced `if`/`for`/`while` lacking an `else`, which would greedily capture the outer `else`.
func (p *printer) unwrapBodyIfElseSafe(rawBody ast.Expr, allowStmtBody bool, hasOuterElse bool) ast.Expr {
	unwrapped := p.unwrapBody(rawBody, allowStmtBody)
	if hasOuterElse && unwrapped != rawBody && endsWithDanglingElseTarget(unwrapped) {
		return rawBody
	}
	return unwrapped
}

func endsWithDanglingElseTarget(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.IfExpr:
		if v.Else == nil {
			return true
		}
		return endsWithDanglingElseTarget(v.Else)
	case *ast.ForExpr:
		if v.Else == nil {
			return true
		}
		return endsWithDanglingElseTarget(v.Else)
	case *ast.WhileExpr:
		if v.Else == nil {
			return true
		}
		return endsWithDanglingElseTarget(v.Else)
	case *ast.MatchExpr:
		if len(v.Arms) > 0 {
			return endsWithDanglingElseTarget(v.Arms[len(v.Arms)-1].Body)
		}
		return false
	case *ast.UnaryExpr:
		return endsWithDanglingElseTarget(v.Operand)
	case *ast.BinaryExpr:
		return endsWithDanglingElseTarget(v.Right)
	case *ast.UnsafeExpr:
		return endsWithDanglingElseTarget(v.Expr)
	default:
		return false
	}
}

// printColonBody prints a body immediately after `:` at the current header line.
// It unwraps single-expression blocks, places single-line bodies inline when they fit
// LineWidth, and places multiline single expressions on the next line indented +1 tab.
func (p *printer) printColonBody(headerStartOff int, rawBody ast.Expr, allowStmtBody bool) {
	p.printColonBodyGuarded(headerStartOff, rawBody, allowStmtBody, false)
}

func (p *printer) printColonBodyGuarded(headerStartOff int, rawBody ast.Expr, allowStmtBody bool, hasOuterElse bool) {
	if headerStartOff > 0 && p.hasCommentsBetweenOffsets(headerStartOff, rawBody.GetSpan().Start.Offset) {
		body := p.unwrapBodyIfElseSafe(rawBody, allowStmtBody, hasOuterElse)
		p.newline()
		p.indent++
		_ = p.emitCommentsBefore(rawBody.GetSpan().Start.Offset, rawBody.GetSpan().Start.Line, false)
		p.writeIndent()
		p.printExpr(body)
		p.indent--
		return
	}
	body := p.unwrapBodyIfElseSafe(rawBody, allowStmtBody, hasOuterElse)

	// Block that remained a BlockExpr (empty, 2+ statements, labeled, explicit `;`, or internal comments).
	if blk, ok := body.(*ast.BlockExpr); ok {
		p.write(" ")
		p.printBlockExpr(blk)
		return
	}

	// Single expression or single statement body.
	if !p.hasCommentsInRange(body.GetSpan().Start.Offset, body.GetSpan().End.Offset) {
		if single, okSingle := p.trySingleLine(func(sub *printer) {
			sub.printExpr(body)
		}); okSingle && p.fitsLineWidth(" "+single) && !preferNextLineExpr(body) {
			p.write(" ")
			p.write(single)
			return
		}
	}

	// Multiline single expression (or exceeds LineWidth): next line, tabbed in.
	p.newline()
	p.indent++
	p.writeIndent()
	p.printExpr(body)
	p.indent--
}

// preferNextLineExpr reports expressions that read much better on their own indented line
// following `:`, such as `match(...)`, multi-arm conditionals, or loops.
func preferNextLineExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.MatchExpr:
		return true
	case *ast.IfExpr:
		return v.Else != nil
	}
	return false
}

func (p *printer) printBlockExpr(blk *ast.BlockExpr) {
	if blk.Label != "" {
		p.write(blk.Label)
		p.write(": ")
	}
	if blk.Unsafe {
		// Safety guard: unwrap `unsafe { expr }` only if inner expr is unary/postfix/primary with no internal comments.
		if len(blk.Stmts) == 0 && blk.Tail != nil && !p.hasCommentsStrictlyInsideBlock(blk) && isUnaryOrTighterExpr(blk.Tail) {
			p.write("unsafe ")
			p.printExpr(blk.Tail)
			return
		}
		p.write("unsafe ")
	}
	p.write("{")
	hasComments := p.hasCommentsStrictlyInsideBlock(blk)
	if len(blk.Stmts) == 0 && blk.Tail == nil && !hasComments {
		p.write("}")
		return
	}
	if blk.Span.End.Line > blk.Span.Start.Line {
		p.emitInlineCommentOnLine(blk.Span.Start.Line)
	}

	p.newline()
	p.indent++
	lastLine := blk.Span.Start.Line
	for i, st := range blk.Stmts {
		stPos := st.GetSpan().Start
		if i > 0 && p.hadBlankLineBetween(lastLine, stPos.Line) && !p.hasCommentsBetweenOffsets(blk.Stmts[i-1].GetSpan().End.Offset, stPos.Offset) {
			p.newline()
		}
		blank := p.emitCommentsBefore(stPos.Offset, stPos.Line, i > 0 && lastLine > blk.Span.Start.Line)
		if blank {
			p.newline()
		}
		p.writeIndent()
		p.printStmt(st)
		p.emitInlineCommentOnLine(st.GetSpan().End.Line)
		p.newline()
		lastLine = st.GetSpan().End.Line
	}

	if blk.Tail != nil {
		tailPos := blk.Tail.GetSpan().Start
		if len(blk.Stmts) > 0 && p.hadBlankLineBetween(lastLine, tailPos.Line) && !p.hasCommentsBetweenOffsets(blk.Stmts[len(blk.Stmts)-1].GetSpan().End.Offset, tailPos.Offset) {
			p.newline()
		}
		blank := p.emitCommentsBefore(tailPos.Offset, tailPos.Line, len(blk.Stmts) > 0)
		if blank {
			p.newline()
		}
		p.writeIndent()
		p.printExpr(blk.Tail)
		p.emitInlineCommentOnLine(blk.Tail.GetSpan().End.Line)
		p.newline()
		lastLine = blk.Tail.GetSpan().End.Line
	}

	_ = p.emitCommentsBefore(blk.Span.End.Offset, blk.Span.End.Line, len(blk.Stmts) > 0 || blk.Tail != nil)
	p.indent--
	p.writeIndent()
	p.write("}")
}

func isUnaryOrTighterExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BinaryExpr, *ast.RangeExpr, *ast.IfExpr, *ast.ForExpr, *ast.WhileExpr, *ast.MatchExpr, *ast.StmtBodyExpr:
		return false
	case *ast.UnaryExpr:
		return isUnaryOrTighterExpr(v.Operand)
	default:
		return true
	}
}

// -----------------------------------------------------------------------------
// Statements
// -----------------------------------------------------------------------------

func (p *printer) printStmt(s ast.Stmt) {
	switch v := s.(type) {
	case *ast.VarStmt:
		if v.IsBind {
			p.write("bind ")
		} else {
			p.write("let ")
		}
		if v.IsUnpack {
			for i, u := range v.UnpackVars {
				if i > 0 {
					p.write(", ")
				}
				p.write(u.Name)
				if u.Type != nil {
					p.write(": ")
					p.printTypeExpr(u.Type)
				}
			}
			p.write(" = unpack ")
			p.printValueWithFallbackUnwind(v.Value, v.FallbackUnwind)
		} else {
			p.write(v.Name)
			if v.Type != nil {
				p.write(": ")
				p.printTypeExpr(v.Type)
			}
			p.write(" = ")
			p.printValueWithFallbackUnwind(v.Value, v.FallbackUnwind)
		}
	case *ast.AssignStmt:
		if v.IsUnpack {
			for i, t := range v.UnpackTargets {
				if i > 0 {
					p.write(", ")
				}
				p.printExpr(t)
			}
			p.write(" = unpack ")
			p.printValueWithFallbackUnwind(v.Value, v.FallbackUnwind)
		} else {
			p.printExpr(v.Target)
			p.write(" ")
			p.write(assignOpString(v.Op))
			p.write(" ")
			p.printValueWithFallbackUnwind(v.Value, v.FallbackUnwind)
		}
	case *ast.UnwindStmt:
		p.printUnwindStmt(v)
	case *ast.DeclStmt:
		p.printDecl(v.Decl)
	case *ast.ExprStmt:
		p.printExpr(v.Expr)
		// Preserve explicit `;` in ExprStmt when inside non-tail statement position and followed by nothing else
		// only if source explicitly had `;` at statement end (needed when suppressing Tail value at block end).
		if p.exprStmtHadExplicitSemi(v) {
			p.write(";")
		}
	}
}

func (p *printer) printValueWithFallbackUnwind(val ast.Expr, fb *ast.UnwindStmt) {
	if fb != nil && needsBinaryChildParens(token.QuestQuest, val, false, p.hadParensAround(val)) {
		p.write("(")
		p.printExpr(val)
		p.write(")")
	} else {
		p.printExpr(val)
	}
	if fb != nil {
		p.write(" ?? ")
		p.printUnwindStmt(fb)
	}
}

func (p *printer) exprStmtHadExplicitSemi(es *ast.ExprStmt) bool {
	off := es.Span.End.Offset
	if off < 0 || off > len(p.src) {
		return false
	}
	for i := off; i < len(p.src); i++ {
		b := p.src[i]
		if b == ';' {
			return true
		}
		if b == ' ' || b == '\t' || b == '\r' {
			continue
		}
		break
	}
	return false
}

func (p *printer) printUnwindStmt(v *ast.UnwindStmt) {
	switch v.Target {
	case ast.UnwindIf:
		p.write("if ")
	case ast.UnwindMatch:
		p.write("match ")
	case ast.UnwindFor:
		p.write("for ")
	case ast.UnwindWhile:
		p.write("while ")
	case ast.UnwindNext:
		p.write("next ")
	case ast.UnwindFunc:
		p.write("func ")
	case ast.UnwindLabel:
		p.write(v.Label)
		p.write(" ")
	case ast.UnwindNextLabel:
		p.write("next ")
		p.write(v.Label)
		p.write(" ")
	}
	p.write("<-")
	if v.Value != nil {
		p.write(" ")
		p.printExpr(v.Value)
	}
}

func assignOpString(op token.Kind) string {
	switch op {
	case token.PlusEq:
		return "+="
	case token.MinusEq:
		return "-="
	case token.StarEq:
		return "*="
	case token.SlashEq:
		return "/="
	case token.PercentEq:
		return "%="
	case token.AmpEq:
		return "&="
	case token.PipeEq:
		return "|="
	case token.CaretEq:
		return "^="
	case token.ShlEq:
		return "<<="
	case token.ShrEq:
		return ">>="
	default:
		return "="
	}
}

// -----------------------------------------------------------------------------
// Expressions
// -----------------------------------------------------------------------------

func (p *printer) printExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.IntLit:
		if v.Raw != "" {
			p.write(v.Raw)
		} else {
			p.write(strconv.FormatUint(v.Val, 10))
		}
	case *ast.FloatLit:
		if v.Raw != "" {
			p.write(v.Raw)
		} else {
			p.write(strconv.FormatFloat(v.Val, 'g', -1, 64))
		}
	case *ast.BoolLit:
		if v.Val {
			p.write("true")
		} else {
			p.write("false")
		}
	case *ast.NoneLit:
		p.write("none")
	case *ast.CharLit:
		p.printCharLit(v)
	case *ast.StringLit:
		p.printStringLit(v)
	case *ast.InterpStringExpr:
		p.printInterpString(v)
	case *ast.IdentExpr:
		p.write(v.Name)
	case *ast.UnaryExpr:
		p.printUnaryExpr(v)
	case *ast.BinaryExpr:
		p.printBinaryExpr(v)
	case *ast.RangeExpr:
		p.printRangeExpr(v)
	case *ast.MemberExpr:
		p.printMemberOrCallChain(v)
	case *ast.CallExpr:
		p.printMemberOrCallChain(v)
	case *ast.IndexExpr:
		p.printPostfixBase(v.Base)
		p.write("[")
		p.printExpr(v.Index)
		p.write("]")
	case *ast.SliceExpr:
		p.printPostfixBase(v.Base)
		p.write("[")
		if v.Low != nil {
			p.printRangeOperand(v.Low)
		}
		if v.Inclusive {
			p.write("..=")
		} else {
			p.write("..")
		}
		if v.High != nil {
			p.printRangeOperand(v.High)
		}
		p.write("]")
	case *ast.VecLit:
		p.printDelimExprList("[", "]", v.Elems, v.Span)
	case *ast.ArrayLit:
		p.printArrayLit(v)
	case *ast.SetLit:
		p.printDelimExprList("#[", "]", v.Elems, v.Span)
	case *ast.EmptyHashLit:
		p.write("#[]")
	case *ast.MapLit:
		p.printMapLit(v)
	case *ast.TupleLit:
		p.printDelimExprList("+[", "]", v.Elems, v.Span)
	case *ast.StructLit:
		p.printStructLit(v)
	case *ast.FuncLit:
		p.printFuncLit(v)
	case *ast.UnsafeExpr:
		p.printUnsafeExpr(v)
	case *ast.BlockExpr:
		p.printBlockExpr(v)
	case *ast.StmtBodyExpr:
		p.printStmt(v.Stmt)
	case *ast.IfExpr:
		p.printIfExpr(v)
	case *ast.ForExpr:
		p.printForExpr(v)
	case *ast.WhileExpr:
		p.printWhileExpr(v)
	case *ast.MatchExpr:
		p.printMatchExpr(v)
	}
}

func (p *printer) printCharLit(v *ast.CharLit) {
	if v.Span.Start.Offset >= 0 && v.Span.End.Offset <= len(p.src) && v.Span.Start.Offset < v.Span.End.Offset {
		raw := p.src[v.Span.Start.Offset:v.Span.End.Offset]
		if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
			p.write(raw)
			return
		}
	}
	p.write("'")
	p.write(escapeRuneBody(v.Val, '\''))
	p.write("'")
}

func (p *printer) printStringLit(v *ast.StringLit) {
	start := v.Span.Start.Offset
	end := v.Span.End.Offset
	if start >= 0 && end <= len(p.src) {
		// Standard lexer sets segStart immediately after the opening quote (`"` or `` ` ``).
		if start > 0 && end > start && (p.src[start-1] == '"' || p.src[start-1] == '`') && p.src[end-1] == p.src[start-1] {
			p.write(p.src[start-1 : end])
			return
		}
		// Backtick scanner may start Span at the opening `` ` `` itself (`end - start >= 2`).
		if end-start >= 2 && (p.src[start] == '`' || p.src[start] == '"') && p.src[end-1] == p.src[start] {
			p.write(p.src[start:end])
			return
		}
	}
	p.write(strconv.Quote(v.Val))
}

func (p *printer) printInterpString(v *ast.InterpStringExpr) {
	quote := byte('"')
	start := v.Span.Start.Offset
	if start >= 0 && start < len(p.src) {
		if p.src[start] == '"' || p.src[start] == '`' {
			quote = p.src[start]
		} else if start > 0 && (p.src[start-1] == '"' || p.src[start-1] == '`') {
			quote = p.src[start-1]
		}
	}
	preserveNewline := quote == '`' && v.Span.End.Line > v.Span.Start.Line
	p.buf.WriteByte(quote)
	for i, seg := range v.Segments {
		p.write(escapeStringSegment(seg, quote, preserveNewline))
		if i < len(v.Exprs) {
			ex := v.Exprs[i]
			nextSeg := ""
			if i+1 < len(v.Segments) {
				nextSeg = v.Segments[i+1]
			}
			if id, ok := ex.(*ast.IdentExpr); ok && !id.IsOpt && (nextSeg == "" || !isIdentContByte(nextSeg[0])) {
				p.write("$")
				p.write(id.Name)
			} else {
				p.write("${")
				p.printExpr(ex)
				p.write("}")
			}
		}
	}
	p.buf.WriteByte(quote)
}

func isIdentContByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '?'
}

func escapeRuneBody(r rune, quote byte) string {
	switch r {
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	case '\\':
		return `\\`
	case '\x00':
		return `\0`
	}
	if r == rune(quote) {
		return `\` + string(r)
	}
	if r < 0x20 || r == 0x7f {
		return fmt.Sprintf(`\x%02x`, r)
	}
	return string(r)
}

func escapeStringSegment(s string, quote byte, preserveNewline bool) string {
	if quote == '`' {
		var sb strings.Builder
		for _, r := range s {
			switch r {
			case '`':
				sb.WriteString("\\`")
			case '$':
				sb.WriteString(`\$`)
			case '\\':
				sb.WriteString(`\\`)
			case '\n':
				if preserveNewline {
					sb.WriteByte('\n')
				} else {
					sb.WriteString(`\n`)
				}
			case '\r':
				sb.WriteString(`\r`)
			case '\t':
				if preserveNewline {
					sb.WriteByte('\t')
				} else {
					sb.WriteString(`\t`)
				}
			default:
				sb.WriteRune(r)
			}
		}
		return sb.String()
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == '$' {
			sb.WriteString(`\$`)
			continue
		}
		sb.WriteString(escapeRuneBody(r, quote))
	}
	return sb.String()
}

// -----------------------------------------------------------------------------
// Operators & Precedence
// -----------------------------------------------------------------------------

func binaryPrecedence(op token.Kind) (prec int, rightAssoc bool) {
	switch op {
	case token.QuestQuest:
		return 1, true
	case token.KwOr:
		return 2, false
	case token.KwAnd:
		return 3, false
	case token.Pipe:
		return 4, false
	case token.Caret:
		return 5, false
	case token.Amp:
		return 6, false
	case token.Eq, token.Neq:
		return 7, false
	case token.Lt, token.Lte, token.Gt, token.Gte:
		return 8, false
	case token.DotDot, token.DotDotEq:
		return 9, false
	case token.Shl, token.Shr:
		return 10, false
	case token.Plus, token.Minus:
		return 11, false
	case token.Star, token.Slash, token.Percent:
		return 12, false
	default:
		return 0, false
	}
}

func binaryOpString(op token.Kind) string {
	switch op {
	case token.QuestQuest:
		return "??"
	case token.KwOr:
		return "or"
	case token.KwAnd:
		return "and"
	case token.Pipe:
		return "|"
	case token.Caret:
		return "^"
	case token.Amp:
		return "&"
	case token.Eq:
		return "=="
	case token.Neq:
		return "!="
	case token.Lt:
		return "<"
	case token.Lte:
		return "<="
	case token.Gt:
		return ">"
	case token.Gte:
		return ">="
	case token.DotDot:
		return ".."
	case token.DotDotEq:
		return "..="
	case token.Shl:
		return "<<"
	case token.Shr:
		return ">>"
	case token.Plus:
		return "+"
	case token.Minus:
		return "-"
	case token.Star:
		return "*"
	case token.Slash:
		return "/"
	case token.Percent:
		return "%"
	default:
		return op.String()
	}
}

func (p *printer) hadParensAround(e ast.Expr) bool {
	off := e.GetSpan().Start.Offset
	if off <= 0 || off > len(p.src) {
		return false
	}
	for i := off - 1; i >= 0; i-- {
		switch p.src[i] {
		case ' ', '\t':
			continue
		case '(':
			return true
		default:
			return false
		}
	}
	return false
}

func needsBinaryChildParens(parentOp token.Kind, child ast.Expr, isRight bool, hadParens bool) bool {
	parentPrec, rightAssoc := binaryPrecedence(parentOp)
	switch c := child.(type) {
	case *ast.BinaryExpr:
		childPrec, _ := binaryPrecedence(c.Op)
		if childPrec < parentPrec {
			return true
		}
		if childPrec == parentPrec {
			if parentPrec == 7 || parentPrec == 8 {
				// Equality (`==`, `!=`) and relational (`<`, `<=`, `>`, `>=`) operators are non-associative.
				return true
			}
			if rightAssoc && !isRight {
				return true
			}
			if !rightAssoc && isRight && c.Op != parentOp {
				return true
			}
			if !rightAssoc && isRight && (parentOp == token.Minus || parentOp == token.Slash || parentOp == token.Percent || parentOp == token.Shl || parentOp == token.Shr) {
				return true
			}
		}
		// Preserve clarifying parens when mixing distinct logical/bitwise groups (e.g. `(a and b) or (c and d)`).
		if hadParens && c.Op != parentOp && isClarifyingOpPair(parentOp, c.Op) {
			return true
		}
	case *ast.RangeExpr:
		if parentPrec >= 9 {
			return true
		}
	case *ast.StmtBodyExpr:
		if parentOp == token.QuestQuest && isRight {
			if _, isUW := c.Stmt.(*ast.UnwindStmt); isUW {
				return false
			}
		}
		return true
	case *ast.IfExpr, *ast.ForExpr, *ast.WhileExpr, *ast.MatchExpr:
		return true
	}
	return false
}

func isClarifyingOpPair(pOp, cOp token.Kind) bool {
	logical := func(k token.Kind) bool { return k == token.KwAnd || k == token.KwOr }
	bitwise := func(k token.Kind) bool { return k == token.Amp || k == token.Pipe || k == token.Caret }
	return (logical(pOp) && logical(cOp)) || (bitwise(pOp) && bitwise(cOp))
}

func (p *printer) printBinaryExpr(v *ast.BinaryExpr) {
	wrapL := needsBinaryChildParens(v.Op, v.Left, false, p.hadParensAround(v.Left))
	wrapR := needsBinaryChildParens(v.Op, v.Right, true, p.hadParensAround(v.Right))
	if wrapL {
		p.write("(")
		p.printExpr(v.Left)
		p.write(")")
	} else {
		p.printExpr(v.Left)
	}
	p.write(" ")
	p.write(binaryOpString(v.Op))
	p.write(" ")
	if wrapR {
		p.write("(")
		p.printExpr(v.Right)
		p.write(")")
	} else {
		p.printExpr(v.Right)
	}
}

func (p *printer) printUnaryExpr(v *ast.UnaryExpr) {
	switch v.Op {
	case token.KwNot:
		p.write("not ")
	case token.KwOwn:
		p.write("own ")
	case token.Minus:
		p.write("-")
	case token.Tilde:
		p.write("~")
	case token.Amp:
		p.write("&")
	case token.Star:
		p.write("*")
	}
	wrap := !isUnaryOrTighterExpr(v.Operand)
	if v.Op == token.Minus {
		if innerU, ok := v.Operand.(*ast.UnaryExpr); ok && innerU.Op == token.Minus {
			wrap = true
		}
	}
	if wrap {
		p.write("(")
		p.printExpr(v.Operand)
		p.write(")")
	} else {
		p.printExpr(v.Operand)
	}
}

func (p *printer) printUnsafeExpr(v *ast.UnsafeExpr) {
	if blk, ok := v.Expr.(*ast.BlockExpr); ok {
		blkCopy := *blk
		blkCopy.Unsafe = true
		p.printBlockExpr(&blkCopy)
		return
	}
	p.write("unsafe ")
	if !isUnaryOrTighterExpr(v.Expr) {
		p.write("(")
		p.printExpr(v.Expr)
		p.write(")")
	} else {
		p.printExpr(v.Expr)
	}
}

func (p *printer) printRangeOperand(e ast.Expr) {
	wrap := false
	switch c := e.(type) {
	case *ast.BinaryExpr:
		prec, _ := binaryPrecedence(c.Op)
		if prec <= 9 {
			wrap = true
		}
	case *ast.RangeExpr, *ast.IfExpr, *ast.ForExpr, *ast.WhileExpr, *ast.MatchExpr, *ast.StmtBodyExpr:
		wrap = true
	}
	if wrap {
		p.write("(")
		p.printExpr(e)
		p.write(")")
	} else {
		p.printExpr(e)
	}
}

func (p *printer) printRangeExpr(v *ast.RangeExpr) {
	p.printRangeOperand(v.Low)
	if v.Inclusive {
		p.write("..=")
	} else {
		p.write("..")
	}
	p.printRangeOperand(v.High)
}

func (p *printer) printPostfixBase(base ast.Expr) {
	wrap := !isPostfixOrPrimary(base)
	if wrap {
		p.write("(")
		p.printExpr(base)
		p.write(")")
	} else {
		p.printExpr(base)
	}
}

func isPostfixOrPrimary(e ast.Expr) bool {
	switch e.(type) {
	case *ast.BinaryExpr, *ast.UnaryExpr, *ast.RangeExpr, *ast.UnsafeExpr,
		*ast.IfExpr, *ast.ForExpr, *ast.WhileExpr, *ast.MatchExpr, *ast.StmtBodyExpr, *ast.FuncLit:
		return false
	default:
		return true
	}
}

// -----------------------------------------------------------------------------
// Calls, Method Chains & Postfix Wrapping
// -----------------------------------------------------------------------------

type chainLink struct {
	member *ast.MemberExpr
	call   *ast.CallExpr // non-nil if this link immediately invokes the member
}

func (p *printer) printMemberOrCallChain(e ast.Expr) {
	root, links := extractMethodChain(e)
	if len(links) >= 2 && p.opts.LineWidth > 0 {
		if p.wasChainMultiline(root, links) {
			p.printPostfixBase(root)
			p.indent++
			for _, lk := range links {
				p.newline()
				p.writeIndent()
				p.printChainLink(lk)
			}
			p.indent--
			return
		}
		single, ok := p.trySingleLine(func(sub *printer) {
			sub.printChainInline(root, links)
		})
		if ok && p.fitsLineWidth(single) {
			p.write(single)
			return
		}
		// Only wrap the dot chain if the receiver+member prefix/middle links themselves overflow LineWidth,
		// not merely because the terminal call carries a single multiline literal argument (e.g. `cp._frames.push(Frame[...])`).
		if !ok {
			if prefix, okPrefix := p.trySingleLine(func(sub *printer) {
				sub.printChainHeaderForWrapCheck(root, links)
			}); !okPrefix || !p.fitsLineWidth(prefix) {
				p.printPostfixBase(root)
				p.indent++
				for _, lk := range links {
					p.newline()
					p.writeIndent()
					p.printChainLink(lk)
				}
				p.indent--
				return
			}
		} else if !p.fitsLineWidth(single) {
			p.printPostfixBase(root)
			p.indent++
			for _, lk := range links {
				p.newline()
				p.writeIndent()
				p.printChainLink(lk)
			}
			p.indent--
			return
		}
	}
	// Default: inline (handles single call, single member, or terminal multiline arg call).
	switch v := e.(type) {
	case *ast.MemberExpr:
		p.printPostfixBase(v.Base)
		p.printMemberSuffix(v)
	case *ast.CallExpr:
		p.printPostfixBase(v.Callee)
		p.printTypeArgList(v.TypeArgs)
		p.printDelimExprList("(", ")", v.Args, diag.Span{Start: v.Callee.GetSpan().End, End: v.Span.End})
	}
}

func (p *printer) printChainHeaderForWrapCheck(root ast.Expr, links []chainLink) {
	p.printPostfixBase(root)
	for i, lk := range links {
		p.printMemberSuffix(lk.member)
		if lk.call != nil {
			p.printTypeArgList(lk.call.TypeArgs)
			if i == len(links)-1 {
				p.write("(")
			} else {
				p.printDelimExprList("(", ")", lk.call.Args, diag.Span{})
			}
		}
	}
}

func extractMethodChain(e ast.Expr) (ast.Expr, []chainLink) {
	var links []chainLink
	cur := e
	for {
		switch v := cur.(type) {
		case *ast.CallExpr:
			mem, ok := v.Callee.(*ast.MemberExpr)
			if !ok {
				goto done
			}
			links = append(links, chainLink{member: mem, call: v})
			cur = mem.Base
		case *ast.MemberExpr:
			links = append(links, chainLink{member: v})
			cur = v.Base
		default:
			goto done
		}
	}
done:
	if len(links) < 2 {
		return e, nil
	}
	for l, r := 0, len(links)-1; l < r; l, r = l+1, r-1 {
		links[l], links[r] = links[r], links[l]
	}
	return cur, links
}

func (p *printer) wasChainMultiline(root ast.Expr, links []chainLink) bool {
	prevEndLine := root.GetSpan().End.Line
	if prevEndLine <= 0 {
		return false
	}
	for _, lk := range links {
		// Compare link `.member` end line against previous segment's end line (before call `(args)`).
		if lk.member.Span.End.Line > prevEndLine {
			return true
		}
		if lk.call != nil {
			prevEndLine = lk.call.Span.End.Line
		} else {
			prevEndLine = lk.member.Span.End.Line
		}
	}
	return false
}

func (p *printer) printChainInline(root ast.Expr, links []chainLink) {
	p.printPostfixBase(root)
	for _, lk := range links {
		p.printChainLink(lk)
	}
}

func (p *printer) printChainLink(lk chainLink) {
	p.printMemberSuffix(lk.member)
	if lk.call != nil {
		p.printTypeArgList(lk.call.TypeArgs)
		p.printDelimExprList("(", ")", lk.call.Args, diag.Span{Start: lk.member.Span.End, End: lk.call.Span.End})
	}
}

func (p *printer) printMemberSuffix(v *ast.MemberExpr) {
	if len(v.BaseTypeArgs) > 0 {
		p.printTypeArgList(v.BaseTypeArgs)
	}
	if v.QuestDot {
		p.write("?.")
	} else {
		p.write(".")
	}
	p.write(v.Member)
}

func (p *printer) printTypeArgList(targs []ast.TypeExpr) {
	if len(targs) == 0 {
		return
	}
	p.write("[")
	for i, ta := range targs {
		if i > 0 {
			p.write(", ")
		}
		p.printTypeExpr(ta)
	}
	p.write("]")
}

// -----------------------------------------------------------------------------
// Comma-Separated Collections, Literals & Wrapping
// -----------------------------------------------------------------------------

func (p *printer) wasSpanMultiline(span diag.Span, firstItem, lastItem diag.Span) bool {
	if firstItem.Start.Line > 0 && lastItem.End.Line > firstItem.Start.Line {
		return true
	}
	if span.Start.Line > 0 && firstItem.Start.Line > span.Start.Line {
		return true
	}
	return false
}

func (p *printer) writeCommaListParts(parts []string, trailingOnSingle bool, closeIndent int) {
	if len(parts) == 0 {
		return
	}
	inline := strings.Join(parts, ", ")
	if p.fitsLineWidth(inline) && !strings.ContainsRune(inline, '\n') {
		p.write(inline)
		if trailingOnSingle {
			p.write(",")
		}
		return
	}
	p.newline()
	p.indent++
	for _, part := range parts {
		p.writeIndent()
		p.write(part)
		if len(parts) > 1 {
			p.write(",")
		}
		p.newline()
	}
	p.indent--
	p.writeIndentLevel(closeIndent)
}

func (p *printer) printDelimExprList(openDelim, closeDelim string, elems []ast.Expr, containerSpan diag.Span) {
	p.write(openDelim)
	if len(elems) == 0 {
		p.write(closeDelim)
		return
	}
	hasComments := containerSpan.End.Offset > containerSpan.Start.Offset && p.hasCommentsInRange(containerSpan.Start.Offset, containerSpan.End.Offset)
	wantMultiline := hasComments || p.wasSpanMultiline(containerSpan, elems[0].GetSpan(), elems[len(elems)-1].GetSpan())

	if !wantMultiline {
		parts := make([]string, len(elems))
		canInline := true
		for i, el := range elems {
			s, ok := p.trySingleLine(func(sub *printer) {
				sub.printExpr(el)
			})
			if !ok {
				canInline = false
				break
			}
			parts[i] = s
		}
		if canInline {
			inline := strings.Join(parts, ", ")
			if p.fitsLineWidth(inline + closeDelim) {
				p.write(inline)
				p.write(closeDelim)
				return
			}
		}
	}

	// Single composite literal / block / lambda argument inside `(...)`: format compactly as `fn(Arg[\n...\n])`.
	if openDelim == "(" && len(elems) == 1 && isCompositeDelimArg(elems[0]) {
		argSpan := elems[0].GetSpan()
		hasOuterComments := containerSpan.End.Offset > containerSpan.Start.Offset &&
			(p.hasCommentsBetweenOffsets(containerSpan.Start.Offset, argSpan.Start.Offset) ||
				p.hasCommentsBetweenOffsets(argSpan.End.Offset, containerSpan.End.Offset))
		if !hasOuterComments && !p.hasCommentsInRange(argSpan.Start.Offset, argSpan.End.Offset) {
			sub := p.cloneTrial(p.indent, false)
			sub.printExpr(elems[0])
			argText := sub.buf.String()
			firstNL := strings.IndexByte(argText, '\n')
			if firstNL > 0 && p.fitsLineWidth(argText[:firstNL]) {
				p.printExpr(elems[0])
				p.write(closeDelim)
				return
			}
		}
	}

	p.newline()
	p.indent++
	for _, el := range elems {
		sp := el.GetSpan()
		if sp.Start.Offset > 0 {
			_ = p.emitCommentsBefore(sp.Start.Offset, sp.Start.Line, false)
		}
		p.writeIndent()
		p.printExpr(el)
		if len(elems) > 1 {
			p.write(",")
		}
		p.emitInlineCommentOnLine(sp.End.Line)
		p.newline()
	}
	if containerSpan.End.Offset > 0 {
		_ = p.emitCommentsBefore(containerSpan.End.Offset, containerSpan.End.Line, false)
	}
	p.indent--
	p.writeIndent()
	p.write(closeDelim)
}

func isCompositeDelimArg(e ast.Expr) bool {
	switch e.(type) {
	case *ast.StructLit, *ast.VecLit, *ast.ArrayLit, *ast.SetLit, *ast.MapLit, *ast.TupleLit, *ast.BlockExpr, *ast.FuncLit:
		return true
	default:
		return false
	}
}

func (p *printer) printArrayLit(v *ast.ArrayLit) {
	p.write("[")
	p.printExpr(v.Count)
	p.write(": ")
	if len(v.Elems) == 0 {
		p.write("]")
		return
	}
	hasComments := p.hasCommentsInRange(v.Span.Start.Offset, v.Span.End.Offset)
	wantMultiline := hasComments || p.wasSpanMultiline(v.Span, v.Elems[0].GetSpan(), v.Elems[len(v.Elems)-1].GetSpan())
	if !wantMultiline {
		parts := make([]string, len(v.Elems))
		canInline := true
		for i, el := range v.Elems {
			s, ok := p.trySingleLine(func(sub *printer) {
				sub.printExpr(el)
			})
			if !ok {
				canInline = false
				break
			}
			parts[i] = s
		}
		if canInline {
			inline := strings.Join(parts, ", ")
			if p.fitsLineWidth(inline + "]") {
				p.write(inline)
				p.write("]")
				return
			}
		}
	}
	p.newline()
	p.indent++
	for _, el := range v.Elems {
		sp := el.GetSpan()
		if sp.Start.Offset > 0 {
			_ = p.emitCommentsBefore(sp.Start.Offset, sp.Start.Line, false)
		}
		p.writeIndent()
		p.printExpr(el)
		if len(v.Elems) > 1 {
			p.write(",")
		}
		p.emitInlineCommentOnLine(sp.End.Line)
		p.newline()
	}
	_ = p.emitCommentsBefore(v.Span.End.Offset, v.Span.End.Line, false)
	p.indent--
	p.writeIndent()
	p.write("]")
}

func (p *printer) printMapLit(v *ast.MapLit) {
	p.write("#[")
	if len(v.Entries) == 0 {
		p.write("]")
		return
	}
	hasComments := p.hasCommentsInRange(v.Span.Start.Offset, v.Span.End.Offset)
	firstSpan := v.Entries[0].Span
	lastSpan := v.Entries[len(v.Entries)-1].Span
	wantMultiline := hasComments || p.wasSpanMultiline(v.Span, firstSpan, lastSpan)

	if !wantMultiline {
		parts := make([]string, len(v.Entries))
		canInline := true
		for i, ent := range v.Entries {
			s, ok := p.trySingleLine(func(sub *printer) {
				sub.printExpr(ent.Key)
				sub.write(": ")
				sub.printExpr(ent.Value)
			})
			if !ok {
				canInline = false
				break
			}
			parts[i] = s
		}
		if canInline {
			inline := strings.Join(parts, ", ")
			if p.fitsLineWidth(inline + "]") {
				p.write(inline)
				p.write("]")
				return
			}
		}
	}

	p.newline()
	p.indent++
	for _, ent := range v.Entries {
		if ent.Span.Start.Offset > 0 {
			_ = p.emitCommentsBefore(ent.Span.Start.Offset, ent.Span.Start.Line, false)
		}
		p.writeIndent()
		p.printExpr(ent.Key)
		p.write(": ")
		p.printExpr(ent.Value)
		if len(v.Entries) > 1 {
			p.write(",")
		}
		p.emitInlineCommentOnLine(ent.Span.End.Line)
		p.newline()
	}
	_ = p.emitCommentsBefore(v.Span.End.Offset, v.Span.End.Line, false)
	p.indent--
	p.writeIndent()
	p.write("]")
}

func (p *printer) printStructLit(v *ast.StructLit) {
	if v.Module != "" {
		p.write(v.Module)
		p.write(".")
	}
	p.write(v.TypeName)
	p.printTypeArgList(v.TypeArgs)
	p.write("[")
	if len(v.Fields) == 0 {
		p.write("]")
		return
	}
	hasComments := p.hasCommentsInRange(v.Span.Start.Offset, v.Span.End.Offset)
	wantMultiline := hasComments || p.wasSpanMultiline(v.Span, v.Fields[0].Span, v.Fields[len(v.Fields)-1].Span)

	if !wantMultiline {
		parts := make([]string, len(v.Fields))
		canInline := true
		for i, f := range v.Fields {
			s, ok := p.trySingleLine(func(sub *printer) {
				sub.write(f.Name)
				sub.write(": ")
				sub.printExpr(f.Value)
			})
			if !ok {
				canInline = false
				break
			}
			parts[i] = s
		}
		if canInline {
			inline := strings.Join(parts, ", ")
			if p.fitsLineWidth(inline + "]") {
				p.write(inline)
				p.write("]")
				return
			}
		}
	}

	p.newline()
	p.indent++
	for _, f := range v.Fields {
		if f.Span.Start.Offset > 0 {
			_ = p.emitCommentsBefore(f.Span.Start.Offset, f.Span.Start.Line, false)
		}
		p.writeIndent()
		p.write(f.Name)
		p.write(": ")
		p.printExpr(f.Value)
		if len(v.Fields) > 1 {
			p.write(",")
		}
		p.emitInlineCommentOnLine(f.Span.End.Line)
		p.newline()
	}
	_ = p.emitCommentsBefore(v.Span.End.Offset, v.Span.End.Line, false)
	p.indent--
	p.writeIndent()
	p.write("]")
}

func (p *printer) printFuncLit(v *ast.FuncLit) {
	p.printParamList(v.Params)
	if v.Ret != nil {
		p.write(" ")
		p.printTypeExpr(v.Ret)
	}
	p.write(":")
	p.printColonBody(v.Span.Start.Offset, v.Body, false)
}

// -----------------------------------------------------------------------------
// Control Flow (`if`, `for`, `while`, `match`)
// -----------------------------------------------------------------------------

func (p *printer) printIfCond(v *ast.IfExpr) {
	p.write("if(")
	switch v.CondKind {
	case ast.IfCondOptVar:
		if v.MutUnwrap {
			p.write("&")
		}
		p.write(v.OptVar)
	case ast.IfCondOptBind:
		if v.MutUnwrap {
			p.write("&")
		}
		p.printExpr(v.Cond)
		p.write(" as ")
		p.write(v.BindName)
	default:
		p.printExpr(v.Cond)
	}
	p.write(")")
	if v.EvalType != nil {
		p.write(" ")
		p.printTypeExpr(v.EvalType)
	}
	p.write(":")
}

func (p *printer) printIfExpr(v *ast.IfExpr) {
	// Single-line compact `if(c): a else: b` or `if(c): a` when no internal comments and everything fits.
	if v.Else == nil {
		p.printIfCond(v)
		p.printColonBody(v.Span.Start.Offset, v.Then, true)
		return
	}

	if !p.hasCommentsInRange(v.Span.Start.Offset, v.Span.End.Offset) {
		thenUnwrapped := p.unwrapBodyIfElseSafe(v.Then, true, true)
		elseUnwrapped := p.unwrapBody(v.Else, true)
		_, thenBlk := thenUnwrapped.(*ast.BlockExpr)
		_, elseBlk := elseUnwrapped.(*ast.BlockExpr)
		_, elseIf := elseUnwrapped.(*ast.IfExpr)
		if !thenBlk && !elseBlk && !elseIf && !endsWithDanglingElseTarget(thenUnwrapped) {
			single, ok := p.trySingleLine(func(sub *printer) {
				sub.printIfCond(v)
				sub.write(" ")
				sub.printExpr(thenUnwrapped)
				sub.write(" else: ")
				sub.printExpr(elseUnwrapped)
			})
			if ok && p.fitsLineWidth(single) && !preferNextLineExpr(thenUnwrapped) && !preferNextLineExpr(elseUnwrapped) {
				p.write(single)
				return
			}
		}
	}

	// Multiline or chained `if (...) ... else ...`
	p.printIfBranchChain(v)
}

func (p *printer) printIfBranchChain(v *ast.IfExpr) {
	p.printIfCond(v)
	thenUnwrapped := p.unwrapBodyIfElseSafe(v.Then, true, v.Else != nil)
	thenIsBlock := false
	if blk, ok := thenUnwrapped.(*ast.BlockExpr); ok {
		p.write(" ")
		p.printBlockExpr(blk)
		thenIsBlock = true
	} else if v.Else != nil && endsWithDanglingElseTarget(thenUnwrapped) {
		p.write(" {\n")
		p.indent++
		p.writeIndent()
		p.printExpr(thenUnwrapped)
		p.newline()
		p.indent--
		p.writeIndent()
		p.write("}")
		thenIsBlock = true
	} else if !p.hasCommentsInRange(thenUnwrapped.GetSpan().Start.Offset, thenUnwrapped.GetSpan().End.Offset) && !preferNextLineExpr(thenUnwrapped) {
		if single, ok := p.trySingleLine(func(sub *printer) {
			sub.printExpr(thenUnwrapped)
		}); ok && p.fitsLineWidth(" "+single) {
			p.write(" ")
			p.write(single)
		} else {
			p.newline()
			p.indent++
			p.writeIndent()
			p.printExpr(thenUnwrapped)
			p.indent--
		}
	} else {
		p.newline()
		p.indent++
		p.writeIndent()
		p.printExpr(thenUnwrapped)
		p.indent--
	}

	if v.Else == nil {
		return
	}

	if thenIsBlock {
		p.write(" else")
	} else {
		p.newline()
		p.writeIndent()
		p.write("else")
	}

	// Normalize `else { if (...) ... }` or `else if (...) ...`
	elseUnwrapped := p.unwrapBody(v.Else, true)
	if nextIf, isChain := elseUnwrapped.(*ast.IfExpr); isChain {
		p.write(" ")
		p.printIfBranchChain(nextIf)
		return
	}
	p.write(":")
	p.printColonBody(v.Then.GetSpan().End.Offset, elseUnwrapped, true)
}

func (p *printer) printLoopHeader(label, kw string, kind ast.ForKind, var1 string, var1Mut bool, var2 string, var2Mut bool, condOrIter, rangeEnd ast.Expr, inclusive bool, evalType ast.TypeExpr) {
	if label != "" {
		p.write(label)
		p.write(": ")
	}
	p.write(kw)
	p.write("(")
	switch kind {
	case ast.ForCond:
		p.printExpr(condOrIter)
	case ast.ForRange:
		if var1Mut {
			p.write("&")
		}
		p.write(var1)
		p.write(" in ")
		p.printRangeOperand(condOrIter)
		if inclusive {
			p.write("..=")
		} else {
			p.write("..")
		}
		p.printRangeOperand(rangeEnd)
	case ast.ForIter:
		if var1Mut {
			p.write("&")
		}
		p.write(var1)
		if var2 != "" {
			p.write(", ")
			if var2Mut {
				p.write("&")
			}
			p.write(var2)
		}
		p.write(" in ")
		p.printExpr(condOrIter)
	}
	p.write(")")
	if evalType != nil {
		p.write(" ")
		p.printTypeExpr(evalType)
	}
	p.write(":")
}

func (p *printer) printForExpr(v *ast.ForExpr) {
	p.printLoopHeader(v.Label, "for", v.Kind, v.Var1, v.Var1Mut, v.Var2, v.Var2Mut, v.CondOrIter, v.RangeEnd, v.Inclusive, v.EvalType)
	p.printColonBodyGuarded(v.Span.Start.Offset, v.Body, true, v.Else != nil)
	if v.Else != nil {
		bodyUnwrapped := p.unwrapBodyIfElseSafe(v.Body, true, true)
		if _, isBlk := bodyUnwrapped.(*ast.BlockExpr); isBlk {
			p.write(" else:")
		} else {
			p.newline()
			p.writeIndent()
			p.write("else:")
		}
		p.printColonBody(v.Body.GetSpan().End.Offset, v.Else, true)
	}
}

func (p *printer) printWhileExpr(v *ast.WhileExpr) {
	p.printLoopHeader(v.Label, "while", v.Kind, v.Var1, v.Var1Mut, v.Var2, v.Var2Mut, v.CondOrIter, v.RangeEnd, v.Inclusive, v.EvalType)
	p.printColonBodyGuarded(v.Span.Start.Offset, v.Body, true, v.Else != nil)
	if v.Else != nil {
		bodyUnwrapped := p.unwrapBodyIfElseSafe(v.Body, true, true)
		if _, isBlk := bodyUnwrapped.(*ast.BlockExpr); isBlk {
			p.write(" else:")
		} else {
			p.newline()
			p.writeIndent()
			p.write("else:")
		}
		p.printColonBody(v.Body.GetSpan().End.Offset, v.Else, true)
	}
}

func (p *printer) printMatchExpr(v *ast.MatchExpr) {
	p.write("match")
	if len(v.Scrutinees) > 0 {
		p.write("(")
		for i, sc := range v.Scrutinees {
			if i > 0 {
				p.write(", ")
			}
			p.printExpr(sc)
		}
		p.write(")")
	}
	if v.EvalType != nil {
		p.write(" ")
		p.printTypeExpr(v.EvalType)
	}
	p.write(":")
	if len(v.Arms) == 0 {
		return
	}
	numScrut := len(v.Scrutinees)
	p.indent++
	for _, arm := range v.Arms {
		p.newline()
		if arm.Span.Start.Offset > 0 {
			_ = p.emitCommentsBefore(arm.Span.Start.Offset, arm.Span.Start.Line, false)
		}
		p.writeIndent()
		if arm.IsDefault {
			p.write("default")
			if len(arm.Cases) > 0 {
				p.write(" ")
				p.printMatchCases(arm.Cases, numScrut)
			}
		} else {
			p.write("when ")
			p.printMatchCases(arm.Cases, numScrut)
		}
		p.write(":")
		p.printColonBody(arm.Span.Start.Offset, arm.Body, true)
		p.emitInlineCommentOnLine(arm.Span.End.Line)
	}
	p.indent--
}

func (p *printer) printMatchCases(cases []ast.MatchCase, numScrut int) {
	for i, c := range cases {
		if i > 0 {
			p.write(", ")
		}
		if numScrut >= 2 && len(cases) > 1 {
			p.write("(")
		}
		for j, pat := range c.Patterns {
			if j > 0 {
				p.write(", ")
			}
			p.printPattern(pat)
		}
		if numScrut >= 2 && len(cases) > 1 {
			p.write(")")
		}
	}
}

func (p *printer) printPattern(pat ast.Pattern) {
	switch v := pat.(type) {
	case *ast.WildcardPattern:
		p.write("_")
	case *ast.ExprPattern:
		p.printExpr(v.Expr)
	case *ast.ContainsPattern:
		p.write("in ")
		p.printExpr(v.Container)
	case *ast.VariantPattern:
		p.printExpr(v.Target)
		if len(v.Bindings) > 0 {
			p.write("(")
			for i, b := range v.Bindings {
				if i > 0 {
					p.write(", ")
				}
				if b.IsWildcard {
					p.write("_")
					continue
				}
				switch b.Mode {
				case ast.PatternBindMut:
					p.write("&")
				case ast.PatternBindOwn:
					p.write("own ")
				}
				p.write(b.BindName)
			}
			p.write(")")
		}
	}
}
