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

* **[`./SPECIFICATION.md`](./SPECIFICATION.md)** — **Current Language Specification (Draft v0.2)**. Contains the formal 13-section specification of Lowlight, incorporating all design phases and all resolved decisions from Critique Rounds 1, 2, and 3.
* **[`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md)** — **Current Critique & Open Questions Document (v0.3 Final)**. Contains the remaining open design questions awaiting user review in **Part 1 (Sections A, B, C, D, E Follow-Up, and F Follow-Up 1)** and **Part 2 (Section-by-Section Critique, §1–§13)**.
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
   * Compile-time borrow checking with read-only borrows (`&T`) and mutable borrows (`&-T`).
3. **Call-Site Mutation Transparency (`&-`)**:
   * Mutating a value through a function argument requires `&-` both in the function signature and at the call site (`push(&-my_list, 4)`), so a reader can see every state mutation without looking up function definitions.
   * UFCS method calls (`pt.translate(5, 5)`) auto-borrow the receiver (`pt`) according to the method's `self` parameter (`&Point` or `&-Point`), verifying `pt` was declared with `let` if `self` is `&-`.
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
   * *User's Borrow Syntax Invention*: The user designed `&T` for immutable references and **`&-T` for mutable references**, requiring `&-` at the call site (`push(&-my_list, 4)`) so mutations are never hidden.

2. **Fixed-Size Stack Arrays vs. Dynamic Heap Vectors**:
   * *Problem*: The seed sample had `[3][1, 2, 3]`, which looks like indexing a 2D array.
   * *Rejected alternatives*: `[3][1, 2, 3]` and `stack[1, 2, 3]`.
   * *Decision*: Use a colon inside brackets to separate the compile-time size from the elements:
     * `[1, 2, 3]` $\rightarrow$ Dynamic heap vector of type `[int]`.
     * `[3: 1, 2, 3]` $\rightarrow$ Fixed-size stack array of 3 elements (`[3: int]`).
     * `[100: 0]` $\rightarrow$ Fixed-size stack array of 100 elements initialized to `0` (`[100: int]`).

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
   * *Initial Decision in Phase 2*: `bind` structs are **deeply immutable** (cannot mutate fields of a `bind` struct), while `bind` Maps/Sets/Vectors were initially marked as shallowly immutable (cannot reassign the variable, but can call `.insert()`/`.push()`). *(Note: In Critique Part 2 §3, there is an open suggestion to make `bind` deeply immutable for collections too; awaiting user review.)*

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
     * `&string`: Borrowed read-only string view/slice (`{ const char* data; int64_t len; }`) — zero-copy.
     * `&-string`: Borrowed mutable string reference (`String*`), allowing in-place `.push(...)` mutation without moving ownership.
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

## 5. What Remains to Be Done Next (Active Roadmap)

When resuming work in a new session, pick up directly in **[`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md)**. Here is the exact checklist of pending items:

### Step 1: Finish Reviewing Part 1 of `SPEC_CRITIQUE.md`
The user has not yet commented on **Sections A–D** or the two follow-up questions in **Sections E & F** of [`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md):
1. **[Section A — Lifetimes](./SPEC_CRITIQUE.md#L9-L59)**: Choose between **Option 1** (Rust-style `'a` lifetimes), **Option 2** (strict second-class references), or **Option 2.5** (second-class references + tied return borrows from `self` or sole borrowed parameter).
2. **[Section B — Reborrowing](./SPEC_CRITIQUE.md#L62-L82)**: Confirm the recommended **Reference Flattening (Reborrowing)** rule when passing `&-list` inside a function that already took `list &-[int]`.
3. **[Section C — Go-Style Semicolon / Newline Insertion](./SPEC_CRITIQUE.md#L85-L103)**: Confirm the adapted Go-style automatic semicolon insertion rules and next-line continuation exceptions (`else`, `when`, `.`).
4. **[Section D — Terminating `match`](./SPEC_CRITIQUE.md#L106-L145)**: Choose between **Option 1** (`match (x) { ... }`), **Option 2** (`match (x) [ ... ]`), or **Option 3** (greedy `when`/`else` continuation with `(...)` for nested matches).
5. **[Section E Follow-Up — `if (user?)` Borrow Syntax](./SPEC_CRITIQUE.md#L159-L185)**: Choose between **Option 1** (implicit immutable borrow `if (user?)` + explicit mutable borrow `if (&-user?)`) vs. **Option 2** (`if (&user?)` vs. `if (&-user?)` vs. `if (user?)`).
6. **[Section F Follow-Up 1 — Rethinking `for` Loops as Expressions](./SPEC_CRITIQUE.md#L196-L253)**: Choose how each iteration's expression relates to the overall `for` loop expression (**Design 1: Collect to `[T]`**, **Design 2: Accumulator/Fold per iteration**, or **Design 3: Combined**).

### Step 2: Review Part 2 of `SPEC_CRITIQUE.md` (Section-by-Section Details, §1–§13)
Once Part 1 is complete, walk through **[Part 2 of `./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md#L288-L417)**:
* **§1 Principles**: Wording of Principle 1 ("zero hidden runtime overhead" vs. heap string defaults) and raw pointers/C interop.
* **§2 Lexical**: Multi-line comments, character literals, escape sequences, hex/binary/underscore numbers.
* **§3 `let` / `bind`**: Making `bind` deeply immutable for collections (`[T]`, `#[K: V]`, `#[T]`, `string`) just like structs; parameter colon consistency `(a int)` vs. `(a: int)`; general variable shadowing rules.
* **§3.3 / §10 Functions & Closures**: Function type syntax `(int, int): int`; whether anonymous lambdas can capture local variables or are non-capturing C function pointers; forbidding `let Type.method`.
* **§4.1 Numerics**: Defaulting untyped literals to `int`/`float` while adapting to target types (`u8`, `i16`); explicit type casting syntax; Copy vs. Move semantics for plain-old-data structs/stack arrays.
* **§4.2 Strings**: `&string` (`StringView`) vs. `&-string` (`String*`) C representation; picking a single `$` escape (`\$`); avoiding infinite recursion in `int.to_string`.
* **§5 Ownership**: Whether `for (item in my_list)` borrows `my_list` and yields `&T` by default; recursive structs (`Node` with `?Node`).
* **§7 Compound Data Structures**: Disambiguating `[1: 5]` / `[3: 5]` stack array syntax; vector out-of-bounds indexing (`T` with panic vs. `?T`); empty collection type annotations (`let v: [int] = []`).
* **§8 `define`**: Enum variant qualification (`Shape.Point` vs. `Point`); named vs. positional enum destructuring in `match`; `&self` vs. `&-self` in `interface` definitions; whether `define UserID int` is a transparent alias or distinct type.
* **§11 Control Flow**: Whitespace disambiguation for `x <- 5` vs. `x < -5`; exclusive (`0..10`) vs. inclusive (`0..=10`) ranges; index+value iteration `for (i, item in my_list)` on vectors.

### Step 3: Future Design Phases (Before Implementation)
After resolving `SPEC_CRITIQUE.md`, design the remaining systems-language features:
1. **Generics / Type Parameters** (for user-defined structs, enums, interfaces, and functions).
2. **Error Handling** (whether errors use optionals, result enums/unions, or targeted unwinding).
3. **Modules, Packages & Visibility** (multi-file imports, namespaces, public/private visibility).
4. **C FFI & Unsafe/Raw Pointers** (calling C headers directly, `extern` declarations, raw pointers for systems programming).
5. **Compiler / Transpiler Implementation Plan** (only after the user signs off on the complete specification).
