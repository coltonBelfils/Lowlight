# Lowlight Language Specification (v1.0)

Lowlight is a lightweight, strictly-typed, expression-oriented low-level systems programming language. It combines ergonomic high-level constructs with predictable stack allocation, compile-time ownership and borrowing, region-based arenas, targeted control-flow unwinding, generics via monomorphization, explicit raw-memory `unsafe` blocks, and C ABI interoperability.

Lowlight's semantics are defined completely independently of C. The reference bootstrap compiler lowers Lowlight through a typed Mid-Level IR (MIR) to portable ISO C11 (and native executables), designed as a clean stepping stone toward a C implementation and a fully self-hosted native Lowlight compiler.

---

## 1. Core Philosophy & Design Principles

1. **Lightweight Syntax, Low-Level Power, Backend-Independent Semantics**:
   Lowlight uses a small 27-keyword grammar with zero lifetime annotations, no garbage collector, no runtime reference counting, and no copy-on-write. Every memory layout, integer operation, evaluation order, and drop point is deterministically specified by Lowlight itself rather than inherited from C undefined behavior.
2. **Single Ownership, Default Read-Only Borrowing, Explicit `.copy()`, `&`, and `own`**:
   * Plain data types (primitives, enums, raw pointers, arena references, and structs/stack-arrays composed purely of plain fields without `Drop`) copy implicitly by value.
   * Resource (heap-owning or `Drop`) types have a **single owner** and **move** on variable initialization, reassignment, struct/collection construction, and return.
   * Function parameters expose three transparent modes:
     * **Read-only borrow** `(x: T)` -> called as `f(x)` (effortless zero-copy default; caller keeps ownership).
     * **Mutable borrow** `(x: &T)` -> called as `f(&x)` (mutation visible at both declaration and call site).
     * **Ownership transfer** `(x: own T)` -> called as `f(own x)` when moving a named Resource variable (invalidation visible at call site; temporaries and plain values need no marker).
   * Deep duplication is always explicit via `.copy()`.
   * References are **second-class scoped borrows** (never stored in structs/collections or returned from functions), eliminating lifetime annotations. Typed `Arena` regions (`ref[T]`) provide the single controlled, compile-time-checked exception for cyclic graphs and ASTs.
3. **Expression-Oriented Control Flow with Strict Statement Separation**:
   Executable constructs—blocks (`{ ... }`), conditionals (`if`/`else`), loops (`for`), and pattern matches (`match`)—are expressions that evaluate to values (or execute as `void` in statement position). Variable and type declarations (`let`, `bind`, `define`), assignments (`=`, `+=`, etc.), and targeted unwinds (`<-`) are strictly statements that never produce values.
4. **Unambiguous Delimiter Roles (`{}` vs. `[]` vs. `()`)**:
   * `{ ... }` is **strictly reserved** for executable block expressions (`{ ... }` and `unsafe { ... }`).
   * `[ ... ]` is **strictly reserved** for types, generic parameters, data structures, and structural indexing/slicing: collection literals (`[1, 2, 3]`, `[100: 0]`, `#[...]`), struct literals (`Point[x: 1, y: 2]`), type definitions (`define ... struct [...]`, `enum [...]`, `interface [...]`), generic parameters (`Pair[A, B]`, `identity[int](5)`), indexing (`v[0]`), and slicing (`s[0..5]`).
   * `( ... )` is used for parameter and argument lists, function types (`(int, int): int`, `(Event)`), control-flow conditions, type conversions (`float(x)`, `Printable(x)`), and expression grouping.
5. **Compile-Time Exhaustiveness & Null Elimination**:
   Safe Lowlight has no raw null pointers. Absence of a value is modeled explicitly via Optionals (`?T`) and enforced visually in identifiers: any variable, parameter, or struct field holding `?T` must end with `?` (`user?`), guaranteeing that any bare identifier (`user`) holds a valid unwrapped value. Every execution path in a value-producing block or function must yield a compatible value or diverge.

---

## 2. Lexical Structure, Keywords & Semicolon Rules

### 2.1 Reserved Keywords
Lowlight reserves **27 keywords**:
```text
let       bind      define    struct    enum      interface
if        else      for       in        match     when
next      func      none      true      false     self
Self      own       and       or        not       as
unsafe    extern    import
```
Predeclared built-in types and universe symbols (`int`, `i8`, `i16`, `i32`, `i64`, `u8`, `u16`, `u32`, `u64`, `float`, `f32`, `bool`, `char`, `string`, `ptr`, `ref`, `Arena`, `Copyable`, `Printable`, `Drop`) reside in the outer universe scope.

### 2.2 Identifiers & Optional Identifier Suffix (`name?`)
* Standard identifiers match `[A-Za-z_][A-Za-z0-9_]*`.
* Optional identifiers append a single trailing `?` with no intervening whitespace: `[A-Za-z_][A-Za-z0-9_]*\?` (e.g., `user?`, `next?`).
* `user` and `user?` are distinct identifiers.

### 2.3 Automatic Semicolon Insertion
Semicolons (`;`) are optional; newlines terminate statements deterministically based on the **innermost enclosing delimiter**:
1. **When the innermost enclosing delimiter is `(` or `[`** (or inside `${ ... }` string interpolation): Newlines are normal whitespace and do not insert semicolons (while triggering the multiline trailing-comma rule in §7.5).
2. **At top level or when the innermost enclosing delimiter is `{`** (even if that `{ ... }` block is nested inside `(...)` or `[...]`): A newline inserts `;` **if and only if** the last token before the newline is:
   * An identifier (`x`, `foo`, `user?`, `self`, `Self`)
   * A literal (`42`, `0xFF`, `0b1010`, `3.14`, `"hi"`, `'a'`, `true`, `false`, `none`)
   * A closing delimiter: `)`, `]`, or `}`
   * A built-in type name (`int`, `u8`, `float`, `string`, `bool`, `char`, etc.)
   * A bare unwind operator `<-`
3. **Next-Line Continuation Exceptions**: Even when Rule 2 applies, `;` is **not** inserted if the first non-comment token on the following line is `else`, `when`, `.`, or `?.`.
4. **Same-Line Statements**: Multiple statements on the same line are separated by `;`:
   ```lowlight
   let a = 1; let b = 2
   ```

### 2.4 Comments
* **Single-line comments**: `//` to end of line.
* **Multi-line comments**: `/* ... */`.

---

## 3. Variable Declarations, Mutability & Closures

### 3.1 `let` vs `bind` (Deep Immutability)
Syntax: `let name [: Type] = expr` or `bind name [: Type] = expr`. Every variable must be initialized at declaration.
* **`let` (Mutable)**: Allows reassignment and in-place field/element mutation. Reassigning a `let` variable holding a Resource type drops its previous value before storing the new one.
* **`bind` (Deeply Immutable)**: Cannot be reassigned, cannot have its fields or elements mutated, and cannot be borrowed mutably with `&`—applying uniformly to primitives, structs, strings, vectors, arrays, maps, sets, and arenas.
* Top-level `bind` declarations initialized with compile-time scalar constants (`bind BUFFER_SIZE = 256`) may be used in compile-time constant contexts such as fixed stack array lengths `[BUFFER_SIZE: u8]`.

### 3.2 Variable Shadowing
Both `let` and `bind` may shadow an existing binding in the same scope or an inner scope. If a shadowed binding in the same scope owns a Resource, the shadowed value remains live until scope exit and drops in reverse declaration order.

### 3.3 Function Types & Capturing Lambdas
* **Function Type Syntax**:
  * Value-returning: `(int, int): int` or with optional documentation names `(a: int, b: int): int`.
  * Void-returning: `(int, string)`, `(e: Event)`, or `()` (no `: ReturnType` suffix; parentheses are never used for grouping types).
* **Static vs. Reassignable Functions**:
  * Top-level `bind add = (a: int, b: int): int a + b` emits a direct static function.
  * Local or `let` function variables (`let op: (int, int): int = add`) hold callable closure values that can be passed, stored in structs, returned, and reassigned.
* **Closure-as-Anonymous-Struct Rule**:
  Anonymous functions `(params)[: Ret] body` may capture bindings from enclosing scopes:
  1. Plain captured values are **copied** by value into the closure's environment.
  2. Resource (Move) captured values **move** into the closure's environment (use a pre-copied binding `bind snap = list.copy()` if the outer scope also needs the value).
  3. All captured bindings are deeply immutable (`bind`) inside the lambda body. External state mutation uses explicit `&` parameters.
  4. Closures never capture borrowed references, so closures are self-contained owned values whose environments drop deterministically at scope exit.

---

## 4. Primitive Types, Literals & Strings

### 4.1 Primitives & Numerics
* **Signed Integers**: `int` (64-bit signed, alias of `i64`), `i8`, `i16`, `i32`, `i64`.
* **Unsigned Integers**: `u8` (byte), `u16`, `u32`, `u64`.
* **Floating-Point (IEEE-754)**: `float` (64-bit, alias of `f64`), `f32`.
* **Boolean**: `bool` (`true`, `false`). Conditions in `if` and `for` require `bool` (no implicit integer/pointer truthiness).
* **Character**: `char` (32-bit Unicode scalar value, `uint32_t`), written in single quotes (`'a'`, `'\n'`, `'\r'`, `'\t'`, `'\\'`, `'\''`, `'\0'`, `'\x7F'`, `'\u{1F680}'`).
* **Numeric Literals & Contextual Adaptation**:
  * Formats: decimal (`42`, `1_000_000`), hexadecimal (`0xFF_00`), binary (`0b1010_0101`), floats (`3.14`, `1.5e-3`, `2e6`).
  * Untyped integer literals default to `int` (`i64`); untyped float literals default to `float` (`f64`).
  * When a target numeric or distinct-numeric type is known from context (`let b: u8 = 255`, `port_listen(8080)`), untyped literals adapt automatically if in range at compile time.
  * No implicit numeric widening or mixing (`i32 + int` is a compile error); conversions use explicit call syntax: `float(x)`, `u8(n)`, `int(ch)`.

### 4.2 Strings & String Interpolation
* **`string` Layout & Ownership**:
  UTF-8 encoded byte sequence `{ data, len, cap }`.
  * In an **owning** position (`let s = "Hello"`), allocates an owned heap buffer (always NUL-terminated at `data[len]` for zero-cost C FFI).
  * In a **read-only parameter** position (`println("Hello")`), passes a zero-allocation static view (`cap = 0`).
* **Escapes & Interpolation**:
  * Escapes: `\n`, `\r`, `\t`, `\"`, `\\`, `\0`, `\$`, `\xHH`, `\u{HHHH}`.
  * `$ident` interpolates a single non-optional variable (`"Hello, $name!"`; in `"Ready $ok?"`, `$ok` is interpolated and `?` is literal).
  * `${expr}` interpolates any expression whose type implements `Printable` (has `.to_string(): string`).

---

## 5. Operators & Evaluation Order

### 5.1 Precedence Table (Highest to Lowest)

| Precedence | Category | Operators | Associativity |
| :---: | :--- | :--- | :---: |
| 1 | Postfix | `x.f`, `x?.f`, `f(...)`, `a[i]`, `a[lo..hi]`, `a[lo..=hi]` | Left |
| 2 | Prefix Unary | `-x`, `not x`, `~x`, `&x` (borrow), `own x` (move), `*p` (unsafe deref) | Right |
| 3 | Multiplicative | `*`, `/`, `%` | Left |
| 4 | Additive | `+`, `-` (includes `string + string`) | Left |
| 5 | Bitwise Shift | `<<`, `>>` | Left |
| 6 | Bitwise AND | `&` | Left |
| 7 | Bitwise XOR | `^` | Left |
| 8 | Bitwise OR | `\|` | Left |
| 9 | Comparison | `==`, `!=`, `<`, `<=`, `>`, `>=` | Non-assoc / Left |
| 10 | Logical AND | `and` (short-circuiting on `bool`) | Left |
| 11 | Logical OR | `or` (short-circuiting on `bool`) | Left |
| 12 | Optional Fallback | `??` (short-circuiting; right-associative) | Right |

### 5.2 Compound Assignment Statements
`+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`, `>>=` are **statements** requiring a mutable `let` or `&` target place (`x += 1`, `s += " suffix"`).

### 5.3 Defined Arithmetic & Evaluation Semantics
* **Evaluation Order**: Strictly left-to-right across binary operands, function arguments, struct literal fields, and collection elements.
* **Integer Overflow**: Both unsigned (`u8`..`u64`) and signed (`int`, `i8`..`i64`) `+`, `-`, `*`, and unary `-` use deterministic two's-complement wrapping (never C undefined behavior).
* **Division & Modulo**: Truncate toward zero. Division or modulo by `0` traps deterministically (`MIN / -1` wraps cleanly to `MIN` without UB).
* **Shifts**: `>>` is arithmetic on signed integers and logical on unsigned integers. Shift counts `< 0` or `>= bit_width` trap deterministically (or mask by `bit_width - 1` under `--release`).

---

## 6. Ownership, Borrowing & Lifetime-Free Memory Safety

### 6.1 Plain (Copy) vs. Resource (Move) Classification
* **Plain Types**: Primitives, `enum`s, distinct types over plain types, `ptr[T]`, `ref[T]`, non-heap-capturing function values, `[N: T]` of plain `T`, non-boxed `?T` of plain `T`, and `struct`s (without `Drop`) whose fields are all Plain. Plain values copy implicitly.
* **Resource (Move) Types**: `string`, `[T]`, `#[T]`, `#[K: V]`, `Arena`, owned `Interface` values, heap-boxed recursive `?T`, structs implementing `Drop`, heap-capturing closures, and any composite containing a Resource type.

### 6.2 Move Semantics & Place Rules (Model 2A)
* Initializing a variable (`let b = a`), reassigning (`b = a`), placing a value in a struct/collection (`Order[customer: c]`, `[s]`), or returning a local variable **moves** ownership if the type is a Resource type, invalidating the source local variable.
* Using a moved variable is a **Compile-Time Error**.
* Moving a variable on only one branch of a conditional and reading it afterward, or moving an outer variable inside a loop without reassigning it before the next iteration, is a **Compile-Time Error**.
* Moving a Resource out of an indexed element (`users[0]`) or borrowed struct field is a **Compile-Time Error**; use `.copy()` or ownership-extracting methods (`v.pop()`, `v.remove(i)`, `v.swap_remove(i)`, `m.remove(k)`).

### 6.3 Parameter Modes (`T`, `&T`, `own T`) & Reborrowing
1. **Read-Only Borrow (`x: T`, called as `f(x)`)**:
   Default parameter mode.Read-only parameters are immutable (`bind`) inside the callee and never drop the caller's value.
2. **Mutable Borrow (`x: &T`, called as `f(&x)`)**:
   Requires a mutable receiver (`let` variable or existing `&T` parameter) and explicit `&` at direct function call sites (`push(&list, 4)`). UFCS dot-method calls (`list.push(4)`) auto-borrow `&list` after verifying `list` is mutable; namespaced method calls (`Point.translate(&pt, 1, 2)`) require `&`.
3. **Consume / Move (`x: own T`, called as `f(own x)` or `f(rvalue)`)**:
   Transfers ownership into the callee (where `x` becomes an owned local dropped by the callee unless moved onward). Moving a named Resource variable requires `own` at the call site (`sink(own buf)`); temporaries (`sink(make_buf())`) and Plain values (`nums.push(n)`) require no marker.
4. **Reborrowing & Exclusivity**:
   * Nested references (`&&T`) do not exist; `&x` on `x: &T` reborrows `x` mutably for the call, and passing `x: &T` to `y: T` reborrows `x` read-only.
   * Passing overlapping mutable and shared borrows in the same call (`foo(&x, x)`) or mutating a collection while iterating over it in `for` is a **Compile-Time Error**.
   * Borrowed references cannot be stored in structs/collections or returned from functions.

### 6.4 `Copyable` & `Drop`
* Built-in types implement `.copy()`. Structs whose fields all support `.copy()` automatically synthesize `.copy()` unless overridden via `bind MyStruct.copy = (self): MyStruct { ... }`.
* User types needing custom teardown (file descriptors, sockets, custom buffers) implement `Drop` by defining `bind MyType.drop = (&self) { ... }`. Locals drop in **reverse declaration order** at scope exit and along every `<-` unwind path; struct fields drop in **reverse declaration order** after `MyType.drop`.

---

## 7. Optionals & Non-Value Safety

### 7.1 `?T`, Flattening, `name?` & Recursive Auto-Boxing
* Nested optionals `?(?T)` flatten automatically to `?T`.
* `T` implicitly wraps to `?T` where `?T` is expected (`let x?: ?int = 42`); bare `none` adapts to any known `?T`.
* **Mandatory `?` Suffix Rule**: Every variable, parameter, or struct field has an optional type `?T` **if and only if** its identifier ends with `?` (`let user? = find_user(id)`).
* **Recursive Struct Auto-Boxing**: Direct (`next?: ?Node`) or mutually recursive optional struct fields closing a type cycle are automatically heap-boxed (`Node*` in C, `NULL` for `none`) and freed recursively on drop. Non-optional value cycles (`next: Node`) are compile-time errors.

### 7.2 Narrowing (`if (x?)`, `if (&x?)`, `if (expr as x)`), Chaining (`?.`) & Fallback (`??`)
1. **Identifier Narrowing**:
   * `if (user?) { ... }` checks presence (`has_value`, even on `?bool`) and binds non-optional `user` as a read-only borrow of `user?` inside the `if` branch (`user?` cannot be reassigned while borrowed).
   * `if (&user?) { ... }` (requires `let user?`) binds `user` as `&T` for in-place mutation of the inner value.
2. **Expression Narrowing (`as`)**:
   * `if (expr as val) { ... }` checks presence on any optional expression (`if (find_user(id) as u)`, `if (ages["Alice"] as age)`) and binds unwrapped `val: T` for the `if` branch.
3. **Mandatory `?` Dot Chaining**:
   * Accessing a field or method on an optional expression requires `?` before `.`: `user?.name` (identifier already ends in `?`), `find_user(id)?.name`, `u?.address?.city`. Short-circuits to `none` (`?ResultType`).
4. **Fallback (`??`)**:
   * `opt_expr ?? fallback_expr` evaluates to unwrapped `T` if present, otherwise `fallback_expr`.
   * In variable declarations and assignments, the top-level right-hand side of `??` may be an unwind statement: `bind user = find_user(id) ?? func <- none`.

### 7.3 Non-Value Block Safety
Blocks, `void` functions, and `void` loops that do not produce a value cannot be assigned to variables or used in value expressions (`let x = { let a = 1 }` is a compile-time error).

---

## 8. Compound Data Structures & Slicing

### 8.1 Dynamic Vectors (`[T]`), Stack Arrays (`[N: T]`) & Slices
* **Dynamic Heap Vector (`[T]`)**: `let v = [1, 2, 3]`, `let empty: [int] = []`. Supports `.push(own val)`, `.pop(): ?T`, `.remove(idx): T`, `.swap_remove(idx): T`, `.clear()`, `.reserve(n)`, `.len(): int`, `.cap(): int`, `.is_empty(): bool`, `.get(idx): ?T`, `.contains(val): bool` (when `T` supports `==`), `v[idx]`, and `[string].join(sep: string): string`.
* **Fixed Stack Array (`[N: T]`)**: Compile-time constant length `N`:
  * Repeated value: `let buf = [256: 0]` (`[256: int]`).
  * Explicit elements: `let rgb: [3: u8] = [255, 128, 0]`.
* **Sequence Views & Slicing (`xs[lo..hi]`, `xs[lo..=hi]`)**:
  * A read-only `[T]` parameter accepts `[T]` vectors, `[N: T]` stack arrays, and slices `xs[lo..hi]` as a zero-allocation contiguous view. Mutable `&[T]` parameters require a mutable `[T]` vector.
  * Slicing `xs[lo..hi]` or `s[lo..hi]` in a read-only parameter call passes a zero-copy view; in an owning position (`let sub = s[0..5]`), it allocates an owned sub-string or sub-vector.

### 8.2 Deterministic Insertion-Order Sets (`#[T]`) & Maps (`#[K: V]`)
Keys/elements of sets and maps may be any integer, `bool`, `char`, `string`, `enum`, `ptr[T]`, or distinct type over a hashable base. Both preserve **deterministic insertion order** during iteration:
* **Set (`#[T]`)**: `let seen = #[1, 2, 3]`, `let empty: #[string] = #[]`. Methods: `.insert(own v): bool`, `.has(v): bool` (alias `.contains(v): bool`), `.remove(v): bool`, `.clear()`, `.len(): int`, `.is_empty(): bool`, `.to_vec(): [T]`.
* **Map (`#[K: V]`)**: `let ages = #["Alice": 30]`, `let empty: #[string: int] = #[]`.
  * `ages["Diana"] = 28` or `ages.insert(own k, own v)` inserts/updates.
  * `ages["Alice"]` evaluates to `?V` (cloning `V` if moved into an owned variable, or borrowing when unwrapped via `if (ages["Alice"] as a)`).
  * Methods: `.has(k): bool` (alias `.contains(k): bool`), `.remove(k): ?V`, `.clear()`, `.len(): int`, `.is_empty(): bool`, `.keys(): [K]`, `.values(): [V]`.

### 8.3 Trailing Comma Rule
Multiline comma-separated lists inside `[...]` and `(...)` **require** a trailing comma after the final item; single-line lists allow an optional trailing comma.

---

## 9. Type Definitions (`define`), Generics, Methods & Interfaces

### 9.1 Structs & Default Fields
```lowlight
define Config struct [
    host: string = "127.0.0.1",
    port: int = 8080,
    cert_path?: ?string, // Optional fields implicitly default to `none`
]
let cfg = Config[port: 9000]
```
`define packed Header struct [...]` emits an unpadded packed struct layout. Struct literals always require named fields `TypeName[field: expr, ...]` (empty structs allow `Unit[]`).

### 9.2 Enums
```lowlight
define Perms enum u8 [
    Read = 1,
    Write = 2,
    Exec = 4,
]
```
Enums are distinct types with qualified variants (`Perms.Read`), optional underlying integer type (`int` by default), optional explicit integer discriminants, explicit integer conversion `int(Perms.Read)` / `u8(Perms.Read)`, and built-in `==`, `.copy()`, and `.to_string()`.

### 9.3 Interfaces & Owned Interface Values
```lowlight
define Shape interface [
    area: (self): float,
    scale: (&self, factor: float),
]
```
* **Implicit Structural Satisfaction**: Any type (struct, enum, distinct type, or built-in type) defining matching methods satisfies the interface automatically.
* **Self-Referential Interfaces (`Self`)**: Interfaces using `Self` (such as `define Copyable interface [ copy: (self): Self, ]`) serve as compile-time generic constraints.
* **Borrowed vs. Owned Interface Values**:
  * Parameters `(s: Shape)` and `(s: &Shape)` pass zero-allocation stack fat pointers.
  * Explicit conversion `Shape(my_circle)` allocates an owned boxed interface value (storing `Shape` in local variables, struct fields, or heterogeneous vectors `[Shape]`, and dropping the boxed concrete value at scope exit).

### 9.4 Distinct Derived Types
`define UserID int` creates a distinct nominal type that inherits `BaseType`'s operators closed over `UserID` (`UserID + UserID -> UserID`), literal adaptation (`let id: UserID = 7`), `.copy()`, and `.to_string()`, while requiring explicit `UserID(x)` / `int(id)` conversions and keeping custom methods isolated.

### 9.5 Namespaced Methods
Methods are bound statically with `bind`:
```lowlight
bind Point.length = (self): float {
    sqrt(float(self.x * self.x + self.y * self.y))
}
bind Point.translate = (&self, dx: int, dy: int) {
    self.x += dx
    self.y += dy
}
```
Receivers may be `(self)` (or `self: Type`), `(&self)` (or `self: &Type`), or `(own self)` (or `self: own Type`).

### 9.6 Generics (`[T]` Monomorphization)
Structs, `bind` functions, and methods support type parameters with optional interface bounds, compiled via full compile-time monomorphization:
```lowlight
define Pair[A, B] struct [
    first: A,
    second: B,
]

bind Pair[A, B].into_first = (own self): A {
    self.first
}

bind print_items[T: Printable] = (items: [T]) {
    for (item in items) {
        println(item.to_string())
    }
}
```
Type arguments may be supplied explicitly (`Pair[int, string][first: 1, second: "one"]`, `print_items[int]([1, 2])`) or inferred from value arguments (`Pair[first: 1, second: "one"]`, `print_items([1, 2])`).

---

## 10. Control Flow, Pattern Matching & Targeted Unwinding

### 10.1 `if` Expressions
* With `else`: `if (cond) then_expr else else_expr` -> evaluates to `T`.
* Without `else` in value position: `if (cond) then_expr` -> evaluates to `?T` (`none` when `cond` is `false`).
* Without `else` in statement position: executes as `void`.

### 10.2 Unified `for` Loops ("Last Non-Skipped Iteration Wins")
Forms:
* Exclusive / Inclusive Range: `for (i in 0..10)`, `for (i in 0..=10)`
* Sequence (`[T]`, `[N: T]`, `string`): `for (item in list)`, `for (&item in list)`, `for (i, item in list)`, `for (i, &item in list)`
* Set: `for (item in my_set)`
* Map: `for (k, v in my_map)`, `for (k, &v in my_map)`
* Conditional / Infinite: `for (count < 10)`, `for (true)`
* Labeled: `outer: for (...) { ... }` (and labeled blocks `blk: { ... }`)

**Expression Value Rule**:
* `for <- val` terminates the loop immediately with `val`.
* Otherwise, each iteration that completes with a value `v` (or `next <- v` / top-level `<- v`) updates the loop's running result (dropping any prior Resource result first). Bare `next <-` skips the rest of the iteration without overwriting the running result.
* When the loop finishes: if at least one iteration produced a value, that final produced value is yielded; otherwise `else` is evaluated (giving type `T` when `else` is present) or `none` is yielded (giving type `?T` when `else` is omitted).

### 10.3 Pattern Matching (`match`)
Container-free `match` requires a terminating `else` arm:
```lowlight
let category = match (ch)
    when '0'..='9': "digit"
    when 'a'..='z', 'A'..='Z', '_': "ident"
    else: "other"

let code = match (status)
    when Status.Pending: 100
    when Status.Active: 200
    else Status.Closed: 300 // Compile-time verified exhaustive over `Status`
```
Inside `match` arms, an `else` followed by `:` or `<Pattern>:` binds to the innermost `match`.

### 10.4 Targeted Unwinding (`<-`)
`<-` is a statement requiring surrounding whitespace (`<- val`, `func <-`, etc.):

| Syntax | Unwind Target |
| :--- | :--- |
| `<- [val]` | Immediate enclosing `{ ... }` block |
| `if <- val` | Nearest enclosing `if` expression |
| `match <- val` | Nearest enclosing `match` expression |
| `for <- [val]` | Nearest enclosing `for` loop (break with optional value) |
| `next <- [val]` | Current iteration of nearest `for` loop (continue) |
| `label <- [val]` | Enclosing `label: for` or `label: { ... }` |
| `next label <- [val]` | Next iteration of enclosing `label: for` |
| `func <- [val]` | Nearest enclosing function, method, or lambda |

* **Valid Positions**: Standalone statement (including `if`/`match` arm bodies — note `if (c) <- val` unwinds the nearest `{}`) or top-level RHS of `??` in a `let`/`bind` or `=` statement.
* **Function Boundary Rule**: Unwinds cannot cross a function or lambda boundary (compile-time error).

---

## 11. Region Arenas (`Arena` & `ref[T]`)

For cyclic graphs, doubly-linked lists, parent pointers, and zero-fragmentation compiler ASTs without GC or reference counting, Lowlight provides region-checked bump arenas:
```lowlight
define GraphNode struct [
    id: int,
    neighbors: [ref[GraphNode]],
]

let arena = Arena.new()
let a = arena.alloc(GraphNode[id: 1, neighbors: []])
let b = arena.alloc(GraphNode[id: 2, neighbors: []])
a.neighbors.push(b)
b.neighbors.push(a) // Safe cycle! Freed together when `arena` drops.
```
* `Arena` is an owned Resource (`Arena.new()`, `Arena.with_capacity(bytes)`, `arena.alloc(own val): ref[T]`, `arena.reset()`, `arena.bytes_used(): int`). Any Resource fields inside arena-allocated values register their destructors with the arena and run deterministically when `arena` resets or drops.
* `ref[T]` is a Plain, non-null region pointer supporting direct field reads, field writes, and method calls.
* **Compile-Time Region Escape Rules**:
  1. A function cannot return `ref[T]` (or a type containing `ref[T]`) unless the function takes an `&Arena` parameter.
  2. A local variable holding `ref[T]` (or `[ref[T]]`, etc.) cannot be declared in an outer scope that outlives the local `Arena` from which it was allocated.

---

## 12. Low-Level Systems Layer: `unsafe`, Raw Pointers, C FFI & Modules

### 12.1 `unsafe { ... }` & `ptr[T]`
`unsafe { ... }` is a block expression (usable inline or as a function body) enabling raw memory operations and C FFI calls:
* **Raw Pointer Type**: `ptr[T]` (Plain type, compiles to `T*`). Dereference read/write `*p` and intrinsics require `unsafe`:
  * Safe anywhere: `size_of[T](): int`, `align_of[T](): int`
  * Require `unsafe`:
    * `alloc_raw[T](count: int): ptr[T]`, `realloc_raw[T](p: ptr[T], count: int): ptr[T]`, `free_raw[T](p: ptr[T])`
    * `null_ptr[T](): ptr[T]`, `is_null[T](p: ptr[T]): bool`
    * `addr_of(place): ptr[T]`, `ptr_add[T](p: ptr[T], offset: int): ptr[T]`
    * `ptr_read[T](p: ptr[T]): T`, `ptr_write[T](p: ptr[T], val: own T)`
    * `ptr_cast[To](x): ptr[To]` (from another `ptr[From]` or integer address `u64`/`int`), `ptr_addr(p: ptr[T]): u64`
    * `copy_bytes(dst: ptr[u8], src: ptr[u8], n: int)`, `zero_bytes(dst: ptr[u8], n: int)`
    * `str.c_str(): ptr[u8]` (NUL-terminated byte pointer)

### 12.2 External C ABI Declarations (`extern`)
```lowlight
extern "math.h"
extern cos as c_cos: (x: float): float

bind cos = (x: float): float unsafe {
    c_cos(x)
}
```
`extern <c_symbol> [as <local_name>]: (params)[: Ret]` binds a platform C ABI symbol (optionally under a local Lowlight alias `<local_name>` so a module can expose a safe Lowlight wrapper `bind cos = ...` with the canonical name without shadowing or colliding with the C symbol). `extern` functions may only be called inside `unsafe { ... }`.

### 12.3 Modules (`import`) & Program Entry (`main`)
* `import "relative/path.ll"` or `import "std/pool.ll"` brings top-level declarations into scope (deduplicated across diamond imports).
* `import "lexer.ll" as lex` binds an explicit module namespace (`lex.tokenize(...)`).
* Top-level declarations (`define`, `bind`, `extern`) are order-independent across a module.
* Executable programs define `bind main = () { ... }` (exit status `0`) or `bind main = (): int { ... }` (returns OS exit status after running all deterministic drops in `main`).

---

## 13. Mid-Level IR (MIR) & C11 Compilation Pipeline

1. **Frontend (`lexer -> parser -> sema`)**: Enforces semicolon insertion, trailing commas, type inference, optional `name?` invariants, generic monomorphization, ownership/move analysis, borrow exclusivity, arena region containment, and control-flow exhaustiveness.
2. **Mid-Level IR (`ir`)**: Lowers expressions and control flow into explicit basic scopes with concrete monomorphized types, closure structs, interface vtables, explicit variable move-zeroing, and deterministic LIFO `Drop` sequences along every normal exit and `<-` unwind jump.
3. **C11 Backend (`codegen`)**: Emits clean, human-readable ISO C11 paired with `lowlight_runtime.h` (zero GC, zero `setjmp`/`longjmp`, UB-free two's-complement arithmetic, deterministic insertion-order hash tables, bump arenas, and optional leak-verification counters).
