# Lowlight Implementation Guide: Architecture, Runtime, & Toolchain

**Implementation Targets:** Direct LLVM IR Backend (Default) & Portable ISO C11 Codegen  
**Compiler Implementation Language:** Go (Standard Library Only, Zero 3rd-Party Dependencies)  
**Document Status:** Complete Implementation Specification  

---

## 1. Overview & Architectural Philosophy

The canonical implementation of the Lowlight language is an optimizing compiler and developer toolchain written in Go. It compiles Lowlight source files (`.ll`) directly to portable **LLVM IR** (the default high-performance path) or into single-translation-unit **ISO C11** source code (for maximum portability and inspection), which is then assembled into native machine binaries via Clang or any system C compiler (such as GCC).

The architectural philosophy of this implementation centers on four strict tenets:

1. **Zero External Dependencies:** The entire Lowlight compiler, standard library, Language Server Protocol (LSP) server, and VS Code extension require zero third-party Go packages and zero NPM dependencies. The toolchain compiles with standard Go 1.22+ and runs everywhere.
2. **Deterministic, Hermetic Compilation:** The compilation process is entirely self-contained. The compiler directly emits LLVM IR modules with explicit data layouts and target triples, and the C code generator bundles its own minimal, header-only runtime (`runtime_h.go`), requiring no external shared libraries or runtime daemons. Dynamic capabilities (such as TLS via OpenSSL) load dynamically at runtime via `dlopen`/`dlsym` without build-time header dependencies.
3. **Strict Memory & UB Verification:** The compiler and its test suite enforce zero undefined behavior and zero memory leaks. Every end-to-end test compiles with Clang's AddressSanitizer (`-fsanitize=address,undefined`) and executes with runtime leak detection enabled (`LOWLIGHT_CHECK_LEAKS=1`), verifying exact 0-leak heap balance upon program termination across both LLVM and C11 backends.
4. **Safety Traps in All Optimization Modes:** Unlike standard C compilers that treat boundary violations as undefined behavior under optimization, Lowlight emits explicit trap guards that are strictly preserved in both debug (`-O0`) and release (`-O2`) modes. Bounds violations, division by zero, and invalid bit-shifts always trap deterministically.

---

## 2. Repository Layout & Package Architecture

The compiler codebase is structured into cleanly decoupled packages under `internal/`, with entry points in `cmd/` and tooling in `editors/`:

```
Lowlight/
├── cmd/
│   ├── lowlight/                  # Main CLI (main.go, pkg.go, fmt.go, doc.go)
│   └── lowlight-site/             # Website generator (`go run ./cmd/lowlight-site`)
├── internal/
│   ├── diag/                      # Source positions, spans, error reporting, structured codes
│   ├── token/                     # Token definitions, keywords, operator precedence
│   ├── lexer/                     # UTF-8 tokenizer & Automatic Semicolon Insertion (ASI)
│   ├── ast/                       # Abstract Syntax Tree node definitions
│   ├── parser/                    # Recursive-descent & Pratt expression parser
│   ├── types/                     # Semantic type system definitions & Plain/Resource logic
│   ├── manifest/                  # Zero-dep TOML parser/editor, lowlight.toml/lock, SemVer MVS, HTTPS/SHA-256 cache & vendor
│   ├── sema/                      # Directory-as-package loader, type checking, borrow & move checker
│   ├── ir/                        # Mid-Level Intermediate Representation (MIR)
│   ├── codegen/                   # Direct LLVM IR (llvmgen.go), ISO C11 (cgen.go), & two-phase split units (split.go)
│   ├── driver/                    # Manifest-aware orchestration, two-phase `.o` cache (incremental.go), package test runner
│   ├── format/                    # Source formatter (`.lowlightfmt` + `[format]` in `lowlight.toml`)
│   ├── docgen/                    # Doc-comment symbol index, intra-doc link resolution/validation, Markdown output
│   ├── markdown/                  # Dependency-free Markdown renderer (blocks, GFM tables, doc-link hooks)
│   ├── site/                      # Static documentation sites: `lowlight doc` package pages and the Lowlight website
│   └── lsp/                       # Built-in Language Server Protocol (LSP 3.17) server
├── std/                           # Standard library package directories (`std/<pkg>/`)
│   ├── core/                      # Implicit prelude (`core.ll`, `interfaces.ll`, `array.ll`, `list.ll`, `map.ll`, `set.ll`, `range.ll`, `result.ll`, `error.ll`, `hash.ll`, `primitives.ll`)
│   ├── chars/                     # Character classification & UTF-8 helpers
│   ├── strings/                   # Extended string searching, splitting, trimming, and transformations
│   ├── fs/                        # File system operations (`File`, `read_file`, `write_file`, `exists`)
│   ├── os/                        # OS processes, environment, and paths
│   ├── math/                      # Math functions and constants (`PI`, `E`, trig, rounding)
│   ├── pool/                      # Safe generational handle pool (`Pool[T]`, `Handle[T]`)
│   ├── sort/                      # In-place sorting, comparators, binary search
│   ├── strconv/                   # Overflow-checked integer and float parsing/formatting
│   ├── strfmt/                    # String and number formatting utilities
│   ├── time/                      # `Instant`, `Duration`, and `DateTime` engine
│   ├── net/                       # TCP stream, listener, and socketpair networking
│   ├── http/                      # Full HTTP/1.1 and HTTPS client/server stack
│   ├── json/                      # JSON values (hand-written, work in progress)
│   └── test/                      # `@test` assertion & diff helpers
├── editors/
│   ├── vscode/                    # Zero-NPM VS Code / Cursor extension with test controller
│   ├── nvim/                      # Neovim syntax, indentation, LSP attach, neotest adapter
│   └── jetbrains/                 # IntelliJ-platform plugin (native LSP or LSP4IJ, test runner)
├── website/                       # Website content (Markdown) and Lowlight by Example programs
├── tests/                         # E2E test suites, ASan/UBSan harness, package & unit tests
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
                                     +-----------------------+
                                     |                       |
                                     v                       v
                      +-----------------------------+ +-----------------------------+
                      |     internal/codegen        | |     internal/codegen        |
                      |   (Direct LLVM IR Gen)      | |     (ISO C11 Emitter)       |
                      +-----------------------------+ +-----------------------------+
                                     |                               |
                                     v                               v
                      +-----------------------------+ +-----------------------------+
                      |      Clang / LLVM           | |      System C Compiler      |
                      |   (Target machine code)     | |    (Clang / GCC / MSVC)     |
                      +-----------------------------+ +-----------------------------+
                                     |                               |
                                     +---------------+---------------+
                                                     |
                                                     v
                                      +-----------------------------+
                                      |    Native Executable / OS   |
                                      +-----------------------------+
```
```

---

## 3. Frontend Subsystems

### 3.1 Diagnostics (`internal/diag`)

Error reporting is centralized in `internal/diag`. The `Reporter` records errors, warnings, and hints. Each diagnostic captures:
- `Span`: Start and End positions (`diag.Pos`), recording byte offset, line number, and column number.
- `Message`: Human-readable error description.
- `Hint`: Optional suggested fix or explanation (e.g., `"prefix the argument with '&'"`).

The reporter prints ANSI-colorized snippets with line numbers, source excerpts, and caret (`^---`) underlines pinpointing the exact offending token span.

**Focused spans (`internal/sema/diag_focus.go`).** A type mismatch underlines the smallest sub-expression that has the wrong type (the branch of an `if`, the arm of a `match`, the operand of an operator) rather than the whole expression, and a non-exhaustive `match` or result chain underlines only its header up to the colon. The language server reports the same spans.

### 3.2 Lexer & Automatic Semicolon Insertion (`internal/lexer`)

Lexical analysis operates in two distinct passes:

#### Pass 1: Raw Scanning
- Reads UTF-8 source bytes.
- Identifies single-line comments (`//`) and multi-line comments (`/* ... */`).
- Scans identifiers and joins name suffixes: a `?` directly after an identifier becomes part of it (an `OptIdent`) unless it begins `??`; a `!` directly after an identifier becomes part of it unless it begins `!=` (Lowlight has no prefix `!`, so `cfg!.is_ok()` and `a!=b` are both unambiguous).
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

**`@derive`.** `internal/sema/derive.go` turns `@derive(Equatable, Comparable, Hashable, Copyable, Printable)` on a struct or enum into Lowlight source for the methods (`eq`, `cmp`, `hash`, `copy`, `to_string`), parses it, and keeps the declarations in `Module.DerivedDecls` (the cached parsed file is never modified). They are registered with the module's other methods and checked like hand-written code. A non-generic type whose fields can't support the interface gets an error at the attribute. On a generic type the method is instantiated only for instances whose fields support it (`deriveApplies`, called from `instantiateMethod`). Structs and enums have no other source of these methods; tuples and optionals still get them from the compiler when their elements do.


The parser combines top-down recursive descent for declarations and control structures with a Pratt operator-precedence parser for expressions.

#### Speculative Lookahead
The parser resolves grammatical ambiguities with bounded lookahead and, where needed, backtracking with a temporary diagnostic reporter (`SPECIFICATION.md` §16 gives the normative rules):
1. **Square brackets after an expression** (`looksLikeStructLitFields`, `looksLikeGenericTypeArgs`): `Type[field: ...]` or `Type[]` on a capitalized name is a struct literal; a bracket that parses as a list of types followed by `(`, a struct literal, or `.name` is type arguments (`size_of[int]()`, `List[int].new()`); anything else is an index or slice.
2. **Parentheses** (`looksLikeFuncLit`): `(` followed by `)`, `name:` (other than a label before `{`, `for`, or `while`), or a receiver starts a function literal.
3. **Result chains** (`isResultChainStart`, `isChainDefaultAt`): after a postfix expression, `:` starts a chain only when followed by `when`, or by `default` and then `:`, `panic` (not a call), or failure patterns ending in `:`.
4. **Match arms** (`isMatchDefaultAt`): `default` begins the final arm when followed by `:` or by one pattern per subject and `:`; the `match` ends after that arm, so nested matches and chains never absorb an outer `match`'s arms.
5. **Empty collections:** `#[]` produces an `ast.EmptyHashLit`; semantic analysis decides between an empty set and an empty map from the expected type.

---

## 4. Semantic Analysis & Type System (`internal/sema`)

Semantic analysis is performed by `Analyzer`: a package loader followed by ordered declaration passes and body checking.

### 4.1 Semantic Passes & Directory-as-Package Loader

1. **Pass 1: Package Graph, Manifest Discovery, & File Filtering:**
   - **Directory-as-Package:** Inside a `lowlight.toml` project/workspace, `std/<pkg>/`, or directory target, all active `.ll` files in a directory compile into one shared `Package` namespace (`Types`, `Symbols`, `Generic*` maps aliased across sibling `*Module`s), enabling mutual cross-file recursion and package-wide `_` privacy while keeping `import` bindings file-scoped. Standalone `.ll` scripts outside manifests remain isolated single-file packages.
   - **Target & Test File Filtering:** Excludes `*_test.ll` outside `TestMode` and filters OS/arch suffixed files (`_linux.ll`, `_darwin.ll`, `_windows.ll`, `_unix.ll`, `_amd64.ll`, `_arm64.ll`, `<stem>_<os>_<arch>.ll`) against the resolved target triple.
   - **Strict Extensionless Qualified Imports & DAG Cycles:** Resolves extensionless directory imports (`"std/math"`, `"myapp/lexer"`, `"dep/subpkg"`, or relative `"./dir"` for standalone scripts), rejects `.ll` suffixes with migration hints, enforces unique per-file qualifiers (normalizing `-` to `_`), rejects importing binary (`main`) packages, enforces `[dev-dependencies]` isolation in production code, and reports full inter-package import cycle chains (`a -> b -> a`).
   - **Canonical `PackageID` Mangling:** Assigns collision-free sanitized C/LLVM prefixes derived from canonical `PackageID` + deterministic SHA-256 disambiguation so identically named directories (`a/util` vs `b/util`) never collide at link time.
2. **Declaration passes (`types_pass.go`):**
   - *1A, integer constants:* evaluates `bind NAME = <integer constant expression>` so array sizes and explicit enum values can use them;
   - *1B, type headers:* registers every `struct`, `enum`, `result`, `interface`, and distinct type in every package, so declarations are order-independent;
   - *1C, type bodies:* resolves distinct bases, enum variants, result members (`checkResultDecl`: exactly one `value` member and at least one failure), interface signatures, and struct fields;
   - *1D, recursion:* rejects types that contain themselves by value and marks structs reachable from themselves through `?T` as `BoxedOptional`;
   - *1E, signatures:* registers functions, methods (including cross-package extension methods, rejecting duplicate method names with both sites reported), constants, and `extern` declarations, and enforces the `?`/`!` naming rules (`checkOptionalIdentRule`, `checkResultIdentRule`) and `_` privacy.
3. **Constant ordering:** builds the dependency graph of top-level `bind` constants, orders initialization topologically, and reports cycles with the full path.
4. **Body checking and worklist monomorphization:** type-checks every function and method body, tracks ownership (§4.3–4.5), checks exhaustiveness of `match` and result chains, resolves bare variant patterns and bare result constructors, and instantiates generic templates on demand.

### 4.2 Worklist Monomorphization

Generic functions, structs, and enums are represented as AST templates (`GenericFuncTemplate`, `GenericStructTemplate`, `GenericEnumTemplate`).
- When a generic call or instantiation is encountered (e.g., `Result[string].value(...)` or `print_all[int](list)`), the analyzer computes concrete type arguments.
- It queries the cache (`Instantiations`). If not present, it creates a new concrete `FuncInstance` or `types.Struct`/`types.Enum`, assigns a mangled C name, adds it to the worklist, and type-checks the monomorphized body.
- **Recursion Depth Guard:** `const MaxMonomorphDepth = 128`. If nested generic instantiations exceed 128 levels, compilation aborts with a recursion limit error, preventing infinite compile-time expansion.

### 4.3 Move State Machine & Affine Tracking

The `funcChecker` maintains a `MoveState` for every local resource variable:
- **`MoveAlive`:** the variable owns its value.
- **`MoveMoved`:** the value was moved on every path; reads, borrows, and moves are errors until the variable is assigned again.
- **`MoveMaybeMoved`:** the value was moved on some paths; it is treated as unusable, but still destroyed on the paths where it was not moved.
- **Branch Merging:** at the join after `if`/`else`, `match`, and result chains, the incoming states are merged (moved everywhere gives `MoveMoved`, somewhere gives `MoveMaybeMoved`). Paths that end in `never` (an unwind, `panic`, `exit`) are excluded from the merge.
- **Fields:** a field can be moved out only of an `own self` receiver, which consumes `self`.
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

### 5.2 Local Slots, Move Zeroing, & Drop Flags

Every local variable and temporary is an `ir.Local`.
- **Move zeroing:** a move writes the inert value into its source (null data with `cap = 0` for strings and vectors, tag `0` for tagged unions, `has_value = false` for optionals), so the scope-exit drop of a moved or maybe-moved variable is a no-op. No runtime bookkeeping is needed for most types.
- **Drop flags:** a struct with a custom `Drop`, whose zeroed form could still be meaningful, gets a boolean `MovedFlag` local recording whether it was moved.
- **Zero-initialization:** locals start zeroed so drops and moves see an inert value. A plain-typed temporary that is always written before it is read, such as the right-hand side of an assignment, is marked `NoZeroInit` and left uninitialized. When the address of a zeroed temporary reaches a call (a struct returned through a hidden pointer), the C compiler cannot prove the zeroing dead and pays for a `memset` on every loop iteration.

### 5.3 Moving Payloads Out of Enums

When lowering a `match` arm that moves a payload out with `own` (e.g., `when data(own payload):`):
1. The IR moves the active payload into the binding `payload`.
2. It zeroes the subject's tag to `0` so the enum's destructor will not free the payload again.
3. For result chains, the implicit success arm moves the `value` payload out the same way.

### 5.4 LIFO Unwind Lowering

The 8 unwind forms (`<-`, `func <-`, `for <-`, `next <-`, etc.) are lowered into explicit cleanup blocks:
- The IR walks the active scope stack from the unwind point to the target construct.
- For each scope exited, it emits destructor calls for all active, un-moved Resource variables in strict LIFO order.
- It places the result value (if any) into the target's result slot before emitting destructors, ensuring the yield value is preserved.
- It emits a `goto` to the destination label.

---

## 6. Code Generation Subsystems (`internal/codegen`)

Lowlight provides two complete, decoupled code generation backends:

1. **Direct LLVM IR Backend (`llvmgen.go` - Default):** Generates pure, portable LLVM IR (.ll) text with platform datalayouts and target triples. Directly optimized and assembled into machine code via LLVM/Clang.
2. **ISO C11 Code Generator (`cgen.go`):** Emits a single, self-contained `.c` translation unit organized into 10 structured sections with an embedded runtime header.

### 6.1 Direct LLVM IR Backend Architecture (`internal/codegen/llvmgen.go`)

The LLVM emitter constructs SSA LLVM IR directly from lowered MIR:
- **Header:** Emits `target datalayout` and `target triple` configured via CLI `--target <triple>` or detected from host platform.
- **Type Definitions:** Emits LLVM struct types for structs, tagged enums, closures (`cl`, `{ ptr, ptr, ptr, ptr }`), fat interfaces (`{ ptr, ptr, i1 }`), and collections (`{ ptr, i64, i64 }`). An `fn` value is a bare `ptr` to an entry function that takes an unused environment pointer first, so calling it passes `null`, and converting it to a `cl` (`ExprFnToClosure`) stores the pointer with a null environment. In C it is a function-pointer typedef with the same signature as a closure's `fn` field.
- **Alloca Entry Hoisting:** Function entry allocates local storage with alignment and generates SSA registers.
- **Synthesized Intrinsics:** Generates SSA bounds checks, division checks, shift checks, string interpolation, and collection helpers directly in LLVM IR.

### 6.2 ISO C11 Code Generator (`internal/codegen/cgen.go`)

The C11 code generator (`cEmitter`) emits a single, self-contained `.c` file organized into 10 structured sections:

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

### 6.3 Lowlight Type Mapping to LLVM & C11

| Lowlight Type | Generated C11 Representation |
| :--- | :--- |
| `int`, `i64` | `int64_t` |
| `i8`, `i16`, `i32` | `int8_t`, `int16_t`, `int32_t` |
| `u8`, `u16`, `u32`, `u64` | `uint8_t`, `uint16_t`, `uint32_t`, `uint64_t` |
| `float`, `f64` | `double` |
| `f32` | `float` |
| `bool` | `bool` (`<stdbool.h>`) |
| `char` | `uint32_t` |
| `string` | `ll_string` (`{ uint8_t* data; int64_t len; int64_t cap; }`; `cap == 0` is a borrowed view) |
| `[T]` | per-element-type struct `{ T* data; int64_t len; int64_t cap; }` |
| `[N: T]` | `struct { T data[N]; }` (wrapped in a struct for value semantics) |
| `#[T]` | the std/core struct `Set[T]` (`{ buckets, buck_cap, entries, ent_cap, ent_len, len }`) |
| `#[K: V]` | the std/core struct `Map[K, V]` (same fields; entries also hold the value) |
| `?T` (Plain $T$) | `struct { bool has_value; T value; }` |
| `?T` (Boxed $T$) | `T*` (pointer with `NULL` representing `none`) |
| Tag Enum | the declared integer type (default `int64_t`) |
| Tagged Enum / result | `struct { int32_t tag; union { ... } data; }`, tags numbered from 1, tag 0 inert |
| Interface | `struct { void* data; const VTable* vtable; bool owned; }` |
| Function Type | `struct { Ret (*fn)(void* env, ...); void* env; void* (*copy_env)(const void*); void (*drop_env)(void*); }` |
| `Arena` | `ll_arena` (chunked bump pointer list with destructor list) |
| `ref[T]` | `T*` |
| `ptr[T]` | `T*` |

### 6.4 Core Built-in Interfaces & Dynamic Dispatch
The standard library defines universal interfaces in `std/core/interfaces.ll` hooked directly into the compiler via `@lang(...)`:
- **`@lang("indexable") Indexable[T]`**: Exposes `len: fn(self): int` and `get: fn(self, idx: int): ?T`. Implemented inherently by `List[T]` / `[T]`, `Array` / `[N: T]`, `string` (`Indexable[u8]`), and custom sequence types with integer index.
- **`@lang("key_indexable") KeyIndexable[K, V]`**: Exposes `len: fn(self): int`, `has: fn(self, key: K): bool`, and `get: fn(self, key: K): ?V`. Implemented inherently by `Map[K, V]` / `#[K: V]` and custom associative types, avoiding accidental structural collision with positional sequence indexing.
- **Synthesized VTables**: For native runtime structures like `[N: T]`, `string`, and `#[K: V]`, method wrappers for `len` and `get` are synthesized directly in code generation (C11 and LLVM IR) to provide zero-overhead dispatch without requiring runtime indirection or conversions.
- **Direct Indexing**: Indexing expressions `c[idx]` on values of interface type `Indexable[T]` or `KeyIndexable[K, V]` (or generic type parameters bounded by them) lower directly into dynamic dispatch to `.get(...)`, evaluating to `?T` or `?V`. Zero-allocation stack borrowing avoids heap allocation when passing arguments to read-only interface parameters.

---

## 7. Embedded C11 Runtime (`runtime_h.go`)

The runtime is an inlined C library embedded into every compiled binary.

### 7.1 Memory Allocator & Leak Checker

All allocations route through runtime macros:
- `ll_alloc(size)`: Wraps `calloc` (zeroed memory); `ll_alloc_uninit` wraps `malloc`. Every allocation increments `ll_rt_live_allocs`. On allocation failure the runtime flushes standard output, prints `lowlight panic: out of memory (requested N bytes)`, and aborts.
- `ll_free(p)`: Wraps `free` and decrements `ll_rt_live_allocs`.
- `ll_check_leaks_at_exit()`: Invoked at the very end of `main()`. If `LOWLIGHT_CHECK_LEAKS=1` is set and `ll_rt_live_allocs != 0`, it prints an error detailing the leak count and aborts with exit code `99`.

### 7.2 Safety Traps & Boundary Macros

Arithmetic macros prevent undefined behavior:
- `LL_DIV(a, b)`: Traps with panic message if `b == 0`. Handles `INT64_MIN / -1` by wrapping to `INT64_MIN`.
- `LL_MOD(a, b)`: Traps if `b == 0`. Handles `INT64_MIN % -1` by returning `0`.
- `LL_LSHIFT(a, s)` / `LL_RSHIFT(a, s)`: Traps if $s < 0$ or $s \ge 64$.
- `ll_check_bounds(idx, len, file, line)`: Traps if `idx < 0 || idx >= len` with `index I out of bounds for length L`.
- Slice checks trap if `lo < 0 || hi < lo || hi > len` with `vector slice bounds out of range` or `string slice bounds out of range`.

Every trap prints `lowlight panic at FILE:LINE: message` after flushing standard output and exits with status 1; under the test runner it `longjmp`s back to the runner, which records the failure and continues (`SPECIFICATION.md` §19 and Appendix D).

### 7.3 Strings & Vectors

- **Zero-Copy Views:** String and vector slices use `cap = 0` as a sentinel indicating a borrowed, non-owning view. The destructor checks `if (v.cap > 0) ll_free(v.data);`, ensuring borrowed views are never freed.
- **Copy-on-Write (CoW) Mutation:** Mutating operations (`push`, `insert`, string append) check if the backing buffer is a view (`cap == 0`). If so, they immediately allocate an independent heap buffer and copy the data before mutating.
- **String buffer methods in Lowlight:** `push`, `push_byte`, `push_bytes`, `push_char`, `clear`, `reserve`, `cap`, and `string.with_capacity` are ordinary prelude methods in `std/core/primitives.ll`. Like `List`, they view the string as its raw `{ data, len, cap }` layout through `ptr_cast`. Each method's no-growth path is small enough to inline; growth is in separate helpers (`_string_grow`, `_string_push_grow`) that take and return the layout by value, so a local string never has its address taken and the optimizer keeps its fields in registers. The string code the compiler generates calls these methods too: interpolation lowers to `string.push`, `a + b` and `a += b` to `string.add` and `string.add_assign` (through the `Addable` and `AddAssignable` operator interfaces, which sema resolves into `ExprInfo.OpMethod` for any non-numeric type), `s[lo..hi]` to `string._slice` (or `string._slice_view` when the slice is only borrowed; both are `@track_caller`, so a bad range panics at the slice), and copies to `string.copy`. The runtime keeps `ll_str_push` and its helpers only for the `to_string` helpers it synthesizes for tuples and optionals.

### 7.4 Insertion-Ordered Hash Tables

`#[T]` and `#[K: V]` are the `@lang("set")` and `@lang("map")` structs `Set[T]` and `Map[K, V]` from `std/core/set.ll` and `std/core/map.ll`: type syntax resolves to those struct instances (`types.Struct.Lang` is `"set"` or `"map"`), and the compiler generates no hash-table code of its own. Literals lower to `with_capacity` plus `insert`, `m[k]` to `get`, `m[k] = v` to `insert`, `x in s` to `has`, and set and map comprehensions to `insert`. `for` loops over a set or map are lowered inline over the entry array, so the loop variable borrows each element instead of copying it. Checks that only care about element types go through `types.CollView`, which presents the structs as structural `*types.Set` / `*types.Map` descriptors.

Both are open-addressing tables with two arrays:
- **Entries** are appended in insertion order (`{ alive, hash, key[, val] }`), which makes iteration order deterministic: `for(k, v in map)` visits keys in the order they were first inserted.
- **Buckets** hold indexes into the entry array, found by linear probing from `hash & mask`. Deleting marks the entry dead and its bucket as a tombstone (`-2`).
- The bucket array doubles when the load passes 70%; tombstones are compacted when the table rehashes.
- Hashes come from the `Hashable` implementations in `std/core/hash.ll` (SplitMix64 mixing for integers) and user `hash` methods. Hashable types without a `hash` method of their own (tuples, pointers, distinct types) get a built-in `.hash()`: tuples use a synthesized per-type helper, and the others hash their address or base value. Structs and enums hash only through a `hash` method (`@derive(Hashable)`).
- Generic structs that declare `drop` are recorded before any type is instantiated, so a `#[K: V]` written in a struct field or signature (instantiated before methods are registered) is still a resource, and its `drop` is instantiated once methods exist.

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

## 8. Manifest Pipeline, Package CLI, & Two-Phase Incremental Driver (`internal/manifest`, `internal/driver`)

### 8.1 Zero-Dependency TOML, Lockfile, Index, & MVS Pipeline (`internal/manifest`)

All package metadata (`lowlight.toml`, `lowlight.lock`, and decentralized static package indexes) shares one zero-dependency TOML parser, canonical formatter, and comment-preserving surgical mutator (`internal/manifest/toml.go`):
- **Root & Workspace Discovery (`manifest.go`):** Walks upward to locate `lowlight.toml` and any enclosing `[workspace]` root (`members` glob expansion + `exclude`), strictly decoupled from `LOWLIGHT_ROOT` / `StdDir` standard-library lookup.
- **SemVer & Decentralized Static Index (`semver.go`, `lock_index.go`):** Parses SemVer 2.0 versions and comparator constraints (`^`, `~`, `=`, `<`, `<=`, `>`, `>=`, `*`, intersections), fetches remote static TOML indexes (`[versions."<semver>"]` with `yanked`), and selects versions deterministically via **Minimal Version Selection (MVS)**.
- **Safe Fetching, Caching, & Precedence (`fetch.go`, `resolve.go`):** Enforces HTTPS for remote endpoints (allowing loopback HTTP for local testing), verifies exact `sha256:<64-hex>` digests, extracts `.tar.gz`/`.tgz`/`.zip` archives rejecting path traversal (`..`), absolute paths, and symlinks, stores canonical immutable trees in `$LOWLIGHT_CACHE/sha256/<hex>/` (default `~/.lowlight/cache`), and enforces strict resolution precedence: root `[overrides]` -> `[workspace]` members -> `./vendor/` -> `lowlight.lock` + cache. Compiler/LSP invocations never perform implicit network I/O.
- **Target & Format Integration (`target.go`, `internal/format/config.go`):** Matches `--target` triples against `[target.<selector>]` and OS/arch filename suffixes, and loads `[format]` defaults from `lowlight.toml` while preserving `.lowlightfmt` for manifest-free directories and subdirectory overrides.

### 8.2 CLI Commands (`cmd/lowlight`)

Inside a `lowlight.toml` project or `[workspace]`, compilation commands default to the current package or workspace root (`-p <pkg>`, `--workspace`, `--locked`, `--offline`), while standalone single-file invocation remains zero-config:

```bash
lowlight build     [-o <bin>] [-p <pkg>|--workspace] [--locked] [--offline] [<path>]
lowlight run       [-p <pkg>] [--locked] [--offline] [<path>] [-- <args...>]
lowlight test      [-p <pkg>|--workspace] [--locked] [--offline] [-run <regex>] [<path...>]
lowlight check     [-p <pkg>|--workspace] [--locked] [--offline] [<path>]
lowlight emit-llvm [-o <file.ll>] [-p <pkg>] [<path>]
lowlight emit-c    [-o <file.c>]  [-p <pkg>] [<path>]
lowlight init      [--lib|--bin] [--name <name>] [<dir>]
lowlight new       <dir> [--lib|--bin] [--name <name>]
lowlight add       <name> (--path <dir> | --url <url> [--hash <sha256>] | --index <url> --version <c>) [--dev] [--override]
lowlight remove    <name> [--dev] [--override]
lowlight fetch     [--locked] [--offline]
lowlight update    [<pkg>...] [--offline]
lowlight vendor    [--offline]
lowlight tree      [--locked] [--offline]
lowlight fmt       [-w] [--check] [<path...|lowlight.toml>]
lowlight doc       [-o <dir>] [--format html|md] [--check-links] [<path...>]
lowlight lsp       [--stdio] [--log <file>]
lowlight version
```

### 8.3 Two-Phase Package Incremental Compilation & `.o` Caching (`internal/codegen/split.go`, `internal/driver/incremental.go`)

For multi-package builds on both the LLVM IR and ISO C11 backends, the driver partitions lowered MIR into two phases:
1. **Phase 1 — Per-Package Concrete Units (`UnitPackage`):** Each non-entry library package's concrete non-generic functions emit into a hermetic filtered translation unit containing only types and external declarations reachable from that package (isolated from downstream `main` edits). Units are keyed by SHA-256 over `(backend, cc, targetTriple, release, compileFlags, PackageID, unitSource)`, compiled once via `clang -c` / `cc -c`, and atomically cached under `<root>/.lowlight/cache/obj/` (or `$LOWLIGHT_CACHE/obj/`). Unchanged dependencies hit the `.o` cache directly without re-invoking the backend compiler.
2. **Phase 2 — Shared Specialization & Link Unit (`UnitLinkMain`):** Monomorphized generic instantiations, lambdas, static closure thunks, structural interface vtables, unified runtime state globals (`ll_rt_live_allocs`, `ll_rt_argc/argv`, test panic buffers, TLS dispatch), top-level globals, and entry/`main`/test-runner code emit into a single link unit compiled and linked alongside all cached Phase 1 `.o` objects.

### 8.4 Compiler Discovery, Cross-Targeting, & Flags
1. **Manifest `[build]` & `[target.<selector>]`:** Applies default `backend`, `release`, `target`, `cc`, `c_flags`, and `c_libs` from `lowlight.toml` (and transitive dependencies), with explicit CLI flags taking precedence.
2. **Target Triple (`--target <triple>`):** Passes the target triple to OS/arch suffix filtering, `[target.<selector>]` matching, LLVM emission (`target triple = "..."`), and `clang`/`cc` (`--target=...`).
3. **Backend Selection (`--backend <llvm|c11>`):** LLVM IR is the default backend; ISO C11 can be selected for portable C compilation.
4. Checks `LOWLIGHT_CC`, then `CC`, then probes `PATH` for `clang` / `cc`.
5. Invokes the compiler in Debug (`-g -O0`) or Release (`-O2`) mode while preserving deterministic safety traps in both modes.
6. Compiles generated C with `-fno-strict-aliasing`: `ptr_cast` lets Lowlight code view memory as another type (`List` and `string` rely on it), which C's type-based aliasing rules would otherwise let the optimizer assume never happens. The LLVM backend emits no type-based alias metadata, so it needs no flag.

---

## 9. Standard Library Implementation Architecture (`std/`)

The Lowlight standard library is organized as 16 directory-as-package modules under `std/<pkg>/` implemented in pure Lowlight, imported via extensionless qualified paths (`import "std/<pkg>"`):

| Package | Description |
| :--- | :--- |
| `std/core/` | Implicit prelude: `Printable`, `Copyable`, `Drop`, `Equatable`, `Comparable`, `Hashable`, `Error`, `Result[T]`, `List[T]`, `Array[T]`, `Set[T]`, `Map[K, V]`, `Range[T]` |
| `std/chars/` | Character classification, ASCII case folding, radix digits, and UTF-8 lengths |
| `std/strings/` | Extended string searching, splitting, trimming, replacement, and transformations |
| `std/fs/` | Safe file operations (`File`, `read_file`, `write_file`, `exists`, `copy_file`, `remove_file`) |
| `std/os/` | Subprocesses (`exec`, `system_run`), directories (`mkdir_p`, `remove_all`), env, system info |
| `std/math/` | Mathematical constants (`PI`, `E`, `INF`, `NAN`), trig, exp, log, clamp, and rounding |
| `std/pool/` | Generational index pool (`Pool[T]`, `Handle[T]`) with recycled slots and ABA protection |
| `std/sort/` | In-place sorting, custom comparators, binary search, and `Set`/`Map` sorted helpers |
| `std/strconv/` | Overflow-checked integer parsing/formatting (bases 2..36), floats, and booleans |
| `std/strfmt/` | Rich formatting: commas, bytes, currency, padding, truncation, snake/camel/kebab case |
| `std/time/` | `Instant`, `Duration`, `DateTime`, POSIX `strftime` & Go layout formatters |
| `std/net/` | RAII-managed `TcpStream`, `TcpListener`, and in-memory `SocketPair` |
| `std/http/` | HTTP/1.1 & HTTPS client/server, `ServeMux`, `ResponseWriter`, URL parser, test utilities |
| `std/test/` | `@test` assertions (`assert_eq`, `assert_is_ok`, `assert_approx_eq`) with caller-tracked diffs |
| `std/json/` | JSON values and conversion (hand-written work in progress) |
| `std/unicode/` | Full Unicode case mapping, Final_Sigma, and case folding; `tables.ll` is generated by `gen_tables.py` from Python's Unicode database |

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

### 10.3 Code Actions

Diagnostics carry structured data that code actions turn into edits (`code_actions.go`, `quickfix_data.go`, `codegen_more.go`). Every generated edit is formatted exactly as `lowlight fmt` would format it.
- **Quick fixes:** insert `&` or `own` at a call site, or `.copy()`; remove a redundant `&`; rename a variable to add or remove its `?` or `!` suffix everywhere it is used; replace `!` with `not `; insert a missing trailing comma; use `?.` on an optional; add missing `match` arms or result-chain failures; add missing struct fields; implement missing interface methods; turn a top-level `let` into `bind`; wrap an operation in `unsafe`; add `@must_use`; add a missing import.
- **Generators:** `Implement <Iface> for <Type>`, a `<Type>.new` constructor, a doc comment skeleton, and `@test` scaffolds for functions and methods.
- **Refactorings:** extract variable, extract function, toggle a `{ ... }` single-expression body, flip `if`/`else`, add an explicit `: Type`.

### 10.4 Editor Integrations (`editors/`)

All editors talk to the same server, and all run tests through `lowlight test --json` (`internal/driver/test_json.go`), which streams one JSON event per test (discovery, start, pass, fail with message and location).
- **Neovim (`editors/nvim/`):** syntax, formatter-matching `indentexpr`, an `ftplugin` that attaches the server in any configuration, and a neotest adapter.
- **JetBrains (`editors/jetbrains/`):** a Kotlin plugin using the platform's native LSP client when available and LSP4IJ otherwise, with run configurations, gutter run markers, and a test runner bridge that shows failures at their source location.

#### VS Code (`editors/vscode/`)

The VS Code extension (`editors/vscode/lowlight/extension.js`) is written in pure CommonJS using only the built-in VS Code API and Node's `child_process`:
- Zero `node_modules` and zero build steps required.
- Implements custom framing over the `lowlight lsp` standard I/O stream.
- Includes a TextMate grammar (`syntaxes/lowlight.tmLanguage.json`), code snippets, language configuration, a Testing-panel controller (`test_controller.js`), and packaging script (`package_vsix.py`).

---

## 11. Documentation Toolchain (`internal/docgen`, `internal/markdown`, `internal/site`)

Documentation has two products built on one generator:

- **`lowlight doc`** documents any Lowlight project: one HTML page per package (overview, index, constants, functions, types with fields, variants, and methods, and methods added to other packages' types), highlighted source pages with line anchors, and client-side search.
- **The Lowlight website** (`go run ./cmd/lowlight-site -o docs`) adds the tour, tutorials, guides, Lowlight by Example, the specification split into one page per chapter, the style guide, this implementation guide, version history, and the standard library reference.

The pieces:

1. **`internal/docgen`** parses and tolerantly type-checks sources (`docgen.Load`), builds the `SymbolIndex` of every documented declaration, resolves ``[`Symbol`]`` links the way names resolve in code, and validates them (`--check-links`). The language server uses the same index for hover documentation. `--format md` output is also written here.
2. **`internal/markdown`** is a dependency-free renderer: ATX headings with GitHub-style ids, paragraphs, nested ordered and unordered lists (tight and loose), block quotes with callout classes, fenced and indented code, GFM tables (with escaped pipes inside code), raw HTML blocks, front matter, and inline code, links, images, emphasis, and strikethrough. Hooks highlight code, rewrite link destinations, and resolve symbol references.
3. **`internal/site`** assembles pages and writes the site:
   - `site.go`: the page model; a two-pass build that first records every document's heading anchors and then renders with links rewritten, so `#61-bodies` in the specification resolves to the page that now holds that chapter; prev/next links; and a post-build link checker that verifies every relative `href` and fragment.
   - `highlight.go`: syntax highlighting using the real Lowlight lexer, plus TOML, shell, and EBNF.
   - `api.go`, `apidocs.go`: package pages from the symbol index, shared by `lowlight doc` and the website.
   - `examples.go`, `run_examples.go`: Lowlight by Example pages from annotated `.ll` files and their `.out` outputs.
   - `website.go`: the website's sections and sources.
   - `theme.go`, `search.go`, `assets/`: the page shell, header navigation, sidebars, on-page table of contents, light and dark themes, copy buttons, and keyboard-driven search over a generated index. Output is static and works from `file://`.

`tests/website_test.go` compiles and runs every complete program in the website's Markdown (comparing against its `output` block), runs every example against its `.out` file, and builds the site with link checking.

---

## 12. Verification, Testing, & Benchmarks

### 12.1 Test Harness & Sanitizers

The test suite in `tests/` executes with Clang sanitizers and exact leak detection:
- `strictCFlags = ["-Wall", "-Wextra", "-Werror", "-fsanitize=address,undefined"]`
- `LOWLIGHT_CHECK_LEAKS=1`: Verifies that `ll_rt_live_allocs == 0` upon process exit.
- `tests/e2e_test.go`: Comprehensive end-to-end showcase tests across both debug and release configurations.
- `tests/language_upgrades_test.go`: Exhaustive positive and negative tests for tagged union destructuring, scoped arena pinning, and release trap preservation.
- `tests/website_test.go`: every runnable example in the website and Lowlight by Example, plus a full site build with link checking.
- Tests run with `t.Parallel()`; the full `go test ./...` takes about a minute.

### 12.2 Benchmark Results

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
