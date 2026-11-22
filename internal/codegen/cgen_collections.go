package codegen

import (
	"fmt"

	"lowlight/internal/ir"
	"lowlight/internal/types"
)

func (g *cEmitter) emitVectorMethods(v *types.Vector) {
	m := v.Mangled()
	cVec := CType(v)
	cElem := CType(v.Elem)

	// reserve
	g.line("static inline void ll_vec_reserve__%s(%s* v, int64_t want) {", m, cVec)
	g.line("    if (want <= v->cap) return;")
	g.line("    int64_t ncap = (v->cap > 0) ? v->cap : 4;")
	g.line("    while (ncap < want) ncap *= 2;")
	g.line("    if (v->cap == 0) {")
	g.line("        %s* nd = (%s*)ll_alloc(ncap * (int64_t)sizeof(%s));", cElem, cElem, cElem)
	g.line("        if (v->len > 0 && v->data) {")
	g.line("            memcpy(nd, v->data, (size_t)v->len * sizeof(%s));", cElem)
	g.line("        }")
	g.line("        v->data = nd;")
	g.line("        v->cap = ncap;")
	g.line("    } else {")
	g.line("        v->data = (%s*)ll_realloc(v->data, ncap * (int64_t)sizeof(%s));", cElem, cElem)
	g.line("        v->cap = ncap;")
	g.line("    }")
	g.line("}")

	// push
	g.line("static inline void ll_vec_push__%s(%s* v, %s item) {", m, cVec, cElem)
	g.line("    if (v->len + 1 > v->cap || v->cap == 0) ll_vec_reserve__%s(v, v->len + 1);", m)
	g.line("    v->data[v->len++] = item;")
	g.line("}")

	// clear
	g.line("static inline void ll_vec_clear__%s(%s* v) {", m, cVec)
	if !v.Elem.IsPlain() {
		g.line("    if (v->cap > 0 && v->data) {")
		g.line("        for (int64_t i = 0; i < v->len; i++) %s(&v->data[i]);", ir.DropFnName(v.Elem))
		g.line("    }")
	}
	g.line("    v->len = 0;")
	g.line("}")

	// remove (shifts remaining elements left, returns owned element)
	g.line("static inline %s ll_vec_remove__%s(%s* v, int64_t idx, int check, const char* f, int l) {", cElem, m, cVec)
	g.line("    if (check) ll_check_bounds(idx, v->len, f, l);")
	g.line("    %s out = v->data[idx];", cElem)
	g.line("    int64_t tail = v->len - idx - 1;")
	g.line("    if (tail > 0) memmove(&v->data[idx], &v->data[idx + 1], (size_t)tail * sizeof(%s));", cElem)
	g.line("    v->len--;")
	g.line("    return out;")
	g.line("}")

	// swap_remove (O(1) unordered removal, returns owned element)
	g.line("static inline %s ll_vec_swap_remove__%s(%s* v, int64_t idx, int check, const char* f, int l) {", cElem, m, cVec)
	g.line("    if (check) ll_check_bounds(idx, v->len, f, l);")
	g.line("    %s out = v->data[idx];", cElem)
	g.line("    v->len--;")
	g.line("    if (idx < v->len) v->data[idx] = v->data[v->len];")
	g.line("    return out;")
	g.line("}")

	// slice_view & slice_owned
	g.line("static inline %s ll_vec_slice_view__%s(%s v, int64_t lo, int64_t hi, int check, const char* f, int l) {", cVec, m, cVec)
	g.line("    if (LL_UNLIKELY(check && (lo < 0 || hi < lo || hi > v.len))) ll_panic_cstr(\"vector slice bounds out of range\", f, l);")
	g.line("    return (%s){ .data = v.data ? (v.data + lo) : NULL, .len = hi - lo, .cap = 0 };", cVec)
	g.line("}")

	if g.canCopy(v.Elem) {
		g.line("static inline %s ll_vec_slice_owned__%s(%s v, int64_t lo, int64_t hi, int check, const char* f, int l) {", cVec, m, cVec)
		g.line("    %s view = ll_vec_slice_view__%s(v, lo, hi, check, f, l);", cVec, m)
		g.line("    %s out = (%s){0};", cVec, cVec)
		g.line("    if (view.len > 0) {")
		g.line("        ll_vec_reserve__%s(&out, view.len);", m)
		g.line("        for (int64_t i = 0; i < view.len; i++) out.data[i] = %s(view.data[i]);", g.copyExpr(v.Elem))
		g.line("        out.len = view.len;")
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}

	// pop & get (only when `opt_<Elem>` is instantiated in the program)
	optElem := types.NewOptional(v.Elem)
	if g.hasOptionalType(optElem) {
		cOpt := CType(optElem)
		g.line("static inline %s ll_vec_pop__%s(%s* v) {", cOpt, m, cVec)
		g.line("    if (v->len <= 0) return %s;", optNoneC(optElem))
		g.line("    v->len--;")
		g.line("    return %s;", wrapSomeC(optElem, v.Elem, "v->data[v->len]"))
		g.line("}")

		if g.canCopy(v.Elem) {
			g.line("static inline %s ll_vec_get__%s(%s v, int64_t idx) {", cOpt, m, cVec)
			g.line("    if (idx < 0 || idx >= v.len) return %s;", optNoneC(optElem))
			g.line("    return %s;", wrapSomeC(optElem, v.Elem, fmt.Sprintf("%s(v.data[idx])", g.copyExpr(v.Elem))))
			g.line("}")
		}
	}

	// contains (when `Elem` supports equality)
	if CanEqType(g.prog, v.Elem) {
		g.line("static inline bool ll_vec_contains__%s(%s v, %s needle) {", m, cVec, cElem)
		g.line("    for (int64_t i = 0; i < v.len; i++) {")
		g.line("        if (%s) return true;", stripOuterParens(g.eqInlineExpr(v.Elem, "v.data[i]", "needle")))
		g.line("    }")
		g.line("    return false;")
		g.line("}")
	}
	g.line("")
}

func (g *cEmitter) emitStackArrayMethods(a *types.StackArray) {
	m := a.Mangled()
	cArr := CType(a)
	cElem := CType(a.Elem)

	optElem := types.NewOptional(a.Elem)
	if g.hasOptionalType(optElem) && g.canCopy(a.Elem) {
		cOpt := CType(optElem)
		g.line("static inline %s ll_arr_get__%s(%s a, int64_t idx) {", cOpt, m, cArr)
		g.line("    if (idx < 0 || idx >= %dLL) return %s;", a.Size, optNoneC(optElem))
		g.line("    return %s;", wrapSomeC(optElem, a.Elem, fmt.Sprintf("%s(a.data[idx])", g.copyExpr(a.Elem))))
		g.line("}")
	}
	if CanEqType(g.prog, a.Elem) {
		g.line("static inline bool ll_arr_contains__%s(%s a, %s needle) {", m, cArr, cElem)
		g.line("    for (int64_t i = 0; i < %dLL; i++) {", a.Size)
		g.line("        if (%s) return true;", stripOuterParens(g.eqInlineExpr(a.Elem, "a.data[i]", "needle")))
		g.line("    }")
		g.line("    return false;")
		g.line("}")
	}
}

func optNoneC(opt *types.Optional) string {
	if opt.IsBoxed() {
		return fmt.Sprintf("((%s)NULL)", CType(opt))
	}
	return fmt.Sprintf("(%s){ .has_value = false }", CType(opt))
}

func wrapSomeC(opt *types.Optional, elem types.Type, valExpr string) string {
	// Nested optionals flatten (`?(?T) -> ?T`), so retrieved `?T` elements are already wrapped.
	if _, isOpt := elem.(*types.Optional); isOpt {
		return valExpr
	}
	return optSomeC(opt, valExpr)
}

func optSomeC(opt *types.Optional, valExpr string) string {
	if opt.IsBoxed() {
		st := opt.Elem.(*types.Struct)
		return fmt.Sprintf("%s(%s)", ir.BoxOptFnName(st), valExpr)
	}
	return fmt.Sprintf("(%s){ .has_value = true, .value = %s }", CType(opt), valExpr)
}
