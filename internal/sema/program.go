// Package sema implements multi-file import resolution, type checking, generic
// monomorphization, ownership/move checking, borrow checking, arena region escape
// analysis, unsafe boundary enforcement, and control-flow exhaustiveness for Lowlight.
package sema

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/parser"
	"lowlight/internal/types"
)

type diskASTCacheEntry struct {
	modTimeNano int64
	size        int64
	file        *ast.File
}

var (
	diskASTMu    sync.RWMutex
	diskASTCache = make(map[string]diskASTCacheEntry)
)

// BindKind classifies how a local variable holds its value.
type BindKind int

const (
	BindOwned      BindKind = iota // owns its value (let, bind, or `own` param)
	BindReadBorrow                 // read-only borrowed view (default param, `for (x in ...)`, `if (u?)`)
	BindMutBorrow                  // exclusive mutable borrow (`&` param, `for (&x in ...)`, `if (&u?)`)
)

// MoveState tracks ownership liveness of a local variable at a program point.
type MoveState int

const (
	MoveAlive MoveState = iota
	MoveMoved
	MoveMaybeMoved
)

// IsExported reports whether an identifier is exported outside its package.
// In Lowlight (Option 1), any name starting with an underscore `_` is unexported.
func IsExported(name string) bool {
	return !strings.HasPrefix(name, "_")
}

// Symbol represents a named entity (variable, constant, function, parameter).
type Symbol struct {
	Name          string
	CName         string
	Mod           *Module
	Type          types.Type
	IsBind        bool // true = deeply immutable (`bind`, read-only param, read-only loop var, capture)
	BindKind      BindKind
	IsTopLevel    bool
	IsConstInt    bool
	ConstIntVal   int64
	ScopeDepth    int
	ArenaDepth    int // > 0 if this value holds `ref[T]` tied to a local Arena at ArenaDepth
	DeclFuncDepth int // function/lambda nesting level where declared
	MustUse       bool
	Deprecated    bool
	DeprecatedMsg string
	IsTest        bool
	IsTestOnly    bool
	TrackCaller   bool
	TestDesc      string
	FuncInst      *FuncInstance
	GenFunc       *GenericFuncTemplate
	ExternFn      *ExternFunc
	Doc           string
	Span          diag.Span
}

// ExternFunc represents a resolved `extern name: (params): Ret` C ABI function.
type ExternFunc struct {
	Name string
	Sig  *types.Func
	Doc  string
	Span diag.Span
}

// GenericStructTemplate stores an uninstantiated `define Name[T...] struct [...]`.
type GenericStructTemplate struct {
	Decl      *ast.StructDecl
	Mod       *Module
	Instances map[string]*types.Struct
}

// GenericEnumTemplate stores an uninstantiated `define Name[T...] enum [...]`.
type GenericEnumTemplate struct {
	Decl      *ast.EnumDecl
	Mod       *Module
	Instances map[string]*types.Enum
}

// GenericInterfaceTemplate stores an uninstantiated `define Name[T...] interface [...]`.
type GenericInterfaceTemplate struct {
	Decl      *ast.InterfaceDecl
	Mod       *Module
	Instances map[string]*types.Interface
}

// GenericFuncTemplate stores an uninstantiated generic `bind fn[T...] = ...` or method.
type GenericFuncTemplate struct {
	Name       string
	VarDecl    *ast.VarDecl
	MethodDecl *ast.MethodDecl
	Mod        *Module
	Instances  map[string]*FuncInstance
}

// FuncInstance is a concrete (or monomorphized) Lowlight function or method to be lowered to MIR.
type FuncInstance struct {
	Name          string // display name (`add`, `Point.length`, `Pair[int, string].swap`)
	CName         string // unique C function name (`ll_fn_add`, `ll_m_Point_length`)
	Sig           *types.Func
	IsMethod      bool
	IsStaticAssoc bool
	RecvType      types.Type // non-nil for methods / associated functions
	RecvMode      ast.ReceiverMode
	ParamNames    []string  // includes receiver `"self"` at index 0 if IsMethod && !IsStaticAssoc
	ParamSpans    []diag.Span
	ParamSyms     []*Symbol // resolved parameter symbols populated during body checking
	Body          ast.Expr
	Mod           *Module
	TypeSubst     map[string]types.Type
	FuncTargetID  int
	BodyMaps      *BodyMaps
	DeclSpan       diag.Span
	Checked        bool
	RetInferred    bool
	InferState     int // 0 = unvisited, 1 = inferring, 2 = resolved
	IsDrop         bool
	IsMain         bool
	IsTestMain     bool
	IsAlwaysInline bool
	IsNoInline     bool
	MustUse        bool
	Deprecated     bool
	DeprecatedMsg  string
	IsTest         bool
	IsTestOnly     bool
	TrackCaller    bool
	TestDesc       string
	Doc            string
}

// BodyMaps holds per-monomorphized-body AST side tables so multiple generic
// instantiations sharing the same AST nodes never overwrite each other.
type BodyMaps struct {
	ExprInfos        map[ast.Expr]*ExprInfo
	UnwindInfos      map[*ast.UnwindStmt]*UnwindInfo
	VarSymbols       map[*ast.VarStmt]*Symbol
	UnpackVarSymbols map[*ast.VarStmt][]*Symbol
	ForSymbols       map[*ast.ForExpr][2]*Symbol   // [Var1Sym, Var2Sym]
	WhileSymbols     map[*ast.WhileExpr][2]*Symbol // [Var1Sym, Var2Sym]
	IfUnwrapSyms     map[*ast.IfExpr]*Symbol
	IfOriginSyms     map[*ast.IfExpr]*Symbol
	PatternSyms      map[*ast.VariantPattern][]*Symbol
	ContainsPatterns map[*ast.ContainsPattern]*ContainsInfo
}

// NewBodyMaps allocates a fresh set of per-body AST side tables.
func NewBodyMaps() *BodyMaps {
	return &BodyMaps{
		ExprInfos:        make(map[ast.Expr]*ExprInfo),
		UnwindInfos:      make(map[*ast.UnwindStmt]*UnwindInfo),
		VarSymbols:       make(map[*ast.VarStmt]*Symbol),
		UnpackVarSymbols: make(map[*ast.VarStmt][]*Symbol),
		ForSymbols:       make(map[*ast.ForExpr][2]*Symbol),
		WhileSymbols:     make(map[*ast.WhileExpr][2]*Symbol),
		IfUnwrapSyms:     make(map[*ast.IfExpr]*Symbol),
		IfOriginSyms:     make(map[*ast.IfExpr]*Symbol),
		PatternSyms:      make(map[*ast.VariantPattern][]*Symbol),
		ContainsPatterns: make(map[*ast.ContainsPattern]*ContainsInfo),
	}
}

// Capture represents a variable captured by an anonymous lambda (`*ast.FuncLit`).
type Capture struct {
	Name     string
	CName    string
	Type     types.Type
	OuterSym *Symbol
	Moved    bool // true when a Resource variable moved into the closure
}

// LambdaInfo holds resolved metadata for an `*ast.FuncLit`.
type LambdaInfo struct {
	ID           int
	CName        string
	EnvCName     string
	Sig          *types.Func
	Captures     []Capture
	Lit          *ast.FuncLit
	ParamSyms    []*Symbol
	FuncTargetID int
	BodyMaps     *BodyMaps
}

// CallKind classifies how a `*ast.CallExpr` is lowered in MIR/Codegen.
type CallKind int

const (
	CallDirectFunc    CallKind = iota // static `bind` function or monomorphized generic function
	CallClosureValue                  // calling a function-typed variable/expression
	CallExternC                       // calling an `extern` C ABI function
	CallTypeConvert                   // `float(x)`, `u8(x)`, `int(Status.Pending)`, `UserID(42)`
	CallInterfaceBox                  // `Shape(my_circle)` -> owned boxed interface value
	CallBuiltinFunc                   // `print`, `println`, `eprint`, `eprintln`, `panic`, `assert`, `parse_int`, etc.
	CallBuiltinMethod                 // built-in method on primitive/string/vec/array/set/map/Arena/enum/struct
	CallInterfaceMethod               // dynamic dispatch through interface fat pointer vtable
	CallIntrinsic                     // `size_of`, `align_of`, `alloc_raw`, `ptr_read`, etc.
	CallEnumVariant                   // tuple-payload enum variant constructor (`Result.Ok(42)`)
)

// ExprInfo stores semantic resolution results per AST expression node.
type ExprInfo struct {
	Type          types.Type
	Sym           *Symbol // non-nil if IdentExpr resolved to a Symbol
	WrapOptional  bool    // true if expression of type `T` is implicitly wrapped into `?T`
	BoxInterface  *types.Interface // non-nil if concrete value is implicitly/explicitly boxed or borrowed as Interface
	BorrowAsView  bool    // true if string literal, slice, or stack array is passed as zero-copy view
	MovedLocal    *Symbol // non-nil if evaluating this expression moves ownership out of a local variable

	// Call resolution (for *ast.CallExpr)
	CallKind      CallKind
	TargetFunc    *FuncInstance
	TargetExtern  *ExternFunc
	BuiltinName   string
	InterfaceMeth *types.InterfaceMethod
	MethodRecv    ast.Expr        // receiver expression for UFCS dot-method calls
	MethodRecvMode types.ParamMode
	IntrinsicType types.Type      // concrete type argument `T` for `size_of[T]`, `alloc_raw[T]`, `ptr_cast[To]`, etc.

	// Member resolution (for *ast.MemberExpr)
	EnumVariant   *types.EnumVariant
	EnumType      *types.Enum
	IsOptChain    bool // true when `.` / `?.` short-circuits on an optional base (`?Base -> ?Result`)
	DerefStructPtr bool // true when accessing field on boxed `?RecursiveStruct` or `ref[Struct]`
	TupleFieldIdx  int  // 0-based field index when MemberExpr or IndexExpr accesses a tuple element

	// Struct literal resolution (for *ast.StructLit)
	StructType    *types.Struct

	// Lambda resolution (for *ast.FuncLit)
	Lambda        *LambdaInfo

	// Target ID for control-flow constructs (`BlockExpr`, `IfExpr`, `MatchExpr`, `ForExpr`)
	TargetID        int
	YieldArenaDepth int  // max ArenaDepth yielded/captured by BlockExpr, IfExpr, MatchExpr, ForExpr, or FuncLit
	MatchMutScrut   bool // true on MatchExpr when any arm destructures with `&` mutable borrow
}

// ContainsKind identifies how a `when in <expr>` match pattern checks membership.
type ContainsKind int

const (
	ContainsInvalid ContainsKind = iota
	ContainsMethod               // static method call: Range[T], List[T], Array[T], Set[T], Map[K, V], string, user nominal type
	ContainsInterface            // dynamic dispatch through Containable[T] (or an interface with contains)
	ContainsBuiltinVec           // built-in vector `[T]` (ll_vec_contains__)
	ContainsBuiltinArr           // built-in stack array `[N: T]` (ll_arr_contains__)
	ContainsBuiltinSet           // built-in set `#[T]` (ll_set_has__)
	ContainsBuiltinMap           // built-in map `#[K: V]` (ll_map_has__)
)

// ContainsInfo stores semantic resolution results for `*ast.ContainsPattern`.
type ContainsInfo struct {
	Kind          ContainsKind
	ContainerType types.Type
	ElemType      types.Type
	WrapOptional  bool
	TargetFunc    *FuncInstance
	InterfaceMeth *types.InterfaceMethod
	BoxInterface  *types.Interface
}

// UnwindInfo records which enclosing construct an `*ast.UnwindStmt` jumps to.
type UnwindInfo struct {
	TargetID       int
	TargetKind     ast.UnwindTargetKind
	IsContinue     bool // true for `next <-` and `next label <-`
	WrapInOptional bool // true when `for <- val` yields `T` into a `?T` loop (or `?T` function)
	ResultType     types.Type
}

// VTableImpl describes a `(ConcreteType, Interface)` implementation pair needed at runtime.
type VTableImpl struct {
	Concrete types.Type
	Iface    *types.Interface
	Methods  []*FuncInstance // parallel to Iface.Methods (nil if satisfied by a built-in method like `to_string`)
}

// Module represents a loaded `.ll` file and its top-level namespace.
type Module struct {
	Path          string
	Dir           string
	File          *ast.File
	Doc           string
	Imports       []*Module            // unqualified `import "foo.ll"`
	Aliased       map[string]*Module   // `import "foo.ll" as alias`
	Types          map[string]types.Type
	GenericStructs    map[string]*GenericStructTemplate
	GenericEnums      map[string]*GenericEnumTemplate
	GenericInterfaces map[string]*GenericInterfaceTemplate
	Symbols        map[string]*Symbol
	GenericFuncs   map[string]*GenericFuncTemplate
	ExternHeaders  []string
}

// TopVarInit represents a non-function top-level `bind` constant to be emitted in C.
type TopVarInit struct {
	Sym   *Symbol
	Decl  *ast.VarDecl
	Mod   *Module
}

// CheckedProgram holds the completely validated, monomorphized semantic program ready for MIR lowering.
type CheckedProgram struct {
	EntryMod       *Module
	Modules        []*Module
	ExternHeaders  []string
	ExternFuncs    []*ExternFunc
	Structs        []*types.Struct
	StructDecls    map[*types.Struct]*ast.StructDecl
	StructMods     map[*types.Struct]*Module
	Enums          []*types.Enum
	EnumDecls      map[*types.Enum]*ast.EnumDecl
	EnumMods       map[*types.Enum]*Module
	Distincts      []*types.Distinct
	DistinctMods   map[*types.Distinct]*Module
	Interfaces     []*types.Interface
	InterfaceMods  map[*types.Interface]*Module
	TopVars        []*TopVarInit
	Funcs          []*FuncInstance
	Lambdas        []*LambdaInfo
	VTables        map[string]*VTableImpl
	MainFunc       *FuncInstance
	TestMainFunc   *FuncInstance
	TestFuncs      []*FuncInstance

	TopBodyMaps    *BodyMaps
	StructBodyMaps map[*types.Struct]*BodyMaps
	EnumBodyMaps   map[*types.Enum]*BodyMaps
	MethodTable    map[string]map[string]*FuncInstance
	GenMethods     map[string]map[string]*GenericFuncTemplate
	TypeSpans      map[types.Type]diag.Span
	StdDir         string
	CoreMod        *Module

	// Built-in interfaces
	IfacePrintable       *types.Interface
	IfaceCopyable        *types.Interface
	IfaceDrop            *types.Interface
	IfaceHashable        *types.Interface
	IfaceEquatable       *types.Interface
	IfaceComparable      *types.Interface
	IfaceCollectable     *types.Interface
	IfaceIterable        *types.Interface
	IfaceIterator        *types.Interface
	IfaceIndexable       *types.Interface
	IfaceKeyIndexable    *types.Interface
	IfaceContainable     *types.Interface
	GenIfaceCollectable  *GenericInterfaceTemplate
	GenIfaceIterable     *GenericInterfaceTemplate
	GenIfaceIterator     *GenericInterfaceTemplate
	GenIfaceIndexable    *GenericInterfaceTemplate
	GenIfaceKeyIndexable *GenericInterfaceTemplate
	GenIfaceContainable  *GenericInterfaceTemplate
	GenStructList        *GenericStructTemplate
	GenStructArray       *GenericStructTemplate
	GenStructSet         *GenericStructTemplate
	GenStructMap         *GenericStructTemplate
	GenStructRange       *GenericStructTemplate
	GenStructs           []*GenericStructTemplate
	GenInterfaces        []*GenericInterfaceTemplate

	implementsFn        func(concrete types.Type, ifc *types.Interface) bool
	isIndexableFn       func(t types.Type) bool
	indexableItemFn     func(t types.Type) types.Type
	isKeyIndexableFn    func(t types.Type) bool
	keyIndexableTypesFn func(t types.Type) (types.Type, types.Type)
	isIterableFn        func(t types.Type) bool
	iterableItemFn      func(t types.Type) types.Type
	isContainableFn     func(t types.Type) bool
	containableItemFn   func(t types.Type) types.Type
	instantiateIfcFn    func(tmpl *GenericInterfaceTemplate, typeArgs []types.Type, span diag.Span) *types.Interface
}

// Analyzer orchestrates all semantic passes.
type Analyzer struct {
	Rep          *diag.Reporter
	ProjectRoot  string
	StdDir       string
	Prog         *CheckedProgram
	curBM        *BodyMaps

	loadedMods   map[string]*Module
	loadingStack map[string]bool
	overlays     map[string]string
	ideMode      bool
	coreAbs      string
	coreMod      *Module

	// Method tables: key is canonical type key (e.g. `"Point"`, `"int"`, `"string"`, `"vec"`, `"Pair"`).
	methods      map[string]map[string]*FuncInstance
	genMethods   map[string]map[string]*GenericFuncTemplate

	inBodyPass   bool
	nextTargetID int
	nextLambdaID int

	topVarMap           map[*Symbol]*TopVarInit
	topVarStates        map[*TopVarInit]int // 0 = unvisited, 1 = visiting, 2 = visited
	orderedTopVars      []*TopVarInit
	deferredIfaceChecks []deferredIfaceCheck
	monoDepth           int
}

type deferredIfaceCheck struct {
	Concrete types.Type
	Iface    *types.Interface
	Span     diag.Span
	Context  string
}

// Check parses and semantically validates `entryPath` and all its transitive imports.
func Check(entryPath, srcOverride, projectRoot string, rep *diag.Reporter) *CheckedProgram {
	var overlays map[string]string
	if srcOverride != "" {
		absEntry, err := filepath.Abs(entryPath)
		if err != nil {
			absEntry = entryPath
		}
		overlays = map[string]string{absEntry: srcOverride}
	}
	return runCheck(entryPath, overlays, projectRoot, rep, false)
}

// CheckForIDE runs tolerant semantic analysis for LSP / IDE tooling:
// - Uses live in-memory `overlays` (including empty buffers and unsaved imported files).
// - Continues through type registration and body checking even when syntax or header errors exist,
//   so autocomplete, hover, go-to-definition, and real-time type/ownership diagnostics stay live while typing.
func CheckForIDE(entryPath string, overlays map[string]string, projectRoot string, rep *diag.Reporter) (prog *CheckedProgram) {
	normOverlays := make(map[string]string, len(overlays))
	for k, v := range overlays {
		if abs, err := filepath.Abs(k); err == nil {
			normOverlays[abs] = v
		} else {
			normOverlays[k] = v
		}
	}
	defer func() {
		if r := recover(); r != nil && prog == nil {
			prog = &CheckedProgram{
				StructDecls:    make(map[*types.Struct]*ast.StructDecl),
				StructMods:     make(map[*types.Struct]*Module),
				EnumDecls:      make(map[*types.Enum]*ast.EnumDecl),
				EnumMods:       make(map[*types.Enum]*Module),
				InterfaceMods:  make(map[*types.Interface]*Module),
				DistinctMods:   make(map[*types.Distinct]*Module),
				VTables:        make(map[string]*VTableImpl),
				TopBodyMaps:    NewBodyMaps(),
				StructBodyMaps: make(map[*types.Struct]*BodyMaps),
				EnumBodyMaps:   make(map[*types.Enum]*BodyMaps),
				MethodTable:    make(map[string]map[string]*FuncInstance),
				GenMethods:     make(map[string]map[string]*GenericFuncTemplate),
				TypeSpans:      make(map[types.Type]diag.Span),
			}
		}
	}()
	prog = runCheck(entryPath, normOverlays, projectRoot, rep, true)
	return prog
}

func runCheck(entryPath string, overlays map[string]string, projectRoot string, rep *diag.Reporter, ideMode bool) *CheckedProgram {
	absEntry, err := filepath.Abs(entryPath)
	if err != nil {
		absEntry = entryPath
	}
	if projectRoot == "" {
		projectRoot = findProjectRoot(filepath.Dir(absEntry))
	}
	stdDir := findStdDir(projectRoot, filepath.Dir(absEntry))

	topBM := NewBodyMaps()
	genMethods := make(map[string]map[string]*GenericFuncTemplate)
	prog := &CheckedProgram{
		StructDecls:    make(map[*types.Struct]*ast.StructDecl),
		StructMods:     make(map[*types.Struct]*Module),
		EnumDecls:      make(map[*types.Enum]*ast.EnumDecl),
		EnumMods:       make(map[*types.Enum]*Module),
		InterfaceMods:  make(map[*types.Interface]*Module),
		DistinctMods:   make(map[*types.Distinct]*Module),
		VTables:        make(map[string]*VTableImpl),
		TopBodyMaps:    topBM,
		StructBodyMaps: make(map[*types.Struct]*BodyMaps),
		EnumBodyMaps:   make(map[*types.Enum]*BodyMaps),
		MethodTable:    make(map[string]map[string]*FuncInstance),
		GenMethods:     genMethods,
		TypeSpans:      make(map[types.Type]diag.Span),
		StdDir:         stdDir,
	}

	a := &Analyzer{
		Rep:          rep,
		ProjectRoot:  projectRoot,
		StdDir:       stdDir,
		Prog:         prog,
		curBM:        topBM,
		loadedMods:   make(map[string]*Module),
		loadingStack: make(map[string]bool),
		overlays:     overlays,
		ideMode:      ideMode,
		methods:      make(map[string]map[string]*FuncInstance),
		genMethods:   genMethods,
	}

	a.initBuiltinInterfaces()

	prog.implementsFn = a.ImplementsInterface
	prog.isIndexableFn = a.isIndexableType
	prog.indexableItemFn = a.indexableItemType
	prog.isKeyIndexableFn = a.isKeyIndexableType
	prog.keyIndexableTypesFn = a.keyIndexableTypes
	prog.isIterableFn = a.isIterableType
	prog.iterableItemFn = a.iterableItemType
	prog.isContainableFn = a.isContainableType
	prog.containableItemFn = a.containableItemType
	prog.instantiateIfcFn = a.instantiateInterface

	// If std/core/core.ll exists (or fallback to std/core.ll), load it as the fundamental prelude module.
	coreAbs := filepath.Join(stdDir, "core", "core.ll")
	if st, err := os.Stat(coreAbs); err != nil || st.IsDir() {
		coreAbs = filepath.Join(stdDir, "core.ll")
	}
	if st, err := os.Stat(coreAbs); err == nil && !st.IsDir() {
		a.coreAbs = coreAbs
		a.coreMod = a.loadModule(coreAbs, diag.Span{})
		prog.CoreMod = a.coreMod
	}

	entryMod := a.loadModule(absEntry, diag.Span{})
	if entryMod == nil {
		return prog
	}
	prog.EntryMod = entryMod
	if rep.HasErrors() && !ideMode {
		return prog
	}

	a.runTypeAndSymbolRegistration()
	if rep.HasErrors() && !ideMode {
		return prog
	}

	a.runBodyChecking()
	if prog.EntryMod != nil {
		var entryTests []*FuncInstance
		for _, fi := range prog.TestFuncs {
			if fi.Mod == prog.EntryMod {
				entryTests = append(entryTests, fi)
			}
		}
		prog.TestFuncs = entryTests
	}
	return prog
}

func findProjectRoot(startDir string) string {
	if env := os.Getenv("LOWLIGHT_ROOT"); env != "" {
		if st, err := os.Stat(filepath.Join(env, "std")); err == nil && st.IsDir() {
			return env
		}
	}
	searchUp := func(from string) string {
		dir := from
		for dir != "" {
			if st, err := os.Stat(filepath.Join(dir, "std")); err == nil && st.IsDir() {
				return dir
			}
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		return ""
	}
	if r := searchUp(startDir); r != "" {
		return r
	}
	if cwd, err := os.Getwd(); err == nil {
		if r := searchUp(cwd); r != "" {
			return r
		}
	}
	if exe, err := os.Executable(); err == nil {
		if r := searchUp(filepath.Dir(exe)); r != "" {
			return r
		}
	}
	return startDir
}

func findStdDir(projectRoot, startDir string) string {
	isStdDir := func(dir string) bool {
		if dir == "" {
			return false
		}
		if st, err := os.Stat(filepath.Join(dir, "core", "core.ll")); err == nil && !st.IsDir() {
			return true
		}
		if st, err := os.Stat(filepath.Join(dir, "core.ll")); err == nil && !st.IsDir() {
			return true
		}
		return false
	}
	if projectRoot != "" {
		cand := filepath.Join(projectRoot, "std")
		if isStdDir(cand) {
			return cand
		}
	}
	if env := os.Getenv("LOWLIGHT_ROOT"); env != "" {
		cand := filepath.Join(env, "std")
		if isStdDir(cand) {
			return cand
		}
	}
	searchStdUp := func(from string) string {
		dir := from
		for dir != "" {
			cand := filepath.Join(dir, "std")
			if isStdDir(cand) {
				return cand
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		return ""
	}
	if r := searchStdUp(startDir); r != "" {
		return r
	}
	if cwd, err := os.Getwd(); err == nil {
		if r := searchStdUp(cwd); r != "" {
			return r
		}
	}
	if exe, err := os.Executable(); err == nil {
		if r := searchStdUp(filepath.Dir(exe)); r != "" {
			return r
		}
	}
	if projectRoot != "" {
		return filepath.Join(projectRoot, "std")
	}
	return filepath.Join(startDir, "std")
}

func (a *Analyzer) initBuiltinInterfaces() {
	a.Prog.IfacePrintable = &types.Interface{
		Name:  "Printable",
		CName: "ll_iface_Printable",
		Methods: []types.InterfaceMethod{{
			Name:     "to_string",
			RecvMode: types.ModeRead,
			Ret:      types.TypeString,
		}},
	}
	a.Prog.IfaceCopyable = &types.Interface{
		Name:     "Copyable",
		CName:    "ll_iface_Copyable",
		UsesSelf: true,
		Methods: []types.InterfaceMethod{{
			Name:     "copy",
			RecvMode: types.ModeRead,
			Ret:      types.TypeSelf,
		}},
	}
	a.Prog.IfaceDrop = &types.Interface{
		Name:  "Drop",
		CName: "ll_iface_Drop",
		Methods: []types.InterfaceMethod{{
			Name:     "drop",
			RecvMode: types.ModeMut,
			Ret:      types.TypeVoid,
		}},
	}
	a.Prog.IfaceEquatable = &types.Interface{
		Name:     "Equatable",
		CName:    "ll_iface_Equatable",
		UsesSelf: true,
		Methods: []types.InterfaceMethod{{
			Name:     "eq",
			RecvMode: types.ModeRead,
			Params:   []types.FuncParam{{Name: "other", Mode: types.ModeRead, Type: types.TypeSelf}},
			Ret:      types.TypeBool,
		}},
	}
	a.Prog.IfaceComparable = &types.Interface{
		Name:     "Comparable",
		CName:    "ll_iface_Comparable",
		UsesSelf: true,
		Methods: []types.InterfaceMethod{{
			Name:     "cmp",
			RecvMode: types.ModeRead,
			Params:   []types.FuncParam{{Name: "other", Mode: types.ModeRead, Type: types.TypeSelf}},
			Ret:      types.TypeInt,
		}},
	}
	a.Prog.Interfaces = append(a.Prog.Interfaces, a.Prog.IfacePrintable, a.Prog.IfaceCopyable, a.Prog.IfaceDrop, a.Prog.IfaceEquatable, a.Prog.IfaceComparable)
}

func (a *Analyzer) loadModule(absPath string, importSpan diag.Span) *Module {
	if existing, ok := a.loadedMods[absPath]; ok {
		return existing
	}
	if a.loadingStack[absPath] {
		a.Rep.Errorf(importSpan, "cyclic module import involving %q", absPath)
		return nil
	}
	a.loadingStack[absPath] = true
	defer delete(a.loadingStack, absPath)

	var astFile *ast.File
	if ov, hasOverlay := a.overlays[absPath]; hasOverlay {
		astFile = parser.ParseFile(absPath, ov, a.Rep)
	} else {
		info, statErr := os.Stat(absPath)
		if statErr == nil {
			diskASTMu.RLock()
			cached, ok := diskASTCache[absPath]
			diskASTMu.RUnlock()
			if ok && cached.modTimeNano == info.ModTime().UnixNano() && cached.size == info.Size() && cached.file != nil {
				astFile = cached.file
			}
		}
		if astFile == nil {
			bytes, err := os.ReadFile(absPath)
			if err != nil {
				a.Rep.Errorf(importSpan, "cannot read imported module %q: %v", absPath, err)
				return nil
			}
			diagsBefore := len(a.Rep.Diagnostics())
			astFile = parser.ParseFile(absPath, string(bytes), a.Rep)
			if statErr == nil && astFile != nil && len(a.Rep.Diagnostics()) == diagsBefore {
				diskASTMu.Lock()
				if len(diskASTCache) > 512 {
					diskASTCache = make(map[string]diskASTCacheEntry)
				}
				diskASTCache[absPath] = diskASTCacheEntry{
					modTimeNano: info.ModTime().UnixNano(),
					size:        info.Size(),
					file:        astFile,
				}
				diskASTMu.Unlock()
			}
		}
	}
	mod := &Module{
		Path:              absPath,
		Dir:               filepath.Dir(absPath),
		File:              astFile,
		Doc:               astFile.Doc,
		Aliased:           make(map[string]*Module),
		Types:          make(map[string]types.Type),
		GenericStructs:    make(map[string]*GenericStructTemplate),
		GenericEnums:      make(map[string]*GenericEnumTemplate),
		GenericInterfaces: make(map[string]*GenericInterfaceTemplate),
		Symbols:        make(map[string]*Symbol),
		GenericFuncs:   make(map[string]*GenericFuncTemplate),
	}
	// Pre-populate built-in interfaces in every module's type lookup
	mod.Types["Printable"] = a.Prog.IfacePrintable
	mod.Types["Copyable"] = a.Prog.IfaceCopyable
	mod.Types["Drop"] = a.Prog.IfaceDrop
	mod.Types["Equatable"] = a.Prog.IfaceEquatable
	mod.Types["Comparable"] = a.Prog.IfaceComparable

	a.loadedMods[absPath] = mod

	for _, d := range astFile.Decls {
		imp, ok := d.(*ast.ImportDecl)
		if !ok {
			continue
		}
		resolved := a.resolveImportPath(mod.Dir, imp.Path)
		if resolved == "" {
			a.Rep.Errorf(imp.Span, "cannot find module %q", imp.Path)
			continue
		}
		child := a.loadModule(resolved, imp.Span)
		if child == nil {
			continue
		}
		if imp.Alias != "" {
			if _, dup := mod.Aliased[imp.Alias]; dup {
				a.Rep.Errorf(imp.Span, "duplicate import alias %q", imp.Alias)
				continue
			}
			mod.Aliased[imp.Alias] = child
		} else {
			mod.Imports = append(mod.Imports, child)
		}
	}

	// Implicitly import core prelude modules for all modules outside the core package itself
	isCorePackageMod := a.coreMod != nil && (mod == a.coreMod || (mod.Dir != "" && a.coreMod.Dir != "" && filepath.Clean(mod.Dir) == filepath.Clean(a.coreMod.Dir)))
	if a.coreMod != nil && !isCorePackageMod {
		coreList := []*Module{a.coreMod}
		coreList = append(coreList, a.coreMod.Imports...)
		for _, cMod := range coreList {
			already := false
			for _, imp := range mod.Imports {
				if imp == cMod {
					already = true
					break
				}
			}
			if !already {
				mod.Imports = append(mod.Imports, cMod)
			}
		}
	}

	// Post-order append so dependencies precede dependents
	a.Prog.Modules = append(a.Prog.Modules, mod)
	return mod
}

func (a *Analyzer) resolveImportPath(fromDir, raw string) string {
	path := raw
	if !strings.HasSuffix(path, ".ll") {
		path += ".ll"
	}
	candidates := []string{
		filepath.Join(fromDir, path),
		filepath.Join(a.ProjectRoot, path),
	}
	if strings.HasPrefix(path, "std/") {
		candidates = append(candidates, filepath.Join(a.StdDir, strings.TrimPrefix(path, "std/")))
	}
	if path == "std/core.ll" || path == "core.ll" {
		candidates = append(candidates, filepath.Join(a.StdDir, "core", "core.ll"))
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if _, ok := a.overlays[abs]; ok {
			return abs
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			return abs
		}
	}
	return ""
}

func (a *Analyzer) allocTargetID() int {
	a.nextTargetID++
	return a.nextTargetID
}

func (a *Analyzer) allocLambdaID() int {
	a.nextLambdaID++
	return a.nextLambdaID
}

func (a *Analyzer) infoFor(e ast.Expr) *ExprInfo {
	if e == nil {
		return &ExprInfo{Type: types.TypeInvalid}
	}
	bm := a.curBM
	if bm == nil {
		bm = a.Prog.TopBodyMaps
	}
	if inf, ok := bm.ExprInfos[e]; ok {
		return inf
	}
	inf := &ExprInfo{Type: types.TypeInvalid}
	bm.ExprInfos[e] = inf
	return inf
}

// LookupConcreteMethod returns a concrete or monomorphized user method (`drop`, `copy`, `to_string`, etc.) on `t`, if any.
func (p *CheckedProgram) LookupConcreteMethod(t types.Type, methodName string) *FuncInstance {
	if p == nil || t == nil {
		return nil
	}
	if m, ok := p.MethodTable[TypeMethodKey(t)]; ok {
		return m[methodName]
	}
	return nil
}

// TypeMethodKey returns the lookup key used in `Analyzer.methods` for a given type.
func TypeMethodKey(t types.Type) string {
	switch v := t.(type) {
	case *types.Primitive:
		return v.String()
	case *types.Struct:
		return v.CName
	case *types.Enum:
		return v.CName
	case *types.Distinct:
		return v.CName
	default:
		return fmt.Sprintf("ty_%s", t.Mangled())
	}
}

// SamePackage reports whether two modules belong to the same package (directory).
func (prog *CheckedProgram) SamePackage(modA, modB *Module) bool {
	if modA == nil || modB == nil {
		return true
	}
	if modA == modB {
		return true
	}
	dirA := filepath.Clean(modA.Dir)
	dirB := filepath.Clean(modB.Dir)
	if dirA == "" || dirB == "" {
		return modA.Path == modB.Path
	}
	if prog != nil && prog.StdDir != "" && dirA == filepath.Clean(prog.StdDir) && dirB == filepath.Clean(prog.StdDir) {
		return modA.Path == modB.Path
	}
	return dirA == dirB
}

// SamePackageDir reports whether `curMod` and a target directory belong to the same package.
func (prog *CheckedProgram) SamePackageDir(curMod *Module, targetDir, targetPath string) bool {
	if curMod == nil || targetDir == "" {
		return true
	}
	dirCur := filepath.Clean(curMod.Dir)
	dirTarget := filepath.Clean(targetDir)
	if prog != nil && prog.StdDir != "" && dirCur == filepath.Clean(prog.StdDir) && dirTarget == filepath.Clean(prog.StdDir) {
		return curMod.Path == targetPath
	}
	return dirCur == dirTarget
}

// SamePackage reports whether two modules belong to the same package (directory).
func (a *Analyzer) SamePackage(modA, modB *Module) bool {
	if a.Prog != nil {
		return a.Prog.SamePackage(modA, modB)
	}
	if modA == nil || modB == nil || modA == modB {
		return true
	}
	return filepath.Clean(modA.Dir) == filepath.Clean(modB.Dir)
}

// SamePackageDir reports whether `curMod` and a target directory belong to the same package.
func (a *Analyzer) SamePackageDir(curMod *Module, targetDir, targetPath string) bool {
	if a.Prog != nil {
		return a.Prog.SamePackageDir(curMod, targetDir, targetPath)
	}
	if curMod == nil || targetDir == "" {
		return true
	}
	return filepath.Clean(curMod.Dir) == filepath.Clean(targetDir)
}

// typeModule returns the defining module of a given type, if known.
func (a *Analyzer) typeModule(t types.Type) *Module {
	if t == nil || a.Prog == nil {
		return nil
	}
	switch v := t.(type) {
	case *types.Struct:
		return a.Prog.StructMods[v]
	case *types.Enum:
		return a.Prog.EnumMods[v]
	case *types.Interface:
		return a.Prog.InterfaceMods[v]
	case *types.Distinct:
		return a.Prog.DistinctMods[v]
	case *types.Ptr:
		return a.typeModule(v.Elem)
	case *types.Ref:
		return a.typeModule(v.Elem)
	case *types.Optional:
		return a.typeModule(v.Elem)
	case *types.Vector:
		return a.typeModule(v.Elem)
	case *types.StackArray:
		return a.typeModule(v.Elem)
	default:
		return nil
	}
}

// -----------------------------------------------------------------------------
// Interface Reflection & Discovery API
// -----------------------------------------------------------------------------

// ImplementsInterface reports whether concrete satisfies all contracts of ifc.
func (p *CheckedProgram) ImplementsInterface(concrete types.Type, ifc *types.Interface) bool {
	if p == nil || concrete == nil || ifc == nil {
		return false
	}
	if p.implementsFn != nil {
		return p.implementsFn(concrete, ifc)
	}
	return false
}

// AllInterfaces returns all declared and built-in interfaces in the program.
func (p *CheckedProgram) AllInterfaces() []*types.Interface {
	if p == nil {
		return nil
	}
	var res []*types.Interface
	seen := make(map[string]bool)
	add := func(ifc *types.Interface) {
		if ifc != nil && !seen[ifc.Name] {
			seen[ifc.Name] = true
			res = append(res, ifc)
		}
	}
	add(p.IfacePrintable)
	add(p.IfaceCopyable)
	add(p.IfaceDrop)
	add(p.IfaceHashable)
	add(p.IfaceEquatable)
	add(p.IfaceComparable)
	add(p.IfaceCollectable)
	add(p.IfaceIterable)
	add(p.IfaceIterator)
	add(p.IfaceIndexable)
	add(p.IfaceKeyIndexable)
	add(p.IfaceContainable)
	for _, ifc := range p.Interfaces {
		add(ifc)
	}
	return res
}

// AllNominalTypes returns all declared structs, distinct types, and enums in the program.
func (p *CheckedProgram) AllNominalTypes() []types.Type {
	if p == nil {
		return nil
	}
	var res []types.Type
	seen := make(map[string]bool)
	add := func(t types.Type) {
		if t != nil && !seen[t.String()] {
			seen[t.String()] = true
			res = append(res, t)
		}
	}
	for _, st := range p.Structs {
		add(st)
	}
	for _, dt := range p.Distincts {
		add(dt)
	}
	for _, en := range p.Enums {
		add(en)
	}
	return res
}

// ImplementedInterfaces returns the interfaces implemented by concrete.
func (p *CheckedProgram) ImplementedInterfaces(concrete types.Type) []*types.Interface {
	if p == nil || concrete == nil {
		return nil
	}
	var res []*types.Interface
	seen := make(map[string]bool)
	add := func(ifc *types.Interface) {
		if ifc != nil && !seen[ifc.Name] {
			seen[ifc.Name] = true
			res = append(res, ifc)
		}
	}

	for _, ifc := range p.AllInterfaces() {
		if p.ImplementsInterface(concrete, ifc) {
			add(ifc)
		}
	}

	// Generic interfaces: Indexable, KeyIndexable, Iterable
	if p.isIndexableFn != nil && p.isIndexableFn(concrete) && p.GenIfaceIndexable != nil && p.instantiateIfcFn != nil {
		itemTy := p.indexableItemFn(concrete)
		if itemTy != nil && itemTy != types.TypeInvalid {
			inst := p.instantiateIfcFn(p.GenIfaceIndexable, []types.Type{itemTy}, diag.Span{})
			if inst != nil {
				add(inst)
			}
		}
	}
	if p.isKeyIndexableFn != nil && p.isKeyIndexableFn(concrete) && p.GenIfaceKeyIndexable != nil && p.instantiateIfcFn != nil {
		k, v := p.keyIndexableTypesFn(concrete)
		if k != nil && k != types.TypeInvalid && v != nil && v != types.TypeInvalid {
			inst := p.instantiateIfcFn(p.GenIfaceKeyIndexable, []types.Type{k, v}, diag.Span{})
			if inst != nil {
				add(inst)
			}
		}
	}
	if p.isIterableFn != nil && p.isIterableFn(concrete) && p.GenIfaceIterable != nil && p.instantiateIfcFn != nil {
		itemTy := p.iterableItemFn(concrete)
		if itemTy != nil && itemTy != types.TypeInvalid {
			inst := p.instantiateIfcFn(p.GenIfaceIterable, []types.Type{itemTy}, diag.Span{})
			if inst != nil {
				add(inst)
			}
		}
	}
	if p.isContainableFn != nil && p.isContainableFn(concrete) && p.GenIfaceContainable != nil && p.instantiateIfcFn != nil {
		itemTy := p.containableItemFn(concrete)
		if _, isOpt := itemTy.(*types.Optional); !isOpt && itemTy != nil && itemTy != types.TypeInvalid {
			inst := p.instantiateIfcFn(p.GenIfaceContainable, []types.Type{itemTy}, diag.Span{})
			if inst != nil {
				add(inst)
			}
		}
	}

	return res
}

// ImplementingTypes returns all declared nominal types that implement ifc.
func (p *CheckedProgram) ImplementingTypes(ifc *types.Interface) []types.Type {
	if p == nil || ifc == nil {
		return nil
	}
	var res []types.Type
	seen := make(map[string]bool)
	add := func(t types.Type) {
		if t != nil && !seen[t.String()] {
			seen[t.String()] = true
			res = append(res, t)
		}
	}

	for _, t := range p.AllNominalTypes() {
		if p.ImplementsInterface(t, ifc) {
			add(t)
		}
	}

	// Check core collections and primitives
	candidates := []types.Type{
		types.TypeString,
		&types.Vector{Elem: types.TypeInt},
		&types.StackArray{Size: 4, Elem: types.TypeInt},
		&types.Map{Key: types.TypeString, Value: types.TypeInt},
		&types.Set{Elem: types.TypeInt},
	}
	for _, c := range candidates {
		if p.ImplementsInterface(c, ifc) {
			add(c)
		}
	}

	return res
}

// ImplementationTarget describes a target interface or implementing type.
type ImplementationTarget struct {
	Name      string
	Span      diag.Span
	IsBuiltin bool
}

// AllGenericStructs returns all generic struct templates across all modules.
func (p *CheckedProgram) AllGenericStructs() []*GenericStructTemplate {
	if p == nil {
		return nil
	}
	var res []*GenericStructTemplate
	seen := make(map[string]bool)
	for _, m := range p.Modules {
		for _, gst := range m.GenericStructs {
			if !seen[gst.Decl.Name] {
				seen[gst.Decl.Name] = true
				res = append(res, gst)
			}
		}
	}
	for _, gst := range p.GenStructs {
		if !seen[gst.Decl.Name] {
			seen[gst.Decl.Name] = true
			res = append(res, gst)
		}
	}
	return res
}

// AllGenericInterfaces returns all generic interface templates across all modules.
func (p *CheckedProgram) AllGenericInterfaces() []*GenericInterfaceTemplate {
	if p == nil {
		return nil
	}
	var res []*GenericInterfaceTemplate
	seen := make(map[string]bool)
	for _, m := range p.Modules {
		for _, gi := range m.GenericInterfaces {
			if !seen[gi.Decl.Name] {
				seen[gi.Decl.Name] = true
				res = append(res, gi)
			}
		}
	}
	for _, gi := range p.GenInterfaces {
		if !seen[gi.Decl.Name] {
			seen[gi.Decl.Name] = true
			res = append(res, gi)
		}
	}
	return res
}

// GenericStructImplementedInterfaces returns all interfaces satisfied by a generic struct template.
func (p *CheckedProgram) GenericStructImplementedInterfaces(gst *GenericStructTemplate) []ImplementationTarget {
	if p == nil || gst == nil || gst.Decl == nil {
		return nil
	}
	var res []ImplementationTarget
	seen := make(map[string]bool)
	add := func(name string, sp diag.Span) {
		if name != "" && !seen[name] {
			seen[name] = true
			res = append(res, ImplementationTarget{Name: name, Span: sp})
		}
	}

	gMethods := p.GenMethods[gst.Decl.Name]

	// 1. Check Indexable[T]
	if p.GenIfaceIndexable != nil {
		hasLen := false
		hasGet := false
		hasHas := false
		if gMethods != nil {
			if m, ok := gMethods["len"]; ok && m.MethodDecl != nil && len(m.MethodDecl.Params) == 0 {
				hasLen = true
			}
			if m, ok := gMethods["get"]; ok && m.MethodDecl != nil && len(m.MethodDecl.Params) == 1 {
				hasGet = true
			}
			if _, ok := gMethods["has"]; ok {
				hasHas = true
			}
		}
		if gst.Decl.Name == "List" || gst.Decl.Name == "Array" || (gst.Decl.Name != "Map" && hasLen && hasGet && !hasHas) {
			add("Indexable[T]", p.GenIfaceIndexable.Decl.Span)
		}
	}

	// 2. Check KeyIndexable[K, V]
	if p.GenIfaceKeyIndexable != nil {
		hasLen := false
		hasGet := false
		hasHas := false
		if gMethods != nil {
			if m, ok := gMethods["len"]; ok && m.MethodDecl != nil && len(m.MethodDecl.Params) == 0 {
				hasLen = true
			}
			if _, ok := gMethods["get"]; ok {
				hasGet = true
			}
			if m, ok := gMethods["has"]; ok && m.MethodDecl != nil && len(m.MethodDecl.Params) == 1 {
				hasHas = true
			}
		}
		if gst.Decl.Name == "Map" || (hasLen && hasGet && hasHas) {
			add("KeyIndexable[K, V]", p.GenIfaceKeyIndexable.Decl.Span)
		}
	}

	// 3. Check Collectable[T]
	if p.GenIfaceCollectable != nil {
		hasPush := false
		if gMethods != nil {
			if m, ok := gMethods["push"]; ok && m.MethodDecl != nil && len(m.MethodDecl.Params) == 1 {
				hasPush = true
			}
		}
		if gst.Decl.Name == "List" || gst.Decl.Name == "Set" || hasPush {
			add("Collectable[T]", p.GenIfaceCollectable.Decl.Span)
		}
	}

	// 4. Check Iterable[T]
	if p.GenIfaceIterable != nil {
		hasIter := false
		if gMethods != nil {
			if _, ok := gMethods["iter"]; ok {
				hasIter = true
			}
		}
		if gst.Decl.Name == "List" || gst.Decl.Name == "Array" || hasIter {
			add("Iterable[T]", p.GenIfaceIterable.Decl.Span)
		} else if gst.Decl.Name == "Map" {
			add("Iterable[MapEntry[K, V]]", p.GenIfaceIterable.Decl.Span)
		}
	}

	// 5. Check Containable[T]
	if p.GenIfaceContainable != nil {
		hasContains := false
		if gMethods != nil {
			if m, ok := gMethods["contains"]; ok && m.MethodDecl != nil && len(m.MethodDecl.Params) == 1 {
				hasContains = true
			}
		}
		if gst.Decl.Name == "Map" {
			add("Containable[K]", p.GenIfaceContainable.Decl.Span)
		} else if gst.Decl.Name == "List" || gst.Decl.Name == "Array" || gst.Decl.Name == "Set" || gst.Decl.Name == "Range" || hasContains {
			add("Containable[T]", p.GenIfaceContainable.Decl.Span)
		}
	}

	// 6. Check Copyable
	if p.IfaceCopyable != nil {
		add("Copyable", p.TypeSpans[p.IfaceCopyable])
	}

	// 6. Check Drop
	if p.IfaceDrop != nil {
		hasDrop := false
		if gMethods != nil {
			if _, ok := gMethods["drop"]; ok {
				hasDrop = true
			}
		}
		if gst.Decl.Name == "List" || gst.Decl.Name == "Map" || hasDrop {
			add("Drop", p.TypeSpans[p.IfaceDrop])
		}
	}

	// 7. Check Equatable
	if p.IfaceEquatable != nil {
		add("Equatable", p.TypeSpans[p.IfaceEquatable])
	}

	// 8. Check any other generic interface templates
	for _, gi := range p.AllGenericInterfaces() {
		if gi == p.GenIfaceIndexable || gi == p.GenIfaceKeyIndexable || gi == p.GenIfaceCollectable || gi == p.GenIfaceIterable || gi == p.GenIfaceContainable {
			continue
		}
		allMatch := true
		for _, req := range gi.Decl.Methods {
			if gMethods == nil || gMethods[req.Name] == nil {
				allMatch = false
				break
			}
		}
		if allMatch && len(gi.Decl.Methods) > 0 {
			add(gi.Decl.Name, gi.Decl.Span)
		}
	}

	// 9. Check any non-generic interfaces
	for _, ifc := range p.AllInterfaces() {
		if ifc == p.IfaceCopyable || ifc == p.IfaceDrop || ifc == p.IfaceEquatable || strings.Contains(ifc.Name, "[") {
			continue
		}
		allMatch := true
		for _, req := range ifc.Methods {
			if gMethods == nil || gMethods[req.Name] == nil {
				allMatch = false
				break
			}
		}
		if allMatch && len(ifc.Methods) > 0 {
			add(ifc.Name, p.TypeSpans[ifc])
		}
	}

	return res
}

// TypeImplementedInterfaceTargets returns all interfaces satisfied by nominal type or generic struct.
func (p *CheckedProgram) TypeImplementedInterfaceTargets(name string, mod *Module) []ImplementationTarget {
	if p == nil || name == "" {
		return nil
	}
	if mod != nil {
		if gst, ok := mod.GenericStructs[name]; ok {
			return p.GenericStructImplementedInterfaces(gst)
		}
	}
	for _, gst := range p.AllGenericStructs() {
		if gst.Decl.Name == name {
			return p.GenericStructImplementedInterfaces(gst)
		}
	}

	var nom types.Type
	if mod != nil {
		nom = mod.Types[name]
	}
	if nom == nil {
		for _, t := range p.AllNominalTypes() {
			if t.String() == name {
				nom = t
				break
			}
		}
	}
	if nom != nil {
		var res []ImplementationTarget
		seen := make(map[string]bool)
		for _, ifc := range p.ImplementedInterfaces(nom) {
			if !seen[ifc.Name] {
				seen[ifc.Name] = true
				res = append(res, ImplementationTarget{
					Name: ifc.Name,
					Span: p.TypeSpans[ifc],
				})
			}
		}
		return res
	}

	return nil
}

// InterfaceImplementingTargets returns all types implementing the specified interface.
func (p *CheckedProgram) InterfaceImplementingTargets(ifcName string, mod *Module) []ImplementationTarget {
	if p == nil || ifcName == "" {
		return nil
	}
	var res []ImplementationTarget
	seen := make(map[string]bool)
	add := func(name string, sp diag.Span, isBuiltin bool) {
		if name != "" && !seen[name] {
			seen[name] = true
			res = append(res, ImplementationTarget{Name: name, Span: sp, IsBuiltin: isBuiltin})
		}
	}

	// 1. Check all generic structs in the program
	for _, gst := range p.AllGenericStructs() {
		ifcs := p.GenericStructImplementedInterfaces(gst)
		for _, it := range ifcs {
			if it.Name == ifcName || strings.HasPrefix(it.Name, ifcName+"[") {
				tName := gst.Decl.Name
				if len(gst.Decl.TypeParams) > 0 {
					var tpNames []string
					for _, tp := range gst.Decl.TypeParams {
						tpNames = append(tpNames, tp.Name)
					}
					tName = fmt.Sprintf("%s[%s]", gst.Decl.Name, strings.Join(tpNames, ", "))
				}
				add(tName, gst.Decl.Span, false)
				break
			}
		}
	}

	// 2. Check all nominal types in the program
	for _, t := range p.AllNominalTypes() {
		for _, ifc := range p.ImplementedInterfaces(t) {
			if ifc.Name == ifcName || strings.HasPrefix(ifc.Name, ifcName+"[") {
				add(t.String(), p.TypeSpans[t], false)
				break
			}
		}
	}

	// 3. Language built-ins
	switch ifcName {
	case "Indexable":
		add("Vector[T]", diag.Span{}, true)
		add("StackArray[N, T]", diag.Span{}, true)
		add("string", diag.Span{}, true)
	case "KeyIndexable":
		add("Map[K, V]", diag.Span{}, true)
	case "Collectable":
		add("Vector[T]", diag.Span{}, true)
		add("Set[T]", diag.Span{}, true)
	case "Iterable":
		add("Vector[T]", diag.Span{}, true)
		add("StackArray[N, T]", diag.Span{}, true)
		add("Set[T]", diag.Span{}, true)
		add("Map[K, V]", diag.Span{}, true)
		add("string", diag.Span{}, true)
	}

	return res
}
