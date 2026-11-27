package sema

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

const (
	MaxMonomorphDepth            = 128
	MaxInstantiationsPerTemplate = 512
)

func (a *Analyzer) moduleTag(mod *Module) string {
	if mod == nil {
		return ""
	}
	if mod.Pkg != nil {
		return mod.Pkg.Tag
	}
	if mod == a.Prog.EntryMod || mod == a.coreMod {
		return ""
	}
	base := filepath.Base(mod.Path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return types.SanitizeCIdent(base)
}

func isCompEnumLangItem(li string) bool {
	return li == "target_os" || li == "target_arch" || li == "target_endian" || li == "comp_type_kind"
}

func isCompStructLangItem(li string) bool {
	return li == "target_info" || li == "comp_attr_info" || li == "comp_field_info" || li == "comp_variant_info" || li == "comp_elem_info" || li == "comp_distinct_info"
}

func astAttrsToTypeAttrs(attrs []ast.Attribute) []types.Attribute {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]types.Attribute, len(attrs))
	for i, a := range attrs {
		out[i] = types.Attribute{
			Name: a.Name,
			Args: append([]string(nil), a.Args...),
		}
	}
	return out
}

// typeTag is the C/LLVM symbol tag for a type named name declared in mod. Entry-package types
// normally have no tag, like std/core's; one that shadows a std/core type name (a user's own
// `Status` or `Error`) is tagged `main` so the two don't share symbols.
func (a *Analyzer) typeTag(mod *Module, name string) string {
	tag := a.moduleTag(mod)
	if tag != "" || mod == nil || a.corePkg == nil || mod.Pkg == a.corePkg {
		return tag
	}
	if a.coreTypeNames == nil {
		a.coreTypeNames = make(map[string]bool)
		for _, f := range a.corePkg.Files {
			for _, d := range f.ActiveDecls() {
				switch td := d.(type) {
				case *ast.StructDecl:
					if la := ast.FindAttribute(td.Attributes, "lang"); la != nil && len(la.Args) > 0 && isCompStructLangItem(strings.Trim(la.Args[0], "\"")) {
						continue
					}
					a.coreTypeNames[td.Name] = true
				case *ast.EnumDecl:
					if la := ast.FindAttribute(td.Attributes, "lang"); la != nil && len(la.Args) > 0 && isCompEnumLangItem(strings.Trim(la.Args[0], "\"")) {
						continue
					}
					a.coreTypeNames[td.Name] = true
				case *ast.InterfaceDecl:
					a.coreTypeNames[td.Name] = true
				case *ast.DistinctTypeDecl:
					a.coreTypeNames[td.Name] = true
				}
			}
		}
	}
	if a.coreTypeNames[name] {
		return "main"
	}
	return ""
}

func (a *Analyzer) checkAttributes(attrs []ast.Attribute, target any, span diag.Span, kind string) {
	if len(attrs) > 0 {
		ta := astAttrsToTypeAttrs(attrs)
		switch t := target.(type) {
		case *types.Struct:
			t.Attributes = ta
		case *types.Enum:
			t.Attributes = ta
		case *types.Distinct:
			t.Attributes = ta
		case *types.Interface:
			t.Attributes = ta
		}
	}
	hasInline := false
	hasNoInline := false

	for _, attr := range attrs {
		switch attr.Name {
		case "inline":
			if kind != "function" && kind != "method" {
				a.Rep.Errorf(attr.Span, "@inline attribute cannot be applied to a %s declaration", kind)
			}
			hasInline = true
			if fi, ok := target.(*FuncInstance); ok {
				fi.IsAlwaysInline = true
			}
		case "noinline":
			if kind != "function" && kind != "method" {
				a.Rep.Errorf(attr.Span, "@noinline attribute cannot be applied to a %s declaration", kind)
			}
			hasNoInline = true
			if fi, ok := target.(*FuncInstance); ok {
				fi.IsNoInline = true
			}
		case "must_use", "nodiscard":
			if kind != "function" && kind != "method" && kind != "struct" && kind != "enum" {
				a.Rep.Errorf(attr.Span, "@%s attribute cannot be applied to a %s declaration", attr.Name, kind)
			}
			if fi, ok := target.(*FuncInstance); ok {
				fi.MustUse = true
			} else if sym, ok := target.(*Symbol); ok {
				sym.MustUse = true
			} else if st, ok := target.(*types.Struct); ok {
				st.MustUse = true
			} else if en, ok := target.(*types.Enum); ok {
				en.MustUse = true
			}
		case "deprecated":
			msg := ""
			if len(attr.Args) > 0 {
				msg = strings.Trim(attr.Args[0], "\"")
			}
			if fi, ok := target.(*FuncInstance); ok {
				fi.Deprecated = true
				fi.DeprecatedMsg = msg
			} else if sym, ok := target.(*Symbol); ok {
				sym.Deprecated = true
				sym.DeprecatedMsg = msg
			} else if st, ok := target.(*types.Struct); ok {
				st.Deprecated = true
				st.DeprecatedMsg = msg
			} else if en, ok := target.(*types.Enum); ok {
				en.Deprecated = true
				en.DeprecatedMsg = msg
			}
		case "packed":
			if kind != "struct" {
				a.Rep.Errorf(attr.Span, "@packed attribute cannot be applied to a %s declaration", kind)
			}
			if st, ok := target.(*types.Struct); ok {
				st.Packed = true
			}
		case "test":
			if kind != "function" {
				a.Rep.Errorf(attr.Span, "@test attribute cannot be applied to a %s declaration", kind)
			}
			desc := ""
			if len(attr.Args) > 0 {
				desc = strings.Trim(attr.Args[0], "\"")
			}
			if fi, ok := target.(*FuncInstance); ok {
				if fi.IsMethod {
					a.Rep.Errorf(attr.Span, "@test attribute cannot be applied to a method")
				}
				if len(fi.Sig.Params) > 0 {
					a.Rep.Errorf(attr.Span, "@test function %q must not take parameters", fi.Name)
				}
				if desc == "" {
					desc = fi.Name
				}
				fi.IsTest = true
				fi.TestDesc = desc
				a.Prog.TestFuncs = append(a.Prog.TestFuncs, fi)
				testIdx := len(a.Prog.TestFuncs)
				tag := a.moduleTag(fi.Mod)
				if tag != "" {
					fi.CName = fmt.Sprintf("ll_test_%s_%d_%s", tag, testIdx, sanitizeSymbolName(fi.Name))
				} else {
					fi.CName = fmt.Sprintf("ll_test_%d_%s", testIdx, sanitizeSymbolName(fi.Name))
				}
			} else if sym, ok := target.(*Symbol); ok {
				if desc == "" {
					desc = sym.Name
				}
				sym.IsTest = true
				sym.TestDesc = desc
			}
		case "test_only":
			if fi, ok := target.(*FuncInstance); ok {
				fi.IsTestOnly = true
			} else if sym, ok := target.(*Symbol); ok {
				sym.IsTestOnly = true
			} else if st, ok := target.(*types.Struct); ok {
				st.IsTestOnly = true
			} else if en, ok := target.(*types.Enum); ok {
				en.IsTestOnly = true
			} else if dt, ok := target.(*types.Distinct); ok {
				dt.IsTestOnly = true
			} else if ifc, ok := target.(*types.Interface); ok {
				ifc.IsTestOnly = true
			} else if kind == "struct field" || kind == "enum variant" {
				a.Rep.Errorf(attr.Span, "@test_only attribute cannot be applied to a %s", kind)
			}
		case "track_caller":
			if kind != "function" && kind != "method" {
				a.Rep.Errorf(attr.Span, "@track_caller attribute cannot be applied to a %s declaration", kind)
			}
			if fi, ok := target.(*FuncInstance); ok {
				fi.TrackCaller = true
			} else if sym, ok := target.(*Symbol); ok {
				sym.TrackCaller = true
			}
		case "comptime_only":
			switch t := target.(type) {
			case *FuncInstance:
				if !t.IsConst {
					a.Rep.Errorf(attr.Span, "@comptime_only can only be applied to a `const` %s", kind)
				}
				t.ComptimeOnly = true
			case *Symbol:
				if !t.IsConst {
					a.Rep.Errorf(attr.Span, "@comptime_only can only be applied to a `const` %s", kind)
				}
				t.ComptimeOnly = true
			case *types.Struct:
				t.IsComptimeOnly = true
			case *types.Enum:
				t.IsComptimeOnly = true
			case *types.Distinct:
				t.IsComptimeOnly = true
			case *types.Interface:
				t.IsComptimeOnly = true
			default:
				a.Rep.Errorf(attr.Span, "@comptime_only attribute cannot be applied to a %s declaration", kind)
			}
		case "comptime_limit":
			if len(attr.Args) == 0 {
				a.Rep.Errorf(attr.Span, "@comptime_limit requires at least one limit (e.g. `@comptime_limit(steps: 5_000_000, memory: 128_000_000)` or `@comptime_limit(5_000_000)`)")
				continue
			}
			var stepLimit int
			var unrollLimit int
			var memLimit int64
			seenNamed := false
			for _, arg := range attr.Args {
				if key, valStr, hasColon := strings.Cut(arg, ":"); hasColon {
					seenNamed = true
					key = strings.TrimSpace(key)
					rawVal := strings.ReplaceAll(strings.Trim(strings.TrimSpace(valStr), "\""), "_", "")
					n, err := strconv.ParseInt(rawVal, 0, 64)
					if err != nil || n <= 0 {
						a.Rep.Errorf(attr.Span, "@comptime_limit %s must be a positive integer, got %q", key, strings.TrimSpace(valStr))
						continue
					}
					switch key {
					case "steps":
						if stepLimit > 0 {
							a.Rep.Errorf(attr.Span, "duplicate `steps` argument in @comptime_limit")
							continue
						}
						stepLimit = int(n)
					case "unroll":
						if unrollLimit > 0 {
							a.Rep.Errorf(attr.Span, "duplicate `unroll` argument in @comptime_limit")
							continue
						}
						unrollLimit = int(n)
					case "memory":
						if memLimit > 0 {
							a.Rep.Errorf(attr.Span, "duplicate `memory` argument in @comptime_limit")
							continue
						}
						memLimit = n
					default:
						a.Rep.Errorf(attr.Span, "unknown argument %q in @comptime_limit (expected `steps`, `unroll`, or `memory`)", key)
					}
				} else {
					if len(attr.Args) > 1 || seenNamed {
						a.Rep.Errorf(attr.Span, "@comptime_limit expects named arguments `steps:` / `unroll:` / `memory:` when specifying multiple limits")
						break
					}
					rawLimit := strings.ReplaceAll(strings.Trim(arg, "\""), "_", "")
					n, err := strconv.ParseInt(rawLimit, 0, 64)
					if err != nil || n <= 0 {
						a.Rep.Errorf(attr.Span, "@comptime_limit step count must be a positive integer, got %q", arg)
						continue
					}
					stepLimit = int(n)
					unrollLimit = int(n)
				}
			}
			if fi, ok := target.(*FuncInstance); ok {
				if stepLimit > 0 {
					fi.ComptimeLimit = stepLimit
				}
				if unrollLimit > 0 {
					fi.ComptimeUnrollLimit = unrollLimit
				}
				if memLimit > 0 {
					fi.ComptimeMemLimit = memLimit
				}
			} else if sym, ok := target.(*Symbol); ok {
				if stepLimit > 0 {
					sym.ComptimeLimit = stepLimit
				}
				if unrollLimit > 0 {
					sym.ComptimeUnrollLimit = unrollLimit
				}
				if memLimit > 0 {
					sym.ComptimeMemLimit = memLimit
				}
			} else {
				a.Rep.Errorf(attr.Span, "@comptime_limit attribute cannot be applied to a %s declaration", kind)
			}
		case "lang":
			if kind == "struct field" || kind == "enum variant" {
				a.Rep.Errorf(attr.Span, "@lang attribute cannot be applied to a %s", kind)
			}
			// handled specially on interfaces and top-level func/symbols
		case "derive":
			if kind != "struct" && kind != "enum" && kind != "distinct type" {
				a.Rep.Errorf(attr.Span, "@derive can only be applied to a struct, enum, or distinct type, not a %s", kind)
			}
			// expanded into methods by expandDerives
		case "derivable":
			if kind != "interface" {
				a.Rep.Errorf(attr.Span, "@derivable can only be applied to an interface, not a %s", kind)
			}
		case "doc":
			a.Rep.Errorf(attr.Span, "unknown attribute @doc (use `//` doc comments instead)")
		default:
			switch kind {
			case "struct", "enum", "interface", "distinct type", "struct field", "enum variant":
				// Custom attributes allowed on type declarations, struct fields, and enum variants
				// so `comp.attrs_of[T]()`, `FieldInfo.attrs`, and `VariantInfo.attrs` can inspect them.
			default:
				a.Rep.Errorf(attr.Span, "unknown attribute @%s", attr.Name)
			}
		}
	}

	if hasInline && hasNoInline {
		a.Rep.Errorf(span, "cannot specify both @inline and @noinline")
	}
}

func isCompNamespaceOnlyType(t types.Type) bool {
	switch v := t.(type) {
	case *types.Struct:
		return v.CompNamespaceOnly
	case *types.Enum:
		return v.CompNamespaceOnly
	case *types.Distinct:
		return v.CompNamespaceOnly
	case *types.Interface:
		return v.CompNamespaceOnly
	}
	return false
}

func (a *Analyzer) lookupTypeInModule(mod *Module, moduleAlias, name string) types.Type {
	if moduleAlias == "comp" {
		if target, hasUserMod := mod.Aliased[moduleAlias]; hasUserMod && target != nil {
			if !a.SamePackage(mod, target) && !IsExported(name) {
				return nil
			}
			return target.Types[name]
		}
		return a.Prog.CompTypes[name]
	}
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		if !a.SamePackage(mod, target) && !IsExported(name) {
			return nil
		}
		return target.Types[name]
	}
	if p := types.LookupPrimitive(name); p != nil {
		return p
	}
	if t, ok := mod.Types[name]; ok {
		return t
	}
	for _, imp := range mod.Imports {
		if !a.SamePackage(mod, imp) && !IsExported(name) {
			continue
		}
		if t, ok := imp.Types[name]; ok {
			if !a.SamePackage(mod, imp) && isCompNamespaceOnlyType(t) {
				continue
			}
			return t
		}
	}
	return nil
}

func (a *Analyzer) lookupGenStructInModule(mod *Module, moduleAlias, name string) *GenericStructTemplate {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		if !a.SamePackage(mod, target) && !IsExported(name) {
			return nil
		}
		return target.GenericStructs[name]
	}
	if g, ok := mod.GenericStructs[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if !a.SamePackage(mod, imp) && !IsExported(name) {
			continue
		}
		if g, ok := imp.GenericStructs[name]; ok {
			return g
		}
	}
	return nil
}

func (a *Analyzer) lookupGenEnumInModule(mod *Module, moduleAlias, name string) *GenericEnumTemplate {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		if !a.SamePackage(mod, target) && !IsExported(name) {
			return nil
		}
		return target.GenericEnums[name]
	}
	if g, ok := mod.GenericEnums[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if !a.SamePackage(mod, imp) && !IsExported(name) {
			continue
		}
		if g, ok := imp.GenericEnums[name]; ok {
			return g
		}
	}
	return nil
}

func (a *Analyzer) lookupGenInterfaceInModule(mod *Module, moduleAlias, name string) *GenericInterfaceTemplate {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		if !a.SamePackage(mod, target) && !IsExported(name) {
			return nil
		}
		return target.GenericInterfaces[name]
	}
	if g, ok := mod.GenericInterfaces[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if !a.SamePackage(mod, imp) && !IsExported(name) {
			continue
		}
		if g, ok := imp.GenericInterfaces[name]; ok {
			return g
		}
	}
	return nil
}

func (a *Analyzer) lookupTopSymbol(mod *Module, moduleAlias, name string) *Symbol {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		if !a.SamePackage(mod, target) && !IsExported(name) {
			return nil
		}
		return target.Symbols[name]
	}
	if s, ok := mod.Symbols[name]; ok {
		return s
	}
	for _, imp := range mod.Imports {
		if !a.SamePackage(mod, imp) && !IsExported(name) {
			continue
		}
		if s, ok := imp.Symbols[name]; ok {
			return s
		}
	}
	return nil
}

func (a *Analyzer) lookupGenFunc(mod *Module, moduleAlias, name string) *GenericFuncTemplate {
	if moduleAlias != "" {
		target, ok := mod.Aliased[moduleAlias]
		if !ok {
			return nil
		}
		if !a.SamePackage(mod, target) && !IsExported(name) {
			return nil
		}
		return target.GenericFuncs[name]
	}
	if g, ok := mod.GenericFuncs[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if !a.SamePackage(mod, imp) && !IsExported(name) {
			continue
		}
		if g, ok := imp.GenericFuncs[name]; ok {
			return g
		}
	}
	return nil
}

func (a *Analyzer) initCompTarget() {
	if a.corePkg == nil {
		return
	}
	// Stage 1: Register @lang("target_os" | "target_arch" | "target_endian" | "comp_type_kind") enums first
	// because comp structs may reference them in their fields.
	for _, mod := range a.corePkg.Files {
		for _, d := range mod.ActiveDecls() {
			ed, ok := d.(*ast.EnumDecl)
			if !ok {
				continue
			}
			la := ast.FindAttribute(ed.Attributes, "lang")
			if la == nil || len(la.Args) == 0 {
				continue
			}
			li := strings.Trim(la.Args[0], "\"")
			if !isCompEnumLangItem(li) {
				continue
			}
			if _, dup := mod.Types[ed.Name]; dup {
				continue
			}
			en := &types.Enum{
				Name:              ed.Name,
				CName:             "ll_e_comp_" + ed.Name,
				PkgDir:            mod.Dir,
				Doc:               ed.Doc,
				Underlying:        types.TypeInt,
				CompNamespaceOnly: true,
			}
			a.checkAttributes(ed.Attributes, en, ed.Span, "enum")
			for idx, v := range ed.Variants {
				en.Variants = append(en.Variants, types.EnumVariant{
					Name:       v.Name,
					CName:      "v_" + types.SanitizeCIdent(v.Name),
					Value:      int64(idx),
					Kind:       types.VariantUnit,
					Attributes: astAttrsToTypeAttrs(v.Attributes),
					Doc:        v.Doc,
				})
			}
			mod.Types[ed.Name] = en
			a.Prog.CompTypes[ed.Name] = en
			a.Prog.EnumDecls[en] = ed
			a.Prog.EnumMods[en] = mod
			a.Prog.TypeSpans[en] = ed.Span
			a.Prog.Enums = append(a.Prog.Enums, en)
			switch li {
			case "target_os":
				a.Prog.EnumTargetOs = en
			case "target_arch":
				a.Prog.EnumTargetArch = en
			case "target_endian":
				a.Prog.EnumEndian = en
			case "comp_type_kind":
				a.Prog.EnumTypeKind = en
			}
		}
	}
	// Stage 2: Register @lang comp structs first, then populate fields so nested refs like `[AttrInfo]` resolve cleanly.
	type compStructEntry struct {
		mod *Module
		sd  *ast.StructDecl
		st  *types.Struct
	}
	var stage2 []compStructEntry
	for _, mod := range a.corePkg.Files {
		for _, d := range mod.ActiveDecls() {
			sd, ok := d.(*ast.StructDecl)
			if !ok {
				continue
			}
			la := ast.FindAttribute(sd.Attributes, "lang")
			if la == nil || len(la.Args) == 0 {
				continue
			}
			li := strings.Trim(la.Args[0], "\"")
			if !isCompStructLangItem(li) {
				continue
			}
			if _, dup := mod.Types[sd.Name]; dup {
				continue
			}
			st := &types.Struct{
				Name:              sd.Name,
				CName:             "ll_s_comp_" + sd.Name,
				PkgDir:            mod.Dir,
				Doc:               sd.Doc,
				Lang:              li,
				CompNamespaceOnly: true,
			}
			a.checkAttributes(sd.Attributes, st, sd.Span, "struct")
			mod.Types[sd.Name] = st
			a.Prog.CompTypes[sd.Name] = st
			a.Prog.StructDecls[st] = sd
			a.Prog.StructMods[st] = mod
			a.Prog.TypeSpans[st] = sd.Span
			a.Prog.Structs = append(a.Prog.Structs, st)
			switch li {
			case "target_info":
				a.Prog.StructTarget = st
			case "comp_attr_info":
				a.Prog.StructAttrInfo = st
			case "comp_field_info":
				a.Prog.StructFieldInfo = st
			case "comp_variant_info":
				a.Prog.StructVariantInfo = st
			case "comp_elem_info":
				a.Prog.StructElemInfo = st
			case "comp_distinct_info":
				a.Prog.StructDistinctInfo = st
			}
			stage2 = append(stage2, compStructEntry{mod: mod, sd: sd, st: st})
		}
	}
	for _, item := range stage2 {
		a.populateStructFields(item.mod, item.st, item.sd, nil)
	}
	if tv := a.buildTargetCtVal(); tv != nil {
		a.Prog.CompTargetSym = &Symbol{
			Name:         "TARGET",
			CName:        "ll_comp_TARGET",
			Mod:          a.coreMod,
			Type:         a.Prog.StructTarget,
			IsBind:       true,
			IsConst:      true,
			BindKind:     BindOwned,
			IsTopLevel:   true,
			ComptimeOnly: true,
			ConstVal:     tv,
		}
	}
}

func (a *Analyzer) pruneTopComptimeIfs() {
	hasAnyTopComptimeIf := func() bool {
		for _, mod := range a.Prog.Modules {
			for _, d := range mod.ActiveDecls() {
				if _, ok := d.(*ast.TopComptimeIfDecl); ok {
					return true
				}
			}
		}
		return false
	}

	if !hasAnyTopComptimeIf() {
		return
	}

	var speculativeSyms []*Symbol
	preEvalPureTopConsts := func() bool {
		changed := false
		for _, mod := range a.Prog.Modules {
			if a.collectTopLevelIntConsts(mod) {
				changed = true
			}
			for _, d := range mod.ActiveDecls() {
				vd, ok := d.(*ast.VarDecl)
				if !ok || !vd.IsConst || vd.IsOptName || len(vd.TypeParams) > 0 || vd.Value == nil {
					continue
				}
				if _, isFn := vd.Value.(*ast.FuncLit); isFn || mod.Symbols[vd.Name] != nil {
					continue
				}
				if exprContainsCall(vd.Value) {
					continue
				}
				sym, cv, okEval := a.speculativeEvalTopConst(mod, vd)
				if okEval && sym != nil && cv != nil {
					mod.Symbols[vd.Name] = sym
					speculativeSyms = append(speculativeSyms, sym)
					changed = true
				}
			}
		}
		return changed
	}

	for progress := true; progress; {
		progress = false
		if preEvalPureTopConsts() {
			progress = true
		}
		for _, mod := range a.Prog.Modules {
			decls := mod.ActiveDecls()
			var next []ast.Decl
			modChanged := false
			for _, d := range decls {
				tc, ok := d.(*ast.TopComptimeIfDecl)
				if !ok {
					next = append(next, d)
					continue
				}
				chosen, evaluated := a.selectTopComptimeIfBranch(mod, tc, false)
				if !evaluated {
					next = append(next, d)
					continue
				}
				modChanged = true
				progress = true
				next = append(next, chosen...)
			}
			if modChanged {
				mod.Decls = next
			}
		}
	}

	// Final pass: any remaining TopComptimeIfDecl failed speculative evaluation — evaluate with real diagnostics
	// and strip invalid TopComptimeIfDecl nodes before Pass 1B.
	for _, mod := range a.Prog.Modules {
		decls := mod.ActiveDecls()
		hasLeft := false
		for _, d := range decls {
			if _, ok := d.(*ast.TopComptimeIfDecl); ok {
				hasLeft = true
				break
			}
		}
		if !hasLeft {
			continue
		}
		var next []ast.Decl
		for _, d := range decls {
			tc, ok := d.(*ast.TopComptimeIfDecl)
			if !ok {
				next = append(next, d)
				continue
			}
			chosen, evaluated := a.selectTopComptimeIfBranch(mod, tc, true)
			if evaluated {
				next = append(next, chosen...)
			}
		}
		mod.Decls = next
	}
	// Reset speculative ConstVals so Pass 2 re-evaluates them once generic stdlib types
	// (`List`, `Set`, `Map`) and TopBodyMaps are registered, keeping `IsConstInt` intact.
	for _, sym := range speculativeSyms {
		sym.ConstVal = nil
	}
}

func exprContainsCall(e ast.Expr) bool {
	if e == nil {
		return false
	}
	found := false
	switch v := e.(type) {
	case *ast.CallExpr:
		return true
	case *ast.UnaryExpr:
		return exprContainsCall(v.Operand)
	case *ast.BinaryExpr:
		return exprContainsCall(v.Left) || exprContainsCall(v.Right)
	case *ast.ComptimeExpr:
		return exprContainsCall(v.Expr)
	case *ast.MemberExpr:
		return exprContainsCall(v.Base)
	case *ast.IndexExpr:
		return exprContainsCall(v.Base) || exprContainsCall(v.Index)
	case *ast.IfExpr:
		if exprContainsCall(v.Cond) || exprContainsCall(v.Then) || exprContainsCall(v.Else) {
			return true
		}
	case *ast.BlockExpr, *ast.MatchExpr, *ast.ForExpr, *ast.WhileExpr:
		return true
	}
	return found
}

func (a *Analyzer) speculativeEvalTopConst(mod *Module, vd *ast.VarDecl) (*Symbol, *ctVal, bool) {
	tmpRep := diag.NewReporter()
	savedRep := a.Rep
	a.Rep = tmpRep
	prevBM := a.curBM
	a.curBM = NewBodyMaps()
	defer func() {
		a.curBM = prevBM
		a.Rep = savedRep
	}()

	var explicitTy types.Type
	if vd.Type != nil {
		explicitTy = a.resolveValueType(mod, vd.Type, nil, false)
		if explicitTy == types.TypeInvalid || tmpRep.HasErrors() {
			return nil, nil, false
		}
	}
	fc := a.newFuncChecker(mod, nil)
	fc.comptimeExprDepth++
	fc.pushScope()
	var got types.Type
	if explicitTy != nil {
		got = fc.checkExprExpect(vd.Value, explicitTy, true)
		fc.verifyAssignable(got, explicitTy, vd.Value, vd.Value.GetSpan(), "top-level const")
	} else {
		got = fc.checkExpr(vd.Value, true)
	}
	fc.popScope()
	if tmpRep.HasErrors() || got == nil || got == types.TypeInvalid || got == types.TypeVoid {
		return nil, nil, false
	}
	targetTy := explicitTy
	if targetTy == nil {
		targetTy = got
	}
	cname := "ll_g_" + types.SanitizeCIdent(vd.Name)
	if tag := a.moduleTag(mod); tag != "" {
		cname = fmt.Sprintf("ll_g_%s_%s", tag, types.SanitizeCIdent(vd.Name))
	}
	sym := &Symbol{
		Name:       vd.Name,
		CName:      cname,
		Type:       targetTy,
		IsBind:     true,
		IsConst:    true,
		BindKind:   BindOwned,
		IsTopLevel: true,
		Mod:        mod,
		Doc:        vd.Doc,
		Span:       vd.Span,
	}
	a.checkAttributes(vd.Attributes, sym, vd.Span, "variable")
	if mod.IsTestFile {
		sym.IsTestOnly = true
	}
	if !sym.ComptimeOnly && types.FindComptimeOnlyType(targetTy) != nil {
		return nil, nil, false
	}

	st := newCtState(a, mod, sym.ComptimeLimit)
	if sym.ComptimeMemLimit > 0 {
		st.memLimit = sym.ComptimeMemLimit
		st.explicitMemLimit = true
	}
	st.frames = []ctFrame{{
		mod:      mod,
		bm:       a.curBM,
		callSpan: vd.Span,
		locals:   make(map[*Symbol]*ctSlot),
		alive:    newFrameAlive(),
	}}
	val, sig := st.evalExpr(vd.Value)
	if sig.kind != sigNone || val == nil || tmpRep.HasErrors() {
		return nil, nil, false
	}
	if !sym.ComptimeOnly {
		if errMsg := a.validateCrossesToRuntime(val, targetTy); errMsg != "" {
			return nil, nil, false
		}
	}
	freezeCtVal(val)
	sym.ConstVal = val
	if p := types.UnderlyingPrimitive(targetTy); p != nil && p.IsInteger() {
		sym.IsConstInt = true
		sym.ConstIntVal = val.asInt64()
	}
	return sym, val, true
}

func (a *Analyzer) selectTopComptimeIfBranch(mod *Module, tc *ast.TopComptimeIfDecl, reportErrors bool) ([]ast.Decl, bool) {
	var tmpRep *diag.Reporter
	savedRep := a.Rep
	if !reportErrors {
		tmpRep = diag.NewReporter()
		a.Rep = tmpRep
	}
	prevBM := a.curBM
	a.curBM = a.Prog.TopBodyMaps
	defer func() {
		a.curBM = prevBM
		a.Rep = savedRep
	}()

	for cur := tc; cur != nil; cur = cur.ElseIf {
		beforeErrs := a.errorCount()
		fc := a.newFuncChecker(mod, nil)
		fc.comptimeExprDepth++
		fc.pushScope()
		condTy := fc.checkExprExpect(cur.Cond, types.TypeBool, true)
		fc.popScope()
		if condTy != types.TypeInvalid && !condTy.Equals(types.TypeBool) {
			a.Rep.Errorf(cur.Cond.GetSpan(), "top-level `comptime if` condition must have type `bool`, got %s", condTy.String())
		}
		if a.errorCount() > beforeErrs {
			return nil, false
		}
		st := newCtState(a, mod, 0)
		st.frames = []ctFrame{{
			mod:      mod,
			bm:       a.curBM,
			callSpan: cur.Cond.GetSpan(),
			locals:   make(map[*Symbol]*ctSlot),
		}}
		val, sig := st.evalExpr(cur.Cond)
		if sig.kind == sigAbort {
			st.reportAbort(sig, cur.Cond.GetSpan())
			return nil, false
		}
		if sig.kind != sigNone || val == nil || val.kind != ctBool {
			a.Rep.Errorf(cur.Cond.GetSpan(), "top-level `comptime if` condition must evaluate to a compile-time `bool`")
			return nil, false
		}
		if val.b {
			return cur.ThenDecls, true
		}
		if cur.ElseIf == nil && len(cur.ElseDecls) > 0 {
			return cur.ElseDecls, true
		}
	}
	return nil, true
}

func (a *Analyzer) runTypeAndSymbolRegistration() {
	// Pass 0: Register `comp.Target*` types and `comp.TARGET` before Pass 1A / top-level `comptime if`.
	a.initCompTarget()

	// Pass 0.5: Evaluate and prune top-level `comptime if` declarations (`H4` Option 3) before type/symbol tables.
	a.pruneTopComptimeIfs()
	if a.Rep.HasErrors() && !a.ideMode {
		return
	}

	// Pass 1A: Multi-pass constant folding for simple integer constants across all modules
	// so `[N: T]` array sizes and enum discriminants can depend on constants in any order or module.
	for changed := true; changed; {
		changed = false
		for _, mod := range a.Prog.Modules {
			if a.collectTopLevelIntConsts(mod) {
				changed = true
			}
		}
	}

	// Pass 1B: Register nominal type headers across all modules (order-independent!).
	for _, mod := range a.Prog.Modules {
		a.collectGenericDropReceivers(mod)
	}
	for _, mod := range a.Prog.Modules {
		a.registerModuleTypeHeaders(mod)
	}
	if a.Rep.HasErrors() && !a.ideMode {
		return
	}

	// Pass 1C: Resolve Distinct base types, Enum variants, Interface signatures, and non-generic Struct fields.
	for _, mod := range a.Prog.Modules {
		a.resolveDistinctsAndEnums(mod)
	}
	for _, mod := range a.Prog.Modules {
		a.resolveInterfaces(mod)
	}
	for _, mod := range a.Prog.Modules {
		a.resolveStructFields(mod)
	}
	if a.Rep.HasErrors() && !a.ideMode {
		return
	}

	// Pass 1D: Detect value vs optional cycles across structs (marking BoxedOptional on recursive `?Struct`).
	a.checkStructRecursion()
	if a.Rep.HasErrors() && !a.ideMode {
		return
	}

	// Pass 1E: Register extern headers/functions, top-level functions/vars, and methods
	// (including those generated by `@derive`).
	for _, mod := range a.Prog.Modules {
		a.registerFunctionsAndMethods(mod)
	}
	a.resolveDerivableAttrs()
	for _, mod := range a.Prog.Modules {
		a.expandDerives(mod)
	}
	a.validateAllConstGenericParams()
	a.instantiatePendingDrops()
	a.validateDerives()
}

// collectGenericDropReceivers records which generic types declare a `drop` method.
func (a *Analyzer) collectGenericDropReceivers(mod *Module) {
	for _, d := range mod.ActiveDecls() {
		if md, ok := d.(*ast.MethodDecl); ok && md.Name == "drop" && len(md.RecvTypeParams) > 0 {
			a.genDropRecv[md.RecvType] = true
		}
	}
}

// instantiatePendingDrops instantiates `drop` for generic instances created before methods
// were registered.
func (a *Analyzer) instantiatePendingDrops() {
	pending := a.pendingDrops
	a.pendingDrops = nil
	for _, t := range pending {
		switch inst := t.(type) {
		case *types.Struct:
			if gm, ok := a.genMethods[inst.Name]["drop"]; ok {
				_ = a.instantiateMethod(gm, inst, nil, a.Prog.TypeSpans[inst])
			}
		case *types.Enum:
			if gm, ok := a.genMethods[inst.Name]["drop"]; ok {
				_ = a.instantiateMethod(gm, inst, nil, a.Prog.TypeSpans[inst])
			}
		}
	}
}

func (a *Analyzer) collectTopLevelIntConsts(mod *Module) bool {
	changed := false
	for _, d := range mod.ActiveDecls() {
		vd, ok := d.(*ast.VarDecl)
		if !ok || (!vd.IsBind && !vd.IsConst) || len(vd.TypeParams) > 0 || vd.IsOptName {
			continue
		}
		if _, already := mod.Symbols[vd.Name]; already {
			continue
		}
		ty := types.TypeInt
		if vd.Type != nil {
			nt, isNt := vd.Type.(*ast.NamedType)
			if !isNt || nt.Module != "" || len(nt.TypeArgs) > 0 {
				continue
			}
			p := types.LookupPrimitive(nt.Name)
			if p == nil || !p.IsInteger() {
				continue
			}
			ty = p
			// Non-64-bit signed/unsigned arithmetic (e.g., `const B: u8 = 200 + 56`)
			// requires typed wrapping in Pass 2 (`resolveTopVar`), unless it's a pure literal.
			if p.Kind != types.PrimI64 {
				switch lit := vd.Value.(type) {
				case *ast.IntLit:
					if !p.FitsUnsignedLiteral(lit.Val, false) {
						continue
					}
				case *ast.UnaryExpr:
					if lit.Op == token.Minus {
						if il, isIL := lit.Operand.(*ast.IntLit); isIL {
							if !p.FitsUnsignedLiteral(il.Val, true) {
								continue
							}
						} else {
							continue
						}
					} else {
						continue
					}
				default:
					continue
				}
			}
		}
		if val, ok2 := a.evalSimpleConstInt(mod, vd.Value); ok2 {
			mod.Symbols[vd.Name] = &Symbol{
				Name:        vd.Name,
				CName:       types.SanitizeCIdent(vd.Name),
				Mod:         mod,
				Type:        ty,
				IsBind:      true,
				IsConst:     vd.IsConst,
				BindKind:    BindOwned,
				IsTopLevel:  true,
				IsConstInt:  true,
				ConstIntVal: val,
				Doc:         vd.Doc,
				Span:        vd.Span,
			}
			changed = true
		}
	}
	return changed
}

func (a *Analyzer) registerModuleTypeHeaders(mod *Module) {
	for _, d := range mod.ActiveDecls() {
		switch td := d.(type) {
		case *ast.StructDecl:
			var langItem string
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				if len(langAttr.Args) == 0 {
					a.Rep.Errorf(langAttr.Span, "@lang attribute requires a string argument (e.g. @lang(\"list\"))")
				} else {
					langItem = strings.Trim(langAttr.Args[0], "\"")
					switch langItem {
					case "list":
						if a.Prog.GenStructList != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"list\")")
						}
					case "array":
						if a.Prog.GenStructArray != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"array\")")
						}
					case "set":
						if a.Prog.GenStructSet != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"set\")")
						}
					case "map":
						if a.Prog.GenStructMap != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"map\")")
						}
					case "range":
						if a.Prog.GenStructRange != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"range\")")
						}
					case "arena":
						if a.Prog.StructArena != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"arena\")")
						}
					case "target_info":
						if existing, ok := mod.Types[td.Name].(*types.Struct); ok && existing == a.Prog.StructTarget && existing != nil {
							continue
						}
						if a.Prog.StructTarget != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"target_info\")")
						}
					case "comp_attr_info":
						if existing, ok := mod.Types[td.Name].(*types.Struct); ok && existing == a.Prog.StructAttrInfo && existing != nil {
							continue
						}
						if a.Prog.StructAttrInfo != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"comp_attr_info\")")
						}
					case "comp_field_info":
						if existing, ok := mod.Types[td.Name].(*types.Struct); ok && existing == a.Prog.StructFieldInfo && existing != nil {
							continue
						}
						if a.Prog.StructFieldInfo != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"comp_field_info\")")
						}
					case "comp_variant_info":
						if existing, ok := mod.Types[td.Name].(*types.Struct); ok && existing == a.Prog.StructVariantInfo && existing != nil {
							continue
						}
						if a.Prog.StructVariantInfo != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"comp_variant_info\")")
						}
					case "comp_elem_info":
						if existing, ok := mod.Types[td.Name].(*types.Struct); ok && existing == a.Prog.StructElemInfo && existing != nil {
							continue
						}
						if a.Prog.StructElemInfo != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"comp_elem_info\")")
						}
					case "comp_distinct_info":
						if existing, ok := mod.Types[td.Name].(*types.Struct); ok && existing == a.Prog.StructDistinctInfo && existing != nil {
							continue
						}
						if a.Prog.StructDistinctInfo != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"comp_distinct_info\")")
						}
					default:
						a.Rep.Errorf(td.Span, "unknown struct lang item @lang(%q); supported struct lang items: \"list\", \"array\", \"set\", \"map\", \"range\", \"arena\", \"target_info\", \"comp_*\"", langItem)
					}
				}
			}
			if len(td.TypeParams) > 0 {
				if _, dup := mod.GenericStructs[td.Name]; dup {
					a.Rep.Errorf(td.Span, "duplicate type definition %q", td.Name)
					continue
				}
				tmpl := &GenericStructTemplate{
					Decl:      td,
					Mod:       mod,
					Instances: make(map[string]*types.Struct),
				}
				mod.GenericStructs[td.Name] = tmpl
				a.Prog.GenStructs = append(a.Prog.GenStructs, tmpl)
				switch langItem {
				case "list":
					a.Prog.GenStructList = tmpl
				case "array":
					a.Prog.GenStructArray = tmpl
				case "set":
					a.Prog.GenStructSet = tmpl
				case "map":
					a.Prog.GenStructMap = tmpl
				case "range":
					a.Prog.GenStructRange = tmpl
				}
				continue
			}
			if langItem != "" && langItem != "arena" && !isCompStructLangItem(langItem) {
				a.Rep.Errorf(td.Span, "struct lang item @lang(%q) must be a generic struct", langItem)
			}
			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			cname := "ll_s_" + td.Name
			if tag := a.typeTag(mod, td.Name); tag != "" {
				cname = fmt.Sprintf("ll_s_%s_%s", tag, td.Name)
			}
			if isCompStructLangItem(langItem) {
				cname = "ll_s_comp_" + td.Name
			}
			st := &types.Struct{
				Name:   td.Name,
				CName:  cname,
				PkgDir: mod.Dir,
				Packed: td.Packed || ast.FindAttribute(td.Attributes, "packed") != nil,
				Doc:    td.Doc,
			}
			a.checkAttributes(td.Attributes, st, td.Span, "struct")
			if langItem == "arena" {
				st.Lang = "arena"
				a.Prog.StructArena = st
			} else if isCompStructLangItem(langItem) {
				st.Lang = langItem
				st.CompNamespaceOnly = true
				a.Prog.CompTypes[td.Name] = st
				switch langItem {
				case "target_info":
					a.Prog.StructTarget = st
				case "comp_attr_info":
					a.Prog.StructAttrInfo = st
				case "comp_field_info":
					a.Prog.StructFieldInfo = st
				case "comp_variant_info":
					a.Prog.StructVariantInfo = st
				case "comp_elem_info":
					a.Prog.StructElemInfo = st
				case "comp_distinct_info":
					a.Prog.StructDistinctInfo = st
				}
			}
			if mod.IsTestFile {
				st.IsTestOnly = true
			}
			mod.Types[td.Name] = st
			a.Prog.StructDecls[st] = td
			a.Prog.StructMods[st] = mod
			a.Prog.TypeSpans[st] = td.Span
			a.Prog.Structs = append(a.Prog.Structs, st)

		case *ast.EnumDecl:
			var enumLangItem string
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				if len(langAttr.Args) == 0 {
					a.Rep.Errorf(langAttr.Span, "@lang attribute requires a string argument")
				} else {
					enumLangItem = strings.Trim(langAttr.Args[0], "\"")
					switch enumLangItem {
					case "target_os":
						if existing, ok := mod.Types[td.Name].(*types.Enum); ok && existing == a.Prog.EnumTargetOs && existing != nil {
							continue
						}
						if a.Prog.EnumTargetOs != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"target_os\")")
						}
					case "target_arch":
						if existing, ok := mod.Types[td.Name].(*types.Enum); ok && existing == a.Prog.EnumTargetArch && existing != nil {
							continue
						}
						if a.Prog.EnumTargetArch != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"target_arch\")")
						}
					case "target_endian":
						if existing, ok := mod.Types[td.Name].(*types.Enum); ok && existing == a.Prog.EnumEndian && existing != nil {
							continue
						}
						if a.Prog.EnumEndian != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"target_endian\")")
						}
					case "comp_type_kind":
						if existing, ok := mod.Types[td.Name].(*types.Enum); ok && existing == a.Prog.EnumTypeKind && existing != nil {
							continue
						}
						if a.Prog.EnumTypeKind != nil {
							a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"comp_type_kind\")")
						}
					default:
						a.Rep.Errorf(langAttr.Span, "unknown enum lang item @lang(%q); supported: \"target_os\", \"target_arch\", \"target_endian\", \"comp_type_kind\"", enumLangItem)
					}
				}
			}
			isTagged := len(td.TypeParams) > 0 || td.IsResult
			if !isTagged {
				for _, v := range td.Variants {
					if v.PayloadType != nil {
						isTagged = true
						break
					}
				}
			}
			if len(td.TypeParams) > 0 {
				if a.checkDupType(mod, td.Name, td.Span) {
					continue
				}
				mod.GenericEnums[td.Name] = &GenericEnumTemplate{
					Decl:      td,
					Mod:       mod,
					Instances: make(map[string]*types.Enum),
				}
				continue
			}
			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			cname := "ll_e_" + td.Name
			if tag := a.typeTag(mod, td.Name); tag != "" {
				cname = fmt.Sprintf("ll_e_%s_%s", tag, td.Name)
			}
			if enumLangItem != "" {
				cname = "ll_e_comp_" + td.Name
			}
			en := &types.Enum{
				Name:       td.Name,
				CName:      cname,
				PkgDir:     mod.Dir,
				Doc:        td.Doc,
				IsTagged:   isTagged,
				IsResult:   td.IsResult,
				Underlying: types.TypeInt,
			}
			a.checkAttributes(td.Attributes, en, td.Span, "enum")
			switch enumLangItem {
			case "target_os":
				en.CompNamespaceOnly = true
				a.Prog.EnumTargetOs = en
				a.Prog.CompTypes[td.Name] = en
			case "target_arch":
				en.CompNamespaceOnly = true
				a.Prog.EnumTargetArch = en
				a.Prog.CompTypes[td.Name] = en
			case "target_endian":
				en.CompNamespaceOnly = true
				a.Prog.EnumEndian = en
				a.Prog.CompTypes[td.Name] = en
			case "comp_type_kind":
				en.CompNamespaceOnly = true
				a.Prog.EnumTypeKind = en
				a.Prog.CompTypes[td.Name] = en
			}
			if mod.IsTestFile {
				en.IsTestOnly = true
			}
			mod.Types[td.Name] = en
			a.Prog.EnumDecls[en] = td
			a.Prog.EnumMods[en] = mod
			a.Prog.TypeSpans[en] = td.Span
			a.Prog.Enums = append(a.Prog.Enums, en)

		case *ast.InterfaceDecl:
			var langItem string
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				if len(langAttr.Args) == 0 {
					a.Rep.Errorf(langAttr.Span, "@lang attribute requires a string argument (e.g. @lang(\"printable\"))")
					continue
				}
				langItem = strings.Trim(langAttr.Args[0], "\"")
			}

			if len(td.TypeParams) > 0 {
				if derivAttr := ast.FindAttribute(td.Attributes, "derivable"); derivAttr != nil {
					a.Rep.Errorf(derivAttr.Span, "@derivable cannot be applied to generic interface %s", td.Name)
				}
				if a.checkDupType(mod, td.Name, td.Span) {
					continue
				}
				tmpl := &GenericInterfaceTemplate{
					Decl:      td,
					Mod:       mod,
					Instances: make(map[string]*types.Interface),
				}
				mod.GenericInterfaces[td.Name] = tmpl
				a.Prog.GenInterfaces = append(a.Prog.GenInterfaces, tmpl)
				if langItem == "collectable" || td.Name == "Collectable" {
					a.Prog.GenIfaceCollectable = tmpl
				} else if langItem == "iterable" || td.Name == "Iterable" {
					a.Prog.GenIfaceIterable = tmpl
				} else if langItem == "iterator" || td.Name == "Iterator" {
					a.Prog.GenIfaceIterator = tmpl
				} else if langItem == "indexable" || td.Name == "Indexable" {
					a.Prog.GenIfaceIndexable = tmpl
				} else if langItem == "key_indexable" || td.Name == "KeyIndexable" {
					a.Prog.GenIfaceKeyIndexable = tmpl
				} else if langItem == "containable" || td.Name == "Containable" {
					a.Prog.GenIfaceContainable = tmpl
				} else if langItem != "" {
					a.Rep.Errorf(td.Span, "unknown generic interface lang item @lang(%q); supported: \"collectable\", \"iterable\", \"iterator\", \"indexable\", \"key_indexable\", \"containable\"", langItem)
				}
				continue
			}

			if langItem == "printable" || td.Name == "Printable" {
				if a.Prog.TypeSpans[a.Prog.IfacePrintable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfacePrintable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"printable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfacePrintable].String())
					continue
				}
				a.Prog.IfacePrintable.Name = td.Name
				a.Prog.IfacePrintable.Doc = td.Doc
				a.Prog.TypeSpans[a.Prog.IfacePrintable] = td.Span
				a.finishBuiltinInterfaceDecl(mod, a.Prog.IfacePrintable, td)
				continue
			}

			if langItem == "copyable" || td.Name == "Copyable" {
				if a.Prog.TypeSpans[a.Prog.IfaceCopyable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceCopyable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"copyable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceCopyable].String())
					continue
				}
				a.Prog.IfaceCopyable.Name = td.Name
				a.Prog.IfaceCopyable.Doc = td.Doc
				a.Prog.TypeSpans[a.Prog.IfaceCopyable] = td.Span
				a.finishBuiltinInterfaceDecl(mod, a.Prog.IfaceCopyable, td)
				continue
			}

			if langItem == "drop" || td.Name == "Drop" {
				if a.Prog.TypeSpans[a.Prog.IfaceDrop].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceDrop].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"drop\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceDrop].String())
					continue
				}
				a.Prog.IfaceDrop.Name = td.Name
				a.Prog.IfaceDrop.Doc = td.Doc
				a.Prog.TypeSpans[a.Prog.IfaceDrop] = td.Span
				a.finishBuiltinInterfaceDecl(mod, a.Prog.IfaceDrop, td)
				continue
			}

			if langItem == "hashable" || td.Name == "Hashable" {
				if a.Prog.IfaceHashable != nil && a.Prog.TypeSpans[a.Prog.IfaceHashable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceHashable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"hashable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceHashable].String())
					continue
				}
				if a.Prog.IfaceHashable == nil {
					a.Prog.IfaceHashable = &types.Interface{Name: td.Name, CName: "ll_iface_" + td.Name}
					a.Prog.Interfaces = append(a.Prog.Interfaces, a.Prog.IfaceHashable)
				} else {
					a.Prog.IfaceHashable.Name = td.Name
				}
				a.Prog.IfaceHashable.Doc = td.Doc
				a.Prog.TypeSpans[a.Prog.IfaceHashable] = td.Span
				a.finishBuiltinInterfaceDecl(mod, a.Prog.IfaceHashable, td)
				continue
			}

			if langItem == "equatable" || td.Name == "Equatable" {
				if a.Prog.TypeSpans[a.Prog.IfaceEquatable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceEquatable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"equatable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceEquatable].String())
					continue
				}
				a.Prog.IfaceEquatable.Name = td.Name
				a.Prog.IfaceEquatable.Doc = td.Doc
				a.Prog.TypeSpans[a.Prog.IfaceEquatable] = td.Span
				a.finishBuiltinInterfaceDecl(mod, a.Prog.IfaceEquatable, td)
				continue
			}

			if langItem == "comparable" || td.Name == "Comparable" {
				if a.Prog.TypeSpans[a.Prog.IfaceComparable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceComparable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"comparable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceComparable].String())
					continue
				}
				a.Prog.IfaceComparable.Name = td.Name
				a.Prog.IfaceComparable.Doc = td.Doc
				a.Prog.TypeSpans[a.Prog.IfaceComparable] = td.Span
				a.finishBuiltinInterfaceDecl(mod, a.Prog.IfaceComparable, td)
				continue
			}

			if langItem != "" && langItem != "collectable" && langItem != "iterable" && langItem != "iterator" && langItem != "addable" && langItem != "add_assignable" {
				a.Rep.Errorf(td.Span, "unknown interface lang item @lang(%q); supported interface lang items: \"printable\", \"copyable\", \"drop\", \"hashable\", \"equatable\", \"comparable\", \"addable\", \"add_assignable\", \"collectable\", \"iterable\", \"iterator\"", langItem)
				continue
			}

			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			cname := "ll_iface_" + td.Name
			if tag := a.typeTag(mod, td.Name); tag != "" {
				cname = fmt.Sprintf("ll_iface_%s_%s", tag, td.Name)
			}
			ifc := &types.Interface{
				Name:   td.Name,
				CName:  cname,
				PkgDir: mod.Dir,
				Doc:    td.Doc,
			}
			if langItem == "collectable" || td.Name == "Collectable" {
				a.Prog.IfaceCollectable = ifc
			} else if langItem == "iterable" || td.Name == "Iterable" {
				a.Prog.IfaceIterable = ifc
			} else if langItem == "iterator" || td.Name == "Iterator" {
				a.Prog.IfaceIterator = ifc
			} else if langItem == "containable" || td.Name == "Containable" {
				a.Prog.IfaceContainable = ifc
			} else if langItem == "addable" {
				a.Prog.IfaceAddable = ifc
			} else if langItem == "add_assignable" {
				a.Prog.IfaceAddAssignable = ifc
			}
			a.checkAttributes(td.Attributes, ifc, td.Span, "interface")
			if mod.IsTestFile {
				ifc.IsTestOnly = true
			}
			mod.Types[td.Name] = ifc
			a.Prog.TypeSpans[ifc] = td.Span
			a.Prog.InterfaceMods[ifc] = mod
			a.Prog.Interfaces = append(a.Prog.Interfaces, ifc)
			a.recordInterfaceDecl(ifc, td)

		case *ast.DistinctTypeDecl:
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				a.Rep.Errorf(langAttr.Span, "@lang attribute cannot be applied to a distinct type declaration")
			}
			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			cname := "ll_d_" + td.Name
			if tag := a.typeTag(mod, td.Name); tag != "" {
				cname = fmt.Sprintf("ll_d_%s_%s", tag, td.Name)
			}
			dt := &types.Distinct{
				Name:   td.Name,
				CName:  cname,
				PkgDir: mod.Dir,
				Doc:    td.Doc,
				Base:   types.TypeInvalid,
			}
			a.checkAttributes(td.Attributes, dt, td.Span, "distinct type")
			if mod.IsTestFile {
				dt.IsTestOnly = true
			}
			mod.Types[td.Name] = dt
			a.Prog.TypeSpans[dt] = td.Span
			a.Prog.DistinctMods[dt] = mod
			a.Prog.Distincts = append(a.Prog.Distincts, dt)
		}
	}
}

func (a *Analyzer) checkDupType(mod *Module, name string, span diag.Span) bool {
	if types.LookupPrimitive(name) != nil || name == "void" {
		a.Rep.Errorf(span, "cannot redefine built-in type %q", name)
		return true
	}
	if existing, ok := mod.Types[name]; ok && existing != nil {
		if name != "Printable" && name != "Copyable" && name != "Drop" && name != "Hashable" && name != "Equatable" && name != "Comparable" {
			if prevSpan, hasSpan := a.Prog.TypeSpans[existing]; hasSpan && prevSpan.Start.File != "" {
				a.Rep.Errorf(span, "duplicate type definition %q (previously defined at %s)", name, prevSpan.String())
			} else {
				a.Rep.Errorf(span, "duplicate type definition %q", name)
			}
			return true
		}
	}
	if gs, ok := mod.GenericStructs[name]; ok && gs != nil && gs.Decl != nil && gs.Decl.Span != span {
		a.Rep.Errorf(span, "duplicate type definition %q (previously defined at %s)", name, gs.Decl.Span.String())
		return true
	}
	if ge, ok := mod.GenericEnums[name]; ok && ge != nil && ge.Decl != nil && ge.Decl.Span != span {
		a.Rep.Errorf(span, "duplicate type definition %q (previously defined at %s)", name, ge.Decl.Span.String())
		return true
	}
	if gi, ok := mod.GenericInterfaces[name]; ok && gi != nil && gi.Decl != nil && gi.Decl.Span != span {
		a.Rep.Errorf(span, "duplicate type definition %q (previously defined at %s)", name, gi.Decl.Span.String())
		return true
	}
	if sym, ok := mod.Symbols[name]; ok && sym != nil && sym.Span != span {
		if sym.Span.Start.File != "" {
			a.Rep.Errorf(span, "duplicate declaration of %q (previously declared at %s)", name, sym.Span.String())
		} else {
			a.Rep.Errorf(span, "duplicate declaration of %q", name)
		}
		return true
	}
	return false
}

func (a *Analyzer) checkDupDecl(mod *Module, name string, span diag.Span) bool {
	if types.LookupPrimitive(name) != nil || name == "void" {
		a.Rep.Errorf(span, "cannot redefine built-in type %q", name)
		return true
	}
	if existing, ok := mod.Symbols[name]; ok && existing != nil && existing.Span != span {
		if existing.Span.Start.File != "" {
			a.Rep.Errorf(span, "duplicate declaration of %q (previously declared at %s)", name, existing.Span.String())
		} else {
			a.Rep.Errorf(span, "duplicate declaration of %q", name)
		}
		return true
	}
	if gf, ok := mod.GenericFuncs[name]; ok && gf != nil && gf.VarDecl != nil && gf.VarDecl.Span != span {
		if gf.VarDecl.Span.Start.File != "" {
			a.Rep.Errorf(span, "duplicate declaration of %q (previously declared at %s)", name, gf.VarDecl.Span.String())
		} else {
			a.Rep.Errorf(span, "duplicate declaration of %q", name)
		}
		return true
	}
	if ty, ok := mod.Types[name]; ok && ty != nil {
		if prevSpan, hasSpan := a.Prog.TypeSpans[ty]; hasSpan && prevSpan.Start.File != "" {
			a.Rep.Errorf(span, "duplicate declaration of %q (previously declared at %s)", name, prevSpan.String())
		} else {
			a.Rep.Errorf(span, "duplicate declaration of %q (previously declared as a type)", name)
		}
		return true
	}
	if gs, ok := mod.GenericStructs[name]; ok && gs != nil && gs.Decl != nil && gs.Decl.Span != span {
		if gs.Decl.Span.Start.File != "" {
			a.Rep.Errorf(span, "duplicate declaration of %q (previously declared at %s)", name, gs.Decl.Span.String())
		} else {
			a.Rep.Errorf(span, "duplicate declaration of %q", name)
		}
		return true
	}
	if ge, ok := mod.GenericEnums[name]; ok && ge != nil && ge.Decl != nil && ge.Decl.Span != span {
		if ge.Decl.Span.Start.File != "" {
			a.Rep.Errorf(span, "duplicate declaration of %q (previously declared at %s)", name, ge.Decl.Span.String())
		} else {
			a.Rep.Errorf(span, "duplicate declaration of %q", name)
		}
		return true
	}
	if gi, ok := mod.GenericInterfaces[name]; ok && gi != nil && gi.Decl != nil && gi.Decl.Span != span {
		if gi.Decl.Span.Start.File != "" {
			a.Rep.Errorf(span, "duplicate declaration of %q (previously declared at %s)", name, gi.Decl.Span.String())
		} else {
			a.Rep.Errorf(span, "duplicate declaration of %q", name)
		}
		return true
	}
	return false
}

func (a *Analyzer) resolveDistinctsAndEnums(mod *Module) {
	for _, d := range mod.ActiveDecls() {
		switch td := d.(type) {
		case *ast.DistinctTypeDecl:
			dt, ok := mod.Types[td.Name].(*types.Distinct)
			if !ok {
				continue
			}
			base := a.resolveValueType(mod, td.Base, nil, false)
			if base == types.TypeInvalid {
				continue
			}
			dt.Base = base

		case *ast.EnumDecl:
			if len(td.TypeParams) > 0 {
				continue
			}
			en, ok := mod.Types[td.Name].(*types.Enum)
			if !ok || en.IsTagged || len(en.Variants) > 0 {
				continue
			}
			if td.BaseType != nil {
				bt := a.resolveValueType(mod, td.BaseType, nil, false)
				prim, ok2 := bt.(*types.Primitive)
				if !ok2 || !prim.IsInteger() {
					a.Rep.Errorf(td.BaseType.GetSpan(), "enum underlying type must be an integer type (`u8`, `i32`, `int`, etc.), got %s", bt.String())
				} else {
					en.Underlying = prim
				}
			}
			seen := make(map[string]bool)
			var nextVal int64 = 0
			for _, v := range td.Variants {
				if seen[v.Name] {
					a.Rep.Errorf(v.Span, "duplicate variant %q in enum %s", v.Name, en.Name)
					continue
				}
				seen[v.Name] = true
				if v.Value != nil {
					cval, ok2 := a.evalConstInt(mod, v.Value)
					if !ok2 {
						a.Rep.Errorf(v.Value.GetSpan(), "enum discriminant must be a compile-time integer constant")
					} else {
						nextVal = cval
					}
				}
				if nextVal < 0 {
					if !en.Underlying.FitsUnsignedLiteral(uint64(-nextVal), true) {
						a.Rep.Errorf(v.Span, "enum discriminant %d overflows underlying type %s", nextVal, en.Underlying.String())
					}
				} else {
					if !en.Underlying.FitsUnsignedLiteral(uint64(nextVal), false) {
						a.Rep.Errorf(v.Span, "enum discriminant %d overflows underlying type %s", nextVal, en.Underlying.String())
					}
				}
				a.checkAttributes(v.Attributes, nil, v.Span, "enum variant")
				en.Variants = append(en.Variants, types.EnumVariant{
					Name:       v.Name,
					CName:      "v_" + types.SanitizeCIdent(v.Name),
					Value:      nextVal,
					Kind:       types.VariantUnit,
					Attributes: astAttrsToTypeAttrs(v.Attributes),
					Doc:        v.Doc,
				})
				nextVal++
			}
		}
	}
}

func (a *Analyzer) resolveInterfaces(mod *Module) {
	for _, d := range mod.ActiveDecls() {
		td, ok := d.(*ast.InterfaceDecl)
		if !ok {
			continue
		}
		if td.Name == "Printable" || td.Name == "Copyable" || td.Name == "Drop" {
			if ifc, ok := mod.Types[td.Name].(*types.Interface); ok {
				for _, m := range td.Methods {
					for idx := range ifc.Methods {
						if ifc.Methods[idx].Name == m.Name {
							ifc.Methods[idx].Doc = m.Doc
						}
					}
				}
			}
			continue
		}
		ifc, ok := mod.Types[td.Name].(*types.Interface)
		if !ok {
			continue
		}
		seen := make(map[string]bool)
		for _, m := range td.Methods {
			if seen[m.Name] {
				a.Rep.Errorf(m.Span, "duplicate method %q in interface %s", m.Name, td.Name)
				continue
			}
			seen[m.Name] = true
			// Built-in pre-populated interfaces (Equatable, Comparable, Hashable) already have methods registered; update Docs instead of duplicating.
			updatedExisting := false
			for idx := range ifc.Methods {
				if ifc.Methods[idx].Name == m.Name {
					ifc.Methods[idx].Doc = m.Doc
					updatedExisting = true
					break
				}
			}
			if updatedExisting {
				continue
			}
			var fparams []types.FuncParam
			for _, p := range m.Params {
				pmode, pty := a.resolveParamType(mod, p.Type, nil, true)
				if typeUsesSelf(pty) {
					ifc.UsesSelf = true
				}
				a.checkOptionalIdentRule(p.Name, p.IsOptName, pty, p.Span, "interface parameter")
				fparams = append(fparams, types.FuncParam{
					Name: p.Name,
					Mode: pmode,
					Type: pty,
				})
			}
			ret := types.Type(types.TypeVoid)
			if m.Ret != nil {
				ret = a.resolveReturnType(mod, m.Ret, nil, true)
				if typeUsesSelf(ret) {
					ifc.UsesSelf = true
				}
			}
			ifc.Methods = append(ifc.Methods, types.InterfaceMethod{
				Name:     m.Name,
				RecvMode: recvModeToParamMode(m.RecvMode),
				Params:   fparams,
				Ret:      ret,
				Doc:      m.Doc,
			})
		}
	}
}

func recvModeToParamMode(rm ast.ReceiverMode) types.ParamMode {
	switch rm {
	case ast.RecvMut:
		return types.ModeMut
	case ast.RecvOwn:
		return types.ModeOwn
	default:
		return types.ModeRead
	}
}

func (a *Analyzer) resolveStructFields(mod *Module) {
	for _, d := range mod.ActiveDecls() {
		switch td := d.(type) {
		case *ast.StructDecl:
			if len(td.TypeParams) > 0 {
				continue
			}
			st, ok := mod.Types[td.Name].(*types.Struct)
			if !ok || len(st.Fields) > 0 {
				continue
			}
			a.populateStructFields(mod, st, td, nil)
		case *ast.EnumDecl:
			if len(td.TypeParams) > 0 {
				continue
			}
			en, ok := mod.Types[td.Name].(*types.Enum)
			if !ok || !en.IsTagged {
				continue
			}
			a.populateEnumVariants(mod, en, td, nil)
		}
	}
}

func (a *Analyzer) populateEnumVariants(mod *Module, en *types.Enum, td *ast.EnumDecl, subst map[string]types.Type) {
	seen := make(map[string]bool)
	var tagVal int64 = 1 // 1-based tags for tagged unions so `0` is always the inert moved/dropped sentinel
	for _, v := range td.Variants {
		if seen[v.Name] {
			a.Rep.Errorf(v.Span, "duplicate variant %q in enum %s", v.Name, en.String())
			continue
		}
		seen[v.Name] = true
		vcname := "v_" + types.SanitizeCIdent(v.Name)
		a.checkAttributes(v.Attributes, nil, v.Span, "enum variant")
		if v.PayloadType == nil {
			en.Variants = append(en.Variants, types.EnumVariant{
				Name:       v.Name,
				CName:      vcname,
				Value:      tagVal,
				Kind:       types.VariantUnit,
				Attributes: astAttrsToTypeAttrs(v.Attributes),
				Doc:        v.Doc,
			})
			tagVal++
			continue
		}
		pty := a.resolveValueType(mod, v.PayloadType, subst, false)
		en.Variants = append(en.Variants, types.EnumVariant{
			Name:       v.Name,
			CName:      vcname,
			Value:      tagVal,
			Kind:       types.VariantTuple,
			Payload:    pty,
			Attributes: astAttrsToTypeAttrs(v.Attributes),
			Doc:        v.Doc,
		})
		tagVal++
	}
	if en.IsResult {
		a.checkResultDecl(en, td)
	}
}

// checkResultDecl validates a `define Name result [...]` declaration: exactly one `value` member
// (with the success payload, or none for a unit result), and at least one failure variant.
func (a *Analyzer) checkResultDecl(en *types.Enum, td *ast.EnumDecl) {
	var valueDecl *ast.EnumVariant
	for i := range td.Variants {
		if td.Variants[i].Name == types.ResultValueName {
			valueDecl = &td.Variants[i]
			break
		}
	}
	if valueDecl == nil {
		a.Rep.ErrorWithHint(td.Span,
			fmt.Sprintf("result %s has no `value` member", td.Name),
			"add the success member, e.g. `value: T,`")
		return
	}
	if len(td.Variants) < 2 {
		a.Rep.ErrorWithHint(td.Span,
			fmt.Sprintf("result %s has no failure variants", td.Name),
			"add at least one failure case, e.g. `Err: Error`")
	}
}

func (a *Analyzer) populateStructFields(mod *Module, st *types.Struct, td *ast.StructDecl, subst map[string]types.Type) {
	if a.populatedStructs == nil {
		a.populatedStructs = make(map[*types.Struct]bool)
	}
	if a.resolvingStructFields == nil {
		a.resolvingStructFields = make(map[*types.Struct]bool)
	}
	if a.populatedStructs[st] || a.resolvingStructFields[st] {
		return
	}
	a.resolvingStructFields[st] = true
	seen := make(map[string]bool)
	for _, f := range td.Fields {
		if seen[f.Name] {
			a.Rep.Errorf(f.Span, "duplicate field %q in struct %s", f.Name, st.String())
			continue
		}
		seen[f.Name] = true
		a.checkAttributes(f.Attributes, nil, f.Span, "struct field")
		fty := a.resolveValueType(mod, f.Type, subst, false)
		if !isDirectSubstTypeParam(f.Type, subst) {
			a.checkOptionalIdentRule(f.Name, f.IsOpt, fty, f.Span, "struct field")
		}
		cname := types.SanitizeCIdent(f.Name)
		if st.Name == "List" || strings.HasPrefix(st.Name, "List[") || st.Name == "Array" || strings.HasPrefix(st.Name, "Array[") {
			cname = f.Name
		}
		st.Fields = append(st.Fields, types.StructField{
			Name:       f.Name,
			CName:      cname,
			Type:       fty,
			HasDefault: f.Default != nil,
			Attributes: astAttrsToTypeAttrs(f.Attributes),
			Doc:        f.Doc,
		})
	}
	delete(a.resolvingStructFields, st)
	a.populatedStructs[st] = true
}

// checkOptionalIdentRule enforces Spec §7.1:
// An identifier has type `?T` IF AND ONLY IF its name ends with `?`.
func (a *Analyzer) checkOptionalIdentRule(name string, isOptName bool, ty types.Type, span diag.Span, kind string) {
	if name == "" || ty == nil || ty == types.TypeInvalid {
		return
	}
	// If `ty` is an uninstantiated generic TypeParamVar `T`, an identifier without `?` is valid (`first: A` where `A = ?int` is instantiated later or `A` is non-opt).
	if _, isTP := ty.(*types.TypeParamVar); isTP {
		return
	}
	a.checkResultIdentRule(name, ty, span, kind)
	_, isOptType := ty.(*types.Optional)
	if isOptType && !isOptName && name != "_" { // `let _ = xs.pop()` discards
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("%s %q has optional type %s and must end with `?`", kind, name, ty.String()),
			fmt.Sprintf("rename `%s` to `%s?` (all optional identifiers in Lowlight end in `?`)", name, name),
		)
	} else if !isOptType && isOptName {
		base := strings.TrimSuffix(name, "?")
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("%s %q ends with `?` but has non-optional type %s", kind, name, ty.String()),
			fmt.Sprintf("rename `%s` to `%s` or give it optional type `?%s`", name, base, ty.String()),
		)
	}
}

// checkResultIdentRule enforces the result naming invariant (mirroring `?` for optionals):
// an identifier holds a result type (one declared with `define Name result [...]`) IF AND ONLY IF
// its name ends with `!`.
func (a *Analyzer) checkResultIdentRule(name string, ty types.Type, span diag.Span, kind string) {
	if name == "" || name == "_" || ty == nil || ty == types.TypeInvalid {
		return
	}
	if _, isTP := ty.(*types.TypeParamVar); isTP {
		return
	}
	isResName := strings.HasSuffix(name, "!")
	isResType := types.AsResult(ty) != nil
	if isResType && !isResName {
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("%s %q has result type %s and must end with `!`", kind, name, ty.String()),
			fmt.Sprintf("rename `%s` to `%s!` (all result identifiers in Lowlight end in `!`)", name, name),
		)
	} else if !isResType && isResName {
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("%s %q ends with `!` but has non-result type %s", kind, name, ty.String()),
			fmt.Sprintf("rename `%s` to `%s`", name, strings.TrimSuffix(name, "!")),
		)
	}
}

// resolveParamType resolves a parameter TypeExpr, extracting `&T` (`ModeMut`) or `own T` (`ModeOwn`),
// while rejecting nested references like `&&T` or `&own T`.
func (a *Analyzer) resolveParamType(mod *Module, te ast.TypeExpr, subst map[string]types.Type, allowSelf bool) (types.ParamMode, types.Type) {
	switch v := te.(type) {
	case *ast.MutBorrowType:
		if _, bad := v.Elem.(*ast.MutBorrowType); bad {
			a.Rep.Errorf(v.Span, "nested reference types (`&&T`) do not exist in Lowlight")
			return types.ModeMut, types.TypeInvalid
		}
		if _, bad := v.Elem.(*ast.OwnType); bad {
			a.Rep.Errorf(v.Span, "cannot combine `&` and `own` on the same parameter")
			return types.ModeMut, types.TypeInvalid
		}
		inner := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		return types.ModeMut, inner
	case *ast.OwnType:
		if _, bad := v.Elem.(*ast.MutBorrowType); bad {
			a.Rep.Errorf(v.Span, "cannot combine `own` and `&` on the same parameter (`own &T` is invalid; borrows cannot be owned)")
			return types.ModeOwn, types.TypeInvalid
		}
		inner := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		return types.ModeOwn, inner
	default:
		return types.ModeRead, a.resolveValueType(mod, te, subst, allowSelf)
	}
}

// resolveReturnType resolves a function/method/interface return type, allowing surface `void`.
func (a *Analyzer) resolveReturnType(mod *Module, te ast.TypeExpr, subst map[string]types.Type, allowSelf bool) types.Type {
	if te == nil {
		return types.TypeVoid
	}
	if nt, ok := te.(*ast.NamedType); ok && nt.Module == "" && nt.Name == "void" && len(nt.TypeArgs) == 0 {
		return types.TypeVoid
	}
	return a.resolveValueType(mod, te, subst, allowSelf)
}

func isStructOrEnum(t types.Type) bool {
	switch t.(type) {
	case *types.Struct, *types.Enum:
		return true
	}
	return false
}

func derivePkgScopeID(m *Module) string {
	if m == nil {
		return ""
	}
	if m.Pkg != nil && m.Pkg.ID != "" {
		return m.Pkg.ID
	}
	if m.Dir != "" {
		return m.Dir
	}
	return m.Path
}

func (a *Analyzer) hasDeriveAccess(fn *FuncInstance, t types.Type) bool {
	if fn == nil || fn.DeriveTarget == nil || t == nil || !fn.DeriveTarget.Equals(t) {
		return false
	}
	if fn.DerivePkg == "" {
		return true
	}
	return derivePkgScopeID(fn.Mod) == fn.DerivePkg
}

func isRealStructType(st *types.Struct) bool {
	if st == nil {
		return false
	}
	switch st.Lang {
	case "list", "array", "set", "map", "range", "arena":
		return false
	}
	return true
}

func isFieldInspectableStruct(st *types.Struct) bool {
	return isRealStructType(st) || (st != nil && st.Lang == "range")
}

func (a *Analyzer) resolveQueryType(mod *Module, te ast.TypeExpr, subst map[string]types.Type) types.Type {
	return a.resolveValueTypeOpts(mod, te, subst, false, true)
}

// resolveValueType resolves a TypeExpr in an owned/value position (forbidding `&T` and `own T`).
func (a *Analyzer) resolveValueType(mod *Module, te ast.TypeExpr, subst map[string]types.Type, allowSelf bool) types.Type {
	return a.resolveValueTypeOpts(mod, te, subst, allowSelf, false)
}

func isDirectSubstTypeParam(te ast.TypeExpr, subst map[string]types.Type) bool {
	if len(subst) == 0 || te == nil {
		return false
	}
	for {
		switch v := te.(type) {
		case *ast.MutBorrowType:
			te = v.Elem
		case *ast.OwnType:
			te = v.Elem
		case *ast.NamedType:
			if v.Module == "" && len(v.TypeArgs) == 0 {
				_, ok := subst[v.Name]
				return ok
			}
			return false
		default:
			return false
		}
	}
}

func (a *Analyzer) finishBuiltinInterfaceDecl(mod *Module, ifc *types.Interface, td *ast.InterfaceDecl) {
	ifc.PkgDir = mod.Dir
	ifc.Attributes = astAttrsToTypeAttrs(td.Attributes)
	a.checkAttributes(td.Attributes, ifc, td.Span, "interface")
	mod.Types[td.Name] = ifc
	a.Prog.InterfaceMods[ifc] = mod
	a.recordInterfaceDecl(ifc, td)
}

func (a *Analyzer) resolveValueTypeOpts(mod *Module, te ast.TypeExpr, subst map[string]types.Type, allowSelf, allowSelfIface bool) types.Type {
	if te == nil {
		return types.TypeVoid
	}
	if mapped, ok := a.synthTypeExprs[te]; ok {
		return mapped
	}
	switch v := te.(type) {
	case *ast.MutBorrowType:
		a.Rep.ErrorWithHint(
			v.Span,
			"borrowed reference `&T` is second-class and cannot be stored in structs/collections or returned from functions",
			"store an owned value `T` (via `.copy()` or `own`), or use `Arena` `ref[T]` for region graphs",
		)
		return types.TypeInvalid

	case *ast.OwnType:
		a.Rep.Errorf(v.Span, "`own` is a parameter passing mode and is only valid on function/method parameters")
		return types.TypeInvalid

	case *ast.OptionalType:
		elem := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		if elem == types.TypeInvalid {
			return types.TypeInvalid
		}
		return types.NewOptional(elem)

	case *ast.VectorType:
		if v.Elem == nil {
			return types.TypeInvalid
		}
		elem := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		if elem == types.TypeInvalid {
			return types.TypeInvalid
		}
		return &types.Vector{Elem: elem}

	case *ast.ConstArgType:
		a.Rep.ErrorWithHint(
			v.Span,
			"expected a type argument, got constant value expression",
			"type parameters `T` expect a type (such as `int` or `string`); only `[N: const Type]` parameters accept constant values",
		)
		return types.TypeInvalid

	case *ast.StackArrayType:
		sz, ok := a.evalConstIntWithSubst(mod, v.Size, subst)
		if !ok || sz <= 0 {
			a.Rep.Errorf(v.Size.GetSpan(), "stack array size in `[N: T]` must be a positive compile-time integer constant")
			return types.TypeInvalid
		}
		elem := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		if elem == types.TypeInvalid {
			return types.TypeInvalid
		}
		return &types.StackArray{Size: sz, Elem: elem}

	case *ast.SetType:
		elem := a.resolveValueType(mod, v.Elem, subst, allowSelf)
		if elem == types.TypeInvalid {
			return types.TypeInvalid
		}
		if _, isTP := elem.(*types.TypeParamVar); !isTP {
			if !a.inBodyPass {
				if a.Prog.IfaceHashable != nil {
					a.deferredIfaceChecks = append(a.deferredIfaceChecks, deferredIfaceCheck{
						Concrete: elem,
						Iface:    a.Prog.IfaceHashable,
						Span:     v.Elem.GetSpan(),
						Context:  "set element type",
					})
				}
			} else {
				isH := (a.Prog.IfaceHashable != nil && a.ImplementsInterface(elem, a.Prog.IfaceHashable)) || types.IsHashable(elem)
				if !isH {
					a.Rep.Errorf(v.Elem.GetSpan(), "set element type %s is not hashable", elem.String())
					return types.TypeInvalid
				}
			}
		}
		return a.setType(elem, v.Elem.GetSpan())

	case *ast.MapType:
		kt := a.resolveValueType(mod, v.Key, subst, allowSelf)
		vt := a.resolveValueType(mod, v.Value, subst, allowSelf)
		if kt == types.TypeInvalid || vt == types.TypeInvalid {
			return types.TypeInvalid
		}
		if _, isTP := kt.(*types.TypeParamVar); !isTP {
			if !a.inBodyPass {
				if a.Prog.IfaceHashable != nil {
					a.deferredIfaceChecks = append(a.deferredIfaceChecks, deferredIfaceCheck{
						Concrete: kt,
						Iface:    a.Prog.IfaceHashable,
						Span:     v.Key.GetSpan(),
						Context:  "map key type",
					})
				}
			} else {
				isH := (a.Prog.IfaceHashable != nil && a.ImplementsInterface(kt, a.Prog.IfaceHashable)) || types.IsHashable(kt)
				if !isH {
					a.Rep.Errorf(v.Key.GetSpan(), "map key type %s is not hashable", kt.String())
					return types.TypeInvalid
				}
			}
		}
		return a.mapType(kt, vt, v.Key.GetSpan())

	case *ast.TupleType:
		var elems []types.Type
		for _, el := range v.Elems {
			t := a.resolveValueType(mod, el, subst, allowSelf)
			if t == types.TypeInvalid {
				return types.TypeInvalid
			}
			elems = append(elems, t)
		}
		return &types.Tuple{Elems: elems}

	case *ast.FuncType:
		var ps []types.FuncParam
		for _, p := range v.Params {
			pm, pt := a.resolveParamType(mod, p.Type, subst, allowSelf)
			if p.Name != "" {
				a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "function type parameter")
			}
			ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
		}
		ret := types.Type(types.TypeVoid)
		if v.Ret != nil {
			ret = a.resolveReturnType(mod, v.Ret, subst, allowSelf)
		}
		ft := &types.Func{Params: ps, Ret: ret, Closure: v.Closure, Extern: v.Extern}
		if v.Extern {
			a.checkCSignature(ft, v.Span)
		}
		return ft

	case *ast.NamedType:
		if v.Module == "" && v.Name == "<error>" {
			return types.TypeInvalid
		}
		if v.Module == "" && v.Name == "Self" {
			if allowSelf {
				return types.TypeSelf
			}
			a.Rep.Errorf(v.Span, "`Self` is only valid inside `interface` declarations")
			return types.TypeInvalid
		}

		// Built-in `ptr[T]` and `ref[T]`
		if v.Module == "" && v.Name == "ptr" {
			if len(v.TypeArgs) != 1 {
				a.Rep.Errorf(v.Span, "`ptr[T]` requires exactly 1 type argument")
				return types.TypeInvalid
			}
			el := a.resolveValueType(mod, v.TypeArgs[0], subst, allowSelf)
			if el == types.TypeInvalid {
				return types.TypeInvalid
			}
			return &types.Ptr{Elem: el}
		}
		if v.Module == "" && v.Name == "ref" {
			if len(v.TypeArgs) != 1 {
				a.Rep.Errorf(v.Span, "`ref[T]` requires exactly 1 type argument")
				return types.TypeInvalid
			}
			el := a.resolveValueType(mod, v.TypeArgs[0], subst, allowSelf)
			if el == types.TypeInvalid {
				return types.TypeInvalid
			}
			return &types.Ref{Elem: el}
		}

		// Check generic type parameter substitution first
		if v.Module == "" && subst != nil {
			if subTy, ok := subst[v.Name]; ok {
				if _, isCV := subTy.(*types.ConstValue); isCV {
					a.Rep.ErrorWithHint(
						v.Span,
						fmt.Sprintf("expected a type, got constant generic parameter %q", v.Name),
						fmt.Sprintf("%q is declared as a `const` value parameter, not a type parameter", v.Name),
					)
					return types.TypeInvalid
				}
				if len(v.TypeArgs) > 0 {
					a.Rep.Errorf(v.Span, "type parameter %s cannot take type arguments", v.Name)
					return types.TypeInvalid
				}
				return subTy
			}
		}

		// Array[N, T] syntactic sugar unification with [N: T]
		if (v.Name == "Array" && (v.Module == "" || v.Module == "core" || v.Module == "array")) && len(v.TypeArgs) == 2 {
			sz, ok := a.evalConstIntFromTypeExprWithSubst(mod, v.TypeArgs[0], subst)
			if !ok || sz <= 0 {
				a.Rep.Errorf(v.TypeArgs[0].GetSpan(), "array size in `Array[N, T]` must be a positive compile-time integer constant")
				return types.TypeInvalid
			}
			el := a.resolveValueType(mod, v.TypeArgs[1], subst, allowSelf)
			if el == types.TypeInvalid {
				return types.TypeInvalid
			}
			return &types.StackArray{Size: sz, Elem: el}
		}

		// Generic struct instantiation `Pair[int, string]` or `Matrix[3, 4]`
		if genSt := a.lookupGenStructInModule(mod, v.Module, v.Name); genSt != nil {
			targs, ok := a.resolveGenericArgs(mod, genSt.Mod, genSt.Decl.TypeParams, v.TypeArgs, subst, v.Span, "generic struct", v.Name)
			if !ok {
				return types.TypeInvalid
			}
			stInst := a.instantiateStruct(genSt, targs, v.Span)
			if stInst == nil {
				return types.TypeInvalid
			}
			return stInst
		}

		// Generic enum instantiation `Result[int, string]`
		if genEn := a.lookupGenEnumInModule(mod, v.Module, v.Name); genEn != nil {
			targs, ok := a.resolveGenericArgs(mod, genEn.Mod, genEn.Decl.TypeParams, v.TypeArgs, subst, v.Span, "generic enum", v.Name)
			if !ok {
				return types.TypeInvalid
			}
			enInst := a.instantiateEnum(genEn, targs, v.Span)
			if enInst == nil {
				return types.TypeInvalid
			}
			return enInst
		}

		// Generic interface instantiation `Iterator[int]`
		if genIfc := a.lookupGenInterfaceInModule(mod, v.Module, v.Name); genIfc != nil {
			targs, ok := a.resolveGenericArgs(mod, genIfc.Mod, genIfc.Decl.TypeParams, v.TypeArgs, subst, v.Span, "generic interface", v.Name)
			if !ok {
				return types.TypeInvalid
			}
			ifcInst := a.instantiateInterface(genIfc, targs, v.Span)
			if ifcInst == nil {
				return types.TypeInvalid
			}
			return ifcInst
		}

		if len(v.TypeArgs) > 0 {
			a.Rep.Errorf(v.Span, "type %s is not generic", v.Name)
			return types.TypeInvalid
		}

		// Check `void` in value position before looking up types in module
		if v.Module == "" && v.Name == "void" {
			a.Rep.ErrorWithHint(
				v.Span,
				"`void` is not a value type and can only be used as a function return type",
				"omit the type or use `void` only as a function return type",
			)
			return types.TypeInvalid
		}

		found := a.lookupTypeInModule(mod, v.Module, v.Name)
		if found == nil {
			a.Rep.Errorf(v.Span, "unknown type %q", v.Name)
			return types.TypeInvalid
		}
		if ifc, isIfc := found.(*types.Interface); isIfc && ifc.UsesSelf && !allowSelfIface {
			a.Rep.Errorf(v.Span, "interface %s uses `Self` and can only be used as a generic constraint (`[T: %s]`), not as a runtime value type", ifc.Name, ifc.Name)
			return types.TypeInvalid
		}
		return found
	}
	return types.TypeInvalid
}

// checkStructRecursion detects illegal infinite-size value cycles (`next: Node`, `BadEnum -> BadEnum`)
// and marks optional cycles (`next?: ?Node` or mutual `A -> ?B -> ?A`) with `BoxedOptional = true`.
func (a *Analyzer) checkStructRecursion() {
	for _, st := range a.Prog.Structs {
		a.checkSingleStructRecursion(st)
	}
	for _, en := range a.Prog.Enums {
		if en.IsTagged {
			a.checkSingleEnumRecursion(en)
		}
	}
}

func (a *Analyzer) checkSingleStructRecursion(root *types.Struct) {
	span := a.Prog.TypeSpans[root]
	// 1. Direct/mutual VALUE cycle (through Struct, tagged Enum, StackArray, or Distinct without `?`, `[]`, `ref`, `ptr`)
	if a.reachesStructByValue(root, root, make(map[types.Type]bool)) {
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("recursive struct %q contains itself by value (infinite size)", root.String()),
			fmt.Sprintf("use an optional field (`?%s`) for automatic heap boxing, `[%s]`, or `ref[%s]`", root.Name, root.Name, root.Name),
		)
		return
	}
	// 2. Direct/mutual OPTIONAL cycle -> auto-heap-box `?root`!
	if a.reachesStructViaOptional(root, root, false, make(map[string]bool)) {
		root.BoxedOptional = true
	}
}

func (a *Analyzer) checkSingleEnumRecursion(root *types.Enum) {
	if !root.IsTagged {
		return
	}
	span := a.Prog.TypeSpans[root]
	if a.reachesEnumByCValue(root, root, make(map[types.Type]bool)) {
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("recursive enum %q contains itself by value (infinite size)", root.String()),
			fmt.Sprintf("wrap recursive payloads in `ref[%s]` (with an `Arena`) or `[%s]`", root.String(), root.String()),
		)
	}
}

func (a *Analyzer) reachesStructByValue(curr, target *types.Struct, visited map[types.Type]bool) bool {
	if visited[curr] {
		return false
	}
	visited[curr] = true
	for _, f := range curr.Fields {
		if a.typeContainsStructByValue(f.Type, target, visited) {
			return true
		}
	}
	return false
}

func (a *Analyzer) typeContainsStructByValue(t types.Type, target *types.Struct, visited map[types.Type]bool) bool {
	switch v := t.(type) {
	case *types.Struct:
		if v == target {
			return true
		}
		return a.reachesStructByValue(v, target, visited)
	case *types.Enum:
		if !v.IsTagged || visited[v] {
			return false
		}
		visited[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil && a.typeContainsStructByValue(vr.Payload, target, visited) {
				return true
			}
		}
		return false
	case *types.StackArray:
		return a.typeContainsStructByValue(v.Elem, target, visited)
	case *types.Tuple:
		for _, el := range v.Elems {
			if a.typeContainsStructByValue(el, target, visited) {
				return true
			}
		}
		return false
	case *types.Distinct:
		return a.typeContainsStructByValue(v.Base, target, visited)
	default:
		return false
	}
}

func (a *Analyzer) reachesEnumByCValue(curr, target *types.Enum, visited map[types.Type]bool) bool {
	if visited[curr] {
		return false
	}
	visited[curr] = true
	for _, vr := range curr.Variants {
		if vr.Kind == types.VariantTuple && vr.Payload != nil && a.typeContainsEnumByCValue(vr.Payload, target, visited) {
			return true
		}
	}
	return false
}

func (a *Analyzer) typeContainsEnumByCValue(t types.Type, target *types.Enum, visited map[types.Type]bool) bool {
	switch v := t.(type) {
	case *types.Enum:
		if !v.IsTagged {
			return false
		}
		if v == target {
			return true
		}
		return a.reachesEnumByCValue(v, target, visited)
	case *types.Struct:
		if visited[v] {
			return false
		}
		visited[v] = true
		for _, f := range v.Fields {
			if a.typeContainsEnumByCValue(f.Type, target, visited) {
				return true
			}
		}
		return false
	case *types.Optional:
		if v.IsBoxed() {
			return false
		}
		return a.typeContainsEnumByCValue(v.Elem, target, visited)
	case *types.StackArray:
		return a.typeContainsEnumByCValue(v.Elem, target, visited)
	case *types.Tuple:
		for _, el := range v.Elems {
			if a.typeContainsEnumByCValue(el, target, visited) {
				return true
			}
		}
		return false
	case *types.Distinct:
		return a.typeContainsEnumByCValue(v.Base, target, visited)
	default:
		return false
	}
}

func (a *Analyzer) reachesStructViaOptional(curr, target *types.Struct, passedOpt bool, visited map[string]bool) bool {
	key := fmt.Sprintf("%p:%v", curr, passedOpt)
	if visited[key] {
		return false
	}
	visited[key] = true
	for _, f := range curr.Fields {
		if a.typeReachesViaOpt(f.Type, target, passedOpt, visited) {
			return true
		}
	}
	return false
}

func (a *Analyzer) typeReachesViaOpt(t types.Type, target *types.Struct, passedOpt bool, visited map[string]bool) bool {
	switch v := t.(type) {
	case *types.Optional:
		return a.typeReachesViaOpt(v.Elem, target, true, visited)
	case *types.Struct:
		if v == target && passedOpt {
			return true
		}
		if v.BoxedOptional && passedOpt {
			// Cycle already broken by an already-boxed struct along this path!
			return false
		}
		return a.reachesStructViaOptional(v, target, passedOpt, visited)
	case *types.Enum:
		if !v.IsTagged {
			return false
		}
		key := fmt.Sprintf("en:%p:%v", v, passedOpt)
		if visited[key] {
			return false
		}
		visited[key] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil && a.typeReachesViaOpt(vr.Payload, target, passedOpt, visited) {
				return true
			}
		}
		return false
	case *types.StackArray:
		return a.typeReachesViaOpt(v.Elem, target, passedOpt, visited)
	case *types.Tuple:
		for _, el := range v.Elems {
			if a.typeReachesViaOpt(el, target, passedOpt, visited) {
				return true
			}
		}
		return false
	case *types.Distinct:
		return a.typeReachesViaOpt(v.Base, target, passedOpt, visited)
	default:
		return false
	}
}

func (a *Analyzer) verifyNonOptionalTypeArg(tpName string, concrete types.Type, span diag.Span, contextName string) bool {
	if concrete == nil || concrete == types.TypeInvalid {
		return true
	}
	if _, isCV := concrete.(*types.ConstValue); isCV {
		return true
	}
	if opt, isOpt := concrete.(*types.Optional); isOpt {
		a.Rep.ErrorWithHint(
			span,
			fmt.Sprintf("generic type parameter %q in %s cannot be instantiated with optional type %s", tpName, contextName, concrete.String()),
			fmt.Sprintf("generic type parameters `T` always represent non-optional values; instantiate with `%s` or declare optional slots with `?%s`", opt.Elem.String(), tpName),
		)
		return false
	}
	return true
}

func (a *Analyzer) validateAllConstGenericParams() {
	for _, st := range a.Prog.GenStructs {
		a.validateConstTypeParamList(st.Mod, st.Decl.TypeParams)
	}
	for _, mod := range a.Prog.Modules {
		for _, en := range mod.GenericEnums {
			a.validateConstTypeParamList(en.Mod, en.Decl.TypeParams)
		}
		for _, gf := range mod.GenericFuncs {
			a.validateConstTypeParamList(gf.Mod, gf.VarDecl.TypeParams)
		}
	}
	for _, gi := range a.Prog.GenInterfaces {
		a.validateConstTypeParamList(gi.Mod, gi.Decl.TypeParams)
	}
	for _, mset := range a.genMethods {
		for _, gm := range mset {
			if gm != nil && gm.MethodDecl != nil {
				a.validateConstTypeParamList(gm.Mod, gm.MethodDecl.TypeParams)
			}
		}
	}
}

func (a *Analyzer) validateConstTypeParamList(declMod *Module, params []ast.TypeParam) {
	if len(params) == 0 {
		return
	}
	paramNames := make(map[string]bool, len(params))
	for _, p := range params {
		paramNames[p.Name] = true
	}
	for _, tp := range params {
		if !tp.IsConst {
			continue
		}
		_ = a.validateConstGenericParam(declMod, paramNames, tp)
	}
}

func (a *Analyzer) validateConstGenericParam(declMod *Module, otherTypeParams map[string]bool, tp ast.TypeParam) types.Type {
	if len(tp.Bounds) > 0 {
		a.Rep.Errorf(tp.Span, "const generic parameter %q cannot have interface bounds", tp.Name)
	}
	if tp.ConstType == nil {
		a.Rep.Errorf(tp.Span, "const generic parameter %q requires a type (`%s: const int`)", tp.Name, tp.Name)
		return types.TypeInvalid
	}
	if nt, ok := tp.ConstType.(*ast.NamedType); ok && nt.Module == "" && len(nt.TypeArgs) == 0 && otherTypeParams[nt.Name] {
		a.Rep.ErrorWithHint(
			tp.ConstType.GetSpan(),
			fmt.Sprintf("const generic parameter %q cannot use type parameter %q as its type", tp.Name, nt.Name),
			"allowed const parameter types: integer types (`int`, `i8`..`i64`, `u8`..`u64`), `bool`, `char`, tag `enum`s, and `string`",
		)
		return types.TypeInvalid
	}
	ty := a.resolveValueType(declMod, tp.ConstType, nil, false)
	if ty == types.TypeInvalid {
		return types.TypeInvalid
	}
	if !types.IsConstGenericParamType(ty) {
		a.Rep.ErrorWithHint(
			tp.ConstType.GetSpan(),
			fmt.Sprintf("const generic parameter %q cannot have type %s", tp.Name, ty.String()),
			"allowed const parameter types: integer types (`int`, `i8`..`i64`, `u8`..`u64`), `bool`, `char`, tag `enum`s, and `string`",
		)
		return types.TypeInvalid
	}
	return ty
}

func (a *Analyzer) resolveGenericArgs(
	callerMod *Module,
	declMod *Module,
	params []ast.TypeParam,
	argNodes []ast.TypeExpr,
	callerSubst map[string]types.Type,
	span diag.Span,
	kindName string,
	ownerName string,
) ([]types.Type, bool) {
	if len(argNodes) != len(params) {
		a.Rep.Errorf(span, "%s %s expects %d generic argument(s), got %d",
			kindName, ownerName, len(params), len(argNodes))
		return nil, false
	}
	otherParams := make(map[string]bool, len(params))
	for _, p := range params {
		if !p.IsConst {
			otherParams[p.Name] = true
		}
	}
	out := make([]types.Type, len(params))
	for i, tp := range params {
		if tp.IsConst {
			cv := a.resolveConstGenericArg(callerMod, declMod, otherParams, tp, argNodes[i], callerSubst, span)
			if cv == nil {
				return nil, false
			}
			out[i] = cv
		} else {
			ty := a.resolveValueType(callerMod, argNodes[i], callerSubst, false)
			if ty == types.TypeInvalid || !a.verifyNonOptionalTypeArg(tp.Name, ty, argNodes[i].GetSpan(), ownerName) {
				return nil, false
			}
			out[i] = ty
		}
	}
	return out, true
}

func (a *Analyzer) resolveConstGenericArg(
	callerMod *Module,
	declMod *Module,
	otherTypeParams map[string]bool,
	tp ast.TypeParam,
	argNode ast.TypeExpr,
	callerSubst map[string]types.Type,
	span diag.Span,
) *types.ConstValue {
	expectedTy := a.validateConstGenericParam(declMod, otherTypeParams, tp)
	if expectedTy == nil || expectedTy == types.TypeInvalid {
		return nil
	}
	argExpr, ok := typeExprToConstExpr(argNode)
	if !ok {
		a.Rep.ErrorWithHint(
			argNode.GetSpan(),
			fmt.Sprintf("expected compile-time constant expression of type %s for const parameter %q, got type", expectedTy.String(), tp.Name),
			"pass a constant value expression instead of a type",
		)
		return nil
	}
	// If NamedType names a concrete type (`int`, `Point`, etc.) rather than a constant symbol/param, report cleanly.
	if nt, isNt := argNode.(*ast.NamedType); isNt {
		isKnownConst := (nt.Module == "" && callerSubst != nil && callerSubst[nt.Name] != nil) ||
			a.lookupTopSymbol(callerMod, nt.Module, nt.Name) != nil
		if !isKnownConst && a.lookupTypeInModule(callerMod, nt.Module, nt.Name) != nil {
			a.Rep.ErrorWithHint(
				argNode.GetSpan(),
				fmt.Sprintf("expected compile-time constant value of type %s for const parameter %q, got type %s", expectedTy.String(), tp.Name, nt.Name),
				"pass a constant value rather than a type name",
			)
			return nil
		}
	}

	prevBM := a.curBM
	if prevBM == nil {
		a.curBM = a.Prog.TopBodyMaps
	}
	beforeErrs := a.errorCount()
	fc := a.newFuncChecker(callerMod, callerSubst)
	fc.comptimeExprDepth++
	fc.pushScope()
	fc.pushConstSubstSymbols()
	gotTy := fc.checkExprExpect(argExpr, expectedTy, true)
	if gotTy != types.TypeInvalid {
		fc.verifyAssignable(gotTy, expectedTy, argExpr, argExpr.GetSpan(), fmt.Sprintf("const generic argument %q", tp.Name))
	}
	fc.popScope()
	if gotTy == types.TypeInvalid || a.errorCount() > beforeErrs {
		a.curBM = prevBM
		return nil
	}
	st := newCtState(a, callerMod, 0)
	st.frames = []ctFrame{{
		mod:      callerMod,
		bm:       a.curBM,
		callSpan: argExpr.GetSpan(),
		locals:   make(map[*Symbol]*ctSlot),
		alive:    newFrameAlive(),
	}}
	val, sig := st.evalExpr(argExpr)
	a.curBM = prevBM
	if sig.kind == sigAbort {
		st.reportAbort(sig, argExpr.GetSpan())
		return nil
	}
	if sig.kind != sigNone || val == nil {
		a.Rep.Errorf(argExpr.GetSpan(), "const generic argument %q failed to evaluate at compile time", tp.Name)
		return nil
	}
	cv := ctValToConstValue(val, expectedTy)
	if cv == nil {
		a.Rep.Errorf(argExpr.GetSpan(), "expected compile-time constant of type %s for const parameter %q", expectedTy.String(), tp.Name)
		return nil
	}
	return cv
}

func typeExprToConstExpr(te ast.TypeExpr) (ast.Expr, bool) {
	if ca, ok := te.(*ast.ConstArgType); ok && ca.Expr != nil {
		return ca.Expr, true
	}
	nt, ok := te.(*ast.NamedType)
	if !ok || len(nt.TypeArgs) > 0 {
		return nil, false
	}
	if nt.Module == "" {
		return &ast.IdentExpr{Name: nt.Name, Span: nt.Span}, true
	}
	return &ast.MemberExpr{
		Base:   &ast.IdentExpr{Name: nt.Module, Span: nt.Span},
		Member: nt.Name,
		Span:   nt.Span,
	}, true
}

// instantiateStruct monomorphizes `define Name[T...] struct [...]` for concrete `typeArgs`.
func (a *Analyzer) instantiateStruct(tmpl *GenericStructTemplate, typeArgs []types.Type, span diag.Span) *types.Struct {
	if a.monoDepth >= MaxMonomorphDepth {
		a.Rep.Errorf(span, "generic instantiation recursion depth limit (%d) exceeded while instantiating %s", MaxMonomorphDepth, tmpl.Decl.Name)
		return nil
	}
	a.monoDepth++
	defer func() { a.monoDepth-- }()

	for i, tp := range tmpl.Decl.TypeParams {
		if i < len(typeArgs) && !a.verifyNonOptionalTypeArg(tp.Name, typeArgs[i], span, tmpl.Decl.Name) {
			return nil
		}
	}
	if tmpl == a.Prog.GenStructRange && len(typeArgs) == 1 {
		ta := typeArgs[0]
		if _, isTP := ta.(*types.TypeParamVar); !isTP && ta != types.TypeInvalid {
			prim := types.UnderlyingPrimitive(ta)
			if prim == nil || (!prim.IsInteger() && prim.Kind != types.PrimChar) {
				a.Rep.Errorf(span, "`Range[T]` requires an integer or `char` element type, got %s", ta.String())
				return nil
			}
		}
	}

	// If any typeArg is still an uninstantiated TypeParamVar, create an uncached symbolic Struct for template checking.
	hasTypeParam := false
	parts := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		parts[i] = ta.Mangled()
		if _, ok := ta.(*types.TypeParamVar); ok {
			hasTypeParam = true
		}
	}
	key := strings.Join(parts, "__")
	if !hasTypeParam {
		if inst, ok := tmpl.Instances[key]; ok {
			return inst
		}
		if len(tmpl.Instances) >= MaxInstantiationsPerTemplate {
			a.Rep.Errorf(span, "generic struct %s instantiation limit (%d) exceeded", tmpl.Decl.Name, MaxInstantiationsPerTemplate)
			return nil
		}
	}

	// Verify interface bounds when concrete!
	subst := make(map[string]types.Type, len(typeArgs))
	for i, tp := range tmpl.Decl.TypeParams {
		subst[tp.Name] = typeArgs[i]
	}

	cname := fmt.Sprintf("ll_gs_%s__%s", tmpl.Decl.Name, key)
	if tag := a.typeTag(tmpl.Mod, tmpl.Decl.Name); tag != "" {
		cname = fmt.Sprintf("ll_gs_%s_%s__%s", tag, tmpl.Decl.Name, key)
	}
	inst := &types.Struct{
		Name:     tmpl.Decl.Name,
		CName:    cname,
		PkgDir:   tmpl.Mod.Dir,
		Packed:   tmpl.Decl.Packed || ast.FindAttribute(tmpl.Decl.Attributes, "packed") != nil,
		Doc:      tmpl.Decl.Doc,
		TypeArgs: typeArgs,
	}
	for _, ta := range typeArgs {
		if types.FindComptimeOnlyType(ta) != nil {
			inst.IsComptimeOnly = true
			break
		}
	}
	switch tmpl {
	case a.Prog.GenStructList:
		inst.Lang = "list"
	case a.Prog.GenStructArray:
		inst.Lang = "array"
	case a.Prog.GenStructSet:
		inst.Lang = "set"
	case a.Prog.GenStructMap:
		inst.Lang = "map"
	case a.Prog.GenStructRange:
		inst.Lang = "range"
	}
	a.checkAttributes(tmpl.Decl.Attributes, inst, tmpl.Decl.Span, "struct")
	if !hasTypeParam {
		tmpl.Instances[key] = inst
	}

	a.populateStructFields(tmpl.Mod, inst, tmpl.Decl, subst)
	if !hasTypeParam {
		a.Prog.StructDecls[inst] = tmpl.Decl
		a.Prog.StructMods[inst] = tmpl.Mod
		a.Prog.TypeSpans[inst] = tmpl.Decl.Span
		a.Prog.Structs = append(a.Prog.Structs, inst)
		a.checkSingleStructRecursion(inst)
		// Check bounds after registration so methods are available if called during body checking
		for i, tp := range tmpl.Decl.TypeParams {
			for _, bName := range tp.Bounds {
				bTy := a.lookupTypeInModule(tmpl.Mod, "", bName)
				ifc, ok := bTy.(*types.Interface)
				if !ok {
					a.Rep.Errorf(tp.Span, "bound %q is not an interface", bName)
					continue
				}
				if !a.inBodyPass {
					a.deferredIfaceChecks = append(a.deferredIfaceChecks, deferredIfaceCheck{
						Concrete: typeArgs[i],
						Iface:    ifc,
						Span:     span,
						Context:  fmt.Sprintf("required by %s", tmpl.Decl.Name),
					})
				} else if !a.ImplementsInterface(typeArgs[i], ifc) {
					a.Rep.Errorf(span, "type %s does not satisfy interface %s required by %s", typeArgs[i].String(), ifc.Name, tmpl.Decl.Name)
				}
			}
		}
		// Check if generic Drop method exists on this template
		if gm, ok := a.genMethods[tmpl.Decl.Name]["drop"]; ok {
			_ = a.instantiateMethod(gm, inst, nil, span)
			inst.HasCustomDrop = true
		} else if a.genDropRecv[tmpl.Decl.Name] {
			inst.HasCustomDrop = true
			a.pendingDrops = append(a.pendingDrops, inst)
		}
	}
	return inst
}

func typeListString(tys []types.Type) string {
	strs := make([]string, len(tys))
	for i, t := range tys {
		if t != nil {
			strs[i] = t.String()
		}
	}
	return strings.Join(strs, ", ")
}

// instantiateInterface monomorphizes `define Name[T...] interface [...]` for concrete `typeArgs`.
func (a *Analyzer) instantiateInterface(tmpl *GenericInterfaceTemplate, typeArgs []types.Type, span diag.Span) *types.Interface {
	if a.monoDepth >= MaxMonomorphDepth {
		a.Rep.Errorf(span, "generic instantiation recursion depth limit (%d) exceeded while instantiating %s", MaxMonomorphDepth, tmpl.Decl.Name)
		return nil
	}
	a.monoDepth++
	defer func() { a.monoDepth-- }()

	for i, tp := range tmpl.Decl.TypeParams {
		if i < len(typeArgs) && !a.verifyNonOptionalTypeArg(tp.Name, typeArgs[i], span, tmpl.Decl.Name) {
			return nil
		}
	}

	hasTypeParam := false
	parts := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		parts[i] = ta.Mangled()
		if _, ok := ta.(*types.TypeParamVar); ok {
			hasTypeParam = true
		}
	}
	key := strings.Join(parts, "__")
	if !hasTypeParam {
		if inst, ok := tmpl.Instances[key]; ok {
			return inst
		}
		if len(tmpl.Instances) >= MaxInstantiationsPerTemplate {
			a.Rep.Errorf(span, "generic interface %s instantiation limit (%d) exceeded", tmpl.Decl.Name, MaxInstantiationsPerTemplate)
			return nil
		}
	}

	subst := make(map[string]types.Type, len(typeArgs))
	for i, tp := range tmpl.Decl.TypeParams {
		subst[tp.Name] = typeArgs[i]
	}

	humanKey := types.SanitizeCIdent(key)
	ifaceHash := fnv64aHex(strings.Join(parts, "\x00"))
	cname := fmt.Sprintf("ll_giface_%s_%s_%s", types.SanitizeCIdent(tmpl.Decl.Name), humanKey, ifaceHash)
	if tag := a.typeTag(tmpl.Mod, tmpl.Decl.Name); tag != "" {
		cname = fmt.Sprintf("ll_giface_%s_%s_%s_%s", tag, types.SanitizeCIdent(tmpl.Decl.Name), humanKey, ifaceHash)
	}
	inst := &types.Interface{
		Name:   fmt.Sprintf("%s[%s]", tmpl.Decl.Name, typeListString(typeArgs)),
		CName:  cname,
		PkgDir: tmpl.Mod.Dir,
		Doc:    tmpl.Decl.Doc,
	}
	a.checkAttributes(tmpl.Decl.Attributes, inst, tmpl.Decl.Span, "interface")
	a.Prog.TypeSpans[inst] = tmpl.Decl.Span
	a.Prog.InterfaceMods[inst] = tmpl.Mod
	if !hasTypeParam {
		tmpl.Instances[key] = inst
		a.Prog.Interfaces = append(a.Prog.Interfaces, inst)
	}

	for _, m := range tmpl.Decl.Methods {
		var fparams []types.FuncParam
		for _, p := range m.Params {
			pmode, pty := a.resolveParamType(tmpl.Mod, p.Type, subst, true)
			if typeUsesSelf(pty) {
				inst.UsesSelf = true
			}
			fparams = append(fparams, types.FuncParam{
				Name: p.Name,
				Mode: pmode,
				Type: pty,
			})
		}
		ret := types.Type(types.TypeVoid)
		if m.Ret != nil {
			ret = a.resolveReturnType(tmpl.Mod, m.Ret, subst, true)
			if typeUsesSelf(ret) {
				inst.UsesSelf = true
			}
		}
		inst.Methods = append(inst.Methods, types.InterfaceMethod{
			Name:     m.Name,
			RecvMode: recvModeToParamMode(m.RecvMode),
			Params:   fparams,
			Ret:      ret,
			Doc:      m.Doc,
		})
	}
	return inst
}

func (a *Analyzer) isCollectableType(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	switch v := types.CollView(t).(type) {
	case *types.Vector:
		return true
	case *types.Set:
		return true
	case *types.Map:
		return true
	case *types.Struct:
		if fi := a.LookupMethod(v, "push", nil, diag.Span{}); fi != nil {
			return len(fi.Sig.Params) == 2 && fi.Sig.Params[0].Mode == types.ModeMut
		}
		return false
	case *types.Interface:
		if a.Prog.GenIfaceCollectable != nil && strings.HasPrefix(v.Name, a.Prog.GenIfaceCollectable.Decl.Name) {
			return true
		}
		for _, m := range v.Methods {
			if m.Name == "push" && m.RecvMode == types.ModeMut && len(m.Params) == 1 {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) collectableItemType(t types.Type) types.Type {
	if t == nil || t == types.TypeInvalid {
		return types.TypeInvalid
	}
	switch v := types.CollView(t).(type) {
	case *types.Vector:
		return v.Elem
	case *types.Set:
		return v.Elem
	case *types.Map:
		stTmpl := a.lookupGenStructInModule(a.Prog.EntryMod, "", "MapEntry")
		if stTmpl == nil && a.coreMod != nil {
			stTmpl = a.coreMod.GenericStructs["MapEntry"]
		}
		if stTmpl == nil {
			for _, m := range a.Prog.Modules {
				if stTmpl = a.lookupGenStructInModule(m, "", "MapEntry"); stTmpl != nil {
					break
				}
			}
		}
		if stTmpl != nil {
			entry := a.instantiateStruct(stTmpl, []types.Type{v.Key, v.Value}, diag.Span{})
			if entry != nil {
				return entry
			}
		}
		return types.TypeInvalid
	case *types.Struct:
		if fi := a.LookupMethod(v, "push", nil, diag.Span{}); fi != nil && len(fi.Sig.Params) == 2 {
			return fi.Sig.Params[1].Type
		}
		return types.TypeInvalid
	case *types.Interface:
		for _, m := range v.Methods {
			if m.Name == "push" && len(m.Params) == 1 {
				return m.Params[0].Type
			}
		}
		return types.TypeInvalid
	default:
		return types.TypeInvalid
	}
}

func (a *Analyzer) isIterableType(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	switch c := types.CollView(t).(type) {
	case *types.Vector, *types.StackArray, *types.Set, *types.Map:
		return true
	case *types.Primitive:
		return c.Kind == types.PrimString
	case *types.Distinct:
		return a.isIterableType(c.Base)
	case *types.Struct:
		if a.isStdPoolStruct(c) && !a.supportsCopy(c.TypeArgs[0]) {
			return false
		}
		if fi := a.LookupMethod(c, "iter", nil, diag.Span{}); fi != nil {
			return true
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) iterableItemType(t types.Type) types.Type {
	if t == nil || t == types.TypeInvalid {
		return types.TypeInvalid
	}
	switch c := types.CollView(t).(type) {
	case *types.Vector:
		return c.Elem
	case *types.StackArray:
		return c.Elem
	case *types.Set:
		return c.Elem
	case *types.Primitive:
		if c.Kind == types.PrimString {
			return types.TypeChar
		}
		return types.TypeInvalid
	case *types.Map:
		return a.collectableItemType(c)
	case *types.Distinct:
		return a.iterableItemType(c.Base)
	case *types.Struct:
		if a.isStdPoolStruct(c) && !a.supportsCopy(c.TypeArgs[0]) {
			return types.TypeInvalid
		}
		if fi := a.LookupMethod(c, "iter", nil, diag.Span{}); fi != nil {
			iterRet := fi.Sig.Ret
			if fi.RetInferred {
				iterRet = a.ensureFuncRetResolved(fi, fi.DeclSpan)
			}
			if iterRet != nil {
				if nextM := a.LookupMethod(iterRet, "next", nil, diag.Span{}); nextM != nil {
					nextRet := nextM.Sig.Ret
					if nextM.RetInferred {
						nextRet = a.ensureFuncRetResolved(nextM, nextM.DeclSpan)
					}
					if nextRet != nil {
						if opt, isOpt := nextRet.(*types.Optional); isOpt {
							return opt.Elem
						}
						return nextRet
					}
				}
			}
		}
		return types.TypeInvalid
	default:
		return types.TypeInvalid
	}
}

func (a *Analyzer) isIndexableType(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	switch v := t.(type) {
	case *types.Vector, *types.StackArray:
		return true
	case *types.Primitive:
		return v.Kind == types.PrimString
	case *types.Struct:
		if a.isStdPoolStruct(v) {
			return false
		}
		if (v.Name == "List" || strings.HasPrefix(v.Name, "List[")) ||
			(v.Name == "Array" || strings.HasPrefix(v.Name, "Array[")) {
			return true
		}
		fiGet := a.LookupMethod(v, "get", nil, diag.Span{})
		fiLen := a.LookupMethod(v, "len", nil, diag.Span{})
		if fiGet != nil && fiLen != nil && len(fiGet.Sig.Params) == 2 {
			return fiGet.Sig.Params[1].Type.Equals(types.TypeInt)
		}
		return false
	case *types.Distinct:
		fiGet := a.LookupMethod(v, "get", nil, diag.Span{})
		fiLen := a.LookupMethod(v, "len", nil, diag.Span{})
		if fiGet != nil && fiLen != nil && len(fiGet.Sig.Params) == 2 {
			return fiGet.Sig.Params[1].Type.Equals(types.TypeInt)
		}
		return false
	case *types.Interface:
		if a.Prog.GenIfaceIndexable != nil && strings.HasPrefix(v.Name, a.Prog.GenIfaceIndexable.Decl.Name) {
			return true
		}
		hasGet := false
		hasLen := false
		for _, m := range v.Methods {
			if m.Name == "get" && len(m.Params) == 1 && m.Params[0].Type.Equals(types.TypeInt) {
				hasGet = true
			}
			if m.Name == "len" && len(m.Params) == 0 && m.Ret.Equals(types.TypeInt) {
				hasLen = true
			}
		}
		return hasGet && hasLen
	case *types.TypeParamVar:
		for _, b := range v.Bounds {
			if a.isIndexableType(b) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) indexableItemType(t types.Type) types.Type {
	if t == nil || t == types.TypeInvalid {
		return types.TypeInvalid
	}
	switch v := t.(type) {
	case *types.Vector:
		return v.Elem
	case *types.StackArray:
		return v.Elem
	case *types.Primitive:
		if v.Kind == types.PrimString {
			return types.TypeU8
		}
		return types.TypeInvalid
	case *types.Struct:
		if (v.Name == "List" || strings.HasPrefix(v.Name, "List[")) && len(v.TypeArgs) >= 1 {
			return v.TypeArgs[0]
		}
		if (v.Name == "Array" || strings.HasPrefix(v.Name, "Array[")) && len(v.TypeArgs) >= 1 {
			return v.TypeArgs[len(v.TypeArgs)-1]
		}
		if fi := a.LookupMethod(v, "get", nil, diag.Span{}); fi != nil {
			if opt, ok := a.ensureFuncRetResolved(fi, fi.DeclSpan).(*types.Optional); ok {
				return opt.Elem
			}
			return fi.Sig.Ret
		}
		return types.TypeInvalid
	case *types.Distinct:
		if fi := a.LookupMethod(v, "get", nil, diag.Span{}); fi != nil {
			if opt, ok := a.ensureFuncRetResolved(fi, fi.DeclSpan).(*types.Optional); ok {
				return opt.Elem
			}
			return fi.Sig.Ret
		}
		return types.TypeInvalid
	case *types.Interface:
		for _, m := range v.Methods {
			if m.Name == "get" {
				if opt, ok := m.Ret.(*types.Optional); ok {
					return opt.Elem
				}
				return m.Ret
			}
		}
		return types.TypeInvalid
	case *types.TypeParamVar:
		for _, b := range v.Bounds {
			if it := a.indexableItemType(b); it != types.TypeInvalid {
				return it
			}
		}
		return types.TypeInvalid
	default:
		return types.TypeInvalid
	}
}

func (a *Analyzer) isKeyIndexableType(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	switch v := types.CollView(t).(type) {
	case *types.Map:
		return true
	case *types.Struct:
		if a.isStdPoolStruct(v) {
			return a.supportsCopy(v.TypeArgs[0])
		}
		if v.Name == "Map" || strings.HasPrefix(v.Name, "Map[") {
			return true
		}
		fiHas := a.LookupMethod(v, "has", nil, diag.Span{})
		fiGet := a.LookupMethod(v, "get", nil, diag.Span{})
		fiLen := a.LookupMethod(v, "len", nil, diag.Span{})
		return fiHas != nil && fiGet != nil && fiLen != nil
	case *types.Distinct:
		fiHas := a.LookupMethod(v, "has", nil, diag.Span{})
		fiGet := a.LookupMethod(v, "get", nil, diag.Span{})
		fiLen := a.LookupMethod(v, "len", nil, diag.Span{})
		return fiHas != nil && fiGet != nil && fiLen != nil
	case *types.Interface:
		if a.Prog.GenIfaceKeyIndexable != nil && strings.HasPrefix(v.Name, a.Prog.GenIfaceKeyIndexable.Decl.Name) {
			return true
		}
		hasGet := false
		hasLen := false
		hasHas := false
		for _, m := range v.Methods {
			if m.Name == "get" && len(m.Params) == 1 {
				hasGet = true
			}
			if m.Name == "len" && len(m.Params) == 0 && m.Ret.Equals(types.TypeInt) {
				hasLen = true
			}
			if m.Name == "has" && len(m.Params) == 1 && m.Ret.Equals(types.TypeBool) {
				hasHas = true
			}
		}
		return hasGet && hasLen && hasHas
	case *types.TypeParamVar:
		for _, b := range v.Bounds {
			if a.isKeyIndexableType(b) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) keyIndexableTypes(t types.Type) (types.Type, types.Type) {
	if t == nil || t == types.TypeInvalid {
		return types.TypeInvalid, types.TypeInvalid
	}
	switch v := types.CollView(t).(type) {
	case *types.Map:
		return v.Key, v.Value
	case *types.Struct:
		if fi := a.LookupMethod(v, "get", nil, diag.Span{}); fi != nil && len(fi.Sig.Params) == 2 {
			k := fi.Sig.Params[1].Type
			r := a.ensureFuncRetResolved(fi, fi.DeclSpan)
			if opt, ok := r.(*types.Optional); ok {
				r = opt.Elem
			}
			return k, r
		}
		return types.TypeInvalid, types.TypeInvalid
	case *types.Distinct:
		if fi := a.LookupMethod(v, "get", nil, diag.Span{}); fi != nil && len(fi.Sig.Params) == 2 {
			k := fi.Sig.Params[1].Type
			r := a.ensureFuncRetResolved(fi, fi.DeclSpan)
			if opt, ok := r.(*types.Optional); ok {
				r = opt.Elem
			}
			return k, r
		}
		return types.TypeInvalid, types.TypeInvalid
	case *types.Interface:
		for _, m := range v.Methods {
			if m.Name == "get" && len(m.Params) == 1 {
				k := m.Params[0].Type
				r := m.Ret
				if opt, ok := r.(*types.Optional); ok {
					r = opt.Elem
				}
				return k, r
			}
		}
		return types.TypeInvalid, types.TypeInvalid
	case *types.TypeParamVar:
		for _, b := range v.Bounds {
			if k, val := a.keyIndexableTypes(b); k != types.TypeInvalid {
				return k, val
			}
		}
		return types.TypeInvalid, types.TypeInvalid
	default:
		return types.TypeInvalid, types.TypeInvalid
	}
}

func (a *Analyzer) isStdStructTemplate(st *types.Struct, tmpl *GenericStructTemplate) bool {
	if st == nil || tmpl == nil || tmpl.Decl == nil {
		return false
	}
	return a.Prog.StructMods[st] == tmpl.Mod && (st.Name == tmpl.Decl.Name || strings.HasPrefix(st.Name, tmpl.Decl.Name+"["))
}

func (a *Analyzer) isStdPoolStruct(st *types.Struct) bool {
	if st == nil || a.Prog == nil || len(st.TypeArgs) != 1 {
		return false
	}
	if st.Name != "Pool" && !strings.HasPrefix(st.Name, "Pool[") {
		return false
	}
	mod := a.Prog.StructMods[st]
	return mod != nil && mod.Pkg != nil && mod.Pkg.ID == "std/pool"
}

func (a *Analyzer) isContainableType(t types.Type) bool {
	item := a.containableItemType(t)
	return item != nil && item != types.TypeInvalid
}

func (a *Analyzer) containsMethodItemType(t types.Type) types.Type {
	if fi := a.LookupMethod(t, "contains", nil, diag.Span{}); fi != nil && !fi.IsStaticAssoc &&
		len(fi.Sig.Params) == 2 && fi.Sig.Params[0].Mode == types.ModeRead && fi.Sig.Params[1].Mode == types.ModeRead {
		ret := a.ensureFuncRetResolved(fi, fi.DeclSpan)
		if ret != nil && ret.Equals(types.TypeBool) {
			return fi.Sig.Params[1].Type
		}
	}
	return types.TypeInvalid
}

func (a *Analyzer) containableItemType(t types.Type) types.Type {
	if t == nil || t == types.TypeInvalid {
		return types.TypeInvalid
	}
	switch v := types.CollView(t).(type) {
	case *types.Vector:
		if !a.supportsEq(v.Elem) {
			return types.TypeInvalid
		}
		return v.Elem
	case *types.StackArray:
		if !a.supportsEq(v.Elem) {
			return types.TypeInvalid
		}
		return v.Elem
	case *types.Set:
		return v.Elem
	case *types.Map:
		return v.Key
	case *types.Primitive:
		if v.Kind == types.PrimString {
			return types.TypeString
		}
		return types.TypeInvalid
	case *types.Struct:
		if a.isStdStructTemplate(v, a.Prog.GenStructList) && len(v.TypeArgs) == 1 {
			if !a.supportsEq(v.TypeArgs[0]) {
				return types.TypeInvalid
			}
			return v.TypeArgs[0]
		}
		if a.isStdStructTemplate(v, a.Prog.GenStructArray) && len(v.TypeArgs) == 1 {
			if !a.supportsEq(v.TypeArgs[0]) {
				return types.TypeInvalid
			}
			return v.TypeArgs[0]
		}
		if a.isStdStructTemplate(v, a.Prog.GenStructSet) && len(v.TypeArgs) == 1 {
			return v.TypeArgs[0]
		}
		if a.isStdStructTemplate(v, a.Prog.GenStructMap) && len(v.TypeArgs) == 2 {
			return v.TypeArgs[0]
		}
		if a.isStdStructTemplate(v, a.Prog.GenStructRange) && len(v.TypeArgs) == 1 {
			return v.TypeArgs[0]
		}
		return a.containsMethodItemType(v)
	case *types.Enum:
		return a.containsMethodItemType(v)
	case *types.Distinct:
		return a.containsMethodItemType(v)
	case *types.Interface:
		for _, m := range v.Methods {
			if m.Name == "contains" && m.RecvMode == types.ModeRead &&
				len(m.Params) == 1 && m.Params[0].Mode == types.ModeRead && m.Ret.Equals(types.TypeBool) {
				return m.Params[0].Type
			}
		}
		return types.TypeInvalid
	case *types.TypeParamVar:
		for _, b := range v.Bounds {
			if it := a.containableItemType(b); it != types.TypeInvalid {
				return it
			}
		}
		return types.TypeInvalid
	default:
		return types.TypeInvalid
	}
}

// instantiateEnum monomorphizes `define Name[T...] enum [...]` for concrete `typeArgs`.
func (a *Analyzer) instantiateEnum(tmpl *GenericEnumTemplate, typeArgs []types.Type, span diag.Span) *types.Enum {
	if a.monoDepth >= MaxMonomorphDepth {
		a.Rep.Errorf(span, "generic instantiation recursion depth limit (%d) exceeded while instantiating %s", MaxMonomorphDepth, tmpl.Decl.Name)
		return nil
	}
	a.monoDepth++
	defer func() { a.monoDepth-- }()

	for i, tp := range tmpl.Decl.TypeParams {
		if i < len(typeArgs) && !a.verifyNonOptionalTypeArg(tp.Name, typeArgs[i], span, tmpl.Decl.Name) {
			return nil
		}
	}

	hasTypeParam := false
	parts := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		parts[i] = ta.Mangled()
		if _, ok := ta.(*types.TypeParamVar); ok {
			hasTypeParam = true
		}
	}
	key := strings.Join(parts, "__")
	if !hasTypeParam {
		if inst, ok := tmpl.Instances[key]; ok {
			return inst
		}
		if len(tmpl.Instances) >= MaxInstantiationsPerTemplate {
			a.Rep.Errorf(span, "generic enum %s instantiation limit (%d) exceeded", tmpl.Decl.Name, MaxInstantiationsPerTemplate)
			return nil
		}
	}

	subst := make(map[string]types.Type, len(typeArgs))
	for i, tp := range tmpl.Decl.TypeParams {
		subst[tp.Name] = typeArgs[i]
	}

	cname := fmt.Sprintf("ll_ge_%s__%s", tmpl.Decl.Name, key)
	if tag := a.typeTag(tmpl.Mod, tmpl.Decl.Name); tag != "" {
		cname = fmt.Sprintf("ll_ge_%s_%s__%s", tag, tmpl.Decl.Name, key)
	}
	inst := &types.Enum{
		Name:       tmpl.Decl.Name,
		CName:      cname,
		PkgDir:     tmpl.Mod.Dir,
		Doc:        tmpl.Decl.Doc,
		IsTagged:   true,
		IsResult:   tmpl.Decl.IsResult,
		TypeArgs:   typeArgs,
		Underlying: types.TypeInt,
	}
	for _, ta := range typeArgs {
		if types.FindComptimeOnlyType(ta) != nil {
			inst.IsComptimeOnly = true
			break
		}
	}
	a.checkAttributes(tmpl.Decl.Attributes, inst, tmpl.Decl.Span, "enum")
	if !hasTypeParam {
		tmpl.Instances[key] = inst
	}

	a.populateEnumVariants(tmpl.Mod, inst, tmpl.Decl, subst)
	if !hasTypeParam {
		a.Prog.EnumDecls[inst] = tmpl.Decl
		a.Prog.EnumMods[inst] = tmpl.Mod
		a.Prog.TypeSpans[inst] = tmpl.Decl.Span
		a.Prog.Enums = append(a.Prog.Enums, inst)
		a.checkSingleEnumRecursion(inst)
		for i, tp := range tmpl.Decl.TypeParams {
			a.verifyBounds(tmpl.Mod, tp, typeArgs[i], span, tmpl.Decl.Name)
		}
		if gm, ok := a.genMethods[tmpl.Decl.Name]["drop"]; ok {
			_ = a.instantiateMethod(gm, inst, nil, span)
			inst.HasCustomDrop = true
		} else if a.genDropRecv[tmpl.Decl.Name] {
			inst.HasCustomDrop = true
			a.pendingDrops = append(a.pendingDrops, inst)
		}
	}
	return inst
}

func (a *Analyzer) registerFunctionsAndMethods(mod *Module) {
	hdrSeen := make(map[string]bool)
	for _, h := range a.Prog.ExternHeaders {
		hdrSeen[h] = true
	}
	modHdrSeen := make(map[string]bool)
	for _, h := range mod.ExternHeaders {
		modHdrSeen[h] = true
	}

	decls := mod.ActiveDecls()
	for _, d := range decls {
		switch decl := d.(type) {
		case *ast.ExternHeaderDecl:
			if !modHdrSeen[decl.Header] {
				modHdrSeen[decl.Header] = true
				mod.ExternHeaders = append(mod.ExternHeaders, decl.Header)
			}
			if !hdrSeen[decl.Header] {
				hdrSeen[decl.Header] = true
				a.Prog.ExternHeaders = append(a.Prog.ExternHeaders, decl.Header)
			}

		case *ast.ExternFnDecl:
			localName := decl.Name
			if decl.Alias != "" {
				localName = decl.Alias
			}
			if a.checkDupDecl(mod, localName, decl.Span) {
				continue
			}
			var ps []types.FuncParam
			for _, p := range decl.Params {
				pm, pt := a.resolveParamType(mod, p.Type, nil, false)
				a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "extern parameter")
				ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
			}
			ret := types.Type(types.TypeVoid)
			if decl.Ret != nil {
				ret = a.resolveReturnType(mod, decl.Ret, nil, false)
			}
			sig := &types.Func{Params: ps, Ret: ret}
			testOnly := ast.FindAttribute(decl.Attributes, "test_only") != nil || mod.IsTestFile
			pkgID := ""
			if mod != nil && mod.Pkg != nil {
				pkgID = mod.Pkg.ID
			}
			ef := &ExternFunc{Name: decl.Name, Sig: sig, PkgID: pkgID, Doc: decl.Doc, Span: decl.Span, IsTestOnly: testOnly}
			a.Prog.ExternFuncs = append(a.Prog.ExternFuncs, ef)
			mod.Symbols[localName] = &Symbol{
				Name:       localName,
				CName:      decl.Name, // exact C ABI symbol!
				Type:       sig,
				IsBind:     true,
				BindKind:   BindOwned,
				IsTopLevel: true,
				Mod:        mod,
				ExternFn:   ef,
				Doc:        decl.Doc,
				Span:       decl.Span,
				IsTestOnly: testOnly,
			}

		case *ast.VarDecl:
			if langAttr := ast.FindAttribute(decl.Attributes, "lang"); langAttr != nil {
				if len(langAttr.Args) == 0 {
					a.Rep.Errorf(langAttr.Span, "@lang attribute requires a string argument (e.g. @lang(\"panic\"))")
				} else {
					kind := strings.Trim(langAttr.Args[0], "\"")
					switch kind {
					case "panic", "assert", "format_loc":
						// valid function lang item
					default:
						a.Rep.Errorf(langAttr.Span, "unknown function lang item @lang(%q); supported function lang items: \"panic\", \"assert\", \"format_loc\"", kind)
					}
				}
			}
			if len(decl.TypeParams) > 0 {
				if !decl.IsBind && !decl.IsConst {
					a.Rep.Errorf(decl.Span, "generic functions must be declared with `bind` or `const`")
					continue
				}
				if _, ok := decl.Value.(*ast.FuncLit); !ok {
					a.Rep.Errorf(decl.Span, "generic top-level `%s` must be a function", func() string {
						if decl.IsConst {
							return "const"
						}
						return "bind"
					}())
					continue
				}
				if a.checkDupDecl(mod, decl.Name, decl.Span) {
					continue
				}
				mod.GenericFuncs[decl.Name] = &GenericFuncTemplate{
					Name:      decl.Name,
					VarDecl:   decl,
					Mod:       mod,
					Instances: make(map[string]*FuncInstance),
				}
				continue
			}

			// Top-level static function `bind name = fn(...)` or `const name = fn(...)`
			if fl, isFn := decl.Value.(*ast.FuncLit); isFn && (decl.IsBind || decl.IsConst) {
				if a.checkDupDecl(mod, decl.Name, decl.Span) {
					continue
				}
				fi := a.buildFuncInstanceFromLit(mod, decl.Name, fl, nil)
				fi.DeclSpan = decl.Span
				fi.Doc = decl.Doc
				fi.IsConst = decl.IsConst
				a.checkAttributes(decl.Attributes, fi, decl.Span, "function")
				if mod.IsTestFile && !fi.IsTest && decl.Name != "test_main" {
					fi.IsTestOnly = true
				}
				a.Prog.Funcs = append(a.Prog.Funcs, fi)
				sym := &Symbol{
					Name:          decl.Name,
					CName:         fi.CName,
					Type:          fi.Sig,
					IsBind:        true,
					IsConst:       fi.IsConst,
					BindKind:      BindOwned,
					IsTopLevel:    true,
					Mod:           mod,
					FuncInst:      fi,
					Doc:           decl.Doc,
					Span:          decl.Span,
					MustUse:       fi.MustUse,
					Deprecated:    fi.Deprecated,
					DeprecatedMsg: fi.DeprecatedMsg,
					IsTest:        fi.IsTest,
					IsTestOnly:    fi.IsTestOnly,
					ComptimeOnly:     fi.ComptimeOnly,
					ComptimeLimit:    fi.ComptimeLimit,
					ComptimeMemLimit: fi.ComptimeMemLimit,
					TrackCaller:   fi.TrackCaller,
					TestDesc:      fi.TestDesc,
				}
				mod.Symbols[decl.Name] = sym
				if mod.Pkg != nil && mod.Pkg.IsCore {
					if langAttr := ast.FindAttribute(decl.Attributes, "lang"); langAttr != nil && len(langAttr.Args) > 0 && strings.Trim(langAttr.Args[0], "\"") == "format_loc" {
						a.Prog.FormatLocFunc = fi
					}
				}
				if a.Prog.IsEntryPackageMod(mod) && decl.Name == "main" {
					fi.IsMain = true
					a.validateMainSig(fi, decl.Span)
					a.Prog.MainFunc = fi
				}
				if a.Prog.IsEntryPackageMod(mod) && decl.Name == "test_main" {
					fi.IsTestMain = true
					a.validateTestMainSig(fi, decl.Span)
					a.Prog.TestMainFunc = fi
				}
				continue
			}

			// Top-level constant / variable
			if !decl.IsBind && !decl.IsConst {
				a.Rep.ErrorWithHint(
					decl.Span,
					"mutable global `let` variables are not allowed at top level",
					"use `const` for compile-time constants or `bind` for runtime constants, and pass mutable state explicitly with `&`",
				)
				continue
			}
			if a.checkDupDecl(mod, decl.Name, decl.Span) {
				continue
			}
			// If not already registered as a simple int constant, resolve during body pass
			sym := mod.Symbols[decl.Name]
			if sym == nil {
				var explicitTy types.Type
				if decl.Type != nil {
					explicitTy = a.resolveValueType(mod, decl.Type, nil, false)
				}
				cname := "ll_g_" + types.SanitizeCIdent(decl.Name)
				if tag := a.moduleTag(mod); tag != "" {
					cname = fmt.Sprintf("ll_g_%s_%s", tag, types.SanitizeCIdent(decl.Name))
				}
				sym = &Symbol{
					Name:       decl.Name,
					CName:      cname,
					Type:       explicitTy,
					IsBind:     true,
					IsConst:    decl.IsConst,
					BindKind:   BindOwned,
					IsTopLevel: true,
					Mod:        mod,
					Doc:        decl.Doc,
					Span:       decl.Span,
				}
				a.checkAttributes(decl.Attributes, sym, decl.Span, "variable")
				if mod.IsTestFile {
					sym.IsTestOnly = true
				}
				mod.Symbols[decl.Name] = sym
			} else {
				sym.IsConst = decl.IsConst
				sym.Doc = decl.Doc
				a.checkAttributes(decl.Attributes, sym, decl.Span, "variable")
				if mod.IsTestFile {
					sym.IsTestOnly = true
				}
			}
			a.Prog.TopVars = append(a.Prog.TopVars, &TopVarInit{Sym: sym, Decl: decl, Mod: mod})

		case *ast.MethodDecl:
			a.registerMethodDecl(mod, decl)
		}
	}
}

func (a *Analyzer) registerMethodDecl(mod *Module, decl *ast.MethodDecl) {
	recvDisplay := decl.RecvType
	if decl.RecvModule != "" {
		recvDisplay = decl.RecvModule + "." + decl.RecvType
	}
	if len(decl.RecvTypeParams) > 0 || len(decl.TypeParams) > 0 {
		if a.genMethods[decl.RecvType] == nil {
			a.genMethods[decl.RecvType] = make(map[string]*GenericFuncTemplate)
		}
		if existing, dup := a.genMethods[decl.RecvType][decl.Name]; dup && existing != nil && existing.MethodDecl != nil {
			a.Rep.Errorf(decl.Span, "duplicate method `%s.%s` (already defined at %s)", recvDisplay, decl.Name, existing.MethodDecl.Span.String())
			return
		}
		a.genMethods[decl.RecvType][decl.Name] = &GenericFuncTemplate{
			Name:       decl.RecvType + "." + decl.Name,
			MethodDecl: decl,
			Mod:        mod,
			Instances:  make(map[string]*FuncInstance),
		}
		return
	}

	recvTy := a.lookupTypeInModule(mod, decl.RecvModule, decl.RecvType)
	if recvTy == nil {
		a.Rep.Errorf(decl.Span, "unknown receiver type %q in method declaration", recvDisplay)
		return
	}
	fi := a.buildMethodInstance(mod, recvTy, decl, nil)
	key := TypeMethodKey(recvTy)
	if a.methods[key] == nil {
		a.methods[key] = make(map[string]*FuncInstance)
	}
	if existing, dup := a.methods[key][decl.Name]; dup {
		if existing != nil && existing.DeclSpan.Start.File != "" {
			a.Rep.Errorf(decl.Span, "duplicate method `%s.%s` (already defined at %s)", recvDisplay, decl.Name, existing.DeclSpan.String())
		} else {
			a.Rep.Errorf(decl.Span, "duplicate method `%s.%s`", recvDisplay, decl.Name)
		}
		return
	}
	a.methods[key][decl.Name] = fi
	if a.Prog.MethodTable[key] == nil {
		a.Prog.MethodTable[key] = make(map[string]*FuncInstance)
	}
	a.Prog.MethodTable[key][decl.Name] = fi
	a.Prog.Funcs = append(a.Prog.Funcs, fi)

	// Check if this is `Drop` (`bind MyStruct.drop = fn(&self)` or `bind MyEnum.drop = fn(&self)`)
	if st, isSt := recvTy.(*types.Struct); isSt && decl.Name == "drop" {
		if decl.RecvMode != ast.RecvMut || len(decl.Params) != 0 || (fi.Sig.Ret != nil && !fi.Sig.Ret.Equals(types.TypeVoid)) {
			a.Rep.Errorf(decl.Span, "`%s.drop` must have signature `(&self)` or `(&self) void`", st.Name)
		} else {
			st.HasCustomDrop = true
		}
	} else if en, isEn := recvTy.(*types.Enum); isEn && decl.Name == "drop" {
		if decl.RecvMode != ast.RecvMut || len(decl.Params) != 0 || (fi.Sig.Ret != nil && !fi.Sig.Ret.Equals(types.TypeVoid)) {
			a.Rep.Errorf(decl.Span, "`%s.drop` must have signature `(&self)` or `(&self) void`", en.Name)
		} else {
			en.HasCustomDrop = true
		}
	}
}

func (a *Analyzer) validateMainSig(fi *FuncInstance, span diag.Span) {
	if len(fi.Sig.Params) != 0 {
		a.Rep.Errorf(span, "`main` must take 0 parameters (use `args()` to read command-line arguments)")
	}
	if fi.Sig.Ret != nil && fi.Sig.Ret != types.TypeInvalid && !fi.Sig.Ret.Equals(types.TypeVoid) && !fi.Sig.Ret.Equals(types.TypeInt) {
		a.Rep.Errorf(span, "`main` return type must be `void` or `int`, got %s", fi.Sig.Ret.String())
	}
}

func (a *Analyzer) validateTestMainSig(fi *FuncInstance, span diag.Span) {
	if len(fi.Sig.Params) != 0 {
		a.Rep.Errorf(span, "`test_main` must take 0 parameters")
	}
	if fi.Sig.Ret != nil && fi.Sig.Ret != types.TypeInvalid && !fi.Sig.Ret.Equals(types.TypeVoid) && !fi.Sig.Ret.Equals(types.TypeInt) {
		a.Rep.Errorf(span, "`test_main` return type must be `void` or `int`, got %s", fi.Sig.Ret.String())
	}
}

func (a *Analyzer) validateDropSig(fi *FuncInstance, span diag.Span) {
	if fi.Sig.Ret != nil && fi.Sig.Ret != types.TypeInvalid && !fi.Sig.Ret.Equals(types.TypeVoid) {
		a.Rep.Errorf(span, "`%s` must return `void`, got %s", fi.Name, fi.Sig.Ret.String())
	}
}

func (a *Analyzer) buildFuncInstanceFromLit(mod *Module, name string, fl *ast.FuncLit, subst map[string]types.Type) *FuncInstance {
	var ps []types.FuncParam
	var pnames []string
	var pspans []diag.Span
	for _, p := range fl.Params {
		pm, pt := a.resolveParamType(mod, p.Type, subst, false)
		if !isDirectSubstTypeParam(p.Type, subst) {
			a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "parameter")
		}
		ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
		pnames = append(pnames, p.Name)
		pspans = append(pspans, p.Span)
	}
	var ret types.Type
	retInferred := false
	if fl.Ret != nil {
		ret = a.resolveReturnType(mod, fl.Ret, subst, false)
	} else {
		retInferred = true
	}
	cname := "ll_fn_" + sanitizeSymbolName(name)
	if name == "main" && a.Prog.IsEntryPackageMod(mod) {
		cname = "ll_user_main"
	} else if tag := a.moduleTag(mod); tag != "" {
		cname = fmt.Sprintf("ll_fn_%s_%s", tag, sanitizeSymbolName(name))
	}
	sig := &types.Func{Params: ps, Ret: ret, Extern: fl.Extern}
	if fl.Extern {
		a.checkCSignature(sig, fl.Span)
	}
	return &FuncInstance{
		Name:        name,
		CName:       cname,
		Sig:         sig,
		ParamNames:  pnames,
		ParamSpans:  pspans,
		Body:        fl.Body,
		Mod:         mod,
		TypeSubst:   subst,
		RetInferred: retInferred,
	}
}

func (a *Analyzer) buildMethodInstance(mod *Module, recvTy types.Type, decl *ast.MethodDecl, subst map[string]types.Type) *FuncInstance {
	var ps []types.FuncParam
	var pnames []string
	var pspans []diag.Span
	isStatic := decl.RecvMode == ast.ReceiverMode(-1)

	if !isStatic {
		if decl.RecvExplicitTy != nil {
			exp := a.resolveValueType(mod, decl.RecvExplicitTy, subst, false)
			if exp != types.TypeInvalid && !exp.Equals(recvTy) {
				a.Rep.Errorf(decl.RecvExplicitTy.GetSpan(), "explicit `self` type %s does not match method receiver %s", exp.String(), recvTy.String())
			}
		}
		ps = append(ps, types.FuncParam{
			Name: "self",
			Mode: recvModeToParamMode(decl.RecvMode),
			Type: recvTy,
		})
		pnames = append(pnames, "self")
		pspans = append(pspans, decl.Span)
	}

	for _, p := range decl.Params {
		pm, pt := a.resolveParamType(mod, p.Type, subst, false)
		if !isDirectSubstTypeParam(p.Type, subst) {
			a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "parameter")
		}
		ps = append(ps, types.FuncParam{Name: p.Name, Mode: pm, Type: pt})
		pnames = append(pnames, p.Name)
		pspans = append(pspans, p.Span)
	}
	ret := types.Type(types.TypeVoid)
	retInferred := false
	if decl.Ret != nil {
		ret = a.resolveReturnType(mod, decl.Ret, subst, false)
	} else {
		ret = nil
		retInferred = true
	}

	cSuffix := decl.Name
	if len(subst) > 0 {
		var keys []string
		for _, ta := range extractSortedSubst(subst) {
			keys = append(keys, ta)
		}
		cSuffix += "__" + strings.Join(keys, "__")
	}

	var deriveInfoEntry *deriveInfo
	if a.derived != nil {
		deriveInfoEntry = a.derived[decl]
	}
	fi := &FuncInstance{
		Name:          fmt.Sprintf("%s.%s", recvTy.String(), decl.Name),
		CName:         fmt.Sprintf("ll_m_%s_%s", recvTy.Mangled(), sanitizeSymbolName(cSuffix)),
		Sig:           &types.Func{Params: ps, Ret: ret},
		IsMethod:      true,
		IsStaticAssoc: isStatic,
		RecvType:      recvTy,
		RecvMode:      decl.RecvMode,
		ParamNames:    pnames,
		ParamSpans:    pspans,
		Body:          decl.Body,
		Mod:           mod,
		TypeSubst:     subst,
		DeclSpan:      decl.Span,
		RetInferred:   retInferred,
		IsDrop:        decl.Name == "drop" && (isStructOrEnum(recvTy)),
		IsConst:       decl.IsConst,
		IsDerived:     deriveInfoEntry != nil,
		Doc:           decl.Doc,
	}
	if deriveInfoEntry != nil {
		fi.DeriveAttrSpan = deriveInfoEntry.Span
		fi.DeriveIfaceName = deriveInfoEntry.Iface
	}
	a.checkAttributes(decl.Attributes, fi, decl.Span, "method")
	if deriveInfoEntry != nil && fi.IsConst && fi.Sig != nil && types.FindComptimeOnlyType(fi.Sig) != nil {
		fi.ComptimeOnly = true
	}
	return fi
}

func extractSortedSubst(subst map[string]types.Type) []string {
	// Deterministic key order for mangling
	keys := make([]string, 0, len(subst))
	for k := range subst {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = subst[k].Mangled()
	}
	return out
}

func sanitizeSymbolName(s string) string {
	r := strings.NewReplacer("[", "_", "]", "_", ",", "_", " ", "", "?", "_opt", ".", "_")
	return r.Replace(s)
}

func sanitizeDeriveScopeSuffix(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func fnv64aHex(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

func typeUsesSelf(t types.Type) bool {
	return typeUsesSelfVisited(t, map[types.Type]bool{})
}

func typeUsesSelfVisited(t types.Type, seen map[types.Type]bool) bool {
	if t == nil || t == types.TypeInvalid || seen[t] {
		return false
	}
	seen[t] = true
	switch v := t.(type) {
	case *types.SelfPlaceholder:
		return true
	case *types.Optional:
		return typeUsesSelfVisited(v.Elem, seen)
	case *types.Vector:
		return typeUsesSelfVisited(v.Elem, seen)
	case *types.StackArray:
		return typeUsesSelfVisited(v.Elem, seen)
	case *types.Set:
		return typeUsesSelfVisited(v.Elem, seen)
	case *types.Map:
		return typeUsesSelfVisited(v.Key, seen) || typeUsesSelfVisited(v.Value, seen)
	case *types.Ptr:
		return typeUsesSelfVisited(v.Elem, seen)
	case *types.Ref:
		return typeUsesSelfVisited(v.Elem, seen)
	case *types.Tuple:
		for _, el := range v.Elems {
			if typeUsesSelfVisited(el, seen) {
				return true
			}
		}
	case *types.Func:
		for _, p := range v.Params {
			if typeUsesSelfVisited(p.Type, seen) {
				return true
			}
		}
		return typeUsesSelfVisited(v.Ret, seen)
	case *types.Struct:
		for _, ta := range v.TypeArgs {
			if typeUsesSelfVisited(ta, seen) {
				return true
			}
		}
	case *types.Enum:
		for _, ta := range v.TypeArgs {
			if typeUsesSelfVisited(ta, seen) {
				return true
			}
		}
	case *types.Distinct:
		return typeUsesSelfVisited(v.Base, seen)
	}
	return false
}

func (a *Analyzer) lookupGenericMethodTemplate(recvTy types.Type, methodName string) *GenericFuncTemplate {
	baseTypeName := ""
	switch r := recvTy.(type) {
	case *types.Struct:
		baseTypeName = r.Name
	case *types.Vector:
		baseTypeName = "List"
	case *types.StackArray:
		baseTypeName = "Array"
	case *types.Primitive:
		baseTypeName = r.String()
	case *types.Distinct:
		baseTypeName = r.Name
	case *types.Enum:
		baseTypeName = r.Name
	}
	if baseTypeName != "" {
		if gset, ok := a.genMethods[baseTypeName]; ok {
			return gset[methodName]
		}
	}
	return nil
}

// LookupMethod finds a user-defined (or monomorphized) method on `recvTy`.
func (a *Analyzer) LookupMethod(recvTy types.Type, methodName string, methodTypeArgs []types.Type, span diag.Span) *FuncInstance {
	return a.LookupMethodWithDerive(recvTy, methodName, methodTypeArgs, span, nil, "", nil)
}

// LookupMethodWithDerive resolves a method on `recvTy`, propagating scoped `@derive`
// authorization to generic helper method templates declared in the same `@derivable` package.
func (a *Analyzer) LookupMethodWithDerive(recvTy types.Type, methodName string, methodTypeArgs []types.Type, span diag.Span, deriveTarget types.Type, derivePkg string, callerMod *Module) *FuncInstance {
	gtmpl := a.lookupGenericMethodTemplate(recvTy, methodName)
	useDerive := deriveTarget != nil && callerMod != nil && gtmpl != nil &&
		a.SamePackage(callerMod, gtmpl.Mod) &&
		(derivePkg == "" || derivePkgScopeID(gtmpl.Mod) == derivePkg) &&
		(a.derived == nil || a.derived[gtmpl.MethodDecl] == nil)
	if useDerive {
		return a.instantiateMethodWithDerive(gtmpl, recvTy, methodTypeArgs, span, deriveTarget, derivePkg)
	}

	key := TypeMethodKey(recvTy)
	if mset, ok := a.methods[key]; ok {
		if fi, ok2 := mset[methodName]; ok2 {
			return fi
		}
	}

	if gtmpl != nil {
		return a.instantiateMethod(gtmpl, recvTy, methodTypeArgs, span)
	}
	if len(methodTypeArgs) == 0 {
		switch tv := recvTy.(type) {
		case *types.Tuple:
			return a.lookupTupleMethod(tv, methodName, span)
		case *types.Optional:
			return a.lookupOptionalMethod(tv, methodName, span)
		}
	}
	return nil
}

func (a *Analyzer) lookupTupleMethod(tup *types.Tuple, methodName string, span diag.Span) *FuncInstance {
	if a.coreMod == nil || tup == nil {
		return nil
	}
	var coreFnName string
	switch methodName {
	case "eq":
		for _, el := range tup.Elems {
			if !a.supportsEq(el) {
				return nil
			}
		}
		coreFnName = "_tuple_eq"
	case "cmp":
		for _, el := range tup.Elems {
			if !a.supportsCmp(el) {
				return nil
			}
		}
		coreFnName = "_tuple_cmp"
	case "hash":
		for _, el := range tup.Elems {
			if !a.supportsHash(el) {
				return nil
			}
		}
		coreFnName = "_tuple_hash"
	case "copy":
		for _, el := range tup.Elems {
			if !a.supportsCopy(el) {
				return nil
			}
		}
		coreFnName = "_tuple_copy"
	case "to_string":
		for _, el := range tup.Elems {
			if !a.supportsToString(el) {
				return nil
			}
		}
		coreFnName = "_tuple_to_string"
	default:
		return nil
	}
	tmpl := a.lookupGenericFnInModOrPkg(a.coreMod, coreFnName, a.coreMod)
	if tmpl == nil {
		return nil
	}
	fi := a.instantiateGenericFunc(tmpl, []types.Type{tup}, span)
	if fi == nil {
		return nil
	}
	a.cacheSynthMethod(TypeMethodKey(tup), methodName, fi)
	return fi
}

func (a *Analyzer) lookupOptionalMethod(opt *types.Optional, methodName string, span diag.Span) *FuncInstance {
	if a.coreMod == nil || opt == nil {
		return nil
	}
	var coreFnName string
	switch methodName {
	case "eq":
		if !a.supportsEq(opt.Elem) {
			return nil
		}
		coreFnName = "_opt_eq"
	case "cmp":
		if !a.supportsCmp(opt.Elem) {
			return nil
		}
		coreFnName = "_opt_cmp"
	case "hash":
		if !a.supportsHash(opt.Elem) {
			return nil
		}
		coreFnName = "_opt_hash"
	case "copy":
		if !a.supportsCopy(opt.Elem) {
			return nil
		}
		coreFnName = "_opt_copy"
	case "to_string":
		if !a.supportsToString(opt.Elem) {
			return nil
		}
		coreFnName = "_opt_to_string"
	default:
		return nil
	}
	tmpl := a.lookupGenericFnInModOrPkg(a.coreMod, coreFnName, a.coreMod)
	if tmpl == nil {
		return nil
	}
	fi := a.instantiateGenericFunc(tmpl, []types.Type{opt.Elem}, span)
	if fi == nil {
		return nil
	}
	a.cacheSynthMethod(TypeMethodKey(opt), methodName, fi)
	return fi
}

func (a *Analyzer) cacheSynthMethod(mkey, methodName string, fi *FuncInstance) {
	if a.methods[mkey] == nil {
		a.methods[mkey] = make(map[string]*FuncInstance)
	}
	prevM, hadM := a.methods[mkey][methodName]
	a.methods[mkey][methodName] = fi
	if a.Prog.MethodTable[mkey] == nil {
		a.Prog.MethodTable[mkey] = make(map[string]*FuncInstance)
	}
	prevMT, hadMT := a.Prog.MethodTable[mkey][methodName]
	a.Prog.MethodTable[mkey][methodName] = fi
	a.recordTrialUndo(func() {
		if hadM {
			a.methods[mkey][methodName] = prevM
		} else {
			delete(a.methods[mkey], methodName)
		}
		if hadMT {
			a.Prog.MethodTable[mkey][methodName] = prevMT
		} else {
			delete(a.Prog.MethodTable[mkey], methodName)
		}
	})
}

func (a *Analyzer) instantiateMethod(tmpl *GenericFuncTemplate, recvTy types.Type, methodTypeArgs []types.Type, span diag.Span) *FuncInstance {
	return a.instantiateMethodWithDerive(tmpl, recvTy, methodTypeArgs, span, nil, "")
}

func (a *Analyzer) instantiateMethodWithDerive(tmpl *GenericFuncTemplate, recvTy types.Type, methodTypeArgs []types.Type, span diag.Span, deriveTarget types.Type, derivePkg string) *FuncInstance {
	if a.monoDepth >= MaxMonomorphDepth {
		a.Rep.Errorf(span, "generic instantiation recursion depth limit (%d) exceeded while instantiating %s.%s", MaxMonomorphDepth, tmpl.MethodDecl.RecvType, tmpl.MethodDecl.Name)
		return nil
	}
	a.monoDepth++
	defer func() { a.monoDepth-- }()

	decl := tmpl.MethodDecl
	subst := make(map[string]types.Type)
	var keyParts []string
	keyParts = append(keyParts, recvTy.Mangled())

	if st, isSt := recvTy.(*types.Struct); isSt && len(decl.RecvTypeParams) == len(st.TypeArgs) {
		for i, rp := range decl.RecvTypeParams {
			subst[rp] = st.TypeArgs[i]
		}
	} else if vec, isVec := recvTy.(*types.Vector); isVec && len(decl.RecvTypeParams) == 1 {
		subst[decl.RecvTypeParams[0]] = vec.Elem
	} else if arr, isArr := recvTy.(*types.StackArray); isArr && len(decl.RecvTypeParams) >= 1 {
		subst[decl.RecvTypeParams[len(decl.RecvTypeParams)-1]] = arr.Elem
	} else if en, isEn := recvTy.(*types.Enum); isEn && len(decl.RecvTypeParams) == len(en.TypeArgs) {
		for i, rp := range decl.RecvTypeParams {
			subst[rp] = en.TypeArgs[i]
		}
	}
	if len(decl.TypeParams) > 0 {
		if len(methodTypeArgs) != len(decl.TypeParams) {
			a.Rep.Errorf(span, "method `%s.%s` expects %d type argument(s), got %d",
				decl.RecvType, decl.Name, len(decl.TypeParams), len(methodTypeArgs))
			return nil
		}
		for i, tp := range decl.TypeParams {
			if !a.verifyNonOptionalTypeArg(tp.Name, methodTypeArgs[i], span, decl.RecvType+"."+decl.Name) {
				return nil
			}
			subst[tp.Name] = methodTypeArgs[i]
			keyParts = append(keyParts, methodTypeArgs[i].Mangled())
			a.verifyBounds(tmpl.Mod, tp, methodTypeArgs[i], span, decl.Name)
		}
	}
	if deriveTarget != nil {
		keyParts = append(keyParts, "derive", deriveTarget.Mangled(), derivePkg)
	}
	instKey := strings.Join(keyParts, "\x00")
	if existing, ok := tmpl.Instances[instKey]; ok {
		return existing
	}
	if len(tmpl.Instances) >= MaxInstantiationsPerTemplate {
		a.Rep.Errorf(span, "generic method `%s.%s` instantiation limit (%d) exceeded", decl.RecvType, decl.Name, MaxInstantiationsPerTemplate)
		return nil
	}
	deriveInfoEntry := a.derived[decl]
	trialKey := ""
	if deriveInfoEntry != nil {
		trialKey = fmt.Sprintf("%s\x00%s\x00%s", recvTy.Mangled(), deriveInfoEntry.Iface, deriveInfoEntry.Method)
		if a.deriveFailed[trialKey] {
			return nil
		}
	}

	funcsBase := len(a.Prog.Funcs)
	fi := a.buildMethodInstance(tmpl.Mod, recvTy, decl, subst)
	fi.IsGenericInst = true
	fi.DeriveTarget = deriveTarget
	fi.DerivePkg = derivePkg
	if len(methodTypeArgs) > 0 {
		displayParts := make([]string, len(methodTypeArgs))
		for i, ta := range methodTypeArgs {
			displayParts[i] = ta.String()
		}
		fi.Name = fmt.Sprintf("%s.%s[%s]", recvTy.String(), decl.Name, strings.Join(displayParts, ", "))
	}
	fi.CName = fmt.Sprintf("%s_%s", fi.CName, fnv64aHex(instKey))
	if fi.IsConst && fi.Sig != nil && types.FindComptimeOnlyType(fi.Sig) != nil {
		fi.ComptimeOnly = true
	}
	tmpl.Instances[instKey] = fi
	mtUndo := func() { delete(tmpl.Instances, instKey) }
	if len(methodTypeArgs) == 0 && deriveTarget == nil {
		mkey := TypeMethodKey(recvTy)
		if a.Prog.MethodTable[mkey] == nil {
			a.Prog.MethodTable[mkey] = make(map[string]*FuncInstance)
		}
		prevMT, hadPrevMT := a.Prog.MethodTable[mkey][decl.Name]
		a.Prog.MethodTable[mkey][decl.Name] = fi
		oldUndo := mtUndo
		mtUndo = func() {
			oldUndo()
			if hadPrevMT {
				a.Prog.MethodTable[mkey][decl.Name] = prevMT
			} else {
				delete(a.Prog.MethodTable[mkey], decl.Name)
			}
		}
	}
	a.recordTrialUndo(mtUndo)
	a.Prog.Funcs = append(a.Prog.Funcs, fi)

	if deriveInfoEntry != nil && len(methodTypeArgs) == 0 && deriveTarget == nil {
		if !a.verifyGenericDerivedMethod(deriveInfoEntry, recvTy, fi, funcsBase, trialKey, mtUndo) {
			return nil
		}
	}

	// Typecheck body immediately if we are already in or past body checking!
	if a.inBodyPass {
		if fi.RetInferred {
			_ = a.ensureFuncRetResolved(fi, span)
		} else {
			a.checkFuncInstance(fi)
		}
	}
	return fi
}

func (a *Analyzer) instantiateGenericFunc(tmpl *GenericFuncTemplate, typeArgs []types.Type, span diag.Span) *FuncInstance {
	return a.instantiateGenericFuncWithDerive(tmpl, typeArgs, span, nil, "")
}

func (a *Analyzer) instantiateGenericFuncWithDerive(tmpl *GenericFuncTemplate, typeArgs []types.Type, span diag.Span, deriveTarget types.Type, derivePkg string) *FuncInstance {
	if a.monoDepth >= MaxMonomorphDepth {
		a.Rep.Errorf(span, "generic instantiation recursion depth limit (%d) exceeded while instantiating %s", MaxMonomorphDepth, tmpl.VarDecl.Name)
		return nil
	}
	a.monoDepth++
	defer func() { a.monoDepth-- }()

	decl := tmpl.VarDecl
	if len(typeArgs) != len(decl.TypeParams) {
		a.Rep.Errorf(span, "generic function %q expects %d type argument(s), got %d",
			decl.Name, len(decl.TypeParams), len(typeArgs))
		return nil
	}
	subst := make(map[string]types.Type, len(typeArgs))
	parts := make([]string, len(typeArgs))
	displayParts := make([]string, len(typeArgs))
	for i, tp := range decl.TypeParams {
		if !a.verifyNonOptionalTypeArg(tp.Name, typeArgs[i], span, decl.Name) {
			return nil
		}
		subst[tp.Name] = typeArgs[i]
		parts[i] = typeArgs[i].Mangled()
		displayParts[i] = typeArgs[i].String()
		a.verifyBounds(tmpl.Mod, tp, typeArgs[i], span, decl.Name)
	}
	key := strings.Join(parts, "\x00")
	if deriveTarget != nil {
		key += "\x00derive\x00" + deriveTarget.Mangled() + "\x00" + derivePkg
	}
	if existing, ok := tmpl.Instances[key]; ok {
		return existing
	}
	if len(tmpl.Instances) >= MaxInstantiationsPerTemplate {
		a.Rep.Errorf(span, "generic function %q instantiation limit (%d) exceeded", decl.Name, MaxInstantiationsPerTemplate)
		return nil
	}

	fl := decl.Value.(*ast.FuncLit)
	instName := fmt.Sprintf("%s[%s]", decl.Name, strings.Join(displayParts, ", "))
	fi := a.buildFuncInstanceFromLit(tmpl.Mod, instName, fl, subst)
	fi.DeclSpan = decl.Span
	fi.Doc = decl.Doc
	fi.IsConst = decl.IsConst
	fi.IsGenericInst = true
	fi.DeriveTarget = deriveTarget
	fi.DerivePkg = derivePkg
	humanParts := types.SanitizeCIdent(strings.Join(parts, "_"))
	hashHex := fnv64aHex(key)
	declCName := types.SanitizeCIdent(decl.Name)
	var cname string
	if tag := a.moduleTag(tmpl.Mod); tag != "" {
		if humanParts != "" {
			cname = fmt.Sprintf("ll_gfn_%s_%s_%s_%s", tag, declCName, humanParts, hashHex)
		} else {
			cname = fmt.Sprintf("ll_gfn_%s_%s_%s", tag, declCName, hashHex)
		}
	} else if humanParts != "" {
		cname = fmt.Sprintf("ll_gfn_%s_%s_%s", declCName, humanParts, hashHex)
	} else {
		cname = fmt.Sprintf("ll_gfn_%s_%s", declCName, hashHex)
	}
	fi.CName = cname
	a.checkAttributes(decl.Attributes, fi, decl.Span, "function")
	if fi.IsConst && fi.Sig != nil && types.FindComptimeOnlyType(fi.Sig) != nil {
		fi.ComptimeOnly = true
	}
	tmpl.Instances[key] = fi
	a.recordTrialUndo(func() { delete(tmpl.Instances, key) })
	a.Prog.Funcs = append(a.Prog.Funcs, fi)
	if a.inBodyPass {
		if fi.RetInferred {
			_ = a.ensureFuncRetResolved(fi, span)
		} else {
			a.checkFuncInstance(fi)
		}
	}
	return fi
}

func (a *Analyzer) verifyBounds(mod *Module, tp ast.TypeParam, concrete types.Type, span diag.Span, contextName string) {
	if tp.IsConst {
		return
	}
	if _, isTP := concrete.(*types.TypeParamVar); isTP {
		return
	}
	for _, bName := range tp.Bounds {
		bTy := a.lookupTypeInModule(mod, "", bName)
		ifc, ok := bTy.(*types.Interface)
		if !ok {
			a.Rep.Errorf(tp.Span, "bound %q is not an interface", bName)
			continue
		}
		if !a.ImplementsInterface(concrete, ifc) {
			a.Rep.Errorf(span, "type %s does not satisfy interface %s required by %s", concrete.String(), ifc.Name, contextName)
		}
	}
}

// ImplementsInterface checks structural satisfaction (including built-in Printable, Copyable, Drop)
// and registers a VTableImpl when `ifc` is a runtime object-safe interface.
func (a *Analyzer) ImplementsInterface(concrete types.Type, ifc *types.Interface) bool {
	if concrete == nil || concrete == types.TypeInvalid {
		return true
	}
	if concrete.Equals(ifc) {
		return true
	}
	if ifc == a.Prog.IfacePrintable {
		return a.supportsToString(concrete)
	}
	if ifc == a.Prog.IfaceCopyable {
		return a.supportsCopy(concrete)
	}
	if ifc == a.Prog.IfaceDrop {
		if st, ok := concrete.(*types.Struct); ok && st.HasCustomDrop {
			return true
		}
		if en, ok := concrete.(*types.Enum); ok && en.HasCustomDrop {
			return true
		}
		return false
	}
	if ifc == a.Prog.IfaceHashable {
		return a.supportsHash(concrete)
	}
	if ifc == a.Prog.IfaceEquatable {
		return a.supportsEq(concrete)
	}
	if ifc == a.Prog.IfaceComparable {
		return a.supportsCmp(concrete)
	}
	if a.Prog.GenIfaceCollectable != nil && strings.HasPrefix(ifc.Name, a.Prog.GenIfaceCollectable.Decl.Name) {
		if a.isCollectableType(concrete) {
			itemTy := a.collectableItemType(concrete)
			if len(ifc.Methods) > 0 && len(ifc.Methods[0].Params) == 1 {
				reqItem := ifc.Methods[0].Params[0].Type
				if itemTy != nil && itemTy != types.TypeInvalid && (reqItem == nil || itemTy.Equals(reqItem)) {
					return true
				}
			}
		}
	}
	if a.Prog.GenIfaceIterable != nil && strings.HasPrefix(ifc.Name, a.Prog.GenIfaceIterable.Decl.Name) {
		switch c := types.CollView(concrete).(type) {
		case *types.Vector, *types.StackArray, *types.Set, *types.Map:
			return true
		case *types.Primitive:
			if c.Kind == types.PrimString {
				return true
			}
		}
	}
	if a.Prog.GenIfaceIndexable != nil && strings.HasPrefix(ifc.Name, a.Prog.GenIfaceIndexable.Decl.Name) {
		if a.isIndexableType(concrete) {
			itemTy := a.indexableItemType(concrete)
			for _, m := range ifc.Methods {
				if m.Name == "get" {
					if opt, isOpt := m.Ret.(*types.Optional); isOpt {
						reqItem := opt.Elem
						if itemTy != nil && itemTy != types.TypeInvalid && (reqItem == nil || itemTy.Equals(reqItem)) {
							return true
						}
					}
				}
			}
		}
	}
	if a.Prog.GenIfaceKeyIndexable != nil && strings.HasPrefix(ifc.Name, a.Prog.GenIfaceKeyIndexable.Decl.Name) {
		if a.isKeyIndexableType(concrete) {
			keyTy, valTy := a.keyIndexableTypes(concrete)
			for _, m := range ifc.Methods {
				if m.Name == "get" && len(m.Params) == 1 {
					reqKey := m.Params[0].Type
					if opt, isOpt := m.Ret.(*types.Optional); isOpt {
						reqVal := opt.Elem
						if (reqKey == nil || keyTy.Equals(reqKey)) && (reqVal == nil || valTy.Equals(reqVal)) {
							return true
						}
					}
				}
			}
		}
	}
	if a.Prog.GenIfaceContainable != nil && strings.HasPrefix(ifc.Name, a.Prog.GenIfaceContainable.Decl.Name) {
		switch st := concrete.(type) {
		case *types.Vector, *types.StackArray, *types.Set, *types.Map:
			if a.isContainableType(concrete) {
				itemTy := a.containableItemType(concrete)
				for _, m := range ifc.Methods {
					if m.Name == "contains" && m.RecvMode == types.ModeRead &&
						len(m.Params) == 1 && m.Params[0].Mode == types.ModeRead && m.Ret.Equals(types.TypeBool) {
						reqItem := m.Params[0].Type
						if itemTy != nil && itemTy != types.TypeInvalid && (reqItem == nil || itemTy.Equals(reqItem)) {
							return true
						}
					}
				}
			}
			return false
		case *types.Struct:
			if (a.isStdStructTemplate(st, a.Prog.GenStructList) || a.isStdStructTemplate(st, a.Prog.GenStructArray)) &&
				len(st.TypeArgs) == 1 && !a.supportsEq(st.TypeArgs[0]) {
				return false
			}
		}
	}

	// Sealed interface check: if the interface has any unexported methods,
	// only types defined in the same package directory can implement it.
	for _, req := range ifc.Methods {
		if !IsExported(req.Name) {
			ifcMod := a.Prog.InterfaceMods[ifc]
			concreteMod := a.typeModule(concrete)
			samePkg := false
			if ifcMod != nil && concreteMod != nil && a.SamePackage(concreteMod, ifcMod) {
				samePkg = true
			} else if ifc.PkgDir != "" && types.TypePkgDir(concrete) != "" && filepath.Clean(ifc.PkgDir) == filepath.Clean(types.TypePkgDir(concrete)) {
				samePkg = true
			}
			if !samePkg {
				return false
			}
			break
		}
	}

	impls := make([]*FuncInstance, len(ifc.Methods))
	for idx, req := range ifc.Methods {
		// Special-case built-in `to_string` if a user interface includes `to_string: (self): string`
		fi := a.LookupMethod(concrete, req.Name, nil, diag.Span{})
		if fi == nil {
			if req.Name == "to_string" && req.RecvMode == types.ModeRead && len(req.Params) == 0 && req.Ret.Equals(types.TypeString) && a.supportsToString(concrete) {
				impls[idx] = nil // synthesized built-in `to_string` wrapper in vtable
				continue
			}
			return false
		}
		if fi.IsStaticAssoc || len(fi.Sig.Params) != len(req.Params)+1 {
			return false
		}
		if fi.Sig.Params[0].Mode != req.RecvMode {
			return false
		}
		for pi, rp := range req.Params {
			fp := fi.Sig.Params[pi+1]
			expected := rp.Type
			if _, isSelf := expected.(*types.SelfPlaceholder); isSelf {
				expected = concrete
			}
			if fp.Mode != rp.Mode || !fp.Type.Equals(expected) {
				return false
			}
		}
		expectedRet := req.Ret
		if _, isSelf := expectedRet.(*types.SelfPlaceholder); isSelf {
			expectedRet = concrete
		}
		retTy := a.ensureFuncRetResolved(fi, a.Prog.TypeSpans[concrete])
		if !retTy.Equals(expectedRet) {
			return false
		}
		impls[idx] = fi
	}

	if !ifc.UsesSelf {
		vtKey := concrete.Mangled() + "__as__" + ifc.Mangled()
		if _, exists := a.Prog.VTables[vtKey]; !exists {
			a.Prog.VTables[vtKey] = &VTableImpl{
				Concrete: concrete,
				Iface:    ifc,
				Methods:  impls,
			}
		}
	}
	return true
}

// RegisterVTable ensures a VTableImpl is recorded (including for `Printable`!).
func (a *Analyzer) RegisterVTable(concrete types.Type, ifc *types.Interface) {
	if ifc.UsesSelf {
		return
	}
	vtKey := concrete.Mangled() + "__as__" + ifc.Mangled()
	if _, exists := a.Prog.VTables[vtKey]; exists {
		return
	}
	// Sealed interface check: if the interface has any unexported methods,
	// only types defined in the same package directory can implement it.
	for _, req := range ifc.Methods {
		if !IsExported(req.Name) {
			ifcMod := a.Prog.InterfaceMods[ifc]
			concreteMod := a.typeModule(concrete)
			samePkg := false
			if ifcMod != nil && concreteMod != nil && a.SamePackage(concreteMod, ifcMod) {
				samePkg = true
			} else if ifc.PkgDir != "" && types.TypePkgDir(concrete) != "" && filepath.Clean(ifc.PkgDir) == filepath.Clean(types.TypePkgDir(concrete)) {
				samePkg = true
			}
			if !samePkg {
				return
			}
			break
		}
	}

	impls := make([]*FuncInstance, len(ifc.Methods))
	for idx, req := range ifc.Methods {
		impls[idx] = a.LookupMethod(concrete, req.Name, nil, diag.Span{})
	}
	a.Prog.VTables[vtKey] = &VTableImpl{
		Concrete: concrete,
		Iface:    ifc,
		Methods:  impls,
	}
}

func (a *Analyzer) supportsToString(t types.Type) bool {
	return a.supportsToStringVisited(t, make(map[types.Type]bool))
}

func (a *Analyzer) supportsToStringVisited(t types.Type, seen map[types.Type]bool) bool {
	// Check a set's or map's element types before instantiating its `to_string`,
	// whose body calls `to_string` on them.
	if view := types.CollView(t); view != t {
		return a.supportsToStringVisited(view, seen) && a.LookupMethod(t, "to_string", nil, diag.Span{}) != nil
	}
	if a.LookupMethod(t, "to_string", nil, diag.Span{}) != nil {
		return true
	}
	if en, ok := t.(*types.Enum); ok && !en.IsTagged && a.isCompBuiltinEnum(en) {
		return true
	}
	if st, ok := t.(*types.Struct); ok && a.isCompBuiltinStruct(st) {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind != types.PrimVoid
	case *types.Enum:
		return false // only through a `to_string` method (`@derive(Printable)`)
	case *types.Distinct:
		return a.supportsToStringVisited(v.Base, seen)
	case *types.Vector:
		return a.supportsToStringVisited(v.Elem, seen)
	case *types.StackArray:
		return a.supportsToStringVisited(v.Elem, seen)
	case *types.Tuple:
		for _, el := range v.Elems {
			if !a.supportsToStringVisited(el, seen) {
				return false
			}
		}
		return true
	case *types.Set:
		return a.supportsToStringVisited(v.Elem, seen)
	case *types.Map:
		return a.supportsToStringVisited(v.Key, seen) && a.supportsToStringVisited(v.Value, seen)
	case *types.Optional:
		return a.supportsToStringVisited(v.Elem, seen)
	case *types.Interface:
		return v == a.Prog.IfacePrintable
	default:
		return false
	}
}

func (a *Analyzer) supportsCopy(t types.Type) bool {
	return a.supportsCopyVisited(t, make(map[types.Type]bool))
}

func (a *Analyzer) supportsCopyVisited(t types.Type, seen map[types.Type]bool) bool {
	if t.IsPlain() {
		return true
	}
	// Check a set's or map's element types before instantiating its `copy`,
	// whose body copies them.
	if view := types.CollView(t); view != t {
		return a.supportsCopyVisited(view, seen) && a.LookupMethod(t, "copy", nil, diag.Span{}) != nil
	}
	if a.LookupMethod(t, "copy", nil, diag.Span{}) != nil {
		return true
	}
	if st, ok := t.(*types.Struct); ok && a.isCompBuiltinStruct(st) {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind == types.PrimString
	case *types.Vector:
		return a.supportsCopyVisited(v.Elem, seen)
	case *types.StackArray:
		return a.supportsCopyVisited(v.Elem, seen)
	case *types.Tuple:
		for _, el := range v.Elems {
			if !a.supportsCopyVisited(el, seen) {
				return false
			}
		}
		return true
	case *types.Set:
		return a.supportsCopyVisited(v.Elem, seen)
	case *types.Map:
		return a.supportsCopyVisited(v.Key, seen) && a.supportsCopyVisited(v.Value, seen)
	case *types.Optional:
		return a.supportsCopyVisited(v.Elem, seen)
	case *types.Distinct:
		return a.supportsCopyVisited(v.Base, seen)
	case *types.Interface:
		return !v.UsesSelf
	case *types.Func:
		return true
	case *types.Enum, *types.Struct:
		return false // non-plain: only through a `copy` method (`@derive(Copyable)`)
	default:
		return false
	}
}

func (a *Analyzer) isCompBuiltinEnum(en *types.Enum) bool {
	return en != nil && (en == a.Prog.EnumTargetOs || en == a.Prog.EnumTargetArch || en == a.Prog.EnumEndian || en == a.Prog.EnumTypeKind)
}

func (a *Analyzer) isCompBuiltinStruct(st *types.Struct) bool {
	return st != nil && (st == a.Prog.StructTarget || st == a.Prog.StructAttrInfo || st == a.Prog.StructFieldInfo || st == a.Prog.StructVariantInfo || st == a.Prog.StructElemInfo || st == a.Prog.StructDistinctInfo)
}

func (a *Analyzer) supportsEq(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	switch v := t.(type) {
	case *types.Optional, *types.Tuple:
		if a.LookupMethod(t, "eq", nil, diag.Span{}) != nil {
			return true
		}
		return a.LookupMethod(t, "cmp", nil, diag.Span{}) != nil
	case *types.Distinct:
		if a.LookupMethod(t, "eq", nil, diag.Span{}) != nil || a.LookupMethod(t, "cmp", nil, diag.Span{}) != nil {
			return true
		}
		return a.supportsEq(v.Base)
	}
	if types.SupportsEq(t) {
		return true
	}
	if en, ok := t.(*types.Enum); ok && !en.IsTagged && a.isCompBuiltinEnum(en) {
		return true
	}
	if st, ok := t.(*types.Struct); ok && a.isCompBuiltinStruct(st) {
		return true
	}
	if a.LookupMethod(t, "eq", nil, diag.Span{}) != nil {
		return true
	}
	if a.LookupMethod(t, "cmp", nil, diag.Span{}) != nil {
		return true
	}
	if tp, isTP := t.(*types.TypeParamVar); isTP {
		for _, b := range tp.Bounds {
			if b == a.Prog.IfaceEquatable || b == a.Prog.IfaceComparable || b.Name == "Equatable" || b.Name == "Comparable" {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) supportsCmp(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	if p := types.UnderlyingPrimitive(t); p != nil {
		return p.IsNumeric() || p.Kind == types.PrimChar || p.Kind == types.PrimString
	}
	if en, ok := t.(*types.Enum); ok && !en.IsTagged && a.isCompBuiltinEnum(en) {
		return true
	}
	if a.LookupMethod(t, "cmp", nil, diag.Span{}) != nil {
		return true
	}
	if d, isDist := t.(*types.Distinct); isDist {
		return a.supportsCmp(d.Base)
	}
	if tp, isTP := t.(*types.TypeParamVar); isTP {
		for _, b := range tp.Bounds {
			if b == a.Prog.IfaceComparable || b.Name == "Comparable" {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) supportsHash(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	switch v := t.(type) {
	case *types.Tuple:
		return a.LookupMethod(t, "hash", nil, diag.Span{}) != nil
	case *types.Distinct:
		if a.LookupMethod(t, "hash", nil, diag.Span{}) != nil {
			return true
		}
		return a.supportsHash(v.Base)
	}
	return types.IsHashable(t) || a.LookupMethod(t, "hash", nil, diag.Span{}) != nil
}

// cRepresentable reports whether values of `t` can cross into C unchanged: numbers, `bool`,
// `char`, pointers, tag-only enums, `extern fn`s, and plain structs, arrays, and distinct types
// made of those.
func cRepresentable(t types.Type) bool {
	return cRepresentableVisited(t, make(map[types.Type]bool))
}

func cRepresentableVisited(t types.Type, seen map[types.Type]bool) bool {
	if seen[t] {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind != types.PrimString
	case *types.Ptr:
		return true
	case *types.Func:
		return v.Extern
	case *types.Enum:
		return !v.IsTagged
	case *types.Distinct:
		return cRepresentableVisited(v.Base, seen)
	case *types.StackArray:
		return cRepresentableVisited(v.Elem, seen)
	case *types.Struct:
		seen[v] = true
		if v.HasCustomDrop || v.Lang != "" {
			return false
		}
		for _, f := range v.Fields {
			if !cRepresentableVisited(f.Type, seen) {
				return false
			}
		}
		return true
	}
	return false
}

// checkCSignature reports `extern fn` parameters and results that have no C representation.
func (a *Analyzer) checkCSignature(ft *types.Func, span diag.Span) {
	for _, p := range ft.Params {
		if p.Type != types.TypeInvalid && !cRepresentable(p.Type) {
			a.Rep.ErrorWithHint(span,
				fmt.Sprintf("an `extern fn` can't take %s: C has no equivalent type", p.Type.String()),
				"use numbers, `bool`, `char`, `ptr[T]`, `extern fn` types, or plain structs of those")
		}
	}
	if ft.Ret != nil && ft.Ret != types.TypeVoid && ft.Ret != types.TypeInvalid && !cRepresentable(ft.Ret) {
		a.Rep.ErrorWithHint(span,
			fmt.Sprintf("an `extern fn` can't return %s: C has no equivalent type", ft.Ret.String()),
			"return a number, `bool`, `char`, `ptr[T]`, or a plain struct")
	}
}
