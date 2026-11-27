package sema

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

var expandSynthSpan = diag.Span{
	Start: diag.Pos{Offset: -1},
	End:   diag.Pos{Offset: -1},
}

type astExpander struct {
	prog            *CheckedProgram
	mod             *Module
	inDerivedExpand bool
}

func collectModuleFuncsBySpan(prog *CheckedProgram, mod *Module) map[diag.Span][]*FuncInstance {
	funcsBySpan := make(map[diag.Span][]*FuncInstance)
	addFuncs := func(list []*FuncInstance) {
		for _, fi := range list {
			if fi == nil || fi.Mod != mod || fi.BodyMaps == nil {
				continue
			}
			funcsBySpan[fi.DeclSpan] = append(funcsBySpan[fi.DeclSpan], fi)
		}
	}
	addFuncs(prog.Funcs)
	addFuncs(prog.TestFuncs)
	for sp, list := range funcsBySpan {
		sort.Slice(list, func(i, j int) bool {
			return list[i].CName < list[j].CName
		})
		funcsBySpan[sp] = list
	}
	return funcsBySpan
}

func collectDerivedDeclsByRecv(mod *Module) map[string][]*ast.MethodDecl {
	out := make(map[string][]*ast.MethodDecl)
	if mod == nil {
		return out
	}
	for _, d := range mod.DerivedDecls {
		if md, ok := d.(*ast.MethodDecl); ok && md != nil {
			out[md.RecvType] = append(out[md.RecvType], md)
		}
	}
	return out
}

// ExpandModuleAST returns a cloned *ast.File for mod whose top-level `comptime if`s
// are pruned, evaluated `const`s / `comptime { ... }` blocks are folded where convertible,
// local `comptime if` / `comptime match` branches are resolved, `comptime for` loops
// are unrolled, and `@derive(...)` methods are materialized using their checked BodyMaps.
func ExpandModuleAST(prog *CheckedProgram, mod *Module) *ast.File {
	if prog == nil || mod == nil {
		return nil
	}
	ex := &astExpander{prog: prog, mod: mod}
	funcsBySpan := collectModuleFuncsBySpan(prog, mod)
	derivedByRecv := collectDerivedDeclsByRecv(mod)

	exp := &ast.File{
		Path: mod.Path,
	}
	for _, d := range mod.ActiveDecls() {
		if d == nil {
			continue
		}
		exp.Decls = append(exp.Decls, ex.expandSingleDecl(d, funcsBySpan, derivedByRecv)...)
	}
	return exp
}

// ExpandDeclPreview returns a focused *ast.File expanding only `targetDecl` (plus any
// synthesized `@derive` methods or monomorphized specializations) for IDE hover/preview.
func ExpandDeclPreview(prog *CheckedProgram, mod *Module, targetDecl ast.Decl) *ast.File {
	if prog == nil || mod == nil || targetDecl == nil {
		return nil
	}
	ex := &astExpander{prog: prog, mod: mod}
	funcsBySpan := collectModuleFuncsBySpan(prog, mod)
	derivedByRecv := collectDerivedDeclsByRecv(mod)
	return &ast.File{
		Path:  mod.Path,
		Decls: ex.expandSingleDecl(targetDecl, funcsBySpan, derivedByRecv),
	}
}

// ExpandExprPreview returns an expanded AST expression inside `fi` (or top-level if `fi == nil`)
// for IDE hover preview on `comptime` expressions, `comptime if`, `comptime for`, or `comptime match`.
func ExpandExprPreview(prog *CheckedProgram, mod *Module, fi *FuncInstance, expr ast.Expr) ast.Expr {
	if prog == nil || mod == nil || expr == nil {
		return nil
	}
	bm := prog.TopBodyMaps
	if fi != nil && fi.BodyMaps != nil {
		bm = fi.BodyMaps
	}
	ex := &astExpander{prog: prog, mod: mod}
	out, keep := ex.expandExprWithBM(expr, bm, false, false)
	if !keep {
		return nil
	}
	return out
}

func (ex *astExpander) expandSingleDecl(d ast.Decl, funcsBySpan map[diag.Span][]*FuncInstance, derivedByRecv map[string][]*ast.MethodDecl) []ast.Decl {
	switch decl := d.(type) {
	case *ast.VarDecl:
		insts := funcsBySpan[decl.Span]
		if fl, isFn := decl.Value.(*ast.FuncLit); isFn && len(decl.TypeParams) > 0 {
			// Emit generic template plus each instantiated specialization.
			out := []ast.Decl{ast.CloneDecl(decl)}
			for _, fi := range insts {
				if spec := ex.buildExpandedFuncSpec(decl, fl, fi); spec != nil {
					out = append(out, spec)
				}
			}
			return out
		}
		cd := ast.CloneDecl(decl).(*ast.VarDecl)
		if fl, isFn := cd.Value.(*ast.FuncLit); isFn {
			var bm *BodyMaps
			if len(insts) > 0 {
				bm = insts[0].BodyMaps
			} else if sym := ex.mod.Symbols[decl.Name]; sym != nil && sym.FuncInst != nil {
				bm = sym.FuncInst.BodyMaps
			}
			if bm != nil {
				if origFl, ok := decl.Value.(*ast.FuncLit); ok {
					fl.Body = ex.expandBodyWithBM(origFl.Body, bm, declIsVoidRet(decl.Value))
				}
			}
		} else if decl.IsConst {
			sym := ex.mod.Symbols[decl.Name]
			if folded := ex.evalSymToASTExpr(sym, cd.Type != nil); folded != nil {
				cd.Value = folded
				if cd.Type == nil && sym != nil && ex.needsExplicitDeclType(sym.Type, cd.Value) {
					cd.Type = ex.typeToASTTypeExpr(sym.Type)
					if reFolded := ex.evalSymToASTExpr(sym, true); reFolded != nil {
						cd.Value = reFolded
					}
				}
			} else if ex.prog.TopBodyMaps != nil {
				val, _ := ex.expandExprWithBM(decl.Value, ex.prog.TopBodyMaps, false, cd.Type != nil)
				cd.Value = val
			}
		} else if ex.prog.TopBodyMaps != nil && cd.Value != nil {
			val, _ := ex.expandExprWithBM(decl.Value, ex.prog.TopBodyMaps, false, cd.Type != nil)
			cd.Value = val
		}
		return []ast.Decl{cd}

	case *ast.MethodDecl:
		insts := funcsBySpan[decl.Span]
		if len(decl.RecvTypeParams) > 0 || len(decl.TypeParams) > 0 {
			out := []ast.Decl{ast.CloneDecl(decl)}
			for _, fi := range insts {
				if spec := ex.buildExpandedMethodSpec(decl, fi); spec != nil {
					out = append(out, spec)
				}
			}
			return out
		}
		cm := ast.CloneDecl(decl).(*ast.MethodDecl)
		if len(insts) > 0 {
			cm.IsConst = insts[0].IsConst
			if insts[0].BodyMaps != nil {
				cm.Body = ex.expandBodyWithBM(decl.Body, insts[0].BodyMaps, decl.Ret == nil)
			}
		}
		return []ast.Decl{cm}

	case *ast.StructDecl:
		return ex.expandNominalWithDerives(decl, decl.Name, len(decl.TypeParams) > 0, funcsBySpan, derivedByRecv[decl.Name])
	case *ast.EnumDecl:
		return ex.expandNominalWithDerives(decl, decl.Name, len(decl.TypeParams) > 0, funcsBySpan, derivedByRecv[decl.Name])
	case *ast.DistinctTypeDecl:
		return ex.expandNominalWithDerives(decl, decl.Name, false, funcsBySpan, derivedByRecv[decl.Name])

	default:
		return []ast.Decl{ast.CloneDecl(d)}
	}
}

func stripDeriveAttributes(attrs []ast.Attribute) []ast.Attribute {
	if len(attrs) == 0 {
		return nil
	}
	var out []ast.Attribute
	for _, attr := range attrs {
		if attr.Name == "derive" {
			continue
		}
		out = append(out, attr)
	}
	return out
}

func (ex *astExpander) expandNominalWithDerives(decl ast.Decl, typeName string, isGeneric bool, funcsBySpan map[diag.Span][]*FuncInstance, derived []*ast.MethodDecl) []ast.Decl {
	cloned := ast.CloneDecl(decl)
	if len(derived) == 0 {
		return []ast.Decl{cloned}
	}

	var emitted []ast.Decl
	if !isGeneric {
		// Concrete @derive: strip `@derive(...)` so emitted derived methods don't collide on re-check.
		switch d := cloned.(type) {
		case *ast.StructDecl:
			d.Attributes = stripDeriveAttributes(d.Attributes)
		case *ast.EnumDecl:
			d.Attributes = stripDeriveAttributes(d.Attributes)
		case *ast.DistinctTypeDecl:
			d.Attributes = stripDeriveAttributes(d.Attributes)
		}
		emitted = append(emitted, cloned)
		for _, md := range derived {
			fi := findMatchingDerivedFuncInstance(funcsBySpan[md.Span], typeName, md.Name, false)
			emitted = append(emitted, ex.expandDerivedMethodDecl(md, fi))
		}
		return emitted
	}

	// Generic @derive: keep `@derive(...)` on the generic declaration and emit concrete checked instantiations.
	emitted = append(emitted, cloned)
	for _, md := range derived {
		for _, fi := range funcsBySpan[md.Span] {
			if fi == nil || !fi.IsMethod || fi.DeclSpan != md.Span {
				continue
			}
			if !strings.HasSuffix(fi.Name, "."+md.Name) {
				continue
			}
			if spec := ex.buildExpandedDerivedGenericSpec(md, fi); spec != nil {
				emitted = append(emitted, spec)
			}
		}
	}
	return emitted
}

func findMatchingDerivedFuncInstance(insts []*FuncInstance, typeName, methodName string, isGeneric bool) *FuncInstance {
	exact := typeName + "." + methodName
	genPrefix := typeName + "["
	suffix := "." + methodName
	for _, fi := range insts {
		if fi == nil || !fi.IsMethod {
			continue
		}
		if !isGeneric && fi.Name == exact {
			return fi
		}
		if isGeneric && strings.HasPrefix(fi.Name, genPrefix) && strings.HasSuffix(fi.Name, suffix) {
			return fi
		}
	}
	return nil
}

func findDeriveImplTargetFI(fi *FuncInstance) *FuncInstance {
	if fi == nil || fi.BodyMaps == nil || fi.Body == nil {
		return nil
	}
	if call, ok := fi.Body.(*ast.CallExpr); ok {
		if inf := fi.BodyMaps.ExprInfos[call]; inf != nil && inf.TargetFunc != nil {
			return inf.TargetFunc
		}
	}
	for exNode, inf := range fi.BodyMaps.ExprInfos {
		if _, ok := exNode.(*ast.CallExpr); ok && inf != nil && inf.TargetFunc != nil && inf.TargetFunc != fi {
			return inf.TargetFunc
		}
	}
	return nil
}

func (ex *astExpander) expandDerivedMethodBody(fi *FuncInstance, md *ast.MethodDecl, isStatic bool, paramRenames map[string]string, voidRet bool) (ast.Expr, bool) {
	targetFI := findDeriveImplTargetFI(fi)
	if targetFI == nil || targetFI.Body == nil || targetFI.BodyMaps == nil {
		return nil, false
	}

	prevDerived := ex.inDerivedExpand
	ex.inDerivedExpand = true
	body := ex.expandBodyWithBM(targetFI.Body, targetFI.BodyMaps, voidRet)
	ex.inDerivedExpand = prevDerived

	rename := make(map[string]string)
	argOffset := 0
	if !isStatic && len(targetFI.ParamNames) > 0 {
		rename[targetFI.ParamNames[0]] = "self"
		if rnm, ok := paramRenames[targetFI.ParamNames[0]]; ok {
			rename[targetFI.ParamNames[0]] = rnm
		}
		argOffset = 1
	}
	for i := range md.Params {
		idx := argOffset + i
		if idx >= len(targetFI.ParamNames) {
			continue
		}
		srcName := targetFI.ParamNames[idx]
		if dstName, ok := paramRenames[srcName]; ok {
			rename[srcName] = dstName
		}
	}
	if len(rename) > 0 {
		body = cloneAndRenameASTIdents(body, rename)
	}
	return body, true
}

func sanitizeDerivedParamName(rawName, fallback string, used map[string]bool) string {
	name := rawName
	if name == "" || name == "self" {
		name = fallback
	}
	// Ensure optional/result suffix sigil matches fallback if present.
	if strings.HasSuffix(fallback, "?") && !strings.HasSuffix(name, "?") {
		name = strings.TrimSuffix(name, "!") + "?"
	} else if strings.HasSuffix(fallback, "!") && !strings.HasSuffix(name, "!") {
		name = strings.TrimSuffix(name, "?") + "!"
	}
	base := name
	for i := 2; used[name]; i++ {
		switch {
		case strings.HasSuffix(base, "?"):
			name = fmt.Sprintf("%s_%d?", strings.TrimSuffix(base, "?"), i)
		case strings.HasSuffix(base, "!"):
			name = fmt.Sprintf("%s_%d!", strings.TrimSuffix(base, "!"), i)
		default:
			name = fmt.Sprintf("%s_%d", base, i)
		}
	}
	used[name] = true
	return name
}

func (ex *astExpander) expandDerivedMethodDecl(md *ast.MethodDecl, fi *FuncInstance) *ast.MethodDecl {
	cm := ast.CloneDecl(md).(*ast.MethodDecl)
	cm.Span = expandSynthSpan
	if fi == nil {
		return cm
	}
	cm.IsConst = fi.IsConst

	targetFI := findDeriveImplTargetFI(fi)
	used := map[string]bool{"self": true}
	paramRenames := make(map[string]string)
	argOffset := 0
	if cm.RecvMode != ast.ReceiverMode(-1) {
		argOffset = 1
	}
	if targetFI != nil {
		for i := range cm.Params {
			idx := argOffset + i
			if idx < len(targetFI.ParamNames) {
				clean := sanitizeDerivedParamName(targetFI.ParamNames[idx], cm.Params[i].Name, used)
				paramRenames[targetFI.ParamNames[idx]] = clean
				cm.Params[i].Name = clean
			}
		}
	}
	if fi.Sig != nil {
		if fi.Sig.Ret != nil && fi.Sig.Ret != types.TypeVoid {
			if retTE := ex.typeToASTTypeExpr(fi.Sig.Ret); retTE != nil {
				cm.Ret = retTE
			}
		}
		for i := range cm.Params {
			idx := argOffset + i
			if idx < len(fi.Sig.Params) {
				if pte := ex.paramTypeToASTTypeExpr(fi.Sig.Params[idx]); pte != nil {
					cm.Params[i].Type = pte
				}
			}
		}
	}

	if expanded, ok := ex.expandDerivedMethodBody(fi, cm, cm.RecvMode == ast.ReceiverMode(-1), paramRenames, cm.Ret == nil); ok {
		cm.Body = expanded
	} else if fi.BodyMaps != nil {
		cm.Body = ex.expandBodyWithBM(md.Body, fi.BodyMaps, cm.Ret == nil)
	}
	return cm
}

func (ex *astExpander) buildExpandedDerivedGenericSpec(md *ast.MethodDecl, fi *FuncInstance) *ast.VarDecl {
	if md == nil || fi == nil || fi.RecvType == nil || fi.Sig == nil {
		return nil
	}
	targetFI := findDeriveImplTargetFI(fi)
	if targetFI == nil || targetFI.Body == nil || targetFI.BodyMaps == nil {
		return nil
	}
	used := make(map[string]bool)
	paramRenames := make(map[string]string)
	params := make([]ast.Param, 0, len(fi.Sig.Params))
	for i, sp := range fi.Sig.Params {
		rawName := ""
		if i < len(targetFI.ParamNames) {
			rawName = targetFI.ParamNames[i]
		}
		fallback := fmt.Sprintf("_p%d", i)
		if _, isOpt := sp.Type.(*types.Optional); isOpt {
			fallback += "?"
		} else if types.AsResult(sp.Type) != nil {
			fallback += "!"
		}
		if rawName == "self" {
			rawName = "this"
		}
		clean := sanitizeDerivedParamName(rawName, fallback, used)
		if i < len(targetFI.ParamNames) && targetFI.ParamNames[i] != "" {
			paramRenames[targetFI.ParamNames[i]] = clean
		}
		params = append(params, ast.Param{
			Name: clean,
			Type: ex.paramTypeToASTTypeExpr(sp),
			Span: expandSynthSpan,
		})
	}
	var retAST ast.TypeExpr
	if fi.Sig.Ret != nil && fi.Sig.Ret != types.TypeVoid {
		retAST = ex.typeToASTTypeExpr(fi.Sig.Ret)
	}
	body, ok := ex.expandDerivedMethodBody(fi, md, true, paramRenames, retAST == nil)
	if !ok {
		return nil
	}
	var attrs []ast.Attribute
	for _, a := range md.Attributes {
		attrs = append(attrs, a)
	}
	specName := fmt.Sprintf("%s_%s", types.SanitizeCIdent(fi.RecvType.Mangled()), md.Name)
	return &ast.VarDecl{
		IsBind:     !fi.IsConst,
		IsConst:    fi.IsConst,
		Name:       specName,
		Attributes: attrs,
		Doc:        fmt.Sprintf("Specialization of `@derive` `%s.%s`.", fi.RecvType.String(), md.Name),
		Value: &ast.FuncLit{
			Params: params,
			Ret:    retAST,
			Body:   body,
			Span:   expandSynthSpan,
		},
		Span: expandSynthSpan,
	}
}

func (ex *astExpander) paramTypeToASTTypeExpr(p types.FuncParam) ast.TypeExpr {
	pt := ex.typeToASTTypeExpr(p.Type)
	if pt == nil {
		return nil
	}
	switch p.Mode {
	case types.ModeMut:
		return &ast.MutBorrowType{Elem: pt, Span: expandSynthSpan}
	case types.ModeOwn:
		return &ast.OwnType{Elem: pt, Span: expandSynthSpan}
	default:
		return pt
	}
}

func declIsVoidRet(val ast.Expr) bool {
	if fl, ok := val.(*ast.FuncLit); ok {
		return fl.Ret == nil
	}
	return false
}

func (ex *astExpander) specializedName(baseName string, typeSubst map[string]types.Type) string {
	if len(typeSubst) == 0 {
		return baseName
	}
	keys := make([]string, 0, len(typeSubst))
	for k := range typeSubst {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if t := typeSubst[k]; t != nil {
			parts = append(parts, types.SanitizeCIdent(t.Mangled()))
		}
	}
	if len(parts) == 0 {
		return baseName
	}
	return fmt.Sprintf("%s__%s", baseName, strings.Join(parts, "_"))
}

func (ex *astExpander) buildExpandedFuncSpec(orig *ast.VarDecl, fl *ast.FuncLit, fi *FuncInstance) *ast.VarDecl {
	if orig == nil || fl == nil || fi == nil {
		return nil
	}
	cloned := ast.CloneDecl(orig).(*ast.VarDecl)
	cloned.TypeParams = nil
	cloned.Name = ex.specializedName(orig.Name, fi.TypeSubst)
	cloned.IsConst = fi.IsConst
	cloned.IsBind = !fi.IsConst
	cloned.Span = expandSynthSpan
	if cfl, ok := cloned.Value.(*ast.FuncLit); ok {
		if fi.Sig != nil {
			for i := range cfl.Params {
				if i < len(fi.Sig.Params) {
					if pte := ex.paramTypeToASTTypeExpr(fi.Sig.Params[i]); pte != nil {
						cfl.Params[i].Type = pte
					}
				}
			}
			if fi.Sig.Ret != nil && fi.Sig.Ret != types.TypeVoid {
				if retTE := ex.typeToASTTypeExpr(fi.Sig.Ret); retTE != nil {
					cfl.Ret = retTE
				}
			} else {
				cfl.Ret = nil
			}
		}
		expandedBody := ex.expandBodyWithBM(fl.Body, fi.BodyMaps, fl.Ret == nil)
		cfl.Body = ex.prependSpecTypeSubst(expandedBody, nil, orig.TypeParams, fi.TypeSubst)
	}
	return cloned
}

func (ex *astExpander) buildExpandedMethodSpec(orig *ast.MethodDecl, fi *FuncInstance) *ast.MethodDecl {
	if orig == nil || fi == nil {
		return nil
	}
	cm := ast.CloneDecl(orig).(*ast.MethodDecl)
	cm.IsConst = fi.IsConst
	cm.Name = ex.specializedName(orig.Name, fi.TypeSubst)
	cm.TypeParams = nil
	cm.RecvTypeParams = nil
	argOffset := 0
	if cm.RecvMode != ast.ReceiverMode(-1) {
		argOffset = 1
		if cm.RecvExplicitTy != nil && fi.RecvType != nil {
			if rte := ex.typeToASTTypeExpr(fi.RecvType); rte != nil {
				cm.RecvExplicitTy = rte
			}
		}
	}
	if fi.Sig != nil {
		for i := range cm.Params {
			idx := argOffset + i
			if idx < len(fi.Sig.Params) {
				if pte := ex.paramTypeToASTTypeExpr(fi.Sig.Params[idx]); pte != nil {
					cm.Params[i].Type = pte
				}
			}
		}
		if fi.Sig.Ret != nil && fi.Sig.Ret != types.TypeVoid {
			if retTE := ex.typeToASTTypeExpr(fi.Sig.Ret); retTE != nil {
				cm.Ret = retTE
			}
		} else {
			cm.Ret = nil
		}
	}
	expandedBody := ex.expandBodyWithBM(orig.Body, fi.BodyMaps, cm.Ret == nil)
	cm.Body = ex.prependSpecTypeSubst(expandedBody, orig.RecvTypeParams, orig.TypeParams, fi.TypeSubst)
	cm.Span = expandSynthSpan
	return cm
}

func (ex *astExpander) prependSpecTypeSubst(body ast.Expr, recvTParams []string, tparams []ast.TypeParam, subst map[string]types.Type) ast.Expr {
	if body == nil || len(subst) == 0 || (len(recvTParams) == 0 && len(tparams) == 0) {
		return body
	}
	var stmts []ast.Stmt
	seenNames := make(map[string]bool)
	appendConst := func(name string) {
		if seenNames[name] {
			return
		}
		ty := subst[name]
		cv, ok := ty.(*types.ConstValue)
		if !ok {
			return
		}
		valExpr := constValueToASTExpr(cv)
		if valExpr == nil {
			return
		}
		seenNames[name] = true
		var typeAnn ast.TypeExpr
		if ex.needsExplicitDeclType(cv.ConstType, valExpr) {
			typeAnn = ex.typeToASTTypeExpr(cv.ConstType)
		}
		stmts = append(stmts, &ast.VarStmt{
			IsConst: true,
			Name:    name,
			Type:    typeAnn,
			Value:   valExpr,
			Span:    expandSynthSpan,
		})
	}
	for _, rtp := range recvTParams {
		appendConst(rtp)
	}
	for _, tp := range tparams {
		if !tp.IsConst {
			continue
		}
		appendConst(tp.Name)
	}
	if len(stmts) == 0 {
		return body
	}
	if blk, ok := body.(*ast.BlockExpr); ok {
		nb := *blk
		nb.Stmts = append(stmts, blk.Stmts...)
		return &nb
	}
	return &ast.BlockExpr{
		Stmts: stmts,
		Tail:  body,
		Span:  expandSynthSpan,
	}
}

func (ex *astExpander) evalSymToASTExpr(sym *Symbol, hasExpectedCtx bool) ast.Expr {
	if sym == nil || sym.ConstVal == nil {
		return nil
	}
	sd := ExportStaticData(sym.ConstVal, sym.Type)
	if sd == nil {
		return nil
	}
	return ex.staticDataToASTExpr(sd, hasExpectedCtx)
}

func (ex *astExpander) expandBodyWithBM(body ast.Expr, bm *BodyMaps, isVoid bool) ast.Expr {
	out, keep := ex.expandExprWithBM(body, bm, isVoid, false)
	if !keep || out == nil {
		return &ast.BlockExpr{Span: expandSynthSpan}
	}
	return out
}

func (ex *astExpander) expandExprWithBM(e ast.Expr, bm *BodyMaps, isVoid, hasExpectedCtx bool) (ast.Expr, bool) {
	if e == nil {
		return nil, true
	}
	if bm == nil {
		return ast.CloneExpr(e), true
	}

	switch v := e.(type) {
	case *ast.ComptimeExpr:
		if cv, ok := bm.ComptimeVals[v]; ok && cv != nil {
			var expectedTy types.Type
			if inf := bm.ExprInfos[v]; inf != nil {
				expectedTy = inf.Type
			}
			if sd := ExportStaticData(cv, expectedTy); sd != nil {
				if folded := ex.staticDataToASTExpr(sd, hasExpectedCtx); folded != nil {
					return folded, true
				}
			}
		}
		return ex.expandExprWithBM(v.Expr, bm, isVoid, hasExpectedCtx)

	case *ast.MemberExpr, *ast.IndexExpr:
		if cv, ok := bm.ComptimeVals[e]; ok && cv != nil {
			var expectedTy types.Type
			if inf := bm.ExprInfos[e]; inf != nil {
				expectedTy = inf.Type
			}
			if sd := ExportStaticData(cv, expectedTy); sd != nil {
				if folded := ex.staticDataToASTExpr(sd, hasExpectedCtx); folded != nil {
					return folded, true
				}
			}
		}
	}

	switch v := e.(type) {
	case *ast.IfExpr:
		if v.IsComptime {
			if branch, ok := bm.ComptimeIfBranch[v]; ok {
				switch branch {
				case 0:
					return ex.expandExprWithBM(v.Then, bm, isVoid, hasExpectedCtx)
				case 2:
					return ex.expandExprWithBM(v.Else, bm, isVoid, hasExpectedCtx)
				case 3:
					if isVoid {
						return nil, false
					}
					return &ast.BlockExpr{Span: expandSynthSpan}, true
				}
			}
		}
		cp := *v
		cp.Cond, _ = ex.expandExprWithBM(v.Cond, bm, false, false)
		cp.EvalType = ast.CloneTypeExpr(v.EvalType)
		cp.Then = ex.expandBodyWithBM(v.Then, bm, isVoid)
		if v.Else != nil {
			cp.Else = ex.expandBodyWithBM(v.Else, bm, isVoid)
		}
		return &cp, true

	case *ast.MatchExpr:
		if v.IsComptime {
			if armIdx, ok := bm.ComptimeMatchArm[v]; ok {
				arms := v.Arms
				if expanded, hasExp := bm.MatchArms[v]; hasExp {
					arms = expanded
				}
				if armIdx >= 0 && armIdx < len(arms) {
					arm := arms[armIdx]
					bodyExpr, keep := ex.expandExprWithBM(arm.Body, bm, isVoid, hasExpectedCtx)
					if !keep {
						return nil, false
					}
					bindings := ex.extractMatchArmConstStmts(arm, bm)
					if len(bindings) == 0 {
						return bodyExpr, true
					}
					return wrapPrefixStmts(bindings, bodyExpr), true
				}
			}
		}
		cp := *v
		cp.Scrutinees = make([]ast.Expr, len(v.Scrutinees))
		for i, sc := range v.Scrutinees {
			cp.Scrutinees[i], _ = ex.expandExprWithBM(sc, bm, false, false)
		}
		cp.EvalType = ast.CloneTypeExpr(v.EvalType)
		cp.Arms = make([]ast.MatchArm, len(v.Arms))
		for i, arm := range v.Arms {
			ca := arm
			if arm.Cases != nil {
				ca.Cases = make([]ast.MatchCase, len(arm.Cases))
				for j, mc := range arm.Cases {
					cmc := mc
					if mc.Patterns != nil {
						cmc.Patterns = make([]ast.Pattern, len(mc.Patterns))
						for k, p := range mc.Patterns {
							cmc.Patterns[k] = ast.ClonePattern(p)
						}
					}
					ca.Cases[j] = cmc
				}
			}
			ca.Body = ex.expandBodyWithBM(arm.Body, bm, isVoid)
			cp.Arms[i] = ca
		}
		return &cp, true

	case *ast.ForExpr:
		if v.IsComptime {
			if uf, ok := bm.ComptimeForUnrolls[v]; ok && uf != nil {
				if isVoid {
					var stmts []ast.Stmt
					for _, it := range uf.Iters {
						itExpr := ex.buildExpandedForIterExpr(it, bm, true)
						stmts = append(stmts, &ast.ExprStmt{Expr: itExpr, Span: expandSynthSpan})
					}
					if v.Else != nil && (len(uf.Iters) == 0 || !forUnrollHasUnwind(uf)) {
						if el, keep := ex.expandExprWithBM(v.Else, bm, true, false); keep && el != nil {
							stmts = append(stmts, &ast.ExprStmt{Expr: el, Span: expandSynthSpan})
						}
					}
					if len(stmts) == 0 {
						return nil, false
					}
					return &ast.BlockExpr{Stmts: stmts, Span: expandSynthSpan}, true
				}
				if len(uf.Iters) == 0 && v.Else != nil {
					return ex.expandExprWithBM(v.Else, bm, false, hasExpectedCtx)
				}
				if uf.CollectStruct != nil && !forUnrollHasUnwind(uf) {
					flds := make([]ast.StructFieldInit, 0, len(uf.Iters))
					for idx, it := range uf.Iters {
						tIdx := it.TargetIdx
						if tIdx < 0 {
							tIdx = idx
						}
						if tIdx < len(uf.CollectStruct.Fields) {
							flds = append(flds, ast.StructFieldInit{
								Name:  uf.CollectStruct.Fields[tIdx].Name,
								Value: ex.buildExpandedForIterExpr(it, bm, false),
								Span:  expandSynthSpan,
							})
						}
					}
					var typeArgs []ast.TypeExpr
					if len(uf.CollectStruct.TypeArgs) > 0 {
						typeArgs = make([]ast.TypeExpr, len(uf.CollectStruct.TypeArgs))
						for i, ta := range uf.CollectStruct.TypeArgs {
							typeArgs[i] = ex.typeToASTTypeExpr(ta)
						}
					}
					return &ast.StructLit{
						Module:   ex.nominalModAlias(uf.CollectStruct),
						TypeName: stripGenericSuffix(uf.CollectStruct.Name),
						TypeArgs: typeArgs,
						Fields:   flds,
						Span:     expandSynthSpan,
					}, true
				}
				if uf.CollectTuple != nil && !forUnrollHasUnwind(uf) {
					elems := make([]ast.Expr, 0, len(uf.Iters))
					for _, it := range uf.Iters {
						elems = append(elems, ex.buildExpandedForIterExpr(it, bm, false))
					}
					return &ast.TupleLit{Elems: elems, Span: expandSynthSpan}, true
				}
				if uf.CollectArray != nil && !forUnrollHasUnwind(uf) {
					elems := make([]ast.Expr, 0, len(uf.Iters))
					for _, it := range uf.Iters {
						elems = append(elems, ex.buildExpandedForIterExpr(it, bm, false))
					}
					return &ast.ArrayLit{
						Count: &ast.IntLit{Val: uint64(uf.CollectArray.Size), Span: expandSynthSpan},
						Elems: elems,
						Span:  expandSynthSpan,
					}, true
				}
				// Try clean vector literal representation when target is a Vec/unknown List and bodies don't unwind early.
				var isVecTarget bool
				if inf := bm.ExprInfos[v]; inf != nil {
					_, isVecTarget = types.CollView(inf.Type).(*types.Vector)
				}
				if isVecTarget && v.Else == nil && !forUnrollHasUnwind(uf) {
					elems := make([]ast.Expr, 0, len(uf.Iters))
					for _, it := range uf.Iters {
						elems = append(elems, ex.buildExpandedForIterExpr(it, bm, false))
					}
					return &ast.VecLit{Elems: elems, Span: expandSynthSpan}, true
				}
				// General value-producing comptime for: show unrolled iteration sequence inside a block.
				stmts := make([]ast.Stmt, 0, len(uf.Iters)+1)
				for _, it := range uf.Iters {
					stmts = append(stmts, &ast.ExprStmt{Expr: ex.buildExpandedForIterExpr(it, bm, false), Span: expandSynthSpan})
				}
				var tail ast.Expr
				if v.Else != nil {
					tail, _ = ex.expandExprWithBM(v.Else, bm, false, false)
				}
				return &ast.BlockExpr{Stmts: stmts, Tail: tail, Span: expandSynthSpan}, true
			}
		}
		cp := *v
		cp.CondOrIter, _ = ex.expandExprWithBM(v.CondOrIter, bm, false, false)
		cp.RangeEnd, _ = ex.expandExprWithBM(v.RangeEnd, bm, false, false)
		cp.EvalType = ast.CloneTypeExpr(v.EvalType)
		cp.Body = ex.expandBodyWithBM(v.Body, bm, isVoid)
		if v.Else != nil {
			cp.Else = ex.expandBodyWithBM(v.Else, bm, isVoid)
		}
		return &cp, true

	case *ast.WhileExpr:
		cp := *v
		cp.CondOrIter, _ = ex.expandExprWithBM(v.CondOrIter, bm, false, false)
		cp.RangeEnd, _ = ex.expandExprWithBM(v.RangeEnd, bm, false, false)
		cp.EvalType = ast.CloneTypeExpr(v.EvalType)
		cp.Body = ex.expandBodyWithBM(v.Body, bm, isVoid)
		if v.Else != nil {
			cp.Else = ex.expandBodyWithBM(v.Else, bm, isVoid)
		}
		return &cp, true

	case *ast.BlockExpr:
		cp := ast.BlockExpr{Span: v.Span}
		for _, st := range v.Stmts {
			if es, isExprStmt := st.(*ast.ExprStmt); isExprStmt {
				exExpr, keep := ex.expandExprWithBM(es.Expr, bm, true, false)
				if !keep {
					continue
				}
				// Flatten unrolled statement blocks inside the parent block for readability when no locals leak.
				cp.Stmts = append(cp.Stmts, &ast.ExprStmt{Expr: exExpr, Span: es.Span})
				continue
			}
			cp.Stmts = append(cp.Stmts, ex.expandStmtWithBM(st, bm))
		}
		if v.Tail != nil {
			tail, keep := ex.expandExprWithBM(v.Tail, bm, isVoid, hasExpectedCtx)
			if keep {
				cp.Tail = tail
			}
		}
		return &cp, true

	case *ast.StmtBodyExpr:
		cp := *v
		cp.Stmt = ex.expandStmtWithBM(v.Stmt, bm)
		return &cp, true

	case *ast.UnsafeExpr:
		cp := *v
		cp.Expr = ex.expandBodyWithBM(v.Expr, bm, isVoid)
		return &cp, true

	case *ast.UnaryExpr:
		cp := *v
		cp.Operand, _ = ex.expandExprWithBM(v.Operand, bm, false, false)
		return &cp, true

	case *ast.BinaryExpr:
		cp := *v
		cp.Left, _ = ex.expandExprWithBM(v.Left, bm, false, false)
		cp.Right, _ = ex.expandExprWithBM(v.Right, bm, false, false)
		return &cp, true

	case *ast.MemberExpr:
		cp := *v
		if inf := bm.ExprInfos[v]; inf != nil && inf.ResolvedBase != nil {
			cp.Base, _ = ex.expandExprWithBM(inf.ResolvedBase, bm, false, false)
			cp.BaseTypeArgs = nil
		} else {
			cp.Base, _ = ex.expandExprWithBM(v.Base, bm, false, false)
			cp.BaseTypeArgs = cloneASTTypeExprs(v.BaseTypeArgs)
		}
		if inf := bm.ExprInfos[v]; inf != nil && inf.ResolvedField != "" {
			cp.Member = inf.ResolvedField
		}
		return &cp, true

	case *ast.CallExpr:
		if inf := bm.ExprInfos[v]; inf != nil {
			if inf.BuiltinName == "struct.field" && inf.MethodRecv != nil {
				base, _ := ex.expandExprWithBM(inf.MethodRecv, bm, false, false)
				return &ast.MemberExpr{Base: base, Member: inf.ResolvedField, QuestDot: inf.IsOptChain, Span: v.Span}, true
			}
			if inf.BuiltinName == "seq.elem" && inf.MethodRecv != nil {
				base, _ := ex.expandExprWithBM(inf.MethodRecv, bm, false, false)
				recvTy := types.Type(nil)
				if rinf := bm.ExprInfos[inf.MethodRecv]; rinf != nil {
					recvTy = rinf.Type
				}
				if rf, isRef := recvTy.(*types.Ref); isRef {
					recvTy = rf.Elem
				}
				if _, isArr := recvTy.(*types.StackArray); isArr && !inf.IsOptChain {
					return &ast.IndexExpr{Base: base, Index: &ast.IntLit{Val: uint64(inf.TupleFieldIdx), Span: v.Span}, Span: v.Span}, true
				}
				return &ast.MemberExpr{Base: base, Member: strconv.Itoa(inf.TupleFieldIdx), QuestDot: inf.IsOptChain, Span: v.Span}, true
			}
			if (inf.BuiltinName == "enum.is_variant" || inf.BuiltinName == "enum.payload" || inf.BuiltinName == "enum.as_variant") && inf.MethodRecv != nil && inf.EnumVariant != nil {
				base, _ := ex.expandExprWithBM(inf.MethodRecv, bm, false, false)
				memName := strings.TrimPrefix(inf.BuiltinName, "enum.")
				return &ast.CallExpr{
					Callee: &ast.MemberExpr{Base: base, Member: memName, QuestDot: inf.IsOptChain, Span: v.Span},
					Args:   []ast.Expr{&ast.StringLit{Val: inf.EnumVariant.Name, Span: v.Span}},
					Span:   v.Span,
				}, true
			}
			if inf.BuiltinName == "make_variant" && inf.EnumType != nil && inf.EnumVariant != nil {
				args := []ast.Expr{&ast.StringLit{Val: inf.EnumVariant.Name, Span: v.Span}}
				if len(v.Args) >= 2 {
					a1, _ := ex.expandExprWithBM(v.Args[1], bm, false, true)
					args = append(args, a1)
				}
				return &ast.CallExpr{
					Callee:   &ast.IdentExpr{Name: "make_variant", Span: v.Span},
					TypeArgs: cloneASTTypeExprs(v.TypeArgs),
					Args:     args,
					Span:     v.Span,
				}, true
			}
			if inf.BuiltinName == "distinct.underlying" && inf.MethodRecv != nil {
				base, _ := ex.expandExprWithBM(inf.MethodRecv, bm, false, false)
				return &ast.CallExpr{
					Callee: &ast.MemberExpr{Base: base, Member: "underlying", QuestDot: inf.IsOptChain, Span: v.Span},
					Span:   v.Span,
				}, true
			}
		}
		cp := *v
		if inf := bm.ExprInfos[v]; inf != nil && inf.ResolvedCallee != nil {
			cp.Callee, _ = ex.expandExprWithBM(inf.ResolvedCallee, bm, false, false)
			cp.TypeArgs = nil
		} else {
			cp.Callee, _ = ex.expandExprWithBM(v.Callee, bm, false, false)
			cp.TypeArgs = cloneASTTypeExprs(v.TypeArgs)
		}
		cp.Args = make([]ast.Expr, len(v.Args))
		for i, a := range v.Args {
			cp.Args[i], _ = ex.expandExprWithBM(a, bm, false, true)
		}
		if ex.inDerivedExpand && len(v.TypeArgs) == 0 {
			if inf := bm.ExprInfos[v]; inf != nil && inf.TargetFunc != nil {
				if inlined, ok := ex.tryInlineCrossPkgHelper(inf.TargetFunc, cp.Args); ok {
					return inlined, true
				}
			}
		}
		return &cp, true

	case *ast.IndexExpr:
		cp := *v
		cp.Base, _ = ex.expandExprWithBM(v.Base, bm, false, false)
		cp.Index, _ = ex.expandExprWithBM(v.Index, bm, false, false)
		return &cp, true

	case *ast.SliceExpr:
		cp := *v
		cp.Base, _ = ex.expandExprWithBM(v.Base, bm, false, false)
		cp.Low, _ = ex.expandExprWithBM(v.Low, bm, false, false)
		cp.High, _ = ex.expandExprWithBM(v.High, bm, false, false)
		return &cp, true

	case *ast.RangeExpr:
		cp := *v
		cp.Low, _ = ex.expandExprWithBM(v.Low, bm, false, false)
		cp.High, _ = ex.expandExprWithBM(v.High, bm, false, false)
		return &cp, true

	case *ast.VecLit:
		cp := *v
		cp.Elems = make([]ast.Expr, len(v.Elems))
		for i, el := range v.Elems {
			cp.Elems[i], _ = ex.expandExprWithBM(el, bm, false, hasExpectedCtx && i > 0)
		}
		return &cp, true

	case *ast.ArrayLit:
		cp := *v
		cp.Count, _ = ex.expandExprWithBM(v.Count, bm, false, false)
		cp.Elems = make([]ast.Expr, len(v.Elems))
		for i, el := range v.Elems {
			cp.Elems[i], _ = ex.expandExprWithBM(el, bm, false, hasExpectedCtx && i > 0)
		}
		return &cp, true

	case *ast.SetLit:
		cp := *v
		cp.Elems = make([]ast.Expr, len(v.Elems))
		for i, el := range v.Elems {
			cp.Elems[i], _ = ex.expandExprWithBM(el, bm, false, false)
		}
		return &cp, true

	case *ast.MapLit:
		cp := *v
		cp.Entries = make([]ast.MapEntry, len(v.Entries))
		for i, ent := range v.Entries {
			k, _ := ex.expandExprWithBM(ent.Key, bm, false, false)
			val, _ := ex.expandExprWithBM(ent.Value, bm, false, false)
			cp.Entries[i] = ast.MapEntry{Key: k, Value: val, Span: ent.Span}
		}
		return &cp, true

	case *ast.TupleLit:
		cp := *v
		cp.Elems = make([]ast.Expr, len(v.Elems))
		for i, el := range v.Elems {
			cp.Elems[i], _ = ex.expandExprWithBM(el, bm, false, hasExpectedCtx)
		}
		return &cp, true

	case *ast.StructLit:
		cp := *v
		cp.TypeArgs = cloneASTTypeExprs(v.TypeArgs)
		cp.Fields = make([]ast.StructFieldInit, len(v.Fields))
		for i, f := range v.Fields {
			fv, _ := ex.expandExprWithBM(f.Value, bm, false, true)
			cp.Fields[i] = ast.StructFieldInit{Name: f.Name, IsOpt: f.IsOpt, Value: fv, Span: f.Span}
		}
		return &cp, true

	case *ast.InterpStringExpr:
		cp := *v
		cp.Segments = append([]string(nil), v.Segments...)
		cp.Exprs = make([]ast.Expr, len(v.Exprs))
		for i, exprItem := range v.Exprs {
			cp.Exprs[i], _ = ex.expandExprWithBM(exprItem, bm, false, false)
		}
		return &cp, true

	case *ast.FuncLit:
		cp := *v
		cp.Params = make([]ast.Param, len(v.Params))
		for i, p := range v.Params {
			p.Type = ast.CloneTypeExpr(p.Type)
			cp.Params[i] = p
		}
		cp.Ret = ast.CloneTypeExpr(v.Ret)
		cp.Body = ex.expandBodyWithBM(v.Body, bm, v.Ret == nil)
		return &cp, true

	default:
		return ast.CloneExpr(e), true
	}
}

func (ex *astExpander) expandStmtWithBM(st ast.Stmt, bm *BodyMaps) ast.Stmt {
	if st == nil {
		return nil
	}
	switch s := st.(type) {
	case *ast.VarStmt:
		cp := *s
		cp.Type = ast.CloneTypeExpr(s.Type)
		if s.IsConst {
			if sym := bm.VarSymbols[s]; sym != nil {
				if folded := ex.evalSymToASTExpr(sym, cp.Type != nil); folded != nil {
					cp.Value = folded
					if cp.Type == nil && ex.needsExplicitDeclType(sym.Type, cp.Value) {
						cp.Type = ex.typeToASTTypeExpr(sym.Type)
						if reFolded := ex.evalSymToASTExpr(sym, true); reFolded != nil {
							cp.Value = reFolded
						}
					}
					return &cp
				}
			}
		}
		cp.Value, _ = ex.expandExprWithBM(s.Value, bm, false, cp.Type != nil)
		if s.FallbackUnwind != nil {
			fu := *s.FallbackUnwind
			fu.Value, _ = ex.expandExprWithBM(s.FallbackUnwind.Value, bm, false, false)
			cp.FallbackUnwind = &fu
		}
		return &cp

	case *ast.AssignStmt:
		cp := *s
		cp.Target, _ = ex.expandExprWithBM(s.Target, bm, false, false)
		cp.UnpackTargets = make([]ast.Expr, len(s.UnpackTargets))
		for i, t := range s.UnpackTargets {
			cp.UnpackTargets[i], _ = ex.expandExprWithBM(t, bm, false, false)
		}
		cp.Value, _ = ex.expandExprWithBM(s.Value, bm, false, true)
		if s.FallbackUnwind != nil {
			fu := *s.FallbackUnwind
			fu.Value, _ = ex.expandExprWithBM(s.FallbackUnwind.Value, bm, false, false)
			cp.FallbackUnwind = &fu
		}
		return &cp

	case *ast.UnwindStmt:
		cp := *s
		cp.Value, _ = ex.expandExprWithBM(s.Value, bm, false, true)
		return &cp

	case *ast.ExprStmt:
		exprOut, keep := ex.expandExprWithBM(s.Expr, bm, true, false)
		if !keep {
			return &ast.ExprStmt{Expr: &ast.BlockExpr{Span: expandSynthSpan}, Span: s.Span}
		}
		return &ast.ExprStmt{Expr: exprOut, Span: s.Span}

	default:
		return ast.CloneStmt(st)
	}
}

func cloneASTTypeExprs(tes []ast.TypeExpr) []ast.TypeExpr {
	if tes == nil {
		return nil
	}
	out := make([]ast.TypeExpr, len(tes))
	for i, te := range tes {
		out[i] = ast.CloneTypeExpr(te)
	}
	return out
}

func (ex *astExpander) buildExpandedForIterExpr(it UnrolledIter, bm *BodyMaps, isVoid bool) ast.Expr {
	var prefix []ast.Stmt
	if it.IdxSym != nil && it.IdxVal != nil {
		if exprVal := ex.staticDataToASTExpr(ExportStaticData(it.IdxVal, it.IdxSym.Type), false); exprVal != nil {
			var typeAnn ast.TypeExpr
			if ex.needsExplicitDeclType(it.IdxSym.Type, exprVal) {
				typeAnn = ex.typeToASTTypeExpr(it.IdxSym.Type)
				if re := ex.staticDataToASTExpr(ExportStaticData(it.IdxVal, it.IdxSym.Type), true); re != nil {
					exprVal = re
				}
			}
			prefix = append(prefix, &ast.VarStmt{
				IsBind:  true,
				IsConst: true,
				Name:    it.IdxSym.Name,
				Type:    typeAnn,
				Value:   exprVal,
				Span:    expandSynthSpan,
			})
		}
	}
	if it.ItemSym != nil && it.ItemVal != nil {
		if exprVal := ex.staticDataToASTExpr(ExportStaticData(it.ItemVal, it.ItemSym.Type), false); exprVal != nil {
			var typeAnn ast.TypeExpr
			if ex.needsExplicitDeclType(it.ItemSym.Type, exprVal) {
				typeAnn = ex.typeToASTTypeExpr(it.ItemSym.Type)
				if re := ex.staticDataToASTExpr(ExportStaticData(it.ItemVal, it.ItemSym.Type), true); re != nil {
					exprVal = re
				}
			}
			prefix = append(prefix, &ast.VarStmt{
				IsBind:  true,
				IsConst: true,
				Name:    it.ItemSym.Name,
				Type:    typeAnn,
				Value:   exprVal,
				Span:    expandSynthSpan,
			})
		}
	}
	bodyExpr, keep := ex.expandExprWithBM(it.Body, bm, isVoid, false)
	if !keep || bodyExpr == nil {
		bodyExpr = &ast.BlockExpr{Span: expandSynthSpan}
	}
	if len(prefix) == 0 {
		return bodyExpr
	}
	return wrapPrefixStmts(prefix, bodyExpr)
}

func wrapPrefixStmts(prefix []ast.Stmt, body ast.Expr) ast.Expr {
	if blk, ok := body.(*ast.BlockExpr); ok {
		nb := *blk
		nb.Stmts = append(prefix, blk.Stmts...)
		return &nb
	}
	return &ast.BlockExpr{
		Stmts: prefix,
		Tail:  body,
		Span:  expandSynthSpan,
	}
}

func (ex *astExpander) extractMatchArmConstStmts(arm ast.MatchArm, bm *BodyMaps) []ast.Stmt {
	if bm == nil || len(arm.Cases) != 1 {
		return nil
	}
	var stmts []ast.Stmt
	for _, pat := range arm.Cases[0].Patterns {
		var psyms []*Symbol
		switch p := pat.(type) {
		case *ast.VariantPattern:
			psyms = bm.PatternSyms[p]
		case *ast.MethodPattern:
			if vp := bm.BareVariantPatterns[p]; vp != nil {
				psyms = bm.PatternSyms[vp]
			}
		}
		for _, sym := range psyms {
			if sym == nil || sym.ConstVal == nil {
				continue
			}
			if exprVal := ex.staticDataToASTExpr(ExportStaticData(sym.ConstVal, sym.Type), false); exprVal != nil {
				var typeAnn ast.TypeExpr
				if ex.needsExplicitDeclType(sym.Type, exprVal) {
					typeAnn = ex.typeToASTTypeExpr(sym.Type)
					if re := ex.staticDataToASTExpr(ExportStaticData(sym.ConstVal, sym.Type), true); re != nil {
						exprVal = re
					}
				}
				stmts = append(stmts, &ast.VarStmt{
					IsBind:  true,
					IsConst: true,
					Name:    sym.Name,
					Type:    typeAnn,
					Value:   exprVal,
					Span:    expandSynthSpan,
				})
			}
		}
	}
	return stmts
}

func forUnrollHasUnwind(uf *UnrolledFor) bool {
	for _, it := range uf.Iters {
		if exprContainsLoopUnwind(it.Body) {
			return true
		}
	}
	return false
}

func stmtContainsLoopUnwind(st ast.Stmt) bool {
	if st == nil {
		return false
	}
	switch s := st.(type) {
	case *ast.UnwindStmt:
		if s.Target == ast.UnwindNext || s.Target == ast.UnwindNextLabel || s.Target == ast.UnwindFor || s.Target == ast.UnwindLabel {
			return true
		}
		return exprContainsLoopUnwind(s.Value)
	case *ast.VarStmt:
		if s.FallbackUnwind != nil && (s.FallbackUnwind.Target == ast.UnwindNext || s.FallbackUnwind.Target == ast.UnwindNextLabel || s.FallbackUnwind.Target == ast.UnwindFor || s.FallbackUnwind.Target == ast.UnwindLabel) {
			return true
		}
		if s.FallbackUnwind != nil && exprContainsLoopUnwind(s.FallbackUnwind.Value) {
			return true
		}
		return exprContainsLoopUnwind(s.Value)
	case *ast.AssignStmt:
		if s.FallbackUnwind != nil && (s.FallbackUnwind.Target == ast.UnwindNext || s.FallbackUnwind.Target == ast.UnwindNextLabel || s.FallbackUnwind.Target == ast.UnwindFor || s.FallbackUnwind.Target == ast.UnwindLabel) {
			return true
		}
		if s.FallbackUnwind != nil && exprContainsLoopUnwind(s.FallbackUnwind.Value) {
			return true
		}
		for _, t := range s.UnpackTargets {
			if exprContainsLoopUnwind(t) {
				return true
			}
		}
		return exprContainsLoopUnwind(s.Target) || exprContainsLoopUnwind(s.Value)
	case *ast.ExprStmt:
		return exprContainsLoopUnwind(s.Expr)
	}
	return false
}

func exprContainsLoopUnwind(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *ast.BlockExpr:
		for _, st := range v.Stmts {
			if stmtContainsLoopUnwind(st) {
				return true
			}
		}
		return exprContainsLoopUnwind(v.Tail)
	case *ast.StmtBodyExpr:
		return stmtContainsLoopUnwind(v.Stmt)
	case *ast.IfExpr:
		return exprContainsLoopUnwind(v.Cond) || exprContainsLoopUnwind(v.Then) || exprContainsLoopUnwind(v.Else)
	case *ast.ForExpr:
		return exprContainsLoopUnwind(v.CondOrIter) || exprContainsLoopUnwind(v.RangeEnd) || exprContainsLoopUnwind(v.Else)
	case *ast.WhileExpr:
		return exprContainsLoopUnwind(v.CondOrIter) || exprContainsLoopUnwind(v.RangeEnd) || exprContainsLoopUnwind(v.Else)
	case *ast.MatchExpr:
		for _, sc := range v.Scrutinees {
			if exprContainsLoopUnwind(sc) {
				return true
			}
		}
		for _, arm := range v.Arms {
			if exprContainsLoopUnwind(arm.Body) {
				return true
			}
		}
	case *ast.UnaryExpr:
		return exprContainsLoopUnwind(v.Operand)
	case *ast.BinaryExpr:
		return exprContainsLoopUnwind(v.Left) || exprContainsLoopUnwind(v.Right)
	case *ast.CallExpr:
		if exprContainsLoopUnwind(v.Callee) {
			return true
		}
		for _, a := range v.Args {
			if exprContainsLoopUnwind(a) {
				return true
			}
		}
	case *ast.MemberExpr:
		return exprContainsLoopUnwind(v.Base)
	case *ast.IndexExpr:
		return exprContainsLoopUnwind(v.Base) || exprContainsLoopUnwind(v.Index)
	case *ast.SliceExpr:
		return exprContainsLoopUnwind(v.Base) || exprContainsLoopUnwind(v.Low) || exprContainsLoopUnwind(v.High)
	case *ast.RangeExpr:
		return exprContainsLoopUnwind(v.Low) || exprContainsLoopUnwind(v.High)
	case *ast.TupleLit:
		for _, el := range v.Elems {
			if exprContainsLoopUnwind(el) {
				return true
			}
		}
	case *ast.ArrayLit:
		for _, el := range v.Elems {
			if exprContainsLoopUnwind(el) {
				return true
			}
		}
	case *ast.VecLit:
		for _, el := range v.Elems {
			if exprContainsLoopUnwind(el) {
				return true
			}
		}
	case *ast.MapLit:
		for _, ent := range v.Entries {
			if exprContainsLoopUnwind(ent.Key) || exprContainsLoopUnwind(ent.Value) {
				return true
			}
		}
	case *ast.StructLit:
		for _, f := range v.Fields {
			if exprContainsLoopUnwind(f.Value) {
				return true
			}
		}
	case *ast.UnsafeExpr:
		return exprContainsLoopUnwind(v.Expr)
	case *ast.ComptimeExpr:
		return exprContainsLoopUnwind(v.Expr)
	}
	return false
}

func constValueToASTExpr(cv *types.ConstValue) ast.Expr {
	if cv == nil {
		return nil
	}
	switch cv.Kind {
	case types.ConstInt:
		if p := types.UnderlyingPrimitive(cv.ConstType); p != nil && p.IsSignedInt() && cv.IntVal < 0 {
			mag := uint64(-cv.IntVal)
			if cv.IntVal == math.MinInt64 {
				mag = uint64(1) << 63
			}
			return &ast.UnaryExpr{
				Op:      token.Minus,
				Operand: &ast.IntLit{Val: mag, Raw: strconv.FormatUint(mag, 10), Span: expandSynthSpan},
				Span:    expandSynthSpan,
			}
		}
		return &ast.IntLit{Val: cv.UintVal, Raw: strconv.FormatUint(cv.UintVal, 10), Span: expandSynthSpan}
	case types.ConstBool:
		return &ast.BoolLit{Val: cv.BoolVal, Span: expandSynthSpan}
	case types.ConstChar:
		return &ast.CharLit{Val: cv.RuneVal, Span: expandSynthSpan}
	case types.ConstString:
		return &ast.StringLit{Val: cv.StrVal, Span: expandSynthSpan}
	case types.ConstEnum:
		if cv.EnumTy != nil && cv.EnumName != "" {
			return &ast.MemberExpr{
				Base:   &ast.IdentExpr{Name: cv.EnumTy.Name, Span: expandSynthSpan},
				Member: cv.EnumName,
				Span:   expandSynthSpan,
			}
		}
	}
	return nil
}

func (ex *astExpander) moduleAliasFor(targetMod *Module) string {
	if ex == nil || ex.mod == nil || targetMod == nil || targetMod == ex.mod {
		return ""
	}
	if ex.mod.Pkg != nil && targetMod.Pkg == ex.mod.Pkg {
		return ""
	}
	if targetMod.Pkg != nil && targetMod.Pkg.IsCore {
		return ""
	}
	aliases := make([]string, 0, len(ex.mod.Aliased))
	for name := range ex.mod.Aliased {
		aliases = append(aliases, name)
	}
	sort.Strings(aliases)
	for _, name := range aliases {
		m := ex.mod.Aliased[name]
		if m == targetMod || (m != nil && m.Pkg != nil && targetMod.Pkg != nil && m.Pkg == targetMod.Pkg) {
			return name
		}
	}
	return ""
}

func (ex *astExpander) nominalModAlias(t types.Type) string {
	if ex == nil || ex.prog == nil {
		return ""
	}
	switch v := t.(type) {
	case *types.Struct:
		return ex.moduleAliasFor(ex.prog.StructMods[v])
	case *types.Enum:
		return ex.moduleAliasFor(ex.prog.EnumMods[v])
	case *types.Distinct:
		return ex.moduleAliasFor(ex.prog.DistinctMods[v])
	case *types.Interface:
		return ex.moduleAliasFor(ex.prog.InterfaceMods[v])
	}
	return ""
}

func stripGenericSuffix(name string) string {
	if idx := strings.IndexByte(name, '['); idx >= 0 {
		return name[:idx]
	}
	return name
}

func (ex *astExpander) typeToASTTypeExpr(t types.Type) ast.TypeExpr {
	if t == nil || t == types.TypeInvalid {
		return nil
	}
	switch v := t.(type) {
	case *types.Primitive:
		return &ast.NamedType{Name: v.String(), Span: expandSynthSpan}
	case *types.Optional:
		elem := ex.typeToASTTypeExpr(v.Elem)
		if elem == nil {
			return nil
		}
		return &ast.OptionalType{Elem: elem, Span: expandSynthSpan}
	case *types.Vector:
		elem := ex.typeToASTTypeExpr(v.Elem)
		if elem == nil {
			return nil
		}
		return &ast.VectorType{Elem: elem, Span: expandSynthSpan}
	case *types.StackArray:
		elem := ex.typeToASTTypeExpr(v.Elem)
		if elem == nil {
			return nil
		}
		return &ast.StackArrayType{
			Size: &ast.IntLit{Val: uint64(v.Size), Raw: strconv.FormatInt(v.Size, 10), Span: expandSynthSpan},
			Elem: elem,
			Span: expandSynthSpan,
		}
	case *types.Set:
		elem := ex.typeToASTTypeExpr(v.Elem)
		if elem == nil {
			return nil
		}
		return &ast.SetType{Elem: elem, Span: expandSynthSpan}
	case *types.Map:
		k := ex.typeToASTTypeExpr(v.Key)
		val := ex.typeToASTTypeExpr(v.Value)
		if k == nil || val == nil {
			return nil
		}
		return &ast.MapType{Key: k, Value: val, Span: expandSynthSpan}
	case *types.Tuple:
		elems := make([]ast.TypeExpr, 0, len(v.Elems))
		for _, el := range v.Elems {
			te := ex.typeToASTTypeExpr(el)
			if te == nil {
				return nil
			}
			elems = append(elems, te)
		}
		return &ast.TupleType{Elems: elems, Span: expandSynthSpan}
	case *types.Ptr:
		elem := ex.typeToASTTypeExpr(v.Elem)
		if elem == nil {
			return nil
		}
		return &ast.NamedType{Name: "ptr", TypeArgs: []ast.TypeExpr{elem}, Span: expandSynthSpan}
	case *types.Ref:
		elem := ex.typeToASTTypeExpr(v.Elem)
		if elem == nil {
			return nil
		}
		return &ast.NamedType{Name: "ref", TypeArgs: []ast.TypeExpr{elem}, Span: expandSynthSpan}
	case *types.ConstValue:
		ce := constValueToASTExpr(v)
		if ce == nil {
			return nil
		}
		return &ast.ConstArgType{Expr: ce, Span: expandSynthSpan}
	case *types.TypeParamVar:
		return &ast.NamedType{Name: v.Name, Span: expandSynthSpan}
	case *types.Distinct:
		return &ast.NamedType{Module: ex.nominalModAlias(v), Name: v.Name, Span: expandSynthSpan}
	case *types.Struct:
		if v.Lang == "list" && len(v.TypeArgs) == 1 {
			if elem := ex.typeToASTTypeExpr(v.TypeArgs[0]); elem != nil {
				return &ast.VectorType{Elem: elem, Span: expandSynthSpan}
			}
		}
		targs := make([]ast.TypeExpr, 0, len(v.TypeArgs))
		for _, ta := range v.TypeArgs {
			te := ex.typeToASTTypeExpr(ta)
			if te == nil {
				return nil
			}
			targs = append(targs, te)
		}
		return &ast.NamedType{Module: ex.nominalModAlias(v), Name: stripGenericSuffix(v.Name), TypeArgs: targs, Span: expandSynthSpan}
	case *types.Enum:
		targs := make([]ast.TypeExpr, 0, len(v.TypeArgs))
		for _, ta := range v.TypeArgs {
			te := ex.typeToASTTypeExpr(ta)
			if te == nil {
				return nil
			}
			targs = append(targs, te)
		}
		return &ast.NamedType{Module: ex.nominalModAlias(v), Name: stripGenericSuffix(v.Name), TypeArgs: targs, Span: expandSynthSpan}
	case *types.Interface:
		return &ast.NamedType{Module: ex.nominalModAlias(v), Name: stripGenericSuffix(v.Name), Span: expandSynthSpan}
	case *types.Func:
		params := make([]ast.Param, 0, len(v.Params))
		for _, p := range v.Params {
			pt := ex.typeToASTTypeExpr(p.Type)
			if pt == nil {
				return nil
			}
			switch p.Mode {
			case types.ModeOwn:
				pt = &ast.OwnType{Elem: pt, Span: expandSynthSpan}
			case types.ModeMut:
				pt = &ast.MutBorrowType{Elem: pt, Span: expandSynthSpan}
			}
			params = append(params, ast.Param{Type: pt, Span: expandSynthSpan})
		}
		var retAST ast.TypeExpr
		if v.Ret != nil && v.Ret != types.TypeVoid {
			retAST = ex.typeToASTTypeExpr(v.Ret)
			if retAST == nil {
				return nil
			}
		}
		return &ast.FuncType{Params: params, Ret: retAST, Closure: v.Closure, Span: expandSynthSpan}
	}
	return nil
}

func (ex *astExpander) needsExplicitDeclType(ty types.Type, valAST ast.Expr) bool {
	if ty == nil || ty == types.TypeInvalid || ty == types.TypeVoid {
		return false
	}
	if _, isOpt := ty.(*types.Optional); isOpt {
		return true
	}
	switch ty.(type) {
	case *types.StackArray:
		// An empty or repeated `[N: ...]` could need explicit type if element count was folded or non-default.
		return true
	}
	if vl, isEmptyVec := valAST.(*ast.VecLit); isEmptyVec && len(vl.Elems) == 0 {
		if _, isVec := ty.(*types.Vector); isVec {
			return true
		}
		if st, isSt := ty.(*types.Struct); isSt && st.Lang == "list" {
			return true
		}
	}
	return false
}

func (ex *astExpander) wrapValueTypeConversion(ty types.Type, inner ast.Expr) ast.Expr {
	if ty == nil || inner == nil {
		return inner
	}
	if dist, isDist := ty.(*types.Distinct); isDist {
		return &ast.CallExpr{
			Callee: ex.nominalTypeToValueExpr(dist, nil),
			Args:   []ast.Expr{inner},
			Span:   expandSynthSpan,
		}
	}
	if p := types.UnderlyingPrimitive(ty); p != nil {
		if p == types.TypeF32 || (p.IsInteger() && p != types.TypeInt) {
			return &ast.CallExpr{
				Callee: &ast.IdentExpr{Name: p.String(), Span: expandSynthSpan},
				Args:   []ast.Expr{inner},
				Span:   expandSynthSpan,
			}
		}
	}
	return inner
}

func (ex *astExpander) nominalTypeToValueExpr(ty types.Type, typeArgs []types.Type) ast.Expr {
	alias := ex.nominalModAlias(ty)
	var rawName string
	switch v := ty.(type) {
	case *types.Distinct:
		rawName = v.Name
	case *types.Enum:
		rawName = stripGenericSuffix(v.Name)
	case *types.Struct:
		rawName = stripGenericSuffix(v.Name)
	}
	var root ast.Expr = &ast.IdentExpr{Name: rawName, Span: expandSynthSpan}
	if alias != "" {
		root = &ast.MemberExpr{
			Base:   &ast.IdentExpr{Name: alias, Span: expandSynthSpan},
			Member: rawName,
			Span:   expandSynthSpan,
		}
	}
	_ = typeArgs
	return root
}

func (ex *astExpander) staticDataToASTExpr(sd *StaticData, hasExpectedCtx bool) ast.Expr {
	if sd == nil {
		return nil
	}
	switch sd.Kind {
	case StaticVoid:
		return &ast.BlockExpr{Span: expandSynthSpan}
	case StaticBool:
		var lit ast.Expr = &ast.BoolLit{Val: sd.Bool, Span: expandSynthSpan}
		if _, isDist := sd.Type.(*types.Distinct); isDist {
			lit = ex.wrapValueTypeConversion(sd.Type, lit)
		}
		return lit
	case StaticChar:
		var lit ast.Expr = &ast.CharLit{Val: sd.Char, Span: expandSynthSpan}
		if _, isDist := sd.Type.(*types.Distinct); isDist {
			lit = ex.wrapValueTypeConversion(sd.Type, lit)
		}
		return lit
	case StaticString:
		var lit ast.Expr = &ast.StringLit{Val: sd.Str, Span: expandSynthSpan}
		if _, isDist := sd.Type.(*types.Distinct); isDist {
			lit = ex.wrapValueTypeConversion(sd.Type, lit)
		}
		return lit
	case StaticInt:
		var lit ast.Expr
		if sd.IsSigned && sd.SignedVal < 0 {
			mag := uint64(-sd.SignedVal)
			if sd.SignedVal == math.MinInt64 {
				mag = uint64(1) << 63
			}
			lit = &ast.UnaryExpr{
				Op:      token.Minus,
				Operand: &ast.IntLit{Val: mag, Raw: strconv.FormatUint(mag, 10), Span: expandSynthSpan},
				Span:    expandSynthSpan,
			}
		} else {
			lit = &ast.IntLit{Val: sd.Uint, Raw: strconv.FormatUint(sd.Uint, 10), Span: expandSynthSpan}
		}
		return ex.wrapValueTypeConversion(sd.Type, lit)
	case StaticFloat:
		var lit ast.Expr
		if math.IsNaN(sd.Float) {
			lit = &ast.BinaryExpr{
				Op:    token.Slash,
				Left:  &ast.FloatLit{Val: 0, Raw: "0.0", Span: expandSynthSpan},
				Right: &ast.FloatLit{Val: 0, Raw: "0.0", Span: expandSynthSpan},
				Span:  expandSynthSpan,
			}
		} else if math.IsInf(sd.Float, 1) {
			lit = &ast.BinaryExpr{
				Op:    token.Slash,
				Left:  &ast.FloatLit{Val: 1, Raw: "1.0", Span: expandSynthSpan},
				Right: &ast.FloatLit{Val: 0, Raw: "0.0", Span: expandSynthSpan},
				Span:  expandSynthSpan,
			}
		} else if math.IsInf(sd.Float, -1) {
			lit = &ast.BinaryExpr{
				Op: token.Slash,
				Left: &ast.UnaryExpr{
					Op:      token.Minus,
					Operand: &ast.FloatLit{Val: 1, Raw: "1.0", Span: expandSynthSpan},
					Span:    expandSynthSpan,
				},
				Right: &ast.FloatLit{Val: 0, Raw: "0.0", Span: expandSynthSpan},
				Span:  expandSynthSpan,
			}
		} else {
			neg := math.Signbit(sd.Float)
			mag := math.Abs(sd.Float)
			raw := strconv.FormatFloat(mag, 'g', -1, 64)
			if !strings.ContainsAny(raw, ".eE") {
				raw += ".0"
			}
			fLit := &ast.FloatLit{Val: mag, Raw: raw, Span: expandSynthSpan}
			if neg {
				lit = &ast.UnaryExpr{Op: token.Minus, Operand: fLit, Span: expandSynthSpan}
			} else {
				lit = fLit
			}
		}
		return ex.wrapValueTypeConversion(sd.Type, lit)
	case StaticOpt:
		var body ast.Expr
		if !sd.HasOpt {
			if hasExpectedCtx {
				return &ast.NoneLit{Span: expandSynthSpan}
			}
			body = &ast.NoneLit{Span: expandSynthSpan}
		} else {
			body = ex.staticDataToASTExpr(sd.OptElem, hasExpectedCtx)
			if hasExpectedCtx || body == nil {
				return body
			}
		}
		if optTyAST := ex.typeToASTTypeExpr(sd.Type); optTyAST != nil {
			return &ast.CallExpr{
				Callee: &ast.FuncLit{
					Ret:  optTyAST,
					Body: body,
					Span: expandSynthSpan,
				},
				Span: expandSynthSpan,
			}
		}
		return body
	case StaticList:
		var elemTy types.Type
		effTy := sd.Type
		for {
			if d, isDist := effTy.(*types.Distinct); isDist {
				effTy = d.Base
			} else {
				break
			}
		}
		switch ct := effTy.(type) {
		case *types.Vector:
			elemTy = ct.Elem
		case *types.Struct:
			if len(ct.TypeArgs) == 1 {
				elemTy = ct.TypeArgs[0]
			}
		}
		if sd.HasRawBytes && elemTy == nil {
			elemTy = types.TypeU8
		}
		isEmpty := (!sd.HasRawBytes && len(sd.Elems) == 0) || (sd.HasRawBytes && len(sd.RawBytes) == 0)
		if isEmpty {
			var listAST ast.Expr = &ast.VecLit{Span: expandSynthSpan}
			if _, isDistinct := sd.Type.(*types.Distinct); !hasExpectedCtx || isDistinct {
				if elemAST := ex.typeToASTTypeExpr(elemTy); elemAST != nil {
					listAST = &ast.CallExpr{
						Callee: &ast.MemberExpr{
							Base:         &ast.IdentExpr{Name: "List", Span: expandSynthSpan},
							BaseTypeArgs: []ast.TypeExpr{elemAST},
							Member:       "new",
							Span:         expandSynthSpan,
						},
						Span: expandSynthSpan,
					}
				}
			}
			return ex.wrapValueTypeConversion(sd.Type, listAST)
		}
		if sd.HasRawBytes {
			elems := make([]ast.Expr, len(sd.RawBytes))
			for i, b := range sd.RawBytes {
				lit := ast.Expr(&ast.IntLit{Val: uint64(b), Raw: fmt.Sprintf("%d", b), Span: expandSynthSpan})
				elems[i] = &ast.CallExpr{
					Callee: &ast.IdentExpr{Name: "u8", Span: expandSynthSpan},
					Args:   []ast.Expr{lit},
					Span:   expandSynthSpan,
				}
			}
			return ex.wrapValueTypeConversion(sd.Type, &ast.VecLit{Elems: elems, Span: expandSynthSpan})
		}
		elems := make([]ast.Expr, 0, len(sd.Elems))
		for _, el := range sd.Elems {
			exExpr := ex.staticDataToASTExpr(el, false)
			if exExpr == nil {
				return nil
			}
			elems = append(elems, exExpr)
		}
		return ex.wrapValueTypeConversion(sd.Type, &ast.VecLit{Elems: elems, Span: expandSynthSpan})
	case StaticArray:
		elems := make([]ast.Expr, 0, len(sd.Elems))
		for _, el := range sd.Elems {
			exExpr := ex.staticDataToASTExpr(el, false)
			if exExpr == nil {
				return nil
			}
			elems = append(elems, exExpr)
		}
		return ex.wrapValueTypeConversion(sd.Type, &ast.ArrayLit{
			Count: &ast.IntLit{Val: uint64(len(elems)), Raw: strconv.Itoa(len(elems)), Span: expandSynthSpan},
			Elems: elems,
			Span:  expandSynthSpan,
		})
	case StaticTuple:
		elems := make([]ast.Expr, 0, len(sd.Elems))
		for _, el := range sd.Elems {
			exExpr := ex.staticDataToASTExpr(el, hasExpectedCtx)
			if exExpr == nil {
				return nil
			}
			elems = append(elems, exExpr)
		}
		return ex.wrapValueTypeConversion(sd.Type, &ast.TupleLit{Elems: elems, Span: expandSynthSpan})
	case StaticEnum:
		effTy := sd.Type
		for {
			if d, ok := effTy.(*types.Distinct); ok {
				effTy = d.Base
			} else {
				break
			}
		}
		en, ok := effTy.(*types.Enum)
		if !ok || sd.VariantIdx < 0 || sd.VariantIdx >= len(en.Variants) {
			return nil
		}
		vr := en.Variants[sd.VariantIdx]
		var baseTypeArgs []ast.TypeExpr
		for _, ta := range en.TypeArgs {
			te := ex.typeToASTTypeExpr(ta)
			if te == nil {
				baseTypeArgs = nil
				break
			}
			baseTypeArgs = append(baseTypeArgs, te)
		}
		mem := &ast.MemberExpr{
			Base:         ex.nominalTypeToValueExpr(en, en.TypeArgs),
			BaseTypeArgs: baseTypeArgs,
			Member:       vr.Name,
			Span:         expandSynthSpan,
		}
		if sd.Payload != nil {
			pv := ex.staticDataToASTExpr(sd.Payload, true)
			if pv == nil {
				return nil
			}
			return ex.wrapValueTypeConversion(sd.Type, &ast.CallExpr{Callee: mem, Args: []ast.Expr{pv}, Span: expandSynthSpan})
		}
		return ex.wrapValueTypeConversion(sd.Type, mem)
	case StaticStruct:
		effTy := sd.Type
		for {
			if d, ok := effTy.(*types.Distinct); ok {
				effTy = d.Base
			} else {
				break
			}
		}
		st, ok := effTy.(*types.Struct)
		if !ok {
			return nil
		}
		typeName := stripGenericSuffix(st.Name)
		var typeArgs []ast.TypeExpr
		for _, ta := range st.TypeArgs {
			te := ex.typeToASTTypeExpr(ta)
			if te == nil {
				typeArgs = nil
				break
			}
			typeArgs = append(typeArgs, te)
		}
		fields := make([]ast.StructFieldInit, 0, len(st.Fields))
		for _, f := range st.Fields {
			fExpr := ex.staticDataToASTExpr(sd.Fields[f.Name], true)
			if fExpr == nil {
				return nil
			}
			fields = append(fields, ast.StructFieldInit{Name: f.Name, Value: fExpr, Span: expandSynthSpan})
		}
		return ex.wrapValueTypeConversion(sd.Type, &ast.StructLit{
			Module:   ex.nominalModAlias(st),
			TypeName: typeName,
			TypeArgs: typeArgs,
			Fields:   fields,
			Span:     expandSynthSpan,
		})
	}
	return nil
}

func cloneAndRenameASTIdents(e ast.Expr, rename map[string]string) ast.Expr {
	if e == nil || len(rename) == 0 {
		return e
	}
	subst := make(map[string]ast.Expr, len(rename))
	for from, to := range rename {
		if from == "" || to == "" || from == to {
			continue
		}
		subst[from] = &ast.IdentExpr{Name: to, Span: expandSynthSpan}
	}
	return ast.CloneExprWithSubst(e, subst)
}

func (ex *astExpander) tryInlineCrossPkgHelper(callee *FuncInstance, args []ast.Expr) (ast.Expr, bool) {
	if callee == nil || IsExported(callee.Name) || callee.IsMethod || callee.IsStaticAssoc {
		return nil, false
	}
	if callee.Mod == nil || ex.mod == nil || callee.Mod.Pkg == ex.mod.Pkg {
		return nil, false
	}
	if callee.Body == nil || callee.BodyMaps == nil || len(args) != len(callee.ParamNames) {
		return nil, false
	}
	expandedBody := ex.expandBodyWithBM(callee.Body, callee.BodyMaps, callee.Sig == nil || callee.Sig.Ret == types.TypeVoid)
	if exprContainsFuncUnwind(expandedBody) {
		return nil, false
	}
	subst := make(map[string]ast.Expr, len(callee.ParamNames))
	for i, pName := range callee.ParamNames {
		if pName == "" {
			continue
		}
		switch args[i].(type) {
		case *ast.IdentExpr, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit, *ast.NoneLit:
			subst[pName] = args[i]
		default:
			if countFreeIdentRefs(expandedBody, pName) <= 1 {
				subst[pName] = args[i]
			} else {
				return nil, false
			}
		}
	}
	return ast.CloneExprWithSubst(expandedBody, subst), true
}

func exprContainsFuncUnwind(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *ast.BlockExpr:
		for _, st := range v.Stmts {
			if stmtContainsFuncUnwind(st) {
				return true
			}
		}
		return exprContainsFuncUnwind(v.Tail)
	case *ast.StmtBodyExpr:
		return stmtContainsFuncUnwind(v.Stmt)
	case *ast.IfExpr:
		return exprContainsFuncUnwind(v.Cond) || exprContainsFuncUnwind(v.Then) || exprContainsFuncUnwind(v.Else)
	case *ast.ForExpr:
		return exprContainsFuncUnwind(v.Body) || exprContainsFuncUnwind(v.Else)
	case *ast.WhileExpr:
		return exprContainsFuncUnwind(v.Body) || exprContainsFuncUnwind(v.Else)
	case *ast.MatchExpr:
		for _, arm := range v.Arms {
			if exprContainsFuncUnwind(arm.Body) {
				return true
			}
		}
	case *ast.UnsafeExpr:
		return exprContainsFuncUnwind(v.Expr)
	case *ast.ComptimeExpr:
		return exprContainsFuncUnwind(v.Expr)
	}
	return false
}

func stmtContainsFuncUnwind(st ast.Stmt) bool {
	if st == nil {
		return false
	}
	switch s := st.(type) {
	case *ast.UnwindStmt:
		return s.Target == ast.UnwindFunc
	case *ast.VarStmt:
		return (s.FallbackUnwind != nil && s.FallbackUnwind.Target == ast.UnwindFunc) || exprContainsFuncUnwind(s.Value)
	case *ast.AssignStmt:
		return (s.FallbackUnwind != nil && s.FallbackUnwind.Target == ast.UnwindFunc) || exprContainsFuncUnwind(s.Value)
	case *ast.ExprStmt:
		return exprContainsFuncUnwind(s.Expr)
	}
	return false
}

func countFreeIdentRefs(e ast.Expr, name string) int {
	if e == nil || name == "" {
		return 0
	}
	switch v := e.(type) {
	case *ast.IdentExpr:
		if v.Name == name {
			return 1
		}
	case *ast.UnaryExpr:
		return countFreeIdentRefs(v.Operand, name)
	case *ast.BinaryExpr:
		return countFreeIdentRefs(v.Left, name) + countFreeIdentRefs(v.Right, name)
	case *ast.MemberExpr:
		return countFreeIdentRefs(v.Base, name)
	case *ast.CallExpr:
		n := countFreeIdentRefs(v.Callee, name)
		for _, a := range v.Args {
			n += countFreeIdentRefs(a, name)
		}
		return n
	case *ast.IndexExpr:
		return countFreeIdentRefs(v.Base, name) + countFreeIdentRefs(v.Index, name)
	case *ast.BlockExpr:
		n := 0
		shadowed := false
		for _, st := range v.Stmts {
			switch s := st.(type) {
			case *ast.ExprStmt:
				n += countFreeIdentRefs(s.Expr, name)
			case *ast.VarStmt:
				n += countFreeIdentRefs(s.Value, name)
				if s.Name == name {
					shadowed = true
				}
			}
			if shadowed {
				return n
			}
		}
		if !shadowed {
			n += countFreeIdentRefs(v.Tail, name)
		}
		return n
	case *ast.IfExpr:
		n := countFreeIdentRefs(v.Cond, name) + countFreeIdentRefs(v.Else, name)
		if v.BindName != name {
			n += countFreeIdentRefs(v.Then, name)
		}
		return n
	}
	return 2
}
