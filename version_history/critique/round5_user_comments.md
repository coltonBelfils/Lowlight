# Round 5 User Comments on `spec_critique.md` (v0.4)

**Date**: October 22, 2026 (13:10 PDT)
**Target Document**: `spec_critique.md` (v0.4 — Updated with Round 4 Feedback)

---

## Verbatim Inline Comments (1–18)

### Comment 1 (Part 1, Section A.1 — Lifetimes: Pure Option 2 Selected)
* **Selected Text**:
  > `* **Under Pure Option 2 (Functions can NEVER return &T):**`
  > `  * You can pass &T and &-T *into* functions, and you can create local &T variables inside a function, but a function's return type can **never** start with & or &-.`
  > `  ...`
* **User Comment**:
  > Do option 2. In these senarios, you would need to copy the data and then return the copy

---

### Comment 2 (Part 1, Section A.2 — Built-in `Copyable` Interface & `.copy()`)
* **Selected Text**:
  > `* **Yes!** You copy the underlying value so the struct gets its own independent owned value (and no borrow is stored):`
  > `  * Either via a built-in .copy() method on copyable/cloneable types:`
  > `  ...`
  > `  * Or via the type conversion syntax you chose in §4.1 (string(borrowed_str) / User(borrowed_user)).`
* **User Comment**:
  > Lets have a builtin copyable interface that calls for a .copy() method. That interface is implamented by all builtin types.

---

### Comment 3 (Part 1, Section D — Terminating `match`: Idea 1 `else:` / `else Pattern:`)
* **Selected Text**:
  > `#### Idea 1: Every match Ends with else: (Allowing else Pattern: for Exhaustive Enums!)`
* **User Comment**:
  > I'm leaning toward this idea. It's the cleanest

---

### Comment 4 (Part 1, Section D — Terminating `match`: Idea 3 Question)
* **Selected Text**:
  > `#### Idea 3: Make match a True { ... } Block That Uses Statement Unwinds!`
* **User Comment**:
  > This is intrigueing. How would "when 200 <- "OK"" know to compair 200 to status_code? If we go this route when would need to be a valid keyword in every block context.

---

### Comment 5 (Part 1, Section F — Question on Search Loop in Design 1)
* **Selected Text**:
  > `let first_even? = for (n in numbers) {`
* **User Comment**:
  > In this example, how would the compiler know what type first_even? is? It looks to be int, but under this design an array of some type. How do I specify that type and how, like in this example do I return a single value?

---

### Comment 6 (Part 1, Section F — Concern with Design 1 Returning `[T]` vs. `T`)
* **Selected Text**:
  > `* **Where Design 1 *does* have a quirk (Why you might hesitate):**`
  > `  Suppose you write a loop whose body yields int each iteration (x * 2), so the loop's type is [int]. What if inside that same loop you also wanted to break out early with for <- ...? Under Design 1, for <- would have to return [int] (the type of the whole loop) rather than a single int—OR using for <- val makes the loop return a single value instead of a vector. Also, every value-producing iteration in Design 1 implicitly allocates a heap vector [T].`
* **User Comment**:
  > This is my main concern. The for loop's value can be ignored and then it looks like a traditional for loop, but if the loop is going to return a value, is it going to be [T] or T. It can't be both. It should only always be one.

---

### Comment 7 (Part 1, Section F — Design 3 Selected: "Last Iteration Wins")
* **Selected Text**:
  > `##### Design 3: "Last Iteration Wins" (Exact Analogy to { ... } Blocks!)`
* **User Comment**:
  > Let's go with this one. Give me a full explination of it and show me it in a bunch of different contexts—traditional for loop, list iteration, map iteration, map/reduce if possible, …

---

### Comment 8 (Part 2, §2.1 — Allow Underscores in Numeric Literals)
* **Selected Text**:
  > `* Totally optional—we can either allow _ between digits for readability or keep numeric literals strictly digits-only until we do our dedicated numeric deep dive.`
* **User Comment**:
  > Yes. Allow underscores

---

### Comment 9 (Part 2, §3.2 — Variable Shadowing: Option 1 vs. Option 2)
* **Selected Text**:
  > `* **How you can get the benefits of shadowing without the bug:**`
  > `  * **Option 1 (Unrestricted Shadowing — Currently in Draft v0.3):** Allow let and bind to shadow both in the same scope and in inner scopes.`
  > `  * **Option 2 (Same-Scope Shadowing Only, Not Inner-Block Shadowing):** Allow re-declaring let x / bind x later in the **same** block (for step-by-step transformations like bind count = ...; bind count = ...), and allow if (user?) unwrapping, but **forbid** an inner { ... } block from shadowing a local variable from an enclosing block of the same function! (Go and Zig use variants of this rule specifically to prevent the let total = total + x loop bug.)`
* **User Comment**:
  > I want to lean toward option 1 but I want to better understnad option 2 first

---

### Comment 10 (Part 2, §3.3 / §10.1 — Function Type Syntax)
* **Selected Text**:
  > `What do you write in place of ??? to specify the **type** of the predicate parameter?`
  > `Now that function parameters use colons (n: int): bool, you have two natural ways to write a function type:`
* **User Comment**:
  > Allow both, but naming the parameter is certainly not required

---

### Comment 11 (Part 2, §3.3 / §10.2 — Capturing Lambdas Copy Variables)
* **Selected Text**:
  > `* **One important question for capturing:** When a lambda uses a variable factor from the outer scope:`
  > `  ...`
  > `  Does the lambda **borrow** factor (&factor), or **copy/move** factor into the lambda?`
* **User Comment**:
  > They should copy, not borrow

---

### Comment 12 (Part 2, §9 — `let Type.method` Parked for Later)
* **Selected Text**:
  > `* **Summary:**`
  > `  * bind Type.method = a fixed method on the type Type (zero-cost static C function).`
  > `  * Struct fields of function type = per-instance runtime-swappable behavior.`
  > `  * let Type.method = a global mutable function pointer that swaps the method for all instances of Type everywhere in the program.`
  > `  Knowing that distinction, do you want to forbid let Type.method (keeping methods bind-only and using struct fields for per-instance swappable functions), or do you actually want global swappable let Type.method?`
* **User Comment**:
  > I'm not fully diswaded of allowing let for type.method. Lets come back to it in a bit

---

### Comment 13 (Part 2, §4.1 / §5.1 — Larger Code Comparison of Model 1 vs. Model 2 & Question on `move`)
* **Selected Text**:
  > `* **Why Model 2 is huge for Lowlight:**`
  > `  * You already have & (read-only) vs. &- (mutable, required at call site).`
  > `  * In Model 2:`
  > `    * **Read-only borrow** = the effortless default (foo(x)).`
  > `    * **In-place mutation** = explicit &- at signature and call site (foo(&-x)).`
  > `    * **Deep copy** = explicit .copy() (x.copy()).`
  > `  What do you think of **Model 2** vs. **Model 1**?`
* **User Comment**:
  > It seems promising, but I'm not convinced one way or another yet. Show me some larger code comparison between the two models. You talk about a move keyword in model 2. What is that? That seems new.

---

### Comment 14 (Part 2, §5.1 — Iterating in a `for` Loop Borrows by Default)
* **Selected Text**:
  > `* In for (item in my_list) { ... }, if my_list is [string] or [User]:`
  > `  * **Recommended:** for (item in my_list) **borrows** my_list and binds item as an immutable borrow &T (or copies for primitives), so looping over my_list does **not** destroy my_list!`
  > `  * And if you want to **mutate** the elements of my_list in place during the loop, you write for (&-item in my_list) (or for (item in &-my_list))—matching if (&-user?)!`
* **User Comment**:
  > Yes. It borrows

---

### Comment 15 (Part 2, §5.2 — Recursive Structs: Option 1 Selected)
* **Selected Text**:
  > `* **Option 1: Automatic Heap Boxing for Self-Referential Optional Fields (Zero New Syntax!)**`
  > `  ...`
* **User Comment**:
  > Do option 1

---

### Comment 16 (Part 2, §8.2 — Enums: Pros/Cons of Tagged Unions vs. Robust C Enums)
* **Selected Text**:
  > `#### 1. What Is an enum in §8.2?`
* **User Comment**:
  > What are the pros and cons to having this more expanded definition of enums? I'm still not sold on having anything more than a robust verson of C enums

---

### Comment 17 (Part 2, §11.2 — Range Syntax: Option 1 `0..10` and `0..=10` Selected)
* **Selected Text**:
  > `1. **Option 1: 0..10 (Exclusive) and 0..=10 (Inclusive) — *(Rust / Kotlin style)***`
  > `   * 0..10 -> 0, 1, 2, 3, 4, 5, 6, 7, 8, 9 (stops *before* 10; ideal for 0..list.len()).`
  > `   * 0..=10 -> 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10 (the = visually shows 10 is included, just like < vs <=!).`
* **User Comment**:
  > this one

---

### Comment 18 (Part 2, §11.3 — `&-` Prefix-Only Confirmed)
* **Selected Text**:
  > `### §11.3 Is Anything Lost If &- Cannot Be Used as a Binary Operator?`
* **User Comment**:
  > Great
