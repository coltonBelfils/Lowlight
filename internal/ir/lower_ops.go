package ir

import (
	"fmt"

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
	isWhileNoElse := we.Else == nil && we.EvalType == nil

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
			iterVal := fl.lowerExprOwned(we.Body, loopElemTy)
			iterTmp := fl.allocSlot("_ll_itval", loopElemTy)
			fl.assignLocal(iterTmp, iterVal)
			fl.flushStmtTemps()
			fl.storeLoopRunningResult(loopScope, NewLocalExpr(iterTmp))
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
		if colSlot.MovedLocal != nil {
			fl.emit(&InstrAssign{Dst: NewLocalExpr(colSlot.MovedLocal), Expr: NewLitBool(false)})
		}
		elemTy = fl.info(fe.Body).Type
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

	if fe.Else != nil {
		if needValue && colSlot != nil {
			var isEmptyCond Expr
			switch colSlot.Type.(type) {
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
			} else {
				fl.lowerVoidExpr(fe.Else)
				fl.flushStmtTemps()
			}
		} else {
			fl.lowerVoidExpr(fe.Else)
			fl.flushStmtTemps()
		}
	}

	if colSlot != nil {
		return NewLocalExpr(colSlot)
	}
	return nil
}

func (fl *funcLowerer) emitPushToCol(colSlot *Local, itemVal Expr, elemTy types.Type) {
	switch ct := colSlot.Type.(type) {
	case *types.Vector:
		fl.emit(&InstrExpr{Expr: &ExprVecPush{VecPtr: &ExprAddrOf{Place: NewLocalExpr(colSlot), Typ: &types.Ptr{Elem: ct}}, Item: EnsureExpr(itemVal, ct.Elem), VecType: ct}})
	case *types.Set:
		fl.emit(&InstrExpr{Expr: &ExprSetInsert{SetPtr: &ExprAddrOf{Place: NewLocalExpr(colSlot), Typ: &types.Ptr{Elem: ct}}, Item: EnsureExpr(itemVal, ct.Elem), SetType: ct}})
	case *types.Map:
		entrySlot := fl.allocSlot("_ll_entry", elemTy)
		fl.assignLocal(entrySlot, itemVal)
		fl.emit(&InstrExpr{Expr: &ExprMapInsert{
			MapPtr:  &ExprAddrOf{Place: NewLocalExpr(colSlot), Typ: &types.Ptr{Elem: ct}},
			Key:     &ExprField{Base: NewLocalExpr(entrySlot), FieldName: "ll_key", Typ: ct.Key},
			Val:     &ExprField{Base: NewLocalExpr(entrySlot), FieldName: "ll_value", Typ: ct.Value},
			MapType: ct,
		}})
	default:
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
		curIdx := fl.allocSlot("_ll_rcur", loTy)
		endIdx := fl.allocSlot("_ll_rend", loTy)
		doneSlot := fl.allocSlot("_ll_rdone", types.TypeBool)
		fl.assignLocal(curIdx, loExpr)
		fl.assignLocal(endIdx, hiExpr)
		if inclusive {
			fl.assignLocal(doneSlot, &ExprBinary{Op: token.Gt, Left: NewLocalExpr(curIdx), Right: NewLocalExpr(endIdx), Typ: types.TypeBool})
		} else {
			fl.assignLocal(doneSlot, &ExprBinary{Op: token.Gte, Left: NewLocalExpr(curIdx), Right: NewLocalExpr(endIdx), Typ: types.TypeBool})
		}
		fl.flushStmtTemps()

		stepInit := []Instr{
			&InstrAssign{Dst: NewLocalExpr(loc1), Expr: NewLocalExpr(curIdx)},
		}
		var stepAdv []Instr
		incExpr := &ExprBinary{Op: token.Plus, Left: NewLocalExpr(curIdx), Right: NewLitInt(1, false, loTy), Typ: loTy}
		if inclusive {
			stepAdv = []Instr{
				NewInstrIf(
					&ExprBinary{Op: token.Eq, Left: NewLocalExpr(curIdx), Right: NewLocalExpr(endIdx), Typ: types.TypeBool},
					[]Instr{&InstrAssign{Dst: NewLocalExpr(doneSlot), Expr: NewLitBool(true)}},
					[]Instr{&InstrAssign{Dst: NewLocalExpr(curIdx), Expr: incExpr}},
				),
			}
		} else {
			stepAdv = []Instr{
				&InstrAssign{Dst: NewLocalExpr(curIdx), Expr: incExpr},
				NewInstrIf(
					&ExprBinary{Op: token.Gte, Left: NewLocalExpr(curIdx), Right: NewLocalExpr(endIdx), Typ: types.TypeBool},
					[]Instr{&InstrAssign{Dst: NewLocalExpr(doneSlot), Expr: NewLitBool(true)}},
					nil,
				),
			}
		}
		emitBodyIteration(stepInit, stepAdv, &ExprUnary{Op: token.KwNot, Operand: NewLocalExpr(doneSlot), Typ: types.TypeBool})

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

		switch c := colTy.(type) {
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
			utf8Call := &ExprCall{
				Callee: &ExprFuncRef{Name: "ll_str_utf8_next"},
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
			entriesField := &ExprField{Base: colPtrExpr, FieldName: "entries", Typ: &types.Ptr{Elem: types.TypeVoid}, IsPtr: true}
			entryAccess := &ExprIndex{Base: entriesField, Index: NewLocalExpr(idxVar)}
			aliveField := &ExprField{Base: entryAccess, FieldName: "alive", Typ: types.TypeBool}
			notAlive := &ExprUnary{Op: token.KwNot, Operand: aliveField, Typ: types.TypeBool}
			keyField := &ExprField{Base: entryAccess, FieldName: "key", Typ: c.Elem}
			stepInit := []Instr{
				NewInstrIf(notAlive, []Instr{&InstrGoto{Label: contLbl}}, nil),
				&InstrAssign{Dst: NewLocalExpr(loc1), Expr: keyField},
			}
			incExpr := &ExprBinary{Op: token.Plus, Left: NewLocalExpr(idxVar), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
			stepAdv := []Instr{&InstrAssign{Dst: NewLocalExpr(idxVar), Expr: incExpr}}
			entLenField := &ExprField{Base: colPtrExpr, FieldName: "ent_len", Typ: types.TypeInt, IsPtr: true}
			loopCond := &ExprBinary{Op: token.Lt, Left: NewLocalExpr(idxVar), Right: entLenField, Typ: types.TypeBool}
			emitBodyIteration(stepInit, stepAdv, loopCond)

		case *types.Map:
			idxVar := fl.allocSlot("_ll_mi", types.TypeInt)
			fl.assignLocal(idxVar, NewLitInt(0, false, types.TypeInt))
			fl.usedLabels[contLbl] = true
			entriesField := &ExprField{Base: colPtrExpr, FieldName: "entries", Typ: &types.Ptr{Elem: types.TypeVoid}, IsPtr: true}
			entryAccess := &ExprIndex{Base: entriesField, Index: NewLocalExpr(idxVar)}
			aliveField := &ExprField{Base: entryAccess, FieldName: "alive", Typ: types.TypeBool}
			notAlive := &ExprUnary{Op: token.KwNot, Operand: aliveField, Typ: types.TypeBool}
			keyAccess := &ExprField{Base: entryAccess, FieldName: "key", Typ: c.Key}
			valAccess := &ExprField{Base: entryAccess, FieldName: "val", Typ: c.Value}
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
			entLenField := &ExprField{Base: colPtrExpr, FieldName: "ent_len", Typ: types.TypeInt, IsPtr: true}
			loopCond := &ExprBinary{Op: token.Lt, Left: NewLocalExpr(idxVar), Right: entLenField, Typ: types.TypeBool}
			emitBodyIteration(stepInit, stepAdv, loopCond)
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

	rawScrutTy := fl.info(me.Scrutinee).Type
	scrutTy := rawScrutTy
	isArenaRefScrut := false
	if rf, ok := rawScrutTy.(*types.Ref); ok {
		if _, isEn := rf.Elem.(*types.Enum); isEn {
			scrutTy = rf.Elem
			isArenaRefScrut = true
		}
	}
	fl.tc.visit(scrutTy)

	var scrutPtrExpr Expr
	var scrutValExpr Expr
	var scrutHoldSlot *Local
	if isArenaRefScrut {
		scrutPtrSlot := fl.allocSlot("_ll_scrutp", rawScrutTy)
		fl.assignLocal(scrutPtrSlot, fl.lowerExprRead(me.Scrutinee))
		scrutPtrExpr = NewLocalExpr(scrutPtrSlot)
		scrutValExpr = &ExprDeref{Ptr: scrutPtrExpr, Typ: scrutTy}
	} else if !fl.info(me.Scrutinee).WrapOptional && fl.isAddressable(me.Scrutinee) {
		scrutPtrSlot := fl.allocSlot("_ll_scrutp", &types.Ptr{Elem: scrutTy})
		fl.assignLocal(scrutPtrSlot, fl.lowerLValueAddr(me.Scrutinee))
		scrutPtrExpr = NewLocalExpr(scrutPtrSlot)
		scrutValExpr = &ExprDeref{Ptr: scrutPtrExpr, Typ: scrutTy}
	} else {
		scrutHoldSlot = fl.allocSlot("_ll_scrut", scrutTy)
		fl.assignLocal(scrutHoldSlot, fl.lowerExprOwned(me.Scrutinee, scrutTy))
		scrutValExpr = NewLocalExpr(scrutHoldSlot)
		scrutPtrExpr = &ExprAddrOf{Place: scrutValExpr, Typ: &types.Ptr{Elem: scrutTy}}
	}
	fl.flushStmtTemps()

	fl.pushScope(inf.TargetID, exitLbl, "", resSlot, nil, false)
	if scrutHoldSlot != nil && !scrutTy.IsPlain() {
		fl.scopes[len(fl.scopes)-1].ownedLocals = append(fl.scopes[len(fl.scopes)-1].ownedLocals, scrutHoldSlot)
	}

	// Build nested `if / else if / else` chain across `me.Arms`
	var buildArmChain func(armIdx int) []Instr
	buildArmChain = func(armIdx int) []Instr {
		if armIdx >= len(me.Arms) {
			return nil
		}
		arm := me.Arms[armIdx]

		// Lower arm body (initializing any destructured variant bindings inside the arm scope!)
		fl.pushInstrBuf()
		armScope := fl.pushScope(0, "", "", nil, nil, false)
		for _, pat := range arm.Patterns {
			if vp, isVP := pat.(*ast.VariantPattern); isVP {
				fl.emitVariantPatternBindings(vp, scrutTy, scrutPtrExpr, scrutValExpr, armScope)
			}
		}
		if !needValue || resSlot == nil {
			fl.lowerVoidExpr(arm.Body)
			fl.flushStmtTemps()
		} else {
			aTy := fl.info(arm.Body).Type
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

		// Final arm (catch-all `else:`, exhaustive `else Variant:`, or final `when` of an exhaustive enum match):
		// Since `sema` already verified exhaustiveness, the final arm always executes if reached!
		if arm.IsElse || armIdx == len(me.Arms)-1 {
			return armBody
		}

		// Build OR condition across `arm.Patterns`
		fl.pushInstrBuf()
		var condParts []Expr
		for _, pat := range arm.Patterns {
			switch p := pat.(type) {
			case *ast.ExprPattern:
				if _, isNone := p.Expr.(*ast.NoneLit); isNone {
					optScrut := scrutTy.(*types.Optional)
					condParts = append(condParts, &ExprUnary{
						Op:      token.KwNot,
						Operand: optHasValueExpr(scrutValExpr, optScrut),
						Typ:     types.TypeBool,
					})
				} else if en, isEn := scrutTy.(*types.Enum); isEn && en.IsTagged && fl.info(p.Expr).EnumVariant != nil {
					vr := fl.info(p.Expr).EnumVariant
					condParts = append(condParts, &ExprBinary{
						Op:    token.Eq,
						Left:  &ExprField{Base: EnsureExpr(scrutPtrExpr, &types.Ptr{Elem: scrutTy}), FieldName: "tag", Typ: types.TypeInt, IsPtr: true},
						Right: &ExprLitInt{Val: uint64(vr.Value), Typ: types.TypeInt},
						Typ:   types.TypeBool,
					})
				} else {
					pExpr := fl.lowerExprRead(p.Expr)
					if fl.info(p.Expr).WrapOptional {
						optScrut := scrutTy.(*types.Optional)
						pExpr = fl.wrapInOptionalExpr(pExpr, optScrut)
					}
					condParts = append(condParts, fl.buildEqualityExpr(scrutTy, scrutValExpr, pExpr, false))
				}
			case *ast.RangePattern:
				loExpr := fl.lowerExprRead(p.Low)
				hiExpr := fl.lowerExprRead(p.High)
				hiOp := token.Lt
				if p.Inclusive {
					hiOp = token.Lte
				}
				geExpr := &ExprBinary{
					Op:    token.Gte,
					Left:  EnsureExpr(scrutValExpr, scrutTy),
					Right: EnsureExpr(loExpr, scrutTy),
					Typ:   types.TypeBool,
				}
				hiCheck := &ExprBinary{
					Op:    hiOp,
					Left:  EnsureExpr(scrutValExpr, scrutTy),
					Right: EnsureExpr(hiExpr, scrutTy),
					Typ:   types.TypeBool,
				}
				condParts = append(condParts, &ExprBinary{
					Op:    token.KwAnd,
					Left:  geExpr,
					Right: hiCheck,
					Typ:   types.TypeBool,
				})
			case *ast.VariantPattern:
				mem := p.Target.(*ast.MemberExpr)
				if vr := fl.info(mem).EnumVariant; vr != nil {
					condParts = append(condParts, &ExprBinary{
						Op:    token.Eq,
						Left:  &ExprField{Base: EnsureExpr(scrutPtrExpr, &types.Ptr{Elem: scrutTy}), FieldName: "tag", Typ: types.TypeInt, IsPtr: true},
						Right: &ExprLitInt{Val: uint64(vr.Value), Typ: types.TypeInt},
						Typ:   types.TypeBool,
					})
				}
			}
		}
		fl.flushStmtTemps()
		patSetup := fl.popInstrBuf()

		var condExpr Expr
		if len(condParts) == 0 {
			condExpr = NewLitBool(false)
		} else {
			condExpr = condParts[0]
			for _, cp := range condParts[1:] {
				condExpr = &ExprBinary{Op: token.KwOr, Left: condExpr, Right: cp, Typ: types.TypeBool}
			}
		}
		restChain := buildArmChain(armIdx + 1)
		return append(patSetup, NewInstrIf(condExpr, armBody, restChain))
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

	if !vp.IsStruct {
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
		return
	}

	// Named-field struct variant (`when Expr.Add[left, right]:`)
	hasOwnRes := false
	boundByField := make(map[string]int, len(vp.Bindings))
	for i, b := range vp.Bindings {
		if b.FieldName != "" {
			boundByField[b.FieldName] = i
		}
		if !b.IsWildcard && b.Mode == ast.PatternBindOwn {
			if sf, _ := vr.FieldByName(b.FieldName); sf != nil && !sf.Type.IsPlain() {
				hasOwnRes = true
			}
		}
	}

	dataPtr := &ExprField{Base: scrutPtrExpr, FieldName: "data", IsPtr: true}
	varPtr := &ExprField{Base: dataPtr, FieldName: vr.CName}
	dataVal := &ExprField{Base: scrutValExpr, FieldName: "data"}
	varVal := &ExprField{Base: dataVal, FieldName: vr.CName}

	if !hasOwnRes {
		for i, b := range vp.Bindings {
			if i >= len(syms) || syms[i] == nil {
				continue
			}
			sym := syms[i]
			sf, _ := vr.FieldByName(b.FieldName)
			if sf == nil {
				continue
			}
			fl.tc.visit(sym.Type)
			loc := &Local{
				CName:    sym.CName,
				Type:     sym.Type,
				IsMutPtr: b.Mode == ast.PatternBindMut,
			}
			fl.locals = append(fl.locals, loc)
			fl.symLocals[sym] = loc

			fieldPtr := &ExprField{Base: varPtr, FieldName: sf.CName, Typ: sym.Type}
			fieldVal := &ExprField{Base: varVal, FieldName: sf.CName, Typ: sym.Type}
			if loc.IsMutPtr {
				fl.emit(&InstrAssign{Dst: NewLocalExpr(loc), Expr: &ExprAddrOf{Place: fieldPtr, Typ: sym.Type}})
			} else {
				fl.assignLocal(loc, fieldVal)
			}
		}
		return
	}

	// Destructive `own` move out of struct variant: extract bound fields, drop unbound/wildcard Resource fields, then zero `*scrutPtr`!
	for _, sf := range vr.Fields {
		fieldPtr := &ExprField{Base: varPtr, FieldName: sf.CName, Typ: sf.Type}
		fieldVal := &ExprField{Base: varVal, FieldName: sf.CName, Typ: sf.Type}
		if bIdx, isBound := boundByField[sf.Name]; isBound && bIdx < len(syms) && syms[bIdx] != nil {
			sym := syms[bIdx]
			fl.tc.visit(sym.Type)
			loc := &Local{
				CName: sym.CName,
				Type:  sym.Type,
			}
			if !sf.Type.IsPlain() && NeedsMovedFlag(sym.Type) {
				loc.MovedFlag = sym.CName + "_moved"
				loc.MovedLocal = &Local{CName: loc.MovedFlag, Type: types.TypeBool}
			}
			fl.locals = append(fl.locals, loc)
			fl.symLocals[sym] = loc
			fl.assignLocal(loc, fieldVal)
			if !sf.Type.IsPlain() {
				armScope.ownedLocals = append(armScope.ownedLocals, loc)
			}
		} else if !sf.Type.IsPlain() {
			fl.emit(&InstrExpr{Expr: &ExprCall{
				Callee: &ExprFuncRef{Name: DropFnName(sf.Type)},
				Args:   []Expr{&ExprAddrOf{Place: fieldPtr, Typ: &types.Ptr{Elem: sf.Type}}},
				Typ:    types.TypeVoid,
			}})
		}
	}
	fl.emit(&InstrAssign{Dst: scrutValExpr, Expr: NewLitZero(en)})
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
	return &ExprAddrOf{Place: &ExprGlobal{Global: &Global{Name: sym.CName, Type: sym.Type}}, Typ: &types.Ptr{Elem: sym.Type}}
}

func (fl *funcLowerer) lowerIdentExpr(v *ast.IdentExpr, inf *sema.ExprInfo) Expr {
	sym := inf.Sym
	if sym == nil {
		cName := types.SanitizeCIdent(v.Name)
		return &ExprGlobal{Global: &Global{Name: v.Name, CName: cName, Type: inf.Type}}
	}
	// Static top-level function referenced as a first-class closure value (`let op = static_add`)!
	if sym.FuncInst != nil {
		fnTy := sym.FuncInst.Sig
		thunk := fl.registerThunk(sym.FuncInst, fnTy)
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

	// 2. Short-circuiting optional fallback `opt_expr ?? fallback_expr`
	if b.Op == token.QuestQuest {
		optTy := fl.info(b.Left).Type.(*types.Optional)
		fl.tc.visit(optTy)
		resSlot := fl.allocSlot("_ll_qq", inf.Type)

		leftVal := fl.lowerExprOwned(b.Left, optTy)
		optSlot := fl.allocSlot("_ll_qq_opt", optTy)
		fl.assignLocal(optSlot, leftVal)

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
			fl.lowerVoidExpr(b.Right)
		} else {
			rVal := fl.lowerExprOwned(b.Right, inf.Type)
			fl.assignLocal(resSlot, rVal)
		}
		elseBuf := fl.popInstrBuf()

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
		lhsExpr := EnsureExpr(lhs, lty)
		rhsExpr := EnsureExpr(rhs, lty)

		if p := types.UnderlyingPrimitive(lty); p != nil {
			return &ExprBinary{
				Op:    b.Op,
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
				Op:    b.Op,
				Left:  call,
				Right: &ExprLitInt{Val: 0, Typ: types.TypeInt},
				Typ:   types.TypeBool,
			}
		}
		if d, isDist := lty.(*types.Distinct); isDist {
			castL := &ExprCast{Val: lhsExpr, From: lty, To: d.Base}
			castR := &ExprCast{Val: rhsExpr, From: lty, To: d.Base}
			if customBase := fl.prog.LookupConcreteMethod(d.Base, "cmp"); customBase != nil {
				call := &ExprCall{
					Callee: &ExprFuncRef{Name: customBase.CName, TargetFunc: customBase, FuncType: customBase.Sig},
					Args:   []Expr{castL, castR},
					Typ:    types.TypeInt,
				}
				return &ExprBinary{
					Op:    b.Op,
					Left:  call,
					Right: &ExprLitInt{Val: 0, Typ: types.TypeInt},
					Typ:   types.TypeBool,
				}
			}
			if p := types.UnderlyingPrimitive(d.Base); p != nil {
				return &ExprBinary{
					Op:    b.Op,
					Left:  castL,
					Right: castR,
					Typ:   types.TypeBool,
				}
			}
		}
		cmpExpr := &ExprCmp{Left: lhsExpr, Right: rhsExpr, ValType: lty}
		return &ExprBinary{
			Op:    b.Op,
			Left:  cmpExpr,
			Right: &ExprLitInt{Val: 0, Typ: types.TypeInt},
			Typ:   types.TypeBool,
		}
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

	// 6. String concatenation `+`
	if b.Op == token.Plus && inf.Type.Equals(types.TypeString) {
		lhs := fl.lowerExprRead(b.Left)
		rhs := fl.lowerExprRead(b.Right)
		return &ExprStringConcat{
			Left:  EnsureExpr(lhs, types.TypeString),
			Right: EnsureExpr(rhs, types.TypeString),
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

func (fl *funcLowerer) buildEqualityExpr(ty types.Type, lhs, rhs any, isNeq bool) Expr {
	fl.tc.visit(ty)
	op := token.Eq
	if isNeq {
		op = token.Neq
	}
	lhsExpr := EnsureExpr(lhs, ty)
	rhsExpr := EnsureExpr(rhs, ty)

	if p := types.UnderlyingPrimitive(ty); p != nil {
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
	case *types.Struct, *types.Optional:
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
			thunk := fl.registerThunk(inf.Sym.FuncInst, fnTy)
			return &ExprConstructClosure{
				EntryFn:  thunk,
				FuncType: fnTy,
			}
		}
		return &ExprGlobal{Global: &Global{Name: inf.Sym.CName, Type: inf.Sym.Type}}
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
			st := rf.Elem.(*types.Struct)
			sf, _ := st.FieldByName(m.Member)
			valField := &ExprField{Base: NewLocalExpr(baseTmp), FieldName: "value", Typ: rf}
			structAccess = &ExprField{Base: valField, FieldName: sf.CName, Typ: sf.Type, IsPtr: true}
			innerTy = sf.Type
		} else {
			st := innerTy.(*types.Struct)
			sf, _ := st.FieldByName(m.Member)
			valField := &ExprField{Base: NewLocalExpr(baseTmp), FieldName: "value", Typ: st}
			structAccess = &ExprField{Base: valField, FieldName: sf.CName, Typ: sf.Type, IsPtr: false}
			innerTy = sf.Type
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
		st := rf.Elem.(*types.Struct)
		sf, _ := st.FieldByName(m.Member)
		ptr := fl.lowerExprRead(m.Base)
		return &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: true}
	}
	if p, isPtr := baseTy.(*types.Ptr); isPtr {
		if st, ok := p.Elem.(*types.Struct); ok {
			sf, _ := st.FieldByName(m.Member)
			ptr := fl.lowerExprRead(m.Base)
			return &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: true}
		}
	}
	st := baseTy.(*types.Struct)
	sf, _ := st.FieldByName(m.Member)
	baseExpr := fl.lowerExprRead(m.Base)
	_ = forOwned
	return &ExprField{Base: EnsureExpr(baseExpr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: false}
}

func (fl *funcLowerer) lowerIndexExpr(idx *ast.IndexExpr, inf *sema.ExprInfo, _ bool) Expr {
	rawBaseTy := fl.info(idx.Base).Type
	baseTy := rawBaseTy
	isArenaRef := false
	if rf, ok := rawBaseTy.(*types.Ref); ok {
		baseTy = rf.Elem
		isArenaRef = true
	}

	file, line := formatSpanLoc(idx)
	idxVal := fl.lowerExprRead(idx.Index)

	switch v := baseTy.(type) {
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
			Val: &ExprIndex{Base: dataPtr, Index: NewLocalExpr(iTmp), Typ: types.TypeU8},
			To:  types.TypeU8,
		}

	case *types.Map:
		fl.tc.visit(v)
		fl.tc.visit(inf.Type)
		var mPtr any
		if isArenaRef {
			mPtr = fl.lowerExprRead(idx.Base)
		} else if fl.isAddressable(idx.Base) {
			mPtr = fl.lowerLValueAddr(idx.Base)
		} else {
			bVal := fl.lowerExprRead(idx.Base)
			mTmp := fl.allocSlot("_ll_mv", v)
			fl.assignLocal(mTmp, bVal)
			mPtr = &ExprAddrOf{Place: NewLocalExpr(mTmp), Typ: &types.Ptr{Elem: v}}
		}
		return &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_map_get__" + v.Mangled()},
			Args:   []Expr{EnsureExpr(mPtr, &types.Ptr{Elem: v}), EnsureExpr(idxVal, v.Key)},
			Typ:    types.NewOptional(v.Value),
		}

	case *types.Ptr:
		ptr := fl.lowerExprRead(idx.Base)
		return &ExprIndex{Base: EnsureExpr(ptr, v), Index: EnsureExpr(idxVal, types.TypeInt), Typ: v.Elem}
	}
	return nil
}

func (fl *funcLowerer) lowerSliceExpr(sl *ast.SliceExpr, inf *sema.ExprInfo) Expr {
	baseTy := fl.info(sl.Base).Type
	loVal := fl.lowerExprRead(sl.Low)
	hiVal := fl.lowerExprRead(sl.High)
	if sl.Inclusive {
		hiVal = &ExprBinary{Op: token.Plus, Left: EnsureExpr(hiVal, types.TypeInt), Right: NewLitInt(1, false, types.TypeInt), Typ: types.TypeInt}
	}
	file, line := formatSpanLoc(sl)
	check := 1

	switch v := baseTy.(type) {
	case *types.Primitive: // `string`
		sVal := fl.lowerExprRead(sl.Base)
		fnName := "ll_str_slice_owned"
		if inf.BorrowAsView {
			fnName = "ll_str_slice_view"
		}
		return &ExprCall{
			Callee: &ExprFuncRef{Name: fnName},
			Args: []Expr{
				EnsureExpr(sVal, types.TypeString),
				EnsureExpr(loVal, types.TypeInt),
				EnsureExpr(hiVal, types.TypeInt),
				NewLitInt(uint64(check), false, types.TypeInt),
				&ExprLitCString{Val: file},
				NewLitInt(uint64(line), false, types.TypeInt),
			},
			Typ: inf.Type,
		}

	case *types.Vector:
		fl.tc.visit(v)
		vVal := fl.lowerExprRead(sl.Base)
		fnName := "ll_vec_slice_owned__" + v.Mangled()
		if inf.BorrowAsView {
			fnName = "ll_vec_slice_view__" + v.Mangled()
		}
		return &ExprCall{
			Callee: &ExprFuncRef{Name: fnName},
			Args: []Expr{
				EnsureExpr(vVal, v),
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
			fl.emit(&InstrExpr{Expr: &ExprStringPush{
				StrPtr: &ExprAddrOf{Place: NewLocalExpr(out), Typ: &types.Ptr{Elem: types.TypeString}},
				Item:   &ExprLitString{Val: seg},
			}})
		}
		if i < len(is.Exprs) {
			sub := is.Exprs[i]
			subTy := fl.info(sub).Type
			subExpr := fl.lowerExprRead(sub)
			if subTy.Equals(types.TypeString) {
				fl.emit(&InstrExpr{Expr: &ExprStringPush{
					StrPtr: &ExprAddrOf{Place: NewLocalExpr(out), Typ: &types.Ptr{Elem: types.TypeString}},
					Item:   EnsureExpr(subExpr, types.TypeString),
				}})
			} else {
				strPart := fl.toStringCallExpr(subTy, subExpr)
				partTmp := fl.allocStmtTemp(types.TypeString, strPart)
				fl.emit(&InstrExpr{Expr: &ExprStringPush{
					StrPtr: &ExprAddrOf{Place: NewLocalExpr(out), Typ: &types.Ptr{Elem: types.TypeString}},
					Item:   NewLocalExpr(partTmp),
				}})
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

func (fl *funcLowerer) lowerSetLit(s *ast.SetLit, inf *sema.ExprInfo) Expr {
	setTy := inf.Type.(*types.Set)
	fl.tc.visit(setTy)
	if len(s.Elems) == 0 {
		return &ExprLitZero{Typ: setTy}
	}
	slot := fl.allocSlot("_ll_set", setTy)
	for _, el := range s.Elems {
		elVal := fl.lowerExprOwned(el, setTy.Elem)
		fl.emit(&InstrExpr{Expr: &ExprSetInsert{
			SetPtr:  &ExprAddrOf{Place: NewLocalExpr(slot), Typ: &types.Ptr{Elem: setTy}},
			Item:    EnsureExpr(elVal, setTy.Elem),
			SetType: setTy,
		}})
	}
	return NewLocalExpr(slot)
}

func (fl *funcLowerer) lowerMapLit(m *ast.MapLit, inf *sema.ExprInfo) Expr {
	mapTy := inf.Type.(*types.Map)
	fl.tc.visit(mapTy)
	if len(m.Entries) == 0 {
		return &ExprLitZero{Typ: mapTy}
	}
	slot := fl.allocSlot("_ll_map", mapTy)
	for _, ent := range m.Entries {
		kVal := fl.lowerExprOwned(ent.Key, mapTy.Key)
		vVal := fl.lowerExprOwned(ent.Value, mapTy.Value)
		fl.emit(&InstrExpr{Expr: &ExprMapInsert{
			MapPtr:  &ExprAddrOf{Place: NewLocalExpr(slot), Typ: &types.Ptr{Elem: mapTy}},
			Key:     EnsureExpr(kVal, mapTy.Key),
			Val:     EnsureExpr(vVal, mapTy.Value),
			MapType: mapTy,
		}})
	}
	return NewLocalExpr(slot)
}

func (fl *funcLowerer) lowerStructLit(sl *ast.StructLit, inf *sema.ExprInfo) Expr {
	if inf.EnumVariant != nil && inf.EnumType != nil {
		return fl.lowerEnumStructVariantLit(sl, inf)
	}
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

func (fl *funcLowerer) lowerEnumStructVariantLit(sl *ast.StructLit, inf *sema.ExprInfo) Expr {
	en := inf.EnumType
	vr := inf.EnumVariant
	fl.tc.visit(en)
	slot := fl.allocSlot("_ll_esv", en)

	// Preserve evaluation order of explicitly written fields first!
	providedVals := make(map[string]any, len(sl.Fields))
	for _, fi := range sl.Fields {
		sf, _ := vr.FieldByName(fi.Name)
		if sf != nil {
			valStr := fl.lowerExprOwned(fi.Value, sf.Type)
			ftmp := fl.allocSlot("_ll_esf", sf.Type)
			fl.assignLocal(ftmp, valStr)
			providedVals[fi.Name] = ftmp
		}
	}

	var astVar *ast.EnumVariant
	if decl := fl.prog.EnumDecls[en]; decl != nil {
		for i := range decl.Variants {
			if decl.Variants[i].Name == vr.Name {
				astVar = &decl.Variants[i]
				break
			}
		}
	}

	fl.emit(&InstrAssign{
		Dst:  &ExprField{Base: NewLocalExpr(slot), FieldName: "tag", Typ: types.TypeInt, IsPtr: false},
		Expr: NewLitInt(uint64(vr.Value), false, types.TypeInt),
	})
	for idx, sf := range vr.Fields {
		var fExpr any
		if valName, ok := providedVals[sf.Name]; ok {
			fExpr = valName
		} else if astVar != nil && idx < len(astVar.Fields) && astVar.Fields[idx].Default != nil {
			prevBM := fl.bm
			if ebm := fl.prog.EnumBodyMaps[en]; ebm != nil {
				fl.bm = ebm
			}
			fExpr = fl.lowerStructDefaultExpr(astVar.Fields[idx].Default, sf.Type)
			fl.bm = prevBM
		} else if optF, isOpt := sf.Type.(*types.Optional); isOpt {
			fl.tc.visit(optF)
			fExpr = optNoneExpr(optF)
		} else {
			fExpr = &ExprLitZero{Typ: sf.Type}
		}
		fl.emit(&InstrAssign{
			Dst: &ExprField{
				Base: &ExprField{
					Base: &ExprField{Base: NewLocalExpr(slot), FieldName: "data", Typ: types.TypeVoid, IsPtr: false},
					FieldName: vr.CName,
					Typ:       types.TypeVoid,
					IsPtr:     false,
				},
				FieldName: sf.CName,
				Typ:       sf.Type,
				IsPtr:     false,
			},
			Expr: EnsureExpr(fExpr, sf.Type),
		})
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
			valExpr = &ExprGlobal{Global: &Global{Name: capItem.OuterSym.CName, Type: capItem.Type}}
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
