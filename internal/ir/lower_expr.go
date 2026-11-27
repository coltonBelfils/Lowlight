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
// Expression Entry Points: Void, Owned, Read-Only, & LValue Address
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerVoidExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.UnsafeExpr:
		fl.lowerVoidExpr(v.Expr)
	case *ast.ComptimeExpr:
		if fl.bm != nil && fl.bm.ComptimeVals[v] != nil {
			return
		}
		fl.lowerVoidExpr(v.Expr)
	case *ast.BlockExpr:
		_ = fl.lowerBlockExpr(v, false)
	case *ast.StmtBodyExpr:
		fl.lowerStmt(v.Stmt)
	case *ast.IfExpr:
		_ = fl.lowerIfExpr(v, false)
	case *ast.ForExpr:
		_ = fl.lowerForExpr(v, false)
	case *ast.WhileExpr:
		_ = fl.lowerWhileExpr(v, false)
	case *ast.MatchExpr:
		_ = fl.lowerMatchExpr(v, false)
	default:
		inf := fl.info(e)
		if inf.Type == nil || inf.Type.Equals(types.TypeVoid) || inf.Type.Equals(types.TypeNever) {
			raw := fl.lowerRawExpr(e, nil, false)
			if raw != nil {
				fl.emit(&InstrExpr{Expr: raw})
			}
			return
		}
		// Expression returns a value that is discarded in statement position:
		// lower via `lowerExprRead` so any fresh Resource rvalue is registered in `stmtTemps` and dropped!
		readExpr := fl.lowerExprRead(e)
		// Ensure side-effecting Plain calls still execute if not already materialized into a temp
		if inf.Type.IsPlain() && readExpr != nil {
			fl.emit(&InstrExpr{Expr: readExpr})
		}
	}
}

// lowerExprOwned lowers `e` producing an OWNED value of `targetTy` (moving out of
// any `MovedLocal`, wrapping `T -> ?T` if `WrapOptional`, or boxing `Concrete -> Interface` if `BoxInterface`).
func (fl *funcLowerer) lowerExprOwned(e ast.Expr, targetTy types.Type) Expr {
	if e == nil {
		return nil
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
			fl.assignLocal(mvTmp, NewLocalExpr(loc))
			fl.emit(&InstrZero{Var: loc, VarName: loc.CName, Type: loc.Type, MovedFlag: loc.MovedFlag})
			return fl.applyOwnedCoercions(e, inf, NewLocalExpr(mvTmp), targetTy)
		}
	}

	unwrapped := e
	for unwrapped != nil {
		if ue, isUE := unwrapped.(*ast.UnsafeExpr); isUE {
			unwrapped = ue.Expr
			continue
		}
		if cx, isCX := unwrapped.(*ast.ComptimeExpr); isCX && (fl.bm == nil || fl.bm.ComptimeVals[cx] == nil) {
			unwrapped = cx.Expr
			continue
		}
		break
	}
	if unwrapped != e {
		if uInf := fl.info(unwrapped); uInf != nil && uInf.MovedLocal != nil {
			if loc, ok := fl.symLocals[uInf.MovedLocal]; ok {
				mvTmp := fl.allocSlot("_ll_mv", loc.Type)
				fl.assignLocal(mvTmp, NewLocalExpr(loc))
				fl.emit(&InstrZero{Var: loc, VarName: loc.CName, Type: loc.Type, MovedFlag: loc.MovedFlag})
				return fl.applyOwnedCoercions(e, inf, NewLocalExpr(mvTmp), targetTy)
			}
		}
		if u, isU := unwrapped.(*ast.UnaryExpr); isU && u.Op == token.KwOwn {
			innerVal := fl.lowerExprOwned(u.Operand, fl.info(u.Operand).Type)
			return fl.applyOwnedCoercions(e, inf, innerVal, targetTy)
		}
	}

	// Check `own self` field move (`self.field`)
	if mem, isMem := unwrapped.(*ast.MemberExpr); isMem {
		memInf := fl.info(mem)
		if memInf != nil && !memInf.IsOptChain && memInf.EnumVariant == nil && memInf.Type != nil && !memInf.Type.IsPlain() {
			mBase := fl.resolvedMemberBase(mem)
			mName := fl.resolvedMemberName(mem)
			baseExpr := fl.unwrapASTWrappers(mBase)
			if id, isId := baseExpr.(*ast.IdentExpr); isId && id.Name == "self" {
				if selfSym := fl.info(id).Sym; selfSym != nil && selfSym.BindKind == sema.BindOwned {
					if selfLoc, ok := fl.symLocals[selfSym]; ok {
						if st, isSt := types.UnwrapDistinct(selfLoc.Type).(*types.Struct); isSt {
							if sf, fIdx := st.FieldByName(mName); sf != nil {
								fieldLval := &ExprField{Base: NewLocalExpr(selfLoc), FieldName: sf.CName, FieldIdx: fIdx, Typ: sf.Type, IsPtr: selfLoc.IsMutPtr}
								mvTmp := fl.allocSlot("_ll_fmove", sf.Type)
								fl.assignLocal(mvTmp, fieldLval)
								fl.emit(&InstrAssign{Dst: fieldLval, Expr: &ExprLitZero{Typ: sf.Type}})
								return fl.applyOwnedCoercions(e, inf, NewLocalExpr(mvTmp), targetTy)
							}
						}
					}
				}
			}
		}
	}

	// Check `own self` / `own v` / rvalue generic field/element/payload move (`v.field(f)`, `t.elem(e)`, `v.payload(vr)`)
	if call, isCall := unwrapped.(*ast.CallExpr); isCall {
		callInf := fl.info(call)
		if callInf != nil && !callInf.IsOptChain && callInf.Type != nil && !callInf.Type.IsPlain() &&
			(callInf.BuiltinName == "struct.field" || callInf.BuiltinName == "seq.elem" || callInf.BuiltinName == "enum.payload" || callInf.BuiltinName == "distinct.underlying") {
			if mem, isMem := fl.resolvedCallee(call).(*ast.MemberExpr); isMem {
				mBase := fl.resolvedMemberBase(mem)
				baseExpr := fl.unwrapASTWrappers(mBase)
				var baseLoc *Local
				var baseAddrExpr Expr
				var rawBaseType types.Type
				isPackedPlace := false
				if id, isId := baseExpr.(*ast.IdentExpr); isId {
					if baseSym := fl.info(id).Sym; baseSym != nil && baseSym.BindKind == sema.BindOwned {
						baseLoc = fl.symLocals[baseSym]
						if baseLoc != nil {
							rawBaseType = baseLoc.Type
						}
					}
				} else {
					bInf := fl.info(mBase)
					if bInf == nil {
						bInf = fl.info(baseExpr)
					}
					if bInf != nil && bInf.Type != nil && bInf.Type != types.TypeInvalid {
						rawBaseType = bInf.Type
						uBase := types.UnwrapDistinct(bInf.Type)
						_, isPtr := uBase.(*types.Ptr)
						_, isRef := uBase.(*types.Ref)
						if !isPtr && !isRef {
							if fl.involvesPackedStruct(mBase) {
								if packedPlace, ok := fl.buildPackedDirectPlace(mBase); ok {
									baseAddrExpr = packedPlace
									isPackedPlace = true
								}
							} else if fl.isAddressable(mBase) {
								addr := fl.lowerLValueAddrMut(mBase)
								ptrSlot := fl.allocSlot("_ll_mbase", &types.Ptr{Elem: bInf.Type})
								fl.assignLocal(ptrSlot, EnsureExpr(addr, &types.Ptr{Elem: bInf.Type}))
								baseAddrExpr = NewLocalExpr(ptrSlot)
							} else {
								rawBase := fl.lowerExprOwned(mBase, bInf.Type)
								baseLoc = fl.allocStmtTemp(bInf.Type, rawBase)
							}
						}
					}
				}
				if baseLoc != nil || baseAddrExpr != nil {
					baseTy := types.UnwrapDistinct(rawBaseType)
					_, isPtr := baseTy.(*types.Ptr)
					_, isRef := baseTy.(*types.Ref)
					if !isPtr && !isRef {
						var basePlaceExpr Expr
						isPtrPlace := false
						if baseLoc != nil {
							basePlaceExpr = NewLocalExpr(baseLoc)
							isPtrPlace = baseLoc.IsMutPtr
						} else {
							basePlaceExpr = baseAddrExpr
							isPtrPlace = !isPackedPlace
						}
						if callInf.BuiltinName == "enum.payload" {
							if en, isEn := baseTy.(*types.Enum); isEn && callInf.EnumVariant != nil {
								baseValExpr := basePlaceExpr
								if isPtrPlace {
									baseValExpr = &ExprDeref{Ptr: basePlaceExpr, Typ: rawBaseType}
								}
								fl.emitEnumTagAssert(basePlaceExpr, en, callInf.EnumVariant, isPtrPlace, call)
								elemTy := callInf.EnumVariant.Payload
								elemLval := &ExprField{
									Base:      &ExprField{Base: basePlaceExpr, FieldName: "data", IsPtr: isPtrPlace},
									FieldName: callInf.EnumVariant.CName,
									Typ:       elemTy,
								}
								mvTmp := fl.allocSlot("_ll_pmove", elemTy)
								fl.assignLocal(mvTmp, elemLval)
								if baseLoc != nil && !baseLoc.IsMutPtr {
									fl.emit(&InstrZero{Var: baseLoc, VarName: baseLoc.CName, Type: baseLoc.Type, MovedFlag: baseLoc.MovedFlag})
								} else {
									fl.emit(&InstrAssign{Dst: baseValExpr, Expr: NewLitZero(rawBaseType)})
								}
								return fl.applyOwnedCoercions(e, inf, NewLocalExpr(mvTmp), targetTy)
							}
						}
						if callInf.BuiltinName == "distinct.underlying" {
							if dt, isDt := rawBaseType.(*types.Distinct); isDt {
								baseValExpr := basePlaceExpr
								if isPtrPlace {
									baseValExpr = &ExprDeref{Ptr: basePlaceExpr, Typ: rawBaseType}
								}
								mvTmp := fl.allocSlot("_ll_dmove", dt.Base)
								fl.assignLocal(mvTmp, &ExprCast{Val: baseValExpr, From: dt, To: dt.Base})
								if baseLoc != nil && !baseLoc.IsMutPtr {
									fl.emit(&InstrZero{Var: baseLoc, VarName: baseLoc.CName, Type: baseLoc.Type, MovedFlag: baseLoc.MovedFlag})
								} else {
									fl.emit(&InstrAssign{Dst: baseValExpr, Expr: NewLitZero(rawBaseType)})
								}
								return fl.applyOwnedCoercions(e, inf, NewLocalExpr(mvTmp), targetTy)
							}
						}
						var elemLval Expr
						var elemTy types.Type
						switch callInf.BuiltinName {
						case "struct.field":
							if st, isSt := baseTy.(*types.Struct); isSt {
								sf, fIdx := st.FieldByName(callInf.ResolvedField)
								if sf == nil && callInf.TupleFieldIdx >= 0 && callInf.TupleFieldIdx < len(st.Fields) {
									sf = &st.Fields[callInf.TupleFieldIdx]
									fIdx = callInf.TupleFieldIdx
								}
								if sf != nil {
									elemTy = sf.Type
									elemLval = &ExprField{Base: basePlaceExpr, FieldName: sf.CName, FieldIdx: fIdx, Typ: elemTy, IsPtr: isPtrPlace}
								}
							}
						case "seq.elem":
							if tup, isTup := baseTy.(*types.Tuple); isTup {
								fIdx := callInf.TupleFieldIdx
								if fIdx >= 0 && fIdx < len(tup.Elems) {
									elemTy = tup.Elems[fIdx]
									elemLval = &ExprField{Base: basePlaceExpr, FieldName: fmt.Sprintf("_%d", fIdx), FieldIdx: fIdx, Typ: elemTy, IsPtr: isPtrPlace}
								}
							} else if arr, isArr := baseTy.(*types.StackArray); isArr {
								fIdx := callInf.TupleFieldIdx
								if fIdx >= 0 && int64(fIdx) < arr.Size {
									elemTy = arr.Elem
									elemLval = &ExprIndex{
										Base:  &ExprField{Base: basePlaceExpr, FieldName: "data", Typ: &types.Ptr{Elem: elemTy}, IsPtr: isPtrPlace},
										Index: NewLitInt(uint64(fIdx), false, types.TypeInt),
										Typ:   elemTy,
									}
								}
							}
						}
						if elemLval != nil && elemTy != nil {
							mvTmp := fl.allocSlot("_ll_fmove", elemTy)
							fl.assignLocal(mvTmp, elemLval)
							fl.emit(&InstrAssign{Dst: elemLval, Expr: &ExprLitZero{Typ: elemTy}})
							return fl.applyOwnedCoercions(e, inf, NewLocalExpr(mvTmp), targetTy)
						}
					}
				}
			}
		}
	}

	raw := fl.lowerRawExpr(e, targetTy, true)
	return fl.applyOwnedCoercions(e, inf, raw, targetTy)
}

func (fl *funcLowerer) lowerStackArrayAddr(e ast.Expr, arrTy *types.StackArray) Expr {
	if fl.isAddressable(e) {
		return fl.lowerLValueAddr(e)
	}
	tmp := fl.allocSlot("_ll_arrview", arrTy)
	fl.assignLocal(tmp, fl.lowerRawExpr(e, arrTy, false))
	return &ExprAddrOf{Place: NewLocalExpr(tmp), Typ: &types.Ptr{Elem: arrTy}}
}

func (fl *funcLowerer) applyOwnedCoercions(_ ast.Expr, inf *sema.ExprInfo, valExpr Expr, targetTy types.Type) Expr {
	if inf.ToClosure != nil {
		fl.tc.visit(inf.ToClosure)
		return &ExprFnToClosure{Fn: EnsureExpr(valExpr, inf.Type), To: inf.ToClosure}
	}
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
func (fl *funcLowerer) lowerExprRead(e ast.Expr) Expr {
	if e == nil {
		return nil
	}
	if fl.exprOverrides != nil {
		if ov, ok := fl.exprOverrides[e]; ok {
			return ov
		}
	}
	inf := fl.info(e)
	fl.tc.visit(inf.Type)

	// Zero-allocation read-only views for call arguments:
	if inf.BorrowAsView {
		// 1. Concrete value passed to read-only `Interface` parameter -> zero-allocation stack fat pointer!
		if inf.BoxInterface != nil && !inf.Type.Equals(inf.BoxInterface) {
			var addr Expr
			if fl.isAddressable(e) {
				addr = fl.lowerLValueAddr(e)
			} else {
				// Materialize rvalue in a temp (dropped at statement end if Resource!)
				baseVal := fl.lowerRawExpr(e, inf.Type, false)
				tname := fl.allocStmtTemp(inf.Type, baseVal)
				addr = &ExprAddrOf{Place: NewLocalExpr(tname), Typ: &types.Ptr{Elem: inf.Type}}
			}
			return &ExprBoxInterface{
				IfcType:      inf.BoxInterface,
				ConcreteType: inf.Type,
				Value:        addr,
				Owned:        false,
			}
		}

		// 2. `[N: T]` stack array passed to read-only `[T]` or `List[T]` parameter!
		if arrTy, isArr := inf.Type.(*types.StackArray); isArr {
			vecTy := &types.Vector{Elem: arrTy.Elem}
			fl.tc.visit(vecTy)
			arrAddr := fl.lowerStackArrayAddr(e, arrTy)
			return &ExprSliceView{
				VecType: vecTy,
				DataPtr: &ExprField{Base: arrAddr, FieldName: "data", Typ: &types.Ptr{Elem: arrTy.Elem}, IsPtr: true},
				Len:     NewLitInt(uint64(arrTy.Size), false, types.TypeInt),
			}
		}

		// 2b. `Array[T]` view passed to read-only `[T]` or `List[T]` parameter!
		if arrSt, isArrSt := inf.Type.(*types.Struct); isArrSt &&
			fl.prog != nil && fl.prog.GenStructArray != nil &&
			fl.prog.StructMods[arrSt] == fl.prog.GenStructArray.Mod && len(arrSt.TypeArgs) == 1 {
			elemTy := arrSt.TypeArgs[0]
			vecTy := &types.Vector{Elem: elemTy}
			fl.tc.visit(vecTy)
			rawArr := fl.lowerRawExpr(e, arrSt, false)
			arrTmp := fl.allocSlot("_ll_arrview", arrSt)
			fl.assignLocal(arrTmp, rawArr)
			tmpExpr := NewLocalExpr(arrTmp)
			return &ExprSliceView{
				VecType: vecTy,
				DataPtr: &ExprField{Base: tmpExpr, FieldName: "data", Typ: &types.Ptr{Elem: elemTy}, IsPtr: false},
				Len:     &ExprField{Base: tmpExpr, FieldName: "len", Typ: types.TypeInt, IsPtr: false},
			}
		}

		// 3. Owned `Interface` variable passed to read-only `Interface` parameter -> unowned fat pointer copy!
		if ifc, isIfc := inf.Type.(*types.Interface); isIfc {
			raw := fl.lowerRawExpr(e, inf.Type, false)
			tmp := fl.allocSlot("_ll_ifcb", ifc)
			fl.assignLocal(tmp, raw)
			return &ExprBoxInterface{
				IfcType:      ifc,
				ConcreteType: ifc,
				Value:        NewLocalExpr(tmp),
				Owned:        false,
			}
		}
	}

	raw := fl.lowerRawExpr(e, nil, false)

	// If `e` produced a fresh owned Resource rvalue, bind it into `stmtTemps` for automatic cleanup!
	if inf.Type != nil && !inf.Type.IsPlain() && fl.isFreshResourceRValue(e, inf) {
		raw = NewLocalExpr(fl.allocStmtTemp(inf.Type, raw))
	}

	if inf.ToClosure != nil {
		fl.tc.visit(inf.ToClosure)
		return &ExprFnToClosure{Fn: EnsureExpr(raw, inf.Type), To: inf.ToClosure}
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
	if fl.bm != nil && fl.bm.ComptimeVals[e] != nil {
		return false
	}
	if inf.BorrowAsView {
		// Note: `SliceExpr` and `StringLit` with `BorrowAsView == true` have `cap = 0` (no heap alloc).
		switch e.(type) {
		case *ast.StringLit, *ast.SliceExpr:
			return false
		}
	}
	switch v := e.(type) {
	case *ast.StringLit, *ast.NoneLit, *ast.RangeExpr:
		// String literals have `cap = 0` (static storage); `none` has `has_value = false` / `NULL`; ranges are plain scalars.
		return false
	case *ast.IdentExpr:
		if inf.Sym != nil && inf.Sym.IsConstParam {
			return false
		}
		// Reading a variable or static function thunk does NOT allocate! A bare unit result
		// member (`Forbidden`) constructs a fresh value like `Lookup.Forbidden`.
		return inf.EnumVariant != nil && inf.EnumType != nil && inf.EnumType.IsTagged
	case *ast.MemberExpr:
		// Optional chaining `u?.city` clones `city` if Resource; tagged unit variant constructs a fresh enum;
		// plain `u.city` reads in-place!
		return inf.IsOptChain || (inf.EnumVariant != nil && inf.EnumType != nil && inf.EnumType.IsTagged)
	case *ast.IndexExpr:
		// `map[k]` clones `?V`, whereas `vec[i]` / `arr[i]` reads in-place!
		baseTy := fl.info(v.Base).Type
		if rf, ok := baseTy.(*types.Ref); ok {
			baseTy = rf.Elem
		}
		_, _, isMap := types.MapKV(baseTy)
		return isMap
	case *ast.UnaryExpr:
		if v.Op == token.Star {
			return false // `*p` dereferences in place
		}
		return true
	case *ast.UnsafeExpr:
		return fl.isFreshResourceRValue(v.Expr, fl.info(v.Expr))
	case *ast.ComptimeExpr:
		if fl.bm != nil && fl.bm.ComptimeVals[v] != nil {
			return false
		}
		return fl.isFreshResourceRValue(v.Expr, fl.info(v.Expr))
	case *ast.CallExpr:
		if !inf.IsOptChain && (inf.BuiltinName == "struct.field" || inf.BuiltinName == "seq.elem" || inf.BuiltinName == "enum.payload") {
			return false
		}
		return true
	default:
		// `InterpStringExpr`, `BinaryExpr` (`s1 + s2`, `??`), `CallExpr`, `SliceExpr` (non-view),
		// `VecLit`, `SetLit`, `MapLit`, `StructLit`, `FuncLit`, `BlockExpr`, `IfExpr`, `ForExpr`, `MatchExpr`
		return true
	}
}

func (fl *funcLowerer) isAddressable(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.IdentExpr:
		inf := fl.info(v)
		if inf.EnumVariant != nil || (inf.Sym != nil && inf.Sym.IsConstParam) {
			return false
		}
		return inf.Sym == nil || inf.Sym.FuncInst == nil
	case *ast.MemberExpr:
		inf := fl.info(v)
		if v.QuestDot || inf.IsOptChain || inf.EnumVariant != nil || inf.Sym != nil {
			return false
		}
		mBase := fl.resolvedMemberBase(v)
		baseTy := fl.info(mBase).Type
		if _, ok := baseTy.(*types.Ref); ok {
			return true
		}
		if _, ok := baseTy.(*types.Ptr); ok {
			return true
		}
		return fl.isAddressable(mBase)
	case *ast.IndexExpr:
		baseTy := fl.info(v.Base).Type
		if rf, ok := baseTy.(*types.Ref); ok {
			baseTy = rf.Elem
		}
		switch baseTy.(type) {
		case *types.Vector, *types.StackArray, *types.Ptr:
			return true
		default:
			// `map[k]` and `str[i]` return computed/cloned rvalues, NOT addressable places!
			return false
		}
	case *ast.UnaryExpr:
		return v.Op == token.Star
	case *ast.UnsafeExpr:
		return fl.isAddressable(v.Expr)
	case *ast.CallExpr:
		inf := fl.info(v)
		if !inf.IsOptChain && (inf.BuiltinName == "struct.field" || inf.BuiltinName == "seq.elem" || inf.BuiltinName == "enum.payload") {
			if mem, isMem := fl.resolvedCallee(v).(*ast.MemberExpr); isMem {
				mBase := fl.resolvedMemberBase(mem)
				baseTy := fl.info(mBase).Type
				if _, isRef := baseTy.(*types.Ref); isRef {
					return true
				}
				return fl.isAddressable(mBase)
			}
		}
		return false
	case *ast.ComptimeExpr:
		return fl.isAddressable(v.Expr)
	default:
		return false
	}
}

// lowerLValueAddr lowers an addressable place expression `e` to a typed pointer expression `T*`.
func (fl *funcLowerer) lowerLValueAddr(e ast.Expr) Expr {
	return fl.lowerLValueAddrMode(e, false)
}

// lowerLValueAddrMut lowers an addressable place expression for mutation (promoting any
// copy-on-write `.rodata`/view List/Vector containers along the lvalue chain).
func (fl *funcLowerer) lowerLValueAddrMut(e ast.Expr) Expr {
	return fl.lowerLValueAddrMode(e, true)
}

func (fl *funcLowerer) ensureVecCoWPtr(colTy types.Type, colPtr Expr) Expr {
	var vecTy *types.Vector
	if v, ok := colTy.(*types.Vector); ok {
		vecTy = v
	} else if st, ok := colTy.(*types.Struct); ok && (st.Lang == "list" || ((st.Name == "List" || strings.HasPrefix(st.Name, "List[")) && len(st.TypeArgs) == 1)) {
		if len(st.TypeArgs) == 1 {
			vecTy = &types.Vector{Elem: st.TypeArgs[0]}
		}
	}
	if vecTy != nil {
		fl.tc.visit(vecTy)
		ptrTy := &types.Ptr{Elem: colTy}
		vSlot := fl.allocSlot("_ll_vcow", ptrTy)
		fl.assignLocal(vSlot, EnsureExpr(colPtr, ptrTy))
		lenExpr := &ExprField{Base: NewLocalExpr(vSlot), FieldName: "len", Typ: types.TypeInt, IsPtr: true}
		fl.emit(&InstrExpr{Expr: &ExprCall{
			Callee: &ExprFuncRef{Name: "ll_vec_reserve__" + vecTy.Mangled()},
			Args:   []Expr{&ExprCast{Val: NewLocalExpr(vSlot), From: ptrTy, To: &types.Ptr{Elem: vecTy}}, lenExpr},
			Typ:    types.TypeVoid,
		}})
		return NewLocalExpr(vSlot)
	}
	return colPtr
}

func (fl *funcLowerer) lowerLValueAddrMode(e ast.Expr, mutable bool) Expr {
	inf := fl.info(e)
	switch v := e.(type) {
	case *ast.UnsafeExpr:
		return fl.lowerLValueAddrMode(v.Expr, mutable)
	case *ast.ComptimeExpr:
		return fl.lowerLValueAddrMode(v.Expr, mutable)
	case *ast.IdentExpr:
		sym := inf.Sym
		if sym != nil {
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
		cName := types.SanitizeCIdent(v.Name)
		return &ExprAddrOf{Place: &ExprGlobal{Global: &Global{Name: cName, CName: cName, Type: inf.Type}}, Typ: &types.Ptr{Elem: inf.Type}}

	case *ast.UnaryExpr:
		if v.Op == token.Star {
			return EnsureExpr(fl.lowerExprRead(v.Operand), &types.Ptr{Elem: inf.Type})
		}
		if v.Op == token.Amp {
			return fl.lowerLValueAddrMode(v.Operand, mutable)
		}

	case *ast.MemberExpr:
		mBase := fl.resolvedMemberBase(v)
		mName := fl.resolvedMemberName(v)
		baseTy := fl.info(mBase).Type
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
				ptr := fl.lowerExprRead(mBase)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true},
					Typ:   &types.Ptr{Elem: tup.Elems[fIdx]},
				}
			}
			st := rf.Elem.(*types.Struct)
			sf, _ := st.FieldByName(mName)
			ptr := fl.lowerExprRead(mBase)
			return &ExprAddrOf{
				Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: true},
				Typ:   &types.Ptr{Elem: sf.Type},
			}
		}
		if _, isVec := baseTy.(*types.Vector); isVec {
			basePtr := fl.lowerLValueAddrMode(mBase, mutable)
			return &ExprAddrOf{
				Place: &ExprField{Base: basePtr, FieldName: mName, Typ: inf.Type, IsPtr: true},
				Typ:   &types.Ptr{Elem: inf.Type},
			}
		}
		if _, isArr := baseTy.(*types.StackArray); isArr {
			basePtr := fl.lowerLValueAddrMode(mBase, mutable)
			return &ExprAddrOf{
				Place: &ExprField{Base: basePtr, FieldName: mName, Typ: inf.Type, IsPtr: true},
				Typ:   &types.Ptr{Elem: inf.Type},
			}
		}
		effectiveBase := baseTy
		if d, isDist := baseTy.(*types.Distinct); isDist {
			if dtTup, isTup := d.Base.(*types.Tuple); isTup {
				effectiveBase = dtTup
			}
		}
		if tup, isTup := effectiveBase.(*types.Tuple); isTup {
			fIdx := inf.TupleFieldIdx
			basePtr := fl.lowerLValueAddrMode(mBase, mutable)
			fName := fmt.Sprintf("_%d", fIdx)
			return &ExprAddrOf{
				Place: &ExprField{Base: basePtr, FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true},
				Typ:   &types.Ptr{Elem: tup.Elems[fIdx]},
			}
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
				ptr := fl.lowerExprRead(mBase)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true},
					Typ:   &types.Ptr{Elem: tup.Elems[fIdx]},
				}
			}
			if _, isVec := p.Elem.(*types.Vector); isVec {
				ptr := fl.lowerExprRead(mBase)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: mName, Typ: inf.Type, IsPtr: true},
					Typ:   &types.Ptr{Elem: inf.Type},
				}
			}
			if _, isArr := p.Elem.(*types.StackArray); isArr {
				ptr := fl.lowerExprRead(mBase)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: mName, Typ: inf.Type, IsPtr: true},
					Typ:   &types.Ptr{Elem: inf.Type},
				}
			}
			if st, ok := p.Elem.(*types.Struct); ok {
				sf, _ := st.FieldByName(mName)
				ptr := fl.lowerExprRead(mBase)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: true},
					Typ:   &types.Ptr{Elem: sf.Type},
				}
			}
		}
		st := baseTy.(*types.Struct)
		sf, _ := st.FieldByName(mName)
		basePtr := fl.lowerLValueAddrMode(mBase, mutable)
		return &ExprAddrOf{
			Place: &ExprField{Base: basePtr, FieldName: sf.CName, Typ: sf.Type, IsPtr: true},
			Typ:   &types.Ptr{Elem: sf.Type},
		}

	case *ast.IndexExpr:
		baseTy := fl.info(v.Base).Type
		if tup, isTup := baseTy.(*types.Tuple); isTup {
			fIdx := inf.TupleFieldIdx
			basePtr := fl.lowerLValueAddrMode(v.Base, mutable)
			fName := fmt.Sprintf("_%d", fIdx)
			return &ExprAddrOf{
				Place: &ExprField{Base: basePtr, FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true},
				Typ:   &types.Ptr{Elem: tup.Elems[fIdx]},
			}
		}
		idxExpr := fl.lowerExprRead(v.Index)
		file, line := formatSpanLoc(v)
		if rf, isRef := baseTy.(*types.Ref); isRef {
			ptr := fl.lowerExprRead(v.Base)
			if mutable {
				ptr = fl.ensureVecCoWPtr(rf.Elem, ptr)
			}
			return fl.indexElementPtr(rf.Elem, ptr, idxExpr, file, line)
		}
		if _, isPtr := baseTy.(*types.Ptr); isPtr {
			ptr := fl.lowerExprRead(v.Base)
			return &ExprBinary{Op: token.Plus, Left: EnsureExpr(ptr, baseTy), Right: EnsureExpr(idxExpr, types.TypeInt), Typ: baseTy}
		}
		basePtr := fl.lowerLValueAddrMode(v.Base, mutable)
		if mutable {
			basePtr = fl.ensureVecCoWPtr(baseTy, basePtr)
		}
		return fl.indexElementPtr(baseTy, basePtr, idxExpr, file, line)

	case *ast.CallExpr:
		if !inf.IsOptChain && (inf.BuiltinName == "struct.field" || inf.BuiltinName == "seq.elem" || inf.BuiltinName == "enum.payload") {
			if mem, isMem := fl.resolvedCallee(v).(*ast.MemberExpr); isMem {
				mBase := fl.resolvedMemberBase(mem)
				rawBaseTy := fl.info(mBase).Type
				baseTy := rawBaseTy
				isRef := false
				if rf, ok := rawBaseTy.(*types.Ref); ok {
					baseTy = rf.Elem
					isRef = true
				}
				var basePtr Expr
				if isRef {
					basePtr = fl.lowerExprRead(mBase)
				} else {
					basePtr = fl.lowerLValueAddrMode(mBase, mutable)
				}
				switch {
				case inf.BuiltinName == "struct.field":
					st := baseTy.(*types.Struct)
					sf, _ := st.FieldByName(inf.ResolvedField)
					if sf == nil && inf.TupleFieldIdx >= 0 && inf.TupleFieldIdx < len(st.Fields) {
						sf = &st.Fields[inf.TupleFieldIdx]
					}
					return &ExprAddrOf{
						Place: &ExprField{Base: EnsureExpr(basePtr, &types.Ptr{Elem: baseTy}), FieldName: sf.CName, Typ: sf.Type, IsPtr: true},
						Typ:   &types.Ptr{Elem: sf.Type},
					}
				case inf.BuiltinName == "seq.elem":
					fIdx := inf.TupleFieldIdx
					if tup, isTup := baseTy.(*types.Tuple); isTup {
						return &ExprAddrOf{
							Place: &ExprField{Base: EnsureExpr(basePtr, &types.Ptr{Elem: baseTy}), FieldName: fmt.Sprintf("_%d", fIdx), FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true},
							Typ:   &types.Ptr{Elem: tup.Elems[fIdx]},
						}
					}
					if arr, isArr := baseTy.(*types.StackArray); isArr {
						dataPtr := &ExprField{Base: EnsureExpr(basePtr, &types.Ptr{Elem: baseTy}), FieldName: "data", Typ: &types.Ptr{Elem: arr.Elem}, IsPtr: true}
						return &ExprAddrOf{
							Place: &ExprIndex{Base: dataPtr, Index: NewLitInt(uint64(fIdx), false, types.TypeInt), Typ: arr.Elem},
							Typ:   &types.Ptr{Elem: arr.Elem},
						}
					}
				case inf.BuiltinName == "enum.payload":
					en := baseTy.(*types.Enum)
					ptrTmp := fl.allocSlot("_ll_epptr", &types.Ptr{Elem: en})
					fl.assignLocal(ptrTmp, EnsureExpr(basePtr, &types.Ptr{Elem: en}))
					fl.emitEnumTagAssert(NewLocalExpr(ptrTmp), en, inf.EnumVariant, true, v)
					return &ExprAddrOf{
						Place: &ExprField{
							Base:      &ExprField{Base: NewLocalExpr(ptrTmp), FieldName: "data", IsPtr: true},
							FieldName: inf.EnumVariant.CName,
							Typ:       inf.EnumVariant.Payload,
						},
						Typ: &types.Ptr{Elem: inf.EnumVariant.Payload},
					}
				}
			}
		}
	}
	// Fallback: materialize into a temporary slot and return its address
	tmp := fl.allocSlot("_ll_lval", inf.Type)
	fl.assignLocal(tmp, fl.lowerRawExpr(e, inf.Type, false))
	return &ExprAddrOf{Place: NewLocalExpr(tmp), Typ: &types.Ptr{Elem: inf.Type}}
}

func (fl *funcLowerer) indexElementPtr(colTy types.Type, colPtr, idxExpr Expr, file string, line int) Expr {
	switch c := colTy.(type) {
	case *types.Vector:
		lenExpr := &ExprField{Base: EnsureExpr(colPtr, &types.Ptr{Elem: c}), FieldName: "len", Typ: types.TypeInt, IsPtr: true}
		checkedIdx := fl.checkedIndexExpr(idxExpr, lenExpr, file, line)
		dataPtr := &ExprField{Base: EnsureExpr(colPtr, &types.Ptr{Elem: c}), FieldName: "data", Typ: &types.Ptr{Elem: c.Elem}, IsPtr: true}
		return &ExprAddrOf{
			Place: &ExprIndex{
				Base:  dataPtr,
				Index: checkedIdx,
				Typ:   c.Elem,
			},
			Typ: &types.Ptr{Elem: c.Elem},
		}
	case *types.StackArray:
		lenExpr := NewLitInt(uint64(c.Size), false, types.TypeInt)
		checkedIdx := fl.checkedIndexExpr(idxExpr, lenExpr, file, line)
		dataPtr := &ExprField{Base: EnsureExpr(colPtr, &types.Ptr{Elem: c}), FieldName: "data", Typ: &types.Ptr{Elem: c.Elem}, IsPtr: true}
		return &ExprAddrOf{
			Place: &ExprIndex{
				Base:  dataPtr,
				Index: checkedIdx,
				Typ:   c.Elem,
			},
			Typ: &types.Ptr{Elem: c.Elem},
		}
	case *types.Struct:
		if (c.Name == "List" || strings.HasPrefix(c.Name, "List[")) && len(c.TypeArgs) == 1 {
			elem := c.TypeArgs[0]
			lenExpr := &ExprField{Base: EnsureExpr(colPtr, &types.Ptr{Elem: c}), FieldName: "len", Typ: types.TypeInt, IsPtr: true}
			checkedIdx := fl.checkedIndexExpr(idxExpr, lenExpr, file, line)
			dataPtr := &ExprField{Base: EnsureExpr(colPtr, &types.Ptr{Elem: c}), FieldName: "data", Typ: &types.Ptr{Elem: elem}, IsPtr: true}
			return &ExprAddrOf{
				Place: &ExprIndex{
					Base:  dataPtr,
					Index: checkedIdx,
					Typ:   elem,
				},
				Typ: &types.Ptr{Elem: elem},
			}
		}
		if (c.Name == "Array" || strings.HasPrefix(c.Name, "Array[")) && len(c.TypeArgs) >= 1 {
			elem := c.TypeArgs[len(c.TypeArgs)-1]
			lenExpr := &ExprField{Base: EnsureExpr(colPtr, &types.Ptr{Elem: c}), FieldName: "len", Typ: types.TypeInt, IsPtr: true}
			checkedIdx := fl.checkedIndexExpr(idxExpr, lenExpr, file, line)
			dataPtr := &ExprField{Base: EnsureExpr(colPtr, &types.Ptr{Elem: c}), FieldName: "data", Typ: &types.Ptr{Elem: elem}, IsPtr: true}
			return &ExprAddrOf{
				Place: &ExprIndex{
					Base:  dataPtr,
					Index: checkedIdx,
					Typ:   elem,
				},
				Typ: &types.Ptr{Elem: elem},
			}
		}
	}
	return EnsureExpr(colPtr, &types.Ptr{Elem: colTy})
}

func (fl *funcLowerer) checkedIndexExpr(idxExpr, lenExpr Expr, file string, line int) Expr {
	return &ExprCall{
		Callee: &ExprFuncRef{Name: "ll_check_bounds"},
		Args: []Expr{
			EnsureExpr(idxExpr, types.TypeInt),
			EnsureExpr(lenExpr, types.TypeInt),
			&ExprLitCString{Val: file},
			NewLitInt(uint64(line), false, types.TypeInt),
		},
		Typ: types.TypeInt,
	}
}

// -----------------------------------------------------------------------------
// Core Expression Dispatcher (`lowerRawExpr`)
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerRawExpr(e ast.Expr, expected types.Type, forOwned bool) Expr {
	inf := fl.info(e)
	if fl.bm != nil {
		if cv, ok := fl.bm.ComptimeVals[e]; ok && cv != nil {
			if _, isCe := e.(*ast.ComptimeExpr); !isCe {
				if sd := sema.ExportStaticData(cv, inf.Type); sd != nil {
					sv := lowerStaticData(sd, fl.tc, fl.registerThunk)
					return &ExprConst{Static: sv, Typ: inf.Type}
				}
			}
		}
	}

	switch v := e.(type) {
	case *ast.IntLit:
		return NewLitInt(v.Val, false, inf.Type)

	case *ast.FloatLit:
		return NewLitFloat(v.Val, inf.Type)

	case *ast.BoolLit:
		return NewLitBool(v.Val)

	case *ast.CharLit:
		return NewLitChar(v.Val)

	case *ast.StringLit:
		if d, isDist := inf.Type.(*types.Distinct); isDist {
			return &ExprCast{Val: &ExprLitString{Val: v.Val}, From: types.TypeString, To: d}
		}
		return &ExprLitString{Val: v.Val}

	case *ast.InterpStringExpr:
		return fl.lowerInterpString(v)

	case *ast.NoneLit:
		optTy := inf.Type.(*types.Optional)
		fl.tc.visit(optTy)
		return &ExprLitNone{OptType: optTy}

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

	case *ast.RangeExpr:
		st := inf.Type.(*types.Struct)
		fl.tc.visit(st)
		lo := fl.lowerExprRead(v.Low)
		hi := fl.lowerExprRead(v.High)
		elemTy := st.TypeArgs[0]
		return &ExprConstructStruct{
			StructType: st,
			Fields: []StructFieldInit{
				{Name: st.Fields[0].CName, Value: EnsureExpr(lo, elemTy)},
				{Name: st.Fields[1].CName, Value: EnsureExpr(hi, elemTy)},
				{Name: st.Fields[2].CName, Value: NewLitBool(v.Inclusive)},
			},
		}

	case *ast.VecLit:
		return fl.lowerVecOrArrayLit(v, inf)

	case *ast.ArrayLit:
		return fl.lowerArrayLit(v, inf)

	case *ast.SetLit:
		return fl.lowerSetLit(v, inf)

	case *ast.MapLit:
		return fl.lowerMapLit(v, inf)

	case *ast.EmptyHashLit:
		fl.tc.visit(inf.Type)
		return &ExprLitZero{Typ: inf.Type}

	case *ast.TupleLit:
		return fl.lowerTupleLit(v, inf)

	case *ast.StructLit:
		return fl.lowerStructLit(v, inf)

	case *ast.FuncLit:
		return fl.lowerFuncLitExpr(v, inf)

	case *ast.UnsafeExpr:
		return fl.lowerRawExpr(v.Expr, expected, forOwned)
	case *ast.ComptimeExpr:
		if fl.bm != nil {
			if cv, ok := fl.bm.ComptimeVals[v]; ok && cv != nil {
				if sd := sema.ExportStaticData(cv, inf.Type); sd != nil {
					sv := lowerStaticData(sd, fl.tc, fl.registerThunk)
					return &ExprConst{Static: sv, Typ: inf.Type}
				}
			}
		}
		return fl.lowerRawExpr(v.Expr, expected, forOwned)

	case *ast.BlockExpr:
		return fl.lowerBlockExpr(v, true)

	case *ast.StmtBodyExpr:
		fl.lowerStmt(v.Stmt)
		return nil

	case *ast.IfExpr:
		return fl.lowerIfExpr(v, true)

	case *ast.ForExpr:
		return fl.lowerForExpr(v, true)

	case *ast.WhileExpr:
		return fl.lowerWhileExpr(v, true)

	case *ast.MatchExpr:
		return fl.lowerMatchExpr(v, true)
	}

	_ = expected
	return nil
}

// -----------------------------------------------------------------------------
// Control-Flow Expressions: Block, If, For, Match
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerBlockExpr(b *ast.BlockExpr, needValue bool) Expr {
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
		return NewLocalExpr(resSlot)
	}
	return nil
}

func (fl *funcLowerer) lowerIfExpr(ie *ast.IfExpr, needValue bool) Expr {
	inf := fl.info(ie)
	exitLbl := fmt.Sprintf("_ll_exit_%d", inf.TargetID)

	if fl.bm != nil {
		if branch, ok := fl.bm.ComptimeIfBranch[ie]; ok {
			if branch == 3 {
				return nil
			}
			chosen := ie.Then
			if branch == 2 {
				chosen = ie.Else
			}
			var resSlot *Local
			if needValue && inf.Type != nil && !inf.Type.Equals(types.TypeVoid) && !inf.Type.Equals(types.TypeNever) {
				resSlot = fl.allocSlot("_ll_if", inf.Type)
			}
			fl.pushScope(inf.TargetID, exitLbl, "", resSlot, nil, false)
			fl.pushInstrBuf()
			fl.pushScope(0, "", "", nil, nil, false)
			if !needValue || resSlot == nil {
				fl.lowerVoidExpr(chosen)
				fl.flushStmtTemps()
			} else if cTy := fl.info(chosen).Type; cTy == types.TypeNever {
				fl.lowerVoidExpr(chosen)
				fl.flushStmtTemps()
			} else {
				cVal := fl.lowerExprOwned(chosen, resSlot.Type)
				fl.assignLocal(resSlot, cVal)
				fl.flushStmtTemps()
			}
			if !endsWithTerm(fl.curInstrs()) {
				fl.popScopeEmitDrops()
			} else {
				fl.scopes = fl.scopes[:len(fl.scopes)-1]
			}
			chosenBody := fl.popInstrBuf()
			fl.emit(&InstrBlock{Body: chosenBody})
			fl.popScopeEmitDrops()
			fl.emit(&InstrLabel{Name: exitLbl})
			if resSlot != nil {
				return NewLocalExpr(resSlot)
			}
			return nil
		}
	}

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

	var condExpr Expr
	var bindHoldSlot *Local
	unwrapSym := fl.bm.IfUnwrapSyms[ie]
	var unwrapInitInstr Instr

	switch ie.CondKind {
	case ast.IfCondBool:
		rawCond := fl.lowerExprRead(ie.Cond)
		condSlot := fl.allocSlot("_ll_ifcond", types.TypeBool)
		fl.assignLocal(condSlot, rawCond)
		condExpr = NewLocalExpr(condSlot)

	case ast.IfCondOptVar:
		origSym := fl.bm.IfOriginSyms[ie]
		optTy := origSym.Type.(*types.Optional)
		origAddr := fl.symbolAddr(origSym)
		derefVal := &ExprDeref{Ptr: origAddr, Typ: optTy}
		condExpr = optHasValueExpr(derefVal, optTy)

		if unwrapSym != nil {
			uLoc := &Local{
				CName:    unwrapSym.CName,
				Type:     unwrapSym.Type,
				IsMutPtr: ie.MutUnwrap,
			}
			fl.locals = append(fl.locals, uLoc)
			fl.symLocals[unwrapSym] = uLoc
			if ie.MutUnwrap {
				unwrapInitInstr = &InstrAssign{Dst: NewLocalExpr(uLoc), Expr: EnsureExpr(optUnwrapPtrExpr(origAddr, optTy), uLoc.Type)}
			} else {
				unwrapInitInstr = &InstrAssign{Dst: NewLocalExpr(uLoc), Expr: EnsureExpr(optUnwrapValExpr(derefVal, optTy), uLoc.Type)}
			}
		}

	case ast.IfCondOptBind:
		unwrappedCond := ie.Cond
		for unwrappedCond != nil {
			if ue, isUE := unwrappedCond.(*ast.UnsafeExpr); isUE {
				unwrappedCond = ue.Expr
				continue
			}
			if cx, isCX := unwrappedCond.(*ast.ComptimeExpr); isCX {
				unwrappedCond = cx.Expr
				continue
			}
			break
		}
		if condCall, isCall := unwrappedCond.(*ast.CallExpr); isCall {
			if cInf := fl.info(condCall); cInf != nil && !cInf.IsOptChain && cInf.BuiltinName == "enum.as_variant" {
				condExpr, bindHoldSlot, unwrapInitInstr = fl.lowerEnumVariantCondBind(ie, cInf.EnumType, cInf.EnumVariant, cInf.MethodRecv, unwrapSym)
				break
			}
		}
		if resEn := types.AsResult(fl.info(ie.Cond).Type); resEn != nil {
			vv, _ := resEn.ResultValue()
			condExpr, bindHoldSlot, unwrapInitInstr = fl.lowerEnumVariantCondBind(ie, resEn, vv, ie.Cond, unwrapSym)
			break
		}
		optTy := fl.info(ie.Cond).Type.(*types.Optional)
		fl.tc.visit(optTy)
		if ie.MutUnwrap {
			condPtr := fl.lowerLValueAddr(ie.Cond)
			ptrTmp := fl.allocSlot("_ll_ifoptp", &types.Ptr{Elem: optTy})
			fl.assignLocal(ptrTmp, condPtr)
			condExpr = optHasValueExpr(&ExprDeref{Ptr: NewLocalExpr(ptrTmp), Typ: optTy}, optTy)
			if unwrapSym != nil {
				uLoc := &Local{CName: unwrapSym.CName, Type: unwrapSym.Type, IsMutPtr: true}
				fl.locals = append(fl.locals, uLoc)
				fl.symLocals[unwrapSym] = uLoc
				unwrapInitInstr = &InstrAssign{Dst: NewLocalExpr(uLoc), Expr: EnsureExpr(optUnwrapPtrExpr(NewLocalExpr(ptrTmp), optTy), uLoc.Type)}
			}
		} else {
			// Evaluate `ie.Cond` into a dedicated slot kept alive across the `then` branch!
			// Note: if `ie.Cond` is an lvalue (`user?`), borrow it without copying; if rvalue (`m["k"]`), own & drop after `if`!
			if fl.isAddressable(ie.Cond) {
				condPtr := fl.lowerLValueAddr(ie.Cond)
				ptrTmp := fl.allocSlot("_ll_ifoptp", &types.Ptr{Elem: optTy})
				fl.assignLocal(ptrTmp, condPtr)
				condExpr = optHasValueExpr(&ExprDeref{Ptr: NewLocalExpr(ptrTmp), Typ: optTy}, optTy)
				if unwrapSym != nil {
					uLoc := &Local{CName: unwrapSym.CName, Type: unwrapSym.Type}
					fl.locals = append(fl.locals, uLoc)
					fl.symLocals[unwrapSym] = uLoc
					unwrapInitInstr = &InstrAssign{Dst: NewLocalExpr(uLoc), Expr: EnsureExpr(optUnwrapValExpr(&ExprDeref{Ptr: NewLocalExpr(ptrTmp), Typ: optTy}, optTy), uLoc.Type)}
				}
			} else {
				bindHoldSlot = fl.allocSlot("_ll_ifbind", optTy)
				valStr := fl.lowerRawExpr(ie.Cond, optTy, true)
				fl.assignLocal(bindHoldSlot, valStr)
				condExpr = optHasValueExpr(NewLocalExpr(bindHoldSlot), optTy)
				if unwrapSym != nil {
					uLoc := &Local{CName: unwrapSym.CName, Type: unwrapSym.Type}
					fl.locals = append(fl.locals, uLoc)
					fl.symLocals[unwrapSym] = uLoc
					unwrapInitInstr = &InstrAssign{Dst: NewLocalExpr(uLoc), Expr: EnsureExpr(optUnwrapValExpr(NewLocalExpr(bindHoldSlot), optTy), uLoc.Type)}
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
		tInf := fl.info(ie.Then)
		tTy := tInf.Type
		if tTy == types.TypeNever {
			fl.lowerVoidExpr(ie.Then)
			fl.flushStmtTemps()
		} else {
			if isOptWithoutElse && ((tTy != nil && tTy.Equals(resSlot.Type)) || tInf.WrapOptional) {
				tVal := fl.lowerExprOwned(ie.Then, resSlot.Type)
				fl.assignLocal(resSlot, tVal)
			} else {
				tVal := fl.lowerExprOwned(ie.Then, branchTy)
				if isOptWithoutElse {
					fl.assignLocal(resSlot, fl.wrapInOptionalExpr(tVal, resSlot.Type.(*types.Optional)))
				} else {
					fl.assignLocal(resSlot, tVal)
				}
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

	fl.emit(&InstrIf{Cond: EnsureExpr(condExpr, types.TypeBool), Then: thenBody, Else: elseBody})

	// Pop outer `if` scope (dropping `bindHoldSlot` if not already unwound)
	fl.popScopeEmitDrops()
	fl.emit(&InstrLabel{Name: exitLbl})

	if resSlot != nil {
		return NewLocalExpr(resSlot)
	}
	return nil
}

// lowerEnumVariantCondBind lowers `if (r! as v)` and `if (v.as_variant(vr) as p)`:
// the branch runs when `recv` holds variant `vr`, binding `unwrapSym` to (or `&` into) its payload.
func (fl *funcLowerer) lowerEnumVariantCondBind(ie *ast.IfExpr, en *types.Enum, vr *types.EnumVariant, recvExpr ast.Expr, unwrapSym *sema.Symbol) (Expr, *Local, Instr) {
	fl.tc.visit(en)
	var condExpr Expr
	var holdSlot *Local
	var initInstr Instr
	bindLocal := func(isPtr bool) *Local {
		if unwrapSym == nil {
			return nil
		}
		uLoc := &Local{CName: unwrapSym.CName, Type: unwrapSym.Type, IsMutPtr: isPtr}
		fl.locals = append(fl.locals, uLoc)
		fl.symLocals[unwrapSym] = uLoc
		return uLoc
	}
	_, isArenaRef := fl.info(recvExpr).Type.(*types.Ref)
	if ie.MutUnwrap || isArenaRef || (fl.isAddressable(recvExpr) && !fl.involvesPackedStruct(recvExpr)) {
		ptrTmp := fl.allocSlot("_ll_ifvarp", &types.Ptr{Elem: en})
		var addrExpr Expr
		if isArenaRef {
			addrExpr = fl.lowerExprRead(recvExpr)
		} else {
			addrExpr = fl.lowerLValueAddrMode(recvExpr, ie.MutUnwrap)
		}
		fl.assignLocal(ptrTmp, EnsureExpr(addrExpr, &types.Ptr{Elem: en}))
		deref := &ExprDeref{Ptr: NewLocalExpr(ptrTmp), Typ: en}
		condExpr = enumVariantTagEqExpr(deref, en, vr)
		if uLoc := bindLocal(ie.MutUnwrap); uLoc != nil {
			if ie.MutUnwrap {
				place := &ExprField{Base: &ExprField{Base: NewLocalExpr(ptrTmp), FieldName: "data", IsPtr: true}, FieldName: vr.CName, Typ: vr.Payload}
				initInstr = &InstrAssign{Dst: NewLocalExpr(uLoc), Expr: &ExprAddrOf{Place: place, Typ: &types.Ptr{Elem: vr.Payload}}}
			} else {
				initInstr = &InstrAssign{Dst: NewLocalExpr(uLoc), Expr: EnsureExpr(&ExprField{Base: &ExprField{Base: deref, FieldName: "data"}, FieldName: vr.CName, Typ: vr.Payload}, uLoc.Type)}
			}
		}
		return condExpr, nil, initInstr
	}
	holdSlot = fl.allocSlot("_ll_ifbind", en)
	fl.assignLocal(holdSlot, fl.lowerRawExpr(recvExpr, en, true))
	condExpr = enumVariantTagEqExpr(NewLocalExpr(holdSlot), en, vr)
	if uLoc := bindLocal(false); uLoc != nil {
		initInstr = &InstrAssign{Dst: NewLocalExpr(uLoc), Expr: EnsureExpr(&ExprField{Base: &ExprField{Base: NewLocalExpr(holdSlot), FieldName: "data"}, FieldName: vr.CName, Typ: vr.Payload}, uLoc.Type)}
	}
	return condExpr, holdSlot, initInstr
}
