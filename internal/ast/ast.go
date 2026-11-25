// Package ast defines the Abstract Syntax Tree nodes for Lowlight v1.0.
package ast

import (
	"lowlight/internal/diag"
	"lowlight/internal/token"
)

// File represents a parsed `.ll` source file.
type File struct {
	Path    string
	Doc     string
	DocSpan diag.Span
	Decls   []Decl
	Span    diag.Span
}

// TypeParam represents a generic type parameter `[T]`, `[T: Bound1 + Bound2]`,
// or a compile-time value parameter `[N: const int]`.
type TypeParam struct {
	Name      string
	IsConst   bool
	ConstType TypeExpr // non-nil when IsConst is true
	Bounds    []string
	Span      diag.Span
}

// -----------------------------------------------------------------------------
// Top-Level & Scoped Declarations
// -----------------------------------------------------------------------------

type Decl interface {
	declNode()
	GetSpan() diag.Span
}

// Attribute: `@name` or `@name("arg")` or `@name(a, b, ...)`
type Attribute struct {
	Name string
	Args []string
	Span diag.Span
}

func (a Attribute) HasArg(arg string) bool {
	for _, x := range a.Args {
		if x == arg {
			return true
		}
	}
	return false
}

func FindAttribute(attrs []Attribute, name string) *Attribute {
	for i := range attrs {
		if attrs[i].Name == name {
			return &attrs[i]
		}
	}
	return nil
}

// AttributedDecl is implemented by any AST declaration that can carry attributes.
type AttributedDecl interface {
	Decl
	GetAttributes() []Attribute
	SetAttributes([]Attribute)
}

// ImportDecl: `import "path.ll"` or `import "path.ll" as alias`
type ImportDecl struct {
	Path       string
	Alias      string
	Span       diag.Span
	Attributes []Attribute
}

func (*ImportDecl) declNode()                     {}
func (d *ImportDecl) GetSpan() diag.Span          { return d.Span }
func (d *ImportDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *ImportDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// ExternHeaderDecl: `extern "stdio.h"`
type ExternHeaderDecl struct {
	Header     string
	Span       diag.Span
	Attributes []Attribute
}

func (*ExternHeaderDecl) declNode()                     {}
func (d *ExternHeaderDecl) GetSpan() diag.Span          { return d.Span }
func (d *ExternHeaderDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *ExternHeaderDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// ExternFnDecl: `extern puts: (s: ptr[u8]): i32` or `extern sin as c_sin: (x: float): float`
type ExternFnDecl struct {
	Name       string
	Alias      string
	Params     []Param
	Ret        TypeExpr // nil for void
	Doc        string
	DocSpan    diag.Span
	Span       diag.Span
	Attributes []Attribute
}

func (*ExternFnDecl) declNode()                     {}
func (d *ExternFnDecl) GetSpan() diag.Span          { return d.Span }
func (d *ExternFnDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *ExternFnDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// StructDecl: `define [packed] Name[T] struct [ field: Type = default, ... ]`
type StructDecl struct {
	Name       string
	Packed     bool
	TypeParams []TypeParam
	Fields     []StructField
	Doc        string
	DocSpan    diag.Span
	Span       diag.Span
	Attributes []Attribute
}

type StructField struct {
	Name    string
	IsOpt   bool // true if field identifier ended with `?`
	Type    TypeExpr
	Default Expr // optional default value expression
	Doc     string
	DocSpan diag.Span
	Span    diag.Span
}

func (*StructDecl) declNode()                     {}
func (d *StructDecl) GetSpan() diag.Span          { return d.Span }
func (d *StructDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *StructDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// EnumDecl: `define Name[T] enum [u8] [ A = 1, B: T, C: [x: int, y: int], ... ]`
// or a result declaration `define Name[T] result [ value: T, Missing: string, Forbidden ]`.
type EnumDecl struct {
	Name       string
	TypeParams []TypeParam
	BaseType   TypeExpr // optional underlying integer type (defaults to `int` for pure tag enums)
	Variants   []EnumVariant
	IsResult   bool // `define Name result [...]`: exactly one `value` member plus failure variants
	Doc        string
	DocSpan    diag.Span
	Span       diag.Span
	Attributes []Attribute
}

type EnumVariant struct {
	Name        string
	Value       Expr     // optional explicit discriminant (pure tag enums only)
	PayloadType TypeExpr // optional single-value payload (`Ok: T`)
	Doc         string
	DocSpan     diag.Span
	Span        diag.Span
}

func (*EnumDecl) declNode()                     {}
func (d *EnumDecl) GetSpan() diag.Span          { return d.Span }
func (d *EnumDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *EnumDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// InterfaceDecl: `define Name[T] interface [ m: (self, x: int): string, ... ]`
type InterfaceDecl struct {
	Name       string
	TypeParams []TypeParam
	Methods    []InterfaceMethod
	Doc        string
	DocSpan    diag.Span
	Span       diag.Span
	Attributes []Attribute
}

type ReceiverMode int

const (
	RecvRead ReceiverMode = iota // (self) / (self: T)
	RecvMut                      // (&self) / (self: &T)
	RecvOwn                      // (own self) / (self: own T)
)

type InterfaceMethod struct {
	Name     string
	RecvMode ReceiverMode
	Params   []Param
	Ret      TypeExpr // nil for void
	Doc      string
	DocSpan  diag.Span
	Span     diag.Span
}

func (*InterfaceDecl) declNode()                     {}
func (d *InterfaceDecl) GetSpan() diag.Span          { return d.Span }
func (d *InterfaceDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *InterfaceDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// DistinctTypeDecl: `define UserID int`
type DistinctTypeDecl struct {
	Name       string
	Base       TypeExpr
	Doc        string
	DocSpan    diag.Span
	Span       diag.Span
	Attributes []Attribute
}

func (*DistinctTypeDecl) declNode()                     {}
func (d *DistinctTypeDecl) GetSpan() diag.Span          { return d.Span }
func (d *DistinctTypeDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *DistinctTypeDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// MethodDecl: `bind Point.translate = fn(&self, dx: int, dy: int): { ... }`
// Also supports generic receivers `bind Pair[A, B].swap[C] = ...`
type MethodDecl struct {
	IsConst        bool   // true when declared with `const`, false when declared with `bind`
	RecvModule     string // optional imported package qualifier in `bind pkg.Type.method = ...`
	RecvType       string
	RecvTypeParams []string
	Name           string
	TypeParams     []TypeParam
	RecvMode       ReceiverMode
	RecvExplicitTy TypeExpr // optional explicit type in `(self: Point)`
	Params         []Param
	Ret            TypeExpr // nil for void
	Body           Expr
	Doc            string
	DocSpan        diag.Span
	Span           diag.Span
	Attributes     []Attribute
}

func (*MethodDecl) declNode()                     {}
func (d *MethodDecl) GetSpan() diag.Span          { return d.Span }
func (d *MethodDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *MethodDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// VarDecl: top-level `bind` or `let` (including `bind main = ...` and generic `bind foo[T] = ...`)
type VarDecl struct {
	IsBind     bool
	IsConst    bool
	Name       string
	IsOptName  bool
	TypeParams []TypeParam
	Type       TypeExpr // optional explicit type
	Value      Expr
	Doc        string
	DocSpan    diag.Span
	Span       diag.Span
	Attributes []Attribute
}

func (*VarDecl) declNode()                     {}
func (d *VarDecl) GetSpan() diag.Span          { return d.Span }
func (d *VarDecl) GetAttributes() []Attribute  { return d.Attributes }
func (d *VarDecl) SetAttributes(a []Attribute) { d.Attributes = a }

// TopComptimeIfDecl: top-level `comptime if (<cond>): ... [else comptime if ... | else: ...]`
type TopComptimeIfDecl struct {
	Cond       Expr
	ThenDecls  []Decl
	ThenBraced bool
	ElseIf     *TopComptimeIfDecl
	ElseDecls  []Decl
	ElseBraced bool
	HasElse    bool
	Span       diag.Span
}

func (*TopComptimeIfDecl) declNode()            {}
func (d *TopComptimeIfDecl) GetSpan() diag.Span { return d.Span }

// Param represents a function or lambda parameter.
type Param struct {
	Name      string // may be empty in unnamed function type annotations `(int, int): int`
	IsOptName bool
	Type      TypeExpr
	Span      diag.Span
}

// -----------------------------------------------------------------------------
// Type Expressions
// -----------------------------------------------------------------------------

type TypeExpr interface {
	typeNode()
	GetSpan() diag.Span
}

// NamedType: `int`, `Point`, `mod.Point`, `Pair[int, string]`, `ptr[u8]`, `ref[Node]`, `Self`
type NamedType struct {
	Module   string // optional module qualifier
	Name     string
	TypeArgs []TypeExpr
	Span     diag.Span
}

func (*NamedType) typeNode()            {}
func (t *NamedType) GetSpan() diag.Span { return t.Span }

// OptionalType: `?T`
type OptionalType struct {
	Elem TypeExpr
	Span diag.Span
}

func (*OptionalType) typeNode()            {}
func (t *OptionalType) GetSpan() diag.Span { return t.Span }

// MutBorrowType: `&T`
type MutBorrowType struct {
	Elem TypeExpr
	Span diag.Span
}

func (*MutBorrowType) typeNode()            {}
func (t *MutBorrowType) GetSpan() diag.Span { return t.Span }

// OwnType: `own T`
type OwnType struct {
	Elem TypeExpr
	Span diag.Span
}

func (*OwnType) typeNode()            {}
func (t *OwnType) GetSpan() diag.Span { return t.Span }

// VectorType: `[T]`
type VectorType struct {
	Elem TypeExpr
	Span diag.Span
}

func (*VectorType) typeNode()            {}
func (t *VectorType) GetSpan() diag.Span { return t.Span }

// StackArrayType: `[N: T]`
type StackArrayType struct {
	Size Expr // compile-time integer literal or constant identifier
	Elem TypeExpr
	Span diag.Span
}

func (*StackArrayType) typeNode()            {}
func (t *StackArrayType) GetSpan() diag.Span { return t.Span }

// SetType: `#[T]`
type SetType struct {
	Elem TypeExpr
	Span diag.Span
}

func (*SetType) typeNode()            {}
func (t *SetType) GetSpan() diag.Span { return t.Span }

// MapType: `#[K: V]`
type MapType struct {
	Key   TypeExpr
	Value TypeExpr
	Span  diag.Span
}

func (*MapType) typeNode()            {}
func (t *MapType) GetSpan() diag.Span { return t.Span }

// TupleType: `+[T1, T2, ...]`
type TupleType struct {
	Elems []TypeExpr
	Span  diag.Span
}

func (*TupleType) typeNode()            {}
func (t *TupleType) GetSpan() diag.Span { return t.Span }

// ConstArgType represents a compile-time value argument in a generic bracket list
// (e.g., `Matrix[2 + 2, 4]`, `zeros[8]()`).
type ConstArgType struct {
	Expr Expr
	Span diag.Span
}

func (*ConstArgType) typeNode()            {}
func (t *ConstArgType) GetSpan() diag.Span { return t.Span }

// FuncType: `fn(int, int) int`, `cl(Event)`, or `fn()`
type FuncType struct {
	Params  []Param
	Ret     TypeExpr // nil for void function type
	Closure bool     // `cl(...)`: a closure; otherwise `fn(...)`: a function with no captures
	Extern  bool     // `extern fn(...)`: a C function pointer
	Span    diag.Span
}

func (*FuncType) typeNode()            {}
func (t *FuncType) GetSpan() diag.Span { return t.Span }

// -----------------------------------------------------------------------------
// Statements
// -----------------------------------------------------------------------------

type Stmt interface {
	stmtNode()
	GetSpan() diag.Span
}

// UnpackVar describes a single variable binding in a tuple-unpacking `let` / `bind` declaration (`let x, y = unpack ...`).
type UnpackVar struct {
	Name      string
	IsOptName bool
	Type      TypeExpr
	Span      diag.Span
}

// VarStmt: `let` / `bind` local variable declaration.
// If FallbackUnwind is non-nil, Value is the optional LHS of `Value ?? FallbackUnwind`.
// If IsUnpack is true, UnpackVars holds the unpacked bindings (`let x, y = unpack expr`).
type VarStmt struct {
	IsBind         bool
	IsConst        bool
	IsUnpack       bool
	Name           string
	IsOptName      bool
	Type           TypeExpr
	UnpackVars     []UnpackVar
	Value          Expr
	FallbackUnwind *UnwindStmt
	Span           diag.Span
}

func (*VarStmt) stmtNode()            {}
func (s *VarStmt) GetSpan() diag.Span { return s.Span }

// AssignStmt: `target = expr`, `t1, t2 = unpack expr`, or compound `target += expr`.
type AssignStmt struct {
	Op             token.Kind // token.Assign, token.PlusEq, etc.
	IsUnpack       bool
	Target         Expr
	UnpackTargets  []Expr
	Value          Expr
	FallbackUnwind *UnwindStmt // only for `target = opt ?? unwind`
	Span           diag.Span
}

func (*AssignStmt) stmtNode()            {}
func (s *AssignStmt) GetSpan() diag.Span { return s.Span }

type UnwindTargetKind int

const (
	UnwindBlock     UnwindTargetKind = iota // `<- [val]`
	UnwindIf                                // `if <- val`
	UnwindMatch                             // `match <- [val]`
	UnwindFor                               // `for <- [val]`
	UnwindWhile                             // `while <- [val]`
	UnwindNext                              // `next <- [val]`
	UnwindFunc                              // `func <- [val]`
	UnwindLabel                             // `label <- [val]`
	UnwindNextLabel                         // `next label <- [val]`
)

// UnwindStmt: `target <- [value]`
type UnwindStmt struct {
	Target UnwindTargetKind
	Label  string // populated for UnwindLabel and UnwindNextLabel
	Value  Expr   // nil for bare unwind `<-`
	Span   diag.Span
}

func (*UnwindStmt) stmtNode()            {}
func (s *UnwindStmt) GetSpan() diag.Span { return s.Span }

func (s *UnwindStmt) TargetName() string {
	switch s.Target {
	case UnwindBlock:
		return "<-"
	case UnwindIf:
		return "if <-"
	case UnwindMatch:
		return "match <-"
	case UnwindFor:
		return "for <-"
	case UnwindWhile:
		return "while <-"
	case UnwindNext:
		return "next <-"
	case UnwindFunc:
		return "func <-"
	case UnwindLabel:
		return s.Label + " <-"
	case UnwindNextLabel:
		return "next " + s.Label + " <-"
	default:
		return "<-"
	}
}

// DeclStmt wraps a local `define` type declaration inside a block.
type DeclStmt struct {
	Decl Decl
	Span diag.Span
}

func (*DeclStmt) stmtNode()            {}
func (s *DeclStmt) GetSpan() diag.Span { return s.Span }

// ExprStmt wraps a standalone expression inside a block.
type ExprStmt struct {
	Expr Expr
	Span diag.Span
}

func (*ExprStmt) stmtNode()            {}
func (s *ExprStmt) GetSpan() diag.Span { return s.Span }

// -----------------------------------------------------------------------------
// Expressions
// -----------------------------------------------------------------------------

type Expr interface {
	exprNode()
	GetSpan() diag.Span
}

type IntLit struct {
	Raw  string
	Val  uint64
	Span diag.Span
}

func (*IntLit) exprNode()            {}
func (e *IntLit) GetSpan() diag.Span { return e.Span }

type FloatLit struct {
	Raw  string
	Val  float64
	Span diag.Span
}

func (*FloatLit) exprNode()            {}
func (e *FloatLit) GetSpan() diag.Span { return e.Span }

type BoolLit struct {
	Val  bool
	Span diag.Span
}

func (*BoolLit) exprNode()            {}
func (e *BoolLit) GetSpan() diag.Span { return e.Span }

type CharLit struct {
	Val  rune
	Span diag.Span
}

func (*CharLit) exprNode()            {}
func (e *CharLit) GetSpan() diag.Span { return e.Span }

type StringLit struct {
	Val  string
	Span diag.Span
}

func (*StringLit) exprNode()            {}
func (e *StringLit) GetSpan() diag.Span { return e.Span }

// InterpStringExpr: `"Hello, $name! Score: ${a + b}"`
// Segments has length len(Exprs)+1.
type InterpStringExpr struct {
	Segments []string
	Exprs    []Expr
	Span     diag.Span
}

func (*InterpStringExpr) exprNode()            {}
func (e *InterpStringExpr) GetSpan() diag.Span { return e.Span }

type NoneLit struct {
	Span diag.Span
}

func (*NoneLit) exprNode()            {}
func (e *NoneLit) GetSpan() diag.Span { return e.Span }

type IdentExpr struct {
	Name  string
	IsOpt bool
	Span  diag.Span
}

func (*IdentExpr) exprNode()            {}
func (e *IdentExpr) GetSpan() diag.Span { return e.Span }

type UnaryExpr struct {
	Op      token.Kind // Minus, KwNot, Tilde, Amp (&borrow), KwOwn (own move), Star (*deref)
	Operand Expr
	Span    diag.Span
}

func (*UnaryExpr) exprNode()            {}
func (e *UnaryExpr) GetSpan() diag.Span { return e.Span }

type BinaryExpr struct {
	Op    token.Kind
	Left  Expr
	Right Expr
	Span  diag.Span
}

func (*BinaryExpr) exprNode()            {}
func (e *BinaryExpr) GetSpan() diag.Span { return e.Span }

// MemberExpr: `base.member`, `base?.member`, or `Type[T].member`
type MemberExpr struct {
	Base         Expr
	BaseTypeArgs []TypeExpr // optional type arguments before `.` (`Result[int, string].Empty` or disambiguated `words[i].len()`)
	Member       string
	MemberHasOpt bool // true if member token itself had trailing `?` (e.g. `u.address?` or `u?.address?.city`)
	QuestDot     bool // true if `?.` token was used explicitly (`find(id)?.name`)
	Span         diag.Span
}

func (*MemberExpr) exprNode()            {}
func (e *MemberExpr) GetSpan() diag.Span { return e.Span }

// CallExpr: `callee[TypeArgs...](args...)`
type CallExpr struct {
	Callee   Expr
	TypeArgs []TypeExpr
	Args     []Expr
	Span     diag.Span
}

func (*CallExpr) exprNode()            {}
func (e *CallExpr) GetSpan() diag.Span { return e.Span }

// IndexExpr: `base[index]`
type IndexExpr struct {
	Base  Expr
	Index Expr
	Span  diag.Span
}

func (*IndexExpr) exprNode()            {}
func (e *IndexExpr) GetSpan() diag.Span { return e.Span }

// SliceExpr: `base[lo..hi]` or `base[lo..=hi]`
type SliceExpr struct {
	Base      Expr
	Low       Expr
	High      Expr
	Inclusive bool
	Span      diag.Span
}

func (*SliceExpr) exprNode()            {}
func (e *SliceExpr) GetSpan() diag.Span { return e.Span }

// RangeExpr: `lo..hi` or `lo..=hi` constructing a `Range[T]` value
type RangeExpr struct {
	Low       Expr
	High      Expr
	Inclusive bool
	Span      diag.Span
}

func (*RangeExpr) exprNode()            {}
func (e *RangeExpr) GetSpan() diag.Span { return e.Span }

// VecLit: `[1, 2, 3]` or `[]`
type VecLit struct {
	Elems []Expr
	Span  diag.Span
}

func (*VecLit) exprNode()            {}
func (e *VecLit) GetSpan() diag.Span { return e.Span }

// ArrayLit: `[4: 1, 2, 3, 4]` or repeat constructor `[100: 0]`
type ArrayLit struct {
	Count Expr
	Elems []Expr
	Span  diag.Span
}

func (*ArrayLit) exprNode()            {}
func (e *ArrayLit) GetSpan() diag.Span { return e.Span }

// RepeatArrayLit is kept as a compatibility alias for ArrayLit
type RepeatArrayLit = ArrayLit

// SetLit: `#[1, 2, 3]`
type SetLit struct {
	Elems []Expr
	Span  diag.Span
}

func (*SetLit) exprNode()            {}
func (e *SetLit) GetSpan() diag.Span { return e.Span }

// MapEntry represents `key: value` inside a MapLit.
type MapEntry struct {
	Key   Expr
	Value Expr
	Span  diag.Span
}

// MapLit: `#["a": 1, "b": 2]`
type MapLit struct {
	Entries []MapEntry
	Span    diag.Span
}

func (*MapLit) exprNode()            {}
func (e *MapLit) GetSpan() diag.Span { return e.Span }

// EmptyHashLit: `#[]` (disambiguated into Set or Map by expected type in sema)
type EmptyHashLit struct {
	Span diag.Span
}

func (*EmptyHashLit) exprNode()            {}
func (e *EmptyHashLit) GetSpan() diag.Span { return e.Span }

// TupleLit: `+[1, "abc"]` or `+[]`
type TupleLit struct {
	Elems []Expr
	Span  diag.Span
}

func (*TupleLit) exprNode()            {}
func (e *TupleLit) GetSpan() diag.Span { return e.Span }

// StructFieldInit represents `name: expr` in a StructLit.
type StructFieldInit struct {
	Name  string
	IsOpt bool
	Value Expr
	Span  diag.Span
}

// StructLit: `Point[x: 1, y: 2]` or `Pair[int, string][first: 1, second: "s"]`.
type StructLit struct {
	Module   string
	TypeName string
	TypeArgs []TypeExpr
	Fields   []StructFieldInit
	Span     diag.Span
}

func (*StructLit) exprNode()            {}
func (e *StructLit) GetSpan() diag.Span { return e.Span }

// FuncLit: `fn(a: int, b: int) int: a + b` or `cl(x: int) int: x + offset`
type FuncLit struct {
	Params  []Param
	Ret     TypeExpr // nil for void
	Body    Expr
	Closure bool // `cl(...)`: may capture; otherwise `fn(...)`: captures nothing
	Extern  bool // `extern fn(...)`: a function with the C calling convention, callable from C
	Span    diag.Span
}

func (*FuncLit) exprNode()            {}
func (e *FuncLit) GetSpan() diag.Span { return e.Span }

// UnsafeExpr: `unsafe <expr>` (e.g., `unsafe { ... }`, `unsafe foo()`, `unsafe *p`)
type UnsafeExpr struct {
	Expr Expr
	Span diag.Span
}

func (*UnsafeExpr) exprNode()            {}
func (e *UnsafeExpr) GetSpan() diag.Span { return e.Span }

// ComptimeExpr: `comptime <expr>` (e.g., `comptime { ... }`, `comptime compute()`)
type ComptimeExpr struct {
	Expr Expr
	Span diag.Span
}

func (*ComptimeExpr) exprNode()            {}
func (e *ComptimeExpr) GetSpan() diag.Span { return e.Span }

// BlockExpr: `{ stmts... [Tail] }` or `unsafe { ... }` or labeled `blk: { ... }`
type BlockExpr struct {
	Label  string
	Unsafe bool
	Stmts  []Stmt
	Tail   Expr // non-nil when the final item in `{ ... }` is an expression without explicit `;`
	Span   diag.Span
}

func (*BlockExpr) exprNode()            {}
func (e *BlockExpr) GetSpan() diag.Span { return e.Span }

// StmtBodyExpr wraps a single braceless statement used directly as the body of
// an `if` branch, `match` arm, or `for` loop (e.g., `if (x < 0) next <-` or `if (ok) count += 1`).
type StmtBodyExpr struct {
	Stmt Stmt
	Span diag.Span
}

func (*StmtBodyExpr) exprNode()            {}
func (e *StmtBodyExpr) GetSpan() diag.Span { return e.Span }

type IfCondKind int

const (
	IfCondBool    IfCondKind = iota // `if (cond)`
	IfCondOptVar                    // `if (user?)` or `if (&user?)`
	IfCondOptBind                   // `if (expr as name)` or `if (&expr as name)`
)

// IfExpr: `if (...) [EvalType]: thenExpr [else if (...) ... | else: elseExpr]`
type IfExpr struct {
	IsComptime bool
	CondKind   IfCondKind
	MutUnwrap bool     // true for `if (&user?)` or `if (&expr as name)`
	OptVar    string   // e.g., `"user?"` for IfCondOptVar
	BindName  string   // unwrapped non-optional name (`"user"` or `as name`)
	Cond      Expr     // boolean expression (IfCondBool) or optional expression (IfCondOptBind)
	EvalType  TypeExpr // optional explicit evaluation type
	Then      Expr
	Else      Expr // nil when `else` is omitted
	Span      diag.Span
}

func (*IfExpr) exprNode()            {}
func (e *IfExpr) GetSpan() diag.Span { return e.Span }

type ForKind int

const (
	ForCond  ForKind = iota // `for (cond)` / `for (true)`
	ForRange                // `for (i in lo..hi)` / `for (i in lo..=hi)`
	ForIter                 // `for (item in col)` / `for (&item in col)` / `for (k, v in col)` / `for (k, &v in col)`
)

// ForExpr: `[label:] for (...) [: Type] body [else elseBody]`
type ForExpr struct {
	IsComptime bool
	Label      string
	Kind       ForKind
	Var1       string // loop var (`i`, `item`, or `k`)
	Var1IsOpt  bool
	Var1Mut    bool   // `&item` when Var2 is empty
	Var2       string // optional second var (`item` in `i, item` or `v` in `k, v`)
	Var2IsOpt  bool
	Var2Mut    bool     // `&item` / `&v` when 2 vars
	CondOrIter Expr     // boolean condition (ForCond), range start (ForRange), or collection (ForIter)
	RangeEnd   Expr     // range end (ForRange)
	Inclusive  bool     // `..=` vs `..` (ForRange)
	EvalType   TypeExpr // optional `: Type` (e.g. `: [int]` or `: Collectable`)
	Body       Expr
	Else       Expr // optional `else` expression
	Span       diag.Span
}

func (*ForExpr) exprNode()            {}
func (e *ForExpr) GetSpan() diag.Span { return e.Span }

// WhileExpr: `[label:] while (...) [: Type] body [else elseBody]`
type WhileExpr struct {
	Label      string
	Kind       ForKind
	Var1       string // loop var (`i`, `item`, or `k`)
	Var1IsOpt  bool
	Var1Mut    bool   // `&item` when Var2 is empty
	Var2       string // optional second var (`item` in `i, item` or `v` in `k, v`)
	Var2IsOpt  bool
	Var2Mut    bool     // `&item` / `&v` when 2 vars
	CondOrIter Expr     // boolean condition (ForCond), range start (ForRange), or collection (ForIter)
	RangeEnd   Expr     // range end (ForRange)
	Inclusive  bool     // `..=` vs `..` (ForRange)
	EvalType   TypeExpr // optional `: Type`
	Body       Expr
	Else       Expr // optional `else` expression
	Span       diag.Span
}

func (*WhileExpr) exprNode()            {}
func (e *WhileExpr) GetSpan() diag.Span { return e.Span }

// Pattern represents a `match` arm pattern.
type Pattern interface {
	patternNode()
	GetSpan() diag.Span
}

// ExprPattern matches a literal (`200`, `"ok"`, `'a'`, `true`, `none`) or qualified enum (`Status.Pending`).
type ExprPattern struct {
	Expr Expr
	Span diag.Span
}

func (*ExprPattern) patternNode()         {}
func (p *ExprPattern) GetSpan() diag.Span { return p.Span }

// ContainsPattern matches `in <container>` where `<container>` implements `Containable[T]`.
type ContainsPattern struct {
	Container Expr
	Span      diag.Span
}

func (*ContainsPattern) patternNode()         {}
func (p *ContainsPattern) GetSpan() diag.Span { return p.Span }

// MethodPattern matches `<method>[T...](args...)` by invoking a bool-returning method on the scrutinee.
type MethodPattern struct {
	Method     string
	MethodSpan diag.Span
	TypeArgs   []TypeExpr
	Args       []Expr
	Span       diag.Span
}

func (*MethodPattern) patternNode()         {}
func (p *MethodPattern) GetSpan() diag.Span { return p.Span }

// WildcardPattern matches `_` as a standalone pattern.
type WildcardPattern struct {
	Span diag.Span
}

func (*WildcardPattern) patternNode()         {}
func (p *WildcardPattern) GetSpan() diag.Span { return p.Span }

type PatternBindMode int

const (
	PatternBindRead PatternBindMode = iota // `val`
	PatternBindMut                         // `&val`
	PatternBindOwn                         // `own val`
)

type VariantPatternBinding struct {
	BindName   string // bound local variable name (empty when IsWildcard is true)
	IsOptName  bool   // true if bound variable identifier ended with `?`
	Mode       PatternBindMode
	IsWildcard bool // true for `_`
	Span       diag.Span
}

// VariantPattern matches a tagged union variant with payload `(x)` destructuring.
type VariantPattern struct {
	Target   Expr // MemberExpr (`Result.Ok`, `Result[int, string].Ok`, `mod.Result.Ok`, etc.)
	Bindings []VariantPatternBinding
	// Contextual is true for the bare failure patterns of a result chain (`when Missing(why):`,
	// `when Forbidden:`). Target is then a MemberExpr whose Base is an empty placeholder IdentExpr;
	// the variant is resolved against the chain's result type.
	Contextual bool
	Span       diag.Span
}

func (*VariantPattern) patternNode()         {}
func (p *VariantPattern) GetSpan() diag.Span { return p.Span }

// MatchCase represents one combination of patterns matching the scrutinees (or a single bool condition for 0-arg match).
type MatchCase struct {
	Patterns []Pattern
	Span     diag.Span
}

type MatchArm struct {
	Cases     []MatchCase // empty ONLY for open catch-all `default:`
	IsDefault bool        // true for the final `default:` or exhaustive `default ...:` arm
	Body      Expr
	// Synthetic marks arms the checker adds to a result chain (the implicit `value` arm and the
	// per-failure arms expanded from `default panic`). Printers and editor features skip them.
	Synthetic bool
	Span      diag.Span
}

// AllPatterns returns a flattened list of all patterns across all cases in this arm.
func (a *MatchArm) AllPatterns() []Pattern {
	var pats []Pattern
	for _, c := range a.Cases {
		pats = append(pats, c.Patterns...)
	}
	return pats
}

// MatchExpr: `match [(scrutinees)] [EvalType]: when ... default [...]: ...`
type MatchExpr struct {
	IsComptime bool
	Scrutinees []Expr   // empty for 0-arg `match` or `match()`
	EvalType   TypeExpr // optional explicit evaluation type
	Arms       []MatchArm
	// ResultChain is true for a result chain `subject: when Failure(x): ... default ...`, which
	// matches the failure variants of a result-typed subject (Scrutinees[0]) and evaluates to its
	// `value`. Arm patterns are Contextual VariantPatterns.
	ResultChain bool
	// DefaultPanic is true when a result chain ends in `default panic`: every failure variant not
	// handled by an arm panics. The checker expands it into Synthetic arms.
	DefaultPanic     bool
	DefaultPanicSpan diag.Span
	Span             diag.Span
}

func (*MatchExpr) exprNode()            {}
func (e *MatchExpr) GetSpan() diag.Span { return e.Span }
