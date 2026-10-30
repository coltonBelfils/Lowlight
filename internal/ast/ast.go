// Package ast defines the Abstract Syntax Tree nodes for Lowlight v1.0.
package ast

import (
	"lowlight/internal/diag"
	"lowlight/internal/token"
)

// File represents a parsed `.ll` source file.
type File struct {
	Path  string
	Decls []Decl
	Span  diag.Span
}

// TypeParam represents a generic type parameter `[T]` or `[T: Bound1 + Bound2]`.
type TypeParam struct {
	Name   string
	Bounds []string
	Span   diag.Span
}

// -----------------------------------------------------------------------------
// Top-Level & Scoped Declarations
// -----------------------------------------------------------------------------

type Decl interface {
	declNode()
	GetSpan() diag.Span
}

// ImportDecl: `import "path.ll"` or `import "path.ll" as alias`
type ImportDecl struct {
	Path  string
	Alias string
	Span  diag.Span
}

func (*ImportDecl) declNode()            {}
func (d *ImportDecl) GetSpan() diag.Span { return d.Span }

// ExternHeaderDecl: `extern "stdio.h"`
type ExternHeaderDecl struct {
	Header string
	Span   diag.Span
}

func (*ExternHeaderDecl) declNode()            {}
func (d *ExternHeaderDecl) GetSpan() diag.Span { return d.Span }

// ExternFnDecl: `extern puts: (s: ptr[u8]): i32` or `extern sin as c_sin: (x: float): float`
type ExternFnDecl struct {
	Name   string
	Alias  string
	Params []Param
	Ret    TypeExpr // nil for void
	Span   diag.Span
}

func (*ExternFnDecl) declNode()            {}
func (d *ExternFnDecl) GetSpan() diag.Span { return d.Span }

// StructDecl: `define [packed] Name[T] struct [ field: Type = default, ... ]`
type StructDecl struct {
	Name       string
	Packed     bool
	TypeParams []TypeParam
	Fields     []StructField
	Span       diag.Span
}

type StructField struct {
	Name    string
	IsOpt   bool // true if field identifier ended with `?`
	Type    TypeExpr
	Default Expr // optional default value expression
	Span    diag.Span
}

func (*StructDecl) declNode()            {}
func (d *StructDecl) GetSpan() diag.Span { return d.Span }

// EnumDecl: `define Name enum [u8] [ A = 1, B, ... ]`
type EnumDecl struct {
	Name     string
	BaseType TypeExpr // optional underlying integer type (defaults to `int`)
	Variants []EnumVariant
	Span     diag.Span
}

type EnumVariant struct {
	Name  string
	Value Expr // optional explicit discriminant
	Span  diag.Span
}

func (*EnumDecl) declNode()            {}
func (d *EnumDecl) GetSpan() diag.Span { return d.Span }

// InterfaceDecl: `define Name interface [ m: (self, x: int): string, ... ]`
type InterfaceDecl struct {
	Name    string
	Methods []InterfaceMethod
	Span    diag.Span
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
	Span     diag.Span
}

func (*InterfaceDecl) declNode()            {}
func (d *InterfaceDecl) GetSpan() diag.Span { return d.Span }

// DistinctTypeDecl: `define UserID int`
type DistinctTypeDecl struct {
	Name string
	Base TypeExpr
	Span diag.Span
}

func (*DistinctTypeDecl) declNode()            {}
func (d *DistinctTypeDecl) GetSpan() diag.Span { return d.Span }

// MethodDecl: `bind Point.translate = (&self, dx: int, dy: int) { ... }`
// Also supports generic receivers `bind Pair[A, B].swap[C] = ...`
type MethodDecl struct {
	RecvType       string
	RecvTypeParams []string
	Name           string
	TypeParams     []TypeParam
	RecvMode       ReceiverMode
	RecvExplicitTy TypeExpr // optional explicit type in `(self: Point)`
	Params         []Param
	Ret            TypeExpr // nil for void
	Body           Expr
	Span           diag.Span
}

func (*MethodDecl) declNode()            {}
func (d *MethodDecl) GetSpan() diag.Span { return d.Span }

// VarDecl: top-level `bind` or `let` (including `bind main = ...` and generic `bind foo[T] = ...`)
type VarDecl struct {
	IsBind     bool
	Name       string
	IsOptName  bool
	TypeParams []TypeParam
	Type       TypeExpr // optional explicit type
	Value      Expr
	Span       diag.Span
}

func (*VarDecl) declNode()            {}
func (d *VarDecl) GetSpan() diag.Span { return d.Span }

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

// FuncType: `(int, int): int` or `(Event)` or `()`
type FuncType struct {
	Params []Param
	Ret    TypeExpr // nil for void function type
	Span   diag.Span
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

// VarStmt: `let` / `bind` local variable declaration.
// If FallbackUnwind is non-nil, Value is the optional LHS of `Value ?? FallbackUnwind`.
type VarStmt struct {
	IsBind         bool
	Name           string
	IsOptName      bool
	Type           TypeExpr
	Value          Expr
	FallbackUnwind *UnwindStmt
	Span           diag.Span
}

func (*VarStmt) stmtNode()            {}
func (s *VarStmt) GetSpan() diag.Span { return s.Span }

// AssignStmt: `target = expr` or compound `target += expr`.
type AssignStmt struct {
	Op             token.Kind // token.Assign, token.PlusEq, etc.
	Target         Expr
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

// MemberExpr: `base.member` or `base?.member`
type MemberExpr struct {
	Base         Expr
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

// VecLit: `[1, 2, 3]` or `[]`
type VecLit struct {
	Elems []Expr
	Span  diag.Span
}

func (*VecLit) exprNode()            {}
func (e *VecLit) GetSpan() diag.Span { return e.Span }

// RepeatArrayLit: `[100: 0]`
type RepeatArrayLit struct {
	Count Expr
	Value Expr
	Span  diag.Span
}

func (*RepeatArrayLit) exprNode()            {}
func (e *RepeatArrayLit) GetSpan() diag.Span { return e.Span }

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

// StructFieldInit represents `name: expr` in a StructLit.
type StructFieldInit struct {
	Name  string
	IsOpt bool
	Value Expr
	Span  diag.Span
}

// StructLit: `Point[x: 1, y: 2]` or `Pair[int, string][first: 1, second: "s"]`
type StructLit struct {
	Module   string
	TypeName string
	TypeArgs []TypeExpr
	Fields   []StructFieldInit
	Span     diag.Span
}

func (*StructLit) exprNode()            {}
func (e *StructLit) GetSpan() diag.Span { return e.Span }

// FuncLit: `(a: int, b: int): int { a + b }`
type FuncLit struct {
	Params []Param
	Ret    TypeExpr // nil for void
	Body   Expr
	Span   diag.Span
}

func (*FuncLit) exprNode()            {}
func (e *FuncLit) GetSpan() diag.Span { return e.Span }

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

// IfExpr: `if (...) thenExpr [else elseExpr]`
type IfExpr struct {
	CondKind  IfCondKind
	MutUnwrap bool   // true for `if (&user?)` or `if (&expr as name)`
	OptVar    string // e.g., `"user?"` for IfCondOptVar
	BindName  string // unwrapped non-optional name (`"user"` or `as name`)
	Cond      Expr   // boolean expression (IfCondBool) or optional expression (IfCondOptBind)
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

// ForExpr: `[label:] for (...) body [else elseBody]`
type ForExpr struct {
	Label      string
	Kind       ForKind
	Var1       string // loop var (`i`, `item`, or `k`)
	Var1IsOpt  bool
	Var1Mut    bool   // `&item` when Var2 is empty
	Var2       string // optional second var (`item` in `i, item` or `v` in `k, v`)
	Var2IsOpt  bool
	Var2Mut    bool // `&item` / `&v` when 2 vars
	CondOrIter Expr // boolean condition (ForCond), range start (ForRange), or collection (ForIter)
	RangeEnd   Expr // range end (ForRange)
	Inclusive  bool // `..=` vs `..` (ForRange)
	Body       Expr
	Else       Expr // optional `else` expression
	Span       diag.Span
}

func (*ForExpr) exprNode()            {}
func (e *ForExpr) GetSpan() diag.Span { return e.Span }

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

// RangePattern matches `lo..hi` or `lo..=hi` on integers or chars.
type RangePattern struct {
	Low       Expr
	High      Expr
	Inclusive bool
	Span      diag.Span
}

func (*RangePattern) patternNode()         {}
func (p *RangePattern) GetSpan() diag.Span { return p.Span }

type MatchArm struct {
	Patterns []Pattern // empty ONLY for open catch-all `else:`
	IsElse   bool      // true for the final `else:` or `else Variant:` arm
	Body     Expr
	Span     diag.Span
}

// MatchExpr: `match (scrutinee) when ...: ... else [...]: ...`
type MatchExpr struct {
	Scrutinee Expr
	Arms      []MatchArm
	Span      diag.Span
}

func (*MatchExpr) exprNode()            {}
func (e *MatchExpr) GetSpan() diag.Span { return e.Span }
