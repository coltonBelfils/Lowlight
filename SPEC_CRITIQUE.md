# Lowlight Spec Critique & Audit (v0.7, against Draft v0.5)

This is the single working review document for [SPECIFICATION.md](file:///Users/thekingoflimbs/Developer/Lowlight/SPECIFICATION.md). It merges the v0.6 critique (open questions from Round 6) with the v0.6 full audit, and adds a new section, **Part M**, covering the memory model and the native, self-hosted roadmap.

The spec itself hasn't been changed. Every item below is either a question for you or a recommendation waiting for your approval.

**Layout**
* **Part 0.** Open questions carried over from the v0.6 critique.
* **Part A.** Structural holes. These block a coherent language.
* **Part M.** Memory model & native / self-hosting roadmap. **New in v0.7.**
* **Part B.** Inconsistencies and ambiguities to unify.
* **Part C.** Rules that should be written down (proposed defaults).
* **Part D.** Deferred to upcoming phases.
* **Part E.** Housekeeping.
* **Part N.** "C-isms" checklist: behaviour currently inherited from C that Lowlight must define for itself. **New in v0.7.**

> [!WARNING]
> **Corrections to earlier critiques that this document depends on:**
> 1. In v0.5 I said Model 2 needs **no `move` keyword**. That's false. With read-only borrowing as the default and Pure Option 2, nothing lets a function *take ownership* of an argument. See **A1** and **M1**.
> 2. In v0.5 I said interfaces "already cover" what payload enums did. That only holds if interface values can be **owned and stored**. Under Pure Option 2 they can't. See **A3**.
> 3. I wrote `numbers.map(...)` in an example as if it already worked. It needs generics, which don't exist yet. See **M7**.
> 4. In discussion I said trees aren't representable. **Plain trees are fine.** A parent owning `children: [Node]` is ordinary single ownership. Only structures with *more than one path to a node* need something extra: parent pointers, graphs, doubly linked lists, and shared data. See **M2**.

---

## Part 0. Open Questions Carried Over from v0.6

### 0.1 `for` loops: `T` vs. `?T` (answered; for reference)
The rule is deterministic, and it is the same rule as `if`:
* **Has an `else` clause → always `T`.** `let score = for (...) { ... } else { 0 }`
* **No `else` clause → always `?T`.** The variable must therefore end in `?` (§6.2): `let score? = for (...) { ... }`. Writing `let bad = for (...) { ... }` without `else` is a compile error.

### 0.2 `next <- val`: Way 1 vs. Way 2 (**decision needed**)
Does a skipped iteration's value carry into the next iteration?
* **Way 1: iterations are independent; the last *non-skipped* iteration wins.** Bare `next <-` skips without updating the loop's result. Cross-iteration state lives in ordinary `let` variables outside the loop.
  ```
  let sum = 0
  let total = for (x in [10, 20, -5]) {
      if (x < 0) next <-   // skips -5; 30 remains the result
      sum = sum + x
      sum
  } else { 0 }
  ```
* **Way 2: each iteration's value is passed to the next one** through a named loop accumulator:
  ```
  let total = for (x in [10, 20, -5], sum = 0) {
      if (x < 0) next <- sum
      sum + x
  }
  ```

**Recommendation: Way 1.** It also unifies the meaning of `for ... else` (see **B4**).

### 0.3 Model 2A vs. 2B (**decision needed**)
**Recommendation: Model 2A.** `let b = a` and `Struct[field: a]` **move**, function parameters **borrow** read-only, and `.copy()` duplicates explicitly.
* Under 2A, building a struct from a local moves it in with zero copies. Under 2B, structs can't store borrows, so every local placed in a struct would be forced to copy.
* Under 2A, every `let`/`bind` owns its value. Borrows only exist as parameters, loop variables, and `if (x?)` unwraps, so local borrow lifetimes never need tracking.

Summary of 2A:
* `foo(x)` borrows read-only.
* `foo(&x)` borrows mutably.
* `let b = a` moves (copies for plain types; see **A4**).
* `a.copy()` duplicates explicitly.

### 0.4 Allowing `let Type.method` (§9) (**decision needed**)
* `bind Point.length = ...` is a fixed static C function.
* `let Point.length = ...` would be a global, reassignable closure variable, and reassigning it changes `.length()` for every `Point`.
* If allowed: can a `let` method satisfy an interface (by updating the vtable or calling through the global pointer), or must interface methods be `bind`?
* **Self-hosting note:** a reassignable method is a global mutable function pointer. That is cheap, but it blocks inlining and complicates any future static analysis. **Lean:** keep methods `bind`-only for now; you can add `let` later without breaking code.

### 0.5 Index + element iteration on vectors (§11.2) (**decision needed**)
Maps already support `for (k, v in m)` and `for (k, &v in m)`. Should `[T]` and `[N: T]` support:
```
for (i, item in my_list) { ... }
for (i, &item in my_list) { ... }
```
**Lean:** yes.

---

## Part A. Structural Holes (these block a coherent language)

### A1. Nothing lets a function take ownership of an argument
Parameters borrow read-only by default (§5.1). Pure Option 2 says stored or returned data must be `.copy()`'d (§5.2). Together, that means **every** function that keeps an argument has to deep-copy it:
```
let names: [string] = []
names.push(make_name())   // push only borrows `item`, so it must copy the string,
                          // then the temporary is freed. 2 allocations instead of 0.

bind Order.new = (customer: string, items: [Item]): Order {
    Order[customer: customer.copy(), items: items.copy()]   // forced deep copy of every item
}
```
It also means `for (s in strings) { out.push(s) }` can never move elements out.

**Options:**
* **(a) Consuming parameter mode, no call-site marker.** Mark the parameter, for example `(item: own T)`. The argument is moved in, and the caller's variable becomes invalid.
* **(b) Consuming mode plus a call-site marker for named variables,** for example `names.push(own name)`. This is the same reasoning as `&`: a call that invalidates your variable should be visible at the call site. Temporaries such as `push(make_name())` need no marker.
* **(c) No new mode.** The compiler silently moves temporaries and last uses, and copies otherwise. Ergonomic, but the copies are hidden, which breaks "copying is always explicit."

**Recommendation: (b).** It keeps all three parameter modes visible: read-only (no marker), mutate (`&`), consume (`own`). The keyword is a placeholder; you pick the spelling. This is also a prerequisite for arenas and pools (**M2**), since `arena.new(value)` and `pool.insert(value)` must consume their argument.

### A2. "Places" (fields, indexes, map entries) have no defined read/move rules
Model 2A says `let b = a` moves. What happens when `a` is **part of** something else?
```
let u = users[0]             // move out of a vector? That leaves a hole in the vector.
let n = order.customer       // move out of one field of a struct?
let u? = user_map["alice"]   // §7.2 says map lookup returns ?V. For a heap V, is that a copy or a move?
(o: Order): string { o.customer }   // returning a field of a borrowed parameter
```
**Recommended rule:** a *place* is a variable, a field (`a.b`), an index (`a[i]`), or a map entry (`m[k]`).
* Reading a place of a plain type copies it (see A4).
* Passing a place to a read-only parameter, calling a method on it, or chaining field access through it all **borrow in place**, with no copy.
* **Moving out of anything other than a whole owned local is a compile error.** The fix is to write `.copy()`, or to call an ownership-returning method such as `v.remove(i)` or `m.remove(k)`.
* **Open question:** how do you unwrap an optional place in place, without copying? `if (user?)` only works on identifiers. One possible syntax is `if (users["alice"]? as user) { ... }`. We need *some* binding form.

### A3. Interface values can't be stored, so heterogeneous collections are impossible
§8.3 compiles interface values to `{ void* data; vtable* }`, a pointer to someone else's data. That is a borrow, and under Pure Option 2 borrows can't go in structs or vectors. Payload enums were also removed (Round 6). The result: **there is no way to write `[Shape]`**, an AST, a list of UI widgets, and so on.

**Options:**
* **(a) Owned interface values.** Converting a concrete value to an interface type moves it into a heap box, and the fat pointer owns the box (the vtable gets a drop entry). Reuse your conversion syntax so the allocation is visible: `let s = Shape(my_circle)`, and `shapes.push(Shape(c))`. Interface-typed *parameters* still just borrow, with no allocation.
* **(b) Reopen payload enums** for closed sets of variants. No heap cost.
* **(c) Neither.** Users hand-roll a struct with a `kind` enum field.

**Recommendation: (a).** It fits `float(x)` / `UserID(x)` and keeps enums simple.

> [!IMPORTANT]
> **Self-hosting note.** A compiler written in Lowlight needs an AST, tokens, IR nodes, and types, all of which are closed sets of variants. With option (a) alone, every AST node is a separate heap box behind a vtable. That works, but it is slow and awkward for pattern-based code like `match node.kind`. **Revisit (b) payload enums with this in mind**, or make sure (a) plus arenas (**M2**) gives a workable AST representation. This is the single strongest argument for reopening payload enums.

### A4. Assignment semantics are no longer written down, and §5.2 contradicts Model 2A
* In v0.5, §5.1 lost the "`let b = a` moves" example when the borrow syntax changed. It is now unspecified, pending your 2A/2B call (**0.3**).
* §5.2 rule 1 says "local variables may be borrowed views." Under 2A, every `let`/`bind` owns its value, and borrows only exist as parameters, loop variables, and `if (x?)` unwraps. One of these has to go.
* **Copying small structs (Round 4, #22, never resolved):** Is `let p2 = p1` for `Point[x: int, y: int]` a move or a copy? Do user structs get `.copy()` automatically?

**Recommendation:**
* Types that own no heap memory are **plain**: primitives, enums, stack arrays of plain types, and structs made only of plain fields. Plain types copy implicitly, as in C.
* Types that own heap memory move.
* `Copyable` is derived automatically for any struct whose fields are all `Copyable`, and can be overridden.
* Result: `.copy()` is only ever required where a copy actually allocates.

### A5. Operators are not specified at all
There is no operator list, no precedence table, and no logical operators. This matters more now that `&` is the mutable-borrow prefix.
* **Logical:** `&&` / `||` / `!`, or `and` / `or` / `not`? The keywords avoid overloading `&` even further.
* **Bitwise:** `& | ^ ~ << >>`. Infix `&` (bitwise) and prefix `&` (borrow) can be told apart by position, which is fine.
* **Compound assignment** (`+=`, etc.): allowed, as statements?
* **`==` on strings, structs, enums:** content equality? Only for plain types? Equality of `?T` against `none`?
* **String concatenation with `+`,** or interpolation only?
* **No truthiness:** conditions must be `bool`. C programmers will assume `if (count)` works, so this needs saying.
* **Self-hosting additions:** shifts on signed values, shift amounts ≥ the bit width, and wrapping vs. checked arithmetic operators (see **Part N**). A compiler and an assembler do a lot of bit manipulation, so this must be fully defined.

**Recommendation:** a short dedicated operators round. I'd suggest `and` / `or` / `not`, compound assignment as statements, and `==` defined for primitives, strings, enums, and plain structs.

### A6. Capturing lambdas: hidden deep copies and confusing mutation
§3.3 says captures are copied (your Round 5 call). Two problems:
```
let items = load_items()          // a large vector
let f = (): int items.len()       // hidden deep copy of the whole vector

let total = 0
items.for_each((x: int) { total = total + x })   // mutates the lambda's private copy
                                                 // outer `total` stays 0
```
**Recommendation:** treat a closure as an **anonymous struct whose fields are its captures**, and reuse the struct rules from A4:
* Plain captures copy.
* Heap captures **move** into the closure (write `.copy()` if you still need the original).
* Captured values are immutable inside the lambda.
* Callbacks that need to change outer state take an `&` parameter.

This is the same rule as building a struct, so nothing new to learn. It does change your "copy" decision for heap values to "move," so it needs your approval. It also keeps closures consistent with second-class references: a closure never captures a borrow, so it can be stored and returned safely.

---

## Part M. Memory Model & Native / Self-Hosting Roadmap (New in v0.7)

### M0. Guiding constraints (from the memory-model discussion)
These are now treated as design constraints for every decision in this document:
1. **All memory management is decided at compile time.** No garbage collector, no runtime reference counting, no copy-on-write. *(Locked in by you; COW and `Rc`-style sharing are rejected.)*
2. **The C-transpiling compiler is a stepping stone.** The long-term goal is a **fully native, self-hosted** Lowlight compiler written in Lowlight. Nothing in the language may depend on C to define its meaning.
3. **Everything is implemented at least twice:** once in the bootstrap compiler, and again in Lowlight itself. Every feature's implementation cost counts double, so **simpler beats more powerful** when the two are close.
4. **Second-class references stay.** No lifetime annotations; references can't be stored in structs or collections, or returned. This is the main thing keeping the borrow checker small enough to write twice. Arenas (**M3**) are the one planned, controlled exception.

> [!NOTE]
> **Clarification on constraint 1.** "Compile-time memory management" rules out runtime *ownership tracking*. It does not rule out cheap runtime *safety checks* such as array bounds checks or generational-handle checks in pools (**M2**). Those don't decide when memory is freed; they only catch logic errors. **Decision needed:** are those checks acceptable, or should they be optional (for example, disabled in release builds)?

### M1. Ownership transfer (`own` / move)
Same issue as **A1**, restated as a memory-model prerequisite. Lowlight needs three visible parameter modes:

| Mode | Declaration | Call site | Effect |
|---|---|---|---|
| Read | `(x: T)` | `f(x)` | Temporary read-only view |
| Mutate | `(x: &T)` | `f(&x)` | Temporary exclusive view |
| Consume | `(x: own T)` | `f(own x)` (named variables only) | Ownership moves in; caller's variable is dead |

Arenas, pools, collections, and builders all depend on consume mode.

### M2. Multiple paths to data: graphs, parent pointers, shared data
Single ownership handles trees. Anything where a node is reachable along more than one path needs one of these, in increasing order of language cost:

1. **Indices into an owning collection (baseline, no language change).** `struct Graph [ nodes: [Node], edges: [(int, int)] ]`. Safe and fast. Stale indices after removal are a logic error, not memory unsafety.
2. **Pools with generational handles (standard library).** A pool stores fixed-size slots of one type, supports freeing individual items, and returns a handle `(index, generation)`. Each slot's generation increments on free, so a stale handle is detected instead of silently reading a reused slot. This is the right tool for graphs with deletion, entity systems, and node caches. It needs only generics (**M7**), consume mode (**M1**), and an unsafe layer (**M8**) to implement in Lowlight.
3. **Arenas with typed regions (language feature, Option B; your current lean).** See **M3**.

**Recommendation:** document (1) as the idiomatic pattern now, ship (2) in the standard library, and design (3) as the one language-level exception to second-class references.

| | Arena | Pool |
|---|---|---|
| Object size | Any, mixed | One type/size |
| Allocation | Bump a pointer | Pop a free-list slot |
| Free one item | Not possible | Yes |
| Free everything | One operation | One operation |
| Typical use | A phase with one lifetime (a compiler pass, a request) | Objects that come and go (graph nodes, entities) |
| Safety mechanism | Compile-time region check | Runtime generation check |

### M3. Arenas as typed regions (Option B) (**design needed**)
Under Option B, users can define arenas and region-bound types themselves, not just use one compiler-known `Arena`. Arena references (placeholder spelling `ref[T]`) are the one place where references may be **stored**, under these rules:
* A `ref[T]` may be stored only inside values that live in the **same** arena.
* A `ref[T]` may never outlive its arena's scope, be stored outside it, or be returned past it.
* The arena itself is an ordinary owned value. Its drop frees everything at once, so cycles are fine.

Open design questions:
1. **How a type declares it lives in a region.** Is it a property of the struct declaration (`define Node struct in region [ ... ]`), inferred from containing a `ref`, or a generic region parameter? The more inference, the bigger the checker.
2. **Mutation through `ref[T]`.** Strict readers-xor-writer makes mutable graphs nearly impossible. Options: (a) `ref` is read-only and you mutate via the arena by handle; (b) arena contents allow shared mutation with no aliasing guarantee *within* the region; (c) per-field mutability. This is the biggest semantic choice in M3.
3. **Heap-owning fields inside arena values** (`string`, `[T]`). Either (a) arena contents must be plain data in v1, (b) the arena records destructors and runs them on drop (a small runtime list, but still fully deterministic), or (c) such fields must themselves allocate from the arena.
4. **Passing arenas to functions.** Functions must be able to allocate into a borrowed arena (`(a: &Arena)`) and return the resulting `ref` to the caller. That return is an explicit exception to "functions can't return references," allowed only because the arena outlives the call.
5. **Relationship to the allocator interface (M6).** An Option-B arena should be a user-written type implementing `Allocator`, with the region check provided by the language. That is what lets users write their own arenas.

> [!TIP]
> Design the region check in the mid-level IR (**M10**) from the start, handling *exactly* the arena case. Resist generalising it toward Rust-style lifetimes; the arena-as-lifetime model is what keeps it small.

### M4. Unwinding must be cleanup edges, not a runtime mechanism
Targeted control-flow unwinding (`<-`) must drop every live owned value in each scope it exits. Specify that:
* Unwinds compile to **explicit cleanup paths** in the IR, with drops in reverse declaration order.
* There are no exception tables, `setjmp`/`longjmp`, or stack-walking at runtime.

This is easy for the C backend and identical for the native backend. **Decision needed:** drop order for the values in a scope, and for struct fields (reverse declaration order is the usual choice).

### M5. User-defined cleanup (`Drop`)
File handles, sockets, OS memory mappings, and arenas themselves need custom cleanup at scope exit. This was deferred to the FFI phase (Part D), but it is a core memory-model feature and a prerequisite for writing arenas and pools in Lowlight. **Recommendation:** a built-in `Drop` interface with one `(&self)` method, called automatically at the same points the compiler already frees built-in types.

### M6. Allocator interface
A self-hosted compiler can't assume `malloc`. Define a minimal `Allocator` interface (`alloc(size, align)`, `free(ptr)`, optionally `resize`) implemented by:
* the default heap (on the C backend, a thin wrapper around `malloc`/`free`; natively, built on OS pages),
* arenas and pools in the standard library.

**Decision needed:** do built-in collections (`[T]`, `string`, maps, sets) take an allocator?
* **(a)** Always use the default heap. Simplest, but a compiler can't keep its AST and strings in an arena.
* **(b)** An optional allocator chosen at construction, stored in the collection.
* **(c)** A scoped "current allocator" context.

**Lean:** (b), with the default heap when not specified.

### M7. Generics are a prerequisite
Arenas, pools, the allocator interface, owned interface values, `Copyable` returning `Self`, `.map`/`.filter`, and a standard library written in Lowlight all need user-level generics. Without them, the standard library must be built into the compiler, which defeats self-hosting. **Recommendation:** promote generics from "deferred" to the next major design phase, ahead of modules and FFI.

Things to decide in that phase: monomorphisation (generate code per type; fast, predictable, simple for C emission) vs. dictionary passing; interface constraints on type parameters; and whether region parameters (M3) reuse the same mechanism.

### M8. An `unsafe` / intrinsics layer
The standard library (vectors, strings, maps, arenas, pools, the allocator) must be writable in Lowlight, which means *some* code must handle raw memory. **Recommendation:** a narrow, clearly marked layer:
* a raw pointer type, with arithmetic and casting,
* intrinsics: `alloc`, `free`, `load`, `store`, `copy_bytes`, `size_of`, `align_of`,
* available only in explicitly marked blocks or modules (`unsafe { ... }` or similar),
* everything else in the language stays safe and built on top.

**Decision needed:** spelling, and whether unsafe is per-block, per-function, or per-module.

### M9. OS access: FFI and syscalls
The compiler has to read source files, write object files or executables, and exit with a status. On the C backend, this is C FFI (`extern` functions). Natively, it is either a small FFI to libc or direct syscalls. **Recommendation:** design `extern` declarations so they mean "call this external symbol using the platform C ABI." That works for both backends, and libc stays an option rather than a requirement.

### M10. A mid-level IR shared by both backends
**Recommendation:** define a mid-level IR before writing the first compiler:
* explicit moves, borrows, drops, scopes, and cleanup edges,
* the ownership checker, borrow checker, and region checker (M3) all run here,
* backend 1: emit C; backend 2 (later): emit native code.

This is the single decision that most reduces the cost of the native backend. The C emitter becomes thin, and porting the compiler to Lowlight means porting a well-defined pipeline instead of an emitter full of C assumptions. It also makes the borrow rules testable independently of C.

### M11. Memory layout must be specified by Lowlight
Today, struct layout, enum representation, closure structs, interface fat pointers, and vtables are "whatever C does." For a native backend and for FFI, define:
* struct field order (declaration order, or reorderable?), size, and alignment,
* explicit layout controls (`packed`, `align(n)`, C-compatible layout for FFI types),
* enum tag size (relates to **C11** explicit enum underlying types),
* layout of `?T` (null-pointer optimisation for heap types vs. a tag),
* layout of strings, vectors, closures, interface values, and vtables.

These can be marked implementation-defined at first, but must be *written down* so both backends agree.

### M12. Concurrency (future, but don't block it)
Not in scope yet. The readers-xor-writer rule is already the foundation for data-race freedom, so avoid decisions that would break it, for example interior mutability without a marker, or global mutable state without a rule (see Part D, modules).

---

## Part B. Inconsistencies & Ambiguities to Unify

### B1. Is `?` required before `.` on non-identifier optionals?
§6.3 says `.` "works uniformly on optional and non-optional values," but its examples write `find_user(id)?.name`. Is `find_user(id).name` legal on a `?User`?

**Recommendation:** `?` is **required** before `.` on any optional expression. For identifiers it is already part of the name (`user?.name`). That way every possible short-circuit is visible, which matches the reasoning behind the `name?` rule.

### B2. Receiver syntax differs between methods and interfaces, and there is no `Self`
* Methods write `(self: Point)` / `(self: &Point)`.
* Interfaces write `(self)` / `(&self)`.
* `Copyable.copy()` must return "the same type," but no type name means that.

**Recommendation:**
* Use `(self, ...)` / `(&self, ...)` in **both** places. The type is already given by `Point.` in `bind Point.length`.
* Add a `Self` type, valid only inside interface definitions.
* Note: an interface whose method returns `Self` (like `Copyable`) can't be used as an interface *value*, because the concrete size is unknown. Until generics exist (**M7**), `Copyable` is effectively a method convention.

### B3. `match`: dangling `else`, what patterns exist, and no `match <-`
* **Dangling `else`.** In `when Status.A: if (c) 1 else Status.B: 2`, does `else` belong to the `if` or the `match`? It is only resolved once the parser reaches `:`.
  * **Recommended rule:** an `else` whose following pattern is immediately followed by `:` always belongs to the innermost `match`.
* **The `else Pattern:` rule** should be stated: the pattern must be **exactly the one remaining variant**. Adding a variant later then breaks that `match` at compile time, which is the point.
* **Pattern language.** Now that destructuring is gone, which patterns are allowed? Suggested: literals, qualified enum variants, alternatives (`when 1, 2, 3:`), and ranges (`when 1..=9:`). If payload enums return (**A3**), this needs revisiting.
* **`match <-`.** `if <-` exists but `match <-` doesn't, so a nested block inside a match arm can't exit the `match` early. **Recommendation:** add it for symmetry.

### B4. `for`: `else` means different things depending on the body
* With a value-producing body, `else` only runs when there were 0 iterations.
* With a search body, `else` runs when the loop never hit `for <-`.

**Way 1** (**0.2**) makes this a single rule: *the loop's value is the `for <-` value, otherwise the last iteration that produced a value, otherwise `else` / `none`.* A search body's iterations produce no value, so it falls through to `else` naturally. **This is a strong argument for Way 1.**

Two smaller gaps:
* **A body ending in `if (c) x`** (type `?T`): is a `none` iteration "no value," or a value of `none`?
* **No way to continue an outer loop.** `outer <-` breaks the labeled loop, but nothing continues it. Suggested: `next outer <-`.

### B5. `func_name <- value` is redundant
Unwinds can't cross function boundaries, so the only function you can ever unwind is the innermost one, and that is exactly what `func <-` does. `func_name <-` adds a name-resolution corner case (what if the name is shadowed?) and nothing else.

**Recommendation:** remove it.

### B6. `Name[...]` could be a struct literal or an index, plus missing field defaults
* Is `Point[x: 1]` a struct literal or indexing a variable named `Point`?
  * **Recommended syntactic rule:** `Ident[ name: ...]` is a struct literal; anything without a top-level `name:` is an index. This means **no positional struct literals** and no `Point[x, y]` shorthand.
* **Field defaults.** Your seed sample had them (`b: float = 8.9`), but the spec never picked them up.
  * **Recommendation:** allow default field values, and let optional fields be omitted, in which case they are `none`.

### B7. What does a distinct type (`define UserID int`) inherit?
§8.4 says it can "override" built-in methods, which implies it inherits something.

**Recommendation:**
* It inherits the base type's operators, closed over the new type: `UserID + UserID → UserID`, while `UserID + int` is an error.
* Literals adapt to it: `let id: UserID = 42`.
* It gets its own built-in `to_string` / `copy`, which it can override.
* It does **not** inherit methods that users added to `int`.

Distinct types are also a natural fit for pool handles and arena indices (`define NodeId Handle`), which keeps handles from different pools from being mixed up.

### B8. How do you write the type of a void function?
`(int): bool` works. A callback that returns nothing has no spelling. `(int)` collides with a parenthesized type.

**Options:**
* **(a)** `(int)` means "function taking int, returning nothing," and parentheses are banned in type position. They are only used in `?(?T)`, which flattens anyway.
* **(b)** Add `void`, allowed only in function types: `(int): void`.

**Lean:** (a).

### B9. Stack arrays vs. vectors as parameters, and compile-time lengths
* Can a read-only `(xs: [int])` parameter accept a `[3: int]` stack array or a sub-range? **Recommendation:** yes. A read-only `[T]` parameter is a view of any contiguous sequence. `&[T]` (which can `push`) only accepts vectors.
* The `N` in `[N: T]` must be known at compile time. Is `bind SIZE = 100` (at top level, initialized with a literal) a compile-time constant?
* **Slices as locals.** Because read-only `[T]` parameters are already views, consider allowing a *local, non-storable* slice binding (`let s = xs[2..5]` as a view that can't escape the scope). Tokenizers and parsers rely heavily on this. It fits second-class references as long as the view can't be stored or returned.

### B10. No reserved keyword list
v0.1 had one, and later drafts dropped it.

**Proposed list:** `let bind define struct enum interface if else for in match when next func none true false self Self`, plus whatever comes out of A1/M1 (`own`), A5, M3 (`ref`, region syntax), and M8 (`unsafe`).

---

## Part C. Rules That Should Be Written Down (proposed defaults; say if you disagree)

| # | Gap | Proposed rule |
|---|---|---|
| C1 | `T` into a `?T` slot | Wraps implicitly (`let x?: ?int = 5`). A bare `none` needs type context. |
| C2 | Parameter rebinding | Read-only parameters are immutable bindings, like `bind`, even for primitives. |
| C3 | Reassigning a heap `let` | The old value is dropped first. Temporaries are dropped at the end of the statement. |
| C4 | Conditional moves | Using a variable that was moved on only *some* paths is a compile error. Moving inside a loop body is an error unless the variable is reassigned before the next iteration. |
| C5 | Borrow conflicts | `foo(&x, x)` is an error. `list.push(list[0])` is an error for heap elements (fine for plain ones). Changing a collection while a `for` iterates it is an error. |
| C6 | Discarded results | A non-void result used as a statement is dropped silently, and may warn. |
| C7 | Divergence | `func <-`, `for <-`, and loops with no exit count as "never returns" for the exhaustive flow check. |
| C8 | Auto-boxing edges | Non-optional self-recursion (`child: Node`) is a compile error. Mutual recursion (A holds `?B`, B holds `?A`) boxes the fields that close the cycle. The boxes use the owner's allocator (**M6**). |
| C9 | String literals | A literal passed to a read-only `string` parameter is a static view with no allocation. A literal in an owning position allocates. |
| C10 | Interpolation | Interpolating a value whose type isn't `Printable` is a compile error. |
| C11 | Enum discriminants | Allow explicit values and an underlying type (`define Flags enum u8 [ A = 1, B = 2, ]`), for C interop, bit flags, and specified layout (**M11**). |
| C12 | `if <- value` in a braceless `if` | `if (c) <- val` unwinds the nearest `{}`. This was decided in Round 1 but dropped from the text. |
| C13 | Map iteration order | Unspecified, or insertion order? (Insertion order costs a little memory.) A self-hosted compiler benefits from **deterministic** order, so its output is reproducible. |
| C14 | Drop order | Locals drop in reverse declaration order at scope exit and on every unwind. Struct fields drop in declaration order (or reverse; pick one). See **M4**. |

---

## Part D. Deferred to Upcoming Phases (logged so they aren't lost)

> [!NOTE]
> Given the self-hosting goal, the recommended phase order is: **memory-model completion (Part M) → generics → errors → modules → FFI/unsafe → strings/numerics → standard library.** Items promoted into Part M are marked.

* **Errors:**
  * Out-of-bounds indexing (parked). Trap, or optional checks (see **M0** note)?
  * Integer overflow. Signed overflow is **undefined behavior in C**, so Lowlight must define it: wrap or trap. (Also in **Part N**.)
  * Division by zero.
  * Panics / abort. What runs on panic: unwind with drops, or immediate abort?
  * `parse_int`-style fallible functions. A compiler is full of fallible operations, so the error model needs to be pleasant.
* **Generics:** **promoted to M7.**
  * `.map` / `.filter`.
  * `Copyable` / `Printable` for `[T]` depending on `T`.
  * Hashable map keys.
  * Functions that take any `[T]`.
* **Modules:**
  * Program entry point (`main`?).
  * Which declarations are allowed at top level.
  * Mutable globals.
  * Whether declaration order matters.
  * Separate compilation and incremental builds, which matter once the compiler compiles itself.
* **C FFI / unsafe:** **largely promoted to M5, M8, M9.**
  * User-defined cleanup (`Drop`): see **M5**.
  * `extern` functions: see **M9**.
* **Strings / numerics:**
  * Full escape list and `\u{...}`.
  * Exponent float literals.
  * Mixing numeric types (`int + i32`: no implicit widening?).
  * Indexing and slicing strings (byte index vs. code point).
  * Fixed-width integers (`u8`…`u64`, `i8`…`i64`) and a byte type are required for writing binaries.
* **Stdlib / prelude:** `print`, `log`, `sqrt`, `abs`, and `parse_int` all appear in examples but are never defined. Arenas, pools, and allocators (**M2**, **M3**, **M6**) join this list.

---

## Part N. "C-isms" Checklist (New in v0.7)

Behaviour Lowlight currently gets "for free" from C, which must be defined by Lowlight itself so the native backend matches the C backend. Each needs an explicit decision, even if the answer is "same as C."

| # | Behaviour | Why it matters | Suggested default |
|---|---|---|---|
| N1 | Signed integer overflow | UB in C | Trap in debug, wrap in release, plus explicit `wrapping_add` etc. (or always trap) |
| N2 | Shifts ≥ bit width, shifts of negatives | UB / implementation-defined in C | Define (mask the shift amount, or trap) |
| N3 | Width of `int`, `float` | Varies in C | `int` = 64-bit, `float` = 64-bit |
| N4 | Evaluation order of arguments and operands | Unspecified in C | Left to right |
| N5 | Uninitialised memory | UB in C | Forbidden; every variable must be initialised |
| N6 | Struct padding and field order | Implementation-defined in C | See **M11** |
| N7 | `char` signedness and meaning | Implementation-defined in C | `char` is a Unicode scalar; bytes are `u8` |
| N8 | String representation | C uses NUL-terminated strings | Pointer + length + capacity; NUL-termination only at the FFI boundary |
| N9 | Integer division and modulo of negatives | Truncation toward zero in C99 | State it explicitly |
| N10 | Float behaviour (NaN, `-0.0`, rounding) | Mostly IEEE 754 in practice | IEEE 754, stated |
| N11 | Pointer and integer casts | Implementation-defined in C | Unsafe-only (**M8**) |
| N12 | Aliasing assumptions (`restrict`, strict aliasing) | C optimisers depend on them | Borrow rules give the guarantees; emit `restrict` only where proven |
| N13 | Stack overflow | Undefined in C | Guard page and abort, stated |
| N14 | Program entry, exit codes, and args | C `main` conventions | Define a Lowlight `main` signature (modules phase) |

---

## Part E. Housekeeping (no design decisions involved)

* [PROJECT_SUMMARY.md](file:///Users/thekingoflimbs/Developer/Lowlight/PROJECT_SUMMARY.md) is stale. It still describes v0.2: `&-`, payload enums, `(a int)` parameters. I'll refresh it once this round settles, and add the native/self-hosting goal to it.
* Principle 1 in the spec says Lowlight "transpiles directly to C." Once you approve **M0**, it should be reworded to say C is the first backend, and that the language's semantics are defined independently of C.
* Principle 3 says loops "omit return types." Loops don't have return types; that sentence needs rewording.
* §3 should formally state the `let name: Type = ...` annotation syntax. Right now it only shows up in examples.
* The description of `next <- value` in the §11.3 table depends on the Way 1/2 decision (**0.2**).
* The previous separate audit file ([v0.6_spec_audit.md](file:///Users/thekingoflimbs/Developer/Lowlight/version_history/critique/v0.6_spec_audit.md)) is now archived only. This document replaces both it and the v0.6 critique.
