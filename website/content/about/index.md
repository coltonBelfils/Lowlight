---
title: About Lowlight
nav: About
summary: What Lowlight is for, the principles behind it, and the state of the project.
order: 0
---

# About Lowlight

Lowlight is a compiled systems programming language. It aims to give programmers the control of C (native code, predictable memory layout, direct access to the machine and to C libraries) while removing the classes of bugs that make systems code hard: use-after-free, double free, null dereferences, uninitialized data, undefined behavior, and ignored errors.

Its distinguishing idea is **visibility**. Lowlight doesn't hide effects behind convenient syntax. Wherever something important happens, the source says so, at the place it happens:

| What happens | How you see it |
|---|---|
| a call changes your variable | `f(&x)` |
| a call takes your value | `f(own x)` |
| a value may be absent | its name ends in `?` |
| a value may be a failure | its name ends in `!` |
| a failure is passed on | a result chain at the call |
| a function or loop is left early | `func <-`, `for <-`, `next <-` |
| the compiler can't check something | `unsafe` |
| a name is private | it starts with `_` |

Everything else stays light: types are inferred, variants are named bare in patterns, single-expression bodies need no braces, and statements need no semicolons.

## Design principles

1. **Deterministic lifetimes.** Memory and other resources are released at the end of the scope that owns them, in reverse order. No garbage collector, no reference counting.
2. **Plain values and resources.** Every type either copies freely or has exactly one owner and moves.
3. **No null.** Absence is a type, `?T`.
4. **Failure is a value.** Operations that can fail return result types; bugs panic and can't be caught.
5. **No lifetime annotations.** Borrows are kept short enough that the compiler never needs them.
6. **Visible effects.** Mutation and ownership transfer are marked at the call site.
7. **Expressions everywhere.** Blocks, conditionals, loops, and matches produce values.
8. **Target neutrality.** The language means the same thing on every backend and platform.

The [specification](/spec/conventions-and-conformance.html#12-design-principles) states these principles and the rules that follow from them.

## Status

Lowlight is under active development. The compiler, standard library, language server, editor integrations, formatter, documentation generator, test runner, and package manager are all functional and tested, and the language definition is complete. Concurrency is being designed, and the language may still change in small ways before a stable release. [Version history](history.html) records how it has evolved.

## This documentation

Everything on this site is generated from the Lowlight repository by `go run ./cmd/lowlight-site`:

- the **specification**, **style guide**, and **implementation guide** are the repository's `SPECIFICATION.md`, `CONVENTIONS.md`, and `IMPLEMENTATION.md`;
- the **package reference** is generated from the standard library's doc comments;
- the **tour**, **tutorials**, **guides**, and **examples** live in `website/`, and every program in them is compiled and run by the test suite, so the documentation can't drift from the compiler.

## More

- [Frequently asked questions](faq.html)
- [Contributing](contributing.html)
- [Version history](history.html)
