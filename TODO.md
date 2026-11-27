# Lowlight TODO

## How to read this list

- Each task is a checkbox. **A task nested under another depends on it**: finish the parent first.
- A task that also depends on something elsewhere says **Also needs:** with a link to it.
- **Decision** marks a question for the language's author. Work nested under a decision waits for the answer.
- Sections run from foundations (language design) to work built on them (std, tooling, self-hosting). Tasks with no prerequisites sit at the top level of their section.
- Finished work is in [Done](#done), with the decisions it recorded.

## Contents

1. [Compile-time execution (comptime)](#1-compile-time-execution-comptime)
2. [Function types and C interop](#2-function-types-and-c-interop)
3. [Tuples](#3-tuples)
4. [Operators](#4-operators)
5. [Results and the failure model](#5-results-and-the-failure-model)
6. [Iteration and patterns](#6-iteration-and-patterns)
7. [Compiler correctness](#7-compiler-correctness)
8. [Standard library](#8-standard-library)
9. [Performance, backends, and targets](#9-performance-backends-and-targets)
10. [Tooling, LSP, and editors](#10-tooling-lsp-and-editors)
11. [Testing and CI](#11-testing-and-ci)
12. [Concurrency](#12-concurrency)
13. [Self-hosting](#13-self-hosting)
14. [std/json review notes](#14-stdjson-review-notes)
15. [Done](#done)

---

## 1. Compile-time execution (comptime)

Chosen over Rust-style macros (2026-11-22); full language design finalized with the author (2026-11-24) in [`COMPTIME_DESIGN.md`](./COMPTIME_DESIGN.md) and mirrored 1:1 in [`COMPTIME_IMPLEMENTATION_PLAN.md`](./COMPTIME_IMPLEMENTATION_PLAN.md). Review findings (`B1`–`B12`, `H1`–`H17`, `A1`–`A15`) are in [`COMPTIME_REVIEW.md`](./COMPTIME_REVIEW.md).

### Phase 1 — Lexer, AST, parser, & formatter (`const` + `comptime` syntax)

- [x] Add `KwConst` (`"const"`) and `KwComptime` (`"comptime"`) to `internal/token/token.go` (`Kind`, `kindNames`, `keywords`) and verify ASI (`blockLevelAfter`) in `internal/lexer/`.
- [x] Extend `internal/ast/ast.go`: `IsConst` on `VarDecl`/`MethodDecl`, `IsConst` + `ConstType` on `TypeParam`, `ComptimeExpr`, `TopComptimeIfDecl`, `IsComptime` on `IfExpr`/`ForExpr`/`MatchExpr`, and expression generic argument slots.
- [x] Parse `KwConst` in `parseTopVarOrMethodDecl` and local declarations, `[N: const Type]` in `parseTypeParams`, `comptime expr` / `comptime { ... }` in `parseUnaryExpr`, `comptime if`/`for`/`match` (including collecting `comptime for(...) T:`), and `TopComptimeIfDecl` (`internal/parser/parser.go`).
- [x] Extend `looksLikeGenericTypeArgs` and bracket argument parsing for value expressions (`Matrix[2 + 2, 4]`, `zeros[8]()`) with `sema` fallback for index-vs-generic call ambiguity (`fns[i]()` vs `zeros[N]()`), plus tests in `internal/parser/parser_test.go`.
- [x] Update `internal/format/printer.go` for `const`, `[N: const Type]`, `comptime` expressions (`printComptimeExpr`), `comptime if`/`for`/`match`, and `TopComptimeIfDecl`, with idempotency tests in `internal/format/format_test.go`.

### Phase 2 — Core evaluator, `const` coloring, & budgets (`internal/sema`)

- [x] Define `ctVal` value model and evaluate safe control flow (`{}`, `if`, `for`, `while`, `match`, result chains, `??`, all 8 unwinds, local `cl`, LIFO `Drop` on comptime locals, real `ctPlace`/`refPlace` references) in `internal/sema/comptime.go`.
- [x] Implement runtime-parity wrapping arithmetic (`+`, `-`, `*`, unary `-`), IEEE-754 float ops (`SPECIFICATION.md` §5.3), `format_float_prec` parity formatting, and literal range overflow checks (`const B: u8 = 256`).
- [x] Convert runtime traps (`/ 0`, `% 0`, bad shift `< 0` or `>= width`, OOB indexing/slicing, invalid `char(int)`, `panic`, `assert`) into compile errors with structured `Diagnostic.Related` call stacks (collapsed frames, user-code span lifting), add `@comptime_only` `comp.compile_error(msg)` and `comp.log(val)`, and enforce step (`1_000_000`) / call-depth (`1024`) / memory (`64 MB`) budgets with `@comptime_limit(N)` (supporting `_` digit separators).
- [x] Add safe compiler-hooked `float.to_bits` / `_float_bits` intrinsic in `std/core/hash.ll` and `primitives.ll` so `float.hash` and `std/math` helpers run at compile time without user `unsafe`.
- [x] Add `type.size_of[T]()`, `type.align_of[T]()`, `type.is_plain[T]()`, and `caller_*()` at compile time. (The first version's Go hooks for `string`, `List`, `Map`, and `Set` were replaced by the simulated heap in Phase 3.5.)
- [x] Register `const` symbols/functions/methods (`IsConst`, `ComptimeOnly`, `ComptimeLimit`, `ConstVal`) in `internal/sema/types_pass.go`, reject `cl(...)` in `const`, enforce opt-in `const` coloring + conditional generics (`ConstFailReason` chained notes + LSP hover), and reject `@comptime_only` calls/fn-values in runtime code and baked tables.
- [x] Replace legacy `evalConstInt` with multi-pass evaluator folding for `const` bindings, `mod.C`, `comptime` expressions, `[N: T]` sizes (`[type.size_of[T](): u8]`), and enum discriminants, with unit tests.

### Phase 3 — Static `.rodata` baking & `List` CoW guards (`std/core`, `internal/ir`, `internal/codegen`)

- [x] Add `_data_mut` / `if(cap <= 0)` copy-on-write heap promotion guards in `std/core/list.ll` (`pop`, `set`, `_remove_at`, `_swap_remove_at`, `swap`, `truncate`) and `std/sort/sort.ll` (`sort`, `sort_by`, `stable_sort`, `stable_sort_by`, `reverse`), with `.rodata` fault & parity tests.
- [x] Enforce `validateCrossesToRuntime` in `internal/sema/comptime.go` (`Plain`, `string`, `List[T]`, and composites of them; reject escaping `Map`, `Set`, `Arena`, `ref[T]`, `ptr[T]`, `cl`, `@comptime_only fn`, custom `Drop`, and non-`none` boxed optionals; move-out semantics for crossing locals while non-crossing locals drop LIFO).
- [x] Add MIR static constant representations (`ConstExpr` / `Global.StaticInit`) in `internal/ir/`, omit `@comptime_only` functions/lambdas, and keep pre-`main` `InitInstr` + exit `Drop` strictly for runtime `bind` globals.
- [x] Emit `.rodata` static byte/element arrays + `{ data, len, cap: 0 }` descriptors for `string` and `List[T]` (and static composites) in `llvmgen.go`, `cgen.go`, and `split.go` (`UnitPackage`).
- [x] Add differential ASan/UBSan + `LOWLIGHT_CHECK_LEAKS=1` parity & static baking tests across both backends (`tests/comptime_static_baking_test.go`, `tests/comptime_parity_test.go`).

### Phase 3.5 — Evaluator simulated heap, `comp`/`type` namespaces, std/core `const` migration, & spec sync

- [x] Build a simulated heap (`alloc_raw`, `alloc_uninit`, `realloc_raw`, `free_raw`, `ptr_read`, `ptr_write`, `ptr_add`, `copy_raw`, `copy_bytes`, `zero_bytes`, `fill_bytes`, `is_null`, `ptr_cast`) in `internal/sema/comptime.go` so `std/core`'s real Lowlight executes at compile time (`A2`).
- [x] Migrate `std/core` (`primitives.ll`, `list.ll`, `map.ll`, `set.ll`, `range.ll`, `array.ll`, `result.ll`, `error.ll`) to `const`, remove `isStdHookedComptimeFunc` and `tryCallStdlibHook` (`H1`, `H3`), and fix `"$x"` vs `x.to_string()` consistency.
- [x] Put compile-time-only builtins in the `@comptime_only` `comp` namespace (`comp.compile_error`, `comp.log`) and layout queries in the `const` `type` namespace (`type.size_of`, `type.align_of`, `type.is_plain`), with a migration error for bare `size_of`. Later phases add `comp.TARGET`, `comp.embed_*`, and reflection to `comp`.
- [x] Reject `@comptime_only` functions used as values in runtime code and `@comptime_only` types in runtime signatures (`H11.5`, `H11.6`).
- [x] Fold finished Phase 1–3 rules (§1 bindings, §2.1 `comptime`, §3.1 conditional generics + coloring, §4 crossing/unsafe CoW invariant) into `SPECIFICATION.md` (`A12`).
- [x] Raise the default comptime call-depth cap to `1024` (`H8`).
- [x] Fix compile-time `Map.remove`, `Map.push`, and `Map.insert` over an existing key when the key or value is a `string`: `addr_of` on a `string`-typed place points into the string's bytes, not at the field (`B10`). Extend `tests/comptime_parity_test.go` with `string` keys and values for `remove`, `push`, and overwrite, and with `addr_of` on `string` struct fields.
- [x] Make the comptime memory budget count real usage: credit `free_raw` / `realloc_raw`, charge target element sizes instead of `ctValMemCost` (232 bytes), and support `@comptime_limit(steps: N, memory: M)` (preserving `@comptime_limit(N)`) to raise memory as well as step budgets (`B11`).
- [x] Show user-facing names (`show[U]`, not `show[ll_s_U]`) in conditional-generic downgrade errors, hints, and notes (`internal/sema/check_calls.go`, `types_pass.go`) (`B12`).
- [ ] Add `assert_comptime_parity`, top-level `comptime assert`, and `@test_compile_error` test helpers (`A1`, `A5`).
- [ ] Add a per-package/per-build step budget (`H8`), and benchmark the evaluator and shrink `ctVal` (`A14`).
- [ ] Revisit `[N: const Type]` const generic parameter syntax later (`COMPTIME_DESIGN.md` §3.2).
- [ ] Consider putting `@track_caller` builtins (`caller_location`, `caller_file`, `caller_line`, `caller_col`) into their own dedicated namespace.

### Phase 4 — `[N: const Type]` generics, `comp.TARGET`, & `comptime if`/`for`/`match` unrolling

- [x] Extend `internal/types/types.go` (`Struct`, `Enum`, `Interface`, generic keys) with `ConstArg` entries (`int`s, `bool`, `char`, tag enums, `string`), canonical `Equals`, and deterministic `Mangled()` using sanitized ASCII prefix + SHA-256 hex tag for `string` (`H11.10`).
- [x] Support `[N: const Type]` on structs, enums, interfaces, generic functions, and methods in `internal/sema/types_pass.go` and `check_calls.go`, including inferring `N: const int` from `[N: T]` arguments, plus instantiation limits (`H15`/`A9`).
- [x] Define `@lang("target_os") TargetOs`, `@lang("target_arch") TargetArch`, `@lang("target_endian") Endian`, `@lang("target_info") Target`, and `@comptime_only` `comp.TARGET` (`manifest.TargetInfo` `Endian`, `PtrSize`, `PtrBits`) ahead of top-level `comptime if` (`H6`).
- [x] Evaluate top-level `comptime if` conditions in `internal/sema/types_pass.go` before Pass 1B–1E type/signature registration and prune untaken declarations (`extern`, `define`, `const`, `bind`; ban `import` under `comptime if` per `H4` Option 3).
- [x] Evaluate local `comptime if` (expression form must have `else` and yields common non-`?T` type; no `else` = statement-only) and exhaustive `comptime match` during body checking, skipping untaken arms (`H11.1`, `H11.2`).
- [x] Unroll `comptime for` into `BodyMaps` (`UnrolledFor`) with per-iteration constant bindings, `next <-` / `for <-` / label unwinds, statement unroll limits (`H15`), plain-data loop lint, and collecting `comptime for(...) T:` lowering.
- [x] Implement a basic `lowlight expand` CLI early after Phase 4 (`A11`), plus `lowlight check --all-targets` for typechecking untaken `comptime if` branches across targets (`H5`/`A6`).

### Phase 5 — `comp.embed_*` & native OS layer

- [x] Implement `@comptime_only` `comp.embed_str(path)` (`string`, UTF-8 validated) and `comp.embed_bytes(path)` (`[u8]`, static `cap = 0`) with declaring-file-relative resolution, strict sandbox confinement (no empty/NUL/abs paths, `..`/backslash traversal, symlinks escaping sandbox, dirs, non-regular files, or dependency escapes outside own `pkg.Dir`), SHA-256 + path tracking on `CheckedProgram` with LSP invalidation, and cross-target + parity + negative embed tests.
- [x] Native OS layer (using single-file `comptime if(comp.TARGET...)` everywhere): replaced the C `sys_*` built-ins (`fopen`..`rename`, `popen`/`system`, `mkdir`/`remove_all`/`file_exists`, `num_cpus`/`os_name`/`os_arch`, `errno`/`strerror`/`errno_name`, `time_*`, `net_*`) with Lowlight across `std/core`, `std/os`, `std/fs`, `std/time`, `std/net`, and `std/http` (Linux `syscall`, macOS `libSystem` `extern`), leaving only `sys_argc`/`sys_argv`, `syscall`, and `sys_tls_*`.
  - [x] Move `ll_format_loc` (`@lang("format_loc")`) out of `internal/codegen/runtime_h.go` and `internal/codegen/llvmgen_runtime.go` into `std/core/primitives.ll` and clean up migrated C/LLVM OS runtime helpers (`ll_sys_*`, `ll_os_*`, `ll_time_*`, `ll_net_*`).
  - [x] Replace `ll_os_remove_all`'s fixed stack buffers (`internal/codegen/runtime_h.go`, `internal/codegen/llvmgen_runtime.go`) with native Lowlight `os.remove_all` dynamic strings and recursive removal (removing symlinks without following).
  - [ ] Windows Win32 implementation branches in `std/core`, `std/os`, `std/fs`, `std/time`, and `std/net` (currently cleanly stubbed/unsupported on `comp.TargetOs.windows`).
  - [ ] Native DNS: a resolver written in Lowlight (read `/etc/resolv.conf` and `/etc/hosts`, A/AAAA queries over UDP with TCP fallback, timeouts and retries), so name lookup stops going through the C library's `getaddrinfo`. **Also needs:** [UDP sockets](#8-standard-library).
  - [ ] Native TLS (long term): TLS 1.2/1.3 in Lowlight (handshake, record layer, X.509 verification against the system root store) with the cryptography it needs (AES-GCM, ChaCha20-Poly1305, SHA-2, HMAC/HKDF, X25519, ECDSA P-256, RSA verification), replacing the OpenSSL `dlopen` loader (`sys_tls_*`). Until then TLS stays on OpenSSL through C interop.

### Phase 6 — Reflection API (`comp.*`), `@derivable`, `std/derive`, & tuple generics

- [x] Define `@comptime_only` descriptors (`TypeKind` with uniform `tag_enum_kind` / `tagged_union_kind`, `AttrInfo`, `FieldInfo`, `VariantInfo`, `ElemInfo`) under `comp` / `std/core/comp.ll`, and support descriptor predicates (`f.implements(I)`, `f.has_attr(name)`, `f.attr(name)`, `vr.payload_implements(I)`, `e.implements(I)`).
- [x] Implement `comp.type_name[T]()`, `comp.kind_of[T]()`, `comp.fields_of[T]()`, `comp.variants_of[T]()`, `comp.elems_of[T]()`, `comp.attrs_of[T]()`, and `comp.implements[T, I]()` in `internal/sema/check_calls.go`.
- [x] Implement `v.field(f)` / `t.elem(e)` (read, `&`, assign, move; disambiguated from user methods via `FieldInfo`/`ElemInfo`) and collecting `comptime for(...) T:` struct/tuple construction (`next <-` defaults/none for optional/defaulted fields).
- [x] Implement `variant_index(v)`, `v.is_variant(vr)`, `v.payload(vr)` / `if(v.as_variant(vr) as p)`, `make_variant[T](vr [, own payload])`, and enforce `_` privacy outside `T`'s package unless authorized by `@derive(...)` (scoped strictly to the derived wrapper package `H16`).
- [x] Add `@derivable(...)` interface support (`Equatable`, `Comparable`, `Hashable`, `Copyable`, `Printable` plus custom std/user interfaces) in `internal/sema/derivable.go`, write `std/derive/derive.ll`, benchmark release perf vs runtime helpers (`H16`), replace `internal/sema/derive.go` with `@derivable` instantiation, and delete `internal/sema/derive.go`.
- [x] Move tuple `eq`/`cmp`/`hash`/`copy`/`to_string` to Lowlight generics (`std/core/tuples.ll` over `comp.elems_of[T]()`, and optionals in `std/core/optional.ll`), then delete the struct/enum/tuple/optional eq/cmp/hash/copy/to_string helpers in `cgen.go` and `llvmgen_runtime.go` (drop/boxopt glue stays compiler-generated). **Also needs:** [Tuples](#3-tuples).

### Phase 7 — Diagnostics, `lowlight expand`, LSP, & editors

- [x] Add `Related []RelatedInfo` frames to `diag.Diagnostic`, CLI `= note:` trace rendering, collapsed recursion stacks, and `@track_caller` / `std/` span lifting (`internal/diag/diag.go`, `internal/sema/comptime.go`, `internal/lsp/server.go`).
- [x] Extend `lowlight expand` CLI (`cmd/lowlight/main.go`, `internal/driver/`) with full Phase 6 `@derive` & IDE expand preview output and golden tests.
- [x] Add `const`, `comptime`, `@comptime_only`, `@comptime_limit`, `@derivable`, `comp.*`, and `type.*` to `internal/lsp/builtins.go` & `attributes.go`, and show constness / generic downgrade reasons in hover, docgen, & completion ordering (`A13`).
- [x] Enforce a tight IDE step/wall-clock timeout cap (non-fatal warning on timeout, never used in `lowlight build`), add `sema` memoization + per-package cached `const` values (`H14`/`A10`) in `CheckForIDE`, plus target switcher, evaluated-value hover, and expand preview (`internal/sema/ide_cache.go`, `internal/lsp/`).
- [x] Update `editors/` syntax highlighting (`vscode`, `nvim`, `jetbrains`) and target/expand IDE integrations.

### Phase 8 — Remaining stdlib migration, specification, & cleanup

- [x] Migrate remaining pure `bind` helpers, constants, and tables across `std/core`, `std/chars`, `std/strings`, `std/math/math.ll`, `std/strconv`, `std/strfmt`, `std/time`, `std/http`, and `std/unicode/` (`tables.ll` in `.rodata` + `unicode.ll`) to `const` / `@comptime_only` (CRC32 compile-time table generation showcased in `website/` docs & examples without adding a CRC package to `std/`).
- [x] Finish folding Phases 4–7 (`COMPTIME_DESIGN.md`) into `SPECIFICATION.md`, update `CONVENTIONS.md` and `IMPLEMENTATION.md`, and add Tour / Guide / By Example pages on `website/`.
- [x] **Decision:** Retain file-suffix target filtering (`_linux.ll`, `_darwin.ll`, `_unix.ll`, etc. in `internal/manifest/target.go`) alongside single-file `comptime if(comp.TARGET...)`, and document single-file `comptime if(comp.TARGET...)` as the preferred pattern.

## 2. Function types and C interop

`fn` (no captures, plain) and `cl` (closures) are implemented; see [Done](#done). Native TLS is long term, so C interop stays. `extern` is likely C-only (undecided; the spec currently allows other languages).

- [ ] Revisit whether `fn` should keep converting to `cl` implicitly, or only through `cl(f)`.
- [ ] C struct layout for any struct passed to C (a `@repr(c)`-style guarantee beyond `packed`), checking that every field has a C representation.
- [ ] Calling C variadic functions (`printf`, `fcntl`, `open` with a mode).
- [ ] The native OS layer, built on `syscall`: see [comptime](#1-compile-time-execution-comptime).

## 3. Tuples

Tuples are due for an overhaul (2026-11-22); `@derive` deliberately doesn't apply to them yet.

- [ ] **Decision:** the tuple overhaul: what changes about tuples (syntax, destructuring, etc.); `eq`/`cmp`/`hash`/`copy`/`to_string` are decided to be comptime-generic over `elems_of[T]()` (`COMPTIME_DESIGN.md`).
  - [x] Tuple `eq`/`cmp`/`hash`/`copy`/`to_string` written in Lowlight (`std/core/tuples.ll` with `comptime for` over `elems_of[T]()`), plus optionals (`std/core/optional.ll`), replacing the helpers both backends synthesize. **Also needs:** [comptime type-description built-ins](#1-compile-time-execution-comptime).
  - [ ] Flat tuple destructuring in declarations (`let +[a, b] = ...`, including mutable, typed, and wildcard elements).
    - [ ] Nested tuple destructuring in declarations and `match` patterns.
  - [x] Add tuples (and optionals) to `LookupMethod` (`internal/sema/program.go`, `internal/sema/types_pass.go`).

## 4. Operators

- [ ] **Decision:** names and grouping for the remaining operator interfaces, following `Addable`/`AddAssignable` (`std/core/interfaces.ll`, `operatorMethod` in `internal/sema/collections.go`): `-`/`-=` (`Subtractable`), `*`/`*=`, `/`/`/=`, `%`/`%=`, unary `-` (`Negatable`), and the bitwise `&`, `|`, `^`, `~`, `<<`, `>>` with their assignment forms; one interface per operator, or grouped ones (say `Numeric`) as bounds.
  - [ ] Implement them in sema and IR, with each numeric primitive implementing them in `primitives.ll` as `add` does.
- [ ] **Decision:** whether lists get `+`.
  - [ ] `Addable` for `[T]` (`[1] + [2]`) and `AddAssignable` as `append`.

## 5. Results and the failure model

Apply the `Result` vs. `panic` rules and the `x` / `must_x` naming convention from `SPECIFICATION.md` §9.1 across `std`.

- [ ] **Decision:** whether optional lookups (`List.get`, `Array.get`, `Map.get` returning `?T`) pair with panicking indexing (`xs[i]`, `xs[i] = v`) as their `must_` forms, or get `must_get` / `must_set` methods; document it in `SPECIFICATION.md` §9.1. `List.set` now returns a `Status`, and `xs[i] = v` is its panicking form for now.
  - [ ] `[]` means two different things today: on lists, arrays, and strings it is the element itself (read in place, assignable, traps when out of range); on maps and `Indexable`/`KeyIndexable` types it is sugar for `.get()`, an optional copy. Make it mean one thing, for example "the element in place, trapping when absent" everywhere, with `.get()` as the optional copy.
- [ ] Audit `std` (especially `std/http`, `std/strconv`, and `std/time` parsing) for panics reachable from external input, and convert them to `Result`/`?T` per §9.1.
  - [ ] Require a `# Panics` doc section on every public std function that can panic, with a `lowlight doc` lint that flags missing ones.
- [ ] Remove the now-unused `ll_vec_remove__T` / `ll_vec_swap_remove__T` runtime helpers from both backends (`List.remove` and `swap_remove` are Lowlight in `std/core/list.ll`).
- [ ] An early return nested inside a `match` arm (behind an `if`) that is an operand of a larger expression doesn't free the expression's earlier temporaries (`??` has the same gap); free them on every unwind path.
- [ ] `@must_use` is a warning, but `SPECIFICATION.md` §4.8 says discarding the value is an error; make them agree.

## 6. Iteration and patterns

- [ ] **Decision:** reconsider `Iterable[T]` and `Iterator[T]` in `std/core/interfaces.ll`, and add `IterableIndex[I, T]` / `IteratorIndex[I, T]` with `next: fn(&self) ?+[I, T]`.
  - [ ] Native `Iterable` and `IterableIndex` iterators across `std/core` (including `string` yielding `char` and `+[int, char]`).
    - [ ] Lower single- and two-variable `for` / `while` iteration through native Lowlight iterators (`internal/sema`, `internal/ir`).
      - [ ] Make `for(x in v)` work for any type implementing `Iterable[T]` (and for `Iterator[T]` values), as `SPECIFICATION.md` §12.1 and §18.6 describe; sema hardcodes lists, arrays, strings, sets, maps, ranges, and `Pool` ("cannot iterate over value of type Countdown").
      - [ ] Fix `Iterable[T]` and user `.iter()` resolution (`internal/sema/check_calls.go`, `check_stmt_expr.go`) and generalize `GenIfaceIterable`.
  - [ ] Avoid the full deep-copy snapshot in `List.iter` (`std/core/list.ll`), which copies the list and then every element again.
- [ ] Fix `for <-` in a collecting loop (`for(x in xs) [int]: ...`): bare `for <-` is rejected ("requires a value of type int") although `SPECIFICATION.md` §6.7 says it stops the loop and keeps what was collected, and `for <- value` crashes LLVM codegen.
- [ ] `..` struct and variant field wildcards in patterns (`internal/parser`, `internal/sema`, `internal/ir`).
- [ ] `when pat if cond:` match guards.
- [ ] Nested constructor patterns.
  - [ ] Allow omitting `default:` on proven-exhaustive `match` expressions.
- [ ] Fix literal patterns on an optional subject: `match(o?): when 3: ...` never matches when `o?` holds `3` (§6.5 says it should).
- [ ] Fix `match` duplicate-arm and `"-0"` bugs, and multi-argument `match` ownership bugs (`internal/sema/check_stmt_expr.go`).
- [ ] Support `*types.Ref` in non-enum `match` and in `checkSliceExpr`.
- [ ] Fix `for ... else` / `while ... else` lowering (`internal/ir/lower_ops.go`): `for ... else` accesses `.len` instead of `.ll_len` on `Set`/`Map` (C11 compile error) and misses `for <-` before push, and `void` `for ... else` / `while ... else` runs `else` unconditionally even after `for <-` / `while <-`.

## 7. Compiler correctness

### Soundness (first)

- [ ] Binding an element of a borrowed resource list moves it out without an error: in `bind f = fn(xs: [string]) int: { let r = xs[0]; r.len() }`, `r` is dropped at the end of `f`, freeing the caller's string. `let r = xs[i]` on a read-only `[T]` of resources should be an error (suggest `.copy()`) or a borrow.
- [ ] Enforce immutability/borrow rules when mutating through `ref[T]` in `analyzePlace` (`internal/sema/check_stmt_expr.go`).
- [ ] Prevent `Arena` method receivers from bypassing `checkMutatingMethodRecvEscape` (`internal/sema/check_calls.go`).
- [ ] Enforce `&Arena` captures inside nested closures.
- [ ] Fix mutable borrow + move/alias hole in `checkSignatureArgs` (`internal/sema/check_calls.go`): `mutRoots` don't increment `activeBorrows` while later args are checked and `readRoots` skips non-trivial expressions (`rootSymbolOfExpr == nil` for calls/blocks/ifs/matches), allowing `b.show(consume(own b))`, `foo(&b, consume(own b))`, and `xs.push(xs.remove(0) ?? "")`.
- [ ] Fix `checkBuiltinMethodCall` running `checkMutatingMethodRecvEscape` before `checkSignatureArgs` typechecks arguments and skipping `mr` when `isArenaRef` or `root == nil`, letting an inner `Arena`'s `ref[T]` escape into an outer container (`internal/sema/check_calls.go`).
- [ ] Check that `rootSym` stays alive after checking the RHS of sub-place assignments (`xs[0] = consume(own xs)`, `p.f = consume(own p)`), and enforce borrow exclusivity on `+=` for `Addable`/`AddAssignable` types (`b += b`, `s += consume(own s)`) (`internal/sema/check_stmt_expr.go`).
- [ ] Propagate arena depth across `+` (`Addable`) in `arenaDepthOfExpr` (`internal/sema/check_stmt_expr.go`).
- [ ] Check `Params[1].Mode == types.ModeRead` in `operatorMethod` (`internal/sema/collections.go`) so `own`/`&` RHS parameters on `add`/`add_assign` aren't accepted without moving or borrowing.
- [ ] Reject owned slicing (`xs[lo..hi]`) on non-copyable elements in `checkSliceExpr` / `consumeValue` (`internal/sema/check_stmt_expr.go`) instead of failing later with an undefined `ll_vec_slice_owned__...` symbol.
- [ ] Mark `self` moved (or skip dropping moved fields) when moving a field out of `own self` in `lowerExprOwned` (`internal/ir/lower_expr.go`): zeroing `self.field` runs `Drop.drop` on `{0}` at scope exit (e.g. `TcpStream`/`TcpListener` closing fd `0`).
- [ ] Enforce `unsafe` and visibility boundaries for `extern` functions: reject `let f = my_extern_fn` as a safe `fn` value (`ExternFnDecl` in `internal/sema/types_pass.go`), reject calls to `@test_only extern`s in non-test code (`internal/sema/check_calls.go`), and re-run `checkCSignature` after return-type inference on `extern fn` literals (`internal/sema/check_stmt_expr.go`).
- [ ] **Decision:** rethink `unsafe` so it enforces stronger invariants (keep unrelated borrow/move/type checks active inside it, scope raw-pointer and intrinsic capabilities narrowly, and reduce convenience `unsafe` blocks in `std`).

### Parser

- [ ] Qualified/generic bounds and receiver bounds in `parseTypeParams`.
- [ ] Struct field-init shorthand (`Point[x, y]`).
- [ ] Resolve the generic call vs. index ambiguity on member expressions (`obj.fns[i]()`) in the parser and `internal/sema/check_calls.go`.
  - [ ] Remove the remaining in-place AST rewrites in sema (`xs[i].m` and `fns[i](...)` disambiguation, now serialized by `astRewriteMu`) by resolving them into side tables, so cached ASTs are strictly read-only.

### Sema

- [ ] A comprehension into a collection whose `push` is `@track_caller` calls `push` without the caller-location arguments; the C11 backend rejects the call ("too few arguments"). Found with `std/collections`' `BitSet.push`.
- [ ] Check `TypeParamVar` in `checkOptionalIdentRule`.
- [ ] Validate duplicate generic `@lang` items (`GenIface*`) in `internal/sema/program.go` and `types_pass.go`.
- [ ] Fix `instantiateStruct` nil-deref and cache races.
- [ ] Scope generic method tables away from bare-name collisions.
- [ ] Method type inference.
- [ ] Fix `?[` optional array unwrapping, and the `?T` least-upper-bound in yields.
- [ ] Fix mutable `&T` covariance checks.
- [ ] Fix `checkIfExpr` move/condition checks.
- [ ] Fix `consumeValue` plain-to-interface moves and whole-`self` field moves.
- [ ] Full state rollback on probe checks (`internal/sema/check_calls.go`, `check_stmt_expr.go`).
- [ ] Fix `checkForExpr` move-state and generic substitution merges.
- [ ] Fix assigning a `??`-with-unwind guard to an optional variable: `p? = parse_int(s) ?? func <- -1` emits invalid LLVM IR and C (the `int` result isn't wrapped into `?int`).
- [ ] **Decision:** how to take several fields out of an `own self` receiver (moving one field consumes `self`, so `Pair.swap = fn(own self)` can't move both fields; there is no struct destructuring).
- [ ] **Decision:** whether `abs` is numeric like `min`/`max` (the prelude's `abs(x: int) int` shadows the numeric builtin, so `abs(-2.5)` is a type error).
- [ ] Improve the diagnostic for a missing associated function on an imported type: `time.Duration.seconds(9)` reports `undeclared identifier "time"`.
- [ ] Warnings for unused locals, imports, and private functions, and for unreachable code after `<-`.
- [ ] Log recovered panics (with stack) from `CheckForIDE` instead of swallowing them (`internal/sema/program.go`), and surface them through LSP `window/logMessage`.
- [ ] Reject cyclic `Distinct` declarations (`define A A`, `define A B` / `define B A`) in `resolveDistinctsAndEnums` and guard type queries (`IsPlain`, `UnderlyingPrimitive`, `IsHashable`, `supportsEqVisited`, `containsRefVisited`, `typeContainsStructByValue`) against `Distinct` cycles (`internal/sema/types_pass.go`, `internal/types/types.go`).
- [ ] Wrap optional-chained `.len`, `.cap`, and `.data` results on `?[T]` / `?[N: T]` in `types.NewOptional` in `checkMemberExpr` (`internal/sema/check_stmt_expr.go`) so IR lowering doesn't panic.
- [ ] Run `supportsEq` when comparing `?T == T` / `T == ?T` (`checkBinaryExpr` in `internal/sema/check_stmt_expr.go`).
- [ ] Preserve non-primitive-integer type annotations in `collectTopLevelIntConsts` (`internal/sema/types_pass.go`) instead of silently typing `bind S: string = 1` or `bind P: Port = 8080` as `int`.
- [ ] Fix result chains on `?T` value payloads (`prepareResultChain` omitting `?` on `_ll_chain_value`) and tail result chains in return-type-inferred functions (`checkMatchExpr` forcing `wantsValue = false` when `allowVoid` is true) (`internal/sema/check_stmt_expr.go`).
- [ ] Reject bare `<-` targeting unannotated value-producing blocks/loops/matches and `<- val` targeting `void` blocks/ifs/matches in `checkUnwindStmt`, and include `match <- val` arms (`aty == TypeNever`) in `liveBranchMoves` in `checkMatchExpr` (`internal/sema/check_stmt_expr.go`).
- [ ] Report overflow in `adaptIntLit` on negated unannotated `i64` literals (`-9223372036854775809`) (`internal/sema/check_stmt_expr.go`).
- [ ] Handle `sym.IsTopLevel` for `MemberExpr` in `consumeValue` (`internal/sema/check_stmt_expr.go`) so `mod.my_fn` and `mod.MY_STR` aren't rejected as moving struct fields.
- [ ] Avoid double-checking `mem.Base` in `checkInstanceMethodCall` (`checkClosureValueCall`) and unwrap `*types.Optional` receivers for `fn`/`cl` struct/tuple fields (`handler?.on_click(ev)`) (`internal/sema/check_calls.go`).
- [ ] Reject `IsStaticAssoc` methods and check `ModeOwn`/`ModeMut` on `self` and index argument in custom `.get` `IndexExpr` fallback (`internal/sema/check_stmt_expr.go`).
- [ ] Apply `expected` element coercion (`verifyAssignable`) to repeat array literals `[N: val]` (`[3: ?int] = [3: 1]`) in `checkArrayLit` (`internal/sema/check_stmt_expr.go`).
- [ ] Fix interface `Self` handling (`internal/sema/types_pass.go`): detect nested `Self` (`?Self`, `[Self]`, `fn(Self) bool`) for `UsesSelf`, check `UsesSelf` on generic interfaces in `resolveValueType`, run `resolveInterfaces` before `resolveDistinctsAndEnums`, and substitute nested `Self` in `ImplementsInterface`.
- [ ] Suppress spurious `"expects N type argument(s), got 0"` errors at `diag.Span{}` during speculative `ImplementsInterface` checks when a concrete type has a generic method with the same name (`internal/sema/types_pass.go`).
- [ ] Compare template/declaration identity instead of bare `Name` during expected-type inference for generic structs/enums (`checkStructLit`, `tryResolveEnumMemberTarget`, `checkGenericTypeAssocCall`).
- [ ] Enforce `IsExported` on bare result member construction and contextual failure patterns (`checkBareResultMember`, `checkContextualFailurePattern`), and run `checkOptionalIdentRule`/`checkResultIdentRule` on `if(opt? as v)` / `if(res! as v)` bindings (`internal/sema/check_stmt_expr.go`).
- [ ] Scope block-local `define` types in `checkLocalTypeDecl` (`internal/sema/check_stmt_expr.go`) instead of mutating module-wide `mod.Types` without duplicate checks and after `StructBodyMaps` initialization.

### IR

- [ ] Replace `lowerRawExpr` with `lowerExprOwned` in `internal/ir/lower_expr.go` and `lower_ops.go`.
- [ ] Evaluate lvalues once in `emitCompoundAssign` (`internal/ir/lower.go`) and reuse checked element pointers to avoid duplicate `ll_check_bounds` calls.
- [ ] Forward `TrackCaller`/`TargetFunc` in custom `IndexExpr` lowering.
- [ ] Avoid copying owned `Array` values on read indexing.
- [ ] Support `&self` receivers and mutating methods under optional chaining (`internal/ir/lower_calls.go`).
- [ ] Populate `ExprBoxInterface.VTable` (or collect vtables from `(ConcreteType, IfcType)` in `buildPackageSubProgram`, `internal/codegen/split.go`) so `UnitPackage` split builds emit `ll_vtinst_...` extern declarations.
- [ ] Register `BorrowAsView` temporaries (`_ll_ifcb` for interface-to-interface and `_ll_arrview` for `[N: Resource]` rvalues) with `allocStmtTemp` instead of `allocSlot` in `lowerExprRead` and `lowerStackArrayAddr` (`internal/ir/lower_expr.go`) to fix leaks.
- [ ] Register boxed `?Struct` temporaries created by `WrapOptional` in `lowerExprRead` (`internal/ir/lower_expr.go`) and avoid shallow-bitcopying resources into the heap box (`cgen.go`, `llvmgen.go`).
- [ ] Mark custom `Indexable`/`KeyIndexable` `.get()` calls as fresh rvalues in `isFreshResourceRValue` (`internal/ir/lower_expr.go`) so read-only indexing doesn't leak `Resource`/`?Resource` returns.
- [ ] Retire the remaining hardcoded `vec.*` builtin method lowerings in favor of `std/core` `List` (sets, maps, strings, and the compiler's string code are done; `string.len`, `byte_at`, and `c_str` remain built-ins).
  - [ ] Replace hardcoded `"List"`/`"Array"` name checks in `internal/types/types.go` with the `Struct.Lang` tag.
  - [ ] Guard capacity doubling against `int64_t` overflow and zero vacated slots in the C11 and LLVM vector helpers, and fix their view materialization for `reserve`, `pop`, `remove`, and `swap_remove`, if the helpers survive.
- [ ] Complete vtable method dispatch for `Vector` in both backends, and fail compilation cleanly on unhandled interface methods.

### LLVM backend

- [ ] Fix stub `""` primitive `to_string` handling and the `_to_string` wrapper heuristics.
- [ ] Fix `bool`/`i1` casts.
- [ ] Fix diverging right-hand-side handling and metadata in `InstrAssign`/`InstrCompoundAssign`.
- [ ] Fix under-aligned `align 8` loads/stores.
- [ ] Fix `ll_copy_p__<Interface>` (`internal/codegen/llvmgen_runtime.go`) returning an uncloned borrowed fat pointer when `owned == false` instead of calling `clone_box` (UAF / C11 mismatch).
- [ ] Allocate `ptr` instead of `g.llvmType(loc.Type)` for `loc.IsMutPtr` locals in `emitMain` (`internal/codegen/llvmgen.go`) to prevent 8-byte stack buffer overflows on global `bind` initializers.
- [ ] Add `isFloat` handling (`frem` / `fmod`) for `%` / `%=` in `emitBinaryOp` (`internal/codegen/llvmgen.go`) instead of emitting calls to `ll_mod_i64`/`ll_mod_i32` with float operands.
- [ ] Emit `fcmp une` instead of `fcmp one` for float `!=` in `emitBinaryOp` (`internal/codegen/llvmgen.go`) so `NaN != x` returns `true`.
- [ ] Check `tag == 0` before calling custom `Drop.drop` in `ll_drop__<Enum>` (`internal/codegen/llvmgen_runtime.go`), matching C11.
- [ ] Emit `ptr` for `ModeMut` (`&T`) parameters on user `extern` declarations and avoid `declareBuiltin` silently colliding with pre-declared libc symbols (`close`, `exit`, `strlen`, etc.) in `emitRuntimeLLVM` (`internal/codegen/llvmgen_runtime.go`).
- [x] Fix `ll_net_set_timeout` (`internal/codegen/llvmgen_runtime.go`) returning `void` and skipping `timeout_ms <= 0`, matching sema (`int`) and C11 (resolved in Phase 5 by migrating `std/net` to native Lowlight and keeping only `@ll_tls_set_timeout` internally for TLS).
- [ ] Fix the inline `ll_vec_get__` fast path (`internal/codegen/llvmgen.go`) using the flattened `optTy.Elem` as the GEP element stride and unconditionally setting `has_value = true` on `[?T]` vectors.

### Runtime

- [ ] Enable peer certificate verification (`SSL_CTX_set_verify` with `SSL_VERIFY_PEER`), hostname checks (`SSL_set1_host`), and `SSL_get_verify_result` in both runtimes.
- [ ] Match LLVM `ll_tls_recv`, `ll_tls_send_all`, and `ll_tls_server_ctx_new` with C11 (`SSL_pending`, `poll`, timeout `-2`, `WANT_READ`/`WANT_WRITE`, `SSL_CTX_use_certificate_chain_file`).
  - [ ] Expose public TLS server APIs/contexts in `std/net`.
- [ ] Detect stack overflow and print a Lowlight panic (guard page plus `sigaltstack` SIGSEGV handler) instead of dying with exit code 139, in both runtimes.

## 8. Standard library

### Collections

- [ ] `std/collections` types the language's author is implementing by hand (leave these files to them): `LinkedList` (`linked_list.ll`), `RingBuffer` (`ring_buffer.ll`), `Trie`, `DisjointSet`, `LruCache`, `BloomFilter`, `SkipList`, `IntervalTree`, `FenwickTree`, `SegmentTree`, `Rope`, `Multiset`, and `Graph`. Each file's header describes the structure and links to references.
- [ ] `for(x in c)` over the `std/collections` types (they implement `iter`), once `for` accepts any `Iterable[T]` (see [Iteration and patterns](#6-iteration-and-patterns)); until then, iterate `c.to_list()` or `c.entries()`.

### Strings, numbers, and API cleanup

- [ ] Shortest round-trip float formatting (Ryu/Grisu) in `float.to_string` and interpolation instead of 6 significant digits (`0.1 + 0.2` prints `0.3`, `123456789.0` prints `1.23457e+08`).
- [ ] Fix `_format_float_fixed`, `format_float_prec`, and `_format_exp_f64` (`std/core/primitives.ll`) rounding `0.49999999999999994` (`0.5 - 2^-54`) up to `"1"` at precision `0` via `rem * float(scale) + 0.5`.
- [ ] Fix `float.format_commas` and `float.format_currency` (`std/strfmt/strfmt.ll`) grouping scientific-notation strings without `.` into `"1e,+06"` / `"$1e,+06"`.
- [ ] Guard `atan2`, `asin`, and `acos` (`std/math/math.ll`) against `NaN`, `(±INF, ±INF)`, and `(±0.0, -0.0)` instead of returning finite values (`±PI/2`, `0.0`) or `NaN`.
- [ ] Consolidate duplicated std APIs (`strings.pad_left_char` vs `strfmt.pad_left_fill`, `count` vs `count_occurrences`, `capitalize` vs `to_title`, `core_sqrt` vs `math.sqrt`, `os.file_exists` vs `fs.exists`), deprecating one of each with `@deprecated`.

### Files, paths, environment, terminal, flags

- [ ] `std/env` exposing `get_env(key: string) ?string`, `set_env(key: string, val: string) bool`, and `list_env() #[string: string]`.
  - [ ] `current_dir() Result[string]` and `set_current_dir(path: string) bool`.
  - [ ] `executable_path() Result[string]`.
- [ ] `std/path`: `path_join`, `path_dirname`, `path_basename`, `path_ext`, `path_is_abs`.
  - [ ] `path_normalize` and `path_rel`.
- [ ] `std/fs`: `is_dir`, `is_file`, `is_symlink`, `modified_time_ms`, `create_temp_file`.
- [ ] `std/fs` `read_dir` directory listing.
  - [ ] Recursive `walk_dir`.
- [ ] `std/term` ANSI color/style helpers.
  - [ ] `is_tty()` and terminal dimension queries.
- [ ] `std/flag` flag parsing (`-o`, `--release`, `--backend`, `--target`, `--cc`, ...).
  - [ ] Subcommand dispatch, validation, and generated `--help`.

### I/O and processes

- [ ] `std/io` `Reader`, `Writer`, `Seeker`, and `Closer` interfaces.
  - [ ] `stdin()`, `stdout()`, and `stderr()` handles.
  - [ ] `StringReader` and `BytesBuffer`.
  - [ ] `BufReader[R: Reader]` and `LineReader`.
  - [ ] `BufWriter[W: Writer]` with flush-on-drop.
- [ ] `std/process` `Command` builder (`fork`/`execvp` or `posix_spawn`, argv arrays, `.cwd(...)`, `.env(...)`).
  - [ ] `.spawn()` and `.wait_with_output()` returning separate `stdout`, `stderr`, and `exit_code`.
    - [ ] Child `stdin` pipes.

### Networking

- [ ] `getaddrinfo`-based IPv6/dual-stack TCP and DNS resolution in both runtimes and `std/net`.
- [ ] UDP sockets in both runtimes and `std/net`.
  - [ ] Native DNS: see [comptime](#1-compile-time-execution-comptime).
- [ ] Nonblocking socket polling. Its design is tied to the [concurrency model](#12-concurrency).
- [ ] Keep-alive connection reuse in the HTTP `Client`.
  - [ ] Connection pooling in the HTTP `Client`.
- [ ] Fix signed 64-bit `int` overflow in `_scan_chunked` (`pos + size + 1`) and `_MessageReader.read_message` (`body_start + length`) in `std/http/http.ll`, which wraps negative and panics in string slicing on untrusted large chunk sizes / `Content-Length` headers.
- [ ] Fix `resolve_reference` (`std/http/http.ll`) treating relative URLs with `://` after `?` or `#` (e.g. `"login?next=https://other.example"`) as absolute URLs, misdirecting redirects to `127.0.0.1:80`.
- [ ] Fix `_client_round_trip` (`std/http/http.ll`) forcing TLS (`u.port == 443`) on `http://host:443/` plaintext URLs.
- [ ] Reject non-numeric HTTP status codes in `Response.parse` and non-numeric or negative ports in `parse_url` (`std/http/http.ll`) instead of silently falling back to `200` / `scheme_port`.

## 9. Performance, backends, and targets

### std/core performance

- [ ] Skip per-element drop loops for plain element types in `List.clear` and `List.drop`, as `resize` already does.
- [ ] Pre-reserve capacity in `string.repeat`, `string.reverse`, and the padding helpers.
- [ ] Replace the byte-at-a-time FNV-1a `string.hash` with a word-at-a-time hash (wyhash/rapidhash), and consider per-process seeding against HashDoS.
- [ ] Lower `std/math` `sqrt`, `floor`, `ceil`, `trunc`, `round`, `fma`, `exp`, `log`, and trig functions to LLVM intrinsics / `<math.h>` instead of pure-Lowlight loops.

### Allocators

- [ ] `Allocator` in `std/core/alloc.ll` (`alloc`, `free`, `realloc`).
  - [ ] `List` with custom or arena allocators, without per-element heap drops.
  - [ ] `Set` and `Map` with custom or arena allocators.
  - [ ] `string` with custom or arena allocators.
- [ ] Gate `ll_rt_live_allocs` counter updates behind debug mode or a `--check-leaks` flag in `--release`.

### Code generation

- [ ] Keep pure SSA temporaries in virtual registers (only `alloca` mutable or address-taken locals) and skip `zeroinitializer` stores on locals proven assigned before read. (Started: `Local.NoZeroInit` covers plain assignment right-hand-side temporaries; a definite-assignment analysis would cover all locals.)
  - [ ] Run `mem2reg` and DCE so unoptimized builds clean up entry `alloca` temporaries. Native std code is 1.3–2x slower than the old runtime helpers in debug builds until these two land.
- [ ] Lower literal range loops (`a..b`, `a..=b`) to counted loops instead of the four-slot state machine.
- [ ] `internal` linkage for non-exported functions so LLVM can dead-strip unused `std/core` code and inline hot functions.
  - [ ] Call-graph reachability pruning in `internal/ir`, emitting optional runtime subsystems (net/TLS, fs, os, time, print helpers) on demand.
- [ ] Floating-point contraction in `--release` LLVM builds (`fmul contract`, `fadd contract`, `fsub contract`, or `@llvm.fmuladd`), matching C11 `-ffp-contract=on`, with a strict-FP opt-out if needed.
- [ ] Cheaper `ll_check_bounds`: one unsigned comparison, `cold`/`noreturn` on `ll_panic_cstr`, `@llvm.assume(len >= 0)` and branch weights in LLVM, `__builtin_expect` in C11.
  - [ ] Range-analysis bounds-check elimination and loop-invariant bounds hoisting in `internal/ir`.
- [ ] `noalias` on allocator results and `&T` parameters (`restrict` in C11), and `!tbaa` metadata, to enable auto-vectorization.
- [ ] Configurable `--release` flags (`--opt=3`, `--native` / `-mcpu=native`, ThinLTO across cached `.o` units).
- [ ] Compile phase-1 package `.o` units and test binaries in parallel (bounded by CPU count) in `internal/driver`.
- [ ] Debug info: `!DICompileUnit`, `!DISubprogram`, `!DILocation`, and `!dbg` metadata in the LLVM backend.

### Targets

- [ ] Unify OS/arch name conventions across the C11 and LLVM backends.
  - [ ] Switch the LLVM backend (`emitHeader`, runtime) from host `runtime.GOOS`/`GOARCH` checks to `Program.TargetTriple`.
    - [ ] Target-conditional link libraries and `.exe` outputs in `internal/driver/driver.go`.
      - [ ] Win64, RISC-V, and 32-bit layouts plus 64-bit file offsets.
      - [ ] Win32/Winsock shims and compiler attribute guards in the C11 runtime.

## 10. Tooling, LSP, and editors

- [ ] Debounce and background `analyzeAndPublish` in the LSP and honor `$/cancelRequest`, so completion and hover aren't queued behind full re-checks.
- [ ] Fix LSP `handleMessage` (`internal/lsp/server.go`, `transport.go`) replying with JSON-RPC `MethodNotFound` errors to client responses for server-initiated `window/showDocument` requests.
- [ ] Fix `executeRunCommand` setting `opts.Targets` instead of `opts.EntryPath` (and dropping `err`) and `executeTestCommand` ignoring `testName` (`opts.TestFilter`) in `internal/lsp/testing.go`.
- [ ] Fix `findWordRangeInSpan` (`internal/lsp/features.go`, `code_actions.go`, `quickfix_data.go`) matching substrings inside leading keywords (`let`, `bind`, `for`) or qualified expressions without word boundaries and corrupting code on quick-fix edits.
- [ ] Handle `bm.WhileSymbols` in `internal/lsp/references.go` and `internal/lsp/semantic_tokens.go` for `while(x in items)` / `while(i, x in items)` bindings.
- [ ] Traverse `StructLit`, `VecLit`, `ArrayLit`, `SetLit`, `MapLit`, `TupleLit`, `IndexExpr`, `SliceExpr`, `RangeExpr`, `MemberExpr`, and `InterpStringExpr` in `walkExprScopes` (`internal/lsp/completion.go`) so nested locals/lambdas get completions.
- [ ] Fix `formatActionEdits` / `declRegion` (`internal/lsp/code_actions.go`) truncating newly appended declarations (`appendDeclsEdit`) when the file ends with trailing blank lines.
- [ ] Fix `IfCondOptBind` (`if(x.val as val):`) semantic tokens (`internal/lsp/semantic_tokens.go`) highlighting `val` inside `v.Cond` instead of the `as val` binding.
- [ ] Fix formatter bugs (`internal/format/printer.go`, `config.go`, `internal/manifest/manifest.go`):
  - [ ] Keep `${...}` braces around `!`-suffixed identifiers in `printInterpString` (`${cfg!}` -> `$cfg!` re-lexes as `$cfg` + `!`).
  - [ ] Prevent wrapped generic static method chains (`Pool[Node].new().build()`) from emitting `Pool\n\t[Node].new()`, which triggers ASI and fails to parse.
  - [ ] Print attributes on leading `import` declarations in `emitImportBlock`.
  - [ ] Print `v.Scrutinees[0]` with `printPostfixBase` in `printResultChain` so non-postfix scrutinees (`(unsafe ...):`, `(a ?? b):`) keep their parentheses.
  - [ ] Distinguish explicit `[format].line_width = 0` from an omitted key in `lowlight.toml` (`FormatConfig` / `LoadConfigFound`) so `0` disables wrapping.
- [ ] Fix manifest dependency resolution (`internal/manifest/resolve.go`, `semver.go`):
  - [ ] Fall back to `existingLock` transitive edges in `Resolve` only when no manifest was loaded (`subM == nil`), not whenever `len(transDeps) == 0`.
  - [ ] Fix caret constraint `^0` (`partsCnt == 1`) in `versionClause.matches` using `0.0.1` instead of `1.0.0` as the upper bound.
- [ ] Fix driver and CLI bugs (`internal/driver/`, `cmd/lowlight/`):
  - [x] Resolve `[build].target` from `lowlight.toml` before filtering target-suffixed test files in `driver.Test`.
  - [ ] Use `defaultPackageEntryDir` for `-p <pkg>` and `--workspace` (`cmd/<app>/` layouts), and avoid running `driver.Check` twice (printing duplicate warnings) in `runWorkspaceBuild`.
  - [ ] Emit `"fail"` events and non-zero `"failed"` counts in `runSingleTestJSON` (`internal/driver/test_json.go`) when a `@test` binary crashes mid-test before printing `--- PASS:`/`--- FAIL:`.
  - [ ] Forward program flags in `lowlight run <path> -flag` (`parseCommonFlags` in `cmd/lowlight/main.go`) instead of rejecting them as `unknown flag`.
  - [ ] Don't skip the root target directory in `runFmt` (`cmd/lowlight/fmt.go`) when its basename starts with `.` (`lowlight fmt ..`, `lowlight fmt .hidden`).
- [ ] Fix docgen, markdown, and site bugs (`internal/docgen/`, `internal/markdown/`, `internal/site/`):
  - [ ] Handle `std/core` prelude entries in `site.BuildPackages` / `BuildAPIDocs` so project `lowlight doc` output doesn't emit broken `std_core_*.html` links.
  - [ ] Register package-level `ModAliases` and module names for multi-file packages (`std/collections`, `std/core`) in `internal/docgen/links.go` so `` [`collections`] `` and `` [`core.List`] `` resolve.
  - [ ] Fix `[src]` links in `renderModuleMarkdown` (`internal/docgen/markdown_out.go`) emitting `<pkg>.ll` instead of `<pkg>/<pkg>.ll`.
  - [ ] Clamp `lv < 1` in `(*renderer).heading` (`internal/markdown/markdown.go`) when `ShiftHeadings` is negative so headings don't emit `<h0 ...>` / `<h-1 ...>`.
  - [ ] Fix `findCloser` skipping only `j += 2` on longer delimiter runs (`*a ***b*** c*`) and `emphasis` `case n >= 3` not falling back to width `1` (`***foo** bar*`) in `internal/markdown/inline.go`.
- [ ] Make `lowlight run` report when the program is killed by a signal (it exits `255` with no message on SIGSEGV).
- [ ] Bound download and extraction sizes in `FetchBytes` (unbounded `io.ReadAll`) and the archive extractors (decompression bombs) in `internal/manifest/fetch.go`.
- [ ] Make `lowlight doc` skip gitignored and build directories (it documents `editors/jetbrains/build/...` and `editors/vscode/lowlight/std`).
  - [ ] **Decision:** whether `docs/` is committed or gitignored.
- [ ] Update the `§` section references in Go comments (`internal/`, `cmd/`, `tests/`) to the rewritten spec's numbering.
- [ ] Run the JetBrains plugin's tests (`./gradlew test` needs JDK 21, not installed when the `fn`/`cl` and `@derive` support was added).
- [ ] Consolidate `benchmarks/runner.go` into `benchmarks/runner.ll` (reporting parity in Lowlight, retiring the Go runner).
- [ ] Port `editors/vscode/package_vsix.py` to a Lowlight packaging tool. **Also needs:** [`walk_dir` and `std/process`](#8-standard-library).
  - [ ] Port the editor install scripts and doc/test orchestration helpers to Lowlight.

## 11. Testing and CI

- [ ] A `-short` mode in `tests/` that skips release-mode and C11 duplicates for quick local runs.
- [ ] Share compiled std object files between parallel test compiles (one warm `LOWLIGHT_CACHE` per `go test` run).
- [ ] Turn `examples/playground.ll` into a buildable example (it has no `main`) or move it out of `examples/`.
- [ ] Fix the `std/test` self-test "diff formatting helpers": the tab-indented multiline diff assertion fails on both backends (already failing at commit 2794079).
- [ ] Make the `std/net` inline tests (`std/net/net.ll` lines 466 and 541: `TcpListener` closed-port `refused` check and loopback `127.0.0.1` `connect()` in `local_addr`/`peer_addr`/`set_nodelay`) work cleanly or detect sandboxed `EPERM` so `TestStandardLibraryInlineSuites/(llvm|c11)/net` does not need to be skipped in restricted environments.
- [ ] A differential test that runs every e2e program under both backends and compares output.
- [ ] Go fuzz targets (`FuzzLexer`, `FuzzParser`, `FuzzFormat` idempotency, `FuzzCheck`, `FuzzCheckForIDE`) seeded from `std/` and `examples/`.
- [ ] A CI workflow running `go vet`, `go test ./...`, and `lowlight test` over `std/` on macOS and Linux for both backends.

## 12. Concurrency

### Decisions

Each needs the author; the implementation work below waits on all of them.

- [ ] **Decision:** the core model: OS `1:1` threads, M:N green threads, or stackless `async`/`await` (or a layered combination), and whether structured concurrency (`scope` / fork-join) is the primary safe primitive over detached threads.
  - [ ] **Decision:** how compile-time data-race freedom (§1.2) is enforced across threads: marker interfaces (`Send`/`Sync` equivalents) versus structural derivation from the plain/resource rules, and the handling of `ptr[T]`, `ref[T]`, `Arena`, thread-unsafe FFI state, and top-level resource `bind`s.
  - [ ] **Decision:** scoped borrowing without lifetime annotations (§1.2 principle 5): how structured concurrency and parallel algorithms borrow stack data read-only or disjoint-mutably without escape.
  - [ ] **Decision:** shared ownership versus move-only transfer across threads: reconcile "no reference-counting runtime" (§1.2) with shared handles (`Arc[T]`/`Shared[T]` versus message passing plus scoped sharing).
  - [ ] **Decision:** interior mutability under explicit `&` call-site mutation (§1.2 principle 6): `Mutex[T]`, `RwLock[T]`, RAII guards versus closure-scoped `with_lock`, and lock poisoning.
  - [ ] **Decision:** the memory model and atomics: `Atomic[T]` types, orderings (SC-DRF, relaxed/acquire/release/acqrel/seqcst), fences, and the safe/`unsafe` boundary for concurrent `ptr[T]` access.
  - [ ] **Decision:** message passing and synchronization primitives (`Channel` MPSC vs. MPMC, `select` syntax vs. API, `Condvar`, `Barrier`, `Once`/`OnceLock`, `@thread_local` vs. context passing).
  - [ ] **Decision:** parallel computing and vectorization: thread-pool ownership (global vs. explicit `ThreadPool`), parallel `for`/map/reduce/join, work stealing, and portable SIMD types vs. auto-vectorization.
  - [ ] **Decision:** cross-thread panics, cancellation, and I/O scaling: unwinding and `Drop` on panicking workers vs. abort, cancellation tokens, and thread pool vs. event loop (`epoll`/`kqueue`/`IOCP`) for `std/net` and `std/http`.
  - [ ] **Decision:** runtime thread-safety requirements in both backends: POSIX `pthreads` and Win32 threads, atomic/thread-local lowering, thread-safe panics, `Arena` thread confinement, and serialized `dlopen`/OpenSSL initialization.

### Implementation

- [ ] Document the finalized memory model, sendability rules, scoped borrowing, atomics, interior mutability, and panic behavior in `SPECIFICATION.md` and `IMPLEMENTATION.md`.
  - [ ] Static concurrency safety checks in `internal/types` and `internal/sema` (sendability/shareability inference, closure capture and escape checks, scoped borrow exclusivity, `Arena`/`ref[T]` confinement).
  - [ ] Atomic load, store, RMW, CAS, fence, and thread-local storage in `internal/ir`, lowered in both backends.
    - [ ] Cross-platform runtime threads, mutex, rwlock, condvar, and synchronization primitives in both runtimes (POSIX and Win32), with runtime globals and `dlopen` initialization hardened for concurrent access.
      - [ ] Concurrency stdlib modules (`Thread` / scoped spawn, `Atomic*`, `Mutex[T]`, `RwLock[T]`, `Condvar`, `Barrier`, `Once`, channels) with tests on both backends.
        - [ ] The parallel computing runtime (`ThreadPool`, fork-join and parallel slice/iterator algorithms, any SIMD primitives), and concurrent I/O in `std/net` and `std/http`.
        - [ ] ThreadSanitizer support (`--tsan`) in `internal/driver`, with multithreaded stress and compile-fail tests.
        - [ ] `assert_panics` in `std/test`, running the closure in a forked child process (panics are abort-only per §9.3) and reporting its panic message or "did not panic"; re-exec the test binary with a filter where there's no `fork`.

## 13. Self-hosting

Each stage depends on the one above it.

- [ ] Port `internal/diag` to `compiler/diag.ll` (`Span` struct). **Also needs:** [`std/term`](#8-standard-library).
  - [ ] Port `internal/token` to `compiler/token.ll` (enum with explicit tags).
    - [ ] Port `internal/lexer` to `compiler/lexer.ll` (`std/chars`, `std/strings`).
      - [ ] Port `internal/ast` to `compiler/ast.ll` (tagged unions, `Arena` `ref[Node]`).
        - [ ] Port `internal/parser` to `compiler/parser.ll` (lookahead checkpointing).
          - [ ] Port `internal/types` to `compiler/types.ll` (`TypeTable` / `Pool[Type]` indexed by `TypeId`).
            - [ ] Port `internal/sema` to `compiler/sema.ll` (move state machine, Tarjan topological sort).
              - [ ] Port `internal/ir` to `compiler/ir.ll` (basic-block CFG or linear instruction list).
                - [ ] Port `internal/codegen` to `compiler/codegen.ll` (growable strings, `embed_str` for the runtime header). **Also needs:** [`embed_str`](#1-compile-time-execution-comptime).
                  - [ ] Port `internal/driver` to `compiler/driver.ll`. **Also needs:** [`std/process`](#8-standard-library).
                    - [ ] Port `internal/lsp` to `compiler/lsp.ll` (JSON-RPC framing, stdio `Reader`). **Also needs:** [`std/io`](#8-standard-library).
                      - [ ] Verify bit-for-bit Stage 2 and Stage 3 binary parity. **Also needs:** deterministic codegen (done) and [debug info](#9-performance-backends-and-targets) if parity covers it.

## 14. std/json review notes

`std/json/json.ll` is hand-written work in progress. These notes are for its author; don't change the file on their behalf. It compiles; its `to_json` test fails 25 of 63 cases.

- [ ] `_parse_json_null` returns before advancing `idx`, so `null` followed by more input fails (failing case [62]: `Expected , or ]. Got: n`); it also has a stray `println`.
- [ ] The empty `[]` / `{}` early returns don't advance past the closing bracket (failing case [58]: `Got: ]`).
- [ ] `_parse_json_object` doesn't skip whitespace after a member value before checking for `,` / `}` (failing case [60], pretty-printed objects).
- [ ] `while(char(str[idx]).is_whitespace())` loops lack bounds checks, so truncated input such as `[1,` panics instead of returning an error.
- [ ] Escape handling in `_parse_json_string` never fires (it compares the backslash position against a quote), so `\"` ends the string early; `\n`, `\t`, `\\`, and `\uXXXX` aren't decoded.
- [ ] `char(str[idx])` converts one byte at a time, garbling multi-byte UTF-8 in string values.
- [ ] `string.to_json` trims into `in_str` but parses the untrimmed `self`, and doesn't reject trailing input after the top-level value.
- [ ] Number parsing has no exponent support (`1e5`), accepts `-` anywhere (`1-2`), and accepts leading zeros.
- [ ] `Json.to_string` doesn't escape quotes, backslashes, or control characters, and emits `nan`/`inf` for non-finite floats.
- [ ] `float`, `u8`–`u64`, and `i8`–`i32` `to_json` produce `Json.String` instead of `Json.Number`.
- [ ] Single-quoted strings are accepted, which isn't standard JSON.
- [ ] Error messages say "Jason".

---

## Done

### Language and compiler

- [x] Decided (2026-11-24): complete `comptime` and single-file platform selection design ([`COMPTIME_DESIGN.md`](./COMPTIME_DESIGN.md)): values + code unrolling only (no first-class `type` values or `comptime for` declaration synthesis); three-tier `let`/`bind`/`const` bindings with opt-in `const` function coloring + `@comptime_only`; `[N: const Type]` generic params (`int`s, `bool`, `char`, tag enums, `string`); safe evaluator + `std/core` hooks (`Plain`/`string`/`List[T]` crossing via `cap == 0`, `@comptime_limit`, runtime wrapping/traps-as-errors + `compile_error`); typed `*_of[T]()` descriptors with `_` privacy + `@derive` access, `v.field(f)`/`t.elem(e)`, collecting `comptime for(...) T:`, enum variant intrinsics, and `@derivable(...)` interfaces + `std/derive` + tuple generics; prelude `target` enums + single-file `comptime if` everywhere (including top-level declarations); `embed_str`/`embed_bytes` package-confined; `Related` trace frames + `@track_caller`, `lowlight expand`, and bounded memoized LSP.

- [x] `fn` and `cl` function types: `fn(T) R` captures nothing (one code pointer, plain); `cl(T) R` is a closure that owns its captures. Literals and all function declarations (including `extern` and interface method signatures) start with `fn`/`cl`. An `fn` converts implicitly to a `cl` where one is expected, `cl(f)` converts explicitly, and a `cl` never becomes an `fn`. A `cl` literal that captures nothing warns when its context doesn't require a closure.
- [x] `@derive(Equatable, Comparable, Hashable, Copyable, Printable)` for structs and enums, replacing the methods the compiler used to synthesize for them. On a generic type a derived method exists only for instances whose fields support it.
- [x] `Addable`/`AddAssignable`: `a + b` and `a += b` on non-numbers call `add` / `add_assign` (or `a = a.add(b)`).
- [x] `#[T]` and `#[K: V]` are the std/core `Set` and `Map` structs; both backends' hash-table code is gone. Custom `Hashable` types (derived or hand-written `hash`) work as elements and keys, and custom `eq` works in `contains` and `when in`.
- [x] The string code the compiler generates (`+`, `+=`, interpolation, slices, copies) and the string buffer methods (`push`, `push_byte`, `push_bytes`, `push_char`, `clear`, `reserve`, `cap`, `with_capacity`) are Lowlight in `std/core/primitives.ll`.
- [x] List literals and comprehensions inside loops start empty on every iteration (they reused the previous iteration's moved, freed storage).
- [x] Generic structs with a `drop` method instantiated while struct fields and signatures are resolved now get their `drop`.
- [x] `println(s.insert(4))` on a set compiles (it emitted invalid LLVM IR).
- [x] Flush `stdout` before writing panic messages to `stderr` in both runtimes.
- [x] Fix invalid LLVM IR for optional chaining through a self-referential optional field (`u.boss?.name`).
- [x] Make codegen output deterministic (the type collector now visits expression info in source order), with a determinism test in `tests/codegen_regressions_test.go`.
- [x] Fix formatter idempotency on overflowing single-line `if` bodies and single composite call arguments.
- [x] Result declarations (`define Name result [...]`), `!` names, result chains, bare member constructors, `??`, and `if(r! as v)`. Decided: `Result[T].ok(v)` is gone in favor of the member constructor `Result[T].value(v)`.
- [x] LSP hover, go-to-definition, and completion for a result's bare members (`value(...)`, `err(...)`) and for bare failure names in chain patterns (`when missing(why):`).
- [x] Unit results: a result's `value` member may have no payload (`define Saved result [value, full: Error, err: Error]`), constructed with bare `value`. A chain on one is a statement; `??`, `if(r! as v)`, and using the chain as a value are errors with their own messages. The prelude's `Status` (`[value, err: Error]`) is the unit counterpart of `Result[T]`. Decided: split-out failures carry an `Error`, like `err`, so no context is lost (`SPECIFICATION.md` §3.9 rule 8, `CONVENTIONS.md` §4).
- [x] A user type named like a std/core type (`Status`, `Error`) no longer shares generated symbols with it.
- [x] `sys_errno()` and `sys_strerror(code)` built-ins; the file and directory built-ins leave `errno` set on failure. The LLVM backend's `remove_all` no longer shells out to `rm -rf`.
- [x] Result chains on a borrowed subject (a read-only parameter) read it in place: statement chains work for any value type, value chains for plain ones.
- [x] `default panic` on a failure that carries an `Error` panics with that error, so its message, causes, and trace print.
- [x] Two `match` expressions or chains in one expression no longer free each other's operands (`(a(): default panic) == (b(): default panic)` compared freed memory).
- [x] `let _ = xs.pop()` (discarding an optional) no longer needs a `?` name.
- [x] `sys_errno_name(code)` built-in; the network built-ins leave `errno` set on failure.
- [x] LSP and editors (VS Code, Neovim, JetBrains) updated for `fn`/`cl`, `@derive` (completion, hover, quick fixes, "Derive I for T"), and the operator interfaces.

### Standard library

- [x] `Result` and `Status` methods narrowed to what chains, `??`, and `if(r! as v)` can't do: `is_ok`, `is_err`, `peek_err`, `wrap_err`, `trace_err`, `unwrap`, `expect`, `unwrap_err`, and `fail_msg`. Removed: `unwrap_or` (`??`), `unwrap_or_else`, `into_ok`, `into_err`, `map`, `and_then`, and `map_err` (chains).
- [x] Failures callers act on are separate result members across `std` (`SPECIFICATION.md` §3.9 rule 8): `fs.FileResult` (`not_found`) for `File.open` and the functions built on it, `fs.ReadResult` (`eof`) for `File.read_bytes`, `net.ConnectResult` (`not_found`, `refused`, `timeout`), `net.RecvResult` (`eof`, `timeout`), `net.AcceptResult` (`timeout`), `net.ListenResult` (`in_use`), `http.RequestResult` (`unreachable`, `timeout`), and `http.ListenResult` / `ServeOneResult` (`in_use`, `timeout`). Every OS failure carries the OS's description, coded with its errno name.
- [x] `os.errno_name` and `os.error_from_errno` replace the errno helpers `std/fs` and `std/os` each had.
- [x] `os.exec`, `system_run`, and `system_run_status` return a `Result`, failing when the shell can't start.
- [x] std's `if(r!.is_err()): ... unwrap_err()` sequences are result chains (`std/http`, `std/test`; `std/fs` and `std/net` were rewritten).
- [x] Decided: `List.insert`, `remove`, `swap_remove`, and `set` follow the `must_` convention. The plain names report a bad index (`insert` and `set` return `Status`; `remove` and `swap_remove` return `?T`); `must_insert`, `must_remove`, and `must_swap_remove` panic and are `@track_caller`.
- [x] Decided: custom result types (`fs.FileResult`, `net.ConnectResult`, `http.RequestResult`, ...) get no `is_ok`/`unwrap`-style methods; they're handled with chains only.
- [x] `strconv.parse_int_safe` and `parse_float_safe` are deprecated in favor of the prelude's `parse_int_radix(s, 10)` and `parse_float`.
- [x] `benchmarks/runner.ll` migrated to `fn`/`cl` (it hadn't compiled since the switch).
- [x] `std/fs` and `std/os` return unit results with the OS's description of each failure: `write_file`, `append_file`, `write_bytes`, `File.flush`, `File.close`, `os.mkdir_p`, and `os.remove_all` return `Status`; `remove_file` and `rename_file` return `RemoveResult` / `RenameResult` (`not_found` separate); `os.mkdir` returns `MkdirResult` (`exists` separate). `remove_all` refuses an empty path and `/`.
- [x] `Set` algebra: `union`, `intersection`, `difference`, `is_subset`.
- [x] `Map` helpers: `get_or_insert`, `update(key, f)` (runs `f: cl(&V)` on the value in place), `entries`.
- [x] `Arena` is Lowlight in `std/core/arena.ll` (a bump allocator over doubling chunks, recording an 8-byte `fn(ptr[u8])` drop entry per non-plain value); the built-in `Arena` type, its IR node, and both runtimes' arena code are gone. It is about twice as fast as the runtime version (20M small allocations: ~45 ms vs. ~80 ms). The checker's lifetime rules apply to the `@lang("arena")` struct. New unsafe intrinsic `ref_from_ptr`.
- [x] An `fn` type no longer counts as possibly holding a `ref[T]` (only a `cl` can capture one).
- [x] The capture-free lambdas in the Go test sources are `fn`.
- [x] C11: user `extern` functions are declared under private names bound to the real symbol (`__asm__` labels), so their signatures can't conflict with system headers; `@test_only` externs are left out of non-test builds.
- [x] `extern fn(...)` C function pointer types: `extern fn(params) R: body` literals that C can call (top-level or inline, no captures), `fn_from_ptr[extern fn(...) R](p)` for pointers from C, `ptr_cast` / `ptr_addr` back; calling a value is unsafe, and the signature must have a C representation. Decided: the literal syntax and `fn_from_ptr`.
- [x] In a test build, a panic inside an `extern fn` that C is running ends the test run instead of jumping back to the test runner across the C frames: C gets a wrapper that clears the runner's jump target while the callback runs. Direct Lowlight calls of the same function aren't wrapped.
- [x] `internal/tools/fnmigrate` and the parser's legacy-syntax hooks are gone; the old `(params):` form still gets its "starts with `fn` or `cl`" error.
- [x] C11: every user `extern` (header functions such as `qsort` included) is declared under its own name with an asm label, so a Lowlight signature never fights the header's.
- [x] `syscall(number, a1, ..., a6)` built-in using the target's system call instruction in both backends (macOS and Linux, arm64 and x86-64), returning `-errno` on failure everywhere. Only macOS arm64 is run here; the other three are checked to compile.
- [x] `std/collections`: `Deque[T]`, `Queue[T]`, `BitSet`, `PriorityQueue[T]` (ordered by a closure, with `min_first` / `max_first`, rather than a `Cmp` type parameter), `StringInterner`, and `BTreeMap[K: Comparable, V]` (a B-tree with `first`, `last`, and `range`).
- [x] `Map.with(key, f)` lends a value read-only to `f` without copying it, so values that can't be copied can be read in place.
- [x] `Map.push` moves the key and value out of its `MapEntry` instead of copying them, so map comprehensions work with values that can't be copied. (`Set.push` already moved its element.)
- [x] `std/json/json.ll` migrated to `fn`/`cl`, `@derive`, and the result declaration changes (by its author).
- [x] `std/json` moved into its own package directory (`std/json/json.ll`).
- [x] Check `ferror`/`flush`/`close` and support non-seekable files in `std/fs`.
- [x] Use `str_from_raw` and distinguish a clean EOF in `TcpStream.recv`.
- [x] `std/time`: normalize negative pre-1970 nanoseconds; fix negative `Duration.to_string`; handle `i64` overflow in `diff`, `add_duration`, and `sub_duration`; fix `.999` formatting and local timezone names in `format_go`; validate `DateTime` components; support ISO 8601 `Z`, fractions, and `+HH:MM` offsets.
- [x] `std/http`: no CPU spin on repeated `accept` failures; `Request.remote_addr` (IPv4 and IPv6); path vs. query `+` decoding; scheme-aware default ports; `#fragment` and `user:pass@`; IPv6 `[::1]:port`; case-insensitive and multi-value headers; `HEAD` framing; pipelined bytes; chunked transfer encoding; redirects in `Client`.
- [x] `StringBuilder` was replaced: a builder wrapping `string` gave no speed-up, since `push` grows in place; strings gained `string.with_capacity(n)`, `reserve(n)`, and `cap()`.
- [x] `ByteBuffer` (little-endian `u16`/`u32`/`u64` read/write helpers) in `std/strings`.
- [x] `std/math`: `round` near 0.5 and at `>= 2^52`; `cbrt` for extreme magnitudes; overflow-safe `hypot`; large-argument reduction in `sin`/`cos`/`tan`.
- [x] `format_float` honors precisions above 15.
- [x] Decided: out-of-range float parses return `none`; underflow still rounds to a signed zero.
- [x] Decided: `parse_int`/`parse_float` reject surrounding whitespace (trim explicitly), like Go and Rust; `to_int`, `to_float`, `is_int`, and `is_float` agree.
- [x] Decided: invalid `char(int)` conversions panic (`invalid char code point N`) in both backends.
- [x] `os.exec` / `os.system_run` distinguish signal termination and expose `ExecResult.signal`.
- [x] Decided: a final `\n` ends the last line, so `"a\nb\n".lines()` is `["a", "b"]`.
- [x] Decided: the prelude's `to_upper`/`to_lower` stay ASCII-only; `std/unicode` adds full, language-independent mappings with Final_Sigma, plus case folding.
- [x] `TcpStream.send_bytes`/`recv_bytes`, `shutdown`, `peer_addr`, `local_addr`, and `set_nodelay`.
- [x] `Result` combinators (`map`, `map_err`, `and_then`, `unwrap_or_else`; `ok() ?T` exists as `into_ok()`).
- [x] `List` helpers (`extend`, `truncate`, `first`, `last`, `index_of`, `swap`).
- [x] `SPECIFICATION.md` §3.4: `s.len()` is the byte count; spec and implementation reconciled, including `for(i, ch in s)` byte offsets.
