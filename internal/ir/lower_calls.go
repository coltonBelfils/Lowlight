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
				out[i] = fl.lowerLValueAddr(u.Operand)
			} else {
				out[i] = fl.lowerLValueAddr(arg)
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

func (fl *funcLowerer) lowerDirectFuncCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
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
		var unwrapped Expr = optUnwrapValExpr(recvTmp, optRecv)
		if rf, isRef := optRecv.Elem.(*types.Ref); isRef {
			_ = rf
			unwrapped = &ExprDeref{Ptr: unwrapped, Typ: rf.Elem}
		}
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
			Callee:  &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
			Args:    cArgs,
			Typ:     fi.Sig.Ret,
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
		thenBuf := fl.popInstrBuf()

		fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp, optRecv), Then: thenBuf})
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
		ifcVal := optUnwrapValExpr(recvTmp, optRecv)
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
		thenBuf := fl.popInstrBuf()
		fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp, optRecv), Then: thenBuf})
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
			ptr := fl.lowerLValueAddr(inf.MethodRecv)
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
		msg := fl.lowerExprRead(call.Args[0])
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
			return &ExprCall{
				Callee: &ExprFuncRef{Name: "ll_format_loc"},
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
	}
	return nil
}

// -----------------------------------------------------------------------------
// Built-in Methods & Raw Memory Intrinsics
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerBuiltinMethodCall(call *ast.CallExpr, inf *sema.ExprInfo) Expr {
	// Static `Arena` constructors (`Arena.new()`, `Arena.with_capacity(n)`) have `MethodRecv == nil`!
	if inf.BuiltinName == "Arena.new" {
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_arena_new"},
			Args:   []Expr{NewLitInt(4096, false, types.TypeInt)},
			Typ:    inf.Type,
		}
	}
	if inf.BuiltinName == "Arena.with_capacity" {
		capVal := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_arena_new"},
			Args:   []Expr{EnsureExpr(capVal, types.TypeInt)},
			Typ:    inf.Type,
		}
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
		unwrapped := optUnwrapValExpr(recvTmp, optRecv)
		if rf, isRef := innerTy.(*types.Ref); isRef {
			innerTy = rf.Elem
			unwrapped = &ExprDeref{Ptr: unwrapped, Typ: innerTy}
		}
		uTmp := fl.allocSlot("_ll_buw", innerTy)
		fl.assignLocal(uTmp, unwrapped)
		innerCall, innerRetTy := fl.emitReadBuiltinMethodOnVal(call, inf, innerTy, NewLocalExpr(uTmp))
		if _, retAlreadyOpt := innerRetTy.(*types.Optional); retAlreadyOpt {
			fl.assignLocal(resSlot, innerCall)
		} else {
			fl.assignLocal(resSlot, fl.wrapInOptionalExpr(innerCall, resOpt))
		}
		thenBuf := fl.popInstrBuf()

		fl.emit(&InstrIf{Cond: optHasValueExpr(recvTmp, optRecv), Then: thenBuf})
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
			recvPtr = fl.lowerLValueAddr(inf.MethodRecv)
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

	// `Set` read methods
	case "set.len":
		return &ExprField{Base: EnsureExpr(recvVal, recvTy), FieldName: "len", Typ: types.TypeInt}, types.TypeInt
	case "set.is_empty":
		return &ExprBinary{Op: token.Eq, Left: &ExprField{Base: EnsureExpr(recvVal, recvTy), FieldName: "len", Typ: types.TypeInt}, Right: NewLitInt(0, false, types.TypeInt), Typ: types.TypeBool}, types.TypeBool
	case "set.has":
		setTy := recvTy.(*types.Set)
		val := fl.lowerExprRead(call.Args[0])
		sTmp := fl.allocSlot("_ll_sh", setTy)
		fl.assignLocal(sTmp, recvVal)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_set_has__" + setTy.Mangled()},
			Args:   []Expr{&ExprAddrOf{Place: NewLocalExpr(sTmp), Typ: &types.Ptr{Elem: setTy}}, EnsureExpr(val, setTy.Elem)},
			Typ:    types.TypeBool,
		}, types.TypeBool
	case "set.to_vec":
		setTy := recvTy.(*types.Set)
		vecTy := &types.Vector{Elem: setTy.Elem}
		fl.tc.visit(vecTy)
		sTmp := fl.allocSlot("_ll_stv", setTy)
		fl.assignLocal(sTmp, recvVal)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_set_to_vec__" + setTy.Mangled()},
			Args:   []Expr{&ExprAddrOf{Place: NewLocalExpr(sTmp), Typ: &types.Ptr{Elem: setTy}}},
			Typ:    vecTy,
		}, vecTy

	// `Map` read methods
	case "map.len":
		return &ExprField{Base: EnsureExpr(recvVal, recvTy), FieldName: "len", Typ: types.TypeInt}, types.TypeInt
	case "map.is_empty":
		return &ExprBinary{Op: token.Eq, Left: &ExprField{Base: EnsureExpr(recvVal, recvTy), FieldName: "len", Typ: types.TypeInt}, Right: NewLitInt(0, false, types.TypeInt), Typ: types.TypeBool}, types.TypeBool
	case "map.has":
		mapTy := recvTy.(*types.Map)
		key := fl.lowerExprRead(call.Args[0])
		mTmp := fl.allocSlot("_ll_mh", mapTy)
		fl.assignLocal(mTmp, recvVal)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_map_has__" + mapTy.Mangled()},
			Args:   []Expr{&ExprAddrOf{Place: NewLocalExpr(mTmp), Typ: &types.Ptr{Elem: mapTy}}, EnsureExpr(key, mapTy.Key)},
			Typ:    types.TypeBool,
		}, types.TypeBool
	case "map.keys":
		mapTy := recvTy.(*types.Map)
		vecTy := &types.Vector{Elem: mapTy.Key}
		fl.tc.visit(vecTy)
		mTmp := fl.allocSlot("_ll_mk", mapTy)
		fl.assignLocal(mTmp, recvVal)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_map_keys__" + mapTy.Mangled()},
			Args:   []Expr{&ExprAddrOf{Place: NewLocalExpr(mTmp), Typ: &types.Ptr{Elem: mapTy}}},
			Typ:    vecTy,
		}, vecTy
	case "map.values":
		mapTy := recvTy.(*types.Map)
		vecTy := &types.Vector{Elem: mapTy.Value}
		fl.tc.visit(vecTy)
		mTmp := fl.allocSlot("_ll_mv", mapTy)
		fl.assignLocal(mTmp, recvVal)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_map_values__" + mapTy.Mangled()},
			Args:   []Expr{&ExprAddrOf{Place: NewLocalExpr(mTmp), Typ: &types.Ptr{Elem: mapTy}}},
			Typ:    vecTy,
		}, vecTy

	// `Arena` read methods
	case "Arena.bytes_used":
		aTmp := fl.allocSlot("_ll_abu", types.TypeArena)
		fl.assignLocal(aTmp, recvVal)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_arena_bytes_used"},
			Args:   []Expr{&ExprAddrOf{Place: NewLocalExpr(aTmp), Typ: &types.Ptr{Elem: types.TypeArena}}},
			Typ:    types.TypeInt,
		}, types.TypeInt
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
	// `string` mutating methods
	case "string.push":
		suf := fl.lowerExprRead(call.Args[0])
		return &ExprStringPush{StrPtr: EnsureExpr(recvPtr, types.TypeString), Item: EnsureExpr(suf, types.TypeString)}
	case "string.push_char":
		ch := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_str_push_char"},
			Args:   []Expr{EnsureExpr(recvPtr, types.TypeString), EnsureExpr(ch, types.TypeChar)},
			Typ:    types.TypeVoid,
		}
	case "string.push_byte":
		b := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_str_push_byte"},
			Args:   []Expr{EnsureExpr(recvPtr, types.TypeString), EnsureExpr(b, types.TypeU8)},
			Typ:    types.TypeVoid,
		}
	case "string.push_bytes":
		p := fl.lowerExprRead(call.Args[0])
		cnt := fl.lowerExprRead(call.Args[1])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_str_push_bytes"},
			Args:   []Expr{EnsureExpr(recvPtr, types.TypeString), EnsureExpr(p, &types.Ptr{Elem: types.TypeU8}), EnsureExpr(cnt, types.TypeInt)},
			Typ:    types.TypeVoid,
		}
	case "string.clear":
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_str_clear"},
			Args:   []Expr{EnsureExpr(recvPtr, types.TypeString)},
			Typ:    types.TypeVoid,
		}

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

	// `Set` mutating methods
	case "set.insert":
		setTy := recvTy.(*types.Set)
		val := fl.lowerExprOwned(call.Args[0], setTy.Elem)
		return &ExprSetInsert{SetPtr: EnsureExpr(recvPtr, &types.Ptr{Elem: setTy}), Item: EnsureExpr(val, setTy.Elem), SetType: setTy}
	case "set.remove":
		setTy := recvTy.(*types.Set)
		val := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_set_remove__" + setTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: setTy}), EnsureExpr(val, setTy.Elem)},
			Typ:    types.TypeBool,
		}
	case "set.clear":
		setTy := recvTy.(*types.Set)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_set_clear__" + setTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: setTy})},
			Typ:    types.TypeVoid,
		}

	// `Map` mutating methods
	case "map.insert":
		mapTy := recvTy.(*types.Map)
		k := fl.lowerExprOwned(call.Args[0], mapTy.Key)
		v := fl.lowerExprOwned(call.Args[1], mapTy.Value)
		return &ExprMapInsert{MapPtr: EnsureExpr(recvPtr, &types.Ptr{Elem: mapTy}), Key: EnsureExpr(k, mapTy.Key), Val: EnsureExpr(v, mapTy.Value), MapType: mapTy}
	case "map.push":
		mapTy := recvTy.(*types.Map)
		entryTy := fl.info(call.Args[0]).Type
		entryVal := fl.lowerExprOwned(call.Args[0], entryTy)
		entrySlot := fl.allocSlot("_ll_entry", entryTy)
		fl.assignLocal(entrySlot, entryVal)
		return &ExprMapInsert{
			MapPtr:  EnsureExpr(recvPtr, &types.Ptr{Elem: mapTy}),
			Key:     &ExprField{Base: NewLocalExpr(entrySlot), FieldName: "ll_key", Typ: mapTy.Key},
			Val:     &ExprField{Base: NewLocalExpr(entrySlot), FieldName: "ll_value", Typ: mapTy.Value},
			MapType: mapTy,
		}
	case "map.remove":
		mapTy := recvTy.(*types.Map)
		fl.tc.visit(inf.Type)
		k := fl.lowerExprRead(call.Args[0])
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_map_remove__" + mapTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: mapTy}), EnsureExpr(k, mapTy.Key)},
			Typ:    inf.Type,
		}
	case "map.clear":
		mapTy := recvTy.(*types.Map)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_map_clear__" + mapTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvPtr, &types.Ptr{Elem: mapTy})},
			Typ:    types.TypeVoid,
		}

	// `Arena` mutating methods
	case "Arena.alloc":
		elemTy := inf.IntrinsicType
		fl.tc.visit(elemTy)
		valExpr := fl.lowerExprOwned(call.Args[0], elemTy)
		dtorFn := ""
		if !elemTy.IsPlain() {
			dtorFn = DropFnName(elemTy)
		}
		ptrSlot := fl.allocSlot("_ll_aptr", &types.Ref{Elem: elemTy})
		fl.assignLocal(ptrSlot, &ExprArenaAlloc{
			Arena:     EnsureExpr(recvPtr, &types.Ptr{Elem: types.TypeArena}),
			AllocType: elemTy,
			DtorFn:    dtorFn,
			Typ:       &types.Ref{Elem: elemTy},
		})
		fl.emit(&InstrAssign{Dst: &ExprDeref{Ptr: NewLocalExpr(ptrSlot), Typ: elemTy}, Expr: EnsureExpr(valExpr, elemTy)})
		return NewLocalExpr(ptrSlot)

	case "Arena.reset":
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_arena_reset"},
			Args:   []Expr{EnsureExpr(recvPtr, types.TypeArena)},
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
	case "alloc_raw":
		fl.tc.visit(inf.IntrinsicType)
		cnt := fl.lowerExprRead(call.Args[0])
		return &ExprAlloc{AllocType: inf.IntrinsicType, Count: EnsureExpr(cnt, types.TypeInt), Typ: inf.Type}
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
	case "ptr_addr":
		p := fl.lowerExprRead(call.Args[0])
		return &ExprCast{Val: EnsureExpr(p, inf.Type), From: inf.Type, To: types.TypeU64}
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
	}

	sysSymbolMap := map[string]string{
		"sys_fopen":                "ll_sys_fopen",
		"sys_fclose":               "ll_sys_fclose",
		"sys_fread":                "ll_sys_fread",
		"sys_fwrite":               "ll_sys_fwrite",
		"sys_fseek":                "ll_sys_fseek",
		"sys_ftell":                "ll_sys_ftell",
		"sys_fflush":               "ll_sys_fflush",
		"sys_remove":               "ll_sys_remove",
		"sys_rename":               "ll_sys_rename",
		"sys_popen":                "ll_os_popen",
		"sys_pclose":               "ll_os_pclose",
		"sys_pread":                "ll_os_pread",
		"sys_system":               "ll_os_system",
		"sys_file_size":            "ll_os_file_size",
		"sys_file_exists":          "ll_os_file_exists",
		"sys_mkdir":                "ll_os_mkdir",
		"sys_remove_all":           "ll_os_remove_all",
		"sys_num_cpus":             "ll_os_num_cpus",
		"sys_os_name":              "ll_os_name_cstr",
		"sys_os_arch":              "ll_os_arch_cstr",
		"sys_argc":                 "ll_os_argc",
		"sys_argv":                 "ll_os_argv_cstr",
		"sys_monotonic_nanos":      "ll_time_monotonic_nanos",
		"sys_epoch_nanos_now":      "ll_time_epoch_nanos",
		"sys_sleep_ms":             "ll_time_sleep_ms",
		"sys_local_tz_offset":      "ll_time_local_offset",
		"sys_tcp_listen":           "ll_net_tcp_listen",
		"sys_tcp_port":             "ll_net_tcp_port",
		"sys_tcp_accept":           "ll_net_tcp_accept",
		"sys_tcp_connect":          "ll_net_tcp_connect",
		"sys_net_send_all":         "ll_net_send_all",
		"sys_net_recv":             "ll_net_recv",
		"sys_net_close":            "ll_net_close",
		"sys_net_shutdown":         "ll_net_shutdown",
		"sys_net_set_timeout":      "ll_net_set_timeout",
		"sys_net_socketpair":       "ll_net_socketpair",
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
