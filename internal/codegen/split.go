package codegen

import (
	"strings"

	"lowlight/internal/ir"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

// UnitMode selects standalone vs two-phase split compilation emission (`SPECIFICATION.md §12.6`).
type UnitMode int

const (
	// UnitStandalone emits one complete translation unit (used by `emit-llvm`, `emit-c`, and standalone scripts).
	UnitStandalone UnitMode = iota
	// UnitPackage emits Phase 1: concrete non-generic functions for one library package.
	UnitPackage
	// UnitLinkMain emits Phase 2: shared monomorphized generics, vtables, lambdas, runtime globals, entry/main.
	UnitLinkMain
)

// PackageUnit holds the emitted Phase 1 IR or C11 source for one library package.
type PackageUnit struct {
	PkgID  string
	PkgTag string
	Source string
}

// SplitUnits holds all Phase 1 per-package units and the Phase 2 shared specialization/link unit.
type SplitUnits struct {
	PackageUnits []PackageUnit
	LinkUnit     string
}

// EmitLLVMSplit partitions `prog` into Phase 1 per-package concrete LLVM IR units
// and a Phase 2 shared specialization/runtime/entry LLVM IR link unit.
func EmitLLVMSplit(prog *ir.Program) SplitUnits {
	pkgOrder, pkgFuncs, allPkgFuncs := partitionPackageFunctions(prog)
	if len(pkgOrder) == 0 {
		return SplitUnits{LinkUnit: EmitLLVM(prog)}
	}

	var units []PackageUnit
	for _, pkg := range pkgOrder {
		fset := pkgFuncs[pkg.ID]
		subProg := buildPackageSubProgram(prog, pkg, fset, false)
		g := newLLVMEmitter(subProg)
		g.unitMode = UnitPackage
		g.defineFuncs = fset
		g.emitAll()
		units = append(units, PackageUnit{
			PkgID:  pkg.ID,
			PkgTag: pkg.Tag,
			Source: g.sb.String(),
		})
	}

	linkFuncs := make(map[string]bool, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		if !allPkgFuncs[fn.CName] {
			linkFuncs[fn.CName] = true
		}
	}
	lg := newLLVMEmitter(prog)
	lg.unitMode = UnitLinkMain
	lg.defineFuncs = linkFuncs
	lg.emitAll()

	return SplitUnits{
		PackageUnits: units,
		LinkUnit:     lg.sb.String(),
	}
}

// EmitCSplit partitions `prog` into Phase 1 per-package concrete ISO C11 units
// and a Phase 2 shared specialization/runtime/entry C11 link unit.
func EmitCSplit(prog *ir.Program) SplitUnits {
	pkgOrder, pkgFuncs, allPkgFuncs := partitionPackageFunctions(prog)
	if len(pkgOrder) == 0 {
		return SplitUnits{LinkUnit: EmitC(prog)}
	}

	var units []PackageUnit
	for _, pkg := range pkgOrder {
		fset := pkgFuncs[pkg.ID]
		subProg := buildPackageSubProgram(prog, pkg, fset, true)
		g := newCEmitter(subProg, UnitPackage, fset)
		g.emitAll()
		units = append(units, PackageUnit{
			PkgID:  pkg.ID,
			PkgTag: pkg.Tag,
			Source: g.sb.String(),
		})
	}

	linkFuncs := make(map[string]bool, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		if !allPkgFuncs[fn.CName] {
			linkFuncs[fn.CName] = true
		}
	}
	lg := newCEmitter(prog, UnitLinkMain, linkFuncs)
	lg.emitAll()

	return SplitUnits{
		PackageUnits: units,
		LinkUnit:     lg.sb.String(),
	}
}

func newCEmitter(prog *ir.Program, mode UnitMode, defineFuncs map[string]bool) *cEmitter {
	g := &cEmitter{
		prog:        prog,
		unitMode:    mode,
		defineFuncs: defineFuncs,
		optSet:      make(map[string]bool),
		vecSet:      make(map[string]bool),
	}
	for _, o := range prog.Optionals {
		g.optSet[o.Mangled()] = true
	}
	for _, st := range prog.Structs {
		if st.BoxedOptional {
			g.optSet[types.NewOptional(st).Mangled()] = true
		}
	}
	for _, v := range prog.Vectors {
		g.vecSet[v.Mangled()] = true
	}
	return g
}

func partitionPackageFunctions(prog *ir.Program) ([]*sema.Package, map[string]map[string]bool, map[string]bool) {
	if prog == nil || prog.Checked == nil || len(prog.Checked.Packages) <= 1 {
		return nil, nil, nil
	}
	eligiblePkg := make(map[string]*sema.Package)
	for _, pkg := range prog.Checked.Packages {
		if pkg == nil || pkg.IsEntry || pkg.IsCore || pkg.Tag == "" {
			continue
		}
		eligiblePkg[pkg.ID] = pkg
	}
	if len(eligiblePkg) == 0 {
		return nil, nil, nil
	}

	pkgFuncs := make(map[string]map[string]bool)
	allPkgFuncs := make(map[string]bool)
	for _, fn := range prog.Funcs {
		if fn == nil || fn.IsGeneric || fn.IsLambda || fn.IsTest || fn.IsAlwaysInline {
			continue
		}
		if _, ok := eligiblePkg[fn.PkgID]; !ok {
			continue
		}
		if pkgFuncs[fn.PkgID] == nil {
			pkgFuncs[fn.PkgID] = make(map[string]bool)
		}
		pkgFuncs[fn.PkgID][fn.CName] = true
		allPkgFuncs[fn.CName] = true
	}

	var pkgOrder []*sema.Package
	for _, pkg := range prog.Checked.Packages {
		if pkg != nil && len(pkgFuncs[pkg.ID]) > 0 {
			pkgOrder = append(pkgOrder, pkg)
		}
	}
	return pkgOrder, pkgFuncs, allPkgFuncs
}

// buildPackageSubProgram creates a hermetic filtered view of `prog` containing ONLY the types,
// external prototypes, globals, vtables, thunks, and lambdas reachable from `pkg`'s Phase 1 functions.
// Because unrelated entry-package edits (new structs, strings, functions in main.ll) do not appear
// in this sub-program, Phase 1 `.o` cache keys remain 100% stable across edits to downstream packages.
func buildPackageSubProgram(prog *ir.Program, pkg *sema.Package, pkgFuncSet map[string]bool, inlineAlwaysInline bool) *ir.Program {
	funcByName := make(map[string]*ir.Function, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		funcByName[fn.CName] = fn
	}
	thunkByName := make(map[string]*ir.StaticThunk, len(prog.StaticThunks))
	for _, st := range prog.StaticThunks {
		thunkByName[st.ThunkCName] = st
	}
	lambdaByCName := make(map[string]*sema.LambdaInfo)
	lambdaByEnv := make(map[string]*sema.LambdaInfo)
	if prog.Checked != nil {
		for _, li := range prog.Checked.Lambdas {
			lambdaByCName[li.CName] = li
			lambdaByEnv[li.EnvCName] = li
		}
	}

	usedTypes := make(map[string]bool)
	usedFns := make(map[string]bool)
	usedGlobals := make(map[string]bool)
	usedVTables := make(map[string]bool)
	usedThunks := make(map[string]bool)
	usedLambdas := make(map[string]bool)
	walkedInline := make(map[string]bool)
	usedExternFuncs := make(map[*sema.ExternFunc]bool)
	usedExternPkgs := make(map[string]bool)
	usedInlinePkgs := make(map[string]bool)

	var markType func(t types.Type)
	var markFuncProto func(cname string)
	var walkInstrs func(ins []ir.Instr)
	var walkExpr func(e ir.Expr)

	// Always include `_hash_mix_u64` if present since synthesized hash helpers reference it.
	if prog.Checked != nil {
		for _, fi := range prog.Checked.Funcs {
			if fi.Name == "_hash_mix_u64" || fi.Name == "hash_mix_u64" {
				usedFns[fi.CName] = true
			}
		}
	}

	markFuncProto = func(cname string) {
		if cname == "" {
			return
		}
		wasUsed := usedFns[cname]
		usedFns[cname] = true
		fn := funcByName[cname]
		if fn == nil {
			return
		}
		if !wasUsed {
			for _, p := range fn.Params {
				markType(p.Type)
			}
			markType(fn.RetType)
		}
		// In C11 mode, `@inline` functions are emitted `static inline` in Phase 1 too, so walk their bodies!
		if inlineAlwaysInline && fn.IsAlwaysInline && !walkedInline[cname] {
			walkedInline[cname] = true
			if fn.PkgID != "" {
				usedInlinePkgs[fn.PkgID] = true
			}
			for _, loc := range fn.Locals {
				markType(loc.Type)
			}
			walkInstrs(fn.Body)
		}
	}

	markType = func(t types.Type) {
		if t == nil || t == types.TypeInvalid || t == types.TypeVoid || t == types.TypeNever {
			return
		}
		if _, isTP := t.(*types.TypeParamVar); isTP {
			return
		}
		if _, isSelf := t.(*types.SelfPlaceholder); isSelf {
			return
		}
		key := t.Mangled()
		if usedTypes[key] {
			return
		}
		usedTypes[key] = true

		// Any concrete lifecycle/protocol method on `t` may be called by synthesized drop/copy/eq/cmp/hash/to_string helpers!
		if prog.Checked != nil {
			for _, mname := range []string{"drop", "copy", "eq", "cmp", "hash", "to_string"} {
				if fi := prog.Checked.LookupConcreteMethod(t, mname); fi != nil {
					markFuncProto(fi.CName)
				}
			}
		}

		switch v := t.(type) {
		case *types.Ptr:
			markType(v.Elem)
		case *types.Ref:
			markType(v.Elem)
		case *types.Distinct:
			markType(v.Base)
		case *types.Enum:
			for _, vr := range v.Variants {
				if vr.Kind == types.VariantTuple {
					markType(vr.Payload)
				}
			}
		case *types.Struct:
			if (v.Name == "List" || strings.HasPrefix(v.Name, "List[")) && len(v.TypeArgs) == 1 {
				markType(&types.Vector{Elem: v.TypeArgs[0]})
			}
			for _, f := range v.Fields {
				markType(f.Type)
			}
		case *types.Optional:
			markType(v.Elem)
		case *types.Vector:
			markType(v.Elem)
		case *types.StackArray:
			markType(v.Elem)
		case *types.Tuple:
			for _, el := range v.Elems {
				markType(el)
			}
		case *types.Func:
			for _, p := range v.Params {
				markType(p.Type)
			}
			markType(v.Ret)
		case *types.Interface:
			for _, m := range v.Methods {
				for _, p := range m.Params {
					markType(p.Type)
				}
				markType(m.Ret)
			}
		}
	}

	walkInstrs = func(ins []ir.Instr) {
		for _, in := range ins {
			if in == nil {
				continue
			}
			switch v := in.(type) {
			case *ir.InstrAssign:
				walkExpr(v.Dst)
				walkExpr(v.Expr)
			case *ir.InstrCompoundAssign:
				markType(v.Type)
				walkExpr(v.Dst)
				walkExpr(v.Expr)
			case *ir.InstrExpr:
				walkExpr(v.Expr)
			case *ir.InstrZero:
				markType(v.Type)
			case *ir.InstrDrop:
				markType(v.Type)
			case *ir.InstrIf:
				walkExpr(v.Cond)
				walkInstrs(v.Then)
				walkInstrs(v.Else)
			case *ir.InstrWhile:
				walkExpr(v.Cond)
				walkInstrs(v.Body)
			case *ir.InstrBlock:
				walkInstrs(v.Body)
			case *ir.InstrReturn:
				walkExpr(v.Expr)
			}
		}
	}

	var walkStatic func(sv *ir.StaticVal)
	walkStatic = func(sv *ir.StaticVal) {
		if sv == nil {
			return
		}
		markType(sv.Type)
		effTy := sv.Type
		for {
			if d, isDist := effTy.(*types.Distinct); isDist {
				effTy = d.Base
			} else {
				break
			}
		}
		if sv.Kind == sema.StaticList {
			if vecTy, ok := effTy.(*types.Vector); ok {
				markType(vecTy)
			} else if st, ok := effTy.(*types.Struct); ok && len(st.TypeArgs) == 1 {
				markType(&types.Vector{Elem: st.TypeArgs[0]})
			}
		}
		for _, el := range sv.Elems {
			walkStatic(el)
		}
		for _, f := range sv.Fields {
			walkStatic(f.Value)
		}
		walkStatic(sv.Payload)
		walkStatic(sv.OptElem)
		if sv.Kind == sema.StaticFn && sv.FnEntry != "" {
			markFuncProto(sv.FnEntry)
			if st, ok := thunkByName[sv.FnEntry]; ok {
				usedThunks[st.ThunkCName] = true
				markFuncProto(st.TargetCName())
				if li, ok := lambdaByCName[st.TargetCName()]; ok {
					usedLambdas[li.CName] = true
				}
				markType(st.FuncType)
			}
			if li, ok := lambdaByCName[sv.FnEntry]; ok {
				usedLambdas[li.CName] = true
				markFuncProto(li.CName)
			}
		}
	}

	walkExpr = func(e ir.Expr) {
		if e == nil {
			return
		}
		markType(e.Type())
		switch v := e.(type) {
		case *ir.ExprConst:
			walkStatic(v.Static)
		case *ir.ExprLocal:
			if v.Local != nil {
				markType(v.Local.Type)
			}
		case *ir.ExprGlobal:
			if v.Global != nil {
				usedGlobals[v.Global.CName] = true
				markType(v.Global.Type)
			}
		case *ir.ExprFuncRef:
			if v.TargetFunc != nil {
				markFuncProto(v.TargetFunc.CName)
			}
			if v.ExternFunc != nil {
				usedExternFuncs[v.ExternFunc] = true
				if v.ExternFunc.PkgID != "" {
					usedExternPkgs[v.ExternFunc.PkgID] = true
				}
			}
			if v.Name != "" {
				markFuncProto(v.Name)
				if v.ExternFunc == nil && pkg != nil {
					for _, ef := range prog.ExternFuncs {
						if ef.Name == v.Name && (ef.PkgID == pkg.ID || ef.PkgID == "") {
							usedExternFuncs[ef] = true
							if ef.PkgID != "" {
								usedExternPkgs[ef.PkgID] = true
							}
						}
					}
				}
				if st, ok := thunkByName[v.Name]; ok {
					usedThunks[st.ThunkCName] = true
					markFuncProto(st.TargetCName())
					if li, ok := lambdaByCName[st.TargetCName()]; ok {
						usedLambdas[li.CName] = true
					}
					markType(st.FuncType)
				}
			}
		case *ir.ExprField:
			walkExpr(v.Base)
		case *ir.ExprIndex:
			walkExpr(v.Base)
			walkExpr(v.Index)
		case *ir.ExprDeref:
			walkExpr(v.Ptr)
		case *ir.ExprAddrOf:
			walkExpr(v.Place)
		case *ir.ExprBinary:
			walkExpr(v.Left)
			walkExpr(v.Right)
		case *ir.ExprUnary:
			walkExpr(v.Operand)
		case *ir.ExprCast:
			markType(v.From)
			markType(v.To)
			walkExpr(v.Val)
		case *ir.ExprCall:
			walkExpr(v.Callee)
			for _, a := range v.Args {
				walkExpr(a)
			}
		case *ir.ExprConstructStruct:
			markType(v.StructType)
			for _, f := range v.Fields {
				walkExpr(f.Value)
			}
		case *ir.ExprConstructEnum:
			markType(v.EnumType)
			walkExpr(v.Payload)
		case *ir.ExprConstructArray:
			markType(v.ArrayType)
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ir.ExprConstructVector:
			markType(v.VecType)
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ir.ExprConstructTuple:
			markType(v.TupleType)
			for _, el := range v.Elems {
				walkExpr(el)
			}
		case *ir.ExprFnToClosure:
			markType(v.To)
			walkExpr(v.Fn)
		case *ir.ExprConstructClosure:
			markType(v.FuncType)
			walkExpr(v.EnvPtr)
			if st, ok := thunkByName[v.EntryFn]; ok {
				usedThunks[st.ThunkCName] = true
				markFuncProto(st.TargetCName())
				if li, ok := lambdaByCName[st.TargetCName()]; ok {
					usedLambdas[li.CName] = true
				}
				markType(st.FuncType)
			}
			// Lambda entry fn is `<li.CName>_entry` (C11) or `<li.CName>` (LLVM).
			baseName := strings.TrimSuffix(v.EntryFn, "_entry")
			if li, ok := lambdaByCName[baseName]; ok {
				usedLambdas[li.CName] = true
				markFuncProto(li.CName)
				for _, c := range li.Captures {
					markType(c.Type)
				}
			}
			if v.DropFn != "" {
				envName := strings.TrimSuffix(v.DropFn, "_drop")
				if li, ok := lambdaByEnv[envName]; ok {
					usedLambdas[li.CName] = true
					markFuncProto(li.CName)
					for _, c := range li.Captures {
						markType(c.Type)
					}
				}
			}
		case *ir.ExprWrapOptional:
			markType(v.OptType)
			walkExpr(v.Value)
		case *ir.ExprOptionalHasValue:
			walkExpr(v.Opt)
		case *ir.ExprOptionalValue:
			markType(v.ElemType)
			walkExpr(v.Opt)
		case *ir.ExprOptionalValuePtr:
			markType(v.ElemType)
			walkExpr(v.OptPtr)
		case *ir.ExprSliceView:
			markType(v.VecType)
			walkExpr(v.DataPtr)
			walkExpr(v.Len)
		case *ir.ExprBoxInterface:
			markType(v.IfcType)
			markType(v.ConcreteType)
			walkExpr(v.Value)
			if v.VTable != nil {
				usedVTables[ir.VTableInstanceName(v.VTable.Concrete, v.VTable.Iface)] = true
			}
		case *ir.ExprAlloc:
			markType(v.AllocType)
			walkExpr(v.Count)
		case *ir.ExprFree:
			walkExpr(v.Ptr)
		case *ir.ExprRealloc:
			markType(v.AllocType)
			walkExpr(v.Ptr)
			walkExpr(v.Count)
		case *ir.ExprCopyBytes:
			walkExpr(v.Dst)
			walkExpr(v.Src)
			walkExpr(v.Count)
		case *ir.ExprSizeOf:
			markType(v.QueryType)
		case *ir.ExprAlignOf:
			markType(v.QueryType)
		case *ir.ExprFloatBits:
			walkExpr(v.Val)
		case *ir.ExprToString:
			markType(v.ValType)
			walkExpr(v.Val)
		case *ir.ExprDrop:
			markType(v.ValType)
			walkExpr(v.Ptr)
		case *ir.ExprCopy:
			markType(v.ValType)
			walkExpr(v.Val)
		case *ir.ExprEq:
			markType(v.ValType)
			walkExpr(v.Left)
			walkExpr(v.Right)
		case *ir.ExprCmp:
			markType(v.ValType)
			walkExpr(v.Left)
			walkExpr(v.Right)
		case *ir.ExprHash:
			markType(v.ValType)
			walkExpr(v.Val)
		case *ir.ExprVecPush:
			markType(v.VecType)
			walkExpr(v.VecPtr)
			walkExpr(v.Item)
		}
	}

	// Seed primitive protocol methods (`to_string`, `hash`, `eq`, `cmp`) referenced unconditionally by runtime helpers.
	for _, prim := range []types.Type{
		types.TypeInt, types.TypeI8, types.TypeI16, types.TypeI32,
		types.TypeU8, types.TypeU16, types.TypeU32, types.TypeU64,
		types.TypeFloat, types.TypeF32,
		types.TypeBool, types.TypeChar, types.TypeString,
	} {
		markType(prim)
	}

	// Seed traversal with all Phase 1 concrete functions belonging to `pkg`.
	for _, fn := range prog.Funcs {
		if !pkgFuncSet[fn.CName] {
			continue
		}
		usedFns[fn.CName] = true
		for _, p := range fn.Params {
			markType(p.Type)
		}
		markType(fn.RetType)
		for _, loc := range fn.Locals {
			markType(loc.Type)
		}
		walkInstrs(fn.Body)
	}

	// Filter `prog` collections preserving exact topological & declaration ordering.
	sub := *prog
	sub.MainFunc = nil
	sub.TestMainFunc = nil
	sub.TestFuncs = nil
	sub.TestMode = false

	// Filter extern functions to only those actually referenced by this unit.
	if len(prog.ExternFuncs) > 0 {
		var extFuncs []*sema.ExternFunc
		for _, ef := range prog.ExternFuncs {
			if usedExternFuncs[ef] {
				extFuncs = append(extFuncs, ef)
			}
		}
		sub.ExternFuncs = extFuncs
	}

	// Filter extern headers to `pkg` plus non-entry packages whose `@inline` or `extern` symbols
	// were actually pulled into this Phase 1 unit so unrelated packages cannot pollute/invalidate it.
	if prog.Checked != nil {
		seenHdr := make(map[string]bool)
		var hdrs []string
		for _, p := range prog.Checked.Packages {
			if p == nil || p.IsEntry {
				continue
			}
			isOwnPkg := pkg != nil && p.ID == pkg.ID
			if !isOwnPkg && !usedExternPkgs[p.ID] && !usedInlinePkgs[p.ID] {
				continue
			}
			for _, m := range p.Files {
				for _, h := range m.ExternHeaders {
					if !seenHdr[h] {
						seenHdr[h] = true
						hdrs = append(hdrs, h)
					}
				}
			}
		}
		sub.ExternHeaders = hdrs
	}

	// Shallow copy CheckedProgram with filtered Lambdas so entry-only lambdas don't pollute Phase 1 units.
	if prog.Checked != nil {
		checkedCopy := *prog.Checked
		var keptLambdas []*sema.LambdaInfo
		for _, li := range prog.Checked.Lambdas {
			if usedLambdas[li.CName] {
				keptLambdas = append(keptLambdas, li)
			}
		}
		checkedCopy.Lambdas = keptLambdas
		sub.Checked = &checkedCopy
	}

	var ordered []types.Type
	for _, t := range prog.OrderedTypes {
		if usedTypes[t.Mangled()] {
			ordered = append(ordered, t)
		}
	}
	sub.OrderedTypes = ordered

	var ens []*types.Enum
	for _, en := range prog.Enums {
		if usedTypes[en.Mangled()] {
			ens = append(ens, en)
		}
	}
	sub.Enums = ens

	var dists []*types.Distinct
	for _, d := range prog.Distincts {
		if usedTypes[d.Mangled()] {
			dists = append(dists, d)
		}
	}
	sub.Distincts = dists

	var sts []*types.Struct
	for _, st := range prog.Structs {
		if usedTypes[st.Mangled()] {
			sts = append(sts, st)
		}
	}
	sub.Structs = sts

	var opts []*types.Optional
	for _, o := range prog.Optionals {
		if usedTypes[o.Mangled()] {
			opts = append(opts, o)
		}
	}
	sub.Optionals = opts

	var vecs []*types.Vector
	for _, v := range prog.Vectors {
		if usedTypes[v.Mangled()] {
			vecs = append(vecs, v)
		}
	}
	sub.Vectors = vecs

	var arrs []*types.StackArray
	for _, a := range prog.Arrays {
		if usedTypes[a.Mangled()] {
			arrs = append(arrs, a)
		}
	}
	sub.Arrays = arrs

	var tups []*types.Tuple
	for _, tp := range prog.Tuples {
		if usedTypes[tp.Mangled()] {
			tups = append(tups, tp)
		}
	}
	sub.Tuples = tups

	var fntys []*types.Func
	for _, ft := range prog.FuncTypes {
		if usedTypes[ft.Mangled()] {
			fntys = append(fntys, ft)
		}
	}
	sub.FuncTypes = fntys

	var ifcs []*types.Interface
	for _, ifc := range prog.Interfaces {
		if usedTypes[ifc.Mangled()] {
			ifcs = append(ifcs, ifc)
		}
	}
	sub.Interfaces = ifcs

	var vtables []*sema.VTableImpl
	for _, vt := range prog.VTables {
		if usedVTables[ir.VTableInstanceName(vt.Concrete, vt.Iface)] {
			vtables = append(vtables, vt)
		}
	}
	sub.VTables = vtables

	var globals []*ir.Global
	for _, gl := range prog.Globals {
		if usedGlobals[gl.CName] {
			globals = append(globals, gl)
		}
	}
	sub.Globals = globals

	var thunks []*ir.StaticThunk
	for _, st := range prog.StaticThunks {
		if usedThunks[st.ThunkCName] {
			thunks = append(thunks, st)
		}
	}
	sub.StaticThunks = thunks

	var funcs []*ir.Function
	for _, fn := range prog.Funcs {
		if usedFns[fn.CName] {
			funcs = append(funcs, fn)
		}
	}
	sub.Funcs = funcs

	_ = pkg
	return &sub
}
