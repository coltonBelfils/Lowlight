package sema

import (
	"fmt"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

func (a *Analyzer) lookupTypeInModule(mod *Module, moduleAlias, name string) types.Type {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		return target.Types[name]
	}
	if p := types.LookupPrimitive(name); p != nil {
		return p
	}
	if t, ok := mod.Types[name]; ok {
		return t
	}
	for _, imp := range mod.Imports {
		if t, ok := imp.Types[name]; ok {
			return t
		}
	}
	return nil
}

func (a *Analyzer) lookupGenStructInModule(mod *Module, moduleAlias, name string) *GenericStructTemplate {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		return target.GenericStructs[name]
	}
	if g, ok := mod.GenericStructs[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if g, ok := imp.GenericStructs[name]; ok {
			return g
		}
	}
	return nil
}

func (a *Analyzer) lookupTopSymbol(mod *Module, moduleAlias, name string) *Symbol {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		return target.Symbols[name]
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

func (a *Analyzer) lookupGenFunc(mod *Module, moduleAlias, name string) *GenericFuncTemplate {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		return target.GenericFuncs[name]
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

func (a *Analyzer) runTypeAndSymbolRegistration() {
	// Pass 1A: Evaluate simple top-level `bind CONST = <int_expr>` so stack array lengths and enum values work.
	for _, mod := range a.Prog.Modules {
		a.collectTopLevelIntConsts(mod)
	}

	// Pass 1B: Register nominal type headers across all modules (order-independent!).
	for _, mod := range a.Prog.Modules {
		a.registerModuleTypeHeaders(mod)
	}
	if a.Rep.HasErrors() && !a.ideMode {
		return
	}

	// Pass 1C: Resolve Distinct base types, Enum variants, Interface signatures, and non-generic Struct fields.
	for _, mod := range a.Prog.Modules {
		a.resolveDistinctsAndEnums(mod)
	}
	for _, mod := range a.Prog.Modules {
		a.resolveInterfaces(mod)
	}
	for _, mod := range a.Prog.Modules {
		a.resolveStructFields(mod)
	}
	if a.Rep.HasErrors() && !a.ideMode {
		return
	}

	// Pass 1D: Detect value vs optional cycles across structs (marking BoxedOptional on recursive `?Struct`).
	a.checkStructRecursion()
	if a.Rep.HasErrors() && !a.ideMode {
		return
	}

	// Pass 1E: Register extern headers/functions, top-level functions/vars, and methods.
	for _, mod := range a.Prog.Modules {
		a.registerFunctionsAndMethods(mod)
	}
}

func (a *Analyzer) collectTopLevelIntConsts(mod *Module) {
	for _, d := range mod.File.Decls {
		vd, ok := d.(*ast.VarDecl)
		if !ok || !vd.IsBind || len(vd.TypeParams) > 0 || vd.IsOptName {
			continue
		}
		if val, ok2 := a.evalConstInt(mod, vd.Value); ok2 {
			mod.Symbols[vd.Name] = &Symbol{
				Name:        vd.Name,
				CName:       types.SanitizeCIdent(vd.Name),
				Type:        types.TypeInt,
				IsBind:      true,
				BindKind:    BindOwned,
				IsTopLevel:  true,
				IsConstInt:  true,
				ConstIntVal: val,
				Span:        vd.Span,
			}
		}
	}
}

func (a *Analyzer) evalConstInt(mod *Module, e ast.Expr) (int64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return int64(v.Val), true
	case *ast.CharLit:
		return int64(v.Val), true
	case *ast.IdentExpr:
		if sym := a.lookupTopSymbol(mod, "", v.Name); sym != nil && sym.IsConstInt {
			return sym.ConstIntVal, true
		}
		return 0, false
	case *ast.UnaryExpr:
		sub, ok := a.evalConstInt(mod, v.Operand)
		if !ok {
			return 0, false
		}
		switch v.Op {
		case token.Minus:
			return -sub, true
		case token.Tilde:
			return ^sub, true
		}
	case *ast.UnsafeExpr:
		return a.evalConstInt(mod, v.Expr)
	case *ast.BinaryExpr:
		l, ok1 := a.evalConstInt(mod, v.Left)
		r, ok2 := a.evalConstInt(mod, v.Right)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch v.Op {
		case token.Plus:
			return l + r, true
		case token.Minus:
			return l - r, true
		case token.Star:
			return l * r, true
		case token.Slash:
			if r == 0 {
				return 0, false
			}
			return l / r, true
		case token.Percent:
			if r == 0 {
				return 0, false
			}
			return l % r, true
		case token.Shl:
			return l << uint(r), true
		case token.Shr:
			return l >> uint(r), true
		case token.Amp:
			return l & r, true
		case token.Pipe:
			return l | r, true
		case token.Caret:
			return l ^ r, true
		}
	}
	return 0, false
}

func (a *Analyzer) registerModuleTypeHeaders(mod *Module) {
	for _, d := range mod.File.Decls {
		switch td := d.(type) {
		case *ast.StructDecl:
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				a.Rep.Errorf(langAttr.Span, "@lang attribute cannot be applied to a struct declaration")
			}
			if len(td.TypeParams) > 0 {
				if _, dup := mod.GenericStructs[td.Name]; dup {
					a.Rep.Errorf(td.Span, "duplicate type definition %q", td.Name)
					continue
				}
				mod.GenericStructs[td.Name] = &GenericStructTemplate{
					Decl:      td,
					Mod:       mod,
					Instances: make(map[string]*types.Struct),
				}
				continue
			}
			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			st := &types.Struct{
				Name:   td.Name,
				CName:  "ll_s_" + td.Name,
				Packed: td.Packed,
			}
			mod.Types[td.Name] = st
			a.Prog.StructDecls[st] = td
			a.Prog.StructMods[st] = mod
			a.Prog.TypeSpans[st] = td.Span
			a.Prog.Structs = append(a.Prog.Structs, st)

		case *ast.EnumDecl:
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				a.Rep.Errorf(langAttr.Span, "@lang attribute cannot be applied to an enum declaration")
			}
			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			en := &types.Enum{
				Name:       td.Name,
				CName:      "ll_e_" + td.Name,
				Underlying: types.TypeInt,
			}
			mod.Types[td.Name] = en
			a.Prog.TypeSpans[en] = td.Span
			a.Prog.Enums = append(a.Prog.Enums, en)

		case *ast.InterfaceDecl:
			var langItem string
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				if len(langAttr.Args) == 0 {
					a.Rep.Errorf(langAttr.Span, "@lang attribute requires a string argument (e.g. @lang(\"printable\"))")
					continue
				}
				langItem = strings.Trim(langAttr.Args[0], "\"")
			}

			if langItem == "printable" || td.Name == "Printable" {
				if a.Prog.TypeSpans[a.Prog.IfacePrintable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfacePrintable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"printable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfacePrintable].String())
					continue
				}
				a.Prog.IfacePrintable.Name = td.Name
				a.Prog.TypeSpans[a.Prog.IfacePrintable] = td.Span
				mod.Types[td.Name] = a.Prog.IfacePrintable
				continue
			}

			if langItem == "copyable" || td.Name == "Copyable" {
				if a.Prog.TypeSpans[a.Prog.IfaceCopyable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceCopyable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"copyable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceCopyable].String())
					continue
				}
				a.Prog.IfaceCopyable.Name = td.Name
				a.Prog.TypeSpans[a.Prog.IfaceCopyable] = td.Span
				mod.Types[td.Name] = a.Prog.IfaceCopyable
				continue
			}

			if langItem == "drop" || td.Name == "Drop" {
				if a.Prog.TypeSpans[a.Prog.IfaceDrop].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceDrop].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"drop\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceDrop].String())
					continue
				}
				a.Prog.IfaceDrop.Name = td.Name
				a.Prog.TypeSpans[a.Prog.IfaceDrop] = td.Span
				mod.Types[td.Name] = a.Prog.IfaceDrop
				continue
			}

			if langItem != "" {
				a.Rep.Errorf(td.Span, "unknown interface lang item @lang(%q); supported interface lang items: \"printable\", \"copyable\", \"drop\"", langItem)
				continue
			}

			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			ifc := &types.Interface{
				Name:  td.Name,
				CName: "ll_iface_" + td.Name,
			}
			mod.Types[td.Name] = ifc
			a.Prog.TypeSpans[ifc] = td.Span
			a.Prog.Interfaces = append(a.Prog.Interfaces, ifc)

		case *ast.DistinctTypeDecl:
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				a.Rep.Errorf(langAttr.Span, "@lang attribute cannot be applied to a distinct type declaration")
			}
			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			dt := &types.Distinct{
				Name:  td.Name,
				CName: "ll_d_" + td.Name,
				Base:  types.TypeInvalid,
			}
			mod.Types[td.Name] = dt
			a.Prog.TypeSpans[dt] = td.Span
			a.Prog.Distincts = append(a.Prog.Distincts, dt)
		}
	}
}

func (a *Analyzer) checkDupType(mod *Module, name string, span diag.Span) bool {
	if types.LookupPrimitive(name) != nil {
		a.Rep.Errorf(span, "cannot redefine built-in type %q", name)
		return true
	}
	if existing, ok := mod.Types[name]; ok && existing != nil {
		if name != "Printable" && name != "Copyable" && name != "Drop" {
			a.Rep.Errorf(span, "duplicate type definition %q", name)
			return true
		}
	}
	if _, ok := mod.GenericStructs[name]; ok {
		a.Rep.Errorf(span, "duplicate type definition %q", name)
		return true
	}
	return false
}

func (a *Analyzer) resolveDistinctsAndEnums(mod *Module) {
	for _, d := range mod.File.Decls {
		switch td := d.(type) {
		case *ast.DistinctTypeDecl:
			dt, ok := mod.Types[td.Name].(*types.Distinct)
			if !ok {
				continue
			}
			base := a.resolveValueType(mod, td.Base, nil, false)
			if base == types.TypeInvalid {
				continue
			}
			dt.Base = base

		case *ast.EnumDecl:
			en, ok := mod.Types[td.Name].(*types.Enum)
			if !ok {
				continue
			}
			if td.BaseType != nil {
				bt := a.resolveValueType(mod, td.BaseType, nil, false)
				prim, ok2 := bt.(*types.Primitive)
				if !ok2 || !prim.IsInteger() {
					a.Rep.Errorf(td.BaseType.GetSpan(), "enum underlying type must be an integer type (`u8`, `i32`, `int`, etc.), got %s", bt.String())
				} else {
					en.Underlying = prim
				}
			}
			seen := make(map[string]bool)
			var nextVal int64 = 0
			for _, v := range td.Variants {
				if seen[v.Name] {
					a.Rep.Errorf(v.Span, "duplicate variant %q in enum %s", v.Name, en.Name)
					continue
				}
				seen[v.Name] = true
				if v.Value != nil {
					cval, ok2 := a.evalConstInt(mod, v.Value)
					if !ok2 {
						a.Rep.Errorf(v.Value.GetSpan(), "enum discriminant must be a compile-time integer constant")
					} else {
						nextVal = cval
					}
				}
				if nextVal < 0 {
					if !en.Underlying.FitsUnsignedLiteral(uint64(-nextVal), true) {
						a.Rep.Errorf(v.Span, "enum discriminant %d overflows underlying type %s", nextVal, en.Underlying.String())
					}
				} else {
					if !en.Underlying.FitsUnsignedLiteral(uint64(nextVal), false) {
						a.Rep.Errorf(v.Span, "enum discriminant %d overflows underlying type %s", nextVal, en.Underlying.String())
					}
				}
				en.Variants = append(en.Variants, types.EnumVariant{
					Name:  v.Name,
					Value: nextVal,
				})
				nextVal++
			}
		}
	}
}

func (a *Analyzer) resolveInterfaces(mod *Module) {
	for _, d := range mod.File.Decls {
		td, ok := d.(*ast.InterfaceDecl)
		if !ok {
			continue
		}
		if td.Name == "Printable" || td.Name == "Copyable" || td.Name == "Drop" {
			continue
		}
		ifc, ok := mod.Types[td.Name].(*types.Interface)
		if !ok {
			continue
		}
		seen := make(map[string]bool)
		for _, m := range td.Methods {
			if seen[m.Name] {
				a.Rep.Errorf(m.Span, "duplicate method %q in interface %s", m.Name, td.Name)
				continue
			}
			seen[m.Name] = true
			var fparams []types.FuncParam
			for _, p := range m.Params {
				pmode, pty := a.resolveParamType(mod, p.Type, nil, true)
				if _, isSelf := pty.(*types.SelfPlaceholder); isSelf {
					ifc.UsesSelf = true
				}
				a.checkOptionalIdentRule(p.Name, p.IsOptName, pty, p.Span, "interface parameter")
				fparams = append(fparams, types.FuncParam{
					Name: p.Name,
					Mode: pmode,
					Type: pty,
				})
			}
			ret := types.Type(types.TypeVoid)
			if m.Ret != nil {
				ret = a.resolveValueType(mod, m.Ret, nil, true)
				if _, isSelf := ret.(*types.SelfPlaceholder); isSelf {
					ifc.UsesSelf = true
				}
			}
			ifc.Methods = append(ifc.Methods, types.InterfaceMethod{
				Name:     m.Name,
				RecvMode: recvModeToParamMode(m.RecvMode),
				Params:   fparams,
				Ret:      ret,
			})
		}
	}
}

func recvModeToParamMode(rm ast.ReceiverMode) types.ParamMode {
	switch rm {
	case ast.RecvMut:
		return types.ModeMut
	case ast.RecvOwn:
		return types.ModeOwn
	default:
		return types.ModeRead
	}
}

func (a *Analyzer) resolveStructFields(mod *Module) {
	for _, d := range mod.File.Decls {
		td, ok := d.(*ast.StructDecl)
		if !ok || len(td.TypeParams) > 0 {
			continue
		}
		st, ok := mod.Types[td.Name].(*types.Struct)
		if !ok {
			continue
		}
		a.populateStructFields(mod, st, td, nil)
	}
}

func (a *Analyzer) populateStructFields(mod *Module, st *types.Struct, td *ast.StructDecl, subst map[string]types.Type) {
	seen := make(map[string]bool)
	for _, f := range td.Fields {
		if seen[f.Name] {
			a.Rep.Errorf(f.Span, "duplicate field %q in struct %s", f.Name, st.String())
			continue
		}
		seen[f.Name] = true
		fty := a.resolveValueType(mod, f.Type, subst, false)
		a.checkOptionalIdentRule(f.Name, f.IsOpt, fty, f.Span, "struct field")
		st.Fields = append(st.Fields, types.StructField{
			Name:       f.Name,
			CName:      types.SanitizeCIdent(f.Name),
			Type:       fty,
			HasDefault: f.Default != nil,
		})
	}
}

// checkOptionalIdentRule enforces Spec §7.1:
// An identifier has type `?T` IF AND ONLY IF its name ends with `?`.
func (a *Analyzer) checkOptionalIdentRule(name string, isOptName bool, ty types.Type, span diag.Span, kind string) {
	if name == "" || ty == nil || ty == types.TypeInvalid {
		return
	}
	// If `ty` is an uninstantiated generic TypeParamVar `T`, an identifier without `?` is valid (`first: A` where `A = ?int` is instantiated later or `A` is non-opt).
	if _, isTP := ty.(*types.TypeParamVar); isTP {
		return
	}
	_, isOptType := ty.(*types.Optional)
	if isOptType && !isOptName {
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("%s %q has optional type %s and must end with `?`", kind, name, ty.String()),
			fmt.Sprintf("rename `%s` to `%s?` (all optional identifiers in Lowlight end in `?`)", name, name),
		)
	} else if !isOptType && isOptName {
		base := strings.TrimSuffix(name, "?")
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("%s %q ends with `?` but has non-optional type %s", kind, name, ty.String()),
			fmt.Sprintf("rename `%s` to `%s` or give it optional type `?%s`", name, base, ty.String()),
		)
	}
}

// resolveParamType resolves a parameter TypeExpr, extracting `&T` (`ModeMut`) or `own T` (`ModeOwn`),
// while rejecting nested references like `&&T` or `&own T`.
func (a *Analyzer) resolveParamType(mod *Module, te ast.TypeExpr, subst map[string]types.Type, allowSelf bool) (types.ParamMode, types.Type) {
	switch v := te.(type) {
	case *ast.MutBorrowType:
		if _, bad := v.Elem.(*ast.MutBorrowType); bad {
			a.Rep.Errorf(v.Span, "nested reference types (`&&T`) do not exist in Lowlight")
			return types.ModeMut, types.TypeInvalid
		}
		if _, bad := v.Elem.(*ast.OwnType); bad {
			a.Rep.Errorf(v.Span, "cannot combine `&` and `own` on the same parameter")
			return types.ModeMut, types.TypeInvalid
		}
		inner := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		return types.ModeMut, inner
	case *ast.OwnType:
		if _, bad := v.Elem.(*ast.MutBorrowType); bad {
			a.Rep.Errorf(v.Span, "cannot combine `own` and `&` on the same parameter (`own &T` is invalid; borrows cannot be owned)")
			return types.ModeOwn, types.TypeInvalid
		}
		inner := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		return types.ModeOwn, inner
	default:
		return types.ModeRead, a.resolveValueType(mod, te, subst, allowSelf)
	}
}

// resolveValueType resolves a TypeExpr in an owned/value position (forbidding `&T` and `own T`).
func (a *Analyzer) resolveValueType(mod *Module, te ast.TypeExpr, subst map[string]types.Type, allowSelf bool) types.Type {
	if te == nil {
		return types.TypeVoid
	}
	switch v := te.(type) {
	case *ast.MutBorrowType:
		a.Rep.ErrorWithHint(
			v.Span,
			"borrowed reference `&T` is second-class and cannot be stored in structs/collections or returned from functions",
			"store an owned value `T` (via `.copy()` or `own`), or use `Arena` `ref[T]` for region graphs",
		)
		return types.TypeInvalid

	case *ast.OwnType:
		a.Rep.Errorf(v.Span, "`own` is a parameter passing mode and is only valid on function/method parameters")
		return types.TypeInvalid

	case *ast.OptionalType:
		elem := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		if elem == types.TypeInvalid {
			return types.TypeInvalid
		}
		return types.NewOptional(elem)

	case *ast.VectorType:
		elem := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		if elem == types.TypeInvalid {
			return types.TypeInvalid
		}
		return &types.Vector{Elem: elem}

	case *ast.StackArrayType:
		sz, ok := a.evalConstInt(mod, v.Size)
		if !ok || sz <= 0 {
			a.Rep.Errorf(v.Size.GetSpan(), "stack array size in `[N: T]` must be a positive compile-time integer constant")
			return types.TypeInvalid
		}
		elem := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		if elem == types.TypeInvalid {
			return types.TypeInvalid
		}
		return &types.StackArray{Size: sz, Elem: elem}

	case *ast.SetType:
		elem := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		if elem == types.TypeInvalid {
			return types.TypeInvalid
		}
		if _, isTP := elem.(*types.TypeParamVar); !isTP && !types.IsHashable(elem) {
			a.Rep.Errorf(v.Elem.GetSpan(), "set element type %s is not hashable", elem.String())
			return types.TypeInvalid
		}
		return &types.Set{Elem: elem}

	case *ast.MapType:
		kt := a.resolveValueType(mod, v.Key, subst, allowSelf)
		vt := a.resolveValueType(mod, v.Value, subst, allowSelf)
		if kt == types.TypeInvalid || vt == types.TypeInvalid {
			return types.TypeInvalid
		}
		if _, isTP := kt.(*types.TypeParamVar); !isTP && !types.IsHashable(kt) {
			a.Rep.Errorf(v.Key.GetSpan(), "map key type %s is not hashable", kt.String())
			return types.TypeInvalid
		}
		return &types.Map{Key: kt, Value: vt}

	case *ast.FuncType:
		var ps []types.FuncParam
		for _, p := range v.Params {
			pm, pt := a.resolveParamType(mod, p.Type, subst, allowSelf)
			if p.Name != "" {
				a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "function type parameter")
			}
			ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
		}
		ret := types.Type(types.TypeVoid)
		if v.Ret != nil {
			ret = a.resolveValueType(mod, v.Ret, subst, allowSelf)
		}
		return &types.Func{Params: ps, Ret: ret}

	case *ast.NamedType:
		if v.Module == "" && v.Name == "Self" {
			if allowSelf {
				return types.TypeSelf
			}
			a.Rep.Errorf(v.Span, "`Self` is only valid inside `interface` declarations")
			return types.TypeInvalid
		}

		// Built-in `ptr[T]` and `ref[T]`
		if v.Module == "" && v.Name == "ptr" {
			if len(v.TypeArgs) != 1 {
				a.Rep.Errorf(v.Span, "`ptr[T]` requires exactly 1 type argument")
				return types.TypeInvalid
			}
			el := a.resolveValueType(mod, v.TypeArgs[0], subst, allowSelf)
			if el == types.TypeInvalid {
				return types.TypeInvalid
			}
			return &types.Ptr{Elem: el}
		}
		if v.Module == "" && v.Name == "ref" {
			if len(v.TypeArgs) != 1 {
				a.Rep.Errorf(v.Span, "`ref[T]` requires exactly 1 type argument")
				return types.TypeInvalid
			}
			el := a.resolveValueType(mod, v.TypeArgs[0], subst, allowSelf)
			if el == types.TypeInvalid {
				return types.TypeInvalid
			}
			return &types.Ref{Elem: el}
		}

		// Check generic type parameter substitution first
		if v.Module == "" && subst != nil {
			if subTy, ok := subst[v.Name]; ok {
				if len(v.TypeArgs) > 0 {
					a.Rep.Errorf(v.Span, "type parameter %s cannot take type arguments", v.Name)
					return types.TypeInvalid
				}
				return subTy
			}
		}

		// Generic struct instantiation `Pair[int, string]`
		if genSt := a.lookupGenStructInModule(mod, v.Module, v.Name); genSt != nil {
			if len(v.TypeArgs) != len(genSt.Decl.TypeParams) {
				a.Rep.Errorf(v.Span, "generic struct %s expects %d type argument(s), got %d",
					v.Name, len(genSt.Decl.TypeParams), len(v.TypeArgs))
				return types.TypeInvalid
			}
			targs := make([]types.Type, len(v.TypeArgs))
			for i, ta := range v.TypeArgs {
				targs[i] = a.resolveValueType(mod, ta, subst, allowSelf)
				if targs[i] == types.TypeInvalid {
					return types.TypeInvalid
				}
			}
			return a.instantiateStruct(genSt, targs, v.Span)
		}

		if len(v.TypeArgs) > 0 {
			a.Rep.Errorf(v.Span, "type %s is not generic", v.Name)
			return types.TypeInvalid
		}

		found := a.lookupTypeInModule(mod, v.Module, v.Name)
		if found == nil {
			a.Rep.Errorf(v.Span, "unknown type %q", v.Name)
			return types.TypeInvalid
		}
		if ifc, isIfc := found.(*types.Interface); isIfc && ifc.UsesSelf {
			a.Rep.Errorf(v.Span, "interface %s uses `Self` and can only be used as a generic constraint (`[T: %s]`), not as a runtime value type", ifc.Name, ifc.Name)
			return types.TypeInvalid
		}
		return found
	}
	return types.TypeInvalid
}

// checkStructRecursion detects illegal infinite-size value cycles (`next: Node`)
// and marks optional cycles (`next?: ?Node` or mutual `A -> ?B -> ?A`) with `BoxedOptional = true`.
func (a *Analyzer) checkStructRecursion() {
	for _, st := range a.Prog.Structs {
		a.checkSingleStructRecursion(st)
	}
}

func (a *Analyzer) checkSingleStructRecursion(root *types.Struct) {
	// 1. Direct/mutual VALUE cycle (through Struct, StackArray, or Distinct without `?`, `[]`, `ref`, `ptr`)
	if a.reachesStructByValue(root, root, make(map[*types.Struct]bool)) {
		a.Rep.ErrorWithHint(
			diag.Span{},
			fmt.Sprintf("recursive struct %q contains itself by value (infinite size)", root.String()),
			fmt.Sprintf("use an optional field (`?%s`) for automatic heap boxing, `[%s]`, or `ref[%s]`", root.Name, root.Name, root.Name),
		)
		return
	}
	// 2. Direct/mutual OPTIONAL cycle -> auto-heap-box `?root`!
	if a.reachesStructViaOptional(root, root, false, make(map[string]bool)) {
		root.BoxedOptional = true
		root.Fields = root.Fields // invalidate cached plainness if any
	}
}

func (a *Analyzer) reachesStructByValue(curr, target *types.Struct, visited map[*types.Struct]bool) bool {
	if visited[curr] {
		return false
	}
	visited[curr] = true
	for _, f := range curr.Fields {
		if a.typeContainsStructByValue(f.Type, target, visited) {
			return true
		}
	}
	return false
}

func (a *Analyzer) typeContainsStructByValue(t types.Type, target *types.Struct, visited map[*types.Struct]bool) bool {
	switch v := t.(type) {
	case *types.Struct:
		if v == target {
			return true
		}
		return a.reachesStructByValue(v, target, visited)
	case *types.StackArray:
		return a.typeContainsStructByValue(v.Elem, target, visited)
	case *types.Distinct:
		return a.typeContainsStructByValue(v.Base, target, visited)
	default:
		// Note: Optional `?T` is checked next: if `?T` is NOT boxed, `Opt_T` embeds `T` by value!
		// Wait: if `Opt_T` embeds `T`, and `T` reaches `target`, then marking `target.BoxedOptional = true`
		// breaks the cycle! So only non-optional edges are unbreakable infinite-size errors!
		return false
	}
}

func (a *Analyzer) reachesStructViaOptional(curr, target *types.Struct, passedOpt bool, visited map[string]bool) bool {
	key := fmt.Sprintf("%p:%v", curr, passedOpt)
	if visited[key] {
		return false
	}
	visited[key] = true
	for _, f := range curr.Fields {
		if a.typeReachesViaOpt(f.Type, target, passedOpt, visited) {
			return true
		}
	}
	return false
}

func (a *Analyzer) typeReachesViaOpt(t types.Type, target *types.Struct, passedOpt bool, visited map[string]bool) bool {
	switch v := t.(type) {
	case *types.Optional:
		return a.typeReachesViaOpt(v.Elem, target, true, visited)
	case *types.Struct:
		if v == target && passedOpt {
			return true
		}
		if v.BoxedOptional && passedOpt {
			// Cycle already broken by an already-boxed struct along this path!
			return false
		}
		return a.reachesStructViaOptional(v, target, passedOpt, visited)
	case *types.StackArray:
		return a.typeReachesViaOpt(v.Elem, target, passedOpt, visited)
	case *types.Distinct:
		return a.typeReachesViaOpt(v.Base, target, passedOpt, visited)
	default:
		return false
	}
}

// instantiateStruct monomorphizes `define Name[T...] struct [...]` for concrete `typeArgs`.
func (a *Analyzer) instantiateStruct(tmpl *GenericStructTemplate, typeArgs []types.Type, span diag.Span) *types.Struct {
	// If any typeArg is still an uninstantiated TypeParamVar, create an uncached symbolic Struct for template checking.
	hasTypeParam := false
	parts := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		parts[i] = ta.Mangled()
		if _, ok := ta.(*types.TypeParamVar); ok {
			hasTypeParam = true
		}
	}
	key := strings.Join(parts, "__")
	if !hasTypeParam {
		if inst, ok := tmpl.Instances[key]; ok {
			return inst
		}
	}

	// Verify interface bounds when concrete!
	subst := make(map[string]types.Type, len(typeArgs))
	for i, tp := range tmpl.Decl.TypeParams {
		subst[tp.Name] = typeArgs[i]
	}

	inst := &types.Struct{
		Name:     tmpl.Decl.Name,
		CName:    fmt.Sprintf("ll_gs_%s__%s", tmpl.Decl.Name, key),
		Packed:   tmpl.Decl.Packed,
		TypeArgs: typeArgs,
	}
	if !hasTypeParam {
		tmpl.Instances[key] = inst
	}

	a.populateStructFields(tmpl.Mod, inst, tmpl.Decl, subst)
	if !hasTypeParam {
		a.Prog.StructDecls[inst] = tmpl.Decl
		a.Prog.StructMods[inst] = tmpl.Mod
		a.Prog.TypeSpans[inst] = tmpl.Decl.Span
		a.Prog.Structs = append(a.Prog.Structs, inst)
		a.checkSingleStructRecursion(inst)
		// Check bounds after registration so methods are available if called during body checking
		for i, tp := range tmpl.Decl.TypeParams {
			for _, bName := range tp.Bounds {
				bTy := a.lookupTypeInModule(tmpl.Mod, "", bName)
				ifc, ok := bTy.(*types.Interface)
				if !ok {
					a.Rep.Errorf(tp.Span, "bound %q is not an interface", bName)
					continue
				}
				if !a.ImplementsInterface(typeArgs[i], ifc) {
					a.Rep.Errorf(span, "type %s does not satisfy interface %s required by %s", typeArgs[i].String(), ifc.Name, tmpl.Decl.Name)
				}
			}
		}
		// Check if generic Drop method exists on this template
		if gm, ok := a.genMethods[tmpl.Decl.Name]["drop"]; ok {
			_ = a.instantiateMethod(gm, inst, nil, span)
			inst.HasCustomDrop = true
		}
	}
	return inst
}

func (a *Analyzer) registerFunctionsAndMethods(mod *Module) {
	hdrSeen := make(map[string]bool)
	for _, h := range a.Prog.ExternHeaders {
		hdrSeen[h] = true
	}

	for _, d := range mod.File.Decls {
		switch decl := d.(type) {
		case *ast.ExternHeaderDecl:
			if !hdrSeen[decl.Header] {
				hdrSeen[decl.Header] = true
				a.Prog.ExternHeaders = append(a.Prog.ExternHeaders, decl.Header)
			}

		case *ast.ExternFnDecl:
			var ps []types.FuncParam
			for _, p := range decl.Params {
				pm, pt := a.resolveParamType(mod, p.Type, nil, false)
				a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "extern parameter")
				ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
			}
			ret := types.Type(types.TypeVoid)
			if decl.Ret != nil {
				ret = a.resolveValueType(mod, decl.Ret, nil, false)
			}
			sig := &types.Func{Params: ps, Ret: ret}
			ef := &ExternFunc{Name: decl.Name, Sig: sig, Span: decl.Span}
			a.Prog.ExternFuncs = append(a.Prog.ExternFuncs, ef)
			localName := decl.Name
			if decl.Alias != "" {
				localName = decl.Alias
			}
			mod.Symbols[localName] = &Symbol{
				Name:       localName,
				CName:      decl.Name, // exact C ABI symbol!
				Type:       sig,
				IsBind:     true,
				BindKind:   BindOwned,
				IsTopLevel: true,
				ExternFn:   ef,
				Span:       decl.Span,
			}

		case *ast.VarDecl:
			if langAttr := ast.FindAttribute(decl.Attributes, "lang"); langAttr != nil {
				if len(langAttr.Args) == 0 {
					a.Rep.Errorf(langAttr.Span, "@lang attribute requires a string argument (e.g. @lang(\"panic\"))")
				} else {
					kind := strings.Trim(langAttr.Args[0], "\"")
					switch kind {
					case "panic", "assert":
						// valid function lang item
					default:
						a.Rep.Errorf(langAttr.Span, "unknown function lang item @lang(%q); supported function lang items: \"panic\", \"assert\"", kind)
					}
				}
			}
			if len(decl.TypeParams) > 0 {
				if !decl.IsBind {
					a.Rep.Errorf(decl.Span, "generic functions must be declared with `bind`")
					continue
				}
				if _, ok := decl.Value.(*ast.FuncLit); !ok {
					a.Rep.Errorf(decl.Span, "generic top-level `bind` must be a function")
					continue
				}
				mod.GenericFuncs[decl.Name] = &GenericFuncTemplate{
					Name:      decl.Name,
					VarDecl:   decl,
					Mod:       mod,
					Instances: make(map[string]*FuncInstance),
				}
				continue
			}

			// Top-level static function `bind name = (params)[: Ret] body`
			if fl, isFn := decl.Value.(*ast.FuncLit); isFn && decl.IsBind {
				fi := a.buildFuncInstanceFromLit(mod, decl.Name, fl, nil)
				fi.DeclSpan = decl.Span
				a.Prog.Funcs = append(a.Prog.Funcs, fi)
				mod.Symbols[decl.Name] = &Symbol{
					Name:       decl.Name,
					CName:      fi.CName,
					Type:       fi.Sig,
					IsBind:     true,
					BindKind:   BindOwned,
					IsTopLevel: true,
					FuncInst:   fi,
					Span:       decl.Span,
				}
				if mod == a.Prog.EntryMod && decl.Name == "main" {
					a.validateMainSig(fi, decl.Span)
					a.Prog.MainFunc = fi
				}
				continue
			}

			// Top-level constant / variable
			if !decl.IsBind {
				a.Rep.ErrorWithHint(
					decl.Span,
					"mutable global `let` variables are not allowed at top level",
					"use `bind` for top-level constants and functions, and pass mutable state explicitly with `&`",
				)
				continue
			}
			// If not already registered as a simple int constant, resolve during body pass
			sym := mod.Symbols[decl.Name]
			if sym == nil {
				var explicitTy types.Type
				if decl.Type != nil {
					explicitTy = a.resolveValueType(mod, decl.Type, nil, false)
				}
				sym = &Symbol{
					Name:       decl.Name,
					CName:      "ll_g_" + types.SanitizeCIdent(decl.Name),
					Type:       explicitTy,
					IsBind:     true,
					BindKind:   BindOwned,
					IsTopLevel: true,
					Span:       decl.Span,
				}
				mod.Symbols[decl.Name] = sym
			}
			a.Prog.TopVars = append(a.Prog.TopVars, &TopVarInit{Sym: sym, Decl: decl, Mod: mod})

		case *ast.MethodDecl:
			if len(decl.RecvTypeParams) > 0 || len(decl.TypeParams) > 0 {
				if a.genMethods[decl.RecvType] == nil {
					a.genMethods[decl.RecvType] = make(map[string]*GenericFuncTemplate)
				}
				a.genMethods[decl.RecvType][decl.Name] = &GenericFuncTemplate{
					Name:       decl.RecvType + "." + decl.Name,
					MethodDecl: decl,
					Mod:        mod,
					Instances:  make(map[string]*FuncInstance),
				}
				continue
			}

			recvTy := a.lookupTypeInModule(mod, "", decl.RecvType)
			if recvTy == nil {
				a.Rep.Errorf(decl.Span, "unknown receiver type %q in method declaration", decl.RecvType)
				continue
			}
			fi := a.buildMethodInstance(mod, recvTy, decl, nil)
			key := TypeMethodKey(recvTy)
			if a.methods[key] == nil {
				a.methods[key] = make(map[string]*FuncInstance)
			}
			if _, dup := a.methods[key][decl.Name]; dup {
				a.Rep.Errorf(decl.Span, "duplicate method `%s.%s`", decl.RecvType, decl.Name)
				continue
			}
			a.methods[key][decl.Name] = fi
			if a.Prog.MethodTable[key] == nil {
				a.Prog.MethodTable[key] = make(map[string]*FuncInstance)
			}
			a.Prog.MethodTable[key][decl.Name] = fi
			a.Prog.Funcs = append(a.Prog.Funcs, fi)

			// Check if this is `Drop` (`bind MyStruct.drop = (&self)`)
			if st, isSt := recvTy.(*types.Struct); isSt && decl.Name == "drop" {
				if decl.RecvMode != ast.RecvMut || len(decl.Params) != 0 || decl.Ret != nil {
					a.Rep.Errorf(decl.Span, "`%s.drop` must have signature `(&self)` with no extra parameters and void return", st.Name)
				} else {
					st.HasCustomDrop = true
				}
			}
		}
	}
}

func (a *Analyzer) validateMainSig(fi *FuncInstance, span diag.Span) {
	if len(fi.Sig.Params) != 0 {
		a.Rep.Errorf(span, "`main` must take 0 parameters (use `args()` to read command-line arguments)")
	}
	if !fi.Sig.Ret.Equals(types.TypeVoid) && !fi.Sig.Ret.Equals(types.TypeInt) {
		a.Rep.Errorf(span, "`main` return type must be omitted (`void`) or `int`, got %s", fi.Sig.Ret.String())
	}
}

func (a *Analyzer) buildFuncInstanceFromLit(mod *Module, name string, fl *ast.FuncLit, subst map[string]types.Type) *FuncInstance {
	var ps []types.FuncParam
	var pnames []string
	var pspans []diag.Span
	for _, p := range fl.Params {
		pm, pt := a.resolveParamType(mod, p.Type, subst, false)
		a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "parameter")
		ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
		pnames = append(pnames, p.Name)
		pspans = append(pspans, p.Span)
	}
	ret := types.Type(types.TypeVoid)
	if fl.Ret != nil {
		ret = a.resolveValueType(mod, fl.Ret, subst, false)
	}
	cname := "ll_fn_" + sanitizeSymbolName(name)
	if name == "main" && mod == a.Prog.EntryMod {
		cname = "ll_user_main"
	}
	return &FuncInstance{
		Name:       name,
		CName:      cname,
		Sig:        &types.Func{Params: ps, Ret: ret},
		ParamNames: pnames,
		ParamSpans: pspans,
		Body:       fl.Body,
		Mod:        mod,
		TypeSubst:  subst,
	}
}

func (a *Analyzer) buildMethodInstance(mod *Module, recvTy types.Type, decl *ast.MethodDecl, subst map[string]types.Type) *FuncInstance {
	var ps []types.FuncParam
	var pnames []string
	var pspans []diag.Span
	isStatic := decl.RecvMode == ast.ReceiverMode(-1)

	if !isStatic {
		if decl.RecvExplicitTy != nil {
			exp := a.resolveValueType(mod, decl.RecvExplicitTy, subst, false)
			if exp != types.TypeInvalid && !exp.Equals(recvTy) {
				a.Rep.Errorf(decl.RecvExplicitTy.GetSpan(), "explicit `self` type %s does not match method receiver %s", exp.String(), recvTy.String())
			}
		}
		ps = append(ps, types.FuncParam{
			Name: "self",
			Mode: recvModeToParamMode(decl.RecvMode),
			Type: recvTy,
		})
		pnames = append(pnames, "self")
		pspans = append(pspans, decl.Span)
	}

	for _, p := range decl.Params {
		pm, pt := a.resolveParamType(mod, p.Type, subst, false)
		a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "parameter")
		ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
		pnames = append(pnames, p.Name)
		pspans = append(pspans, p.Span)
	}
	ret := types.Type(types.TypeVoid)
	if decl.Ret != nil {
		ret = a.resolveValueType(mod, decl.Ret, subst, false)
	}

	cSuffix := decl.Name
	if len(subst) > 0 {
		var keys []string
		for _, ta := range extractSortedSubst(subst) {
			keys = append(keys, ta)
		}
		cSuffix += "__" + strings.Join(keys, "__")
	}

	return &FuncInstance{
		Name:          fmt.Sprintf("%s.%s", recvTy.String(), decl.Name),
		CName:         fmt.Sprintf("ll_m_%s_%s", recvTy.Mangled(), sanitizeSymbolName(cSuffix)),
		Sig:           &types.Func{Params: ps, Ret: ret},
		IsMethod:      true,
		IsStaticAssoc: isStatic,
		RecvType:      recvTy,
		RecvMode:      decl.RecvMode,
		ParamNames:    pnames,
		ParamSpans:    pspans,
		Body:          decl.Body,
		Mod:           mod,
		TypeSubst:     subst,
		DeclSpan:      decl.Span,
	}
}

func extractSortedSubst(subst map[string]types.Type) []string {
	// Deterministic key order for mangling
	keys := make([]string, 0, len(subst))
	for k := range subst {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = subst[k].Mangled()
	}
	return out
}

func sanitizeSymbolName(s string) string {
	r := strings.NewReplacer("[", "_", "]", "_", ",", "_", " ", "", "?", "_opt", ".", "_")
	return r.Replace(s)
}

// LookupMethod finds a user-defined (or monomorphized) method on `recvTy`.
func (a *Analyzer) LookupMethod(recvTy types.Type, methodName string, methodTypeArgs []types.Type, span diag.Span) *FuncInstance {
	key := TypeMethodKey(recvTy)
	if mset, ok := a.methods[key]; ok {
		if fi, ok2 := mset[methodName]; ok2 {
			return fi
		}
	}

	// Check generic method templates (`bind Pair[A, B].swap = ...` or `bind Foo.bar[T] = ...`)
	baseTypeName := ""
	var recvArgs []types.Type
	if st, isSt := recvTy.(*types.Struct); isSt {
		baseTypeName = st.Name
		recvArgs = st.TypeArgs
	} else if p, isP := recvTy.(*types.Primitive); isP {
		baseTypeName = p.String()
	} else if d, isD := recvTy.(*types.Distinct); isD {
		baseTypeName = d.Name
	} else if e, isE := recvTy.(*types.Enum); isE {
		baseTypeName = e.Name
	}
	if baseTypeName != "" {
		if gset, ok := a.genMethods[baseTypeName]; ok {
			if gtmpl, ok2 := gset[methodName]; ok2 {
				return a.instantiateMethod(gtmpl, recvTy, methodTypeArgs, span)
			}
		}
		_ = recvArgs
	}
	return nil
}

func (a *Analyzer) instantiateMethod(tmpl *GenericFuncTemplate, recvTy types.Type, methodTypeArgs []types.Type, span diag.Span) *FuncInstance {
	decl := tmpl.MethodDecl
	subst := make(map[string]types.Type)
	var keyParts []string
	keyParts = append(keyParts, recvTy.Mangled())

	if st, isSt := recvTy.(*types.Struct); isSt && len(decl.RecvTypeParams) == len(st.TypeArgs) {
		for i, rp := range decl.RecvTypeParams {
			subst[rp] = st.TypeArgs[i]
		}
	}
	if len(decl.TypeParams) > 0 {
		if len(methodTypeArgs) != len(decl.TypeParams) {
			a.Rep.Errorf(span, "method `%s.%s` expects %d type argument(s), got %d",
				decl.RecvType, decl.Name, len(decl.TypeParams), len(methodTypeArgs))
			return nil
		}
		for i, tp := range decl.TypeParams {
			subst[tp.Name] = methodTypeArgs[i]
			keyParts = append(keyParts, methodTypeArgs[i].Mangled())
			a.verifyBounds(tmpl.Mod, tp, methodTypeArgs[i], span, decl.Name)
		}
	}
	instKey := strings.Join(keyParts, "__")
	if existing, ok := tmpl.Instances[instKey]; ok {
		return existing
	}

	fi := a.buildMethodInstance(tmpl.Mod, recvTy, decl, subst)
	tmpl.Instances[instKey] = fi
	if len(methodTypeArgs) == 0 {
		mkey := TypeMethodKey(recvTy)
		if a.Prog.MethodTable[mkey] == nil {
			a.Prog.MethodTable[mkey] = make(map[string]*FuncInstance)
		}
		a.Prog.MethodTable[mkey][decl.Name] = fi
	}
	a.Prog.Funcs = append(a.Prog.Funcs, fi)
	// Typecheck body immediately if we are already in or past body checking!
	if a.inBodyPass {
		a.checkFuncInstance(fi)
	}
	return fi
}

func (a *Analyzer) instantiateGenericFunc(tmpl *GenericFuncTemplate, typeArgs []types.Type, span diag.Span) *FuncInstance {
	decl := tmpl.VarDecl
	if len(typeArgs) != len(decl.TypeParams) {
		a.Rep.Errorf(span, "generic function %q expects %d type argument(s), got %d",
			decl.Name, len(decl.TypeParams), len(typeArgs))
		return nil
	}
	subst := make(map[string]types.Type, len(typeArgs))
	parts := make([]string, len(typeArgs))
	for i, tp := range decl.TypeParams {
		subst[tp.Name] = typeArgs[i]
		parts[i] = typeArgs[i].Mangled()
		a.verifyBounds(tmpl.Mod, tp, typeArgs[i], span, decl.Name)
	}
	key := strings.Join(parts, "__")
	if existing, ok := tmpl.Instances[key]; ok {
		return existing
	}

	fl := decl.Value.(*ast.FuncLit)
	instName := fmt.Sprintf("%s[%s]", decl.Name, strings.Join(parts, ","))
	fi := a.buildFuncInstanceFromLit(tmpl.Mod, instName, fl, subst)
	fi.DeclSpan = decl.Span
	fi.CName = fmt.Sprintf("ll_gfn_%s__%s", decl.Name, key)
	tmpl.Instances[key] = fi
	a.Prog.Funcs = append(a.Prog.Funcs, fi)
	if a.inBodyPass {
		a.checkFuncInstance(fi)
	}
	return fi
}

func (a *Analyzer) verifyBounds(mod *Module, tp ast.TypeParam, concrete types.Type, span diag.Span, contextName string) {
	if _, isTP := concrete.(*types.TypeParamVar); isTP {
		return
	}
	for _, bName := range tp.Bounds {
		bTy := a.lookupTypeInModule(mod, "", bName)
		ifc, ok := bTy.(*types.Interface)
		if !ok {
			a.Rep.Errorf(tp.Span, "bound %q is not an interface", bName)
			continue
		}
		if !a.ImplementsInterface(concrete, ifc) {
			a.Rep.Errorf(span, "type %s does not satisfy interface %s required by %s", concrete.String(), ifc.Name, contextName)
		}
	}
}

// ImplementsInterface checks structural satisfaction (including built-in Printable, Copyable, Drop)
// and registers a VTableImpl when `ifc` is a runtime object-safe interface.
func (a *Analyzer) ImplementsInterface(concrete types.Type, ifc *types.Interface) bool {
	if concrete == nil || concrete == types.TypeInvalid {
		return true
	}
	if concrete.Equals(ifc) {
		return true
	}
	if ifc == a.Prog.IfacePrintable {
		return a.supportsToString(concrete)
	}
	if ifc == a.Prog.IfaceCopyable {
		return a.supportsCopy(concrete)
	}
	if ifc == a.Prog.IfaceDrop {
		if st, ok := concrete.(*types.Struct); ok && st.HasCustomDrop {
			return true
		}
		return false
	}

	impls := make([]*FuncInstance, len(ifc.Methods))
	for idx, req := range ifc.Methods {
		// Special-case built-in `to_string` if a user interface includes `to_string: (self): string`
		fi := a.LookupMethod(concrete, req.Name, nil, diag.Span{})
		if fi == nil {
			if req.Name == "to_string" && req.RecvMode == types.ModeRead && len(req.Params) == 0 && req.Ret.Equals(types.TypeString) && a.supportsToString(concrete) {
				impls[idx] = nil // synthesized built-in `to_string` wrapper in vtable
				continue
			}
			return false
		}
		if fi.IsStaticAssoc || len(fi.Sig.Params) != len(req.Params)+1 {
			return false
		}
		if fi.Sig.Params[0].Mode != req.RecvMode {
			return false
		}
		for pi, rp := range req.Params {
			fp := fi.Sig.Params[pi+1]
			expected := rp.Type
			if _, isSelf := expected.(*types.SelfPlaceholder); isSelf {
				expected = concrete
			}
			if fp.Mode != rp.Mode || !fp.Type.Equals(expected) {
				return false
			}
		}
		expectedRet := req.Ret
		if _, isSelf := expectedRet.(*types.SelfPlaceholder); isSelf {
			expectedRet = concrete
		}
		if !fi.Sig.Ret.Equals(expectedRet) {
			return false
		}
		impls[idx] = fi
	}

	if !ifc.UsesSelf {
		vtKey := concrete.Mangled() + "__as__" + ifc.Mangled()
		if _, exists := a.Prog.VTables[vtKey]; !exists {
			a.Prog.VTables[vtKey] = &VTableImpl{
				Concrete: concrete,
				Iface:    ifc,
				Methods:  impls,
			}
		}
	}
	return true
}

// RegisterVTable ensures a VTableImpl is recorded (including for `Printable`!).
func (a *Analyzer) RegisterVTable(concrete types.Type, ifc *types.Interface) {
	if ifc.UsesSelf {
		return
	}
	vtKey := concrete.Mangled() + "__as__" + ifc.Mangled()
	if _, exists := a.Prog.VTables[vtKey]; exists {
		return
	}
	impls := make([]*FuncInstance, len(ifc.Methods))
	for idx, req := range ifc.Methods {
		impls[idx] = a.LookupMethod(concrete, req.Name, nil, diag.Span{})
	}
	a.Prog.VTables[vtKey] = &VTableImpl{
		Concrete: concrete,
		Iface:    ifc,
		Methods:  impls,
	}
}

func (a *Analyzer) supportsToString(t types.Type) bool {
	if a.LookupMethod(t, "to_string", nil, diag.Span{}) != nil {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind != types.PrimVoid && v.Kind != types.PrimArena
	case *types.Enum:
		return true
	case *types.Distinct:
		return a.supportsToString(v.Base)
	case *types.Vector:
		return a.supportsToString(v.Elem)
	case *types.StackArray:
		return a.supportsToString(v.Elem)
	case *types.Set:
		return a.supportsToString(v.Elem)
	case *types.Map:
		return a.supportsToString(v.Key) && a.supportsToString(v.Value)
	case *types.Optional:
		return a.supportsToString(v.Elem)
	case *types.Interface:
		return v == a.Prog.IfacePrintable
	default:
		return false
	}
}

func (a *Analyzer) supportsCopy(t types.Type) bool {
	return a.supportsCopyVisited(t, make(map[*types.Struct]bool))
}

func (a *Analyzer) supportsCopyVisited(t types.Type, seen map[*types.Struct]bool) bool {
	if t.IsPlain() {
		return true
	}
	if a.LookupMethod(t, "copy", nil, diag.Span{}) != nil {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind == types.PrimString
	case *types.Vector:
		return a.supportsCopyVisited(v.Elem, seen)
	case *types.StackArray:
		return a.supportsCopyVisited(v.Elem, seen)
	case *types.Set:
		return a.supportsCopyVisited(v.Elem, seen)
	case *types.Map:
		return a.supportsCopyVisited(v.Key, seen) && a.supportsCopyVisited(v.Value, seen)
	case *types.Optional:
		return a.supportsCopyVisited(v.Elem, seen)
	case *types.Distinct:
		return a.supportsCopyVisited(v.Base, seen)
	case *types.Interface:
		return !v.UsesSelf
	case *types.Func:
		return true
	case *types.Struct:
		if v.HasCustomDrop {
			// Structs with custom Drop only copy if user explicitly defines `.copy()`
			return false
		}
		if seen[v] {
			return true
		}
		seen[v] = true
		for _, f := range v.Fields {
			if !a.supportsCopyVisited(f.Type, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
