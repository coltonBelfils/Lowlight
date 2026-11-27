# Lowlight Post-Phase 7 Codebase Audit

**Scope:** Full static audit of the Lowlight compiler (`internal/`), CLI (`cmd/`), standard library (`std/`), and editor plugins (`editors/vscode`, `editors/nvim`, `editors/jetbrains`) following Phase 7 of `COMPTIME_IMPLEMENTATION_PLAN.md`.  
**Note on Verification:** Go is not installed in this audit environment, so every finding below was verified directly against the source files (line numbers match the uploaded snapshot).

---

## Executive Summary & Phase 7 Completion Status

A substantial amount of Phase 6 hardening landed cleanly (notably `C3`, `C6`, `C7`, `C10`, `C12`, `D1`, `D2`, `D5`, `D6`, and most of `H1`–`H12`). However, **two categories of urgent issues remain before moving to Phase 8**:

1. **Memory-safety & soundness bugs (including one critical regression):** The Phase 6 `C8` CoW fix introduced a **heap buffer overflow** in both `std/core/list.ll` and `cgen_collections.go` when `reserve(want)` is called on a `cap <= 0` view with `want < len`. Several partial Phase 6 fixes also leave critical holes: negative enum discriminants in `emitEnumTagAssert`, chained `own outer.inner.field(f)` double-frees, hardcoded LLVM `align 8` loads/stores and `@packed` field drops, `newCtSome` flattening, `evalUnary` nil-pointer panic, and a `_` private-field bypass in struct-collecting `comptime for`.
2. **Incomplete Phase 7 deliverables:** Several headline items in `COMPTIME_IMPLEMENTATION_PLAN.md` Phase 7 are missing or only partially wired:
   - **IDE comptime caps** have no wall-clock deadline and emit fatal `diag.Error` (clamping explicit `@comptime_limit`) instead of a non-fatal warning.
   - **`CheckForIDE` memoization** (per-package cached `const` values keyed by AST/`const`/`embed` hashes) is not implemented, and `diskASTCache` has a **data race + permanent AST corruption bug**.
   - **LSP target switcher** and **IDE expand preview** (`lowlight/expand`) do not exist yet, and all LSP AST walkers skip `ComptimeExpr`, `TopComptimeIfDecl`, and `DeclStmt`.
   - **`lowlight expand`** still omits `@derive(...)` generated methods and emits broken generic specializations.
   - **All three editor plugins** (`vscode`, `nvim`, `jetbrains`) still lack `const`, `comptime`, `comp.*`, and `type.*` syntax highlighting.

| Area | Critical | High | Medium | Low / Cleanup |
| :--- | :---: | :---: | :---: | :---: |
| IR, Backends (`cgen`/`llvmgen`), Types & Stdlib | 4 | 5 | 4 | 2 |
| Sema, Comptime Evaluator & Diagnostics | 2 | 7 | 6 | 5 |
| LSP & IDE Memoization | 3 | 3 | 4 | 3 |
| `lowlight expand`, `docgen`, Driver & Editors | 1 | 5 | 7 | 5 |
| Frontend (Lexer/Parser), Formatter, Manifest & Tests | 0 | 3 | 8 | 4 |

---

## Status of Prior Phase 6 Audit Items

| Phase 6 ID | Summary | Current Status | Notes |
| :--- | :--- | :--- | :--- |
| **C1** | `?T` flattening / `as_variant` | **Partially Fixed** | `if (v.as_variant(vr) as p)` fixed; `newCtSome` (`comptime.go:783`) and standalone `enum.as_variant` (`lower_calls.go:915`) still flatten. |
| **C2** | `own` moves via `.field`/`.elem`/`.payload` | **Partially Fixed** | Direct `IdentExpr` bases fixed; chained `own outer.inner.field(f)` still double-frees (**C3** below). |
| **C3** | Boxed `StaticOpt` (`HasOpt == true`) | **Fixed** | Guarded in `ExportStaticData`, `cgen.go`, and `llvmgen.go`. |
| **C4** | Baked `StaticFn` & closures | **Partially Fixed** | LLVM & `split.go` fixed; C11 emits non-constant compound literal in `static` initializers (**H8**). |
| **C5** | Negative enum discriminants | **Half-Fixed** | `enumVariantTagEqExpr` & LLVM `.tag` fixed; `emitEnumTagAssert` (`lower_calls.go:694`) missed (**C2**). |
| **C6** | LLVM tagged-enum `tag == 0` drop | **Fixed** | `llvmgen_runtime.go:2901-2905` guards `%tag == 0`. |
| **C7** | `ll_str_push` aliasing & `_string_grow` | **Fixed** | `runtime_h.go:401-434`, `std/core/primitives.ll:1016-1077`. |
| **C8** | `cap <= 0` `List[T]` CoW deep copy | **Regressed** | Deep copy added, but `reserve(want)` with `want < len` now overflows the heap (**C1**). |
| **C9** | `StackArray.Equals` / `Struct.Equals` | **Partially Fixed** | 2-arg `Array[N, T]` checked; 1-arg `Array[T]` and `Lang` checks still broken (**H10**). |
| **C10** | Phase 6 composite backend fallbacks | **Fixed** | Explicit `ICE:` panics added. |
| **C11** | `@packed` borrows & LLVM `align 8` | **Partially Fixed** | `ExprConstructArray`, `ExprWrapOptional`, `ExprOptionalValue`, enum `.tag`, and `@packed` drops still misaligned (**C4**, **H9**). |
| **C12** | `enum.as_variant` lvalue lowering | **Fixed** | Removed from `analyzePlace` and `lowerLValueAddrMode`. |
| **H1–H12** | Evaluator, privacy, unrolling, embed, OS | **Mostly Fixed** | Residuals tracked in **H2–H7**, **H9**, **H11**, **M1–M4**, **M11–M12**. |
| **D1, D2, D5, D6** | Optional hash/cmp, `distinct`, `List.pop/truncate`, `os_name/arch` | **Fixed** | Implemented and verified. |
| **D4, D7** | Dead `_ll_cf_def` & pre-Phase-6 backend dead code | **Unfixed** | Tracked in **L1** and **L2**. |

---

## Part 1 — Critical Findings (Memory Safety, Soundness, Concurrency, & Blocking Phase 7 Gaps)

### C1. Heap Buffer Overflow in `List[T].reserve` and `ll_vec_reserve` on `cap <= 0` Views When `want < len`
- **Locations:** `std/core/list.ll:87-111`, `internal/codegen/cgen_collections.go:16-37`
- **Problem & Trigger:** When promoting a `cap <= 0` `.rodata` or borrowed slice view (e.g., `const DATA = [1, 2, ..., 100]` copied to `let xs = DATA`, so `len == 100, cap == 0`) via `xs.reserve(1)` (any `want < len`), both `std/core/list.ll:89-95` and `cgen_collections.go:19-25` size `ncap` solely from `want` (`ncap = 4`), allocate `4 * sizeof(T)` bytes, and then copy all `self.len` (100) elements into the 4-element heap buffer (`list.ll:98, 102`; `cgen_collections.go:28, 30`). This immediately overflows the heap buffer and leaves `cap < len`. (LLVM's `ll_vec_ensure_owned_cap` at `llvmgen_runtime.go:3496-3497` correctly takes `max(nc1, len)`.)
- **Fix:**
  - In `std/core/list.ll:88-90`, compute `let need = if(self.cap <= 0 and min_cap < self.len): self.len else: min_cap` and grow `ncap` to `>= need` (returning early only when `self.cap > 0 and min_cap <= self.cap`).
  - In `internal/codegen/cgen_collections.go:18-19`, add `if (v->cap <= 0 && want < v->len) want = v->len;` before computing `ncap`.

### C2. Negative Enum Discriminants Still Broken in `emitEnumTagAssert`
- **Locations:** `internal/ir/lower_calls.go:694` (also triggered via `std/derive/derive.ll:144`)
- **Problem & Trigger:** `enumVariantTagEqExpr` (`lower_calls.go:672`) was fixed to use `NewLitInt64(vr.Value)`, but `emitEnumTagAssert` (`lower_calls.go:694`) still emits `NewLitInt(uint64(vr.Value), vr.Value < 0, types.TypeInt)`. For `Err = -1: string`, `uint64(-1)` is `18446744073709551615` with `Negated: true`, producing `-18446744073709551615` instead of `-1`. Calling `v.payload(vr)` — or `@derive(Copyable)` when the last variant has a negative discriminant (`std/derive/derive.ll:144`) — either fails backend compilation or panics at runtime.
- **Fix:** Replace `NewLitInt(uint64(vr.Value), vr.Value < 0, types.TypeInt)` at `internal/ir/lower_calls.go:694` with `NewLitInt64(vr.Value)`.

### C3. Chained Owned Place Moves (`own outer.inner.field(f)`) Shallow-Copy and Double-Free
- **Locations:** `internal/ir/lower_expr.go:147-165, 246-251`
- **Problem & Trigger:** In `lowerExprOwned`, moving a non-Plain field/element/payload via `struct.field`, `seq.elem`, `enum.payload`, or `distinct.underlying` only resolves `baseLoc` to the real local when `mem.Base` is a direct `*ast.IdentExpr` (`lines 147-150`). Any chained place like `own outer.inner.field(f)` enters the `else` branch (`lines 151-164`), shallow-reads `outer.inner` into `allocStmtTemp` without zeroing it in `outer`, zeros only the moved field in the temporary, and then `flushStmtTemps()` drops the temporary's remaining fields — double-freeing every sibling non-Plain field of `outer.inner` and leaving the moved field unzeroed in `outer`.
- **Fix:** Check whether `mem.Base` is an addressable owned place chain; if so, compute its lvalue address/place and zero the moved leaf slot in place, reserving `allocStmtTemp` strictly for true rvalues.

### C4. Hardcoded LLVM `align 8` Loads/Stores and Unaligned `@packed` Field Drops in Both Backends
- **Locations:** `internal/codegen/llvmgen.go:2401, 2572, 2578, 2581, 2681, 2688, 2690, 2711, 2718`; `internal/codegen/cgen.go:750`; `internal/codegen/llvmgen_runtime.go:2877-2878`
- **Problem & Trigger:**
  1. `ExprConstructArray` (`llvmgen.go:2578`) emits `store ..., align 8` for every element; for `[4: u8]`, `[4: i16]`, `[4: i32]`, or `[4: PackedStruct]`, elements at `i >= 1` are misaligned (LLVM UB / SIGBUS).
  2. `ExprWrapOptional` (`2688`) and `ExprOptionalValue` (`2718`) emit `align 8` on field `1` of `{ i1, T }`, which sits at offset 1/2/4 for `?u8`, `?i16`, `?i32`, and `?PackedStruct`.
  3. `ExprField` (`2401`) hardcodes `load i32, ptr %s, align 4` for enum `.tag` even inside a `@packed` struct.
  4. Dropping a `@packed` struct with a non-Plain field (e.g., `string` or `List[T]`) emits `ll_drop__T(&p->field)` in C11 (`cgen.go:750`, UB / `-Waddress-of-packed-member`) and passes an unaligned GEP directly to `@ll_drop__T` in LLVM (`llvmgen_runtime.go:2878`).
- **Fix:** Use `g.typeAlign(...)` (clamped to `>= 1`) in `llvmgen.go` (`1` for `.tag` when `g.isPackedLValue(v)`), and copy `@packed` non-Plain fields to an aligned stack temporary (`align 1` load -> aligned alloca) before calling `ll_drop__T` in both backends.

### C5. Struct-Collecting `comptime for` Bypasses `_` Private Field Visibility
- **Locations:** `internal/sema/check_stmt_expr.go:6520-6525, 6561-6609, 6644-6649`
- **Problem & Trigger:** When `comptime for (...) -> ForeignStruct` constructs a struct, `checkComptimeForExpr` checks slot counts and field types but never checks whether any field is unexported (`_`-prefixed) in another package without `@derive` authorization. Any package can forge an arbitrary instance of an opaque foreign struct. In addition, line `6566` reads `len(collectStruct.Fields)` without calling `fc.a.ensureStructFieldsForLayout(collectStruct, fe.Span)`, so lazily populated structs can report 0 fields.
- **Fix:** Call `fc.a.ensureStructFieldsForLayout(collectStruct, fe.Span)` before line `6566`, and enforce `IsExported(sf.Name) || SamePackage(...) || fc.hasDeriveAccess(collectStruct)` on every field of `collectStruct`.

### C6. `newCtSome` Flattening (`C1(5)` Residual) & `evalUnary` Nil-Pointer Panic (`H4(4)` Residual)
- **Locations:** `internal/sema/comptime.go:783-788` (callers at `9030, 9095, 9142, 9172, 9249, 9418`); `internal/sema/comptime.go:5220-5228`; `internal/ir/lower_calls.go:915-931`
- **Problem & Trigger:**
  1. `newCtSome` still short-circuits `if inner != nil && inner.kind == ctOpt { return inner }`, collapsing `Some(none)` to `none` in `vec.pop()`, `vec.get(i)`, `arr.get(i)`, `attr(name)`, `enum.as_variant`, and `map[k]` at compile time (and standalone `enum.as_variant` in `lower_calls.go:915-931` still flattens via `types.NewOptional(vr.Payload)`).
  2. `evalUnary` (`5220-5223`) dereferences `inf.Type` for negated integer literals without checking `inf != nil`, panicking on any synthesized/unchecked AST node where `infoFor` returns `nil`.
- **Fix:** Remove the `inner.kind == ctOpt` flattening from `newCtSome` (keeping optional coercion only in `wrapResult` and `inf.WrapOptional`), and guard `if inf != nil` in `evalUnary`.

### C7. Data Race & Permanent Cross-Overlay AST Corruption on Shared `diskASTCache`
- **Locations:** `internal/sema/program.go:24-37, 1293-1330`; `internal/sema/check_calls.go:228-264`
- **Problem & Trigger:**
  1. `diskASTCache` shares `*ast.File` pointers across concurrent `Check`/`CheckForIDE` calls. `check_calls.go:258-259` mutates `call.Callee` and `call.TypeArgs` in place under `astRewriteMu`, while `check_calls.go:228` and every reader in `sema` and `internal/lsp/` read those fields without holding `astRewriteMu` (Go data race).
  2. If an unsaved editor overlay temporarily changes an imported symbol from a generic function to an indexable slice of functions, `check_calls.go:258-259` permanently overwrites `call.Callee` and clears `call.TypeArgs = nil` on the cached on-disk `*ast.File` of importing files, corrupting them even after the overlay is fixed.
  3. `program.go:1297-1327` calls `os.Stat` before `os.ReadFile` (TOCTOU) and keys solely on `(modTimeNano, size)`, serving stale ASTs on equal-length edits within the same filesystem timestamp tick.
- **Fix:** Record rewritten index-call targets in `BodyMaps` instead of mutating `*ast.CallExpr` in place, and verify file content hash (or `f.Stat()` on the open descriptor + content hash for recent mtimes) in `diskASTCache`.

### C8. IDE Comptime Caps Have No Wall-Clock Deadline, Clamp `@comptime_limit`, and Emit Fatal Errors
- **Locations:** `internal/sema/comptime.go:23-25, 275-290, 304-311, 388-414, 691-696, 7528-7550`; `internal/sema/check_stmt_expr.go:6463-6490`
- **Problem & Trigger:**
  1. **No wall-clock cap:** `ctState` has no `time.Time` deadline (`time` is not even imported in `comptime.go`). Slow operations staying under 100k steps can still freeze the synchronous LSP loop.
  2. **False errors on valid builds:** In `ideMode`, explicit `@comptime_limit(...)` budgets are clamped down to `100_000` steps / `8 MB` / `256` unrolls, and hitting any IDE cap emits **`diag.Error`** with `"increase with @comptime_limit(...)"` — flagging valid code that builds cleanly via `lowlight build`. (Note: `ideMode` itself is cleanly isolated to `CheckForIDE` and does not leak into CLI builds.)
- **Fix:** Add an IDE-only `deadline time.Time` to `ctState` (checked every ~256 steps), tag IDE-cap aborts with `ideCapExceeded bool`, and emit a non-fatal **`diag.Warning`** in `reportAbort` and `checkComptimeFor` when `ideMode && ideCapExceeded`.

### C9. Missing Phase 7 LSP/IDE Features: Memoization, Target Switcher, and Expand Preview
- **Locations:** `internal/sema/program.go:744-773`; `internal/lsp/server.go:40-43, 182, 205-211, 666-725, 1195`; `internal/lsp/completion.go:289`; `internal/lsp/features.go:2425`
- **Problem & Trigger:**
  1. **No `CheckForIDE` memoization:** Every `didChange`, dot completion, and UFCS signature help re-checks and re-evaluates `std/core`, `std/derive`, all imports, and all `const` initializers from scratch; there is no per-package `const` cache keyed by `(targetTriple, astHash, depConstHash, embedSHA256s)`.
  2. **No target switcher:** `ServerConfig` only has `Format` and `InlayHints`; `CheckForIDE` does not accept a `TargetTriple`, and no `lowlight.target` config or `lowlight/setTarget` request exists.
  3. **No expand preview:** No `lowlight/expand` request or `lowlight.expand` command is registered in `server.go` to preview `ExpandModuleAST` on in-memory editor buffers.
- **Fix:** Add an IDE-scoped memoization cache to `CheckForIDE`, add `Target` to `ServerConfig` + `lowlight/setTarget` (invalidating `cachedProgs` on change), and expose `lowlight/expand` backed by `sema.ExpandModuleAST` + `format.FormatAST`.

### C10. `@derive(...)` Generated Methods Are Completely Omitted from `lowlight expand`
- **Locations:** `internal/sema/expand.go:59`, `internal/sema/program.go:450-466`, `internal/sema/derivable.go:451`
- **Problem & Trigger:** `expandDerives` stores generated `@derive(...)` wrapper methods in `mod.DerivedDecls`, while `ExpandModuleAST` iterates only `mod.ActiveDecls()` (`m.Decls`). Running `lowlight expand` on a file with `@derive(Equatable, Printable)` outputs zero derived methods.
- **Fix:** Iterate `mod.DerivedDecls` after `mod.ActiveDecls()` in `ExpandModuleAST` and emit the expanded `*ast.MethodDecl` for each derived method's `FuncInstance`.

---

## Part 2 — High-Severity Bugs

### H1. `evalCompElemsOf` Allows Unbounded Go-Heap OOM / Panic on Large `[N: T]`
- **Location:** `internal/sema/comptime.go:10336-10360`
- **Problem:** For `*types.StackArray`, `evalCompElemsOf` allocates `make([]types.Type, v.Size)` and `v.Size` descriptor structs *before* calling `allocMem` or `step`. `const _ = comp.elems_of([1_000_000_000: u8])` OOMs the compiler (or panics if `v.Size < 0`), bypassing all step, memory, and IDE budgets.
- **Fix:** Validate `v.Size >= 0` and charge `chargeBulkSteps` + `allocMem` before allocating `elemTypes` and `elems`.

### H2. `evalConstIntWithState` Resets Exhausted Memory Budget to 64 MB, Omits `subst`, and Accepts `CharLit`
- **Location:** `internal/sema/comptime.go:408-411, 3618-3654, 3697-3698`
- **Problem:** (1) When `caller` has 0 remaining memory (`remMem == 0`), `st.memLimit = 0` causes `allocMem` (`409-411`) to silently reset the budget back to `defaultComptimeMaxMem` (64 MB). (2) `st.frames[0]` still omits `subst: subst`, breaking const-generic array sizes like `[foo(N): int]`. (3) `['a': int]` still evaluates to `[97: int]`.
- **Fix:** Abort immediately if `caller != nil && remMem <= 0` (and distinguish explicit `0` from unset in `allocMem`), pass `subst: subst`, and reject `*ast.CharLit` / `ctChar`.

### H3. `validateCrossVal` / `validateCrossTypeOnly` Residual Bypasses (`H1(3)-(4)`)
- **Location:** `internal/sema/comptime.go:2966-2980, 3001-3012, 3082-3085, 3150-3153, 3167-3179`
- **Problem:** (1) `if _, _, ok := listStructBytes(val); ok { return "" }` (`3083, 3151`) skips validating `elemTy`, allowing `List[SecretByte]` (a `distinct u8` with custom `Drop`) to escape. (2) `*types.Struct` checks `t.HasCustomDrop` while `*types.Enum` checks `LookupConcreteMethod(t, "drop")`. (3) Neither calls `ensureStructFieldsForLayout(t, diag.Span{})`, so lazily populated structs can skip field validation.
- **Fix:** Validate `elemTy` before returning on `listStructBytes`, check both `HasCustomDrop` and `LookupConcreteMethod` consistently, and call `ensureStructFieldsForLayout` before walking `t.Fields`.

### H4. `reportAbort` Recursion-Collapsing Attributes Wrong Caller, `@track_caller` Leaves Skipped Frames, and `isStdLibFile` False-Positives
- **Location:** `internal/sema/comptime.go:594-600, 630-689, 8534-8545`
- **Problem:** (1) In collapsed recursion (`662`), `caller := ctFrameDisplayName(frames[i-runCount])` pairs the outer caller with `frames[i].callSpan` (which is inside the recursive function); for `runCount == 2`, both notes say `called from 'root'`. (2) When `@track_caller` lifts `origSpan` into user code, `isStdLibFile(origSpan)` is `false`, so `reportAbort` still prints the skipped `@track_caller` frames as trace notes. (3) `isStdLibFile` matches substring `"/std/"`, misclassifying any user project folder named `std/`.
- **Fix:** Pair `frames[i].callSpan` with `frames[i-1]` and emit the entry call separately; propagate the lifted frame index from `resolveCallerLocationSpan`; check `fr.mod.Pkg.IsStd` instead of substring `"/std/"`.

### H5. Comptime Resource Leaks & Stale `ctString.s` (`callLambda`, `Distinct` Drop, `evalAssignStmt`)
- **Location:** `internal/sema/comptime.go:2359-2373, 5398-5404, 5864-5866, 5975-5985, 7643-7702, 8555, 8632, 9925, 10120`
- **Problem:** (1) `callLambda` never calls `registerDropLocal` for `ModeOwn` params, leaking resources and skipping `Drop`. (2) `dropValue` on custom-`Drop` `*types.Distinct` returns after `dropFn` without dropping `dt.Base`. (3) Unpack assign (`5864`) skips dropping `_` elements; string `+=` (`5975`) skips `dropTargetBeforeOverwrite`, and `+`/`+=` charge `allocMem` for `sAlloc == nil` strings that `dropValue` never credits. (4) `compile_error`, `assert`, `+`, `+=`, `exportCtVal`, and `ctValToConstValue` read `v.s` directly instead of syncing from `v.sAlloc.bytes`.
- **Fix:** Register `ModeOwn` params in `callLambda`, drop `dt.Base` after custom `Distinct.drop`, drop `_` unpack values and use `dropTargetBeforeOverwrite` on `+=`, and centralize string reads through a `ctStringBytes(v)` helper.

### H6. `castWriteVal` Corrupts Cells on `ctFloat`, `nil` Heap Cells, and Cross-Width Int Writes
- **Location:** `internal/sema/comptime.go:2001-2017, 2184-2192`
- **Problem:** `castWriteVal` only handles `ctString` and `ctInt <- ctFloat`. Writing `ctInt` into a `ctFloat` cell via `ptr_cast[u64](p_f64)`, writing into a freshly allocated `nil` heap cell, or writing a different integer width/signedness overwrites the cell's `ctKind`/`prim` directly.
- **Fix:** Pass the target storage type (`al.elemTy` / `cur.ty`) into `castWriteVal` and convert bit patterns into the storage's canonical `ctKind`/`prim`.

### H7. `substSelfInType` & `typeExprFromInterfaceType` Drop `Self` in Generic/Function Signatures (`H8(2)`)
- **Location:** `internal/sema/derivable.go:305-371, 630-689`
- **Problem:** Neither function recurses into `*types.Func` parameters/returns or generic `*types.Struct`/`*types.Enum` `TypeArgs`, breaking `@derivable` interface methods like `fn parse(s: string) Result[Self, string]`.
- **Fix:** Add `*types.Func`, generic `*types.Struct`, and generic `*types.Enum` `TypeArgs` recursion to both functions.

### H8. C11 `StaticFn` Closure Emits Non-Constant Compound Literal in File-Scope Initializers (`C4` Residual)
- **Location:** `internal/codegen/cgen.go:2664-2672`
- **Problem:** `buildCStaticInit` emits `((ll_cl_...){ .fn = ..., .env = NULL, ... })`. Under ISO C11 §6.6, compound literals are not constant expressions; GCC rejects them in `static` globals and `static const` composite tables.
- **Fix:** Emit a plain brace-enclosed initializer `{ .fn = %s, .env = NULL, .copy_env = NULL, .drop_env = NULL }`.

### H9. `@packed` Address-Taking Bypasses, `Map.push` Double-Drop, and `split.go` `@comptime_only` Leak
- **Locations:** `internal/ir/lower_expr.go:1308-1328`; `internal/ir/lower.go:978-1005`; `std/core/map.ll:236-241`; `internal/codegen/split.go:250-264`
- **Problem:**
  1. `lowerEnumVariantCondBind` takes `&packed->e` on read-only `if (packed.e.as_variant(vr) as p):`.
  2. `lowerAssignStmt` (`lower.go:979`) skips `lowerPackedDirectPlace` for by-value operator overloads (`packed.x += y`), taking `&packed->x`.
  3. `std/core/map.ll:236-241` calls `zero_bytes(addr_of(entry), ...)` *after* `self.insert(own key, own val)` (panic double-free) and still runs custom `Drop` on zeroed `entry` fields at scope exit.
  4. `split.go:250-264` (`markType`) has no `FindComptimeOnlyType` guard, leaking reachable `@comptime_only` types into split units.
- **Fix:** Route read-only `@packed` `as_variant` through `holdSlot`; use `lowerPackedDirectPlace` when `fi.Sig.Params[0].Mode != types.ModeMut`; move `zero_bytes` before `self.insert` in `Map.push`; add `if types.FindComptimeOnlyType(t) != nil { return }` in `split.go:markType`.

### H10. `StackArray.Equals` / `Struct.Equals` / `Vector.Equals` Match 1-Arg `Array[T]` and User Structs Named `List`/`Array`
- **Location:** `internal/types/types.go:315-319, 336-347, 526-542`
- **Problem:** (1) 1-arg `Array[T]` matches `[N: T]` for any `N`. (2) `List` and `Array` are matched by bare `st.Name` without checking `st.Lang` / core package, so user structs named `List[T]` or `Array[N, T]` alias built-in `[T]`/`[N: T]`. (3) Unsigned `ConstValue` sizes compare `cv.IntVal` instead of `cv.UintVal`.
- **Fix:** Check `st.Lang == "list"` / core `Array` identity, require `len(st.TypeArgs) == 2`, and handle unsigned `ConstInt` via `cv.UintVal`.

### H11. Non-Fixed Collecting `comptime for` `colSlot` Leak, Unconditional Void `else`, and Darwin `sleep_ms` `EINTR`
- **Locations:** `internal/ir/lower_ops.go:156-235`; `std/time/time.ll:190-197`
- **Problem:** (1) Non-fixed collecting `comptime for` never registers `colSlot` in an outer `collectScope`, leaking pushed elements on `func <-` or outer-loop break, and void `comptime for ... else` runs `else` even when `len(uf.Iters) > 0`. (2) Darwin `time.sleep_ms` passes `null_ptr` for `rem` and does not retry on `EINTR` (`errno == 4`).
- **Fix:** Wrap `colSlot` in an outer `collectScope` and guard void `else` with `if len(uf.Iters) == 0`; add a `rem` + `EINTR` retry loop in Darwin `time.sleep_ms`.

### H12. `lowlight expand` Emits Invalid Generic Specializations and Drops `Distinct` Wrappers
- **Location:** `internal/sema/expand.go:141-182, 1304-1461`
- **Problem:** (1) `buildExpandedFuncSpec` strips `TypeParams` without substituting `T` in params/return/body (`bind id__int = fn(x: T) T: x`). (2) `buildExpandedMethodSpec` leaves `RecvTypeParams`/`TypeParams` untouched and emits duplicate identical declarations for every instantiation. (3) `staticDataToASTExpr` drops `*types.Distinct` wrappers on `StaticList`, `StaticArray`, `StaticTuple`, `StaticEnum`, and `StaticStruct`.
- **Fix:** Substitute `fi.TypeSubst` across params/return/receiver/body in both specialization builders, and wrap compound `StaticData` results with `ex.wrapValueTypeConversion(sd.Type, expr)`.

### H13. All LSP Walkers Ignore `ComptimeExpr`, `TopComptimeIfDecl`, and `DeclStmt` + Nil Panics on Incomplete ASTs
- **Locations:** `internal/lsp/features.go:389-762, 975, 1716-2090`; `completion.go:1556, 1888-2419`; `semantic_tokens.go:135, 599-1115`; `navigation.go:108-663`; `references.go:198-1970`; `code_actions.go:976-2585`; `testing.go:37-573`; `server.go:1124`
- **Problem:** (1) Hover, dot completion, go-to-def, references, rename, semantic tokens, document symbols, code actions, and `@test` lenses all fail inside `comptime { ... }`, `TopComptimeIfDecl`, and `DeclStmt`. (2) Unchecked `.GetSpan()` / `.String()` calls on nullable partial AST fields (`arm.Body`, `vs.Value`, `vs.Target`, `f.Value`, `v.Callee`, `v.Base`, `inf.Type`) panic the LSP server (which has no `recover()` in `handleMessage`).
- **Fix:** Add `*ast.ComptimeExpr` and `*ast.DeclStmt` cases plus `ActiveDecls()` / recursive `TopComptimeIfDecl` traversal across all walkers, guard nullable AST/type fields, and add a top-level `defer recover()` in `Server.handleMessage`.

### H14. LSP Hover, Signature Help, Completions, & Semantic Tokens Gaps for Phase 4–7
- **Locations:** `internal/lsp/features.go:103-105, 145-169, 881-885, 2331-2417`; `completion.go:308-313, 972-1042, 1452-1476, 1606, 1709`; `semantic_tokens.go:921-922`; `attributes.go:100-157`
- **Problem:** (1) Hover never prints `sym.ConstVal` for non-`int` constants (`string`, `float`, `bool`, structs, arrays) and hardcodes `bind` for generic `const` functions. (2) `comp.*`/`type.*` have zero signature help, no hover on `comp.TARGET`/`comp.<Type>`, and broken semantic tokens (`emitFirstIdentInSpan` fails on `"comp.embed_str"` and falls through to `semTokProperty`). (3) `completeTypeNames` includes value `comp.TARGET` in type annotations, while `let x: comp.|` fails. (4) Completions hardcode `bind` and lack constness-aware `SortText` ordering (`A13`). (5) `@derive(...)` completion/hover ignores user-defined `@derivable` interfaces.
- **Fix:** Format `sym.ConstVal` in hover; wire `comp`/`type` namespaces into signature help, hover, semantic tokens, and type completions; boost `IsConst` items in `const`/`comptime` contexts; include user `@derivable` interfaces from `prog`/`mod`.

### H15. `docgen` Drops `[N: const Type]`, Hardcodes `[N: T]`, and Omits Comptime Attributes & Downgrade Status
- **Locations:** `internal/docgen/links.go:45-59, 515-538, 722-797, 811-929`; `internal/docgen/docgen.go:116`; `internal/site/api.go:279-364`; `internal/docgen/markdown_out.go:171-178`
- **Problem:** (1) `formatTypeParams` ignores `tp.IsConst`/`tp.ConstType`, rendering `Matrix[T, R: const int, C: const int]` as `Matrix[T, R, C]`. (2) `formatTypeStr` hardcodes `"[N: " + ...` for `*ast.StackArrayType` and returns `""` for `*ast.ConstArgType` (`Matrix[f64, , ]`). (3) `@comptime_only`, `@comptime_limit`, `@derivable`, `@derive`, `const fn` badges, evaluated `ConstVal`, and `FuncInstance.ConstFail` downgrade reasons are not rendered (`docgen.Load` also only checks `files[0]`, leaving other modules syntax-only).
- **Fix:** Format `tp.IsConst`/`ConstType`, `StackArrayType.Size`, and `ConstArgType.Expr`; extend `SymbolEntry` with attributes, constness, evaluated value, and downgrade reason populated from `CheckedProgram`.

### H16. Incremental `.o` Cache Key Omits `EmbeddedFiles`, Compiler Version, and Local C Headers
- **Location:** `internal/driver/incremental.go:59-71, 148-155`
- **Problem:** `computeObjCacheKey` ignores `checked.EmbeddedFiles`, `driver.Version`, `cc` identity/version, and contents of local `checked.ExternHeaders` (only hashing flag strings), and truncates the SHA-256 key to 20 hex chars (80 bits).
- **Fix:** Hash `driver.Version`, resolved `cc` fingerprint, sorted `checked.EmbeddedFiles` digests, and local `ExternHeaders` file contents into `computeObjCacheKey`, and use at least 32–64 hex chars.

### H17. Frontend, Formatter, & Manifest High-Severity Bugs
- **Locations:** `internal/format/printer.go:2868-2882`; `internal/parser/parser.go:1970-2021, 2205, 2225-2242`; `internal/sema/check_calls.go:228-267`; `internal/manifest/resolve.go:63-65`; `internal/driver/driver.go:819`; `internal/manifest/manifest.go:478-516`
- **Problem:**
  1. `printMatchExpr` omits `()` on 0-scrutinee `comptime match() EvalType:`, producing unparseable `comptime match EvalType:`.
  2. Index-vs-generic call disambiguation only rewrites bare `IdentExpr` with `ta.Module == ""`, breaking `obj.fns[0](x)`, `grid[0][1](x)`, `fns[mod.IDX](x)`, and `fns[idxs[0]](x)`; `scanGenericBracketLookahead` (`2205`) can also silently drop `targs` on non-struct-lit bases like `foo()[0][x: 1]`.
  3. `manifest.Resolve` and `driver.RunTests` ignore `lowlight.toml`'s `[build].target`, diverging from `sema`/`comp.TARGET`.
  4. `EffectiveDeps` and `EffectiveTargetFlags` sort `[target.<selector>]` alphabetically, so `[target.unix]` overrides `[target.linux]` and `[target.darwin]`.
- **Fix:** Emit `()` when `len(v.Scrutinees) > 0 || v.EvalType != nil`; guard `scanGenericBracketLookahead` and generalize `typeExprToIndexExpr` + call rewrite; fall back to `ActiveManifest.Build.Target` in `Resolve` and `RunTests`; sort target selectors by specificity tier.

### H18. All Three Editor Plugins Lack `const`, `comptime`, `comp.*`, and `type.*` Highlighting
- **Locations:** `editors/vscode/lowlight/syntaxes/lowlight.tmLanguage.json:294, 308, 360, 401-412`; `editors/nvim/syntax/lowlight.vim:18-20, 53-57`; `editors/jetbrains/src/main/kotlin/dev/lowlight/intellij/syntax/LowlightLexer.kt:19-25, 39-48`; `editors/vscode/lowlight/package.json:45-96`
- **Problem:** While `@attr` highlights generically across all three editors, `const` and `comptime` are absent from every keyword list; VS Code `#declarations` only matches `bind` (breaking `const foo = fn(...)` and `const T.m = fn(...)`); `comp.*`/`type.*` namespaces are unhighlighted (while stale bare `size_of`/`align_of`/`is_plain` remain in Neovim/JetBrains); and none of the three plugins expose Phase 7 target-switcher or expand-preview commands.
- **Fix:** Add `const` and `comptime` to all three grammars/lexers, match `(bind|const)` in VS Code `#declarations`, add `comp.*`/`type.*` rules, and wire target-switcher + expand-preview commands once **C9** lands.

---

## Part 3 — Medium-Severity Bugs & Oversights

| ID | Area & Location | Issue & Trigger | Suggested Fix |
| :--- | :--- | :--- | :--- |
| **M1** | **Sema** `types_pass.go:317-326`, `check_stmt_expr.go:6459-6465` | `@comptime_limit(unroll: N)` on top-level `const` is stored in `sym.ComptimeUnrollLimit` but never read (`fc.curFn == nil`). | Also read `fc.curTopVar.Sym.ComptimeUnrollLimit` in `checkComptimeForExpr`. |
| **M2** | **Sema** `check_stmt_expr.go:6517-6551`, `comptime.go:7014-7037, 8669-8682` | (1) `comptime for` without `-> T` ignores fixed-aggregate `expected` (`[3: int]`, tuple, struct). (2) `comptime match` skips `registerDropLocal` and multi-case/non-variant bindings. (3) `min`/`max` still call `cmpVal` which returns `0` for `NaN`. | Check fixed aggregates on `expected`; register drops & handle all cases in `evalMatch`; handle `math.IsNaN` in `min`/`max`. |
| **M3** | **Sema** `comptime.go:2261-2263, 7526-7579, 8329-8332, 8513-8532` | (1) `isWithinSandboxDir` compares non-canonical paths at `8330`, falsely rejecting valid macOS `/private/var` vs `/var` embeds. (2) `makeByteListStruct` doubles peak Go heap via redundant `copy`. (3) Tighter `@comptime_limit(memory: M)` on a callee does not check already-allocated `s.memBytes`, and restoring caller `stepLimit` after a higher-limit callee aborts the caller's next statement. | Canonicalize both paths in `8330`; take ownership of `data` slice; check `s.memBytes > s.memLimit` on entry and preserve caller remaining steps on exit. |
| **M4** | **LSP** `server.go:205-211, 1022-1025, 1052-1203` | (1) `getOrCheckProgram` serves stale `cachedProgs` for unopened files and skips `syncEmbeddedFileWatchers`. (2) Editing a path-dep `lowlight.toml` doesn't invalidate dependents; unresolved-import check skips `ActiveDecls()` and imported modules. (3) `cachedProgs` is unbounded; `didChangeConfiguration` doesn't refresh. (4) `syncEmbeddedFileWatchers` passes raw paths as `GlobPattern` (breaks on `[ ] { } * ?`). | Validate source hash + cap `cachedProgs` + call `syncEmbeddedFileWatchers`; invalidate all `cachedProgs` on manifest edits; escape glob metacharacters. |
| **M5** | **LSP** `protocol.go:593-618`, `server.go:335, 788-813, 888` | (1) `URIToPath` double-unescapes `u.Path` (`%2520` -> space) and drops Windows UNC hosts. (2) `DiagnosticRelatedInformation` silently drops empty-span frames and ignores `s.docURIs` (breaking drive-letter/symlink URI matching). | Remove redundant `url.PathUnescape`; append empty-span related notes to `fullMsg`; use a `uriForPathLocked` helper preferring `s.docURIs`. |
| **M6** | **Expand & CLI** `sema/expand.go:379-466, 881-914, 1326-1340`; `driver/expand.go:41-57`; `cmd/lowlight/main.go:83-101, 344-466` | (1) Void `comptime for` emits `Else` unconditionally; general value `comptime for` discards iteration values; `CollectStruct` omits `Module`; `exprContainsLoopUnwind` misses `UnsafeExpr`/`ComptimeExpr`/`FallbackUnwind`. (2) `driver.Expand` only expands `EntryMod` on multi-file packages. (3) `lowlight expand` rejects `-o` and silently accepts `--workspace` before failing. (4) Empty `StaticList` emits `List[T].new()` even if `List` is unimported/shadowed. | Fix `comptime for` unrolling & unwind scan; expand all modules in `EntryPkg`; pass `allowOut=true` and add `allowWorkspace` validation; force explicit decl type on empty `VecLit`. |
| **M7** | **Docgen** `docgen/links.go:540-546, 921`; `docgen/markdown_out.go:172` | (1) `TopComptimeIfDecl` indexes all branches unconditionally, producing duplicate symbols and HTML `id`s. (2) Directory-named modules emit broken `[src](std/core.ll#L...)` links instead of `std/core/core.ll`. (3) `formatVarDeclSig` panics if `v.Value == nil` on incomplete syntax. | Deduplicate/annotate conditional decls; compute Markdown source links via `filepath.Rel`; guard `v.Value != nil`. |
| **M8** | **Driver & Diag** `driver/expand.go:98-132`, `driver/driver.go:391-395`, `diag/diag.go:273-279` | (1) `CheckAllTargets` appends duplicate target triples per bucket, which can falsely suppress `[targets: ...]`. (2) `diag.FormatOne` omits source snippets for `RelatedInfo` and drops `line:col` when `File == ""`. (3) `driver.Check` bundles warnings into `CompileError` while `CheckAllTargets` separates them. | Deduplicate `seenTarget` per bucket; render `RelatedInfo` snippets and check `File != "" \|\| Line > 0`; unify warning/error separation. |
| **M9** | **Stdlib** `std/net/net.ll:123-145, 515, 749, 777`; `std/http/http.ll:1097`; `std/core/map.ll:61-62`; `std/core/set.ll:58-59` | (1) `_net_poll_event` resets `timeout_ms` on `EINTR`; `_raw_tcp_connect` gives each candidate a full timeout; `shutdown`, `set_nodelay`, and `_MessageReader.fill` drop `os.last_errno()`. (2) `Map.with_capacity` / `Set.with_capacity` can overflow `bcap *= 2` into an infinite loop on huge `cap`. | Track a monotonic deadline across `poll` and `getaddrinfo` loops; wrap `os.last_errno()`; add upper-bound guard before doubling `bcap`. |
| **M10** | **Backend Parity** `cgen.go:804-805` vs `llvmgen_runtime.go:3975`; `cgen_collections.go:32` vs `llvmgen_runtime.go:3527` | `StackArray.to_string()` prints `[3: 1, 2, 3]` in C11 vs `[1, 2, 3]` in LLVM; LLVM's non-Copyable vector view panic mistakenly passes `sliceOobSym` (`"slice bounds out of range"`) as message and filename. | Align `StackArray.to_string()` format and intern the proper panic message in `llvmgen_runtime.go:3527`. |
| **M11** | **Parser & Formatter** `parser.go:848-857, 2428-2431, 2499-2504, 2852-2856`; `printer.go:775-811` | (1) Unlabeled `comptime for` overwrites `Span.Start` with `for` instead of `comptime`. (2) Parenthesized `(outer: comptime for ...)` is misclassified as a function literal. (3) Const receiver type params on `MethodDecl` (`Matrix[N: const int].len`) are silently stripped. (4) Braced `TopComptimeIfDecl` drops/misplaces empty-block and trailing comments. | Preserve `ctTok.Span.Start`; add `KwComptime`+`KwFor` check in `looksLikeFuncLit`; reject or store `t.IsConst` on receivers; flush comments before `}` in `printTopComptimeDeclsBranch`. |
| **M12** | **Manifest/Target** `internal/manifest/target.go:55, 88-98, 140-230` | (1) `isBigEndianTriple` scans all segments and false-positives on `"eb"` (e.g., `wasm32-web-wasi` -> big-endian), while `ParseTarget("")` hardcodes `"little"`. (2) 32-bit arches (`mips`, `arm`, `riscv32`, `ppc`) get `PtrSize = 8`. (3) `MatchesSelector` misses aliases (`x86_64`, `aarch64`, `macos`), ignores `os_arch` (`_`) and `unix-amd64`, and false-matches unknown triples. | Check only arch in `isBigEndianTriple` + inspect `runtime.GOARCH`; handle 32-bit arches for `PtrSize`; normalize selectors and reject `unknown` matches. |
| **M13** | **Tests** `internal/lsp/lsp_test.go:2628-2645`, `driver_test.go:152-382`, `docgen_test.go:118-161`, `comptime_phase6_test.go:199-214` | `TestLSPComptimeDotCompletionAttributesAndConst` comments a `semanticTokens` check it never runs; zero LSP tests for `type.`, `const` hover, timeouts, memoization, target switching, or expand preview; `driver_test.go:214` checks bare `"48"`; `docgen_test.go` never asserts `cond_helper` or `comptime if(false)` pruning; `comptime_phase6_test.go:211` asserts `ideRep.HasErrors()` contradicting the Phase 7 warning spec. | Add the missing assertions and tests once the corresponding Phase 7 fixes land. |

---

## Part 4 — Low-Severity Issues, Dead Code, & Documentation Drift

- **L1 (Dead backend code — Phase 6 `D7`):** Pre-Phase-6 `ll_set_has__`/`ll_map_get__`/`ll_map_has__` vtable & call interceptors (`cgen.go:1128-1167`, `llvmgen.go:3716-3765, 4000, 4047`), leftover inline composite copy branches (`llvmgen.go:1819-1917`), `ll_closure_tostr_p` (`llvmgen_runtime.go:2988, 3034-3075, 3188`), unused `ll_str_push_byte(s)` (`runtime_h.go:417-434`), and unconditional protocol pulls (`split.go:212-215, 266-273`).
- **L2 (Dead IR — Phase 6 `D4`):** `lowerFixedCollectComptimeFor` (`internal/ir/lower_ops.go:383-409`) emits dead `if (_ll_cf_def)` blocks for required non-optional struct fields.
- **L3 (`types_pass.go:1098-1101, 3822-3830`):** `collectTopLevelIntConsts` sets an unprefixed `CName` (`SanitizeCIdent(vd.Name)` without `ll_g_<tag>_`) that Pass 1D reuses verbatim, risking C symbol collisions across packages or with libc names (`errno`).
- **L4 (`comptime.go:7893-7906`, `derivable.go:732-773`, `comptime.go:7410-7451`):** `addr_of` on sub-places allocates a fresh stack address on every call (`ptr_addr(addr_of(s.x)) != ptr_addr(addr_of(s.x))`); `a.trialUndos` is never cleared after successful outermost `@derive` trials; `CallClosureValue` omits temporary drops for `ModeRead` rvalue args.
- **L5 (`lsp/features.go:2184-2221`, `lsp/semantic_tokens.go:768-772`, `lsp/server.go:685, 1387`):** `DocumentSymbol` labels `const`/`let` as `"bind"`; local `const VarStmt` omits `semModReadonly`; `window/showDocument` uses `UnixNano()` instead of `nextServerReqID`.
- **L6 (`sema/expand.go:199-201, 211-217, 1193`, `site/highlight.go:33-35`, `docgen/links.go:170-208`):** Empty `if !isConst {}` branch and unused `typeArgs` in `expand.go`; `looksLikeLowlight` omits `"const "`/`"comptime "`; `builtinUniverseTargets` omits `never`.
- **L7 (`cmd/lowlight/main.go:162-336`, `driver/driver.go:372-525`, `driver/incremental.go:132-151`):** Extra trailing newline on `CompileError` in `build`/`run`/`test`; redundant `ResolveOptions` calls; `targetKey` vs `--target` consistency in `buildTwoPhase`.
- **L8 (`lexer/lexer.go:135-139`, `format/printer.go:746, 1297-1307, 2771-2798`, `manifest/target.go:34-42, 245-278`):** Single mismatched delimiter disables ASI for the rest of the file; `TopComptimeIfDecl` `else:` passes `if` start offset; comment before braced loop body misaligns `} else:`; `MatchesTargetSuffix` misses `_x86_64` / `_aarch64`.
- **L9 (Documentation drift):** `COMPTIME_IMPLEMENTATION_PLAN.md` still lists `B10`–`B12` as open (closed in `TODO.md:69-71`), omits `[Done]` on Phase 6, and points to `std/core/core.ll` instead of `std/core/target.ll`; `TODO.md:109-112` still has Phase 7 checkboxes unchecked.

---

## Suggested Remediation Sequence (Before Starting Phase 8)

1. **PR 1 — Memory Safety, Miscompilation, & Soundness (`C1`–`C6`, `H1`–`H11`):**
   - Fix the `List.reserve`/`ll_vec_reserve` heap buffer overflow (`C1`), `emitEnumTagAssert` negative discriminants (`C2`), chained `own` moves (`C3`), LLVM `align` & `@packed` drops/borrows (`C4`, `H9`), C11 `StaticFn` compound literal (`H8`), `StackArray.Equals` (`H10`), and `colSlot`/Darwin `EINTR` (`H11`).
   - Close sema/comptime holes: struct-collecting `comptime for` privacy (`C5`), `newCtSome` & `evalUnary` (`C6`), `evalCompElemsOf` OOM (`H1`), `evalConstIntWithState` (`H2`), `validateCrossVal` (`H3`), `reportAbort`/`@track_caller` (`H4`), `callLambda`/`Distinct`/`evalAssignStmt` leaks (`H5`), `castWriteVal` (`H6`), and `substSelfInType` (`H7`).
2. **PR 2 — Complete Phase 7 LSP, Memoization, & IDE Caps (`C7`–`C9`, `H13`–`H14`, `M4`–`M5`):**
   - Eliminate in-place `*ast.CallExpr` mutation on `diskASTCache` (`C7`), add IDE wall-clock deadline + non-fatal warning on IDE cap exceedance (`C8`), add `CheckForIDE` per-package memoization + target switcher + `lowlight/expand` endpoint (`C9`), fix all LSP AST walkers + nil guards (`H13`), and finish `ConstVal` hover / `comp.*` & `type.*` signature help & completions (`H14`).
3. **PR 3 — `lowlight expand`, `docgen`, Incremental Cache, & Editor Plugins (`C10`, `H12`, `H15`–`H16`, `H18`, `M6`–`M8`):**
   - Emit `@derive` methods (`C10`) and fix generic specialization/`Distinct` folding (`H12`) in `expand.go`; fix `docgen` const-generic/attribute rendering (`H15`); add `EmbeddedFiles`/version/headers to `.o` cache keys (`H16`); update VS Code, Neovim, and JetBrains grammars + commands (`H18`).
4. **PR 4 — Frontend, Formatter, Manifest, Stdlib Polish, & Dead-Code Cleanup (`H17`, `M1`–`M3`, `M9`–`M13`, `L1`–`L9`):**
   - Fix `printMatchExpr` 0-arg `EvalType`, index-vs-generic disambiguation, `[build].target` propagation, and `[target.*]` specificity (`H17`), harden `std/net`/`std/http`/`Map`/`Set` (`M9`), align C11/LLVM parity (`M10`), add missing Phase 7 tests (`M13`), delete leftover `D7` dead code (`L1`), and sync `TODO.md` / `COMPTIME_IMPLEMENTATION_PLAN.md` (`L9`).
