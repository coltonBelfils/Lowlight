package sema

import (
	"fmt"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/parser"
	"lowlight/internal/types"
)

// `@derive(Equatable, Comparable, Hashable, Copyable, Printable)` on a struct or enum writes the
// interface's method in Lowlight. The method is generated as source text, parsed, and checked
// exactly like a hand-written one, so `lowlight` treats it no differently. Structs and enums have
// no other source of these methods: without `@derive` or a hand-written method they don't
// support `==`, `<`, hashing, `.copy()` (unless plain), or interpolation.

// derivableMethods maps each derivable interface to the method it provides.
var derivableMethods = map[string]string{
	"Equatable":  "eq",
	"Comparable": "cmp",
	"Hashable":   "hash",
	"Copyable":   "copy",
	"Printable":  "to_string",
}

// deriveInfo records where a derived method came from.
type deriveInfo struct {
	Iface    string
	TypeName string
	Span     diag.Span // the `@derive(...)` attribute
	Mod      *Module
}

// expandDerives generates the methods requested by `@derive` attributes in mod. They are kept in
// mod.DerivedDecls rather than added to the (cached, shared) parsed file.
func (a *Analyzer) expandDerives(mod *Module) {
	mod.DerivedDecls = nil
	written := make(map[string]bool) // "Type.method" for methods written by hand in mod
	for _, d := range mod.File.Decls {
		if md, ok := d.(*ast.MethodDecl); ok && md.RecvModule == "" {
			written[md.RecvType+"."+md.Name] = true
		}
	}
	for _, d := range mod.File.Decls {
		var attrs []ast.Attribute
		var name string
		var gen func(iface string) string
		switch td := d.(type) {
		case *ast.StructDecl:
			attrs, name = td.Attributes, td.Name
			gen = func(iface string) string { return deriveStructSource(td, iface) }
		case *ast.EnumDecl:
			attrs, name = td.Attributes, td.Name
			gen = func(iface string) string { return deriveEnumSource(td, iface) }
		default:
			continue
		}
		for _, attr := range attrs {
			if attr.Name != "derive" {
				continue
			}
			if len(attr.Args) == 0 {
				a.Rep.Errorf(attr.Span, "@derive needs at least one interface, e.g. @derive(Equatable, Hashable)")
				continue
			}
			for _, iface := range attr.Args {
				method, ok := derivableMethods[iface]
				if !ok {
					a.Rep.Errorf(attr.Span, "cannot derive %q; derivable interfaces: Equatable, Comparable, Hashable, Copyable, Printable", iface)
					continue
				}
				if written[name+"."+method] {
					a.Rep.Errorf(attr.Span, "%s derives %s but also defines `%s` by hand; remove one", name, iface, method)
					continue
				}
				src := gen(iface)
				file := fmt.Sprintf("%s (@derive(%s) for %s)", mod.Path, iface, name)
				f := parser.ParseFile(file, src, a.Rep)
				for _, gd := range f.Decls {
					if md, ok := gd.(*ast.MethodDecl); ok {
						md.Doc = fmt.Sprintf("Derived by `@derive(%s)`.", iface)
						a.derived[md] = &deriveInfo{Iface: iface, TypeName: name, Span: attr.Span, Mod: mod}
						a.derivedOrder = append(a.derivedOrder, md)
					}
					mod.DerivedDecls = append(mod.DerivedDecls, gd)
				}
			}
		}
	}
}

// selfType is how generated code names the type: `P`, or `Pair[A, B]` for a generic one.
func selfType(name string, tps []ast.TypeParam) string {
	if len(tps) == 0 {
		return name
	}
	ns := make([]string, len(tps))
	for i, tp := range tps {
		ns[i] = tp.Name
	}
	return name + "[" + strings.Join(ns, ", ") + "]"
}

// fieldRef is how generated code names a field: optional fields keep their `?`.
func fieldRef(f ast.StructField) string {
	if f.IsOpt && !strings.HasSuffix(f.Name, "?") {
		return f.Name + "?"
	}
	return f.Name
}

// FNV-1a style combining keeps derived hashes order-sensitive and cheap.
const (
	deriveHashSeed  = "14695981039346656037"
	deriveHashPrime = "1099511628211"
)

func deriveStructSource(sd *ast.StructDecl, iface string) string {
	T := selfType(sd.Name, sd.TypeParams)
	var b strings.Builder
	switch iface {
	case "Equatable":
		fmt.Fprintf(&b, "bind %s.eq = (self, other: %s) bool: ", T, T)
		if len(sd.Fields) == 0 {
			b.WriteString("true\n")
			break
		}
		parts := make([]string, len(sd.Fields))
		for i, f := range sd.Fields {
			parts[i] = fmt.Sprintf("self.%s == other.%s", fieldRef(f), fieldRef(f))
		}
		b.WriteString(strings.Join(parts, " and ") + "\n")
	case "Comparable":
		// Lexicographic, in field order.
		fmt.Fprintf(&b, "bind %s.cmp = (self, other: %s) int: {\n", T, T)
		for _, f := range sd.Fields {
			r := fieldRef(f)
			fmt.Fprintf(&b, "\tif(self.%s < other.%s): func <- -1\n\tif(self.%s > other.%s): func <- 1\n", r, r, r, r)
		}
		b.WriteString("\t0\n}\n")
	case "Hashable":
		fmt.Fprintf(&b, "bind %s.hash = (self) u64: {\n\tlet h: u64 = %s\n", T, deriveHashSeed)
		for _, f := range sd.Fields {
			fmt.Fprintf(&b, "\th = (h ^ self.%s.hash()) * %s\n", fieldRef(f), deriveHashPrime)
		}
		b.WriteString("\th\n}\n")
	case "Copyable":
		inits := make([]string, len(sd.Fields))
		for i, f := range sd.Fields {
			inits[i] = fmt.Sprintf("%s: self.%s.copy()", fieldRef(f), fieldRef(f))
		}
		fmt.Fprintf(&b, "bind %s.copy = (self) %s: %s[%s]\n", T, T, T, strings.Join(inits, ", "))
	case "Printable":
		// Printed like a struct literal: `Point[x: 1, y: 2]`.
		parts := make([]string, len(sd.Fields))
		for i, f := range sd.Fields {
			parts[i] = fmt.Sprintf("%s: ${self.%s}", fieldRef(f), fieldRef(f))
		}
		fmt.Fprintf(&b, "bind %s.to_string = (self) string: \"%s[%s]\"\n", T, sd.Name, strings.Join(parts, ", "))
	}
	return b.String()
}

func deriveEnumSource(ed *ast.EnumDecl, iface string) string {
	T := selfType(ed.Name, ed.TypeParams)
	tagged := len(ed.TypeParams) > 0 || ed.IsResult
	for _, v := range ed.Variants {
		if v.PayloadType != nil {
			tagged = true
		}
	}
	var b strings.Builder
	if !tagged {
		// A tag-only enum is its integer value.
		switch iface {
		case "Equatable":
			fmt.Fprintf(&b, "bind %s.eq = (self, other: %s) bool: int(self) == int(other)\n", T, T)
		case "Comparable":
			fmt.Fprintf(&b, "bind %s.cmp = (self, other: %s) int: int(self).cmp(int(other))\n", T, T)
		case "Hashable":
			fmt.Fprintf(&b, "bind %s.hash = (self) u64: int(self).hash()\n", T)
		case "Copyable":
			fmt.Fprintf(&b, "bind %s.copy = (self) %s: self\n", T, T)
		case "Printable":
			fmt.Fprintf(&b, "bind %s.to_string = (self) string: match(self):\n", T)
			for i, v := range ed.Variants {
				kw := "when"
				if i == len(ed.Variants)-1 {
					kw = "default"
				}
				fmt.Fprintf(&b, "\t%s %s.%s: \"%s.%s\"\n", kw, T, v.Name, ed.Name, v.Name)
			}
		}
		return b.String()
	}

	// Tagged enums: one arm per variant. The last arm is `default <pattern>:`, which makes the
	// match exhaustive. `pat(v, x)` matches variant v, binding its payload to x.
	pat := func(v ast.EnumVariant, bind string) string {
		if v.PayloadType != nil {
			return fmt.Sprintf("%s.%s(%s)", T, v.Name, bind)
		}
		return T + "." + v.Name
	}
	n := len(ed.Variants)
	kw := func(i int) string {
		if i == n-1 {
			return "default"
		}
		return "when"
	}
	// arms writes `match(self):` arms at indent; body returns the arm's expression, which may
	// itself be a nested match written with nested().
	arms := func(indent string, body func(i int, v ast.EnumVariant) string) {
		for i, v := range ed.Variants {
			fmt.Fprintf(&b, "%s%s %s: %s\n", indent, kw(i), pat(v, "a"), body(i, v))
		}
	}
	// nested returns `match(other):` that yields onMatch when other is also variant v.
	nested := func(indent string, v ast.EnumVariant, onMatch, otherwise string) string {
		if n == 1 {
			return fmt.Sprintf("match(other):\n%s\tdefault %s: %s", indent, pat(v, "b"), onMatch)
		}
		return fmt.Sprintf("match(other):\n%s\twhen %s: %s\n%s\tdefault: %s", indent, pat(v, "b"), onMatch, indent, otherwise)
	}
	switch iface {
	case "Equatable":
		fmt.Fprintf(&b, "bind %s.eq = (self, other: %s) bool: match(self):\n", T, T)
		arms("\t", func(i int, v ast.EnumVariant) string {
			if v.PayloadType != nil {
				return nested("\t", v, "a == b", "false")
			}
			return nested("\t", v, "true", "false")
		})
	case "Comparable":
		// Variants order by declaration; equal variants by payload.
		fmt.Fprintf(&b, "bind %s._derive_index = (self) int: match(self):\n", T)
		arms("\t", func(i int, v ast.EnumVariant) string { return fmt.Sprint(i) })
		fmt.Fprintf(&b, "bind %s.cmp = (self, other: %s) int: {\n", T, T)
		b.WriteString("\tlet ia = self._derive_index()\n\tlet ib = other._derive_index()\n")
		b.WriteString("\tif(ia != ib): func <- if(ia < ib): -1 else: 1\n\tmatch(self):\n")
		arms("\t\t", func(i int, v ast.EnumVariant) string {
			if v.PayloadType != nil {
				return nested("\t\t", v, "if(a < b): -1 else if(a > b): 1 else: 0", "0")
			}
			return "0"
		})
		b.WriteString("}\n")
	case "Hashable":
		fmt.Fprintf(&b, "bind %s.hash = (self) u64: match(self):\n", T)
		arms("\t", func(i int, v ast.EnumVariant) string {
			seed := fmt.Sprintf("u64(%d).hash()", i)
			if v.PayloadType != nil {
				return fmt.Sprintf("(%s ^ a.hash()) * %s", seed, deriveHashPrime)
			}
			return seed
		})
	case "Copyable":
		fmt.Fprintf(&b, "bind %s.copy = (self) %s: match(self):\n", T, T)
		arms("\t", func(i int, v ast.EnumVariant) string {
			if v.PayloadType != nil {
				return fmt.Sprintf("%s.%s(a.copy())", T, v.Name)
			}
			return T + "." + v.Name
		})
	case "Printable":
		fmt.Fprintf(&b, "bind %s.to_string = (self) string: match(self):\n", T)
		arms("\t", func(i int, v ast.EnumVariant) string {
			if v.PayloadType != nil {
				return fmt.Sprintf("\"%s.%s(${a})\"", ed.Name, v.Name)
			}
			return fmt.Sprintf("\"%s.%s\"", ed.Name, v.Name)
		})
	}
	return b.String()
}

// deriveFieldsOK reports whether every field (or variant payload) of t supports what iface
// needs, returning the first one that doesn't.
func (a *Analyzer) deriveFieldsOK(iface string, t types.Type) (bool, string, types.Type) {
	var names []string
	var tys []types.Type
	switch v := t.(type) {
	case *types.Struct:
		for _, f := range v.Fields {
			names = append(names, "field `"+f.Name+"`")
			tys = append(tys, f.Type)
		}
	case *types.Enum:
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil {
				names = append(names, "variant `"+vr.Name+"`")
				tys = append(tys, vr.Payload)
			}
		}
	}
	for i, ft := range tys {
		if !a.supportsIface(iface, ft) {
			return false, names[i], ft
		}
	}
	return true, "", nil
}

func (a *Analyzer) supportsIface(iface string, t types.Type) bool {
	switch iface {
	case "Equatable":
		return a.supportsEq(t)
	case "Comparable":
		return a.supportsCmp(t)
	case "Hashable":
		return a.supportsHash(t)
	case "Copyable":
		return a.supportsCopy(t)
	case "Printable":
		return a.supportsToString(t)
	}
	return false
}

// deriveApplies reports whether a derived method of a generic type exists for the instance
// recvTy: `@derive(Equatable)` on `Box[T]` gives `Box[int].eq` but not `Box[File].eq` when
// `File` has no `==`.
func (a *Analyzer) deriveApplies(info *deriveInfo, recvTy types.Type) bool {
	var targs []types.Type
	switch v := recvTy.(type) {
	case *types.Struct:
		targs = v.TypeArgs
	case *types.Enum:
		targs = v.TypeArgs
	}
	for _, ta := range targs {
		if _, isTP := ta.(*types.TypeParamVar); isTP {
			return true
		}
	}
	key := info.Iface + "|" + recvTy.Mangled()
	if a.deriveChecking[key] {
		return true // recursive type: assume yes while deciding
	}
	a.deriveChecking[key] = true
	defer delete(a.deriveChecking, key)
	ok, _, _ := a.deriveFieldsOK(info.Iface, recvTy)
	return ok
}

// validateDerives reports a derived method of a non-generic type whose fields can't support it,
// at the `@derive` attribute, and skips checking its body (which would only repeat the error).
func (a *Analyzer) validateDerives() {
	for _, md := range a.derivedOrder {
		info := a.derived[md]
		if len(md.RecvTypeParams) > 0 {
			continue
		}
		recvTy := a.lookupTypeInModule(info.Mod, "", md.RecvType)
		if recvTy == nil {
			continue
		}
		fi := a.methods[TypeMethodKey(recvTy)][md.Name]
		if fi == nil || md.Name == "_derive_index" {
			continue
		}
		key := info.Iface + "|" + recvTy.Mangled()
		a.deriveChecking[key] = true
		ok, what, ft := a.deriveFieldsOK(info.Iface, recvTy)
		delete(a.deriveChecking, key)
		if !ok {
			a.Rep.Errorf(info.Span, "cannot derive %s for %s: %s (%s) does not implement %s", info.Iface, info.TypeName, what, ft.String(), info.Iface)
			fi.Checked = true
		}
	}
}

// supportsHash reports whether t can be hashed (`Hashable`).
func (a *Analyzer) supportsHash(t types.Type) bool {
	switch v := t.(type) {
	case *types.Tuple:
		for _, el := range v.Elems {
			if !a.supportsHash(el) {
				return false
			}
		}
		return true
	case *types.Distinct:
		if a.LookupMethod(t, "hash", nil, diag.Span{}) != nil {
			return true
		}
		return a.supportsHash(v.Base)
	}
	return types.IsHashable(t) || a.LookupMethod(t, "hash", nil, diag.Span{}) != nil
}
