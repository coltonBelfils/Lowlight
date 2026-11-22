// Package sema implements multi-file import resolution, type checking, generic
// monomorphization, ownership/move checking, borrow checking, arena region escape
// analysis, unsafe boundary enforcement, and control-flow exhaustiveness for Lowlight.
package sema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/manifest"
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
	// astRewriteMu serializes the few idempotent in-place disambiguation rewrites sema makes to
	// cached ASTs (`xs[i].m` / `fns[i](...)` first parsed as type arguments).
	astRewriteMu sync.Mutex
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
	Name           string // display name (`add`, `Point.length`, `Pair[int, string].swap`)
	CName          string // unique C function name (`ll_fn_add`, `ll_m_Point_length`)
	Sig            *types.Func
	IsMethod       bool
	IsStaticAssoc  bool
	RecvType       types.Type // non-nil for methods / associated functions
	RecvMode       ast.ReceiverMode
	ParamNames     []string // includes receiver `"self"` at index 0 if IsMethod && !IsStaticAssoc
	ParamSpans     []diag.Span
	ParamSyms      []*Symbol // resolved parameter symbols populated during body checking
	Body           ast.Expr
	Mod            *Module
	TypeSubst      map[string]types.Type
	FuncTargetID   int
	BodyMaps       *BodyMaps
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
	// MatchArms holds the expanded arms of result chains (the implicit `value` arm and the arms
	// `default panic` stands for). The shared AST is never rewritten, so parsed files can be
	// cached and checked concurrently.
	MatchArms map[*ast.MatchExpr][]ast.MatchArm
	ContainsPatterns    map[*ast.ContainsPattern]*ContainsInfo
	MethodPatterns      map[*ast.MethodPattern]*MethodPatternInfo
	BareVariantPatterns map[*ast.MethodPattern]*ast.VariantPattern
}

// NewBodyMaps allocates a fresh set of per-body AST side tables.
func NewBodyMaps() *BodyMaps {
	return &BodyMaps{
		ExprInfos:           make(map[ast.Expr]*ExprInfo),
		UnwindInfos:         make(map[*ast.UnwindStmt]*UnwindInfo),
		VarSymbols:          make(map[*ast.VarStmt]*Symbol),
		UnpackVarSymbols:    make(map[*ast.VarStmt][]*Symbol),
		ForSymbols:          make(map[*ast.ForExpr][2]*Symbol),
		WhileSymbols:        make(map[*ast.WhileExpr][2]*Symbol),
		IfUnwrapSyms:        make(map[*ast.IfExpr]*Symbol),
		IfOriginSyms:        make(map[*ast.IfExpr]*Symbol),
		PatternSyms:         make(map[*ast.VariantPattern][]*Symbol),
		MatchArms:           make(map[*ast.MatchExpr][]ast.MatchArm),
		ContainsPatterns:    make(map[*ast.ContainsPattern]*ContainsInfo),
		MethodPatterns:      make(map[*ast.MethodPattern]*MethodPatternInfo),
		BareVariantPatterns: make(map[*ast.MethodPattern]*ast.VariantPattern),
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
	CallDirectFunc      CallKind = iota // static `bind` function or monomorphized generic function
	CallClosureValue                    // calling a function-typed variable/expression
	CallExternC                         // calling an `extern` C ABI function
	CallTypeConvert                     // `float(x)`, `u8(x)`, `int(Status.Pending)`, `UserID(42)`
	CallInterfaceBox                    // `Shape(my_circle)` -> owned boxed interface value
	CallBuiltinFunc                     // `print`, `println`, `eprint`, `eprintln`, `panic`, `assert`, `parse_int`, etc.
	CallBuiltinMethod                   // built-in method on primitive/string/vec/array/set/map/Arena/enum/struct
	CallInterfaceMethod                 // dynamic dispatch through interface fat pointer vtable
	CallIntrinsic                       // `size_of`, `align_of`, `alloc_raw`, `ptr_read`, etc.
	CallEnumVariant                     // tuple-payload enum variant constructor (`Result.Ok(42)`)
)

// ExprInfo stores semantic resolution results per AST expression node.
type ExprInfo struct {
	Type         types.Type
	Sym          *Symbol          // non-nil if IdentExpr resolved to a Symbol
	WrapOptional bool             // true if expression of type `T` is implicitly wrapped into `?T`
	BoxInterface *types.Interface // non-nil if concrete value is implicitly/explicitly boxed or borrowed as Interface
	BorrowAsView bool             // true if string literal, slice, or stack array is passed as zero-copy view
	MovedLocal   *Symbol          // non-nil if evaluating this expression moves ownership out of a local variable

	// Call resolution (for *ast.CallExpr)
	CallKind       CallKind
	TargetFunc     *FuncInstance
	TargetExtern   *ExternFunc
	BuiltinName    string
	InterfaceMeth  *types.InterfaceMethod
	MethodRecv     ast.Expr // receiver expression for UFCS dot-method calls
	MethodRecvMode types.ParamMode
	IntrinsicType  types.Type // concrete type argument `T` for `size_of[T]`, `alloc_raw[T]`, `ptr_cast[To]`, etc.

	// Member resolution (for *ast.MemberExpr)
	EnumVariant    *types.EnumVariant
	EnumType       *types.Enum
	IsOptChain     bool // true when `.` / `?.` short-circuits on an optional base (`?Base -> ?Result`)
	DerefStructPtr bool // true when accessing field on boxed `?RecursiveStruct` or `ref[Struct]`
	TupleFieldIdx  int  // 0-based field index when MemberExpr or IndexExpr accesses a tuple element

	// Struct literal resolution (for *ast.StructLit)
	StructType *types.Struct

	// Lambda resolution (for *ast.FuncLit)
	Lambda *LambdaInfo

	// Target ID for control-flow constructs (`BlockExpr`, `IfExpr`, `MatchExpr`, `ForExpr`)
	TargetID        int
	YieldArenaDepth int  // max ArenaDepth yielded/captured by BlockExpr, IfExpr, MatchExpr, ForExpr, or FuncLit
	MatchMutScrut   bool // true on MatchExpr when any arm destructures with `&` mutable borrow
}

// ContainsKind identifies how a `when in <expr>` match pattern checks membership.
type ContainsKind int

const (
	ContainsInvalid    ContainsKind = iota
	ContainsMethod                  // static method call: Range[T], List[T], Array[T], Set[T], Map[K, V], string, user nominal type
	ContainsInterface               // dynamic dispatch through Containable[T] (or an interface with contains)
	ContainsBuiltinVec              // built-in vector `[T]` (ll_vec_contains__)
	ContainsBuiltinArr              // built-in stack array `[N: T]` (ll_arr_contains__)
	ContainsBuiltinSet              // built-in set `#[T]` (ll_set_has__)
	ContainsBuiltinMap              // built-in map `#[K: V]` (ll_map_has__)
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

// MethodPatternInfo stores semantic resolution results for `*ast.MethodPattern`.
type MethodPatternInfo struct {
	Call     *ast.CallExpr
	RecvExpr *ast.IdentExpr
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

// Package represents a cohesive directory-as-package unit (or an isolated standalone script)
// sharing a single top-level declaration namespace (`SPECIFICATION.md §4.1`).
type Package struct {
	ID                string // Canonical PackageID (e.g. "std/core", "std/fs", "myapp/lexer", "dep/jsonx")
	Name              string // Normalized default import qualifier identifier
	Dir               string // Canonical physical directory path
	Tag               string // Collision-free C/LLVM symbol prefix ("" for EntryPkg and CorePkg)
	IsStd             bool
	IsCore            bool
	IsEntry           bool
	IsStandalone      bool
	IsIntegrationTest bool
	HasMain           bool
	PrimaryMod        *Module
	Files             []*Module

	// Shared package-wide symbol tables referenced by every `*Module` belonging to this package.
	Types             map[string]types.Type
	GenericStructs    map[string]*GenericStructTemplate
	GenericEnums      map[string]*GenericEnumTemplate
	GenericInterfaces map[string]*GenericInterfaceTemplate
	Symbols           map[string]*Symbol
	GenericFuncs      map[string]*GenericFuncTemplate
}

// Module represents a single loaded `.ll` source file within a Package.
// Its top-level type/symbol maps alias `Pkg`'s shared maps so all sibling files in a
// package directory share one unified namespace while keeping `Aliased` imports file-scoped.
type Module struct {
	Path              string
	Dir               string
	Pkg               *Package
	IsTestFile        bool // true for `*_test.ll`
	File              *ast.File
	Doc               string
	Imports           []*Module          // implicit `std/core` prelude only (zero open user imports)
	Aliased           map[string]*Module // file-scoped qualified `import "pkg" [as alias]` -> childPkg.PrimaryMod
	DevAliased        map[string]bool    // true for qualifiers originating from `[dev-dependencies]`
	Types             map[string]types.Type
	GenericStructs    map[string]*GenericStructTemplate
	GenericEnums      map[string]*GenericEnumTemplate
	GenericInterfaces map[string]*GenericInterfaceTemplate
	Symbols           map[string]*Symbol
	GenericFuncs      map[string]*GenericFuncTemplate
	ExternHeaders     []string
}

// TopVarInit represents a non-function top-level `bind` constant to be emitted in C.
type TopVarInit struct {
	Sym  *Symbol
	Decl *ast.VarDecl
	Mod  *Module
}

// CheckedProgram holds the completely validated, monomorphized semantic program ready for MIR lowering.
type CheckedProgram struct {
	EntryMod      *Module
	EntryPkg      *Package
	Modules       []*Module
	Packages      []*Package
	PackageByID   map[string]*Package
	ExternHeaders []string
	ExternFuncs   []*ExternFunc
	Structs       []*types.Struct
	StructDecls   map[*types.Struct]*ast.StructDecl
	StructMods    map[*types.Struct]*Module
	Enums         []*types.Enum
	EnumDecls     map[*types.Enum]*ast.EnumDecl
	EnumMods      map[*types.Enum]*Module
	Distincts     []*types.Distinct
	DistinctMods  map[*types.Distinct]*Module
	Interfaces    []*types.Interface
	InterfaceMods map[*types.Interface]*Module
	TopVars       []*TopVarInit
	Funcs         []*FuncInstance
	Lambdas       []*LambdaInfo
	VTables       map[string]*VTableImpl
	MainFunc      *FuncInstance
	TestMainFunc  *FuncInstance
	TestFuncs     []*FuncInstance

	TopBodyMaps    *BodyMaps
	StructBodyMaps map[*types.Struct]*BodyMaps
	EnumBodyMaps   map[*types.Enum]*BodyMaps
	MethodTable    map[string]map[string]*FuncInstance
	GenMethods     map[string]map[string]*GenericFuncTemplate
	TypeSpans      map[types.Type]diag.Span
	StdDir         string
	CoreMod        *Module
	CorePkg        *Package
	ManifestCtx    *manifest.ProjectContext
	ResolvedDeps   *manifest.ResolvedGraph
	Target         manifest.TargetInfo

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

// CheckOptions configures package-aware semantic analysis (`SPECIFICATION.md §4, §12`).
type CheckOptions struct {
	EntryPath    string
	SrcOverride  string
	Overlays     map[string]string
	ProjectRoot  string
	StdDir       string
	TargetTriple string
	TestMode     bool
	IDEMode      bool
	ManifestCtx  *manifest.ProjectContext
	ResolvedDeps *manifest.ResolvedGraph
	CacheRoot    string
	Locked       bool
	Offline      bool
}

// Analyzer orchestrates all semantic passes.
type Analyzer struct {
	Rep             *diag.Reporter
	ProjectRoot     string
	StdDir          string
	Prog            *CheckedProgram
	curBM           *BodyMaps
	target          manifest.TargetInfo
	testMode        bool
	ideMode         bool
	manifestCtx     *manifest.ProjectContext
	resolvedDeps    *manifest.ResolvedGraph
	overlays        map[string]string
	loadedPkgs      map[string]*Package // canonical clean dir (or file path for standalone) -> Package
	loadingPkgSet   map[string]bool     // Package.ID -> true while resolving imports
	loadingPkgStack []string            // ordered Package.ID stack for cycle chain diagnostics
	usedTags        map[string]string   // sanitized C/LLVM tag -> Package.ID
	corePkg         *Package
	coreMod         *Module

	// Method tables: key is canonical type key (e.g. `"Point"`, `"int"`, `"string"`, `"vec"`, `"Pair"`).
	methods    map[string]map[string]*FuncInstance
	genMethods map[string]map[string]*GenericFuncTemplate
	// genDropRecv names the generic structs and enums that declare a `drop` method. It is filled
	// before any type is instantiated, so instances created while struct fields and signatures are
	// resolved (before methods are registered) still know they are resources.
	genDropRecv map[string]bool
	// pendingDrops are those early instances; their `drop` is instantiated once methods exist.
	pendingDrops []types.Type

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

// Check parses and semantically validates `entryPath` (file or package directory) and all transitive imports.
func Check(entryPath, srcOverride, projectRoot string, rep *diag.Reporter) *CheckedProgram {
	return CheckWithOptions(CheckOptions{
		EntryPath:   entryPath,
		SrcOverride: srcOverride,
		ProjectRoot: projectRoot,
	}, rep)
}

// CheckWithOptions runs package-aware semantic analysis configured by `opts`.
func CheckWithOptions(opts CheckOptions, rep *diag.Reporter) *CheckedProgram {
	overlays := make(map[string]string, len(opts.Overlays)+1)
	for k, v := range opts.Overlays {
		if abs, err := filepath.Abs(k); err == nil {
			overlays[abs] = v
		} else {
			overlays[k] = v
		}
	}
	if opts.SrcOverride != "" && opts.EntryPath != "" {
		absEntry, err := filepath.Abs(opts.EntryPath)
		if err != nil {
			absEntry = opts.EntryPath
		}
		overlays[absEntry] = opts.SrcOverride
	}
	opts.Overlays = overlays
	return runCheckWithOptions(opts, rep)
}

// CheckForIDE runs tolerant semantic analysis for LSP / IDE tooling:
//   - Uses live in-memory `overlays` (including empty buffers and unsaved imported files).
//   - Continues through type registration and body checking even when syntax or header errors exist,
//     so autocomplete, hover, go-to-definition, and real-time type/ownership diagnostics stay live while typing.
func CheckForIDE(entryPath string, overlays map[string]string, projectRoot string, rep *diag.Reporter) (prog *CheckedProgram) {
	defer func() {
		if r := recover(); r != nil && prog == nil {
			prog = &CheckedProgram{
				PackageByID:    make(map[string]*Package),
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
	prog = CheckWithOptions(CheckOptions{
		EntryPath:   entryPath,
		Overlays:    overlays,
		ProjectRoot: projectRoot,
		TestMode:    manifest.IsTestFile(entryPath),
		IDEMode:     true,
	}, rep)
	return prog
}

func runCheckWithOptions(opts CheckOptions, rep *diag.Reporter) *CheckedProgram {
	absEntry, err := filepath.Abs(opts.EntryPath)
	if err != nil {
		absEntry = opts.EntryPath
	}

	entryIsDir := false
	if st, statErr := os.Stat(absEntry); statErr == nil && st.IsDir() {
		entryIsDir = true
	}
	entryDir := absEntry
	if !entryIsDir {
		entryDir = filepath.Dir(absEntry)
	}

	// Automatically enable test mode if explicitly checking a `*_test.ll` file.
	if !entryIsDir && manifest.IsTestFile(absEntry) {
		opts.TestMode = true
	}

	// Discover `lowlight.toml` / `[workspace]` context if not provided by caller.
	mCtx := opts.ManifestCtx
	if mCtx == nil {
		var discErr error
		mCtx, discErr = manifest.DiscoverProject(absEntry)
		if discErr != nil {
			rep.Errorf(diag.Span{}, "failed to load lowlight.toml: %v", discErr)
		}
	}

	target := manifest.ParseTarget(opts.TargetTriple)
	if opts.TargetTriple == "" && mCtx != nil && mCtx.ActiveManifest != nil && mCtx.ActiveManifest.Build.Target != "" {
		target = manifest.ParseTarget(mCtx.ActiveManifest.Build.Target)
	}

	projectRoot := opts.ProjectRoot
	if projectRoot == "" {
		if mCtx != nil && mCtx.RootDir() != "" {
			projectRoot = mCtx.RootDir()
		} else {
			projectRoot = findProjectRoot(entryDir)
		}
	}

	stdDir := opts.StdDir
	if stdDir == "" {
		stdDir = manifest.FindStdDir(opts.ProjectRoot, entryDir)
		if stdDir == "" || !dirExists(stdDir) {
			stdDir = findStdDir(projectRoot, entryDir)
		}
	}
	if absStd, err := filepath.Abs(stdDir); err == nil {
		stdDir = absStd
	}

	// Resolve manifest dependencies offline (zero implicit network during compilation per §12.5).
	resolvedDeps := opts.ResolvedDeps
	if mCtx != nil && resolvedDeps == nil {
		var resErr error
		resolvedDeps, resErr = manifest.Resolve(mCtx, manifest.ResolveOptions{
			Target:       target,
			IncludeDev:   opts.TestMode || opts.IDEMode,
			AllowNetwork: false,
			Locked:       opts.Locked,
			Offline:      opts.Offline,
			CacheRoot:    opts.CacheRoot,
		})
		if resErr != nil && !opts.IDEMode {
			rep.Errorf(diag.Span{}, "dependency resolution failed: %v", resErr)
		}
	}

	topBM := NewBodyMaps()
	genMethods := make(map[string]map[string]*GenericFuncTemplate)
	prog := &CheckedProgram{
		PackageByID:    make(map[string]*Package),
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
		ManifestCtx:    mCtx,
		ResolvedDeps:   resolvedDeps,
		Target:         target,
	}

	a := &Analyzer{
		Rep:           rep,
		ProjectRoot:   projectRoot,
		StdDir:        stdDir,
		Prog:          prog,
		curBM:         topBM,
		target:        target,
		testMode:      opts.TestMode,
		ideMode:       opts.IDEMode,
		manifestCtx:   mCtx,
		resolvedDeps:  resolvedDeps,
		overlays:      opts.Overlays,
		loadedPkgs:    make(map[string]*Package),
		loadingPkgSet: make(map[string]bool),
		usedTags:      make(map[string]string),
		methods:       make(map[string]map[string]*FuncInstance),
		genMethods:    genMethods,
		genDropRecv:   make(map[string]bool),
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

	// Determine whether the entry itself is `std/core`.
	coreDir := filepath.Join(stdDir, "core")
	entryIsCore := dirExists(coreDir) && filepath.Clean(entryDir) == filepath.Clean(coreDir)

	// Load implicit `std/core` directory package first (unless entry IS `std/core`).
	if dirExists(coreDir) && !entryIsCore {
		a.corePkg = a.loadPackageDir(coreDir, "std/core", "core", true, true, false, false, diag.Span{})
		if a.corePkg != nil {
			a.coreMod = a.corePkg.PrimaryMod
			prog.CorePkg = a.corePkg
			prog.CoreMod = a.coreMod
		}
	}

	// Load entry package (directory-as-package for directories, manifest projects, and std subdirs;
	// isolated single-file package for standalone `.ll` scripts outside manifests per §4.1).
	var entryPkg *Package
	var entryMod *Module
	if entryIsCore {
		entryPkg = a.loadPackageDir(coreDir, "std/core", "core", true, true, true, false, diag.Span{})
		if entryPkg != nil {
			a.corePkg = entryPkg
			a.coreMod = entryPkg.PrimaryMod
			prog.CorePkg = entryPkg
			prog.CoreMod = entryPkg.PrimaryMod
			entryMod = entryPkg.PrimaryMod
			if !entryIsDir {
				for _, f := range entryPkg.Files {
					if filepath.Clean(f.Path) == filepath.Clean(absEntry) {
						entryMod = f
						break
					}
				}
			}
		}
	} else {
		useDirPackage := entryIsDir || mCtx != nil || a.isInsideStdSubdir(absEntry)
		if useDirPackage {
			pkgID, pkgName, isStd, isIntegTest := a.classifyPackageDir(entryDir)
			entryPkg = a.loadPackageDir(entryDir, pkgID, pkgName, isStd, false, true, isIntegTest, diag.Span{})
			if entryPkg != nil {
				entryMod = entryPkg.PrimaryMod
				if !entryIsDir {
					for _, f := range entryPkg.Files {
						if filepath.Clean(f.Path) == filepath.Clean(absEntry) {
							entryMod = f
							break
						}
					}
				}
			}
		} else {
			entryPkg = a.loadStandaloneFile(absEntry, diag.Span{})
			if entryPkg != nil {
				entryMod = entryPkg.PrimaryMod
			}
		}
	}

	if entryPkg == nil || entryMod == nil {
		return prog
	}
	prog.EntryPkg = entryPkg
	prog.EntryMod = entryMod
	if rep.HasErrors() && !opts.IDEMode {
		return prog
	}

	a.runTypeAndSymbolRegistration()
	if rep.HasErrors() && !opts.IDEMode {
		return prog
	}

	a.runBodyChecking()
	if prog.EntryPkg != nil {
		var entryTests []*FuncInstance
		for _, fi := range prog.TestFuncs {
			if prog.IsEntryPackageMod(fi.Mod) {
				entryTests = append(entryTests, fi)
			}
		}
		prog.TestFuncs = entryTests
	}
	return prog
}

func findProjectRoot(startDir string) string {
	if ctx, err := manifest.DiscoverProject(startDir); err == nil && ctx != nil && ctx.RootDir() != "" {
		return ctx.RootDir()
	}
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

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func (a *Analyzer) isInsideStdSubdir(absPath string) bool {
	if a.StdDir == "" {
		return false
	}
	cleanStd := filepath.Clean(a.StdDir)
	dir := filepath.Clean(filepath.Dir(absPath))
	if dir == cleanStd {
		return false
	}
	return strings.HasPrefix(dir, cleanStd+string(filepath.Separator))
}

func (a *Analyzer) classifyPackageDir(dir string) (pkgID, pkgName string, isStd, isIntegTest bool) {
	cleanDir := filepath.Clean(dir)
	cleanStd := filepath.Clean(a.StdDir)
	if a.StdDir != "" && strings.HasPrefix(cleanDir, cleanStd+string(filepath.Separator)) {
		rel, err := filepath.Rel(cleanStd, cleanDir)
		if err == nil {
			relSlash := filepath.ToSlash(rel)
			return "std/" + relSlash, manifest.NormalizeQualifier(path.Base(relSlash)), true, false
		}
	}
	if a.manifestCtx != nil {
		// Check if inside a workspace member or active manifest.
		if a.manifestCtx.ActiveManifest != nil && a.manifestCtx.ActiveManifest.HasPackage {
			mDir := filepath.Clean(a.manifestCtx.ActiveManifest.Dir)
			if cleanDir == mDir {
				return a.manifestCtx.ActiveManifest.Package.Name, manifest.NormalizeQualifier(a.manifestCtx.ActiveManifest.Package.Name), false, false
			}
			if strings.HasPrefix(cleanDir, mDir+string(filepath.Separator)) {
				rel, err := filepath.Rel(mDir, cleanDir)
				if err == nil {
					relSlash := filepath.ToSlash(rel)
					isTests := relSlash == "tests" || strings.HasPrefix(relSlash, "tests/")
					return a.manifestCtx.ActiveManifest.Package.Name + "/" + relSlash, manifest.NormalizeQualifier(path.Base(relSlash)), false, isTests
				}
			}
		}
		if a.manifestCtx.WorkspaceManifest != nil {
			wDir := filepath.Clean(a.manifestCtx.WorkspaceManifest.Dir)
			if strings.HasPrefix(cleanDir, wDir+string(filepath.Separator)) {
				rel, err := filepath.Rel(wDir, cleanDir)
				if err == nil {
					relSlash := filepath.ToSlash(rel)
					isTests := relSlash == "tests" || strings.HasPrefix(relSlash, "tests/")
					return relSlash, manifest.NormalizeQualifier(path.Base(relSlash)), false, isTests
				}
			}
		}
	}
	if a.ProjectRoot != "" {
		cleanRoot := filepath.Clean(a.ProjectRoot)
		if cleanDir == cleanRoot {
			base := filepath.Base(cleanDir)
			return base, manifest.NormalizeQualifier(base), false, false
		}
		if strings.HasPrefix(cleanDir, cleanRoot+string(filepath.Separator)) {
			rel, err := filepath.Rel(cleanRoot, cleanDir)
			if err == nil {
				relSlash := filepath.ToSlash(rel)
				return relSlash, manifest.NormalizeQualifier(path.Base(relSlash)), false, false
			}
		}
	}
	base := filepath.Base(cleanDir)
	return filepath.ToSlash(cleanDir), manifest.NormalizeQualifier(base), false, false
}

func sanitizePkgTagPart(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
			prevUnderscore = false
		} else if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	res := strings.Trim(b.String(), "_")
	if res == "" {
		return "pkg"
	}
	if res[0] >= '0' && res[0] <= '9' {
		res = "p_" + res
	}
	return res
}

func (a *Analyzer) assignPackageTag(pkg *Package) string {
	if pkg == nil || pkg.IsEntry || pkg.IsCore {
		return ""
	}
	sum := sha256.Sum256([]byte(pkg.ID))
	hashHex := hex.EncodeToString(sum[:4])
	var base string
	if strings.HasPrefix(pkg.ID, "/") || (len(pkg.ID) >= 2 && pkg.ID[1] == ':') || len(pkg.ID) > 48 {
		base = sanitizePkgTagPart(pkg.Name) + "_" + hashHex
	} else {
		base = sanitizePkgTagPart(pkg.ID)
	}
	if prev, taken := a.usedTags[base]; !taken || prev == pkg.ID {
		a.usedTags[base] = pkg.ID
		return base
	}
	tag := base + "_" + hashHex
	a.usedTags[tag] = pkg.ID
	return tag
}

func (a *Analyzer) parseSourceFile(absPath string, importSpan diag.Span) *ast.File {
	if ov, hasOverlay := a.overlays[absPath]; hasOverlay {
		return parser.ParseFile(absPath, ov, a.Rep)
	}
	info, statErr := os.Stat(absPath)
	if statErr == nil {
		diskASTMu.RLock()
		cached, ok := diskASTCache[absPath]
		diskASTMu.RUnlock()
		if ok && cached.modTimeNano == info.ModTime().UnixNano() && cached.size == info.Size() && cached.file != nil {
			return cached.file
		}
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		a.Rep.Errorf(importSpan, "cannot read source file %q: %v", absPath, err)
		return nil
	}
	diagsBefore := len(a.Rep.Diagnostics())
	astFile := parser.ParseFile(absPath, string(raw), a.Rep)
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
	return astFile
}

func hasTopLevelMain(f *ast.File) bool {
	if f == nil {
		return false
	}
	for _, d := range f.Decls {
		if vd, ok := d.(*ast.VarDecl); ok && vd.IsBind && vd.Name == "main" {
			if _, isFn := vd.Value.(*ast.FuncLit); isFn {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) newPackage(pkgID, pkgName, pkgDir string, isStd, isCore, isEntry, isStandalone, isIntegTest bool) *Package {
	pkg := &Package{
		ID:                pkgID,
		Name:              pkgName,
		Dir:               filepath.Clean(pkgDir),
		IsStd:             isStd,
		IsCore:            isCore,
		IsEntry:           isEntry,
		IsStandalone:      isStandalone,
		IsIntegrationTest: isIntegTest,
		Types:             make(map[string]types.Type),
		GenericStructs:    make(map[string]*GenericStructTemplate),
		GenericEnums:      make(map[string]*GenericEnumTemplate),
		GenericInterfaces: make(map[string]*GenericInterfaceTemplate),
		Symbols:           make(map[string]*Symbol),
		GenericFuncs:      make(map[string]*GenericFuncTemplate),
	}
	pkg.Tag = a.assignPackageTag(pkg)
	// Pre-populate built-in interfaces once on the shared package type table.
	pkg.Types["Printable"] = a.Prog.IfacePrintable
	pkg.Types["Copyable"] = a.Prog.IfaceCopyable
	pkg.Types["Drop"] = a.Prog.IfaceDrop
	pkg.Types["Equatable"] = a.Prog.IfaceEquatable
	pkg.Types["Comparable"] = a.Prog.IfaceComparable
	return pkg
}

func (a *Analyzer) newModuleInPkg(pkg *Package, absPath string, astFile *ast.File, isTestFile bool) *Module {
	doc := ""
	if astFile != nil {
		doc = astFile.Doc
	}
	return &Module{
		Path:              absPath,
		Dir:               pkg.Dir,
		Pkg:               pkg,
		IsTestFile:        isTestFile,
		File:              astFile,
		Doc:               doc,
		Aliased:           make(map[string]*Module),
		DevAliased:        make(map[string]bool),
		Types:             pkg.Types,
		GenericStructs:    pkg.GenericStructs,
		GenericEnums:      pkg.GenericEnums,
		GenericInterfaces: pkg.GenericInterfaces,
		Symbols:           pkg.Symbols,
		GenericFuncs:      pkg.GenericFuncs,
	}
}

func (a *Analyzer) collectPackageFiles(pkgDir string, includeTests bool) []string {
	cleanDir := filepath.Clean(pkgDir)
	seen := make(map[string]bool)
	var files []string

	consider := func(fullPath string) {
		abs, err := filepath.Abs(fullPath)
		if err != nil {
			abs = fullPath
		}
		if seen[abs] {
			return
		}
		base := filepath.Base(abs)
		if !strings.HasSuffix(base, ".ll") {
			return
		}
		if manifest.IsTestFile(base) && !includeTests {
			return
		}
		if !manifest.MatchesTargetSuffix(base, a.target) {
			return
		}
		seen[abs] = true
		files = append(files, abs)
	}

	if entries, err := os.ReadDir(cleanDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			consider(filepath.Join(cleanDir, e.Name()))
		}
	}
	for ovPath := range a.overlays {
		if filepath.Clean(filepath.Dir(ovPath)) == cleanDir {
			consider(ovPath)
		}
	}
	sort.Strings(files)
	return files
}

func (a *Analyzer) loadStandaloneFile(absPath string, importSpan diag.Span) *Package {
	cleanPath := filepath.Clean(absPath)
	if existing, ok := a.loadedPkgs[cleanPath]; ok {
		return existing
	}
	astFile := a.parseSourceFile(absPath, importSpan)
	if astFile == nil {
		return nil
	}
	base := strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
	pkg := a.newPackage(cleanPath, manifest.NormalizeQualifier(base), filepath.Dir(absPath), false, false, true, true, false)
	mod := a.newModuleInPkg(pkg, absPath, astFile, manifest.IsTestFile(absPath))
	pkg.Files = []*Module{mod}
	pkg.PrimaryMod = mod
	pkg.HasMain = hasTopLevelMain(astFile)

	a.loadedPkgs[cleanPath] = pkg
	a.Prog.PackageByID[pkg.ID] = pkg
	a.resolvePackageImports(pkg)
	a.attachPrelude(pkg)
	a.Prog.Packages = append(a.Prog.Packages, pkg)
	a.Prog.Modules = append(a.Prog.Modules, mod)
	return pkg
}

func (a *Analyzer) loadPackageDir(pkgDir, pkgID, pkgName string, isStd, isCore, isEntry, isIntegTest bool, importSpan diag.Span) *Package {
	cleanDir := filepath.Clean(pkgDir)
	if a.loadingPkgSet[pkgID] {
		var chain []string
		started := false
		for _, id := range a.loadingPkgStack {
			if id == pkgID {
				started = true
			}
			if started {
				chain = append(chain, id)
			}
		}
		chain = append(chain, pkgID)
		a.Rep.Errorf(importSpan, "cyclic package import: %s", strings.Join(chain, " -> "))
		return nil
	}
	if existing, ok := a.loadedPkgs[cleanDir]; ok {
		return existing
	}

	includeTests := isEntry && a.testMode
	files := a.collectPackageFiles(cleanDir, includeTests)
	if len(files) == 0 {
		if importSpan.Start.Line > 0 {
			a.Rep.Errorf(importSpan, "package directory %q contains no active .ll source files for target %s/%s", pkgDir, a.target.OS, a.target.Arch)
		} else {
			a.Rep.Errorf(diag.Span{}, "package directory %q contains no active .ll source files for target %s/%s", pkgDir, a.target.OS, a.target.Arch)
		}
		return nil
	}

	pkg := a.newPackage(pkgID, pkgName, cleanDir, isStd, isCore, isEntry, false, isIntegTest)
	a.loadingPkgSet[pkgID] = true
	a.loadingPkgStack = append(a.loadingPkgStack, pkgID)
	defer func() {
		delete(a.loadingPkgSet, pkgID)
		if len(a.loadingPkgStack) > 0 {
			a.loadingPkgStack = a.loadingPkgStack[:len(a.loadingPkgStack)-1]
		}
	}()

	for _, fpath := range files {
		astFile := a.parseSourceFile(fpath, importSpan)
		if astFile == nil {
			continue
		}
		isTest := manifest.IsTestFile(fpath)
		mod := a.newModuleInPkg(pkg, fpath, astFile, isTest)
		pkg.Files = append(pkg.Files, mod)
		if !isTest && hasTopLevelMain(astFile) {
			pkg.HasMain = true
		}
	}
	if len(pkg.Files) == 0 {
		return nil
	}
	// Prefer a non-test file matching `<pkgName>.ll`, otherwise the first non-test file, otherwise Files[0].
	pkg.PrimaryMod = pkg.Files[0]
	for _, m := range pkg.Files {
		if !m.IsTestFile {
			pkg.PrimaryMod = m
			break
		}
	}
	for _, m := range pkg.Files {
		if !m.IsTestFile && strings.TrimSuffix(filepath.Base(m.Path), ".ll") == pkgName {
			pkg.PrimaryMod = m
			break
		}
	}

	a.loadedPkgs[cleanDir] = pkg
	a.Prog.PackageByID[pkg.ID] = pkg

	a.resolvePackageImports(pkg)
	a.attachPrelude(pkg)

	// Post-order append so dependencies precede dependents.
	a.Prog.Packages = append(a.Prog.Packages, pkg)
	for _, m := range pkg.Files {
		a.Prog.Modules = append(a.Prog.Modules, m)
	}
	return pkg
}

func (a *Analyzer) attachPrelude(pkg *Package) {
	if a.corePkg == nil || pkg.IsCore || a.corePkg.PrimaryMod == nil {
		return
	}
	for _, mod := range pkg.Files {
		already := false
		for _, imp := range mod.Imports {
			if imp == a.corePkg.PrimaryMod {
				already = true
				break
			}
		}
		if !already {
			mod.Imports = append(mod.Imports, a.corePkg.PrimaryMod)
		}
	}
}

type resolvedPkgTarget struct {
	dir      string
	id       string
	name     string
	isStd    bool
	isCore   bool
	isDevDep bool
}

func (a *Analyzer) resolvePackageImports(pkg *Package) {
	for _, mod := range pkg.Files {
		if mod.File == nil {
			continue
		}
		for _, d := range mod.File.Decls {
			imp, ok := d.(*ast.ImportDecl)
			if !ok {
				continue
			}
			// Reject legacy `.ll` file suffixes with an actionable migration diagnostic (`SPECIFICATION.md §4.3`).
			if strings.HasSuffix(imp.Path, ".ll") {
				suggested := strings.TrimSuffix(imp.Path, ".ll")
				qual := manifest.NormalizeQualifier(path.Base(suggested))
				if suggested == "std/core/core" {
					a.Rep.ErrorWithHint(
						imp.Span,
						fmt.Sprintf("import path %q must not specify a `.ll` file", imp.Path),
						"`std/core` is automatically imported as the implicit prelude; remove this import or use `import \"std/core\"`",
					)
				} else {
					a.Rep.ErrorWithHint(
						imp.Span,
						fmt.Sprintf("import path %q must target a package directory without the `.ll` extension", imp.Path),
						fmt.Sprintf("use `import %q` and access exported declarations via `%s.<name>`", suggested, qual),
					)
				}
				continue
			}

			// Inside a `lowlight.toml` project or workspace, disallow relative `./` / `../` imports (`SPECIFICATION.md §4.3`).
			isRel := imp.Path == "." || imp.Path == ".." || strings.HasPrefix(imp.Path, "./") || strings.HasPrefix(imp.Path, "../")
			if isRel && a.manifestCtx != nil {
				projHint := "<project>/<subpkg>"
				if a.manifestCtx.ActiveManifest != nil && a.manifestCtx.ActiveManifest.HasPackage {
					projHint = a.manifestCtx.ActiveManifest.Package.Name + "/<subpkg>"
				}
				a.Rep.ErrorWithHint(
					imp.Span,
					fmt.Sprintf("relative import %q is not allowed inside a lowlight.toml project", imp.Path),
					fmt.Sprintf("use a project-rooted package path like %q", projHint),
				)
				continue
			}

			target, errMsg, hintMsg := a.resolveImportTarget(pkg, mod, imp.Path)
			if errMsg != "" {
				if hintMsg != "" {
					a.Rep.ErrorWithHint(imp.Span, errMsg, hintMsg)
				} else {
					a.Rep.Errorf(imp.Span, "%s", errMsg)
				}
				continue
			}

			if target.isDevDep && !a.testMode && !a.ideMode {
				a.Rep.Errorf(imp.Span, "cannot import [dev-dependencies] package %q in non-test build", imp.Path)
				continue
			}

			var childPkg *Package
			if target.isCore && a.corePkg != nil {
				childPkg = a.corePkg
			} else {
				childPkg = a.loadPackageDir(target.dir, target.id, target.name, target.isStd, target.isCore, false, false, imp.Span)
			}
			if childPkg == nil || childPkg.PrimaryMod == nil {
				continue
			}

			// Executable `main` packages cannot be imported (`SPECIFICATION.md §4.5`).
			if childPkg.HasMain && !childPkg.IsEntry {
				a.Rep.Errorf(imp.Span, "cannot import executable package %q (defines `main`)", imp.Path)
				continue
			}

			qual := imp.Alias
			if qual == "" {
				qual = childPkg.Name
			}
			if existing, dup := mod.Aliased[qual]; dup {
				a.Rep.ErrorWithHint(
					imp.Span,
					fmt.Sprintf("duplicate import qualifier %q (conflicts with package %q)", qual, existing.Pkg.ID),
					fmt.Sprintf("add an explicit alias: `import %q as <alias>`", imp.Path),
				)
				continue
			}
			mod.Aliased[qual] = childPkg.PrimaryMod
			if target.isDevDep {
				mod.DevAliased[qual] = true
			}
		}
	}
}

func (a *Analyzer) resolveImportTarget(fromPkg *Package, fromMod *Module, rawPath string) (resolvedPkgTarget, string, string) {
	cleanRaw := strings.TrimSuffix(strings.TrimSpace(rawPath), "/")
	if cleanRaw == "" {
		return resolvedPkgTarget{}, "empty import path", ""
	}

	// 1. Standard library packages (`std/<pkg>` or `std/core`).
	if cleanRaw == "std/core" {
		coreDir := filepath.Join(a.StdDir, "core")
		return resolvedPkgTarget{dir: coreDir, id: "std/core", name: "core", isStd: true, isCore: true}, "", ""
	}
	if strings.HasPrefix(cleanRaw, "std/") {
		rest := strings.TrimPrefix(cleanRaw, "std/")
		cand := filepath.Join(a.StdDir, filepath.FromSlash(rest))
		if a.packageDirExists(cand) {
			return resolvedPkgTarget{
				dir:   cand,
				id:    "std/" + rest,
				name:  manifest.NormalizeQualifier(path.Base(rest)),
				isStd: true,
			}, "", ""
		}
		return resolvedPkgTarget{}, fmt.Sprintf("cannot find standard library package %q", rawPath), ""
	}

	// 2. Relative imports (standalone manifest-free scripts only; manifest projects rejected before calling this).
	if cleanRaw == "." || cleanRaw == ".." || strings.HasPrefix(cleanRaw, "./") || strings.HasPrefix(cleanRaw, "../") {
		cand := filepath.Join(fromMod.Dir, filepath.FromSlash(cleanRaw))
		abs, err := filepath.Abs(cand)
		if err == nil {
			cand = abs
		}
		if a.packageDirExists(cand) {
			id, name, isStd, _ := a.classifyPackageDir(cand)
			return resolvedPkgTarget{dir: cand, id: id, name: name, isStd: isStd}, "", ""
		}
		return resolvedPkgTarget{}, fmt.Sprintf("cannot find package directory %q", rawPath), ""
	}

	// 3. Rooted project, workspace, or external dependency imports.
	parts := strings.SplitN(cleanRaw, "/", 2)
	rootSeg := parts[0]
	subPath := ""
	if len(parts) == 2 {
		subPath = parts[1]
	}

	// Check active manifest `[package].name` first.
	if a.manifestCtx != nil && a.manifestCtx.ActiveManifest != nil && a.manifestCtx.ActiveManifest.HasPackage {
		am := a.manifestCtx.ActiveManifest
		if rootSeg == am.Package.Name {
			cand := am.Dir
			if subPath != "" {
				cand = filepath.Join(am.Dir, filepath.FromSlash(subPath))
			}
			if a.packageDirExists(cand) {
				qName := manifest.NormalizeQualifier(am.Package.Name)
				if subPath != "" {
					qName = manifest.NormalizeQualifier(path.Base(subPath))
				}
				return resolvedPkgTarget{dir: cand, id: cleanRaw, name: qName}, "", ""
			}
			return resolvedPkgTarget{}, fmt.Sprintf("cannot find package %q in project %q", rawPath, am.Package.Name), ""
		}
	}

	// Check workspace members.
	if a.manifestCtx != nil {
		if mm, ok := a.manifestCtx.WorkspaceMembers[rootSeg]; ok {
			cand := mm.Dir
			if subPath != "" {
				cand = filepath.Join(mm.Dir, filepath.FromSlash(subPath))
			}
			if a.packageDirExists(cand) {
				qName := manifest.NormalizeQualifier(rootSeg)
				if subPath != "" {
					qName = manifest.NormalizeQualifier(path.Base(subPath))
				}
				return resolvedPkgTarget{dir: cand, id: cleanRaw, name: qName}, "", ""
			}
			return resolvedPkgTarget{}, fmt.Sprintf("cannot find package %q in workspace member %q", rawPath, rootSeg), ""
		}
	}

	// Check resolved external dependencies (`[dependencies]`, `[dev-dependencies]`, `[overrides]`, `./vendor/`).
	if a.resolvedDeps != nil {
		if rp, ok := a.resolvedDeps.Packages[rootSeg]; ok {
			cand := rp.Dir
			if subPath != "" {
				cand = filepath.Join(rp.Dir, filepath.FromSlash(subPath))
			}
			if a.packageDirExists(cand) {
				qName := manifest.NormalizeQualifier(rootSeg)
				if subPath != "" {
					qName = manifest.NormalizeQualifier(path.Base(subPath))
				}
				isDev := a.isDevOnlyDependency(fromPkg, rootSeg)
				return resolvedPkgTarget{dir: cand, id: "dep/" + cleanRaw, name: qName, isDevDep: isDev}, "", ""
			}
			return resolvedPkgTarget{}, fmt.Sprintf("cannot find package %q in dependency %q (%s)", rawPath, rootSeg, rp.Dir), ""
		}
	}

	// Check if `rootSeg` is declared only under `[dev-dependencies]` when running in non-test mode.
	if a.manifestCtx != nil && a.manifestCtx.ActiveManifest != nil {
		_, devDeps := a.manifestCtx.ActiveManifest.EffectiveDeps(a.target, true)
		if _, isDev := devDeps[rootSeg]; isDev {
			return resolvedPkgTarget{}, fmt.Sprintf("cannot import [dev-dependencies] package %q in non-test build", rawPath), ""
		}
	}

	// Fallback for standalone manifest-free builds (e.g., tests passing explicit `ProjectRoot`):
	// check relative to `fromMod.Dir` or `a.ProjectRoot`.
	if a.manifestCtx == nil {
		for _, base := range []string{fromMod.Dir, a.ProjectRoot} {
			if base == "" {
				continue
			}
			cand := filepath.Join(base, filepath.FromSlash(cleanRaw))
			if abs, err := filepath.Abs(cand); err == nil {
				cand = abs
			}
			if a.packageDirExists(cand) {
				id, name, isStd, _ := a.classifyPackageDir(cand)
				return resolvedPkgTarget{dir: cand, id: id, name: name, isStd: isStd}, "", ""
			}
		}
	}

	return resolvedPkgTarget{}, fmt.Sprintf("cannot find package %q", rawPath), ""
}

func (a *Analyzer) isDevOnlyDependency(fromPkg *Package, depName string) bool {
	if a.manifestCtx != nil && a.manifestCtx.ActiveManifest != nil && fromPkg != nil && fromPkg.IsEntry {
		deps, devDeps := a.manifestCtx.ActiveManifest.EffectiveDeps(a.target, true)
		if _, inDeps := deps[depName]; inDeps {
			return false
		}
		if _, inDev := devDeps[depName]; inDev {
			return true
		}
	}
	if a.resolvedDeps != nil && a.resolvedDeps.DevOnly[depName] {
		return true
	}
	return false
}

func (a *Analyzer) packageDirExists(dir string) bool {
	cleanDir := filepath.Clean(dir)
	if dirExists(cleanDir) {
		if entries, err := os.ReadDir(cleanDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".ll") {
					return true
				}
			}
		}
	}
	for ovPath := range a.overlays {
		if filepath.Clean(filepath.Dir(ovPath)) == cleanDir && strings.HasSuffix(ovPath, ".ll") {
			return true
		}
	}
	return false
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

// IsEntryPackageMod reports whether `mod` belongs to the entry package (`SPECIFICATION.md §4.1`).
func (prog *CheckedProgram) IsEntryPackageMod(mod *Module) bool {
	if prog == nil || mod == nil {
		return false
	}
	if prog.EntryPkg != nil && mod.Pkg != nil {
		return mod.Pkg == prog.EntryPkg
	}
	return mod == prog.EntryMod
}

// SamePackage reports whether two modules belong to the same directory package (`SPECIFICATION.md §4.4.2`).
func (prog *CheckedProgram) SamePackage(modA, modB *Module) bool {
	if modA == nil || modB == nil {
		return true
	}
	if modA == modB {
		return true
	}
	if modA.Pkg != nil && modB.Pkg != nil {
		if modA.Pkg == modB.Pkg {
			return true
		}
		if modA.Pkg.IsStandalone || modB.Pkg.IsStandalone {
			return modA.Path == modB.Path
		}
		return modA.Pkg.ID == modB.Pkg.ID
	}
	dirA := filepath.Clean(modA.Dir)
	dirB := filepath.Clean(modB.Dir)
	if dirA == "" || dirB == "" {
		return modA.Path == modB.Path
	}
	return dirA == dirB
}

// SamePackageDir reports whether `curMod` and a target directory belong to the same package.
func (prog *CheckedProgram) SamePackageDir(curMod *Module, targetDir, targetPath string) bool {
	if curMod == nil || targetDir == "" {
		return true
	}
	if curMod.Pkg != nil && curMod.Pkg.IsStandalone && targetPath != "" {
		return filepath.Clean(curMod.Path) == filepath.Clean(targetPath)
	}
	dirCur := filepath.Clean(curMod.Dir)
	dirTarget := filepath.Clean(targetDir)
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
	}
	if p.GenStructMap != nil {
		if inst, ok := p.GenStructMap.Instances[types.TypeString.Mangled()+"__"+types.TypeInt.Mangled()]; ok {
			candidates = append(candidates, inst)
		}
	}
	if p.GenStructSet != nil {
		if inst, ok := p.GenStructSet.Instances[types.TypeInt.Mangled()]; ok {
			candidates = append(candidates, inst)
		}
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
