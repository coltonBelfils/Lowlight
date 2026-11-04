# Lowlight Implementation Guide: Architecture, Runtime, & Toolchain

**Implementation Target:** ISO C11 Transpiler & Toolchain  
**Compiler Implementation Language:** Go (Standard Library Only, Zero 3rd-Party Dependencies)  
**Document Status:** Complete Implementation Specification  

---

## 1. Overview & Architectural Philosophy

The canonical implementation of the Lowlight language is an optimizing compiler and developer toolchain written in Go. It compiles Lowlight source files (`.ll`) into single-translation-unit ISO C11 source code, which is then compiled to native machine binaries by any standard C compiler (such as Clang or GCC).

The architectural philosophy of this implementation centers on four strict tenets:

1. **Zero External Dependencies:** The entire Lowlight compiler, standard library, Language Server Protocol (LSP) server, and VS Code extension require zero third-party Go packages and zero NPM dependencies. The toolchain compiles with standard Go 1.20+ and runs everywhere.
2. **Deterministic, Hermetic Compilation:** The compilation process is entirely self-contained. The C code generator bundles its own minimal, header-only runtime (`runtime_h.go`), requiring no external shared libraries or runtime runtime daemons. Dynamic capabilities (such as TLS via OpenSSL) load dynamically at runtime via `dlopen`/`dlsym` without build-time header dependencies.
3. **Strict Memory & UB Verification:** The compiler and its test suite enforce zero undefined behavior and zero memory leaks. Every end-to-end test compiles with Clang's AddressSanitizer (`-fsanitize=address,undefined`) and executes with runtime leak detection enabled (`LOWLIGHT_CHECK_LEAKS=1`), verifying exact 0-leak heap balance upon program termination.
4. **Safety Traps in All Optimization Modes:** Unlike standard C compilers that treat boundary violations as undefined behavior under optimization, the Lowlight C generator emits explicit trap guards that are strictly preserved in both debug (`-O0`) and release (`-O2`) modes. Bounds violations, division by zero, and invalid bit-shifts always trap deterministically.

---

## 2. Repository Layout & Package Architecture

The compiler codebase is structured into cleanly decoupled packages under `internal/`, with entry points in `cmd/` and tooling in `editors/`:

```
Lowlight/
├── cmd/
│   └── lowlight/                  # Main CLI entry point (main.go)
├── internal/
│   ├── diag/                      # Source positions, spans, error reporting, color output
│   ├── token/                     # Token definitions, keywords, operator precedence
│   ├── lexer/                     # UTF-8 tokenizer & Automatic Semicolon Insertion (ASI)
│   ├── ast/                       # Abstract Syntax Tree node definitions
│   ├── parser/                    # Recursive-descent & Pratt expression parser
│   ├── types/                     # Semantic type system definitions & Plain/Resource logic
│   ├── sema/                      # Semantic analysis, type checking, borrow & move checker
│   ├── ir/                        # Mid-Level Intermediate Representation (MIR)
│   ├── codegen/                   # ISO C11 code generator & embedded runtime
│   ├── driver/                    # Orchestration, C compiler discovery, file build runner
│   └── lsp/                       # Built-in Language Server Protocol (LSP 3.17) server
├── std/                           # Standard library source modules (*.ll)
│   ├── core.ll                    # Implicit prelude (Printable, Copyable, Drop, Result, etc.)
│   ├── fs.ll                      # File system operations
│   ├── os.ll                      # OS processes, environment, and paths
│   ├── math.ll                    # Math functions and constants
│   ├── pool.ll                    # Safe generational handle pool
│   ├── strconv.ll                 # Overflow-checked integer and float parsing
│   ├── strfmt.ll                  # String and number formatting utilities
│   ├── time.ll                    # Instant, Duration, and DateTime engine
│   ├── net.ll                     # TCP stream, listener, and socketpair networking
│   └── http.ll                    # Full HTTP/1.1 and HTTPS client/server stack
├── editors/
│   └── vscode/                    # Zero-NPM VS Code / Cursor extension
├── tests/                         # E2E test suites, ASan/UBSan harness, unit tests
└── benchmarks/                    # Benchmark suite (fib, sieve, mandelbrot, matmul)
```

### Compiler Pipeline Flow

```
                      +-----------------------------+
                      |   Lowlight Source (*.ll)    |
                      +-----------------------------+
                                     |
                                     v
                      +-----------------------------+
                      |      internal/lexer         |  Two-pass scanning &
                      |   (Tokenization & ASI)      |  delimiter-stack ASI
                      +-----------------------------+
                                     |
                                     v
                      +-----------------------------+
                      |      internal/parser        |  Pratt parsing &
                      |     (AST Construction)      |  speculative lookahead
                      +-----------------------------+
                                     |
                                     v
                      +-----------------------------+
                      |      internal/sema          |  5-stage header analysis,
                      | (Type Checking & Analysis)  |  monomorphization, borrow
                      +-----------------------------+  & affine move checking
                                     |
                                     v
                      +-----------------------------+
                      |       internal/ir           |  Topological sort, C naming,
                      |     (MIR Lowering)          |  LIFO drop lowering,
                      +-----------------------------+  closure envs & vtables
                                     |
                                     v
                      +-----------------------------+
                      |     internal/codegen        |  10-section ISO C11 TU,
                      |  (C11 Code Generation)      |  embedded runtime header
                      +-----------------------------+
                                     |
                                     v
                      +-----------------------------+
                      |      System C Compiler      |  Clang / GCC
                      |     (Clang / GCC / MSVC)    |  (-O0 debug or -O2 release)
                      +-----------------------------+
                                     |
                                     v
                      +-----------------------------+
                      |    Native Executable / OS   |
                      +-----------------------------+
```

---

## 3. Frontend Subsystems

### 3.1 Diagnostics (`internal/diag`)

Error reporting is centralized in `internal/diag`. The `Reporter` records errors, warnings, and hints. Each diagnostic captures:
- `Span`: Start and End positions (`diag.Pos`), recording byte offset, line number, and column number.
- `Message`: Human-readable error description.
- `Hint`: Optional suggested fix or explanation (e.g., `"prefix the argument with '&'"`).

The reporter prints ANSI-colorized snippets with line numbers, source excerpts, and caret (`^---`) underlines pinpointing the exact offending token span.

### 3.2 Lexer & Automatic Semicolon Insertion (`internal/lexer`)

Lexical analysis operates in two distinct passes:

#### Pass 1: Raw Scanning
- Reads UTF-8 source bytes.
- Identifies single-line comments (`//`) and multi-line comments (`/* ... */`).
- Scans identifiers and applies the `OptIdent` lookahead rule: consumes trailing `?` into the identifier if followed by whitespace, EOF, delimiter, or dot, avoiding collision with `??`.
- Scans numeric literals: decimal, hexadecimal (`0x`), and binary (`0b`).
- Handles string literals and expands escape codes (`\n`, `\r`, `\t`, `\\`, `\'`, `\"`, `\0`, `\xHH`, `\u{HEX}`, `\$`).
- Identifies string interpolation (`$ident`, `$self`, and `${expr}`). Inside `${...}`, the lexer pushes an interpolation delimiter onto its delimiter stack.
- Disambiguates `<-`: lexed as `token.Unwind` if bounded by whitespace or delimiters; otherwise lexed as relational `<` followed by unary `-`.

#### Pass 2: Delimiter-Stack ASI Engine
The lexer maintains a delimiter stack tracking open `{`, `(`, `[`, `#[`, and `${`:
- The variable `blockLevelAfter` is true only when the stack is empty or the top element is `{`.
- On encountering a newline or EOF when `blockLevelAfter` is true, the lexer inspects the previous token. If it is a value-ending token (identifier, literal, `)`, `]`, `}`, `none`, `true`, `false`, `self`, `Self`, `<-`) and the following token is not a continuation token (`else`, `when`, `.`, `?.`), it emits a synthetic `token.AutoSemi`.
- The parser inspects the token kind: explicit `token.Semi` discards block tail values (yielding `void`), whereas `token.AutoSemi` preserves block tail return expressions.

### 3.3 Parser & AST (`internal/ast`, `internal/parser`)

The parser combines top-down recursive descent for declarations and control structures with a Pratt operator-precedence parser for expressions.

#### Speculative Lookahead
The parser resolves grammatical ambiguities using backtracking with temporary diagnostic reporters:
1. **Speculative Generic Invocations:** In expressions like `a < b > (c)`, the parser speculatively checks whether `ident[...]` constitutes a valid generic type argument list or relational operators.
2. **Dangling `else` in `match` Arms:** When an `if` expression without braces occurs inside a `match` arm, an ensuing `else` followed by `:` or `<Pattern>:` belongs to the `match`, not the `if`. The parser speculatively checks for `<Pattern>:` to resolve the ambiguity correctly.
3. **Empty Collection Ambiguity:** The empty token `#[ ]` produces an `ast.EmptyHashLit`. Semantic analysis resolves whether it represents an empty Set (`#[T]`) or an empty Map (`#[K: V]`) based on the expected type context.

---

## 4. Semantic Analysis & Type System (`internal/sema`)

Semantic analysis is performed by `Analyzer` across 5 ordered passes:

### 4.1 The 5 Semantic Passes

1. **Pass 1: Module Graph & File Discovery:** Traverses `import` paths, constructs `Module` descriptors, prevents circular imports, and registers all top-level symbols.
2. **Pass 2: Nominal Type Headers:** Registers names and type structures for all `struct`, `enum`, `interface`, and distinct types across all modules before inspecting function bodies.
3. **Pass 3: Interface Collection & Method Tables:** Collects interface method signatures and inherent type methods. Constructs the global method table (`MethodTable`).
4. **Pass 4: Topological Constant Evaluation:** Constructs a directed dependency graph of all top-level `bind` constants. Performs topological sorting (Tarjan's algorithm) to compute initialization order. Detects and reports cyclic dependency loops with exact cycle paths.
5. **Pass 5: Function Body Analysis & Worklist Monomorphizer:** Recursively type-checks function and method bodies, tracks variable lifecycles, and instantiates generic templates on demand.

### 4.2 Worklist Monomorphization

Generic functions, structs, and enums are represented as AST templates (`GenericFuncTemplate`, `GenericStructTemplate`, `GenericEnumTemplate`).
- When a generic call or instantiation is encountered (e.g., `Result[string, int].ok(...)` or `print_all[int](list)`), the analyzer computes concrete type arguments.
- It queries the cache (`Instantiations`). If not present, it creates a new concrete `FuncInstance` or `types.Struct`/`types.Enum`, assigns a mangled C name, adds it to the worklist, and type-checks the monomorphized body.
- **Recursion Depth Guard:** `const MaxMonomorphDepth = 128`. If nested generic instantiations exceed 128 levels, compilation aborts with a recursion limit error, preventing infinite compile-time expansion.

### 4.3 Move State Machine & Affine Tracking

The `funcChecker` maintains a lifecycle state map for all local variables:
- **`BindOwned`:** Variable owns an active Resource.
- **`BindMoved`:** Variable was moved; subsequent reads, re-borrows, or moves produce compile errors.
- **Branch Merging:** When analyzing `if/else` or `match` blocks:
  - If a variable is moved in *any* branch, it is marked moved after the conditional unless it is reassigned on *all* branches.
- **Loop Move Restrictions:** Moving an outer Resource variable inside a loop body without immediate reassignment is a compile error, as the second iteration would encounter an uninitialized value.

### 4.4 Borrow Exclusivity & Alias Checker

During call-site verification (`checkSignatureArgs`):
- For parameters marked `&T`, the caller must supply `&expr`. The analyzer evaluates `rootSymbolOfExpr(expr)` to find the underlying variable root.
- **Multiple Mutable Borrows:** If the same root symbol appears more than once as a mutable borrow in the same call (`foo(&x, &x)`), the call is rejected.
- **Read/Write Conflict:** If a variable is borrowed mutably (`&x`) while another argument reads or moves `x` (`foo(&x, x)`), the call is rejected unless `x` is a Plain type copied by value before the call executes.

### 4.5 Scoped Arena Pinning & Escape Analysis

- When `arena.alloc(...)` is invoked on an owned local `Arena`, `pinArenaAt(sym, depth)` records the current lexical scope depth in `fc.arenaPinnedDepth`.
- Any call to `.reset()`, reassignment, or move of `sym` while its depth is $\ge$ current scope fails with `"arena is pinned by active allocations"`.
- `arenaDepthOfExpr` checks expressions producing `ref[T]`:
  - If a `ref[T]` is returned via tail expression or `func <-` and its origin depth is deeper than the function outer scope, compilation fails with an escape error unless the function receives a caller-provided `&Arena`.
  - Storing a `ref[T]` into a vector or struct declared in an outer scope is rejected.

---

## 5. Mid-Level Intermediate Representation (`internal/ir`)

Before generating C code, the semantic AST is lowered into a Mid-Level IR (`ir.Program`), simplifying complex Lowlight constructs into linear, structured instructions.

### 5.1 Type Collector & Topological Sort

C requires types to be declared before they are used by value. The `typeCollector` in `internal/ir`:
- Traverses all types used in the program.
- Distinguishes value dependencies (struct $A$ contains struct $B$ inline) from pointer dependencies (struct $A$ contains `?B` or `ptr[B]`).
- Performs a topological sort on value dependencies, guaranteeing that all C structs and unions are defined in valid dependency order.

### 5.2 Local Slots & `NeedsMovedFlag` Optimization

Every local variable and temporary is assigned an `ir.Slot`.
- If an owned Resource variable can be conditionally moved across branches, the IR allocates a boolean flag `_ll_moved_<var>` to track whether the variable requires destruction at scope exit.
- **Optimization:** If static analysis proves a variable is always initialized and never moved, or always moved on all execution paths, the boolean flag is eliminated (`NeedsMovedFlag == false`), reducing C runtime overhead.

### 5.3 Partial Enum Move & Sibling Drops

When lowering `match` patterns that destructure a tagged union with `own` on a specific field (e.g., `when Msg.Data[own payload, id]:`):
1. The IR assigns the target field to `payload`.
2. The IR immediately emits drop calls for all sibling fields that are Resources (e.g., dropping `id` if Resource).
3. The IR marks the parent enum moved so its container destructor will not double-free the payload.

### 5.4 LIFO Unwind Lowering

The 8 unwind forms (`<-`, `func <-`, `for <-`, `next <-`, etc.) are lowered into explicit cleanup blocks:
- The IR walks the active scope stack from the unwind point to the target construct.
- For each scope exited, it emits destructor calls for all active, un-moved Resource variables in strict LIFO order.
- It places the result value (if any) into the target's result slot before emitting destructors, ensuring the yield value is preserved.
- It emits a `goto` to the destination label.

---

## 6. ISO C11 Code Generator (`internal/codegen`)

The code generator (`cEmitter`) emits a single, self-contained `.c` file organized into 10 structured sections:

```
+---------------------------------------------------------------+
| SECTION 1: Standard POSIX / C11 Headers & Compiler Pragmas    |
+---------------------------------------------------------------+
| SECTION 2: Embedded Lowlight C Runtime Header (runtime_h.go)   |
+---------------------------------------------------------------+
| SECTION 3: Forward Declarations of Structs, Unions, Functions |
+---------------------------------------------------------------+
| SECTION 4: Virtual Method Table (VTable) Structures & Globals |
+---------------------------------------------------------------+
| SECTION 5: Concrete Type Definitions (Structs, Enums, Arrays) |
+---------------------------------------------------------------+
| SECTION 6: Synthesized Drop Functions (LIFO Destructors)       |
+---------------------------------------------------------------+
| SECTION 7: Synthesized Deep Copy Functions (.copy())          |
+---------------------------------------------------------------+
| SECTION 8: Synthesized Equality & Hash Functions (==, hash)   |
+---------------------------------------------------------------+
| SECTION 9: Lowered Function & Method Implementations          |
+---------------------------------------------------------------+
| SECTION 10: Global Initializers, Cleanup & C main() Function  |
+---------------------------------------------------------------+
```

### 6.1 Lowlight Type Mapping to C11

| Lowlight Type | Generated C11 Representation |
| :--- | :--- |
| `int`, `i64` | `int64_t` |
| `i8`, `i16`, `i32` | `int8_t`, `int16_t`, `int32_t` |
| `u8`, `u16`, `u32`, `u64` | `uint8_t`, `uint16_t`, `uint32_t`, `uint64_t` |
| `float`, `f64` | `double` |
| `f32` | `float` |
| `bool` | `bool` (`<stdbool.h>`) |
| `char` | `uint32_t` |
| `string` | `ll_str` (`{ const char* data; int64_t len; int64_t cap; }`) |
| `[T]` | `ll_vec` (`{ void* data; int64_t len; int64_t cap; }`) |
| `[N: T]` | `typedef struct { T elems[N]; } ll_arr_*;` (wrapped in struct for value semantics) |
| `#[T]` | `ll_set` (Robin Hood hash table) |
| `#[K: V]` | `ll_map` (Robin Hood hash table with insertion ordering) |
| `?T` (Plain $T$) | `struct { bool has_val; T val; }` |
| `?T` (Boxed $T$) | `T*` (pointer with `NULL` representing `none`) |
| Tagged Enum | `struct { int32_t tag; union { ... } data; }` |
| Interface | `struct { void* obj; const VTable* vtable; bool is_owned; }` |
| Function Type | `struct { Ret (*fn)(void* env, ...); void* env; void (*drop_env)(void*); void* (*copy_env)(void*); }` |
| `Arena` | `ll_arena` (chunked bump pointer list with destructor list) |
| `ref[T]` | `T*` |
| `ptr[T]` | `T*` |

---

## 7. Embedded C11 Runtime (`runtime_h.go`)

The runtime is an inlined C library embedded into every compiled binary.

### 7.1 Memory Allocator & Leak Checker

All allocations route through runtime macros:
- `ll_alloc(size)`: Wraps `malloc`. If `LOWLIGHT_CHECK_LEAKS=1` is enabled at runtime, it increments `ll_rt_live_allocs` and tracks total bytes allocated.
- `ll_free(p)`: Wraps `free` and decrements `ll_rt_live_allocs`.
- `ll_check_leaks_at_exit()`: Invoked at the very end of `main()`. If `LOWLIGHT_CHECK_LEAKS=1` is set and `ll_rt_live_allocs != 0`, it prints an error detailing the leak count and aborts with exit code `99`.

### 7.2 Safety Traps & Boundary Macros

Arithmetic macros prevent undefined behavior:
- `LL_DIV(a, b)`: Traps with panic message if `b == 0`. Handles `INT64_MIN / -1` by wrapping to `INT64_MIN`.
- `LL_MOD(a, b)`: Traps if `b == 0`. Handles `INT64_MIN % -1` by returning `0`.
- `LL_LSHIFT(a, s)` / `LL_RSHIFT(a, s)`: Traps if $s < 0$ or $s \ge 64$.
- `LL_BOUNDS_CHECK(idx, len)`: Traps if `idx < 0 || idx >= len` with `"index out of bounds"`.
- `LL_SLICE_CHECK(lo, hi, len)`: Traps if `lo < 0 || hi < lo || hi > len` with `"slice range out of bounds"`.

### 7.3 Strings & Vectors

- **Zero-Copy Views:** String and vector slices use `cap = 0` as a sentinel indicating a borrowed, non-owning view. The destructor checks `if (v.cap > 0) ll_free(v.data);`, ensuring borrowed views are never freed.
- **Copy-on-Write (CoW) Mutation:** Mutating operations (`push`, `insert`, string append) check if the backing buffer is a view (`cap == 0`). If so, they immediately allocate an independent heap buffer and copy the data before mutating.

### 7.4 Robin Hood Hash Tables

Sets and Maps use an open-addressing Robin Hood hash table with backward-shift deletion:
- Hash functions: FNV-1a for strings; SplitMix64 for integers.
- **Insertion Ordering:** Maps maintain an array of insertion entries, guaranteeing predictable `for (k, v in map)` iteration order matching insertion sequence.
- **Tombstone Compaction:** Deletion shifts subsequent elements backward to eliminate tombstones, preventing lookup degradation across frequent insert/delete cycles.

### 7.5 Chunked Bump Arena

`ll_arena` implements high-performance region allocation:
- Allocates memory in exponential chunks (4KB to 64KB).
- Maintains a pointer to the active chunk offset; allocation is a pointer bump.
- Maintains an intrusive singly-linked list of destructors for allocated Resource objects.
- `arena.reset()` executes all registered destructors in LIFO order and resets chunk offsets without returning memory to the OS, enabling fast buffer reuse.

### 7.6 Dynamic OpenSSL Loader

The runtime provides HTTPS networking without static dependencies:
- On POSIX platforms, `std/http.ll` invokes runtime helpers that call `dlopen()` on `libssl.so`, `libssl.dylib`, or `libcrypto.so`.
- Function pointers (`SSL_CTX_new`, `SSL_connect`, etc.) are resolved via `dlsym()`.
- If OpenSSL is not present on the host system, calls to HTTPS return a runtime error result, but HTTP/1.1 over plain TCP remains fully functional.

---

## 8. Driver, CLI, & Compilation Orchestration (`internal/driver`)

The main executable `lowlight` (`cmd/lowlight`) provides six primary commands:

```bash
lowlight check <file.ll>        # Type-check and verify source without compiling
lowlight emit-c <file.ll>       # Emit generated ISO C11 code to stdout or file
lowlight build <file.ll>        # Compile source to native machine executable
lowlight run <file.ll>          # Compile and immediately run binary
lowlight lsp                    # Start Language Server Protocol (LSP 3.17) over stdio
lowlight version                # Print compiler and language edition info
```

### Compiler Discovery & Invocation
1. Checks the `LOWLIGHT_CC` environment variable.
2. Checks the standard `CC` environment variable.
3. Probes the host system `PATH` for `clang`, then `gcc`.
4. Invokes the C compiler:
   - **Debug mode (default):** `-g -O0`
   - **Release mode (`--release`):** `-O2`
   - Preserves all safety trap guards in both modes.

---

## 9. Standard Library Implementation Architecture (`std/`)

The Lowlight standard library consists of 10 modules implemented in pure Lowlight, layering safe abstractions over C runtime primitives:

| Module | Lines | Description |
| :--- | :---: | :--- |
| `std/core.ll` | 185 | Implicit prelude: `Printable`, `Copyable`, `Drop`, `Result[T, E]`, math utils |
| `std/fs.ll` | 38 | Safe file operations (`read_text`, `write_text`, `exists`, `remove_file`) |
| `std/os.ll` | 82 | Subprocesses (`exec`, `system_run`), directories (`mkdir_p`), system info |
| `std/math.ll` | 64 | Mathematical constants (`PI`, `E`), trig, exp, log, and rounding functions |
| `std/pool.ll` | 115 | 100% safe generational index pool with recycled slots and ABA protection |
| `std/strconv.ll` | 239 | Overflow-checked integer parsing (bases 2..36) and float parsing |
| `std/strfmt.ll` | 473 | Rich formatting: commas, bytes, padding, truncation, snake/camel conversion |
| `std/time.ll` | 768 | `Instant`, `Duration`, `DateTime`, POSIX `strftime` & Go layout formatters |
| `std/net.ll` | 168 | RAII-managed `TcpStream`, `TcpListener`, and in-memory `SocketPair` |
| `std/http.ll` | 1,273 | HTTP/1.1 & HTTPS client/server, `ServeMux`, `ResponseWriter`, test utilities |

---

## 10. Language Server Protocol (LSP 3.17) & IDE Integration

Lowlight includes a high-performance Language Server Protocol 3.17 implementation built directly into the compiler binary (`lowlight lsp --stdio`).

### 10.1 Architecture & Incremental Sync

- **UTF-16 Mapping:** Lowlight source spans operate on byte offsets; the LSP server translates back and forth between byte positions and LSP 0-based UTF-16 code units (`PosToLSP`, `LSPToOffset`), correctly accounting for surrogate pairs (`r > 0xFFFF`).
- **Fault-Tolerant Engine (`CheckForIDE`):** In IDE mode, the analyzer continues through syntax and type errors to analyze subsequent declarations. Panics during analysis of malformed code are caught via `recover()`, ensuring the server never crashes while the user types.

### 10.2 Phantom Sentinel Member Completion (`__ll_complete__`)

When the user types `expr.` or `expr?.`, the buffer contains incomplete syntax. The LSP server:
1. Injects a phantom identifier `__ll_complete__` immediately following the dot.
2. Re-runs `CheckForIDE` on the shadow buffer.
3. Resolves the semantic type of `MemberExpr.Base` (including chained generics, optionals, and UFCS).
4. Emits matching methods, fields, and snippets.
5. If the receiver is optional (`user?`), it automatically supplies `additionalTextEdits` to convert `.` to `?.`.

### 10.3 The 8 Quick-Fix Code Actions

The LSP server provides automatic code-repair actions:
1. Insert `&` at call site when an argument borrows mutably.
2. Remove redundant `&` when an argument borrows read-only.
3. Prefix `own ` or append `.copy()` on consuming parameters.
4. Rename `x` to `x?` when an optional variable lacks `?`.
5. Rename `x?` to `x` when a non-optional variable ends with `?`.
6. Replace `!` with `not `.
7. Insert missing trailing comma in multiline collection or argument lists.
8. Insert `?` for `?.` optional chaining on optional types.

### 10.4 Zero-NPM VS Code Extension (`editors/vscode/`)

The VS Code extension (`editors/vscode/lowlight/extension.js`) is written in pure CommonJS using only the built-in VS Code API and Node's `child_process`:
- Zero `node_modules` and zero build steps required.
- Implements custom framing over the `lowlight lsp` standard I/O stream.
- Includes a TextMate grammar (`syntaxes/lowlight.tmLanguage.json`), code snippets, language configuration, and packaging script (`package_vsix.py`).

---

## 11. Verification, Testing, & Benchmarks

### 11.1 Test Harness & Sanitizers

The test suite in `tests/` executes with Clang sanitizers and exact leak detection:
- `strictCFlags = ["-Wall", "-Wextra", "-Werror", "-fsanitize=address,undefined"]`
- `LOWLIGHT_CHECK_LEAKS=1`: Verifies that `ll_rt_live_allocs == 0` upon process exit.
- `tests/e2e_test.go`: Comprehensive end-to-end showcase tests across both debug and release configurations.
- `tests/language_upgrades_test.go`: Exhaustive positive and negative tests for tagged union destructuring, scoped arena pinning, and release trap preservation.

### 11.2 Benchmark Results

Lowlight's benchmark suite (`benchmarks/`) compares native Lowlight against native C (compiled with Clang `-O2` and `-O3`) and Go 1.25 across four compute workloads:

| Workload | Lowlight (`--release`) | Native C (`-O2`) | Go 1.25 | Lowlight vs. C | Lowlight vs. Go |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **`fib_42`** | **870.4 ms** | 879.7 ms | 955.2 ms | **0.99x** (Faster) | **1.10x** (Faster) |
| **`sieve_10m`** | **79.8 ms** | 54.5 ms | 107.2 ms | 1.46x | **1.34x** (Faster) |
| **`mandelbrot_1500`**| **934.5 ms** | 926.9 ms | 1,022.5 ms | **1.01x** (Equal) | **1.09x** (Faster) |
| **`matmul_400`** | **18.0 ms** | 15.5 ms | 41.6 ms | 1.16x | **2.31x** (Faster) |

*Note: In `sieve_10m` and `matmul_400`, Lowlight actively preserves runtime bounds safety checks in `--release`, resulting in a modest ~1.16x–1.46x difference compared to raw unchecked C pointers while delivering memory safety and substantially outperforming Go.*

---
*End of Lowlight Implementation Guide.*
