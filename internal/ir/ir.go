// Package ir defines Lowlight's Mid-Level Intermediate Representation (MIR),
// which makes monomorphized types, temporary lifetimes, move zeroing, and
// scope/unwind Drop sequences completely explicit before backend code generation.
package ir

import (
	"fmt"
	"sort"

	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// Program is the lowered, backend-ready MIR representation of an entire Lowlight program.
type Program struct {
	Checked       *sema.CheckedProgram
	ExternHeaders []string
	ExternFuncs   []*sema.ExternFunc

	// Concrete nominal & parameterized types in deterministic emission order.
	// OrderedTypes is a post-order topological sort of all value/composite types
	// so every value-embedded field/element type is defined before its container in C11.
	OrderedTypes []types.Type
	Enums        []*types.Enum
	Distincts    []*types.Distinct
	Structs      []*types.Struct
	Optionals    []*types.Optional // non-boxed optionals needing `typedef struct { bool has_value; T value; }`
	Vectors      []*types.Vector
	Arrays       []*types.StackArray
	Sets         []*types.Set
	Maps         []*types.Map
	FuncTypes    []*types.Func
	Interfaces   []*types.Interface
	VTables      []*sema.VTableImpl

	// Top-level constants, functions, lambdas, and entry point
	Globals      []*Global
	Funcs        []*Function
	StaticThunks []*StaticThunk
	MainFunc     *Function

	// Release disables runtime bounds/shift checks when --release is passed.
	Release bool
}

// StaticThunk records a top-level static function referenced as a first-class closure value.
type StaticThunk struct {
	ThunkCName string
	TargetFunc *sema.FuncInstance
	FuncType   *types.Func
}

// Global represents an initialized top-level `bind` constant.
type Global struct {
	Name      string
	CName     string
	Type      types.Type
	InitInstr []Instr
	InitExpr  string
}

// Local represents a function-scoped variable or compiler-synthesized slot.
type Local struct {
	CName     string
	Type      types.Type
	IsMutPtr  bool   // true when C representation is `T*` (`&T` parameter or mutable unwrap `if (&u?)`)
	MovedFlag string // non-empty C bool var name when `Type` is a Struct with custom `Drop`
	IsParam   bool
}

// Function is a lowered function, method, or lambda body in MIR.
type Function struct {
	Name       string
	CName      string
	Params     []*Local
	RetType    types.Type
	Locals     []*Local
	Body       []Instr
	IsLambda   bool
	LambdaInfo *sema.LambdaInfo
}

// -----------------------------------------------------------------------------
// MIR Instructions
// -----------------------------------------------------------------------------

type Instr interface {
	irInstr()
}

// InstrAssign: `Dst = Expr;`
type InstrAssign struct {
	Dst  string
	Expr string
}

func (*InstrAssign) irInstr() {}

// InstrCompoundAssign: `Dst += Expr;` (or helper call for string `+=` / safe shifts)
type InstrCompoundAssign struct {
	Op   token.Kind
	Type types.Type
	Dst  string
	Expr string
}

func (*InstrCompoundAssign) irInstr() {}

// InstrExpr: standalone void call or side-effect expression `Expr;`
type InstrExpr struct {
	Expr string
}

func (*InstrExpr) irInstr() {}

// InstrZero: zeroes a moved local variable slot so subsequent scope-exit Drop is a no-op.
type InstrZero struct {
	Var       string
	Type      types.Type
	MovedFlag string
}

func (*InstrZero) irInstr() {}

// InstrDrop: deterministically drops `Var` if live.
type InstrDrop struct {
	Var       string
	Type      types.Type
	MovedFlag string
}

func (*InstrDrop) irInstr() {}

// InstrIf: `if (Cond) { Then... } else { Else... }`
type InstrIf struct {
	Cond string
	Then []Instr
	Else []Instr
}

func (*InstrIf) irInstr() {}

// InstrWhile: `while (Cond) { Body... }`
type InstrWhile struct {
	Cond string
	Body []Instr
}

func (*InstrWhile) irInstr() {}

// InstrBlock wraps a scoped block `{ Body... }`.
type InstrBlock struct {
	Body []Instr
}

func (*InstrBlock) irInstr() {}

// InstrLabel: `Name:;`
type InstrLabel struct {
	Name string
}

func (*InstrLabel) irInstr() {}

// InstrGoto: `goto Label;` (always preceded by explicit InstrDrop cleanup!)
type InstrGoto struct {
	Label string
}

func (*InstrGoto) irInstr() {}

// InstrReturn: `return [Expr];` (always preceded by explicit InstrDrop cleanup!)
type InstrReturn struct {
	Expr string
}

func (*InstrReturn) irInstr() {}

// -----------------------------------------------------------------------------
// Concrete Type Collector & Topological Sorter
// -----------------------------------------------------------------------------

type typeCollector struct {
	optSeen  map[string]*types.Optional
	vecSeen  map[string]*types.Vector
	arrSeen  map[string]*types.StackArray
	setSeen  map[string]*types.Set
	mapSeen  map[string]*types.Map
	fnSeen   map[string]*types.Func
	ifcSeen  map[string]*types.Interface
	stSeen   map[*types.Struct]bool
	enSeen   map[*types.Enum]bool
	distSeen map[*types.Distinct]bool

	opts    []*types.Optional
	vecs    []*types.Vector
	arrs    []*types.StackArray
	sets    []*types.Set
	maps    []*types.Map
	fns     []*types.Func
	ifcs    []*types.Interface
	sts     []*types.Struct
	ens     []*types.Enum
	dists   []*types.Distinct
	ordered []types.Type
}

func newTypeCollector() *typeCollector {
	return &typeCollector{
		optSeen:  make(map[string]*types.Optional),
		vecSeen:  make(map[string]*types.Vector),
		arrSeen:  make(map[string]*types.StackArray),
		setSeen:  make(map[string]*types.Set),
		mapSeen:  make(map[string]*types.Map),
		fnSeen:   make(map[string]*types.Func),
		ifcSeen:  make(map[string]*types.Interface),
		stSeen:   make(map[*types.Struct]bool),
		enSeen:   make(map[*types.Enum]bool),
		distSeen: make(map[*types.Distinct]bool),
	}
}

func (tc *typeCollector) visit(t types.Type) {
	if t == nil || t == types.TypeInvalid || t == types.TypeVoid || t == types.TypeNever {
		return
	}
	if _, isTP := t.(*types.TypeParamVar); isTP {
		return
	}
	if _, isSelf := t.(*types.SelfPlaceholder); isSelf {
		return
	}

	switch v := t.(type) {
	case *types.Primitive:
		return
	case *types.Ptr:
		tc.visit(v.Elem)
	case *types.Ref:
		tc.visit(v.Elem)
	case *types.Enum:
		if !tc.enSeen[v] {
			tc.enSeen[v] = true
			tc.ens = append(tc.ens, v)
		}
	case *types.Distinct:
		if !tc.distSeen[v] {
			tc.distSeen[v] = true
			tc.visit(v.Base)
			tc.dists = append(tc.dists, v)
			tc.ordered = append(tc.ordered, v)
		}
	case *types.Struct:
		if !tc.stSeen[v] {
			tc.stSeen[v] = true
			for _, f := range v.Fields {
				tc.visit(f.Type)
			}
			tc.sts = append(tc.sts, v)
			tc.ordered = append(tc.ordered, v)
		}
	case *types.Optional:
		tc.visit(v.Elem)
		if !v.IsBoxed() {
			k := v.Mangled()
			if _, ok := tc.optSeen[k]; !ok {
				tc.optSeen[k] = v
				tc.opts = append(tc.opts, v)
				tc.ordered = append(tc.ordered, v)
			}
		}
	case *types.Vector:
		k := v.Mangled()
		if _, ok := tc.vecSeen[k]; !ok {
			tc.vecSeen[k] = v
			tc.visit(v.Elem)
			tc.vecs = append(tc.vecs, v)
			tc.ordered = append(tc.ordered, v)
		}
	case *types.StackArray:
		k := v.Mangled()
		if _, ok := tc.arrSeen[k]; !ok {
			tc.arrSeen[k] = v
			tc.visit(v.Elem)
			tc.arrs = append(tc.arrs, v)
			tc.ordered = append(tc.ordered, v)
		}
	case *types.Set:
		k := v.Mangled()
		if _, ok := tc.setSeen[k]; !ok {
			tc.setSeen[k] = v
			tc.visit(v.Elem)
			tc.sets = append(tc.sets, v)
			tc.ordered = append(tc.ordered, v)
		}
	case *types.Map:
		k := v.Mangled()
		if _, ok := tc.mapSeen[k]; !ok {
			tc.mapSeen[k] = v
			tc.visit(v.Key)
			tc.visit(v.Value)
			tc.maps = append(tc.maps, v)
			tc.ordered = append(tc.ordered, v)
		}
	case *types.Func:
		k := v.Mangled()
		if _, ok := tc.fnSeen[k]; !ok {
			tc.fnSeen[k] = v
			for _, p := range v.Params {
				tc.visit(p.Type)
			}
			tc.visit(v.Ret)
			tc.fns = append(tc.fns, v)
			tc.ordered = append(tc.ordered, v)
		}
	case *types.Interface:
		if v.UsesSelf {
			return
		}
		k := v.Mangled()
		if _, ok := tc.ifcSeen[k]; !ok {
			tc.ifcSeen[k] = v
			for _, m := range v.Methods {
				for _, p := range m.Params {
					tc.visit(p.Type)
				}
				tc.visit(m.Ret)
			}
			tc.ifcs = append(tc.ifcs, v)
			tc.ordered = append(tc.ordered, v)
		}
	}
}

func sortVTablesDeterministically(m map[string]*sema.VTableImpl) []*sema.VTableImpl {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*sema.VTableImpl, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}

func FormatTempName(id int) string {
	return fmt.Sprintf("_ll_t%d", id)
}
