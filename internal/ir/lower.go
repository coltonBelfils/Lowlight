package ir

import (
	"fmt"
	"path/filepath"

	"lowlight/internal/ast"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// Lower translates a semantically validated CheckedProgram into Mid-Level IR (MIR),
// making monomorphized types, temporary lifetimes, move zeroing, and LIFO scope/unwind
// Drop sequences completely explicit before C11 emission.
func Lower(prog *sema.CheckedProgram, release bool) *Program {
	tc := newTypeCollector()

	// Seed type collector with declared & instantiated types
	for _, en := range prog.Enums {
		tc.visit(en)
	}
	for _, dt := range prog.Distincts {
		tc.visit(dt)
	}
	for _, st := range prog.Structs {
		tc.visit(st)
	}
	for _, ifc := range prog.Interfaces {
		tc.visit(ifc)
	}
	for _, ef := range prog.ExternFuncs {
		for _, p := range ef.Sig.Params {
			tc.visit(p.Type)
		}
		tc.visit(ef.Sig.Ret)
	}
	for _, tv := range prog.TopVars {
		tc.visit(tv.Sym.Type)
	}
	for _, fi := range prog.Funcs {
		for _, p := range fi.Sig.Params {
			tc.visit(p.Type)
		}
		tc.visit(fi.Sig.Ret)
	}
	for _, li := range prog.Lambdas {
		tc.visit(li.Sig)
		for _, c := range li.Captures {
			tc.visit(c.Type)
		}
	}
	vtables := sortVTablesDeterministically(prog.VTables)
	for _, vt := range vtables {
		tc.visit(vt.Concrete)
		tc.visit(vt.Iface)
	}
	visitBodyMaps := func(bm *sema.BodyMaps) {
		if bm == nil {
			return
		}
		for _, inf := range bm.ExprInfos {
			if inf != nil {
				tc.visit(inf.Type)
				tc.visit(inf.IntrinsicType)
				if inf.BoxInterface != nil {
					tc.visit(inf.BoxInterface)
				}
			}
		}
	}
	visitBodyMaps(prog.TopBodyMaps)
	for _, st := range prog.Structs {
		visitBodyMaps(prog.StructBodyMaps[st])
	}
	for _, fi := range prog.Funcs {
		visitBodyMaps(fi.BodyMaps)
	}
	for _, li := range prog.Lambdas {
		visitBodyMaps(li.BodyMaps)
	}

	irp := &Program{
		Checked:       prog,
		ExternHeaders: prog.ExternHeaders,
		ExternFuncs:   prog.ExternFuncs,
		VTables:       vtables,
		Release:       release,
	}

	seenThunks := make(map[string]bool)
	registerThunk := func(fi *sema.FuncInstance, fnTy *types.Func) string {
		tname := StaticFuncThunkName(fi.CName, fnTy)
		if !seenThunks[tname] {
			seenThunks[tname] = true
			tc.visit(fnTy)
			irp.StaticThunks = append(irp.StaticThunks, &StaticThunk{
				ThunkCName: tname,
				TargetFunc: fi,
				FuncType:   fnTy,
			})
		}
		return tname
	}

	// 1. Lower top-level `bind` constants
	for _, tv := range prog.TopVars {
		fl := newFuncLowerer(prog, prog.TopBodyMaps, tc, release, registerThunk)
		fl.pushScope(0, "", "", nil, nil, false)
		exprStr := fl.lowerExprOwned(tv.Decl.Value, tv.Sym.Type)
		fl.flushStmtTemps()
		fl.popScopeEmitDrops()
		irp.Globals = append(irp.Globals, &Global{
			Name:      tv.Sym.Name,
			CName:     tv.Sym.CName,
			Type:      tv.Sym.Type,
			InitInstr: fl.rootInstrs(),
			InitExpr:  exprStr,
		})
	}

	// 2. Lower all concrete & monomorphized functions/methods
	for _, fi := range prog.Funcs {
		fn := lowerFuncInstance(prog, fi, tc, release, registerThunk)
		irp.Funcs = append(irp.Funcs, fn)
		if fi == prog.MainFunc {
			irp.MainFunc = fn
		}
	}

	// 3. Lower all anonymous lambdas
	for _, li := range prog.Lambdas {
		fn := lowerLambda(prog, li, tc, release, registerThunk)
		irp.Funcs = append(irp.Funcs, fn)
	}

	// Populate finalized topological type lists
	irp.OrderedTypes = tc.ordered
	irp.Enums = tc.ens
	irp.Distincts = tc.dists
	irp.Structs = tc.sts
	irp.Optionals = tc.opts
	irp.Vectors = tc.vecs
	irp.Arrays = tc.arrs
	irp.Sets = tc.sets
	irp.Maps = tc.maps
	irp.FuncTypes = tc.fns
	irp.Interfaces = tc.ifcs
	return irp
}

// -----------------------------------------------------------------------------
// Function Lowerer State & Scope Management
// -----------------------------------------------------------------------------

type mirScope struct {
	targetID      int
	breakLabel    string
	continueLabel string
	resultVar     *Local
	hasValVar     *Local // non-nil in value-producing `for` loops to drop overwritten iteration values
	loopElemTy    types.Type
	isForNoElse   bool
	isFunc        bool
	ownedLocals   []*Local
}

type funcLowerer struct {
	prog          *sema.CheckedProgram
	bm            *sema.BodyMaps
	tc            *typeCollector
	release       bool
	registerThunk func(*sema.FuncInstance, *types.Func) string

	locals        []*Local
	symLocals     map[*sema.Symbol]*Local
	captureFields map[*sema.Symbol]string // inside lambda: outer Symbol -> `_ll_env-><field>`
	scopes        []*mirScope
	stmtTemps     []*Local
	emitStack     [][]Instr
	usedLabels    map[string]bool
	nextTempID    int
}

func newFuncLowerer(
	prog *sema.CheckedProgram,
	bm *sema.BodyMaps,
	tc *typeCollector,
	release bool,
	registerThunk func(*sema.FuncInstance, *types.Func) string,
) *funcLowerer {
	if bm == nil {
		bm = prog.TopBodyMaps
	}
	return &funcLowerer{
		prog:          prog,
		bm:            bm,
		tc:            tc,
		release:       release,
		registerThunk: registerThunk,
		symLocals:     make(map[*sema.Symbol]*Local),
		captureFields: make(map[*sema.Symbol]string),
		emitStack:     [][]Instr{{}},
		usedLabels:    make(map[string]bool),
	}
}

func lowerFuncInstance(
	prog *sema.CheckedProgram,
	fi *sema.FuncInstance,
	tc *typeCollector,
	release bool,
	registerThunk func(*sema.FuncInstance, *types.Func) string,
) *Function {
	fl := newFuncLowerer(prog, fi.BodyMaps, tc, release, registerThunk)
	var params []*Local

	fnScope := fl.pushScope(fi.FuncTargetID, "", "", nil, nil, true)

	for i, fp := range fi.Sig.Params {
		sym := fi.ParamSyms[i]
		tc.visit(fp.Type)
		pLoc := &Local{
			CName:    sym.CName,
			Type:     fp.Type,
			IsMutPtr: fp.Mode == types.ModeMut,
			IsParam:  true,
		}
		if fp.Mode == types.ModeOwn && !fp.Type.IsPlain() {
			if NeedsMovedFlag(fp.Type) {
				pLoc.MovedFlag = pLoc.CName + "_moved"
			}
			fnScope.ownedLocals = append(fnScope.ownedLocals, pLoc)
		}
		params = append(params, pLoc)
		fl.symLocals[sym] = pLoc
	}

	fl.lowerFunctionBody(fi.Body, fi.Sig.Ret, fnScope)

	return &Function{
		Name:    fi.Name,
		CName:   fi.CName,
		Params:  params,
		RetType: fi.Sig.Ret,
		Locals:  fl.locals,
		Body:    pruneUnusedLabels(fl.rootInstrs(), fl.usedLabels),
	}
}

func lowerLambda(
	prog *sema.CheckedProgram,
	li *sema.LambdaInfo,
	tc *typeCollector,
	release bool,
	registerThunk func(*sema.FuncInstance, *types.Func) string,
) *Function {
	fl := newFuncLowerer(prog, li.BodyMaps, tc, release, registerThunk)
	for _, capItem := range li.Captures {
		fl.captureFields[capItem.OuterSym] = fmt.Sprintf("((%s*)_ll_env)->%s", li.EnvCName, capItem.CName)
	}

	fnScope := fl.pushScope(li.FuncTargetID, "", "", nil, nil, true)
	var params []*Local
	for i, fp := range li.Sig.Params {
		sym := li.ParamSyms[i]
		tc.visit(fp.Type)
		pLoc := &Local{
			CName:    sym.CName,
			Type:     fp.Type,
			IsMutPtr: fp.Mode == types.ModeMut,
			IsParam:  true,
		}
		if fp.Mode == types.ModeOwn && !fp.Type.IsPlain() {
			if NeedsMovedFlag(fp.Type) {
				pLoc.MovedFlag = pLoc.CName + "_moved"
			}
			fnScope.ownedLocals = append(fnScope.ownedLocals, pLoc)
		}
		params = append(params, pLoc)
		fl.symLocals[sym] = pLoc
	}

	fl.lowerFunctionBody(li.Lit.Body, li.Sig.Ret, fnScope)

	return &Function{
		Name:       li.CName,
		CName:      li.CName,
		Params:     params,
		RetType:    li.Sig.Ret,
		Locals:     fl.locals,
		Body:       pruneUnusedLabels(fl.rootInstrs(), fl.usedLabels),
		IsLambda:   true,
		LambdaInfo: li,
	}
}

func (fl *funcLowerer) lowerFunctionBody(body ast.Expr, retTy types.Type, fnScope *mirScope) {
	if retTy.Equals(types.TypeVoid) {
		fl.lowerVoidExpr(body)
		fl.flushStmtTemps()
		if !endsWithTerm(fl.curInstrs()) {
			fl.popScopeEmitDrops()
			fl.emit(&InstrReturn{})
		} else {
			fl.scopes = fl.scopes[:len(fl.scopes)-1]
		}
		return
	}

	retSlot := fl.allocSlot("_ll_ret", retTy)
	fnScope.resultVar = retSlot
	bodyTy := fl.info(body).Type
	if bodyTy == types.TypeNever {
		fl.lowerVoidExpr(body)
		fl.flushStmtTemps()
		fl.scopes = fl.scopes[:len(fl.scopes)-1]
		return
	}

	valExpr := fl.lowerExprOwned(body, retTy)
	fl.assignLocal(retSlot, valExpr)
	fl.flushStmtTemps()
	if !endsWithTerm(fl.curInstrs()) {
		fl.popScopeEmitDrops()
		fl.emit(&InstrReturn{Expr: retSlot.CName})
	} else {
		fl.scopes = fl.scopes[:len(fl.scopes)-1]
	}
}

// -----------------------------------------------------------------------------
// Instruction Buffer & Temporary Helpers
// -----------------------------------------------------------------------------

func (fl *funcLowerer) emit(ins Instr) {
	top := len(fl.emitStack) - 1
	fl.emitStack[top] = append(fl.emitStack[top], ins)
}

func (fl *funcLowerer) curInstrs() []Instr {
	return fl.emitStack[len(fl.emitStack)-1]
}

func (fl *funcLowerer) rootInstrs() []Instr {
	return fl.emitStack[0]
}

func (fl *funcLowerer) pushInstrBuf() {
	fl.emitStack = append(fl.emitStack, nil)
}

func (fl *funcLowerer) popInstrBuf() []Instr {
	top := len(fl.emitStack) - 1
	buf := fl.emitStack[top]
	fl.emitStack = fl.emitStack[:top]
	return buf
}

func (fl *funcLowerer) allocSlot(prefix string, ty types.Type) *Local {
	fl.nextTempID++
	fl.tc.visit(ty)
	cname := fmt.Sprintf("%s_%d", prefix, fl.nextTempID)
	loc := &Local{
		CName: cname,
		Type:  ty,
	}
	if NeedsMovedFlag(ty) {
		loc.MovedFlag = cname + "_moved"
	}
	fl.locals = append(fl.locals, loc)
	return loc
}

func (fl *funcLowerer) allocStmtTemp(ty types.Type, initExpr string) string {
	loc := fl.allocSlot("_ll_tmp", ty)
	fl.assignLocal(loc, initExpr)
	if !ty.IsPlain() {
		fl.stmtTemps = append(fl.stmtTemps, loc)
	}
	return loc.CName
}

func (fl *funcLowerer) assignLocal(loc *Local, expr string) {
	fl.emit(&InstrAssign{Dst: loc.CName, Expr: expr})
	if loc.MovedFlag != "" {
		fl.emit(&InstrAssign{Dst: loc.MovedFlag, Expr: "false"})
	}
}

func (fl *funcLowerer) flushStmtTemps() {
	if len(fl.stmtTemps) == 0 {
		return
	}
	temps := fl.stmtTemps
	fl.stmtTemps = nil
	for i := len(temps) - 1; i >= 0; i-- {
		t := temps[i]
		fl.emit(&InstrDrop{Var: t.CName, Type: t.Type, MovedFlag: t.MovedFlag})
	}
}

func (fl *funcLowerer) pushScope(targetID int, breakLbl, contLbl string, resVar, hasValVar *Local, isFunc bool) *mirScope {
	sc := &mirScope{
		targetID:      targetID,
		breakLabel:    breakLbl,
		continueLabel: contLbl,
		resultVar:     resVar,
		hasValVar:     hasValVar,
		isFunc:        isFunc,
	}
	fl.scopes = append(fl.scopes, sc)
	return sc
}

func (fl *funcLowerer) popScopeEmitDrops() {
	top := len(fl.scopes) - 1
	sc := fl.scopes[top]
	fl.scopes = fl.scopes[:top]
	for i := len(sc.ownedLocals) - 1; i >= 0; i-- {
		loc := sc.ownedLocals[i]
		fl.emit(&InstrDrop{Var: loc.CName, Type: loc.Type, MovedFlag: loc.MovedFlag})
	}
}

func (fl *funcLowerer) emitDropsDownTo(stopIdx int) {
	for s := len(fl.scopes) - 1; s >= stopIdx; s-- {
		sc := fl.scopes[s]
		for i := len(sc.ownedLocals) - 1; i >= 0; i-- {
			loc := sc.ownedLocals[i]
			fl.emit(&InstrDrop{Var: loc.CName, Type: loc.Type, MovedFlag: loc.MovedFlag})
		}
	}
}

func (fl *funcLowerer) info(e ast.Expr) *sema.ExprInfo {
	if e == nil {
		return &sema.ExprInfo{Type: types.TypeVoid}
	}
	if fl.bm != nil {
		if inf, ok := fl.bm.ExprInfos[e]; ok && inf != nil {
			return inf
		}
	}
	return &sema.ExprInfo{Type: types.TypeVoid}
}

func formatSpanLoc(e ast.Expr) (string, int) {
	if e == nil {
		return "<unknown>", 0
	}
	sp := e.GetSpan()
	if sp.Start.File == "" {
		return "<input>", sp.Start.Line
	}
	return filepath.Base(sp.Start.File), sp.Start.Line
}

func endsWithTerm(ins []Instr) bool {
	if len(ins) == 0 {
		return false
	}
	switch ins[len(ins)-1].(type) {
	case *InstrGoto, *InstrReturn:
		return true
	default:
		return false
	}
}

func pruneUnusedLabels(ins []Instr, used map[string]bool) []Instr {
	out := make([]Instr, 0, len(ins))
	for _, in := range ins {
		switch v := in.(type) {
		case *InstrLabel:
			if !used[v.Name] {
				continue
			}
			out = append(out, v)
		case *InstrIf:
			v.Then = pruneUnusedLabels(v.Then, used)
			v.Else = pruneUnusedLabels(v.Else, used)
			out = append(out, v)
		case *InstrWhile:
			v.Body = pruneUnusedLabels(v.Body, used)
			out = append(out, v)
		case *InstrBlock:
			v.Body = pruneUnusedLabels(v.Body, used)
			out = append(out, v)
		default:
			out = append(out, in)
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// Statements & Unwinds
// -----------------------------------------------------------------------------

func (fl *funcLowerer) lowerStmt(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.VarStmt:
		fl.lowerVarStmt(s)
	case *ast.AssignStmt:
		fl.lowerAssignStmt(s)
	case *ast.UnwindStmt:
		fl.lowerUnwindStmt(s)
	case *ast.DeclStmt:
		// Nominal types already registered globally in sema
	case *ast.ExprStmt:
		fl.lowerVoidExpr(s.Expr)
		fl.flushStmtTemps()
	}
}

func (fl *funcLowerer) lowerVarStmt(s *ast.VarStmt) {
	sym := fl.bm.VarSymbols[s]
	if sym == nil {
		return
	}
	fl.tc.visit(sym.Type)

	loc := &Local{
		CName: sym.CName,
		Type:  sym.Type,
	}
	if NeedsMovedFlag(sym.Type) {
		loc.MovedFlag = sym.CName + "_moved"
	}
	fl.locals = append(fl.locals, loc)
	fl.symLocals[sym] = loc

	if s.FallbackUnwind != nil {
		// `let a = opt_expr ?? <unwind>`
		optTy := fl.info(s.Value).Type.(*types.Optional)
		optVal := fl.lowerExprOwned(s.Value, optTy)
		optTmp := fl.allocSlot("_ll_coalesce", optTy)
		fl.assignLocal(optTmp, optVal)
		fl.flushStmtTemps()

		fl.pushInstrBuf()
		fl.lowerUnwindStmt(s.FallbackUnwind)
		elseBranch := fl.popInstrBuf()

		fl.emit(&InstrIf{
			Cond: "!" + optHasValueExpr(optTmp.CName, optTy),
			Then: elseBranch,
		})
		// Extract owned value out of `optTmp` (freeing outer heap box if `optTy.IsBoxed()`)!
		fl.extractOwnedOptionalInto(optTmp, optTy, loc)
		if !sym.Type.IsPlain() {
			cur := fl.scopes[len(fl.scopes)-1]
			cur.ownedLocals = append(cur.ownedLocals, loc)
		}
		return
	}

	valExpr := fl.lowerExprOwned(s.Value, sym.Type)
	fl.assignLocal(loc, valExpr)
	fl.flushStmtTemps()
	if !sym.Type.IsPlain() {
		cur := fl.scopes[len(fl.scopes)-1]
		cur.ownedLocals = append(cur.ownedLocals, loc)
	}
}

func (fl *funcLowerer) extractOwnedOptionalInto(optSlot *Local, optTy *types.Optional, dst *Local) {
	if optTy.IsBoxed() {
		// Deref `*optSlot`, assign to `dst`, and free ONLY the heap shell (not inner fields)!
		fl.assignLocal(dst, fmt.Sprintf("(*%s)", optSlot.CName))
		fl.emit(&InstrExpr{Expr: fmt.Sprintf("ll_free(%s)", optSlot.CName)})
		fl.emit(&InstrAssign{Dst: optSlot.CName, Expr: "NULL"})
	} else {
		fl.assignLocal(dst, fmt.Sprintf("%s.value", optSlot.CName))
		fl.emit(&InstrAssign{Dst: optSlot.CName + ".has_value", Expr: "false"})
	}
}

func (fl *funcLowerer) lowerAssignStmt(s *ast.AssignStmt) {
	// Shorthand `map[key] = val` -> `ll_map_insert(&map, key, val)`
	if idx, isIdx := s.Target.(*ast.IndexExpr); isIdx && s.Op == token.Assign && s.FallbackUnwind == nil {
		if mapTy, isMap := fl.info(idx.Base).Type.(*types.Map); isMap {
			fl.tc.visit(mapTy)
			mPtr := fl.lowerLValueAddr(idx.Base)
			kExpr := fl.lowerExprOwned(idx.Index, mapTy.Key)
			vExpr := fl.lowerExprOwned(s.Value, mapTy.Value)
			fl.emit(&InstrExpr{
				Expr: fmt.Sprintf("ll_map_insert__%s(%s, %s, %s)", mapTy.Mangled(), mPtr, kExpr, vExpr),
			})
			fl.flushStmtTemps()
			return
		}
	}

	targetTy := fl.info(s.Target).Type

	if s.FallbackUnwind != nil {
		optTy := fl.info(s.Value).Type.(*types.Optional)
		optVal := fl.lowerExprOwned(s.Value, optTy)
		optTmp := fl.allocSlot("_ll_asgn_opt", optTy)
		fl.assignLocal(optTmp, optVal)
		fl.flushStmtTemps()

		fl.pushInstrBuf()
		fl.lowerUnwindStmt(s.FallbackUnwind)
		elseBranch := fl.popInstrBuf()

		fl.emit(&InstrIf{
			Cond: "!" + optHasValueExpr(optTmp.CName, optTy),
			Then: elseBranch,
		})

		unwrappedTmp := fl.allocSlot("_ll_asgn_val", optTy.Elem)
		fl.extractOwnedOptionalInto(optTmp, optTy, unwrappedTmp)
		fl.assignToTargetLValue(s.Target, targetTy, unwrappedTmp.CName)
		return
	}

	if s.Op == token.Assign {
		// Evaluate RHS into a temporary FIRST before dropping old LHS Resource value
		// (so self-referential reassignments like `s = s + "!"` never read freed `s`!)
		valExpr := fl.lowerExprOwned(s.Value, targetTy)
		rhsTmp := fl.allocSlot("_ll_rhs", targetTy)
		fl.assignLocal(rhsTmp, valExpr)
		fl.flushStmtTemps()
		fl.assignToTargetLValue(s.Target, targetTy, rhsTmp.CName)
		return
	}

	// Compound assignment (`+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`, `>>=`)
	lhsPtr := fl.lowerLValueAddr(s.Target)
	rhsExpr := fl.lowerExprRead(s.Value)
	file, line := formatSpanLoc(s.Target)
	fl.emitCompoundAssign(s.Op, targetTy, lhsPtr, rhsExpr, file, line)
	fl.flushStmtTemps()
}

func (fl *funcLowerer) assignToTargetLValue(target ast.Expr, targetTy types.Type, newValExpr string) {
	if id, isId := target.(*ast.IdentExpr); isId {
		if sym := fl.info(id).Sym; sym != nil {
			if loc, ok := fl.symLocals[sym]; ok && !loc.IsMutPtr {
				if !targetTy.IsPlain() {
					fl.emit(&InstrDrop{Var: loc.CName, Type: targetTy, MovedFlag: loc.MovedFlag})
				}
				fl.assignLocal(loc, newValExpr)
				return
			}
		}
	}
	lhsPtr := fl.lowerLValueAddr(target)
	if !targetTy.IsPlain() {
		fl.emit(&InstrExpr{Expr: fmt.Sprintf("%s(%s)", DropFnName(targetTy), lhsPtr)})
	}
	fl.emit(&InstrAssign{Dst: fmt.Sprintf("(*%s)", lhsPtr), Expr: newValExpr})
}

func (fl *funcLowerer) emitCompoundAssign(op token.Kind, ty types.Type, lhsPtr, rhsExpr, file string, line int) {
	if op == token.PlusEq && ty.Equals(types.TypeString) {
		fl.emit(&InstrExpr{Expr: fmt.Sprintf("ll_str_push(%s, %s)", lhsPtr, rhsExpr)})
		return
	}
	prim := types.UnderlyingPrimitive(ty)
	lhsVal := fmt.Sprintf("(*%s)", lhsPtr)
	var computed string
	switch op {
	case token.PlusEq:
		computed = fl.buildArithBinary(token.Plus, ty, prim, lhsVal, rhsExpr, file, line)
	case token.MinusEq:
		computed = fl.buildArithBinary(token.Minus, ty, prim, lhsVal, rhsExpr, file, line)
	case token.StarEq:
		computed = fl.buildArithBinary(token.Star, ty, prim, lhsVal, rhsExpr, file, line)
	case token.SlashEq:
		computed = fl.buildArithBinary(token.Slash, ty, prim, lhsVal, rhsExpr, file, line)
	case token.PercentEq:
		computed = fl.buildArithBinary(token.Percent, ty, prim, lhsVal, rhsExpr, file, line)
	case token.AmpEq:
		computed = fmt.Sprintf("((%s)(%s & %s))", CType(ty), lhsVal, rhsExpr)
	case token.PipeEq:
		computed = fmt.Sprintf("((%s)(%s | %s))", CType(ty), lhsVal, rhsExpr)
	case token.CaretEq:
		computed = fmt.Sprintf("((%s)(%s ^ %s))", CType(ty), lhsVal, rhsExpr)
	case token.ShlEq:
		computed = fl.buildShiftExpr(token.Shl, ty, prim, lhsVal, rhsExpr, file, line)
	case token.ShrEq:
		computed = fl.buildShiftExpr(token.Shr, ty, prim, lhsVal, rhsExpr, file, line)
	}
	fl.emit(&InstrAssign{Dst: lhsVal, Expr: computed})
}

func (fl *funcLowerer) lowerUnwindStmt(u *ast.UnwindStmt) {
	uinf := fl.bm.UnwindInfos[u]
	if uinf == nil {
		return
	}

	// Locate target scope on `fl.scopes`
	targetIdx := -1
	for i := len(fl.scopes) - 1; i >= 0; i-- {
		if fl.scopes[i].targetID == uinf.TargetID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return
	}
	tgt := fl.scopes[targetIdx]

	// 1. Evaluate unwind payload (if any) BEFORE dropping scopes along the jump path!
	if u.Value != nil && tgt.resultVar != nil {
		if tgt.hasValVar != nil {
			// Target is a value-producing `for` loop (`for <- val` or `next <- val`):
			elemTy := tgt.loopElemTy
			valExpr := fl.lowerExprOwned(u.Value, elemTy)
			valTmp := fl.allocSlot("_ll_uval", elemTy)
			fl.assignLocal(valTmp, valExpr)
			fl.flushStmtTemps()

			// Drop any prior iteration value held in `tgt.resultVar` before overwriting!
			fl.storeLoopRunningResult(tgt, valTmp.CName)
		} else {
			destTy := tgt.resultVar.Type
			valExpr := fl.lowerExprOwned(u.Value, destTy)
			fl.assignLocal(tgt.resultVar, valExpr)
			fl.flushStmtTemps()
		}
	} else {
		fl.flushStmtTemps()
	}

	// 2. Emit deterministic LIFO Drops for all scopes exited by this unwind!
	if uinf.IsContinue {
		// `next <-`: drop scopes strictly INSIDE the loop scope (`targetIdx + 1 .. top`),
		// keeping the outer loop state alive for the next iteration!
		fl.emitDropsDownTo(targetIdx + 1)
		fl.usedLabels[tgt.continueLabel] = true
		fl.emit(&InstrGoto{Label: tgt.continueLabel})
		return
	}

	if tgt.isFunc {
		fl.emitDropsDownTo(targetIdx)
		if tgt.resultVar != nil {
			fl.emit(&InstrReturn{Expr: tgt.resultVar.CName})
		} else {
			fl.emit(&InstrReturn{})
		}
		return
	}

	// Break / block / if / match / label exit: drop all scopes down to `targetIdx` inclusive,
	// because `tgt.breakLabel` sits immediately AFTER `tgt` has exited!
	fl.emitDropsDownTo(targetIdx)
	fl.usedLabels[tgt.breakLabel] = true
	fl.emit(&InstrGoto{Label: tgt.breakLabel})
}

func (fl *funcLowerer) storeLoopRunningResult(loopScope *mirScope, elemExpr string) {
	if loopScope.resultVar == nil || loopScope.hasValVar == nil {
		return
	}
	// If a previous iteration already wrote a Resource value into `resultVar`, drop it first!
	if !loopScope.resultVar.Type.IsPlain() {
		fl.emit(&InstrIf{
			Cond: loopScope.hasValVar.CName,
			Then: []Instr{
				&InstrDrop{
					Var:       loopScope.resultVar.CName,
					Type:      loopScope.resultVar.Type,
					MovedFlag: loopScope.resultVar.MovedFlag,
				},
			},
		})
	}
	if loopScope.isForNoElse {
		optRes := loopScope.resultVar.Type.(*types.Optional)
		fl.assignLocal(loopScope.resultVar, fl.wrapInOptionalExpr(elemExpr, optRes))
	} else {
		fl.assignLocal(loopScope.resultVar, elemExpr)
	}
	fl.emit(&InstrAssign{Dst: loopScope.hasValVar.CName, Expr: "true"})
}
