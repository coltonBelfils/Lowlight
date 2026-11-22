---
title: Performance
summary: Release builds, backends, data layout, and what safety checks cost.
order: 23
group: Working with code
---

# Performance

Lowlight compiles to native code through LLVM, with no garbage collector, no runtime type information, and no hidden allocations. Generic code is specialized for each type it is used with, and plain values live inline. Well-written Lowlight performs like well-written C.

## Build modes

| | Debug (default) | Release (`--release`) |
|---|---|---|
| Optimization | none (`-O0`), with debug info | `-O2` |
| Bounds, division, and shift checks | on | **on** |
| Integer overflow | wraps | wraps |

Safety checks stay on in release builds. An out-of-range index or a division by zero always stops the program with a clear message instead of corrupting memory. The checks are cheap: a compare and a rarely-taken branch, which modern CPUs predict well, and LLVM removes many of them when it can prove they always pass.

```bash
lowlight build --release -o app main.ll
```

## Backends

- **LLVM** (default): the compiler emits LLVM IR directly, and Clang optimizes and links it.
- **C11** (`--backend c11`): the compiler emits one portable C file. Use it for platforms LLVM doesn't target, for inspecting generated code, or when only a C compiler is available.

Both backends produce identical program behavior. `lowlight emit-llvm` and `lowlight emit-c` write the generated code so you can inspect it.

## Where time goes

### Allocation

Strings, lists, maps, sets, closures, and interface values allocate on the heap. Plain values, arrays, tuples, and structs of plain fields don't. To reduce allocation:

- Reserve capacity when you know the size: `List[int].with_capacity(n)`, `string.with_capacity(n)`, `xs.reserve(n)`.
- Reuse buffers: `clear()` keeps the capacity of a list or string.
- Use arrays (`[N: T]`) for small fixed-size data.
- Use an [arena](/guides/memory.html) for many small, short-lived objects; allocation becomes a pointer bump and freeing is one operation.

### Copies

Plain values copy freely and cheaply. Resources only copy when you write `.copy()`, so every expensive copy is visible in the source. Read-only parameters of resource type are passed by reference, never copied.

### Indirection

Interface values call through a function table and box their value on the heap. In hot loops, prefer generics, which compile to direct, inlinable calls.

### Strings

Strings are UTF-8 bytes. `s.len()`, `s[i]`, and slicing are constant-time byte operations. `for(c in s)` decodes characters as it goes, and `s.chars()` builds a list of them; when you only need ASCII, index bytes directly with `s[i]`.

## Measuring

Build with `--release` and time it:

```bash
lowlight build --release -o bench bench.ll
time ./bench
```

The repository's `benchmarks/` directory has small benchmarks (Fibonacci, a prime sieve, Mandelbrot, matrix multiplication) with equivalent C programs for comparison.

## Memory usage

Every value's lifetime is known at compile time, so memory is returned as soon as it's no longer needed: there is no garbage waiting for a collector. Set `LOWLIGHT_CHECK_LEAKS=1` when running a program to verify that it frees everything it allocated by the time it exits.
