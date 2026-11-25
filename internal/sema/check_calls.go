package sema

import (
	"fmt"
	"math"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

func (fc *funcChecker) recordTargetFunc(inf *ExprInfo, fi *FuncInstance, span diag.Span) {
	inf.TargetFunc = fi
	if fi != nil {
		if fi.Deprecated {
			msg := ""
			if fi.DeprecatedMsg != "" {
				msg = ": " + fi.DeprecatedMsg
			}
			fc.a.Rep.Warnf(span, "use of deprecated function or method %q%s", fi.Name, msg)
		}
		if (fi.IsTest || fi.IsTestOnly || (fc.a.Prog.TestMainFunc != nil && fi == fc.a.Prog.TestMainFunc)) && !fc.isTestContext() {
			callerName := "<top-level>"
			if fc.curFn != nil {
				callerName = fc.curFn.Name
			}
			fc.a.Rep.Errorf(span, "cannot call test-only function %q from non-test context %q", fi.Name, callerName)
		}
		fc.checkFuncConstAndComptimeAccess(fi, span)
	}
}

func (fc *funcChecker) checkFuncConstAndComptimeAccess(fi *FuncInstance, span diag.Span) {
	if fi == nil || fc.probing > 0 {
		return
	}
	if fi.ComptimeOnly && !fc.inComptimeOnlyContext() {
		callerName := "<top-level>"
		if fc.curFn != nil {
			callerName = fc.curFn.Name
		}
		fc.a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("cannot call `@comptime_only` function %q from runtime context %q", fi.Name, callerName),
			"call it inside a `comptime` block/expression, a `const` initializer, or another `@comptime_only` function",
		)
	}
	if fc.inConstContext() {
		if fi.IsConst && (fi.IsGenericInst || fi.IsDerived) && !fi.Checked && !fc.a.checkingFuncs[fi] {
			fc.a.ensureFuncCheckedForComptime(fi, span)
		}
		if fi.IsConst && (fi.IsGenericInst || fi.IsDerived) && fc.a.checkingFuncs[fi] && fc.inConditionalGenericBody() && fc.curFn != nil && fc.curFn != fi {
			fc.a.constGenericDeps = append(fc.a.constGenericDeps, constGenericDep{caller: fc.curFn, callee: fi, span: span})
		}
		if !fc.a.isComptimeCallable(fi) {
			if fi.ConstFail != nil {
				fc.recordConstViolation(
					span,
					fmt.Sprintf("cannot call generic instantiation %q in a `const` or `comptime` context (runtime-only)", fi.Name),
					fmt.Sprintf("%q is declared `const`, so each instantiation is compile-time callable only when all operations on its type arguments are `const`", fi.Name),
					fi,
				)
			} else {
				fc.recordConstViolation(
					span,
					fmt.Sprintf("cannot call runtime-only (`bind`) function %q in a `const` or `comptime` context", fi.Name),
					fmt.Sprintf("declare %q with `const` instead of `bind` if it uses only compile-time-safe operations", fi.Name),
					fi,
				)
			}
		}
	}
}

func (fc *funcChecker) checkInterpConst(ty types.Type, span diag.Span) {
	if ty == nil || ty == types.TypeInvalid {
		return
	}
	if fn := fc.a.LookupMethod(ty, "to_string", nil, span); fn != nil {
		fc.checkFuncConstAndComptimeAccess(fn, span)
		return
	}
	switch t := ty.(type) {
	case *types.Tuple:
		for _, el := range t.Elems {
			fc.checkInterpConst(el, span)
		}
	case *types.StackArray:
		fc.checkInterpConst(t.Elem, span)
	case *types.Optional:
		fc.checkInterpConst(t.Elem, span)
	}
}

func (fc *funcChecker) checkCallExpr(call *ast.CallExpr, expected types.Type) types.Type {
	retTy := fc.checkCallExprInner(call, expected)
	inf := fc.a.infoFor(call)
	if retTy != nil && retTy != types.TypeInvalid {
		if typeMayHoldArenaRef(retTy) || inf.CallKind == CallClosureValue || inf.CallKind == CallInterfaceMethod {
			for _, arg := range call.Args {
				if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.Amp {
					if sym := fc.rootSymbolOfExpr(u.Operand); sym != nil && sym.Type != nil && types.IsArena(sym.Type) && sym.BindKind == BindOwned {
						fc.pinArenaAt(sym, fc.curDepth())
					}
				}
			}
		}
	}
	return retTy
}

func (fc *funcChecker) checkCallExprInner(call *ast.CallExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(call)

	// `cl(f)`: convert a function to a closure. (`cl` is a keyword, so no symbol has that name.)
	if id, isId := call.Callee.(*ast.IdentExpr); isId && id.Name == "cl" && len(call.Args) == 1 {
		var hint types.Type
		if clExp, ok := expected.(*types.Func); ok && clExp.Closure {
			hint = &types.Func{Params: clExp.Params, Ret: clExp.Ret}
		}
		argTy := fc.checkExprExpect(call.Args[0], hint, true)
		fnTy, ok := argTy.(*types.Func)
		if !ok {
			if argTy != types.TypeInvalid {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`cl(...)` converts a function to a closure, but this is %s", argTy.String())
			}
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		clTy := fnTy.AsClosure()
		if fnTy.Extern {
			fc.a.Rep.ErrorWithHint(call.Span, "an `extern fn` can't become a closure",
				"it has the C calling convention; call it from a `cl` literal instead")
		}
		if fnTy.Closure {
			fc.a.Rep.Warnf(call.Span, "this is already a closure; `cl(...)` does nothing")
		} else {
			fc.a.infoFor(call.Args[0]).ToClosure = clTy
		}
		fc.consumeValue(call.Args[0], argTy, false)
		inf.BuiltinName = "cl"
		inf.CallKind = CallBuiltinFunc
		inf.Type = clTy
		return clTy
	}

	// 1. Check `Ident(...)` or `Ident[T...](...)` forms first (intrinsics, conversions, builtins, direct/generic fns)
	if id, isId := call.Callee.(*ast.IdentExpr); isId && !id.IsOpt {
		// Check disambiguation of `vec_of_fns[idx](args)` when parser tentatively parsed `[idx]` as TypeArgs.
		// The rewrite is idempotent and serialized: cached ASTs may be checked concurrently.
		astRewriteMu.Lock()
		rewritten := false
		if len(call.TypeArgs) == 1 {
			var idxExprArg ast.Expr
			var idxSpan diag.Span
			switch ta := call.TypeArgs[0].(type) {
			case *ast.NamedType:
				if ta.Module == "" && len(ta.TypeArgs) == 0 {
					idxExprArg = &ast.IdentExpr{Name: ta.Name, Span: ta.Span}
					idxSpan = ta.Span
				}
			case *ast.ConstArgType:
				idxExprArg = ta.Expr
				idxSpan = ta.Span
			}
			if idxExprArg != nil {
				if sym := fc.lookupVar(id.Name); sym != nil {
					switch sym.Type.(type) {
					case *types.Vector, *types.StackArray, *types.Map:
						idxExpr := &ast.IndexExpr{
							Base:  id,
							Index: idxExprArg,
							Span:  idxSpan,
						}
						call.Callee = idxExpr
						call.TypeArgs = nil
						rewritten = true
					}
				}
			}
		}
		astRewriteMu.Unlock()
		if rewritten {
			return fc.checkClosureValueCall(call)
		}

		// 1a. Check Intrinsics (`size_of`, `alloc_raw`, `ptr_read`, etc.) if not shadowed
		if fc.lookupVar(id.Name) == nil {
			if handled, retTy := fc.tryCheckIntrinsicCall(id.Name, call); handled {
				return retTy
			}
		}

		// 1b. Check Type Conversion or Owned Interface Box (`float(x)`, `u8(x)`, `UserID(x)`, `Shape(c)`)
		if fc.lookupVar(id.Name) == nil && len(call.TypeArgs) == 0 {
			if targetTy := fc.a.lookupTypeInModule(fc.mod, "", id.Name); targetTy != nil {
				return fc.checkTypeConversionOrBox(targetTy, call)
			}
			if fc.curFn != nil && fc.curFn.TypeSubst != nil {
				if subTy, ok := fc.curFn.TypeSubst[id.Name]; ok && subTy != nil {
					if _, isCV := subTy.(*types.ConstValue); !isCV {
						return fc.checkTypeConversionOrBox(subTy, call)
					}
				}
			}
		}

		// 1c. Check Generic Function (`identity[int](5)` or inferred `identity(5)`)
		if fc.lookupVar(id.Name) == nil {
			if genFn := fc.a.lookupGenFunc(fc.mod, "", id.Name); genFn != nil {
				return fc.checkGenericFuncCall(genFn, call, expected)
			}
		}

		// 1d. Check Direct / Extern / Local Closure Symbol
		if sym := fc.lookupVar(id.Name); sym != nil {
			if len(call.TypeArgs) > 0 {
				fc.a.Rep.Errorf(call.Span, "%q is not a generic function", id.Name)
			}
			fc.a.infoFor(id).Sym = sym
			fc.a.infoFor(id).Type = sym.Type
			if sym.ExternFn != nil {
				if fc.inConstContext() {
					fc.recordConstViolation(call.Span, fmt.Sprintf("cannot call `extern` C function %q in a `const` or `comptime` context", sym.Name), "move FFI calls out of `const` code or remove `const` if compile-time evaluation is not intended", nil)
				} else if fc.unsafeDepth == 0 {
					fc.a.Rep.Errorf(call.Span, "calling `extern` C function %q requires `unsafe`", sym.Name)
				}
				fc.checkSignatureArgs(sym.ExternFn.Sig.Params, call.Args, nil, call.Span)
				inf.CallKind = CallExternC
				inf.TargetExtern = sym.ExternFn
				inf.Type = sym.ExternFn.Sig.Ret
				return inf.Type
			}
			if sym.FuncInst != nil {
				fc.checkSignatureArgs(sym.FuncInst.Sig.Params, call.Args, nil, call.Span)
				inf.CallKind = CallDirectFunc
				fc.recordTargetFunc(inf, sym.FuncInst, call.Span)
				retTy := fc.a.ensureFuncRetResolved(sym.FuncInst, call.Span)
				inf.Type = retTy
				return retTy
			}
			return fc.checkClosureValueCall(call)
		}

		// 1e. Check Universe Built-in Functions (`print`, `println`, `panic`, `assert`, `parse_int`, etc.)
		if handled, retTy := fc.tryCheckBuiltinFunc(id.Name, call); handled {
			return retTy
		}

		hint, cands := fc.unknownNameHint(id.Name)
		fc.a.errorWithRename(id.Span, fmt.Sprintf("undeclared function %q", id.Name), id.Name, hint, cands)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	// 2. Check `MemberExpr` callee:
	//    - Module-qualified call (`mod.fn(...)`, `mod.GenericFn[T](...)`, `mod.Type(x)`)
	//    - Type-namespaced static/method call (`Arena.new()`, `Point.translate(&pt, 5, 5)`, `Pool[Node].new()`)
	//    - Instance UFCS dot-method call (`pt.translate(5, 5)`, `v.push(4)`, `opt?.to_string()`)
	//    - Struct field holding a closure (`handler.on_click(ev)`)
	if mem, isMem := call.Callee.(*ast.MemberExpr); isMem {
		return fc.checkMemberCallExpr(mem, call, expected)
	}

	// 3. Arbitrary expression callee (`get_adder()(1, 2)`)
	return fc.checkClosureValueCall(call)
}

func (fc *funcChecker) checkClosureValueCall(call *ast.CallExpr) types.Type {
	inf := fc.a.infoFor(call)
	calleeTy := fc.checkExpr(call.Callee, true)
	fnSig, ok := calleeTy.(*types.Func)
	if !ok {
		if calleeTy != types.TypeInvalid {
			fc.a.Rep.Errorf(call.Callee.GetSpan(), "cannot call non-function value of type %s", calleeTy.String())
		}
		for _, a := range call.Args {
			_ = fc.checkExpr(a, true)
		}
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if fnSig.Extern && fc.unsafeDepth == 0 {
		fc.a.Rep.ErrorWithHint(call.Span, "calling an `extern fn` value requires `unsafe`",
			"it may point to any C function; write `unsafe f(...)`")
	}
	fc.checkSignatureArgs(fnSig.Params, call.Args, nil, call.Span)
	inf.CallKind = CallClosureValue
	inf.Type = fnSig.Ret
	return fnSig.Ret
}

// -----------------------------------------------------------------------------
// Type Conversions & Interface Boxing
// -----------------------------------------------------------------------------

func (fc *funcChecker) checkTypeConversionOrBox(targetTy types.Type, call *ast.CallExpr) types.Type {
	inf := fc.a.infoFor(call)
	if len(call.Args) != 1 {
		fc.a.Rep.Errorf(call.Span, "type conversion `%s(...)` expects 1 argument, got %d", targetTy.String(), len(call.Args))
		inf.Type = targetTy
		return targetTy
	}
	arg := call.Args[0]

	// Owned Interface Box: `Shape(my_circle)` or `Printable(val)`
	if ifc, isIfc := targetTy.(*types.Interface); isIfc {
		if ifc.UsesSelf {
			fc.a.Rep.Errorf(call.Span, "interface %s uses `Self` and cannot be constructed as a boxed value", ifc.Name)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		argTy := fc.checkExpr(arg, true)
		if argTy != types.TypeInvalid {
			if argTy.Equals(ifc) {
				fc.consumeValue(arg, argTy, false)
			} else if fc.a.ImplementsInterface(argTy, ifc) {
				fc.a.RegisterVTable(argTy, ifc)
				fc.consumeValue(arg, argTy, false)
			} else {
				fc.a.Rep.Errorf(arg.GetSpan(), "type %s does not implement interface %s", argTy.String(), ifc.Name)
			}
		}
		inf.CallKind = CallInterfaceBox
		inf.BoxInterface = ifc
		inf.Type = ifc
		return ifc
	}

	// Distinct derived type conversion: `UserID(x)` or `int(my_id)`
	if dist, isDist := targetTy.(*types.Distinct); isDist {
		argTy := fc.checkExprExpect(arg, dist.Base, true)
		if argTy != types.TypeInvalid && !argTy.Equals(dist.Base) {
			// Also allow numeric conversion directly into Distinct numeric (`Port(8080)` where `8080: int` and `Port` wraps `u16`)!
			pTarget := types.UnderlyingPrimitive(dist.Base)
			pArg := types.UnderlyingPrimitive(argTy)
			if pTarget == nil || pArg == nil || !pTarget.IsNumeric() || !pArg.IsNumeric() {
				fc.a.Rep.Errorf(arg.GetSpan(), "cannot convert %s to %s (underlying type %s)", argTy.String(), dist.Name, dist.Base.String())
			}
		}
		fc.consumeValue(arg, dist.Base, false)
		inf.CallKind = CallTypeConvert
		inf.Type = dist
		return dist
	}

	// Primitive conversion: `float(x)`, `int(x)`, `u8(x)`, `char(x)`, `string(x)`
	var argTy types.Type
	if pTgt := types.UnderlyingPrimitive(targetTy); pTgt != nil && pTgt.Kind == types.PrimU64 {
		if il, isInt := arg.(*ast.IntLit); isInt && il.Val > math.MaxInt64 {
			argTy = fc.checkExprExpect(arg, targetTy, true)
		}
	}
	if argTy == nil {
		argTy = fc.checkExpr(arg, true)
	}
	if argTy == types.TypeInvalid {
		inf.CallKind = CallTypeConvert
		inf.Type = targetTy
		return targetTy
	}

	// Unwrapping a Distinct type back to its base (`int(user_id)`)
	if distArg, isDist := argTy.(*types.Distinct); isDist {
		if distArg.Base.Equals(targetTy) {
			fc.consumeValue(arg, targetTy, false)
			inf.CallKind = CallTypeConvert
			inf.Type = targetTy
			return targetTy
		}
	}

	primTarget, isPrim := targetTy.(*types.Primitive)
	if !isPrim {
		fc.a.Rep.Errorf(call.Span, "cannot use %s(...) as a conversion", targetTy.String())
		inf.Type = targetTy
		return targetTy
	}

	// Plain C-tag Enum -> Integer (`int(Status.Pending)`, `u8(Flags.Read)`)
	if en, isEn := argTy.(*types.Enum); isEn && !en.IsTagged && primTarget.IsInteger() {
		inf.CallKind = CallTypeConvert
		inf.Type = primTarget
		return primTarget
	}

	primArg := types.UnderlyingPrimitive(argTy)
	valid := false
	if primArg != nil {
		switch {
		case primTarget.IsNumeric() && primArg.IsNumeric():
			valid = true
		case primTarget.IsInteger() && primArg.Kind == types.PrimChar:
			valid = true
		case primTarget.Kind == types.PrimChar && primArg.IsInteger():
			valid = true
		case primTarget.IsInteger() && primArg.Kind == types.PrimBool:
			valid = true
		case primTarget.Kind == types.PrimString && primArg.Kind == types.PrimChar:
			valid = true
		}
	}
	if !valid {
		fc.a.Rep.Errorf(call.Span, "cannot convert %s to %s", argTy.String(), primTarget.String())
	}
	inf.CallKind = CallTypeConvert
	inf.Type = primTarget
	return primTarget
}

// -----------------------------------------------------------------------------
// Member / Method / Namespaced Calls
// -----------------------------------------------------------------------------

func (fc *funcChecker) checkMemberCallExpr(mem *ast.MemberExpr, call *ast.CallExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(call)
	fc.maybeRewriteIndexedMemberBase(mem)

	if fc.checkModuleOptChain(mem.Base, mem.Member, mem.QuestDot, mem.Span) {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	// 0. Check built-in compiler namespaces (`comp.*` for `@comptime_only` APIs, `type.*` for `const` type-layout queries)
	if baseId, isId := mem.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && !mem.QuestDot && len(mem.BaseTypeArgs) == 0 && fc.lookupVar(baseId.Name) == nil {
		if _, isMod := fc.mod.Aliased[baseId.Name]; !isMod {
			if baseId.Name == "comp" {
				return fc.checkCompNamespaceCall(mem, call)
			}
			if baseId.Name == "type" {
				return fc.checkTypeNamespaceCall(mem, call)
			}
		}
	}

	// 1. Check module-qualified calls (`mod.fn(...)`, `mod.GenericFn[T](...)`, `mod.Type(x)`)
	if baseId, isId := mem.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && !mem.QuestDot && len(mem.BaseTypeArgs) == 0 && fc.lookupVar(baseId.Name) == nil {
		if targetMod, isMod := fc.mod.Aliased[baseId.Name]; isMod {
			if fc.mod.DevAliased[baseId.Name] && !fc.isTestContext() {
				fc.a.Rep.Errorf(mem.Span, "cannot reference [dev-dependencies] package %q from non-test code", baseId.Name)
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			if !fc.a.SamePackage(fc.mod, targetMod) && !IsExported(mem.Member) {
				fc.a.Rep.Errorf(mem.Span, "cannot access unexported symbol %q from package %q", mem.Member, targetMod.Path)
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			if genFn, ok := targetMod.GenericFuncs[mem.Member]; ok {
				return fc.checkGenericFuncCall(genFn, call, expected)
			}
			if sym, ok := targetMod.Symbols[mem.Member]; ok {
				if sym.FuncInst != nil {
					fc.checkSignatureArgs(sym.FuncInst.Sig.Params, call.Args, nil, call.Span)
					inf.CallKind = CallDirectFunc
					fc.recordTargetFunc(inf, sym.FuncInst, call.Span)
					retTy := fc.a.ensureFuncRetResolved(sym.FuncInst, call.Span)
					inf.Type = retTy
					return retTy
				}
				if sym.ExternFn != nil {
					if fc.inConstContext() {
						fc.recordConstViolation(call.Span, fmt.Sprintf("cannot call `extern` C function %q in a `const` or `comptime` context", sym.Name), "move FFI calls out of `const` code or remove `const` if compile-time evaluation is not intended", nil)
					} else if fc.unsafeDepth == 0 {
						fc.a.Rep.Errorf(call.Span, "calling `extern` C function %q requires `unsafe`", sym.Name)
					}
					fc.checkSignatureArgs(sym.ExternFn.Sig.Params, call.Args, nil, call.Span)
					inf.CallKind = CallExternC
					inf.TargetExtern = sym.ExternFn
					inf.Type = sym.ExternFn.Sig.Ret
					return inf.Type
				}
			}
			if ty, ok := targetMod.Types[mem.Member]; ok && len(call.TypeArgs) == 0 {
				return fc.checkTypeConversionOrBox(ty, call)
			}
			cands := closestNames(mem.Member, fc.a.moduleExportNames(fc.mod, targetMod, true))
			hint := ""
			if len(cands) > 0 {
				hint = fmt.Sprintf("did you mean `%s.%s`?", baseId.Name, strings.Join(cands, "` or `"+baseId.Name+"."))
			}
			fc.a.errorWithRename(fc.a.focusSpan(mem), fmt.Sprintf("module %q has no function or type %q", baseId.Name, mem.Member), mem.Member, hint, cands)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	}

	// 2a. Check Enum tuple-payload variant constructors (`Result[int, string].Ok(42)`, `Result.Ok(42)`, `mod.Expr.Num(1)`)
	//     and Enum static/namespaced methods (`Color.from_name("red")`, `Result[int, string].default()`)!
	if en, genEn, matched := fc.tryResolveEnumMemberTarget(mem, expected); matched {
		return fc.checkEnumMemberCall(en, genEn, mem, call)
	}

	// 2b. Check Type-namespaced calls (`Arena.new()`, `Point.translate(&pt, 5, 5)`, `Pool[Node].new()`)
	if baseId, isId := mem.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && !mem.QuestDot && fc.lookupVar(baseId.Name) == nil {
		if genSt := fc.a.lookupGenStructInModule(fc.mod, "", baseId.Name); genSt != nil {
			return fc.checkGenericTypeAssocCall(genSt, mem.BaseTypeArgs, mem.Member, call, expected)
		}
		if recvTy := fc.a.lookupTypeInModule(fc.mod, "", baseId.Name); recvTy != nil {
			if len(mem.BaseTypeArgs) > 0 {
				fc.a.Rep.Errorf(mem.Span, "type %s is not generic", recvTy.String())
			}
			var mTypeArgs []types.Type
			for _, ta := range call.TypeArgs {
				mTypeArgs = append(mTypeArgs, fc.a.resolveValueType(fc.mod, ta, fc.subst, false))
			}
			fi := fc.a.LookupMethod(recvTy, mem.Member, mTypeArgs, call.Span)
			if fi == nil {
				hint, cands := fc.a.unknownMethodHint(recvTy, mem.Member)
				fc.a.errorWithRename(fc.a.focusSpan(mem), fmt.Sprintf("type %s has no method %q", recvTy.String(), mem.Member), mem.Member, hint, cands)
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			if !IsExported(mem.Member) && !fc.a.SamePackage(fc.mod, fi.Mod) && !fc.a.SamePackageDir(fc.mod, types.TypePkgDir(recvTy), "") {
				fc.a.Rep.Errorf(call.Span, "cannot call unexported method %q on type %s outside its package", mem.Member, recvTy.String())
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			// Namespaced call (`Point.translate(&pt, 5, 5)`): requires explicit `&` / `own` on arg 0!
			fc.checkSignatureArgs(fi.Sig.Params, call.Args, nil, call.Span)
			inf.CallKind = CallDirectFunc
			fc.recordTargetFunc(inf, fi, call.Span)
			retTy := fc.a.ensureFuncRetResolved(fi, call.Span)
			inf.Type = retTy
			return retTy
		}
	}

	// Check `mod.GenericStruct[T].method(...)` or `mod.Type.method(...)` when `mem.Base` is `mod.Type`!
	if baseMem, isBM := mem.Base.(*ast.MemberExpr); isBM && len(baseMem.BaseTypeArgs) == 0 {
		if fc.checkModuleOptChain(baseMem.Base, baseMem.Member, baseMem.QuestDot, baseMem.Span) {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if modId, isId := baseMem.Base.(*ast.IdentExpr); isId && !modId.IsOpt && fc.lookupVar(modId.Name) == nil {
			if targetMod, isMod := fc.mod.Aliased[modId.Name]; isMod {
				if fc.mod.DevAliased[modId.Name] && !fc.isTestContext() {
					fc.a.Rep.Errorf(baseMem.Span, "cannot reference [dev-dependencies] package %q from non-test code", modId.Name)
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
				memberName := baseMem.Member
				if baseMem.MemberHasOpt {
					memberName = strings.TrimSuffix(memberName, "?")
				}
				_, isGenType := targetMod.GenericStructs[memberName]
				recvTy, isNamedType := targetMod.Types[memberName]
				if isGenType || isNamedType {
					if !fc.a.SamePackage(fc.mod, targetMod) && !IsExported(memberName) {
						fc.a.Rep.Errorf(baseMem.Span, "cannot access unexported type %q from package %q", memberName, targetMod.Path)
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
					if mem.QuestDot || baseMem.MemberHasOpt {
						fc.a.Rep.Errorf(mem.Span, "cannot use optional chaining `?.` on type `%s.%s`", modId.Name, memberName)
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
					if genSt, ok := targetMod.GenericStructs[memberName]; ok {
						return fc.checkGenericTypeAssocCall(genSt, mem.BaseTypeArgs, mem.Member, call, expected)
					}
					var mTypeArgs []types.Type
					for _, ta := range call.TypeArgs {
						mTypeArgs = append(mTypeArgs, fc.a.resolveValueType(fc.mod, ta, fc.subst, false))
					}
					fi := fc.a.LookupMethod(recvTy, mem.Member, mTypeArgs, call.Span)
					if fi != nil {
						if !IsExported(mem.Member) && !fc.a.SamePackage(fc.mod, fi.Mod) && !fc.a.SamePackageDir(fc.mod, types.TypePkgDir(recvTy), "") {
							fc.a.Rep.Errorf(call.Span, "cannot call unexported method %q on type %s outside its package", mem.Member, recvTy.String())
							inf.Type = types.TypeInvalid
							return types.TypeInvalid
						}
						fc.checkSignatureArgs(fi.Sig.Params, call.Args, nil, call.Span)
						inf.CallKind = CallDirectFunc
						fc.recordTargetFunc(inf, fi, call.Span)
						retTy := fc.a.ensureFuncRetResolved(fi, call.Span)
						inf.Type = retTy
						return retTy
					}
				}
			}
		}
	}

	// 3. Instance method or function-valued struct field on `mem.Base`!
	recvTy := fc.checkExpr(mem.Base, true)
	if recvTy == types.TypeInvalid {
		for _, a := range call.Args {
			_ = fc.checkExpr(a, true)
		}
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	return fc.checkInstanceMethodCall(recvTy, mem, call, expected, false)
}

func (fc *funcChecker) checkInstanceMethodCall(recvTy types.Type, mem *ast.MemberExpr, call *ast.CallExpr, expected types.Type, inMatchPattern bool) types.Type {
	inf := fc.a.infoFor(call)

	if !inMatchPattern {
		// Check if `mem.Base` is a struct (or `ref[Struct]`) that has a function-typed field named `mem.Member`!
		innerStructTy := recvTy
		if rf, isRf := innerStructTy.(*types.Ref); isRf {
			innerStructTy = rf.Elem
		}
		if st, isSt := innerStructTy.(*types.Struct); isSt {
			if sf, _ := st.FieldByName(mem.Member); sf != nil {
				if !IsExported(mem.Member) && !fc.a.SamePackageDir(fc.mod, st.PkgDir, "") && !fc.a.SamePackage(fc.mod, fc.a.typeModule(st)) {
					fc.a.Rep.Errorf(mem.Span, "cannot access unexported field %q of struct %s outside its package", sf.Name, st.String())
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
				if _, isFn := sf.Type.(*types.Func); isFn {
					return fc.checkClosureValueCall(call)
				}
			}
		}
		var tup *types.Tuple
		if t, isTup := innerStructTy.(*types.Tuple); isTup {
			tup = t
		} else if d, isDist := innerStructTy.(*types.Distinct); isDist {
			if dtTup, isTup := d.Base.(*types.Tuple); isTup {
				tup = dtTup
			}
		}
		if tup != nil {
			if idx, ok := IsCanonicalTupleIndex(mem.Member); ok && idx >= 0 && idx < len(tup.Elems) {
				if _, isFn := tup.Elems[idx].(*types.Func); isFn {
					return fc.checkClosureValueCall(call)
				}
			}
		}
	}

	// Handle Optional Method Chaining (`user?.full_name()`, `acc.nick?.len()`, `s?.trim().len()`, `ages["Alice"]?.to_string()`)!
	isOptChain := false
	actualRecvTy := recvTy
	if optR, isOpt := recvTy.(*types.Optional); isOpt {
		hasVisualQ := mem.QuestDot || fc.a.infoFor(mem.Base).IsOptChain
		switch bv := mem.Base.(type) {
		case *ast.IdentExpr:
			if bv.IsOpt {
				hasVisualQ = true
			}
		case *ast.MemberExpr:
			if bv.MemberHasOpt {
				hasVisualQ = true
			}
		}
		if !hasVisualQ {
			fc.a.Rep.ErrorWithHint(
				mem.Span,
				fmt.Sprintf("cannot call `.%s(...)` on optional value of type %s without `?`", mem.Member, recvTy.String()),
				fmt.Sprintf("write `?.%s(...)` to short-circuit to `none`", mem.Member),
			)
		}
		isOptChain = true
		actualRecvTy = optR.Elem
	} else if mem.QuestDot {
		fc.a.Rep.Report(diag.Diagnostic{
			Severity: diag.Error,
			Span:     mem.Span,
			Full:     mem.Span,
			Message:  fmt.Sprintf("`?.` cannot be used on non-optional type %s", recvTy.String()),
			Hint:     "remove `?` and use `.`",
			Data:     map[string]string{"kind": "drop_quest_dot"},
		})
	}

	// Unwrap `ref[T]` when calling methods on an arena-allocated value (`node_ref.sum()`)!
	isArenaRef := false
	if rf, isRf := actualRecvTy.(*types.Ref); isRf {
		isArenaRef = true
		actualRecvTy = rf.Elem
	}

	// 3a. Interface dynamic dispatch (`shape.area()`, `shape.scale(2.0)`)
	if ifc, isIfc := actualRecvTy.(*types.Interface); isIfc {
		return fc.checkInterfaceMethodCall(ifc, mem, call, isOptChain, inMatchPattern)
	}

	// 3b. User-defined or Generic Method (`bind Point.length`, `bind Pair[A, B].swap`, `bind UserID.to_string`)
	var mTypeArgs []types.Type
	gtmpl := fc.a.lookupGenericMethodTemplate(actualRecvTy, mem.Member)
	if len(call.TypeArgs) > 0 {
		if gtmpl != nil && gtmpl.MethodDecl != nil && len(gtmpl.MethodDecl.TypeParams) > 0 {
			resolved, ok := fc.a.resolveGenericArgs(fc.mod, gtmpl.Mod, gtmpl.MethodDecl.TypeParams, call.TypeArgs, fc.subst, call.Span, "method", mem.Member)
			if !ok {
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			mTypeArgs = resolved
		} else {
			for _, ta := range call.TypeArgs {
				mTypeArgs = append(mTypeArgs, fc.a.resolveValueType(fc.mod, ta, fc.subst, false))
			}
		}
	}
	if len(mTypeArgs) == 0 {
		if gtmpl != nil && gtmpl.MethodDecl != nil && len(gtmpl.MethodDecl.TypeParams) > 0 {
			decl := gtmpl.MethodDecl
			if decl.RecvMode != ast.ReceiverMode(-1) && len(call.Args) == len(decl.Params) {
				tpNames := make(map[string]bool)
				for _, tp := range decl.TypeParams {
					tpNames[tp.Name] = true
				}
				inferred := make(map[string]types.Type)
				for i, arg := range call.Args {
					probe := arg
					if u, ok := arg.(*ast.UnaryExpr); ok && (u.Op == token.Amp || u.Op == token.KwOwn) {
						probe = u.Operand
					}
					if _, isNone := probe.(*ast.NoneLit); isNone {
						continue
					}
					savedMoves := fc.cloneMoves()
					fc.probing++
					aty := fc.checkExpr(probe, true)
					fc.probing--
					fc.moves = savedMoves
					matchTypeExprForInference(decl.Params[i].Type, aty, tpNames, inferred)
				}
				if expected != nil && decl.Ret != nil {
					matchTypeExprForInference(decl.Ret, expected, tpNames, inferred)
				}
				candArgs := make([]types.Type, len(decl.TypeParams))
				allFound := true
				for i, tp := range decl.TypeParams {
					ta, ok := inferred[tp.Name]
					if !ok {
						allFound = false
						break
					}
					validTa, ok2 := validateInferredConstArg(fc.a, gtmpl.Mod, decl.TypeParams, tp, ta, call.Span)
					if !ok2 {
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
					candArgs[i] = validTa
				}
				if allFound {
					mTypeArgs = candArgs
				}
			}
		}
	}
	if !fc.a.checkCollMethodElems(actualRecvTy, mem.Member, call.Span) {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if fi := fc.a.LookupMethod(actualRecvTy, mem.Member, mTypeArgs, call.Span); fi != nil && !fi.IsStaticAssoc {
		if !IsExported(mem.Member) && !fc.a.SamePackage(fc.mod, fi.Mod) && !fc.a.SamePackageDir(fc.mod, types.TypePkgDir(actualRecvTy), "") {
			fc.a.Rep.Errorf(call.Span, "cannot call unexported method %q on type %s outside its package", mem.Member, actualRecvTy.String())
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		recvMode := fi.Sig.Params[0].Mode
		if isOptChain && recvMode != types.ModeRead {
			fc.a.Rep.Errorf(call.Span, "cannot call mutating or consuming method %q via optional `?.` chain", mem.Member)
		}
		var mutRecvRoot *Symbol
		if inMatchPattern {
			if recvMode != types.ModeRead {
				modeStr := "&self"
				if recvMode == types.ModeOwn {
					modeStr = "own self"
				}
				fc.a.Rep.Errorf(call.Span, "method `%s.%s` in `match` pattern must take read-only `self` (got `%s`)", actualRecvTy.String(), mem.Member, modeStr)
			} else {
				fc.markViewIfApplicable(mem.Base, actualRecvTy)
			}
		} else {
			switch recvMode {
			case types.ModeMut:
				if !isArenaRef {
					mutRecvRoot = fc.checkMutablePlace(mem.Base, fmt.Sprintf("call `&self` method `%s` on", mem.Member))
				}
			case types.ModeOwn:
				if isArenaRef {
					fc.a.Rep.Errorf(call.Span, "cannot call consuming `own self` method %q through arena `ref[%s]`", mem.Member, actualRecvTy.String())
				} else {
					fc.consumeValue(mem.Base, actualRecvTy, false)
				}
			case types.ModeRead:
				fc.markViewIfApplicable(mem.Base, actualRecvTy)
			}
		}

		fc.checkSignatureArgs(fi.Sig.Params[1:], call.Args, mutRecvRoot, call.Span)
		if types.IsArena(actualRecvTy) && !inMatchPattern {
			fc.checkArenaMethodRules(mem, call, mutRecvRoot)
		}
		if !inMatchPattern && recvMode == types.ModeMut && !types.IsArena(actualRecvTy) {
			// (An arena's own methods follow the arena rules above instead.)
			fc.checkMutatingMethodRecvEscape(mem.Base, call.Args, call.Span)
		}
		inf.CallKind = CallDirectFunc
		fc.recordTargetFunc(inf, fi, call.Span)
		inf.MethodRecv = mem.Base
		inf.MethodRecvMode = recvMode
		inf.IsOptChain = isOptChain
		retTy := fc.a.ensureFuncRetResolved(fi, call.Span)
		if isOptChain && retTy != types.TypeInvalid && !retTy.Equals(types.TypeVoid) {
			retTy = types.NewOptional(retTy)
		}
		inf.Type = retTy
		return retTy
	}

	// 3c. Built-in Methods (`.copy()`, `.to_string()`, `string.*`, `char.*`, `vec.*`, `set.*`, `map.*`, `Arena.*`)
	return fc.checkBuiltinMethodCall(actualRecvTy, isArenaRef, isOptChain, inMatchPattern, mem, call)
}

// checkArenaMethodRules applies the rules that keep `ref[T]`s from outliving their `Arena` to a
// call of the std/core `Arena` method `mem` (after its arguments are checked): an arena can't be
// allocated into itself, an `alloc` pins the arena for the current scope, and `reset` is refused
// on a borrowed arena or while references from this scope may be live.
func (fc *funcChecker) checkArenaMethodRules(mem *ast.MemberExpr, call *ast.CallExpr, mutRecv *Symbol) {
	sym := mutRecv
	if sym == nil {
		sym = fc.rootSymbolOfExpr(mem.Base)
	}
	switch mem.Member {
	case "alloc":
		if sym != nil && len(call.Args) == 1 {
			if rr := fc.rootSymbolOfExpr(call.Args[0]); rr == sym {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "cannot pass arena %q into its own `alloc`", sym.Name)
			}
		}
		if sym != nil && sym.BindKind == BindOwned {
			fc.pinArenaAt(sym, fc.curDepth())
		}
	case "reset":
		if sym != nil && sym.BindKind != BindOwned {
			fc.a.Rep.Errorf(call.Span, "cannot call `.reset()` on borrowed `&Arena` parameter %q (caller may hold live `ref[T]` allocations)", sym.Name)
		} else if sym != nil && fc.isArenaPinned(sym) {
			fc.a.Rep.ErrorWithHint(
				call.Span,
				fmt.Sprintf("cannot call `.reset()` on `Arena` %q while `ref[T]` allocations from this scope may still be live", sym.Name),
				"wrap the phase that allocates `ref[T]` in a `{ ... }` block so references go out of scope before calling `.reset()`",
			)
		}
	}
}

func (fc *funcChecker) checkEnumMemberCall(en *types.Enum, genEn *GenericEnumTemplate, mem *ast.MemberExpr, call *ast.CallExpr) types.Type {
	inf := fc.a.infoFor(call)

	// If generic enum wasn't instantiated yet (no explicit `Enum[...]` and no matching expected type),
	// try inferring type parameters from a tuple-payload variant constructor call `Enum.Variant(arg)`!
	if en == nil && genEn != nil {
		if len(mem.BaseTypeArgs) > 0 {
			for _, a := range call.Args {
				_ = fc.checkExpr(a, true)
			}
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		var declVar *ast.EnumVariant
		for i := range genEn.Decl.Variants {
			if genEn.Decl.Variants[i].Name == mem.Member {
				declVar = &genEn.Decl.Variants[i]
				break
			}
		}
		if declVar == nil {
			fc.a.Rep.Errorf(mem.Span, "cannot infer generic type arguments for `%s.%s(...)`; write `%s[...].%s(...)`",
				genEn.Decl.Name, mem.Member, genEn.Decl.Name, mem.Member)
			for _, a := range call.Args {
				_ = fc.checkExpr(a, true)
			}
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if declVar.PayloadType == nil {
			fc.a.Rep.Errorf(call.Span, "variant `%s.%s` has no payload; write `%s[...].%s` without `()`",
				genEn.Decl.Name, declVar.Name, genEn.Decl.Name, declVar.Name)
			for _, a := range call.Args {
				_ = fc.checkExpr(a, true)
			}
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "variant `%s.%s(...)` expects 1 payload argument, got %d",
				genEn.Decl.Name, declVar.Name, len(call.Args))
			for _, a := range call.Args {
				_ = fc.checkExpr(a, true)
			}
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}

		tpNames := make(map[string]bool, len(genEn.Decl.TypeParams))
		for _, tp := range genEn.Decl.TypeParams {
			tpNames[tp.Name] = true
		}
		inferred := make(map[string]types.Type)
		if _, isNone := call.Args[0].(*ast.NoneLit); !isNone {
			savedMoves := fc.cloneMoves()
			aty := fc.checkExpr(call.Args[0], true)
			fc.moves = savedMoves
			matchTypeExprForInference(declVar.PayloadType, aty, tpNames, inferred)
		}
		targs := make([]types.Type, len(genEn.Decl.TypeParams))
		for i, tp := range genEn.Decl.TypeParams {
			ta, ok := inferred[tp.Name]
			if !ok {
				fc.a.Rep.Errorf(call.Span, "cannot infer type parameter %s for generic enum %s; specify `%s[...].%s(...)` or add a type annotation",
					tp.Name, genEn.Decl.Name, genEn.Decl.Name, declVar.Name)
				_ = fc.checkExpr(call.Args[0], true)
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			validTa, ok2 := validateInferredConstArg(fc.a, genEn.Mod, genEn.Decl.TypeParams, tp, ta, call.Span)
			if !ok2 {
				_ = fc.checkExpr(call.Args[0], true)
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			targs[i] = validTa
		}
		en = fc.a.instantiateEnum(genEn, targs, mem.Span)
	}

	if en == nil {
		for _, a := range call.Args {
			_ = fc.checkExpr(a, true)
		}
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	if v, isVar := en.VariantByName(mem.Member); isVar {
		if en.IsComptimeOnly && !fc.inComptimeOnlyContext() {
			fc.a.Rep.ErrorWithHint(
				call.Span,
				fmt.Sprintf("cannot use `@comptime_only` enum `%s.%s` outside a compile-time context", en.Name, mem.Member),
				"use it inside `comptime`, a `const` initializer, or another `@comptime_only` function",
			)
		}
		if !IsExported(mem.Member) && !fc.a.SamePackageDir(fc.mod, en.PkgDir, "") && !fc.a.SamePackage(fc.mod, fc.a.typeModule(en)) {
			fc.a.Rep.Errorf(call.Span, "cannot access unexported enum variant `%s.%s` outside its package", en.Name, mem.Member)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if en.Deprecated {
			msg := ""
			if en.DeprecatedMsg != "" {
				msg = ": " + en.DeprecatedMsg
			}
			fc.a.Rep.Warnf(call.Span, "use of deprecated enum %q%s", en.Name, msg)
		}
		if len(call.TypeArgs) > 0 {
			fc.a.Rep.ErrorWithHint(
				call.Span,
				fmt.Sprintf("enum variant `%s.%s` does not take method type arguments", en.Name, v.Name),
				fmt.Sprintf("place type arguments on the enum name: `%s[...].%s(...)`", en.Name, v.Name),
			)
		}
		switch v.Kind {
		case types.VariantUnit:
			fc.a.Rep.Report(diag.Diagnostic{
				Severity: diag.Error,
				Span:     call.Span,
				Full:     call.Span,
				Message:  fmt.Sprintf("variant `%s.%s` has no payload; write `%s.%s` without `()`", en.String(), v.Name, en.Name, v.Name),
				Hint:     "unit enum variants take no arguments; remove `()`",
				Data:     map[string]string{"kind": "remove_parens"},
			})
			for _, a := range call.Args {
				_ = fc.checkExpr(a, true)
			}
			inf.Type = en
			return en
		case types.VariantTuple:
			if len(call.Args) != 1 {
				fc.a.Rep.Errorf(call.Span, "variant `%s.%s(...)` expects 1 payload argument of type %s, got %d",
					en.String(), v.Name, v.Payload.String(), len(call.Args))
				for _, a := range call.Args {
					_ = fc.checkExpr(a, true)
				}
				inf.Type = en
				return en
			}
			arg := call.Args[0]
			aty := fc.checkExprExpect(arg, v.Payload, true)
			fc.verifyAssignable(aty, v.Payload, arg, arg.GetSpan(), fmt.Sprintf("%s.%s payload", en.Name, v.Name))
			fc.consumeValue(arg, v.Payload, false)
			memInf := fc.a.infoFor(mem)
			memInf.EnumType = en
			memInf.EnumVariant = v
			memInf.Type = en
			inf.CallKind = CallEnumVariant
			inf.EnumType = en
			inf.EnumVariant = v
			inf.Type = en
			return en
		}
	}

	// Not a variant name: check static or namespaced method bound on `en`!
	var mTypeArgs []types.Type
	for _, ta := range call.TypeArgs {
		mTypeArgs = append(mTypeArgs, fc.a.resolveValueType(fc.mod, ta, fc.subst, false))
	}
	fi := fc.a.LookupMethod(en, mem.Member, mTypeArgs, call.Span)
	if fi == nil {
		cands := closestNames(mem.Member, fc.a.enumMemberNames(fc.mod, en, true))
		hint := ""
		if len(cands) > 0 {
			hint = fmt.Sprintf("did you mean `%s.%s`?", en.Name, strings.Join(cands, "` or `"+en.Name+"."))
		}
		fc.a.errorWithRename(fc.a.focusSpan(mem), fmt.Sprintf("enum %s has no variant or method %q", en.String(), mem.Member), mem.Member, hint, cands)
		for _, a := range call.Args {
			_ = fc.checkExpr(a, true)
		}
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if !IsExported(mem.Member) && !fc.a.SamePackage(fc.mod, fi.Mod) && !fc.a.SamePackageDir(fc.mod, en.PkgDir, "") {
		fc.a.Rep.Errorf(call.Span, "cannot call unexported method %q on enum %s outside its package", mem.Member, en.String())
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	fc.checkSignatureArgs(fi.Sig.Params, call.Args, nil, call.Span)
	inf.CallKind = CallDirectFunc
	fc.recordTargetFunc(inf, fi, call.Span)
	retTy := fc.a.ensureFuncRetResolved(fi, call.Span)
	inf.Type = retTy
	return retTy
}

func (fc *funcChecker) checkGenericTypeAssocCall(genSt *GenericStructTemplate, recvTypeArgs []ast.TypeExpr, method string, call *ast.CallExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(call)
	var targs []types.Type
	var mTypeArgs []types.Type

	expTarget := expected
	if opt, ok := expTarget.(*types.Optional); ok {
		expTarget = opt.Elem
	}

	resolveMethodArgs := func(args []ast.TypeExpr) ([]types.Type, bool) {
		if len(args) == 0 {
			return nil, true
		}
		if gset, ok := fc.a.genMethods[genSt.Decl.Name]; ok {
			if gtmpl, ok2 := gset[method]; ok2 && gtmpl != nil && gtmpl.MethodDecl != nil && len(gtmpl.MethodDecl.TypeParams) > 0 {
				return fc.a.resolveGenericArgs(fc.mod, gtmpl.Mod, gtmpl.MethodDecl.TypeParams, args, fc.subst, call.Span, "method", method)
			}
		}
		out := make([]types.Type, len(args))
		for i, ta := range args {
			out[i] = fc.a.resolveValueType(fc.mod, ta, fc.subst, false)
			if out[i] == types.TypeInvalid {
				return nil, false
			}
		}
		return out, true
	}

	if len(recvTypeArgs) > 0 {
		resolvedRecv, ok := fc.a.resolveGenericArgs(fc.mod, genSt.Mod, genSt.Decl.TypeParams, recvTypeArgs, fc.subst, call.Span, "generic type", genSt.Decl.Name)
		if !ok {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		targs = resolvedRecv
		var okM bool
		mTypeArgs, okM = resolveMethodArgs(call.TypeArgs)
		if !okM {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	} else if expSt, ok := expTarget.(*types.Struct); ok && expSt.Name == genSt.Decl.Name && len(expSt.TypeArgs) == len(genSt.Decl.TypeParams) {
		targs = expSt.TypeArgs
		var okM bool
		mTypeArgs, okM = resolveMethodArgs(call.TypeArgs)
		if !okM {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	} else if len(call.TypeArgs) == len(genSt.Decl.TypeParams) {
		resolvedRecv, ok := fc.a.resolveGenericArgs(fc.mod, genSt.Mod, genSt.Decl.TypeParams, call.TypeArgs, fc.subst, call.Span, "generic type", genSt.Decl.Name)
		if !ok {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		targs = resolvedRecv
	} else {
		fc.a.Rep.Errorf(call.Span, "generic type %s expects %d type argument(s); write `%s[...].%s(...)`",
			genSt.Decl.Name, len(genSt.Decl.TypeParams), genSt.Decl.Name, method)
		for _, a := range call.Args {
			_ = fc.checkExpr(a, true)
		}
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	instSt := fc.a.instantiateStruct(genSt, targs, call.Span)
	fi := fc.a.LookupMethod(instSt, method, mTypeArgs, call.Span)
	if fi == nil {
		fc.a.Rep.Errorf(call.Span, "type %s has no associated function %q", instSt.String(), method)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if !IsExported(method) && !fc.a.SamePackage(fc.mod, fi.Mod) && !fc.a.SamePackageDir(fc.mod, instSt.PkgDir, "") {
		fc.a.Rep.Errorf(call.Span, "cannot call unexported associated function %q on type %s outside its package", method, instSt.String())
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	fc.checkSignatureArgs(fi.Sig.Params, call.Args, nil, call.Span)
	inf.CallKind = CallDirectFunc
	fc.recordTargetFunc(inf, fi, call.Span)
	retTy := fc.a.ensureFuncRetResolved(fi, call.Span)
	inf.Type = retTy
	return retTy
}

func (fc *funcChecker) checkGenericFuncCall(genFn *GenericFuncTemplate, call *ast.CallExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(call)
	decl := genFn.VarDecl
	if !IsExported(decl.Name) && !fc.a.SamePackage(fc.mod, genFn.Mod) {
		fc.a.Rep.Errorf(call.Span, "cannot access unexported generic function %q from package %q", decl.Name, genFn.Mod.Path)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	var targs []types.Type

	if len(call.TypeArgs) > 0 {
		resolvedArgs, ok := fc.a.resolveGenericArgs(fc.mod, genFn.Mod, decl.TypeParams, call.TypeArgs, fc.subst, call.Span, "generic function", decl.Name)
		if !ok {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		targs = resolvedArgs
	} else {
		// Infer type arguments from call arguments (and expected return type if needed)!
		fl := decl.Value.(*ast.FuncLit)
		if len(call.Args) != len(fl.Params) {
			fc.a.Rep.Errorf(call.Span, "function %q expects %d argument(s), got %d", decl.Name, len(fl.Params), len(call.Args))
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		tpNames := make(map[string]bool)
		for _, tp := range decl.TypeParams {
			tpNames[tp.Name] = true
		}
		inferred := make(map[string]types.Type)
		for i, arg := range call.Args {
			// Unwrap `&arg` or `own arg` solely to inspect argument's base type during type-param inference
			probe := arg
			if u, ok := arg.(*ast.UnaryExpr); ok && (u.Op == token.Amp || u.Op == token.KwOwn) {
				probe = u.Operand
			}
			if _, isNone := probe.(*ast.NoneLit); isNone {
				continue
			}
			// Check probe without committing moves yet by saving/restoring move map!
			savedMoves := fc.cloneMoves()
			fc.probing++
			aty := fc.checkExpr(probe, true)
			fc.probing--
			fc.moves = savedMoves
			matchTypeExprForInference(fl.Params[i].Type, aty, tpNames, inferred)
		}
		if expected != nil && fl.Ret != nil {
			matchTypeExprForInference(fl.Ret, expected, tpNames, inferred)
		}
		targs = make([]types.Type, len(decl.TypeParams))
		for i, tp := range decl.TypeParams {
			ta, ok := inferred[tp.Name]
			if !ok {
				fc.a.Rep.Errorf(call.Span, "cannot infer type parameter %s for %q; provide `%s[...]` explicitly",
					tp.Name, decl.Name, decl.Name)
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			validTa, ok2 := validateInferredConstArg(fc.a, genFn.Mod, decl.TypeParams, tp, ta, call.Span)
			if !ok2 {
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			targs[i] = validTa
		}
	}

	fi := fc.a.instantiateGenericFunc(genFn, targs, call.Span)
	if fi == nil {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	fc.checkSignatureArgs(fi.Sig.Params, call.Args, nil, call.Span)
	inf.CallKind = CallDirectFunc
	fc.recordTargetFunc(inf, fi, call.Span)
	retTy := fc.a.ensureFuncRetResolved(fi, call.Span)
	inf.Type = retTy
	return retTy
}

func (fc *funcChecker) checkInterfaceMethodCall(ifc *types.Interface, mem *ast.MemberExpr, call *ast.CallExpr, isOptChain, inMatchPattern bool) types.Type {
	inf := fc.a.infoFor(call)
	var found *types.InterfaceMethod
	for i := range ifc.Methods {
		if ifc.Methods[i].Name == mem.Member {
			found = &ifc.Methods[i]
			break
		}
	}
	if found == nil {
		// Built-in `.copy()` on any owned interface!
		if mem.Member == "copy" && len(call.Args) == 0 {
			inf.CallKind = CallBuiltinMethod
			inf.BuiltinName = "Interface.copy"
			inf.MethodRecv = mem.Base
			inf.IsOptChain = isOptChain
			ret := types.Type(ifc)
			if isOptChain {
				ret = types.NewOptional(ifc)
			}
			inf.Type = ret
			return ret
		}
		fc.a.Rep.Errorf(mem.Span, "interface %s has no method %q", ifc.Name, mem.Member)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	if !IsExported(mem.Member) {
		ifcMod := fc.a.Prog.InterfaceMods[ifc]
		samePkg := false
		if ifcMod != nil && fc.a.SamePackage(fc.mod, ifcMod) {
			samePkg = true
		} else if ifc.PkgDir != "" && fc.a.SamePackageDir(fc.mod, ifc.PkgDir, "") {
			samePkg = true
		}
		if !samePkg {
			fc.a.Rep.Errorf(call.Span, "cannot call unexported interface method %q outside its package", mem.Member)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	}

	var mutRoot *Symbol
	if found.RecvMode == types.ModeMut {
		if inMatchPattern {
			fc.a.Rep.Errorf(call.Span, "method `%s.%s` in `match` pattern must take read-only `self` (got `&self`)", ifc.Name, mem.Member)
		} else {
			if isOptChain {
				fc.a.Rep.Errorf(call.Span, "cannot call `&self` method %q via optional `?.` chain", mem.Member)
			}
			mutRoot = fc.checkMutablePlace(mem.Base, fmt.Sprintf("call `&self` method `%s` on", mem.Member))
		}
	}
	fc.checkSignatureArgs(found.Params, call.Args, mutRoot, call.Span)
	inf.CallKind = CallInterfaceMethod
	inf.InterfaceMeth = found
	inf.MethodRecv = mem.Base
	inf.MethodRecvMode = found.RecvMode
	inf.IsOptChain = isOptChain
	ret := found.Ret
	if isOptChain && !ret.Equals(types.TypeVoid) {
		ret = types.NewOptional(ret)
	}
	inf.Type = ret
	return ret
}

// -----------------------------------------------------------------------------
// Signature Argument Checking, `&` / `own` Enforcement, & Borrow Exclusivity
// -----------------------------------------------------------------------------

// arityHint names the parameters a call is missing, or says how many arguments to remove.
func arityHint(params []types.FuncParam, got int) string {
	if got > len(params) {
		if len(params) == 0 {
			return "this takes no arguments; remove them"
		}
		return fmt.Sprintf("remove %d argument(s); the parameters are (%s)", got-len(params), formatParams(params))
	}
	return fmt.Sprintf("missing %s", "`"+formatParams(params[got:])+"`")
}

func formatParams(params []types.FuncParam) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = fmt.Sprintf("%s: %s%s", p.Name, p.Mode.Prefix(), p.Type.String())
	}
	return strings.Join(parts, ", ")
}

func (fc *funcChecker) checkSignatureArgs(params []types.FuncParam, args []ast.Expr, receiverMutRoot *Symbol, callSpan diag.Span) {
	if len(args) != len(params) {
		fc.a.Rep.ErrorWithHint(callSpan, fmt.Sprintf("expected %d argument(s), got %d", len(params), len(args)), arityHint(params, len(args)))
		for _, a := range args {
			_ = fc.checkExpr(a, true)
		}
		return
	}

	type mutEntry struct {
		sym  *Symbol
		span diag.Span
	}
	var mutRoots []mutEntry
	if receiverMutRoot != nil {
		mutRoots = append(mutRoots, mutEntry{sym: receiverMutRoot, span: callSpan})
	}

	type readEntry struct {
		sym         *Symbol
		isPlainCopy bool
		span        diag.Span
	}
	var readRoots []readEntry

	for i, param := range params {
		arg := args[i]
		switch param.Mode {
		case types.ModeMut:
			// Mandatory `&` prefix at direct call sites (Spec §5.2 / §6.3)!
			u, isAmp := arg.(*ast.UnaryExpr)
			if !isAmp || u.Op != token.Amp {
				fc.a.Rep.ErrorWithHint(
					arg.GetSpan(),
					fmt.Sprintf("argument %d (`%s`) borrows mutably (`&%s`) and requires `&` at the call site", i+1, param.Name, param.Type.String()),
					"prefix the argument with `&` so the mutation is visible at the call site",
				)
				_ = fc.checkExpr(arg, true)
				continue
			}
			// Check the inner operand of `&operand`!
			aty := fc.checkExpr(u.Operand, true)
			fc.a.infoFor(arg).Type = aty
			if aty != types.TypeInvalid && !aty.Equals(param.Type) {
				if _, isArr := aty.(*types.StackArray); isArr {
					if _, isVec := param.Type.(*types.Vector); isVec {
						fc.a.Rep.Errorf(arg.GetSpan(), "cannot pass fixed array %s to mutable list `&%s` (only read-only `%s` accepts array views)",
							aty.String(), param.Type.String(), param.Type.String())
						continue
					}
					if arrSt, isArrSt := fc.a.isArrayViewStruct(param.Type); isArrSt {
						fc.a.Rep.Errorf(arg.GetSpan(), "cannot pass fixed array %s to mutable `&%s` (only read-only `%s` accepts array views)",
							aty.String(), arrSt.String(), arrSt.String())
						continue
					}
				}
				fc.verifyAssignable(aty, param.Type, u.Operand, arg.GetSpan(), fmt.Sprintf("mutable argument %d", i+1))
			}
			mroot := fc.checkMutablePlace(u.Operand, "mutably borrow")
			if mroot != nil {
				mutRoots = append(mutRoots, mutEntry{sym: mroot, span: arg.GetSpan()})
			}

		case types.ModeOwn:
			aty := fc.checkExprExpect(arg, param.Type, true)
			fc.verifyAssignable(aty, param.Type, arg, arg.GetSpan(), fmt.Sprintf("argument %d", i+1))
			if r := fc.rootSymbolOfExpr(arg); r != nil {
				readRoots = append(readRoots, readEntry{sym: r, isPlainCopy: param.Type.IsPlain(), span: arg.GetSpan()})
			}
			fc.consumeValue(arg, param.Type, true)

		case types.ModeRead:
			if u, isU := arg.(*ast.UnaryExpr); isU && u.Op == token.Amp {
				fc.a.Rep.ErrorWithHint(
					arg.GetSpan(),
					fmt.Sprintf("unexpected `&` on read-only parameter %d (`%s: %s`)", i+1, param.Name, param.Type.String()),
					"parameters borrow read-only by default; omit `&`",
				)
				_ = fc.checkExpr(u.Operand, true)
				continue
			}
			// Allow `[N: T]` stack arrays to pass as read-only `[T]` contiguous sequence views (Decision B9)!
			aty := fc.checkReadOnlyArg(arg, param.Type, i+1)
			if r := fc.rootSymbolOfExpr(arg); r != nil {
				readRoots = append(readRoots, readEntry{sym: r, isPlainCopy: aty != nil && aty.IsPlain(), span: arg.GetSpan()})
			}
		}
	}

	// Enforce Borrow Exclusivity Rule C5:
	// 1. Two `&` borrows of the same root variable (`foo(&x, &x)`) -> Error!
	for i := 0; i < len(mutRoots); i++ {
		for j := i + 1; j < len(mutRoots); j++ {
			if mutRoots[i].sym == mutRoots[j].sym {
				fc.a.Rep.Errorf(mutRoots[j].span, "cannot borrow %q mutably more than once in the same call", mutRoots[i].sym.Name)
			}
		}
	}
	// 2. `&x` alongside a read/move of `x` (`foo(&x, x)` or `list.push(list[0])` on heap elements) -> Error!
	// Note per C5: `list.push(list[0])` IS allowed when `list[0]` is a Plain copy (evaluated before `push` runs),
	// whereas `foo(&x, x)` where `x` is borrowed as a view or `list.push(list[0])` on Resource elements is an error!
	for _, mr := range mutRoots {
		for _, rr := range readRoots {
			if mr.sym == rr.sym && !rr.isPlainCopy {
				fc.a.Rep.Errorf(rr.span, "cannot read or borrow %q while it is also mutably borrowed (`&%s`) in the same call",
					mr.sym.Name, mr.sym.Name)
			}
		}
		// Arena escape check: prevent storing a `ref[T]` from an inner Arena into an outer mutable container (`escaped.push(r)`)!
		hasRefBearingArg := false
		for i, arg := range args {
			ad := fc.arenaDepthOfExpr(arg)
			if i < len(params) && params[i].Mode == types.ModeMut && types.IsArena(params[i].Type) {
				if asym := fc.rootSymbolOfExpr(arg); asym != nil && asym.BindKind == BindOwned && asym.ScopeDepth > ad {
					ad = asym.ScopeDepth
				}
				hasRefBearingArg = true
			} else if i < len(params) && typeMayHoldArenaRef(params[i].Type) {
				hasRefBearingArg = true
			}
			if ad > 0 && mr.sym.ScopeDepth < ad && !types.IsArena(mr.sym.Type) {
				fc.a.Rep.Errorf(arg.GetSpan(), "arena reference `ref[T]` cannot escape into %q declared in an outer scope", mr.sym.Name)
			}
		}
		if hasRefBearingArg && !types.IsArena(mr.sym.Type) && typeMayHoldArenaRef(mr.sym.Type) {
			destDepth := mr.sym.ScopeDepth
			if destDepth < 1 {
				destDepth = 1
			}
			fc.lowerPinnedArenasTo(destDepth)
		}
	}

	// Pin owned `&Arena` arguments whenever any sibling parameter (or mutable receiver) may store `ref[T]`,
	// and forbid passing an inner-arena `ref[T]` (or `&Arena`) alongside an outer interior-mutable `ref[Node]`!
	otherMayHoldRef := receiverMutRoot != nil && typeMayHoldArenaRef(receiverMutRoot.Type)
	for _, p := range params {
		if !types.IsArena(p.Type) && typeMayHoldArenaRef(p.Type) {
			otherMayHoldRef = true
			break
		}
	}
	if otherMayHoldRef {
		for i, p := range params {
			if p.Mode == types.ModeMut && types.IsArena(p.Type) {
				if asym := fc.rootSymbolOfExpr(args[i]); asym != nil && asym.BindKind == BindOwned {
					fc.pinArenaAt(asym, fc.curDepth())
				}
			}
		}
	}

	if fc.unsafeDepth == 0 {
		for i, p := range params {
			if !paramHasInteriorRefSlot(p.Type) {
				continue
			}
			if _, isNone := args[i].(*ast.NoneLit); isNone {
				continue
			}
			targetAd := fc.arenaDepthOfExpr(args[i])
			for j, argJ := range args {
				if i == j {
					continue
				}
				adJ := fc.arenaDepthOfExpr(argJ)
				if j < len(params) && params[j].Mode == types.ModeMut && types.IsArena(params[j].Type) {
					if asym := fc.rootSymbolOfExpr(argJ); asym != nil && asym.BindKind == BindOwned && asym.ScopeDepth > adJ {
						adJ = asym.ScopeDepth
					}
				}
				if adJ > targetAd {
					fc.a.Rep.Errorf(argJ.GetSpan(), "arena reference `ref[T]` from an inner `Arena` cannot be passed alongside outer %s that may store it", p.Type.String())
				}
			}
		}
	}
}

func paramHasInteriorRefSlot(t types.Type) bool {
	return paramHasInteriorRefSlotVisited(t, make(map[types.Type]bool))
}

func paramHasInteriorRefSlotVisited(t types.Type, seen map[types.Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	switch v := types.CollView(t).(type) {
	case *types.Ref:
		return typeMayHoldArenaRef(v.Elem)
	case *types.Interface, *types.Func:
		return true
	case *types.Optional:
		return paramHasInteriorRefSlotVisited(v.Elem, seen)
	case *types.Vector:
		return paramHasInteriorRefSlotVisited(v.Elem, seen)
	case *types.StackArray:
		return paramHasInteriorRefSlotVisited(v.Elem, seen)
	case *types.Tuple:
		for _, el := range v.Elems {
			if paramHasInteriorRefSlotVisited(el, seen) {
				return true
			}
		}
		return false
	case *types.Set:
		return paramHasInteriorRefSlotVisited(v.Elem, seen)
	case *types.Map:
		return paramHasInteriorRefSlotVisited(v.Key, seen) || paramHasInteriorRefSlotVisited(v.Value, seen)
	case *types.Distinct:
		return paramHasInteriorRefSlotVisited(v.Base, seen)
	case *types.Enum:
		if !v.IsTagged {
			return false
		}
		seen[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && paramHasInteriorRefSlotVisited(vr.Payload, seen) {
				return true
			}
		}
		return false
	case *types.Struct:
		seen[v] = true
		for _, f := range v.Fields {
			if paramHasInteriorRefSlotVisited(f.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (fc *funcChecker) checkMutatingMethodRecvEscape(recv ast.Expr, args []ast.Expr, span diag.Span) {
	if recv == nil || fc.unsafeDepth > 0 {
		return
	}
	recvAd := fc.arenaDepthOfExpr(recv)
	placeRoot := fc.rootSymbolOfExpr(recv)
	for _, arg := range args {
		ad := fc.arenaDepthOfExpr(arg)
		if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.Amp {
			if asym := fc.rootSymbolOfExpr(u.Operand); asym != nil && asym.Type != nil && types.IsArena(asym.Type) && asym.BindKind == BindOwned && asym.ScopeDepth > ad {
				ad = asym.ScopeDepth
			}
		}
		if ad <= 0 {
			continue
		}
		if recvAd > 0 && recvAd < ad {
			fc.a.Rep.Errorf(arg.GetSpan(), "arena reference `ref[T]` from an inner `Arena` cannot escape into an outer `Arena` allocation")
		} else if placeRoot != nil && placeRoot.ScopeDepth < ad && !types.IsArena(placeRoot.Type) {
			fc.a.Rep.Errorf(arg.GetSpan(), "arena reference `ref[T]` cannot escape into %q declared in an outer scope", placeRoot.Name)
		} else if placeRoot == nil && recvAd == 0 {
			fc.a.Rep.Errorf(span, "arena reference `ref[T]` cannot escape into an outer reference target")
		}
	}
	if recvTy := fc.a.infoFor(recv).Type; typeMayHoldArenaRef(recvTy) {
		if placeRoot != nil && placeRoot.ScopeDepth > 0 {
			fc.lowerPinnedArenasTo(placeRoot.ScopeDepth)
		} else {
			fc.lowerPinnedArenasTo(1)
		}
	}
}

func (fc *funcChecker) checkReadOnlyArg(arg ast.Expr, paramTy types.Type, argNum int) types.Type {
	aty := fc.checkExprExpect(arg, paramTy, true)
	if aty == types.TypeInvalid || paramTy == types.TypeInvalid {
		return aty
	}
	// Contiguous view coercion: `[N: T]` -> `[T]` / `List[T]` / `Array[T]`,
	// and `Array[T]` -> `[T]` / `List[T]` read-only views!
	if arr, isArr := aty.(*types.StackArray); isArr {
		if elemTy, ok := fc.a.listOrVecElem(paramTy); ok && arr.Elem.Equals(elemTy) {
			fc.a.infoFor(arg).BorrowAsView = true
			return aty
		}
		if arrSt, isArrSt := fc.a.isArrayViewStruct(paramTy); isArrSt && arr.Elem.Equals(arrSt.TypeArgs[0]) {
			fc.a.infoFor(arg).BorrowAsView = true
			return aty
		}
	}
	if arrSt, isArrSt := fc.a.isArrayViewStruct(aty); isArrSt {
		if elemTy, ok := fc.a.listOrVecElem(paramTy); ok && arrSt.TypeArgs[0].Equals(elemTy) {
			fc.a.infoFor(arg).BorrowAsView = true
			return aty
		}
	}
	// Mark zero-allocation views for string literals, slices, and borrowed interfaces!
	fc.markViewIfApplicable(arg, paramTy)
	fc.verifyAssignable(aty, paramTy, arg, arg.GetSpan(), fmt.Sprintf("argument %d", argNum))
	return aty
}

func (a *Analyzer) isListStruct(t types.Type) (*types.Struct, bool) {
	st, ok := t.(*types.Struct)
	if !ok || !a.isStdStructTemplate(st, a.Prog.GenStructList) || len(st.TypeArgs) != 1 {
		return nil, false
	}
	return st, true
}

func (a *Analyzer) listOrVecElem(t types.Type) (types.Type, bool) {
	if vec, isVec := t.(*types.Vector); isVec {
		return vec.Elem, true
	}
	if listSt, isList := a.isListStruct(t); isList {
		return listSt.TypeArgs[0], true
	}
	return nil, false
}

func (a *Analyzer) isArrayViewStruct(t types.Type) (*types.Struct, bool) {
	st, ok := t.(*types.Struct)
	if !ok || !a.isStdStructTemplate(st, a.Prog.GenStructArray) || len(st.TypeArgs) != 1 {
		return nil, false
	}
	return st, true
}

func (fc *funcChecker) markViewIfApplicable(expr ast.Expr, paramTy types.Type) {
	inf := fc.a.infoFor(expr)
	switch expr.(type) {
	case *ast.StringLit:
		if paramTy.Equals(types.TypeString) {
			inf.BorrowAsView = true
		}
	case *ast.SliceExpr:
		inf.BorrowAsView = true
	}
	if arr, isArr := inf.Type.(*types.StackArray); isArr {
		if elemTy, ok := fc.a.listOrVecElem(paramTy); ok && arr.Elem.Equals(elemTy) {
			inf.BorrowAsView = true
		} else if arrSt, isArrSt := fc.a.isArrayViewStruct(paramTy); isArrSt && arr.Elem.Equals(arrSt.TypeArgs[0]) {
			inf.BorrowAsView = true
		}
	} else if arrSt, isArrSt := fc.a.isArrayViewStruct(inf.Type); isArrSt {
		if elemTy, ok := fc.a.listOrVecElem(paramTy); ok && arrSt.TypeArgs[0].Equals(elemTy) {
			inf.BorrowAsView = true
		}
	}
	if _, isIfc := paramTy.(*types.Interface); isIfc {
		inf.BorrowAsView = true // borrow fat pointer on stack without heap box!
	}
}

// -----------------------------------------------------------------------------
// Built-in Methods, Universe Functions & Raw Memory Intrinsics
// -----------------------------------------------------------------------------

func (fc *funcChecker) checkBuiltinMethodCall(recvTy types.Type, isArenaRef, isOptChain, inMatchPattern bool, mem *ast.MemberExpr, call *ast.CallExpr) types.Type {
	inf := fc.a.infoFor(call)
	inf.CallKind = CallBuiltinMethod
	inf.MethodRecv = mem.Base
	inf.IsOptChain = isOptChain

	wrapRet := func(t types.Type) types.Type {
		if isOptChain && !t.Equals(types.TypeVoid) {
			t = types.NewOptional(t)
		}
		inf.Type = t
		return t
	}

	requireMutRecv := func() *Symbol {
		if inMatchPattern {
			fc.a.Rep.Errorf(call.Span, "method `%s.%s` in `match` pattern must take read-only `self` (got `&self`)", recvTy.String(), mem.Member)
			return nil
		}
		if isOptChain {
			fc.a.Rep.Errorf(call.Span, "cannot call mutating method %q via optional `?.` chain", mem.Member)
			return nil
		}
		inf.MethodRecvMode = types.ModeMut
		fc.checkMutatingMethodRecvEscape(mem.Base, call.Args, call.Span)
		if isArenaRef {
			return nil
		}
		return fc.checkMutablePlace(mem.Base, fmt.Sprintf("call mutating method `.%s` on", mem.Member))
	}

	// Universal `.copy()` and `.to_string()`
	if mem.Member == "copy" && len(call.Args) == 0 {
		if !fc.a.supportsCopy(recvTy) {
			fc.a.Rep.Errorf(call.Span, "type %s does not implement `Copyable` (`.copy()`)", recvTy.String())
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		inf.BuiltinName = "any.copy"
		return wrapRet(recvTy)
	}
	// Universal `.hash()` for hashable types with no `hash` method of their own
	// (tuples, tag-only enums, pointers, and distinct types).
	if mem.Member == "hash" && len(call.Args) == 0 && fc.a.supportsHash(recvTy) {
		inf.BuiltinName = "any.hash"
		return wrapRet(types.TypeU64)
	}
	if mem.Member == "to_string" && len(call.Args) == 0 {
		if !fc.a.supportsToString(recvTy) {
			fc.a.Rep.Errorf(call.Span, "type %s does not implement `Printable` (`.to_string()`)", recvTy.String())
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		inf.BuiltinName = "any.to_string"
		return wrapRet(types.TypeString)
	}
	if mem.Member == "to_bits" {
		if recvTy.Equals(types.TypeFloat) {
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "float.to_bits"
			return wrapRet(types.TypeU64)
		}
		if recvTy.Equals(types.TypeF32) {
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "f32.to_bits"
			return wrapRet(types.TypeU32)
		}
	}

	// `string` built-in primitive representation methods (higher-level string & char methods live in `std/core.ll`)
	if recvTy.Equals(types.TypeString) {
		fc.markViewIfApplicable(mem.Base, types.TypeString)
		switch mem.Member {
		case "len":
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "string.len"
			return wrapRet(types.TypeInt)
		case "byte_at":
			fc.checkSignatureArgs([]types.FuncParam{{Name: "idx", Mode: types.ModeRead, Type: types.TypeInt}}, call.Args, nil, call.Span)
			inf.BuiltinName = "string.byte_at"
			return wrapRet(types.TypeU8)
		case "c_str":
			if fc.unsafeDepth == 0 {
				fc.a.Rep.Errorf(call.Span, "`string.c_str()` returns raw `ptr[u8]` and requires `unsafe`")
			}
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "string.c_str"
			return wrapRet(&types.Ptr{Elem: types.TypeU8})
		}
	}

	// `[T]` Vector methods
	if vec, isVec := recvTy.(*types.Vector); isVec {
		switch mem.Member {
		case "len", "cap":
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "vec." + mem.Member
			return wrapRet(types.TypeInt)
		case "is_empty":
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "vec.is_empty"
			return wrapRet(types.TypeBool)
		case "get":
			if !fc.a.supportsCopy(vec.Elem) {
				fc.a.Rep.Errorf(call.Span, "`.get(i)` requires copyable element type, got %s", vec.Elem.String())
			}
			fc.checkSignatureArgs([]types.FuncParam{{Name: "idx", Mode: types.ModeRead, Type: types.TypeInt}}, call.Args, nil, call.Span)
			inf.BuiltinName = "vec.get"
			return wrapRet(types.NewOptional(vec.Elem))
		case "contains":
			if !fc.a.supportsEq(vec.Elem) {
				fc.a.Rep.Errorf(call.Span, "`.contains(x)` requires equality on element type %s", vec.Elem.String())
			}
			fc.checkSignatureArgs([]types.FuncParam{{Name: "val", Mode: types.ModeRead, Type: vec.Elem}}, call.Args, nil, call.Span)
			inf.BuiltinName = "vec.contains"
			return wrapRet(types.TypeBool)
		case "push":
			mr := requireMutRecv()
			fc.checkSignatureArgs([]types.FuncParam{{Name: "val", Mode: types.ModeOwn, Type: vec.Elem}}, call.Args, mr, call.Span)
			inf.BuiltinName = "vec.push"
			return wrapRet(types.TypeVoid)
		case "pop":
			_ = requireMutRecv()
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "vec.pop"
			return wrapRet(types.NewOptional(vec.Elem))
		case "clear":
			_ = requireMutRecv()
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "vec.clear"
			return wrapRet(types.TypeVoid)
		case "reserve":
			mr := requireMutRecv()
			fc.checkSignatureArgs([]types.FuncParam{{Name: "cap", Mode: types.ModeRead, Type: types.TypeInt}}, call.Args, mr, call.Span)
			inf.BuiltinName = "vec.reserve"
			return wrapRet(types.TypeVoid)
		case "join":
			if vec.Elem.Equals(types.TypeString) {
				fc.checkSignatureArgs([]types.FuncParam{{Name: "sep", Mode: types.ModeRead, Type: types.TypeString}}, call.Args, nil, call.Span)
				inf.BuiltinName = "vec.join"
				return wrapRet(types.TypeString)
			}
		}
	}

	// `[N: T]` StackArray methods
	if arr, isArr := recvTy.(*types.StackArray); isArr {
		switch mem.Member {
		case "len":
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "arr.len"
			return wrapRet(types.TypeInt)
		case "is_empty":
			fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
			inf.BuiltinName = "arr.is_empty"
			return wrapRet(types.TypeBool)
		case "get":
			if !fc.a.supportsCopy(arr.Elem) {
				fc.a.Rep.Errorf(call.Span, "`.get(i)` requires copyable element type, got %s", arr.Elem.String())
			}
			fc.checkSignatureArgs([]types.FuncParam{{Name: "idx", Mode: types.ModeRead, Type: types.TypeInt}}, call.Args, nil, call.Span)
			inf.BuiltinName = "arr.get"
			return wrapRet(types.NewOptional(arr.Elem))
		case "contains":
			if !fc.a.supportsEq(arr.Elem) {
				fc.a.Rep.Errorf(call.Span, "`.contains(x)` requires equality on element type %s", arr.Elem.String())
			}
			fc.checkSignatureArgs([]types.FuncParam{{Name: "val", Mode: types.ModeRead, Type: arr.Elem}}, call.Args, nil, call.Span)
			inf.BuiltinName = "arr.contains"
			return wrapRet(types.TypeBool)
		}
	}

	hint, cands := fc.a.unknownMethodHint(recvTy, mem.Member)
	fc.a.errorWithRename(fc.a.focusSpan(mem), fmt.Sprintf("type %s has no method %q", recvTy.String(), mem.Member), mem.Member, hint, cands)
	for _, a := range call.Args {
		_ = fc.checkExpr(a, true)
	}
	inf.Type = types.TypeInvalid
	return types.TypeInvalid
}

func (fc *funcChecker) tryCheckBuiltinFunc(name string, call *ast.CallExpr) (bool, types.Type) {
	inf := fc.a.infoFor(call)
	switch name {
	case "print", "println", "eprint", "eprintln":
		if fc.inConstContext() {
			fc.recordConstViolation(call.Span, fmt.Sprintf("cannot call host I/O builtin `%s` in a `const` or `comptime` context", name), "host I/O is only available in runtime `bind` functions", nil)
		}
		if len(call.Args) == 0 && (name == "println" || name == "eprintln") {
			inf.CallKind = CallBuiltinFunc
			inf.BuiltinName = name
			inf.Type = types.TypeVoid
			return true, types.TypeVoid
		}
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`%s` expects 1 argument, got %d", name, len(call.Args))
			inf.Type = types.TypeVoid
			return true, types.TypeVoid
		}
		aty := fc.checkExpr(call.Args[0], true)
		if aty.Equals(types.TypeString) {
			fc.markViewIfApplicable(call.Args[0], types.TypeString)
		} else if aty != types.TypeInvalid && !fc.a.supportsToString(aty) {
			fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`%s` argument of type %s does not implement `Printable`", name, aty.String())
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = name
		inf.Type = types.TypeVoid
		return true, types.TypeVoid

	case "panic":
		// `panic(msg: string)` or `panic(err: Error)`: a panic is a fatal `Error`, so both
		// channels share one error type and one formatted trace (see SPECIFICATION.md §9.6).
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "panic"
		inf.Type = types.TypeNever
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`panic` expects 1 argument (a `string` message or an `Error`), got %d", len(call.Args))
			for _, a := range call.Args {
				fc.checkExpr(a, true)
			}
			return true, types.TypeNever
		}
		aty := fc.checkExpr(call.Args[0], true)
		if aty.Equals(types.TypeString) {
			fc.markViewIfApplicable(call.Args[0], types.TypeString)
		} else if aty != types.TypeInvalid && !fc.a.isCoreErrorType(aty) {
			fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`panic` expects a `string` message or an `Error`, got %s", aty.String())
		}
		return true, types.TypeNever

	case "assert":
		if len(call.Args) == 1 {
			fc.checkSignatureArgs([]types.FuncParam{{Name: "cond", Mode: types.ModeRead, Type: types.TypeBool}}, call.Args, nil, call.Span)
		} else {
			fc.checkSignatureArgs([]types.FuncParam{
				{Name: "cond", Mode: types.ModeRead, Type: types.TypeBool},
				{Name: "msg", Mode: types.ModeRead, Type: types.TypeString},
			}, call.Args, nil, call.Span)
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "assert"
		inf.Type = types.TypeVoid
		return true, types.TypeVoid

	case "compile_error":
		fc.a.Rep.ErrorWithHint(
			call.Span,
			"`compile_error` has moved into the `comp` namespace (`comp.compile_error`)",
			"write `comp.compile_error(...)` instead",
		)
		for _, a := range call.Args {
			fc.checkExpr(a, true)
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "compile_error"
		inf.Type = types.TypeNever
		return true, types.TypeNever

	case "embed_str", "embed_bytes":
		fc.a.Rep.ErrorWithHint(
			call.Span,
			fmt.Sprintf("`%s` belongs in the `comp` namespace (`comp.%s`)", name, name),
			fmt.Sprintf("write `comp.%s(...)` instead", name),
		)
		for _, a := range call.Args {
			fc.checkExpr(a, true)
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "comp." + name
		if name == "embed_bytes" {
			retTy := &types.Vector{Elem: types.TypeU8}
			inf.Type = retTy
			return true, retTy
		}
		inf.Type = types.TypeString
		return true, types.TypeString

	case "caller_location":
		if len(call.Args) != 0 {
			fc.a.Rep.Errorf(call.Span, "`caller_location()` takes 0 arguments")
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "caller_location"
		inf.Type = types.TypeString
		return true, types.TypeString

	case "caller_file":
		if len(call.Args) != 0 {
			fc.a.Rep.Errorf(call.Span, "`caller_file()` takes 0 arguments")
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "caller_file"
		inf.Type = types.TypeString
		return true, types.TypeString

	case "caller_line":
		if len(call.Args) != 0 {
			fc.a.Rep.Errorf(call.Span, "`caller_line()` takes 0 arguments")
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "caller_line"
		inf.Type = types.TypeInt
		return true, types.TypeInt

	case "caller_col":
		if len(call.Args) != 0 {
			fc.a.Rep.Errorf(call.Span, "`caller_col()` takes 0 arguments")
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "caller_col"
		inf.Type = types.TypeInt
		return true, types.TypeInt

	case "abs":
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`abs(x)` expects 1 numeric argument")
			inf.Type = types.TypeInvalid
			return true, types.TypeInvalid
		}
		aty := fc.checkExpr(call.Args[0], true)
		p := types.UnderlyingPrimitive(aty)
		if aty != types.TypeInvalid && (p == nil || (!p.IsSignedInt() && !p.IsFloat())) {
			fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`abs` requires signed integer or float, got %s", aty.String())
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "abs"
		inf.Type = aty
		return true, aty

	case "min", "max":
		if len(call.Args) != 2 {
			fc.a.Rep.Errorf(call.Span, "`%s(a, b)` expects 2 numeric arguments", name)
			inf.Type = types.TypeInvalid
			return true, types.TypeInvalid
		}
		lty, rty := fc.checkOperandPairWithAdaptation(call.Args[0], call.Args[1], nil)
		if lty != types.TypeInvalid && rty != types.TypeInvalid {
			p := types.UnderlyingPrimitive(lty)
			if !lty.Equals(rty) || p == nil || !p.IsNumeric() {
				fc.a.Rep.Errorf(call.Span, "`%s` requires two matching numeric arguments, got %s and %s", name, lty.String(), rty.String())
			}
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = name
		inf.Type = lty
		return true, lty

	case "args":
		if fc.inConstContext() {
			fc.recordConstViolation(call.Span, "cannot call runtime process builtin `args()` in a `const` or `comptime` context", "process CLI arguments are only available in runtime `bind` functions", nil)
		}
		fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "args"
		res := &types.Vector{Elem: types.TypeString}
		inf.Type = res
		return true, res

	case "exit":
		if fc.inConstContext() {
			fc.recordConstViolation(call.Span, "cannot call runtime process builtin `exit()` in a `const` or `comptime` context", "`exit()` is only available in runtime `bind` functions", nil)
		}
		fc.checkSignatureArgs([]types.FuncParam{{Name: "code", Mode: types.ModeRead, Type: types.TypeInt}}, call.Args, nil, call.Span)
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "exit"
		inf.Type = types.TypeNever
		return true, types.TypeNever
	}
	return false, nil
}

func (fc *funcChecker) checkCompNamespaceCall(mem *ast.MemberExpr, call *ast.CallExpr) types.Type {
	inf := fc.a.infoFor(call)
	if !fc.inComptimeOnlyContext() {
		fc.a.Rep.ErrorWithHint(
			call.Span,
			fmt.Sprintf("cannot call `@comptime_only` builtin `comp.%s` outside a compile-time context", mem.Member),
			"wrap the call in `comptime { ... }`, a `const` initializer, or a `@comptime_only` function",
		)
	}
	if len(call.TypeArgs) > 0 {
		fc.a.Rep.Errorf(call.Span, "`comp.%s` does not take type arguments", mem.Member)
	}
	switch mem.Member {
	case "compile_error":
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`comp.compile_error(msg)` expects 1 `string` argument, got %d", len(call.Args))
			for _, a := range call.Args {
				fc.checkExpr(a, true)
			}
		} else {
			fc.checkSignatureArgs([]types.FuncParam{{Name: "msg", Mode: types.ModeRead, Type: types.TypeString}}, call.Args, nil, call.Span)
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "compile_error"
		inf.Type = types.TypeNever
		return types.TypeNever

	case "log":
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`comp.log(val)` expects 1 argument, got %d", len(call.Args))
			for _, a := range call.Args {
				fc.checkExpr(a, true)
			}
		} else {
			aty := fc.checkExpr(call.Args[0], true)
			if aty.Equals(types.TypeString) {
				fc.markViewIfApplicable(call.Args[0], types.TypeString)
			} else if aty != types.TypeInvalid && !fc.a.supportsToString(aty) {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`comp.log` argument of type %s does not implement `Printable`", aty.String())
			}
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "comp.log"
		inf.Type = types.TypeVoid
		return types.TypeVoid

	case "embed_str":
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`comp.embed_str(path)` expects 1 `string` argument, got %d", len(call.Args))
			for _, a := range call.Args {
				fc.checkExpr(a, true)
			}
		} else {
			fc.checkSignatureArgs([]types.FuncParam{{Name: "path", Mode: types.ModeRead, Type: types.TypeString}}, call.Args, nil, call.Span)
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "comp.embed_str"
		inf.Type = types.TypeString
		return types.TypeString

	case "embed_bytes":
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`comp.embed_bytes(path)` expects 1 `string` argument, got %d", len(call.Args))
			for _, a := range call.Args {
				fc.checkExpr(a, true)
			}
		} else {
			fc.checkSignatureArgs([]types.FuncParam{{Name: "path", Mode: types.ModeRead, Type: types.TypeString}}, call.Args, nil, call.Span)
		}
		inf.CallKind = CallBuiltinFunc
		inf.BuiltinName = "comp.embed_bytes"
		retTy := &types.Vector{Elem: types.TypeU8}
		inf.Type = retTy
		return retTy

	default:
		fc.a.Rep.ErrorWithHint(
			mem.Span,
			fmt.Sprintf("unknown `comp` builtin %q", mem.Member),
			"available `comp` builtins: `comp.compile_error`, `comp.log`, `comp.embed_str`, `comp.embed_bytes` (and target constant `comp.TARGET`)",
		)
		for _, a := range call.Args {
			fc.checkExpr(a, true)
		}
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
}

func (fc *funcChecker) checkTypeNamespaceCall(mem *ast.MemberExpr, call *ast.CallExpr) types.Type {
	inf := fc.a.infoFor(call)
	requireOneTypeArg := func() types.Type {
		if len(call.TypeArgs) != 1 {
			fc.a.Rep.Errorf(call.Span, "`type.%s[T]()` requires 1 type argument", mem.Member)
			return types.TypeInvalid
		}
		return fc.a.resolveValueType(fc.mod, call.TypeArgs[0], fc.subst, false)
	}
	switch mem.Member {
	case "size_of", "align_of":
		ty := requireOneTypeArg()
		fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = mem.Member
		inf.IntrinsicType = ty
		inf.Type = types.TypeInt
		return types.TypeInt

	case "is_plain":
		ty := requireOneTypeArg()
		fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "is_plain"
		inf.IntrinsicType = ty
		inf.Type = types.TypeBool
		return types.TypeBool

	default:
		fc.a.Rep.ErrorWithHint(
			mem.Span,
			fmt.Sprintf("unknown `type` builtin %q", mem.Member),
			"available `type` builtins: `type.size_of`, `type.align_of`, `type.is_plain`",
		)
		for _, a := range call.Args {
			fc.checkExpr(a, true)
		}
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
}

func (fc *funcChecker) tryCheckIntrinsicCall(name string, call *ast.CallExpr) (bool, types.Type) {
	inf := fc.a.infoFor(call)

	requireUnsafe := func() {
		if fc.unsafeDepth == 0 {
			fc.a.Rep.Errorf(call.Span, "low-level memory intrinsic `%s` requires `unsafe`", name)
		}
	}

	requireOneTypeArg := func() types.Type {
		if len(call.TypeArgs) != 1 {
			fc.a.Rep.Errorf(call.Span, "`%s[T](...)` requires 1 type argument", name)
			return types.TypeInvalid
		}
		return fc.a.resolveValueType(fc.mod, call.TypeArgs[0], fc.subst, false)
	}

	switch name {
	case "size_of", "align_of":
		fc.a.Rep.ErrorWithHint(
			call.Span,
			fmt.Sprintf("`%s` has moved into the `type` namespace (`type.%s`)", name, name),
			fmt.Sprintf("write `type.%s[T]()` instead", name),
		)
		ty := requireOneTypeArg()
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = name
		inf.IntrinsicType = ty
		inf.Type = types.TypeInt
		return true, types.TypeInt

	case "is_plain":
		fc.a.Rep.ErrorWithHint(
			call.Span,
			"`is_plain` has moved into the `type` namespace (`type.is_plain`)",
			"write `type.is_plain[T]()` instead",
		)
		ty := requireOneTypeArg()
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "is_plain"
		inf.IntrinsicType = ty
		inf.Type = types.TypeBool
		return true, types.TypeBool

	case "alloc_raw":
		requireUnsafe()
		ty := requireOneTypeArg()
		fc.checkSignatureArgs([]types.FuncParam{{Name: "count", Mode: types.ModeRead, Type: types.TypeInt}}, call.Args, nil, call.Span)
		res := &types.Ptr{Elem: ty}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "alloc_raw"
		inf.IntrinsicType = ty
		inf.Type = res
		return true, res

	case "alloc_uninit":
		requireUnsafe()
		ty := requireOneTypeArg()
		fc.checkSignatureArgs([]types.FuncParam{{Name: "count", Mode: types.ModeRead, Type: types.TypeInt}}, call.Args, nil, call.Span)
		res := &types.Ptr{Elem: ty}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "alloc_uninit"
		inf.IntrinsicType = ty
		inf.Type = res
		return true, res

	case "realloc_raw":
		requireUnsafe()
		if len(call.Args) != 2 {
			fc.a.Rep.Errorf(call.Span, "`realloc_raw(p, count)` expects 2 arguments")
			inf.Type = types.TypeInvalid
			return true, types.TypeInvalid
		}
		pty := fc.checkExpr(call.Args[0], true)
		cty := fc.checkExprExpect(call.Args[1], types.TypeInt, true)
		fc.verifyAssignable(cty, types.TypeInt, call.Args[1], call.Args[1].GetSpan(), "realloc_raw count")
		ptrTy, ok := pty.(*types.Ptr)
		if !ok && pty != types.TypeInvalid {
			fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`realloc_raw` requires `ptr[T]`, got %s", pty.String())
			inf.Type = types.TypeInvalid
			return true, types.TypeInvalid
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "realloc_raw"
		if ptrTy != nil {
			inf.IntrinsicType = ptrTy.Elem
		}
		inf.Type = pty
		return true, pty

	case "free_raw":
		requireUnsafe()
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`free_raw(p)` expects 1 `ptr[T]` argument")
			inf.Type = types.TypeVoid
			return true, types.TypeVoid
		}
		pty := fc.checkExpr(call.Args[0], true)
		if _, ok := pty.(*types.Ptr); !ok && pty != types.TypeInvalid {
			fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`free_raw` requires `ptr[T]`, got %s", pty.String())
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "free_raw"
		inf.Type = types.TypeVoid
		return true, types.TypeVoid

	case "null_ptr":
		requireUnsafe()
		ty := requireOneTypeArg()
		fc.checkSignatureArgs(nil, call.Args, nil, call.Span)
		res := &types.Ptr{Elem: ty}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "null_ptr"
		inf.IntrinsicType = ty
		inf.Type = res
		return true, res

	case "is_null":
		requireUnsafe()
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`is_null(p)` expects 1 `ptr[T]` argument")
			inf.Type = types.TypeBool
			return true, types.TypeBool
		}
		pty := fc.checkExpr(call.Args[0], true)
		if _, ok := pty.(*types.Ptr); !ok && pty != types.TypeInvalid {
			fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`is_null` requires `ptr[T]`, got %s", pty.String())
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "is_null"
		inf.Type = types.TypeBool
		return true, types.TypeBool

	case "addr_of":
		requireUnsafe()
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`addr_of(place)` expects 1 lvalue argument")
			inf.Type = types.TypeInvalid
			return true, types.TypeInvalid
		}
		aty := fc.checkExpr(call.Args[0], true)
		_, isLval, _, _ := fc.analyzePlace(call.Args[0])
		if !isLval {
			fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`addr_of` requires an addressable variable, field, or element")
		}
		res := &types.Ptr{Elem: aty}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "addr_of"
		inf.IntrinsicType = aty
		inf.Type = res
		return true, res

	case "ptr_add":
		requireUnsafe()
		if len(call.Args) != 2 {
			fc.a.Rep.Errorf(call.Span, "`ptr_add(p, offset)` expects 2 arguments")
			inf.Type = types.TypeInvalid
			return true, types.TypeInvalid
		}
		pty := fc.checkExpr(call.Args[0], true)
		oty := fc.checkExprExpect(call.Args[1], types.TypeInt, true)
		fc.verifyAssignable(oty, types.TypeInt, call.Args[1], call.Args[1].GetSpan(), "ptr_add offset")
		if _, ok := pty.(*types.Ptr); !ok && pty != types.TypeInvalid {
			fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`ptr_add` requires `ptr[T]`, got %s", pty.String())
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "ptr_add"
		inf.Type = pty
		return true, pty

	case "ptr_read":
		requireUnsafe()
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`ptr_read(p)` expects 1 `ptr[T]` argument")
			inf.Type = types.TypeInvalid
			return true, types.TypeInvalid
		}
		pty := fc.checkExpr(call.Args[0], true)
		pt, ok := pty.(*types.Ptr)
		if !ok {
			if pty != types.TypeInvalid {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`ptr_read` requires `ptr[T]`, got %s", pty.String())
			}
			inf.Type = types.TypeInvalid
			return true, types.TypeInvalid
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "ptr_read"
		inf.IntrinsicType = pt.Elem
		inf.Type = pt.Elem
		return true, pt.Elem

	case "ptr_write":
		requireUnsafe()
		if len(call.Args) != 2 {
			fc.a.Rep.Errorf(call.Span, "`ptr_write(p, val)` expects 2 arguments")
			inf.Type = types.TypeVoid
			return true, types.TypeVoid
		}
		pty := fc.checkExpr(call.Args[0], true)
		pt, ok := pty.(*types.Ptr)
		if !ok {
			if pty != types.TypeInvalid {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`ptr_write` requires `ptr[T]`, got %s", pty.String())
			}
			_ = fc.checkExpr(call.Args[1], true)
			inf.Type = types.TypeVoid
			return true, types.TypeVoid
		}
		vty := fc.checkExprExpect(call.Args[1], pt.Elem, true)
		fc.verifyAssignable(vty, pt.Elem, call.Args[1], call.Args[1].GetSpan(), "ptr_write value")
		fc.consumeValue(call.Args[1], pt.Elem, true)
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "ptr_write"
		inf.IntrinsicType = pt.Elem
		inf.Type = types.TypeVoid
		return true, types.TypeVoid

	case "ptr_cast":
		requireUnsafe()
		toTy := requireOneTypeArg()
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`ptr_cast[To](x)` expects 1 argument")
		} else {
			aty := fc.checkExpr(call.Args[0], true)
			_, isP := aty.(*types.Ptr)
			if ft, ok := aty.(*types.Func); ok && ft.Extern {
				isP = true // an `extern fn` is a C function pointer
			}
			prim := types.UnderlyingPrimitive(aty)
			if !isP && (prim == nil || !prim.IsInteger()) && aty != types.TypeInvalid {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`ptr_cast` source must be `ptr[From]`, an `extern fn`, or an integer address, got %s", aty.String())
			}
		}
		res := &types.Ptr{Elem: toTy}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "ptr_cast"
		inf.IntrinsicType = toTy
		inf.Type = res
		return true, res

	case "fn_from_ptr":
		// `fn_from_ptr[extern fn(...) R](p)`: the C function at address `p` (from `dlsym`, or a C
		// struct field) as a callable `extern fn`.
		requireUnsafe()
		toTy := requireOneTypeArg()
		ft, ok := toTy.(*types.Func)
		if toTy != types.TypeInvalid && (!ok || !ft.Extern) {
			fc.a.Rep.Errorf(call.Span, "`fn_from_ptr[T](p)` needs an `extern fn(...)` type for `T`, got %s", toTy.String())
			toTy = types.TypeInvalid
		}
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`fn_from_ptr[T](p)` expects 1 argument")
		} else {
			aty := fc.checkExpr(call.Args[0], true)
			_, isP := aty.(*types.Ptr)
			prim := types.UnderlyingPrimitive(aty)
			if !isP && (prim == nil || !prim.IsInteger()) && aty != types.TypeInvalid {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`fn_from_ptr` takes a `ptr[T]` or an integer address, got %s", aty.String())
			}
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "fn_from_ptr"
		inf.Type = toTy
		return true, toTy

	case "ref_from_ptr":
		// `ref_from_ptr(p)`: the `ref[T]` for the value at `p`. For std/core's `Arena`, which hands
		// out references to the memory it owns.
		requireUnsafe()
		var elem types.Type = types.TypeInvalid
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`ref_from_ptr(p)` expects 1 `ptr[T]` argument")
		} else {
			pty := fc.checkExpr(call.Args[0], true)
			if p, ok := pty.(*types.Ptr); ok {
				elem = p.Elem
			} else if pty != types.TypeInvalid {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`ref_from_ptr` requires `ptr[T]`, got %s", pty.String())
			}
		}
		res := types.Type(types.TypeInvalid)
		if elem != types.TypeInvalid {
			res = &types.Ref{Elem: elem}
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "ref_from_ptr"
		inf.Type = res
		return true, res

	case "ptr_addr":
		requireUnsafe()
		if len(call.Args) != 1 {
			fc.a.Rep.Errorf(call.Span, "`ptr_addr(p)` expects 1 `ptr[T]` argument")
		} else {
			pty := fc.checkExpr(call.Args[0], true)
			_, isP := pty.(*types.Ptr)
			if ft, ok := pty.(*types.Func); ok && ft.Extern {
				isP = true
			}
			if !isP && pty != types.TypeInvalid {
				fc.a.Rep.Errorf(call.Args[0].GetSpan(), "`ptr_addr` requires `ptr[T]` or an `extern fn`, got %s", pty.String())
			}
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "ptr_addr"
		inf.Type = types.TypeU64
		return true, types.TypeU64

	case "copy_bytes":
		requireUnsafe()
		u8p := &types.Ptr{Elem: types.TypeU8}
		fc.checkSignatureArgs([]types.FuncParam{
			{Name: "dst", Mode: types.ModeRead, Type: u8p},
			{Name: "src", Mode: types.ModeRead, Type: u8p},
			{Name: "count", Mode: types.ModeRead, Type: types.TypeInt},
		}, call.Args, nil, call.Span)
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "copy_bytes"
		inf.Type = types.TypeVoid
		return true, types.TypeVoid

	case "zero_bytes":
		requireUnsafe()
		u8p := &types.Ptr{Elem: types.TypeU8}
		fc.checkSignatureArgs([]types.FuncParam{
			{Name: "dst", Mode: types.ModeRead, Type: u8p},
			{Name: "count", Mode: types.ModeRead, Type: types.TypeInt},
		}, call.Args, nil, call.Span)
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "zero_bytes"
		inf.Type = types.TypeVoid
		return true, types.TypeVoid

	case "fill_bytes":
		requireUnsafe()
		u8p := &types.Ptr{Elem: types.TypeU8}
		fc.checkSignatureArgs([]types.FuncParam{
			{Name: "dst", Mode: types.ModeRead, Type: u8p},
			{Name: "val", Mode: types.ModeRead, Type: types.TypeU8},
			{Name: "count", Mode: types.ModeRead, Type: types.TypeInt},
		}, call.Args, nil, call.Span)
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = "fill_bytes"
		inf.Type = types.TypeVoid
		return true, types.TypeVoid
	}

	u8p := &types.Ptr{Elem: types.TypeU8}
	sysIntrinsic := func(params []types.FuncParam, ret types.Type) (bool, types.Type) {
		requireUnsafe()
		fc.checkSignatureArgs(params, call.Args, nil, call.Span)
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = name
		inf.Type = ret
		return true, ret
	}

	switch name {
	case "sys_argc", "sys_tls_available":
		return sysIntrinsic(nil, types.TypeInt)
	case "sys_argv":
		return sysIntrinsic([]types.FuncParam{
			{Name: "idx", Mode: types.ModeRead, Type: types.TypeInt},
		}, u8p)
	case "syscall":
		// `syscall(number, a1, ..., a6)`: the target's system call instruction. Returns the result,
		// or `-errno` on failure on every platform.
		requireUnsafe()
		if len(call.Args) < 1 || len(call.Args) > 7 {
			fc.a.Rep.Errorf(call.Span, "`syscall(number, a1, ..., a6)` takes a number and up to 6 arguments, got %d", len(call.Args))
		}
		for _, arg := range call.Args {
			at := fc.checkExprExpect(arg, types.TypeInt, true)
			fc.verifyAssignable(at, types.TypeInt, arg, arg.GetSpan(), "`syscall` argument")
		}
		inf.CallKind = CallIntrinsic
		inf.BuiltinName = name
		inf.Type = types.TypeInt
		return true, types.TypeInt
	case "sys_tls_client_handshake":
		return sysIntrinsic([]types.FuncParam{
			{Name: "fd", Mode: types.ModeRead, Type: types.TypeInt},
			{Name: "host", Mode: types.ModeRead, Type: u8p},
		}, u8p)
	case "sys_tls_send_all":
		return sysIntrinsic([]types.FuncParam{
			{Name: "session", Mode: types.ModeRead, Type: u8p},
			{Name: "data", Mode: types.ModeRead, Type: u8p},
			{Name: "len", Mode: types.ModeRead, Type: types.TypeInt},
		}, types.TypeInt)
	case "sys_tls_recv":
		return sysIntrinsic([]types.FuncParam{
			{Name: "session", Mode: types.ModeRead, Type: u8p},
			{Name: "buf", Mode: types.ModeRead, Type: u8p},
			{Name: "max_len", Mode: types.ModeRead, Type: types.TypeInt},
			{Name: "timeout_ms", Mode: types.ModeRead, Type: types.TypeInt},
		}, types.TypeInt)
	case "sys_tls_close", "sys_tls_server_ctx_free":
		return sysIntrinsic([]types.FuncParam{
			{Name: "ptr", Mode: types.ModeRead, Type: u8p},
		}, types.TypeVoid)
	case "sys_tls_server_ctx_new":
		return sysIntrinsic([]types.FuncParam{
			{Name: "cert_file", Mode: types.ModeRead, Type: u8p},
			{Name: "key_file", Mode: types.ModeRead, Type: u8p},
		}, u8p)
	case "sys_tls_server_handshake":
		return sysIntrinsic([]types.FuncParam{
			{Name: "server_ctx", Mode: types.ModeRead, Type: u8p},
			{Name: "fd", Mode: types.ModeRead, Type: types.TypeInt},
		}, u8p)
	}

	_ = strings.TrimSpace
	return false, nil
}

// isCoreErrorType reports whether `t` is the prelude `Error` struct from `std/core`.
func (a *Analyzer) isCoreErrorType(t types.Type) bool {
	st, ok := t.(*types.Struct)
	if !ok || st.Name != "Error" {
		return false
	}
	mod := a.Prog.StructMods[st]
	return mod != nil && mod.Pkg != nil && mod.Pkg.IsCore
}
