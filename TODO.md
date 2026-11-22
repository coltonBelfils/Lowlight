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

Chosen over Rust-style macros (2026-11-22). The design is to be worked through with the language's author before anything is built.

- [ ] **Decision:** design comptime. Open questions: the syntax (`comptime expr`, `comptime for`, `comptime if`, comptime parameters such as `[N: comptime int]`); which subset of Lowlight can run at compile time (heap use, loops, recursion limits, no I/O, determinism, `unsafe`); the type-description API (`fields_of[T]()`, `variants_of[T]()`, `elems_of[T]()`, `kind_of[T]()`, `type_name[T]()`, field attributes and visibility); access by compile-time name (`v.field(f)`, constructing a struct from compile-time field lists); the `target` description (`target.os`, `target.arch`, pointer size, endianness); how comptime code reports errors and where they point; whether comptime results can be types or new declarations; and how comptime interacts with generics, monomorphization, incremental builds, and the LSP.
  - [ ] Specify comptime in SPECIFICATION.md and document it on the website.
  - [ ] Implement the compile-time evaluator in `internal/sema` (or a new package) for the agreed subset, with limits on steps and memory.
    - [ ] Constant arithmetic, string concatenation, and fixed array lengths from comptime expressions (`[size_of[T](): u8]`).
      - [ ] Multi-pass constant folding with overflow checking, including `mod.C` and `size_of[T]()` (`internal/sema/types_pass.go`).
    - [ ] Compile-time tables and constants in std (CRC and Unicode tables, `math` constants) computed with `comptime` instead of written out by hand or built at startup.
    - [ ] `@embed_file`, `embed_bytes`, and `embed_str` intrinsics.
    - [ ] `lowlight expand` (or LSP support) that shows what comptime code unrolled to, including what `@derive` generates.
    - [ ] Type-description built-ins and `comptime for` / `comptime if` unrolling.
      - [ ] Rebuild `@derive` on comptime: `eq`, `cmp`, `hash`, `copy`, and `to_string` become ordinary generic functions in a `std/derive` package (`comptime for(f in fields_of[T]())` with `v.field(f)`), and `@derive(I)` only attaches them (`bind Point.eq = derive.eq[Point]`). Replaces the source-text generator in `internal/sema/derive.go`, so derived code is readable, editable Lowlight whose errors come from normal type checking.
        - [ ] Let std and user packages provide their own derivable implementations that `@derive` attaches.
        - [ ] Delete the struct/enum `eq`/`cmp`/`hash`/`copy`/`to_string` helpers in `cgen.go` and `llvmgen_runtime.go`. Drop glue stays compiler-generated. **Also needs:** [tuple methods in Lowlight](#3-tuples).
      - [ ] Tuple methods in Lowlight: see [Tuples](#3-tuples).
    - [ ] `target` and compile-time platform selection (dropped branches are not compiled for other targets).
      - [ ] **Decision:** per-platform files versus `comptime if(target.os == ...)` within one file, for platform-specific std code.
        - [ ] Native OS layer: replace the C `sys_*` built-ins (files, sockets, time, processes, environment, CPU count) with Lowlight in a `std/os`-level package, with the same API on every platform. Linux uses the `syscall` built-in; macOS calls `libSystem` through `extern`, since Apple doesn't keep raw system calls stable. **Also needs:** [the `syscall` built-in](#2-function-types-and-c-interop).
          - [ ] Move the backend-agnostic helpers left in `internal/codegen/runtime_h.go` and `internal/codegen/llvmgen_runtime.go` (those that don't need C or the OS) into `std/core/*.ll`.
          - [ ] Replace the fixed `char sub[1024]` buffers in `ll_os_remove_all` (`internal/codegen/runtime_h.go`) and the LLVM runtime's `rm -rf` shell-out, if the native layer isn't done first.
          - [ ] Native DNS: a resolver written in Lowlight (read `/etc/resolv.conf` and `/etc/hosts`, A/AAAA queries over UDP with TCP fallback, timeouts and retries), so name lookup stops going through the C library's `getaddrinfo`. **Also needs:** [UDP sockets](#8-standard-library).
          - [ ] Native TLS (long term): TLS 1.2/1.3 in Lowlight (handshake, record layer, X.509 verification against the system root store) with the cryptography it needs (AES-GCM, ChaCha20-Poly1305, SHA-2, HMAC/HKDF, X25519, ECDSA P-256, RSA verification), replacing the OpenSSL `dlopen` loader. Until then TLS stays on OpenSSL through C interop.

## 2. Function types and C interop

`fn` (no captures, plain) and `cl` (closures) are implemented; see [Done](#done). Native TLS is long term, so C interop stays. `extern` is likely C-only (undecided; the spec currently allows other languages).

- [ ] Migrate the lambdas in Go test sources (`tests/*.go`, `internal/**/*_test.go`) that capture nothing from `cl` to `fn` (the migration made every lambda there a `cl`).
- [ ] Revisit whether `fn` should keep converting to `cl` implicitly, or only through `cl(f)`.
- [ ] `json.ll` migration to `fn`/`cl` (its author's file): `go run ./internal/tools/fnmigrate std/json/json.ll`.
  - [ ] Delete `internal/tools/fnmigrate` and the parser's legacy-syntax hooks (`AcceptLegacyFuncSyntax`, `RecordLegacyFuncSites`).
- [ ] Move `Arena` into `std/core`. A prototype in Lowlight works: `alloc[T]` stores an entry that drops the value, and `reset` calls the stored entries; it runs as fast as the runtime version. With `fn` types each entry holds an 8-byte `fn(ptr[u8])` instead of a 48-byte closure entry. The checker's arena rules (pinning, `ref[T]` lifetimes) stay in the compiler.
- [ ] `extern fn(...)` function pointer types with the C calling convention, for C callbacks (`qsort`, signal handlers) and calling C function pointers (`dlsym`).
  - [ ] Callbacks from C into Lowlight (`extern fn` values made from Lowlight `fn`s, with panics not unwinding through C).
- [ ] C struct layout for any struct passed to C (a `@repr(c)`-style guarantee beyond `packed`), checking that every field has a C representation.
- [ ] Calling C variadic functions (`printf`, `fcntl`, `open` with a mode).
- [ ] Avoid C11 prototype conflicts for user `extern` declarations of libc/POSIX functions missing from `stdCHeaderFuncs` (`internal/codegen/cgen.go`); e.g. `extern write: fn(fd: i32, buf: ptr[u8], n: int) int` fails `-Werror` against `unistd.h`.
- [ ] Stop emitting `@test_only` externs in non-test builds (a test-only `extern write` in `std/fs` broke the C11 package build of every program importing `std/fs`).
- [ ] A `syscall(number, a1, ..., a6) int` built-in that emits the target's system call instruction in both backends (inline assembly in the LLVM IR; `__asm__` in C).
  - [ ] The native OS layer: see [comptime](#1-compile-time-execution-comptime).

## 3. Tuples

Tuples are due for an overhaul (2026-11-22); `@derive` deliberately doesn't apply to them yet.

- [ ] **Decision:** the tuple overhaul: what changes about tuples, and whether they get methods (for example `bind +[A, B].hash = ...` per arity) or comptime-generic implementations.
  - [ ] Tuple `eq`/`cmp`/`hash`/`copy`/`to_string` written in Lowlight (with `comptime for` over `elems_of[T]()`), replacing the helpers both backends synthesize. Do optionals at the same time (they may only need ordinary generic code). **Also needs:** [comptime type-description built-ins](#1-compile-time-execution-comptime).
  - [ ] Flat tuple destructuring in declarations (`let +[a, b] = ...`, including mutable, typed, and wildcard elements).
    - [ ] Nested tuple destructuring in declarations and `match` patterns.
  - [ ] Add tuples (and optionals) to `LookupMethod` (`internal/sema/program.go`, `internal/sema/types_pass.go`).

## 4. Operators

- [ ] **Decision:** names and grouping for the remaining operator interfaces, following `Addable`/`AddAssignable` (`std/core/interfaces.ll`, `operatorMethod` in `internal/sema/collections.go`): `-`/`-=` (`Subtractable`), `*`/`*=`, `/`/`/=`, `%`/`%=`, unary `-` (`Negatable`), and the bitwise `&`, `|`, `^`, `~`, `<<`, `>>` with their assignment forms; one interface per operator, or grouped ones (say `Numeric`) as bounds.
  - [ ] Implement them in sema and IR, with each numeric primitive implementing them in `primitives.ll` as `add` does.
- [ ] **Decision:** whether lists get `+`.
  - [ ] `Addable` for `[T]` (`[1] + [2]`) and `AddAssignable` as `append`.

## 5. Results and the failure model

Apply the `Result` vs. `panic` rules and the `x` / `must_x` naming convention from `SPECIFICATION.md` §9.1 across `std`.

- [ ] A payload-less `value` member (a unit result).
  - [ ] Replace success-only `Result[bool]` returns (`fs.write_file`, `fs.append_file`, `fs.write_bytes`) with a unit result.
  - [ ] Return `Result` with the OS error message instead of `bool` from `File.close`, `File.flush`, `fs.remove_file`, and `fs.rename_file` (`std/fs/fs.ll`).
  - [ ] Return `Result` with the OS error message instead of `bool` from `os.mkdir`, `os.mkdir_p`, and `os.remove_all` (`std/os/os.ll`).
- [ ] Return `Result[ExecResult]` from `os.exec` instead of reporting a failed launch as `exit_code: -1` (`std/os/os.ll`).
- [ ] Allow a result chain on a borrowed subject (`(r!: Result[string])` parameters) without moving a non-plain value out, e.g. binding the value as a borrow when the chain only reads it.
- [ ] Include the error's message and trace in `default panic` output for `Result[T]` (it prints only `unhandled failure Result.err`).
- [ ] Rewrite std's `if(r!.is_err()): func <- ...unwrap_err()...` / `r!.unwrap()` sequences in `std/fs`, `std/http`, `std/net`, and `std/test` as result chains.
- [ ] **Decision:** whether optional lookups (`List.get`, `Array.get`, `Map.get` returning `?T`, paired with panicking `xs[i]`) are an accepted exception to the `try_` prefix; document it in `SPECIFICATION.md` §9.1.
  - [ ] Make `List.set` panic on an out-of-range index like `insert`/`remove` (it returns `false` and silently drops `val`), and add `List.try_set`.
  - [ ] Add `List.try_insert`, `List.try_remove`, and `List.try_swap_remove` returning `Result`/`?T` alongside the panicking forms.
  - [ ] Rename `strconv.parse_int_safe` / `parse_float_safe` (which return `?T`) to fit the convention, or remove them as duplicates of `parse_int` / `parse_float`, deprecating the old names.
- [ ] Mark `List.insert`, `List.remove`, and `List.swap_remove` `@track_caller` so out-of-bounds panics report the caller's location.
- [ ] Audit `std` (especially `std/http`, `std/strconv`, and `std/time` parsing) for panics reachable from external input, and convert them to `Result`/`?T` per §9.1.
  - [ ] Require a `# Panics` doc section on every public std function that can panic, with a `lowlight doc` lint that flags missing ones.

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

## 7. Compiler correctness

### Soundness (first)

- [ ] Binding an element of a borrowed resource list moves it out without an error: in `bind f = fn(xs: [string]) int: { let r = xs[0]; r.len() }`, `r` is dropped at the end of `f`, freeing the caller's string. `let r = xs[i]` on a read-only `[T]` of resources should be an error (suggest `.copy()`) or a borrow.
- [ ] Enforce immutability/borrow rules when mutating through `ref[T]` in `analyzePlace` (`internal/sema/check_stmt_expr.go`).
- [ ] Prevent `Arena` method receivers from bypassing `checkMutatingMethodRecvEscape` (`internal/sema/check_calls.go`).
- [ ] Enforce `&Arena` captures inside nested closures.
- [ ] **Decision:** rethink `unsafe` so it enforces stronger invariants (keep unrelated borrow/move/type checks active inside it, scope raw-pointer and intrinsic capabilities narrowly, and reduce convenience `unsafe` blocks in `std`).

### Parser

- [ ] Qualified/generic bounds and receiver bounds in `parseTypeParams`.
- [ ] Struct field-init shorthand (`Point[x, y]`).
- [ ] Resolve the generic call vs. index ambiguity on member expressions (`obj.fns[i]()`) in the parser and `internal/sema/check_calls.go`.
  - [ ] Remove the remaining in-place AST rewrites in sema (`xs[i].m` and `fns[i](...)` disambiguation, now serialized by `astRewriteMu`) by resolving them into side tables, so cached ASTs are strictly read-only.

### Sema

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
- [ ] Allow the bare `_` discard for optional values (`let _ = m.remove(k)` errors with "must end with `?`", although §2.5 exempts `_`).
- [ ] Fix assigning a `??`-with-unwind guard to an optional variable: `p? = parse_int(s) ?? func <- -1` emits invalid LLVM IR and C (the `int` result isn't wrapped into `?int`).
- [ ] **Decision:** how to take several fields out of an `own self` receiver (moving one field consumes `self`, so `Pair.swap = fn(own self)` can't move both fields; there is no struct destructuring).
- [ ] **Decision:** whether `abs` is numeric like `min`/`max` (the prelude's `abs(x: int) int` shadows the numeric builtin, so `abs(-2.5)` is a type error).
- [ ] Improve the diagnostic for a missing associated function on an imported type: `time.Duration.seconds(9)` reports `undeclared identifier "time"`.
- [ ] Warnings for unused locals, imports, and private functions, and for unreachable code after `<-`.
- [ ] Log recovered panics (with stack) from `CheckForIDE` instead of swallowing them (`internal/sema/program.go`), and surface them through LSP `window/logMessage`.

### IR

- [ ] Replace `lowerRawExpr` with `lowerExprOwned` in `internal/ir/lower_expr.go` and `lower_ops.go`.
- [ ] Evaluate lvalues once in `emitCompoundAssign` (`internal/ir/lower.go`) and reuse checked element pointers to avoid duplicate `ll_check_bounds` calls.
- [ ] Forward `TrackCaller`/`TargetFunc` in custom `IndexExpr` lowering.
- [ ] Avoid copying owned `Array` values on read indexing.
- [ ] Support `&self` receivers and mutating methods under optional chaining (`internal/ir/lower_calls.go`).
- [ ] Retire the remaining hardcoded `vec.*` builtin method lowerings in favor of `std/core` `List` (sets, maps, strings, and the compiler's string code are done; `string.len`, `byte_at`, and `c_str` remain built-ins).
  - [ ] Replace hardcoded `"List"`/`"Array"` name checks in `internal/types/types.go` with the `Struct.Lang` tag.
  - [ ] Guard capacity doubling against `int64_t` overflow and zero vacated slots in the C11 and LLVM vector helpers, and fix their view materialization for `reserve`, `pop`, `remove`, and `swap_remove`, if the helpers survive.
- [ ] Complete vtable method dispatch for `Vector` in both backends, and fail compilation cleanly on unhandled interface methods.

### LLVM backend

- [ ] Fix stub `""` primitive `to_string` handling and the `_to_string` wrapper heuristics.
- [ ] Fix `bool`/`i1` casts.
- [ ] Fix diverging right-hand-side handling and metadata in `InstrAssign`/`InstrCompoundAssign`.
- [ ] Fix under-aligned `align 8` loads/stores.

### Runtime

- [ ] Enable peer certificate verification (`SSL_CTX_set_verify` with `SSL_VERIFY_PEER`), hostname checks (`SSL_set1_host`), and `SSL_get_verify_result` in both runtimes.
- [ ] Match LLVM `ll_tls_recv`, `ll_tls_send_all`, and `ll_tls_server_ctx_new` with C11 (`SSL_pending`, `poll`, timeout `-2`, `WANT_READ`/`WANT_WRITE`, `SSL_CTX_use_certificate_chain_file`).
  - [ ] Expose public TLS server APIs/contexts in `std/net`.
- [ ] Detect stack overflow and print a Lowlight panic (guard page plus `sigaltstack` SIGSEGV handler) instead of dying with exit code 139, in both runtimes.

## 8. Standard library

### Collections

- [ ] `Set` algebra (`union`, `intersection`, `difference`, `is_subset`) in `std/core/set.ll`.
- [ ] `Map` update helpers (`get_or_insert`, `update(key, f)`, `entries`) in `std/core/map.ll`.
- [ ] Borrowed read access to non-`Copyable` values in `Map`/`List` (`m.get(k)` and `m[k]` need a copyable `V`, leaving only `remove` for others), e.g. a closure-scoped `with(key, ...)`.
- [ ] Move key and value out of the `MapEntry` in `Map.push` (and the element in `Set.push`) instead of copying.
- [ ] `std/collections`: `Queue[T]`, `Deque[T]`, `BitSet`, `PriorityQueue[T, Cmp]`, `StringInterner`, `BTreeMap[K, V]`.

### Strings, numbers, and API cleanup

- [ ] Shortest round-trip float formatting (Ryu/Grisu) in `float.to_string` and interpolation instead of 6 significant digits (`0.1 + 0.2` prints `0.3`, `123456789.0` prints `1.23457e+08`).
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
                - [ ] Port `internal/codegen` to `compiler/codegen.ll` (growable strings, `@embed_file` for the runtime header). **Also needs:** [`@embed_file`](#1-compile-time-execution-comptime).
                  - [ ] Port `internal/driver` to `compiler/driver.ll`. **Also needs:** [`std/process`](#8-standard-library).
                    - [ ] Port `internal/lsp` to `compiler/lsp.ll` (JSON-RPC framing, stdio `Reader`). **Also needs:** [`std/io`](#8-standard-library).
                      - [ ] Verify bit-for-bit Stage 2 and Stage 3 binary parity. **Also needs:** deterministic codegen (done) and [debug info](#9-performance-backends-and-targets) if parity covers it.

## 14. std/json review notes

`std/json/json.ll` is hand-written work in progress. These notes are for its author; don't change the file on their behalf. The suite fails to compile until the first two items are done.

- [ ] Migrate to `fn`/`cl`: `go run ./internal/tools/fnmigrate std/json/json.ll`.
- [ ] Add `@derive(Copyable)` to `Json` and `@derive(Equatable)` to `Number` (structs and enums no longer get these automatically).
- [ ] Migrate to the result declaration changes: `Result.Ok` is now `Result.value`, `Result.ok`/`Result.fail` were removed, `Result.Err` is now `Result.err`, names holding a `Result` must end in `!`, and `Error.wrap` consumes the error, so `Result.Err(e)` / `Err(e)` need `err(own e)`.
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
- [x] LSP and editors (VS Code, Neovim, JetBrains) updated for `fn`/`cl`, `@derive` (completion, hover, quick fixes, "Derive I for T"), and the operator interfaces.

### Standard library

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
