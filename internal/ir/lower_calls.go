package ir

import (
	"fmt"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

func (fl *funcLowerer) lowerCallExpr(call *ast.CallExpr, inf *sema.ExprInfo) string {
	switch inf.CallKind {
	case sema.CallDirectFunc:
		return fl.lowerDirectFuncCall(call, inf)
	case sema.CallClosureValue:
		return fl.lowerClosureValueCall(call, inf)
	case sema.CallExternC:
		return fl.lowerExternCCall(call, inf)
	case sema.CallTypeConvert:
		return fl.lowerTypeConvertCall(call, inf)
	case sema.CallInterfaceBox:
		return fl.lowerInterfaceBoxCall(call, inf)
	case sema.CallInterfaceMethod:
		return fl.lowerInterfaceMethodCall(call, inf)
	case sema.CallBuiltinFunc:
		return fl.lowerBuiltinFuncCall(call, inf)
	case sema.CallBuiltinMethod:
		return fl.lowerBuiltinMethodCall(call, inf)
	case sema.CallIntrinsic:
		return fl.lowerIntrinsicCall(call, inf)
	case sema.CallEnumVariant:
		return fl.lowerEnumVariantCall(call, inf)
	}
	return ""
}

func (fl *funcLowerer) lowerEnumVariantCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	en := inf.EnumType
	vr := inf.EnumVariant
	fl.tc.visit(en)
	slot := fl.allocSlot("_ll_evar", en)
	payloadVal := fl.lowerExprOwned(call.Args[0], vr.Payload)
	fl.emit(&InstrAssign{
		Dst:  fmt.Sprintf("%s.tag", slot.CName),
		Expr: fmt.Sprintf("%d", vr.Value),
	})
	fl.emit(&InstrAssign{
		Dst:  fmt.Sprintf("%s.data.%s", slot.CName, vr.CName),
		Expr: payloadVal,
	})
	return slot.CName
}

func (fl *funcLowerer) lowerCallArgs(params []types.FuncParam, args []ast.Expr) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		p := params[i]
		fl.tc.visit(p.Type)
		switch p.Mode {
		case types.ModeMut:
			if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.Amp {
				out[i] = fl.lowerLValueAddr(u.Operand)
			} else {
				out[i] = fl.lowerLValueAddr(arg)
			}
		case types.ModeOwn:
			out[i] = fl.lowerExprOwned(arg, p.Type)
		case types.ModeRead:
			out[i] = fl.lowerExprRead(arg)
		}
	}
	return out
}

func (fl *funcLowerer) lowerMethodReceiverArg(recv ast.Expr, mode types.ParamMode, targetRecvTy types.Type) string {
	rawRecvTy := fl.info(recv).Type
	if rf, isRef := rawRecvTy.(*types.Ref); isRef {
		_ = rf
		ptr := fl.lowerExprRead(recv)
		if mode == types.ModeMut {
			return ptr
		}
		return fmt.Sprintf("(*(%s))", ptr)
	}
	switch mode {
	case types.ModeMut:
		return fl.lowerLValueAddr(recv)
	case types.ModeOwn:
		return fl.lowerExprOwned(recv, targetRecvTy)
	default:
		return fl.lowerExprRead(recv)
	}
}

// -----------------------------------------------------------------------------
// Direct, Closure, Extern, Conversion, & Interface Calls
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerDirectFuncCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	fi := inf.TargetFunc

	// Handle UFCS optional-chained method call (`user?.full_name()`)
	if inf.MethodRecv != nil && inf.IsOptChain {
		optRecv := fl.info(inf.MethodRecv).Type.(*types.Optional)
		fl.tc.visit(optRecv)
		recvOptVal := fl.lowerExprRead(inf.MethodRecv)
		recvTmp := fl.allocSlot("_ll_mopt", optRecv)
		fl.assignLocal(recvTmp, recvOptVal)

		var resSlot *Local
		var resOpt *types.Optional
		if !fi.Sig.Ret.Equals(types.TypeVoid) {
			resOpt = inf.Type.(*types.Optional)
			fl.tc.visit(resOpt)
			resSlot = fl.allocSlot("_ll_mres", resOpt)
			fl.assignLocal(resSlot, optNoneExpr(resOpt))
		}

		fl.pushInstrBuf()
		unwrapped := optUnwrapValExpr(recvTmp.CName, optRecv)
		if rf, isRef := optRecv.Elem.(*types.Ref); isRef {
			_ = rf
			unwrapped = fmt.Sprintf("(*(%s))", unwrapped)
		}
		cArgs := make([]string, 0, 1+len(call.Args))
		cArgs = append(cArgs, unwrapped)
		cArgs = append(cArgs, fl.lowerCallArgs(fi.Sig.Params[1:], call.Args)...)
		callStr := fmt.Sprintf("%s(%s)", fi.CName, strings.Join(cArgs, ", "))
		if resSlot != nil {
			if _, retAlreadyOpt := fi.Sig.Ret.(*types.Optional); retAlreadyOpt {
				fl.assignLocal(resSlot, callStr)
			} else {
				fl.assignLocal(resSlot, fl.wrapInOptionalExpr(callStr, resOpt))
			}
		} else {
			fl.emit(&InstrExpr{Expr: callStr})
		}
		thenBuf := fl.popInstrBuf()

		fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp.CName, optRecv), Then: thenBuf})
		if resSlot != nil {
			return resSlot.CName
		}
		return ""
	}

	var cArgs []string
	if inf.MethodRecv != nil {
		recvArg := fl.lowerMethodReceiverArg(inf.MethodRecv, inf.MethodRecvMode, fi.Sig.Params[0].Type)
		cArgs = append(cArgs, recvArg)
		cArgs = append(cArgs, fl.lowerCallArgs(fi.Sig.Params[1:], call.Args)...)
	} else {
		cArgs = fl.lowerCallArgs(fi.Sig.Params, call.Args)
	}
	return fmt.Sprintf("%s(%s)", fi.CName, strings.Join(cArgs, ", "))
}

func (fl *funcLowerer) lowerClosureValueCall(call *ast.CallExpr, _ *sema.ExprInfo) string {
	calleeTy := fl.info(call.Callee).Type.(*types.Func)
	fl.tc.visit(calleeTy)
	calleeVal := fl.lowerExprRead(call.Callee)
	fnTmp := fl.allocSlot("_ll_clos", calleeTy)
	fl.assignLocal(fnTmp, calleeVal)

	cArgs := make([]string, 0, 1+len(call.Args))
	cArgs = append(cArgs, fnTmp.CName+".env")
	cArgs = append(cArgs, fl.lowerCallArgs(calleeTy.Params, call.Args)...)
	return fmt.Sprintf("%s.fn(%s)", fnTmp.CName, strings.Join(cArgs, ", "))
}

func (fl *funcLowerer) lowerExternCCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	ef := inf.TargetExtern
	cArgs := fl.lowerCallArgs(ef.Sig.Params, call.Args)
	for i, p := range ef.Sig.Params {
		if _, isPtr := p.Type.(*types.Ptr); isPtr {
			cArgs[i] = fmt.Sprintf("((void*)(uintptr_t)(%s))", cArgs[i])
		}
	}
	callExpr := fmt.Sprintf("%s(%s)", ef.Name, strings.Join(cArgs, ", "))
	if _, retPtr := ef.Sig.Ret.(*types.Ptr); retPtr {
		return fmt.Sprintf("((%s)(void*)(uintptr_t)(%s))", CType(ef.Sig.Ret), callExpr)
	}
	return callExpr
}

func (fl *funcLowerer) lowerTypeConvertCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	arg := call.Args[0]
	argTy := fl.info(arg).Type
	targetTy := inf.Type
	fl.tc.visit(targetTy)

	// `string(ch)` where `ch: char` -> UTF-8 single-character string
	if targetTy.Equals(types.TypeString) && argTy.Equals(types.TypeChar) {
		chVal := fl.lowerExprRead(arg)
		return fmt.Sprintf("ll_str_from_char(%s)", chVal)
	}

	// Distinct conversion (`UserID(x)` or `Base(uid)`): preserve ownership if Resource!
	if !targetTy.IsPlain() {
		val := fl.lowerExprOwned(arg, argTy)
		return fmt.Sprintf("((%s)(%s))", CType(targetTy), val)
	}

	val := fl.lowerExprRead(arg)
	// Float -> Int conversion: cast directly in C
	return fmt.Sprintf("((%s)(%s))", CType(targetTy), val)
}

func (fl *funcLowerer) lowerInterfaceBoxCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	arg := call.Args[0]
	argTy := fl.info(arg).Type
	ifc := inf.BoxInterface
	fl.tc.visit(ifc)

	if argTy.Equals(ifc) {
		return fl.lowerExprOwned(arg, ifc)
	}
	valExpr := fl.lowerExprOwned(arg, argTy)
	return fl.boxConcreteIntoInterface(valExpr, argTy, ifc)
}

func (fl *funcLowerer) lowerInterfaceMethodCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	im := inf.InterfaceMeth
	rawRecvTy := fl.info(inf.MethodRecv).Type

	if inf.IsOptChain {
		optRecv := rawRecvTy.(*types.Optional)
		ifc := optRecv.Elem.(*types.Interface)
		fl.tc.visit(optRecv)
		fl.tc.visit(ifc)

		recvTmp := fl.allocSlot("_ll_iopt", optRecv)
		fl.assignLocal(recvTmp, fl.lowerExprRead(inf.MethodRecv))

		var resSlot *Local
		var resOpt *types.Optional
		if !im.Ret.Equals(types.TypeVoid) {
			resOpt = inf.Type.(*types.Optional)
			fl.tc.visit(resOpt)
			resSlot = fl.allocSlot("_ll_ires", resOpt)
			fl.assignLocal(resSlot, optNoneExpr(resOpt))
		}

		fl.pushInstrBuf()
		ifcVal := optUnwrapValExpr(recvTmp.CName, optRecv)
		ifcTmp := fl.allocSlot("_ll_iv", ifc)
		fl.assignLocal(ifcTmp, ifcVal)
		cArgs := make([]string, 0, 1+len(call.Args))
		cArgs = append(cArgs, ifcTmp.CName+".data")
		cArgs = append(cArgs, fl.lowerCallArgs(im.Params, call.Args)...)
		callStr := fmt.Sprintf("%s.vtable->m_%s(%s)", ifcTmp.CName, im.Name, strings.Join(cArgs, ", "))
		if resSlot != nil {
			if _, retOpt := im.Ret.(*types.Optional); retOpt {
				fl.assignLocal(resSlot, callStr)
			} else {
				fl.assignLocal(resSlot, fl.wrapInOptionalExpr(callStr, resOpt))
			}
		} else {
			fl.emit(&InstrExpr{Expr: callStr})
		}
		thenBuf := fl.popInstrBuf()
		fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp.CName, optRecv), Then: thenBuf})
		if resSlot != nil {
			return resSlot.CName
		}
		return ""
	}

	var ifcExpr string
	var ifc *types.Interface
	if rf, isRef := rawRecvTy.(*types.Ref); isRef {
		ifc = rf.Elem.(*types.Interface)
		ptr := fl.lowerExprRead(inf.MethodRecv)
		ifcExpr = fmt.Sprintf("(*(%s))", ptr)
	} else {
		ifc = rawRecvTy.(*types.Interface)
		if inf.MethodRecvMode == types.ModeMut {
			ptr := fl.lowerLValueAddr(inf.MethodRecv)
			ifcExpr = fmt.Sprintf("(*(%s))", ptr)
		} else {
			ifcExpr = fl.lowerExprRead(inf.MethodRecv)
		}
	}
	fl.tc.visit(ifc)
	ifcTmp := fl.allocSlot("_ll_ifc", ifc)
	fl.assignLocal(ifcTmp, ifcExpr)

	cArgs := make([]string, 0, 1+len(call.Args))
	cArgs = append(cArgs, ifcTmp.CName+".data")
	cArgs = append(cArgs, fl.lowerCallArgs(im.Params, call.Args)...)
	return fmt.Sprintf("%s.vtable->m_%s(%s)", ifcTmp.CName, im.Name, strings.Join(cArgs, ", "))
}

// -----------------------------------------------------------------------------
// Universe Built-in Functions
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerBuiltinFuncCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	file, line := formatSpanLoc(call)
	switch inf.BuiltinName {
	case "print", "println", "eprint", "eprintln":
		isErr := strings.HasPrefix(inf.BuiltinName, "e")
		isLn := strings.HasSuffix(inf.BuiltinName, "ln")
		if len(call.Args) == 0 {
			return fmt.Sprintf("ll_print_str(ll_str_lit(\"\", 0LL), %d, %d)", boolToInt(isErr), boolToInt(isLn))
		}
		arg := call.Args[0]
		argTy := fl.info(arg).Type
		argExpr := fl.lowerExprRead(arg)
		if argTy.Equals(types.TypeString) {
			return fmt.Sprintf("ll_print_str(%s, %d, %d)", argExpr, boolToInt(isErr), boolToInt(isLn))
		}
		strExpr := fl.toStringCallExpr(argTy, argExpr)
		strTmp := fl.allocStmtTemp(types.TypeString, strExpr)
		return fmt.Sprintf("ll_print_str(%s, %d, %d)", strTmp, boolToInt(isErr), boolToInt(isLn))

	case "panic":
		msg := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_panic_str(%s, %q, %d)", msg, file, line)

	case "assert":
		cond := fl.lowerExprRead(call.Args[0])
		msg := `ll_str_lit("assertion failed", 16LL)`
		if len(call.Args) == 2 {
			msg = fl.lowerExprRead(call.Args[1])
		}
		return fmt.Sprintf("ll_assert(%s, %s, %q, %d)", cond, msg, file, line)

	case "parse_int":
		fl.tc.visit(inf.Type)
		s := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_parse_int(%s)", s)

	case "parse_float":
		fl.tc.visit(inf.Type)
		s := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_parse_float(%s)", s)

	case "sqrt":
		x := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("sqrt(%s)", x)

	case "abs":
		x := fl.lowerExprRead(call.Args[0])
		prim := types.UnderlyingPrimitive(inf.Type)
		if prim != nil && prim.IsFloat() {
			return fmt.Sprintf("((%s)fabs((double)(%s)))", CType(inf.Type), x)
		}
		bits := 64
		if prim != nil {
			bits = prim.BitWidth()
		}
		uTy := unsignedCTypeForWidth(bits)
		tmp := fl.allocSlot("_ll_abs", inf.Type)
		fl.assignLocal(tmp, x)
		return fmt.Sprintf("((%s) < 0 ? (%s)((%s)0U - (%s)(%s)) : (%s))",
			tmp.CName, CType(inf.Type), uTy, uTy, tmp.CName, tmp.CName)

	case "min", "max":
		a := fl.lowerExprRead(call.Args[0])
		b := fl.lowerExprRead(call.Args[1])
		ta := fl.allocSlot("_ll_mm_a", inf.Type)
		tb := fl.allocSlot("_ll_mm_b", inf.Type)
		fl.assignLocal(ta, a)
		fl.assignLocal(tb, b)
		op := "<"
		if inf.BuiltinName == "max" {
			op = ">"
		}
		return fmt.Sprintf("((%s %s %s) ? %s : %s)", ta.CName, op, tb.CName, ta.CName, tb.CName)

	case "read_file":
		fl.tc.visit(inf.Type)
		p := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_read_file(%s)", p)

	case "write_file":
		p := fl.lowerExprRead(call.Args[0])
		c := fl.lowerExprRead(call.Args[1])
		return fmt.Sprintf("ll_write_file(%s, %s)", p, c)

	case "args":
		fl.tc.visit(inf.Type)
		return "ll_get_cli_args()"

	case "exit":
		code := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_exit(%s)", code)
	}
	return ""
}

// -----------------------------------------------------------------------------
// Built-in Methods & Raw Memory Intrinsics
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerBuiltinMethodCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	// Static `Arena` constructors (`Arena.new()`, `Arena.with_capacity(n)`) have `MethodRecv == nil`!
	if inf.BuiltinName == "Arena.new" {
		return "ll_arena_new(4096LL)"
	}
	if inf.BuiltinName == "Arena.with_capacity" {
		capVal := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_arena_new(%s)", capVal)
	}

	// Optional-chained built-in method (`opt?.len()`, `opt?.to_string()`, `opt?.copy()`, etc.)!
	if inf.IsOptChain {
		optRecv := fl.info(inf.MethodRecv).Type.(*types.Optional)
		resOpt := inf.Type.(*types.Optional)
		fl.tc.visit(optRecv)
		fl.tc.visit(resOpt)

		recvTmp := fl.allocSlot("_ll_bopt", optRecv)
		fl.assignLocal(recvTmp, fl.lowerExprRead(inf.MethodRecv))
		resSlot := fl.allocSlot("_ll_bres", resOpt)
		fl.assignLocal(resSlot, optNoneExpr(resOpt))

		fl.pushInstrBuf()
		innerTy := optRecv.Elem
		unwrappedExpr := optUnwrapValExpr(recvTmp.CName, optRecv)
		if rf, isRef := innerTy.(*types.Ref); isRef {
			innerTy = rf.Elem
			unwrappedExpr = fmt.Sprintf("(*(%s))", unwrappedExpr)
		}
		innerCall, innerRetTy := fl.emitReadBuiltinMethodOnVal(call, inf, innerTy, unwrappedExpr)
		if _, retAlreadyOpt := innerRetTy.(*types.Optional); retAlreadyOpt {
			fl.assignLocal(resSlot, innerCall)
		} else {
			fl.assignLocal(resSlot, fl.wrapInOptionalExpr(innerCall, resOpt))
		}
		thenBuf := fl.popInstrBuf()

		fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp.CName, optRecv), Then: thenBuf})
		return resSlot.CName
	}

	rawRecvTy := fl.info(inf.MethodRecv).Type
	recvTy := rawRecvTy
	isArenaRef := false
	if rf, isRef := rawRecvTy.(*types.Ref); isRef {
		recvTy = rf.Elem
		isArenaRef = true
	}

	// Mutating built-in methods (`&self`)
	if inf.MethodRecvMode == types.ModeMut {
		var recvPtr string
		if isArenaRef {
			recvPtr = fl.lowerExprRead(inf.MethodRecv)
		} else {
			recvPtr = fl.lowerLValueAddr(inf.MethodRecv)
		}
		return fl.emitMutBuiltinMethodOnPtr(call, inf, recvTy, recvPtr)
	}

	// Read-only built-in methods (`self`)
	var recvVal string
	if isArenaRef {
		ptr := fl.lowerExprRead(inf.MethodRecv)
		recvVal = fmt.Sprintf("(*(%s))", ptr)
	} else {
		recvVal = fl.lowerExprRead(inf.MethodRecv)
	}
	resStr, _ := fl.emitReadBuiltinMethodOnVal(call, inf, recvTy, recvVal)
	return resStr
}

func (fl *funcLowerer) emitReadBuiltinMethodOnVal(
	call *ast.CallExpr,
	inf *sema.ExprInfo,
	recvTy types.Type,
	recvVal string,
) (string, types.Type) {
	file, line := formatSpanLoc(call)
	fl.tc.visit(recvTy)

	switch inf.BuiltinName {
	case "any.copy", "Interface.copy":
		return fl.copyCallExpr(recvTy, recvVal), recvTy

	case "any.to_string":
		return fl.toStringCallExpr(recvTy, recvVal), types.TypeString

	// `char` methods
	case "char.is_alpha":
		return fmt.Sprintf("ll_char_is_alpha(%s)", recvVal), types.TypeBool
	case "char.is_digit":
		return fmt.Sprintf("ll_char_is_digit(%s)", recvVal), types.TypeBool
	case "char.is_alnum":
		return fmt.Sprintf("ll_char_is_alnum(%s)", recvVal), types.TypeBool
	case "char.is_whitespace":
		return fmt.Sprintf("ll_char_is_whitespace(%s)", recvVal), types.TypeBool
	case "char.to_int":
		return fmt.Sprintf("((int64_t)(%s))", recvVal), types.TypeInt

	// `string` read methods
	case "string.len":
		return fmt.Sprintf("((%s).len)", recvVal), types.TypeInt
	case "string.is_empty":
		return fmt.Sprintf("((%s).len == 0LL)", recvVal), types.TypeBool
	case "string.byte_at":
		idx := fl.lowerExprRead(call.Args[0])
		sTmp := fl.allocSlot("_ll_sba", types.TypeString)
		fl.assignLocal(sTmp, recvVal)
		cIdx := fl.checkedIndexExpr(idx, sTmp.CName+".len", file, line)
		return fmt.Sprintf("((uint8_t)%s.data[%s])", sTmp.CName, cIdx), types.TypeU8
	case "string.get_byte":
		optU8 := types.NewOptional(types.TypeU8)
		fl.tc.visit(optU8)
		idx := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_get_byte(%s, %s)", recvVal, idx), optU8
	case "string.bytes":
		vecU8 := &types.Vector{Elem: types.TypeU8}
		fl.tc.visit(vecU8)
		return fmt.Sprintf("ll_str_bytes(%s)", recvVal), vecU8
	case "string.chars":
		vecCh := &types.Vector{Elem: types.TypeChar}
		fl.tc.visit(vecCh)
		return fmt.Sprintf("ll_str_chars(%s)", recvVal), vecCh
	case "string.contains":
		sub := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_contains(%s, %s)", recvVal, sub), types.TypeBool
	case "string.starts_with":
		sub := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_starts_with(%s, %s)", recvVal, sub), types.TypeBool
	case "string.ends_with":
		sub := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_ends_with(%s, %s)", recvVal, sub), types.TypeBool
	case "string.index_of":
		optInt := types.NewOptional(types.TypeInt)
		fl.tc.visit(optInt)
		sub := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_index_of(%s, %s)", recvVal, sub), optInt
	case "string.trim":
		return fmt.Sprintf("ll_str_trim(%s)", recvVal), types.TypeString
	case "string.to_upper":
		return fmt.Sprintf("ll_str_to_upper(%s)", recvVal), types.TypeString
	case "string.to_lower":
		return fmt.Sprintf("ll_str_to_lower(%s)", recvVal), types.TypeString
	case "string.split":
		vecStr := &types.Vector{Elem: types.TypeString}
		fl.tc.visit(vecStr)
		sep := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_split(%s, %s)", recvVal, sep), vecStr
	case "string.replace":
		oldS := fl.lowerExprRead(call.Args[0])
		newS := fl.lowerExprRead(call.Args[1])
		return fmt.Sprintf("ll_str_replace(%s, %s, %s)", recvVal, oldS, newS), types.TypeString
	case "string.c_str":
		return fmt.Sprintf("ll_str_c_str(%s)", recvVal), &types.Ptr{Elem: types.TypeU8}

	// `Vector` read methods
	case "vec.len":
		return fmt.Sprintf("((%s).len)", recvVal), types.TypeInt
	case "vec.cap":
		return fmt.Sprintf("((%s).cap)", recvVal), types.TypeInt
	case "vec.is_empty":
		return fmt.Sprintf("((%s).len == 0LL)", recvVal), types.TypeBool
	case "vec.get":
		vecTy := recvTy.(*types.Vector)
		optElem := types.NewOptional(vecTy.Elem)
		fl.tc.visit(optElem)
		idx := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_vec_get__%s(%s, %s)", vecTy.Mangled(), recvVal, idx), optElem
	case "vec.contains":
		vecTy := recvTy.(*types.Vector)
		needle := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_vec_contains__%s(%s, %s)", vecTy.Mangled(), recvVal, needle), types.TypeBool
	case "vec.join":
		sep := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_vec_join_str(%s, %s)", recvVal, sep), types.TypeString

	// `StackArray` read methods
	case "arr.len":
		arrTy := recvTy.(*types.StackArray)
		return fmt.Sprintf("%dLL", arrTy.Size), types.TypeInt
	case "arr.is_empty":
		return "false", types.TypeBool
	case "arr.get":
		arrTy := recvTy.(*types.StackArray)
		optElem := types.NewOptional(arrTy.Elem)
		fl.tc.visit(optElem)
		idx := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_arr_get__%s(%s, %s)", arrTy.Mangled(), recvVal, idx), optElem
	case "arr.contains":
		arrTy := recvTy.(*types.StackArray)
		needle := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_arr_contains__%s(%s, %s)", arrTy.Mangled(), recvVal, needle), types.TypeBool

	// `Set` read methods
	case "set.len":
		return fmt.Sprintf("((%s).len)", recvVal), types.TypeInt
	case "set.is_empty":
		return fmt.Sprintf("((%s).len == 0LL)", recvVal), types.TypeBool
	case "set.has":
		setTy := recvTy.(*types.Set)
		val := fl.lowerExprRead(call.Args[0])
		sTmp := fl.allocSlot("_ll_sh", setTy)
		fl.assignLocal(sTmp, recvVal)
		return fmt.Sprintf("ll_set_has__%s(&%s, %s)", setTy.Mangled(), sTmp.CName, val), types.TypeBool
	case "set.to_vec":
		setTy := recvTy.(*types.Set)
		vecTy := &types.Vector{Elem: setTy.Elem}
		fl.tc.visit(vecTy)
		sTmp := fl.allocSlot("_ll_stv", setTy)
		fl.assignLocal(sTmp, recvVal)
		return fmt.Sprintf("ll_set_to_vec__%s(&%s)", setTy.Mangled(), sTmp.CName), vecTy

	// `Map` read methods
	case "map.len":
		return fmt.Sprintf("((%s).len)", recvVal), types.TypeInt
	case "map.is_empty":
		return fmt.Sprintf("((%s).len == 0LL)", recvVal), types.TypeBool
	case "map.has":
		mapTy := recvTy.(*types.Map)
		key := fl.lowerExprRead(call.Args[0])
		mTmp := fl.allocSlot("_ll_mh", mapTy)
		fl.assignLocal(mTmp, recvVal)
		return fmt.Sprintf("ll_map_has__%s(&%s, %s)", mapTy.Mangled(), mTmp.CName, key), types.TypeBool
	case "map.keys":
		mapTy := recvTy.(*types.Map)
		vecTy := &types.Vector{Elem: mapTy.Key}
		fl.tc.visit(vecTy)
		mTmp := fl.allocSlot("_ll_mk", mapTy)
		fl.assignLocal(mTmp, recvVal)
		return fmt.Sprintf("ll_map_keys__%s(&%s)", mapTy.Mangled(), mTmp.CName), vecTy
	case "map.values":
		mapTy := recvTy.(*types.Map)
		vecTy := &types.Vector{Elem: mapTy.Value}
		fl.tc.visit(vecTy)
		mTmp := fl.allocSlot("_ll_mv", mapTy)
		fl.assignLocal(mTmp, recvVal)
		return fmt.Sprintf("ll_map_values__%s(&%s)", mapTy.Mangled(), mTmp.CName), vecTy

	// `Arena` read methods
	case "Arena.bytes_used":
		aTmp := fl.allocSlot("_ll_abu", types.TypeArena)
		fl.assignLocal(aTmp, recvVal)
		return fmt.Sprintf("ll_arena_bytes_used(&%s)", aTmp.CName), types.TypeInt
	}
	return "", types.TypeVoid
}

func (fl *funcLowerer) emitMutBuiltinMethodOnPtr(
	call *ast.CallExpr,
	inf *sema.ExprInfo,
	recvTy types.Type,
	recvPtr string,
) string {
	file, line := formatSpanLoc(call)
	fl.tc.visit(recvTy)

	switch inf.BuiltinName {
	// `string` mutating methods
	case "string.push":
		suf := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_push(%s, %s)", recvPtr, suf)
	case "string.push_char":
		ch := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_push_char(%s, %s)", recvPtr, ch)
	case "string.push_byte":
		b := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_str_push_byte(%s, %s)", recvPtr, b)
	case "string.push_bytes":
		p := fl.lowerExprRead(call.Args[0])
		cnt := fl.lowerExprRead(call.Args[1])
		return fmt.Sprintf("ll_str_push_bytes(%s, %s, %s)", recvPtr, p, cnt)
	case "string.clear":
		return fmt.Sprintf("ll_str_clear(%s)", recvPtr)

	// `Vector` mutating methods
	case "vec.push":
		vecTy := recvTy.(*types.Vector)
		val := fl.lowerExprOwned(call.Args[0], vecTy.Elem)
		return fmt.Sprintf("ll_vec_push__%s(%s, %s)", vecTy.Mangled(), recvPtr, val)
	case "vec.pop":
		vecTy := recvTy.(*types.Vector)
		fl.tc.visit(inf.Type)
		return fmt.Sprintf("ll_vec_pop__%s(%s)", vecTy.Mangled(), recvPtr)
	case "vec.remove":
		vecTy := recvTy.(*types.Vector)
		idx := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_vec_remove__%s(%s, %s, 1, %q, %d)", vecTy.Mangled(), recvPtr, idx, file, line)
	case "vec.swap_remove":
		vecTy := recvTy.(*types.Vector)
		idx := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_vec_swap_remove__%s(%s, %s, 1, %q, %d)", vecTy.Mangled(), recvPtr, idx, file, line)
	case "vec.clear":
		vecTy := recvTy.(*types.Vector)
		return fmt.Sprintf("ll_vec_clear__%s(%s)", vecTy.Mangled(), recvPtr)
	case "vec.reserve":
		vecTy := recvTy.(*types.Vector)
		capVal := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_vec_reserve__%s(%s, %s)", vecTy.Mangled(), recvPtr, capVal)

	// `Set` mutating methods
	case "set.insert":
		setTy := recvTy.(*types.Set)
		val := fl.lowerExprOwned(call.Args[0], setTy.Elem)
		return fmt.Sprintf("ll_set_insert__%s(%s, %s)", setTy.Mangled(), recvPtr, val)
	case "set.remove":
		setTy := recvTy.(*types.Set)
		val := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_set_remove__%s(%s, %s)", setTy.Mangled(), recvPtr, val)
	case "set.clear":
		setTy := recvTy.(*types.Set)
		return fmt.Sprintf("ll_set_clear__%s(%s)", setTy.Mangled(), recvPtr)

	// `Map` mutating methods
	case "map.insert":
		mapTy := recvTy.(*types.Map)
		k := fl.lowerExprOwned(call.Args[0], mapTy.Key)
		v := fl.lowerExprOwned(call.Args[1], mapTy.Value)
		return fmt.Sprintf("ll_map_insert__%s(%s, %s, %s)", mapTy.Mangled(), recvPtr, k, v)
	case "map.push":
		mapTy := recvTy.(*types.Map)
		entryTy := fl.info(call.Args[0]).Type
		entryVal := fl.lowerExprOwned(call.Args[0], entryTy)
		entrySlot := fl.allocSlot("_ll_entry", entryTy)
		fl.assignLocal(entrySlot, entryVal)
		return fmt.Sprintf("ll_map_insert__%s(%s, %s.ll_key, %s.ll_value)", mapTy.Mangled(), recvPtr, entrySlot.CName, entrySlot.CName)
	case "map.remove":
		mapTy := recvTy.(*types.Map)
		fl.tc.visit(inf.Type)
		k := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_map_remove__%s(%s, %s)", mapTy.Mangled(), recvPtr, k)
	case "map.clear":
		mapTy := recvTy.(*types.Map)
		return fmt.Sprintf("ll_map_clear__%s(%s)", mapTy.Mangled(), recvPtr)

	// `Arena` mutating methods
	case "Arena.alloc":
		elemTy := inf.IntrinsicType
		fl.tc.visit(elemTy)
		cElem := CType(elemTy)
		valExpr := fl.lowerExprOwned(call.Args[0], elemTy)
		dtorArg := "NULL"
		if !elemTy.IsPlain() {
			dtorArg = fmt.Sprintf("(void (*)(void*))%s", DropFnName(elemTy))
		}
		ptrSlot := fl.allocSlot("_ll_aptr", &types.Ref{Elem: elemTy})
		fl.assignLocal(ptrSlot, fmt.Sprintf("(%s*)ll_arena_alloc(%s, (int64_t)sizeof(%s), (int64_t)_Alignof(%s), %s)",
			cElem, recvPtr, cElem, cElem, dtorArg))
		fl.emit(&InstrAssign{Dst: fmt.Sprintf("(*%s)", ptrSlot.CName), Expr: valExpr})
		return ptrSlot.CName

	case "Arena.reset":
		return fmt.Sprintf("ll_arena_reset(%s)", recvPtr)
	}
	return ""
}

func (fl *funcLowerer) lowerIntrinsicCall(call *ast.CallExpr, inf *sema.ExprInfo) string {
	switch inf.BuiltinName {
	case "size_of":
		fl.tc.visit(inf.IntrinsicType)
		return fmt.Sprintf("((int64_t)sizeof(%s))", CType(inf.IntrinsicType))
	case "align_of":
		fl.tc.visit(inf.IntrinsicType)
		return fmt.Sprintf("((int64_t)_Alignof(%s))", CType(inf.IntrinsicType))
	case "alloc_raw":
		fl.tc.visit(inf.IntrinsicType)
		cElem := CType(inf.IntrinsicType)
		cnt := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("((%s*)ll_alloc((%s) * (int64_t)sizeof(%s)))", cElem, cnt, cElem)
	case "realloc_raw":
		fl.tc.visit(inf.IntrinsicType)
		cElem := CType(inf.IntrinsicType)
		p := fl.lowerExprRead(call.Args[0])
		cnt := fl.lowerExprRead(call.Args[1])
		return fmt.Sprintf("((%s*)ll_realloc((void*)(%s), (%s) * (int64_t)sizeof(%s)))", cElem, p, cnt, cElem)
	case "free_raw":
		p := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("ll_free((void*)(%s))", p)
	case "null_ptr":
		return fmt.Sprintf("((%s)NULL)", CType(inf.Type))
	case "is_null":
		p := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("((%s) == NULL)", p)
	case "addr_of":
		return fl.lowerLValueAddr(call.Args[0])
	case "ptr_add":
		p := fl.lowerExprRead(call.Args[0])
		off := fl.lowerExprRead(call.Args[1])
		return fmt.Sprintf("((%s) + (%s))", p, off)
	case "ptr_read":
		p := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("(*(%s))", p)
	case "ptr_write":
		p := fl.lowerExprRead(call.Args[0])
		v := fl.lowerExprOwned(call.Args[1], inf.IntrinsicType)
		pTmp := fl.allocSlot("_ll_pw", fl.info(call.Args[0]).Type)
		fl.assignLocal(pTmp, p)
		fl.emit(&InstrAssign{Dst: fmt.Sprintf("(*%s)", pTmp.CName), Expr: v})
		return ""
	case "ptr_cast":
		src := fl.lowerExprRead(call.Args[0])
		srcTy := fl.info(call.Args[0]).Type
		if _, isP := srcTy.(*types.Ptr); isP {
			return fmt.Sprintf("((%s)(%s))", CType(inf.Type), src)
		}
		return fmt.Sprintf("((%s)(uintptr_t)(%s))", CType(inf.Type), src)
	case "ptr_addr":
		p := fl.lowerExprRead(call.Args[0])
		return fmt.Sprintf("((uint64_t)(uintptr_t)(%s))", p)
	case "copy_bytes":
		dst := fl.lowerExprRead(call.Args[0])
		src := fl.lowerExprRead(call.Args[1])
		cnt := fl.lowerExprRead(call.Args[2])
		return fmt.Sprintf("ll_copy_bytes(%s, %s, %s)", dst, src, cnt)
	case "zero_bytes":
		dst := fl.lowerExprRead(call.Args[0])
		cnt := fl.lowerExprRead(call.Args[1])
		return fmt.Sprintf("ll_zero_bytes(%s, %s)", dst, cnt)
	}
	return ""
}
