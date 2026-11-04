package sema

import (
	"fmt"
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

type cfTarget struct {
	id                 int
	kind               cfKind
	label              string
	funcDepth          int
	outerScopeDepth    int
	maxYieldArenaDepth int
	expectedType       types.Type   // nil if inferred bottom-up
	isForNoElse        bool         // true when `for` has no `else`, so loop yields `?Elem` while `for <- v` supplies `Elem`
	yieldTypes         []types.Type // collected types from unwinds to unify/infer
	hitValueExit       bool
}

type scopeFrame struct {
	depth int
	vars  map[string]*Symbol
}

type funcChecker struct {
	a                *Analyzer
	mod              *Module
	subst            map[string]types.Type
	scopes           []*scopeFrame
	moves            map[*Symbol]MoveState
	activeBorrows    map[*Symbol]int
	arenaPinnedDepth map[*Symbol]int     // minimum scope depth at which an owned `Arena` is pinned by live `ref[T]` values
	mutUnwrapOrigin  map[*Symbol]*Symbol // maps unwrapped `&user` -> `user?` so mutating `user` is allowed while reassigning `user?` is blocked
	cfStack          []*cfTarget
	unsafeDepth      int
	funcDepth        int
	hasMutArenaParam bool
	nextLocalID      int

	// For lambda capture tracking: stack of active lambda frames
	lambdaStack []*lambdaFrame
}

type lambdaFrame struct {
	funcDepth int
	captures  map[*Symbol]*Capture
	order     []*Capture
}

func (a *Analyzer) runBodyChecking() {
	a.inBodyPass = true

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
		for j, f := range decl.Fields {
			if f.Default != nil && j < len(st.Fields) {
				fty := st.Fields[j].Type
				got := fc.checkExprExpect(f.Default, fty, true)
				fc.verifyAssignable(got, fty, f.Default, f.Default.GetSpan(), "struct default field")
			}
		}
		fc.popScope()
		a.curBM = prevBM
	}

	// 1b. Check tagged enum struct-variant field default expressions
	for i := 0; i < len(a.Prog.Enums); i++ {
		en := a.Prog.Enums[i]
		if !en.IsTagged {
			continue
		}
		decl := a.Prog.EnumDecls[en]
		mod := a.Prog.EnumMods[en]
		if decl == nil || mod == nil {
			continue
		}
		var subst map[string]types.Type
		if len(decl.TypeParams) == len(en.TypeArgs) && len(en.TypeArgs) > 0 {
			subst = make(map[string]types.Type, len(en.TypeArgs))
			for j, tp := range decl.TypeParams {
				subst[tp.Name] = en.TypeArgs[j]
			}
		}
		bm := NewBodyMaps()
		a.Prog.EnumBodyMaps[en] = bm
		prevBM := a.curBM
		a.curBM = bm
		fc := a.newFuncChecker(mod, subst)
		fc.pushScope()
		for vi, dv := range decl.Variants {
			if !dv.HasFields || vi >= len(en.Variants) || en.Variants[vi].Kind != types.VariantStruct {
				continue
			}
			rv := &en.Variants[vi]
			for fi, df := range dv.Fields {
				if df.Default != nil && fi < len(rv.Fields) {
					fty := rv.Fields[fi].Type
					got := fc.checkExprExpect(df.Default, fty, true)
					fc.verifyAssignable(got, fty, df.Default, df.Default.GetSpan(), "variant default field")
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
		a.checkFuncInstance(fi)
	}
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
		a.Rep.Errorf(tv.Decl.Span, "cyclic dependency detected in top-level bind constant %q", tv.Sym.Name)
		tv.Sym.Type = types.TypeInvalid
		return
	}

	a.topVarStates[tv] = 1 // visiting
	prevBM := a.curBM
	a.curBM = a.Prog.TopBodyMaps

	fc := a.newFuncChecker(tv.Mod, nil)
	fc.pushScope()
	var got types.Type
	if tv.Sym.Type != nil && tv.Sym.Type != types.TypeInvalid {
		got = fc.checkExprExpect(tv.Decl.Value, tv.Sym.Type, true)
		fc.verifyAssignable(got, tv.Sym.Type, tv.Decl.Value, tv.Decl.Value.GetSpan(), "top-level bind")
	} else {
		got = fc.checkExpr(tv.Decl.Value, true)
		if got == types.TypeVoid {
			a.Rep.Errorf(tv.Decl.Span, "cannot bind non-value (`void`) expression to %q", tv.Sym.Name)
			got = types.TypeInvalid
		}
		tv.Sym.Type = got
	}
	if tv.Sym.Type == nil {
		tv.Sym.Type = types.TypeInvalid
	}
	a.checkOptionalIdentRule(tv.Sym.Name, tv.Decl.IsOptName, tv.Sym.Type, tv.Decl.Span, "top-level bind")
	fc.popScope()

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
	if fi.BodyMaps == nil {
		fi.BodyMaps = NewBodyMaps()
	}
	prevBM := a.curBM
	a.curBM = fi.BodyMaps
	defer func() { a.curBM = prevBM }()

	fc := a.newFuncChecker(fi.Mod, fi.TypeSubst)
	fc.pushScope()

	for i, fp := range fi.Sig.Params {
		pname := fi.ParamNames[i]
		bkind := BindReadBorrow
		isBind := true
		switch fp.Mode {
		case types.ModeMut:
			bkind = BindMutBorrow
			isBind = false
			if fp.Type.Equals(types.TypeArena) {
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
	}
	fc.cfStack = append(fc.cfStack, fnTarget)

	if fi.Sig.Ret.Equals(types.TypeVoid) {
		_ = fc.checkVoidOrDiscardedBody(fi.Body)
	} else {
		bodyTy := fc.checkExprExpect(fi.Body, fi.Sig.Ret, true)
		if bodyTy == types.TypeVoid {
			a.Rep.Errorf(fi.Body.GetSpan(), "function %q must return a value of type %s on all execution paths", fi.Name, fi.Sig.Ret.String())
		} else {
			fc.verifyAssignable(bodyTy, fi.Sig.Ret, fi.Body, fi.Body.GetSpan(), fmt.Sprintf("return value of %q", fi.Name))
			if ad := fc.arenaDepthOfExpr(fi.Body); ad > 0 {
				a.Rep.ErrorWithHint(
					fi.Body.GetSpan(),
					fmt.Sprintf("arena reference `ref[T]` allocated from a local or owned `Arena` cannot escape function %q", fi.Name),
					"allocate into a caller-provided `&Arena` parameter instead of a local `Arena`",
				)
			}
			fc.consumeValue(fi.Body, fi.Sig.Ret, false)
		}
	}

	fc.cfStack = fc.cfStack[:len(fc.cfStack)-1]
	fc.popScope()
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

func (fc *funcChecker) popScope() {
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
	if sym == nil || !sym.Type.Equals(types.TypeArena) || sym.BindKind != BindOwned {
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
	switch v := t.(type) {
	case *types.Ref, *types.Interface, *types.Func:
		return true
	case *types.Optional:
		return typeMayHoldArenaRefVisited(v.Elem, seen)
	case *types.Vector:
		return typeMayHoldArenaRefVisited(v.Elem, seen)
	case *types.StackArray:
		return typeMayHoldArenaRefVisited(v.Elem, seen)
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
			switch vr.Kind {
			case types.VariantTuple:
				if vr.Payload != nil && typeMayHoldArenaRefVisited(vr.Payload, seen) {
					return true
				}
			case types.VariantStruct:
				for _, f := range vr.Fields {
					if typeMayHoldArenaRefVisited(f.Type, seen) {
						return true
					}
				}
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
	frame := fc.scopes[len(fc.scopes)-1]
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

	switch v := inner.(type) {
	case *ast.IdentExpr:
		sym := fc.a.infoFor(v).Sym
		if sym == nil {
			sym = fc.lookupVar(v.Name)
		}
		if sym == nil || sym.IsTopLevel {
			// Note: top-level `bind` functions (`static_add`) are stateless global symbols, never moved!
			return
		}
		if sym.BindKind != BindOwned {
			fc.a.Rep.ErrorWithHint(
				expr.GetSpan(),
				fmt.Sprintf("cannot move borrowed variable %q into an owned value", sym.Name),
				fmt.Sprintf("write `%s.copy()` to create an independent owned copy", sym.Name),
			)
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
		if _, isMap := baseTy.(*types.Map); isMap {
			return
		}
		fc.a.Rep.ErrorWithHint(
			expr.GetSpan(),
			fmt.Sprintf("cannot move Resource value of type %s out of indexed element", ty.String()),
			"write `.copy()` to duplicate the element, or `.remove(i)` / `.swap_remove(i)` / `.pop()` to extract ownership",
		)

	case *ast.MemberExpr:
		// Allow `self.field` move ONLY when `self` is an `own self` receiver!
		if id, ok := v.Base.(*ast.IdentExpr); ok && id.Name == "self" {
			selfSym := fc.lookupVar("self")
			if selfSym != nil && selfSym.BindKind == BindOwned && !fc.a.infoFor(v).IsOptChain {
				fc.moves[selfSym] = MoveMoved
				return
			}
		}
		if fc.a.infoFor(v).EnumVariant != nil {
			return
		}
		// Moving a Resource field out of a struct leaves a partially-moved struct; require `.copy()`!
		if !fc.a.infoFor(v).IsOptChain {
			fc.a.Rep.ErrorWithHint(
				expr.GetSpan(),
				fmt.Sprintf("cannot move Resource field %q (type %s) out of struct", v.Member, ty.String()),
				fmt.Sprintf("write `.%s.copy()` to duplicate the field", v.Member),
			)
		}
	}
}

// rootSymbolAndMutability checks whether `place` is a valid mutable lvalue (`let` or `&` borrow)
// and returns the underlying root `*Symbol`.
func (fc *funcChecker) checkMutablePlace(place ast.Expr, action string) *Symbol {
	root, isLval, isMut, reason := fc.analyzePlace(place)
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
		for _, arm := range v.Arms {
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
		if inf.CallKind == CallBuiltinMethod && inf.BuiltinName == "Arena.alloc" && inf.MethodRecv != nil {
			if root := fc.rootSymbolOfExpr(inf.MethodRecv); root != nil && root.BindKind == BindOwned {
				return root.ScopeDepth
			}
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
				if root := fc.rootSymbolOfExpr(inf.MethodRecv); root != nil && root.Type.Equals(types.TypeArena) && root.BindKind == BindOwned {
					if root.ScopeDepth > maxD {
						maxD = root.ScopeDepth
					}
				} else if d := fc.arenaDepthOfExpr(inf.MethodRecv); d > maxD {
					maxD = d
				}
			}
			for _, arg := range v.Args {
				if root := fc.rootSymbolOfExpr(arg); root != nil && root.Type.Equals(types.TypeArena) && root.BindKind == BindOwned {
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

	fc.a.Rep.Errorf(span, "type mismatch in %s: expected %s, got %s", context, expected.String(), got.String())
	return false
}

// -----------------------------------------------------------------------------
// Blocks & Statements
// -----------------------------------------------------------------------------

func (fc *funcChecker) checkVoidOrDiscardedBody(e ast.Expr) types.Type {
	switch b := e.(type) {
	case *ast.UnsafeExpr:
		fc.unsafeDepth++
		defer func() { fc.unsafeDepth-- }()
		ty := fc.checkVoidOrDiscardedBody(b.Expr)
		inf := fc.a.infoFor(b)
		inf.Type = ty
		return ty
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
	fc.unsafeDepth++
	defer func() { fc.unsafeDepth-- }()
	ty := fc.checkExprExpect(ue.Expr, expected, needValue)
	inf := fc.a.infoFor(ue)
	inf.Type = ty
	return ty
}

func (fc *funcChecker) checkBlockExpr(b *ast.BlockExpr, expected types.Type, needValue bool) types.Type {
	id := fc.a.allocTargetID()
	inf := fc.a.infoFor(b)
	inf.TargetID = id

	if b.Unsafe {
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
	for _, st := range b.Stmts {
		stTy := fc.checkStmt(st)
		if stTy == types.TypeNever {
			diverged = true
		}
	}

	if b.Tail != nil {
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

		tailTy = fc.checkExpr(b.Tail, true)
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
		isTagged := false
		for _, v := range td.Variants {
			if v.PayloadType != nil || v.HasFields {
				isTagged = true
				break
			}
		}
		en := &types.Enum{
			Name:       td.Name,
			CName:      fmt.Sprintf("ll_le_%s_%d", td.Name, fc.a.allocTargetID()),
			IsTagged:   isTagged,
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

func (fc *funcChecker) checkVarStmt(s *ast.VarStmt) types.Type {
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
		_ = fc.checkUnwindStmt(s.FallbackUnwind)
		unwrapped := types.Type(types.TypeInvalid)
		if o, ok := optTy.(*types.Optional); ok {
			unwrapped = o.Elem
		} else if optTy != types.TypeInvalid {
			fc.a.Rep.Errorf(s.Value.GetSpan(), "left-hand side of `??` must be an optional `?T`, got %s", optTy.String())
		}
		finalTy := unwrapped
		if declTy != nil && declTy != types.TypeInvalid {
			fc.verifyAssignable(unwrapped, declTy, nil, s.Value.GetSpan(), "variable declaration")
			finalTy = declTy
		}
		fc.a.checkOptionalIdentRule(s.Name, s.IsOptName, finalTy, s.Span, "variable")
		fc.consumeValue(s.Value, optTy, false)
		sym := fc.declareLocal(s.Name, finalTy, s.IsBind, BindOwned, s.Span)
		sym.ArenaDepth = fc.arenaDepthOfExpr(s.Value)
		fc.a.curBM.VarSymbols[s] = sym
		return types.TypeVoid
	}

	var valTy types.Type
	if declTy != nil && declTy != types.TypeInvalid {
		valTy = fc.checkExprExpect(s.Value, declTy, true)
		if valTy == types.TypeVoid {
			fc.a.Rep.Errorf(s.Span, "cannot initialize variable %q with a `void` non-value expression", s.Name)
		} else {
			fc.verifyAssignable(valTy, declTy, s.Value, s.Value.GetSpan(), fmt.Sprintf("declaration of %q", s.Name))
		}
	} else {
		valTy = fc.checkExpr(s.Value, true)
		if valTy == types.TypeVoid {
			fc.a.Rep.Errorf(s.Span, "cannot initialize variable %q with a `void` non-value expression", s.Name)
			valTy = types.TypeInvalid
		}
		declTy = valTy
	}

	fc.a.checkOptionalIdentRule(s.Name, s.IsOptName, declTy, s.Span, "variable")
	fc.consumeValue(s.Value, declTy, false)

	sym := fc.declareLocal(s.Name, declTy, s.IsBind, BindOwned, s.Span)
	sym.ArenaDepth = fc.arenaDepthOfExpr(s.Value)
	fc.a.curBM.VarSymbols[s] = sym
	if valTy == types.TypeNever {
		return types.TypeNever
	}
	return types.TypeVoid
}

func (fc *funcChecker) checkAssignStmt(s *ast.AssignStmt) types.Type {
	// Special shorthand: `map[key] = value` (Spec §7.2 / §8.2: shorthand for `map.insert(own k, own v)`)!
	if idx, isIdx := s.Target.(*ast.IndexExpr); isIdx && s.Op == token.Assign && s.FallbackUnwind == nil {
		baseTy := fc.checkExpr(idx.Base, true)
		if mapTy, isMap := baseTy.(*types.Map); isMap {
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
			fc.a.Rep.Errorf(id.Span, "cannot reassign deeply immutable `bind` variable %q", sym.Name)
		}
		if sym.Type != nil && sym.Type.Equals(types.TypeArena) {
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
		rootSym = fc.checkMutablePlace(s.Target, "mutate")
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
		_ = fc.checkUnwindStmt(s.FallbackUnwind)
		if o, ok := optTy.(*types.Optional); ok {
			fc.verifyAssignable(o.Elem, targetTy, nil, s.Value.GetSpan(), "assignment")
		} else if optTy != types.TypeInvalid {
			fc.a.Rep.Errorf(s.Value.GetSpan(), "left-hand side of `??` must be optional, got %s", optTy.String())
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
	fc.checkCompoundAssignTypes(s.Op, targetTy, valTy, s.Span)
	return types.TypeVoid
}

func (fc *funcChecker) checkCompoundAssignTypes(op token.Kind, lhs, rhs types.Type, span diag.Span) {
	if lhs == types.TypeInvalid || rhs == types.TypeInvalid {
		return
	}
	if op == token.PlusEq && lhs.Equals(types.TypeString) && rhs.Equals(types.TypeString) {
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
	wrapOpt := false
	if (tgt.kind == cfFor || tgt.kind == cfWhile) && tgt.isForNoElse && exp != nil {
		// In `let found? = for (x in list) { if (x > 10) for <- x }`,
		// the loop expression has type `?int`, while `for <- x` yields `int` (which wraps into `?int`)!
		if opt, isOpt := exp.(*types.Optional); isOpt {
			exp = opt.Elem
			wrapOpt = true
		}
	}

	if u.Value == nil {
		// Bare unwind (`<-`, `next <-`, `for <-`, `func <-`)
		if !isCont && exp != nil && !exp.Equals(types.TypeVoid) {
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
			// Inferring block/loop yield type bottom-up!
			valTy = fc.checkExpr(u.Value, true)
			tgt.yieldTypes = append(tgt.yieldTypes, valTy)
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
		} else if !isCont {
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

	resTy := tgt.expectedType
	if resTy == nil && len(tgt.yieldTypes) > 0 {
		resTy = tgt.yieldTypes[0]
	}

	fc.a.curBM.UnwindInfos[u] = &UnwindInfo{
		TargetID:       tgt.id,
		TargetKind:     u.Target,
		IsContinue:     isCont,
		WrapInOptional: wrapOpt,
		ResultType:     resTy,
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
			fc.a.Rep.Errorf(v.Span, "undeclared identifier %q", v.Name)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if sym.IsTopLevel && fc.a.inBodyPass {
			fc.a.resolveTopVar(sym)
		}
		if sym.Deprecated {
			msg := ""
			if sym.DeprecatedMsg != "" {
				msg = ": " + sym.DeprecatedMsg
			}
			fc.a.Rep.Warnf(v.Span, "use of deprecated identifier %q%s", sym.Name, msg)
		}
		fc.checkSymReadable(sym, v.Span)
		// Check lambda capture if referencing a local from an enclosing function scope!
		if !sym.IsTopLevel && sym.DeclFuncDepth < fc.funcDepth {
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

	case *ast.StructLit:
		return fc.checkStructLit(v, expected)

	case *ast.FuncLit:
		return fc.checkFuncLit(v, expected)

	case *ast.UnsafeExpr:
		return fc.checkUnsafeExpr(v, expected, needValue)

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
				fc.a.Rep.Errorf(u.Span, "cannot dereference non-pointer type %s", sub.String())
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
		optL, ok := lty.(*types.Optional)
		if !ok {
			if lty != types.TypeInvalid {
				fc.a.Rep.Errorf(b.Left.GetSpan(), "left-hand side of `??` must be an optional `?T`, got %s", lty.String())
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
		rty := fc.checkExprExpect(b.Right, rExpect, true)
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
				fc.a.Rep.Errorf(b.Span, "cannot compare non-optional %s to `none`", lty.String())
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
				fc.a.Rep.Errorf(b.Span, "cannot compare `none` to non-optional %s", rty.String())
				inf.Type = types.TypeBool
				return types.TypeBool
			}
			lty = fc.checkExprExpect(b.Left, rty, true)
			inf.Type = types.TypeBool
			return types.TypeBool
		}

		lty, rty = fc.checkOperandPairWithAdaptation(b.Left, b.Right, nil)
		if lty != nil && rty != nil && lty != types.TypeInvalid && rty != types.TypeInvalid {
			if !lty.Equals(rty) {
				// Allow `T == ?T` or `?T == T` by wrapping the `T` side
				if oL, ok := lty.(*types.Optional); ok && oL.Elem.Equals(rty) {
					fc.a.infoFor(b.Right).WrapOptional = true
					rty = lty
				} else if oR, ok := rty.(*types.Optional); ok && oR.Elem.Equals(lty) {
					fc.a.infoFor(b.Left).WrapOptional = true
					lty = rty
				} else {
					fc.a.Rep.Errorf(b.Span, "cannot compare mismatched types %s and %s", lty.String(), rty.String())
				}
			} else if !fc.a.supportsEq(lty) {
				fc.a.Rep.Errorf(b.Span, "equality `%s` is not defined on type %s", b.Op.String(), lty.String())
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
				fc.a.Rep.Errorf(b.Span, "cannot compare mismatched types %s and %s", lty.String(), rty.String())
			} else if !fc.a.supportsCmp(lty) {
				fc.a.Rep.Errorf(b.Span, "ordering operator `%s` is not defined on %s (type does not implement Comparable)", b.Op.String(), lty.String())
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

	if b.Op == token.Plus && lty.Equals(types.TypeString) {
		inf.Type = types.TypeString
		return types.TypeString
	}

	lp := types.UnderlyingPrimitive(lty)
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
				if len(m.BaseTypeArgs) != len(genEn.Decl.TypeParams) {
					fc.a.Rep.Errorf(m.Span, "generic enum %s expects %d type argument(s), got %d",
						genEn.Decl.Name, len(genEn.Decl.TypeParams), len(m.BaseTypeArgs))
					return nil, genEn, true
				}
				targs := make([]types.Type, len(m.BaseTypeArgs))
				for i, ta := range m.BaseTypeArgs {
					targs[i] = fc.a.resolveValueType(fc.mod, ta, fc.subst, false)
					if targs[i] == types.TypeInvalid {
						return nil, genEn, true
					}
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
			if _, isMod := fc.mod.Aliased[modId.Name]; isMod {
				return resolveFromMod(modId.Name, baseMem.Member)
			}
		}
	}
	return nil, nil, false
}

// checkMemberExpr resolves `Enum.Variant`, `Result[T, E].Empty`, `mod.Symbol`, `struct.field`, `ref[Struct].field`,
// and optional dot-chaining (`user?.name`, `self.profile?.score`, `acc?.nick.len()`, `find_user(id)?.name`).
func (fc *funcChecker) checkMemberExpr(m *ast.MemberExpr, expected types.Type) types.Type {
	inf := fc.a.infoFor(m)
	fc.maybeRewriteIndexedMemberBase(m)

	// 1. Check imported module symbol (`mod.val`)!
	if baseId, isId := m.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && len(m.BaseTypeArgs) == 0 {
		if fc.lookupVar(baseId.Name) == nil {
			if targetMod, isMod := fc.mod.Aliased[baseId.Name]; isMod {
				if sym, ok := targetMod.Symbols[m.Member]; ok {
					if sym.IsTopLevel && fc.a.inBodyPass {
						fc.a.resolveTopVar(sym)
					}
					if sym.Deprecated {
						msg := ""
						if sym.DeprecatedMsg != "" {
							msg = ": " + sym.DeprecatedMsg
						}
						fc.a.Rep.Warnf(m.Span, "use of deprecated identifier %q%s", sym.Name, msg)
					}
					inf.Sym = sym
					inf.Type = sym.Type
					if sym.Type == nil {
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
					return sym.Type
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
			fc.a.Rep.Errorf(m.Span, "enum %s has no variant %q", en.String(), m.Member)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if v.Kind == types.VariantTuple {
			fc.a.Rep.Errorf(m.Span, "variant `%s.%s` carries a payload of type %s; construct with `%s.%s(...)`",
				en.String(), v.Name, v.Payload.String(), en.Name, v.Name)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		if v.Kind == types.VariantStruct {
			fc.a.Rep.Errorf(m.Span, "variant `%s.%s` carries named fields; construct with `%s.%s[...]`",
				en.String(), v.Name, en.Name, v.Name)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		inf.EnumType = en
		inf.EnumVariant = v
		inf.Type = en
		return en
	}

	// 2. Evaluate base expression and handle optional chaining (Spec §7.2.3 & Decision B1 / 3A)!
	baseTy := fc.checkExpr(m.Base, true)
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
		fc.a.Rep.Errorf(m.Span, "`?.` cannot be used on non-optional type %s (use `.`)", baseTy.String())
	}

	// Unwrap `ref[Struct]` transparently for field access (`node_ref.val`)!
	if refTy, isRef := innerTy.(*types.Ref); isRef {
		innerTy = refTy.Elem
		inf.DerefStructPtr = true
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
		fc.a.Rep.Errorf(m.Span, "struct %s has no field %q", st.String(), m.Member)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	resTy := f.Type
	if isOptBase {
		resTy = types.NewOptional(resTy) // flattens `?(?T) -> ?T` automatically!
	}
	inf.Type = resTy
	return resTy
}

func (fc *funcChecker) checkIndexExpr(idx *ast.IndexExpr) types.Type {
	inf := fc.a.infoFor(idx)
	baseTy := fc.checkExpr(idx.Base, true)
	if baseTy == types.TypeInvalid {
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	// Deref `ref[Vec]` if indexing through arena ref
	if r, ok := baseTy.(*types.Ref); ok {
		baseTy = r.Elem
	}

	switch v := baseTy.(type) {
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
	}

	fc.a.Rep.Errorf(idx.Span, "cannot index value of type %s", baseTy.String())
	inf.Type = types.TypeInvalid
	return types.TypeInvalid
}

func (fc *funcChecker) checkSliceExpr(sl *ast.SliceExpr) types.Type {
	inf := fc.a.infoFor(sl)
	baseTy := fc.checkExpr(sl.Base, true)
	loTy := fc.checkExprExpect(sl.Low, types.TypeInt, true)
	hiTy := fc.checkExprExpect(sl.High, types.TypeInt, true)
	fc.verifyAssignable(loTy, types.TypeInt, sl.Low, sl.Low.GetSpan(), "slice start")
	fc.verifyAssignable(hiTy, types.TypeInt, sl.High, sl.High.GetSpan(), "slice end")

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

// -----------------------------------------------------------------------------
// Collection, Struct, & Lambda Literals
// -----------------------------------------------------------------------------

func (fc *funcChecker) checkVecOrArrayLit(v *ast.VecLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(v)
	target := expected
	if opt, ok := target.(*types.Optional); ok {
		target = opt.Elem
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
	sz, ok := fc.a.evalConstInt(fc.mod, r.Count)
	if !ok || sz <= 0 {
		fc.a.Rep.Errorf(r.Count.GetSpan(), "size/repeat count in `[N: ...]` must be a positive compile-time integer constant")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	var elemHint types.Type
	if arr, isArr := expected.(*types.StackArray); isArr {
		elemHint = arr.Elem
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
	if st, ok := expected.(*types.Set); ok {
		elemHint = st.Elem
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
		isH := types.IsHashable(elemTy) || (fc.a.Prog.IfaceHashable != nil && fc.a.ImplementsInterface(elemTy, fc.a.Prog.IfaceHashable))
		if !isH {
			fc.a.Rep.Errorf(s.Span, "set element type %s is not hashable", elemTy.String())
		}
	}
	res := &types.Set{Elem: elemTy}
	inf.Type = res
	return res
}

func (fc *funcChecker) checkMapLit(m *ast.MapLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(m)
	var kHint, vHint types.Type
	if mt, ok := expected.(*types.Map); ok {
		kHint = mt.Key
		vHint = mt.Value
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
		isH := types.IsHashable(kTy) || (fc.a.Prog.IfaceHashable != nil && fc.a.ImplementsInterface(kTy, fc.a.Prog.IfaceHashable))
		if !isH {
			fc.a.Rep.Errorf(m.Span, "map key type %s is not hashable", kTy.String())
		}
	}
	res := &types.Map{Key: kTy, Value: vTy}
	inf.Type = res
	return res
}

func (fc *funcChecker) checkEmptyHashLit(e *ast.EmptyHashLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(e)
	target := expected
	if opt, ok := target.(*types.Optional); ok {
		target = opt.Elem
	}
	switch v := target.(type) {
	case *types.Set, *types.Map:
		inf.Type = v
		return v
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

func (fc *funcChecker) checkStructLit(sl *ast.StructLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(sl)
	expTarget := expected
	if opt, ok := expTarget.(*types.Optional); ok {
		expTarget = opt.Elem
	}

	// Check struct-payload enum variant:
	// 1. `Tree[int].Branch[...]` / `mod.Expr.Add[...]` / `mod.Tree[int].Branch[...]` (`sl.VariantName != ""`)
	// 2. `Expr.Add[...]` (`sl.VariantName == ""` and `sl.Module` is an Enum/GenericEnum in `fc.mod`, not an import alias)
	if sl.VariantName != "" {
		return fc.checkEnumStructVariantLit(sl.Module, sl.TypeName, sl.VariantName, sl, expTarget)
	}
	if sl.Module != "" {
		if _, isMod := fc.mod.Aliased[sl.Module]; !isMod {
			if fc.a.lookupGenEnumInModule(fc.mod, "", sl.Module) != nil {
				return fc.checkEnumStructVariantLit("", sl.Module, sl.TypeName, sl, expTarget)
			}
			if ty := fc.a.lookupTypeInModule(fc.mod, "", sl.Module); ty != nil {
				if _, isEn := ty.(*types.Enum); isEn {
					return fc.checkEnumStructVariantLit("", sl.Module, sl.TypeName, sl, expTarget)
				}
			}
		}
	}

	var st *types.Struct

	if genSt := fc.a.lookupGenStructInModule(fc.mod, sl.Module, sl.TypeName); genSt != nil {
		if len(sl.TypeArgs) > 0 {
			if len(sl.TypeArgs) != len(genSt.Decl.TypeParams) {
				fc.a.Rep.Errorf(sl.Span, "generic struct %s expects %d type argument(s), got %d",
					sl.TypeName, len(genSt.Decl.TypeParams), len(sl.TypeArgs))
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			targs := make([]types.Type, len(sl.TypeArgs))
			for i, ta := range sl.TypeArgs {
				targs[i] = fc.a.resolveValueType(fc.mod, ta, fc.subst, false)
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

	provided := make(map[string]bool)
	for _, fi := range sl.Fields {
		if provided[fi.Name] {
			fc.a.Rep.Errorf(fi.Span, "duplicate field %q in %s literal", fi.Name, st.String())
			continue
		}
		provided[fi.Name] = true
		sf, _ := st.FieldByName(fi.Name)
		if sf == nil {
			fc.a.Rep.Errorf(fi.Span, "struct %s has no field %q", st.String(), fi.Name)
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

func (fc *funcChecker) checkEnumStructVariantLit(modAlias, enumName, varName string, sl *ast.StructLit, expTarget types.Type) types.Type {
	inf := fc.a.infoFor(sl)
	var en *types.Enum

	if genEn := fc.a.lookupGenEnumInModule(fc.mod, modAlias, enumName); genEn != nil {
		if len(sl.EnumTypeArgs) > 0 {
			if len(sl.EnumTypeArgs) != len(genEn.Decl.TypeParams) {
				fc.a.Rep.Errorf(sl.Span, "generic enum %s expects %d type argument(s), got %d",
					enumName, len(genEn.Decl.TypeParams), len(sl.EnumTypeArgs))
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			targs := make([]types.Type, len(sl.EnumTypeArgs))
			for i, ta := range sl.EnumTypeArgs {
				targs[i] = fc.a.resolveValueType(fc.mod, ta, fc.subst, false)
				if targs[i] == types.TypeInvalid {
					inf.Type = types.TypeInvalid
					return types.TypeInvalid
				}
			}
			en = fc.a.instantiateEnum(genEn, targs, sl.Span)
		} else if expEn, ok := expTarget.(*types.Enum); ok && expEn.Name == genEn.Decl.Name && len(expEn.TypeArgs) == len(genEn.Decl.TypeParams) {
			en = expEn
		} else {
			en = fc.inferGenericEnumStructLit(genEn, varName, sl)
		}
		if en == nil {
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	} else {
		if len(sl.EnumTypeArgs) > 0 {
			fc.a.Rep.Errorf(sl.Span, "enum %s is not generic", enumName)
		}
		ty := fc.a.lookupTypeInModule(fc.mod, modAlias, enumName)
		var ok bool
		en, ok = ty.(*types.Enum)
		if !ok || en == nil {
			fc.a.Rep.Errorf(sl.Span, "%q is not an enum type", enumName)
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
	}

	v, ok := en.VariantByName(varName)
	if !ok {
		fc.a.Rep.Errorf(sl.Span, "enum %s has no variant %q", en.String(), varName)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if v.Kind == types.VariantUnit {
		fc.a.Rep.Errorf(sl.Span, "variant `%s.%s` takes no payload; write `%s.%s` without `[...]`",
			en.String(), varName, en.Name, varName)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	if v.Kind == types.VariantTuple {
		fc.a.Rep.Errorf(sl.Span, "variant `%s.%s` carries a single payload of type %s; construct with `%s.%s(...)` instead of `[...]`",
			en.String(), varName, v.Payload.String(), en.Name, varName)
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}

	provided := make(map[string]bool)
	for _, fi := range sl.Fields {
		if provided[fi.Name] {
			fc.a.Rep.Errorf(fi.Span, "duplicate field %q in `%s.%s` literal", fi.Name, en.String(), varName)
			continue
		}
		provided[fi.Name] = true
		sf, _ := v.FieldByName(fi.Name)
		if sf == nil {
			fc.a.Rep.Errorf(fi.Span, "variant `%s.%s` has no field %q", en.String(), varName, fi.Name)
			_ = fc.checkExpr(fi.Value, true)
			continue
		}
		got := fc.checkExprExpect(fi.Value, sf.Type, true)
		fc.verifyAssignable(got, sf.Type, fi.Value, fi.Value.GetSpan(), fmt.Sprintf("field %q of %s.%s", fi.Name, en.String(), varName))
		fc.consumeValue(fi.Value, sf.Type, false)
	}

	for _, sf := range v.Fields {
		if provided[sf.Name] || sf.HasDefault {
			continue
		}
		if _, isOpt := sf.Type.(*types.Optional); isOpt {
			continue
		}
		fc.a.Rep.Errorf(sl.Span, "missing required field %q (type %s) in `%s.%s` literal", sf.Name, sf.Type.String(), en.String(), varName)
	}

	inf.EnumType = en
	inf.EnumVariant = v
	inf.Type = en
	return en
}

func (fc *funcChecker) inferGenericEnumStructLit(genEn *GenericEnumTemplate, varName string, sl *ast.StructLit) *types.Enum {
	var declVar *ast.EnumVariant
	for i := range genEn.Decl.Variants {
		if genEn.Decl.Variants[i].Name == varName {
			declVar = &genEn.Decl.Variants[i]
			break
		}
	}
	if declVar == nil || !declVar.HasFields {
		fc.a.Rep.Errorf(sl.Span, "cannot infer generic type parameters for `%s.%s`; specify `%s[...].%s` explicitly",
			genEn.Decl.Name, varName, genEn.Decl.Name, varName)
		return nil
	}
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
	for _, tp := range genEn.Decl.TypeParams {
		tpNames[tp.Name] = true
	}
	for _, df := range declVar.Fields {
		if got, ok := fieldTypes[df.Name]; ok && got != types.TypeInvalid {
			matchTypeExprForInference(df.Type, got, tpNames, inferred)
		}
	}
	targs := make([]types.Type, len(genEn.Decl.TypeParams))
	for i, tp := range genEn.Decl.TypeParams {
		ta, ok := inferred[tp.Name]
		if !ok {
			fc.a.Rep.Errorf(sl.Span, "cannot infer generic type parameter %s for `%s.%s`; specify `%s[...].%s` explicitly",
				tp.Name, genEn.Decl.Name, varName, genEn.Decl.Name, varName)
			return nil
		}
		targs[i] = ta
	}
	return fc.a.instantiateEnum(genEn, targs, sl.Span)
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
		targs[i] = ta
	}
	return fc.a.instantiateStruct(genSt, targs, sl.Span)
}

func matchTypeExprForInference(pat ast.TypeExpr, concrete types.Type, tpNames map[string]bool, out map[string]types.Type) {
	if concrete == nil || concrete == types.TypeInvalid {
		return
	}
	switch p := pat.(type) {
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
		}
	case *ast.StackArrayType:
		if arr, ok := concrete.(*types.StackArray); ok {
			matchTypeExprForInference(p.Elem, arr.Elem, tpNames, out)
		}
	case *ast.SetType:
		if st, ok := concrete.(*types.Set); ok {
			matchTypeExprForInference(p.Elem, st.Elem, tpNames, out)
		}
	case *ast.MapType:
		if mt, ok := concrete.(*types.Map); ok {
			matchTypeExprForInference(p.Key, mt.Key, tpNames, out)
			matchTypeExprForInference(p.Value, mt.Value, tpNames, out)
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

func (fc *funcChecker) checkFuncLit(fl *ast.FuncLit, _ types.Type) types.Type {
	inf := fc.a.infoFor(fl)
	var ps []types.FuncParam
	for _, p := range fl.Params {
		pm, pt := fc.a.resolveParamType(fc.mod, p.Type, fc.subst, false)
		fc.a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "lambda parameter")
		ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
	}
	ret := types.Type(types.TypeVoid)
	if fl.Ret != nil {
		ret = fc.a.resolveValueType(fc.mod, fl.Ret, fc.subst, false)
	}
	sig := &types.Func{Params: ps, Ret: ret}

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
			if fp.Type.Equals(types.TypeArena) {
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
		expectedType:    ret,
	}
	fc.cfStack = append(fc.cfStack, lamTarget)

	if ret.Equals(types.TypeVoid) {
		_ = fc.checkVoidOrDiscardedBody(fl.Body)
	} else {
		bty := fc.checkExprExpect(fl.Body, ret, true)
		if bty == types.TypeVoid {
			fc.a.Rep.Errorf(fl.Body.GetSpan(), "lambda must return a value of type %s on all paths", ret.String())
		} else {
			fc.verifyAssignable(bty, ret, fl.Body, fl.Body.GetSpan(), "lambda return")
			fc.consumeValue(fl.Body, ret, false)
			bAd := fc.arenaDepthOfExpr(fl.Body)
			if bAd > lamOuterDepth {
				fc.a.Rep.ErrorWithHint(
					fl.Body.GetSpan(),
					"arena reference `ref[T]` allocated from a lambda-local or owned `Arena` cannot escape the lambda",
					"allocate into an outer or caller-provided `&Arena` instead",
				)
			} else if types.ContainsArenaRef(ret) && !fc.hasMutArenaParam && !prevHasArena && bAd == 0 {
				fc.a.Rep.Errorf(fl.Body.GetSpan(), "lambda cannot return %s containing `ref[T]` unless it takes an `&Arena` parameter", ret.String())
			}
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
	inf := fc.a.infoFor(ie)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	// Determine target expected type for `if <- val`:
	var branchExp types.Type
	if needValue && expected != nil && expected != types.TypeVoid {
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
				fc.a.Rep.Errorf(ie.Cond.GetSpan(), "`if` condition must have type `bool`, got %s (no implicit truthiness)", cty.String())
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
		cty := fc.checkExpr(ie.Cond, true)
		optTy, ok := cty.(*types.Optional)
		if !ok {
			if cty != types.TypeInvalid {
				fc.a.Rep.Errorf(ie.Cond.GetSpan(), "`if (expr as %s)` requires an optional `?T` expression, got %s", ie.BindName, cty.String())
			}
		} else {
			if ie.MutUnwrap {
				lockedOrigin = fc.checkMutablePlace(ie.Cond, "mutably unwrap")
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
	if !needValue {
		thenTy = fc.checkVoidOrDiscardedBody(ie.Then)
	} else {
		thenTy = fc.checkExprExpect(ie.Then, branchExp, true)
		if thenTy == types.TypeVoid {
			fc.a.Rep.Errorf(ie.Then.GetSpan(), "`if` branch used as a value must evaluate to a value")
		} else if branchExp != nil {
			fc.verifyAssignable(thenTy, branchExp, ie.Then, ie.Then.GetSpan(), "`if` then-branch")
			fc.consumeValue(ie.Then, branchExp, false)
		} else {
			fc.consumeValue(ie.Then, thenTy, false)
		}
	}

	if lockedOrigin != nil {
		fc.activeBorrows[lockedOrigin]--
	}
	thenAd := fc.arenaDepthOfExpr(ie.Then)
	if needValue && thenAd > outerDepth {
		fc.a.Rep.Errorf(ie.Then.GetSpan(), "arena reference `ref[T]` cannot escape `if` branch scope")
	}
	if needValue && thenAd > tgt.maxYieldArenaDepth {
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
	if !needValue {
		elseTy = fc.checkVoidOrDiscardedBody(ie.Else)
	} else {
		hint := branchExp
		if hint == nil && thenTy != types.TypeNever && thenTy != types.TypeInvalid {
			hint = thenTy
		}
		elseTy = fc.checkExprExpect(ie.Else, hint, true)
		if elseTy == types.TypeVoid {
			fc.a.Rep.Errorf(ie.Else.GetSpan(), "`else` branch used as a value must evaluate to a value")
		}
	}
	elseAd := fc.arenaDepthOfExpr(ie.Else)
	if needValue && elseAd > outerDepth {
		fc.a.Rep.Errorf(ie.Else.GetSpan(), "arena reference `ref[T]` cannot escape `else` branch scope")
	}
	if needValue && elseAd > tgt.maxYieldArenaDepth {
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
		if thenTy == types.TypeNever && elseTy == types.TypeNever {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	resTy := branchExp
	if resTy == nil {
		resTy = unifyYieldTypes(append(tgt.yieldTypes, thenTy, elseTy))
	}
	if thenTy != types.TypeVoid && thenTy != types.TypeInvalid {
		fc.verifyAssignable(thenTy, resTy, ie.Then, ie.Then.GetSpan(), "`if` then-branch")
	}
	if elseTy != types.TypeVoid && elseTy != types.TypeInvalid {
		fc.verifyAssignable(elseTy, resTy, ie.Else, ie.Else.GetSpan(), "`if` else-branch")
		fc.consumeValue(ie.Else, resTy, false)
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
			fc.a.Rep.Errorf(h.condOrIter.GetSpan(), "loop condition must be `bool`, got %s", cty.String())
		}

	case ast.ForRange:
		loTy := fc.checkExprExpect(h.condOrIter, types.TypeInt, true)
		hiTy := fc.checkExprExpect(h.rangeEnd, loTy, true)
		p := types.UnderlyingPrimitive(loTy)
		if loTy != types.TypeInvalid && (p == nil || !p.IsInteger() || !loTy.Equals(hiTy)) {
			fc.a.Rep.Errorf(h.span, "range bounds in loop `(%s in a..b)` must be matching integer types, got %s and %s",
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

	switch c := colTy.(type) {
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
	}

	if colTy != types.TypeInvalid {
		fc.a.Rep.Errorf(h.condOrIter.GetSpan(), "cannot iterate over value of type %s", colTy.String())
	}
	return nil, nil
}

func (fc *funcChecker) checkForExpr(fe *ast.ForExpr, expected types.Type, needValue bool) types.Type {
	inf := fc.a.infoFor(fe)
	id := fc.a.allocTargetID()
	inf.TargetID = id

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

	if !needValue {
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

	if fe.EvalType != nil {
		if vecAst, isVec := fe.EvalType.(*ast.VectorType); isVec && vecAst.Elem == nil {
			isVectorInferred = true
		} else {
			colTy := fc.a.resolveValueType(fc.mod, fe.EvalType, nil, false)
			if colTy != types.TypeInvalid && !fc.a.isCollectableType(colTy) {
				fc.a.Rep.Errorf(fe.EvalType.GetSpan(), "type %s does not implement Collectable", colTy.String())
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
			targetColTy = colTy
			if colTy != types.TypeInvalid {
				expectedItemTy = fc.a.collectableItemType(colTy)
			}
		}
	} else if expected != nil && expected != types.TypeVoid && expected != types.TypeInvalid && fc.a.isCollectableType(expected) {
		targetColTy = expected
		expectedItemTy = fc.a.collectableItemType(expected)
	} else {
		isVectorInferred = true
	}

	outerDepth := fc.curDepth()
	tgt := &cfTarget{
		id:              id,
		kind:            cfFor,
		label:           fe.Label,
		funcDepth:       fc.funcDepth,
		outerScopeDepth: outerDepth,
		expectedType:    expectedItemTy,
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

	bodyTy := fc.checkExprExpect(fe.Body, expectedItemTy, true)

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
		if bodyTy == types.TypeVoid || bodyTy == types.TypeNever || bodyTy == types.TypeInvalid {
			fc.a.Rep.Errorf(fe.Body.GetSpan(), "`for` loop expression producing a list must evaluate to a non-void value")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		targetColTy = &types.Vector{Elem: bodyTy}
		expectedItemTy = bodyTy
	}

	if bodyTy != types.TypeVoid && bodyTy != types.TypeNever && bodyTy != types.TypeInvalid && expectedItemTy != nil {
		fc.verifyAssignable(bodyTy, expectedItemTy, fe.Body, fe.Body.GetSpan(), "`for` iteration item")
		fc.consumeValue(fe.Body, expectedItemTy, false)
	}

	if fe.Else != nil {
		fc.pushScope()
		elseTy := fc.checkExprExpect(fe.Else, targetColTy, true)
		fc.popScope()
		fc.verifyAssignable(elseTy, targetColTy, fe.Else, fe.Else.GetSpan(), "`for ... else` fallback")
		fc.consumeValue(fe.Else, targetColTy, false)
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
		evalTy = fc.a.resolveValueType(fc.mod, we.EvalType, nil, false)
	}

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
	if !needValue {
		tgtExp = types.TypeVoid
	} else if elemExp != nil {
		if we.Else == nil && evalTy == nil {
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
	if !needValue {
		bodyTy = fc.checkVoidOrDiscardedBody(we.Body)
	} else {
		bodyTy = fc.checkLoopBodyExpr(we.Body, elemExp)
	}

	if lockedCol != nil {
		fc.activeBorrows[lockedCol]--
	}
	if needValue && bodyTy != types.TypeVoid && bodyTy != types.TypeNever && bodyTy != types.TypeInvalid {
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

	if !needValue {
		fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)
		if we.Else != nil {
			fc.checkVoidOrDiscardedBody(we.Else)
		}
		_ = isInfiniteTrue
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
			if elseAd > outerDepth {
				fc.a.Rep.Errorf(we.Else.GetSpan(), "arena reference `ref[T]` cannot escape `while ... else` scope")
			} else if elseAd > tgt.maxYieldArenaDepth {
				tgt.maxYieldArenaDepth = elseAd
			}
			fc.popScope()
			elseMoves := fc.cloneMoves()
			fc.moves = mergeMoveMaps(postBodyMoves, elseMoves)
			fc.verifyAssignable(elseTy, evalTy, we.Else, we.Else.GetSpan(), "`while ... else` fallback")
			fc.consumeValue(we.Else, evalTy, false)
			inf.YieldArenaDepth = tgt.maxYieldArenaDepth
			if typeMayHoldArenaRef(evalTy) {
				fc.lowerPinnedArenasTo(outerDepth)
			}
			inf.Type = evalTy
			return evalTy
		} else {
			fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)
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
	elseTy := fc.checkExprExpect(we.Else, inferredElem, true)
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

// checkLoopBodyExpr allows a value-position `for` loop body to EITHER evaluate to an iteration value `T`
// OR be a `void` search body that uses `for <- val`!
func (fc *funcChecker) checkLoopBodyExpr(body ast.Expr, elemExp types.Type) types.Type {
	_, elemIsOpt := elemExp.(*types.Optional)
	if !elemIsOpt && isSearchStmtBody(body) {
		return fc.checkVoidOrDiscardedBody(body)
	}
	return fc.checkExprExpect(body, elemExp, true)
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
	inf := fc.a.infoFor(me)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	scrutTy := fc.checkExpr(me.Scrutinee, true)
	scrutRoot := fc.rootSymbolOfExpr(me.Scrutinee)
	scrutArenaDepth := fc.arenaDepthOfExpr(me.Scrutinee)
	var armExp types.Type
	if needValue && expected != nil && expected != types.TypeVoid {
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

	// Track enum variant (and bool) coverage for exhaustive `else Enum.FinalVariant:` (Spec §10.3 / B3)!
	isRefScrut := false
	if rf, ok := scrutTy.(*types.Ref); ok {
		if _, isEn := rf.Elem.(*types.Enum); isEn {
			scrutTy = rf.Elem
			isRefScrut = true
		}
	}
	enumScrut, isEnum := scrutTy.(*types.Enum)
	coveredVariants := make(map[string]bool)
	hasCatchAllElse := false

	preMoves := fc.cloneMoves()
	var liveBranchMoves []map[*Symbol]MoveState
	var armTypes []types.Type
	allDiverge := true

	for _, arm := range me.Arms {
		if arm.IsElse && len(arm.Patterns) == 0 {
			hasCatchAllElse = true
		}

		fc.moves = fc.cloneMovesFrom(preMoves)
		fc.pushScope()

		hasBindings := false
		hasMutBind := false
		hasOwnResourceBind := false
		hasReadOrMutBind := false
		for _, pat := range arm.Patterns {
			pHasBind, pHasMut, pHasOwnRes := fc.checkMatchPattern(pat, scrutTy, enumScrut, scrutArenaDepth, coveredVariants)
			if pHasBind {
				hasBindings = true
			}
			if pHasMut {
				hasMutBind = true
			}
			if pHasOwnRes {
				hasOwnResourceBind = true
			}
			if pHasBind && !pHasOwnRes {
				hasReadOrMutBind = true
			}
		}
		if len(arm.Patterns) > 1 && hasBindings {
			fc.a.Rep.Errorf(arm.Span, "cannot bind variables in a multi-pattern `when A, B:` arm; use `_` wildcards or separate `when` arms")
		}
		if hasMutBind {
			inf.MatchMutScrut = true
			if !isRefScrut {
				_ = fc.checkMutablePlace(me.Scrutinee, "mutably destructure")
			}
		}
		if hasOwnResourceBind {
			if isRefScrut {
				fc.a.Rep.Errorf(arm.Span, "cannot move `own` resource payload out of arena reference `ref[%s]`", scrutTy.String())
			} else if enumScrut != nil && enumScrut.HasCustomDrop {
				fc.a.Rep.Errorf(arm.Span, "cannot move `own` payload out of enum %s because it implements custom `Drop`", enumScrut.String())
			} else {
				fc.consumeValue(me.Scrutinee, scrutTy, false)
			}
		}

		lockedScrut := (*Symbol)(nil)
		if scrutRoot != nil && (hasMutBind || (hasReadOrMutBind && !hasOwnResourceBind)) {
			lockedScrut = scrutRoot
			fc.activeBorrows[lockedScrut]++
		}

		var aty types.Type
		if !needValue {
			aty = fc.checkVoidOrDiscardedBody(arm.Body)
		} else {
			hint := armExp
			if hint == nil && len(armTypes) > 0 {
				hint = armTypes[0]
			}
			aty = fc.checkExprExpect(arm.Body, hint, true)
			if aty == types.TypeVoid {
				fc.a.Rep.Errorf(arm.Body.GetSpan(), "`match` arm used as a value must produce a value")
			} else if aty != types.TypeNever && aty != types.TypeInvalid {
				armTypes = append(armTypes, aty)
				fc.consumeValue(arm.Body, aty, false)
			}
		}

		if lockedScrut != nil {
			fc.activeBorrows[lockedScrut]--
		}
		armAd := fc.arenaDepthOfExpr(arm.Body)
		if needValue && armAd > outerDepth {
			fc.a.Rep.Errorf(arm.Body.GetSpan(), "arena reference `ref[T]` cannot escape `match` arm scope")
		} else if needValue && armAd > tgt.maxYieldArenaDepth {
			tgt.maxYieldArenaDepth = armAd
		}
		fc.popScope()

		if aty != types.TypeNever {
			allDiverge = false
			liveBranchMoves = append(liveBranchMoves, fc.cloneMoves())
		}
	}

	// Exhaustiveness check when `else` had an explicit pattern (`else Status.Closed:`)!
	if !hasCatchAllElse {
		if !isEnum {
			if scrutTy.Equals(types.TypeBool) {
				var missingBool []string
				if !coveredVariants["true"] {
					missingBool = append(missingBool, "true")
				}
				if !coveredVariants["false"] {
					missingBool = append(missingBool, "false")
				}
				if len(missingBool) > 0 {
					fc.a.Rep.Errorf(me.Span, "non-exhaustive `match` on `bool`: missing %s", strings.Join(missingBool, ", "))
				}
			} else if scrutTy != types.TypeInvalid {
				fc.a.Rep.Errorf(me.Span, "`match` on non-enum type %s must end with an open catch-all `else:` arm", scrutTy.String())
			}
		} else {
			var missing []string
			for _, v := range enumScrut.Variants {
				if !coveredVariants[v.Name] {
					missing = append(missing, enumScrut.Name+"."+v.Name)
				}
			}
			if len(missing) > 0 {
				fc.a.Rep.Errorf(me.Span, "non-exhaustive `match` on enum %s: missing variant(s): %s",
					enumScrut.String(), strings.Join(missing, ", "))
			}
		}
	}

	if len(liveBranchMoves) > 0 {
		fc.moves = mergeMoveMaps(liveBranchMoves...)
	}

	if !needValue {
		if allDiverge && !tgt.hitValueExit {
			inf.Type = types.TypeNever
			return types.TypeNever
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	resTy := armExp
	if resTy == nil {
		resTy = unifyYieldTypes(append(tgt.yieldTypes, armTypes...))
	}
	for _, arm := range me.Arms {
		aty := fc.a.infoFor(arm.Body).Type
		if aty != nil && aty != types.TypeVoid && aty != types.TypeNever && aty != types.TypeInvalid {
			fc.verifyAssignable(aty, resTy, arm.Body, arm.Body.GetSpan(), "`match` arm")
		}
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

func (fc *funcChecker) checkMatchPattern(pat ast.Pattern, scrutTy types.Type, enumScrut *types.Enum, scrutArenaDepth int, covered map[string]bool) (hasBind, hasMut, hasOwnRes bool) {
	switch p := pat.(type) {
	case *ast.ExprPattern:
		pty := fc.checkExprExpect(p.Expr, scrutTy, true)
		if scrutTy == types.TypeInvalid || pty == types.TypeInvalid {
			return false, false, false
		}
		fc.verifyAssignable(pty, scrutTy, p.Expr, p.Span, "`match` pattern")
		if _, isNone := p.Expr.(*ast.NoneLit); !isNone && fc.a.infoFor(p.Expr).EnumVariant == nil && !types.SupportsEq(scrutTy) {
			fc.a.Rep.Errorf(p.Span, "cannot match value pattern of type `%s` because it does not support `==`", scrutTy.String())
		}
		if enumScrut != nil {
			if inf := fc.a.infoFor(p.Expr); inf.EnumVariant != nil {
				vname := inf.EnumVariant.Name
				if covered[vname] {
					fc.a.Rep.Errorf(p.Span, "duplicate enum variant `%s.%s` in `match`", enumScrut.Name, vname)
				}
				covered[vname] = true
			}
		} else if scrutTy.Equals(types.TypeBool) {
			if bl, ok := p.Expr.(*ast.BoolLit); ok {
				bkey := "false"
				if bl.Val {
					bkey = "true"
				}
				if covered[bkey] {
					fc.a.Rep.Errorf(p.Span, "duplicate boolean pattern `%s` in `match`", bkey)
				}
				covered[bkey] = true
			}
		}
		return false, false, false

	case *ast.RangePattern:
		loTy := fc.checkExprExpect(p.Low, scrutTy, true)
		hiTy := fc.checkExprExpect(p.High, scrutTy, true)
		fc.verifyAssignable(loTy, scrutTy, p.Low, p.Low.GetSpan(), "`match` range pattern start")
		fc.verifyAssignable(hiTy, scrutTy, p.High, p.High.GetSpan(), "`match` range pattern end")
		prim := types.UnderlyingPrimitive(scrutTy)
		if scrutTy != types.TypeInvalid && (prim == nil || (!prim.IsInteger() && prim.Kind != types.PrimChar)) {
			fc.a.Rep.Errorf(p.Span, "range pattern in `match` requires integer or `char` scrutinee, got %s", scrutTy.String())
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
		fc.a.infoFor(mem).EnumType = enumScrut
		fc.a.infoFor(mem).EnumVariant = v
		fc.a.infoFor(mem).Type = enumScrut

		if covered[v.Name] {
			fc.a.Rep.Errorf(p.Span, "duplicate enum variant `%s.%s` in `match`", enumScrut.Name, v.Name)
		}
		covered[v.Name] = true

		declarePatBinding := func(b ast.VariantPatternBinding, fty types.Type) *Symbol {
			if b.IsWildcard {
				return nil
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
			sym := fc.declareLocal(b.BindName, fty, isBind, bkind, b.Span)
			if typeMayHoldArenaRef(fty) {
				sym.ArenaDepth = scrutArenaDepth
			}
			return sym
		}

		syms := make([]*Symbol, len(p.Bindings))
		if !p.IsStruct {
			if v.Kind == types.VariantUnit {
				fc.a.Rep.Errorf(p.Span, "variant `%s.%s` has no payload; match with `when %s.%s:`", enumScrut.String(), v.Name, enumScrut.Name, v.Name)
				return false, false, false
			}
			if v.Kind == types.VariantStruct {
				fc.a.Rep.Errorf(p.Span, "variant `%s.%s` has named fields; destructure with `when %s.%s[...]:` instead of `(...)`", enumScrut.String(), v.Name, enumScrut.Name, v.Name)
				return false, false, false
			}
			if len(p.Bindings) != 1 {
				fc.a.Rep.Errorf(p.Span, "variant `%s.%s` expects 1 payload binding, got %d", enumScrut.String(), v.Name, len(p.Bindings))
				return false, false, false
			}
			syms[0] = declarePatBinding(p.Bindings[0], v.Payload)
		} else {
			if v.Kind != types.VariantStruct {
				fc.a.Rep.Errorf(p.Span, "variant `%s.%s` does not have named fields; cannot destructure with `[...]`", enumScrut.String(), v.Name)
				return false, false, false
			}
			seenFields := make(map[string]bool)
			for i, b := range p.Bindings {
				if b.IsWildcard && b.FieldName == "" {
					continue
				}
				if seenFields[b.FieldName] {
					fc.a.Rep.Errorf(b.Span, "duplicate field %q in variant pattern `%s.%s`", b.FieldName, enumScrut.String(), v.Name)
					continue
				}
				seenFields[b.FieldName] = true
				sf, _ := v.FieldByName(b.FieldName)
				if sf == nil {
					fc.a.Rep.Errorf(b.Span, "variant `%s.%s` has no field %q", enumScrut.String(), v.Name, b.FieldName)
					continue
				}
				syms[i] = declarePatBinding(b, sf.Type)
			}
		}
		if hasMut && hasOwnRes {
			fc.a.Rep.Errorf(p.Span, "cannot mix mutable borrow (`&`) and ownership move (`own`) in the same variant pattern")
		}
		fc.a.curBM.PatternSyms[p] = syms
		return hasBind, hasMut, hasOwnRes
	}
	return false, false, false
}
