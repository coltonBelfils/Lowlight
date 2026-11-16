package sema

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

const MaxMonomorphDepth = 128

func (a *Analyzer) moduleTag(mod *Module) string {
	if mod == nil || mod == a.Prog.EntryMod || mod == a.coreMod {
		return ""
	}
	base := filepath.Base(mod.Path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return types.SanitizeCIdent(base)
}

func (a *Analyzer) checkAttributes(attrs []ast.Attribute, target any, span diag.Span, kind string) {
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
		case "doc":
			// documentation string attribute, valid on all declarations
		case "lang":
			// handled specially on interfaces and top-level func/symbols
		default:
			a.Rep.Errorf(attr.Span, "unknown attribute @%s", attr.Name)
		}
	}

	if hasInline && hasNoInline {
		a.Rep.Errorf(span, "cannot specify both @inline and @noinline")
	}
}

func (a *Analyzer) lookupTypeInModule(mod *Module, moduleAlias, name string) types.Type {
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

func (a *Analyzer) runTypeAndSymbolRegistration() {
	// Pass 1A: Evaluate simple top-level `bind CONST = <int_expr>` so stack array lengths and enum values work.
	for _, mod := range a.Prog.Modules {
		a.collectTopLevelIntConsts(mod)
	}

	// Pass 1B: Register nominal type headers across all modules (order-independent!).
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

	// Pass 1E: Register extern headers/functions, top-level functions/vars, and methods.
	for _, mod := range a.Prog.Modules {
		a.registerFunctionsAndMethods(mod)
	}
}

func (a *Analyzer) collectTopLevelIntConsts(mod *Module) {
	for _, d := range mod.File.Decls {
		vd, ok := d.(*ast.VarDecl)
		if !ok || !vd.IsBind || len(vd.TypeParams) > 0 || vd.IsOptName {
			continue
		}
		if val, ok2 := a.evalConstInt(mod, vd.Value); ok2 {
			if a.checkDupDecl(mod, vd.Name, vd.Span) {
				continue
			}
			ty := types.TypeInt
			if vd.Type != nil {
				if nt, isNt := vd.Type.(*ast.NamedType); isNt && nt.Module == "" && len(nt.TypeArgs) == 0 {
					if p := types.LookupPrimitive(nt.Name); p != nil && p.IsInteger() {
						ty = p
					}
				}
			}
			mod.Symbols[vd.Name] = &Symbol{
				Name:        vd.Name,
				CName:       types.SanitizeCIdent(vd.Name),
				Mod:         mod,
				Type:        ty,
				IsBind:      true,
				BindKind:    BindOwned,
				IsTopLevel:  true,
				IsConstInt:  true,
				ConstIntVal: val,
				Span:        vd.Span,
			}
		}
	}
}

func (a *Analyzer) evalConstInt(mod *Module, e ast.Expr) (int64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return int64(v.Val), true
	case *ast.CharLit:
		return int64(v.Val), true
	case *ast.IdentExpr:
		if sym := a.lookupTopSymbol(mod, "", v.Name); sym != nil && sym.IsConstInt {
			return sym.ConstIntVal, true
		}
		return 0, false
	case *ast.UnaryExpr:
		sub, ok := a.evalConstInt(mod, v.Operand)
		if !ok {
			return 0, false
		}
		switch v.Op {
		case token.Minus:
			return -sub, true
		case token.Tilde:
			return ^sub, true
		}
	case *ast.UnsafeExpr:
		return a.evalConstInt(mod, v.Expr)
	case *ast.BinaryExpr:
		l, ok1 := a.evalConstInt(mod, v.Left)
		r, ok2 := a.evalConstInt(mod, v.Right)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch v.Op {
		case token.Plus:
			return l + r, true
		case token.Minus:
			return l - r, true
		case token.Star:
			return l * r, true
		case token.Slash:
			if r == 0 {
				return 0, false
			}
			return l / r, true
		case token.Percent:
			if r == 0 {
				return 0, false
			}
			return l % r, true
		case token.Shl:
			return l << uint(r), true
		case token.Shr:
			return l >> uint(r), true
		case token.Amp:
			return l & r, true
		case token.Pipe:
			return l | r, true
		case token.Caret:
			return l ^ r, true
		}
	}
	return 0, false
}

func (a *Analyzer) evalConstIntFromTypeExpr(mod *Module, te ast.TypeExpr) (int64, bool) {
	if nt, ok := te.(*ast.NamedType); ok {
		if val, err := strconv.ParseInt(nt.Name, 10, 64); err == nil {
			return val, true
		}
		if sym := a.lookupTopSymbol(mod, nt.Module, nt.Name); sym != nil && sym.IsConstInt {
			return sym.ConstIntVal, true
		}
	}
	return 0, false
}

func (a *Analyzer) registerModuleTypeHeaders(mod *Module) {
	for _, d := range mod.File.Decls {
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
					default:
						a.Rep.Errorf(td.Span, "unknown struct lang item @lang(%q); supported struct lang items: \"list\", \"array\", \"set\", \"map\", \"range\"", langItem)
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
			if langItem != "" {
				a.Rep.Errorf(td.Span, "struct lang item @lang(%q) must be a generic struct", langItem)
			}
			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			cname := "ll_s_" + td.Name
			if tag := a.moduleTag(mod); tag != "" {
				cname = fmt.Sprintf("ll_s_%s_%s", tag, td.Name)
			}
			st := &types.Struct{
				Name:   td.Name,
				CName:  cname,
				PkgDir: mod.Dir,
				Packed: td.Packed || ast.FindAttribute(td.Attributes, "packed") != nil,
			}
			a.checkAttributes(td.Attributes, st, td.Span, "struct")
			mod.Types[td.Name] = st
			a.Prog.StructDecls[st] = td
			a.Prog.StructMods[st] = mod
			a.Prog.TypeSpans[st] = td.Span
			a.Prog.Structs = append(a.Prog.Structs, st)

		case *ast.EnumDecl:
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				a.Rep.Errorf(langAttr.Span, "@lang attribute cannot be applied to an enum declaration")
			}
			isTagged := len(td.TypeParams) > 0
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
			if tag := a.moduleTag(mod); tag != "" {
				cname = fmt.Sprintf("ll_e_%s_%s", tag, td.Name)
			}
			en := &types.Enum{
				Name:       td.Name,
				CName:      cname,
				PkgDir:     mod.Dir,
				IsTagged:   isTagged,
				Underlying: types.TypeInt,
			}
			a.checkAttributes(td.Attributes, en, td.Span, "enum")
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
				a.Prog.TypeSpans[a.Prog.IfacePrintable] = td.Span
				mod.Types[td.Name] = a.Prog.IfacePrintable
				continue
			}

			if langItem == "copyable" || td.Name == "Copyable" {
				if a.Prog.TypeSpans[a.Prog.IfaceCopyable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceCopyable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"copyable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceCopyable].String())
					continue
				}
				a.Prog.IfaceCopyable.Name = td.Name
				a.Prog.TypeSpans[a.Prog.IfaceCopyable] = td.Span
				mod.Types[td.Name] = a.Prog.IfaceCopyable
				continue
			}

			if langItem == "drop" || td.Name == "Drop" {
				if a.Prog.TypeSpans[a.Prog.IfaceDrop].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceDrop].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"drop\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceDrop].String())
					continue
				}
				a.Prog.IfaceDrop.Name = td.Name
				a.Prog.TypeSpans[a.Prog.IfaceDrop] = td.Span
				mod.Types[td.Name] = a.Prog.IfaceDrop
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
				a.Prog.TypeSpans[a.Prog.IfaceHashable] = td.Span
				mod.Types[td.Name] = a.Prog.IfaceHashable
				continue
			}

			if langItem == "equatable" || td.Name == "Equatable" {
				if a.Prog.TypeSpans[a.Prog.IfaceEquatable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceEquatable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"equatable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceEquatable].String())
					continue
				}
				a.Prog.IfaceEquatable.Name = td.Name
				a.Prog.TypeSpans[a.Prog.IfaceEquatable] = td.Span
				mod.Types[td.Name] = a.Prog.IfaceEquatable
				continue
			}

			if langItem == "comparable" || td.Name == "Comparable" {
				if a.Prog.TypeSpans[a.Prog.IfaceComparable].Start.File != "" && a.Prog.TypeSpans[a.Prog.IfaceComparable].Start.File != td.Span.Start.File {
					a.Rep.Errorf(td.Span, "duplicate lang item @lang(\"comparable\") (already defined at %s)", a.Prog.TypeSpans[a.Prog.IfaceComparable].String())
					continue
				}
				a.Prog.IfaceComparable.Name = td.Name
				a.Prog.TypeSpans[a.Prog.IfaceComparable] = td.Span
				mod.Types[td.Name] = a.Prog.IfaceComparable
				continue
			}

			if langItem != "" && langItem != "collectable" && langItem != "iterable" && langItem != "iterator" {
				a.Rep.Errorf(td.Span, "unknown interface lang item @lang(%q); supported interface lang items: \"printable\", \"copyable\", \"drop\", \"hashable\", \"equatable\", \"comparable\", \"collectable\", \"iterable\", \"iterator\"", langItem)
				continue
			}

			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			cname := "ll_iface_" + td.Name
			if tag := a.moduleTag(mod); tag != "" {
				cname = fmt.Sprintf("ll_iface_%s_%s", tag, td.Name)
			}
			ifc := &types.Interface{
				Name:   td.Name,
				CName:  cname,
				PkgDir: mod.Dir,
			}
			if langItem == "collectable" || td.Name == "Collectable" {
				a.Prog.IfaceCollectable = ifc
			} else if langItem == "iterable" || td.Name == "Iterable" {
				a.Prog.IfaceIterable = ifc
			} else if langItem == "iterator" || td.Name == "Iterator" {
				a.Prog.IfaceIterator = ifc
			} else if langItem == "containable" || td.Name == "Containable" {
				a.Prog.IfaceContainable = ifc
			}
			a.checkAttributes(td.Attributes, ifc, td.Span, "interface")
			mod.Types[td.Name] = ifc
			a.Prog.TypeSpans[ifc] = td.Span
			a.Prog.InterfaceMods[ifc] = mod
			a.Prog.Interfaces = append(a.Prog.Interfaces, ifc)

		case *ast.DistinctTypeDecl:
			if langAttr := ast.FindAttribute(td.Attributes, "lang"); langAttr != nil {
				a.Rep.Errorf(langAttr.Span, "@lang attribute cannot be applied to a distinct type declaration")
			}
			if a.checkDupType(mod, td.Name, td.Span) {
				continue
			}
			cname := "ll_d_" + td.Name
			if tag := a.moduleTag(mod); tag != "" {
				cname = fmt.Sprintf("ll_d_%s_%s", tag, td.Name)
			}
			dt := &types.Distinct{
				Name:   td.Name,
				CName:  cname,
				PkgDir: mod.Dir,
				Base:   types.TypeInvalid,
			}
			a.checkAttributes(td.Attributes, dt, td.Span, "distinct type")
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
	for _, d := range mod.File.Decls {
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
			if !ok || en.IsTagged {
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
				en.Variants = append(en.Variants, types.EnumVariant{
					Name:  v.Name,
					CName: "v_" + types.SanitizeCIdent(v.Name),
					Value: nextVal,
					Kind:  types.VariantUnit,
				})
				nextVal++
			}
		}
	}
}

func (a *Analyzer) resolveInterfaces(mod *Module) {
	for _, d := range mod.File.Decls {
		td, ok := d.(*ast.InterfaceDecl)
		if !ok {
			continue
		}
		if td.Name == "Printable" || td.Name == "Copyable" || td.Name == "Drop" {
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
			var fparams []types.FuncParam
			for _, p := range m.Params {
				pmode, pty := a.resolveParamType(mod, p.Type, nil, true)
				if _, isSelf := pty.(*types.SelfPlaceholder); isSelf {
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
				if _, isSelf := ret.(*types.SelfPlaceholder); isSelf {
					ifc.UsesSelf = true
				}
			}
			ifc.Methods = append(ifc.Methods, types.InterfaceMethod{
				Name:     m.Name,
				RecvMode: recvModeToParamMode(m.RecvMode),
				Params:   fparams,
				Ret:      ret,
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
	for _, d := range mod.File.Decls {
		switch td := d.(type) {
		case *ast.StructDecl:
			if len(td.TypeParams) > 0 {
				continue
			}
			st, ok := mod.Types[td.Name].(*types.Struct)
			if !ok {
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
		if v.PayloadType == nil {
			en.Variants = append(en.Variants, types.EnumVariant{
				Name:  v.Name,
				CName: vcname,
				Value: tagVal,
				Kind:  types.VariantUnit,
			})
			tagVal++
			continue
		}
		pty := a.resolveValueType(mod, v.PayloadType, subst, false)
		en.Variants = append(en.Variants, types.EnumVariant{
			Name:    v.Name,
			CName:   vcname,
			Value:   tagVal,
			Kind:    types.VariantTuple,
			Payload: pty,
		})
		tagVal++
	}
}

func (a *Analyzer) populateStructFields(mod *Module, st *types.Struct, td *ast.StructDecl, subst map[string]types.Type) {
	seen := make(map[string]bool)
	for _, f := range td.Fields {
		if seen[f.Name] {
			a.Rep.Errorf(f.Span, "duplicate field %q in struct %s", f.Name, st.String())
			continue
		}
		seen[f.Name] = true
		fty := a.resolveValueType(mod, f.Type, subst, false)
		a.checkOptionalIdentRule(f.Name, f.IsOpt, fty, f.Span, "struct field")
		cname := types.SanitizeCIdent(f.Name)
		if st.Name == "List" || strings.HasPrefix(st.Name, "List[") || st.Name == "Array" || strings.HasPrefix(st.Name, "Array[") {
			cname = f.Name
		}
		st.Fields = append(st.Fields, types.StructField{
			Name:       f.Name,
			CName:      cname,
			Type:       fty,
			HasDefault: f.Default != nil,
		})
	}
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
	_, isOptType := ty.(*types.Optional)
	if isOptType && !isOptName {
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

// resolveValueType resolves a TypeExpr in an owned/value position (forbidding `&T` and `own T`).
func (a *Analyzer) resolveValueType(mod *Module, te ast.TypeExpr, subst map[string]types.Type, allowSelf bool) types.Type {
	if te == nil {
		return types.TypeVoid
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

	case *ast.StackArrayType:
		sz, ok := a.evalConstInt(mod, v.Size)
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
			if !types.IsHashable(elem) && !a.inBodyPass {
				if a.Prog.IfaceHashable != nil {
					a.deferredIfaceChecks = append(a.deferredIfaceChecks, deferredIfaceCheck{
						Concrete: elem,
						Iface:    a.Prog.IfaceHashable,
						Span:     v.Elem.GetSpan(),
						Context:  "set element type",
					})
				}
			} else {
				isH := types.IsHashable(elem) || (a.Prog.IfaceHashable != nil && a.ImplementsInterface(elem, a.Prog.IfaceHashable))
				if !isH {
					a.Rep.Errorf(v.Elem.GetSpan(), "set element type %s is not hashable", elem.String())
					return types.TypeInvalid
				}
			}
		}
		return &types.Set{Elem: elem}

	case *ast.MapType:
		kt := a.resolveValueType(mod, v.Key, subst, allowSelf)
		vt := a.resolveValueType(mod, v.Value, subst, allowSelf)
		if kt == types.TypeInvalid || vt == types.TypeInvalid {
			return types.TypeInvalid
		}
		if _, isTP := kt.(*types.TypeParamVar); !isTP {
			if !types.IsHashable(kt) && !a.inBodyPass {
				if a.Prog.IfaceHashable != nil {
					a.deferredIfaceChecks = append(a.deferredIfaceChecks, deferredIfaceCheck{
						Concrete: kt,
						Iface:    a.Prog.IfaceHashable,
						Span:     v.Key.GetSpan(),
						Context:  "map key type",
					})
				}
			} else {
				isH := types.IsHashable(kt) || (a.Prog.IfaceHashable != nil && a.ImplementsInterface(kt, a.Prog.IfaceHashable))
				if !isH {
					a.Rep.Errorf(v.Key.GetSpan(), "map key type %s is not hashable", kt.String())
					return types.TypeInvalid
				}
			}
		}
		return &types.Map{Key: kt, Value: vt}

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
		return &types.Func{Params: ps, Ret: ret}

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
				if len(v.TypeArgs) > 0 {
					a.Rep.Errorf(v.Span, "type parameter %s cannot take type arguments", v.Name)
					return types.TypeInvalid
				}
				return subTy
			}
		}

		// Array[N, T] syntactic sugar unification with [N: T]
		if (v.Name == "Array" && (v.Module == "" || v.Module == "core" || v.Module == "array")) && len(v.TypeArgs) == 2 {
			sz, ok := a.evalConstIntFromTypeExpr(mod, v.TypeArgs[0])
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

		// Generic struct instantiation `Pair[int, string]`
		if genSt := a.lookupGenStructInModule(mod, v.Module, v.Name); genSt != nil {
			if len(v.TypeArgs) != len(genSt.Decl.TypeParams) {
				a.Rep.Errorf(v.Span, "generic struct %s expects %d type argument(s), got %d",
					v.Name, len(genSt.Decl.TypeParams), len(v.TypeArgs))
				return types.TypeInvalid
			}
			targs := make([]types.Type, len(v.TypeArgs))
			for i, ta := range v.TypeArgs {
				targs[i] = a.resolveValueType(mod, ta, subst, allowSelf)
				if targs[i] == types.TypeInvalid {
					return types.TypeInvalid
				}
			}
			stInst := a.instantiateStruct(genSt, targs, v.Span)
			if stInst == nil {
				return types.TypeInvalid
			}
			return stInst
		}

		// Generic enum instantiation `Result[int, string]`
		if genEn := a.lookupGenEnumInModule(mod, v.Module, v.Name); genEn != nil {
			if len(v.TypeArgs) != len(genEn.Decl.TypeParams) {
				a.Rep.Errorf(v.Span, "generic enum %s expects %d type argument(s), got %d",
					v.Name, len(genEn.Decl.TypeParams), len(v.TypeArgs))
				return types.TypeInvalid
			}
			targs := make([]types.Type, len(v.TypeArgs))
			for i, ta := range v.TypeArgs {
				targs[i] = a.resolveValueType(mod, ta, subst, allowSelf)
				if targs[i] == types.TypeInvalid {
					return types.TypeInvalid
				}
			}
			enInst := a.instantiateEnum(genEn, targs, v.Span)
			if enInst == nil {
				return types.TypeInvalid
			}
			return enInst
		}

		// Generic interface instantiation `Iterator[int]`
		if genIfc := a.lookupGenInterfaceInModule(mod, v.Module, v.Name); genIfc != nil {
			if len(v.TypeArgs) != len(genIfc.Decl.TypeParams) {
				a.Rep.Errorf(v.Span, "generic interface %s expects %d type argument(s), got %d",
					v.Name, len(genIfc.Decl.TypeParams), len(v.TypeArgs))
				return types.TypeInvalid
			}
			targs := make([]types.Type, len(v.TypeArgs))
			for i, ta := range v.TypeArgs {
				targs[i] = a.resolveValueType(mod, ta, subst, allowSelf)
				if targs[i] == types.TypeInvalid {
					return types.TypeInvalid
				}
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
		if ifc, isIfc := found.(*types.Interface); isIfc && ifc.UsesSelf {
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
	}

	// Verify interface bounds when concrete!
	subst := make(map[string]types.Type, len(typeArgs))
	for i, tp := range tmpl.Decl.TypeParams {
		subst[tp.Name] = typeArgs[i]
	}

	cname := fmt.Sprintf("ll_gs_%s__%s", tmpl.Decl.Name, key)
	if tag := a.moduleTag(tmpl.Mod); tag != "" {
		cname = fmt.Sprintf("ll_gs_%s_%s__%s", tag, tmpl.Decl.Name, key)
	}
	inst := &types.Struct{
		Name:     tmpl.Decl.Name,
		CName:    cname,
		PkgDir:   tmpl.Mod.Dir,
		Packed:   tmpl.Decl.Packed || ast.FindAttribute(tmpl.Decl.Attributes, "packed") != nil,
		TypeArgs: typeArgs,
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
	}

	subst := make(map[string]types.Type, len(typeArgs))
	for i, tp := range tmpl.Decl.TypeParams {
		subst[tp.Name] = typeArgs[i]
	}

	cname := fmt.Sprintf("ll_giface_%s__%s", tmpl.Decl.Name, key)
	if tag := a.moduleTag(tmpl.Mod); tag != "" {
		cname = fmt.Sprintf("ll_giface_%s_%s__%s", tag, tmpl.Decl.Name, key)
	}
	inst := &types.Interface{
		Name:   fmt.Sprintf("%s[%s]", tmpl.Decl.Name, typeListString(typeArgs)),
		CName:  cname,
		PkgDir: tmpl.Mod.Dir,
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
			if _, isSelf := pty.(*types.SelfPlaceholder); isSelf {
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
			if _, isSelf := ret.(*types.SelfPlaceholder); isSelf {
				inst.UsesSelf = true
			}
		}
		inst.Methods = append(inst.Methods, types.InterfaceMethod{
			Name:     m.Name,
			RecvMode: recvModeToParamMode(m.RecvMode),
			Params:   fparams,
			Ret:      ret,
		})
	}
	return inst
}

func (a *Analyzer) isCollectableType(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	switch v := t.(type) {
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
	switch v := t.(type) {
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
	switch c := t.(type) {
	case *types.Vector, *types.StackArray, *types.Set, *types.Map:
		return true
	case *types.Primitive:
		return c.Kind == types.PrimString
	case *types.Distinct:
		return a.isIterableType(c.Base)
	case *types.Struct:
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
	switch c := t.(type) {
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
	switch v := t.(type) {
	case *types.Map:
		return true
	case *types.Struct:
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
	switch v := t.(type) {
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
	switch v := t.(type) {
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
	}

	subst := make(map[string]types.Type, len(typeArgs))
	for i, tp := range tmpl.Decl.TypeParams {
		subst[tp.Name] = typeArgs[i]
	}

	cname := fmt.Sprintf("ll_ge_%s__%s", tmpl.Decl.Name, key)
	if tag := a.moduleTag(tmpl.Mod); tag != "" {
		cname = fmt.Sprintf("ll_ge_%s_%s__%s", tag, tmpl.Decl.Name, key)
	}
	inst := &types.Enum{
		Name:       tmpl.Decl.Name,
		CName:      cname,
		PkgDir:     tmpl.Mod.Dir,
		IsTagged:   true,
		TypeArgs:   typeArgs,
		Underlying: types.TypeInt,
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
		}
	}
	return inst
}

func (a *Analyzer) registerFunctionsAndMethods(mod *Module) {
	hdrSeen := make(map[string]bool)
	for _, h := range a.Prog.ExternHeaders {
		hdrSeen[h] = true
	}

	for _, d := range mod.File.Decls {
		switch decl := d.(type) {
		case *ast.ExternHeaderDecl:
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
			ef := &ExternFunc{Name: decl.Name, Sig: sig, Span: decl.Span}
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
				Span:       decl.Span,
			}

		case *ast.VarDecl:
			if langAttr := ast.FindAttribute(decl.Attributes, "lang"); langAttr != nil {
				if len(langAttr.Args) == 0 {
					a.Rep.Errorf(langAttr.Span, "@lang attribute requires a string argument (e.g. @lang(\"panic\"))")
				} else {
					kind := strings.Trim(langAttr.Args[0], "\"")
					switch kind {
					case "panic", "assert":
						// valid function lang item
					default:
						a.Rep.Errorf(langAttr.Span, "unknown function lang item @lang(%q); supported function lang items: \"panic\", \"assert\"", kind)
					}
				}
			}
			if len(decl.TypeParams) > 0 {
				if !decl.IsBind {
					a.Rep.Errorf(decl.Span, "generic functions must be declared with `bind`")
					continue
				}
				if _, ok := decl.Value.(*ast.FuncLit); !ok {
					a.Rep.Errorf(decl.Span, "generic top-level `bind` must be a function")
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

			// Top-level static function `bind name = (params)[: Ret] body`
			if fl, isFn := decl.Value.(*ast.FuncLit); isFn && decl.IsBind {
				if a.checkDupDecl(mod, decl.Name, decl.Span) {
					continue
				}
				fi := a.buildFuncInstanceFromLit(mod, decl.Name, fl, nil)
				fi.DeclSpan = decl.Span
				a.checkAttributes(decl.Attributes, fi, decl.Span, "function")
				a.Prog.Funcs = append(a.Prog.Funcs, fi)
				sym := &Symbol{
					Name:          decl.Name,
					CName:         fi.CName,
					Type:          fi.Sig,
					IsBind:        true,
					BindKind:      BindOwned,
					IsTopLevel:    true,
					Mod:           mod,
					FuncInst:      fi,
					Span:          decl.Span,
					MustUse:       fi.MustUse,
					Deprecated:    fi.Deprecated,
					DeprecatedMsg: fi.DeprecatedMsg,
					IsTest:        fi.IsTest,
					IsTestOnly:    fi.IsTestOnly,
					TrackCaller:   fi.TrackCaller,
					TestDesc:      fi.TestDesc,
				}
				mod.Symbols[decl.Name] = sym
				if mod == a.Prog.EntryMod && decl.Name == "main" {
					fi.IsMain = true
					a.validateMainSig(fi, decl.Span)
					a.Prog.MainFunc = fi
				}
				if mod == a.Prog.EntryMod && decl.Name == "test_main" {
					fi.IsTestMain = true
					a.validateTestMainSig(fi, decl.Span)
					a.Prog.TestMainFunc = fi
				}
				continue
			}

			// Top-level constant / variable
			if !decl.IsBind {
				a.Rep.ErrorWithHint(
					decl.Span,
					"mutable global `let` variables are not allowed at top level",
					"use `bind` for top-level constants and functions, and pass mutable state explicitly with `&`",
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
					BindKind:   BindOwned,
					IsTopLevel: true,
					Mod:        mod,
					Span:       decl.Span,
				}
				a.checkAttributes(decl.Attributes, sym, decl.Span, "variable")
				mod.Symbols[decl.Name] = sym
			} else {
				a.checkAttributes(decl.Attributes, sym, decl.Span, "variable")
			}
			a.Prog.TopVars = append(a.Prog.TopVars, &TopVarInit{Sym: sym, Decl: decl, Mod: mod})

		case *ast.MethodDecl:
			if len(decl.RecvTypeParams) > 0 || len(decl.TypeParams) > 0 {
				if a.genMethods[decl.RecvType] == nil {
					a.genMethods[decl.RecvType] = make(map[string]*GenericFuncTemplate)
				}
				a.genMethods[decl.RecvType][decl.Name] = &GenericFuncTemplate{
					Name:       decl.RecvType + "." + decl.Name,
					MethodDecl: decl,
					Mod:        mod,
					Instances:  make(map[string]*FuncInstance),
				}
				continue
			}

			recvTy := a.lookupTypeInModule(mod, "", decl.RecvType)
			if recvTy == nil {
				a.Rep.Errorf(decl.Span, "unknown receiver type %q in method declaration", decl.RecvType)
				continue
			}
			fi := a.buildMethodInstance(mod, recvTy, decl, nil)
			key := TypeMethodKey(recvTy)
			if a.methods[key] == nil {
				a.methods[key] = make(map[string]*FuncInstance)
			}
			if _, dup := a.methods[key][decl.Name]; dup {
				a.Rep.Errorf(decl.Span, "duplicate method `%s.%s`", decl.RecvType, decl.Name)
				continue
			}
			a.methods[key][decl.Name] = fi
			if a.Prog.MethodTable[key] == nil {
				a.Prog.MethodTable[key] = make(map[string]*FuncInstance)
			}
			a.Prog.MethodTable[key][decl.Name] = fi
			a.Prog.Funcs = append(a.Prog.Funcs, fi)

			// Check if this is `Drop` (`bind MyStruct.drop = (&self)` or `bind MyEnum.drop = (&self)`)
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
		a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "parameter")
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
	if name == "main" && mod == a.Prog.EntryMod {
		cname = "ll_user_main"
	} else if tag := a.moduleTag(mod); tag != "" {
		cname = fmt.Sprintf("ll_fn_%s_%s", tag, sanitizeSymbolName(name))
	}
	return &FuncInstance{
		Name:        name,
		CName:       cname,
		Sig:         &types.Func{Params: ps, Ret: ret},
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
		a.checkOptionalIdentRule(p.Name, p.IsOptName, pt, p.Span, "parameter")
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
	}
	a.checkAttributes(decl.Attributes, fi, decl.Span, "method")
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

// LookupMethod finds a user-defined (or monomorphized) method on `recvTy`.
func (a *Analyzer) LookupMethod(recvTy types.Type, methodName string, methodTypeArgs []types.Type, span diag.Span) *FuncInstance {
	key := TypeMethodKey(recvTy)
	if mset, ok := a.methods[key]; ok {
		if fi, ok2 := mset[methodName]; ok2 {
			return fi
		}
	}

	// Check generic method templates (`bind Pair[A, B].swap = ...` or `bind Foo.bar[T] = ...`)
	baseTypeName := ""
	var recvArgs []types.Type
	if st, isSt := recvTy.(*types.Struct); isSt {
		baseTypeName = st.Name
		recvArgs = st.TypeArgs
	} else if vec, isVec := recvTy.(*types.Vector); isVec {
		baseTypeName = "List"
		recvArgs = []types.Type{vec.Elem}
	} else if arr, isArr := recvTy.(*types.StackArray); isArr {
		baseTypeName = "Array"
		recvArgs = []types.Type{types.TypeInt, arr.Elem}
	} else if p, isP := recvTy.(*types.Primitive); isP {
		baseTypeName = p.String()
	} else if d, isD := recvTy.(*types.Distinct); isD {
		baseTypeName = d.Name
	} else if e, isE := recvTy.(*types.Enum); isE {
		baseTypeName = e.Name
	}
	if baseTypeName != "" {
		if gset, ok := a.genMethods[baseTypeName]; ok {
			if gtmpl, ok2 := gset[methodName]; ok2 {
				return a.instantiateMethod(gtmpl, recvTy, methodTypeArgs, span)
			}
		}
		_ = recvArgs
	}
	return nil
}

func (a *Analyzer) instantiateMethod(tmpl *GenericFuncTemplate, recvTy types.Type, methodTypeArgs []types.Type, span diag.Span) *FuncInstance {
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
	instKey := strings.Join(keyParts, "__")
	if existing, ok := tmpl.Instances[instKey]; ok {
		return existing
	}

	fi := a.buildMethodInstance(tmpl.Mod, recvTy, decl, subst)
	tmpl.Instances[instKey] = fi
	if len(methodTypeArgs) == 0 {
		mkey := TypeMethodKey(recvTy)
		if a.Prog.MethodTable[mkey] == nil {
			a.Prog.MethodTable[mkey] = make(map[string]*FuncInstance)
		}
		a.Prog.MethodTable[mkey][decl.Name] = fi
	}
	a.Prog.Funcs = append(a.Prog.Funcs, fi)
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
	for i, tp := range decl.TypeParams {
		if !a.verifyNonOptionalTypeArg(tp.Name, typeArgs[i], span, decl.Name) {
			return nil
		}
		subst[tp.Name] = typeArgs[i]
		parts[i] = typeArgs[i].Mangled()
		a.verifyBounds(tmpl.Mod, tp, typeArgs[i], span, decl.Name)
	}
	key := strings.Join(parts, "__")
	if existing, ok := tmpl.Instances[key]; ok {
		return existing
	}

	fl := decl.Value.(*ast.FuncLit)
	instName := fmt.Sprintf("%s[%s]", decl.Name, strings.Join(parts, ","))
	fi := a.buildFuncInstanceFromLit(tmpl.Mod, instName, fl, subst)
	fi.DeclSpan = decl.Span
	cname := fmt.Sprintf("ll_gfn_%s__%s", decl.Name, key)
	if tag := a.moduleTag(tmpl.Mod); tag != "" {
		cname = fmt.Sprintf("ll_gfn_%s_%s__%s", tag, decl.Name, key)
	}
	fi.CName = cname
	a.checkAttributes(decl.Attributes, fi, decl.Span, "function")
	tmpl.Instances[key] = fi
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
		if types.IsHashable(concrete) {
			return true
		}
		if a.LookupMethod(concrete, "hash", nil, diag.Span{}) != nil {
			return true
		}
		return false
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
		switch c := concrete.(type) {
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
	if a.LookupMethod(t, "to_string", nil, diag.Span{}) != nil {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind != types.PrimVoid && v.Kind != types.PrimArena
	case *types.Enum:
		if !v.IsTagged || seen[v] {
			return true
		}
		seen[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil && !a.supportsToStringVisited(vr.Payload, seen) {
				return false
			}
		}
		return true
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
	if a.LookupMethod(t, "copy", nil, diag.Span{}) != nil {
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
	case *types.Enum:
		if v.HasCustomDrop {
			return false
		}
		if !v.IsTagged || seen[v] {
			return true
		}
		seen[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil && !a.supportsCopyVisited(vr.Payload, seen) {
				return false
			}
		}
		return true
	case *types.Struct:
		if v.HasCustomDrop {
			// Structs with custom Drop only copy if user explicitly defines `.copy()`
			return false
		}
		if seen[v] {
			return true
		}
		seen[v] = true
		for _, f := range v.Fields {
			if !a.supportsCopyVisited(f.Type, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (a *Analyzer) supportsEq(t types.Type) bool {
	if t == nil || t == types.TypeInvalid {
		return false
	}
	if opt, isOpt := t.(*types.Optional); isOpt {
		return a.supportsEq(opt.Elem)
	}
	if types.SupportsEq(t) {
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
	if a.LookupMethod(t, "cmp", nil, diag.Span{}) != nil {
		return true
	}
	if d, isDist := t.(*types.Distinct); isDist {
		return a.supportsCmp(d.Base)
	}
	if tup, isTup := t.(*types.Tuple); isTup {
		for _, el := range tup.Elems {
			if !a.supportsCmp(el) {
				return false
			}
		}
		return true
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
