# The Lowlight Language Specification

**Edition:** 2026 · **Status:** Normative reference

Lowlight is a statically typed, expression-oriented systems language. It gives you C-level control over memory and data layout without a garbage collector, reference counting, or lifetime annotations, and it rules out null references, use-after-free, double-free, and ignored failures at compile time.

This document is the complete definition of the language. It serves three readers:

| If you want to… | Read |
|---|---|
| Learn Lowlight from scratch | [Part I: A Tour of Lowlight](#part-i-a-tour-of-lowlight), then the "At a glance" box that opens each reference chapter |
| Look up how to write something | [Part II: Quick Reference](#part-ii-quick-reference) |
| Know exactly what a construct means | [Part III: Language Reference](#part-iii-language-reference) |
| Build a Lowlight compiler or tool | Part III, then [Part IV: Implementer's Guide](#part-iv-implementers-guide) and the [Appendices](#appendices) |

Every chapter of Part III is layered the same way, so you can stop reading as soon as you have what you need:

1. **At a glance**: a short example and the handful of rules you use every day.
2. **Rules**: the normative definition, complete enough to decide every edge case.

Everything about *how* a compiler realizes the rules (parsing strategy, analysis passes, data layouts, the runtime) is collected in Part IV, organized by compiler stage and linked back to the rules it implements. Part IV never adds language rules beyond those it marks **Required**.

---

## Contents

**[Part I: A Tour of Lowlight](#part-i-a-tour-of-lowlight)**
- [Hello, world](#hello-world) · [Values and variables](#values-and-variables) · [Functions](#functions) · [Types you define](#types-you-define) · [Optionals](#optionals) · [Results and failures](#results-and-failures) · [Control flow](#control-flow) · [Ownership in five minutes](#ownership-in-five-minutes) · [Packages and tests](#packages-and-tests) · [Where to go next](#where-to-go-next)

**[Part II: Quick Reference](#part-ii-quick-reference)**
- [Declarations](#declarations) · [Types](#types) · [Naming rules](#naming-rules) · [Operators](#operators) · [Control flow forms](#control-flow-forms) · [Unwinds](#unwinds) · [Ownership and parameter modes](#ownership-and-parameter-modes) · [Handling absence and failure](#handling-absence-and-failure) · [Attributes](#attributes) · [Universe functions](#universe-functions) · [Standard library packages](#standard-library-packages)

**[Part III: Language Reference](#part-iii-language-reference)**
1. [Conventions and Conformance](#1-conventions-and-conformance)
2. [Lexical Structure](#2-lexical-structure)
3. [Types](#3-types)
4. [Declarations and Program Structure](#4-declarations-and-program-structure)
5. [Expressions](#5-expressions)
6. [Control Flow](#6-control-flow)
7. [Ownership, Borrowing, and Destruction](#7-ownership-borrowing-and-destruction)
8. [Generics](#8-generics)
9. [Failure Handling](#9-failure-handling)
10. [Unsafe Code and Foreign Functions](#10-unsafe-code-and-foreign-functions)
11. [Testing](#11-testing)
12. [The Prelude](#12-the-prelude)
13. [The Standard Library](#13-the-standard-library)
14. [Packages, Manifests, and Dependencies](#14-packages-manifests-and-dependencies)

**[Part IV: Implementer's Guide](#part-iv-implementers-guide)**
15. [Compiler Architecture](#15-compiler-architecture)
16. [Parsing Ambiguities and Their Resolution](#16-parsing-ambiguities-and-their-resolution)
17. [Semantic Analysis](#17-semantic-analysis)
18. [Lowering and Runtime Representation](#18-lowering-and-runtime-representation)
19. [The Runtime Contract](#19-the-runtime-contract)
20. [Conformance Checklist](#20-conformance-checklist)

**[Appendices](#appendices)**
- [A. Grammar](#appendix-a-grammar) · [B. Keywords and Contextual Words](#appendix-b-keywords-and-contextual-words) · [C. Operator Precedence](#appendix-c-operator-precedence) · [D. Runtime Traps](#appendix-d-runtime-traps) · [E. Glossary](#appendix-e-glossary)

---

# Part I: A Tour of Lowlight

This part shows the language by example. Each section links to the reference chapter that defines it precisely. Every program here compiles and runs as written.

## Hello, world

```lowlight
bind main = fn(): println("Hello, world!")
```

Save it as `hello.ll` and run `lowlight run hello.ll`. A program is a set of top-level declarations; `main` is the entry point. `bind name = value` declares something that never changes, here a function. `fn(): ...` is a function that takes nothing; its body follows the colon.

## Values and variables

```lowlight
bind main = fn(): {
	let count = 3            // `let`: a variable you may reassign
	count += 1
	bind limit = 10          // `bind`: never reassigned
	let ratio = 2.5          // float
	let name = "Lowlight"    // string
	let initial = 'L'        // char
	let ok = count < limit   // bool
	println("$name has $count of $limit, ok: $ok, ratio ${ratio * 2.0}, starts with $initial")
}
```

- Types are inferred; write them when you want to: `let count: int = 3`.
- Numbers never convert implicitly. `int`, `float`, `u8`, `i32` and the rest are separate types; convert explicitly with `float(count)` or `u8(n)`.
- `"$name"` and `"${expr}"` interpolate any value that can print itself.
- A `{ ... }` block is an expression whose value is its last line.

See [§2 Lexical Structure](#2-lexical-structure) and [§3 Types](#3-types).

## Functions

```lowlight
bind square = fn(x: int) int: x * x

bind describe = fn(n: int) string: {
	let kind = if(n % 2 == 0): "even" else: "odd"
	"$n is $kind"
}

bind main = fn(): {
	println(square(7))
	println(describe(7))
	let twice = fn(x: int) int: x * 2      // a function value
	println(twice(21))
}
```

A function is written `fn(parameters) ReturnType: body`. The body is a single expression or a `{ ... }` block. Leave the return type out when the function returns nothing. `if(...): a else: b` is an expression, so it can initialize a variable.

See [§4.3 Functions](#43-functions) and [§5 Expressions](#5-expressions).

## Types you define

```lowlight
define Point struct [
	x: float,
	y: float,
]

define Shape enum [
	circle: float,
	rect: Point,
	empty,
]

bind Point.length_squared = fn(self) float: self.x * self.x + self.y * self.y

bind area = fn(s: Shape) float: match(s):
	when circle(r): 3.14159 * r * r
	when rect(p): p.x * p.y
	default empty: 0.0

bind main = fn(): {
	let p = Point[x: 3.0, y: 4.0]
	println(p.length_squared())
	println(area(Shape.rect(p)))
	println(area(Shape.circle(1.0)))
}
```

- `struct` groups named fields; build one with `Type[field: value, ...]`.
- `enum` lists alternatives (variants); a variant may carry one value. Outside a `match`, a variant is named with its type, `Shape.circle(1.0)`; inside a `match` arm it is named bare, `when circle(r)`.
- `bind Type.method = fn(self) ...` adds a method. `self` is read-only; write `&self` to mutate the receiver.
- `match` must handle every case. The last arm always starts with `default`.

See [§3.7 Structs](#37-structs), [§3.8 Enums](#38-enums), and [§6.5 Pattern matching](#65-pattern-matching).

## Optionals

There is no `null`. A value that may be absent has an optional type `?T`, and its name ends in `?`:

```lowlight
bind find_index = fn(items: [string], wanted: string) ?int: {
	for(i, item in items): if(item == wanted): func <- i
	none
}

bind main = fn(): {
	let names = ["ada", "grace", "linus"]
	let pos? = find_index(names, "grace")
	if(pos?): println("found at $pos")      // inside, `pos` is a plain int
	println(find_index(names, "bob") ?? -1)  // `??` supplies a fallback
}
```

`func <- i` returns `i` from the function early. It's one of the unwinds described in [§6.7](#67-unwinds-and-labels).

See [§3.5 Optionals](#35-optionals).

## Results and failures

An operation that can fail returns a **result**. Names that hold one end in `!`:

```lowlight
define Lookup result [
	value: string,
	missing: string,
	forbidden,
]

bind find = fn(key: string) Lookup:
	if(key == "secret"): forbidden
	else if(key == "name"): value("Lowlight")
	else: missing(key.copy())

bind greet = fn(key: string) string: {
	let who = find(key):
		when missing(k): func <- "no entry for $k"
		default forbidden: "stranger"
	"hello, $who"
}

bind main = fn(): {
	println(greet("name"))
	println(greet("secret"))
	println(greet("age"))
	let r! = find("name")
	println(r! ?? "fallback")
}
```

- A result type has one success member, `value`, plus failure cases.
- Where a result is expected you name the member directly: `value(x)`, `missing(k)`, `forbidden`.
- `expr:` followed by `when` arms is a **result chain**. It evaluates to the success value, and each arm handles a failure by producing a value or leaving with an unwind. `default panic` ends a chain when the remaining failures should never happen.
- The standard `Result[T]` (`value: T` or `err: Error`) is used throughout the standard library, and `Status` (`value` or `err: Error`) for operations that produce nothing.

Unrecoverable bugs, such as an index out of bounds or a failed `assert`, **panic**: the program stops with a message. Panics can't be caught.

See [§3.9 Results](#39-results), [§6.6 Result chains](#66-result-chains), and [§9 Failure Handling](#9-failure-handling).

## Control flow

```lowlight
bind main = fn(): {
	// `for` collects its body's values into a list.
	let squares = for(n in 1..=5): n * n
	println(squares)

	// `next <-` skips an element.
	let odd = for(n in 1..10): {
		if(n % 2 == 0): next <-
		n
	}
	println(odd)

	// `while` produces a single value; `while <-` leaves with one.
	let first_big = while(n in 1..100) int: {
		if(n * n > 50): while <- n
	} else: 0
	println(first_big)

	// `match` with no subject is a chain of conditions.
	let size = match:
		when first_big > 5: "large"
		default: "small"
	println(size)
}
```

Lowlight has no `break`, `continue`, or `return` keywords. One operator, `<-`, leaves a construct, and the word before it says which one: `func <-` returns, `for <-` and `while <-` break, `next <-` continues, and `label <-` leaves a labeled block or loop.

See [§6 Control Flow](#6-control-flow).

## Ownership in five minutes

Every value has exactly one owner. Small fixed-size values (numbers, `bool`, `char`, plain structs) are **plain** and copy freely. Values that own memory or system resources (`string`, lists, maps, files) are **resources**: they move rather than copy, and are freed automatically at the end of their owner's scope.

```lowlight
bind shout = fn(s: string) string: s.to_upper()       // read-only borrow
bind append_bang = fn(s: &string): s.push("!")        // mutable borrow
bind consume = fn(s: own string): println("took $s")  // takes ownership

bind main = fn(): {
	let msg = "hi" + " there"
	println(shout(msg))      // borrowed; msg is still ours
	append_bang(&msg)        // `&` at the call site: msg may change
	println(msg)
	consume(own msg)         // `own` at the call site: msg moves away
	// println(msg)          // error: use of moved variable "msg"
	let copy = shout("x").copy()
	println(copy)
}
```

- A parameter is borrowed read-only by default.
- `&T` lets the function change your variable, and you must write `&` when you call it.
- `own T` takes the value, and you must write `own` when you call it.
- Nothing is ever mutated or moved without a visible marker at the call site.

See [§7 Ownership, Borrowing, and Destruction](#7-ownership-borrowing-and-destruction).

## Packages and tests

A directory of `.ll` files is a package. Other packages are used through a qualifier:

```lowlight
import "std/strings"

@test("join puts separators between items")
bind test_join = fn(): {
	let joined = strings.join(["a", "b", "c"], "-")
	assert(joined == "a-b-c", "unexpected: $joined")
}

bind main = fn(): println(strings.join(["x", "y"], ", "))
```

`lowlight test` runs every `@test` function and checks each one for leaks. Names beginning with `_` are private to their package.

See [§4.6 Packages and imports](#46-packages-and-imports), [§11 Testing](#11-testing), and [§14 Packages, Manifests, and Dependencies](#14-packages-manifests-and-dependencies).

## Where to go next

- Keep [Part II: Quick Reference](#part-ii-quick-reference) open while you write code.
- Read the "At a glance" box at the top of each chapter in [Part III](#part-iii-language-reference) for a fuller picture, and the rest of each chapter when you need the exact rules.
- Run `lowlight doc` to browse the standard library's API documentation.

---

# Part II: Quick Reference

Tables for looking things up. Each row links to its full definition.

## Declarations

| Form | Example | Reference |
|---|---|---|
| Immutable or function | `bind limit = 100` · `bind add = fn(a: int, b: int) int: a + b` | [§4.2](#42-bind-const-and-let), [§4.3](#43-functions) |
| Compile-time constant or function | `const N = 16` · `const sq = fn(x: int) int: x * x` | [§4.2](#42-bind-const-and-let), [§4.3](#43-functions) |
| Mutable variable (inside functions only) | `let total = 0` | [§4.2](#42-bind-const-and-let) |
| Method | `(bind \| const) Point.norm = fn(self) float: ...` | [§4.4](#44-methods-and-associated-functions) |
| Associated function | `(bind \| const) Point.origin = fn() Point: Point[x: 0.0, y: 0.0]` | [§4.4](#44-methods-and-associated-functions) |
| Struct | `define Point struct [x: float, y: float]` | [§3.7](#37-structs) |
| Enum | `define Color enum [red, green]` · `define Shape enum [circle: float, empty]` | [§3.8](#38-enums) |
| Result type | `define Lookup result [value: Item, missing: string]` | [§3.9](#39-results) |
| Interface | `define Shape interface [area: fn(self) float]` | [§3.11](#311-interfaces) |
| Distinct type | `define UserId int` | [§3.10](#310-distinct-types) |
| Generic | `define Stack[T] struct [items: [T]]` · `bind first[T] = fn(xs: [T]) ?T: xs.get(0)` | [§8](#8-generics) |
| Import | `import "std/fs"` · `import "std/math" as m` | [§4.6](#46-packages-and-imports) |
| Foreign function | `extern "stdio.h"` · `extern puts: (s: ptr[u8]) i32` | [§10.4](#104-foreign-functions) |
| Tuple unpacking | `let x, y = unpack +[1, 2]` | [§4.2](#42-bind-const-and-let) |

## Types

| Type | Meaning | Category | Reference |
|---|---|---|---|
| `int` `i8` `i16` `i32` `i64` | Signed integers (`int` = `i64`) | plain | [§3.2](#32-numbers) |
| `u8` `u16` `u32` `u64` | Unsigned integers | plain | [§3.2](#32-numbers) |
| `float` `f32` `f64` | IEEE-754 floats (`float` = `f64`) | plain | [§3.2](#32-numbers) |
| `bool` · `char` | Truth value · Unicode scalar value | plain | [§3.3](#33-bool-and-char) |
| `string` | Owned UTF-8 text | resource | [§3.4](#34-strings) |
| `?T` | `T` or `none` | as `T` | [§3.5](#35-optionals) |
| `[T]` | Growable list | resource | [§3.6](#36-collections) |
| `[N: T]` | Fixed-size array | as `T` | [§3.6](#36-collections) |
| `#[T]` · `#[K: V]` | Set · map | resource | [§3.6](#36-collections) |
| `+[A, B, ...]` | Tuple | plain if all parts are | [§3.6](#36-collections) |
| `Range[T]` | `lo..hi`, `lo..=hi` | plain | [§3.6](#36-collections) |
| `fn(A, B) R` · `fn(A)` | Function returning `R` · returning nothing | plain | [§3.12](#312-functions-and-closures) |
| `cl(A, B) R` · `cl(A)` | Closure returning `R` · returning nothing | resource | [§3.12](#312-functions-and-closures) |
| `extern fn(A, B) R` | C function pointer | plain | [§10.5](#105-c-function-pointers) |
| `Arena` · `ref[T]` | Region allocator · reference into it | resource · plain | [§3.13](#313-arenas) |
| `ptr[T]` | Raw pointer | plain | [§3.14](#314-raw-pointers) |
| `void` · `never` | No value (return types only) · does not return | | [§3.15](#315-void-and-never) |

## Naming rules

| Rule | Example | Reference |
|---|---|---|
| A name holding an optional ends in `?`; no other name may | `let user? = find(id)` | [§2.5](#25-identifiers) |
| A name holding a result ends in `!`; no other name may | `let cfg! = load(path)` | [§2.5](#25-identifiers) |
| A leading `_` makes a declaration, field, variant, or method private to its package | `bind _helper = ...` · `_count: int` | [§4.7](#47-visibility) |
| Convention: types `PascalCase`; variants, functions, variables, fields `snake_case`; top-level constants `SCREAMING_SNAKE_CASE` (see [CONVENTIONS.md](CONVENTIONS.md)) | `Point`, `Shape.circle`, `read_file`, `MAX_RETRIES` | [§2.5](#25-identifiers) |

## Operators

From tightest to loosest binding. Full details are in [§5.1](#51-evaluation-order-and-precedence) and [Appendix C](#appendix-c-operator-precedence).

| Operators | Meaning |
|---|---|
| `.` `?.` `()` `[]` | Member, optional member, call, index or slice |
| `-x` `not` `~` `*p` `&x` `own x` `unsafe e` `comptime e` | Prefix operators |
| `*` `/` `%` | Multiply, divide, remainder |
| `+` `-` | Add, subtract (`+` also concatenates strings, and calls `add` on types that implement `Addable`) |
| `<<` `>>` | Shift (traps unless `0 <= n < bit width`) |
| `..` `..=` | Range, half-open or inclusive |
| `<` `<=` `>` `>=` | Ordering (non-associative) |
| `==` `!=` | Equality (non-associative) |
| `&` · `^` · `\|` | Bitwise and · xor · or |
| `and` · `or` | Short-circuit logic |
| `??` | Fallback for an optional or result |
| `<-` | Unwind (leave a construct) |

Assignment is a statement: `=` `+=` `-=` `*=` `/=` `%=` `&=` `|=` `^=` `<<=` `>>=`. There is no `!` negation (use `not`) and no `as` cast (write `int(x)`, `float(x)`, ...).

## Control flow forms

| Form | Value | Reference |
|---|---|---|
| `if(c): a else: b` | `a` or `b` | [§6.2](#62-if) |
| `if(c): a` | `?T`: `a` or `none` | [§6.2](#62-if) |
| `if(x?): ...` · `if(&x?): ...` | Narrows `x?` to `x` (read-only, mutable) | [§6.2](#62-if) |
| `if(e as v): ...` | Binds the value of an optional or result | [§6.2](#62-if) |
| `while(cond): ...` · `while(x in xs) T: ... else: d` | A single value (`?T` without `else`) | [§6.3](#63-while) |
| `for(x in xs): body` | A list of each iteration's value | [§6.4](#64-for) |
| `for(x in xs) #[T]: body` | Any collection that can `push` | [§6.4](#64-for) |
| `match(v): when P: ... default: ...` | Pattern match; must end in `default`; variants are named bare (`when pending:`) | [§6.5](#65-pattern-matching) |
| `match: when cond: ... default: ...` | Condition chain | [§6.5](#65-pattern-matching) |
| `expr: when Failure(x): ... default ...` | Result chain; evaluates to the success value | [§6.6](#66-result-chains) |
| `name: { ... }` · `name: for(...)` | Labeled block or loop | [§6.7](#67-unwinds-and-labels) |
| `comptime if(c): ...` · `comptime for(...)` · `comptime match(...)` | Compile-time branch selection, loop unrolling, and pattern matching | [§4.2](#42-bind-const-and-let), [§6.5](#65-pattern-matching) |

## Unwinds

| Write | Leaves | With value |
|---|---|---|
| `func <- v` | the function (return) | the return value |
| `for <-` · `while <- v` | the innermost `for` / `while` (break) | `for`: what it has collected; `while`: `v` |
| `next <-` | the current iteration of the innermost loop (continue) | |
| `<- v` | the innermost `{ }` block | the block's value |
| `if <- v` · `match <- v` | the innermost `if` / `match` | its value |
| `name <- v` · `next name <-` | the labeled construct · next iteration of the labeled loop | |

An unwind has type `never`, so `let x = find() ?? func <- fallback` and similar guards type-check. Unwinds never cross a function boundary. See [§6.7](#67-unwinds-and-labels).

## Ownership and parameter modes

| Parameter | At the call site | The function may | Reference |
|---|---|---|---|
| `x: T` | `f(x)` | read (plain values are copied) | [§7.3](#73-parameter-modes) |
| `x: &T` | `f(&x)` | read and modify the caller's variable | [§7.3](#73-parameter-modes) |
| `x: own T` | `f(own x)` (a temporary needs nothing) | keep or destroy the value | [§7.3](#73-parameter-modes) |
| receiver `self` · `&self` · `own self` | `v.m()` | as above, for the receiver | [§4.4](#44-methods-and-associated-functions) |

Moving an existing resource variable always needs `own`: `let b = own a`, `[own a]`, `Point[name: own s]`. Copy a resource with `.copy()`.

## Handling absence and failure

| You have | Do this | Write |
|---|---|---|
| `x?: ?T` | use it if present | `if(x?): use(x)` |
| | supply a default | `x? ?? default` |
| | leave if absent | `let v = x? ?? func <- fallback` |
| | reach through it | `x?.field`, `x?.method()` |
| `r!: R` (a result) | handle each failure | `r!: when missing(k): ... default forbidden: ...` |
| | handle every failure alike | `r!: default: fallback` |
| | assert it can't fail | `r!: default panic` |
| | discard the failure | `r! ?? fallback` |
| | use the value only if it succeeded | `if(r! as v): use(v) else: ...` |
| | pass the failure up | `default err(own e): func <- err(e.wrap("context"))` |
| a bug or broken invariant | stop the program | `panic("msg")`, `assert(cond, "msg")` |

## Attributes

| Attribute | On | Effect |
|---|---|---|
| `@test` · `@test("name")` | function | a test run by `lowlight test` |
| `@test_only` | any declaration | usable only from tests |
| `@track_caller` | function | panics and `caller_*()` report the caller's location |
| `@must_use` · `@nodiscard` | function, type | discarding the value is an error |
| `@deprecated` · `@deprecated("msg")` | declaration | warns at each use |
| `@inline` · `@noinline` | function | inlining hint |
| `@packed` (or `define packed T struct`) | struct | no padding between fields |
| `@derive(Equatable, ...)` | struct, enum | derives the listed interfaces ([§12.2](#122-deriving-methods)) |
| `@derivable(m: fn_path, ...)` | interface | names generic derivation functions for `@derive` ([§12.2](#122-deriving-methods)) |
| `@comptime_only` | `const` decl, type | available only at compile time (never emitted or callable at runtime) |
| `@comptime_limit(steps: N, memory: M)` | `const` decl | override the default compile-time step (`1_000_000`) and memory (`64 MB`) budgets (single integer `@comptime_limit(N)` sets steps) |
| `@lang("name")` | prelude declaration | binds a language hook (prelude only) |

See [§4.8](#48-attributes).

## Universe functions

Always in scope; no import needed. Full list in [§12.3](#123-universe-functions).

| Function | Meaning |
|---|---|
| `print(v)` · `println(v)` · `eprint(v)` · `eprintln(v)` | Write to stdout / stderr (`println()` alone prints a newline) |
| `panic(msg)` · `panic(err)` | Stop the program (`never`) |
| `assert(cond)` · `assert(cond, msg)` | Panic if `cond` is false |
| `exit(code)` | End the process (`never`) |
| `args()` | Command-line arguments, `[string]` |
| `parse_int(s)` · `parse_float(s)` | `?int` · `?float` |
| `abs(x)` · `min(a, b)` · `max(a, b)` | Numeric helpers |
| `caller_location()` · `caller_file()` · `caller_line()` · `caller_col()` | Source location of the caller |
| `type.size_of[T]()` · `type.align_of[T]()` · `type.is_plain[T]()` | `const` type layout queries (usable in both `const` and runtime code) |
| `comp.TARGET` | `@comptime_only` target descriptor (`os`, `arch`, `endian`, `is_unix`, `ptr_size`, `ptr_bits`, `triple`) |
| `comp.compile_error(msg)` · `comp.log(val)` | `@comptime_only` diagnostics emitted during compile-time evaluation |
| `comp.embed_str(path)` · `comp.embed_bytes(path)` | `@comptime_only` package-confined file embedding (`string` / `[u8]`, static `.rodata`) |
| `comp.type_name[T]()` · `comp.kind_of[T]()` · `comp.fields_of[T]()` · `comp.variants_of[T]()` · `comp.elems_of[T]()` · `comp.attrs_of[T]()` · `comp.implements[T, I]()` | `@comptime_only` compile-time type reflection queries ([§4.2](#42-bind-const-and-let)) |
| `variant_index(v)` · `make_variant[T](vr [, own p])` | generic enum/`result` discriminant inspection and variant construction |

## Standard library packages

| Package | Provides |
|---|---|
| `std/core` | The prelude: interfaces, collections, `Result`, `Status`, `Error` (always imported) ([§12](#12-the-prelude)) |
| `std/chars` | Character classification and conversion |
| `std/strings` | String searching, splitting, joining, padding |
| `std/strconv` | Parsing and formatting numbers and booleans |
| `std/strfmt` | Number formatting, padding, case conversion |
| `std/unicode` | Full Unicode case mapping and case-insensitive comparison |
| `std/math` | Floating-point math |
| `std/sort` | Sorting and searching lists and arrays |
| `std/fs` | Files and the file system |
| `std/os` | Processes, directories, platform information |
| `std/time` | Durations, instants, dates and times, formatting |
| `std/net` | TCP sockets |
| `std/http` | HTTP client, server, and routing |
| `std/pool` | Handle-based object pools |
| `std/collections` | Deques, queues, bit sets, priority queues, string interning, ordered maps |
| `std/test` | Rich test assertions |

See [§13](#13-the-standard-library).

---

# Part III: Language Reference

## 1. Conventions and Conformance

### 1.1 How rules are written

- **must** and **must not** state requirements on programs: a program that breaks one is rejected at compile time unless the rule says it is a runtime trap. When they appear in implementation notes, they state requirements on implementations.
- **Trap** means the program stops at run time with a panic ([§9.3](#93-panics)). Traps are deterministic: the same program and input always trap at the same point, in every build mode.
- Grammar fragments use EBNF: `[ x ]` is optional, `{ x }` repeats zero or more times, `a | b` is a choice, and quoted text is literal. The complete grammar is [Appendix A](#appendix-a-grammar).
- Code examples that define `main` are complete programs. Other examples are fragments that assume surrounding declarations.

### 1.2 Design principles

These principles explain the rules that follow. When a rule seems surprising, one of them is usually the reason.

1. **Deterministic lifetimes.** Memory and system resources are released at the end of the scope that owns them, in reverse order of creation. There is no garbage collector, reference counting, or finalizer thread.
2. **Plain values and resources.** Every type is either *plain* (copied bit for bit, nothing to release) or a *resource* (owned by exactly one place, moved rather than copied, released automatically).
3. **No null.** Absence is an optional type `?T`. Optionals never nest, and every name that holds one says so with a trailing `?`.
4. **Failure is a value.** An operation that can fail returns a result type, and every name that holds one says so with a trailing `!`. Bugs panic and cannot be caught.
5. **No lifetime annotations.** References are kept valid by lexical scoping, borrow exclusivity, and arena pinning, never by annotations in the program.
6. **Visible effects.** A function can change the caller's variable only if the call site says `&`, and can take ownership of it only if the call site says `own`.
7. **Expressions everywhere.** Blocks, conditionals, loops, and matches are expressions that produce values.
8. **Target neutrality.** The semantics do not depend on a machine or backend. An implementation may compile to native code, C, or another target, as long as observable behavior, ownership guarantees, and traps match this document.

### 1.3 Conformance

A conforming implementation:

1. accepts every program this document defines as valid, and rejects every program it defines as invalid, with a diagnostic at or near the offending construct;
2. produces the observable behavior this document defines: output, exit status, traps, and the order in which resources are released;
3. provides the prelude ([§12](#12-the-prelude)) with the behavior specified there.

The standard library packages of [§13](#13-the-standard-library) are part of the distribution but are ordinary Lowlight code; their detailed API is documented by `lowlight doc`.

---

## 2. Lexical Structure

> **At a glance**
> - Source is UTF-8. Identifiers, keywords, and operators are ASCII.
> - Newlines end statements; you rarely write `;`. A line that starts with `else`, `when`, `.`, or `?.` continues the previous one.
> - A multi-line list of two or more items needs a trailing comma.
> - `name?` names an optional and `name!` names a result; the suffix is part of the name.
> - Strings: `"..."` on one line, `` `...` `` across lines; both interpolate `$name` and `${expr}`.

### 2.1 Source text

A source file is a sequence of Unicode characters encoded in UTF-8, stored in a file with the `.ll` extension. Non-ASCII characters may appear only in comments, character literals, and string literals. Line endings `\r\n` are equivalent to `\n`.

### 2.2 Whitespace and comments

Whitespace is space (U+0020), tab (U+0009), carriage return (U+000D), and line feed (U+000A). It separates tokens and is otherwise insignificant, except that newlines drive automatic semicolon insertion ([§2.3](#23-statement-separation)) and the unwind operator must be separated by whitespace ([§2.9](#29-operators-and-punctuation)).

- A line comment starts with `//` and runs to the end of the line.
- A block comment starts with `/*` and ends at the first `*/`. Block comments do not nest.

**Documentation comments** are ordinary comments placed immediately before a declaration (or before its attributes), with no blank line between them, or at the end of the line declaring a struct field, enum variant, or interface method. A comment block at the top of a file documents the package. Documentation is Markdown; ``[`Name`]`` links to another declaration, and `# Heading` lines (such as `# Panics` or `# Example`) start sections. Tools (`lowlight doc`, the language server) render these; the compiler otherwise ignores them.

### 2.3 Statement separation

Statements are separated by semicolons. Most semicolons are inserted automatically by the lexer, so source code rarely contains them.

**Rule.** At a newline (or end of file), the lexer inserts a semicolon if all of the following hold:

1. **Block level.** Every delimiter opened so far and not yet closed is a `{`. Inside `( )`, `[ ]`, `#[ ]`, `+[ ]`, or `${ }` no semicolons are inserted, so expressions in parentheses, argument lists, and collection literals may span lines freely.
2. **Value end.** The last token on the line is one of: an identifier (including `name?` and `name!`), a literal (integer, float, character, string, or the end of an interpolated string), `true`, `false`, `none`, `self`, `Self`, `)`, `]`, `}`, or `<-`.
3. **No continuation.** The first token on the next line is not `else`, `when`, `.`, or `?.`.

So a method chain can continue on the next line by starting it with `.`, and `else:` and `when` arms can begin new lines.

**Block values.** An expression at the end of a block, followed only by an inserted semicolon or by `}`, is the block's value. An explicitly written `;` after it discards it, and the block then has type `void`.

```lowlight
bind main = fn(): {
	let total = [1, 2, 3]
		.len()                // continues the previous line
	let doubled = {
		let t = total * 2
		t                     // the block's value
	}
	println(doubled)
}
```

### 2.4 Trailing commas

In any comma-separated list of **two or more** items enclosed in `( )`, `[ ]`, `#[ ]`, or `+[ ]`, if the closing delimiter is on a later line than both the opening delimiter and the last item, the last item **must** be followed by a comma. Otherwise a trailing comma is optional.

The rule applies to every comma-separated list: struct fields, enum variants, interface methods, parameters, arguments, struct literal fields, list, array, set, map, and tuple literals, type parameters and arguments, and pattern lists.

```lowlight
define Point struct [
	x: float,
	y: float,     // required: two items, `]` on its own line
]
```

### 2.5 Identifiers

```ebnf
Ident     = ( Letter | "_" ) { Letter | Digit | "_" } ;
OptIdent  = Ident "?" ;
ResIdent  = Ident "!" ;
```

Identifiers are case-sensitive and ASCII. The suffix of an `OptIdent` or `ResIdent` is part of the name: `user` and `user?` are different names.

**The optional naming rule.** A variable, parameter, struct field, loop variable, or pattern binding whose type is an optional `?T` **must** have a name ending in `?`. A name ending in `?` **must not** hold a non-optional type.

**The result naming rule.** A variable, parameter, struct field, loop variable, or pattern binding whose type is a result type ([§3.9](#39-results)) **must** have a name ending in `!`. A name ending in `!` **must not** hold a non-result type. An optional result is named with `?` (the outer optional wins).

```lowlight
let count? = parse_int("42")     // ?int
let cfg! = load_config(path)     // a result
let total = 3                    // neither
```

The wildcard `_` is not a name: it discards a value in unpacking and patterns, and is exempt from both rules.

**How the suffixes are lexed.**
- `?` directly after an identifier joins it, unless it begins `??`. (`x???` lexes as `x?` followed by `??`.) `user?.name` is `user?` `.` `name`.
- `!` directly after an identifier joins it, unless it begins `!=`. Lowlight has no prefix `!` operator (negation is `not`), so this is unambiguous: `a!=b` is `a != b`, and `cfg!.is_ok()` is `cfg!` `.` `is_ok` `(` `)`.
- In simple string interpolation, `$name` ends before a trailing `?` or `!`, so `"Hello $name!"` prints the exclamation mark. To interpolate a suffixed name, use braces: `"${user?.name}"`, `"${cfg!.is_ok()}"`.

**Conventions** (not enforced by the compiler): types and interfaces use `PascalCase`, with acronyms written as words (`Url`, `HttpClient`); enum variants, result members, functions, variables, fields, and packages use `snake_case`; top-level constants use `SCREAMING_SNAKE_CASE`. [CONVENTIONS.md](CONVENTIONS.md) is the full style guide. A leading `_` is not a convention but a rule: it makes a declaration package-private ([§4.7](#47-visibility)).

### 2.6 Keywords

These 32 words are reserved and cannot be used as identifiers:

```
and      as       bind     cl       comptime const    define   else
enum     extern   false    fn       for      func     if       import
in       interface let     match    none     not      or       own
self     Self     struct   true     unpack   unsafe   when     while
```

These words are **contextual**: they have special meaning only in the positions listed, and are ordinary identifiers everywhere else.

| Word | Special when | Example |
|---|---|---|
| `next` | followed by `<-`, or by a label and `<-` | `next <-`, `next outer <-` |
| `default` | starting the last arm of a `match` or result chain | `default:`, `default red:`, `default panic` |
| `panic` | directly after `default` in a result chain, not followed by `(` | `default panic` |
| `result` | after `define Name[...]` | `define Lookup result [...]` |
| `packed` | between `define` and a struct name | `define packed Header struct [...]` |

`void` and `never` are type names, not keywords. `void` is accepted only as a return type ([§3.15](#315-void-and-never)).

### 2.7 Literals

**Integers.** Decimal (`42`), hexadecimal (`0xFF`, `0X1f`), and binary (`0b1010`), with optional `_` between digits (`1_000_000`). An integer literal takes the integer type expected by its context; with no expectation it is `int`. A literal that does not fit the expected type is an error.

**Floats.** Digits, a `.`, at least one digit, and an optional exponent: `3.14`, `0.5`, `1.0e-4`, `2.5E+3`. `1.` and `.5` are not float literals (this keeps `1..10` and member access unambiguous). A float literal takes `f32` or `f64` from context and defaults to `float`.

**Booleans.** `true` and `false`.

**None.** `none` is the absent value of every optional type. It needs an optional type from context: `let x? = none` without an annotation is an error, while `let x?: ?int = none` is valid.

**Characters.** A single Unicode scalar value in single quotes: `'a'`, `'🦀'`, `'\n'`. Escapes: `\n` `\r` `\t` `\\` `\'` `\"` `\0`, `\xHH` (a byte value), and `\u{H...}` (one to six hex digits, a valid scalar value).

### 2.8 Strings

**Double-quoted strings** `"..."` may not contain a raw newline. Escapes are those of character literals plus `\$` (a literal dollar sign).

**Backtick strings** `` `...` `` may span lines; newlines are kept as `\n`. Quotes need no escaping. `` \` `` is a backtick and `\$` a dollar sign. The usual escapes (`\n`, `\t`, `\\`, `\xHH`, `\u{...}`, ...) are decoded; any other backslash sequence, such as `\d` or `C:\path`, is kept verbatim.

**Interpolation** works in both forms:

- `$name` inserts a variable (or `$self` the receiver). The name ends at the first character that cannot continue an identifier, so a trailing `?` or `!` is literal text.
- `${expr}` inserts any expression. Inside the braces, the full expression grammar applies, including nested strings.

Each interpolated value must implement `Printable` ([§12.1](#121-core-interfaces)); interpolation calls its `to_string()` and concatenates the pieces. Interpolation is resolved at compile time; there is no runtime formatting language.

```lowlight
bind main = fn(): {
	let name = "Ada"
	let n = 3
	println("Hello $name! You have ${n * 2} messages.")
	let json = `{"name": "$name"}`
	println(json)
}
```

### 2.9 Operators and punctuation

```
+   -   *   /   %   &   |   ^   ~   <<  >>
==  !=  <   <=  >   >=
=   +=  -=  *=  /=  %=  &=  |=  ^=  <<= >>=
.   ?.  ..  ..= ?   ??  <-
(   )   [   ]   {   }   #[  +[
:   ,   ;   @   $
```

**The unwind operator `<-`** is recognized only when it is preceded by whitespace (or the start of the file, a comment, or `${`) and followed by whitespace, end of file, `;`, a closing delimiter, `,`, or a comment. Everywhere else, `<` and `-` are separate tokens, so `a<-b` means `a < -b`.

---

## 3. Types

> **At a glance**
> - Every type is either **plain** (copied freely, nothing to release) or a **resource** (one owner, moves, released automatically). See [§3.1](#31-plain-values-and-resources).
> - No implicit conversions between numeric types: write `float(n)`, `u8(x)`, `int(c)`.
> - `?T` is "maybe a `T`". There is no null, and `??T` does not exist.
> - Build values with brackets: `Point[x: 1.0, y: 2.0]`, `[1, 2]`, `#["k": v]`, `+[a, b]`.
> - `define X result [value: T, Failure: P]` declares a result type; `Result[T]` is the standard one.

### 3.1 Plain values and resources

Every type belongs to exactly one of two categories.

**Plain types** have a fixed size, own no heap memory or system resource, and have no destructor. Copying one copies its bits; both copies are independent. Plain types are: the numeric types, `bool`, `char`, pure tag enums, `ptr[T]`, `ref[T]`, `Range[T]`, and structs, fixed arrays, tuples, tagged enums, and distinct types all of whose parts are plain. Every plain type satisfies `Copyable`.

**Resource types** own something that must be released: heap memory (`string`, `[T]`, `#[T]`, `#[K: V]`), an arena, a closure environment, an interface box, or an operating-system resource. A struct, array, tuple, enum, or distinct type that contains a resource is a resource, and so is any type with a `Drop` implementation. A resource has exactly one owner. Assigning or passing it **moves** it, and when its owner's scope ends without moving it, it is destroyed ([§7.5](#75-destruction)).

The category is computed from the type's structure; it is never declared.

### 3.2 Numbers

| Type | Size | Range |
|---|---|---|
| `i8` `i16` `i32` `i64` | 8–64 bits | two's complement |
| `int` | 64 bits | same type as `i64` |
| `u8` `u16` `u32` `u64` | 8–64 bits | unsigned |
| `f32` · `f64` | 32 · 64 bits | IEEE-754 |
| `float` | 64 bits | same type as `f64` |

Numeric types never convert implicitly, in either direction. Convert with a conversion call named after the target type: `int(x)`, `i32(x)`, `u8(x)`, `float(x)`, `f32(x)`. Integer-to-integer conversion truncates or sign-extends; float-to-integer truncates toward zero; integer-to-float rounds to nearest.

Arithmetic semantics, including wrapping and traps, are in [§5.3](#53-arithmetic).

### 3.3 Bool and char

- `bool` holds `true` or `false`. Conditions (`if`, `while`, `for`, `match` guards, `and`, `or`, `not`) must be exactly `bool`; there is no implicit truthiness.
- `char` holds one Unicode scalar value (U+0000–U+D7FF or U+E000–U+10FFFF) in 32 bits. `int(c)` gives its code point and `char(n)` converts an integer back.

### 3.4 Strings

`string` is UTF-8 text. It is a resource: a string built at run time owns its buffer and frees it at the end of its owner's scope. A string literal refers to static memory and allocates nothing until it is modified or copied.

| Operation | Meaning |
|---|---|
| `s.len()` | length in **bytes** |
| `s[i]` | the byte at offset `i`, a `u8`; traps if out of bounds |
| `s[lo..hi]` | the substring of bytes `lo` to `hi`, a `string`; traps if out of bounds |
| `a + b` | concatenation (a new string) |
| `a == b`, `a < b` | byte-wise equality and ordering |
| `for(ch in s)` | each `char`, decoded from UTF-8 |
| `for(i, ch in s)` | each `char` with the byte offset where it starts |
| `s.push(t)`, `s.push_char(c)` | append in place (needs a mutable `s`) |

The full method set is in [§12.5](#125-strings-and-characters).

### 3.5 Optionals

`?T` is a value that is either a `T` or absent. `none` is the absent value.

```lowlight
bind main = fn(): {
	let name?: ?string = "Ada"
	let empty?: ?string = none
	println(name? ?? "nobody")
	println(empty? ?? "nobody")
}
```

**Rules.**

1. **No nesting.** `??T` is not a type. Optionals inside optionals cannot arise: a generic parameter can never be instantiated with an optional ([§8.3](#83-type-parameters-are-never-optional)).
2. **Naming.** A name holding `?T` ends in `?` ([§2.5](#25-identifiers)).
3. **Implicit wrapping.** A `T` is accepted wherever a `?T` is expected and is wrapped automatically. This is the only implicit conversion in the language besides interface boxing ([§3.11](#311-interfaces)).
4. **Using the value.** You cannot use a `?T` as a `T`. Unwrap it with `if(x?)`, `if(e as v)`, `??`, optional chaining `?.`, or a `match` on `none`. See [§5.8](#58-optional-chaining-and-fallback) and [§6.2](#62-if).
5. **Recursive types.** A struct may contain an optional field of its own type, `next?: ?Node`; such a field is stored behind a pointer. A struct may not contain itself directly.

### 3.6 Collections

**Lists** `[T]` (the prelude type `List[T]`): growable, heap-allocated, contiguous. A resource; dropping a list drops every element. Literals: `[]`, `[1, 2, 3]`. `xs[i]` is bounds-checked and traps when out of range; `xs.get(i)` returns `?T` instead.

**Arrays** `[N: T]` (the prelude type `Array`): exactly `N` elements stored inline, no heap allocation. `N` is a positive compile-time constant. Plain if `T` is plain. Literals:
- `[N: v]` repeats `v` `N` times (`T` must be `Copyable` when `N > 1`);
- `[N: a, b, c, ...]` lists exactly `N` elements (any other count greater than one is an error);
- `[a, b, c]` where a `[N: T]` is expected.

An array can be passed where a read-only `[T]` is expected.

**Sets** `#[T]` (`Set[T]`): unordered unique elements; `T` must be `Hashable`. Literal `#[1, 2, 3]`; `#[]` needs a type from context.

**Maps** `#[K: V]` (`Map[K, V]`): hash maps that iterate in insertion order; `K` must be `Hashable`. Literal `#["a": 1, "b": 2]`; `#[]` needs a type from context. `m[k]` means `m.get(k)` and yields `?V`. `m[k] = v` means `m.insert(k, v)`. Iterating a map yields `MapEntry[K, V]` values (fields `key` and `value`), or two variables with `for(k, v in m)`.

**Tuples** `+[A, B, ...]`: fixed groups of two or more values of any types, stored inline. Literal `+[1, "a"]`. Elements are accessed by position, `t.0` and `t.1`; `t[0]` is not allowed. Unpack with `let a, b = unpack t` ([§4.2](#42-bind-const-and-let)). A tuple is plain if all its elements are, and is `Printable` (as `+[1, a]`), equatable, and `Hashable` when all its elements are.

**Ranges** `Range[T]`: `lo..hi` (excludes `hi`) and `lo..=hi` (includes `hi`) over integer types, integer-based distinct types, and `char`. Float ranges are rejected. A range is a plain value: it can be stored, passed, iterated, and used in `when in` patterns. Methods: `contains`, `is_empty`, `len`, `iter`, `to_string`.

### 3.7 Structs

```lowlight
define User struct [
	id: int,
	name: string,
	email?: ?string,
	role: string = "viewer",
]

bind main = fn(): {
	let u = User[id: 1, name: "Ada"]      // `email?` and `role` take their defaults
	println("${u.name} is a ${u.role}")
}
```

**Rules.**

1. A struct is a nominal record of named fields. Fields are accessed with `.field`.
2. A struct literal is `Type[field: value, ...]`, naming every field it sets. Fields may appear in any order.
3. Fields with a default (`= expr`) and optional fields (default `none`) may be omitted from a literal. All other fields are required.
4. A struct with no fields is written `define Empty struct []` and built as `Empty[]`.
5. A generic struct literal may give type arguments: `Pair[int, string][first: 1, second: "a"]`. Usually they are inferred.
6. A struct is plain if all its fields are plain, and a resource otherwise.
7. `define packed Name struct [...]` (or `@packed`) lays fields out with no padding, for binary formats and hardware layouts.
8. Visibility of fields and construction from other packages is in [§4.7](#47-visibility).
9. A struct has no `==`, ordering, hashing, or printing, and copies only if it is plain, until it gets the methods by hand or with `@derive` ([§12.2](#122-deriving-methods)).

### 3.8 Enums

An enum lists alternatives. There are two kinds.

**Tag enums** have no payloads. Each variant has an integer value, counting from 0 or from the previous explicit value plus one. The underlying type is `int` unless written: `define Status enum u8 [...]`.

```lowlight
define Status enum i32 [
	pending = 0,
	active,
	closed = 10,
]
```

Tag enums are plain, so they are `Copyable`; `==`, ordering, hashing, and printing come from `@derive` or hand-written methods ([§12.2](#122-deriving-methods)). `int(Status.active)` converts a variant to its value; integers never convert to enums.

**Tagged unions** have at least one variant carrying a payload (or have type parameters). Each variant is a **unit variant** (`null`) or carries exactly **one** value (`number: float`; use a struct or tuple for several values).

```lowlight
define Json enum [
	null,
	boolean: bool,
	number: float,
	text: string,
	list: [Json],
]
```

In expressions, variants are written with the enum's name: `Json.null`, `Json.number(1.5)`. In `match` patterns they are normally written bare (`when number(n):`; [§6.5](#65-pattern-matching)). Type arguments of a generic enum are inferred from the expected type when possible. A tagged union is a resource if any payload is. It has no integer value, cannot be converted with `int(...)`, and is inspected with `match` ([§6.5](#65-pattern-matching)).

### 3.9 Results

A **result type** describes the outcome of an operation: a success value or one of several failures. It is declared like a tagged union with the word `result`:

```lowlight
define Lookup result [
	value: string,
	missing: string,
	forbidden,
]

bind find = fn(key: string) Lookup:
	if(key == "admin"): forbidden
	else if(key == "name"): value("Ada")
	else: missing(key.copy())

bind main = fn(): {
	let r! = find("name")
	println(r! ?? "unknown")
	let who = find("admin"):
		default: "nobody"
	println(who)
}
```

**Rules.**

1. A result declaration has exactly one member named `value` and at least one other variant (the failures). `value: T` declares the success payload; a bare `value` declares a **unit result**, for an operation that produces nothing when it succeeds. Failures are unit or single-payload variants.
2. A result is constructed like an enum (`Lookup.value(x)`, `Lookup.missing(k)`, `Lookup.forbidden`) or, wherever a result type is expected, by its **bare member name**: `value(x)`, `missing(k)`, `forbidden`. "Expected" means the function's return value, the value of `func <-`, an annotated variable, an argument, or an `if`/`match` branch that produces the result. If a bare name also names a variable or function in scope, it is ambiguous and must be qualified.
3. There is no implicit conversion into a result. A `T` or a failure payload is never wrapped automatically; every result value names its member.
4. Names holding a result end in `!` ([§2.5](#25-identifiers)).
5. Only result types support result chains ([§6.6](#66-result-chains)), `??` ([§5.8](#58-optional-chaining-and-fallback)), and `if(r! as v)` ([§6.2](#62-if)). They may also be inspected with an ordinary `match` on `Name.value(v)` and the failure variants.
6. **Unit results.** A unit result's success is constructed with the bare name `value` (no parentheses). It has no value to produce, so a chain on it is a statement ([§6.6](#66-result-chains)), and `??` and `if(r! as v)` don't apply to it.
7. **`Result[T]`** is the prelude's general-purpose result, `define Result[T] result [value: T, err: Error]`, and **`Status`** its unit counterpart, `define Status result [value, err: Error]`. Both are marked `@must_use`. Nothing about them is special; they are ordinary result declarations ([§12.7](#127-result-and-error)).
8. **Separate failures callers act on.** A function whose callers may need to handle one failure differently from the rest (a missing file when removing it, the end of input versus a read error) declares a result with that failure as its own member, rather than folding it into `err`. Such a failure carries an `Error` like `err` does, so no message, cause, or trace is lost (`not_found: Error`). Failures callers handle alike stay together in `err`. See `CONVENTIONS.md`.

`result` is a contextual word; `let result = 1` is fine.

### 3.10 Distinct types

`define Name BaseType` creates a new type with the same representation as `BaseType` that does not mix with it:

```lowlight
define UserId int
define Pair +[int, string]

bind Pair.first = fn(self) int: self.0

bind main = fn(): {
	let id = UserId(42)
	println(int(id))
	let p: Pair = +[7, "seven"]
	println(p.first())
}
```

Build a value with a conversion call (`UserId(42)`) or a literal where the distinct type is expected. Unwrap with a conversion to the base type (`int(id)`). Distinct types may have methods, and inherit the category of their base. A distinct tuple keeps positional access (`p.0`).

### 3.11 Interfaces

An interface is a set of method signatures:

```lowlight
define Shape interface [
	area: fn(self) float,
	scale: fn(&self, by: float),
]
```

**Rules.**

1. **Structural conformance.** A type implements an interface when it has every listed method with matching parameter types, return type, and receiver mode. It never declares that it does.
2. **Receivers.** Methods take `self` (read-only), `&self` (mutable), or `own self` (consuming). `Self` inside an interface stands for the implementing type.
3. **Interface values.** An interface type used as a value type is a dynamically dispatched box holding the concrete value and a method table. It is a resource. Box a value explicitly with a conversion call, `Shape(circle)`.
4. **Implicit boxing.** A concrete value passed where an interface is expected is converted automatically; for a read-only parameter it is borrowed without allocation.
5. **Generic bounds.** Interfaces also constrain type parameters, `[T: Printable + Equatable]`, with static dispatch ([§8.2](#82-bounds)).
6. **Sealed interfaces.** An interface with at least one `_`-prefixed method can be implemented only inside its own package ([§4.7](#47-visibility)).
7. An interface that uses `Self` in a parameter can be a bound but not a value type.

The prelude's interfaces, which give meaning to operators and loops, are listed in [§12.1](#121-core-interfaces).

### 3.12 Functions and closures

There are two kinds of function value, plus the C function pointer `extern fn` for foreign code ([§10.5](#105-c-function-pointers)):

- **`fn`**: a function that captures nothing. Its type is `fn(ParamTypes) ReturnType`, or `fn(ParamTypes)` for no result. An `fn` value is a code pointer: it is plain and copies freely. Top-level functions, methods, and function literals written `fn(...)` have `fn` types.
- **`cl`**: a closure. Its type is `cl(ParamTypes) ReturnType`. A closure owns the variables it captures, so it is a resource: it moves, and `.copy()` duplicates it with its captures.

Parameter modes are part of both types: `fn(&[int], own string) bool`.

A function literal is `fn(params) ReturnType: body` or `cl(params) ReturnType: body`, or without `ReturnType` for no result. The body is one expression or a block. Without a written return type, it is inferred from the body and context. A `cl` literal may refer to variables of the enclosing scope; an `fn` literal that does so is an error. Captures follow these rules:

- plain captured values are copied in;
- resource captured values are **moved** in (the outer variable becomes unusable), so write `own` or `.copy()` as needed;
- mutable borrows (`&T` parameters and `if(&x?)` bindings) cannot be captured;
- captured variables are read-only inside the closure.

Conversions:

1. An `fn` converts implicitly to a `cl` with the same parameter and return types wherever a closure is expected, without allocating. `cl(f)` writes the conversion explicitly.
2. A `cl` never converts to an `fn`.
3. A `cl` literal that captures nothing, in a place that doesn't require a closure, draws a warning: it could be an `fn`.

```lowlight
bind apply = fn(xs: [int], f: cl(int) int) [int]: for(x in xs): f(x)

bind main = fn(): {
	let offset = 10
	println(apply([1, 2, 3], cl(x: int) int: x + offset))
}
```

### 3.13 Arenas

An `Arena` is a region allocator: `arena.alloc(value)` moves `value` into the arena and returns a `ref[T]`, a plain reference that is valid as long as the arena is. Dropping the arena (or `arena.reset()`) frees everything in it at once, running any `Drop` implementations in reverse order of allocation.

The compiler guarantees that no `ref[T]` outlives its arena ([§7.6](#76-arenas-and-references)). `Arena.new()` creates one, and `Arena.with_capacity(bytes)` one whose first chunk holds `bytes`. `arena.bytes_used()` reports the bytes handed out since the last reset, including alignment padding.

`Arena` is an ordinary prelude struct written in Lowlight (`std/core/arena.ll`): a bump allocator over chunks that double in size, which records a drop function for each allocated value that isn't plain. `reset` keeps the largest chunk for reuse. Only the lifetime rules of [§7.6](#76-arenas-and-references) are in the compiler; they apply to the struct marked `@lang("arena")`.

### 3.14 Raw pointers

`ptr[T]` is an unmanaged address, a plain value used for foreign-function interfaces and low-level memory work. Creating, reading through, writing through, and offsetting a pointer all require `unsafe` ([§10](#10-unsafe-code-and-foreign-functions)).

### 3.15 Void and never

- `void` means "no value". It may appear only as a return type (of functions, methods, interface methods, `extern` functions, and function types), and may be omitted there. A block whose last statement ends in an explicit `;` has type `void`.
- `never` is the type of expressions that do not complete normally: unwinds (`func <-`, `next <-`, ...), `panic(...)`, `exit(...)`, and loops with no way out. A `never` expression is accepted wherever any type is expected. It is not written in programs.

### 3.16 Type equivalence and implicit conversions

Lowlight is nominal: two struct, enum, distinct, or interface types are the same only if they are the same declaration (with the same type arguments). Structural types (lists, maps, sets, arrays, tuples, functions, optionals) are the same when their parts are.

The language performs only these implicit conversions:

| From | To | Where |
|---|---|---|
| `T` | `?T` | any expected `?T` |
| a concrete type | an interface it implements | any expected interface |
| `[N: T]` | a read-only `[T]` | arguments |
| an integer or float literal | any integer or float type it fits | any expected numeric type |
| a string literal | a distinct type over `string` | any expected distinct type |
| `never` | anything | everywhere |

Everything else, including integer widening, needs an explicit conversion.

---

## 4. Declarations and Program Structure

> **At a glance**
> - A file is a list of top-level declarations: `bind`, `define`, `import`, and `extern`. Order doesn't matter.
> - `bind` names something that never changes; `let` (inside functions only) names a variable.
> - Methods are top-level `bind`s on a type name: `bind Point.norm = fn(self) float: ...`.
> - A directory is a package. `import "std/fs"` makes its exported names available as `fs.name`.
> - A leading `_` makes any name private to its package.

### 4.1 Top-level declarations

A source file contains, in any order:

| Declaration | Form |
|---|---|
| Constant or function | `const NAME = expr` · `bind NAME = expr` · `(const \| bind) name = fn(params) Ret: body` |
| Method or associated function | `(const \| bind) Type.name = fn(self, params) Ret: body` |
| Type | `define Name struct [...]`, `enum [...]`, `result [...]`, `interface [...]`, or `define Name BaseType` |
| Import | `import "path"` · `import "path" as alias` |
| Foreign function or header | `extern name: fn(params) Ret` · `extern "header.h"` |

Any of these may be preceded by attributes ([§4.8](#48-attributes)) and documentation comments ([§2.2](#22-whitespace-and-comments)). Declarations are **order-independent**: a declaration may refer to any other declaration in its package or in an imported package, wherever it appears. Two top-level declarations of the same name in one package are an error. `let` is not allowed at top level.

### 4.2 Bind, const, and let

```ebnf
VarDecl = ( "let" | "bind" | "const" ) Name [ ":" Type ] "=" Expr
        | ( "let" | "bind" | "const" ) Target { "," Target } "=" "unpack" Expr ;
Target  = ( Name | "_" ) [ ":" Type ] ;
```

- `let` declares a **mutable** variable: it may be reassigned, mutated through its methods, and passed as `&`. It is allowed only inside function bodies.
- `bind` declares a **runtime immutable** binding: it may not be reassigned, mutated, or passed as `&`, and neither may anything reachable through it. Top-level `bind`s are runtime-initialized constants or functions.
- `const` declares a **compile-time constant or compile-time-capable function**:
  - A `const` variable/constant is evaluated at compile time during semantic checking (inside a bounded interpreter: default `1_000_000` steps, max call depth `1024`, max heap budget `64 MB`, configurable per declaration with `@comptime_limit(steps: N, memory: M)` or `@comptime_limit(N)`). A local `const` inside a function may reference outer `const`s, compile-time locals in the same `comptime` context, and `const` functions, and fails with an error if it depends on a runtime local.
  - A `const` function or method may be called in **both** compile-time contexts (`const` initializers, `comptime` blocks/expressions, other `const` functions) and runtime `bind` code. Calling host I/O (`print`, `println`, `read_line`), `args()`, `exit()`, `extern` C functions, or user `unsafe` blocks inside a `const` function is an error. (Built-in `std/` `unsafe` pointer/memory intrinsics run on the compiler's bounds- and lifetime-checked simulated heap.)
  - **Conditional generics & `@derive`.** Generic `const` functions/methods (`const Box[T].show = ...`) and `@derive(...)` methods are **conditionally `const`**: an instantiation `Box[Point].show` is compile-time callable when every operation it invokes on its type arguments is `const`; otherwise that specific instantiation becomes runtime-only without breaking compilation. Calling a downgraded instantiation in a `const`/`comptime` context fails with the diagnostic chain to the underlying non-`const` dependency.
  - Inline **`comptime expr` / `comptime { ... }`**: forces evaluation of `expr` or the block at compile time and splices the validated result into runtime code. A `comptime` value that crosses into runtime must satisfy **crossing rules**: Plain values, `string`, `Range`, `[T]`, tuples/structs/enums built from crossable fields (without custom `Drop`), `none` optionals, and non-closure `const` `fn` pointers may cross; `Map`, `Set`, `Arena`, `ref[T]`, `ptr[T]`, `cl` closures, `@comptime_only` functions, and `@comptime_only` types cannot cross and must either stay in `@comptime_only` helpers or be projected into crossable values (`List`, arrays, tuples, plain structs). Static `[T]` and `string` values backed into `.rodata` are immutable read-only slices (`cap == 0`) with copy-on-write behavior on safe `List`/`string` mutation APIs.
  - **Target introspection & top-level `comptime if` pruning.** `comp.TARGET` is an `@comptime_only` constant of type `comp.Target` (with enums `comp.TargetOs`, `comp.TargetArch`, `comp.Endian` and fields `os`, `arch`, `endian`, `is_unix`, `ptr_size`, `ptr_bits`, `triple`) describing the active `--target`. Top-level `comptime if(cond): { ... }` evaluates `cond` before type/signature resolution and keeps only declarations from the selected branch (`import` is forbidden in any top-level `comptime if` branch so package dependencies stay target-independent). Single-file `comptime if(comp.TARGET...)` is preferred over file-suffix target filtering ([§4.6](#46-packages-and-imports)). Untaken target branches can be checked across all canonical targets with `lowlight check --all-targets`, and expanded source can be inspected with `lowlight expand`.
  - **Compile-time file embedding.** `comp.embed_str(path)` (`string`, UTF-8 validated) and `comp.embed_bytes(path)` (`[u8]`) are `@comptime_only` built-ins that read static asset files at compile time and embed them in `.rodata` (`cap == 0`). Paths resolve relative to the declaring `.ll` file and are strictly confined to the declaring package/project root sandbox (rejecting empty paths, NUL bytes, absolute paths, `..`/backslash traversal escapes, symlinks escaping the sandbox, non-regular files, directories, and third-party dependency access outside that dependency's own package directory).
  - **Compile-time reflection and generic field/variant access.** `@comptime_only` queries `comp.type_name[T]()`, `comp.kind_of[T]()` (`comp.TypeKind`), `comp.fields_of[T]()` (`[comp.FieldInfo]`), `comp.variants_of[T]()` (`[comp.VariantInfo]`), `comp.elems_of[T]()` (`[comp.ElemInfo]`), `comp.attrs_of[T]()` (`[comp.AttrInfo]`), and `comp.implements[T, I]()` inspect types statically without RTTI. Inside generic or unrolled code, `v.field(f)` (accepting a `FieldInfo` or `const string`), `t.elem(e)` (`ElemInfo` or `const int`), `variant_index(v)`, `v.is_variant(vr)`, `v.payload(vr)`, `v.as_variant(vr)`, `make_variant[T](vr [, own p])`, and collecting `comptime for(f in comp.fields_of[T]()) T:` / `comptime for(e in ...) T:` manipulate structs, tuples, arrays, enums, and results directly. Descriptors list private `_` members (`is_public == false`), and accessing a private member via reflection outside `T`'s package is an error except inside a generic helper instantiated by `@derive(...)` on `T`'s own declaration ([§12.2](#122-deriving-methods)).
- The type annotation is optional when it can be inferred. It is required when the value alone does not determine a type (`let xs: [int] = []`, `let x?: ?int = none`).
- **Unpacking** splits a tuple, or a distinct type over a tuple, into separate bindings: `let x, y = unpack +[1, 2]`. `_` discards an element. The number of targets must equal the tuple's length. Assignment can unpack too: `a, b = unpack +[b, a]`.
- A local binding may shadow an outer one in an inner block.

**Top-level constants** (`bind`) are evaluated before `main` runs, in dependency order; top-level `const` bindings are evaluated at compile time and require acyclic compile-time dependencies. Resource-valued `bind` constants are released at program exit in reverse order of initialization.

### 4.3 Functions

```ebnf
FuncDecl = ( "bind" | "const" ) Ident [ TypeParams ] "=" FuncLit ;
FuncLit  = ( "fn" | "cl" ) "(" [ Params ] ")" [ Type ] ":" Body ;
Param    = Name ":" [ "&" | "own" ] Type ;
Body     = Expr | Block ;
```

```lowlight
bind add = fn(a: int, b: int) int: a + b

bind greet = fn(name: string): {
	println("Hello, $name")
}

bind main = fn(): {
	greet("Ada")
	println(add(2, 3))
}
```

**Rules.**

1. The return type follows the parameter list. Omit it (or write `void`) when the function returns nothing.
2. The body's value is the return value. `func <- value` returns early ([§6.7](#67-unwinds-and-labels)).
3. A body without braces must be an expression. Assignments and declarations are statements ([§5.12](#512-assignment)), so a function whose body assigns needs braces: `bind reset = fn(&self): { self.count = 0 }`. (The bodies of `if`, loops, and `match` arms may be a single assignment.)
4. Parameters are read-only borrows by default; `&T` and `own T` select the other modes ([§7.3](#73-parameter-modes)). Parameter names follow the `?` and `!` naming rules.
5. A function declared with type parameters is generic ([§8](#8-generics)).
6. Functions may be recursive and mutually recursive. A function whose return type is omitted and inferred cannot take part in a recursive cycle; give it an explicit return type.
7. Functions are values: `bind f = add` has type `fn(int, int) int`, and converts to `cl(int, int) int` where a closure is expected ([§3.12](#312-functions-and-closures)).
8. Declaring a top-level function with `const` instead of `bind` allows it to run both at compile time and at runtime, subject to [§4.2](#42-bind-const-and-let).

### 4.4 Methods and associated functions

A method is a function attached to a type, declared at top level with `bind` or `const` and the type's name:

```lowlight
define Counter struct [
	count: int,
]

bind Counter.new = fn() Counter: Counter[count: 0]          // associated function
bind Counter.value = fn(self) int: self.count               // read-only receiver
bind Counter.bump = fn(&self): { self.count += 1 }           // mutable receiver
bind Counter.into_count = fn(own self) int: self.count      // consuming receiver

bind main = fn(): {
	let c = Counter.new()
	c.bump()
	c.bump()
	println(c.value())
	println(c.into_count())
}
```

**Rules.**

1. **Receivers.** The first parameter `self`, `&self`, or `own self` makes the function a method; its receiver mode works like the corresponding parameter mode. `self: Type` may also be written explicitly.
2. **Calls.** `value.method(args)` calls a method. For an `&self` method the receiver is borrowed mutably without writing `&` (the receiver must be a mutable place). For an `own self` method the receiver is moved, and the variable becomes unusable.
3. **Associated functions** have no receiver and are called on the type: `Counter.new()`.
4. **Any type.** Methods may be attached to structs, enums, results, distinct types, primitive types (`bind int.double = fn(self) int: self * 2`), and generic types (`bind Stack[T].peek = fn(self) ?T: ...`). A method may have its own type parameters: `bind List[T].map[U] = ...`.
5. **Extension methods.** A package may add methods to types it did not define, including types from other packages and primitives. Three rules keep this coherent:
   - no two declarations anywhere in a program may attach the same method name to the same type;
   - an extension method cannot see the type's private (`_`) fields, variants, or methods;
   - an extension method cannot implement a sealed interface's private methods.
6. **Lookup order.** `v.name(args)` resolves to, in order: a method of `v`'s type; a method required by an interface bound on `v`'s type parameter; a function `name` in scope whose first parameter accepts `v` (uniform function call syntax).

### 4.5 Type declarations

```ebnf
TypeDecl = "define" [ "packed" ] Name [ TypeParams ] TypeBody ;
TypeBody = "struct" "[" [ Fields ] "]"
         | "enum" [ IntType ] "[" [ Variants ] "]"
         | "result" "[" Variants "]"
         | "interface" "[" [ MethodSigs ] "]"
         | Type ;                                    (* distinct type *)
```

Each kind is described in [§3](#3-types): structs [§3.7](#37-structs), enums [§3.8](#38-enums), results [§3.9](#39-results), distinct types [§3.10](#310-distinct-types), and interfaces [§3.11](#311-interfaces). Type declarations may also appear inside a function body, where they are local to that function; local type declarations cannot be generic.

### 4.6 Packages and imports

**Packages.** Every directory containing `.ll` files is one **package**. All files in the directory share one namespace: a top-level name declared in one file is visible in every other file of the package without importing it, and files may refer to each other freely. Files do not declare a package name; identity comes from the directory and the manifest ([§14](#14-packages-manifests-and-dependencies)).

When a single `.ll` file outside any project is compiled directly (`lowlight run script.ll`), it forms a package by itself; sibling files are not pulled in.

**File selection.** Within a package directory:

- files named `*_test.ll` belong to the package only when running tests ([§11](#11-testing));
- files named `name_<os>.ll`, `name_<arch>.ll`, `name_<os>_<arch>.ll`, or `name_unix.ll` are included only when compiling for a matching target (for example `fs_linux.ll`, `fs_windows.ll`, `net_unix.ll`, `atomic_arm64.ll`); single-file `comptime if(comp.TARGET...)` ([§4.2](#42-bind-const-and-let)) is preferred for conditional platform code, while suffix filtering remains supported;
- every other `.ll` file is always included.

**Imports.**

```lowlight
import "std/fs"
import "std/math" as m
```

1. An import names a package **directory**, without a `.ll` suffix. `import "std/fs.ll"` is an error.
2. Its exported declarations become available through a **qualifier**: by default the last path segment (`fs.read_file(...)`), with characters that cannot appear in identifiers replaced by `_` (`my-lib` becomes `my_lib`), or the alias given with `as`. Imported names are never injected unqualified.
3. Two imports in one file with the same qualifier are an error; give one an alias.
4. Imports are per file: each file lists what it uses, even though other declarations are shared across the package.
5. The package dependency graph must be acyclic. A cycle of imports is an error that names the cycle.
6. A package that declares `main` cannot be imported.
7. Paths resolve to standard library packages (`std/...`), packages of the current project (`<project>/sub/dir`), declared dependencies (`<dep>`, `<dep>/sub`), and workspace members. Relative paths (`./helpers`, `../shared`) are allowed only for code outside any project. See [§14.3](#143-import-path-resolution).

The prelude `std/core` is imported implicitly into every file, unqualified ([§12](#12-the-prelude)).

### 4.7 Visibility

There are no visibility keywords. A name is **private to its package** if it begins with `_`, and **exported** otherwise. This applies to top-level declarations, struct fields, enum and result variants, methods, and interface methods.

Outside the declaring package:

1. a private top-level name cannot be referenced;
2. a private field cannot be read, written, or set in a struct literal;
3. a struct whose private fields are not all optional or defaulted cannot be built with a struct literal at all, so the package must provide a constructor such as `Type.new(...)`;
4. a private variant cannot be constructed, matched, or named;
5. a private method cannot be called by any route (`v._m()`, `Type._m(v)`, or `pkg.Type._m(v)`);
6. an interface with at least one private method (a **sealed** interface) cannot be implemented.

```lowlight
define Counter struct [
	name: string,
	_count: int = 0,
	_token: int,
]

// Other packages cannot write Counter[...] because `_token` has no default,
// so the package exports a constructor:
bind Counter.new = fn(name: own string, token: int) Counter: Counter[name: own name, _token: token]
```

Child directories are separate packages: `a/b` cannot see `a`'s private names, and `a` cannot see `a/b`'s.

### 4.8 Attributes

An attribute is `@name` or `@name(args)` written before a declaration (or a struct field).

| Attribute | Applies to | Meaning |
|---|---|---|
| `@test`, `@test("description")` | function | a test case ([§11](#11-testing)) |
| `@test_only` | any declaration | may be used only from tests; stripped from normal builds |
| `@track_caller` | function, method | panics, `Error.new`, and `caller_*()` inside report the **caller's** location; chains through nested `@track_caller` functions |
| `@must_use`, `@nodiscard` | function, method, type | discarding the returned value (or any value of the type) is an error; bind it or write `let _ = ...` |
| `@deprecated`, `@deprecated("message")` | declaration | each use produces a warning (with the message, if given) |
| `@inline`, `@noinline` | function, method | request or forbid inlining; no semantic effect |
| `@packed` | struct | lay fields out without padding (same as `define packed`) |
| `@derive(I, ...)` | struct, enum, result | generate the listed interfaces' methods via their `@derivable` generic implementations ([§12.2](#122-deriving-methods)) |
| `@derivable(m: fn_path, ...)` | interface | attach generic compile-time derivation function(s) to an interface so structs/enums/results can `@derive` it ([§12.2](#122-deriving-methods)) |
| `@comptime_only` | `const` decl, type | restrict a `const` function/constant or type (`struct`, `enum`, `interface`, distinct) strictly to compile time; rejected in runtime code, runtime signatures, and non-`@comptime_only` crossing values |
| `@comptime_limit(steps: N, memory: M)` | `const` decl | set the compile-time step and/or memory byte budget for evaluating that `const` function or constant (defaults: `1_000_000` steps, `64 MB` heap; `@comptime_limit(N)` sets steps) |
| `@lang("hook")` | prelude declaration | binds a declaration to a language feature ([§12.1](#121-core-interfaces)); reserved for `std/core` |

An attribute on a kind of declaration it does not apply to is an error.

### 4.9 The entry point

A package that declares a top-level `main` is a program; one that doesn't is a library.

- `main` takes no parameters and returns nothing or an `int`.
- Returning normally exits with status `0`, or with the returned `int`. A panic exits with status `1` ([§9.3](#93-panics)). `exit(code)` exits immediately with `code`.
- Command-line arguments are available through `args()`.

---

## 5. Expressions

> **At a glance**
> - Operands are evaluated left to right, once.
> - Integer arithmetic wraps; division by zero and out-of-range shifts trap.
> - `==` and `<` work on numbers, strings, and types that implement `Equatable` / `Comparable`; structs and enums get them from `@derive` or a hand-written method.
> - `xs[i]` traps when out of range; `xs.get(i)` returns `?T`. `m[k]` on a map is always `?V`.
> - `a?.b` reaches through an optional; `x ?? fallback` supplies a default for an optional or a result.

### 5.1 Evaluation order and precedence

Every expression's operands, a call's arguments, and a literal's elements are evaluated **left to right**, each exactly once. `and`, `or`, `??`, and conditional constructs evaluate only the operands they need.

| Precedence | Operators | Associativity |
|---|---|---|
| 1 (tightest) | `.` `?.` call `()` index/slice `[]` | left |
| 2 | prefix `-` `not` `~` `*` `&` `own` `unsafe` `comptime` | right |
| 3 | `*` `/` `%` | left |
| 4 | `+` `-` | left |
| 5 | `<<` `>>` | left |
| 6 | `..` `..=` | none |
| 7 | `<` `<=` `>` `>=` | none |
| 8 | `==` `!=` | none |
| 9 | `&` | left |
| 10 | `^` | left |
| 11 | `\|` | left |
| 12 | `and` | left |
| 13 | `or` | left |
| 14 | `??` | right |
| 15 (loosest) | unwind `<-` | right |

"None" means the operator cannot be chained: `a < b < c` and `a == b == c` are errors; write `a < b and b < c`. Comparisons bind more tightly than the bitwise operators `&`, `^`, and `|`, so `x & mask == 0` means `x & (mask == 0)`, a type error; write `(x & mask) == 0`.

There is no cast operator. `as` is used only in imports, `extern` aliases, and `if(e as v)`.

### 5.2 Literals and constructors

| Expression | Builds |
|---|---|
| `42`, `0xFF`, `3.5`, `'c'`, `"text"`, `` `text` ``, `true`, `none` | literals ([§2.7](#27-literals)) |
| `Type[field: value, ...]` | a struct ([§3.7](#37-structs)) |
| `Type.variant`, `Type.variant(payload)` | an enum or result value ([§3.8](#38-enums), [§3.9](#39-results)) |
| `value(x)`, `missing(k)` where a result is expected | a result value by its bare member ([§3.9](#39-results)) |
| `[a, b]`, `[]` | a list |
| `[N: v]`, `[N: a, b, ...]` | an array |
| `#[a, b]`, `#[k: v]`, `#[]` | a set or a map |
| `+[a, b]` | a tuple |
| `lo..hi`, `lo..=hi` | a range |
| `fn(params) Ret: body` · `cl(params) Ret: body` | a function or closure value ([§3.12](#312-functions-and-closures)) |
| `extern fn(params) Ret: body` | a function C can call ([§10.5](#105-c-function-pointers)) |
| `cl(f)` | a function converted to a closure ([§3.12](#312-functions-and-closures)) |
| `Iface(value)` | an interface box ([§3.11](#311-interfaces)) |
| `T(x)` for a primitive or distinct `T` | a conversion ([§5.10](#510-conversions)) |

An empty literal (`[]`, `#[]`) and `none` need their type from context. The element type of a non-empty literal is the type of its elements, which must agree (after literal adaptation and implicit conversions, [§3.16](#316-type-equivalence-and-implicit-conversions)). Elements that move existing resource variables must say `own` ([§7.2](#72-moves)).

### 5.3 Arithmetic

| Operation | Integers | Floats |
|---|---|---|
| `+` `-` `*`, unary `-` | two's-complement **wrapping** in the operand type | IEEE-754 |
| `/` | truncates toward zero; **traps** if the divisor is zero; `MIN / -1` wraps to `MIN` | IEEE-754 (`1.0 / 0.0` is `inf`) |
| `%` | sign follows the dividend; **traps** on a zero divisor; `MIN % -1` is `0` | not defined |
| `<<` `>>` | **traps** unless `0 <= shift < bit width`; `>>` is arithmetic for signed types, logical for unsigned | not defined |
| `&` `\|` `^` `~` | bitwise | not defined |

Both operands of a binary arithmetic or bitwise operator must have the same type (a literal adapts to the other operand). These rules hold in every build mode, including `--release`; integer overflow is never undefined behavior.

On numbers, `+` and `+=` are the built-in operations above. On any other type, `a + b` calls `a.add(b)`, which the type provides by implementing `Addable` (`add: fn(self, other: Self) Self`). `a += b` calls `a.add_assign(b)` when the type implements `AddAssignable` (`add_assign: fn(&self, other: Self)`), and otherwise means `a = a + b`. `string` implements both: `+` concatenates into a new string and `+=` appends in place. The numeric types implement both as well, so generic code can require them ([§8.2](#82-bounds)).

### 5.4 Equality and ordering

`a == b` and `a != b` require both operands to have the same type, and that type to support equality:

- numbers, `bool`, `char`, and `string` (byte-wise);
- optionals of equatable types (`none == none`; `x? == none` tests absence);
- tuples, compared element by element;
- distinct types whose base supports equality;
- any type with an `eq: fn(self, other: Self) bool` method (`Equatable`), or with a `cmp` method (`a == b` exactly when `a.cmp(b) == 0`).

Structs, enums, and results have `==` only through such a method, written by hand or by `@derive(Equatable)` ([§12.2](#122-deriving-methods)). Lists, sets, and maps have no `==`.

`<`, `<=`, `>`, `>=` require a type that supports ordering: numbers, `char`, `string` (byte-wise lexicographic), and types with a `cmp: fn(self, other: Self) int` method (`Comparable`), which returns a negative number, zero, or a positive number. `bool` and enums are not ordered.

In generic code, `==` needs the bound `T: Equatable` and `<` needs `T: Comparable` ([§8.2](#82-bounds)).

### 5.5 Logic and bitwise operators

`and`, `or`, and `not` take and produce `bool`; `and` and `or` short-circuit. There are no `&&`, `||`, or `!` operators. The bitwise operators `&`, `|`, `^`, and `~` work on integers of one type.

### 5.6 Ranges

`lo..hi` is the half-open range `lo <= x < hi`; `lo..=hi` includes `hi`. Both bounds must have the same integer, integer-based distinct, or `char` type; float ranges are errors. A range whose upper bound is below its lower bound is empty. Ranges are values of type `Range[T]` ([§3.6](#36-collections)); in a `match` pattern a range is used as `when in lo..hi:` ([§6.5](#65-pattern-matching)).

### 5.7 Indexing and slicing

| Expression | On | Result |
|---|---|---|
| `xs[i]` | list `[T]`, array `[N: T]` | the element; **traps** unless `0 <= i < len`; assignable when `xs` is mutable |
| `s[i]` | `string` | the byte at offset `i`, a `u8`; **traps** when out of range |
| `m[k]` | map `#[K: V]` | `?V` (same as `m.get(k)`); assignment `m[k] = v` inserts or replaces |
| `c[i]` | a type implementing `Indexable[T]` | `?T`, via `c.get(i)` |
| `c[k]` | a type implementing `KeyIndexable[K, V]` | `?V`, via `c.get(k)` |
| `p[i]` | `ptr[T]` (in `unsafe`) | the element at `p + i * type.size_of[T]()`, unchecked |
| `xs[lo..hi]`, `xs[lo..=hi]` | list, array, string | the elements from `lo` up to `hi`; **traps** unless `0 <= lo <= hi <= len` |
| `xs[..hi]`, `xs[lo..]` | list, array, string | a missing bound is `0` or `len` |

The index must be an `int` (or an integer literal). A slice of a list or array is a read-only view of the original elements; a slice of a string is a `string`.

### 5.8 Optional chaining and fallback

**Optional chaining.** On an optional `?T`, `a?.field` and `a?.method(args)` evaluate to `none` if `a` is `none`, and otherwise access the member and wrap the result in an optional. A name ending in `?` may use plain `.` (`user?.name` and `user?.name` are the same; the `?` belongs to the name). Once a chain short-circuits, the rest of it is skipped:

```lowlight
define User struct [
	name: string,
	manager?: ?User,
]

bind main = fn(): {
	let u = User[name: "Ada", manager?: User[name: "Grace"]]
	println(u.manager?.name ?? "none")
	println(u.manager?.manager?.name ?? "none")
}
```

A method called through `?.` must take `self`; `&self` and `own self` methods cannot be called through an optional chain.

**Fallback `??`.** `a ?? b`:

- if `a` is an optional `?T`: `a`'s value if present, otherwise `b`. `b` may be a `T`, or another `?T` (making the whole expression `?T`, so `a? ?? b? ?? 0` chains).
- if `a` is a result: `a`'s success value, or on any failure, `b`. The failure is dropped. `b` must be the success type.
- `b` is evaluated only when needed, and may be an unwind: `let v = x? ?? func <- fallback`. After such a guard, `v` is a plain `T`. An unwind is allowed on the right of `??` only when the `??` expression is the entire right-hand side of a `let`, `bind`, or assignment statement; elsewhere (inside a call argument or a struct literal), bind the value first.

### 5.9 Member access and calls

- `v.field` reads a field. On a mutable place, `v.field = x` and `v.field += x` assign to it.
- `v.method(args)` calls a method ([§4.4](#44-methods-and-associated-functions)); a `&self` receiver is borrowed automatically.
- `f(args)` calls a function or function value. Arguments are evaluated left to right.
- An argument for a `&T` parameter must be written `&place`; one for an `own T` parameter that moves an existing variable must be written `own place` ([§7.3](#73-parameter-modes)).
- Explicit type arguments go before the argument list: `type.size_of[int]()`, `parse[Config](text)`. They are usually inferred.
- Method calls on a separate line may continue a chain by starting with `.` ([§2.3](#23-statement-separation)).

### 5.10 Conversions

A conversion is written as a call to the target type.

| From → to | Write | Behavior |
|---|---|---|
| integer → integer | `u8(x)`, `int(x)` | truncate or sign/zero-extend |
| float → integer | `int(f)` | truncate toward zero |
| integer ↔ float, float ↔ float | `float(n)`, `f32(x)` | nearest representable value |
| `char` → integer · integer → `char` | `int(c)` · `char(n)` | code point; `char(n)` **panics** unless `n` is a Unicode scalar value (`0..=0x10FFFF`, excluding the surrogates `0xD800..=0xDFFF`) |
| tag enum → integer | `int(Status.active)` | the variant's value |
| base → distinct · distinct → base | `UserId(5)` · `int(id)` | no change in representation |
| concrete → interface | `Shape(circle)` | boxes the value ([§3.11](#311-interfaces)) |

Converting to `string` is `.to_string()` or interpolation. Parsing a string is `parse_int`, `parse_float`, or `std/strconv` ([§12.3](#123-universe-functions)).

### 5.11 Blocks

`{ stmt; stmt; expr }` is an expression. It opens a scope, runs its statements in order, and evaluates to its final expression ([§2.3](#23-statement-separation)). A block without a final expression, or whose final expression is followed by an explicit `;`, has type `void`. Resource variables declared in the block are released when it ends ([§7.5](#75-destruction)). A labeled block, `name: { ... }`, can be left early with `name <- value` ([§6.7](#67-unwinds-and-labels)). `unsafe { ... }` is a block in which unsafe operations are allowed ([§10](#10-unsafe-code-and-foreign-functions)).

### 5.12 Assignment

Assignment is a statement, not an expression:

```ebnf
Assign = Place ( "=" | "+=" | "-=" | "*=" | "/=" | "%=" | "&=" | "|=" | "^=" | "<<=" | ">>=" ) Expr
       | Place { "," Place } "=" "unpack" Expr ;
Place  = Name | Place "." Name | Place "[" Expr "]" | "*" Expr ;
```

1. The target must be a **mutable place**: a `let` variable, a field or element of one, a field reached through `&self` or a `&T` parameter, or (in `unsafe`) a pointer dereference.
2. `a op= b` means `a = a op b`, with `a` evaluated once.
3. The right side is evaluated first. Assigning to a place that holds a resource releases the old value after the new one is computed, so `s = s + "!"` is safe.
4. Assigning an existing resource variable moves it, so write `own`: `slot = own value`.
5. `a, b = unpack t` assigns each element of a tuple; `a, b = unpack +[b, a]` swaps.

---

## 6. Control Flow

> **At a glance**
> - `if`, `while`, `for`, and `match` are expressions. Headers are written `keyword(...)` and followed by `:` and a body (an expression or a block).
> - `if` without `else` produces `?T`. `while` produces one value; `for` collects a list.
> - `match` must cover every case and always ends with a `default` arm. Variants are named bare: `when pending:`, `when num(n):`.
> - A result chain, `expr: when failure: ... default ...`, evaluates to the success value.
> - `<-` leaves a construct: `func <-`, `for <-`, `while <-`, `next <-`, `name <-`.

### 6.1 Bodies

Every control construct has the shape `keyword(header) [EvalType]: Body`. A **body** is a single expression (which may be a single assignment or an unwind) or a `{ ... }` block. When a body is on the next line it is indented; the formatter decides between the two layouts.

The optional **evaluation type** written before the `:` states the construct's result type, as in `if(c) int: a else: b` or `while(true) string: ...`. It is needed only when the type cannot be inferred.

A construct in **statement position** (its value unused) runs for its effects only. In that position, `if` needs no `else`, a `for` loop collects nothing and allocates nothing, and a `while` loop produces nothing.

### 6.2 If

```ebnf
IfExpr  = "if" "(" Cond ")" [ Type ] ":" Body { "else" "if" "(" Cond ")" [ Type ] ":" Body } [ "else" ":" Body ] ;
Cond    = Expr                          (* a bool *)
        | [ "&" ] OptName               (* narrow an optional variable *)
        | [ "&" ] Expr "as" Name ;      (* bind an optional's or a result's value *)
```

**Values.** With `else`, both branches must have compatible types and the `if` has that type; an unwinding branch (type `never`) is compatible with anything. Without `else`, an `if` used as a value has type `?T` (the branch's value or `none`). An explicit non-optional evaluation type without `else` is an error.

**Conditions.**

1. **Boolean.** The condition must be `bool`.
2. **Optional narrowing, `if(x?)`.** If the optional variable `x?` is present, the branch runs with `x` (no `?`) bound to its value as a read-only view. `if(&x?)` binds `x` as a mutable borrow of the value inside `x?`, which must be mutable. While the branch runs, `x?` itself cannot be used.
3. **Binding, `if(e as v)`.** `e` is any optional expression or result expression. The branch runs when `e` holds a value (for a result, its success value), with `v` bound to it. `else` runs otherwise and binds nothing. `if(&e as v)` binds `v` mutably and requires `e` to be a mutable place.

```lowlight
bind main = fn(): {
	let score = 72
	let grade = if(score >= 90): "A" else if(score >= 70): "B" else: "C"
	println(grade)

	let bonus? = if(score > 50): 10          // ?int
	println(bonus? ?? 0)

	let n? = parse_int("41")
	if(&n?): n += 1
	if(n?): println("n is $n")

	if(parse_int("x") as v): println(v) else: println("not a number")
}
```

### 6.3 While

```ebnf
WhileExpr  = [ Label ":" ] "while" "(" LoopHeader ")" [ Type ] ":" Body [ "else" ":" Body ] ;
LoopHeader = Expr                                       (* a bool condition *)
           | Binding [ "," Binding ] "in" Expr ;        (* iteration *)
Binding    = [ "&" ] Name ;
```

`while` runs its body while the condition holds, or once per element when the header is an iteration ([§6.4](#64-for) describes iteration). It produces **at most one value**:

- `while <- v` leaves the loop and makes `v` the loop's value;
- with `else`, the loop's value is `T`: the `while <- v` value, or the `else` body's value if the loop ends without one;
- without `else`, the loop's value is `?T`: `none` if it ends without `while <- v`;
- `while(true) T: ...` (a loop with no normal exit) has type `T` and needs no `else`.

```lowlight
bind main = fn(): {
	let items = [4, 8, 15, 16]
	let first_odd = while(x in items) int: {
		if(x % 2 == 1): while <- x
	} else: -1
	println(first_odd)

	let i = 0
	while(i < 3): {
		println(i)
		i += 1
	}
}
```

### 6.4 For

```ebnf
ForExpr = [ Label ":" ] "for" "(" LoopHeader ")" [ Type ] ":" Body [ "else" ":" Body ] ;
```

`for` iterates, and used as a value it **collects**: the value of each iteration's body is pushed into a collection, and the collection is the loop's value.

- With no evaluation type the collection is a list `[T]` of the body's type. An evaluation type may name any `Collectable` type, such as `#[T]`, `#[K: V]` (pushing `MapEntry` values), or `[T]`. Any other type is an error.
- `next <-` skips the current iteration without pushing anything.
- `for <-` stops the loop and yields what has been collected so far.
- An `else` body runs when the loop finishes without `for <-`.

**Headers.** `for(cond)` loops while `cond` is true. `for(x in e)` iterates `e`:

| `e` | `for(x in e)` | `for(a, b in e)` |
|---|---|---|
| `lo..hi`, `lo..=hi`, `Range[T]` | each value | n/a |
| `[T]`, `[N: T]` | each element | `a` = index, `b` = element |
| `string` | each `char` | `a` = byte offset, `b` = `char` |
| `#[T]` | each element | n/a |
| `#[K: V]` | each `MapEntry[K, V]` (`.key`, `.value`) | `a` = key, `b` = value |
| any `Iterable[T]` | each `next()` value until `none` | n/a |

Loop variables are read-only views of the elements. Write `&x` (or `k, &v`) to bind mutable borrows of a list's, array's, or map's elements; the collection must be mutable. The collection cannot be modified by other means while it is being iterated.

```lowlight
bind main = fn(): {
	let squares = for(n in 1..=4): n * n
	println(squares)

	let evens = for(n in 0..10) #[int]: {
		if(n % 2 != 0): next <-
		n
	}
	println(evens.len())

	let scores = [3, 5, 8]
	for(&s in scores): s *= 10
	for(i, s in scores): println("$i: $s")
}
```

### 6.5 Pattern matching

```ebnf
MatchExpr = "match" [ "(" Expr { "," Expr } ")" ] [ Type ] ":"
            { "when" Cases ":" Body } "default" [ Cases ] ":" Body ;
Cases     = Case { "," Case } ;
Case      = Pattern                                   (* one subject *)
          | Pattern { "," Pattern }                   (* several subjects: one pattern each *)
          | "(" Pattern { "," Pattern } ")" ;         (* several subjects: one combination of several *)
```

A `match` compares one or more **subjects** against the arms in order and evaluates the first arm that matches. It has no braces; each arm starts with `when` and the last one with `default`.

```lowlight
define Status enum [
	pending,
	active,
	closed,
]

define Shape enum [
	circle: float,
	square: float,
	point,
]

bind label = fn(s: Status) string:
	match(s):
		when pending: "waiting"
		when active: "running"
		default closed: "done"

bind area = fn(s: Shape) float:
	match(s):
		when circle(r): 3.14159 * r * r
		when square(side): side * side
		default point: 0.0

bind main = fn(): {
	println(label(Status.active))
	println(area(Shape.square(3.0)))
	let n = 42
	let kind = match(n):
		when 0: "zero"
		when 1, 2, 3: "small"
		when in 4..100: "medium"
		default: "large"
	println(kind)
	let sign = match:
		when n < 0: "negative"
		when n == 0: "zero"
		default: "positive"
	println(sign)
}
```

**Patterns.**

| Pattern | Matches |
|---|---|
| a literal (`3`, `'x'`, `"text"`, `true`, `none`) | a subject equal to it |
| `_` | anything |
| a variant, bare: `pending`, `circle(r)` | that variant of the subject's enum or result type, binding its payload |
| a variant, qualified: `Status.pending`, `Shape.circle(r)` | the same, written with the type name |
| `in container` | when `container.contains(subject)` (`Containable[T]`): ranges, lists, arrays, sets, maps (keys), strings (substrings) |
| `method(args)` | when `subject.method(args)` returns `true`; the method must take `self` and return `bool` |

**Variant names.** Inside a pattern, a variant of the subject's enum or result type is normally written **bare**. The qualified form is always allowed and is required in two cases:

- a bare unit name (`pending`) that is also the name of a variable in scope with the subject's type;
- a bare payload pattern without type arguments (`len(n)`) that is also the name of a method of the subject's type.

Both are reported as ambiguous.

**Payload bindings.** `circle(r)` binds `r` to the payload as a read-only view (a copy, if plain). `circle(&r)` binds a mutable borrow (the subject must be a mutable place). `word(own w)` moves the payload out, after which the subject is consumed. `circle(_)` matches without binding. Binding names follow the `?` and `!` rules. A payload pattern holds only bindings and `_`: it cannot test the payload's value (match on the payload inside the arm instead). Each variant may appear in at most one arm of a `match`.

**Optional subjects.** A subject of type `?T` is matched by `none`, by patterns for `T`'s values, and by `in` patterns whose container holds `T` or `?T`. Method patterns cannot be used on an optional subject.

**Forms.**

- **One subject**, `match(x):`. Each `when` lists one or more patterns separated by commas; the arm matches if any of them does.
- **Several subjects**, `match(a, b):`. An arm's patterns match positionally: `when circle(r), 0:`. To give one arm several combinations, parenthesize each: `when (point, _), (_, 0):`.
- **No subject**, `match:` (or `match():`). Each `when` lists `bool` conditions; the arm runs if any is true. It must end with a plain `default:`.

**Exhaustiveness.** Every `match` must handle every possible value, and its last arm must begin with `default`:

- `default:` with no pattern matches everything that remains;
- `default Pattern:` is allowed only when the earlier arms plus this pattern cover every case of an enum, result, or `bool` subject (or every combination, for several subjects), which the compiler checks. Putting the last variant on `default` instead of writing a catch-all means that adding a variant later produces a compile error at every `match` that misses it.

Method and `in` patterns never count toward exhaustiveness.

**Values.** All arms must produce compatible types (unwinding arms are compatible with anything), and the `match` has that type. A `match` in statement position may have arms of any type.

**Compile-time control flow (`comptime if`, `comptime match`, `comptime for`).**

- **`comptime if(cond):`** evaluates `bool` conditions at compile time (inside monomorphized bodies for generic functions) and checks/lowers only the selected branch. In expression position it **must** include an `else` branch (it cannot omit `else` to yield `?T`); omitting `else` is valid only in statement (`void`) position. Optional unwrap headers (`if(x?)` / `if(expr as v)`) are not supported on `comptime if`.
- **`comptime match(scrut):`** evaluates its scrutinee(s) and pattern predicates at compile time, checks **all** arm patterns and full exhaustiveness statically across every arm, and then checks/lowers only the single matching arm with bound pattern variables available as compile-time constants (mutable `&` destructuring is disallowed).
- **`comptime for(var in seq):`** evaluates ranges and compile-time collections (`[N: T]`, `[T]`, `Range`, `string`, `Set`, `Map`) statically and unrolls the body (including collecting `comptime for(...) T:` comprehensions, `next <-`, `for <-`, labeled unwinds, and `else` fallbacks). Loop headers must be compile-time expressions and loop variables cannot be `&`-mutable. Unrolling enforces a per-function statement limit (default `1024`, adjustable via `@comptime_limit(...)`) and warns on large runtime loops over plain data that do not use compile-time specialization.

### 6.6 Result chains

A **result chain** handles the failures of a result-typed expression ([§3.9](#39-results)) and evaluates to its success value. On a unit result it has no value and is a statement.

```ebnf
ResultChain = PostfixExpr ":" { "when" FailureCases ":" Body } ChainEnd ;
ChainEnd    = "default" [ FailureCases ] ":" Body
            | "default" "panic" ;
```

```lowlight
define Lookup result [
	value: string,
	missing: string,
	forbidden,
	expired: int,
]

bind find = fn(key: string) Lookup:
	if(key == "a"): value("apple")
	else if(key == "x"): forbidden
	else if(key == "old"): expired(3)
	else: missing(key.copy())

bind describe = fn(key: string) string: {
	let item = find(key):
		when missing(k): func <- "no $k"
		when forbidden: "(hidden)"
		default expired(days): "expired $days days ago"
	"got $item"
}

bind main = fn(): {
	println(describe("a"))
	println(describe("x"))
	println(describe("old"))
	println(describe("zz"))
	let sure = find("a"): default panic
	println(sure)
}
```

**Rules.**

1. **Subject.** The chain is written directly after a postfix expression (a name, call, member access, or index). The subject must have a result type. Writing `subject:` followed by a `when` or `default` arm starts the chain.
2. **Arms.** Each `when` names one or more failure variants of the subject's type, bare (`missing(k)`, `forbidden`), with the payload-binding rules of [§6.5](#65-pattern-matching). The `value` member is never named: the chain itself evaluates to it. Naming `value`, or a variant that is not a failure of the subject's type, is an error.
3. **Arm bodies** either unwind or evaluate to the success type. On a unit result they are statements, and using the chain as a value is an error:

   ```lowlight
   import "std/fs"

   bind save = fn(path: string, text: string) Status: {
   	fs.write_file(path, text):
   		default err(own e): func <- err(e.wrap("saving \"$path\""))
   	fs.remove_file("$path.bak"):
   		when not_found(_): {}
   		default err(own e): func <- err(e)
   	value
   }
   ```
4. **Ending.** The chain must end with exactly one of:
   - `default failure(...):`, which handles the last failure exhaustively;
   - `default:` with a body, which handles every remaining failure without binding it;
   - `default panic`, with no colon and no body. Each remaining failure then panics at that line with `unhandled failure Type.variant`. When the failure carries an `Error`, the panic is that error wrapped with this message, so its causes and trace print too. On its own, `expr: default panic` unwraps any result.

   A chain that leaves a failure unhandled with no such ending is an error, as is `default panic` when nothing remains to handle.
5. **Ownership.** The chain consumes its subject, and a resource success value is moved out of it. A subject the function doesn't own (a read-only parameter or binding) is read in place instead: the chain binds the value and failures as borrows. Such a chain can produce a plain value, or be a statement; producing a resource value would move out of the borrow, which is an error (chain on `r!.copy()` instead).
6. **Propagation.** To pass a failure to the caller, construct the caller's result type with a bare member: `default err(own e): func <- err(e.wrap("loading"))`.
7. **Nesting.** A chain ends at its `default` arm, so a chain inside a `match` arm does not absorb the `match`'s later arms.

### 6.7 Unwinds and labels

There are no `return`, `break`, or `continue` statements. The **unwind** operator `<-` leaves a construct, and the word before it says which one.

| Unwind | Leaves | Value |
|---|---|---|
| `func <- [v]` | the innermost function or function literal | the return value |
| `for <-` | the innermost `for` loop | none; the loop yields what it has collected |
| `while <- [v]` | the innermost `while` loop | the loop's value |
| `next <- ` | the current iteration of the innermost `for` or `while` | none |
| `<- [v]` | the innermost `{ }` block | the block's value |
| `if <- [v]` · `match <- [v]` | the innermost `if` · `match` | its value |
| `name <- [v]` | the labeled block or loop `name` | its value |
| `next name <-` | the current iteration of the labeled loop `name` | none |

**Labels.** A block or loop may be labeled: `name: { ... }`, `name: for(...)`, `name: while(...)`. Labels are visible only inside the labeled construct.

```lowlight
bind main = fn(): {
	let grid = [[1, 2], [3, 4], [5, 6]]
	let found = outer: while(row in grid) string: {
		for(x in row): if(x == 4): outer <- "found 4"
	} else: "not found"
	println(found)
}
```

**Rules.**

1. An unwind is an expression of type `never`, so it can stand wherever a value is expected: `let user = find(id) ?? func <- missing(id)`.
2. An unwind cannot cross a function boundary. `func <-` inside a function literal returns from that literal.
3. The value must match what the target produces. A bare `func <-` is allowed only in a function that returns nothing.
4. Leaving scopes runs all pending destruction, innermost scope first, in reverse order of declaration ([§7.5](#75-destruction)).

---

## 7. Ownership, Borrowing, and Destruction

> **At a glance**
> - Plain values copy. Resources have one owner and **move**, and moving an existing variable needs `own`.
> - Parameters borrow read-only by default; `&T` borrows mutably (call with `&x`); `own T` takes ownership (call with `own x`).
> - You cannot use a variable after moving it, or touch a value while something else mutably borrows it.
> - A resource is destroyed when its owner's scope ends, in reverse order of declaration. `Drop` adds custom cleanup.
> - `.copy()` makes an independent duplicate of a resource.

The rules in this chapter are checked at compile time. They make use-after-free, double-free, and dangling references impossible in code outside `unsafe`, without any runtime bookkeeping.

### 7.1 Owners

Every value has exactly one **owner**: a variable, a field or element of another value, a temporary, or an arena. When an owner goes away, so does what it owns.

- A **plain** value ([§3.1](#31-plain-values-and-resources)) is copied whenever it is assigned, passed, or returned. Each copy is independent, and nothing needs to be released.
- A **resource** is moved instead. After a move the source no longer holds a value: using it is an error until it is assigned again.

### 7.2 Moves

```lowlight
bind main = fn(): {
	let a = "hello" + " world"
	let b = own a            // moves the string from `a` to `b`
	// println(a)            // error: use of moved variable "a"
	println(b)
	let c = b.copy()         // an independent duplicate; `b` is still usable
	println(b + c)
}
```

**Moving an existing variable requires `own`.** Whenever the value of an existing resource variable (or a field of one) is moved into a new owner, the move is written `own x`:

- initializing or assigning a variable: `let b = own a`, `slot = own a`;
- a struct field, enum payload, or collection element: `User[name: own n]`, `Shape.label(own s)`, `[own a, own b]`;
- an `own` parameter: `consume(own a)`;
- moving a field out of an `own self` receiver: `own self.name`.

A field can be moved out only of an `own self` receiver, and doing so consumes `self`: read any other fields you need first. Moving a field out of any other value is an error; use `.copy()` to duplicate it.

**Moves that need no `own`:**

- a temporary, which has no name: `consume("a" + "b")`, `[make_user()]`;
- the value a function returns, through its final expression or `func <-`;
- the value an unwind carries;
- a variable captured by a closure ([§7.7](#77-closures-and-captures));
- a payload moved out by a pattern, which says `own` in the pattern instead ([§6.5](#65-pattern-matching)).

**Moves and control flow.** If a variable is moved on some paths and not others, it counts as moved afterwards and cannot be used, but it is still destroyed correctly on the paths where it was not moved. A moved `let` variable may be assigned a new value, which makes it usable again. A path that ends in an unwind or a panic does not count: moving `s` and then leaving with `func <-` leaves `s` usable after the `if`. Inside a loop body, a variable declared outside the loop may be moved only if every path through the body assigns it again before the next iteration.

**Copying.** `.copy()` duplicates a resource deeply. All plain types, strings, tuples and collections of `Copyable` elements provide it. A non-plain struct or enum provides it only through a `copy` method, written by hand or by `@derive(Copyable)` ([§12.2](#122-deriving-methods)).

### 7.3 Parameter modes

| Parameter | The call writes | The function gets | The caller afterwards |
|---|---|---|---|
| `x: T` (default) | `f(v)` | a read-only view: a copy if `T` is plain, a borrow otherwise | keeps `v` unchanged |
| `x: &T` | `f(&v)` | an exclusive, mutable borrow of `v` | keeps `v`, possibly changed |
| `x: own T` | `f(own v)`, or `f(temp)` | the value itself | `v` is moved |

```lowlight
bind total = fn(xs: [int]) int: {
	let t = 0
	for(x in xs): t += x
	t
}

bind add_zero = fn(xs: &[int]): xs.push(0)

bind into_set = fn(xs: own [int]) #[int]: for(x in xs) #[int]: x

bind main = fn(): {
	let xs = [3, 1, 2]
	println(total(xs))
	add_zero(&xs)
	let s = into_set(own xs)
	println(s.len())
}
```

**Rules.**

1. A `&T` argument must be a mutable place (a `let` variable, or a field or element reached from one, or from another `&` binding) and must be written with `&`. A `bind` value cannot be passed as `&`.
2. An `own T` argument that is an existing variable must be written with `own`.
3. A read-only parameter cannot be modified, moved out of, or passed on as `&` or `own`. To keep a copy, call `.copy()`.
4. Receivers follow the same modes: `self`, `&self`, `own self` ([§4.4](#44-methods-and-associated-functions)). For `&self`, the receiver is borrowed automatically without writing `&`.
5. A function type records its parameter modes: `fn(&[int])` and `fn([int])` are different types.

### 7.4 Borrowing rules

While a mutable borrow of a place is active, nothing else may use that place. A read-only borrow prevents mutation and moves. Concretely:

1. **No two mutable borrows** of the same place in one call: `swap(&a, &a)` is an error.
2. **No reading while mutably borrowed** in one call: `update(&state, state)` is an error when `state` is a resource. (A plain argument is copied before the call starts and is allowed.)
3. **No mutation or moves during iteration.** Inside `for(x in xs)`, `xs` cannot be pushed to, cleared, reassigned, or moved. The same applies to an optional narrowed by `if(x?)` or `if(&x?)`, which is borrowed for the duration of the branch.
4. **Borrows do not escape.** A borrowed parameter cannot be returned, stored in a longer-lived value, or captured by a closure. Since every borrow lasts at most one call or one branch, no lifetime annotations are needed.

### 7.5 Destruction

When a scope ends, whether normally or by an unwind leaving it, every resource it still owns is destroyed, **in reverse order of declaration**. Destroying a value:

1. runs its `drop` method, if its type implements `Drop` (`bind T.drop = fn(&self): ...`);
2. then destroys its fields (struct), its active payload (enum), or its elements (collection), in reverse order.

```lowlight
define Noisy struct [
	name: string,
]

bind Noisy.drop = fn(&self): println("drop ${self.name}")

bind main = fn(): {
	let a = Noisy[name: "a"]
	let b = Noisy[name: "b"]
	{
		let c = Noisy[name: "c"]
	}
	println("end of main")
}
```

This prints `drop c`, `end of main`, `drop b`, `drop a`.

**Other rules.**

- A value that has been moved is not destroyed by its old owner.
- When a `match` arm moves one payload out with `own`, the rest of the subject is destroyed at that point.
- Temporaries are destroyed at the end of the statement that created them.
- Resource constants are destroyed at program exit, in reverse order of initialization.
- A panic ends the process **without** running destruction ([§9.3](#93-panics)).

### 7.6 Arenas and references

An `Arena` owns many values that are destroyed together. `arena.alloc(v)` moves `v` into the arena and returns a `ref[T]`, a plain, non-null reference usable like the value itself (`r.field`, `r.method()`).

```lowlight
define Node struct [
	value: int,
]

bind main = fn(): {
	let arena = Arena.new()
	let n = arena.alloc(Node[value: 7])
	println(n.value)
}
```

The compiler guarantees that no `ref[T]` outlives its arena:

1. Allocating from an arena **pins** it for the rest of the scope where the allocation happened. A pinned arena cannot be reset, reassigned, or moved, including into a closure.
2. A `ref[T]` cannot escape the scope of its arena: it cannot be returned, stored into a value from an outer scope, or assigned to an outer variable.
3. The exception is a function that receives the arena as `&Arena`: references allocated from it may be returned, because the arena belongs to the caller.
4. When the scope that pinned the arena ends, the arena is unpinned. Dropping or resetting it then destroys everything it holds, in reverse order of allocation.

### 7.7 Closures and captures

A function literal that uses variables from its enclosing scope captures them when it is created:

- plain variables are **copied** into the closure;
- resource variables are **moved** into the closure. The outer variable is then moved; use `.copy()` first to keep it;
- variables bound by mutable borrow (`&` parameters, `&` loop variables, `if(&x?)`) cannot be captured;
- inside the closure, captured variables are read-only.

The closure value owns its captures and destroys them when it is destroyed. A closure is a resource; `.copy()` duplicates it and its captures. Only `cl(...)` literals capture; an `fn(...)` literal that uses an outer variable is an error ([§3.12](#312-functions-and-closures)).

---

## 8. Generics

> **At a glance**
> - Type parameters go in brackets after the name: `define Stack[T] struct [...]`, `bind first[T] = fn(xs: [T]) ?T: ...`.
> - Bounds restrict them: `[T: Comparable + Copyable]`.
> - Type arguments are usually inferred; write them when they aren't: `Stack[int][items: []]`, `type.size_of[u8]()`.
> - Generic code is compiled separately for each set of type arguments. A type parameter is never an optional.

### 8.1 Generic declarations

Structs, enums, results, interfaces, functions, and methods may take type parameters and **const generic value parameters** (`[N: const int]`, where allowed const parameter types are integer primitives `int`/`i8`–`i64`/`u8`–`u64`, `bool`, `char`, tag `enum`s, and `string`):

```lowlight
define Stack[T] struct [
	items: [T],
]

bind Stack[T].push = fn(&self, x: own T): self.items.push(own x)

bind Stack[T].peek = fn(self) ?T: self.items.get(self.items.len() - 1)

bind largest[T: Comparable + Copyable] = fn(xs: [T]) ?T: {
	if(xs.len() == 0): func <- none
	let best = xs[0].copy()
	for(x in xs): if(x > best): best = x.copy()
	best
}

bind main = fn(): {
	let s = Stack[int][items: []]
	s.push(3)
	s.push(5)
	println(s.peek() ?? 0)
	println(largest(["b", "z", "a"]) ?? "")
}
```

- A method of a generic type repeats the type's parameters on its name: `bind Stack[T].push = ...`. A method may add parameters of its own: `bind Stack[T].map[U] = ...`.
- Generic type declarations must be at top level.

### 8.2 Bounds

`[T: A + B]` requires every type argument for `T` to implement interfaces `A` and `B`. Inside the generic code, values of type `T` may use exactly the operations the bounds provide, plus those available for every type (moving, borrowing, and passing to other generic code with compatible bounds):

| To use | The bound must include |
|---|---|
| `==`, `!=` | `Equatable` (or `Comparable`) |
| `<`, `<=`, `>`, `>=` | `Comparable` |
| `+` | `Addable` |
| `+=` | `AddAssignable` (or `Addable`) |
| `.copy()` | `Copyable` |
| interpolation, `.to_string()` | `Printable` |
| use as a set element or map key | `Hashable` |
| any other method | an interface that declares it |

Calls through bounds are resolved statically, at compile time.

### 8.3 Type parameters are never optional

A type argument may not be an optional type: `Stack[?int]` is an error. A generic type that needs optional values writes `?T` itself (`item?: ?T`). This keeps optionals from nesting: `?T` with `T` substituted is always exactly one level of optional.

### 8.4 Inference and instantiation

1. Type arguments may be omitted when they can be determined from the arguments, the expected type, or a literal's contents. Const generic `N: const int` parameters may also be inferred from `[N: T]` fixed-size array parameters (`const dot[N: const int] = fn(a: [N: int], b: [N: int]) int: ...`). Otherwise arguments must be supplied explicitly (`Matrix[2 + 2, 4][...]`, `zeros[8]()`).
2. Each distinct set of type and canonical const arguments produces a separate instantiation (monomorphization). Equivalent constant expressions yield the exact same type identity (`Matrix[2 + 2, 4]` and `Matrix[4, 4]` are identical types); `[N: const int]` parameters may appear in array size expressions (`[R * C: float]`) and repeat literals (`[N: 0]`).
3. Instantiation may nest at most 128 levels deep (with a per-template instantiation cap of 512); a type or const generic that expands without bound (such as `Box[T]` containing `Box[Box[T]]` or `boom[N + 1]()`) is reported as an error.

---

## 9. Failure Handling

> **At a glance**
> - Expected failures (missing files, bad input, refused connections) are **results**: return `Result[T]` or your own result type, and handle them with chains, `??`, or `if(r! as v)`.
> - Bugs and broken invariants **panic**: the program stops. Panics can't be caught.
> - Absence that isn't an error is an optional, `?T`.
> - Fallible operations take the plain name (`File.open`). A panicking convenience twin is `must_*`.

### 9.1 Choosing a mechanism

| Situation | Use |
|---|---|
| A value may legitimately be absent (`map[k]`, `parse_int`, an iterator's end) | `?T` |
| A correct program can still fail because of its input or environment | `Result[T]` (with an `Error`), or `Status` when there is no value |
| Callers need to react differently to distinct failure cases | a custom result type, with each distinct failure carrying an `Error` ([§3.9](#39-results) rule 8) |
| A precondition was violated or an invariant is broken: a bug | `panic`, `assert` |

Library code must not panic on bad external input.

**Naming.** A fallible operation has the plain name and returns a result or optional: `DateTime.new(...) Result[DateTime]`, `File.open(path) FileResult[File]`, `DateTime.diff(other) ?Duration`, `xs.remove(i) ?T`. There is no `try_` prefix. If a panicking twin is useful, for an argument the caller knows is valid, it is named `must_*`: `DateTime.must_new(...)`, `xs.must_remove(i)`. An alternative that avoids failure some other way gets a descriptive name, such as `diff_saturating`. See `CONVENTIONS.md`.

### 9.2 Results

The language-level rules for result types are in [§3.9](#39-results) (declaration and construction), [§6.6](#66-result-chains) (chains), [§5.8](#58-optional-chaining-and-fallback) (`??`), and [§6.2](#62-if) (`if(r! as v)`). The standard `Result[T]` and `Error` types, including error chains, codes, and traces, are in [§12.7](#127-result-and-error).

A typical function that propagates failures:

```lowlight
bind parse_port = fn(raw: string) Result[int]: {
	let port = parse_int(raw) ?? func <- err(Error.coded("invalid_port", "not a number: $raw"))
	if(port < 1 or port > 65535): func <- err(Error.coded("invalid_port", "out of range: $port"))
	value(port)
}

bind main = fn(): {
	println(parse_port("8080") ?? 0)
	let p = parse_port("http"):
		default err(own e): {
			println(e.summary())
			80
		}
	println(p)
}
```

Values of `@must_use` types, which include `Result[T]` and `Status`, cannot be silently discarded ([§4.8](#48-attributes)).

### 9.3 Panics

A **panic** stops the program. Panics come from:

- `panic(msg)` with a `string`, or `panic(err)` with an `Error`;
- `assert(cond)` and `assert(cond, msg)` when `cond` is false;
- a result chain's `default panic`, and the `unwrap` and `expect` methods of `Result`;
- runtime **traps** ([Appendix D](#appendix-d-runtime-traps)): index or slice out of range, division by zero, an out-of-range shift, and similar.

When a panic occurs:

1. standard output is flushed;
2. `lowlight panic at FILE:LINE:COL: MESSAGE` is written to standard error. For an `Error`, its full chain and trace follow;
3. the process exits with status `1`.

No destruction runs: no `drop` methods, no scope cleanup. The operating system reclaims memory and handles. A panic cannot be caught, recovered from, or inspected by Lowlight code.

Panics are abort-only for three reasons:

- Catching them would add an invisible exit to every call, contrary to Lowlight's explicit `<-` control flow.
- Every `drop` would have to tolerate half-finished state.
- Every backend would need unwinding machinery.

Test runners isolate panics per test ([§11.3](#113-the-test-runner)), but that is a property of the tool, not the language.

The location reported is where the panic was raised. Inside a function marked `@track_caller`, the caller's location is used instead ([§4.8](#48-attributes)).

---

## 10. Unsafe Code and Foreign Functions

> **At a glance**
> - `unsafe expr` or `unsafe { ... }` allows raw-pointer operations, raw allocation, and calls to `extern` functions.
> - Inside `unsafe`, ownership, borrowing, and destruction rules still apply; only the listed operations are added.
> - `extern "header.h"` and `extern name: fn(params) Ret` declare foreign functions.

### 10.1 Unsafe expressions

`unsafe` is a prefix operator that applies to one expression, often a block:

```lowlight
bind main = fn(): {
	let x = 5
	let y = unsafe {
		let p = addr_of(x)
		ptr_read(p) + 1
	}
	println(y)
}
```

The following operations are **unsafe**: each is an error unless it appears inside an `unsafe` expression.

- dereferencing a pointer, `*p`, `*p = v`, and indexing one, `p[i]`;
- the pointer intrinsics: `null_ptr`, `is_null`, `addr_of`, `ptr_add`, `ptr_read`, `ptr_write`, `ptr_cast`, `ptr_addr`, `ref_from_ptr`;
- `syscall` ([§10.6](#106-system-calls));
- the raw memory intrinsics: `alloc_raw`, `alloc_uninit`, `realloc_raw`, `free_raw`, `copy_bytes`, `zero_bytes`, `fill_bytes`;
- calling an `extern` function, or an `extern fn` value;
- `fn_from_ptr`;
- `s.c_str()`, which exposes a string's bytes as a `ptr[u8]`.

In unsafe code the programmer, not the compiler, guarantees that pointers are valid, aligned, non-null where required, and point to live memory of the right type. All other rules (types, moves, borrows, destruction) still apply.

### 10.2 Raw pointers

`ptr[T]` is a plain address. The intrinsics, with their types:

| Intrinsic | Type | Meaning |
|---|---|---|
| `null_ptr[T]()` | `ptr[T]` | the null pointer |
| `is_null(p)` | `bool` | tests for null |
| `addr_of(place)` | `ptr[T]` | the address of a variable, field, or element |
| `ptr_add(p, n)` | `ptr[T]` | `p` offset by `n` elements |
| `ptr_read(p)` · `*p` | `T` | reads the value at `p` (a bitwise copy) |
| `ptr_write(p, v)` · `*p = v` | | writes `v` (moved) to `p` without destroying the old contents |
| `ptr_cast[U](p)` | `ptr[U]` | reinterprets a pointer, or an integer address, as `ptr[U]` |
| `ptr_addr(p)` | `u64` | the numeric address |
| `fn_from_ptr[extern fn(...) R](p)` | `extern fn(...) R` | the C function at address `p` (a `ptr[T]` or an integer), callable; `ptr_cast` and `ptr_addr` also accept an `extern fn`, for the reverse |
| `ref_from_ptr(p)` | `ref[T]` | a `ref[T]` to the value at `p`, for an allocator such as `Arena` that owns the memory; nothing checks that the memory outlives the reference |

### 10.3 Memory intrinsics and `.rodata` invariant

| Intrinsic | Unsafe | Meaning |
|---|---|---|
| `type.size_of[T]()` · `type.align_of[T]()` | no | size and alignment of `T` in bytes, as `const` compile-time values (`int`) |
| `type.is_plain[T]()` | no | whether `T` is plain (`bool`) |
| `alloc_raw[T](n)` · `alloc_uninit[T](n)` | yes | allocate room for `n` values of `T`, zeroed or uninitialized |
| `realloc_raw(p, n)` | yes | resize an allocation to `n` elements |
| `free_raw(p)` | yes | release an allocation (does not destroy its contents) |
| `copy_bytes(dst, src, n)` · `zero_bytes(dst, n)` · `fill_bytes(dst, b, n)` | yes | byte operations on `ptr[u8]` |

Memory from `alloc_raw` is not tracked by ownership: it must be released with `free_raw`, and values written into it are not destroyed automatically. Compile-time constant strings and `[T]` literals backed into static `.rodata` have `cap == 0`; mutating them through `unsafe` raw pointers (`ptr_write`) without going through safe copy-on-write methods causes undefined behavior / a hardware memory fault.

### 10.4 Foreign functions

```ebnf
ExternDecl = "extern" StringLit                                      (* a header to include *)
           | "extern" ExternName [ "as" Name ] ":" "fn" "(" [ Params ] ")" [ Type ] ;
ExternName = Name | StringLit | Keyword ;
```

```lowlight
extern "stdio.h"
extern puts: fn(s: ptr[u8]) i32

bind main = fn(): {
	let msg = "hello from C"
	let _ = unsafe puts(msg.c_str())
}
```

- `extern "header.h"` names a header (or another target-specific binding unit) that provides foreign declarations.
- `extern name: fn(params) Ret` declares a function with the platform's C calling convention. `as alias` gives it a different local name, which is required when the foreign name is a Lowlight keyword (`extern "match" as c_match: ...`).
- Parameter and return types should be plain types with a C representation: numbers, `bool`, `char`, `ptr[T]`, `extern fn` types, and plain structs.
- Every call is unsafe.
- The declaration is the only signature that matters: it may differ from a C header's prototype of the same function (`extern write: fn(fd: i32, buf: ptr[u8], n: int) int` against `<unistd.h>`'s `const void*` and `size_t`), as long as the two agree at the machine level.
- A `@test_only` extern is declared only in test builds.
- Foreign functions are outside Lowlight's guarantees. They must not unwind through Lowlight frames, and pointers they keep must not outlive the memory they point to.

### 10.5 C function pointers

`extern fn(ParamTypes) ReturnType` is the type of a C function pointer: a plain, 8-byte value called with the platform's C calling convention and nothing but its arguments. Its parameter and return types must have a C representation (§10.4).

- **Callbacks into Lowlight.** `extern fn(params) Ret: body` is a function C can call, such as a `qsort` comparator or a signal handler. Like an `fn`, it can't capture variables; pass what it needs through its arguments (C APIs usually take a `ptr[u8]` user-data argument for this). Written at the top level (`bind cmp = extern fn(...)`), it is an ordinary function that Lowlight code can also call directly.
- **Pointers from C.** `fn_from_ptr[extern fn(...) R](p)` makes the C function at address `p` (from `dlsym`, or a C struct field) callable. `ptr_cast[u8](f)` and `ptr_addr(f)` go the other way.
- Calling an `extern fn` value is unsafe: it may be any C function. Calling a top-level `extern fn` by its name is not.
- An `extern fn` never converts to or from `fn` or `cl`.
- A panic inside a callback C is running ends the program; it never unwinds through the C frames. That holds under `lowlight test` too: the panic ends the test run instead of failing just the current test. (Calling the same function directly from Lowlight is an ordinary call, so a panic there fails only its test.)

```lowlight
extern qsort: fn(base: ptr[u8], n: int, size: int, cmp: extern fn(ptr[u8], ptr[u8]) i32)

bind ascending = extern fn(a: ptr[u8], b: ptr[u8]) i32: {
	let x = unsafe ptr_read(ptr_cast[int](a))
	let y = unsafe ptr_read(ptr_cast[int](b))
	i32(x.cmp(y))
}

bind main = fn(): {
	let data = unsafe alloc_raw[int](3)
	for(i, v in [3, 1, 2]): unsafe ptr_write(ptr_add(data, i), v)
	unsafe qsort(ptr_cast[u8](data), 3, type.size_of[int](), ascending)
	println(for(i in 0..3) [int]: unsafe ptr_read(ptr_add(data, i)))
	unsafe free_raw(data)
}
```

### 10.6 System calls

`syscall(number, a1, ..., a6) int` makes system call `number` with up to six `int` arguments, using the target's system call instruction (`svc` on arm64, `syscall` on x86-64), and returns the result. A failure returns `-errno` on every platform (macOS's convention of a carry flag and a positive errno is converted). It is unsafe.

Call numbers differ by operating system and architecture. Linux keeps its numbers stable; macOS does not promise to, so portable code on macOS calls the C library through `extern` instead.

```lowlight
import "std/os"

bind main = fn(): {
	let msg = "hello\n"
	// write(1, msg, len): call number 4 on macOS, 1 on x86-64 Linux, 64 on arm64 Linux.
	let n = unsafe syscall(4, 1, int(ptr_addr(msg.c_str())), msg.len())
	if(n < 0): println("write failed: ${os.errno_name(-n)}")
}
```

---

## 11. Testing

> **At a glance**
> - Mark a no-argument function with `@test` (or `@test("description")`); `lowlight test` runs it.
> - Put larger suites in `name_test.ll` files next to the code; they see the package's private names.
> - Each test is checked for heap leaks, and a panic fails only that test.
> - `std/test` provides assertions with readable diffs.

### 11.1 Declaring tests

```lowlight
import "std/test"

bind add = fn(a: int, b: int) int: a + b

@test("add sums its arguments")
bind test_add = fn(): {
	test.assert_eq(add(2, 3), 5)
	assert(add(-1, 1) == 0)
}

bind main = fn(): println(add(1, 1))
```

1. `@test` marks a top-level function with no parameters that returns nothing. Its name is free; the optional string is a description.
2. Tests may live in any file of a package, or in files named `*_test.ll`, which are compiled only for testing and belong to the package (so they see `_` names).
3. `@test_only` marks helpers, types, and constants intended for tests. Non-test code may not refer to `@test` or `@test_only` declarations, or to `[dev-dependencies]` ([§14.2](#142-the-manifest)). Normal builds omit all test code.
4. Black-box tests that use a package only through its exported API go in separate packages under `tests/`.

### 11.2 Caller locations

`@track_caller` on a function makes panics, `Error.new`, `Error.wrap`, and `caller_*()` inside it report the location of the call to that function. Calls through nested `@track_caller` functions report the outermost caller. This is how assertion helpers report the line of the failing test.

| Function | Returns |
|---|---|
| `caller_location()` | `"file:line:col"` |
| `caller_file()` · `caller_line()` · `caller_col()` | its parts |

### 11.3 The test runner

`lowlight test [paths]` compiles one test program per package and runs every test in it.

- If a package defines `bind test_main = fn(): ...`, that is the entry point instead.
- Each test runs in turn. A test **fails** if it panics (including a failed assertion) or if it leaves heap allocations unreleased: `--- FAIL: name (leaked N heap allocation(s))`.
- A panic in one test is contained to that test, and the run continues. Because no destruction runs after a panic, a panicking test may also report leaks.
- The runner reports each test's result and duration, and exits with a non-zero status if any test failed.

The `std/test` assertions are listed in [§13](#13-the-standard-library).

---

## 12. The Prelude

> **At a glance**
> - `std/core` is imported into every file automatically, unqualified.
> - It defines the interfaces that give meaning to operators, loops, and interpolation: `Printable`, `Equatable`, `Comparable`, `Addable`, `AddAssignable`, `Hashable`, `Copyable`, `Drop`, `Iterable`, `Iterator`, `Collectable`, `Indexable`, `KeyIndexable`, `Containable`.
> - It provides the collection types behind `[T]`, `#[T]`, `#[K: V]`, `[N: T]`, and `lo..hi`, plus `Result[T]`, `Status`, `Error`, and the methods on primitive types.

The prelude is ordinary Lowlight source in `std/core`. Where the language relies on a prelude declaration, the declaration is marked `@lang("hook")`. The compiler finds language features through these hooks rather than through names.

### 12.1 Core interfaces

| Interface | Methods | Hook | Gives meaning to |
|---|---|---|---|
| `Printable` | `to_string: fn(self) string` | `printable` | interpolation, `print`/`println`, `to_string()` |
| `Copyable` | `copy: fn(self) Self` | `copyable` | `.copy()`, repeat array literals |
| `Drop` | `drop: fn(&self)` | `drop` | custom cleanup before a value's fields are destroyed |
| `Equatable` | `eq: fn(self, other: Self) bool` | `equatable` | `==`, `!=` |
| `Comparable` | `cmp: fn(self, other: Self) int` (negative, zero, positive) | `comparable` | `<`, `<=`, `>`, `>=`, and also `==` |
| `Addable` | `add: fn(self, other: Self) Self` | `addable` | `+` (and `+=` without `add_assign`) |
| `AddAssignable` | `add_assign: fn(&self, other: Self)` | `add_assignable` | `+=` |
| `Hashable` | `hash: fn(self) u64` (equal values hash equally) | `hashable` | set elements and map keys |
| `Iterator[T]` | `next: fn(&self) ?T` | `iterator` | stepping through `for` and `while` loops |
| `Iterable[T]` | `iter: fn(self) Iterator[T]` | `iterable` | `for(x in v)` over any type |
| `Collectable[T]` | `push: fn(&self, item: own T)` | `collectable` | the collection a `for` loop builds |
| `Indexable[T]` | `len: fn(self) int`, `get: fn(self, idx: int) ?T` | `indexable` | `v[i]` on user types |
| `KeyIndexable[K, V]` | `len`, `has: fn(self, key: K) bool`, `get: fn(self, key: K) ?V` | `key_indexable` | `v[k]` on user types |
| `Containable[T]` | `contains: fn(self, item: T) bool` | `containable` | `when in v:` patterns |

The prelude also binds `list`, `array`, `set`, `map`, and `range` hooks to the types `List`, `Array`, `Set`, `Map`, and `Range`, which implement `[T]`, `[N: T]`, `#[T]`, `#[K: V]`, and `lo..hi`. Implementing an interface's methods on your own type is all it takes for that type to work with the corresponding syntax.

`MapEntry[K, V]` is a struct with fields `key` and `value`, used when iterating and collecting maps.

### 12.2 Deriving methods

`@derive(I, ...)` on a struct, enum, or result instantiates the generic implementations declared by each interface's `@derivable(...)` attribute ([§4.8](#48-attributes)), producing ordinary Lowlight methods checked like hand-written ones. In `std/core`, the five core interfaces below point via `@derivable` to generic implementations in `std/derive`; user packages can likewise annotate custom interfaces with `@derivable(method: generic_impl, ...)`.

| Interface | Generated method |
|---|---|
| `Equatable` | `eq`: every field (or the variant and its payload) compares equal |
| `Comparable` | `cmp`: fields in declaration order; for enums, variants in declaration order, then the payload |
| `Hashable` | `hash`: combines the hashes of every field (or the variant and its payload) |
| `Copyable` | `copy`: copies every field (or the payload) |
| `Printable` | `to_string`: `Point[x: 1, y: 2]` for structs, `Shape.circle(1.5)` and `Level.high` for enums |

```lowlight
@derive(Equatable, Hashable, Printable)
define Point struct [
	x: int,
	y: int,
]
```

Rules:

1. Every field or payload must support the interface; otherwise `@derive` is an error naming the field.
2. On a generic type, a derived method exists only for the instances whose fields support it: with `@derive(Equatable)` on `Box[T]`, `Box[int]` has `eq` and `Box[File]` does not.
3. A derived method is bound as `const` whenever every field or variant operation it invokes is `const`, and falls back to `bind` otherwise.
4. Generic derivations instantiated through `@derive(...)` are authorized to inspect private (`_`) fields and variants of the deriving type via `v.field(f)`, `v.payload(vr)`, and `make_variant`.
5. A type may not both derive an interface and define its method by hand.
6. Tuples are not declared, so they cannot use `@derive`; generic methods over `comp.elems_of[T]()` in `std/core` provide `==`, ordering, hashing, copying, and printing whenever their elements do ([§3.6](#36-collections)).

### 12.3 Universe functions

These are built into the language and need no import.

| Function | Type | Meaning |
|---|---|---|
| `print(v)` · `println(v)` | `fn(Printable)` | write `v.to_string()` to standard output; `println` adds a newline, and `println()` writes only a newline |
| `eprint(v)` · `eprintln(v)` | `fn(Printable)` | the same, to standard error |
| `panic(msg)` | `fn(string \| Error) never` | stop the program ([§9.3](#93-panics)) |
| `assert(cond)` · `assert(cond, msg)` | `fn(bool [, string])` | panic with `msg` (or "assertion failed") if `cond` is false |
| `exit(code)` | `fn(int) never` | end the process immediately with status `code`; no destruction runs |
| `args()` | `fn() [string]` | the command-line arguments, starting with the program name |
| `abs(x)` | `fn(int) int` | absolute value (for floats, `math.abs_float` in `std/math`) |
| `min(a, b)` · `max(a, b)` | numeric `fn(T, T) T` | the smaller or larger of two numbers of the same type |
| `caller_location()`, `caller_file()`, `caller_line()`, `caller_col()` | | the caller's source location ([§11.2](#112-caller-locations)) |
| `type.size_of[T]()`, `type.align_of[T]()`, `type.is_plain[T]()` | | `const` type layout queries ([§10.3](#103-memory-intrinsics-and-rodata-invariant)) |
| `comp.TARGET` | `comp.Target` | `@comptime_only` active compilation target descriptor ([§4.2](#42-bind-const-and-let)) |
| `comp.compile_error(msg)`, `comp.log(val)` | | `@comptime_only` compile-time diagnostic abort and `note:` diagnostic emitter |
| `comp.embed_str(path)`, `comp.embed_bytes(path)` | | `@comptime_only` declaring-file-relative, sandbox-confined static file embedding (`string`, `[u8]`) |
| `comp.type_name[T]()`, `comp.kind_of[T]()`, `comp.fields_of[T]()`, `comp.variants_of[T]()`, `comp.elems_of[T]()`, `comp.attrs_of[T]()`, `comp.implements[T, I]()` | | `@comptime_only` compile-time type reflection queries ([§4.2](#42-bind-const-and-let)) |
| `variant_index(v)`, `make_variant[T](vr [, own p])` | `int`, `T` | generic enum/`result` active-variant index and compile-time variant constructor |

The prelude adds these ordinary functions:

| Function | Type |
|---|---|
| `parse_int(s)` · `parse_int_radix(s, radix)` | `?int` |
| `parse_float(s)` | `?float` |

The parsers accept exactly a number: an optional sign, digits (and a `0x`, `0b`, or `0o` prefix for integers; a fraction, exponent, `inf`, or `nan` for floats). They return `none` for anything else, including surrounding whitespace (call `s.trim()` first to accept it) and values out of range: an integer outside `int`, or a finite decimal too large for a `float` (`"1e400"`). Decimals too small to represent round to a signed zero. `parse_float` is correctly rounded.
| `join_strings(parts, sep)` | `string` |

### 12.4 Methods on primitive types

| On | Methods |
|---|---|
| every primitive | `to_string()`; numbers, `char`, `bool` also `eq`, `hash`; numbers and `char` also `cmp` |
| `float` | `to_exp(precision)` |
| `char` | `is_digit()`, `is_alpha()`, `is_alnum()`, `is_whitespace()`, `to_int()` (more in `std/chars`) |

### 12.5 Strings and characters

| Method | Type | Meaning |
|---|---|---|
| `len()` | `int` | length in bytes |
| `is_empty()` | `bool` | `len() == 0` |
| `byte_at(i)` · `s[i]` | `u8` | byte at offset `i`; traps when out of range |
| `get(i)` · `get_byte(i)` | `?u8` | byte at offset `i`, or `none` |
| `bytes()` · `chars()` | `[u8]` · `[char]` | the bytes, or the decoded characters |
| `starts_with(p)` · `ends_with(p)` · `contains(sub)` | `bool` | substring tests |
| `index_of(sub)` | `?int` | byte offset of the first occurrence |
| `trim()` · `trim_start()` · `trim_end()` | `string` | without surrounding whitespace |
| `to_upper()` · `to_lower()` | `string` | ASCII case conversion |
| `replace(old, new)` · `replace_all(old, new)` | `string` | replace the first or every occurrence |
| `split(sep)` | `[string]` | pieces between occurrences of `sep` |
| `push(t)` · `push_char(c)` · `push_byte(b)` · `clear()` | | modify a mutable string in place; appends grow the buffer by doubling, so building a string with `push` is linear in its length |
| `string.with_capacity(n)` · `reserve(n)` · `cap()` | `string` · · `int` | preallocate room for `n` (more) bytes; the bytes it can hold without reallocating (`0` for literals) |
| `eq`, `cmp`, `hash`, `copy`, `to_string` | | byte-wise comparison and hashing |
| `c_str()` | `ptr[u8]` | NUL-terminated bytes for C (unsafe) |

`std/strings`, `std/strconv`, and `std/strfmt` add many more ([§13](#13-the-standard-library)).

### 12.6 Collections

**List** `[T]`:

| Method | Meaning |
|---|---|
| `List[T].new()` · `List[T].with_capacity(n)` · `List[T].filled(n, v)` | construct |
| `len()` · `cap()` · `is_empty()` | size |
| `push(x)` · `pop() ?T` · `insert(i, x) Status` · `remove(i) ?T` · `swap_remove(i) ?T` | add and remove; an index out of range is a failure or `none` |
| `must_insert(i, x)` · `must_remove(i) T` · `must_swap_remove(i) T` | the same for an index known to be valid; trap when out of range |
| `get(i) ?T` · `set(i, x) Status` | checked access (`xs[i]` and `xs[i] = x` trap instead) |
| `first() ?T` · `last() ?T` · `index_of(x) ?int` | ends of the list; position of the first element equal to `x` |
| `swap(i, j)` · `truncate(n)` · `extend(own other)` | exchange two elements (traps when out of range); drop elements past `n`; move all of `other` onto the end |
| `reserve(n)` · `resize(n, v)` · `clear()` | capacity and length |
| `contains(x)` | membership (`T` must support `==`) |
| `copy()` · `to_string()` · `iter()` | |

**Array** `[N: T]`: `len()`, `is_empty()`, `get(i) ?T`, `contains(x)`, `to_string()`, `iter()`.

**Set** `#[T]`: `Set[T].new()`, `with_capacity(n)`, `len()`, `is_empty()`, `insert(x) bool` (false if already present), `has(x)` / `contains(x)`, `remove(x) bool`, `clear()`, `to_list()`, `copy()`, `to_string()`, `iter()`. Set algebra returns new sets of copied elements: `union(other)`, `intersection(other)`, `difference(other)`; `is_subset(other) bool`.

**Map** `#[K: V]`: `Map[K, V].new()`, `with_capacity(n)`, `len()`, `is_empty()`, `get(k) ?V`, `has(k)` / `contains(k)`, `insert(k, v)`, `remove(k) ?V`, `clear()`, `keys() [K]`, `values() [V]`, `entries() [MapEntry[K, V]]` (all in insertion order), `copy()`, `to_string()`, `iter()`. `get_or_insert(k, v) V` inserts `v` when `k` is absent and returns a copy of the value; `update(k, f) bool` runs `f: cl(&V)` on the value in place, and `with(k, f) ?R` lends the value read-only to `f: cl(V) R` and returns its result; neither copies the value, so they work for values that can't be copied.

**Range** `Range[T]`: `contains(x)`, `is_empty()`, `len()`, `to_string()`, `iter()`.

Sorting and searching are in `std/sort`, which adds methods such as `sort`, `sort_by`, `binary_search`, and `reverse` to lists and arrays.

### 12.7 Result and Error

```lowlight
@must_use
define Result[T] result [
	value: T,
	err: Error,
]
```

**Constructing.** Where a `Result[T]` is expected, write `value(x)` or `err(e)`. Elsewhere, qualify: `Result[int].value(1)`, `Result[int].err(e)`. `Result[T].fail_msg(msg)` is shorthand for `err(Error.new(msg))`, recording the caller's location.

**Methods.** Result chains ([§6.6](#66-result-chains)), `??`, and `if(r! as v)` are the way to handle a result. The methods cover only what those can't do: look at a result without consuming it, pass it on with added context, and assert success in the middle of a longer expression.

| Method | Meaning |
|---|---|
| `is_ok()` · `is_err()` | which member it holds, without consuming it |
| `peek_err() ?Error` | a copy of the failure's error, or `none`, without consuming it |
| `wrap_err(msg)` · `trace_err()` | add context or a trace frame to a failure, passing a value through unchanged (`@track_caller`) |
| `unwrap()` | the value, or panic with the error wrapped as `"called Result.unwrap on an error"`; the expression form of `r!: default panic` (`@track_caller`) |
| `expect(msg)` | the value, or panic with the error wrapped as `msg` (`@track_caller`) |
| `unwrap_err()` | the error, or panic if it holds a value (`@track_caller`) |

There are no combinators. Write `r! ?? fallback` for a fallback, `r!: default err(own e): f(e)` to compute one from the error, `if(r! as v)` to read the value as an optional would, and a chain with `func <- err(...)` and `value(...)` to transform or continue.

**Status.** The unit counterpart of `Result[T]`, for operations that produce nothing when they succeed (`fs.write_file`, `File.close`, `os.remove_all`):

```lowlight
@must_use
define Status result [
	value,
	err: Error,
]
```

Write `value` or `err(e)` where a `Status` is expected; `Status.fail_msg(msg)` is shorthand for `err(Error.new(msg))`. It has the same methods as `Result[T]`; `unwrap` and `expect` return nothing.

**Error.** An `Error` holds a message, an optional code, an optional cause (another `Error`), and a trace of source locations (`ErrorFrame`s). Its fields are private.

| Method | Meaning |
|---|---|
| `Error.new(msg)` | a new error, recording the caller's location (`@track_caller`) |
| `Error.coded(code, msg)` | the same, with a machine-readable code such as `"not_found"` |
| `e.wrap(msg)` | consumes `e` and returns a new error with message `msg` whose cause is `e` (`@track_caller`) |
| `e.trace()` | consumes `e` and returns it with the caller's location appended (`@track_caller`) |
| `e.with_code(code)` | consumes `e` and returns it with `code` set |
| `e.message()` · `e.code() ?string` · `e.cause() ?Error` · `e.root()` · `e.frames()` | accessors |
| `e.is(target)` | whether any error in the chain matches `target` (same code, or same message if uncoded) |
| `e.has_code(code)` | whether any error in the chain has `code` |
| `e.summary()` | one line: `outer: middle: root` |
| `e.to_string()` | the summary, then the trace, one frame per line |

```lowlight
bind main = fn(): {
	let e = Error.coded("disk_full", "no space left").wrap("saving report")
	println(e.summary())
	println(e.has_code("disk_full"))
}
```

---

## 13. The Standard Library

The standard library is a set of ordinary packages under `std/`. Import each by path; its exported names are used through the last path segment (`import "std/fs"` gives `fs.read_file`). This chapter summarizes each package. Run `lowlight doc` for the complete API with documentation.

| Package | Contents |
|---|---|
| `std/core` | the prelude ([§12](#12-the-prelude)), imported automatically |
| `std/chars` | character classification (`is_ascii`, `is_ascii_upper`, `is_control`, `is_punctuation`, ...), case conversion (`to_ascii_upper`), digits (`to_digit(radix)`, `from_digit`), `len_utf8` |
| `std/strings` | `last_index_of`, `count`, `capitalize`, `repeat`, `reverse`, padding (`pad_left_char`, `center_char`), `split_whitespace`, `lines`, `join`, `chars_list`, `bytes_list`, `is_whitespace`, `is_ascii`; `ByteBuffer` for reading and writing little- and big-endian integers |
| `std/strconv` | parsing (`string.to_int`, `to_int_radix`, `to_float`, `to_bool`, `is_numeric`, ...) and formatting (`int.to_radix`, `to_hex`, `to_bin`, `to_oct`, `float_to_exp`) |
| `std/strfmt` | number formatting (`format_int_commas`, `format_float`, `format_bytes`, `format_percent`, `format_currency`), padding (`pad_left`, `center`, `pad_int_zeros`), case styles (`to_title`, `to_snake_case`, `to_kebab_case`, `to_camel_case`), `quote`, `unquote`, `indent`, `truncate` |
| `std/math` | `sqrt`, `cbrt`, `pow`, `exp`, `log`, `log2`, `log10`, trigonometric and hyperbolic functions, `floor`, `ceil`, `round`, `trunc`, `fmod`, `hypot`, `is_nan`, `is_inf`, `clamp_int`, `clamp_float`, `lerp`, angle conversions, `PI` and related constants |
| `std/sort` | `sort`, `sort_by`, `stable_sort`, `is_sorted`, `binary_search`, `lower_bound`, `upper_bound`, `reverse` for lists and arrays (as methods and functions); `Set.to_sorted_list`, `Map.to_sorted_keys` |
| `std/fs` | the `File` resource (`File.open`, `open_read`, `open_write`, `open_append`, `read_to_string`, `read_bytes`, `write_string`, `write_bytes`, `seek`, `tell`, `size`, `flush`, `close`; closes itself on drop) and functions `read_file`, `write_file`, `append_file`, `read_bytes`, `write_bytes`, `exists`, `file_size`, `remove_file`, `rename_file`, `copy_file`. Opening and reading by path return `FileResult[T]` (separating `not_found`); `File.read_bytes` returns `ReadResult[T]` (separating `eof`); writes, `flush`, and `close` return `Status`; `remove_file` and `rename_file` return `RemoveResult` and `RenameResult` (separating `not_found`). Every failure carries the OS's description, with the errno name as its code |
| `std/os` | `exec`, `system_run`, and `system_run_status` (a `Result` of an `ExecResult` with `stdout`, `exit_code`, and the terminating `signal`; a failure only when the shell can't start), `mkdir` (a `MkdirResult` separating `exists`), `mkdir_p` and `remove_all` (`Status`), `errno_name` and `error_from_errno` for OS errors, `file_exists`, `num_cpus`, `os_name`, `arch` |
| `std/time` | `Duration`, `Instant` (`now`, `elapsed`), `DateTime` (`new`, `must_new`, `now_utc`, `now_local`, `from_unix_secs`, formatting and parsing with layouts such as `RFC3339`), `sleep_ms`, calendar helpers |
| `std/net` | `TcpStream` (`connect`, `send`/`recv` for strings and `send_bytes`/`recv_bytes` for `[u8]`, `shutdown`, `peer_addr`, `local_addr`, `set_nodelay`, `close`), `TcpListener` (`listen`, `accept`, `port`), `dial_tcp`, `listen_tcp`, `socketpair`, `format_ipv6`. `connect` separates `not_found` (an unresolvable host), `refused`, and `timeout`; `recv` separates `eof` and `timeout`; `accept` separates `timeout`; `listen` separates `in_use` |
| `std/http` | URL parsing, `Request` and `Response`, a client (`get`, `post`, `put`, ...; a `RequestResult` separating `unreachable` and `timeout`), a server with routing (`ServeMux`, `listen_and_serve`; `Server.listen` separates `in_use`, `serve_one` separates `timeout`), and `STATUS_*` constants |
| `std/pool` | `Pool[T]` with generational `Handle[T]`s: `alloc`, `get`, `with_mut`, `set`, `take`, `free`, iteration |
| `std/collections` | `Deque[T]` (a ring buffer: `push_front`, `push_back`, `pop_front`, `pop_back`, `front`, `back`, `get`), `Queue[T]` (first in, first out: `push`, `pop`, `peek`), `BitSet` (non-negative `int`s, one bit each, with `union`, `intersection`, `difference`, `is_subset`), `PriorityQueue[T]` (a binary heap ordered by a closure; `min_first` and `max_first` for `Comparable` elements; `push`, `pop`, `peek`, `drain`), `StringInterner` (`intern`, `id_of`, `name_of`), and `BTreeMap[K: Comparable, V]` (an ordered map: `insert`, `get`, `remove`, `update`, `with`, `first`, `last`, `range`, `entries` of `Entry[K, V]`) |
| `std/test` | assertions for tests: `assert_eq`, `assert_ne`, `assert_true`, `assert_false`, `assert_gt`/`lt`/`ge`/`le`, `assert_approx_eq`, `assert_contains`, `assert_starts_with`, `assert_ends_with`, `assert_some`, `assert_none`, result assertions (`assert_is_ok`, `assert_is_err`, `assert_ok_eq`, `assert_err_contains`, ...), batch forms (`assert_all_*`, `assert_any_eq`), and a `_msg` variant of each |
| `std/unicode` | full Unicode case conversion: `to_upper`, `to_lower` (with the Final_Sigma rule), `fold`, `equal_fold`, `UNICODE_VERSION`; mappings may change length (`ß` → `SS`) and are language-independent. The prelude's `string.to_upper`/`to_lower` stay ASCII-only |
| `std/json` | JSON values and conversion (work in progress) |

All of these follow the failure conventions of [§9.1](#91-choosing-a-mechanism): operations that can fail because of the outside world return `Result`, and lookups that may find nothing return `?T`.

---

## 14. Packages, Manifests, and Dependencies

> **At a glance**
> - A single `.ll` file runs with no configuration: `lowlight run hello.ll`.
> - A project has a `lowlight.toml` at its root. Directories map one-to-one to import paths: `myapp/lexer/` is `import "myapp/lexer"`.
> - Dependencies are local paths, archive URLs with a SHA-256 hash, or versions from a static index. `lowlight.lock` pins them exactly.
> - Builds never touch the network; `lowlight fetch` and `lowlight update` do.

### 14.1 Project layout

`lowlight.toml` is optional. Without one, a file or directory compiles on its own, and imports resolve to `std/...` and to relative directories. With one, it marks a project root (or a workspace root), found by walking up from the file being compiled.

```text
myapp/
├── lowlight.toml        # manifest
├── lowlight.lock        # resolved dependencies (generated)
├── main.ll              # the root package (a program, because it has `main`)
├── myapp_test.ll        # tests for the root package
├── lexer/               # package "myapp/lexer"
│   ├── scanner.ll
│   └── scanner_test.ll
├── cmd/
│   └── tool/main.ll     # another program: lowlight build ./cmd/tool
├── tests/               # black-box integration test packages
└── vendor/              # optional vendored dependencies
```

Hidden directories (`.git/`, `.lowlight/`), `editors/`, and `node_modules/` are never treated as packages.

### 14.2 The manifest

`lowlight.toml` is TOML:

```toml
[package]
name = "myapp"
version = "0.2.0"
edition = "2026"
description = "A worker service"
license = "MIT"

[dependencies]
local_utils = { path = "../local_utils" }
jsonx = { url = "https://example.org/jsonx-1.2.0.tar.gz", hash = "sha256:9a3f..." }
http_ext = { index = "https://acme.dev/pkgs/http_ext.toml", version = "^0.3" }

[dev-dependencies]
benchkit = { url = "https://example.org/benchkit-0.1.0.tar.gz", hash = "sha256:4c1d..." }

[overrides]
jsonx = { path = "../my_jsonx_fork" }

[workspace]
members = ["libs/shared", "cmd/*"]

[build]
backend = "llvm"

[format]
indent_style = "tabs"
tab_width = 4
line_width = 100

[target.linux]
c_libs = ["ssl", "crypto"]
```

| Table | Contents |
|---|---|
| `[package]` | `name` (required; letters, digits, `_`, `-`, starting with a letter or `_`; also the root import path), `version` (SemVer, default `0.1.0`), `edition` (default `2026`), and optional `description`, `license`, `authors`, `repository`, `homepage`, `keywords`, `bin` (output file name). Optional in a workspace-only root. |
| `[dependencies]` | name → source. Exactly one of: `{ path = "..." }` (relative to this manifest); `{ url = "https://...", hash = "sha256:<64 hex>" }` (a `.tar.gz`, `.tgz`, or `.zip`); `{ index = "https://.../pkg.toml", version = "<constraint>" }`. Constraints support `^`, `~`, `=`, `<`, `<=`, `>`, `>=`, `*`, and intersections. The key is the dependency's import root. |
| `[dev-dependencies]` | the same, usable only from tests ([§11](#11-testing)); never part of normal builds or passed on to dependents |
| `[overrides]` | root manifest only: force every reference to a dependency, direct or transitive, to a given `path` or `url` + `hash` |
| `[workspace]` | `members` (paths, or trailing `*` globs) and optional `exclude`. Members share one lockfile, `[overrides]`, and `vendor/`. |
| `[build]` | defaults for `backend` (`"llvm"` or `"c11"`), `release`, `target`, `cc`, `c_flags`, `c_libs`; command-line flags take precedence |
| `[format]` | formatter settings ([§2](#2-lexical-structure)); a `.lowlightfmt` file in a directory overrides them for that directory |
| `[target.<selector>]` | settings applied when the target matches an OS (`linux`, `darwin`, `windows`), OS group (`unix`), architecture (`amd64`, `arm64`, `riscv64`), or full triple (`[target."aarch64-unknown-linux-gnu"]`): `dependencies`, `dev-dependencies`, `c_libs`, `c_flags` |

Dependency tables may also be written as TOML sub-tables (`[dependencies.jsonx]`).

### 14.3 Import path resolution

Inside a project, a non-relative import path resolves, in order, to:

1. a standard library package, `std/<pkg>` (or `std/<group>/<pkg>`);
2. a package of this project, `<name>/<sub/dir>`, matching the directory `./<sub/dir>/`;
3. a declared dependency, `<dep>` or `<dep>/<sub/dir>` (dev-dependencies only from tests);
4. a workspace member.

Relative imports (`./x`, `../x`) are not allowed inside projects, so every package has one canonical path. Outside a project, imports resolve to `std/...` and to relative paths.

The default qualifier of a project or dependency root is its `[package].name` or dependency key; of a subdirectory, the directory name.

### 14.4 The lockfile

`lowlight.lock` sits at the project or workspace root. It is written by `lowlight` and should be committed:

```toml
# Code generated by `lowlight`; DO NOT EDIT.
lock_version = 1

[[package]]
name = "http_ext"
version = "0.3.4"
index = "https://acme.dev/pkgs/http_ext.toml"
url = "https://acme.dev/tarballs/http_ext-0.3.4.tar.gz"
hash = "sha256:71e2..."
dependencies = ["jsonx"]
```

Entries are sorted by name, version, and URL. Each has `name` and `version` (`0.0.0` for a raw archive without a version), and as applicable `index`, `url`, `hash` (required for every remote package), `path`, and the sorted names of its own `dependencies`.

### 14.5 Package indexes

A dependency's `index` is a single static TOML file, so any web server or object store can host packages:

```toml
[package]
name = "http_ext"

[versions."0.3.4"]
url = "https://acme.dev/tarballs/http_ext-0.3.4.tar.gz"
hash = "sha256:71e2..."

[versions."0.3.4".dependencies]
jsonx = { index = "https://example.org/pkgs/jsonx.toml", version = "^1.2.0" }

[versions."0.3.3"]
url = "https://acme.dev/tarballs/http_ext-0.3.3.tar.gz"
hash = "sha256:aa10..."
yanked = true
```

- Each version lists its archive, its hash, and optionally its dependencies (also per target), so the whole graph can be resolved before downloading anything.
- Yanked versions are skipped for new resolutions but remain usable when already locked.
- Indexes and archives must be served over HTTPS (except on loopback), and every archive is checked against its hash before it is extracted.
- Extraction rejects absolute paths, `..` components, and links pointing outside the package. A single top-level directory in an archive is unwrapped.

### 14.6 Resolution

1. **One version per dependency.** A build graph contains at most one copy of each dependency. Version constraints from all dependents are combined, and **minimal version selection** picks the smallest non-yanked version satisfying all of them. Unsatisfiable constraints, or conflicting `url` + `hash` pins, are errors that suggest an `[overrides]` entry.
2. **Precedence.** A dependency's files come from, in order: `[overrides]`; a matching workspace member; `vendor/<dep>/` (when `vendor/` exists, it is authoritative and nothing is fetched); the locked version in the local cache (`$LOWLIGHT_CACHE`, default `~/.lowlight/cache`).
3. **No hidden network access.** `check`, `build`, `run`, `test`, `doc`, `emit-*`, and the language server never use the network. A missing dependency, or a lockfile that disagrees with the manifest under `--locked`, is an error that tells you to run `lowlight fetch` or `lowlight update`. `--offline` forbids network access for every command.

### 14.7 Commands

| Command | Effect |
|---|---|
| `lowlight new <name>` · `lowlight init [--lib]` | create a project |
| `lowlight add <dep> ...` · `lowlight remove <dep>` | edit `[dependencies]`, preserving comments and layout (computing the hash for a `url`) |
| `lowlight fetch` | download what the lockfile lists, without changing versions |
| `lowlight update [<dep>...]` | re-resolve to newer allowed versions and rewrite the lockfile |
| `lowlight vendor` | copy all dependencies into `vendor/` |
| `lowlight tree` | print the dependency graph |
| `lowlight build`, `run`, `check`, `expand`, `test`, `doc`, `fmt`, `emit-llvm`, `emit-c` | inside a project, default to the current package; `-p <pkg>` and `--workspace` select others; `check --all-targets` checks every canonical platform target and `expand` prints post-comptime Lowlight |

---

# Part IV: Implementer's Guide

Part III defines what programs mean. This part explains how the reference compiler implements that meaning, and which choices a new implementation must make in the same way to be conforming. Where this part describes the reference compiler's internals (names of passes, C type layouts), it is **informative**: another implementation may do things differently as long as the observable behavior is the same. Statements marked **Required** are normative.

## 15. Compiler Architecture

The reference compiler is written in Go. One pipeline serves every command:

```text
source files ─► lexer ─► parser ─► AST ─► semantic analysis ─► checked program
                                             (types, names,       │
                                              ownership)          ▼
                     executable ◄─ C compiler / linker ◄─ backend ◄─ MIR (lowering)
                                                         (LLVM IR or C11)
```

| Stage | Package | Responsibility |
|---|---|---|
| Lexing | `internal/lexer`, `internal/token` | tokens, semicolon insertion, `?`/`!` name suffixes, `<-` recognition, string interpolation |
| Parsing | `internal/parser`, `internal/ast` | a syntax tree for each file; doc comments |
| Semantic analysis | `internal/sema`, `internal/types` | name resolution across a package, type checking, monomorphization, ownership and borrow checking, exhaustiveness |
| Lowering | `internal/ir` | a mid-level IR (MIR) in which every type is concrete, and every temporary, move, and destruction is explicit |
| Code generation | `internal/codegen` | LLVM IR (default) or ISO C11 from MIR, plus a small C runtime |
| Driver | `internal/driver` | packages, caching, invoking `clang`/`cc`, running programs and tests |
| Tools | `internal/format`, `internal/docgen`, `internal/lsp`, `internal/manifest` | formatter, documentation, language server, manifests and dependencies |

**Compilation units.** A package is all `.ll` files in one directory, minus test files outside `lowlight test` and files excluded by platform suffixes ([§4.6](#46-packages-and-imports)). Packages form a directed acyclic graph through their imports; inside a package, declarations may refer to each other in any order and recursively.

**Two-phase builds.** When building a project, each non-generic package is compiled to its own cached object file, keyed by a hash of its sources, the compiler version, and its dependencies' interfaces. Generic instantiations and interface vtables, which depend on how packages are used together, go into one shared unit compiled last. `--no-incremental` disables the cache. This is an optimization only; the output must be the same.

## 16. Parsing Ambiguities and Their Resolution

Lowlight's grammar ([Appendix A](#appendix-a-grammar)) is written to be readable, and a few constructs need lookahead or context to parse. A conforming parser must resolve each of them as described here (**Required**).

### 16.1 Semicolon insertion

The lexer keeps a stack of open delimiters. At each newline it inserts `;` exactly when the rules of [§2.3](#23-statement-separation) hold: only `{` is open (or nothing), the line ends in a value-ending token, and the next line does not begin with `else`, `when`, `.`, or `?.`. The parser then treats `;` as a statement terminator and is never involved in the decision.

Consequences that implementations must reproduce:

- A binary operator at the end of a line continues the expression (`a +` newline `b`); one at the start of the next line does not.
- Inside `( )`, `[ ]`, `#[ ]`, `+[ ]`, and `${ }`, newlines are insignificant, so a long condition can be wrapped in parentheses.
- `default` is not a continuation token. A `match` or chain arm beginning with `default` on a new line works because the preceding line ends an arm body with `;` inserted, and `default` is then recognized as starting the closing arm (§16.5).

### 16.2 Name suffixes and `<-`

- `?` and `!` join the preceding identifier when they directly follow it, except when they begin `??` or `!=` ([§2.5](#25-identifiers)). There is no space allowed between a name and its suffix.
- `<-` is a single token only with whitespace (or a start-of-input context) before it and whitespace, a terminator, or a closing delimiter after it ([§2.9](#29-operators-and-punctuation)). Otherwise `<` and `-` are separate tokens.

### 16.3 Square brackets after an expression

After an expression, `[` can begin an index, a slice, generic type arguments, or a struct literal. The parser decides as follows:

1. **Struct literal.** If the expression could be a type name (an identifier or qualified name that starts with an uppercase letter, possibly with type arguments) and the bracket is either `[]` or begins `name:` or `name?:`, it is a struct literal: `Point[x: 1, y: 2]`, `Config[]`.
2. **Type arguments.** Otherwise, the parser tries to read the bracket's contents as a comma-separated list of types. If that succeeds and the `]` is followed by `(` (a generic call: `type.size_of[int]()`), by a struct literal (`Pair[int][a: 1, b: 2]`), or, for a type-like name, by `.` and a name (`List[int].new()`), the bracket is type arguments.
3. **Index or slice.** Otherwise it is an index or a slice, a slice when the contents contain a top-level `..` or `..=`.

So `xs[i]`, `xs[0..2]`, and `grid[row][col]` are indexes, and `List[string].new()` is a call with type arguments. A conforming parser may implement rule 2 by speculative parsing with backtracking, as the reference parser does.

### 16.4 Parentheses: grouping or function literal

`(` starts a function literal when it is followed by `)`, by `name:` (except `name: {`, `name: for`, and `name: while`, which are labels inside a grouping), or by a receiver (`self`, `&self`, `own self`). Otherwise it is a parenthesized expression or a tuple of `match` subjects.

### 16.5 Colons

A `:` has several roles: type annotations, struct literal fields, map entries, array sizes, labels, the start of a body, and the start of a result chain. They are separated by position:

- In a declaration, `name:` before `=` is a type annotation.
- Inside `[ ]` or `#[ ]` after a field name or key, it separates a field or entry.
- `name: {`, `name: for(`, and `name: while(` at the start of an expression are labels.
- After an `if`, `for`, `while`, or `match` header (and its optional result type), and after a function's parameter list and return type, `:` begins the body.
- After a postfix expression, `:` begins a **result chain** if the next token is `when`, or is `default` followed by `:`, by `panic` (not followed by `(`), or by a list of failure patterns that ends in `:`. Anything else is not a chain.

The last rule means `x: default panic` is a chain while `default panic(...)` is not, and that a label or annotation is never mistaken for a chain.

### 16.6 Match arms and `default`

In a `match`, each arm begins with `when` or with the contextual word `default`. `default` starts the final arm when it is followed by `:` or by patterns (as many as there are subjects) and then `:`. After the final arm's body, the `match` is complete: a following `when` belongs to an enclosing `match`. This is what lets a result chain or `match` be nested inside another `match`'s arm ([§6.6](#66-result-chains)).

### 16.7 Contextual words

`next`, `default`, `panic`, `result`, and `packed` are identifiers except in the positions listed in [§2.6](#26-keywords). In particular, `next` is an unwind only when directly followed by `<-` or by a name and `<-`; a variable named `next` or a method `it.next()` is unaffected.

### 16.8 Bodies

A body after `:` is either a block `{ ... }` or a single expression. In `if`, loop, and `match` arm bodies the expression may also be an assignment statement or an unwind. A function body that is a bare assignment must be written as a block ([§6.1](#61-bodies)).

## 17. Semantic Analysis

### 17.1 Passes

Name resolution is **order-independent** within a package. The reference analyzer runs these passes over all packages of the program:

1. **Top-level pruning & integer constants.** Prune top-level `comptime if` declarations against `comp.TARGET` before type resolution, and evaluate integer constant expressions needed for array sizes and explicit enum tag values.
2. **Type headers.** Register every `define` name, so types may refer to each other in any order.
3. **Type bodies.** Resolve distinct types' bases, enum variants, result members, interface signatures, `@derivable` hooks, and struct fields.
4. **Recursion check.** Reject types that contain themselves by value (infinite size). Mark structs reached from themselves through an optional (`next?: ?Node`) so that such optionals are boxed (§18.2).
5. **Signatures & `@derive` attachment.** Register functions, `const` functions, methods, constants, `@derive`-generated methods, and `extern` declarations, and check for duplicates and conflicting method names on a type.
6. **Bodies & compile-time evaluation.** Check every function body (`types`, `?`/`!`/`_`, ownership, borrowing, exhaustiveness, unwind targets, `@comptime_only` boundaries), evaluate `const` initializers and inline `comptime` blocks/control flow in the simulated interpreter, unroll `comptime for` and prune local `comptime if`/`comptime match`, and validate runtime crossings. Generic declarations are checked once in generic form against their bounds, and then each instantiation (including const generics) is created on demand (§17.4).

Top-level constants whose initializers depend on each other are initialized in dependency order (or evaluated at compile time for `const`); a cycle is an error.

### 17.2 Ownership checking

For each function, the checker tracks a **move state** for every local resource variable: alive, moved, or maybe moved.

- A move (`own x`, a capture, moving a payload in a pattern) changes the state to moved. Reading a moved or maybe-moved variable is an error; assigning to it makes it alive.
- At a join point (after `if`/`else`, `match`, the end of a loop), the states from all incoming paths are merged: moved on every path gives moved, moved on some gives maybe moved, otherwise alive. Paths that end in `never` (an unwind, `panic`, `exit`) are excluded from the merge.
- A loop body that moves an outer variable must reassign it on every path before the next iteration.
- A field can be moved out only of an `own self` receiver. Doing so consumes the receiver: `self` counts as moved, so no other field can be read afterwards, and the remaining fields are destroyed individually.

Borrow checking is local because borrows never outlive a call or a branch ([§7.4](#74-borrowing-rules)). For each call, the checker compares the places passed as `&`, `own`, and read-only arguments and reports overlaps. For each `for` loop and each narrowed optional, it records the borrowed place and rejects mutation or moves of it inside the body. Arena pinning ([§7.6](#76-arenas-and-references)) is tracked per scope.

### 17.3 Patterns and exhaustiveness

A `match` on enum, result, or `bool` subjects is checked by computing, for each subject, the set of variants (or `true`/`false`) not yet covered by earlier arms. Each variant pattern removes its variant; since payload patterns contain only bindings and `_`, a variant pattern always covers its whole variant, and naming a variant a second time is an error. For several subjects, coverage is computed over the product of their cases, with `_` covering every case of its subject. `default Pattern:` is accepted only when its patterns cover exactly what remains; `default:` is always accepted. Literal, `in`, and method patterns never count toward coverage, so a `match` on numbers or strings always ends in `default:`.

**Bare names in patterns.** In a pattern, a bare identifier (or a bare call form `name(...)`) that names a variant of the subject's enum or result type is that variant. It is an error, reported as ambiguous, when a bare unit name is also a variable in scope, or a bare payload pattern is also a method of the subject's type ([§6.5](#65-pattern-matching)). An identifier that names no variant is resolved as an ordinary expression (a constant to compare against).

**Bare constructors.** In an expression whose expected type is an enum or result type `E`, a bare call or name that matches a variant of `E` (`value(x)`, `err(e)`, `missing(k)`) constructs that variant, unless a variable or function of the same name is in scope, in which case that name wins.

### 17.4 Generics

Generics are **monomorphized**: each distinct list of type arguments produces a separate copy of the function or type, checked and lowered with the arguments substituted. Instantiation is driven by use and recurses: instantiating `Tree[int]` may instantiate `List[Tree[int]]`. A depth of 128 nested instantiations is the limit, beyond which compilation fails with an error (so `f[T]` calling `f[List[T]]` is rejected instead of looping forever). Type arguments that are optional types are rejected ([§8.3](#83-type-parameters-are-never-optional)).

### 17.5 Interfaces

Interface satisfaction is structural. When a concrete type is converted to an interface type, the analyzer verifies that the type has every method with exactly matching parameter modes and types, and records a **vtable** for the pair. Interfaces that mention `Self` other than as the receiver (`Copyable`, `Equatable`, `Comparable`, `Addable`, `AddAssignable`) can be used only as generic bounds, never as runtime interface types.

## 18. Lowering and Runtime Representation

This chapter describes the representation used by the reference compiler. It is informative except where marked: programs cannot observe layouts (other than through `type.size_of`, `unsafe` code, and `extern` calls), and another implementation may choose differently. Layouts of `packed` structs and of types passed to `extern` functions follow the C ABI of the target (**Required**).

### 18.1 Primitive and composite values

| Lowlight | Representation |
|---|---|
| `int`, `i8`…`i64`, `u8`…`u64` | the C integer of that width (`int` is 64-bit) |
| `float`, `f32`, `f64` | IEEE-754 `double` / `float` |
| `bool` | C `bool` |
| `char` | 32-bit Unicode scalar value |
| `string` | `{ data: *u8, len: i64, cap: i64 }`. `cap == 0` means a borrowed view (a literal or a slice) that is not freed; `cap > 0` means owned heap memory. |
| struct | a C struct with fields in declaration order (an empty struct has one padding byte) |
| tuple `+[A, B]` | a struct with fields `_0`, `_1`, ... |
| array `[N: T]` | a struct wrapping `T data[N]` |
| list `[T]` | `{ data: *T, len: i64, cap: i64 }`. `cap == 0` with `len > 0` denotes a static `.rodata` slice promoted copy-on-write on mutation. |
| set, map | open-addressing hash tables: a bucket index array plus an insertion-ordered entry array (so iteration follows insertion order) |
| distinct type | the base type's representation |
| `ref[T]` | a non-null pointer into an arena |
| `ptr[T]` | a raw C pointer |

### 18.2 Optionals

`?T` is a struct `{ has_value: bool, value: T }`. The exception is an optional of a struct that refers to itself through optionals (`next?: ?Node`), detected in §17.1 step 4: such optionals are **boxed**, represented as a nullable pointer to a heap-allocated `T`. Boxing is what gives recursive structs a finite size. It is invisible to programs, except that moving a boxed optional moves only the pointer.

Because `?T` never nests ([§3.5](#35-optionals)), no representation for `??T` is needed.

### 18.3 Enums and results

- A **tag enum** is a C integer of the declared (or default) width holding each variant's value.
- A **tagged union** (an enum with any payload variant, or a result type) is `{ tag: i32, data: union { ...payloads } }`. Tags are numbered from **1** in declaration order. Tag **0** is reserved as an inert state that is never observed by programs: a value whose payload has been moved out, or that has been destroyed, is set to tag 0 so that destroying it again does nothing.

### 18.4 Moves and destruction

MIR makes every destruction an explicit instruction. The lowering inserts them as follows:

1. At each scope exit (normal end, or an unwind leaving it), it emits a drop for each live resource local of that scope, in reverse declaration order.
2. A move **zeroes** its source (a string or list with `cap = 0` and null data, a union with tag 0, an optional with `has_value = false`), so a drop of a moved value is a no-op. This is how maybe-moved variables are destroyed correctly without runtime flags.
3. For a struct with a custom `Drop`, whose zeroed form could still be a meaningful value, a separate boolean **drop flag** records whether it was moved.
4. Temporaries are materialized into hidden locals and dropped at the end of their statement.
5. A drop function exists for every resource type: for a struct with `Drop`, it calls `drop(&self)` first, then drops fields in reverse order; for a tagged union, it switches on the tag; for a collection, it drops each element and frees the buffer.

`.copy()` on built-in types and on structs and enums without a custom implementation is a generated deep copy function.

### 18.5 Control flow

- `if`, `match`, and blocks that produce values are lowered to assignments into a result local followed by jumps.
- An **unwind** becomes: evaluate its value into the target's result local, emit drops for every scope between the unwind and its target (innermost first), then jump to the target's exit label. `func <- v` jumps to the function's return sequence.
- A **result chain** `e: when a(x): A ... default ...` is lowered as a `match` on `e` with an implicit first arm `when value(v): v` that moves the success value out, followed by the written arms. `default panic` becomes an arm that calls the panic routine with `unhandled failure Type.variant` and the chain's source location.
- `a ?? b` evaluates `a` into a temporary, tests `has_value` (or the `value` tag), and evaluates `b` only on the other branch. On a result, the failure payload is dropped before `b` is evaluated.
- `a?.m()` is a conditional on `a`'s presence that wraps the result in an optional.

### 18.6 Loops

A `for(x in c)` loop is lowered according to the type of `c`:

| `c` | Lowering |
|---|---|
| range | a counted loop from the start to the end bound |
| list, array, string, slice | an index loop over `0..len` (strings by byte offset) |
| set, map | a loop over the entry array, skipping deleted entries |
| a type with `iter()` | `let it = c.iter()`, then `while` `it.next()` has a value |
| an `Iterator` | `next()` directly |

A loop with a result type `T` creates an empty `T` before the loop, and each body value is passed to `T.push` (`Collectable`). A `while` loop's value comes only from its unwinds and its `else` arm. `next <-` jumps to the loop's continue label after dropping the iteration's locals.

### 18.7 Functions, closures, and interfaces

- A **top-level function** is a C function. Read-only parameters of resource type are passed by value without copying (the caller keeps ownership, and the callee never drops them); `&T` parameters are pointers; `own T` parameters are passed by value, and the callee drops them.
- An **`fn` value** is one code pointer to an entry function that takes an (unused) environment pointer as a hidden first argument; a top-level function used as a value points to a static thunk. Calling it passes a null environment.
- A **closure** (`cl`) is `{ fn, env, copy_env, drop_env }`: the same kind of code pointer, a pointer to a heap environment struct holding the captures, and functions to copy and destroy it. Converting an `fn` to a `cl` stores its code pointer with a null environment.
- An **interface value** is `{ data, vtable, owned }`: a pointer to a heap copy of the concrete value, a pointer to the static vtable for (type, interface), which holds a drop function, a clone function, and one entry per method, and a flag saying whether the box owns `data`.

### 18.8 Strings and interpolation

A string literal is a borrowed view of static data. `"a ${x} b"` is lowered to a sequence of appends into a new owned string: literal parts are copied, and each `${expr}` appends `expr.to_string()` (with built-in fast paths for numbers, `bool`, `char`, and `string`). The temporary strings from `to_string` are dropped immediately.

### 18.9 Backends

The LLVM backend emits textual LLVM IR, which `clang` compiles and links with the runtime. The C11 backend emits a single C file that includes the runtime header (`runtime_h.go`) and compiles with any C11 compiler. Both must produce the same observable behavior. In both, integer arithmetic is emitted so that wrapping is defined (never relying on C signed-overflow behavior), and the checks of [§5.3](#53-arithmetic) and [§5.7](#57-indexing-and-slicing) stay in `--release` builds.

## 19. The Runtime Contract

These behaviors are observable and **Required**.

1. **Program start.** Before `main` runs, top-level constants are initialized in dependency order. `args()` returns the arguments as given to the process.
2. **Program end.** When `main` returns, resource constants are destroyed in reverse initialization order, standard output is flushed, and the process exits with `0` (or the `int` `main` returned). `exit(code)` flushes standard output and exits immediately with `code`, without destruction.
3. **Panics and traps.** A panic, from `panic`, `assert`, a failed `unwrap`/`expect`, `default panic`, or a runtime trap ([Appendix D](#appendix-d-runtime-traps)):
   - flushes standard output;
   - writes `lowlight panic at FILE:LINE:COL: MESSAGE` to standard error, followed by a newline (traps raised by generated code report only `FILE:LINE`);
   - for a panic with an `Error`, writes the error's summary as the message, then `trace:` and one line per frame, `  at FILE:LINE:COL — note`;
   - exits with status `1`, without running any destruction.
   `FILE` is the source path as given to the compiler when it is relative; an absolute path is shown relative to the working directory when the file is inside it, and otherwise as its base name.
4. **Under `lowlight test`**, a panic inside a test ends that test only: the runner records it as a failure with the panic message and location, and continues with the next test. Resources owned by the failed test are leaked, not destroyed.
5. **Standard streams.** `print` and `println` write to standard output, which is buffered; `eprint` and `eprintln` write to standard error, unbuffered. Output written before a panic is never lost.
6. **Memory exhaustion.** If an allocation fails, the program flushes standard output, writes `lowlight panic: out of memory (requested N bytes)` to standard error, and aborts. This cannot be intercepted, even by the test runner.

## 20. Conformance Checklist

An implementation is conforming when it does all of the following. Each item links to the rule it summarizes.

**Lexing and parsing**
- [ ] Inserts semicolons exactly as in [§2.3](#23-statement-separation) and enforces trailing commas as in [§2.4](#24-trailing-commas).
- [ ] Lexes `?`/`!` suffixes and `<-` as in [§2.5](#25-identifiers) and [§2.9](#29-operators-and-punctuation).
- [ ] Resolves the ambiguities of [§16](#16-parsing-ambiguities-and-their-resolution) the same way.
- [ ] Accepts exactly the grammar of [Appendix A](#appendix-a-grammar).

**Names and declarations**
- [ ] Enforces the `?` and `!` naming rules and `_`-prefixed package privacy ([§2.5](#25-identifiers), [§4.7](#47-visibility)).
- [ ] Resolves top-level names order-independently within a package and rejects import cycles ([§4.6](#46-packages-and-imports)).
- [ ] Applies attributes as in [§4.8](#48-attributes).

**Types**
- [ ] Never nests optionals and never implicitly converts between numeric types ([§3.5](#35-optionals), [§3.16](#316-type-equivalence-and-implicit-conversions)).
- [ ] Distinguishes plain values from resources ([§3.1](#31-plain-values-and-resources)).
- [ ] Rejects infinite-size recursive types and accepts recursion through optionals, collections, and references ([§3.7](#37-structs)).
- [ ] Checks result declarations: exactly one `value` member and at least one failure ([§3.9](#39-results)).
- [ ] Checks interface satisfaction structurally ([§3.11](#311-interfaces)).

**Expressions and control flow**
- [ ] Evaluates operands left to right, once ([§5.1](#51-evaluation-order-and-precedence)).
- [ ] Wraps integer arithmetic and traps on division by zero, bad shifts, and out-of-range indexes and slices in every build mode ([§5.3](#53-arithmetic), [§5.7](#57-indexing-and-slicing)).
- [ ] Requires `default` at the end of every `match` and checks exhaustiveness ([§6.5](#65-pattern-matching)).
- [ ] Implements result chains, including `default panic` ([§6.6](#66-result-chains)).
- [ ] Implements all unwind forms and labels, with destruction on exit ([§6.7](#67-unwinds-and-labels)).

**Ownership**
- [ ] Requires `own` for moves of existing variables, and rejects use after move, including maybe-moved variables ([§7.2](#72-moves)).
- [ ] Enforces parameter modes and the borrowing rules ([§7.3](#73-parameter-modes), [§7.4](#74-borrowing-rules)).
- [ ] Destroys values deterministically in reverse declaration order, running `drop` first ([§7.5](#75-destruction)).
- [ ] Prevents `ref[T]` from outliving its arena ([§7.6](#76-arenas-and-references)).

**Generics, comptime, failure, unsafe, testing**
- [ ] Monomorphizes type and const generic parameters (`[N: const Type]`), checks bounds, and rejects optional type arguments ([§8](#8-generics)).
- [ ] Evaluates `const`, `comptime` blocks/control flow, reflection queries, `@derivable` derivations, `comp.TARGET`, and `comp.embed_*` within resource budgets and crossing rules ([§4.2](#42-bind-const-and-let), [§12.2](#122-deriving-methods)).
- [ ] Implements `Result`, `Error`, and panics with the exact output format ([§9](#9-failure-handling), [§19](#19-the-runtime-contract)).
- [ ] Requires `unsafe` for the operations of [§10](#10-unsafe-code-and-foreign-functions) and follows the C ABI for `extern`.
- [ ] Discovers and runs tests as in [§11](#11-testing).

**Library and tooling**
- [ ] Provides the prelude of [§12](#12-the-prelude) with the `@lang` hooks.
- [ ] Resolves imports and dependencies as in [§14](#14-packages-manifests-and-dependencies), with no network access during builds.

---

# Appendices

## Appendix A: Grammar

This is the complete syntax of Lowlight in Extended Backus–Naur Form. `{ x }` means zero or more, `[ x ]` means optional, and quoted text is literal. Semicolons (`;`) are those written or inserted by the lexer ([§2.3](#23-statement-separation)). Every comma-separated list allows a trailing comma, which is required when the list spans lines ([§2.4](#24-trailing-commas)). Ambiguities are resolved as in [§16](#16-parsing-ambiguities-and-their-resolution).

### A.1 Lexical elements

```ebnf
Ident       = ( Letter | "_" ) { Letter | Digit | "_" } ;
OptIdent    = Ident "?" ;                         (* no space before "?" *)
ResIdent    = Ident "!" ;                         (* no space before "!" *)
Name        = Ident | OptIdent | ResIdent ;

IntLit      = DecDigits | ( "0x" | "0X" ) HexDigits | "0b" BinDigits ;      (* "_" allowed between digits *)
FloatLit    = DecDigits "." DecDigits [ Exponent ] ;
Exponent    = ( "e" | "E" ) [ "+" | "-" ] DecDigits ;
CharLit     = "'" ( Char | Escape ) "'" ;
StringLit   = '"' { Char | Escape | Interp } '"'
            | "`" { AnyChar | Interp } "`" ;
Interp      = "$" ( Ident | "self" ) | "${" Expr "}" ;
Escape      = "\n" | "\t" | "\r" | "\0" | "\\" | "\'" | '\"' | "\$" | "\`" | "\x" Hex Hex | "\u{" Hex { Hex } "}" ;   (* "\`" in backtick strings only *)
BoolLit     = "true" | "false" ;
Literal     = IntLit | FloatLit | CharLit | StringLit | BoolLit | "none" ;
```

### A.2 Files and declarations

```ebnf
File        = { TopDecl ";" } ;
TopDecl     = { Attribute } ( ImportDecl | ExternDecl | TypeDecl | VarDecl | FuncDecl | MethodDecl ) ;

Attribute   = "@" Ident [ "(" [ AttrArg { "," AttrArg } ] ")" ] ;
AttrArg     = StringLit | Ident ;

ImportDecl  = "import" StringLit [ "as" Ident ] ;

ExternDecl  = "extern" StringLit
            | "extern" ( Ident | StringLit | Keyword ) [ "as" Ident ] ":" "fn" "(" [ Params ] ")" [ Type ] ;

VarDecl     = ( "let" | "bind" | "const" ) Name [ ":" Type ] "=" Expr
            | ( "let" | "bind" | "const" ) Target { "," Target } "=" "unpack" Expr ;
Target      = ( Name | "_" ) [ ":" Type ] ;

FuncDecl    = ( "bind" | "const" ) Ident [ TypeParams ] "=" FuncLit ;     (* must be "fn" *)
MethodDecl  = ( "bind" | "const" ) TypeRef "." Ident [ TypeParams ] "=" FuncLit ; (* must be "fn" *)
TypeRef     = ( Ident | PrimitiveType | "string" ) [ TypeParams ] ;

TypeDecl    = "define" [ "packed" ] Ident [ TypeParams ] TypeBody ;
TypeBody    = "struct" "[" [ Field { "," Field } ] "]"
            | "enum" [ IntType ] "[" [ Variant { "," Variant } ] "]"
            | "result" "[" Variant { "," Variant } "]"
            | "interface" "[" [ MethodSig { "," MethodSig } ] "]"
            | Type ;                                            (* distinct type *)
Field       = { Attribute } Name ":" Type [ "=" Expr ] ;
Variant     = { Attribute } Ident [ "=" Expr ]                  (* unit variant, optional tag value *)
            | { Attribute } Ident ":" Type ;                    (* payload variant *)
MethodSig   = { Attribute } Ident ":" "fn" "(" [ Params ] ")" [ Type ] ;

TypeParams  = "[" TypeParam { "," TypeParam } "]" ;
TypeParam   = Ident [ ":" Ident { "+" Ident } ] ;
```

### A.3 Types

```ebnf
Type        = PrimitiveType | "string" | "void" | "never"
            | "?" Type                                          (* optional *)
            | "[" Type "]"                                      (* list *)
            | "[" Expr ":" Type "]"                             (* array; constant size *)
            | "#[" Type "]"                                     (* set *)
            | "#[" Type ":" Type "]"                            (* map *)
            | "+[" [ Type { "," Type } ] "]"                    (* tuple *)
            | ( "fn" | "cl" ) "(" [ ParamType { "," ParamType } ] ")" [ Type ]  (* function, closure *)
            | "ref" "[" Type "]" | "ptr" "[" Type "]"
            | "Self"
            | [ Ident "." ] Ident [ TypeArgs ] ;                (* named, possibly from a package *)
ParamType   = [ "&" | "own" ] Type ;
TypeArgs    = "[" Type { "," Type } "]" ;
PrimitiveType = "int" | "i8" | "i16" | "i32" | "i64" | "u8" | "u16" | "u32" | "u64"
              | "float" | "f32" | "f64" | "bool" | "char" ;
IntType     = "int" | "i8" | "i16" | "i32" | "i64" | "u8" | "u16" | "u32" | "u64" ;
```

### A.4 Statements and bodies

```ebnf
Block       = "{" { Stmt ";" } [ Expr ] "}" ;
Stmt        = { Attribute } ( VarDecl | TypeDecl | Assign | Expr ) ;
Assign      = Place AssignOp Expr
            | Place { "," Place } "=" "unpack" Expr ;
AssignOp    = "=" | "+=" | "-=" | "*=" | "/=" | "%=" | "&=" | "|=" | "^=" | "<<=" | ">>=" ;
Place       = Name | Place "." Name | Place "[" Expr "]" | "*" UnaryExpr ;

Body        = Block | Expr | Assign ;                          (* Assign not allowed as a function body *)
```

### A.5 Expressions

```ebnf
Expr        = Unwind | Coalesce ;
Unwind      = ( "func" | "for" | "while" | "if" | "match" | "next" [ Ident ] | Ident ) "<-" [ Expr ]
            | "<-" [ Expr ] ;
Coalesce    = OrExpr [ "??" Expr ] ;
OrExpr      = AndExpr { "or" AndExpr } ;
AndExpr     = BitOrExpr { "and" BitOrExpr } ;
BitOrExpr   = BitXorExpr { "|" BitXorExpr } ;
BitXorExpr  = BitAndExpr { "^" BitAndExpr } ;
BitAndExpr  = EqExpr { "&" EqExpr } ;
EqExpr      = RelExpr [ ( "==" | "!=" ) RelExpr ] ;
RelExpr     = RangeExpr [ ( "<" | "<=" | ">" | ">=" ) RangeExpr ] ;
RangeExpr   = ShiftExpr [ ( ".." | "..=" ) ShiftExpr ] ;
ShiftExpr   = AddExpr { ( "<<" | ">>" ) AddExpr } ;
AddExpr     = MulExpr { ( "+" | "-" ) MulExpr } ;
MulExpr     = UnaryExpr { ( "*" | "/" | "%" ) UnaryExpr } ;
UnaryExpr   = ( "-" | "not" | "~" | "*" | "&" | "own" | "unsafe" | "comptime" ) UnaryExpr
            | ChainExpr ;
ChainExpr   = PostfixExpr [ ResultChain ] ;

PostfixExpr = Primary { "." Name | "?." Name | [ TypeArgs ] "(" [ Args ] ")" | "[" Expr "]" | "[" Slice "]" } ;
Slice       = [ Expr ] ".." [ Expr ] | [ Expr ] "..=" Expr ;
Args        = Arg { "," Arg } ;
Arg         = [ "&" | "own" ] Expr ;

Primary     = Literal | Name | "self" | "Self"
            | TypeName [ TypeArgs ] "[" [ FieldInit { "," FieldInit } ] "]"   (* struct literal *)
            | "[" [ Expr { "," Expr } ] "]"                                     (* list *)
            | "[" Expr ":" Expr { "," Expr } "]"                                (* array *)
            | "#[" [ Expr { "," Expr } | Expr ":" Expr { "," Expr ":" Expr } ] "]"   (* set or map *)
            | "+[" [ Expr { "," Expr } ] "]"                                    (* tuple *)
            | FuncLit | Block | "unsafe" Block | "comptime" Block | Labeled
            | IfExpr | ForExpr | WhileExpr | MatchExpr
            | PrimitiveType "(" Expr ")"                                        (* conversion *)
            | "cl" "(" Expr ")"                                                 (* fn to closure *)
            | "(" Expr ")" ;
TypeName    = [ Ident "." ] Ident ;                                             (* begins with an uppercase letter *)
FieldInit   = Name ":" Expr ;
FuncLit     = ( "fn" | "cl" ) "(" [ Params ] ")" [ Type ] ":" Body ;
Params      = Param { "," Param } ;
Param       = ( Name ":" [ "&" | "own" ] Type ) | [ "&" | "own" ] "self" [ ":" Type ] ;
Labeled     = Ident ":" ( Block | ForExpr | WhileExpr ) ;
```

### A.6 Control flow

```ebnf
IfExpr      = "if" "(" Cond ")" [ Type ] ":" Body
              { "else" "if" "(" Cond ")" [ Type ] ":" Body }
              [ "else" ":" Body ] ;
Cond        = Expr | [ "&" ] OptIdent | [ "&" ] Expr "as" Name ;

ForExpr     = "for" "(" LoopHeader ")" [ Type ] ":" Body [ "else" ":" Body ] ;
WhileExpr   = "while" "(" LoopHeader ")" [ Type ] ":" Body [ "else" ":" Body ] ;
LoopHeader  = Expr | LoopVar [ "," LoopVar ] "in" Expr ;
LoopVar     = [ "&" ] ( Name | "_" ) ;

MatchExpr   = "match" [ "(" [ Expr { "," Expr } ] ")" ] [ Type ] ":"
              { "when" Cases ":" Body } "default" [ Cases ] ":" Body ;
Cases       = Case { "," Case } ;
Case        = Pattern | "(" Pattern { "," Pattern } ")" ;
Pattern     = Literal | "_" | "in" Expr
            | [ TypeName "." ] Ident [ "(" PayloadBind ")" ]        (* variant *)
            | Ident "(" [ Args ] ")"                                (* method pattern *)
            | Expr ;                                                (* in `match:` arms: a bool condition *)
PayloadBind = "_" | [ "&" | "own" ] Name ;

ResultChain = ":" { "when" FailureCases ":" Body } ChainEnd ;
FailureCases = FailurePat { "," FailurePat } ;
FailurePat  = Ident [ "(" PayloadBind ")" ] ;
ChainEnd    = "default" [ FailureCases ] ":" Body
            | "default" "panic" ;
```

## Appendix B: Keywords and Contextual Words

**Reserved keywords** (32): `and`, `as`, `bind`, `cl`, `comptime`, `const`, `define`, `else`, `enum`, `extern`, `false`, `fn`, `for`, `func`, `if`, `import`, `in`, `interface`, `let`, `match`, `none`, `not`, `or`, `own`, `self`, `Self`, `struct`, `true`, `unpack`, `unsafe`, `when`, `while`.

**Contextual words**, identifiers everywhere except as shown:

| Word | Special meaning |
|---|---|
| `next` | `next <-`, `next label <-` |
| `default` | the final arm of a `match` or result chain |
| `panic` | `default panic` in a result chain |
| `result` | `define Name result [...]` |
| `packed` | `define packed Name struct [...]` |

**Predeclared names**, which are not keywords: the primitive types (`int`, `i8`…`u64`, `float`, `f32`, `f64`, `bool`, `char`, `string`), `void`, `never`, `ref`, `ptr`, the universe functions ([§12.3](#123-universe-functions)), and every name exported by the prelude ([§12](#12-the-prelude)).

## Appendix C: Operator Precedence

From tightest to loosest:

| Level | Operators | Associativity |
|---|---|---|
| 1 | `.` `?.` `()` `[]` | left |
| 2 | prefix `-` `not` `~` `*` `&` `own` `unsafe` `comptime` | right |
| 3 | `*` `/` `%` | left |
| 4 | `+` `-` | left |
| 5 | `<<` `>>` | left |
| 6 | `..` `..=` | none |
| 7 | `<` `<=` `>` `>=` | none |
| 8 | `==` `!=` | none |
| 9 | `&` | left |
| 10 | `^` | left |
| 11 | `\|` | left |
| 12 | `and` | left |
| 13 | `or` | left |
| 14 | `??` | right |
| 15 | unwind `<-` | right |

A result chain's `:` applies to a postfix expression, so `a.b(c): default panic` chains on the call. Assignment is a statement, not an operator.

## Appendix D: Runtime Traps

A trap is a panic raised by the language itself ([§19](#19-the-runtime-contract)). Every trap below is checked in every build mode.

| Trap | Message | Raised by |
|---|---|---|
| Index out of range | `index I out of bounds for length L` | `xs[i]`, `s[i]`, `arr[i]` |
| Slice out of range | `vector slice bounds out of range` · `string slice bounds out of range` | `xs[lo..hi]`, `s[lo..hi]` |
| Division by zero | `integer division by zero` · `unsigned division by zero` | `/` |
| Remainder by zero | `integer modulo by zero` · `unsigned modulo by zero` | `%` |
| Bad shift | `shift count out of range` | `<<`, `>>` with a count outside `0 ..< bits` |
| Unhandled failure | `unhandled failure Type.variant`, followed by the failure's error and trace when it carries an `Error` | `default panic` in a result chain |
| Unwrap | `called Result.unwrap on an error: <error summary>` | `Result.unwrap()` |
| Expect | `<msg>: <error summary>` | `Result.expect(msg)` |
| Unwrap error | `called Result.unwrap_err on a value` | `Result.unwrap_err()` |
| Assertion | `assertion failed`, or the given message | `assert(cond)`, `assert(cond, msg)` |
| Invalid character | `invalid char code point N` | `char(n)` with `n` outside the Unicode scalar values |
| List index | `List.must_insert: index I out of bounds for length L` (also `List.must_remove`, `List.must_swap_remove`) | `must_insert`, `must_remove`, `must_swap_remove`, attributed to the caller |
| Out of memory | `out of memory (requested N bytes)` | any allocation; aborts the process ([§19](#19-the-runtime-contract)) |

Arithmetic overflow is **not** a trap: integer arithmetic wraps ([§5.3](#53-arithmetic)).

## Appendix E: Glossary

| Term | Meaning |
|---|---|
| **arena** | an owner of many values destroyed together, handing out `ref[T]` references ([§7.6](#76-arenas-and-references)) |
| **associated function** | a function attached to a type without a receiver, called as `Type.name()` ([§4.4](#44-methods-and-associated-functions)) |
| **bind** | declare an immutable name ([§4.2](#42-bind-const-and-let)) |
| **body** | the block or single expression after a `:` in a function, `if`, loop, or arm ([§6.1](#61-bodies)) |
| **borrow** | temporary access to a value without owning it; read-only by default, mutable with `&` ([§7.3](#73-parameter-modes)) |
| **bound** | an interface a type parameter must satisfy, `T: Hashable` ([§8.2](#82-bounds)) |
| **comptime** | compile-time evaluation context (`comptime expr`, `comptime if`/`for`/`match`) and `comp.*` introspection ([§4.2](#42-bind-const-and-let)) |
| **const** | declare a compile-time constant or compile-time-capable function ([§4.2](#42-bind-const-and-let)) |
| **destruction** | releasing a resource at the end of its owner's scope, running `drop` first if defined ([§7.5](#75-destruction)) |
| **distinct type** | a new nominal type with the representation of an existing one ([§3.10](#310-distinct-types)) |
| **exhaustive** | covering every possible value of a `match` subject ([§6.5](#65-pattern-matching)) |
| **failure** | any member of a result type other than `value` ([§3.9](#39-results)) |
| **instantiation** | a copy of a generic declaration with concrete type arguments ([§8.4](#84-inference-and-instantiation)) |
| **label** | a name on a block or loop, targeted by `name <-` ([§6.7](#67-unwinds-and-labels)) |
| **move** | transfer of a resource to a new owner, leaving the source unusable ([§7.2](#72-moves)) |
| **narrowing** | treating an optional variable as its value inside `if(x?)` ([§6.2](#62-if)) |
| **optional** | `?T`, a value that may be absent ([§3.5](#35-optionals)) |
| **owner** | the variable, field, element, temporary, or arena responsible for destroying a value ([§7.1](#71-owners)) |
| **package** | all `.ll` files in one directory ([§4.6](#46-packages-and-imports)) |
| **place** | an expression that denotes storage: a variable, field, element, or dereference ([§5.12](#512-assignment)) |
| **plain value** | a value with no owned resources, copied freely ([§3.1](#31-plain-values-and-resources)) |
| **prelude** | `std/core`, imported implicitly into every file ([§12](#12-the-prelude)) |
| **receiver** | the `self` parameter of a method, in mode `self`, `&self`, or `own self` ([§4.4](#44-methods-and-associated-functions)) |
| **resource** | a value that owns memory or another resource and therefore moves instead of copying ([§3.1](#31-plain-values-and-resources)) |
| **result chain** | `expr: when ... default ...`, handling a result's failures and evaluating to its value ([§6.6](#66-result-chains)) |
| **result type** | a type declared with `result`, with one `value` member and one or more failures ([§3.9](#39-results)) |
| **subject** | the value or values a `match` examines ([§6.5](#65-pattern-matching)) |
| **tag enum** | an enum with no payloads, each variant an integer ([§3.8](#38-enums)) |
| **tagged union** | an enum with at least one payload variant ([§3.8](#38-enums)) |
| **trap** | a panic raised by the language on an invalid operation ([Appendix D](#appendix-d-runtime-traps)) |
| **unwind** | `<-`, leaving a function, loop, block, or iteration early ([§6.7](#67-unwinds-and-labels)) |
| **vtable** | the table of method pointers behind an interface value ([§18.7](#187-functions-closures-and-interfaces)) |
