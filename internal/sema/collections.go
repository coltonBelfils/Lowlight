package sema

import (
	"lowlight/internal/diag"
	"lowlight/internal/types"
)

// operatorMethod returns t's method for an overloadable operator, when it has the shape the
// operator's interface requires: `add: fn(self, other: Self) Self` for `+`, or
// `add_assign: fn(&self, other: Self)` for `+=`.
func (a *Analyzer) operatorMethod(t types.Type, name string, span diag.Span) *FuncInstance {
	if _, isTP := t.(*types.TypeParamVar); isTP {
		return nil
	}
	fi := a.LookupMethod(t, name, nil, span)
	if fi == nil || fi.IsStaticAssoc || len(fi.Sig.Params) != 2 || !fi.Sig.Params[1].Type.Equals(t) {
		return nil
	}
	ret := a.ensureFuncRetResolved(fi, span)
	if name == "add_assign" {
		if fi.Sig.Params[0].Mode != types.ModeMut || (ret != nil && !ret.Equals(types.TypeVoid)) {
			return nil
		}
	} else if fi.Sig.Params[0].Mode != types.ModeRead || ret == nil || !ret.Equals(t) {
		return nil
	}
	return fi
}

// typeParamHasBound reports whether t is a type parameter bounded by ifc.
func (a *Analyzer) typeParamHasBound(t types.Type, ifc *types.Interface) bool {
	tp, ok := t.(*types.TypeParamVar)
	if !ok || ifc == nil {
		return false
	}
	for _, b := range tp.Bounds {
		if b == ifc || b.Name == ifc.Name {
			return true
		}
	}
	return false
}

// setType returns the set type `#[elem]`: the std/core `Set[elem]` struct.
func (a *Analyzer) setType(elem types.Type, span diag.Span) types.Type {
	if a.Prog.GenStructSet == nil {
		a.Rep.Errorf(span, "set type `#[%s]` requires std/core `Set` (`@lang(\"set\")`)", elem.String())
		return types.TypeInvalid
	}
	a.requireEquatable(elem, span, "set element type")
	st := a.instantiateStruct(a.Prog.GenStructSet, []types.Type{elem}, span)
	if st == nil {
		return types.TypeInvalid
	}
	return st
}

// requireEquatable reports, at the place a set or map type is written, an element or key type
// that has no `==` (sets and maps compare keys after hashing them). Before methods exist (while
// signatures and fields are resolved) the check waits for the body pass.
func (a *Analyzer) requireEquatable(t types.Type, span diag.Span, context string) {
	if _, isTP := t.(*types.TypeParamVar); isTP || t == types.TypeInvalid || a.Prog.IfaceEquatable == nil {
		return
	}
	if !a.inBodyPass {
		a.deferredIfaceChecks = append(a.deferredIfaceChecks, deferredIfaceCheck{Concrete: t, Iface: a.Prog.IfaceEquatable, Span: span, Context: context})
		return
	}
	if !a.supportsEq(t) {
		a.Rep.Errorf(span, "type %s does not satisfy interface %s (%s)", t.String(), a.Prog.IfaceEquatable.Name, context)
	}
}

// mapType returns the map type `#[k: v]`: the std/core `Map[k, v]` struct.
func (a *Analyzer) mapType(k, v types.Type, span diag.Span) types.Type {
	if a.Prog.GenStructMap == nil {
		a.Rep.Errorf(span, "map type `#[%s: %s]` requires std/core `Map` (`@lang(\"map\")`)", k.String(), v.String())
		return types.TypeInvalid
	}
	a.requireEquatable(k, span, "map key type")
	st := a.instantiateStruct(a.Prog.GenStructMap, []types.Type{k, v}, span)
	if st == nil {
		return types.TypeInvalid
	}
	return st
}

// requireCollMethod instantiates a std/core Set or Map method that the
// compiler calls on the user's behalf (literals, `m[k]`, `x in s`, ...), so
// that it is in the method table when the IR is lowered.
func (a *Analyzer) requireCollMethod(colTy types.Type, name string, span diag.Span) {
	if a.LookupMethod(colTy, name, nil, span) == nil {
		a.Rep.Errorf(span, "%s has no method %q", colTy.String(), name)
	}
}

// checkCollMethodElems reports a call-site error when a set or map method needs its element
// types to be copyable or printable and they are not. Without it the error would surface
// inside the std/core method body when the method is instantiated.
func (a *Analyzer) checkCollMethodElems(recvTy types.Type, method string, span diag.Span) bool {
	var need []types.Type
	what := "Copyable"
	if el, ok := types.SetElem(recvTy); ok {
		switch method {
		case "copy", "to_list", "iter":
			need = []types.Type{el}
		case "to_string":
			need, what = []types.Type{el}, "Printable"
		}
	} else if k, v, ok := types.MapKV(recvTy); ok {
		switch method {
		case "copy", "iter":
			need = []types.Type{k, v}
		case "keys":
			need = []types.Type{k}
		case "values", "get":
			need = []types.Type{v}
		case "to_string":
			need, what = []types.Type{k, v}, "Printable"
		}
	}
	for _, t := range need {
		ok := a.supportsCopy(t)
		if what == "Printable" {
			ok = a.supportsToString(t)
		}
		if !ok {
			a.Rep.Errorf(span, "`%s` on %s requires %s to implement `%s`", method, recvTy.String(), t.String(), what)
			return false
		}
	}
	return true
}
