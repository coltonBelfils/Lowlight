package codegen

import (
	"lowlight/internal/ir"
	"lowlight/internal/types"
)

// EmitCompanionRuntime emits the C11 companion runtime and synthesized collection/type helpers
// needed when linking LLVM IR modules. It does NOT emit user globals, functions, or main.
func EmitCompanionRuntime(prog *ir.Program) string {
	g := &cEmitter{
		prog:        prog,
		isCompanion: true,
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

	g.emitCompanion()
	return g.sb.String()
}

func (g *cEmitter) emitCompanion() {
	// Enable external exports for runtime globals and functions
	g.sb.WriteString("#define LL_RT_GLOBAL\n#define LL_RT_FN\n#define static\n#define inline\n")

	// 1. Embedded C11 Runtime Header + User `extern "<header>"` includes
	g.sb.WriteString(RuntimeHeaderC11)
	if len(g.prog.ExternHeaders) > 0 {
		g.line("/* User Extern Headers */")
		for _, hdr := range g.prog.ExternHeaders {
			if len(hdr) > 0 && (hdr[0] == '.' || hdr[0] == '/') {
				g.line("#include %q", hdr)
			} else {
				g.line("#include <%s>", hdr)
			}
		}
		g.line("")
	}

	// 2. Enum Typedefs & Forward Struct Declarations
	g.emitEnumsAndForwardDecls()

	// 3. Topologically Ordered Concrete Type Definitions
	g.emitOrderedTypeDefs()

	// 4. Lambda Environment Structs
	g.emitLambdaEnvStructs()

	// 5. Forward Function & Helper Prototypes
	g.emitForwardPrototypes()

	// 6. Synthesized Type Lifecycle Helpers (`drop`, `copy`, `eq`, `hash`, `to_string`, collections)
	g.emitSynthesizedTypeHelpers()
	g.emitBuiltinCompositeWrappers()

	// 7. Lambda Env Helpers & Static Closure Thunks
	g.emitLambdaAndThunkHelpers()

	// 8. Companion Pointer-Based Runtime Wrappers for LLVM IR Calling ABI
	g.emitCompanionPointerWrappers()

	// 9. Companion Test Harness Runner
	g.emitCompanionTestHarness()
}

func (g *cEmitter) emitCompanionPointerWrappers() {
	g.line("/* LLVM IR Companion Pointer Wrappers */")
	g.line(`
void ll_print_str_p(const ll_string* s, int is_err, int is_ln) {
    if (s) ll_print_str(*s, is_err, is_ln);
}

void ll_panic_str_p(const ll_string* s, const char* f, int l) {
    if (s && s->data) {
        char buf[512];
        int64_t n = s->len < 511 ? s->len : 511;
        memcpy(buf, s->data, (size_t)n);
        buf[n] = '\0';
        ll_panic_cstr(buf, f, l);
    } else {
        ll_panic_cstr("<empty panic>", f, l);
    }
}

void ll_str_lit_p(ll_string* res, const char* s, int64_t len) {
    if (res) *res = ll_str_lit(s, len);
}

void ll_str_concat_p(ll_string* res, const ll_string* a, const ll_string* b) {
    if (res && a && b) *res = ll_str_concat(*a, *b);
}

void ll_str_clone_p(ll_string* res, const ll_string* s) {
    if (res && s) *res = ll_copy__string(*s);
}

void ll_str_free_p(ll_string* s) {
    if (s) ll_drop__string(s);
}

bool ll_str_eq_p(const ll_string* a, const ll_string* b) {
    if (!a || !b) return false;
    return ll_str_eq(*a, *b);
}

int64_t ll_str_cmp_p(const ll_string* a, const ll_string* b) {
    if (!a || !b) return 0;
    return ll_str_cmp(*a, *b);
}

uint64_t ll_str_hash_p(const ll_string* s) {
    if (!s) return 0;
    return ll_str_hash(*s);
}

int32_t ll_str_utf8_next_p(const ll_string* s, int64_t off, int64_t* next_off) {
    if (!s || !next_off) return 0;
    return (int32_t)ll_str_utf8_next(*s, off, next_off);
}

void ll_str_trim_p(ll_string* res, const ll_string* s) {
    if (res && s) *res = ll_str_trim(*s);
}

void ll_str_to_upper_p(ll_string* res, const ll_string* s) {
    if (res && s) *res = ll_str_to_upper(*s);
}

void ll_str_to_lower_p(ll_string* res, const ll_string* s) {
    if (res && s) *res = ll_str_to_lower(*s);
}

void ll_str_replace_p(ll_string* res, const ll_string* s, const ll_string* old_s, const ll_string* new_s) {
    if (res && s && old_s && new_s) *res = ll_str_replace(*s, *old_s, *new_s);
}

bool ll_str_contains_p(const ll_string* s, const ll_string* sub) {
    if (!s || !sub) return false;
    return ll_str_contains(*s, *sub);
}

bool ll_str_starts_with_p(const ll_string* s, const ll_string* sub) {
    if (!s || !sub) return false;
    return ll_str_starts_with(*s, *sub);
}

bool ll_str_ends_with_p(const ll_string* s, const ll_string* sub) {
    if (!s || !sub) return false;
    return ll_str_ends_with(*s, *sub);
}

void ll_str_push_p(ll_string* s, const ll_string* suffix) {
    if (s && suffix) ll_str_push(s, *suffix);
}

void ll_str_push_char_p(ll_string* s, int32_t ch) {
    if (s) ll_str_push_char(s, (uint32_t)ch);
}

void ll_str_push_byte_p(ll_string* s, uint8_t b) {
    if (s) ll_str_push_byte(s, b);
}

void ll_str_push_bytes_p(ll_string* s, const uint8_t* data, int64_t len) {
    if (s) ll_str_push_bytes(s, data, len);
}

void ll_str_clear_p(ll_string* s) {
    if (s) ll_str_clear(s);
}

bool ll_write_file_p(const ll_string* path, const ll_string* content) {
    if (!path || !content) return false;
    return ll_write_file(*path, *content);
}

void ll_arena_new_p(ll_arena* res, int64_t initial_cap) {
    if (res) *res = ll_arena_new(initial_cap);
}

void ll_os_name_p(ll_string* res) {
    if (res) *res = ll_os_name();
}

void ll_os_arch_p(ll_string* res) {
    if (res) *res = ll_os_arch();
}

void ll_os_argv_p(ll_string* res, int64_t idx) {
    if (res) *res = ll_os_argv(idx);
}

void ll_str_from_char_p(ll_string* res, uint32_t ch) {
    if (res) *res = ll_str_from_char(ch);
}

void ll_str_from_raw_p(ll_string* res, const uint8_t* data, int64_t len) {
    if (res) *res = ll_str_from_raw(data, len);
}

void ll_fmt_float_prec_p(ll_string* res, double v, int64_t prec) {
    if (res) *res = ll_fmt_float_prec(v, prec);
}

void ll_fmt_exp_f64_p(ll_string* res, double v, int64_t prec) {
    if (res) *res = ll_fmt_exp_f64(v, prec);
}

void ll_str_slice_view_p(ll_string* res, const ll_string* s, int64_t lo, int64_t hi, int check, const char* f, int64_t l) {
    if (res && s) *res = ll_str_slice_view(*s, lo, hi, check, f, (int)l);
}

void ll_str_slice_owned_p(ll_string* res, const ll_string* s, int64_t lo, int64_t hi, int check, const char* f, int64_t l) {
    if (res && s) *res = ll_str_slice_owned(*s, lo, hi, check, f, (int)l);
}

void ll_assert_p(bool cond, const ll_string* msg, const char* file, int64_t line) {
    if (!cond) {
        if (msg) ll_panic_str(*msg, file, (int)line);
        else ll_panic_cstr("assertion failed", file, (int)line);
    }
}
`)

	optInt := types.NewOptional(types.TypeInt)
	if g.hasOptionalType(optInt) {
		g.line(`
void ll_parse_int_p(void* res, const ll_string* s) {
    if (res && s) *(ll_opt_int*)res = ll_parse_int(*s);
}
void ll_str_index_of_p(void* res, const ll_string* s, const ll_string* sub) {
    if (res && s && sub) *(ll_opt_int*)res = ll_str_index_of(*s, *sub);
}
`)
	}

	optFloat := types.NewOptional(types.TypeFloat)
	if g.hasOptionalType(optFloat) {
		g.line(`
void ll_parse_float_p(void* res, const ll_string* s) {
    if (res && s) *(ll_opt_float*)res = ll_parse_float(*s);
}
`)
	}

	optStr := types.NewOptional(types.TypeString)
	if g.hasOptionalType(optStr) {
		g.line(`
void ll_read_file_p(void* res, const ll_string* path) {
    if (res && path) *(ll_opt_string*)res = ll_read_file(*path);
}
`)
	}

	optU8 := types.NewOptional(types.TypeU8)
	if g.hasOptionalType(optU8) {
		g.line(`
void ll_str_get_byte_p(void* res, const ll_string* s, int64_t idx) {
    if (res && s) *(ll_opt_u8*)res = ll_str_get_byte(*s, idx);
}
`)
	}

	vecU8 := &types.Vector{Elem: types.TypeU8}
	if g.hasVectorType(vecU8) {
		g.line(`
void ll_str_bytes_p(void* res, const ll_string* s) {
    if (res && s) *(ll_vec_u8*)res = ll_str_bytes(*s);
}
`)
	}

	vecChar := &types.Vector{Elem: types.TypeChar}
	if g.hasVectorType(vecChar) {
		g.line(`
void ll_str_chars_p(void* res, const ll_string* s) {
    if (res && s) *(ll_vec_char*)res = ll_str_chars(*s);
}
`)
	}

	vecStr := &types.Vector{Elem: types.TypeString}
	if g.hasVectorType(vecStr) {
		g.line(`
void ll_str_split_p(void* res, const ll_string* s, const ll_string* sep) {
    if (res && s && sep) *(ll_vec_string*)res = ll_str_split(*s, *sep);
}
void ll_get_cli_args_p(void* res) {
    if (res) *(ll_vec_string*)res = ll_get_cli_args();
}
`)
	}

	if g.vecSet["vec_string"] {
		g.line(`
void ll_vec_join_str_p(ll_string* res, const void* v, const ll_string* sep) {
    if (res && v && sep) *res = ll_vec_join_str(*(const ll_vec_string*)v, *sep);
}
`)
	}

	for _, v := range g.prog.Vectors {
		m := v.Mangled()
		cVec := CType(v)
		cElem := CType(v.Elem)
		g.line("void ll_vec_push_p__%s(%s* v, const %s* item) { if (v && item) ll_vec_push__%s(v, *item); }", m, cVec, cElem, m)
		optTy := types.NewOptional(v.Elem)
		if g.hasOptionalType(optTy) {
			cOpt := CType(optTy)
			g.line("void ll_vec_get_p__%s(%s* res, const %s* v, int64_t idx) { if (res && v) *res = ll_vec_get__%s(*v, idx); }", m, cOpt, cVec, m)
			g.line("void ll_vec_pop_p__%s(%s* res, %s* v) { if (res && v) *res = ll_vec_pop__%s(v); }", m, cOpt, cVec, m)
		}
		if types.SupportsEq(v.Elem) {
			g.line("bool ll_vec_contains_p__%s(const %s* v, const %s* item) { if (v && item) return ll_vec_contains__%s(*v, *item); return false; }", m, cVec, cElem, m)
		}
	}
	for _, s := range g.prog.Sets {
		m := s.Mangled()
		cSet := CType(s)
		cElem := CType(s.Elem)
		g.line("void ll_set_insert_p__%s(%s* s, const %s* key) { if (s && key) ll_set_insert__%s(s, *key); }", m, cSet, cElem, m)
		g.line("bool ll_set_has_p__%s(const %s* s, const %s* key) { if (s && key) return ll_set_has__%s(s, *key); return false; }", m, cSet, cElem, m)
		g.line("bool ll_set_remove_p__%s(%s* s, const %s* key) { if (s && key) return ll_set_remove__%s(s, *key); return false; }", m, cSet, cElem, m)
	}
	for _, mp := range g.prog.Maps {
		m := mp.Mangled()
		cMap := CType(mp)
		cKey := CType(mp.Key)
		cVal := CType(mp.Value)
		g.line("void ll_map_insert_p__%s(%s* m, const %s* key, const %s* val) { if (m && key && val) ll_map_insert__%s(m, *key, *val); }", m, cMap, cKey, cVal, m)
		g.line("bool ll_map_has_p__%s(const %s* m, const %s* key) { if (m && key) return ll_map_has__%s(m, *key); return false; }", m, cMap, cKey, m)
		optVal := types.NewOptional(mp.Value)
		if g.hasOptionalType(optVal) {
			cOpt := CType(optVal)
			g.line("void ll_map_get_p__%s(%s* res, const %s* m, const %s* key) { if (res && m && key) *res = ll_map_get__%s(m, *key); }", m, cOpt, cMap, cKey, m)
			g.line("void ll_map_remove_p__%s(%s* res, %s* m, const %s* key) { if (res && m && key) *res = ll_map_remove__%s(m, *key); }", m, cOpt, cMap, cKey, m)
		}
	}

	// String copy/eq/cmp/tostr pointer wrappers
	g.line("void ll_copy_p__string(ll_string* dest, const ll_string* src) { if (dest && src) *dest = ll_copy__string(*src); }")
	g.line("bool ll_eq_p__string(const ll_string* a, const ll_string* b) { if (!a || !b) return false; return ll_str_eq(*a, *b); }")
	g.line("int64_t ll_cmp_p__string(const ll_string* a, const ll_string* b) { if (!a || !b) return 0; return ll_str_cmp(*a, *b); }")
	g.line("void ll_tostr_p__string(ll_string* res, const ll_string* src) { if (res && src) *res = ll_copy__string(*src); }")

	for _, t := range g.prog.OrderedTypes {
		cT := CType(t)
		m := t.Mangled()
		if !t.IsPlain() && g.canCopy(t) {
			copyFn := ir.CopyFnName(t)
			g.line("void ll_copy_p__%s(%s* dest, const %s* src) { if (dest && src) *dest = %s(*src); }", m, cT, cT, copyFn)
		} else {
			g.line("void ll_copy_p__%s(%s* dest, const %s* src) { if (dest && src) *dest = *src; }", m, cT, cT)
		}
		hasEq := types.SupportsEq(t) || g.prog.Checked.LookupConcreteMethod(t, "eq") != nil || g.prog.Checked.LookupConcreteMethod(t, "cmp") != nil
		if hasEq {
			switch t.(type) {
			case *types.Struct, *types.Optional:
				eqFn := ir.EqFnName(t)
				g.line("bool ll_eq_p__%s(const %s* a, const %s* b) { if (a && b) return %s(*a, *b); return false; }", m, cT, cT, eqFn)
			}
		}
		if g.prog.Checked.LookupConcreteMethod(t, "cmp") != nil {
			cmpFn := ir.CmpFnName(t)
			g.line("int64_t ll_cmp_p__%s(const %s* a, const %s* b) { if (a && b) return %s(*a, *b); return 0; }", m, cT, cT, cmpFn)
		} else if d, isDist := t.(*types.Distinct); isDist {
			if g.prog.Checked.LookupConcreteMethod(d.Base, "cmp") != nil {
				cmpFn := ir.CmpFnName(t)
				g.line("int64_t ll_cmp_p__%s(const %s* a, const %s* b) { if (a && b) return %s(*a, *b); return 0; }", m, cT, cT, cmpFn)
			}
		}
		if g.canToString(t) {
			toStrFn := ir.ToStrFnName(t)
			g.line("void ll_tostr_p__%s(ll_string* res, const %s* src) { if (res && src) *res = %s(*src); }", m, cT, toStrFn)
		}
	}

	for _, en := range g.prog.Enums {
		cEn := CType(en)
		m := en.Mangled()
		g.line("void ll_copy_p__%s(%s* dest, const %s* src) { if (dest && src) *dest = *src; }", m, cEn, cEn)
		if en.IsTagged {
			if types.SupportsEq(en) {
				eqFn := ir.EqFnName(en)
				g.line("bool ll_eq_p__%s(const %s* a, const %s* b) { if (a && b) return %s(*a, *b); return false; }", m, cEn, cEn, eqFn)
			}
		} else {
			g.line("bool ll_eq_p__%s(const %s* a, const %s* b) { if (a && b) return *a == *b; return false; }", m, cEn, cEn)
		}
		toStrFn := ir.ToStrFnName(en)
		g.line("void ll_tostr_p__%s(ll_string* res, const %s* src) { if (res && src) *res = %s(*src); }", m, cEn, toStrFn)
	}
}

func (g *cEmitter) emitCompanionTestHarness() {
	g.raw("/* LLVM Companion Test Harness */")
	g.raw(`
int ll_run_test_case(void (*fn)(void), const char* desc) {
    printf("=== RUN   %s\n", desc);
    fflush(stdout);
    int64_t allocs_before = ll_rt_live_allocs;
    struct timespec t0, t1;
    clock_gettime(CLOCK_MONOTONIC, &t0);
    jmp_buf jmp;
    ll_test_current_jmp = &jmp;
    if (setjmp(jmp) == 0) {
        fn();
        clock_gettime(CLOCK_MONOTONIC, &t1);
        int64_t elapsed_us = (t1.tv_sec - t0.tv_sec) * 1000000LL + (t1.tv_nsec - t0.tv_nsec) / 1000LL;
        int64_t allocs_after = ll_rt_live_allocs;
        int64_t leaked = allocs_after - allocs_before;
        if (leaked > 0) {
            printf("--- FAIL: %s (leaked %lld heap allocation(s))\n", desc, (long long)leaked);
            fflush(stdout);
            ll_test_current_jmp = NULL;
            return 1;
        } else {
            printf("--- PASS: %s (%lldµs, 0 leaks)\n", desc, (long long)elapsed_us);
            fflush(stdout);
            ll_test_current_jmp = NULL;
            return 0;
        }
    } else {
        clock_gettime(CLOCK_MONOTONIC, &t1);
        printf("--- FAIL: %s (%s)\n", desc, ll_test_last_panic);
        fflush(stdout);
        ll_test_current_jmp = NULL;
        return 1;
    }
}

void ll_report_test_summary(int passed, int failed, int total) {
    printf("\nTest results: %d passed, %d failed (total %d)\n", passed, failed, total);
    fflush(stdout);
}
`)
}
