# Lowlight `comptime` Implementation Plan

**Design Specification:** [`COMPTIME_DESIGN.md`](./COMPTIME_DESIGN.md)  
**Roadmap Tracking:** [`TODO.md`](./TODO.md) (Section 1 mirrors every phase and atomic step below 1:1)  
**Review:** [`COMPTIME_REVIEW.md`](./COMPTIME_REVIEW.md) (bugs `B1`–`B12`, holes `H1`–`H17`, additions `A1`–`A15`, cited below)

This plan breaks the finalized `comptime` design into 8 incremental, atomic phases across the Go compiler, standard library, CLI, LSP, editors, tests, and documentation. Each phase leaves the compiler building and passing all strict ASan/UBSan + `LOWLIGHT_CHECK_LEAKS=1` checks.

---

## Architecture Overview

```
Source (.ll)
  │
  ▼
[lexer / parser] ──► `const`, `comptime`, `comptime if/for/match`, `[N: const Type]`,
  │                  top-level `comptime if`, `@comptime_only`, `@comptime_limit`, `@derivable`
  ▼
[sema: Pass 1]  ──► Evaluate top-level `comptime if` conditions & prune untaken declarations
  │                 Register types, `@derivable` hooks, and `const` vs `bind` symbols
  ▼
[sema: Evaluator] ─► Real Lowlight evaluator + simulated heap (`std/` `unsafe`)
  │                 Opt-in `const`, conditional generics, `comp.*` & `type.*` namespaces,
  │                 `Related` traces, step/depth/memory budgets
  ▼
[sema: Unrolling] ─► Prune local `comptime if/match`, unroll `comptime for` & collecting `T:`,
  │                 resolve `v.field(f)`, `t.elem(e)`, `is_variant`/`payload`/`make_variant`,
  │                 instantiate `std/derive` + tuple generics
  ▼
[ir / codegen]  ──► Static `.rodata` baking (`Plain`, `string`, `List[T]` with `cap = 0`);
                    omit `@comptime_only` code; skip startup init / exit `Drop` for `const` globals
```

---

## Phase 1: Lexer, AST, Parser, & Formatter (`const` + `comptime` Syntax) [Done]

**Goal:** Parse and format all new `const` and `comptime` syntax cleanly without changing runtime semantics yet.

1. **Keywords & Lexer ([`internal/token/token.go`](./internal/token/token.go), [`internal/lexer/`](./internal/lexer)):**
   * Add `KwConst` (`"const"`) and `KwComptime` (`"comptime"`) to `Kind`, `kindNames`, and `keywords` (30 → 32 keywords), and verify ASI (`blockLevelAfter`) rules handle `const` declarations and `comptime` prefix/control-flow expressions identically to `bind` and `unsafe`.
2. **AST Nodes ([`internal/ast/ast.go`](./internal/ast/ast.go)):**
   * Add `IsConst bool` to `VarDecl` and `MethodDecl` (distinguishing `const` vs `bind` vs `let`).
   * Add `IsConst bool` and `ConstType TypeExpr` to `TypeParam` (`[N: const Type]`).
   * Add `ComptimeExpr` (mirroring `UnsafeExpr`), `TopComptimeIfDecl` (for top-level `comptime if`), `IsComptime bool` on `IfExpr`/`ForExpr`/`MatchExpr`, and expression generic argument slots in call/type nodes.
3. **Parser — Declarations & Control Flow ([`internal/parser/parser.go`](./internal/parser/parser.go)):**
   * Parse `KwConst` in `parseTopVarOrMethodDecl` and local variable declarations (`const x = ...`, `const fn_name[T] = ...`, `const Type.method = ...`).
   * Parse `[N: const Type]` in `parseTypeParams`, `comptime expr` / `comptime { ... }` in `parseUnaryExpr`, `comptime if`/`for`/`match` (including collecting `comptime for(...) T:`), and `TopComptimeIfDecl` at file top level.
4. **Parser — Value Generic Call/Type Disambiguation & Tests ([`internal/parser/parser.go`](./internal/parser/parser.go)):**
   * Extend `looksLikeGenericTypeArgs` and bracket argument parsing for value expressions (`Matrix[2 + 2, 4]`, `zeros[8]()`) with `sema` fallback for index-vs-generic call ambiguity (`fns[i]()` vs `zeros[N]()`), and add parser tests in `internal/parser/parser_test.go`.
5. **Formatter ([`internal/format/printer.go`](./internal/format/printer.go)):**
   * Format `const` declarations/methods, `[N: const Type]`, `comptime` expressions (`printComptimeExpr`), `comptime if`/`for`/`match`, and `TopComptimeIfDecl`, with idempotency tests in `internal/format/format_test.go`.

---

## Phase 2: Core Evaluator, `const` Coloring, & Budgets (`internal/sema`) [Done]

**Goal:** Build the safe compile-time interpreter, enforce `const` / `@comptime_only` coloring, and upgrade constant folding with overflow/wrap checking and layout queries.

1. **Value Model & Control-Flow Interpreter (`internal/sema/comptime.go` or `internal/comptime/`):**
   * Define `ctVal` (integers with bit-width, `f32`/`f64`, `bool`, `char`, `string`, `[]ctVal` lists, `map`/`set` handles, structs, tagged unions, tuples, `?T`, `fn` refs, opaque reflection descriptors) and evaluate `{ ... }`, `if`, `for`, `while`, `match`, result chains, `??`, all 8 unwinds, local `cl`, and LIFO `Drop` on comptime locals.
2. **Arithmetic Parity & Overflow Checking (`internal/sema/comptime.go`):**
   * Implement `SPECIFICATION.md` §5.3 arithmetic (`+`, `-`, `*`, unary `-` wrapping in operand bit width; IEEE-754 float ops) and literal range validation (`const B: u8 = 256` -> overflow error).
3. **Traps, `compile_error`, & Execution Budgets (`internal/sema/comptime.go`):**
   * Convert runtime traps (`/ 0`, `% 0`, bad shift `< 0` or `>= width`, OOB indexing/slicing, invalid `char(int)`, `panic`, `assert`) into compile errors with call stacks, add `@comptime_only` `comp.compile_error(msg)` & `comp.log(val)`, and enforce step (`1_000_000`), call-depth (`1024`), and allocation memory (`64 MB`) limits with `@comptime_limit(N)` override.
4. **Safe Bit Intrinsic for `float.hash` ([`std/core/hash.ll`](./std/core/hash.ll), [`std/core/primitives.ll`](./std/core/primitives.ll)):**
   * Add a safe compiler-hooked `float.to_bits` / `_float_bits` intrinsic so `float.hash` and `std/math` helpers run at compile time without user `unsafe`.
5. **`std/core` & Layout/Location Evaluator Hooks (`internal/sema/comptime.go`):**
   * Support `type.size_of[T]()`, `type.align_of[T]()`, `type.is_plain[T]()`, `caller_location()`, `caller_file()`, `caller_line()`, and `caller_col()`, and run real `std/core` on the simulated heap (`H1`/`H3`).
6. **`const` Registration & Opt-In Coloring ([`internal/sema/types_pass.go`](./internal/sema/types_pass.go), [`check_stmt_expr.go`](./internal/sema/check_stmt_expr.go), [`check_calls.go`](./internal/sema/check_calls.go)):**
   * Register `const` top-level bindings, functions, and methods (`IsConst`, `ComptimeOnly`, `ComptimeLimit`, `ConstVal`), reject `cl(...)` closures in `const` initializers, enforce definition/monomorphization `const` coloring (reject `bind`-only calls, `unsafe`, `extern`, `syscall`), and reject `@comptime_only` calls from runtime code.
7. **Multi-Pass Constant Folding Upgrade ([`internal/sema/types_pass.go`](./internal/sema/types_pass.go)):**
   * Replace legacy `evalConstInt` with multi-pass evaluator folding for `const` bindings, `mod.C`, `comptime` expressions, `[N: T]` sizes (`[type.size_of[T](): u8]`), and enum discriminants, with unit tests.

---

## Phase 3: Static `.rodata` Baking & `List` CoW Guards (`std/core`, `internal/ir`, `internal/codegen`) [Done]

**Goal:** Bake compile-time values (`Plain`, `string`, `List[T]`, composites) cleanly into LLVM IR and C11 static data with zero startup work and zero leaks.

1. **`List` Copy-on-Write Guards ([`std/core/list.ll`](./std/core/list.ll)):**
   * Add `if(self.cap <= 0)` copy-on-write heap promotion guards (matching `List.reserve`) to `List.set`, `List.pop`, and `List.truncate` so mutating a runtime local initialized from `let xs = comptime ...` safely promotes static buffers to the heap.
2. **Crossing Boundary Validation (`internal/sema/comptime.go`):**
   * Enforce `validateCrossesToRuntime(val, ty)`: allow `Plain`, `string`, `List[T]` (recursively), and composites built from them (allow boxed `?T` only when `none`); reject escaping `Map`, `Set`, `Arena`, `ref[T]`, `ptr[T]`, `cl`, and custom `Drop` structs.
3. **MIR Static Representation ([`internal/ir/ir.go`](./internal/ir/ir.go), [`expr.go`](./internal/ir/expr.go), [`lower.go`](./internal/ir/lower.go)):**
   * Add static constant representations (`ConstExpr` / `Global.StaticInit`) without `InitInstr`/`Locals`, omit `@comptime_only` functions from MIR, and keep pre-`main` `InitInstr` + exit `Drop` strictly for runtime `bind` globals (`!gl.IsConst`).
4. **LLVM IR & C11 Static Emission ([`internal/codegen/llvmgen.go`](./internal/codegen/llvmgen.go), [`cgen.go`](./internal/codegen/cgen.go), [`split.go`](./internal/codegen/split.go)):**
   * Emit `.rodata` static byte/element arrays + `{ data, len, cap: 0 }` descriptors for `string` and `List[T]` (and static composites) in both backends and in `UnitPackage` split units.
5. **Baking & Mutation Verification (`tests/`):**
   * Add differential ASan/UBSan + `LOWLIGHT_CHECK_LEAKS=1` tests across both backends covering nested `const` `List[string]` / `List[Struct]` and mutated `let xs = comptime ...` locals (`push`, `set`, `pop`, `truncate`, `.copy()`).

---

## Phase 3.5: Architecture Audit Hardening (`COMPTIME_REVIEW.md`) [Done, except the open items under "Remaining"]

* **Simulated Heap & Zero `std/core` Hooks:** Replaced hardcoded `List`/`Map`/`Set`/`string` evaluator hooks (`isStdHookedComptimeFunc` / `tryCallStdlibHook`) with a bounds- and lifetime-checked simulated heap so `std/core` (`primitives.ll`, `list.ll`, `map.ll`, `set.ll`, `range.ll`, `array.ll`, `result.ll`, `error.ll`) runs real `const` Lowlight at compile time (`std/` `unsafe` permitted at comptime; user `unsafe` stays forbidden), with `MovedLocal` move-out and recursive LIFO `Drop` parity and `"$x"` / `x.to_string()` consistency.
* **Conditional Generics & `@derive`:** Generic `const` functions/methods and `@derive` methods are conditionally `const` based on concrete operations, with LSP hover status and chained `Related` diagnostics on downgrade.
* **`comp` & `type` Namespaces:** Split `@comptime_only` `comp` (`comp.compile_error`, `comp.log`) from `const` `type` layout queries (`type.size_of[T]()`, `type.align_of[T]()`, `type.is_plain[T]()`).
* **Safety & Cross-Boundary Hardening:** Implemented `List` `_data_mut` / `std/sort` CoW guards, `.rodata` write-trap fault E2E tests, `@comptime_only` type/function crossing rejection (`H11.5`/`H11.6`), recursion depth `1024` + memory budget (`64 MB`), ordered cycle chains, and `Related` diagnostic spans.
* **Spec sync:** Folded Phases 1–3 (§1 bindings, §2.1 `comptime`, §3.1 conditional generics + coloring, §4 crossing and the CoW invariant) into `SPECIFICATION.md` (`A12`).

### Phase 3.5 — Remaining (open, found in the second review)

1. **`string` fields under `addr_of` (`B10`):** Fix compile-time `Map.remove`, `Map.push`, and `Map.insert` over an existing key when the key or value is a `string` (`addr_of` on a `string`-typed place points into the string's bytes, not at the field), and extend `tests/comptime_parity_test.go` with `string` keys/values and `addr_of` on `string` struct fields.
2. **Memory accounting (`B11`):** Credit `free_raw` / `realloc_raw`, charge target sizes per cell instead of `ctValMemCost`, and support `@comptime_limit(steps: N, memory: M)` (as well as `@comptime_limit(N)`) to raise memory and step budgets.
3. **Diagnostic names (`B12`):** Show `show[U]`, not `show[ll_s_U]`, in conditional-generic downgrade errors (`internal/sema/check_calls.go`).
4. **Test helpers (`A1`, `A5`):** `assert_comptime_parity`, top-level `comptime assert`, and `@test_compile_error`.
5. **Budgets & speed (`H8`, `A14`):** A per-package/per-build step budget, evaluator benchmarks, and a smaller `ctVal`.

---

## Phase 4: `[N: const Type]` Generics & `comptime if`/`for`/`match` Unrolling [Done]

**Goal:** Support const generic parameters and unroll `comptime if`, `comptime match`, and `comptime for` (including top-level `comptime if`).

1. **Const Generic Type Identity & Mangling ([`internal/types/types.go`](./internal/types/types.go)):**
   * Extend `Struct`, `Enum`, `Interface`, and generic cache keys with `ConstArg` entries (`int`s, `bool`, `char`, tag enums, `string`), implementing canonical `Equals` and deterministic `Mangled()` (`Matrix[2 + 2, 4]` == `Matrix[4, 4]`).
   * Mangle `string` arguments as a sanitized ASCII prefix plus a SHA-256 hex tag (`H11.10`).
2. **Const Generic Parameters & `[N: T]` Inference ([`internal/sema/types_pass.go`](./internal/sema/types_pass.go), [`check_calls.go`](./internal/sema/check_calls.go)):**
   * Support `[N: const Type]` on structs, enums, interfaces, generic functions, and methods (validating allowed parameter types), and infer `N: const int` from `[N: T]` array arguments, with instantiation limits (`H15`/`A9`).
3. **`comp.TARGET` Types & Target Plumbing ([`std/core/core.ll`](./std/core/core.ll), [`internal/manifest/target.go`](./internal/manifest/target.go), [`internal/sema/types_pass.go`](./internal/sema/types_pass.go)):** *(moved from Phase 5 so top-level `comptime if` has real conditions to test, `H6`)*
   * Declare `@lang("target_os") TargetOs`, `@lang("target_arch") TargetArch`, `@lang("target_endian") Endian`, and `@lang("target_info") Target` in `std/core`.
   * Extend `manifest.TargetInfo` with `Endian`, `PtrSize` (`4`/`8`), and `PtrBits` (`32`/`64`), and register `@comptime_only` `comp.TARGET: Target`.
4. **Top-Level `comptime if` Pruning ([`internal/sema/types_pass.go`](./internal/sema/types_pass.go)):**
   * Evaluate top-level `comptime if` conditions after minimal constant pre-pass (and `comp.TARGET` setup) and prune untaken declarations (`extern`, `define`, `const`, `bind`; forbid `import` inside top-level `comptime if`, `H4` option 3) before Pass 1B–1E type/signature registration.
5. **Local `comptime if` & `comptime match` Pruning ([`internal/sema/check_stmt_expr.go`](./internal/sema/check_stmt_expr.go), [`program.go`](./internal/sema/program.go), [`internal/ir/`](./internal/ir)):**
   * Evaluate `comptime if` and `comptime match` during body checking (post-monomorphization for generics), record the taken arm in `BodyMaps`, and skip checking/lowering untaken arms.
   * Expression-form `comptime if` requires `else` and has the arms' common type (never `?T`); without `else` it is statement-only (`H11.1`). `comptime match` must be exhaustive, as runtime `match` is (`H11.2`).
6. **`comptime for` Unrolling & Tests ([`internal/sema/check_stmt_expr.go`](./internal/sema/check_stmt_expr.go), [`internal/ir/`](./internal/ir), `tests/`):**
   * Unroll `comptime for` into `BodyMaps` (`UnrolledFor`) with per-iteration constant bindings, `next <-` / `for <-` / label unwinds, statement unroll limits (`H15`), a lint for plain-data loops that don't need unrolling, and collecting `comptime for(...) T:` lowering, with E2E tests for `Matrix[R, C]`, `Tagged[TAG, T]`, dead branches, and unrolling.
7. **Early Tooling ([`cmd/lowlight/main.go`](./cmd/lowlight/main.go), [`internal/driver/`](./internal/driver)):**
   * A basic `lowlight expand` right after this phase (`A11`), and `lowlight check --all-targets` to typecheck untaken `comptime if` branches for every target (`H5`/`A6`).

---

## Phase 5: `comp.embed_str` / `comp.embed_bytes` & Native OS Layer *(Implemented)*

**Goal:** Enable target-neutral single-file platform code and sandboxed file embedding.

*(`comp.TARGET` moved to Phase 4, step 3.)*

1. **`comp.embed_str` & `comp.embed_bytes` Intrinsics ([`internal/sema/check_calls.go`](./internal/sema/check_calls.go), `comptime.go`, [`program.go`](./internal/sema/program.go)):** *(done)*
   * Implemented `@comptime_only` `comp.embed_str(path)` (`string`, UTF-8 validated) and `comp.embed_bytes(path)` (`[u8]`, static `.rodata` with `cap = 0`), enforcing declaring-file-relative resolution, strict sandbox confinement (no empty/NUL/abs paths, `..`/backslash traversal, symlinks escaping sandbox, dirs, non-regular files, or dependency escapes outside own `pkg.Dir`), and recording embedded paths + SHA-256 digests on `CheckedProgram` with LSP watcher invalidation.
   * Added cross-target codegen tests (`--target=aarch64-unknown-linux-gnu`, `--target=x86_64-unknown-linux-gnu`, `--target=arm64-apple-darwin`, `--target=x86_64-apple-darwin`) plus negative embed tests and C11/LLVM CoW/leak-check parity tests.
2. **Native OS Layer on Single-File `comptime if` ([`std/os/`](./std/os), [`internal/codegen/`](./internal/codegen)):** *(done for Linux/macOS POSIX)*
   * Replaced the C `sys_*` OS built-ins across `std/core`, `std/os`, `std/fs`, `std/time`, `std/net`, and `std/http` with native Lowlight using single-file `comptime if(comp.TARGET...)` (Linux `syscall`, macOS `libSystem` `extern`), leaving only `sys_argc`/`sys_argv`, `syscall`, and `sys_tls_*`.
   * Moved `ll_format_loc` (`@lang("format_loc")`) and `os.remove_all` (with dynamic strings, no fixed 1024/4096 stack buffer, symlink-safe) out of [`runtime_h.go`](./internal/codegen/runtime_h.go) and [`llvmgen_runtime.go`](./internal/codegen/llvmgen_runtime.go) into `std/core/primitives.ll` / `std/os/os.ll`, and deleted the migrated C/LLVM wrappers (`ll_sys_*`, `ll_os_*`, `ll_time_*`, `ll_net_*`).
   * Future follow-ups tracked in [`TODO.md`](./TODO.md): Windows Win32 branches, Native DNS resolver (`std/net`), and long-term Native TLS.

---

## Phase 6: Reflection API, `@derivable`, `std/derive`, & Tuple Generics

**Goal:** Implement full compile-time reflection, migrate `@derive` and tuples into pure Lowlight, and delete `internal/sema/derive.go` + backend struct/enum/tuple helper synthesis.

1. **`comp` Descriptor Types & Predicates (new `std/core/comp.ll`, [`internal/sema/types_pass.go`](./internal/sema/types_pass.go)):**
   * Define `@comptime_only` `TypeKind` (`tag_enum_kind`, `tagged_union_kind`, ...), `AttrInfo`, `FieldInfo`, `VariantInfo`, and `ElemInfo` in `std/core/comp.ll`.
   * Support descriptor predicates (`f.implements(I)`, `f.has_attr(name)`, `f.attr(name)`, `vr.payload_implements(I)`, `e.implements(I)`).
2. **`comp.*_of[T]()` Reflection Builtins ([`internal/sema/check_calls.go`](./internal/sema/check_calls.go)):**
   * Implement `comp.type_name[T]()`, `comp.kind_of[T]()`, `comp.fields_of[T]()`, `comp.variants_of[T]()`, `comp.elems_of[T]()`, `comp.attrs_of[T]()`, and `comp.implements[T, I]()`.
3. **Struct/Tuple Access & Collecting Construction ([`internal/sema/check_stmt_expr.go`](./internal/sema/check_stmt_expr.go), [`check_calls.go`](./internal/sema/check_calls.go), [`internal/ir/`](./internal/ir)):**
   * Implement `v.field(f)` and `t.elem(e)` (disambiguating `FieldInfo`/`ElemInfo` args from user methods) for read, `&`, assign, and move, plus collecting `comptime for(...) T:` struct/tuple construction (`next <-` defaults/none for optional/defaulted fields).
4. **Enum/Result Variant Intrinsics & `_` Privacy ([`internal/sema/check_stmt_expr.go`](./internal/sema/check_stmt_expr.go), [`check_calls.go`](./internal/sema/check_calls.go), [`internal/ir/`](./internal/ir)):**
   * Implement `variant_index(v)`, `v.is_variant(vr)`, `v.payload(vr)` / `if(v.as_variant(vr) as p)`, and `make_variant[T](vr [, own payload])`.
   * Enforce `_` field/variant privacy outside `T`'s package unless the `FuncInstance` carries `@derive(...)` authorization from `T`'s declaration. Authorization covers the derived function and the calls it makes inside the derive package with the same `T`, and cached instances are keyed on authorization as well as type arguments (`H16`).
5. **`@derivable(...)` & `std/derive` (new `std/derive/derive.ll`, [`std/core/interfaces.ll`](./std/core/interfaces.ll), [`internal/sema/derive.go`](./internal/sema/derive.go)):**
   * Support `@derivable(method: generic_fn, ...)` on interfaces and tag `Equatable`, `Comparable`, `Hashable`, `Copyable`, `Printable`.
   * Implement `std/derive` (`eq`, `cmp`, `hash`, `copy`, `to_string`) in Lowlight for structs, enums, results, and tuples, and benchmark release-build speed and std check time against the current compiler-generated helpers (`H16`).
   * Replace `internal/sema/derive.go` source-text generation with `@derivable` instantiation (`@derive` privacy authorization, `const` attachment when all field ops are `const` else `bind`, conditional generics, `BoxedOptional`), support custom user `@derivable` interfaces, and delete `internal/sema/derive.go`.
6. **Tuple & Optional Lowlight Generics + Backend Cleanup ([`std/core/`](./std/core), [`internal/codegen/cgen.go`](./internal/codegen/cgen.go), [`llvmgen_runtime.go`](./internal/codegen/llvmgen_runtime.go)):**
   * Move tuple `eq`/`cmp`/`hash`/`copy`/`to_string` to Lowlight generics over `elems_of[T]()` (and optionals to `std/core`), then delete compiler-synthesized struct/enum/tuple helpers in `cgen.go` and `llvmgen_runtime.go` (keeping drop glue compiler-generated), running full `@derive` and tuple E2E verification.

---

## Phase 7: Diagnostics, `lowlight expand`, LSP, & Editors

**Goal:** Deliver first-class diagnostics, expansion inspection, fast memoized LSP feedback, and editor highlighting.

1. **Diagnostic Traces & `@track_caller` ([`internal/diag/diag.go`](./internal/diag/diag.go), [`internal/sema/`](./internal/sema)):** *(done early, in Phase 3.5)*
   * Add `Related []RelatedInfo` (`Span`, `Message`) to `diag.Diagnostic`, print `= note:` traces in CLI snippets, attach comptime/instantiation call stacks, and lift primary spans on `@track_caller` (`assert`, `comp.compile_error`, `@derive`).
2. **`lowlight expand` CLI ([`cmd/lowlight/main.go`](./cmd/lowlight/main.go), [`internal/driver/`](./internal/driver)):**
   * Extend the basic Phase 4 `lowlight expand [-p <pkg>] [<path>]` with Phase 6 `@derive` attachment and IDE expand preview output, with golden tests.
3. **LSP Builtins, Attributes, & `Related` Mapping ([`internal/lsp/builtins.go`](./internal/lsp/builtins.go), [`attributes.go`](./internal/lsp/attributes.go), [`internal/lsp/`](./internal/lsp)):**
   * Add `const`, `comptime`, `@comptime_only`, `@comptime_limit`, `@derivable`, `comp.*`, `type.*`, and reflection builtins to completions/hover, and show constness and generic downgrade reasons in hover, docgen, and completion ordering (`A13`). (`Diagnostic.Related` already maps to LSP `DiagnosticRelatedInformation`.)
4. **LSP Memoization, IDE Caps, Hover Values, & Expand Preview ([`internal/lsp/`](./internal/lsp), [`internal/sema/program.go`](./internal/sema/program.go)):**
   * Enforce a tight IDE step and wall-clock cap (a non-fatal warning on timeout; wall-clock limits never apply to `lowlight build`), and add `sema` memoization (keyed by AST/`const`/`comp.embed_*` hashes) and per-package cached `const` values (`H14`/`A10`) in `CheckForIDE`, plus target switcher, evaluated-value hover, and expand preview, with LSP integration tests.
5. **Editor Grammars ([`editors/`](./editors)):**
   * Highlight `const`, `comptime`, and new attributes across `editors/vscode`, `editors/nvim`, and `editors/jetbrains`.

---

## Phase 8: Stdlib Migration, Specification, & Cleanup

**Goal:** Put `comptime` to work across `std/` and bring all official documentation in sync.

1. **Stdlib `const` & Table Migration ([`std/`](./std)):**
   * Migrate pure `bind` helpers and constants across `std/core`, `std/chars`, `std/strings`, [`std/math/math.ll`](./std/math/math.ll), and [`std/unicode/tables.ll`](./std/unicode/tables.ll) to `const` / `@comptime_only`, computing CRC and Unicode tables at compile time.
2. **Specification & Docs ([`SPECIFICATION.md`](./SPECIFICATION.md), [`CONVENTIONS.md`](./CONVENTIONS.md), [`IMPLEMENTATION.md`](./IMPLEMENTATION.md), [`website/`](./website)):**
   * Fold [`COMPTIME_DESIGN.md`](./COMPTIME_DESIGN.md) into `SPECIFICATION.md`, update `CONVENTIONS.md` (`const` vs `bind` vs `let`) and `IMPLEMENTATION.md` (evaluator, static `.rodata`, `lowlight expand`), and add Tour / Guide / By Example pages under `website/`.
3. **File-Suffix Target Filtering Decision ([`internal/manifest/target.go`](./internal/manifest/target.go)):**
   * Decide whether to deprecate/retire `_linux.ll` / `_darwin.ll` / `_unix.ll` file-suffix target filtering or retain it alongside single-file `comptime if(comp.TARGET...)`.

---

## Global Verification Checklist

At the end of every phase (and before landing):
```sh
go vet ./...
go test -v ./...
./lowlight test std/
```
All E2E tests must pass on **both** `--backend=llvm` and `--backend=c11` under `-std=c11 -Wall -Wextra -Werror -fsanitize=address,undefined` with `LOWLIGHT_CHECK_LEAKS=1`, and `TestWebsiteBuilds` must pass with zero broken links or example diffs.
