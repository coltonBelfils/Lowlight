// Package types defines Lowlight's semantic type representations, type equality,
// Plain-vs-Resource (Move) classification, and canonical C identifier mangling.
package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Type is implemented by every resolved semantic type in Lowlight.
type Type interface {
	String() string
	Mangled() string
	Equals(other Type) bool
	IsPlain() bool // true = implicitly copied by value; false = Resource (single owner, moved & dropped)
}

// ParamMode represents the three transparent parameter passing modes in Lowlight.
type ParamMode int

const (
	ModeRead ParamMode = iota // (x: T)      -> read-only borrow
	ModeMut                   // (x: &T)     -> exclusive mutable borrow
	ModeOwn                   // (x: own T)  -> ownership transfer (consume)
)

func (m ParamMode) Prefix() string {
	switch m {
	case ModeMut:
		return "&"
	case ModeOwn:
		return "own "
	default:
		return ""
	}
}

// -----------------------------------------------------------------------------
// Primitive Types
// -----------------------------------------------------------------------------

type PrimKind int

const (
	PrimInvalid PrimKind = iota
	PrimVoid             // non-value return/statement marker
	PrimNever            // diverging control flow (`func <-`, infinite loop)
	PrimBool
	PrimChar
	PrimI8
	PrimI16
	PrimI32
	PrimI64 // `int`
	PrimU8
	PrimU16
	PrimU32
	PrimU64
	PrimF32
	PrimF64 // `float`
	PrimString
)

type Primitive struct {
	Kind PrimKind
}

var (
	TypeInvalid = &Primitive{Kind: PrimInvalid}
	TypeVoid    = &Primitive{Kind: PrimVoid}
	TypeNever   = &Primitive{Kind: PrimNever}
	TypeBool    = &Primitive{Kind: PrimBool}
	TypeChar    = &Primitive{Kind: PrimChar}
	TypeI8      = &Primitive{Kind: PrimI8}
	TypeI16     = &Primitive{Kind: PrimI16}
	TypeI32     = &Primitive{Kind: PrimI32}
	TypeInt     = &Primitive{Kind: PrimI64}
	TypeU8      = &Primitive{Kind: PrimU8}
	TypeU16     = &Primitive{Kind: PrimU16}
	TypeU32     = &Primitive{Kind: PrimU32}
	TypeU64     = &Primitive{Kind: PrimU64}
	TypeF32     = &Primitive{Kind: PrimF32}
	TypeFloat   = &Primitive{Kind: PrimF64}
	TypeString  = &Primitive{Kind: PrimString}
)

// LookupPrimitive resolves a built-in type name (`int`, `i64`, `float`, `f64`, `u8`, etc.).
func LookupPrimitive(name string) *Primitive {
	switch name {
	case "bool":
		return TypeBool
	case "char":
		return TypeChar
	case "i8":
		return TypeI8
	case "i16":
		return TypeI16
	case "i32":
		return TypeI32
	case "int", "i64":
		return TypeInt
	case "u8":
		return TypeU8
	case "u16":
		return TypeU16
	case "u32":
		return TypeU32
	case "u64":
		return TypeU64
	case "f32":
		return TypeF32
	case "float", "f64":
		return TypeFloat
	case "string":
		return TypeString
	default:
		return nil
	}
}

func (p *Primitive) String() string {
	switch p.Kind {
	case PrimVoid:
		return "void"
	case PrimNever:
		return "never"
	case PrimBool:
		return "bool"
	case PrimChar:
		return "char"
	case PrimI8:
		return "i8"
	case PrimI16:
		return "i16"
	case PrimI32:
		return "i32"
	case PrimI64:
		return "int"
	case PrimU8:
		return "u8"
	case PrimU16:
		return "u16"
	case PrimU32:
		return "u32"
	case PrimU64:
		return "u64"
	case PrimF32:
		return "f32"
	case PrimF64:
		return "float"
	case PrimString:
		return "string"
	default:
		return "<invalid>"
	}
}

func (p *Primitive) Mangled() string {
	return p.String()
}

func (p *Primitive) Equals(other Type) bool {
	o, ok := other.(*Primitive)
	return ok && p.Kind == o.Kind
}

func (p *Primitive) IsPlain() bool {
	return p.Kind != PrimString
}

// IsArena reports whether `t` is std/core's `Arena` (the `@lang("arena")` struct), whose
// `ref[T]` allocations the checker ties to the arena's scope.
func IsArena(t Type) bool {
	st, ok := t.(*Struct)
	return ok && st.Lang == "arena"
}

func (p *Primitive) IsInteger() bool {
	switch p.Kind {
	case PrimI8, PrimI16, PrimI32, PrimI64, PrimU8, PrimU16, PrimU32, PrimU64:
		return true
	default:
		return false
	}
}

func (p *Primitive) IsSignedInt() bool {
	switch p.Kind {
	case PrimI8, PrimI16, PrimI32, PrimI64:
		return true
	default:
		return false
	}
}

func (p *Primitive) IsUnsignedInt() bool {
	switch p.Kind {
	case PrimU8, PrimU16, PrimU32, PrimU64:
		return true
	default:
		return false
	}
}

func (p *Primitive) IsFloat() bool {
	return p.Kind == PrimF32 || p.Kind == PrimF64
}

func (p *Primitive) IsNumeric() bool {
	return p.IsInteger() || p.IsFloat()
}

func (p *Primitive) BitWidth() int {
	switch p.Kind {
	case PrimI8, PrimU8:
		return 8
	case PrimI16, PrimU16:
		return 16
	case PrimI32, PrimU32, PrimF32, PrimChar:
		return 32
	case PrimI64, PrimU64, PrimF64:
		return 64
	default:
		return 0
	}
}

// FitsUnsignedLiteral checks whether a non-negative integer literal `val` fits in `p`.
func (p *Primitive) FitsUnsignedLiteral(val uint64, negated bool) bool {
	switch p.Kind {
	case PrimU8:
		return !negated && val <= math.MaxUint8
	case PrimU16:
		return !negated && val <= math.MaxUint16
	case PrimU32:
		return !negated && val <= math.MaxUint32
	case PrimU64:
		return !negated
	case PrimI8:
		if negated {
			return val <= 128
		}
		return val <= math.MaxInt8
	case PrimI16:
		if negated {
			return val <= 32768
		}
		return val <= math.MaxInt16
	case PrimI32:
		if negated {
			return val <= 2147483648
		}
		return val <= math.MaxInt32
	case PrimI64:
		if negated {
			return val <= 9223372036854775808
		}
		return val <= math.MaxInt64
	default:
		return false
	}
}

// -----------------------------------------------------------------------------
// Composite & Nominal Types
// -----------------------------------------------------------------------------

// Optional represents `?Elem`. Nested optionals flatten automatically (`?(?T) -> ?T`).
type Optional struct {
	Elem Type
}

func NewOptional(elem Type) *Optional {
	if opt, ok := elem.(*Optional); ok {
		return opt
	}
	return &Optional{Elem: elem}
}

// IsBoxed returns true when `Elem` is a recursive struct whose `?Elem` is heap-boxed as `Elem*`.
func (o *Optional) IsBoxed() bool {
	if st, ok := o.Elem.(*Struct); ok {
		return st.BoxedOptional
	}
	return false
}

func (o *Optional) String() string  { return "?" + o.Elem.String() }
func (o *Optional) Mangled() string { return "opt_" + o.Elem.Mangled() }
func (o *Optional) Equals(other Type) bool {
	ot, ok := other.(*Optional)
	return ok && o.Elem.Equals(ot.Elem)
}
func (o *Optional) IsPlain() bool {
	if o.IsBoxed() {
		return false // owns a heap-allocated `Struct*`
	}
	return o.Elem.IsPlain()
}

// Vector represents `[Elem]` (dynamic heap vector, or read-only contiguous view as parameter).
type Vector struct {
	Elem Type
}

func (v *Vector) String() string  { return "[" + v.Elem.String() + "]" }
func (v *Vector) Mangled() string { return "vec_" + v.Elem.Mangled() }
func (v *Vector) Equals(other Type) bool {
	if o, ok := other.(*Vector); ok {
		return v.Elem.Equals(o.Elem)
	}
	if st, ok := other.(*Struct); ok {
		if (st.Name == "List" || strings.HasPrefix(st.Name, "List[")) && len(st.TypeArgs) == 1 {
			return v.Elem.Equals(st.TypeArgs[0])
		}
	}
	return false
}
func (v *Vector) IsPlain() bool { return false }

// StackArray represents `[Size: Elem]`.
type StackArray struct {
	Size int64
	Elem Type
}

func (a *StackArray) String() string  { return fmt.Sprintf("[%d: %s]", a.Size, a.Elem.String()) }
func (a *StackArray) Mangled() string { return fmt.Sprintf("arr%d_%s", a.Size, a.Elem.Mangled()) }
func (a *StackArray) Equals(other Type) bool {
	if o, ok := other.(*StackArray); ok {
		return a.Size == o.Size && a.Elem.Equals(o.Elem)
	}
	if st, ok := other.(*Struct); ok {
		if (st.Name == "Array" || strings.HasPrefix(st.Name, "Array[")) && len(st.TypeArgs) >= 1 {
			if len(st.TypeArgs) == 2 {
				return a.Elem.Equals(st.TypeArgs[1])
			}
			return a.Elem.Equals(st.TypeArgs[0])
		}
	}
	return false
}
func (a *StackArray) IsPlain() bool { return a.Elem.IsPlain() }

// Set represents `#[Elem]`.
type Set struct {
	Elem Type
}

func (s *Set) String() string  { return "#[" + s.Elem.String() + "]" }
func (s *Set) Mangled() string { return "set_" + s.Elem.Mangled() }
func (s *Set) Equals(other Type) bool {
	o, ok := other.(*Set)
	return ok && s.Elem.Equals(o.Elem)
}
func (s *Set) IsPlain() bool { return false }

// Map represents `#[Key: Value]`.
type Map struct {
	Key   Type
	Value Type
}

func (m *Map) String() string  { return fmt.Sprintf("#[%s: %s]", m.Key.String(), m.Value.String()) }
func (m *Map) Mangled() string { return fmt.Sprintf("map_%s__%s", m.Key.Mangled(), m.Value.Mangled()) }
func (m *Map) Equals(other Type) bool {
	o, ok := other.(*Map)
	return ok && m.Key.Equals(o.Key) && m.Value.Equals(o.Value)
}
func (m *Map) IsPlain() bool { return false }

// Tuple represents `+[T0, T1, ...]`.
type Tuple struct {
	Elems []Type
}

func NewTuple(elems []Type) *Tuple {
	return &Tuple{Elems: elems}
}

func (t *Tuple) String() string {
	var sb strings.Builder
	sb.WriteString("+[")
	for i, e := range t.Elems {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(e.String())
	}
	sb.WriteString("]")
	return sb.String()
}

func (t *Tuple) Mangled() string {
	var sb strings.Builder
	sb.WriteString("tup")
	sb.WriteString(fmt.Sprintf("%d", len(t.Elems)))
	for _, e := range t.Elems {
		sb.WriteString("_")
		sb.WriteString(e.Mangled())
	}
	return sb.String()
}

func (t *Tuple) Equals(other Type) bool {
	o, ok := other.(*Tuple)
	if !ok || len(t.Elems) != len(o.Elems) {
		return false
	}
	for i, e := range t.Elems {
		if !e.Equals(o.Elems[i]) {
			return false
		}
	}
	return true
}

func (t *Tuple) IsPlain() bool {
	for _, e := range t.Elems {
		if !e.IsPlain() {
			return false
		}
	}
	return true
}

// Ptr represents a raw pointer `ptr[Elem]` (dereference/ops require `unsafe`).
type Ptr struct {
	Elem Type
}

func (p *Ptr) String() string  { return "ptr[" + p.Elem.String() + "]" }
func (p *Ptr) Mangled() string { return "ptr_" + p.Elem.Mangled() }
func (p *Ptr) Equals(other Type) bool {
	o, ok := other.(*Ptr)
	return ok && p.Elem.Equals(o.Elem)
}
func (p *Ptr) IsPlain() bool { return true }

// Ref represents a region-checked arena reference `ref[Elem]`.
type Ref struct {
	Elem Type
}

func (r *Ref) String() string  { return "ref[" + r.Elem.String() + "]" }
func (r *Ref) Mangled() string { return "ref_" + r.Elem.Mangled() }
func (r *Ref) Equals(other Type) bool {
	o, ok := other.(*Ref)
	return ok && r.Elem.Equals(o.Elem)
}
func (r *Ref) IsPlain() bool { return true }

// StructField describes a resolved field on a Struct.
type StructField struct {
	Name       string // includes trailing `?` if optional (`next?`)
	CName      string // sanitized C identifier (`next_opt`)
	Type       Type
	HasDefault bool
	Doc        string
}

// Struct represents a concrete (or monomorphized) struct type.
type Struct struct {
	Name          string
	CName         string
	PkgDir        string
	Packed        bool
	MustUse       bool
	Deprecated    bool
	DeprecatedMsg  string
	IsTestOnly        bool
	IsComptimeOnly    bool
	CompNamespaceOnly bool // declared in std/core for `comp.*` only, not exposed bare in prelude
	Doc               string
	Fields        []StructField
	HasCustomDrop bool
	BoxedOptional bool // true if `?ThisStruct` closes a recursive cycle and compiles to `ThisStruct*`
	TypeArgs      []Type
	// Lang is the `@lang("...")` item this struct was instantiated from
	// ("list", "array", "set", "map", "range"), or "" for ordinary structs.
	Lang          string
	plainComputed bool
	plainCached   bool
	plainVisiting bool
}

func (s *Struct) String() string {
	if len(s.TypeArgs) == 0 {
		return s.Name
	}
	// `#[T]` and `#[K: V]` are spelled with their literal syntax in diagnostics.
	if s.Lang == "set" && len(s.TypeArgs) == 1 {
		return "#[" + s.TypeArgs[0].String() + "]"
	}
	if s.Lang == "map" && len(s.TypeArgs) == 2 {
		return "#[" + s.TypeArgs[0].String() + ": " + s.TypeArgs[1].String() + "]"
	}
	parts := make([]string, len(s.TypeArgs))
	for i, ta := range s.TypeArgs {
		parts[i] = ta.String()
	}
	return fmt.Sprintf("%s[%s]", s.Name, strings.Join(parts, ", "))
}

func (s *Struct) Mangled() string { return s.CName }

func (s *Struct) Equals(other Type) bool {
	if o, ok := other.(*Struct); ok {
		return s == o
	}
	if v, ok := other.(*Vector); ok {
		if (s.Name == "List" || strings.HasPrefix(s.Name, "List[")) && len(s.TypeArgs) == 1 {
			return s.TypeArgs[0].Equals(v.Elem)
		}
	}
	if arr, ok := other.(*StackArray); ok {
		if (s.Name == "Array" || strings.HasPrefix(s.Name, "Array[")) && len(s.TypeArgs) >= 1 {
			if len(s.TypeArgs) == 2 {
				return s.TypeArgs[1].Equals(arr.Elem)
			}
			return s.TypeArgs[0].Equals(arr.Elem)
		}
	}
	return false
}

func (s *Struct) IsPlain() bool {
	if s.HasCustomDrop {
		return false
	}
	if s.plainComputed {
		return s.plainCached
	}
	if s.plainVisiting {
		return true
	}
	s.plainVisiting = true
	res := true
	for _, f := range s.Fields {
		if !f.Type.IsPlain() {
			res = false
			break
		}
	}
	s.plainVisiting = false
	s.plainComputed = true
	s.plainCached = res
	return res
}

func (s *Struct) FieldByName(name string) (*StructField, int) {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return &s.Fields[i], i
		}
	}
	return nil, -1
}

type EnumVariantKind int

const (
	VariantUnit  EnumVariantKind = iota // `Empty`
	VariantTuple                        // `Ok: T`
)

// EnumVariant describes a variant in a pure tag enum or a tagged union enum.
type EnumVariant struct {
	Name    string
	CName   string
	Value   int64 // 0-based (or explicit) for pure tag enums; 1..N for tagged unions (0 = inert moved/dropped sentinel)
	Kind    EnumVariantKind
	Payload Type // non-nil for VariantTuple
	Doc     string
}

// Enum represents a strongly-typed tag enum (`define Status enum ...`)
// or a payload-carrying / generic tagged union (`define Result[T, E] enum ...`).
type Enum struct {
	Name          string
	CName         string
	PkgDir        string
	MustUse       bool
	Deprecated    bool
	DeprecatedMsg  string
	IsTestOnly        bool
	IsComptimeOnly    bool
	CompNamespaceOnly bool // declared in std/core for `comp.*` only, not exposed bare in prelude
	Doc               string
	IsTagged      bool   // true when any variant carries a payload or enum has generic type parameters
	IsResult      bool   // declared with `define Name result [...]`: one `value` member plus failure variants
	TypeArgs      []Type // populated for monomorphized generic enums
	HasCustomDrop bool
	Underlying    *Primitive // integer type for pure tag enums, default `TypeInt`
	Variants      []EnumVariant

	plainComputed bool
	plainCached   bool
	plainVisiting bool
}

// ResultValueName is the reserved name of a result declaration's success member.
const ResultValueName = "value"

// ResultValue returns the success member of a result declaration and its index, or (nil, -1)
// when `e` is not a result (or is malformed).
func (e *Enum) ResultValue() (*EnumVariant, int) {
	if !e.IsResult {
		return nil, -1
	}
	for i := range e.Variants {
		if e.Variants[i].Name == ResultValueName {
			return &e.Variants[i], i
		}
	}
	return nil, -1
}

// IsUnitResult reports whether `e` is a result whose `value` member carries no payload.
func (e *Enum) IsUnitResult() bool {
	v, _ := e.ResultValue()
	return v != nil && v.Payload == nil
}

// ResultFailures returns the failure variants of a result declaration (every member except `value`).
func (e *Enum) ResultFailures() []*EnumVariant {
	var out []*EnumVariant
	for i := range e.Variants {
		if e.Variants[i].Name != ResultValueName {
			out = append(out, &e.Variants[i])
		}
	}
	return out
}

// AsResult returns `t` as a result-declared enum, or nil.
func AsResult(t Type) *Enum {
	if en, ok := t.(*Enum); ok && en.IsResult {
		return en
	}
	return nil
}

func (e *Enum) String() string {
	if len(e.TypeArgs) == 0 {
		return e.Name
	}
	parts := make([]string, len(e.TypeArgs))
	for i, ta := range e.TypeArgs {
		parts[i] = ta.String()
	}
	return fmt.Sprintf("%s[%s]", e.Name, strings.Join(parts, ", "))
}

func (e *Enum) Mangled() string { return e.CName }
func (e *Enum) Equals(other Type) bool {
	o, ok := other.(*Enum)
	return ok && e == o
}
func (e *Enum) IsPlain() bool {
	if e.HasCustomDrop {
		return false
	}
	if !e.IsTagged {
		return true
	}
	if e.plainComputed {
		return e.plainCached
	}
	if e.plainVisiting {
		return true
	}
	e.plainVisiting = true
	res := true
	for _, v := range e.Variants {
		if v.Kind == VariantTuple && v.Payload != nil && !v.Payload.IsPlain() {
			res = false
		}
		if !res {
			break
		}
	}
	e.plainVisiting = false
	e.plainComputed = true
	e.plainCached = res
	return res
}

func (e *Enum) VariantByName(name string) (*EnumVariant, bool) {
	for i := range e.Variants {
		if e.Variants[i].Name == name {
			return &e.Variants[i], true
		}
	}
	return nil, false
}

// Distinct represents `define UserID int`.
type Distinct struct {
	Name       string
	CName      string
	PkgDir         string
	IsTestOnly        bool
	IsComptimeOnly    bool
	CompNamespaceOnly bool // declared in std/core for `comp.*` only, not exposed bare in prelude
	Doc               string
	Base           Type
}

func (d *Distinct) String() string  { return d.Name }
func (d *Distinct) Mangled() string { return d.CName }
func (d *Distinct) Equals(other Type) bool {
	o, ok := other.(*Distinct)
	return ok && d == o
}
func (d *Distinct) IsPlain() bool { return d.Base.IsPlain() }

// FindComptimeOnlyType reports the first `@comptime_only` named type inside t, or nil.
func FindComptimeOnlyType(t Type) Type {
	return findComptimeOnlyRec(t, map[Type]bool{})
}

func findComptimeOnlyRec(t Type, seen map[Type]bool) Type {
	if t == nil || t == TypeInvalid || seen[t] {
		return nil
	}
	seen[t] = true
	switch v := t.(type) {
	case *Struct:
		if v.IsComptimeOnly {
			return v
		}
		for _, ta := range v.TypeArgs {
			if hit := findComptimeOnlyRec(ta, seen); hit != nil {
				return hit
			}
		}
		for _, f := range v.Fields {
			if hit := findComptimeOnlyRec(f.Type, seen); hit != nil {
				return hit
			}
		}
	case *Enum:
		if v.IsComptimeOnly {
			return v
		}
		for _, ta := range v.TypeArgs {
			if hit := findComptimeOnlyRec(ta, seen); hit != nil {
				return hit
			}
		}
		for _, vr := range v.Variants {
			if hit := findComptimeOnlyRec(vr.Payload, seen); hit != nil {
				return hit
			}
		}
	case *Distinct:
		if v.IsComptimeOnly {
			return v
		}
		return findComptimeOnlyRec(v.Base, seen)
	case *Interface:
		if v.IsComptimeOnly {
			return v
		}
	case *Ptr:
		return findComptimeOnlyRec(v.Elem, seen)
	case *Ref:
		return findComptimeOnlyRec(v.Elem, seen)
	case *Optional:
		return findComptimeOnlyRec(v.Elem, seen)
	case *Vector:
		return findComptimeOnlyRec(v.Elem, seen)
	case *StackArray:
		return findComptimeOnlyRec(v.Elem, seen)
	case *Set:
		return findComptimeOnlyRec(v.Elem, seen)
	case *Map:
		if hit := findComptimeOnlyRec(v.Key, seen); hit != nil {
			return hit
		}
		return findComptimeOnlyRec(v.Value, seen)
	case *Tuple:
		for _, el := range v.Elems {
			if hit := findComptimeOnlyRec(el, seen); hit != nil {
				return hit
			}
		}
	case *Func:
		for _, p := range v.Params {
			if hit := findComptimeOnlyRec(p.Type, seen); hit != nil {
				return hit
			}
		}
		return findComptimeOnlyRec(v.Ret, seen)
	}
	return nil
}

// UnderlyingPrimitive unwraps nested Distinct types to their root Primitive, if any.
func UnderlyingPrimitive(t Type) *Primitive {
	for {
		switch v := t.(type) {
		case *Primitive:
			return v
		case *Distinct:
			t = v.Base
		default:
			return nil
		}
	}
}

// TypePkgDir returns the package directory where type `t` was defined, or empty string.
func TypePkgDir(t Type) string {
	if t == nil {
		return ""
	}
	switch v := t.(type) {
	case *Struct:
		return v.PkgDir
	case *Enum:
		return v.PkgDir
	case *Interface:
		return v.PkgDir
	case *Distinct:
		return v.PkgDir
	case *Ptr:
		return TypePkgDir(v.Elem)
	case *Ref:
		return TypePkgDir(v.Elem)
	case *Optional:
		return TypePkgDir(v.Elem)
	case *Vector:
		return TypePkgDir(v.Elem)
	case *StackArray:
		return TypePkgDir(v.Elem)
	default:
		return ""
	}
}

// FuncParam describes one parameter of a function or interface method.
type FuncParam struct {
	Name string
	Mode ParamMode
	Type Type
}

// Func represents `(params...): Ret` (or void when Ret == TypeVoid).
// Note: A closure value may own heap captures at runtime, so a first-class Func
// stored in a variable carries a `drop_env` hook in C and is treated as a Resource
// ONLY if it may hold heap captures; to keep closures 100% leak-free even when
// reassigned dynamically (`let f = ...`), first-class Func values have a lightweight
// `{ fn, env, copy_env, drop_env }` representation where non-capturing functions have `env = NULL`!
// Func is a function type: `fn(params) Ret`, a function that captures nothing (a plain code
// pointer), or `cl(params) Ret`, a closure that owns its captured variables.
type Func struct {
	Params  []FuncParam
	Ret     Type // TypeVoid if void
	Closure bool
	// Extern marks an `extern fn(...)`: a C function pointer, called with the platform's C calling
	// convention and no closure environment.
	Extern bool
}

func (f *Func) String() string {
	ps := make([]string, len(f.Params))
	for i, p := range f.Params {
		ps[i] = p.Mode.Prefix() + p.Type.String()
	}
	kw := "fn"
	if f.Closure {
		kw = "cl"
	} else if f.Extern {
		kw = "extern fn"
	}
	if f.Ret == nil || f.Ret.Equals(TypeVoid) {
		return kw + "(" + strings.Join(ps, ", ") + ")"
	}
	return fmt.Sprintf("%s(%s) %s", kw, strings.Join(ps, ", "), f.Ret.String())
}

// SameSignature reports whether f and o take and return the same types, ignoring fn vs cl.
func (f *Func) SameSignature(o *Func) bool {
	if len(f.Params) != len(o.Params) {
		return false
	}
	for i := range f.Params {
		if f.Params[i].Mode != o.Params[i].Mode || !f.Params[i].Type.Equals(o.Params[i].Type) {
			return false
		}
	}
	return f.Ret.Equals(o.Ret)
}

// AsClosure returns the closure type with f's signature.
func (f *Func) AsClosure() *Func {
	return &Func{Params: f.Params, Ret: f.Ret, Closure: true}
}

func (f *Func) Mangled() string {
	var sb strings.Builder
	if f.Closure {
		sb.WriteString("cl")
	} else if f.Extern {
		sb.WriteString("xfn")
	} else {
		sb.WriteString("fn")
	}
	for _, p := range f.Params {
		sb.WriteByte('_')
		switch p.Mode {
		case ModeMut:
			sb.WriteString("mut_")
		case ModeOwn:
			sb.WriteString("own_")
		}
		sb.WriteString(p.Type.Mangled())
	}
	sb.WriteString("__ret_")
	if f.Ret == nil {
		sb.WriteString("void")
	} else {
		sb.WriteString(f.Ret.Mangled())
	}
	return sb.String()
}

func (f *Func) Equals(other Type) bool {
	o, ok := other.(*Func)
	return ok && f.Closure == o.Closure && f.Extern == o.Extern && f.SameSignature(o)
}

func (f *Func) IsPlain() bool {
	// An `fn` is a code pointer; a `cl` owns its captured environment and is a resource.
	return !f.Closure
}

// InterfaceMethod describes one required method on an Interface.
type InterfaceMethod struct {
	Name     string
	RecvMode ParamMode
	Params   []FuncParam
	Ret      Type // may be TypeSelf inside Copyable
	Doc      string
}

// SelfPlaceholder represents `Self` inside interface definitions.
type SelfPlaceholder struct{}

var TypeSelf = &SelfPlaceholder{}

func (*SelfPlaceholder) String() string     { return "Self" }
func (*SelfPlaceholder) Mangled() string    { return "Self" }
func (*SelfPlaceholder) Equals(o Type) bool { _, ok := o.(*SelfPlaceholder); return ok }
func (*SelfPlaceholder) IsPlain() bool      { return true }

// Interface represents `define Printable interface [...]`.
type Interface struct {
	Name       string
	CName      string
	PkgDir         string
	IsTestOnly        bool
	IsComptimeOnly    bool
	CompNamespaceOnly bool // declared in std/core for `comp.*` only, not exposed bare in prelude
	Doc               string
	Methods        []InterfaceMethod
	UsesSelf   bool // if true (e.g. Copyable), usable as generic bound `[T: Copyable]`, not runtime fat pointer
}

func (i *Interface) String() string  { return i.Name }
func (i *Interface) Mangled() string { return i.CName }
func (i *Interface) Equals(other Type) bool {
	o, ok := other.(*Interface)
	return ok && i == o
}
func (i *Interface) IsPlain() bool { return false } // owned interface values own a heap box

// TypeParamVar represents an uninstantiated generic type parameter `T` during template checking.
type TypeParamVar struct {
	Name   string
	Bounds []*Interface
}

func (tp *TypeParamVar) String() string  { return tp.Name }
func (tp *TypeParamVar) Mangled() string { return "tp_" + tp.Name }
func (tp *TypeParamVar) Equals(other Type) bool {
	o, ok := other.(*TypeParamVar)
	return ok && tp.Name == o.Name
}
func (tp *TypeParamVar) IsPlain() bool { return false }

// ConstValueKind classifies compile-time constant arguments in `[N: const Type]`.
type ConstValueKind int

const (
	ConstInt ConstValueKind = iota
	ConstBool
	ConstChar
	ConstEnum
	ConstString
)

// ConstValue represents a resolved compile-time constant argument stored alongside types
// in monomorphized `Struct.TypeArgs`, `Enum.TypeArgs`, `FuncInstance.TypeSubst`, etc.
type ConstValue struct {
	Kind       ConstValueKind
	ConstType  Type   // concrete parameter type (`int`, `u8`, `bool`, `char`, `Endian`, `string`, ...)
	IntVal     int64  // sign-extended value for signed integers / enum tag
	UintVal    uint64 // width-masked bit pattern for integers / bool / char
	BoolVal    bool
	RuneVal    rune
	EnumTy     *Enum
	VariantIdx int
	EnumName   string
	StrVal     string
	CtVal      any // optional cached *sema.ctVal
}

func NewConstInt(signed int64, raw uint64, prim *Primitive) *ConstValue {
	if prim == nil {
		prim = TypeInt
	}
	return &ConstValue{
		Kind:      ConstInt,
		ConstType: prim,
		IntVal:    signed,
		UintVal:   raw,
	}
}

func NewConstBool(b bool) *ConstValue {
	u := uint64(0)
	if b {
		u = 1
	}
	return &ConstValue{Kind: ConstBool, ConstType: TypeBool, BoolVal: b, UintVal: u}
}

func NewConstChar(r rune) *ConstValue {
	return &ConstValue{Kind: ConstChar, ConstType: TypeChar, RuneVal: r, IntVal: int64(r), UintVal: uint64(r)}
}

func NewConstEnum(en *Enum, variantIdx int, name string) *ConstValue {
	var tagVal int64
	if en != nil && variantIdx >= 0 && variantIdx < len(en.Variants) {
		tagVal = en.Variants[variantIdx].Value
	}
	return &ConstValue{
		Kind:       ConstEnum,
		ConstType:  en,
		EnumTy:     en,
		VariantIdx: variantIdx,
		EnumName:   name,
		IntVal:     tagVal,
		UintVal:    uint64(tagVal),
	}
}

func NewConstString(s string) *ConstValue {
	return &ConstValue{Kind: ConstString, ConstType: TypeString, StrVal: s}
}

func (c *ConstValue) String() string {
	switch c.Kind {
	case ConstInt:
		if p, ok := c.ConstType.(*Primitive); ok && p.IsUnsignedInt() {
			return strconv.FormatUint(c.UintVal, 10)
		}
		return strconv.FormatInt(c.IntVal, 10)
	case ConstBool:
		if c.BoolVal {
			return "true"
		}
		return "false"
	case ConstChar:
		return strconv.QuoteRune(c.RuneVal)
	case ConstEnum:
		if c.EnumTy != nil && c.EnumName != "" {
			return c.EnumTy.Name + "." + c.EnumName
		}
		return fmt.Sprintf("<enum:%d>", c.VariantIdx)
	case ConstString:
		return strconv.Quote(c.StrVal)
	default:
		return "<const>"
	}
}

func sanitizeConstStringPrefix(s string, maxLen int) string {
	var sb strings.Builder
	prevUnderscore := false
	for i := 0; i < len(s) && sb.Len() < maxLen; i++ {
		b := s[i]
		isAlnum := (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
		if isAlnum {
			sb.WriteByte(b)
			prevUnderscore = false
		} else if !prevUnderscore && sb.Len() > 0 {
			sb.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.TrimRight(sb.String(), "_")
}

func (c *ConstValue) Mangled() string {
	switch c.Kind {
	case ConstInt:
		if p, ok := c.ConstType.(*Primitive); !ok || p.IsSignedInt() {
			if c.IntVal < 0 {
				return fmt.Sprintf("neg%d", uint64(^c.IntVal)+1)
			}
			return strconv.FormatInt(c.IntVal, 10)
		}
		return strconv.FormatUint(c.UintVal, 10)
	case ConstBool:
		if c.BoolVal {
			return "true"
		}
		return "false"
	case ConstChar:
		return fmt.Sprintf("ch_%x", uint32(c.RuneVal))
	case ConstEnum:
		return fmt.Sprintf("ev_%d", c.VariantIdx)
	case ConstString:
		sum := sha256.Sum256([]byte(c.StrVal))
		tag := hex.EncodeToString(sum[:6])
		prefix := sanitizeConstStringPrefix(c.StrVal, 16)
		if prefix == "" {
			return "str_" + tag
		}
		return "str_" + prefix + "_" + tag
	default:
		return "const"
	}
}

func (c *ConstValue) Equals(other Type) bool {
	o, ok := other.(*ConstValue)
	if !ok || c.Kind != o.Kind {
		return false
	}
	if c.ConstType == nil || o.ConstType == nil || !c.ConstType.Equals(o.ConstType) {
		return false
	}
	switch c.Kind {
	case ConstInt:
		return c.IntVal == o.IntVal && c.UintVal == o.UintVal
	case ConstBool:
		return c.BoolVal == o.BoolVal
	case ConstChar:
		return c.RuneVal == o.RuneVal
	case ConstEnum:
		return c.EnumTy != nil && c.EnumTy.Equals(o.EnumTy) && c.VariantIdx == o.VariantIdx
	case ConstString:
		return c.StrVal == o.StrVal
	default:
		return false
	}
}

func (c *ConstValue) IsPlain() bool { return true }

// IsConstGenericParamType reports whether `t` is valid as the declared type of `[N: const Type]`.
// Allowed: integer primitives (`int`, `i8`..`i64`, `u8`..`u64`), `bool`, `char`, tag `enum`s, and `string`.
func IsConstGenericParamType(t Type) bool {
	switch v := t.(type) {
	case *Primitive:
		return v.IsInteger() || v.Kind == PrimBool || v.Kind == PrimChar || v.Kind == PrimString
	case *Enum:
		return !v.IsTagged && !v.IsResult
	default:
		return false
	}
}

// -----------------------------------------------------------------------------
// Capabilities: Hashability, Equality, Region Reference Containment
// -----------------------------------------------------------------------------

// IsHashable reports whether `t` is valid as a Set element or Map key.
func IsHashable(t Type) bool {
	switch v := t.(type) {
	case *Primitive:
		return v.IsInteger() || v.Kind == PrimBool || v.Kind == PrimChar || v.Kind == PrimString
	case *Ptr:
		return true
	case *Distinct:
		return IsHashable(v.Base)
	case *Tuple:
		for _, el := range v.Elems {
			if !IsHashable(el) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// SupportsEq reports whether `==` and `!=` are defined on `t`.
func SupportsEq(t Type) bool {
	return supportsEqVisited(t, make(map[Type]bool))
}

func supportsEqVisited(t Type, seen map[Type]bool) bool {
	switch v := t.(type) {
	case *Primitive:
		return v.Kind != PrimVoid
	case *Ptr, *Ref:
		return true
	case *Distinct:
		return supportsEqVisited(v.Base, seen)
	case *Optional:
		return supportsEqVisited(v.Elem, seen)
	case *Tuple:
		for _, el := range v.Elems {
			if !supportsEqVisited(el, seen) {
				return false
			}
		}
		return true
	default:
		// Structs and enums have `==` only through an `eq` or `cmp` method (`@derive`).
		return false
	}
}

// ContainsArenaRef reports whether `t` is `ref[U]` or transitively contains a `ref[U]`.
func ContainsArenaRef(t Type) bool {
	return containsRefVisited(t, make(map[Type]bool))
}

func containsRefVisited(t Type, seen map[Type]bool) bool {
	switch v := t.(type) {
	case *Ref:
		return true
	case *Optional:
		return containsRefVisited(v.Elem, seen)
	case *Vector:
		return containsRefVisited(v.Elem, seen)
	case *StackArray:
		return containsRefVisited(v.Elem, seen)
	case *Set:
		return containsRefVisited(v.Elem, seen)
	case *Map:
		return containsRefVisited(v.Key, seen) || containsRefVisited(v.Value, seen)
	case *Tuple:
		for _, el := range v.Elems {
			if containsRefVisited(el, seen) {
				return true
			}
		}
		return false
	case *Distinct:
		return containsRefVisited(v.Base, seen)
	case *Enum:
		if !v.IsTagged || seen[v] {
			return false
		}
		seen[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == VariantTuple && vr.Payload != nil && containsRefVisited(vr.Payload, seen) {
				return true
			}
		}
		return false
	case *Struct:
		if seen[v] {
			return false
		}
		seen[v] = true
		for _, f := range v.Fields {
			if containsRefVisited(f.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// SanitizeCIdent converts a Lowlight identifier (including `user?` and `cfg!`) into a valid C identifier.
func SanitizeCIdent(name string) string {
	if strings.HasSuffix(name, "?") {
		return "ll_" + strings.TrimSuffix(name, "?") + "_opt"
	}
	if strings.HasSuffix(name, "!") {
		return "ll_" + strings.TrimSuffix(name, "!") + "_res"
	}
	return "ll_" + name
}
