package ir

import (
	"fmt"
	"math"
	"strconv"
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

func (fl *funcLowerer) lowerWhileExpr(we *ast.WhileExpr, needValue bool) string {
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
		fl.assignLocal(hasValSlot, "false")
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
			fl.storeLoopRunningResult(loopScope, iterTmp.CName)
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
			fl.emit(&InstrIf{
				Cond: "!" + hasValSlot.CName,
				Then: elseBranch,
			})
		} else {
			fl.lowerVoidExpr(we.Else)
			fl.flushStmtTemps()
		}
	}

	if resSlot != nil {
		return resSlot.CName
	}
	return ""
}

func (fl *funcLowerer) lowerForExpr(fe *ast.ForExpr, needValue bool) string {
	inf := fl.info(fe)
	exitLbl := fmt.Sprintf("_ll_exit_%d", inf.TargetID)
	contLbl := fmt.Sprintf("_ll_cont_%d", inf.TargetID)

	var colSlot *Local
	var elemTy types.Type

	if needValue && inf.Type != nil && !inf.Type.Equals(types.TypeVoid) && !inf.Type.Equals(types.TypeNever) {
		colSlot = fl.allocSlot("_ll_col", inf.Type)
		fl.tc.visit(inf.Type)
		if colSlot.MovedFlag != "" {
			fl.emit(&InstrAssign{Dst: colSlot.MovedFlag, Expr: "false"})
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
			var isEmptyCond string
			switch colSlot.Type.(type) {
			case *types.Vector:
				isEmptyCond = fmt.Sprintf("(%s.len == 0LL)", colSlot.CName)
			case *types.Set:
				isEmptyCond = fmt.Sprintf("(%s.len == 0LL)", colSlot.CName)
			case *types.Map:
				isEmptyCond = fmt.Sprintf("(%s.len == 0LL)", colSlot.CName)
			}
			if isEmptyCond != "" {
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
				fl.emit(&InstrIf{
					Cond: isEmptyCond,
					Then: elseBranch,
				})
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
		return colSlot.CName
	}
	return ""
}

func (fl *funcLowerer) emitPushToCol(colSlot *Local, itemVal string, elemTy types.Type) {
	switch ct := colSlot.Type.(type) {
	case *types.Vector:
		fl.emit(&InstrExpr{
			Expr: fmt.Sprintf("ll_vec_push__%s(&%s, %s)", ct.Mangled(), colSlot.CName, itemVal),
		})
	case *types.Set:
		fl.emit(&InstrExpr{
			Expr: fmt.Sprintf("ll_set_insert__%s(&%s, %s)", ct.Mangled(), colSlot.CName, itemVal),
		})
	case *types.Map:
		entrySlot := fl.allocSlot("_ll_entry", elemTy)
		fl.assignLocal(entrySlot, itemVal)
		fl.emit(&InstrExpr{
			Expr: fmt.Sprintf("ll_map_insert__%s(&%s, %s.ll_key, %s.ll_value)", ct.Mangled(), colSlot.CName, entrySlot.CName, entrySlot.CName),
		})
	default:
		fi := fl.lookupMethod(ct, "push")
		if fi != nil {
			fl.emit(&InstrExpr{
				Expr: fmt.Sprintf("%s(&%s, %s)", fi.CName, colSlot.CName, itemVal),
			})
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

	emitBodyIteration := func(stepInit []Instr, stepAdv []Instr, whileCond string) {
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

		fl.emit(&InstrWhile{Cond: whileCond, Body: whileBody})
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
		fl.emit(&InstrIf{
			Cond: "!" + condSlot.CName,
			Then: []Instr{&InstrGoto{Label: exitLbl}},
		})
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

		fl.emit(&InstrWhile{Cond: "true", Body: whileBody})
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
			fl.assignLocal(doneSlot, fmt.Sprintf("(%s > %s)", curIdx.CName, endIdx.CName))
		} else {
			fl.assignLocal(doneSlot, fmt.Sprintf("(%s >= %s)", curIdx.CName, endIdx.CName))
		}
		fl.flushStmtTemps()

		stepInit := []Instr{
			&InstrAssign{Dst: loc1.CName, Expr: curIdx.CName},
		}
		var stepAdv []Instr
		prim := types.UnderlyingPrimitive(loTy)
		incExpr := fl.buildArithBinary(token.Plus, loTy, prim, curIdx.CName, formatCIntLit(1, false, loTy), "", 0)
		if inclusive {
			stepAdv = []Instr{
				&InstrIf{
					Cond: fmt.Sprintf("(%s == %s)", curIdx.CName, endIdx.CName),
					Then: []Instr{&InstrAssign{Dst: doneSlot.CName, Expr: "true"}},
					Else: []Instr{&InstrAssign{Dst: curIdx.CName, Expr: incExpr}},
				},
			}
		} else {
			stepAdv = []Instr{
				&InstrAssign{Dst: curIdx.CName, Expr: incExpr},
				&InstrIf{
					Cond: fmt.Sprintf("(%s >= %s)", curIdx.CName, endIdx.CName),
					Then: []Instr{&InstrAssign{Dst: doneSlot.CName, Expr: "true"}},
				},
			}
		}
		emitBodyIteration(stepInit, stepAdv, "!"+doneSlot.CName)

	case ast.ForIter:
		rawColTy := fl.info(condOrIter).Type
		colTy := rawColTy
		isArenaRef := false
		if rf, ok := rawColTy.(*types.Ref); ok {
			colTy = rf.Elem
			isArenaRef = true
		}

		var colPtr string
		if isArenaRef {
			ptrTmp := fl.allocSlot("_ll_colp", rawColTy)
			fl.assignLocal(ptrTmp, fl.lowerExprRead(condOrIter))
			colPtr = ptrTmp.CName
		} else if fl.isAddressable(condOrIter) {
			ptrTmp := fl.allocSlot("_ll_colp", &types.Ptr{Elem: colTy})
			fl.assignLocal(ptrTmp, fl.lowerLValueAddr(condOrIter))
			colPtr = ptrTmp.CName
		} else {
			colHoldSlot = fl.allocSlot("_ll_colhold", colTy)
			fl.assignLocal(colHoldSlot, fl.lowerRawExpr(condOrIter, colTy, true))
			colPtr = "&" + colHoldSlot.CName
		}
		fl.flushStmtTemps()

		switch c := colTy.(type) {
		case *types.Vector:
			idxVar := fl.allocSlot("_ll_vi", types.TypeInt)
			fl.assignLocal(idxVar, "0LL")
			var stepInit []Instr
			elemAccess := fmt.Sprintf("(%s)->data[%s]", colPtr, idxVar.CName)
			if loc2 == nil {
				if loc1.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: loc1.CName, Expr: "&(" + elemAccess + ")"})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: loc1.CName, Expr: elemAccess})
				}
			} else {
				stepInit = append(stepInit, &InstrAssign{Dst: loc1.CName, Expr: idxVar.CName})
				if loc2.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: loc2.CName, Expr: "&(" + elemAccess + ")"})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: loc2.CName, Expr: elemAccess})
				}
			}
			stepAdv := []Instr{&InstrAssign{Dst: idxVar.CName, Expr: idxVar.CName + " + 1LL"}}
			emitBodyIteration(stepInit, stepAdv, fmt.Sprintf("(%s < (%s)->len)", idxVar.CName, colPtr))

		case *types.StackArray:
			idxVar := fl.allocSlot("_ll_ai", types.TypeInt)
			fl.assignLocal(idxVar, "0LL")
			var stepInit []Instr
			elemAccess := fmt.Sprintf("(%s)->data[%s]", colPtr, idxVar.CName)
			if loc2 == nil {
				if loc1.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: loc1.CName, Expr: "&(" + elemAccess + ")"})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: loc1.CName, Expr: elemAccess})
				}
			} else {
				stepInit = append(stepInit, &InstrAssign{Dst: loc1.CName, Expr: idxVar.CName})
				if loc2.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: loc2.CName, Expr: "&(" + elemAccess + ")"})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: loc2.CName, Expr: elemAccess})
				}
			}
			stepAdv := []Instr{&InstrAssign{Dst: idxVar.CName, Expr: idxVar.CName + " + 1LL"}}
			emitBodyIteration(stepInit, stepAdv, fmt.Sprintf("(%s < %dLL)", idxVar.CName, c.Size))

		case *types.Primitive: // `string` UTF-8 code point iteration!
			byteOff := fl.allocSlot("_ll_soff", types.TypeInt)
			nextOff := fl.allocSlot("_ll_snext", types.TypeInt)
			chSlot := fl.allocSlot("_ll_sch", types.TypeChar)
			fl.assignLocal(byteOff, "0LL")
			var stepInit []Instr
			stepInit = append(stepInit, &InstrAssign{
				Dst:  chSlot.CName,
				Expr: fmt.Sprintf("ll_str_utf8_next(*(%s), %s, &%s)", colPtr, byteOff.CName, nextOff.CName),
			})
			if loc2 == nil {
				stepInit = append(stepInit, &InstrAssign{Dst: loc1.CName, Expr: chSlot.CName})
			} else {
				stepInit = append(stepInit, &InstrAssign{Dst: loc1.CName, Expr: byteOff.CName})
				stepInit = append(stepInit, &InstrAssign{Dst: loc2.CName, Expr: chSlot.CName})
			}
			stepAdv := []Instr{&InstrAssign{Dst: byteOff.CName, Expr: nextOff.CName}}
			emitBodyIteration(stepInit, stepAdv, fmt.Sprintf("(%s < (%s)->len)", byteOff.CName, colPtr))

		case *types.Set:
			idxVar := fl.allocSlot("_ll_si", types.TypeInt)
			fl.assignLocal(idxVar, "0LL")
			fl.usedLabels[contLbl] = true
			stepInit := []Instr{
				&InstrIf{
					Cond: fmt.Sprintf("!(%s)->entries[%s].alive", colPtr, idxVar.CName),
					Then: []Instr{&InstrGoto{Label: contLbl}},
				},
				&InstrAssign{
					Dst:  loc1.CName,
					Expr: fmt.Sprintf("(%s)->entries[%s].key", colPtr, idxVar.CName),
				},
			}
			stepAdv := []Instr{&InstrAssign{Dst: idxVar.CName, Expr: idxVar.CName + " + 1LL"}}
			emitBodyIteration(stepInit, stepAdv, fmt.Sprintf("(%s < (%s)->ent_len)", idxVar.CName, colPtr))

		case *types.Map:
			idxVar := fl.allocSlot("_ll_mi", types.TypeInt)
			fl.assignLocal(idxVar, "0LL")
			fl.usedLabels[contLbl] = true
			keyAccess := fmt.Sprintf("(%s)->entries[%s].key", colPtr, idxVar.CName)
			valAccess := fmt.Sprintf("(%s)->entries[%s].val", colPtr, idxVar.CName)
			var stepInit []Instr
			stepInit = append(stepInit, &InstrIf{
				Cond: fmt.Sprintf("!(%s)->entries[%s].alive", colPtr, idxVar.CName),
				Then: []Instr{&InstrGoto{Label: contLbl}},
			})
			if loc2 == nil {
				fl.tc.visit(loc1.Type)
				stepInit = append(stepInit, &InstrAssign{
					Dst:  loc1.CName,
					Expr: fmt.Sprintf("(%s){ .ll_key = %s, .ll_value = %s }", CType(loc1.Type), keyAccess, valAccess),
				})
			} else {
				stepInit = append(stepInit, &InstrAssign{
					Dst:  loc1.CName,
					Expr: keyAccess,
				})
				if loc2.IsMutPtr {
					stepInit = append(stepInit, &InstrAssign{Dst: loc2.CName, Expr: "&(" + valAccess + ")"})
				} else {
					stepInit = append(stepInit, &InstrAssign{Dst: loc2.CName, Expr: valAccess})
				}
			}
			stepAdv := []Instr{&InstrAssign{Dst: idxVar.CName, Expr: idxVar.CName + " + 1LL"}}
			emitBodyIteration(stepInit, stepAdv, fmt.Sprintf("(%s < (%s)->ent_len)", idxVar.CName, colPtr))
		}
	}
}

func (fl *funcLowerer) lowerMatchExpr(me *ast.MatchExpr, needValue bool) string {
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

	var scrutPtrExpr string
	var scrutHoldSlot *Local
	if isArenaRefScrut {
		scrutPtrSlot := fl.allocSlot("_ll_scrutp", rawScrutTy)
		fl.assignLocal(scrutPtrSlot, fl.lowerExprRead(me.Scrutinee))
		scrutPtrExpr = scrutPtrSlot.CName
	} else if !fl.info(me.Scrutinee).WrapOptional && fl.isAddressable(me.Scrutinee) {
		scrutPtrSlot := fl.allocSlot("_ll_scrutp", &types.Ptr{Elem: scrutTy})
		fl.assignLocal(scrutPtrSlot, fl.lowerLValueAddr(me.Scrutinee))
		scrutPtrExpr = scrutPtrSlot.CName
	} else {
		scrutHoldSlot = fl.allocSlot("_ll_scrut", scrutTy)
		fl.assignLocal(scrutHoldSlot, fl.lowerExprOwned(me.Scrutinee, scrutTy))
		scrutPtrExpr = "&" + scrutHoldSlot.CName
	}
	fl.flushStmtTemps()
	scrutValExpr := fmt.Sprintf("(*%s)", scrutPtrExpr)

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
		var condParts []string
		for _, pat := range arm.Patterns {
			switch p := pat.(type) {
			case *ast.ExprPattern:
				if _, isNone := p.Expr.(*ast.NoneLit); isNone {
					optScrut := scrutTy.(*types.Optional)
					condParts = append(condParts, "!"+optHasValueExpr(scrutValExpr, optScrut))
				} else if en, isEn := scrutTy.(*types.Enum); isEn && en.IsTagged && fl.info(p.Expr).EnumVariant != nil {
					vr := fl.info(p.Expr).EnumVariant
					condParts = append(condParts, fmt.Sprintf("((%s)->tag == %d)", scrutPtrExpr, vr.Value))
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
				if p.Inclusive {
					condParts = append(condParts, fmt.Sprintf("(%s >= %s && %s <= %s)", scrutValExpr, loExpr, scrutValExpr, hiExpr))
				} else {
					condParts = append(condParts, fmt.Sprintf("(%s >= %s && %s < %s)", scrutValExpr, loExpr, scrutValExpr, hiExpr))
				}
			case *ast.VariantPattern:
				mem := p.Target.(*ast.MemberExpr)
				if vr := fl.info(mem).EnumVariant; vr != nil {
					condParts = append(condParts, fmt.Sprintf("((%s)->tag == %d)", scrutPtrExpr, vr.Value))
				}
			}
		}
		fl.flushStmtTemps()
		patSetup := fl.popInstrBuf()

		condStr := strings.Join(condParts, " || ")
		if condStr == "" {
			condStr = "false"
		}
		restChain := buildArmChain(armIdx + 1)
		return append(patSetup, &InstrIf{
			Cond: condStr,
			Then: armBody,
			Else: restChain,
		})
	}

	chain := buildArmChain(0)
	for _, ins := range chain {
		fl.emit(ins)
	}
	fl.popScopeEmitDrops()
	fl.emit(&InstrLabel{Name: exitLbl})

	if resSlot != nil {
		return resSlot.CName
	}
	return ""
}

func (fl *funcLowerer) emitVariantPatternBindings(
	vp *ast.VariantPattern,
	scrutTy types.Type,
	scrutPtrExpr string,
	scrutValExpr string,
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
		}
		fl.locals = append(fl.locals, loc)
		fl.symLocals[sym] = loc

		if loc.IsMutPtr {
			fl.emit(&InstrAssign{
				Dst:  loc.CName,
				Expr: fmt.Sprintf("&((%s)->data.%s)", scrutPtrExpr, vr.CName),
			})
		} else if hasOwnRes {
			fl.assignLocal(loc, fmt.Sprintf("(%s).data.%s", scrutValExpr, vr.CName))
			fl.emit(&InstrAssign{Dst: scrutValExpr, Expr: fmt.Sprintf("(%s){0}", CType(en))})
			armScope.ownedLocals = append(armScope.ownedLocals, loc)
		} else {
			fl.assignLocal(loc, fmt.Sprintf("(%s).data.%s", scrutValExpr, vr.CName))
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
			if loc.IsMutPtr {
				fl.emit(&InstrAssign{
					Dst:  loc.CName,
					Expr: fmt.Sprintf("&((%s)->data.%s.%s)", scrutPtrExpr, vr.CName, sf.CName),
				})
			} else {
				fl.assignLocal(loc, fmt.Sprintf("(%s).data.%s.%s", scrutValExpr, vr.CName, sf.CName))
			}
		}
		return
	}

	// Destructive `own` move out of struct variant: extract bound fields, drop unbound/wildcard Resource fields, then zero `*scrutPtr`!
	for _, sf := range vr.Fields {
		if bIdx, isBound := boundByField[sf.Name]; isBound && bIdx < len(syms) && syms[bIdx] != nil {
			sym := syms[bIdx]
			fl.tc.visit(sym.Type)
			loc := &Local{
				CName: sym.CName,
				Type:  sym.Type,
			}
			if !sf.Type.IsPlain() && NeedsMovedFlag(sym.Type) {
				loc.MovedFlag = sym.CName + "_moved"
			}
			fl.locals = append(fl.locals, loc)
			fl.symLocals[sym] = loc
			fl.assignLocal(loc, fmt.Sprintf("(%s).data.%s.%s", scrutValExpr, vr.CName, sf.CName))
			if !sf.Type.IsPlain() {
				armScope.ownedLocals = append(armScope.ownedLocals, loc)
			}
		} else if !sf.Type.IsPlain() {
			fl.emit(&InstrExpr{
				Expr: fmt.Sprintf("%s(&((%s)->data.%s.%s))", DropFnName(sf.Type), scrutPtrExpr, vr.CName, sf.CName),
			})
		}
	}
	fl.emit(&InstrAssign{Dst: scrutValExpr, Expr: fmt.Sprintf("(%s){0}", CType(en))})
}

// -----------------------------------------------------------------------------
// Identifiers, Operators, Member Access, Indexing, & Slicing
// -----------------------------------------------------------------------------

func (fl *funcLowerer) symbolAddr(sym *sema.Symbol) string {
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

func (fl *funcLowerer) lowerIdentExpr(v *ast.IdentExpr, inf *sema.ExprInfo) string {
	sym := inf.Sym
	if sym == nil {
		return types.SanitizeCIdent(v.Name)
	}
	// Static top-level function referenced as a first-class closure value (`let op = static_add`)!
	if sym.FuncInst != nil {
		fnTy := sym.FuncInst.Sig
		thunk := fl.registerThunk(sym.FuncInst, fnTy)
		return fmt.Sprintf("(%s){ .fn = %s, .env = NULL, .copy_env = NULL, .drop_env = NULL }",
			CType(fnTy), thunk)
	}
	if capField, ok := fl.captureFields[sym]; ok {
		return capField
	}
	if loc, ok := fl.symLocals[sym]; ok {
		if loc.IsMutPtr {
			return fmt.Sprintf("(*%s)", loc.CName)
		}
		return loc.CName
	}
	return sym.CName
}

func (fl *funcLowerer) lowerUnaryExpr(u *ast.UnaryExpr, inf *sema.ExprInfo) string {
	switch u.Op {
	case token.Minus:
		if lit, isLit := u.Operand.(*ast.IntLit); isLit {
			return formatCIntLit(lit.Val, true, inf.Type)
		}
		sub := fl.lowerExprRead(u.Operand)
		prim := types.UnderlyingPrimitive(inf.Type)
		if prim != nil && prim.IsFloat() {
			return fmt.Sprintf("(-(%s))", sub)
		}
		// Two's-complement negation without C signed-overflow UB (`-INT_MIN`)!
		uType := unsignedCTypeForWidth(prim.BitWidth())
		return fmt.Sprintf("((%s)((%s)0U - (%s)(%s)))", CType(inf.Type), uType, uType, sub)

	case token.KwNot:
		sub := fl.lowerExprRead(u.Operand)
		return fmt.Sprintf("(!(%s))", sub)

	case token.Tilde:
		sub := fl.lowerExprRead(u.Operand)
		return fmt.Sprintf("((%s)(~(%s)))", CType(inf.Type), sub)

	case token.Star:
		ptr := fl.lowerExprRead(u.Operand)
		return fmt.Sprintf("(*(%s))", ptr)

	case token.KwOwn:
		return fl.lowerRawExpr(u.Operand, inf.Type, true)
	}
	return ""
}

func (fl *funcLowerer) lowerBinaryExpr(b *ast.BinaryExpr, inf *sema.ExprInfo, forOwned bool) string {
	// 1. Short-circuiting `and` / `or`
	if b.Op == token.KwAnd {
		res := fl.allocSlot("_ll_and", types.TypeBool)
		lhs := fl.lowerExprRead(b.Left)
		fl.assignLocal(res, lhs)
		fl.pushInstrBuf()
		rhs := fl.lowerExprRead(b.Right)
		fl.assignLocal(res, rhs)
		thenBuf := fl.popInstrBuf()
		fl.emit(&InstrIf{Cond: res.CName, Then: thenBuf})
		return res.CName
	}
	if b.Op == token.KwOr {
		res := fl.allocSlot("_ll_or", types.TypeBool)
		lhs := fl.lowerExprRead(b.Left)
		fl.assignLocal(res, lhs)
		fl.pushInstrBuf()
		rhs := fl.lowerExprRead(b.Right)
		fl.assignLocal(res, rhs)
		elseBuf := fl.popInstrBuf()
		fl.emit(&InstrIf{Cond: "!" + res.CName, Then: elseBuf})
		return res.CName
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
			fl.assignLocal(resSlot, optSlot.CName)
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

		fl.emit(&InstrIf{
			Cond: optHasValueExpr(optSlot.CName, optTy),
			Then: thenBuf,
			Else: elseBuf,
		})
		_ = forOwned
		return resSlot.CName
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
			return "(!" + hasV + ")"
		}
		if _, lNone := b.Left.(*ast.NoneLit); lNone {
			optR := fl.info(b.Right).Type.(*types.Optional)
			rhs := fl.lowerExprRead(b.Right)
			hasV := optHasValueExpr(rhs, optR)
			if isNeq {
				return hasV
			}
			return "(!" + hasV + ")"
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
		opStr := b.Op.String()
		if p := types.UnderlyingPrimitive(lty); p != nil {
			if p.Kind == types.PrimString {
				return fmt.Sprintf("(ll_str_cmp((ll_string)(%s), (ll_string)(%s)) %s 0)", lhs, rhs, opStr)
			}
			return fmt.Sprintf("((%s) %s (%s))", lhs, opStr, rhs)
		}
		if custom := fl.prog.LookupConcreteMethod(lty, "cmp"); custom != nil {
			return fmt.Sprintf("(%s(%s, %s) %s 0)", custom.CName, lhs, rhs, opStr)
		}
		if d, isDist := lty.(*types.Distinct); isDist {
			cBase := CType(d.Base)
			if customBase := fl.prog.LookupConcreteMethod(d.Base, "cmp"); customBase != nil {
				return fmt.Sprintf("(%s((%s)(%s), (%s)(%s)) %s 0)", customBase.CName, cBase, lhs, cBase, rhs, opStr)
			}
			if p := types.UnderlyingPrimitive(d.Base); p != nil {
				if p.Kind == types.PrimString {
					return fmt.Sprintf("(ll_str_cmp((ll_string)(%s), (ll_string)(%s)) %s 0)", lhs, rhs, opStr)
				}
				return fmt.Sprintf("(((%s)(%s)) %s ((%s)(%s)))", cBase, lhs, opStr, cBase, rhs)
			}
		}
		return fmt.Sprintf("(%s(%s, %s) %s 0)", CmpFnName(lty), lhs, rhs, opStr)
	}

	// 5. Shifts `<<` / `>>`
	file, line := formatSpanLoc(b)
	if b.Op == token.Shl || b.Op == token.Shr {
		lhs := fl.lowerExprRead(b.Left)
		rhs := fl.lowerExprRead(b.Right)
		prim := types.UnderlyingPrimitive(inf.Type)
		return fl.buildShiftExpr(b.Op, inf.Type, prim, lhs, rhs, file, line)
	}

	// 6. String concatenation `+`
	if b.Op == token.Plus && inf.Type.Equals(types.TypeString) {
		lhs := fl.lowerExprRead(b.Left)
		rhs := fl.lowerExprRead(b.Right)
		return fmt.Sprintf("ll_str_concat(%s, %s)", lhs, rhs)
	}

	// 7. Numeric Arithmetic & Bitwise (`+`, `-`, `*`, `/`, `%`, `&`, `|`, `^`)
	lhs := fl.lowerExprRead(b.Left)
	rhs := fl.lowerExprRead(b.Right)
	prim := types.UnderlyingPrimitive(inf.Type)
	switch b.Op {
	case token.Plus, token.Minus, token.Star, token.Slash, token.Percent:
		return fl.buildArithBinary(b.Op, inf.Type, prim, lhs, rhs, file, line)
	case token.Amp:
		return fmt.Sprintf("((%s)((%s) & (%s)))", CType(inf.Type), lhs, rhs)
	case token.Pipe:
		return fmt.Sprintf("((%s)((%s) | (%s)))", CType(inf.Type), lhs, rhs)
	case token.Caret:
		return fmt.Sprintf("((%s)((%s) ^ (%s)))", CType(inf.Type), lhs, rhs)
	}
	return ""
}

func (fl *funcLowerer) buildEqualityExpr(ty types.Type, lhs, rhs string, isNeq bool) string {
	fl.tc.visit(ty)
	if p := types.UnderlyingPrimitive(ty); p != nil {
		var eq string
		if p.Kind == types.PrimString {
			eq = fmt.Sprintf("ll_str_eq(%s, %s)", lhs, rhs)
		} else {
			eq = fmt.Sprintf("((%s) == (%s))", lhs, rhs)
		}
		if isNeq {
			return "!" + eq
		}
		return eq
	}

	if custom := fl.prog.LookupConcreteMethod(ty, "eq"); custom != nil {
		if isNeq {
			return fmt.Sprintf("(!%s(%s, %s))", custom.CName, lhs, rhs)
		}
		return fmt.Sprintf("%s(%s, %s)", custom.CName, lhs, rhs)
	}
	if customCmp := fl.prog.LookupConcreteMethod(ty, "cmp"); customCmp != nil {
		if isNeq {
			return fmt.Sprintf("(%s(%s, %s) != 0)", customCmp.CName, lhs, rhs)
		}
		return fmt.Sprintf("(%s(%s, %s) == 0)", customCmp.CName, lhs, rhs)
	}

	var eq string
	switch v := ty.(type) {
	case *types.Primitive:
		if v.Kind == types.PrimString {
			eq = fmt.Sprintf("ll_str_eq(%s, %s)", lhs, rhs)
		} else {
			eq = fmt.Sprintf("((%s) == (%s))", lhs, rhs)
		}
	case *types.Enum:
		if v.IsTagged {
			eq = fmt.Sprintf("%s(%s, %s)", EqFnName(ty), lhs, rhs)
		} else {
			eq = fmt.Sprintf("((%s) == (%s))", lhs, rhs)
		}
	case *types.Ptr, *types.Ref:
		eq = fmt.Sprintf("((%s) == (%s))", lhs, rhs)
	case *types.Distinct:
		return fl.buildEqualityExpr(v.Base, fmt.Sprintf("((%s)(%s))", CType(v.Base), lhs), fmt.Sprintf("((%s)(%s))", CType(v.Base), rhs), isNeq)
	case *types.Struct, *types.Optional:
		eq = fmt.Sprintf("%s(%s, %s)", EqFnName(ty), lhs, rhs)
	default:
		eq = fmt.Sprintf("((%s) == (%s))", lhs, rhs)
	}
	if isNeq {
		return "(!" + eq + ")"
	}
	return eq
}

func (fl *funcLowerer) buildArithBinary(op token.Kind, resTy types.Type, prim *types.Primitive, lhs, rhs, file string, line int) string {
	cRes := CType(resTy)
	if prim != nil && prim.IsFloat() {
		switch op {
		case token.Plus:
			return fmt.Sprintf("((%s)((%s) + (%s)))", cRes, lhs, rhs)
		case token.Minus:
			return fmt.Sprintf("((%s)((%s) - (%s)))", cRes, lhs, rhs)
		case token.Star:
			return fmt.Sprintf("((%s)((%s) * (%s)))", cRes, lhs, rhs)
		case token.Slash:
			return fmt.Sprintf("((%s)((%s) / (%s)))", cRes, lhs, rhs)
		case token.Percent:
			return fmt.Sprintf("((%s)fmod((double)(%s), (double)(%s)))", cRes, lhs, rhs)
		}
	}

	// Integer operations: two's-complement wrapping for `+`, `-`, `*`; div-by-zero & INT_MIN/-1 safe `/` and `%`!
	bits := 64
	if prim != nil {
		bits = prim.BitWidth()
	}
	uType := unsignedCTypeForWidth(bits)
	switch op {
	case token.Plus:
		return fmt.Sprintf("((%s)((%s)(%s) + (%s)(%s)))", cRes, uType, lhs, uType, rhs)
	case token.Minus:
		return fmt.Sprintf("((%s)((%s)(%s) - (%s)(%s)))", cRes, uType, lhs, uType, rhs)
	case token.Star:
		return fmt.Sprintf("((%s)((%s)(%s) * (%s)(%s)))", cRes, uType, lhs, uType, rhs)
	case token.Slash:
		if prim != nil && prim.IsUnsignedInt() {
			return fmt.Sprintf("((%s)ll_div_u64((uint64_t)(%s), (uint64_t)(%s), %q, %d))", cRes, lhs, rhs, file, line)
		}
		return fmt.Sprintf("((%s)ll_div_i%d((int%d_t)(%s), (int%d_t)(%s), %q, %d))", cRes, bits, bits, lhs, bits, rhs, file, line)
	case token.Percent:
		if prim != nil && prim.IsUnsignedInt() {
			return fmt.Sprintf("((%s)ll_mod_u64((uint64_t)(%s), (uint64_t)(%s), %q, %d))", cRes, lhs, rhs, file, line)
		}
		return fmt.Sprintf("((%s)ll_mod_i%d((int%d_t)(%s), (int%d_t)(%s), %q, %d))", cRes, bits, bits, lhs, bits, rhs, file, line)
	}
	return lhs
}

func (fl *funcLowerer) buildShiftExpr(op token.Kind, resTy types.Type, prim *types.Primitive, lhs, rhs, file string, line int) string {
	cRes := CType(resTy)
	bits := 64
	if prim != nil {
		bits = prim.BitWidth()
	}
	if op == token.Shl {
		return fmt.Sprintf("((%s)ll_shl_u%d((uint%d_t)(%s), (int64_t)(%s), 1, %q, %d))",
			cRes, bits, bits, lhs, rhs, file, line)
	}
	if prim != nil && prim.IsUnsignedInt() {
		return fmt.Sprintf("((%s)ll_shr_u%d((uint%d_t)(%s), (int64_t)(%s), 1, %q, %d))",
			cRes, bits, bits, lhs, rhs, file, line)
	}
	return fmt.Sprintf("((%s)ll_shr_i%d((int%d_t)(%s), (int64_t)(%s), 1, %q, %d))",
		cRes, bits, bits, lhs, rhs, file, line)
}

func (fl *funcLowerer) lowerMemberExpr(m *ast.MemberExpr, inf *sema.ExprInfo, forOwned bool) string {
	// 1. Enum Variant (`Status.Pending`, `Result[int, string].Empty`, or `mod.Status.Pending`)
	if inf.EnumVariant != nil && inf.EnumType != nil {
		fl.tc.visit(inf.EnumType)
		if inf.EnumType.IsTagged {
			return fmt.Sprintf("((%s){ .tag = %d })", CType(inf.EnumType), inf.EnumVariant.Value)
		}
		return fmt.Sprintf("((%s)%dLL)", CType(inf.EnumType), inf.EnumVariant.Value)
	}
	// 2. Module-qualified top-level constant or static function (`mod.PI` or `mod.fn`)
	if inf.Sym != nil {
		if inf.Sym.FuncInst != nil {
			fnTy := inf.Sym.FuncInst.Sig
			thunk := fl.registerThunk(inf.Sym.FuncInst, fnTy)
			return fmt.Sprintf("(%s){ .fn = %s, .env = NULL, .copy_env = NULL, .drop_env = NULL }",
				CType(fnTy), thunk)
		}
		return inf.Sym.CName
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
		var structAccess string
		if optBase.IsBoxed() {
			st := innerTy.(*types.Struct)
			sf, _ := st.FieldByName(m.Member)
			structAccess = fmt.Sprintf("%s->%s", baseTmp.CName, sf.CName)
			innerTy = sf.Type
		} else if rf, isRef := innerTy.(*types.Ref); isRef {
			st := rf.Elem.(*types.Struct)
			sf, _ := st.FieldByName(m.Member)
			structAccess = fmt.Sprintf("%s.value->%s", baseTmp.CName, sf.CName)
			innerTy = sf.Type
		} else {
			st := innerTy.(*types.Struct)
			sf, _ := st.FieldByName(m.Member)
			structAccess = fmt.Sprintf("%s.value.%s", baseTmp.CName, sf.CName)
			innerTy = sf.Type
		}

		// Clone Resource field when extracted into an independent `?T` result!
		fieldVal := structAccess
		if !innerTy.IsPlain() {
			fieldVal = fmt.Sprintf("%s(%s)", CopyFnName(innerTy), structAccess)
		}
		var wrapped string
		if _, alreadyOpt := innerTy.(*types.Optional); alreadyOpt {
			wrapped = fieldVal
		} else {
			wrapped = fl.wrapInOptionalExpr(fieldVal, resOpt)
		}

		fl.emit(&InstrIf{
			Cond: optHasValueExpr(baseTmp.CName, optBase),
			Then: []Instr{&InstrAssign{Dst: resSlot.CName, Expr: wrapped}},
		})
		return resSlot.CName
	}

	// 4. Direct struct / `ref[Struct]` field access
	baseTy := fl.info(m.Base).Type
	if rf, isRef := baseTy.(*types.Ref); isRef {
		st := rf.Elem.(*types.Struct)
		sf, _ := st.FieldByName(m.Member)
		ptr := fl.lowerExprRead(m.Base)
		return fmt.Sprintf("(%s->%s)", ptr, sf.CName)
	}
	st := baseTy.(*types.Struct)
	sf, _ := st.FieldByName(m.Member)
	baseExpr := fl.lowerExprRead(m.Base)
	_ = forOwned
	return fmt.Sprintf("((%s).%s)", baseExpr, sf.CName)
}

func (fl *funcLowerer) lowerIndexExpr(idx *ast.IndexExpr, inf *sema.ExprInfo, _ bool) string {
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
			fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, pTmp.CName+"->len", file, line))
			return fmt.Sprintf("(%s->data[%s])", pTmp.CName, iTmp.CName)
		}
		if fl.isAddressable(idx.Base) {
			bPtr := fl.lowerLValueAddr(idx.Base)
			pTmp := fl.allocSlot("_ll_vp", &types.Ptr{Elem: v})
			fl.assignLocal(pTmp, bPtr)
			iTmp := fl.allocSlot("_ll_vi", types.TypeInt)
			fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, pTmp.CName+"->len", file, line))
			return fmt.Sprintf("(%s->data[%s])", pTmp.CName, iTmp.CName)
		}
		bVal := fl.lowerExprRead(idx.Base)
		bTmp := fl.allocSlot("_ll_vv", v)
		fl.assignLocal(bTmp, bVal)
		iTmp := fl.allocSlot("_ll_vi", types.TypeInt)
		fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, bTmp.CName+".len", file, line))
		return fmt.Sprintf("(%s.data[%s])", bTmp.CName, iTmp.CName)

	case *types.StackArray:
		iTmp := fl.allocSlot("_ll_ai", types.TypeInt)
		fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, fmt.Sprintf("%dLL", v.Size), file, line))
		if isArenaRef {
			ptr := fl.lowerExprRead(idx.Base)
			return fmt.Sprintf("((%s)->data[%s])", ptr, iTmp.CName)
		}
		if fl.isAddressable(idx.Base) {
			bPtr := fl.lowerLValueAddr(idx.Base)
			return fmt.Sprintf("((%s)->data[%s])", bPtr, iTmp.CName)
		}
		bVal := fl.lowerExprRead(idx.Base)
		return fmt.Sprintf("((%s).data[%s])", bVal, iTmp.CName)

	case *types.Primitive: // `string` O(1) byte index `s[i] -> u8`
		bVal := fl.lowerExprRead(idx.Base)
		sTmp := fl.allocSlot("_ll_sv", types.TypeString)
		fl.assignLocal(sTmp, bVal)
		iTmp := fl.allocSlot("_ll_si", types.TypeInt)
		fl.assignLocal(iTmp, fl.checkedIndexExpr(idxVal, sTmp.CName+".len", file, line))
		return fmt.Sprintf("((uint8_t)%s.data[%s])", sTmp.CName, iTmp.CName)

	case *types.Map:
		fl.tc.visit(v)
		fl.tc.visit(inf.Type)
		var mPtr string
		if isArenaRef {
			mPtr = fl.lowerExprRead(idx.Base)
		} else if fl.isAddressable(idx.Base) {
			mPtr = fl.lowerLValueAddr(idx.Base)
		} else {
			bVal := fl.lowerExprRead(idx.Base)
			mTmp := fl.allocSlot("_ll_mv", v)
			fl.assignLocal(mTmp, bVal)
			mPtr = "&" + mTmp.CName
		}
		return fmt.Sprintf("ll_map_get__%s(%s, %s)", v.Mangled(), mPtr, idxVal)

	case *types.Ptr:
		ptr := fl.lowerExprRead(idx.Base)
		return fmt.Sprintf("((%s)[%s])", ptr, idxVal)
	}
	return ""
}

func (fl *funcLowerer) lowerSliceExpr(sl *ast.SliceExpr, inf *sema.ExprInfo) string {
	baseTy := fl.info(sl.Base).Type
	loVal := fl.lowerExprRead(sl.Low)
	hiVal := fl.lowerExprRead(sl.High)
	if sl.Inclusive {
		hiVal = fmt.Sprintf("((%s) + 1LL)", hiVal)
	}
	file, line := formatSpanLoc(sl)
	check := 1

	switch v := baseTy.(type) {
	case *types.Primitive: // `string`
		sVal := fl.lowerExprRead(sl.Base)
		if inf.BorrowAsView {
			return fmt.Sprintf("ll_str_slice_view(%s, %s, %s, %d, %q, %d)", sVal, loVal, hiVal, check, file, line)
		}
		return fmt.Sprintf("ll_str_slice_owned(%s, %s, %s, %d, %q, %d)", sVal, loVal, hiVal, check, file, line)

	case *types.Vector:
		fl.tc.visit(v)
		vVal := fl.lowerExprRead(sl.Base)
		if inf.BorrowAsView {
			return fmt.Sprintf("ll_vec_slice_view__%s(%s, %s, %s, %d, %q, %d)", v.Mangled(), vVal, loVal, hiVal, check, file, line)
		}
		return fmt.Sprintf("ll_vec_slice_owned__%s(%s, %s, %s, %d, %q, %d)", v.Mangled(), vVal, loVal, hiVal, check, file, line)

	case *types.StackArray:
		vecTy := inf.Type.(*types.Vector)
		fl.tc.visit(vecTy)
		var arrPtr string
		if fl.isAddressable(sl.Base) {
			arrPtr = fl.lowerLValueAddr(sl.Base)
		} else {
			tmp := fl.allocSlot("_ll_slarr", v)
			fl.assignLocal(tmp, fl.lowerExprRead(sl.Base))
			arrPtr = "&" + tmp.CName
		}
		viewExpr := fmt.Sprintf("(%s){ .data = (%s*)(%s)->data, .len = %dLL, .cap = 0 }",
			CType(vecTy), CType(v.Elem), arrPtr, v.Size)
		if inf.BorrowAsView {
			return fmt.Sprintf("ll_vec_slice_view__%s(%s, %s, %s, %d, %q, %d)", vecTy.Mangled(), viewExpr, loVal, hiVal, check, file, line)
		}
		return fmt.Sprintf("ll_vec_slice_owned__%s(%s, %s, %s, %d, %q, %d)", vecTy.Mangled(), viewExpr, loVal, hiVal, check, file, line)
	}
	return ""
}

// -----------------------------------------------------------------------------
// String Interpolation, Collection/Struct Literals, & Closures
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerInterpString(is *ast.InterpStringExpr) string {
	out := fl.allocSlot("_ll_interp", types.TypeString)
	fl.assignLocal(out, `ll_str_lit("", 0LL)`)

	for i, seg := range is.Segments {
		if len(seg) > 0 {
			fl.emit(&InstrExpr{
				Expr: fmt.Sprintf("ll_str_push(&%s, ll_str_lit(%s, %dLL))", out.CName, quoteCString(seg), len(seg)),
			})
		}
		if i < len(is.Exprs) {
			sub := is.Exprs[i]
			subTy := fl.info(sub).Type
			subExpr := fl.lowerExprRead(sub)
			if subTy.Equals(types.TypeString) {
				fl.emit(&InstrExpr{Expr: fmt.Sprintf("ll_str_push(&%s, %s)", out.CName, subExpr)})
			} else {
				strPart := fl.toStringCallExpr(subTy, subExpr)
				partTmp := fl.allocStmtTemp(types.TypeString, strPart)
				fl.emit(&InstrExpr{Expr: fmt.Sprintf("ll_str_push(&%s, %s)", out.CName, partTmp)})
			}
		}
	}
	return out.CName
}

func (fl *funcLowerer) toStringCallExpr(ty types.Type, valExpr string) string {
	fl.tc.visit(ty)
	// Check if user defined a custom `to_string` method on `ty`
	if custom := fl.prog.LookupConcreteMethod(ty, "to_string"); custom != nil {
		return fmt.Sprintf("%s(%s)", custom.CName, valExpr)
	}
	return fmt.Sprintf("%s(%s)", ToStrFnName(ty), valExpr)
}

func (fl *funcLowerer) copyCallExpr(ty types.Type, valExpr string) string {
	fl.tc.visit(ty)
	if custom := fl.prog.LookupConcreteMethod(ty, "copy"); custom != nil {
		return fmt.Sprintf("%s(%s)", custom.CName, valExpr)
	}
	if ty.IsPlain() {
		return valExpr
	}
	return fmt.Sprintf("%s(%s)", CopyFnName(ty), valExpr)
}

func (fl *funcLowerer) lowerVecOrArrayLit(v *ast.VecLit, inf *sema.ExprInfo) string {
	fl.tc.visit(inf.Type)
	// StackArray `[N: T]` literal: `let a: [3: int] = [1, 2, 3]`
	if arrTy, isArr := inf.Type.(*types.StackArray); isArr {
		slot := fl.allocSlot("_ll_arr", arrTy)
		for i, el := range v.Elems {
			elVal := fl.lowerExprOwned(el, arrTy.Elem)
			fl.emit(&InstrAssign{
				Dst:  fmt.Sprintf("%s.data[%d]", slot.CName, i),
				Expr: elVal,
			})
		}
		if slot.MovedFlag != "" {
			fl.emit(&InstrAssign{Dst: slot.MovedFlag, Expr: "false"})
		}
		return slot.CName
	}

	// Dynamic Vector `[T]` literal
	vecTy := inf.Type.(*types.Vector)
	if len(v.Elems) == 0 {
		return fmt.Sprintf("(%s){0}", CType(vecTy))
	}
	slot := fl.allocSlot("_ll_vec", vecTy)
	fl.emit(&InstrExpr{
		Expr: fmt.Sprintf("ll_vec_reserve__%s(&%s, %dLL)", vecTy.Mangled(), slot.CName, len(v.Elems)),
	})
	for _, el := range v.Elems {
		elVal := fl.lowerExprOwned(el, vecTy.Elem)
		fl.emit(&InstrExpr{
			Expr: fmt.Sprintf("ll_vec_push__%s(&%s, %s)", vecTy.Mangled(), slot.CName, elVal),
		})
	}
	return slot.CName
}

func (fl *funcLowerer) lowerArrayLit(r *ast.ArrayLit, inf *sema.ExprInfo) string {
	arrTy := inf.Type.(*types.StackArray)
	fl.tc.visit(arrTy)

	// Repeat constructor
	if len(r.Elems) == 1 && arrTy.Size > 1 {
		slot := fl.allocSlot("_ll_rarr", arrTy)
		valExpr := fl.lowerExprOwned(r.Elems[0], arrTy.Elem)
		valTmp := fl.allocSlot("_ll_rseed", arrTy.Elem)
		fl.assignLocal(valTmp, valExpr)

		idx := fl.allocSlot("_ll_ri", types.TypeInt)
		fl.assignLocal(idx, "0LL")
		// Copy into indices `0 .. Size-2`, and move `valTmp` into index `Size-1`!
		fl.emit(&InstrWhile{
			Cond: fmt.Sprintf("(%s < %dLL)", idx.CName, arrTy.Size-1),
			Body: []Instr{
				&InstrAssign{
					Dst:  fmt.Sprintf("%s.data[%s]", slot.CName, idx.CName),
					Expr: fl.copyCallExpr(arrTy.Elem, valTmp.CName),
				},
				&InstrAssign{Dst: idx.CName, Expr: idx.CName + " + 1LL"},
			},
		})
		fl.emit(&InstrAssign{
			Dst:  fmt.Sprintf("%s.data[%d]", slot.CName, arrTy.Size-1),
			Expr: valTmp.CName,
		})
		if slot.MovedFlag != "" {
			fl.emit(&InstrAssign{Dst: slot.MovedFlag, Expr: "false"})
		}
		return slot.CName
	}

	// Explicit elements (including single element when Size == 1)
	slot := fl.allocSlot("_ll_arr", arrTy)
	for i, el := range r.Elems {
		elVal := fl.lowerExprOwned(el, arrTy.Elem)
		fl.emit(&InstrAssign{
			Dst:  fmt.Sprintf("%s.data[%d]", slot.CName, i),
			Expr: elVal,
		})
	}
	if slot.MovedFlag != "" {
		fl.emit(&InstrAssign{Dst: slot.MovedFlag, Expr: "false"})
	}
	return slot.CName
}

func (fl *funcLowerer) lowerSetLit(s *ast.SetLit, inf *sema.ExprInfo) string {
	setTy := inf.Type.(*types.Set)
	fl.tc.visit(setTy)
	if len(s.Elems) == 0 {
		return fmt.Sprintf("(%s){0}", CType(setTy))
	}
	slot := fl.allocSlot("_ll_set", setTy)
	for _, el := range s.Elems {
		elVal := fl.lowerExprOwned(el, setTy.Elem)
		fl.emit(&InstrExpr{
			Expr: fmt.Sprintf("(void)ll_set_insert__%s(&%s, %s)", setTy.Mangled(), slot.CName, elVal),
		})
	}
	return slot.CName
}

func (fl *funcLowerer) lowerMapLit(m *ast.MapLit, inf *sema.ExprInfo) string {
	mapTy := inf.Type.(*types.Map)
	fl.tc.visit(mapTy)
	if len(m.Entries) == 0 {
		return fmt.Sprintf("(%s){0}", CType(mapTy))
	}
	slot := fl.allocSlot("_ll_map", mapTy)
	for _, ent := range m.Entries {
		kVal := fl.lowerExprOwned(ent.Key, mapTy.Key)
		vVal := fl.lowerExprOwned(ent.Value, mapTy.Value)
		fl.emit(&InstrExpr{
			Expr: fmt.Sprintf("ll_map_insert__%s(&%s, %s, %s)", mapTy.Mangled(), slot.CName, kVal, vVal),
		})
	}
	return slot.CName
}

func (fl *funcLowerer) lowerStructLit(sl *ast.StructLit, inf *sema.ExprInfo) string {
	if inf.EnumVariant != nil && inf.EnumType != nil {
		return fl.lowerEnumStructVariantLit(sl, inf)
	}
	st := inf.StructType
	fl.tc.visit(st)
	slot := fl.allocSlot("_ll_st", st)

	providedMap := make(map[string]ast.Expr, len(sl.Fields))
	// Preserve evaluation order of explicitly written fields first!
	providedVals := make(map[string]string, len(sl.Fields))
	for _, fi := range sl.Fields {
		providedMap[fi.Name] = fi.Value
		sf, _ := st.FieldByName(fi.Name)
		if sf != nil {
			valStr := fl.lowerExprOwned(fi.Value, sf.Type)
			ftmp := fl.allocSlot("_ll_sf", sf.Type)
			fl.assignLocal(ftmp, valStr)
			providedVals[fi.Name] = ftmp.CName
		}
	}

	decl := fl.prog.StructDecls[st]
	for idx, sf := range st.Fields {
		var fExpr string
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
			fExpr = fmt.Sprintf("(%s){0}", CType(sf.Type))
		}
		fl.emit(&InstrAssign{
			Dst:  fmt.Sprintf("%s.%s", slot.CName, sf.CName),
			Expr: fExpr,
		})
	}
	if slot.MovedFlag != "" {
		fl.emit(&InstrAssign{Dst: slot.MovedFlag, Expr: "false"})
	}
	return slot.CName
}

func (fl *funcLowerer) lowerEnumStructVariantLit(sl *ast.StructLit, inf *sema.ExprInfo) string {
	en := inf.EnumType
	vr := inf.EnumVariant
	fl.tc.visit(en)
	slot := fl.allocSlot("_ll_esv", en)

	// Preserve evaluation order of explicitly written fields first!
	providedVals := make(map[string]string, len(sl.Fields))
	for _, fi := range sl.Fields {
		sf, _ := vr.FieldByName(fi.Name)
		if sf != nil {
			valStr := fl.lowerExprOwned(fi.Value, sf.Type)
			ftmp := fl.allocSlot("_ll_esf", sf.Type)
			fl.assignLocal(ftmp, valStr)
			providedVals[fi.Name] = ftmp.CName
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
		Dst:  fmt.Sprintf("%s.tag", slot.CName),
		Expr: fmt.Sprintf("%d", vr.Value),
	})
	for idx, sf := range vr.Fields {
		var fExpr string
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
			fExpr = fmt.Sprintf("(%s){0}", CType(sf.Type))
		}
		fl.emit(&InstrAssign{
			Dst:  fmt.Sprintf("%s.data.%s.%s", slot.CName, vr.CName, sf.CName),
			Expr: fExpr,
		})
	}
	return slot.CName
}

func (fl *funcLowerer) lowerStructDefaultExpr(defExpr ast.Expr, fieldTy types.Type) string {
	fl.tc.visit(fieldTy)
	switch d := defExpr.(type) {
	case *ast.VecLit:
		if len(d.Elems) == 0 {
			return fmt.Sprintf("(%s){0}", CType(fieldTy))
		}
	case *ast.EmptyHashLit:
		return fmt.Sprintf("(%s){0}", CType(fieldTy))
	case *ast.NoneLit:
		if opt, ok := fieldTy.(*types.Optional); ok {
			return optNoneExpr(opt)
		}
	case *ast.IntLit:
		return formatCIntLit(d.Val, false, fieldTy)
	case *ast.FloatLit:
		return formatCFloatLit(d.Val, fieldTy)
	case *ast.BoolLit:
		if d.Val {
			return "true"
		}
		return "false"
	case *ast.StringLit:
		return fmt.Sprintf("ll_str_lit(%s, %dLL)", quoteCString(d.Val), len(d.Val))
	}
	return fl.lowerExprOwned(defExpr, fieldTy)
}

func (fl *funcLowerer) lowerFuncLitExpr(_ *ast.FuncLit, inf *sema.ExprInfo) string {
	li := inf.Lambda
	fl.tc.visit(li.Sig)
	if len(li.Captures) == 0 {
		return fmt.Sprintf("(%s){ .fn = %s_entry, .env = NULL, .copy_env = NULL, .drop_env = NULL }",
			CType(li.Sig), li.CName)
	}

	// Allocate heap environment struct & populate captured values (moving Resource captures!)
	envPtr := fmt.Sprintf("_ll_env_%d", fl.nextTempID+1)
	fl.nextTempID++
	fl.emit(&InstrExpr{
		Expr: fmt.Sprintf("%s* %s = (%s*)ll_alloc((int64_t)sizeof(%s))", li.EnvCName, envPtr, li.EnvCName, li.EnvCName),
	})
	for _, capItem := range li.Captures {
		fl.tc.visit(capItem.Type)
		var valStr string
		if capField, ok := fl.captureFields[capItem.OuterSym]; ok {
			// Nested lambda capturing from an enclosing lambda's env
			if capItem.Type.IsPlain() {
				valStr = capField
			} else {
				valStr = fl.copyCallExpr(capItem.Type, capField)
			}
		} else if loc, ok := fl.symLocals[capItem.OuterSym]; ok {
			if capItem.Moved && !capItem.Type.IsPlain() {
				mvTmp := fl.allocSlot("_ll_capmv", capItem.Type)
				fl.assignLocal(mvTmp, loc.CName)
				fl.emit(&InstrZero{Var: loc.CName, Type: loc.Type, MovedFlag: loc.MovedFlag})
				valStr = mvTmp.CName
			} else {
				if loc.IsMutPtr {
					valStr = fmt.Sprintf("(*%s)", loc.CName)
				} else {
					valStr = loc.CName
				}
			}
		} else {
			valStr = capItem.OuterSym.CName
		}
		fl.emit(&InstrAssign{
			Dst:  fmt.Sprintf("%s->%s", envPtr, capItem.CName),
			Expr: valStr,
		})
	}

	return fmt.Sprintf("(%s){ .fn = %s_entry, .env = (void*)%s, .copy_env = %s_copy, .drop_env = %s_drop }",
		CType(li.Sig), li.CName, envPtr, li.EnvCName, li.EnvCName)
}

// -----------------------------------------------------------------------------
// Optional, Interface Box, & Literal Formatting Helpers
// -----------------------------------------------------------------------------

func optNoneExpr(opt *types.Optional) string {
	if opt.IsBoxed() {
		return fmt.Sprintf("((%s)NULL)", CType(opt))
	}
	return fmt.Sprintf("(%s){ .has_value = false }", CType(opt))
}

func optHasValueExpr(optExpr string, opt *types.Optional) string {
	if opt.IsBoxed() {
		return fmt.Sprintf("((%s) != NULL)", optExpr)
	}
	return fmt.Sprintf("((%s).has_value)", optExpr)
}

func optUnwrapValExpr(optExpr string, opt *types.Optional) string {
	if opt.IsBoxed() {
		return fmt.Sprintf("(*(%s))", optExpr)
	}
	return fmt.Sprintf("((%s).value)", optExpr)
}

func optUnwrapPtrExpr(optPtrExpr string, opt *types.Optional) string {
	if opt.IsBoxed() {
		// `optPtrExpr` has type `Node**`; dereferencing once gives `Node*`!
		return fmt.Sprintf("(*(%s))", optPtrExpr)
	}
	return fmt.Sprintf("(&((%s)->value))", optPtrExpr)
}

func (fl *funcLowerer) wrapInOptionalExpr(elemExpr string, opt *types.Optional) string {
	fl.tc.visit(opt)
	if opt.IsBoxed() {
		st := opt.Elem.(*types.Struct)
		return fmt.Sprintf("%s(%s)", BoxOptFnName(st), elemExpr)
	}
	return fmt.Sprintf("(%s){ .has_value = true, .value = %s }", CType(opt), elemExpr)
}

func (fl *funcLowerer) boxConcreteIntoInterface(valExpr string, concreteTy types.Type, ifc *types.Interface) string {
	fl.tc.visit(concreteTy)
	fl.tc.visit(ifc)
	boxPtr := fmt.Sprintf("_ll_ibox_%d", fl.nextTempID+1)
	fl.nextTempID++
	cCon := CType(concreteTy)
	fl.emit(&InstrExpr{
		Expr: fmt.Sprintf("%s* %s = (%s*)ll_alloc((int64_t)sizeof(%s))", cCon, boxPtr, cCon, cCon),
	})
	fl.emit(&InstrAssign{Dst: "*" + boxPtr, Expr: valExpr})
	vtName := VTableInstanceName(concreteTy, ifc)
	return fmt.Sprintf("(%s){ .data = (void*)%s, .vtable = &%s, .owned = true }", CType(ifc), boxPtr, vtName)
}

func formatCIntLit(val uint64, negated bool, ty types.Type) string {
	cTy := CType(ty)
	prim := types.UnderlyingPrimitive(ty)
	if negated {
		if val == 9223372036854775808 {
			return fmt.Sprintf("((%s)(-9223372036854775807LL - 1LL))", cTy)
		}
		if val == 2147483648 && prim != nil && prim.Kind == types.PrimI32 {
			return fmt.Sprintf("((%s)(-2147483647 - 1))", cTy)
		}
		return fmt.Sprintf("((%s)(-%dLL))", cTy, int64(val))
	}
	if prim != nil && prim.IsUnsignedInt() {
		return fmt.Sprintf("((%s)%dULL)", cTy, val)
	}
	return fmt.Sprintf("((%s)%dLL)", cTy, val)
}

func formatCFloatLit(val float64, ty types.Type) string {
	cTy := CType(ty)
	if math.IsInf(val, 1) {
		return fmt.Sprintf("((%s)INFINITY)", cTy)
	}
	if math.IsInf(val, -1) {
		return fmt.Sprintf("((%s)(-INFINITY))", cTy)
	}
	if math.IsNaN(val) {
		return fmt.Sprintf("((%s)NAN)", cTy)
	}
	s := strconv.FormatFloat(val, 'g', 17, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return fmt.Sprintf("((%s)%s)", cTy, s)
}

func quoteCString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch b {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case 0:
			sb.WriteString(`\0`)
		default:
			if b >= 0x20 && b <= 0x7e {
				sb.WriteByte(b)
			} else {
				// Use 3-digit octal so following hex digits (`0-9a-f`) never merge into `\x...`!
				sb.WriteString(fmt.Sprintf("\\%03o", b))
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func unsignedCTypeForWidth(bits int) string {
	switch bits {
	case 8:
		return "uint8_t"
	case 16:
		return "uint16_t"
	case 32:
		return "uint32_t"
	default:
		return "uint64_t"
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
