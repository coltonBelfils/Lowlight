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

	// Check `own self` field move (`self.field`)
	if mem, isMem := e.(*ast.MemberExpr); isMem && !inf.IsOptChain && inf.EnumVariant == nil && !inf.Type.IsPlain() {
		if id, isId := mem.Base.(*ast.IdentExpr); isId && id.Name == "self" {
			if selfSym := fl.info(id).Sym; selfSym != nil && selfSym.BindKind == sema.BindOwned {
				if selfLoc, ok := fl.symLocals[selfSym]; ok {
					st := selfLoc.Type.(*types.Struct)
					sf, _ := st.FieldByName(mem.Member)
					if sf != nil {
						fieldLval := &ExprField{Base: NewLocalExpr(selfLoc), FieldName: sf.CName, Typ: sf.Type, IsPtr: false}
						mvTmp := fl.allocSlot("_ll_fmove", sf.Type)
						fl.assignLocal(mvTmp, fieldLval)
						fl.emit(&InstrAssign{Dst: fieldLval, Expr: &ExprLitZero{Typ: sf.Type}})
						return fl.applyOwnedCoercions(e, inf, NewLocalExpr(mvTmp), targetTy)
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

		// 2. `[N: T]` stack array passed to read-only `[T]` parameter!
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
	case *ast.StringLit, *ast.NoneLit, *ast.RangeExpr:
		// String literals have `cap = 0` (static storage); `none` has `has_value = false` / `NULL`; ranges are plain scalars.
		return false
	case *ast.IdentExpr:
		// Reading a variable or static function thunk does NOT allocate!
		return false
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

func (fl *funcLowerer) isAddressable(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.IdentExpr:
		inf := fl.info(v)
		return inf.Sym == nil || inf.Sym.FuncInst == nil
	case *ast.MemberExpr:
		inf := fl.info(v)
		if v.QuestDot || inf.IsOptChain || inf.EnumVariant != nil || inf.Sym != nil {
			return false
		}
		baseTy := fl.info(v.Base).Type
		if _, ok := baseTy.(*types.Ref); ok {
			return true
		}
		if _, ok := baseTy.(*types.Ptr); ok {
			return true
		}
		return fl.isAddressable(v.Base)
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
	default:
		return false
	}
}

// lowerLValueAddr lowers an addressable place expression `e` to a typed pointer expression `T*`.
func (fl *funcLowerer) lowerLValueAddr(e ast.Expr) Expr {
	inf := fl.info(e)
	switch v := e.(type) {
	case *ast.UnsafeExpr:
		return fl.lowerLValueAddr(v.Expr)
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
			return fl.lowerLValueAddr(v.Operand)
		}

	case *ast.MemberExpr:
		baseTy := fl.info(v.Base).Type
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
				ptr := fl.lowerExprRead(v.Base)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true},
					Typ:   &types.Ptr{Elem: tup.Elems[fIdx]},
				}
			}
			st := rf.Elem.(*types.Struct)
			sf, _ := st.FieldByName(v.Member)
			ptr := fl.lowerExprRead(v.Base)
			return &ExprAddrOf{
				Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: true},
				Typ:   &types.Ptr{Elem: sf.Type},
			}
		}
		if _, isVec := baseTy.(*types.Vector); isVec {
			basePtr := fl.lowerLValueAddr(v.Base)
			return &ExprAddrOf{
				Place: &ExprField{Base: basePtr, FieldName: v.Member, Typ: inf.Type, IsPtr: true},
				Typ:   &types.Ptr{Elem: inf.Type},
			}
		}
		if _, isArr := baseTy.(*types.StackArray); isArr {
			basePtr := fl.lowerLValueAddr(v.Base)
			return &ExprAddrOf{
				Place: &ExprField{Base: basePtr, FieldName: v.Member, Typ: inf.Type, IsPtr: true},
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
			basePtr := fl.lowerLValueAddr(v.Base)
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
				ptr := fl.lowerExprRead(v.Base)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: fName, FieldIdx: fIdx, Typ: tup.Elems[fIdx], IsPtr: true},
					Typ:   &types.Ptr{Elem: tup.Elems[fIdx]},
				}
			}
			if _, isVec := p.Elem.(*types.Vector); isVec {
				ptr := fl.lowerExprRead(v.Base)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: v.Member, Typ: inf.Type, IsPtr: true},
					Typ:   &types.Ptr{Elem: inf.Type},
				}
			}
			if _, isArr := p.Elem.(*types.StackArray); isArr {
				ptr := fl.lowerExprRead(v.Base)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: v.Member, Typ: inf.Type, IsPtr: true},
					Typ:   &types.Ptr{Elem: inf.Type},
				}
			}
			if st, ok := p.Elem.(*types.Struct); ok {
				sf, _ := st.FieldByName(v.Member)
				ptr := fl.lowerExprRead(v.Base)
				return &ExprAddrOf{
					Place: &ExprField{Base: EnsureExpr(ptr, baseTy), FieldName: sf.CName, Typ: sf.Type, IsPtr: true},
					Typ:   &types.Ptr{Elem: sf.Type},
				}
			}
		}
		st := baseTy.(*types.Struct)
		sf, _ := st.FieldByName(v.Member)
		basePtr := fl.lowerLValueAddr(v.Base)
		return &ExprAddrOf{
			Place: &ExprField{Base: basePtr, FieldName: sf.CName, Typ: sf.Type, IsPtr: true},
			Typ:   &types.Ptr{Elem: sf.Type},
		}

	case *ast.IndexExpr:
		baseTy := fl.info(v.Base).Type
		if tup, isTup := baseTy.(*types.Tuple); isTup {
			fIdx := inf.TupleFieldIdx
			basePtr := fl.lowerLValueAddr(v.Base)
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
			return fl.indexElementPtr(rf.Elem, ptr, idxExpr, file, line)
		}
		if _, isPtr := baseTy.(*types.Ptr); isPtr {
			ptr := fl.lowerExprRead(v.Base)
			return &ExprBinary{Op: token.Plus, Left: EnsureExpr(ptr, baseTy), Right: EnsureExpr(idxExpr, types.TypeInt), Typ: baseTy}
		}
		basePtr := fl.lowerLValueAddr(v.Base)
		return fl.indexElementPtr(baseTy, basePtr, idxExpr, file, line)
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
