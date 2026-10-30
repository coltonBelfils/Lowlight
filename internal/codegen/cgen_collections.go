package codegen

import (
	"fmt"

	"lowlight/internal/ir"
	"lowlight/internal/types"
)

func (g *cEmitter) emitVectorMethods(v *types.Vector) {
	m := v.Mangled()
	cVec := ir.CType(v)
	cElem := ir.CType(v.Elem)

	// reserve
	g.line("static inline void ll_vec_reserve__%s(%s* v, int64_t want) {", m, cVec)
	g.line("    if (want <= v->cap) return;")
	g.line("    int64_t ncap = (v->cap > 0) ? v->cap : 4;")
	g.line("    while (ncap < want) ncap *= 2;")
	g.line("    if (v->cap == 0) {")
	g.line("        %s* nd = (%s*)ll_alloc(ncap * (int64_t)sizeof(%s));", cElem, cElem, cElem)
	g.line("        if (v->len > 0 && v->data) {")
	if v.Elem.IsPlain() {
		g.line("            memcpy(nd, v->data, (size_t)v->len * sizeof(%s));", cElem)
	} else {
		g.line("            for (int64_t i = 0; i < v->len; i++) nd[i] = %s(v->data[i]);", g.copyExpr(v.Elem))
	}
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
	g.line("    if (check && (lo < 0 || hi < lo || hi > v.len)) ll_panic_cstr(\"vector slice bounds out of range\", f, l);")
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
		cOpt := ir.CType(optElem)
		g.line("static inline %s ll_vec_pop__%s(%s* v) {", cOpt, m, cVec)
		g.line("    if (v->len <= 0) return %s;", optNoneC(optElem))
		g.line("    v->len--;")
		g.line("    return %s;", optSomeC(optElem, "v->data[v->len]"))
		g.line("}")

		if g.canCopy(v.Elem) {
			g.line("static inline %s ll_vec_get__%s(%s v, int64_t idx) {", cOpt, m, cVec)
			g.line("    if (idx < 0 || idx >= v.len) return %s;", optNoneC(optElem))
			g.line("    return %s;", optSomeC(optElem, fmt.Sprintf("%s(v.data[idx])", g.copyExpr(v.Elem))))
			g.line("}")
		}
	}

	// contains (when `Elem` supports equality)
	if types.SupportsEq(v.Elem) {
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
	cArr := ir.CType(a)
	cElem := ir.CType(a.Elem)

	optElem := types.NewOptional(a.Elem)
	if g.hasOptionalType(optElem) && g.canCopy(a.Elem) {
		cOpt := ir.CType(optElem)
		g.line("static inline %s ll_arr_get__%s(%s a, int64_t idx) {", cOpt, m, cArr)
		g.line("    if (idx < 0 || idx >= %dLL) return %s;", a.Size, optNoneC(optElem))
		g.line("    return %s;", optSomeC(optElem, fmt.Sprintf("%s(a.data[idx])", g.copyExpr(a.Elem))))
		g.line("}")
	}
	if types.SupportsEq(a.Elem) {
		g.line("static inline bool ll_arr_contains__%s(%s a, %s needle) {", m, cArr, cElem)
		g.line("    for (int64_t i = 0; i < %dLL; i++) {", a.Size)
		g.line("        if (%s) return true;", stripOuterParens(g.eqInlineExpr(a.Elem, "a.data[i]", "needle")))
		g.line("    }")
		g.line("    return false;")
		g.line("}")
	}
}

func (g *cEmitter) emitSetMethods(s *types.Set) {
	m := s.Mangled()
	cSet := ir.CType(s)
	cElem := ir.CType(s.Elem)
	cEnt := cSet + "_ent"

	// rehash helper
	g.line("static inline void ll_set_rehash__%s(%s* s, int64_t new_bcap) {", m, cSet)
	g.line("    int64_t* nb = (int64_t*)ll_alloc(new_bcap * (int64_t)sizeof(int64_t));")
	g.line("    for (int64_t i = 0; i < new_bcap; i++) nb[i] = -1LL;")
	g.line("    int64_t mask = new_bcap - 1LL;")
	g.line("    for (int64_t i = 0; i < s->ent_len; i++) {")
	g.line("        if (!s->entries[i].alive) continue;")
	g.line("        int64_t slot = (int64_t)(s->entries[i].hash & (uint64_t)mask);")
	g.line("        while (nb[slot] >= 0) slot = (slot + 1LL) & mask;")
	g.line("        nb[slot] = i;")
	g.line("    }")
	g.line("    if (s->buckets) ll_free(s->buckets);")
	g.line("    s->buckets = nb;")
	g.line("    s->buck_cap = new_bcap;")
	g.line("}")

	// find_slot helper
	g.line("static inline int64_t ll_set_find_entry__%s(const %s* s, %s key, uint64_t h) {", m, cSet, cElem)
	g.line("    if (!s->buckets || s->buck_cap == 0) return -1LL;")
	g.line("    int64_t mask = s->buck_cap - 1LL;")
	g.line("    int64_t slot = (int64_t)(h & (uint64_t)mask);")
	g.line("    for (;;) {")
	g.line("        int64_t idx = s->buckets[slot];")
	g.line("        if (idx == -1LL) return -1LL;")
	g.line("        if (idx >= 0 && s->entries[idx].alive && s->entries[idx].hash == h && %s) return idx;",
		g.eqInlineExpr(s.Elem, "s->entries[idx].key", "key"))
	g.line("        slot = (slot + 1LL) & mask;")
	g.line("    }")
	g.line("}")

	// has
	g.line("static inline bool ll_set_has__%s(const %s* s, %s key) {", m, cSet, cElem)
	g.line("    uint64_t h = %s;", g.hashInlineExpr(s.Elem, "key"))
	g.line("    return ll_set_find_entry__%s(s, key, h) >= 0;", m)
	g.line("}")

	// insert (returns true if newly inserted; drops incoming duplicate key if already present!)
	g.line("static inline bool ll_set_insert__%s(%s* s, %s key) {", m, cSet, cElem)
	g.line("    uint64_t h = %s;", g.hashInlineExpr(s.Elem, "key"))
	g.line("    if (ll_set_find_entry__%s(s, key, h) >= 0) {", m)
	if !s.Elem.IsPlain() {
		g.line("        %s(&key);", ir.DropFnName(s.Elem))
	}
	g.line("        return false;")
	g.line("    }")
	g.line("    if (s->buck_cap == 0 || (s->ent_len + 1) * 10 >= s->buck_cap * 7) {")
	g.line("        ll_set_rehash__%s(s, s->buck_cap > 0 ? s->buck_cap * 2 : 16LL);", m)
	g.line("    }")
	g.line("    if (s->ent_len + 1 > s->ent_cap) {")
	g.line("        int64_t ncap = s->ent_cap > 0 ? s->ent_cap * 2 : 8LL;")
	g.line("        s->entries = (%s*)ll_realloc(s->entries, ncap * (int64_t)sizeof(%s));", cEnt, cEnt)
	g.line("        s->ent_cap = ncap;")
	g.line("    }")
	g.line("    int64_t idx = s->ent_len++;")
	g.line("    s->entries[idx] = (%s){ .alive = true, .hash = h, .key = key };", cEnt)
	g.line("    int64_t mask = s->buck_cap - 1LL;")
	g.line("    int64_t slot = (int64_t)(h & (uint64_t)mask);")
	g.line("    while (s->buckets[slot] >= 0) slot = (slot + 1LL) & mask;")
	g.line("    s->buckets[slot] = idx;")
	g.line("    s->len++;")
	g.line("    return true;")
	g.line("}")

	// remove
	g.line("static inline bool ll_set_remove__%s(%s* s, %s key) {", m, cSet, cElem)
	g.line("    if (!s->buckets || s->buck_cap == 0) return false;")
	g.line("    uint64_t h = %s;", g.hashInlineExpr(s.Elem, "key"))
	g.line("    int64_t mask = s->buck_cap - 1LL;")
	g.line("    int64_t slot = (int64_t)(h & (uint64_t)mask);")
	g.line("    for (;;) {")
	g.line("        int64_t idx = s->buckets[slot];")
	g.line("        if (idx == -1LL) return false;")
	g.line("        if (idx >= 0 && s->entries[idx].alive && s->entries[idx].hash == h && %s) {",
		g.eqInlineExpr(s.Elem, "s->entries[idx].key", "key"))
	if !s.Elem.IsPlain() {
		g.line("            %s(&s->entries[idx].key);", ir.DropFnName(s.Elem))
	}
	g.line("            s->entries[idx].alive = false;")
	g.line("            s->buckets[slot] = -2LL; /* tombstone */")
	g.line("            s->len--;")
	g.line("            return true;")
	g.line("        }")
	g.line("        slot = (slot + 1LL) & mask;")
	g.line("    }")
	g.line("}")

	// clear
	g.line("static inline void ll_set_clear__%s(%s* s) {", m, cSet)
	if !s.Elem.IsPlain() {
		g.line("    for (int64_t i = 0; i < s->ent_len; i++) {")
		g.line("        if (s->entries[i].alive) %s(&s->entries[i].key);", ir.DropFnName(s.Elem))
		g.line("    }")
	}
	g.line("    if (s->buckets) {")
	g.line("        for (int64_t i = 0; i < s->buck_cap; i++) s->buckets[i] = -1LL;")
	g.line("    }")
	g.line("    s->ent_len = 0;")
	g.line("    s->len = 0;")
	g.line("}")

	// to_vec (if `vec_<Elem>` exists)
	vecElem := &types.Vector{Elem: s.Elem}
	if g.hasVectorType(vecElem) && g.canCopy(s.Elem) {
		cVec := ir.CType(vecElem)
		vm := vecElem.Mangled()
		g.line("static inline %s ll_set_to_vec__%s(const %s* s) {", cVec, m, cSet)
		g.line("    %s out = (%s){0};", cVec, cVec)
		g.line("    if (s->len > 0) ll_vec_reserve__%s(&out, s->len);", vm)
		g.line("    for (int64_t i = 0; i < s->ent_len; i++) {")
		g.line("        if (s->entries[i].alive) ll_vec_push__%s(&out, %s(s->entries[i].key));", vm, g.copyExpr(s.Elem))
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}
	g.line("")
}

func (g *cEmitter) emitMapMethods(mp *types.Map) {
	m := mp.Mangled()
	cMap := ir.CType(mp)
	cKey := ir.CType(mp.Key)
	cVal := ir.CType(mp.Value)
	cEnt := cMap + "_ent"

	// rehash
	g.line("static inline void ll_map_rehash__%s(%s* m, int64_t new_bcap) {", m, cMap)
	g.line("    int64_t* nb = (int64_t*)ll_alloc(new_bcap * (int64_t)sizeof(int64_t));")
	g.line("    for (int64_t i = 0; i < new_bcap; i++) nb[i] = -1LL;")
	g.line("    int64_t mask = new_bcap - 1LL;")
	g.line("    for (int64_t i = 0; i < m->ent_len; i++) {")
	g.line("        if (!m->entries[i].alive) continue;")
	g.line("        int64_t slot = (int64_t)(m->entries[i].hash & (uint64_t)mask);")
	g.line("        while (nb[slot] >= 0) slot = (slot + 1LL) & mask;")
	g.line("        nb[slot] = i;")
	g.line("    }")
	g.line("    if (m->buckets) ll_free(m->buckets);")
	g.line("    m->buckets = nb;")
	g.line("    m->buck_cap = new_bcap;")
	g.line("}")

	// find_entry
	g.line("static inline int64_t ll_map_find_entry__%s(const %s* m, %s key, uint64_t h) {", m, cMap, cKey)
	g.line("    if (!m->buckets || m->buck_cap == 0) return -1LL;")
	g.line("    int64_t mask = m->buck_cap - 1LL;")
	g.line("    int64_t slot = (int64_t)(h & (uint64_t)mask);")
	g.line("    for (;;) {")
	g.line("        int64_t idx = m->buckets[slot];")
	g.line("        if (idx == -1LL) return -1LL;")
	g.line("        if (idx >= 0 && m->entries[idx].alive && m->entries[idx].hash == h && %s) return idx;",
		g.eqInlineExpr(mp.Key, "m->entries[idx].key", "key"))
	g.line("        slot = (slot + 1LL) & mask;")
	g.line("    }")
	g.line("}")

	// has
	g.line("static inline bool ll_map_has__%s(const %s* m, %s key) {", m, cMap, cKey)
	g.line("    uint64_t h = %s;", g.hashInlineExpr(mp.Key, "key"))
	g.line("    return ll_map_find_entry__%s(m, key, h) >= 0;", m)
	g.line("}")

	// insert (overwrites value in place if key exists, preserving insertion order!)
	g.line("static inline void ll_map_insert__%s(%s* m, %s key, %s val) {", m, cMap, cKey, cVal)
	g.line("    uint64_t h = %s;", g.hashInlineExpr(mp.Key, "key"))
	g.line("    int64_t existing = ll_map_find_entry__%s(m, key, h);", m)
	g.line("    if (existing >= 0) {")
	if !mp.Key.IsPlain() {
		g.line("        %s(&key);", ir.DropFnName(mp.Key))
	}
	if !mp.Value.IsPlain() {
		g.line("        %s(&m->entries[existing].val);", ir.DropFnName(mp.Value))
	}
	g.line("        m->entries[existing].val = val;")
	g.line("        return;")
	g.line("    }")
	g.line("    if (m->buck_cap == 0 || (m->ent_len + 1) * 10 >= m->buck_cap * 7) {")
	g.line("        ll_map_rehash__%s(m, m->buck_cap > 0 ? m->buck_cap * 2 : 16LL);", m)
	g.line("    }")
	g.line("    if (m->ent_len + 1 > m->ent_cap) {")
	g.line("        int64_t ncap = m->ent_cap > 0 ? m->ent_cap * 2 : 8LL;")
	g.line("        m->entries = (%s*)ll_realloc(m->entries, ncap * (int64_t)sizeof(%s));", cEnt, cEnt)
	g.line("        m->ent_cap = ncap;")
	g.line("    }")
	g.line("    int64_t idx = m->ent_len++;")
	g.line("    m->entries[idx] = (%s){ .alive = true, .hash = h, .key = key, .val = val };", cEnt)
	g.line("    int64_t mask = m->buck_cap - 1LL;")
	g.line("    int64_t slot = (int64_t)(h & (uint64_t)mask);")
	g.line("    while (m->buckets[slot] >= 0) slot = (slot + 1LL) & mask;")
	g.line("    m->buckets[slot] = idx;")
	g.line("    m->len++;")
	g.line("}")

	// get (`m[k] -> ?V`) & remove (`m.remove(k) -> ?V`)
	optVal := types.NewOptional(mp.Value)
	if g.hasOptionalType(optVal) {
		cOpt := ir.CType(optVal)
		if g.canCopy(mp.Value) {
			g.line("static inline %s ll_map_get__%s(const %s* m, %s key) {", cOpt, m, cMap, cKey)
			g.line("    uint64_t h = %s;", g.hashInlineExpr(mp.Key, "key"))
			g.line("    int64_t idx = ll_map_find_entry__%s(m, key, h);", m)
			g.line("    if (idx < 0) return %s;", optNoneC(optVal))
			g.line("    return %s;", optSomeC(optVal, fmt.Sprintf("%s(m->entries[idx].val)", g.copyExpr(mp.Value))))
			g.line("}")
		}

		g.line("static inline %s ll_map_remove__%s(%s* m, %s key) {", cOpt, m, cMap, cKey)
		g.line("    if (!m->buckets || m->buck_cap == 0) return %s;", optNoneC(optVal))
		g.line("    uint64_t h = %s;", g.hashInlineExpr(mp.Key, "key"))
		g.line("    int64_t mask = m->buck_cap - 1LL;")
		g.line("    int64_t slot = (int64_t)(h & (uint64_t)mask);")
		g.line("    for (;;) {")
		g.line("        int64_t idx = m->buckets[slot];")
		g.line("        if (idx == -1LL) return %s;", optNoneC(optVal))
		g.line("        if (idx >= 0 && m->entries[idx].alive && m->entries[idx].hash == h && %s) {",
			g.eqInlineExpr(mp.Key, "m->entries[idx].key", "key"))
		if !mp.Key.IsPlain() {
			g.line("            %s(&m->entries[idx].key);", ir.DropFnName(mp.Key))
		}
		g.line("            %s out = m->entries[idx].val;", cVal)
		g.line("            m->entries[idx].alive = false;")
		g.line("            m->buckets[slot] = -2LL;")
		g.line("            m->len--;")
		g.line("            return %s;", optSomeC(optVal, "out"))
		g.line("        }")
		g.line("        slot = (slot + 1LL) & mask;")
		g.line("    }")
		g.line("}")
	}

	// clear
	g.line("static inline void ll_map_clear__%s(%s* m) {", m, cMap)
	if !mp.Key.IsPlain() || !mp.Value.IsPlain() {
		g.line("    for (int64_t i = 0; i < m->ent_len; i++) {")
		g.line("        if (!m->entries[i].alive) continue;")
		if !mp.Key.IsPlain() {
			g.line("        %s(&m->entries[i].key);", ir.DropFnName(mp.Key))
		}
		if !mp.Value.IsPlain() {
			g.line("        %s(&m->entries[i].val);", ir.DropFnName(mp.Value))
		}
		g.line("    }")
	}
	g.line("    if (m->buckets) {")
	g.line("        for (int64_t i = 0; i < m->buck_cap; i++) m->buckets[i] = -1LL;")
	g.line("    }")
	g.line("    m->ent_len = 0;")
	g.line("    m->len = 0;")
	g.line("}")

	// keys & values
	vecKey := &types.Vector{Elem: mp.Key}
	if g.hasVectorType(vecKey) && g.canCopy(mp.Key) {
		cVec := ir.CType(vecKey)
		vm := vecKey.Mangled()
		g.line("static inline %s ll_map_keys__%s(const %s* m) {", cVec, m, cMap)
		g.line("    %s out = (%s){0};", cVec, cVec)
		g.line("    if (m->len > 0) ll_vec_reserve__%s(&out, m->len);", vm)
		g.line("    for (int64_t i = 0; i < m->ent_len; i++) {")
		g.line("        if (m->entries[i].alive) ll_vec_push__%s(&out, %s(m->entries[i].key));", vm, g.copyExpr(mp.Key))
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}
	vecVal := &types.Vector{Elem: mp.Value}
	if g.hasVectorType(vecVal) && g.canCopy(mp.Value) {
		cVec := ir.CType(vecVal)
		vm := vecVal.Mangled()
		g.line("static inline %s ll_map_values__%s(const %s* m) {", cVec, m, cMap)
		g.line("    %s out = (%s){0};", cVec, cVec)
		g.line("    if (m->len > 0) ll_vec_reserve__%s(&out, m->len);", vm)
		g.line("    for (int64_t i = 0; i < m->ent_len; i++) {")
		g.line("        if (m->entries[i].alive) ll_vec_push__%s(&out, %s(m->entries[i].val));", vm, g.copyExpr(mp.Value))
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}
	g.line("")
}

// emitBuiltinCompositeWrappers emits optional/vector-returning standard wrappers
// (`parse_int`, `parse_float`, `read_file`, `str.get_byte`, `str.index_of`, `str.bytes`, `str.chars`, `str.split`, `args`)
// whenever their return types are present in `prog`.
func (g *cEmitter) emitBuiltinCompositeWrappers() {
	optInt := types.NewOptional(types.TypeInt)
	if g.hasOptionalType(optInt) {
		g.line("static inline ll_opt_int ll_parse_int(ll_string s) {")
		g.line("    int64_t v = 0;")
		g.line("    if (ll_raw_parse_int(s, &v)) return (ll_opt_int){ .has_value = true, .value = v };")
		g.line("    return (ll_opt_int){ .has_value = false };")
		g.line("}")
		g.line("static inline ll_opt_int ll_str_index_of(ll_string s, ll_string sub) {")
		g.line("    int64_t idx = ll_raw_index_of(s, sub);")
		g.line("    if (idx >= 0) return (ll_opt_int){ .has_value = true, .value = idx };")
		g.line("    return (ll_opt_int){ .has_value = false };")
		g.line("}")
	}

	optFloat := types.NewOptional(types.TypeFloat)
	if g.hasOptionalType(optFloat) {
		g.line("static inline ll_opt_float ll_parse_float(ll_string s) {")
		g.line("    double v = 0.0;")
		g.line("    if (ll_raw_parse_float(s, &v)) return (ll_opt_float){ .has_value = true, .value = v };")
		g.line("    return (ll_opt_float){ .has_value = false };")
		g.line("}")
	}

	optStr := types.NewOptional(types.TypeString)
	if g.hasOptionalType(optStr) {
		g.line("static inline ll_opt_string ll_read_file(ll_string path) {")
		g.line("    ll_string content = {0};")
		g.line("    if (ll_raw_read_file(path, &content)) return (ll_opt_string){ .has_value = true, .value = content };")
		g.line("    return (ll_opt_string){ .has_value = false };")
		g.line("}")
	}

	optU8 := types.NewOptional(types.TypeU8)
	if g.hasOptionalType(optU8) {
		g.line("static inline ll_opt_u8 ll_str_get_byte(ll_string s, int64_t idx) {")
		g.line("    if (idx < 0 || idx >= s.len) return (ll_opt_u8){ .has_value = false };")
		g.line("    return (ll_opt_u8){ .has_value = true, .value = (uint8_t)s.data[idx] };")
		g.line("}")
	}

	vecU8 := &types.Vector{Elem: types.TypeU8}
	if g.hasVectorType(vecU8) {
		g.line("static inline ll_vec_u8 ll_str_bytes(ll_string s) {")
		g.line("    ll_vec_u8 out = {0};")
		g.line("    if (s.len > 0) {")
		g.line("        ll_vec_reserve__vec_u8(&out, s.len);")
		g.line("        memcpy(out.data, s.data, (size_t)s.len);")
		g.line("        out.len = s.len;")
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}

	vecChar := &types.Vector{Elem: types.TypeChar}
	if g.hasVectorType(vecChar) {
		g.line("static inline ll_vec_char ll_str_chars(ll_string s) {")
		g.line("    ll_vec_char out = {0};")
		g.line("    int64_t off = 0, next = 0;")
		g.line("    while (off < s.len) {")
		g.line("        uint32_t ch = ll_str_utf8_next(s, off, &next);")
		g.line("        ll_vec_push__vec_char(&out, ch);")
		g.line("        off = next;")
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}

	vecStr := &types.Vector{Elem: types.TypeString}
	if g.hasVectorType(vecStr) {
		g.line("static inline ll_vec_string ll_str_split(ll_string s, ll_string sep) {")
		g.line("    ll_vec_string out = {0};")
		g.line("    if (sep.len == 0) {")
		g.line("        int64_t off = 0, next = 0;")
		g.line("        while (off < s.len) {")
		g.line("            uint32_t ch = ll_str_utf8_next(s, off, &next);")
		g.line("            ll_vec_push__vec_string(&out, ll_str_from_char(ch));")
		g.line("            off = next;")
		g.line("        }")
		g.line("        return out;")
		g.line("    }")
		g.line("    int64_t start = 0, i = 0;")
		g.line("    while (i <= s.len - sep.len) {")
		g.line("        if (memcmp(s.data + i, sep.data, (size_t)sep.len) == 0) {")
		g.line("            ll_vec_push__vec_string(&out, ll_str_slice_owned(s, start, i, 0, \"\", 0));")
		g.line("            i += sep.len;")
		g.line("            start = i;")
		g.line("        } else {")
		g.line("            i++;")
		g.line("        }")
		g.line("    }")
		g.line("    ll_vec_push__vec_string(&out, ll_str_slice_owned(s, start, s.len, 0, \"\", 0));")
		g.line("    return out;")
		g.line("}")

		g.line("static inline ll_vec_string ll_get_cli_args(void) {")
		g.line("    ll_vec_string out = {0};")
		g.line("    for (int i = 0; i < ll_rt_argc; i++) {")
		g.line("        const char* a = ll_rt_argv[i];")
		g.line("        ll_vec_push__vec_string(&out, ll_copy__string(ll_str_lit(a, (int64_t)strlen(a))));")
		g.line("    }")
		g.line("    return out;")
		g.line("}")
		g.line("static inline ll_string ll_vec_join_str(ll_vec_string v, ll_string sep) {")
		g.line("    if (v.len <= 0) return ll_str_lit(\"\", 0);")
		g.line("    ll_string out = ll_str_lit(\"\", 0);")
		g.line("    for (int64_t i = 0; i < v.len; i++) {")
		g.line("        if (i > 0 && sep.len > 0) ll_str_push(&out, sep);")
		g.line("        ll_str_push(&out, v.data[i]);")
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}
	g.line("")
}

func optNoneC(opt *types.Optional) string {
	if opt.IsBoxed() {
		return fmt.Sprintf("((%s)NULL)", ir.CType(opt))
	}
	return fmt.Sprintf("(%s){ .has_value = false }", ir.CType(opt))
}

func optSomeC(opt *types.Optional, valExpr string) string {
	if opt.IsBoxed() {
		st := opt.Elem.(*types.Struct)
		return fmt.Sprintf("%s(%s)", ir.BoxOptFnName(st), valExpr)
	}
	return fmt.Sprintf("(%s){ .has_value = true, .value = %s }", ir.CType(opt), valExpr)
}
