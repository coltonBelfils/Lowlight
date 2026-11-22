---
title: Unsafe code and C
summary: Raw pointers, memory intrinsics, packed structs, foreign functions, and keeping unsafe code safe.
order: 22
group: Working with code
---

# Unsafe code and C

Lowlight's safety rules are checked by the compiler. A few operations can't be checked: following a raw pointer, managing memory by hand, and calling C. Those are marked `unsafe`, and they are the only places where memory errors are possible.

## `unsafe` is an expression prefix

`unsafe` applies to one expression or one block:

```lowlight
bind main = fn(): {
	let x = 41
	let p = unsafe addr_of(x)
	unsafe ptr_write(p, 42)
	println(x)
	let total = unsafe {
		let q = alloc_raw[int](3)
		for(i in 0..3): ptr_write(ptr_add(q, i), i * 10)
		let t = ptr_read(ptr_add(q, 1)) + ptr_read(ptr_add(q, 2))
		free_raw(q)
		t
	}
	println(total)
}
```

```output
42
30
```

Keeping `unsafe` tight makes it easy to find and review: search for `unsafe` and you have every place that needs extra care.

## Raw pointers

`ptr[T]` is a raw, nullable pointer: plain, copyable, never freed automatically. In `unsafe` code:

| Operation | Meaning |
|---|---|
| `null_ptr[T]()` · `is_null(p)` | a null pointer; a null check |
| `addr_of(place)` | the address of a variable or field |
| `ptr_read(p)` · `ptr_write(p, v)` · `*p` | load and store |
| `ptr_add(p, n)` · `p[i]` | element offset and indexing (in units of `T`) |
| `ptr_cast[U](p)` · `ptr_addr(p)` | reinterpret; the address as an integer |

## Memory intrinsics

| Intrinsic | Does |
|---|---|
| `alloc_raw[T](n)` · `alloc_uninit[T](n)` | allocate `n` elements, zeroed or uninitialized |
| `realloc_raw(p, n)` · `free_raw(p)` | resize and free |
| `copy_bytes(dst, src, n)` · `zero_bytes(p, n)` · `fill_bytes(p, b, n)` | bulk memory operations |
| `size_of[T]()` · `align_of[T]()` · `is_plain[T]()` | layout queries (safe) |

Memory from `alloc_raw` holds raw bytes: destroying it doesn't run any destructors. Store only plain values in raw memory, or destroy resources yourself before freeing.

## Data layout

Structs are laid out in field order with C alignment rules, so a Lowlight struct of C-compatible fields matches the equivalent C struct. `define packed Name struct [...]` removes padding for binary formats and hardware registers.

## Calling C

```lowlight
extern "math.h"
extern cbrt: fn(x: f64) f64

bind main = fn(): println(unsafe cbrt(27.0))
```

```output
3
```

- `extern "header.h"` includes a C header; `extern name: fn(params) Ret` declares a function.
- `extern name as alias: ...` gives it a different Lowlight name.
- Parameter and return types should be plain types that match the C declaration exactly: `i32` for C `int`, `u64` for `size_t` on 64-bit systems, `ptr[u8]` for `char *`, `f64` for `double`.
- Pass Lowlight strings as `s.c_str()`; the pointer is valid while the string is alive and unchanged.
- C variadic functions (`printf`) can't be declared; wrap them in a fixed-parameter C function.
- Link libraries with `c_libs` in the manifest's `[build]` or `[target.*]` tables.

## Writing safe wrappers

The goal is that code using your package never writes `unsafe`. Wrap each foreign or raw operation in a function with a safe signature, and make sure it is correct for every possible input:

```lowlight fragment
extern "unistd.h"
extern getpid as c_getpid: fn() i32

// Returns the operating system's id for this process.
bind process_id = fn() int: int(unsafe c_getpid())
```

Checklist for a wrapper:

- Every pointer passed to C is valid for as long as C uses it.
- Every allocation is freed exactly once, on every path, including early returns.
- Sizes passed to C match the buffers.
- Failures C reports through return codes or `errno` become a `Result` or an optional.
- Resources that need cleanup (file descriptors, handles) live in a struct with a `drop` method, so cleanup happens automatically.

## Testing unsafe code

`lowlight test` checks every test for leaked heap allocations, and the compiler's own test suite builds with AddressSanitizer. Write tests that exercise every path through your wrappers, including error paths.

For the complete rules, see [chapter 10 of the specification](/spec/unsafe-code-and-foreign-functions.html).
