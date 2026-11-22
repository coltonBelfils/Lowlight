package lsp

import (
	"fmt"
	"io/fs"
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

type refTargetKind int

const (
	refNone refTargetKind = iota
	refLocalVar
	refTopSym
	refMethod
	refType
	refField
	refVariant
	refInterfaceMethod
	refModuleAlias
)

type refTarget struct {
	kind           refTargetKind
	name           string // identifier name (may include `?` for optional local)
	ownerFile      string // normalized file where declaration lives
	declKey        string // canonicalSpanKey of declaration
	ownerTypeKey   string // canonicalSpanKey of owning Struct / Enum / Interface
	ownerTypeName  string // name of owning Struct / Enum / Interface
	localSym       *sema.Symbol
	localAliases   map[*sema.Symbol]bool // linked IfUnwrapSym / IfOriginSym
	ownerBodySpan  diag.Span
	isOptionalName bool
	isBuiltin      bool
}

type refOccurrence struct {
	filePath string
	startOff int
	endOff   int
	isDecl   bool
	isUnwrap bool // `user` narrowed inside `if (user?):`
}

func (s *Server) computeReferences(filePath, src string, pos Position, includeDecl bool) []Location {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return []Location{}
	}
	tok, ok := s.findTokenAtOffset(filePath, src, offset)
	if !ok {
		return []Location{}
	}

	tgt := resolveRefTargetAt(prog, mod, filePath, src, offset, tok)
	if tgt == nil || tgt.kind == refNone {
		return []Location{}
	}

	occs := s.collectTargetOccurrences(prog, mod, filePath, src, tgt)
	srcCache := map[string]string{absClean(filePath): src}
	var locs []Location
	seenLocs := make(map[string]bool)

	for _, occ := range occs {
		if !includeDecl && occ.isDecl {
			continue
		}
		fSrc, ok := srcCache[occ.filePath]
		if !ok {
			fSrc = s.readSourceForPath(occ.filePath)
			srcCache[occ.filePath] = fSrc
		}
		if fSrc == "" || occ.endOff <= occ.startOff || occ.endOff > len(fSrc) {
			continue
		}
		locKey := fmt.Sprintf("%s:%d:%d", occ.filePath, occ.startOff, occ.endOff)
		if seenLocs[locKey] {
			continue
		}
		seenLocs[locKey] = true
		locs = append(locs, Location{
			URI: PathToURI(occ.filePath),
			Range: Range{
				Start: OffsetToLSP(fSrc, occ.startOff),
				End:   OffsetToLSP(fSrc, occ.endOff),
			},
		})
	}

	sort.Slice(locs, func(i, j int) bool {
		if locs[i].URI != locs[j].URI {
			return locs[i].URI < locs[j].URI
		}
		if locs[i].Range.Start.Line != locs[j].Range.Start.Line {
			return locs[i].Range.Start.Line < locs[j].Range.Start.Line
		}
		return locs[i].Range.Start.Character < locs[j].Range.Start.Character
	})
	return locs
}

func (s *Server) computeTypeDefinition(filePath, src string, pos Position) *Location {
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

	ty := findTypeAtOffset(prog, mod, offset, tok)
	if ty == nil {
		return nil
	}
	baseTy := unwrapTypeForTypeDef(ty)
	if baseTy == nil {
		return nil
	}
	if sp := s.typeSpan(prog, baseTy); sp.Start.File != "" {
		return s.spanToLocation(sp)
	}
	return nil
}

func (s *Server) computeDeclaration(filePath, src string, pos Position) *Location {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod != nil && mod.File != nil {
		if tok, ok := s.findTokenAtOffset(filePath, src, offset); ok {
			// Check if cursor is on an implementing method declaration or call `bind Type.iface_meth` -> jump to Interface method decl.
			if sp := findInterfaceDeclForMethodAt(prog, mod, offset, tok); sp.Start.File != "" {
				return s.spanToLocation(sp)
			}
		}
	}
	return s.computeDefinition(filePath, src, pos)
}

func unwrapTypeForTypeDef(t types.Type) types.Type {
	for t != nil {
		switch v := t.(type) {
		case *types.Optional:
			t = v.Elem
		case *types.Ref:
			t = v.Elem
		case *types.Ptr:
			t = v.Elem
		case *types.Vector:
			t = v.Elem
		case *types.StackArray:
			t = v.Elem
		default:
			if el, ok := types.SetElem(t); ok {
				t = el
				continue
			}
			return t
		}
	}
	return nil
}

func findInterfaceDeclForMethodAt(prog *sema.CheckedProgram, mod *sema.Module, offset int, tok token.Token) diag.Span {
	if prog == nil || mod == nil || mod.File == nil {
		return diag.Span{}
	}
	findInIface := func(recvName, methName string) diag.Span {
		for _, target := range prog.TypeImplementedInterfaceTargets(recvName, mod) {
			for _, m := range prog.Modules {
				if m == nil || m.File == nil {
					continue
				}
				for _, d := range m.File.Decls {
					if ifc, ok := d.(*ast.InterfaceDecl); ok && ifc.Name == target.Name {
						for _, im := range ifc.Methods {
							if im.Name == methName {
								return im.Span
							}
						}
					}
				}
			}
		}
		return diag.Span{}
	}

	for _, d := range mod.File.Decls {
		if md, ok := d.(*ast.MethodDecl); ok && spanContainsOffset(md.Span, offset) && tok.Text == md.Name {
			if md.Body == nil || offset < md.Body.GetSpan().Start.Offset {
				if sp := findInIface(md.RecvType, md.Name); sp.Start.File != "" {
					return sp
				}
			}
		}
	}
	return diag.Span{}
}

func findTypeAtOffset(prog *sema.CheckedProgram, mod *sema.Module, offset int, tok token.Token) types.Type {
	if mod == nil || mod.File == nil {
		return nil
	}
	for _, d := range mod.File.Decls {
		if !spanContainsOffset(d.GetSpan(), offset) {
			continue
		}
		switch decl := d.(type) {
		case *ast.VarDecl:
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				fi := findFuncInstanceForDecl(prog, mod, decl.Span)
				if fi != nil {
					for i, p := range fl.Params {
						if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i < len(fi.ParamSyms) && fi.ParamSyms[i] != nil {
							return fi.ParamSyms[i].Type
						}
					}
					if ty := findExprTypeAtOffset(prog, fi.BodyMaps, fl.Body, offset, tok); ty != nil {
						return ty
					}
				}
			} else if decl.Value != nil {
				if ty := findExprTypeAtOffset(prog, prog.TopBodyMaps, decl.Value, offset, tok); ty != nil {
					return ty
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
					if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i+pOffset < len(fi.ParamSyms) && fi.ParamSyms[i+pOffset] != nil {
						return fi.ParamSyms[i+pOffset].Type
					}
				}
				if ty := findExprTypeAtOffset(prog, fi.BodyMaps, decl.Body, offset, tok); ty != nil {
					return ty
				}
			}
		}
	}
	if sym := lookupTopSym(mod, tok.Text); sym != nil && sym.Type != nil {
		return sym.Type
	}
	if nom := lookupNominalType(mod, "", tok.Text); nom != nil {
		return nom
	}
	return nil
}

func findExprTypeAtOffset(prog *sema.CheckedProgram, bm *sema.BodyMaps, e ast.Expr, offset int, tok token.Token) types.Type {
	if e == nil || !spanContainsOffset(e.GetSpan(), offset) {
		return nil
	}
	if bm != nil {
		var best ast.Expr
		var bestLen int
		for expr := range bm.ExprInfos {
			if expr == nil {
				continue
			}
			sp := expr.GetSpan()
			if spanContainsOffset(sp, offset) {
				l := sp.End.Offset - sp.Start.Offset
				if best == nil || l < bestLen {
					best = expr
					bestLen = l
				}
			}
		}
		if best != nil {
			if inf := bm.ExprInfos[best]; inf != nil && inf.Type != nil && inf.Type != types.TypeInvalid {
				return inf.Type
			}
		}
		// Check VarStmt declarations
		for vs, sym := range bm.VarSymbols {
			if vs != nil && sym != nil && spanContainsOffset(vs.Span, offset) && tok.Text == vs.Name {
				return sym.Type
			}
		}
		for vs, syms := range bm.UnpackVarSymbols {
			if vs == nil || !spanContainsOffset(vs.Span, offset) {
				continue
			}
			for i, uv := range vs.UnpackVars {
				if spanContainsOffset(uv.Span, offset) && tok.Text == uv.Name && i < len(syms) && syms[i] != nil {
					return syms[i].Type
				}
			}
		}
	}
	_ = prog
	return nil
}

func absClean(p string) string {
	if p == "" {
		return ""
	}
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(p)
}

func canonicalSpanKey(sp diag.Span) string {
	if sp.Start.File == "" || sp.Start.Line <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", absClean(sp.Start.File), sp.Start.Line, sp.Start.Col)
}

func lexCleanTokens(filePath, src string) []token.Token {
	raw := lexer.Lex(filePath, src, diag.NewReporter())
	var out []token.Token
	for _, tk := range raw {
		if tk.Kind != token.EOF && !tk.AutoSemi {
			out = append(out, tk)
		}
	}
	return out
}

func tokensInSpan(toks []token.Token, sp diag.Span) []token.Token {
	if sp.Start.Offset < 0 || sp.End.Offset <= sp.Start.Offset {
		return nil
	}
	lo := sort.Search(len(toks), func(i int) bool {
		return toks[i].Span.Start.Offset >= sp.Start.Offset
	})
	var res []token.Token
	for i := lo; i < len(toks); i++ {
		if toks[i].Span.Start.Offset >= sp.End.Offset {
			break
		}
		res = append(res, toks[i])
	}
	return res
}

func findHeaderToken(toks []token.Token, sp diag.Span, name string) (token.Token, bool) {
	for _, tk := range tokensInSpan(toks, sp) {
		if tk.Kind == token.Assign || tk.Kind == token.LBrace {
			break
		}
		if (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == name {
			return tk, true
		}
	}
	return token.Token{}, false
}

func findMethodDeclToken(toks []token.Token, sp diag.Span, methName string) (token.Token, bool) {
	seenDot := false
	for _, tk := range tokensInSpan(toks, sp) {
		if tk.Kind == token.Assign || tk.Kind == token.LBrace {
			break
		}
		if tk.Kind == token.Dot {
			seenDot = true
			continue
		}
		if seenDot && (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == methName {
			return tk, true
		}
	}
	return token.Token{}, false
}

func findFirstTokenNamed(toks []token.Token, sp diag.Span, name string) (token.Token, bool) {
	for _, tk := range tokensInSpan(toks, sp) {
		if (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == name {
			return tk, true
		}
	}
	return token.Token{}, false
}

func findLastTokenNamed(toks []token.Token, sp diag.Span, name string) (token.Token, bool) {
	slice := tokensInSpan(toks, sp)
	for i := len(slice) - 1; i >= 0; i-- {
		tk := slice[i]
		if (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == name {
			return tk, true
		}
	}
	return token.Token{}, false
}

func resolveRefTargetAt(prog *sema.CheckedProgram, mod *sema.Module, filePath, src string, offset int, tok token.Token) *refTarget {
	word := tok.Text
	if word == "" {
		return nil
	}

	// Reject universe built-ins & keywords for non-AST references
	resolveFromSym := func(sym *sema.Symbol, ownerBody diag.Span, bm *sema.BodyMaps) *refTarget {
		if sym == nil {
			return nil
		}
		if sym.FuncInst != nil {
			sp := sym.FuncInst.DeclSpan
			if sp.Start.File == "" {
				sp = sym.Span
			}
			kind := refTopSym
			if sym.FuncInst.IsMethod {
				kind = refMethod
			}
			return &refTarget{
				kind:      kind,
				name:      tok.Text,
				ownerFile: absClean(sp.Start.File),
				declKey:   canonicalSpanKey(sp),
			}
		}
		if sym.ExternFn != nil {
			sp := sym.ExternFn.Span
			if sp.Start.File == "" {
				sp = sym.Span
			}
			return &refTarget{
				kind:      refTopSym,
				name:      sym.Name,
				ownerFile: absClean(sp.Start.File),
				declKey:   canonicalSpanKey(sp),
			}
		}
		if sym.IsTopLevel {
			return &refTarget{
				kind:           refTopSym,
				name:           sym.Name,
				ownerFile:      absClean(sym.Span.Start.File),
				declKey:        canonicalSpanKey(sym.Span),
				isOptionalName: strings.HasSuffix(sym.Name, "?"),
			}
		}
		// Local symbol / parameter / loop variable / unwrapped opt var
		tgt := &refTarget{
			kind:           refLocalVar,
			name:           sym.Name,
			ownerFile:      absClean(filePath),
			declKey:        canonicalSpanKey(sym.Span),
			localSym:       sym,
			localAliases:   make(map[*sema.Symbol]bool),
			ownerBodySpan:  ownerBody,
			isOptionalName: strings.HasSuffix(sym.Name, "?"),
		}
		if bm != nil {
			// Link `IfUnwrapSyms` <-> `IfOriginSyms` bidirectionally
			for ie, orig := range bm.IfOriginSyms {
				unw := bm.IfUnwrapSyms[ie]
				if orig == sym && unw != nil {
					tgt.localAliases[unw] = true
				} else if unw == sym && orig != nil {
					tgt.localSym = orig
					tgt.name = orig.Name
					tgt.isOptionalName = strings.HasSuffix(orig.Name, "?")
					tgt.localAliases[sym] = true
				}
			}
			// Re-collect any other IfUnwrapSyms pointing to the canonical tgt.localSym
			for ie, orig := range bm.IfOriginSyms {
				if orig == tgt.localSym {
					if unw := bm.IfUnwrapSyms[ie]; unw != nil {
						tgt.localAliases[unw] = true
					}
				}
			}
		}
		return tgt
	}

	resolveTypeByName := func(modAlias, name string) *refTarget {
		if nom := lookupNominalType(mod, modAlias, name); nom != nil {
			if sp, ok := prog.TypeSpans[nom]; ok && sp.Start.File != "" {
				return &refTarget{
					kind:      refType,
					name:      name,
					ownerFile: absClean(sp.Start.File),
					declKey:   canonicalSpanKey(sp),
				}
			}
		}
		if gst := lookupGenStruct(mod, modAlias, name); gst != nil && gst.Decl != nil {
			return &refTarget{
				kind:      refType,
				name:      name,
				ownerFile: absClean(gst.Decl.Span.Start.File),
				declKey:   canonicalSpanKey(gst.Decl.Span),
			}
		}
		if gen := lookupGenEnum(mod, modAlias, name); gen != nil && gen.Decl != nil {
			return &refTarget{
				kind:      refType,
				name:      name,
				ownerFile: absClean(gen.Decl.Span.Start.File),
				declKey:   canonicalSpanKey(gen.Decl.Span),
			}
		}
		if gi := lookupGenInterface(mod, modAlias, name); gi != nil && gi.Decl != nil {
			return &refTarget{
				kind:      refType,
				name:      name,
				ownerFile: absClean(gi.Decl.Span.Start.File),
				declKey:   canonicalSpanKey(gi.Decl.Span),
			}
		}
		return nil
	}

	typeKeyForNominal := func(ty types.Type) (string, string) {
		if ty == nil {
			return "", ""
		}
		if sp, ok := prog.TypeSpans[ty]; ok && sp.Start.File != "" {
			return canonicalSpanKey(sp), ty.String()
		}
		if st, ok := ty.(*types.Struct); ok {
			if decl := prog.StructDecls[st]; decl != nil {
				return canonicalSpanKey(decl.Span), st.Name
			}
			return "", st.Name
		}
		if en, ok := ty.(*types.Enum); ok {
			if decl := prog.EnumDecls[en]; decl != nil {
				return canonicalSpanKey(decl.Span), en.Name
			}
			return "", en.Name
		}
		return "", ty.String()
	}

	var inExpr func(bm *sema.BodyMaps, e ast.Expr, bodySpan diag.Span) *refTarget
	var inStmt func(bm *sema.BodyMaps, st ast.Stmt, bodySpan diag.Span) *refTarget
	var inTypeExpr func(te ast.TypeExpr) *refTarget

	inTypeExpr = func(te ast.TypeExpr) *refTarget {
		if te == nil || !spanContainsOffset(te.GetSpan(), offset) {
			return nil
		}
		switch v := te.(type) {
		case *ast.NamedType:
			for _, ta := range v.TypeArgs {
				if r := inTypeExpr(ta); r != nil {
					return r
				}
			}
			if v.Module != "" && tok.Text == v.Module {
				if targetMod, ok := mod.Aliased[v.Module]; ok {
					return &refTarget{kind: refModuleAlias, name: v.Module, ownerFile: absClean(mod.Path), declKey: canonicalSpanKey(v.Span) + ":" + targetMod.Path}
				}
			}
			if tok.Text == v.Name {
				return resolveTypeByName(v.Module, v.Name)
			}
		case *ast.OptionalType:
			return inTypeExpr(v.Elem)
		case *ast.MutBorrowType:
			return inTypeExpr(v.Elem)
		case *ast.OwnType:
			return inTypeExpr(v.Elem)
		case *ast.VectorType:
			return inTypeExpr(v.Elem)
		case *ast.StackArrayType:
			return inTypeExpr(v.Elem)
		case *ast.SetType:
			return inTypeExpr(v.Elem)
		case *ast.MapType:
			if r := inTypeExpr(v.Key); r != nil {
				return r
			}
			return inTypeExpr(v.Value)
		case *ast.TupleType:
			for _, el := range v.Elems {
				if r := inTypeExpr(el); r != nil {
					return r
				}
			}
		case *ast.FuncType:
			for _, p := range v.Params {
				if r := inTypeExpr(p.Type); r != nil {
					return r
				}
			}
			return inTypeExpr(v.Ret)
		}
		return nil
	}

	inStmt = func(bm *sema.BodyMaps, st ast.Stmt, bodySpan diag.Span) *refTarget {
		if st == nil {
			return nil
		}
		switch vs := st.(type) {
		case *ast.VarStmt:
			if vs.Type != nil && spanContainsOffset(vs.Type.GetSpan(), offset) {
				if r := inTypeExpr(vs.Type); r != nil {
					return r
				}
			}
			if spanContainsOffset(vs.Value.GetSpan(), offset) {
				if r := inExpr(bm, vs.Value, bodySpan); r != nil {
					return r
				}
			}
			if vs.FallbackUnwind != nil && vs.FallbackUnwind.Value != nil && spanContainsOffset(vs.FallbackUnwind.Value.GetSpan(), offset) {
				if r := inExpr(bm, vs.FallbackUnwind.Value, bodySpan); r != nil {
					return r
				}
			}
			if bm != nil {
				if vs.IsUnpack {
					syms := bm.UnpackVarSymbols[vs]
					for i, uv := range vs.UnpackVars {
						if uv.Type != nil && spanContainsOffset(uv.Type.GetSpan(), offset) {
							if r := inTypeExpr(uv.Type); r != nil {
								return r
							}
						}
						if tok.Text == uv.Name && i < len(syms) && syms[i] != nil {
							return resolveFromSym(syms[i], bodySpan, bm)
						}
					}
				} else if tok.Text == vs.Name {
					if sym := bm.VarSymbols[vs]; sym != nil {
						return resolveFromSym(sym, bodySpan, bm)
					}
				}
			}
		case *ast.AssignStmt:
			if vs.IsUnpack {
				for _, tgt := range vs.UnpackTargets {
					if spanContainsOffset(tgt.GetSpan(), offset) {
						return inExpr(bm, tgt, bodySpan)
					}
				}
				return inExpr(bm, vs.Value, bodySpan)
			}
			if spanContainsOffset(vs.Target.GetSpan(), offset) {
				return inExpr(bm, vs.Target, bodySpan)
			}
			return inExpr(bm, vs.Value, bodySpan)
		case *ast.UnwindStmt:
			return inExpr(bm, vs.Value, bodySpan)
		case *ast.ExprStmt:
			return inExpr(bm, vs.Expr, bodySpan)
		}
		return nil
	}

	inExpr = func(bm *sema.BodyMaps, e ast.Expr, bodySpan diag.Span) *refTarget {
		if e == nil || !spanContainsOffset(e.GetSpan(), offset) {
			return nil
		}
		switch v := e.(type) {
		case *ast.BlockExpr:
			for _, st := range v.Stmts {
				if spanContainsOffset(st.GetSpan(), offset) {
					return inStmt(bm, st, bodySpan)
				}
			}
			return inExpr(bm, v.Tail, bodySpan)
		case *ast.StmtBodyExpr:
			return inStmt(bm, v.Stmt, bodySpan)
		case *ast.IfExpr:
			if v.CondKind == ast.IfCondOptVar && tok.Text == v.OptVar && (v.Then == nil || offset < v.Then.GetSpan().Start.Offset) {
				if bm != nil {
					if orig := bm.IfOriginSyms[v]; orig != nil {
						return resolveFromSym(orig, bodySpan, bm)
					}
				}
			}
			if v.CondKind == ast.IfCondOptBind && tok.Text == v.BindName && (v.Then == nil || offset < v.Then.GetSpan().Start.Offset) {
				if bm != nil {
					if sym := bm.IfUnwrapSyms[v]; sym != nil {
						return resolveFromSym(sym, bodySpan, bm)
					}
				}
			}
			if r := inTypeExpr(v.EvalType); r != nil {
				return r
			}
			if r := inExpr(bm, v.Cond, bodySpan); r != nil {
				return r
			}
			if r := inExpr(bm, v.Then, bodySpan); r != nil {
				return r
			}
			return inExpr(bm, v.Else, bodySpan)
		case *ast.ForExpr:
			if (v.Var1 != "" && tok.Text == v.Var1) || (v.Var2 != "" && tok.Text == v.Var2) {
				if v.Body == nil || offset < v.Body.GetSpan().Start.Offset {
					if bm != nil {
						fsyms := bm.ForSymbols[v]
						if fsyms[0] != nil && fsyms[0].Name == tok.Text {
							return resolveFromSym(fsyms[0], bodySpan, bm)
						}
						if fsyms[1] != nil && fsyms[1].Name == tok.Text {
							return resolveFromSym(fsyms[1], bodySpan, bm)
						}
					}
				}
			}
			if r := inTypeExpr(v.EvalType); r != nil {
				return r
			}
			if r := inExpr(bm, v.CondOrIter, bodySpan); r != nil {
				return r
			}
			if r := inExpr(bm, v.RangeEnd, bodySpan); r != nil {
				return r
			}
			if r := inExpr(bm, v.Body, bodySpan); r != nil {
				return r
			}
			return inExpr(bm, v.Else, bodySpan)
		case *ast.WhileExpr:
			if r := inTypeExpr(v.EvalType); r != nil {
				return r
			}
			if r := inExpr(bm, v.CondOrIter, bodySpan); r != nil {
				return r
			}
			if r := inExpr(bm, v.RangeEnd, bodySpan); r != nil {
				return r
			}
			if r := inExpr(bm, v.Body, bodySpan); r != nil {
				return r
			}
			return inExpr(bm, v.Else, bodySpan)
		case *ast.MatchExpr:
			for _, scr := range v.Scrutinees {
				if r := inExpr(bm, scr, bodySpan); r != nil {
					return r
				}
			}
			for _, arm := range v.Arms {
				if arm.Synthetic {
					continue
				}
				resolveVariantPat := func(vp *ast.VariantPattern) *refTarget {
					if bm != nil && spanContainsOffset(vp.Span, offset) {
						syms := bm.PatternSyms[vp]
						for i, b := range vp.Bindings {
							if spanContainsOffset(b.Span, offset) && tok.Text == b.BindName && i < len(syms) && syms[i] != nil {
								return resolveFromSym(syms[i], bodySpan, bm)
							}
						}
					}
					return inExpr(bm, vp.Target, bodySpan)
				}
				for _, pat := range arm.AllPatterns() {
					switch p := pat.(type) {
					case *ast.ExprPattern:
						if r := inExpr(bm, p.Expr, bodySpan); r != nil {
							return r
						}
					case *ast.ContainsPattern:
						if r := inExpr(bm, p.Container, bodySpan); r != nil {
							return r
						}
					case *ast.MethodPattern:
						if bm != nil {
							if vp := bm.BareVariantPatterns[p]; vp != nil {
								if r := resolveVariantPat(vp); r != nil {
									return r
								}
								continue
							}
							if mpInfo := bm.MethodPatterns[p]; mpInfo != nil && mpInfo.Call != nil {
								if r := inExpr(bm, mpInfo.Call, bodySpan); r != nil {
									return r
								}
							}
						}
						for _, a := range p.Args {
							if r := inExpr(bm, a, bodySpan); r != nil {
								return r
							}
						}
					case *ast.VariantPattern:
						if r := resolveVariantPat(p); r != nil {
							return r
						}
					}
				}
				if r := inExpr(bm, arm.Body, bodySpan); r != nil {
					return r
				}
			}
		case *ast.FuncLit:
			lbm := bm
			lBodySpan := v.Span
			for _, li := range prog.Lambdas {
				if li != nil && li.Lit == v {
					lbm = li.BodyMaps
					for i, p := range v.Params {
						if spanContainsOffset(p.Type.GetSpan(), offset) {
							if r := inTypeExpr(p.Type); r != nil {
								return r
							}
						}
						if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i < len(li.ParamSyms) {
							return resolveFromSym(li.ParamSyms[i], lBodySpan, lbm)
						}
					}
					break
				}
			}
			if v.Ret != nil && spanContainsOffset(v.Ret.GetSpan(), offset) {
				if r := inTypeExpr(v.Ret); r != nil {
					return r
				}
			}
			return inExpr(lbm, v.Body, lBodySpan)
		case *ast.CallExpr:
			for _, a := range v.Args {
				if spanContainsOffset(a.GetSpan(), offset) {
					return inExpr(bm, a, bodySpan)
				}
			}
			for _, ta := range v.TypeArgs {
				if spanContainsOffset(ta.GetSpan(), offset) {
					if r := inTypeExpr(ta); r != nil {
						return r
					}
				}
			}
			if mem, isMem := v.Callee.(*ast.MemberExpr); isMem && spanContainsOffset(mem.Base.GetSpan(), offset) {
				return inExpr(bm, mem.Base, bodySpan)
			}
			if inf := exprInfoFromBM(prog, bm, v); inf != nil {
				if inf.CallKind == sema.CallDirectFunc && inf.TargetFunc != nil {
					sp := inf.TargetFunc.DeclSpan
					kind := refTopSym
					if inf.TargetFunc.IsMethod {
						kind = refMethod
					}
					if _, isMem := v.Callee.(*ast.MemberExpr); isMem {
						return &refTarget{
							kind:      kind,
							name:      tok.Text,
							ownerFile: absClean(sp.Start.File),
							declKey:   canonicalSpanKey(sp),
						}
					}
					return &refTarget{
						kind:      kind,
						name:      tok.Text,
						ownerFile: absClean(sp.Start.File),
						declKey:   canonicalSpanKey(sp),
					}
				}
				if inf.CallKind == sema.CallExternC && inf.TargetExtern != nil {
					return &refTarget{
						kind:      refTopSym,
						name:      tok.Text,
						ownerFile: absClean(inf.TargetExtern.Span.Start.File),
						declKey:   canonicalSpanKey(inf.TargetExtern.Span),
					}
				}
				if inf.CallKind == sema.CallInterfaceMethod && inf.InterfaceMeth != nil {
					if mem, ok := v.Callee.(*ast.MemberExpr); ok {
						if bInf := exprInfoFromBM(prog, bm, mem.Base); bInf != nil && bInf.Type != nil {
							if ifc, ok2 := unwrapOptAndRef(bInf.Type).(*types.Interface); ok2 {
								tKey, tName := typeKeyForNominal(ifc)
								return &refTarget{
									kind:          refInterfaceMethod,
									name:          inf.InterfaceMeth.Name,
									ownerTypeKey:  tKey,
									ownerTypeName: tName,
								}
							}
						}
					}
				}
				if inf.CallKind == sema.CallEnumVariant && inf.EnumType != nil && inf.EnumVariant != nil {
					tKey, tName := typeKeyForNominal(inf.EnumType)
					return &refTarget{
						kind:          refVariant,
						name:          inf.EnumVariant.Name,
						ownerTypeKey:  tKey,
						ownerTypeName: tName,
					}
				}
				if inf.CallKind == sema.CallBuiltinFunc || inf.CallKind == sema.CallIntrinsic || inf.CallKind == sema.CallBuiltinMethod {
					return &refTarget{kind: refNone, isBuiltin: true}
				}
			}
			return inExpr(bm, v.Callee, bodySpan)
		case *ast.MemberExpr:
			if spanContainsOffset(v.Base.GetSpan(), offset) && !isPlaceholderIdent(v.Base) {
				if id, ok := v.Base.(*ast.IdentExpr); ok {
					if targetMod, isMod := mod.Aliased[id.Name]; isMod && targetMod != nil {
						return &refTarget{kind: refModuleAlias, name: id.Name, ownerFile: absClean(mod.Path)}
					}
					if inf := exprInfoFromBM(prog, bm, id); inf == nil || inf.Sym == nil {
						if r := resolveTypeByName("", id.Name); r != nil {
							return r
						}
					}
				}
				return inExpr(bm, v.Base, bodySpan)
			}
			inf := exprInfoFromBM(prog, bm, v)
			if inf != nil {
				if inf.EnumVariant != nil && inf.EnumType != nil {
					tKey, tName := typeKeyForNominal(inf.EnumType)
					return &refTarget{
						kind:          refVariant,
						name:          inf.EnumVariant.Name,
						ownerTypeKey:  tKey,
						ownerTypeName: tName,
					}
				}
				if inf.Sym != nil {
					return resolveFromSym(inf.Sym, bodySpan, bm)
				}
				if bInf := exprInfoFromBM(prog, bm, v.Base); bInf != nil && bInf.Type != nil {
					recv := unwrapOptAndRef(bInf.Type)
					if st, ok := recv.(*types.Struct); ok {
						tKey, tName := typeKeyForNominal(st)
						return &refTarget{
							kind:          refField,
							name:          v.Member,
							ownerTypeKey:  tKey,
							ownerTypeName: tName,
						}
					}
				}
			}
		case *ast.StructLit:
			for _, f := range v.Fields {
				if spanContainsOffset(f.Value.GetSpan(), offset) {
					return inExpr(bm, f.Value, bodySpan)
				}
				if spanContainsOffset(f.Span, offset) && tok.Text == f.Name {
					if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.StructType != nil {
						tKey, tName := typeKeyForNominal(inf.StructType)
						return &refTarget{
							kind:          refField,
							name:          f.Name,
							ownerTypeKey:  tKey,
							ownerTypeName: tName,
						}
					}
				}
			}
			for _, ta := range v.TypeArgs {
				if spanContainsOffset(ta.GetSpan(), offset) {
					if r := inTypeExpr(ta); r != nil {
						return r
					}
				}
			}
			if tok.Text == v.TypeName {
				if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.StructType != nil {
					tKey, _ := typeKeyForNominal(inf.StructType)
					return &refTarget{kind: refType, name: v.TypeName, declKey: tKey}
				}
				return resolveTypeByName(v.Module, v.TypeName)
			}
		case *ast.IdentExpr:
			if inf := exprInfoFromBM(prog, bm, v); inf != nil {
				if inf.Sym != nil {
					return resolveFromSym(inf.Sym, bodySpan, bm)
				}
				if inf.EnumVariant != nil && inf.EnumType != nil {
					tKey, tName := typeKeyForNominal(inf.EnumType)
					return &refTarget{
						kind:          refVariant,
						name:          inf.EnumVariant.Name,
						ownerTypeKey:  tKey,
						ownerTypeName: tName,
					}
				}
			}
			if LookupBuiltinFunc(v.Name) != nil || LookupBuiltinTypeDoc(v.Name) != nil || LookupKeywordDoc(v.Name) != nil {
				return &refTarget{kind: refNone, isBuiltin: true}
			}
			if targetMod, isMod := mod.Aliased[v.Name]; isMod && targetMod != nil {
				return &refTarget{kind: refModuleAlias, name: v.Name, ownerFile: absClean(mod.Path)}
			}
		case *ast.BinaryExpr:
			if r := inExpr(bm, v.Left, bodySpan); r != nil {
				return r
			}
			return inExpr(bm, v.Right, bodySpan)
		case *ast.UnaryExpr:
			return inExpr(bm, v.Operand, bodySpan)
		case *ast.UnsafeExpr:
			return inExpr(bm, v.Expr, bodySpan)
		case *ast.IndexExpr:
			if r := inExpr(bm, v.Base, bodySpan); r != nil {
				return r
			}
			return inExpr(bm, v.Index, bodySpan)
		case *ast.SliceExpr:
			if r := inExpr(bm, v.Base, bodySpan); r != nil {
				return r
			}
			if r := inExpr(bm, v.Low, bodySpan); r != nil {
				return r
			}
			return inExpr(bm, v.High, bodySpan)
		case *ast.RangeExpr:
			if r := inExpr(bm, v.Low, bodySpan); r != nil {
				return r
			}
			return inExpr(bm, v.High, bodySpan)
		case *ast.TupleLit:
			for _, el := range v.Elems {
				if r := inExpr(bm, el, bodySpan); r != nil {
					return r
				}
			}
		case *ast.ArrayLit:
			for _, el := range v.Elems {
				if r := inExpr(bm, el, bodySpan); r != nil {
					return r
				}
			}
		case *ast.VecLit:
			for _, el := range v.Elems {
				if r := inExpr(bm, el, bodySpan); r != nil {
					return r
				}
			}
		case *ast.SetLit:
			for _, el := range v.Elems {
				if r := inExpr(bm, el, bodySpan); r != nil {
					return r
				}
			}
		case *ast.MapLit:
			for _, ent := range v.Entries {
				if r := inExpr(bm, ent.Key, bodySpan); r != nil {
					return r
				}
				if r := inExpr(bm, ent.Value, bodySpan); r != nil {
					return r
				}
			}
		case *ast.InterpStringExpr:
			for _, sub := range v.Exprs {
				if r := inExpr(bm, sub, bodySpan); r != nil {
					return r
				}
			}
		}
		return nil
	}

	// Check top-level AST declarations first
	for _, d := range mod.File.Decls {
		if !spanContainsOffset(d.GetSpan(), offset) {
			continue
		}
		switch decl := d.(type) {
		case *ast.ImportDecl:
			if decl.Alias != "" && tok.Text == decl.Alias {
				return &refTarget{kind: refModuleAlias, name: decl.Alias, ownerFile: absClean(mod.Path)}
			}
		case *ast.StructDecl:
			for _, f := range decl.Fields {
				if f.Type != nil && spanContainsOffset(f.Type.GetSpan(), offset) {
					if r := inTypeExpr(f.Type); r != nil {
						return r
					}
				}
				if spanContainsOffset(f.Span, offset) && tok.Text == f.Name {
					return &refTarget{
						kind:          refField,
						name:          f.Name,
						ownerFile:     absClean(decl.Span.Start.File),
						ownerTypeKey:  canonicalSpanKey(decl.Span),
						ownerTypeName: decl.Name,
					}
				}
			}
			if tok.Text == decl.Name {
				return &refTarget{kind: refType, name: decl.Name, ownerFile: absClean(decl.Span.Start.File), declKey: canonicalSpanKey(decl.Span)}
			}
		case *ast.EnumDecl:
			for _, v := range decl.Variants {
				if v.PayloadType != nil && spanContainsOffset(v.PayloadType.GetSpan(), offset) {
					if r := inTypeExpr(v.PayloadType); r != nil {
						return r
					}
				}
				if spanContainsOffset(v.Span, offset) && tok.Text == v.Name {
					return &refTarget{
						kind:          refVariant,
						name:          v.Name,
						ownerFile:     absClean(decl.Span.Start.File),
						ownerTypeKey:  canonicalSpanKey(decl.Span),
						ownerTypeName: decl.Name,
					}
				}
			}
			if tok.Text == decl.Name {
				return &refTarget{kind: refType, name: decl.Name, ownerFile: absClean(decl.Span.Start.File), declKey: canonicalSpanKey(decl.Span)}
			}
		case *ast.InterfaceDecl:
			for _, m := range decl.Methods {
				for _, p := range m.Params {
					if spanContainsOffset(p.Type.GetSpan(), offset) {
						if r := inTypeExpr(p.Type); r != nil {
							return r
						}
					}
				}
				if m.Ret != nil && spanContainsOffset(m.Ret.GetSpan(), offset) {
					if r := inTypeExpr(m.Ret); r != nil {
						return r
					}
				}
				if spanContainsOffset(m.Span, offset) && tok.Text == m.Name {
					return &refTarget{
						kind:          refInterfaceMethod,
						name:          m.Name,
						ownerFile:     absClean(decl.Span.Start.File),
						ownerTypeKey:  canonicalSpanKey(decl.Span),
						ownerTypeName: decl.Name,
					}
				}
			}
			if tok.Text == decl.Name {
				return &refTarget{kind: refType, name: decl.Name, ownerFile: absClean(decl.Span.Start.File), declKey: canonicalSpanKey(decl.Span)}
			}
		case *ast.DistinctTypeDecl:
			if decl.Base != nil && spanContainsOffset(decl.Base.GetSpan(), offset) {
				if r := inTypeExpr(decl.Base); r != nil {
					return r
				}
			}
			if tok.Text == decl.Name {
				return &refTarget{kind: refType, name: decl.Name, ownerFile: absClean(decl.Span.Start.File), declKey: canonicalSpanKey(decl.Span)}
			}
		case *ast.MethodDecl:
			if tok.Text == decl.RecvType && (decl.Body == nil || offset < decl.Body.GetSpan().Start.Offset) {
				// Make sure we are before `.` in `bind Type.method`
				dotPos := strings.IndexByte(src[decl.Span.Start.Offset:], '.')
				if dotPos < 0 || offset < decl.Span.Start.Offset+dotPos {
					return resolveTypeByName("", decl.RecvType)
				}
			}
			if tok.Text == decl.Name && (decl.Body == nil || offset < decl.Body.GetSpan().Start.Offset) {
				return &refTarget{
					kind:      refMethod,
					name:      decl.Name,
					ownerFile: absClean(decl.Span.Start.File),
					declKey:   canonicalSpanKey(decl.Span),
				}
			}
			for _, p := range decl.Params {
				if spanContainsOffset(p.Type.GetSpan(), offset) {
					if r := inTypeExpr(p.Type); r != nil {
						return r
					}
				}
			}
			if decl.Ret != nil && spanContainsOffset(decl.Ret.GetSpan(), offset) {
				if r := inTypeExpr(decl.Ret); r != nil {
					return r
				}
			}
			fi := findFuncInstanceForDecl(prog, mod, decl.Span)
			var bm *sema.BodyMaps
			if fi != nil {
				bm = fi.BodyMaps
				pOff := 0
				if !fi.IsStaticAssoc {
					pOff = 1
				}
				for i, p := range decl.Params {
					if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i+pOff < len(fi.ParamSyms) && fi.ParamSyms[i+pOff] != nil {
						return resolveFromSym(fi.ParamSyms[i+pOff], decl.Span, bm)
					}
				}
			}
			if r := inExpr(bm, decl.Body, decl.Span); r != nil {
				return r
			}
		case *ast.VarDecl:
			if decl.Type != nil && spanContainsOffset(decl.Type.GetSpan(), offset) {
				if r := inTypeExpr(decl.Type); r != nil {
					return r
				}
			}
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				if tok.Text == decl.Name && offset < fl.Span.Start.Offset {
					return &refTarget{
						kind:      refTopSym,
						name:      decl.Name,
						ownerFile: absClean(decl.Span.Start.File),
						declKey:   canonicalSpanKey(decl.Span),
					}
				}
				fi := findFuncInstanceForDecl(prog, mod, decl.Span)
				var bm *sema.BodyMaps
				if fi != nil {
					bm = fi.BodyMaps
					for i, p := range fl.Params {
						if spanContainsOffset(p.Type.GetSpan(), offset) {
							if r := inTypeExpr(p.Type); r != nil {
								return r
							}
						}
						if spanContainsOffset(p.Span, offset) && tok.Text == p.Name && i < len(fi.ParamSyms) && fi.ParamSyms[i] != nil {
							return resolveFromSym(fi.ParamSyms[i], decl.Span, bm)
						}
					}
				}
				if fl.Ret != nil && spanContainsOffset(fl.Ret.GetSpan(), offset) {
					if r := inTypeExpr(fl.Ret); r != nil {
						return r
					}
				}
				if r := inExpr(bm, fl.Body, decl.Span); r != nil {
					return r
				}
			} else {
				if tok.Text == decl.Name && (decl.Value == nil || offset < decl.Value.GetSpan().Start.Offset) {
					return &refTarget{
						kind:      refTopSym,
						name:      decl.Name,
						ownerFile: absClean(decl.Span.Start.File),
						declKey:   canonicalSpanKey(decl.Span),
					}
				}
				if r := inExpr(prog.TopBodyMaps, decl.Value, decl.Span); r != nil {
					return r
				}
			}
		case *ast.ExternFnDecl:
			aliasOrName := decl.Name
			if decl.Alias != "" {
				aliasOrName = decl.Alias
			}
			if tok.Text == aliasOrName {
				return &refTarget{
					kind:      refTopSym,
					name:      aliasOrName,
					ownerFile: absClean(decl.Span.Start.File),
					declKey:   canonicalSpanKey(decl.Span),
				}
			}
		}
	}

	// Fallback: check top-level symbols / types if found on word
	if r := resolveTypeByName("", word); r != nil {
		return r
	}
	if sym := lookupTopSym(mod, word); sym != nil {
		return resolveFromSym(sym, diag.Span{}, nil)
	}
	if LookupBuiltinFunc(word) != nil || LookupBuiltinTypeDoc(word) != nil || LookupKeywordDoc(word) != nil {
		return &refTarget{kind: refNone, isBuiltin: true}
	}
	return nil
}

func (s *Server) collectTargetOccurrences(
	entryProg *sema.CheckedProgram,
	entryMod *sema.Module,
	entryPath string,
	entrySrc string,
	tgt *refTarget,
) []refOccurrence {
	var allOccs []refOccurrence
	seenOcc := make(map[string]bool)

	addOcc := func(occ refOccurrence) {
		if occ.endOff <= occ.startOff {
			return
		}
		k := fmt.Sprintf("%s:%d:%d", occ.filePath, occ.startOff, occ.endOff)
		if seenOcc[k] {
			return
		}
		seenOcc[k] = true
		allOccs = append(allOccs, occ)
	}

	// Local variables, parameters, and module aliases only span the current file.
	if tgt.kind == refLocalVar || tgt.kind == refModuleAlias {
		collectOccsInModule(entryProg, entryMod, absClean(entryPath), entrySrc, tgt, addOcc)
		return allOccs
	}

	// Collect candidate programs across current program modules, open docs, and workspace `.ll` files containing `tgt.name`.
	type modCheck struct {
		prog *sema.CheckedProgram
		mod  *sema.Module
		path string
		src  string
	}
	checkedFiles := make(map[string]bool)
	var queue []modCheck

	addModCheck := func(p *sema.CheckedProgram, m *sema.Module, pSrc string) {
		if m == nil || m.File == nil {
			return
		}
		absP := absClean(m.Path)
		if checkedFiles[absP] {
			return
		}
		checkedFiles[absP] = true
		if pSrc == "" {
			pSrc = s.readSourceForPath(absP)
		}
		if pSrc == "" || !strings.Contains(pSrc, tgt.name) {
			return
		}
		queue = append(queue, modCheck{prog: p, mod: m, path: absP, src: pSrc})
	}

	addModCheck(entryProg, entryMod, entrySrc)
	for _, m := range entryProg.Modules {
		addModCheck(entryProg, m, "")
	}

	for openPath, openSrc := range s.snapshotOverlays() {
		absOpen := absClean(openPath)
		if checkedFiles[absOpen] || !strings.Contains(openSrc, tgt.name) {
			continue
		}
		p := s.getOrCheckProgram(absOpen, openSrc)
		if m := findModuleForPath(p, absOpen); m != nil {
			addModCheck(p, m, openSrc)
			for _, sub := range p.Modules {
				addModCheck(p, sub, "")
			}
		}
	}

	// Discover sibling/workspace `.ll` files referencing `tgt.name`.
	for _, wsFile := range s.candidateWorkspaceFiles(entryPath, tgt) {
		absWS := absClean(wsFile)
		if checkedFiles[absWS] {
			continue
		}
		wsSrc := s.readSourceForPath(absWS)
		if wsSrc == "" || !strings.Contains(wsSrc, tgt.name) {
			continue
		}
		p := s.getOrCheckProgram(absWS, wsSrc)
		if m := findModuleForPath(p, absWS); m != nil {
			addModCheck(p, m, wsSrc)
		}
	}

	for _, item := range queue {
		collectOccsInModule(item.prog, item.mod, item.path, item.src, tgt, addOcc)
	}
	return allOccs
}

func (s *Server) candidateWorkspaceFiles(entryPath string, tgt *refTarget) []string {
	s.mu.RLock()
	root := s.rootDir
	s.mu.RUnlock()
	if root == "" {
		root = filepath.Dir(absClean(entryPath))
	}
	inStd := false
	if strings.Contains(filepath.ToSlash(tgt.ownerFile), "/std/") {
		inStd = true
	}

	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if shouldSkipWorkspaceDir(name) {
				return filepath.SkipDir
			}
			if name == "std" && !inStd {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".ll") {
			files = append(files, p)
			if len(files) >= 200 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return files
}

func collectOccsInModule(
	prog *sema.CheckedProgram,
	mod *sema.Module,
	absPath string,
	src string,
	tgt *refTarget,
	addOcc func(refOccurrence),
) {
	if prog == nil || mod == nil || mod.File == nil || src == "" {
		return
	}
	toks := lexCleanTokens(absPath, src)

	matchSym := func(sym *sema.Symbol) (matched bool, isUnwrap bool) {
		if sym == nil {
			return false, false
		}
		if tgt.kind == refLocalVar {
			if sym == tgt.localSym {
				return true, false
			}
			if tgt.localAliases[sym] {
				return true, true
			}
			return false, false
		}
		if tgt.kind == refTopSym {
			if sym.FuncInst != nil && canonicalSpanKey(sym.FuncInst.DeclSpan) == tgt.declKey {
				return true, false
			}
			if sym.ExternFn != nil && canonicalSpanKey(sym.ExternFn.Span) == tgt.declKey {
				return true, false
			}
			if sym.IsTopLevel && canonicalSpanKey(sym.Span) == tgt.declKey {
				return true, false
			}
		}
		return false, false
	}

	matchNominalKey := func(ty types.Type) bool {
		if ty == nil {
			return false
		}
		if sp, ok := prog.TypeSpans[ty]; ok && canonicalSpanKey(sp) == tgt.declKey {
			return true
		}
		if st, ok := ty.(*types.Struct); ok && prog.StructDecls[st] != nil {
			return canonicalSpanKey(prog.StructDecls[st].Span) == tgt.declKey
		}
		if en, ok := ty.(*types.Enum); ok && prog.EnumDecls[en] != nil {
			return canonicalSpanKey(prog.EnumDecls[en].Span) == tgt.declKey
		}
		return false
	}

	typeKeyMatch := func(ty types.Type, key, name string) bool {
		if ty == nil {
			return false
		}
		if key != "" {
			if sp, ok := prog.TypeSpans[ty]; ok && canonicalSpanKey(sp) == key {
				return true
			}
			if st, ok := ty.(*types.Struct); ok && prog.StructDecls[st] != nil && canonicalSpanKey(prog.StructDecls[st].Span) == key {
				return true
			}
			if en, ok := ty.(*types.Enum); ok && prog.EnumDecls[en] != nil && canonicalSpanKey(prog.EnumDecls[en].Span) == key {
				return true
			}
		}
		if name != "" {
			if st, ok := ty.(*types.Struct); ok && st.Name == name {
				return true
			}
			if en, ok := ty.(*types.Enum); ok && en.Name == name {
				return true
			}
			if ifc, ok := ty.(*types.Interface); ok && ifc.Name == name {
				return true
			}
		}
		return false
	}

	var scanTypeExpr func(te ast.TypeExpr)
	scanTypeExpr = func(te ast.TypeExpr) {
		if te == nil {
			return
		}
		switch v := te.(type) {
		case *ast.NamedType:
			for _, ta := range v.TypeArgs {
				scanTypeExpr(ta)
			}
			if tgt.kind == refModuleAlias && v.Module == tgt.name {
				if tk, ok := findFirstTokenNamed(toks, v.Span, v.Module); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
				}
			}
			if tgt.kind == refType && v.Name == tgt.name {
				matches := false
				if nom := lookupNominalType(mod, v.Module, v.Name); nom != nil && matchNominalKey(nom) {
					matches = true
				} else if gst := lookupGenStruct(mod, v.Module, v.Name); gst != nil && gst.Decl != nil && canonicalSpanKey(gst.Decl.Span) == tgt.declKey {
					matches = true
				} else if gen := lookupGenEnum(mod, v.Module, v.Name); gen != nil && gen.Decl != nil && canonicalSpanKey(gen.Decl.Span) == tgt.declKey {
					matches = true
				} else if gi := lookupGenInterface(mod, v.Module, v.Name); gi != nil && gi.Decl != nil && canonicalSpanKey(gi.Decl.Span) == tgt.declKey {
					matches = true
				}
				if matches {
					if tk, ok := findFirstTokenNamed(toks, v.Span, v.Name); ok {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
					}
				}
			}
		case *ast.OptionalType:
			scanTypeExpr(v.Elem)
		case *ast.MutBorrowType:
			scanTypeExpr(v.Elem)
		case *ast.OwnType:
			scanTypeExpr(v.Elem)
		case *ast.VectorType:
			scanTypeExpr(v.Elem)
		case *ast.StackArrayType:
			scanTypeExpr(v.Elem)
		case *ast.SetType:
			scanTypeExpr(v.Elem)
		case *ast.MapType:
			scanTypeExpr(v.Key)
			scanTypeExpr(v.Value)
		case *ast.TupleType:
			for _, el := range v.Elems {
				scanTypeExpr(el)
			}
		case *ast.FuncType:
			for _, p := range v.Params {
				scanTypeExpr(p.Type)
			}
			scanTypeExpr(v.Ret)
		}
	}

	var scanStmt func(bm *sema.BodyMaps, st ast.Stmt)
	var scanExpr func(bm *sema.BodyMaps, e ast.Expr)

	scanStmt = func(bm *sema.BodyMaps, st ast.Stmt) {
		if st == nil {
			return
		}
		switch vs := st.(type) {
		case *ast.VarStmt:
			if vs.IsUnpack {
				var syms []*sema.Symbol
				if bm != nil {
					syms = bm.UnpackVarSymbols[vs]
				}
				for i, uv := range vs.UnpackVars {
					scanTypeExpr(uv.Type)
					if i < len(syms) {
						if ok, unwrap := matchSym(syms[i]); ok {
							if tk, okTk := findHeaderToken(toks, uv.Span, uv.Name); okTk {
								addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
							}
						}
					}
				}
			} else {
				scanTypeExpr(vs.Type)
				if bm != nil {
					if sym := bm.VarSymbols[vs]; sym != nil {
						if ok, unwrap := matchSym(sym); ok {
							if tk, okTk := findHeaderToken(toks, vs.Span, vs.Name); okTk {
								addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
							}
						}
					}
				}
			}
			scanExpr(bm, vs.Value)
			if vs.FallbackUnwind != nil {
				scanExpr(bm, vs.FallbackUnwind.Value)
			}
		case *ast.AssignStmt:
			if vs.IsUnpack {
				for _, t := range vs.UnpackTargets {
					scanExpr(bm, t)
				}
			} else {
				scanExpr(bm, vs.Target)
			}
			scanExpr(bm, vs.Value)
		case *ast.UnwindStmt:
			scanExpr(bm, vs.Value)
		case *ast.ExprStmt:
			scanExpr(bm, vs.Expr)
		}
	}

	scanExpr = func(bm *sema.BodyMaps, e ast.Expr) {
		if e == nil {
			return
		}
		switch v := e.(type) {
		case *ast.BlockExpr:
			for _, st := range v.Stmts {
				scanStmt(bm, st)
			}
			scanExpr(bm, v.Tail)
		case *ast.StmtBodyExpr:
			scanStmt(bm, v.Stmt)
		case *ast.IfExpr:
			if v.CondKind == ast.IfCondOptVar && bm != nil {
				if orig := bm.IfOriginSyms[v]; orig != nil {
					if ok, unwrap := matchSym(orig); ok {
						condRng := v.Span
						if v.Then != nil {
							condRng.End = v.Then.GetSpan().Start
						}
						if tk, okTk := findFirstTokenNamed(toks, condRng, v.OptVar); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isUnwrap: unwrap})
						}
					}
				}
			}
			scanExpr(bm, v.Cond)
			if v.CondKind == ast.IfCondOptBind && bm != nil {
				if unw := bm.IfUnwrapSyms[v]; unw != nil {
					if ok, unwrap := matchSym(unw); ok {
						hdrRng := v.Span
						if v.Then != nil {
							hdrRng.End = v.Then.GetSpan().Start
						}
						if tk, okTk := findLastTokenNamed(toks, hdrRng, v.BindName); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
						}
					}
				}
			}
			scanTypeExpr(v.EvalType)
			scanExpr(bm, v.Then)
			scanExpr(bm, v.Else)
		case *ast.ForExpr:
			if bm != nil {
				fsyms := bm.ForSymbols[v]
				hdrRng := v.Span
				if v.Body != nil {
					hdrRng.End = v.Body.GetSpan().Start
				}
				if fsyms[0] != nil {
					if ok, unwrap := matchSym(fsyms[0]); ok {
						if tk, okTk := findFirstTokenNamed(toks, hdrRng, fsyms[0].Name); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
						}
					}
				}
				if fsyms[1] != nil {
					if ok, unwrap := matchSym(fsyms[1]); ok {
						if tk, okTk := findFirstTokenNamed(toks, hdrRng, fsyms[1].Name); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
						}
					}
				}
			}
			scanTypeExpr(v.EvalType)
			scanExpr(bm, v.CondOrIter)
			scanExpr(bm, v.RangeEnd)
			scanExpr(bm, v.Body)
			scanExpr(bm, v.Else)
		case *ast.WhileExpr:
			scanTypeExpr(v.EvalType)
			scanExpr(bm, v.CondOrIter)
			scanExpr(bm, v.RangeEnd)
			scanExpr(bm, v.Body)
			scanExpr(bm, v.Else)
		case *ast.MatchExpr:
			for _, scr := range v.Scrutinees {
				scanExpr(bm, scr)
			}
			for _, arm := range v.Arms {
				if arm.Synthetic {
					continue
				}
				scanVariantPat := func(vp *ast.VariantPattern) {
					scanExpr(bm, vp.Target)
					if bm != nil {
						syms := bm.PatternSyms[vp]
						for i, b := range vp.Bindings {
							if i < len(syms) {
								if ok, unwrap := matchSym(syms[i]); ok {
									if tk, okTk := findFirstTokenNamed(toks, b.Span, b.BindName); okTk {
										addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
									}
								}
							}
						}
					}
				}
				for _, pat := range arm.AllPatterns() {
					switch p := pat.(type) {
					case *ast.ExprPattern:
						scanExpr(bm, p.Expr)
					case *ast.ContainsPattern:
						scanExpr(bm, p.Container)
					case *ast.MethodPattern:
						if bm != nil {
							if vp := bm.BareVariantPatterns[p]; vp != nil {
								scanVariantPat(vp)
								continue
							}
							if mpInfo := bm.MethodPatterns[p]; mpInfo != nil && mpInfo.Call != nil {
								scanExpr(bm, mpInfo.Call)
								continue
							}
						}
						for _, a := range p.Args {
							scanExpr(bm, a)
						}
					case *ast.VariantPattern:
						scanVariantPat(p)
					}
				}
				scanExpr(bm, arm.Body)
			}
		case *ast.FuncLit:
			lbm := bm
			for _, li := range prog.Lambdas {
				if li != nil && li.Lit == v {
					lbm = li.BodyMaps
					for i, p := range v.Params {
						scanTypeExpr(p.Type)
						if i < len(li.ParamSyms) {
							if ok, unwrap := matchSym(li.ParamSyms[i]); ok {
								if tk, okTk := findHeaderToken(toks, p.Span, p.Name); okTk {
									addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
								}
							}
						}
					}
					break
				}
			}
			scanTypeExpr(v.Ret)
			scanExpr(lbm, v.Body)
		case *ast.CallExpr:
			for _, ta := range v.TypeArgs {
				scanTypeExpr(ta)
			}
			for _, a := range v.Args {
				scanExpr(bm, a)
			}
			inf := exprInfoFromBM(prog, bm, v)
			if inf != nil {
				if tgt.kind == refMethod && inf.CallKind == sema.CallDirectFunc && inf.TargetFunc != nil && canonicalSpanKey(inf.TargetFunc.DeclSpan) == tgt.declKey {
					if mem, ok := v.Callee.(*ast.MemberExpr); ok {
						scanExpr(bm, mem.Base)
						if tk, okTk := findLastTokenNamed(toks, mem.Span, mem.Member); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
						}
						return
					}
				}
				if tgt.kind == refTopSym && ((inf.CallKind == sema.CallDirectFunc && inf.TargetFunc != nil && canonicalSpanKey(inf.TargetFunc.DeclSpan) == tgt.declKey) || (inf.CallKind == sema.CallExternC && inf.TargetExtern != nil && canonicalSpanKey(inf.TargetExtern.Span) == tgt.declKey)) {
					if mem, ok := v.Callee.(*ast.MemberExpr); ok {
						scanExpr(bm, mem.Base)
						if tk, okTk := findLastTokenNamed(toks, mem.Span, mem.Member); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
						}
						return
					}
					if id, ok := v.Callee.(*ast.IdentExpr); ok {
						if tk, okTk := findFirstTokenNamed(toks, id.Span, id.Name); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
						}
						return
					}
				}
				if tgt.kind == refInterfaceMethod && inf.CallKind == sema.CallInterfaceMethod && inf.InterfaceMeth != nil && inf.InterfaceMeth.Name == tgt.name {
					if mem, ok := v.Callee.(*ast.MemberExpr); ok {
						if bInf := exprInfoFromBM(prog, bm, mem.Base); bInf != nil && bInf.Type != nil {
							if typeKeyMatch(unwrapOptAndRef(bInf.Type), tgt.ownerTypeKey, tgt.ownerTypeName) {
								scanExpr(bm, mem.Base)
								if tk, okTk := findLastTokenNamed(toks, mem.Span, mem.Member); okTk {
									addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
								}
								return
							}
						}
					}
				}
				if tgt.kind == refVariant && inf.CallKind == sema.CallEnumVariant && inf.EnumVariant != nil && inf.EnumVariant.Name == tgt.name {
					if typeKeyMatch(inf.EnumType, tgt.ownerTypeKey, tgt.ownerTypeName) {
						if mem, ok := v.Callee.(*ast.MemberExpr); ok {
							scanExpr(bm, mem.Base)
							if tk, okTk := findLastTokenNamed(toks, mem.Span, mem.Member); okTk {
								addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
							}
							return
						}
					}
				}
			}
			scanExpr(bm, v.Callee)
		case *ast.MemberExpr:
			scanExpr(bm, v.Base)
			// Check qualifier Base if it's a type or module alias (when Base isn't a local value)
			if tgt.kind == refType {
				if id, ok := v.Base.(*ast.IdentExpr); ok && id.Name == tgt.name {
					if infBase := exprInfoFromBM(prog, bm, id); infBase == nil || infBase.Sym == nil {
						if nom := lookupNominalType(mod, "", id.Name); nom != nil && matchNominalKey(nom) {
							if tk, okTk := findFirstTokenNamed(toks, id.Span, id.Name); okTk {
								addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
							}
						}
					}
				}
			}
			inf := exprInfoFromBM(prog, bm, v)
			if inf != nil {
				if tgt.kind == refVariant && inf.EnumVariant != nil && inf.EnumVariant.Name == tgt.name && typeKeyMatch(inf.EnumType, tgt.ownerTypeKey, tgt.ownerTypeName) {
					if tk, okTk := findLastTokenNamed(toks, v.Span, v.Member); okTk {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
					}
				}
				if inf.Sym != nil {
					if ok, unwrap := matchSym(inf.Sym); ok {
						if tk, okTk := findLastTokenNamed(toks, v.Span, v.Member); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isUnwrap: unwrap})
						}
					}
				}
				if tgt.kind == refField && v.Member == tgt.name {
					if bInf := exprInfoFromBM(prog, bm, v.Base); bInf != nil && bInf.Type != nil {
						if typeKeyMatch(unwrapOptAndRef(bInf.Type), tgt.ownerTypeKey, tgt.ownerTypeName) {
							if tk, okTk := findLastTokenNamed(toks, v.Span, v.Member); okTk {
								addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
							}
						}
					}
				}
			}
		case *ast.StructLit:
			for _, ta := range v.TypeArgs {
				scanTypeExpr(ta)
			}
			inf := exprInfoFromBM(prog, bm, v)
			if tgt.kind == refModuleAlias && v.Module == tgt.name {
				if tk, okTk := findFirstTokenNamed(toks, v.Span, v.Module); okTk {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
				}
			}
			if tgt.kind == refType && v.TypeName == tgt.name && inf != nil && inf.StructType != nil && matchNominalKey(inf.StructType) {
				if tk, okTk := findFirstTokenNamed(toks, v.Span, v.TypeName); okTk {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
				}
			}
			for _, f := range v.Fields {
				if tgt.kind == refField && f.Name == tgt.name && inf != nil && inf.StructType != nil && typeKeyMatch(inf.StructType, tgt.ownerTypeKey, tgt.ownerTypeName) {
					if tk, okTk := findHeaderToken(toks, f.Span, f.Name); okTk {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
					}
				}
				scanExpr(bm, f.Value)
			}
		case *ast.IdentExpr:
			if tgt.kind == refModuleAlias && v.Name == tgt.name {
				if _, isMod := mod.Aliased[v.Name]; isMod {
					if tk, okTk := findFirstTokenNamed(toks, v.Span, v.Name); okTk {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
					}
				}
			}
			if inf := exprInfoFromBM(prog, bm, v); inf != nil {
				if tgt.kind == refVariant && inf.Sym == nil && inf.EnumVariant != nil && inf.EnumVariant.Name == tgt.name && typeKeyMatch(inf.EnumType, tgt.ownerTypeKey, tgt.ownerTypeName) {
					if tk, okTk := findFirstTokenNamed(toks, v.Span, v.Name); okTk {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
					}
				}
				if inf.Sym != nil {
					if ok, unwrap := matchSym(inf.Sym); ok {
						// Note: `findFirstTokenNamed` avoids duplicating trailing `?` vs `Ident` span
						want := v.Name
						if strings.HasSuffix(tgt.name, "?") && v.IsOpt {
							want = v.Name + "?"
							if tk, okTk := findFirstTokenNamed(toks, v.Span, want); okTk {
								addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isUnwrap: unwrap})
								return
							}
						}
						if tk, okTk := findFirstTokenNamed(toks, v.Span, v.Name); okTk {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isUnwrap: unwrap})
						}
					}
				}
			}
		case *ast.BinaryExpr:
			scanExpr(bm, v.Left)
			scanExpr(bm, v.Right)
		case *ast.UnaryExpr:
			scanExpr(bm, v.Operand)
		case *ast.UnsafeExpr:
			scanExpr(bm, v.Expr)
		case *ast.IndexExpr:
			scanExpr(bm, v.Base)
			scanExpr(bm, v.Index)
		case *ast.SliceExpr:
			scanExpr(bm, v.Base)
			scanExpr(bm, v.Low)
			scanExpr(bm, v.High)
		case *ast.RangeExpr:
			scanExpr(bm, v.Low)
			scanExpr(bm, v.High)
		case *ast.TupleLit:
			for _, el := range v.Elems {
				scanExpr(bm, el)
			}
		case *ast.ArrayLit:
			for _, el := range v.Elems {
				scanExpr(bm, el)
			}
		case *ast.VecLit:
			for _, el := range v.Elems {
				scanExpr(bm, el)
			}
		case *ast.SetLit:
			for _, el := range v.Elems {
				scanExpr(bm, el)
			}
		case *ast.MapLit:
			for _, ent := range v.Entries {
				scanExpr(bm, ent.Key)
				scanExpr(bm, ent.Value)
			}
		case *ast.InterpStringExpr:
			for _, sub := range v.Exprs {
				scanExpr(bm, sub)
			}
		}
	}

	for _, d := range mod.File.Decls {
		switch decl := d.(type) {
		case *ast.ImportDecl:
			if tgt.kind == refModuleAlias && decl.Alias == tgt.name {
				if tk, ok := findLastTokenNamed(toks, decl.Span, decl.Alias); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
				}
			}
		case *ast.StructDecl:
			if tgt.kind == refType && canonicalSpanKey(decl.Span) == tgt.declKey {
				if tk, ok := findHeaderToken(toks, decl.Span, decl.Name); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
				}
			}
			for _, tp := range decl.TypeParams {
				for _, b := range tp.Bounds {
					if tgt.kind == refType && b == tgt.name {
						matches := false
						if nom := lookupNominalType(mod, "", b); nom != nil && matchNominalKey(nom) {
							matches = true
						} else if gi := lookupGenInterface(mod, "", b); gi != nil && gi.Decl != nil && canonicalSpanKey(gi.Decl.Span) == tgt.declKey {
							matches = true
						}
						if matches {
							if tk, ok := findFirstTokenNamed(toks, tp.Span, b); ok {
								addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
							}
						}
					}
				}
			}
			for _, f := range decl.Fields {
				if tgt.kind == refField && f.Name == tgt.name && canonicalSpanKey(decl.Span) == tgt.ownerTypeKey {
					if tk, ok := findHeaderToken(toks, f.Span, f.Name); ok {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
					}
				}
				scanTypeExpr(f.Type)
				if f.Default != nil {
					scanExpr(prog.TopBodyMaps, f.Default)
				}
			}
		case *ast.EnumDecl:
			if tgt.kind == refType && canonicalSpanKey(decl.Span) == tgt.declKey {
				if tk, ok := findHeaderToken(toks, decl.Span, decl.Name); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
				}
			}
			for _, ev := range decl.Variants {
				if tgt.kind == refVariant && ev.Name == tgt.name && canonicalSpanKey(decl.Span) == tgt.ownerTypeKey {
					if tk, ok := findHeaderToken(toks, ev.Span, ev.Name); ok {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
					}
				}
				scanTypeExpr(ev.PayloadType)
			}
		case *ast.InterfaceDecl:
			if tgt.kind == refType && canonicalSpanKey(decl.Span) == tgt.declKey {
				if tk, ok := findHeaderToken(toks, decl.Span, decl.Name); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
				}
			}
			for _, m := range decl.Methods {
				if tgt.kind == refInterfaceMethod && m.Name == tgt.name && canonicalSpanKey(decl.Span) == tgt.ownerTypeKey {
					if tk, ok := findHeaderToken(toks, m.Span, m.Name); ok {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
					}
				}
				for _, p := range m.Params {
					scanTypeExpr(p.Type)
				}
				scanTypeExpr(m.Ret)
			}
		case *ast.DistinctTypeDecl:
			if tgt.kind == refType && canonicalSpanKey(decl.Span) == tgt.declKey {
				if tk, ok := findHeaderToken(toks, decl.Span, decl.Name); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
				}
			}
			scanTypeExpr(decl.Base)
		case *ast.ExternFnDecl:
			name := decl.Name
			if decl.Alias != "" {
				name = decl.Alias
			}
			if tgt.kind == refTopSym && canonicalSpanKey(decl.Span) == tgt.declKey {
				if tk, ok := findHeaderToken(toks, decl.Span, name); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
				}
			}
			for _, p := range decl.Params {
				scanTypeExpr(p.Type)
			}
			scanTypeExpr(decl.Ret)
		case *ast.MethodDecl:
			if tgt.kind == refType && decl.RecvType == tgt.name {
				if nom := lookupNominalType(mod, "", decl.RecvType); nom != nil && matchNominalKey(nom) {
					if tk, ok := findHeaderToken(toks, decl.Span, decl.RecvType); ok {
						addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset})
					}
				}
			}
			if tgt.kind == refMethod && canonicalSpanKey(decl.Span) == tgt.declKey {
				if tk, ok := findMethodDeclToken(toks, decl.Span, decl.Name); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
				}
			}
			if tgt.kind == refInterfaceMethod && decl.Name == tgt.name {
				for _, ifc := range prog.TypeImplementedInterfaceTargets(decl.RecvType, mod) {
					if canonicalSpanKey(ifc.Span) == tgt.ownerTypeKey || ifc.Name == tgt.ownerTypeName {
						if tk, ok := findMethodDeclToken(toks, decl.Span, decl.Name); ok {
							addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
						}
					}
				}
			}
			for _, p := range decl.Params {
				scanTypeExpr(p.Type)
			}
			scanTypeExpr(decl.Ret)
			fi := findFuncInstanceForDecl(prog, mod, decl.Span)
			var bm *sema.BodyMaps
			if fi != nil {
				bm = fi.BodyMaps
				if tgt.kind == refLocalVar {
					pOff := 0
					if !fi.IsStaticAssoc {
						pOff = 1
					}
					for i, p := range decl.Params {
						if i+pOff < len(fi.ParamSyms) {
							if ok, unwrap := matchSym(fi.ParamSyms[i+pOff]); ok {
								if tk, okTk := findHeaderToken(toks, p.Span, p.Name); okTk {
									addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
								}
							}
						}
					}
				}
			}
			if tgt.kind != refLocalVar || canonicalSpanKey(decl.Span) == canonicalSpanKey(tgt.ownerBodySpan) || tgt.ownerBodySpan.Start.Line <= 0 {
				scanExpr(bm, decl.Body)
			}
		case *ast.VarDecl:
			if tgt.kind == refTopSym && canonicalSpanKey(decl.Span) == tgt.declKey {
				if tk, ok := findHeaderToken(toks, decl.Span, decl.Name); ok {
					addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true})
				}
			}
			scanTypeExpr(decl.Type)
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				fi := findFuncInstanceForDecl(prog, mod, decl.Span)
				var bm *sema.BodyMaps
				if fi != nil {
					bm = fi.BodyMaps
					if tgt.kind == refLocalVar {
						for i, p := range fl.Params {
							if i < len(fi.ParamSyms) {
								if ok, unwrap := matchSym(fi.ParamSyms[i]); ok {
									if tk, okTk := findHeaderToken(toks, p.Span, p.Name); okTk {
										addOcc(refOccurrence{filePath: absPath, startOff: tk.Span.Start.Offset, endOff: tk.Span.End.Offset, isDecl: true, isUnwrap: unwrap})
									}
								}
							}
						}
					}
				}
				for _, p := range fl.Params {
					scanTypeExpr(p.Type)
				}
				scanTypeExpr(fl.Ret)
				if tgt.kind != refLocalVar || canonicalSpanKey(decl.Span) == canonicalSpanKey(tgt.ownerBodySpan) || tgt.ownerBodySpan.Start.Line <= 0 {
					scanExpr(bm, fl.Body)
				}
			} else {
				scanExpr(prog.TopBodyMaps, decl.Value)
			}
		}
	}
}
