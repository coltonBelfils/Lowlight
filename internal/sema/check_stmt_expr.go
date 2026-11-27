package sema

import (
	"fmt"
	"strconv"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

type cfKind int

const (
	cfBlock cfKind = iota
	cfIf
	cfMatch
	cfFor
	cfWhile
	cfFunc
)

type inferredYield struct {
	expr ast.Expr
	ty   types.Type
	span diag.Span
}

type cfTarget struct {
	id                 int
	kind               cfKind
	label              string
	funcDepth          int
	outerScopeDepth    int
	maxYieldArenaDepth int
	expectedType       types.Type         // nil if inferred bottom-up
	isCollecting       bool               // true for value-producing collection/aggregate `for` / `comptime for`
	collectColType     types.Type         // target collection type in collecting `for` / `comptime for`
	collectFixedTarget types.Type         // non-nil when collecting into Struct, Tuple, or StackArray via `comptime for(...) T:`
	collectStructField *types.StructField // active target field for current iteration when `collectFixedTarget` is a `*types.Struct`
	isForNoElse        bool               // true when `for` has no `else`, so loop yields `?Elem` while `for <- v` supplies `Elem`
	isInferredFunc     bool               // true when checking a function/lambda whose return type is inferred
	hasVoidUnwind      bool               // true if bare `func <-` targeted this inferred function
	yieldTypes         []types.Type       // collected types from unwinds to unify/infer
	breakColTypes      []types.Type       // collected collection types from `for <- col_val` when `collectColType` was initially inferred
	yieldExprs         []inferredYield
	hitValueExit       bool
}

type scopeFrame struct {
	depth int
	vars  map[string]*Symbol
}

type optChainErrEntry struct {
	expr ast.Expr
	emit func()
}

type funcChecker struct {
	probing            int // > 0 while checking an argument only to infer generic type arguments
	a                  *Analyzer
	mod                *Module
	subst              map[string]types.Type
	scopes             []*scopeFrame
	moves              map[*Symbol]MoveState
	activeBorrows      map[*Symbol]int
	arenaPinnedDepth   map[*Symbol]int     // minimum scope depth at which an owned `Arena` is pinned by live `ref[T]` values
	mutUnwrapOrigin    map[*Symbol]*Symbol // maps unwrapped `&user` -> `user?` so mutating `user` is allowed while reassigning `user?` is blocked
	cfStack            []*cfTarget
	unsafeDepth        int
	comptimeExprDepth  int
	allowComptimeChain int // > 0 while checking base of a `MemberExpr`/`IndexExpr` projecting out of `@comptime_only`
	comptimeMinScope   int // minimum ScopeDepth of non-const locals visible inside a `comptime` block
	unrolledStmts      int // cumulative unrolled statements in this function (H15)
	funcDepth          int
	hasMutArenaParam   bool
	nextLocalID        int

	// For lambda capture tracking: stack of active lambda frames
	lambdaStack []*lambdaFrame

	curFn               *FuncInstance
	curTopVar           *TopVarInit
	inferVoidOrValue    bool
	ifUnwrapCondExpr    ast.Expr
	pendingOptChainErrs []optChainErrEntry
	pendingOptChainMap  map[ast.Expr]int
}

func (fc *funcChecker) pushConstSubstSymbols() {
	if len(fc.subst) == 0 || len(fc.scopes) == 0 {
		return
	}
	scope := fc.scopes[len(fc.scopes)-1]
	for k, v := range fc.subst {
		cv, isCV := v.(*types.ConstValue)
		if !isCV || cv == nil {
			continue
		}
		scope.vars[k] = &Symbol{
			Name:          k,
			CName:         "ll_cparam_" + types.SanitizeCIdent(k),
			Mod:           fc.mod,
			Type:          cv.ConstType,
			IsBind:        true,
			IsConst:       true,
			IsConstParam:  true,
			BindKind:      BindOwned,
			ScopeDepth:    scope.depth,
			DeclFuncDepth: fc.funcDepth,
			IsConstInt:    cv.Kind == types.ConstInt,
			ConstIntVal:   cv.IntVal,
			ConstVal:      constValueToCtVal(cv),
		}
	}
}

func (fc *funcChecker) inConstContext() bool {
	if fc.comptimeExprDepth > 0 {
		return true
	}
	if fc.curFn != nil && fc.curFn.IsConst {
		return true
	}
	if fc.curTopVar != nil && fc.curTopVar.Sym != nil && fc.curTopVar.Sym.IsConst {
		return true
	}
	return false
}

func (fc *funcChecker) inConditionalGenericBody() bool {
	return fc.comptimeExprDepth == 0 &&
		fc.curFn != nil &&
		fc.curFn.IsConst &&
		(fc.curFn.IsGenericInst || fc.curFn.IsDerived) &&
		!fc.curFn.ComptimeOnly
}

func (fc *funcChecker) buildConstRelated(causeFn *FuncInstance) []diag.RelatedInfo {
	var rel []diag.RelatedInfo
	cur := causeFn
	seen := make(map[*FuncInstance]bool)
	for cur != nil && len(rel) < 8 && !seen[cur] {
		seen[cur] = true
		if cur.ConstFail != nil {
			rel = append(rel, diag.RelatedInfo{
				Span:    cur.ConstFail.Span,
				Message: fmt.Sprintf("in `%s`: %s", cur.Name, cur.ConstFail.Message),
			})
			cur = cur.ConstFail.CauseFn
			continue
		}
		if cur.DeclSpan.Start.File != "" {
			rel = append(rel, diag.RelatedInfo{
				Span:    cur.DeclSpan,
				Message: fmt.Sprintf("`%s` is declared `bind` (runtime-only) here", cur.Name),
			})
		}
		break
	}
	return rel
}

func (fc *funcChecker) recordConstViolation(span diag.Span, msg, hint string, causeFn *FuncInstance) {
	if fc.probing > 0 {
		return
	}
	if fc.inConditionalGenericBody() {
		fc.curFn.IsConst = false
		if fc.curFn.ConstFail == nil {
			fc.curFn.ConstFail = &ConstFailReason{
				Span:    span,
				Message: msg,
				Hint:    hint,
				CauseFn: causeFn,
			}
		}
		return
	}
	fc.a.Rep.Report(diag.Diagnostic{
		Severity: diag.Error,
		Span:     span,
		Message:  msg,
		Hint:     hint,
		Related:  fc.buildConstRelated(causeFn),
	})
}

func (fc *funcChecker) inComptimeOnlyContext() bool {
	if fc.comptimeExprDepth > 0 {
		return true
	}
	if fc.curFn != nil && fc.curFn.ComptimeOnly {
		return true
	}
	return false
}

func (fc *funcChecker) checkInferOrValue(e ast.Expr, expected types.Type) types.Type {
	prev := fc.inferVoidOrValue
	fc.inferVoidOrValue = true
	ty := fc.checkExprExpect(e, expected, true)
	fc.inferVoidOrValue = prev
	return ty
}

func (fc *funcChecker) isTestContext() bool {
	if fc.mod != nil && (fc.mod.IsTestFile || (fc.mod.Pkg != nil && fc.mod.Pkg.IsIntegrationTest)) {
		return true
	}
	if fc.curFn != nil {
		if fc.curFn.IsTest || fc.curFn.IsTestOnly {
			return true
		}
		if fc.a.Prog.TestMainFunc != nil && fc.curFn == fc.a.Prog.TestMainFunc {
			return true
		}
		return false
	}
	if fc.curTopVar != nil && fc.curTopVar.Sym != nil {
		return fc.curTopVar.Sym.IsTest || fc.curTopVar.Sym.IsTestOnly
	}
	return false
}

type lambdaFrame struct {
	funcDepth int
	captures  map[*Symbol]*Capture
	order     []*Capture
}

func (a *Analyzer) runBodyChecking() {
	a.inBodyPass = true
	for i := 0; i < len(a.deferredIfaceChecks); i++ {
		chk := a.deferredIfaceChecks[i]
		if !a.ImplementsInterface(chk.Concrete, chk.Iface) {
			if chk.Context != "" {
				a.Rep.Errorf(chk.Span, "type %s does not satisfy interface %s (%s)", chk.Concrete.String(), chk.Iface.Name, chk.Context)
			} else {
				a.Rep.Errorf(chk.Span, "type %s does not satisfy interface %s", chk.Concrete.String(), chk.Iface.Name)
			}
		}
	}

	// 1. Check struct field default expressions
	for i := 0; i < len(a.Prog.Structs); i++ {
		st := a.Prog.Structs[i]
		decl := a.Prog.StructDecls[st]
		mod := a.Prog.StructMods[st]
		if decl == nil || mod == nil {
			continue
		}
		var subst map[string]types.Type
		if len(decl.TypeParams) == len(st.TypeArgs) && len(st.TypeArgs) > 0 {
			subst = make(map[string]types.Type, len(st.TypeArgs))
			for j, tp := range decl.TypeParams {
				subst[tp.Name] = st.TypeArgs[j]
			}
		}
		bm := NewBodyMaps()
		a.Prog.StructBodyMaps[st] = bm
		prevBM := a.curBM
		a.curBM = bm
		fc := a.newFuncChecker(mod, subst)
		fc.pushScope()
		fc.pushConstSubstSymbols()
		for j, f := range decl.Fields {
			if j < len(st.Fields) {
				fty := st.Fields[j].Type
				if !st.IsComptimeOnly {
					if ctTy := types.FindComptimeOnlyType(fty); ctTy != nil {
						a.Rep.ErrorWithHint(
							f.Span,
							fmt.Sprintf("non-`@comptime_only` struct %q field %q cannot hold `@comptime_only` type %s", st.Name, f.Name, ctTy.String()),
							fmt.Sprintf("mark struct %q `@comptime_only` if it exists only at compile time", st.Name),
						)
					}
				}
				if f.Default != nil {
					got := fc.checkExprExpect(f.Default, fty, true)
					fc.verifyAssignable(got, fty, f.Default, f.Default.GetSpan(), "struct default field")
				}
			}
		}
		fc.popScope()
		a.curBM = prevBM
	}

	// 2. Check top-level `bind` constants in topological dependency order
	a.topVarMap = make(map[*Symbol]*TopVarInit)
	a.topVarStates = make(map[*TopVarInit]int)
	a.orderedTopVars = make([]*TopVarInit, 0, len(a.Prog.TopVars))
	for _, tv := range a.Prog.TopVars {
		a.topVarMap[tv.Sym] = tv
	}
	for _, tv := range a.Prog.TopVars {
		a.resolveTopVar(tv.Sym)
	}
	a.Prog.TopVars = a.orderedTopVars

	// 3. Check all concrete & monomorphized functions (growing dynamically as generics instantiate!)
	for i := 0; i < len(a.Prog.Funcs); i++ {
		fi := a.Prog.Funcs[i]
		if fi.RetInferred {
			_ = a.ensureFuncRetResolved(fi, fi.DeclSpan)
		} else {
			a.checkFuncInstance(fi)
		}
	}
	// Propagate conditional generic downgrades across mutually recursive cycles.
	a.propagateConstGenericDowngrades()
}

// propagateConstGenericDowngrades propagates `const` -> runtime downgrades across
// recorded `constGenericDeps` edges until reaching a fixed point.
func (a *Analyzer) propagateConstGenericDowngrades() {
	for {
		changed := false
		for _, dep := range a.constGenericDeps {
			if dep.caller != nil && dep.callee != nil && dep.caller.IsConst && (dep.caller.IsGenericInst || dep.caller.IsDerived) && !dep.caller.ComptimeOnly && !a.isComptimeCallable(dep.callee) {
				dep.caller.IsConst = false
				if dep.caller.ConstFail == nil {
					dep.caller.ConstFail = &ConstFailReason{
						Span:    dep.span,
						Message: fmt.Sprintf("calls %q, which downgraded to runtime-only", dep.callee.Name),
						CauseFn: dep.callee,
					}
				}
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func (fc *funcChecker) hasDeriveAccess(t types.Type) bool {
	if fc == nil || fc.funcDepth > 0 || fc.a == nil {
		return false
	}
	return fc.a.hasDeriveAccess(fc.curFn, t)
}

func (fc *funcChecker) deriveAuthScope() (types.Type, string) {
	if fc == nil || fc.funcDepth > 0 || fc.curFn == nil || fc.curFn.DeriveTarget == nil {
		return nil, ""
	}
	return fc.curFn.DeriveTarget, fc.curFn.DerivePkg
}

func (fc *funcChecker) lookupMethod(recvTy types.Type, methodName string, methodTypeArgs []types.Type, span diag.Span) *FuncInstance {
	deriveTarget, derivePkg := fc.deriveAuthScope()
	var callerMod *Module
	if fc != nil && fc.curFn != nil {
		callerMod = fc.curFn.Mod
	}
	return fc.a.LookupMethodWithDerive(recvTy, methodName, methodTypeArgs, span, deriveTarget, derivePkg, callerMod)
}

func (fc *funcChecker) probeArgType(arg ast.Expr) types.Type {
	if fc == nil || arg == nil {
		return types.TypeInvalid
	}
	savedRep := fc.a.Rep
	trialRep := diag.NewReporter()
	fc.a.Rep = trialRep
	savedMoves := fc.cloneMoves()
	savedBorrows := make(map[*Symbol]int, len(fc.activeBorrows))
	for k, v := range fc.activeBorrows {
		savedBorrows[k] = v
	}
	savedPinned := make(map[*Symbol]int, len(fc.arenaPinnedDepth))
	for k, v := range fc.arenaPinnedDepth {
		savedPinned[k] = v
	}
	fc.probing++
	ty := fc.checkExpr(arg, true)
	fc.probing--
	fc.moves = savedMoves
	fc.activeBorrows = savedBorrows
	fc.arenaPinnedDepth = savedPinned
	fc.a.Rep = savedRep
	if trialRep.HasErrors() {
		return types.TypeInvalid
	}
	return ty
}

func (a *Analyzer) resolveTopVar(sym *Symbol) {
	if sym == nil {
		return
	}
	tv, ok := a.topVarMap[sym]
	if !ok {
		return
	}
	state := a.topVarStates[tv]
	if state == 2 { // visited
		return
	}
	if state == 1 { // visiting -> cyclic dependency!
		declKind := "bind"
		if tv.Sym.IsConst {
			declKind = "const"
		}
		startIdx := 0
		for i, item := range a.topVarStack {
			if item == tv {
				startIdx = i
				break
			}
		}
		cycle := a.topVarStack[startIdx:]
		names := make([]string, 0, len(cycle)+1)
		var rel []diag.RelatedInfo
		for _, item := range cycle {
			names = append(names, item.Sym.Name)
			rel = append(rel, diag.RelatedInfo{
				Span:    item.Decl.Span,
				Message: fmt.Sprintf("`%s` references the next declaration in this cycle", item.Sym.Name),
			})
		}
		names = append(names, tv.Sym.Name)
		a.Rep.Report(diag.Diagnostic{
			Severity: diag.Error,
			Span:     tv.Decl.Span,
			Message:  fmt.Sprintf("cyclic dependency detected in top-level `%s` declaration %q (%s)", declKind, tv.Sym.Name, strings.Join(names, " -> ")),
			Related:  rel,
		})
		tv.Sym.Type = types.TypeInvalid
		return
	}

	a.topVarStates[tv] = 1 // visiting
	a.topVarStack = append(a.topVarStack, tv)
	defer func() {
		if len(a.topVarStack) > 0 {
			a.topVarStack = a.topVarStack[:len(a.topVarStack)-1]
		}
	}()
	prevBM := a.curBM
	a.curBM = a.Prog.TopBodyMaps

	fc := a.newFuncChecker(tv.Mod, nil)
	fc.curTopVar = tv
	if tv.Sym.IsConst {
		fc.comptimeExprDepth++
	}
	beforeErrs := a.errorCount()
	fc.pushScope()
	var got types.Type
	if tv.Sym.Type != nil && tv.Sym.Type != types.TypeInvalid {
		got = fc.checkExprExpect(tv.Decl.Value, tv.Sym.Type, true)
		fc.verifyAssignable(got, tv.Sym.Type, tv.Decl.Value, tv.Decl.Value.GetSpan(), "top-level bind")
	} else {
		got = fc.checkExpr(tv.Decl.Value, true)
		if got == types.TypeVoid {
			a.Rep.ErrorWithHint(
				tv.Decl.Span,
				fmt.Sprintf("cannot bind non-value (`void`) expression to %q", tv.Sym.Name),
				"remove the `bind` declaration or make the expression evaluate to a value",
			)
			got = types.TypeInvalid
		}
		tv.Sym.Type = got
	}
	if tv.Sym.Type == nil {
		tv.Sym.Type = types.TypeInvalid
	}
	if tv.Sym.IsConst {
		if fl, isFL := tv.Decl.Value.(*ast.FuncLit); isFL && fl.Closure {
			a.Rep.Errorf(fl.Span, "`const` declaration %q cannot be initialized with a `cl` closure", tv.Sym.Name)
		} else if fnTy, isFn := tv.Sym.Type.(*types.Func); isFn && fnTy.Closure {
			a.Rep.Errorf(tv.Decl.Value.GetSpan(), "`const` declaration %q cannot hold a runtime closure value", tv.Sym.Name)
		}
	}
	a.checkOptionalIdentRule(tv.Sym.Name, tv.Decl.IsOptName, tv.Sym.Type, tv.Decl.Span, "top-level bind")
	if !tv.Sym.ComptimeOnly && tv.Sym.Type != nil && tv.Sym.Type != types.TypeInvalid {
		if ctTy := types.FindComptimeOnlyType(tv.Sym.Type); ctTy != nil {
			kindWord := "bind"
			if tv.Sym.IsConst {
				kindWord = "const"
			}
			a.Rep.ErrorWithHint(
				tv.Decl.Span,
				fmt.Sprintf("top-level %s %q cannot hold `@comptime_only` type %s", kindWord, tv.Sym.Name, ctTy.String()),
				fmt.Sprintf("mark %q `const` with `@comptime_only` if it should only exist at compile time", tv.Sym.Name),
			)
		}
	}
	fc.popScope()
	if tv.Sym.IsConst {
		fc.comptimeExprDepth--
		if tv.Sym.Type != types.TypeInvalid && a.errorCount() == beforeErrs {
			_, _ = a.evalComptimeTopVar(tv)
		}
	}

	a.curBM = prevBM
	a.topVarStates[tv] = 2 // visited
	a.orderedTopVars = append(a.orderedTopVars, tv)
}

func (a *Analyzer) newFuncChecker(mod *Module, subst map[string]types.Type) *funcChecker {
	return &funcChecker{
		a:                a,
		mod:              mod,
		subst:            subst,
		moves:            make(map[*Symbol]MoveState),
		activeBorrows:    make(map[*Symbol]int),
		arenaPinnedDepth: make(map[*Symbol]int),
		mutUnwrapOrigin:  make(map[*Symbol]*Symbol),
	}
}

func (a *Analyzer) checkFuncInstance(fi *FuncInstance) {
	if fi.Checked {
		return
	}
	fi.Checked = true
	if a.checkingFuncs == nil {
		a.checkingFuncs = make(map[*FuncInstance]bool)
	}
	a.checkingFuncs[fi] = true
	defer delete(a.checkingFuncs, fi)
	if fi.BodyMaps == nil {
		fi.BodyMaps = NewBodyMaps()
	}
	prevBM := a.curBM
	a.curBM = fi.BodyMaps
	defer func() { a.curBM = prevBM }()
	deriveTarget := fi.DeriveTarget
	if deriveTarget == nil && fi.IsDerived {
		deriveTarget = fi.RecvType
	}
	if deriveTarget != nil && fi.DeriveAttrSpan.Start.File != "" && a.inDeriveTrial == 0 {
		diagStart := a.Rep.Len()
		targetName := deriveTarget.String()
		ifaceName := fi.DeriveIfaceName
		attrSpan := fi.DeriveAttrSpan
		defer func() {
			if a.Rep.HasErrorSince(diagStart) {
				a.Rep.RewriteErrorsSince(diagStart, func(d diag.Diagnostic) diag.Diagnostic {
					if strings.HasPrefix(d.Message, "cannot derive ") {
						return d
					}
					d.Related = append([]diag.RelatedInfo{{
						Span:    d.Span,
						Message: fmt.Sprintf("while instantiating `@derive(%s)` for `%s`", ifaceName, targetName),
					}}, d.Related...)
					d.Span = attrSpan
					d.Message = fmt.Sprintf("cannot derive %s for %s: %s", ifaceName, targetName, d.Message)
					return d
				})
			}
		}()
	}
	if !fi.ComptimeOnly && fi.Sig != nil {
		if ctTy := types.FindComptimeOnlyType(fi.Sig); ctTy != nil {
			a.Rep.ErrorWithHint(
				fi.DeclSpan,
				fmt.Sprintf("runtime-callable function %q cannot use `@comptime_only` type %s in its signature", fi.Name, ctTy.String()),
				fmt.Sprintf("mark %q `const` with `@comptime_only` if it is intended only for compile-time evaluation", fi.Name),
			)
		}
	}

	fc := a.newFuncChecker(fi.Mod, fi.TypeSubst)
	fc.curFn = fi
	fc.pushScope()
	fc.pushConstSubstSymbols()

	for i, fp := range fi.Sig.Params {
		pname := fi.ParamNames[i]
		bkind := BindReadBorrow
		isBind := true
		switch fp.Mode {
		case types.ModeMut:
			bkind = BindMutBorrow
			isBind = false
			if types.IsArena(fp.Type) {
				fc.hasMutArenaParam = true
			}
		case types.ModeOwn:
			bkind = BindOwned
			isBind = false // owned local copy/move inside callee; Spec C2 notes read-only params are `bind`!
		case types.ModeRead:
			bkind = BindReadBorrow
			isBind = true // Spec C2: read-only parameters are deeply immutable like `bind`!
		}
		var pspan diag.Span
		if i < len(fi.ParamSpans) {
			pspan = fi.ParamSpans[i]
		} else {
			pspan = fi.DeclSpan
		}
		sym := fc.declareLocal(pname, fp.Type, isBind, bkind, pspan)
		if bkind != BindOwned {
			sym.ScopeDepth = 0
		}
		fi.ParamSyms = append(fi.ParamSyms, sym)
		fc.moves[sym] = MoveAlive
	}

	// Check arena return escape rule (Spec §11):
	if types.ContainsArenaRef(fi.Sig.Ret) && !fc.hasMutArenaParam {
		a.Rep.ErrorWithHint(
			fi.Body.GetSpan(),
			fmt.Sprintf("function %q cannot return %s containing `ref[T]` unless it takes an `&Arena` parameter", fi.Name, fi.Sig.Ret.String()),
			"pass `(arena: &Arena, ...)` from the caller so the arena outlives the returned `ref[T]`",
		)
	}

	fnTargetID := a.allocTargetID()
	fi.FuncTargetID = fnTargetID
	fnTarget := &cfTarget{
		id:              fnTargetID,
		kind:            cfFunc,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: 0,
		expectedType:    fi.Sig.Ret,
		isInferredFunc:  fi.RetInferred,
	}
	fc.cfStack = append(fc.cfStack, fnTarget)

	retTy := fc.checkFuncBodyAndReturn(fi.Name, fi.Body, fi.Sig.Ret, fi.RetInferred, fi.DeclSpan, fnTarget)
	if fi.RetInferred {
		if fi.InferState != 2 {
			fi.Sig.Ret = retTy
		}
		if fi.Sig.Ret != nil && fi.Sig.Ret != types.TypeInvalid {
			if types.ContainsArenaRef(fi.Sig.Ret) && !fc.hasMutArenaParam {
				a.Rep.ErrorWithHint(
					fi.Body.GetSpan(),
					fmt.Sprintf("function %q cannot return %s containing `ref[T]` unless it takes an `&Arena` parameter", fi.Name, fi.Sig.Ret.String()),
					"pass `(arena: &Arena, ...)` from the caller so the arena outlives the returned `ref[T]`",
				)
			}
			if fi.IsMain {
				a.validateMainSig(fi, fi.DeclSpan)
			}
			if fi.IsTestMain {
				a.validateTestMainSig(fi, fi.DeclSpan)
			}
			if fi.IsDrop {
				a.validateDropSig(fi, fi.DeclSpan)
			}
		}
	}

	fc.cfStack = fc.cfStack[:len(fc.cfStack)-1]
	fc.popScope()
}

func (a *Analyzer) ensureFuncRetResolved(fi *FuncInstance, useSpan diag.Span) types.Type {
	if fi == nil {
		return types.TypeInvalid
	}
	if !fi.RetInferred {
		return fi.Sig.Ret
	}
	if fi.InferState == 2 {
		return fi.Sig.Ret
	}
	if !a.inBodyPass {
		return types.TypeInvalid
	}
	if fi.InferState == 1 {
		a.Rep.ErrorWithHint(
			useSpan,
			fmt.Sprintf("cannot infer return type of recursive function %q", fi.Name),
			"add an explicit return type (or `void`) on at least one function in the cycle",
		)
		fi.Sig.Ret = types.TypeInvalid
		fi.InferState = 2
		return types.TypeInvalid
	}
	fi.InferState = 1
	a.checkFuncInstance(fi)
	if fi.Sig.Ret == nil {
		fi.Sig.Ret = types.TypeInvalid
	}
	fi.InferState = 2
	return fi.Sig.Ret
}

func (fc *funcChecker) checkFuncBodyAndReturn(name string, body ast.Expr, expRet types.Type, inferRet bool, declSpan diag.Span, tgt *cfTarget) types.Type {
	if !inferRet {
		if expRet.Equals(types.TypeVoid) {
			_ = fc.checkVoidOrDiscardedBody(body)
			return types.TypeVoid
		}
		bodyTy := fc.checkExprExpect(body, expRet, true)
		if bodyTy == types.TypeVoid {
			fc.a.Rep.ErrorWithHint(
				body.GetSpan(),
				fmt.Sprintf("function %q must return a value of type %s on all execution paths", name, expRet.String()),
				"end the function body with an expression, or return early with `func <- value`",
			)
		} else {
			fc.verifyAssignable(bodyTy, expRet, body, body.GetSpan(), fmt.Sprintf("return value of %q", name))
			if ad := fc.arenaDepthOfExpr(body); ad > tgt.outerScopeDepth {
				fc.a.Rep.ErrorWithHint(
					body.GetSpan(),
					fmt.Sprintf("arena reference `ref[T]` allocated from a local or owned `Arena` cannot escape function %q", name),
					"allocate into a caller-provided `&Arena` parameter instead of a local `Arena`",
				)
			}
			fc.consumeValue(body, expRet, false)
		}
		return expRet
	}

	bodyTy := fc.checkInferOrValue(body, nil)
	if bodyTy == types.TypeInvalid {
		return types.TypeInvalid
	}
	fallsThroughVoid := bodyTy == types.TypeVoid
	var valTypes []types.Type
	for _, yt := range tgt.yieldTypes {
		if yt != nil && yt != types.TypeInvalid {
			valTypes = append(valTypes, yt)
		}
	}
	if bodyTy != types.TypeVoid && bodyTy != types.TypeNever {
		valTypes = append(valTypes, bodyTy)
	}

	if tgt.hasVoidUnwind && len(valTypes) > 0 {
		fc.a.Rep.ErrorWithHint(
			body.GetSpan(),
			fmt.Sprintf("function %q mixes bare `func <-` (void) with non-void return value", name),
			"add an explicit return type or ensure all return paths produce consistent values",
		)
		return types.TypeInvalid
	}
	if fallsThroughVoid && len(valTypes) > 0 {
		fc.a.Rep.Errorf(body.GetSpan(), "function %q returns a value via `func <-` on some paths, but does not return a value on all execution paths", name)
		return types.TypeInvalid
	}
	if len(valTypes) == 0 {
		return types.TypeVoid
	}

	inferred := unifyYieldTypes(valTypes)
	if inferred == types.TypeInvalid {
		return types.TypeInvalid
	}
	for _, y := range tgt.yieldExprs {
		if y.ty != types.TypeInvalid {
			fc.verifyAssignable(y.ty, inferred, y.expr, y.span, fmt.Sprintf("return value of %q", name))
		}
	}
	if bodyTy != types.TypeVoid && bodyTy != types.TypeNever {
		fc.verifyAssignable(bodyTy, inferred, body, body.GetSpan(), fmt.Sprintf("return value of %q", name))
		if ad := fc.arenaDepthOfExpr(body); ad > tgt.outerScopeDepth {
			fc.a.Rep.ErrorWithHint(
				body.GetSpan(),
				fmt.Sprintf("arena reference `ref[T]` allocated from a local or owned `Arena` cannot escape function %q", name),
				"allocate into a caller-provided `&Arena` parameter instead of a local `Arena`",
			)
		}
		fc.consumeValue(body, inferred, false)
	}
	_ = declSpan
	return inferred
}

// -----------------------------------------------------------------------------
// Scope, Ownership Move, & Borrow Helpers
// -----------------------------------------------------------------------------

func (fc *funcChecker) pushScope() {
	depth := len(fc.scopes) + 1
	fc.scopes = append(fc.scopes, &scopeFrame{
		depth: depth,
		vars:  make(map[string]*Symbol),
	})
}

func (fc *funcChecker) recordPendingOptChainErr(e ast.Expr, emit func()) {
	if fc.probing > 0 || e == nil || emit == nil {
		return
	}
	if fc.pendingOptChainMap == nil {
		fc.pendingOptChainMap = make(map[ast.Expr]int)
	}
	if idx := fc.pendingOptChainMap[e]; idx > 0 {
		return
	}
	fc.pendingOptChainErrs = append(fc.pendingOptChainErrs, optChainErrEntry{expr: e, emit: emit})
	fc.pendingOptChainMap[e] = len(fc.pendingOptChainErrs)
}

func (fc *funcChecker) clearPendingOptChainErr(e ast.Expr) {
	if fc.pendingOptChainMap == nil || e == nil {
		return
	}
	if idx := fc.pendingOptChainMap[e]; idx > 0 {
		fc.pendingOptChainErrs[idx-1].emit = nil
		fc.pendingOptChainMap[e] = 0
	}
}

func (fc *funcChecker) flushPendingOptChainErrs() {
	for _, entry := range fc.pendingOptChainErrs {
		if entry.emit != nil {
			entry.emit()
		}
	}
	fc.pendingOptChainErrs = nil
	fc.pendingOptChainMap = nil
}

func (fc *funcChecker) popScope() {
	if len(fc.scopes) == 1 {
		fc.flushPendingOptChainErrs()
	}
	if len(fc.scopes) > 0 {
		fc.scopes = fc.scopes[:len(fc.scopes)-1]
	}
	cur := fc.curDepth()
	for sym, d := range fc.arenaPinnedDepth {
		if d > cur {
			delete(fc.arenaPinnedDepth, sym)
		}
	}
}

func (fc *funcChecker) pinArenaAt(sym *Symbol, depth int) {
	if sym == nil || !types.IsArena(sym.Type) || sym.BindKind != BindOwned {
		return
	}
	if depth < sym.ScopeDepth {
		depth = sym.ScopeDepth
	}
	if existing, ok := fc.arenaPinnedDepth[sym]; !ok || depth < existing {
		fc.arenaPinnedDepth[sym] = depth
	}
}

func (fc *funcChecker) lowerPinnedArenasTo(targetDepth int) {
	for sym, d := range fc.arenaPinnedDepth {
		if d > targetDepth {
			newD := targetDepth
			if newD < sym.ScopeDepth {
				newD = sym.ScopeDepth
			}
			fc.arenaPinnedDepth[sym] = newD
		}
	}
}

func (fc *funcChecker) isArenaPinned(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	_, ok := fc.arenaPinnedDepth[sym]
	return ok
}

func typeMayHoldArenaRef(t types.Type) bool {
	return typeMayHoldArenaRefVisited(t, make(map[types.Type]bool))
}

func typeMayHoldArenaRefVisited(t types.Type, seen map[types.Type]bool) bool {
	if t == nil || t == types.TypeInvalid || t == types.TypeVoid || t == types.TypeNever {
		return false
	}
	switch v := types.CollView(t).(type) {
	case *types.Func:
		// An `fn` captures nothing; a closure may capture a `ref[T]`.
		return v.Closure
	case *types.Ref, *types.Interface:
		return true
	case *types.Optional:
		return typeMayHoldArenaRefVisited(v.Elem, seen)
	case *types.Vector:
		return typeMayHoldArenaRefVisited(v.Elem, seen)
	case *types.StackArray:
		return typeMayHoldArenaRefVisited(v.Elem, seen)
	case *types.Tuple:
		for _, el := range v.Elems {
			if typeMayHoldArenaRefVisited(el, seen) {
				return true
			}
		}
		return false
	case *types.Set:
		return typeMayHoldArenaRefVisited(v.Elem, seen)
	case *types.Map:
		return typeMayHoldArenaRefVisited(v.Key, seen) || typeMayHoldArenaRefVisited(v.Value, seen)
	case *types.Distinct:
		return typeMayHoldArenaRefVisited(v.Base, seen)
	case *types.Enum:
		if !v.IsTagged || seen[v] {
			return false
		}
		seen[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil && typeMayHoldArenaRefVisited(vr.Payload, seen) {
				return true
			}
		}
		return false
	case *types.Struct:
		if seen[v] {
			return false
		}
		seen[v] = true
		for _, f := range v.Fields {
			if typeMayHoldArenaRefVisited(f.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (fc *funcChecker) curDepth() int {
	return len(fc.scopes)
}

func (fc *funcChecker) declareLocal(name string, ty types.Type, isBind bool, bkind BindKind, span diag.Span) *Symbol {
	frame := fc.scopes[len(fc.scopes)-1]
	if name != "_" {
		for i := len(fc.scopes) - 1; i >= 0; i-- {
			if existing, dup := fc.scopes[i].vars[name]; dup && existing != nil && existing.Span != span {
				if existing.Span.Start.File != "" {
					fc.a.Rep.Errorf(span, "duplicate declaration of %q (previously declared at %s)", name, existing.Span.String())
				} else {
					fc.a.Rep.Errorf(span, "duplicate declaration of %q", name)
				}
				break
			}
		}
	}
	fc.nextLocalID++
	cname := fmt.Sprintf("%s_%d", types.SanitizeCIdent(name), fc.nextLocalID)
	sym := &Symbol{
		Name:          name,
		CName:         cname,
		Type:          ty,
		IsBind:        isBind,
		BindKind:      bkind,
		ScopeDepth:    fc.curDepth(),
		DeclFuncDepth: fc.funcDepth,
		Span:          span,
	}
	frame.vars[name] = sym
	fc.moves[sym] = MoveAlive
	return sym
}

func (fc *funcChecker) lookupVar(name string) *Symbol {
	for i := len(fc.scopes) - 1; i >= 0; i-- {
		if sym, ok := fc.scopes[i].vars[name]; ok {
			return sym
		}
	}
	return fc.a.lookupTopSymbol(fc.mod, "", name)
}

func (fc *funcChecker) cloneMoves() map[*Symbol]MoveState {
	cp := make(map[*Symbol]MoveState, len(fc.moves))
	for k, v := range fc.moves {
		cp[k] = v
	}
	return cp
}

func mergeMoveMaps(maps ...map[*Symbol]MoveState) map[*Symbol]MoveState {
	if len(maps) == 0 {
		return make(map[*Symbol]MoveState)
	}
	out := make(map[*Symbol]MoveState, len(maps[0]))
	for sym, st0 := range maps[0] {
		allMoved := st0 == MoveMoved
		anyMoved := st0 != MoveAlive
		for _, m := range maps[1:] {
			stm, ok := m[sym]
			if !ok {
				continue
			}
			if stm != MoveMoved {
				allMoved = false
			}
			if stm != MoveAlive {
				anyMoved = true
			}
		}
		if allMoved {
			out[sym] = MoveMoved
		} else if anyMoved {
			out[sym] = MoveMaybeMoved
		} else {
			out[sym] = MoveAlive
		}
	}
	return out
}

func (fc *funcChecker) checkSymReadable(sym *Symbol, span diag.Span) {
	if sym == nil || sym.IsTopLevel {
		return
	}
	switch fc.moves[sym] {
	case MoveMoved:
		fc.a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("use of moved variable %q (type %s)", sym.Name, sym.Type.String()),
			"call `.copy()` before moving if you still need the original value",
		)
	case MoveMaybeMoved:
		fc.a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("use of variable %q which may have been moved on an earlier branch", sym.Name),
			"ensure the variable is either not moved or reassigned on all branches before reading",
		)
	}
}

// consumeValue handles Model 2A move semantics when `expr` is placed into an owning slot
// (`let`/`bind`, reassignment, struct field, collection literal, return/unwind, or `own` parameter).
// When `requireExplicitOwnOnVar` is true (direct function call to an `own T` parameter),
// moving a named Resource variable requires `own varName` at the call site!
func (fc *funcChecker) consumeValue(expr ast.Expr, ty types.Type, requireExplicitOwnOnVar bool) {
	if expr == nil || ty == nil || ty == types.TypeInvalid || ty == types.TypeNever || ty.IsPlain() {
		return
	}

	// Unwrap `own x` unary marker or `unsafe` wrapper if present
	hasOwnMarker := false
	inner := expr
	for {
		if ue, ok := inner.(*ast.UnsafeExpr); ok {
			inner = ue.Expr
			continue
		}
		if u, ok := inner.(*ast.UnaryExpr); ok && u.Op == token.KwOwn {
			hasOwnMarker = true
			inner = u.Operand
			continue
		}
		break
	}

	switch inner.(type) {
	case *ast.MemberExpr, *ast.IndexExpr:
		if fc.a != nil && fc.a.curBM != nil && fc.a.curBM.ComptimeVals[inner] != nil {
			// Folded crossable `@comptime_only` projections (e.g. `f.name`, `a.args[0]`, `comp.TARGET.os`)
			// lower to fresh static constants rather than partial runtime moves.
			return
		}
	}

	switch v := inner.(type) {
	case *ast.IdentExpr:
		sym := fc.a.infoFor(v).Sym
		if sym == nil {
			sym = fc.lookupVar(v.Name)
		}
		if sym == nil {
			return
		}
		if sym.IsTopLevel {
			if sym.FuncInst == nil && sym.GenFunc == nil && sym.ExternFn == nil {
				d := diag.Diagnostic{
					Severity: diag.Error,
					Span:     expr.GetSpan(),
					Full:     expr.GetSpan(),
					Message:  fmt.Sprintf("cannot transfer ownership of immutable top-level constant %q", sym.Name),
					Hint:     fmt.Sprintf("write `%s.copy()` to create an independent owned copy", sym.Name),
				}
				if fc.a.supportsCopy(ty) {
					d.Data = map[string]string{"kind": "append_copy"}
				}
				fc.a.Rep.Report(d)
			}
			return
		}
		if sym.BindKind != BindOwned {
			d := diag.Diagnostic{
				Severity: diag.Error,
				Span:     expr.GetSpan(),
				Full:     expr.GetSpan(),
				Message:  fmt.Sprintf("cannot move borrowed variable %q into an owned value", sym.Name),
				Hint:     fmt.Sprintf("write `%s.copy()` to create an independent owned copy", sym.Name),
			}
			if fc.a.supportsCopy(ty) {
				d.Data = map[string]string{"kind": "append_copy"}
			}
			fc.a.Rep.Report(d)
			return
		}
		if fc.activeBorrows[sym] > 0 {
			fc.a.Rep.Errorf(expr.GetSpan(), "cannot move %q while it is actively borrowed", sym.Name)
			return
		}
		if fc.isArenaPinned(sym) {
			fc.a.Rep.ErrorWithHint(
				expr.GetSpan(),
				fmt.Sprintf("cannot move `Arena` %q while `ref[T]` allocations from this scope may still be live", sym.Name),
				"wrap the phase that allocates `ref[T]` in a `{ ... }` block so references go out of scope before moving the arena",
			)
			return
		}
		if requireExplicitOwnOnVar && !hasOwnMarker {
			fc.a.Rep.ErrorWithHint(
				expr.GetSpan(),
				fmt.Sprintf("passing %q to a consuming `own` parameter moves ownership and requires `own %s` at the call site", sym.Name, sym.Name),
				fmt.Sprintf("write `own %s` to transfer ownership, or `%s.copy()` to keep %q valid", sym.Name, sym.Name, sym.Name),
			)
			return
		}
		fc.moves[sym] = MoveMoved
		fc.a.infoFor(expr).MovedLocal = sym
		fc.a.infoFor(inner).MovedLocal = sym

	case *ast.IndexExpr:
		// Note: Map indexing `ages["Alice"]` already produces a fresh owned `?V` rvalue (cloning `V`),
		// whereas vector/array indexing `users[0]` refers to an owned element inside the vector!
		baseTy := fc.a.infoFor(v.Base).Type
		if _, _, isMap := types.MapKV(baseTy); isMap {
			return
		}
		if fc.a.isIndexableType(baseTy) || fc.a.isKeyIndexableType(baseTy) {
			return
		}
		if fc.a.infoFor(v).TargetFunc != nil {
			return
		}
		d := diag.Diagnostic{
			Severity: diag.Error,
			Span:     expr.GetSpan(),
			Full:     expr.GetSpan(),
			Message:  fmt.Sprintf("cannot move Resource value of type %s out of indexed element", ty.String()),
			Hint:     "write `.copy()` to duplicate the element, or `.remove(i)` / `.swap_remove(i)` / `.pop()` to extract ownership",
		}
		if fc.a.supportsCopy(ty) {
			d.Data = map[string]string{"kind": "append_copy"}
		}
		fc.a.Rep.Report(d)

	case *ast.MemberExpr:
		if inf := fc.a.infoFor(v); inf != nil && inf.EnumVariant != nil {
			return
		}
		// Allow `self.field` move ONLY when `self` is an `own self` receiver without Ptr/Ref or custom Drop!
		baseExpr := unwrapExprWrappers(v.Base)
		if id, ok := baseExpr.(*ast.IdentExpr); ok && id.Name == "self" {
			selfSym := fc.lookupVar("self")
			if selfSym != nil && selfSym.BindKind == BindOwned && !fc.a.infoFor(v).IsOptChain {
				uBase := types.UnwrapDistinct(selfSym.Type)
				_, isPtr := uBase.(*types.Ptr)
				_, isRef := uBase.(*types.Ref)
				if !isPtr && !isRef && !typeHasCustomDrop(selfSym.Type) && !typeHasCustomDrop(ty) {
					fc.moves[selfSym] = MoveMoved
					return
				}
			}
		}
		// Moving a Resource field out of a struct leaves a partially-moved struct; require `.copy()`!
		if !fc.a.infoFor(v).IsOptChain {
			d := diag.Diagnostic{
				Severity: diag.Error,
				Span:     expr.GetSpan(),
				Full:     expr.GetSpan(),
				Message:  fmt.Sprintf("cannot move Resource field %q (type %s) out of struct", v.Member, ty.String()),
				Hint:     fmt.Sprintf("write `.%s.copy()` to duplicate the field", v.Member),
			}
			if fc.a.supportsCopy(ty) {
				d.Data = map[string]string{"kind": "append_copy"}
			}
			fc.a.Rep.Report(d)
		}

	case *ast.CallExpr:
		callInf := fc.a.infoFor(v)
		if callInf != nil && !callInf.IsOptChain && (callInf.BuiltinName == "struct.field" || callInf.BuiltinName == "seq.elem" || callInf.BuiltinName == "enum.payload" || callInf.BuiltinName == "distinct.underlying") {
			isEnumPayload := callInf.BuiltinName == "enum.payload"
			if mem, isMem := v.Callee.(*ast.MemberExpr); isMem {
				baseExpr := unwrapExprWrappers(mem.Base)
				var baseTy types.Type
				if bInf := fc.a.infoFor(baseExpr); bInf != nil && bInf.Type != nil {
					baseTy = bInf.Type
				} else if bInf := fc.a.infoFor(mem.Base); bInf != nil {
					baseTy = bInf.Type
				}
				uBase := types.UnwrapDistinct(baseTy)
				_, isPtr := uBase.(*types.Ptr)
				_, isRef := uBase.(*types.Ref)
				if !isPtr && !isRef && baseTy != nil && !typeHasCustomDrop(baseTy) && (isEnumPayload || !typeHasCustomDrop(ty)) {
					if id, isId := baseExpr.(*ast.IdentExpr); isId {
						baseSym := fc.lookupVar(id.Name)
						allowMove := baseSym != nil && baseSym.BindKind == BindOwned && (id.Name == "self" || hasOwnMarker)
						if allowMove {
							fc.moves[baseSym] = MoveMoved
							return
						}
					} else {
						_, isLval, _, _ := fc.analyzePlace(baseExpr)
						if !isLval {
							if !baseTy.IsPlain() {
								fc.consumeValue(baseExpr, baseTy, false)
							}
							return
						}
					}
				}
			}
			what := "element"
			if callInf.BuiltinName == "struct.field" {
				what = fmt.Sprintf("field %q", callInf.ResolvedField)
			} else if callInf.BuiltinName == "enum.payload" && callInf.EnumVariant != nil {
				what = fmt.Sprintf("payload of variant %q", callInf.EnumVariant.Name)
			} else if callInf.BuiltinName == "distinct.underlying" {
				what = "underlying value"
			}
			d := diag.Diagnostic{
				Severity: diag.Error,
				Span:     expr.GetSpan(),
				Full:     expr.GetSpan(),
				Message:  fmt.Sprintf("cannot move Resource %s (type %s) out of composite", what, ty.String()),
				Hint:     "write `.copy()` to duplicate the value, or use `own` on an owned variable/self",
			}
			if fc.a.supportsCopy(ty) {
				d.Data = map[string]string{"kind": "append_copy"}
			}
			fc.a.Rep.Report(d)
		}

	case *ast.UnsafeExpr:
		if hasOwnMarker {
			fc.consumeValue(&ast.UnaryExpr{Op: token.KwOwn, Operand: v.Expr, Span: v.Span}, ty, requireExplicitOwnOnVar)
		} else {
			fc.consumeValue(v.Expr, ty, requireExplicitOwnOnVar)
		}

	case *ast.ComptimeExpr:
		if fc.a != nil && fc.a.curBM != nil && fc.a.curBM.ComptimeVals[v] != nil {
			return
		}
		if hasOwnMarker {
			fc.consumeValue(&ast.UnaryExpr{Op: token.KwOwn, Operand: v.Expr, Span: v.Span}, ty, requireExplicitOwnOnVar)
		} else {
			fc.consumeValue(v.Expr, ty, requireExplicitOwnOnVar)
		}
	}
}

func unwrapExprWrappers(e ast.Expr) ast.Expr {
	for e != nil {
		if ue, ok := e.(*ast.UnsafeExpr); ok {
			e = ue.Expr
			continue
		}
		if cx, ok := e.(*ast.ComptimeExpr); ok {
			e = cx.Expr
			continue
		}
		break
	}
	return e
}

// rootSymbolAndMutability checks whether `place` is a valid mutable lvalue (`let` or `&` borrow)
// and returns the underlying root `*Symbol`.
func typeHasCustomDrop(t types.Type) bool {
	switch v := t.(type) {
	case *types.Struct:
		return v.HasCustomDrop
	case *types.Enum:
		return v.HasCustomDrop
	case *types.Distinct:
		return typeHasCustomDrop(v.Base)
	case *types.Optional:
		return typeHasCustomDrop(v.Elem)
	case *types.Tuple:
		for _, el := range v.Elems {
			if typeHasCustomDrop(el) {
				return true
			}
		}
	case *types.StackArray:
		return typeHasCustomDrop(v.Elem)
	}
	return false
}

func (fc *funcChecker) checkMutablePlaceForAssign(place ast.Expr, action string) *Symbol {
	return fc.checkMutablePlaceImpl(place, action, false)
}

func (fc *funcChecker) checkMutablePlace(place ast.Expr, action string) *Symbol {
	return fc.checkMutablePlaceImpl(place, action, true)
}

func (fc *funcChecker) checkMutablePlaceImpl(place ast.Expr, action string, checkPacked bool) *Symbol {
	root, isLval, isMut, reason := fc.analyzePlace(place)
	if checkPacked && isLval && isMut {
		fc.checkUnalignedPackedFieldAddr(place, action)
	}
	if !isLval {
		fc.a.Rep.Errorf(place.GetSpan(), "cannot %s temporary rvalue expression", action)
		return nil
	}
	if !isMut {
		fc.a.Rep.Errorf(place.GetSpan(), "cannot %s %s", action, reason)
		return nil
	}
	if root != nil && fc.activeBorrows[root] > 0 {
		fc.a.Rep.Errorf(place.GetSpan(), "cannot %s %q while it is borrowed (e.g. by an enclosing `for` loop or `if (x?)` unwrap)", action, root.Name)
		return nil
	}
	return root
}

func (fc *funcChecker) analyzePlace(e ast.Expr) (root *Symbol, isLval bool, isMut bool, immutReason string) {
	switch v := e.(type) {
	case *ast.IdentExpr:
		sym := fc.lookupVar(v.Name)
		if sym == nil {
			return nil, false, false, "unknown identifier"
		}
		fc.checkSymReadable(sym, v.Span)
		if sym.DeclFuncDepth < fc.funcDepth {
			return sym, true, false, fmt.Sprintf("captured variable %q inside lambda", sym.Name)
		}
		if sym.IsBind {
			if sym.BindKind == BindReadBorrow {
				return sym, true, false, fmt.Sprintf("read-only borrowed variable %q", sym.Name)
			}
			return sym, true, false, fmt.Sprintf("deeply immutable `bind` variable %q", sym.Name)
		}
		return sym, true, true, ""

	case *ast.MemberExpr:
		if v.QuestDot || fc.a.infoFor(v).IsOptChain || fc.a.infoFor(v).EnumVariant != nil {
			return nil, false, false, "optional chain or enum constant"
		}
		baseTy := fc.a.infoFor(v.Base).Type
		// `ref[T]` arena pointer allows mutating target struct fields!
		if _, isRef := baseTy.(*types.Ref); isRef {
			return nil, true, true, ""
		}
		return fc.analyzePlace(v.Base)

	case *ast.IndexExpr:
		baseTy := fc.a.infoFor(v.Base).Type
		if _, isPtr := baseTy.(*types.Ptr); isPtr {
			return nil, true, true, ""
		}
		return fc.analyzePlace(v.Base)

	case *ast.UnaryExpr:
		if v.Op == token.Star {
			// `*p` in unsafe block is a mutable lvalue!
			return nil, true, true, ""
		}

	case *ast.UnsafeExpr:
		return fc.analyzePlace(v.Expr)

	case *ast.CallExpr:
		callInf := fc.a.infoFor(v)
		if callInf != nil && (callInf.BuiltinName == "struct.field" || callInf.BuiltinName == "seq.elem" || callInf.BuiltinName == "enum.payload") {
			if callInf.IsOptChain {
				return nil, false, false, "optional chain"
			}
			if mem, isMem := v.Callee.(*ast.MemberExpr); isMem {
				baseTy := fc.a.infoFor(mem.Base).Type
				if _, isRef := baseTy.(*types.Ref); isRef {
					return nil, true, true, ""
				}
				return fc.analyzePlace(mem.Base)
			}
		}
	}
	return nil, false, false, "rvalue expression"
}

func (fc *funcChecker) rootSymbolOfExpr(e ast.Expr) *Symbol {
	switch v := e.(type) {
	case *ast.IdentExpr:
		return fc.lookupVar(v.Name)
	case *ast.UnaryExpr:
		return fc.rootSymbolOfExpr(v.Operand)
	case *ast.UnsafeExpr:
		return fc.rootSymbolOfExpr(v.Expr)
	case *ast.MemberExpr:
		return fc.rootSymbolOfExpr(v.Base)
	case *ast.IndexExpr:
		return fc.rootSymbolOfExpr(v.Base)
	case *ast.SliceExpr:
		return fc.rootSymbolOfExpr(v.Base)
	case *ast.CallExpr:
		callInf := fc.a.infoFor(v)
		if callInf != nil && (callInf.BuiltinName == "struct.field" || callInf.BuiltinName == "seq.elem" || callInf.BuiltinName == "enum.payload" || callInf.BuiltinName == "enum.as_variant" || callInf.BuiltinName == "distinct.underlying") && callInf.MethodRecv != nil {
			return fc.rootSymbolOfExpr(callInf.MethodRecv)
		}
		return nil
	default:
		return nil
	}
}

func (fc *funcChecker) arenaDepthOfExpr(e ast.Expr) int {
	if e == nil {
		return 0
	}
	switch v := e.(type) {
	case *ast.IdentExpr:
		if sym := fc.lookupVar(v.Name); sym != nil {
			return sym.ArenaDepth
		}
	case *ast.UnsafeExpr:
		return fc.arenaDepthOfExpr(v.Expr)
	case *ast.UnaryExpr:
		return fc.arenaDepthOfExpr(v.Operand)
	case *ast.MemberExpr:
		return fc.arenaDepthOfExpr(v.Base)
	case *ast.IndexExpr:
		return fc.arenaDepthOfExpr(v.Base)
	case *ast.SliceExpr:
		return fc.arenaDepthOfExpr(v.Base)
	case *ast.IfExpr:
		if yd := fc.a.infoFor(v).YieldArenaDepth; yd > 0 {
			return yd
		}
		d1 := fc.arenaDepthOfExpr(v.Then)
		d2 := fc.arenaDepthOfExpr(v.Else)
		if d1 > d2 {
			return d1
		}
		return d2
	case *ast.BlockExpr:
		if yd := fc.a.infoFor(v).YieldArenaDepth; yd > 0 {
			return yd
		}
		return fc.arenaDepthOfExpr(v.Tail)
	case *ast.MatchExpr:
		if yd := fc.a.infoFor(v).YieldArenaDepth; yd > 0 {
			return yd
		}
		maxD := 0
		for _, arm := range fc.matchArms(v) {
			if d := fc.arenaDepthOfExpr(arm.Body); d > maxD {
				maxD = d
			}
		}
		return maxD
	case *ast.ForExpr:
		return fc.a.infoFor(v).YieldArenaDepth
	case *ast.WhileExpr:
		return fc.a.infoFor(v).YieldArenaDepth
	case *ast.FuncLit:
		return fc.a.infoFor(v).YieldArenaDepth
	case *ast.BinaryExpr:
		if v.Op == token.QuestQuest {
			d1 := fc.arenaDepthOfExpr(v.Left)
			d2 := fc.arenaDepthOfExpr(v.Right)
			if d1 > d2 {
				return d1
			}
			return d2
		}
	case *ast.CallExpr:
		inf := fc.a.infoFor(v)
		if inf != nil && (inf.BuiltinName == "struct.field" || inf.BuiltinName == "seq.elem" || inf.BuiltinName == "enum.payload" || inf.BuiltinName == "enum.as_variant" || inf.BuiltinName == "distinct.underlying") && inf.MethodRecv != nil {
			return fc.arenaDepthOfExpr(inf.MethodRecv)
		}
		if inf.CallKind == CallClosureValue {
			if cd := fc.arenaDepthOfExpr(v.Callee); cd > 0 && typeMayHoldArenaRef(inf.Type) {
				return cd
			}
		}
		// Propagate max arena depth of arguments if return type may hold `ref[T]`
		if typeMayHoldArenaRef(inf.Type) {
			maxD := fc.arenaDepthOfExpr(v.Callee)
			if inf.MethodRecv != nil {
				if root := fc.rootSymbolOfExpr(inf.MethodRecv); root != nil && types.IsArena(root.Type) && root.BindKind == BindOwned {
					if root.ScopeDepth > maxD {
						maxD = root.ScopeDepth
					}
				} else if d := fc.arenaDepthOfExpr(inf.MethodRecv); d > maxD {
					maxD = d
				}
			}
			for _, arg := range v.Args {
				if root := fc.rootSymbolOfExpr(arg); root != nil && types.IsArena(root.Type) && root.BindKind == BindOwned {
					if root.ScopeDepth > maxD {
						maxD = root.ScopeDepth
					}
				}
				if d := fc.arenaDepthOfExpr(arg); d > maxD {
					maxD = d
				}
			}
			return maxD
		}
	case *ast.StructLit:
		maxD := 0
		for _, f := range v.Fields {
			if d := fc.arenaDepthOfExpr(f.Value); d > maxD {
				maxD = d
			}
		}
		return maxD
	case *ast.VecLit:
		maxD := 0
		for _, el := range v.Elems {
			if d := fc.arenaDepthOfExpr(el); d > maxD {
				maxD = d
			}
		}
		return maxD
	case *ast.ArrayLit:
		maxD := 0
		for _, el := range v.Elems {
			if d := fc.arenaDepthOfExpr(el); d > maxD {
				maxD = d
			}
		}
		return maxD
	case *ast.MapLit:
		maxD := 0
		for _, ent := range v.Entries {
			if d := fc.arenaDepthOfExpr(ent.Key); d > maxD {
				maxD = d
			}
			if d := fc.arenaDepthOfExpr(ent.Value); d > maxD {
				maxD = d
			}
		}
		return maxD
	case *ast.TupleLit:
		maxD := 0
		for _, el := range v.Elems {
			if d := fc.arenaDepthOfExpr(el); d > maxD {
				maxD = d
			}
		}
		return maxD
	}
	return 0
}

// -----------------------------------------------------------------------------
// Type Compatibility & Implicit Adaptation
// -----------------------------------------------------------------------------

// verifyAssignable checks whether `got` can be used where `expected` is required,
// recording implicit `T -> ?T` wrapping or `Concrete -> Interface` coercion on `expr`!
func (fc *funcChecker) verifyAssignable(got, expected types.Type, expr ast.Expr, span diag.Span, context string) bool {
	if got == nil || expected == nil || got == types.TypeInvalid || expected == types.TypeInvalid {
		return true
	}
	if got == types.TypeNever {
		return true
	}
	if got.Equals(expected) {
		return true
	}

	// An `fn` converts implicitly to a closure with the same signature.
	if fnGot, ok := got.(*types.Func); ok && !fnGot.Closure && !fnGot.Extern {
		if clExp, ok := expected.(*types.Func); ok && clExp.Closure && fnGot.SameSignature(clExp) {
			if expr != nil {
				fc.a.infoFor(expr).ToClosure = clExp
			}
			return true
		}
	}

	// Rule C1: `T` wraps implicitly into `?T`!
	if optExp, isOpt := expected.(*types.Optional); isOpt {
		if got.Equals(optExp.Elem) {
			if expr != nil {
				fc.a.infoFor(expr).WrapOptional = true
			}
			return true
		}
	}

	// Concrete type passed to an Interface expected slot:
	// Note: For owned positions, Spec A3 encourages `Shape(c)`, AND if someone passes a concrete type
	// to an Interface parameter `(x: Printable)`, it borrows as a zero-allocation fat pointer automatically!
	// Even in owned position (`let p: Printable = Printable(x)` or `[Printable]`), supporting implicit or explicit coercion
	// when `expected` is `*types.Interface` and `ImplementsInterface(got, expected)` holds is super clean!
	if ifcExp, isIfc := expected.(*types.Interface); isIfc && !ifcExp.UsesSelf {
		if fc.a.ImplementsInterface(got, ifcExp) {
			fc.a.RegisterVTable(got, ifcExp)
			if expr != nil {
				fc.a.infoFor(expr).BoxInterface = ifcExp
			}
			return true
		}
	}

	full := span
	if expr != nil && span == expr.GetSpan() {
		span = fc.a.focusSpan(expr)
	} else {
		span = fc.a.firstLine(span)
	}
	printable := expected.Equals(types.TypeString) && fc.a.Prog != nil && fc.a.Prog.IfacePrintable != nil && fc.a.ImplementsInterface(got, fc.a.Prog.IfacePrintable)
	hint, fix := mismatchHint(expected, got, fc.a.ImplementsInterface, printable)
	fc.a.Rep.Report(diag.Diagnostic{
		Severity: diag.Error,
		Span:     span,
		Full:     full,
		Message:  mismatchMessage(context, expected, got),
		Hint:     hint,
		Data:     map[string]string{"kind": "mismatch", "expected": expected.String(), "got": got.String(), "fix": fix},
	})
	return false
}

// -----------------------------------------------------------------------------
// Blocks & Statements
// -----------------------------------------------------------------------------

func (fc *funcChecker) allowComptimeUnsafe() bool {
	return fc.mod != nil && fc.mod.Pkg != nil && fc.mod.Pkg.IsStd
}

func (fc *funcChecker) checkVoidOrDiscardedBody(e ast.Expr) types.Type {
	switch b := e.(type) {
	case *ast.UnsafeExpr:
		if fc.inConstContext() && !fc.allowComptimeUnsafe() {
			fc.recordConstViolation(b.Span, "`unsafe` is not allowed inside a `const` function or `comptime` expression", "keep `unsafe` in runtime `bind` functions", nil)
		}
		fc.unsafeDepth++
		defer func() { fc.unsafeDepth-- }()
		ty := fc.checkVoidOrDiscardedBody(b.Expr)
		inf := fc.a.infoFor(b)
		inf.Type = ty
		return ty
	case *ast.ComptimeExpr:
		return fc.checkComptimeExpr(b, types.TypeVoid, false)
	case *ast.BlockExpr:
		return fc.checkBlockExpr(b, types.TypeVoid, false)
	case *ast.StmtBodyExpr:
		return fc.checkStmt(b.Stmt)
	case *ast.IfExpr:
		return fc.checkIfExpr(b, types.TypeVoid, false)
	case *ast.ForExpr:
		return fc.checkForExpr(b, types.TypeVoid, false)
	case *ast.WhileExpr:
		return fc.checkWhileExpr(b, types.TypeVoid, false)
	case *ast.MatchExpr:
		return fc.checkMatchExpr(b, types.TypeVoid, false)
	default:
		return fc.checkExpr(e, false)
	}
}

func (fc *funcChecker) checkUnsafeExpr(ue *ast.UnsafeExpr, expected types.Type, needValue bool) types.Type {
	if fc.inConstContext() && !fc.allowComptimeUnsafe() {
		fc.recordConstViolation(ue.Span, "`unsafe` is not allowed inside a `const` function or `comptime` expression", "keep `unsafe` in runtime `bind` functions", nil)
	}
	fc.unsafeDepth++
	defer func() { fc.unsafeDepth-- }()
	ty := fc.checkExprExpect(ue.Expr, expected, needValue)
	inf := fc.a.infoFor(ue)
	inf.Type = ty
	return ty
}

func (fc *funcChecker) checkComptimeExpr(ce *ast.ComptimeExpr, expected types.Type, needValue bool) types.Type {
	prevMin := fc.comptimeMinScope
	wasOuterRuntime := !fc.inComptimeOnlyContext() && fc.comptimeExprDepth == 0
	if fc.comptimeExprDepth == 0 {
		fc.comptimeMinScope = fc.curDepth() + 1
	}
	fc.comptimeExprDepth++
	beforeErrs := fc.a.errorCount()
	ty := fc.checkExprExpect(ce.Expr, expected, needValue)
	if fc.probing == 0 {
		fc.flushPendingOptChainErrs()
	}
	if fc.probing == 0 && ty != types.TypeInvalid && fc.a.errorCount() == beforeErrs {
		if cv, ok := fc.a.evalComptimeExprInChecker(fc, ce.Expr, 0); ok && cv != nil && fc.a.curBM != nil {
			if ty != nil {
				cv = cv.wrapResult(ty)
			}
			if wasOuterRuntime {
				if msg := fc.a.validateCrossesToRuntime(cv, ty); msg != "" {
					fc.a.Rep.Errorf(ce.Span, "%s", msg)
				}
			}
			fc.a.curBM.ComptimeVals[ce] = cv
		} else if wasOuterRuntime {
			if msg := fc.a.validateCrossTypeOnly(ty, make(map[types.Type]bool)); msg != "" {
				fc.a.Rep.Errorf(ce.Span, "%s", msg)
			}
		}
	}
	fc.comptimeExprDepth--
	fc.comptimeMinScope = prevMin
	inf := fc.a.infoFor(ce)
	inf.Type = ty
	return ty
}

func (fc *funcChecker) checkBlockExpr(b *ast.BlockExpr, expected types.Type, needValue bool) types.Type {
	allowVoid := fc.inferVoidOrValue
	fc.inferVoidOrValue = false
	id := fc.a.allocTargetID()
	inf := fc.a.infoFor(b)
	inf.TargetID = id

	if b.Unsafe {
		if fc.inConstContext() && !fc.allowComptimeUnsafe() {
			fc.recordConstViolation(b.Span, "`unsafe` is not allowed inside a `const` function or `comptime` expression", "keep `unsafe` in runtime `bind` functions", nil)
		}
		fc.unsafeDepth++
		defer func() { fc.unsafeDepth-- }()
	}

	outerDepth := fc.curDepth()
	fc.pushScope()
	defer fc.popScope()

	var blkExp types.Type
	if needValue && expected != nil && expected != types.TypeVoid {
		blkExp = expected
	}
	tgt := &cfTarget{
		id:              id,
		kind:            cfBlock,
		label:           b.Label,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: outerDepth,
		expectedType:    blkExp,
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	diverged := false
	warnedUnreachable := false
	for _, st := range b.Stmts {
		if diverged && !warnedUnreachable {
			if _, isDecl := st.(*ast.DeclStmt); !isDecl {
				warnedUnreachable = true
				fc.a.Rep.Report(diag.Diagnostic{
					Severity: diag.Warning,
					Span:     st.GetSpan(),
					Message:  "unreachable code",
					Hint:     "statements after an unconditional exit (`<-`, `panic`, `exit`) will never execute",
				})
			}
		}
		stTy := fc.checkStmt(st)
		if stTy == types.TypeNever {
			diverged = true
		}
	}

	if b.Tail != nil {
		if diverged && !warnedUnreachable {
			warnedUnreachable = true
			fc.a.Rep.Report(diag.Diagnostic{
				Severity: diag.Warning,
				Span:     fc.a.focusSpan(b.Tail),
				Message:  "unreachable code",
				Hint:     "expression after an unconditional exit (`<-`, `panic`, `exit`) will never execute",
			})
		}
		var tailTy types.Type
		if !needValue {
			tailTy = fc.checkVoidOrDiscardedBody(b.Tail)
			fc.checkMustUseDiscard(b.Tail, tailTy)
			if (diverged || tailTy == types.TypeNever) && !tgt.hitValueExit {
				inf.Type = types.TypeNever
				return types.TypeNever
			}
			inf.Type = types.TypeVoid
			return types.TypeVoid
		}
		blockScopeDepth := fc.curDepth()
		if blkExp != nil {
			tailTy = fc.checkExprExpect(b.Tail, blkExp, true)
			if tailTy == types.TypeVoid {
				fc.a.Rep.Errorf(b.Tail.GetSpan(), "block must evaluate to a value of type %s, but final expression is `void`", blkExp.String())
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			fc.verifyAssignable(tailTy, blkExp, b.Tail, b.Tail.GetSpan(), "block expression")
			tailAd := fc.arenaDepthOfExpr(b.Tail)
			if tailAd >= blockScopeDepth {
				fc.a.Rep.Errorf(b.Tail.GetSpan(), "arena reference `ref[T]` cannot escape the block scope of its `Arena`")
			}
			if tailAd > tgt.maxYieldArenaDepth {
				tgt.maxYieldArenaDepth = tailAd
			}
			inf.YieldArenaDepth = tgt.maxYieldArenaDepth
			if typeMayHoldArenaRef(blkExp) {
				fc.lowerPinnedArenasTo(outerDepth)
			}
			fc.consumeValue(b.Tail, blkExp, false)
			inf.Type = blkExp
			return blkExp
		}

		if allowVoid {
			var tailExp types.Type
			if len(tgt.yieldTypes) > 0 {
				tailExp = tgt.yieldTypes[0]
			}
			tailTy = fc.checkInferOrValue(b.Tail, tailExp)
			if tailTy == types.TypeVoid && !tgt.hitValueExit {
				if diverged {
					inf.Type = types.TypeNever
					return types.TypeNever
				}
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
		} else {
			tailTy = fc.checkExpr(b.Tail, true)
		}
		if tailTy == types.TypeVoid {
			fc.a.Rep.Errorf(b.Span, "block used as a value does not produce a value")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		tailAd := fc.arenaDepthOfExpr(b.Tail)
		if tailAd >= blockScopeDepth {
			fc.a.Rep.Errorf(b.Tail.GetSpan(), "arena reference `ref[T]` cannot escape the block scope of its `Arena`")
		}
		if tailAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = tailAd
		}
		inf.YieldArenaDepth = tgt.maxYieldArenaDepth
		resTy := unifyYieldTypes(append(tgt.yieldTypes, tailTy))
		if typeMayHoldArenaRef(resTy) {
			fc.lowerPinnedArenasTo(outerDepth)
		}
		fc.consumeValue(b.Tail, resTy, false)
		inf.Type = resTy
		return resTy
	}

	// No tail expression in `{ ... }`!
	if !needValue {
		if diverged && !tgt.hitValueExit {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}
	if tgt.hitValueExit {
		resTy := blkExp
		if resTy == nil {
			resTy = unifyYieldTypes(tgt.yieldTypes)
		}
		if !diverged {
			fc.a.Rep.Errorf(b.Span, "not all execution paths in value-producing block yield a value")
		}
		inf.YieldArenaDepth = tgt.maxYieldArenaDepth
		if typeMayHoldArenaRef(resTy) {
			fc.lowerPinnedArenasTo(outerDepth)
		}
		inf.Type = resTy
		return resTy
	}
	if diverged {
		inf.Type = types.TypeNever
		return types.TypeNever
	}
	if allowVoid {
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	// Spec §6.4 / §7.3: Non-Value Block Safety!
	fc.a.Rep.ErrorWithHint(
		b.Span,
		"block does not evaluate to a value (ends in a statement rather than an expression)",
		"provide a final expression at the end of `{ ... }` or unwind with `<- value`",
	)
	inf.Type = types.TypeInvalid
	return types.TypeInvalid
}

func unifyYieldTypes(ts []types.Type) types.Type {
	var concrete types.Type
	hasOpt := false
	for _, t := range ts {
		if t == nil || t == types.TypeNever || t == types.TypeInvalid {
			continue
		}
		if opt, ok := t.(*types.Optional); ok {
			hasOpt = true
			if concrete == nil {
				concrete = opt.Elem
			}
		} else if concrete == nil {
			concrete = t
		}
	}
	if concrete == nil {
		return types.TypeNever
	}
	if hasOpt {
		return types.NewOptional(concrete)
	}
	return concrete
}

func (fc *funcChecker) checkStmt(st ast.Stmt) types.Type {
	switch s := st.(type) {
	case *ast.VarStmt:
		return fc.checkVarStmt(s)
	case *ast.AssignStmt:
		return fc.checkAssignStmt(s)
	case *ast.UnwindStmt:
		return fc.checkUnwindStmt(s)
	case *ast.DeclStmt:
		// Local type declarations inside blocks
		fc.checkLocalTypeDecl(s.Decl)
		return types.TypeVoid
	case *ast.ExprStmt:
		t := fc.checkVoidOrDiscardedBody(s.Expr)
		fc.checkMustUseDiscard(s.Expr, t)
		if t == types.TypeNever {
			return types.TypeNever
		}
		return types.TypeVoid
	}
	return types.TypeVoid
}

func (fc *funcChecker) checkMustUseDiscard(expr ast.Expr, t types.Type) {
	if t == nil || t == types.TypeVoid || t == types.TypeNever || t == types.TypeInvalid {
		return
	}
	if st, ok := t.(*types.Struct); ok && st.MustUse {
		fc.a.Rep.Warnf(expr.GetSpan(), "unused return value of type %s marked @must_use", st.Name)
		return
	}
	if en, ok := t.(*types.Enum); ok && en.MustUse {
		fc.a.Rep.Warnf(expr.GetSpan(), "unused return value of type %s marked @must_use", en.Name)
		return
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		inf := fc.a.infoFor(call)
		if inf.TargetFunc != nil && inf.TargetFunc.MustUse {
			fc.a.Rep.Warnf(call.Span, "unused return value of %q marked @must_use", inf.TargetFunc.Name)
		} else if inf.Sym != nil && inf.Sym.MustUse {
			fc.a.Rep.Warnf(call.Span, "unused return value of %q marked @must_use", inf.Sym.Name)
		}
	}
}

func (fc *funcChecker) checkLocalTypeDecl(d ast.Decl) {
	switch td := d.(type) {
	case *ast.StructDecl:
		if len(td.TypeParams) > 0 {
			fc.a.Rep.Errorf(td.Span, "generic struct declarations must be at top level")
			return
		}
		st := &types.Struct{
			Name:   td.Name,
			CName:  fmt.Sprintf("ll_ls_%s_%d", td.Name, fc.a.allocTargetID()),
			Packed: td.Packed,
		}
		fc.mod.Types[td.Name] = st
		fc.a.Prog.StructDecls[st] = td
		fc.a.Prog.StructMods[st] = fc.mod
		fc.a.Prog.Structs = append(fc.a.Prog.Structs, st)
		fc.a.populateStructFields(fc.mod, st, td, fc.subst)
		fc.a.checkSingleStructRecursion(st)
	case *ast.EnumDecl:
		if len(td.TypeParams) > 0 {
			fc.a.Rep.Errorf(td.Span, "generic enum declarations must be at top level")
			return
		}
		isTagged := td.IsResult
		for _, v := range td.Variants {
			if v.PayloadType != nil {
				isTagged = true
				break
			}
		}
		en := &types.Enum{
			Name:       td.Name,
			CName:      fmt.Sprintf("ll_le_%s_%d", td.Name, fc.a.allocTargetID()),
			IsTagged:   isTagged,
			IsResult:   td.IsResult,
			Underlying: types.TypeInt,
		}
		fc.mod.Types[td.Name] = en
		fc.a.Prog.EnumDecls[en] = td
		fc.a.Prog.EnumMods[en] = fc.mod
		fc.a.Prog.TypeSpans[en] = td.Span
		fc.a.Prog.Enums = append(fc.a.Prog.Enums, en)
		if isTagged {
			fc.a.populateEnumVariants(fc.mod, en, td, fc.subst)
			fc.a.checkSingleEnumRecursion(en)
		} else {
			for idx, v := range td.Variants {
				en.Variants = append(en.Variants, types.EnumVariant{
					Name:  v.Name,
					CName: "v_" + types.SanitizeCIdent(v.Name),
					Value: int64(idx),
					Kind:  types.VariantUnit,
				})
			}
		}
	case *ast.DistinctTypeDecl:
		base := fc.a.resolveValueType(fc.mod, td.Base, fc.subst, false)
		dt := &types.Distinct{
			Name:  td.Name,
			CName: fmt.Sprintf("ll_ld_%s_%d", td.Name, fc.a.allocTargetID()),
			Base:  base,
		}
		fc.mod.Types[td.Name] = dt
		fc.a.Prog.Distincts = append(fc.a.Prog.Distincts, dt)
	}
}

func (fc *funcChecker) checkFallbackUnwindStmt(uw *ast.UnwindStmt) {
	// A top-level `?? <unwind>` diverges on the fallback path, so moves inside it
	// must not taint the fallthrough move state.
	preUW := fc.cloneMoves()
	_ = fc.checkUnwindStmt(uw)
	fc.moves = preUW
}

func (fc *funcChecker) checkVarStmt(s *ast.VarStmt) types.Type {
	if s.IsUnpack {
		return fc.checkUnpackVarStmt(s)
	}
	wasOuterRuntime := !fc.inComptimeOnlyContext() && fc.comptimeExprDepth == 0
	if s.IsConst {
		prevMin := fc.comptimeMinScope
		if fc.comptimeExprDepth == 0 {
			fc.comptimeMinScope = fc.curDepth() + 1
		}
		fc.comptimeExprDepth++
		defer func() {
			fc.comptimeExprDepth--
			fc.comptimeMinScope = prevMin
		}()
	}
	beforeErrs := fc.a.errorCount()
	var declTy types.Type
	if s.Type != nil {
		declTy = fc.a.resolveValueType(fc.mod, s.Type, fc.subst, false)
	}

	if s.FallbackUnwind != nil {
		// `let a = opt_expr ?? <unwind>`
		var optTy types.Type
		if declTy != nil && declTy != types.TypeInvalid {
			optTy = fc.checkExprExpect(s.Value, types.NewOptional(declTy), true)
		} else {
			optTy = fc.checkExpr(s.Value, true)
		}
		fc.checkFallbackUnwindStmt(s.FallbackUnwind)
		unwrapped := types.Type(types.TypeInvalid)
		if elem, ok := guardUnwrapElem(optTy); ok {
			unwrapped = elem
		} else if optTy != types.TypeInvalid {
			fc.reportBadFallbackSubject(s.Value.GetSpan(), optTy)
		}
		finalTy := unwrapped
		if declTy != nil && declTy != types.TypeInvalid {
			fc.verifyAssignable(unwrapped, declTy, nil, s.Value.GetSpan(), "variable declaration")
			finalTy = declTy
		}
		fc.a.checkOptionalIdentRule(s.Name, s.IsOptName, finalTy, s.Span, "variable")
		fc.consumeValue(s.Value, optTy, false)
		sym := fc.declareLocal(s.Name, finalTy, s.IsBind || s.IsConst, BindOwned, s.Span)
		sym.IsConst = s.IsConst
		sym.ArenaDepth = fc.arenaDepthOfExpr(s.Value)
		fc.a.curBM.VarSymbols[s] = sym
		if s.IsConst && fc.probing == 0 {
			fc.flushPendingOptChainErrs()
			if finalTy != types.TypeInvalid && fc.a.errorCount() == beforeErrs {
				if cv, ok := fc.a.evalComptimeExprInChecker(fc, s.Value, 0); ok && cv != nil {
					if uw, okUW := unwrapFallbackOrUnwind(cv, s.FallbackUnwind, nil); okUW && uw != nil {
						resV := uw.clone().wrapResult(finalTy)
						if wasOuterRuntime {
							if msg := fc.a.validateCrossesToRuntime(resV, finalTy); msg != "" {
								fc.a.Rep.Errorf(s.Value.GetSpan(), "%s", msg)
							}
						}
						freezeCtVal(resV)
						sym.ConstVal = resV
						fc.a.curBM.ConstVarVals[sym] = resV
					}
				} else if wasOuterRuntime {
					if msg := fc.a.validateCrossTypeOnly(finalTy, make(map[types.Type]bool)); msg != "" {
						fc.a.Rep.Errorf(s.Value.GetSpan(), "%s", msg)
					}
				}
			}
		}
		return types.TypeVoid
	}

	var valTy types.Type
	if declTy != nil && declTy != types.TypeInvalid {
		valTy = fc.checkExprExpect(s.Value, declTy, true)
		if valTy == types.TypeVoid {
			fc.a.Rep.ErrorWithHint(
				s.Span,
				fmt.Sprintf("cannot initialize variable %q with a `void` non-value expression", s.Name),
				"call it as a statement without `let`/`bind`, or make the expression evaluate to a value",
			)
		} else {
			fc.verifyAssignable(valTy, declTy, s.Value, s.Value.GetSpan(), fmt.Sprintf("declaration of %q", s.Name))
		}
	} else {
		valTy = fc.checkExpr(s.Value, true)
		if valTy == types.TypeVoid {
			fc.a.Rep.ErrorWithHint(
				s.Span,
				fmt.Sprintf("cannot initialize variable %q with a `void` non-value expression", s.Name),
				"call it as a statement without `let`/`bind`, or make the expression evaluate to a value",
			)
			valTy = types.TypeInvalid
		}
		declTy = valTy
	}

	fc.a.checkOptionalIdentRule(s.Name, s.IsOptName, declTy, s.Span, "variable")
	fc.consumeValue(s.Value, declTy, false)

	sym := fc.declareLocal(s.Name, declTy, s.IsBind || s.IsConst, BindOwned, s.Span)
	sym.IsConst = s.IsConst
	if s.IsConst {
		if fnTy, isFn := declTy.(*types.Func); isFn && fnTy.Closure {
			fc.a.Rep.Errorf(s.Value.GetSpan(), "local `const` declaration %q cannot hold a runtime `cl` closure value", s.Name)
		}
	}
	sym.ArenaDepth = fc.arenaDepthOfExpr(s.Value)
	fc.a.curBM.VarSymbols[s] = sym
	if s.IsConst && fc.probing == 0 {
		fc.flushPendingOptChainErrs()
	}
	if s.IsConst && fc.probing == 0 && declTy != types.TypeInvalid && fc.a.errorCount() == beforeErrs {
		if cv, ok := fc.a.evalComptimeExprInChecker(fc, s.Value, 0); ok && cv != nil {
			cv = cv.wrapResult(declTy)
			if wasOuterRuntime {
				if msg := fc.a.validateCrossesToRuntime(cv, declTy); msg != "" {
					fc.a.Rep.Errorf(s.Value.GetSpan(), "%s", msg)
				}
			}
			freezeCtVal(cv)
			sym.ConstVal = cv
			fc.a.curBM.ConstVarVals[sym] = cv
		} else if wasOuterRuntime {
			if msg := fc.a.validateCrossTypeOnly(declTy, make(map[types.Type]bool)); msg != "" {
				fc.a.Rep.Errorf(s.Value.GetSpan(), "%s", msg)
			}
		}
	}
	if valTy == types.TypeNever {
		return types.TypeNever
	}
	return types.TypeVoid
}

func (fc *funcChecker) checkUnpackVarStmt(s *ast.VarStmt) types.Type {
	wasOuterRuntime := !fc.inComptimeOnlyContext() && fc.comptimeExprDepth == 0
	if s.IsConst {
		prevMin := fc.comptimeMinScope
		if fc.comptimeExprDepth == 0 {
			fc.comptimeMinScope = fc.curDepth() + 1
		}
		fc.comptimeExprDepth++
		defer func() {
			fc.comptimeExprDepth--
			fc.comptimeMinScope = prevMin
		}()
	}
	beforeErrs := fc.a.errorCount()
	declTypes := make([]types.Type, len(s.UnpackVars))
	allAnnotated := len(s.UnpackVars) >= 2
	anyAnnotated := false
	for i, uv := range s.UnpackVars {
		if uv.Type != nil {
			declTypes[i] = fc.a.resolveValueType(fc.mod, uv.Type, fc.subst, false)
			if declTypes[i] == types.TypeInvalid {
				allAnnotated = false
			}
			anyAnnotated = true
		} else {
			allAnnotated = false
		}
	}

	var expectedTup types.Type
	if allAnnotated {
		expectedTup = &types.Tuple{Elems: declTypes}
	} else if anyAnnotated && s.FallbackUnwind == nil {
		if _, isTL := s.Value.(*ast.TupleLit); isTL {
			expectedTup = &types.Tuple{Elems: declTypes}
		}
	}

	var valTy types.Type
	var unwrappedTy types.Type
	if s.FallbackUnwind != nil {
		var expOpt types.Type
		if expectedTup != nil {
			expOpt = types.NewOptional(expectedTup)
		}
		valTy = fc.checkExprExpect(s.Value, expOpt, true)
		fc.checkFallbackUnwindStmt(s.FallbackUnwind)
		unwrappedTy = types.TypeInvalid
		if elem, ok := guardUnwrapElem(valTy); ok {
			unwrappedTy = elem
		} else if valTy != types.TypeInvalid {
			fc.reportBadFallbackSubject(s.Value.GetSpan(), valTy)
		}
	} else {
		valTy = fc.checkExprExpect(s.Value, expectedTup, true)
		unwrappedTy = valTy
	}

	var tupTy *types.Tuple
	if t, isTup := unwrappedTy.(*types.Tuple); isTup {
		tupTy = t
	} else if dist, isDist := unwrappedTy.(*types.Distinct); isDist {
		if t, isTup := dist.Base.(*types.Tuple); isTup {
			tupTy = t
		}
	}

	if tupTy == nil {
		if unwrappedTy != types.TypeInvalid && unwrappedTy != types.TypeNever {
			fc.a.Rep.Errorf(s.Value.GetSpan(), "cannot unpack non-tuple type %s", unwrappedTy.String())
		}
	} else if len(s.UnpackVars) != len(tupTy.Elems) {
		fc.a.Rep.Errorf(s.Span, "cannot unpack %d-element tuple %s into %d variables", len(tupTy.Elems), unwrappedTy.String(), len(s.UnpackVars))
	}

	fc.consumeValue(s.Value, valTy, false)
	valArenaDepth := fc.arenaDepthOfExpr(s.Value)

	syms := make([]*Symbol, len(s.UnpackVars))
	for i, uv := range s.UnpackVars {
		elemTy := types.Type(types.TypeInvalid)
		if tupTy != nil && i < len(tupTy.Elems) {
			elemTy = tupTy.Elems[i]
		}
		if declTypes[i] != nil && declTypes[i] != types.TypeInvalid {
			if elemTy != types.TypeInvalid {
				fc.verifyAssignable(elemTy, declTypes[i], nil, uv.Span, fmt.Sprintf("declaration of %q", uv.Name))
			}
			elemTy = declTypes[i]
		}
		if uv.Name == "_" {
			if uv.IsOptName {
				fc.a.Rep.Errorf(uv.Span, "wildcard `_` cannot have optional `?` suffix")
			}
			syms[i] = nil
			continue
		}
		fc.a.checkOptionalIdentRule(uv.Name, uv.IsOptName, elemTy, uv.Span, "variable")
		sym := fc.declareLocal(uv.Name, elemTy, s.IsBind || s.IsConst, BindOwned, uv.Span)
		sym.IsConst = s.IsConst
		if elemTy != types.TypeInvalid && typeMayHoldArenaRef(elemTy) {
			elemDepth := valArenaDepth
			if valTuple, isTL := s.Value.(*ast.TupleLit); isTL && s.FallbackUnwind == nil && i < len(valTuple.Elems) {
				elemDepth = fc.arenaDepthOfExpr(valTuple.Elems[i])
			}
			sym.ArenaDepth = elemDepth
		}
		syms[i] = sym
	}
	fc.a.curBM.UnpackVarSymbols[s] = syms
	if s.IsConst && fc.probing == 0 && fc.a.errorCount() == beforeErrs {
		cv, ok := fc.a.evalComptimeExprInChecker(fc, s.Value, 0)
		if ok && cv != nil && s.FallbackUnwind != nil {
			if uw, okUW := unwrapFallbackOrUnwind(cv, s.FallbackUnwind, nil); okUW {
				cv = uw
			} else {
				cv = nil
			}
		}
		if ok && cv != nil && cv.kind == ctTuple && cv.tup != nil {
			for i, sym := range syms {
				if sym != nil && i < len(cv.tup.elems) {
					el := cv.tup.elems[i].clone().wrapResult(sym.Type)
					if wasOuterRuntime {
						if msg := fc.a.validateCrossesToRuntime(el, sym.Type); msg != "" {
							fc.a.Rep.Errorf(s.Value.GetSpan(), "%s", msg)
						}
					}
					freezeCtVal(el)
					sym.ConstVal = el
					fc.a.curBM.ConstVarVals[sym] = el
				}
			}
		} else if wasOuterRuntime {
			for _, sym := range syms {
				if sym != nil {
					if msg := fc.a.validateCrossTypeOnly(sym.Type, make(map[types.Type]bool)); msg != "" {
						fc.a.Rep.Errorf(s.Value.GetSpan(), "%s", msg)
					}
				}
			}
		}
	}
	if valTy == types.TypeNever {
		return types.TypeNever
	}
	return types.TypeVoid
}

func (fc *funcChecker) checkAssignStmt(s *ast.AssignStmt) types.Type {
	if s.IsUnpack {
		return fc.checkUnpackAssignStmt(s)
	}
	// Special shorthand: `map[key] = value` (Spec §7.2 / §8.2: shorthand for `map.insert(own k, own v)`)!
	if idx, isIdx := s.Target.(*ast.IndexExpr); isIdx && s.Op == token.Assign && s.FallbackUnwind == nil {
		baseTy := fc.checkExpr(idx.Base, true)
		if mapTy, isMap := types.CollView(baseTy).(*types.Map); isMap {
			fc.a.requireCollMethod(baseTy, "insert", idx.Span)
			_ = fc.checkMutablePlace(idx.Base, "insert into")
			kTy := fc.checkExprExpect(idx.Index, mapTy.Key, true)
			fc.verifyAssignable(kTy, mapTy.Key, idx.Index, idx.Index.GetSpan(), "map key")
			fc.consumeValue(idx.Index, mapTy.Key, false)

			vTy := fc.checkExprExpect(s.Value, mapTy.Value, true)
			fc.verifyAssignable(vTy, mapTy.Value, s.Value, s.Value.GetSpan(), "map value")
			fc.consumeValue(s.Value, mapTy.Value, false)

			// Arena escape & pin propagation on `map[k] = v`!
			kAd := fc.arenaDepthOfExpr(idx.Index)
			vAd := fc.arenaDepthOfExpr(s.Value)
			maxAd := kAd
			if vAd > maxAd {
				maxAd = vAd
			}
			mapRoot := fc.rootSymbolOfExpr(idx.Base)
			mapArenaDepth := fc.arenaDepthOfExpr(idx.Base)
			if maxAd > 0 {
				if mapArenaDepth > 0 && mapArenaDepth < maxAd {
					fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` from an inner `Arena` cannot escape into an outer `Arena` map")
				} else if mapRoot != nil && mapRoot.ScopeDepth < maxAd {
					fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` cannot escape into map %q declared in an outer scope", mapRoot.Name)
				} else if mapRoot == nil && mapArenaDepth == 0 && fc.unsafeDepth == 0 {
					fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` cannot escape into an outer map target")
				}
			}
			if typeMayHoldArenaRef(mapTy.Key) || typeMayHoldArenaRef(mapTy.Value) {
				if mapRoot != nil {
					fc.lowerPinnedArenasTo(mapRoot.ScopeDepth)
				} else {
					fc.lowerPinnedArenasTo(1)
				}
			}
			return types.TypeVoid
		}
	}

	// Note: Reassigning a moved `let` variable (`x = new_val`) brings `x` back to `MoveAlive`!
	// So if `s.Target` is a simple `IdentExpr` and `s.Op == token.Assign`, check mutability WITHOUT failing on `MoveMoved`!
	var targetTy types.Type
	var rootSym *Symbol
	if id, isId := s.Target.(*ast.IdentExpr); isId && s.Op == token.Assign {
		sym := fc.lookupVar(id.Name)
		if sym == nil {
			fc.a.Rep.Errorf(id.Span, "cannot assign to undeclared variable %q", id.Name)
			return types.TypeVoid
		}
		if sym.IsBind {
			fc.a.Rep.Report(diag.Diagnostic{
				Severity: diag.Error,
				Span:     id.Span,
				Message:  fmt.Sprintf("cannot reassign deeply immutable `bind` variable %q", sym.Name),
				Hint:     "declare it with `let` to make it reassignable",
				Data:     map[string]string{"kind": "bind_to_let", "declFile": sym.Span.Start.File, "declOffset": strconv.Itoa(sym.Span.Start.Offset)},
			})
		}
		if sym.Type != nil && types.IsArena(sym.Type) {
			if sym.BindKind != BindOwned {
				fc.a.Rep.Errorf(id.Span, "cannot reassign borrowed `&Arena` parameter %q (caller may hold live `ref[T]` allocations)", sym.Name)
			} else if fc.isArenaPinned(sym) {
				fc.a.Rep.ErrorWithHint(
					id.Span,
					fmt.Sprintf("cannot reassign `Arena` %q while `ref[T]` allocations from this scope may still be live", sym.Name),
					"wrap the phase that allocates `ref[T]` in a `{ ... }` block so references go out of scope before replacing the arena",
				)
			}
		}
		if fc.activeBorrows[sym] > 0 {
			fc.a.Rep.Errorf(id.Span, "cannot reassign %q while it is borrowed", sym.Name)
		}
		fc.a.infoFor(id).Sym = sym
		fc.a.infoFor(id).Type = sym.Type
		targetTy = sym.Type
		rootSym = sym
	} else {
		targetTy = fc.checkExpr(s.Target, true)
		rootSym = fc.checkMutablePlaceForAssign(s.Target, "mutate")
	}

	checkTargetArenaEscape := func(valExpr ast.Expr) {
		exprArenaDepth := fc.arenaDepthOfExpr(valExpr)
		placeRoot := fc.rootSymbolOfExpr(s.Target)
		targetArenaDepth := fc.arenaDepthOfExpr(s.Target)
		if exprArenaDepth > 0 {
			if targetArenaDepth > 0 && targetArenaDepth < exprArenaDepth {
				fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` from an inner `Arena` cannot escape into an outer `Arena` allocation")
			} else if placeRoot != nil && placeRoot.ScopeDepth < exprArenaDepth {
				fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` cannot escape into %q declared in an outer scope", placeRoot.Name)
			} else if placeRoot == nil && targetArenaDepth == 0 && fc.unsafeDepth == 0 {
				fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` cannot escape into an outer reference target")
			}
		}
		if typeMayHoldArenaRef(targetTy) {
			if placeRoot != nil {
				fc.lowerPinnedArenasTo(placeRoot.ScopeDepth)
			} else {
				fc.lowerPinnedArenasTo(1)
			}
		}
		if rootSym != nil {
			if _, isId := s.Target.(*ast.IdentExpr); isId && exprArenaDepth > rootSym.ArenaDepth {
				rootSym.ArenaDepth = exprArenaDepth
			}
		}
	}

	if s.FallbackUnwind != nil {
		optTy := fc.checkExprExpect(s.Value, types.NewOptional(targetTy), true)
		fc.checkFallbackUnwindStmt(s.FallbackUnwind)
		if elem, ok := guardUnwrapElem(optTy); ok {
			fc.verifyAssignable(elem, targetTy, nil, s.Value.GetSpan(), "assignment")
		} else if optTy != types.TypeInvalid {
			fc.reportBadFallbackSubject(s.Value.GetSpan(), optTy)
		}
		fc.consumeValue(s.Value, optTy, false)
		checkTargetArenaEscape(s.Value)
		if rootSym != nil && s.Op == token.Assign {
			if _, isId := s.Target.(*ast.IdentExpr); isId {
				fc.moves[rootSym] = MoveAlive
			}
		}
		return types.TypeVoid
	}

	if s.Op == token.Assign {
		valTy := fc.checkExprExpect(s.Value, targetTy, true)
		if name, same := samePlaceExpr(s.Target, s.Value); same && targetTy != types.TypeInvalid && valTy != types.TypeInvalid {
			fc.a.Rep.Report(diag.Diagnostic{
				Severity: diag.Warning,
				Span:     s.Span,
				Message:  fmt.Sprintf("assigning `%s` to itself has no effect", name),
				Hint:     "did you mean to assign a different value or parameter?",
			})
		}
		fc.verifyAssignable(valTy, targetTy, s.Value, s.Value.GetSpan(), "assignment")
		fc.consumeValue(s.Value, targetTy, false)
		checkTargetArenaEscape(s.Value)
		if rootSym != nil {
			if _, isId := s.Target.(*ast.IdentExpr); isId {
				fc.moves[rootSym] = MoveAlive
			}
		}
		return types.TypeVoid
	}

	// Compound assignment (`+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`, `>>=`)
	valTy := fc.checkExprExpect(s.Value, targetTy, true)
	if s.Op == token.PlusEq && targetTy != types.TypeInvalid && valTy != types.TypeInvalid && targetTy.Equals(valTy) {
		if lp := types.UnderlyingPrimitive(targetTy); lp == nil || !lp.IsNumeric() {
			// `a += b` calls `add_assign` (`AddAssignable`), or else assigns `a.add(b)` (`Addable`).
			if fi := fc.a.operatorMethod(targetTy, "add_assign", s.Span); fi != nil {
				fc.checkUnalignedPackedFieldAddr(s.Target, "call `+=` operator on")
				fc.a.infoFor(s.Target).OpMethod = fi
				fc.checkFuncConstAndComptimeAccess(fi, s.Span)
			} else if fi := fc.a.operatorMethod(targetTy, "add", s.Span); fi != nil {
				fc.checkUnalignedPackedFieldAddr(s.Target, "call `+=` operator on")
				fc.a.infoFor(s.Target).OpMethod = fi
				fc.checkFuncConstAndComptimeAccess(fi, s.Span)
			} else if !fc.a.typeParamHasBound(targetTy, fc.a.Prog.IfaceAddAssignable) && !fc.a.typeParamHasBound(targetTy, fc.a.Prog.IfaceAddable) {
				fc.a.Rep.Errorf(s.Span, "operator `+=` is not defined on %s (implement `AddAssignable` or `Addable`)", targetTy.String())
			}
			return types.TypeVoid
		}
	}
	fc.checkCompoundAssignTypes(s.Op, targetTy, valTy, s.Span)
	return types.TypeVoid
}

func (fc *funcChecker) checkUnalignedPackedFieldAddr(place ast.Expr, action string) {
	fieldName, fieldTy, insidePacked := fc.packedMemberInfo(place)
	if !insidePacked || fieldTy == nil || fieldTy == types.TypeInvalid {
		return
	}
	if al := fc.a.typeAlign(fieldTy); al > 1 {
		fc.a.Rep.ErrorWithHint(
			place.GetSpan(),
			fmt.Sprintf("cannot %s unaligned field %q inside `@packed` struct (alignment %d > 1)", action, fieldName, al),
			"copy the field into a local variable first, or mutate it with direct `=` or built-in compound assignment",
		)
	}
}

func (fc *funcChecker) packedMemberInfo(e ast.Expr) (string, types.Type, bool) {
	cur := unwrapExprWrappers(e)
	if cur == nil {
		return "", nil, false
	}
	leafInf := fc.a.infoFor(cur)
	if leafInf == nil || leafInf.Type == nil {
		return "", nil, false
	}
	leafTy := leafInf.Type
	fieldName := ""
	insidePacked := false
	for cur != nil {
		cur = unwrapExprWrappers(cur)
		switch v := cur.(type) {
		case *ast.MemberExpr:
			if v.Base == nil {
				return fieldName, leafTy, insidePacked
			}
			bExpr := unwrapExprWrappers(v.Base)
			var bTy types.Type
			if bInf := fc.a.infoFor(bExpr); bInf != nil && bInf.Type != nil {
				bTy = bInf.Type
			} else if bInf := fc.a.infoFor(v.Base); bInf != nil {
				bTy = bInf.Type
			}
			uBase := types.UnwrapDistinct(bTy)
			baseIsPtrOrRef := false
			if p, ok := uBase.(*types.Ptr); ok {
				uBase = types.UnwrapDistinct(p.Elem)
				baseIsPtrOrRef = true
			} else if r, ok := uBase.(*types.Ref); ok {
				uBase = types.UnwrapDistinct(r.Elem)
				baseIsPtrOrRef = true
			}
			if st, ok := uBase.(*types.Struct); ok && st.Packed {
				insidePacked = true
			}
			if fieldName == "" {
				fieldName = v.Member
			}
			if baseIsPtrOrRef {
				return fieldName, leafTy, insidePacked
			}
			cur = bExpr
		case *ast.CallExpr:
			cInf := fc.a.infoFor(v)
			if cInf == nil || (cInf.BuiltinName != "struct.field" && cInf.BuiltinName != "seq.elem" && cInf.BuiltinName != "enum.payload") || cInf.MethodRecv == nil {
				return fieldName, leafTy, insidePacked
			}
			rExpr := unwrapExprWrappers(cInf.MethodRecv)
			var rTy types.Type
			if rInf := fc.a.infoFor(rExpr); rInf != nil && rInf.Type != nil {
				rTy = rInf.Type
			} else if rInf := fc.a.infoFor(cInf.MethodRecv); rInf != nil {
				rTy = rInf.Type
			}
			uBase := types.UnwrapDistinct(rTy)
			baseIsPtrOrRef := false
			if p, ok := uBase.(*types.Ptr); ok {
				uBase = types.UnwrapDistinct(p.Elem)
				baseIsPtrOrRef = true
			} else if r, ok := uBase.(*types.Ref); ok {
				uBase = types.UnwrapDistinct(r.Elem)
				baseIsPtrOrRef = true
			}
			if st, ok := uBase.(*types.Struct); ok && st.Packed {
				insidePacked = true
			}
			if fieldName == "" {
				if cInf.ResolvedField != "" {
					fieldName = cInf.ResolvedField
				} else {
					fieldName = "field"
				}
			}
			if baseIsPtrOrRef {
				return fieldName, leafTy, insidePacked
			}
			cur = rExpr
		case *ast.IndexExpr:
			if v.Base == nil {
				return fieldName, leafTy, insidePacked
			}
			bExpr := unwrapExprWrappers(v.Base)
			var bTy types.Type
			if bInf := fc.a.infoFor(bExpr); bInf != nil && bInf.Type != nil {
				bTy = bInf.Type
			} else if bInf := fc.a.infoFor(v.Base); bInf != nil {
				bTy = bInf.Type
			}
			if _, isArr := types.UnwrapDistinct(bTy).(*types.StackArray); !isArr {
				return fieldName, leafTy, insidePacked
			}
			if fieldName == "" {
				fieldName = "element"
			}
			cur = bExpr
		default:
			return fieldName, leafTy, insidePacked
		}
	}
	return fieldName, leafTy, insidePacked
}

func (fc *funcChecker) checkUnpackAssignStmt(s *ast.AssignStmt) types.Type {
	targetTypes := make([]types.Type, len(s.UnpackTargets))
	rootSyms := make([]*Symbol, len(s.UnpackTargets))
	allTyped := len(s.UnpackTargets) >= 2
	anyTyped := false

	for i, tgt := range s.UnpackTargets {
		if id, isId := tgt.(*ast.IdentExpr); isId {
			if id.Name == "_" {
				fc.a.infoFor(id).Type = types.TypeVoid
				allTyped = false
				continue
			}
			sym := fc.lookupVar(id.Name)
			if sym == nil {
				fc.a.Rep.Errorf(id.Span, "cannot assign to undeclared variable %q", id.Name)
				targetTypes[i] = types.TypeInvalid
				allTyped = false
				continue
			}
			if sym.IsBind {
				fc.a.Rep.Report(diag.Diagnostic{
					Severity: diag.Error,
					Span:     id.Span,
					Message:  fmt.Sprintf("cannot reassign deeply immutable `bind` variable %q", sym.Name),
					Hint:     "declare it with `let` to make it reassignable",
					Data:     map[string]string{"kind": "bind_to_let", "declFile": sym.Span.Start.File, "declOffset": strconv.Itoa(sym.Span.Start.Offset)},
				})
			}
			if sym.Type != nil && types.IsArena(sym.Type) {
				if sym.BindKind != BindOwned {
					fc.a.Rep.Errorf(id.Span, "cannot reassign borrowed `&Arena` parameter %q (caller may hold live `ref[T]` allocations)", sym.Name)
				} else if fc.isArenaPinned(sym) {
					fc.a.Rep.ErrorWithHint(
						id.Span,
						fmt.Sprintf("cannot reassign `Arena` %q while `ref[T]` allocations from this scope may still be live", sym.Name),
						"wrap the phase that allocates `ref[T]` in a `{ ... }` block so references go out of scope before replacing the arena",
					)
				}
			}
			if fc.activeBorrows[sym] > 0 {
				fc.a.Rep.Errorf(id.Span, "cannot reassign %q while it is borrowed", sym.Name)
			}
			fc.a.infoFor(id).Sym = sym
			fc.a.infoFor(id).Type = sym.Type
			targetTypes[i] = sym.Type
			rootSyms[i] = sym
			if sym.Type == nil || sym.Type == types.TypeInvalid {
				allTyped = false
			} else {
				anyTyped = true
			}
		} else {
			tTy := fc.checkExpr(tgt, true)
			rSym := fc.checkMutablePlaceForAssign(tgt, "mutate")
			targetTypes[i] = tTy
			rootSyms[i] = rSym
			if tTy == nil || tTy == types.TypeInvalid {
				allTyped = false
			} else {
				anyTyped = true
			}
		}
	}

	var expectedTup types.Type
	if allTyped {
		expectedTup = &types.Tuple{Elems: targetTypes}
	} else if anyTyped && s.FallbackUnwind == nil {
		if _, isTL := s.Value.(*ast.TupleLit); isTL {
			expectedTup = &types.Tuple{Elems: targetTypes}
		}
	}

	var valTy types.Type
	var unwrappedTy types.Type
	if s.FallbackUnwind != nil {
		var expOpt types.Type
		if expectedTup != nil {
			expOpt = types.NewOptional(expectedTup)
		}
		valTy = fc.checkExprExpect(s.Value, expOpt, true)
		fc.checkFallbackUnwindStmt(s.FallbackUnwind)
		unwrappedTy = types.TypeInvalid
		if elem, ok := guardUnwrapElem(valTy); ok {
			unwrappedTy = elem
		} else if valTy != types.TypeInvalid {
			fc.reportBadFallbackSubject(s.Value.GetSpan(), valTy)
		}
	} else {
		valTy = fc.checkExprExpect(s.Value, expectedTup, true)
		unwrappedTy = valTy
	}

	var tupTy *types.Tuple
	if t, isTup := unwrappedTy.(*types.Tuple); isTup {
		tupTy = t
	} else if dist, isDist := unwrappedTy.(*types.Distinct); isDist {
		if t, isTup := dist.Base.(*types.Tuple); isTup {
			tupTy = t
		}
	}

	if tupTy == nil {
		if unwrappedTy != types.TypeInvalid && unwrappedTy != types.TypeNever {
			fc.a.Rep.Errorf(s.Value.GetSpan(), "cannot unpack non-tuple type %s", unwrappedTy.String())
		}
	} else if len(s.UnpackTargets) != len(tupTy.Elems) {
		fc.a.Rep.Errorf(s.Span, "cannot unpack %d-element tuple %s into %d targets", len(tupTy.Elems), unwrappedTy.String(), len(s.UnpackTargets))
	}

	fc.consumeValue(s.Value, valTy, false)
	exprArenaDepth := fc.arenaDepthOfExpr(s.Value)

	for i, tgt := range s.UnpackTargets {
		if id, isId := tgt.(*ast.IdentExpr); isId && id.Name == "_" {
			continue
		}
		tTy := targetTypes[i]
		rSym := rootSyms[i]
		if tupTy != nil && i < len(tupTy.Elems) && tTy != nil && tTy != types.TypeInvalid {
			fc.verifyAssignable(tupTy.Elems[i], tTy, nil, tgt.GetSpan(), "assignment")
		}
		placeRoot := fc.rootSymbolOfExpr(tgt)
		targetArenaDepth := fc.arenaDepthOfExpr(tgt)
		elemDepth := exprArenaDepth
		if valTuple, isTL := s.Value.(*ast.TupleLit); isTL && s.FallbackUnwind == nil && i < len(valTuple.Elems) {
			elemDepth = fc.arenaDepthOfExpr(valTuple.Elems[i])
		}
		if elemDepth > 0 && tTy != nil && typeMayHoldArenaRef(tTy) {
			if targetArenaDepth > 0 && targetArenaDepth < elemDepth {
				fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` from an inner `Arena` cannot escape into an outer `Arena` allocation")
			} else if placeRoot != nil && placeRoot.ScopeDepth < elemDepth {
				fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` cannot escape into %q declared in an outer scope", placeRoot.Name)
			} else if placeRoot == nil && targetArenaDepth == 0 && fc.unsafeDepth == 0 {
				fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` cannot escape into an outer reference target")
			}
		}
		if tTy != nil && typeMayHoldArenaRef(tTy) {
			if placeRoot != nil {
				fc.lowerPinnedArenasTo(placeRoot.ScopeDepth)
			} else {
				fc.lowerPinnedArenasTo(1)
			}
			if rSym != nil {
				if _, isId := tgt.(*ast.IdentExpr); isId && elemDepth > rSym.ArenaDepth {
					rSym.ArenaDepth = elemDepth
				}
			}
		}
		if rSym != nil {
			if _, isId := tgt.(*ast.IdentExpr); isId {
				fc.moves[rSym] = MoveAlive
			}
		}
	}

	return types.TypeVoid
}

func (fc *funcChecker) checkCompoundAssignTypes(op token.Kind, lhs, rhs types.Type, span diag.Span) {
	if lhs == types.TypeInvalid || rhs == types.TypeInvalid {
		return
	}
	lp := types.UnderlyingPrimitive(lhs)
	if lp == nil || !lhs.Equals(rhs) {
		// Exception: shift count can be `int` or matching integer type
		if (op == token.ShlEq || op == token.ShrEq) && lp != nil && lp.IsInteger() {
			if rp := types.UnderlyingPrimitive(rhs); rp != nil && rp.IsInteger() {
				return
			}
		}
		fc.a.Rep.Errorf(span, "invalid operand types %s and %s for %s", lhs.String(), rhs.String(), op.String())
		return
	}
	switch op {
	case token.PlusEq, token.MinusEq, token.StarEq, token.SlashEq, token.PercentEq:
		if !lp.IsNumeric() {
			fc.a.Rep.Errorf(span, "arithmetic assignment %s requires numeric type, got %s", op.String(), lhs.String())
		}
	case token.AmpEq, token.PipeEq, token.CaretEq, token.ShlEq, token.ShrEq:
		if !lp.IsInteger() {
			fc.a.Rep.Errorf(span, "bitwise assignment %s requires integer type, got %s", op.String(), lhs.String())
		}
	}
}

func (fc *funcChecker) checkUnwindStmt(u *ast.UnwindStmt) types.Type {
	tgt := fc.resolveUnwindTarget(u)
	if tgt == nil {
		if u.Value != nil {
			_ = fc.checkExpr(u.Value, true)
		}
		return types.TypeNever
	}

	isCont := u.Target == ast.UnwindNext || u.Target == ast.UnwindNextLabel
	// Expected value type for this unwind:
	exp := tgt.expectedType
	isBreakCollect := !isCont && tgt.kind == cfFor && tgt.isCollecting
	if isBreakCollect {
		if tgt.collectFixedTarget != nil {
			// `for <- val` breaking out of a struct/tuple/array-collecting `comptime for` yields the whole aggregate type.
			exp = tgt.collectFixedTarget
		} else {
			// `for <- val` breaking out of a collection-producing `for` / `comptime for` yields the whole collection type.
			exp = tgt.collectColType
		}
	}
	wrapOpt := false
	if (tgt.kind == cfFor || tgt.kind == cfWhile) && tgt.isForNoElse && exp != nil {
		// In `let found? = for (x in list) { if (x > 10) for <- x }`,
		// the loop expression has type `?int`, while `for <- x` yields `int` (which wraps into `?int`)!
		if opt, isOpt := exp.(*types.Optional); isOpt {
			exp = opt.Elem
			wrapOpt = true
		}
	}

	isStructFieldDefault := false
	if u.Value == nil {
		// Bare unwind (`<-`, `next <-`, `for <-`, `func <-`)
		if isCont && tgt.collectFixedTarget != nil {
			if tgt.collectStructField != nil {
				sf := tgt.collectStructField
				_, isOpt := sf.Type.(*types.Optional)
				if !sf.HasDefault && !isOpt {
					fc.a.Rep.ErrorWithHint(
						u.Span,
						fmt.Sprintf("cannot use bare `next <-` for required field %q of struct %s", sf.Name, tgt.collectFixedTarget.String()),
						"provide a field value (`next <- expr`) or use `next <-` only for optional (`?T`) or default fields",
					)
				} else {
					isStructFieldDefault = true
				}
			} else {
				fc.a.Rep.Errorf(u.Span, "cannot omit value in `next <-` inside %s-collecting `comptime for` (write `next <- value`)", tgt.collectFixedTarget.String())
			}
		} else if !isCont && tgt.kind == cfFor && tgt.collectFixedTarget != nil {
			fc.a.Rep.Errorf(u.Span, "cannot use bare `for <-` inside %s-collecting `comptime for` (provide a full %s value)", tgt.collectFixedTarget.String(), tgt.collectFixedTarget.String())
		} else if tgt.kind == cfFunc && tgt.isInferredFunc {
			tgt.hasVoidUnwind = true
		} else if !isCont && exp != nil && !exp.Equals(types.TypeVoid) {
			fc.a.Rep.Errorf(u.Span, "unwind `%s` requires a value of type %s", u.TargetName(), exp.String())
		}
	} else {
		var valTy types.Type
		if exp != nil && !exp.Equals(types.TypeVoid) {
			valTy = fc.checkExprExpect(u.Value, exp, true)
			fc.verifyAssignable(valTy, exp, u.Value, u.Value.GetSpan(), "unwind value")
			fc.consumeValue(u.Value, exp, false)
			tgt.hitValueExit = true
		} else if exp != nil && exp.Equals(types.TypeVoid) {
			// Void loop or void block/function: if `for <- val` is used in a statement-position loop, disallow or error if `func <- val` on void function!
			if tgt.kind == cfFunc {
				fc.a.Rep.Errorf(u.Span, "void function cannot unwind with a value (`func <- ...`)")
				_ = fc.checkExpr(u.Value, true)
			} else {
				valTy = fc.checkExpr(u.Value, true)
				fc.consumeValue(u.Value, valTy, false)
			}
		} else {
			// Inferring block/loop/func yield type bottom-up!
			var expHint types.Type
			if tgt.isInferredFunc && len(tgt.yieldTypes) > 0 {
				cand := tgt.yieldTypes[0]
				if cand != nil && cand != types.TypeInvalid && cand != types.TypeNever {
					expHint = cand
					if _, isNone := u.Value.(*ast.NoneLit); isNone {
						expHint = types.NewOptional(cand)
					}
				}
			}
			if expHint != nil {
				valTy = fc.checkExprExpect(u.Value, expHint, true)
			} else {
				valTy = fc.checkExpr(u.Value, true)
			}
			if tgt.isInferredFunc && valTy == types.TypeVoid {
				fc.a.Rep.Errorf(u.Span, "cannot yield `void` value from `func <-`; use bare `func <-` instead")
				valTy = types.TypeInvalid
			}
			if isBreakCollect {
				tgt.breakColTypes = append(tgt.breakColTypes, valTy)
			} else {
				tgt.yieldTypes = append(tgt.yieldTypes, valTy)
			}
			tgt.yieldExprs = append(tgt.yieldExprs, inferredYield{expr: u.Value, ty: valTy, span: u.Span})
			tgt.hitValueExit = true
			fc.consumeValue(u.Value, valTy, false)
		}

		ad := fc.arenaDepthOfExpr(u.Value)
		if tgt.kind == cfFunc {
			if (types.ContainsArenaRef(valTy) || ad > tgt.outerScopeDepth) && !fc.hasMutArenaParam && ad <= tgt.outerScopeDepth {
				fc.a.Rep.Errorf(u.Span, "cannot return `ref[T]` via `func <-` unless the function takes an `&Arena` parameter")
			}
			if ad > tgt.outerScopeDepth {
				fc.a.Rep.ErrorWithHint(
					u.Span,
					"arena reference `ref[T]` allocated from a local or owned `Arena` cannot escape the function via `func <-`",
					"allocate into a caller-provided `&Arena` parameter instead of a local `Arena`",
				)
			}
		} else if !isCont || tgt.isCollecting {
			if ad > tgt.outerScopeDepth {
				fc.a.Rep.Errorf(u.Span, "arena reference `ref[T]` cannot escape the scope of its `Arena` via `%s`", u.TargetName())
			} else if ad > tgt.maxYieldArenaDepth {
				tgt.maxYieldArenaDepth = ad
			}
			if typeMayHoldArenaRef(valTy) {
				fc.lowerPinnedArenasTo(tgt.outerScopeDepth)
			}
		}
	}

	resTy := exp
	if resTy == nil {
		resTy = tgt.expectedType
	}
	if resTy == nil && len(tgt.yieldTypes) > 0 {
		resTy = tgt.yieldTypes[0]
	}

	fc.a.curBM.UnwindInfos[u] = &UnwindInfo{
		TargetID:             tgt.id,
		TargetKind:           u.Target,
		IsContinue:           isCont,
		WrapInOptional:       wrapOpt,
		IsStructFieldDefault: isStructFieldDefault,
		ResultType:           resTy,
	}
	return types.TypeNever
}

func (fc *funcChecker) resolveUnwindTarget(u *ast.UnwindStmt) *cfTarget {
	for i := len(fc.cfStack) - 1; i >= 0; i-- {
		cand := fc.cfStack[i]
		// Spec §10.4 Rule 1: Unwinds can NEVER cross a function or lambda boundary!
		if cand.funcDepth != fc.funcDepth {
			fc.a.Rep.ErrorWithHint(
				u.Span,
				"unwind (`<-`) cannot cross a function or lambda boundary",
				"unwinds may only target blocks, loops, conditionals, or `func <-` inside the current function/lambda",
			)
			return nil
		}

		switch u.Target {
		case ast.UnwindBlock:
			if cand.kind == cfBlock {
				return cand
			}
		case ast.UnwindIf:
			if cand.kind == cfIf {
				return cand
			}
		case ast.UnwindMatch:
			if cand.kind == cfMatch {
				return cand
			}
		case ast.UnwindFor:
			if cand.kind == cfFor {
				return cand
			}
		case ast.UnwindWhile:
			if cand.kind == cfWhile {
				return cand
			}
		case ast.UnwindNext:
			if cand.kind == cfFor || cand.kind == cfWhile {
				return cand
			}
		case ast.UnwindFunc:
			if cand.kind == cfFunc {
				return cand
			}
		case ast.UnwindLabel:
			if (cand.kind == cfFor || cand.kind == cfWhile || cand.kind == cfBlock) && cand.label == u.Label {
				return cand
			}
		case ast.UnwindNextLabel:
			if (cand.kind == cfFor || cand.kind == cfWhile) && cand.label == u.Label {
				return cand
			}
		}
	}

	fc.a.Rep.Errorf(u.Span, "no enclosing %s target found in current function for unwind", unwindDesc(u))
	return nil
}

func unwindDesc(u *ast.UnwindStmt) string {
	switch u.Target {
	case ast.UnwindBlock:
		return "`{ ... }` block"
	case ast.UnwindIf:
		return "`if`"
	case ast.UnwindMatch:
		return "`match`"
	case ast.UnwindFor:
		return "`for` loop"
	case ast.UnwindWhile:
		return "`while` loop"
	case ast.UnwindNext:
		return "loop"
	case ast.UnwindFunc:
		return "function"
	case ast.UnwindLabel:
		return fmt.Sprintf("label %q", u.Label)
	case ast.UnwindNextLabel:
		return fmt.Sprintf("loop label %q", u.Label)
	default:
		return "target"
	}
}

// -----------------------------------------------------------------------------
// Expressions (`if`, `for`, `match`, Operators, Literals, Lambdas)
// -----------------------------------------------------------------------------

func (fc *funcChecker) checkExpr(e ast.Expr, needValue bool) types.Type {
	return fc.checkExprExpect(e, nil, needValue)
}

func (fc *funcChecker) checkExprExpect(e ast.Expr, expected types.Type, needValue bool) types.Type {
	if e == nil {
		return types.TypeInvalid
	}
	inf := fc.a.infoFor(e)

	if en := types.AsResult(expected); en != nil {
		if ty, ok := fc.checkBareResultMember(e, en); ok {
			return ty
		}
	}

	switch v := e.(type) {
	case *ast.IntLit:
		ty := fc.adaptIntLit(v.Val, false, expected, v.Span)
		inf.Type = ty
		return ty

	case *ast.FloatLit:
		ty := fc.adaptFloatLit(expected)
		inf.Type = ty
		return ty

	case *ast.BoolLit:
		inf.Type = types.TypeBool
		return types.TypeBool

	case *ast.CharLit:
		inf.Type = types.TypeChar
		return types.TypeChar

	case *ast.StringLit:
		// If expected is a Distinct over `string`, adapt!
		if d, ok := expected.(*types.Distinct); ok && d.Base.Equals(types.TypeString) {
			inf.Type = d
			return d
		}
		inf.Type = types.TypeString
		return types.TypeString

	case *ast.InterpStringExpr:
		for _, sub := range v.Exprs {
			subTy := fc.checkExpr(sub, true)
			if subTy != types.TypeInvalid && !fc.a.supportsToString(subTy) {
				if _, isOpt := subTy.(*types.Optional); isOpt {
					fc.a.Rep.ErrorWithHint(
						sub.GetSpan(),
						fmt.Sprintf("cannot directly interpolate optional value of type %s without unwrapping", subTy.String()),
						`provide a fallback with ?? (e.g. "${val? ?? "none"}")`,
					)
				} else {
					fc.a.Rep.Errorf(sub.GetSpan(), "interpolated value of type %s does not implement `Printable` (`to_string`)", subTy.String())
				}
			} else if subTy != types.TypeInvalid && fc.inConstContext() {
				fc.checkInterpConst(subTy, sub.GetSpan())
			}
		}
		inf.Type = types.TypeString
		return types.TypeString

	case *ast.NoneLit:
		if expected != nil {
			if opt, ok := expected.(*types.Optional); ok {
				inf.Type = opt
				return opt
			}
		}
		fc.a.Rep.ErrorWithHint(
			v.Span,
			"cannot infer optional type for bare `none` without type context",
			"annotate the variable type (e.g. `let x?: ?int = none`) or function return type",
		)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid

	case *ast.IdentExpr:
		sym := fc.lookupVar(v.Name)
		if sym == nil {
			hint, cands := fc.unknownNameHint(v.Name)
			fc.a.errorWithRename(v.Span, fmt.Sprintf("undeclared identifier %q", v.Name), v.Name, hint, cands)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if sym.Mod != nil && !IsExported(sym.Name) && !fc.a.SamePackage(fc.mod, sym.Mod) {
			fc.a.Rep.Errorf(v.Span, "cannot access unexported identifier %q from package %q", sym.Name, sym.Mod.Path)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if sym.IsTopLevel && fc.a.inBodyPass {
			fc.a.resolveTopVar(sym)
		}
		if sym.FuncInst != nil && fc.a.inBodyPass {
			_ = fc.a.ensureFuncRetResolved(sym.FuncInst, v.Span)
		}
		if sym.Deprecated {
			msg := ""
			if sym.DeprecatedMsg != "" {
				msg = ": " + sym.DeprecatedMsg
			}
			fc.a.Rep.Warnf(v.Span, "use of deprecated identifier %q%s", sym.Name, msg)
		}
		if (sym.IsTest || sym.IsTestOnly || (sym.FuncInst != nil && sym.FuncInst == fc.a.Prog.TestMainFunc)) && !fc.isTestContext() {
			callerName := "<top-level>"
			if fc.curFn != nil {
				callerName = fc.curFn.Name
			}
			fc.a.Rep.Errorf(v.Span, "cannot access test symbol %q from non-test context %q", sym.Name, callerName)
		}
		if sym.ComptimeOnly && !fc.inComptimeOnlyContext() && fc.allowComptimeChain == 0 {
			fc.a.Rep.ErrorWithHint(
				v.Span,
				fmt.Sprintf("cannot use `@comptime_only` symbol %q in runtime code", sym.Name),
				"use it inside `comptime`, a `const` initializer, or another `@comptime_only` function",
			)
		}
		if fc.comptimeExprDepth > 0 && !sym.IsTopLevel && !sym.IsConst && sym.ScopeDepth < fc.comptimeMinScope {
			fc.a.Rep.ErrorWithHint(
				v.Span,
				fmt.Sprintf("cannot reference runtime variable %q inside `comptime`", sym.Name),
				"declare it with `const` if its value is known at compile time",
			)
		}
		fc.checkSymReadable(sym, v.Span)
		if (sym.IsConstParam || sym.IsConst) && sym.ConstVal != nil && !fc.inComptimeOnlyContext() && fc.allowComptimeChain == 0 {
			if cv, ok := sym.ConstVal.(*ctVal); ok {
				if msg := fc.a.validateCrossesToRuntime(cv, sym.Type); msg != "" {
					fc.a.Rep.ErrorWithHint(
						v.Span,
						fmt.Sprintf("compile-time constant %q cannot cross into runtime code: %s", sym.Name, msg),
						"extract plain/string values at compile time, or use it inside `comptime`",
					)
				}
			}
		}
		// Check lambda capture if referencing a local from an enclosing function scope!
		if !sym.IsTopLevel && !sym.IsConstParam && sym.DeclFuncDepth < fc.funcDepth {
			fc.recordLambdaCapture(sym, v.Span)
		}
		inf.Sym = sym
		inf.Type = sym.Type
		if sym.Type == nil {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		return sym.Type

	case *ast.UnaryExpr:
		return fc.checkUnaryExpr(v, expected)

	case *ast.BinaryExpr:
		return fc.checkBinaryExpr(v, expected)

	case *ast.MemberExpr:
		return fc.checkMemberExpr(v, expected)

	case *ast.CallExpr:
		return fc.checkCallExpr(v, expected)

	case *ast.IndexExpr:
		return fc.checkIndexExpr(v)

	case *ast.SliceExpr:
		return fc.checkSliceExpr(v)

	case *ast.RangeExpr:
		return fc.checkRangeExpr(v, expected)

	case *ast.VecLit:
		return fc.checkVecOrArrayLit(v, expected)

	case *ast.ArrayLit:
		return fc.checkArrayLit(v, expected)

	case *ast.SetLit:
		return fc.checkSetLit(v, expected)

	case *ast.MapLit:
		return fc.checkMapLit(v, expected)

	case *ast.EmptyHashLit:
		return fc.checkEmptyHashLit(v, expected)

	case *ast.TupleLit:
		return fc.checkTupleLit(v, expected)

	case *ast.StructLit:
		return fc.checkStructLit(v, expected)

	case *ast.FuncLit:
		return fc.checkFuncLit(v, expected)

	case *ast.UnsafeExpr:
		return fc.checkUnsafeExpr(v, expected, needValue)

	case *ast.ComptimeExpr:
		return fc.checkComptimeExpr(v, expected, needValue)

	case *ast.BlockExpr:
		return fc.checkBlockExpr(v, expected, needValue)

	case *ast.StmtBodyExpr:
		stTy := fc.checkStmt(v.Stmt)
		inf.Type = stTy
		return stTy

	case *ast.IfExpr:
		return fc.checkIfExpr(v, expected, needValue)

	case *ast.ForExpr:
		return fc.checkForExpr(v, expected, needValue)

	case *ast.WhileExpr:
		return fc.checkWhileExpr(v, expected, needValue)

	case *ast.MatchExpr:
		return fc.checkMatchExpr(v, expected, needValue)
	}

	return types.TypeInvalid
}

func (fc *funcChecker) adaptIntLit(val uint64, negated bool, expected types.Type, span diag.Span) types.Type {
	target := expected
	if opt, ok := target.(*types.Optional); ok {
		target = opt.Elem
	}
	if prim := types.UnderlyingPrimitive(target); prim != nil && prim.IsInteger() {
		if !prim.FitsUnsignedLiteral(val, negated) {
			prefix := ""
			if negated {
				prefix = "-"
			}
			fc.a.Rep.Errorf(span, "integer literal %s%d out of range for %s", prefix, val, target.String())
		}
		return target
	}
	if !types.TypeInt.FitsUnsignedLiteral(val, negated) {
		if !negated {
			// Large unsigned hex/int literal that only fits u64: if no expected type, error unless u64(lit)
			fc.a.Rep.Errorf(span, "integer literal %d overflows `int` (`i64`); annotate as `u64` if unsigned", val)
		}
	}
	return types.TypeInt
}

func (fc *funcChecker) adaptFloatLit(expected types.Type) types.Type {
	target := expected
	if opt, ok := target.(*types.Optional); ok {
		target = opt.Elem
	}
	if prim := types.UnderlyingPrimitive(target); prim != nil && prim.IsFloat() {
		return target
	}
	return types.TypeFloat
}

func (fc *funcChecker) checkUnaryExpr(u *ast.UnaryExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(u)
	switch u.Op {
	case token.Minus:
		// Handle `-<IntLit>` directly so `-128` fits in `i8` and `-9223372036854775808` fits in `i64`!
		if lit, isLit := u.Operand.(*ast.IntLit); isLit {
			ty := fc.adaptIntLit(lit.Val, true, expected, u.Span)
			fc.a.infoFor(lit).Type = ty
			inf.Type = ty
			return ty
		}
		sub := fc.checkExprExpect(u.Operand, expected, true)
		if sub == nil || sub == types.TypeInvalid {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		p := types.UnderlyingPrimitive(sub)
		if p == nil || (!p.IsSignedInt() && !p.IsFloat()) {
			fc.a.Rep.Errorf(u.Span, "unary `-` requires signed integer or float, got %s", sub.String())
		}
		inf.Type = sub
		return sub

	case token.KwNot:
		sub := fc.checkExprExpect(u.Operand, types.TypeBool, true)
		if sub == nil || sub == types.TypeInvalid {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if !sub.Equals(types.TypeBool) {
			fc.a.Rep.Errorf(u.Span, "`not` requires `bool` operand, got %s", sub.String())
		}
		inf.Type = types.TypeBool
		return types.TypeBool

	case token.Tilde:
		sub := fc.checkExprExpect(u.Operand, expected, true)
		if sub == nil || sub == types.TypeInvalid {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		p := types.UnderlyingPrimitive(sub)
		if p == nil || !p.IsInteger() {
			fc.a.Rep.Errorf(u.Span, "bitwise `~` requires integer operand, got %s", sub.String())
		}
		inf.Type = sub
		return sub

	case token.Star:
		if fc.unsafeDepth == 0 {
			fc.a.Rep.Errorf(u.Span, "raw pointer dereference `*p` requires `unsafe`")
		}
		sub := fc.checkExpr(u.Operand, true)
		pt, ok := sub.(*types.Ptr)
		if !ok {
			if sub != types.TypeInvalid {
				if _, isRef := sub.(*types.Ref); isRef {
					fc.a.Rep.ErrorWithHint(
						u.Span,
						fmt.Sprintf("cannot dereference non-pointer type %s", sub.String()),
						"`ref[T]` dereferences automatically on `.field` and method calls; `*p` is only for raw `ptr[T]`",
					)
				} else {
					fc.a.Rep.Errorf(u.Span, "cannot dereference non-pointer type %s", sub.String())
				}
			}
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		inf.Type = pt.Elem
		return pt.Elem

	case token.Amp:
		// Prefix `&x` is only valid as a mutable borrow argument in a call (handled by checkCallExpr)
		// or `if (&x?)` (handled by parser/checkIfExpr)!
		fc.a.Rep.ErrorWithHint(
			u.Span,
			"mutable borrow `&expr` is only valid as a function call argument or in `if (&x?)`",
			"borrowed references cannot be stored in local variables; use `addr_of(x)` inside `unsafe` if you need a raw pointer `ptr[T]`",
		)
		sub := fc.checkExpr(u.Operand, true)
		inf.Type = sub
		return sub

	case token.KwOwn:
		sub := fc.checkExprExpect(u.Operand, expected, true)
		inf.Type = sub
		return sub
	}
	inf.Type = types.TypeInvalid
	return types.TypeInvalid
}

func (fc *funcChecker) checkBinaryExpr(b *ast.BinaryExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(b)

	// Optional fallback `opt_expr ?? fallback_expr`
	if b.Op == token.QuestQuest {
		var optHint types.Type
		if expected != nil {
			optHint = types.NewOptional(expected)
		}
		lty := fc.checkExprExpect(b.Left, optHint, true)
		if resEn := types.AsResult(lty); resEn != nil {
			return fc.checkResultFallback(b, resEn, lty)
		}
		optL, ok := lty.(*types.Optional)
		if !ok {
			if lty != types.TypeInvalid {
				fc.reportBadFallbackSubject(b.Left.GetSpan(), lty)
			}
			_ = fc.checkExpr(b.Right, true)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}

		// Right-hand side may evaluate to `Elem` (giving `Elem`) OR `?Elem` when chained (`a? ?? b? ?? 0`)!
		rExpect := optL.Elem
		if expected != nil {
			if _, expIsOpt := expected.(*types.Optional); expIsOpt {
				rExpect = expected
			}
		}
		preRightMoves := fc.cloneMoves()
		rty := fc.checkExprExpect(b.Right, rExpect, true)
		if rty == types.TypeNever {
			fc.moves = preRightMoves
		}
		fc.consumeValue(b.Left, lty, false)

		if rty.Equals(optL.Elem) || rty == types.TypeNever {
			fc.consumeValue(b.Right, optL.Elem, false)
			inf.Type = optL.Elem
			return optL.Elem
		}
		if rOpt, isROpt := rty.(*types.Optional); isROpt && rOpt.Elem.Equals(optL.Elem) {
			fc.consumeValue(b.Right, rOpt, false)
			inf.Type = rOpt
			return rOpt
		}
		fc.verifyAssignable(rty, optL.Elem, b.Right, b.Right.GetSpan(), "`??` fallback")
		fc.consumeValue(b.Right, optL.Elem, false)
		inf.Type = optL.Elem
		return optL.Elem
	}

	// Logical `and` / `or`
	if b.Op == token.KwAnd || b.Op == token.KwOr {
		lty := fc.checkExprExpect(b.Left, types.TypeBool, true)
		rty := fc.checkExprExpect(b.Right, types.TypeBool, true)
		if lty != types.TypeInvalid && !lty.Equals(types.TypeBool) {
			fc.a.Rep.Errorf(b.Left.GetSpan(), "`%s` requires `bool` left operand, got %s", b.Op.String(), lty.String())
		}
		if rty != types.TypeInvalid && !rty.Equals(types.TypeBool) {
			fc.a.Rep.Errorf(b.Right.GetSpan(), "`%s` requires `bool` right operand, got %s", b.Op.String(), rty.String())
		}
		inf.Type = types.TypeBool
		return types.TypeBool
	}

	// Equality `==` / `!=` (including `opt? == none` and literal adaptation between operands!)
	if b.Op == token.Eq || b.Op == token.Neq {
		var lty, rty types.Type
		if _, rIsNone := b.Right.(*ast.NoneLit); rIsNone {
			lty = fc.checkExpr(b.Left, true)
			if _, isOpt := lty.(*types.Optional); !isOpt && lty != types.TypeInvalid {
				hint := "only optional types (`?T`) can be compared to `none`"
				if types.AsResult(lty) != nil {
					hint = "for a result type, handle it with a result chain (`expr: default ...`) or `if (expr as v)`"
				}
				fc.a.Rep.ErrorWithHint(b.Span, fmt.Sprintf("cannot compare non-optional %s to `none`", lty.String()), hint)
				inf.Type = types.TypeBool
				return types.TypeBool
			}
			rty = fc.checkExprExpect(b.Right, lty, true)
			inf.Type = types.TypeBool
			return types.TypeBool
		}
		if _, lIsNone := b.Left.(*ast.NoneLit); lIsNone {
			rty = fc.checkExpr(b.Right, true)
			if _, isOpt := rty.(*types.Optional); !isOpt && rty != types.TypeInvalid {
				hint := "only optional types (`?T`) can be compared to `none`"
				if types.AsResult(rty) != nil {
					hint = "for a result type, handle it with a result chain (`expr: default ...`) or `if (expr as v)`"
				}
				fc.a.Rep.ErrorWithHint(b.Span, fmt.Sprintf("cannot compare `none` to non-optional %s", rty.String()), hint)
				inf.Type = types.TypeBool
				return types.TypeBool
			}
			lty = fc.checkExprExpect(b.Left, rty, true)
			inf.Type = types.TypeBool
			return types.TypeBool
		}

		lty, rty = fc.checkOperandPairWithAdaptation(b.Left, b.Right, nil)
		if lty != nil && rty != nil && lty != types.TypeInvalid && rty != types.TypeInvalid {
			matched := true
			if !lty.Equals(rty) {
				// Allow `T == ?T` or `?T == T` by wrapping the `T` side
				if oL, ok := lty.(*types.Optional); ok && oL.Elem.Equals(rty) {
					fc.a.infoFor(b.Right).WrapOptional = true
					rty = lty
				} else if oR, ok := rty.(*types.Optional); ok && oR.Elem.Equals(lty) {
					fc.a.infoFor(b.Left).WrapOptional = true
					lty = rty
				} else {
					matched = false
					if lp, rp := types.UnderlyingPrimitive(lty), types.UnderlyingPrimitive(rty); lp != nil && rp != nil && lp.IsNumeric() && rp.IsNumeric() {
						fc.a.Rep.ErrorWithHint(
							b.Span,
							fmt.Sprintf("cannot compare mismatched types %s and %s", lty.String(), rty.String()),
							fmt.Sprintf("numbers never convert implicitly; convert one side, e.g. `%s(...)`", lty.String()),
						)
					} else {
						fc.a.Rep.Errorf(b.Span, "cannot compare mismatched types %s and %s", lty.String(), rty.String())
					}
				}
			}
			if matched {
				if !fc.a.supportsEq(lty) {
					fc.a.Rep.Errorf(b.Span, "equality `%s` is not defined on type %s", b.Op.String(), lty.String())
				} else {
					if fc.inConstContext() {
						fc.checkConstBinaryMethod(lty, true, b.Span)
					}
					if name, same := samePlaceExpr(b.Left, b.Right); same {
						p := types.UnderlyingPrimitive(lty)
						if (p == nil || !p.IsFloat()) && !fc.isTestContext() {
							fc.a.Rep.Report(diag.Diagnostic{
								Severity: diag.Warning,
								Span:     b.Span,
								Message:  fmt.Sprintf("comparing `%s` to itself with `%s`", name, b.Op.String()),
								Hint:     "did you mean to compare against a different variable or field?",
							})
						}
					}
				}
			}
		}
		inf.Type = types.TypeBool
		return types.TypeBool
	}

	// Ordering `<`, `<=`, `>`, `>=`
	if b.Op == token.Lt || b.Op == token.Lte || b.Op == token.Gt || b.Op == token.Gte {
		lty, rty := fc.checkOperandPairWithAdaptation(b.Left, b.Right, nil)
		if lty != nil && rty != nil && lty != types.TypeInvalid && rty != types.TypeInvalid {
			if !lty.Equals(rty) {
				if lp, rp := types.UnderlyingPrimitive(lty), types.UnderlyingPrimitive(rty); lp != nil && rp != nil && lp.IsNumeric() && rp.IsNumeric() {
					fc.a.Rep.ErrorWithHint(
						b.Span,
						fmt.Sprintf("cannot compare mismatched types %s and %s", lty.String(), rty.String()),
						fmt.Sprintf("numbers never convert implicitly; convert one side, e.g. `%s(...)`", lty.String()),
					)
				} else {
					fc.a.Rep.Errorf(b.Span, "cannot compare mismatched types %s and %s", lty.String(), rty.String())
				}
			} else if !fc.a.supportsCmp(lty) {
				fc.a.Rep.Errorf(b.Span, "ordering operator `%s` is not defined on %s (type does not implement Comparable)", b.Op.String(), lty.String())
			} else {
				if fc.inConstContext() {
					fc.checkConstBinaryMethod(lty, false, b.Span)
				}
				if name, same := samePlaceExpr(b.Left, b.Right); same {
					p := types.UnderlyingPrimitive(lty)
					if p == nil || !p.IsFloat() {
						fc.a.Rep.Report(diag.Diagnostic{
							Severity: diag.Warning,
							Span:     b.Span,
							Message:  fmt.Sprintf("comparing `%s` to itself with `%s`", name, b.Op.String()),
							Hint:     "did you mean to compare against a different variable or field?",
						})
					}
				}
			}
		}
		inf.Type = types.TypeBool
		return types.TypeBool
	}

	// Bitwise shifts `<<`, `>>`
	if b.Op == token.Shl || b.Op == token.Shr {
		lty := fc.checkExprExpect(b.Left, expected, true)
		rty := fc.checkExprExpect(b.Right, types.TypeInt, true)
		if lty == nil || rty == nil || lty == types.TypeInvalid || rty == types.TypeInvalid {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		lp := types.UnderlyingPrimitive(lty)
		rp := types.UnderlyingPrimitive(rty)
		if lp == nil || !lp.IsInteger() {
			fc.a.Rep.Errorf(b.Left.GetSpan(), "shift operand must be an integer, got %s", lty.String())
		}
		if rp == nil || !rp.IsInteger() {
			fc.a.Rep.Errorf(b.Right.GetSpan(), "shift count must be an integer, got %s", rty.String())
		}
		inf.Type = lty
		return lty
	}

	// Arithmetic & Bitwise (`+`, `-`, `*`, `/`, `%`, `&`, `|`, `^`)
	lty, rty := fc.checkOperandPairWithAdaptation(b.Left, b.Right, expected)
	if lty == nil || rty == nil || lty == types.TypeInvalid || rty == types.TypeInvalid {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if !lty.Equals(rty) {
		fc.a.Rep.Errorf(b.Span, "mismatched operand types %s and %s for `%s` (use explicit conversion like `%s(...)`)",
			lty.String(), rty.String(), b.Op.String(), lty.String())
		inf.Type = lty
		return lty
	}

	lp := types.UnderlyingPrimitive(lty)
	if b.Op == token.Plus && (lp == nil || !lp.IsNumeric()) {
		// `+` on anything but a number calls its `add` method (`Addable`).
		if fi := fc.a.operatorMethod(lty, "add", b.Span); fi != nil {
			inf.OpMethod = fi
			fc.checkFuncConstAndComptimeAccess(fi, b.Span)
		} else if !fc.a.typeParamHasBound(lty, fc.a.Prog.IfaceAddable) {
			fc.a.Rep.Errorf(b.Span, "operator `+` is not defined on %s (implement `Addable`: `add: fn(self, other: Self) Self`)", lty.String())
		}
		inf.Type = lty
		return lty
	}
	switch b.Op {
	case token.Plus, token.Minus, token.Star, token.Slash, token.Percent:
		if lp == nil || !lp.IsNumeric() {
			fc.a.Rep.Errorf(b.Span, "operator `%s` is not defined on %s", b.Op.String(), lty.String())
		}
	case token.Amp, token.Pipe, token.Caret:
		if lp == nil || !lp.IsInteger() {
			fc.a.Rep.Errorf(b.Span, "bitwise operator `%s` requires integer operands, got %s", b.Op.String(), lty.String())
		}
	}
	inf.Type = lty
	return lty
}

func isAdaptableLiteral(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.IntLit, *ast.FloatLit:
		return true
	case *ast.UnaryExpr:
		if v.Op == token.Minus {
			_, ok := v.Operand.(*ast.IntLit)
			if ok {
				return true
			}
			_, ok = v.Operand.(*ast.FloatLit)
			return ok
		}
	}
	return false
}

// checkOperandPairWithAdaptation allows `x + 1` where `x: u8` to adapt `1` to `u8` (and `1 + x` symmetrically)!
func (fc *funcChecker) checkOperandPairWithAdaptation(left, right ast.Expr, expected types.Type) (types.Type, types.Type) {
	lLit := isAdaptableLiteral(left)
	rLit := isAdaptableLiteral(right)
	if !lLit && rLit {
		lty := fc.checkExprExpect(left, expected, true)
		rty := fc.checkExprExpect(right, lty, true)
		return lty, rty
	}
	if lLit && !rLit {
		rty := fc.checkExprExpect(right, expected, true)
		lty := fc.checkExprExpect(left, rty, true)
		return lty, rty
	}
	lty := fc.checkExprExpect(left, expected, true)
	rty := fc.checkExprExpect(right, expected, true)
	return lty, rty
}

func typeExprToIndexExpr(ta ast.TypeExpr) (ast.Expr, bool) {
	if ca, ok := ta.(*ast.ConstArgType); ok && ca.Expr != nil {
		return ca.Expr, true
	}
	nt, ok := ta.(*ast.NamedType)
	if !ok || len(nt.TypeArgs) > 0 {
		return nil, false
	}
	if nt.Module == "" {
		return &ast.IdentExpr{Name: nt.Name, Span: nt.Span}, true
	}
	return &ast.MemberExpr{
		Base:   &ast.IdentExpr{Name: nt.Module, Span: nt.Span},
		Member: nt.Name,
		Span:   nt.Span,
	}, true
}

func (fc *funcChecker) maybeRewriteIndexedMemberBase(m *ast.MemberExpr) {
	// Parsed files are cached and may be checked concurrently; this rewrite is idempotent and
	// serialized so concurrent checks of the same file never race on the node.
	astRewriteMu.Lock()
	defer astRewriteMu.Unlock()
	if len(m.BaseTypeArgs) != 1 {
		return
	}
	isValueBase := false
	switch bv := m.Base.(type) {
	case *ast.IdentExpr:
		if fc.lookupVar(bv.Name) != nil {
			isValueBase = true
		}
	case *ast.MemberExpr:
		if id, ok := bv.Base.(*ast.IdentExpr); ok && !id.IsOpt {
			if fc.lookupVar(id.Name) != nil {
				isValueBase = true
			} else if targetMod, isMod := fc.mod.Aliased[id.Name]; isMod {
				if targetMod.Symbols[bv.Member] != nil {
					isValueBase = true
				}
			} else {
				isValueBase = true
			}
		} else {
			isValueBase = true
		}
	default:
		isValueBase = true
	}
	if !isValueBase {
		return
	}
	if idxExpr, ok := typeExprToIndexExpr(m.BaseTypeArgs[0]); ok {
		m.Base = &ast.IndexExpr{
			Base:  m.Base,
			Index: idxExpr,
			Span:  diag.Span{Start: m.Base.GetSpan().Start, End: m.BaseTypeArgs[0].GetSpan().End},
		}
		m.BaseTypeArgs = nil
	}
}

func (fc *funcChecker) tryResolveEnumMemberTarget(m *ast.MemberExpr, expected types.Type) (*types.Enum, *GenericEnumTemplate, bool) {
	if m.QuestDot || m.MemberHasOpt {
		return nil, nil, false
	}
	expTarget := expected
	if opt, ok := expTarget.(*types.Optional); ok {
		expTarget = opt.Elem
	}

	resolveFromMod := func(modAlias, typeName string) (*types.Enum, *GenericEnumTemplate, bool) {
		if genEn := fc.a.lookupGenEnumInModule(fc.mod, modAlias, typeName); genEn != nil {
			if len(m.BaseTypeArgs) > 0 {
				targs, ok := fc.a.resolveGenericArgs(fc.mod, genEn.Mod, genEn.Decl.TypeParams, m.BaseTypeArgs, fc.subst, m.Span, "generic enum", genEn.Decl.Name)
				if !ok {
					return nil, genEn, true
				}
				return fc.a.instantiateEnum(genEn, targs, m.Span), genEn, true
			}
			if expEn, ok := expTarget.(*types.Enum); ok && expEn.Name == genEn.Decl.Name && len(expEn.TypeArgs) == len(genEn.Decl.TypeParams) {
				return expEn, genEn, true
			}
			return nil, genEn, true
		}
		if ty := fc.a.lookupTypeInModule(fc.mod, modAlias, typeName); ty != nil {
			if en, isEn := ty.(*types.Enum); isEn {
				if len(m.BaseTypeArgs) > 0 {
					fc.a.Rep.Errorf(m.Span, "enum %s is not generic", en.Name)
				}
				return en, nil, true
			}
		}
		return nil, nil, false
	}

	if baseId, isId := m.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && fc.lookupVar(baseId.Name) == nil {
		if _, isMod := fc.mod.Aliased[baseId.Name]; !isMod {
			return resolveFromMod("", baseId.Name)
		}
	}
	if baseMem, isMem := m.Base.(*ast.MemberExpr); isMem && !baseMem.QuestDot && !baseMem.MemberHasOpt && len(baseMem.BaseTypeArgs) == 0 {
		if modId, isId := baseMem.Base.(*ast.IdentExpr); isId && !modId.IsOpt && fc.lookupVar(modId.Name) == nil {
			if modId.Name == "comp" && fc.mod.Aliased["comp"] == nil {
				if t := fc.a.Prog.CompTypes[baseMem.Member]; t != nil {
					if en, isEn := t.(*types.Enum); isEn {
						return en, nil, true
					}
				}
			}
			if _, isMod := fc.mod.Aliased[modId.Name]; isMod {
				return resolveFromMod(modId.Name, baseMem.Member)
			}
		}
	}
	return nil, nil, false
}

// IsCanonicalTupleIndex checks if s is a canonical non-negative integer ("0", "1", "2", ...)
// Disallowing leading zeros ("00"), underscores ("0_1"), negative signs ("-1"), and non-decimal characters.
func IsCanonicalTupleIndex(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	if s == "0" {
		return 0, true
	}
	if s[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	val, err := strconv.Atoi(s)
	if err != nil || val < 0 {
		return 0, false
	}
	return val, true
}

func (fc *funcChecker) checkModuleOptChain(base ast.Expr, member string, questDot bool, span diag.Span) bool {
	baseId, isId := base.(*ast.IdentExpr)
	if !isId || fc.lookupVar(baseId.Name) != nil {
		return false
	}
	modName := baseId.Name
	hasOpt := questDot
	if baseId.IsOpt {
		modName = strings.TrimSuffix(modName, "?")
		hasOpt = true
	}
	if !hasOpt {
		return false
	}
	if _, isMod := fc.mod.Aliased[modName]; isMod {
		fc.a.Rep.Errorf(span, "cannot use optional chaining `?.` on module %q (use `%s.%s`)", modName, modName, member)
		return true
	}
	return false
}

// checkMemberExpr resolves `Enum.Variant`, `Result[T, E].Empty`, `mod.Symbol`, `struct.field`, `ref[Struct].field`,
// and optional dot-chaining (`user?.name`, `self.profile?.score`, `acc?.nick.len()`, `find_user(id)?.name`).
func (fc *funcChecker) tryFoldComptimeProjection(expr, base ast.Expr, resTy types.Type, indexExpr ast.Expr) {
	if resTy == types.TypeInvalid || fc.probing > 0 || fc.inComptimeOnlyContext() {
		return
	}
	baseTy := fc.a.infoFor(base).Type
	baseRawCt := types.FindComptimeOnlyType(baseTy) != nil
	baseProj := fc.a.infoFor(base).ComptimeProjection
	if !baseRawCt && !baseProj {
		return
	}
	if !baseRawCt && baseProj && indexExpr != nil {
		// Base already crossed safely to runtime; only fold constant indices (e.g. `a.args[0]`),
		// leaving runtime-indexed `a.args[i]` to index the baked constant at runtime.
		switch iv := indexExpr.(type) {
		case *ast.IntLit:
		case *ast.IdentExpr:
			sym := fc.lookupVar(iv.Name)
			if sym == nil || (!sym.IsConst && !sym.IsConstParam) {
				return
			}
		default:
			return
		}
	}
	if fc.allowComptimeChain > 0 && types.FindComptimeOnlyType(resTy) != nil {
		return
	}
	if cv, ok := fc.a.evalComptimeExprInChecker(fc, expr, 0); ok && cv != nil {
		if msg := fc.a.validateCrossesToRuntime(cv, resTy); msg != "" {
			fc.a.Rep.ErrorWithHint(
				expr.GetSpan(),
				msg,
				"extract a `Plain`, `string`, or `[T]` value inside `comptime`",
			)
		} else if fc.a.curBM != nil {
			fc.a.curBM.ComptimeVals[expr] = cv
			fc.a.infoFor(expr).ComptimeProjection = true
		}
	} else if baseRawCt {
		fc.a.Rep.ErrorWithHint(
			expr.GetSpan(),
			fmt.Sprintf("cannot project `%s` out of `@comptime_only` expression in runtime code (expression is not constant)", resTy.String()),
			"move this expression inside a `comptime` block or `const` initializer with compile-time inputs",
		)
	}
}

func (fc *funcChecker) checkMemberExpr(m *ast.MemberExpr, expected types.Type) types.Type {
	res := fc.checkMemberExprInner(m, expected)
	inf := fc.a.infoFor(m)
	if inf.EnumVariant == nil && inf.Sym == nil {
		fc.tryFoldComptimeProjection(m, m.Base, res, nil)
	}
	return res
}

func (fc *funcChecker) checkMemberExprInner(m *ast.MemberExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(m)
	fc.maybeRewriteIndexedMemberBase(m)

	if fc.checkModuleOptChain(m.Base, m.Member, m.QuestDot, m.Span) {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	// 0. Check built-in compiler namespace values (`comp.TARGET`)!
	if baseId, isId := m.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && !m.QuestDot && len(m.BaseTypeArgs) == 0 && fc.lookupVar(baseId.Name) == nil {
		if _, isMod := fc.mod.Aliased[baseId.Name]; !isMod && baseId.Name == "comp" {
			if m.Member == "TARGET" && fc.a.Prog.CompTargetSym != nil {
				if !fc.inComptimeOnlyContext() && fc.allowComptimeChain == 0 {
					fc.a.Rep.ErrorWithHint(
						m.Span,
						"cannot use `@comptime_only` constant `comp.TARGET` outside a compile-time context",
						"use it inside `comptime`, `comptime if`/`comptime match`, a `const` initializer, or another `@comptime_only` function",
					)
				}
				inf.Sym = fc.a.Prog.CompTargetSym
				inf.Type = fc.a.Prog.CompTargetSym.Type
				return inf.Type
			}
			if _, isCompTy := fc.a.Prog.CompTypes[m.Member]; isCompTy {
				fc.a.Rep.Errorf(m.Span, "`comp.%s` is a type, not a value", m.Member)
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			fc.a.Rep.ErrorWithHint(
				m.Span,
				fmt.Sprintf("unknown `comp` member %q", m.Member),
				"available `comp` members: `comp.TARGET`, `comp.Target`, `comp.TargetOs`, `comp.TargetArch`, `comp.Endian`, `comp.TypeKind`, `comp.AttrInfo`, `comp.FieldInfo`, `comp.VariantInfo`, `comp.ElemInfo`, `comp.DistinctInfo`, `comp.type_name`, `comp.kind_of`, `comp.fields_of`, `comp.variants_of`, `comp.elems_of`, `comp.underlying_of`, `comp.attrs_of`, `comp.implements`, `comp.compile_error`, `comp.log`, `comp.embed_str`, `comp.embed_bytes`",
			)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	}

	// 1. Check imported module symbol (`mod.val`)!
	if baseId, isId := m.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && len(m.BaseTypeArgs) == 0 {
		if fc.lookupVar(baseId.Name) == nil {
			if targetMod, isMod := fc.mod.Aliased[baseId.Name]; isMod {
				if fc.mod.DevAliased[baseId.Name] && !fc.isTestContext() {
					fc.a.Rep.Errorf(m.Span, "cannot reference [dev-dependencies] package %q from non-test code", baseId.Name)
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
				if sym, ok := targetMod.Symbols[m.Member]; ok {
					if !IsExported(m.Member) && !fc.a.SamePackage(fc.mod, targetMod) {
						fc.a.Rep.Errorf(m.Span, "cannot access unexported symbol %q from package %q", m.Member, targetMod.Path)
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
					if sym.IsTopLevel && fc.a.inBodyPass {
						fc.a.resolveTopVar(sym)
					}
					if sym.FuncInst != nil && fc.a.inBodyPass {
						_ = fc.a.ensureFuncRetResolved(sym.FuncInst, m.Span)
					}
					if sym.Deprecated {
						msg := ""
						if sym.DeprecatedMsg != "" {
							msg = ": " + sym.DeprecatedMsg
						}
						fc.a.Rep.Warnf(m.Span, "use of deprecated identifier %q%s", sym.Name, msg)
					}
					if (sym.IsTest || sym.IsTestOnly || (sym.FuncInst != nil && sym.FuncInst == fc.a.Prog.TestMainFunc)) && !fc.isTestContext() {
						callerName := "<top-level>"
						if fc.curFn != nil {
							callerName = fc.curFn.Name
						}
						fc.a.Rep.Errorf(m.Span, "cannot access test symbol %q from non-test context %q", sym.Name, callerName)
					}
					if sym.ComptimeOnly && !fc.inComptimeOnlyContext() {
						fc.a.Rep.ErrorWithHint(
							m.Span,
							fmt.Sprintf("cannot use `@comptime_only` symbol %q in runtime code", sym.Name),
							"use it inside `comptime`, a `const` initializer, or another `@comptime_only` function",
						)
					}
					inf.Sym = sym
					inf.Type = sym.Type
					if sym.Type == nil {
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
					return sym.Type
				}
				// If it's not a value/function symbol, check if it's an enum/type before reporting unknown module export.
				if _, isType := targetMod.Types[m.Member]; !isType {
					if _, isGen := targetMod.GenericFuncs[m.Member]; !isGen {
						cands := closestNames(m.Member, fc.a.moduleExportNames(fc.mod, targetMod, false))
						hint := ""
						if len(cands) > 0 {
							hint = fmt.Sprintf("did you mean `%s.%s`?", baseId.Name, strings.Join(cands, "` or `"+baseId.Name+"."))
						}
						fc.a.errorWithRename(fc.a.focusSpan(m), fmt.Sprintf("module %q has no exported symbol %q", baseId.Name, m.Member), m.Member, hint, cands)
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
				}
			}
		}
	}

	// Check Enum unit variant (`Status.Pending`, `Result[int, string].Empty`, `Result.Empty`, `mod.Status.Pending`)!
	if en, genEn, matched := fc.tryResolveEnumMemberTarget(m, expected); matched {
		if en == nil {
			if genEn != nil && len(m.BaseTypeArgs) == 0 {
				fc.a.Rep.Errorf(m.Span, "cannot infer generic type arguments for `%s.%s`; specify `%s[...].%s` or annotate the expected type",
					genEn.Decl.Name, m.Member, genEn.Decl.Name, m.Member)
			}
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if en.Deprecated {
			msg := ""
			if en.DeprecatedMsg != "" {
				msg = ": " + en.DeprecatedMsg
			}
			fc.a.Rep.Warnf(m.Span, "use of deprecated enum %q%s", en.Name, msg)
		}
		v, ok := en.VariantByName(m.Member)
		if !ok {
			cands := closestNames(m.Member, fc.a.enumMemberNames(fc.mod, en, false))
			hint := ""
			if len(cands) > 0 {
				hint = fmt.Sprintf("did you mean `%s.%s`?", en.Name, strings.Join(cands, "` or `"+en.Name+"."))
			}
			fc.a.errorWithRename(fc.a.focusSpan(m), fmt.Sprintf("enum %s has no variant %q", en.String(), m.Member), m.Member, hint, cands)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if !IsExported(m.Member) && !fc.a.SamePackageDir(fc.mod, en.PkgDir, "") && !fc.a.SamePackage(fc.mod, fc.a.typeModule(en)) {
			fc.a.Rep.Errorf(m.Span, "cannot access unexported enum variant `%s.%s` outside its package", en.Name, m.Member)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if v.Kind == types.VariantTuple {
			fc.a.Rep.Errorf(m.Span, "variant `%s.%s` carries a payload of type %s; construct with `%s.%s(...)`",
				en.String(), v.Name, v.Payload.String(), en.Name, v.Name)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if en.IsComptimeOnly && !fc.inComptimeOnlyContext() {
			fc.a.Rep.ErrorWithHint(
				m.Span,
				fmt.Sprintf("cannot use `@comptime_only` enum `%s.%s` outside a compile-time context", en.Name, v.Name),
				"use it inside `comptime`, `comptime if`/`comptime match`, a `const` initializer, or another `@comptime_only` function",
			)
		}
		inf.EnumType = en
		inf.EnumVariant = v
		inf.Type = en
		return en
	}

	// 2. Evaluate base expression and handle optional chaining (Spec §7.2.3 & Decision B1 / 3A)!
	fc.allowComptimeChain++
	baseTy := fc.checkExpr(m.Base, true)
	fc.allowComptimeChain--
	fc.clearPendingOptChainErr(m.Base)
	if baseTy == types.TypeInvalid {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	isOptBase := false
	innerTy := baseTy
	if optB, ok := baseTy.(*types.Optional); ok {
		isOptBase = true
		innerTy = optB.Elem
		// Allow normal `.` optional chaining once an optional identifier, optional field (`self.profile?.score`),
		// `?.` operator, or earlier optional chain (`acc?.nick.len()`) has visually marked optionality!
		hasVisualQuestion := m.QuestDot || fc.a.infoFor(m.Base).IsOptChain
		switch bv := m.Base.(type) {
		case *ast.IdentExpr:
			if bv.IsOpt {
				hasVisualQuestion = true
			}
		case *ast.MemberExpr:
			if bv.MemberHasOpt {
				hasVisualQuestion = true
			}
		}
		if !hasVisualQuestion {
			fc.a.Rep.ErrorWithHint(
				m.Span,
				fmt.Sprintf("cannot access `.%s` on optional value of type %s without `?`", m.Member, baseTy.String()),
				fmt.Sprintf("use `?.%s` to short-circuit to `none`, or unwrap with `??` / `if (... as x)`", m.Member),
			)
		}
		inf.IsOptChain = true
		if optB.IsBoxed() {
			inf.DerefStructPtr = true
		}
	} else if m.QuestDot {
		fc.a.Rep.Report(diag.Diagnostic{
			Severity: diag.Error,
			Span:     m.Span,
			Full:     m.Span,
			Message:  fmt.Sprintf("`?.` cannot be used on non-optional type %s (use `.`)", baseTy.String()),
			Hint:     "remove `?` and use `.`",
			Data:     map[string]string{"kind": "drop_quest_dot"},
		})
	}

	// Unwrap `ref[Struct]` transparently for field access (`node_ref.val`)!
	if refTy, isRef := innerTy.(*types.Ref); isRef {
		innerTy = refTy.Elem
		inf.DerefStructPtr = true
	}

	if vec, isVec := innerTy.(*types.Vector); isVec {
		if m.Member == "len" || m.Member == "cap" {
			inf.Type = types.TypeInt
			return types.TypeInt
		}
		if m.Member == "data" {
			ptrTy := &types.Ptr{Elem: vec.Elem}
			inf.Type = ptrTy
			return ptrTy
		}
	}
	if arr, isArr := innerTy.(*types.StackArray); isArr {
		if m.Member == "len" {
			inf.Type = types.TypeInt
			return types.TypeInt
		}
		if m.Member == "data" {
			ptrTy := &types.Ptr{Elem: arr.Elem}
			inf.Type = ptrTy
			return ptrTy
		}
	}
	var tup *types.Tuple
	typeName := innerTy.String()
	if t, isTup := innerTy.(*types.Tuple); isTup {
		tup = t
	} else if d, isDist := innerTy.(*types.Distinct); isDist {
		if dtTup, isTup := d.Base.(*types.Tuple); isTup {
			tup = dtTup
			typeName = d.Name
		}
	}
	if tup != nil {
		idx, ok := IsCanonicalTupleIndex(m.Member)
		if !ok {
			fc.a.Rep.Errorf(m.Span, "invalid tuple element index %q: must be a non-negative canonical integer", m.Member)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if idx >= len(tup.Elems) {
			fc.a.Rep.Errorf(m.Span, "tuple %s has no element %d (index out of bounds)", typeName, idx)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		inf.TupleFieldIdx = idx
		resTy := tup.Elems[idx]
		if isOptBase {
			if !resTy.IsPlain() && !fc.a.supportsCopy(resTy) {
				span, elemIdx, typeStr := m.Span, idx, resTy.String()
				fc.recordPendingOptChainErr(m, func() {
					fc.a.Rep.Errorf(span, "cannot clone Resource tuple element %d (%s) through optional `?.%d`: type does not implement `Copyable`", elemIdx, typeStr, elemIdx)
				})
			}
			resTy = types.NewOptional(resTy)
		}
		inf.Type = resTy
		return resTy
	}

	st, isSt := innerTy.(*types.Struct)
	if !isSt {
		fc.a.Rep.Errorf(m.Span, "type %s has no field %q", innerTy.String(), m.Member)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	// Resolve field on `st`:
	// Remember our Edge Case 1 insight for `u?.address?.city`!
	// If `m.Member` is `"address?"` (because `.` followed `address?`), first check field `"address?"`;
	// if not found and `isOptBase` is true, check non-optional field `"address"`!
	f, _ := st.FieldByName(m.Member)
	if f == nil && m.MemberHasOpt && isOptBase {
		plainName := strings.TrimSuffix(m.Member, "?")
		if f2, _ := st.FieldByName(plainName); f2 != nil {
			f = f2
			// Keep `m.MemberHasOpt = true` so the NEXT `.city` in `u?.address?.city` sees the `?` marker,
			// while recording the resolved non-optional struct field name!
			m.Member = plainName
		}
	}
	if f == nil {
		hint, cands := fc.a.unknownFieldHint(st, m.Member)
		fc.a.errorWithRename(fc.a.focusSpan(m), fmt.Sprintf("struct %s has no field %q", st.String(), m.Member), m.Member, hint, cands)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if !IsExported(f.Name) && !fc.a.SamePackageDir(fc.mod, st.PkgDir, "") && !fc.a.SamePackage(fc.mod, fc.a.typeModule(st)) {
		fc.a.Rep.Errorf(m.Span, "cannot access unexported field %q of struct %s outside its package", f.Name, st.String())
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	resTy := f.Type
	if isOptBase {
		if !f.Type.IsPlain() && !fc.a.supportsCopy(f.Type) {
			span, fname, typeStr := m.Span, f.Name, f.Type.String()
			fc.recordPendingOptChainErr(m, func() {
				fc.a.Rep.Errorf(span, "cannot clone Resource field %q (%s) through optional `?.%s`: type does not implement `Copyable`", fname, typeStr, fname)
			})
		}
		resTy = types.NewOptional(resTy) // flattens `?(?T) -> ?T` automatically!
	}
	inf.Type = resTy
	return resTy
}

func (fc *funcChecker) checkIndexExpr(idx *ast.IndexExpr) types.Type {
	res := fc.checkIndexExprInner(idx)
	fc.tryFoldComptimeProjection(idx, idx.Base, res, idx.Index)
	return res
}

func (fc *funcChecker) checkIndexExprInner(idx *ast.IndexExpr) types.Type {
	inf := fc.a.infoFor(idx)
	fc.allowComptimeChain++
	baseTy := fc.checkExpr(idx.Base, true)
	fc.allowComptimeChain--
	if baseTy == types.TypeInvalid {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	// Deref `ref[Vec]` if indexing through arena ref
	if r, ok := baseTy.(*types.Ref); ok {
		baseTy = r.Elem
	}

	switch v := types.CollView(baseTy).(type) {
	case *types.Vector:
		kTy := fc.checkExprExpect(idx.Index, types.TypeInt, true)
		fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "list index")
		inf.Type = v.Elem
		return v.Elem

	case *types.StackArray:
		kTy := fc.checkExprExpect(idx.Index, types.TypeInt, true)
		fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "array index")
		inf.Type = v.Elem
		return v.Elem

	case *types.Tuple:
		fc.reportTupleBracketIndex(idx, v)
		return types.TypeInvalid

	case *types.Distinct:
		if dtTup, isTup := v.Base.(*types.Tuple); isTup {
			fc.reportTupleBracketIndex(idx, dtTup)
			return types.TypeInvalid
		}

	case *types.Primitive:
		if v.Kind == types.PrimString {
			kTy := fc.checkExprExpect(idx.Index, types.TypeInt, true)
			fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "string byte index")
			inf.Type = types.TypeU8
			return types.TypeU8
		}

	case *types.Map:
		kTy := fc.checkExprExpect(idx.Index, v.Key, true)
		fc.verifyAssignable(kTy, v.Key, idx.Index, idx.Index.GetSpan(), "map lookup key")
		if !fc.a.supportsCopy(v.Value) {
			fc.a.Rep.Errorf(idx.Span, "map value type %s does not support `.copy()` for `m[k]` lookup", v.Value.String())
		} else {
			fc.a.requireCollMethod(baseTy, "get", idx.Span)
		}
		res := types.NewOptional(v.Value)
		inf.Type = res
		return res

	case *types.Ptr:
		if fc.unsafeDepth == 0 {
			fc.a.Rep.Errorf(idx.Span, "raw pointer indexing `p[i]` requires `unsafe`")
		}
		kTy := fc.checkExprExpect(idx.Index, types.TypeInt, true)
		fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "pointer index")
		inf.Type = v.Elem
		return v.Elem

	case *types.Struct:
		if (v.Name == "List" || strings.HasPrefix(v.Name, "List[")) && len(v.TypeArgs) == 1 {
			kTy := fc.checkExprExpect(idx.Index, types.TypeInt, true)
			fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "list index")
			inf.Type = v.TypeArgs[0]
			return v.TypeArgs[0]
		}
		if (v.Name == "Array" || strings.HasPrefix(v.Name, "Array[")) && len(v.TypeArgs) >= 1 {
			kTy := fc.checkExprExpect(idx.Index, types.TypeInt, true)
			fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "array index")
			elem := v.TypeArgs[len(v.TypeArgs)-1]
			inf.Type = elem
			return elem
		}

	case *types.Interface:
		if fc.a.isIndexableType(v) {
			kTy := fc.checkExprExpect(idx.Index, types.TypeInt, true)
			fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "indexable index")
			itemTy := fc.a.indexableItemType(v)
			res := types.NewOptional(itemTy)
			inf.Type = res
			return res
		}
		if fc.a.isKeyIndexableType(v) {
			keyTy, valTy := fc.a.keyIndexableTypes(v)
			kTy := fc.checkExprExpect(idx.Index, keyTy, true)
			fc.verifyAssignable(kTy, keyTy, idx.Index, idx.Index.GetSpan(), "key_indexable key")
			res := types.NewOptional(valTy)
			inf.Type = res
			return res
		}

	case *types.TypeParamVar:
		for _, b := range v.Bounds {
			if fc.a.isIndexableType(b) {
				kTy := fc.checkExprExpect(idx.Index, types.TypeInt, true)
				fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "indexable index")
				itemTy := fc.a.indexableItemType(b)
				res := types.NewOptional(itemTy)
				inf.Type = res
				return res
			}
			if fc.a.isKeyIndexableType(b) {
				keyTy, valTy := fc.a.keyIndexableTypes(b)
				kTy := fc.checkExprExpect(idx.Index, keyTy, true)
				fc.verifyAssignable(kTy, keyTy, idx.Index, idx.Index.GetSpan(), "key_indexable key")
				res := types.NewOptional(valTy)
				inf.Type = res
				return res
			}
		}
	}

	if fi := fc.a.LookupMethod(baseTy, "get", nil, idx.Span); fi != nil && len(fi.Sig.Params) == 2 {
		if fi.Sig.Params[0].Mode == types.ModeMut {
			_ = fc.checkMutablePlace(idx.Base, "mutable index receiver")
		}
		kTy := fc.checkExprExpect(idx.Index, fi.Sig.Params[1].Type, true)
		fc.verifyAssignable(kTy, fi.Sig.Params[1].Type, idx.Index, idx.Index.GetSpan(), "index")
		inf.TargetFunc = fi
		fc.checkFuncConstAndComptimeAccess(fi, idx.Span)
		retTy := fc.a.ensureFuncRetResolved(fi, idx.Span)
		inf.Type = retTy
		return retTy
	}

	fc.a.Rep.Errorf(idx.Span, "cannot index value of type %s", baseTy.String())
	inf.Type = types.TypeInvalid
	return types.TypeInvalid
}

func (fc *funcChecker) checkSliceExpr(sl *ast.SliceExpr) types.Type {
	inf := fc.a.infoFor(sl)
	baseTy := fc.checkExpr(sl.Base, true)
	if sl.Low != nil {
		loTy := fc.checkExprExpect(sl.Low, types.TypeInt, true)
		fc.verifyAssignable(loTy, types.TypeInt, sl.Low, sl.Low.GetSpan(), "slice start")
	}
	if sl.High != nil {
		hiTy := fc.checkExprExpect(sl.High, types.TypeInt, true)
		fc.verifyAssignable(hiTy, types.TypeInt, sl.High, sl.High.GetSpan(), "slice end")
	}

	switch v := baseTy.(type) {
	case *types.Primitive:
		if v.Kind == types.PrimString {
			inf.Type = types.TypeString
			return types.TypeString
		}
	case *types.Vector:
		inf.Type = v
		return v
	case *types.StackArray:
		vt := &types.Vector{Elem: v.Elem}
		inf.Type = vt
		return vt
	}
	if baseTy != types.TypeInvalid {
		fc.a.Rep.Errorf(sl.Span, "cannot slice value of type %s", baseTy.String())
	}
	inf.Type = types.TypeInvalid
	return types.TypeInvalid
}

func (fc *funcChecker) checkRangeExpr(re *ast.RangeExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(re)
	if fc.a.Prog.GenStructRange == nil {
		fc.a.Rep.Errorf(re.Span, "`Range[T]` is not defined in the core prelude")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	var hintTy types.Type = types.TypeInt
	if expected != nil {
		target := expected
		if opt, isOpt := target.(*types.Optional); isOpt {
			target = opt.Elem
		}
		if st, isSt := target.(*types.Struct); isSt && fc.a.isStdStructTemplate(st, fc.a.Prog.GenStructRange) && len(st.TypeArgs) == 1 {
			hintTy = st.TypeArgs[0]
		}
	}

	loTy, hiTy := fc.checkOperandPairWithAdaptation(re.Low, re.High, hintTy)
	if loTy == types.TypeInvalid || hiTy == types.TypeInvalid {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if !loTy.Equals(hiTy) {
		fc.a.Rep.Errorf(re.Span, "range bounds in `a..b` must have matching types, got %s and %s", loTy.String(), hiTy.String())
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	prim := types.UnderlyingPrimitive(loTy)
	if prim == nil || (!prim.IsInteger() && prim.Kind != types.PrimChar) {
		fc.a.Rep.Errorf(re.Span, "range bounds require an integer or `char` type, got %s", loTy.String())
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	fc.consumeValue(re.Low, loTy, false)
	fc.consumeValue(re.High, hiTy, false)

	rangeTy := fc.a.instantiateStruct(fc.a.Prog.GenStructRange, []types.Type{loTy}, re.Span)
	if rangeTy == nil {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	inf.Type = rangeTy
	return rangeTy
}

// -----------------------------------------------------------------------------
// Collection, Struct, & Lambda Literals
// -----------------------------------------------------------------------------

func (fc *funcChecker) checkVecOrArrayLit(v *ast.VecLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(v)
	target := expected
	if opt, ok := target.(*types.Optional); ok {
		target = opt.Elem
	}

	if st, isSt := target.(*types.Struct); isSt {
		if (st.Name == "Array" || strings.HasPrefix(st.Name, "Array[")) && len(st.TypeArgs) >= 1 {
			target = &types.StackArray{Size: int64(len(v.Elems)), Elem: st.TypeArgs[len(st.TypeArgs)-1]}
		}
	}

	// Explicit-element stack array: `let s: [3: int] = [1, 2, 3]` (Spec §7.1 / §8.1)!
	if arrTy, isArr := target.(*types.StackArray); isArr {
		if int64(len(v.Elems)) != arrTy.Size {
			fc.a.Rep.Errorf(v.Span, "stack array literal expects %d element(s) for %s, got %d",
				arrTy.Size, arrTy.String(), len(v.Elems))
		}
		for _, el := range v.Elems {
			et := fc.checkExprExpect(el, arrTy.Elem, true)
			fc.verifyAssignable(et, arrTy.Elem, el, el.GetSpan(), "stack array element")
			fc.consumeValue(el, arrTy.Elem, false)
		}
		inf.Type = arrTy
		return arrTy
	}

	var elemHint types.Type
	if vecTy, isVec := target.(*types.Vector); isVec {
		elemHint = vecTy.Elem
	} else if st, isSt := target.(*types.Struct); isSt && (st.Name == "List" || strings.HasPrefix(st.Name, "List[")) && len(st.TypeArgs) == 1 {
		elemHint = st.TypeArgs[0]
	}

	if len(v.Elems) == 0 {
		if elemHint == nil {
			fc.a.Rep.ErrorWithHint(
				v.Span,
				"cannot infer element type for empty list literal `[]`",
				"add an explicit type annotation (e.g. `let v: [int] = []`)",
			)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		res := &types.Vector{Elem: elemHint}
		inf.Type = res
		return res
	}

	var elemTy types.Type
	if elemHint != nil {
		elemTy = elemHint
	} else {
		elemTy = fc.checkExpr(v.Elems[0], true)
		fc.consumeValue(v.Elems[0], elemTy, false)
	}

	for idx, el := range v.Elems {
		if idx == 0 && elemHint == nil {
			continue
		}
		et := fc.checkExprExpect(el, elemTy, true)
		fc.verifyAssignable(et, elemTy, el, el.GetSpan(), "list element")
		fc.consumeValue(el, elemTy, false)
	}
	res := &types.Vector{Elem: elemTy}
	inf.Type = res
	return res
}

func (fc *funcChecker) checkArrayLit(r *ast.ArrayLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(r)
	sz, ok := fc.a.evalConstIntWithSubst(fc.mod, r.Count, fc.subst)
	if !ok || sz <= 0 {
		fc.a.Rep.Errorf(r.Count.GetSpan(), "size/repeat count in `[N: ...]` must be a positive compile-time integer constant")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	var elemHint types.Type
	if arr, isArr := expected.(*types.StackArray); isArr {
		elemHint = arr.Elem
	} else if st, isSt := expected.(*types.Struct); isSt && (st.Name == "Array" || strings.HasPrefix(st.Name, "Array[")) && len(st.TypeArgs) >= 1 {
		elemHint = st.TypeArgs[len(st.TypeArgs)-1]
	}

	// Repeat constructor: `[N: value]` where N > 1 and len(Elems) == 1
	if len(r.Elems) == 1 && sz > 1 {
		valTy := fc.checkExprExpect(r.Elems[0], elemHint, true)
		if !fc.a.supportsCopy(valTy) {
			fc.a.Rep.Errorf(r.Elems[0].GetSpan(), "repeated element in `[%d: value]` must support `.copy()`", sz)
		} else {
			fc.consumeValue(r.Elems[0], valTy, false)
		}
		res := &types.StackArray{Size: sz, Elem: valTy}
		inf.Type = res
		return res
	}

	// Single element array: `[1: value]`
	if len(r.Elems) == 1 && sz == 1 {
		valTy := fc.checkExprExpect(r.Elems[0], elemHint, true)
		fc.consumeValue(r.Elems[0], valTy, false)
		res := &types.StackArray{Size: 1, Elem: valTy}
		inf.Type = res
		return res
	}

	// Multiple elements provided: `[N: e0, e1, ..., eM]`
	if int64(len(r.Elems)) != sz {
		fc.a.Rep.Errorf(r.Span, "fixed array literal expects %d element(s), got %d", sz, len(r.Elems))
	}
	var elemTy types.Type
	if elemHint != nil {
		elemTy = elemHint
	} else {
		elemTy = fc.checkExpr(r.Elems[0], true)
		fc.consumeValue(r.Elems[0], elemTy, false)
	}
	for i, el := range r.Elems {
		if i == 0 && elemHint == nil {
			continue
		}
		et := fc.checkExprExpect(el, elemTy, true)
		fc.verifyAssignable(et, elemTy, el, el.GetSpan(), "stack array element")
		fc.consumeValue(el, elemTy, false)
	}
	res := &types.StackArray{Size: sz, Elem: elemTy}
	inf.Type = res
	return res
}

func (fc *funcChecker) checkSetLit(s *ast.SetLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(s)
	var elemHint types.Type
	if el, ok := types.SetElem(expected); ok {
		elemHint = el
	}
	var elemTy types.Type
	for i, el := range s.Elems {
		if i == 0 && elemHint == nil {
			elemTy = fc.checkExpr(el, true)
			fc.consumeValue(el, elemTy, false)
			continue
		}
		if i == 0 {
			elemTy = elemHint
		}
		et := fc.checkExprExpect(el, elemTy, true)
		fc.verifyAssignable(et, elemTy, el, el.GetSpan(), "set element")
		fc.consumeValue(el, elemTy, false)
	}
	if elemTy != nil && elemTy != types.TypeInvalid {
		isH := (fc.a.Prog.IfaceHashable != nil && fc.a.ImplementsInterface(elemTy, fc.a.Prog.IfaceHashable)) || types.IsHashable(elemTy)
		if !isH {
			fc.a.Rep.Errorf(s.Span, "set element type %s is not hashable", elemTy.String())
		}
	}
	if elemTy == nil || elemTy == types.TypeInvalid {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	res := fc.a.setType(elemTy, s.Span)
	if res != types.TypeInvalid {
		fc.a.requireCollMethod(res, "with_capacity", s.Span)
		fc.a.requireCollMethod(res, "insert", s.Span)
	}
	inf.Type = res
	return res
}

func (fc *funcChecker) checkMapLit(m *ast.MapLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(m)
	var kHint, vHint types.Type
	if k, v, ok := types.MapKV(expected); ok {
		kHint = k
		vHint = v
	}
	var kTy, vTy types.Type
	for i, ent := range m.Entries {
		if i == 0 && kHint == nil {
			kTy = fc.checkExpr(ent.Key, true)
			vTy = fc.checkExpr(ent.Value, true)
			fc.consumeValue(ent.Key, kTy, false)
			fc.consumeValue(ent.Value, vTy, false)
			continue
		}
		if i == 0 {
			kTy = kHint
			vTy = vHint
		}
		kt := fc.checkExprExpect(ent.Key, kTy, true)
		vt := fc.checkExprExpect(ent.Value, vTy, true)
		fc.verifyAssignable(kt, kTy, ent.Key, ent.Key.GetSpan(), "map key")
		fc.verifyAssignable(vt, vTy, ent.Value, ent.Value.GetSpan(), "map value")
		fc.consumeValue(ent.Key, kTy, false)
		fc.consumeValue(ent.Value, vTy, false)
	}
	if kTy != nil && kTy != types.TypeInvalid {
		isH := (fc.a.Prog.IfaceHashable != nil && fc.a.ImplementsInterface(kTy, fc.a.Prog.IfaceHashable)) || types.IsHashable(kTy)
		if !isH {
			fc.a.Rep.Errorf(m.Span, "map key type %s is not hashable", kTy.String())
		}
	}
	if kTy == nil || kTy == types.TypeInvalid || vTy == nil || vTy == types.TypeInvalid {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	res := fc.a.mapType(kTy, vTy, m.Span)
	if res != types.TypeInvalid {
		fc.a.requireCollMethod(res, "with_capacity", m.Span)
		fc.a.requireCollMethod(res, "insert", m.Span)
	}
	inf.Type = res
	return res
}

func (fc *funcChecker) checkEmptyHashLit(e *ast.EmptyHashLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(e)
	target := expected
	if opt, ok := target.(*types.Optional); ok {
		target = opt.Elem
	}
	switch v := types.CollView(target).(type) {
	case *types.Set, *types.Map:
		_ = v
		inf.Type = target
		return target
	default:
		fc.a.Rep.ErrorWithHint(
			e.Span,
			"cannot infer Set vs. Map type for empty `#[]` literal without type context",
			"add an explicit type annotation (e.g. `let s: #[string] = #[]` or `let m: #[string: int] = #[]`)",
		)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
}

func (fc *funcChecker) checkTupleLit(v *ast.TupleLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(v)
	expTarget := expected
	if opt, ok := expTarget.(*types.Optional); ok {
		expTarget = opt.Elem
	}
	var distTarget *types.Distinct
	if dist, isDist := expTarget.(*types.Distinct); isDist {
		if dtTup, isTup := dist.Base.(*types.Tuple); isTup {
			distTarget = dist
			expTarget = dtTup
		}
	}
	expTup, _ := expTarget.(*types.Tuple)
	var elemTypes []types.Type
	for i, el := range v.Elems {
		var expElem types.Type
		if expTup != nil && i < len(expTup.Elems) {
			expElem = expTup.Elems[i]
		}
		t := fc.checkExprExpect(el, expElem, true)
		if t == types.TypeInvalid {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		targetElem := t
		if expElem != nil {
			fc.verifyAssignable(t, expElem, el, el.GetSpan(), fmt.Sprintf("tuple element %d", i))
			targetElem = expElem
		}
		fc.consumeValue(el, targetElem, false)
		elemTypes = append(elemTypes, targetElem)
	}
	tupTy := &types.Tuple{Elems: elemTypes}
	resTy := types.Type(tupTy)
	if distTarget != nil && tupTy.Equals(distTarget.Base) {
		resTy = distTarget
	}
	inf.Type = resTy
	partialExpected := false
	if expTup != nil {
		for _, el := range expTup.Elems {
			if el == nil {
				partialExpected = true
				break
			}
		}
	}
	if !partialExpected {
		if expTarget != nil && !tupTy.Equals(expTarget) {
			fc.verifyAssignable(tupTy, expTarget, v, v.Span, "tuple literal")
		}
		if expected != nil && !resTy.Equals(expected) {
			fc.verifyAssignable(resTy, expected, v, v.Span, "tuple literal")
		}
	}
	return resTy
}

func (fc *funcChecker) checkStructLit(sl *ast.StructLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(sl)
	expTarget := expected
	if opt, ok := expTarget.(*types.Optional); ok {
		expTarget = opt.Elem
	}

	var st *types.Struct

	if genSt := fc.a.lookupGenStructInModule(fc.mod, sl.Module, sl.TypeName); genSt != nil {
		if len(sl.TypeArgs) > 0 {
			targs, ok := fc.a.resolveGenericArgs(fc.mod, genSt.Mod, genSt.Decl.TypeParams, sl.TypeArgs, fc.subst, sl.Span, "generic struct", sl.TypeName)
			if !ok {
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			st = fc.a.instantiateStruct(genSt, targs, sl.Span)
		} else if expSt, ok := expTarget.(*types.Struct); ok && expSt.Name == sl.TypeName && len(expSt.TypeArgs) == len(genSt.Decl.TypeParams) {
			st = expSt
		} else {
			// Infer type arguments from field expressions (`Pair[first: 1, second: "hi"]`)!
			st = fc.inferGenericStructLit(genSt, sl)
		}
		if st == nil {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	} else {
		if len(sl.TypeArgs) > 0 {
			fc.a.Rep.Errorf(sl.Span, "struct %s is not generic", sl.TypeName)
		}
		ty := fc.a.lookupTypeInModule(fc.mod, sl.Module, sl.TypeName)
		var ok bool
		st, ok = ty.(*types.Struct)
		if !ok {
			fc.a.Rep.Errorf(sl.Span, "%q is not a struct type", sl.TypeName)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	}

	if sl.Module != "" && fc.mod.DevAliased[sl.Module] && !fc.isTestContext() {
		fc.a.Rep.Errorf(sl.Span, "cannot reference [dev-dependencies] package %q from non-test code", sl.Module)
	}
	if st.IsTestOnly && !fc.isTestContext() {
		callerName := "<top-level>"
		if fc.curFn != nil {
			callerName = fc.curFn.Name
		}
		fc.a.Rep.Errorf(sl.Span, "cannot use test-only struct %q in non-test context %q", st.Name, callerName)
	}
	if st.IsComptimeOnly && !fc.inComptimeOnlyContext() {
		fc.a.Rep.ErrorWithHint(
			sl.Span,
			fmt.Sprintf("cannot construct `@comptime_only` struct %q in runtime context", st.Name),
			"construct it inside a `comptime` block/expression, a `const` initializer, or a `@comptime_only` function",
		)
	}

	samePkg := fc.a.SamePackageDir(fc.mod, st.PkgDir, "") || fc.a.SamePackage(fc.mod, fc.a.typeModule(st))
	if !samePkg {
		for _, sf := range st.Fields {
			if !IsExported(sf.Name) && !sf.HasDefault {
				if _, isOpt := sf.Type.(*types.Optional); !isOpt {
					fc.a.Rep.Errorf(sl.Span, "cannot construct struct %s outside its package: unexported field %q has no default value (use a constructor function)", st.String(), sf.Name)
					break
				}
			}
		}
	}

	provided := make(map[string]bool)
	for _, fi := range sl.Fields {
		if !samePkg && !IsExported(fi.Name) {
			fc.a.Rep.Errorf(fi.Span, "cannot initialize unexported field %q of struct %s outside its package", fi.Name, st.String())
		}
		if provided[fi.Name] {
			fc.a.Rep.Errorf(fi.Span, "duplicate field %q in %s literal", fi.Name, st.String())
			continue
		}
		provided[fi.Name] = true
		sf, _ := st.FieldByName(fi.Name)
		if sf == nil {
			hint, cands := fc.a.unknownFieldHint(st, fi.Name)
			fc.a.errorWithRename(fi.Span, fmt.Sprintf("struct %s has no field %q", st.String(), fi.Name), fi.Name, hint, cands)
			_ = fc.checkExpr(fi.Value, true)
			continue
		}
		got := fc.checkExprExpect(fi.Value, sf.Type, true)
		fc.verifyAssignable(got, sf.Type, fi.Value, fi.Value.GetSpan(), fmt.Sprintf("field %q of %s", fi.Name, st.String()))
		fc.consumeValue(fi.Value, sf.Type, false)
	}

	// Verify all required fields (those without default values and not `?T`) were provided!
	for _, sf := range st.Fields {
		if provided[sf.Name] || sf.HasDefault {
			continue
		}
		if _, isOpt := sf.Type.(*types.Optional); isOpt {
			continue // Optional fields default to `none` when omitted (Decision B6)!
		}
		fc.a.Rep.Errorf(sl.Span, "missing required field %q (type %s) in %s literal", sf.Name, sf.Type.String(), st.String())
	}

	inf.StructType = st
	inf.Type = st
	return st
}

func (fc *funcChecker) inferGenericStructLit(genSt *GenericStructTemplate, sl *ast.StructLit) *types.Struct {
	fieldTypes := make(map[string]types.Type)
	for _, fi := range sl.Fields {
		if _, isNone := fi.Value.(*ast.NoneLit); isNone {
			continue
		}
		savedMoves := fc.cloneMoves()
		fieldTypes[fi.Name] = fc.checkExpr(fi.Value, true)
		fc.moves = savedMoves
	}
	inferred := make(map[string]types.Type)
	tpNames := make(map[string]bool)
	for _, tp := range genSt.Decl.TypeParams {
		tpNames[tp.Name] = true
	}
	for _, df := range genSt.Decl.Fields {
		if got, ok := fieldTypes[df.Name]; ok && got != types.TypeInvalid {
			matchTypeExprForInference(df.Type, got, tpNames, inferred)
		}
	}
	targs := make([]types.Type, len(genSt.Decl.TypeParams))
	for i, tp := range genSt.Decl.TypeParams {
		ta, ok := inferred[tp.Name]
		if !ok {
			fc.a.Rep.Errorf(sl.Span, "cannot infer generic type parameter %s for %s; specify `%s[...]` explicitly",
				tp.Name, genSt.Decl.Name, genSt.Decl.Name)
			return nil
		}
		validTa, ok2 := validateInferredConstArg(fc.a, genSt.Mod, genSt.Decl.TypeParams, tp, ta, sl.Span)
		if !ok2 {
			return nil
		}
		targs[i] = validTa
	}
	return fc.a.instantiateStruct(genSt, targs, sl.Span)
}

func validateInferredConstArg(a *Analyzer, declMod *Module, params []ast.TypeParam, tp ast.TypeParam, inferred types.Type, span diag.Span) (types.Type, bool) {
	if !tp.IsConst {
		if _, isCV := inferred.(*types.ConstValue); isCV {
			a.Rep.Errorf(span, "type parameter %q cannot be inferred as a constant value", tp.Name)
			return nil, false
		}
		return inferred, true
	}
	cv, isCV := inferred.(*types.ConstValue)
	if !isCV || cv == nil {
		a.Rep.Errorf(span, "const generic parameter %q cannot be inferred as a type", tp.Name)
		return nil, false
	}
	otherParams := make(map[string]bool, len(params))
	for _, p := range params {
		if !p.IsConst {
			otherParams[p.Name] = true
		}
	}
	expectedTy := a.validateConstGenericParam(declMod, otherParams, tp)
	if expectedTy == nil || expectedTy == types.TypeInvalid {
		return nil, false
	}
	if !cv.ConstType.Equals(expectedTy) {
		a.Rep.Errorf(span, "inferred const parameter %q has type %s, expected %s", tp.Name, cv.ConstType.String(), expectedTy.String())
		return nil, false
	}
	return cv, true
}

func matchTypeExprForInference(pat ast.TypeExpr, concrete types.Type, tpNames map[string]bool, out map[string]types.Type) {
	if concrete == nil || concrete == types.TypeInvalid {
		return
	}
	switch p := pat.(type) {
	case *ast.ConstArgType:
		if id, ok := p.Expr.(*ast.IdentExpr); ok && tpNames[id.Name] {
			if _, exists := out[id.Name]; !exists {
				out[id.Name] = concrete
			}
		}
		return
	case *ast.NamedType:
		if p.Module == "" && len(p.TypeArgs) == 0 && tpNames[p.Name] {
			if _, exists := out[p.Name]; !exists {
				out[p.Name] = concrete
			}
			return
		}
		if p.Name == "ptr" && len(p.TypeArgs) == 1 {
			if pt, ok := concrete.(*types.Ptr); ok {
				matchTypeExprForInference(p.TypeArgs[0], pt.Elem, tpNames, out)
			}
		}
		if p.Name == "ref" && len(p.TypeArgs) == 1 {
			if rf, ok := concrete.(*types.Ref); ok {
				matchTypeExprForInference(p.TypeArgs[0], rf.Elem, tpNames, out)
			}
		}
		if p.Name == "Indexable" && len(p.TypeArgs) == 1 {
			if vec, ok := concrete.(*types.Vector); ok {
				matchTypeExprForInference(p.TypeArgs[0], vec.Elem, tpNames, out)
			} else if arr, ok := concrete.(*types.StackArray); ok {
				matchTypeExprForInference(p.TypeArgs[0], arr.Elem, tpNames, out)
			} else if prim, ok := concrete.(*types.Primitive); ok && prim.Kind == types.PrimString {
				matchTypeExprForInference(p.TypeArgs[0], types.TypeU8, tpNames, out)
			} else if st, ok := concrete.(*types.Struct); ok && len(st.TypeArgs) >= 1 {
				matchTypeExprForInference(p.TypeArgs[0], st.TypeArgs[len(st.TypeArgs)-1], tpNames, out)
			}
		}
		if p.Name == "KeyIndexable" && len(p.TypeArgs) == 2 {
			if mp, ok := types.CollView(concrete).(*types.Map); ok {
				matchTypeExprForInference(p.TypeArgs[0], mp.Key, tpNames, out)
				matchTypeExprForInference(p.TypeArgs[1], mp.Value, tpNames, out)
			} else if st, ok := concrete.(*types.Struct); ok && st.Name == "Map" && len(st.TypeArgs) == 2 {
				matchTypeExprForInference(p.TypeArgs[0], st.TypeArgs[0], tpNames, out)
				matchTypeExprForInference(p.TypeArgs[1], st.TypeArgs[1], tpNames, out)
			}
		}
		if p.Name == "List" && len(p.TypeArgs) == 1 {
			if vec, ok := concrete.(*types.Vector); ok {
				matchTypeExprForInference(p.TypeArgs[0], vec.Elem, tpNames, out)
			} else if arr, ok := concrete.(*types.StackArray); ok {
				matchTypeExprForInference(p.TypeArgs[0], arr.Elem, tpNames, out)
			} else if st, ok := concrete.(*types.Struct); ok && st.Name == "Array" && len(st.TypeArgs) == 1 {
				matchTypeExprForInference(p.TypeArgs[0], st.TypeArgs[0], tpNames, out)
			}
		}
		if p.Name == "Array" && len(p.TypeArgs) == 1 {
			if arr, ok := concrete.(*types.StackArray); ok {
				matchTypeExprForInference(p.TypeArgs[0], arr.Elem, tpNames, out)
			}
		}
		if p.Name == "Array" && len(p.TypeArgs) == 2 {
			if arr, ok := concrete.(*types.StackArray); ok {
				szConst := types.NewConstInt(arr.Size, uint64(arr.Size), types.TypeInt)
				matchTypeExprForInference(p.TypeArgs[0], szConst, tpNames, out)
				matchTypeExprForInference(p.TypeArgs[1], arr.Elem, tpNames, out)
			}
		}
		if st, ok := concrete.(*types.Struct); ok && st.Name == p.Name && len(p.TypeArgs) == len(st.TypeArgs) {
			for i := range p.TypeArgs {
				matchTypeExprForInference(p.TypeArgs[i], st.TypeArgs[i], tpNames, out)
			}
		}
		if en, ok := concrete.(*types.Enum); ok && en.Name == p.Name && len(p.TypeArgs) == len(en.TypeArgs) {
			for i := range p.TypeArgs {
				matchTypeExprForInference(p.TypeArgs[i], en.TypeArgs[i], tpNames, out)
			}
		}
	case *ast.OptionalType:
		if opt, ok := concrete.(*types.Optional); ok {
			matchTypeExprForInference(p.Elem, opt.Elem, tpNames, out)
		} else {
			matchTypeExprForInference(p.Elem, concrete, tpNames, out)
		}
	case *ast.VectorType:
		if vec, ok := concrete.(*types.Vector); ok {
			matchTypeExprForInference(p.Elem, vec.Elem, tpNames, out)
		} else if arr, ok := concrete.(*types.StackArray); ok {
			matchTypeExprForInference(p.Elem, arr.Elem, tpNames, out)
		} else if st, ok := concrete.(*types.Struct); ok && (st.Name == "List" || st.Name == "Array") && len(st.TypeArgs) == 1 {
			matchTypeExprForInference(p.Elem, st.TypeArgs[0], tpNames, out)
		}
	case *ast.StackArrayType:
		if arr, ok := concrete.(*types.StackArray); ok {
			if id, isId := p.Size.(*ast.IdentExpr); isId && tpNames[id.Name] {
				if _, exists := out[id.Name]; !exists {
					out[id.Name] = types.NewConstInt(arr.Size, uint64(arr.Size), types.TypeInt)
				}
			}
			matchTypeExprForInference(p.Elem, arr.Elem, tpNames, out)
		}
	case *ast.SetType:
		if st, ok := types.CollView(concrete).(*types.Set); ok {
			matchTypeExprForInference(p.Elem, st.Elem, tpNames, out)
		}
	case *ast.MapType:
		if mt, ok := types.CollView(concrete).(*types.Map); ok {
			matchTypeExprForInference(p.Key, mt.Key, tpNames, out)
			matchTypeExprForInference(p.Value, mt.Value, tpNames, out)
		}
	case *ast.TupleType:
		if tup, ok := concrete.(*types.Tuple); ok && len(p.Elems) == len(tup.Elems) {
			for i := range p.Elems {
				matchTypeExprForInference(p.Elems[i], tup.Elems[i], tpNames, out)
			}
		}
	case *ast.FuncType:
		if fn, ok := concrete.(*types.Func); ok && len(p.Params) == len(fn.Params) {
			for i := range p.Params {
				matchTypeExprForInference(p.Params[i].Type, fn.Params[i].Type, tpNames, out)
			}
			if p.Ret != nil {
				matchTypeExprForInference(p.Ret, fn.Ret, tpNames, out)
			}
		}
	case *ast.MutBorrowType:
		matchTypeExprForInference(p.Elem, concrete, tpNames, out)
	case *ast.OwnType:
		matchTypeExprForInference(p.Elem, concrete, tpNames, out)
	}
}

func (fc *funcChecker) checkFuncLit(fl *ast.FuncLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(fl)
	var ps []types.FuncParam
	for _, p := range fl.Params {
		pm, pt := fc.a.resolveParamType(fc.mod, p.Type, fc.subst, false)
		fc.a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "lambda parameter")
		ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
	}
	var expRet types.Type
	inferRet := false
	if fl.Ret != nil {
		expRet = fc.a.resolveReturnType(fc.mod, fl.Ret, fc.subst, false)
	} else if fnExp, ok := expected.(*types.Func); ok && fnExp.Ret != nil && fnExp.Ret != types.TypeInvalid {
		expRet = fnExp.Ret
	} else {
		inferRet = true
	}
	sig := &types.Func{Params: ps, Ret: expRet, Closure: fl.Closure, Extern: fl.Extern}

	// Push lambda boundary & capture tracker!
	fc.funcDepth++
	lf := &lambdaFrame{
		funcDepth: fc.funcDepth,
		captures:  make(map[*Symbol]*Capture),
	}
	fc.lambdaStack = append(fc.lambdaStack, lf)

	prevHasArena := fc.hasMutArenaParam
	fc.hasMutArenaParam = false
	lamOuterDepth := fc.curDepth()
	fc.pushScope()
	var paramSyms []*Symbol
	for idx, fp := range ps {
		bkind := BindReadBorrow
		isBind := true
		switch fp.Mode {
		case types.ModeMut:
			bkind = BindMutBorrow
			isBind = false
			if types.IsArena(fp.Type) {
				fc.hasMutArenaParam = true
			}
		case types.ModeOwn:
			bkind = BindOwned
			isBind = false
		}
		pspan := fl.Span
		if idx < len(fl.Params) {
			pspan = fl.Params[idx].Span
		}
		sym := fc.declareLocal(fp.Name, fp.Type, isBind, bkind, pspan)
		if bkind != BindOwned {
			sym.ScopeDepth = 0
		}
		paramSyms = append(paramSyms, sym)
		fc.moves[sym] = MoveAlive
	}

	lamTargetID := fc.a.allocTargetID()
	lamTarget := &cfTarget{
		id:              lamTargetID,
		kind:            cfFunc,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: lamOuterDepth,
		expectedType:    expRet,
		isInferredFunc:  inferRet,
	}
	fc.cfStack = append(fc.cfStack, lamTarget)

	ret := fc.checkFuncBodyAndReturn("<lambda>", fl.Body, expRet, inferRet, fl.Span, lamTarget)
	sig.Ret = ret
	if fl.Extern {
		fc.a.checkCSignature(sig, fl.Span)
	}
	if ret != nil && ret != types.TypeInvalid && types.ContainsArenaRef(ret) && !fc.hasMutArenaParam && !prevHasArena {
		if fc.a.infoFor(fl.Body).Type != types.TypeNever && fc.arenaDepthOfExpr(fl.Body) == 0 {
			fc.a.Rep.Errorf(fl.Body.GetSpan(), "lambda cannot return %s containing `ref[T]` unless it takes an `&Arena` parameter", ret.String())
		}
	}

	fc.cfStack = fc.cfStack[:len(fc.cfStack)-1]
	fc.popScope()
	fc.hasMutArenaParam = prevHasArena
	fc.lambdaStack = fc.lambdaStack[:len(fc.lambdaStack)-1]
	fc.funcDepth--

	// Apply Closure-as-Anonymous-Struct capture rules (Spec §3.3 / Decision A6):
	// Plain captures copy; Resource captures MOVE out of enclosing scope!
	id := fc.a.allocLambdaID()
	var caps []Capture
	maxCapArenaDepth := 0
	for _, c := range lf.order {
		if c.OuterSym != nil && c.OuterSym.ArenaDepth > maxCapArenaDepth {
			maxCapArenaDepth = c.OuterSym.ArenaDepth
		}
		if !c.Type.IsPlain() {
			if c.OuterSym.BindKind != BindOwned {
				fc.a.Rep.ErrorWithHint(
					fl.Span,
					fmt.Sprintf("lambda cannot capture borrowed Resource parameter %q", c.Name),
					fmt.Sprintf("bind an explicit copy first (`bind %s_copy = %s.copy()`) and capture that", c.Name, c.Name),
				)
			} else if fc.isArenaPinned(c.OuterSym) {
				fc.a.Rep.ErrorWithHint(
					fl.Span,
					fmt.Sprintf("lambda cannot capture and move `Arena` %q while `ref[T]` allocations from this scope may still be live", c.Name),
					"wrap the phase that allocates `ref[T]` in a `{ ... }` block before moving the arena",
				)
			} else {
				fc.moves[c.OuterSym] = MoveMoved
				c.Moved = true
			}
		}
		caps = append(caps, *c)
	}
	inf.YieldArenaDepth = maxCapArenaDepth
	if fl.Extern && len(caps) > 0 {
		fc.a.Rep.ErrorWithHint(fl.Span,
			fmt.Sprintf("an `extern fn` can't capture variables, but this one uses %q from the enclosing scope", caps[0].Name),
			"C calls it with nothing but its arguments; pass what it needs through them (a `ptr[u8]` user-data argument, for example)")
	} else if !fl.Closure && len(caps) > 0 {
		fc.a.Rep.ErrorWithHint(fl.Span,
			fmt.Sprintf("an `fn` can't capture variables, but this one uses %q from the enclosing scope", caps[0].Name),
			"write `cl(...)` for a closure")
	} else if fl.Closure && len(caps) == 0 && fc.probing == 0 {
		// (Not while probing an argument for generic inference, which has no expected type yet.)
		if fnExp, ok := expected.(*types.Func); !ok || !fnExp.Closure {
			fc.a.Rep.Warnf(fl.Span, "this closure captures nothing, so it could be an `fn(...)`")
		}
	}

	li := &LambdaInfo{
		ID:           id,
		CName:        fmt.Sprintf("ll_lambda_%d", id),
		EnvCName:     fmt.Sprintf("ll_lambda_env_%d", id),
		Sig:          sig,
		Captures:     caps,
		Lit:          fl,
		ParamSyms:    paramSyms,
		FuncTargetID: lamTargetID,
		BodyMaps:     fc.a.curBM,
		ComptimeOnly: fc.inComptimeOnlyContext(),
	}
	if prev := inf.Lambda; prev != nil {
		for i := len(fc.a.Prog.Lambdas) - 1; i >= 0; i-- {
			if fc.a.Prog.Lambdas[i] == prev {
				fc.a.Prog.Lambdas[i] = li
				inf.Lambda = li
				inf.Type = sig
				return sig
			}
		}
	}
	fc.a.Prog.Lambdas = append(fc.a.Prog.Lambdas, li)
	inf.Lambda = li
	inf.Type = sig
	return sig
}

func (fc *funcChecker) recordLambdaCapture(sym *Symbol, span diag.Span) {
	// Record capture across every intermediate lambda boundary from `sym.DeclFuncDepth+1` up to `fc.funcDepth`!
	for _, lf := range fc.lambdaStack {
		if sym.DeclFuncDepth < lf.funcDepth {
			if _, exists := lf.captures[sym]; !exists {
				if sym.BindKind != BindOwned && sym.Type.IsPlain() {
					// Plain read-only param can be copied by value into closure!
					if sym.BindKind == BindMutBorrow {
						fc.a.Rep.Errorf(span, "lambda cannot capture mutable borrow `%s` (closures cannot hold borrowed references)", sym.Name)
					}
				}
				capEntry := &Capture{
					Name:     sym.Name,
					CName:    types.SanitizeCIdent(sym.Name),
					Type:     sym.Type,
					OuterSym: sym,
				}
				lf.captures[sym] = capEntry
				lf.order = append(lf.order, capEntry)
			}
		}
	}
	// Enforce deep immutability of captured variables inside the lambda (Decision A6)!
	// Notice `analyzePlace` checks `sym.IsBind`, OR if `sym.DeclFuncDepth < fc.funcDepth`, let's forbid mutating `sym`!
	// Wait: let's make sure `analyzePlace` ALSO checks `sym.DeclFuncDepth < fc.funcDepth`!
}

// -----------------------------------------------------------------------------
// Control Flow: `if`, `for`, `match`
// -----------------------------------------------------------------------------

func (fc *funcChecker) checkIfExpr(ie *ast.IfExpr, expected types.Type, needValue bool) types.Type {
	if ie.IsComptime {
		return fc.checkComptimeIfExpr(ie, expected, needValue)
	}
	inf := fc.a.infoFor(ie)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	allowVoid := fc.inferVoidOrValue
	fc.inferVoidOrValue = false

	var evalTy types.Type
	if ie.EvalType != nil {
		evalTy = fc.a.resolveValueType(fc.mod, ie.EvalType, fc.subst, false)
		allowVoid = false
		if ie.Else == nil && evalTy != nil && evalTy != types.TypeInvalid {
			if _, isOpt := evalTy.(*types.Optional); !isOpt {
				fc.a.Rep.ErrorWithHint(
					ie.EvalType.GetSpan(),
					fmt.Sprintf("`if` without an `else` branch must have an optional evaluation type (got %s)", evalTy.String()),
					fmt.Sprintf("use `?%s` or add an `else` branch", evalTy.String()),
				)
				evalTy = types.NewOptional(evalTy)
			}
		}
	}

	wantValue := needValue || evalTy != nil

	// Determine target expected type for `if <- val`:
	var branchExp types.Type
	if evalTy != nil && evalTy != types.TypeInvalid {
		branchExp = evalTy
	} else if needValue && expected != nil && expected != types.TypeVoid {
		branchExp = expected
		if ie.Else == nil {
			if opt, ok := expected.(*types.Optional); ok {
				branchExp = opt.Elem
			}
		}
	}

	// Check condition & set up optional unwrap scope if `if (user?)` / `if (expr as x)`
	var lockedOrigin *Symbol
	var unwrapSym *Symbol
	switch ie.CondKind {
	case ast.IfCondBool:
		cty := fc.checkExprExpect(ie.Cond, types.TypeBool, true)
		if cty != types.TypeInvalid && !cty.Equals(types.TypeBool) {
			if _, isOpt := cty.(*types.Optional); isOpt {
				fc.a.Rep.ErrorWithHint(
					ie.Cond.GetSpan(),
					fmt.Sprintf("`if` condition has optional type %s; optional expressions must be unwrapped with `if (expr as name)`", cty.String()),
					"use `if (user?)` for optional variables or `if (expr as val)` for optional expressions",
				)
			} else {
				fc.a.reportTruthiness(ie.Cond, cty, "`if`")
			}
		}

	case ast.IfCondOptVar:
		orig := fc.lookupVar(ie.OptVar)
		if orig == nil {
			fc.a.Rep.Errorf(ie.Span, "undeclared optional variable %q in `if`", ie.OptVar)
		} else {
			fc.checkSymReadable(orig, ie.Span)
			optTy, ok := orig.Type.(*types.Optional)
			if !ok {
				fc.a.Rep.Errorf(ie.Span, "variable %q has non-optional type %s", ie.OptVar, orig.Type.String())
			} else {
				if ie.MutUnwrap && orig.IsBind {
					fc.a.Rep.Errorf(ie.Span, "cannot mutably unwrap `if (&%s)` because %q is immutable (`bind`)", ie.OptVar, ie.OptVar)
				}
				lockedOrigin = orig
				bkind := BindReadBorrow
				isBind := true
				if ie.MutUnwrap {
					bkind = BindMutBorrow
					isBind = false
				}
				unwrapSym = &Symbol{
					Name:          ie.BindName,
					CName:         fmt.Sprintf("%s_unwrap_%d", types.SanitizeCIdent(ie.BindName), id),
					Type:          optTy.Elem,
					IsBind:        isBind,
					BindKind:      bkind,
					ScopeDepth:    fc.curDepth() + 1,
					ArenaDepth:    orig.ArenaDepth,
					DeclFuncDepth: fc.funcDepth,
					Span:          ie.Span,
				}
				fc.a.curBM.IfUnwrapSyms[ie] = unwrapSym
				fc.a.curBM.IfOriginSyms[ie] = orig
			}
		}

	case ast.IfCondOptBind:
		var asVariantRecv ast.Expr
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
		prevUnwrapCond := fc.ifUnwrapCondExpr
		fc.ifUnwrapCondExpr = unwrappedCond
		cty := fc.checkExpr(ie.Cond, true)
		fc.ifUnwrapCondExpr = prevUnwrapCond
		optTy, ok := cty.(*types.Optional)
		if condCall, isCall := unwrappedCond.(*ast.CallExpr); isCall {
			if cInf := fc.a.infoFor(condCall); cInf != nil && !cInf.IsOptChain && cInf.BuiltinName == "enum.as_variant" && cInf.EnumVariant != nil {
				if cty != types.TypeInvalid && cInf.EnumVariant.Payload != nil {
					optTy, ok = &types.Optional{Elem: cInf.EnumVariant.Payload}, true
				}
				asVariantRecv = cInf.MethodRecv
			}
		}
		if resEn := types.AsResult(cty); resEn != nil {
			// `if (r! as v)`: binds the result's success value; `else` runs on any failure.
			if vv, _ := resEn.ResultValue(); vv != nil && vv.Payload != nil {
				optTy, ok = &types.Optional{Elem: vv.Payload}, true
			}
		}
		if !ok {
			if en := types.AsResult(cty); en != nil && en.IsUnitResult() {
				fc.a.Rep.ErrorWithHint(ie.Cond.GetSpan(),
					fmt.Sprintf("%s is a unit result: its `value` has nothing to bind to `%s`", cty.String(), ie.BindName),
					"handle the failures with a chain (`expr: when ... default ...`) or a `match`")
			} else if cty != types.TypeInvalid {
				fc.a.Rep.Errorf(ie.Cond.GetSpan(), "`if (expr as %s)` requires an optional `?T` or result expression, got %s", ie.BindName, cty.String())
			}
		} else {
			if ie.MutUnwrap {
				if asVariantRecv != nil {
					if recvInf := fc.a.infoFor(asVariantRecv); recvInf != nil {
						if _, isRef := recvInf.Type.(*types.Ref); isRef {
							lockedOrigin = fc.rootSymbolOfExpr(asVariantRecv)
						} else {
							lockedOrigin = fc.checkMutablePlace(asVariantRecv, "mutably unwrap")
						}
					}
				} else {
					lockedOrigin = fc.checkMutablePlace(ie.Cond, "mutably unwrap")
				}
			} else {
				lockedOrigin = fc.rootSymbolOfExpr(ie.Cond)
			}
			bkind := BindReadBorrow
			isBind := true
			if ie.MutUnwrap {
				bkind = BindMutBorrow
				isBind = false
			}
			unwrapSym = &Symbol{
				Name:          ie.BindName,
				CName:         fmt.Sprintf("%s_as_%d", types.SanitizeCIdent(ie.BindName), id),
				Type:          optTy.Elem,
				IsBind:        isBind,
				BindKind:      bkind,
				ScopeDepth:    fc.curDepth() + 1,
				ArenaDepth:    fc.arenaDepthOfExpr(ie.Cond),
				DeclFuncDepth: fc.funcDepth,
				Span:          ie.Span,
			}
			fc.a.curBM.IfUnwrapSyms[ie] = unwrapSym
		}
	}

	outerDepth := fc.curDepth()
	tgt := &cfTarget{
		id:              id,
		kind:            cfIf,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: outerDepth,
		expectedType:    branchExp,
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	preMoves := fc.cloneMoves()

	// Check `then` branch
	fc.pushScope()
	if unwrapSym != nil {
		fc.scopes[len(fc.scopes)-1].vars[unwrapSym.Name] = unwrapSym
		fc.moves[unwrapSym] = MoveAlive
	}
	if lockedOrigin != nil {
		fc.activeBorrows[lockedOrigin]++
	}

	var thenTy types.Type
	if !wantValue {
		thenTy = fc.checkVoidOrDiscardedBody(ie.Then)
	} else {
		if allowVoid && branchExp == nil {
			thenTy = fc.checkInferOrValue(ie.Then, nil)
		} else {
			thenTy = fc.checkExprExpect(ie.Then, branchExp, true)
		}
		if thenTy == types.TypeVoid {
			if !(allowVoid && branchExp == nil) {
				fc.a.Rep.Errorf(ie.Then.GetSpan(), "`if` branch used as a value must evaluate to a value")
			}
		} else if branchExp != nil && thenTy != types.TypeNever && thenTy != types.TypeInvalid {
			fc.verifyAssignable(thenTy, branchExp, ie.Then, ie.Then.GetSpan(), "`if` then-branch")
			fc.consumeValue(ie.Then, branchExp, false)
		} else if thenTy != types.TypeNever && thenTy != types.TypeInvalid {
			fc.consumeValue(ie.Then, thenTy, false)
		}
	}

	if lockedOrigin != nil {
		fc.activeBorrows[lockedOrigin]--
	}
	thenAd := fc.arenaDepthOfExpr(ie.Then)
	if wantValue && thenTy != types.TypeVoid && thenTy != types.TypeNever && thenAd > outerDepth {
		fc.a.Rep.Errorf(ie.Then.GetSpan(), "arena reference `ref[T]` cannot escape `if` branch scope")
	}
	if wantValue && thenTy != types.TypeVoid && thenTy != types.TypeNever && thenAd > tgt.maxYieldArenaDepth {
		tgt.maxYieldArenaDepth = thenAd
	}
	fc.popScope()
	thenMoves := fc.cloneMoves()

	// Restore moves before `else` branch!
	fc.moves = fc.cloneMovesFrom(preMoves)
	var elseMoves map[*Symbol]MoveState

	if ie.Else == nil {
		elseMoves = preMoves
		if thenTy == types.TypeNever {
			fc.moves = elseMoves
		} else {
			fc.moves = mergeMoveMaps(thenMoves, elseMoves)
		}
		if !needValue {
			inf.Type = types.TypeVoid
			return types.TypeVoid
		}
		if allowVoid && evalTy == nil {
			if thenTy == types.TypeInvalid {
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			if thenTy == types.TypeVoid {
				if len(tgt.yieldTypes) > 0 {
					fc.a.Rep.Errorf(ie.Then.GetSpan(), "`if` branch yields a value via `if <-` on some paths, but does not evaluate to a value on all paths")
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
			if thenTy == types.TypeNever && len(tgt.yieldTypes) == 0 {
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
		}
		elemTy := branchExp
		if elemTy == nil {
			elemTy = unifyYieldTypes(append(tgt.yieldTypes, thenTy))
		}
		resOpt := types.NewOptional(elemTy)
		inf.YieldArenaDepth = tgt.maxYieldArenaDepth
		if typeMayHoldArenaRef(resOpt) {
			fc.lowerPinnedArenasTo(outerDepth)
		}
		inf.Type = resOpt
		return resOpt
	}

	// Has `else` branch!
	fc.pushScope()
	var elseTy types.Type
	if !wantValue {
		elseTy = fc.checkVoidOrDiscardedBody(ie.Else)
	} else {
		hint := branchExp
		if hint == nil && thenTy != types.TypeVoid && thenTy != types.TypeNever && thenTy != types.TypeInvalid {
			hint = thenTy
			if _, isNone := ie.Else.(*ast.NoneLit); isNone {
				hint = types.NewOptional(thenTy)
			}
		}
		if allowVoid && hint == nil {
			elseTy = fc.checkInferOrValue(ie.Else, nil)
		} else {
			elseTy = fc.checkExprExpect(ie.Else, hint, true)
		}
		if elseTy == types.TypeVoid {
			if !(allowVoid && branchExp == nil) {
				fc.a.Rep.Errorf(ie.Else.GetSpan(), "`else` branch used as a value must evaluate to a value")
			}
		} else if branchExp != nil && elseTy != types.TypeNever && elseTy != types.TypeInvalid {
			fc.verifyAssignable(elseTy, branchExp, ie.Else, ie.Else.GetSpan(), "`if` else-branch")
			fc.consumeValue(ie.Else, branchExp, false)
		}
	}
	elseAd := fc.arenaDepthOfExpr(ie.Else)
	if wantValue && elseTy != types.TypeVoid && elseTy != types.TypeNever && elseAd > outerDepth {
		fc.a.Rep.Errorf(ie.Else.GetSpan(), "arena reference `ref[T]` cannot escape `else` branch scope")
	}
	if wantValue && elseTy != types.TypeVoid && elseTy != types.TypeNever && elseAd > tgt.maxYieldArenaDepth {
		tgt.maxYieldArenaDepth = elseAd
	}
	fc.popScope()
	elseMoves = fc.cloneMoves()

	// Diverging branch shouldn't taint post-if move state!
	switch {
	case thenTy == types.TypeNever && elseTy == types.TypeNever:
		// Both diverge
	case thenTy == types.TypeNever:
		fc.moves = elseMoves
	case elseTy == types.TypeNever:
		fc.moves = thenMoves
	default:
		fc.moves = mergeMoveMaps(thenMoves, elseMoves)
	}

	if !needValue {
		if thenTy == types.TypeNever && elseTy == types.TypeNever && !tgt.hitValueExit {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	if allowVoid && branchExp == nil {
		if thenTy == types.TypeInvalid || elseTy == types.TypeInvalid {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		isThenVoid := thenTy == types.TypeVoid
		isElseVoid := elseTy == types.TypeVoid
		if isThenVoid && isElseVoid {
			if len(tgt.yieldTypes) > 0 {
				fc.a.Rep.Errorf(ie.Span, "`if` expression mixes `void` and value-producing paths")
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			inf.Type = types.TypeVoid
			return types.TypeVoid
		}
		if isThenVoid || isElseVoid {
			otherTy := elseTy
			if isElseVoid {
				otherTy = thenTy
			}
			if otherTy == types.TypeNever && len(tgt.yieldTypes) == 0 {
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
			fc.a.Rep.Errorf(ie.Span, "`if` and `else` branches must both evaluate to a value or both be `void`")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	}

	resTy := branchExp
	if resTy == nil {
		resTy = unifyYieldTypes(append(tgt.yieldTypes, thenTy, elseTy))
	}
	if branchExp == nil {
		if thenTy != types.TypeVoid && thenTy != types.TypeNever && thenTy != types.TypeInvalid {
			fc.verifyAssignable(thenTy, resTy, ie.Then, ie.Then.GetSpan(), "`if` then-branch")
		}
		if elseTy != types.TypeVoid && elseTy != types.TypeNever && elseTy != types.TypeInvalid {
			fc.verifyAssignable(elseTy, resTy, ie.Else, ie.Else.GetSpan(), "`if` else-branch")
			fc.consumeValue(ie.Else, resTy, false)
		}
	}
	if thenTy == types.TypeNever && elseTy == types.TypeNever && !tgt.hitValueExit {
		inf.Type = types.TypeNever
		return types.TypeNever
	}
	inf.YieldArenaDepth = tgt.maxYieldArenaDepth
	if typeMayHoldArenaRef(resTy) {
		fc.lowerPinnedArenasTo(outerDepth)
	}
	inf.Type = resTy
	return resTy
}

func (fc *funcChecker) checkComptimeIfExpr(ie *ast.IfExpr, expected types.Type, needValue bool) types.Type {
	inf := fc.a.infoFor(ie)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	allowVoid := fc.inferVoidOrValue
	fc.inferVoidOrValue = false

	var evalTy types.Type
	if ie.EvalType != nil {
		evalTy = fc.a.resolveValueType(fc.mod, ie.EvalType, fc.subst, false)
		allowVoid = false
	}

	wantValue := needValue || evalTy != nil
	var branchExp types.Type
	if evalTy != nil && evalTy != types.TypeInvalid {
		branchExp = evalTy
	} else if needValue && expected != nil && expected != types.TypeVoid {
		branchExp = expected
	}

	// Evaluate condition in strict compile-time scope (no runtime locals/params, even in `const fn`).
	takeThen := false
	condOK := true
	prevDepth := fc.comptimeExprDepth
	prevMin := fc.comptimeMinScope
	fc.comptimeExprDepth++
	if prevDepth == 0 {
		fc.comptimeMinScope = fc.curDepth() + 1
	}
	switch ie.CondKind {
	case ast.IfCondBool:
		cty := fc.checkExprExpect(ie.Cond, types.TypeBool, true)
		if cty == types.TypeInvalid {
			condOK = false
		} else if !cty.Equals(types.TypeBool) {
			if _, isOpt := cty.(*types.Optional); isOpt {
				fc.a.Rep.ErrorWithHint(
					ie.Cond.GetSpan(),
					fmt.Sprintf("`comptime if` condition has optional type %s; optional expressions are not supported in `comptime if` conditions", cty.String()),
					"evaluate a `bool` expression or use `comptime match`",
				)
			} else {
				fc.a.reportTruthiness(ie.Cond, cty, "`comptime if`")
			}
			condOK = false
		}
	default:
		fc.a.Rep.ErrorWithHint(
			ie.Span,
			"optional unwrap conditions (`if (x?)` / `if (expr as v)`) are not supported on `comptime if`",
			"use a `bool` condition (`comptime if (cond):`) or `comptime match`",
		)
		condOK = false
	}
	if condOK {
		if cv, ok := fc.a.evalComptimeExprInChecker(fc, ie.Cond, 0); ok && cv != nil {
			takeThen = cv.b
		} else {
			condOK = false
		}
	}
	fc.comptimeExprDepth = prevDepth
	fc.comptimeMinScope = prevMin

	if !condOK {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	// H11.1: expression position requires `else` and cannot produce `?T` implicitly by omitting `else`.
	if ie.Else == nil && wantValue && !allowVoid {
		fc.a.Rep.ErrorWithHint(
			ie.Span,
			"`comptime if` in expression position must include an `else` branch",
			"add an `else:` branch (unlike runtime `if`, `comptime if` cannot omit `else` to produce an optional)",
		)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	outerDepth := fc.curDepth()
	tgt := &cfTarget{
		id:              id,
		kind:            cfIf,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: outerDepth,
		expectedType:    branchExp,
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	if !takeThen && ie.Else == nil {
		if fc.a.curBM != nil {
			fc.a.curBM.ComptimeIfBranch[ie] = 3
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	chosenExpr := ie.Then
	branchCode := 0
	if !takeThen {
		chosenExpr = ie.Else
		branchCode = 2
	}
	if fc.a.curBM != nil {
		fc.a.curBM.ComptimeIfBranch[ie] = branchCode
	}

	fc.pushScope()
	var chosenTy types.Type
	if !wantValue {
		chosenTy = fc.checkVoidOrDiscardedBody(chosenExpr)
	} else if allowVoid && branchExp == nil {
		chosenTy = fc.checkInferOrValue(chosenExpr, nil)
	} else {
		chosenTy = fc.checkExprExpect(chosenExpr, branchExp, true)
	}
	chosenAd := fc.arenaDepthOfExpr(chosenExpr)
	fc.popScope()

	if ie.Else == nil {
		// Statement/void `comptime if` without `else` when condition was true:
		// must NOT yield a value (`chosenTy` or `if <- val`).
		if len(tgt.yieldTypes) > 0 || (chosenTy != types.TypeVoid && chosenTy != types.TypeNever && chosenTy != types.TypeInvalid) {
			fc.a.Rep.ErrorWithHint(
				ie.Span,
				"`comptime if` in expression position must include an `else` branch",
				"add an `else:` branch (unlike runtime `if`, `comptime if` cannot omit `else` to produce an optional)",
			)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if chosenTy == types.TypeNever {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		if chosenTy == types.TypeInvalid {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	if !wantValue {
		if chosenTy == types.TypeNever && !tgt.hitValueExit {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	if chosenTy == types.TypeVoid && allowVoid && branchExp == nil {
		if len(tgt.yieldTypes) > 0 {
			fc.a.Rep.Errorf(ie.Span, "`comptime if` branch yields a value via `if <-` on some paths, but falls through with `void`")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}
	if chosenTy == types.TypeVoid {
		fc.a.Rep.Errorf(chosenExpr.GetSpan(), "`comptime if` branch used as a value must evaluate to a value")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if chosenTy != types.TypeNever && chosenTy != types.TypeInvalid {
		if chosenAd > outerDepth {
			fc.a.Rep.Errorf(chosenExpr.GetSpan(), "arena reference `ref[T]` cannot escape `comptime if` branch scope")
		} else if chosenAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = chosenAd
		}
	}

	resTy := branchExp
	if resTy == nil {
		resTy = unifyYieldTypes(append(tgt.yieldTypes, chosenTy))
	} else if chosenTy != types.TypeNever && chosenTy != types.TypeInvalid {
		fc.verifyAssignable(chosenTy, branchExp, chosenExpr, chosenExpr.GetSpan(), "`comptime if` branch")
		fc.consumeValue(chosenExpr, branchExp, false)
	}
	if branchExp == nil && chosenTy != types.TypeVoid && chosenTy != types.TypeNever && chosenTy != types.TypeInvalid {
		fc.verifyAssignable(chosenTy, resTy, chosenExpr, chosenExpr.GetSpan(), "`comptime if` branch")
		fc.consumeValue(chosenExpr, resTy, false)
	}
	if chosenTy == types.TypeNever && !tgt.hitValueExit {
		inf.Type = types.TypeNever
		return types.TypeNever
	}
	inf.YieldArenaDepth = tgt.maxYieldArenaDepth
	if typeMayHoldArenaRef(resTy) {
		fc.lowerPinnedArenasTo(outerDepth)
	}
	inf.Type = resTy
	return resTy
}

func (fc *funcChecker) cloneMovesFrom(src map[*Symbol]MoveState) map[*Symbol]MoveState {
	cp := make(map[*Symbol]MoveState, len(src))
	for k, v := range src {
		cp[k] = v
	}
	return cp
}

type loopHeaderInfo struct {
	kind       ast.ForKind
	condOrIter ast.Expr
	rangeEnd   ast.Expr
	inclusive  bool
	var1       string
	var1IsOpt  bool
	var1Mut    bool
	var2       string
	var2IsOpt  bool
	var2Mut    bool
	span       diag.Span
}

func (fc *funcChecker) checkLoopHeader(h loopHeaderInfo, id int) (sym1, sym2 *Symbol, lockedCol *Symbol, isInfiniteTrue bool) {
	switch h.kind {
	case ast.ForCond:
		if bl, ok := h.condOrIter.(*ast.BoolLit); ok && bl.Val {
			isInfiniteTrue = true
		}
		cty := fc.checkExprExpect(h.condOrIter, types.TypeBool, true)
		if cty != types.TypeInvalid && !cty.Equals(types.TypeBool) {
			fc.a.reportTruthiness(h.condOrIter, cty, "loop")
		}

	case ast.ForRange:
		loTy, hiTy := fc.checkOperandPairWithAdaptation(h.condOrIter, h.rangeEnd, types.TypeInt)
		p := types.UnderlyingPrimitive(loTy)
		if loTy != types.TypeInvalid && hiTy != types.TypeInvalid && (p == nil || (!p.IsInteger() && p.Kind != types.PrimChar) || !loTy.Equals(hiTy)) {
			fc.a.Rep.Errorf(h.span, "range bounds in loop `(%s in a..b)` must be matching integer or `char` types, got %s and %s",
				h.var1, loTy.String(), hiTy.String())
		}
		if h.var2 != "" || h.var1Mut {
			fc.a.Rep.Errorf(h.span, "range loop only binds a single read-only variable (`(i in lo..hi)`)")
		}
		fc.a.checkOptionalIdentRule(h.var1, h.var1IsOpt, loTy, h.span, "loop variable")
		sym1 = &Symbol{
			Name:          h.var1,
			CName:         fmt.Sprintf("%s_loop_%d", types.SanitizeCIdent(h.var1), id),
			Type:          loTy,
			IsBind:        true,
			BindKind:      BindOwned,
			ScopeDepth:    fc.curDepth() + 1,
			DeclFuncDepth: fc.funcDepth,
			Span:          h.span,
		}

	case ast.ForIter:
		colTy := fc.checkExpr(h.condOrIter, true)
		lockedCol = fc.rootSymbolOfExpr(h.condOrIter)
		if (h.var1Mut || h.var2Mut) && colTy != types.TypeInvalid {
			_ = fc.checkMutablePlace(h.condOrIter, "mutably iterate over")
		}
		sym1, sym2 = fc.resolveIterVars(h, colTy, id)
		colAd := fc.arenaDepthOfExpr(h.condOrIter)
		if sym1 != nil && typeMayHoldArenaRef(sym1.Type) {
			sym1.ArenaDepth = colAd
		}
		if sym2 != nil && typeMayHoldArenaRef(sym2.Type) {
			sym2.ArenaDepth = colAd
		}
	}
	return
}

func (fc *funcChecker) resolveIterVars(h loopHeaderInfo, colTy types.Type, id int) (*Symbol, *Symbol) {
	if r, ok := colTy.(*types.Ref); ok {
		colTy = r.Elem
	}
	mkSym := func(name string, isOptName bool, ty types.Type, mut bool) *Symbol {
		fc.a.checkOptionalIdentRule(name, isOptName, ty, h.span, "loop variable")
		bkind := BindReadBorrow
		isBind := true
		if mut {
			bkind = BindMutBorrow
			isBind = false
		}
		return &Symbol{
			Name:          name,
			CName:         fmt.Sprintf("%s_it_%d", types.SanitizeCIdent(name), fc.a.allocTargetID()),
			Type:          ty,
			IsBind:        isBind,
			BindKind:      bkind,
			ScopeDepth:    fc.curDepth() + 1,
			DeclFuncDepth: fc.funcDepth,
			Span:          h.span,
		}
	}

	switch c := types.CollView(colTy).(type) {
	case *types.Vector:
		if h.var2 == "" {
			return mkSym(h.var1, h.var1IsOpt, c.Elem, h.var1Mut), nil
		}
		return mkSym(h.var1, h.var1IsOpt, types.TypeInt, false), mkSym(h.var2, h.var2IsOpt, c.Elem, h.var2Mut)

	case *types.StackArray:
		if h.var2 == "" {
			return mkSym(h.var1, h.var1IsOpt, c.Elem, h.var1Mut), nil
		}
		return mkSym(h.var1, h.var1IsOpt, types.TypeInt, false), mkSym(h.var2, h.var2IsOpt, c.Elem, h.var2Mut)

	case *types.Primitive:
		if c.Kind == types.PrimString {
			if h.var1Mut || h.var2Mut {
				fc.a.Rep.Errorf(h.span, "cannot mutably iterate UTF-8 characters of a `string` in place")
			}
			if h.var2 == "" {
				return mkSym(h.var1, h.var1IsOpt, types.TypeChar, false), nil
			}
			return mkSym(h.var1, h.var1IsOpt, types.TypeInt, false), mkSym(h.var2, h.var2IsOpt, types.TypeChar, false)
		}

	case *types.Set:
		if h.var1Mut || h.var2 != "" {
			fc.a.Rep.Errorf(h.span, "set iteration only supports a single read-only element (`(item in my_set)`)")
		}
		return mkSym(h.var1, h.var1IsOpt, c.Elem, false), nil

	case *types.Map:
		if h.var2 == "" {
			entryTy := fc.a.collectableItemType(c)
			if entryTy == nil || entryTy == types.TypeInvalid {
				fc.a.Rep.Errorf(h.span, "cannot resolve `MapEntry` struct for map iteration")
				return nil, nil
			}
			return mkSym(h.var1, h.var1IsOpt, entryTy, h.var1Mut), nil
		}
		return mkSym(h.var1, h.var1IsOpt, c.Key, false), mkSym(h.var2, h.var2IsOpt, c.Value, h.var2Mut)

	case *types.Struct:
		if fc.a.isStdStructTemplate(c, fc.a.Prog.GenStructRange) && len(c.TypeArgs) == 1 {
			if h.var2 != "" || h.var1Mut {
				fc.a.Rep.Errorf(h.span, "range loop only binds a single read-only variable (`(i in r)`)")
			}
			return mkSym(h.var1, h.var1IsOpt, c.TypeArgs[0], false), nil
		}
		if fc.a.isStdPoolStruct(c) {
			elem := c.TypeArgs[0]
			if h.var2 == "" {
				return mkSym(h.var1, h.var1IsOpt, elem, h.var1Mut), nil
			}
			if h.var1Mut {
				fc.a.Rep.Errorf(h.span, "pool handle loop variable `%s` cannot be mutable `&`; only the value (`%s`) may be mutably borrowed", h.var1, h.var2)
			}
			poolMod := fc.a.Prog.StructMods[c]
			var handleTy types.Type = types.TypeInvalid
			if poolMod != nil {
				if genHandle := poolMod.GenericStructs["Handle"]; genHandle != nil {
					handleTy = fc.a.instantiateStruct(genHandle, []types.Type{elem}, h.span)
				}
			}
			if handleTy == types.TypeInvalid {
				fc.a.Rep.Errorf(h.span, "cannot resolve `Handle` struct for pool iteration")
				return nil, nil
			}
			return mkSym(h.var1, h.var1IsOpt, handleTy, false), mkSym(h.var2, h.var2IsOpt, elem, h.var2Mut)
		}
		if (c.Name == "List" || strings.HasPrefix(c.Name, "List[")) && len(c.TypeArgs) == 1 {
			elem := c.TypeArgs[0]
			if h.var2 == "" {
				return mkSym(h.var1, h.var1IsOpt, elem, h.var1Mut), nil
			}
			return mkSym(h.var1, h.var1IsOpt, types.TypeInt, false), mkSym(h.var2, h.var2IsOpt, elem, h.var2Mut)
		}
		if (c.Name == "Array" || strings.HasPrefix(c.Name, "Array[")) && len(c.TypeArgs) >= 1 {
			elem := c.TypeArgs[len(c.TypeArgs)-1]
			if h.var2 == "" {
				return mkSym(h.var1, h.var1IsOpt, elem, h.var1Mut), nil
			}
			return mkSym(h.var1, h.var1IsOpt, types.TypeInt, false), mkSym(h.var2, h.var2IsOpt, elem, h.var2Mut)
		}
	}

	if colTy != types.TypeInvalid {
		fc.a.Rep.Errorf(h.condOrIter.GetSpan(), "cannot iterate over value of type %s", colTy.String())
	}
	return nil, nil
}

func (fc *funcChecker) checkForExpr(fe *ast.ForExpr, expected types.Type, needValue bool) types.Type {
	if fe.IsComptime {
		return fc.checkComptimeForExpr(fe, expected, needValue)
	}
	inf := fc.a.infoFor(fe)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	allowVoid := fc.inferVoidOrValue
	fc.inferVoidOrValue = false

	h := loopHeaderInfo{
		kind:       fe.Kind,
		condOrIter: fe.CondOrIter,
		rangeEnd:   fe.RangeEnd,
		inclusive:  fe.Inclusive,
		var1:       fe.Var1,
		var1IsOpt:  fe.Var1IsOpt,
		var1Mut:    fe.Var1Mut,
		var2:       fe.Var2,
		var2IsOpt:  fe.Var2IsOpt,
		var2Mut:    fe.Var2Mut,
		span:       fe.Span,
	}

	sym1, sym2, lockedCol, isInfiniteTrue := fc.checkLoopHeader(h, id)
	fc.a.curBM.ForSymbols[fe] = [2]*Symbol{sym1, sym2}

	var evalTy types.Type
	if fe.EvalType != nil {
		evalTy = fc.a.resolveValueType(fc.mod, fe.EvalType, fc.subst, false)
		allowVoid = false
	}
	wantValue := needValue || evalTy != nil

	if !wantValue {
		outerDepth := fc.curDepth()
		tgt := &cfTarget{
			id:              id,
			kind:            cfFor,
			label:           fe.Label,
			funcDepth:       fc.funcDepth,
			outerScopeDepth: outerDepth,
			expectedType:    types.TypeVoid,
		}
		fc.cfStack = append(fc.cfStack, tgt)
		defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

		preLoopMoves := fc.cloneMoves()
		loopScopeDepth := fc.curDepth() + 1

		fc.pushScope()
		if sym1 != nil {
			fc.scopes[len(fc.scopes)-1].vars[sym1.Name] = sym1
			fc.moves[sym1] = MoveAlive
		}
		if sym2 != nil {
			fc.scopes[len(fc.scopes)-1].vars[sym2.Name] = sym2
			fc.moves[sym2] = MoveAlive
		}
		if lockedCol != nil {
			fc.activeBorrows[lockedCol]++
		}

		fc.checkVoidOrDiscardedBody(fe.Body)

		if lockedCol != nil {
			fc.activeBorrows[lockedCol]--
		}
		fc.popScope()

		for sym, preState := range preLoopMoves {
			if sym.ScopeDepth < loopScopeDepth && preState == MoveAlive && fc.moves[sym] != MoveAlive {
				fc.a.Rep.ErrorWithHint(
					fe.Span,
					fmt.Sprintf("variable %q is moved inside a loop body without being reassigned before the next iteration", sym.Name),
					"reassign the variable on every path of the loop or `.copy()` it instead of moving",
				)
			}
		}
		postBodyMoves := fc.cloneMoves()
		fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)

		if fe.Else != nil {
			fc.checkVoidOrDiscardedBody(fe.Else)
		}

		_ = isInfiniteTrue
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	// Value-producing collection comprehension loop!
	var targetColTy types.Type
	var expectedItemTy types.Type
	isVectorInferred := false

	if evalTy != nil {
		if evalTy != types.TypeInvalid && !fc.a.isCollectableType(evalTy) {
			fc.a.Rep.Errorf(fe.EvalType.GetSpan(), "type %s does not implement Collectable", evalTy.String())
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		targetColTy = evalTy
		if evalTy != types.TypeInvalid {
			expectedItemTy = fc.a.collectableItemType(evalTy)
		}
	} else if expected != nil && expected != types.TypeVoid && expected != types.TypeInvalid && fc.a.isCollectableType(expected) {
		targetColTy = expected
		expectedItemTy = fc.a.collectableItemType(expected)
	} else {
		isVectorInferred = true
	}
	if _, isSet := types.SetElem(targetColTy); isSet {
		fc.a.requireCollMethod(targetColTy, "insert", fe.Span)
	} else if _, _, isMap := types.MapKV(targetColTy); isMap {
		fc.a.requireCollMethod(targetColTy, "insert", fe.Span)
	}

	outerDepth := fc.curDepth()
	tgt := &cfTarget{
		id:              id,
		kind:            cfFor,
		label:           fe.Label,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: outerDepth,
		expectedType:    expectedItemTy,
		isCollecting:    true,
		collectColType:  targetColTy,
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	preLoopMoves := fc.cloneMoves()
	loopScopeDepth := fc.curDepth() + 1

	fc.pushScope()
	if sym1 != nil {
		fc.scopes[len(fc.scopes)-1].vars[sym1.Name] = sym1
		fc.moves[sym1] = MoveAlive
	}
	if sym2 != nil {
		fc.scopes[len(fc.scopes)-1].vars[sym2.Name] = sym2
		fc.moves[sym2] = MoveAlive
	}
	if lockedCol != nil {
		fc.activeBorrows[lockedCol]++
	}

	var bodyTy types.Type
	if allowVoid && expectedItemTy == nil {
		bodyTy = fc.checkInferOrValue(fe.Body, nil)
	} else {
		bodyTy = fc.checkExprExpect(fe.Body, expectedItemTy, true)
	}

	if lockedCol != nil {
		fc.activeBorrows[lockedCol]--
	}
	if bodyTy != types.TypeVoid && bodyTy != types.TypeNever && bodyTy != types.TypeInvalid {
		bodyAd := fc.arenaDepthOfExpr(fe.Body)
		if bodyAd > outerDepth {
			fc.a.Rep.Errorf(fe.Body.GetSpan(), "arena reference `ref[T]` cannot escape `for` loop scope")
		} else if bodyAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = bodyAd
		}
	}
	fc.popScope()

	for sym, preState := range preLoopMoves {
		if sym.ScopeDepth < loopScopeDepth && preState == MoveAlive && fc.moves[sym] != MoveAlive {
			fc.a.Rep.ErrorWithHint(
				fe.Span,
				fmt.Sprintf("variable %q is moved inside a loop body without being reassigned before the next iteration", sym.Name),
				"reassign the variable on every path of the loop or `.copy()` it instead of moving",
			)
		}
	}
	postBodyMoves := fc.cloneMoves()
	fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)

	if isVectorInferred {
		if bodyTy == types.TypeVoid {
			if allowVoid {
				if fe.Else != nil {
					fc.pushScope()
					elseTy := fc.checkInferOrValue(fe.Else, nil)
					fc.popScope()
					if elseTy != types.TypeVoid && elseTy != types.TypeNever && elseTy != types.TypeInvalid {
						fc.a.Rep.Errorf(fe.Else.GetSpan(), "`for ... else` branch must be `void` when `for` body is `void`")
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
				}
				if len(tgt.yieldTypes) > 0 || len(tgt.breakColTypes) > 0 {
					fc.a.Rep.Errorf(fe.Span, "`for` loop yields a value via unwind on some paths, but body evaluates to `void`")
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
			fc.a.Rep.Errorf(fe.Body.GetSpan(), "`for` loop expression producing a list must evaluate to a non-void value")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if bodyTy == types.TypeNever {
			if len(tgt.yieldTypes) > 0 {
				yt := unifyYieldTypes(tgt.yieldTypes)
				if yt != types.TypeVoid && yt != types.TypeNever && yt != types.TypeInvalid {
					targetColTy = &types.Vector{Elem: yt}
					expectedItemTy = yt
				}
			} else if len(tgt.breakColTypes) > 0 {
				bt := unifyYieldTypes(tgt.breakColTypes)
				if bt != types.TypeVoid && bt != types.TypeNever && bt != types.TypeInvalid && fc.a.isCollectableType(bt) {
					targetColTy = bt
					expectedItemTy = fc.a.collectableItemType(bt)
				}
			}
			if targetColTy == nil {
				if allowVoid {
					if fe.Else != nil {
						fc.pushScope()
						elseTy := fc.checkInferOrValue(fe.Else, nil)
						fc.popScope()
						if elseTy != types.TypeVoid && elseTy != types.TypeNever && elseTy != types.TypeInvalid {
							fc.a.Rep.Errorf(fe.Else.GetSpan(), "`for ... else` branch must be `void` when `for` body diverges")
							inf.Type = types.TypeInvalid
							return types.TypeInvalid
						}
					}
					inf.Type = types.TypeVoid
					return types.TypeVoid
				}
				fc.a.Rep.Errorf(fe.Body.GetSpan(), "`for` loop expression producing a list must evaluate to a non-void value")
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
		} else if bodyTy == types.TypeInvalid {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		} else {
			targetColTy = &types.Vector{Elem: bodyTy}
			expectedItemTy = bodyTy
		}
	}

	if bodyTy != types.TypeVoid && bodyTy != types.TypeNever && bodyTy != types.TypeInvalid && expectedItemTy != nil {
		fc.verifyAssignable(bodyTy, expectedItemTy, fe.Body, fe.Body.GetSpan(), "`for` iteration item")
		fc.consumeValue(fe.Body, expectedItemTy, false)
	}
	if targetColTy != nil {
		for _, bt := range tgt.breakColTypes {
			fc.verifyAssignable(bt, targetColTy, fe, fe.Span, "`for` break value")
		}
	}
	if expectedItemTy != nil {
		for _, yt := range tgt.yieldTypes {
			fc.verifyAssignable(yt, expectedItemTy, fe, fe.Span, "`for` continue item")
		}
	}

	if fe.Else != nil {
		fc.pushScope()
		elseTy := fc.checkExprExpect(fe.Else, targetColTy, true)
		elseAd := fc.arenaDepthOfExpr(fe.Else)
		fc.popScope()
		fc.verifyAssignable(elseTy, targetColTy, fe.Else, fe.Else.GetSpan(), "`for ... else` fallback")
		fc.consumeValue(fe.Else, targetColTy, false)
		if elseAd > outerDepth {
			fc.a.Rep.Errorf(fe.Else.GetSpan(), "arena reference `ref[T]` cannot escape `for ... else` scope")
		} else if elseAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = elseAd
		}
	}

	if !needValue {
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}
	inf.YieldArenaDepth = tgt.maxYieldArenaDepth
	if typeMayHoldArenaRef(targetColTy) {
		fc.lowerPinnedArenasTo(outerDepth)
	}
	inf.Type = targetColTy
	return targetColTy
}

func cloneForIterSym(base *Symbol, suffix int, val *ctVal) *Symbol {
	if base == nil {
		return nil
	}
	s := *base
	s.CName = fmt.Sprintf("%s_cf_%d", types.SanitizeCIdent(base.Name), suffix)
	s.IsConst = true
	s.IsConstParam = true
	s.IsBind = true
	s.BindKind = BindReadBorrow
	s.ConstVal = val
	return &s
}

func countASTStmts(e ast.Expr) int {
	if e == nil {
		return 0
	}
	b, ok := e.(*ast.BlockExpr)
	if !ok {
		return 1
	}
	n := len(b.Stmts)
	if b.Tail != nil {
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}

func exprHasComptimeFeatures(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *ast.ComptimeExpr:
		return true
	case *ast.IdentExpr:
		return v.Name == "comp" || v.Name == "type"
	case *ast.UnaryExpr:
		return exprHasComptimeFeatures(v.Operand)
	case *ast.BinaryExpr:
		return exprHasComptimeFeatures(v.Left) || exprHasComptimeFeatures(v.Right)
	case *ast.MemberExpr:
		return len(v.BaseTypeArgs) > 0 || exprHasComptimeFeatures(v.Base)
	case *ast.CallExpr:
		if len(v.TypeArgs) > 0 || exprHasComptimeFeatures(v.Callee) {
			return true
		}
		for _, a := range v.Args {
			if exprHasComptimeFeatures(a) {
				return true
			}
		}
	case *ast.IndexExpr:
		return exprHasComptimeFeatures(v.Base) || exprHasComptimeFeatures(v.Index)
	case *ast.BlockExpr:
		for _, s := range v.Stmts {
			if stmtHasComptimeFeatures(s) {
				return true
			}
		}
		return exprHasComptimeFeatures(v.Tail)
	case *ast.StmtBodyExpr:
		return stmtHasComptimeFeatures(v.Stmt)
	case *ast.IfExpr:
		return v.IsComptime || exprHasComptimeFeatures(v.Cond) || exprHasComptimeFeatures(v.Then) || exprHasComptimeFeatures(v.Else)
	case *ast.ForExpr:
		return v.IsComptime || exprHasComptimeFeatures(v.CondOrIter) || exprHasComptimeFeatures(v.RangeEnd) || exprHasComptimeFeatures(v.Body) || exprHasComptimeFeatures(v.Else)
	case *ast.WhileExpr:
		return exprHasComptimeFeatures(v.CondOrIter) || exprHasComptimeFeatures(v.Body) || exprHasComptimeFeatures(v.Else)
	case *ast.MatchExpr:
		if v.IsComptime {
			return true
		}
		for _, a := range v.Arms {
			if exprHasComptimeFeatures(a.Body) {
				return true
			}
		}
	case *ast.StructLit:
		return len(v.TypeArgs) > 0
	case *ast.ArrayLit:
		return true
	}
	return false
}

func stmtHasComptimeFeatures(st ast.Stmt) bool {
	switch s := st.(type) {
	case *ast.VarStmt:
		return s.IsConst || exprHasComptimeFeatures(s.Value)
	case *ast.AssignStmt:
		return exprHasComptimeFeatures(s.Target) || exprHasComptimeFeatures(s.Value)
	case *ast.UnwindStmt:
		return exprHasComptimeFeatures(s.Value)
	case *ast.ExprStmt:
		return exprHasComptimeFeatures(s.Expr)
	}
	return false
}

func (fc *funcChecker) checkComptimeForExpr(fe *ast.ForExpr, expected types.Type, needValue bool) types.Type {
	inf := fc.a.infoFor(fe)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	allowVoid := fc.inferVoidOrValue
	fc.inferVoidOrValue = false

	if fe.Kind == ast.ForCond {
		fc.a.Rep.ErrorWithHint(
			fe.Span,
			"`comptime for` requires a static range (`i in lo..hi`) or compile-time collection (`x in items`)",
			"conditional `for (cond)` loops cannot be statically unrolled; use a runtime `for` loop",
		)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if fe.Var1Mut || fe.Var2Mut {
		fc.a.Rep.ErrorWithHint(
			fe.Span,
			"compile-time `for` loop variables cannot be mutable (`&`)",
			"remove `&` from the `comptime for` loop variable",
		)
	}

	h := loopHeaderInfo{
		kind:       fe.Kind,
		condOrIter: fe.CondOrIter,
		rangeEnd:   fe.RangeEnd,
		inclusive:  fe.Inclusive,
		var1:       fe.Var1,
		var1IsOpt:  fe.Var1IsOpt,
		var1Mut:    false,
		var2:       fe.Var2,
		var2IsOpt:  fe.Var2IsOpt,
		var2Mut:    false,
		span:       fe.Span,
	}

	maxUnroll := MaxComptimeUnrollIters
	if fc.curFn != nil && fc.curFn.ComptimeUnrollLimit > 0 {
		maxUnroll = fc.curFn.ComptimeUnrollLimit
	}
	if fc.a != nil && fc.a.ideMode && maxUnroll > defaultIDEComptimeMaxUnroll {
		maxUnroll = defaultIDEComptimeMaxUnroll
	}
	prevDepth := fc.comptimeExprDepth
	prevMin := fc.comptimeMinScope
	fc.comptimeExprDepth++
	if prevDepth == 0 {
		fc.comptimeMinScope = fc.curDepth() + 1
	}
	sym1, sym2, _, _ := fc.checkLoopHeader(h, id)
	items, ok := fc.a.evalComptimeForItems(fc, fe, sym1, sym2, maxUnroll)
	fc.comptimeExprDepth = prevDepth
	fc.comptimeMinScope = prevMin

	if !ok {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	perIterStmts := countASTStmts(fe.Body)
	if fc.unrolledStmts+len(items)*perIterStmts > maxUnroll {
		fc.a.Rep.ErrorWithHint(
			fe.Span,
			fmt.Sprintf("compile-time `for` unrolling exceeds function statement limit of %d", maxUnroll),
			"reduce the loop count, annotate the function with `@comptime_limit(...)`, or use a runtime `for` loop",
		)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	fc.unrolledStmts += len(items) * perIterStmts

	if !fc.inComptimeOnlyContext() && len(items) >= 16 && !exprHasComptimeFeatures(fe.Body) {
		fc.a.Rep.Warnf(
			fe.Span,
			"`comptime for` unrolls %d iterations of plain code into the binary; prefer a regular runtime `for` loop unless unrolling or compile-time specialization is intended",
			len(items),
		)
	}

	var evalTy types.Type
	if fe.EvalType != nil {
		evalTy = fc.a.resolveValueType(fc.mod, fe.EvalType, fc.subst, false)
		allowVoid = false
	}
	wantValue := needValue || evalTy != nil

	var targetColTy types.Type
	var expectedItemTy types.Type
	var collectFixedTy types.Type
	var collectStruct *types.Struct
	var collectTuple *types.Tuple
	var collectArr *types.StackArray
	var collectIndices []int
	isVectorInferred := false
	if wantValue {
		if evalTy != nil {
			if evalTy != types.TypeInvalid {
				switch ev := evalTy.(type) {
				case *types.Struct:
					if isRealStructType(ev) {
						collectFixedTy = ev
						collectStruct = ev
					}
				case *types.Tuple:
					collectFixedTy = ev
					collectTuple = ev
				case *types.StackArray:
					collectFixedTy = ev
					collectArr = ev
				}
			}
			if collectFixedTy != nil {
				targetColTy = collectFixedTy
			} else if evalTy != types.TypeInvalid && !fc.a.isCollectableType(evalTy) {
				fc.a.Rep.Errorf(fe.EvalType.GetSpan(), "type %s does not implement Collectable", evalTy.String())
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			} else {
				targetColTy = evalTy
				if evalTy != types.TypeInvalid {
					expectedItemTy = fc.a.collectableItemType(evalTy)
				}
			}
		} else if expected != nil && expected != types.TypeVoid && expected != types.TypeInvalid && fc.a.isCollectableType(expected) {
			targetColTy = expected
			expectedItemTy = fc.a.collectableItemType(expected)
		} else {
			isVectorInferred = true
		}
		if collectFixedTy == nil {
			if _, isSet := types.SetElem(targetColTy); isSet {
				fc.a.requireCollMethod(targetColTy, "insert", fe.Span)
			} else if _, _, isMap := types.MapKV(targetColTy); isMap {
				fc.a.requireCollMethod(targetColTy, "insert", fe.Span)
			}
		}
	}

	if collectFixedTy != nil {
		var needed int
		var kindDesc string
		switch {
		case collectStruct != nil:
			needed = len(collectStruct.Fields)
			kindDesc = "struct"
		case collectTuple != nil:
			needed = len(collectTuple.Elems)
			kindDesc = "tuple"
		case collectArr != nil:
			needed = int(collectArr.Size)
			kindDesc = "array"
		}
		if len(items) != needed {
			fc.a.Rep.Errorf(fe.Span, "%s-collecting `comptime for` over %d iterations does not match %s (%d slots)", kindDesc, len(items), collectFixedTy.String(), needed)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		collectIndices = make([]int, len(items))
		seenIdx := make(map[int]bool, needed)
		for i, it := range items {
			tIdx := i
			checkRefl := func(cv *ctVal) {
				if cv != nil && cv.kind == ctStruct && cv.st != nil && cv.st.refl != nil {
					if collectStruct != nil && cv.st.refl.Kind == "field" && cv.st.refl.TargetType.Equals(collectStruct) {
						tIdx = cv.st.refl.Index
					} else if (collectTuple != nil || collectArr != nil) && cv.st.refl.Kind == "elem" && cv.st.refl.TargetType.Equals(collectFixedTy) {
						tIdx = cv.st.refl.Index
					}
				}
			}
			if it.itemVal != nil {
				checkRefl(it.itemVal)
			} else {
				checkRefl(it.idxVal)
			}
			if tIdx < 0 || tIdx >= needed || seenIdx[tIdx] {
				fc.a.Rep.Errorf(fe.Span, "%s-collecting `comptime for` produced duplicate or out-of-range slot index %d for %s", kindDesc, tIdx, collectFixedTy.String())
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			seenIdx[tIdx] = true
			collectIndices[i] = tIdx
		}
		if fe.Else != nil {
			fc.a.Rep.Errorf(fe.Else.GetSpan(), "`else` branch is not supported on %s-collecting `comptime for`", kindDesc)
		}
	}

	outerDepth := fc.curDepth()
	tgt := &cfTarget{
		id:                 id,
		kind:               cfFor,
		label:              fe.Label,
		funcDepth:          fc.funcDepth,
		outerScopeDepth:    outerDepth,
		expectedType:       expectedItemTy,
		isCollecting:       wantValue,
		collectColType:     targetColTy,
		collectFixedTarget: collectFixedTy,
	}
	if !wantValue {
		tgt.expectedType = types.TypeVoid
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	unrolled := &UnrolledFor{
		Iters:         make([]UnrolledIter, 0, len(items)),
		CollectStruct: collectStruct,
		CollectTuple:  collectTuple,
		CollectArray:  collectArr,
	}
	hasBodyVoid := false
	hasBodyVal := false
	hasBodyInvalid := false

	for iterIdx, it := range items {
		iterTargetIdx := -1
		if collectFixedTy != nil {
			iterTargetIdx = collectIndices[iterIdx]
			switch {
			case collectStruct != nil:
				sf := &collectStruct.Fields[iterTargetIdx]
				tgt.collectStructField = sf
				expectedItemTy = sf.Type
				tgt.expectedType = sf.Type
			case collectTuple != nil:
				expectedItemTy = collectTuple.Elems[iterTargetIdx]
				tgt.expectedType = expectedItemTy
			case collectArr != nil:
				expectedItemTy = collectArr.Elem
				tgt.expectedType = expectedItemTy
			}
		}
		iterBody := ast.CloneExpr(fe.Body)
		iterSym1 := cloneForIterSym(sym1, fc.a.allocTargetID(), it.idxVal)
		iterSym2 := cloneForIterSym(sym2, fc.a.allocTargetID(), it.itemVal)

		fc.pushScope()
		if iterSym1 != nil {
			fc.scopes[len(fc.scopes)-1].vars[iterSym1.Name] = iterSym1
			fc.moves[iterSym1] = MoveAlive
			if fc.a.curBM != nil && it.idxVal != nil {
				fc.a.curBM.ConstVarVals[iterSym1] = it.idxVal
			}
		}
		if iterSym2 != nil {
			fc.scopes[len(fc.scopes)-1].vars[iterSym2.Name] = iterSym2
			fc.moves[iterSym2] = MoveAlive
			if fc.a.curBM != nil && it.itemVal != nil {
				fc.a.curBM.ConstVarVals[iterSym2] = it.itemVal
			}
		}

		var bty types.Type
		if !wantValue {
			bty = fc.checkVoidOrDiscardedBody(iterBody)
		} else {
			if isVectorInferred && expectedItemTy == nil {
				if allowVoid {
					bty = fc.checkInferOrValue(iterBody, nil)
				} else {
					bty = fc.checkExpr(iterBody, true)
				}
			} else if allowVoid && expectedItemTy == nil {
				bty = fc.checkInferOrValue(iterBody, nil)
			} else {
				bty = fc.checkExprExpect(iterBody, expectedItemTy, true)
			}

			switch {
			case bty == types.TypeVoid:
				hasBodyVoid = true
				if !allowVoid || expectedItemTy != nil {
					fc.a.Rep.Errorf(iterBody.GetSpan(), "`comptime for` expression producing a collection must evaluate to a non-void value")
					hasBodyInvalid = true
				}
			case bty == types.TypeInvalid:
				hasBodyInvalid = true
			case bty == types.TypeNever:
				if isVectorInferred && expectedItemTy == nil && len(tgt.yieldTypes) > 0 {
					if yt := unifyYieldTypes(tgt.yieldTypes); yt != types.TypeVoid && yt != types.TypeNever && yt != types.TypeInvalid {
						expectedItemTy = yt
						targetColTy = &types.Vector{Elem: yt}
						tgt.expectedType = expectedItemTy
						tgt.collectColType = targetColTy
					}
				}
			default:
				hasBodyVal = true
				if isVectorInferred && expectedItemTy == nil {
					expectedItemTy = bty
					targetColTy = &types.Vector{Elem: bty}
					tgt.expectedType = expectedItemTy
					tgt.collectColType = targetColTy
				}
				fc.verifyAssignable(bty, expectedItemTy, iterBody, iterBody.GetSpan(), "`comptime for` iteration item")
				fc.consumeValue(iterBody, expectedItemTy, false)
				bodyAd := fc.arenaDepthOfExpr(iterBody)
				if bodyAd > outerDepth {
					fc.a.Rep.Errorf(iterBody.GetSpan(), "arena reference `ref[T]` cannot escape `comptime for` loop scope")
				} else if bodyAd > tgt.maxYieldArenaDepth {
					tgt.maxYieldArenaDepth = bodyAd
				}
			}
		}

		fc.popScope()
		unrolled.Iters = append(unrolled.Iters, UnrolledIter{
			IdxSym:    iterSym1,
			ItemSym:   iterSym2,
			IdxVal:    it.idxVal,
			ItemVal:   it.itemVal,
			Body:      iterBody,
			TargetIdx: iterTargetIdx,
		})
	}

	if fc.a.curBM != nil {
		fc.a.curBM.ComptimeForUnrolls[fe] = unrolled
	}

	if !wantValue {
		if fe.Else != nil {
			fc.checkVoidOrDiscardedBody(fe.Else)
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	if hasBodyVoid && (hasBodyVal || len(tgt.yieldTypes) > 0 || len(tgt.breakColTypes) > 0) {
		fc.a.Rep.Errorf(fe.Span, "`comptime for` mixes `void` and value-producing iterations")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	if hasBodyInvalid {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	if isVectorInferred && targetColTy == nil {
		if len(tgt.yieldTypes) > 0 {
			if yt := unifyYieldTypes(tgt.yieldTypes); yt != types.TypeVoid && yt != types.TypeNever && yt != types.TypeInvalid {
				expectedItemTy = yt
				targetColTy = &types.Vector{Elem: yt}
				tgt.expectedType = yt
				tgt.collectColType = targetColTy
			}
		} else if !hasBodyVoid && len(tgt.breakColTypes) > 0 {
			if bt := unifyYieldTypes(tgt.breakColTypes); bt != types.TypeVoid && bt != types.TypeNever && bt != types.TypeInvalid {
				if !fc.a.isCollectableType(bt) {
					fc.a.Rep.Errorf(fe.Span, "type %s does not implement Collectable", bt.String())
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
				targetColTy = bt
				expectedItemTy = fc.a.collectableItemType(bt)
				tgt.collectColType = targetColTy
			}
		}
	}

	if isVectorInferred && targetColTy == nil {
		if hasBodyVoid {
			if fe.Else != nil {
				fc.pushScope()
				elseTy := fc.checkInferOrValue(fe.Else, nil)
				fc.popScope()
				if elseTy != types.TypeVoid && elseTy != types.TypeNever && elseTy != types.TypeInvalid {
					fc.a.Rep.Errorf(fe.Else.GetSpan(), "`comptime for ... else` branch must be `void` when body is `void`")
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
			}
			inf.Type = types.TypeVoid
			return types.TypeVoid
		}
		if fe.Else != nil {
			fc.pushScope()
			var elseTy types.Type
			if allowVoid && len(items) == 0 && len(tgt.yieldTypes) == 0 && len(tgt.breakColTypes) == 0 {
				elseTy = fc.checkInferOrValue(fe.Else, expected)
			} else {
				elseTy = fc.checkExprExpect(fe.Else, expected, true)
			}
			elseAd := fc.arenaDepthOfExpr(fe.Else)
			fc.popScope()
			if allowVoid && len(items) == 0 && (elseTy == types.TypeVoid || elseTy == types.TypeNever) {
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
			if elseTy != types.TypeInvalid && elseTy != types.TypeNever && fc.a.isCollectableType(elseTy) {
				targetColTy = elseTy
				if elseAd > outerDepth {
					fc.a.Rep.Errorf(fe.Else.GetSpan(), "arena reference `ref[T]` cannot escape `comptime for ... else` scope")
				} else if elseAd > tgt.maxYieldArenaDepth {
					tgt.maxYieldArenaDepth = elseAd
				}
			} else if elseTy != types.TypeInvalid {
				fc.a.Rep.Errorf(fe.Else.GetSpan(), "type %s does not implement Collectable", elseTy.String())
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
		}
		if targetColTy == nil {
			if allowVoid && len(items) == 0 {
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
			fc.a.Rep.ErrorWithHint(
				fe.Span,
				"cannot infer element type of empty or diverging `comptime for` comprehension",
				"specify an explicit target collection type like `comptime for (...) [int]:`",
			)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	} else if fe.Else != nil && collectFixedTy == nil {
		fc.pushScope()
		elseTy := fc.checkExprExpect(fe.Else, targetColTy, true)
		elseAd := fc.arenaDepthOfExpr(fe.Else)
		fc.popScope()
		fc.verifyAssignable(elseTy, targetColTy, fe.Else, fe.Else.GetSpan(), "`comptime for ... else` fallback")
		fc.consumeValue(fe.Else, targetColTy, false)
		if elseAd > outerDepth {
			fc.a.Rep.Errorf(fe.Else.GetSpan(), "arena reference `ref[T]` cannot escape `comptime for ... else` scope")
		} else if elseAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = elseAd
		}
	}

	if targetColTy != nil && collectFixedTy == nil {
		for _, bt := range tgt.breakColTypes {
			fc.verifyAssignable(bt, targetColTy, fe, fe.Span, "`comptime for` break value")
		}
	}
	if expectedItemTy != nil {
		for _, yt := range tgt.yieldTypes {
			fc.verifyAssignable(yt, expectedItemTy, fe, fe.Span, "`comptime for` continue item")
		}
	}

	if !needValue {
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}
	inf.YieldArenaDepth = tgt.maxYieldArenaDepth
	if typeMayHoldArenaRef(targetColTy) {
		fc.lowerPinnedArenasTo(outerDepth)
	}
	inf.Type = targetColTy
	return targetColTy
}

func (fc *funcChecker) checkWhileExpr(we *ast.WhileExpr, expected types.Type, needValue bool) types.Type {
	inf := fc.a.infoFor(we)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	allowVoid := fc.inferVoidOrValue
	fc.inferVoidOrValue = false

	h := loopHeaderInfo{
		kind:       we.Kind,
		condOrIter: we.CondOrIter,
		rangeEnd:   we.RangeEnd,
		inclusive:  we.Inclusive,
		var1:       we.Var1,
		var1IsOpt:  we.Var1IsOpt,
		var1Mut:    we.Var1Mut,
		var2:       we.Var2,
		var2IsOpt:  we.Var2IsOpt,
		var2Mut:    we.Var2Mut,
		span:       we.Span,
	}

	sym1, sym2, lockedCol, isInfiniteTrue := fc.checkLoopHeader(h, id)
	fc.a.curBM.WhileSymbols[we] = [2]*Symbol{sym1, sym2}

	var evalTy types.Type
	if we.EvalType != nil {
		evalTy = fc.a.resolveValueType(fc.mod, we.EvalType, fc.subst, false)
		allowVoid = false
		if we.Else == nil && !isInfiniteTrue && evalTy != nil && evalTy != types.TypeInvalid {
			if _, isOpt := evalTy.(*types.Optional); !isOpt {
				fc.a.Rep.ErrorWithHint(
					we.EvalType.GetSpan(),
					fmt.Sprintf("`while` loop without an `else` branch must have an optional evaluation type (got %s)", evalTy.String()),
					fmt.Sprintf("use `?%s`, add an `else` branch, or use `while (true)`", evalTy.String()),
				)
				evalTy = types.NewOptional(evalTy)
			}
		}
	}
	wantValue := needValue || evalTy != nil

	var elemExp types.Type
	if evalTy != nil && evalTy != types.TypeInvalid {
		elemExp = evalTy
	} else if needValue && expected != nil && expected != types.TypeVoid {
		if we.Else != nil {
			elemExp = expected
		} else if opt, ok := expected.(*types.Optional); ok {
			elemExp = opt.Elem
		} else {
			elemExp = expected
		}
	}

	var tgtExp types.Type
	if !wantValue {
		tgtExp = types.TypeVoid
	} else if evalTy != nil && evalTy != types.TypeInvalid {
		tgtExp = evalTy
	} else if elemExp != nil {
		if we.Else == nil {
			tgtExp = types.NewOptional(elemExp)
		} else {
			tgtExp = elemExp
		}
	}

	outerDepth := fc.curDepth()
	tgt := &cfTarget{
		id:              id,
		kind:            cfWhile,
		label:           we.Label,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: outerDepth,
		expectedType:    tgtExp,
		isForNoElse:     we.Else == nil && evalTy == nil,
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	preLoopMoves := fc.cloneMoves()
	loopScopeDepth := fc.curDepth() + 1

	fc.pushScope()
	if sym1 != nil {
		fc.scopes[len(fc.scopes)-1].vars[sym1.Name] = sym1
		fc.moves[sym1] = MoveAlive
	}
	if sym2 != nil {
		fc.scopes[len(fc.scopes)-1].vars[sym2.Name] = sym2
		fc.moves[sym2] = MoveAlive
	}
	if lockedCol != nil {
		fc.activeBorrows[lockedCol]++
	}

	var bodyTy types.Type
	if !wantValue {
		bodyTy = fc.checkVoidOrDiscardedBody(we.Body)
	} else if allowVoid && elemExp == nil {
		if isDefiniteStmtBody(we.Body) {
			bodyTy = fc.checkVoidOrDiscardedBody(we.Body)
		} else {
			bodyTy = fc.checkInferOrValue(we.Body, nil)
		}
	} else {
		bodyTy = fc.checkLoopBodyExpr(we.Body, elemExp)
	}

	if lockedCol != nil {
		fc.activeBorrows[lockedCol]--
	}
	if wantValue && bodyTy != types.TypeVoid && bodyTy != types.TypeNever && bodyTy != types.TypeInvalid {
		bodyAd := fc.arenaDepthOfExpr(we.Body)
		if bodyAd > outerDepth {
			fc.a.Rep.Errorf(we.Body.GetSpan(), "arena reference `ref[T]` cannot escape `while` loop scope")
		} else if bodyAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = bodyAd
		}
	}
	fc.popScope()

	for sym, preState := range preLoopMoves {
		if sym.ScopeDepth < loopScopeDepth && preState == MoveAlive && fc.moves[sym] != MoveAlive {
			fc.a.Rep.ErrorWithHint(
				we.Span,
				fmt.Sprintf("variable %q is moved inside a loop body without being reassigned before the next iteration", sym.Name),
				"reassign the variable on every path of the loop or `.copy()` it instead of moving",
			)
		}
	}
	postBodyMoves := fc.cloneMoves()

	if !wantValue {
		fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)
		if we.Else != nil {
			fc.checkVoidOrDiscardedBody(we.Else)
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	// Value-producing `while` loop!
	inferredElem := elemExp
	if inferredElem == nil {
		var cands []types.Type
		if bodyTy != types.TypeVoid && bodyTy != types.TypeNever && bodyTy != types.TypeInvalid {
			cands = append(cands, bodyTy)
		}
		cands = append(cands, tgt.yieldTypes...)
		if len(cands) > 0 {
			inferredElem = unifyYieldTypes(cands)
		}
	}
	if bodyTy != types.TypeVoid && bodyTy != types.TypeNever && bodyTy != types.TypeInvalid && inferredElem != nil {
		fc.verifyAssignable(bodyTy, inferredElem, we.Body, we.Body.GetSpan(), "`while` iteration value")
		fc.consumeValue(we.Body, inferredElem, false)
	}

	if evalTy != nil && evalTy != types.TypeInvalid {
		if we.Else != nil {
			fc.moves = fc.cloneMovesFrom(preLoopMoves)
			fc.pushScope()
			elseTy := fc.checkExprExpect(we.Else, evalTy, true)
			elseAd := fc.arenaDepthOfExpr(we.Else)
			if wantValue && elseAd > outerDepth {
				fc.a.Rep.Errorf(we.Else.GetSpan(), "arena reference `ref[T]` cannot escape `while ... else` scope")
			} else if elseAd > tgt.maxYieldArenaDepth {
				tgt.maxYieldArenaDepth = elseAd
			}
			fc.popScope()
			elseMoves := fc.cloneMoves()
			fc.moves = mergeMoveMaps(postBodyMoves, elseMoves)
			fc.verifyAssignable(elseTy, evalTy, we.Else, we.Else.GetSpan(), "`while ... else` fallback")
			fc.consumeValue(we.Else, evalTy, false)
			if !needValue {
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
			inf.YieldArenaDepth = tgt.maxYieldArenaDepth
			if typeMayHoldArenaRef(evalTy) {
				fc.lowerPinnedArenasTo(outerDepth)
			}
			inf.Type = evalTy
			return evalTy
		} else {
			fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)
			if !needValue {
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
			inf.YieldArenaDepth = tgt.maxYieldArenaDepth
			if typeMayHoldArenaRef(evalTy) {
				fc.lowerPinnedArenasTo(outerDepth)
			}
			inf.Type = evalTy
			return evalTy
		}
	}

	if we.Else == nil {
		fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)
		if inferredElem == nil {
			if allowVoid {
				if bodyTy == types.TypeInvalid {
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
				inf.Type = types.TypeVoid
				return types.TypeVoid
			}
			fc.a.Rep.Errorf(we.Span, "`while` loop used as a value produces no iteration value and has no `while <- val` exit")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		resOpt := types.NewOptional(inferredElem)
		inf.YieldArenaDepth = tgt.maxYieldArenaDepth
		if typeMayHoldArenaRef(resOpt) {
			fc.lowerPinnedArenasTo(outerDepth)
		}
		inf.Type = resOpt
		return resOpt
	}

	// Has `else` clause -> guaranteed type `T`!
	fc.moves = fc.cloneMovesFrom(preLoopMoves)
	fc.pushScope()
	var elseTy types.Type
	if allowVoid && inferredElem == nil {
		elseTy = fc.checkInferOrValue(we.Else, nil)
	} else {
		elseTy = fc.checkExprExpect(we.Else, inferredElem, true)
	}
	elseAd := fc.arenaDepthOfExpr(we.Else)
	if elseAd > outerDepth {
		fc.a.Rep.Errorf(we.Else.GetSpan(), "arena reference `ref[T]` cannot escape `while ... else` scope")
	} else if elseAd > tgt.maxYieldArenaDepth {
		tgt.maxYieldArenaDepth = elseAd
	}
	fc.popScope()
	elseMoves := fc.cloneMoves()
	fc.moves = mergeMoveMaps(postBodyMoves, elseMoves)

	if inferredElem == nil {
		if allowVoid && (elseTy == types.TypeVoid || elseTy == types.TypeNever) {
			inf.Type = types.TypeVoid
			return types.TypeVoid
		}
		inferredElem = elseTy
	}
	if inferredElem == nil || inferredElem == types.TypeVoid {
		fc.a.Rep.Errorf(we.Span, "`while ... else` used as a value must produce a non-void value")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	fc.verifyAssignable(elseTy, inferredElem, we.Else, we.Else.GetSpan(), "`while ... else` fallback")
	fc.consumeValue(we.Else, inferredElem, false)
	inf.YieldArenaDepth = tgt.maxYieldArenaDepth
	if typeMayHoldArenaRef(inferredElem) {
		fc.lowerPinnedArenasTo(outerDepth)
	}
	inf.Type = inferredElem
	return inferredElem
}

// checkLoopBodyExpr allows a value-position `while` loop body to EITHER evaluate to an iteration value `T`
// OR be a `void` search body that uses `while <- val`!
func (fc *funcChecker) checkLoopBodyExpr(body ast.Expr, elemExp types.Type) types.Type {
	_, elemIsOpt := elemExp.(*types.Optional)
	if (!elemIsOpt && isSearchStmtBody(body)) || isDefiniteStmtBody(body) {
		return fc.checkVoidOrDiscardedBody(body)
	}
	return fc.checkExprExpect(body, elemExp, true)
}

func isDefiniteStmtBody(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.StmtBodyExpr:
		return true
	case *ast.IfExpr:
		return v.Else == nil && isDefiniteStmtBody(v.Then)
	case *ast.ForExpr:
		return v.Else == nil && isDefiniteStmtBody(v.Body)
	case *ast.WhileExpr:
		return v.Else == nil && isDefiniteStmtBody(v.Body)
	case *ast.BlockExpr:
		return v.Tail == nil || isDefiniteStmtBody(v.Tail)
	case *ast.UnsafeExpr:
		return isDefiniteStmtBody(v.Expr)
	default:
		return false
	}
}

func isSearchStmtBody(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.StmtBodyExpr:
		return true
	case *ast.IfExpr:
		return v.Else == nil
	case *ast.ForExpr:
		return v.Else == nil
	case *ast.WhileExpr:
		return v.Else == nil
	case *ast.BlockExpr:
		return v.Tail == nil || isSearchStmtBody(v.Tail)
	case *ast.UnsafeExpr:
		return isSearchStmtBody(v.Expr)
	default:
		return false
	}
}

func (fc *funcChecker) checkMatchExpr(me *ast.MatchExpr, expected types.Type, needValue bool) types.Type {
	if me.IsComptime {
		return fc.checkComptimeMatchExpr(me, expected, needValue)
	}
	inf := fc.a.infoFor(me)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	allowVoid := fc.inferVoidOrValue
	fc.inferVoidOrValue = false

	numScrut := len(me.Scrutinees)
	scrutTys := make([]types.Type, numScrut)
	scrutRoots := make([]*Symbol, numScrut)
	scrutArenaDepths := make([]int, numScrut)
	isExplicitOwnScrut := make([]bool, numScrut)
	isRefScrut := make([]bool, numScrut)
	enumScrut := make([]*types.Enum, numScrut)

	for i, s := range me.Scrutinees {
		st := fc.checkExpr(s, true)
		scrutRoots[i] = fc.rootSymbolOfExpr(s)
		scrutArenaDepths[i] = fc.arenaDepthOfExpr(s)
		if u, isOwn := s.(*ast.UnaryExpr); isOwn && u.Op == token.KwOwn {
			isExplicitOwnScrut[i] = true
			fc.consumeValue(s, st, false)
		}
		if rf, ok := st.(*types.Ref); ok {
			if _, isEn := rf.Elem.(*types.Enum); isEn {
				st = rf.Elem
				isRefScrut[i] = true
			}
		}
		if en, isEn := st.(*types.Enum); isEn {
			enumScrut[i] = en
		}
		scrutTys[i] = st
	}

	if me.ResultChain {
		wantsValue := needValue && !allowVoid && me.EvalType == nil && expected != types.TypeVoid
		expanded, ok := fc.prepareResultChain(me, scrutTys[0], wantsValue)
		if !ok {
			return types.TypeInvalid
		}
		fc.a.curBM.MatchArms[me] = expanded
		if en := types.AsResult(scrutTys[0]); en != nil && en.IsUnitResult() && wantsValue {
			fc.a.Rep.ErrorWithHint(me.Scrutinees[0].GetSpan(),
				fmt.Sprintf("a chain on %s has no value: it is a unit result", en.String()),
				"write the chain as a statement; its arms handle the failures")
			return types.TypeInvalid
		}
	}

	var evalTy types.Type
	if me.EvalType != nil {
		evalTy = fc.a.resolveValueType(fc.mod, me.EvalType, fc.subst, false)
		allowVoid = false
	}
	wantValue := needValue || evalTy != nil

	var armExp types.Type
	if evalTy != nil && evalTy != types.TypeInvalid {
		armExp = evalTy
	} else if needValue && expected != nil && expected != types.TypeVoid {
		armExp = expected
	}

	outerDepth := fc.curDepth()
	tgt := &cfTarget{
		id:              id,
		kind:            cfMatch,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: outerDepth,
		expectedType:    armExp,
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	coveredVariants1 := make(map[string]bool)
	hasCatchAllElse := false

	preMoves := fc.cloneMoves()
	var liveBranchMoves []map[*Symbol]MoveState
	var armTypes []types.Type
	allDiverge := true
	hasVoidArm := false
	hasNonVoidArm := false
	hasInvalidArm := false

	for _, arm := range fc.matchArms(me) {
		if arm.IsDefault && len(arm.Cases) == 0 {
			hasCatchAllElse = true
		}

		fc.moves = fc.cloneMovesFrom(preMoves)
		fc.pushScope()

		hasBindings := false
		var activeRoots []*Symbol
		rootMutCount := make(map[*Symbol]int)
		rootOwnCount := make(map[*Symbol]int)
		rootReadCount := make(map[*Symbol]int)

		if numScrut == 0 {
			for _, c := range arm.Cases {
				if len(c.Patterns) != 1 {
					fc.a.Rep.Errorf(c.Span, "expected 1 condition in 0-argument match branch, got %d", len(c.Patterns))
					continue
				}
				ep, ok := c.Patterns[0].(*ast.ExprPattern)
				if !ok {
					fc.a.Rep.Errorf(c.Span, "expected boolean condition expression in 0-argument match branch")
					continue
				}
				cTy := fc.checkExpr(ep.Expr, true)
				fc.verifyAssignable(cTy, types.TypeBool, ep.Expr, ep.Span, "match condition")
			}
		} else {
			for _, c := range arm.Cases {
				if len(c.Patterns) != numScrut {
					fc.a.Rep.Errorf(c.Span, "match case has %d patterns, expected %d", len(c.Patterns), numScrut)
					continue
				}
				for i, pat := range c.Patterns {
					var covMap map[string]bool
					if numScrut == 1 {
						covMap = coveredVariants1
					}
					pHasBind, pHasMut, pHasOwnRes := fc.checkMatchPattern(pat, scrutTys[i], enumScrut[i], scrutArenaDepths[i], covMap)
					if pHasBind {
						hasBindings = true
					}
					if pHasMut {
						inf.MatchMutScrut = true
						if !isRefScrut[i] {
							_ = fc.checkMutablePlace(me.Scrutinees[i], "mutably destructure")
						}
					}
					if pHasOwnRes {
						if isRefScrut[i] {
							fc.a.Rep.Errorf(arm.Span, "cannot move `own` resource payload out of arena reference `ref[%s]`", scrutTys[i].String())
						} else if enumScrut[i] != nil && enumScrut[i].HasCustomDrop {
							fc.a.Rep.Errorf(arm.Span, "cannot move `own` payload out of enum %s because it implements custom `Drop`", enumScrut[i].String())
						} else if !isExplicitOwnScrut[i] {
							fc.consumeValue(me.Scrutinees[i], scrutTys[i], false)
						}
					}
					r := scrutRoots[i]
					if r != nil {
						if pHasMut {
							rootMutCount[r]++
						}
						if pHasOwnRes {
							rootOwnCount[r]++
						}
						if !pHasMut && !pHasOwnRes && pHasBind {
							rootReadCount[r]++
						}
						if pHasMut || (!pHasOwnRes && pHasBind) {
							hasActive := false
							for _, ar := range activeRoots {
								if ar == r {
									hasActive = true
									break
								}
							}
							if !hasActive {
								activeRoots = append(activeRoots, r)
							}
						}
					}
				}
			}
			if len(arm.Cases) > 1 && hasBindings {
				fc.a.Rep.Errorf(arm.Span, "cannot bind variables in a multi-pattern arm; use `_` wildcards or separate `when` arms")
			}
			for r, mc := range rootMutCount {
				if mc > 1 || (mc > 0 && (rootOwnCount[r] > 0 || rootReadCount[r] > 0)) {
					fc.a.Rep.Errorf(arm.Span, "conflicting borrows/moves of variable %q in `match` arm", r.Name)
				}
			}
			for r, oc := range rootOwnCount {
				if oc > 1 || (oc > 0 && rootReadCount[r] > 0) {
					fc.a.Rep.Errorf(arm.Span, "conflicting borrows/moves of variable %q in `match` arm", r.Name)
				}
			}
		}

		for _, r := range activeRoots {
			fc.activeBorrows[r]++
		}

		var aty types.Type
		if !wantValue {
			aty = fc.checkVoidOrDiscardedBody(arm.Body)
		} else {
			hint := armExp
			if hint == nil && len(armTypes) > 0 {
				hint = armTypes[0]
				if _, isNone := arm.Body.(*ast.NoneLit); isNone {
					hint = types.NewOptional(hint)
				}
			}
			if allowVoid && hint == nil {
				aty = fc.checkInferOrValue(arm.Body, nil)
			} else {
				aty = fc.checkExprExpect(arm.Body, hint, true)
			}
			if aty == types.TypeVoid {
				if allowVoid && armExp == nil {
					hasVoidArm = true
				} else {
					fc.a.Rep.Errorf(arm.Body.GetSpan(), "`match` arm used as a value must produce a value")
				}
			} else if aty == types.TypeInvalid {
				hasInvalidArm = true
			} else if aty != types.TypeNever {
				hasNonVoidArm = true
				armTypes = append(armTypes, aty)
				fc.consumeValue(arm.Body, aty, false)
			}
		}

		for _, r := range activeRoots {
			fc.activeBorrows[r]--
		}

		armAd := fc.arenaDepthOfExpr(arm.Body)
		if wantValue && aty != types.TypeVoid && aty != types.TypeNever && armAd > outerDepth {
			fc.a.Rep.Errorf(arm.Body.GetSpan(), "arena reference `ref[T]` cannot escape `match` arm scope")
		} else if wantValue && aty != types.TypeVoid && aty != types.TypeNever && armAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = armAd
		}
		fc.popScope()

		if aty != types.TypeNever {
			allDiverge = false
			liveBranchMoves = append(liveBranchMoves, fc.cloneMoves())
		}
	}

	fc.checkMatchExhaustiveness(me, numScrut, hasCatchAllElse, coveredVariants1, scrutTys, enumScrut)

	if len(liveBranchMoves) > 0 {
		fc.moves = mergeMoveMaps(liveBranchMoves...)
	}

	if !wantValue {
		if allDiverge && !tgt.hitValueExit {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	if allowVoid && armExp == nil {
		if hasVoidArm && (hasNonVoidArm || len(tgt.yieldTypes) > 0) {
			fc.a.Rep.Errorf(me.Span, "`match` expression mixes `void` and value-producing arms")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if hasInvalidArm && !hasNonVoidArm && len(tgt.yieldTypes) == 0 {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if hasVoidArm {
			inf.Type = types.TypeVoid
			return types.TypeVoid
		}
	}

	resTy := armExp
	if resTy == nil {
		resTy = unifyYieldTypes(append(tgt.yieldTypes, armTypes...))
	}
	for _, arm := range fc.matchArms(me) {
		aty := fc.a.infoFor(arm.Body).Type
		if aty != nil && aty != types.TypeVoid && aty != types.TypeNever && aty != types.TypeInvalid {
			fc.verifyAssignable(aty, resTy, arm.Body, arm.Body.GetSpan(), "`match` arm")
		}
	}
	if !needValue {
		if allDiverge && !tgt.hitValueExit {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}
	if allDiverge && !tgt.hitValueExit {
		inf.Type = types.TypeNever
		return types.TypeNever
	}
	inf.YieldArenaDepth = tgt.maxYieldArenaDepth
	if typeMayHoldArenaRef(resTy) {
		fc.lowerPinnedArenasTo(outerDepth)
	}
	inf.Type = resTy
	return resTy
}

func (fc *funcChecker) checkMatchExhaustiveness(me *ast.MatchExpr, numScrut int, hasCatchAllElse bool, coveredVariants1 map[string]bool, scrutTys []types.Type, enumScrut []*types.Enum) {
	if len(fc.matchArms(me)) == 0 || !fc.matchArms(me)[len(fc.matchArms(me))-1].IsDefault {
		fc.a.Rep.Errorf(fc.a.matchHeaderSpan(me), "non-exhaustive `match` expression: must terminate with a `default` arm")
	}
	if hasCatchAllElse {
		return
	}
	if numScrut == 0 {
		fc.a.Rep.Errorf(me.Span, "0-argument `match` expression must end with an open catch-all `default:` arm")
		return
	}
	if numScrut == 1 {
		scrutTy := scrutTys[0]
		es := enumScrut[0]
		if coveredVariants1["*"] {
			return
		}
		if es == nil {
			if scrutTy.Equals(types.TypeBool) {
				var missingBool []string
				if !coveredVariants1["true"] {
					missingBool = append(missingBool, "true")
				}
				if !coveredVariants1["false"] {
					missingBool = append(missingBool, "false")
				}
				if len(missingBool) > 0 {
					fc.a.Rep.ErrorWithHint(fc.a.matchHeaderSpan(me), fmt.Sprintf("non-exhaustive `match` on `bool`: missing %s", strings.Join(missingBool, ", ")),
						"add the missing `when` arm or a `default:` arm (the quick fix can add it)")
				}
			} else if scrutTy != types.TypeInvalid {
				fc.a.Rep.Errorf(me.Span, "`match` on non-enum type %s must end with an open catch-all `default:` arm", scrutTy.String())
			}
			return
		}
		var missing []string
		hasUnexported := false
		for _, v := range es.Variants {
			if !IsExported(v.Name) {
				hasUnexported = true
			}
			if !coveredVariants1[v.Name] {
				missing = append(missing, es.Name+"."+v.Name)
			}
		}
		if len(missing) > 0 {
			samePkg := fc.a.SamePackageDir(fc.mod, es.PkgDir, "") || fc.a.SamePackage(fc.mod, fc.a.typeModule(es))
			if !samePkg && hasUnexported {
				fc.a.Rep.Errorf(fc.a.matchHeaderSpan(me), "non-exhaustive `match` on enum %s: enum declares unexported variants, requiring a `default:` catch-all arm when matched outside its package", es.String())
			} else if me.ResultChain {
				var bare []string
				for _, m := range missing {
					bare = append(bare, "`"+strings.TrimPrefix(m, es.Name+".")+"`")
				}
				fc.a.Rep.ErrorWithHint(fc.a.matchHeaderSpan(me),
					fmt.Sprintf("non-exhaustive `match` on enum %s: missing variant(s): %s",
						es.String(), strings.Join(missing, ", ")),
					fmt.Sprintf("this result chain does not handle %s; add `when` arms for them, or end with `default:` or `default panic` (the quick fix can add the arms)", strings.Join(bare, ", ")))
			} else {
				fc.a.Rep.ErrorWithHint(fc.a.matchHeaderSpan(me),
					fmt.Sprintf("non-exhaustive `match` on enum %s: missing variant(s): %s",
						es.String(), strings.Join(missing, ", ")),
					"add a `when` arm for each missing variant, or end with a catch-all `default:` (the quick fix can add the arms)")
			}
		}
		return
	}
	allEnumOrBool := true
	for i := 0; i < numScrut; i++ {
		if enumScrut[i] == nil && !scrutTys[i].Equals(types.TypeBool) {
			allEnumOrBool = false
			break
		}
	}
	if !allEnumOrBool {
		fc.a.Rep.Errorf(me.Span, "multi-argument `match` on non-enum/bool types must end with an open catch-all `default:` arm")
	} else {
		fc.checkMultiArgExhaustiveness(me, scrutTys, enumScrut)
	}
}

func (fc *funcChecker) checkComptimeMatchExpr(me *ast.MatchExpr, expected types.Type, needValue bool) types.Type {
	inf := fc.a.infoFor(me)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	allowVoid := fc.inferVoidOrValue
	fc.inferVoidOrValue = false

	var evalTy types.Type
	if me.EvalType != nil {
		evalTy = fc.a.resolveValueType(fc.mod, me.EvalType, fc.subst, false)
		allowVoid = false
	}
	wantValue := needValue || evalTy != nil

	var armExp types.Type
	if evalTy != nil && evalTy != types.TypeInvalid {
		armExp = evalTy
	} else if needValue && expected != nil && expected != types.TypeVoid {
		armExp = expected
	}

	// Phase 1: check scrutinees, all arm patterns, and full exhaustiveness in strict compile-time scope.
	prevDepth := fc.comptimeExprDepth
	prevMin := fc.comptimeMinScope
	fc.comptimeExprDepth++
	if prevDepth == 0 {
		fc.comptimeMinScope = fc.curDepth() + 1
	}

	numScrut := len(me.Scrutinees)
	scrutTys := make([]types.Type, numScrut)
	scrutArenaDepths := make([]int, numScrut)
	enumScrut := make([]*types.Enum, numScrut)
	for i, s := range me.Scrutinees {
		st := fc.checkExpr(s, true)
		scrutArenaDepths[i] = fc.arenaDepthOfExpr(s)
		if rf, ok := st.(*types.Ref); ok {
			if _, isEn := rf.Elem.(*types.Enum); isEn {
				st = rf.Elem
			}
		}
		if en, isEn := st.(*types.Enum); isEn {
			enumScrut[i] = en
		}
		scrutTys[i] = st
	}

	if me.ResultChain {
		wantsValue := needValue && !allowVoid && me.EvalType == nil && expected != types.TypeVoid
		expanded, ok := fc.prepareResultChain(me, scrutTys[0], wantsValue)
		if !ok {
			fc.comptimeExprDepth = prevDepth
			fc.comptimeMinScope = prevMin
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		fc.a.curBM.MatchArms[me] = expanded
		if en := types.AsResult(scrutTys[0]); en != nil && en.IsUnitResult() && wantsValue {
			fc.a.Rep.ErrorWithHint(me.Scrutinees[0].GetSpan(),
				fmt.Sprintf("a chain on %s has no value: it is a unit result", en.String()),
				"write the chain as a statement; its arms handle the failures")
			fc.comptimeExprDepth = prevDepth
			fc.comptimeMinScope = prevMin
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	}

	arms := fc.matchArms(me)
	coveredVariants1 := make(map[string]bool)
	hasCatchAllElse := false
	armVars := make([]map[string]*Symbol, len(arms))

	for armIdx, arm := range arms {
		if arm.IsDefault && len(arm.Cases) == 0 {
			hasCatchAllElse = true
		}
		fc.pushScope()
		hasBindings := false
		if numScrut == 0 {
			for _, c := range arm.Cases {
				if len(c.Patterns) != 1 {
					fc.a.Rep.Errorf(c.Span, "expected 1 condition in 0-argument `comptime match` branch, got %d", len(c.Patterns))
					continue
				}
				ep, ok := c.Patterns[0].(*ast.ExprPattern)
				if !ok {
					fc.a.Rep.Errorf(c.Span, "expected boolean condition expression in 0-argument `comptime match` branch")
					continue
				}
				cTy := fc.checkExprExpect(ep.Expr, types.TypeBool, true)
				fc.verifyAssignable(cTy, types.TypeBool, ep.Expr, ep.Span, "match condition")
			}
		} else {
			for _, c := range arm.Cases {
				if len(c.Patterns) != numScrut {
					fc.a.Rep.Errorf(c.Span, "match case has %d patterns, expected %d", len(c.Patterns), numScrut)
					continue
				}
				for i, pat := range c.Patterns {
					var covMap map[string]bool
					if numScrut == 1 {
						covMap = coveredVariants1
					}
					pHasBind, pHasMut, _ := fc.checkMatchPattern(pat, scrutTys[i], enumScrut[i], scrutArenaDepths[i], covMap)
					if pHasBind {
						hasBindings = true
					}
					if pHasMut {
						fc.a.Rep.ErrorWithHint(
							arm.Span,
							"cannot mutably destructure (`&`) a `comptime match` scrutinee",
							"bind the payload immutably (`when Variant(v):`) instead of `&v`",
						)
					}
				}
			}
			if len(arm.Cases) > 1 && hasBindings {
				fc.a.Rep.Errorf(arm.Span, "cannot bind variables in a multi-pattern arm; use `_` wildcards or separate `when` arms")
			}
		}
		armVars[armIdx] = fc.scopes[len(fc.scopes)-1].vars
		fc.popScope()
	}

	fc.checkMatchExhaustiveness(me, numScrut, hasCatchAllElse, coveredVariants1, scrutTys, enumScrut)

	// Phase 2: evaluate scrutinees and match at compile time to select ONE arm.
	matchedIdx, boundVals, ok := fc.a.evalComptimeMatchSelect(fc, me, me.Scrutinees)
	fc.comptimeExprDepth = prevDepth
	fc.comptimeMinScope = prevMin
	if !ok || matchedIdx < 0 || matchedIdx >= len(arms) {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if fc.a.curBM != nil {
		fc.a.curBM.ComptimeMatchArm[me] = matchedIdx
	}

	// Phase 3: check ONLY the chosen arm body with its bound payload variables treated as compile-time constants.
	outerDepth := fc.curDepth()
	tgt := &cfTarget{
		id:              id,
		kind:            cfMatch,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: outerDepth,
		expectedType:    armExp,
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	chosenArm := arms[matchedIdx]
	fc.pushScope()
	for name, sym := range armVars[matchedIdx] {
		sym.ScopeDepth = fc.curDepth()
		sym.IsConst = true
		sym.IsConstParam = true
		sym.IsBind = true
		sym.BindKind = BindReadBorrow
		if types.FindComptimeOnlyType(sym.Type) != nil {
			sym.ComptimeOnly = true
		}
		if bv, hasBV := boundVals[sym]; hasBV && bv != nil {
			freezeCtVal(bv)
			sym.ConstVal = bv
			if fc.a.curBM != nil {
				fc.a.curBM.ConstVarVals[sym] = bv
			}
		}
		fc.scopes[len(fc.scopes)-1].vars[name] = sym
		fc.moves[sym] = MoveAlive
	}

	var aty types.Type
	if !wantValue {
		aty = fc.checkVoidOrDiscardedBody(chosenArm.Body)
	} else if allowVoid && armExp == nil {
		aty = fc.checkInferOrValue(chosenArm.Body, nil)
	} else {
		aty = fc.checkExprExpect(chosenArm.Body, armExp, true)
	}
	armAd := fc.arenaDepthOfExpr(chosenArm.Body)
	fc.popScope()

	if !wantValue {
		if aty == types.TypeNever && !tgt.hitValueExit {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	if allowVoid && armExp == nil && aty == types.TypeVoid {
		if len(tgt.yieldTypes) > 0 {
			fc.a.Rep.Errorf(me.Span, "`comptime match` arm yields a value via `match <-` on some paths, but falls through with `void`")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}
	if aty == types.TypeVoid {
		fc.a.Rep.Errorf(chosenArm.Body.GetSpan(), "`comptime match` arm used as a value must produce a value")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if aty != types.TypeNever && aty != types.TypeInvalid {
		if armAd > outerDepth {
			fc.a.Rep.Errorf(chosenArm.Body.GetSpan(), "arena reference `ref[T]` cannot escape `comptime match` arm scope")
		} else if armAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = armAd
		}
	}

	resTy := armExp
	if resTy == nil {
		resTy = unifyYieldTypes(append(tgt.yieldTypes, aty))
	} else if aty != types.TypeNever && aty != types.TypeInvalid {
		fc.verifyAssignable(aty, armExp, chosenArm.Body, chosenArm.Body.GetSpan(), "`comptime match` arm")
		fc.consumeValue(chosenArm.Body, armExp, false)
	}
	if armExp == nil && aty != types.TypeVoid && aty != types.TypeNever && aty != types.TypeInvalid {
		fc.verifyAssignable(aty, resTy, chosenArm.Body, chosenArm.Body.GetSpan(), "`comptime match` arm")
		fc.consumeValue(chosenArm.Body, resTy, false)
	}
	if aty == types.TypeNever && !tgt.hitValueExit {
		inf.Type = types.TypeNever
		return types.TypeNever
	}
	inf.YieldArenaDepth = tgt.maxYieldArenaDepth
	if typeMayHoldArenaRef(resTy) {
		fc.lowerPinnedArenasTo(outerDepth)
	}
	inf.Type = resTy
	return resTy
}

func (fc *funcChecker) checkMultiArgExhaustiveness(me *ast.MatchExpr, scrutTys []types.Type, enumScrut []*types.Enum) {
	numScrut := len(scrutTys)
	slotVariants := make([][]string, numScrut)
	for i := 0; i < numScrut; i++ {
		if enumScrut[i] != nil {
			for _, v := range enumScrut[i].Variants {
				slotVariants[i] = append(slotVariants[i], v.Name)
			}
		} else {
			slotVariants[i] = []string{"true", "false"}
		}
	}

	var combinations [][]string
	var gen func(idx int, cur []string)
	gen = func(idx int, cur []string) {
		if idx == numScrut {
			combo := make([]string, len(cur))
			copy(combo, cur)
			combinations = append(combinations, combo)
			return
		}
		for _, v := range slotVariants[idx] {
			gen(idx+1, append(cur, v))
		}
	}
	gen(0, nil)

	patMatches := func(pat ast.Pattern, vName string) bool {
		switch p := pat.(type) {
		case *ast.WildcardPattern:
			return true
		case *ast.ExprPattern:
			if bl, ok := p.Expr.(*ast.BoolLit); ok {
				return (bl.Val && vName == "true") || (!bl.Val && vName == "false")
			}
			if inf := fc.a.infoFor(p.Expr); inf.EnumVariant != nil {
				return inf.EnumVariant.Name == vName
			}
			return false
		case *ast.VariantPattern:
			if mem, ok := p.Target.(*ast.MemberExpr); ok {
				if vr := fc.a.infoFor(mem).EnumVariant; vr != nil {
					return vr.Name == vName
				}
			}
			return false
		case *ast.MethodPattern:
			if fc.a.curBM != nil {
				if vp := fc.a.curBM.BareVariantPatterns[p]; vp != nil {
					if mem, ok := vp.Target.(*ast.MemberExpr); ok {
						if vr := fc.a.infoFor(mem).EnumVariant; vr != nil {
							return vr.Name == vName
						}
					}
				}
			}
			return false
		default:
			return false
		}
	}

	caseMatchesCombo := func(c ast.MatchCase, combo []string) bool {
		if len(c.Patterns) != len(combo) {
			return false
		}
		for i := 0; i < len(combo); i++ {
			if !patMatches(c.Patterns[i], combo[i]) {
				return false
			}
		}
		return true
	}

	var missing []string
	for _, combo := range combinations {
		covered := false
		for _, arm := range fc.matchArms(me) {
			for _, c := range arm.Cases {
				if caseMatchesCombo(c, combo) {
					covered = true
					break
				}
			}
			if covered {
				break
			}
		}
		if !covered {
			var parts []string
			for i, v := range combo {
				if enumScrut[i] != nil {
					parts = append(parts, enumScrut[i].Name+"."+v)
				} else {
					parts = append(parts, v)
				}
			}
			missing = append(missing, strings.Join(parts, ", "))
		}
	}

	if len(missing) > 0 {
		fc.a.Rep.Errorf(fc.a.matchHeaderSpan(me), "non-exhaustive `match`: missing combination(s): %s", strings.Join(missing, "; "))
	}
}

func extractVariantBindingFromExpr(arg ast.Expr) (ast.VariantPatternBinding, bool) {
	switch v := arg.(type) {
	case *ast.IdentExpr:
		if v.Name == "self" {
			return ast.VariantPatternBinding{}, false
		}
		if v.Name == "_" {
			return ast.VariantPatternBinding{
				IsWildcard: true,
				Span:       v.Span,
			}, true
		}
		return ast.VariantPatternBinding{
			BindName:  v.Name,
			IsOptName: v.IsOpt,
			Mode:      ast.PatternBindRead,
			Span:      v.Span,
		}, true
	case *ast.UnaryExpr:
		var mode ast.PatternBindMode
		switch v.Op {
		case token.Amp:
			mode = ast.PatternBindMut
		case token.KwOwn:
			mode = ast.PatternBindOwn
		default:
			return ast.VariantPatternBinding{}, false
		}
		id, ok := v.Operand.(*ast.IdentExpr)
		if !ok || id.Name == "self" || id.Name == "_" {
			return ast.VariantPatternBinding{}, false
		}
		return ast.VariantPatternBinding{
			BindName:  id.Name,
			IsOptName: id.IsOpt,
			Mode:      mode,
			Span:      v.Span,
		}, true
	default:
		return ast.VariantPatternBinding{}, false
	}
}

func (a *Analyzer) hasInstanceMethodOnEnum(en *types.Enum, name string) bool {
	if en == nil {
		return false
	}
	if mset, ok := a.methods[TypeMethodKey(en)]; ok {
		if fi, ok2 := mset[name]; ok2 && !fi.IsStaticAssoc {
			return true
		}
	}
	if gset, ok := a.genMethods[en.Name]; ok {
		if gtmpl, ok2 := gset[name]; ok2 && gtmpl != nil && gtmpl.MethodDecl != nil && gtmpl.MethodDecl.RecvMode != ast.ReceiverMode(-1) {
			return true
		}
	}
	return false
}

func (fc *funcChecker) declareVariantPatternBinding(b ast.VariantPatternBinding, fty types.Type, scrutArenaDepth int) (sym *Symbol, hasBind, hasMut, hasOwnRes bool) {
	if b.IsWildcard {
		return nil, false, false, false
	}
	hasBind = true
	fc.a.checkOptionalIdentRule(b.BindName, b.IsOptName, fty, b.Span, "pattern binding")
	bkind := BindReadBorrow
	isBind := true
	switch b.Mode {
	case ast.PatternBindMut:
		hasMut = true
		bkind = BindMutBorrow
		isBind = false
	case ast.PatternBindOwn:
		bkind = BindOwned
		isBind = false // owned local binding
		if !fty.IsPlain() {
			hasOwnRes = true
		}
	case ast.PatternBindRead:
		if fty.IsPlain() {
			bkind = BindOwned
		} else {
			bkind = BindReadBorrow
		}
		isBind = true
	}
	sym = fc.declareLocal(b.BindName, fty, isBind, bkind, b.Span)
	if typeMayHoldArenaRef(fty) {
		sym.ArenaDepth = scrutArenaDepth
	}
	return sym, hasBind, hasMut, hasOwnRes
}

func (fc *funcChecker) checkBareUnitEnumPattern(id *ast.IdentExpr, pSpan diag.Span, enumScrut *types.Enum, covered map[string]bool) bool {
	samePkg := fc.a.SamePackageDir(fc.mod, enumScrut.PkgDir, "") || fc.a.SamePackage(fc.mod, fc.a.typeModule(enumScrut))
	v, isVariant := enumScrut.VariantByName(id.Name)
	sym := fc.lookupVar(id.Name)
	if isVariant {
		if sym != nil {
			if sym.IsTopLevel && fc.a.inBodyPass {
				fc.a.resolveTopVar(sym)
			}
			if sym.Type != nil && sym.Type != types.TypeInvalid && sym.Type.Equals(enumScrut) {
				fc.a.Rep.ErrorWithHint(
					pSpan,
					fmt.Sprintf("pattern %q is ambiguous: it names both a variant of %s and a variable in scope", id.Name, enumScrut.String()),
					fmt.Sprintf("write `%s.%s` for the enum variant", enumScrut.Name, id.Name),
				)
				fc.a.infoFor(id).Type = types.TypeInvalid
				return true
			}
		}
		if !samePkg && !IsExported(v.Name) {
			fc.a.Rep.Errorf(pSpan, "cannot match unexported enum variant %q of %s outside its package", v.Name, enumScrut.String())
			fc.a.infoFor(id).Type = types.TypeInvalid
			return true
		}
		if covered != nil {
			if covered[v.Name] {
				fc.a.Rep.Errorf(pSpan, "duplicate enum variant `%s.%s` in `match`", enumScrut.Name, v.Name)
			}
			covered[v.Name] = true
		}
		if v.Kind != types.VariantUnit {
			fc.a.Rep.Errorf(pSpan, "variant `%s.%s` carries a payload of type %s; match with `%s(val)` or `%s(_)`",
				enumScrut.String(), v.Name, v.Payload.String(), v.Name, v.Name)
			fc.a.infoFor(id).Type = types.TypeInvalid
			return true
		}
		inf := fc.a.infoFor(id)
		inf.EnumType = enumScrut
		inf.EnumVariant = v
		inf.Type = enumScrut
		return true
	}
	if sym == nil {
		var variantNames []string
		for _, ev := range enumScrut.Variants {
			if samePkg || IsExported(ev.Name) {
				variantNames = append(variantNames, ev.Name)
			}
		}
		hint := ""
		var cands []string
		if c := closestNames(id.Name, variantNames); len(c) > 0 {
			hint = fmt.Sprintf("did you mean `%s`?", strings.Join(c, "` or `"))
			cands = c
		} else {
			hint, cands = fc.unknownNameHint(id.Name)
		}
		fc.a.errorWithRename(id.Span, fmt.Sprintf("enum %s has no variant %q", enumScrut.String(), id.Name), id.Name, hint, cands)
		fc.a.infoFor(id).Type = types.TypeInvalid
		return true
	}
	return false
}

func (fc *funcChecker) checkBarePayloadEnumPattern(p *ast.MethodPattern, enumScrut *types.Enum, v *types.EnumVariant, scrutArenaDepth int, covered map[string]bool) (hasBind, hasMut, hasOwnRes bool) {
	if fc.a.hasInstanceMethodOnEnum(enumScrut, p.Method) {
		fc.a.Rep.ErrorWithHint(
			p.Span,
			fmt.Sprintf("pattern `%s(...)` is ambiguous: it names both a variant and a method of %s", p.Method, enumScrut.String()),
			fmt.Sprintf("write `%s.%s(...)` for the enum variant", enumScrut.Name, p.Method),
		)
		return false, false, false
	}
	samePkg := fc.a.SamePackageDir(fc.mod, enumScrut.PkgDir, "") || fc.a.SamePackage(fc.mod, fc.a.typeModule(enumScrut))
	if !samePkg && !IsExported(v.Name) {
		fc.a.Rep.Errorf(p.Span, "cannot match unexported enum variant %q of %s outside its package", v.Name, enumScrut.String())
		return false, false, false
	}
	if covered != nil {
		if covered[v.Name] {
			fc.a.Rep.Errorf(p.Span, "duplicate enum variant `%s.%s` in `match`", enumScrut.Name, v.Name)
		}
		covered[v.Name] = true
	}
	if v.Kind == types.VariantUnit {
		fc.a.Rep.Errorf(p.Span, "variant `%s.%s` has no payload; match with `when %s:`", enumScrut.String(), v.Name, v.Name)
		return false, false, false
	}
	if len(p.Args) != 1 {
		fc.a.Rep.Errorf(p.Span, "variant `%s.%s` expects 1 payload binding, got %d", enumScrut.String(), v.Name, len(p.Args))
		return false, false, false
	}
	b, ok := extractVariantBindingFromExpr(p.Args[0])
	if !ok {
		fc.a.Rep.Errorf(p.Span, "invalid payload binding in variant pattern `%s(...)`: expected a variable name (optionally `&name` or `own name`) or `_`", p.Method)
		return false, false, false
	}
	placeholder := &ast.IdentExpr{Span: diag.Span{Start: p.MethodSpan.Start, End: p.MethodSpan.Start}}
	targetMem := &ast.MemberExpr{
		Base:   placeholder,
		Member: p.Method,
		Span:   p.MethodSpan,
	}
	vp := &ast.VariantPattern{
		Target:     targetMem,
		Bindings:   []ast.VariantPatternBinding{b},
		Contextual: true,
		Span:       p.Span,
	}
	fc.a.infoFor(targetMem).EnumType = enumScrut
	fc.a.infoFor(targetMem).EnumVariant = v
	fc.a.infoFor(targetMem).Type = enumScrut
	sym, hasBind, hasMut, hasOwnRes := fc.declareVariantPatternBinding(b, v.Payload, scrutArenaDepth)
	if fc.a.curBM != nil {
		fc.a.curBM.PatternSyms[vp] = []*Symbol{sym}
		fc.a.curBM.BareVariantPatterns[p] = vp
	}
	return hasBind, hasMut, hasOwnRes
}

func (fc *funcChecker) checkMatchPattern(pat ast.Pattern, scrutTy types.Type, enumScrut *types.Enum, scrutArenaDepth int, covered map[string]bool) (hasBind, hasMut, hasOwnRes bool) {
	switch p := pat.(type) {
	case *ast.WildcardPattern:
		if covered != nil {
			covered["*"] = true
		}
		return false, false, false

	case *ast.ExprPattern:
		if enumScrut != nil {
			if id, isId := p.Expr.(*ast.IdentExpr); isId && !id.IsOpt && id.Name != "self" {
				if fc.checkBareUnitEnumPattern(id, p.Span, enumScrut, covered) {
					return false, false, false
				}
			}
		}
		pty := fc.checkExprExpect(p.Expr, scrutTy, true)
		if scrutTy == types.TypeInvalid || pty == types.TypeInvalid {
			return false, false, false
		}
		if _, isRange := p.Expr.(*ast.RangeExpr); isRange {
			scrutBase := scrutTy
			if opt, isOpt := scrutBase.(*types.Optional); isOpt {
				scrutBase = opt.Elem
			}
			isRangeScrut := false
			if st, isSt := scrutBase.(*types.Struct); isSt && fc.a.isStdStructTemplate(st, fc.a.Prog.GenStructRange) {
				isRangeScrut = true
			}
			if !isRangeScrut {
				fc.a.Rep.ErrorWithHint(
					p.Span,
					fmt.Sprintf("type mismatch in `match` pattern: expected `%s`, got `%s`", scrutTy.String(), pty.String()),
					"use `when in ...:` to check membership in a range (for example, `when in '0'..='9':`)",
				)
				return false, false, false
			}
		}
		fc.verifyAssignable(pty, scrutTy, p.Expr, p.Span, "`match` pattern")
		if _, isNone := p.Expr.(*ast.NoneLit); !isNone && fc.a.infoFor(p.Expr).EnumVariant == nil && !fc.a.supportsEq(scrutTy) {
			fc.a.Rep.Errorf(p.Span, "cannot match value pattern of type `%s` because it does not support `==`", scrutTy.String())
		}
		if enumScrut != nil {
			if inf := fc.a.infoFor(p.Expr); inf.EnumVariant != nil {
				vname := inf.EnumVariant.Name
				if covered != nil {
					if covered[vname] {
						fc.a.Rep.Errorf(p.Span, "duplicate enum variant `%s.%s` in `match`", enumScrut.Name, vname)
					}
					covered[vname] = true
				}
			}
		} else if scrutTy.Equals(types.TypeBool) {
			if bl, ok := p.Expr.(*ast.BoolLit); ok {
				bkey := "false"
				if bl.Val {
					bkey = "true"
				}
				if covered != nil {
					if covered[bkey] {
						fc.a.Rep.Errorf(p.Span, "duplicate boolean pattern `%s` in `match`", bkey)
					}
					covered[bkey] = true
				}
			}
		} else if sl, ok := p.Expr.(*ast.StringLit); ok {
			key := "str:" + sl.Val
			if covered != nil {
				if covered[key] {
					fc.a.Rep.Errorf(p.Span, "duplicate string pattern %q in `match`", sl.Val)
				}
				covered[key] = true
			}
		} else if il, ok := p.Expr.(*ast.IntLit); ok {
			key := fmt.Sprintf("int:%d", il.Val)
			if covered != nil {
				if covered[key] {
					fc.a.Rep.Errorf(p.Span, "duplicate integer pattern `%d` in `match`", il.Val)
				}
				covered[key] = true
			}
		} else if u, ok := p.Expr.(*ast.UnaryExpr); ok && u.Op == token.Minus {
			if il, ok := u.Operand.(*ast.IntLit); ok {
				key := fmt.Sprintf("int:-%d", il.Val)
				if covered != nil {
					if covered[key] {
						fc.a.Rep.Errorf(p.Span, "duplicate integer pattern `-%d` in `match`", il.Val)
					}
					covered[key] = true
				}
			}
		} else if cl, ok := p.Expr.(*ast.CharLit); ok {
			key := fmt.Sprintf("char:%d", cl.Val)
			if covered != nil {
				if covered[key] {
					fc.a.Rep.Errorf(p.Span, "duplicate character pattern '%c' in `match`", cl.Val)
				}
				covered[key] = true
			}
		} else if _, ok := p.Expr.(*ast.NoneLit); ok {
			key := "none"
			if covered != nil {
				if covered[key] {
					fc.a.Rep.Errorf(p.Span, "duplicate `none` pattern in `match`")
				}
				covered[key] = true
			}
		}
		return false, false, false

	case *ast.ContainsPattern:
		var expect types.Type
		if scrutTy != nil && scrutTy != types.TypeInvalid {
			switch p.Container.(type) {
			case *ast.RangeExpr:
				if fc.a.Prog.GenStructRange != nil {
					if prim := types.UnderlyingPrimitive(scrutTy); prim != nil && (prim.IsInteger() || prim.Kind == types.PrimChar) {
						if rTy := fc.a.instantiateStruct(fc.a.Prog.GenStructRange, []types.Type{scrutTy}, p.Span); rTy != nil {
							expect = rTy
						}
					}
				}
			case *ast.VecLit:
				expect = &types.Vector{Elem: scrutTy}
			case *ast.ArrayLit:
				expect = &types.StackArray{Size: 1, Elem: scrutTy}
			case *ast.SetLit:
				expect = fc.a.setType(scrutTy, p.Span)
			}
		}
		contTy := fc.checkExprExpect(p.Container, expect, true)
		if contTy == types.TypeInvalid || scrutTy == types.TypeInvalid {
			return false, false, false
		}
		if cinfo, ok := fc.resolveContainsPattern(p, contTy, scrutTy); ok && fc.a.curBM != nil {
			fc.a.curBM.ContainsPatterns[p] = cinfo
		}
		return false, false, false

	case *ast.MethodPattern:
		if scrutTy == nil || scrutTy == types.TypeInvalid {
			for _, a := range p.Args {
				_ = fc.checkExpr(a, true)
			}
			return false, false, false
		}
		if enumScrut != nil && len(p.TypeArgs) == 0 {
			if v, isVariant := enumScrut.VariantByName(p.Method); isVariant {
				return fc.checkBarePayloadEnumPattern(p, enumScrut, v, scrutArenaDepth, covered)
			}
		}
		if _, isOpt := scrutTy.(*types.Optional); isOpt {
			fc.a.Rep.Errorf(p.Span, "cannot call method `%s` on optional scrutinee of type `%s` in `match` pattern", p.Method, scrutTy.String())
			for _, a := range p.Args {
				_ = fc.checkExpr(a, true)
			}
			return false, false, false
		}
		recvPlaceholder := &ast.IdentExpr{
			Name: "<scrutinee>",
			Span: diag.Span{Start: diag.Pos{Offset: -1}, End: diag.Pos{Offset: -1}},
		}
		fc.a.infoFor(recvPlaceholder).Type = scrutTy
		mem := &ast.MemberExpr{
			Base:   recvPlaceholder,
			Member: p.Method,
			Span:   p.MethodSpan,
		}
		call := &ast.CallExpr{
			Callee:   mem,
			TypeArgs: p.TypeArgs,
			Args:     p.Args,
			Span:     p.Span,
		}
		retTy := fc.checkInstanceMethodCall(scrutTy, mem, call, types.TypeBool, true)
		if retTy != types.TypeInvalid && !retTy.Equals(types.TypeBool) {
			fc.a.Rep.Errorf(p.Span, "method `%s.%s` in `match` pattern must return `bool`, got `%s`", scrutTy.String(), p.Method, retTy.String())
		} else if retTy.Equals(types.TypeBool) && fc.a.curBM != nil {
			fc.a.curBM.MethodPatterns[p] = &MethodPatternInfo{
				Call:     call,
				RecvExpr: recvPlaceholder,
			}
		}
		return false, false, false

	case *ast.VariantPattern:
		if scrutTy == types.TypeInvalid {
			return false, false, false
		}
		if enumScrut == nil {
			fc.a.Rep.Errorf(p.Span, "variant destructuring pattern requires an enum scrutinee, got %s", scrutTy.String())
			return false, false, false
		}
		mem, isMem := p.Target.(*ast.MemberExpr)
		if !isMem {
			fc.a.Rep.Errorf(p.Span, "invalid variant pattern target")
			return false, false, false
		}
		if p.Contextual {
			return fc.checkContextualFailurePattern(p, mem, enumScrut, scrutArenaDepth, covered)
		}
		targetEn, genEn, matched := fc.tryResolveEnumMemberTarget(mem, enumScrut)
		if !matched || targetEn == nil {
			if genEn != nil {
				targetEn = enumScrut
			} else {
				fc.a.Rep.Errorf(p.Span, "pattern does not name a variant of enum %s", enumScrut.String())
				return false, false, false
			}
		}
		if !targetEn.Equals(enumScrut) {
			fc.a.Rep.Errorf(p.Span, "pattern enum %s does not match scrutinee type %s", targetEn.String(), enumScrut.String())
			return false, false, false
		}
		v, ok := enumScrut.VariantByName(mem.Member)
		if !ok {
			fc.a.Rep.Errorf(p.Span, "enum %s has no variant %q", enumScrut.String(), mem.Member)
			return false, false, false
		}
		if enumScrut.IsComptimeOnly && !fc.inComptimeOnlyContext() {
			fc.a.Rep.ErrorWithHint(
				p.Span,
				fmt.Sprintf("cannot match `@comptime_only` enum %s outside a compile-time context", enumScrut.Name),
				"use it inside `comptime`, `comptime match`, a `const` initializer, or another `@comptime_only` function",
			)
		}
		samePkg := fc.a.SamePackageDir(fc.mod, enumScrut.PkgDir, "") || fc.a.SamePackage(fc.mod, fc.a.typeModule(enumScrut))
		if !samePkg && !IsExported(mem.Member) {
			fc.a.Rep.Errorf(p.Span, "cannot match unexported enum variant %q of %s outside its package", mem.Member, enumScrut.String())
			return false, false, false
		}
		fc.a.infoFor(mem).EnumType = enumScrut
		fc.a.infoFor(mem).EnumVariant = v
		fc.a.infoFor(mem).Type = enumScrut

		if covered != nil {
			if covered[v.Name] {
				fc.a.Rep.Errorf(p.Span, "duplicate enum variant `%s.%s` in `match`", enumScrut.Name, v.Name)
			}
			covered[v.Name] = true
		}

		syms := make([]*Symbol, len(p.Bindings))
		if v.Kind == types.VariantUnit {
			fc.a.Rep.Errorf(p.Span, "variant `%s.%s` has no payload; match with `when %s.%s:`", enumScrut.String(), v.Name, enumScrut.Name, v.Name)
			return false, false, false
		}
		if len(p.Bindings) != 1 {
			fc.a.Rep.Errorf(p.Span, "variant `%s.%s` expects 1 payload binding, got %d", enumScrut.String(), v.Name, len(p.Bindings))
			return false, false, false
		}
		syms[0], hasBind, hasMut, hasOwnRes = fc.declareVariantPatternBinding(p.Bindings[0], v.Payload, scrutArenaDepth)
		if hasMut && hasOwnRes {
			fc.a.Rep.Errorf(p.Span, "cannot mix mutable borrow (`&`) and ownership move (`own`) in the same variant pattern")
		}
		fc.a.curBM.PatternSyms[p] = syms
		return hasBind, hasMut, hasOwnRes
	}
	return false, false, false
}

func (fc *funcChecker) resolveContainsPattern(p *ast.ContainsPattern, contTy, scrutTy types.Type) (*ContainsInfo, bool) {
	if _, isOpt := contTy.(*types.Optional); isOpt {
		fc.a.Rep.Errorf(p.Span, "cannot use optional value of type `%s` in `when in` pattern", contTy.String())
		return nil, false
	}

	fc.markViewIfApplicable(p.Container, contTy)
	info := &ContainsInfo{
		ContainerType: contTy,
	}

	switch c := types.CollView(contTy).(type) {
	case *types.Vector:
		if !fc.a.supportsEq(c.Elem) {
			fc.a.Rep.Errorf(p.Span, "cannot use `when in` with `%s` because element type `%s` does not support equality", c.String(), c.Elem.String())
			return nil, false
		}
		info.Kind = ContainsBuiltinVec
		info.ElemType = c.Elem

	case *types.StackArray:
		if !fc.a.supportsEq(c.Elem) {
			fc.a.Rep.Errorf(p.Span, "cannot use `when in` with `%s` because element type `%s` does not support equality", c.String(), c.Elem.String())
			return nil, false
		}
		info.Kind = ContainsBuiltinArr
		info.ElemType = c.Elem

	case *types.Set:
		fc.a.requireCollMethod(contTy, "has", p.Span)
		info.Kind = ContainsBuiltinSet
		info.ElemType = c.Elem

	case *types.Map:
		fc.a.requireCollMethod(contTy, "has", p.Span)
		info.Kind = ContainsBuiltinMap
		info.ElemType = c.Key

	case *types.Interface:
		var cMeth *types.InterfaceMethod
		for i := range c.Methods {
			if c.Methods[i].Name == "contains" {
				cMeth = &c.Methods[i]
				break
			}
		}
		if cMeth == nil {
			fc.a.Rep.Errorf(p.Span, "interface `%s` does not implement `Containable` (`contains: fn(self, item: T) bool`)", c.String())
			return nil, false
		}
		if cMeth.RecvMode != types.ModeRead || len(cMeth.Params) != 1 || cMeth.Params[0].Mode != types.ModeRead || !cMeth.Ret.Equals(types.TypeBool) {
			fc.a.Rep.Errorf(p.Span, "`%s.contains` must have signature `(self, item: T) bool` to use with `when in`", c.Name)
			return nil, false
		}
		if _, isSelf := cMeth.Params[0].Type.(*types.SelfPlaceholder); isSelf {
			fc.a.Rep.Errorf(p.Span, "cannot use interface `%s` in `when in` because `contains` takes `Self`", c.Name)
			return nil, false
		}
		info.Kind = ContainsInterface
		info.InterfaceMeth = cMeth
		info.BoxInterface = c
		info.ElemType = cMeth.Params[0].Type

	default:
		if st, isSt := contTy.(*types.Struct); isSt &&
			(fc.a.isStdStructTemplate(st, fc.a.Prog.GenStructList) || fc.a.isStdStructTemplate(st, fc.a.Prog.GenStructArray)) &&
			len(st.TypeArgs) == 1 && !fc.a.supportsEq(st.TypeArgs[0]) {
			fc.a.Rep.Errorf(p.Span, "cannot use `when in` with `%s` because element type `%s` does not support equality", st.String(), st.TypeArgs[0].String())
			return nil, false
		}
		fi := fc.a.LookupMethod(contTy, "contains", nil, p.Span)
		if fi == nil {
			fc.a.Rep.Errorf(p.Span, "type `%s` does not implement `Containable` (`contains: fn(self, item: T) bool`)", contTy.String())
			return nil, false
		}
		if fi.IsStaticAssoc || len(fi.Sig.Params) != 2 || fi.Sig.Params[0].Mode != types.ModeRead || fi.Sig.Params[1].Mode != types.ModeRead {
			fc.a.Rep.Errorf(p.Span, "`%s.contains` must have signature `(self, item: T) bool` to use with `when in`", contTy.String())
			return nil, false
		}
		retTy := fc.a.ensureFuncRetResolved(fi, p.Span)
		if retTy == nil || !retTy.Equals(types.TypeBool) {
			retStr := "invalid"
			if retTy != nil {
				retStr = retTy.String()
			}
			fc.a.Rep.Errorf(p.Span, "`%s.contains` must return `bool` for `when in`, got `%s`", contTy.String(), retStr)
			return nil, false
		}
		info.Kind = ContainsMethod
		info.TargetFunc = fi
		fc.checkFuncConstAndComptimeAccess(fi, p.Span)
		info.ElemType = fi.Sig.Params[1].Type
	}

	if opt, isOpt := info.ElemType.(*types.Optional); isOpt && scrutTy.Equals(opt.Elem) {
		info.WrapOptional = true
	} else if !scrutTy.Equals(info.ElemType) {
		fc.a.Rep.Errorf(p.Span, "type mismatch in `when in` pattern: container holds `%s`, cannot test value of type `%s`", info.ElemType.String(), scrutTy.String())
		return nil, false
	}
	return info, true
}

// resultChainValueName is the hidden local bound to a result chain's success payload by the
// implicit `value` arm.
const resultChainValueName = "_ll_chain_value"

// resultChainFailureName binds an unhandled failure's Error in a `default panic` arm.
const resultChainFailureName = "_ll_chain_failure"

// prepareResultChain validates a result chain's subject and expands it into an ordinary match:
// it prepends the implicit `value` arm (evaluating to the success payload) and, for
// `default panic`, appends one panicking arm per failure variant no user arm handles. The
// expanded arms are returned (and recorded in BodyMaps.MatchArms); the AST is not modified.
//
// A chain whose subject is a borrowed variable (a read-only parameter or binding) doesn't consume
// it: the success value is bound as a read-only borrow. Such a chain can produce its value only
// when the value type is plain; otherwise it must be a statement (`wantsValue` false).
func (fc *funcChecker) prepareResultChain(me *ast.MatchExpr, subjTy types.Type, wantsValue bool) ([]ast.MatchArm, bool) {
	if subjTy == types.TypeInvalid {
		return nil, false
	}
	en := types.AsResult(subjTy)
	if en == nil {
		hint := "result chains (`expr: when Failure(...): ... default ...`) apply only to types declared with `define Name result [...]`"
		if _, isOpt := subjTy.(*types.Optional); isOpt {
			hint = "for an optional, use `??` or `if (x? as v)`"
		}
		fc.a.Rep.ErrorWithHint(me.Scrutinees[0].GetSpan(),
			fmt.Sprintf("result chain subject must be a result type, got %s", subjTy.String()), hint)
		return nil, false
	}
	valueVar, _ := en.ResultValue()
	if valueVar == nil {
		return nil, false // malformed declaration, already reported
	}

	handled := make(map[string]bool)
	for _, arm := range me.Arms {
		for _, c := range arm.Cases {
			for _, pat := range c.Patterns {
				vp, ok := pat.(*ast.VariantPattern)
				if !ok || !vp.Contextual {
					continue
				}
				name := vp.Target.(*ast.MemberExpr).Member
				if name == types.ResultValueName {
					fc.a.Rep.ErrorWithHint(vp.Span,
						"a result chain cannot match `value`",
						"the chain itself evaluates to the value; its arms handle only failures")
					return nil, false
				}
				handled[name] = true
			}
		}
	}

	at := me.Scrutinees[0].GetSpan()
	placeholder := func(span diag.Span) *ast.MemberExpr {
		return &ast.MemberExpr{
			Base: &ast.IdentExpr{Span: diag.Span{Start: span.Start, End: span.Start}},
			Span: span,
		}
	}
	valueTarget := placeholder(at)
	valueTarget.Member = types.ResultValueName
	valuePat := &ast.VariantPattern{Target: valueTarget, Contextual: true, Span: at}
	// A unit result's chain evaluates to nothing: its `value` arm is an empty block.
	var valueBody ast.Expr = &ast.BlockExpr{Span: at}
	borrowed := fc.isBorrowedVariable(me.Scrutinees[0])
	if borrowed && valueVar.Payload != nil && !valueVar.Payload.IsPlain() && wantsValue {
		name := exprSourceName(me.Scrutinees[0])
		fc.a.Rep.ErrorWithHint(me.Scrutinees[0].GetSpan(),
			fmt.Sprintf("a chain on borrowed `%s` can't produce its %s value without moving it", name, valueVar.Payload.String()),
			fmt.Sprintf("chain on `%s.copy()` for an owned value, or read it in place with `if(%s as v)` or `match`", name, name))
		return nil, false
	}
	if borrowed && valueVar.Payload != nil && !wantsValue {
		// A statement chain on a borrowed subject only handles failures.
		valuePat.Bindings = []ast.VariantPatternBinding{{IsWildcard: true, Span: at}}
	} else if valueVar.Payload != nil {
		bindName := resultChainValueName
		if types.AsResult(valueVar.Payload) != nil {
			bindName += "!" // nested results keep the `!` naming invariant
		}
		mode := ast.PatternBindRead
		if !valueVar.Payload.IsPlain() && !borrowed {
			mode = ast.PatternBindOwn
		}
		valuePat.Bindings = []ast.VariantPatternBinding{{BindName: bindName, Mode: mode, Span: at}}
		valueBody = &ast.IdentExpr{Name: bindName, Span: at}
	}
	valueArm := ast.MatchArm{
		Cases:     []ast.MatchCase{{Patterns: []ast.Pattern{valuePat}, Span: at}},
		Body:      valueBody,
		Synthetic: true,
		Span:      at,
	}
	arms := append([]ast.MatchArm{valueArm}, me.Arms...)

	if me.DefaultPanic {
		var rest []*types.EnumVariant
		for _, f := range en.ResultFailures() {
			if !handled[f.Name] {
				rest = append(rest, f)
			}
		}
		if len(rest) == 0 {
			fc.a.Rep.ErrorWithHint(me.DefaultPanicSpan,
				fmt.Sprintf("`default panic` is unreachable: every failure of %s is already handled", en.String()),
				"make the last arm `default <Failure>:` instead")
			return nil, false
		}
		ps := me.DefaultPanicSpan
		for i, f := range rest {
			target := placeholder(ps)
			target.Member = f.Name
			vp := &ast.VariantPattern{Target: target, Contextual: true, Span: ps}
			msg := fmt.Sprintf("unhandled failure %s.%s", en.Name, f.Name)
			var panicArg ast.Expr = &ast.StringLit{Val: msg, Span: ps}
			if f.Kind != types.VariantUnit {
				vp.Bindings = []ast.VariantPatternBinding{{IsWildcard: true, Span: ps}}
				if fc.isCoreError(f.Payload) {
					// Panic with the failure's own Error, so its message, causes, and trace print.
					vp.Bindings = []ast.VariantPatternBinding{{BindName: resultChainFailureName, Mode: ast.PatternBindRead, Span: ps}}
					copied := &ast.CallExpr{
						Callee: &ast.MemberExpr{Base: &ast.IdentExpr{Name: resultChainFailureName, Span: ps}, Member: "copy", Span: ps},
						Span:   ps,
					}
					panicArg = &ast.CallExpr{
						Callee: &ast.MemberExpr{Base: copied, Member: "wrap", Span: ps},
						Args:   []ast.Expr{&ast.StringLit{Val: msg, Span: ps}},
						Span:   ps,
					}
				}
			}
			arms = append(arms, ast.MatchArm{
				Cases:     []ast.MatchCase{{Patterns: []ast.Pattern{vp}, Span: ps}},
				IsDefault: i == len(rest)-1,
				Body: &ast.CallExpr{
					Callee: &ast.IdentExpr{Name: "panic", Span: ps},
					Args:   []ast.Expr{panicArg},
					Span:   ps,
				},
				Synthetic: true,
				Span:      ps,
			})
		}
	}
	return arms, true
}

// checkContextualFailurePattern checks a bare result-chain pattern (`Missing(why)`, `Forbidden`)
// against the chain's result type `en`.
func (fc *funcChecker) checkContextualFailurePattern(p *ast.VariantPattern, mem *ast.MemberExpr, en *types.Enum, scrutArenaDepth int, covered map[string]bool) (hasBind, hasMut, hasOwnRes bool) {
	v, ok := en.VariantByName(mem.Member)
	if !ok {
		fc.a.Rep.Errorf(p.Span, "result %s has no failure variant %q", en.String(), mem.Member)
		return false, false, false
	}
	fc.a.infoFor(mem).EnumType = en
	fc.a.infoFor(mem).EnumVariant = v
	fc.a.infoFor(mem).Type = en
	if covered != nil {
		if covered[v.Name] {
			fc.a.Rep.Errorf(p.Span, "duplicate failure `%s` in result chain", v.Name)
		}
		covered[v.Name] = true
	}
	if v.Kind == types.VariantUnit {
		if len(p.Bindings) > 0 {
			fc.a.Rep.Errorf(p.Span, "failure `%s` has no payload; write `when %s:`", v.Name, v.Name)
		}
		fc.a.curBM.PatternSyms[p] = nil
		return false, false, false
	}
	if len(p.Bindings) != 1 {
		fc.a.Rep.Errorf(p.Span, "failure `%s` carries a %s payload; write `%s(name)` or `%s(_)`", v.Name, v.Payload.String(), v.Name, v.Name)
		return false, false, false
	}
	b := p.Bindings[0]
	syms := make([]*Symbol, 1)
	syms[0], hasBind, hasMut, hasOwnRes = fc.declareVariantPatternBinding(b, v.Payload, scrutArenaDepth)
	fc.a.curBM.PatternSyms[p] = syms
	return hasBind, hasMut, hasOwnRes
}

// checkResultFallback checks `r! ?? fallback` where `r!` is a result: the success value, or
// `fallback` (of the value type, or an unwind) on any failure, which is discarded.
func (fc *funcChecker) checkResultFallback(b *ast.BinaryExpr, en *types.Enum, lty types.Type) types.Type {
	inf := fc.a.infoFor(b)
	vv, _ := en.ResultValue()
	if vv == nil || vv.Payload == nil {
		if vv != nil {
			fc.reportBadFallbackSubject(b.Left.GetSpan(), lty)
		}
		_ = fc.checkExpr(b.Right, true)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	elem := vv.Payload
	preRightMoves := fc.cloneMoves()
	rty := fc.checkExprExpect(b.Right, elem, true)
	if rty == types.TypeNever {
		fc.moves = preRightMoves
	}
	fc.consumeValue(b.Left, lty, false)
	if rty != types.TypeNever && rty != types.TypeInvalid && !rty.Equals(elem) {
		fc.verifyAssignable(rty, elem, b.Right, b.Right.GetSpan(), "`??` fallback")
	}
	fc.consumeValue(b.Right, elem, false)
	inf.Type = elem
	return elem
}

// isCoreError reports whether t is std/core's Error.
func (fc *funcChecker) isCoreError(t types.Type) bool {
	if fc.a.coreMod == nil || t == nil {
		return false
	}
	errTy, ok := fc.a.coreMod.Types["Error"]
	return ok && errTy != nil && t.Equals(errTy)
}

// isBorrowedVariable reports whether e names a variable the function doesn't own: a read-only
// parameter or binding, or an immutable top-level constant.
func (fc *funcChecker) isBorrowedVariable(e ast.Expr) bool {
	id, ok := e.(*ast.IdentExpr)
	if !ok {
		return false
	}
	sym := fc.a.infoFor(id).Sym
	if sym == nil {
		sym = fc.lookupVar(id.Name)
	}
	if sym == nil {
		return false
	}
	if sym.IsTopLevel {
		return sym.FuncInst == nil && sym.GenFunc == nil && sym.ExternFn == nil
	}
	return sym.BindKind != BindOwned
}

// exprSourceName is the name an identifier expression is written with, or "expr".
func exprSourceName(e ast.Expr) string {
	if id, ok := e.(*ast.IdentExpr); ok {
		return id.Name
	}
	return "expr"
}

// reportBadFallbackSubject reports a `??` whose left side is neither an optional nor a result
// with a value; a unit result gets its own message.
func (fc *funcChecker) reportBadFallbackSubject(span diag.Span, ty types.Type) {
	if en := types.AsResult(ty); en != nil && en.IsUnitResult() {
		fc.a.Rep.ErrorWithHint(span,
			fmt.Sprintf("`??` needs a value, but %s is a unit result", ty.String()),
			"handle the failures with a chain instead: `expr: when ... default ...`")
		return
	}
	if ty != types.TypeInvalid {
		fc.a.Rep.Errorf(span, "left-hand side of `??` must be an optional `?T` or a result, got %s", ty.String())
	}
}

// guardUnwrapElem returns the type a `?? <unwind>` guard unwraps `ty` to: the element of an
// optional, or the success value of a result.
func guardUnwrapElem(ty types.Type) (types.Type, bool) {
	if o, ok := ty.(*types.Optional); ok {
		return o.Elem, true
	}
	if en := types.AsResult(ty); en != nil {
		if vv, _ := en.ResultValue(); vv != nil && vv.Payload != nil {
			return vv.Payload, true
		}
	}
	return nil, false
}

// checkBareResultMember resolves a bare member name of the expected result type `en`:
// `value(x)`, `err(e)`, `missing(key)` construct payload members and `forbidden` a unit failure,
// wherever a result is expected (return position, `func <-`, annotated `let`, arguments). This is
// the construction counterpart of a result chain's bare failure patterns. Names that resolve to a
// variable or function in scope are not treated as members unless they are called while also
// naming a member, which is reported as ambiguous.
func (fc *funcChecker) checkBareResultMember(e ast.Expr, en *types.Enum) (types.Type, bool) {
	inf := fc.a.infoFor(e)
	switch v := e.(type) {
	case *ast.CallExpr:
		id, ok := v.Callee.(*ast.IdentExpr)
		if !ok || id.IsOpt || len(v.TypeArgs) > 0 {
			return nil, false
		}
		vr, ok := en.VariantByName(id.Name)
		if !ok {
			return nil, false
		}
		if fc.lookupVar(id.Name) != nil {
			fc.a.Rep.ErrorWithHint(v.Span,
				fmt.Sprintf("`%s(...)` is ambiguous: it names both a member of %s and a function or variable in scope", id.Name, en.String()),
				fmt.Sprintf("write `%s.%s(...)` for the member", en.Name, id.Name))
			for _, a := range v.Args {
				_ = fc.checkExpr(a, true)
			}
			inf.Type = en
			return en, true
		}
		if vr.Kind == types.VariantUnit {
			fc.a.Rep.Report(diag.Diagnostic{
				Severity: diag.Error,
				Span:     v.Span,
				Full:     v.Span,
				Message:  fmt.Sprintf("member `%s` of %s has no payload; write `%s` without `()`", vr.Name, en.String(), vr.Name),
				Hint:     "unit result members take no arguments; remove `()`",
				Data:     map[string]string{"kind": "remove_parens"},
			})
			for _, a := range v.Args {
				_ = fc.checkExpr(a, true)
			}
			inf.Type = en
			return en, true
		}
		if len(v.Args) != 1 {
			fc.a.Rep.Errorf(v.Span, "`%s(...)` expects 1 payload argument of type %s, got %d", vr.Name, vr.Payload.String(), len(v.Args))
			for _, a := range v.Args {
				_ = fc.checkExpr(a, true)
			}
			inf.Type = en
			return en, true
		}
		arg := v.Args[0]
		aty := fc.checkExprExpect(arg, vr.Payload, true)
		fc.verifyAssignable(aty, vr.Payload, arg, arg.GetSpan(), fmt.Sprintf("%s.%s payload", en.Name, vr.Name))
		fc.consumeValue(arg, vr.Payload, false)
		idInf := fc.a.infoFor(id)
		idInf.EnumType = en
		idInf.EnumVariant = vr
		idInf.Type = en
		inf.CallKind = CallEnumVariant
		inf.EnumType = en
		inf.EnumVariant = vr
		inf.Type = en
		return en, true
	case *ast.IdentExpr:
		if v.IsOpt {
			return nil, false
		}
		vr, ok := en.VariantByName(v.Name)
		if !ok || vr.Kind != types.VariantUnit || fc.lookupVar(v.Name) != nil {
			return nil, false
		}
		inf.EnumType = en
		inf.EnumVariant = vr
		inf.Type = en
		return en, true
	}
	return nil, false
}

// reportTruthiness reports a non-`bool` condition, suggesting the explicit comparison.
func (a *Analyzer) reportTruthiness(cond ast.Expr, ty types.Type, construct string) {
	hint := "Lowlight has no implicit truthiness; write the comparison explicitly"
	switch {
	case types.UnderlyingPrimitive(ty) != nil && types.UnderlyingPrimitive(ty).IsNumeric():
		hint = "Lowlight has no implicit truthiness; compare explicitly, e.g. `x != 0`"
	case ty.Equals(types.TypeString):
		hint = "Lowlight has no implicit truthiness; test the length explicitly: `not s.is_empty()`"
	}
	a.Rep.Report(diag.Diagnostic{
		Severity: diag.Error,
		Span:     a.focusSpan(cond),
		Full:     cond.GetSpan(),
		Message:  fmt.Sprintf("%s condition must have type `bool`, got `%s`", construct, ty.String()),
		Hint:     hint,
		Data:     map[string]string{"kind": "truthiness", "got": ty.String()},
	})
}

// matchArms returns the arms to check for `me`: a result chain's expanded arms, or its own.
func (fc *funcChecker) matchArms(me *ast.MatchExpr) []ast.MatchArm {
	if fc.a.curBM != nil {
		if arms, ok := fc.a.curBM.MatchArms[me]; ok {
			return arms
		}
	}
	return me.Arms
}

func (fc *funcChecker) reportTupleBracketIndex(idx *ast.IndexExpr, tup *types.Tuple) {
	if lit, ok := idx.Index.(*ast.IntLit); ok && lit.Val < uint64(len(tup.Elems)) {
		idxStr := strconv.FormatUint(lit.Val, 10)
		bracketSpan := diag.Span{Start: idx.Base.GetSpan().End, End: idx.Span.End}
		fc.a.Rep.Report(diag.Diagnostic{
			Severity: diag.Error,
			Span:     idx.Span,
			Full:     bracketSpan,
			Message:  "tuple indexing with brackets is not supported; use dot access (e.g. '.0') instead",
			Hint:     fmt.Sprintf("Lowlight accesses tuple elements by dot index: `.%s`", idxStr),
			Data:     map[string]string{"kind": "tuple_index", "index": idxStr},
		})
		return
	}
	fc.a.Rep.ErrorWithHint(
		idx.Span,
		"tuple indexing with brackets is not supported; use dot access (e.g. '.0') instead",
		"tuple indices must be constant dot fields like `.0`, `.1`, etc.",
	)
}

// samePlaceExpr reports whether a and b refer to the exact same pure variable or member chain.
func samePlaceExpr(a, b ast.Expr) (string, bool) {
	switch av := a.(type) {
	case *ast.IdentExpr:
		bv, ok := b.(*ast.IdentExpr)
		if !ok || av.Name != bv.Name || av.IsOpt != bv.IsOpt {
			return "", false
		}
		return av.Name, true
	case *ast.MemberExpr:
		bv, ok := b.(*ast.MemberExpr)
		if !ok || av.Member != bv.Member || av.QuestDot || bv.QuestDot || len(av.BaseTypeArgs) > 0 || len(bv.BaseTypeArgs) > 0 {
			return "", false
		}
		baseName, ok := samePlaceExpr(av.Base, bv.Base)
		if !ok {
			return "", false
		}
		return baseName + "." + av.Member, true
	case *ast.CallExpr:
		bv, ok := b.(*ast.CallExpr)
		if !ok {
			return "", false
		}
		amem, ok1 := av.Callee.(*ast.MemberExpr)
		bmem, ok2 := bv.Callee.(*ast.MemberExpr)
		if !ok1 || !ok2 || amem.Member != bmem.Member || len(av.Args) != 1 || len(bv.Args) != 1 {
			return "", false
		}
		if (amem.Member != "field" && amem.Member != "elem") || amem.QuestDot || bmem.QuestDot {
			return "", false
		}
		baseName, ok := samePlaceExpr(amem.Base, bmem.Base)
		if !ok {
			return "", false
		}
		// Conservative: only warn on exact AST literal/ident arg match if structurally identical
		aid, okA := av.Args[0].(*ast.IdentExpr)
		bid, okB := bv.Args[0].(*ast.IdentExpr)
		if okA && okB && aid.Name == bid.Name {
			return baseName + "." + amem.Member + "(" + aid.Name + ")", true
		}
	}
	return "", false
}
