// Package sema implements multi-file import resolution, type checking, generic
// monomorphization, ownership/move checking, borrow checking, arena region escape
// analysis, unsafe boundary enforcement, and control-flow exhaustiveness for Lowlight.
package sema

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/parser"
	"lowlight/internal/types"
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

// Symbol represents a named entity (variable, constant, function, parameter).
type Symbol struct {
	Name          string
	CName         string
	Type          types.Type
	IsBind        bool // true = deeply immutable (`bind`, read-only param, read-only loop var, capture)
	BindKind      BindKind
	IsTopLevel    bool
	IsConstInt    bool
	ConstIntVal   int64
	ScopeDepth    int
	ArenaDepth    int // > 0 if this value holds `ref[T]` tied to a local Arena at ArenaDepth
	DeclFuncDepth int // function/lambda nesting level where declared
	FuncInst      *FuncInstance
	GenFunc       *GenericFuncTemplate
	ExternFn      *ExternFunc
	Span          diag.Span
}

// ExternFunc represents a resolved `extern name: (params): Ret` C ABI function.
type ExternFunc struct {
	Name string
	Sig  *types.Func
	Span diag.Span
}

// GenericStructTemplate stores an uninstantiated `define Name[T...] struct [...]`.
type GenericStructTemplate struct {
	Decl      *ast.StructDecl
	Mod       *Module
	Instances map[string]*types.Struct
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
	ParamSyms     []*Symbol // resolved parameter symbols populated during body checking
	Body          ast.Expr
	Mod           *Module
	TypeSubst     map[string]types.Type
	FuncTargetID  int
	BodyMaps      *BodyMaps
	Checked       bool
}

// BodyMaps holds per-monomorphized-body AST side tables so multiple generic
// instantiations sharing the same AST nodes never overwrite each other.
type BodyMaps struct {
	ExprInfos    map[ast.Expr]*ExprInfo
	UnwindInfos  map[*ast.UnwindStmt]*UnwindInfo
	VarSymbols   map[*ast.VarStmt]*Symbol
	ForSymbols   map[*ast.ForExpr][2]*Symbol // [Var1Sym, Var2Sym]
	IfUnwrapSyms map[*ast.IfExpr]*Symbol
	IfOriginSyms map[*ast.IfExpr]*Symbol
}

// NewBodyMaps allocates a fresh set of per-body AST side tables.
func NewBodyMaps() *BodyMaps {
	return &BodyMaps{
		ExprInfos:    make(map[ast.Expr]*ExprInfo),
		UnwindInfos:  make(map[*ast.UnwindStmt]*UnwindInfo),
		VarSymbols:   make(map[*ast.VarStmt]*Symbol),
		ForSymbols:   make(map[*ast.ForExpr][2]*Symbol),
		IfUnwrapSyms: make(map[*ast.IfExpr]*Symbol),
		IfOriginSyms: make(map[*ast.IfExpr]*Symbol),
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

	// Struct literal resolution (for *ast.StructLit)
	StructType    *types.Struct

	// Lambda resolution (for *ast.FuncLit)
	Lambda        *LambdaInfo

	// Target ID for control-flow constructs (`BlockExpr`, `IfExpr`, `MatchExpr`, `ForExpr`)
	TargetID      int
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
	Imports       []*Module            // unqualified `import "foo.ll"`
	Aliased       map[string]*Module   // `import "foo.ll" as alias`
	Types         map[string]types.Type
	GenericStructs map[string]*GenericStructTemplate
	Symbols       map[string]*Symbol
	GenericFuncs  map[string]*GenericFuncTemplate
	ExternHeaders []string
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
	Distincts      []*types.Distinct
	Interfaces     []*types.Interface
	TopVars        []*TopVarInit
	Funcs          []*FuncInstance
	Lambdas        []*LambdaInfo
	VTables        map[string]*VTableImpl
	MainFunc       *FuncInstance

	TopBodyMaps    *BodyMaps
	StructBodyMaps map[*types.Struct]*BodyMaps
	MethodTable    map[string]map[string]*FuncInstance

	// Built-in interfaces
	IfacePrintable *types.Interface
	IfaceCopyable  *types.Interface
	IfaceDrop      *types.Interface
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

	// Method tables: key is canonical type key (e.g. `"Point"`, `"int"`, `"string"`, `"vec"`, `"Pair"`).
	methods      map[string]map[string]*FuncInstance
	genMethods   map[string]map[string]*GenericFuncTemplate

	inBodyPass   bool
	nextTargetID int
	nextLambdaID int
}

// Check parses and semantically validates `entryPath` and all its transitive imports.
func Check(entryPath, srcOverride, projectRoot string, rep *diag.Reporter) *CheckedProgram {
	absEntry, err := filepath.Abs(entryPath)
	if err != nil {
		absEntry = entryPath
	}
	if projectRoot == "" {
		projectRoot = findProjectRoot(filepath.Dir(absEntry))
	}
	stdDir := filepath.Join(projectRoot, "std")

	topBM := NewBodyMaps()
	prog := &CheckedProgram{
		StructDecls:    make(map[*types.Struct]*ast.StructDecl),
		StructMods:     make(map[*types.Struct]*Module),
		VTables:        make(map[string]*VTableImpl),
		TopBodyMaps:    topBM,
		StructBodyMaps: make(map[*types.Struct]*BodyMaps),
		MethodTable:    make(map[string]map[string]*FuncInstance),
	}

	a := &Analyzer{
		Rep:          rep,
		ProjectRoot:  projectRoot,
		StdDir:       stdDir,
		Prog:         prog,
		curBM:        topBM,
		loadedMods:   make(map[string]*Module),
		loadingStack: make(map[string]bool),
		methods:      make(map[string]map[string]*FuncInstance),
		genMethods:   make(map[string]map[string]*GenericFuncTemplate),
	}

	a.initBuiltinInterfaces()
	entryMod := a.loadModule(absEntry, srcOverride, diag.Span{})
	if entryMod == nil || rep.HasErrors() {
		return prog
	}
	prog.EntryMod = entryMod

	a.runTypeAndSymbolRegistration()
	if rep.HasErrors() {
		return prog
	}

	a.runBodyChecking()
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
	a.Prog.Interfaces = append(a.Prog.Interfaces, a.Prog.IfacePrintable, a.Prog.IfaceCopyable, a.Prog.IfaceDrop)
}

func (a *Analyzer) loadModule(absPath, srcOverride string, importSpan diag.Span) *Module {
	if existing, ok := a.loadedMods[absPath]; ok {
		return existing
	}
	if a.loadingStack[absPath] {
		a.Rep.Errorf(importSpan, "cyclic module import involving %q", absPath)
		return nil
	}
	a.loadingStack[absPath] = true
	defer delete(a.loadingStack, absPath)

	var content string
	if srcOverride != "" {
		content = srcOverride
	} else {
		bytes, err := os.ReadFile(absPath)
		if err != nil {
			a.Rep.Errorf(importSpan, "cannot read imported module %q: %v", absPath, err)
			return nil
		}
		content = string(bytes)
	}

	astFile := parser.ParseFile(absPath, content, a.Rep)
	mod := &Module{
		Path:           absPath,
		Dir:            filepath.Dir(absPath),
		File:           astFile,
		Aliased:        make(map[string]*Module),
		Types:          make(map[string]types.Type),
		GenericStructs: make(map[string]*GenericStructTemplate),
		Symbols:        make(map[string]*Symbol),
		GenericFuncs:   make(map[string]*GenericFuncTemplate),
	}
	// Pre-populate built-in interfaces in every module's type lookup
	mod.Types["Printable"] = a.Prog.IfacePrintable
	mod.Types["Copyable"] = a.Prog.IfaceCopyable
	mod.Types["Drop"] = a.Prog.IfaceDrop

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
		child := a.loadModule(resolved, "", imp.Span)
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
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
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
