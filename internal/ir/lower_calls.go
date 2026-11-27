package ir

import (
	"fmt"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

func (fl *funcLowerer) lowerCallExpr(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
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
	return nil
}

func (fl *funcLowerer) lowerEnumVariantCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	en := inf.EnumType
	vr := inf.EnumVariant
	fl.tc.visit(en)
	payloadVal := fl.lowerExprOwned(call.Args[0], vr.Payload)
	return &ExprConstructEnum{
		EnumType: en,
		Variant:  vr,
		Payload:  EnsureExpr(payloadVal, vr.Payload),
	}
}

func (fl *funcLowerer) lowerCallArgs(params []types.FuncParam, args []ast.Expr) []Expr {
	out := make([]Expr, len(args))
	for i, arg := range args {
		p := params[i]
		fl.tc.visit(p.Type)
		switch p.Mode {
		case types.ModeMut:
			if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.Amp {
				out[i] = fl.lowerLValueAddrMut(u.Operand)
			} else {
				out[i] = fl.lowerLValueAddrMut(arg)
			}
		case types.ModeOwn:
			out[i] = fl.lowerExprOwned(arg, p.Type)
		case types.ModeRead:
			argInf := fl.info(arg)
			if arrTy, isArr := argInf.Type.(*types.StackArray); isArr {
				if arrSt, isArrSt := p.Type.(*types.Struct); isArrSt &&
					fl.prog != nil && fl.prog.GenStructArray != nil &&
					fl.prog.StructMods[arrSt] == fl.prog.GenStructArray.Mod && len(arrSt.TypeArgs) == 1 {
					arrAddr := fl.lowerStackArrayAddr(arg, arrTy)
					out[i] = &ExprConstructStruct{
						StructType: arrSt,
						Fields: []StructFieldInit{
							{Name: "data", Value: &ExprField{Base: arrAddr, FieldName: "data", Typ: &types.Ptr{Elem: arrTy.Elem}, IsPtr: true}},
							{Name: "len", Value: NewLitInt(uint64(arrTy.Size), false, types.TypeInt)},
						},
					}
					continue
				}
			}
			out[i] = fl.lowerExprRead(arg)
		}
	}
	return out
}

func (fl *funcLowerer) lowerMethodReceiverArg(recv ast.Expr, mode types.ParamMode, targetRecvTy types.Type) Expr {
	rawRecvTy := fl.info(recv).Type
	if rf, isRef := rawRecvTy.(*types.Ref); isRef {
		_ = rf
		ptr := fl.lowerExprRead(recv)
		if mode == types.ModeMut {
			return ptr
		}
		return &ExprDeref{Ptr: EnsureExpr(ptr, rawRecvTy), Typ: rf.Elem}
	}
	switch mode {
	case types.ModeMut:
		return fl.lowerLValueAddrMut(recv)
	case types.ModeOwn:
		return fl.lowerExprOwned(recv, targetRecvTy)
	default:
		return fl.lowerExprRead(recv)
	}
}

// -----------------------------------------------------------------------------
// Direct, Closure, Extern, Conversion, & Interface Calls
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerDirectFuncCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	fi := inf.TargetFunc

	// Handle UFCS optional-chained method call (`user?.full_name()`)
	if inf.MethodRecv != nil && inf.IsOptChain {
		optRecv := fl.info(inf.MethodRecv).Type.(*types.Optional)
		fl.tc.visit(optRecv)

		var resSlot *Local
		var resOpt *types.Optional
		if !fi.Sig.Ret.Equals(types.TypeVoid) {
			resOpt = inf.Type.(*types.Optional)
			fl.tc.visit(resOpt)
			resSlot = fl.allocSlot("_ll_mres", resOpt)
			fl.assignLocal(resSlot, optNoneExpr(resOpt))
		}

		emitCallWithUnwrapped := func(unwrapped Expr) {
			cArgs := make([]Expr, 0, 1+len(call.Args))
			cArgs = append(cArgs, unwrapped)
			for _, a := range fl.lowerCallArgs(fi.Sig.Params[1:], call.Args) {
				cArgs = append(cArgs, a)
			}
			if fi.TrackCaller {
				if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerFileLoc != nil {
					cArgs = append(cArgs,
						NewLocalExpr(fl.callerFileLoc),
						NewLocalExpr(fl.callerLineLoc),
						NewLocalExpr(fl.callerColLoc),
					)
				} else {
					file, line, col := formatSpanPos(call)
					cArgs = append(cArgs,
						NewLitString(file),
						NewLitInt(uint64(line), false, types.TypeInt),
						NewLitInt(uint64(col), false, types.TypeInt),
					)
				}
			}
			callExpr := &ExprCall{
				Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
				Args:   cArgs,
				Typ:    fi.Sig.Ret,
			}
			if resSlot != nil {
				if _, retAlreadyOpt := fi.Sig.Ret.(*types.Optional); retAlreadyOpt {
					fl.assignLocal(resSlot, callExpr)
				} else {
					fl.assignLocal(resSlot, fl.wrapInOptionalExpr(callExpr, resOpt))
				}
			} else {
				fl.emit(&InstrExpr{Expr: callExpr})
			}
		}

		if !fl.lowerOptChainBorrow(inf.MethodRecv, optRecv, func(elemPtr Expr, elemTy types.Type) {
			emitCallWithUnwrapped(&ExprDeref{Ptr: elemPtr, Typ: elemTy})
		}) {
			recvOptVal := fl.lowerExprRead(inf.MethodRecv)
			recvTmp := fl.allocSlot("_ll_mopt", optRecv)
			fl.assignLocal(recvTmp, recvOptVal)

			fl.pushInstrBuf()
			var unwrapped Expr = optUnwrapValExpr(recvTmp, optRecv)
			if rf, isRef := optRecv.Elem.(*types.Ref); isRef {
				unwrapped = &ExprDeref{Ptr: unwrapped, Typ: rf.Elem}
			}
			emitCallWithUnwrapped(unwrapped)
			thenBuf := fl.popInstrBuf()

			fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp, optRecv), Then: thenBuf})
		}
		if resSlot != nil {
			return NewLocalExpr(resSlot)
		}
		return nil
	}

	var cArgs []Expr
	if inf.MethodRecv != nil {
		recvArg := fl.lowerMethodReceiverArg(inf.MethodRecv, inf.MethodRecvMode, fi.Sig.Params[0].Type)
		cArgs = append(cArgs, recvArg)
		for _, a := range fl.lowerCallArgs(fi.Sig.Params[1:], call.Args) {
			cArgs = append(cArgs, a)
		}
	} else {
		for _, a := range fl.lowerCallArgs(fi.Sig.Params, call.Args) {
			cArgs = append(cArgs, a)
		}
	}
	if fi.TrackCaller {
		if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerFileLoc != nil {
			cArgs = append(cArgs,
				NewLocalExpr(fl.callerFileLoc),
				NewLocalExpr(fl.callerLineLoc),
				NewLocalExpr(fl.callerColLoc),
			)
		} else {
			file, line, col := formatSpanPos(call)
			cArgs = append(cArgs,
				NewLitString(file),
				NewLitInt(uint64(line), false, types.TypeInt),
				NewLitInt(uint64(col), false, types.TypeInt),
			)
		}
	}
	return &ExprCall{
		Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
		Args:   cArgs,
		Typ:    fi.Sig.Ret,
	}
}

func (fl *funcLowerer) lowerClosureValueCall(call *ast.CallExpr, _ *sema.ExprInfo) Expr {
	calleeTy := fl.info(call.Callee).Type.(*types.Func)
	fl.tc.visit(calleeTy)
	calleeVal := fl.lowerExprRead(call.Callee)
	if calleeTy.Extern {
		// An `extern fn` value is a C function pointer: just the arguments.
		return &ExprCall{Callee: EnsureExpr(calleeVal, calleeTy), Args: fl.lowerCallArgs(calleeTy.Params, call.Args), Typ: calleeTy.Ret}
	}
	if !calleeTy.Closure {
		// An `fn` value is the entry function itself; it gets no environment.
		cArgs := []Expr{&ExprLitZero{Typ: &types.Ptr{Elem: types.TypeVoid}}}
		cArgs = append(cArgs, fl.lowerCallArgs(calleeTy.Params, call.Args)...)
		return &ExprCall{Callee: EnsureExpr(calleeVal, calleeTy), Args: cArgs, Typ: calleeTy.Ret}
	}
	fnTmp := fl.allocSlot("_ll_clos", calleeTy)
	fl.assignLocal(fnTmp, calleeVal)

	cArgs := make([]Expr, 0, 1+len(call.Args))
	cArgs = append(cArgs, &ExprField{Base: NewLocalExpr(fnTmp), FieldName: "env", Typ: &types.Ptr{Elem: types.TypeVoid}})
	for _, a := range fl.lowerCallArgs(calleeTy.Params, call.Args) {
		cArgs = append(cArgs, a)
	}
	return &ExprCall{
		Callee: &ExprField{Base: NewLocalExpr(fnTmp), FieldName: "fn", Typ: calleeTy},
		Args:   cArgs,
		Typ:    calleeTy.Ret,
	}
}

func (fl *funcLowerer) lowerExternCCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	ef := inf.TargetExtern
	rawArgs := fl.lowerCallArgs(ef.Sig.Params, call.Args)
	cArgs := make([]Expr, len(rawArgs))
	for i, p := range ef.Sig.Params {
		argExpr := rawArgs[i]
		if _, isPtr := p.Type.(*types.Ptr); isPtr {
			argExpr = &ExprCast{Val: argExpr, From: p.Type, To: &types.Ptr{Elem: types.TypeVoid}}
		}
		cArgs[i] = argExpr
	}
	var callExpr Expr = &ExprCall{
		Callee: &ExprFuncRef{Name: ef.Name, ExternFunc: ef, FuncType: ef.Sig},
		Args:   cArgs,
		Typ:    ef.Sig.Ret,
	}
	if _, isPtr := ef.Sig.Ret.(*types.Ptr); isPtr {
		callExpr = &ExprCast{
			Val:  callExpr,
			From: &types.Ptr{Elem: types.TypeVoid},
			To:   ef.Sig.Ret,
		}
	}
	return callExpr
}

func (fl *funcLowerer) lookupCoreFunc(name string) *sema.FuncInstance {
	for _, mod := range fl.prog.Modules {
		if sym, ok := mod.Symbols[name]; ok && sym.FuncInst != nil {
			return sym.FuncInst
		}
	}
	return nil
}

func (fl *funcLowerer) lowerTypeConvertCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	arg := call.Args[0]
	argTy := fl.info(arg).Type
	targetTy := inf.Type
	fl.tc.visit(targetTy)

	// `string(ch)` where `ch: char` -> UTF-8 single-character string via `char.to_string` in `std/core.ll`
	if targetTy.Equals(types.TypeString) && argTy.Equals(types.TypeChar) {
		chVal := fl.lowerExprRead(arg)
		if fi := fl.lookupMethod(types.TypeChar, "to_string"); fi != nil {
			return &ExprCall{
				Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
				Args:   []Expr{EnsureExpr(chVal, types.TypeChar)},
				Typ:    types.TypeString,
			}
		}
	}

	// `char(n)` from an integer: panic unless `n` is a Unicode scalar value.
	if targetTy.Equals(types.TypeChar) {
		if prim, ok := argTy.(*types.Primitive); ok && prim.IsInteger() {
			file, line := formatSpanLoc(call)
			val := fl.lowerExprRead(arg)
			var asInt Expr = EnsureExpr(val, argTy)
			if !argTy.Equals(types.TypeInt) {
				asInt = &ExprCast{Val: asInt, From: argTy, To: types.TypeInt}
			}
			checked := &ExprCall{
				Callee: &ExprFuncRef{Name: "ll_check_char"},
				Args:   []Expr{asInt, &ExprLitCString{Val: file}, NewLitInt(uint64(line), false, types.TypeInt)},
				Typ:    types.TypeInt,
			}
			return &ExprCast{Val: checked, From: types.TypeInt, To: types.TypeChar}
		}
	}

	// Distinct conversion (`UserID(x)` or `Base(uid)`): preserve ownership if Resource!
	if !targetTy.IsPlain() {
		val := fl.lowerExprOwned(arg, argTy)
		return &ExprCast{Val: val, From: argTy, To: targetTy}
	}

	val := fl.lowerExprRead(arg)
	return &ExprCast{Val: val, From: argTy, To: targetTy}
}

func (fl *funcLowerer) lowerInterfaceBoxCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
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

func (fl *funcLowerer) lowerInterfaceMethodCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	im := inf.InterfaceMeth
	rawRecvTy := fl.info(inf.MethodRecv).Type

	if inf.IsOptChain {
		optRecv := rawRecvTy.(*types.Optional)
		ifc := optRecv.Elem.(*types.Interface)
		fl.tc.visit(optRecv)
		fl.tc.visit(ifc)

		var resSlot *Local
		var resOpt *types.Optional
		if !im.Ret.Equals(types.TypeVoid) {
			resOpt = inf.Type.(*types.Optional)
			fl.tc.visit(resOpt)
			resSlot = fl.allocSlot("_ll_ires", resOpt)
			fl.assignLocal(resSlot, optNoneExpr(resOpt))
		}

		emitIfaceCallWithVal := func(ifcVal Expr) {
			ifcTmp := fl.allocSlot("_ll_iv", ifc)
			fl.assignLocal(ifcTmp, ifcVal)

			cArgs := make([]Expr, 0, 1+len(call.Args))
			cArgs = append(cArgs, &ExprField{Base: NewLocalExpr(ifcTmp), FieldName: "data", Typ: &types.Ptr{Elem: types.TypeVoid}})
			for _, a := range fl.lowerCallArgs(im.Params, call.Args) {
				cArgs = append(cArgs, a)
			}
			vtField := &ExprField{Base: NewLocalExpr(ifcTmp), FieldName: "vtable", Typ: &types.Ptr{Elem: types.TypeVoid}}
			fnField := &ExprField{Base: vtField, FieldName: "m_" + im.Name, Typ: &types.Func{Params: im.Params, Ret: im.Ret}}
			callExpr := &ExprCall{Callee: fnField, Args: cArgs, Typ: im.Ret}

			if resSlot != nil {
				if _, retOpt := im.Ret.(*types.Optional); retOpt {
					fl.assignLocal(resSlot, callExpr)
				} else {
					fl.assignLocal(resSlot, fl.wrapInOptionalExpr(callExpr, resOpt))
				}
			} else {
				fl.emit(&InstrExpr{Expr: callExpr})
			}
		}

		if !fl.lowerOptChainBorrow(inf.MethodRecv, optRecv, func(elemPtr Expr, elemTy types.Type) {
			emitIfaceCallWithVal(&ExprDeref{Ptr: elemPtr, Typ: elemTy})
		}) {
			recvTmp := fl.allocSlot("_ll_iopt", optRecv)
			fl.assignLocal(recvTmp, fl.lowerExprRead(inf.MethodRecv))

			fl.pushInstrBuf()
			emitIfaceCallWithVal(optUnwrapValExpr(recvTmp, optRecv))
			thenBuf := fl.popInstrBuf()
			fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp, optRecv), Then: thenBuf})
		}
		if resSlot != nil {
			return NewLocalExpr(resSlot)
		}
		return nil
	}

	var ifcExpr Expr
	var ifc *types.Interface
	if rf, isRef := rawRecvTy.(*types.Ref); isRef {
		ifc = rf.Elem.(*types.Interface)
		ptr := fl.lowerExprRead(inf.MethodRecv)
		ifcExpr = &ExprDeref{Ptr: ptr, Typ: ifc}
	} else {
		ifc = rawRecvTy.(*types.Interface)
		if inf.MethodRecvMode == types.ModeMut {
			ptr := fl.lowerLValueAddrMut(inf.MethodRecv)
			ifcExpr = &ExprDeref{Ptr: ptr, Typ: ifc}
		} else {
			ifcExpr = fl.lowerExprRead(inf.MethodRecv)
		}
	}
	fl.tc.visit(ifc)
	ifcTmp := fl.allocSlot("_ll_ifc", ifc)
	fl.assignLocal(ifcTmp, ifcExpr)

	cArgs := make([]Expr, 0, 1+len(call.Args))
	cArgs = append(cArgs, &ExprField{Base: NewLocalExpr(ifcTmp), FieldName: "data", Typ: &types.Ptr{Elem: types.TypeVoid}})
	for _, a := range fl.lowerCallArgs(im.Params, call.Args) {
		cArgs = append(cArgs, a)
	}
	vtField := &ExprField{Base: NewLocalExpr(ifcTmp), FieldName: "vtable", Typ: &types.Ptr{Elem: types.TypeVoid}}
	fnField := &ExprField{Base: vtField, FieldName: "m_" + im.Name, Typ: &types.Func{Params: im.Params, Ret: im.Ret}}
	return &ExprCall{Callee: fnField, Args: cArgs, Typ: im.Ret}
}

// -----------------------------------------------------------------------------
// Universe Built-in Functions
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerBuiltinFuncCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	switch inf.BuiltinName {
	case "cl":
		// `cl(f)`: the argument carries the conversion (ToClosure).
		return fl.lowerExprOwned(call.Args[0], inf.Type)
	case "print", "println", "eprint", "eprintln":
		isErr := strings.HasPrefix(inf.BuiltinName, "e")
		isLn := strings.HasSuffix(inf.BuiltinName, "ln")
		if len(call.Args) == 0 {
			return &ExprCall{
				Callee: &ExprFuncRef{Name: "ll_print_str"},
				Args: []Expr{
					NewLitString(""),
					NewLitBool(isErr),
					NewLitBool(isLn),
				},
				Typ: types.TypeVoid,
			}
		}
		arg := call.Args[0]
		argTy := fl.info(arg).Type
		argExpr := fl.lowerExprRead(arg)
		if argTy.Equals(types.TypeString) {
			return &ExprCall{
				Callee: &ExprFuncRef{Name: "ll_print_str"},
				Args: []Expr{
					EnsureExpr(argExpr, types.TypeString),
					NewLitBool(isErr),
					NewLitBool(isLn),
				},
				Typ: types.TypeVoid,
			}
		}
		strExpr := fl.toStringCallExpr(argTy, argExpr)
		strTmp := fl.allocStmtTemp(types.TypeString, strExpr)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_print_str"},
			Args: []Expr{
				EnsureExpr(strTmp, types.TypeString),
				NewLitBool(isErr),
				NewLitBool(isLn),
			},
			Typ: types.TypeVoid,
		}

	case "panic":
		var msg Expr
		if argTy := fl.info(call.Args[0]).Type; argTy.Equals(types.TypeString) {
			msg = fl.lowerExprRead(call.Args[0])
		} else {
			// `panic(err: Error)`: report the error's full formatted message and trace.
			msg = NewLocalExpr(fl.allocStmtTemp(types.TypeString, fl.toStringCallExpr(argTy, fl.lowerExprRead(call.Args[0]))))
		}
		var fileExpr, lineExpr, colExpr Expr
		if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerFileLoc != nil {
			fileExpr = NewLocalExpr(fl.callerFileLoc)
			lineExpr = NewLocalExpr(fl.callerLineLoc)
			colExpr = NewLocalExpr(fl.callerColLoc)
		} else {
			f, l, c := formatSpanPos(call)
			fileExpr = NewLitString(f)
			lineExpr = NewLitInt(uint64(l), false, types.TypeInt)
			colExpr = NewLitInt(uint64(c), false, types.TypeInt)
		}
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_panic_str"},
			Args: []Expr{
				EnsureExpr(msg, types.TypeString),
				EnsureExpr(fileExpr, types.TypeString),
				lineExpr,
				colExpr,
			},
			Typ: types.TypeNever,
		}

	case "assert":
		cond := fl.lowerExprRead(call.Args[0])
		var msg Expr = NewLitString("assertion failed")
		if len(call.Args) == 2 {
			msg = fl.lowerExprRead(call.Args[1])
		}
		var fileExpr, lineExpr, colExpr Expr
		if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerFileLoc != nil {
			fileExpr = NewLocalExpr(fl.callerFileLoc)
			lineExpr = NewLocalExpr(fl.callerLineLoc)
			colExpr = NewLocalExpr(fl.callerColLoc)
		} else {
			f, l, c := formatSpanPos(call)
			fileExpr = NewLitString(f)
			lineExpr = NewLitInt(uint64(l), false, types.TypeInt)
			colExpr = NewLitInt(uint64(c), false, types.TypeInt)
		}
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_assert"},
			Args: []Expr{
				EnsureExpr(cond, types.TypeBool),
				EnsureExpr(msg, types.TypeString),
				EnsureExpr(fileExpr, types.TypeString),
				lineExpr,
				colExpr,
			},
			Typ: types.TypeVoid,
		}

	case "caller_location":
		if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerFileLoc != nil {
			locFn := "ll_format_loc"
			if fl.prog.FormatLocFunc != nil {
				locFn = fl.prog.FormatLocFunc.CName
			}
			return &ExprCall{
				Callee: &ExprFuncRef{Name: locFn},
				Args: []Expr{
					NewLocalExpr(fl.callerFileLoc),
					NewLocalExpr(fl.callerLineLoc),
					NewLocalExpr(fl.callerColLoc),
				},
				Typ: types.TypeString,
			}
		}
		f, l, c := formatSpanPos(call)
		return NewLitString(fmt.Sprintf("%s:%d:%d", f, l, c))

	case "caller_file":
		if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerFileLoc != nil {
			return NewLocalExpr(fl.callerFileLoc)
		}
		f, _, _ := formatSpanPos(call)
		return NewLitString(f)

	case "caller_line":
		if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerLineLoc != nil {
			return NewLocalExpr(fl.callerLineLoc)
		}
		_, l, _ := formatSpanPos(call)
		return NewLitInt(uint64(l), false, types.TypeInt)

	case "caller_col":
		if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerColLoc != nil {
			return NewLocalExpr(fl.callerColLoc)
		}
		_, _, c := formatSpanPos(call)
		return NewLitInt(uint64(c), false, types.TypeInt)

	case "abs":
		x := fl.lowerExprRead(call.Args[0])
		tmp := fl.allocSlot("_ll_abs", inf.Type)
		fl.assignLocal(tmp, x)
		res := fl.allocSlot("_ll_abs_res", inf.Type)
		neg := &ExprUnary{Op: token.Minus, Operand: NewLocalExpr(tmp), Typ: inf.Type}
		var zeroExpr Expr = NewLitInt(0, false, inf.Type)
		if prim := types.UnderlyingPrimitive(inf.Type); prim != nil && prim.IsFloat() {
			zeroExpr = NewLitFloat(0.0, inf.Type)
		}
		fl.emit(&InstrIf{
			Cond: &ExprBinary{Op: token.Lt, Left: NewLocalExpr(tmp), Right: zeroExpr, Typ: types.TypeBool},
			Then: []Instr{NewAssign(res, neg)},
			Else: []Instr{NewAssign(res, tmp)},
		})
		return NewLocalExpr(res)

	case "min", "max":
		a := fl.lowerExprRead(call.Args[0])
		b := fl.lowerExprRead(call.Args[1])
		ta := fl.allocSlot("_ll_mm_a", inf.Type)
		tb := fl.allocSlot("_ll_mm_b", inf.Type)
		fl.assignLocal(ta, a)
		fl.assignLocal(tb, b)
		op := token.Lt
		if inf.BuiltinName == "max" {
			op = token.Gt
		}
		cond := &ExprBinary{Op: op, Left: NewLocalExpr(ta), Right: NewLocalExpr(tb), Typ: types.TypeBool}
		res := fl.allocSlot("_ll_mm_res", inf.Type)
		fl.emit(&InstrIf{
			Cond: cond,
			Then: []Instr{NewAssign(res, ta)},
			Else: []Instr{NewAssign(res, tb)},
		})
		return NewLocalExpr(res)

	case "args":
		fl.tc.visit(inf.Type)
		if fi := fl.lookupCoreFunc("core_cli_args"); fi != nil {
			return &ExprCall{
				Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
				Args:   nil,
				Typ:    inf.Type,
			}
		}
		return nil

	case "exit":
		code := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_exit"},
			Args:   []Expr{EnsureExpr(code, types.TypeInt)},
			Typ:    types.TypeNever,
		}

	case "variant_index":
		en := inf.EnumType
		fl.tc.visit(en)
		argVal := fl.lowerExprRead(call.Args[0])
		if _, isRef := fl.info(call.Args[0]).Type.(*types.Ref); isRef {
			argVal = &ExprDeref{Ptr: argVal, Typ: en}
		}
		return fl.emitEnumVariantIdxExpr(en, argVal)

	case "make_variant":
		en := inf.EnumType
		vr := inf.EnumVariant
		fl.tc.visit(en)
		var payload Expr
		if vr != nil && vr.Kind == types.VariantTuple && vr.Payload != nil && len(call.Args) >= 2 {
			payload = EnsureExpr(fl.lowerExprOwned(call.Args[1], vr.Payload), vr.Payload)
		}
		return &ExprConstructEnum{
			EnumType: en,
			Variant:  vr,
			Payload:  payload,
		}
	}
	return nil
}

func enumVariantTagEqExpr(valExpr Expr, en *types.Enum, vr *types.EnumVariant) Expr {
	if en.IsTagged {
		return &ExprBinary{
			Op:    token.Eq,
			Left:  &ExprField{Base: EnsureExpr(valExpr, en), FieldName: "tag", Typ: types.TypeInt},
			Right: NewLitInt64(vr.Value),
			Typ:   types.TypeBool,
		}
	}
	return &ExprBinary{
		Op:    token.Eq,
		Left:  EnsureExpr(valExpr, en),
		Right: &ExprConstructEnum{EnumType: en, Variant: vr},
		Typ:   types.TypeBool,
	}
}

func (fl *funcLowerer) emitEnumTagAssert(enumExpr Expr, en *types.Enum, vr *types.EnumVariant, isPtr bool, call ast.Expr) {
	var tagExpr Expr
	if isPtr {
		tagExpr = &ExprField{Base: EnsureExpr(enumExpr, &types.Ptr{Elem: en}), FieldName: "tag", Typ: types.TypeInt, IsPtr: true}
	} else {
		tagExpr = &ExprField{Base: EnsureExpr(enumExpr, en), FieldName: "tag", Typ: types.TypeInt, IsPtr: false}
	}
	cond := &ExprBinary{
		Op:    token.Neq,
		Left:  tagExpr,
		Right: NewLitInt(uint64(vr.Value), vr.Value < 0, types.TypeInt),
		Typ:   types.TypeBool,
	}
	var fileExpr, lineExpr, colExpr Expr
	if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerFileLoc != nil {
		fileExpr = NewLocalExpr(fl.callerFileLoc)
		lineExpr = NewLocalExpr(fl.callerLineLoc)
		colExpr = NewLocalExpr(fl.callerColLoc)
	} else {
		f, l, c := formatSpanPos(call)
		fileExpr = NewLitString(f)
		lineExpr = NewLitInt(uint64(l), false, types.TypeInt)
		colExpr = NewLitInt(uint64(c), false, types.TypeInt)
	}
	msg := fmt.Sprintf("variant payload access failed: expected %s.%s", en.Name, vr.Name)
	fl.emit(&InstrIf{
		Cond: cond,
		Then: []Instr{
			&InstrExpr{
				Expr: &ExprCall{
					Callee: &ExprFuncRef{Name: "ll_panic_str"},
					Args:   []Expr{NewLitString(msg), fileExpr, lineExpr, colExpr},
					Typ:    types.TypeNever,
				},
			},
		},
	})
}

func (fl *funcLowerer) emitEnumVariantIdxExpr(en *types.Enum, valExpr Expr) Expr {
	allSeq := true
	for i, vr := range en.Variants {
		if vr.Value != int64(i) {
			allSeq = false
			break
		}
	}
	if allSeq {
		if en.IsTagged {
			return &ExprField{Base: EnsureExpr(valExpr, en), FieldName: "tag", Typ: types.TypeInt}
		}
		return &ExprCast{Val: EnsureExpr(valExpr, en), From: en, To: types.TypeInt}
	}
	vTmp := fl.allocSlot("_ll_vidx_v", en)
	fl.assignLocal(vTmp, EnsureExpr(valExpr, en))
	resSlot := fl.allocSlot("_ll_vidx", types.TypeInt)
	fl.assignLocal(resSlot, NewLitInt(0, false, types.TypeInt))
	var elseTail []Instr
	for i := len(en.Variants) - 1; i >= 0; i-- {
		vr := &en.Variants[i]
		cond := enumVariantTagEqExpr(NewLocalExpr(vTmp), en, vr)
		elseTail = []Instr{
			&InstrIf{
				Cond: cond,
				Then: []Instr{&InstrAssign{Dst: NewLocalExpr(resSlot), Expr: NewLitInt(uint64(i), false, types.TypeInt)}},
				Else: elseTail,
			},
		}
	}
	for _, ins := range elseTail {
		fl.emit(ins)
	}
	return NewLocalExpr(resSlot)
}

// -----------------------------------------------------------------------------
// Built-in Methods & Raw Memory Intrinsics
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerBuiltinMethodCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	// Optional-chained built-in method (`opt?.len()`, `opt?.to_string()`, `opt?.copy()`, etc.)!
	if inf.IsOptChain {
		optRecv := fl.info(inf.MethodRecv).Type.(*types.Optional)
		resOpt := inf.Type.(*types.Optional)
		fl.tc.visit(optRecv)
		fl.tc.visit(resOpt)

		resSlot := fl.allocSlot("_ll_bres", resOpt)
		fl.assignLocal(resSlot, optNoneExpr(resOpt))

		emitBuiltinWithUnwrapped := func(innerTy types.Type, unwrapped Expr) {
			uTmp := fl.allocSlot("_ll_buw", innerTy)
			fl.assignLocal(uTmp, unwrapped)
			innerCall, innerRetTy := fl.emitReadBuiltinMethodOnVal(call, inf, innerTy, NewLocalExpr(uTmp))
			if !innerRetTy.IsPlain() && (inf.BuiltinName == "struct.field" || inf.BuiltinName == "seq.elem" || inf.BuiltinName == "enum.payload" || inf.BuiltinName == "distinct.underlying") {
				innerCall = fl.copyCallExpr(innerRetTy, innerCall)
			}
			if _, retAlreadyOpt := innerRetTy.(*types.Optional); retAlreadyOpt {
				fl.assignLocal(resSlot, innerCall)
			} else {
				fl.assignLocal(resSlot, fl.wrapInOptionalExpr(innerCall, resOpt))
			}
		}

		if !fl.lowerOptChainBorrow(inf.MethodRecv, optRecv, func(elemPtr Expr, elemTy types.Type) {
			emitBuiltinWithUnwrapped(elemTy, &ExprDeref{Ptr: elemPtr, Typ: elemTy})
		}) {
			recvTmp := fl.allocSlot("_ll_bopt", optRecv)
			fl.assignLocal(recvTmp, fl.lowerExprRead(inf.MethodRecv))

			fl.pushInstrBuf()
			innerTy := optRecv.Elem
			unwrapped := optUnwrapValExpr(recvTmp, optRecv)
			if rf, isRef := innerTy.(*types.Ref); isRef {
				innerTy = rf.Elem
				unwrapped = &ExprDeref{Ptr: unwrapped, Typ: innerTy}
			}
			emitBuiltinWithUnwrapped(innerTy, unwrapped)
			thenBuf := fl.popInstrBuf()

			fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp, optRecv), Then: thenBuf})
		}
		return NewLocalExpr(resSlot)
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
		var recvPtr Expr
		if isArenaRef {
			recvPtr = fl.lowerExprRead(inf.MethodRecv)
		} else {
			recvPtr = fl.lowerLValueAddrMut(inf.MethodRecv)
		}
		return fl.emitMutBuiltinMethodOnPtr(call, inf, recvTy, recvPtr)
	}

	// Read-only built-in methods (`self`)
	var recvVal Expr
	if isArenaRef {
		ptr := fl.lowerExprRead(inf.MethodRecv)
		recvVal = &ExprDeref{Ptr: ptr, Typ: recvTy}
	} else {
		recvVal = fl.lowerExprRead(inf.MethodRecv)
	}
	resExpr, _ := fl.emitReadBuiltinMethodOnVal(call, inf, recvTy, recvVal)
	return resExpr
}

func (fl *funcLowerer) emitReadBuiltinMethodOnVal(
	call *ast.CallExpr,
	inf *sema.ExprInfo,
	recvTy types.Type,
	recvVal Expr,
) (Expr, types.Type) {
	file, line := formatSpanLoc(call)
	fl.tc.visit(recvTy)

	switch inf.BuiltinName {
	case "any.copy", "Interface.copy":
		return fl.copyCallExpr(recvTy, recvVal), recvTy

	case "any.to_string":
		return fl.toStringCallExpr(recvTy, recvVal), types.TypeString

	case "any.hash":
		return fl.hashCallExpr(recvTy, recvVal), types.TypeU64

	case "struct.field":
		st := recvTy.(*types.Struct)
		sf, _ := st.FieldByName(inf.ResolvedField)
		if sf == nil && inf.TupleFieldIdx >= 0 && inf.TupleFieldIdx < len(st.Fields) {
			sf = &st.Fields[inf.TupleFieldIdx]
		}
		return &ExprField{Base: EnsureExpr(recvVal, recvTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: false}, sf.Type

	case "seq.elem":
		fIdx := inf.TupleFieldIdx
		switch v := recvTy.(type) {
		case *types.Tuple:
			return &ExprField{
				Base:      EnsureExpr(recvVal, recvTy),
				FieldName: fmt.Sprintf("_%d", fIdx),
				FieldIdx:  fIdx,
				Typ:       v.Elems[fIdx],
				IsPtr:     false,
			}, v.Elems[fIdx]
		case *types.StackArray:
			return &ExprIndex{
				Base: &ExprField{
					Base:      EnsureExpr(recvVal, recvTy),
					FieldName: "data",
					Typ:       &types.Ptr{Elem: v.Elem},
				},
				Index: NewLitInt(uint64(fIdx), false, types.TypeInt),
				Typ:   v.Elem,
			}, v.Elem
		}

	case "enum.is_variant":
		en := inf.EnumType
		return enumVariantTagEqExpr(recvVal, en, inf.EnumVariant), types.TypeBool

	case "distinct.underlying":
		dt := recvTy.(*types.Distinct)
		return &ExprCast{Val: EnsureExpr(recvVal, recvTy), From: recvTy, To: dt.Base}, dt.Base

	case "enum.payload":
		en := inf.EnumType
		vr := inf.EnumVariant
		eTmp := fl.allocSlot("_ll_epay", en)
		fl.assignLocal(eTmp, EnsureExpr(recvVal, en))
		fl.emitEnumTagAssert(NewLocalExpr(eTmp), en, vr, false, call)
		return &ExprField{
			Base:      &ExprField{Base: NewLocalExpr(eTmp), FieldName: "data"},
			FieldName: vr.CName,
			Typ:       vr.Payload,
		}, vr.Payload

	case "enum.as_variant":
		en := inf.EnumType
		vr := inf.EnumVariant
		eTmp := fl.allocSlot("_ll_eas", en)
		fl.assignLocal(eTmp, EnsureExpr(recvVal, en))
		resOpt := types.NewOptional(vr.Payload)
		fl.tc.visit(resOpt)
		resSlot := fl.allocSlot("_ll_asres", resOpt)
		fl.assignLocal(resSlot, optNoneExpr(resOpt))
		fl.pushInstrBuf()
		payVal := Expr(&ExprField{
			Base:      &ExprField{Base: NewLocalExpr(eTmp), FieldName: "data"},
			FieldName: vr.CName,
			Typ:       vr.Payload,
		})
		if !vr.Payload.IsPlain() {
			payVal = fl.copyCallExpr(vr.Payload, payVal)
		}
		fl.assignLocal(resSlot, fl.wrapInOptionalExpr(payVal, resOpt))
		thenBuf := fl.popInstrBuf()
		fl.emit(&InstrIf{Cond: enumVariantTagEqExpr(NewLocalExpr(eTmp), en, vr), Then: thenBuf})
		return NewLocalExpr(resSlot), resOpt

	case "float.to_bits":
		return &ExprFloatBits{Val: EnsureExpr(recvVal, types.TypeFloat), FloatType: types.TypeFloat, Typ: types.TypeU64}, types.TypeU64
	case "f32.to_bits":
		return &ExprFloatBits{Val: EnsureExpr(recvVal, types.TypeF32), FloatType: types.TypeF32, Typ: types.TypeU32}, types.TypeU32

	// `string` read methods
	case "string.len":
		return &ExprField{Base: EnsureExpr(recvVal, types.TypeString), FieldName: "len", Typ: types.TypeInt}, types.TypeInt
	case "string.byte_at":
		idx := fl.lowerExprRead(call.Args[0])
		sTmp := fl.allocSlot("_ll_sba", types.TypeString)
		fl.assignLocal(sTmp, recvVal)
		cIdx := fl.checkedIndexExpr(idx, &ExprField{Base: NewLocalExpr(sTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: false}, file, line)
		return &ExprIndex{
			Base:  &ExprField{Base: NewLocalExpr(sTmp), FieldName: "data", Typ: &types.Ptr{Elem: types.TypeU8}},
			Index: EnsureExpr(cIdx, types.TypeInt),
			Typ:   types.TypeU8,
		}, types.TypeU8
	case "string.c_str":
		return &ExprCall{Callee: &ExprFuncRef{Name: "ll_str_c_str"}, Args: []Expr{EnsureExpr(recvVal, types.TypeString)}, Typ: &types.Ptr{Elem: types.TypeU8}}, &types.Ptr{Elem: types.TypeU8}

	// `Vector` read methods
	case "vec.len":
		return &ExprField{Base: EnsureExpr(recvVal, recvTy), FieldName: "len", Typ: types.TypeInt}, types.TypeInt
	case "vec.cap":
		return &ExprField{Base: EnsureExpr(recvVal, recvTy), FieldName: "cap", Typ: types.TypeInt}, types.TypeInt
	case "vec.is_empty":
		return &ExprBinary{Op: token.Eq, Left: &ExprField{Base: EnsureExpr(recvVal, recvTy), FieldName: "len", Typ: types.TypeInt}, Right: NewLitInt(0, false, types.TypeInt), Typ: types.TypeBool}, types.TypeBool
	case "vec.get":
		vecTy := recvTy.(*types.Vector)
		optElem := types.NewOptional(vecTy.Elem)
		fl.tc.visit(optElem)
		idx := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_get__" + vecTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvVal, vecTy), EnsureExpr(idx, types.TypeInt)},
			Typ:    optElem,
		}, optElem
	case "vec.contains":
		vecTy := recvTy.(*types.Vector)
		needle := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_contains__" + vecTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvVal, vecTy), EnsureExpr(needle, vecTy.Elem)},
			Typ:    types.TypeBool,
		}, types.TypeBool
	case "vec.join":
		sep := fl.lowerExprRead(call.Args[0])
		if fi := fl.lookupCoreFunc("join_strings"); fi != nil {
			return &ExprCall{
				Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
				Args:   []Expr{EnsureExpr(recvVal, recvTy), EnsureExpr(sep, types.TypeString)},
				Typ:    types.TypeString,
			}, types.TypeString
		}
		return nil, types.TypeString

	// `StackArray` read methods
	case "arr.len":
		arrTy := recvTy.(*types.StackArray)
		return NewLitInt(uint64(arrTy.Size), false, types.TypeInt), types.TypeInt
	case "arr.is_empty":
		return NewLitBool(false), types.TypeBool
	case "arr.get":
		arrTy := recvTy.(*types.StackArray)
		optElem := types.NewOptional(arrTy.Elem)
		fl.tc.visit(optElem)
		idx := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_arr_get__" + arrTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvVal, arrTy), EnsureExpr(idx, types.TypeInt)},
			Typ:    optElem,
		}, optElem
	case "arr.contains":
		arrTy := recvTy.(*types.StackArray)
		needle := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_arr_contains__" + arrTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvVal, arrTy), EnsureExpr(needle, arrTy.Elem)},
			Typ:    types.TypeBool,
		}, types.TypeBool

	}
	return nil, types.TypeVoid
}

func (fl *funcLowerer) emitMutBuiltinMethodOnPtr(
	call *ast.CallExpr,
	inf *sema.ExprInfo,
	recvTy types.Type,
	recvPtr Expr,
) Expr {
	file, line := formatSpanLoc(call)
	fl.tc.visit(recvTy)

	switch inf.BuiltinName {
	// `Vector` mutating methods
	case "vec.push":
		vecTy := recvTy.(*types.Vector)
		val := fl.lowerExprOwned(call.Args[0], vecTy.Elem)
		return &ExprVecPush{VecPtr: EnsureExpr(recvPtr, &types.Ptr{Elem: vecTy}), Item: EnsureExpr(val, vecTy.Elem), VecType: vecTy}
	case "vec.pop":
		vecTy := recvTy.(*types.Vector)
		fl.tc.visit(inf.Type)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_pop__" + vecTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: vecTy})},
			Typ:    inf.Type,
		}
	case "vec.remove":
		vecTy := recvTy.(*types.Vector)
		idx := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_remove__" + vecTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: vecTy}), EnsureExpr(idx, types.TypeInt), NewLitInt(1, false, types.TypeInt), &ExprLitCString{Val: file}, NewLitInt(uint64(line), false, types.TypeInt)},
			Typ:    inf.Type,
		}
	case "vec.swap_remove":
		vecTy := recvTy.(*types.Vector)
		idx := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_swap_remove__" + vecTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: vecTy}), EnsureExpr(idx, types.TypeInt), NewLitInt(1, false, types.TypeInt), &ExprLitCString{Val: file}, NewLitInt(uint64(line), false, types.TypeInt)},
			Typ:    inf.Type,
		}
	case "vec.clear":
		vecTy := recvTy.(*types.Vector)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_clear__" + vecTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: vecTy})},
			Typ:    types.TypeVoid,
		}
	case "vec.reserve":
		vecTy := recvTy.(*types.Vector)
		capVal := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_reserve__" + vecTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: vecTy}), EnsureExpr(capVal, types.TypeInt)},
			Typ:    types.TypeVoid,
		}

	}
	return nil
}

func (fl *funcLowerer) lowerIntrinsicCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	switch inf.BuiltinName {
	case "size_of":
		fl.tc.visit(inf.IntrinsicType)
		return &ExprSizeOf{QueryType: inf.IntrinsicType}
	case "align_of":
		fl.tc.visit(inf.IntrinsicType)
		return &ExprAlignOf{QueryType: inf.IntrinsicType}
	case "is_plain":
		fl.tc.visit(inf.IntrinsicType)
		return NewLitBool(inf.IntrinsicType.IsPlain())
	case "alloc_raw":
		fl.tc.visit(inf.IntrinsicType)
		cnt := fl.lowerExprRead(call.Args[0])
		return &ExprAlloc{AllocType: inf.IntrinsicType, Count: EnsureExpr(cnt, types.TypeInt), Typ: inf.Type}
	case "alloc_uninit":
		fl.tc.visit(inf.IntrinsicType)
		cnt := fl.lowerExprRead(call.Args[0])
		return &ExprAlloc{AllocType: inf.IntrinsicType, Count: EnsureExpr(cnt, types.TypeInt), Typ: inf.Type, Uninit: true}
	case "realloc_raw":
		fl.tc.visit(inf.IntrinsicType)
		p := fl.lowerExprRead(call.Args[0])
		cnt := fl.lowerExprRead(call.Args[1])
		return &ExprRealloc{Ptr: EnsureExpr(p, &types.Ptr{Elem: types.TypeVoid}), AllocType: inf.IntrinsicType, Count: EnsureExpr(cnt, types.TypeInt), Typ: inf.Type}
	case "free_raw":
		p := fl.lowerExprRead(call.Args[0])
		return &ExprFree{Ptr: EnsureExpr(p, &types.Ptr{Elem: types.TypeVoid})}
	case "null_ptr":
		return &ExprLitZero{Typ: inf.Type}
	case "is_null":
		p := fl.lowerExprRead(call.Args[0])
		pTy := fl.info(call.Args[0]).Type
		return &ExprBinary{Op: token.Eq, Left: EnsureExpr(p, pTy), Right: &ExprLitZero{Typ: pTy}, Typ: types.TypeBool}
	case "addr_of":
		return fl.lowerLValueAddr(call.Args[0])
	case "ptr_add":
		p := fl.lowerExprRead(call.Args[0])
		off := fl.lowerExprRead(call.Args[1])
		return &ExprBinary{Op: token.Plus, Left: EnsureExpr(p, inf.Type), Right: EnsureExpr(off, types.TypeInt), Typ: inf.Type}
	case "ptr_read":
		p := fl.lowerExprRead(call.Args[0])
		return &ExprDeref{Ptr: EnsureExpr(p, &types.Ptr{Elem: inf.Type}), Typ: inf.Type}
	case "ptr_write":
		p := fl.lowerExprRead(call.Args[0])
		v := fl.lowerExprOwned(call.Args[1], inf.IntrinsicType)
		pTmp := fl.allocSlot("_ll_pw", fl.info(call.Args[0]).Type)
		fl.assignLocal(pTmp, p)
		fl.emit(&InstrAssign{Dst: &ExprDeref{Ptr: NewLocalExpr(pTmp), Typ: inf.IntrinsicType}, Expr: EnsureExpr(v, inf.IntrinsicType)})
		return nil
	case "ptr_cast":
		src := fl.lowerExprRead(call.Args[0])
		srcTy := fl.info(call.Args[0]).Type
		return &ExprCast{Val: EnsureExpr(src, srcTy), From: srcTy, To: inf.Type}
	case "fn_from_ptr":
		src := fl.lowerExprRead(call.Args[0])
		srcTy := fl.info(call.Args[0]).Type
		return &ExprCast{Val: EnsureExpr(src, srcTy), From: srcTy, To: inf.Type}
	case "ref_from_ptr":
		src := fl.lowerExprRead(call.Args[0])
		srcTy := fl.info(call.Args[0]).Type
		return &ExprCast{Val: EnsureExpr(src, srcTy), From: srcTy, To: inf.Type}
	case "ptr_addr":
		p := fl.lowerExprRead(call.Args[0])
		pTy := fl.info(call.Args[0]).Type
		return &ExprCast{Val: EnsureExpr(p, pTy), From: pTy, To: types.TypeU64}
	case "copy_bytes":
		dst := fl.lowerExprRead(call.Args[0])
		src := fl.lowerExprRead(call.Args[1])
		cnt := fl.lowerExprRead(call.Args[2])
		return &ExprCopyBytes{Dst: EnsureExpr(dst, types.TypeVoid), Src: EnsureExpr(src, types.TypeVoid), Count: EnsureExpr(cnt, types.TypeInt)}
	case "zero_bytes":
		dst := fl.lowerExprRead(call.Args[0])
		cnt := fl.lowerExprRead(call.Args[1])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_zero_bytes"},
			Args:   []Expr{EnsureExpr(dst, types.TypeVoid), EnsureExpr(cnt, types.TypeInt)},
			Typ:    types.TypeVoid,
		}
	case "fill_bytes":
		dst := fl.lowerExprRead(call.Args[0])
		val := fl.lowerExprRead(call.Args[1])
		cnt := fl.lowerExprRead(call.Args[2])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_fill_bytes"},
			Args:   []Expr{EnsureExpr(dst, &types.Ptr{Elem: types.TypeU8}), EnsureExpr(val, types.TypeU8), EnsureExpr(cnt, types.TypeInt)},
			Typ:    types.TypeVoid,
		}
	}

	if inf.BuiltinName == "syscall" {
		// Always the six-argument form; unused argument registers get 0.
		args := make([]Expr, 7)
		for i := range args {
			if i < len(call.Args) {
				args[i] = EnsureExpr(fl.lowerExprRead(call.Args[i]), types.TypeInt)
			} else {
				args[i] = NewLitInt(0, false, types.TypeInt)
			}
		}
		return &ExprCall{Callee: &ExprFuncRef{Name: "ll_syscall6"}, Args: args, Typ: types.TypeInt}
	}

	sysSymbolMap := map[string]string{
		"sys_argc":                 "ll_os_argc",
		"sys_argv":                 "ll_os_argv_cstr",
		"sys_tls_available":        "ll_tls_available",
		"sys_tls_client_handshake": "ll_tls_client_handshake",
		"sys_tls_send_all":         "ll_tls_send_all",
		"sys_tls_recv":             "ll_tls_recv",
		"sys_tls_close":            "ll_tls_close",
		"sys_tls_server_ctx_new":   "ll_tls_server_ctx_new",
		"sys_tls_server_ctx_free":  "ll_tls_server_ctx_free",
		"sys_tls_server_handshake": "ll_tls_server_handshake",
	}
	if rtSym, ok := sysSymbolMap[inf.BuiltinName]; ok {
		cArgs := make([]Expr, len(call.Args))
		for i, a := range call.Args {
			aTy := fl.info(a).Type
			cArgs[i] = EnsureExpr(fl.lowerExprRead(a), aTy)
		}
		return &ExprCall{
			Callee: &ExprFuncRef{Name: rtSym},
			Args:   cArgs,
			Typ:    inf.Type,
		}
	}
	return nil
}
