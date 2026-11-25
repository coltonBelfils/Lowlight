package sema

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

const (
	defaultComptimeStepLimit = 1_000_000
	defaultComptimeMaxDepth  = 1024
	defaultComptimeMaxMem    = 64 * 1024 * 1024 // 64 MB
	maxComptimeGoDepth       = 8192
	maxComptimeValueDepth    = 256
	ctValMemCost             = 232
)

type ctKind int

const (
	ctVoid ctKind = iota
	ctInt
	ctFloat
	ctBool
	ctChar
	ctString
	ctList
	ctArray
	ctSet
	ctMap
	ctTuple
	ctStruct
	ctEnum
	ctOpt
	ctFn
	ctRange
	ctPointer
)

type ctMapEntry struct {
	key *ctVal
	val *ctVal
}

type ctReflectMeta struct {
	TargetType types.Type
	Index      int
	Kind       string // "field", "variant", or "elem"
}

type ctStructBox struct {
	ty         *types.Struct
	fields     map[string]*ctVal
	fieldOrder []string
	refl       *ctReflectMeta
}

type ctListBox struct {
	elemTy types.Type
	elems  []*ctVal
}

type ctArrayBox struct {
	elemTy types.Type
	elems  []*ctVal
	alloc  *ctHeapAlloc
}

type ctHeapAlloc struct {
	id           int64
	baseAddr     uint64
	elemTy       types.Type
	elemSize     int64
	isBytes      bool
	bytes        []byte
	cells        []*ctVal
	freed        bool
	readOnly     bool
	chargedBytes int64
}

type ctPtr struct {
	alloc    *ctHeapAlloc
	byteOff  int64
	elemSize int64
	elemTy   types.Type
	place    *ctPlace
	castTy   types.Type
}

type ctTupleBox struct {
	elems []*ctVal
}

type ctSetBox struct {
	elemTy types.Type
	elems  []*ctVal
}

type ctMapBox struct {
	keyTy   types.Type
	valTy   types.Type
	entries []ctMapEntry
}

type ctRangeBox struct {
	low       uint64
	high      uint64
	inclusive bool
	prim      *types.Primitive
}

// ctVal represents any compile-time value in the Lowlight comptime evaluator.
type ctVal struct {
	kind       ctKind
	ty         types.Type
	u          uint64
	f          float64
	prim       *types.Primitive
	b          bool
	ch         rune
	s          string
	sAlloc     *ctHeapAlloc
	sOff       int64
	sCap       int64
	ptrVal     *ctPtr
	list       *ctListBox
	arr        *ctArrayBox
	set        *ctSetBox
	m          *ctMapBox
	tup        *ctTupleBox
	st         *ctStructBox
	en         *types.Enum
	variantIdx int
	payload    *ctVal
	hasOpt     bool
	optElem    *ctVal
	fn         *FuncInstance
	lambda     *LambdaInfo
	captures   map[*Symbol]*ctSlot
	rng        *ctRangeBox
}

type ctSlot struct {
	val      *ctVal
	ty       types.Type
	moved    bool
	refPlace *ctPlace // non-nil when bound to a `&T` / `&self` reference parameter
}

func (sl *ctSlot) getVal(span diag.Span, name string) (*ctVal, ctSignal) {
	if sl == nil {
		return nil, abortSig(span, "uninitialized slot %q", name)
	}
	if sl.refPlace != nil {
		return sl.refPlace.get()
	}
	if sl.moved {
		return nil, abortSig(span, "use of moved variable %q at compile time", name)
	}
	if sl.val == nil {
		return nil, abortSig(span, "uninitialized variable %q", name)
	}
	return sl.val, okSig()
}

func (sl *ctSlot) setVal(nv *ctVal) ctSignal {
	if sl == nil {
		return okSig()
	}
	if sl.refPlace != nil {
		return sl.refPlace.set(nv)
	}
	sl.val = nv
	sl.moved = false
	return okSig()
}

type ctPlace struct {
	get func() (*ctVal, ctSignal)
	set func(*ctVal) ctSignal
}

type ctArg struct {
	val   *ctVal
	place *ctPlace
}

func (a ctArg) getVal(span diag.Span) (*ctVal, ctSignal) {
	if a.place != nil {
		return a.place.get()
	}
	return a.val, okSig()
}

func (a ctArg) setVal(nv *ctVal, span diag.Span) ctSignal {
	if a.place != nil {
		return a.place.set(nv)
	}
	_ = span
	return okSig()
}

func valArgs(vals ...*ctVal) []ctArg {
	out := make([]ctArg, len(vals))
	for i, v := range vals {
		out[i] = ctArg{val: v}
	}
	return out
}

type ctSigKind int

const (
	sigNone ctSigKind = iota
	sigUnwind
	sigAbort
)

type ctSignal struct {
	kind       ctSigKind
	targetKind ast.UnwindTargetKind
	targetID   int
	label      string
	isContinue bool
	isDefault  bool
	val        *ctVal
	errMsg     string
	errSpan    diag.Span
	trace      []ctFrame
}

func okSig() ctSignal { return ctSignal{} }

func abortSig(span diag.Span, format string, args ...any) ctSignal {
	return ctSignal{
		kind:    sigAbort,
		errMsg:  fmt.Sprintf(format, args...),
		errSpan: span,
	}
}

type ctFrame struct {
	fi       *FuncInstance
	lambda   *LambdaInfo
	rootName string
	mod      *Module
	bm       *BodyMaps
	subst    map[string]types.Type
	callSpan diag.Span
	locals   map[*Symbol]*ctSlot
	captures map[*Symbol]*ctSlot
}

type ctState struct {
	a            *Analyzer
	rootMod      *Module
	stepLimit    int
	steps        int
	depth        int
	goDepth      int
	memBytes     int64
	memLimit     int64
	frames       []ctFrame
	// scopeStack tracks local symbols whose custom Drop needs to run on block exit.
	dropScopes [][]dropEntry
}

type dropEntry struct {
	sym  *Symbol
	slot *ctSlot
	ty   types.Type
}

func newCtState(a *Analyzer, rootMod *Module, stepLimit int) *ctState {
	if stepLimit <= 0 {
		stepLimit = defaultComptimeStepLimit
	}
	return &ctState{
		a:         a,
		rootMod:   rootMod,
		stepLimit: stepLimit,
		memLimit:  defaultComptimeMaxMem,
	}
}

func (s *ctState) step(span diag.Span) ctSignal {
	s.steps++
	if s.steps > s.stepLimit {
		return abortSig(span, "compile-time execution exceeded step limit (%d steps; increase with `@comptime_limit(steps: N)`)", s.stepLimit)
	}
	return okSig()
}

func (s *ctState) allocMem(bytes int64, span diag.Span) ctSignal {
	if bytes <= 0 {
		return okSig()
	}
	limit := s.memLimit
	if limit <= 0 {
		limit = defaultComptimeMaxMem
	}
	s.memBytes += bytes
	if s.memBytes > limit {
		return abortSig(span, "compile-time execution exceeded memory budget (%d MB; increase with `@comptime_limit(memory: N)`)", limit/(1024*1024))
	}
	return okSig()
}

func (s *ctState) freeMem(bytes int64) {
	if bytes <= 0 {
		return
	}
	s.memBytes -= bytes
	if s.memBytes < 0 {
		s.memBytes = 0
	}
}

func (s *ctState) enterGoDepth(span diag.Span) ctSignal {
	s.goDepth++
	if s.goDepth > maxComptimeGoDepth {
		s.goDepth--
		return abortSig(span, "compile-time evaluator recursion limit exceeded")
	}
	return okSig()
}

func (s *ctState) leaveGoDepth() {
	if s.goDepth > 0 {
		s.goDepth--
	}
}

func (s *ctState) isCallable(fi *FuncInstance) bool {
	if fi == nil {
		return false
	}
	return s.a.isComptimeCallable(fi)
}

func (s *ctState) estimateValBytes(v *ctVal, depth int) int64 {
	if v == nil || depth > 32 {
		return 0
	}
	switch v.kind {
	case ctString:
		if v.sAlloc != nil {
			// Buffer bytes already tracked via ctHeapAlloc.
			return s.a.typeSize(types.TypeString)
		}
		return s.a.typeSize(types.TypeString) + int64(len(v.s))
	case ctList:
		if v.list == nil {
			return 24
		}
		tot := int64(24)
		for _, el := range v.list.elems {
			tot += s.estimateValBytes(el, depth+1)
		}
		return tot
	case ctArray:
		if v.arr == nil {
			return 0
		}
		var tot int64
		for _, el := range v.arr.elems {
			tot += s.estimateValBytes(el, depth+1)
		}
		return tot
	case ctSet:
		if v.set == nil {
			return 24
		}
		tot := int64(24)
		for _, el := range v.set.elems {
			tot += s.estimateValBytes(el, depth+1)
		}
		return tot
	case ctMap:
		if v.m == nil {
			return 24
		}
		tot := int64(24)
		for _, ent := range v.m.entries {
			tot += s.estimateValBytes(ent.key, depth+1) + s.estimateValBytes(ent.val, depth+1)
		}
		return tot
	case ctTuple:
		if v.tup == nil {
			return 0
		}
		var tot int64
		for _, el := range v.tup.elems {
			tot += s.estimateValBytes(el, depth+1)
		}
		return tot
	case ctStruct:
		if v.st == nil {
			return 0
		}
		if v.st.ty != nil && (v.st.ty.Lang == "list" || v.st.ty.Lang == "map" || v.st.ty.Lang == "set") {
			// Heap backing already accounted in ctHeapAlloc.
			return s.a.typeSize(v.st.ty)
		}
		if v.ty != nil {
			return s.simElemSize(v.ty)
		}
		return 0
	}
	if v.ty != nil {
		return s.simElemSize(v.ty)
	}
	return 8
}

func (s *ctState) allocCloneMem(v *ctVal, span diag.Span) ctSignal {
	if v == nil {
		return okSig()
	}
	switch v.kind {
	case ctList, ctSet, ctMap, ctArray, ctStruct, ctTuple:
		return s.allocMem(s.estimateValBytes(v, 0), span)
	}
	return okSig()
}

func (s *ctState) frame() *ctFrame {
	if len(s.frames) == 0 {
		return nil
	}
	return &s.frames[len(s.frames)-1]
}

func (s *ctState) infoFor(e ast.Expr) *ExprInfo {
	if f := s.frame(); f != nil && f.bm != nil {
		if inf, ok := f.bm.ExprInfos[e]; ok {
			return inf
		}
	}
	if s.a.Prog.TopBodyMaps != nil {
		if inf, ok := s.a.Prog.TopBodyMaps.ExprInfos[e]; ok {
			return inf
		}
	}
	return nil
}

func (s *ctState) varSymFor(vs *ast.VarStmt) *Symbol {
	if f := s.frame(); f != nil && f.bm != nil {
		if sym := f.bm.VarSymbols[vs]; sym != nil {
			return sym
		}
	}
	if s.a.Prog.TopBodyMaps != nil {
		return s.a.Prog.TopBodyMaps.VarSymbols[vs]
	}
	return nil
}

func (s *ctState) unpackSymsFor(vs *ast.VarStmt) []*Symbol {
	if f := s.frame(); f != nil && f.bm != nil {
		if syms := f.bm.UnpackVarSymbols[vs]; syms != nil {
			return syms
		}
	}
	if s.a.Prog.TopBodyMaps != nil {
		return s.a.Prog.TopBodyMaps.UnpackVarSymbols[vs]
	}
	return nil
}

func (s *ctState) unwindInfoFor(uw *ast.UnwindStmt) *UnwindInfo {
	if f := s.frame(); f != nil && f.bm != nil {
		if inf := f.bm.UnwindInfos[uw]; inf != nil {
			return inf
		}
	}
	if s.a.Prog.TopBodyMaps != nil {
		return s.a.Prog.TopBodyMaps.UnwindInfos[uw]
	}
	return nil
}

func isStdLibFile(file string) bool {
	if file == "" {
		return false
	}
	slashed := filepath.ToSlash(file)
	return strings.Contains(slashed, "/std/") || strings.HasPrefix(slashed, "std/")
}

func ctFrameDisplayName(fr ctFrame) string {
	if fr.fi != nil {
		return fr.fi.Name
	}
	if fr.lambda != nil {
		return "<lambda>"
	}
	if fr.rootName != "" {
		return fr.rootName
	}
	return "<comptime>"
}

func (s *ctState) reportAbort(sig ctSignal, rootSpan diag.Span) {
	origSpan := sig.errSpan
	if origSpan.Start.File == "" {
		origSpan = rootSpan
	}
	frames := sig.trace
	if len(frames) == 0 {
		frames = s.frames
	}

	primarySpan := origSpan
	var related []diag.RelatedInfo

	// If execution aborted inside std/, lift the primary diagnostic span to the innermost
	// user call site when one exists so editor red squiggles land in user code.
	emitTop := len(frames) - 1
	if isStdLibFile(origSpan.Start.File) {
		for i := len(frames) - 1; i >= 0; i-- {
			if frames[i].callSpan.Start.File != "" && !isStdLibFile(frames[i].callSpan.Start.File) {
				primarySpan = frames[i].callSpan
				related = append(related, diag.RelatedInfo{
					Span:    origSpan,
					Message: fmt.Sprintf("aborted here: %s", sig.errMsg),
				})
				emitTop = i - 1
				break
			}
		}
	}

	// Walk active call frames from innermost to outermost, collapsing runs of consecutive
	// identical recursive functions so deep recursion traces stay readable.
	const maxTraceNotes = 24
	notesEmitted := 0
	for i := emitTop; i >= 1 && notesEmitted < maxTraceNotes; {
		fr := frames[i]
		runCount := 1
		if fr.fi != nil {
			for j := i - 1; j >= 1; j-- {
				if frames[j].fi == fr.fi {
					runCount++
				} else {
					break
				}
			}
		}
		callee := ctFrameDisplayName(fr)
		caller := ctFrameDisplayName(frames[i-runCount])
		var msg string
		if fr.callSpan.Start.File != "" {
			msg = fmt.Sprintf("called from `%s` (while executing `%s`)", caller, callee)
		} else {
			msg = fmt.Sprintf("in `%s`", callee)
		}
		related = append(related, diag.RelatedInfo{
			Span:    fr.callSpan,
			Message: msg,
		})
		notesEmitted++
		if runCount > 2 {
			related = append(related, diag.RelatedInfo{
				Span:    frames[i-runCount+1].callSpan,
				Message: fmt.Sprintf("... %d more calls to `%s` ...", runCount-1, callee),
			})
			notesEmitted++
		} else if runCount == 2 {
			innerCaller := ctFrameDisplayName(frames[i-2])
			related = append(related, diag.RelatedInfo{
				Span:    frames[i-1].callSpan,
				Message: fmt.Sprintf("called from `%s` (while executing `%s`)", innerCaller, callee),
			})
			notesEmitted++
		}
		i -= runCount
	}

	s.a.Rep.Report(diag.Diagnostic{
		Severity: diag.Error,
		Span:     primarySpan,
		Message:  sig.errMsg,
		Related:  related,
	})
}

// -----------------------------------------------------------------------------
// Value Constructors, Bit-Width Arithmetic, & Equality/Hash/String Parity
// -----------------------------------------------------------------------------

func primBitWidth(p *types.Primitive) int {
	if p == nil {
		return 64
	}
	switch p.Kind {
	case types.PrimI8, types.PrimU8:
		return 8
	case types.PrimI16, types.PrimU16:
		return 16
	case types.PrimI32, types.PrimU32, types.PrimChar:
		return 32
	default:
		return 64
	}
}

func widthMask(bits int) uint64 {
	if bits >= 64 {
		return math.MaxUint64
	}
	return (uint64(1) << uint(bits)) - 1
}

func maskToPrim(u uint64, p *types.Primitive) uint64 {
	return u & widthMask(primBitWidth(p))
}

func signExtend(u uint64, bits int) int64 {
	if bits <= 0 || bits >= 64 {
		return int64(u)
	}
	u &= widthMask(bits)
	signBit := uint64(1) << uint(bits-1)
	if u&signBit != 0 {
		return int64(u | ^widthMask(bits))
	}
	return int64(u)
}

func newCtInt(u uint64, p *types.Primitive) *ctVal {
	if p == nil {
		p = types.TypeInt
	}
	return &ctVal{kind: ctInt, ty: p, prim: p, u: maskToPrim(u, p)}
}

func newCtSignedInt(v int64, p *types.Primitive) *ctVal {
	return newCtInt(uint64(v), p)
}

func newCtFloat(f float64, p *types.Primitive) *ctVal {
	if p == nil {
		p = types.TypeFloat
	}
	if p.Kind == types.PrimF32 {
		f = float64(float32(f))
	}
	return &ctVal{kind: ctFloat, ty: p, prim: p, f: f}
}

func newCtBool(b bool) *ctVal {
	return &ctVal{kind: ctBool, ty: types.TypeBool, b: b}
}

func newCtChar(ch rune) *ctVal {
	return &ctVal{kind: ctChar, ty: types.TypeChar, ch: ch}
}

func newCtString(s string) *ctVal {
	return &ctVal{kind: ctString, ty: types.TypeString, s: s}
}

func newCtVoid() *ctVal {
	return &ctVal{kind: ctVoid, ty: types.TypeVoid}
}

func newCtNone(ty types.Type) *ctVal {
	return &ctVal{kind: ctOpt, ty: ty, hasOpt: false}
}

func newCtSome(ty types.Type, inner *ctVal) *ctVal {
	if inner != nil && inner.kind == ctOpt {
		return inner
	}
	return &ctVal{kind: ctOpt, ty: ty, hasOpt: true, optElem: inner}
}

func (v *ctVal) asInt64() int64 {
	if v == nil {
		return 0
	}
	if v.kind == ctChar {
		return int64(v.ch)
	}
	if v.kind == ctBool {
		if v.b {
			return 1
		}
		return 0
	}
	if v.kind == ctEnum && v.en != nil && !v.en.IsTagged && v.variantIdx >= 0 && v.variantIdx < len(v.en.Variants) {
		return v.en.Variants[v.variantIdx].Value
	}
	if v.prim != nil && !v.prim.IsSignedInt() {
		return int64(v.u)
	}
	return signExtend(v.u, primBitWidth(v.prim))
}

func (v *ctVal) asUint64() uint64 {
	if v == nil {
		return 0
	}
	if v.kind == ctChar {
		return uint64(v.ch)
	}
	return maskToPrim(v.u, v.prim)
}

func (v *ctVal) wrapResult(ty types.Type) *ctVal {
	if v == nil {
		return nil
	}
	if ty == nil {
		return v
	}
	if opt, ok := ty.(*types.Optional); ok && v.kind != ctOpt {
		return newCtSome(opt, v)
	}
	return v
}

func (v *ctVal) clone() *ctVal {
	return v.cloneWithDepth(0)
}

func (v *ctVal) cloneWithDepth(depth int) *ctVal {
	if v == nil {
		return nil
	}
	if depth >= maxComptimeValueDepth {
		cp := *v
		return &cp
	}
	switch v.kind {
	case ctVoid, ctInt, ctFloat, ctBool, ctChar, ctString, ctRange:
		cp := *v
		return &cp
	case ctFn, ctPointer:
		cp := *v
		return &cp
	case ctOpt:
		if !v.hasOpt {
			return &ctVal{kind: ctOpt, ty: v.ty, hasOpt: false}
		}
		return &ctVal{kind: ctOpt, ty: v.ty, hasOpt: true, optElem: v.optElem.cloneWithDepth(depth + 1)}
	case ctEnum:
		return &ctVal{kind: ctEnum, ty: v.ty, en: v.en, variantIdx: v.variantIdx, payload: v.payload.cloneWithDepth(depth + 1)}
	case ctList:
		elems := make([]*ctVal, len(v.list.elems))
		for i, el := range v.list.elems {
			elems[i] = el.cloneWithDepth(depth + 1)
		}
		return &ctVal{kind: ctList, ty: v.ty, list: &ctListBox{elemTy: v.list.elemTy, elems: elems}}
	case ctArray:
		elems := make([]*ctVal, len(v.arr.elems))
		for i, el := range v.arr.elems {
			elems[i] = el.cloneWithDepth(depth + 1)
		}
		return &ctVal{kind: ctArray, ty: v.ty, arr: &ctArrayBox{elemTy: v.arr.elemTy, elems: elems}}
	case ctTuple:
		elems := make([]*ctVal, len(v.tup.elems))
		for i, el := range v.tup.elems {
			elems[i] = el.cloneWithDepth(depth + 1)
		}
		return &ctVal{kind: ctTuple, ty: v.ty, tup: &ctTupleBox{elems: elems}}
	case ctStruct:
		fields := make(map[string]*ctVal, len(v.st.fields))
		for k, fv := range v.st.fields {
			fields[k] = fv.cloneWithDepth(depth + 1)
		}
		order := append([]string(nil), v.st.fieldOrder...)
		return &ctVal{kind: ctStruct, ty: v.ty, st: &ctStructBox{ty: v.st.ty, fields: fields, fieldOrder: order, refl: v.st.refl}}
	case ctSet:
		elems := make([]*ctVal, len(v.set.elems))
		for i, el := range v.set.elems {
			elems[i] = el.cloneWithDepth(depth + 1)
		}
		return &ctVal{kind: ctSet, ty: v.ty, set: &ctSetBox{elemTy: v.set.elemTy, elems: elems}}
	case ctMap:
		entries := make([]ctMapEntry, len(v.m.entries))
		for i, e := range v.m.entries {
			entries[i] = ctMapEntry{key: e.key.cloneWithDepth(depth + 1), val: e.val.cloneWithDepth(depth + 1)}
		}
		return &ctVal{kind: ctMap, ty: v.ty, m: &ctMapBox{keyTy: v.m.keyTy, valTy: v.m.valTy, entries: entries}}
	}
	cp := *v
	return &cp
}

func freezeCtVal(v *ctVal) {
	freezeCtValDepth(v, 0)
}

func freezeCtValDepth(v *ctVal, depth int) {
	if v == nil || depth >= maxComptimeValueDepth {
		return
	}
	switch v.kind {
	case ctString:
		v.sCap = 0
		if v.sAlloc != nil {
			v.sAlloc.readOnly = true
		}
	case ctOpt:
		if v.hasOpt {
			freezeCtValDepth(v.optElem, depth+1)
		}
	case ctEnum:
		freezeCtValDepth(v.payload, depth+1)
	case ctTuple:
		if v.tup != nil {
			for _, el := range v.tup.elems {
				freezeCtValDepth(el, depth+1)
			}
		}
	case ctArray:
		if v.arr != nil {
			for _, el := range v.arr.elems {
				freezeCtValDepth(el, depth+1)
			}
		}
	case ctList:
		if v.list != nil {
			for _, el := range v.list.elems {
				freezeCtValDepth(el, depth+1)
			}
		}
	case ctSet:
		if v.set != nil {
			for _, el := range v.set.elems {
				freezeCtValDepth(el, depth+1)
			}
		}
	case ctMap:
		if v.m != nil {
			for _, ent := range v.m.entries {
				freezeCtValDepth(ent.key, depth+1)
				freezeCtValDepth(ent.val, depth+1)
			}
		}
	case ctStruct:
		if v.st != nil {
			if v.st.ty != nil && v.st.ty.Lang == "list" {
				v.st.fields["cap"] = newCtSignedInt(0, types.TypeInt)
				if dv := v.st.fields["data"]; dv != nil && dv.kind == ctPointer && dv.ptrVal != nil && dv.ptrVal.alloc != nil {
					al := dv.ptrVal.alloc
					al.readOnly = true
					for _, cell := range al.cells {
						freezeCtValDepth(cell, depth+1)
					}
				}
				return
			}
			for _, fv := range v.st.fields {
				freezeCtValDepth(fv, depth+1)
			}
		}
	}
}

func (v *ctVal) equals(a *Analyzer, other *ctVal) bool {
	return v.equalsDepth(a, other, 0)
}

func (v *ctVal) equalsDepth(a *Analyzer, other *ctVal, depth int) bool {
	if depth >= maxComptimeValueDepth {
		return v == other
	}
	if v == nil || other == nil {
		return v == other
	}
	if v.kind != other.kind {
		// Support `T == ?T` when `WrapOptional` wrapped one side before compare.
		if v.kind == ctOpt && v.hasOpt {
			return v.optElem.equalsDepth(a, other, depth+1)
		}
		if other.kind == ctOpt && other.hasOpt {
			return v.equalsDepth(a, other.optElem, depth+1)
		}
		return false
	}
	switch v.kind {
	case ctVoid:
		return true
	case ctInt:
		return v.u == other.u
	case ctFloat:
		return v.f == other.f
	case ctBool:
		return v.b == other.b
	case ctChar:
		return v.ch == other.ch
	case ctString:
		return v.s == other.s
	case ctOpt:
		if !v.hasOpt || !other.hasOpt {
			return !v.hasOpt && !other.hasOpt
		}
		return v.optElem.equalsDepth(a, other.optElem, depth+1)
	case ctTuple:
		if len(v.tup.elems) != len(other.tup.elems) {
			return false
		}
		for i := range v.tup.elems {
			if !v.tup.elems[i].equalsDepth(a, other.tup.elems[i], depth+1) {
				return false
			}
		}
		return true
	case ctArray:
		if len(v.arr.elems) != len(other.arr.elems) {
			return false
		}
		for i := range v.arr.elems {
			if !v.arr.elems[i].equalsDepth(a, other.arr.elems[i], depth+1) {
				return false
			}
		}
		return true
	case ctList:
		if len(v.list.elems) != len(other.list.elems) {
			return false
		}
		for i := range v.list.elems {
			if !v.list.elems[i].equalsDepth(a, other.list.elems[i], depth+1) {
				return false
			}
		}
		return true
	case ctEnum:
		if v.variantIdx != other.variantIdx {
			return false
		}
		if v.payload != nil || other.payload != nil {
			return v.payload.equalsDepth(a, other.payload, depth+1)
		}
		return true
	case ctStruct:
		for _, k := range v.st.fieldOrder {
			if !v.st.fields[k].equalsDepth(a, other.st.fields[k], depth+1) {
				return false
			}
		}
		return true
	case ctFn:
		return v.fn == other.fn && v.lambda == other.lambda
	case ctPointer:
		if v.ptrVal == nil || other.ptrVal == nil {
			return (v.ptrVal == nil || (v.ptrVal.alloc == nil && v.ptrVal.place == nil)) &&
				(other.ptrVal == nil || (other.ptrVal.alloc == nil && other.ptrVal.place == nil))
		}
		return v.ptrVal.alloc == other.ptrVal.alloc &&
			v.ptrVal.byteOff == other.ptrVal.byteOff &&
			v.ptrVal.place == other.ptrVal.place
	case ctSet:
		if len(v.set.elems) != len(other.set.elems) {
			return false
		}
		for _, el := range v.set.elems {
			found := false
			for _, oel := range other.set.elems {
				if el.equalsDepth(a, oel, depth+1) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	case ctMap:
		if len(v.m.entries) != len(other.m.entries) {
			return false
		}
		for _, e := range v.m.entries {
			found := false
			for _, oe := range other.m.entries {
				if e.key.equalsDepth(a, oe.key, depth+1) && e.val.equalsDepth(a, oe.val, depth+1) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	return false
}

func (v *ctVal) cmpVal(other *ctVal) int {
	if v == nil || other == nil {
		return 0
	}
	switch v.kind {
	case ctInt:
		if v.prim != nil && !v.prim.IsSignedInt() {
			if v.u < other.u {
				return -1
			}
			if v.u > other.u {
				return 1
			}
			return 0
		}
		l, r := v.asInt64(), other.asInt64()
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
		return 0
	case ctFloat:
		if v.f < other.f {
			return -1
		}
		if v.f > other.f {
			return 1
		}
		return 0
	case ctChar:
		if v.ch < other.ch {
			return -1
		}
		if v.ch > other.ch {
			return 1
		}
		return 0
	case ctBool:
		if v.b == other.b {
			return 0
		}
		if !v.b {
			return -1
		}
		return 1
	case ctString:
		return strings.Compare(v.s, other.s)
	case ctEnum:
		l, r := v.asInt64(), other.asInt64()
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
		return 0
	case ctTuple:
		for i := 0; i < len(v.tup.elems) && i < len(other.tup.elems); i++ {
			if c := v.tup.elems[i].cmpVal(other.tup.elems[i]); c != 0 {
				return c
			}
		}
		return 0
	}
	return 0
}

func splitMix64(x uint64) uint64 {
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (v *ctVal) hash() uint64 {
	return v.hashWithDepth(0)
}

func (v *ctVal) hashWithDepth(depth int) uint64 {
	if v == nil || depth >= maxComptimeValueDepth {
		return 0
	}
	switch v.kind {
	case ctInt:
		if v.prim != nil && v.prim.IsSignedInt() {
			return splitMix64(uint64(v.asInt64()))
		}
		return splitMix64(v.u)
	case ctFloat:
		f := v.f
		if f == 0.0 {
			f = 0.0 // normalize -0.0
		}
		return splitMix64(math.Float64bits(f))
	case ctBool:
		if v.b {
			return splitMix64(1)
		}
		return splitMix64(0)
	case ctChar:
		return splitMix64(uint64(v.ch))
	case ctString:
		var h uint64 = 14695981039346656037
		const prime uint64 = 1099511628211
		for i := 0; i < len(v.s); i++ {
			h = (h ^ uint64(v.s[i])) * prime
		}
		return h
	case ctEnum:
		h := splitMix64(uint64(v.variantIdx))
		if v.payload != nil {
			h ^= v.payload.hashWithDepth(depth+1) + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
		}
		return h
	case ctOpt:
		if !v.hasOpt {
			return 0
		}
		return v.optElem.hashWithDepth(depth+1) ^ 0x517cc1b727220a95
	case ctTuple:
		var h uint64
		for _, el := range v.tup.elems {
			h ^= el.hashWithDepth(depth+1) + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
		}
		return h
	case ctArray:
		var h uint64
		for _, el := range v.arr.elems {
			h ^= el.hashWithDepth(depth+1) + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
		}
		return h
	case ctStruct:
		var h uint64
		for _, k := range v.st.fieldOrder {
			h ^= v.st.fields[k].hashWithDepth(depth+1) + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
		}
		return h
	case ctPointer:
		return splitMix64(v.ptrAddr())
	}
	return 0
}

func (v *ctVal) toString() string {
	if v == nil {
		return ""
	}
	switch v.kind {
	case ctInt:
		if v.prim != nil && !v.prim.IsSignedInt() {
			return strconv.FormatUint(v.asUint64(), 10)
		}
		return strconv.FormatInt(v.asInt64(), 10)
	case ctFloat:
		return strconv.FormatFloat(v.f, 'g', -1, 64)
	case ctBool:
		if v.b {
			return "true"
		}
		return "false"
	case ctChar:
		return string(v.ch)
	case ctString:
		return v.s
	case ctOpt:
		if !v.hasOpt {
			return "none"
		}
		return v.optElem.toString()
	case ctEnum:
		if v.en != nil && v.variantIdx >= 0 && v.variantIdx < len(v.en.Variants) {
			vr := v.en.Variants[v.variantIdx]
			if v.payload != nil {
				return fmt.Sprintf("%s.%s(%s)", v.en.Name, vr.Name, v.payload.toString())
			}
			return v.en.Name + "." + vr.Name
		}
	case ctList:
		parts := make([]string, len(v.list.elems))
		for i, el := range v.list.elems {
			parts[i] = el.toString()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case ctArray:
		parts := make([]string, len(v.arr.elems))
		for i, el := range v.arr.elems {
			parts[i] = el.toString()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case ctTuple:
		parts := make([]string, len(v.tup.elems))
		for i, el := range v.tup.elems {
			parts[i] = el.toString()
		}
		return "+[" + strings.Join(parts, ", ") + "]"
	case ctStruct:
		if v.st.ty != nil && v.st.ty.Lang == "list" {
			if elems, ok := listStructElems(v); ok {
				parts := make([]string, len(elems))
				for i, el := range elems {
					parts[i] = el.toString()
				}
				return "[" + strings.Join(parts, ", ") + "]"
			}
		}
		parts := make([]string, 0, len(v.st.fieldOrder))
		for _, k := range v.st.fieldOrder {
			parts = append(parts, k+": "+v.st.fields[k].toString())
		}
		return v.st.ty.Name + "[" + strings.Join(parts, ", ") + "]"
	case ctPointer:
		addr := v.ptrAddr()
		if addr == 0 {
			return "null"
		}
		return fmt.Sprintf("0x%x", addr)
	case ctSet:
		parts := make([]string, len(v.set.elems))
		for i, el := range v.set.elems {
			parts[i] = el.toString()
		}
		return "#[" + strings.Join(parts, ", ") + "]"
	case ctMap:
		parts := make([]string, len(v.m.entries))
		for i, e := range v.m.entries {
			parts[i] = e.key.toString() + ": " + e.val.toString()
		}
		return "#[" + strings.Join(parts, ", ") + "]"
	}
	return "<value>"
}

// -----------------------------------------------------------------------------
// Simulated Heap, Pointers & Raw Memory Helpers (`std/core` comptime execution)
// -----------------------------------------------------------------------------

func (v *ctVal) ptrAddr() uint64 {
	if v == nil || v.kind != ctPointer || v.ptrVal == nil {
		return 0
	}
	p := v.ptrVal
	if p.alloc != nil {
		return p.alloc.baseAddr + uint64(p.byteOff)
	}
	if p.place != nil {
		return uint64(0x70000000) + uint64(p.byteOff)
	}
	return 0
}

func listStructBytes(v *ctVal) ([]byte, *ctHeapAlloc, bool) {
	if v == nil || v.kind != ctStruct || v.st == nil || v.st.ty == nil || v.st.ty.Lang != "list" {
		return nil, nil, false
	}
	if len(v.st.ty.TypeArgs) != 1 || !isByteElemType(v.st.ty.TypeArgs[0]) {
		return nil, nil, false
	}
	lenV := v.st.fields["len"]
	if lenV == nil {
		return nil, nil, false
	}
	n := lenV.asInt64()
	if n == 0 {
		return nil, nil, true
	}
	if n < 0 {
		return nil, nil, false
	}
	dataV := v.st.fields["data"]
	if dataV == nil || dataV.kind != ctPointer || dataV.ptrVal == nil || dataV.ptrVal.alloc == nil || dataV.ptrVal.alloc.freed {
		return nil, nil, false
	}
	al := dataV.ptrVal.alloc
	if !al.isBytes {
		return nil, nil, false
	}
	base := int(dataV.ptrVal.byteOff)
	if base < 0 || base+int(n) > len(al.bytes) {
		return nil, nil, false
	}
	return al.bytes[base : base+int(n)], al, true
}

func listStructElems(v *ctVal) ([]*ctVal, bool) {
	if v == nil || v.kind != ctStruct || v.st == nil || v.st.ty == nil || v.st.ty.Lang != "list" {
		return nil, false
	}
	if bs, _, ok := listStructBytes(v); ok {
		elems := make([]*ctVal, len(bs))
		for i, b := range bs {
			elems[i] = newCtInt(uint64(b), types.TypeU8)
		}
		return elems, true
	}
	lenV := v.st.fields["len"]
	if lenV == nil {
		return nil, false
	}
	n := lenV.asInt64()
	if n <= 0 {
		return nil, true
	}
	dataV := v.st.fields["data"]
	if dataV == nil || dataV.kind != ctPointer || dataV.ptrVal == nil || dataV.ptrVal.alloc == nil || dataV.ptrVal.alloc.freed {
		return nil, false
	}
	al := dataV.ptrVal.alloc
	if al.isBytes || al.elemSize <= 0 {
		return nil, false
	}
	base := int(dataV.ptrVal.byteOff / al.elemSize)
	if base < 0 || base+int(n) > len(al.cells) {
		return nil, false
	}
	return al.cells[base : base+int(n)], true
}

func setStructElems(v *ctVal) ([]*ctVal, bool) {
	if v == nil || v.kind != ctStruct || v.st == nil || v.st.ty == nil || v.st.ty.Lang != "set" {
		return nil, false
	}
	entLenV := v.st.fields["ent_len"]
	if entLenV == nil {
		return nil, false
	}
	n := entLenV.asInt64()
	if n <= 0 {
		return nil, true
	}
	entV := v.st.fields["entries"]
	if entV == nil || entV.kind != ctPointer || entV.ptrVal == nil || entV.ptrVal.alloc == nil || entV.ptrVal.alloc.freed {
		return nil, false
	}
	al := entV.ptrVal.alloc
	if al.isBytes || al.elemSize <= 0 {
		return nil, false
	}
	base := int(entV.ptrVal.byteOff / al.elemSize)
	if base < 0 || base+int(n) > len(al.cells) {
		return nil, false
	}
	var out []*ctVal
	for _, slot := range al.cells[base : base+int(n)] {
		if slot == nil || slot.kind != ctStruct || slot.st == nil {
			continue
		}
		if alive := slot.st.fields["alive"]; alive != nil && alive.b {
			if key := slot.st.fields["key"]; key != nil {
				out = append(out, key)
			}
		}
	}
	return out, true
}

func arrayStructElems(v *ctVal) ([]*ctVal, bool) {
	if v == nil || v.kind != ctStruct || v.st == nil || v.st.ty == nil {
		return nil, false
	}
	name := v.st.ty.Name
	if name != "Array" && !strings.HasPrefix(name, "Array[") {
		return nil, false
	}
	dataV := v.st.fields["data"]
	lenV := v.st.fields["len"]
	if dataV == nil || dataV.kind != ctArray || dataV.arr == nil || lenV == nil || lenV.kind != ctInt {
		return nil, false
	}
	n := int(lenV.asInt64())
	if n < 0 {
		n = 0
	}
	if n > len(dataV.arr.elems) {
		n = len(dataV.arr.elems)
	}
	return dataV.arr.elems[:n], true
}

func mapStructEntries(v *ctVal) ([]ctMapEntry, bool) {
	if v == nil || v.kind != ctStruct || v.st == nil || v.st.ty == nil || v.st.ty.Lang != "map" {
		return nil, false
	}
	entLenV := v.st.fields["ent_len"]
	if entLenV == nil {
		return nil, false
	}
	n := entLenV.asInt64()
	if n <= 0 {
		return nil, true
	}
	entV := v.st.fields["entries"]
	if entV == nil || entV.kind != ctPointer || entV.ptrVal == nil || entV.ptrVal.alloc == nil || entV.ptrVal.alloc.freed {
		return nil, false
	}
	al := entV.ptrVal.alloc
	if al.isBytes || al.elemSize <= 0 {
		return nil, false
	}
	base := int(entV.ptrVal.byteOff / al.elemSize)
	if base < 0 || base+int(n) > len(al.cells) {
		return nil, false
	}
	var out []ctMapEntry
	for _, slot := range al.cells[base : base+int(n)] {
		if slot == nil || slot.kind != ctStruct || slot.st == nil {
			continue
		}
		if alive := slot.st.fields["alive"]; alive != nil && alive.b {
			k := slot.st.fields["key"]
			val := slot.st.fields["val"]
			if k != nil && val != nil {
				out = append(out, ctMapEntry{key: k, val: val})
			}
		}
	}
	return out, true
}

func (s *ctState) allowStdUnsafe() bool {
	m := s.curMod()
	return m != nil && m.Pkg != nil && m.Pkg.IsStd
}

func (s *ctState) nextAllocIDAddr() (int64, uint64) {
	s.a.nextCtAllocID++
	id := s.a.nextCtAllocID
	return id, uint64(0x10000000) + uint64(id)*uint64(0x100000)
}

func (s *ctState) simElemSize(t types.Type) int64 {
	sz := s.a.typeSize(t)
	if sz <= 0 {
		return 1
	}
	return sz
}

func isByteElemType(t types.Type) bool {
	p := types.UnderlyingPrimitive(t)
	return p != nil && p.Kind == types.PrimU8
}

func (s *ctState) newBytesAlloc(bytes []byte, readOnly bool, span diag.Span) (*ctHeapAlloc, ctSignal) {
	charge := int64(len(bytes))
	if sig := s.allocMem(charge, span); sig.kind != sigNone {
		return nil, sig
	}
	id, addr := s.nextAllocIDAddr()
	return &ctHeapAlloc{
		id:           id,
		baseAddr:     addr,
		elemTy:       types.TypeU8,
		elemSize:     1,
		isBytes:      true,
		bytes:        bytes,
		readOnly:     readOnly,
		chargedBytes: charge,
	}, okSig()
}

func (s *ctState) newHeapAlloc(elemTy types.Type, count int64, zeroInit bool, span diag.Span) (*ctHeapAlloc, ctSignal) {
	if count < 0 {
		return nil, abortSig(span, "negative allocation count (%d) at compile time", count)
	}
	if isByteElemType(elemTy) {
		return s.newBytesAlloc(make([]byte, count), false, span)
	}
	elemSize := s.simElemSize(elemTy)
	charge := count * elemSize
	if sig := s.allocMem(charge, span); sig.kind != sigNone {
		return nil, sig
	}
	id, addr := s.nextAllocIDAddr()
	cells := make([]*ctVal, count)
	if zeroInit {
		for i := int64(0); i < count; i++ {
			cells[i] = s.zeroCtVal(elemTy)
		}
	}
	return &ctHeapAlloc{
		id:           id,
		baseAddr:     addr,
		elemTy:       elemTy,
		elemSize:     elemSize,
		isBytes:      false,
		cells:        cells,
		chargedBytes: charge,
	}, okSig()
}

func (s *ctState) zeroCtVal(t types.Type) *ctVal {
	if t == nil {
		return newCtVoid()
	}
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PrimVoid, types.PrimNever:
			return newCtVoid()
		case types.PrimBool:
			return newCtBool(false)
		case types.PrimChar:
			return newCtChar(0)
		case types.PrimF64, types.PrimF32:
			return newCtFloat(0, v)
		case types.PrimString:
			return newCtString("")
		default:
			return newCtInt(0, v)
		}
	case *types.Ptr:
		return &ctVal{kind: ctPointer, ty: t, ptrVal: &ctPtr{elemTy: v.Elem, elemSize: s.simElemSize(v.Elem)}}
	case *types.Ref:
		return &ctVal{kind: ctPointer, ty: t, ptrVal: &ctPtr{elemTy: v.Elem, elemSize: s.simElemSize(v.Elem)}}
	case *types.Distinct:
		zv := s.zeroCtVal(v.Base)
		zv.ty = t
		return zv
	case *types.Optional:
		return newCtNone(t)
	case *types.Enum:
		if !v.IsTagged {
			u := types.TypeInt
			if v.Underlying != nil {
				u = v.Underlying
			}
			zv := newCtInt(0, types.UnderlyingPrimitive(u))
			zv.ty = t
			return zv
		}
		return &ctVal{kind: ctEnum, ty: t, en: v, variantIdx: 0}
	case *types.Tuple:
		elems := make([]*ctVal, len(v.Elems))
		for i, el := range v.Elems {
			elems[i] = s.zeroCtVal(el)
		}
		return &ctVal{kind: ctTuple, ty: t, tup: &ctTupleBox{elems: elems}}
	case *types.StackArray:
		elems := make([]*ctVal, v.Size)
		for i := int64(0); i < v.Size; i++ {
			elems[i] = s.zeroCtVal(v.Elem)
		}
		return &ctVal{kind: ctArray, ty: t, arr: &ctArrayBox{elemTy: v.Elem, elems: elems}}
	case *types.Struct:
		_ = s.a.ensureStructFieldsForLayout(v, diag.Span{})
		fields := make(map[string]*ctVal, len(v.Fields))
		order := make([]string, len(v.Fields))
		for i, f := range v.Fields {
			fields[f.Name] = s.zeroCtVal(f.Type)
			order[i] = f.Name
		}
		return &ctVal{kind: ctStruct, ty: t, st: &ctStructBox{ty: v, fields: fields, fieldOrder: order}}
	}
	return newCtVoid()
}

func unwrapStructType(t types.Type) (*types.Struct, bool) {
	for {
		if dt, ok := t.(*types.Distinct); ok {
			t = dt.Base
			continue
		}
		break
	}
	st, ok := t.(*types.Struct)
	return st, ok && st != nil
}

func (s *ctState) isStringRawType(t types.Type) bool {
	if st, ok := unwrapStructType(t); ok {
		if st.Name == "_StringRaw" {
			mod := s.a.Prog.StructMods[st]
			return mod == s.a.coreMod || (mod != nil && mod.Pkg == s.a.corePkg)
		}
	}
	return false
}

func (s *ctState) materializeStringVal(v *ctVal, span diag.Span) (*ctVal, ctSignal) {
	if v == nil || v.kind != ctString {
		return v, okSig()
	}
	if v.sAlloc == nil {
		alloc, sig := s.newBytesAlloc(append([]byte(nil), v.s...), v.sCap == 0, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		cp := *v
		cp.sAlloc = alloc
		cp.sOff = 0
		return &cp, okSig()
	}
	return v, okSig()
}

func (s *ctState) stringToRawStruct(v *ctVal, rawTy types.Type, span diag.Span) (*ctVal, ctSignal) {
	mv, sig := s.materializeStringVal(v, span)
	if sig.kind != sigNone {
		return nil, sig
	}
	st, _ := unwrapStructType(rawTy)
	dataPtr := &ctVal{
		kind: ctPointer,
		ty:   &types.Ptr{Elem: types.TypeU8},
		ptrVal: &ctPtr{
			alloc:    mv.sAlloc,
			byteOff:  mv.sOff,
			elemTy:   types.TypeU8,
			elemSize: 1,
		},
	}
	fields := map[string]*ctVal{
		"data": dataPtr,
		"len":  newCtSignedInt(int64(len(mv.s)), types.TypeInt),
		"cap":  newCtSignedInt(mv.sCap, types.TypeInt),
	}
	return &ctVal{
		kind: ctStruct,
		ty:   rawTy,
		st:   &ctStructBox{ty: st, fields: fields, fieldOrder: []string{"data", "len", "cap"}},
	}, okSig()
}

func (s *ctState) rawStructToString(sv *ctVal, span diag.Span) (*ctVal, ctSignal) {
	if sv == nil || sv.kind != ctStruct || sv.st == nil {
		return nil, abortSig(span, "expected `_StringRaw` struct converting to `string`")
	}
	dataV := sv.st.fields["data"]
	lenV := sv.st.fields["len"]
	capV := sv.st.fields["cap"]
	var n, c int64
	if lenV != nil {
		n = lenV.asInt64()
	}
	if capV != nil {
		c = capV.asInt64()
	}
	var alloc *ctHeapAlloc
	var off int64
	if dataV != nil && dataV.kind == ctPointer && dataV.ptrVal != nil {
		alloc = dataV.ptrVal.alloc
		off = dataV.ptrVal.byteOff
	}
	if n < 0 {
		return nil, abortSig(span, "negative string length (%d) in `_StringRaw`", n)
	}
	var str string
	if n > 0 {
		if alloc == nil || alloc.freed || !alloc.isBytes {
			return nil, abortSig(span, "invalid string buffer pointer in `_StringRaw`")
		}
		if off < 0 || off+n > int64(len(alloc.bytes)) {
			return nil, abortSig(span, "string length (%d) exceeds buffer bounds", n)
		}
		str = string(alloc.bytes[off : off+n])
	}
	return &ctVal{
		kind:   ctString,
		ty:     types.TypeString,
		prim:   types.TypeString,
		s:      str,
		sAlloc: alloc,
		sOff:   off,
		sCap:   c,
	}, okSig()
}

func (s *ctState) castReadVal(src *ctVal, dstTy types.Type, span diag.Span) (*ctVal, ctSignal) {
	if src == nil || dstTy == nil {
		return src, okSig()
	}
	if src.kind == ctString && s.isStringRawType(dstTy) {
		return s.stringToRawStruct(src, dstTy, span)
	}
	if src.kind == ctStruct && s.isStringRawType(src.ty) && types.UnderlyingPrimitive(dstTy) == types.TypeString {
		return s.rawStructToString(src, span)
	}
	dp := types.UnderlyingPrimitive(dstTy)
	if dp != nil {
		if src.kind == ctInt {
			switch dp.Kind {
			case types.PrimF64:
				return newCtFloat(math.Float64frombits(src.u), types.TypeFloat), okSig()
			case types.PrimF32:
				return newCtFloat(float64(math.Float32frombits(uint32(src.u))), types.TypeF32), okSig()
			case types.PrimBool:
				return newCtBool(src.u != 0), okSig()
			case types.PrimChar:
				return newCtChar(rune(src.u)), okSig()
			default:
				if dp.IsInteger() {
					return newCtInt(src.u, dp), okSig()
				}
			}
		} else if src.kind == ctFloat {
			switch dp.Kind {
			case types.PrimU64, types.PrimI64:
				return newCtInt(math.Float64bits(src.f), dp), okSig()
			case types.PrimU32, types.PrimI32:
				return newCtInt(uint64(math.Float32bits(float32(src.f))), dp), okSig()
			}
		}
	}
	return src, okSig()
}

func (s *ctState) castWriteVal(nv *ctVal, cur *ctVal, castTy types.Type, span diag.Span) (*ctVal, ctSignal) {
	if nv == nil || castTy == nil {
		return nv, okSig()
	}
	if cur != nil && cur.kind == ctString && s.isStringRawType(castTy) {
		return s.rawStructToString(nv, span)
	}
	if cur != nil && cur.kind == ctInt {
		if nv.kind == ctFloat {
			if nv.prim == types.TypeF32 {
				return newCtInt(uint64(math.Float32bits(float32(nv.f))), cur.prim), okSig()
			}
			return newCtInt(math.Float64bits(nv.f), cur.prim), okSig()
		}
	}
	return nv, okSig()
}

func (s *ctState) ptrDerefPlace(pv *ctPtr, span diag.Span) (ctPlace, ctSignal) {
	if pv == nil || (pv.alloc == nil && pv.place == nil) {
		return ctPlace{}, abortSig(span, "null pointer dereference at compile time")
	}
	if pv.place != nil && pv.alloc == nil {
		if pv.byteOff != 0 {
			return ctPlace{}, abortSig(span, "out-of-bounds offset (%d) on stack pointer at compile time", pv.byteOff)
		}
		targetPlace := *pv.place
		castTy := pv.castTy
		return ctPlace{
			get: func() (*ctVal, ctSignal) {
				v, sig := targetPlace.get()
				if sig.kind != sigNone {
					return nil, sig
				}
				if castTy != nil {
					return s.castReadVal(v, castTy, span)
				}
				return v, okSig()
			},
			set: func(nv *ctVal) ctSignal {
				if castTy != nil {
					cur, sig := targetPlace.get()
					if sig.kind != sigNone {
						return sig
					}
					conv, sig := s.castWriteVal(nv, cur, castTy, span)
					if sig.kind != sigNone {
						return sig
					}
					return targetPlace.set(conv)
				}
				return targetPlace.set(nv)
			},
		}, okSig()
	}
	// If place points to a ctString AND castTy is _StringRaw, prefer the string place so `_StringRaw` mutations write back to the string!
	if pv.place != nil && pv.castTy != nil && s.isStringRawType(pv.castTy) && pv.byteOff == 0 {
		targetPlace := *pv.place
		castTy := pv.castTy
		return ctPlace{
			get: func() (*ctVal, ctSignal) {
				v, sig := targetPlace.get()
				if sig.kind != sigNone {
					return nil, sig
				}
				return s.castReadVal(v, castTy, span)
			},
			set: func(nv *ctVal) ctSignal {
				cur, sig := targetPlace.get()
				if sig.kind != sigNone {
					return sig
				}
				conv, sig := s.castWriteVal(nv, cur, castTy, span)
				if sig.kind != sigNone {
					return sig
				}
				return targetPlace.set(conv)
			},
		}, okSig()
	}
	al := pv.alloc
	off := pv.byteOff
	elemTy := pv.elemTy
	elemSize := pv.elemSize
	castTy := pv.castTy
	if al.freed {
		return ctPlace{}, abortSig(span, "use-after-free: dereferencing freed compile-time heap pointer")
	}
	if al.isBytes {
		if off < 0 || off+elemSize > int64(len(al.bytes)) {
			return ctPlace{}, abortSig(span, "compile-time pointer byte offset %d out of bounds (len %d)", off, len(al.bytes))
		}
		p := types.UnderlyingPrimitive(elemTy)
		if elemSize == 1 || (p != nil && (p.Kind == types.PrimU8 || p.Kind == types.PrimI8 || p.Kind == types.PrimBool)) {
			prim := p
			if prim == nil {
				prim = types.TypeU8
			}
			return ctPlace{
				get: func() (*ctVal, ctSignal) {
					if al.freed {
						return nil, abortSig(span, "use-after-free reading compile-time byte buffer")
					}
					b := al.bytes[off]
					if prim.Kind == types.PrimBool {
						return newCtBool(b != 0), okSig()
					}
					return newCtInt(uint64(b), prim), okSig()
				},
				set: func(nv *ctVal) ctSignal {
					if al.freed {
						return abortSig(span, "use-after-free writing compile-time byte buffer")
					}
					if al.readOnly {
						return abortSig(span, "cannot mutate read-only compile-time buffer")
					}
					var b byte
					if nv.kind == ctBool {
						if nv.b {
							b = 1
						}
					} else {
						b = byte(nv.asUint64())
					}
					al.bytes[off] = b
					return okSig()
				},
			}, okSig()
		}
		return ctPlace{}, abortSig(span, "unsupported multi-byte pointer dereference `%v` on byte buffer at compile time", elemTy)
	}
	if al.elemSize <= 0 || off%al.elemSize != 0 {
		return ctPlace{}, abortSig(span, "misaligned compile-time heap pointer access (offset %d, elemSize %d)", off, al.elemSize)
	}
	idx := off / al.elemSize
	if idx < 0 || idx >= int64(len(al.cells)) {
		return ctPlace{}, abortSig(span, "compile-time heap pointer index %d out of bounds (len %d)", idx, len(al.cells))
	}
	return ctPlace{
		get: func() (*ctVal, ctSignal) {
			if al.freed {
				return nil, abortSig(span, "use-after-free reading compile-time heap memory")
			}
			cell := al.cells[idx]
			if cell == nil {
				return nil, abortSig(span, "read of uninitialized compile-time heap element [%d]", idx)
			}
			if castTy != nil {
				return s.castReadVal(cell, castTy, span)
			}
			return cell, okSig()
		},
		set: func(nv *ctVal) ctSignal {
			if al.freed {
				return abortSig(span, "use-after-free writing compile-time heap memory")
			}
			if al.readOnly {
				return abortSig(span, "cannot mutate read-only compile-time heap memory")
			}
			al.cells[idx] = nv
			return okSig()
		},
	}, okSig()
}

func (s *ctState) makeListStruct(listTy types.Type, elems []*ctVal, span diag.Span) (*ctVal, ctSignal) {
	if vec, ok := listTy.(*types.Vector); ok && s.a.Prog.GenStructList != nil {
		listTy = s.a.instantiateStruct(s.a.Prog.GenStructList, []types.Type{vec.Elem}, span)
	}
	if s.a.Prog.GenStructList == nil || listTy == nil {
		return &ctVal{kind: ctList, ty: listTy, list: &ctListBox{elems: elems}}, okSig()
	}
	st, ok := listTy.(*types.Struct)
	if !ok || !s.a.isStdStructTemplate(st, s.a.Prog.GenStructList) || len(st.TypeArgs) != 1 {
		return &ctVal{kind: ctList, ty: listTy, list: &ctListBox{elems: elems}}, okSig()
	}
	elemTy := st.TypeArgs[0]
	n := int64(len(elems))
	var ptr *ctPtr
	if n == 0 {
		ptr = &ctPtr{elemTy: elemTy, elemSize: s.simElemSize(elemTy)}
	} else {
		al, sig := s.newHeapAlloc(elemTy, n, false, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		if al.isBytes {
			for i, el := range elems {
				al.bytes[i] = byte(el.asUint64())
			}
		} else {
			for i, el := range elems {
				al.cells[i] = el
			}
		}
		ptr = &ctPtr{alloc: al, elemTy: elemTy, elemSize: al.elemSize}
	}
	fields := map[string]*ctVal{
		"data": {kind: ctPointer, ty: &types.Ptr{Elem: elemTy}, ptrVal: ptr},
		"len":  newCtSignedInt(n, types.TypeInt),
		"cap":  newCtSignedInt(n, types.TypeInt),
	}
	return &ctVal{
		kind: ctStruct,
		ty:   st,
		st:   &ctStructBox{ty: st, fields: fields, fieldOrder: []string{"data", "len", "cap"}},
	}, okSig()
}

func (s *ctState) makeByteListStruct(data []byte, span diag.Span) (*ctVal, ctSignal) {
	if s.a.Prog.GenStructList == nil {
		elems := make([]*ctVal, len(data))
		for i, b := range data {
			elems[i] = newCtInt(uint64(b), types.TypeU8)
		}
		return &ctVal{kind: ctList, ty: &types.Vector{Elem: types.TypeU8}, list: &ctListBox{elemTy: types.TypeU8, elems: elems}}, okSig()
	}
	st := s.a.instantiateStruct(s.a.Prog.GenStructList, []types.Type{types.TypeU8}, span)
	if st == nil {
		return nil, abortSig(span, "missing `List[u8]` runtime definition")
	}
	n := int64(len(data))
	var ptr *ctPtr
	if n == 0 {
		ptr = &ctPtr{elemTy: types.TypeU8, elemSize: 1}
	} else {
		buf := make([]byte, len(data))
		copy(buf, data)
		al, sig := s.newBytesAlloc(buf, false, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		ptr = &ctPtr{alloc: al, elemTy: types.TypeU8, elemSize: 1}
	}
	fields := map[string]*ctVal{
		"data": {kind: ctPointer, ty: &types.Ptr{Elem: types.TypeU8}, ptrVal: ptr},
		"len":  newCtSignedInt(n, types.TypeInt),
		"cap":  newCtSignedInt(n, types.TypeInt),
	}
	return &ctVal{
		kind: ctStruct,
		ty:   st,
		st:   &ctStructBox{ty: st, fields: fields, fieldOrder: []string{"data", "len", "cap"}},
	}, okSig()
}

func (s *ctState) makeSetStruct(setTy types.Type, elems []*ctVal, span diag.Span) (*ctVal, ctSignal) {
	if setT, ok := setTy.(*types.Set); ok && s.a.Prog.GenStructSet != nil {
		setTy = s.a.instantiateStruct(s.a.Prog.GenStructSet, []types.Type{setT.Elem}, span)
	}
	st, ok := setTy.(*types.Struct)
	if !ok || s.a.Prog.GenStructSet == nil || !s.a.isStdStructTemplate(st, s.a.Prog.GenStructSet) {
		return &ctVal{kind: ctSet, ty: setTy, set: &ctSetBox{elems: elems}}, okSig()
	}
	newFn := s.a.LookupMethod(st, "new", nil, span)
	if newFn == nil {
		return &ctVal{kind: ctSet, ty: setTy, set: &ctSetBox{elems: elems}}, okSig()
	}
	sv, sig := s.callFuncInstance(newFn, nil, span)
	if sig.kind != sigNone {
		return nil, sig
	}
	if len(elems) > 0 {
		insFn := s.a.LookupMethod(st, "insert", nil, span)
		if insFn == nil {
			return nil, abortSig(span, "missing `Set.insert` method at compile time")
		}
		cur := sv
		pl := ctPlace{
			get: func() (*ctVal, ctSignal) { return cur, okSig() },
			set: func(nv *ctVal) ctSignal { cur = nv; return okSig() },
		}
		for _, el := range elems {
			_, sig := s.callFuncInstance(insFn, []ctArg{{place: &pl}, {val: el}}, span)
			if sig.kind != sigNone {
				return nil, sig
			}
		}
		sv = cur
	}
	return sv, okSig()
}

func (s *ctState) makeMapStruct(mapTy types.Type, entries []ctMapEntry, span diag.Span) (*ctVal, ctSignal) {
	if mapT, ok := mapTy.(*types.Map); ok && s.a.Prog.GenStructMap != nil {
		mapTy = s.a.instantiateStruct(s.a.Prog.GenStructMap, []types.Type{mapT.Key, mapT.Value}, span)
	}
	st, ok := mapTy.(*types.Struct)
	if !ok || s.a.Prog.GenStructMap == nil || !s.a.isStdStructTemplate(st, s.a.Prog.GenStructMap) {
		return &ctVal{kind: ctMap, ty: mapTy, m: &ctMapBox{entries: entries}}, okSig()
	}
	newFn := s.a.LookupMethod(st, "new", nil, span)
	if newFn == nil {
		return &ctVal{kind: ctMap, ty: mapTy, m: &ctMapBox{entries: entries}}, okSig()
	}
	mv, sig := s.callFuncInstance(newFn, nil, span)
	if sig.kind != sigNone {
		return nil, sig
	}
	if len(entries) > 0 {
		insFn := s.a.LookupMethod(st, "insert", nil, span)
		if insFn == nil {
			return nil, abortSig(span, "missing `Map.insert` method at compile time")
		}
		cur := mv
		pl := ctPlace{
			get: func() (*ctVal, ctSignal) { return cur, okSig() },
			set: func(nv *ctVal) ctSignal { cur = nv; return okSig() },
		}
		for _, ent := range entries {
			_, sig := s.callFuncInstance(insFn, []ctArg{{place: &pl}, {val: ent.key}, {val: ent.val}}, span)
			if sig.kind != sigNone {
				return nil, sig
			}
		}
		mv = cur
	}
	return mv, okSig()
}

func (s *ctState) dropValue(ty types.Type, val *ctVal, span diag.Span) ctSignal {
	if ty == nil || val == nil || ty.IsPlain() {
		return okSig()
	}
	if dt, ok := ty.(*types.Distinct); ok {
		if dropFn := s.a.LookupMethod(dt, "drop", nil, span); dropFn != nil {
			if !s.isCallable(dropFn) {
				return abortSig(span, "custom `drop` method %q is not marked `const`", dropFn.Name)
			}
			cur := val
			pl := ctPlace{
				get: func() (*ctVal, ctSignal) { return cur, okSig() },
				set: func(nv *ctVal) ctSignal { cur = nv; return okSig() },
			}
			_, sig := s.callFuncInstance(dropFn, []ctArg{{place: &pl}}, span)
			return sig
		}
		return s.dropValue(dt.Base, val, span)
	}
	switch t := ty.(type) {
	case *types.Primitive:
		if t.Kind == types.PrimString && val.kind == ctString && val.sAlloc != nil && !val.sAlloc.freed && !val.sAlloc.readOnly && val.sCap > 0 && val.sOff == 0 {
			s.freeMem(val.sAlloc.chargedBytes)
			val.sAlloc.chargedBytes = 0
			val.sAlloc.freed = true
		}
		return okSig()
	case *types.Struct:
		if dropFn := s.a.LookupMethod(t, "drop", nil, span); dropFn != nil {
			if !s.isCallable(dropFn) {
				return abortSig(span, "custom `drop` method %q is not marked `const`", dropFn.Name)
			}
			cur := val
			pl := ctPlace{
				get: func() (*ctVal, ctSignal) { return cur, okSig() },
				set: func(nv *ctVal) ctSignal { cur = nv; return okSig() },
			}
			_, sig := s.callFuncInstance(dropFn, []ctArg{{place: &pl}}, span)
			if sig.kind != sigNone {
				return sig
			}
			val = cur
		}
		if val != nil && val.kind == ctStruct && val.st != nil {
			_ = s.a.ensureStructFieldsForLayout(t, span)
			for i := len(t.Fields) - 1; i >= 0; i-- {
				f := t.Fields[i]
				if !f.Type.IsPlain() {
					if fv := val.st.fields[f.Name]; fv != nil {
						if sig := s.dropValue(f.Type, fv, span); sig.kind != sigNone {
							return sig
						}
					}
				}
			}
		}
	case *types.Optional:
		if val.kind == ctOpt && val.hasOpt {
			return s.dropValue(t.Elem, val.optElem, span)
		}
	case *types.Tuple:
		if val.kind == ctTuple && val.tup != nil {
			for i := len(t.Elems) - 1; i >= 0; i-- {
				if i < len(val.tup.elems) && !t.Elems[i].IsPlain() {
					if sig := s.dropValue(t.Elems[i], val.tup.elems[i], span); sig.kind != sigNone {
						return sig
					}
				}
			}
		}
	case *types.StackArray:
		if val.kind == ctArray && val.arr != nil && !t.Elem.IsPlain() {
			for i := len(val.arr.elems) - 1; i >= 0; i-- {
				if sig := s.dropValue(t.Elem, val.arr.elems[i], span); sig.kind != sigNone {
					return sig
				}
			}
		}
	case *types.Enum:
		if dropFn := s.a.LookupMethod(t, "drop", nil, span); dropFn != nil {
			if !s.isCallable(dropFn) {
				return abortSig(span, "custom `drop` method %q is not marked `const`", dropFn.Name)
			}
			cur := val
			pl := ctPlace{
				get: func() (*ctVal, ctSignal) { return cur, okSig() },
				set: func(nv *ctVal) ctSignal { cur = nv; return okSig() },
			}
			_, sig := s.callFuncInstance(dropFn, []ctArg{{place: &pl}}, span)
			if sig.kind != sigNone {
				return sig
			}
			val = cur
		}
		if t.IsTagged && val != nil && val.kind == ctEnum && val.payload != nil && val.variantIdx >= 0 && val.variantIdx < len(t.Variants) {
			vr := t.Variants[val.variantIdx]
			if vr.Kind == types.VariantTuple && vr.Payload != nil && !vr.Payload.IsPlain() {
				return s.dropValue(vr.Payload, val.payload, span)
			}
		}
	}
	return okSig()
}

func collectCtRefsDepth(v *ctVal, vals map[*ctVal]bool, allocs map[*ctHeapAlloc]bool, depth int) {
	if v == nil || depth >= maxComptimeValueDepth {
		return
	}
	vals[v] = true
	switch v.kind {
	case ctString:
		if v.sAlloc != nil && !v.sAlloc.freed {
			allocs[v.sAlloc] = true
		}
	case ctPointer:
		if v.ptrVal != nil && v.ptrVal.alloc != nil && !v.ptrVal.alloc.freed {
			allocs[v.ptrVal.alloc] = true
		}
	case ctOpt:
		if v.hasOpt {
			collectCtRefsDepth(v.optElem, vals, allocs, depth+1)
		}
	case ctEnum:
		collectCtRefsDepth(v.payload, vals, allocs, depth+1)
	case ctTuple:
		if v.tup != nil {
			for _, el := range v.tup.elems {
				collectCtRefsDepth(el, vals, allocs, depth+1)
			}
		}
	case ctArray:
		if v.arr != nil {
			for _, el := range v.arr.elems {
				collectCtRefsDepth(el, vals, allocs, depth+1)
			}
		}
	case ctList:
		if v.list != nil {
			for _, el := range v.list.elems {
				collectCtRefsDepth(el, vals, allocs, depth+1)
			}
		}
	case ctSet:
		if v.set != nil {
			for _, el := range v.set.elems {
				collectCtRefsDepth(el, vals, allocs, depth+1)
			}
		}
	case ctMap:
		if v.m != nil {
			for _, ent := range v.m.entries {
				collectCtRefsDepth(ent.key, vals, allocs, depth+1)
				collectCtRefsDepth(ent.val, vals, allocs, depth+1)
			}
		}
	case ctStruct:
		if v.st != nil {
			for _, fv := range v.st.fields {
				collectCtRefsDepth(fv, vals, allocs, depth+1)
			}
		}
	}
}

func ctValSharesRefsDepth(v *ctVal, liveVals map[*ctVal]bool, liveAllocs map[*ctHeapAlloc]bool, depth int) bool {
	if v == nil || depth >= maxComptimeValueDepth {
		return false
	}
	if liveVals[v] {
		return true
	}
	switch v.kind {
	case ctString:
		return v.sAlloc != nil && !v.sAlloc.freed && liveAllocs[v.sAlloc]
	case ctPointer:
		return v.ptrVal != nil && v.ptrVal.alloc != nil && !v.ptrVal.alloc.freed && liveAllocs[v.ptrVal.alloc]
	case ctOpt:
		return v.hasOpt && ctValSharesRefsDepth(v.optElem, liveVals, liveAllocs, depth+1)
	case ctEnum:
		return ctValSharesRefsDepth(v.payload, liveVals, liveAllocs, depth+1)
	case ctTuple:
		if v.tup != nil {
			for _, el := range v.tup.elems {
				if ctValSharesRefsDepth(el, liveVals, liveAllocs, depth+1) {
					return true
				}
			}
		}
	case ctArray:
		if v.arr != nil {
			for _, el := range v.arr.elems {
				if ctValSharesRefsDepth(el, liveVals, liveAllocs, depth+1) {
					return true
				}
			}
		}
	case ctList:
		if v.list != nil {
			for _, el := range v.list.elems {
				if ctValSharesRefsDepth(el, liveVals, liveAllocs, depth+1) {
					return true
				}
			}
		}
	case ctSet:
		if v.set != nil {
			for _, el := range v.set.elems {
				if ctValSharesRefsDepth(el, liveVals, liveAllocs, depth+1) {
					return true
				}
			}
		}
	case ctMap:
		if v.m != nil {
			for _, ent := range v.m.entries {
				if ctValSharesRefsDepth(ent.key, liveVals, liveAllocs, depth+1) || ctValSharesRefsDepth(ent.val, liveVals, liveAllocs, depth+1) {
					return true
				}
			}
		}
	case ctStruct:
		if v.st != nil {
			for _, fv := range v.st.fields {
				if ctValSharesRefsDepth(fv, liveVals, liveAllocs, depth+1) {
					return true
				}
			}
		}
	}
	return false
}

func (s *ctState) sharesLiveSlotAlloc(val *ctVal) bool {
	if val == nil {
		return false
	}
	liveVals := make(map[*ctVal]bool)
	liveAllocs := make(map[*ctHeapAlloc]bool)
	for i := range s.frames {
		for _, sl := range s.frames[i].locals {
			if sl != nil && !sl.moved && sl.refPlace == nil && sl.val != nil {
				collectCtRefsDepth(sl.val, liveVals, liveAllocs, 0)
			}
		}
		for _, sl := range s.frames[i].captures {
			if sl != nil && !sl.moved && sl.refPlace == nil && sl.val != nil {
				collectCtRefsDepth(sl.val, liveVals, liveAllocs, 0)
			}
		}
	}
	return ctValSharesRefsDepth(val, liveVals, liveAllocs, 0)
}

// -----------------------------------------------------------------------------
// Evaluation Entrypoints
// -----------------------------------------------------------------------------

func (a *Analyzer) evalComptimeTopVar(tv *TopVarInit) (*ctVal, bool) {
	if tv == nil || tv.Sym == nil || tv.Decl == nil {
		return nil, false
	}
	if tv.Sym.ConstVal != nil {
		if cv, ok := tv.Sym.ConstVal.(*ctVal); ok {
			return cv, true
		}
	}
	if a.comptimeEvalActive == nil {
		a.comptimeEvalActive = make(map[*Symbol]bool)
	}
	if a.comptimeEvalActive[tv.Sym] {
		startIdx := 0
		for i, sym := range a.comptimeEvalStack {
			if sym == tv.Sym {
				startIdx = i
				break
			}
		}
		cycle := a.comptimeEvalStack[startIdx:]
		names := make([]string, 0, len(cycle)+1)
		var rel []diag.RelatedInfo
		for _, sym := range cycle {
			names = append(names, sym.Name)
			if item, ok := a.topVarMap[sym]; ok && item.Decl != nil {
				rel = append(rel, diag.RelatedInfo{
					Span:    item.Decl.Span,
					Message: fmt.Sprintf("while evaluating `const` %q", sym.Name),
				})
			}
		}
		names = append(names, tv.Sym.Name)
		a.Rep.Report(diag.Diagnostic{
			Severity: diag.Error,
			Span:     tv.Decl.Span,
			Message:  fmt.Sprintf("cyclic dependency detected while evaluating `const` %q (%s)", tv.Sym.Name, strings.Join(names, " -> ")),
			Related:  rel,
		})
		return nil, false
	}
	a.comptimeEvalActive[tv.Sym] = true
	a.comptimeEvalStack = append(a.comptimeEvalStack, tv.Sym)
	defer func() {
		delete(a.comptimeEvalActive, tv.Sym)
		if len(a.comptimeEvalStack) > 0 {
			a.comptimeEvalStack = a.comptimeEvalStack[:len(a.comptimeEvalStack)-1]
		}
	}()

	limit := tv.Sym.ComptimeLimit
	st := newCtState(a, tv.Mod, limit)
	if tv.Sym.ComptimeMemLimit > 0 {
		st.memLimit = tv.Sym.ComptimeMemLimit
	}
	st.frames = []ctFrame{{
		rootName: "const " + tv.Sym.Name,
		mod:      tv.Mod,
		bm:       a.Prog.TopBodyMaps,
		callSpan: tv.Decl.Span,
		locals:   make(map[*Symbol]*ctSlot),
	}}
	val, sig := st.evalExpr(tv.Decl.Value)
	if sig.kind == sigAbort {
		st.reportAbort(sig, tv.Decl.Value.GetSpan())
		return nil, false
	}
	if sig.kind == sigUnwind {
		a.Rep.Errorf(tv.Decl.Span, "unexpected control-flow unwind inside top-level `const` initializer")
		return nil, false
	}
	if val != nil && tv.Sym.Type != nil {
		val = val.wrapResult(tv.Sym.Type)
	}
	if !tv.Sym.ComptimeOnly && tv.Sym.Type != nil && tv.Sym.Type != types.TypeInvalid {
		if msg := a.validateCrossesToRuntime(val, tv.Sym.Type); msg != "" {
			a.Rep.Errorf(tv.Decl.Value.GetSpan(), "%s", msg)
			return nil, false
		}
	}
	freezeCtVal(val)
	tv.Sym.ConstVal = val
	if val != nil && val.kind == ctInt {
		tv.Sym.IsConstInt = true
		tv.Sym.ConstIntVal = val.asInt64()
	}
	return val, true
}

// validateCrossesToRuntime enforces that compile-time values crossing into runtime code or
// non-@comptime_only `const` globals are Plain, string, List[T], or composites built from them
// (boxed ?T is allowed only when `none`; Map, Set, Arena, ref[T], ptr[T], cl, and custom Drop
// structs are rejected).
func (a *Analyzer) validateCrossesToRuntime(val *ctVal, ty types.Type) string {
	return a.validateCrossVal(val, ty, make(map[types.Type]bool))
}

func (a *Analyzer) validateCrossTypeOnly(ty types.Type, seen map[types.Type]bool) string {
	if ty == nil || ty == types.TypeInvalid {
		return ""
	}
	if ctTy := types.FindComptimeOnlyType(ty); ctTy != nil {
		return fmt.Sprintf("`@comptime_only` type %s cannot cross from compile time to runtime", ctTy.String())
	}
	if seen[ty] {
		return ""
	}
	switch t := ty.(type) {
	case *types.Primitive, *types.Interface:
		return ""
	case *types.Distinct:
		if a.Prog != nil && a.Prog.LookupConcreteMethod(t, "drop") != nil {
			return fmt.Sprintf("value of type %s with custom `Drop` cannot cross from compile time to runtime", ty.String())
		}
		return a.validateCrossTypeOnly(t.Base, seen)
	case *types.Ptr:
		return fmt.Sprintf("pointer type %s cannot cross from compile time to runtime", ty.String())
	case *types.Ref:
		return fmt.Sprintf("arena reference type %s cannot cross from compile time to runtime", ty.String())
	case *types.Map:
		return fmt.Sprintf("map type %s cannot cross from compile time to runtime (mark `@comptime_only` or convert to a list/plain table)", ty.String())
	case *types.Set:
		return fmt.Sprintf("set type %s cannot cross from compile time to runtime (mark `@comptime_only` or convert to a list/plain table)", ty.String())
	case *types.Func:
		if t.Closure {
			return fmt.Sprintf("closure type %s cannot cross from compile time to runtime", ty.String())
		}
		return ""
	case *types.Optional:
		if !t.IsBoxed() {
			return a.validateCrossTypeOnly(t.Elem, seen)
		}
		return ""
	case *types.Vector:
		seen[ty] = true
		msg := a.validateCrossTypeOnly(t.Elem, seen)
		delete(seen, ty)
		return msg
	case *types.StackArray:
		return a.validateCrossTypeOnly(t.Elem, seen)
	case *types.Tuple:
		for _, el := range t.Elems {
			if msg := a.validateCrossTypeOnly(el, seen); msg != "" {
				return msg
			}
		}
		return ""
	case *types.Enum:
		if a.Prog != nil && a.Prog.LookupConcreteMethod(t, "drop") != nil {
			return fmt.Sprintf("type %s with custom `Drop` cannot cross from compile time to runtime", ty.String())
		}
		return ""
	case *types.Struct:
		if types.IsArena(t) || t.Lang == "arena" {
			return "`Arena` cannot cross from compile time to runtime"
		}
		switch t.Lang {
		case "map":
			return fmt.Sprintf("map type %s cannot cross from compile time to runtime (mark `@comptime_only` or convert to a list/plain table)", ty.String())
		case "set":
			return fmt.Sprintf("set type %s cannot cross from compile time to runtime (mark `@comptime_only` or convert to a list/plain table)", ty.String())
		case "list":
			if len(t.TypeArgs) == 1 {
				seen[ty] = true
				msg := a.validateCrossTypeOnly(t.TypeArgs[0], seen)
				delete(seen, ty)
				return msg
			}
			return ""
		case "string", "range":
			return ""
		}
		if t.HasCustomDrop {
			return fmt.Sprintf("struct %s with custom `Drop` cannot cross from compile time to runtime", ty.String())
		}
		seen[ty] = true
		for _, f := range t.Fields {
			if msg := a.validateCrossTypeOnly(f.Type, seen); msg != "" {
				delete(seen, ty)
				return msg
			}
		}
		delete(seen, ty)
		return ""
	}
	return ""
}

func (a *Analyzer) validateCrossVal(val *ctVal, ty types.Type, seen map[types.Type]bool) string {
	if ty == nil || ty == types.TypeInvalid {
		return ""
	}
	if ctTy := types.FindComptimeOnlyType(ty); ctTy != nil {
		return fmt.Sprintf("`@comptime_only` type %s cannot cross from compile time to runtime", ctTy.String())
	}
	switch t := ty.(type) {
	case *types.Distinct:
		if a.Prog != nil && a.Prog.LookupConcreteMethod(t, "drop") != nil {
			return fmt.Sprintf("value of type %s with custom `Drop` cannot cross from compile time to runtime", ty.String())
		}
		return a.validateCrossVal(val, t.Base, seen)
	case *types.Ptr:
		return fmt.Sprintf("pointer type %s cannot cross from compile time to runtime", ty.String())
	case *types.Ref:
		return fmt.Sprintf("arena reference type %s cannot cross from compile time to runtime", ty.String())
	case *types.Map:
		return fmt.Sprintf("map type %s cannot cross from compile time to runtime (mark `@comptime_only` or convert to a list/plain table)", ty.String())
	case *types.Set:
		return fmt.Sprintf("set type %s cannot cross from compile time to runtime (mark `@comptime_only` or convert to a list/plain table)", ty.String())
	case *types.Func:
		if t.Closure || (val != nil && val.lambda != nil && val.lambda.Sig != nil && val.lambda.Sig.Closure) {
			return fmt.Sprintf("closure value of type %s cannot cross from compile time to runtime", ty.String())
		}
		if val != nil && val.fn != nil && val.fn.ComptimeOnly {
			return fmt.Sprintf("`@comptime_only` function %q cannot cross from compile time to runtime", val.fn.Name)
		}
		return ""
	case *types.Optional:
		if val == nil || !val.hasOpt {
			// `none` is allowed for both value optionals and boxed optionals (except non-boxed still safe).
			return ""
		}
		if t.IsBoxed() {
			return fmt.Sprintf("non-none boxed optional value of type %s cannot cross from compile time to runtime", ty.String())
		}
		return a.validateCrossVal(val.optElem, t.Elem, seen)
	case *types.Vector:
		if val != nil && val.kind == ctList && val.list != nil && len(val.list.elems) > 0 {
			for _, el := range val.list.elems {
				if msg := a.validateCrossVal(el, t.Elem, seen); msg != "" {
					return msg
				}
			}
			return ""
		}
		if val != nil && val.kind == ctStruct {
			if _, _, ok := listStructBytes(val); ok {
				return ""
			}
			if elems, ok := listStructElems(val); ok && len(elems) > 0 {
				for _, el := range elems {
					if msg := a.validateCrossVal(el, t.Elem, seen); msg != "" {
						return msg
					}
				}
				return ""
			}
		}
		return a.validateCrossTypeOnly(t.Elem, seen)
	case *types.StackArray:
		if val != nil && val.kind == ctArray && val.arr != nil && len(val.arr.elems) > 0 {
			for _, el := range val.arr.elems {
				if msg := a.validateCrossVal(el, t.Elem, seen); msg != "" {
					return msg
				}
			}
			return ""
		}
		return a.validateCrossTypeOnly(t.Elem, seen)
	case *types.Tuple:
		for i, elTy := range t.Elems {
			var elVal *ctVal
			if val != nil && val.kind == ctTuple && val.tup != nil && i < len(val.tup.elems) {
				elVal = val.tup.elems[i]
			}
			if msg := a.validateCrossVal(elVal, elTy, seen); msg != "" {
				return msg
			}
		}
		return ""
	case *types.Enum:
		if a.Prog != nil && a.Prog.LookupConcreteMethod(t, "drop") != nil {
			return fmt.Sprintf("type %s with custom `Drop` cannot cross from compile time to runtime", ty.String())
		}
		if t.IsTagged && val != nil && val.kind == ctEnum && val.variantIdx >= 0 && val.variantIdx < len(t.Variants) {
			vr := t.Variants[val.variantIdx]
			if vr.Kind == types.VariantTuple && vr.Payload != nil {
				return a.validateCrossVal(val.payload, vr.Payload, seen)
			}
		}
		return ""
	case *types.Struct:
		if types.IsArena(t) || t.Lang == "arena" {
			return "`Arena` cannot cross from compile time to runtime"
		}
		switch t.Lang {
		case "map":
			return fmt.Sprintf("map type %s cannot cross from compile time to runtime (mark `@comptime_only` or convert to a list/plain table)", ty.String())
		case "set":
			return fmt.Sprintf("set type %s cannot cross from compile time to runtime (mark `@comptime_only` or convert to a list/plain table)", ty.String())
		case "list":
			var elemTy types.Type
			if len(t.TypeArgs) == 1 {
				elemTy = t.TypeArgs[0]
			}
			if val != nil && val.kind == ctList && val.list != nil && len(val.list.elems) > 0 {
				for _, el := range val.list.elems {
					if msg := a.validateCrossVal(el, elemTy, seen); msg != "" {
						return msg
					}
				}
				return ""
			}
			if val != nil && val.kind == ctStruct {
				if _, _, ok := listStructBytes(val); ok {
					return ""
				}
				if elems, ok := listStructElems(val); ok && len(elems) > 0 {
					for _, el := range elems {
						if msg := a.validateCrossVal(el, elemTy, seen); msg != "" {
							return msg
						}
					}
					return ""
				}
			}
			return a.validateCrossTypeOnly(elemTy, seen)
		case "string", "range":
			return ""
		}
		if t.HasCustomDrop {
			return fmt.Sprintf("struct %s with custom `Drop` cannot cross from compile time to runtime", ty.String())
		}
		for _, f := range t.Fields {
			var fVal *ctVal
			if val != nil && val.kind == ctStruct && val.st != nil {
				fVal = val.st.fields[f.Name]
			}
			if msg := a.validateCrossVal(fVal, f.Type, seen); msg != "" {
				return msg
			}
		}
		return ""
	case *types.Primitive:
		return ""
	}
	return ""
}

const MaxComptimeUnrollIters = 1024

func (a *Analyzer) newCheckerCtState(fc *funcChecker, span diag.Span, stepLimit int) *ctState {
	if stepLimit <= 0 && fc.curFn != nil && fc.curFn.ComptimeLimit > 0 {
		stepLimit = fc.curFn.ComptimeLimit
	}
	st := newCtState(a, fc.mod, stepLimit)
	if fc.curFn != nil && fc.curFn.ComptimeMemLimit > 0 {
		st.memLimit = fc.curFn.ComptimeMemLimit
	}
	locals := make(map[*Symbol]*ctSlot)
	// Seed any already-evaluated local `const` / unrolled bindings in scope.
	if a.curBM != nil {
		for sym, cv := range a.curBM.ConstVarVals {
			locals[sym] = &ctSlot{val: cv, ty: sym.Type}
		}
	}
	rootName := "<comptime>"
	if fc.curFn != nil && fc.curFn.IsConst {
		rootName = fc.curFn.Name
	}
	st.frames = []ctFrame{{
		rootName: rootName,
		mod:      fc.mod,
		bm:       a.curBM,
		subst:    fc.subst,
		callSpan: span,
		locals:   locals,
	}}
	return st
}

func (a *Analyzer) evalComptimeExprInChecker(fc *funcChecker, expr ast.Expr, stepLimit int) (*ctVal, bool) {
	if expr == nil || fc == nil {
		return nil, false
	}
	st := a.newCheckerCtState(fc, expr.GetSpan(), stepLimit)
	val, sig := st.evalExpr(expr)
	if sig.kind == sigAbort {
		st.reportAbort(sig, expr.GetSpan())
		return nil, false
	}
	if sig.kind == sigUnwind {
		a.Rep.Errorf(expr.GetSpan(), "cannot unwind out of `comptime` expression")
		return nil, false
	}
	return val, true
}

func (a *Analyzer) evalComptimeMatchSelect(fc *funcChecker, me *ast.MatchExpr, scrutExprs []ast.Expr) (int, map[*Symbol]*ctVal, bool) {
	if me == nil || fc == nil {
		return -1, nil, false
	}
	st := a.newCheckerCtState(fc, me.Span, 0)
	scruts := make([]*ctVal, len(scrutExprs))
	for i, sc := range scrutExprs {
		sv, sig := st.evalExpr(sc)
		if sig.kind == sigAbort {
			st.reportAbort(sig, sc.GetSpan())
			return -1, nil, false
		}
		if sig.kind == sigUnwind {
			a.Rep.Errorf(sc.GetSpan(), "cannot unwind out of `comptime match` scrutinee")
			return -1, nil, false
		}
		scruts[i] = sv
	}
	arms := me.Arms
	if a.curBM != nil {
		if expanded, ok := a.curBM.MatchArms[me]; ok {
			arms = expanded
		}
	}
	for idx, arm := range arms {
		if len(arm.Cases) == 0 {
			return idx, nil, true
		}
		for _, mc := range arm.Cases {
			matched, bindings, sig := st.matchCase(scruts, mc)
			if sig.kind == sigAbort {
				st.reportAbort(sig, mc.Span)
				return -1, nil, false
			}
			if sig.kind == sigUnwind {
				a.Rep.Errorf(mc.Span, "cannot unwind out of `comptime match` pattern")
				return -1, nil, false
			}
			if matched {
				out := make(map[*Symbol]*ctVal, len(bindings))
				for sym, sl := range bindings {
					if sl != nil {
						out[sym] = sl.val
					}
				}
				return idx, out, true
			}
		}
	}
	a.Rep.Errorf(me.Span, "no matching arm in compile-time `match`")
	return -1, nil, false
}

type comptimeForItem struct {
	idxVal  *ctVal
	itemVal *ctVal
}

func expandComptimeRangeItems(loV, hiV *ctVal, inclusive bool, maxIters int, span diag.Span) ([]comptimeForItem, ctSignal) {
	if maxIters <= 0 {
		maxIters = MaxComptimeUnrollIters
	}
	var out []comptimeForItem
	addItem := func(v *ctVal) ctSignal {
		if len(out) >= maxIters {
			return abortSig(span, "compile-time `for` loop exceeds unroll limit of %d iterations (reduce the range or use a runtime `for` loop)", maxIters)
		}
		out = append(out, comptimeForItem{idxVal: v})
		return okSig()
	}
	if loV.kind == ctChar {
		lo, hi := loV.ch, hiV.ch
		for cur := lo; ; {
			if inclusive {
				if cur > hi {
					break
				}
			} else if cur >= hi {
				break
			}
			if sig := addItem(newCtChar(cur)); sig.kind != sigNone {
				return nil, sig
			}
			if cur == hi {
				break
			}
			cur++
		}
		return out, okSig()
	}
	p := loV.prim
	if p == nil {
		p = types.TypeInt
	}
	if !p.IsSignedInt() {
		lo, hi := loV.asUint64(), hiV.asUint64()
		for cur := lo; ; {
			if inclusive {
				if cur > hi {
					break
				}
			} else if cur >= hi {
				break
			}
			if sig := addItem(newCtInt(cur, p)); sig.kind != sigNone {
				return nil, sig
			}
			if cur == hi {
				break
			}
			cur++
		}
		return out, okSig()
	}
	lo, hi := loV.asInt64(), hiV.asInt64()
	for cur := lo; ; {
		if inclusive {
			if cur > hi {
				break
			}
		} else if cur >= hi {
			break
		}
		if sig := addItem(newCtSignedInt(cur, p)); sig.kind != sigNone {
			return nil, sig
		}
		if cur == hi {
			break
		}
		cur++
	}
	return out, okSig()
}

func (a *Analyzer) evalComptimeForItems(fc *funcChecker, fe *ast.ForExpr, sym1, sym2 *Symbol, maxIters int) ([]comptimeForItem, bool) {
	if fe == nil || fc == nil {
		return nil, false
	}
	if maxIters <= 0 {
		maxIters = MaxComptimeUnrollIters
	}
	st := a.newCheckerCtState(fc, fe.Span, 0)
	checkLimit := func(n int) bool {
		if n > maxIters {
			a.Rep.ErrorWithHint(
				fe.Span,
				fmt.Sprintf("compile-time `for` loop has %d iterations, exceeding unroll limit of %d", n, maxIters),
				"reduce the compile-time collection/range size, annotate the function with `@comptime_limit(...)`, or use a runtime `for` loop",
			)
			return false
		}
		return true
	}

	switch fe.Kind {
	case ast.ForRange:
		loV, sig := st.evalExpr(fe.CondOrIter)
		if sig.kind != sigNone {
			if sig.kind == sigAbort {
				st.reportAbort(sig, fe.CondOrIter.GetSpan())
			} else {
				a.Rep.Errorf(fe.CondOrIter.GetSpan(), "cannot unwind out of `comptime for` range bound")
			}
			return nil, false
		}
		hiV, sig := st.evalExpr(fe.RangeEnd)
		if sig.kind != sigNone {
			if sig.kind == sigAbort {
				st.reportAbort(sig, fe.RangeEnd.GetSpan())
			} else {
				a.Rep.Errorf(fe.RangeEnd.GetSpan(), "cannot unwind out of `comptime for` range bound")
			}
			return nil, false
		}
		items, rsig := expandComptimeRangeItems(loV, hiV, fe.Inclusive, maxIters, fe.Span)
		if rsig.kind == sigAbort {
			st.reportAbort(rsig, fe.Span)
			return nil, false
		}
		return items, true

	case ast.ForIter:
		colV, sig := st.evalExpr(fe.CondOrIter)
		if sig.kind != sigNone {
			if sig.kind == sigAbort {
				st.reportAbort(sig, fe.CondOrIter.GetSpan())
			} else {
				a.Rep.Errorf(fe.CondOrIter.GetSpan(), "cannot unwind out of `comptime for` sequence expression")
			}
			return nil, false
		}
		twoVars := sym2 != nil
		buildSliceItems := func(elems []*ctVal) ([]comptimeForItem, bool) {
			if !checkLimit(len(elems)) {
				return nil, false
			}
			out := make([]comptimeForItem, len(elems))
			for i, el := range elems {
				if twoVars {
					out[i] = comptimeForItem{
						idxVal:  newCtSignedInt(int64(i), types.TypeInt),
						itemVal: el.clone(),
					}
				} else {
					out[i] = comptimeForItem{idxVal: el.clone()}
				}
			}
			return out, true
		}
		buildMapItems := func(entries []ctMapEntry) ([]comptimeForItem, bool) {
			if !checkLimit(len(entries)) {
				return nil, false
			}
			out := make([]comptimeForItem, len(entries))
			for i, ent := range entries {
				if twoVars {
					out[i] = comptimeForItem{
						idxVal:  ent.key.clone(),
						itemVal: ent.val.clone(),
					}
				} else if sym1 != nil {
					if entrySt, ok := sym1.Type.(*types.Struct); ok {
						fields := map[string]*ctVal{"key": ent.key.clone(), "value": ent.val.clone()}
						order := []string{"key", "value"}
						out[i] = comptimeForItem{
							idxVal: &ctVal{kind: ctStruct, ty: entrySt, st: &ctStructBox{ty: entrySt, fields: fields, fieldOrder: order}},
						}
					}
				}
			}
			return out, true
		}

		switch colV.kind {
		case ctList:
			if colV.list == nil {
				return nil, true
			}
			return buildSliceItems(colV.list.elems)
		case ctArray:
			if colV.arr == nil {
				return nil, true
			}
			return buildSliceItems(colV.arr.elems)
		case ctString:
			var out []comptimeForItem
			for byteOff, ch := range colV.s {
				if !checkLimit(len(out) + 1) {
					return nil, false
				}
				if twoVars {
					out = append(out, comptimeForItem{
						idxVal:  newCtSignedInt(int64(byteOff), types.TypeInt),
						itemVal: newCtChar(ch),
					})
				} else {
					out = append(out, comptimeForItem{idxVal: newCtChar(ch)})
				}
			}
			return out, true
		case ctSet:
			if colV.set == nil {
				return nil, true
			}
			return buildSliceItems(colV.set.elems)
		case ctMap:
			if colV.m == nil {
				return nil, true
			}
			return buildMapItems(colV.m.entries)
		case ctRange:
			if colV.rng == nil {
				return nil, true
			}
			loV := newCtInt(colV.rng.low, colV.rng.prim)
			hiV := newCtInt(colV.rng.high, colV.rng.prim)
			if colV.rng.prim == types.TypeChar {
				loV = newCtChar(rune(colV.rng.low))
				hiV = newCtChar(rune(colV.rng.high))
			}
			items, rsig := expandComptimeRangeItems(loV, hiV, colV.rng.inclusive, maxIters, fe.Span)
			if rsig.kind == sigAbort {
				st.reportAbort(rsig, fe.Span)
				return nil, false
			}
			return items, true
		case ctStruct:
			if elems, ok := listStructElems(colV); ok {
				return buildSliceItems(elems)
			}
			if elems, ok := arrayStructElems(colV); ok {
				return buildSliceItems(elems)
			}
			if elems, ok := setStructElems(colV); ok {
				return buildSliceItems(elems)
			}
			if entries, ok := mapStructEntries(colV); ok {
				return buildMapItems(entries)
			}
		}
		a.Rep.Errorf(fe.CondOrIter.GetSpan(), "unsupported sequence value in `comptime for`")
		return nil, false
	}
	return nil, false
}

// evalConstInt evaluates a compile-time integer expression (including calls to `const` functions,
// `size_of[T]()`, `align_of[T]()`, `mod.C`, etc.) cleanly across compiler passes.
func (a *Analyzer) evalConstInt(mod *Module, e ast.Expr) (int64, bool) {
	return a.evalConstIntWithSubst(mod, e, nil)
}

func (a *Analyzer) evalConstIntWithSubst(mod *Module, e ast.Expr, subst map[string]types.Type) (int64, bool) {
	if e == nil {
		return 0, false
	}
	if v, ok := a.evalSimpleConstIntWithSubst(mod, e, subst); ok {
		return v, true
	}
	if a.Prog == nil {
		return 0, false
	}
	// Typecheck expression in a temporary const checker context and evaluate cleanly.
	prevBM := a.curBM
	if prevBM == nil {
		a.curBM = a.Prog.TopBodyMaps
	}
	beforeErrs := a.errorCount()
	fc := a.newFuncChecker(mod, subst)
	fc.comptimeExprDepth++
	fc.pushScope()
	fc.pushConstSubstSymbols()
	ty := fc.checkExprExpect(e, types.TypeInt, true)
	fc.popScope()
	if ty == types.TypeInvalid || a.errorCount() > beforeErrs {
		a.curBM = prevBM
		return 0, false
	}
	st := newCtState(a, mod, 0)
	st.frames = []ctFrame{{
		mod:      mod,
		bm:       a.curBM,
		callSpan: e.GetSpan(),
		locals:   make(map[*Symbol]*ctSlot),
	}}
	val, sig := st.evalExpr(e)
	a.curBM = prevBM
	if sig.kind == sigAbort {
		st.reportAbort(sig, e.GetSpan())
		return 0, false
	}
	if sig.kind != sigNone || val == nil {
		return 0, false
	}
	if val.kind != ctInt && val.kind != ctChar {
		return 0, false
	}
	return val.asInt64(), true
}

func (a *Analyzer) errorCount() int {
	if a.Rep == nil {
		return 0
	}
	n := 0
	for _, d := range a.Rep.Diagnostics() {
		if d.Severity == diag.Error {
			n++
		}
	}
	return n
}

func (a *Analyzer) ensureStructFieldsForLayout(st *types.Struct, span diag.Span) bool {
	if st == nil || len(st.Fields) > 0 || (a.populatedStructs != nil && a.populatedStructs[st]) {
		return true
	}
	if a.resolvingStructFields != nil && a.resolvingStructFields[st] {
		a.Rep.Errorf(span, "cyclic dependency computing compile-time layout of struct %s", st.String())
		return false
	}
	decl := a.Prog.StructDecls[st]
	mod := a.Prog.StructMods[st]
	if decl != nil && mod != nil && len(decl.Fields) > 0 && len(decl.TypeParams) == 0 {
		a.populateStructFields(mod, st, decl, nil)
	}
	return true
}

func (a *Analyzer) evalSimpleConstInt(mod *Module, e ast.Expr) (int64, bool) {
	return a.evalSimpleConstIntWithSubst(mod, e, nil)
}

func (a *Analyzer) evalSimpleConstIntWithSubst(mod *Module, e ast.Expr, subst map[string]types.Type) (int64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return int64(v.Val), true
	case *ast.CharLit:
		return int64(v.Val), true
	case *ast.IdentExpr:
		if subst != nil {
			if sub, ok := subst[v.Name]; ok {
				if cv, isCV := sub.(*types.ConstValue); isCV && cv.Kind == types.ConstInt {
					return cv.IntVal, true
				}
				return 0, false
			}
		}
		if sym := a.lookupTopSymbol(mod, "", v.Name); sym != nil && sym.IsConstInt {
			return sym.ConstIntVal, true
		}
		return 0, false
	case *ast.MemberExpr:
		if !v.QuestDot && !v.MemberHasOpt && len(v.BaseTypeArgs) == 0 {
			if modId, ok := v.Base.(*ast.IdentExpr); ok && !modId.IsOpt {
				if sym := a.lookupTopSymbol(mod, modId.Name, v.Member); sym != nil && sym.IsConstInt {
					return sym.ConstIntVal, true
				}
			}
		}
		return 0, false
	case *ast.ComptimeExpr:
		return a.evalSimpleConstIntWithSubst(mod, v.Expr, subst)
	case *ast.UnaryExpr:
		sub, ok := a.evalSimpleConstIntWithSubst(mod, v.Operand, subst)
		if !ok {
			return 0, false
		}
		switch v.Op {
		case token.Minus:
			return -sub, true
		case token.Tilde:
			return ^sub, true
		}
	case *ast.BinaryExpr:
		l, ok1 := a.evalSimpleConstIntWithSubst(mod, v.Left, subst)
		r, ok2 := a.evalSimpleConstIntWithSubst(mod, v.Right, subst)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch v.Op {
		case token.Plus:
			return l + r, true
		case token.Minus:
			return l - r, true
		case token.Star:
			return l * r, true
		case token.Slash:
			if r == 0 || (l == math.MinInt64 && r == -1) {
				return 0, false
			}
			return l / r, true
		case token.Percent:
			if r == 0 || (l == math.MinInt64 && r == -1) {
				return 0, false
			}
			return l % r, true
		case token.Shl:
			if r < 0 || r >= 64 {
				return 0, false
			}
			return l << uint(r), true
		case token.Shr:
			if r < 0 || r >= 64 {
				return 0, false
			}
			return l >> uint(r), true
		case token.Amp:
			return l & r, true
		case token.Pipe:
			return l | r, true
		case token.Caret:
			return l ^ r, true
		}
	}
	return 0, false
}

func (a *Analyzer) evalConstIntFromTypeExpr(mod *Module, te ast.TypeExpr) (int64, bool) {
	return a.evalConstIntFromTypeExprWithSubst(mod, te, nil)
}

func (a *Analyzer) evalConstIntFromTypeExprWithSubst(mod *Module, te ast.TypeExpr, subst map[string]types.Type) (int64, bool) {
	if ca, ok := te.(*ast.ConstArgType); ok {
		return a.evalConstIntWithSubst(mod, ca.Expr, subst)
	}
	if nt, ok := te.(*ast.NamedType); ok {
		if nt.Module == "" && subst != nil {
			if sub, ok := subst[nt.Name]; ok {
				if cv, isCV := sub.(*types.ConstValue); isCV && cv.Kind == types.ConstInt {
					return cv.IntVal, true
				}
			}
		}
		if val, err := strconv.ParseInt(nt.Name, 10, 64); err == nil {
			return val, true
		}
		if sym := a.lookupTopSymbol(mod, nt.Module, nt.Name); sym != nil && sym.IsConstInt {
			return sym.ConstIntVal, true
		}
	}
	return 0, false
}

// -----------------------------------------------------------------------------
// Function Coloring Helpers & Target Layout Query
// -----------------------------------------------------------------------------

func (a *Analyzer) isComptimeCallable(fi *FuncInstance) bool {
	if fi == nil {
		return false
	}
	return fi.IsConst
}

func (a *Analyzer) ensureFuncCheckedForComptime(fi *FuncInstance, callSpan diag.Span) {
	if fi == nil {
		return
	}
	if !fi.Checked {
		prevBM := a.curBM
		prevInBody := a.inBodyPass
		a.inBodyPass = true
		if fi.RetInferred {
			_ = a.ensureFuncRetResolved(fi, callSpan)
		} else {
			a.checkFuncInstance(fi)
		}
		a.inBodyPass = prevInBody
		a.curBM = prevBM
	}
}

func (a *Analyzer) targetPtrBytes() int64 {
	if a.target.PtrSize > 0 {
		return int64(a.target.PtrSize)
	}
	if a.target.Arch == "x86" || a.target.Arch == "wasm32" {
		return 4
	}
	return 8
}

func (a *Analyzer) typeAlign(t types.Type) int64 {
	if t == nil {
		return 1
	}
	ptrSz := a.targetPtrBytes()
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PrimBool, types.PrimI8, types.PrimU8:
			return 1
		case types.PrimI16, types.PrimU16:
			return 2
		case types.PrimI32, types.PrimU32, types.PrimF32, types.PrimChar:
			return 4
		default:
			return 8
		}
	case *types.Ptr, *types.Ref:
		return ptrSz
	case *types.Distinct:
		return a.typeAlign(v.Base)
	case *types.Enum:
		if !v.IsTagged {
			if v.Underlying != nil {
				return a.typeAlign(v.Underlying)
			}
			return 8
		}
		return 8
	case *types.Struct:
		_ = a.ensureStructFieldsForLayout(v, diag.Span{})
		if v.Packed {
			return 1
		}
		maxA := int64(1)
		for _, f := range v.Fields {
			if fa := a.typeAlign(f.Type); fa > maxA {
				maxA = fa
			}
		}
		return maxA
	case *types.Optional:
		if v.IsBoxed() {
			return ptrSz
		}
		al := a.typeAlign(v.Elem)
		if al < 1 {
			return 1
		}
		return al
	case *types.StackArray:
		return a.typeAlign(v.Elem)
	case *types.Tuple:
		maxA := int64(1)
		for _, el := range v.Elems {
			if ea := a.typeAlign(el); ea > maxA {
				maxA = ea
			}
		}
		return maxA
	default:
		return ptrSz
	}
}

func (a *Analyzer) typeSize(t types.Type) int64 {
	if t == nil {
		return 0
	}
	ptrSz := a.targetPtrBytes()
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PrimVoid, types.PrimNever:
			return 0
		case types.PrimBool, types.PrimI8, types.PrimU8:
			return 1
		case types.PrimI16, types.PrimU16:
			return 2
		case types.PrimI32, types.PrimU32, types.PrimF32, types.PrimChar:
			return 4
		case types.PrimI64, types.PrimU64, types.PrimF64:
			return 8
		case types.PrimString:
			return 3 * ptrSz
		default:
			return ptrSz
		}
	case *types.Ptr, *types.Ref:
		return ptrSz
	case *types.Distinct:
		return a.typeSize(v.Base)
	case *types.Enum:
		if !v.IsTagged {
			if v.Underlying != nil {
				return a.typeSize(v.Underlying)
			}
			return 8
		}
		maxPayloadWords := int64(2)
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple {
				w := (a.typeSize(vr.Payload) + 7) / 8
				if w > maxPayloadWords {
					maxPayloadWords = w
				}
			}
		}
		return 8 + maxPayloadWords*8
	case *types.Struct:
		_ = a.ensureStructFieldsForLayout(v, diag.Span{})
		if v.Packed {
			var tot int64
			for _, f := range v.Fields {
				tot += a.typeSize(f.Type)
			}
			if tot == 0 {
				return 1
			}
			return tot
		}
		var offset int64
		maxAlign := int64(1)
		for _, f := range v.Fields {
			fa := a.typeAlign(f.Type)
			if fa > maxAlign {
				maxAlign = fa
			}
			offset = (offset + fa - 1) & -fa
			offset += a.typeSize(f.Type)
		}
		total := (offset + maxAlign - 1) & -maxAlign
		if total == 0 {
			return 1
		}
		return total
	case *types.Vector:
		return 3 * ptrSz
	case *types.Optional:
		if v.IsBoxed() {
			return ptrSz
		}
		elemAlign := a.typeAlign(v.Elem)
		if elemAlign < 1 {
			elemAlign = 1
		}
		total := elemAlign + a.typeSize(v.Elem)
		return (total + elemAlign - 1) & -elemAlign
	case *types.StackArray:
		return v.Size * a.typeSize(v.Elem)
	case *types.Tuple:
		if len(v.Elems) == 0 {
			return 1
		}
		var offset int64
		maxAlign := int64(1)
		for _, el := range v.Elems {
			fa := a.typeAlign(el)
			if fa > maxAlign {
				maxAlign = fa
			}
			offset = (offset + fa - 1) & -fa
			offset += a.typeSize(el)
		}
		return (offset + maxAlign - 1) & -maxAlign
	case *types.Func:
		if !v.Closure {
			return ptrSz
		}
		return 4 * ptrSz
	case *types.Interface:
		return 3 * ptrSz
	default:
		return ptrSz
	}
}

// -----------------------------------------------------------------------------
// Expression & Statement Evaluation
// -----------------------------------------------------------------------------

func (s *ctState) lookupSlot(sym *Symbol) (*ctSlot, bool) {
	if sym == nil {
		return nil, false
	}
	if fr := s.frame(); fr != nil {
		if sl, ok := fr.locals[sym]; ok {
			return sl, true
		}
		if sl, ok := fr.captures[sym]; ok {
			return sl, true
		}
	}
	return nil, false
}

func (s *ctState) evalPlace(e ast.Expr) (ctPlace, ctSignal) {
	switch v := e.(type) {
	case *ast.UnsafeExpr:
		if s.allowStdUnsafe() {
			return s.evalPlace(v.Expr)
		}
		return ctPlace{}, abortSig(v.Span, "`unsafe` is not allowed in compile-time evaluation")
	case *ast.ComptimeExpr:
		return s.evalPlace(v.Expr)
	case *ast.UnaryExpr:
		if v.Op == token.Amp {
			return s.evalPlace(v.Operand)
		}
		if v.Op == token.Star {
			pv, sig := s.evalExpr(v.Operand)
			if sig.kind != sigNone {
				return ctPlace{}, sig
			}
			if pv == nil || pv.kind != ctPointer {
				return ctPlace{}, abortSig(v.Span, "cannot dereference non-pointer value at compile time")
			}
			return s.ptrDerefPlace(pv.ptrVal, v.Span)
		}
	case *ast.IdentExpr:
		inf := s.infoFor(v)
		var sym *Symbol
		if inf != nil {
			sym = inf.Sym
		}
		if sym == nil {
			sym = s.a.lookupTopSymbol(s.curMod(), "", v.Name)
		}
		sl, ok := s.lookupSlot(sym)
		if !ok {
			if sym != nil && sym.IsTopLevel && sym.IsConst {
				return ctPlace{
					get: func() (*ctVal, ctSignal) {
						return s.evalIdent(v)
					},
					set: func(*ctVal) ctSignal {
						return abortSig(v.Span, "cannot mutate `const` binding %q", v.Name)
					},
				}, okSig()
			}
			return ctPlace{}, abortSig(v.Span, "cannot reference non-local place %q at compile time", v.Name)
		}
		return ctPlace{
			get: func() (*ctVal, ctSignal) {
				return sl.getVal(v.Span, v.Name)
			},
			set: func(nv *ctVal) ctSignal {
				return sl.setVal(nv)
			},
		}, okSig()

	case *ast.MemberExpr:
		baseInf := s.infoFor(v.Base)
		if baseInf != nil {
			if _, isPtr := baseInf.Type.(*types.Ptr); isPtr {
				pv, sig := s.evalExpr(v.Base)
				if sig.kind != sigNone {
					return ctPlace{}, sig
				}
				if pv == nil || pv.kind != ctPointer {
					return ctPlace{}, abortSig(v.Span, "cannot dereference non-pointer member base at compile time")
				}
				dp, sig := s.ptrDerefPlace(pv.ptrVal, v.Span)
				if sig.kind != sigNone {
					return ctPlace{}, sig
				}
				inf := s.infoFor(v)
				return ctPlace{
					get: func() (*ctVal, ctSignal) {
						bv, s1 := dp.get()
						if s1.kind != sigNone {
							return nil, s1
						}
						return s.extractMember(bv, v, inf)
					},
					set: func(nv *ctVal) ctSignal {
						bv, s1 := dp.get()
						if s1.kind != sigNone {
							return s1
						}
						if bv.kind == ctStruct && bv.st != nil {
							bv.st.fields[v.Member] = nv
							return dp.set(bv)
						}
						if bv.kind == ctTuple && bv.tup != nil && inf != nil && inf.TupleFieldIdx < len(bv.tup.elems) {
							bv.tup.elems[inf.TupleFieldIdx] = nv
							return dp.set(bv)
						}
						return abortSig(v.Span, "cannot assign field %q at compile time", v.Member)
					},
				}, okSig()
			}
		}
		basePlace, sig := s.evalPlace(v.Base)
		if sig.kind != sigNone {
			return ctPlace{}, sig
		}
		inf := s.infoFor(v)
		return ctPlace{
			get: func() (*ctVal, ctSignal) {
				bv, s1 := basePlace.get()
				if s1.kind != sigNone {
					return nil, s1
				}
				return s.extractMember(bv, v, inf)
			},
			set: func(nv *ctVal) ctSignal {
				bv, s1 := basePlace.get()
				if s1.kind != sigNone {
					return s1
				}
				if bv.kind == ctStruct && bv.st != nil {
					bv.st.fields[v.Member] = nv
					return basePlace.set(bv)
				}
				if bv.kind == ctTuple && bv.tup != nil && inf != nil && inf.TupleFieldIdx < len(bv.tup.elems) {
					bv.tup.elems[inf.TupleFieldIdx] = nv
					return basePlace.set(bv)
				}
				return abortSig(v.Span, "cannot assign field %q at compile time", v.Member)
			},
		}, okSig()

	case *ast.IndexExpr:
		baseInf := s.infoFor(v.Base)
		if baseInf != nil {
			if _, isPtr := baseInf.Type.(*types.Ptr); isPtr {
				pv, sig := s.evalExpr(v.Base)
				if sig.kind != sigNone {
					return ctPlace{}, sig
				}
				idxVal, sig := s.evalExpr(v.Index)
				if sig.kind != sigNone {
					return ctPlace{}, sig
				}
				if pv == nil || pv.kind != ctPointer || pv.ptrVal == nil {
					return ctPlace{}, abortSig(v.Span, "cannot index null pointer at compile time")
				}
				idx := idxVal.asInt64()
				elemPtr := &ctPtr{
					alloc:    pv.ptrVal.alloc,
					byteOff:  pv.ptrVal.byteOff + idx*pv.ptrVal.elemSize,
					elemSize: pv.ptrVal.elemSize,
					elemTy:   pv.ptrVal.elemTy,
					castTy:   pv.ptrVal.castTy,
				}
				if idx == 0 {
					elemPtr.place = pv.ptrVal.place
				}
				return s.ptrDerefPlace(elemPtr, v.Span)
			}
		}
		basePlace, sig := s.evalPlace(v.Base)
		if sig.kind != sigNone {
			return ctPlace{}, sig
		}
		idxVal, sig := s.evalExpr(v.Index)
		if sig.kind != sigNone {
			return ctPlace{}, sig
		}
		return ctPlace{
			get: func() (*ctVal, ctSignal) {
				bv, s1 := basePlace.get()
				if s1.kind != sigNone {
					return nil, s1
				}
				return s.evalIndexGet(bv, idxVal, v.Span)
			},
			set: func(nv *ctVal) ctSignal {
				bv, s1 := basePlace.get()
				if s1.kind != sigNone {
					return s1
				}
				return s.evalIndexSet(bv, idxVal, nv, v.Span)
			},
		}, okSig()

	case *ast.CallExpr:
		inf := s.infoFor(v)
		if inf != nil && !inf.IsOptChain && (inf.BuiltinName == "struct.field" || inf.BuiltinName == "seq.elem" || inf.BuiltinName == "enum.payload") {
			if mem, isMem := v.Callee.(*ast.MemberExpr); isMem {
				basePlace, sig := s.evalDerefBasePlace(mem.Base, v.Span)
				if sig.kind != sigNone {
					return ctPlace{}, sig
				}
				fieldName := inf.ResolvedField
				elemIdx := inf.TupleFieldIdx
				builtin := inf.BuiltinName
				enumVar := inf.EnumVariant
				return ctPlace{
					get: func() (*ctVal, ctSignal) {
						bv, s1 := basePlace.get()
						if s1.kind != sigNone {
							return nil, s1
						}
						bv, s1 = s.derefCtVal(bv, v.Span)
						if s1.kind != sigNone {
							return nil, s1
						}
						switch builtin {
						case "struct.field":
							return s.readStructFieldCtVal(bv, fieldName, inf.Type, v.Span)
						case "enum.payload":
							return s.readEnumPayloadCtVal(bv, elemIdx, enumVar, v.Span)
						default:
							return s.readSeqElemCtVal(bv, elemIdx, v.Span)
						}
					},
					set: func(nv *ctVal) ctSignal {
						bv, s1 := basePlace.get()
						if s1.kind != sigNone {
							return s1
						}
						targetPlace := basePlace
						for bv != nil && bv.kind == ctPointer {
							if bv.ptrVal == nil {
								return abortSig(v.Span, "cannot dereference nil pointer/reference at compile time")
							}
							dp, s2 := s.ptrDerefPlace(bv.ptrVal, v.Span)
							if s2.kind != sigNone {
								return s2
							}
							targetPlace = dp
							bv, s1 = targetPlace.get()
							if s1.kind != sigNone {
								return s1
							}
						}
						if builtin == "enum.payload" {
							if bv == nil || bv.kind != ctEnum {
								return abortSig(v.Span, "cannot assign variant payload on non-enum at compile time")
							}
							if bv.variantIdx != elemIdx {
								return abortSig(v.Span, "variant payload assignment failed: active variant mismatch")
							}
							bv.payload = nv
							return targetPlace.set(bv)
						}
						if builtin == "struct.field" {
							if bv != nil && bv.kind == ctStruct && bv.st != nil {
								bv.st.fields[fieldName] = nv
								return targetPlace.set(bv)
							}
							return abortSig(v.Span, "cannot assign field %q at compile time", fieldName)
						}
						if bv != nil && bv.kind == ctTuple && bv.tup != nil && elemIdx >= 0 && elemIdx < len(bv.tup.elems) {
							bv.tup.elems[elemIdx] = nv
							return targetPlace.set(bv)
						}
						if bv != nil && bv.kind == ctArray && bv.arr != nil && elemIdx >= 0 && elemIdx < len(bv.arr.elems) {
							bv.arr.elems[elemIdx] = nv
							if bv.arr.alloc != nil && elemIdx < len(bv.arr.alloc.cells) {
								bv.arr.alloc.cells[elemIdx] = nv
							}
							return targetPlace.set(bv)
						}
						return abortSig(v.Span, "cannot assign element %d at compile time", elemIdx)
					},
				}, okSig()
			}
		}
	}
	return ctPlace{}, abortSig(e.GetSpan(), "expression is not an addressable place at compile time")
}

func (s *ctState) evalDerefBasePlace(base ast.Expr, span diag.Span) (ctPlace, ctSignal) {
	baseInf := s.infoFor(base)
	if baseInf != nil {
		switch baseInf.Type.(type) {
		case *types.Ptr, *types.Ref:
			pv, sig := s.evalExpr(base)
			if sig.kind != sigNone {
				return ctPlace{}, sig
			}
			if pv != nil && pv.kind == ctPointer && pv.ptrVal != nil {
				return s.ptrDerefPlace(pv.ptrVal, span)
			}
			if pv == nil || pv.kind == ctPointer {
				return ctPlace{}, abortSig(span, "cannot dereference nil pointer/reference at compile time")
			}
			// `ModeMut` ref param (`&self`) already dereferences via `refPlace` inside `evalExpr`;
			// fall through to `evalPlace(base)` so writes go back through `refPlace`.
		}
	}
	return s.evalPlace(base)
}

func (s *ctState) derefCtVal(bv *ctVal, span diag.Span) (*ctVal, ctSignal) {
	for bv != nil && bv.kind == ctPointer {
		if bv.ptrVal == nil {
			return nil, abortSig(span, "cannot dereference nil pointer/reference at compile time")
		}
		dp, sig := s.ptrDerefPlace(bv.ptrVal, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		var s2 ctSignal
		bv, s2 = dp.get()
		if s2.kind != sigNone {
			return nil, s2
		}
	}
	return bv, okSig()
}

func (s *ctState) readStructFieldCtVal(bv *ctVal, fieldName string, expectTy types.Type, span diag.Span) (*ctVal, ctSignal) {
	if bv != nil && bv.kind == ctStruct && bv.st != nil {
		if fv, ok := bv.st.fields[fieldName]; ok && fv != nil {
			return fv, okSig()
		}
		return newCtNone(expectTy), okSig()
	}
	return nil, abortSig(span, "cannot access field %q at compile time", fieldName)
}

func (s *ctState) readEnumPayloadCtVal(bv *ctVal, variantIdx int, vr *types.EnumVariant, span diag.Span) (*ctVal, ctSignal) {
	if bv == nil || bv.kind != ctEnum {
		return nil, abortSig(span, "cannot access variant payload on non-enum at compile time")
	}
	if bv.variantIdx != variantIdx {
		vname := fmt.Sprintf("#%d", bv.variantIdx)
		if bv.en != nil && bv.variantIdx >= 0 && bv.variantIdx < len(bv.en.Variants) {
			vname = bv.en.Variants[bv.variantIdx].Name
		}
		want := fmt.Sprintf("#%d", variantIdx)
		if vr != nil {
			want = vr.Name
		}
		return nil, abortSig(span, "variant payload access failed: expected %q, got %q", want, vname)
	}
	return bv.payload, okSig()
}

func (s *ctState) readSeqElemCtVal(bv *ctVal, idx int, span diag.Span) (*ctVal, ctSignal) {
	if bv != nil && bv.kind == ctTuple && bv.tup != nil {
		if idx >= 0 && idx < len(bv.tup.elems) {
			return bv.tup.elems[idx], okSig()
		}
		return nil, abortSig(span, "tuple index %d out of bounds (len %d)", idx, len(bv.tup.elems))
	}
	if bv != nil && bv.kind == ctArray && bv.arr != nil {
		if idx >= 0 && idx < len(bv.arr.elems) {
			return bv.arr.elems[idx], okSig()
		}
		return nil, abortSig(span, "array index %d out of bounds (len %d)", idx, len(bv.arr.elems))
	}
	return nil, abortSig(span, "cannot access element %d on %T at compile time", idx, bv)
}

func (s *ctState) curMod() *Module {
	if fr := s.frame(); fr != nil && fr.mod != nil {
		return fr.mod
	}
	return s.rootMod
}

func (s *ctState) curSubst() map[string]types.Type {
	for i := len(s.frames) - 1; i >= 0; i-- {
		fr := &s.frames[i]
		if len(fr.subst) > 0 {
			return fr.subst
		}
		if fr.fi != nil && len(fr.fi.TypeSubst) > 0 {
			return fr.fi.TypeSubst
		}
	}
	return nil
}

func (s *ctState) evalExpr(e ast.Expr) (*ctVal, ctSignal) {
	if e == nil {
		return newCtVoid(), okSig()
	}
	if sig := s.enterGoDepth(e.GetSpan()); sig.kind != sigNone {
		return nil, sig
	}
	defer s.leaveGoDepth()
	if sig := s.step(e.GetSpan()); sig.kind != sigNone {
		return nil, sig
	}

	inf := s.infoFor(e)
	var expectedTy types.Type
	if inf != nil {
		expectedTy = inf.Type
	}

	wrap := func(val *ctVal) *ctVal {
		if inf != nil {
			if inf.MovedLocal != nil {
				if sl, ok := s.lookupSlot(inf.MovedLocal); ok {
					sl.moved = true
				}
			}
			if inf.WrapOptional && val != nil && val.kind != ctOpt {
				return newCtSome(types.NewOptional(val.ty), val)
			}
		}
		return val
	}

	switch v := e.(type) {
	case *ast.IntLit:
		p := types.UnderlyingPrimitive(expectedTy)
		if p == nil || !p.IsInteger() {
			p = types.TypeInt
		}
		return wrap(newCtInt(v.Val, p)), okSig()

	case *ast.FloatLit:
		p := types.UnderlyingPrimitive(expectedTy)
		if p == nil || !p.IsFloat() {
			p = types.TypeFloat
		}
		return wrap(newCtFloat(v.Val, p)), okSig()

	case *ast.BoolLit:
		return wrap(newCtBool(v.Val)), okSig()

	case *ast.CharLit:
		return wrap(newCtChar(v.Val)), okSig()

	case *ast.StringLit:
		return wrap(newCtString(v.Val)), okSig()

	case *ast.NoneLit:
		return newCtNone(expectedTy), okSig()

	case *ast.InterpStringExpr:
		var sb strings.Builder
		for i, seg := range v.Segments {
			sb.WriteString(seg)
			if i < len(v.Exprs) {
				sub, sig := s.evalExpr(v.Exprs[i])
				if sig.kind != sigNone {
					return nil, sig
				}
				str, sig := s.valToString(sub, v.Exprs[i].GetSpan())
				if sig.kind != sigNone {
					return nil, sig
				}
				sb.WriteString(str)
			}
		}
		outStr := sb.String()
		if sig := s.allocMem(ctValMemCost+int64(len(outStr)), v.Span); sig.kind != sigNone {
			return nil, sig
		}
		return wrap(newCtString(outStr)), okSig()

	case *ast.IdentExpr:
		val, sig := s.evalIdent(v)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.MemberExpr:
		val, sig := s.evalMember(v, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.UnaryExpr:
		val, sig := s.evalUnary(v, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.BinaryExpr:
		val, sig := s.evalBinary(v, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.RangeExpr:
		loVal, sig := s.evalExpr(v.Low)
		if sig.kind != sigNone {
			return nil, sig
		}
		hiVal, sig := s.evalExpr(v.High)
		if sig.kind != sigNone {
			return nil, sig
		}
		prim := loVal.prim
		if loVal.kind == ctChar {
			prim = types.TypeChar
		}
		return &ctVal{
			kind: ctRange,
			ty:   expectedTy,
			rng: &ctRangeBox{
				low:       loVal.asUint64(),
				high:      hiVal.asUint64(),
				inclusive: v.Inclusive,
				prim:      prim,
			},
		}, okSig()

	case *ast.VecLit:
		elems := make([]*ctVal, 0, len(v.Elems))
		var elemTy types.Type
		if expectedTy != nil {
			if vec, ok := expectedTy.(*types.Vector); ok {
				elemTy = vec.Elem
			} else if st, ok := expectedTy.(*types.Struct); ok && s.a.isStdStructTemplate(st, s.a.Prog.GenStructList) && len(st.TypeArgs) == 1 {
				elemTy = st.TypeArgs[0]
			}
		}
		for _, elExpr := range v.Elems {
			elVal, sig := s.evalExpr(elExpr)
			if sig.kind != sigNone {
				return nil, sig
			}
			elems = append(elems, elVal.clone())
		}
		if arr, isArr := expectedTy.(*types.StackArray); isArr {
			if sig := s.allocMem(int64(len(elems))*s.simElemSize(arr.Elem), v.Span); sig.kind != sigNone {
				return nil, sig
			}
			return wrap(&ctVal{kind: ctArray, ty: expectedTy, arr: &ctArrayBox{elemTy: arr.Elem, elems: elems}}), okSig()
		}
		_ = elemTy
		lv, sig := s.makeListStruct(expectedTy, elems, v.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(lv), okSig()

	case *ast.ArrayLit:
		var elemTy types.Type
		var size int64
		if arr, ok := expectedTy.(*types.StackArray); ok {
			elemTy = arr.Elem
			size = arr.Size
		} else {
			var okSz bool
			size, okSz = s.a.evalConstIntWithSubst(s.curMod(), v.Count, s.curSubst())
			if !okSz || size <= 0 {
				return nil, abortSig(v.Count.GetSpan(), "stack array size in `[N: ...]` must be positive")
			}
		}
		if sig := s.allocMem(size*s.simElemSize(elemTy), v.Span); sig.kind != sigNone {
			return nil, sig
		}
		elems := make([]*ctVal, size)
		if len(v.Elems) == 1 && size > 1 {
			first, sig := s.evalExpr(v.Elems[0])
			if sig.kind != sigNone {
				return nil, sig
			}
			for i := int64(0); i < size; i++ {
				elems[i] = first.clone()
			}
		} else {
			for i := 0; i < len(v.Elems) && int64(i) < size; i++ {
				el, sig := s.evalExpr(v.Elems[i])
				if sig.kind != sigNone {
					return nil, sig
				}
				elems[i] = el.clone()
			}
		}
		return wrap(&ctVal{kind: ctArray, ty: expectedTy, arr: &ctArrayBox{elemTy: elemTy, elems: elems}}), okSig()

	case *ast.SetLit:
		elems := make([]*ctVal, 0, len(v.Elems))
		for _, elExpr := range v.Elems {
			elVal, sig := s.evalExpr(elExpr)
			if sig.kind != sigNone {
				return nil, sig
			}
			elems = append(elems, elVal.clone())
		}
		sv, sig := s.makeSetStruct(expectedTy, elems, v.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(sv), okSig()

	case *ast.MapLit:
		entries := make([]ctMapEntry, 0, len(v.Entries))
		for _, ent := range v.Entries {
			kv, sig := s.evalExpr(ent.Key)
			if sig.kind != sigNone {
				return nil, sig
			}
			vv, sig := s.evalExpr(ent.Value)
			if sig.kind != sigNone {
				return nil, sig
			}
			entries = append(entries, ctMapEntry{key: kv.clone(), val: vv.clone()})
		}
		mv, sig := s.makeMapStruct(expectedTy, entries, v.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(mv), okSig()

	case *ast.EmptyHashLit:
		if expectedTy != nil {
			if st, ok := expectedTy.(*types.Struct); ok && s.a.isStdStructTemplate(st, s.a.Prog.GenStructSet) {
				sv, sig := s.makeSetStruct(expectedTy, nil, v.Span)
				if sig.kind != sigNone {
					return nil, sig
				}
				return wrap(sv), okSig()
			}
			if _, ok := expectedTy.(*types.Set); ok {
				return wrap(&ctVal{kind: ctSet, ty: expectedTy, set: &ctSetBox{}}), okSig()
			}
		}
		mv, sig := s.makeMapStruct(expectedTy, nil, v.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(mv), okSig()

	case *ast.TupleLit:
		elems := make([]*ctVal, len(v.Elems))
		for i, elExpr := range v.Elems {
			el, sig := s.evalExpr(elExpr)
			if sig.kind != sigNone {
				return nil, sig
			}
			elems[i] = el.clone()
		}
		return wrap(&ctVal{kind: ctTuple, ty: expectedTy, tup: &ctTupleBox{elems: elems}}), okSig()

	case *ast.StructLit:
		val, sig := s.evalStructLit(v, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.FuncLit:
		var lam *LambdaInfo
		if inf != nil {
			lam = inf.Lambda
		}
		caps := make(map[*Symbol]*ctSlot)
		if lam != nil {
			for _, capInfo := range lam.Captures {
				if sl, ok := s.lookupSlot(capInfo.OuterSym); ok {
					caps[capInfo.OuterSym] = &ctSlot{val: sl.val.clone(), ty: capInfo.Type}
				}
			}
		}
		var fnTy *types.Func
		if expectedTy != nil {
			if f, ok := expectedTy.(*types.Func); ok {
				fnTy = f
			}
		}
		return wrap(&ctVal{kind: ctFn, ty: fnTy, lambda: lam, captures: caps}), okSig()

	case *ast.UnsafeExpr:
		if s.allowStdUnsafe() {
			val, sig := s.evalExpr(v.Expr)
			if sig.kind != sigNone {
				return nil, sig
			}
			return wrap(val), okSig()
		}
		return nil, abortSig(v.Span, "`unsafe` is not allowed in compile-time evaluation")

	case *ast.ComptimeExpr:
		val, sig := s.evalExpr(v.Expr)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.BlockExpr:
		val, sig := s.evalBlock(v)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.StmtBodyExpr:
		val, sig := s.evalStmt(v.Stmt)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.IfExpr:
		val, sig := s.evalIf(v, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.ForExpr:
		val, sig := s.evalFor(loopEval{
			expr:       v,
			kind:       v.Kind,
			condOrIter: v.CondOrIter,
			rangeEnd:   v.RangeEnd,
			inclusive:  v.Inclusive,
			var1Mut:    v.Var1Mut,
			var2Mut:    v.Var2Mut,
			evalType:   expectedTy,
			body:       v.Body,
			elseExpr:   v.Else,
			span:       v.Span,
			isFor:      true,
		}, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.WhileExpr:
		val, sig := s.evalFor(loopEval{
			expr:       v,
			kind:       v.Kind,
			condOrIter: v.CondOrIter,
			rangeEnd:   v.RangeEnd,
			inclusive:  v.Inclusive,
			var1Mut:    v.Var1Mut,
			var2Mut:    v.Var2Mut,
			evalType:   expectedTy,
			body:       v.Body,
			elseExpr:   v.Else,
			span:       v.Span,
			isFor:      false,
		}, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.MatchExpr:
		val, sig := s.evalMatch(v, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.CallExpr:
		val, sig := s.evalCall(v, inf)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.IndexExpr:
		bv, sig := s.evalExpr(v.Base)
		if sig.kind != sigNone {
			return nil, sig
		}
		iv, sig := s.evalExpr(v.Index)
		if sig.kind != sigNone {
			return nil, sig
		}
		val, sig := s.evalIndexGet(bv, iv, v.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()

	case *ast.SliceExpr:
		bv, sig := s.evalExpr(v.Base)
		if sig.kind != sigNone {
			return nil, sig
		}
		var loPtr, hiPtr *int64
		if v.Low != nil {
			lv, s1 := s.evalExpr(v.Low)
			if s1.kind != sigNone {
				return nil, s1
			}
			li := lv.asInt64()
			loPtr = &li
		}
		if v.High != nil {
			hv, s1 := s.evalExpr(v.High)
			if s1.kind != sigNone {
				return nil, s1
			}
			hi := hv.asInt64()
			hiPtr = &hi
		}
		val, sig := s.evalSlice(bv, loPtr, hiPtr, v.Inclusive, inf != nil && inf.BorrowAsView, expectedTy, v.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return wrap(val), okSig()
	}

	return nil, abortSig(e.GetSpan(), "unsupported expression `%T` in compile-time evaluation", e)
}

func (s *ctState) evalIdent(v *ast.IdentExpr) (*ctVal, ctSignal) {
	inf := s.infoFor(v)
	if inf != nil && inf.EnumType != nil && inf.EnumVariant != nil {
		for idx, vr := range inf.EnumType.Variants {
			if vr.Name == inf.EnumVariant.Name {
				return &ctVal{kind: ctEnum, ty: inf.EnumType, en: inf.EnumType, variantIdx: idx}, okSig()
			}
		}
	}
	var sym *Symbol
	if inf != nil {
		sym = inf.Sym
	}
	if sym == nil {
		sym = s.a.lookupTopSymbol(s.curMod(), "", v.Name)
	}
	if sym == nil {
		return nil, abortSig(v.Span, "unresolved symbol %q at compile time", v.Name)
	}
	if sl, ok := s.lookupSlot(sym); ok {
		return sl.getVal(v.Span, v.Name)
	}
	if sym.IsConstParam && sym.ConstVal != nil {
		if cv, ok := sym.ConstVal.(*ctVal); ok {
			return cv.clone(), okSig()
		}
	}
	if sym.FuncInst != nil {
		return &ctVal{kind: ctFn, ty: sym.Type, fn: sym.FuncInst}, okSig()
	}
	if sym.IsTopLevel {
		if !sym.IsConst {
			// Still allow legacy simple `bind INT_CONST = 42` folded in Pass 1A
			if sym.IsConstInt {
				p := types.UnderlyingPrimitive(sym.Type)
				if p == nil || !p.IsInteger() {
					p = types.TypeInt
				}
				return newCtSignedInt(sym.ConstIntVal, p), okSig()
			}
			return nil, abortSig(v.Span, "cannot read runtime `bind` global %q in compile-time expression (declare with `const`)", sym.Name)
		}
		if sym.ConstVal != nil {
			if cv, ok := sym.ConstVal.(*ctVal); ok {
				return cv.clone(), okSig()
			}
		}
		if tv := s.a.topVarMap[sym]; tv != nil {
			cv, ok := s.a.evalComptimeTopVar(tv)
			if !ok {
				return nil, abortSig(v.Span, "failed to evaluate `const` %q", sym.Name)
			}
			return cv.clone(), okSig()
		}
		if sym.IsConstInt {
			p := types.UnderlyingPrimitive(sym.Type)
			if p == nil || !p.IsInteger() {
				p = types.TypeInt
			}
			return newCtSignedInt(sym.ConstIntVal, p), okSig()
		}
	}
	return nil, abortSig(v.Span, "cannot read runtime local %q inside `comptime`", sym.Name)
}

func (s *ctState) evalMember(m *ast.MemberExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	if inf != nil && inf.EnumType != nil && inf.EnumVariant != nil {
		for idx, vr := range inf.EnumType.Variants {
			if vr.Name == inf.EnumVariant.Name {
				return &ctVal{kind: ctEnum, ty: inf.EnumType, en: inf.EnumType, variantIdx: idx}, okSig()
			}
		}
	}
	if inf != nil && inf.Sym != nil {
		sym := inf.Sym
		if sym.FuncInst != nil {
			return &ctVal{kind: ctFn, ty: sym.Type, fn: sym.FuncInst}, okSig()
		}
		if sym.IsTopLevel {
			if !sym.IsConst {
				if sym.IsConstInt {
					p := types.UnderlyingPrimitive(sym.Type)
					if p == nil || !p.IsInteger() {
						p = types.TypeInt
					}
					return newCtSignedInt(sym.ConstIntVal, p), okSig()
				}
				return nil, abortSig(m.Span, "cannot read runtime `bind` global %q in compile-time expression", sym.Name)
			}
			if sym.ConstVal != nil {
				if cv, ok := sym.ConstVal.(*ctVal); ok {
					return cv.clone(), okSig()
				}
			}
			if tv := s.a.topVarMap[sym]; tv != nil {
				cv, ok := s.a.evalComptimeTopVar(tv)
				if !ok {
					return nil, abortSig(m.Span, "failed to evaluate `const` %q", sym.Name)
				}
				return cv.clone(), okSig()
			}
		}
	}
	// Handle `comp.TARGET` directly (including in Pass 1A / top-level `comptime if` conditions).
	if baseId, isId := m.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && !m.QuestDot && !m.MemberHasOpt && len(m.BaseTypeArgs) == 0 && baseId.Name == "comp" && m.Member == "TARGET" {
		if tv := s.a.buildTargetCtVal(); tv != nil {
			return tv.clone(), okSig()
		}
	}
	// Handle `comp.TargetOs.darwin`, `comp.TargetArch.amd64`, `comp.Endian.little` in pre-body Pass 1 evaluation.
	if baseMem, isBM := m.Base.(*ast.MemberExpr); isBM && !baseMem.QuestDot && !baseMem.MemberHasOpt && len(baseMem.BaseTypeArgs) == 0 {
		if compId, isId := baseMem.Base.(*ast.IdentExpr); isId && !compId.IsOpt && compId.Name == "comp" {
			if t, ok := s.a.Prog.CompTypes[baseMem.Member]; ok {
				if en, isEn := t.(*types.Enum); isEn {
					for idx, vr := range en.Variants {
						if vr.Name == m.Member {
							return &ctVal{kind: ctEnum, ty: en, en: en, variantIdx: idx}, okSig()
						}
					}
				}
			}
		}
	}
	// Try module.CONST lookup when infoFor didn't record Sym (pre-body Pass 1C/1A).
	if baseId, isId := m.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && !m.QuestDot && !m.MemberHasOpt && len(m.BaseTypeArgs) == 0 {
		if sym := s.a.lookupTopSymbol(s.curMod(), baseId.Name, m.Member); sym != nil {
			if sym.FuncInst != nil {
				return &ctVal{kind: ctFn, ty: sym.Type, fn: sym.FuncInst}, okSig()
			}
			if sym.ConstVal != nil {
				if cv, ok := sym.ConstVal.(*ctVal); ok {
					return cv.clone(), okSig()
				}
			}
			if sym.IsConstInt {
				p := types.UnderlyingPrimitive(sym.Type)
				if p == nil || !p.IsInteger() {
					p = types.TypeInt
				}
				return newCtSignedInt(sym.ConstIntVal, p), okSig()
			}
		}
	}

	bv, sig := s.evalExpr(m.Base)
	if sig.kind != sigNone {
		return nil, sig
	}
	return s.extractMember(bv, m, inf)
}

func (s *ctState) extractMember(bv *ctVal, m *ast.MemberExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	if bv == nil {
		return nil, abortSig(m.Span, "nil value on field access `.%s`", m.Member)
	}
	if inf != nil && inf.IsOptChain && bv.kind == ctOpt {
		if !bv.hasOpt {
			return newCtNone(inf.Type), okSig()
		}
		res, sig := s.extractMember(bv.optElem, &ast.MemberExpr{Base: m.Base, Member: m.Member, Span: m.Span}, &ExprInfo{
			Type:          inf.Type,
			TupleFieldIdx: inf.TupleFieldIdx,
		})
		if sig.kind != sigNone {
			return nil, sig
		}
		if res.kind != ctOpt {
			return newCtSome(inf.Type, res), okSig()
		}
		return res, okSig()
	}
	if bv.kind == ctPointer {
		dp, sig := s.ptrDerefPlace(bv.ptrVal, m.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		dv, sig := dp.get()
		if sig.kind != sigNone {
			return nil, sig
		}
		return s.extractMember(dv, m, inf)
	}
	if bv.kind == ctArray && bv.arr != nil {
		if m.Member == "len" {
			return newCtSignedInt(int64(len(bv.arr.elems)), types.TypeInt), okSig()
		}
		if m.Member == "data" {
			if bv.arr.alloc == nil {
				id, addr := s.nextAllocIDAddr()
				bv.arr.alloc = &ctHeapAlloc{
					id:       id,
					baseAddr: addr,
					elemTy:   bv.arr.elemTy,
					elemSize: s.simElemSize(bv.arr.elemTy),
					cells:    bv.arr.elems,
				}
			}
			return &ctVal{
				kind: ctPointer,
				ty:   &types.Ptr{Elem: bv.arr.elemTy},
				ptrVal: &ctPtr{
					alloc:    bv.arr.alloc,
					elemTy:   bv.arr.elemTy,
					elemSize: bv.arr.alloc.elemSize,
				},
			}, okSig()
		}
	}
	if bv.kind == ctStruct && bv.st != nil {
		if fv, ok := bv.st.fields[m.Member]; ok {
			return fv, okSig()
		}
		// Optional omitted fields default to none.
		return newCtNone(nil), okSig()
	}
	if bv.kind == ctTuple && bv.tup != nil {
		idx := -1
		if inf != nil {
			idx = inf.TupleFieldIdx
		} else {
			if n, err := strconv.Atoi(m.Member); err == nil {
				idx = n
			}
		}
		if idx >= 0 && idx < len(bv.tup.elems) {
			return bv.tup.elems[idx], okSig()
		}
	}
	return nil, abortSig(m.Span, "cannot access member %q on %T", m.Member, bv.ty)
}

func (s *ctState) evalStructLit(sl *ast.StructLit, inf *ExprInfo) (*ctVal, ctSignal) {
	var st *types.Struct
	if inf != nil && inf.StructType != nil {
		st = inf.StructType
	} else if inf != nil {
		st, _ = inf.Type.(*types.Struct)
	}
	if st == nil {
		return nil, abortSig(sl.Span, "unresolved struct type in literal")
	}

	fields := make(map[string]*ctVal, len(st.Fields))
	order := make([]string, len(st.Fields))
	provided := make(map[string]bool, len(sl.Fields))

	for i, sf := range st.Fields {
		order[i] = sf.Name
	}
	for _, fi := range sl.Fields {
		fv, sig := s.evalExpr(fi.Value)
		if sig.kind != sigNone {
			return nil, sig
		}
		fields[fi.Name] = fv.clone()
		provided[fi.Name] = true
	}

	// Evaluate default field expressions and optional defaults (`none`) for omitted fields.
	for i, sf := range st.Fields {
		if provided[sf.Name] {
			continue
		}
		dv, sig := s.evalStructDefaultOrNone(st, i)
		if sig.kind != sigNone {
			return nil, sig
		}
		fields[sf.Name] = dv
	}
	if sig := s.allocMem(int64(len(order))*24, sl.Span); sig.kind != sigNone {
		return nil, sig
	}
	return &ctVal{
		kind: ctStruct,
		ty:   st,
		st:   &ctStructBox{ty: st, fields: fields, fieldOrder: order},
	}, okSig()
}

func (s *ctState) evalStructDefaultOrNone(st *types.Struct, idx int) (*ctVal, ctSignal) {
	if st == nil || idx < 0 || idx >= len(st.Fields) {
		return nil, okSig()
	}
	sf := st.Fields[idx]
	decl := s.a.Prog.StructDecls[st]
	if sf.HasDefault && decl != nil && idx < len(decl.Fields) && decl.Fields[idx].Default != nil {
		mod := s.a.Prog.StructMods[st]
		bm := s.a.Prog.StructBodyMaps[st]
		s.frames = append(s.frames, ctFrame{
			mod:      mod,
			bm:       bm,
			callSpan: decl.Fields[idx].Default.GetSpan(),
			locals:   make(map[*Symbol]*ctSlot),
		})
		defVal, sig := s.evalExpr(decl.Fields[idx].Default)
		s.frames = s.frames[:len(s.frames)-1]
		if sig.kind != sigNone {
			return nil, sig
		}
		return defVal.clone().wrapResult(sf.Type), okSig()
	}
	return newCtNone(sf.Type), okSig()
}

func (s *ctState) evalUnary(u *ast.UnaryExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	if u.Op == token.Minus {
		if lit, isLit := u.Operand.(*ast.IntLit); isLit {
			p := types.UnderlyingPrimitive(inf.Type)
			if p == nil || !p.IsInteger() {
				p = types.TypeInt
			}
			// Two's complement negation (`-128` for i8, `-9223372036854775808` for i64)
			return newCtInt(^lit.Val+1, p), okSig()
		}
	}
	sub, sig := s.evalExpr(u.Operand)
	if sig.kind != sigNone {
		return nil, sig
	}
	switch u.Op {
	case token.Minus:
		if sub.kind == ctFloat {
			return newCtFloat(-sub.f, sub.prim), okSig()
		}
		return newCtInt(^sub.u+1, sub.prim), okSig()
	case token.KwNot:
		return newCtBool(!sub.b), okSig()
	case token.Tilde:
		return newCtInt(^sub.u, sub.prim), okSig()
	case token.KwOwn:
		return sub.clone(), okSig()
	case token.Star:
		if sub == nil || sub.kind != ctPointer {
			return nil, abortSig(u.Span, "cannot dereference non-pointer value at compile time")
		}
		dp, sig := s.ptrDerefPlace(sub.ptrVal, u.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return dp.get()
	case token.Amp:
		return sub, okSig()
	}
	return nil, abortSig(u.Span, "unsupported unary operator `%s` at compile time", u.Op.String())
}

func (s *ctState) evalBinary(b *ast.BinaryExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	// Short-circuit operators first
	switch b.Op {
	case token.KwAnd:
		lv, sig := s.evalExpr(b.Left)
		if sig.kind != sigNone {
			return nil, sig
		}
		if !lv.b {
			return newCtBool(false), okSig()
		}
		rv, sig := s.evalExpr(b.Right)
		if sig.kind != sigNone {
			return nil, sig
		}
		return newCtBool(rv.b), okSig()

	case token.KwOr:
		lv, sig := s.evalExpr(b.Left)
		if sig.kind != sigNone {
			return nil, sig
		}
		if lv.b {
			return newCtBool(true), okSig()
		}
		rv, sig := s.evalExpr(b.Right)
		if sig.kind != sigNone {
			return nil, sig
		}
		return newCtBool(rv.b), okSig()

	case token.QuestQuest:
		lv, sig := s.evalExpr(b.Left)
		if sig.kind != sigNone {
			return nil, sig
		}
		if lv.kind == ctOpt {
			if lv.hasOpt {
				if inf != nil {
					if _, wantOpt := inf.Type.(*types.Optional); wantOpt {
						return lv.clone(), okSig()
					}
				}
				return lv.optElem.clone(), okSig()
			}
			return s.evalExpr(b.Right)
		}
		if lv.kind == ctEnum && types.AsResult(lv.ty) != nil {
			if lv.variantIdx == 0 {
				return lv.payload.clone(), okSig()
			}
			return s.evalExpr(b.Right)
		}
		return nil, abortSig(b.Span, "invalid operand to `??` at compile time")
	}

	lv, sig := s.evalExpr(b.Left)
	if sig.kind != sigNone {
		return nil, sig
	}
	rv, sig := s.evalExpr(b.Right)
	if sig.kind != sigNone {
		return nil, sig
	}

	// Overloaded operator method (`a + b` calling `a.add(b)`)
	if inf != nil && inf.OpMethod != nil {
		return s.callFuncInstance(inf.OpMethod, valArgs(lv, rv), b.Span)
	}

	switch b.Op {
	case token.Eq, token.Neq:
		isNoneCmp := false
		if _, rNone := b.Right.(*ast.NoneLit); rNone {
			isNoneCmp = true
		} else if _, lNone := b.Left.(*ast.NoneLit); lNone {
			isNoneCmp = true
		}
		if !isNoneCmp {
			cmpTy := s.binaryLeftOrWrappedType(b, lv)
			if handled, eq, sig := s.evalMethodEq(cmpTy, lv, rv, b.Span); handled || sig.kind != sigNone {
				if sig.kind != sigNone {
					return nil, sig
				}
				if b.Op == token.Neq {
					eq = !eq
				}
				return newCtBool(eq), okSig()
			}
		}
		eq := lv.equals(s.a, rv)
		if b.Op == token.Neq {
			eq = !eq
		}
		return newCtBool(eq), okSig()
	case token.Lt, token.Lte, token.Gt, token.Gte:
		if lv.kind == ctFloat && (math.IsNaN(lv.f) || math.IsNaN(rv.f)) {
			return newCtBool(false), okSig()
		}
		lty := s.binaryLeftType(b, lv)
		if handled, c, sig := s.evalMethodCmp(lty, lv, rv, b.Span); handled || sig.kind != sigNone {
			if sig.kind != sigNone {
				return nil, sig
			}
			switch b.Op {
			case token.Lt:
				return newCtBool(c < 0), okSig()
			case token.Lte:
				return newCtBool(c <= 0), okSig()
			case token.Gt:
				return newCtBool(c > 0), okSig()
			case token.Gte:
				return newCtBool(c >= 0), okSig()
			}
		}
		c := lv.cmpVal(rv)
		switch b.Op {
		case token.Lt:
			return newCtBool(c < 0), okSig()
		case token.Lte:
			return newCtBool(c <= 0), okSig()
		case token.Gt:
			return newCtBool(c > 0), okSig()
		case token.Gte:
			return newCtBool(c >= 0), okSig()
		}
	}

	if lv.kind == ctFloat {
		return s.evalFloatBinary(b.Op, lv, rv, b.Span)
	}
	if lv.kind == ctInt {
		return s.evalIntBinary(b.Op, lv, rv, b.Span)
	}
	if lv.kind == ctString && b.Op == token.Plus {
		out := lv.s + rv.s
		if sig := s.allocMem(ctValMemCost+int64(len(out)), b.Span); sig.kind != sigNone {
			return nil, sig
		}
		return newCtString(out), okSig()
	}
	return nil, abortSig(b.Span, "unsupported binary operator `%s` at compile time", b.Op.String())
}

func (s *ctState) binaryLeftType(b *ast.BinaryExpr, lv *ctVal) types.Type {
	if lInf := s.infoFor(b.Left); lInf != nil && lInf.Type != nil {
		return lInf.Type
	}
	if lv != nil {
		return lv.ty
	}
	return nil
}

func (s *ctState) binaryLeftOrWrappedType(b *ast.BinaryExpr, lv *ctVal) types.Type {
	if lInf := s.infoFor(b.Left); lInf != nil && lInf.Type != nil {
		if lInf.WrapOptional {
			if rInf := s.infoFor(b.Right); rInf != nil && rInf.Type != nil {
				return rInf.Type
			}
		}
		return lInf.Type
	}
	if lv != nil {
		return lv.ty
	}
	return nil
}

func (s *ctState) evalMethodEq(ty types.Type, lv, rv *ctVal, span diag.Span) (bool, bool, ctSignal) {
	if ty == nil || s.a == nil || s.a.Prog == nil {
		return false, false, okSig()
	}
	if p, isPrim := ty.(*types.Primitive); isPrim && p.Kind != types.PrimString {
		return false, false, okSig()
	}
	if en, isEn := ty.(*types.Enum); isEn && !en.IsTagged && s.a.isCompBuiltinEnum(en) {
		return false, false, okSig()
	}
	if eqM := s.a.Prog.LookupConcreteMethod(ty, "eq"); eqM != nil {
		res, sig := s.callFuncInstance(eqM, valArgs(lv, rv), span)
		if sig.kind != sigNone {
			return true, false, sig
		}
		return true, res.b, okSig()
	}
	if cmpM := s.a.Prog.LookupConcreteMethod(ty, "cmp"); cmpM != nil {
		res, sig := s.callFuncInstance(cmpM, valArgs(lv, rv), span)
		if sig.kind != sigNone {
			return true, false, sig
		}
		return true, res.asInt64() == 0, okSig()
	}
	if d, isDist := ty.(*types.Distinct); isDist {
		return s.evalMethodEq(d.Base, lv, rv, span)
	}
	return false, false, okSig()
}

func (s *ctState) evalMethodCmp(ty types.Type, lv, rv *ctVal, span diag.Span) (bool, int64, ctSignal) {
	if ty == nil || s.a == nil || s.a.Prog == nil {
		return false, 0, okSig()
	}
	if p, isPrim := ty.(*types.Primitive); isPrim && p.Kind != types.PrimString {
		return false, 0, okSig()
	}
	if en, isEn := ty.(*types.Enum); isEn && !en.IsTagged {
		return false, 0, okSig()
	}
	if cmpM := s.a.Prog.LookupConcreteMethod(ty, "cmp"); cmpM != nil {
		res, sig := s.callFuncInstance(cmpM, valArgs(lv, rv), span)
		if sig.kind != sigNone {
			return true, 0, sig
		}
		return true, res.asInt64(), okSig()
	}
	if d, isDist := ty.(*types.Distinct); isDist {
		return s.evalMethodCmp(d.Base, lv, rv, span)
	}
	return false, 0, okSig()
}

func (s *ctState) evalFloatBinary(op token.Kind, lv, rv *ctVal, span diag.Span) (*ctVal, ctSignal) {
	if lv.prim != nil && lv.prim.Kind == types.PrimF32 {
		lf, rf := float32(lv.f), float32(rv.f)
		switch op {
		case token.Plus:
			return newCtFloat(float64(lf+rf), lv.prim), okSig()
		case token.Minus:
			return newCtFloat(float64(lf-rf), lv.prim), okSig()
		case token.Star:
			return newCtFloat(float64(lf*rf), lv.prim), okSig()
		case token.Slash:
			return newCtFloat(float64(lf/rf), lv.prim), okSig()
		case token.Percent:
			return newCtFloat(float64(float32(math.Mod(float64(lf), float64(rf)))), lv.prim), okSig()
		}
	} else {
		switch op {
		case token.Plus:
			return newCtFloat(lv.f+rv.f, lv.prim), okSig()
		case token.Minus:
			return newCtFloat(lv.f-rv.f, lv.prim), okSig()
		case token.Star:
			return newCtFloat(lv.f*rv.f, lv.prim), okSig()
		case token.Slash:
			return newCtFloat(lv.f/rv.f, lv.prim), okSig()
		case token.Percent:
			return newCtFloat(math.Mod(lv.f, rv.f), lv.prim), okSig()
		}
	}
	return nil, abortSig(span, "unsupported float operator `%s`", op.String())
}

func (s *ctState) evalIntBinary(op token.Kind, lv, rv *ctVal, span diag.Span) (*ctVal, ctSignal) {
	p := lv.prim
	bits := primBitWidth(p)
	signed := p == nil || p.IsSignedInt()
	switch op {
	case token.Plus:
		return newCtInt(lv.u+rv.u, p), okSig()
	case token.Minus:
		return newCtInt(lv.u-rv.u, p), okSig()
	case token.Star:
		return newCtInt(lv.u*rv.u, p), okSig()
	case token.Amp:
		return newCtInt(lv.u&rv.u, p), okSig()
	case token.Pipe:
		return newCtInt(lv.u|rv.u, p), okSig()
	case token.Caret:
		return newCtInt(lv.u^rv.u, p), okSig()
	case token.Slash:
		if signed {
			l, r := lv.asInt64(), rv.asInt64()
			if r == 0 {
				return nil, abortSig(span, "compile-time integer division by zero")
			}
			minVal := -(int64(1) << uint(bits-1))
			if bits == 64 {
				minVal = math.MinInt64
			}
			if l == minVal && r == -1 {
				return nil, abortSig(span, "compile-time signed integer division overflow (%d / -1)", l)
			}
			return newCtSignedInt(l/r, p), okSig()
		}
		if rv.asUint64() == 0 {
			return nil, abortSig(span, "compile-time unsigned integer division by zero")
		}
		return newCtInt(lv.asUint64()/rv.asUint64(), p), okSig()

	case token.Percent:
		if signed {
			l, r := lv.asInt64(), rv.asInt64()
			if r == 0 {
				return nil, abortSig(span, "compile-time integer modulo by zero")
			}
			minVal := -(int64(1) << uint(bits-1))
			if bits == 64 {
				minVal = math.MinInt64
			}
			if l == minVal && r == -1 {
				return nil, abortSig(span, "compile-time signed integer modulo overflow (%d %% -1)", l)
			}
			return newCtSignedInt(l%r, p), okSig()
		}
		if rv.asUint64() == 0 {
			return nil, abortSig(span, "compile-time unsigned integer modulo by zero")
		}
		return newCtInt(lv.asUint64()%rv.asUint64(), p), okSig()

	case token.Shl:
		var shift int64
		if rv.prim == nil || rv.prim.IsSignedInt() {
			shift = rv.asInt64()
		} else {
			u := rv.asUint64()
			if u >= uint64(bits) {
				return nil, abortSig(span, "compile-time left shift amount %d out of range [0, %d)", u, bits)
			}
			shift = int64(u)
		}
		if shift < 0 || shift >= int64(bits) {
			return nil, abortSig(span, "compile-time left shift amount %d out of range [0, %d)", shift, bits)
		}
		return newCtInt(lv.asUint64()<<uint(shift), p), okSig()

	case token.Shr:
		var shift int64
		if rv.prim == nil || rv.prim.IsSignedInt() {
			shift = rv.asInt64()
		} else {
			u := rv.asUint64()
			if u >= uint64(bits) {
				return nil, abortSig(span, "compile-time right shift amount %d out of range [0, %d)", u, bits)
			}
			shift = int64(u)
		}
		if shift < 0 || shift >= int64(bits) {
			return nil, abortSig(span, "compile-time right shift amount %d out of range [0, %d)", shift, bits)
		}
		if signed {
			return newCtSignedInt(lv.asInt64()>>uint(shift), p), okSig()
		}
		return newCtInt(lv.asUint64()>>uint(shift), p), okSig()
	}
	return nil, abortSig(span, "unsupported integer operator `%s`", op.String())
}

// -----------------------------------------------------------------------------
// Blocks, Statements, & LIFO Drop
// -----------------------------------------------------------------------------

func (s *ctState) pushDropScope() {
	s.dropScopes = append(s.dropScopes, nil)
}

func (s *ctState) registerDropLocal(sym *Symbol, slot *ctSlot, ty types.Type) {
	if sym == nil || slot == nil || ty == nil || ty.IsPlain() || sym.BindKind != BindOwned || len(s.dropScopes) == 0 {
		return
	}
	idx := len(s.dropScopes) - 1
	s.dropScopes[idx] = append(s.dropScopes[idx], dropEntry{sym: sym, slot: slot, ty: ty})
}

func (s *ctState) popDropScope(span diag.Span) ctSignal {
	if len(s.dropScopes) == 0 {
		return okSig()
	}
	entries := s.dropScopes[len(s.dropScopes)-1]
	s.dropScopes = s.dropScopes[:len(s.dropScopes)-1]
	for i := len(entries) - 1; i >= 0; i-- {
		ent := entries[i]
		if ent.slot == nil || ent.slot.refPlace != nil || ent.slot.moved || ent.slot.val == nil {
			continue
		}
		if sig := s.dropValue(ent.ty, ent.slot.val, span); sig.kind != sigNone {
			return sig
		}
	}
	return okSig()
}

func (s *ctState) evalBlock(blk *ast.BlockExpr) (*ctVal, ctSignal) {
	s.pushDropScope()
	var res *ctVal = newCtVoid()
	var sig ctSignal

	for _, st := range blk.Stmts {
		_, sig = s.evalStmt(st)
		if sig.kind != sigNone {
			break
		}
	}
	if sig.kind == sigNone && blk.Tail != nil {
		res, sig = s.evalExpr(blk.Tail)
	}

	dropSig := s.popDropScope(blk.Span)
	if sig.kind == sigAbort {
		return nil, sig
	}
	if dropSig.kind == sigAbort {
		return nil, dropSig
	}

	if sig.kind == sigUnwind {
		inf := s.infoFor(blk)
		if inf != nil && sig.targetID == inf.TargetID {
			if sig.val != nil {
				return sig.val, okSig()
			}
			return newCtVoid(), okSig()
		}
		return nil, sig
	}
	return res, okSig()
}

func (s *ctState) evalStmt(st ast.Stmt) (*ctVal, ctSignal) {
	if st == nil {
		return newCtVoid(), okSig()
	}
	if sig := s.enterGoDepth(st.GetSpan()); sig.kind != sigNone {
		return nil, sig
	}
	defer s.leaveGoDepth()
	if sig := s.step(st.GetSpan()); sig.kind != sigNone {
		return nil, sig
	}
	switch v := st.(type) {
	case *ast.VarStmt:
		return s.evalVarStmt(v)
	case *ast.AssignStmt:
		return s.evalAssignStmt(v)
	case *ast.UnwindStmt:
		uwi := s.unwindInfoFor(v)
		var val *ctVal
		if v.Value != nil {
			var sig ctSignal
			val, sig = s.evalExpr(v.Value)
			if sig.kind != sigNone {
				return nil, sig
			}
			val = val.clone()
		} else {
			val = newCtVoid()
		}
		if uwi == nil {
			return nil, abortSig(v.Span, "unresolved unwind target at compile time")
		}
		return nil, ctSignal{
			kind:       sigUnwind,
			targetKind: uwi.TargetKind,
			targetID:   uwi.TargetID,
			isContinue: uwi.IsContinue,
			isDefault:  uwi.IsStructFieldDefault,
			val:        val,
		}
	case *ast.DeclStmt:
		return newCtVoid(), okSig()
	case *ast.ExprStmt:
		val, sig := s.evalExpr(v.Expr)
		if sig.kind != sigNone {
			return nil, sig
		}
		if val != nil && !s.isCtLvalueExpr(v.Expr) && !s.sharesLiveSlotAlloc(val) {
			if dsig := s.dropValue(val.ty, val, v.Span); dsig.kind != sigNone {
				return nil, dsig
			}
		}
		return newCtVoid(), okSig()
	}
	return newCtVoid(), okSig()
}

func (s *ctState) evalVarStmt(vs *ast.VarStmt) (*ctVal, ctSignal) {
	fr := s.frame()
	val, sig := s.evalExpr(vs.Value)
	if sig.kind != sigNone {
		return nil, sig
	}

	if vs.FallbackUnwind != nil {
		var ok bool
		val, ok = unwrapFallbackOrUnwind(val, vs.FallbackUnwind, s)
		if !ok {
			if val != nil && val.kind == ctEnum && types.AsResult(val.ty) != nil {
				// Result failure triggers FallbackUnwind
			}
			_, uwSig := s.evalStmt(vs.FallbackUnwind)
			return nil, uwSig
		}
	}

	// Track move out of simple identifier RHS for Resource Drop tracking.
	if id, isId := vs.Value.(*ast.IdentExpr); isId {
		if srcInf := s.infoFor(id); srcInf != nil && srcInf.MovedLocal != nil {
			if srcSlot, ok := s.lookupSlot(srcInf.MovedLocal); ok {
				srcSlot.moved = true
			}
		}
	}

	if vs.IsUnpack {
		syms := s.unpackSymsFor(vs)
		if val.kind != ctTuple || val.tup == nil {
			return nil, abortSig(vs.Span, "cannot unpack non-tuple value at compile time")
		}
		for i, sym := range syms {
			if i >= len(val.tup.elems) {
				continue
			}
			el := val.tup.elems[i].clone()
			if sym == nil {
				if el != nil {
					if sig := s.dropValue(el.ty, el, vs.Span); sig.kind != sigNone {
						return nil, sig
					}
				}
				continue
			}
			el = el.wrapResult(sym.Type)
			if vs.IsConst {
				freezeCtVal(el)
			}
			sl := &ctSlot{val: el, ty: sym.Type}
			if fr != nil {
				fr.locals[sym] = sl
			}
			s.registerDropLocal(sym, sl, sym.Type)
			if vs.IsConst && fr != nil && fr.bm != nil {
				fr.bm.ConstVarVals[sym] = el.clone()
			}
		}
		return newCtVoid(), okSig()
	}

	sym := s.varSymFor(vs)
	if sym == nil {
		if vs.Name == "_" && val != nil {
			if sig := s.dropValue(val.ty, val, vs.Span); sig.kind != sigNone {
				return nil, sig
			}
			return newCtVoid(), okSig()
		}
		return nil, abortSig(vs.Span, "missing variable symbol for %q", vs.Name)
	}
	stored := val.clone().wrapResult(sym.Type)
	if vs.IsConst {
		freezeCtVal(stored)
	}
	sl := &ctSlot{val: stored, ty: sym.Type}
	if fr != nil {
		fr.locals[sym] = sl
	}
	s.registerDropLocal(sym, sl, sym.Type)
	if vs.IsConst && fr != nil && fr.bm != nil {
		fr.bm.ConstVarVals[sym] = stored.clone()
	}
	return newCtVoid(), okSig()
}

func unwrapFallbackOrUnwind(val *ctVal, _ *ast.UnwindStmt, _ *ctState) (*ctVal, bool) {
	if val == nil {
		return nil, false
	}
	if val.kind == ctOpt {
		if !val.hasOpt {
			return nil, false
		}
		return val.optElem, true
	}
	if val.kind == ctEnum && types.AsResult(val.ty) != nil {
		if val.variantIdx == 0 {
			return val.payload, true
		}
		return nil, false
	}
	return val, true
}

func (s *ctState) evalAssignStmt(as *ast.AssignStmt) (*ctVal, ctSignal) {
	if as.IsUnpack {
		val, sig := s.evalExpr(as.Value)
		if sig.kind != sigNone {
			return nil, sig
		}
		if as.FallbackUnwind != nil {
			var ok bool
			val, ok = unwrapFallbackOrUnwind(val, as.FallbackUnwind, s)
			if !ok {
				_, uwSig := s.evalStmt(as.FallbackUnwind)
				return nil, uwSig
			}
		}
		if val.kind != ctTuple || val.tup == nil {
			return nil, abortSig(as.Span, "cannot unpack non-tuple value at compile time")
		}
		for i, tgt := range as.UnpackTargets {
			if id, isId := tgt.(*ast.IdentExpr); isId && id.Name == "_" {
				continue
			}
			pl, sig := s.evalPlace(tgt)
			if sig.kind != sigNone {
				return nil, sig
			}
			if i < len(val.tup.elems) {
				elem := val.tup.elems[i]
				if sig := s.allocCloneMem(elem, as.Span); sig.kind != sigNone {
					return nil, sig
				}
				if sig := pl.set(elem.clone()); sig.kind != sigNone {
					return nil, sig
				}
			}
		}
		return newCtVoid(), okSig()
	}

	pl, sig := s.evalPlace(as.Target)
	if sig.kind != sigNone {
		return nil, sig
	}

	if as.Op == token.Assign {
		val, sig := s.evalExpr(as.Value)
		if sig.kind != sigNone {
			return nil, sig
		}
		if as.FallbackUnwind != nil {
			var ok bool
			val, ok = unwrapFallbackOrUnwind(val, as.FallbackUnwind, s)
			if !ok {
				_, uwSig := s.evalStmt(as.FallbackUnwind)
				return nil, uwSig
			}
		}
		if sig := s.allocCloneMem(val, as.Span); sig.kind != sigNone {
			return nil, sig
		}
		if id, isId := as.Target.(*ast.IdentExpr); isId {
			if inf := s.infoFor(id); inf != nil && inf.Sym != nil {
				if sl, ok := s.lookupSlot(inf.Sym); ok && sl.refPlace == nil {
					if !sl.moved && sl.val != nil && sl.ty != nil && !sl.ty.IsPlain() {
						if dsig := s.dropValue(sl.ty, sl.val, as.Span); dsig.kind != sigNone {
							return nil, dsig
						}
					}
					sl.moved = false
				}
			}
		} else if tgtInf := s.infoFor(as.Target); tgtInf != nil && tgtInf.Type != nil && !tgtInf.Type.IsPlain() {
			// Safe non-pointer field overwrite drops old value first.
			if me, isMem := as.Target.(*ast.MemberExpr); isMem {
				baseInf := s.infoFor(me.Base)
				_, isPtr := baseInf.Type.(*types.Ptr)
				if baseInf == nil || !isPtr {
					if oldV, gsig := pl.get(); gsig.kind == sigNone && oldV != nil {
						if dsig := s.dropValue(tgtInf.Type, oldV, as.Span); dsig.kind != sigNone {
							return nil, dsig
						}
					}
				}
			}
		}
		return newCtVoid(), pl.set(val.clone())
	}

	// Compound assignment
	cur, sig := pl.get()
	if sig.kind != sigNone {
		return nil, sig
	}
	rhs, sig := s.evalExpr(as.Value)
	if sig.kind != sigNone {
		return nil, sig
	}

	tgtInf := s.infoFor(as.Target)
	if tgtInf != nil && tgtInf.OpMethod != nil {
		isMutFirst := tgtInf.OpMethod.Sig != nil && len(tgtInf.OpMethod.Sig.Params) > 0 && tgtInf.OpMethod.Sig.Params[0].Mode == types.ModeMut
		if isMutFirst || tgtInf.OpMethod.Name == "add_assign" || strings.HasSuffix(tgtInf.OpMethod.Name, ".add_assign") {
			_, sig := s.callFuncInstance(tgtInf.OpMethod, []ctArg{{place: &pl}, {val: rhs}}, as.Span)
			return newCtVoid(), sig
		}
		res, sig := s.callFuncInstance(tgtInf.OpMethod, valArgs(cur, rhs), as.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return newCtVoid(), pl.set(res.clone())
	}

	var binOp token.Kind
	switch as.Op {
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
		return nil, abortSig(as.Span, "unsupported compound assignment %q", as.Op.String())
	}

	var nextVal *ctVal
	if cur.kind == ctFloat {
		nextVal, sig = s.evalFloatBinary(binOp, cur, rhs, as.Span)
	} else if cur.kind == ctInt {
		nextVal, sig = s.evalIntBinary(binOp, cur, rhs, as.Span)
	} else if cur.kind == ctString && binOp == token.Plus {
		out := cur.s + rhs.s
		sig = s.allocMem(ctValMemCost+int64(len(out)), as.Span)
		nextVal = newCtString(out)
	} else {
		return nil, abortSig(as.Span, "unsupported compound assignment on %v", cur.ty)
	}
	if sig.kind != sigNone {
		return nil, sig
	}
	return newCtVoid(), pl.set(nextVal)
}

// -----------------------------------------------------------------------------
// Control Flow: `if`, `for`, `while`, `match`
// -----------------------------------------------------------------------------

func (s *ctState) evalIf(ie *ast.IfExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	fr := s.frame()
	if fr != nil && fr.bm != nil {
		if branch, ok := fr.bm.ComptimeIfBranch[ie]; ok {
			var res *ctVal = newCtVoid()
			var sig ctSignal
			switch branch {
			case 0:
				s.pushDropScope()
				res, sig = s.evalExpr(ie.Then)
				if dsig := s.popDropScope(ie.Then.GetSpan()); dsig.kind == sigAbort {
					return nil, dsig
				}
			case 2:
				if ie.Else != nil {
					s.pushDropScope()
					res, sig = s.evalExpr(ie.Else)
					if dsig := s.popDropScope(ie.Else.GetSpan()); dsig.kind == sigAbort {
						return nil, dsig
					}
				}
			case 3:
				return newCtVoid(), okSig()
			}
			if sig.kind == sigUnwind && inf != nil && sig.targetID == inf.TargetID {
				return sig.val, okSig()
			}
			if sig.kind != sigNone {
				return nil, sig
			}
			return res, okSig()
		}
	}

	takeThen := false
	var unwrapSym *Symbol
	var originSlot *ctSlot
	var boundVal *ctVal
	var variantRefPlace *ctPlace

	if fr != nil && fr.bm != nil {
		unwrapSym = fr.bm.IfUnwrapSyms[ie]
	}

	switch ie.CondKind {
	case ast.IfCondOptVar:
		if fr != nil && fr.bm != nil {
			origSym := fr.bm.IfOriginSyms[ie]
			if origSym != nil {
				originSlot, _ = s.lookupSlot(origSym)
			}
		}
		var condVal *ctVal
		if originSlot != nil {
			condVal = originSlot.val
		} else {
			sym := s.a.lookupTopSymbol(s.curMod(), "", ie.OptVar)
			if sym != nil && sym.ConstVal != nil {
				condVal, _ = sym.ConstVal.(*ctVal)
			}
		}
		if condVal != nil && condVal.kind == ctOpt && condVal.hasOpt {
			takeThen = true
			boundVal = condVal.optElem
		}

	case ast.IfCondOptBind:
		var condCall *ast.CallExpr
		ce := ie.Cond
		for ce != nil {
			if ue, ok := ce.(*ast.UnsafeExpr); ok {
				ce = ue.Expr
				continue
			}
			if cx, ok := ce.(*ast.ComptimeExpr); ok {
				ce = cx.Expr
				continue
			}
			if c, ok := ce.(*ast.CallExpr); ok {
				condCall = c
			}
			break
		}
		if condCall != nil {
			if cInf := s.infoFor(condCall); cInf != nil && !cInf.IsOptChain && cInf.BuiltinName == "enum.as_variant" {
				if ie.MutUnwrap {
					enumPlace, sig := s.evalDerefBasePlace(cInf.MethodRecv, ie.Cond.GetSpan())
					if sig.kind != sigNone {
						return nil, sig
					}
					bv, sig := enumPlace.get()
					if sig.kind != sigNone {
						return nil, sig
					}
					targetPlace := enumPlace
					for bv != nil && bv.kind == ctPointer {
						if bv.ptrVal == nil {
							return nil, abortSig(ie.Cond.GetSpan(), "cannot dereference nil reference in `as_variant`")
						}
						dp, s2 := s.ptrDerefPlace(bv.ptrVal, ie.Cond.GetSpan())
						if s2.kind != sigNone {
							return nil, s2
						}
						targetPlace = dp
						bv, sig = targetPlace.get()
						if sig.kind != sigNone {
							return nil, sig
						}
					}
					if bv != nil && bv.kind == ctEnum && bv.variantIdx == cInf.TupleFieldIdx {
						takeThen = true
						boundVal = bv.payload
						vIdx := cInf.TupleFieldIdx
						vr := cInf.EnumVariant
						rp := ctPlace{
							get: func() (*ctVal, ctSignal) {
								cur, s1 := targetPlace.get()
								if s1.kind != sigNone {
									return nil, s1
								}
								return s.readEnumPayloadCtVal(cur, vIdx, vr, ie.Then.GetSpan())
							},
							set: func(nv *ctVal) ctSignal {
								cur, s1 := targetPlace.get()
								if s1.kind != sigNone {
									return s1
								}
								if cur == nil || cur.kind != ctEnum || cur.variantIdx != vIdx {
									return abortSig(ie.Then.GetSpan(), "cannot assign payload of mismatched enum variant")
								}
								cur.payload = nv
								return targetPlace.set(cur)
							},
						}
						variantRefPlace = &rp
					}
				} else {
					bv, sig := s.evalExpr(cInf.MethodRecv)
					if sig.kind != sigNone {
						return nil, sig
					}
					bv, sig = s.derefCtVal(bv, ie.Cond.GetSpan())
					if sig.kind != sigNone {
						return nil, sig
					}
					if bv != nil && bv.kind == ctEnum && bv.variantIdx == cInf.TupleFieldIdx {
						takeThen = true
						boundVal = bv.payload.clone()
					}
				}
				break
			}
		}
		if ie.MutUnwrap {
			pl, sig := s.evalPlace(ie.Cond)
			if sig.kind != sigNone {
				return nil, sig
			}
			condVal, sig := pl.get()
			if sig.kind != sigNone {
				return nil, sig
			}
			if condVal != nil && condVal.kind == ctOpt && condVal.hasOpt {
				takeThen = true
				boundVal = condVal.optElem
			} else if condVal != nil && condVal.kind == ctEnum && types.AsResult(condVal.ty) != nil && condVal.variantIdx == 0 {
				takeThen = true
				boundVal = condVal.payload
			}
		} else {
			condVal, sig := s.evalExpr(ie.Cond)
			if sig.kind != sigNone {
				return nil, sig
			}
			if condVal != nil && condVal.kind == ctOpt && condVal.hasOpt {
				takeThen = true
				boundVal = condVal.optElem.clone()
			} else if condVal != nil && condVal.kind == ctEnum && types.AsResult(condVal.ty) != nil && condVal.variantIdx == 0 {
				takeThen = true
				boundVal = condVal.payload.clone()
			}
		}

	default:
		condVal, sig := s.evalExpr(ie.Cond)
		if sig.kind != sigNone {
			return nil, sig
		}
		takeThen = condVal != nil && condVal.b
	}

	var res *ctVal = newCtVoid()
	var sig ctSignal

	if takeThen {
		s.pushDropScope()
		if unwrapSym != nil && fr != nil {
			if variantRefPlace != nil {
				fr.locals[unwrapSym] = &ctSlot{ty: unwrapSym.Type, refPlace: variantRefPlace}
			} else if ie.MutUnwrap && originSlot != nil {
				// Share element value directly for `&x` mut unwrap, sync back on pop if scalar.
				fr.locals[unwrapSym] = &ctSlot{val: boundVal, ty: unwrapSym.Type}
			} else {
				fr.locals[unwrapSym] = &ctSlot{val: boundVal, ty: unwrapSym.Type}
			}
		}
		res, sig = s.evalExpr(ie.Then)
		if unwrapSym != nil && ie.MutUnwrap && variantRefPlace == nil && fr != nil {
			if sl := fr.locals[unwrapSym]; sl != nil && sl.val != nil && originSlot != nil && originSlot.val != nil {
				if originSlot.val.kind == ctOpt {
					originSlot.val.optElem = sl.val
				}
			}
		}
		if dsig := s.popDropScope(ie.Then.GetSpan()); dsig.kind == sigAbort {
			return nil, dsig
		}
	} else if ie.Else != nil {
		res, sig = s.evalExpr(ie.Else)
	}

	if sig.kind == sigUnwind && inf != nil && sig.targetID == inf.TargetID {
		return sig.val, okSig()
	}
	if sig.kind != sigNone {
		return nil, sig
	}
	return res, okSig()
}

type loopEval struct {
	expr       ast.Expr
	kind       ast.ForKind
	condOrIter ast.Expr
	rangeEnd   ast.Expr
	inclusive  bool
	var1Mut    bool
	var2Mut    bool
	evalType   types.Type
	body       ast.Expr
	elseExpr   ast.Expr
	span       diag.Span
	isFor      bool
}

func (s *ctState) evalFixedCollectUnrolledFor(le loopEval, uf *UnrolledFor, targetID int, fr *ctFrame) (*ctVal, ctSignal) {
	slotCount := len(uf.Iters)
	if uf.CollectStruct != nil {
		slotCount = len(uf.CollectStruct.Fields)
	} else if uf.CollectTuple != nil {
		slotCount = len(uf.CollectTuple.Elems)
	} else if uf.CollectArray != nil {
		slotCount = int(uf.CollectArray.Size)
	}
	fixedVals := make([]*ctVal, slotCount)
	fixedFilled := make([]bool, slotCount)
	var filledOrder []int

	dropPopulated := func(span diag.Span) ctSignal {
		for i := len(filledOrder) - 1; i >= 0; i-- {
			idx := filledOrder[i]
			if !fixedFilled[idx] || fixedVals[idx] == nil {
				continue
			}
			var elemTy types.Type
			switch {
			case uf.CollectStruct != nil && idx < len(uf.CollectStruct.Fields):
				elemTy = uf.CollectStruct.Fields[idx].Type
			case uf.CollectTuple != nil && idx < len(uf.CollectTuple.Elems):
				elemTy = uf.CollectTuple.Elems[idx]
			case uf.CollectArray != nil:
				elemTy = uf.CollectArray.Elem
			}
			if dsig := s.dropValue(elemTy, fixedVals[idx], span); dsig.kind != sigNone {
				return dsig
			}
			fixedFilled[idx] = false
			fixedVals[idx] = nil
		}
		return okSig()
	}

	for _, it := range uf.Iters {
		if sig := s.step(le.span); sig.kind != sigNone {
			_ = dropPopulated(le.span)
			return nil, sig
		}
		if it.IdxSym != nil && it.IdxVal != nil {
			fr.locals[it.IdxSym] = &ctSlot{val: it.IdxVal.clone(), ty: it.IdxSym.Type}
		}
		if it.ItemSym != nil && it.ItemVal != nil {
			fr.locals[it.ItemSym] = &ctSlot{val: it.ItemVal.clone(), ty: it.ItemSym.Type}
		}
		s.pushDropScope()
		val, bsig := s.evalExpr(it.Body)
		dsig := s.popDropScope(it.Body.GetSpan())
		if bsig.kind == sigAbort {
			_ = dropPopulated(it.Body.GetSpan())
			return nil, bsig
		}
		if dsig.kind == sigAbort {
			_ = dropPopulated(it.Body.GetSpan())
			return nil, dsig
		}
		if bsig.kind == sigUnwind {
			if bsig.targetID == targetID {
				if bsig.isContinue {
					var storeVal *ctVal
					if bsig.isDefault && uf.CollectStruct != nil {
						dv, defSig := s.evalStructDefaultOrNone(uf.CollectStruct, it.TargetIdx)
						if defSig.kind != sigNone {
							_ = dropPopulated(it.Body.GetSpan())
							return nil, defSig
						}
						storeVal = dv
					} else if bsig.val != nil {
						storeVal = bsig.val.clone()
					}
					if it.TargetIdx >= 0 && it.TargetIdx < len(fixedVals) {
						fixedVals[it.TargetIdx] = storeVal
						fixedFilled[it.TargetIdx] = true
						filledOrder = append(filledOrder, it.TargetIdx)
					}
					continue
				}
				if ds := dropPopulated(it.Body.GetSpan()); ds.kind != sigNone {
					return nil, ds
				}
				if bsig.val != nil {
					return bsig.val, okSig()
				}
				return newCtVoid(), okSig()
			}
			_ = dropPopulated(it.Body.GetSpan())
			return nil, bsig
		}
		if dsig.kind == sigUnwind {
			_ = dropPopulated(it.Body.GetSpan())
			return nil, dsig
		}
		if it.TargetIdx >= 0 && it.TargetIdx < len(fixedVals) && val != nil {
			fixedVals[it.TargetIdx] = val.clone()
			fixedFilled[it.TargetIdx] = true
			filledOrder = append(filledOrder, it.TargetIdx)
		}
	}

	switch {
	case uf.CollectStruct != nil:
		st := uf.CollectStruct
		fields := make(map[string]*ctVal, len(st.Fields))
		order := make([]string, len(st.Fields))
		for i, sf := range st.Fields {
			order[i] = sf.Name
			if i < len(fixedVals) && fixedFilled[i] && fixedVals[i] != nil {
				fields[sf.Name] = fixedVals[i]
			} else {
				dv, defSig := s.evalStructDefaultOrNone(st, i)
				if defSig.kind != sigNone {
					_ = dropPopulated(le.span)
					return nil, defSig
				}
				fields[sf.Name] = dv
			}
		}
		if sig := s.allocMem(int64(len(order))*24, le.span); sig.kind != sigNone {
			_ = dropPopulated(le.span)
			return nil, sig
		}
		resTy := le.evalType
		if resTy == nil {
			resTy = st
		}
		return &ctVal{
			kind: ctStruct,
			ty:   resTy,
			st:   &ctStructBox{ty: st, fields: fields, fieldOrder: order},
		}, okSig()
	case uf.CollectTuple != nil:
		resTy := le.evalType
		if resTy == nil {
			resTy = uf.CollectTuple
		}
		return &ctVal{kind: ctTuple, ty: resTy, tup: &ctTupleBox{elems: fixedVals}}, okSig()
	case uf.CollectArray != nil:
		resTy := le.evalType
		if resTy == nil {
			resTy = uf.CollectArray
		}
		return &ctVal{kind: ctArray, ty: resTy, arr: &ctArrayBox{elems: fixedVals, elemTy: uf.CollectArray.Elem}}, okSig()
	}
	return newCtVoid(), okSig()
}

func (s *ctState) evalFor(le loopEval, inf *ExprInfo) (*ctVal, ctSignal) {
	fr := s.frame()
	var syms [2]*Symbol
	if fr != nil && fr.bm != nil {
		if fe, ok := le.expr.(*ast.ForExpr); ok {
			syms = fr.bm.ForSymbols[fe]
		} else if we, ok := le.expr.(*ast.WhileExpr); ok {
			syms = fr.bm.WhileSymbols[we]
		}
	}

	targetID := 0
	if inf != nil {
		targetID = inf.TargetID
	}

	var collected []*ctVal
	var collectMap *ctMapBox
	var collectSet *ctSetBox
	collecting := le.evalType != nil && !le.evalType.Equals(types.TypeVoid) && !le.evalType.Equals(types.TypeNever) && le.isFor
	if collecting {
		switch types.CollView(le.evalType).(type) {
		case *types.Map:
			collectMap = &ctMapBox{}
		case *types.Set:
			collectSet = &ctSetBox{}
		}
	}

	appendCollected := func(v *ctVal) {
		if !collecting || v == nil || v.kind == ctVoid {
			return
		}
		if collectMap != nil {
			var k, mv *ctVal
			if v.kind == ctTuple && v.tup != nil && len(v.tup.elems) == 2 {
				k, mv = v.tup.elems[0].clone(), v.tup.elems[1].clone()
			} else if v.kind == ctStruct && v.st != nil {
				if kf, ok := v.st.fields["key"]; ok {
					if vf, ok2 := v.st.fields["value"]; ok2 {
						k, mv = kf.clone(), vf.clone()
					}
				}
			}
			if k != nil && mv != nil {
				for i := range collectMap.entries {
					if collectMap.entries[i].key.equals(s.a, k) {
						collectMap.entries[i].val = mv
						return
					}
				}
				collectMap.entries = append(collectMap.entries, ctMapEntry{key: k, val: mv})
				return
			}
		}
		if collectSet != nil {
			for _, ex := range collectSet.elems {
				if ex.equals(s.a, v) {
					return
				}
			}
			collectSet.elems = append(collectSet.elems, v.clone())
			return
		}
		collected = append(collected, v.clone())
	}

	finishCollected := func() (*ctVal, ctSignal) {
		if !collecting {
			return newCtVoid(), okSig()
		}
		if collectMap != nil {
			return s.makeMapStruct(le.evalType, collectMap.entries, le.span)
		}
		if collectSet != nil {
			return s.makeSetStruct(le.evalType, collectSet.elems, le.span)
		}
		if _, isArr := le.evalType.(*types.StackArray); isArr {
			return &ctVal{kind: ctArray, ty: le.evalType, arr: &ctArrayBox{elems: collected}}, okSig()
		}
		return s.makeListStruct(le.evalType, collected, le.span)
	}

	runBody := func() (broken bool, retVal *ctVal, sig ctSignal) {
		s.pushDropScope()
		val, bsig := s.evalExpr(le.body)
		dsig := s.popDropScope(le.body.GetSpan())
		if bsig.kind == sigAbort {
			return false, nil, bsig
		}
		if dsig.kind == sigAbort {
			return false, nil, dsig
		}
		if bsig.kind == sigUnwind {
			if bsig.targetID == targetID {
				if bsig.isContinue {
					return false, nil, okSig()
				}
				return true, bsig.val, okSig()
			}
			return false, nil, bsig
		}
		if dsig.kind == sigUnwind {
			return false, nil, dsig
		}
		appendCollected(val)
		return false, nil, okSig()
	}

	broken := false
	var breakVal *ctVal

	if fe, ok := le.expr.(*ast.ForExpr); ok && fr != nil && fr.bm != nil {
		if uf, hasUF := fr.bm.ComptimeForUnrolls[fe]; hasUF && uf != nil {
			if uf.CollectStruct != nil || uf.CollectTuple != nil || uf.CollectArray != nil {
				return s.evalFixedCollectUnrolledFor(le, uf, targetID, fr)
			}
			for _, it := range uf.Iters {
				if sig := s.step(le.span); sig.kind != sigNone {
					return nil, sig
				}
				if it.IdxSym != nil && it.IdxVal != nil {
					fr.locals[it.IdxSym] = &ctSlot{val: it.IdxVal.clone(), ty: it.IdxSym.Type}
				}
				if it.ItemSym != nil && it.ItemVal != nil {
					fr.locals[it.ItemSym] = &ctSlot{val: it.ItemVal.clone(), ty: it.ItemSym.Type}
				}
				s.pushDropScope()
				val, bsig := s.evalExpr(it.Body)
				dsig := s.popDropScope(it.Body.GetSpan())
				if bsig.kind == sigAbort {
					return nil, bsig
				}
				if dsig.kind == sigAbort {
					return nil, dsig
				}
				if bsig.kind == sigUnwind {
					if bsig.targetID == targetID {
						if bsig.isContinue {
							continue
						}
						broken = true
						breakVal = bsig.val
						break
					}
					return nil, bsig
				}
				if dsig.kind == sigUnwind {
					return nil, dsig
				}
				appendCollected(val)
			}
			if collecting {
				if broken && breakVal != nil {
					return breakVal, okSig()
				}
				isEmpty := len(collected) == 0 && (collectMap == nil || len(collectMap.entries) == 0) && (collectSet == nil || len(collectSet.elems) == 0)
				if isEmpty && le.elseExpr != nil {
					switch types.CollView(le.evalType).(type) {
					case *types.Vector, *types.Set, *types.Map:
						return s.evalExpr(le.elseExpr)
					}
				}
				return finishCollected()
			}
			if broken {
				if breakVal != nil {
					return breakVal, okSig()
				}
				return newCtVoid(), okSig()
			}
			if le.elseExpr != nil {
				return s.evalExpr(le.elseExpr)
			}
			return newCtVoid(), okSig()
		}
	}

	switch le.kind {
	case ast.ForCond:
		for {
			if sig := s.step(le.span); sig.kind != sigNone {
				return nil, sig
			}
			cv, sig := s.evalExpr(le.condOrIter)
			if sig.kind != sigNone {
				return nil, sig
			}
			if !cv.b {
				break
			}
			br, bv, bsig := runBody()
			if bsig.kind != sigNone {
				return nil, bsig
			}
			if br {
				broken = true
				breakVal = bv
				break
			}
		}

	case ast.ForRange:
		loV, sig := s.evalExpr(le.condOrIter)
		if sig.kind != sigNone {
			return nil, sig
		}
		hiV, sig := s.evalExpr(le.rangeEnd)
		if sig.kind != sigNone {
			return nil, sig
		}
		if sig := s.runIntRangeLoop(loV, hiV, le.inclusive, syms[0], fr, le.span, runBody, &broken, &breakVal); sig.kind != sigNone {
			return nil, sig
		}

	case ast.ForIter:
		colV, sig := s.evalExpr(le.condOrIter)
		if sig.kind != sigNone {
			return nil, sig
		}
		switch colV.kind {
		case ctList:
			for i := 0; i < len(colV.list.elems); i++ {
				if sig := s.step(le.span); sig.kind != sigNone {
					return nil, sig
				}
				if syms[1] != nil && fr != nil {
					fr.locals[syms[0]] = &ctSlot{val: newCtSignedInt(int64(i), types.TypeInt), ty: types.TypeInt}
					fr.locals[syms[1]] = &ctSlot{val: colV.list.elems[i], ty: syms[1].Type}
				} else if syms[0] != nil && fr != nil {
					fr.locals[syms[0]] = &ctSlot{val: colV.list.elems[i], ty: syms[0].Type}
				}
				br, bv, bsig := runBody()
				if bsig.kind != sigNone {
					return nil, bsig
				}
				if br {
					broken = true
					breakVal = bv
					break
				}
			}
		case ctArray:
			for i := 0; i < len(colV.arr.elems); i++ {
				if sig := s.step(le.span); sig.kind != sigNone {
					return nil, sig
				}
				if syms[1] != nil && fr != nil {
					fr.locals[syms[0]] = &ctSlot{val: newCtSignedInt(int64(i), types.TypeInt), ty: types.TypeInt}
					fr.locals[syms[1]] = &ctSlot{val: colV.arr.elems[i], ty: syms[1].Type}
				} else if syms[0] != nil && fr != nil {
					fr.locals[syms[0]] = &ctSlot{val: colV.arr.elems[i], ty: syms[0].Type}
				}
				br, bv, bsig := runBody()
				if bsig.kind != sigNone {
					return nil, bsig
				}
				if br {
					broken = true
					breakVal = bv
					break
				}
			}
		case ctString:
			for byteOff, ch := range colV.s {
				if sig := s.step(le.span); sig.kind != sigNone {
					return nil, sig
				}
				if syms[1] != nil && fr != nil {
					fr.locals[syms[0]] = &ctSlot{val: newCtSignedInt(int64(byteOff), types.TypeInt), ty: types.TypeInt}
					fr.locals[syms[1]] = &ctSlot{val: newCtChar(ch), ty: types.TypeChar}
				} else if syms[0] != nil && fr != nil {
					fr.locals[syms[0]] = &ctSlot{val: newCtChar(ch), ty: types.TypeChar}
				}
				br, bv, bsig := runBody()
				if bsig.kind != sigNone {
					return nil, bsig
				}
				if br {
					broken = true
					breakVal = bv
					break
				}
			}
		case ctSet:
			for _, el := range colV.set.elems {
				if sig := s.step(le.span); sig.kind != sigNone {
					return nil, sig
				}
				if syms[0] != nil && fr != nil {
					fr.locals[syms[0]] = &ctSlot{val: el, ty: syms[0].Type}
				}
				br, bv, bsig := runBody()
				if bsig.kind != sigNone {
					return nil, bsig
				}
				if br {
					broken = true
					breakVal = bv
					break
				}
			}
		case ctMap:
			for _, ent := range colV.m.entries {
				if sig := s.step(le.span); sig.kind != sigNone {
					return nil, sig
				}
				if syms[1] != nil && fr != nil {
					fr.locals[syms[0]] = &ctSlot{val: ent.key, ty: syms[0].Type}
					fr.locals[syms[1]] = &ctSlot{val: ent.val, ty: syms[1].Type}
				} else if syms[0] != nil && fr != nil {
					// Entry struct (`MapEntry[K, V][key: k, value: v]`)
					if entrySt, ok := syms[0].Type.(*types.Struct); ok {
						fields := map[string]*ctVal{"key": ent.key, "value": ent.val}
						order := []string{"key", "value"}
						fr.locals[syms[0]] = &ctSlot{
							val: &ctVal{kind: ctStruct, ty: entrySt, st: &ctStructBox{ty: entrySt, fields: fields, fieldOrder: order}},
							ty:  entrySt,
						}
					}
				}
				br, bv, bsig := runBody()
				if bsig.kind != sigNone {
					return nil, bsig
				}
				if br {
					broken = true
					breakVal = bv
					break
				}
			}
		case ctRange:
			loV := newCtInt(colV.rng.low, colV.rng.prim)
			hiV := newCtInt(colV.rng.high, colV.rng.prim)
			if colV.rng.prim == types.TypeChar {
				loV = newCtChar(rune(colV.rng.low))
				hiV = newCtChar(rune(colV.rng.high))
			}
			if sig := s.runIntRangeLoop(loV, hiV, colV.rng.inclusive, syms[0], fr, le.span, runBody, &broken, &breakVal); sig.kind != sigNone {
				return nil, sig
			}
		case ctStruct:
			if elems, ok := listStructElems(colV); ok {
				for i := 0; i < len(elems); i++ {
					if sig := s.step(le.span); sig.kind != sigNone {
						return nil, sig
					}
					if syms[1] != nil && fr != nil {
						fr.locals[syms[0]] = &ctSlot{val: newCtSignedInt(int64(i), types.TypeInt), ty: types.TypeInt}
						fr.locals[syms[1]] = &ctSlot{val: elems[i], ty: syms[1].Type}
					} else if syms[0] != nil && fr != nil {
						fr.locals[syms[0]] = &ctSlot{val: elems[i], ty: syms[0].Type}
					}
					br, bv, bsig := runBody()
					if bsig.kind != sigNone {
						return nil, bsig
					}
					if br {
						broken = true
						breakVal = bv
						break
					}
				}
			} else if elems, ok := setStructElems(colV); ok {
				for _, el := range elems {
					if sig := s.step(le.span); sig.kind != sigNone {
						return nil, sig
					}
					if syms[0] != nil && fr != nil {
						fr.locals[syms[0]] = &ctSlot{val: el, ty: syms[0].Type}
					}
					br, bv, bsig := runBody()
					if bsig.kind != sigNone {
						return nil, bsig
					}
					if br {
						broken = true
						breakVal = bv
						break
					}
				}
			} else if entries, ok := mapStructEntries(colV); ok {
				for _, ent := range entries {
					if sig := s.step(le.span); sig.kind != sigNone {
						return nil, sig
					}
					if syms[1] != nil && fr != nil {
						fr.locals[syms[0]] = &ctSlot{val: ent.key, ty: syms[0].Type}
						fr.locals[syms[1]] = &ctSlot{val: ent.val, ty: syms[1].Type}
					} else if syms[0] != nil && fr != nil {
						if entrySt, ok := syms[0].Type.(*types.Struct); ok {
							fields := map[string]*ctVal{"key": ent.key, "value": ent.val}
							order := []string{"key", "value"}
							fr.locals[syms[0]] = &ctSlot{
								val: &ctVal{kind: ctStruct, ty: entrySt, st: &ctStructBox{ty: entrySt, fields: fields, fieldOrder: order}},
								ty:  entrySt,
							}
						}
					}
					br, bv, bsig := runBody()
					if bsig.kind != sigNone {
						return nil, bsig
					}
					if br {
						broken = true
						breakVal = bv
						break
					}
				}
			} else {
				return nil, abortSig(le.condOrIter.GetSpan(), "unsupported collection kind in compile-time loop")
			}
		default:
			return nil, abortSig(le.condOrIter.GetSpan(), "unsupported collection kind in compile-time loop")
		}
	}

	if broken {
		if breakVal != nil {
			return breakVal, okSig()
		}
		return newCtVoid(), okSig()
	}
	if le.elseExpr != nil {
		return s.evalExpr(le.elseExpr)
	}
	return finishCollected()
}

func (s *ctState) runIntRangeLoop(
	loV, hiV *ctVal,
	inclusive bool,
	sym *Symbol,
	fr *ctFrame,
	span diag.Span,
	runBody func() (bool, *ctVal, ctSignal),
	broken *bool,
	breakVal **ctVal,
) ctSignal {
	if loV.kind == ctChar {
		lo, hi := loV.ch, hiV.ch
		for cur := lo; ; cur++ {
			if inclusive {
				if cur > hi {
					break
				}
			} else if cur >= hi {
				break
			}
			if sig := s.step(span); sig.kind != sigNone {
				return sig
			}
			if sym != nil && fr != nil {
				fr.locals[sym] = &ctSlot{val: newCtChar(cur), ty: types.TypeChar}
			}
			br, bv, bsig := runBody()
			if bsig.kind != sigNone {
				return bsig
			}
			if br {
				*broken = true
				*breakVal = bv
				return okSig()
			}
		}
		return okSig()
	}

	p := loV.prim
	if p == nil {
		p = types.TypeInt
	}
	if !p.IsSignedInt() {
		lo, hi := loV.asUint64(), hiV.asUint64()
		for cur := lo; ; {
			if inclusive {
				if cur > hi {
					break
				}
			} else if cur >= hi {
				break
			}
			if sig := s.step(span); sig.kind != sigNone {
				return sig
			}
			if sym != nil && fr != nil {
				fr.locals[sym] = &ctSlot{val: newCtInt(cur, p), ty: p}
			}
			br, bv, bsig := runBody()
			if bsig.kind != sigNone {
				return bsig
			}
			if br {
				*broken = true
				*breakVal = bv
				return okSig()
			}
			if cur == hi {
				break
			}
			cur++
		}
		return okSig()
	}

	lo, hi := loV.asInt64(), hiV.asInt64()
	for cur := lo; ; {
		if inclusive {
			if cur > hi {
				break
			}
		} else if cur >= hi {
			break
		}
		if sig := s.step(span); sig.kind != sigNone {
			return sig
		}
		if sym != nil && fr != nil {
			fr.locals[sym] = &ctSlot{val: newCtSignedInt(cur, p), ty: p}
		}
		br, bv, bsig := runBody()
		if bsig.kind != sigNone {
			return bsig
		}
		if br {
			*broken = true
			*breakVal = bv
			return okSig()
		}
		if cur == hi {
			break
		}
		cur++
	}
	return okSig()
}

func (s *ctState) evalMatch(me *ast.MatchExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	fr := s.frame()
	arms := me.Arms
	if fr != nil && fr.bm != nil {
		if expanded, ok := fr.bm.MatchArms[me]; ok {
			arms = expanded
		}
		if armIdx, ok := fr.bm.ComptimeMatchArm[me]; ok && armIdx >= 0 && armIdx < len(arms) {
			arm := arms[armIdx]
			s.pushDropScope()
			res, bsig := s.evalExpr(arm.Body)
			dsig := s.popDropScope(arm.Body.GetSpan())
			if bsig.kind == sigAbort {
				return nil, bsig
			}
			if dsig.kind == sigAbort {
				return nil, dsig
			}
			if bsig.kind == sigUnwind && inf != nil && bsig.targetID == inf.TargetID {
				return bsig.val, okSig()
			}
			return res, bsig
		}
	}

	scruts := make([]*ctVal, len(me.Scrutinees))
	for i, sc := range me.Scrutinees {
		sv, sig := s.evalExpr(sc)
		if sig.kind != sigNone {
			return nil, sig
		}
		scruts[i] = sv
	}

	for _, arm := range arms {
		if len(arm.Cases) == 0 {
			// Bare `default:`
			s.pushDropScope()
			res, sig := s.evalExpr(arm.Body)
			if dsig := s.popDropScope(arm.Body.GetSpan()); dsig.kind == sigAbort {
				return nil, dsig
			}
			if sig.kind == sigUnwind && inf != nil && sig.targetID == inf.TargetID {
				return sig.val, okSig()
			}
			return res, sig
		}
		for _, mc := range arm.Cases {
			matched, bindings, sig := s.matchCase(scruts, mc)
			if sig.kind != sigNone {
				return nil, sig
			}
			if matched {
				s.pushDropScope()
				if fr != nil {
					for sym, sl := range bindings {
						fr.locals[sym] = sl
						s.registerDropLocal(sym, sl, sym.Type)
					}
				}
				res, bsig := s.evalExpr(arm.Body)
				dsig := s.popDropScope(arm.Body.GetSpan())
				if bsig.kind == sigAbort {
					return nil, bsig
				}
				if dsig.kind == sigAbort {
					return nil, dsig
				}
				if bsig.kind == sigUnwind && inf != nil && bsig.targetID == inf.TargetID {
					return bsig.val, okSig()
				}
				return res, bsig
			}
		}
	}
	return nil, abortSig(me.Span, "no matching arm in compile-time `match`")
}

func (s *ctState) matchCase(scruts []*ctVal, mc ast.MatchCase) (bool, map[*Symbol]*ctSlot, ctSignal) {
	bindings := make(map[*Symbol]*ctSlot)
	if len(scruts) == 0 {
		// 0-argument `match` evaluates boolean predicate pattern
		if len(mc.Patterns) != 1 {
			return false, nil, okSig()
		}
		if ep, ok := mc.Patterns[0].(*ast.ExprPattern); ok {
			pv, sig := s.evalExpr(ep.Expr)
			if sig.kind != sigNone {
				return false, nil, sig
			}
			return pv.b, bindings, okSig()
		}
		return false, nil, okSig()
	}

	for i, pat := range mc.Patterns {
		if i >= len(scruts) {
			return false, nil, okSig()
		}
		ok, sig := s.matchPattern(scruts[i], pat, bindings)
		if sig.kind != sigNone {
			return false, nil, sig
		}
		if !ok {
			return false, nil, okSig()
		}
	}
	return true, bindings, okSig()
}

func (s *ctState) matchPattern(scrut *ctVal, pat ast.Pattern, bindings map[*Symbol]*ctSlot) (bool, ctSignal) {
	fr := s.frame()
	switch p := pat.(type) {
	case *ast.WildcardPattern:
		return true, okSig()

	case *ast.ExprPattern:
		if re, isRng := p.Expr.(*ast.RangeExpr); isRng {
			loV, sig := s.evalExpr(re.Low)
			if sig.kind != sigNone {
				return false, sig
			}
			hiV, sig := s.evalExpr(re.High)
			if sig.kind != sigNone {
				return false, sig
			}
			cLo := scrut.cmpVal(loV)
			cHi := scrut.cmpVal(hiV)
			if re.Inclusive {
				return cLo >= 0 && cHi <= 0, okSig()
			}
			return cLo >= 0 && cHi < 0, okSig()
		}
		pv, sig := s.evalExpr(p.Expr)
		if sig.kind != sigNone {
			return false, sig
		}
		return scrut.equals(s.a, pv), okSig()

	case *ast.ContainsPattern:
		cv, sig := s.evalExpr(p.Container)
		if sig.kind != sigNone {
			return false, sig
		}
		if fr != nil && fr.bm != nil {
			if ci := fr.bm.ContainsPatterns[p]; ci != nil && ci.Kind == ContainsMethod && ci.TargetFunc != nil {
				res, sig := s.callFuncInstance(ci.TargetFunc, valArgs(cv, scrut), p.Span)
				if sig.kind != sigNone {
					return false, sig
				}
				return res.b, okSig()
			}
		}
		return s.evalContains(cv, scrut, p.Span)

	case *ast.MethodPattern:
		if fr != nil && fr.bm != nil {
			if vp := fr.bm.BareVariantPatterns[p]; vp != nil {
				return s.matchPattern(scrut, vp, bindings)
			}
			if mpi := fr.bm.MethodPatterns[p]; mpi != nil && mpi.Call != nil {
				cInf := s.infoFor(mpi.Call)
				args := make([]ctArg, 0, 1+len(mpi.Call.Args))
				args = append(args, ctArg{val: scrut})
				for _, aExpr := range mpi.Call.Args {
					av, sig := s.evalExpr(aExpr)
					if sig.kind != sigNone {
						return false, sig
					}
					args = append(args, ctArg{val: av})
				}
				if cInf != nil && cInf.TargetFunc != nil {
					res, sig := s.callFuncInstance(cInf.TargetFunc, args, p.Span)
					if sig.kind != sigNone {
						return false, sig
					}
					return res.b, okSig()
				}
			}
		}
		return false, abortSig(p.Span, "unresolved method pattern %q at compile time", p.Method)

	case *ast.VariantPattern:
		if scrut.kind != ctEnum || scrut.en == nil {
			return false, okSig()
		}
		mem, _ := p.Target.(*ast.MemberExpr)
		varName := ""
		if mem != nil {
			varName = mem.Member
		}
		if scrut.variantIdx < 0 || scrut.variantIdx >= len(scrut.en.Variants) {
			return false, okSig()
		}
		active := scrut.en.Variants[scrut.variantIdx]
		if active.Name != varName {
			return false, okSig()
		}
		if len(p.Bindings) > 0 && fr != nil && fr.bm != nil {
			psyms := fr.bm.PatternSyms[p]
			if len(psyms) == 1 && psyms[0] != nil {
				pv := scrut.payload
				if p.Bindings[0].Mode != ast.PatternBindMut {
					pv = pv.clone()
				}
				bindings[psyms[0]] = &ctSlot{val: pv, ty: psyms[0].Type}
			} else if len(psyms) > 1 && scrut.payload != nil && scrut.payload.kind == ctTuple && scrut.payload.tup != nil {
				for i, ps := range psyms {
					if ps != nil && i < len(scrut.payload.tup.elems) {
						pv := scrut.payload.tup.elems[i]
						if p.Bindings[i].Mode != ast.PatternBindMut {
							pv = pv.clone()
						}
						bindings[ps] = &ctSlot{val: pv, ty: ps.Type}
					}
				}
			}
		}
		return true, okSig()
	}
	return false, okSig()
}

// -----------------------------------------------------------------------------
// Calls, Closures, Intrinsics, & std/core Collection Hooks
// -----------------------------------------------------------------------------

func (s *ctState) evalCall(call *ast.CallExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	if inf == nil {
		return nil, abortSig(call.Span, "missing call resolution at compile time")
	}

	switch inf.CallKind {
	case CallExternC:
		return nil, abortSig(call.Span, "cannot call `extern` C function in compile-time expression")

	case CallIntrinsic:
		return s.evalIntrinsicCall(call, inf)

	case CallBuiltinFunc:
		return s.evalBuiltinFuncCall(call, inf)

	case CallBuiltinMethod:
		return s.evalBuiltinMethodCall(call, inf)

	case CallTypeConvert:
		return s.evalTypeConvertCall(call, inf)

	case CallInterfaceBox:
		av, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		return av.clone(), okSig()

	case CallEnumVariant:
		return s.evalEnumVariantCall(call, inf)

	case CallDirectFunc:
		fi := inf.TargetFunc
		if fi == nil {
			return nil, abortSig(call.Span, "missing target function at compile time")
		}
		if !s.isCallable(fi) {
			return nil, abortSig(call.Span, "cannot call runtime-only (`bind`) function %q at compile time", fi.Name)
		}
		args := make([]ctArg, 0, len(fi.Sig.Params))
		if inf.MethodRecv != nil {
			if inf.MethodRecvMode == types.ModeMut {
				pl, sig := s.evalPlace(inf.MethodRecv)
				if sig.kind != sigNone {
					return nil, sig
				}
				if inf.IsOptChain {
					rv, sig := pl.get()
					if sig.kind != sigNone {
						return nil, sig
					}
					if rv.kind == ctOpt {
						if !rv.hasOpt {
							return newCtNone(inf.Type), okSig()
						}
						optPlace := ctPlace{
							get: func() (*ctVal, ctSignal) {
								cur, s2 := pl.get()
								if s2.kind != sigNone {
									return nil, s2
								}
								return cur.optElem, okSig()
							},
							set: func(nv *ctVal) ctSignal {
								cur, s2 := pl.get()
								if s2.kind != sigNone {
									return s2
								}
								cp := cur.clone()
								cp.optElem = nv
								return pl.set(cp)
							},
						}
						args = append(args, ctArg{place: &optPlace})
					} else {
						args = append(args, ctArg{place: &pl})
					}
				} else {
					args = append(args, ctArg{place: &pl})
				}
			} else {
				rv, sig := s.evalExpr(inf.MethodRecv)
				if sig.kind != sigNone {
					return nil, sig
				}
				if inf.IsOptChain && rv.kind == ctOpt {
					if !rv.hasOpt {
						return newCtNone(inf.Type), okSig()
					}
					rv = rv.optElem
				}
				if inf.MethodRecvMode == types.ModeOwn {
					rv = rv.clone()
				}
				args = append(args, ctArg{val: rv})
			}
		}
		var tempDrops []struct {
			val  *ctVal
			span diag.Span
		}
		argStart := 0
		if inf.MethodRecv != nil {
			argStart = 1
			if inf.MethodRecvMode == types.ModeRead && len(args) > 0 && args[0].val != nil && !s.isCtLvalueExpr(inf.MethodRecv) {
				tempDrops = append(tempDrops, struct {
					val  *ctVal
					span diag.Span
				}{val: args[0].val, span: inf.MethodRecv.GetSpan()})
			}
		}
		for i, aExpr := range call.Args {
			mode := types.ModeRead
			if argStart+i < len(fi.Sig.Params) {
				mode = fi.Sig.Params[argStart+i].Mode
			}
			av, sig := s.evalCallArg(aExpr, mode)
			if sig.kind != sigNone {
				return nil, sig
			}
			args = append(args, av)
			if mode == types.ModeRead && av.val != nil && !s.isCtLvalueExpr(aExpr) {
				tempDrops = append(tempDrops, struct {
					val  *ctVal
					span diag.Span
				}{val: av.val, span: aExpr.GetSpan()})
			}
		}
		res, sig := s.callFuncInstance(fi, args, call.Span)
		for _, td := range tempDrops {
			if s.sharesLiveSlotAlloc(td.val) {
				continue
			}
			if dsig := s.dropValue(td.val.ty, td.val, td.span); dsig.kind != sigNone && sig.kind == sigNone {
				return nil, dsig
			}
		}
		if sig.kind != sigNone {
			return nil, sig
		}
		if inf.IsOptChain && inf.Type != nil && res != nil && res.kind != ctOpt {
			return newCtSome(inf.Type, res), okSig()
		}
		return res, okSig()

	case CallClosureValue:
		fv, sig := s.evalExpr(call.Callee)
		if sig.kind != sigNone {
			return nil, sig
		}
		if fv == nil || fv.kind != ctFn {
			return nil, abortSig(call.Span, "invalid function value at compile time")
		}
		if fv.fn != nil {
			if !s.isCallable(fv.fn) {
				return nil, abortSig(call.Span, "cannot call runtime-only function %q through pointer at compile time", fv.fn.Name)
			}
			args := make([]ctArg, len(call.Args))
			for i, aExpr := range call.Args {
				mode := types.ModeRead
				if i < len(fv.fn.Sig.Params) {
					mode = fv.fn.Sig.Params[i].Mode
				}
				av, sig := s.evalCallArg(aExpr, mode)
				if sig.kind != sigNone {
					return nil, sig
				}
				args[i] = av
			}
			return s.callFuncInstance(fv.fn, args, call.Span)
		}
		if fv.lambda != nil {
			args := make([]ctArg, len(call.Args))
			for i, aExpr := range call.Args {
				mode := types.ModeRead
				if fv.lambda.Sig != nil && i < len(fv.lambda.Sig.Params) {
					mode = fv.lambda.Sig.Params[i].Mode
				}
				av, sig := s.evalCallArg(aExpr, mode)
				if sig.kind != sigNone {
					return nil, sig
				}
				args[i] = av
			}
			return s.callLambda(fv.lambda, fv.captures, args, call.Span)
		}
	}

	return nil, abortSig(call.Span, "unsupported call kind %v at compile time", inf.CallKind)
}

func (s *ctState) isCtLvalueExpr(e ast.Expr) bool {
	for e != nil {
		switch v := e.(type) {
		case *ast.ComptimeExpr:
			e = v.Expr
		case *ast.UnsafeExpr:
			e = v.Expr
		case *ast.IdentExpr, *ast.MemberExpr, *ast.IndexExpr:
			return true
		case *ast.UnaryExpr:
			return v.Op == token.Star || v.Op == token.Amp
		case *ast.CallExpr:
			if s != nil {
				if inf := s.infoFor(v); inf != nil && !inf.IsOptChain && (inf.BuiltinName == "struct.field" || inf.BuiltinName == "seq.elem" || inf.BuiltinName == "enum.payload") {
					if mem, isMem := v.Callee.(*ast.MemberExpr); isMem {
						if bInf := s.infoFor(mem.Base); bInf != nil {
							switch bInf.Type.(type) {
							case *types.Ptr, *types.Ref:
								return true
							}
						}
						return s.isCtLvalueExpr(mem.Base)
					}
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}

func (s *ctState) evalCallArg(aExpr ast.Expr, mode types.ParamMode) (ctArg, ctSignal) {
	if mode == types.ModeMut {
		pl, sig := s.evalPlace(aExpr)
		if sig.kind != sigNone {
			return ctArg{}, sig
		}
		return ctArg{place: &pl}, okSig()
	}
	v, sig := s.evalExpr(aExpr)
	if sig.kind != sigNone {
		return ctArg{}, sig
	}
	if mode == types.ModeOwn {
		v = v.clone()
		if id, isId := aExpr.(*ast.IdentExpr); isId {
			if inf := s.infoFor(id); inf != nil && inf.MovedLocal != nil {
				if sl, ok := s.lookupSlot(inf.MovedLocal); ok {
					sl.moved = true
				}
			}
		}
	}
	return ctArg{val: v}, okSig()
}

func (s *ctState) callFuncInstance(fi *FuncInstance, argList []ctArg, callSpan diag.Span) (*ctVal, ctSignal) {
	if s.depth >= defaultComptimeMaxDepth {
		return nil, abortSig(callSpan, "compile-time call stack depth exceeded (%d) calling %q", defaultComptimeMaxDepth, fi.Name)
	}
	s.a.ensureFuncCheckedForComptime(fi, callSpan)
	if !s.isCallable(fi) {
		return nil, abortSig(callSpan, "cannot call runtime-only function %q at compile time", fi.Name)
	}

	s.depth++
	prevLimit := s.stepLimit
	prevSteps := s.steps
	hasOverrideLimit := fi.ComptimeLimit > 0
	if hasOverrideLimit {
		s.steps = 0
		s.stepLimit = fi.ComptimeLimit
	}
	prevMemLimit := s.memLimit
	hasMemOverride := fi.ComptimeMemLimit > 0 && fi.ComptimeMemLimit > s.memLimit
	if hasMemOverride {
		s.memLimit = fi.ComptimeMemLimit
	}
	locals := make(map[*Symbol]*ctSlot, len(fi.ParamSyms))
	for i, ps := range fi.ParamSyms {
		if ps != nil && i < len(argList) {
			if argList[i].place != nil {
				locals[ps] = &ctSlot{ty: ps.Type, refPlace: argList[i].place}
			} else {
				locals[ps] = &ctSlot{val: argList[i].val, ty: ps.Type}
			}
		}
	}
	s.frames = append(s.frames, ctFrame{
		fi:       fi,
		mod:      fi.Mod,
		bm:       fi.BodyMaps,
		callSpan: callSpan,
		locals:   locals,
	})
	defer func() {
		s.frames = s.frames[:len(s.frames)-1]
		if hasOverrideLimit {
			s.stepLimit = prevLimit
			s.steps = prevSteps + 1
		}
		if hasMemOverride {
			s.memLimit = prevMemLimit
		}
		s.depth--
	}()

	s.pushDropScope()
	for i, ps := range fi.ParamSyms {
		if ps != nil && i < len(fi.Sig.Params) && fi.Sig.Params[i].Mode == types.ModeOwn {
			s.registerDropLocal(ps, locals[ps], ps.Type)
		}
	}
	val, sig := s.evalExpr(fi.Body)
	dsig := s.popDropScope(fi.Body.GetSpan())
	if sig.kind == sigAbort {
		if len(sig.trace) == 0 {
			sig.trace = append([]ctFrame(nil), s.frames...)
		}
		return nil, sig
	}
	if dsig.kind == sigAbort {
		if len(dsig.trace) == 0 {
			dsig.trace = append([]ctFrame(nil), s.frames...)
		}
		return nil, dsig
	}
	if sig.kind == sigUnwind && sig.targetKind == ast.UnwindFunc {
		if fi.BodyMaps != nil && sig.targetID == fi.FuncTargetID {
			if sig.val != nil {
				out := sig.val.clone().wrapResult(fi.Sig.Ret)
				s.preserveDescriptorMeta(fi, argList, out)
				return out, okSig()
			}
			return newCtVoid(), okSig()
		}
		return nil, sig
	}
	if sig.kind != sigNone {
		return nil, sig
	}
	if val != nil && fi.Sig != nil {
		val = val.wrapResult(fi.Sig.Ret)
	}
	s.preserveDescriptorMeta(fi, argList, val)
	return val, okSig()
}

func (s *ctState) preserveDescriptorMeta(fi *FuncInstance, argList []ctArg, res *ctVal) {
	if fi == nil || res == nil || res.kind != ctStruct || res.st == nil || !fi.IsMethod || fi.IsStaticAssoc || len(argList) != 1 {
		return
	}
	isDescCopy := fi.RecvType == s.a.Prog.StructFieldInfo ||
		fi.RecvType == s.a.Prog.StructVariantInfo ||
		fi.RecvType == s.a.Prog.StructElemInfo
	if !isDescCopy || !strings.HasSuffix(fi.Name, ".copy") {
		return
	}
	if sv := argList[0].val; sv != nil && sv.kind == ctStruct && sv.st != nil && sv.st.refl != nil {
		res.st.refl = sv.st.refl
	}
}

func (s *ctState) callLambda(lam *LambdaInfo, captures map[*Symbol]*ctSlot, argList []ctArg, callSpan diag.Span) (*ctVal, ctSignal) {
	if s.depth >= defaultComptimeMaxDepth {
		return nil, abortSig(callSpan, "compile-time call stack depth exceeded (%d) in closure", defaultComptimeMaxDepth)
	}
	s.depth++
	locals := make(map[*Symbol]*ctSlot, len(lam.ParamSyms))
	for i, ps := range lam.ParamSyms {
		if ps != nil && i < len(argList) {
			if argList[i].place != nil {
				locals[ps] = &ctSlot{ty: ps.Type, refPlace: argList[i].place}
			} else {
				locals[ps] = &ctSlot{val: argList[i].val, ty: ps.Type}
			}
		}
	}
	s.frames = append(s.frames, ctFrame{
		lambda:   lam,
		mod:      s.curMod(),
		bm:       lam.BodyMaps,
		callSpan: callSpan,
		locals:   locals,
		captures: captures,
	})
	defer func() {
		s.frames = s.frames[:len(s.frames)-1]
		s.depth--
	}()

	s.pushDropScope()
	val, sig := s.evalExpr(lam.Lit.Body)
	dsig := s.popDropScope(lam.Lit.Body.GetSpan())
	if sig.kind == sigAbort {
		if len(sig.trace) == 0 {
			sig.trace = append([]ctFrame(nil), s.frames...)
		}
		return nil, sig
	}
	if dsig.kind == sigAbort {
		if len(dsig.trace) == 0 {
			dsig.trace = append([]ctFrame(nil), s.frames...)
		}
		return nil, dsig
	}
	if sig.kind == sigUnwind && sig.targetKind == ast.UnwindFunc && sig.targetID == lam.FuncTargetID {
		if sig.val != nil && lam.Sig != nil {
			return sig.val.clone().wrapResult(lam.Sig.Ret), okSig()
		}
		return sig.val, okSig()
	}
	if sig.kind != sigNone {
		return nil, sig
	}
	if val != nil && lam.Sig != nil {
		val = val.wrapResult(lam.Sig.Ret)
	}
	return val, okSig()
}

func (s *ctState) evalEnumVariantCall(call *ast.CallExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	en := inf.EnumType
	vr := inf.EnumVariant
	if en == nil || vr == nil {
		return nil, abortSig(call.Span, "missing enum variant metadata at compile time")
	}
	idx := -1
	for i, v := range en.Variants {
		if v.Name == vr.Name {
			idx = i
			break
		}
	}
	var payload *ctVal
	if len(call.Args) == 1 {
		pv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		payload = pv.clone()
	} else if len(call.Args) > 1 {
		elems := make([]*ctVal, len(call.Args))
		for i, a := range call.Args {
			pv, sig := s.evalExpr(a)
			if sig.kind != sigNone {
				return nil, sig
			}
			elems[i] = pv.clone()
		}
		payload = &ctVal{kind: ctTuple, ty: vr.Payload, tup: &ctTupleBox{elems: elems}}
	}
	return &ctVal{
		kind:       ctEnum,
		ty:         en,
		en:         en,
		variantIdx: idx,
		payload:    payload,
	}, okSig()
}

func (s *ctState) evalIntrinsicCall(call *ast.CallExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	switch inf.BuiltinName {
	case "size_of":
		return newCtSignedInt(s.a.typeSize(inf.IntrinsicType), types.TypeInt), okSig()
	case "align_of":
		return newCtSignedInt(s.a.typeAlign(inf.IntrinsicType), types.TypeInt), okSig()
	case "is_plain":
		return newCtBool(inf.IntrinsicType != nil && inf.IntrinsicType.IsPlain()), okSig()
	}
	if !s.allowStdUnsafe() {
		return nil, abortSig(call.Span, "raw memory/system intrinsic `%s` is forbidden at compile time", inf.BuiltinName)
	}
	switch inf.BuiltinName {
	case "null_ptr":
		return &ctVal{
			kind:   ctPointer,
			ty:     inf.Type,
			ptrVal: &ctPtr{elemTy: inf.IntrinsicType, elemSize: s.simElemSize(inf.IntrinsicType)},
		}, okSig()

	case "is_null":
		pv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		isNull := pv == nil || pv.kind != ctPointer || pv.ptrVal == nil || (pv.ptrVal.alloc == nil && pv.ptrVal.place == nil)
		return newCtBool(isNull), okSig()

	case "alloc_raw", "alloc_uninit":
		cv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		cnt := cv.asInt64()
		al, sig := s.newHeapAlloc(inf.IntrinsicType, cnt, inf.BuiltinName == "alloc_raw", call.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return &ctVal{
			kind:   ctPointer,
			ty:     inf.Type,
			ptrVal: &ctPtr{alloc: al, elemTy: inf.IntrinsicType, elemSize: al.elemSize},
		}, okSig()

	case "realloc_raw":
		pv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		cv, sig := s.evalExpr(call.Args[1])
		if sig.kind != sigNone {
			return nil, sig
		}
		cnt := cv.asInt64()
		if cnt < 0 {
			return nil, abortSig(call.Span, "`realloc_raw` negative count (%d) at compile time", cnt)
		}
		var elemTy types.Type = inf.IntrinsicType
		if elemTy == nil && pv != nil && pv.ptrVal != nil {
			elemTy = pv.ptrVal.elemTy
		}
		if pv == nil || pv.ptrVal == nil || (pv.ptrVal.alloc == nil && pv.ptrVal.place == nil) {
			al, sig := s.newHeapAlloc(elemTy, cnt, false, call.Span)
			if sig.kind != sigNone {
				return nil, sig
			}
			return &ctVal{
				kind:   ctPointer,
				ty:     inf.Type,
				ptrVal: &ctPtr{alloc: al, elemTy: elemTy, elemSize: al.elemSize},
			}, okSig()
		}
		op := pv.ptrVal
		if op.alloc == nil || op.byteOff != 0 {
			return nil, abortSig(call.Span, "`realloc_raw` requires a base heap pointer at compile time")
		}
		if op.alloc.freed {
			return nil, abortSig(call.Span, "`realloc_raw` use-after-free at compile time")
		}
		if op.alloc.readOnly {
			return nil, abortSig(call.Span, "`realloc_raw` on read-only compile-time buffer")
		}
		al, sig := s.newHeapAlloc(op.alloc.elemTy, cnt, false, call.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		if op.alloc.isBytes {
			copy(al.bytes, op.alloc.bytes)
		} else {
			nCopy := len(op.alloc.cells)
			if len(al.cells) < nCopy {
				nCopy = len(al.cells)
			}
			for i := 0; i < nCopy; i++ {
				if op.alloc.cells[i] != nil {
					al.cells[i] = op.alloc.cells[i].clone()
				}
			}
		}
		op.alloc.freed = true
		s.freeMem(op.alloc.chargedBytes)
		op.alloc.chargedBytes = 0
		return &ctVal{
			kind:   ctPointer,
			ty:     inf.Type,
			ptrVal: &ctPtr{alloc: al, elemTy: op.elemTy, elemSize: op.elemSize, castTy: op.castTy},
		}, okSig()

	case "free_raw":
		pv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		if pv == nil || pv.kind != ctPointer || pv.ptrVal == nil || (pv.ptrVal.alloc == nil && pv.ptrVal.place == nil) {
			return newCtVoid(), okSig()
		}
		p := pv.ptrVal
		if p.alloc == nil || p.byteOff != 0 {
			return nil, abortSig(call.Span, "`free_raw` on non-base or stack pointer at compile time")
		}
		if p.alloc.readOnly {
			return nil, abortSig(call.Span, "`free_raw` on read-only compile-time buffer")
		}
		if p.alloc.freed {
			return nil, abortSig(call.Span, "double free in `free_raw` at compile time")
		}
		p.alloc.freed = true
		s.freeMem(p.alloc.chargedBytes)
		p.alloc.chargedBytes = 0
		return newCtVoid(), okSig()

	case "addr_of":
		pl, sig := s.evalPlace(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		cur, gsig := pl.get()
		if gsig.kind == sigNone && cur != nil && cur.kind == ctString {
			mv, msig := s.materializeStringVal(cur, call.Span)
			if msig.kind != sigNone {
				return nil, msig
			}
			_ = pl.set(mv)
		}
		return &ctVal{
			kind: ctPointer,
			ty:   inf.Type,
			ptrVal: &ctPtr{
				elemTy:   inf.IntrinsicType,
				elemSize: s.simElemSize(inf.IntrinsicType),
				place:    &pl,
			},
		}, okSig()

	case "ptr_add":
		pv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		ov, sig := s.evalExpr(call.Args[1])
		if sig.kind != sigNone {
			return nil, sig
		}
		off := ov.asInt64()
		if pv == nil || pv.kind != ctPointer || pv.ptrVal == nil || (pv.ptrVal.alloc == nil && pv.ptrVal.place == nil) {
			if off == 0 {
				return pv, okSig()
			}
			return nil, abortSig(call.Span, "null pointer arithmetic in `ptr_add` at compile time")
		}
		p := pv.ptrVal
		if p.alloc == nil {
			if off == 0 {
				return pv, okSig()
			}
			return nil, abortSig(call.Span, "pointer arithmetic on non-heap address in `ptr_add` at compile time")
		}
		if p.alloc.freed {
			return nil, abortSig(call.Span, "`ptr_add` on freed pointer at compile time")
		}
		cp := *p
		cp.byteOff += off * p.elemSize
		if off != 0 {
			cp.place = nil
		}
		return &ctVal{kind: ctPointer, ty: inf.Type, ptrVal: &cp}, okSig()

	case "ptr_read":
		pv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		if pv == nil || pv.kind != ctPointer {
			return nil, abortSig(call.Span, "`ptr_read` requires a pointer at compile time")
		}
		dp, sig := s.ptrDerefPlace(pv.ptrVal, call.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		val, sig := dp.get()
		if sig.kind != sigNone {
			return nil, sig
		}
		return val.clone(), okSig()

	case "ptr_write":
		pv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		nv, sig := s.evalExpr(call.Args[1])
		if sig.kind != sigNone {
			return nil, sig
		}
		if pv == nil || pv.kind != ctPointer {
			return nil, abortSig(call.Span, "`ptr_write` requires a pointer at compile time")
		}
		dp, sig := s.ptrDerefPlace(pv.ptrVal, call.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return newCtVoid(), dp.set(nv.clone())

	case "ptr_cast":
		srcV, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		toTy := inf.IntrinsicType
		if srcV != nil && srcV.kind == ctPointer && srcV.ptrVal != nil {
			cp := *srcV.ptrVal
			cp.elemTy = toTy
			cp.elemSize = s.simElemSize(toTy)
			cp.castTy = toTy
			return &ctVal{kind: ctPointer, ty: inf.Type, ptrVal: &cp}, okSig()
		}
		if srcV != nil && srcV.kind == ctInt && srcV.u == 0 {
			return &ctVal{
				kind:   ctPointer,
				ty:     inf.Type,
				ptrVal: &ctPtr{elemTy: toTy, elemSize: s.simElemSize(toTy), castTy: toTy},
			}, okSig()
		}
		return nil, abortSig(call.Span, "unsupported `ptr_cast` source at compile time")

	case "ptr_addr":
		pv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		return newCtInt(pv.ptrAddr(), types.TypeU64), okSig()

	case "copy_bytes":
		dstV, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		srcV, sig := s.evalExpr(call.Args[1])
		if sig.kind != sigNone {
			return nil, sig
		}
		cntV, sig := s.evalExpr(call.Args[2])
		if sig.kind != sigNone {
			return nil, sig
		}
		cnt := cntV.asInt64()
		if cnt < 0 {
			return nil, abortSig(call.Span, "`copy_bytes` negative byte count %d", cnt)
		}
		if cnt == 0 {
			return newCtVoid(), okSig()
		}
		if dstV == nil || dstV.ptrVal == nil || srcV == nil || srcV.ptrVal == nil {
			return nil, abortSig(call.Span, "`copy_bytes` null pointer at compile time")
		}
		dp, sp := dstV.ptrVal, srcV.ptrVal
		if dp.alloc == nil || sp.alloc == nil {
			return nil, abortSig(call.Span, "`copy_bytes` requires allocated buffer pointers at compile time")
		}
		if dp.alloc.freed || sp.alloc.freed {
			return nil, abortSig(call.Span, "`copy_bytes` use-after-free at compile time")
		}
		if dp.alloc.readOnly {
			return nil, abortSig(call.Span, "`copy_bytes` into read-only buffer at compile time")
		}
		if dp.alloc.isBytes && sp.alloc.isBytes {
			if dp.byteOff < 0 || dp.byteOff+cnt > int64(len(dp.alloc.bytes)) ||
				sp.byteOff < 0 || sp.byteOff+cnt > int64(len(sp.alloc.bytes)) {
				return nil, abortSig(call.Span, "`copy_bytes` out of bounds (%d bytes)", cnt)
			}
			copy(dp.alloc.bytes[dp.byteOff:dp.byteOff+cnt], sp.alloc.bytes[sp.byteOff:sp.byteOff+cnt])
			return newCtVoid(), okSig()
		}
		if !dp.alloc.isBytes && !sp.alloc.isBytes && dp.alloc.elemSize == sp.alloc.elemSize && dp.alloc.elemSize > 0 {
			es := dp.alloc.elemSize
			if dp.byteOff%es != 0 || sp.byteOff%es != 0 || cnt%es != 0 {
				return nil, abortSig(call.Span, "`copy_bytes` unaligned structured copy at compile time")
			}
			dIdx := dp.byteOff / es
			sIdx := sp.byteOff / es
			nEl := cnt / es
			if dIdx < 0 || dIdx+nEl > int64(len(dp.alloc.cells)) || sIdx < 0 || sIdx+nEl > int64(len(sp.alloc.cells)) {
				return nil, abortSig(call.Span, "`copy_bytes` structured copy out of bounds")
			}
			tmp := make([]*ctVal, nEl)
			for i := int64(0); i < nEl; i++ {
				if c := sp.alloc.cells[sIdx+i]; c != nil {
					tmp[i] = c.clone()
				}
			}
			for i := int64(0); i < nEl; i++ {
				dp.alloc.cells[dIdx+i] = tmp[i]
			}
			return newCtVoid(), okSig()
		}
		return nil, abortSig(call.Span, "`copy_bytes` incompatible buffer element kinds at compile time")

	case "zero_bytes":
		dstV, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		cntV, sig := s.evalExpr(call.Args[1])
		if sig.kind != sigNone {
			return nil, sig
		}
		cnt := cntV.asInt64()
		if cnt < 0 {
			return nil, abortSig(call.Span, "`zero_bytes` negative byte count %d", cnt)
		}
		if cnt == 0 {
			return newCtVoid(), okSig()
		}
		if dstV == nil || dstV.ptrVal == nil {
			return nil, abortSig(call.Span, "`zero_bytes` null pointer at compile time")
		}
		dp := dstV.ptrVal
		if dp.alloc != nil {
			if dp.alloc.freed {
				return nil, abortSig(call.Span, "`zero_bytes` use-after-free at compile time")
			}
			if dp.alloc.readOnly {
				return nil, abortSig(call.Span, "`zero_bytes` on read-only buffer at compile time")
			}
			if dp.alloc.isBytes {
				if dp.byteOff < 0 || dp.byteOff+cnt > int64(len(dp.alloc.bytes)) {
					return nil, abortSig(call.Span, "`zero_bytes` out of bounds (%d bytes)", cnt)
				}
				clear(dp.alloc.bytes[dp.byteOff : dp.byteOff+cnt])
				return newCtVoid(), okSig()
			}
			es := dp.alloc.elemSize
			if es > 0 && dp.byteOff%es == 0 && cnt%es == 0 {
				dIdx := dp.byteOff / es
				nEl := cnt / es
				if dIdx < 0 || dIdx+nEl > int64(len(dp.alloc.cells)) {
					return nil, abortSig(call.Span, "`zero_bytes` structured zero out of bounds")
				}
				for i := int64(0); i < nEl; i++ {
					dp.alloc.cells[dIdx+i] = s.zeroCtVal(dp.alloc.elemTy)
				}
				return newCtVoid(), okSig()
			}
			return nil, abortSig(call.Span, "`zero_bytes` unaligned structured range at compile time")
		}
		if dp.place != nil && dp.byteOff == 0 {
			cur, gsig := dp.place.get()
			if gsig.kind != sigNone {
				return nil, gsig
			}
			if cur != nil && s.simElemSize(cur.ty) == cnt {
				return newCtVoid(), dp.place.set(s.zeroCtVal(cur.ty))
			}
		}
		return nil, abortSig(call.Span, "`zero_bytes` unsupported target at compile time")

	case "fill_bytes":
		dstV, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		valV, sig := s.evalExpr(call.Args[1])
		if sig.kind != sigNone {
			return nil, sig
		}
		cntV, sig := s.evalExpr(call.Args[2])
		if sig.kind != sigNone {
			return nil, sig
		}
		cnt := cntV.asInt64()
		if cnt < 0 {
			return nil, abortSig(call.Span, "`fill_bytes` negative byte count %d", cnt)
		}
		if cnt == 0 {
			return newCtVoid(), okSig()
		}
		if dstV == nil || dstV.ptrVal == nil || dstV.ptrVal.alloc == nil {
			return nil, abortSig(call.Span, "`fill_bytes` requires allocated buffer pointer at compile time")
		}
		dp := dstV.ptrVal
		if dp.alloc.freed {
			return nil, abortSig(call.Span, "`fill_bytes` use-after-free at compile time")
		}
		if dp.alloc.readOnly {
			return nil, abortSig(call.Span, "`fill_bytes` on read-only buffer at compile time")
		}
		bv := byte(valV.asUint64())
		if dp.alloc.isBytes {
			if dp.byteOff < 0 || dp.byteOff+cnt > int64(len(dp.alloc.bytes)) {
				return nil, abortSig(call.Span, "`fill_bytes` out of bounds (%d bytes)", cnt)
			}
			for i := dp.byteOff; i < dp.byteOff+cnt; i++ {
				dp.alloc.bytes[i] = bv
			}
			return newCtVoid(), okSig()
		}
		if bv == 0 && dp.alloc.elemSize > 0 && dp.byteOff%dp.alloc.elemSize == 0 && cnt%dp.alloc.elemSize == 0 {
			dIdx := dp.byteOff / dp.alloc.elemSize
			nEl := cnt / dp.alloc.elemSize
			if dIdx >= 0 && dIdx+nEl <= int64(len(dp.alloc.cells)) {
				for i := int64(0); i < nEl; i++ {
					dp.alloc.cells[dIdx+i] = s.zeroCtVal(dp.alloc.elemTy)
				}
				return newCtVoid(), okSig()
			}
		}
		return nil, abortSig(call.Span, "`fill_bytes` non-zero fill on structured buffer at compile time")

	default:
		return nil, abortSig(call.Span, "raw memory/system intrinsic `%s` is forbidden at compile time", inf.BuiltinName)
	}
}

func evalSymlinksBestEffort(p string) (string, bool) {
	cur := filepath.Clean(p)
	var suffix []string
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			parts := make([]string, 0, 1+len(suffix))
			parts = append(parts, filepath.Clean(real))
			for i := len(suffix) - 1; i >= 0; i-- {
				parts = append(parts, suffix[i])
			}
			return filepath.Clean(filepath.Join(parts...)), true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func (a *Analyzer) lookupEmbedOverlay(joined, canonicalTarget string) (string, bool) {
	if a == nil || len(a.overlays) == 0 {
		return "", false
	}
	cleanJoined := filepath.Clean(joined)
	cleanCanon := filepath.Clean(canonicalTarget)
	if v, ok := a.overlays[cleanCanon]; ok {
		return v, true
	}
	if v, ok := a.overlays[cleanJoined]; ok {
		return v, true
	}
	for k, v := range a.overlays {
		cleanK := filepath.Clean(k)
		if cleanK == cleanJoined || cleanK == cleanCanon {
			return v, true
		}
		if canonK, ok := evalSymlinksBestEffort(cleanK); ok && canonK == cleanCanon {
			return v, true
		}
	}
	return "", false
}

func (s *ctState) evalCompEmbed(call *ast.CallExpr, asBytes bool) (*ctVal, ctSignal) {
	builtinName := "embed_str"
	if asBytes {
		builtinName = "embed_bytes"
	}
	pv, sig := s.evalExpr(call.Args[0])
	if sig.kind != sigNone {
		return nil, sig
	}
	rawPath, psig := s.valToString(pv, call.Args[0].GetSpan())
	if psig.kind != sigNone {
		return nil, psig
	}
	if rawPath == "" {
		return nil, abortSig(call.Span, "`comp.%s`: path must not be empty", builtinName)
	}
	if strings.ContainsRune(rawPath, 0) {
		return nil, abortSig(call.Span, "`comp.%s`: path contains a NUL byte", builtinName)
	}
	isDriveAbs := len(rawPath) >= 2 && rawPath[1] == ':' && ((rawPath[0] >= 'a' && rawPath[0] <= 'z') || (rawPath[0] >= 'A' && rawPath[0] <= 'Z'))
	if filepath.IsAbs(rawPath) || strings.HasPrefix(rawPath, "/") || strings.HasPrefix(rawPath, "\\") || isDriveAbs {
		return nil, abortSig(call.Span, "`comp.%s`: absolute path %q is not allowed (paths must be relative to the declaring source file)", builtinName, rawPath)
	}

	baseDir, allowedRoot := s.resolveEmbedModuleAndRoot(call.Span.Start.File)
	if baseDir == "" || allowedRoot == "" {
		return nil, abortSig(call.Span, "`comp.%s`: cannot resolve declaring source directory for %q", builtinName, rawPath)
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, abortSig(call.Span, "`comp.%s`: invalid source directory %q: %v", builtinName, baseDir, err)
	}
	absRoot, err := filepath.Abs(allowedRoot)
	if err != nil {
		return nil, abortSig(call.Span, "`comp.%s`: invalid package root %q: %v", builtinName, allowedRoot, err)
	}
	absBase = filepath.Clean(absBase)
	absRoot = filepath.Clean(absRoot)

	// Normalize backslashes to forward slashes so Windows-style traversal (`..\\secret`)
	// cannot bypass POSIX sandbox confinement either.
	normRel := strings.ReplaceAll(rawPath, "\\", "/")
	joined := filepath.Clean(filepath.Join(absBase, filepath.FromSlash(normRel)))
	if !isWithinSandboxDir(absRoot, joined) {
		return nil, abortSig(call.Span, "`comp.%s`: path %q escapes package sandbox %q", builtinName, rawPath, absRoot)
	}

	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, abortSig(call.Span, "`comp.%s`: cannot resolve package root %q: %v", builtinName, absRoot, err)
	}
	realRoot = filepath.Clean(realRoot)

	limit := s.memLimit
	if limit <= 0 {
		limit = defaultComptimeMaxMem
	}
	remaining := limit - s.memBytes

	var canonicalTarget string
	var diskInfo os.FileInfo
	realTarget, symErr := filepath.EvalSymlinks(joined)
	if symErr == nil {
		canonicalTarget = filepath.Clean(realTarget)
		if !isWithinSandboxDir(realRoot, canonicalTarget) {
			return nil, abortSig(call.Span, "`comp.%s`: symlink %q escapes package sandbox %q", builtinName, rawPath, absRoot)
		}
		info, statErr := os.Stat(canonicalTarget)
		if statErr != nil {
			return nil, abortSig(call.Span, "`comp.%s`: cannot stat %q: %v", builtinName, rawPath, statErr)
		}
		if info.IsDir() {
			return nil, abortSig(call.Span, "`comp.%s`: %q is a directory, expected a file", builtinName, rawPath)
		}
		if !info.Mode().IsRegular() {
			return nil, abortSig(call.Span, "`comp.%s`: %q is not a regular file", builtinName, rawPath)
		}
		diskInfo = info
	} else {
		if !os.IsNotExist(symErr) {
			return nil, abortSig(call.Span, "`comp.%s`: cannot open %q: %v", builtinName, rawPath, symErr)
		}
		canon, ok := evalSymlinksBestEffort(joined)
		if !ok {
			return nil, abortSig(call.Span, "`comp.%s`: cannot open %q: %v", builtinName, rawPath, symErr)
		}
		if !isWithinSandboxDir(realRoot, canon) {
			return nil, abortSig(call.Span, "`comp.%s`: symlink %q escapes package sandbox %q", builtinName, rawPath, absRoot)
		}
		canonicalTarget = canon
	}

	var data []byte
	if ov, hasOv := s.a.lookupEmbedOverlay(joined, canonicalTarget); hasOv {
		if int64(len(ov)) > remaining {
			return nil, abortSig(call.Span, "`comp.%s`: file %q (%d bytes) exceeds remaining compile-time memory budget (%d bytes remaining of %d MB; increase with `@comptime_limit(memory: N)`)", builtinName, rawPath, len(ov), remaining, limit/(1024*1024))
		}
		data = []byte(ov)
	} else {
		if symErr != nil {
			s.a.recordAttemptedEmbed(canonicalTarget)
			return nil, abortSig(call.Span, "`comp.%s`: cannot open %q: %v", builtinName, rawPath, symErr)
		}
		if diskInfo.Size() > remaining {
			return nil, abortSig(call.Span, "`comp.%s`: file %q (%d bytes) exceeds remaining compile-time memory budget (%d bytes remaining of %d MB; increase with `@comptime_limit(memory: N)`)", builtinName, rawPath, diskInfo.Size(), remaining, limit/(1024*1024))
		}
		var readErr error
		data, readErr = os.ReadFile(canonicalTarget)
		if readErr != nil {
			return nil, abortSig(call.Span, "`comp.%s`: failed reading %q: %v", builtinName, rawPath, readErr)
		}
	}
	s.a.recordEmbeddedFile(canonicalTarget, data)

	if asBytes {
		return s.makeByteListStruct(data, call.Span)
	}
	if !utf8.Valid(data) {
		return nil, abortSig(call.Span, "`comp.embed_str`: %q is not valid UTF-8 (use `comp.embed_bytes` for binary data)", rawPath)
	}
	if sig := s.allocMem(int64(len(data)), call.Span); sig.kind != sigNone {
		return nil, sig
	}
	return newCtString(string(data)), okSig()
}

func (s *ctState) resolveEmbedModuleAndRoot(callFile string) (baseDir, allowedRoot string) {
	var mod *Module
	if callFile != "" {
		cleanCall := filepath.Clean(callFile)
		absCall, err := filepath.Abs(callFile)
		if err != nil {
			absCall = cleanCall
		}
		absCall = filepath.Clean(absCall)
		if s.a != nil && s.a.Prog != nil {
			for _, m := range s.a.Prog.Modules {
				if m == nil || m.Path == "" {
					continue
				}
				mClean := filepath.Clean(m.Path)
				mAbs, mErr := filepath.Abs(m.Path)
				if mErr != nil {
					mAbs = mClean
				}
				if mClean == cleanCall || filepath.Clean(mAbs) == absCall {
					mod = m
					break
				}
			}
		}
		baseDir = filepath.Dir(absCall)
	}
	if mod == nil {
		mod = s.rootMod
	}
	if mod != nil {
		if baseDir == "" {
			if mod.Path != "" {
				baseDir = filepath.Dir(mod.Path)
			} else {
				baseDir = mod.Dir
			}
		}
		if mod.Pkg != nil {
			pkg := mod.Pkg
			// Third-party dependencies (`dep/...`), stdlib packages, and standalone scripts
			// are strictly confined to their own package directory (`pkg.Dir`).
			if strings.HasPrefix(pkg.ID, "dep/") || pkg.IsStd || pkg.IsStandalone || pkg.Dir == "" {
				return baseDir, pkg.Dir
			}
			// Project / workspace packages may embed shared project assets inside their
			// enclosing manifest/workspace member root when declared in `lowlight.toml`.
			if s.a != nil && s.a.manifestCtx != nil {
				for _, mm := range s.a.manifestCtx.WorkspaceMembers {
					if mm != nil && mm.Dir != "" && isWithinSandboxDir(mm.Dir, pkg.Dir) {
						return baseDir, mm.Dir
					}
				}
				if am := s.a.manifestCtx.ActiveManifest; am != nil && am.Dir != "" && isWithinSandboxDir(am.Dir, pkg.Dir) {
					return baseDir, am.Dir
				}
			}
			return baseDir, pkg.Dir
		}
		if mod.Dir != "" {
			return baseDir, mod.Dir
		}
	}
	return baseDir, baseDir
}

func isWithinSandboxDir(rootDir, targetPath string) bool {
	if rootDir == "" || targetPath == "" {
		return false
	}
	absRoot, err1 := filepath.Abs(rootDir)
	absTarget, err2 := filepath.Abs(targetPath)
	if err1 != nil || err2 != nil {
		return false
	}
	absRoot = filepath.Clean(absRoot)
	absTarget = filepath.Clean(absTarget)
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func (s *ctState) resolveCallerLocationSpan(callSpan diag.Span) diag.Span {
	sp := callSpan
	for i := len(s.frames) - 1; i >= 0; i-- {
		fr := s.frames[i]
		if fr.fi != nil && fr.fi.TrackCaller && fr.callSpan.Start.File != "" {
			sp = fr.callSpan
			continue
		}
		break
	}
	return sp
}

func (s *ctState) evalBuiltinFuncCall(call *ast.CallExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	switch inf.BuiltinName {
	case "compile_error":
		mv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		sp := s.resolveCallerLocationSpan(call.Span)
		return nil, abortSig(sp, "%s", mv.s)

	case "comp.log":
		mv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		sp := s.resolveCallerLocationSpan(call.Span)
		msg, msig := s.valToString(mv, call.Args[0].GetSpan())
		if msig.kind != sigNone {
			return nil, msig
		}
		if s.a != nil && s.a.Rep != nil {
			s.a.Rep.Notef(sp, "%s", msg)
		}
		return newCtVoid(), okSig()

	case "comp.embed_str":
		return s.evalCompEmbed(call, false)

	case "comp.embed_bytes":
		return s.evalCompEmbed(call, true)

	case "comp.type_name":
		if inf.IntrinsicType == nil {
			return newCtString(""), okSig()
		}
		return newCtString(inf.IntrinsicType.String()), okSig()

	case "comp.kind_of":
		return s.evalCompKindOf(inf.IntrinsicType, call.Span)

	case "comp.fields_of":
		return s.evalCompFieldsOf(inf.IntrinsicType, call.Span)

	case "comp.variants_of":
		return s.evalCompVariantsOf(inf.IntrinsicType, call.Span)

	case "comp.elems_of":
		return s.evalCompElemsOf(inf.IntrinsicType, call.Span)

	case "comp.attrs_of":
		return s.evalCompAttrsOf(inf.IntrinsicType, call.Span)

	case "comp.implements":
		if inf.IntrinsicType == nil || inf.ResolvedIface == nil {
			return newCtBool(false), okSig()
		}
		return newCtBool(s.a.ImplementsInterface(inf.IntrinsicType, inf.ResolvedIface)), okSig()

	case "panic":
		mv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		sp := s.resolveCallerLocationSpan(call.Span)
		msg, msig := s.valToString(mv, call.Args[0].GetSpan())
		if msig.kind != sigNone {
			return nil, msig
		}
		return nil, abortSig(sp, "compile-time panic: %s", msg)

	case "assert":
		cv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		if !cv.b {
			msg := "assertion failed"
			if len(call.Args) == 2 {
				mv, sig := s.evalExpr(call.Args[1])
				if sig.kind != sigNone {
					return nil, sig
				}
				msg = mv.s
			}
			sp := s.resolveCallerLocationSpan(call.Span)
			return nil, abortSig(sp, "compile-time assertion failed: %s", msg)
		}
		return newCtVoid(), okSig()

	case "caller_location":
		sp := s.resolveCallerLocationSpan(call.Span)
		return newCtString(fmt.Sprintf("%s:%d:%d", sp.Start.File, sp.Start.Line, sp.Start.Col)), okSig()

	case "caller_file":
		sp := s.resolveCallerLocationSpan(call.Span)
		return newCtString(sp.Start.File), okSig()

	case "caller_line":
		sp := s.resolveCallerLocationSpan(call.Span)
		return newCtSignedInt(int64(sp.Start.Line), types.TypeInt), okSig()

	case "caller_col":
		sp := s.resolveCallerLocationSpan(call.Span)
		return newCtSignedInt(int64(sp.Start.Col), types.TypeInt), okSig()

	case "abs":
		av, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		if av.kind == ctFloat {
			return newCtFloat(math.Abs(av.f), av.prim), okSig()
		}
		v := av.asInt64()
		if v < 0 {
			return newCtInt(^av.u+1, av.prim), okSig()
		}
		return av.clone(), okSig()

	case "min", "max":
		lv, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		rv, sig := s.evalExpr(call.Args[1])
		if sig.kind != sigNone {
			return nil, sig
		}
		cmp := lv.cmpVal(rv)
		if (inf.BuiltinName == "min" && cmp <= 0) || (inf.BuiltinName == "max" && cmp >= 0) {
			return lv.clone(), okSig()
		}
		return rv.clone(), okSig()

	case "variant_index":
		ev, sig := s.evalExpr(call.Args[0])
		if sig.kind != sigNone {
			return nil, sig
		}
		ev, sig = s.derefCtVal(ev, call.Args[0].GetSpan())
		if sig.kind != sigNone {
			return nil, sig
		}
		if ev == nil || ev.kind != ctEnum {
			return nil, abortSig(call.Span, "`variant_index` requires an enum or result value at compile time")
		}
		return newCtSignedInt(int64(ev.variantIdx), types.TypeInt), okSig()

	case "make_variant":
		en := inf.EnumType
		vr := inf.EnumVariant
		idx := inf.TupleFieldIdx
		if en == nil || vr == nil {
			return nil, abortSig(call.Span, "unresolved enum or variant in `make_variant`")
		}
		var payload *ctVal
		if vr.Kind == types.VariantTuple && vr.Payload != nil && len(call.Args) >= 2 {
			av, sig := s.evalCallArg(call.Args[1], types.ModeOwn)
			if sig.kind != sigNone {
				return nil, sig
			}
			pv, sig := av.getVal(call.Args[1].GetSpan())
			if sig.kind != sigNone {
				return nil, sig
			}
			payload = pv.clone().wrapResult(vr.Payload)
		}
		return &ctVal{kind: ctEnum, ty: en, en: en, variantIdx: idx, payload: payload}, okSig()

	default:
		return nil, abortSig(call.Span, "host I/O / OS builtin `%s` cannot run at compile time", inf.BuiltinName)
	}
}

func (s *ctState) evalTypeConvertCall(call *ast.CallExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	av, sig := s.evalExpr(call.Args[0])
	if sig.kind != sigNone {
		return nil, sig
	}
	target := inf.Type
	if dist, ok := target.(*types.Distinct); ok {
		if underlying := av.ty; underlying != nil && underlying.Equals(dist.Base) {
			cp := av.clone()
			cp.ty = dist
			return cp, okSig()
		}
		// Distinct unwrap or underlying cast
		cp := av.clone()
		cp.ty = target
		return cp, okSig()
	}
	if _, isPrim := target.(*types.Primitive); isPrim && av.ty != nil {
		if d, isDist := av.ty.(*types.Distinct); isDist && d.Base.Equals(target) {
			cp := av.clone()
			cp.ty = target
			return cp, okSig()
		}
	}

	p := types.UnderlyingPrimitive(target)
	if p == nil {
		return av.clone(), okSig()
	}
	if p.Kind == types.PrimChar {
		var code int64
		switch av.kind {
		case ctInt:
			if av.prim != nil && !av.prim.IsSignedInt() {
				u := av.asUint64()
				if u > utf8.MaxRune {
					return nil, abortSig(call.Span, "invalid char code point %d at compile time", u)
				}
				code = int64(u)
			} else {
				code = av.asInt64()
			}
		case ctChar:
			return av.clone(), okSig()
		default:
			return nil, abortSig(call.Span, "cannot convert %v to `char`", av.ty)
		}
		if code < 0 || code > utf8.MaxRune || (code >= 0xD800 && code <= 0xDFFF) {
			return nil, abortSig(call.Span, "invalid char code point %d at compile time", code)
		}
		return newCtChar(rune(code)), okSig()
	}

	if p.IsFloat() {
		var f float64
		switch av.kind {
		case ctFloat:
			f = av.f
		case ctInt:
			if av.prim != nil && !av.prim.IsSignedInt() {
				f = float64(av.asUint64())
			} else {
				f = float64(av.asInt64())
			}
		default:
			return nil, abortSig(call.Span, "cannot convert %v to float", av.ty)
		}
		return newCtFloat(f, p), okSig()
	}

	if p.IsInteger() {
		switch av.kind {
		case ctInt:
			if p.IsSignedInt() && (av.prim == nil || av.prim.IsSignedInt()) {
				return newCtSignedInt(av.asInt64(), p), okSig()
			}
			return newCtInt(av.asUint64(), p), okSig()
		case ctFloat:
			if p.IsSignedInt() {
				return newCtSignedInt(int64(av.f), p), okSig()
			}
			return newCtInt(uint64(av.f), p), okSig()
		case ctChar:
			return newCtSignedInt(int64(av.ch), p), okSig()
		case ctEnum:
			return newCtSignedInt(av.asInt64(), p), okSig()
		case ctBool:
			if av.b {
				return newCtSignedInt(1, p), okSig()
			}
			return newCtSignedInt(0, p), okSig()
		}
	}
	return av.clone(), okSig()
}

func (s *ctState) formatFloat(val float64, prim *types.Primitive, span diag.Span) (string, ctSignal) {
	if prim != nil && prim.Kind == types.PrimF32 {
		val = float64(float32(val))
	}
	if s.a != nil && s.a.coreMod != nil {
		sym := s.a.coreMod.Symbols["format_float_prec"]
		if (sym == nil || sym.FuncInst == nil) && s.a.coreMod.Pkg != nil {
			sym = s.a.coreMod.Pkg.Symbols["format_float_prec"]
		}
		if sym != nil && sym.FuncInst != nil {
			res, sig := s.callFuncInstance(sym.FuncInst, valArgs(newCtFloat(val, types.TypeFloat), newCtSignedInt(-1, types.TypeInt)), span)
			if sig.kind != sigNone {
				return "", sig
			}
			if res != nil && res.kind == ctString {
				return res.s, okSig()
			}
		}
	}
	return strconv.FormatFloat(val, 'g', -1, 64), okSig()
}

func (s *ctState) valToString(v *ctVal, span diag.Span) (string, ctSignal) {
	return s.valToStringDepth(v, span, 0)
}

func (s *ctState) valToStringDepth(v *ctVal, span diag.Span, depth int) (string, ctSignal) {
	if v == nil {
		return "", okSig()
	}
	if depth >= maxComptimeValueDepth {
		return "", abortSig(span, "compile-time value nesting depth exceeded (%d)", maxComptimeValueDepth)
	}
	if v.kind == ctFloat {
		if _, isDist := v.ty.(*types.Distinct); isDist {
			if m := s.a.LookupMethod(v.ty, "to_string", nil, span); m != nil && s.isCallable(m) {
				res, sig := s.callFuncInstance(m, valArgs(v), span)
				if sig.kind != sigNone {
					return "", sig
				}
				if res != nil && res.kind == ctString {
					return res.s, okSig()
				}
			}
		}
		return s.formatFloat(v.f, v.prim, span)
	}
	if v.ty != nil {
		if m := s.a.LookupMethod(v.ty, "to_string", nil, span); m != nil && s.isCallable(m) {
			res, sig := s.callFuncInstance(m, valArgs(v), span)
			if sig.kind != sigNone {
				return "", sig
			}
			if res != nil && res.kind == ctString {
				return res.s, okSig()
			}
		}
	}
	switch v.kind {
	case ctOpt:
		if !v.hasOpt {
			return "none", okSig()
		}
		return s.valToStringDepth(v.optElem, span, depth+1)
	case ctEnum:
		if v.en != nil && v.variantIdx >= 0 && v.variantIdx < len(v.en.Variants) {
			vr := v.en.Variants[v.variantIdx]
			if v.payload != nil {
				ps, sig := s.valToStringDepth(v.payload, span, depth+1)
				if sig.kind != sigNone {
					return "", sig
				}
				return fmt.Sprintf("%s.%s(%s)", v.en.Name, vr.Name, ps), okSig()
			}
			return v.en.Name + "." + vr.Name, okSig()
		}
	case ctList:
		parts := make([]string, len(v.list.elems))
		for i, el := range v.list.elems {
			ps, sig := s.valToStringDepth(el, span, depth+1)
			if sig.kind != sigNone {
				return "", sig
			}
			parts[i] = ps
		}
		return "[" + strings.Join(parts, ", ") + "]", okSig()
	case ctArray:
		parts := make([]string, len(v.arr.elems))
		for i, el := range v.arr.elems {
			ps, sig := s.valToStringDepth(el, span, depth+1)
			if sig.kind != sigNone {
				return "", sig
			}
			parts[i] = ps
		}
		return "[" + strings.Join(parts, ", ") + "]", okSig()
	case ctTuple:
		parts := make([]string, len(v.tup.elems))
		for i, el := range v.tup.elems {
			ps, sig := s.valToStringDepth(el, span, depth+1)
			if sig.kind != sigNone {
				return "", sig
			}
			parts[i] = ps
		}
		return "+[" + strings.Join(parts, ", ") + "]", okSig()
	case ctStruct:
		parts := make([]string, 0, len(v.st.fieldOrder))
		for _, k := range v.st.fieldOrder {
			fs, sig := s.valToStringDepth(v.st.fields[k], span, depth+1)
			if sig.kind != sigNone {
				return "", sig
			}
			parts = append(parts, k+": "+fs)
		}
		return v.st.ty.Name + "[" + strings.Join(parts, ", ") + "]", okSig()
	case ctSet:
		parts := make([]string, len(v.set.elems))
		for i, el := range v.set.elems {
			ps, sig := s.valToStringDepth(el, span, depth+1)
			if sig.kind != sigNone {
				return "", sig
			}
			parts[i] = ps
		}
		return "#[" + strings.Join(parts, ", ") + "]", okSig()
	case ctMap:
		parts := make([]string, len(v.m.entries))
		for i, e := range v.m.entries {
			ks, sig := s.valToStringDepth(e.key, span, depth+1)
			if sig.kind != sigNone {
				return "", sig
			}
			vs, sig := s.valToStringDepth(e.val, span, depth+1)
			if sig.kind != sigNone {
				return "", sig
			}
			parts[i] = ks + ": " + vs
		}
		return "#[" + strings.Join(parts, ", ") + "]", okSig()
	}
	return v.toString(), okSig()
}

func (s *ctState) evalBuiltinMethodCall(call *ast.CallExpr, inf *ExprInfo) (*ctVal, ctSignal) {
	if inf.MethodRecvMode == types.ModeMut {
		pl, sig := s.evalPlace(inf.MethodRecv)
		if sig.kind != sigNone {
			return nil, sig
		}
		rv, sig := pl.get()
		if sig.kind != sigNone {
			return nil, sig
		}
		switch inf.BuiltinName {
		case "vec.push":
			av, sig := s.evalCallArg(call.Args[0], types.ModeOwn)
			if sig.kind != sigNone {
				return nil, sig
			}
			val, sig := av.getVal(call.Span)
			if sig.kind != sigNone {
				return nil, sig
			}
			rv.list.elems = append(rv.list.elems, val)
			if sig := s.allocMem(ctValMemCost, call.Span); sig.kind != sigNone {
				return nil, sig
			}
			return newCtVoid(), okSig()
		case "vec.pop":
			if len(rv.list.elems) == 0 {
				return newCtNone(inf.Type), okSig()
			}
			last := rv.list.elems[len(rv.list.elems)-1]
			rv.list.elems = rv.list.elems[:len(rv.list.elems)-1]
			return newCtSome(inf.Type, last), okSig()
		case "vec.clear":
			rv.list.elems = rv.list.elems[:0]
			return newCtVoid(), okSig()
		case "vec.reserve":
			_, sig := s.evalExpr(call.Args[0])
			return newCtVoid(), sig
		}
		return nil, abortSig(call.Span, "unsupported mutating builtin method %q", inf.BuiltinName)
	}

	rv, sig := s.evalExpr(inf.MethodRecv)
	if sig.kind != sigNone {
		return nil, sig
	}
	if inf.IsOptChain && rv.kind == ctOpt {
		if !rv.hasOpt {
			return newCtNone(inf.Type), okSig()
		}
		rv = rv.optElem
	}

	var res *ctVal
	switch inf.BuiltinName {
	case "any.copy":
		res = rv.clone()
	case "any.to_string":
		str, sig := s.valToString(rv, call.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
		res = newCtString(str)
	case "any.hash":
		res = newCtInt(rv.hash(), types.TypeU64)
	case "float.to_bits":
		res = newCtInt(math.Float64bits(rv.f), types.TypeU64)
	case "f32.to_bits":
		res = newCtInt(uint64(math.Float32bits(float32(rv.f))), types.TypeU32)
	case "string.len":
		res = newCtSignedInt(int64(len(rv.s)), types.TypeInt)
	case "string.byte_at":
		iv, s1 := s.evalExpr(call.Args[0])
		if s1.kind != sigNone {
			return nil, s1
		}
		idx := iv.asInt64()
		if idx < 0 || idx >= int64(len(rv.s)) {
			return nil, abortSig(call.Span, "compile-time `string.byte_at` index %d out of bounds (len %d)", idx, len(rv.s))
		}
		res = newCtInt(uint64(rv.s[idx]), types.TypeU8)
	case "vec.len":
		res = newCtSignedInt(int64(len(rv.list.elems)), types.TypeInt)
	case "vec.cap":
		res = newCtSignedInt(int64(cap(rv.list.elems)), types.TypeInt)
	case "vec.is_empty":
		res = newCtBool(len(rv.list.elems) == 0)
	case "vec.get":
		iv, s1 := s.evalExpr(call.Args[0])
		if s1.kind != sigNone {
			return nil, s1
		}
		idx := iv.asInt64()
		if idx < 0 || idx >= int64(len(rv.list.elems)) {
			return newCtNone(inf.Type), okSig()
		}
		res = newCtSome(inf.Type, rv.list.elems[idx].clone())
	case "vec.contains":
		needle, s1 := s.evalExpr(call.Args[0])
		if s1.kind != sigNone {
			return nil, s1
		}
		found := false
		for _, el := range rv.list.elems {
			if el.equals(s.a, needle) {
				found = true
				break
			}
		}
		res = newCtBool(found)
	case "vec.join":
		sep, s1 := s.evalExpr(call.Args[0])
		if s1.kind != sigNone {
			return nil, s1
		}
		parts := make([]string, len(rv.list.elems))
		for i, el := range rv.list.elems {
			parts[i] = el.s
		}
		res = newCtString(strings.Join(parts, sep.s))
	case "arr.len":
		res = newCtSignedInt(int64(len(rv.arr.elems)), types.TypeInt)
	case "arr.is_empty":
		res = newCtBool(len(rv.arr.elems) == 0)
	case "arr.get":
		iv, s1 := s.evalExpr(call.Args[0])
		if s1.kind != sigNone {
			return nil, s1
		}
		idx := iv.asInt64()
		if idx < 0 || idx >= int64(len(rv.arr.elems)) {
			return newCtNone(inf.Type), okSig()
		}
		res = newCtSome(inf.Type, rv.arr.elems[idx].clone())
	case "arr.contains":
		needle, s1 := s.evalExpr(call.Args[0])
		if s1.kind != sigNone {
			return nil, s1
		}
		found := false
		for _, el := range rv.arr.elems {
			if el.equals(s.a, needle) {
				found = true
				break
			}
		}
		res = newCtBool(found)
	case "comp_field.has_attr", "comp_variant.has_attr":
		nv, s1 := s.evalExpr(call.Args[0])
		if s1.kind != sigNone {
			return nil, s1
		}
		_, ok := findCompAttrByName(rv, nv.s)
		res = newCtBool(ok)
	case "comp_field.attr", "comp_variant.attr":
		nv, s1 := s.evalExpr(call.Args[0])
		if s1.kind != sigNone {
			return nil, s1
		}
		if match, ok := findCompAttrByName(rv, nv.s); ok && match != nil {
			res = newCtSome(inf.Type, match.clone())
		} else {
			res = newCtNone(inf.Type)
		}
	case "comp_field.implements":
		res, sig = s.evalDescriptorImplements(rv, inf.ResolvedIface, "field", call.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
	case "comp_variant.payload_implements":
		res, sig = s.evalDescriptorImplements(rv, inf.ResolvedIface, "variant", call.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
	case "comp_elem.implements":
		res, sig = s.evalDescriptorImplements(rv, inf.ResolvedIface, "elem", call.Span)
		if sig.kind != sigNone {
			return nil, sig
		}
	case "struct.field":
		dv, s1 := s.derefCtVal(rv, call.Span)
		if s1.kind != sigNone {
			return nil, s1
		}
		res, s1 = s.readStructFieldCtVal(dv, inf.ResolvedField, inf.Type, call.Span)
		if s1.kind != sigNone {
			return nil, s1
		}
	case "seq.elem":
		dv, s1 := s.derefCtVal(rv, call.Span)
		if s1.kind != sigNone {
			return nil, s1
		}
		res, s1 = s.readSeqElemCtVal(dv, inf.TupleFieldIdx, call.Span)
		if s1.kind != sigNone {
			return nil, s1
		}
	case "enum.is_variant":
		dv, s1 := s.derefCtVal(rv, call.Span)
		if s1.kind != sigNone {
			return nil, s1
		}
		res = newCtBool(dv != nil && dv.kind == ctEnum && dv.variantIdx == inf.TupleFieldIdx)
	case "enum.payload":
		dv, s1 := s.derefCtVal(rv, call.Span)
		if s1.kind != sigNone {
			return nil, s1
		}
		res, s1 = s.readEnumPayloadCtVal(dv, inf.TupleFieldIdx, inf.EnumVariant, call.Span)
		if s1.kind != sigNone {
			return nil, s1
		}
	case "enum.as_variant":
		dv, s1 := s.derefCtVal(rv, call.Span)
		if s1.kind != sigNone {
			return nil, s1
		}
		if dv != nil && dv.kind == ctEnum && dv.variantIdx == inf.TupleFieldIdx {
			res = newCtSome(inf.Type, dv.payload.clone())
		} else {
			res = newCtNone(inf.Type)
		}
	default:
		return nil, abortSig(call.Span, "unsupported builtin method %q at compile time", inf.BuiltinName)
	}
	if inf.IsOptChain && inf.Type != nil && res != nil && res.kind != ctOpt {
		return newCtSome(inf.Type, res), okSig()
	}
	return res, okSig()
}

func findCompAttrByName(desc *ctVal, name string) (*ctVal, bool) {
	if desc == nil || desc.kind != ctStruct || desc.st == nil {
		return nil, false
	}
	attrsVal := desc.st.fields["attrs"]
	if attrsVal == nil {
		return nil, false
	}
	var attrs []*ctVal
	if elems, ok := listStructElems(attrsVal); ok {
		attrs = elems
	} else if attrsVal.kind == ctList && attrsVal.list != nil {
		attrs = attrsVal.list.elems
	}
	for _, a := range attrs {
		if a != nil && a.kind == ctStruct && a.st != nil {
			if nv := a.st.fields["name"]; nv != nil && nv.s == name {
				return a, true
			}
		}
	}
	return nil, false
}

func (s *ctState) evalDescriptorImplements(rv *ctVal, ifc *types.Interface, expectKind string, span diag.Span) (*ctVal, ctSignal) {
	if ifc == nil {
		return newCtBool(false), okSig()
	}
	if rv == nil || rv.kind != ctStruct || rv.st == nil || rv.st.refl == nil || rv.st.refl.TargetType == nil {
		return nil, abortSig(span, "descriptor value is missing compiler reflection metadata (use values from `comp.*_of[T]()`)")
	}
	if rv.st.refl.Kind != expectKind {
		return nil, abortSig(span, "invalid descriptor kind %q (expected %q)", rv.st.refl.Kind, expectKind)
	}
	targetTy := rv.st.refl.TargetType
	idx := rv.st.refl.Index
	var memberTy types.Type
	switch expectKind {
	case "field":
		st, ok := targetTy.(*types.Struct)
		if !ok || idx < 0 || idx >= len(st.Fields) {
			return nil, abortSig(span, "invalid `FieldInfo` index %d for type %s", idx, targetTy.String())
		}
		memberTy = st.Fields[idx].Type
	case "variant":
		en, ok := targetTy.(*types.Enum)
		if !ok || idx < 0 || idx >= len(en.Variants) {
			return nil, abortSig(span, "invalid `VariantInfo` index %d for type %s", idx, targetTy.String())
		}
		vr := en.Variants[idx]
		if vr.Kind != types.VariantTuple || vr.Payload == nil {
			return newCtBool(false), okSig()
		}
		memberTy = vr.Payload
	case "elem":
		switch t := targetTy.(type) {
		case *types.Tuple:
			if idx < 0 || idx >= len(t.Elems) {
				return nil, abortSig(span, "invalid `ElemInfo` index %d for tuple %s", idx, targetTy.String())
			}
			memberTy = t.Elems[idx]
		case *types.StackArray:
			if int64(idx) < 0 || int64(idx) >= t.Size {
				return nil, abortSig(span, "invalid `ElemInfo` index %d for array %s", idx, targetTy.String())
			}
			memberTy = t.Elem
		default:
			return nil, abortSig(span, "invalid `ElemInfo` target type %s", targetTy.String())
		}
	}
	return newCtBool(s.a.ImplementsInterface(memberTy, ifc)), okSig()
}

func (s *ctState) evalIndexGet(bv, iv *ctVal, span diag.Span) (*ctVal, ctSignal) {
	if bv == nil {
		return nil, abortSig(span, "nil container in compile-time index")
	}
	switch bv.kind {
	case ctStruct:
		if bs, _, ok := listStructBytes(bv); ok {
			idx := iv.asInt64()
			if idx < 0 || idx >= int64(len(bs)) {
				return nil, abortSig(span, "compile-time list index %d out of bounds (len %d)", idx, len(bs))
			}
			return newCtInt(uint64(bs[idx]), types.TypeU8), okSig()
		}
		if elems, ok := listStructElems(bv); ok {
			idx := iv.asInt64()
			if idx < 0 || idx >= int64(len(elems)) {
				return nil, abortSig(span, "compile-time list index %d out of bounds (len %d)", idx, len(elems))
			}
			return elems[idx], okSig()
		}
		if bv.st != nil && bv.st.ty != nil && bv.st.ty.Lang == "map" {
			if getFn := s.a.LookupMethod(bv.ty, "get", nil, span); getFn != nil {
				cur := bv
				pl := ctPlace{
					get: func() (*ctVal, ctSignal) { return cur, okSig() },
					set: func(nv *ctVal) ctSignal { cur = nv; return okSig() },
				}
				return s.callFuncInstance(getFn, []ctArg{{place: &pl}, {val: iv}}, span)
			}
		}
	case ctPointer:
		idx := iv.asInt64()
		if bv.ptrVal == nil {
			return nil, abortSig(span, "null pointer index at compile time")
		}
		ep := *bv.ptrVal
		ep.byteOff += idx * ep.elemSize
		if idx != 0 {
			ep.place = nil
		}
		dp, sig := s.ptrDerefPlace(&ep, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return dp.get()
	case ctList:
		idx := iv.asInt64()
		if idx < 0 || idx >= int64(len(bv.list.elems)) {
			return nil, abortSig(span, "compile-time list index %d out of bounds (len %d)", idx, len(bv.list.elems))
		}
		return bv.list.elems[idx], okSig()
	case ctArray:
		idx := iv.asInt64()
		if idx < 0 || idx >= int64(len(bv.arr.elems)) {
			return nil, abortSig(span, "compile-time array index %d out of bounds (len %d)", idx, len(bv.arr.elems))
		}
		return bv.arr.elems[idx], okSig()
	case ctString:
		idx := iv.asInt64()
		if idx < 0 || idx >= int64(len(bv.s)) {
			return nil, abortSig(span, "compile-time string byte index %d out of bounds (len %d)", idx, len(bv.s))
		}
		return newCtInt(uint64(bv.s[idx]), types.TypeU8), okSig()
	case ctMap:
		for _, ent := range bv.m.entries {
			if ent.key.equals(s.a, iv) {
				return newCtSome(types.NewOptional(ent.val.ty), ent.val.clone()), okSig()
			}
		}
		return newCtNone(nil), okSig()
	}
	return nil, abortSig(span, "cannot index value of type %v at compile time", bv.ty)
}

func (s *ctState) ensureListStructCoW(bv *ctVal, span diag.Span) ctSignal {
	if bv == nil || bv.kind != ctStruct || bv.st == nil || bv.st.ty == nil || bv.st.ty.Lang != "list" {
		return okSig()
	}
	capV := bv.st.fields["cap"]
	dataV := bv.st.fields["data"]
	var curAl *ctHeapAlloc
	if dataV != nil && dataV.kind == ctPointer && dataV.ptrVal != nil {
		curAl = dataV.ptrVal.alloc
	}
	if capV != nil && capV.asInt64() > 0 && curAl != nil && !curAl.freed && !curAl.readOnly {
		return okSig()
	}
	lenV := bv.st.fields["len"]
	var n int64
	if lenV != nil {
		n = lenV.asInt64()
	}
	if n <= 0 {
		return okSig()
	}
	var elemTy types.Type
	if len(bv.st.ty.TypeArgs) == 1 {
		elemTy = bv.st.ty.TypeArgs[0]
	}
	if elemTy == nil {
		return abortSig(span, "missing `List` element type in compile-time index assignment")
	}
	if bs, _, ok := listStructBytes(bv); ok {
		buf := make([]byte, len(bs))
		copy(buf, bs)
		al, sig := s.newBytesAlloc(buf, false, span)
		if sig.kind != sigNone {
			return sig
		}
		bv.st.fields["data"] = &ctVal{kind: ctPointer, ty: &types.Ptr{Elem: elemTy}, ptrVal: &ctPtr{alloc: al, elemTy: elemTy, elemSize: 1}}
		bv.st.fields["cap"] = newCtSignedInt(n, types.TypeInt)
		return okSig()
	}
	elems, ok := listStructElems(bv)
	if !ok {
		return abortSig(span, "invalid `List` backing storage in compile-time index assignment")
	}
	al, sig := s.newHeapAlloc(elemTy, n, false, span)
	if sig.kind != sigNone {
		return sig
	}
	if al.isBytes {
		for i, el := range elems {
			al.bytes[i] = byte(el.asUint64())
		}
	} else {
		for i, el := range elems {
			al.cells[i] = el.clone()
		}
	}
	bv.st.fields["data"] = &ctVal{kind: ctPointer, ty: &types.Ptr{Elem: elemTy}, ptrVal: &ctPtr{alloc: al, elemTy: elemTy, elemSize: al.elemSize}}
	bv.st.fields["cap"] = newCtSignedInt(n, types.TypeInt)
	return okSig()
}

func (s *ctState) evalIndexSet(bv, iv, nv *ctVal, span diag.Span) ctSignal {
	if bv == nil {
		return abortSig(span, "nil container in compile-time index assignment")
	}
	switch bv.kind {
	case ctStruct:
		if bv.st != nil && bv.st.ty != nil && bv.st.ty.Lang == "list" {
			lenV := bv.st.fields["len"]
			var n int64
			if lenV != nil {
				n = lenV.asInt64()
			}
			idx := iv.asInt64()
			if idx < 0 || idx >= n {
				return abortSig(span, "compile-time list index %d out of bounds (len %d)", idx, n)
			}
			if sig := s.ensureListStructCoW(bv, span); sig.kind != sigNone {
				return sig
			}
			if bs, al, ok := listStructBytes(bv); ok {
				if al != nil && al.readOnly {
					return abortSig(span, "cannot mutate read-only compile-time heap memory")
				}
				bs[idx] = byte(nv.asUint64())
				return okSig()
			}
			if elems, ok := listStructElems(bv); ok {
				elems[idx] = nv
				return okSig()
			}
		}
	case ctPointer:
		idx := iv.asInt64()
		if bv.ptrVal == nil {
			return abortSig(span, "null pointer index assignment at compile time")
		}
		ep := *bv.ptrVal
		ep.byteOff += idx * ep.elemSize
		if idx != 0 {
			ep.place = nil
		}
		dp, sig := s.ptrDerefPlace(&ep, span)
		if sig.kind != sigNone {
			return sig
		}
		return dp.set(nv)
	case ctList:
		idx := iv.asInt64()
		if idx < 0 || idx >= int64(len(bv.list.elems)) {
			return abortSig(span, "compile-time list index %d out of bounds (len %d)", idx, len(bv.list.elems))
		}
		bv.list.elems[idx] = nv
		return okSig()
	case ctArray:
		idx := iv.asInt64()
		if idx < 0 || idx >= int64(len(bv.arr.elems)) {
			return abortSig(span, "compile-time array index %d out of bounds (len %d)", idx, len(bv.arr.elems))
		}
		bv.arr.elems[idx] = nv
		return okSig()
	}
	return abortSig(span, "cannot assign index on type %v at compile time", bv.ty)
}

func (s *ctState) evalSlice(bv *ctVal, loPtr, hiPtr *int64, inclusive, borrowAsView bool, expectedTy types.Type, span diag.Span) (*ctVal, ctSignal) {
	var total int64
	switch bv.kind {
	case ctString:
		total = int64(len(bv.s))
	case ctList:
		total = int64(len(bv.list.elems))
	case ctArray:
		total = int64(len(bv.arr.elems))
	case ctStruct:
		if bs, _, ok := listStructBytes(bv); ok {
			total = int64(len(bs))
		} else if elems, ok := listStructElems(bv); ok {
			total = int64(len(elems))
		} else {
			return nil, abortSig(span, "cannot slice value of type %v at compile time", bv.ty)
		}
	default:
		return nil, abortSig(span, "cannot slice value of type %v at compile time", bv.ty)
	}
	lo := int64(0)
	if loPtr != nil {
		lo = *loPtr
	}
	hi := total
	if hiPtr != nil {
		hi = *hiPtr
		if inclusive {
			hi++
		}
	}
	if lo < 0 || hi < lo || hi > total {
		return nil, abortSig(span, "compile-time slice range [%d..%d] out of bounds (len %d)", lo, hi, total)
	}
	switch bv.kind {
	case ctString:
		sub := bv.s[lo:hi]
		if borrowAsView || hi == lo {
			if bv.sAlloc != nil && !bv.sAlloc.freed {
				return &ctVal{kind: ctString, ty: types.TypeString, s: sub, sAlloc: bv.sAlloc, sOff: bv.sOff + lo, sCap: 0}, okSig()
			}
			return newCtString(sub), okSig()
		}
		buf := make([]byte, len(sub)+1)
		copy(buf, sub)
		al, sig := s.newBytesAlloc(buf, false, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		return &ctVal{kind: ctString, ty: types.TypeString, s: sub, sAlloc: al, sOff: 0, sCap: int64(len(buf))}, okSig()
	case ctList:
		if sig := s.allocMem((hi-lo)*ctValMemCost, span); sig.kind != sigNone {
			return nil, sig
		}
		elems := make([]*ctVal, hi-lo)
		for i := lo; i < hi; i++ {
			elems[i-lo] = bv.list.elems[i].clone()
		}
		return &ctVal{kind: ctList, ty: expectedTy, list: &ctListBox{elemTy: bv.list.elemTy, elems: elems}}, okSig()
	case ctArray:
		if sig := s.allocMem((hi-lo)*ctValMemCost, span); sig.kind != sigNone {
			return nil, sig
		}
		elems := make([]*ctVal, hi-lo)
		for i := lo; i < hi; i++ {
			elems[i-lo] = bv.arr.elems[i].clone()
		}
		lv, sig := s.makeListStruct(expectedTy, elems, span)
		if sig.kind == sigNone && borrowAsView && lv != nil && lv.kind == ctStruct && lv.st != nil {
			lv.st.fields["cap"] = newCtSignedInt(0, types.TypeInt)
		}
		return lv, sig
	case ctStruct:
		if borrowAsView && bv.st != nil && bv.st.ty != nil && bv.st.ty.Lang == "list" && len(bv.st.ty.TypeArgs) == 1 {
			elemTy := bv.st.ty.TypeArgs[0]
			var slicePtr *ctPtr
			if dv := bv.st.fields["data"]; dv != nil && dv.kind == ctPointer && dv.ptrVal != nil {
				p := *dv.ptrVal
				p.byteOff += lo * p.elemSize
				p.place = nil
				slicePtr = &p
			} else {
				slicePtr = &ctPtr{elemTy: elemTy, elemSize: s.simElemSize(elemTy)}
			}
			fields := map[string]*ctVal{
				"data": {kind: ctPointer, ty: &types.Ptr{Elem: elemTy}, ptrVal: slicePtr},
				"len":  newCtSignedInt(hi-lo, types.TypeInt),
				"cap":  newCtSignedInt(0, types.TypeInt),
			}
			return &ctVal{
				kind: ctStruct,
				ty:   bv.ty,
				st:   &ctStructBox{ty: bv.st.ty, fields: fields, fieldOrder: []string{"data", "len", "cap"}},
			}, okSig()
		}
		if bs, _, ok := listStructBytes(bv); ok {
			return s.makeByteListStruct(bs[lo:hi], span)
		}
		elems, _ := listStructElems(bv)
		out := make([]*ctVal, hi-lo)
		for i := lo; i < hi; i++ {
			out[i-lo] = elems[i].clone()
		}
		return s.makeListStruct(expectedTy, out, span)
	}
	return nil, okSig()
}

func (s *ctState) evalContains(cv, needle *ctVal, span diag.Span) (bool, ctSignal) {
	switch cv.kind {
	case ctStruct:
		if cv.st != nil && cv.st.ty != nil {
			methodName := ""
			switch cv.st.ty.Lang {
			case "list", "set":
				methodName = "contains"
			case "map":
				methodName = "contains_key"
			}
			if methodName != "" {
				if fn := s.a.LookupMethod(cv.ty, methodName, nil, span); fn != nil {
					res, sig := s.callFuncInstance(fn, valArgs(cv, needle), span)
					if sig.kind != sigNone {
						return false, sig
					}
					return res != nil && res.b, okSig()
				}
			}
		}
	case ctList:
		for _, el := range cv.list.elems {
			if el.equals(s.a, needle) {
				return true, okSig()
			}
		}
		return false, okSig()
	case ctArray:
		for _, el := range cv.arr.elems {
			if el.equals(s.a, needle) {
				return true, okSig()
			}
		}
		return false, okSig()
	case ctSet:
		for _, el := range cv.set.elems {
			if el.equals(s.a, needle) {
				return true, okSig()
			}
		}
		return false, okSig()
	case ctMap:
		for _, ent := range cv.m.entries {
			if ent.key.equals(s.a, needle) {
				return true, okSig()
			}
		}
		return false, okSig()
	case ctRange:
		loV := newCtInt(cv.rng.low, cv.rng.prim)
		hiV := newCtInt(cv.rng.high, cv.rng.prim)
		if cv.rng.prim == types.TypeChar {
			loV = newCtChar(rune(cv.rng.low))
			hiV = newCtChar(rune(cv.rng.high))
		}
		cLo := needle.cmpVal(loV)
		cHi := needle.cmpVal(hiV)
		if cv.rng.inclusive {
			return cLo >= 0 && cHi <= 0, okSig()
		}
		return cLo >= 0 && cHi < 0, okSig()
	}
	return false, abortSig(span, "unsupported `in` container at compile time")
}

// -----------------------------------------------------------------------------
// Compile-Time Value Export for MIR Static Baking
// -----------------------------------------------------------------------------

type StaticKind int

const (
	StaticVoid StaticKind = iota
	StaticInt
	StaticFloat
	StaticBool
	StaticChar
	StaticString
	StaticList
	StaticArray
	StaticTuple
	StaticStruct
	StaticEnum
	StaticOpt
	StaticFn
)

// StaticData is an immutable, MIR-ready projection of a validated compile-time value
// that crossed into runtime code.
type StaticData struct {
	Kind        StaticKind
	Type        types.Type
	Uint        uint64
	SignedVal   int64
	IsSigned    bool
	Float       float64
	Bool        bool
	Char        rune
	Str         string
	HasRawBytes bool
	RawBytes    []byte
	Elems       []*StaticData
	Fields     map[string]*StaticData
	VariantIdx int
	Payload    *StaticData
	HasOpt     bool
	OptElem    *StaticData
	Fn         *FuncInstance
	Lambda     *LambdaInfo
}

// ExportStaticData converts a `*ctVal` stored on a Symbol or `BodyMaps` into `*StaticData`.
func ExportStaticData(raw any, expectedTy types.Type) *StaticData {
	cv, ok := raw.(*ctVal)
	if !ok || cv == nil {
		return nil
	}
	return exportCtVal(cv, expectedTy)
}

func exportCtVal(cv *ctVal, expectedTy types.Type) *StaticData {
	if cv == nil {
		return nil
	}
	ty := expectedTy
	if ty == nil {
		ty = cv.ty
	}

	// Unwrap Distinct wrapper to match underlying value while preserving `ty` for CType/mangling.
	effTy := ty
	for {
		if d, isDist := effTy.(*types.Distinct); isDist {
			effTy = d.Base
		} else {
			break
		}
	}

	if optTy, isOpt := effTy.(*types.Optional); isOpt {
		if cv.kind == ctOpt {
			if !cv.hasOpt {
				return &StaticData{Kind: StaticOpt, Type: ty, HasOpt: false}
			}
			return &StaticData{
				Kind:    StaticOpt,
				Type:    ty,
				HasOpt:  true,
				OptElem: exportCtVal(cv.optElem, optTy.Elem),
			}
		}
		return &StaticData{
			Kind:    StaticOpt,
			Type:    ty,
			HasOpt:  true,
			OptElem: exportCtVal(cv, optTy.Elem),
		}
	}

	switch cv.kind {
	case ctVoid:
		return &StaticData{Kind: StaticVoid, Type: ty}
	case ctInt:
		signed := false
		if cv.prim != nil && cv.prim.IsSignedInt() {
			signed = true
		} else if p := types.UnderlyingPrimitive(effTy); p != nil && p.IsSignedInt() {
			signed = true
		}
		return &StaticData{
			Kind:      StaticInt,
			Type:      ty,
			Uint:      cv.asUint64(),
			SignedVal: cv.asInt64(),
			IsSigned:  signed,
		}
	case ctFloat:
		return &StaticData{Kind: StaticFloat, Type: ty, Float: cv.f}
	case ctBool:
		return &StaticData{Kind: StaticBool, Type: ty, Bool: cv.b}
	case ctChar:
		return &StaticData{Kind: StaticChar, Type: ty, Char: cv.ch}
	case ctString:
		return &StaticData{Kind: StaticString, Type: ty, Str: cv.s}
	case ctList:
		var elemTy types.Type
		switch ct := effTy.(type) {
		case *types.Vector:
			elemTy = ct.Elem
		case *types.Struct:
			if len(ct.TypeArgs) == 1 {
				elemTy = ct.TypeArgs[0]
			}
		}
		if elemTy == nil && cv.list != nil {
			elemTy = cv.list.elemTy
		}
		var elems []*StaticData
		if cv.list != nil {
			elems = make([]*StaticData, len(cv.list.elems))
			for i, el := range cv.list.elems {
				elems[i] = exportCtVal(el, elemTy)
			}
		}
		return &StaticData{Kind: StaticList, Type: ty, Elems: elems}
	case ctArray:
		var elemTy types.Type
		if arr, ok := effTy.(*types.StackArray); ok {
			elemTy = arr.Elem
		} else if cv.arr != nil {
			elemTy = cv.arr.elemTy
		}
		var elems []*StaticData
		if cv.arr != nil {
			elems = make([]*StaticData, len(cv.arr.elems))
			for i, el := range cv.arr.elems {
				elems[i] = exportCtVal(el, elemTy)
			}
		}
		return &StaticData{Kind: StaticArray, Type: ty, Elems: elems}
	case ctTuple:
		var tup *types.Tuple
		if t, ok := effTy.(*types.Tuple); ok {
			tup = t
		}
		var elems []*StaticData
		if cv.tup != nil {
			elems = make([]*StaticData, len(cv.tup.elems))
			for i, el := range cv.tup.elems {
				var elTy types.Type
				if tup != nil && i < len(tup.Elems) {
					elTy = tup.Elems[i]
				}
				elems[i] = exportCtVal(el, elTy)
			}
		}
		return &StaticData{Kind: StaticTuple, Type: ty, Elems: elems}
	case ctStruct:
		st, _ := effTy.(*types.Struct)
		if st == nil && cv.st != nil {
			st = cv.st.ty
		}
		if st != nil && st.Lang == "list" && cv.st != nil {
			if bs, _, ok := listStructBytes(cv); ok {
				raw := make([]byte, len(bs))
				copy(raw, bs)
				return &StaticData{Kind: StaticList, Type: ty, HasRawBytes: true, RawBytes: raw}
			}
			var elemTy types.Type
			if len(st.TypeArgs) == 1 {
				elemTy = st.TypeArgs[0]
			} else if vec, ok := effTy.(*types.Vector); ok {
				elemTy = vec.Elem
			}
			rawElems, _ := listStructElems(cv)
			elems := make([]*StaticData, len(rawElems))
			for i, el := range rawElems {
				elems[i] = exportCtVal(el, elemTy)
			}
			return &StaticData{Kind: StaticList, Type: ty, Elems: elems}
		}
		fields := make(map[string]*StaticData)
		if st != nil && cv.st != nil {
			for _, f := range st.Fields {
				fields[f.Name] = exportCtVal(cv.st.fields[f.Name], f.Type)
			}
		}
		return &StaticData{Kind: StaticStruct, Type: ty, Fields: fields}
	case ctRange:
		if st, ok := effTy.(*types.Struct); ok && cv.rng != nil && len(st.Fields) >= 3 {
			loV := newCtInt(cv.rng.low, cv.rng.prim)
			hiV := newCtInt(cv.rng.high, cv.rng.prim)
			if cv.rng.prim == types.TypeChar {
				loV = newCtChar(rune(cv.rng.low))
				hiV = newCtChar(rune(cv.rng.high))
			}
			fields := map[string]*StaticData{
				st.Fields[0].Name: exportCtVal(loV, st.Fields[0].Type),
				st.Fields[1].Name: exportCtVal(hiV, st.Fields[1].Type),
				st.Fields[2].Name: {Kind: StaticBool, Type: types.TypeBool, Bool: cv.rng.inclusive},
			}
			return &StaticData{Kind: StaticStruct, Type: ty, Fields: fields}
		}
	case ctEnum:
		en, _ := effTy.(*types.Enum)
		if en == nil {
			en = cv.en
		}
		var payload *StaticData
		if en != nil && en.IsTagged && cv.variantIdx >= 0 && cv.variantIdx < len(en.Variants) {
			vr := en.Variants[cv.variantIdx]
			if vr.Kind == types.VariantTuple && vr.Payload != nil {
				payload = exportCtVal(cv.payload, vr.Payload)
			}
		}
		return &StaticData{
			Kind:       StaticEnum,
			Type:       ty,
			VariantIdx: cv.variantIdx,
			Payload:    payload,
		}
	case ctFn:
		return &StaticData{
			Kind:   StaticFn,
			Type:   ty,
			Fn:     cv.fn,
			Lambda: cv.lambda,
		}
	}
	return nil
}

func constValueToCtVal(cv *types.ConstValue) *ctVal {
	if cv == nil {
		return nil
	}
	if cached, ok := cv.CtVal.(*ctVal); ok && cached != nil {
		return cached.clone()
	}
	switch cv.Kind {
	case types.ConstInt:
		p, _ := cv.ConstType.(*types.Primitive)
		return newCtInt(cv.UintVal, p)
	case types.ConstBool:
		return newCtBool(cv.BoolVal)
	case types.ConstChar:
		return newCtChar(cv.RuneVal)
	case types.ConstEnum:
		return &ctVal{kind: ctEnum, ty: cv.EnumTy, en: cv.EnumTy, variantIdx: cv.VariantIdx}
	case types.ConstString:
		return newCtString(cv.StrVal)
	}
	return nil
}

func ctValToConstValue(v *ctVal, expectedTy types.Type) *types.ConstValue {
	if v == nil || expectedTy == nil {
		return nil
	}
	switch et := expectedTy.(type) {
	case *types.Primitive:
		if et.IsInteger() {
			u := maskToPrim(v.u, et)
			signed := int64(u)
			if et.IsSignedInt() {
				signed = signExtend(u, et.BitWidth())
			}
			cv := types.NewConstInt(signed, u, et)
			cv.CtVal = newCtInt(u, et)
			return cv
		}
		if et.Kind == types.PrimBool {
			cv := types.NewConstBool(v.b)
			cv.CtVal = newCtBool(v.b)
			return cv
		}
		if et.Kind == types.PrimChar {
			cv := types.NewConstChar(v.ch)
			cv.CtVal = newCtChar(v.ch)
			return cv
		}
		if et.Kind == types.PrimString {
			cv := types.NewConstString(v.s)
			cv.CtVal = newCtString(v.s)
			return cv
		}
	case *types.Enum:
		if !et.IsTagged && v.variantIdx >= 0 && v.variantIdx < len(et.Variants) {
			vr := et.Variants[v.variantIdx]
			cv := types.NewConstEnum(et, v.variantIdx, vr.Name)
			cv.CtVal = &ctVal{kind: ctEnum, ty: et, en: et, variantIdx: v.variantIdx}
			return cv
		}
	}
	return nil
}

func (s *ctState) evalCompKindOf(ty types.Type, span diag.Span) (*ctVal, ctSignal) {
	en := s.a.Prog.EnumTypeKind
	if en == nil {
		return nil, abortSig(span, "`comp.TypeKind` is not available")
	}
	variantName := ""
	switch v := ty.(type) {
	case *types.Primitive:
		if v.Kind == types.PrimString {
			variantName = "string_kind"
		} else {
			variantName = "primitive_kind"
		}
	case *types.Optional:
		variantName = "optional_kind"
	case *types.Vector:
		variantName = "list_kind"
	case *types.StackArray:
		variantName = "array_kind"
	case *types.Set:
		variantName = "set_kind"
	case *types.Map:
		variantName = "map_kind"
	case *types.Tuple:
		variantName = "tuple_kind"
	case *types.Struct:
		switch v.Lang {
		case "list":
			variantName = "list_kind"
		case "array":
			variantName = "array_kind"
		case "set":
			variantName = "set_kind"
		case "map":
			variantName = "map_kind"
		case "range":
			variantName = "range_kind"
		case "arena":
			variantName = "arena_kind"
		default:
			variantName = "struct_kind"
		}
	case *types.Enum:
		switch {
		case v.IsResult:
			variantName = "result_kind"
		case v.IsTagged:
			variantName = "tagged_union_kind"
		default:
			variantName = "tag_enum_kind"
		}
	case *types.Distinct:
		variantName = "distinct_kind"
	case *types.Interface:
		variantName = "interface_kind"
	case *types.Func:
		switch {
		case v.Closure:
			variantName = "cl_kind"
		case v.Extern:
			variantName = "extern_fn_kind"
		default:
			variantName = "fn_kind"
		}
	case *types.Ptr:
		variantName = "ptr_kind"
	case *types.Ref:
		variantName = "ref_kind"
	default:
		return nil, abortSig(span, "`comp.kind_of` unsupported type %v", ty)
	}
	for idx, vr := range en.Variants {
		if vr.Name == variantName {
			return &ctVal{kind: ctEnum, ty: en, en: en, variantIdx: idx}, okSig()
		}
	}
	return nil, abortSig(span, "missing variant %q in `comp.TypeKind`", variantName)
}

func (s *ctState) evalCompAttrList(attrs []types.Attribute, span diag.Span) (*ctVal, ctSignal) {
	attrSt := s.a.Prog.StructAttrInfo
	if attrSt == nil {
		return nil, abortSig(span, "`comp.AttrInfo` is not available")
	}
	strListTy := &types.Vector{Elem: types.TypeString}
	attrListTy := &types.Vector{Elem: attrSt}
	elems := make([]*ctVal, 0, len(attrs))
	for _, a := range attrs {
		argVals := make([]*ctVal, len(a.Args))
		for i, arg := range a.Args {
			argVals[i] = newCtString(arg)
		}
		argsList, sig := s.makeListStruct(strListTy, argVals, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		elems = append(elems, &ctVal{
			kind: ctStruct,
			ty:   attrSt,
			st: &ctStructBox{
				ty: attrSt,
				fields: map[string]*ctVal{
					"name": newCtString(a.Name),
					"args": argsList,
				},
				fieldOrder: []string{"name", "args"},
			},
		})
	}
	return s.makeListStruct(attrListTy, elems, span)
}

func (s *ctState) evalCompFieldsOf(ty types.Type, span diag.Span) (*ctVal, ctSignal) {
	fieldSt := s.a.Prog.StructFieldInfo
	st, ok := ty.(*types.Struct)
	if fieldSt == nil || !ok || !isFieldInspectableStruct(st) {
		return nil, abortSig(span, "`comp.fields_of` requires a struct type")
	}
	elems := make([]*ctVal, 0, len(st.Fields))
	for idx, f := range st.Fields {
		attrsVal, sig := s.evalCompAttrList(f.Attributes, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		_, isOpt := f.Type.(*types.Optional)
		isOpt = isOpt || strings.HasSuffix(f.Name, "?")
		isPub := IsExported(strings.TrimSuffix(f.Name, "?"))
		elems = append(elems, &ctVal{
			kind: ctStruct,
			ty:   fieldSt,
			st: &ctStructBox{
				ty: fieldSt,
				fields: map[string]*ctVal{
					"name":        newCtString(f.Name),
					"type_name":   newCtString(f.Type.String()),
					"is_opt":      newCtBool(isOpt),
					"is_public":   newCtBool(isPub),
					"has_default": newCtBool(f.HasDefault),
					"attrs":       attrsVal,
				},
				fieldOrder: []string{"name", "type_name", "is_opt", "is_public", "has_default", "attrs"},
				refl:       &ctReflectMeta{TargetType: st, Index: idx, Kind: "field"},
			},
		})
	}
	return s.makeListStruct(&types.Vector{Elem: fieldSt}, elems, span)
}

func (s *ctState) evalCompVariantsOf(ty types.Type, span diag.Span) (*ctVal, ctSignal) {
	varSt := s.a.Prog.StructVariantInfo
	en, ok := ty.(*types.Enum)
	if varSt == nil || !ok {
		return nil, abortSig(span, "`comp.variants_of` requires an enum or result type")
	}
	optIntTy := types.NewOptional(types.TypeInt)
	elems := make([]*ctVal, 0, len(en.Variants))
	for idx, vr := range en.Variants {
		attrsVal, sig := s.evalCompAttrList(vr.Attributes, span)
		if sig.kind != sigNone {
			return nil, sig
		}
		var tagVal *ctVal
		if !en.IsTagged && !en.IsResult {
			tagVal = newCtSome(optIntTy, newCtSignedInt(vr.Value, types.TypeInt))
		} else {
			tagVal = newCtNone(optIntTy)
		}
		hasPayload := vr.Kind == types.VariantTuple && vr.Payload != nil
		elems = append(elems, &ctVal{
			kind: ctStruct,
			ty:   varSt,
			st: &ctStructBox{
				ty: varSt,
				fields: map[string]*ctVal{
					"name":        newCtString(vr.Name),
					"index":       newCtSignedInt(int64(idx), types.TypeInt),
					"tag_value?":  tagVal,
					"has_payload": newCtBool(hasPayload),
					"is_public":   newCtBool(IsExported(vr.Name)),
					"attrs":       attrsVal,
				},
				fieldOrder: []string{"name", "index", "tag_value?", "has_payload", "is_public", "attrs"},
				refl:       &ctReflectMeta{TargetType: en, Index: idx, Kind: "variant"},
			},
		})
	}
	return s.makeListStruct(&types.Vector{Elem: varSt}, elems, span)
}

func (s *ctState) evalCompElemsOf(ty types.Type, span diag.Span) (*ctVal, ctSignal) {
	elemSt := s.a.Prog.StructElemInfo
	if elemSt == nil {
		return nil, abortSig(span, "`comp.ElemInfo` is not available")
	}
	var elemTypes []types.Type
	switch v := ty.(type) {
	case *types.Tuple:
		elemTypes = v.Elems
	case *types.StackArray:
		elemTypes = make([]types.Type, v.Size)
		for i := range elemTypes {
			elemTypes[i] = v.Elem
		}
	default:
		return nil, abortSig(span, "`comp.elems_of` requires a tuple or fixed array `[N: T]`")
	}
	elems := make([]*ctVal, 0, len(elemTypes))
	for idx, et := range elemTypes {
		elems = append(elems, &ctVal{
			kind: ctStruct,
			ty:   elemSt,
			st: &ctStructBox{
				ty: elemSt,
				fields: map[string]*ctVal{
					"index":     newCtSignedInt(int64(idx), types.TypeInt),
					"type_name": newCtString(et.String()),
				},
				fieldOrder: []string{"index", "type_name"},
				refl:       &ctReflectMeta{TargetType: ty, Index: idx, Kind: "elem"},
			},
		})
	}
	return s.makeListStruct(&types.Vector{Elem: elemSt}, elems, span)
}

func (s *ctState) evalCompAttrsOf(ty types.Type, span diag.Span) (*ctVal, ctSignal) {
	var attrs []types.Attribute
	switch v := ty.(type) {
	case *types.Struct:
		attrs = v.Attributes
	case *types.Enum:
		attrs = v.Attributes
	case *types.Distinct:
		attrs = v.Attributes
	case *types.Interface:
		attrs = v.Attributes
	}
	return s.evalCompAttrList(attrs, span)
}

func (a *Analyzer) buildTargetCtVal() *ctVal {
	if a.Prog == nil || a.Prog.StructTarget == nil || a.Prog.EnumTargetOs == nil || a.Prog.EnumTargetArch == nil || a.Prog.EnumEndian == nil {
		return nil
	}
	enumVariantVal := func(en *types.Enum, name string) *ctVal {
		for idx, vr := range en.Variants {
			if vr.Name == name {
				return &ctVal{kind: ctEnum, ty: en, en: en, variantIdx: idx}
			}
		}
		// Fallback to "unknown" or variant 0.
		for idx, vr := range en.Variants {
			if vr.Name == "unknown" {
				return &ctVal{kind: ctEnum, ty: en, en: en, variantIdx: idx}
			}
		}
		return &ctVal{kind: ctEnum, ty: en, en: en, variantIdx: 0}
	}
	ptrSize := int64(a.target.PtrSize)
	if ptrSize == 0 {
		ptrSize = a.targetPtrBytes()
	}
	ptrBits := int64(a.target.PtrBits)
	if ptrBits == 0 {
		ptrBits = ptrSize * 8
	}
	endianName := a.target.Endian
	if endianName != "big" {
		endianName = "little"
	}
	fields := map[string]*ctVal{
		"os":       enumVariantVal(a.Prog.EnumTargetOs, a.target.OS),
		"arch":     enumVariantVal(a.Prog.EnumTargetArch, a.target.Arch),
		"endian":   enumVariantVal(a.Prog.EnumEndian, endianName),
		"is_unix":  newCtBool(a.target.IsUnix),
		"ptr_size": newCtSignedInt(ptrSize, types.TypeInt),
		"ptr_bits": newCtSignedInt(ptrBits, types.TypeInt),
		"triple":   newCtString(a.target.Triple),
	}
	return &ctVal{
		kind: ctStruct,
		ty:   a.Prog.StructTarget,
		st: &ctStructBox{
			ty:         a.Prog.StructTarget,
			fields:     fields,
			fieldOrder: []string{"os", "arch", "endian", "is_unix", "ptr_size", "ptr_bits", "triple"},
		},
	}
}
