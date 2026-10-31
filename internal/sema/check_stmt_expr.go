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
	cfFunc
)

type cfTarget struct {
	id           int
	kind         cfKind
	label        string
	funcDepth    int
	expectedType types.Type   // nil if inferred bottom-up
	isForNoElse  bool         // true when `for` has no `else`, so loop yields `?Elem` while `for <- v` supplies `Elem`
	yieldTypes   []types.Type // collected types from unwinds to unify/infer
	hitValueExit bool
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

	// 2. Check top-level `bind` constants
	a.curBM = a.Prog.TopBodyMaps
	for _, tv := range a.Prog.TopVars {
		fc := a.newFuncChecker(tv.Mod, nil)
		fc.pushScope()
		var got types.Type
		if tv.Sym.Type != nil {
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
		a.checkOptionalIdentRule(tv.Sym.Name, tv.Decl.IsOptName, tv.Sym.Type, tv.Decl.Span, "top-level bind")
		fc.popScope()
	}

	// 3. Check all concrete & monomorphized functions (growing dynamically as generics instantiate!)
	for i := 0; i < len(a.Prog.Funcs); i++ {
		fi := a.Prog.Funcs[i]
		a.checkFuncInstance(fi)
	}
}

func (a *Analyzer) newFuncChecker(mod *Module, subst map[string]types.Type) *funcChecker {
	return &funcChecker{
		a:               a,
		mod:             mod,
		subst:           subst,
		moves:           make(map[*Symbol]MoveState),
		activeBorrows:   make(map[*Symbol]int),
		mutUnwrapOrigin: make(map[*Symbol]*Symbol),
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
		id:           fnTargetID,
		kind:         cfFunc,
		funcDepth:    fc.funcDepth,
		expectedType: fi.Sig.Ret,
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
		sym := fc.lookupVar(v.Name)
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
	case *ast.CallExpr:
		inf := fc.a.infoFor(v)
		if inf.CallKind == CallBuiltinMethod && inf.BuiltinName == "Arena.alloc" && inf.MethodRecv != nil {
			if root := fc.rootSymbolOfExpr(inf.MethodRecv); root != nil && root.BindKind == BindOwned {
				return root.ScopeDepth
			}
		}
		// Propagate max arena depth of arguments if return type contains `ref[T]`
		if types.ContainsArenaRef(inf.Type) {
			maxD := 0
			if inf.MethodRecv != nil {
				if root := fc.rootSymbolOfExpr(inf.MethodRecv); root != nil && root.Type.Equals(types.TypeArena) && root.BindKind == BindOwned {
					maxD = root.ScopeDepth
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

	fc.pushScope()
	defer fc.popScope()

	var blkExp types.Type
	if needValue && expected != nil && expected != types.TypeVoid {
		blkExp = expected
	}
	tgt := &cfTarget{
		id:           id,
		kind:         cfBlock,
		label:        b.Label,
		funcDepth:    fc.funcDepth,
		expectedType: blkExp,
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
			if ad := fc.arenaDepthOfExpr(b.Tail); ad >= blockScopeDepth {
				fc.a.Rep.Errorf(b.Tail.GetSpan(), "arena reference `ref[T]` cannot escape the block scope of its `Arena`")
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
		if ad := fc.arenaDepthOfExpr(b.Tail); ad >= blockScopeDepth {
			fc.a.Rep.Errorf(b.Tail.GetSpan(), "arena reference `ref[T]` cannot escape the block scope of its `Arena`")
		}
		resTy := unifyYieldTypes(append(tgt.yieldTypes, tailTy))
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
		if t == types.TypeNever {
			return types.TypeNever
		}
		return types.TypeVoid
	}
	return types.TypeVoid
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
		en := &types.Enum{
			Name:       td.Name,
			CName:      fmt.Sprintf("ll_le_%s_%d", td.Name, fc.a.allocTargetID()),
			Underlying: types.TypeInt,
		}
		for idx, v := range td.Variants {
			en.Variants = append(en.Variants, types.EnumVariant{Name: v.Name, Value: int64(idx)})
		}
		fc.mod.Types[td.Name] = en
		fc.a.Prog.Enums = append(fc.a.Prog.Enums, en)
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
		if sym.BindKind == BindMutBorrow {
			// Wait: in `let x = 5; fn(&x)`, inside `fn(p: &int)`, does `p = 10` mutate `*p`?
			// YES! Because `&T` is a reference to the caller's `T` (just like `self.x = ...` or `p = 10` on `&int`),
			// unless `p` was declared with `if (&u?)` where `u` is `&T` mutating inside `u?`!
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

	if s.FallbackUnwind != nil {
		optTy := fc.checkExprExpect(s.Value, types.NewOptional(targetTy), true)
		_ = fc.checkUnwindStmt(s.FallbackUnwind)
		if o, ok := optTy.(*types.Optional); ok {
			fc.verifyAssignable(o.Elem, targetTy, nil, s.Value.GetSpan(), "assignment")
		} else if optTy != types.TypeInvalid {
			fc.a.Rep.Errorf(s.Value.GetSpan(), "left-hand side of `??` must be optional, got %s", optTy.String())
		}
		fc.consumeValue(s.Value, optTy, false)
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

		// Arena escape check: cannot assign `ref[T]` from an inner Arena into an outer variable!
		exprArenaDepth := fc.arenaDepthOfExpr(s.Value)
		if rootSym != nil && exprArenaDepth > 0 && rootSym.ScopeDepth < exprArenaDepth {
			fc.a.Rep.Errorf(s.Span, "arena reference `ref[T]` cannot escape into variable %q declared in an outer scope", rootSym.Name)
		}
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
	if tgt.kind == cfFor && tgt.isForNoElse && exp != nil {
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

		if tgt.kind == cfFunc && types.ContainsArenaRef(valTy) && !fc.hasMutArenaParam {
			fc.a.Rep.Errorf(u.Span, "cannot return `ref[T]` via `func <-` unless the function takes an `&Arena` parameter")
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
		case ast.UnwindFor, ast.UnwindNext:
			if cand.kind == cfFor {
				return cand
			}
		case ast.UnwindFunc:
			if cand.kind == cfFunc {
				return cand
			}
		case ast.UnwindLabel:
			if (cand.kind == cfFor || cand.kind == cfBlock) && cand.label == u.Label {
				return cand
			}
		case ast.UnwindNextLabel:
			if cand.kind == cfFor && cand.label == u.Label {
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
	case ast.UnwindFor, ast.UnwindNext:
		return "`for` loop"
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
		fc.checkSymReadable(sym, v.Span)
		// Check lambda capture if referencing a local from an enclosing function scope!
		if !sym.IsTopLevel && sym.DeclFuncDepth < fc.funcDepth {
			fc.recordLambdaCapture(sym, v.Span)
		}
		inf.Sym = sym
		inf.Type = sym.Type
		return sym.Type

	case *ast.UnaryExpr:
		return fc.checkUnaryExpr(v, expected)

	case *ast.BinaryExpr:
		return fc.checkBinaryExpr(v, expected)

	case *ast.MemberExpr:
		return fc.checkMemberExpr(v)

	case *ast.CallExpr:
		return fc.checkCallExpr(v, expected)

	case *ast.IndexExpr:
		return fc.checkIndexExpr(v)

	case *ast.SliceExpr:
		return fc.checkSliceExpr(v)

	case *ast.VecLit:
		return fc.checkVecOrArrayLit(v, expected)

	case *ast.RepeatArrayLit:
		return fc.checkRepeatArrayLit(v, expected)

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
		p := types.UnderlyingPrimitive(sub)
		if sub != types.TypeInvalid && (p == nil || (!p.IsSignedInt() && !p.IsFloat())) {
			fc.a.Rep.Errorf(u.Span, "unary `-` requires signed integer or float, got %s", sub.String())
		}
		inf.Type = sub
		return sub

	case token.KwNot:
		sub := fc.checkExprExpect(u.Operand, types.TypeBool, true)
		if sub != types.TypeInvalid && !sub.Equals(types.TypeBool) {
			fc.a.Rep.Errorf(u.Span, "`not` requires `bool` operand, got %s", sub.String())
		}
		inf.Type = types.TypeBool
		return types.TypeBool

	case token.Tilde:
		sub := fc.checkExprExpect(u.Operand, expected, true)
		p := types.UnderlyingPrimitive(sub)
		if sub != types.TypeInvalid && (p == nil || !p.IsInteger()) {
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
		if lty != types.TypeInvalid && rty != types.TypeInvalid {
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
			} else if !types.SupportsEq(lty) {
				fc.a.Rep.Errorf(b.Span, "equality `%s` is not defined on type %s", b.Op.String(), lty.String())
			}
		}
		inf.Type = types.TypeBool
		return types.TypeBool
	}

	// Ordering `<`, `<=`, `>`, `>=`
	if b.Op == token.Lt || b.Op == token.Lte || b.Op == token.Gt || b.Op == token.Gte {
		lty, rty := fc.checkOperandPairWithAdaptation(b.Left, b.Right, nil)
		if lty != types.TypeInvalid && rty != types.TypeInvalid {
			if !lty.Equals(rty) {
				fc.a.Rep.Errorf(b.Span, "cannot compare mismatched types %s and %s", lty.String(), rty.String())
			} else {
				p := types.UnderlyingPrimitive(lty)
				if p == nil || (!p.IsNumeric() && p.Kind != types.PrimChar && p.Kind != types.PrimString) {
					fc.a.Rep.Errorf(b.Span, "ordering operator `%s` is not defined on %s", b.Op.String(), lty.String())
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
		lp := types.UnderlyingPrimitive(lty)
		rp := types.UnderlyingPrimitive(rty)
		if lty != types.TypeInvalid && (lp == nil || !lp.IsInteger()) {
			fc.a.Rep.Errorf(b.Left.GetSpan(), "shift operand must be an integer, got %s", lty.String())
		}
		if rty != types.TypeInvalid && (rp == nil || !rp.IsInteger()) {
			fc.a.Rep.Errorf(b.Right.GetSpan(), "shift count must be an integer, got %s", rty.String())
		}
		inf.Type = lty
		return lty
	}

	// Arithmetic & Bitwise (`+`, `-`, `*`, `/`, `%`, `&`, `|`, `^`)
	lty, rty := fc.checkOperandPairWithAdaptation(b.Left, b.Right, expected)
	if lty == types.TypeInvalid || rty == types.TypeInvalid {
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

// checkMemberExpr resolves `Enum.Variant`, `mod.Symbol`, `struct.field`, `ref[Struct].field`,
// and optional dot-chaining (`user?.name`, `u?.address?.city`, `find_user(id)?.name`).
func (fc *funcChecker) checkMemberExpr(m *ast.MemberExpr) types.Type {
	inf := fc.a.infoFor(m)

	// 1. Check if `Base` is an `Enum` type name (`Status.Pending`) or imported module alias (`mod.val`)!
	if baseId, isId := m.Base.(*ast.IdentExpr); isId && !baseId.IsOpt {
		if fc.lookupVar(baseId.Name) == nil {
			// Check module alias first
			if targetMod, isMod := fc.mod.Aliased[baseId.Name]; isMod {
				if sym, ok := targetMod.Symbols[m.Member]; ok {
					inf.Sym = sym
					inf.Type = sym.Type
					return sym.Type
				}
				// Check `mod.EnumName` if chained further
				if en, isEn := targetMod.Types[m.Member].(*types.Enum); isEn {
					_ = en
				}
			}
			// Check Enum type (`Status.Pending`)
			if ty := fc.a.lookupTypeInModule(fc.mod, "", baseId.Name); ty != nil {
				if en, isEn := ty.(*types.Enum); isEn {
					v, ok := en.VariantByName(m.Member)
					if !ok {
						fc.a.Rep.Errorf(m.Span, "enum %s has no variant %q", en.Name, m.Member)
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
					inf.EnumType = en
					inf.EnumVariant = v
					inf.Type = en
					return en
				}
			}
		}
	}

	// Check `mod.Enum.Variant`
	if baseMem, isMem := m.Base.(*ast.MemberExpr); isMem {
		if modId, isId := baseMem.Base.(*ast.IdentExpr); isId && !modId.IsOpt {
			if targetMod, isMod := fc.mod.Aliased[modId.Name]; isMod {
				if en, isEn := targetMod.Types[baseMem.Member].(*types.Enum); isEn {
					v, ok := en.VariantByName(m.Member)
					if !ok {
						fc.a.Rep.Errorf(m.Span, "enum %s has no variant %q", en.Name, m.Member)
						inf.Type = types.TypeInvalid
						return types.TypeInvalid
					}
					inf.EnumType = en
					inf.EnumVariant = v
					inf.Type = en
					return en
				}
			}
		}
	}

	// 2. Evaluate base expression and handle optional chaining (Spec §7.2.3 & Decision B1)!
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
		// Enforce Decision B1: `?` MUST appear before `.` on optional expressions!
		// It appears via:
		// - `OptIdent` (`user?.name`)
		// - `QuestDot` (`find_user(id)?.name`)
		// - `MemberExpr` whose `MemberHasOpt` was true (`u?.address?.city`)!
		hasVisualQuestion := m.QuestDot
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
		fc.verifyAssignable(kTy, types.TypeInt, idx.Index, idx.Index.GetSpan(), "vector index")
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
				"cannot infer element type for empty vector literal `[]`",
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
		fc.verifyAssignable(et, elemTy, el, el.GetSpan(), "vector element")
		fc.consumeValue(el, elemTy, false)
	}
	res := &types.Vector{Elem: elemTy}
	inf.Type = res
	return res
}

func (fc *funcChecker) checkRepeatArrayLit(r *ast.RepeatArrayLit, expected types.Type) types.Type {
	inf := fc.a.infoFor(r)
	sz, ok := fc.a.evalConstInt(fc.mod, r.Count)
	if !ok || sz <= 0 {
		fc.a.Rep.Errorf(r.Count.GetSpan(), "repeat count in `[N: value]` must be a positive compile-time integer constant")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	var elemHint types.Type
	if arr, isArr := expected.(*types.StackArray); isArr {
		elemHint = arr.Elem
	}
	valTy := fc.checkExprExpect(r.Value, elemHint, true)
	if sz > 1 && !fc.a.supportsCopy(valTy) {
		fc.a.Rep.Errorf(r.Value.GetSpan(), "repeated element in `[%d: value]` must support `.copy()`", sz)
	} else {
		fc.consumeValue(r.Value, valTy, false)
	}
	res := &types.StackArray{Size: sz, Elem: valTy}
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
	if elemTy != nil && elemTy != types.TypeInvalid && !types.IsHashable(elemTy) {
		fc.a.Rep.Errorf(s.Span, "set element type %s is not hashable", elemTy.String())
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
	if kTy != nil && kTy != types.TypeInvalid && !types.IsHashable(kTy) {
		fc.a.Rep.Errorf(m.Span, "map key type %s is not hashable", kTy.String())
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
		} else if expSt, ok := expected.(*types.Struct); ok && expSt.Name == sl.TypeName && len(expSt.TypeArgs) == len(genSt.Decl.TypeParams) {
			st = expSt
		} else {
			// Infer type arguments from field expressions (`Pair[first: 1, second: "hi"]`)!
			st = fc.inferGenericStructLit(genSt, sl)
			if st == nil {
				inf.Type = types.TypeInvalid
				return types.TypeInvalid
			}
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

func (fc *funcChecker) inferGenericStructLit(genSt *GenericStructTemplate, sl *ast.StructLit) *types.Struct {
	fieldTypes := make(map[string]types.Type)
	for _, fi := range sl.Fields {
		fieldTypes[fi.Name] = fc.checkExpr(fi.Value, true)
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
		paramSyms = append(paramSyms, sym)
		fc.moves[sym] = MoveAlive
	}

	lamTargetID := fc.a.allocTargetID()
	fc.cfStack = append(fc.cfStack, &cfTarget{
		id:           lamTargetID,
		kind:         cfFunc,
		funcDepth:    fc.funcDepth,
		expectedType: ret,
	})

	if ret.Equals(types.TypeVoid) {
		_ = fc.checkVoidOrDiscardedBody(fl.Body)
	} else {
		bty := fc.checkExprExpect(fl.Body, ret, true)
		if bty == types.TypeVoid {
			fc.a.Rep.Errorf(fl.Body.GetSpan(), "lambda must return a value of type %s on all paths", ret.String())
		} else {
			fc.verifyAssignable(bty, ret, fl.Body, fl.Body.GetSpan(), "lambda return")
			fc.consumeValue(fl.Body, ret, false)
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
	for _, c := range lf.order {
		if !c.Type.IsPlain() {
			if c.OuterSym.BindKind != BindOwned {
				fc.a.Rep.ErrorWithHint(
					fl.Span,
					fmt.Sprintf("lambda cannot capture borrowed Resource parameter %q", c.Name),
					fmt.Sprintf("bind an explicit copy first (`bind %s_copy = %s.copy()`) and capture that", c.Name, c.Name),
				)
			} else {
				fc.moves[c.OuterSym] = MoveMoved
				c.Moved = true
			}
		}
		caps = append(caps, *c)
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
				DeclFuncDepth: fc.funcDepth,
				Span:          ie.Span,
			}
			fc.a.curBM.IfUnwrapSyms[ie] = unwrapSym
		}
	}

	tgt := &cfTarget{
		id:           id,
		kind:         cfIf,
		funcDepth:    fc.funcDepth,
		expectedType: branchExp,
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

func (fc *funcChecker) checkForExpr(fe *ast.ForExpr, expected types.Type, needValue bool) types.Type {
	inf := fc.a.infoFor(fe)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	// Determine loop element type if expected from context:
	// Spec 0.1 / §10.2:
	// - With `else` -> type `T`
	// - Without `else` (when `needValue`) -> type `?T`!
	var elemExp types.Type
	if needValue && expected != nil && expected != types.TypeVoid {
		if fe.Else != nil {
			elemExp = expected
		} else if opt, ok := expected.(*types.Optional); ok {
			elemExp = opt.Elem
		} else {
			// E.g. `let bad = for (...) { ... }` without `else` when `expected` was non-optional, or `return for (...)`
			elemExp = expected
		}
	}

	var lockedCol *Symbol
	var sym1, sym2 *Symbol
	isInfiniteTrue := false

	switch fe.Kind {
	case ast.ForCond:
		if bl, ok := fe.CondOrIter.(*ast.BoolLit); ok && bl.Val {
			isInfiniteTrue = true
		}
		cty := fc.checkExprExpect(fe.CondOrIter, types.TypeBool, true)
		if cty != types.TypeInvalid && !cty.Equals(types.TypeBool) {
			fc.a.Rep.Errorf(fe.CondOrIter.GetSpan(), "`for` condition must be `bool`, got %s", cty.String())
		}

	case ast.ForRange:
		loTy := fc.checkExprExpect(fe.CondOrIter, types.TypeInt, true)
		hiTy := fc.checkExprExpect(fe.RangeEnd, loTy, true)
		p := types.UnderlyingPrimitive(loTy)
		if loTy != types.TypeInvalid && (p == nil || !p.IsInteger() || !loTy.Equals(hiTy)) {
			fc.a.Rep.Errorf(fe.Span, "range bounds in `for (%s in a..b)` must be matching integer types, got %s and %s",
				fe.Var1, loTy.String(), hiTy.String())
		}
		if fe.Var2 != "" || fe.Var1Mut {
			fc.a.Rep.Errorf(fe.Span, "range loop only binds a single read-only variable (`for (i in lo..hi)`)")
		}
		fc.a.checkOptionalIdentRule(fe.Var1, fe.Var1IsOpt, loTy, fe.Span, "loop variable")
		sym1 = &Symbol{
			Name:          fe.Var1,
			CName:         fmt.Sprintf("%s_loop_%d", types.SanitizeCIdent(fe.Var1), id),
			Type:          loTy,
			IsBind:        true,
			BindKind:      BindOwned,
			ScopeDepth:    fc.curDepth() + 1,
			DeclFuncDepth: fc.funcDepth,
			Span:          fe.Span,
		}

	case ast.ForIter:
		colTy := fc.checkExpr(fe.CondOrIter, true)
		lockedCol = fc.rootSymbolOfExpr(fe.CondOrIter)
		if (fe.Var1Mut || fe.Var2Mut) && colTy != types.TypeInvalid {
			_ = fc.checkMutablePlace(fe.CondOrIter, "mutably iterate over")
		}
		sym1, sym2 = fc.resolveForIterVars(fe, colTy, id)
	}

	fc.a.curBM.ForSymbols[fe] = [2]*Symbol{sym1, sym2}

	var tgtExp types.Type
	if !needValue {
		tgtExp = types.TypeVoid
	} else if elemExp != nil {
		if fe.Else == nil {
			tgtExp = types.NewOptional(elemExp)
		} else {
			tgtExp = elemExp
		}
	}

	tgt := &cfTarget{
		id:           id,
		kind:         cfFor,
		label:        fe.Label,
		funcDepth:    fc.funcDepth,
		expectedType: tgtExp,
		isForNoElse:  fe.Else == nil,
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
		bodyTy = fc.checkVoidOrDiscardedBody(fe.Body)
	} else {
		// Note: A search loop (`let found? = for (x in list) { if (x > 10) for <- x }`)
		// has a `void` body that yields its value solely via `for <- x`!
		bodyTy = fc.checkLoopBodyExpr(fe.Body, elemExp)
	}

	if lockedCol != nil {
		fc.activeBorrows[lockedCol]--
	}
	fc.popScope()

	// Rule C4: Moving an outer variable inside a loop body without reassigning it before the next iteration is an error!
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

	if !needValue {
		fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)
		if isInfiniteTrue && !tgt.hitValueExit {
			// Check if any `for <-` broke out of this infinite loop:
			inf.Type = types.TypeVoid
			return types.TypeVoid
		}
		inf.Type = types.TypeVoid
		return types.TypeVoid
	}

	// Value-producing `for` loop!
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
		fc.verifyAssignable(bodyTy, inferredElem, fe.Body, fe.Body.GetSpan(), "`for` iteration value")
		fc.consumeValue(fe.Body, inferredElem, false)
	}

	if fe.Else == nil {
		fc.moves = mergeMoveMaps(preLoopMoves, postBodyMoves)
		if inferredElem == nil {
			fc.a.Rep.Errorf(fe.Span, "`for` loop used as a value produces no iteration value and has no `for <- val` exit")
			inf.Type = types.TypeInvalid
			return types.TypeInvalid
		}
		resOpt := types.NewOptional(inferredElem)
		inf.Type = resOpt
		return resOpt
	}

	// Has `else` clause -> guaranteed type `T`!
	fc.moves = fc.cloneMovesFrom(preLoopMoves)
	fc.pushScope()
	elseTy := fc.checkExprExpect(fe.Else, inferredElem, true)
	fc.popScope()
	elseMoves := fc.cloneMoves()
	fc.moves = mergeMoveMaps(postBodyMoves, elseMoves)

	if inferredElem == nil {
		inferredElem = elseTy
	}
	if inferredElem == nil || inferredElem == types.TypeVoid {
		fc.a.Rep.Errorf(fe.Span, "`for ... else` used as a value must produce a non-void value")
		inf.Type = types.TypeInvalid
		return types.TypeInvalid
	}
	fc.verifyAssignable(elseTy, inferredElem, fe.Else, fe.Else.GetSpan(), "`for ... else` fallback")
	fc.consumeValue(fe.Else, inferredElem, false)
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
	case *ast.BlockExpr:
		return v.Tail == nil || isSearchStmtBody(v.Tail)
	case *ast.UnsafeExpr:
		return isSearchStmtBody(v.Expr)
	default:
		return false
	}
}

func (fc *funcChecker) resolveForIterVars(fe *ast.ForExpr, colTy types.Type, id int) (*Symbol, *Symbol) {
	if r, ok := colTy.(*types.Ref); ok {
		colTy = r.Elem
	}
	mkSym := func(name string, isOptName bool, ty types.Type, mut bool) *Symbol {
		fc.a.checkOptionalIdentRule(name, isOptName, ty, fe.Span, "loop variable")
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
			Span:          fe.Span,
		}
	}

	switch c := colTy.(type) {
	case *types.Vector:
		if fe.Var2 == "" {
			return mkSym(fe.Var1, fe.Var1IsOpt, c.Elem, fe.Var1Mut), nil
		}
		return mkSym(fe.Var1, fe.Var1IsOpt, types.TypeInt, false), mkSym(fe.Var2, fe.Var2IsOpt, c.Elem, fe.Var2Mut)

	case *types.StackArray:
		if fe.Var2 == "" {
			return mkSym(fe.Var1, fe.Var1IsOpt, c.Elem, fe.Var1Mut), nil
		}
		return mkSym(fe.Var1, fe.Var1IsOpt, types.TypeInt, false), mkSym(fe.Var2, fe.Var2IsOpt, c.Elem, fe.Var2Mut)

	case *types.Primitive:
		if c.Kind == types.PrimString {
			if fe.Var1Mut || fe.Var2Mut {
				fc.a.Rep.Errorf(fe.Span, "cannot mutably iterate UTF-8 characters of a `string` in place")
			}
			if fe.Var2 == "" {
				return mkSym(fe.Var1, fe.Var1IsOpt, types.TypeChar, false), nil
			}
			return mkSym(fe.Var1, fe.Var1IsOpt, types.TypeInt, false), mkSym(fe.Var2, fe.Var2IsOpt, types.TypeChar, false)
		}

	case *types.Set:
		if fe.Var1Mut || fe.Var2 != "" {
			fc.a.Rep.Errorf(fe.Span, "set iteration only supports a single read-only element (`for (item in my_set)`)")
		}
		return mkSym(fe.Var1, fe.Var1IsOpt, c.Elem, false), nil

	case *types.Map:
		if fe.Var2 == "" {
			fc.a.Rep.Errorf(fe.Span, "map iteration requires key and value variables: `for (k, v in my_map)`")
			return mkSym(fe.Var1, fe.Var1IsOpt, c.Key, false), nil
		}
		return mkSym(fe.Var1, fe.Var1IsOpt, c.Key, false), mkSym(fe.Var2, fe.Var2IsOpt, c.Value, fe.Var2Mut)
	}

	if colTy != types.TypeInvalid {
		fc.a.Rep.Errorf(fe.CondOrIter.GetSpan(), "cannot iterate over value of type %s", colTy.String())
	}
	return nil, nil
}

func (fc *funcChecker) checkMatchExpr(me *ast.MatchExpr, expected types.Type, needValue bool) types.Type {
	inf := fc.a.infoFor(me)
	id := fc.a.allocTargetID()
	inf.TargetID = id

	scrutTy := fc.checkExpr(me.Scrutinee, true)
	var armExp types.Type
	if needValue && expected != nil && expected != types.TypeVoid {
		armExp = expected
	}

	tgt := &cfTarget{
		id:           id,
		kind:         cfMatch,
		funcDepth:    fc.funcDepth,
		expectedType: armExp,
	}
	fc.cfStack = append(fc.cfStack, tgt)
	defer func() { fc.cfStack = fc.cfStack[:len(fc.cfStack)-1] }()

	// Track enum variant coverage for exhaustive `else Enum.FinalVariant:` (Spec §10.3 / B3)!
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
		for _, pat := range arm.Patterns {
			fc.checkMatchPattern(pat, scrutTy, enumScrut, coveredVariants)
		}

		fc.moves = fc.cloneMovesFrom(preMoves)
		fc.pushScope()
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
			}
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
			// Check bool exhaustiveness (`when true: ... else false: ...`) too!
			if scrutTy.Equals(types.TypeBool) {
				// Valid if both true & false covered
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
					enumScrut.Name, strings.Join(missing, ", "))
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
			fc.consumeValue(arm.Body, resTy, false)
		}
	}
	if allDiverge && !tgt.hitValueExit {
		inf.Type = types.TypeNever
		return types.TypeNever
	}
	inf.Type = resTy
	return resTy
}

func (fc *funcChecker) checkMatchPattern(pat ast.Pattern, scrutTy types.Type, enumScrut *types.Enum, covered map[string]bool) {
	switch p := pat.(type) {
	case *ast.ExprPattern:
		pty := fc.checkExprExpect(p.Expr, scrutTy, true)
		if scrutTy == types.TypeInvalid || pty == types.TypeInvalid {
			return
		}
		fc.verifyAssignable(pty, scrutTy, p.Expr, p.Span, "`match` pattern")
		if enumScrut != nil {
			if inf := fc.a.infoFor(p.Expr); inf.EnumVariant != nil {
				vname := inf.EnumVariant.Name
				if covered[vname] {
					fc.a.Rep.Errorf(p.Span, "duplicate enum variant `%s.%s` in `match`", enumScrut.Name, vname)
				}
				covered[vname] = true
			}
		}

	case *ast.RangePattern:
		loTy := fc.checkExprExpect(p.Low, scrutTy, true)
		hiTy := fc.checkExprExpect(p.High, scrutTy, true)
		fc.verifyAssignable(loTy, scrutTy, p.Low, p.Low.GetSpan(), "`match` range pattern start")
		fc.verifyAssignable(hiTy, scrutTy, p.High, p.High.GetSpan(), "`match` range pattern end")
		prim := types.UnderlyingPrimitive(scrutTy)
		if scrutTy != types.TypeInvalid && (prim == nil || (!prim.IsInteger() && prim.Kind != types.PrimChar)) {
			fc.a.Rep.Errorf(p.Span, "range pattern in `match` requires integer or `char` scrutinee, got %s", scrutTy.String())
		}
	}
}
