package ir

import (
	"fmt"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// -----------------------------------------------------------------------------
// Loops (`ForExpr`) & Pattern Matching (`MatchExpr`)
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lookupMethod(recvTy types.Type, name string) *sema.FuncInstance {
	key := sema.TypeMethodKey(recvTy)
	if mset, ok := fl.prog.MethodTable[key]; ok {
		return mset[name]
	}
	return nil
}

func (fl *funcLowerer) lowerWhileExpr(we *ast.WhileExpr, needValue bool) Expr {
	inf := fl.info(we)
	exitLbl := fmt.Sprintf("_ll_exit_%d", inf.TargetID)
	contLbl := fmt.Sprintf("_ll_cont_%d", inf.TargetID)

	var resSlot *Local
	var hasValSlot *Local
	var loopElemTy types.Type
	isWhileNoElse := false
	if we.Else == nil && inf.Type != nil {
		_, isWhileNoElse = inf.Type.(*types.Optional)
	}

	if needValue && inf.Type != nil && !inf.Type.Equals(types.TypeVoid) && !inf.Type.Equals(types.TypeNever) {
		resSlot = fl.allocSlot("_ll_while_val", inf.Type)
		hasValSlot = fl.allocSlot("_ll_while_has", types.TypeBool)
		fl.assignLocal(hasValSlot, NewLitBool(false))
		if isWhileNoElse {
			optTy := inf.Type.(*types.Optional)
			loopElemTy = optTy.Elem
			fl.assignLocal(resSlot, optNoneExpr(optTy))
		} else {
			loopElemTy = inf.Type
		}
	}

	syms := fl.bm.WhileSymbols[we]
	sym1, sym2 := syms[0], syms[1]

	var loc1, loc2 *Local
	if sym1 != nil {
		loc1 = &Local{
			CName:    sym1.CName,
			Type:     sym1.Type,
			IsMutPtr: sym1.BindKind == sema.BindMutBorrow,
		}
		fl.locals = append(fl.locals, loc1)
		fl.symLocals[sym1] = loc1
	}
	if sym2 != nil {
		loc2 = &Local{
			CName:    sym2.CName,
			Type:     sym2.Type,
			IsMutPtr: sym2.BindKind == sema.BindMutBorrow,
		}
		fl.locals = append(fl.locals, loc2)
		fl.symLocals[sym2] = loc2
	}

	bodyRunner := func(loopScope *mirScope) {
		bodyTy := fl.info(we.Body).Type
		if !needValue || resSlot == nil || bodyTy == nil || bodyTy.Equals(types.TypeVoid) || bodyTy.Equals(types.TypeNever) {
			fl.lowerVoidExpr(we.Body)
			fl.flushStmtTemps()
		} else {
			elemTy := loopElemTy
			needsWrap := isWhileNoElse
			if isWhileNoElse && (bodyTy.Equals(resSlot.Type) || fl.info(we.Body).WrapOptional) {
				elemTy = resSlot.Type
				needsWrap = false
			}
			iterVal := fl.lowerExprOwned(we.Body, elemTy)
			iterTmp := fl.allocSlot("_ll_itval", elemTy)
			fl.assignLocal(iterTmp, iterVal)
			fl.flushStmtTemps()
			fl.storeLoopRunningResult(loopScope, NewLocalExpr(iterTmp), needsWrap)
		}
	}

	fl.lowerLoopDriver(we.Kind, we.CondOrIter, we.RangeEnd, we.Inclusive, sym1, sym2, loc1, loc2, inf.TargetID, exitLbl, contLbl, resSlot, hasValSlot, loopElemTy, isWhileNoElse, bodyRunner)

	if we.Else != nil {
		if resSlot != nil && hasValSlot != nil {
			fl.pushInstrBuf()
			fl.pushScope(0, "", "", nil, nil, false)
			eTy := fl.info(we.Else).Type
			if eTy == types.TypeNever {
				fl.lowerVoidExpr(we.Else)
				fl.flushStmtTemps()
			} else {
				eVal := fl.lowerExprOwned(we.Else, resSlot.Type)
				fl.assignLocal(resSlot, eVal)
				fl.flushStmtTemps()
			}
			if !endsWithTerm(fl.curInstrs()) {
				fl.popScopeEmitDrops()
			} else {
				fl.scopes = fl.scopes[:len(fl.scopes)-1]
			}
			elseBranch := fl.popInstrBuf()
			fl.emit(NewInstrIf(&ExprUnary{Op: token.KwNot, Operand: NewLocalExpr(hasValSlot), Typ: types.TypeBool}, elseBranch, nil))
		} else {
			fl.lowerVoidExpr(we.Else)
			fl.flushStmtTemps()
		}
	}

	if resSlot != nil {
		return NewLocalExpr(resSlot)
	}
	return nil
}

func (fl *funcLowerer) lowerForExpr(fe *ast.ForExpr, needValue bool) Expr {
	inf := fl.info(fe)
	exitLbl := fmt.Sprintf("_ll_exit_%d", inf.TargetID)
	contLbl := fmt.Sprintf("_ll_cont_%d", inf.TargetID)

	var colSlot *Local
	var elemTy types.Type

	if needValue && inf.Type != nil && !inf.Type.Equals(types.TypeVoid) && !inf.Type.Equals(types.TypeNever) {
		colSlot = fl.allocSlot("_ll_col", inf.Type)
		fl.tc.visit(inf.Type)
		// Start from an empty collection each time, as a comprehension inside a loop is
		// evaluated again after the previous result was moved away.
		fl.emit(&InstrAssign{Dst: NewLocalExpr(colSlot), Expr: &ExprLitZero{Typ: inf.Type}})
		if colSlot.MovedLocal != nil {
			fl.emit(&InstrAssign{Dst: NewLocalExpr(colSlot.MovedLocal), Expr: NewLitBool(false)})
		}
		if fe.Body != nil {
			elemTy = fl.info(fe.Body).Type
		}
	}

	if fl.bm != nil {
		if uf, ok := fl.bm.ComptimeForUnrolls[fe]; ok && uf != nil {
			for i, it := range uf.Iters {
				iterContLbl := fmt.Sprintf("_ll_cf_cont_%d_%d", inf.TargetID, i)
				forScope := fl.pushScope(inf.TargetID, exitLbl, iterContLbl, colSlot, nil, false)
				forScope.loopElemTy = elemTy

				fl.pushInstrBuf()
				fl.pushScope(0, "", "", nil, nil, false)
				bodyTy := fl.info(it.Body).Type
				if !needValue || colSlot == nil || bodyTy == nil || bodyTy.Equals(types.TypeVoid) || bodyTy.Equals(types.TypeNever) {
					fl.lowerVoidExpr(it.Body)
					fl.flushStmtTemps()
				} else {
					itemTargetTy := bodyTy
					switch cv := types.CollView(colSlot.Type).(type) {
					case *types.Vector:
						itemTargetTy = cv.Elem
					case *types.Set:
						itemTargetTy = cv.Elem
					}
					itemVal := fl.lowerExprOwned(it.Body, itemTargetTy)
					fl.flushStmtTemps()
					fl.emitPushToCol(colSlot, itemVal, itemTargetTy)
				}
				if !endsWithTerm(fl.curInstrs()) {
					fl.popScopeEmitDrops()
				} else {
					fl.scopes = fl.scopes[:len(fl.scopes)-1]
				}
				iterInstrs := fl.popInstrBuf()
				fl.emit(&InstrBlock{Body: iterInstrs})
				fl.emit(&InstrLabel{Name: iterContLbl})
				fl.popScopeEmitDrops()
			}
			if needValue && colSlot != nil {
				fl.emit(&InstrLabel{Name: exitLbl})
				fl.lowerForElse(fe, true, colSlot)
				return NewLocalExpr(colSlot)
			}
			fl.lowerForElse(fe, false, nil)
			fl.emit(&InstrLabel{Name: exitLbl})
			return nil
		}
	}

	syms := fl.bm.ForSymbols[fe]
	sym1, sym2 := syms[0], syms[1]

	var loc1, loc2 *Local
	if sym1 != nil {
		loc1 = &Local{
			CName:    sym1.CName,
			Type:     sym1.Type,
			IsMutPtr: sym1.BindKind == sema.BindMutBorrow,
		}
		fl.locals = append(fl.locals, loc1)
		fl.symLocals[sym1] = loc1
	}
	if sym2 != nil {
		loc2 = &Local{
			CName:    sym2.CName,
			Type:     sym2.Type,
			IsMutPtr: sym2.BindKind == sema.BindMutBorrow,
		}
		fl.locals = append(fl.locals, loc2)
		fl.symLocals[sym2] = loc2
	}

	bodyRunner := func(loopScope *mirScope) {
		bodyTy := fl.info(fe.Body).Type
		if !needValue || colSlot == nil || bodyTy == nil || bodyTy.Equals(types.TypeVoid) || bodyTy.Equals(types.TypeNever) {
			fl.lowerVoidExpr(fe.Body)
			fl.flushStmtTemps()
		} else {
			itemVal := fl.lowerExprOwned(fe.Body, elemTy)
			fl.flushStmtTemps()
			fl.emitPushToCol(colSlot, itemVal, elemTy)
		}
	}

	fl.lowerLoopDriver(fe.Kind, fe.CondOrIter, fe.RangeEnd, fe.Inclusive, sym1, sym2, loc1, loc2, inf.TargetID, exitLbl, contLbl, colSlot, nil, elemTy, false, bodyRunner)
	fl.lowerForElse(fe, needValue, colSlot)

	if colSlot != nil {
		return NewLocalExpr(colSlot)
	}
	return nil
}

func (fl *funcLowerer) lowerForElse(fe *ast.ForExpr, needValue bool, colSlot *Local) {
	if fe.Else == nil {
		return
	}
	if needValue && colSlot != nil {
		var isEmptyCond Expr
		switch types.CollView(colSlot.Type).(type) {
		case *types.Vector, *types.Set, *types.Map:
			isEmptyCond = &ExprBinary{
				Op:    token.Eq,
				Left:  &ExprField{Base: NewLocalExpr(colSlot), FieldName: "len", Typ: types.TypeInt},
				Right: NewLitInt(0, false, types.TypeInt),
				Typ:   types.TypeBool,
			}
		}
		if isEmptyCond != nil {
			fl.pushInstrBuf()
			fl.pushScope(0, "", "", nil, nil, false)
			eVal := fl.lowerExprOwned(fe.Else, colSlot.Type)
			fl.assignLocal(colSlot, eVal)
			fl.flushStmtTemps()
			if !endsWithTerm(fl.curInstrs()) {
				fl.popScopeEmitDrops()
			} else {
				fl.scopes = fl.scopes[:len(fl.scopes)-1]
			}
			elseBranch := fl.popInstrBuf()
			fl.emit(NewInstrIf(isEmptyCond, elseBranch, nil))
			return
		}
	}
	fl.lowerVoidExpr(fe.Else)
	fl.flushStmtTemps()
}

func (fl *funcLowerer) emitPushToCol(colSlot *Local, itemVal Expr, elemTy types.Type) {
	colAddr := &ExprAddrOf{Place: NewLocalExpr(colSlot), Typ: &types.Ptr{Elem: colSlot.Type}}
	switch ct := types.CollView(colSlot.Type).(type) {
	case *types.Vector:
		fl.emit(&InstrExpr{Expr: &ExprVecPush{VecPtr: &ExprAddrOf{Place: NewLocalExpr(colSlot), Typ: &types.Ptr{Elem: ct}}, Item: EnsureExpr(itemVal, ct.Elem), VecType: ct}})
	case *types.Set:
		fl.emit(&InstrExpr{Expr: fl.callMethod(colSlot.Type, "insert", colAddr, EnsureExpr(itemVal, ct.Elem))})
	case *types.Map:
		entrySlot := fl.allocSlot("_ll_entry", elemTy)
		fl.assignLocal(entrySlot, itemVal)
		fl.emit(&InstrExpr{Expr: fl.callMethod(colSlot.Type, "insert", colAddr,
			&ExprField{Base: NewLocalExpr(entrySlot), FieldName: "ll_key", Typ: ct.Key},
			&ExprField{Base: NewLocalExpr(entrySlot), FieldName: "ll_value", Typ: ct.Value},
		)})
	default:
		ct = colSlot.Type
		fi := fl.lookupMethod(ct, "push")
		if fi != nil {
			fl.emit(&InstrExpr{Expr: &ExprCall{
				Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
				Args:   []Expr{&ExprAddrOf{Place: NewLocalExpr(colSlot), Typ: &types.Ptr{Elem: ct}}, EnsureExpr(itemVal, elemTy)},
				Typ:    types.TypeVoid,
			}})
		}
	}
}

func (fl *funcLowerer) lowerLoopDriver(
	kind ast.ForKind,
	condOrIter ast.Expr,
	rangeEnd ast.Expr,
	inclusive bool,
	sym1, sym2 *sema.Symbol,
	loc1, loc2 *Local,
	targetID int,
	exitLbl, contLbl string,
	resSlot, hasValSlot *Local,
	loopElemTy types.Type,
	isForNoElse bool,
	bodyRunner func(loopScope *mirScope),
) {
	var colHoldSlot *Local

	emitBodyIteration := func(stepInit []Instr, stepAdv []Instr, whileCond any) {
		forScope := fl.pushScope(targetID, exitLbl, contLbl, resSlot, hasValSlot, false)
		forScope.loopElemTy = loopElemTy
		forScope.isForNoElse = isForNoElse
		if colHoldSlot != nil && !colHoldSlot.Type.IsPlain() {
			forScope.ownedLocals = append(forScope.ownedLocals, colHoldSlot)
		}

		fl.pushInstrBuf()
		for _, si := range stepInit {
			fl.emit(si)
		}

		// Inner body scope
		fl.pushScope(0, "", "", nil, nil, false)
		bodyRunner(forScope)

		if !endsWithTerm(fl.curInstrs()) {
			fl.popScopeEmitDrops()
		} else {
			fl.scopes = fl.scopes[:len(fl.scopes)-1]
		}

		fl.emit(&InstrLabel{Name: contLbl})
		for _, sa := range stepAdv {
			fl.emit(sa)
		}
		whileBody := fl.popInstrBuf()

		fl.emit(NewInstrWhile(whileCond, whileBody))
		fl.popScopeEmitDrops()
		fl.emit(&InstrLabel{Name: exitLbl})
	}

	emitNumericOrCharRange := func(loVal, hiVal Expr, elemTy types.Type, incVal Expr) {
		curIdx := fl.allocSlot("_ll_rcur", elemTy)
		endIdx := fl.allocSlot("_ll_rend", elemTy)
		incSlot := fl.allocSlot("_ll_rinc", types.TypeBool)
		doneSlot := fl.allocSlot("_ll_rdone", types.TypeBool)
		fl.assignLocal(curIdx, loVal)
		fl.assignLocal(endIdx, hiVal)
		fl.assignLocal(incSlot, incVal)
		fl.flushStmtTemps()

		fl.emit(NewInstrIf(
			NewLocalExpr(incSlot),
			[]Instr{&InstrAssign{Dst: NewLocalExpr(doneSlot), Expr: &ExprBinary{Op: token.Gt, Left: NewLocalExpr(curIdx), Right: NewLocalExpr(endIdx), Typ: types.TypeBool}}},
			[]Instr{&InstrAssign{Dst: NewLocalExpr(doneSlot), Expr: &ExprBinary{Op: token.Gte, Left: NewLocalExpr(curIdx), Right: NewLocalExpr(endIdx), Typ: types.TypeBool}}},
		))

		stepInit := []Instr{
			&InstrAssign{Dst: NewLocalExpr(loc1), Expr: NewLocalExpr(curIdx)},
		}
		incExpr := &ExprBinary{Op: token.Plus, Left: NewLocalExpr(curIdx), Right: NewLitInt(1, false, elemTy), Typ: elemTy}
		stepAdv := []Instr{
			NewInstrIf(
				NewLocalExpr(incSlot),
				[]Instr{
					NewInstrIf(
						&ExprBinary{Op: token.Eq, Left: NewLocalExpr(curIdx), Right: NewLocalExpr(endIdx), Typ: types.TypeBool},
						[]Instr{&InstrAssign{Dst: NewLocalExpr(doneSlot), Expr: NewLitBool(true)}},
						[]Instr{&InstrAssign{Dst: NewLocalExpr(curIdx), Expr: incExpr}},
					),
				},
				[]Instr{
					&InstrAssign{Dst: NewLocalExpr(curIdx), Expr: incExpr},
					NewInstrIf(
						&ExprBinary{Op: token.Gte, Left: NewLocalExpr(curIdx), Right: NewLocalExpr(endIdx), Typ: types.TypeBool},
						[]Instr{&InstrAssign{Dst: NewLocalExpr(doneSlot), Expr: NewLitBool(true)}},
						nil,
					),
				},
			),
		}
		emitBodyIteration(stepInit, stepAdv, &ExprUnary{Op: token.KwNot, Operand: NewLocalExpr(doneSlot), Typ: types.TypeBool})
	}

	switch kind {
	case ast.ForCond:
		forScope := fl.pushScope(targetID, exitLbl, contLbl, resSlot, hasValSlot, false)
		forScope.loopElemTy = loopElemTy
		forScope.isForNoElse = isForNoElse

		fl.pushInstrBuf()
		condExpr := fl.lowerExprRead(condOrIter)
		condSlot := fl.allocSlot("_ll_lcond", types.TypeBool)
		fl.assignLocal(condSlot, condExpr)
		fl.flushStmtTemps()
		fl.emit(NewInstrIf(&ExprUnary{Op: token.KwNot, Operand: NewLocalExpr(condSlot), Typ: types.TypeBool}, []Instr{&InstrGoto{Label: exitLbl}}, nil))
		fl.usedLabels[exitLbl] = true

		fl.pushScope(0, "", "", nil, nil, false)
		bodyRunner(forScope)
		if !endsWithTerm(fl.curInstrs()) {
			fl.popScopeEmitDrops()
		} else {
			fl.scopes = fl.scopes[:len(fl.scopes)-1]
		}
		fl.emit(&InstrLabel{Name: contLbl})
		whileBody := fl.popInstrBuf()

		fl.emit(NewInstrWhile(NewLitBool(true), whileBody))
		fl.popScopeEmitDrops()
		fl.emit(&InstrLabel{Name: exitLbl})

	case ast.ForRange:
		loTy := sym1.Type
		loExpr := fl.lowerExprRead(condOrIter)
		hiExpr := fl.lowerExprRead(rangeEnd)
		emitNumericOrCharRange(loExpr, hiExpr, loTy, NewLitBool(inclusive))

	case ast.ForIter:
		rawColTy := fl.info(condOrIter).Type
		colTy := rawColTy
		isArenaRef := false
		if rf, ok := rawColTy.(*types.Ref); ok {
			colTy = rf.Elem
			isArenaRef = true
		}

		var colPtrExpr Expr
		if isArenaRef {
			ptrTmp := fl.allocSlot("_ll_colp", rawColTy)
			fl.assignLocal(ptrTmp, fl.lowerExprRead(condOrIter))
			colPtrExpr = NewLocalExpr(ptrTmp)
		} else if fl.isAddressable(condOrIter) {
			ptrTmp := fl.allocSlot("_ll_colp", &types.Ptr{Elem: colTy})
			fl.assignLocal(ptrTmp, fl.lowerLValueAddr(condOrIter))
			colPtrExpr = NewLocalExpr(ptrTmp)
		} else {
			colHoldSlot = fl.allocSlot("_ll_colhold", colTy)
			fl.assignLocal(colHoldSlot, fl.lowerRawExpr(condOrIter, colTy, true))
			colPtrExpr = &ExprAddrOf{Place: NewLocalExpr(colHoldSlot), Typ: &types.Ptr{Elem: colTy}}
		}
		fl.flushStmtTemps()

		switch c := types.CollView(colTy).(type) {
		case *types.Vector:
			idxVar := fl.allocSlot("_ll_vi", types.TypeInt)
			fl.assignLocal(idxVar, NewLitInt(0, false, types.TypeInt))
			var stepInit []Instr
			dataField := &ExprField{Base: colPtrExpr, FieldName: "data", Typ: &types.Ptr{Elem: c.Elem}, IsPtr: true}
			elemAccess := &ExprIndex{Base: dataField, Index: NewLocalExpr(idxVar), Typ: c.Elem}
			if loc2 == nil {
				if loc1.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: &ExprAddrOf{Place: elemAccess, Typ: &types.Ptr{Elem: c.Elem}}})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: elemAccess})
				}
			} else {
				stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: NewLocalExpr(idxVar)})
				if loc2.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: &ExprAddrOf{Place: elemAccess, Typ: &types.Ptr{Elem: c.Elem}}})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: elemAccess})
				}
			}
			incExpr := &ExprBinary{Op: token.Plus, Left: NewLocalExpr(idxVar), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
			stepAdv := []Instr{&InstrAssign{Dst: NewLocalExpr(idxVar), Expr: incExpr}}
			lenField := &ExprField{Base: colPtrExpr, FieldName: "len", Typ: types.TypeInt, IsPtr: true}
			loopCond := &ExprBinary{Op: token.Lt, Left: NewLocalExpr(idxVar), Right: lenField, Typ: types.TypeBool}
			emitBodyIteration(stepInit, stepAdv, loopCond)

		case *types.StackArray:
			idxVar := fl.allocSlot("_ll_ai", types.TypeInt)
			fl.assignLocal(idxVar, NewLitInt(0, false, types.TypeInt))
			var stepInit []Instr
			dataField := &ExprField{Base: colPtrExpr, FieldName: "data", Typ: &types.Ptr{Elem: c.Elem}, IsPtr: true}
			elemAccess := &ExprIndex{Base: dataField, Index: NewLocalExpr(idxVar), Typ: c.Elem}
			if loc2 == nil {
				if loc1.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: &ExprAddrOf{Place: elemAccess, Typ: &types.Ptr{Elem: c.Elem}}})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: elemAccess})
				}
			} else {
				stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: NewLocalExpr(idxVar)})
				if loc2.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: &ExprAddrOf{Place: elemAccess, Typ: &types.Ptr{Elem: c.Elem}}})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: elemAccess})
				}
			}
			incExpr := &ExprBinary{Op: token.Plus, Left: NewLocalExpr(idxVar), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
			stepAdv := []Instr{&InstrAssign{Dst: NewLocalExpr(idxVar), Expr: incExpr}}
			loopCond := &ExprBinary{Op: token.Lt, Left: NewLocalExpr(idxVar), Right: NewLitInt(uint64(c.Size), false, types.TypeInt), Typ: types.TypeBool}
			emitBodyIteration(stepInit, stepAdv, loopCond)

		case *types.Primitive: // `string` UTF-8 code point iteration!
			byteOff := fl.allocSlot("_ll_soff", types.TypeInt)
			nextOff := fl.allocSlot("_ll_snext", types.TypeInt)
			chSlot := fl.allocSlot("_ll_sch", types.TypeChar)
			fl.assignLocal(byteOff, NewLitInt(0, false, types.TypeInt))
			var stepInit []Instr
			strDeref := &ExprDeref{Ptr: colPtrExpr, Typ: types.TypeString}
			nextOffAddr := &ExprAddrOf{Place: NewLocalExpr(nextOff), Typ: &types.Ptr{Elem: types.TypeInt}}
			utf8Fn := fl.lookupCoreFunc("str_utf8_next")
			callee := &ExprFuncRef{Name: "ll_fn_str_utf8_next"}
			if utf8Fn != nil {
				callee = &ExprFuncRef{Name: utf8Fn.CName, TargetFunc: utf8Fn, FuncType: utf8Fn.Sig}
			}
			utf8Call := &ExprCall{
				Callee: callee,
				Args:   []Expr{strDeref, NewLocalExpr(byteOff), nextOffAddr},
				Typ:    types.TypeChar,
			}
			stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(chSlot), Expr: utf8Call})
			if loc2 == nil {
				stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: NewLocalExpr(chSlot)})
			} else {
				stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: NewLocalExpr(byteOff)})
				stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: NewLocalExpr(chSlot)})
			}
			stepAdv := []Instr{&InstrAssign{Dst: NewLocalExpr(byteOff), Expr: NewLocalExpr(nextOff)}}
			lenField := &ExprField{Base: colPtrExpr, FieldName: "len", Typ: types.TypeInt, IsPtr: true}
			loopCond := &ExprBinary{Op: token.Lt, Left: NewLocalExpr(byteOff), Right: lenField, Typ: types.TypeBool}
			emitBodyIteration(stepInit, stepAdv, loopCond)

		case *types.Set:
			idxVar := fl.allocSlot("_ll_si", types.TypeInt)
			fl.assignLocal(idxVar, NewLitInt(0, false, types.TypeInt))
			fl.usedLabels[contLbl] = true
			entriesF, entLenF, setEntSt := collEntries(colTy)
			fl.tc.visit(setEntSt)
			entriesField := &ExprField{Base: colPtrExpr, FieldName: entriesF.CName, Typ: entriesF.Type, IsPtr: true}
			entryAccess := &ExprIndex{Base: entriesField, Index: NewLocalExpr(idxVar), Typ: setEntSt}
			aliveField := &ExprField{Base: entryAccess, FieldName: collField(setEntSt, "alive").CName, Typ: types.TypeBool}
			notAlive := &ExprUnary{Op: token.KwNot, Operand: aliveField, Typ: types.TypeBool}
			keyField := &ExprField{Base: entryAccess, FieldName: collField(setEntSt, "key").CName, Typ: c.Elem}
			stepInit := []Instr{
				NewInstrIf(notAlive, []Instr{&InstrGoto{Label: contLbl}}, nil),
				&InstrAssign{Dst: NewLocalExpr(loc1), Expr: keyField},
			}
			incExpr := &ExprBinary{Op: token.Plus, Left: NewLocalExpr(idxVar), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
			stepAdv := []Instr{&InstrAssign{Dst: NewLocalExpr(idxVar), Expr: incExpr}}
			entLenField := &ExprField{Base: colPtrExpr, FieldName: entLenF.CName, Typ: types.TypeInt, IsPtr: true}
			loopCond := &ExprBinary{Op: token.Lt, Left: NewLocalExpr(idxVar), Right: entLenField, Typ: types.TypeBool}
			emitBodyIteration(stepInit, stepAdv, loopCond)

		case *types.Map:
			idxVar := fl.allocSlot("_ll_mi", types.TypeInt)
			fl.assignLocal(idxVar, NewLitInt(0, false, types.TypeInt))
			fl.usedLabels[contLbl] = true
			entriesF, entLenF, mapEntSt := collEntries(colTy)
			fl.tc.visit(mapEntSt)
			entriesField := &ExprField{Base: colPtrExpr, FieldName: entriesF.CName, Typ: entriesF.Type, IsPtr: true}
			entryAccess := &ExprIndex{Base: entriesField, Index: NewLocalExpr(idxVar), Typ: mapEntSt}
			aliveField := &ExprField{Base: entryAccess, FieldName: collField(mapEntSt, "alive").CName, Typ: types.TypeBool}
			notAlive := &ExprUnary{Op: token.KwNot, Operand: aliveField, Typ: types.TypeBool}
			keyAccess := &ExprField{Base: entryAccess, FieldName: collField(mapEntSt, "key").CName, Typ: c.Key}
			valAccess := &ExprField{Base: entryAccess, FieldName: collField(mapEntSt, "val").CName, Typ: c.Value}
			var stepInit []Instr
			stepInit = append(stepInit, NewInstrIf(notAlive, []Instr{&InstrGoto{Label: contLbl}}, nil))
			if loc2 == nil {
				fl.tc.visit(loc1.Type)
				entryLit := &ExprConstructStruct{
					StructType: loc1.Type.(*types.Struct),
					Fields: []StructFieldInit{
						{Name: "ll_key", Value: keyAccess},
						{Name: "ll_value", Value: valAccess},
					},
				}
				stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: entryLit})
			} else {
				stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: keyAccess})
				if loc2.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: &ExprAddrOf{Place: valAccess, Typ: &types.Ptr{Elem: c.Value}}})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: valAccess})
				}
			}
			incExpr := &ExprBinary{Op: token.Plus, Left: NewLocalExpr(idxVar), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
			stepAdv := []Instr{&InstrAssign{Dst: NewLocalExpr(idxVar), Expr: incExpr}}
			entLenField := &ExprField{Base: colPtrExpr, FieldName: entLenF.CName, Typ: types.TypeInt, IsPtr: true}
			loopCond := &ExprBinary{Op: token.Lt, Left: NewLocalExpr(idxVar), Right: entLenField, Typ: types.TypeBool}
			emitBodyIteration(stepInit, stepAdv, loopCond)

		case *types.Struct:
			if fl.prog != nil && fl.prog.GenStructRange != nil &&
				fl.prog.StructMods[c] == fl.prog.GenStructRange.Mod &&
				(c.Name == "Range" || strings.HasPrefix(c.Name, "Range[")) &&
				len(c.TypeArgs) == 1 && len(c.Fields) >= 3 {
				elemTy := c.TypeArgs[0]
				loVal := &ExprField{Base: colPtrExpr, FieldName: c.Fields[0].CName, Typ: elemTy, IsPtr: true}
				hiVal := &ExprField{Base: colPtrExpr, FieldName: c.Fields[1].CName, Typ: elemTy, IsPtr: true}
				incVal := &ExprField{Base: colPtrExpr, FieldName: c.Fields[2].CName, Typ: types.TypeBool, IsPtr: true}
				emitNumericOrCharRange(loVal, hiVal, elemTy, incVal)
			} else if fl.prog != nil {
				if poolMod := fl.prog.StructMods[c]; poolMod != nil && poolMod.Pkg != nil && poolMod.Pkg.ID == "std/pool" &&
					(c.Name == "Pool" || strings.HasPrefix(c.Name, "Pool[")) &&
					len(c.TypeArgs) == 1 && len(c.Fields) >= 1 {
					if slotsVecTy, isVec := c.Fields[0].Type.(*types.Vector); isVec {
						if slotSt, isSlotSt := slotsVecTy.Elem.(*types.Struct); isSlotSt && len(slotSt.Fields) >= 3 {
							if optTy, isOpt := slotSt.Fields[0].Type.(*types.Optional); isOpt {
								fl.tc.visit(slotSt)
								fl.tc.visit(optTy)
								idxVar := fl.allocSlot("_ll_pi", types.TypeInt)
								fl.assignLocal(idxVar, NewLitInt(0, false, types.TypeInt))
								fl.usedLabels[contLbl] = true

								slotsField := &ExprField{Base: colPtrExpr, FieldName: c.Fields[0].CName, FieldIdx: 0, Typ: slotsVecTy, IsPtr: true}
								dataField := &ExprField{Base: slotsField, FieldName: "data", Typ: &types.Ptr{Elem: slotSt}, IsPtr: false}
								lenField := &ExprField{Base: slotsField, FieldName: "len", Typ: types.TypeInt, IsPtr: false}
								slotAccess := &ExprIndex{Base: dataField, Index: NewLocalExpr(idxVar), Typ: slotSt}
								aliveField := &ExprField{Base: slotAccess, FieldName: slotSt.Fields[2].CName, FieldIdx: 2, Typ: types.TypeBool}
								notAlive := &ExprUnary{Op: token.KwNot, Operand: aliveField, Typ: types.TypeBool}
								optField := &ExprField{Base: slotAccess, FieldName: slotSt.Fields[0].CName, FieldIdx: 0, Typ: optTy}

								var stepInit []Instr
								stepInit = append(stepInit, NewInstrIf(notAlive, []Instr{&InstrGoto{Label: contLbl}}, nil))
								if loc2 == nil {
									if loc1.IsMutPtr {
										optPtr := &ExprAddrOf{Place: optField, Typ: &types.Ptr{Elem: optTy}}
										stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: optUnwrapPtrExpr(optPtr, optTy)})
									} else {
										stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: optUnwrapValExpr(optField, optTy)})
									}
								} else {
									handleSt := loc1.Type.(*types.Struct)
									fl.tc.visit(handleSt)
									genField := &ExprField{Base: slotAccess, FieldName: slotSt.Fields[1].CName, FieldIdx: 1, Typ: types.TypeInt}
									handleLit := &ExprConstructStruct{
										StructType: handleSt,
										Fields: []StructFieldInit{
											{Name: handleSt.Fields[0].CName, Index: 0, Value: NewLocalExpr(idxVar)},
											{Name: handleSt.Fields[1].CName, Index: 1, Value: genField},
										},
									}
									stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc1), Expr: handleLit})
									if loc2.IsMutPtr {
										optPtr := &ExprAddrOf{Place: optField, Typ: &types.Ptr{Elem: optTy}}
										stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: optUnwrapPtrExpr(optPtr, optTy)})
									} else {
										stepInit = append(stepInit, &InstrAssign{Dst: NewLocalExpr(loc2), Expr: optUnwrapValExpr(optField, optTy)})
									}
								}
								incExpr := &ExprBinary{Op: token.Plus, Left: NewLocalExpr(idxVar), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
								stepAdv := []Instr{&InstrAssign{Dst: NewLocalExpr(idxVar), Expr: incExpr}}
								loopCond := &ExprBinary{Op: token.Lt, Left: NewLocalExpr(idxVar), Right: lenField, Typ: types.TypeBool}
								emitBodyIteration(stepInit, stepAdv, loopCond)
							}
						}
					}
				}
			}
		}
	}
}

func (fl *funcLowerer) lowerMatchExpr(me *ast.MatchExpr, needValue bool) Expr {
	inf := fl.info(me)
	exitLbl := fmt.Sprintf("_ll_exit_%d", inf.TargetID)

	var resSlot *Local
	if needValue && inf.Type != nil && !inf.Type.Equals(types.TypeVoid) && !inf.Type.Equals(types.TypeNever) {
		resSlot = fl.allocSlot("_ll_match", inf.Type)
	}

	// Temporaries of the enclosing expression (the left operand of `a == match(...)`) must
	// outlive this match: its own statement boundaries free only what it creates. An arm that
	// unwinds frees them on its way out.
	outerTemps := fl.stmtTemps
	fl.stmtTemps = nil
	defer func() { fl.stmtTemps = append(outerTemps, fl.stmtTemps...) }()

	if fl.bm != nil {
		if armIdx, ok := fl.bm.ComptimeMatchArm[me]; ok {
			arms := me.Arms
			if expanded, hasExp := fl.bm.MatchArms[me]; hasExp {
				arms = expanded
			}
			if armIdx >= 0 && armIdx < len(arms) {
				chosen := arms[armIdx]
				fl.pushScope(inf.TargetID, exitLbl, "", resSlot, nil, false)
				fl.pushInstrBuf()
				fl.pushScope(0, "", "", nil, nil, false)
				aTy := fl.info(chosen.Body).Type
				if aTy == types.TypeNever && len(outerTemps) > 0 {
					fl.stmtTemps = append([]*Local(nil), outerTemps...)
				}
				if !needValue || resSlot == nil {
					fl.lowerVoidExpr(chosen.Body)
					fl.flushStmtTemps()
				} else if aTy == types.TypeNever {
					fl.lowerVoidExpr(chosen.Body)
					fl.flushStmtTemps()
				} else {
					aVal := fl.lowerExprOwned(chosen.Body, resSlot.Type)
					fl.assignLocal(resSlot, aVal)
					fl.flushStmtTemps()
				}
				if !endsWithTerm(fl.curInstrs()) {
					fl.popScopeEmitDrops()
				} else {
					fl.scopes = fl.scopes[:len(fl.scopes)-1]
				}
				armBody := fl.popInstrBuf()
				fl.emit(&InstrBlock{Body: armBody})
				fl.popScopeEmitDrops()
				fl.emit(&InstrLabel{Name: exitLbl})
				if resSlot != nil {
					return NewLocalExpr(resSlot)
				}
				return nil
			}
		}
	}

	numScrut := len(me.Scrutinees)
	type scrutLowerInfo struct {
		rawTy         types.Type
		scrutTy       types.Type
		isArenaRef    bool
		scrutPtrExpr  Expr
		scrutValExpr  Expr
		scrutHoldSlot *Local
	}
	scrutInfos := make([]scrutLowerInfo, numScrut)
	for i, s := range me.Scrutinees {
		rawScrutTy := fl.info(s).Type
		sTy := rawScrutTy
		isRef := false
		if rf, ok := rawScrutTy.(*types.Ref); ok {
			if _, isEn := rf.Elem.(*types.Enum); isEn {
				sTy = rf.Elem
				isRef = true
			}
		}
		fl.tc.visit(sTy)
		info := scrutLowerInfo{
			rawTy:      rawScrutTy,
			scrutTy:    sTy,
			isArenaRef: isRef,
		}
		if isRef {
			ptrSlot := fl.allocSlot("_ll_scrutp", rawScrutTy)
			fl.assignLocal(ptrSlot, fl.lowerExprRead(s))
			info.scrutPtrExpr = NewLocalExpr(ptrSlot)
			info.scrutValExpr = &ExprDeref{Ptr: info.scrutPtrExpr, Typ: sTy}
		} else if !fl.info(s).WrapOptional && fl.isAddressable(s) {
			ptrSlot := fl.allocSlot("_ll_scrutp", &types.Ptr{Elem: sTy})
			fl.assignLocal(ptrSlot, fl.lowerLValueAddr(s))
			info.scrutPtrExpr = NewLocalExpr(ptrSlot)
			info.scrutValExpr = &ExprDeref{Ptr: info.scrutPtrExpr, Typ: sTy}
		} else {
			holdSlot := fl.allocSlot("_ll_scrut", sTy)
			fl.assignLocal(holdSlot, fl.lowerExprOwned(s, sTy))
			info.scrutHoldSlot = holdSlot
			info.scrutValExpr = NewLocalExpr(holdSlot)
			info.scrutPtrExpr = &ExprAddrOf{Place: info.scrutValExpr, Typ: &types.Ptr{Elem: sTy}}
		}
		scrutInfos[i] = info
	}
	fl.flushStmtTemps()

	fl.pushScope(inf.TargetID, exitLbl, "", resSlot, nil, false)
	for _, si := range scrutInfos {
		if si.scrutHoldSlot != nil && !si.scrutTy.IsPlain() {
			fl.scopes[len(fl.scopes)-1].ownedLocals = append(fl.scopes[len(fl.scopes)-1].ownedLocals, si.scrutHoldSlot)
		}
	}

	// Result chains carry expanded arms (implicit `value` arm, `default panic` arms) in BodyMaps.
	arms := me.Arms
	if expanded, ok := fl.bm.MatchArms[me]; ok {
		arms = expanded
	}
	// Build nested `if / else if / else` chain across `arms`
	var buildArmChain func(armIdx int) []Instr
	buildArmChain = func(armIdx int) []Instr {
		if armIdx >= len(arms) {
			return nil
		}
		arm := arms[armIdx]

		// Lower arm body (initializing any destructured variant bindings inside the arm scope!)
		fl.pushInstrBuf()
		armScope := fl.pushScope(0, "", "", nil, nil, false)
		if len(arm.Cases) == 1 {
			for i, pat := range arm.Cases[0].Patterns {
				if i < numScrut {
					if vp, isVP := pat.(*ast.VariantPattern); isVP {
						fl.emitVariantPatternBindings(vp, scrutInfos[i].scrutTy, scrutInfos[i].scrutPtrExpr, scrutInfos[i].scrutValExpr, armScope)
					} else if mp, isMP := pat.(*ast.MethodPattern); isMP {
						if vp := fl.bm.BareVariantPatterns[mp]; vp != nil {
							fl.emitVariantPatternBindings(vp, scrutInfos[i].scrutTy, scrutInfos[i].scrutPtrExpr, scrutInfos[i].scrutValExpr, armScope)
						}
					}
				}
			}
		}
		aTy := fl.info(arm.Body).Type
		if aTy == types.TypeNever && len(outerTemps) > 0 {
			fl.stmtTemps = append([]*Local(nil), outerTemps...)
		}
		if !needValue || resSlot == nil {
			fl.lowerVoidExpr(arm.Body)
			fl.flushStmtTemps()
		} else {
			if aTy == types.TypeNever {
				fl.lowerVoidExpr(arm.Body)
				fl.flushStmtTemps()
			} else {
				aVal := fl.lowerExprOwned(arm.Body, resSlot.Type)
				fl.assignLocal(resSlot, aVal)
				fl.flushStmtTemps()
			}
		}
		if !endsWithTerm(fl.curInstrs()) {
			fl.popScopeEmitDrops()
		} else {
			fl.scopes = fl.scopes[:len(fl.scopes)-1]
		}
		armBody := fl.popInstrBuf()

		// Final arm (catch-all `default:`, exhaustive `default Variant:`, or final `when` of an exhaustive enum match):
		// Since `sema` already verified exhaustiveness, the final arm always executes if reached!
		if arm.IsDefault || armIdx == len(arms)-1 {
			return armBody
		}

		// Build short-circuiting condition across `arm.Cases`, flushing temporary containers per pattern check.
		armMatch := fl.allocSlot("_ll_marm", types.TypeBool)
		fl.pushInstrBuf()
		fl.assignLocal(armMatch, NewLitBool(false))
		if numScrut == 0 {
			for cIdx, c := range arm.Cases {
				ep := c.Patterns[0].(*ast.ExprPattern)
				fl.pushInstrBuf()
				fl.assignLocal(armMatch, EnsureExpr(fl.lowerExprRead(ep.Expr), types.TypeBool))
				fl.flushStmtTemps()
				caseInstrs := fl.popInstrBuf()
				if cIdx == 0 {
					for _, ins := range caseInstrs {
						fl.emit(ins)
					}
				} else {
					fl.emit(NewInstrIf(&ExprUnary{Op: token.KwNot, Operand: NewLocalExpr(armMatch), Typ: types.TypeBool}, caseInstrs, nil))
				}
			}
		} else {
			for cIdx, c := range arm.Cases {
				fl.pushInstrBuf()
				caseMatch := fl.allocSlot("_ll_mcase", types.TypeBool)
				fl.assignLocal(caseMatch, NewLitBool(true))
				checkedAny := false
				for i, pat := range c.Patterns {
					if i >= numScrut {
						continue
					}
					if _, isWild := pat.(*ast.WildcardPattern); isWild {
						continue
					}
					si := scrutInfos[i]
					fl.pushInstrBuf()
					var slotCond Expr
					variantTagCond := func(vp *ast.VariantPattern) Expr {
						mem := vp.Target.(*ast.MemberExpr)
						if vr := fl.info(mem).EnumVariant; vr != nil {
							return &ExprBinary{
								Op:    token.Eq,
								Left:  &ExprField{Base: EnsureExpr(si.scrutPtrExpr, &types.Ptr{Elem: si.scrutTy}), FieldName: "tag", Typ: types.TypeInt, IsPtr: true},
								Right: &ExprLitInt{Val: uint64(vr.Value), Typ: types.TypeInt},
								Typ:   types.TypeBool,
							}
						}
						return nil
					}
					switch p := pat.(type) {
					case *ast.ExprPattern:
						if _, isNone := p.Expr.(*ast.NoneLit); isNone {
							optScrut := si.scrutTy.(*types.Optional)
							slotCond = &ExprUnary{
								Op:      token.KwNot,
								Operand: optHasValueExpr(si.scrutValExpr, optScrut),
								Typ:     types.TypeBool,
							}
						} else if en, isEn := si.scrutTy.(*types.Enum); isEn && en.IsTagged && fl.info(p.Expr).EnumVariant != nil {
							vr := fl.info(p.Expr).EnumVariant
							slotCond = &ExprBinary{
								Op:    token.Eq,
								Left:  &ExprField{Base: EnsureExpr(si.scrutPtrExpr, &types.Ptr{Elem: si.scrutTy}), FieldName: "tag", Typ: types.TypeInt, IsPtr: true},
								Right: &ExprLitInt{Val: uint64(vr.Value), Typ: types.TypeInt},
								Typ:   types.TypeBool,
							}
						} else {
							pExpr := fl.lowerExprRead(p.Expr)
							if fl.info(p.Expr).WrapOptional {
								optScrut := si.scrutTy.(*types.Optional)
								pExpr = fl.wrapInOptionalExpr(pExpr, optScrut)
							}
							slotCond = fl.buildEqualityExpr(si.scrutTy, si.scrutValExpr, pExpr, false)
						}
					case *ast.ContainsPattern:
						slotCond = fl.lowerContainsPattern(p, fl.bm.ContainsPatterns[p], si.scrutValExpr)
					case *ast.MethodPattern:
						if vp := fl.bm.BareVariantPatterns[p]; vp != nil {
							slotCond = variantTagCond(vp)
						} else if mpInfo := fl.bm.MethodPatterns[p]; mpInfo != nil {
							if fl.exprOverrides == nil {
								fl.exprOverrides = make(map[ast.Expr]Expr)
							}
							fl.exprOverrides[mpInfo.RecvExpr] = si.scrutValExpr
							slotCond = fl.lowerCallExpr(mpInfo.Call, fl.info(mpInfo.Call))
							delete(fl.exprOverrides, mpInfo.RecvExpr)
						} else {
							slotCond = NewLitBool(false)
						}
					case *ast.VariantPattern:
						slotCond = variantTagCond(p)
					}
					if slotCond != nil {
						fl.assignLocal(caseMatch, EnsureExpr(slotCond, types.TypeBool))
					}
					fl.flushStmtTemps()
					slotInstrs := fl.popInstrBuf()
					if !checkedAny {
						for _, ins := range slotInstrs {
							fl.emit(ins)
						}
						checkedAny = true
					} else {
						fl.emit(NewInstrIf(NewLocalExpr(caseMatch), slotInstrs, nil))
					}
				}
				fl.assignLocal(armMatch, NewLocalExpr(caseMatch))
				caseInstrs := fl.popInstrBuf()
				if cIdx == 0 {
					for _, ins := range caseInstrs {
						fl.emit(ins)
					}
				} else {
					fl.emit(NewInstrIf(&ExprUnary{Op: token.KwNot, Operand: NewLocalExpr(armMatch), Typ: types.TypeBool}, caseInstrs, nil))
				}
			}
		}
		patSetup := fl.popInstrBuf()

		restChain := buildArmChain(armIdx + 1)
		return append(patSetup, NewInstrIf(NewLocalExpr(armMatch), armBody, restChain))
	}

	chain := buildArmChain(0)
	for _, ins := range chain {
		fl.emit(ins)
	}
	fl.popScopeEmitDrops()
	fl.emit(&InstrLabel{Name: exitLbl})

	if resSlot != nil {
		return NewLocalExpr(resSlot)
	}
	return nil
}

func (fl *funcLowerer) lowerContainsPattern(p *ast.ContainsPattern, cinfo *sema.ContainsInfo, scrutValExpr Expr) Expr {
	if cinfo == nil {
		return NewLitBool(false)
	}
	fl.tc.visit(cinfo.ContainerType)
	fl.tc.visit(cinfo.ElemType)

	needleArg := scrutValExpr
	if cinfo.WrapOptional {
		optTy := cinfo.ElemType.(*types.Optional)
		fl.tc.visit(optTy)
		needleArg = fl.wrapInOptionalExpr(scrutValExpr, optTy)
	}

	switch cinfo.Kind {
	case sema.ContainsMethod:
		contArg := fl.lowerExprRead(p.Container)
		fi := cinfo.TargetFunc
		return &ExprCall{
			Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
			Args:   []Expr{EnsureExpr(contArg, cinfo.ContainerType), EnsureExpr(needleArg, cinfo.ElemType)},
			Typ:    types.TypeBool,
		}

	case sema.ContainsInterface:
		ifc := cinfo.BoxInterface
		var ifcExpr Expr
		if fl.isFreshResourceRValue(p.Container, fl.info(p.Container)) {
			raw := fl.lowerRawExpr(p.Container, nil, false)
			ifcExpr = NewLocalExpr(fl.allocStmtTemp(ifc, raw))
		} else {
			ifcExpr = fl.lowerExprRead(p.Container)
		}
		ifcTmp := fl.allocSlot("_ll_ifc", ifc)
		fl.assignLocal(ifcTmp, ifcExpr)
		im := cinfo.InterfaceMeth
		cArgs := []Expr{
			&ExprField{Base: NewLocalExpr(ifcTmp), FieldName: "data", Typ: &types.Ptr{Elem: types.TypeVoid}},
			EnsureExpr(needleArg, cinfo.ElemType),
		}
		vtField := &ExprField{Base: NewLocalExpr(ifcTmp), FieldName: "vtable", Typ: &types.Ptr{Elem: types.TypeVoid}}
		fnField := &ExprField{Base: vtField, FieldName: "m_" + im.Name, Typ: &types.Func{Params: im.Params, Ret: im.Ret}}
		return &ExprCall{Callee: fnField, Args: cArgs, Typ: types.TypeBool}

	case sema.ContainsBuiltinVec:
		vecTy := cinfo.ContainerType.(*types.Vector)
		recvVal := fl.lowerExprRead(p.Container)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_contains__" + vecTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvVal, vecTy), EnsureExpr(needleArg, vecTy.Elem)},
			Typ:    types.TypeBool,
		}

	case sema.ContainsBuiltinArr:
		arrTy := cinfo.ContainerType.(*types.StackArray)
		recvVal := fl.lowerExprRead(p.Container)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_arr_contains__" + arrTy.Mangled()},
			Args:   []Expr{EnsureExpr(recvVal, arrTy), EnsureExpr(needleArg, arrTy.Elem)},
			Typ:    types.TypeBool,
		}

	case sema.ContainsBuiltinSet, sema.ContainsBuiltinMap:
		colTy := cinfo.ContainerType
		recvVal := fl.lowerExprRead(p.Container)
		return fl.callMethod(colTy, "has", EnsureExpr(recvVal, colTy), EnsureExpr(needleArg, cinfo.ElemType))
	}
	return NewLitBool(false)
}

func (fl *funcLowerer) emitVariantPatternBindings(
	vp *ast.VariantPattern,
	scrutTy types.Type,
	scrutPtrExpr Expr,
	scrutValExpr Expr,
	armScope *mirScope,
) {
	en, ok := scrutTy.(*types.Enum)
	if !ok {
		return
	}
	mem, ok := vp.Target.(*ast.MemberExpr)
	if !ok {
		return
	}
	vr := fl.info(mem).EnumVariant
	if vr == nil {
		return
	}
	syms := fl.bm.PatternSyms[vp]

	if len(vp.Bindings) != 1 || len(syms) != 1 || syms[0] == nil {
		return
	}
	b := vp.Bindings[0]
	sym := syms[0]
	fl.tc.visit(sym.Type)
	hasOwnRes := b.Mode == ast.PatternBindOwn && !vr.Payload.IsPlain()
	loc := &Local{
		CName:    sym.CName,
		Type:     sym.Type,
		IsMutPtr: b.Mode == ast.PatternBindMut,
	}
	if hasOwnRes && NeedsMovedFlag(sym.Type) {
		loc.MovedFlag = sym.CName + "_moved"
		loc.MovedLocal = &Local{CName: loc.MovedFlag, Type: types.TypeBool}
	}
	fl.locals = append(fl.locals, loc)
	fl.symLocals[sym] = loc

	dataPtr := &ExprField{Base: scrutPtrExpr, FieldName: "data", IsPtr: true}
	payloadPtr := &ExprField{Base: dataPtr, FieldName: vr.CName, Typ: sym.Type}
	dataVal := &ExprField{Base: scrutValExpr, FieldName: "data"}
	payloadVal := &ExprField{Base: dataVal, FieldName: vr.CName, Typ: sym.Type}

	if loc.IsMutPtr {
		fl.emit(&InstrAssign{Dst: NewLocalExpr(loc), Expr: &ExprAddrOf{Place: payloadPtr, Typ: sym.Type}})
	} else if hasOwnRes {
		fl.assignLocal(loc, payloadVal)
		fl.emit(&InstrAssign{Dst: scrutValExpr, Expr: NewLitZero(en)})
		armScope.ownedLocals = append(armScope.ownedLocals, loc)
	} else {
		fl.assignLocal(loc, payloadVal)
	}
}

// -----------------------------------------------------------------------------
// Identifiers, Operators, Member Access, Indexing, & Slicing
// -----------------------------------------------------------------------------

func (fl *funcLowerer) symbolAddr(sym *sema.Symbol) Expr {
	if capField, ok := fl.captureFields[sym]; ok {
		return &ExprAddrOf{Place: capField, Typ: &types.Ptr{Elem: sym.Type}}
	}
	if loc, ok := fl.symLocals[sym]; ok {
		if loc.IsMutPtr {
			return NewLocalExpr(loc)
		}
		return &ExprAddrOf{Place: NewLocalExpr(loc), Typ: &types.Ptr{Elem: loc.Type}}
	}
	return &ExprAddrOf{Place: &ExprGlobal{Global: &Global{Name: sym.CName, CName: sym.CName, Type: sym.Type}}, Typ: &types.Ptr{Elem: sym.Type}}
}

func (fl *funcLowerer) lowerIdentExpr(v *ast.IdentExpr, inf *sema.ExprInfo) Expr {
	// Bare unit member of an expected result (`Forbidden` for `Lookup.Forbidden`)
	if inf.EnumVariant != nil && inf.EnumType != nil {
		fl.tc.visit(inf.EnumType)
		return &ExprConstructEnum{EnumType: inf.EnumType, Variant: inf.EnumVariant}
	}
	sym := inf.Sym
	if sym == nil {
		cName := types.SanitizeCIdent(v.Name)
		return &ExprGlobal{Global: &Global{Name: v.Name, CName: cName, Type: inf.Type}}
	}
	if sym.IsConstParam && sym.ConstVal != nil {
		if sd := sema.ExportStaticData(sym.ConstVal, sym.Type); sd != nil {
			sv := lowerStaticData(sd, fl.tc, fl.registerThunk)
			return &ExprConst{Static: sv, Typ: sym.Type}
		}
	}
	// Static top-level function referenced as a first-class closure value (`let op = static_add`)!
	if sym.FuncInst != nil {
		fnTy := sym.FuncInst.Sig
		if fnTy.Extern {
			// An `extern fn` value is the C function itself.
			return &ExprConstructClosure{EntryFn: fl.registerThunk.cCallback(sym.FuncInst.CName, fnTy), FuncType: fnTy}
		}
		thunk := fl.registerThunk.static(sym.FuncInst, fnTy)
		return &ExprConstructClosure{
			EntryFn:  thunk,
			FuncType: fnTy,
		}
	}
	if capField, ok := fl.captureFields[sym]; ok {
		return capField
	}
	if loc, ok := fl.symLocals[sym]; ok {
		if loc.IsMutPtr {
			return &ExprDeref{Ptr: NewLocalExpr(loc), Typ: loc.Type}
		}
		return NewLocalExpr(loc)
	}
	return &ExprGlobal{Global: &Global{Name: sym.Name, CName: sym.CName, Type: sym.Type}}
}

func (fl *funcLowerer) lowerUnaryExpr(u *ast.UnaryExpr, inf *sema.ExprInfo) Expr {
	switch u.Op {
	case token.Minus:
		if lit, isLit := u.Operand.(*ast.IntLit); isLit {
			return NewLitInt(lit.Val, true, inf.Type)
		}
		sub := fl.lowerExprRead(u.Operand)
		return &ExprUnary{
			Op:      token.Minus,
			Operand: EnsureExpr(sub, inf.Type),
			Typ:     inf.Type,
		}

	case token.KwNot:
		sub := fl.lowerExprRead(u.Operand)
		return &ExprUnary{
			Op:      token.KwNot,
			Operand: EnsureExpr(sub, types.TypeBool),
			Typ:     types.TypeBool,
		}

	case token.Tilde:
		sub := fl.lowerExprRead(u.Operand)
		return &ExprUnary{
			Op:      token.Tilde,
			Operand: EnsureExpr(sub, inf.Type),
			Typ:     inf.Type,
		}

	case token.Star:
		ptr := fl.lowerExprRead(u.Operand)
		return &ExprDeref{
			Ptr: EnsureExpr(ptr, &types.Ptr{Elem: inf.Type}),
			Typ: inf.Type,
		}

	case token.KwOwn:
		return EnsureExpr(fl.lowerRawExpr(u.Operand, inf.Type, true), inf.Type)
	}
	return nil
}

func (fl *funcLowerer) lowerBinaryExpr(b *ast.BinaryExpr, inf *sema.ExprInfo, forOwned bool) Expr {
	// 1. Short-circuiting `and` / `or`
	if b.Op == token.KwAnd {
		res := fl.allocSlot("_ll_and", types.TypeBool)
		lhs := fl.lowerExprRead(b.Left)
		fl.assignLocal(res, lhs)
		fl.pushInstrBuf()
		rhs := fl.lowerExprRead(b.Right)
		fl.assignLocal(res, rhs)
		thenBuf := fl.popInstrBuf()
		fl.emit(NewInstrIf(NewLocalExpr(res), thenBuf, nil))
		return NewLocalExpr(res)
	}
	if b.Op == token.KwOr {
		res := fl.allocSlot("_ll_or", types.TypeBool)
		lhs := fl.lowerExprRead(b.Left)
		fl.assignLocal(res, lhs)
		fl.pushInstrBuf()
		rhs := fl.lowerExprRead(b.Right)
		fl.assignLocal(res, rhs)
		elseBuf := fl.popInstrBuf()
		fl.emit(NewInstrIf(&ExprUnary{Op: token.KwNot, Operand: NewLocalExpr(res), Typ: types.TypeBool}, elseBuf, nil))
		return NewLocalExpr(res)
	}

	// 2a. Result fallback `res! ?? fallback_expr`: the success value, else drop the failure and
	// evaluate the fallback.
	if b.Op == token.QuestQuest {
		if en := types.AsResult(fl.info(b.Left).Type); en != nil {
			return fl.lowerResultFallback(b, en, inf.Type)
		}
	}

	// 2. Short-circuiting optional fallback `opt_expr ?? fallback_expr`
	if b.Op == token.QuestQuest {
		optTy := fl.info(b.Left).Type.(*types.Optional)
		fl.tc.visit(optTy)
		resSlot := fl.allocSlot("_ll_qq", inf.Type)

		outerTemps := fl.stmtTemps
		fl.stmtTemps = nil
		leftVal := fl.lowerExprOwned(b.Left, optTy)
		optSlot := fl.allocSlot("_ll_qq_opt", optTy)
		fl.assignLocal(optSlot, leftVal)
		fl.flushStmtTemps()

		// Then: left has value
		fl.pushInstrBuf()
		if resOpt, isResOpt := inf.Type.(*types.Optional); isResOpt && resOpt.Equals(optTy) {
			fl.assignLocal(resSlot, NewLocalExpr(optSlot))
		} else {
			fl.extractOwnedOptionalInto(optSlot, optTy, resSlot)
		}
		thenBuf := fl.popInstrBuf()

		// Else: evaluate fallback ONLY when left is `none`!
		fl.pushInstrBuf()
		rTy := fl.info(b.Right).Type
		if rTy == types.TypeNever {
			if len(outerTemps) > 0 {
				fl.stmtTemps = append([]*Local(nil), outerTemps...)
			}
			fl.lowerVoidExpr(b.Right)
			fl.flushStmtTemps()
		} else {
			rVal := fl.lowerExprOwned(b.Right, inf.Type)
			fl.assignLocal(resSlot, rVal)
			fl.flushStmtTemps()
		}
		elseBuf := fl.popInstrBuf()
		fl.stmtTemps = outerTemps

		fl.emit(NewInstrIf(optHasValueExpr(NewLocalExpr(optSlot), optTy), thenBuf, elseBuf))
		_ = forOwned
		return NewLocalExpr(resSlot)
	}

	// 3. Equality `==` / `!=`
	if b.Op == token.Eq || b.Op == token.Neq {
		isNeq := b.Op == token.Neq
		if _, rNone := b.Right.(*ast.NoneLit); rNone {
			optL := fl.info(b.Left).Type.(*types.Optional)
			lhs := fl.lowerExprRead(b.Left)
			hasV := optHasValueExpr(lhs, optL)
			if isNeq {
				return hasV
			}
			return &ExprUnary{Op: token.KwNot, Operand: hasV, Typ: types.TypeBool}
		}
		if _, lNone := b.Left.(*ast.NoneLit); lNone {
			optR := fl.info(b.Right).Type.(*types.Optional)
			rhs := fl.lowerExprRead(b.Right)
			hasV := optHasValueExpr(rhs, optR)
			if isNeq {
				return hasV
			}
			return &ExprUnary{Op: token.KwNot, Operand: hasV, Typ: types.TypeBool}
		}
		lhs := fl.lowerExprRead(b.Left)
		rhs := fl.lowerExprRead(b.Right)
		cmpTy := fl.info(b.Left).Type
		if fl.info(b.Left).WrapOptional {
			cmpTy = fl.info(b.Right).Type
		}
		return fl.buildEqualityExpr(cmpTy, lhs, rhs, isNeq)
	}

	// 4. Ordering `<`, `<=`, `>`, `>=`
	if b.Op == token.Lt || b.Op == token.Lte || b.Op == token.Gt || b.Op == token.Gte {
		lhs := fl.lowerExprRead(b.Left)
		rhs := fl.lowerExprRead(b.Right)
		lty := fl.info(b.Left).Type
		return fl.buildOrderingExpr(b.Op, lty, lhs, rhs)
	}

	// 5. Shifts `<<` / `>>`
	file, line := formatSpanLoc(b)
	if b.Op == token.Shl || b.Op == token.Shr {
		lhs := fl.lowerExprRead(b.Left)
		rhs := fl.lowerExprRead(b.Right)
		return &ExprBinary{
			Op:    b.Op,
			Left:  EnsureExpr(lhs, inf.Type),
			Right: EnsureExpr(rhs, types.TypeInt),
			Typ:   inf.Type,
			File:  file,
			Line:  line,
		}
	}

	// 6. `+` on a type that implements `Addable` (strings included): `a.add(b)`
	if b.Op == token.Plus && inf.OpMethod != nil {
		fi := inf.OpMethod
		lhs := fl.lowerExprRead(b.Left)
		rhs := fl.lowerExprRead(b.Right)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
			Args:   []Expr{EnsureExpr(lhs, inf.Type), EnsureExpr(rhs, inf.Type)},
			Typ:    inf.Type,
		}
	}

	// 7. Numeric Arithmetic & Bitwise (`+`, `-`, `*`, `/`, `%`, `&`, `|`, `^`)
	lhs := fl.lowerExprRead(b.Left)
	rhs := fl.lowerExprRead(b.Right)
	return &ExprBinary{
		Op:    b.Op,
		Left:  EnsureExpr(lhs, inf.Type),
		Right: EnsureExpr(rhs, inf.Type),
		Typ:   inf.Type,
		File:  file,
		Line:  line,
	}
}

func (fl *funcLowerer) buildOrderingExpr(op token.Kind, lty types.Type, lhs, rhs any) Expr {
	fl.tc.visit(lty)
	lhsExpr := EnsureExpr(lhs, lty)
	rhsExpr := EnsureExpr(rhs, lty)

	if p, isPrim := lty.(*types.Primitive); isPrim && p.Kind != types.PrimString {
		return &ExprBinary{
			Op:    op,
			Left:  lhsExpr,
			Right: rhsExpr,
			Typ:   types.TypeBool,
		}
	}
	if en, isEn := lty.(*types.Enum); isEn && !en.IsTagged {
		return &ExprBinary{
			Op:    op,
			Left:  lhsExpr,
			Right: rhsExpr,
			Typ:   types.TypeBool,
		}
	}
	if custom := fl.prog.LookupConcreteMethod(lty, "cmp"); custom != nil {
		call := &ExprCall{
			Callee: &ExprFuncRef{Name: custom.CName, TargetFunc: custom, FuncType: custom.Sig},
			Args:   []Expr{lhsExpr, rhsExpr},
			Typ:    types.TypeInt,
		}
		return &ExprBinary{
			Op:    op,
			Left:  call,
			Right: &ExprLitInt{Val: 0, Typ: types.TypeInt},
			Typ:   types.TypeBool,
		}
	}
	if d, isDist := lty.(*types.Distinct); isDist {
		castL := &ExprCast{Val: lhsExpr, From: lty, To: d.Base}
		castR := &ExprCast{Val: rhsExpr, From: lty, To: d.Base}
		return fl.buildOrderingExpr(op, d.Base, castL, castR)
	}
	cmpExpr := &ExprCmp{Left: lhsExpr, Right: rhsExpr, ValType: lty}
	return &ExprBinary{
		Op:    op,
		Left:  cmpExpr,
		Right: &ExprLitInt{Val: 0, Typ: types.TypeInt},
		Typ:   types.TypeBool,
	}
}

func (fl *funcLowerer) buildEqualityExpr(ty types.Type, lhs, rhs any, isNeq bool) Expr {
	fl.tc.visit(ty)
	op := token.Eq
	if isNeq {
		op = token.Neq
	}
	lhsExpr := EnsureExpr(lhs, ty)
	rhsExpr := EnsureExpr(rhs, ty)

	if p, isPrim := ty.(*types.Primitive); isPrim && p.Kind != types.PrimString {
		return &ExprBinary{
			Op:    op,
			Left:  lhsExpr,
			Right: rhsExpr,
			Typ:   types.TypeBool,
		}
	}

	if custom := fl.prog.LookupConcreteMethod(ty, "eq"); custom != nil {
		call := &ExprCall{
			Callee: &ExprFuncRef{Name: custom.CName, TargetFunc: custom, FuncType: custom.Sig},
			Args:   []Expr{lhsExpr, rhsExpr},
			Typ:    types.TypeBool,
		}
		if isNeq {
			return &ExprUnary{Op: token.KwNot, Operand: call, Typ: types.TypeBool}
		}
		return call
	}
	if customCmp := fl.prog.LookupConcreteMethod(ty, "cmp"); customCmp != nil {
		call := &ExprCall{
			Callee: &ExprFuncRef{Name: customCmp.CName, TargetFunc: customCmp, FuncType: customCmp.Sig},
			Args:   []Expr{lhsExpr, rhsExpr},
			Typ:    types.TypeInt,
		}
		cmpOp := token.Eq
		if isNeq {
			cmpOp = token.Neq
		}
		return &ExprBinary{
			Op:    cmpOp,
			Left:  call,
			Right: &ExprLitInt{Val: 0, Typ: types.TypeInt},
			Typ:   types.TypeBool,
		}
	}

	switch v := ty.(type) {
	case *types.Primitive:
		return &ExprBinary{
			Op:    op,
			Left:  lhsExpr,
			Right: rhsExpr,
			Typ:   types.TypeBool,
		}
	case *types.Enum:
		if v.IsTagged {
			eqExpr := &ExprEq{Left: lhsExpr, Right: rhsExpr, ValType: ty}
			if isNeq {
				return &ExprUnary{Op: token.KwNot, Operand: eqExpr, Typ: types.TypeBool}
			}
			return eqExpr
		}
		return &ExprBinary{
			Op:    op,
			Left:  lhsExpr,
			Right: rhsExpr,
			Typ:   types.TypeBool,
		}
	case *types.Ptr, *types.Ref:
		return &ExprBinary{
			Op:    op,
			Left:  lhsExpr,
			Right: rhsExpr,
			Typ:   types.TypeBool,
		}
	case *types.Distinct:
		castL := &ExprCast{Val: lhsExpr, From: ty, To: v.Base}
		castR := &ExprCast{Val: rhsExpr, From: ty, To: v.Base}
		return fl.buildEqualityExpr(v.Base, castL, castR, isNeq)
	case *types.Struct, *types.Optional, *types.Tuple:
		eqExpr := &ExprEq{Left: lhsExpr, Right: rhsExpr, ValType: ty}
		if isNeq {
			return &ExprUnary{Op: token.KwNot, Operand: eqExpr, Typ: types.TypeBool}
		}
		return eqExpr
	default:
		return &ExprBinary{
			Op:    op,
			Left:  lhsExpr,
			Right: rhsExpr,
			Typ:   types.TypeBool,
		}
	}
}

func (fl *funcLowerer) lowerMemberExpr(m *ast.MemberExpr, inf *sema.ExprInfo, forOwned bool) Expr {
	// 1. Enum Variant (`Status.Pending`, `Result[int, string].Empty`, or `mod.Status.Pending`)
	if inf.EnumVariant != nil && inf.EnumType != nil {
		fl.tc.visit(inf.EnumType)
		return &ExprConstructEnum{
			EnumType: inf.EnumType,
			Variant:  inf.EnumVariant,
		}
	}
	// 2. Module-qualified top-level constant or static function (`mod.PI` or `mod.fn`)
	if inf.Sym != nil {
		if inf.Sym.FuncInst != nil {
			fnTy := inf.Sym.FuncInst.Sig
			if fnTy.Extern {
				return &ExprConstructClosure{EntryFn: fl.registerThunk.cCallback(inf.Sym.FuncInst.CName, fnTy), FuncType: fnTy}
			}
			thunk := fl.registerThunk.static(inf.Sym.FuncInst, fnTy)
			return &ExprConstructClosure{
				EntryFn:  thunk,
				FuncType: fnTy,
			}
		}
		return &ExprGlobal{Global: &Global{Name: inf.Sym.CName, CName: inf.Sym.CName, Type: inf.Sym.Type}}
	}

	// 3. Optional chaining (`u?.address?.city` or `find(id)?.name`)
	if inf.IsOptChain {
		optBase := fl.info(m.Base).Type.(*types.Optional)
		resOpt := inf.Type.(*types.Optional)
		fl.tc.visit(optBase)
		fl.tc.visit(resOpt)

		resSlot := fl.allocSlot("_ll_optmem", resOpt)
		fl.assignLocal(resSlot, optNoneExpr(resOpt))

		baseVal := fl.lowerExprRead(m.Base)
		baseTmp := fl.allocSlot("_ll_optbase", optBase)
		fl.assignLocal(baseTmp, baseVal)

		innerTy := optBase.Elem
		var structAccess Expr
		if optBase.IsBoxed() {
			st := innerTy.(*types.Struct)
			sf, _ := st.FieldByName(m.Member)
			structAccess = &ExprField{Base: NewLocalExpr(baseTmp), FieldName: sf.CName, Typ: sf.Type, IsPtr: true}
			innerTy = sf.Type
		} else if rf, isRef := innerTy.(*types.Ref); isRef {
			rfElem := rf.Elem
			if d, isDist := rfElem.(*types.Distinct); isDist {
				if dtTup, isTup := d.Base.(*types.Tuple); isTup {
					rfElem = dtTup
				}
			}
			if tup, isTup := rfElem.(*types.Tuple); isTup {
				fIdx := inf.TupleFieldIdx
				fName := fmt.Sprintf("_%d", fIdx)
				valField := &ExprField{Base: NewLocalExpr(baseTmp), FieldName: "value", Typ: rf}
				structAccess = &ExprField{Base: valField, FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true}
				innerTy = tup.Elems[fIdx]
			} else {
				st := rf.Elem.(*types.Struct)
				sf, _ := st.FieldByName(m.Member)
				valField := &ExprField{Base: NewLocalExpr(baseTmp), FieldName: "value", Typ: rf}
				structAccess = &ExprField{Base: valField, FieldName: sf.CName, Typ: sf.Type, IsPtr: true}
				innerTy = sf.Type
			}
		} else {
			rawInner := innerTy
			effectiveInner := innerTy
			if d, isDist := innerTy.(*types.Distinct); isDist {
				if dtTup, isTup := d.Base.(*types.Tuple); isTup {
					effectiveInner = dtTup
				}
			}
			if tup, isTup := effectiveInner.(*types.Tuple); isTup {
				fIdx := inf.TupleFieldIdx
				fName := fmt.Sprintf("_%d", fIdx)
				valField := &ExprField{Base: NewLocalExpr(baseTmp), FieldName: "value", Typ: rawInner}
				structAccess = &ExprField{Base: valField, FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: false}
				innerTy = tup.Elems[fIdx]
			} else {
				st := innerTy.(*types.Struct)
				sf, _ := st.FieldByName(m.Member)
				valField := &ExprField{Base: NewLocalExpr(baseTmp), FieldName: "value", Typ: st}
				structAccess = &ExprField{Base: valField, FieldName: sf.CName, Typ: sf.Type, IsPtr: false}
				innerTy = sf.Type
			}
		}

		// Clone Resource field when extracted into an independent `?T` result!
		var fieldVal Expr = structAccess
		if !innerTy.IsPlain() {
			fieldVal = fl.copyCallExpr(innerTy, structAccess)
		}
		var wrapped Expr
		if _, alreadyOpt := innerTy.(*types.Optional); alreadyOpt {
			wrapped = fieldVal
		} else {
			wrapped = fl.wrapInOptionalExpr(fieldVal, resOpt)
		}

		fl.emit(&InstrIf{
			Cond: optHasValueExpr(NewLocalExpr(baseTmp), optBase),
			Then: []Instr{&InstrAssign{Dst: NewLocalExpr(resSlot), Expr: EnsureExpr(wrapped, resOpt)}},
		})
		return NewLocalExpr(resSlot)
	}

	// 4. Direct struct / `ref[Struct]` field access
	baseTy := fl.info(m.Base).Type
	if rf, isRef := baseTy.(*types.Ref); isRef {
		rfElem := rf.Elem
		if d, isDist := rfElem.(*types.Distinct); isDist {
			if dtTup, isTup := d.Base.(*types.Tuple); isTup {
				rfElem = dtTup
			}
		}
		if tup, isTup := rfElem.(*types.Tuple); isTup {
			fIdx := inf.TupleFieldIdx
			fName := fmt.Sprintf("_%d", fIdx)
			ptr := fl.lowerExprRead(m.Base)
			return &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true}
		}
		st := rf.Elem.(*types.Struct)
		sf, _ := st.FieldByName(m.Member)
		ptr := fl.lowerExprRead(m.Base)
		return &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: true}
	}
	if p, isPtr := baseTy.(*types.Ptr); isPtr {
		pElem := p.Elem
		if d, isDist := pElem.(*types.Distinct); isDist {
			if dtTup, isTup := d.Base.(*types.Tuple); isTup {
				pElem = dtTup
			}
		}
		if tup, isTup := pElem.(*types.Tuple); isTup {
			fIdx := inf.TupleFieldIdx
			fName := fmt.Sprintf("_%d", fIdx)
			ptr := fl.lowerExprRead(m.Base)
			return &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true}
		}
		if st, ok := p.Elem.(*types.Struct); ok {
			sf, _ := st.FieldByName(m.Member)
			ptr := fl.lowerExprRead(m.Base)
			return &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: true}
		}
	}
	if _, isVec := baseTy.(*types.Vector); isVec {
		baseExpr := fl.lowerExprRead(m.Base)
		return &ExprField{Base: EnsureExpr(baseExpr, baseTy), FieldName: m.Member, Typ: inf.Type, IsPtr: false}
	}
	if arr, isArr := baseTy.(*types.StackArray); isArr {
		if m.Member == "len" {
			return NewLitInt(uint64(arr.Size), false, types.TypeInt)
		}
		baseExpr := fl.lowerExprRead(m.Base)
		return &ExprField{Base: EnsureExpr(baseExpr, baseTy), FieldName: m.Member, Typ: inf.Type, IsPtr: false}
	}
	rawBaseTy := baseTy
	effectiveBaseTy := baseTy
	if d, isDist := baseTy.(*types.Distinct); isDist {
		if dtTup, isTup := d.Base.(*types.Tuple); isTup {
			effectiveBaseTy = dtTup
		}
	}
	if tup, isTup := effectiveBaseTy.(*types.Tuple); isTup {
		fIdx := inf.TupleFieldIdx
		fName := fmt.Sprintf("_%d", fIdx)
		baseExpr := fl.lowerExprRead(m.Base)
		return &ExprField{Base: EnsureExpr(baseExpr, rawBaseTy), FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: false}
	}

	st := baseTy.(*types.Struct)
	sf, _ := st.FieldByName(m.Member)
	baseExpr := fl.lowerExprRead(m.Base)
	_ = forOwned
	return &ExprField{Base: EnsureExpr(baseExpr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: false}
}

func (fl *funcLowerer) lowerIndexExpr(idx *ast.IndexExpr, inf *sema.ExprInfo, _ bool) Expr {
	if inf.TargetFunc != nil {
		var baseVal Expr
		if inf.TargetFunc.Sig.Params[0].Mode == types.ModeMut {
			baseVal = fl.lowerLValueAddr(idx.Base)
		} else {
			baseVal = fl.lowerExprRead(idx.Base)
		}
		idxVal := fl.lowerExprRead(idx.Index)
		return &ExprCall{
			Callee: &ExprFuncRef{Name: inf.TargetFunc.CName},
			Args:   []Expr{baseVal, EnsureExpr(idxVal, inf.TargetFunc.Sig.Params[1].Type)},
			Typ:    inf.TargetFunc.Sig.Ret,
		}
	}

	rawBaseTy := fl.info(idx.Base).Type
	baseTy := rawBaseTy
	isArenaRef := false
	if rf, ok := rawBaseTy.(*types.Ref); ok {
		baseTy = rf.Elem
		isArenaRef = true
	}

	file, line := formatSpanLoc(idx)
	idxVal := fl.lowerExprRead(idx.Index)

	switch v := types.CollView(baseTy).(type) {
	case *types.Tuple:
		fIdx := inf.TupleFieldIdx
		fName := fmt.Sprintf("_%d", fIdx)
		baseExpr := fl.lowerExprRead(idx.Base)
		return &ExprField{Base: EnsureExpr(baseExpr, baseTy), FieldName: fName, FieldIdx: fIdx, Typ: v.Elems[fIdx], IsPtr: false}

	case *types.Vector:
		if isArenaRef {
			ptr := fl.lowerExprRead(idx.Base)
			pTmp := fl.allocSlot("_ll_vp", rawBaseTy)
			fl.assignLocal(pTmp, ptr)
			iTmp := fl.allocSlot("_ll_vi", types.TypeInt)
			lenExpr := &ExprField{Base: NewLocalExpr(pTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: true}
			fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
			dataPtr := &ExprField{Base: NewLocalExpr(pTmp), FieldName: "data", Typ: &types.Ptr{Elem: v.Elem}, IsPtr: true}
			return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: v.Elem}
		}
		if fl.isAddressable(idx.Base) {
			bPtr := fl.lowerLValueAddr(idx.Base)
			pTmp := fl.allocSlot("_ll_vp", &types.Ptr{Elem: v})
			fl.assignLocal(pTmp, bPtr)
			iTmp := fl.allocSlot("_ll_vi", types.TypeInt)
			lenExpr := &ExprField{Base: NewLocalExpr(pTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: true}
			fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
			dataPtr := &ExprField{Base: NewLocalExpr(pTmp), FieldName: "data", Typ: &types.Ptr{Elem: v.Elem}, IsPtr: true}
			return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: v.Elem}
		}
		bVal := fl.lowerExprRead(idx.Base)
		bTmp := fl.allocSlot("_ll_vv", v)
		fl.assignLocal(bTmp, bVal)
		iTmp := fl.allocSlot("_ll_vi", types.TypeInt)
		lenExpr := &ExprField{Base: NewLocalExpr(bTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: false}
		fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
		dataPtr := &ExprField{Base: NewLocalExpr(bTmp), FieldName: "data", Typ: &types.Ptr{Elem: v.Elem}, IsPtr: false}
		return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: v.Elem}

	case *types.StackArray:
		iTmp := fl.allocSlot("_ll_ai", types.TypeInt)
		lenExpr := NewLitInt(uint64(v.Size), false, types.TypeInt)
		fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
		if isArenaRef {
			ptr := fl.lowerExprRead(idx.Base)
			dataPtr := &ExprField{Base: EnsureExpr(ptr, rawBaseTy), FieldName: "data", Typ: &types.Ptr{Elem: v.Elem}, IsPtr: true}
			return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: v.Elem}
		}
		if fl.isAddressable(idx.Base) {
			bPtr := fl.lowerLValueAddr(idx.Base)
			dataPtr := &ExprField{Base: EnsureExpr(bPtr, &types.Ptr{Elem: v}), FieldName: "data", Typ: &types.Ptr{Elem: v.Elem}, IsPtr: true}
			return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: v.Elem}
		}
		bVal := fl.lowerExprRead(idx.Base)
		dataPtr := &ExprField{Base: EnsureExpr(bVal, v), FieldName: "data", Typ: &types.Ptr{Elem: v.Elem}, IsPtr: false}
		return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: v.Elem}

	case *types.Primitive: // `string` O(1) byte index `s[i] -> u8`
		bVal := fl.lowerExprRead(idx.Base)
		sTmp := fl.allocSlot("_ll_sv", types.TypeString)
		fl.assignLocal(sTmp, bVal)
		iTmp := fl.allocSlot("_ll_si", types.TypeInt)
		lenExpr := &ExprField{Base: NewLocalExpr(sTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: false}
		fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
		dataPtr := &ExprField{Base: NewLocalExpr(sTmp), FieldName: "data", Typ: &types.Ptr{Elem: types.TypeU8}, IsPtr: false}
		return &ExprCast{
			Val:  &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: types.TypeU8},
			From: types.TypeU8,
			To:   types.TypeU8,
		}

	case *types.Map:
		fl.tc.visit(baseTy)
		fl.tc.visit(inf.Type)
		var recv Expr
		if isArenaRef {
			recv = &ExprDeref{Ptr: EnsureExpr(fl.lowerExprRead(idx.Base), rawBaseTy), Typ: baseTy}
		} else {
			recv = EnsureExpr(fl.lowerExprRead(idx.Base), baseTy)
		}
		return fl.callMethod(baseTy, "get", recv, EnsureExpr(idxVal, v.Key))

	case *types.Ptr:
		ptr := fl.lowerExprRead(idx.Base)
		return &ExprIndex{Base: EnsureExpr(ptr, v), Index: EnsureExpr(idxVal, types.TypeInt), Typ: v.Elem}

	case *types.Struct:
		if (v.Name == "List" || strings.HasPrefix(v.Name, "List[")) && len(v.TypeArgs) == 1 {
			elemTy := v.TypeArgs[0]
			if isArenaRef {
				ptr := fl.lowerExprRead(idx.Base)
				pTmp := fl.allocSlot("_ll_vp", rawBaseTy)
				fl.assignLocal(pTmp, ptr)
				iTmp := fl.allocSlot("_ll_vi", types.TypeInt)
				lenExpr := &ExprField{Base: NewLocalExpr(pTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: true}
				fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
				dataPtr := &ExprField{Base: NewLocalExpr(pTmp), FieldName: "data", Typ: &types.Ptr{Elem: elemTy}, IsPtr: true}
				return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: elemTy}
			}
			if fl.isAddressable(idx.Base) {
				bPtr := fl.lowerLValueAddr(idx.Base)
				pTmp := fl.allocSlot("_ll_vp", &types.Ptr{Elem: v})
				fl.assignLocal(pTmp, bPtr)
				iTmp := fl.allocSlot("_ll_vi", types.TypeInt)
				lenExpr := &ExprField{Base: NewLocalExpr(pTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: true}
				fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
				dataPtr := &ExprField{Base: NewLocalExpr(pTmp), FieldName: "data", Typ: &types.Ptr{Elem: elemTy}, IsPtr: true}
				return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: elemTy}
			}
			bVal := fl.lowerExprRead(idx.Base)
			bTmp := fl.allocSlot("_ll_vv", v)
			fl.assignLocal(bTmp, bVal)
			iTmp := fl.allocSlot("_ll_vi", types.TypeInt)
			lenExpr := &ExprField{Base: NewLocalExpr(bTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: false}
			fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
			dataPtr := &ExprField{Base: NewLocalExpr(bTmp), FieldName: "data", Typ: &types.Ptr{Elem: elemTy}, IsPtr: false}
			return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: elemTy}
		}
		if (v.Name == "Array" || strings.HasPrefix(v.Name, "Array[")) && len(v.TypeArgs) >= 1 {
			elemTy := v.TypeArgs[len(v.TypeArgs)-1]
			bVal := fl.lowerExprRead(idx.Base)
			bTmp := fl.allocSlot("_ll_av", v)
			fl.assignLocal(bTmp, bVal)
			iTmp := fl.allocSlot("_ll_ai", types.TypeInt)
			lenExpr := &ExprField{Base: NewLocalExpr(bTmp), FieldName: "len", Typ: types.TypeInt, IsPtr: false}
			fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, lenExpr, file, line))
			dataPtr := &ExprField{Base: NewLocalExpr(bTmp), FieldName: "data", Typ: &types.Ptr{Elem: elemTy}, IsPtr: false}
			return &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: elemTy}
		}

	case *types.Interface:
		recvVal := fl.lowerExprRead(idx.Base)
		recvTmp := fl.allocSlot("_ll_ifc_idx", v)
		fl.assignLocal(recvTmp, recvVal)
		fl.tc.visit(v)
		fl.tc.visit(inf.Type)
		var m *types.InterfaceMethod
		for i := range v.Methods {
			if v.Methods[i].Name == "get" {
				m = &v.Methods[i]
				break
			}
		}
		if m != nil {
			cArgs := []Expr{
				&ExprField{Base: NewLocalExpr(recvTmp), FieldName: "data", Typ: &types.Ptr{Elem: types.TypeVoid}},
				EnsureExpr(idxVal, m.Params[0].Type),
			}
			vtField := &ExprField{Base: NewLocalExpr(recvTmp), FieldName: "vtable", Typ: &types.Ptr{Elem: types.TypeVoid}}
			fnField := &ExprField{Base: vtField, FieldName: "m_get", Typ: &types.Func{Params: m.Params, Ret: m.Ret}}
			return &ExprCall{Callee: fnField, Args: cArgs, Typ: m.Ret}
		}
	}
	return nil
}

func (fl *funcLowerer) lowerSliceExpr(sl *ast.SliceExpr, inf *sema.ExprInfo) Expr {
	baseTy := fl.info(sl.Base).Type
	file, line := formatSpanLoc(sl)
	check := 1

	var loVal Expr
	if sl.Low != nil {
		loVal = fl.lowerExprRead(sl.Low)
	} else {
		loVal = NewLitInt(0, false, types.TypeInt)
	}

	switch v := baseTy.(type) {
	case *types.Primitive: // `string`
		sVal := fl.lowerExprRead(sl.Base)
		var baseRef Expr
		if fl.isAddressable(sl.Base) {
			baseRef = sVal
		} else {
			slot := fl.allocSlot("_ll_slstr", types.TypeString)
			fl.assignLocal(slot, sVal)
			baseRef = NewLocalExpr(slot)
		}
		var hiVal Expr
		if sl.High != nil {
			hiVal = fl.lowerExprRead(sl.High)
			if sl.Inclusive {
				hiVal = &ExprBinary{Op: token.Plus, Left: EnsureExpr(hiVal, types.TypeInt), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
			}
		} else {
			hiVal = &ExprField{Base: EnsureExpr(baseRef, types.TypeString), FieldName: "len", Typ: types.TypeInt}
		}
		method := "_slice"
		if inf.BorrowAsView {
			method = "_slice_view"
		}
		return fl.callMethodAt(sl, types.TypeString, method,
			EnsureExpr(baseRef, types.TypeString),
			EnsureExpr(loVal, types.TypeInt),
			EnsureExpr(hiVal, types.TypeInt),
		)

	case *types.Vector:
		fl.tc.visit(v)
		vVal := fl.lowerExprRead(sl.Base)
		var baseRef Expr
		if fl.isAddressable(sl.Base) {
			baseRef = vVal
		} else {
			slot := fl.allocSlot("_ll_slvec", v)
			fl.assignLocal(slot, vVal)
			baseRef = NewLocalExpr(slot)
		}
		var hiVal Expr
		if sl.High != nil {
			hiVal = fl.lowerExprRead(sl.High)
			if sl.Inclusive {
				hiVal = &ExprBinary{Op: token.Plus, Left: EnsureExpr(hiVal, types.TypeInt), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
			}
		} else {
			hiVal = &ExprField{Base: EnsureExpr(baseRef, v), FieldName: "len", Typ: types.TypeInt}
		}
		fnName := "ll_vec_slice_owned__" + v.Mangled()
		if inf.BorrowAsView {
			fnName = "ll_vec_slice_view__" + v.Mangled()
		}
		return &ExprCall{
			Callee: &ExprFuncRef{Name: fnName},
			Args: []Expr{
				EnsureExpr(baseRef, v),
				EnsureExpr(loVal, types.TypeInt),
				EnsureExpr(hiVal, types.TypeInt),
				NewLitInt(uint64(check), false, types.TypeInt),
				&ExprLitCString{Val: file},
				NewLitInt(uint64(line), false, types.TypeInt),
			},
			Typ: inf.Type,
		}

	case *types.StackArray:
		vecTy := inf.Type.(*types.Vector)
		fl.tc.visit(vecTy)
		var arrExpr Expr
		if fl.isAddressable(sl.Base) {
			arrAddr := fl.lowerLValueAddr(sl.Base)
			arrExpr = EnsureExpr(arrAddr, &types.Ptr{Elem: v})
		} else {
			tmp := fl.allocSlot("_ll_slarr", v)
			fl.assignLocal(tmp, fl.lowerExprRead(sl.Base))
			arrExpr = &ExprAddrOf{Place: NewLocalExpr(tmp), Typ: &types.Ptr{Elem: v}}
		}
		var hiVal Expr
		if sl.High != nil {
			hiVal = fl.lowerExprRead(sl.High)
			if sl.Inclusive {
				hiVal = &ExprBinary{Op: token.Plus, Left: EnsureExpr(hiVal, types.TypeInt), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
			}
		} else {
			hiVal = NewLitInt(uint64(v.Size), false, types.TypeInt)
		}
		dataField := &ExprField{Base: arrExpr, FieldName: "data", Typ: &types.Ptr{Elem: v.Elem}}
		viewExpr := &ExprSliceView{VecType: vecTy, DataPtr: dataField, Len: NewLitInt(uint64(v.Size), false, types.TypeInt)}
		fnName := "ll_vec_slice_owned__" + vecTy.Mangled()
		if inf.BorrowAsView {
			fnName = "ll_vec_slice_view__" + vecTy.Mangled()
		}
		return &ExprCall{
			Callee: &ExprFuncRef{Name: fnName},
			Args: []Expr{
				viewExpr,
				EnsureExpr(loVal, types.TypeInt),
				EnsureExpr(hiVal, types.TypeInt),
				NewLitInt(uint64(check), false, types.TypeInt),
				&ExprLitCString{Val: file},
				NewLitInt(uint64(line), false, types.TypeInt),
			},
			Typ: inf.Type,
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// String Interpolation, Collection/Struct Literals, & Closures
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerInterpString(is *ast.InterpStringExpr) Expr {
	out := fl.allocSlot("_ll_interp", types.TypeString)
	fl.assignLocal(out, &ExprLitString{Val: ""})

	for i, seg := range is.Segments {
		if len(seg) > 0 {
			fl.emit(fl.strPush(&ExprAddrOf{Place: NewLocalExpr(out), Typ: &types.Ptr{Elem: types.TypeString}}, &ExprLitString{Val: seg}))
		}
		if i < len(is.Exprs) {
			sub := is.Exprs[i]
			subTy := fl.info(sub).Type
			subExpr := fl.lowerExprRead(sub)
			if subTy.Equals(types.TypeString) {
				fl.emit(fl.strPush(&ExprAddrOf{Place: NewLocalExpr(out), Typ: &types.Ptr{Elem: types.TypeString}}, subExpr))
			} else {
				strPart := fl.toStringCallExpr(subTy, subExpr)
				partTmp := fl.allocStmtTemp(types.TypeString, strPart)
				fl.emit(fl.strPush(&ExprAddrOf{Place: NewLocalExpr(out), Typ: &types.Ptr{Elem: types.TypeString}}, NewLocalExpr(partTmp)))
			}
		}
	}
	return NewLocalExpr(out)
}

func (fl *funcLowerer) toStringCallExpr(ty types.Type, valExpr Expr) Expr {
	fl.tc.visit(ty)
	// Check if user defined a custom `to_string` method on `ty`
	if custom := fl.prog.LookupConcreteMethod(ty, "to_string"); custom != nil {
		return &ExprCall{
			Callee: &ExprFuncRef{Name: custom.CName, TargetFunc: custom, FuncType: custom.Sig},
			Args:   []Expr{EnsureExpr(valExpr, ty)},
			Typ:    types.TypeString,
		}
	}
	return &ExprToString{Val: EnsureExpr(valExpr, ty), ValType: ty}
}

func (fl *funcLowerer) copyCallExpr(ty types.Type, valExpr Expr) Expr {
	fl.tc.visit(ty)
	if custom := fl.prog.LookupConcreteMethod(ty, "copy"); custom != nil {
		return &ExprCall{
			Callee: &ExprFuncRef{Name: custom.CName, TargetFunc: custom, FuncType: custom.Sig},
			Args:   []Expr{EnsureExpr(valExpr, ty)},
			Typ:    ty,
		}
	}
	if ty.IsPlain() {
		return EnsureExpr(valExpr, ty)
	}
	return &ExprCopy{Val: EnsureExpr(valExpr, ty), ValType: ty}
}

func (fl *funcLowerer) lowerVecOrArrayLit(v *ast.VecLit, inf *sema.ExprInfo) Expr {
	fl.tc.visit(inf.Type)
	// StackArray `[N: T]` literal: `let a: [3: int] = [1, 2, 3]`
	if arrTy, isArr := inf.Type.(*types.StackArray); isArr {
		slot := fl.allocSlot("_ll_arr", arrTy)
		for i, el := range v.Elems {
			elVal := fl.lowerExprOwned(el, arrTy.Elem)
			fl.emit(&InstrAssign{
				Dst: &ExprIndex{
					Base:  &ExprField{Base: NewLocalExpr(slot), FieldName: "data", Typ: &types.Ptr{Elem: arrTy.Elem}},
					Index: NewLitInt(uint64(i), false, types.TypeInt),
					Typ:   arrTy.Elem,
				},
				Expr: EnsureExpr(elVal, arrTy.Elem),
			})
		}
		if slot.MovedLocal != nil {
			fl.emit(&InstrAssign{Dst: NewLocalExpr(slot.MovedLocal), Expr: NewLitBool(false)})
		}
		return NewLocalExpr(slot)
	}

	// Dynamic Vector `[T]` literal
	vecTy := inf.Type.(*types.Vector)
	if len(v.Elems) == 0 {
		return &ExprLitZero{Typ: vecTy}
	}
	slot := fl.allocSlot("_ll_vec", vecTy)
	// Locals are zeroed once per call, so a literal evaluated again in a loop must start from an
	// empty list rather than the one it built (and gave away) last time.
	fl.assignLocal(slot, &ExprLitZero{Typ: vecTy})
	fl.emit(&InstrExpr{Expr: &ExprCall{
		Callee: &ExprFuncRef{Name: "ll_vec_reserve__" + vecTy.Mangled()},
		Args:   []Expr{&ExprAddrOf{Place: NewLocalExpr(slot), Typ: &types.Ptr{Elem: vecTy}}, NewLitInt(uint64(len(v.Elems)), false, types.TypeInt)},
		Typ:    types.TypeVoid,
	}})
	for _, el := range v.Elems {
		elVal := fl.lowerExprOwned(el, vecTy.Elem)
		fl.emit(&InstrExpr{Expr: &ExprVecPush{
			VecPtr:  &ExprAddrOf{Place: NewLocalExpr(slot), Typ: &types.Ptr{Elem: vecTy}},
			Item:    EnsureExpr(elVal, vecTy.Elem),
			VecType: vecTy,
		}})
	}
	return NewLocalExpr(slot)
}

func (fl *funcLowerer) lowerArrayLit(r *ast.ArrayLit, inf *sema.ExprInfo) Expr {
	arrTy := inf.Type.(*types.StackArray)
	fl.tc.visit(arrTy)

	// Repeat constructor
	if len(r.Elems) == 1 && arrTy.Size > 1 {
		slot := fl.allocSlot("_ll_rarr", arrTy)
		valExpr := fl.lowerExprOwned(r.Elems[0], arrTy.Elem)
		valTmp := fl.allocSlot("_ll_rseed", arrTy.Elem)
		fl.assignLocal(valTmp, valExpr)

		idx := fl.allocSlot("_ll_ri", types.TypeInt)
		fl.assignLocal(idx, NewLitInt(0, false, types.TypeInt))
		// Copy into indices `0 .. Size-2`, and move `valTmp` into index `Size-1`!
		fl.emit(&InstrWhile{
			Cond: &ExprBinary{
				Op:    token.Lt,
				Left:  NewLocalExpr(idx),
				Right: NewLitInt(uint64(arrTy.Size-1), false, types.TypeInt),
				Typ:   types.TypeBool,
			},
			Body: []Instr{
				&InstrAssign{
					Dst: &ExprIndex{
						Base:  &ExprField{Base: NewLocalExpr(slot), FieldName: "data", Typ: &types.Ptr{Elem: arrTy.Elem}},
						Index: NewLocalExpr(idx),
						Typ:   arrTy.Elem,
					},
					Expr: EnsureExpr(fl.copyCallExpr(arrTy.Elem, NewLocalExpr(valTmp)), arrTy.Elem),
				},
				&InstrAssign{
					Dst: NewLocalExpr(idx),
					Expr: &ExprBinary{
						Op:    token.Plus,
						Left:  NewLocalExpr(idx),
						Right: NewLitInt(1, false, types.TypeInt),
						Typ:   types.TypeInt,
					},
				},
			},
		})
		fl.emit(&InstrAssign{
			Dst: &ExprIndex{
				Base:  &ExprField{Base: NewLocalExpr(slot), FieldName: "data", Typ: &types.Ptr{Elem: arrTy.Elem}},
				Index: NewLitInt(uint64(arrTy.Size-1), false, types.TypeInt),
				Typ:   arrTy.Elem,
			},
			Expr: NewLocalExpr(valTmp),
		})
		if slot.MovedLocal != nil {
			fl.emit(&InstrAssign{Dst: NewLocalExpr(slot.MovedLocal), Expr: NewLitBool(false)})
		}
		return NewLocalExpr(slot)
	}

	// Explicit elements (including single element when Size == 1)
	slot := fl.allocSlot("_ll_arr", arrTy)
	for i, el := range r.Elems {
		elVal := fl.lowerExprOwned(el, arrTy.Elem)
		fl.emit(&InstrAssign{
			Dst: &ExprIndex{
				Base:  &ExprField{Base: NewLocalExpr(slot), FieldName: "data", Typ: &types.Ptr{Elem: arrTy.Elem}},
				Index: NewLitInt(uint64(i), false, types.TypeInt),
				Typ:   arrTy.Elem,
			},
			Expr: EnsureExpr(elVal, arrTy.Elem),
		})
	}
	if slot.MovedLocal != nil {
		fl.emit(&InstrAssign{Dst: NewLocalExpr(slot.MovedLocal), Expr: NewLitBool(false)})
	}
	return NewLocalExpr(slot)
}

func (fl *funcLowerer) lowerTupleLit(lit *ast.TupleLit, inf *sema.ExprInfo) Expr {
	var tupTy *types.Tuple
	if t, ok := inf.Type.(*types.Tuple); ok {
		tupTy = t
	} else if d, ok := inf.Type.(*types.Distinct); ok {
		tupTy = d.Base.(*types.Tuple)
	}
	fl.tc.visit(inf.Type)
	slot := fl.allocSlot("_ll_tup", inf.Type)
	for i, el := range lit.Elems {
		elVal := fl.lowerExprOwned(el, tupTy.Elems[i])
		fName := fmt.Sprintf("_%d", i)
		fl.emit(&InstrAssign{
			Dst: &ExprField{
				Base:      NewLocalExpr(slot),
				FieldName: fName,
				FieldIdx:  i,
				Typ:       tupTy.Elems[i],
			},
			Expr: EnsureExpr(elVal, tupTy.Elems[i]),
		})
	}
	if slot.MovedLocal != nil {
		fl.emit(&InstrAssign{Dst: NewLocalExpr(slot.MovedLocal), Expr: NewLitBool(false)})
	}
	return NewLocalExpr(slot)
}

func (fl *funcLowerer) lowerSetLit(s *ast.SetLit, inf *sema.ExprInfo) Expr {
	setTy := inf.Type
	elemTy, _ := types.SetElem(setTy)
	fl.tc.visit(setTy)
	if len(s.Elems) == 0 {
		return &ExprLitZero{Typ: setTy}
	}
	slot := fl.allocSlot("_ll_set", setTy)
	fl.assignLocal(slot, fl.callMethod(setTy, "with_capacity", NewLitInt(uint64(len(s.Elems)), false, types.TypeInt)))
	for _, el := range s.Elems {
		elVal := fl.lowerExprOwned(el, elemTy)
		fl.emit(&InstrExpr{Expr: fl.callMethod(setTy, "insert",
			&ExprAddrOf{Place: NewLocalExpr(slot), Typ: &types.Ptr{Elem: setTy}},
			EnsureExpr(elVal, elemTy),
		)})
	}
	return NewLocalExpr(slot)
}

func (fl *funcLowerer) lowerMapLit(m *ast.MapLit, inf *sema.ExprInfo) Expr {
	mapTy := inf.Type
	keyTy, valTy, _ := types.MapKV(mapTy)
	fl.tc.visit(mapTy)
	if len(m.Entries) == 0 {
		return &ExprLitZero{Typ: mapTy}
	}
	slot := fl.allocSlot("_ll_map", mapTy)
	fl.assignLocal(slot, fl.callMethod(mapTy, "with_capacity", NewLitInt(uint64(len(m.Entries)), false, types.TypeInt)))
	for _, ent := range m.Entries {
		kVal := fl.lowerExprOwned(ent.Key, keyTy)
		vVal := fl.lowerExprOwned(ent.Value, valTy)
		fl.emit(&InstrExpr{Expr: fl.callMethod(mapTy, "insert",
			&ExprAddrOf{Place: NewLocalExpr(slot), Typ: &types.Ptr{Elem: mapTy}},
			EnsureExpr(kVal, keyTy),
			EnsureExpr(vVal, valTy),
		)})
	}
	return NewLocalExpr(slot)
}

func (fl *funcLowerer) lowerStructLit(sl *ast.StructLit, inf *sema.ExprInfo) Expr {
	st := inf.StructType
	fl.tc.visit(st)
	slot := fl.allocSlot("_ll_st", st)

	providedMap := make(map[string]ast.Expr, len(sl.Fields))
	// Preserve evaluation order of explicitly written fields first!
	providedVals := make(map[string]any, len(sl.Fields))
	for _, fi := range sl.Fields {
		providedMap[fi.Name] = fi.Value
		sf, _ := st.FieldByName(fi.Name)
		if sf != nil {
			valStr := fl.lowerExprOwned(fi.Value, sf.Type)
			ftmp := fl.allocSlot("_ll_sf", sf.Type)
			fl.assignLocal(ftmp, valStr)
			providedVals[fi.Name] = ftmp
		}
	}

	decl := fl.prog.StructDecls[st]
	for idx, sf := range st.Fields {
		var fExpr any
		if valName, ok := providedVals[sf.Name]; ok {
			fExpr = valName
		} else if decl != nil && idx < len(decl.Fields) && decl.Fields[idx].Default != nil {
			prevBM := fl.bm
			if sbm := fl.prog.StructBodyMaps[st]; sbm != nil {
				fl.bm = sbm
			}
			fExpr = fl.lowerStructDefaultExpr(decl.Fields[idx].Default, sf.Type)
			fl.bm = prevBM
		} else if optF, isOpt := sf.Type.(*types.Optional); isOpt {
			fl.tc.visit(optF)
			fExpr = optNoneExpr(optF)
		} else {
			fExpr = &ExprLitZero{Typ: sf.Type}
		}
		fl.emit(&InstrAssign{
			Dst:  &ExprField{Base: NewLocalExpr(slot), FieldName: sf.CName, Typ: sf.Type, IsPtr: false},
			Expr: EnsureExpr(fExpr, sf.Type),
		})
	}
	if slot.MovedLocal != nil {
		fl.emit(&InstrAssign{Dst: NewLocalExpr(slot.MovedLocal), Expr: NewLitBool(false)})
	}
	return NewLocalExpr(slot)
}

func (fl *funcLowerer) lowerStructDefaultExpr(defExpr ast.Expr, fieldTy types.Type) Expr {
	fl.tc.visit(fieldTy)
	switch d := defExpr.(type) {
	case *ast.VecLit:
		if len(d.Elems) == 0 {
			return &ExprLitZero{Typ: fieldTy}
		}
	case *ast.EmptyHashLit:
		return &ExprLitZero{Typ: fieldTy}
	case *ast.NoneLit:
		if opt, ok := fieldTy.(*types.Optional); ok {
			return optNoneExpr(opt)
		}
	case *ast.IntLit:
		return NewLitInt(d.Val, false, fieldTy)
	case *ast.FloatLit:
		return NewLitFloat(d.Val, fieldTy)
	case *ast.BoolLit:
		return NewLitBool(d.Val)
	case *ast.StringLit:
		return &ExprLitString{Val: d.Val}
	}
	return fl.lowerExprOwned(defExpr, fieldTy)
}

func (fl *funcLowerer) lowerFuncLitExpr(_ *ast.FuncLit, inf *sema.ExprInfo) Expr {
	li := inf.Lambda
	fl.tc.visit(li.Sig)
	if li.Sig.Extern {
		return &ExprConstructClosure{EntryFn: fl.registerThunk.cCallback(li.CName, li.Sig), FuncType: li.Sig}
	}
	if len(li.Captures) == 0 {
		return &ExprConstructClosure{
			EntryFn:  li.CName + "_entry",
			FuncType: li.Sig,
		}
	}

	// Allocate heap environment struct & populate captured values (moving Resource captures!)
	envSlot := fl.allocSlot("_ll_env", &types.Ptr{Elem: &types.Struct{CName: li.EnvCName}})
	fl.assignLocal(envSlot, &ExprAlloc{
		AllocType: &types.Struct{CName: li.EnvCName},
		Typ:       &types.Ptr{Elem: &types.Struct{CName: li.EnvCName}},
	})
	for _, capItem := range li.Captures {
		fl.tc.visit(capItem.Type)
		var valExpr Expr
		if capField, ok := fl.captureFields[capItem.OuterSym]; ok {
			// Nested lambda capturing from an enclosing lambda's env
			if capItem.Type.IsPlain() {
				valExpr = capField
			} else {
				valExpr = EnsureExpr(fl.copyCallExpr(capItem.Type, capField), capItem.Type)
			}
		} else if loc, ok := fl.symLocals[capItem.OuterSym]; ok {
			if capItem.Moved && !capItem.Type.IsPlain() {
				mvTmp := fl.allocSlot("_ll_capmv", capItem.Type)
				fl.assignLocal(mvTmp, NewLocalExpr(loc))
				fl.emit(NewInstrZero(loc, loc.Type, loc.MovedFlag))
				valExpr = NewLocalExpr(mvTmp)
			} else {
				if loc.IsMutPtr {
					valExpr = &ExprDeref{Ptr: NewLocalExpr(loc), Typ: capItem.Type}
				} else {
					valExpr = NewLocalExpr(loc)
				}
			}
		} else {
			valExpr = &ExprGlobal{Global: &Global{Name: capItem.OuterSym.CName, CName: capItem.OuterSym.CName, Type: capItem.Type}}
		}
		fl.emit(&InstrAssign{
			Dst:  &ExprField{Base: NewLocalExpr(envSlot), FieldName: capItem.CName, Typ: capItem.Type, IsPtr: true},
			Expr: EnsureExpr(valExpr, capItem.Type),
		})
	}

	return &ExprConstructClosure{
		EntryFn:  li.CName + "_entry",
		EnvPtr:   NewLocalExpr(envSlot),
		CopyFn:   li.EnvCName + "_copy",
		DropFn:   li.EnvCName + "_drop",
		FuncType: li.Sig,
	}
}

// -----------------------------------------------------------------------------
// Optional, Interface Box, & Literal Formatting Helpers
// -----------------------------------------------------------------------------

func optNoneExpr(opt *types.Optional) Expr {
	return &ExprLitNone{OptType: opt}
}

func optHasValueExpr(optExpr any, opt *types.Optional) Expr {
	return &ExprOptionalHasValue{Opt: EnsureExpr(optExpr, opt)}
}

func optUnwrapValExpr(optExpr any, opt *types.Optional) Expr {
	return &ExprOptionalValue{Opt: EnsureExpr(optExpr, opt), ElemType: opt.Elem, IsBoxed: opt.IsBoxed()}
}

func optUnwrapPtrExpr(optPtrExpr any, opt *types.Optional) Expr {
	ptrTy := &types.Ptr{Elem: opt}
	return &ExprOptionalValuePtr{OptPtr: EnsureExpr(optPtrExpr, ptrTy), ElemType: opt.Elem, IsBoxed: opt.IsBoxed()}
}

func (fl *funcLowerer) wrapInOptionalExpr(elemExpr Expr, opt *types.Optional) Expr {
	fl.tc.visit(opt)
	return &ExprWrapOptional{OptType: opt, Value: EnsureExpr(elemExpr, opt.Elem)}
}

func (fl *funcLowerer) boxConcreteIntoInterface(valExpr Expr, concreteTy types.Type, ifc *types.Interface) Expr {
	fl.tc.visit(concreteTy)
	fl.tc.visit(ifc)
	boxPtr := fl.allocSlot("_ll_ibox", &types.Ptr{Elem: concreteTy})
	fl.emit(&InstrAssign{
		Dst:  NewLocalExpr(boxPtr),
		Expr: &ExprAlloc{AllocType: concreteTy, Typ: &types.Ptr{Elem: concreteTy}},
	})
	fl.emit(&InstrAssign{
		Dst:  &ExprDeref{Ptr: NewLocalExpr(boxPtr), Typ: concreteTy},
		Expr: EnsureExpr(valExpr, concreteTy),
	})
	return &ExprBoxInterface{IfcType: ifc, ConcreteType: concreteTy, Value: NewLocalExpr(boxPtr), Owned: true}
}

// resultIsValueExpr tests whether the result value `val` holds its `value` member.
func resultIsValueExpr(val Expr, en *types.Enum) Expr {
	vv, _ := en.ResultValue()
	return &ExprBinary{
		Op:    token.Eq,
		Left:  &ExprField{Base: val, FieldName: "tag", Typ: types.TypeInt},
		Right: &ExprLitInt{Val: uint64(vv.Value), Typ: types.TypeInt},
		Typ:   types.TypeBool,
	}
}

// resultValueExpr reads the success payload of the result value `val` (which must hold `value`).
func resultValueExpr(val Expr, en *types.Enum) Expr {
	vv, _ := en.ResultValue()
	return &ExprField{Base: &ExprField{Base: val, FieldName: "data"}, FieldName: vv.CName, Typ: vv.Payload}
}

// resultValuePtrExpr addresses the success payload through a pointer to a result.
func resultValuePtrExpr(ptr Expr, en *types.Enum) Expr {
	vv, _ := en.ResultValue()
	place := &ExprField{Base: &ExprField{Base: ptr, FieldName: "data", IsPtr: true}, FieldName: vv.CName, Typ: vv.Payload}
	return &ExprAddrOf{Place: place, Typ: vv.Payload}
}

func (fl *funcLowerer) lowerResultFallback(b *ast.BinaryExpr, en *types.Enum, resTy types.Type) Expr {
	fl.tc.visit(en)
	resSlot := fl.allocSlot("_ll_qq", resTy)

	outerTemps := fl.stmtTemps
	fl.stmtTemps = nil
	leftVal := fl.lowerExprOwned(b.Left, en)
	resultSlot := fl.allocSlot("_ll_qq_res", en)
	fl.assignLocal(resultSlot, leftVal)
	fl.flushStmtTemps()

	// Then: move the success payload out and leave the result inert.
	fl.pushInstrBuf()
	fl.assignLocal(resSlot, resultValueExpr(NewLocalExpr(resultSlot), en))
	fl.emit(&InstrAssign{Dst: NewLocalExpr(resultSlot), Expr: NewLitZero(en)})
	thenBuf := fl.popInstrBuf()

	// Else: drop the failure, then evaluate the fallback.
	fl.pushInstrBuf()
	fl.emit(&InstrDrop{Var: resultSlot, VarName: resultSlot.CName, Type: en, MovedFlag: resultSlot.MovedFlag})
	if fl.info(b.Right).Type == types.TypeNever {
		if len(outerTemps) > 0 {
			fl.stmtTemps = append([]*Local(nil), outerTemps...)
		}
		fl.lowerVoidExpr(b.Right)
		fl.flushStmtTemps()
	} else {
		fl.assignLocal(resSlot, fl.lowerExprOwned(b.Right, resTy))
		fl.flushStmtTemps()
	}
	elseBuf := fl.popInstrBuf()
	fl.stmtTemps = outerTemps

	fl.emit(NewInstrIf(resultIsValueExpr(NewLocalExpr(resultSlot), en), thenBuf, elseBuf))
	return NewLocalExpr(resSlot)
}
