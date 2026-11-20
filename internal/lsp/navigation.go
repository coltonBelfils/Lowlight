package lsp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/parser"
)

// -----------------------------------------------------------------------------
// Workspace Symbols (`workspace/symbol`)
// -----------------------------------------------------------------------------

func (s *Server) computeWorkspaceSymbols(query string) []SymbolInformation {
	q := strings.ToLower(strings.TrimSpace(query))

	// Gather open documents plus workspace `.ll` files.
	overlays := s.snapshotOverlays()
	seenFiles := make(map[string]bool)
	var fileList []string

	for p := range overlays {
		abs := absClean(p)
		if !seenFiles[abs] {
			seenFiles[abs] = true
			fileList = append(fileList, abs)
		}
	}

	s.mu.RLock()
	root := s.rootDir
	s.mu.RUnlock()

	if root != "" {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				if shouldSkipWorkspaceDir(name) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(name, ".ll") {
				abs := absClean(p)
				if !seenFiles[abs] {
					seenFiles[abs] = true
					fileList = append(fileList, abs)
				}
				if len(fileList) >= 300 {
					return filepath.SkipAll
				}
			}
			return nil
		})
	}

	sort.Strings(fileList)

	var out []SymbolInformation
	matchesQuery := func(name string) bool {
		if q == "" {
			return true
		}
		return strings.Contains(strings.ToLower(name), q)
	}

	for _, fpath := range fileList {
		src, ok := overlays[fpath]
		if !ok {
			raw, err := os.ReadFile(fpath)
			if err != nil {
				continue
			}
			src = string(raw)
		}
		rep := diag.NewReporter()
		af, _ := parser.ParseFileWithComments(fpath, src, rep)
		if af == nil {
			continue
		}
		uri := PathToURI(fpath)
		modContainer := strings.TrimSuffix(filepath.Base(fpath), ".ll")

		add := func(name string, kind SymbolKind, sp diag.Span, container string) {
			if !matchesQuery(name) || sp.Start.Line <= 0 {
				return
			}
			out = append(out, SymbolInformation{
				Name: name,
				Kind: kind,
				Location: Location{
					URI:   uri,
					Range: ExactSpanToLSP(src, sp),
				},
				ContainerName: container,
			})
		}

		for _, d := range af.Decls {
			switch decl := d.(type) {
			case *ast.StructDecl:
				add(decl.Name, SymStruct, decl.Span, modContainer)
				for _, f := range decl.Fields {
					add(decl.Name+"."+f.Name, SymField, f.Span, decl.Name)
				}
			case *ast.EnumDecl:
				add(decl.Name, SymEnum, decl.Span, modContainer)
				for _, v := range decl.Variants {
					add(decl.Name+"."+v.Name, SymEnumMember, v.Span, decl.Name)
				}
			case *ast.InterfaceDecl:
				add(decl.Name, SymInterface, decl.Span, modContainer)
				for _, m := range decl.Methods {
					add(decl.Name+"."+m.Name, SymMethod, m.Span, decl.Name)
				}
			case *ast.DistinctTypeDecl:
				add(decl.Name, SymStruct, decl.Span, modContainer)
			case *ast.MethodDecl:
				add(decl.RecvType+"."+decl.Name, SymMethod, decl.Span, decl.RecvType)
			case *ast.ExternFnDecl:
				n := decl.Name
				if decl.Alias != "" {
					n = decl.Alias
				}
				add(n, SymFunction, decl.Span, modContainer)
			case *ast.VarDecl:
				kind := SymConstant
				if _, isFn := decl.Value.(*ast.FuncLit); isFn {
					kind = SymFunction
				}
				add(decl.Name, kind, decl.Span, modContainer)
			}
		}
		if len(out) >= 500 {
			break
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// Folding Ranges (`textDocument/foldingRange`)
// -----------------------------------------------------------------------------

func (s *Server) computeFoldingRanges(filePath, src string) []FoldingRange {
	rep := diag.NewReporter()
	af, comments := parser.ParseFileWithComments(filePath, src, rep)

	var ranges []FoldingRange
	seen := make(map[string]bool)
	addFold := func(startLine, endLine int, kind string) {
		if endLine <= startLine {
			return
		}
		key := fmt.Sprintf("%d:%d:%s", startLine, endLine, kind)
		if seen[key] {
			return
		}
		seen[key] = true
		ranges = append(ranges, FoldingRange{
			StartLine: startLine,
			EndLine:   endLine,
			Kind:      kind,
		})
	}

	// Fold contiguous comment blocks and multi-line /* ... */ comments
	i := 0
	for i < len(comments) {
		c := comments[i]
		if c.IsBlock {
			if c.Span.End.Line > c.Span.Start.Line {
				addFold(c.Span.Start.Line-1, c.Span.End.Line-1, "comment")
			}
			i++
			continue
		}
		startLine := c.Span.Start.Line
		endLine := c.Span.End.Line
		j := i + 1
		for j < len(comments) && !comments[j].IsBlock && comments[j].Span.Start.Line == endLine+1 {
			endLine = comments[j].Span.End.Line
			j++
		}
		if endLine > startLine {
			addFold(startLine-1, endLine-1, "comment")
		}
		i = j
	}

	if af == nil {
		return ranges
	}

	// Fold contiguous import blocks
	impStart := -1
	impEnd := -1
	flushImports := func() {
		if impStart >= 0 && impEnd > impStart {
			addFold(impStart, impEnd, "imports")
		}
		impStart = -1
		impEnd = -1
	}

	var walkExpr func(e ast.Expr)
	var walkStmt func(st ast.Stmt)

	walkStmt = func(st ast.Stmt) {
		if st == nil {
			return
		}
		switch v := st.(type) {
		case *ast.VarStmt:
			walkExpr(v.Value)
			if v.FallbackUnwind != nil {
				walkExpr(v.FallbackUnwind.Value)
			}
		case *ast.AssignStmt:
			for _, t := range v.UnpackTargets {
				walkExpr(t)
			}
			walkExpr(v.Target)
			walkExpr(v.Value)
		case *ast.UnwindStmt:
			walkExpr(v.Value)
		case *ast.ExprStmt:
			walkExpr(v.Expr)
		}
	}

	walkExpr = func(e ast.Expr) {
		if e == nil {
			return
		}
		sp := e.GetSpan()
		switch v := e.(type) {
		case *ast.BlockExpr:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			for _, st := range v.Stmts {
				walkStmt(st)
			}
			walkExpr(v.Tail)
		case *ast.StmtBodyExpr:
			walkStmt(v.Stmt)
		case *ast.IfExpr:
			if v.Then != nil {
				tsp := v.Then.GetSpan()
				if tsp.Start.Line > 0 && tsp.End.Line > tsp.Start.Line {
					addFold(sp.Start.Line-1, tsp.End.Line-1, "region")
				}
				walkExpr(v.Then)
			}
			walkExpr(v.Cond)
			walkExpr(v.Else)
		case *ast.ForExpr:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			walkExpr(v.CondOrIter)
			walkExpr(v.RangeEnd)
			walkExpr(v.Body)
			walkExpr(v.Else)
		case *ast.WhileExpr:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			walkExpr(v.CondOrIter)
			walkExpr(v.RangeEnd)
			walkExpr(v.Body)
			walkExpr(v.Else)
		case *ast.MatchExpr:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			for _, arm := range v.Arms {
				if arm.Span.Start.Line > 0 && arm.Span.End.Line > arm.Span.Start.Line {
					addFold(arm.Span.Start.Line-1, arm.Span.End.Line-1, "region")
				}
				walkExpr(arm.Body)
			}
		case *ast.FuncLit:
			walkExpr(v.Body)
		case *ast.StructLit:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			for _, f := range v.Fields {
				walkExpr(f.Value)
			}
		case *ast.ArrayLit:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ast.VecLit:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ast.MapLit:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			for _, ent := range v.Entries {
				walkExpr(ent.Key)
				walkExpr(ent.Value)
			}
		case *ast.CallExpr:
			if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
				addFold(sp.Start.Line-1, sp.End.Line-1, "region")
			}
			for _, a := range v.Args {
				walkExpr(a)
			}
		case *ast.BinaryExpr:
			walkExpr(v.Left)
			walkExpr(v.Right)
		}
	}

	for _, d := range af.Decls {
		sp := d.GetSpan()
		if imp, isImp := d.(*ast.ImportDecl); isImp {
			_ = imp
			line0 := sp.Start.Line - 1
			if impStart < 0 {
				impStart = line0
				impEnd = line0
			} else {
				impEnd = line0
			}
			continue
		}
		flushImports()
		if sp.Start.Line > 0 && sp.End.Line > sp.Start.Line {
			addFold(sp.Start.Line-1, sp.End.Line-1, "region")
		}
		switch decl := d.(type) {
		case *ast.MethodDecl:
			walkExpr(decl.Body)
		case *ast.VarDecl:
			walkExpr(decl.Value)
		}
	}
	flushImports()

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].StartLine != ranges[j].StartLine {
			return ranges[i].StartLine < ranges[j].StartLine
		}
		return ranges[i].EndLine < ranges[j].EndLine
	})
	return ranges
}

// -----------------------------------------------------------------------------
// Smart Selection Ranges (`textDocument/selectionRange`)
// -----------------------------------------------------------------------------

func (s *Server) computeSelectionRanges(filePath, src string, positions []Position) []*SelectionRange {
	rep := diag.NewReporter()
	af, _ := parser.ParseFileWithComments(filePath, src, rep)
	toks := lexCleanTokens(filePath, src)

	out := make([]*SelectionRange, 0, len(positions))
	for _, pos := range positions {
		off := LSPToOffset(src, pos)
		var spans []diag.Span

		// 1. Token under cursor
		for _, tk := range toks {
			if tk.Span.Start.Offset <= off && off <= tk.Span.End.Offset {
				spans = append(spans, tk.Span)
				break
			}
		}

		// 2. Collect enclosing AST spans
		if af != nil {
			spans = append(spans, collectEnclosingASTSpans(af, off)...)
		}

		// 3. Full document span
		if len(src) > 0 {
			spans = append(spans, diag.Span{
				Start: diag.Pos{File: filePath, Line: 1, Col: 1, Offset: 0},
				End:   diag.Pos{File: filePath, Line: 1, Col: 1, Offset: len(src)},
			})
		}

		// Deduplicate and sort strictly from smallest (innermost) to largest (outermost).
		sort.SliceStable(spans, func(i, j int) bool {
			li := spans[i].End.Offset - spans[i].Start.Offset
			lj := spans[j].End.Offset - spans[j].Start.Offset
			if li != lj {
				return li < lj
			}
			return spans[i].Start.Offset > spans[j].Start.Offset
		})

		var unique []Range
		seenRng := make(map[string]bool)
		for _, sp := range spans {
			if sp.End.Offset <= sp.Start.Offset {
				continue
			}
			r := Range{
				Start: OffsetToLSP(src, sp.Start.Offset),
				End:   OffsetToLSP(src, sp.End.Offset),
			}
			k := fmt.Sprintf("%d:%d-%d:%d", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
			if seenRng[k] {
				continue
			}
			seenRng[k] = true
			unique = append(unique, r)
		}

		var root *SelectionRange
		for i := len(unique) - 1; i >= 0; i-- {
			root = &SelectionRange{
				Range:  unique[i],
				Parent: root,
			}
		}
		if root == nil {
			p := OffsetToLSP(src, off)
			root = &SelectionRange{Range: Range{Start: p, End: p}}
		}
		out = append(out, root)
	}
	return out
}

func collectEnclosingASTSpans(af *ast.File, offset int) []diag.Span {
	var spans []diag.Span
	maybeAdd := func(sp diag.Span) {
		if sp.Start.Line > 0 && sp.Start.Offset <= offset && offset <= sp.End.Offset {
			spans = append(spans, sp)
		}
	}

	var walkType func(te ast.TypeExpr)
	var walkStmt func(st ast.Stmt)
	var walkExpr func(e ast.Expr)

	walkType = func(te ast.TypeExpr) {
		if te == nil || !spanContainsOffset(te.GetSpan(), offset) {
			return
		}
		maybeAdd(te.GetSpan())
		switch v := te.(type) {
		case *ast.NamedType:
			for _, ta := range v.TypeArgs {
				walkType(ta)
			}
		case *ast.OptionalType:
			walkType(v.Elem)
		case *ast.MutBorrowType:
			walkType(v.Elem)
		case *ast.OwnType:
			walkType(v.Elem)
		case *ast.VectorType:
			walkType(v.Elem)
		case *ast.StackArrayType:
			walkType(v.Elem)
		case *ast.SetType:
			walkType(v.Elem)
		case *ast.MapType:
			walkType(v.Key)
			walkType(v.Value)
		case *ast.TupleType:
			for _, el := range v.Elems {
				walkType(el)
			}
		case *ast.FuncType:
			for _, p := range v.Params {
				walkType(p.Type)
			}
			walkType(v.Ret)
		}
	}

	walkStmt = func(st ast.Stmt) {
		if st == nil || !spanContainsOffset(st.GetSpan(), offset) {
			return
		}
		maybeAdd(st.GetSpan())
		switch v := st.(type) {
		case *ast.VarStmt:
			walkType(v.Type)
			for _, uv := range v.UnpackVars {
				if spanContainsOffset(uv.Span, offset) {
					maybeAdd(uv.Span)
					walkType(uv.Type)
				}
			}
			walkExpr(v.Value)
			if v.FallbackUnwind != nil {
				walkExpr(v.FallbackUnwind.Value)
			}
		case *ast.AssignStmt:
			for _, t := range v.UnpackTargets {
				walkExpr(t)
			}
			walkExpr(v.Target)
			walkExpr(v.Value)
		case *ast.UnwindStmt:
			walkExpr(v.Value)
		case *ast.ExprStmt:
			walkExpr(v.Expr)
		}
	}

	walkExpr = func(e ast.Expr) {
		if e == nil || !spanContainsOffset(e.GetSpan(), offset) {
			return
		}
		maybeAdd(e.GetSpan())
		switch v := e.(type) {
		case *ast.BlockExpr:
			for _, st := range v.Stmts {
				walkStmt(st)
			}
			walkExpr(v.Tail)
		case *ast.StmtBodyExpr:
			walkStmt(v.Stmt)
		case *ast.IfExpr:
			walkExpr(v.Cond)
			walkExpr(v.Then)
			walkExpr(v.Else)
		case *ast.ForExpr:
			walkExpr(v.CondOrIter)
			walkExpr(v.RangeEnd)
			walkExpr(v.Body)
			walkExpr(v.Else)
		case *ast.WhileExpr:
			walkType(v.EvalType)
			walkExpr(v.CondOrIter)
			walkExpr(v.RangeEnd)
			walkExpr(v.Body)
			walkExpr(v.Else)
		case *ast.MatchExpr:
			for _, scr := range v.Scrutinees {
				walkExpr(scr)
			}
			for _, arm := range v.Arms {
				if spanContainsOffset(arm.Span, offset) {
					maybeAdd(arm.Span)
					for _, pat := range arm.AllPatterns() {
						switch p := pat.(type) {
						case *ast.ExprPattern:
							walkExpr(p.Expr)
						case *ast.ContainsPattern:
							walkExpr(p.Container)
						case *ast.MethodPattern:
							for _, a := range p.Args {
								walkExpr(a)
							}
						case *ast.VariantPattern:
							walkExpr(p.Target)
						}
					}
					walkExpr(arm.Body)
				}
			}
		case *ast.FuncLit:
			for _, p := range v.Params {
				if spanContainsOffset(p.Span, offset) {
					maybeAdd(p.Span)
					walkType(p.Type)
				}
			}
			walkType(v.Ret)
			walkExpr(v.Body)
		case *ast.CallExpr:
			walkExpr(v.Callee)
			for _, ta := range v.TypeArgs {
				walkType(ta)
			}
			for _, a := range v.Args {
				walkExpr(a)
			}
		case *ast.MemberExpr:
			walkExpr(v.Base)
		case *ast.StructLit:
			for _, ta := range v.TypeArgs {
				walkType(ta)
			}
			for _, f := range v.Fields {
				if spanContainsOffset(f.Span, offset) {
					maybeAdd(f.Span)
					walkExpr(f.Value)
				}
			}
		case *ast.BinaryExpr:
			walkExpr(v.Left)
			walkExpr(v.Right)
		case *ast.UnaryExpr:
			walkExpr(v.Operand)
		case *ast.UnsafeExpr:
			walkExpr(v.Expr)
		case *ast.IndexExpr:
			walkExpr(v.Base)
			walkExpr(v.Index)
		case *ast.SliceExpr:
			walkExpr(v.Base)
			walkExpr(v.Low)
			walkExpr(v.High)
		case *ast.RangeExpr:
			walkExpr(v.Low)
			walkExpr(v.High)
		case *ast.TupleLit:
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ast.ArrayLit:
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ast.VecLit:
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ast.SetLit:
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ast.MapLit:
			for _, ent := range v.Entries {
				walkExpr(ent.Key)
				walkExpr(ent.Value)
			}
		case *ast.InterpStringExpr:
			for _, sub := range v.Exprs {
				walkExpr(sub)
			}
		}
	}

	for _, d := range af.Decls {
		if !spanContainsOffset(d.GetSpan(), offset) {
			continue
		}
		maybeAdd(d.GetSpan())
		switch decl := d.(type) {
		case *ast.StructDecl:
			for _, f := range decl.Fields {
				if spanContainsOffset(f.Span, offset) {
					maybeAdd(f.Span)
					walkType(f.Type)
					walkExpr(f.Default)
				}
			}
		case *ast.EnumDecl:
			for _, ev := range decl.Variants {
				if spanContainsOffset(ev.Span, offset) {
					maybeAdd(ev.Span)
					walkType(ev.PayloadType)
				}
			}
		case *ast.InterfaceDecl:
			for _, m := range decl.Methods {
				if spanContainsOffset(m.Span, offset) {
					maybeAdd(m.Span)
					for _, p := range m.Params {
						if spanContainsOffset(p.Span, offset) {
							maybeAdd(p.Span)
							walkType(p.Type)
						}
					}
					walkType(m.Ret)
				}
			}
		case *ast.DistinctTypeDecl:
			walkType(decl.Base)
		case *ast.ExternFnDecl:
			for _, p := range decl.Params {
				if spanContainsOffset(p.Span, offset) {
					maybeAdd(p.Span)
					walkType(p.Type)
				}
			}
			walkType(decl.Ret)
		case *ast.MethodDecl:
			for _, p := range decl.Params {
				if spanContainsOffset(p.Span, offset) {
					maybeAdd(p.Span)
					walkType(p.Type)
				}
			}
			walkType(decl.Ret)
			walkExpr(decl.Body)
		case *ast.VarDecl:
			walkType(decl.Type)
			walkExpr(decl.Value)
		}
	}
	return spans
}
