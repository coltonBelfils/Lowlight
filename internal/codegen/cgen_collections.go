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

func (g *cEmitter) emitSetMethods(s *types.Set) {
	m := s.Mangled()
	cSet := CType(s)
	cElem := CType(s.Elem)
	cEnt := cSet + "_ent"

	// rehash helper
	g.line("static inline void ll_set_rehash__%s(%s* s, int64_t new_bcap) {", m, cSet)
	g.line("    if (s->len < s->ent_len) {")
	g.line("        int64_t w = 0;")
	g.line("        for (int64_t r = 0; r < s->ent_len; r++) {")
	g.line("            if (s->entries[r].alive) {")
	g.line("                if (w != r) s->entries[w] = s->entries[r];")
	g.line("                w++;")
	g.line("            }")
	g.line("        }")
	g.line("        s->ent_len = w;")
	g.line("    }")
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
	g.line("        int64_t target_bcap = s->buck_cap > 0 ? ((s->len * 2 >= s->buck_cap) ? s->buck_cap * 2 : s->buck_cap) : 16LL;")
	g.line("        ll_set_rehash__%s(s, target_bcap);", m)
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
		cVec := CType(vecElem)
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
	cMap := CType(mp)
	cKey := CType(mp.Key)
	cVal := CType(mp.Value)
	cEnt := cMap + "_ent"

	// rehash
	g.line("static inline void ll_map_rehash__%s(%s* m, int64_t new_bcap) {", m, cMap)
	g.line("    if (m->len < m->ent_len) {")
	g.line("        int64_t w = 0;")
	g.line("        for (int64_t r = 0; r < m->ent_len; r++) {")
	g.line("            if (m->entries[r].alive) {")
	g.line("                if (w != r) m->entries[w] = m->entries[r];")
	g.line("                w++;")
	g.line("            }")
	g.line("        }")
	g.line("        m->ent_len = w;")
	g.line("    }")
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
	g.line("        int64_t target_bcap = m->buck_cap > 0 ? ((m->len * 2 >= m->buck_cap) ? m->buck_cap * 2 : m->buck_cap) : 16LL;")
	g.line("        ll_map_rehash__%s(m, target_bcap);", m)
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
		cOpt := CType(optVal)
		if g.canCopy(mp.Value) {
			g.line("static inline %s ll_map_get__%s(const %s* m, %s key) {", cOpt, m, cMap, cKey)
			g.line("    uint64_t h = %s;", g.hashInlineExpr(mp.Key, "key"))
			g.line("    int64_t idx = ll_map_find_entry__%s(m, key, h);", m)
			g.line("    if (idx < 0) return %s;", optNoneC(optVal))
			g.line("    return %s;", wrapSomeC(optVal, mp.Value, fmt.Sprintf("%s(m->entries[idx].val)", g.copyExpr(mp.Value))))
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
		g.line("            return %s;", wrapSomeC(optVal, mp.Value, "out"))
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
		cVec := CType(vecKey)
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
		cVec := CType(vecVal)
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
