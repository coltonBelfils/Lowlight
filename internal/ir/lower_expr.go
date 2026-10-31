package ir

import (
	"fmt"

	"lowlight/internal/ast"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// -----------------------------------------------------------------------------
// Expression Entry Points: Void, Owned, Read-Only, & LValue Address
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerVoidExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.UnsafeExpr:
		fl.lowerVoidExpr(v.Expr)
	case *ast.BlockExpr:
		_ = fl.lowerBlockExpr(v, false)
	case *ast.StmtBodyExpr:
		fl.lowerStmt(v.Stmt)
	case *ast.IfExpr:
		_ = fl.lowerIfExpr(v, false)
	case *ast.ForExpr:
		_ = fl.lowerForExpr(v, false)
	case *ast.MatchExpr:
		_ = fl.lowerMatchExpr(v, false)
	default:
		inf := fl.info(e)
		if inf.Type == nil || inf.Type.Equals(types.TypeVoid) || inf.Type.Equals(types.TypeNever) {
			raw := fl.lowerRawExpr(e, nil, false)
			if raw != "" {
				fl.emit(&InstrExpr{Expr: raw})
			}
			return
		}
		// Expression returns a value that is discarded in statement position:
		// lower via `lowerExprRead` so any fresh Resource rvalue is registered in `stmtTemps` and dropped!
		exprStr := fl.lowerExprRead(e)
		// Ensure side-effecting Plain calls still execute if not already materialized into a temp
		if inf.Type.IsPlain() && exprStr != "" {
			fl.emit(&InstrExpr{Expr: fmt.Sprintf("(void)(%s)", exprStr)})
		}
	}
}

// lowerExprOwned lowers `e` producing an OWNED value of `targetTy` (moving out of
// any `MovedLocal`, wrapping `T -> ?T` if `WrapOptional`, or boxing `Concrete -> Interface` if `BoxInterface`).
func (fl *funcLowerer) lowerExprOwned(e ast.Expr, targetTy types.Type) string {
	if e == nil {
		return ""
	}
	inf := fl.info(e)
	if targetTy == nil {
		targetTy = inf.Type
	}
	fl.tc.visit(targetTy)

	// Unwrap explicit `own expr` marker: lower inner operand at its own natural type,
	// then apply outer target coercions (`T -> ?T` or `T -> Interface`) exactly once!
	if u, isU := e.(*ast.UnaryExpr); isU && u.Op == token.KwOwn {
		innerVal := fl.lowerExprOwned(u.Operand, fl.info(u.Operand).Type)
		return fl.applyOwnedCoercions(e, inf, innerVal, targetTy)
	}

	// Check if this expression moves out of an owned local variable
	if inf.MovedLocal != nil {
		if loc, ok := fl.symLocals[inf.MovedLocal]; ok {
			mvTmp := fl.allocSlot("_ll_mv", loc.Type)
			fl.assignLocal(mvTmp, loc.CName)
			fl.emit(&InstrZero{Var: loc.CName, Type: loc.Type, MovedFlag: loc.MovedFlag})
			return fl.applyOwnedCoercions(e, inf, mvTmp.CName, targetTy)
		}
	}

	// Check `own self` field move (`self.field`)
	if mem, isMem := e.(*ast.MemberExpr); isMem && !inf.IsOptChain && inf.EnumVariant == nil && !inf.Type.IsPlain() {
		if id, isId := mem.Base.(*ast.IdentExpr); isId && id.Name == "self" {
			if selfSym := fl.info(id).Sym; selfSym != nil && selfSym.BindKind == sema.BindOwned {
				if selfLoc, ok := fl.symLocals[selfSym]; ok {
					st := selfLoc.Type.(*types.Struct)
					sf, _ := st.FieldByName(mem.Member)
					if sf != nil {
						fieldLval := fmt.Sprintf("%s.%s", selfLoc.CName, sf.CName)
						mvTmp := fl.allocSlot("_ll_fmove", sf.Type)
						fl.assignLocal(mvTmp, fieldLval)
						fl.emit(&InstrAssign{Dst: fieldLval, Expr: fmt.Sprintf("(%s){0}", CType(sf.Type))})
						return fl.applyOwnedCoercions(e, inf, mvTmp.CName, targetTy)
					}
				}
			}
		}
	}

	raw := fl.lowerRawExpr(e, targetTy, true)
	return fl.applyOwnedCoercions(e, inf, raw, targetTy)
}

func (fl *funcLowerer) applyOwnedCoercions(_ ast.Expr, inf *sema.ExprInfo, valExpr string, targetTy types.Type) string {
	// Owned Interface boxing (`let s: Shape = my_circle` or `[Printable(x)]`)
	if inf.BoxInterface != nil && !inf.BorrowAsView && !inf.Type.Equals(inf.BoxInterface) {
		return fl.boxConcreteIntoInterface(valExpr, inf.Type, inf.BoxInterface)
	}
	// Implicit `T -> ?T` wrapping (either via `inf.WrapOptional` or target `?T` expecting `Elem`)
	if optTy, isOpt := targetTy.(*types.Optional); isOpt && inf.Type != nil && inf.Type.Equals(optTy.Elem) {
		fl.tc.visit(optTy)
		return fl.wrapInOptionalExpr(valExpr, optTy)
	}
	if inf.WrapOptional {
		optTy := types.NewOptional(inf.Type)
		fl.tc.visit(optTy)
		return fl.wrapInOptionalExpr(valExpr, optTy)
	}
	return valExpr
}

// lowerExprRead lowers `e` for read-only consumption.
// If `e` creates a fresh heap Resource rvalue (e.g. `"hi $x"`, `make_vec()`, `m["k"]`),
// it is stored in a statement temporary (`stmtTemps`) so `flushStmtTemps()` frees it cleanly!
func (fl *funcLowerer) lowerExprRead(e ast.Expr) string {
	if e == nil {
		return ""
	}
	inf := fl.info(e)
	fl.tc.visit(inf.Type)

	// Zero-allocation read-only views for call arguments:
	if inf.BorrowAsView {
		// 1. `[N: T]` stack array passed to read-only `[T]` parameter!
		if arrTy, isArr := inf.Type.(*types.StackArray); isArr {
			vecTy := &types.Vector{Elem: arrTy.Elem}
			fl.tc.visit(vecTy)
			var arrAddr string
			if isAddressableAST(e) {
				arrAddr = fl.lowerLValueAddr(e)
			} else {
				tmp := fl.allocSlot("_ll_arrview", arrTy)
				fl.assignLocal(tmp, fl.lowerRawExpr(e, arrTy, false))
				arrAddr = "&" + tmp.CName
			}
			return fmt.Sprintf("(%s){ .data = (%s*)(%s)->data, .len = %d, .cap = 0 }",
				CType(vecTy), CType(arrTy.Elem), arrAddr, arrTy.Size)
		}

		// 2. Concrete value passed to read-only `Interface` parameter -> zero-allocation stack fat pointer!
		if inf.BoxInterface != nil && !inf.Type.Equals(inf.BoxInterface) {
			var addr string
			if isAddressableAST(e) {
				addr = fl.lowerLValueAddr(e)
			} else {
				// Materialize rvalue in a temp (dropped at statement end if Resource!)
				baseVal := fl.lowerRawExpr(e, inf.Type, false)
				tname := fl.allocStmtTemp(inf.Type, baseVal)
				addr = "&" + tname
			}
			vtName := VTableInstanceName(inf.Type, inf.BoxInterface)
			return fmt.Sprintf("(%s){ .data = (void*)(%s), .vtable = &%s, .owned = false }",
				CType(inf.BoxInterface), addr, vtName)
		}

		// 3. Owned `Interface` variable passed to read-only `Interface` parameter -> unowned fat pointer copy!
		if ifc, isIfc := inf.Type.(*types.Interface); isIfc {
			raw := fl.lowerRawExpr(e, inf.Type, false)
			tmp := fl.allocSlot("_ll_ifcb", ifc)
			fl.assignLocal(tmp, raw)
			return fmt.Sprintf("(%s){ .data = %s.data, .vtable = %s.vtable, .owned = false }",
				CType(ifc), tmp.CName, tmp.CName)
		}
	}

	raw := fl.lowerRawExpr(e, nil, false)

	// If `e` produced a fresh owned Resource rvalue, bind it into `stmtTemps` for automatic cleanup!
	if inf.Type != nil && !inf.Type.IsPlain() && fl.isFreshResourceRValue(e, inf) {
		raw = fl.allocStmtTemp(inf.Type, raw)
	}

	if inf.WrapOptional {
		optTy := types.NewOptional(inf.Type)
		fl.tc.visit(optTy)
		return fl.wrapInOptionalExpr(raw, optTy)
	}
	return raw
}

// isFreshResourceRValue reports whether evaluating `e` in read-only mode produces a newly owned
// Resource value that is NOT already owned by an existing variable/place.
func (fl *funcLowerer) isFreshResourceRValue(e ast.Expr, inf *sema.ExprInfo) bool {
	if inf.BorrowAsView {
		// Note: `SliceExpr` and `StringLit` with `BorrowAsView == true` have `cap = 0` (no heap alloc).
		switch e.(type) {
		case *ast.StringLit, *ast.SliceExpr:
			return false
		}
	}
	switch v := e.(type) {
	case *ast.StringLit, *ast.NoneLit:
		// String literals have `cap = 0` (static storage); `none` has `has_value = false` / `NULL`.
		return false
	case *ast.IdentExpr:
		// Reading a variable or static function thunk does NOT allocate!
		return false
	case *ast.MemberExpr:
		// Optional chaining `u?.city` clones `city` if Resource; plain `u.city` reads in-place!
		return inf.IsOptChain
	case *ast.IndexExpr:
		// `map[k]` clones `?V`, whereas `vec[i]` / `arr[i]` reads in-place!
		baseTy := fl.info(v.Base).Type
		if rf, ok := baseTy.(*types.Ref); ok {
			baseTy = rf.Elem
		}
		_, isMap := baseTy.(*types.Map)
		return isMap
	case *ast.UnaryExpr:
		if v.Op == token.Star {
			return false // `*p` dereferences in place
		}
		return true
	case *ast.UnsafeExpr:
		return fl.isFreshResourceRValue(v.Expr, fl.info(v.Expr))
	default:
		// `InterpStringExpr`, `BinaryExpr` (`s1 + s2`, `??`), `CallExpr`, `SliceExpr` (non-view),
		// `VecLit`, `SetLit`, `MapLit`, `StructLit`, `FuncLit`, `BlockExpr`, `IfExpr`, `ForExpr`, `MatchExpr`
		return true
	}
}

func isAddressableAST(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.IdentExpr:
		return true
	case *ast.MemberExpr:
		return !v.QuestDot
	case *ast.IndexExpr:
		return true
	case *ast.UnaryExpr:
		return v.Op == token.Star
	case *ast.UnsafeExpr:
		return isAddressableAST(v.Expr)
	default:
		return false
	}
}

// lowerLValueAddr lowers an addressable place expression `e` to a C pointer `T*`.
func (fl *funcLowerer) lowerLValueAddr(e ast.Expr) string {
	inf := fl.info(e)
	switch v := e.(type) {
	case *ast.UnsafeExpr:
		return fl.lowerLValueAddr(v.Expr)
	case *ast.IdentExpr:
		sym := inf.Sym
		if sym != nil {
			if capField, ok := fl.captureFields[sym]; ok {
				return "&(" + capField + ")"
			}
			if loc, ok := fl.symLocals[sym]; ok {
				if loc.IsMutPtr {
					return loc.CName
				}
				return "&" + loc.CName
			}
			return "&" + sym.CName
		}
		return "&" + types.SanitizeCIdent(v.Name)

	case *ast.UnaryExpr:
		if v.Op == token.Star {
			return fl.lowerExprRead(v.Operand)
		}
		if v.Op == token.Amp {
			return fl.lowerLValueAddr(v.Operand)
		}

	case *ast.MemberExpr:
		baseTy := fl.info(v.Base).Type
		if rf, isRef := baseTy.(*types.Ref); isRef {
			st := rf.Elem.(*types.Struct)
			sf, _ := st.FieldByName(v.Member)
			ptr := fl.lowerExprRead(v.Base)
			return fmt.Sprintf("&(%s->%s)", ptr, sf.CName)
		}
		st := baseTy.(*types.Struct)
		sf, _ := st.FieldByName(v.Member)
		basePtr := fl.lowerLValueAddr(v.Base)
		return fmt.Sprintf("&((%s)->%s)", basePtr, sf.CName)

	case *ast.IndexExpr:
		baseTy := fl.info(v.Base).Type
		idxExpr := fl.lowerExprRead(v.Index)
		file, line := formatSpanLoc(v)
		if rf, isRef := baseTy.(*types.Ref); isRef {
			ptr := fl.lowerExprRead(v.Base)
			return fl.indexElementPtr(rf.Elem, ptr, idxExpr, file, line)
		}
		if _, isPtr := baseTy.(*types.Ptr); isPtr {
			ptr := fl.lowerExprRead(v.Base)
			return fmt.Sprintf("(%s + %s)", ptr, idxExpr)
		}
		basePtr := fl.lowerLValueAddr(v.Base)
		return fl.indexElementPtr(baseTy, basePtr, idxExpr, file, line)
	}
	// Fallback: materialize into a temporary slot and return its address
	tmp := fl.allocSlot("_ll_lval", inf.Type)
	fl.assignLocal(tmp, fl.lowerRawExpr(e, inf.Type, false))
	return "&" + tmp.CName
}

func (fl *funcLowerer) indexElementPtr(colTy types.Type, colPtr, idxExpr, file string, line int) string {
	switch c := colTy.(type) {
	case *types.Vector:
		checkedIdx := fl.checkedIndexExpr(idxExpr, fmt.Sprintf("(%s)->len", colPtr), file, line)
		return fmt.Sprintf("&((%s)->data[%s])", colPtr, checkedIdx)
	case *types.StackArray:
		checkedIdx := fl.checkedIndexExpr(idxExpr, fmt.Sprintf("%dLL", c.Size), file, line)
		return fmt.Sprintf("&((%s)->data[%s])", colPtr, checkedIdx)
	}
	return colPtr
}

func (fl *funcLowerer) checkedIndexExpr(idxExpr, lenExpr, file string, line int) string {
	if fl.release {
		return idxExpr
	}
	return fmt.Sprintf("ll_check_bounds(%s, %s, %q, %d)", idxExpr, lenExpr, file, line)
}

// -----------------------------------------------------------------------------
// Core Expression Dispatcher (`lowerRawExpr`)
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerRawExpr(e ast.Expr, expected types.Type, forOwned bool) string {
	inf := fl.info(e)

	switch v := e.(type) {
	case *ast.IntLit:
		return formatCIntLit(v.Val, false, inf.Type)

	case *ast.FloatLit:
		return formatCFloatLit(v.Val, inf.Type)

	case *ast.BoolLit:
		if v.Val {
			return "true"
		}
		return "false"

	case *ast.CharLit:
		return fmt.Sprintf("((uint32_t)%dU)", uint32(v.Val))

	case *ast.StringLit:
		cstr := quoteCString(v.Val)
		expr := fmt.Sprintf("ll_str_lit(%s, %dLL)", cstr, len(v.Val))
		if d, isDist := inf.Type.(*types.Distinct); isDist {
			return fmt.Sprintf("((%s)%s)", CType(d), expr)
		}
		return expr

	case *ast.InterpStringExpr:
		return fl.lowerInterpString(v)

	case *ast.NoneLit:
		optTy := inf.Type.(*types.Optional)
		fl.tc.visit(optTy)
		return optNoneExpr(optTy)

	case *ast.IdentExpr:
		return fl.lowerIdentExpr(v, inf)

	case *ast.UnaryExpr:
		return fl.lowerUnaryExpr(v, inf)

	case *ast.BinaryExpr:
		return fl.lowerBinaryExpr(v, inf, forOwned)

	case *ast.MemberExpr:
		return fl.lowerMemberExpr(v, inf, forOwned)

	case *ast.CallExpr:
		return fl.lowerCallExpr(v, inf)

	case *ast.IndexExpr:
		return fl.lowerIndexExpr(v, inf, forOwned)

	case *ast.SliceExpr:
		return fl.lowerSliceExpr(v, inf)

	case *ast.VecLit:
		return fl.lowerVecOrArrayLit(v, inf)

	case *ast.RepeatArrayLit:
		return fl.lowerRepeatArrayLit(v, inf)

	case *ast.SetLit:
		return fl.lowerSetLit(v, inf)

	case *ast.MapLit:
		return fl.lowerMapLit(v, inf)

	case *ast.EmptyHashLit:
		fl.tc.visit(inf.Type)
		return fmt.Sprintf("(%s){0}", CType(inf.Type))

	case *ast.StructLit:
		return fl.lowerStructLit(v, inf)

	case *ast.FuncLit:
		return fl.lowerFuncLitExpr(v, inf)

	case *ast.UnsafeExpr:
		return fl.lowerRawExpr(v.Expr, expected, forOwned)

	case *ast.BlockExpr:
		return fl.lowerBlockExpr(v, true)

	case *ast.StmtBodyExpr:
		fl.lowerStmt(v.Stmt)
		return ""

	case *ast.IfExpr:
		return fl.lowerIfExpr(v, true)

	case *ast.ForExpr:
		return fl.lowerForExpr(v, true)

	case *ast.MatchExpr:
		return fl.lowerMatchExpr(v, true)
	}

	_ = expected
	return ""
}

// -----------------------------------------------------------------------------
// Control-Flow Expressions: Block, If, For, Match
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerBlockExpr(b *ast.BlockExpr, needValue bool) string {
	inf := fl.info(b)
	exitLbl := fmt.Sprintf("_ll_exit_%d", inf.TargetID)

	var resSlot *Local
	if needValue && inf.Type != nil && !inf.Type.Equals(types.TypeVoid) && !inf.Type.Equals(types.TypeNever) {
		resSlot = fl.allocSlot("_ll_blk", inf.Type)
	}

	fl.pushScope(inf.TargetID, exitLbl, "", resSlot, nil, false)
	fl.pushInstrBuf()

	for _, st := range b.Stmts {
		fl.lowerStmt(st)
		if endsWithTerm(fl.curInstrs()) {
			break
		}
	}

	if b.Tail != nil && !endsWithTerm(fl.curInstrs()) {
		if !needValue || resSlot == nil {
			fl.lowerVoidExpr(b.Tail)
			fl.flushStmtTemps()
		} else {
			tailTy := fl.info(b.Tail).Type
			if tailTy == types.TypeNever {
				fl.lowerVoidExpr(b.Tail)
				fl.flushStmtTemps()
			} else {
				tval := fl.lowerExprOwned(b.Tail, resSlot.Type)
				fl.assignLocal(resSlot, tval)
				fl.flushStmtTemps()
			}
		}
	}

	if !endsWithTerm(fl.curInstrs()) {
		fl.popScopeEmitDrops()
	} else {
		fl.scopes = fl.scopes[:len(fl.scopes)-1]
	}

	blkBody := fl.popInstrBuf()
	fl.emit(&InstrBlock{Body: blkBody})
	fl.emit(&InstrLabel{Name: exitLbl})

	if resSlot != nil {
		return resSlot.CName
	}
	return ""
}

func (fl *funcLowerer) lowerIfExpr(ie *ast.IfExpr, needValue bool) string {
	inf := fl.info(ie)
	exitLbl := fmt.Sprintf("_ll_exit_%d", inf.TargetID)

	var resSlot *Local
	if needValue && inf.Type != nil && !inf.Type.Equals(types.TypeVoid) && !inf.Type.Equals(types.TypeNever) {
		resSlot = fl.allocSlot("_ll_if", inf.Type)
	}

	// Determine branch target type (note: for `else`-less `if`, `inf.Type` is `?T` while branch yields `T`!)
	var branchTy types.Type
	isOptWithoutElse := false
	if resSlot != nil {
		branchTy = resSlot.Type
		if ie.Else == nil {
			if opt, ok := resSlot.Type.(*types.Optional); ok {
				branchTy = opt.Elem
				isOptWithoutElse = true
				fl.assignLocal(resSlot, optNoneExpr(opt))
			}
		}
	}

	var condExpr string
	var bindHoldSlot *Local
	unwrapSym := fl.bm.IfUnwrapSyms[ie]
	var unwrapInitInstr Instr

	switch ie.CondKind {
	case ast.IfCondBool:
		condExpr = fl.lowerExprRead(ie.Cond)

	case ast.IfCondOptVar:
		origSym := fl.bm.IfOriginSyms[ie]
		optTy := origSym.Type.(*types.Optional)
		origAddr := fl.symbolAddr(origSym)
		origVal := fmt.Sprintf("(*%s)", origAddr)
		condExpr = optHasValueExpr(origVal, optTy)

		if unwrapSym != nil {
			uLoc := &Local{
				CName:    unwrapSym.CName,
				Type:     unwrapSym.Type,
				IsMutPtr: ie.MutUnwrap,
			}
			fl.locals = append(fl.locals, uLoc)
			fl.symLocals[unwrapSym] = uLoc
			if ie.MutUnwrap {
				unwrapInitInstr = &InstrAssign{Dst: uLoc.CName, Expr: optUnwrapPtrExpr(origAddr, optTy)}
			} else {
				unwrapInitInstr = &InstrAssign{Dst: uLoc.CName, Expr: optUnwrapValExpr(origVal, optTy)}
			}
		}

	case ast.IfCondOptBind:
		optTy := fl.info(ie.Cond).Type.(*types.Optional)
		fl.tc.visit(optTy)
		if ie.MutUnwrap {
			condPtr := fl.lowerLValueAddr(ie.Cond)
			ptrTmp := fl.allocSlot("_ll_ifoptp", &types.Ptr{Elem: optTy})
			fl.assignLocal(ptrTmp, condPtr)
			condExpr = optHasValueExpr(fmt.Sprintf("(*%s)", ptrTmp.CName), optTy)
			if unwrapSym != nil {
				uLoc := &Local{CName: unwrapSym.CName, Type: unwrapSym.Type, IsMutPtr: true}
				fl.locals = append(fl.locals, uLoc)
				fl.symLocals[unwrapSym] = uLoc
				unwrapInitInstr = &InstrAssign{Dst: uLoc.CName, Expr: optUnwrapPtrExpr(ptrTmp.CName, optTy)}
			}
		} else {
			// Evaluate `ie.Cond` into a dedicated slot kept alive across the `then` branch!
			// Note: if `ie.Cond` is an lvalue (`user?`), borrow it without copying; if rvalue (`m["k"]`), own & drop after `if`!
			if isAddressableAST(ie.Cond) {
				condPtr := fl.lowerLValueAddr(ie.Cond)
				ptrTmp := fl.allocSlot("_ll_ifoptp", &types.Ptr{Elem: optTy})
				fl.assignLocal(ptrTmp, condPtr)
				condExpr = optHasValueExpr(fmt.Sprintf("(*%s)", ptrTmp.CName), optTy)
				if unwrapSym != nil {
					uLoc := &Local{CName: unwrapSym.CName, Type: unwrapSym.Type}
					fl.locals = append(fl.locals, uLoc)
					fl.symLocals[unwrapSym] = uLoc
					unwrapInitInstr = &InstrAssign{Dst: uLoc.CName, Expr: optUnwrapValExpr(fmt.Sprintf("(*%s)", ptrTmp.CName), optTy)}
				}
			} else {
				bindHoldSlot = fl.allocSlot("_ll_ifbind", optTy)
				valStr := fl.lowerRawExpr(ie.Cond, optTy, true)
				fl.assignLocal(bindHoldSlot, valStr)
				condExpr = optHasValueExpr(bindHoldSlot.CName, optTy)
				if unwrapSym != nil {
					uLoc := &Local{CName: unwrapSym.CName, Type: unwrapSym.Type}
					fl.locals = append(fl.locals, uLoc)
					fl.symLocals[unwrapSym] = uLoc
					unwrapInitInstr = &InstrAssign{Dst: uLoc.CName, Expr: optUnwrapValExpr(bindHoldSlot.CName, optTy)}
				}
			}
		}
	}
	fl.flushStmtTemps()

	fl.pushScope(inf.TargetID, exitLbl, "", resSlot, nil, false)
	if bindHoldSlot != nil && !bindHoldSlot.Type.IsPlain() {
		// Ensure `bindHoldSlot` is dropped even if `then` unwinds out of `if`!
		fl.scopes[len(fl.scopes)-1].ownedLocals = append(fl.scopes[len(fl.scopes)-1].ownedLocals, bindHoldSlot)
	}

	// Lower `Then` branch
	fl.pushInstrBuf()
	if unwrapInitInstr != nil {
		fl.emit(unwrapInitInstr)
	}
	fl.pushScope(0, "", "", nil, nil, false)
	if !needValue || resSlot == nil {
		fl.lowerVoidExpr(ie.Then)
		fl.flushStmtTemps()
	} else {
		tTy := fl.info(ie.Then).Type
		if tTy == types.TypeNever {
			fl.lowerVoidExpr(ie.Then)
			fl.flushStmtTemps()
		} else {
			tVal := fl.lowerExprOwned(ie.Then, branchTy)
			if isOptWithoutElse {
				fl.assignLocal(resSlot, fl.wrapInOptionalExpr(tVal, resSlot.Type.(*types.Optional)))
			} else {
				fl.assignLocal(resSlot, tVal)
			}
			fl.flushStmtTemps()
		}
	}
	if !endsWithTerm(fl.curInstrs()) {
		fl.popScopeEmitDrops()
	} else {
		fl.scopes = fl.scopes[:len(fl.scopes)-1]
	}
	thenBody := fl.popInstrBuf()

	// Lower `Else` branch (if present)
	var elseBody []Instr
	if ie.Else != nil {
		fl.pushInstrBuf()
		fl.pushScope(0, "", "", nil, nil, false)
		if !needValue || resSlot == nil {
			fl.lowerVoidExpr(ie.Else)
			fl.flushStmtTemps()
		} else {
			eTy := fl.info(ie.Else).Type
			if eTy == types.TypeNever {
				fl.lowerVoidExpr(ie.Else)
				fl.flushStmtTemps()
			} else {
				eVal := fl.lowerExprOwned(ie.Else, branchTy)
				fl.assignLocal(resSlot, eVal)
				fl.flushStmtTemps()
			}
		}
		if !endsWithTerm(fl.curInstrs()) {
			fl.popScopeEmitDrops()
		} else {
			fl.scopes = fl.scopes[:len(fl.scopes)-1]
		}
		elseBody = fl.popInstrBuf()
	}

	fl.emit(&InstrIf{Cond: condExpr, Then: thenBody, Else: elseBody})

	// Pop outer `if` scope (dropping `bindHoldSlot` if not already unwound)
	fl.popScopeEmitDrops()
	fl.emit(&InstrLabel{Name: exitLbl})

	if resSlot != nil {
		return resSlot.CName
	}
	return ""
}
