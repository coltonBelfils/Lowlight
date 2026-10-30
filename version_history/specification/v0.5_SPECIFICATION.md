# Lowlight Language Specification (Draft v0.5)

Lowlight is a low-level, strictly-typed, expression-oriented systems programming language featuring deterministic memory management with single ownership and default read-only borrowing, targeted control-flow unwinding, and direct transpilation to C (and subsequently native executables).

---

## 1. Core Philosophy & Design Principles

1. **Low-Level Control with Predictable, Deterministic Memory Management**:
   Lowlight transpiles directly to clean, portable C (C99/C11) with native stack frames and predictable memory layouts. There is no garbage collector and no background reference-counting runtime. While high-level ergonomics such as dynamic strings (`string`), vectors (`[T]`), sets (`#[T]`), and maps (`#[K: V]`) allocate on the heap, their lifetimes are completely deterministic: the compiler automatically emits deallocation calls at scope exit and prior to any control-flow unwind.
2. **Default Read-Only Borrowing, Explicit `.copy()`, & Call-Site Mutation Transparency (`&`)**:
   Every heap-allocated resource has a single owner. Passing a value to a function parameter (`foo(x)`) borrows it read-only by default without moving or destroying the caller's variable, while duplicating data into a new owned value is always explicit via `.copy()`. Mutating a value through a parameter or reference requires `&` both in the declaration (`list: &[int]`) and at the call site (`push(&my_list, 4)`), making every potential state mutation immediately visible. References are strict second-class scoped borrows (never stored in structs/collections or returned from functions), eliminating lifetime annotations and dangling pointers by construction.
3. **Expression-Oriented Control Flow with Strict Statement Separation**:
   Executable constructs—blocks (`{ ... }`), conditionals (`if`/`else`), loops (`for`), and pattern matches (`match`)—are expressions that evaluate to values. Conversely, variable and type declarations (`let`, `bind`, `define`), assignments (`=`), and targeted unwinds (`<-`) are strictly statements that never produce values, preventing C-style assignment-in-condition bugs. Functions and loops that exist solely for side effects omit return types (`void`) and are forbidden from being assigned to variables.
4. **Unambiguous Delimiter Roles (`{}` vs. `[]` vs. `()`)**:
   * `{ ... }` is **strictly reserved** for executable block expressions (a sequence of statements and expressions that either yields a final value or executes as a `void` block).
   * `[ ... ]` is **strictly reserved** for data structures, types, and structural access: collection literals (`[1, 2, 3]`, `#[...]`), struct literals (`Point[x: 1, y: 2]`), type definitions (`define ... struct [...]`, `enum [...]`, `interface [...]`), and collection indexing (`ages["Diana"]`).
   * `( ... )` is used for function parameter and argument lists, control-flow conditions, type conversions (`float(x)`), and expression grouping.
5. **Compile-Time Exhaustiveness & Null Elimination**:
   Raw null pointers do not exist. The possible absence of a value is modeled explicitly in the type system via Optionals (`?T`) and enforced visually in identifiers: any variable, parameter, or field holding an optional value must end with a `?` suffix (`user?`), guaranteeing that any bare identifier (`user`) holds a valid value. In value-producing blocks and functions, the compiler verifies that every possible execution path yields a compatible value.

---

## 2. Lexical Structure & Statements

### 2.1 Semicolons & Go-Style Newline Rules
Semicolons (`;`) are optional; newlines act as statement separators according to the following deterministic rules:
1. **Inside `(...)` and `[...]`**: Newlines are treated as normal whitespace and do not terminate statements (while still triggering the multiline trailing-comma requirement in §7.4).
2. **Inside `{...}` and at top level**: A newline acts as a semicolon (`;`) **if and only if** the last token before the newline is:
   * An identifier (`x`, `foo`, `user?`)
   * A literal (`42`, `1_000`, `0xFF`, `3.14`, `"hi"`, `'a'`, `true`, `false`, `none`)
   * A closing delimiter: `)`, `]`, or `}`
   * A built-in type name (`int`, `u8`, `float`, `string`, `bool`, `char`, etc.)
   * A bare unwind operator `<-` (when used without a value)
3. **Next-Line Continuation Exceptions**: Even when Rule 2 applies, a semicolon is **not** inserted if the first token on the following line is:
   * `else` (allowing multiline `if (...) ... else ...`, `for (...) ... else ...`, and `match` expressions)
   * `when` (allowing multiline `match` arms)
   * `.` (allowing multiline method/field chains such as `items\n .filter(...)\n .map(...)`)
4. **Same-Line Statements**: Multiple statements on the same line must be separated by explicit semicolons:
   ```
   let a = 1; let b = 2
   ```

### 2.2 Comments
* **Single-line comments** begin with `//` and continue to the end of the line.
* **Multi-line comments** are enclosed in `/* ... */`:
  ```
  // Single-line comment
  /*
     Multi-line comment
  */
  ```

---

## 3. Variable Declarations & Mutability

### 3.1 `let` vs `bind` (Deep Immutability)
* **`let` (Mutable)**: Declares a variable that can be reassigned or mutated in place.
  ```
  let x = 10
  x = 20

  let pt = Point[x: 1, y: 2]
  pt.x = 5

  let cache = #["a": 1]
  cache.insert("b", 2)
  ```
* **`bind` (Deeply Immutable)**: Declares a constant binding that can never be reassigned **and** whose contents cannot be mutated—applying uniformly to primitives, structs, strings, vectors, maps, and sets:
  ```
  bind max_size = 100
  max_size = 200 // COMPILE ERROR: cannot reassign a bound variable

  bind pt = Point[x: 1, y: 2]
  pt.x = 5       // COMPILE ERROR: `pt` is deeply immutable

  bind cache = #["a": 1]
  cache.insert("b", 2) // COMPILE ERROR: cannot mutate a `bind` collection
  ```

### 3.2 Variable Shadowing
Both `let` and `bind` declarations may shadow an existing variable of the same name in the same scope or in an inner scope:
```
bind raw = "42"
let val = parse_int(raw) ?? 0
bind val = val + 1 // Shadows mutable `val` with an immutable `bind`
```

### 3.3 Function Variables, Function Types & Capturing Lambdas
Function parameters use `name: Type` syntax. In function type annotations (such as callback parameters or struct fields), parameter names are optional—both `(int, int): int` and `(a: int, b: int): int` are valid:
```
bind apply_op = (a: int, b: int, op: (int, int): int): int {
    op(a, b)
}
```
Functions declared with `bind` emit static functions, while function variables declared with `let` can be reassigned at runtime. Anonymous functions (lambdas) can capture variables from their enclosing scope; captured variables are **copied** into the lambda's environment at creation time so the closure owns its captured data independently:
```
bind static_add = (a: int, b: int): int a + b
let current_op = static_add
let factor = 3
current_op = (a: int, b: int): int (a + b) * factor // Copies `factor` into the closure
```

---

## 4. Primitive Types & Literals

### 4.1 Integers, Floats, Booleans & Characters
* **`int`**: Default signed integer type, aliased to `i64` (`int64_t` in C).
* **Fixed-Width Signed Integers**: `i8`, `i16`, `i32`, `i64`.
* **Fixed-Width Unsigned Integers**: `u8`, `u16`, `u32`, `u64`.
* **Floating-Point Types**: `float` (default, aliased to `f64`) and `f32`.
* **Numeric Literal Inference & Formats**:
  * Untyped integer literals default to `int` (`i64`) and untyped floating-point literals default to `float` (`f64`).
  * When a target type is known from context (such as `let b: u8 = 10` or passing `500` to a `u16` parameter), a numeric literal automatically adapts to that target type if its value fits within range at compile time.
  * Integer literals support decimal (`42`), hexadecimal (`0xFF`), and binary (`0b1010`) notation.
  * Underscores (`_`) may be placed between digits in any numeric literal for visual readability (`1_000_000`, `0xFF_00`, `0b1110_0101`, `3.141_592`).
* **Explicit Type Conversions**:
  Numeric and distinct-type conversions use explicit `TargetType(expr)` call syntax:
  ```
  let x = 10
  let y = float(x)
  let small = u8(x)
  ```
* **Booleans**: `bool` (`true` or `false`).
* **Characters**: `char` (32-bit Unicode scalar value), written with single quotes (`'a'`, `'\n'`, `'\t'`, `'\\'`, `'\''`).

### 4.2 Strings & Interpolation
* **`string` (Dynamic UTF-8 String)**:
  Heap-allocated, dynamically resizable UTF-8 text with length and capacity, enclosed in double quotes (`"..."`). Automatically dropped at scope exit when owned, and passed as a zero-cost read-only view when borrowed as a function parameter `(s: string)`:
  ```
  let s = "Hello\n" // type: string
  ```
* **`&string` (Mutable Borrowed String)**:
  A mutable reference allowing in-place mutation of the caller's string without moving ownership:
  ```
  bind append_tag = (s: &string) {
      s.push(" [TAG]")
  }
  ```
* **Interpolation & Escapes**:
  * Standard escape sequences include `\n`, `\t`, `\"`, `\\`, and `\$`.
  * `$ident` interpolates a single non-optional variable name: `"Hello, $name!"` (Because optional wrappers must be unwrapped before use, bare `$ident` never includes a trailing `?`; in `"Are you $ready?"`, `$ready` is interpolated and `?` is literal text.)
  * `${expr}` interpolates arbitrary expressions (including optionals with fallbacks): `"Total: ${price * count}"`, `"User: ${user?.name ?? "Guest"}"`
  * Escaped literal dollar sign: `\$`

---

## 5. Memory Management, Borrowing & Copying

### 5.1 Single Ownership, Default Read-Only Borrowing & Explicit `.copy()`
* Scalar/primitive types (`int`, `float`, `bool`, `char`) are passed and assigned by value.
* All heap-allocated resources (vectors, maps, sets, strings, structs containing them) have a **single owner** responsible for deterministic cleanup at scope exit.
* **Function Parameters Borrow Read-Only by Default**:
  Passing a variable to a standard parameter `(u: User, items: [int])` **borrows** the value read-only for the duration of the call—zero heap copies are made, no marker is needed at the call site (`process(my_user, my_items)`), and the caller's variables remain valid after the call returns.

### 5.2 Mutable Borrowing (`&T`) & Strict Second-Class Reference Rules
To mutate a caller's variable in place, a **Mutable Borrow (`&T`)** is used:
* **Immutable Borrow (Default — `T`)**: Shared, read-only view. Multiple readers allowed concurrently.
* **Mutable Borrow (`&T`)**: Exclusive, read-write view. Exactly one writer allowed; no other readers or writers.
* **Scoped Second-Class Rule**:
  1. Function parameters and local variables may be borrowed views.
  2. **Structs, enums, and collections cannot store borrowed references** as fields or elements; they only store owned values (`T`).
  3. **Functions and methods cannot return borrowed references**. When a function or struct needs to retain or return data from a borrowed parameter, it creates an owned copy via `.copy()`.

#### Mandatory Call-Site `&` Annotation & Method Auto-Borrowing:
* **Direct Function Calls**: The caller must explicitly prefix an argument with `&` when passing a mutable borrow:
  ```
  bind push = (list: &[int], val: int) { ... }

  let my_list = [1, 2, 3]
  push(&my_list, 4) // `&` required at call site for mutation transparency
  ```
* **Method Calls**: Calling a method via dot syntax (`pt.translate(5, 5)`) automatically borrows the receiver (`pt`) as read-only or mutable (`&`) according to the method's `self` parameter (verifying at compile time that `pt` was declared with `let` if `self` is `&Point`). Calling the method using its full namespaced path (`Point.translate(&pt, 5, 5)`) requires explicit `&` at the call site.

### 5.3 Reborrowing (Reference Flattening)
Nested reference types (`&&T`) do not exist in Lowlight. Applying `&` to an existing mutable reference flattens and reborrows the reference for the duration of the call:
1. Writing `&x` when `x` already has mutable borrow type `&T` **reborrows** `x` as `&T` (temporarily lending exclusive mutable access to the callee and restoring it when the call returns):
   ```
   bind push_two = (list: &[int], a: int, b: int) {
       push(&list, a)
       push(&list, b)
   }
   ```
2. Passing `x` (where `x` is `&T`) to a read-only parameter `(list: [int])` reborrows `x` as read-only.
3. Writing `&x` when `x` is a read-only parameter or a `bind` variable is a **Compile-Time Error**.

### 5.4 Built-in `Copyable` Interface & `.copy()`
Lowlight provides a built-in `Copyable` interface requiring a `.copy()` method that takes a read-only receiver `(self)` and returns a new, independent owned value of the same type:
```
// Built-in interface implemented by all built-in types (`int`, `float`, `bool`, `char`, `string`, `[T]`, `[N: T]`, `#[T]`, `#[K: V]`):
bind Team.leader_copy = (self: Team): User {
    self.members[0].copy() // Copies the borrowed member into a new owned `User`
}
```

---

## 6. The Optional System & Non-Value Safety

### 6.1 No Raw Nulls
Raw `NULL` pointers do not exist. Absence of a value is modeled safely through **Optionals** (`?T`).

### 6.2 Optional Types (`?T`), Variable-Name Optionals (`name?`) & Recursive Structs
* The underlying optional type is written `?T` (e.g., `?int`, `?User`).
* **Flattening**: Nested optionals `?(?T)` automatically flatten to `?T`. Optional types may be stored inside collections and other types (e.g., `[?int]`, `#[string: ?int]`).
* **Mandatory `?` Identifier Suffix**: A variable, parameter, or struct field can hold an optional value (`?T`) **if and only if** its identifier ends with a `?`. Conversely, an identifier without a `?` suffix is guaranteed never to hold an optional:
  ```
  let user? = find_user(id) // type: ?User
  let count?: ?int = none   // Explicitly typed optional initialized to none
  ```
* **Distinct Identifiers**: `user` (type `User`) and `user?` (type `?User`) are two distinct identifiers.

Under the hood in C, a non-recursive `?T` transpiles to a tagged stack struct:
```c
typedef struct {
    bool has_value;
    T value;
} Opt_T;
```
* **Automatic Heap Boxing for Self-Referential Optional Fields**:
  When a struct `T` contains a recursive optional field of its own type (such as `next?: ?Node` in a linked list or tree `Node`), the compiler automatically represents `?Node` in C as an owned heap pointer (`Node*`, where `NULL` represents `none`), allocating on assignment and automatically freeing the child node when the parent `Node` is dropped:
  ```
  define Node struct [
      val: int,
      next?: ?Node, // Automatically heap-boxed by the compiler
  ]
  ```

### 6.3 Smart Narrowing, Chaining & Fallbacks
1. **Presence Check & Borrowed Unwrapping in `if`**:
   * `if (user?)` always checks for the **presence** of a value (`has_value`), even if the inner type is `bool` (`if (flag?)`). The optional wrapper must be handled before interacting with the inner value.
   * **Immutable Unwrap (`if (user?)`)**: Inside the `if` branch, the unwrapped value is automatically bound to the non-optional identifier `user` as a **read-only borrow** of `user?` (or copied for primitive scalars), preventing `user?` from being mutated to `none` while `user` is borrowed. If an outer `user` exists in the enclosing scope, it is shadowed inside the `if` branch:
     ```
     if (user?) {
         print(user.name) // `user` immutably borrows the unwrapped value of `user?`
     }
     ```
   * **Mutable Unwrap (`if (&user?)`)**: Prefixing the condition with `&` (when `user?` is declared with `let`) binds `user` as a **mutable borrow** (`&T`) of the inner value inside `user?`:
     ```
     if (&user?) {
         user.name = "Updated" // Mutates the `User` inside `user?` in place!
     }
     ```
2. **Dot Chaining (`.`)**:
   The `.` operator works uniformly on both non-optional and optional values. Accessing a field or method on an optional (`user?.name`, `u?.address?.city`, or `find_user(id)?.name`) short-circuits to `none` (producing `?FieldType`) if the base value is `none`.
3. **Fallback Operator (`??`)**:
   Unwraps an optional if present, or evaluates the right-hand fallback (which may be a default value or an unwind statement):
   ```
   let display_name = user?.name ?? "Guest"
   bind verified_user = user? ?? func <- none
   ```

### 6.4 Non-Value Block Safety
A block expression `{ ... }` produces a value if and only if its final statement is an expression or an early exit yielding a value.
* Attempting to assign a block (or `void` function/loop) that ends in a statement without a value is a **Compile-Time Error**:
  ```
  let x = {
      let a = 1
      let b = 2
  } // COMPILE ERROR: Block does not evaluate to a value
  ```

---

## 7. Compound Data Structures

### 7.1 Vectors vs Stack Arrays
* **Dynamic Heap Vector (`[T]`)**:
  Heap-allocated, dynamically growable:
  ```
  let v = [1, 2, 3] // type: [int]
  v.push(4)
  ```
* **Fixed Stack Array (`[N: T]`)**:
  Allocated directly on the C stack frame with compile-time length `N` and element type `T`. Zero heap overhead:
  * **Repeated-Value Stack Array Literal (`[N: value]`)**: Repeats `value` `N` times on the stack:
    ```
    let buffer = [100: 0] // type: [100: int] (100 zeroes on the stack)
    let trio = [3: 5]     // type: [3: int] (three 5s on the stack: 5, 5, 5)
    ```
  * **Explicit-Element Stack Array (`let name: [N: T] = [...]`)**: Uses an explicit `[N: T]` type annotation with a standard element list:
    ```
    let s: [3: int] = [1, 2, 3] // Stack array of 3 elements: 1, 2, 3
    ```

### 7.2 Maps & Sets
* **Set Literal (`#[T]`)**:
  ```
  let visited = #[1, 2, 3]
  visited.insert(4)
  if (visited.has(2)) { ... }
  visited.remove(1)
  ```
* **Map Literal (`#[Key: Value]`)**:
  ```
  let ages = #["Alice": 30, "Bob": 25]
  ages.insert("Charlie", 35)
  ages["Diana"] = 28 // Shorthand for insert
  ```
* **Map Lookup Returns Optionals**:
  Indexing a map always evaluates to `?Value` (with `?(?T)` automatically flattened to `?T`):
  ```
  let age? = ages["Alice"]
  let actual_age = ages["Alice"] ?? 0
  ```

### 7.3 Empty Collection Literals
Because an empty vector literal `[]` or empty hash literal `#[]` does not contain element expressions from which to infer key/element types, initializing an empty collection requires an explicit type annotation:
```
let empty_vec: [int] = []
let empty_set: #[string] = #[]
let empty_map: #[string: int] = #[]
```

### 7.4 Trailing Comma Rule
* **Multiline comma-separated lists** require a trailing comma on the final line:
  ```
  let g = [
      1,
      2,
      3, // Required!
  ]
  ```
* Inline lists have optional trailing commas: `[1, 2, 3]` or `[1, 2, 3,]`.

---

## 8. Type Definition System (`define`)

All user-defined types use the unified `define <Name> <Kind> [ ... ]` syntax.

### 8.1 Structs
```
define Point struct [
    x: int,
    y: int,
]

// Instantiation:
let pt = Point [
    x: 10,
    y: 20,
]

// Field access:
let px = pt.x
```

### 8.2 Enums
Enums define a strictly-typed set of named symbolic constants:
```
define Status enum [
    Pending,
    Active,
    Closed,
]
```
* **Qualified Variants**: Enum values are referenced via `EnumName.Variant` (`Status.Pending`, `Status.Active`, `Status.Closed`).
* **Type Safety & Conversion**: Each `enum` is a distinct type (never implicitly mixed with `int` or other enums). Explicit integer conversion `int(Status.Pending)` returns the underlying integer tag.
* **Methods & Interfaces**: Enums can define methods (`bind Status.to_string = (self: Status): string ...`) and implement interfaces.

### 8.3 Interfaces
Interface method signatures explicitly specify `self` (read-only borrow by default) or `&self` (mutable borrow):
```
define Printable interface [
    to_string: (self): string,
]
```
* **Implicit Implementation (Structural / Duck Typing)**:
  Any type that defines the methods declared in an interface with matching receiver mutability and parameter/return types automatically implements that interface.
* **Universal Applicability**:
  Structs, enums, distinct derived types, and built-in types (`int`, `string`, etc.) can all implement interfaces.
* **C Fat Pointer Representation**:
  Transpiles to `{ void* data; const VTable* vtable; }`. Zero heap overhead when passed as `Printable` (read-only borrow) or `&Printable` (mutable borrow).

### 8.4 Distinct Derived Types
```
define UserID int
define Port u16
```
`define <NewType> <BaseType>` creates a **distinct type** rather than an interchangeable alias:
* Passing an `int` where a `UserID` is expected is a compile-time error; conversion must be explicit (`UserID(42)` or `int(my_id)`).
* Methods defined on `UserID` (`bind UserID.to_string = ...`) apply exclusively to `UserID` without affecting `int`, and can override built-in methods or interfaces like `Printable` and `Copyable`.

---

## 9. Namespaced Methods

Functions are attached to types using namespaced declarations. The first parameter explicitly binds `self` (`self: Type` for read-only borrow, or `self: &Type` for mutable borrow):

```
// Read-only method borrow (returns float)
bind Point.length = (self: Point): float {
    sqrt(float(self.x * self.x + self.y * self.y))
}

// Mutable method borrow (requires `let` struct; void return)
bind Point.translate = (self: &Point, dx: int, dy: int) {
    self.x = self.x + dx
    self.y = self.y + dy
}

// Called using dot syntax (auto-borrows `pt`):
let len = pt.length()
pt.translate(5, 5)

// Or called via explicit type namespace (requires explicit `&` for mutable borrow):
Point.translate(&pt, 5, 5)
```

### 9.1 Built-in Methods & Extending Types
Built-in types (`int`, `float`, `bool`, `char`, `string`, `[T]`, `[N: T]`, `#[T]`, `#[K: V]`) provide predefined bound `.copy()` (`Copyable`) and `.to_string()` (`Printable`) methods. Built-in types and distinct derived types can also be extended with additional methods:
```
bind int.is_even = (self: int): bool {
    self % 2 == 0
}

// Overriding `to_string` on a distinct derived type:
bind UserID.to_string = (self: UserID): string {
    "User#${int(self)}"
}
```

---

## 10. Functions & Expression Bodies

A function takes **exactly one expression** (or block expression) as its body.
* **Value-Returning Functions**: Must specify their return type with `:Type` or `:?Type` after the parameter list.
* **Void Functions**: Functions that only perform side effects or mutate `&` arguments omit the `:Type` annotation and do not return a value.

```
bind add = (a: int, b: int): int a + b

bind add_with_block = (a: int, b: int): int {
    let sum = a + b
    sum // evaluates to sum
}
```
Both are functionally identical because a block `{}` is an expression.

---

## 11. Control Flow & Unwinding

### 11.1 If-Expressions
`if` is an expression yielding a value:
```
let result = if (c == 3) 4 else 3

let complex = if (c == 3) {
    log("Matched")
    4
} else {
    3
}
```

### 11.2 Unified `for` Loops
The unified `for` expression handles all iterations:
```
// Exclusive Range (0 through 9)
for (i in 0..10) { ... }

// Inclusive Range (0 through 10)
for (i in 0..=10) { ... }

// Collections (borrows `my_list` and binds `item` as read-only by default)
for (item in my_list) { ... }

// Mutable Collection Iteration (binds `item` as `&T` for in-place mutation)
for (&item in my_list) { ... }

// Maps (borrows `k` and `v` read-only; `for (k, &v in my_map)` for mutable values)
for (k, v in my_map) { ... }

// Conditionals (While equivalent)
for (count < 10) { ... }

// Infinite Loops
for (true) { ... }
```

#### Loop & Per-Iteration Expression Evaluation ("Last Iteration Wins"):
Just as a `{ ... }` block executes a sequence of steps and evaluates to its final expression, a `for` loop executes its iteration body repeatedly and evaluates to a **single value** (`T` when an `else` clause is present, or `?T` when `else` is omitted):
1. **Per-Iteration Expression**: Each iteration's body (or `<- val` / `next <- val` unwind) may evaluate to a value of type `T`. When the loop finishes normally after running one or more iterations, the `for` loop evaluates to the **value produced by the final iteration that executed**.
2. **Early Loop Exit (`for <- val`)**: Executing `for <- val` immediately terminates the loop and makes the entire `for` expression evaluate to `val` (whether the iteration body itself ends in a value expression or is a search loop ending in statements).
3. **With `else` Clause (Always Type `T`)**: If 0 iterations run (e.g., an empty collection/range or initially false condition) or a search loop finishes all iterations without hitting `for <- val`, the loop evaluates to the `else` expression, guaranteeing a non-optional return type of `T`:
   ```
   let final_score = for (round in 1..=5) {
       let score = play_round(round)
       if (score >= 100) for <- score // Early exit with `score`
       score                          // Iteration value; last iteration wins if loop finishes
   } else {
       0
   }
   ```
4. **Without `else` Clause (Always Type `?T`)**: If a `for` loop is used as a value expression without an `else` clause, its type is always `?T` (requiring a `?` suffix on the target variable `found?`), evaluating to `none` if 0 iterations run or if a search loop exhausts without hitting `for <- val`:
   ```
   let found? = for (x in list) {
       if (x > 10) for <- x
   }
   ```
5. **As a Statement (`void` Loop)**: If a `for` loop is not assigned or used as a value, any iteration value is discarded and the loop runs purely for side effects.

### 11.3 Targeted Unwinding (`target <- value`)
`<-` is a statement (not a general expression) that unwinds nested `{ ... }` blocks directly to a target. To prevent lexical ambiguity with the less-than operator followed by unary minus (`x < -5`), the `<-` operator **requires whitespace around it**.

| Syntax | Target | Description |
| :--- | :--- | :--- |
| `<- value` / `<-` | Immediate Block `{}` | Exits the nearest enclosing `{ ... }` block, yielding `value` (or no value in a `void` block). |
| `if <- value` | Nearest `if` | Unwinds all blocks up to the nearest enclosing `if`, making that `if` evaluate to `value`. |
| `for <- value` / `for <-` | Nearest `for` loop | Breaks out of the nearest loop, evaluating the loop to `value` (or exiting a `void` loop). |
| `next <- value` / `next <-` | Current loop iteration | Ends the current iteration of the nearest `for` loop (yielding `value` as this iteration's result, or no value in a `void` iteration) and advances to the next iteration. |
| `func <- value` / `func <-` | Nearest Function | Unwinds all blocks and immediately returns `value` (or returns `void`) from the current function, method, or lambda. |
| `func_name <- value` | Named Function | Unwinds all blocks and immediately returns `value` from the lexically enclosing `func_name`. |
| `label <- value` | Labeled block / loop | Unwinds to the specified label (`outer: for (...)`). |

#### Unwind Rules & Restrictions:
1. **No Cross-Function Jumps**: An unwind (`<-`) can **never** cross a function or lambda boundary. Attempting to unwind an outer function from inside a nested function or lambda is a **Compile-Time Error**.
2. **Valid Positions for `<-`**:
   * As a standalone statement (including as the body of an `if` branch or `match` arm).
   * Immediately on the right-hand side of a top-level `??` in a variable declaration or assignment:
     ```
     let b? = maybe_null()
     bind example = (): int {
         if (true) {
             let a = b? ?? if <- 0
             a
         } else {
             0
         }
     }
     ```
   * Unwinds are **not** permitted inside nested sub-expressions or function call argument lists.

#### Example:
```
bind find_even = (numbers: [int]): ?int {
    let first_even? = for (n in numbers) {
        if (n % 2 == 0) {
            for <- n // breaks loop, yields n to first_even?
        }
        if (n == 999) {
            func <- none // bails out of the entire function immediately!
        }
    }
    first_even?
}
```

### 11.4 Exhaustive Flow Check (Rule 1)
Every execution path in a value-producing block or function must yield a value. Missing returns or unhandled branches result in a **Compile-Time Error**.

---

## 12. Pattern Matching (`match`)

`match` is a container-free expression using `when ... else`. Every `match` expression **must terminate with an `else` arm**, guaranteeing an unambiguous end to the `match` (even when nested) and compile-time exhaustiveness:

* **Open / Fallback `match` (`else: expr`)**: Matches specific values with `when` and provides a catch-all fallback with `else:`:
  ```
  let label = match (status_code)
      when 200: "OK"
      when 404: "Not Found"
      when 500: {
          log("Internal server error")
          "Error"
      }
      else: "Unknown"
  ```
* **Exhaustive Enum `match` (`else Enum.FinalVariant: expr`)**: When matching all variants of an `enum` without a wildcard fallback, the final variant is placed in the `else` arm (`else Status.Closed:`). The compiler verifies at compile time that every variant of `Status` is covered:
  ```
  let code = match (current_status)
      when Status.Pending: 100
      when Status.Active: 200
      else Status.Closed: 300
  ```

---

## 13. Transpilation to C Architecture

1. **Target Standard**: Clean, portable C99 / C11.
2. **Deterministic Cleanup Injection**:
   At every block exit and prior to any `target <- value` jump, the compiler emits cleanup functions for all live owned resources in that scope (`vec_free`, `map_free`, `string_free`).
3. **Enums & Optionals**:
   Enums transpile directly to C integer enums; non-recursive Optionals transpile to `{ bool has_value; T value; }` stack structs, while self-referential optional struct fields are automatically boxed as owned heap pointers (`T*`).
4. **VTable & Closure Generation**:
   Static struct vtables generated for all types implementing interfaces; capturing lambdas compile to closure structs containing a function pointer and copied environment values.
