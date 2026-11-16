package lsp

import (
	"fmt"
	"os"
	"path/filepath"
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

	tok, ok := findTokenAtOffset(filePath, src, offset)
	if !ok {
		return nil
	}
	rng := ExactSpanToLSP(src, tok.Span)

	// 1. Check AST context (calls, member accesses, local declarations, parameters, type annotations)
	if md := hoverFromAST(prog, mod, offset, tok); md != "" {
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
			ifcTargets = s.allInterfaceImplementingTargets(nom.String(), prog, mod)
		}
		if md := formatNominalTypeHover(prog, nom, ifcTargets); md != "" {
			return &Hover{
				Contents: MarkupContent{Kind: "markdown", Value: md},
				Range:    &rng,
			}
		}
	}
	if gst := lookupGenStruct(mod, "", word); gst != nil {
		return &Hover{
			Contents: MarkupContent{Kind: "markdown", Value: formatStructDeclHover(prog, mod, gst.Decl, nil)},
			Range:    &rng,
		}
	}
	if gen := lookupGenEnum(mod, "", word); gen != nil {
		return &Hover{
			Contents: MarkupContent{Kind: "markdown", Value: formatEnumDeclHover(gen.Decl)},
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
			Contents: MarkupContent{Kind: "markdown", Value: formatSymbolHover(sym)},
			Range:    &rng,
		}
	}
	if gfn := lookupTopGenFunc(mod, word); gfn != nil {
		return &Hover{
			Contents: MarkupContent{
				Kind:  "markdown",
				Value: fmt.Sprintf("```lowlight\nbind %s%s\n```\nGeneric function template.", gfn.Name, formatGenericVarDeclSig(gfn.VarDecl)),
			},
			Range: &rng,
		}
	}
	if targetMod, isMod := mod.Aliased[word]; isMod {
		return &Hover{
			Contents: MarkupContent{
				Kind:  "markdown",
				Value: fmt.Sprintf("```lowlight\nimport %q as %s\n```\nResolved module: `%s`", filepath.Base(targetMod.Path), word, targetMod.Path),
			},
			Range: &rng,
		}
	}

	// 4. Built-in functions, intrinsics, built-in types, keywords & attributes
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

func hoverFromAST(prog *sema.CheckedProgram, mod *sema.Module, offset int, tok token.Token) string {
	for _, d := range mod.File.Decls {
		if !spanContainsOffset(d.GetSpan(), offset) {
			continue
		}
		switch decl := d.(type) {
		case *ast.ImportDecl:
			resolved := resolveImportFromMod(prog, mod, decl.Path)
			if resolved != "" {
				return fmt.Sprintf("```lowlight\nimport %q\n```\nModule path: `%s`", decl.Path, resolved)
			}
		case *ast.StructDecl:
			for _, f := range decl.Fields {
				if spanContainsOffset(f.Span, offset) && tok.Text == f.Name {
					if st, ok := mod.Types[decl.Name].(*types.Struct); ok {
						if sf, _ := st.FieldByName(f.Name); sf != nil {
							return formatStructFieldHover(st.String(), sf.Name, sf.Type)
						}
					}
					return fmt.Sprintf("```lowlight\n%s.%s: %s\n```", decl.Name, f.Name, formatTypeExpr(f.Type))
				}
			}
		case *ast.EnumDecl:
			if en, ok := mod.Types[decl.Name].(*types.Enum); ok {
				for _, v := range decl.Variants {
					if spanContainsOffset(v.Span, offset) && tok.Text == v.Name {
						if ev, ok2 := en.VariantByName(v.Name); ok2 {
							return formatEnumVariantHover(en, ev)
						}
					}
				}
			}
		case *ast.VarDecl:
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				fi := findFuncInstanceForDecl(prog, mod, decl.Span)
				if fi != nil {
					for i, p := range fl.Params {
						if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i < len(fi.ParamSyms) {
							return formatSymbolHover(fi.ParamSyms[i])
						}
					}
				}
				var bm *sema.BodyMaps
				if fi != nil {
					bm = fi.BodyMaps
				}
				if md := hoverInExpr(prog, mod, bm, fl.Body, offset, tok); md != "" {
					return md
				}
			} else if decl.Value != nil {
				if md := hoverInExpr(prog, mod, prog.TopBodyMaps, decl.Value, offset, tok); md != "" {
					return md
				}
			}
		case *ast.MethodDecl:
			fi := findFuncInstanceForDecl(prog, mod, decl.Span)
			if fi != nil {
				pOffset := 0
				if !fi.IsStaticAssoc {
					pOffset = 1
				}
				for i, p := range decl.Params {
					if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i+pOffset < len(fi.ParamSyms) {
						return formatSymbolHover(fi.ParamSyms[i+pOffset])
					}
				}
			}
			var bm *sema.BodyMaps
			if fi != nil {
				bm = fi.BodyMaps
			}
			if md := hoverInExpr(prog, mod, bm, decl.Body, offset, tok); md != "" {
				return md
			}
		}
	}
	return ""
}

func hoverInExpr(prog *sema.CheckedProgram, mod *sema.Module, bm *sema.BodyMaps, e ast.Expr, offset int, tok token.Token) string {
	if e == nil || !spanContainsOffset(e.GetSpan(), offset) {
		return ""
	}
	switch v := e.(type) {
	case *ast.BlockExpr:
		for _, st := range v.Stmts {
			if spanContainsOffset(st.GetSpan(), offset) {
				return hoverInStmt(prog, mod, bm, st, offset, tok)
			}
		}
		return hoverInExpr(prog, mod, bm, v.Tail, offset, tok)

	case *ast.StmtBodyExpr:
		return hoverInStmt(prog, mod, bm, v.Stmt, offset, tok)

	case *ast.IfExpr:
		if v.CondKind == ast.IfCondOptVar && tok.Text == v.OptVar && (v.Then == nil || offset < v.Then.GetSpan().Start.Offset) {
			if bm != nil {
				if orig := bm.IfOriginSyms[v]; orig != nil {
					return formatSymbolHover(orig)
				}
			}
		}
		if md := hoverInExpr(prog, mod, bm, v.Cond, offset, tok); md != "" {
			return md
		}
		if v.Then != nil && spanContainsOffset(v.Then.GetSpan(), offset) {
			if md := hoverInExpr(prog, mod, bm, v.Then, offset, tok); md != "" {
				return md
			}
		}
		return hoverInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.ForExpr:
		if md := hoverInExpr(prog, mod, bm, v.CondOrIter, offset, tok); md != "" {
			return md
		}
		if md := hoverInExpr(prog, mod, bm, v.RangeEnd, offset, tok); md != "" {
			return md
		}
		if v.Body != nil && spanContainsOffset(v.Body.GetSpan(), offset) {
			return hoverInExpr(prog, mod, bm, v.Body, offset, tok)
		}
		return hoverInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.WhileExpr:
		if md := hoverInExpr(prog, mod, bm, v.CondOrIter, offset, tok); md != "" {
			return md
		}
		if md := hoverInExpr(prog, mod, bm, v.RangeEnd, offset, tok); md != "" {
			return md
		}
		if v.Body != nil && spanContainsOffset(v.Body.GetSpan(), offset) {
			return hoverInExpr(prog, mod, bm, v.Body, offset, tok)
		}
		return hoverInExpr(prog, mod, bm, v.Else, offset, tok)

	case *ast.MatchExpr:
		for _, s := range v.Scrutinees {
			if md := hoverInExpr(prog, mod, bm, s, offset, tok); md != "" {
				return md
			}
		}
		for _, arm := range v.Arms {
			for _, pat := range arm.AllPatterns() {
				if ep, ok := pat.(*ast.ExprPattern); ok && spanContainsOffset(ep.Span, offset) {
					if md := hoverInExpr(prog, mod, bm, ep.Expr, offset, tok); md != "" {
						return md
					}
				}
				if cp, ok := pat.(*ast.ContainsPattern); ok && spanContainsOffset(cp.Span, offset) {
					if md := hoverInExpr(prog, mod, bm, cp.Container, offset, tok); md != "" {
						return md
					}
				}
				if vp, ok := pat.(*ast.VariantPattern); ok && spanContainsOffset(vp.Span, offset) {
					if bm != nil {
						syms := bm.PatternSyms[vp]
						for i, b := range vp.Bindings {
							if spanContainsOffset(b.Span, offset) && tok.Text == b.BindName && i < len(syms) && syms[i] != nil {
								return formatSymbolHover(syms[i])
							}
						}
					}
					if md := hoverInExpr(prog, mod, bm, vp.Target, offset, tok); md != "" {
						return md
					}
				}
			}
			if spanContainsOffset(arm.Body.GetSpan(), offset) {
				return hoverInExpr(prog, mod, bm, arm.Body, offset, tok)
			}
		}

	case *ast.FuncLit:
		lbm := bm
		for _, li := range prog.Lambdas {
			if li.Lit == v {
				lbm = li.BodyMaps
				for i, p := range v.Params {
					if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i < len(li.ParamSyms) {
						return formatSymbolHover(li.ParamSyms[i])
					}
				}
				break
			}
		}
		return hoverInExpr(prog, mod, lbm, v.Body, offset, tok)

	case *ast.CallExpr:
		// First check arguments so nested expressions win over outer CallExpr!
		for _, a := range v.Args {
			if spanContainsOffset(a.GetSpan(), offset) {
				return hoverInExpr(prog, mod, bm, a, offset, tok)
			}
		}
		if spanContainsOffset(v.Callee.GetSpan(), offset) {
			// If `v.Callee` is `MemberExpr` (`pt.translate`) and cursor is on `pt`, hover `pt`!
			if mem, isMem := v.Callee.(*ast.MemberExpr); isMem && spanContainsOffset(mem.Base.GetSpan(), offset) {
				return hoverInExpr(prog, mod, bm, mem.Base, offset, tok)
			}
			if inf := exprInfoFromBM(prog, bm, v); inf != nil {
				if md := formatCallHover(v, inf); md != "" {
					return md
				}
			}
			return hoverInExpr(prog, mod, bm, v.Callee, offset, tok)
		}

	case *ast.MemberExpr:
		if spanContainsOffset(v.Base.GetSpan(), offset) {
			return hoverInExpr(prog, mod, bm, v.Base, offset, tok)
		}
		inf := exprInfoFromBM(prog, bm, v)
		if inf != nil {
			if inf.EnumVariant != nil && inf.EnumType != nil {
				return formatEnumVariantHover(inf.EnumType, inf.EnumVariant)
			}
			if inf.Sym != nil {
				return formatSymbolHover(inf.Sym)
			}
			if baseInf := exprInfoFromBM(prog, bm, v.Base); baseInf != nil && baseInf.Type != nil {
				recv := unwrapOptAndRef(baseInf.Type)
				if st, isSt := recv.(*types.Struct); isSt {
					if sf, _ := st.FieldByName(v.Member); sf != nil {
						return formatStructFieldHover(st.String(), sf.Name, inf.Type)
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
				return hoverInExpr(prog, mod, bm, f.Value, offset, tok)
			}
			if spanContainsOffset(f.Span, offset) && tok.Text == f.Name {
				if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.StructType != nil {
					if sf, _ := inf.StructType.FieldByName(f.Name); sf != nil {
						return formatStructFieldHover(inf.StructType.String(), sf.Name, sf.Type)
					}
				}
			}
		}
		if inf := exprInfoFromBM(prog, bm, v); inf != nil {
			if inf.StructType != nil && tok.Text == v.TypeName {
				return formatNominalTypeHover(prog, inf.StructType, nil)
			}
		}

	case *ast.IdentExpr:
		if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.Sym != nil {
			return formatSymbolHover(inf.Sym)
		}

	case *ast.BinaryExpr:
		if md := hoverInExpr(prog, mod, bm, v.Left, offset, tok); md != "" {
			return md
		}
		return hoverInExpr(prog, mod, bm, v.Right, offset, tok)

	case *ast.UnaryExpr:
		return hoverInExpr(prog, mod, bm, v.Operand, offset, tok)

	case *ast.UnsafeExpr:
		return hoverInExpr(prog, mod, bm, v.Expr, offset, tok)

	case *ast.IndexExpr:
		if md := hoverInExpr(prog, mod, bm, v.Base, offset, tok); md != "" {
			return md
		}
		return hoverInExpr(prog, mod, bm, v.Index, offset, tok)

	case *ast.SliceExpr:
		if md := hoverInExpr(prog, mod, bm, v.Base, offset, tok); md != "" {
			return md
		}
		if md := hoverInExpr(prog, mod, bm, v.Low, offset, tok); md != "" {
			return md
		}
		return hoverInExpr(prog, mod, bm, v.High, offset, tok)

	case *ast.RangeExpr:
		if md := hoverInExpr(prog, mod, bm, v.Low, offset, tok); md != "" {
			return md
		}
		return hoverInExpr(prog, mod, bm, v.High, offset, tok)

	case *ast.TupleLit:
		for _, el := range v.Elems {
			if md := hoverInExpr(prog, mod, bm, el, offset, tok); md != "" {
				return md
			}
		}

	case *ast.ArrayLit:
		for _, el := range v.Elems {
			if md := hoverInExpr(prog, mod, bm, el, offset, tok); md != "" {
				return md
			}
		}

	case *ast.VecLit:
		for _, el := range v.Elems {
			if md := hoverInExpr(prog, mod, bm, el, offset, tok); md != "" {
				return md
			}
		}

	case *ast.SetLit:
		for _, el := range v.Elems {
			if md := hoverInExpr(prog, mod, bm, el, offset, tok); md != "" {
				return md
			}
		}

	case *ast.MapLit:
		for _, ent := range v.Entries {
			if md := hoverInExpr(prog, mod, bm, ent.Key, offset, tok); md != "" {
				return md
			}
			if md := hoverInExpr(prog, mod, bm, ent.Value, offset, tok); md != "" {
				return md
			}
		}

	case *ast.InterpStringExpr:
		for _, sub := range v.Exprs {
			if md := hoverInExpr(prog, mod, bm, sub, offset, tok); md != "" {
				return md
			}
		}
	}
	return ""
}

func hoverInStmt(prog *sema.CheckedProgram, mod *sema.Module, bm *sema.BodyMaps, st ast.Stmt, offset int, tok token.Token) string {
	switch s := st.(type) {
	case *ast.VarStmt:
		if spanContainsOffset(s.Value.GetSpan(), offset) {
			return hoverInExpr(prog, mod, bm, s.Value, offset, tok)
		}
		if s.FallbackUnwind != nil && s.FallbackUnwind.Value != nil && spanContainsOffset(s.FallbackUnwind.Value.GetSpan(), offset) {
			return hoverInExpr(prog, mod, bm, s.FallbackUnwind.Value, offset, tok)
		}
		if s.IsUnpack && bm != nil {
			syms := bm.UnpackVarSymbols[s]
			for i, uv := range s.UnpackVars {
				if tok.Text == uv.Name && i < len(syms) && syms[i] != nil {
					return formatSymbolHover(syms[i])
				}
			}
		}
		if tok.Text == s.Name && bm != nil {
			if sym := bm.VarSymbols[s]; sym != nil {
				return formatSymbolHover(sym)
			}
		}
	case *ast.AssignStmt:
		if s.IsUnpack {
			for _, tgt := range s.UnpackTargets {
				if spanContainsOffset(tgt.GetSpan(), offset) {
					return hoverInExpr(prog, mod, bm, tgt, offset, tok)
				}
			}
			return hoverInExpr(prog, mod, bm, s.Value, offset, tok)
		}
		if spanContainsOffset(s.Target.GetSpan(), offset) {
			return hoverInExpr(prog, mod, bm, s.Target, offset, tok)
		}
		return hoverInExpr(prog, mod, bm, s.Value, offset, tok)
	case *ast.UnwindStmt:
		return hoverInExpr(prog, mod, bm, s.Value, offset, tok)
	case *ast.ExprStmt:
		return hoverInExpr(prog, mod, bm, s.Expr, offset, tok)
	}
	return ""
}

func formatSymbolHover(sym *sema.Symbol) string {
	if sym == nil {
		return ""
	}
	if sym.FuncInst != nil {
		return fmt.Sprintf("```lowlight\nbind %s = %s\n```", sym.FuncInst.Name, formatFullFuncSig(sym.FuncInst))
	}
	if sym.ExternFn != nil {
		return fmt.Sprintf("```lowlight\nextern %s: %s\n```\n*C11 ABI function* (requires `unsafe`)", sym.Name, sym.ExternFn.Sig.String())
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
	return fmt.Sprintf("```lowlight\n%s %s: %s\n```\n%s", kw, sym.Name, tyStr, strings.Join(notes, " · "))
}

func formatCallHover(call *ast.CallExpr, inf *sema.ExprInfo) string {
	switch inf.CallKind {
	case sema.CallDirectFunc:
		if inf.TargetFunc != nil {
			return fmt.Sprintf("```lowlight\nbind %s = %s\n```", inf.TargetFunc.Name, formatFullFuncSig(inf.TargetFunc))
		}
	case sema.CallExternC:
		if inf.TargetExtern != nil {
			return fmt.Sprintf("```lowlight\nextern %s: %s\n```\n*C11 ABI function* (requires `unsafe`)", inf.TargetExtern.Name, inf.TargetExtern.Sig.String())
		}
	case sema.CallInterfaceMethod:
		if inf.InterfaceMeth != nil {
			return fmt.Sprintf("```lowlight\n%s: %s\n```\n*Dynamic interface method dispatch*",
				inf.InterfaceMeth.Name, formatInterfaceMethodSig(*inf.InterfaceMeth, inf.IsOptChain))
		}
	case sema.CallBuiltinFunc, sema.CallIntrinsic:
		if bf := LookupBuiltinFunc(inf.BuiltinName); bf != nil {
			return fmt.Sprintf("```lowlight\n%s\n```\n%s", bf.Signature, bf.Summary)
		}
	case sema.CallBuiltinMethod:
		if mem, ok := call.Callee.(*ast.MemberExpr); ok {
			return fmt.Sprintf("```lowlight\n.%s(...) %s\n```\nBuilt-in method `%s`.", mem.Member, inf.Type.String(), inf.BuiltinName)
		}
	case sema.CallEnumVariant:
		if inf.EnumType != nil && inf.EnumVariant != nil {
			return formatEnumVariantHover(inf.EnumType, inf.EnumVariant)
		}
	}
	return ""
}

func formatEnumVariantHover(en *types.Enum, ev *types.EnumVariant) string {
	if en == nil || ev == nil {
		return ""
	}
	if !en.IsTagged {
		return fmt.Sprintf("```lowlight\n%s.%s = %d // enum %s (%s)\n```",
			en.Name, ev.Name, ev.Value, en.Name, en.Underlying.String())
	}
	if ev.Kind == types.VariantTuple {
		pStr := "any"
		if ev.Payload != nil {
			pStr = ev.Payload.String()
		}
		return fmt.Sprintf("```lowlight\n%s.%s(%s)\n```\nTagged union variant.", en.String(), ev.Name, pStr)
	}
	return fmt.Sprintf("```lowlight\n%s.%s\n```\nTagged union unit variant.", en.String(), ev.Name)
}

func formatStructFieldHover(structName, fieldName string, fieldTy types.Type) string {
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
	return fmt.Sprintf("```lowlight\n%s.%s: %s\n```%s", structName, fieldName, tyStr, note)
}

func formatNominalTypeHover(prog *sema.CheckedProgram, ty types.Type, ifcTargets []sema.ImplementationTarget) string {
	switch v := ty.(type) {
	case *types.Struct:
		var lines []string
		modPrefix := "define "
		if v.Packed {
			modPrefix = "define packed "
		}
		lines = append(lines, fmt.Sprintf("%s%s struct [", modPrefix, v.String()))
		for _, f := range v.Fields {
			def := ""
			if f.HasDefault {
				def = " = ..."
			}
			lines = append(lines, fmt.Sprintf("    %s: %s%s,", f.Name, f.Type.String(), def))
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
		return res

	case *types.Enum:
		var lines []string
		if !v.IsTagged {
			lines = append(lines, fmt.Sprintf("define %s enum %s [", v.Name, v.Underlying.String()))
			for _, ev := range v.Variants {
				lines = append(lines, fmt.Sprintf("    %s = %d,", ev.Name, ev.Value))
			}
			lines = append(lines, "]")
			res := fmt.Sprintf("```lowlight\n%s\n```\nPlain tag enum.", strings.Join(lines, "\n"))
			if prog != nil {
				if ifcs := prog.ImplementedInterfaces(v); len(ifcs) > 0 {
					var inames []string
					for _, ifc := range ifcs {
						inames = append(inames, "`"+ifc.Name+"`")
					}
					res += "\n\n**Implements:** " + strings.Join(inames, ", ")
				}
			}
			return res
		}
		lines = append(lines, fmt.Sprintf("define %s enum [", v.String()))
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
		return res

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
		return res

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
		}
		return res
	}
	_ = prog
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
		targets := s.allInterfaceImplementingTargets(decl.Name, prog, nil)
		if len(targets) > 0 {
			var tnames []string
			for _, tgt := range targets {
				tnames = append(tnames, "`"+tgt.Name+"`")
			}
			res += "\n\n**Implemented by:** " + strings.Join(tnames, ", ")
		}
	}
	return res
}

func formatStructDeclHover(prog *sema.CheckedProgram, mod *sema.Module, decl *ast.StructDecl, _ map[string]string) string {
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
	return res
}

func formatEnumDeclHover(decl *ast.EnumDecl) string {
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
	lines = append(lines, fmt.Sprintf("define %s%s enum [", decl.Name, tpStr))
	for _, v := range decl.Variants {
		if v.PayloadType != nil {
			lines = append(lines, fmt.Sprintf("    %s: %s,", v.Name, formatTypeExpr(v.PayloadType)))
		} else {
			lines = append(lines, fmt.Sprintf("    %s,", v.Name))
		}
	}
	lines = append(lines, "]")
	return fmt.Sprintf("```lowlight\n%s\n```", strings.Join(lines, "\n"))
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

	tok, ok := findTokenAtOffset(filePath, src, offset)
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

	tok, ok := findTokenAtOffset(filePath, src, offset)
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
		if spanContainsOffset(v.Base.GetSpan(), offset) {
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
						return makeSigHelpFromFuncInst(sym.FuncInst, false, activeParam)
					}
					if gfn := targetMod.GenericFuncs[calleeName]; gfn != nil {
						return makeSigHelpFromGenericVar(gfn, activeParam)
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
								return &SignatureHelp{
									Signatures: []SignatureInformation{{
										Label: lbl,
										Documentation: &MarkupContent{
											Kind:  "markdown",
											Value: fmt.Sprintf("Constructs `%s.%s` enum variant.", en.Name, v.Name),
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
							return makeSigHelpFromFuncInst(fi, false, activeParam)
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
							return &SignatureHelp{
								Signatures: []SignatureInformation{{
									Label: lbl,
									Documentation: &MarkupContent{
										Kind:  "markdown",
										Value: fmt.Sprintf("Constructs generic enum variant `%s.%s`.", gen.Decl.Name, v.Name),
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
							return makeSigHelpFromFuncInst(fi, true, activeParam)
						}
					}
					baseName, recvArgs := baseTypeNameAndArgs(recvTy)
					if baseName != "" {
						if gset, ok := sProg.GenMethods[baseName]; ok {
							if gtmpl := gset[calleeName]; gtmpl != nil && gtmpl.MethodDecl != nil {
								return makeSigHelpFromGenericMethod(recvTy.String(), gtmpl.MethodDecl, recvArgs, true, activeParam)
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
				return makeSigHelpFromFuncInst(sym.FuncInst, false, activeParam)
			}
			if sym.ExternFn != nil {
				var ps []string
				for _, p := range sym.ExternFn.Sig.Params {
					ps = append(ps, fmt.Sprintf("%s: %s%s", p.Name, p.Mode.Prefix(), p.Type.String()))
				}
				return makeSimpleSigHelp("extern "+calleeName+sym.ExternFn.Sig.String(), ps, activeParam, "C11 extern function")
			}
		}
		if gfn := lookupTopGenFunc(mod, calleeName); gfn != nil {
			return makeSigHelpFromGenericVar(gfn, activeParam)
		}
	}
	if bf := LookupBuiltinFunc(calleeName); bf != nil {
		return makeSimpleSigHelp(bf.Signature, bf.Params, activeParam, bf.Summary)
	}

	return nil
}

func makeSigHelpFromFuncInst(fi *sema.FuncInstance, dropSelf bool, active int) *SignatureHelp {
	startIdx := 0
	if dropSelf && fi.IsMethod && !fi.IsStaticAssoc && len(fi.Sig.Params) > 0 {
		startIdx = 1
	}
	var pInfos []ParameterInformation
	var pLabels []string
	for i := startIdx; i < len(fi.Sig.Params); i++ {
		p := fi.Sig.Params[i]
		pl := fmt.Sprintf("%s: %s%s", p.Name, p.Mode.Prefix(), p.Type.String())
		pLabels = append(pLabels, pl)
		pInfos = append(pInfos, ParameterInformation{Label: pl})
	}
	lbl := fmt.Sprintf("%s(%s)", fi.Name, strings.Join(pLabels, ", "))
	if fi.Sig.Ret != nil && !fi.Sig.Ret.Equals(types.TypeVoid) {
		lbl += ": " + fi.Sig.Ret.String()
	}
	if active >= len(pInfos) && len(pInfos) > 0 {
		active = len(pInfos) - 1
	}
	return &SignatureHelp{
		Signatures: []SignatureInformation{{
			Label:      lbl,
			Parameters: pInfos,
		}},
		ActiveSignature: 0,
		ActiveParameter: active,
	}
}

func makeSigHelpFromGenericVar(gfn *sema.GenericFuncTemplate, active int) *SignatureHelp {
	fl, ok := gfn.VarDecl.Value.(*ast.FuncLit)
	if !ok {
		return nil
	}
	var pLabels []string
	for _, p := range fl.Params {
		pLabels = append(pLabels, fmt.Sprintf("%s: %s", p.Name, formatTypeExpr(p.Type)))
	}
	return makeSimpleSigHelp(gfn.Name+formatGenericVarDeclSig(gfn.VarDecl), pLabels, active, "Generic function")
}

func makeSigHelpFromGenericMethod(recvStr string, decl *ast.MethodDecl, recvArgs []types.Type, dropSelf bool, active int) *SignatureHelp {
	subst := make(map[string]string)
	for i, tp := range decl.RecvTypeParams {
		if i < len(recvArgs) && recvArgs[i] != nil {
			subst[tp] = recvArgs[i].String()
		}
	}
	var pLabels []string
	if !dropSelf && decl.RecvMode != ast.ReceiverMode(-1) {
		pLabels = append(pLabels, "self: "+recvStr)
	}
	for _, p := range decl.Params {
		pLabels = append(pLabels, fmt.Sprintf("%s: %s", p.Name, formatTypeExprSubst(p.Type, subst)))
	}
	lbl := fmt.Sprintf("%s.%s(%s)", recvStr, decl.Name, strings.Join(pLabels, ", "))
	if decl.Ret != nil {
		lbl += " " + formatTypeExprSubst(decl.Ret, subst)
	}
	return makeSimpleSigHelp(lbl, pLabels, active, "")
}

func makeSimpleSigHelp(label string, params []string, active int, summary string) *SignatureHelp {
	var pInfos []ParameterInformation
	for _, p := range params {
		pInfos = append(pInfos, ParameterInformation{Label: p})
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
// Quick-Fix Code Actions (`textDocument/codeAction`)
// -----------------------------------------------------------------------------

func (s *Server) computeCodeActions(filePath string, src string, params CodeActionParams) []CodeAction {
	uri := PathToURI(filePath)
	diags := params.Context.Diagnostics
	if len(diags) == 0 {
		// Fall back to server's latest diagnostics overlapping `params.Range`
		for _, d := range s.latestDiagnosticsFor(filePath) {
			if rangesOverlap(d.Range, params.Range) {
				diags = append(diags, d)
			}
		}
	}

	var actions []CodeAction
	for _, d := range diags {
		msg := d.Message

		// 1. Missing `&` on mutable borrow parameter
		if strings.Contains(msg, "borrows mutably") && strings.Contains(msg, "requires `&` at the call site") {
			insertAt := Range{Start: d.Range.Start, End: d.Range.Start}
			actions = append(actions, CodeAction{
				Title:       "Prefix argument with '&' (mutable borrow)",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: insertAt, NewText: "&"}},
					},
				},
			})
		}

		// 2. Unexpected `&` on read-only parameter
		if strings.Contains(msg, "unexpected `&` on read-only parameter") {
			startOff := LSPToOffset(src, d.Range.Start)
			if startOff < len(src) && src[startOff] == '&' {
				ampRange := Range{
					Start: d.Range.Start,
					End:   OffsetToLSP(src, startOff+1),
				}
				actions = append(actions, CodeAction{
					Title:       "Remove unnecessary '&'",
					Kind:        "quickfix",
					Diagnostics: []Diagnostic{d},
					IsPreferred: true,
					Edit: &WorkspaceEdit{
						Changes: map[string][]TextEdit{
							uri: {{Range: ampRange, NewText: ""}},
						},
					},
				})
			}
		}

		// 3. Missing `own` on consuming parameter
		if strings.Contains(msg, "to a consuming `own` parameter") {
			insertStart := Range{Start: d.Range.Start, End: d.Range.Start}
			insertEnd := Range{Start: d.Range.End, End: d.Range.End}
			actions = append(actions,
				CodeAction{
					Title:       "Prefix argument with 'own ' (transfer ownership)",
					Kind:        "quickfix",
					Diagnostics: []Diagnostic{d},
					IsPreferred: true,
					Edit: &WorkspaceEdit{
						Changes: map[string][]TextEdit{
							uri: {{Range: insertStart, NewText: "own "}},
						},
					},
				},
				CodeAction{
					Title:       "Append '.copy()' (keep original valid)",
					Kind:        "quickfix",
					Diagnostics: []Diagnostic{d},
					Edit: &WorkspaceEdit{
						Changes: map[string][]TextEdit{
							uri: {{Range: insertEnd, NewText: ".copy()"}},
						},
					},
				},
			)
		}

		// 4. Optional identifier missing `?` suffix (`rename `x` to `x?``)
		if strings.Contains(msg, "has optional type") && strings.Contains(msg, "must end with `?`") {
			if name := extractQuotedWord(msg); name != "" {
				if editRng, ok := findWordRangeInSpan(src, d.Range, name); ok {
					actions = append(actions, CodeAction{
						Title:       fmt.Sprintf("Rename '%s' to '%s?'", name, name),
						Kind:        "quickfix",
						Diagnostics: []Diagnostic{d},
						IsPreferred: true,
						Edit: &WorkspaceEdit{
							Changes: map[string][]TextEdit{
								uri: {{Range: editRng, NewText: name + "?"}},
							},
						},
					})
				}
			}
		}

		// 5. Non-optional identifier ending with `?` (`ends with `?` but has non-optional type`)
		if strings.Contains(msg, "ends with `?` but has non-optional type") {
			if optName := extractQuotedWord(msg); strings.HasSuffix(optName, "?") {
				plain := strings.TrimSuffix(optName, "?")
				if editRng, ok := findWordRangeInSpan(src, d.Range, optName); ok {
					actions = append(actions, CodeAction{
						Title:       fmt.Sprintf("Rename '%s' to '%s' (remove '?')", optName, plain),
						Kind:        "quickfix",
						Diagnostics: []Diagnostic{d},
						IsPreferred: true,
						Edit: &WorkspaceEdit{
							Changes: map[string][]TextEdit{
								uri: {{Range: editRng, NewText: plain}},
							},
						},
					})
				}
			}
		}

		// 6. `!` instead of `not`
		if strings.Contains(msg, "logical negation in Lowlight uses 'not'") {
			actions = append(actions, CodeAction{
				Title:       "Replace '!' with 'not '",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: d.Range, NewText: "not "}},
					},
				},
			})
		}

		// 7. Missing trailing comma in multiline `[...]` / `(...)`
		if strings.Contains(msg, "missing trailing comma in multiline") {
			// `SpanToLSP` may expand a zero-width span by 1 char; compute exact insertion point before newline/closing bracket!
			insOff := LSPToOffset(src, d.Range.Start)
			if insOff < len(src) && src[insOff] != '\n' && src[insOff] != '\r' && src[insOff] != ' ' && src[insOff] != '\t' && src[insOff] != ']' && src[insOff] != ')' {
				insOff = LSPToOffset(src, d.Range.End)
			}
			insPos := OffsetToLSP(src, insOff)
			actions = append(actions, CodeAction{
				Title:       "Insert trailing comma ','",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: Range{Start: insPos, End: insPos}, NewText: ","}},
					},
				},
			})
		}

		// 8. Optional member access missing `?` before `.`
		if strings.Contains(msg, "on optional value of type") && strings.Contains(msg, "without `?`") {
			sOff := LSPToOffset(src, d.Range.Start)
			eOff := LSPToOffset(src, d.Range.End)
			if eOff > len(src) {
				eOff = len(src)
			}
			dotIdx := strings.LastIndexByte(src[sOff:eOff], '.')
			if dotIdx >= 0 {
				dotPos := OffsetToLSP(src, sOff+dotIdx)
				actions = append(actions, CodeAction{
					Title:       "Use '?.' optional chaining",
					Kind:        "quickfix",
					Diagnostics: []Diagnostic{d},
					IsPreferred: true,
					Edit: &WorkspaceEdit{
						Changes: map[string][]TextEdit{
							uri: {{Range: Range{Start: dotPos, End: dotPos}, NewText: "?"}},
						},
					},
				})
			}
		}

		// 9. Non-exhaustive `match` on enum
		if strings.Contains(msg, "non-exhaustive `match` on enum") && strings.Contains(msg, "missing variant(s):") {
			missingIdx := strings.Index(msg, "missing variant(s):")
			if missingIdx >= 0 {
				rawMissing := strings.TrimSpace(msg[missingIdx+len("missing variant(s):"):])
				if nl := strings.IndexByte(rawMissing, '\n'); nl >= 0 {
					rawMissing = strings.TrimSpace(rawMissing[:nl])
				}
				variantNames := strings.Split(rawMissing, ", ")
				if len(variantNames) > 0 {
					prog := s.getOrCheckProgram(filePath, src)
					mod := findModuleForPath(prog, filePath)
					indent := getLineIndent(src, d.Range.Start.Line)
					armIndent := indent + "    "
					insertPos, hasExistingElse := findMatchInsertionPoint(src, d.Range)

					var whenArms []string
					for _, vn := range variantNames {
						vn = strings.TrimSpace(vn)
						if vn != "" {
							whenArms = append(whenArms, formatVariantPatternForMatch(prog, mod, vn))
						}
					}

					if len(whenArms) > 0 {
						if hasExistingElse {
							var sb strings.Builder
							for _, pat := range whenArms {
								sb.WriteString(armIndent)
								sb.WriteString("when ")
								sb.WriteString(pat)
								sb.WriteString(": panic(\"todo\")\n")
							}
							actions = append(actions, CodeAction{
								Title:       "Generate missing match arms",
								Kind:        "quickfix",
								Diagnostics: []Diagnostic{d},
								IsPreferred: true,
								Edit: &WorkspaceEdit{
									Changes: map[string][]TextEdit{
										uri: {{Range: Range{Start: insertPos, End: insertPos}, NewText: sb.String()}},
									},
								},
							})
						} else {
							// Option 1: Exhaustive with final `default Pattern:`
							var sbExhaustive strings.Builder
							for i, pat := range whenArms {
								sbExhaustive.WriteString("\n")
								sbExhaustive.WriteString(armIndent)
								if i == len(whenArms)-1 {
									sbExhaustive.WriteString("default ")
								} else {
									sbExhaustive.WriteString("when ")
								}
								sbExhaustive.WriteString(pat)
								sbExhaustive.WriteString(": panic(\"todo\")")
							}
							actions = append(actions, CodeAction{
								Title:       "Generate missing match arms (exhaustive 'default Pattern')",
								Kind:        "quickfix",
								Diagnostics: []Diagnostic{d},
								IsPreferred: true,
								Edit: &WorkspaceEdit{
									Changes: map[string][]TextEdit{
										uri: {{Range: Range{Start: insertPos, End: insertPos}, NewText: sbExhaustive.String()}},
									},
								},
							})

							// Option 2: Fallback `default:`
							var sbFallback strings.Builder
							for _, pat := range whenArms {
								sbFallback.WriteString("\n")
								sbFallback.WriteString(armIndent)
								sbFallback.WriteString("when ")
								sbFallback.WriteString(pat)
								sbFallback.WriteString(": panic(\"todo\")")
							}
							sbFallback.WriteString("\n")
							sbFallback.WriteString(armIndent)
							sbFallback.WriteString("default: panic(\"todo\")")
							actions = append(actions, CodeAction{
								Title:       "Generate missing match arms (with fallback 'default:')",
								Kind:        "quickfix",
								Diagnostics: []Diagnostic{d},
								IsPreferred: false,
								Edit: &WorkspaceEdit{
									Changes: map[string][]TextEdit{
										uri: {{Range: Range{Start: insertPos, End: insertPos}, NewText: sbFallback.String()}},
									},
								},
							})
						}
					}
				}
			}
		}

		// 10. Non-exhaustive `match` on `bool`
		if strings.Contains(msg, "non-exhaustive `match` on `bool`") {
			indent := getLineIndent(src, d.Range.Start.Line)
			armIndent := indent + "    "
			insertPos, hasExistingDefault := findMatchInsertionPoint(src, d.Range)
			if !hasExistingDefault {
				actions = append(actions, CodeAction{
					Title:       "Generate missing boolean match arms",
					Kind:        "quickfix",
					Diagnostics: []Diagnostic{d},
					IsPreferred: true,
					Edit: &WorkspaceEdit{
						Changes: map[string][]TextEdit{
							uri: {{Range: Range{Start: insertPos, End: insertPos}, NewText: "\n" + armIndent + "when true: panic(\"todo\")\n" + armIndent + "default: panic(\"todo\")"}},
						},
					},
				})
			}
		}

		// 11. `match` on non-enum type missing catch-all `default:`
		if strings.Contains(msg, "must end with an open catch-all `default:` arm") {
			indent := getLineIndent(src, d.Range.Start.Line)
			armIndent := indent + "    "
			insertPos, _ := findMatchInsertionPoint(src, d.Range)
			actions = append(actions, CodeAction{
				Title:       "Add catch-all 'default:' arm",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: Range{Start: insertPos, End: insertPos}, NewText: "\n" + armIndent + "default: panic(\"todo\")"}},
					},
				},
			})
		}
	}

	testActions := s.computeTestCodeActions(filePath, src, params.Range)
	actions = append(actions, testActions...)

	return actions
}

// -----------------------------------------------------------------------------
// Shared Helpers
// -----------------------------------------------------------------------------

func findTokenAtOffset(filePath string, src string, offset int) (token.Token, bool) {
	rep := diag.NewReporter()
	toks := lexer.Lex(filePath, src, rep)
	var candidate *token.Token
	for i := range toks {
		tk := &toks[i]
		if tk.Kind == token.EOF || tk.AutoSemi {
			continue
		}
		if tk.Span.Start.Offset <= offset && offset < tk.Span.End.Offset {
			return *tk, true
		}
		if offset == tk.Span.End.Offset && (tk.Kind == token.Ident || tk.Kind == token.OptIdent || tk.Kind >= token.KwLet) {
			candidate = tk
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
						if v.Name == "Ok" {
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
					if v.Name == "Ok" {
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
