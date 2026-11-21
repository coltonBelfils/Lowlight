package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// -----------------------------------------------------------------------------
// Hover (`textDocument/hover`)
// -----------------------------------------------------------------------------

func (s *Server) computeHover(filePath string, src string, pos Position) *Hover {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}

	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return nil
	}

	// 0. Check intra-doc links (`[`Symbol`]` or `[label][`Symbol`]`) inside `//` and `/* ... */` comments
	if target, docRng, isLink := findDocLinkAtOffset(filePath, src, offset); isLink {
		idx := s.getDocIndex(prog, mod.Path)
		encType := enclosingTypeAtOffset(mod.File, offset)
		if idx != nil {
			entry, isBuiltin := idx.ResolveLink(target, mod.Path, encType)
			if md := s.hoverForDocEntry(prog, entry, isBuiltin, target, mod.Path); md != "" {
				return &Hover{
					Contents: MarkupContent{Kind: "markdown", Value: md},
					Range:    &docRng,
				}
			}
		}
	}

	tok, ok := s.findTokenAtOffset(filePath, src, offset)
	if !ok {
		return nil
	}
	rng := ExactSpanToLSP(src, tok.Span)

	// 1. Check AST context (calls, member accesses, local declarations, parameters, type annotations)
	if md := s.hoverFromAST(prog, mod, offset, tok); md != "" {
		return &Hover{
			Contents: MarkupContent{Kind: "markdown", Value: md},
			Range:    &rng,
		}
	}

	// 2. Check module-level or imported types (`Struct`, `Enum`, `Interface`, `Distinct`, `GenericStruct`)
	word := tok.Text
	if nom := lookupNominalType(mod, "", word); nom != nil {
		var ifcTargets []sema.ImplementationTarget
		if _, isIfc := nom.(*types.Interface); isIfc {
			ifcTargets = s.loadedInterfaceImplementingTargets(nom.String(), prog, mod)
		}
		if md := s.formatNominalTypeHover(prog, mod, nom, ifcTargets); md != "" {
			return &Hover{
				Contents: MarkupContent{Kind: "markdown", Value: md},
				Range:    &rng,
			}
		}
	}
	if gst := lookupGenStruct(mod, "", word); gst != nil {
		return &Hover{
			Contents: MarkupContent{Kind: "markdown", Value: s.formatStructDeclHover(prog, mod, gst.Decl, nil)},
			Range:    &rng,
		}
	}
	if gen := lookupGenEnum(mod, "", word); gen != nil {
		return &Hover{
			Contents: MarkupContent{Kind: "markdown", Value: s.formatEnumDeclHover(prog, mod, gen.Decl)},
			Range:    &rng,
		}
	}
	if gi := lookupGenInterface(mod, "", word); gi != nil {
		return &Hover{
			Contents: MarkupContent{Kind: "markdown", Value: s.formatInterfaceDeclHover(prog, gi.Decl)},
			Range:    &rng,
		}
	}

	// 3. Check top-level functions, generic functions, constants & module aliases
	if sym := lookupTopSym(mod, word); sym != nil {
		return &Hover{
			Contents: MarkupContent{Kind: "markdown", Value: s.formatSymbolHover(prog, mod, sym)},
			Range:    &rng,
		}
	}
	if gfn := lookupTopGenFunc(mod, word); gfn != nil {
		base := fmt.Sprintf("```lowlight\nbind %s%s\n```\nGeneric function template.", gfn.Name, formatGenericVarDeclSig(gfn.VarDecl))
		var sp diag.Span
		var doc string
		if gfn.VarDecl != nil {
			sp = gfn.VarDecl.Span
			doc = gfn.VarDecl.Doc
		}
		gModPath := mod.Path
		if gfn.Mod != nil && gfn.Mod.Path != "" {
			gModPath = gfn.Mod.Path
		}
		rendered := s.renderDocMarkdown(prog, gModPath, "", doc, sp.Start.File, sp.Start.Line, true)
		return &Hover{
			Contents: MarkupContent{
				Kind:  "markdown",
				Value: appendRenderedDoc(base, rendered),
			},
			Range: &rng,
		}
	}
	if targetMod, isMod := mod.Aliased[word]; isMod {
		base := fmt.Sprintf("```lowlight\nimport %q as %s\n```\nResolved module: `%s`", filepath.Base(targetMod.Path), word, targetMod.Path)
		rendered := s.renderDocMarkdown(prog, targetMod.Path, "", targetMod.Doc, targetMod.Path, 1, true)
		return &Hover{
			Contents: MarkupContent{
				Kind:  "markdown",
				Value: appendRenderedDoc(base, rendered),
			},
			Range: &rng,
		}
	}

	// 4. Contextual keywords (`define Name result [...]`, a result chain's `default panic`)
	if md := contextualKeywordHover(src, tok); md != "" {
		return &Hover{Contents: MarkupContent{Kind: "markdown", Value: md}, Range: &rng}
	}

	// 4b. Built-in functions, intrinsics, built-in types, keywords & attributes
	if (word == "test" || word == "test_only" || word == "track_caller") && tok.Span.Start.Offset > 0 && src[tok.Span.Start.Offset-1] == '@' {
		if word == "test" {
			return &Hover{
				Contents: MarkupContent{
					Kind:  "markdown",
					Value: "### `@test` / `@test(\"description\")`\nMarks a top-level function as an automated test case.\n\n- **Signature**: `() void` (takes 0 arguments, returns void)\n- **Run with**: `lowlight test <file.ll>`\n- **Features**: Automatic heap allocation leak checking, panic/assertion recovery, execution timer.",
				},
				Range: &rng,
			}
		}
		if word == "track_caller" {
			return &Hover{
				Contents: MarkupContent{
					Kind:  "markdown",
					Value: "### `@track_caller`\nTracks the caller's source location (file, line, column). Any assertions, panics, or nested `@track_caller` helpers report the outer caller's call site.",
				},
				Range: &rng,
			}
		}
		return &Hover{
			Contents: MarkupContent{
				Kind:  "markdown",
				Value: "### `@test_only`\nMarks helper functions, mock structs, enums, or variables intended exclusively for test code.\n\n- **Compile-time isolated**: Production code cannot reference `@test_only` symbols.\n- **Stripped**: Automatically omitted from production binaries and `--release` builds.",
			},
			Range: &rng,
		}
	}
	if bf := LookupBuiltinFunc(word); bf != nil {
		badge := "Built-in function"
		if bf.IsIntrinsic {
			badge = "Compiler intrinsic"
			if bf.NeedsUnsafe {
				badge += " (requires `unsafe`)"
			}
		}
		return &Hover{
			Contents: MarkupContent{
				Kind:  "markdown",
				Value: fmt.Sprintf("```lowlight\n%s\n```\n*%s* — %s", bf.Signature, badge, bf.Summary),
			},
			Range: &rng,
		}
	}
	if bt := LookupBuiltinTypeDoc(word); bt != nil {
		return &Hover{
			Contents: MarkupContent{
				Kind:  "markdown",
				Value: fmt.Sprintf("```lowlight\ntype %s // %s\n```\n%s", bt.Name, bt.Detail, bt.Summary),
			},
			Range: &rng,
		}
	}
	if kw := LookupKeywordDoc(word); kw != nil {
		return &Hover{
			Contents: MarkupContent{
				Kind:  "markdown",
				Value: fmt.Sprintf("```lowlight\n%s // %s\n```\n%s", kw.Word, kw.Detail, kw.Summary),
			},
			Range: &rng,
		}
	}

	return nil
}

func (s *Server) hoverFromAST(prog *sema.CheckedProgram, mod *sema.Module, offset int, tok token.Token) string {
	for _, d := range mod.File.Decls {
		if !spanContainsOffset(d.GetSpan(), offset) {
			continue
		}
		switch decl := d.(type) {
		case *ast.ImportDecl:
			resolved := resolveImportFromMod(prog, mod, decl.Path)
			if resolved != "" {
				base := fmt.Sprintf("```lowlight\nimport %q\n```\nModule path: `%s`", decl.Path, resolved)
				for _, m := range prog.Modules {
					if m != nil && m.Path == resolved {
						rendered := s.renderDocMarkdown(prog, m.Path, "", m.Doc, m.Path, 1, true)
						return appendRenderedDoc(base, rendered)
					}
				}
				return base
			}
		case *ast.StructDecl:
			for _, f := range decl.Fields {
				if f.Type != nil && spanContainsOffset(f.Type.GetSpan(), offset) {
					if md := s.hoverInTypeExpr(prog, mod, f.Type, offset, tok); md != "" {
						return md
					}
				}
				if spanContainsOffset(f.Span, offset) && tok.Text == f.Name {
					if st, ok := mod.Types[decl.Name].(*types.Struct); ok {
						if sf, _ := st.FieldByName(f.Name); sf != nil {
							return s.formatStructFieldHover(prog, mod, st, sf, sf.Type)
						}
					}
					base := fmt.Sprintf("```lowlight\n%s.%s: %s\n```", decl.Name, f.Name, formatTypeExpr(f.Type))
					rendered := s.renderDocMarkdown(prog, mod.Path, decl.Name, f.Doc, f.Span.Start.File, f.Span.Start.Line, true)
					return appendRenderedDoc(base, rendered)
				}
			}
		case *ast.EnumDecl:
			en, _ := mod.Types[decl.Name].(*types.Enum)
			for _, v := range decl.Variants {
				if v.PayloadType != nil && spanContainsOffset(v.PayloadType.GetSpan(), offset) {
					if md := s.hoverInTypeExpr(prog, mod, v.PayloadType, offset, tok); md != "" {
						return md
					}
				}
				if spanContainsOffset(v.Span, offset) && tok.Text == v.Name {
					if en != nil {
						if ev, ok2 := en.VariantByName(v.Name); ok2 {
							return s.formatEnumVariantHover(prog, mod, en, ev)
						}
					}
					base := fmt.Sprintf("```lowlight\n%s.%s\n```", decl.Name, v.Name)
					rendered := s.renderDocMarkdown(prog, mod.Path, decl.Name, v.Doc, v.Span.Start.File, v.Span.Start.Line, true)
					return appendRenderedDoc(base, rendered)
				}
			}
		case *ast.VarDecl:
			if decl.Type != nil && spanContainsOffset(decl.Type.GetSpan(), offset) {
				if md := s.hoverInTypeExpr(prog, mod, decl.Type, offset, tok); md != "" {
					return md
				}
			}
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				for _, p := range fl.Params {
					if p.Type != nil && spanContainsOffset(p.Type.GetSpan(), offset) {
						if md := s.hoverInTypeExpr(prog, mod, p.Type, offset, tok); md != "" {
							return md
						}
					}
				}
				if fl.Ret != nil && spanContainsOffset(fl.Ret.GetSpan(), offset) {
					if md := s.hoverInTypeExpr(prog, mod, fl.Ret, offset, tok); md != "" {
						return md
					}
				}
				fi := findFuncInstanceForDecl(prog, mod, decl.Span)
				if fi != nil {
					for i, p := range fl.Params {
						if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i < len(fi.ParamSyms) {
							return s.formatSymbolHover(prog, mod, fi.ParamSyms[i])
						}
					}
				}
				var bm *sema.BodyMaps
				if fi != nil {
					bm = fi.BodyMaps
				}
				if md := s.hoverInExpr(prog, mod, bm, fl.Body, offset, tok); md != "" {
					return md
				}
			} else if decl.Value != nil {
				if md := s.hoverInExpr(prog, mod, prog.TopBodyMaps, decl.Value, offset, tok); md != "" {
					return md
				}
			}
		case *ast.MethodDecl:
			for _, p := range decl.Params {
				if p.Type != nil && spanContainsOffset(p.Type.GetSpan(), offset) {
					if md := s.hoverInTypeExpr(prog, mod, p.Type, offset, tok); md != "" {
						return md
					}
				}
			}
			if decl.Ret != nil && spanContainsOffset(decl.Ret.GetSpan(), offset) {
				if md := s.hoverInTypeExpr(prog, mod, decl.Ret, offset, tok); md != "" {
					return md
				}
			}
			if tok.Text == decl.Name && (decl.Body == nil || offset < decl.Body.GetSpan().Start.Offset) {
				fi := findFuncInstanceForDecl(prog, mod, decl.Span)
				if fi != nil {
					base := fmt.Sprintf("```lowlight\nbind %s = %s\n```", fi.Name, formatFullFuncSig(fi))
					doc := fi.Doc
					if doc == "" {
						doc = decl.Doc
					}
					rendered := s.renderDocMarkdown(prog, mod.Path, decl.RecvType, doc, decl.Span.Start.File, decl.Span.Start.Line, true)
					return appendRenderedDoc(base, rendered)
				}
				base := fmt.Sprintf("```lowlight\nbind %s.%s = %s\n```", decl.RecvType, decl.Name, formatGenericMethodDeclSig(decl, nil, false))
				rendered := s.renderDocMarkdown(prog, mod.Path, decl.RecvType, decl.Doc, decl.Span.Start.File, decl.Span.Start.Line, true)
				return appendRenderedDoc(base, rendered)
			}
			fi := findFuncInstanceForDecl(prog, mod, decl.Span)
			if fi != nil {
				pOffset := 0
				if !fi.IsStaticAssoc {
					pOffset = 1
				}
				for i, p := range decl.Params {
					if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i+pOffset < len(fi.ParamSyms) {
						return s.formatSymbolHover(prog, mod, fi.ParamSyms[i+pOffset])
					}
				}
			}
			var bm *sema.BodyMaps
			if fi != nil {
				bm = fi.BodyMaps
			}
			if md := s.hoverInExpr(prog, mod, bm, decl.Body, offset, tok); md != "" {
				return md
			}
		}
	}
	return ""
}

func (s *Server) hoverInTypeExpr(prog *sema.CheckedProgram, mod *sema.Module, te ast.TypeExpr, offset int, tok token.Token) string {
	if te == nil || !spanContainsOffset(te.GetSpan(), offset) {
		return ""
	}
	switch v := te.(type) {
	case *ast.NamedType:
		for _, ta := range v.TypeArgs {
			if md := s.hoverInTypeExpr(prog, mod, ta, offset, tok); md != "" {
				return md
			}
		}
		if tok.Text == v.Name {
			if nom := lookupNominalType(mod, v.Module, v.Name); nom != nil {
				var ifcTargets []sema.ImplementationTarget
				if _, isIfc := nom.(*types.Interface); isIfc {
					ifcTargets = s.loadedInterfaceImplementingTargets(nom.String(), prog, mod)
				}
				return s.formatNominalTypeHover(prog, mod, nom, ifcTargets)
			}
			if gst := lookupGenStruct(mod, v.Module, v.Name); gst != nil {
				return s.formatStructDeclHover(prog, mod, gst.Decl, nil)
			}
			if gen := lookupGenEnum(mod, v.Module, v.Name); gen != nil {
				return s.formatEnumDeclHover(prog, mod, gen.Decl)
			}
			if gi := lookupGenInterface(mod, v.Module, v.Name); gi != nil {
				return s.formatInterfaceDeclHover(prog, gi.Decl)
			}
		}
	case *ast.OptionalType:
		return s.hoverInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.MutBorrowType:
		return s.hoverInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.OwnType:
		return s.hoverInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.VectorType:
		return s.hoverInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.StackArrayType:
		return s.hoverInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.TupleType:
		for _, el := range v.Elems {
			if md := s.hoverInTypeExpr(prog, mod, el, offset, tok); md != "" {
				return md
			}
		}
	case *ast.SetType:
		return s.hoverInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.MapType:
		if md := s.hoverInTypeExpr(prog, mod, v.Key, offset, tok); md != "" {
			return md
		}
		return s.hoverInTypeExpr(prog, mod, v.Value, offset, tok)
	}
	return ""
}

func (s *Server) hoverInExpr(prog *sema.CheckedProgram, mod *sema.Module, bm *sema.BodyMaps, e ast.Expr, offset int, tok token.Token) string {
	if e == nil || !spanContainsOffset(e.GetSpan(), offset) {
		return ""
	}
	switch v := e.(type) {
	case *ast.BlockExpr:
		for _, st := range v.Stmts {
			if spanContainsOffset(st.GetSpan(), offset) {
				return s.hoverInStmt(prog, mod, bm, st, offset, tok)
			}
		}
		return s.hoverInExpr(prog, mod, bm, v.Tail, offset, tok)

	case *ast.StmtBodyExpr:
		return s.hoverInStmt(prog, mod, bm, v.Stmt, offset, tok)

	case *ast.IfExpr:
		if v.CondKind == ast.IfCondOptVar && tok.Text == v.OptVar && (v.Then == nil || offset < v.Then.GetSpan().Start.Offset) {
			if bm != nil {
				if orig := bm.IfOriginSyms[v]; orig != nil {
					return s.formatSymbolHover(prog, mod, orig)
				}
			}
		}
		if md := s.hoverInExpr(prog, mod, bm, v.Cond, offset, tok); md != "" {
			return md
		}
		if v.Then != nil && spanContainsOffset(v.Then.GetSpan(), offset) {
			if md := s.hoverInExpr(prog, mod, bm, v.Then, offset, tok); md != "" {
				return md
			}
		}
		return s.hoverInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.ForExpr:
		if md := s.hoverInExpr(prog, mod, bm, v.CondOrIter, offset, tok); md != "" {
			return md
		}
		if md := s.hoverInExpr(prog, mod, bm, v.RangeEnd, offset, tok); md != "" {
			return md
		}
		if v.Body != nil && spanContainsOffset(v.Body.GetSpan(), offset) {
			return s.hoverInExpr(prog, mod, bm, v.Body, offset, tok)
		}
		return s.hoverInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.WhileExpr:
		if md := s.hoverInExpr(prog, mod, bm, v.CondOrIter, offset, tok); md != "" {
			return md
		}
		if md := s.hoverInExpr(prog, mod, bm, v.RangeEnd, offset, tok); md != "" {
			return md
		}
		if v.Body != nil && spanContainsOffset(v.Body.GetSpan(), offset) {
			return s.hoverInExpr(prog, mod, bm, v.Body, offset, tok)
		}
		return s.hoverInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.MatchExpr:
		for _, scr := range v.Scrutinees {
			if md := s.hoverInExpr(prog, mod, bm, scr, offset, tok); md != "" {
				return md
			}
		}
		for _, arm := range v.Arms {
			if arm.Synthetic {
				continue
			}
			for _, pat := range arm.AllPatterns() {
				if ep, ok := pat.(*ast.ExprPattern); ok && spanContainsOffset(ep.Span, offset) {
					if md := s.hoverInExpr(prog, mod, bm, ep.Expr, offset, tok); md != "" {
						return md
					}
				}
				if cp, ok := pat.(*ast.ContainsPattern); ok && spanContainsOffset(cp.Span, offset) {
					if md := s.hoverInExpr(prog, mod, bm, cp.Container, offset, tok); md != "" {
						return md
					}
				}
				if mp, ok := pat.(*ast.MethodPattern); ok && spanContainsOffset(mp.Span, offset) {
					if bm != nil {
						if mpInfo := bm.MethodPatterns[mp]; mpInfo != nil && mpInfo.Call != nil {
							if md := s.hoverInExpr(prog, mod, bm, mpInfo.Call, offset, tok); md != "" {
								return md
							}
						}
					}
					for _, a := range mp.Args {
						if md := s.hoverInExpr(prog, mod, bm, a, offset, tok); md != "" {
							return md
						}
					}
				}
				if vp, ok := pat.(*ast.VariantPattern); ok && spanContainsOffset(vp.Span, offset) {
					if bm != nil {
						syms := bm.PatternSyms[vp]
						for i, b := range vp.Bindings {
							if spanContainsOffset(b.Span, offset) && tok.Text == b.BindName && i < len(syms) && syms[i] != nil {
								return s.formatSymbolHover(prog, mod, syms[i])
							}
						}
					}
					if md := s.hoverInExpr(prog, mod, bm, vp.Target, offset, tok); md != "" {
						return md
					}
				}
			}
			if spanContainsOffset(arm.Body.GetSpan(), offset) {
				return s.hoverInExpr(prog, mod, bm, arm.Body, offset, tok)
			}
		}

	case *ast.FuncLit:
		lbm := bm
		for _, li := range prog.Lambdas {
			if li.Lit == v {
				lbm = li.BodyMaps
				for i, p := range v.Params {
					if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i < len(li.ParamSyms) {
						return s.formatSymbolHover(prog, mod, li.ParamSyms[i])
					}
				}
				break
			}
		}
		return s.hoverInExpr(prog, mod, lbm, v.Body, offset, tok)

	case *ast.CallExpr:
		for _, a := range v.Args {
			if spanContainsOffset(a.GetSpan(), offset) {
				return s.hoverInExpr(prog, mod, bm, a, offset, tok)
			}
		}
		for _, ta := range v.TypeArgs {
			if spanContainsOffset(ta.GetSpan(), offset) {
				if md := s.hoverInTypeExpr(prog, mod, ta, offset, tok); md != "" {
					return md
				}
			}
		}
		if spanContainsOffset(v.Callee.GetSpan(), offset) {
			if mem, isMem := v.Callee.(*ast.MemberExpr); isMem && spanContainsOffset(mem.Base.GetSpan(), offset) {
				return s.hoverInExpr(prog, mod, bm, mem.Base, offset, tok)
			}
			if inf := exprInfoFromBM(prog, bm, v); inf != nil {
				if md := s.formatCallHover(prog, mod, bm, v, inf); md != "" {
					return md
				}
			}
			return s.hoverInExpr(prog, mod, bm, v.Callee, offset, tok)
		}

	case *ast.MemberExpr:
		if spanContainsOffset(v.Base.GetSpan(), offset) {
			return s.hoverInExpr(prog, mod, bm, v.Base, offset, tok)
		}
		inf := exprInfoFromBM(prog, bm, v)
		if inf != nil {
			if inf.EnumVariant != nil && inf.EnumType != nil {
				return s.formatEnumVariantHover(prog, mod, inf.EnumType, inf.EnumVariant)
			}
			if inf.Sym != nil {
				return s.formatSymbolHover(prog, mod, inf.Sym)
			}
			if baseInf := exprInfoFromBM(prog, bm, v.Base); baseInf != nil && baseInf.Type != nil {
				recv := unwrapOptAndRef(baseInf.Type)
				if st, isSt := recv.(*types.Struct); isSt {
					if sf, _ := st.FieldByName(v.Member); sf != nil {
						return s.formatStructFieldHover(prog, mod, st, sf, inf.Type)
					}
				}
				if tup, isTup := recv.(*types.Tuple); isTup {
					if idx, ok := sema.IsCanonicalTupleIndex(v.Member); ok && idx >= 0 && idx < len(tup.Elems) {
						return fmt.Sprintf("```lowlight\n(tuple element) %s.%s: %s\n```", tup.String(), v.Member, inf.Type.String())
					}
				}
				if dist, isDist := recv.(*types.Distinct); isDist {
					if dtTup, isTup := dist.Base.(*types.Tuple); isTup {
						if idx, ok := sema.IsCanonicalTupleIndex(v.Member); ok && idx >= 0 && idx < len(dtTup.Elems) {
							return fmt.Sprintf("```lowlight\n(tuple element) %s.%s: %s\n```", dist.Name, v.Member, inf.Type.String())
						}
					}
				}
			}
		}

	case *ast.StructLit:
		for _, f := range v.Fields {
			if spanContainsOffset(f.Value.GetSpan(), offset) {
				return s.hoverInExpr(prog, mod, bm, f.Value, offset, tok)
			}
			if spanContainsOffset(f.Span, offset) && tok.Text == f.Name {
				if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.StructType != nil {
					if sf, _ := inf.StructType.FieldByName(f.Name); sf != nil {
						return s.formatStructFieldHover(prog, mod, inf.StructType, sf, sf.Type)
					}
				}
			}
		}
		if inf := exprInfoFromBM(prog, bm, v); inf != nil {
			if inf.StructType != nil && tok.Text == v.TypeName {
				return s.formatNominalTypeHover(prog, mod, inf.StructType, nil)
			}
		}

	case *ast.IdentExpr:
		if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.Sym != nil {
			return s.formatSymbolHover(prog, mod, inf.Sym)
		}

	case *ast.BinaryExpr:
		if md := s.hoverInExpr(prog, mod, bm, v.Left, offset, tok); md != "" {
			return md
		}
		return s.hoverInExpr(prog, mod, bm, v.Right, offset, tok)

	case *ast.UnaryExpr:
		return s.hoverInExpr(prog, mod, bm, v.Operand, offset, tok)

	case *ast.UnsafeExpr:
		return s.hoverInExpr(prog, mod, bm, v.Expr, offset, tok)

	case *ast.IndexExpr:
		if md := s.hoverInExpr(prog, mod, bm, v.Base, offset, tok); md != "" {
			return md
		}
		return s.hoverInExpr(prog, mod, bm, v.Index, offset, tok)

	case *ast.SliceExpr:
		if md := s.hoverInExpr(prog, mod, bm, v.Base, offset, tok); md != "" {
			return md
		}
		if md := s.hoverInExpr(prog, mod, bm, v.Low, offset, tok); md != "" {
			return md
		}
		return s.hoverInExpr(prog, mod, bm, v.High, offset, tok)

	case *ast.RangeExpr:
		if md := s.hoverInExpr(prog, mod, bm, v.Low, offset, tok); md != "" {
			return md
		}
		return s.hoverInExpr(prog, mod, bm, v.High, offset, tok)

	case *ast.TupleLit:
		for _, el := range v.Elems {
			if md := s.hoverInExpr(prog, mod, bm, el, offset, tok); md != "" {
				return md
			}
		}

	case *ast.ArrayLit:
		for _, el := range v.Elems {
			if md := s.hoverInExpr(prog, mod, bm, el, offset, tok); md != "" {
				return md
			}
		}

	case *ast.VecLit:
		for _, el := range v.Elems {
			if md := s.hoverInExpr(prog, mod, bm, el, offset, tok); md != "" {
				return md
			}
		}

	case *ast.SetLit:
		for _, el := range v.Elems {
			if md := s.hoverInExpr(prog, mod, bm, el, offset, tok); md != "" {
				return md
			}
		}

	case *ast.MapLit:
		for _, ent := range v.Entries {
			if md := s.hoverInExpr(prog, mod, bm, ent.Key, offset, tok); md != "" {
				return md
			}
			if md := s.hoverInExpr(prog, mod, bm, ent.Value, offset, tok); md != "" {
				return md
			}
		}

	case *ast.InterpStringExpr:
		for _, sub := range v.Exprs {
			if md := s.hoverInExpr(prog, mod, bm, sub, offset, tok); md != "" {
				return md
			}
		}
	}
	return ""
}

func (s *Server) hoverInStmt(prog *sema.CheckedProgram, mod *sema.Module, bm *sema.BodyMaps, st ast.Stmt, offset int, tok token.Token) string {
	switch vs := st.(type) {
	case *ast.VarStmt:
		if vs.Type != nil && spanContainsOffset(vs.Type.GetSpan(), offset) {
			if md := s.hoverInTypeExpr(prog, mod, vs.Type, offset, tok); md != "" {
				return md
			}
		}
		if spanContainsOffset(vs.Value.GetSpan(), offset) {
			return s.hoverInExpr(prog, mod, bm, vs.Value, offset, tok)
		}
		if vs.FallbackUnwind != nil && vs.FallbackUnwind.Value != nil && spanContainsOffset(vs.FallbackUnwind.Value.GetSpan(), offset) {
			return s.hoverInExpr(prog, mod, bm, vs.FallbackUnwind.Value, offset, tok)
		}
		if vs.IsUnpack && bm != nil {
			syms := bm.UnpackVarSymbols[vs]
			for i, uv := range vs.UnpackVars {
				if uv.Type != nil && spanContainsOffset(uv.Type.GetSpan(), offset) {
					if md := s.hoverInTypeExpr(prog, mod, uv.Type, offset, tok); md != "" {
						return md
					}
				}
				if tok.Text == uv.Name && i < len(syms) && syms[i] != nil {
					return s.formatSymbolHover(prog, mod, syms[i])
				}
			}
		}
		if tok.Text == vs.Name && bm != nil {
			if sym := bm.VarSymbols[vs]; sym != nil {
				return s.formatSymbolHover(prog, mod, sym)
			}
		}
	case *ast.AssignStmt:
		if vs.IsUnpack {
			for _, tgt := range vs.UnpackTargets {
				if spanContainsOffset(tgt.GetSpan(), offset) {
					return s.hoverInExpr(prog, mod, bm, tgt, offset, tok)
				}
			}
			return s.hoverInExpr(prog, mod, bm, vs.Value, offset, tok)
		}
		if spanContainsOffset(vs.Target.GetSpan(), offset) {
			return s.hoverInExpr(prog, mod, bm, vs.Target, offset, tok)
		}
		return s.hoverInExpr(prog, mod, bm, vs.Value, offset, tok)
	case *ast.UnwindStmt:
		return s.hoverInExpr(prog, mod, bm, vs.Value, offset, tok)
	case *ast.ExprStmt:
		return s.hoverInExpr(prog, mod, bm, vs.Expr, offset, tok)
	}
	return ""
}

func (s *Server) formatSymbolHover(prog *sema.CheckedProgram, mod *sema.Module, sym *sema.Symbol) string {
	if sym == nil {
		return ""
	}
	modPath := ""
	if mod != nil {
		modPath = mod.Path
	}
	if sym.FuncInst != nil {
		base := fmt.Sprintf("```lowlight\nbind %s = %s\n```", sym.FuncInst.Name, formatFullFuncSig(sym.FuncInst))
		doc := sym.FuncInst.Doc
		if doc == "" {
			doc = sym.Doc
		}
		fModPath := modPath
		if sym.FuncInst.Mod != nil && sym.FuncInst.Mod.Path != "" {
			fModPath = sym.FuncInst.Mod.Path
		}
		curType := ""
		if sym.FuncInst.IsMethod {
			if dot := strings.IndexByte(sym.FuncInst.Name, '.'); dot > 0 {
				curType = sym.FuncInst.Name[:dot]
			}
		}
		sp := sym.FuncInst.DeclSpan
		if sp.Start.File == "" {
			sp = sym.Span
		}
		rendered := s.renderDocMarkdown(prog, fModPath, curType, doc, sp.Start.File, sp.Start.Line, true)
		return appendRenderedDoc(base, rendered)
	}
	if sym.ExternFn != nil {
		base := fmt.Sprintf("```lowlight\nextern %s: %s\n```\n*C11 ABI function* (requires `unsafe`)", sym.Name, sym.ExternFn.Sig.String())
		doc := sym.ExternFn.Doc
		if doc == "" {
			doc = sym.Doc
		}
		sp := sym.ExternFn.Span
		if sp.Start.File == "" {
			sp = sym.Span
		}
		rendered := s.renderDocMarkdown(prog, modPath, "", doc, sp.Start.File, sp.Start.Line, true)
		return appendRenderedDoc(base, rendered)
	}
	tyStr := "<unknown>"
	ownershipNote := ""
	if sym.Type != nil && sym.Type != types.TypeInvalid {
		tyStr = sym.Type.String()
		if sym.Type.IsPlain() {
			ownershipNote = "Plain value type (copied implicitly)"
		} else {
			ownershipNote = "Resource type (single owner, moved & automatically dropped)"
		}
	}
	kw := "let"
	if sym.IsBind {
		kw = "bind"
	}
	// A parameter hovers as `(name: T)` with its `- `name`: ...` line from the function's doc.
	if fi, paramDoc := paramOwner(prog, sym); fi != nil {
		var notes []string
		switch sym.BindKind {
		case sema.BindReadBorrow:
			notes = append(notes, "read-only borrow parameter")
		case sema.BindMutBorrow:
			notes = append(notes, "exclusive mutable borrow parameter (`&`)")
		default:
			notes = append(notes, "owned parameter")
		}
		if ownershipNote != "" {
			notes = append(notes, ownershipNote)
		}
		base := fmt.Sprintf("```lowlight\n(%s: %s)\n```\nParameter of `%s` · %s", sym.Name, tyStr, fi.Name, strings.Join(notes, " · "))
		fModPath := modPath
		if fi.Mod != nil && fi.Mod.Path != "" {
			fModPath = fi.Mod.Path
		}
		return appendRenderedDoc(base, s.renderDocMarkdown(prog, fModPath, "", paramDoc, "", 0, false))
	}
	modeNote := ""
	switch sym.BindKind {
	case sema.BindReadBorrow:
		modeNote = "read-only borrow"
	case sema.BindMutBorrow:
		modeNote = "exclusive mutable borrow (`&`)"
	default:
		if sym.IsConstInt {
			modeNote = fmt.Sprintf("compile-time constant = %d", sym.ConstIntVal)
		} else {
			modeNote = "owned"
		}
	}
	var notes []string
	if modeNote != "" {
		notes = append(notes, modeNote)
	}
	if ownershipNote != "" {
		notes = append(notes, ownershipNote)
	}
	base := fmt.Sprintf("```lowlight\n%s %s: %s\n```\n%s", kw, sym.Name, tyStr, strings.Join(notes, " · "))
	showSrc := sym.IsTopLevel
	rendered := s.renderDocMarkdown(prog, modPath, "", sym.Doc, sym.Span.Start.File, sym.Span.Start.Line, showSrc)
	return appendRenderedDoc(base, rendered)
}

// paramOwner returns the function whose parameter `sym` is, with that parameter's documentation
// (the `- `name`: ...` line of the function's doc comment).
func paramOwner(prog *sema.CheckedProgram, sym *sema.Symbol) (*sema.FuncInstance, string) {
	if prog == nil || sym == nil || sym.IsTopLevel {
		return nil, ""
	}
	for _, fi := range prog.Funcs {
		for _, ps := range fi.ParamSyms {
			if ps == sym {
				return fi, parseParamDocs(fi.Doc)[sym.Name]
			}
		}
	}
	return nil, ""
}

func (s *Server) formatCallHover(prog *sema.CheckedProgram, mod *sema.Module, bm *sema.BodyMaps, call *ast.CallExpr, inf *sema.ExprInfo) string {
	modPath := ""
	if mod != nil {
		modPath = mod.Path
	}
	switch inf.CallKind {
	case sema.CallDirectFunc:
		if inf.TargetFunc != nil {
			base := fmt.Sprintf("```lowlight\nbind %s = %s\n```", inf.TargetFunc.Name, formatFullFuncSig(inf.TargetFunc))
			fModPath := modPath
			if inf.TargetFunc.Mod != nil && inf.TargetFunc.Mod.Path != "" {
				fModPath = inf.TargetFunc.Mod.Path
			}
			curType := ""
			if inf.TargetFunc.IsMethod {
				if dot := strings.IndexByte(inf.TargetFunc.Name, '.'); dot > 0 {
					curType = inf.TargetFunc.Name[:dot]
				}
			}
			sp := inf.TargetFunc.DeclSpan
			rendered := s.renderDocMarkdown(prog, fModPath, curType, inf.TargetFunc.Doc, sp.Start.File, sp.Start.Line, true)
			return appendRenderedDoc(base, rendered)
		}
	case sema.CallExternC:
		if inf.TargetExtern != nil {
			base := fmt.Sprintf("```lowlight\nextern %s: %s\n```\n*C11 ABI function* (requires `unsafe`)", inf.TargetExtern.Name, inf.TargetExtern.Sig.String())
			sp := inf.TargetExtern.Span
			rendered := s.renderDocMarkdown(prog, modPath, "", inf.TargetExtern.Doc, sp.Start.File, sp.Start.Line, true)
			return appendRenderedDoc(base, rendered)
		}
	case sema.CallInterfaceMethod:
		if inf.InterfaceMeth != nil {
			base := fmt.Sprintf("```lowlight\n%s: %s\n```\n*Dynamic interface method dispatch*",
				inf.InterfaceMeth.Name, formatInterfaceMethodSig(*inf.InterfaceMeth, inf.IsOptChain))
			var sp diag.Span
			if mem, ok := call.Callee.(*ast.MemberExpr); ok {
				if bInf := exprInfoFromBM(prog, bm, mem.Base); bInf != nil && bInf.Type != nil {
					if ifc, ok2 := unwrapOptAndRef(bInf.Type).(*types.Interface); ok2 {
						sp = s.typeSpan(prog, ifc)
					}
				}
			}
			rendered := s.renderDocMarkdown(prog, modPath, "", inf.InterfaceMeth.Doc, sp.Start.File, sp.Start.Line, sp.Start.File != "")
			return appendRenderedDoc(base, rendered)
		}
	case sema.CallBuiltinFunc, sema.CallIntrinsic:
		if bf := LookupBuiltinFunc(inf.BuiltinName); bf != nil {
			return fmt.Sprintf("```lowlight\n%s\n```\n%s", bf.Signature, bf.Summary)
		}
	case sema.CallBuiltinMethod:
		if mem, ok := call.Callee.(*ast.MemberExpr); ok {
			if bInf := exprInfoFromBM(prog, bm, mem.Base); bInf != nil && bInf.Type != nil {
				recvTy := unwrapOptAndRef(bInf.Type)
				for _, mDoc := range BuiltinMethodsForType(recvTy, inf.IsOptChain) {
					if mDoc.Name == mem.Member {
						return fmt.Sprintf("```lowlight\n%s.%s%s\n```\n%s", recvTy.String(), mDoc.Name, mDoc.Signature, mDoc.Summary)
					}
				}
			}
			return fmt.Sprintf("```lowlight\n.%s(...) %s\n```\nBuilt-in method `%s`.", mem.Member, inf.Type.String(), inf.BuiltinName)
		}
	case sema.CallEnumVariant:
		if inf.EnumType != nil && inf.EnumVariant != nil {
			return s.formatEnumVariantHover(prog, mod, inf.EnumType, inf.EnumVariant)
		}
	}
	return ""
}

// contextualKeywordHover documents words that are keywords only in context: `result` after
// `define Name[...]`, and `panic` in a result chain's `default panic` arm.
func contextualKeywordHover(src string, tok token.Token) string {
	before := strings.TrimRight(src[:tok.Span.Start.Offset], " \t")
	switch tok.Text {
	case "result":
		lineStart := strings.LastIndex(before, "\n") + 1
		if strings.HasPrefix(strings.TrimSpace(before[lineStart:]), "define ") {
			return "```lowlight\ndefine Name[T] result [\n    value: T,\n    Failure: Payload,\n]\n```\n" +
				"Declares a **result type**: exactly one `value` member (the success payload) plus one or more failure variants.\n\n" +
				"- Names holding a result end in `!` (`let cfg! = load(path)`).\n" +
				"- Handle failures with a result chain (`load(path): when Missing(k): ... default ...`), `??`, or `if (r! as v)`.\n" +
				"- Construct with bare members where a result is expected: `value(x)`, `Missing(k)`, `Err(e)`."
		}
	case "panic":
		if strings.HasSuffix(before, "default") {
			return "```lowlight\ndefault panic\n```\nEnds a result chain: every failure no arm handles panics with " +
				"`unhandled failure Name.Variant` at this line. Use it where those failures are not expected."
		}
	case "default":
		after := strings.TrimLeft(src[tok.Span.End.Offset:], " \t")
		if strings.HasPrefix(after, "panic") && !strings.HasPrefix(after, "panic(") {
			return contextualKeywordHover(src, token.Token{Text: "panic", Span: diag.Span{Start: tok.Span.End, End: tok.Span.End}})
		}
	}
	return ""
}

// resultKindNote describes a result declaration in hovers.
const resultKindNote = "Result type: `value` is the success member; the rest are failures. " +
	"Names holding one end in `!`; handle failures with a result chain (`expr: when Failure(x): ... default ...`), " +
	"`??`, or `if (r! as v)`."

// enumKindWord is the declaration keyword for `en`: `result` or `enum`.
func enumKindWord(en *types.Enum) string {
	if en.IsResult {
		return "result"
	}
	return "enum"
}

// variantKindNote describes one member of a tagged enum or result in hovers.
func variantKindNote(en *types.Enum, ev *types.EnumVariant) string {
	switch {
	case en.IsResult && ev.Name == types.ResultValueName:
		return fmt.Sprintf("Success member of result `%s`.", en.String())
	case en.IsResult:
		return fmt.Sprintf("Failure of result `%s`.", en.String())
	case ev.Kind == types.VariantTuple:
		return "Tagged union variant."
	default:
		return "Tagged union unit variant."
	}
}

func (s *Server) formatEnumVariantHover(prog *sema.CheckedProgram, mod *sema.Module, en *types.Enum, ev *types.EnumVariant) string {
	if en == nil || ev == nil {
		return ""
	}
	var base string
	if !en.IsTagged {
		base = fmt.Sprintf("```lowlight\n%s.%s = %d // enum %s (%s)\n```",
			en.Name, ev.Name, ev.Value, en.Name, en.Underlying.String())
	} else if ev.Kind == types.VariantTuple {
		pStr := "any"
		if ev.Payload != nil {
			pStr = ev.Payload.String()
		}
		base = fmt.Sprintf("```lowlight\n%s.%s(%s)\n```\n%s", en.String(), ev.Name, pStr, variantKindNote(en, ev))
	} else {
		base = fmt.Sprintf("```lowlight\n%s.%s\n```\n%s", en.String(), ev.Name, variantKindNote(en, ev))
	}
	modPath := ""
	if mod != nil {
		modPath = mod.Path
	}
	if prog != nil && prog.EnumMods[en] != nil && prog.EnumMods[en].Path != "" {
		modPath = prog.EnumMods[en].Path
	}
	sp := findEnumVariantSpan(prog, en, ev.Name)
	rendered := s.renderDocMarkdown(prog, modPath, en.Name, ev.Doc, sp.Start.File, sp.Start.Line, sp.Start.File != "")
	return appendRenderedDoc(base, rendered)
}

func (s *Server) formatStructFieldHover(prog *sema.CheckedProgram, mod *sema.Module, st *types.Struct, sf *types.StructField, fieldTy types.Type) string {
	structName := ""
	if st != nil {
		structName = st.String()
	}
	fieldName := ""
	fieldDoc := ""
	if sf != nil {
		fieldName = sf.Name
		fieldDoc = sf.Doc
	}
	tyStr := "<unknown>"
	note := ""
	if fieldTy != nil && fieldTy != types.TypeInvalid {
		tyStr = fieldTy.String()
		if fieldTy.IsPlain() {
			note = "\nPlain field (copied on read)."
		} else {
			note = "\nResource field (borrowed on read; call `.copy()` to duplicate ownership)."
		}
	}
	base := fmt.Sprintf("```lowlight\n%s.%s: %s\n```%s", structName, fieldName, tyStr, note)
	modPath := ""
	if mod != nil {
		modPath = mod.Path
	}
	if prog != nil && st != nil && prog.StructMods[st] != nil && prog.StructMods[st].Path != "" {
		modPath = prog.StructMods[st].Path
	}
	var sp diag.Span
	if prog != nil && st != nil {
		if decl := prog.StructDecls[st]; decl != nil {
			sp = decl.Span
			for _, df := range decl.Fields {
				if df.Name == fieldName {
					sp = df.Span
					if fieldDoc == "" {
						fieldDoc = df.Doc
					}
					break
				}
			}
		}
	}
	curType := ""
	if st != nil {
		curType = st.Name
	}
	rendered := s.renderDocMarkdown(prog, modPath, curType, fieldDoc, sp.Start.File, sp.Start.Line, sp.Start.File != "")
	return appendRenderedDoc(base, rendered)
}

func (s *Server) formatNominalTypeHover(prog *sema.CheckedProgram, mod *sema.Module, ty types.Type, ifcTargets []sema.ImplementationTarget) string {
	modPath := ""
	if mod != nil {
		modPath = mod.Path
	}
	switch v := ty.(type) {
	case *types.Struct:
		var lines []string
		modPrefix := "define "
		if v.Packed {
			modPrefix = "define packed "
		}
		lines = append(lines, fmt.Sprintf("%s%s struct [", modPrefix, v.String()))
		// Unexported (`_`) fields are only visible inside the struct's own package.
		samePkg := true
		if prog != nil && mod != nil {
			if stMod := prog.StructMods[v]; stMod != nil && stMod.Dir != mod.Dir {
				samePkg = false
			}
		}
		hidden := 0
		for _, f := range v.Fields {
			if !samePkg && strings.HasPrefix(f.Name, "_") {
				hidden++
				continue
			}
			def := ""
			if f.HasDefault {
				def = " = ..."
			}
			lines = append(lines, fmt.Sprintf("    %s: %s%s,", f.Name, f.Type.String(), def))
		}
		if hidden == 1 {
			lines = append(lines, "    // 1 private field")
		} else if hidden > 1 {
			lines = append(lines, fmt.Sprintf("    // %d private fields", hidden))
		}
		lines = append(lines, "]")
		kindNote := "Plain value struct (bitwise copyable)"
		if !v.IsPlain() {
			kindNote = "Resource struct (single owner, deterministic drop)"
		}
		res := fmt.Sprintf("```lowlight\n%s\n```\n%s", strings.Join(lines, "\n"), kindNote)
		if prog != nil {
			if ifcs := prog.ImplementedInterfaces(v); len(ifcs) > 0 {
				var inames []string
				for _, ifc := range ifcs {
					inames = append(inames, "`"+ifc.Name+"`")
				}
				res += "\n\n**Implements:** " + strings.Join(inames, ", ")
			}
		}
		if prog != nil && prog.StructMods[v] != nil && prog.StructMods[v].Path != "" {
			modPath = prog.StructMods[v].Path
		}
		sp := s.typeSpan(prog, v)
		doc := v.Doc
		if doc == "" && prog != nil && prog.StructDecls[v] != nil {
			doc = prog.StructDecls[v].Doc
		}
		return appendRenderedDoc(res, s.renderDocMarkdown(prog, modPath, v.Name, doc, sp.Start.File, sp.Start.Line, true))

	case *types.Enum:
		var lines []string
		var res string
		if !v.IsTagged {
			lines = append(lines, fmt.Sprintf("define %s enum %s [", v.Name, v.Underlying.String()))
			for _, ev := range v.Variants {
				lines = append(lines, fmt.Sprintf("    %s = %d,", ev.Name, ev.Value))
			}
			lines = append(lines, "]")
			res = fmt.Sprintf("```lowlight\n%s\n```\nPlain tag enum.", strings.Join(lines, "\n"))
		} else {
			lines = append(lines, fmt.Sprintf("define %s %s [", v.String(), enumKindWord(v)))
			for _, ev := range v.Variants {
				if ev.Kind == types.VariantTuple {
					pStr := "any"
					if ev.Payload != nil {
						pStr = ev.Payload.String()
					}
					lines = append(lines, fmt.Sprintf("    %s: %s,", ev.Name, pStr))
				} else {
					lines = append(lines, fmt.Sprintf("    %s,", ev.Name))
				}
			}
			lines = append(lines, "]")
			kindNote := "Plain tagged union (bitwise copyable)"
			if !v.IsPlain() {
				kindNote = "Resource tagged union (single owner, deterministic drop)"
			}
			if v.IsResult {
				kindNote = resultKindNote
			}
			res = fmt.Sprintf("```lowlight\n%s\n```\n%s", strings.Join(lines, "\n"), kindNote)
		}
		if prog != nil {
			if ifcs := prog.ImplementedInterfaces(v); len(ifcs) > 0 {
				var inames []string
				for _, ifc := range ifcs {
					inames = append(inames, "`"+ifc.Name+"`")
				}
				res += "\n\n**Implements:** " + strings.Join(inames, ", ")
			}
			if prog.EnumMods[v] != nil && prog.EnumMods[v].Path != "" {
				modPath = prog.EnumMods[v].Path
			}
		}
		sp := s.typeSpan(prog, v)
		doc := v.Doc
		if doc == "" && prog != nil && prog.EnumDecls[v] != nil {
			doc = prog.EnumDecls[v].Doc
		}
		return appendRenderedDoc(res, s.renderDocMarkdown(prog, modPath, v.Name, doc, sp.Start.File, sp.Start.Line, true))

	case *types.Interface:
		var lines []string
		lines = append(lines, fmt.Sprintf("define %s interface [", v.Name))
		for _, m := range v.Methods {
			lines = append(lines, fmt.Sprintf("    %s: %s,", m.Name, formatInterfaceMethodSig(m, false)))
		}
		lines = append(lines, "]")
		res := fmt.Sprintf("```lowlight\n%s\n```", strings.Join(lines, "\n"))
		targets := ifcTargets
		if len(targets) == 0 && prog != nil {
			targets = prog.InterfaceImplementingTargets(v.Name, nil)
		}
		if len(targets) > 0 {
			var tnames []string
			for _, tgt := range targets {
				tnames = append(tnames, "`"+tgt.Name+"`")
			}
			res += "\n\n**Implemented by:** " + strings.Join(tnames, ", ")
		}
		if prog != nil && prog.InterfaceMods[v] != nil && prog.InterfaceMods[v].Path != "" {
			modPath = prog.InterfaceMods[v].Path
		}
		sp := s.typeSpan(prog, v)
		return appendRenderedDoc(res, s.renderDocMarkdown(prog, modPath, v.Name, v.Doc, sp.Start.File, sp.Start.Line, sp.Start.File != ""))

	case *types.Distinct:
		res := fmt.Sprintf("```lowlight\ndefine %s %s\n```\nDistinct nominal type.", v.Name, v.Base.String())
		if prog != nil {
			if ifcs := prog.ImplementedInterfaces(v); len(ifcs) > 0 {
				var inames []string
				for _, ifc := range ifcs {
					inames = append(inames, "`"+ifc.Name+"`")
				}
				res += "\n\n**Implements:** " + strings.Join(inames, ", ")
			}
			if prog.DistinctMods[v] != nil && prog.DistinctMods[v].Path != "" {
				modPath = prog.DistinctMods[v].Path
			}
		}
		sp := s.typeSpan(prog, v)
		return appendRenderedDoc(res, s.renderDocMarkdown(prog, modPath, v.Name, v.Doc, sp.Start.File, sp.Start.Line, true))
	}
	return ""
}

func (s *Server) formatInterfaceDeclHover(prog *sema.CheckedProgram, decl *ast.InterfaceDecl) string {
	if decl == nil {
		return ""
	}
	var tps []string
	for _, tp := range decl.TypeParams {
		tps = append(tps, tp.Name)
	}
	tpStr := ""
	if len(tps) > 0 {
		tpStr = "[" + strings.Join(tps, ", ") + "]"
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("define %s%s interface [", decl.Name, tpStr))
	for _, m := range decl.Methods {
		var pStrs []string
		for _, p := range m.Params {
			pStrs = append(pStrs, fmt.Sprintf("%s: %s", p.Name, formatTypeExpr(p.Type)))
		}
		retStr := ""
		if m.Ret != nil {
			retStr = " " + formatTypeExpr(m.Ret)
		}
		lines = append(lines, fmt.Sprintf("    %s: (%s)%s,", m.Name, strings.Join(pStrs, ", "), retStr))
	}
	lines = append(lines, "]")
	res := fmt.Sprintf("```lowlight\n%s\n```", strings.Join(lines, "\n"))
	if prog != nil {
		targets := s.loadedInterfaceImplementingTargets(decl.Name, prog, nil)
		if len(targets) > 0 {
			var tnames []string
			for _, tgt := range targets {
				tnames = append(tnames, "`"+tgt.Name+"`")
			}
			res += "\n\n**Implemented by:** " + strings.Join(tnames, ", ")
		}
	}
	return appendRenderedDoc(res, s.renderDocMarkdown(prog, decl.Span.Start.File, decl.Name, decl.Doc, decl.Span.Start.File, decl.Span.Start.Line, true))
}

func (s *Server) formatStructDeclHover(prog *sema.CheckedProgram, mod *sema.Module, decl *ast.StructDecl, _ map[string]string) string {
	var tps []string
	for _, tp := range decl.TypeParams {
		tps = append(tps, tp.Name)
	}
	tpStr := ""
	if len(tps) > 0 {
		tpStr = "[" + strings.Join(tps, ", ") + "]"
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("define %s%s struct [", decl.Name, tpStr))
	for _, f := range decl.Fields {
		lines = append(lines, fmt.Sprintf("    %s: %s,", f.Name, formatTypeExpr(f.Type)))
	}
	lines = append(lines, "]")
	res := fmt.Sprintf("```lowlight\n%s\n```\nResource struct (single owner, deterministic drop)", strings.Join(lines, "\n"))
	if prog != nil {
		targets := prog.TypeImplementedInterfaceTargets(decl.Name, mod)
		if len(targets) > 0 {
			var inames []string
			for _, it := range targets {
				inames = append(inames, "`"+it.Name+"`")
			}
			res += "\n\n**Implements:** " + strings.Join(inames, ", ")
		}
	}
	modPath := decl.Span.Start.File
	if modPath == "" && mod != nil {
		modPath = mod.Path
	}
	return appendRenderedDoc(res, s.renderDocMarkdown(prog, modPath, decl.Name, decl.Doc, decl.Span.Start.File, decl.Span.Start.Line, true))
}

func (s *Server) formatEnumDeclHover(prog *sema.CheckedProgram, mod *sema.Module, decl *ast.EnumDecl) string {
	if decl == nil {
		return ""
	}
	var tps []string
	for _, tp := range decl.TypeParams {
		tps = append(tps, tp.Name)
	}
	tpStr := ""
	if len(tps) > 0 {
		tpStr = "[" + strings.Join(tps, ", ") + "]"
	}
	var lines []string
	kind := "enum"
	if decl.IsResult {
		kind = "result"
	}
	lines = append(lines, fmt.Sprintf("define %s%s %s [", decl.Name, tpStr, kind))
	for _, v := range decl.Variants {
		if v.PayloadType != nil {
			lines = append(lines, fmt.Sprintf("    %s: %s,", v.Name, formatTypeExpr(v.PayloadType)))
		} else {
			lines = append(lines, fmt.Sprintf("    %s,", v.Name))
		}
	}
	lines = append(lines, "]")
	res := fmt.Sprintf("```lowlight\n%s\n```", strings.Join(lines, "\n"))
	modPath := decl.Span.Start.File
	if modPath == "" && mod != nil {
		modPath = mod.Path
	}
	return appendRenderedDoc(res, s.renderDocMarkdown(prog, modPath, decl.Name, decl.Doc, decl.Span.Start.File, decl.Span.Start.Line, true))
}

// -----------------------------------------------------------------------------
// Go-to-Definition (`textDocument/definition`)
// -----------------------------------------------------------------------------

func (s *Server) computeDefinition(filePath string, src string, pos Position) *Location {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}

	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return nil
	}

	// 0. Check intra-doc links (`[`Symbol`]`) inside `//` and `/* ... */` comments
	if target, _, isLink := findDocLinkAtOffset(filePath, src, offset); isLink {
		idx := s.getDocIndex(prog, mod.Path)
		encType := enclosingTypeAtOffset(mod.File, offset)
		if idx != nil {
			if entry, _ := idx.ResolveLink(target, mod.Path, encType); entry != nil && entry.FilePath != "" {
				line := entry.Line - 1
				if line < 0 {
					line = 0
				}
				col := entry.Col - 1
				if col < 0 {
					col = 0
				}
				p := Position{Line: line, Character: col}
				return &Location{
					URI:   PathToURI(entry.FilePath),
					Range: Range{Start: p, End: p},
				}
			}
		}
	}

	tok, ok := s.findTokenAtOffset(filePath, src, offset)
	if !ok {
		return nil
	}

	// 1. Check AST-directed jump targets (imports, calls, fields, locals, params, enum variants)
	if sp := definitionSpanFromAST(prog, mod, offset, tok); sp.Start.File != "" {
		return s.spanToLocation(sp)
	}

	// 2. Check nominal types (`Point`, `Status`, `Pool`, `UserID`)
	word := tok.Text
	if nom := lookupNominalType(mod, "", word); nom != nil {
		if sp, ok2 := prog.TypeSpans[nom]; ok2 && sp.Start.File != "" {
			return s.spanToLocation(sp)
		}
	}
	if gst := lookupGenStruct(mod, "", word); gst != nil && gst.Decl != nil {
		return s.spanToLocation(gst.Decl.Span)
	}
	if gen := lookupGenEnum(mod, "", word); gen != nil && gen.Decl != nil {
		return s.spanToLocation(gen.Decl.Span)
	}
	if gi := lookupGenInterface(mod, "", word); gi != nil && gi.Decl != nil {
		return s.spanToLocation(gi.Decl.Span)
	}

	// 3. Check top-level symbols, generic functions, and module aliases
	if sym := lookupTopSym(mod, word); sym != nil && sym.Span.Start.File != "" {
		return s.spanToLocation(sym.Span)
	}
	if gfn := lookupTopGenFunc(mod, word); gfn != nil && gfn.VarDecl != nil {
		return s.spanToLocation(gfn.VarDecl.Span)
	}
	if targetMod, isMod := mod.Aliased[word]; isMod && targetMod.File != nil {
		return &Location{
			URI:   PathToURI(targetMod.Path),
			Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 0}},
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// Go-to-Implementation (`textDocument/implementation`)
// -----------------------------------------------------------------------------

func (s *Server) typeSpan(prog *sema.CheckedProgram, t types.Type) diag.Span {
	if prog == nil || t == nil {
		return diag.Span{}
	}
	if sp, ok := prog.TypeSpans[t]; ok && sp.Start.File != "" {
		return sp
	}
	if st, ok := t.(*types.Struct); ok {
		if decl, ok2 := prog.StructDecls[st]; ok2 && decl != nil {
			return decl.Span
		}
	}
	if en, ok := t.(*types.Enum); ok {
		if decl, ok2 := prog.EnumDecls[en]; ok2 && decl != nil {
			return decl.Span
		}
	}
	return diag.Span{}
}

func (s *Server) computeImplementation(filePath string, src string, pos Position) []Location {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}

	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return nil
	}

	tok, ok := s.findTokenAtOffset(filePath, src, offset)
	if !ok {
		return nil
	}

	word := tok.Text
	var locs []Location
	seen := make(map[string]bool)
	addLoc := func(sp diag.Span) {
		if sp.Start.File == "" {
			return
		}
		loc := s.spanToLocation(sp)
		if loc != nil {
			key := fmt.Sprintf("%s:%d:%d", loc.URI, loc.Range.Start.Line, loc.Range.Start.Character)
			if !seen[key] {
				seen[key] = true
				locs = append(locs, *loc)
			}
		}
	}

	// 1. Check if token refers to an interface (concrete or generic template) -> find implementing types!
	if ifcTargets := s.allInterfaceImplementingTargets(word, prog, mod); len(ifcTargets) > 0 {
		for _, tgt := range ifcTargets {
			if tgt.Span.Start.File != "" {
				addLoc(tgt.Span)
			}
		}
		if len(locs) > 0 {
			return locs
		}
	}

	// 2. Check if token refers to a struct, distinct type, or generic struct -> find implemented interfaces!
	if typeTargets := prog.TypeImplementedInterfaceTargets(word, mod); len(typeTargets) > 0 {
		for _, tgt := range typeTargets {
			if tgt.Span.Start.File != "" {
				addLoc(tgt.Span)
			}
		}
		if len(locs) > 0 {
			return locs
		}
	}

	// 4. Check AST context (variable or expression at cursor)
	for _, fi := range prog.Funcs {
		if fi != nil && fi.Mod == mod && fi.BodyMaps != nil {
			for expr, inf := range fi.BodyMaps.ExprInfos {
				if spanContainsOffset(expr.GetSpan(), offset) && inf.Type != nil {
					if ifc, isIfc := inf.Type.(*types.Interface); isIfc {
						for _, it := range prog.ImplementingTypes(ifc) {
							addLoc(s.typeSpan(prog, it))
						}
						return locs
					}
					for _, ifc := range prog.ImplementedInterfaces(inf.Type) {
						addLoc(s.typeSpan(prog, ifc))
					}
					if len(locs) > 0 {
						return locs
					}
				}
			}
		}
	}

	return locs
}

// isPlaceholderIdent reports the empty base the parser gives a result chain's bare failure
// patterns (`when Missing(why):`).
func isPlaceholderIdent(e ast.Expr) bool {
	id, ok := e.(*ast.IdentExpr)
	return ok && id.Name == ""
}

func definitionSpanFromAST(prog *sema.CheckedProgram, mod *sema.Module, offset int, tok token.Token) diag.Span {
	for _, d := range mod.File.Decls {
		if !spanContainsOffset(d.GetSpan(), offset) {
			continue
		}
		switch decl := d.(type) {
		case *ast.ImportDecl:
			resolved := resolveImportFromMod(prog, mod, decl.Path)
			if resolved != "" {
				return diag.Span{
					Start: diag.Pos{File: resolved, Line: 1, Col: 1},
					End:   diag.Pos{File: resolved, Line: 1, Col: 1},
				}
			}
		case *ast.VarDecl:
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				fi := findFuncInstanceForDecl(prog, mod, decl.Span)
				var bm *sema.BodyMaps
				if fi != nil {
					bm = fi.BodyMaps
				}
				for _, p := range fl.Params {
					if p.Type != nil && spanContainsOffset(p.Type.GetSpan(), offset) {
						if sp := definitionSpanInTypeExpr(prog, mod, p.Type, offset, tok); sp.Start.File != "" {
							return sp
						}
					}
				}
				if fl.Ret != nil && spanContainsOffset(fl.Ret.GetSpan(), offset) {
					if sp := definitionSpanInTypeExpr(prog, mod, fl.Ret, offset, tok); sp.Start.File != "" {
						return sp
					}
				}
				if sp := definitionSpanInExpr(prog, mod, bm, fl.Body, offset, tok); sp.Start.File != "" {
					return sp
				}
			} else if decl.Value != nil {
				if sp := definitionSpanInExpr(prog, mod, prog.TopBodyMaps, decl.Value, offset, tok); sp.Start.File != "" {
					return sp
				}
			}
		case *ast.MethodDecl:
			if tok.Text == decl.RecvType {
				if nom := lookupNominalType(mod, "", decl.RecvType); nom != nil {
					if sp, ok := prog.TypeSpans[nom]; ok {
						return sp
					}
				}
				if gst := lookupGenStruct(mod, "", decl.RecvType); gst != nil {
					return gst.Decl.Span
				}
			}
			fi := findFuncInstanceForDecl(prog, mod, decl.Span)
			var bm *sema.BodyMaps
			if fi != nil {
				bm = fi.BodyMaps
			}
			if sp := definitionSpanInExpr(prog, mod, bm, decl.Body, offset, tok); sp.Start.File != "" {
				return sp
			}
		case *ast.StructDecl:
			for _, f := range decl.Fields {
				if f.Type != nil && spanContainsOffset(f.Type.GetSpan(), offset) {
					if sp := definitionSpanInTypeExpr(prog, mod, f.Type, offset, tok); sp.Start.File != "" {
						return sp
					}
				}
			}
		}
	}
	return diag.Span{}
}

func definitionSpanInTypeExpr(prog *sema.CheckedProgram, mod *sema.Module, te ast.TypeExpr, offset int, tok token.Token) diag.Span {
	if te == nil || !spanContainsOffset(te.GetSpan(), offset) {
		return diag.Span{}
	}
	switch v := te.(type) {
	case *ast.NamedType:
		for _, ta := range v.TypeArgs {
			if sp := definitionSpanInTypeExpr(prog, mod, ta, offset, tok); sp.Start.File != "" {
				return sp
			}
		}
		if tok.Text == v.Name {
			if nom := lookupNominalType(mod, v.Module, v.Name); nom != nil {
				if sp, ok := prog.TypeSpans[nom]; ok {
					return sp
				}
			}
			if gst := lookupGenStruct(mod, v.Module, v.Name); gst != nil {
				return gst.Decl.Span
			}
			if gen := lookupGenEnum(mod, v.Module, v.Name); gen != nil {
				return gen.Decl.Span
			}
		}
	case *ast.OptionalType:
		return definitionSpanInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.MutBorrowType:
		return definitionSpanInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.OwnType:
		return definitionSpanInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.VectorType:
		return definitionSpanInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.StackArrayType:
		return definitionSpanInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.TupleType:
		for _, el := range v.Elems {
			if sp := definitionSpanInTypeExpr(prog, mod, el, offset, tok); sp.Start.File != "" {
				return sp
			}
		}
	case *ast.SetType:
		return definitionSpanInTypeExpr(prog, mod, v.Elem, offset, tok)
	case *ast.MapType:
		if sp := definitionSpanInTypeExpr(prog, mod, v.Key, offset, tok); sp.Start.File != "" {
			return sp
		}
		return definitionSpanInTypeExpr(prog, mod, v.Value, offset, tok)
	}
	return diag.Span{}
}

func definitionSpanInExpr(prog *sema.CheckedProgram, mod *sema.Module, bm *sema.BodyMaps, e ast.Expr, offset int, tok token.Token) diag.Span {
	if e == nil || !spanContainsOffset(e.GetSpan(), offset) {
		return diag.Span{}
	}
	switch v := e.(type) {
	case *ast.BlockExpr:
		for _, st := range v.Stmts {
			if spanContainsOffset(st.GetSpan(), offset) {
				switch s := st.(type) {
				case *ast.VarStmt:
					if s.IsUnpack {
						for _, uv := range s.UnpackVars {
							if uv.Type != nil && spanContainsOffset(uv.Type.GetSpan(), offset) {
								return definitionSpanInTypeExpr(prog, mod, uv.Type, offset, tok)
							}
						}
					} else if s.Type != nil && spanContainsOffset(s.Type.GetSpan(), offset) {
						return definitionSpanInTypeExpr(prog, mod, s.Type, offset, tok)
					}
					if sp := definitionSpanInExpr(prog, mod, bm, s.Value, offset, tok); sp.Start.File != "" {
						return sp
					}
				case *ast.AssignStmt:
					if s.IsUnpack {
						for _, tgt := range s.UnpackTargets {
							if sp := definitionSpanInExpr(prog, mod, bm, tgt, offset, tok); sp.Start.File != "" {
								return sp
							}
						}
						return definitionSpanInExpr(prog, mod, bm, s.Value, offset, tok)
					}
					if sp := definitionSpanInExpr(prog, mod, bm, s.Target, offset, tok); sp.Start.File != "" {
						return sp
					}
					return definitionSpanInExpr(prog, mod, bm, s.Value, offset, tok)
				case *ast.UnwindStmt:
					return definitionSpanInExpr(prog, mod, bm, s.Value, offset, tok)
				case *ast.ExprStmt:
					return definitionSpanInExpr(prog, mod, bm, s.Expr, offset, tok)
				}
			}
		}
		return definitionSpanInExpr(prog, mod, bm, v.Tail, offset, tok)

	case *ast.StmtBodyExpr:
		if es, ok := v.Stmt.(*ast.ExprStmt); ok {
			return definitionSpanInExpr(prog, mod, bm, es.Expr, offset, tok)
		}
		if as, ok := v.Stmt.(*ast.AssignStmt); ok {
			if sp := definitionSpanInExpr(prog, mod, bm, as.Target, offset, tok); sp.Start.File != "" {
				return sp
			}
			return definitionSpanInExpr(prog, mod, bm, as.Value, offset, tok)
		}

	case *ast.IfExpr:
		if v.CondKind == ast.IfCondOptVar && tok.Text == v.OptVar && (v.Then == nil || offset < v.Then.GetSpan().Start.Offset) {
			if bm != nil {
				if orig := bm.IfOriginSyms[v]; orig != nil && orig.Span.Start.File != "" {
					return orig.Span
				}
			}
		}
		if sp := definitionSpanInExpr(prog, mod, bm, v.Cond, offset, tok); sp.Start.File != "" {
			return sp
		}
		if sp := definitionSpanInExpr(prog, mod, bm, v.Then, offset, tok); sp.Start.File != "" {
			return sp
		}
		return definitionSpanInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.ForExpr:
		if sp := definitionSpanInExpr(prog, mod, bm, v.CondOrIter, offset, tok); sp.Start.File != "" {
			return sp
		}
		if sp := definitionSpanInExpr(prog, mod, bm, v.RangeEnd, offset, tok); sp.Start.File != "" {
			return sp
		}
		if sp := definitionSpanInExpr(prog, mod, bm, v.Body, offset, tok); sp.Start.File != "" {
			return sp
		}
		return definitionSpanInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.WhileExpr:
		if sp := definitionSpanInExpr(prog, mod, bm, v.CondOrIter, offset, tok); sp.Start.File != "" {
			return sp
		}
		if sp := definitionSpanInExpr(prog, mod, bm, v.RangeEnd, offset, tok); sp.Start.File != "" {
			return sp
		}
		if sp := definitionSpanInExpr(prog, mod, bm, v.Body, offset, tok); sp.Start.File != "" {
			return sp
		}
		return definitionSpanInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.MatchExpr:
		for _, s := range v.Scrutinees {
			if sp := definitionSpanInExpr(prog, mod, bm, s, offset, tok); sp.Start.File != "" {
				return sp
			}
		}
		for _, arm := range v.Arms {
			if arm.Synthetic {
				continue
			}
			for _, pat := range arm.AllPatterns() {
				if ep, ok := pat.(*ast.ExprPattern); ok && spanContainsOffset(ep.Span, offset) {
					if sp := definitionSpanInExpr(prog, mod, bm, ep.Expr, offset, tok); sp.Start.File != "" {
						return sp
					}
				}
				if cp, ok := pat.(*ast.ContainsPattern); ok && spanContainsOffset(cp.Span, offset) {
					if sp := definitionSpanInExpr(prog, mod, bm, cp.Container, offset, tok); sp.Start.File != "" {
						return sp
					}
				}
				if mp, ok := pat.(*ast.MethodPattern); ok && spanContainsOffset(mp.Span, offset) {
					if bm != nil {
						if mpInfo := bm.MethodPatterns[mp]; mpInfo != nil && mpInfo.Call != nil {
							if sp := definitionSpanInExpr(prog, mod, bm, mpInfo.Call, offset, tok); sp.Start.File != "" {
								return sp
							}
						}
					}
					for _, a := range mp.Args {
						if sp := definitionSpanInExpr(prog, mod, bm, a, offset, tok); sp.Start.File != "" {
							return sp
						}
					}
				}
				if vp, ok := pat.(*ast.VariantPattern); ok && spanContainsOffset(vp.Target.GetSpan(), offset) {
					if sp := definitionSpanInExpr(prog, mod, bm, vp.Target, offset, tok); sp.Start.File != "" {
						return sp
					}
				}
			}
			if sp := definitionSpanInExpr(prog, mod, bm, arm.Body, offset, tok); sp.Start.File != "" {
				return sp
			}
		}

	case *ast.FuncLit:
		lbm := bm
		for _, li := range prog.Lambdas {
			if li.Lit == v {
				lbm = li.BodyMaps
				break
			}
		}
		return definitionSpanInExpr(prog, mod, lbm, v.Body, offset, tok)

	case *ast.CallExpr:
		for _, a := range v.Args {
			if spanContainsOffset(a.GetSpan(), offset) {
				return definitionSpanInExpr(prog, mod, bm, a, offset, tok)
			}
		}
		for _, ta := range v.TypeArgs {
			if spanContainsOffset(ta.GetSpan(), offset) {
				return definitionSpanInTypeExpr(prog, mod, ta, offset, tok)
			}
		}
		if spanContainsOffset(v.Callee.GetSpan(), offset) {
			if mem, isMem := v.Callee.(*ast.MemberExpr); isMem && spanContainsOffset(mem.Base.GetSpan(), offset) {
				return definitionSpanInExpr(prog, mod, bm, mem.Base, offset, tok)
			}
			if inf := exprInfoFromBM(prog, bm, v); inf != nil {
				if inf.TargetFunc != nil && inf.TargetFunc.DeclSpan.Start.File != "" {
					return inf.TargetFunc.DeclSpan
				}
				if inf.TargetExtern != nil && inf.TargetExtern.Span.Start.File != "" {
					return inf.TargetExtern.Span
				}
			}
			return definitionSpanInExpr(prog, mod, bm, v.Callee, offset, tok)
		}

	case *ast.MemberExpr:
		if spanContainsOffset(v.Base.GetSpan(), offset) && !isPlaceholderIdent(v.Base) {
			return definitionSpanInExpr(prog, mod, bm, v.Base, offset, tok)
		}
		inf := exprInfoFromBM(prog, bm, v)
		if inf != nil {
			if inf.Sym != nil && inf.Sym.Span.Start.File != "" {
				return inf.Sym.Span
			}
			if inf.EnumType != nil && inf.EnumVariant != nil {
				return findEnumVariantSpan(prog, inf.EnumType, inf.EnumVariant.Name)
			}
			if baseInf := exprInfoFromBM(prog, bm, v.Base); baseInf != nil && baseInf.Type != nil {
				recv := unwrapOptAndRef(baseInf.Type)
				if st, isSt := recv.(*types.Struct); isSt {
					if decl := prog.StructDecls[st]; decl != nil {
						for _, df := range decl.Fields {
							if df.Name == v.Member {
								return df.Span
							}
						}
						return decl.Span
					}
				}
			}
		}

	case *ast.StructLit:
		for _, f := range v.Fields {
			if spanContainsOffset(f.Value.GetSpan(), offset) {
				return definitionSpanInExpr(prog, mod, bm, f.Value, offset, tok)
			}
			if spanContainsOffset(f.Span, offset) && tok.Text == f.Name {
				if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.StructType != nil {
					if decl := prog.StructDecls[inf.StructType]; decl != nil {
						for _, df := range decl.Fields {
							if df.Name == f.Name {
								return df.Span
							}
						}
					}
				}
			}
		}
		if tok.Text == v.TypeName {
			if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.StructType != nil {
				if sp, ok := prog.TypeSpans[inf.StructType]; ok {
					return sp
				}
			}
		}

	case *ast.IdentExpr:
		if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.Sym != nil && inf.Sym.Span.Start.File != "" {
			return inf.Sym.Span
		}
		// Bare result member (`Forbidden`, or the callee of `value(x)` / `Err(e)`)
		if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.EnumType != nil && inf.EnumVariant != nil {
			return findEnumVariantSpan(prog, inf.EnumType, inf.EnumVariant.Name)
		}

	case *ast.BinaryExpr:
		if sp := definitionSpanInExpr(prog, mod, bm, v.Left, offset, tok); sp.Start.File != "" {
			return sp
		}
		return definitionSpanInExpr(prog, mod, bm, v.Right, offset, tok)

	case *ast.UnaryExpr:
		return definitionSpanInExpr(prog, mod, bm, v.Operand, offset, tok)

	case *ast.UnsafeExpr:
		return definitionSpanInExpr(prog, mod, bm, v.Expr, offset, tok)

	case *ast.IndexExpr:
		if sp := definitionSpanInExpr(prog, mod, bm, v.Base, offset, tok); sp.Start.File != "" {
			return sp
		}
		return definitionSpanInExpr(prog, mod, bm, v.Index, offset, tok)

	case *ast.SliceExpr:
		if sp := definitionSpanInExpr(prog, mod, bm, v.Base, offset, tok); sp.Start.File != "" {
			return sp
		}
		if sp := definitionSpanInExpr(prog, mod, bm, v.Low, offset, tok); sp.Start.File != "" {
			return sp
		}
		return definitionSpanInExpr(prog, mod, bm, v.High, offset, tok)

	case *ast.RangeExpr:
		if sp := definitionSpanInExpr(prog, mod, bm, v.Low, offset, tok); sp.Start.File != "" {
			return sp
		}
		return definitionSpanInExpr(prog, mod, bm, v.High, offset, tok)

	case *ast.TupleLit:
		for _, el := range v.Elems {
			if sp := definitionSpanInExpr(prog, mod, bm, el, offset, tok); sp.Start.File != "" {
				return sp
			}
		}

	case *ast.VecLit:
		for _, el := range v.Elems {
			if sp := definitionSpanInExpr(prog, mod, bm, el, offset, tok); sp.Start.File != "" {
				return sp
			}
		}

	case *ast.SetLit:
		for _, el := range v.Elems {
			if sp := definitionSpanInExpr(prog, mod, bm, el, offset, tok); sp.Start.File != "" {
				return sp
			}
		}

	case *ast.MapLit:
		for _, ent := range v.Entries {
			if sp := definitionSpanInExpr(prog, mod, bm, ent.Key, offset, tok); sp.Start.File != "" {
				return sp
			}
			if sp := definitionSpanInExpr(prog, mod, bm, ent.Value, offset, tok); sp.Start.File != "" {
				return sp
			}
		}

	case *ast.ArrayLit:
		for _, el := range v.Elems {
			if sp := definitionSpanInExpr(prog, mod, bm, el, offset, tok); sp.Start.File != "" {
				return sp
			}
		}

	case *ast.InterpStringExpr:
		for _, sub := range v.Exprs {
			if sp := definitionSpanInExpr(prog, mod, bm, sub, offset, tok); sp.Start.File != "" {
				return sp
			}
		}
	}
	return diag.Span{}
}

func findEnumVariantSpan(prog *sema.CheckedProgram, en *types.Enum, variantName string) diag.Span {
	for _, m := range prog.Modules {
		if m.File == nil {
			continue
		}
		for _, d := range m.File.Decls {
			ed, ok := d.(*ast.EnumDecl)
			if !ok || ed.Name != en.Name {
				continue
			}
			for _, v := range ed.Variants {
				if v.Name == variantName {
					return v.Span
				}
			}
			return ed.Span
		}
	}
	if sp, ok := prog.TypeSpans[en]; ok {
		return sp
	}
	return diag.Span{}
}

func (s *Server) spanToLocation(sp diag.Span) *Location {
	if sp.Start.File == "" {
		return nil
	}
	targetSrc := s.readSourceForPath(sp.Start.File)
	return &Location{
		URI:   PathToURI(sp.Start.File),
		Range: SpanToLSP(targetSrc, sp),
	}
}

// -----------------------------------------------------------------------------
// Document Symbols (`textDocument/documentSymbol`)
// -----------------------------------------------------------------------------

func (s *Server) computeDocumentSymbols(filePath string, src string) []DocumentSymbol {
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return nil
	}

	var symbols []DocumentSymbol
	for _, d := range mod.File.Decls {
		switch decl := d.(type) {
		case *ast.StructDecl:
			rng := SpanToLSP(src, decl.Span)
			var children []DocumentSymbol
			for _, f := range decl.Fields {
				fr := SpanToLSP(src, f.Span)
				children = append(children, DocumentSymbol{
					Name:           f.Name,
					Detail:         formatTypeExpr(f.Type),
					Kind:           SymField,
					Range:          fr,
					SelectionRange: fr,
				})
			}
			detail := "struct"
			if decl.Packed {
				detail = "packed struct"
			}
			symbols = append(symbols, DocumentSymbol{
				Name:           decl.Name,
				Detail:         detail,
				Kind:           SymStruct,
				Range:          rng,
				SelectionRange: rng,
				Children:       children,
			})

		case *ast.EnumDecl:
			rng := SpanToLSP(src, decl.Span)
			var children []DocumentSymbol
			for _, v := range decl.Variants {
				vr := SpanToLSP(src, v.Span)
				children = append(children, DocumentSymbol{
					Name:           v.Name,
					Kind:           SymEnumMember,
					Range:          vr,
					SelectionRange: vr,
				})
			}
			symbols = append(symbols, DocumentSymbol{
				Name:           decl.Name,
				Detail:         "enum",
				Kind:           SymEnum,
				Range:          rng,
				SelectionRange: rng,
				Children:       children,
			})

		case *ast.InterfaceDecl:
			rng := SpanToLSP(src, decl.Span)
			var children []DocumentSymbol
			for _, m := range decl.Methods {
				mr := SpanToLSP(src, m.Span)
				children = append(children, DocumentSymbol{
					Name:           m.Name,
					Kind:           SymMethod,
					Range:          mr,
					SelectionRange: mr,
				})
			}
			symbols = append(symbols, DocumentSymbol{
				Name:           decl.Name,
				Detail:         "interface",
				Kind:           SymInterface,
				Range:          rng,
				SelectionRange: rng,
				Children:       children,
			})

		case *ast.DistinctTypeDecl:
			rng := SpanToLSP(src, decl.Span)
			symbols = append(symbols, DocumentSymbol{
				Name:           decl.Name,
				Detail:         "define " + formatTypeExpr(decl.Base),
				Kind:           SymStruct,
				Range:          rng,
				SelectionRange: rng,
			})

		case *ast.MethodDecl:
			rng := SpanToLSP(src, decl.Span)
			symbols = append(symbols, DocumentSymbol{
				Name:           decl.RecvType + "." + decl.Name,
				Detail:         formatGenericMethodDeclSig(decl, nil, false),
				Kind:           SymMethod,
				Range:          rng,
				SelectionRange: rng,
			})

		case *ast.VarDecl:
			rng := SpanToLSP(src, decl.Span)
			kind := SymConstant
			detail := "bind"
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				kind = SymFunction
				var ps []string
				for _, p := range fl.Params {
					ps = append(ps, fmt.Sprintf("%s: %s", p.Name, formatTypeExpr(p.Type)))
				}
				if fl.Ret == nil {
					detail = "(" + strings.Join(ps, ", ") + ")"
				} else {
					detail = fmt.Sprintf("(%s) %s", strings.Join(ps, ", "), formatTypeExpr(fl.Ret))
				}
			}
			for _, attr := range decl.Attributes {
				if attr.Name == "test" {
					kind = SymFunction
					if len(attr.Args) > 0 {
						detail = fmt.Sprintf("@test %q", attr.Args[0])
					} else {
						detail = "@test"
					}
					break
				}
			}
			if decl.Name == "test_main" {
				kind = SymFunction
				detail = "test_main"
			}
			symbols = append(symbols, DocumentSymbol{
				Name:           decl.Name,
				Detail:         detail,
				Kind:           kind,
				Range:          rng,
				SelectionRange: rng,
			})

		case *ast.ExternFnDecl:
			rng := SpanToLSP(src, decl.Span)
			name := decl.Name
			if decl.Alias != "" {
				name = decl.Alias
			}
			symbols = append(symbols, DocumentSymbol{
				Name:           name,
				Detail:         "extern " + decl.Name,
				Kind:           SymFunction,
				Range:          rng,
				SelectionRange: rng,
			})
		}
	}

	return symbols
}

// -----------------------------------------------------------------------------
// Signature Help (`textDocument/signatureHelp`)
// -----------------------------------------------------------------------------

func (s *Server) computeSignatureHelp(filePath string, src string, pos Position) *SignatureHelp {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}

	silentRep := diag.NewReporter()
	rawToks := lexer.Lex(filePath, src[:offset], silentRep)
	var toks []token.Token
	for _, tk := range rawToks {
		if tk.Kind != token.EOF && !tk.AutoSemi {
			toks = append(toks, tk)
		}
	}
	if len(toks) == 0 {
		return nil
	}

	// Find innermost unclosed `(` and count top-level `,` arguments inside it!
	type frame struct {
		idx    int
		kind   token.Kind
		commas int
	}
	var stack []frame
	for i, tk := range toks {
		switch tk.Kind {
		case token.LParen, token.LBracket, token.HashBrack, token.PlusBrack, token.LBrace:
			stack = append(stack, frame{idx: i, kind: tk.Kind})
		case token.RParen, token.RBracket, token.RBrace:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case token.Comma:
			if len(stack) > 0 {
				stack[len(stack)-1].commas++
			}
		}
	}

	callOpenIdx := -1
	activeParam := 0
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].kind == token.LParen {
			// Skip control-flow parens (`if (`, `for (`, `while (`, `match (`)!
			if stack[i].idx >= 1 {
				prevK := toks[stack[i].idx-1].Kind
				if prevK == token.KwIf || prevK == token.KwFor || prevK == token.KwWhile || prevK == token.KwMatch {
					continue
				}
			}
			callOpenIdx = stack[i].idx
			activeParam = stack[i].commas
			break
		}
	}
	if callOpenIdx <= 0 {
		return nil
	}

	// Walk back past optional generic type arguments `callee[T](`
	calleeEndIdx := callOpenIdx - 1
	if toks[calleeEndIdx].Kind == token.RBracket {
		d := 0
		for j := calleeEndIdx; j >= 0; j-- {
			if toks[j].Kind == token.RBracket {
				d++
			} else if toks[j].Kind == token.LBracket {
				d--
				if d == 0 {
					calleeEndIdx = j - 1
					break
				}
			}
		}
	}
	if calleeEndIdx < 0 || toks[calleeEndIdx].Kind != token.Ident {
		return nil
	}

	calleeName := toks[calleeEndIdx].Text
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)

	// Case 1: Member call `expr.method(` or `mod.fn(` or `Arena.with_capacity(`
	if calleeEndIdx >= 2 && (toks[calleeEndIdx-1].Kind == token.Dot || toks[calleeEndIdx-1].Kind == token.QuestDot) {
		dotTok := toks[calleeEndIdx-1]
		// Check `mod.fn(` or `Arena.with_capacity(` when preceded by a plain identifier
		if calleeEndIdx >= 2 && (toks[calleeEndIdx-2].Kind == token.Ident || toks[calleeEndIdx-2].Kind == token.OptIdent) {
			recvIdent := toks[calleeEndIdx-2].Text
			if mod != nil {
				if targetMod, isMod := mod.Aliased[recvIdent]; isMod {
					if sym := targetMod.Symbols[calleeName]; sym != nil && sym.FuncInst != nil {
						return s.makeSigHelpFromFuncInst(prog, targetMod, sym.FuncInst, false, activeParam)
					}
					if gfn := targetMod.GenericFuncs[calleeName]; gfn != nil {
						return s.makeSigHelpFromGenericVar(prog, targetMod, gfn, activeParam)
					}
				}
				if recvIdent == "Arena" && calleeName == "with_capacity" {
					return makeSimpleSigHelp("Arena.with_capacity(bytes: int) Arena", []string{"bytes: int"}, activeParam, "Pre-sizes a new Arena region.")
				}
				if nom := lookupNominalType(mod, "", recvIdent); nom != nil {
					if en, ok := nom.(*types.Enum); ok {
						for _, v := range en.Variants {
							if v.Name == calleeName {
								var ps []string
								var pInfos []ParameterInformation
								if v.Payload != nil {
									pStr := fmt.Sprintf("payload: %s", v.Payload.String())
									ps = append(ps, pStr)
									pInfos = append(pInfos, ParameterInformation{Label: pStr})
								}
								lbl := fmt.Sprintf("%s.%s(%s) %s", en.Name, v.Name, strings.Join(ps, ", "), en.Name)
								docText := fmt.Sprintf("Constructs `%s.%s` enum variant.", en.Name, v.Name)
								if v.Doc != "" {
									docMod := ""
									if prog != nil && prog.EnumMods[en] != nil {
										docMod = prog.EnumMods[en].Path
									}
									docText = s.renderDocMarkdown(prog, docMod, en.Name, v.Doc, "", 0, false)
								}
								return &SignatureHelp{
									Signatures: []SignatureInformation{{
										Label: lbl,
										Documentation: &MarkupContent{
											Kind:  "markdown",
											Value: docText,
										},
										Parameters: pInfos,
									}},
									ActiveSignature: 0,
									ActiveParameter: activeParam,
								}
							}
						}
					}
					if mset, ok := prog.MethodTable[sema.TypeMethodKey(nom)]; ok {
						if fi := mset[calleeName]; fi != nil {
							return s.makeSigHelpFromFuncInst(prog, mod, fi, false, activeParam)
						}
					}
				}
				if gen := lookupGenEnum(mod, "", recvIdent); gen != nil && gen.Decl != nil {
					for _, v := range gen.Decl.Variants {
						if v.Name == calleeName {
							var ps []string
							var pInfos []ParameterInformation
							if v.PayloadType != nil {
								pStr := fmt.Sprintf("payload: %s", formatTypeExpr(v.PayloadType))
								ps = append(ps, pStr)
								pInfos = append(pInfos, ParameterInformation{Label: pStr})
							}
							lbl := fmt.Sprintf("%s.%s(%s) %s", gen.Decl.Name, v.Name, strings.Join(ps, ", "), gen.Decl.Name)
							docText := fmt.Sprintf("Constructs generic enum variant `%s.%s`.", gen.Decl.Name, v.Name)
							if v.Doc != "" {
								docText = s.renderDocMarkdown(prog, v.Span.Start.File, gen.Decl.Name, v.Doc, "", 0, false)
							}
							return &SignatureHelp{
								Signatures: []SignatureInformation{{
									Label: lbl,
									Documentation: &MarkupContent{
										Kind:  "markdown",
										Value: docText,
									},
									Parameters: pInfos,
								}},
								ActiveSignature: 0,
								ActiveParameter: activeParam,
							}
						}
					}
				}
			}
		}

		// UFCS instance method `expr.method(` -> resolve `expr`'s receiver type using shadow sentinel!
		dotByteOff := dotTok.Span.End.Offset
		shadowSrc := src[:dotByteOff] + completionSentinel + src[offset:]
		overlays := s.snapshotOverlays()
		overlays[filePath] = shadowSrc
		sRep := diag.NewReporter()
		sProg := sema.CheckForIDE(filePath, overlays, s.projectRootFor(filePath), sRep)
		sMod := findModuleForPath(sProg, filePath)
		if sMod != nil && sMod.File != nil {
			if mem := findSentinelMemberExpr(sMod.File); mem != nil {
				if bInf := lookupExprInfo(sProg, mem.Base); bInf != nil && bInf.Type != nil {
					_, isOpt := bInf.Type.(*types.Optional)
					recvTy := unwrapOptAndRef(bInf.Type)
					if mset, ok := sProg.MethodTable[sema.TypeMethodKey(recvTy)]; ok {
						if fi := mset[calleeName]; fi != nil && !fi.IsStaticAssoc {
							return s.makeSigHelpFromFuncInst(sProg, sMod, fi, true, activeParam)
						}
					}
					baseName, recvArgs := baseTypeNameAndArgs(recvTy)
					if baseName != "" {
						if gset, ok := sProg.GenMethods[baseName]; ok {
							if gtmpl := gset[calleeName]; gtmpl != nil && gtmpl.MethodDecl != nil {
								return s.makeSigHelpFromGenericMethod(sProg, sMod, recvTy.String(), gtmpl.MethodDecl, recvArgs, true, activeParam)
							}
						}
					}
					for _, bm := range BuiltinMethodsForType(recvTy, isOpt) {
						if bm.Name == calleeName {
							lbl := fmt.Sprintf("%s.%s(%s)", recvTy.String(), bm.Name, strings.Join(bm.Params, ", "))
							if bm.ReturnType != "" && bm.ReturnType != "void" {
								lbl += ": " + bm.ReturnType
							}
							return makeSimpleSigHelp(lbl, bm.Params, activeParam, bm.Summary)
						}
					}
				}
			}
		}
		return nil
	}

	// Case 2: Unqualified call `fn(` / `println(` / `alloc_raw[u8](`
	if mod != nil {
		if sym := lookupTopSym(mod, calleeName); sym != nil {
			if sym.FuncInst != nil {
				return s.makeSigHelpFromFuncInst(prog, mod, sym.FuncInst, false, activeParam)
			}
			if sym.ExternFn != nil {
				var ps []string
				for _, p := range sym.ExternFn.Sig.Params {
					ps = append(ps, fmt.Sprintf("%s: %s%s", p.Name, p.Mode.Prefix(), p.Type.String()))
				}
				docText := "C11 extern function"
				if sym.ExternFn.Doc != "" {
					docText = s.renderDocMarkdown(prog, mod.Path, "", sym.ExternFn.Doc, "", 0, false)
				}
				return makeSimpleSigHelp("extern "+calleeName+sym.ExternFn.Sig.String(), ps, activeParam, docText)
			}
		}
		if gfn := lookupTopGenFunc(mod, calleeName); gfn != nil {
			return s.makeSigHelpFromGenericVar(prog, mod, gfn, activeParam)
		}
	}
	if bf := LookupBuiltinFunc(calleeName); bf != nil {
		return makeSimpleSigHelp(bf.Signature, bf.Params, activeParam, bf.Summary)
	}

	return nil
}

func (s *Server) makeSigHelpFromFuncInst(prog *sema.CheckedProgram, mod *sema.Module, fi *sema.FuncInstance, dropSelf bool, active int) *SignatureHelp {
	startIdx := 0
	if dropSelf && fi.IsMethod && !fi.IsStaticAssoc && len(fi.Sig.Params) > 0 {
		startIdx = 1
	}
	pDocs := parseParamDocs(fi.Doc)
	modPath := ""
	if fi.Mod != nil && fi.Mod.Path != "" {
		modPath = fi.Mod.Path
	} else if mod != nil {
		modPath = mod.Path
	}
	curType := ""
	if fi.IsMethod {
		if dot := strings.IndexByte(fi.Name, '.'); dot > 0 {
			curType = fi.Name[:dot]
		}
	}
	var pInfos []ParameterInformation
	var pLabels []string
	for i := startIdx; i < len(fi.Sig.Params); i++ {
		p := fi.Sig.Params[i]
		pl := fmt.Sprintf("%s: %s%s", p.Name, p.Mode.Prefix(), p.Type.String())
		pLabels = append(pLabels, pl)
		pi := ParameterInformation{Label: pl}
		if pd := pDocs[p.Name]; pd != "" {
			renderedPD := s.renderDocMarkdown(prog, modPath, curType, pd, "", 0, false)
			pi.Documentation = &MarkupContent{Kind: "markdown", Value: renderedPD}
		}
		pInfos = append(pInfos, pi)
	}
	lbl := fmt.Sprintf("%s(%s)", fi.Name, strings.Join(pLabels, ", "))
	if fi.Sig.Ret != nil && !fi.Sig.Ret.Equals(types.TypeVoid) {
		lbl += " " + fi.Sig.Ret.String() // Lowlight signature style: `(a: int) int`
	}
	if active >= len(pInfos) && len(pInfos) > 0 {
		active = len(pInfos) - 1
	}
	var doc *MarkupContent
	if rendered := s.renderDocMarkdown(prog, modPath, curType, fi.Doc, "", 0, false); rendered != "" {
		doc = &MarkupContent{Kind: "markdown", Value: rendered}
	}
	return &SignatureHelp{
		Signatures: []SignatureInformation{{
			Label:         lbl,
			Documentation: doc,
			Parameters:    pInfos,
		}},
		ActiveSignature: 0,
		ActiveParameter: active,
	}
}

func (s *Server) makeSigHelpFromGenericVar(prog *sema.CheckedProgram, mod *sema.Module, gfn *sema.GenericFuncTemplate, active int) *SignatureHelp {
	fl, ok := gfn.VarDecl.Value.(*ast.FuncLit)
	if !ok {
		return nil
	}
	modPath := ""
	if gfn.Mod != nil && gfn.Mod.Path != "" {
		modPath = gfn.Mod.Path
	} else if mod != nil {
		modPath = mod.Path
	}
	rawDoc := gfn.VarDecl.Doc
	pDocs := parseParamDocs(rawDoc)
	var pInfos []ParameterInformation
	for _, p := range fl.Params {
		pl := fmt.Sprintf("%s: %s", p.Name, formatTypeExpr(p.Type))
		pi := ParameterInformation{Label: pl}
		if pd := pDocs[p.Name]; pd != "" {
			pi.Documentation = &MarkupContent{
				Kind:  "markdown",
				Value: s.renderDocMarkdown(prog, modPath, "", pd, "", 0, false),
			}
		}
		pInfos = append(pInfos, pi)
	}
	if active >= len(pInfos) && len(pInfos) > 0 {
		active = len(pInfos) - 1
	}
	summary := "Generic function"
	if rendered := s.renderDocMarkdown(prog, modPath, "", rawDoc, "", 0, false); rendered != "" {
		summary = rendered
	}
	return &SignatureHelp{
		Signatures: []SignatureInformation{{
			Label:         gfn.Name + formatGenericVarDeclSig(gfn.VarDecl),
			Documentation: &MarkupContent{Kind: "markdown", Value: summary},
			Parameters:    pInfos,
		}},
		ActiveSignature: 0,
		ActiveParameter: active,
	}
}

func (s *Server) makeSigHelpFromGenericMethod(prog *sema.CheckedProgram, mod *sema.Module, recvStr string, decl *ast.MethodDecl, recvArgs []types.Type, dropSelf bool, active int) *SignatureHelp {
	subst := make(map[string]string)
	for i, tp := range decl.RecvTypeParams {
		if i < len(recvArgs) && recvArgs[i] != nil {
			subst[tp] = recvArgs[i].String()
		}
	}
	modPath := decl.Span.Start.File
	if modPath == "" && mod != nil {
		modPath = mod.Path
	}
	pDocs := parseParamDocs(decl.Doc)
	var pLabels []string
	var pInfos []ParameterInformation
	if !dropSelf && decl.RecvMode != ast.ReceiverMode(-1) {
		sl := "self: " + recvStr
		pLabels = append(pLabels, sl)
		pInfos = append(pInfos, ParameterInformation{Label: sl})
	}
	for _, p := range decl.Params {
		pl := fmt.Sprintf("%s: %s", p.Name, formatTypeExprSubst(p.Type, subst))
		pLabels = append(pLabels, pl)
		pi := ParameterInformation{Label: pl}
		if pd := pDocs[p.Name]; pd != "" {
			pi.Documentation = &MarkupContent{
				Kind:  "markdown",
				Value: s.renderDocMarkdown(prog, modPath, decl.RecvType, pd, "", 0, false),
			}
		}
		pInfos = append(pInfos, pi)
	}
	lbl := fmt.Sprintf("%s.%s(%s)", recvStr, decl.Name, strings.Join(pLabels, ", "))
	if decl.Ret != nil {
		lbl += " " + formatTypeExprSubst(decl.Ret, subst)
	}
	if active >= len(pInfos) && len(pInfos) > 0 {
		active = len(pInfos) - 1
	}
	var doc *MarkupContent
	if rendered := s.renderDocMarkdown(prog, modPath, decl.RecvType, decl.Doc, "", 0, false); rendered != "" {
		doc = &MarkupContent{Kind: "markdown", Value: rendered}
	}
	return &SignatureHelp{
		Signatures: []SignatureInformation{{
			Label:         lbl,
			Documentation: doc,
			Parameters:    pInfos,
		}},
		ActiveSignature: 0,
		ActiveParameter: active,
	}
}

func makeSimpleSigHelp(label string, params []string, active int, summary string) *SignatureHelp {
	var pInfos []ParameterInformation
	pDocs := parseParamDocs(summary)
	for _, p := range params {
		pi := ParameterInformation{Label: p}
		if colon := strings.IndexByte(p, ':'); colon > 0 {
			pName := strings.TrimSpace(p[:colon])
			if pd := pDocs[pName]; pd != "" {
				pi.Documentation = &MarkupContent{Kind: "markdown", Value: pd}
			}
		}
		pInfos = append(pInfos, pi)
	}
	if active >= len(pInfos) && len(pInfos) > 0 {
		active = len(pInfos) - 1
	}
	var doc *MarkupContent
	if summary != "" {
		doc = &MarkupContent{Kind: "markdown", Value: summary}
	}
	return &SignatureHelp{
		Signatures: []SignatureInformation{{
			Label:         label,
			Documentation: doc,
			Parameters:    pInfos,
		}},
		ActiveSignature: 0,
		ActiveParameter: active,
	}
}

// -----------------------------------------------------------------------------
// Shared Helpers
// -----------------------------------------------------------------------------

func findTokenAtOffset(filePath string, src string, offset int) (token.Token, bool) {
	rep := diag.NewReporter()
	toks := lexer.Lex(filePath, src, rep)
	return findTokenInSlice(toks, offset)
}

func (s *Server) findTokenAtOffset(filePath string, src string, offset int) (token.Token, bool) {
	toks := s.getLexedTokens(filePath, src)
	return findTokenInSlice(toks, offset)
}

func findTokenInSlice(toks []token.Token, offset int) (token.Token, bool) {
	idx := sort.Search(len(toks), func(i int) bool {
		return toks[i].Span.Start.Offset > offset
	})
	var candidate *token.Token
	for i := idx - 1; i >= 0; i-- {
		tk := &toks[i]
		if tk.Kind == token.EOF || tk.AutoSemi {
			continue
		}
		if tk.Span.Start.Offset <= offset && offset < tk.Span.End.Offset {
			return *tk, true
		}
		if offset == tk.Span.End.Offset && (tk.Kind == token.Ident || tk.Kind == token.OptIdent || tk.Kind >= token.KwLet) {
			if candidate == nil {
				candidate = tk
			}
		}
		if tk.Span.End.Offset < offset {
			break
		}
	}
	if candidate != nil {
		return *candidate, true
	}
	return token.Token{}, false
}

func exprInfoFromBM(prog *sema.CheckedProgram, bm *sema.BodyMaps, e ast.Expr) *sema.ExprInfo {
	if bm != nil {
		if inf, ok := bm.ExprInfos[e]; ok {
			return inf
		}
	}
	return lookupExprInfo(prog, e)
}

func unwrapOptAndRef(t types.Type) types.Type {
	for t != nil {
		switch v := t.(type) {
		case *types.Optional:
			t = v.Elem
		case *types.Ref:
			t = v.Elem
		default:
			return t
		}
	}
	return nil
}

func lookupTopSym(mod *sema.Module, name string) *sema.Symbol {
	if mod == nil {
		return nil
	}
	if s, ok := mod.Symbols[name]; ok {
		return s
	}
	for _, imp := range mod.Imports {
		if s, ok := imp.Symbols[name]; ok {
			return s
		}
	}
	return nil
}

func lookupTopGenFunc(mod *sema.Module, name string) *sema.GenericFuncTemplate {
	if mod == nil {
		return nil
	}
	if g, ok := mod.GenericFuncs[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if g, ok := imp.GenericFuncs[name]; ok {
			return g
		}
	}
	return nil
}

func resolveImportFromMod(prog *sema.CheckedProgram, mod *sema.Module, raw string) string {
	path := raw
	if !strings.HasSuffix(path, ".ll") {
		path += ".ll"
	}
	candidates := []string{filepath.Join(mod.Dir, path)}
	for _, m := range prog.Modules {
		if strings.HasSuffix(filepath.ToSlash(m.Path), filepath.ToSlash(path)) {
			return m.Path
		}
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if st, err2 := os.Stat(abs); err2 == nil && !st.IsDir() {
				return abs
			}
		}
	}
	return ""
}

func rangesOverlap(a, b Range) bool {
	if posLess(a.End, b.Start) || posLess(b.End, a.Start) {
		return false
	}
	return true
}

func posLess(a, b Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Character < b.Character
}

func extractQuotedWord(msg string) string {
	q1 := strings.IndexByte(msg, '"')
	if q1 < 0 {
		return ""
	}
	rest := msg[q1+1:]
	q2 := strings.IndexByte(rest, '"')
	if q2 < 0 {
		return ""
	}
	return rest[:q2]
}

func findWordRangeInSpan(src string, rng Range, word string) (Range, bool) {
	sOff := LSPToOffset(src, rng.Start)
	eOff := LSPToOffset(src, rng.End)
	if eOff > len(src) {
		eOff = len(src)
	}
	if sOff >= eOff {
		return Range{}, false
	}
	idx := strings.Index(src[sOff:eOff], word)
	if idx < 0 {
		return Range{}, false
	}
	wStart := sOff + idx
	wEnd := wStart + len(word)
	return Range{
		Start: OffsetToLSP(src, wStart),
		End:   OffsetToLSP(src, wEnd),
	}, true
}

func getLineIndent(src string, lineIdx int) string {
	lines := strings.Split(src, "\n")
	if lineIdx < 0 || lineIdx >= len(lines) {
		return ""
	}
	line := lines[lineIdx]
	trimmed := strings.TrimLeft(line, " \t")
	return line[:len(line)-len(trimmed)]
}

func findMatchInsertionPoint(src string, matchRange Range) (Position, bool) {
	startOff := LSPToOffset(src, matchRange.Start)
	endOff := LSPToOffset(src, matchRange.End)
	if endOff > len(src) {
		endOff = len(src)
	}
	matchText := src[startOff:endOff]
	lines := strings.Split(matchText, "\n")
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "default:") || (strings.HasPrefix(trimmed, "default ") && strings.HasSuffix(trimmed, ":")) {
			lineNum := matchRange.Start.Line + i
			return Position{Line: lineNum, Character: 0}, true
		}
	}
	return matchRange.End, false
}

func formatVariantPatternForMatch(prog *sema.CheckedProgram, mod *sema.Module, variantName string) string {
	parts := strings.Split(variantName, ".")
	if len(parts) != 2 || mod == nil {
		return variantName
	}
	enumName := parts[0]
	vName := parts[1]

	if nom := lookupNominalType(mod, "", enumName); nom != nil {
		if en, ok := nom.(*types.Enum); ok {
			for _, v := range en.Variants {
				if v.Name == vName {
					if v.Kind == types.VariantUnit {
						return variantName
					}
					if v.Kind == types.VariantTuple {
						argName := "val"
						if v.Payload != nil {
							pStr := v.Payload.String()
							switch pStr {
							case "bool":
								argName = "b"
							case "int", "i64", "i32", "u8":
								argName = "n"
							case "float", "f64", "f32":
								argName = "n"
							case "string":
								argName = "s"
							default:
								if strings.HasPrefix(pStr, "[") {
									argName = "items"
								} else if strings.HasPrefix(pStr, "#[") {
									argName = "map"
								}
							}
						}
						if v.Name == "value" {
							argName = "val"
						} else if v.Name == "Err" {
							argName = "err"
						}
						return fmt.Sprintf("%s(%s)", variantName, argName)
					}
				}
			}
		}
	}

	if gen := lookupGenEnum(mod, "", enumName); gen != nil && gen.Decl != nil {
		for _, v := range gen.Decl.Variants {
			if v.Name == vName {
				if v.PayloadType != nil {
					argName := "val"
					if v.Name == "value" {
						argName = "val"
					} else if v.Name == "Err" {
						argName = "err"
					}
					return fmt.Sprintf("%s(%s)", variantName, argName)
				}
				return variantName
			}
		}
	}

	return variantName
}
