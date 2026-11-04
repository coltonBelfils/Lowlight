# Lowlight (Lightweight) Language Project — Complete Summary & Onboarding Guide

> [!IMPORTANT]
> **Purpose of This Document**
> This folder is a complete, self-contained export of the **Lowlight** (also referred to as **Lightweight**) programming language project. Any developer or AI coding assistant (such as Google Antigravity) opening this folder on a new machine has **100% of the context, design history, rationale, rejected alternatives, resolved rules, and open questions** needed to seamlessly continue the project.
>
> **Golden Rule Established by the User**:
> *"Run all design decisions past me. I know there will be many. Don't start on implementation until everything is ironed out."*
> Do **not** begin writing compiler/transpiler code until the user explicitly confirms that the specification and critique phases are complete.

---

## 1. Quick Navigation & Folder Contents

> [!NOTE]
> **Branch Status (`auto-v1-prototype` vs. `main`)**
> * **`auto-v1-prototype` (Current Branch)**: All open design questions from `SPEC_CRITIQUE.md` have been resolved in [`DECISIONS.md`](./DECISIONS.md), formalized in [`SPECIFICATION.md`](./SPECIFICATION.md) (**v1.0**), and implemented end-to-end in the working Go reference compiler (`cmd/lowlight/`, `internal/`, `std/`, `examples/`, `tests/`). See [`README.md`](./README.md) to build and run Lowlight programs.
> * **`main` Branch**: Preserved untouched at Draft v0.5 / Critique v0.7 (`f82b473`) so every open design question can later be revisited and decided interactively from scratch.

* **[`./README.md`](./README.md)** — **Quickstart, CLI Reference, Language Tour & Compiler Architecture**.
* **[`./SPECIFICATION.md`](./SPECIFICATION.md)** — **Finalized Language Specification (v1.0)**.
* **[`./DECISIONS.md`](./DECISIONS.md)** — **Complete v1.0 Design Resolution Log** covering every item from `SPEC_CRITIQUE.md` (Parts 0, A, M, B, C, D, E, N) and implementation refinements.
* **[`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md)** — **Historical Critique & Audit Document (v0.7)** that drove the v1.0 resolutions.
* **[`./VERSION_HISTORY.md`](./VERSION_HISTORY.md)** — **Complete Changelog & Diff Guide** across every version of both the specification and the critique document.
* **[`./version_history/`](./version_history/)** — **Verbatim Historical Archive**:
  * `specification/v0.0_initial_seed_sample.md` — The user's original seed code sample (Oct 16, 2026).
  * `specification/v0.1_LANGUAGE_SPECIFICATION.md` & `v0.1_SPECIFICATION.md` — Draft v0.1 of the specification (Oct 17, 2026).
  * `specification/v0.2_SPECIFICATION.md` — Draft v0.2 of the specification (Oct 21, 2026).
  * `critique/v0.1_spec_critique.md` — Initial critique of Draft v0.1 (Oct 17, 2026).
  * `critique/round1_user_comments.md` — Verbatim 21 inline user comments on Critique v0.1 (Oct 20, 2026).
  * `critique/v0.2_spec_critique.md` — Critique v0.2 updated with Round 1 feedback (Oct 20, 2026).
  * `critique/round2_user_comments.md` — Verbatim 7 inline user comments on Critique v0.2 (Oct 21, 2026).
  * `critique/v0.3_initial_spec_critique.md` — Critique v0.3 updated with Round 2 feedback (Oct 21, 2026).
  * `critique/round3_user_comments.md` — Verbatim inline user comment on Critique v0.3 (Oct 21, 2026).
  * `critique/v0.3_final_spec_critique.md` — Critique v0.3 Final after Round 3 & Spec v0.2 sync (Oct 21, 2026).

---

## 2. Project Origin & Core Vision

Lowlight is a custom low-level systems programming language designed to **transpile directly to human-readable, high-performance C (C99/C11)** while providing modern ergonomics, expression-oriented control flow, and strict compile-time safety.

### Core Design Pillars
1. **Transpiles to C (C99/C11)**:
   * Prior to starting the language syntax design (in an Oct 15, 2026 exploration), transpiling to C was chosen over LLVM IR, Cranelift, bytecode VMs, and WebAssembly because it provides world-class `gcc`/`clang` optimizations, instant portability, trivial C FFI, and human-inspectable output.
2. **Deterministic Memory Safety Without Garbage Collection**:
   * Single ownership with move semantics for heap-allocated types (`string`, vectors `[T]`, maps `#[K: V]`, sets `#[T]`, and structs containing them).
   * Automatic, deterministic scope-based deallocation (`free_*` calls emitted by the compiler at scope exit and before any jump/unwind).
   * Compile-time borrow checking with read-only borrows (`(x: T)` by default) and mutable borrows (`(x: &T)`).
3. **Call-Site Mutation Transparency (`&`) & Ownership Transfer (`own`)**:
   * Mutating a value through a function argument requires `&` both in the function parameter and at the call site (`push(&my_list, 4)`), so a reader can see every state mutation without looking up function definitions.
   * Transferring ownership of a named Resource variable requires `own` at the call site (`consume(own my_resource)`), making caller variable invalidation completely visible.
   * UFCS method calls (`pt.translate(5, 5)`) auto-borrow the receiver (`pt`) according to the method's `self` parameter (`(self)` or `(&self)`), verifying `pt` was declared with `let` if `self` is `(&self)`.
4. **Expression-Oriented Blocks & Targeted Unwinding (`<-`)**:
   * Blocks `{ ... }`, `if`/`else`, `for`, and `match` are expressions.
   * Statements (`let`, `bind`, `define`, `=`, `<-`) are strictly distinct from expressions.
   * A unified unwind operator (`target <- value` or bare `target <-`) exits any enclosing block (`<-`), conditional (`if <-`), loop iteration (`next <-`), loop (`for <-`), labeled loop (`label <-`), or function (`func <-` / `func_name <-`).
5. **Strict Non-Value & Null Safety**:
   * No `null` pointers and no general `unit` value assignable to variables (`let x = { let a = 1; let b = 2 }` is a compile error).
   * Optional values have type `?T`, and **every variable, parameter, or field holding an optional must end with a `?` suffix (`user?`)**, visually guaranteeing that any identifier without `?` (`user`) holds a valid, unwrapped value.
6. **Strict Delimiter Separation (`{}` vs. `[]`)**:
   * Curly braces `{ ... }` are exclusively for executable block expressions.
   * Square brackets `[ ... ]` are for data literals (vectors, stack arrays, sets, maps, struct literals) and type definitions (`define ... struct [...]`, `enum [...]`, `interface [...]`).

---

## 3. Chronological Design History: How Every Feature Was Decided

### Phase 0: Pre-Project Exploration & Abandoned Prototype (Oct 15–16, 2026)
1. **C Transpilation Exploration (Oct 15, 2026)**: Evaluated compiler backends; selected transpiling to C.
2. **Initial "Lightweight Language" Workspace (Oct 16, 2026, 15:07 PDT)**: The user pasted the initial seed syntax sample without the instruction to hold off on implementation. The assistant prematurely wrote a Python lexer/parser prototype. The user abandoned that workspace and started the **Lowlight** workspace (`/Users/coltonbelfils/Documents/Lowlight`) with the strict instruction to **iron out all design decisions before any implementation**.

---

### Phase 1: Memory Model, Stack Arrays, Unwinding, Optionals & Primitives (Oct 16, 2026)
Starting from [`v0.0_initial_seed_sample.md`](./version_history/specification/v0.0_initial_seed_sample.md), six foundational questions were resolved interactively:

1. **Memory Management**:
   * *Options considered*: Manual `free`/`defer` (Zig-style) vs. Reference Counting (ARC) vs. Scope-based Ownership/RAII (Rust/C++ style).
   * *Decision*: **Single ownership + deterministic scope-based drop + move semantics + borrowing**.
   * *Borrow Syntax Evolution*: Initially explored `&T` / `&-T` during Phase 1. In Round 6 (v0.6/v0.7) and the v1.0 specification, the syntax was streamlined: parameter defaults are read-only borrows (`(x: T)`), mutable borrows require `(x: &T)` and explicit call-site `&` (`push(&my_list, 4)`), and ownership moves require `(x: own T)` and explicit call-site `own` (`consume(own item)`).

2. **Fixed Arrays vs. Dynamic Lists**:
   * *Problem*: The seed sample had `[3][1, 2, 3]`, which looks like indexing a 2D array.
   * *Rejected alternatives*: `[3][1, 2, 3]` and `stack[1, 2, 3]`.
   * *Decision*: Use a colon inside brackets to separate the compile-time size from the elements:
     * `[1, 2, 3]` $\rightarrow$ Dynamic heap list of type `[int]` (`List[int]`).
     * `[3: 1, 2, 3]` $\rightarrow$ Fixed-size stack array of 3 elements (`[3: int]` / `Array`).
     * `[100: 0]` $\rightarrow$ Fixed-size stack array of 100 elements initialized to `0` (`[100: int]` / `Array`).
     * *(Note: `Array` is the name of the fixed array type; `List` is the name of the dynamic list type. `Vector` is not an alias to either and is removed.)*

3. **Block Unwinding (`<-`) & Function Returns**:
   * *Initial exploration*: Considered `<-` (1 block) + `<--` (function return), plus whether every path must return a value.
   * *Decision*:
     * Locked in **Exhaustive Flow Check**: Every execution path in a value-producing block or function must yield a compatible value (missing return paths are a compile-time error).
     * Later in Phase 3 (when `<--` conflicted between loop break and function return), the user invented **Targeted Unwinding (`target <- value`)**:
       * `<- value` unwinds the immediate `{}` block.
       * `for <- value` breaks out of the nearest enclosing `for` loop.
       * `func_name <- value` (and later `func <- value`) returns from the enclosing function.
       * `label <- value` breaks out of a labeled loop (`outer: for (...)`).

4. **Non-Value Safety & Optionals (`?T` and `name?`)**:
   * *Problem*: What happens if a block has no final expression (`let x = { let a = 1; let b = 2 }`), or an `if` has no `else`?
   * *Rejected alternative*: Rust-style `unit` (`()`) type where any block can silently evaluate to `()`.
   * *Decision*:
     * Blocks that do not yield a value cannot be assigned to variables (`let x = { let a = 1; let b = 2 }` is a compile error).
     * An `if (cond) val` without an `else` evaluates to an **Optional (`?T`)**, defaulting to `none` when `cond` is false.
     * **Mandatory `?` Suffix on Optional Identifiers**: Any variable holding an optional value must have `?` at the end of its name (`let user? = find_user(id)`).
     * **Unwrapping via `if (user?)` and `??`**: Inside `if (user?) { ... }`, the unwrapped value is accessed via the non-optional identifier `user` (no `?`). The `??` operator provides a fallback (`let n = count? ?? 0`).

5. **Primitives & Type Inference**:
   * *Decision*:
     * Default integer is `int` (`i64`); explicit fixed-width types are `i8`, `i16`, `i32`, `i64`, `u8`, `u16`, `u32`, `u64`.
     * Default float is `float` (`f64`); explicit 32-bit float is `f32`.
     * `bool` (`true`/`false`) and `char` (32-bit Unicode scalar).

6. **`let` vs. `bind` for Functions**:
   * *Decision*:
     * `bind add = (a int, b int): int { a + b }` declares an immutable, static C function (zero indirection).
     * `let op = (a int, b int): int { a + b }` declares a mutable function pointer variable that can be reassigned at runtime to another compatible function or lambda.

---

### Phase 2: Structs, Methods, Immutability & Maps/Sets (Oct 16, 2026)

1. **Field Access & Method Syntax**:
   * *Options considered*: Free functions only with UFCS vs. Go-style receiver `bind (p &Point) length = () ...` vs. Namespaced method `bind Point.length = (self &Point) ...`.
   * *Decision*: **Namespaced Method Syntax (`bind Point.length = (self &Point): float { ... }`)** with **UFCS (Uniform Function Call Syntax)** so `pt.length()` and `Point.length(&pt)` are interchangeable.

2. **`let` vs. `bind` Immutability Depth**:
   * *Initial Decision in Phase 2*: `bind` structs are **deeply immutable** (cannot mutate fields of a `bind` struct), while `bind` Maps/Sets/Lists were initially marked as shallowly immutable (cannot reassign the variable, but can call `.insert()`/`.push()`). *(Note: In Critique Part 2 §3, there is an open suggestion to make `bind` deeply immutable for collections too; awaiting user review.)*

3. **Built-in Hash Maps & Hash Sets (`#[...]`)**:
   * *Decision*:
     * **Hash Set (`#[T]`)**: `let s = #[1, 2, 3]` with `.has(v)`, `.insert(v)`, `.remove(v)`, `.len()`, `.clear()`, `.to_array()`.
     * **Hash Map (`#[K: V]`)**: `let m = #["Alice": 30]` with `m["Alice"]` returning `?V`, `m["Bob"] = 25`, `.has(k)`, `.remove(k)`, `.len()`, `.clear()`, `.keys()`, `.values()`.

4. **Trailing Comma Rule**:
   * *Decision*: Multi-line `[...]` definitions and literals **require** a trailing comma on the final item; single-line `[...]` definitions allow omitting it.

---

### Phase 3: Unified `for` Loops as Expressions (Oct 16, 2026)

* *Options considered*: Separate `for` and `while` keywords vs. a single unified `for` keyword (Go-style) that also acts as an expression.
* *Decision*: **Single unified `for` keyword**:
  * Range: `for (i in 0..10) { ... }`
  * Collection: `for (item in my_list) { ... }` and `for (k, v in my_map) { ... }`
  * Conditional (while-style): `for (count < 10) { ... }`
  * Infinite: `for (true) { ... }`
  * **Loop as Expression**:
    * `let x = for (...) { if (...) for <- val } else { default_val }` has type `T`.
    * `let x? = for (...) { if (...) for <- val }` (without `else`) has type `?T` (evaluates to `none` if the loop finishes without hitting `for <-`).
  * *(Note: In Critique Round 2, the user requested a deeper rethink of `for` loops so that **each iteration** is also treated as an expression alongside the overall loop expression; see Open Question F.1 below.)*

---

### Phase 4 & 5: Strings & Interfaces (Oct 17, 2026)

1. **String Model & Interpolation**:
   * *Decision*:
     * `string`: Owned, growable heap-allocated UTF-8 string (`{ char* data; int64_t len; int64_t capacity; }`).
     * `string` parameter mode: Borrowed read-only string view/slice (`{ const char* data; int64_t len; }`) — zero-copy.
     * `&string`: Borrowed mutable string reference, allowing in-place `.push(...)` and `.push_bytes(...)` mutation without moving ownership.
     * String interpolation uses `$ident` for simple non-optional variables and `${expr}` for arbitrary expressions.

2. **Interfaces (Implicit / Structural Polymorphism)**:
   * *Options considered*: Explicit `implements` keyword vs. Go-style implicit structural satisfaction.
   * *Decision*: **Implicit structural interfaces** (`define Printable interface [ to_string: (): string, ]`). Any type—including built-in primitives like `int` extended with methods—automatically satisfies `Printable` if it has matching methods. Compiles to a C fat pointer `{ void* data; const Printable_VTable* vtable; }`.

---

### Phase 6: Unified `define` & Pattern Matching (`match`) (Oct 17, 2026)

1. **Unified `define` Keyword**:
   * All type declarations start with `define`:
     * `define Point struct [ x: int, y: int, ]`
     * `define Shape enum [ Circle [ radius: float, ], Rectangle [ width: float, height: float, ], Point, ]`
     * `define Printable interface [ to_string: (): string, ]`
     * `define UserID int`

2. **Pattern Matching (`match`)**:
   * *Rejected alternatives*: `match (x) { ... }` (initially rejected by user during Phase 6 to keep `{}` only for blocks), `match (x) [ ... ]`, `match (x) ( ... )`, pipe `|`, and `end`.
   * *Initial Decision in Phase 6*: Container-free `match (expr) when Pattern: expr ... else: expr`. *(Note: Critique Part 1 Section D revisits how container-free `match` terminates when `else` is omitted on exhaustive enums or when `match` is nested; awaiting user selection.)*

---

## 4. Critique & Refinement Rounds (Oct 17–21, 2026)

After writing `SPECIFICATION.md` (Draft v0.1), a comprehensive architectural critique (`SPEC_CRITIQUE.md`) was created. The user reviewed Part 1 across **three rounds of inline comments**, locking in the following updates (which are now live in [`./SPECIFICATION.md`](./SPECIFICATION.md) Draft v0.2):

### Locked-In Decisions from Critique Rounds 1–3
1. **Explicit Function Return Types**:
   * Value-returning functions, methods, and lambdas must declare `:ReturnType` or `:?ReturnType` after `(...)`, e.g., `bind find_user = (id int): ?User { ... }`.
   * Functions that do not return a value omit `:ReturnType` and compile to `void` in C.
2. **Underlying Optional Type (`?T`) vs. Identifier Suffix (`name?`)**:
   * The type is written `?T` (`?int`, `?User`).
   * Any variable, parameter, or struct field of type `?T` must have a name ending in `?` (`user?`).
   * `user` (type `T`) and `user?` (type `?T`) are two distinct identifiers.
3. **Optional Flattening (`?(?T)` $\rightarrow$ `?T`)**:
   * Nested optionals automatically flatten to `?T`. Optional types `?T` are permitted inside collections (`[?int]`, `#[string: ?User]`).
4. **Presence Testing, Borrow Semantics & Shadowing in `if (user?)`**:
   * `if (flag?)` on a `?bool` tests **presence** (`has_value`), never boolean truthiness.
   * Inside `if (user?) { ... }`, `user` is bound as a **borrow** of `user?` (so `user?` cannot be mutated to `none` while `user` is borrowed) and **shadows** any outer `user`.
5. **Unified Dot Chaining (`.`) on Optionals**:
   * The standard `.` operator works on both non-optional values and optional expressions (`?T`), short-circuiting to `none` when the left side is `none` (`find_user(id)?.address?.city ?? "Unknown"`).
6. **String Interpolation with `?`**:
   * Bare `$ident` only parses non-optional identifiers (without `?`), so `"Are you $ready?"` interpolates non-optional `ready` followed by literal `?`.
7. **Statements vs. Expressions & Valid Positions for `<-`**:
   * Declarations (`let`, `bind`, `define`), assignments (`=`), and unwinds (`<-`) are statements, not expressions.
   * Unwind statements (`<-`) are only allowed as **standalone statements** (including `if`/`match` branches) or on the **top-level right-hand side of `??` in a variable declaration or assignment** (`bind user = find_user(id) ?? func <- none`). They are forbidden inside function arguments or sub-expressions.
8. **New Unwind Targets & Boundary Rule**:
   * Added `if <- val` (unwind nearest `if`), `next <-` / `next <- val` (skip to next loop iteration), `func <- val` / `func <-` (return from current function/method), and bare `<-` (unwind void block/loop/function).
   * Braceless `if (c) <- val` unwinds the nearest `{}` block, not the `if`.
   * Unwinds **cannot** cross a function or lambda boundary (compile-time error).

---

## 5. Status: v1.0 Specification & Bootstrap Compiler (`auto-v1-prototype`)

All open design items from [`SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md) (Rounds 1–6 and Parts 0, A, M, B, C, N) were resolved autonomously and logged in [`DECISIONS.md`](./DECISIONS.md). The complete language specification is published in [`SPECIFICATION.md`](./SPECIFICATION.md) (v1.0).

### Key v1.0 Additions & Standard Prelude
1. **Implicit Standard Prelude (`std/core.ll`)**:
   * Automatically loaded in every module without explicit `import`.
   * Defines the foundational `Result[T, E]` type and constructors/methods:
     * `Result[T, E].ok(val: T): Result[T, E]`
     * `Result[T, E].fail(err: E): Result[T, E]`
     * `.is_ok(): bool` and `.is_err(): bool`
     * `.unwrap(): T` (panics with error message on Err)
     * `.unwrap_or(fallback: T): T`
   * Built-in interfaces: `Printable`, `Drop` (`drop: (&self)`), `Hashable`, `Equatable` (`eq: (self, other: Self): bool`), `Comparable` (`cmp: (self, other: Self): int`), `Collectable[T]`, `Iterable[T]`, `Iterator[T]`.
   * Operator Backing & Prelude Math: `==` / `!=` backed by `Equatable` (and derived from `Comparable`), `<`, `<=`, `>`, `>=` backed by `Comparable`. Core functions `min`, `max`, `clamp` bounded by `[T: Comparable + Copyable]`.
2. **Compiler Attributes**:
   * Functions & methods:
     * `@inline`: Hint compiler backend to inline. Emits `inline __attribute__((always_inline))` in C.
     * `@noinline`: Forbid inlining. Emits `__attribute__((noinline))` in C.
     * `@must_use` / `@nodiscard`: Emits a compile-time warning if the return value of a call expression is discarded.
     * `@deprecated("message")`: Emits a compile-time warning whenever the function, method, enum, or struct is referenced.
   * Structs:
     * `@packed`: Disable alignment padding. Emits `__attribute__((packed))` on the generated C struct.
     * `@doc("string")`: Attaches documentation metadata to types, functions, and fields.
3. **High-Performance Buffer Operations**:
   * `string.push_bytes(ptr[u8], int)`: Copies raw bytes directly into an owned string's heap buffer via `memmove` with dynamic capacity growth, avoiding quadratic character-by-character loops.
4. **Standard Library**:
   * `std/core.ll`: Core prelude, assertions, `Result[T, E]`, `Drop`, `Hashable`, `Equatable`, `Comparable`, pure Lowlight 64-bit hashing stack (`hash_mix_u64`, primitive `.hash()` methods, string FNV-1a, float IEEE bit-cast), and pure Lowlight dynamic collections `@lang("list") List[T]`, `@lang("set") Set[T: Hashable]`, and `@lang("map") Map[K: Hashable, V]` with `Iterator[T]`, `Iterable[T]`, `Collectable[T]`, `Printable`, `Copyable`. (Compiler intrinsic `hash_of` completely retired).
   * `std/chars.ll`: Pure native Lowlight character classification, case manipulation, radix conversion (`to_digit`, `from_digit`), UTF-8 byte length calculation, and mirrored standalone functions.
   * `std/strings.ll`: Pure native Lowlight string manipulation library: predicates (`is_empty`, `is_whitespace`, `is_ascii`), prefix/suffix checks (`starts_with`, `ends_with`), substring search & non-overlapping occurrences counting (`index_of`, `last_index_of`, `contains`, `count`), whitespace trimming (`trim`, `trim_start`, `trim_end`), case conversions (`to_upper`, `to_lower`, `capitalize`), replacement & repetitions (`replace`, `replace_all`, `repeat`, `reverse`), padding/centering (`pad_left`, `pad_right`, `center`), and tokenization (`split`, `split_whitespace`, `lines`, `join`, `chars_list`, `bytes_list`).
   * `std/fs.ll`: Robust native Lowlight file I/O library: RAII `File` stream struct (`File.open`, `open_read`, `open_write`, `open_append`, `open_read_write`, `close`, `flush`, `seek`, `tell`, `size`, `write_string`, `write_bytes`, `read_bytes`, `read_to_string`, `drop`), with top-level convenience functions (`read_file`, `read_to_string`, `read_text`, `write_file`, `write_to_file`, `write_text`, `append_file`, `read_bytes`, `write_bytes`, `exists`, `file_size`, `remove_file`, `rename_file`, `copy_file`).
   * `std/os.ll`: Command-line arguments `args(): [string]` native parser, environment variables, native POSIX directory management (`mkdir_p`, `remove_all`, `file_exists`), CPU counts and platform architecture introspection.
   * `std/sort.ll`: `define Sortable interface [ sort: (&self) ]`, in-place Quicksort algorithm with median-of-three Lomuto partitioning and insertion sort optimization, `List[T].sort`, `List[T].sort_by`, `List[T].is_sorted`, `List[T].binary_search`, `List[T].reverse`, extensions on `Set[T].to_sorted_list` and `Map[K, V].to_sorted_keys`, and standalone sorting utilities.
   * `std/net.ll`: TCP networking (`TcpListener`, `TcpStream`).
   * `std/http.ll`: High-performance HTTP 1.1 server engine and client request primitives.
   * `std/strconv.ll` & `std/strfmt.ll`: Integer/float parsing and radix conversions with full two's-complement overflow safety (handling `INT64_MIN`).
   * `std/time.ll`: Epoch timestamps and time formatting.
   * `std/math.ll`: Safe C math wrappers.
   * `std/pool.ll`: Generational object pool (`Pool[T]`, `Handle[T]`).
   * `std/json.ll`: Fast JSON parser and serializer.
   * `std/testing.ll`: Testing assertions and harness.

### Self-Hosting Roadmap
1. **Stage 0 (Current)**: Go Bootstrap Compiler (`lowlight build`, `run`, `emit-c`, `check`, `lsp`) targeting ISO C11.
2. **Stage 1**: Standard library expansion and verification against ASan/UBSan with strict warning flags (`-Wall -Wextra -Werror`).
3. **Stage 2**: Self-hosted Lowlight compiler written in Lowlight, transpiling itself to clean C11, achieving complete language self-sufficiency.
