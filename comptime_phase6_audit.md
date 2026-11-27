# Lowlight Post-Phase 6 Codebase Audit

**Audited:** 2026-11-25 (immediately following completion of Phase 6 of `COMPTIME_IMPLEMENTATION_PLAN.md`)
**Method:** Exhaustive static code audit across the compiler pipeline (`internal/token`, `internal/lexer`, `internal/ast`, `internal/parser`, `internal/types`, `internal/sema`, `internal/ir`, `internal/codegen`, `internal/driver`, `internal/format`, `internal/docgen`, `internal/lsp`, `cmd/lowlight`) and standard library (`std/core`, `std/derive`, `std/os`, `std/fs`, `std/time`, `std/net`, `std/http`, `std/sort`, `std/collections`).
**Note:** Every finding below is verified directly against source lines (`file:line`). Because a Go toolchain was not available in the audit environment, items have not been executed against `go test`.

## Status of Prior Open Items (`COMPTIME_REVIEW.md` B10–B12)

| ID | Description | Status | Current Code Verification & Residual Gaps | 
| ----- | ----- | ----- | ----- | 
| **B10** | `addr_of` on `string` fields (`Map.remove`/`push`/`insert`) | **Mostly fixed** | `internal/sema/comptime.go:7050-7071` now reads `pl.get()`, materializes `ctString` via `materializeStringVal`, and writes back via `pl.set(mv)`. `ptrDerefPlace` (`1735-1792`) routes `_StringRaw` casts through `castReadVal`/`castWriteVal` (tested in `comptime_test.go:915-957`). **Residual bug (`comptime.go:7061`):** `_ = pl.set(mv)` silently discards writeback errors on read-only/const `string` places, leaking charged bytes and re-allocating on every read. | 
| **B11** | Memory budget accounting & `@comptime_limit` | **Partially fixed** | `@comptime_limit(steps: N, memory: M)` is parsed (`types_pass.go:250-314`) and wired to `ctState.memLimit` (`comptime.go:2342, 2636, 6732`; `types_pass.go:858`) with target-size accounting and `freeMem` credits (`comptime_test.go:959-974`). **Six holes remain** (see `H2` and `H3` below). | 
| **B12** | Mangled names in conditional generic errors | **Fixed** | `types_pass.go:4411` sets generic function `fi.Name` to `decl.Name[displayParts...]` and `types_pass.go:3991` sets method `fi.Name` to `recvTy.String() + "." + decl.Name`. `check_calls.go:61-62` and `check_stmt_expr.go:148, 156` report `fi.Name` (tested in `comptime_test.go:755-777`). *Minor note:* generic methods with method-level type params omit the method type args from `fi.Name` at `types_pass.go:3991`, though no mangled names leak. | 

## Part 1 — Critical Bugs (Soundness, Memory Safety, Miscompilation, & Compiler Panics)

### C1. `types.NewOptional` and `newCtSome` flattening breaks `v.as_variant(vr)` and `@derive` on enums with `?T` payloads

* **Locations:** `internal/types/types.go:276-281`, `internal/sema/check_calls.go:2157`, `internal/sema/check_stmt_expr.go:5029-5059`, `internal/sema/comptime.go:683-688, 8068, 8133, 8170, 8255, 8407`, `internal/ir/lower_calls.go:906-927`, `internal/ir/lower_expr.go:1232`, `std/derive/derive.ll:33, 70, 99, 129, 178`

* **Root Cause:**

  1. `checkEnumAsVariant` returns `types.NewOptional(vr.Payload)`. Because `types.NewOptional` flattens nested optionals (`if opt, ok := elem.(*Optional); ok { return opt }`), any tagged union or result variant whose payload is itself `?T` (e.g., `define E enum [ Maybe: ?int, Empty ]`) gives `v.as_variant(vr)` type `?int` instead of an optional-wrapped `?int`.

  2. In `if (v.as_variant(vr) as p):`, `checkIfExpr` unwraps `?int` and types `p` as `int`. `lowerEnumVariantCondBind` (`internal/ir/lower_expr.go:1232`) then assigns `vr.Payload` (`?int`, a tagged optional struct) directly into `uLoc` (typed as `int`), producing invalid C/LLVM IR.

  3. Outside `if (... as p)`, `lower_calls.go:911-927` calls `wrapInOptionalExpr(payVal, resOpt)` where both are `?int`, conflating "variant absent" with "variant present containing `none`".

  4. In `std/derive/derive.ll`, `eq`, `cmp`, `hash`, `copy`, and `to_string` all use `as_variant` for payload variants, so all five derives break on enums with `?T` payloads.

  5. In the comptime evaluator, `newCtSome` (`comptime.go:683-688`) likewise flattens `ctOpt`, breaking `as_variant`, `vec.get`, `arr.get`, `vec.pop`, `map[k]`, and optional chaining when the inner value is `?T`.

* **Impact:** Miscompilation / invalid C and LLVM IR for `as_variant` and all `@derive` traits on enums with optional payloads; silent wrong values at compile time.

* **How to Fix:**

  1. In `checkIfExpr` (`internal/sema/check_stmt_expr.go:5025-5044`), detect when `ie.Cond` is an `enum.as_variant` call (`cInf.BuiltinName == "enum.as_variant" && cInf.EnumVariant != nil`) and bind `unwrapSym.Type = cInf.EnumVariant.Payload` directly instead of unwrapping `types.NewOptional(vr.Payload)`.

  2. For standalone `v.as_variant(vr)` expressions (outside `if (... as p)`), either emit a compile error when `vr.Payload` is `*types.Optional` (hinting `v.is_variant(vr)` + `v.payload(vr)` or `if (v.as_variant(vr) as p)`), or introduce a non-flattening optional representation for `as_variant`.

  3. In `internal/sema/comptime.go:683-688`, remove `if inner != nil && inner.kind == ctOpt { return inner }` from `newCtSome`; perform optional-coercion checks only in `wrapResult` / `WrapOptional` when coercing `T` to `?T`, never when constructing an explicit outer optional layer.

  4. Add E2E + comptime parity tests for `define E enum [ Maybe: ?int, Empty ]` with `@derive(Equatable, Comparable, Hashable, Copyable, Printable)` and `if (v.as_variant(vr) as p)`.

### C2. `own` moves through `.field(f)` / `.elem(e)` / `.payload(vr)` cause compiler panics, UAF, and double-drop

* **Locations:** `internal/sema/check_stmt_expr.go:1119-1129`, `internal/ir/lower_expr.go:98-175`

* **Root Cause:**

  1. **Compiler panic & double-free on `ref[T]`:** `consumeValue` (`check_stmt_expr.go:1123`) permits moving a Resource field/element/payload when `baseSym.BindKind == BindOwned && (id.Name == "self" || (hasOwnMarker && !typeHasCustomDrop(baseSym.Type)))`. An owned local holding an arena reference (`let r: ref[MyStruct] = arena.alloc(...)`) has `BindKind == BindOwned` and `typeHasCustomDrop(*types.Ref) == false`. In `lower_expr.go:144`, `st := baseLoc.Type.(*types.Struct)` does an unchecked type assertion on `*types.Ref` and **panics the compiler**. For `ref[Tuple]` (`151-162`) and `ref[Enum]` (`116`), the type assertion fails and falls through to a non-zeroing read, aliasing the arena-owned Resource and causing double-free.

  2. **Partial move out of custom-`Drop` `own self`:** `check_stmt_expr.go:1123` lets `id.Name == "self"` bypass `!typeHasCustomDrop(baseSym.Type)`, allowing `own self` methods to partially move Resource fields while `self.drop()` still runs at scope exit.

  3. **Rvalue & chained-place UAF:** `lower_expr.go:112-114` only handles owned moves when `mem.Base` is a direct `*ast.IdentExpr` with `BindOwned`. Expressions like `make_struct().field(f)`, `make_enum().payload(vr)`, `make_tuple().elem(0)`, or `outer.inner.field(f)` fall through to `lowerRawExpr` -> `lowerBuiltinMethodCall` -> `lowerExprRead`, which registers the parent rvalue in `stmtTemps` and returns a shallow copy of the field; `flushStmtTemps()` immediately drops the parent temporary, freeing the moved value.

  4. **Missing `TupleFieldIdx` fallback:** `lower_expr.go:144-149` omits `if sf == nil && inf.TupleFieldIdx >= 0` (which exists in `lower_calls.go:862-864` and `lower_expr.go:647-649`).

  5. **Custom-`Drop` field double-drop:** `lower_expr.go:167` (and `self.field` at line `100`) only zeros `elemLval` (`ExprLitZero`). Because struct fields have no per-field moved flag, if `elemTy` has a custom `Drop`, dropping the parent struct still calls `Drop.drop` on the zeroed field.

* **Impact:** Compiler crash on `own r.field(f)`, immediate UAF on rvalue field/payload extraction, and double-free/double-drop on custom-`Drop` fields and arena refs.

* **How to Fix:**

  1. In `consumeValue` (`check_stmt_expr.go:1123`), explicitly reject `*types.Ref` and `*types.Ptr` base types before allowing a move, and apply `!typeHasCustomDrop(baseSym.Type)` uniformly (removing the `id.Name == "self"` exemption).

  2. In `lower_expr.go:144`, guard `baseLoc.Type.(*types.Struct)` with a checked assertion and add the `if sf == nil && inf.TupleFieldIdx >= 0` fallback.

  3. For rvalue bases in owned position (`make_struct().field(f)`, `make_enum().payload(vr)`), materialize the parent into a temporary local, extract/move the target field/payload, zero that slot, and drop only the remaining sibling fields (or zero the enum slot for `payload`).

  4. Either forbid moving a field whose type has custom `Drop` out of a struct/tuple (unless the whole parent is consumed), or track per-field drop flags.

### C3. Boxed `StaticOpt` (`HasOpt == true`) silently bakes as `NULL`/`null` in both backends

* **Locations:** `internal/sema/comptime.go:8788-8805`, `internal/codegen/cgen.go:2476-2480, 2514-2515`, `internal/codegen/llvmgen.go:1123-1128, 1340-1344, 2207-2211`

* **Root Cause:** `ExportStaticData` exports `StaticOpt{HasOpt: true, OptElem: ...}` for boxed optionals, but `isImmediateCStatic` and `isImmediateLLVMStatic` return `true` for *all* boxed optionals without checking `!sv.HasOpt`, and both emitters unconditionally output `NULL` / `null`. Furthermore, `ll_drop__opt_<T>` calls `ll_free(*p)` on any non-null box pointer (`cgen.go:743`, `llvmgen_runtime.go:2811`), so pointing a boxed optional directly at `.rodata` would crash on drop.

* **Impact:** Any compile-time `const` holding a non-`none` boxed optional silently evaluates to `none` at runtime.

* **How to Fix:**

  1. Gate `isImmediateCStatic` (`cgen.go:2476`) and `isImmediateLLVMStatic` (`llvmgen.go:1123`) on `!sv.HasOpt`.

  2. In `ExportStaticData` (`comptime.go:8788`) and `validateCrossVal` (`comptime.go:2512`), ensure non-`none` boxed optionals are always rejected from static `.rodata` export (or if ever allowed in runtime init, materialize them on the heap via `ll_alloc`).

### C4. Baked `StaticFn` corrupts non-capturing lambdas and fails to compile closures (`cl(...)`)

* **Locations:** `internal/ir/lower.go:1368-1373`, `internal/codegen/cgen.go:2469, 2507-2511`, `internal/codegen/llvmgen.go:1116, 1183-1187, 2198-2202`, `internal/codegen/split.go:393-396`

* **Root Cause:**

  1. `lowerStaticData` (`lower.go:1368-1373`) sets `sv.FnEntry = sd.Lambda.CName` directly instead of `sd.Lambda.CName + "_entry"` (for non-extern lambdas) or `reg.cCallback(...)` (for `extern fn` lambdas in test mode), diverging from `lowerFuncLitExpr` (`internal/ir/lower_ops.go:2575-2582`).

  2. When `sv.Type` is a closure (`*types.Func` with `Closure == true`), `cgen.go:2511` emits `((ll_fn_...)fn)` (casting a function pointer to a C struct) and `llvmgen.go:1187, 2202` emits `ptr @fn` instead of `%struct.ll_closure { ptr @fn, ptr null, ptr null, ptr null }`.

  3. `llvmgen.go:2202` emits `"@" + v.Static.FnEntry` instead of `llvmGlobalRef(...)` and does not strip `_entry` as `ExprConstructClosure` does (`llvmgen.go:2502-2506`).

  4. `split.go:393-396` looks up `lambdaByCName[sv.FnEntry]` without trimming `"_entry"` (unlike `split.go:509`).

* **Impact:** C11 compile error or LLVM IR type mismatch for baked closures; ABI corruption (missing `void* _ll_env` parameter) or missed test-mode `extern fn` callback wrapping for baked lambdas; missing lambda definitions in split compilation.

* **How to Fix:**

  1. In `lowerStaticData` (`lower.go:1368-1373`), match `lowerFuncLitExpr`: append `"_entry"` for non-extern lambdas and wrap `extern fn` lambdas via `cCallback` in test mode.

  2. Treat `Closure == true` in `isImmediateCStatic` / `isImmediateLLVMStatic` as non-immediate (or emit `{ .fn = ..., .env = NULL, .drop_fn = NULL, .copy_fn = NULL }` in C11 and `%struct.ll_closure { ptr @..., ptr null, ptr null, ptr null }` in LLVM).

  3. Use `llvmGlobalRef` at `llvmgen.go:2202` and trim `"_entry"` consistently in `llvmgen.go` and `split.go:393-396`.

### C5. Negative enum discriminants are broken in Phase 6 intrinsics and LLVM `.tag` loads

* **Locations:** `internal/ir/lower_calls.go:672, 694`, `internal/codegen/llvmgen.go:2294-2298`

* **Root Cause:**

  1. `enumVariantTagEqExpr` (`672`) and `emitEnumTagAssert` (`694`) construct `NewLitInt(uint64(vr.Value), vr.Value < 0, types.TypeInt)`. When `vr.Value < 0` (e.g., `-1`), `uint64(vr.Value)` is `18446744073709551615` with `Negated: true`, emitting `-18446744073709551615` instead of `-1`.

  2. `llvmgen.go:2297` zero-extends the `i32` enum tag (`zext i32 %f32 to i64`) instead of sign-extending (`sext`).

* **Impact:** `is_variant`, `payload`, `as_variant`, and `variant_index` fail on any enum variant with a negative discriminant.

* **How to Fix:**

  1. In `lower_calls.go:672, 694`, compute the magnitude properly:

     ```
     var mag uint64
     if vr.Value < 0 {
         mag = uint64(-(vr.Value + 1)) + 1 // safe for math.MinInt64
     } else {
         mag = uint64(vr.Value)
     }
     NewLitInt(mag, vr.Value < 0, types.TypeInt)
     
     ```

  2. In `llvmgen.go:2297`, change `zext i32` to `sext i32`.

### C6. Double-drop of moved/zeroed tagged enums with custom `Drop` in LLVM

* **Locations:** `internal/codegen/llvmgen_runtime.go:2750-2760` (compare `internal/codegen/cgen.go:686-691`)

* **Root Cause:** `emitTaggedEnumHelpersLLVM` invokes `customDrop(p)` unconditionally before checking `p->tag == 0`, whereas `cgen.go:686` returns early on `if (!p || p->tag == 0) return;`.

* **Impact:** If a tagged enum with custom `Drop` is moved/zeroed inside a struct/tuple/array/vector (where there is no per-field moved flag) or dropped twice, LLVM invokes `Drop.drop` on the zeroed enum.

* **How to Fix:** In `emitTaggedEnumHelpersLLVM` (`llvmgen_runtime.go:2750`), load `%tag` first in `do_drop` and `br i1 %is_zero, label %done, label %call_drop` before invoking `customDrop`.

### C7. Self-aliasing use-after-free in `ll_str_push` and `string.push_bytes`, plus `_string_grow` overflow

* **Locations:** `internal/codegen/runtime_h.go:394-402`, `std/core/primitives.ll:984-992, 1041-1049`

* **Root Cause:**

  1. In `runtime_h.go:394-402`, despite the comment `/* Copy suffix bytes before realloc in case suffix aliases s->data */`, the code calls `ll_str_ensure_owned_cap(s, s->len + slen + 1)` (which may `realloc` and free `s->data`) *before* reading `suffix.data` in `memmove`.

  2. In `std/core/primitives.ll:1045`, `string.push_bytes(bytes, count)` calls `_string_grow(*r, len + count + 1)` directly before `copy_bytes(..., bytes, count)` instead of using `_string_push_grow` (`1013-1027`), causing the same UAF when `bytes` aliases `self.data`.

  3. In `_string_grow` (`std/core/primitives.ll:986`), `while(ncap < needed): ncap *= 2` has no upper-bound guard (unlike `List.reserve` at `std/core/list.ll:91`), overflowing `ncap` to negative when `needed > 2^62`.

* **Impact:** Use-after-free when pushing a string or slice of itself onto itself; infinite loop or negative allocation on extreme `needed` values.

* **How to Fix:**

  1. In `runtime_h.go:394-402`, check `bool aliased = (s->data && suffix.data >= s->data && suffix.data < s->data + s->len); ptrdiff_t off = aliased ? (suffix.data - s->data) : 0;`, grow `s`, then recompute `const char *src = aliased ? (s->data + off) : suffix.data;` before `memmove`.

  2. In `std/core/primitives.ll:1045`, route `string.push_bytes` growth through `_string_push_grow`.

  3. In `_string_grow` (`primitives.ll:986`), add `if(ncap >= 4611686018427387904): ncap = needed else: ncap *= 2`.

### C8. Copy-on-Write promotion of `cap <= 0` `List[T]` shallow-copies non-Plain elements in C11 and `std/core/list.ll`

* **Locations:** `internal/codegen/cgen_collections.go:20-26`, `std/core/list.ll:96-98, 366-378` (compare `internal/codegen/llvmgen_runtime.go:3357-3377`)

* **Root Cause:** When promoting a `cap == 0` view to heap storage, LLVM's `ll_vec_ensure_owned_cap` deep-copies non-Plain copyable elements via `emitValueCopyInline`, whereas C11's `ll_vec_reserve` performs a shallow `memcpy` and `std/core/list.ll` (`List.reserve` at `96-98`, `List.extend` at `366-378`) uses `copy_bytes`.

* **Impact:** Promoting a `cap <= 0` view holding non-Plain heap-owning elements aliases those resources and double-frees when both containers drop, and C11 diverges from LLVM.

* **How to Fix:**

  1. In `cgen_collections.go:22-24`, when `!elemTy.IsPlain()`, emit a loop calling the element copy helper (`nd[i] = <copy>(v->data[i])`) instead of `memcpy`.

  2. In `std/core/list.ll:96-98` (and `extend` when `src.cap <= 0`), branch on `if(type.is_plain[T]()): copy_bytes(...) else: for(i in 0..self.len): ptr_write(ptr_add(nd, i), self.data[i].copy())`.

### C9. `StackArray.Equals` and `Struct.Equals` ignore array length `N` when comparing `[N: T]` against `Array[M, T]`

* **Locations:** `internal/types/types.go:336-343` (`StackArray.Equals`), `internal/types/types.go:527-534` (`Struct.Equals`)

* **Root Cause:** When `len(st.TypeArgs) == 2`, both methods only check `a.Elem.Equals(st.TypeArgs[1])` and never compare `a.Size` against `st.TypeArgs[0]` (`*ConstValue`).

* **Impact:** `[4: int]` compares equal to `Array[8, int]`, allowing mismatched fixed-size array types to pass typechecking and corrupt stack memory.

* **How to Fix:** In both `StackArray.Equals` and `Struct.Equals`, when `len(st.TypeArgs) == 2`, require `cv, ok := st.TypeArgs[0].(*ConstValue); ok && cv.Kind == ConstInt && !cv.IsUnsigned && cv.IntVal == a.Size` (or `cv.IsUnsigned && cv.UintVal <= math.MaxInt64 && int64(cv.UintVal) == a.Size`) before comparing `a.Elem.Equals(st.TypeArgs[1])`.

### C10. Broken backend fallbacks after Phase 6 deletion of composite `eq`/`cmp`/`hash`/`copy`/`to_string` helpers

* **Locations:** `internal/ir/lower_ops.go:1545, 1611, 1635, 2288, 2307`, `internal/codegen/cgen.go:648-678, 2074, 2084, 2109, 2116, 2131, 2135, 2183`, `internal/codegen/llvmgen_runtime.go:2019, 2072, 2122, 2167, 2197`

* **Root Cause:** `lower_ops.go` still emits `ExprEq`, `ExprCmp`, `ExprCopy`, and `ExprToString` for structs, tagged enums, tuples, and optionals when no concrete method is found, and `emitDistinctHelpers` (`cgen.go:654-677`) forwards to `d.Base`. Now that backend composite helper synthesis is deleted:

  * **C11** emits `"false"` for tagged enum eq, `((a) == (b))` (invalid C11 on structs) for structs/tuples/optionals, `"0LL"` for cmp, `"0ULL"` / `(uint64_t)(struct_val)` for hash, and calls to nonexistent `ll_copy__<T>` / `ll_tostr__<T>`.

  * **LLVM** emits external declarations and calls to deleted `@ll_*_p__<T>` symbols, causing linker errors.

* **Impact:** Linker errors in LLVM and invalid C11 or silent wrong results (`false`/`0`) whenever an unlowered composite protocol op reaches codegen (especially through `Distinct` wrappers).

* **How to Fix:**

  1. Ensure `sema` and `lower_ops.go` always resolve composite `eq`/`cmp`/`hash`/`copy`/`to_string` (including through `*types.Distinct` wrappers) to concrete `std/core`/`std/derive` methods before IR emission.

  2. Replace silent fallback returns in `cgen.go` and `llvmgen_runtime.go` with explicit internal compiler errors (`panic(fmt.Sprintf("ICE: unlowered composite op on %s", ty))`) so any future lowering gap fails loudly instead of miscompiling.

### C11. `@packed` struct `&v.field(f)` borrows and hardcoded LLVM `align 8` loads/stores (UB)

* **Locations:** `internal/sema/check_stmt_expr.go:1211-1251`, `internal/sema/check_calls.go:1428`, `internal/ir/lower_expr.go:644-653`, `internal/codegen/llvmgen.go:1553-1554, 2309, 2425, 2459, 2575, 2605`

* **Root Cause:**

  1. `analyzePlace` / `checkMutablePlace` never checks whether a field access (`.field` or `.field(f)`) projects through a `Packed` struct, so `&p.field(f)` and `&self` method calls on `@packed` fields pass sema and emit `&packed->field` in C11 (`-Waddress-of-packed-member` / UB).

  2. `llvmgen.go` hardcodes `align 8` in `ExprConstructStruct` (`2425`), `ExprConstructArray` (`2459`), `ExprWrapOptional` (`2575`), and `ExprOptionalValue` (`2605`), and ignores `Packed` in `ExprField` (`2309`) and `InstrAssign` (`1553-1554`).

* **Impact:** LLVM UB (miscompilation under `-O2` or SIGBUS on strict-alignment targets) for `[N: u8]`, `[N: i32]`, `?u8`, `?i32`, and all `@packed` structs; C11 `-Werror` failures under `-Waddress-of-packed-member`.

* **How to Fix:**

  1. In `checkMutablePlace` (for `&` borrows and `&self` receivers), reject taking the address of a field inside a `st.Packed` struct when `fieldAlign > 1` (while still permitting by-value reads and direct `p.field(f) = val` assignments).

  2. In `llvmgen.go`, emit `align 1` for loads/stores whose base is a `Packed` struct, and replace hardcoded `align 8` at lines `2425, 2459, 2575, 2605` with `g.typeAlign(...)`.

### C12. `enum.as_variant` is accepted as a mutable lvalue in sema, silently mutating a temporary in IR

* **Locations:** `internal/sema/check_stmt_expr.go:1240-1251`, `internal/ir/lower_expr.go:627-689`

* **Root Cause:** `analyzePlace` (`check_stmt_expr.go:1240`) includes `callInf.BuiltinName == "enum.as_variant"` so that `if (v.as_variant(vr) as &p)` passes `checkMutablePlace` (`5046`). Because `analyzePlace` is shared by all mutation checks, `v.as_variant(vr) = none` and `foo(&v.as_variant(vr))` pass sema, but `lowerLValueAddrMode` (`lower_expr.go:628`) does not handle `enum.as_variant` and falls through to `_ll_lval` (`687`), mutating a discarded temporary.

* **Impact:** Silent miscompilation — assignments and `&` borrows on `v.as_variant(vr)` write into a temporary and are lost.

* **How to Fix:** Remove `callInf.BuiltinName == "enum.as_variant"` from `analyzePlace` (`check_stmt_expr.go:1240`). In `checkIfExpr` (`5045-5049`), check if `ie.Cond` is an `enum.as_variant` call and call `fc.checkMutablePlace(cInf.MethodRecv, "mutably unwrap")` on its receiver instead.

## Part 2 — High-Severity Bugs (Comptime Evaluator, Privacy, Unrolling, & Native OS Layer)

### H1. `validateCrossesToRuntime` has four bypasses letting forbidden types leak to runtime

* **Locations:** `internal/sema/comptime.go:2398, 2437-2441, 2478, 2485-2626`

* **Root Cause:**

  1. **Interface / trait objects (`*types.Interface`):** `validateCrossTypeOnly` (`2398`) returns `""`, and `validateCrossVal` has no `case *types.Interface` (falling through to `return ""` at `2626`). Boxing a `@comptime_only` struct, custom `Drop`, `Map`, `Set`, or `Arena` into an interface (`const X: Printable = MyDropStruct[...]`) bypasses validation completely.

  2. **Unboxed `none` (`2507-2511`):** `if val == nil || !val.hasOpt { return "" }` returns *before* checking `t.IsBoxed()`, letting `const X: ?ptr[i32] = none`, `?ref[T]`, `?Map[K, V]`, `?Set[T]`, `?Arena`, `?cl() int`, and `?CustomDrop` cross to runtime (contradicting `validateCrossTypeOnly:2419-2421`). Also, if `val.kind != ctOpt`, `val.hasOpt` is `false` and validation is skipped.

  3. **Enums & Results (`2437-2441, 2560-2570`):** `validateCrossTypeOnly` checks `LookupConcreteMethod(t, "drop")` on `*types.Enum`, but skips `t.HasCustomDrop` and never recurses into `vr.Payload`. An empty `List[MyResultWithPtr]` passes. Meanwhile `validateCrossVal` (`2561`) checks `LookupConcreteMethod` but not `t.HasCustomDrop`, while `*types.Struct` (`2610`) checks `t.HasCustomDrop` but not `LookupConcreteMethod`.

  4. **Byte-list shortcut (`2526, 2594`):** `if _, _, ok := listStructBytes(val); ok { return "" }` skips validating `t.Elem` / `elemTy`, letting `List[DistinctU8WithCustomDrop]` cross.

* **How to Fix:**

  1. Reject `*types.Interface` in both `validateCrossTypeOnly` and `validateCrossVal` with a clear error.

  2. In `validateCrossVal` for `*types.Optional`, only return `""` on `none` when `t.IsBoxed()`; for unboxed optionals, always validate `a.validateCrossTypeOnly(t.Elem, seen)` even when `none`.

  3. In `validateCrossTypeOnly` for `*types.Enum`, check `t.HasCustomDrop || LookupConcreteMethod(t, "drop") != nil`, mark `seen[ty] = true`, and recurse into every `vr.Payload`. Check both `HasCustomDrop` and `LookupConcreteMethod` consistently for structs and enums, and track `seen` in `validateCrossVal`.

  4. Before returning `""` on the `listStructBytes` shortcut, call `a.validateCrossTypeOnly(elemTy, seen)`.

### H2. `@comptime_limit` step-budget reset bypass, ignored tighter memory limits, and asymmetric memory accounting

* **Locations:** `internal/sema/comptime.go:305, 314, 1475, 3016, 3866, 3966, 4489, 4702, 5175, 5204, 5292, 6724-6732, 6755-6758, 7492, 7501, 8591, 8600`

* **Root Cause:**

  1. **Step-budget reset bypass (`6724-6730, 6755-6758`):** Entering a function with `fi.ComptimeLimit > 0` resets `s.steps = 0`, and returning restores `s.steps = prevSteps + 1`. Calling a `@comptime_limit(steps: 1000)` helper in a loop or recursively (up to depth 1024) grants a fresh 1,000 steps per call while charging the caller 1 step (enabling $10^9$–$2^{1024}$ step DoS).

  2. **Stricter memory limit ignored (`6732`):** `fi.ComptimeMemLimit > 0 && fi.ComptimeMemLimit > s.memLimit` ignores `@comptime_limit(memory: 1024)` on callees when the caller has the default 64 MB limit.

  3. **Sub-MB formatting (`314, 7492, 7501`):** `limit / (1024 * 1024)` prints `"0 MB"` for limits under 1 MB.

  4. **`evalConstIntWithSubst` (`3016`)** creates `newCtState(a, mod, 0)` without inheriting the enclosing symbol/function's `@comptime_limit`.

  5. **Asymmetric accounting:** `InterpStringExpr` (`3866`), string `+`/`+=` (`4702, 5292`), `evalStructLit` (`4489`), `evalAssignStmt` (`5175, 5204`), `ArrayLit` (`3966`), and `evalSlice` (`8591, 8600`) charge `s.memBytes` without ever crediting back on overwrite/drop (so string-concat loops monotonically hit 64 MB even with tiny live memory), whereas `vec.join`, `any.copy`, `realloc_raw` growth, and `copy_bytes` are uncharged.

* **How to Fix:**

  1. Never reset `s.steps` to `0` on callee entry; keep `s.steps` cumulative and only adjust `s.stepLimit = max(s.stepLimit, fi.ComptimeLimit)` (restoring `prevStepLimit` on return, and checking `s.steps > s.stepLimit` if restored).

  2. Allow `@comptime_limit(memory: M)` on callees to set `s.memLimit` (checking `s.memBytes > s.memLimit` immediately on entry), and format limits `< 1 MB` in KB/bytes.

  3. Propagate enclosing limits into `evalConstIntWithSubst`, and audit/balance `allocMem` + `freeMem` across string/slice/clone operations (or separate cumulative-allocation cap from live-heap cap).

### H3. Simulated heap soundness: `int64` overflow panics, ignored `castTy`, pointer collisions, no UAR check, and cyclic pointer hangs

* **Locations:** `internal/sema/comptime.go:1251-1253, 1475, 1658, 1802, 1865-1874, 3562, 3635-3648, 3708-3724, 3966, 7063-7071, 7100, 7205, 7264, 7327, 8377, 8513`

* **Root Cause:**

  1. **Bounds/allocation `int64` overflows:** `count * elemSize` (`1475`), `size * s.simElemSize(...)` (`3966`), `dp.byteOff + cnt > int64(len(...))` (`7205, 7264, 7327`), `off + elemSize > int64(len(...))` (`1802`), `off + n > int64(len(...))` (`1658`), and `cp.byteOff += off * p.elemSize` (`7100, 3562, 8377, 8513`) can overflow `int64` negative, bypassing checks and panicking Go (`makeslice` / `slice bounds out of range`).

  2. **Heap `ptr_write` ignores `castTy` (`1865-1874`):** Unlike the stack branch (`1753-1763`), heap `ptrDerefPlace.set` stores `nv` directly into `al.cells[idx]` without calling `castWriteVal(nv, cur, castTy, span)`, corrupting type-punned heap writes like `ptr_write(ptr_cast[f64](p_u64), 3.14)`.

  3. **Stack pointer address collisions (`1251-1253`):** `ptrAddr()` returns `0x70000000 + byteOff` for every `p.place != nil` pointer, making `ptr_addr(addr_of(x)) == ptr_addr(addr_of(y))` return `true` for distinct locals.

  4. **No use-after-return detection (`7063-7071`):** `addr_of(local)` closes over a Go-heap `*ctSlot` that is never invalidated when `ctFrame` pops.

  5. **Infinite loop on cyclic pointers (`3635-3648, 3708-3724`):** `for bv != nil && bv.kind == ctPointer` has no hop limit or `s.step()` call.

  6. **Missing step accounting (`7209-7232, 7267-7280, 7330-7342`):** `copy_bytes`, `zero_bytes`, and `fill_bytes` run in a single step regardless of buffer size.

* **How to Fix:**

  1. Use overflow-safe checks everywhere: `if elemSize > 0 && count > math.MaxInt64/elemSize`, `if off < 0 || off > bufLen || cnt < 0 || cnt > bufLen - off`, and checked multiply/add for `ptr_add`.

  2. Call `s.castWriteVal(nv, al.cells[idx], castTy, span)` in `ptrDerefPlace.set` (`1865-1874`) when `castTy != nil`.

  3. Assign a unique synthetic address per `ctSlot`/materialized stack place via `nextAllocIDAddr()`, and attach a `frameAlive *bool` flag per `ctFrame` (set `false` on pop) checked on deref.

  4. Call `s.step(span)` (and cap hops) inside `derefCtVal` and `evalPlace`, and charge `cnt / 4096` steps in `copy_bytes`/`zero_bytes`/`fill_bytes`.

### H4. Comptime LIFO `Drop` omissions, overwrite UAF, and Go nil-pointer panics

* **Locations:** `internal/sema/comptime.go:914-986, 1065, 1128-1140, 1185-1230, 4069, 4527, 5165-5232, 5317, 5324, 5521, 7970-8020, 8057-8183`

* **Root Cause:**

  1. **Missing drops on overwrite:** Unpack assignment (`5165-5182`) never drops overwritten targets or `_` discarded tuple elements; `evalAssignStmt` (`5218-5231`) only drops old values for `*ast.IdentExpr` and `*ast.MemberExpr` (skipping `arr[i] = v`, `list[i] = v`, `v.field(f) = x`, `t.elem(e) = x`, `e.payload(v) = x`) and skips `&mut` params (`sl.refPlace == nil` at `5209`).

  2. **Self-assignment UAF (`5192, 5224-5232`):** `evalAssignStmt` evaluates RHS `val`, drops `oldV` (freeing its `ctHeapAlloc`), then calls `pl.set(val.clone())`. For `a.b = a.b`, `oldV` and `val` share the same `ctHeapAlloc`, freeing `val` before storing it.

  3. **Primary abort masked (`5317, 5324, 5521`):** In `evalIf`, a secondary abort from `popDropScope` overwrites a primary `sigAbort` from `evalExpr`.

  4. **Nil-pointer panics:** `evalUnary` (`4527`) dereferences `inf.Type` without `inf != nil`; `evalAssignStmt` (`5222`) evaluates `baseInf.Type.(*types.Ptr)` *before* `if baseInf == nil` on line `5223`; `FuncLit` (`4069`) calls `sl.val.clone()` without `sl.getVal` when capturing a `&T` param (`sl.val == nil`); container helpers (`914-986, 1065, 1128-1140, 1185-1230, 8057-8183`) dereference box pointers without nil checks.

* **How to Fix:**

  1. Drop overwritten values uniformly via `pl.get()` for all non-Plain places (including `refPlace`, index, reflection places, and unpack targets), and clone RHS *before* dropping `oldV` (or skip drop when `oldV == val`).

  2. Preserve primary `sigAbort` in `evalIf` (matching `evalBlock:4965-4969`).

  3. Fix the `baseInf == nil` check order at `5222-5223`, guard `inf != nil` at `4527`, use `sl.getVal` at `4069`, and add nil guards on box pointers in value helpers.

### H5. Phase 4/6 evaluator gaps (`comptime match` bindings, `if &v = opt`, `Distinct` casts, `NaN`, `evalCompFieldsOf`)

* **Locations:** `internal/sema/comptime.go:1029-1036, 1161, 2748-2766, 3017-3022, 3032, 3071-3154, 4662, 5361, 5460-5475, 5514-5520, 5613-5630, 5690-5696, 5796, 5839, 6124-6126, 6140-6166, 6245-6260, 7742-7755, 7803-7813, 7874-7878, 8576, 8984-9024, 9138-9172`

* **Root Cause & How to Fix (itemized):**

  1. **`comptime match` pattern bindings dropped (`6245-6260`):** When `ComptimeMatchArm[me]` hits, `evalMatch` evaluates `arms[armIdx].Body` without populating `fr.locals` with pattern bindings. *Fix:* Store bound values on `ComptimeMatchBindings[me]` (or run `matchCase` on `arms[armIdx]`) and inject them into `fr.locals`.

  2. **`if &v = opt_expr` loses writebacks (`5361, 5460-5475, 5514-5520`):** `IfCondOptBind` + `MutUnwrap` binds by value without `originSlot`, and `IfCondOptVar` (`5361`) reads `originSlot.val` instead of `originSlot.getVal()`. *Fix:* Bind `MutUnwrap` via a `ctPlace` backed by `pl` (mirroring `variantRefPlace` at `5421-5441`) and use `originSlot.getVal(...)` at `5361`.

  3. **`evalCompFieldsOf` (`9138-9172`)** doesn't call `s.a.ensureStructFieldsForLayout(st, span)`, returning `[]` if invoked early. *Fix:* Call `ensureStructFieldsForLayout` before reading `st.Fields`.

  4. **`Distinct` numeric conversion skips base conversion/masking (`7803-7813`):** `Port(70000)` on `distinct u16` keeps `70000` (`TypeInt`) instead of `u16(4464)`. *Fix:* When `!av.ty.Equals(dist.Base)`, convert `av` to `dist.Base` first, then set `res.ty = dist`. Also unwrap `*types.Distinct` in `ctValToConstValue` (`8984-9024`).

  5. **`evalConstIntWithSubst` (`3017-3022`)** omits `subst: subst` on `st.frames[0]`; `evalSimpleConstIntWithSubst` (`3032, 3071-3154`) accepts `CharLit`/`ctChar` (`['a': int]` -> `[97: int]`), casts `uint64` literals to `int64` without overflow checks, and uses signed `>>` for `u64`. *Fix:* Add `subst: subst`, reject `CharLit`/`ctChar` for `[N: T]` sizes, and check overflows (or fall through to the full evaluator).

  6. **Collecting `for ... else` & `continue val` (`5613-5630, 5690-5696, 5796, 5839, 6124-6126`):** Non-unrolled collecting `for ... else` (`6124`) runs `elseExpr` even when `collected` is non-empty; list/map/set collecting loops ignore `continue val`; bare `continue` in `CollectTuple`/`CollectArray` leaves `nil` slots. *Fix:* Check `isEmpty` before running `elseExpr`, handle `bsig.val` on `continue`, and reject bare `continue` without a value for tuple/array targets.

  7. **Arithmetic/char/string parity:**

     * `cmpVal` (`1029-1036, 4662, 7742-7755`) returns `0` for `NaN`, breaking tuple `<=` and `min`/`max` with `NaN`. *Fix:* Return `(int, bool)` with an `unordered` flag.

     * Float-to-int cast (`7874-7878`) uses host-arch-dependent Go `int64(av.f)`. *Fix:* Implement explicit trapping or saturating semantics matching runtime codegen.

     * Char range loop (`6142, 2750`) lacks `if cur == hi { break }` (overflow risk on `MaxInt32`) and iterates through UTF-16 surrogates `0xD800..0xDFFF`. *Fix:* Add the `cur == hi` break and skip/reject surrogates.

     * Raw `ctString` slicing (`8576`) doesn't validate UTF-8 boundaries; nested `List[T]` in `equalsDepth` (`951-957`) compares pointer identity; `ctVal.toString` (`1161`) formats `f32` at 64-bit precision.

### H6. Phase 6 `_` privacy authorization: closure escape, over-restricted helper methods, and cache key collisions

* **Locations:** `internal/sema/check_stmt_expr.go:4738-4823`, `internal/sema/check_calls.go:809, 1865, 2034`, `internal/sema/types_pass.go:4046-4058, 4115, 4248-4252, 4294-4297, 4397-4400`

* **Root Cause:**

  1. **Closure escape (`check_stmt_expr.go:4738-4823`):** `checkFuncLit` leaves `fc.curFn` intact, so closures created inside custom `@derivable` handlers retain `_` private field/variant access even if returned or stored out of scope.

  2. **Over-restricted helper methods (`types_pass.go:4248-4252`):** `instantiateMethodWithDerive` is dead code — `instantiateMethod` hardcodes `(nil, "")`, so same-package generic helper *methods* called from `@derive` handlers lose derive authorization.

  3. **Cache key & C symbol collision (`types_pass.go:4046-4058`):** `sanitizeDeriveScopeSuffix` maps `/`, `-`, `.` all to `_`, so packages `a/b` and `a_b` collide in `tmpl.Instances` and `fi.CName`.

* **How to Fix:**

  1. Reject `hasDeriveAccess` when `fc.funcDepth > 0` (or forbid escaping closures that accessed private fields via derive authorization).

  2. Thread `(deriveTarget, derivePkg)` into `LookupMethod` / `instantiateMethodWithDerive` when `fc.curFn != nil && fc.curFn.DeriveTarget != nil && SamePackage(fc.curFn.Mod, gtmpl.Mod)`.

  3. Use raw `derivePkg` separated by `\x00` in `instKey`, and append a deterministic FNV-64a hex hash of `derivePkg` to `CName`.

### H7. User methods named `field`/`elem`/`is_variant`/`payload`/`as_variant` shadow reflection intrinsics and break `@derive`

* **Locations:** `internal/sema/check_calls.go:809-876, 2273-2292`, `std/derive/derive.ll:24, 33, 40, 53, 70, 90, 99, 115, 129, 136, 159, 172, 178`

* **Root Cause:** `LookupMethod` (`check_calls.go:809`) runs unconditionally before `checkBuiltinMethodCall` (`872`). Any struct with a method `field(...)` or enum with `is_variant`/`payload`/`as_variant` shadows the reflection intrinsic even when the argument is a `comp.FieldInfo` or `comp.VariantInfo`.

* **Impact:** `@derive(Equatable, Comparable, Hashable, Copyable, Printable)` fails to compile on any type with a method sharing one of those names.

* **How to Fix:** Check whether the single call argument has type `comp.FieldInfo`/`comp.ElemInfo`/`comp.VariantInfo` before `LookupMethod` and dispatch directly to the reflection intrinsic (or expose `comp.field_of_val`/`comp.is_variant` functional intrinsics used by `std/derive`).

### H8. `@derive` / `@derivable` bugs: nested trial rollback leak, `Self` in generic/fn signatures, and top-level `const` fixpoint ordering

* **Locations:** `internal/sema/derivable.go:305-371, 628-687, 702-755`, `internal/sema/check_stmt_expr.go:296-329`

* **Root Cause:**

  1. **Trial rollback leak (`derivable.go:702-755`):** For mutually recursive generic types `A[T]`/`B[T]`, a nested trial for `B[NoEq].eq` sets `a.derivePassed[trialKey] = true` (`754`) without recording a `trialUndo`. When the outer `A[NoEq].eq` trial fails and rolls back `tmpl.Instances`, `a.derivePassed` stays `true`, causing subsequent checks of `B[NoEq].eq` to skip trial mode and emit hard errors.

  2. **Dropped `Self` in `substSelfInType` & `typeExprFromInterfaceType` (`derivable.go:305-371, 628-687`):** Neither recurses into `*types.Func` or generic `*types.Struct`/`*types.Enum` `TypeArgs` (e.g., `Result[Self, string]`, `fn(Self) bool`).

  3. **Top-level `const` vs `constGenericDeps` ordering (`check_stmt_expr.go:296-329`):** Top-level `const` initializers evaluate at `296-299`, before the `constGenericDeps` downgrade fixpoint at `311-329`, so mutually recursive concrete derived methods can be treated as `const` before downgrade propagation completes.

* **How to Fix:**

  1. Record `a.recordTrialUndo(func() { delete(a.derivePassed, trialKey) })` (and for `deriveFailed`) when `a.inDeriveTrial > 0`.

  2. Add `*types.Func` and generic `*types.Struct`/`*types.Enum` `TypeArgs` recursion to `substSelfInType` and `typeExprFromInterfaceType`.

  3. Run the `constGenericDeps` fixpoint inside `ensureFuncCheckedForComptime` whenever a top-level `const` eagerly checks a generic/derived function.

### H9. `@comptime_only` types and functions leak into MIR `VTables`, `typeCollector`, and `split.go`

* **Locations:** `internal/ir/lower.go:74-78, 104-108, 126`, `internal/codegen/split.go:250-264`

* **Root Cause:**

  1. `irp.VTables` (`lower.go:74-78, 126`) copies all `prog.VTables` without filtering `vt.Iface.IsComptimeOnly || types.FindComptimeOnlyType(vt.Concrete) != nil`.

  2. `lower.go:105` checks `if !fi.ComptimeOnly` when feeding `BodyMaps` into `typeCollector`, omitting `&& types.FindComptimeOnlyType(fi.Sig) == nil` (which lines `57` and `184` check).

  3. `split.go:250` (`markType`) has no `types.FindComptimeOnlyType(t) != nil` guard.

* **Impact:** Vtables for `@comptime_only` types/interfaces reference stripped types in C11/LLVM, causing backend compile errors.

* **How to Fix:** Filter `prog.VTables` in `lower.go:74-78`, add `&& types.FindComptimeOnlyType(fi.Sig) == nil` at `lower.go:105`, and add `if types.FindComptimeOnlyType(t) != nil { return }` at the top of `split.go:250`.

### H10. `comptime for` bugs: `colSlot` leak on unwind, wrong `else`, `for <- val` typecheck, `next <-` arena escape, and incomplete `CloneDecl`

* **Locations:** `internal/ir/lower_ops.go:159-200`, `internal/ir/lower.go:1160-1163`, `internal/sema/check_stmt_expr.go:2520-2524, 2614-2623, 6142-6147, 6208-6216, 6348-6388`, `internal/ast/clone.go:390-410`

* **Root Cause:**

  1. **Resource leak & unconditional `else` (`lower_ops.go:159-200`):** Non-fixed collecting `comptime for` never adds `colSlot` to `ownedLocals` (leaking pushed elements on `func <-` or outer breaks) and overwrites `colSlot` on `for <- val` (`lower.go:1160-1163`) without dropping existing elements. Void `comptime for ... else` (`lower_ops.go:197-198`) runs `else` even when `len(uf.Iters) > 0`.

  2. **`for <- val` type mismatch (`check_stmt_expr.go:2520-2524, 6208-6216`):** Non-fixed collecting `comptime for` checks `for <- val` against the item type instead of the collection type.

  3. **`next <- val` skips arena-escape check (`check_stmt_expr.go:2614-2623`):** Guarded by `else if !isCont`, allowing inner-scope `Arena` refs to escape via `next <- ref`.

  4. **Missing inference (`check_stmt_expr.go:6142-6147, 6348-6388`):** `comptime for` ignores `expected` when it is a fixed aggregate (`[3: int]`, struct, tuple) and ignores `tgt.yieldTypes` when every iteration yields via `next <- expr`.

  5. **Incomplete `CloneDecl` (`internal/ast/clone.go:390-410`):** Only clones `*VarDecl` and `*MethodDecl`; local `define` (`DeclStmt`) inside `comptime for` shares identical AST pointers across unrolled iterations, causing `BodyMaps` collisions. `VarDecl`/`MethodDecl` cloning also skips deep-cloning `TypeParams`, `RecvTypeParams`, and `Attributes`.

* **How to Fix:**

  1. Wrap non-fixed collecting `comptime for` in an outer `collectScope` owning `colSlot` (mirroring `lowerFixedCollectComptimeFor`), drop `colSlot` before overwriting on `for <- val`, and gate void `lowerForElse` on `len(uf.Iters) == 0`.

  2. Separate `itemExpectedType` from `breakExpectedType` on `cfTarget`; run the arena-escape check for value-carrying `next <- val` in collecting loops; unify `tgt.yieldTypes` when `isVectorInferred && targetColTy == nil`.

  3. Implement deep cloning for `*StructDecl`, `*EnumDecl`, `*InterfaceDecl`, `*DistinctTypeDecl`, `*TopComptimeIfDecl`, `*ImportDecl`, `*ExternHeaderDecl`, `*ExternFnDecl`, `TypeParams`, and `Attributes` in `internal/ast/clone.go`.

### H11. `comp.embed_str` / `comp.embed_bytes` sandbox TOCTOU, symlink/casing mismatches, and fail-open root fallback

* **Locations:** `internal/sema/comptime.go:1939-1944, 7373-7395, 7458-7508, 7523-7608`

* **Root Cause:**

  1. **TOCTOU & unbounded read (`7458-7508`):** Calls `EvalSymlinks` + `os.Stat` to check regular-file status and size, then opens a second time via `os.ReadFile` with no size cap. `makeByteListStruct` (`1939-1944`) also allocates a second full copy (2× peak Go heap).

  2. **Symlink/casing mismatch (`7379-7393, 7572-7579`):** `isWithinSandboxDir` compares raw un-canonicalized paths (can fail on macOS `/var` -> `/private/var`), and `lookupEmbedOverlay` uses case-sensitive `==` on macOS/Windows.

  3. **Fail-open fallback (`7523-7586`):** If `callFile` fails to match any `m.Path` in `Prog.Modules`, `mod` falls back to `s.rootMod`, potentially granting a dependency the workspace root instead of its own `pkg.Dir`.

  4. **Incomplete `AttemptedEmbedPaths` (`7464-7499`):** Only records `os.IsNotExist(symErr)`, not directories or broken symlinks, so LSP watchers miss repairs.

* **How to Fix:**

  1. Open once (`os.Open` / `O_NOFOLLOW`), call `f.Stat()` on the open FD to verify `IsRegular()` and size, and read via `io.LimitReader(f, remaining+1)`.

  2. Canonicalize both sides with `evalSymlinksBestEffort` in `isWithinSandboxDir` and `resolveEmbedModuleAndRoot`, and fold case on `darwin`/`windows` in both `isWithinSandboxDir` and `lookupEmbedOverlay`.

  3. Fail closed (`return "", ""`) if non-empty `callFile` doesn't match any loaded module after canonicalization, and record `AttemptedEmbedPaths` for all in-sandbox failures.

### H12. Native OS layer (`std/os`, `std/fs`, `std/time`, `std/net`, `std/http`) ABI, TOCTOU, `EINTR`, FD-leak, and HTTP DoS bugs

* **Locations & Root Causes:**

  1. **Silent `amd64` fallthrough on other Linux arches (`std/os/os.ll:355+`, `std/fs/fs.ll:129+`, `std/time/time.ll:19+`, `std/net/net.ll:58+`):** `comptime if(comp.TARGET.arch == arm64): ... else: ...` silently emits `amd64` syscalls and LP64 struct offsets on `linux/riscv64` (which uses `asm-generic`) and `linux/x86`.

  2. **Symlink TOCTOU in `os.remove_all` (`std/os/os.ll:468-603`):** Closes `dfd` (`517, 576`) before recursing by string path `"$target/$ch"`, allowing symlink swaps on ancestor components. macOS `readdir` (`570-575`) neither clears nor checks `errno`; Linux `unlinkat(AT_REMOVEDIR)` treats `-2` (`ENOENT`) as an error (`541-546`).

  3. **Missing `EINTR` retries & short-read error loss (`std/time/time.ll:190-197`, `std/net/net.ll:108-146`, `std/fs/fs.ll:243, 450, 477-482`, `std/os/os.ll:293, 488, 555`):** Darwin `sleep_ms` ignores `rem` and return code; `_net_poll_event` (`ppoll`/`poll`) aborts on `EINTR`; Darwin `File._read_raw` only checks `_fs_ferror` when `n == 0` (losing errors after partial reads).

  4. **Missing `O_CLOEXEC` / `SOCK_CLOEXEC` (`std/fs/fs.ll:210-267`, `std/net/net.ll:314-437, 987`):** Open FDs and sockets leak across `os.exec`.

  5. **`std/net/net.ll` & `std/os/os.ll` ABI/endianness:** `pollfd.events` written as `u8` instead of `i16` (`net.ll:115, 136`); Darwin `poll` timeout overflows `i32` (`net.ll:137`); raw `syscall(92, ...)` on macOS for `fcntl` (`net.ll:60-61, 72-73`) instead of `extern fcntl`; `sa_family` little-endian byte check (`net.ll:157-158, 784-785`); `_raw_tcp_connect` applies full timeout per `getaddrinfo` candidate (`496`); `shutdown`/`set_nodelay` drop `errno` (`730, 758`); `os.error_from_errno` calls non-thread-safe `strerror` (`os.ll:218`).

  6. **`std/http/http.ll` (`1075, 1123, 1158-1232`):** Unbounded header/body memory (OOM); `pos + size + 1` and `body_start + length` `int64` overflow; $O(N^2)$ chunked re-parsing on every `fill()`; comma-separated `Content-Length: 5, 5` rejected at line `1218` because `other.trim()` is unsplit; `_MessageReader.fill` drops `errno` (`1075`).

* **How to Fix:**

  1. Explicitly match `amd64` and `arm64` (and `riscv64` where asm-generic applies) and emit `comp.compile_error(...)` for unverified arches/32-bit targets.

  2. Anchor `os.remove_all` to open directory FDs using `openat(dfd, name, O_RDONLY|O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC)` and `unlinkat(dfd, name, ...)` (resetting/checking `errno` around Darwin `readdir`, treating `ENOENT` on final `rmdir` as ok).

  3. Retry `nanosleep`, `ppoll`/`poll` (recomputing remaining deadline), `openat`, and `pclose` on `EINTR`; check `_fs_ferror` whenever `n < count` in Darwin `File._read_raw`.

  4. Add `O_CLOEXEC`/`SOCK_CLOEXEC` (Linux) and `fcntl(F_SETFD, FD_CLOEXEC)` (Darwin).

  5. Fix `pollfd.events` (`i16`), clamp Darwin `poll` timeout to `INT32_MAX`, use `extern fcntl` on Darwin, read `u16` for `sa_family`, use `strerror_r`, and add header/body caps + overflow checks + incremental chunk state + comma-split `Content-Length` in `std/http/http.ll`.

## Part 3 — Medium-Severity Bugs (Parser, Formatter, `expand`, Cache, Docgen, & LSP)

### M1. `docgen` completely drops all `const` values/functions and mangles Phase 4 signatures

* **Locations:** `internal/docgen/docgen.go:283, 343-348`, `internal/docgen/links.go:350-510, 690-733, 857-892`, `cmd/lowlight/doc.go:65-76`

* **Root Cause:**

  1. The parser sets `IsBind: false, IsConst: true` for `const` (`parser.go:827-828, 941-943`), and both `ValidateLinks` (`docgen.go:344`) and `BuildSymbolIndex` (`links.go:491`) skip with `if !v.IsBind || ... { continue }`.

  2. `formatMethodSig` (`857`) and `formatVarDeclSig` (`878, 892`) hardcode `"bind "`.

  3. `formatTypeParams` (`690-703`) ignores `tp.IsConst`/`tp.ConstType`; `formatTypeStr` (`730-731`) hardcodes `"[N: "` and returns `""` for `*ast.ConstArgType` (rendering `Matrix[4, 4]` as `Matrix[, ]`).

  4. Both `docgen.go` and `links.go` iterate raw `f.Decls`, ignoring declarations inside `*ast.TopComptimeIfDecl`.

  5. `cmd/lowlight/doc.go:65-76` calls `docgen.Load` twice (double full-codebase `CheckForIDE`).

* **How to Fix:** Change `!v.IsBind` to `(!v.IsBind && !v.IsConst)`, emit `"const "` when `IsConst`, format `[N: const Type]` and `*ast.ConstArgType` and actual `StackArrayType` sizes, walk `mod.ActiveDecls()` / `*ast.TopComptimeIfDecl`, and reuse the single `docgen.Load` result in `runDoc`.

### M2. LSP can freeze/OOM on comptime evaluation in `CheckForIDE`

* **Locations:** `internal/lsp/server.go:112-128, 738, 1195`, `internal/sema/comptime.go:19-21, 284-317`, `internal/sema/types_pass.go:250-314`

* **Root Cause:** `comptime.go` never checks `a.ideMode`, runs with the full 1M-step / 64 MB budget, honors unbounded `@comptime_limit`, and has no wall-clock timeout inside the single-threaded synchronous `Server.Serve()` loop.

* **How to Fix:** When `a.ideMode` is true, clamp `stepLimit <= 100_000`, `memLimit <= 16 MB`, ignore higher `@comptime_limit` overrides, and add a per-evaluation wall-clock deadline (e.g., 250–500 ms) emitting a non-fatal warning on timeout.

### M3. LSP missing Phase 4–6 completions, hover, signature help, semantic tokens, and AST walker coverage

* **Locations:** `internal/lsp/attributes.go:18-39`, `internal/lsp/builtins.go:321-326, 467-537, 709-717, 739-829`, `internal/lsp/completion.go:303-420, 965-1035, 1306-1342, 1644-1654, 1846-1852, 1879-2438`, `internal/lsp/features.go:104, 145, 161-168, 333-772, 1665-2237, 2331-2457, 2514`, `internal/lsp/semantic_tokens.go:234-273, 558-1115`, `internal/lsp/code_actions.go:73, 993-1222, 1520-2662`, `internal/lsp/inlay_hints.go:88-115`

* **Root Cause & How to Fix:**

  1. Add `@derivable`, `@comptime_only`, and `@comptime_limit` to `BuiltinAttributes`, and include user-defined `@derivable` interfaces from `CheckedProgram` in `@derive(...)` completions.

  2. Add `comp.TypeKind`/`AttrInfo`/`FieldInfo`/`VariantInfo`/`ElemInfo`, all `comp.*_of`/`comp.implements`/`comp.type_name` functions, and reflection methods (`.field`, `.elem`, `.is_variant`, `.payload`, `.as_variant`, `.variant_index`, `.attr`, `.has_attr`, `.implements`) to `builtins.go`.

  3. Restrict `LookupBuiltinFunc` (`builtins.go:709-717`) to exact `n == name` so bare `log`/`size_of` don't false-match `comp.log`/`type.size_of`.

  4. Add explicit `comp.` and `type.` (and `comp.<Enum>.`) branches in `completeDotMember`, `computeSignatureHelp`, `hoverInExpr`, and `semantic_tokens.go:921-922` (highlighting `comp`/`type` as `semTokNamespace` and members as `semTokFunction | semModDefaultLibrary` and returning before falling through to `semTokProperty`).

  5. Add `"const"`/`"comptime"` to top-level completions and `isTypeAnnotationContext`; check `IsConst` for `KindConstant` and `semModReadonly | semModStatic`; handle `*ast.ComptimeExpr`, `*ast.TopComptimeIfDecl`, `*ast.DeclStmt`, and `*ast.ConstArgType` across all LSP AST walkers; add top-level `const` inlay hints.

### M4. LSP `EmbeddedFiles` watcher registration & path-key mismatches

* **Locations:** `internal/lsp/server.go:249-252, 729, 846, 967-1050, 1140, 1180-1203`

* **Root Cause:** `syncEmbeddedFileWatchers` (`1024`) passes unescaped paths as `GlobPattern` (breaks on `[`, `]`, `{`, `}`, `*`, `?`); `getOrCheckProgram` (`1180-1203`) caches `prog` without calling `syncEmbeddedFileWatchers()`; `s.docs` uses `URIToPath` while `s.cachedProgs` uses `filepath.Abs`, causing `invalidateDependentsLocked` (`1140`) to miss open dependents on non-cleaned/symlinked URIs.

* **How to Fix:** Escape glob metacharacters (or use `RelativePattern`), call `s.syncEmbeddedFileWatchers()` inside `getOrCheckProgram`, and normalize all `s.docs`/`s.docURIs` keys with `filepath.Abs(filepath.Clean(...))`.

### M5. Index-vs-generic call disambiguation misses non-bare callees (`obj.fns[i]()`, `grid[i][j]()`, `fns[mod.IDX]()`)

* **Locations:** `internal/parser/parser.go:2159-2209`, `internal/sema/check_calls.go:202-239`

* **Root Cause:** `scanGenericBracketLookahead` treats any `<expr>[<expr>](...)` as a generic call. Sema's fallback rewrite (`check_calls.go:202-239`) only fires when `call.Callee` is a bare `*ast.IdentExpr`, `NamedType.Module == ""`, and `sym.Type` is directly `*types.Vector`, `*types.StackArray`, or `*types.Map` (missing struct fields, nested arrays, `List`, `ref`, `Distinct`, custom `Indexable`/`KeyIndexable`, and unresolved top-level `const`/`bind` symbols).

* **How to Fix:** Generalize `check_calls.go:202-239` to rewrite any 1-arg bracketed call whose callee is a non-generic value expression, resolve top-level `sym.Type` first, use `typeExprToIndexExpr` (supporting `Module != ""`), and check `isIndexableType`/`isKeyIndexableType` (unwrapping `Ref`/`Distinct`).

### M6. `lowlight expand` omits `@derive` methods, drops `BlockExpr` flags, scrambles tuple/array order, and breaks generics

* **Locations:** `internal/sema/expand.go:59-182, 341-359, 406, 413-452, 476-477, 658-696, 913-920, 1096, 1170-1436`, `internal/driver/expand.go:38-44`, `internal/format/printer.go:648-680`

* **Root Cause & How to Fix:**

  1. **Missing `@derive` methods (`expand.go:59-131`):** Emit synthesized `MethodDecl`s for `fi.IsDerived` entries matching each `StructDecl`/`EnumDecl`.

  2. **Dropped `Label` & `Unsafe` (`expand.go:476-477`):** Copy `Label: v.Label, Unsafe: v.Unsafe` on `BlockExpr`.

  3. **Scrambled `CollectTuple` / `CollectArray` (`expand.go:413-429`):** Pre-allocate `elems` and place each item at `elems[it.TargetIdx]` (matching `CollectStruct`).

  4. **Broken generic specialization (`expand.go:141-182`):** Substitute `fi.TypeSubst` into `Params`/`Ret` and disambiguate `buildExpandedMethodSpec` headers instead of emitting unbound `T` and duplicate `Box[T].get` headers.

  5. **Other gaps:** Copy `Extern` on `*ast.FuncType` (`1096`), handle `StaticSet`/`StaticMap` (`1170-1436`), re-wrap `Distinct` values, emit `Module` qualifiers for imported types (`406, 913`), synthesize an accumulator for value-producing `comptime for` fallbacks (`443-452`), expand `*ast.DeclStmt`/`StructDecl`/`EnumDecl`/match patterns, expand all sibling files in `driver/expand.go:41`, and allow `'_'` in `isPlainIntLit` (`printer.go:648-680`) so synthetic `@comptime_limit(5_000_000)` isn't string-quoted.

### M7. Incremental `.o` cache key, `check --all-targets`, and backend `StackArray.to_string()` parity

* **Locations:** `internal/driver/incremental.go:59-71, 93-104, 118-205`, `internal/driver/driver.go:400-426, 525-536, 641, 699-709`, `internal/driver/expand.go:19-25, 30-34, 98-107, 123-125`, `cmd/lowlight/main.go:83-101`, `internal/codegen/cgen.go:774`, `internal/codegen/llvmgen_runtime.go:3815`

* **Root Cause & How to Fix:**

  1. **Host target omitted from `.o` cache key (`incremental.go:59-62, 150`):** Hash `checked.Target.Triple` instead of raw `opts.TargetTriple` (`""` on native builds) to prevent cross-host cache collisions and avoid misses between native builds and explicit `--target <host>`. Also hash `cc` binary fingerprint/`--version` and add LRU/mtime eviction to `.lowlight/cache/obj`.

  2. **Missing GCC guard for LLVM (`incremental.go:99-104`, `driver.go:699-709`):** Reject non-clang `cc` up front when `backend == BackendLLVM`.

  3. **`check --all-targets` (`driver/expand.go:19-25, 98-107`):** Deduplicate `triple` per `diagBucket` so repeated diagnostics on one target don't suppress `[targets: ...]`; add `x86_64-apple-darwin`, `x86_64-unknown-freebsd`, and `riscv64-unknown-linux-gnu` to `CanonicalTargets`.

  4. **`StackArray.to_string()` parity (`cgen.go:774` vs `llvmgen_runtime.go:3815`):** C11 emits `"[N: ..."` while LLVM emits `"[..."`. Align both backends.

  5. **Minor CLI/driver:** Add `-o` to `lowlight expand`, support `--flag=value` in `cmd/lowlight/fmt.go` and `doc.go`, and avoid calling `ResolveOptions` twice in `Expand`/`EmitC`/`EmitLLVM`/`BuildWithStats`.

### M8. Const-generic & Pass 0.5 edge cases (`Array[N, T]`, `CName` collision, `TopBodyMaps` mutation, generic method receivers)

* **Locations:** `internal/sema/types_pass.go:769-795, 885-905, 1079-1092, 2297-2308`, `internal/sema/comptime.go:3160-3180`, `internal/parser/parser.go:848-854`, `internal/sema/check_stmt_expr.go:7215-7265, 7329-7338`

* **Root Cause & How to Fix:**

  1. **`Array[N, T]` vs `[N: T]` (`comptime.go:3160-3180`):** Fall back to `evalConstIntWithSubst` when `sym.IsConstInt` is false so `Array[COMPUTED_N, int]` works just like `[COMPUTED_N: int]`.

  2. **Missing `ll_g_<modTag>_` prefix (`types_pass.go:1079-1092`):** Use `ll_g_<modTag>_<SanitizeCIdent>` in `collectTopLevelIntConsts` to avoid cross-module C symbol collisions.

  3. **Speculative `TopBodyMaps` pollution (`types_pass.go:885-905`):** Use a fresh `NewBodyMaps()` when `!reportErrors` in `selectTopComptimeIfBranch`, and recurse into literal/slice/range/interp nodes in `exprContainsCall` (`769-795`).

  4. **Generic method receiver params (`parser.go:848-854`):** Either reject `: const Type`/bounds on `Matrix[R: const int, C: const int].method` with a diagnostic or preserve and validate them against the receiver's template instead of silently stripping them.

  5. **`checkComptimeMatchExpr` (`check_stmt_expr.go:7215-7338`):** Reject `own` bindings in `comptime match` patterns, skip `evalComptimeMatchSelect` if Phase 1 had errors, and return `TypeVoid` when `!needValue`.

## Part 4 — Design Holes, Coverage Gaps, & Cleanup Improvements

### D1. Missing `_opt_hash` and `_opt_cmp` in `std/core/optional.ll`

* **Locations:** `std/core/optional.ll:1-35`, `internal/sema/types_pass.go:4187-4221`

* **Issue:** `lookupOptionalMethod` only wires `eq`, `copy`, and `to_string`, so `@derive(Hashable)` and `@derive(Comparable)` fail on any struct, enum, or tuple containing `?T`.

* **Fix:** Implement `_opt_hash` and `_opt_cmp` in `std/core/optional.ll` and wire `"hash"` and `"cmp"` in `lookupOptionalMethod`.

### D2. `distinct` types have no reflection unwrapping or `std/derive` support

* **Locations:** `internal/sema/check_calls.go:2252-2648`, `std/core/comp.ll:19`, `std/derive/derive.ll`

* **Issue:** `comp.kind_of[D]()` returns `distinct_kind`, but there is no `comp.underlying_of[D]()` or distinct-unwrapping in `std/derive/derive.ll` or reflection intrinsics.

* **Fix:** Add `distinct_kind` arms in `std/derive/derive.ll` (or `comp.unwrap_distinct(v)` / `comp.underlying_of[D]()`) so `distinct` types work cleanly with `@derive`.

### D3. Separate `comptime for` AST unroll limit from `@comptime_limit` step budget

* **Locations:** `internal/sema/check_stmt_expr.go:6058-6061`

* **Issue:** `maxUnroll` is overwritten with `fc.curFn.ComptimeLimit`, so `@comptime_limit(steps: 5_000_000)` inadvertently permits 5,000,000 unrolled AST statements.

* **Fix:** Keep `MaxComptimeUnrollIters` / `MaxComptimeUnrollStmts` independent of the interpreter step budget (or add a dedicated `@comptime_limit(unroll: N)` named argument).

### D4. Collecting `comptime for` edge cases

* **Locations:** `internal/sema/check_stmt_expr.go:6171-6193`, `internal/ir/lower_ops.go:347-373`

* **Issues:**

  1. Iterating `comp.fields_of[Src]()` to collect into `Dst` (same field count, different order) silently falls back to positional indexing `i` instead of erroring or matching by name (`6178-6193`). *Fix:* Emit a compile error when `FieldInfo.TargetType` does not match `collectStruct`.

  2. Filtered field subsets are rejected even when omitted fields are optional or have defaults (`6171-6175`), even though `comptime.go:5663-5670` already handles missing slots.

  3. `lowerFixedCollectComptimeFor` (`lower_ops.go:347-373`) emits dead `if (_ll_cf_def)` blocks even for required fields where sema forbids bare `next <-`. *Fix:* Guard with `if sf.HasDefault || isOpt`.

### D5. Unnecessary heap promotion in `List[T].truncate` and `List[T].pop`

* **Locations:** `std/core/list.ll:122, 351-361`

* **Issue:** `truncate` unconditionally calls `self._data_mut()` on `cap <= 0` `.rodata` buffers (copying the whole buffer onto the heap just to shrink `len`), inconsistent with `resize` (`268-278`). `pop` similarly promotes `Plain` `cap <= 0` buffers even though it only reads `self.data[len-1]`.

* **Fix:** Match `resize` in `truncate` (only drop elements if `self.cap > 0`, otherwise just update `self.len`), and fast-path `pop` for `self.cap <= 0 && type.is_plain[T]()`.

### D6. `os.os_name()` and `os.arch()` omit valid `comp.TARGET` enum variants

* **Locations:** `std/os/os.ll:650-667`

* **Issue:** Missing `openbsd`, `netbsd`, and `wasm64` (all fall through to `"unknown"`).

* **Fix:** Add `when comp.TargetOs.openbsd: "openbsd"`, `when comp.TargetOs.netbsd: "netbsd"`, and `when comp.TargetArch.wasm64: "wasm64"`.

### D7. Dead code and leftover Phase 6 backend synthesis to delete

* `internal/codegen/llvmgen.go:1742-1812`: Leftover inline composite copy branches in `emitCopyVal` (already removed from `cgen.go`).

* `internal/codegen/cgen.go:1098-1137` & `internal/codegen/llvmgen.go:3897-3902`: Dead `*types.Set`/`*types.Map` vtable cases referencing nonexistent `ll_set_has__`/`ll_map_get__`/`ll_map_has__`.

* `internal/codegen/runtime_h.go:404, 410` & `internal/codegen/llvmgen_runtime.go:904`: Unused `ll_str_push_byte` and `ll_str_push_bytes`.

* `internal/codegen/llvmgen_runtime.go:2839, 3039`: `ll_closure_tostr_p` / non-`Printable` interface `to_string` with no C11 counterpart.

* `internal/codegen/split.go:212-219, 266-273`: Unconditionally pulls `_hash_mix_u64` and all 6 protocol methods for every type in Phase 1 units.

* `internal/sema/types_pass.go:4682-4778`: `supportsToStringVisited` and `supportsCopyVisited` allocate and thread `seen map[types.Type]bool` without ever reading or writing it.

* `internal/sema/check_calls.go:154-157`: `DeriveAttrSpan`/`DeriveIfaceName` stored on cached `FuncInstance` can clobber diagnostics when two `@derivable` interfaces share a handler for the same `T`.

* `internal/lsp/server.go:685, 947, 1012, 1387`: Unify `nextServerReqID` instead of mixing sequential IDs with `time.Now().UnixNano()`.

## Suggested Fix Order (Before Starting Phase 7)

1. **PR 1 — Memory Safety, Miscompilation, & Backend Parity (`C1`–`C12`):**
   Fix `as_variant` `?T` flattening (`C1`), `own` moves on `ref`/rvalue/custom-`Drop` (`C2`), `StaticOpt`/`StaticFn` baking (`C3`, `C4`), negative enum discriminants (`C5`), LLVM tagged-enum `tag == 0` drop guard (`C6`), `ll_str_push`/`string.push_bytes` aliasing UAF (`C7`), `List` `cap <= 0` deep-copy promotion (`C8`), `StackArray.Equals` (`C9`), Phase 6 backend fallback ICEs (`C10`), `@packed` + LLVM `align` fixes (`C11`), and `enum.as_variant` lvalue guard (`C12`).

2. **PR 2 — Comptime Evaluator, Crossing, & Privacy Soundness (`H1`–`H10`, `D1`–`D4`):**
   Close `validateCrossesToRuntime` bypasses (`H1`), fix `@comptime_limit` step-reset & memory accounting (`H2`), fix heap `int64` overflows / `castTy` / pointer identity / UAR / cycle loops (`H3`), fix LIFO `Drop` & nil-pointer panics (`H4`), fix Phase 4/6 evaluator gaps (`H5`), harden `_` privacy & `@derive` trials/method shadowing (`H6`–`H8`), filter `@comptime_only` from `VTables`/`split.go` (`H9`), and fix `comptime for` lowering & `CloneDecl` (`H10`).

3. **PR 3 — Phase 5 Embedding & Native OS Layer Hardening (`H11`–`H12`, `D5`–`D6`):**
   Single-FD `O_NOFOLLOW` + `LimitReader` + canonical sandbox in `evalCompEmbed` (`H11`), explicit Linux arch guards, FD-anchored `os.remove_all`, `EINTR` retries, `O_CLOEXEC`/`SOCK_CLOEXEC`, `std/net` ABI fixes, and `std/http` caps/overflow/chunked fixes (`H12`).

4. **PR 4 — Tooling, Docgen, & Phase 7 LSP Readiness (`M1`–`M8`, `D7`):**
   Fix `docgen` `const` & Phase 4 signatures (`M1`), add LSP `IDEMode` comptime budgets (`M2`), add Phase 4–6 LSP builtins/attributes/`comp.`/`type.`/AST walkers (`M3`–`M4`), generalize index-vs-generic call rewrite (`M5`), complete `lowlight expand` (`M6`), fix incremental `.o` cache target key & `--all-targets` (`M7`), and clean up dead backend helpers (`D7`).