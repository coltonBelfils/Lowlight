package ir

import (
	"fmt"

	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// Expr is the interface implemented by all typed, backend-independent expressions in Lowlight MIR.
type Expr interface {
	irExpr()
	Type() types.Type
}

// -----------------------------------------------------------------------------
// Leaves / Operands
// -----------------------------------------------------------------------------

// ExprLocal references a function-scoped local variable, parameter, or temporary slot.
type ExprLocal struct {
	Local *Local
}

func (*ExprLocal) irExpr() {}
func (e *ExprLocal) Type() types.Type {
	if e.Local != nil {
		return e.Local.Type
	}
	return types.TypeVoid
}

// ExprGlobal references a top-level immutable constant or module-level binding.
type ExprGlobal struct {
	Global *Global
}

func (*ExprGlobal) irExpr() {}
func (e *ExprGlobal) Type() types.Type {
	if e.Global != nil {
		return e.Global.Type
	}
	return types.TypeVoid
}

// ExprFuncRef references a static function, method instance, extern symbol, or static closure thunk.
type ExprFuncRef struct {
	Name       string
	TargetFunc *sema.FuncInstance
	ExternFunc *sema.ExternFunc
	FuncType   *types.Func
}

func (*ExprFuncRef) irExpr() {}
func (e *ExprFuncRef) Type() types.Type {
	if e.FuncType != nil {
		return e.FuncType
	}
	if e.TargetFunc != nil {
		return e.TargetFunc.Sig
	}
	if e.ExternFunc != nil {
		return e.ExternFunc.Sig
	}
	return types.TypeVoid
}

// ExprLitInt represents an integer literal of signed or unsigned integer type.
type ExprLitInt struct {
	Val     uint64
	Negated bool
	Typ     types.Type
}

func (*ExprLitInt) irExpr() {}
func (e *ExprLitInt) Type() types.Type {
	if e.Typ != nil {
		return e.Typ
	}
	return types.TypeInt
}

// ExprLitFloat represents a 32-bit or 64-bit floating point literal.
type ExprLitFloat struct {
	Val float64
	Typ types.Type
}

func (*ExprLitFloat) irExpr() {}
func (e *ExprLitFloat) Type() types.Type {
	if e.Typ != nil {
		return e.Typ
	}
	return types.TypeFloat
}

// ExprLitBool represents a boolean constant (`true` or `false`).
type ExprLitBool struct {
	Val bool
}

func (*ExprLitBool) irExpr() {}
func (*ExprLitBool) Type() types.Type {
	return types.TypeBool
}

// ExprLitChar represents a Unicode codepoint (`char` / `rune`).
type ExprLitChar struct {
	Val rune
}

func (*ExprLitChar) irExpr() {}
func (*ExprLitChar) Type() types.Type {
	return types.TypeChar
}

// ExprLitString represents an immutable string literal.
type ExprLitString struct {
	Val string
}

func (*ExprLitString) irExpr() {}
func (*ExprLitString) Type() types.Type {
	return types.TypeString
}

// ExprLitCString represents a null-terminated raw byte string literal (ptr[u8] / const char*).
type ExprLitCString struct {
	Val string
	Typ types.Type
}

func (*ExprLitCString) irExpr() {}
func (e *ExprLitCString) Type() types.Type {
	if e.Typ != nil {
		return e.Typ
	}
	return &types.Ptr{Elem: types.TypeU8}
}

// ExprLitNone represents the empty/absent optional value (`none` / `?T`).
type ExprLitNone struct {
	OptType *types.Optional
}

func (*ExprLitNone) irExpr() {}
func (e *ExprLitNone) Type() types.Type {
	if e.OptType != nil {
		return e.OptType
	}
	return types.TypeVoid
}

// ExprLitZero represents a typed all-zero value (e.g. for moved struct slots, default init).
type ExprLitZero struct {
	Typ types.Type
}

func (*ExprLitZero) irExpr() {}
func (e *ExprLitZero) Type() types.Type {
	return e.Typ
}

// -----------------------------------------------------------------------------
// Accessors / LValues
// -----------------------------------------------------------------------------

// ExprField accesses a nominal struct field or tuple field.
type ExprField struct {
	Base      Expr
	FieldName string
	FieldIdx  int
	Typ       types.Type
	IsPtr     bool
}

func (*ExprField) irExpr() {}
func (e *ExprField) Type() types.Type {
	return e.Typ
}

// ExprIndex accesses an element by index in a slice, array, vector, string, or map.
type ExprIndex struct {
	Base  Expr
	Index Expr
	Typ   types.Type
}

func (*ExprIndex) irExpr() {}
func (e *ExprIndex) Type() types.Type {
	return e.Typ
}

// ExprDeref dereferences a pointer (`*ptr`).
type ExprDeref struct {
	Ptr Expr
	Typ types.Type
}

func (*ExprDeref) irExpr() {}
func (e *ExprDeref) Type() types.Type {
	return e.Typ
}

// ExprAddrOf takes the memory address of an LValue (`&place`).
type ExprAddrOf struct {
	Place Expr
	Typ   types.Type
}

func (*ExprAddrOf) irExpr() {}
func (e *ExprAddrOf) Type() types.Type {
	return e.Typ
}

// -----------------------------------------------------------------------------
// Operations & Control
// -----------------------------------------------------------------------------

// ExprBinary represents an arithmetic, bitwise, comparison, or logical binary operation.
type ExprBinary struct {
	Op    token.Kind
	Left  Expr
	Right Expr
	Typ   types.Type
	File  string
	Line  int
}

func (*ExprBinary) irExpr() {}
func (e *ExprBinary) Type() types.Type {
	return e.Typ
}

// ExprUnary represents a unary prefix operation (`-`, `not`, `~`, `*`, `&`).
type ExprUnary struct {
	Op      token.Kind
	Operand Expr
	Typ     types.Type
	File    string
	Line    int
}

func (*ExprUnary) irExpr() {}
func (e *ExprUnary) Type() types.Type {
	return e.Typ
}

// ExprCast represents an explicit numeric, pointer, or distinct type cast.
type ExprCast struct {
	Val  Expr
	From types.Type
	To   types.Type
}

func (*ExprCast) irExpr() {}
func (e *ExprCast) Type() types.Type {
	return e.To
}

// ExprCall represents a function call, method call, closure invocation, or interface dispatch.
type ExprCall struct {
	Callee Expr
	Args   []Expr
	Typ    types.Type
}

func (*ExprCall) irExpr() {}
func (e *ExprCall) Type() types.Type {
	return e.Typ
}

// -----------------------------------------------------------------------------
// Compound Constructors
// -----------------------------------------------------------------------------

type StructFieldInit struct {
	Name  string
	Index int
	Value Expr
}

// ExprConstructStruct builds an instance of a nominal struct.
type ExprConstructStruct struct {
	StructType *types.Struct
	Fields     []StructFieldInit
}

func (*ExprConstructStruct) irExpr() {}
func (e *ExprConstructStruct) Type() types.Type {
	return e.StructType
}

// ExprConstructEnum builds an enum variant instance (scalar or tagged payload).
type ExprConstructEnum struct {
	EnumType *types.Enum
	Variant  *types.EnumVariant
	Payload  Expr
}

func (*ExprConstructEnum) irExpr() {}
func (e *ExprConstructEnum) Type() types.Type {
	return e.EnumType
}

// ExprConstructArray builds a stack-allocated fixed-size array (`[N: T]`).
type ExprConstructArray struct {
	ArrayType *types.StackArray
	Elems     []Expr
}

func (*ExprConstructArray) irExpr() {}
func (e *ExprConstructArray) Type() types.Type {
	return e.ArrayType
}

// ExprConstructVector builds a heap-allocated dynamic vector (`[T]`).
type ExprConstructVector struct {
	VecType *types.Vector
	Elems   []Expr
}

func (*ExprConstructVector) irExpr() {}
func (e *ExprConstructVector) Type() types.Type {
	return e.VecType
}

// ExprConstructTuple builds a stack-allocated tuple (`+[e0, e1, ...]`).
type ExprConstructTuple struct {
	TupleType *types.Tuple
	Elems     []Expr
}

func (*ExprConstructTuple) irExpr() {}
func (e *ExprConstructTuple) Type() types.Type {
	return e.TupleType
}

// ExprFnToClosure converts an `fn` value (a code pointer) to a closure with no captured
// environment.
type ExprFnToClosure struct {
	Fn Expr
	To *types.Func
}

func (*ExprFnToClosure) irExpr() {}
func (e *ExprFnToClosure) Type() types.Type {
	return e.To
}

// ExprConstructClosure builds a function value: for an `fn` type just the entry function's
// address, and for a `cl` type the closure with its environment.
type ExprConstructClosure struct {
	FuncType *types.Func
	EntryFn  string
	EnvPtr   Expr
	CopyFn   string
	DropFn   string
}

func (*ExprConstructClosure) irExpr() {}
func (e *ExprConstructClosure) Type() types.Type {
	return e.FuncType
}

// ExprWrapOptional wraps a concrete value `T` into an optional `?T`.
type ExprWrapOptional struct {
	OptType *types.Optional
	Value   Expr
}

func (*ExprWrapOptional) irExpr() {}
func (e *ExprWrapOptional) Type() types.Type {
	return e.OptType
}

// ExprOptionalHasValue checks if an optional contains a live value (`opt != none`).
type ExprOptionalHasValue struct {
	Opt Expr
}

func (*ExprOptionalHasValue) irExpr() {}
func (*ExprOptionalHasValue) Type() types.Type {
	return types.TypeBool
}

// ExprOptionalValue extracts the inner payload `T` from an optional `?T`.
type ExprOptionalValue struct {
	Opt      Expr
	ElemType types.Type
	IsBoxed  bool
}

func (*ExprOptionalValue) irExpr() {}
func (e *ExprOptionalValue) Type() types.Type {
	return e.ElemType
}

// ExprOptionalValuePtr extracts a pointer to the inner payload `&T` from an optional place.
type ExprOptionalValuePtr struct {
	OptPtr   Expr
	ElemType types.Type
	IsBoxed  bool
}

func (*ExprOptionalValuePtr) irExpr() {}
func (e *ExprOptionalValuePtr) Type() types.Type {
	return &types.Ptr{Elem: e.ElemType}
}

// ExprSliceView builds a zero-allocation read-only view (`[T]`) pointing to existing array or buffer data.
type ExprSliceView struct {
	VecType *types.Vector
	DataPtr Expr
	Len     Expr
}

func (*ExprSliceView) irExpr() {}
func (e *ExprSliceView) Type() types.Type {
	return e.VecType
}

// ExprBoxInterface packages a concrete type into an existential fat-pointer interface value.
type ExprBoxInterface struct {
	IfcType      *types.Interface
	ConcreteType types.Type
	Value        Expr
	VTable       *sema.VTableImpl
	Owned        bool
}

func (*ExprBoxInterface) irExpr() {}
func (e *ExprBoxInterface) Type() types.Type {
	return e.IfcType
}

// -----------------------------------------------------------------------------
// Intrinsic & Memory Operations
// -----------------------------------------------------------------------------

// ExprAlloc allocates memory on the heap for a given type.
type ExprAlloc struct {
	AllocType types.Type
	Count     Expr
	Typ       types.Type
	Uninit    bool
}

func (*ExprAlloc) irExpr() {}
func (e *ExprAlloc) Type() types.Type {
	return e.Typ
}

// ExprFree frees heap memory pointed to by `Ptr`.
type ExprFree struct {
	Ptr Expr
}

func (*ExprFree) irExpr() {}
func (*ExprFree) Type() types.Type {
	return types.TypeVoid
}

// ExprRealloc reallocates heap memory.
type ExprRealloc struct {
	Ptr       Expr
	AllocType types.Type
	Count     Expr
	Typ       types.Type
}

func (*ExprRealloc) irExpr() {}
func (e *ExprRealloc) Type() types.Type {
	return e.Typ
}

// ExprCopyBytes copies raw memory from `Src` to `Dst`.
type ExprCopyBytes struct {
	Dst   Expr
	Src   Expr
	Count Expr
}

func (*ExprCopyBytes) irExpr() {}
func (*ExprCopyBytes) Type() types.Type {
	return types.TypeVoid
}

// ExprSizeOf returns the byte size of `QueryType`.
type ExprSizeOf struct {
	QueryType types.Type
}

func (*ExprSizeOf) irExpr() {}
func (*ExprSizeOf) Type() types.Type {
	return types.TypeInt
}

// ExprAlignOf returns the byte alignment of `QueryType`.
type ExprAlignOf struct {
	QueryType types.Type
}

func (*ExprAlignOf) irExpr() {}
func (*ExprAlignOf) Type() types.Type {
	return types.TypeInt
}

// ExprFloatBits reinterprets the IEEE-754 bit pattern of `float` (`u64`) or `f32` (`u32`).
type ExprFloatBits struct {
	Val       Expr
	FloatType types.Type
	Typ       types.Type
}

func (*ExprFloatBits) irExpr() {}
func (e *ExprFloatBits) Type() types.Type {
	return e.Typ
}

// ExprConst references a statically evaluated constant value (baked into `.rodata` when
// composite such as `string` or `List[T]` with `cap = 0`).
type ExprConst struct {
	Static *StaticVal
	Typ    types.Type
}

func (*ExprConst) irExpr() {}
func (e *ExprConst) Type() types.Type {
	return e.Typ
}

// -----------------------------------------------------------------------------
// Standard Prelude & Collection Operations
// -----------------------------------------------------------------------------

// ExprToString converts a value to its string representation.
type ExprToString struct {
	Val     Expr
	ValType types.Type
}

func (*ExprToString) irExpr() {}
func (*ExprToString) Type() types.Type {
	return types.TypeString
}

// ExprDrop invokes the deterministic destructor for a value.
type ExprDrop struct {
	Ptr     Expr
	ValType types.Type
}

func (*ExprDrop) irExpr() {}
func (*ExprDrop) Type() types.Type {
	return types.TypeVoid
}

// ExprCopy duplicates an owned resource value via its .copy() method.
type ExprCopy struct {
	Val     Expr
	ValType types.Type
}

func (*ExprCopy) irExpr() {}
func (e *ExprCopy) Type() types.Type {
	return e.ValType
}

// ExprEq evaluates equality between two instances of a type.
type ExprEq struct {
	Left    Expr
	Right   Expr
	ValType types.Type
}

func (*ExprEq) irExpr() {}
func (*ExprEq) Type() types.Type {
	return types.TypeBool
}

// ExprCmp evaluates three-way comparison between two instances of a type.
type ExprCmp struct {
	Left    Expr
	Right   Expr
	ValType types.Type
}

func (*ExprCmp) irExpr() {}
func (*ExprCmp) Type() types.Type {
	return types.TypeInt
}

// ExprHash computes the 64-bit hash of a value.
type ExprHash struct {
	Val     Expr
	ValType types.Type
}

func (*ExprHash) irExpr() {}
func (*ExprHash) Type() types.Type {
	return types.TypeU64
}

// ExprVecPush pushes an item onto a dynamic vector.
type ExprVecPush struct {
	VecPtr  Expr
	Item    Expr
	VecType *types.Vector
}

func (*ExprVecPush) irExpr() {}
func (*ExprVecPush) Type() types.Type {
	return types.TypeVoid
}

// -----------------------------------------------------------------------------
// Expression Constructors & Helpers
// -----------------------------------------------------------------------------

func NewLocalExpr(loc *Local) *ExprLocal {
	return &ExprLocal{Local: loc}
}

func NewGlobalExpr(gl *Global) *ExprGlobal {
	return &ExprGlobal{Global: gl}
}

func NewLitInt(val uint64, negated bool, typ types.Type) *ExprLitInt {
	return &ExprLitInt{Val: val, Negated: negated, Typ: typ}
}

func NewLitFloat(val float64, typ types.Type) *ExprLitFloat {
	return &ExprLitFloat{Val: val, Typ: typ}
}

func NewLitBool(val bool) *ExprLitBool {
	return &ExprLitBool{Val: val}
}

func NewLitChar(val rune) *ExprLitChar {
	return &ExprLitChar{Val: val}
}

func NewLitString(val string) *ExprLitString {
	return &ExprLitString{Val: val}
}

func NewLitNone(optTy *types.Optional) *ExprLitNone {
	return &ExprLitNone{OptType: optTy}
}

func NewLitZero(typ types.Type) *ExprLitZero {
	return &ExprLitZero{Typ: typ}
}

// EnsureExpr converts an Expr, *Local, or *Global into a typed ir.Expr.
func EnsureExpr(e any, fallbackType types.Type) Expr {
	if e == nil {
		return nil
	}
	if ex, ok := e.(Expr); ok {
		return ex
	}
	if loc, ok := e.(*Local); ok {
		return &ExprLocal{Local: loc}
	}
	if gl, ok := e.(*Global); ok {
		return &ExprGlobal{Global: gl}
	}
	panic(fmt.Sprintf("EnsureExpr received non-Expr: %T (%v)", e, e))
}

func NewAssign(dst, expr any) *InstrAssign {
	return &InstrAssign{
		Dst:  EnsureExpr(dst, types.TypeVoid),
		Expr: EnsureExpr(expr, types.TypeVoid),
	}
}

func NewCompoundAssign(op token.Kind, ty types.Type, dst, expr any, file string, line int) *InstrCompoundAssign {
	return &InstrCompoundAssign{
		Op:   op,
		Type: ty,
		Dst:  EnsureExpr(dst, ty),
		Expr: EnsureExpr(expr, ty),
		File: file,
		Line: line,
	}
}

func NewInstrExpr(expr any) *InstrExpr {
	return &InstrExpr{
		Expr: EnsureExpr(expr, types.TypeVoid),
	}
}

func NewInstrIf(cond any, then []Instr, els []Instr) *InstrIf {
	return &InstrIf{
		Cond: EnsureExpr(cond, types.TypeBool),
		Then: then,
		Else: els,
	}
}

func NewInstrWhile(cond any, body []Instr) *InstrWhile {
	return &InstrWhile{
		Cond: EnsureExpr(cond, types.TypeBool),
		Body: body,
	}
}

func NewInstrReturn(expr any) *InstrReturn {
	if expr == nil {
		return &InstrReturn{}
	}
	return &InstrReturn{
		Expr: EnsureExpr(expr, types.TypeVoid),
	}
}

func NewInstrDrop(v any, ty types.Type, movedFlag string) *InstrDrop {
	if loc, ok := v.(*Local); ok {
		return &InstrDrop{Var: loc, Type: ty, MovedFlag: movedFlag}
	}
	if name, ok := v.(string); ok {
		return &InstrDrop{VarName: name, Type: ty, MovedFlag: movedFlag}
	}
	return &InstrDrop{Type: ty, MovedFlag: movedFlag}
}

func NewInstrZero(v any, ty types.Type, movedFlag string) *InstrZero {
	if loc, ok := v.(*Local); ok {
		return &InstrZero{Var: loc, Type: ty, MovedFlag: movedFlag}
	}
	if name, ok := v.(string); ok {
		return &InstrZero{VarName: name, Type: ty, MovedFlag: movedFlag}
	}
	return &InstrZero{Type: ty, MovedFlag: movedFlag}
}
