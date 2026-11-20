# Lowlight TODO

Tasks are atomic and ordered from easiest to most involved.

## Stdlib Bug Fixes & Targeted Library Additions

- [x] Check `ferror`/`flush`/`close` and support non-seekable files in `std/fs.ll:162-242`.
- [x] Use `str_from_raw` and distinguish clean EOF in `TcpStream.recv` (`std/net.ll:57-73`).
- [x] Normalize negative pre-1970 nanoseconds in `from_unix_millis`/`nanos` (`std/time.ll:284-295`).
- [x] Fix negative formatting in `Duration.to_string` in `std/time.ll`.
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

- [ ] Fix `math.round` returning `1` for `0.49999999999999994` and losing precision at `>= 2^52` by rounding via `trunc` plus remainder instead of `floor(x + 0.5)` (`std/math/math.ll:64`).
- [ ] Fix `math.cbrt` for extreme magnitudes (`cbrt(1e-300)` returns `inf`, `cbrt(1e300)` returns `1e296`) by normalizing the exponent before Newton iteration and handling `inf`/`nan` (`std/math/math.ll:142`).
- [ ] Make `math.hypot` overflow-safe via scaling (`hypot(1e200, 1e200)` returns `inf`) (`std/math/math.ll:157`).
- [ ] Fix large-argument reduction in `sin`/`cos`/`tan` (`sin(1e15)` returns `0.8376` instead of `0.8583`) with Cody–Waite / Payne–Hanek reduction in `_reduce_angle` (`std/math/math.ll`).
- [ ] Honor precisions above 15 in `format_float` / `_format_float_fixed` (`(0.1).format_float(20)` yields only 15 decimals) in `std/core/primitives.ll`.
- [ ] Reject out-of-range float parses (`"1e400".to_float()` returns `inf` as success) or document saturation in `parse_float` (`std/core/primitives.ll:1278`).
- [ ] Decide whether `parse_int`/`parse_float` accept surrounding whitespace (`" 12".to_int()` currently succeeds) and make `to_int`, `to_float`, and `is_int`/`is_float` agree.
- [ ] Trap or return `none` on invalid `char(int)` conversions (surrogates, negatives, `> 0x10FFFF`); `char(0xD800)` and `char(-1)` currently print invalid UTF-8 despite `SPECIFICATION.md` §3.3.
- [ ] Distinguish signal termination in `os.exec` / `os.system_run` (a `SIGKILL`ed child returns `9`, same as `exit(9)`) in `ll_os_pclose`/`ll_os_system` (`internal/codegen/runtime_h.go:1185-1205`, `internal/codegen/llvmgen_runtime.go:1867-1915`) and expose `ExecResult.signal`.
- [ ] Decide whether `string.lines` drops the trailing empty element for newline-terminated text (`"a\nb\n".lines()` returns 3 items) (`std/strings/strings.ll:186`).
- [ ] Decide on Unicode case mapping for `string.to_upper`/`to_lower` (ASCII-only today: `"ÀB".to_lower()` gives `"Àb"`) and implement or document it.
- [ ] Add `send_bytes`/`recv_bytes` (`[u8]`) to `TcpStream` so binary protocols avoid `string` (`std/net/net.ll`).
- [ ] Add `TcpStream.shutdown`, `peer_addr`, `local_addr`, and `set_nodelay` to `std/net/net.ll` with runtime support in both backends.
- [ ] Add `Result` combinators (`map`, `map_err`, `and_then`, `ok() ?T`, `unwrap_or_else`) to `std/core/result.ll`.
- [ ] Add `List` helpers (`extend`, `truncate`, `first`, `last`, `index_of`, `swap`) to `std/core/list.ll`.
- [ ] Add `Set` algebra (`union`, `intersection`, `difference`, `is_subset`) to `std/core/set.ll`.
- [ ] Add `Map` update helpers (`get_or_insert`, `update(key, fn)`, `entries`) to `std/core/map.ll`.
- [ ] Provide borrowed read access to non-`Copyable` values in `Map`/`List` (`m.get(k)` and `m[k]` are unavailable when `V` has a custom `drop`, leaving only `remove`), e.g. a closure-scoped `with(key, ...)` API.
- [ ] Consolidate duplicated std APIs (`strings.pad_left_char` vs `strfmt.pad_left_fill`, `count` vs `count_occurrences`, `capitalize` vs `to_title`, `core_sqrt` vs `math.sqrt`, `os.file_exists` vs `fs.exists`), deprecating one of each with `@deprecated`.
- [ ] Emit the shortest round-trip representation (Ryu/Grisu) from `float.to_string` and string interpolation instead of 6 significant digits (`0.1 + 0.2` prints `0.3`, `123456789.0` prints `1.23457e+08`) in `format_float_prec` (`std/core/primitives.ll:310`).

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
- [ ] Allow the bare `_` discard binding for optional values (`let _ = m.remove(k)` errors with "must end with `?`").
- [ ] Stop emitting `@test_only` externs in non-test builds (a test-only `extern write` in `std/fs` broke the C11 package build of every program importing `std/fs`).
- [ ] Avoid C11 prototype conflicts for user `extern` declarations of libc/POSIX functions missing from `stdCHeaderFuncs` (`internal/codegen/cgen.go:360`); e.g. `extern write: (fd: i32, buf: ptr[u8], n: int) int` fails `-Werror` against `unistd.h`.
- [ ] Flush `stdout` before writing panic messages to `stderr` in both runtimes so output ordering is preserved.
- [ ] Log recovered panics (with stack) from `CheckForIDE` instead of silently swallowing them (`internal/sema/program.go:543-561`), and surface them through LSP `window/logMessage`.
- [x] Fix invalid LLVM IR for optional chaining through a self-referential optional field: with `define User struct [ name: string, boss?: ?User ]`, `u.boss?.name` emits `getelementptr inbounds ptr, ptr %t, i32 0, i32 0`, which clang rejects (C11 works) (`internal/codegen/llvmgen.go`).
- [ ] Add warnings for unused locals, unused imports, unused private functions, and unreachable code after `<-` in `internal/sema` (none are reported today).
- [ ] Detect stack overflow and print a Lowlight panic (guard page plus `sigaltstack` SIGSEGV handler) instead of dying silently with exit code 139, in both runtimes.
- [x] Make codegen output deterministic: the order of function-type/closure helper typedefs and `ll_drop__fn_*`/`ll_copy__fn_*` definitions changes between runs on both backends (`ir.Lower` fed the type collector from the pointer-keyed `BodyMaps.ExprInfos` map; it now visits entries in source-span order). This blocks reproducible builds and Stage 2/3 parity and can perturb `computeObjCacheKey` (`internal/driver/incremental.go:59`).
- [ ] Retire the hardcoded `map.*`/`set.*`/`vec.*`/`string.*` builtin method lowerings (`BuiltinName` in `internal/sema/check_calls.go` plus both backends) in favor of the `@lang` `std/core` implementations so collection semantics live in one place.

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

- [ ] Make `lowlight run` report when the program is killed by a signal (it exits `255` with no message on SIGSEGV) in `internal/driver/driver.go:722`.
- [ ] Fix formatter idempotency: `TestFormatStandardLibraryFilesParseAndSemaAndIdempotent` fails on `std/core/list.ll` with the in-progress `internal/format/printer.go` changes.
- [ ] Bound download and extraction sizes in `FetchBytes` (unbounded `io.ReadAll`) and `extractTarGzSafe`/`extractZipSafe` (decompression bombs) in `internal/manifest/fetch.go`.
- [ ] Make `lowlight doc` skip gitignored and build directories (it documents `editors/jetbrains/build/...` and `editors/vscode/lowlight/std` copies into `docs/`), and decide whether `docs/` is committed or gitignored.
- [ ] Debounce and background `analyzeAndPublish` in the LSP and honor `$/cancelRequest` so completion/hover aren't queued behind full re-checks (`internal/lsp/server.go:241-266,691`).
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
- [ ] Rethink and harden `unsafe` so it enforces stronger correctness and invariants (keeping unrelated borrow/move/type checks active inside `unsafe`, scoping raw-pointer/intrinsic capabilities narrowly, and reducing convenience `unsafe` blocks in `std`) rather than acting as a blanket escape hatch.
- [ ] Consolidate `benchmarks/runner.go` into `benchmarks/runner.ll` (achieving feature/reporting parity in native Lowlight and retiring the Go runner).
- [ ] Port `editors/vscode/package_vsix.py` to a native Lowlight packaging tool once `std/fs` directory traversal and `std/process` are available.
- [ ] Port standalone repository build, release, and editor installation scripts (`editors/*/install.sh` and doc/test orchestration helpers) to native Lowlight for dogfooding.

## Testing, CI & Repository Hygiene

- [ ] Turn `examples/playground.ll` into a buildable example (it has no `main`) or move it out of `examples/`.
- [ ] Move `std/json.ll` into a `std/json/` package directory like the other std packages.
- [ ] Update `SPECIFICATION.md` §3.4: `s.len()` returns the byte count and `s.byte_len()` does not exist; reconcile spec and implementation, including `for (i, ch in s)` byte-offset semantics.
- [x] Add a codegen determinism test (emit LLVM and C twice and compare) (`tests/codegen_regressions_test.go`).
- [ ] Add a differential test that runs every e2e program under both backends and compares stdout.
- [ ] Add Go native fuzz targets (`FuzzLexer`, `FuzzParser`, `FuzzFormat` idempotency, `FuzzCheck`, `FuzzCheckForIDE`) seeded from `std/` and `examples/`.
- [ ] Add a CI workflow running `go vet`, `go test ./...`, and `lowlight test` over `std/` on macOS and Linux for both backends.

## std/json Review Notes

`std/json.ll` is hand-written work in progress. These notes are for its author; don't change the file on their behalf.

- [ ] `_parse_json_null` returns before advancing `idx`, so `null` followed by more input fails (failing test case [62]: `Expected , or ]. Got: n`); it also has a stray `println`.
- [ ] The empty `[]` / `{}` early returns don't advance past the closing bracket (failing case [58]: `Got: ]`).
- [ ] `_parse_json_object` doesn't skip whitespace after a member value before checking for `,` / `}` (failing case [60], pretty-printed objects).
- [ ] `while(char(str[idx]).is_whitespace())` loops lack bounds checks, so truncated input such as `[1,` panics instead of returning `Err`.
- [ ] Escape handling in `_parse_json_string` never fires (it compares the backslash position against a quote), so `\"` ends the string early; `\n`, `\t`, `\\`, and `\uXXXX` aren't decoded.
- [ ] `char(str[idx])` converts one byte at a time, garbling multi-byte UTF-8 in string values.
- [ ] `string.to_json` trims into `in_str` but parses the untrimmed `self`, and doesn't reject trailing input after the top-level value.
- [ ] Number parsing has no exponent support (`1e5`), accepts `-` anywhere (`1-2`), and accepts leading zeros.
- [ ] `Json.to_string` doesn't escape quotes, backslashes, or control characters, and emits `nan`/`inf` for non-finite floats.
- [ ] `float`, `u8`–`u64`, and `i8`–`i32` `to_json` produce `Json.String` instead of `Json.Number`.
- [ ] Single-quoted strings are accepted, which isn't standard JSON.
- [ ] Error messages say "Jason".

## Compiler Backend & Allocators

- [ ] Skip per-element drop loops for Plain element types in `List.clear` and `List.drop` (`std/core/list.ll:249,322`), as `resize` already does.
- [ ] Pre-reserve capacity in `string.repeat`, `string.reverse`, and the padding helpers (`std/strings/strings.ll`).
- [ ] Move key/value out of the `MapEntry` in `Map.push` instead of copying both (`std/core/map.ll:237`), and likewise in `Set.push`.
- [ ] Replace byte-at-a-time FNV-1a `string.hash` (bounds-checked `byte_at` per byte) with a word-at-a-time hash (wyhash/rapidhash), and consider per-process seeding against HashDoS (`std/core/hash.ll:61`).
- [ ] Avoid the full deep-copy snapshot in `List.iter` (`std/core/list.ll:315`), which copies the list and then every element again on each iteration.
- [ ] Lower `std/math` `sqrt`, `floor`, `ceil`, `trunc`, `round`, `fma`, `exp`, `log`, and trig functions to LLVM intrinsics / `<math.h>` (already listed in `stdCHeaderFuncs`) instead of pure-Lowlight Newton/series loops, for speed and correct rounding.
- [ ] Compile Phase-1 package `.o` units and package test binaries in parallel (bounded by CPU count) in `internal/driver/incremental.go` and `internal/driver/driver.go`; both run sequentially today.
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
- [ ] Audit `internal/codegen/runtime_h.go` and `internal/codegen/llvmgen_runtime.go` and move backend-agnostic runtime helpers (that do not strictly require C/OS syscalls) into `std/core/*.ll` so they are implemented once in native Lowlight.

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
