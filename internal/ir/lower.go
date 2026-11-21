package ir

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// Lower translates a semantically validated CheckedProgram into Mid-Level IR (MIR),
// making monomorphized types, temporary lifetimes, move zeroing, and LIFO scope/unwind
// Drop sequences completely explicit before C11 emission.
func Lower(prog *sema.CheckedProgram, release bool, testMode bool) *Program {
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
		// `ExprInfos` is keyed by AST pointer, so map iteration order is random. Visit in
		// source order so `OrderedTypes`/`FuncTypes` (and thus emitted code) are deterministic.
		for _, inf := range sortedExprInfos(bm.ExprInfos) {
			tc.visit(inf.Type)
			tc.visit(inf.IntrinsicType)
			if inf.BoxInterface != nil {
				tc.visit(inf.BoxInterface)
			}
		}
	}
	visitBodyMaps(prog.TopBodyMaps)
	for _, st := range prog.Structs {
		visitBodyMaps(prog.StructBodyMaps[st])
	}
	for _, en := range prog.Enums {
		visitBodyMaps(prog.EnumBodyMaps[en])
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
		TestMode:      testMode,
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
		if tv.Sym.IsTestOnly && !testMode {
			continue
		}
		fl := newFuncLowerer(prog, prog.TopBodyMaps, tc, release, registerThunk)
		fl.pushScope(0, "", "", nil, nil, false)
		exprStr := fl.lowerExprOwned(tv.Decl.Value, tv.Sym.Type)
		fl.flushStmtTemps()
		fl.popScopeEmitDrops()
		pkgID := ""
		if tv.Mod != nil && tv.Mod.Pkg != nil {
			pkgID = tv.Mod.Pkg.ID
		}
		irp.Globals = append(irp.Globals, &Global{
			Name:      tv.Sym.Name,
			CName:     tv.Sym.CName,
			PkgID:     pkgID,
			Type:      tv.Sym.Type,
			Locals:    fl.locals,
			InitInstr: fl.rootInstrs(),
			InitExpr:  EnsureExpr(exprStr, tv.Sym.Type),
		})
	}

	// 2. Lower all concrete & monomorphized functions/methods
	for _, fi := range prog.Funcs {
		if (fi.IsTest || fi.IsTestOnly) && !testMode {
			continue
		}
		if fi.IsTest && prog.EntryPkg != nil && !prog.IsEntryPackageMod(fi.Mod) {
			continue
		}
		fn := lowerFuncInstance(prog, fi, tc, release, registerThunk)
		irp.Funcs = append(irp.Funcs, fn)
		if fi == prog.MainFunc {
			irp.MainFunc = fn
		}
		if fi == prog.TestMainFunc {
			irp.TestMainFunc = fn
		}
		if fi.IsTest && (prog.EntryPkg == nil || prog.IsEntryPackageMod(fi.Mod)) {
			irp.TestFuncs = append(irp.TestFuncs, fn)
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
	irp.Tuples = tc.tups
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
	captureFields map[*sema.Symbol]Expr // inside lambda: outer Symbol -> `_ll_env-><field>`
	exprOverrides map[ast.Expr]Expr     // temporary receiver overrides (e.g. `match` MethodPattern scrutinee)
	scopes        []*mirScope
	stmtTemps     []*Local
	emitStack     [][]Instr
	usedLabels    map[string]bool
	nextTempID    int

	currFunc      *sema.FuncInstance
	callerFileLoc *Local
	callerLineLoc *Local
	callerColLoc  *Local
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
		captureFields: make(map[*sema.Symbol]Expr),
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
	fl.currFunc = fi
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
				pLoc.MovedLocal = &Local{CName: pLoc.MovedFlag, Type: types.TypeBool}
			}
			fnScope.ownedLocals = append(fnScope.ownedLocals, pLoc)
		}
		params = append(params, pLoc)
		fl.symLocals[sym] = pLoc
	}

	if fi.TrackCaller {
		fl.callerFileLoc = &Local{
			CName:   "_ll_caller_file",
			Type:    types.TypeString,
			IsParam: true,
		}
		fl.callerLineLoc = &Local{
			CName:   "_ll_caller_line",
			Type:    types.TypeInt,
			IsParam: true,
		}
		fl.callerColLoc = &Local{
			CName:   "_ll_caller_col",
			Type:    types.TypeInt,
			IsParam: true,
		}
		params = append(params, fl.callerFileLoc, fl.callerLineLoc, fl.callerColLoc)
	}

	fl.lowerFunctionBody(fi.Body, fi.Sig.Ret, fnScope)

	pkgID := ""
	if fi.Mod != nil && fi.Mod.Pkg != nil {
		pkgID = fi.Mod.Pkg.ID
	}
	isGeneric := len(fi.TypeSubst) > 0
	if !isGeneric && fi.RecvType != nil {
		switch rt := fi.RecvType.(type) {
		case *types.Struct:
			isGeneric = len(rt.TypeArgs) > 0
		case *types.Enum:
			isGeneric = len(rt.TypeArgs) > 0
		}
	}
	return &Function{
		Name:           fi.Name,
		CName:          fi.CName,
		PkgID:          pkgID,
		IsGeneric:      isGeneric,
		Params:         params,
		RetType:        fi.Sig.Ret,
		Locals:         fl.locals,
		Body:           pruneUnusedLabels(fl.rootInstrs(), fl.usedLabels),
		IsAlwaysInline: fi.IsAlwaysInline,
		IsNoInline:     fi.IsNoInline,
		IsTest:         fi.IsTest,
		IsTestOnly:     fi.IsTestOnly,
		TestDesc:       fi.TestDesc,
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
	envLocal := &Local{CName: "_ll_env", Type: &types.Ptr{Elem: types.TypeVoid}}
	envTyped := &ExprCast{
		Val:  NewLocalExpr(envLocal),
		From: &types.Ptr{Elem: types.TypeVoid},
		To:   &types.Ptr{Elem: &types.Struct{CName: li.EnvCName}},
	}
	for _, capItem := range li.Captures {
		fl.captureFields[capItem.OuterSym] = &ExprField{
			Base:      envTyped,
			FieldName: capItem.CName,
			Typ:       capItem.Type,
			IsPtr:     true,
		}
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
				pLoc.MovedLocal = &Local{CName: pLoc.MovedFlag, Type: types.TypeBool}
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
		fl.emit(&InstrReturn{Expr: NewLocalExpr(retSlot)})
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
		loc.MovedLocal = &Local{CName: loc.MovedFlag, Type: types.TypeBool}
	}
	fl.locals = append(fl.locals, loc)
	return loc
}

func (fl *funcLowerer) allocStmtTemp(ty types.Type, initExpr Expr) *Local {
	loc := fl.allocSlot("_ll_tmp", ty)
	fl.assignLocal(loc, initExpr)
	if !ty.IsPlain() {
		fl.stmtTemps = append(fl.stmtTemps, loc)
	}
	return loc
}

func (fl *funcLowerer) assignLocal(loc *Local, expr Expr) {
	fl.emit(&InstrAssign{Dst: NewLocalExpr(loc), Expr: EnsureExpr(expr, loc.Type)})
	if loc.MovedLocal != nil {
		fl.emit(&InstrAssign{Dst: NewLocalExpr(loc.MovedLocal), Expr: NewLitBool(false)})
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
		fl.emit(&InstrDrop{Var: t, VarName: t.CName, Type: t.Type, MovedFlag: t.MovedFlag})
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
		fl.emit(&InstrDrop{Var: loc, VarName: loc.CName, Type: loc.Type, MovedFlag: loc.MovedFlag})
	}
}

func (fl *funcLowerer) emitDropsDownTo(stopIdx int) {
	for s := len(fl.scopes) - 1; s >= stopIdx; s-- {
		sc := fl.scopes[s]
		for i := len(sc.ownedLocals) - 1; i >= 0; i-- {
			loc := sc.ownedLocals[i]
			fl.emit(&InstrDrop{Var: loc, VarName: loc.CName, Type: loc.Type, MovedFlag: loc.MovedFlag})
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

func formatSpanPos(e ast.Expr) (string, int, int) {
	if e == nil {
		return "<unknown>", 0, 0
	}
	sp := e.GetSpan()
	if sp.Start.File == "" {
		return "<input>", sp.Start.Line, sp.Start.Col
	}
	cleanPath := filepath.Clean(sp.Start.File)
	if rel, err := filepath.Rel(".", cleanPath); err == nil && !strings.HasPrefix(rel, "..") {
		return rel, sp.Start.Line, sp.Start.Col
	}
	return filepath.Base(cleanPath), sp.Start.Line, sp.Start.Col
}

func formatSpanLoc(e ast.Expr) (string, int) {
	f, l, _ := formatSpanPos(e)
	return f, l
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
	if s.IsUnpack {
		fl.lowerUnpackVarStmt(s)
		return
	}
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
		loc.MovedLocal = &Local{CName: loc.MovedFlag, Type: types.TypeBool}
	}
	fl.locals = append(fl.locals, loc)
	fl.symLocals[sym] = loc

	if s.FallbackUnwind != nil {
		// `let a = opt_expr ?? <unwind>` / `let a = res_expr ?? <unwind>`
		fl.lowerGuardedUnwrap(s.Value, s.FallbackUnwind, "_ll_coalesce", loc)
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

// guardElemType returns what a `?? <unwind>` guard unwraps `ty` (an optional or a result) to.
func guardElemType(ty types.Type) types.Type {
	if o, ok := ty.(*types.Optional); ok {
		return o.Elem
	}
	vv, _ := types.AsResult(ty).ResultValue()
	return vv.Payload
}

// lowerGuardedUnwrap lowers `value ?? <unwind>` in statement position: it evaluates `value` (an
// optional or a result), runs `unwind` when it holds no value (dropping a result's failure
// first), and otherwise moves the unwrapped value into `dst`.
func (fl *funcLowerer) lowerGuardedUnwrap(value ast.Expr, unwind *ast.UnwindStmt, prefix string, dst *Local) {
	valTy := fl.info(value).Type
	fl.tc.visit(valTy)
	v := fl.lowerExprOwned(value, valTy)
	tmp := fl.allocSlot(prefix, valTy)
	fl.assignLocal(tmp, v)
	fl.flushStmtTemps()

	if en := types.AsResult(valTy); en != nil {
		fl.pushInstrBuf()
		fl.emit(&InstrDrop{Var: tmp, VarName: tmp.CName, Type: en, MovedFlag: tmp.MovedFlag})
		fl.lowerUnwindStmt(unwind)
		failBranch := fl.popInstrBuf()
		fl.emit(&InstrIf{
			Cond: &ExprUnary{Op: token.KwNot, Operand: resultIsValueExpr(NewLocalExpr(tmp), en), Typ: types.TypeBool},
			Then: failBranch,
		})
		fl.assignLocal(dst, resultValueExpr(NewLocalExpr(tmp), en))
		fl.emit(&InstrAssign{Dst: NewLocalExpr(tmp), Expr: NewLitZero(en)})
		return
	}

	optTy := valTy.(*types.Optional)
	fl.pushInstrBuf()
	fl.lowerUnwindStmt(unwind)
	elseBranch := fl.popInstrBuf()
	fl.emit(&InstrIf{
		Cond: &ExprUnary{Op: token.KwNot, Operand: optHasValueExpr(tmp, optTy), Typ: types.TypeBool},
		Then: elseBranch,
	})
	// Extract owned value out of `tmp` (freeing outer heap box if `optTy.IsBoxed()`)!
	fl.extractOwnedOptionalInto(tmp, optTy, dst)
}

func (fl *funcLowerer) extractOwnedOptionalInto(optSlot *Local, optTy *types.Optional, dst *Local) {
	if optTy.IsBoxed() {
		// Deref `*optSlot`, assign to `dst`, and free ONLY the heap shell (not inner fields)!
		fl.assignLocal(dst, &ExprDeref{Ptr: NewLocalExpr(optSlot), Typ: optTy.Elem})
		fl.emit(&InstrExpr{Expr: &ExprFree{Ptr: NewLocalExpr(optSlot)}})
		fl.emit(&InstrAssign{Dst: NewLocalExpr(optSlot), Expr: NewLitNone(optTy)})
	} else {
		fl.assignLocal(dst, &ExprField{Base: NewLocalExpr(optSlot), FieldName: "value", Typ: optTy.Elem})
		fl.emit(&InstrAssign{Dst: &ExprField{Base: NewLocalExpr(optSlot), FieldName: "has_value", Typ: types.TypeBool}, Expr: NewLitBool(false)})
	}
}

func (fl *funcLowerer) coerceUnpackedElem(valExpr Expr, srcTy, dstTy types.Type) Expr {
	if dstTy == nil || srcTy == nil || srcTy.Equals(dstTy) {
		return valExpr
	}
	if ifc, isIfc := dstTy.(*types.Interface); isIfc {
		fl.tc.visit(ifc)
		return fl.boxConcreteIntoInterface(valExpr, srcTy, ifc)
	}
	if optTy, isOpt := dstTy.(*types.Optional); isOpt && srcTy.Equals(optTy.Elem) {
		fl.tc.visit(optTy)
		return fl.wrapInOptionalExpr(valExpr, optTy)
	}
	return valExpr
}

func (fl *funcLowerer) lowerUnpackVarStmt(s *ast.VarStmt) {
	syms := fl.bm.UnpackVarSymbols[s]
	locs := make([]*Local, len(syms))
	for i, sym := range syms {
		if sym == nil {
			continue
		}
		fl.tc.visit(sym.Type)
		loc := &Local{
			CName: sym.CName,
			Type:  sym.Type,
		}
		if NeedsMovedFlag(sym.Type) {
			loc.MovedFlag = sym.CName + "_moved"
			loc.MovedLocal = &Local{CName: loc.MovedFlag, Type: types.TypeBool}
		}
		fl.locals = append(fl.locals, loc)
		fl.symLocals[sym] = loc
		locs[i] = loc
	}

	var tupSlot *Local
	var tupValTy types.Type
	if s.FallbackUnwind != nil {
		tupValTy = guardElemType(fl.info(s.Value).Type)
		fl.tc.visit(tupValTy)
		tupSlot = fl.allocSlot("_ll_unpack", tupValTy)
		fl.lowerGuardedUnwrap(s.Value, s.FallbackUnwind, "_ll_coalesce", tupSlot)
	} else {
		tupValTy = fl.info(s.Value).Type
		fl.tc.visit(tupValTy)
		valExpr := fl.lowerExprOwned(s.Value, tupValTy)
		tupSlot = fl.allocSlot("_ll_unpack", tupValTy)
		fl.assignLocal(tupSlot, valExpr)
		fl.flushStmtTemps()
	}

	var tupTy *types.Tuple
	if t, isTup := tupValTy.(*types.Tuple); isTup {
		tupTy = t
	} else if d, isDist := tupValTy.(*types.Distinct); isDist {
		tupTy = d.Base.(*types.Tuple)
	}
	if tupTy == nil {
		return
	}
	fl.tc.visit(tupTy)

	for i := range s.UnpackVars {
		if i >= len(tupTy.Elems) {
			break
		}
		elemTy := tupTy.Elems[i]
		fl.tc.visit(elemTy)
		fieldExpr := &ExprField{
			Base:      NewLocalExpr(tupSlot),
			FieldName: fmt.Sprintf("_%d", i),
			FieldIdx:  i,
			Typ:       elemTy,
			IsPtr:     false,
		}
		loc := locs[i]
		if loc == nil {
			if !elemTy.IsPlain() {
				fl.emit(&InstrExpr{
					Expr: &ExprDrop{
						Ptr:     &ExprAddrOf{Place: fieldExpr, Typ: &types.Ptr{Elem: elemTy}},
						ValType: elemTy,
					},
				})
			}
			continue
		}
		coerced := fl.coerceUnpackedElem(fieldExpr, elemTy, loc.Type)
		fl.assignLocal(loc, coerced)
		if !loc.Type.IsPlain() {
			cur := fl.scopes[len(fl.scopes)-1]
			cur.ownedLocals = append(cur.ownedLocals, loc)
		}
	}
}

func (fl *funcLowerer) lowerAssignStmt(s *ast.AssignStmt) {
	if s.IsUnpack {
		fl.lowerUnpackAssignStmt(s)
		return
	}
	tgt := s.Target
	if ue, isUnsafe := tgt.(*ast.UnsafeExpr); isUnsafe {
		tgt = ue.Expr
	}
	// Shorthand `map[key] = val` -> `ll_map_insert(&map, key, val)`
	if idx, isIdx := tgt.(*ast.IndexExpr); isIdx && s.Op == token.Assign && s.FallbackUnwind == nil {
		if mapTy, isMap := fl.info(idx.Base).Type.(*types.Map); isMap {
			fl.tc.visit(mapTy)
			mPtr := fl.lowerLValueAddr(idx.Base)
			kExpr := fl.lowerExprOwned(idx.Index, mapTy.Key)
			vExpr := fl.lowerExprOwned(s.Value, mapTy.Value)
			fl.emit(&InstrExpr{
				Expr: &ExprMapInsert{
					MapPtr:  EnsureExpr(mPtr, &types.Ptr{Elem: mapTy}),
					Key:     EnsureExpr(kExpr, mapTy.Key),
					Val:     EnsureExpr(vExpr, mapTy.Value),
					MapType: mapTy,
				},
			})
			fl.flushStmtTemps()
			return
		}
	}

	targetTy := fl.info(s.Target).Type

	if s.FallbackUnwind != nil {
		unwrappedTmp := fl.allocSlot("_ll_asgn_val", guardElemType(fl.info(s.Value).Type))
		fl.lowerGuardedUnwrap(s.Value, s.FallbackUnwind, "_ll_asgn_opt", unwrappedTmp)
		fl.assignToTargetLValue(s.Target, targetTy, NewLocalExpr(unwrappedTmp))
		return
	}

	if s.Op == token.Assign {
		// Evaluate RHS into a temporary FIRST before dropping old LHS Resource value
		// (so self-referential reassignments like `s = s + "!"` never read freed `s`!)
		valExpr := fl.lowerExprOwned(s.Value, targetTy)
		rhsTmp := fl.allocSlot("_ll_rhs", targetTy)
		fl.assignLocal(rhsTmp, valExpr)
		fl.flushStmtTemps()
		fl.assignToTargetLValue(s.Target, targetTy, NewLocalExpr(rhsTmp))
		return
	}

	// Compound assignment (`+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`, `>>=`)
	lhsPtr := fl.lowerLValueAddr(s.Target)
	rhsExpr := fl.lowerExprRead(s.Value)
	file, line := formatSpanLoc(s.Target)
	fl.emitCompoundAssign(s.Op, targetTy, lhsPtr, rhsExpr, file, line)
	fl.flushStmtTemps()
}

func (fl *funcLowerer) lowerUnpackAssignStmt(s *ast.AssignStmt) {
	var tupSlot *Local
	var tupValTy types.Type
	if s.FallbackUnwind != nil {
		tupValTy = guardElemType(fl.info(s.Value).Type)
		fl.tc.visit(tupValTy)
		tupSlot = fl.allocSlot("_ll_unpack_asgn", tupValTy)
		fl.lowerGuardedUnwrap(s.Value, s.FallbackUnwind, "_ll_asgn_opt", tupSlot)
	} else {
		tupValTy = fl.info(s.Value).Type
		fl.tc.visit(tupValTy)
		valExpr := fl.lowerExprOwned(s.Value, tupValTy)
		tupSlot = fl.allocSlot("_ll_unpack_asgn", tupValTy)
		fl.assignLocal(tupSlot, valExpr)
		fl.flushStmtTemps()
	}

	var tupTy *types.Tuple
	if t, isTup := tupValTy.(*types.Tuple); isTup {
		tupTy = t
	} else if d, isDist := tupValTy.(*types.Distinct); isDist {
		tupTy = d.Base.(*types.Tuple)
	}
	if tupTy == nil {
		return
	}
	fl.tc.visit(tupTy)

	for i, tgt := range s.UnpackTargets {
		if i >= len(tupTy.Elems) {
			break
		}
		elemTy := tupTy.Elems[i]
		fl.tc.visit(elemTy)
		fieldExpr := &ExprField{
			Base:      NewLocalExpr(tupSlot),
			FieldName: fmt.Sprintf("_%d", i),
			FieldIdx:  i,
			Typ:       elemTy,
			IsPtr:     false,
		}
		if id, isId := tgt.(*ast.IdentExpr); isId && id.Name == "_" {
			if !elemTy.IsPlain() {
				fl.emit(&InstrExpr{
					Expr: &ExprDrop{
						Ptr:     &ExprAddrOf{Place: fieldExpr, Typ: &types.Ptr{Elem: elemTy}},
						ValType: elemTy,
					},
				})
			}
			continue
		}
		tgtTy := fl.info(tgt).Type
		fl.tc.visit(tgtTy)
		coerced := fl.coerceUnpackedElem(fieldExpr, elemTy, tgtTy)
		elemTmp := fl.allocSlot("_ll_unpack_el", tgtTy)
		fl.assignLocal(elemTmp, coerced)
		fl.assignToTargetLValue(tgt, tgtTy, NewLocalExpr(elemTmp))
	}
}

func (fl *funcLowerer) assignToTargetLValue(target ast.Expr, targetTy types.Type, newValExpr Expr) {
	if ue, isUnsafe := target.(*ast.UnsafeExpr); isUnsafe {
		target = ue.Expr
	}
	isRawDeref := false
	if u, ok := target.(*ast.UnaryExpr); ok && u.Op == token.Star {
		isRawDeref = true
	}
	if id, isId := target.(*ast.IdentExpr); isId {
		if sym := fl.info(id).Sym; sym != nil {
			if loc, ok := fl.symLocals[sym]; ok && !loc.IsMutPtr {
				if !targetTy.IsPlain() {
					fl.emit(&InstrDrop{Var: loc, VarName: loc.CName, Type: targetTy, MovedFlag: loc.MovedFlag})
				}
				fl.assignLocal(loc, newValExpr)
				return
			}
		}
	}
	lhsPtr := fl.lowerLValueAddr(target)
	if !targetTy.IsPlain() && !isRawDeref {
		fl.emit(&InstrExpr{Expr: &ExprDrop{Ptr: EnsureExpr(lhsPtr, targetTy), ValType: targetTy}})
	}
	fl.emit(&InstrAssign{Dst: &ExprDeref{Ptr: EnsureExpr(lhsPtr, targetTy), Typ: targetTy}, Expr: EnsureExpr(newValExpr, targetTy)})
}

func (fl *funcLowerer) emitCompoundAssign(op token.Kind, ty types.Type, lhsPtr Expr, rhsExpr Expr, file string, line int) {
	if op == token.PlusEq && ty.Equals(types.TypeString) {
		fl.emit(&InstrExpr{Expr: &ExprStringPush{StrPtr: EnsureExpr(lhsPtr, types.TypeString), Item: EnsureExpr(rhsExpr, types.TypeString)}})
		return
	}
	var binOp token.Kind
	switch op {
	case token.PlusEq:
		binOp = token.Plus
	case token.MinusEq:
		binOp = token.Minus
	case token.StarEq:
		binOp = token.Star
	case token.SlashEq:
		binOp = token.Slash
	case token.PercentEq:
		binOp = token.Percent
	case token.AmpEq:
		binOp = token.Amp
	case token.PipeEq:
		binOp = token.Pipe
	case token.CaretEq:
		binOp = token.Caret
	case token.ShlEq:
		binOp = token.Shl
	case token.ShrEq:
		binOp = token.Shr
	default:
		panic(fmt.Sprintf("unsupported compound assign op %v", op))
	}
	lhsDeref := &ExprDeref{Ptr: EnsureExpr(lhsPtr, ty), Typ: ty}
	fl.emit(&InstrAssign{
		Dst: lhsDeref,
		Expr: &ExprBinary{
			Op:    binOp,
			Left:  lhsDeref,
			Right: EnsureExpr(rhsExpr, ty),
			Typ:   ty,
			File:  file,
			Line:  line,
		},
	})
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
			// Target is a value-producing loop (`while <- val`):
			elemTy := tgt.loopElemTy
			needsWrap := tgt.isForNoElse
			uInf := fl.info(u.Value)
			if rawTy := uInf.Type; tgt.isForNoElse && ((rawTy != nil && rawTy.Equals(tgt.resultVar.Type)) || uInf.WrapOptional) {
				elemTy = tgt.resultVar.Type
				needsWrap = false
			}
			valExpr := fl.lowerExprOwned(u.Value, elemTy)
			valTmp := fl.allocSlot("_ll_uval", elemTy)
			fl.assignLocal(valTmp, valExpr)
			fl.flushStmtTemps()

			// Drop any prior iteration value held in `tgt.resultVar` before overwriting!
			fl.storeLoopRunningResult(tgt, NewLocalExpr(valTmp), needsWrap)
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
			fl.emit(&InstrReturn{Expr: NewLocalExpr(tgt.resultVar)})
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

func (fl *funcLowerer) storeLoopRunningResult(loopScope *mirScope, elemExpr Expr, needsWrap bool) {
	if loopScope.resultVar == nil || loopScope.hasValVar == nil {
		return
	}
	// If a previous iteration already wrote a Resource value into `resultVar`, drop it first!
	if !loopScope.resultVar.Type.IsPlain() {
		fl.emit(&InstrIf{
			Cond: NewLocalExpr(loopScope.hasValVar),
			Then: []Instr{
				&InstrDrop{
					Var:       loopScope.resultVar,
					VarName:   loopScope.resultVar.CName,
					Type:      loopScope.resultVar.Type,
					MovedFlag: loopScope.resultVar.MovedFlag,
				},
			},
		})
	}
	if needsWrap {
		optRes := loopScope.resultVar.Type.(*types.Optional)
		fl.assignLocal(loopScope.resultVar, fl.wrapInOptionalExpr(elemExpr, optRes))
	} else {
		fl.assignLocal(loopScope.resultVar, elemExpr)
	}
	fl.emit(&InstrAssign{Dst: NewLocalExpr(loopScope.hasValVar), Expr: NewLitBool(true)})
}

// sortedExprInfos returns the non-nil entries of `infos` ordered by source span (file, start,
// end), breaking ties by mangled type names, so type collection does not depend on Go map order.
func sortedExprInfos(infos map[ast.Expr]*sema.ExprInfo) []*sema.ExprInfo {
	type entry struct {
		span diag.Span
		key  string
		inf  *sema.ExprInfo
	}
	entries := make([]entry, 0, len(infos))
	for expr, inf := range infos {
		if inf == nil {
			continue
		}
		var span diag.Span
		if expr != nil {
			span = expr.GetSpan()
		}
		entries = append(entries, entry{span: span, key: exprInfoTypeKey(inf), inf: inf})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].span, entries[j].span
		if a.Start.File != b.Start.File {
			return a.Start.File < b.Start.File
		}
		if a.Start.Offset != b.Start.Offset {
			return a.Start.Offset < b.Start.Offset
		}
		if a.End.Offset != b.End.Offset {
			return a.End.Offset < b.End.Offset
		}
		return entries[i].key < entries[j].key
	})
	out := make([]*sema.ExprInfo, len(entries))
	for i, e := range entries {
		out[i] = e.inf
	}
	return out
}

func exprInfoTypeKey(inf *sema.ExprInfo) string {
	var sb strings.Builder
	for _, t := range []types.Type{inf.Type, inf.IntrinsicType} {
		if t != nil {
			sb.WriteString(t.Mangled())
		}
		sb.WriteByte('|')
	}
	if inf.BoxInterface != nil {
		sb.WriteString(inf.BoxInterface.Mangled())
	}
	return sb.String()
}
