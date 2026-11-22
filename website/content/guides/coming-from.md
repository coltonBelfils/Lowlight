---
title: Lowlight for C, Rust, and Go programmers
nav: Coming from C, Rust, or Go
summary: What carries over from other languages, what's different, and a side-by-side translation table.
order: 6
group: Writing Lowlight
---

# Lowlight for C, Rust, and Go programmers

## Side by side

| Concept | C | Rust | Go | Lowlight |
|---|---|---|---|---|
| Immutable binding | `const int x = 1;` | `let x = 1;` | `const x = 1` | `bind x = 1` |
| Mutable variable | `int x = 1;` | `let mut x = 1;` | `x := 1` | `let x = 1` |
| Function | `int f(int a)` | `fn f(a: i32) -> i32` | `func f(a int) int` | `bind f = (a: int) int: ...` |
| Return | `return x;` | `return x` / tail | `return x` | `func <- x` / tail |
| Break / continue | `break;` `continue;` | `break` `continue` | `break` `continue` | `for <-` `next <-` |
| Struct literal | `(Point){1, 2}` | `Point { x: 1, y: 2 }` | `Point{X: 1, Y: 2}` | `Point[x: 1, y: 2]` |
| Tagged union | manual | `enum` | interfaces | `define X enum [...]` |
| Switch | `switch` | `match` | `switch` | `match(x): when ...` |
| Null | `NULL` | `Option<T>` | `nil` | `?T`, `none`, names ending in `?` |
| Errors | return codes | `Result<T, E>`, `?` | `(T, error)` | result types, result chains |
| Logical operators | `&& \|\| !` | `&& \|\| !` | `&& \|\| !` | `and or not` |
| Generics | macros | `<T: Bound>` | `[T Constraint]` | `[T: Bound]` |
| Interfaces | function pointers | traits | interfaces (structural) | interfaces (structural) |
| Visibility | `static` | `pub` | capitalization | leading `_` is private |
| Memory | `malloc`/`free` | ownership + lifetimes | garbage collector | ownership, no lifetimes |
| Unchecked code | everything | `unsafe {}` | `unsafe` package | `unsafe` prefix |

## Coming from C

**What's familiar.** Native code, predictable layout, structs laid out in C order, direct calls to C functions, no runtime to speak of, and the same performance model.

**What's different.**

- **Memory is freed for you**, deterministically, at the end of the owner's scope. There's no `free` in safe code, and no use-after-free or double free.
- **No null.** A pointer-like value that may be absent is an optional, and you must check it.
- **No undefined behavior in safe code.** Integer overflow wraps, and out-of-range indexing, division by zero, and bad shifts stop the program with a message, in debug and release builds alike.
- **Strings are a real type**: UTF-8, with a length, growable, freed automatically. `s.c_str()` gives C a NUL-terminated pointer.
- **No preprocessor and no headers.** Packages are directories; imports are by path.

## Coming from Rust

**What's familiar.** Ownership and moves, enums with payloads and exhaustive `match`, `Option`/`Result`-style failure handling, traits as bounds, monomorphized generics, `unsafe` for raw pointers, and `Drop`.

**What's different.**

- **No lifetimes.** Borrows can't be stored or returned; they last for a call, a loop, or a branch. Data that must be shared uses [arenas or pools](/guides/memory.html). You give up some expressiveness and get code with no lifetime parameters at all.
- **Moves and mutable borrows are marked at the call site**: `f(own x)`, `f(&x)`. Reading code shows every ownership change.
- **No `?` operator.** Propagation is a one-line result chain, `default err(own e): func <- err(e.wrap("context"))`, which makes the context you add explicit.
- **Result types name their failures**: `define Lookup result [value: T, missing: string, forbidden]`, and a chain handles each by name.
- **Names carry type information**: optionals end in `?` and results in `!`, enforced by the compiler.
- **Interfaces are structural**: no `impl Trait for Type`; having the methods is enough.
- **Panics can't be caught**, and they don't unwind.

## Coming from Go

**What's familiar.** Packages are directories and imports are paths, structural interfaces, a built-in formatter and test runner, fast compilation, and a single toolchain command.

**What's different.**

- **No garbage collector.** Values are freed when their owner goes out of scope. You'll pass values with `&` when a function should change them and `own` when it should keep them.
- **No nil**, and no zero values for missing data: optionals make absence explicit.
- **Errors are values with types**: instead of `if err != nil`, a result chain handles each failure, and the compiler checks that none are forgotten.
- **Enums with payloads** replace interface-based sum types, with exhaustive `match`.
- **Expressions everywhere**: `if`, `match`, and loops produce values.
- **No goroutines yet.** Concurrency is being designed.

## Idioms to unlearn

- **Don't write getters and setters by reflex.** Public fields are fine; use private fields (`_name`) only when there is an invariant to protect.
- **Don't reach for exceptions or sentinel values.** Absence is `?T`; failure is a result.
- **Don't fight the ownership rules with copies.** If you copy a lot, rethink who owns what, or use an arena.

Next, read [Effective Lowlight](effective-lowlight.html) for the community style.
