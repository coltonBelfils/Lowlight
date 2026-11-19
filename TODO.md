# Lowlight TODO

Tasks are atomic and ordered from easiest to most involved.

## Stdlib Bug Fixes & Targeted Library Additions

- [ ] Add `set` and `reserve` to `List[T]` in `std/core/list.ll`.
- [ ] Add `insert`, `remove`, and `swap_remove` to `List[T]` in `std/core/list.ll`.
- [ ] Add `List[T].filled(len, val)` and `resize(n, val)` in `std/core/list.ll` using `alloc_uninit` (`malloc`) + `memset` for 1-byte `Plain` types (`bool`, `u8`, `i8`) and capacity-check-free fill loops for other `Plain` types.
- [ ] Invalidate or guard `ListIterator` on mutation in `std/core/list.ll`.
- [ ] Zero `realloc_raw`-grown slots before `= own val` initialization in `std/core/set.ll` and `std/core/map.ll` so MIR does not drop uninitialized memory.
- [ ] Rehash in place when tombstone count is high at low load in `std/core/set.ll` and `std/core/map.ll`.
- [ ] Remove `.copy()` requirements in comparator and search helpers in `std/sort.ll`.
- [ ] Support `[T]`/`Array[T]` and add `binary_search_by`, `lower_bound`, and `upper_bound` in `std/sort.ll`.
- [ ] Switch from unbounded recursive Quicksort (`std/sort.ll:78-141`) to depth-bounded Introsort.
- [ ] Add a stable sort algorithm to `std/sort.ll`.
- [ ] Remove `Copyable` bounds on `Pool[T].get` (`std/pool.ll:84-90`).
- [ ] Add `with_mut(&T)`, `clear()`, and iterators to `Pool[T]` in `std/pool.ll`.
- [ ] Check `ferror`/`flush`/`close` and support non-seekable files in `std/fs.ll:162-242`.
- [ ] Use `str_from_raw` and distinguish clean EOF in `TcpStream.recv` (`std/net.ll:57-73`).
- [ ] Normalize negative pre-1970 nanoseconds in `from_unix_millis`/`nanos` (`std/time.ll:284-295`).
- [ ] Fix negative formatting in `Duration.to_string` in `std/time.ll`.
- [ ] Handle `i64` overflow in `diff`, `add_duration`, and `sub_duration` in `std/time.ll`.
- [ ] Fix `.999` formatting in `format_go` (`std/time.ll:603-611`).
- [ ] Fix local timezone names in `format_go` (`std/time.ll:244,523-636`).
- [ ] Validate `DateTime` components in `std/time.ll`.
- [ ] Support ISO 8601 `Z`, fractional, and `+HH:MM` offsets in `std/time.ll`.
- [ ] Add `assert_panics` to `std/test.ll`.
- [ ] Fix `Server.serve` CPU spin on repeated `accept` failures in `std/http.ll`.
- [ ] Populate `req.remote_addr` in HTTP `Server` connection handling in `std/http.ll`.
- [ ] Fix path vs query `+` decoding in `parse_url` (`std/http.ll`).
- [ ] Use scheme-aware default ports in `parse_url` and `URL.to_string` (`std/http.ll`).
- [ ] Support `#fragment` and `user:pass@` in `parse_url` and `URL.to_string` (`std/http.ll`).
- [ ] Support IPv6 `[::1]:port` in `parse_url` and `URL.to_string` (`std/http.ll`).
- [ ] Implement exact case-insensitive header parsing in `std/http.ll`.
- [ ] Support multi-value headers in `std/http.ll`.
- [ ] Handle `HEAD` response framing in `_read_http_message` and `_read_http_message_tls` (`std/http.ll`).
- [ ] Buffer pipelined remainder bytes in `_read_http_message` and `_read_http_message_tls` (`std/http.ll`).
- [ ] Implement `Transfer-Encoding: chunked` decoding in `_read_http_message` and `_read_http_message_tls` (`std/http.ll`).
- [ ] Support HTTP redirects in `Client` in `std/http.ll`.
- [ ] Add `StringBuilder` (`with_capacity`, `append`, `append_char`, `append_byte`, `append_int`, `append_float`, `to_string() own string`) in `std/strings.ll`.
- [ ] Add `ByteBuffer` (little-endian `u16`/`u32`/`u64` read/write helpers) in `std/strings.ll`.

## Parser, Sema, IR, Codegen & Runtime Hardening

- [ ] Support qualified/generic bounds and receiver bounds in `parseTypeParams` (`internal/parser/parser.go:578-588,624-629`).
- [ ] Add struct field-init shorthand (`Point[x, y]`) in `internal/parser/parser.go`.
- [ ] Resolve generic call vs index ambiguity on member expressions (`obj.fns[i]()`) across `internal/parser/parser.go:1836-1886` and `internal/sema/check_calls.go:56-72`.
- [ ] Replace fixed `char sub[1024]` buffers in `ll_os_remove_all` (`internal/codegen/runtime_h.go:1205-1206`).
- [ ] Replace LLVM `rm -rf` shell execution in `ll_os_remove_all` (`internal/codegen/llvmgen_runtime.go`).
- [ ] Enable peer certificate verification (`SSL_CTX_set_verify` with `SSL_VERIFY_PEER`), hostname checks (`SSL_set1_host` / `X509_VERIFY_PARAM_set1_host`), and `SSL_get_verify_result` in `internal/codegen/runtime_h.go:944-976` and `internal/codegen/llvmgen_runtime.go:2547-2746`.
- [ ] Match LLVM `ll_tls_recv`, `ll_tls_send_all`, and `ll_tls_server_ctx_new` with C11 (`SSL_pending`, `poll`, timeout `-2`, `WANT_READ`/`WANT_WRITE`, `SSL_CTX_use_certificate_chain_file`) in `internal/codegen/llvmgen_runtime.go`.
- [ ] Expose public TLS server APIs/contexts in `std/net.ll`.
- [ ] Zero-initialize `allocSlot` temporaries at collection literal sites (`internal/ir/lower_ops.go:1905-1919,2030-2059`) so loop iterations do not reuse moved/freed collection buffers.
- [ ] Replace `lowerRawExpr` with `lowerExprOwned` in `internal/ir/lower_expr.go:447,788` and `internal/ir/lower_ops.go:397`.
- [ ] Evaluate lvalues once in `emitCompoundAssign` (`internal/ir/lower.go:977-988`) and reuse checked element pointers to avoid duplicate `ll_check_bounds` calls.
- [ ] Forward `TrackCaller`/`TargetFunc` in custom `IndexExpr` lowering in `internal/ir`.
- [ ] Avoid copying owned `Array` values on read indexing in `internal/ir`.
- [ ] Support `ModeMut` (`&self`) receivers and mutating methods under optional chaining (`internal/ir/lower_calls.go:116-140,611-641`).
- [ ] Guard capacity doubling against `int64_t` overflow and zero vacated slots in C11 and LLVM vector helpers (`internal/codegen/cgen_collections.go:16-31,50-95` and `internal/codegen/llvmgen.go`).
- [ ] Fix C11 and LLVM vector view materialization (`internal/codegen/cgen_collections.go:16-31,50-95` and `internal/codegen/llvmgen.go`) for `reserve`, `pop`, `remove`, and `swap_remove`.
- [ ] Fix stub `""` primitive `to_string` handling in `internal/codegen/llvmgen.go`.
- [ ] Fix `_to_string` wrapper heuristics in `internal/codegen/llvmgen.go:1013-1024`.
- [ ] Fix LLVM `bool`/`i1` casts in `internal/codegen/llvmgen.go:2815-2847`.
- [ ] Fix diverging RHS handling and metadata in `InstrAssign`/`InstrCompoundAssign` (`internal/codegen/llvmgen.go:1060-1074`).
- [ ] Fix under-aligned `align 8` loads/stores in `internal/codegen/llvmgen.go`.
- [ ] Complete VTable method dispatch in both backends (`internal/codegen/cgen.go:1275-1352`, `internal/codegen/llvmgen.go:3398-3524`) for `Vector` and `Set` and fail compilation cleanly on unhandled interface methods.
- [ ] Support custom `Hashable` structs in `IsHashable` (`internal/types/types.go:835-855`).
- [ ] Replace hardcoded `"List"`/`"Array"` name checks in `internal/types/types.go`.
- [ ] Check `TypeParamVar` in `checkOptionalIdentRule` in `internal/sema/types_pass.go`.
- [ ] Validate `GenIface*`/generic `@lang` duplicates in `internal/sema/program.go` and `internal/sema/types_pass.go`.
- [ ] Fix `instantiateStruct` nil-deref and cache races in `internal/sema/types_pass.go`.
- [ ] Add `Set`, `Map`, `Optional`, and `Tuple` to `LookupMethod` in `internal/sema/program.go` and `internal/sema/types_pass.go`.
- [ ] Scope generic method tables away from bare-name collisions in `internal/sema/program.go` and `internal/sema/types_pass.go`.
- [ ] Generalize `GenIfaceIterable` in `internal/sema/program.go` and `internal/sema/types_pass.go`.
- [ ] Support multi-pass constant folding with overflow checking in `internal/sema/types_pass.go`.
- [ ] Support `mod.C` and `size_of[T]()` in constant folding in `internal/sema/types_pass.go`.
- [ ] Support method type inference in `internal/sema`.
- [ ] Fix `?[ ` optional array unwrapping in `internal/sema/check_stmt_expr.go` and `internal/sema/check_calls.go`.
- [ ] Fix `?T` LUB in yields in `internal/sema/check_stmt_expr.go`.
- [ ] Fix mutable `&T` covariance checks in `internal/sema/check_stmt_expr.go` and `internal/sema/check_calls.go`.
- [ ] Fix `checkIfExpr` move/condition checks in `internal/sema/check_stmt_expr.go`.
- [ ] Fix `consumeValue` Plain-to-Interface moves in `internal/sema/check_stmt_expr.go`.
- [ ] Fix `consumeValue` whole-`self` field moves in `internal/sema/check_stmt_expr.go`.
- [ ] Perform full state rollback on probe checks in `internal/sema/check_calls.go` and `internal/sema/check_stmt_expr.go`.
- [ ] Fix `checkForExpr` move-state and generic substitution merges in `internal/sema/check_stmt_expr.go`.
- [ ] Fix `Iterable[T]` and user `.iter()` resolution in `internal/sema/check_calls.go` and `internal/sema/check_stmt_expr.go`.
- [ ] Support custom `.eq()` in `contains` and `match` in `internal/sema/check_stmt_expr.go` and `internal/sema/check_calls.go`.
- [ ] Fix `match` duplicate-arm and `"-0"` bugs in `internal/sema/check_stmt_expr.go`.
- [ ] Fix multi-arg `match` ownership bugs in `internal/sema/check_stmt_expr.go`.
- [ ] Support `*types.Ref` in `checkSliceExpr` (`internal/sema/check_stmt_expr.go:3094-3125`).
- [ ] Support `*types.Ref` in non-enum `match` (`internal/sema/check_stmt_expr.go:4935-4940`).
- [ ] Prevent `Arena` method receivers from bypassing `checkMutatingMethodRecvEscape` (`internal/sema/check_calls.go:1200`).
- [ ] Enforce immutability/borrow rules when mutating through `ref[T]` in `analyzePlace` (`internal/sema/check_stmt_expr.go:819-821`).
- [ ] Enforce `&Arena` captures inside nested `FuncLit` closures (`internal/sema/check_stmt_expr.go:3765`).

## New Stdlib Modules & Networking Extensions

- [ ] Create `std/env.ll` exposing `get_env(key: string) ?string`, `set_env(key: string, val: string) bool`, and `list_env() #[string: string]`.
- [ ] Add `current_dir() Result[string]` and `set_current_dir(path: string) bool` to `std/env.ll`.
- [ ] Add `executable_path() Result[string]` to `std/env.ll`.
- [ ] Create `std/path.ll` exposing `path_join`, `path_dirname`, `path_basename`, `path_ext`, and `path_is_abs`.
- [ ] Add `path_normalize` and `path_rel` to `std/path.ll`.
- [ ] Add `is_dir`, `is_file`, and `is_symlink` to `std/fs.ll`.
- [ ] Add `modified_time_ms` to `std/fs.ll`.
- [ ] Add `create_temp_file` to `std/fs.ll`.
- [ ] Add `read_dir` directory listing to `std/fs.ll`.
- [ ] Add recursive `walk_dir` traversal to `std/fs.ll`.
- [ ] Create `std/term.ll` with ANSI color/style helpers.
- [ ] Add `is_tty()` detection and terminal dimension queries to `std/term.ll`.
- [ ] Create `std/flag.ll` supporting flag parsing (`-o`, `--release`, `--backend`, `--target`, `--cc`, etc.).
- [ ] Add subcommand dispatch, validation, and generated `--help` text to `std/flag.ll`.
- [ ] Create `std/io.ll` defining the `Reader`, `Writer`, `Seeker`, and `Closer` interfaces.
- [ ] Add `stdin()`, `stdout()`, and `stderr()` handles to `std/io.ll`.
- [ ] Add `StringReader` and `BytesBuffer` to `std/io.ll`.
- [ ] Add `BufReader[R: Reader]` and `LineReader` to `std/io.ll`.
- [ ] Add `BufWriter[W: Writer]` with flush-on-drop to `std/io.ll`.
- [ ] Implement `Queue[T]` under `std/collections/`.
- [ ] Implement `Deque[T]` under `std/collections/`.
- [ ] Implement `BitSet` under `std/collections/`.
- [ ] Implement `PriorityQueue[T, Cmp]` under `std/collections/`.
- [ ] Implement `StringInterner` under `std/collections/`.
- [ ] Implement `BTreeMap[K, V]` under `std/collections/`.
- [ ] Create `std/process.ll` with a `Command` builder backed by `fork`/`execvp` or `posix_spawn` supporting argv arrays, `.cwd(...)`, and `.env(...)`.
- [ ] Add `.spawn()` and `.wait_with_output()` returning separate `stdout`, `stderr`, and `exit_code` to `std/process.ll`.
- [ ] Add child `stdin` pipes to `std/process.ll`.
- [ ] Extend both backend runtimes and `std/net.ll` with `getaddrinfo`-based IPv6/dual-stack TCP and DNS resolution.
- [ ] Extend both backend runtimes and `std/net.ll` with UDP sockets.
- [ ] Extend both backend runtimes and `std/net.ll` with nonblocking socket polling.
- [ ] Add keep-alive connection reuse to `Client` in `std/http.ll`.
- [ ] Add connection pooling to `Client` in `std/http.ll`.

## Tooling, LSP & Language Features

- [ ] Support flat brace tuple destructuring (`let +[a, b] = ...`, including mutable, typed, and wildcard elements) in variable declarations.
- [ ] Support nested brace tuple destructuring in variable declarations and `match` patterns.
- [ ] Reconsider `Iterable[T]` and `Iterator[T]` in `std/core/interfaces.ll` and add `IterableIndex[I, T]` / `IteratorIndex[I, T]` with `next: (&self) ?+[I, T]`.
- [ ] Implement native `Iterable` and `IterableIndex` iterators across `std/core` (including `string` yielding `char` and `+[int, char]`).
- [ ] Lower single-variable and two-variable `for` / `while` iteration through native Lowlight iterators across `internal/sema` and `internal/ir`.
- [ ] Support `..` struct and variant field wildcards in patterns across `internal/parser`, `internal/sema`, and `internal/ir`.
- [ ] Support `when pat if cond:` match guards across `internal/parser`, `internal/sema`, and `internal/ir`.
- [ ] Allow omitting `default:` on proven-exhaustive `match` expressions across `internal/sema` and `internal/ir`.
- [ ] Support nested constructor patterns across `internal/parser`, `internal/sema`, and `internal/ir`.
- [ ] Implement `@embed_file`, `embed_bytes`, and `embed_str` intrinsics.
- [ ] Implement compile-time `comptime` evaluation for constant arithmetic, string concatenation, and fixed array lengths (`[size_of[T](): u8]`).

## Compiler Backend & Allocators

- [ ] Unify OS/arch name conventions across C11 and LLVM backends (`internal/codegen`).
- [ ] Switch `internal/codegen/llvmgen.go` (`emitHeader`) and `internal/codegen/llvmgen_runtime.go` from host `runtime.GOOS`/`GOARCH` checks to `Program.TargetTriple`.
- [ ] Update `internal/driver/driver.go` for target-conditional link libraries and `.exe` outputs.
- [ ] Support Win64, RISC-V, and 32-bit layouts plus 64-bit file offsets in `internal/codegen/llvmgen.go` and `internal/codegen/llvmgen_runtime.go`.
- [ ] Add Win32/Winsock shims and compiler attribute guards to `internal/codegen/runtime_h.go` and `internal/codegen/cgen.go`.
- [ ] Emit `internal` linkage for non-exported/standalone functions (except `main` and Phase-1 cross-unit exports) in `internal/codegen/llvmgen.go` so LLVM can dead-strip unused `std/core` code and inline/specialize hot functions.
- [ ] Emit floating-point contraction flags (`fmul contract`, `fadd contract`, `fsub contract` or `@llvm.fmuladd`) in `--release` mode in `internal/codegen/llvmgen.go` to match C11 `-ffp-contract=on` (with a strict-FP opt-out if needed).
- [ ] Optimize `ll_check_bounds` in `internal/codegen/llvmgen_runtime.go` and `internal/codegen/runtime_h.go` to use a single unsigned comparison (`(uint64_t)idx >= (uint64_t)len`), `cold`/`noreturn` attributes on `ll_panic_cstr`, `@llvm.assume(len >= 0)` and `!prof` branch weights in LLVM, and `__builtin_expect` in C11.
- [ ] Add `noalias` return attributes on allocator declarations/helpers (`calloc`, `malloc`, `realloc`, `ll_alloc`), emit `noalias` (LLVM) / `restrict` (C11) on `&T` mutable borrow parameters, and emit `!tbaa` metadata in `internal/codegen/llvmgen.go` and `internal/codegen/cgen.go` to enable loop auto-vectorization.
- [ ] Gate `ll_rt_live_allocs` counter updates behind debug mode or an explicit `--check-leaks` flag in `--release`, and add an uninitialized `alloc_uninit` (`malloc`) runtime path for immediate-init collection constructors in `internal/codegen/runtime_h.go` and `internal/codegen/llvmgen_runtime.go`.
- [ ] Add configurable `--release` optimization flags (`--opt=3` / `-O3`, `--native` / `-mcpu=native`, and ThinLTO for cross-package inlining across two-phase cached `.o` units) in `internal/driver`.
- [ ] Lower direct literal range loops (`a..b`, `a..=b`) to canonical counted loops in `internal/ir/lower_ops.go` instead of the 4-slot `_ll_rcur`/`_ll_rend`/`_ll_rinc`/`_ll_rdone` state machine.
- [ ] Add call-graph reachability dead-function pruning in `internal/ir` and emit optional LLVM runtime subsystems (net/TLS, fs, os, time, print/conversion helpers) on demand in `internal/codegen/llvmgen_runtime.go`.
- [ ] Keep pure SSA temporaries in virtual registers (only `alloca`-ing mutable or address-taken locals) and skip `zeroinitializer` stores on locals proven assigned before read in `internal/codegen/llvmgen.go` and `internal/ir`.
- [ ] Run `mem2reg` and DCE promotion passes so unoptimized builds clean up entry `alloca` temporaries in `internal/codegen/llvmgen.go`.
- [ ] Implement a MIR range-analysis Bounds-Check Elimination (BCE) and loop-invariant bounds-hoisting / loop-versioning pass in `internal/ir` to elide provably safe `ExprIndex` checks while preserving memory safety.
- [ ] Emit `!DICompileUnit`, `!DISubprogram`, `!DILocation`, and `!dbg` metadata in `internal/codegen/llvmgen.go`.
- [ ] Define `Allocator` in `std/core/alloc.ll` (`alloc`, `free`, `realloc`).
- [ ] Allow `List` to accept custom or arena allocators without per-element heap drops.
- [ ] Allow `#[T]` (`Set`) and `#[K: V]` (`Map`) to accept custom or arena allocators without per-element heap drops.
- [ ] Allow `string` to accept custom or arena allocators without per-element heap drops.

## Multithreading & Parallel Computing Overhaul

### Open Concurrency & Parallelism Design Decisions

- [ ] Decide Lowlight's core concurrency model: OS `1:1` threads vs. M:N green fibers/goroutines vs. stackless `async`/`await` (or a layered combination), and whether structured concurrency (`scope` / fork-join) is the primary safe primitive over detached threads.
- [ ] Decide how compile-time data-race freedom (`SPECIFICATION.md` §1) is enforced across threads: opt-in or auto marker interfaces (`Send`/`Sync` equivalents) vs. structural derivation from Plain/Resource rules, and explicit handling of `ptr[T]`, `ref[T]`, `Arena`, thread-unsafe FFI state, and top-level Resource `bind` constants.
- [ ] Decide scoped-borrowing rules without lifetime annotations (`SPECIFICATION.md` §1 axiom 4): how structured concurrency and parallel algorithms can borrow stack read-only (`T`) or disjoint mutable (`&T`) data safely without closure/reference escape.
- [ ] Decide cross-thread shared ownership vs. strict move-only transfer: reconcile the "no reference-counting runtime" axiom (`SPECIFICATION.md` §1) with shared concurrent handles (explicit opt-in `Arc[T]`/`Shared[T]` vs. message passing + scoped sharing only).
- [ ] Decide interior mutability ergonomics under explicit `&` call-site mutation (`SPECIFICATION.md` §1 axiom 6): locking APIs (`Mutex[T]`, `RwLock[T]`), RAII lock guards (`Drop`) vs. closure-based `with_lock(&T)` scopes, and lock poisoning vs. non-poisoning behavior.
- [ ] Decide Lowlight's formal memory model and atomic primitives: supported `Atomic[T]` types, memory ordering guarantees (SC-DRF, relaxed/acquire/release/acqrel/seqcst), memory fences, and `safe` vs. `unsafe` boundaries for concurrent raw `ptr[T]` access.
- [ ] Decide message-passing, synchronization, and thread-local primitives (`Channel` / MPSC vs. MPMC, `select` syntax vs. helper API, `Condvar`, `Barrier`, `Once`/`OnceLock`, and `@thread_local` vs. explicit context passing).
- [ ] Decide parallel computing & vectorization model: thread-pool ownership (global runtime pool vs. explicit `ThreadPool` keeping no hidden runtime), parallel `for`/map/reduce/join APIs, work-stealing scheduling, and explicit portable SIMD types/intrinsics vs. compiler auto-vectorization.
- [ ] Decide cross-thread panic, cancellation, and I/O scaling semantics: unwinding and `Drop` execution on panicking worker threads vs. process abort, cooperative cancellation tokens, and thread-pool vs. async event-loop (`epoll`/`kqueue`/`IOCP`) concurrency for `std/net.ll` and `std/http.ll`.
- [ ] Decide global/runtime thread-safety requirements across both backends (`internal/codegen/runtime_h.go` and `internal/codegen/llvmgen_runtime.go`): POSIX `pthreads` + Win32 threads, atomic/TLS lowering across C11 and LLVM IR, thread-safe panic/trap handling, `Arena` thread confinement, and serialized dynamic `dlopen`/OpenSSL initialization.

### Concurrency Runtime, Compiler & Stdlib Follow-Ups

- [ ] Document the finalized memory model, thread-safety/sendability rules, scoped borrowing, atomics, interior mutability, and panic behavior in `SPECIFICATION.md` and `IMPLEMENTATION.md`.
- [ ] Implement static concurrency safety checks across `internal/types` and `internal/sema` (thread-sendability/shareability inference, closure capture/escape checks, scoped borrow exclusivity, and `Arena`/`ref[T]` cross-thread confinement).
- [ ] Add atomic load, store, RMW, CAS, fence, and thread-local storage (TLS) primitives to `internal/ir` and lower them in both `internal/codegen/cgen.go` (`<stdatomic.h>` / compiler intrinsics) and `internal/codegen/llvmgen.go` (`atomic` / `cmpxchg` / `atomicrmw` / `fence` / `thread_local`).
- [ ] Implement cross-platform runtime threading, mutex, rwlock, condvar, and synchronization primitives in `internal/codegen/runtime_h.go` and `internal/codegen/llvmgen_runtime.go` (POSIX and Win32), and harden runtime globals/`dlopen` initialization for concurrent access.
- [ ] Implement core concurrency and synchronization stdlib modules (`Thread` / scoped spawn, `Atomic*`, `Mutex[T]`, `RwLock[T]`, `Condvar`, `Barrier`, `Once`, and channels) with matching semantics and tests across both C11 and LLVM backends.
- [ ] Implement the decided parallel computing runtime (explicit or managed `ThreadPool`, parallel fork-join / slice / iterator algorithms, and any portable SIMD primitives) and scale `std/net.ll` and `std/http.ll` for concurrent I/O.
- [ ] Add ThreadSanitizer (`--tsan` / `-fsanitize=thread`) and race-detection test support in `internal/driver` alongside deterministic multithreaded stress and compile-fail tests.

## Self-Hosting Compiler & Stage Parity

- [ ] Port `internal/diag` to `compiler/diag.ll` (`std/term.ll`, `Span` struct).
- [ ] Port `internal/token` to `compiler/token.ll` (Enum with explicit tags).
- [ ] Port `internal/lexer` to `compiler/lexer.ll` (`std/chars.ll`, `std/strings.ll`).
- [ ] Port `internal/ast` to `compiler/ast.ll` (Tagged union enums, `Arena` `ref[Node]`).
- [ ] Port `internal/parser` to `compiler/parser.ll` (Lookahead checkpointing).
- [ ] Port `internal/types` to `compiler/types.ll` (`TypeTable` / `Pool[Type]` indexed by `TypeId`).
- [ ] Port `internal/sema` to `compiler/sema.ll` (Move state machine, Tarjan topo sort).
- [ ] Port `internal/ir` to `compiler/ir.ll` (Basic block CFG or linear instruction list).
- [ ] Port `internal/codegen` to `compiler/codegen.ll` (`StringBuilder`, `@embed_file` for runtime header).
- [ ] Port `internal/driver` to `compiler/driver.ll` (`std/process.ll` (`Command` spawn)).
- [ ] Port `internal/lsp` to `compiler/lsp.ll` (JSON-RPC framing, stdio `Reader`).
- [ ] Verify bit-for-bit Stage 2 and Stage 3 binary parity.
