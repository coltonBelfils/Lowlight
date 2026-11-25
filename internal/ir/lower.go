package ir

import (
	"fmt"
	"os"
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

	// Seed type collector with declared & instantiated runtime types (omitting `@comptime_only`)
	for _, en := range prog.Enums {
		if !en.IsComptimeOnly {
			tc.visit(en)
		}
	}
	for _, dt := range prog.Distincts {
		if !dt.IsComptimeOnly {
			tc.visit(dt)
		}
	}
	for _, st := range prog.Structs {
		if !st.IsComptimeOnly {
			tc.visit(st)
		}
	}
	for _, ifc := range prog.Interfaces {
		if !ifc.IsComptimeOnly {
			tc.visit(ifc)
		}
	}
	for _, ef := range prog.ExternFuncs {
		for _, p := range ef.Sig.Params {
			tc.visit(p.Type)
		}
		tc.visit(ef.Sig.Ret)
	}
	for _, tv := range prog.TopVars {
		if tv.Sym.ComptimeOnly {
			continue
		}
		tc.visit(tv.Sym.Type)
	}
	for _, fi := range prog.Funcs {
		if fi.ComptimeOnly || types.FindComptimeOnlyType(fi.Sig) != nil {
			continue
		}
		for _, p := range fi.Sig.Params {
			tc.visit(p.Type)
		}
		tc.visit(fi.Sig.Ret)
	}
	for _, li := range prog.Lambdas {
		if li.ComptimeOnly {
			continue
		}
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
		if !st.IsComptimeOnly {
			visitBodyMaps(prog.StructBodyMaps[st])
		}
	}
	for _, en := range prog.Enums {
		if !en.IsComptimeOnly {
			visitBodyMaps(prog.EnumBodyMaps[en])
		}
	}
	for _, fi := range prog.Funcs {
		if !fi.ComptimeOnly {
			visitBodyMaps(fi.BodyMaps)
		}
	}
	for _, li := range prog.Lambdas {
		if !li.ComptimeOnly {
			visitBodyMaps(li.BodyMaps)
		}
	}

	// `@test_only` externs are declared only in test builds.
	var externs []*sema.ExternFunc
	for _, ef := range prog.ExternFuncs {
		if !ef.IsTestOnly || testMode {
			externs = append(externs, ef)
		}
	}
	irp := &Program{
		Checked:       prog,
		ExternHeaders: prog.ExternHeaders,
		ExternFuncs:   externs,
		VTables:       vtables,
		Release:       release,
		TestMode:      testMode,
		TargetTriple:  prog.Target.Triple,
	}

	registerThunk := &thunkRegistry{
		irp:         irp,
		tc:          tc,
		seen:        make(map[string]bool),
		testMode:    testMode,
		usedLambdas: make(map[*sema.LambdaInfo]bool),
	}

	// 1. Lower top-level `bind` & `const` bindings (omitting `@comptime_only` symbols)
	for _, tv := range prog.TopVars {
		if tv.Sym.ComptimeOnly {
			continue
		}
		if tv.Sym.IsTestOnly && !testMode {
			continue
		}
		pkgID := ""
		if tv.Mod != nil && tv.Mod.Pkg != nil {
			pkgID = tv.Mod.Pkg.ID
		}
		if tv.Sym.IsConst && tv.Sym.ConstVal != nil {
			if sd := sema.ExportStaticData(tv.Sym.ConstVal, tv.Sym.Type); sd != nil {
				sv := lowerStaticData(sd, tc, registerThunk)
				irp.Globals = append(irp.Globals, &Global{
					Name:       tv.Sym.Name,
					CName:      tv.Sym.CName,
					PkgID:      pkgID,
					Type:       tv.Sym.Type,
					IsConst:    true,
					StaticInit: sv,
				})
				continue
			}
		}
		fl := newFuncLowerer(prog, prog.TopBodyMaps, tc, release, registerThunk)
		fl.pushScope(0, "", "", nil, nil, false)
		exprStr := fl.lowerExprOwned(tv.Decl.Value, tv.Sym.Type)
		fl.flushStmtTemps()
		fl.popScopeEmitDrops()
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

	// 2. Lower all concrete & monomorphized functions/methods (omitting `@comptime_only`)
	for _, fi := range prog.Funcs {
		if fi.ComptimeOnly || types.FindComptimeOnlyType(fi.Sig) != nil {
			continue
		}
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

	// 3. Lower all runtime-reachable anonymous lambdas (omitting unused lambdas inside `@comptime_only`/`comptime`)
	loweredLambdas := make(map[*sema.LambdaInfo]bool)
	for {
		progressed := false
		for _, li := range prog.Lambdas {
			if loweredLambdas[li] {
				continue
			}
			if li.ComptimeOnly && !registerThunk.usedLambdas[li] {
				continue
			}
			loweredLambdas[li] = true
			progressed = true
			fn := lowerLambda(prog, li, tc, release, registerThunk)
			irp.Funcs = append(irp.Funcs, fn)
		}
		if !progressed {
			break
		}
	}

	// Populate finalized topological type lists
	irp.OrderedTypes = tc.ordered
	irp.Enums = tc.ens
	irp.Distincts = tc.dists
	irp.Structs = tc.sts
	irp.Optionals = tc.opts
	irp.Vectors = tc.vecs
	irp.Arrays = tc.arrs
	irp.Tuples = tc.tups
	irp.FuncTypes = tc.fns
	irp.Interfaces = tc.ifcs
	return irp
}

// -----------------------------------------------------------------------------
// Function Lowerer State & Scope Management
// -----------------------------------------------------------------------------

type mirScope struct {
	targetID           int
	breakLabel         string
	continueLabel      string
	resultVar          *Local
	hasValVar          *Local // non-nil in value-producing `for` loops to drop overwritten iteration values
	collectIterSlot    *Local // non-nil inside fixed-composite-collecting `comptime for`: current iteration's field/elem slot
	collectUsedDefault *Local // non-nil inside struct-collecting `comptime for`: set true when bare `next <-` skips iteration
	loopElemTy         types.Type
	isForNoElse        bool
	isFunc             bool
	ownedLocals        []*Local
}

type funcLowerer struct {
	prog          *sema.CheckedProgram
	bm            *sema.BodyMaps
	tc            *typeCollector
	release       bool
	registerThunk *thunkRegistry

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
	registerThunk *thunkRegistry,
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
	registerThunk *thunkRegistry,
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
		File:           fi.DeclSpan.Start.File,
		Line:           fi.DeclSpan.Start.Line,
		Col:            fi.DeclSpan.Start.Col,
	}
}

func lowerLambda(
	prog *sema.CheckedProgram,
	li *sema.LambdaInfo,
	tc *typeCollector,
	release bool,
	registerThunk *thunkRegistry,
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
		Name:    li.CName,
		CName:   li.CName,
		Params:  params,
		RetType: li.Sig.Ret,
		Locals:  fl.locals,
		Body:    pruneUnusedLabels(fl.rootInstrs(), fl.usedLabels),
		// An `extern fn` literal captures nothing and is called by C, so it takes no environment.
		IsLambda:   !li.Sig.Extern,
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
	if !filepath.IsAbs(cleanPath) {
		return filepath.ToSlash(cleanPath), sp.Start.Line, sp.Start.Col
	}
	if cwd, err := os.Getwd(); err == nil {
		if absCwd, errCwd := filepath.Abs(cwd); errCwd == nil {
			if rel, errRel := filepath.Rel(absCwd, cleanPath); errRel == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel), sp.Start.Line, sp.Start.Col
			}
			// Handle symlinked working directories (such as macOS /var -> /private/var).
			if realCwd, err1 := filepath.EvalSymlinks(absCwd); err1 == nil {
				if realPath, err2 := filepath.EvalSymlinks(cleanPath); err2 == nil {
					if rel, errRel := filepath.Rel(realCwd, realPath); errRel == nil && rel != "." && !strings.HasPrefix(rel, "..") {
						return filepath.ToSlash(rel), sp.Start.Line, sp.Start.Col
					}
				}
			}
		}
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

	if s.IsConst && s.FallbackUnwind == nil && fl.bm != nil {
		if cv, ok := fl.bm.ConstVarVals[sym]; ok && cv != nil {
			if sd := sema.ExportStaticData(cv, sym.Type); sd != nil {
				sv := lowerStaticData(sd, fl.tc, fl.registerThunk)
				fl.assignLocal(loc, &ExprConst{Static: sv, Typ: sym.Type})
				if !sym.Type.IsPlain() {
					cur := fl.scopes[len(fl.scopes)-1]
					cur.ownedLocals = append(cur.ownedLocals, loc)
				}
				return
			}
		}
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
	// Shorthand `map[key] = val` -> `map.insert(key, val)`
	if idx, isIdx := tgt.(*ast.IndexExpr); isIdx && s.Op == token.Assign && s.FallbackUnwind == nil {
		mapTy := fl.info(idx.Base).Type
		if keyTy, valTy, isMap := types.MapKV(mapTy); isMap {
			fl.tc.visit(mapTy)
			mPtr := fl.lowerLValueAddr(idx.Base)
			kExpr := fl.lowerExprOwned(idx.Index, keyTy)
			vExpr := fl.lowerExprOwned(s.Value, valTy)
			fl.emit(&InstrExpr{Expr: fl.callMethod(mapTy, "insert",
				EnsureExpr(mPtr, &types.Ptr{Elem: mapTy}),
				EnsureExpr(kExpr, keyTy),
				EnsureExpr(vExpr, valTy),
			)})
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
		// Assigned right here and read right below, with nothing to drop if plain.
		rhsTmp.NoZeroInit = targetTy.IsPlain()
		fl.assignLocal(rhsTmp, valExpr)
		fl.flushStmtTemps()
		fl.assignToTargetLValue(s.Target, targetTy, NewLocalExpr(rhsTmp))
		return
	}

	// Compound assignment (`+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`, `>>=`)
	lhsPtr := fl.lowerLValueAddrMut(s.Target)
	rhsExpr := fl.lowerExprRead(s.Value)
	if fi := fl.info(s.Target).OpMethod; fi != nil {
		fl.emitOperatorAssign(fi, targetTy, lhsPtr, rhsExpr)
		fl.flushStmtTemps()
		return
	}
	file, line := formatSpanLoc(s.Target)
	fl.emitCompoundAssign(s.Op, targetTy, lhsPtr, rhsExpr, file, line)
	fl.flushStmtTemps()
}

// emitOperatorAssign lowers `a += b` for a type that implements the operator with a method:
// `a.add_assign(b)`, or `a = a.add(b)` when the type only has `add`.
func (fl *funcLowerer) emitOperatorAssign(fi *sema.FuncInstance, ty types.Type, lhsPtr, rhsExpr Expr) {
	ptrTy := &types.Ptr{Elem: ty}
	dst := fl.allocSlot("_ll_opdst", ptrTy)
	fl.assignLocal(dst, EnsureExpr(lhsPtr, ptrTy))
	ref := &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig}
	rhs := EnsureExpr(rhsExpr, ty)
	if fi.Sig.Params[0].Mode == types.ModeMut {
		fl.emit(&InstrExpr{Expr: &ExprCall{Callee: ref, Args: []Expr{NewLocalExpr(dst), rhs}, Typ: types.TypeVoid}})
		return
	}
	// The old value is dropped only after the new one is computed from it.
	val := fl.allocSlot("_ll_opval", ty)
	fl.assignLocal(val, &ExprCall{Callee: ref, Args: []Expr{&ExprDeref{Ptr: NewLocalExpr(dst), Typ: ty}, rhs}, Typ: ty})
	if !ty.IsPlain() {
		fl.emit(&InstrExpr{Expr: &ExprDrop{Ptr: NewLocalExpr(dst), ValType: ty}})
	}
	fl.emit(&InstrAssign{Dst: &ExprDeref{Ptr: NewLocalExpr(dst), Typ: ty}, Expr: NewLocalExpr(val)})
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
	lhsPtr := fl.lowerLValueAddrMut(target)
	if !targetTy.IsPlain() && !isRawDeref {
		fl.emit(&InstrExpr{Expr: &ExprDrop{Ptr: EnsureExpr(lhsPtr, targetTy), ValType: targetTy}})
	}
	fl.emit(&InstrAssign{Dst: &ExprDeref{Ptr: EnsureExpr(lhsPtr, targetTy), Typ: targetTy}, Expr: EnsureExpr(newValExpr, targetTy)})
}

func (fl *funcLowerer) emitCompoundAssign(op token.Kind, ty types.Type, lhsPtr Expr, rhsExpr Expr, file string, line int) {
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
	if u.Value != nil {
		if uinf.IsContinue && tgt.collectIterSlot != nil {
			destTy := tgt.collectIterSlot.Type
			valExpr := fl.lowerExprOwned(u.Value, destTy)
			fl.assignLocal(tgt.collectIterSlot, valExpr)
			fl.flushStmtTemps()
		} else if tgt.resultVar != nil {
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
			fl.lowerVoidExpr(u.Value)
			fl.flushStmtTemps()
		}
	} else {
		if uinf.IsContinue && uinf.IsStructFieldDefault && tgt.collectUsedDefault != nil {
			fl.emit(&InstrAssign{Dst: NewLocalExpr(tgt.collectUsedDefault), Expr: NewLitBool(true)})
		}
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

// thunkRegistry collects the wrappers function values need, emitting each once.
type thunkRegistry struct {
	irp         *Program
	tc          *typeCollector
	seen        map[string]bool
	testMode    bool
	usedLambdas map[*sema.LambdaInfo]bool
}

func lowerStaticData(sd *sema.StaticData, tc *typeCollector, reg *thunkRegistry) *StaticVal {
	if sd == nil {
		return nil
	}
	if sd.Type != nil {
		tc.visit(sd.Type)
	}
	sv := &StaticVal{
		Kind:        sd.Kind,
		Type:        sd.Type,
		Uint:        sd.Uint,
		SignedVal:   sd.SignedVal,
		IsSigned:    sd.IsSigned,
		Float:       sd.Float,
		Bool:        sd.Bool,
		Char:        sd.Char,
		Str:         sd.Str,
		HasRawBytes: sd.HasRawBytes,
		RawBytes:    sd.RawBytes,
		HasOpt:      sd.HasOpt,
	}
	effTy := sd.Type
	for {
		if d, isDist := effTy.(*types.Distinct); isDist {
			effTy = d.Base
		} else {
			break
		}
	}
	switch sd.Kind {
	case sema.StaticList:
		if vecTy, ok := effTy.(*types.Vector); ok {
			tc.visit(vecTy)
		} else if st, ok := effTy.(*types.Struct); ok && len(st.TypeArgs) == 1 {
			tc.visit(&types.Vector{Elem: st.TypeArgs[0]})
		}
		sv.Elems = make([]*StaticVal, len(sd.Elems))
		for i, el := range sd.Elems {
			sv.Elems[i] = lowerStaticData(el, tc, reg)
		}
	case sema.StaticArray, sema.StaticTuple:
		sv.Elems = make([]*StaticVal, len(sd.Elems))
		for i, el := range sd.Elems {
			sv.Elems[i] = lowerStaticData(el, tc, reg)
		}
	case sema.StaticStruct:
		if st, ok := effTy.(*types.Struct); ok {
			sv.Fields = make([]StaticStructField, len(st.Fields))
			for i, f := range st.Fields {
				sv.Fields[i] = StaticStructField{
					Name:  f.CName,
					Value: lowerStaticData(sd.Fields[f.Name], tc, reg),
				}
			}
		}
	case sema.StaticEnum:
		if en, ok := effTy.(*types.Enum); ok && sd.VariantIdx >= 0 && sd.VariantIdx < len(en.Variants) {
			vr := en.Variants[sd.VariantIdx]
			sv.VariantVal = vr.Value
			sv.VariantCN = vr.CName
			if sd.Payload != nil {
				sv.Payload = lowerStaticData(sd.Payload, tc, reg)
			}
		}
	case sema.StaticOpt:
		if sd.HasOpt && sd.OptElem != nil {
			sv.OptElem = lowerStaticData(sd.OptElem, tc, reg)
		}
	case sema.StaticFn:
		if sd.Fn != nil {
			fnTy := sd.Fn.Sig
			if fnTy.Extern {
				sv.FnEntry = reg.cCallback(sd.Fn.CName, fnTy)
			} else {
				sv.FnEntry = reg.static(sd.Fn, fnTy)
			}
		} else if sd.Lambda != nil {
			if reg != nil && reg.usedLambdas != nil {
				reg.usedLambdas[sd.Lambda] = true
			}
			sv.FnEntry = sd.Lambda.CName
		}
	}
	return sv
}

// static returns the environment-taking thunk that makes top-level function `fi` an `fn` value.
func (r *thunkRegistry) static(fi *sema.FuncInstance, fnTy *types.Func) string {
	tname := StaticFuncThunkName(fi.CName, fnTy)
	if !r.seen[tname] {
		r.seen[tname] = true
		r.tc.visit(fnTy)
		r.irp.StaticThunks = append(r.irp.StaticThunks, &StaticThunk{ThunkCName: tname, TargetFunc: fi, FuncType: fnTy})
	}
	return tname
}

// cCallback returns the function C should be given for the `extern fn` named `target`: the
// function itself, or in a test build a wrapper that keeps a panic from jumping back to the test
// runner across C frames.
func (r *thunkRegistry) cCallback(target string, fnTy *types.Func) string {
	if !r.testMode {
		return target
	}
	tname := target + "_ccb"
	if !r.seen[tname] {
		r.seen[tname] = true
		r.tc.visit(fnTy)
		r.irp.StaticThunks = append(r.irp.StaticThunks, &StaticThunk{ThunkCName: tname, FuncType: fnTy, CCallback: true, Target: target})
	}
	return tname
}
