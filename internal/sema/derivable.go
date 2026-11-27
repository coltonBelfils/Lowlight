package sema

import (
	"fmt"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// `@derivable(...)` interface hooks and `@derive(...)` wrapper synthesis.
//
// Interfaces declare generic derivation implementations via
// `@derivable(method: impl_fn, ...)`. `@derive(Iface)` synthesizes thin `const`
// wrapper methods on the target struct/enum that delegate to `impl_fn[Self]`
// with scoped H16 `@derive` authorization and automatic conditional `const`
// downgrade when any underlying field or variant operation is runtime `bind`.

type deriveInfo struct {
	Iface    string
	Method   string
	TypeName string
	Span     diag.Span // the `@derive(...)` attribute
	Mod      *Module
	Tmpl     *GenericFuncTemplate
}

type derivableEntry struct {
	Method   string
	Impl     *GenericFuncTemplate
	AttrSpan diag.Span
}

type deriveCallBinding struct {
	ImplTmpl *GenericFuncTemplate
	Iface    *types.Interface
	AttrSpan diag.Span
	TypeName string
}

func (a *Analyzer) ensureDeriveMaps() {
	if a.derived == nil {
		a.derived = make(map[*ast.MethodDecl]*deriveInfo)
	}
	if a.deriveChecking == nil {
		a.deriveChecking = make(map[string]bool)
	}
	if a.derivePassed == nil {
		a.derivePassed = make(map[string]bool)
	}
	if a.deriveFailed == nil {
		a.deriveFailed = make(map[string]bool)
	}
	if a.ifaceDecls == nil {
		a.ifaceDecls = make(map[*types.Interface]*ast.InterfaceDecl)
	}
	if a.derivable == nil {
		a.derivable = make(map[*types.Interface]map[string]*derivableEntry)
	}
	if a.deriveCallBindings == nil {
		a.deriveCallBindings = make(map[*ast.CallExpr]*deriveCallBinding)
	}
	if a.synthTypeExprs == nil {
		a.synthTypeExprs = make(map[ast.TypeExpr]types.Type)
	}
}

func (a *Analyzer) recordInterfaceDecl(ifc *types.Interface, td *ast.InterfaceDecl) {
	if ifc == nil || td == nil {
		return
	}
	a.ensureDeriveMaps()
	a.ifaceDecls[ifc] = td
}

func (a *Analyzer) recordTrialUndo(fn func()) {
	if a.inDeriveTrial > 0 && fn != nil {
		a.trialUndos = append(a.trialUndos, fn)
	}
}

// resolveDerivableAttrs validates every `@derivable(...)` attribute on registered
// interfaces once Pass 1E has populated all generic functions across modules.
func (a *Analyzer) resolveDerivableAttrs() {
	a.ensureDeriveMaps()
	seen := make(map[*types.Interface]bool)
	for _, ifc := range a.Prog.Interfaces {
		if ifc == nil || seen[ifc] {
			continue
		}
		seen[ifc] = true
		td := a.ifaceDecls[ifc]
		mod := a.Prog.InterfaceMods[ifc]
		if td == nil || mod == nil {
			continue
		}
		var derivableAttr *ast.Attribute
		for i := range td.Attributes {
			if td.Attributes[i].Name == "derivable" {
				if derivableAttr != nil {
					a.Rep.Errorf(td.Attributes[i].Span, "duplicate @derivable attribute on interface %s", ifc.Name)
					continue
				}
				derivableAttr = &td.Attributes[i]
			}
		}
		if derivableAttr == nil {
			continue
		}
		if len(derivableAttr.Args) == 0 {
			a.Rep.Errorf(derivableAttr.Span, "@derivable on interface %s requires at least one `method: generic_fn` mapping", ifc.Name)
			continue
		}

		methodByName := make(map[string]types.InterfaceMethod, len(ifc.Methods))
		for _, im := range ifc.Methods {
			methodByName[im.Name] = im
		}

		entries := make(map[string]*derivableEntry, len(derivableAttr.Args))
		meta := make(map[string]string, len(derivableAttr.Args))
		for _, rawArg := range derivableAttr.Args {
			colonIdx := strings.IndexByte(rawArg, ':')
			if colonIdx < 0 {
				a.Rep.Errorf(derivableAttr.Span, "expected `method: generic_fn` in @derivable on interface %s, got %q", ifc.Name, rawArg)
				continue
			}
			methName := strings.TrimSpace(rawArg[:colonIdx])
			fnRef := strings.TrimSpace(rawArg[colonIdx+1:])
			im, ok := methodByName[methName]
			if !ok {
				a.Rep.Errorf(derivableAttr.Span, "@derivable references unknown method %q on interface %s", methName, ifc.Name)
				continue
			}
			if entries[methName] != nil {
				a.Rep.Errorf(derivableAttr.Span, "duplicate @derivable entry for method %q on interface %s", methName, ifc.Name)
				continue
			}

			tmpl := a.resolveDerivableTargetFn(mod, fnRef, derivableAttr.Span)
			if tmpl == nil {
				continue
			}
			if !a.validateDerivableHandlerSig(ifc, im, tmpl, fnRef, derivableAttr.Span) {
				continue
			}
			entries[methName] = &derivableEntry{
				Method:   methName,
				Impl:     tmpl,
				AttrSpan: derivableAttr.Span,
			}
			meta[methName] = fnRef
		}

		for _, im := range ifc.Methods {
			if entries[im.Name] == nil {
				a.Rep.Errorf(derivableAttr.Span, "@derivable on interface %s is missing handler for method %q", ifc.Name, im.Name)
			}
		}

		if len(entries) == len(ifc.Methods) {
			a.derivable[ifc] = entries
			ifc.DerivableMethods = meta
		}
	}
}

func (a *Analyzer) resolveDerivableTargetFn(mod *Module, fnRef string, span diag.Span) *GenericFuncTemplate {
	if fnRef == "" {
		a.Rep.Errorf(span, "missing handler function in @derivable")
		return nil
	}
	if dotIdx := strings.IndexByte(fnRef, '.'); dotIdx >= 0 {
		pkgAlias := strings.TrimSpace(fnRef[:dotIdx])
		symName := strings.TrimSpace(fnRef[dotIdx+1:])
		if pkgAlias == "" || symName == "" || strings.ContainsRune(symName, '.') {
			a.Rep.Errorf(span, "invalid @derivable handler %q; expected `fn_name` or `module.fn_name`", fnRef)
			return nil
		}
		targetMod := mod.Aliased[pkgAlias]
		if targetMod == nil && mod.Pkg != nil && mod.Pkg.IsCore && pkgAlias == "derive" && a.derivePkg != nil {
			targetMod = a.derivePkg.PrimaryMod
		}
		if targetMod == nil {
			a.Rep.Errorf(span, "unknown module %q in @derivable handler %q", pkgAlias, fnRef)
			return nil
		}
		if !IsExported(symName) && !a.SamePackage(mod, targetMod) {
			a.Rep.Errorf(span, "cannot reference unexported function %q in @derivable across packages", fnRef)
			return nil
		}
		if tmpl := a.lookupGenericFnInModOrPkg(targetMod, symName, mod); tmpl != nil {
			return tmpl
		}
		if sym := targetMod.Symbols[symName]; sym != nil && sym.FuncInst != nil {
			a.Rep.Errorf(span, "@derivable handler %q must be a generic function with one type parameter [T]", fnRef)
			return nil
		}
		a.Rep.Errorf(span, "cannot find generic function %q for @derivable", fnRef)
		return nil
	}

	if tmpl := a.lookupGenericFnInModOrPkg(mod, fnRef, mod); tmpl != nil {
		return tmpl
	}
	if sym := mod.Symbols[fnRef]; sym != nil && sym.FuncInst != nil {
		a.Rep.Errorf(span, "@derivable handler %q must be a generic function with one type parameter [T]", fnRef)
		return nil
	}
	a.Rep.Errorf(span, "cannot find generic function %q for @derivable", fnRef)
	return nil
}

func (a *Analyzer) lookupGenericFnInModOrPkg(targetMod *Module, name string, callerMod *Module) *GenericFuncTemplate {
	if targetMod == nil {
		return nil
	}
	if tmpl := targetMod.GenericFuncs[name]; tmpl != nil {
		return tmpl
	}
	if targetMod.Pkg != nil {
		for _, f := range targetMod.Pkg.Files {
			if f == nil || (f.IsTestFile && f != callerMod) {
				continue
			}
			if tmpl := f.GenericFuncs[name]; tmpl != nil {
				return tmpl
			}
		}
	}
	return nil
}

func (a *Analyzer) validateDerivableHandlerSig(ifc *types.Interface, im types.InterfaceMethod, tmpl *GenericFuncTemplate, fnRef string, span diag.Span) bool {
	if tmpl == nil || tmpl.VarDecl == nil {
		return false
	}
	decl := tmpl.VarDecl
	if len(decl.TypeParams) != 1 || decl.TypeParams[0].IsConst {
		a.Rep.Errorf(span, "@derivable handler %q for `%s.%s` must take a single type parameter [T]", fnRef, ifc.Name, im.Name)
		return false
	}
	fl, ok := decl.Value.(*ast.FuncLit)
	if !ok {
		a.Rep.Errorf(span, "@derivable handler %q for `%s.%s` must be a generic function", fnRef, ifc.Name, im.Name)
		return false
	}
	if fl.Ret == nil && im.Ret != nil && im.Ret != types.TypeVoid {
		a.Rep.Errorf(span, "@derivable handler %q for `%s.%s` must declare an explicit return type", fnRef, ifc.Name, im.Name)
		return false
	}
	if len(fl.Params) != 1+len(im.Params) {
		a.Rep.Errorf(span, "@derivable handler %q has %d parameter(s), but `%s.%s` requires %d (target value plus method parameters)",
			fnRef, len(fl.Params), ifc.Name, im.Name, 1+len(im.Params))
		return false
	}

	tp := decl.TypeParams[0]
	var bounds []*types.Interface
	for _, bName := range tp.Bounds {
		if bTy := a.lookupTypeInModule(tmpl.Mod, "", bName); bTy != nil {
			if bIfc, ok := bTy.(*types.Interface); ok {
				bounds = append(bounds, bIfc)
			}
		}
	}
	tpVar := &types.TypeParamVar{Name: tp.Name, Bounds: bounds}
	subst := map[string]types.Type{tp.Name: tpVar}

	p0Mode, p0Ty := a.resolveParamType(tmpl.Mod, fl.Params[0].Type, subst, false)
	if p0Mode != im.RecvMode || !p0Ty.Equals(tpVar) {
		a.Rep.Errorf(span, "@derivable handler %q first parameter must have mode/type matching `%s.%s` receiver (%s)",
			fnRef, ifc.Name, im.Name, tp.Name)
		return false
	}

	for j, ip := range im.Params {
		pmode, pty := a.resolveParamType(tmpl.Mod, fl.Params[1+j].Type, subst, false)
		expTy := substSelfInType(ip.Type, tpVar)
		if pmode != ip.Mode || !pty.Equals(expTy) {
			a.Rep.Errorf(span, "@derivable handler %q parameter %d does not match `%s.%s` parameter type %s",
				fnRef, j+2, ifc.Name, im.Name, expTy.String())
			return false
		}
	}

	var retTy types.Type = types.TypeVoid
	if fl.Ret != nil {
		retTy = a.resolveReturnType(tmpl.Mod, fl.Ret, subst, false)
	}
	expRet := substSelfInType(im.Ret, tpVar)
	if expRet == nil {
		expRet = types.TypeVoid
	}
	if !retTy.Equals(expRet) {
		a.Rep.Errorf(span, "@derivable handler %q return type %s does not match `%s.%s` return type %s",
			fnRef, retTy.String(), ifc.Name, im.Name, expRet.String())
		return false
	}
	return true
}

func substSelfInType(t types.Type, repl types.Type) types.Type {
	if t == nil {
		return nil
	}
	switch v := t.(type) {
	case *types.SelfPlaceholder:
		return repl
	case *types.Optional:
		el := substSelfInType(v.Elem, repl)
		if el == v.Elem {
			return t
		}
		return &types.Optional{Elem: el}
	case *types.Vector:
		el := substSelfInType(v.Elem, repl)
		if el == v.Elem {
			return t
		}
		return &types.Vector{Elem: el}
	case *types.StackArray:
		el := substSelfInType(v.Elem, repl)
		if el == v.Elem {
			return t
		}
		return &types.StackArray{Size: v.Size, Elem: el}
	case *types.Ptr:
		el := substSelfInType(v.Elem, repl)
		if el == v.Elem {
			return t
		}
		return &types.Ptr{Elem: el}
	case *types.Ref:
		el := substSelfInType(v.Elem, repl)
		if el == v.Elem {
			return t
		}
		return &types.Ref{Elem: el}
	case *types.Tuple:
		elems := make([]types.Type, len(v.Elems))
		changed := false
		for i, e := range v.Elems {
			elems[i] = substSelfInType(e, repl)
			if elems[i] != e {
				changed = true
			}
		}
		if !changed {
			return t
		}
		return &types.Tuple{Elems: elems}
	case *types.Set:
		el := substSelfInType(v.Elem, repl)
		if el == v.Elem {
			return t
		}
		return &types.Set{Elem: el}
	case *types.Map:
		k := substSelfInType(v.Key, repl)
		val := substSelfInType(v.Value, repl)
		if k == v.Key && val == v.Value {
			return t
		}
		return &types.Map{Key: k, Value: val}
	default:
		return t
	}
}

// expandDerives synthesizes thin `const` wrapper methods for `@derive(...)` attributes
// on structs and enums, backed by each interface's `@derivable` generic handler.
func (a *Analyzer) expandDerives(mod *Module) {
	a.ensureDeriveMaps()
	mod.DerivedDecls = nil

	written := a.collectWrittenMethods(mod)
	derivedMethods := make(map[string]bool)

	for _, d := range mod.ActiveDecls() {
		var name string
		var tparams []ast.TypeParam
		var attrs []ast.Attribute
		var isResult bool
		switch decl := d.(type) {
		case *ast.StructDecl:
			name, tparams, attrs = decl.Name, decl.TypeParams, decl.Attributes
		case *ast.EnumDecl:
			name, tparams, attrs, isResult = decl.Name, decl.TypeParams, decl.Attributes, decl.IsResult
		case *ast.DistinctTypeDecl:
			name, attrs = decl.Name, decl.Attributes
		default:
			continue
		}

		isComptimeOnly := ast.FindAttribute(attrs, "comptime_only") != nil
		seenIfaces := make(map[*types.Interface]bool)

		for _, attr := range attrs {
			if attr.Name != "derive" {
				continue
			}
			if len(attr.Args) == 0 {
				a.Rep.Errorf(attr.Span, "@derive needs at least one interface, e.g. @derive(Equatable, Hashable)")
				continue
			}
			for _, ifaceArg := range attr.Args {
				ifc := a.resolveDeriveInterfaceArg(mod, ifaceArg, attr.Span)
				if ifc == nil {
					continue
				}
				if seenIfaces[ifc] {
					continue
				}
				seenIfaces[ifc] = true

				entries := a.derivable[ifc]
				if len(entries) == 0 {
					a.Rep.Errorf(attr.Span, "cannot derive %q; interface %s has no `@derivable(...)` handlers", ifaceArg, ifc.Name)
					continue
				}

				for _, im := range ifc.Methods {
					key := name + "." + im.Name
					if written[key] {
						a.Rep.Errorf(attr.Span, "%s derives %s but also defines `%s` by hand; remove one", name, ifc.Name, im.Name)
						continue
					}
					if derivedMethods[key] {
						a.Rep.Errorf(attr.Span, "%s derives conflicting implementations for method `%s` (interface %s)", name, im.Name, ifc.Name)
						continue
					}
					entry := entries[im.Name]
					if entry == nil || entry.Impl == nil {
						continue
					}
					derivedMethods[key] = true

					md := a.synthesizeDerivedWrapper(mod, name, tparams, isComptimeOnly, isResult, ifc, im, entry, attr.Span)
					a.derived[md] = &deriveInfo{
						Iface:    ifc.Name,
						Method:   im.Name,
						TypeName: name,
						Span:     attr.Span,
						Mod:      mod,
						Tmpl:     entry.Impl,
					}
					a.derivedOrder = append(a.derivedOrder, md)
					mod.DerivedDecls = append(mod.DerivedDecls, md)
					a.registerMethodDecl(mod, md)
				}
			}
		}
	}
}

func (a *Analyzer) collectWrittenMethods(mod *Module) map[string]bool {
	written := make(map[string]bool)
	recordMod := func(m *Module) {
		if m == nil {
			return
		}
		for _, d := range m.ActiveDecls() {
			if md, ok := d.(*ast.MethodDecl); ok && md.RecvModule == "" {
				written[md.RecvType+"."+md.Name] = true
			}
		}
	}
	if mod != nil && mod.Pkg != nil {
		for _, f := range mod.Pkg.Files {
			if f == nil || (f.IsTestFile && f != mod) {
				continue
			}
			recordMod(f)
		}
	} else {
		recordMod(mod)
	}
	return written
}

func (a *Analyzer) resolveDeriveInterfaceArg(mod *Module, arg string, span diag.Span) *types.Interface {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		a.Rep.Errorf(span, "empty interface name in @derive(...)")
		return nil
	}
	if dotIdx := strings.IndexByte(arg, '.'); dotIdx >= 0 {
		alias := strings.TrimSpace(arg[:dotIdx])
		name := strings.TrimSpace(arg[dotIdx+1:])
		if alias == "" || name == "" || strings.ContainsRune(name, '.') {
			a.Rep.Errorf(span, "invalid interface reference %q in @derive(...)", arg)
			return nil
		}
		targetMod := mod.Aliased[alias]
		if targetMod == nil {
			a.Rep.Errorf(span, "unknown module %q in @derive(%s)", alias, arg)
			return nil
		}
		if !IsExported(name) && !a.SamePackage(mod, targetMod) {
			a.Rep.Errorf(span, "cannot derive unexported interface %q across packages", arg)
			return nil
		}
		if gt := targetMod.GenericInterfaces[name]; gt != nil {
			a.Rep.Errorf(span, "cannot derive generic interface %q", arg)
			return nil
		}
		if ifc, ok := targetMod.Types[name].(*types.Interface); ok {
			return ifc
		}
		a.Rep.Errorf(span, "cannot derive %q: %s is not an interface", arg, arg)
		return nil
	}

	if tmpl := a.lookupGenInterfaceInModule(mod, "", arg); tmpl != nil {
		a.Rep.Errorf(span, "cannot derive generic interface %q", arg)
		return nil
	}
	if ty := a.lookupTypeInModule(mod, "", arg); ty != nil {
		if ifc, ok := ty.(*types.Interface); ok {
			return ifc
		}
		a.Rep.Errorf(span, "cannot derive %q: %s is not an interface", arg, arg)
		return nil
	}
	a.Rep.Errorf(span, "cannot derive unknown interface %q", arg)
	return nil
}

func (a *Analyzer) synthesizeDerivedWrapper(
	mod *Module,
	typeName string,
	tparams []ast.TypeParam,
	isComptimeOnly bool,
	isResult bool,
	ifc *types.Interface,
	im types.InterfaceMethod,
	entry *derivableEntry,
	attrSpan diag.Span,
) *ast.MethodDecl {
	recvTPs := make([]string, len(tparams))
	tpTypeArgs := make([]ast.TypeExpr, len(tparams))
	for i, tp := range tparams {
		recvTPs[i] = tp.Name
		tpTypeArgs[i] = &ast.NamedType{Name: tp.Name, Span: attrSpan}
	}

	selfTypeExpr := &ast.NamedType{
		Name:     typeName,
		TypeArgs: tpTypeArgs,
		Span:     attrSpan,
	}

	recvMode := ast.RecvRead
	var selfArg ast.Expr = &ast.IdentExpr{Name: "self", Span: attrSpan}
	switch im.RecvMode {
	case types.ModeMut:
		recvMode = ast.RecvMut
		selfArg = &ast.UnaryExpr{Op: token.Amp, Operand: selfArg, Span: attrSpan}
	case types.ModeOwn:
		recvMode = ast.RecvOwn
		selfArg = &ast.UnaryExpr{Op: token.KwOwn, Operand: selfArg, Span: attrSpan}
	}

	params := make([]ast.Param, len(im.Params))
	callArgs := make([]ast.Expr, 1+len(im.Params))
	callArgs[0] = selfArg
	for i, ip := range im.Params {
		pname := fmt.Sprintf("_drv_p%d", i)
		if _, isOpt := ip.Type.(*types.Optional); isOpt {
			pname += "?"
		} else if types.AsResult(ip.Type) != nil {
			pname += "!"
		} else if _, isSelf := ip.Type.(*types.SelfPlaceholder); isSelf && isResult {
			pname += "!"
		}
		pte := a.typeExprFromInterfaceType(ip.Type, selfTypeExpr, attrSpan)
		switch ip.Mode {
		case types.ModeMut:
			pte = &ast.MutBorrowType{Elem: pte, Span: attrSpan}
			callArgs[1+i] = &ast.UnaryExpr{Op: token.Amp, Operand: &ast.IdentExpr{Name: pname, Span: attrSpan}, Span: attrSpan}
		case types.ModeOwn:
			pte = &ast.OwnType{Elem: pte, Span: attrSpan}
			callArgs[1+i] = &ast.UnaryExpr{Op: token.KwOwn, Operand: &ast.IdentExpr{Name: pname, Span: attrSpan}, Span: attrSpan}
		default:
			callArgs[1+i] = &ast.IdentExpr{Name: pname, Span: attrSpan}
		}
		params[i] = ast.Param{Name: pname, Type: pte, Span: attrSpan}
	}

	var retExpr ast.TypeExpr
	if im.Ret != nil && !im.Ret.Equals(types.TypeVoid) {
		retExpr = a.typeExprFromInterfaceType(im.Ret, selfTypeExpr, attrSpan)
	}

	call := &ast.CallExpr{
		Callee: &ast.IdentExpr{Name: entry.Impl.Name, Span: attrSpan},
		Args:   callArgs,
		Span:   attrSpan,
	}
	a.deriveCallBindings[call] = &deriveCallBinding{
		ImplTmpl: entry.Impl,
		Iface:    ifc,
		AttrSpan: attrSpan,
		TypeName: typeName,
	}

	var mdAttrs []ast.Attribute
	if isComptimeOnly {
		mdAttrs = []ast.Attribute{{Name: "comptime_only", Span: attrSpan}}
	}

	return &ast.MethodDecl{
		RecvType:       typeName,
		RecvTypeParams: recvTPs,
		Name:           im.Name,
		RecvMode:       recvMode,
		Params:         params,
		Ret:            retExpr,
		Body:           call,
		IsConst:        true,
		Attributes:     mdAttrs,
		Doc:            fmt.Sprintf("Derived by `@derive(%s)`.", ifc.Name),
		Span:           attrSpan,
	}
}

func (a *Analyzer) typeExprFromInterfaceType(ty types.Type, selfTE ast.TypeExpr, span diag.Span) ast.TypeExpr {
	if ty == nil || ty == types.TypeVoid {
		return nil
	}
	switch v := ty.(type) {
	case *types.SelfPlaceholder:
		return selfTE
	case *types.Optional:
		return &ast.OptionalType{
			Elem: a.typeExprFromInterfaceType(v.Elem, selfTE, span),
			Span: span,
		}
	case *types.Vector:
		return &ast.VectorType{
			Elem: a.typeExprFromInterfaceType(v.Elem, selfTE, span),
			Span: span,
		}
	case *types.StackArray:
		return &ast.StackArrayType{
			Size: &ast.IntLit{Raw: fmt.Sprintf("%d", v.Size), Val: uint64(v.Size), Span: span},
			Elem: a.typeExprFromInterfaceType(v.Elem, selfTE, span),
			Span: span,
		}
	case *types.Ptr:
		return &ast.NamedType{
			Name:     "ptr",
			TypeArgs: []ast.TypeExpr{a.typeExprFromInterfaceType(v.Elem, selfTE, span)},
			Span:     span,
		}
	case *types.Ref:
		return &ast.NamedType{
			Name:     "ref",
			TypeArgs: []ast.TypeExpr{a.typeExprFromInterfaceType(v.Elem, selfTE, span)},
			Span:     span,
		}
	case *types.Tuple:
		elems := make([]ast.TypeExpr, len(v.Elems))
		for i, el := range v.Elems {
			elems[i] = a.typeExprFromInterfaceType(el, selfTE, span)
		}
		return &ast.TupleType{Elems: elems, Span: span}
	case *types.Set:
		return &ast.SetType{
			Elem: a.typeExprFromInterfaceType(v.Elem, selfTE, span),
			Span: span,
		}
	case *types.Map:
		return &ast.MapType{
			Key:   a.typeExprFromInterfaceType(v.Key, selfTE, span),
			Value: a.typeExprFromInterfaceType(v.Value, selfTE, span),
			Span:  span,
		}
	default:
		// Map closed non-Self types directly so cross-package interface parameter/return types
		// never depend on imports in the deriving struct/enum's module.
		ph := &ast.NamedType{Name: ty.String(), Span: span}
		a.synthTypeExprs[ph] = ty
		return ph
	}
}

// verifyGenericDerivedMethod runs a silent trial check on a newly synthesized
// conditional generic derived method (`Box[T].eq` instantiated for `Box[Arg]`).
// If checking fails (for example `Arg` lacks the required operations), any tentative
// instances created during the trial are rolled back cleanly and false is returned.
func (a *Analyzer) verifyGenericDerivedMethod(
	info *deriveInfo,
	recvTy types.Type,
	fi *FuncInstance,
	funcsBase int,
	trialKey string,
	undoFn func(),
) bool {
	a.ensureDeriveMaps()
	if info == nil || a.derivePassed[trialKey] {
		return true
	}
	if a.deriveFailed[trialKey] {
		undoFn()
		a.Prog.Funcs = a.Prog.Funcs[:funcsBase]
		return false
	}
	if a.deriveChecking[trialKey] {
		return true
	}

	fi.DeriveAttrSpan = info.Span
	fi.DeriveIfaceName = info.Iface

	a.deriveChecking[trialKey] = true
	savedRep := a.Rep
	trialRep := diag.NewReporter()
	a.Rep = trialRep
	savedInBodyPass := a.inBodyPass
	a.inBodyPass = true
	a.inDeriveTrial++
	undoBase := len(a.trialUndos)
	constDepsBase := len(a.constGenericDeps)
	defersBase := len(a.deferredIfaceChecks)

	a.checkFuncInstance(fi)

	a.inDeriveTrial--
	a.inBodyPass = savedInBodyPass
	a.Rep = savedRep
	delete(a.deriveChecking, trialKey)

	if trialRep.HasErrors() {
		for i := len(a.trialUndos) - 1; i >= undoBase; i-- {
			a.trialUndos[i]()
		}
		a.trialUndos = a.trialUndos[:undoBase]
		undoFn()
		if len(a.Prog.Funcs) > funcsBase {
			a.Prog.Funcs = a.Prog.Funcs[:funcsBase]
		}
		if len(a.constGenericDeps) > constDepsBase {
			a.constGenericDeps = a.constGenericDeps[:constDepsBase]
		}
		if len(a.deferredIfaceChecks) > defersBase {
			a.deferredIfaceChecks = a.deferredIfaceChecks[:defersBase]
		}
		prevFailed, hadFailed := a.deriveFailed[trialKey]
		a.deriveFailed[trialKey] = true
		a.recordTrialUndo(func() {
			if hadFailed {
				a.deriveFailed[trialKey] = prevFailed
			} else {
				delete(a.deriveFailed, trialKey)
			}
		})
		return false
	}

	prevPassed, hadPassed := a.derivePassed[trialKey]
	a.derivePassed[trialKey] = true
	a.recordTrialUndo(func() {
		if hadPassed {
			a.derivePassed[trialKey] = prevPassed
		} else {
			delete(a.derivePassed, trialKey)
		}
	})
	return true
}

// validateDerives validates that all concrete `@derive(...)` implementations have
// valid targets; actual type checking runs uniformly in runBodyChecking.
func (a *Analyzer) validateDerives() {
	// Concrete derived methods are typechecked directly in Pass 2 (`runBodyChecking`),
	// while conditional generic derives (`Box[T]`) are checked on demand via trial instantiation.
}
