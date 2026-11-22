---
title: Frequently asked questions
nav: FAQ
summary: Answers to common questions about Lowlight's design and use.
order: 10
---

# Frequently asked questions

## Design

### Why no garbage collector?

Predictability. A garbage collector decides when memory is freed and when the program pauses; Lowlight decides at compile time. Memory use is as low as the program's live data, destructors run at known points (closing files and sockets on time), and there is no runtime to tune. The ownership rules that make this safe are checked by the compiler.

### Why no lifetime annotations, when Rust needs them?

Lowlight restricts borrows instead. A borrow lasts at most one call, loop, or branch, and can't be stored or returned. With that restriction, the compiler can check every borrow locally, with no annotations. Structures that need long-lived references use arenas (`ref[T]`, checked at compile time) or pools (generational handles, checked at run time).

### Why do names end in `?` and `!`?

So you can see, wherever a name is used, whether it might be absent (`user?`) or a failure (`cfg!`). The compiler enforces the suffixes in both directions, so they never lie. It costs one character and removes a whole category of "I forgot this could be null" bugs.

### Why `<-` instead of `return` and `break`?

One operator with a target is simpler and more precise than several keywords. `func <- x` returns, `for <-` breaks, `next <-` continues, `outer <- x` leaves a labeled loop or block with a value, and the same operator works inside any expression, as in `let n = parse_int(s) ?? func <- 0`.

### Why `[` `]` for struct literals and generics?

Square brackets mean "a list of named or positional parts" throughout the language: struct fields `Point[x: 1, y: 2]`, type arguments `List[int]`, collection literals `[1, 2]`. Angle brackets would make `<` ambiguous in expressions.

### Why must every `match` end in `default`?

So exhaustiveness is visible. Writing `default variant:` for the last case means the compiler checks that every case is handled, and a new variant breaks the build instead of falling into a catch-all. Writing `default:` makes the catch-all explicit.

### Why are imports always qualified?

So you can always tell where a name comes from: `fs.read_file`, `http.Client`. Methods are the exception: importing `std/sort` makes `xs.sort()` available, because a method's receiver already says what it applies to.

### Can I catch a panic?

No. A panic means the program has a bug; continuing would be guessing. Expected failures are results, which you handle. The test runner contains panics to individual tests so one failure doesn't stop the run.

### Does Lowlight have exceptions, inheritance, or operator overloading?

No exceptions: failures are results. No inheritance: interfaces are structural, and types compose by containing other types. Operators can be given meaning for your types only through the prelude's interfaces (`Equatable` for `==`, `Comparable` for `<`), so `a + b` always means numeric addition or string concatenation.

## Using Lowlight

### What platforms are supported?

Lowlight is developed on macOS and Linux, on x86-64 and ARM64. The C11 backend makes it easy to target other systems with a C compiler, and `--target` cross-compiles.

### How fast is it?

Comparable to C for the same algorithm. It compiles through LLVM with optimizations, generics are specialized, and there is no runtime overhead beyond the bounds and division checks, which stay on in release builds. See [Performance](/guides/performance.html).

### Can I use C libraries?

Yes, directly: declare functions with `extern` and call them in `unsafe` expressions. See [Calling C](/learn/calling-c.html).

### Is there concurrency?

Not yet. Threads, tasks, and safe sharing are being designed to fit the ownership model.

### Which editors are supported?

VS Code (and Cursor), Neovim, and JetBrains IDEs have integrations, and any editor with LSP support can use `lowlight lsp --stdio`. See [Editor setup](/start/editors.html).

### Where do I report a problem or suggest a change?

In the Lowlight repository. See [Contributing](contributing.html).
