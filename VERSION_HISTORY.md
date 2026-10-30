# Lowlight Document Version History & Changelog

This document tracks the complete chronological evolution of the **Lowlight** (originally explored under the working folder name **"Lightweight Language"**) specification and critique documents, along with links to the verbatim historical snapshots stored in `./version_history/`.

---

## 1. Archive Directory Structure

```text
Lowlight-Export/
├── DECISIONS.md                   # Complete item-by-item resolution log for v1.0 (on auto-v1-prototype)
├── PROJECT_SUMMARY.md             # Master onboarding & full design history
├── README.md                      # Quickstart, CLI guide, language tour & compiler architecture
├── SPECIFICATION.md               # Finalized Language Specification (v1.0 on auto-v1-prototype)
├── SPEC_CRITIQUE.md               # Critique & Audit (v0.7, resolved on auto-v1-prototype)
├── VERSION_HISTORY.md             # This file: complete version history & diffs
├── cmd/lowlight/                  # Lowlight v1.0 CLI entrypoint (`build`, `run`, `emit-c`, `check`, `version`)
├── internal/                      # Complete Go compiler pipeline (`token`, `lexer`, `ast`, `parser`, `types`, `sema`, `ir`, `codegen`, `driver`)
├── std/                           # Standard library modules (`math.ll`, `fs.ll`, `pool.ll`)
├── examples/                      # End-to-end showcase Lowlight programs
├── tests/                         # Strict C11 (`-Wall -Wextra -Werror`), ASan/UBSan & zero-leak E2E test suite
└── version_history/
    ├── specification/
    │   ├── v0.0_initial_seed_sample.md      # Oct 16, 2026 (16:12 PDT) — Original user seed code
    │   ├── v0.1_LANGUAGE_SPECIFICATION.md   # Oct 17, 2026 (15:53 PDT) — First complete spec artifact
    │   ├── v0.1_SPECIFICATION.md            # Oct 17, 2026 (17:35 PDT) — Workspace spec (Draft v0.1)
    │   ├── v0.2_SPECIFICATION.md            # Oct 21, 2026 (15:25 PDT) — Updated spec (Draft v0.2)
    │   ├── v0.3_SPECIFICATION.md            # Oct 21, 2026 (23:38 PDT) — Updated spec (Draft v0.3)
    │   ├── v0.4_SPECIFICATION.md            # Oct 22, 2026 (13:15 PDT) — Updated spec (Draft v0.4)
    │   ├── v0.5_SPECIFICATION.md            # Oct 22, 2026 (15:52 PDT) — Updated spec (Draft v0.5)
    │   └── v1.0_SPECIFICATION.md            # Finalized Lowlight v1.0 Specification (branch auto-v1-prototype)
    └── critique/
        ├── v0.1_spec_critique.md            # Oct 17, 2026 (17:48 PDT) — Initial critique of Spec v0.1
        ├── round1_user_comments.md          # Oct 20, 2026 (17:39 PDT) — 21 inline user comments on v0.1
        ├── v0.2_spec_critique.md            # Oct 20, 2026 (17:44 PDT) — Critique updated with Round 1
        ├── round2_user_comments.md          # Oct 21, 2026 (12:41 PDT) — 7 inline user comments on v0.2
        ├── v0.3_initial_spec_critique.md    # Oct 21, 2026 (12:46 PDT) — Critique updated with Round 2
        ├── round3_user_comments.md          # Oct 21, 2026 (15:23 PDT) — 1 inline user comment on v0.3
        ├── v0.3_final_spec_critique.md      # Oct 21, 2026 (15:25 PDT) — Critique updated with Round 3
        ├── round4_user_comments.md          # Oct 21, 2026 (23:33 PDT) — 31 inline user comments on v0.3
        ├── v0.4_spec_critique.md            # Oct 21, 2026 (23:42 PDT) — Critique updated with Round 4
        ├── round5_user_comments.md          # Oct 22, 2026 (13:10 PDT) — 18 inline user comments on v0.4
        ├── v0.5_spec_critique.md            # Oct 22, 2026 (13:17 PDT) — Critique updated with Round 5
        ├── round6_user_comments.md          # Oct 22, 2026 (15:48 PDT) — 6 inline user comments on v0.5
        ├── v0.6_spec_critique.md            # Oct 22, 2026 (15:54 PDT) — Critique updated with Round 6
        ├── v0.6_spec_audit.md               # Oct 22, 2026 (17:32 PDT) — Full audit of Spec v0.5 (merged into v0.7)
        └── v0.7_spec_critique.md            # Oct 22, 2026 (23:29 PDT) — Merged critique + audit, memory model & self-hosting
```

---

## 2. Language Specification Version History (`SPECIFICATION.md`)

### Version 0.0 — Initial Seed Syntax Sample
* **File**: [`./version_history/specification/v0.0_initial_seed_sample.md`](./version_history/specification/v0.0_initial_seed_sample.md)
* **Date**: October 16, 2026 (16:12 PDT)
* **Summary**:
  * Provided by the user as the opening prompt of the Lowlight design session, accompanied by the instruction: *"Run all design decisions past me. I know there will be many. Don't start on implamentation until everything is ironed out."*
  * Introduced the core aesthetic and syntax ideas:
    * `let` vs. `bind` variable declarations (`let a = 5`, `bind c = 10`).
    * Braceless and braced `if (...) ... else ...` expressions (`let b = if (c == 10) 5 else 8`).
    * Block expressions `{ ... }` yielding their final expression or unwinding early with `<-` (`<- 20`).
    * List/array literals `[1, 2, 3]`, `[3][1, 2, 3]` (later refined to `[3: 1, 2, 3]`), and `[0..10]`.
    * `define custom struct [ a: int, b: float = 8.9, ]` using square brackets `[]` for type definitions and struct literals `custom[a: 5]`.
    * First-class function syntax `let func = (a int, b float) { ... }`.

---

### Version 0.1 — First Complete Formal Language Specification
* **Files**:
  * Artifact copy: [`./version_history/specification/v0.1_LANGUAGE_SPECIFICATION.md`](./version_history/specification/v0.1_LANGUAGE_SPECIFICATION.md) (Oct 17, 2026, 15:53 PDT)
  * Workspace copy: [`./version_history/specification/v0.1_SPECIFICATION.md`](./version_history/specification/v0.1_SPECIFICATION.md) (Oct 17, 2026, 17:35 PDT — identical body; title changed from `# Formal Language Specification (Draft v0.1)` to `# Lowlight Language Specification (Draft v0.1)`)
* **Summary**:
  * Synthesized all 6 interactive design phases from Conversation 1 into a 13-section specification:
    * **§1 Core Philosophy & Principles**: Zero-cost C transpilation, compile-time safety without GC, expression-oriented blocks, and `{}` (code) vs. `[]` (data/types) delimiter separation.
    * **§2 Lexical Structure**: Optional semicolons, `//` comments, identifiers (including trailing `?` for optionals), and reserved keywords (`let`, `bind`, `define`, `struct`, `enum`, `interface`, `if`, `else`, `for`, `in`, `match`, `when`, `none`, `true`, `false`).
    * **§3 Variable Declarations & Mutability**: `let` (mutable) vs. `bind` (deeply immutable for primitives/structs, shallowly immutable for Maps/Sets/Vectors), plus `let` (C function pointer) vs. `bind` (static C function) for functions.
    * **§4 Primitive Types & Strings**: `int` (`i64`), `i8`..`i64`, `u8`..`u64`, `float` (`f64`), `f32`, `bool`, `char` (32-bit Unicode), `string` (owned heap), `&string` (slice), `&-string` (mutable borrow), and `$ident` / `${expr}` string interpolation.
    * **§5 Ownership, Move Semantics & Borrowing**: Single ownership, automatic C scope drop, move on assignment/pass for heap types, copy for scalars, `&T` (immutable borrow), and `&-T` (mutable borrow requiring `&-` at call sites).
    * **§6 Optionals & Non-Value Safety**: No `unit` type for empty blocks; optional variables end with `?` (`user?`), hold `none` or a value, narrow to `user` inside `if (user?)`, and support `??` fallback.
    * **§7 Compound Data Structures**: Dynamic vectors `[1, 2, 3]` (`[int]`), fixed stack arrays `[3: 1, 2, 3]` and `[100: 0]` (`[3: int]`), hash sets `#[1, 2, 3]`, hash maps `#["a": 1]`, and mandatory trailing commas on multi-line `[]` definitions.
    * **§8 User-Defined Types (`define`)**: Unified `define Name struct [...]`, `define Name enum [...]`, `define Name interface [...]`, and `define Name BaseType`.
    * **§9 Methods & UFCS**: Namespaced methods (`bind Point.length = (self &Point) ...`), UFCS dot calls, and implicit structural interface implementation (including extending built-in types like `int`).
    * **§10 Functions**: `(param1 type1) { ... }` or single-expression body; exhaustive control-flow return check.
    * **§11 Control Flow & Targeted Unwinding**: `if`/`else` expressions, unified `for` loops (ranges, collections, while-style, infinite, loop-as-expression returning `T` with `else` or `?T` without `else`), and targeted unwinding (`<- val`, `for <- val`, `func_name <- val`, `label <- val`).
    * **§12 Pattern Matching (`match`)**: Container-free `match (expr) when Pattern: expr ... else: expr`.
    * **§13 C Transpilation & Runtime Model**: Direct C99/C11 mapping table (`Opt_T`, fat pointer interfaces, tagged union enums, deterministic cleanup before C `goto`/`return`).

---

### Version 0.2 — Updated Language Specification (Current)
* **Files**:
  * Current export root: [`./SPECIFICATION.md`](./SPECIFICATION.md)
  * Version archive copy: [`./version_history/specification/v0.2_SPECIFICATION.md`](./version_history/specification/v0.2_SPECIFICATION.md)
* **Date**: October 21, 2026 (15:25 PDT)
* **Detailed Diffs from Draft v0.1 to Draft v0.2**:
  1. **§1 Principle 3 (Expression-Oriented Control Flow with Strict Statements)**:
     * Clarified that control-flow constructs (`if`, `for`, `match`, `{}` blocks) are expressions, whereas **declarations (`let`, `bind`, `define`), assignments (`=`), and unwinds (`<-`) are statements** that do not evaluate to values.
     * Added explicit support for `void` functions (functions omitting a return type) and `void` statement loops alongside value-returning functions and loops.
  2. **§2.3 Identifiers & §6 Optionals (`?T` vs. `name?`)**:
     * Formalized that the **underlying type** is written `?T` (e.g., `?int`, `?User`), while any **identifier** (variable, parameter, or struct field) holding an optional value must end with `?` (`user?`).
     * Clarified that `user` (type `T`) and `user?` (type `?T`) are **two distinct identifiers** in the language.
     * Added **Optional Flattening (§6.2)**: Nested optionals `?(?T)` automatically flatten to `?T`, and optional types `?T` are allowed inside collections (e.g., `[?int]`, `#[string: ?User]`).
  3. **§4.2 String Interpolation (`$ident` vs. `?`)**:
     * Added the rule that bare `$ident` interpolation only parses non-optional identifiers (stopping before `?`), because optional variables cannot be interacted with before unwrapping. Thus `"Are you $ready?"` interpolates non-optional `ready` followed by a literal `?` character.
  4. **§5.3 Call-Site Transparency vs. Method Receiver Auto-Borrowing**:
     * Clarified that direct function calls require explicit `&-` at the call site (`push(&-my_list, 4)` and `Point.translate(&-pt, 5, 5)`), whereas UFCS method calls (`pt.translate(5, 5)`) implicitly borrow the receiver `pt` according to the method's `self` signature (`&Point` or `&-Point`), verifying at compile time that `pt` was declared with `let` if `self` requires `&-`.
  5. **§6.3 Presence Testing, Borrowed Unwrapping & Shadowing in `if (name?)`**:
     * Specified that `if (flag?)` on a `?bool` tests **value presence** (`has_value`), never the inner boolean truthiness.
     * Specified that inside `if (user?) { ... }`, the unwrapped variable `user` is bound as a **borrow** of `user?` (preventing mutation of `user?` to `none` while `user` is borrowed) and **shadows** any outer variable named `user`.
  6. **§6.4 Unified Dot Chaining (`.`) on Optionals**:
     * Specified that the standard dot operator `.` works on both non-optional values and optional expressions (`?T`), short-circuiting to `none` (yielding `?FieldType`) if the left-hand side is `none` (e.g., `find_user(id)?.address?.city ?? "Unknown"`).
  7. **§6.5 Fallback & Early Unwind (`??`)**:
     * Added the guard-unwrap pattern allowing an unwind statement on the top-level right-hand side of `??` in a declaration or assignment: `bind user = find_user(id) ?? func <- none`.
  8. **§8.3, §9, §10 Explicit Function Return Types (`:ReturnType`)**:
     * Added mandatory `:ReturnType` (or `:?ReturnType`) syntax after parameter lists for all value-returning functions, lambdas, methods, and interface signatures (e.g., `bind add = (a int, b int): int { a + b }`, `bind find_user = (id int): ?User { ... }`), while functions that omit `:ReturnType` compile to `void`.
  9. **§11.3 Targeted Unwinding (`<-`) Enhancements & Restrictions**:
     * Added three new keyword targets alongside `{}` block unwind (`<-`), `for <-`, and `label <-`:
       * **`if <- val`**: Unwinds out of the nearest enclosing `if` expression.
       * **`next <-` / `next <- val`**: Skips to the next iteration of the nearest enclosing `for` loop from any nesting depth.
       * **`func <- val` / `func <-`**: Returns early from the current enclosing function or method (alongside `func_name <- val`).
     * Added **Bare Unwinds (`target <-`)**: Allowed without a value when exiting a non-value `{}` block, skipping a loop iteration (`next <-`), breaking a `void` loop (`for <-`), or returning from a `void` function (`func <-`).
     * Clarified that braceless `if (cond) <- val` unwinds the nearest enclosing `{ ... }` block, whereas `if (cond) if <- val` unwinds the `if`.
     * Added **Function/Lambda Boundary Rule**: Unwinds (`<-`) cannot cross a function or lambda boundary (compile-time error).
     * Added **Valid Positions for `<-` Statements**: Unwinds may only appear as standalone statements (including `if`/`match` arms) or on the top-level right-hand side of `??` in a variable declaration or assignment—never nested inside function call arguments or sub-expressions.

---

## 3. Critique Specification Version History (`SPEC_CRITIQUE.md`)

### Version 0.1 — Initial Critique of Draft v0.1
* **File**: [`./version_history/critique/v0.1_spec_critique.md`](./version_history/critique/v0.1_spec_critique.md)
* **Date**: October 17, 2026 (17:48 PDT)
* **Summary**:
  * Organized into **Part 1 (Top 6 Cross-Cutting / Structural Questions)** and **Part 2 (Section-by-Section Critique, §1–§13)**:
    * **Part 1.A**: Borrow Checker & Lifetime Model (First-class references with lifetimes vs. strict second-class scoped references + method auto-borrowing vs. explicit `&-` + reborrowing).
    * **Part 1.B**: Grammar Ambiguities from Optional Semicolons & Newlines (`a\n -b`, `return\n expr`, multiline `if`/`else`).
    * **Part 1.C**: Container-Free `match` Termination Ambiguity (how `match` knows when it ends when exhaustive without `else`, or when nested inside another `match`).
    * **Part 1.D**: Optional Naming Rule (`name?`) vs. General Type System (function return types, collections of optionals, `?bool` in `if (flag?)`, `user` vs. `user?` collision, string interpolation `"Are you $ready?"`).
    * **Part 1.E**: Targeted Unwinding (`<-`) Corner Cases (braceless `if (c) <- 5`, skipping iterations in nested blocks inside `for`, variable shadowing vs. `func_name <-`, method returns, unwinding inside sub-expressions, and temporary cleanup).
    * **Part 1.F**: Strict Non-Value Safety vs. Void Functions & Statement Loops (`append_tag` returning void, statement `for` loops, `for (true)` without `else`).
    * **Part 2 (§1–§13)**: Detailed edge cases for every section of the spec.

---

### Round 1 User Comments (on Critique v0.1)
* **File**: [`./version_history/critique/round1_user_comments.md`](./version_history/critique/round1_user_comments.md)
* **Date**: October 20, 2026 (17:39 PDT)
* **Summary of the 21 Inline User Comments**:
  1. Leaning toward Option 2 (second-class references) for lifetimes; asked for pros/cons of Option 1 vs. Option 2.
  2. Confirmed method calls (`pt.translate(5, 5)`) auto-borrow receiver, while direct calls (`Point.translate(&-pt, 5, 5)`) require explicit `&-`.
  3. Asked for a recommendation on how reborrowing (`push(&-list, 4)` when `list` is already `&-[int]`) should work.
  4. Chose Go-style automatic semicolon insertion rules adapted for Lowlight.
  5. Asked whether `end` or significant indentation (or another option) is better for terminating `match`.
  6. Decided functions should declare explicit return types (`:Type` / `:?Type`).
  7. Asked what "nested optionals (`??T`) vs. flattening" means and for pros/cons.
  8. Asked if a `?.` chaining operator is worth having since `?` is already at the end of optional variable names (`user?.name`).
  9. Clarified that the underlying type is `?T`, and any variable of that type must end in `?`.
  10. Decided `if (flag?)` on `?bool` tests presence (`has_value`), because the optional wrapper must be unwrapped before interacting with the inner value.
  11. Clarified `user` and `user?` are two different identifiers (`T` vs. `?T`); asked what was meant by narrowing edge cases.
  12. Asked how `"Are you $ready?"` should be handled in string interpolation.
  13. Clarified `<-` only unwinds `{ ... }` blocks (or explicit named targets); suggested `if <-` for unwinding an `if`.
  14. Asked why `<- value` as `continue` in a `for` loop is a problem.
  15. Asked how variable shadowing could collide with `func_name <-`, and proposed `func <-` as a general keyword target for returning from the current function/method.
  16. Stated a return cannot cross function calls and asked what "lexically enclosing" means.
  17. Stated assignments and definitions are statements (not expressions), unwinds are statements except on the RHS of `??` (`let a = b? ?? if <- 0`).
  18. Asked what "freeing temporaries on jump" means.
  19. Confirmed functions can be `void` (non-value-returning) and have a return type when they return a value.
  20. Confirmed `for` loops can also be `void` when not used as values.
  21. Confirmed `for (true)` without `else` can still return `?T` for consistency.

---

### Version 0.2 — Critique Updated with Round 1 Feedback
* **File**: [`./version_history/critique/v0.2_spec_critique.md`](./version_history/critique/v0.2_spec_critique.md)
* **Date**: October 20, 2026 (17:44 PDT)
* **Summary of Changes from Critique v0.1**:
  * Reorganized Part 1 into Sections A–F directly answering the user's 21 Round 1 comments:
    * **Section A**: Detailed pros/cons of Lifetimes Option 1 (Rust-style `'a`) vs. Option 2 (strict second-class references) vs. **Option 2.5** (second-class references + tied return borrows from `self` or sole borrowed parameter).
    * **Section B**: Locked in method receiver auto-borrowing vs. explicit call-site `&-`; proposed **Reference Flattening (Reborrowing)** rule.
    * **Section C**: Drafted concrete Go-style newline/semicolon insertion rules adapted for Lowlight (`()`/`[]` ignore newlines; `{}`/top-level insert `;` after identifiers, literals, `)`, `]`, `}`, type names, or bare `<-`, unless the next line starts with `else`, `when`, or `.`).
    * **Section D**: Compared `end` vs. indentation for `match` and proposed 3 native alternatives: Option 1 (`match (x) { ... }`), Option 2 (`match (x) [ ... ]`), and Option 3 (greedy `when`/`else` continuation with `(...)` for nested matches).
    * **Section E**: Recorded resolved optional rules and explained: (1) Nested optionals `??T` vs. flattening `?T`, (2) Unified `?.` dot chaining on expressions, (3) `if (user?)` borrow semantics and shadowing of outer `user`, and (4) Why `"Are you $ready?"` works automatically when bare `$ident` only parses non-optional identifiers.
    * **Section F**: Recorded resolved unwinding rules and explained: (1) Why skipping a loop iteration from inside `if (n < 0) { ... }` needs bare `<-` and `next <-`, (2) Jump-target namespace vs. variable shadowing and `func <-`, (3) Why unwinding across nested function/lambda boundaries must be a compile error, (4) What freeing heap temporaries on mid-expression unwind means, and (5) Void functions and statement loops.

---

### Round 2 User Comments (on Critique v0.2)
* **File**: [`./version_history/critique/round2_user_comments.md`](./version_history/critique/round2_user_comments.md)
* **Date**: October 21, 2026 (12:41 PDT)
* **Summary of the 7 Inline User Comments**:
  1. Agreed to flatten `??T` to `?T` and allow optional types in collections/optionals.
  2. Agreed to allow unified dot-chaining `.` on both optionals and non-optionals.
  3. Agreed that `if (user?)` treats `user` as a borrow of `user?`, and asked: *"Should there be syntax to show it is a borrow?"*
  4. Decided that an outer `user` is shadowed inside `if (user?)`.
  5. Liked `next <-` as an unwind target, and asked to rethink `for` loops so that **both the `for` loop itself and each individual iteration are treated consistently as expressions**.
  6. Agreed that unwinding across a nested function or lambda boundary is a compile-time error.
  7. Asked whether it would be better to **disallow unwind statements inside sub-expressions**, only allowing them in assignment/declaration statements (`??`) or as bare/standalone unwind statements.

---

### Version 0.3 (Initial) — Critique Updated with Round 2 Feedback
* **File**: [`./version_history/critique/v0.3_initial_spec_critique.md`](./version_history/critique/v0.3_initial_spec_critique.md)
* **Date**: October 21, 2026 (12:46 PDT)
* **Summary of Changes from Critique v0.2**:
  * Preserved Sections A–D of Part 1 and all of Part 2 (since the user noted they had not yet reviewed Sections A–D or Part 2).
  * Updated **Section E** with all Round 2 optional resolutions and added a follow-up comparing two options for `if (user?)` borrow syntax:
    * **Option 1 (Recommended)**: Implicit immutable borrow `if (user?)`, explicit mutable borrow `if (&-user?)`.
    * **Option 2**: Always explicit `if (&user?)` vs. `if (&-user?)` vs. `if (user?)` (move).
  * Updated **Section F** with Round 2 unwinding resolutions and added two follow-ups:
    * **Follow-Up 1 (Rethinking `for` Loops Around Expressions)**: Explored **Design 1** (Iteration values collect into `[T]` by default, with `next <- val` yielding an element, bare `next <-` filtering/skipping, and `for <- val` breaking early), **Design 2** (Accumulator/Fold model `for (x in list, sum = 0) { ... }` with zero heap allocation), and **Design 3** (Combined model supporting fold loops, search loops, and comprehensions).
    * **Follow-Up 2 (Where Can `<-` Unwind Statements Appear?)**: Recommended restricting `<-` strictly to standalone statements and the top-level RHS of `??` in declarations/assignments, forbidding `<-` inside function arguments or sub-expressions.

---

### Round 3 User Comment (on Critique v0.3 Initial) & Spec v0.2 Update
* **Files**:
  * User comment: [`./version_history/critique/round3_user_comments.md`](./version_history/critique/round3_user_comments.md) (Oct 21, 2026, 15:23 PDT)
  * Final Critique v0.3: [`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md) and [`./version_history/critique/v0.3_final_spec_critique.md`](./version_history/critique/v0.3_final_spec_critique.md) (Oct 21, 2026, 15:25 PDT)
* **Summary**:
  * The user approved **Section F Follow-Up 2** (*"This all looks good"*) and requested: *"Update the specification document to make it fully up to date with our changes."*
  * `SPECIFICATION.md` was updated from **Draft v0.1** to **Draft v0.2** incorporating all resolved decisions from Rounds 1, 2, and 3.
  * `SPEC_CRITIQUE.md` (v0.3 Final) was updated to mark Section F Follow-Up 2 as resolved and synchronize line number references with `SPECIFICATION.md` (Draft v0.2).

---

### Round 4 User Comments (on Critique v0.3) & Critique v0.4 / Spec v0.3 Update
* **Files**:
  * User comments: [`./version_history/critique/round4_user_comments.md`](./version_history/critique/round4_user_comments.md) (Oct 21, 2026, 23:33 PDT)
  * Updated Spec v0.3: [`./SPECIFICATION.md`](./SPECIFICATION.md) and [`./version_history/specification/v0.3_SPECIFICATION.md`](./version_history/specification/v0.3_SPECIFICATION.md) (Oct 21, 2026, 23:38 PDT)
  * Updated Critique v0.4: [`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md) and [`./version_history/critique/v0.4_spec_critique.md`](./version_history/critique/v0.4_spec_critique.md) (Oct 21, 2026, 23:42 PDT)
* **Summary**:
  * The user provided 31 inline comments across both Part 1 and Part 2 of `spec_critique.md` (v0.3).
  * Locked-in decisions were applied to `SPECIFICATION.md` (Draft v0.3), and `SPEC_CRITIQUE.md` (v0.4) was updated with detailed answers, comparisons, and new design options for all remaining open questions.

---

### Round 5 User Comments (on Critique v0.4) & Critique v0.5 / Spec v0.4 Update
* **Files**:
  * User comments: [`./version_history/critique/round5_user_comments.md`](./version_history/critique/round5_user_comments.md) (Oct 22, 2026, 13:10 PDT)
  * Updated Spec v0.4: [`./SPECIFICATION.md`](./SPECIFICATION.md) and [`./version_history/specification/v0.4_SPECIFICATION.md`](./version_history/specification/v0.4_SPECIFICATION.md) (Oct 22, 2026, 13:15 PDT)
  * Updated Critique v0.5: [`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md) and [`./version_history/critique/v0.5_spec_critique.md`](./version_history/critique/v0.5_spec_critique.md) (Oct 22, 2026, 13:17 PDT)
* **Summary**:
  * The user provided 18 inline comments on `spec_critique.md` (v0.4), locking in Pure Option 2 lifetimes, the built-in `Copyable` interface (`.copy()`), Design 3 ("Last Iteration Wins") for `for` loops, numeric underscores, optional parameter names in function types, copy-capturing lambdas, automatic heap boxing for recursive `?Node` fields, and `0..10` / `0..=10` ranges.
  * Updated `SPECIFICATION.md` to Draft v0.4 and `SPEC_CRITIQUE.md` to v0.5 with a full multi-context showcase of Design 3 `for` loops, a 40-line side-by-side comparison of Model 1 vs. Model 2 parameter borrowing, Option 1 vs. Option 2 shadowing, and Robust C-style enums vs. tagged unions.

---

### Round 6 User Comments (on Critique v0.5) & Critique v0.6 / Spec v0.5 Update
* **Files**:
  * User comments: [`./version_history/critique/round6_user_comments.md`](./version_history/critique/round6_user_comments.md) (Oct 22, 2026, 15:48 PDT)
  * Updated Spec v0.5: [`./SPECIFICATION.md`](./SPECIFICATION.md) and [`./version_history/specification/v0.5_SPECIFICATION.md`](./version_history/specification/v0.5_SPECIFICATION.md) (Oct 22, 2026, 15:52 PDT)
  * Updated Critique v0.6: [`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md) and [`./version_history/critique/v0.6_spec_critique.md`](./version_history/critique/v0.6_spec_critique.md) (Oct 22, 2026, 15:54 PDT)
* **Summary**:
  * The user provided 6 inline comments on `spec_critique.md` (v0.5), locking in Idea 1 for `match` (`else:` / `else FinalVariant:`), Option 1 for variable shadowing, Option 1 for Enums (Robust C-Style Enums only), and Model 2 default read-only borrowing with `&` replacing `&-` as the mutable borrow marker across the language.
  * Updated `SPECIFICATION.md` to Draft v0.5 and `SPEC_CRITIQUE.md` to v0.6 addressing the two follow-up questions on `for` loops (`T` vs. `?T` and `next <-`), recommending Model 2A over 2B, and surfacing the final two parked items (`let Type.method` and `for (i, item in list)`).

---

### v0.6 Spec Audit
* **File**: [`./version_history/critique/v0.6_spec_audit.md`](./version_history/critique/v0.6_spec_audit.md) (Oct 22, 2026, 17:32 PDT)
* **Summary**:
  * Full pass over Spec Draft v0.5 for holes and contradictions: structural holes (A1–A6), inconsistencies (B1–B10), unwritten default rules (C1–C13), deferred items (D), and housekeeping (E). Corrected two earlier claims (no `move` keyword needed; interfaces replacing payload enums).

---

### Critique v0.7 — Critique & Audit Merged; Memory Model & Self-Hosting Roadmap
* **Files**:
  * Merged Critique v0.7: [`./SPEC_CRITIQUE.md`](./SPEC_CRITIQUE.md) and [`./version_history/critique/v0.7_spec_critique.md`](./version_history/critique/v0.7_spec_critique.md) (Oct 22, 2026, 23:29 PDT)
* **Summary**:
  * Merged the v0.6 critique and the v0.6 audit into a single working document, `SPEC_CRITIQUE.md`. From now on, this is the only review document.
  * Added **Part M (Memory Model & Native / Self-Hosting Roadmap)** after a discussion of ownership and borrowing. It records these constraints: all memory management is decided at compile time, with no GC, refcounting, or copy-on-write. Second-class references are retained. The long-term goal is a native, self-hosted compiler.
  * Part M covers: `own` consume mode (M1); indices, pools with generational handles, and arenas for multi-path data (M2); arenas as user-definable typed regions, Option B (M3); unwinding as cleanup edges (M4); `Drop` (M5); an allocator interface (M6); generics as a prerequisite (M7); an `unsafe`/intrinsics layer (M8); FFI/syscalls (M9); a shared mid-level IR (M10); a specified memory layout (M11); and concurrency (M12).
  * Added **Part N ("C-isms" checklist)** of C-inherited behaviour Lowlight must define itself, plus rule C14 (drop order). Added self-hosting notes to A3, A5, B9, C8, C13, and Part D, with a recommended phase order.

---

### Lowlight v1.0 Specification & Complete Go Reference Compiler (Branch `auto-v1-prototype`)
* **Files**:
  * Decision Log: [`./DECISIONS.md`](./DECISIONS.md)
  * Finalized Specification: [`./SPECIFICATION.md`](./SPECIFICATION.md) and [`./version_history/specification/v1.0_SPECIFICATION.md`](./version_history/specification/v1.0_SPECIFICATION.md)
  * Reference Compiler & Tests: `cmd/lowlight/`, `internal/`, `std/`, `examples/`, `tests/`
* **Summary**:
  * Preserved `main` untouched at the Draft v0.5 / Critique v0.7 checkpoint (`f82b473`) so interactive step-by-step design can later resume cleanly from `main`.
  * On `auto-v1-prototype`, resolved every open item across Parts 0, A, M, B, C, D, E, and N of `SPEC_CRITIQUE.md`, balancing lightweight ergonomics with low-level systems control:
    * Model 2A ownership (`T` read-only default borrow, `&T` call-site visible mutable borrow, `own T` move/consume), custom `Drop`, and synthesized `.copy()`.
    * Lifetime-free memory safety + lexical region `Arena` / `ref[T]` (with compile-time escape verification) + generational `Pool[T]` / `Handle[T]` in `std/pool.ll`.
    * Monomorphized generics (`[T: Bound1 + Bound2]`), stack fat-pointer borrowed interfaces + owned boxed interface values (`Shape(c)`), and first-class closures (Plain copy capture + Resource move capture).
    * Full low-level systems escape hatch: `unsafe { ... }`, `ptr[T]` & memory intrinsics, `define packed Name struct [...]`, and `extern "header.h"` / `extern sym [as alias]: ...` C ABI interop.
    * Backend-independent arithmetic & layout rules (UB-free wrapping signed/unsigned arithmetic, deterministic insertion-order sets/maps, UTF-8 strings + slices).
  * Implemented and verified the complete, zero-dependency Go reference compiler (`lexer -> parser -> sema -> ir -> C11 codegen -> driver`), `std/` modules, 8 showcase programs, and strict ASan/UBSan + `LOWLIGHT_CHECK_LEAKS=1` E2E test suite.
