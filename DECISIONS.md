# Lowlight v1.0 — Design Decisions Resolution Log (`auto-v1-prototype`)

> [!IMPORTANT]
> **Branch Context (`auto-v1-prototype`)**
> This document records every design decision made autonomously on the `auto-v1-prototype` branch to resolve all open items from [`SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md) (v0.7) and complete the **Lowlight v1.0 Language Specification** ([`SPECIFICATION.md`](./SPECIFICATION.md)) for the Go bootstrap compiler.
>
> All prior user decisions from Rounds 1–6 are preserved intact. Every new decision balances two core goals:
> 1. **Lightweight & Readable**: Minimal keywords, explicit call-site intent, zero lifetime-annotation noise, clean syntax.
> 2. **Genuinely Low-Level & Self-Hostable**: Deterministic compile-time memory management, stack allocation, arenas, generics via monomorphization, raw memory `unsafe` intrinsics, C ABI `extern` FFI, and zero reliance on C undefined behavior so the compiler can cleanly progress `Go -> C -> Self-Hosted Lowlight`.

---

## 1. Part 0 — Carry-Over Questions from v0.6

| Item | Topic | Resolution | Rationale |
| :--- | :--- | :--- | :--- |
| **0.1** | `for` loop expression type (`T` vs `?T`) | Confirmed: `for (...) ... else ...` has type `T`; `for (...) ...` without `else` (in value position) has type `?T` (requiring a `?`-suffixed target identifier); `for` in statement position is `void`. | Matches `if` without `else`, statically handles 0-iteration loops without panics or hidden defaults. |
| **0.2** | `next <- val`: Way 1 vs Way 2 | **Way 1 (Independent iterations; last non-skipped value wins).** Bare `next <-` skips the rest of the iteration without overwriting the running loop result; `next <- val` sets the current iteration's result to `val` (dropping any prior heap result first) and advances. Cross-iteration state lives in enclosing `let` variables. | Unifies value loops and search loops under one rule (see **B4**), avoids adding accumulator syntax to `for (...)`, and prevents subtle cross-iteration ownership moves. |
| **0.3** | Ownership Model 2A vs 2B | **Model 2A.** Plain types copy by value; heap-owning (Resource) types **move** on `let`/`bind`, reassignment, struct/collection construction, and return; function parameters `(x: T)` **borrow read-only** by default; `(x: &T)` **borrows mutably**; `(x: own T)` **consumes ownership**; `.copy()` duplicates explicitly. | Zero-copy struct construction from locals, zero-copy function calls by default, and every `let`/`bind` variable owns its data so local lifetime annotations are never needed. |
| **0.4** | Allowing `let Type.method` | **Methods are `bind`-only (`bind Type.method = ...`).** | Compiles directly to static C/native functions and static vtables, enabling direct calls and inlining. Per-instance swappable behavior uses function-typed struct fields. |
| **0.5** | Index + element loop on sequences | **Supported:** `for (i, item in seq)` and `for (i, &item in vec)` on `[T]`, `[N: T]`, and slices (`i: int`). Also `for (ch in str)` and `for (byte_offset, ch in str)` on `string` (`ch: char`). | Matches map iteration `for (k, v in map)` and makes writing tokenizers, parsers, and array algorithms ergonomic. |

---

## 2. Part A — Structural Holes

### A1 / M1: Taking Ownership of Function Arguments (`own`)
* **Decision**: **Option (b) — Consuming parameter mode `(x: own T)` with call-site `own` required when moving a named non-plain variable.**
* **Rules**:
  * Read-only borrow: `(x: T)` called as `f(x)`.
  * Mutable borrow: `(x: &T)` called as `f(&x)`.
  * Ownership transfer (consume): `(x: own T)` (and `(own self)` on consuming methods):
    * Passing a named Resource (Move) variable requires `own` at the call site: `names.push(own name)` — making variable invalidation visible at the call site, symmetric with `&`.
    * Passing an rvalue temporary (`names.push("Alice")`, `names.push(make_name())`, `names.push(name.copy())`) or a Plain (Copy) value (`nums.push(x)` where `x: int`) does **not** require `own`, because no caller variable is invalidated.

### A2: Places (Fields, Indexes, Map Entries) & Unwrapping Optional Expressions (`as`)
* **Decision**:
  * Reading a Plain place (`pt.x`, `nums[0]`) copies by value.
  * Passing any place (`users[0]`, `order.customer`) to a read-only parameter or method borrows it in place with zero copies.
  * Moving a Resource (Move) value out of an indexed place (`users[0]`) or borrowed struct field is a **compile-time error** (use `.copy()`, `.pop()`, or `.remove(i)` / `.swap_remove(i)`).
  * Moving a whole owned local (`own x`) invalidates `x`.
  * **Unwrapping Arbitrary Optional Expressions (`if (expr as name)`)**:
    * `if (user?)` and `if (&user?)` remain the concise shorthand for optional identifiers (`user?` binds `user`).
    * `if (expr as name)` binds any optional expression (`if (find_user(id) as user)`, `if (ages["Alice"] as age)`) to the non-optional identifier `name` as a read-only borrow (or value for plain types) scoped to the `if` branch, without requiring a temporary `let res? = ...` variable.

### A3: Stored Interface Values & Heterogeneous Collections
* **Decision**: **Option (a) — Dual Borrowed / Owned Interface Representation via `Interface(val)`.**
  * Interface parameters `(x: Printable)` and `(x: &Printable)` pass a stack fat pointer `{ data, vtable, owned = false }` with **zero heap allocation**.
  * Converting a concrete value via `Printable(val)` (or assigning/pushing where an owned interface is expected, via `Shape(my_circle)`) moves/copies `val` into a heap box `{ data, vtable, owned = true }` whose vtable carries a generated `drop` and `copy` function.
  * This enables `[Shape]`, struct fields of interface type, and returning interface values while keeping enums pure tag enums (Round 6). For high-performance compiler ASTs without per-node `malloc`, `Arena` + `ref[T]` or `Pool[T]` handles provide bump/slab allocation.

### A4: Plain (Copy) vs. Resource (Move) Types & Auto-Derived `Copyable`
* **Decision**:
  * **Plain Types** (copied implicitly by value, no destructor): primitives (`int`, `i8`..`i64`, `u8`..`u64`, `float`, `f32`, `bool`, `char`), `enum`s, distinct types over plain base types, `ptr[T]`, `ref[T]`, non-heap-capturing function values, `[N: T]` where `T` is Plain, non-boxed `?T` where `T` is Plain, and `struct`s without `Drop` whose fields are all Plain.
  * **Resource (Move) Types** (single owner, moved on assignment/init/return, deterministically dropped): `string`, `[T]`, `#[T]`, `#[K: V]`, `Arena`, owned `Interface` values, heap-boxed recursive `?T`, types implementing `Drop`, heap-capturing closures, and any struct/array/optional containing a Move type.
  * **Auto-derived `.copy()`**: Automatically synthesized for any struct whose fields are all copyable (unless overridden by an explicit `bind StructName.copy` method) and for all enums.

### A5: Complete Operator Set & Precedence
* **Decision**:
  * **Logical**: Keywords `and`, `or`, `not` (short-circuiting, strict `bool` operands — no implicit integer/pointer truthiness). Avoids overloading `&` and `!`.
  * **Bitwise**: `&`, `|`, `^`, `~`, `<<`, `>>` on all integer types (infix `&` is bitwise AND; prefix `&` is mutable borrow — syntactically disjoint).
  * **Arithmetic & String `+`**: `+`, `-`, `*`, `/`, `%`, unary `-` on matching numeric types (`string + string` concatenates into a new owned `string`).
  * **Comparison**: `==`, `!=` on primitives, `string` (content equality), `enum`, `ptr[T]`, `?T` (including `x? == none` / `x? != none`), and structs of comparable fields; `<`, `<=`, `>`, `>=` on numbers, `char`, and `string`.
  * **Compound Assignments**: `+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`, `>>=` as **statements** (never expressions).

### A6: Capturing Lambdas
* **Decision**: **Closure-as-Anonymous-Struct Rule.**
  * Plain captures are copied by value into the closure environment.
  * Resource (Move) captures are **moved** into the closure environment (or explicitly `.copy()`'d beforehand into a local binding if the outer scope still needs the original).
  * All captured bindings are deeply immutable (`bind`) inside the lambda body.
  * Heap-capturing closures own their environment and deterministically free it when dropped.

---

## 3. Part M — Memory Model, Generics, Arenas, Unsafe, FFI & Mid-Level IR

| Item | Topic | Resolution |
| :--- | :--- | :--- |
| **M0** | Runtime safety checks | Compile-time ownership/borrowing/regions (zero GC/ARC). Array/vector bounds checks, division-by-zero checks, and invalid bit-shift checks trap deterministically by default and can be elided with `--release`. |
| **M2 / M3** | Arenas (`Arena` & `ref[T]`) & `Pool[T]` | Built-in `Arena` bump allocator (`Arena.new()`, `arena.alloc(val): ref[T]`, `arena.reset()`). `ref[T]` is a copyable non-null region reference allowing field reads/writes for graphs, doubly-linked lists, and ASTs. Arena destructors are registered for Move values stored in an arena and run on `Arena` drop/reset. **Compile-time escape check**: a `ref[T]` cannot outlive its local `Arena` or be returned from a function unless the function borrows `&Arena`. Standard library `std/pool.ll` provides a generic generational `Pool[T]` and `Handle[T]`. |
| **M4 / C14** | Unwind cleanup & drop order | All `<-` unwinds compile to explicit IR cleanup sequences (no `setjmp`/`longjmp`). Locals in each exited scope drop in **reverse declaration order (LIFO)**; struct fields drop in **reverse declaration order**. Moved slots are zeroed (plus a moved flag for `Drop` structs) so conditional moves never double-free or leak. |
| **M5** | User-defined cleanup (`Drop`) | Built-in `Drop` interface (`drop: (&self)`). Any struct implementing `bind T.drop = (&self) { ... }` becomes a Move type and runs `T.drop` before field drops at scope exit, reassignment, and unwinds. |
| **M6** | Allocator architecture | Runtime allocator hooks (`ll_alloc`, `ll_realloc`, `ll_free`) wrap the system allocator on C with OOM abort and optional leak-tracking (`LOWLIGHT_CHECK_LEAKS=1`), alongside explicit `Arena` region allocation and `unsafe` raw allocation intrinsics. |
| **M7** | User-level Generics | Type parameters use `[T, U]` with optional interface bounds `[T: Printable]` on `struct`s, `bind` functions, and methods (`define Pair[A, B] struct [...]`, `bind id[T] = (x: T): T ...`). Type arguments can be explicit (`Pair[int, string][...]`, `id[int](5)`) or inferred from arguments. Compiled via **full compile-time monomorphization**. |
| **M8** | `unsafe` & raw memory intrinsics | `unsafe { ... }` block expression (usable inline or as an entire function body). Enables typed raw pointers `ptr[T]`, pointer deref/read/write/cast/offset (`alloc_raw`, `realloc_raw`, `free_raw`, `null_ptr`, `is_null`, `ptr_add`, `ptr_read`, `ptr_write`, `ptr_cast`, `addr_of`, `copy_bytes`, `zero_bytes`), `.c_str()`, and calling `extern` C functions. `size_of[T]()` and `align_of[T]()` are safe anywhere. |
| **M9** | C FFI (`extern`) & Modules (`import`) | `extern "header.h"` includes a C header; `extern fn_name: (params): Ret` declares an external C ABI symbol (callable inside `unsafe`). Multi-file programs use `import "path.ll"` (merging declarations or `import "path.ll" as alias` for qualified access). Entry point is `bind main = () { ... }` or `bind main = (): int { ... }`. |
| **M10** | Mid-Level IR (`internal/ir`) | Compiler lowers typed AST to an explicit monomorphized Mid-Level IR (MIR) with concrete types, lowered closures/vtables, explicit scope drop lists, and explicit unwind cleanup jumps before emitting C11. |
| **M11** | Memory layout | Struct fields preserve declaration order with platform C ABI alignment (`define packed Name struct [...]` supported for packed layout). Unified `ll_string` / `ll_vec` layout (`{ data, len, cap }` where `cap == 0` marks zero-allocation read-only views). |

---

## 4. Part B & Part C — Syntax, Consistency & Edge-Case Rules

* **B1 (`?.` on optional expressions)**: `?` is **mandatory** before `.` on optional expressions (`find_user(id)?.name`, `map[k]?.field`). Optional variable identifiers already end in `?` (`user?.name`).
* **B2 (Unified `self` / `&self` / `own self` & `Self`)**: Both interfaces and `bind Type.method` support `(self, ...)`, `(&self, ...)`, and `(own self, ...)` (explicit `self: Type` / `self: &Type` also remains valid on methods). `Self` is valid inside `interface` declarations.
* **B3 (`match` disambiguation, patterns & `match <-`)**:
  * An `else` followed by `:` or `<Pattern>:` inside a `match` arm belongs to the innermost `match`.
  * `else Enum.Variant:` requires all variants of `Enum` to be covered exhaustively with no duplicates.
  * Patterns support literals, `none`, qualified enum variants, comma-separated alternatives (`when 1, 2, 3:`), and ranges (`when '0'..='9':`).
  * `match <- val` / `match <-` unwinds to the nearest enclosing `match`.
* **B4 (`for` evaluation & `next label <-`)**: Loops support labels (`outer: for (...)`) with both `outer <-` (break outer) and `next outer <-` (continue outer). Blocks also support labels (` blk: { ... } `).
* **B5 (`func_name <-`)**: Removed in favor of unambiguous `func <-` (nearest function) and `label <-` (enclosing labeled loop/block).
* **B6 (`Name[...]` & struct field defaults)**: `Type[field: val, ...]` (with `field:`) is always a struct literal; `expr[idx]` without `field:` is indexing/slicing. Struct fields support default values (`port: int = 8080`) and optional fields (`tls?: ?string`) implicitly default to `none` when omitted.
* **B7 (Distinct types)**: `define UserID int` inherits base operators closed over `UserID`, numeric/string literal adaptation, `.copy()`, and `.to_string()`, without inheriting custom user-added methods on `int`.
* **B8 (Void function types)**: `(int)` / `(a: int)` and `()` denote void-returning function types; grouping parentheses are not used in type grammar.
* **B9 (Sequence views, compile-time `bind`, and slicing)**:
  * Top-level constant `bind N = 64` can be used in `[N: T]` and `[N: val]`.
  * Read-only `[T]` parameters accept `[T]` vectors, `[N: T]` stack arrays, and `xs[lo..hi]` slices with zero allocation.
  * Slicing `xs[lo..hi]` / `xs[lo..=hi]` (on vectors, arrays, and strings) passes a zero-copy view in read-only argument position and allocates an owned copy in owning position (`let sub = xs[lo..hi]`).
* **B10 (Reserved keywords)**: 27 keywords: `let`, `bind`, `define`, `struct`, `enum`, `interface`, `if`, `else`, `for`, `in`, `match`, `when`, `next`, `func`, `none`, `true`, `false`, `self`, `Self`, `own`, `and`, `or`, `not`, `as`, `unsafe`, `extern`, `import`.
* **C1–C14 Defaults**: All 14 proposed rules in Part C are adopted verbatim (`T` implicitly wraps into `?T`; read-only params are `bind`-immutable; reassigning a `let` drops old value first; partial/loop moves without re-init are compile errors; `foo(&x, x)` and mutating an iterated collection are compile errors; discarded non-void expressions drop cleanly; diverging paths satisfy exhaustiveness; direct and mutual recursive `?T` struct cycles auto-box on the heap; string literals in read-only param position do not allocate; interpolation requires `Printable`/`.to_string()`; enums support explicit integer discriminants and optional underlying integer type `define Flags enum u8 [ A = 1, B = 2, ]`; braceless `if (c) <- val` unwinds nearest `{}`; maps/sets preserve deterministic insertion order; reverse-declaration-order drops).

---

## 5. Part N — Defined "C-isms" (Backend-Independent Semantics)

1. **Integer Widths (N3)**: `int` is always 64-bit signed (`i64`); `float` is always 64-bit IEEE-754 (`f64`).
2. **Signed Overflow (N1)**: Deterministic two's-complement wrapping on `+`, `-`, `*` and unary `-` (emitted via unsigned casts in C11 so signed-overflow UB never occurs).
3. **Division & Modulo (N9)**: Truncates toward zero; division or modulo by `0` traps deterministically (`MIN / -1` wraps cleanly without C UB).
4. **Bit Shifts (N2)**: Negative shift counts or shift counts `>= bit_width` trap deterministically (unless `--release` masks by `width - 1`); `>>` is arithmetic on signed integers and logical on unsigned integers (`<<` emitted via unsigned cast in C11 to avoid signed left-shift UB).
5. **Evaluation Order (N4)**: Strictly left-to-right for binary operators (with short-circuiting on `and`, `or`, `??`, `?.`), call arguments, struct fields, and collection elements.
6. **Initialization (N5)**: Every variable and struct field is deterministically initialized before read.
7. **`char` vs `u8` (N7)**: `char` is a 32-bit Unicode scalar (`uint32_t`); raw bytes are `u8`.
8. **`string` Representation (N8)**: Explicit `{ data, len, cap }` UTF-8 buffer (maintaining a trailing `\0` byte on heap allocations and literals for zero-cost `c_str()` FFI); embedded NUL bytes inside strings are preserved via explicit `len`.
9. **Program Entry (N14)**: `bind main = () { ... }` (returns exit status `0`) or `bind main = (): int { ... }` (returns `int` truncated to OS exit code, after dropping all live resources in `main`).

---

## 6. Refinements Discovered During v1.0 Compiler & `std/` Implementation

1. **`extern <c_name> as <local_alias>` FFI Aliasing**:
   * Wrapping C standard library functions in safe Lowlight modules (e.g., `std/math.ll` exposing safe `sin`, `cos`, `tan` around `<math.h>`) creates a symbol collision if the `extern` declaration and the safe `bind` wrapper must share the same Lowlight identifier.
   * **Decision**: Allow optional `as <alias>` on `extern` function declarations (`extern sin as c_sin: (x: float): float`), emitting calls to the exact C ABI name `sin` while binding `c_sin` in Lowlight scope so `bind sin = (x: float): float { unsafe { c_sin(x) } }` is clean and collision-free.
2. **Sequence & Set/Map Ergonomics (`[string].join`, `.contains`)**:
   * Added `[string].join(sep: string): string` as the natural dual of `string.split(sep: string): [string]`, avoiding quadratic temporary allocations when formatting lists and paths.
   * Added `.contains(k)` on `#[T]` and `#[K: V]` as an ergonomic alias alongside `.has(k)`, unifying membership checks across `[T]`, `[N: T]`, `string`, `#[T]`, and `#[K: V]`.
3. **Search `for` Loops with Statement Bodies**:
   * A `for` loop with an `else` fallback (or yielding values solely via `for <- val` inside its body) whose body ends in a statement (`if (cond) { for <- x }`) checks its body as statement-valued while collecting all `for <- val` exits into the loop's result type `T` (with `else`) or `?T` (without `else`).

---

## 7. Post-v1.0 Security Audit & Robustness Resolutions

During the post-v1.0 codebase audit and fuzzing pass, 12 key compiler and standard library areas were identified, hardened, and verified:

1. **Top-Level Constant Dependency Resolution & Topological Ordering (Bugs 1 & 2)**:
   * Fixed compiler nil-pointer crash and C uninitialized variable emission on out-of-order and forward-referenced top-level `bind` constants.
   * Semantic analyzer topologically sorts top-level variable dependencies and detects cyclic definitions (`A = B + 1`, `B = A + 1`) at compile time with a clean diagnostic error instead of crashing.
2. **Move Semantics on Dynamic Vector Reallocations (`[NonCopyable]`) (Bug 3)**:
   * Fixed C compilation failure when instantiating vectors containing non-copyable Resource types (e.g. types with `Drop`).
   * `ll_vec_reserve` uses raw memory relocation (`memcpy`) without dropping or duplicating slots, adhering strictly to Lowlight move semantics.
3. **Full Two's-Complement `INT64_MIN` Overflow Hardening (Bug 4 & Security Hole 6)**:
   * Handled `-9223372036854775807 - 1` without arithmetic wrap in `std/strconv.ll:to_radix` (using `u64(0) - u64(v)`) and `std/strfmt.ll:format_commas`.
   * Added strict 64-bit integer overflow detection in `std/strconv.ll:parse_int_radix` returning `none` on overflow.
4. **Recursive Arena Escape Analysis (Security Hole 1)**:
   * Extended `arenaDepthOfExpr` in `check_stmt_expr.go` to deeply recurse into `MemberExpr`, `IndexExpr`, `SliceExpr`, `UnaryExpr`, `IfExpr`, `BlockExpr`, and `MatchExpr`, preventing `ref[T]` pointers from escaping an `Arena` scope through struct fields, if/else branches, or block values.
5. **C Namespace Isolation & Module Mangling (Security Hole 2)**:
   * Non-entry, non-core modules mangle top-level functions and generic instantiations with a deterministic module tag (`ll_fn_<module>_<name>`), preventing C ABI symbol collisions across multi-module projects.
6. **Generic Monomorphization Recursion Depth Limit (Security Hole 3)**:
   * Implemented a compile-time recursion limit (`MaxMonomorphDepth = 128`) on generic struct, function, and method monomorphizations, preventing compiler stack overflows or hangs on recursive generic bombs.
7. **Tombstone Compaction in Hash Collections (Security Hole 4)**:
   * In `ll_map_rehash` and `ll_set_rehash`, live entries are compacted into contiguous memory when `len < ent_len`, preventing tombstone leaks from repeated insertions and deletions. Rehashes in place when load factor is dominated by tombstones.
8. **Native POSIX Directory Operations (Security Hole 5)**:
   * Removed shell-based execution (`system("/bin/sh -c ...")`) from `std/os.ll:mkdir_p` and `remove_all`.
   * Replaced with secure, native POSIX implementations `ll_os_mkdir_p` and `ll_os_remove_all` in `runtime_h.go`, eliminating command injection vulnerabilities.
9. **Raw Pointer Assignment Without Dropping Uninitialized Memory (Security Hole 7)**:
   * Assigning to a raw pointer dereference `*p = val` in `unsafe` blocks now omits the prior drop of `*p`, ensuring newly allocated raw memory from `alloc_raw[T]` can be safely initialized.
10. **Prelude Unification (`Result[T, E]`) & Interface Signature Fixes**:
    * Moved `Result[T, E]` to `std/core.ll` (implicit standard prelude), available globally without imports across `std/fs.ll`, `std/net.ll`, `std/os.ll`, and `std/http.ll`.
    * Fixed `Drop` interface return signature in `std/core.ll` to `drop: (&self),` without `: void`.
11. **Attributes Support**:
    * Full compiler and codegen support for `@inline`, `@noinline`, `@must_use`, `@nodiscard`, `@deprecated`, `@packed`, and `@doc` on functions, methods, structs, and enums.
12. **High-Performance String Byte Push**:
    * Added `string.push_bytes(ptr[u8], int)` primitive in runtime and compiler, replacing slow byte loops in `std/http.ll`.

---

## 8. Separation of `while` (Scalar) and `for` (Collection) & Interface Unification

1. **Stack Array Literals (`[N: value]` vs `[N: e0, e1, ...]`)**:
   * Fixed-size stack array literals distinguish between repeat constructor `[N: value]` (when single element provided and $N > 1$) and explicit elements `[N: e0, e1, ...]`.
   * Single-element array `[1: 0]` is valid under either interpretation.
   * Element count mismatch when $M > 1$ and $M \ne N$ (e.g. `[4: 1, 2]`) is rejected at compile time.
2. **`Iterator[T]`, `Iterable[T]`, and `Collectable[T]` Interfaces in Core**:
   * Added generic interface declarations in `std/core.ll`.
   * Built-in collections (`[T]`, `[N: T]`, `string`, `#[T]`, `#[K: V]`) implement `Iterable[T]` and `Collectable[T]`.
3. **Map Iteration and Uniform `MapEntry[K, V]`**:
   * Single-variable map iteration `for (entry in my_map)` binds `entry` as `MapEntry[K, V]` with fields `key: K` and `value: V`.
   * Maps implement `Collectable[MapEntry[K, V]]` via `.push(entry)`.
   * Two-variable iteration `for (k, v in my_map)` is retained for convenience.
4. **`while` Loops Evaluate to Scalars**:
   * Syntax: `while (<cond>|<var> in <iter>)[: <eval_type>] <body> [else <else_body>]`.
   * Evaluates to scalar `T` with `else`, or `?T` without `else`.
   * Infinite condition `while(true): T` evaluates to `T` without requiring `else`.
   * Early exit via `while <- val` yields the scalar and terminates the loop.
   * In statement/void position, runs purely for side effects without allocating.
5. **`for` Loops Evaluate to Collections (`Collectable`)**:
   * Syntax: `for (<cond>|<var> in <iter>)[: <eval_type>: Collectable] <body>`.
   * In value position, evaluates to a collection where each iteration's expression is added via `push`.
   * Default collection is dynamic List `[Elem]`; explicit list shorthand is `: []`.
   * Non-`Collectable` annotations (e.g., `for (true): int`) are rejected with compile-time errors.
   * `next <-` filters/skips iteration without adding to the collection.
   * `for <-` breaks early, yielding the collection accumulated up to that point.
   * In statement/void position, runs purely for side effects without allocating.
6. **Pure Lowlight Collections Architecture & Interface Migration**:
   * Dynamic collection types (`List[T]`, `Set[T]`, `Map[K, V]`) are implemented directly in `std/core.ll` in pure Lowlight using raw memory primitives (`alloc_raw`, `realloc_raw`, `free_raw`, `ptr[T]`, `ptr_read`, `ptr_write`).
   * All core capabilities (`Printable`, `Copyable`, `Drop`, `Collectable`, `Iterable`, `Iterator`) are standard Lowlight interface implementations rather than Go compiler macro synthesis.
   * Bracket type syntax (`[T]`, `#[T]`, `#[K: V]`), literals (`[]`, `[a, b, c]`, `#[...]`), indexing (`lst[i]`, `m[k]`), and loops (`for (x in col)`) serve as pure compiler syntactic sugar over `List[T]`, `Set[T]`, and `Map[K, V]`.

---

## 9. `@lang` Attribute on Structs, `List` vs `Array` Nomenclature, Vector Removal & `Hashable` Constraints

1. **`@lang` Attribute on Generic Struct Declarations**:
   - The compiler recognizes `@lang("list")`, `@lang("set")`, and `@lang("map")` on generic struct declarations in `std/core.ll`.
   - Binds compiler syntax sugar (`[T]`, `[]`, `[1, 2, 3]`, `lst[i]`, `#[T]`, `#[K: V]`, `m[k]`, and collection comprehensions) to standard library structs.
2. **Collection Nomenclature: `Array` vs `List`, Complete Removal of `Vector`**:
   - `Array`: Fixed-size stack array type (`[N: T]`).
   - `List`: Dynamically resizable heap list type (`[T]` / `List[T]`).
   - `Vector` is not an alias to either and is completely removed from the language, standard library (`std/core.ll`), and compiler.
3. **`Map` Key Type Constrained to `K: Hashable`**:
   - Generic struct declarations enforce `K: Hashable`:
     - `define MapSlot[K: Hashable, V] struct`
     - `@lang("map") define Map[K: Hashable, V] struct`
     - `define MapIterator[K: Hashable, V] struct`
     - `define MapEntry[K: Hashable, V] struct`
   - Rejects non-Hashable key types at compile time.
4. **`Set` Type Constrained to `T: Hashable`**:
   - Generic struct declarations enforce `T: Hashable`:
     - `define SetEntry[T: Hashable] struct`
     - `@lang("set") define Set[T: Hashable] struct`
     - `define SetIterator[T: Hashable] struct`
   - Rejects non-Hashable element types at compile time.
5. **`Hashable` Interface & Method-Based Hashing**:
   - `@lang("hashable") define Hashable interface [ hash: (self): u64 ]` in `std/core.ll`.
   - Pre-bound `.hash = (self): u64` for all primitive types (`int`, `bool`, `string`, `char`, `float`, `u8`..`u64`, `i8`..`i32`).
   - Custom structs/enums implementing `.hash = (self): u64` automatically satisfy `Hashable` and generate appropriate C hashing delegates (`ll_hash__...`).
6. **Pure Lowlight Implementations & Safety**:
   - `List[T]`, `Set[T: Hashable]`, and `Map[K: Hashable, V]` implemented in pure Lowlight.
   - Non-consuming methods borrow in-place via pointer indexing (`self.data[i]`, `self.entries[i]`), preventing premature drops and double-free hazards.
   - Removal operations (`remove`, `clear`, `drop`) invoke destructors on dead resource slots via `ptr_read`, ensuring zero leaks under `LOWLIGHT_CHECK_LEAKS=1`.

---

## 10. Complete Retirement of `hash_of()` Intrinsic & Pure Lowlight Hashing Stack

1. **Complete Retirement of `hash_of()`**:
   - The compiler intrinsic `hash_of(val)` has been retired completely from `sema/check_calls.go`, `ir/lower_calls.go`, and runtime headers.
   - Any attempt to invoke `hash_of(...)` is rejected as an undeclared identifier at compile time.
   - Code computes hashes uniformly via `.hash()` method calls: `val.hash()`, `key.hash()`, `self.id.hash()`.
2. **Pure Lowlight Hashing Stack in `std/core.ll`**:
   - `bind hash_mix_u64 = (x: u64): u64`: SplitMix64 64-bit integer hash mixer implemented directly in Lowlight.
   - Primitive integer hashing: `int.hash`, `i8.hash`..`i32.hash`, `u8.hash`..`u64.hash`, `char.hash`, `bool.hash` pass their 64-bit value to `hash_mix_u64`.
   - Float hashing: `float.hash` normalizes `-0.0` to `+0.0`, bit-casts the 64-bit float IEEE representation to `u64` via `addr_of` and `ptr_cast[u64]`, and mixes via `hash_mix_u64`.
   - String hashing: `string.hash` implements the 64-bit FNV-1a hash algorithm directly in pure Lowlight looping over string byte length (`self.len()`) and byte indexing (`u64(self[i])`).
3. **C Runtime Simplification**:
   - Removed C implementations `ll_hash_u64_mix`, `ll_fnv1a_bytes`, and primitive `ll_hash__*` from `runtime_h.go`.
   - Zero C compiler affordances or helper functions are required for hashing; all hashing code lives in Lowlight.
   - C delegates in `cgen.go` invoke the pure Lowlight methods `ll_m_<prim>_hash` and `ll_fn_hash_mix_u64`.

---

## 11. Separation of `Equatable` and `Comparable` Interfaces & Operator Backing

1. **Architectural Separation**:
   - `Equatable`: Universal interface for equivalence relation checking (`==` and `!=`). Required method: `eq: (self, other: Self): bool`.
   - `Comparable`: Universal interface for total ordering (`<`, `<=`, `>`, `>=`). Required method: `cmp: (self, other: Self): int` (negative for `<`, `0` for `==`, positive for `>`).
   - Types that have natural equality but no meaningful total order (e.g. `bool`, `Point`, `Set`, `Map`) implement `Equatable` without being forced to define an arbitrary sorting order.
   - Any type implementing `Comparable` automatically satisfies `Equatable` (`a == b` $\iff$ `a.cmp(b) == 0`).
2. **Backing Behind Comparison & Ordering Operators**:
   - `==` and `!=` operators check for equality capability:
     - Primitives compile directly to native C equality operators (`((a) == (b))`) or `ll_str_eq` with zero function call overhead.
     - Custom structs and user types invoke `.eq()` or delegate to `(.cmp() == 0)`.
     - Generic bodies bounded by `[T: Equatable]` or `[T: Comparable]` permit `==` and `!=`.
   - `<`, `<=`, `>`, `>=` operators check for ordering capability:
     - Primitives (numeric, char, string) compile directly to native C comparison operators or `ll_str_cmp`.
     - Custom structs and user types invoke `.cmp()`.
     - Generic bodies bounded by `[T: Comparable]` permit ordering operators.
     - Types that do not implement `Comparable` (e.g. `bool`, unorderable structs) produce a compile-time diagnostic error when used with relational operators.
3. **Core Utility Functions (`min`, `max`, `clamp`)**:
   - `min[T: Comparable + Copyable] = (a: T, b: T): T`
   - `max[T: Comparable + Copyable] = (a: T, b: T): T`
   - `clamp[T: Comparable + Copyable] = (val: T, low: T, high: T): T`
   - Bounded by `Comparable + Copyable`, guaranteeing compile-time type safety while operating on primitives (`int`, `float`, `char`, `string`) and custom user structs alike.



