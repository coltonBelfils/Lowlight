---
title: "Tutorial: calling C"
nav: Calling C
summary: Declare C functions, pass strings and buffers across the boundary, and wrap unsafe calls in safe functions.
order: 5
group: Tutorials
---

# Tutorial: calling C

Lowlight calls C functions directly, with no wrapper generator and no runtime cost. In this tutorial you'll use a few functions from the C standard library and learn the pattern for using C safely: keep each `unsafe` operation small, and wrap it in a function that safe code can call.

## 1. Declare and call

Declare a C function with `extern`, giving its Lowlight signature. Name the header that declares it with `extern "header.h"`:

```lowlight
extern "stdlib.h"
extern abs as c_abs: fn(n: i32) i32

bind main = fn(): {
	let n = unsafe c_abs(-42)
	println(n)
}
```

```output
42
```

- `as c_abs` gives the function a Lowlight name, avoiding a clash with the built-in `abs`.
- Every call to a C function is `unsafe`, because the compiler cannot check what it does. `unsafe` prefixes the one expression that needs it.
- C's `int` is `i32` in Lowlight. Use the exact sized types that match the C declaration.

## 2. Strings

C strings are pointers to NUL-terminated bytes. `s.c_str()` gives one for a Lowlight string; it is valid as long as the string is alive and unchanged:

```lowlight
extern "string.h"
extern strlen: fn(s: ptr[u8]) u64

bind c_length = fn(s: string) int: int(unsafe strlen(s.c_str()))

bind main = fn(): {
	println(c_length("hello"))
	println(c_length(""))
}
```

```output
5
0
```

`c_length` is a **safe wrapper**: callers pass an ordinary string and get an `int`, and the only `unsafe` expression is inside. That's the pattern to follow. Keep `unsafe` at the edge of your program, in small functions whose safety you can check by reading them.

## 3. Buffers

When C writes into memory you provide, allocate it with the raw memory intrinsics and free it yourself:

```lowlight
extern "string.h"
extern strcpy: fn(dst: ptr[u8], src: ptr[u8]) ptr[u8]
extern strcat: fn(dst: ptr[u8], src: ptr[u8]) ptr[u8]
extern strlen: fn(s: ptr[u8]) u64

bind c_join = fn(a: string, b: string) string: {
	let cap = a.len() + b.len() + 1
	let buf = unsafe alloc_raw[u8](cap)
	let _ = unsafe strcpy(buf, a.c_str())
	let _ = unsafe strcat(buf, b.c_str())
	let n = int(unsafe strlen(buf))
	let out = ""
	for(i in 0..n): out.push_byte(unsafe ptr_read(unsafe ptr_add(buf, i)))
	unsafe free_raw(buf)
	out
}

bind main = fn(): println(c_join("hello, ", "world"))
```

```output
hello, world
```

- `alloc_raw[u8](n)` returns a `ptr[u8]` to `n` bytes; `free_raw` releases it. Nothing frees raw memory automatically: that is what makes it raw.
- `ptr_add(p, i)` advances a pointer by `i` elements, and `ptr_read` loads through it.
- The bytes are copied into `out` before `free_raw`, so the returned string owns its own memory.
- `let _ = ...` explicitly discards the pointer that `strcpy` and `strcat` return.

C **variadic** functions such as `printf` and `snprintf` cannot be declared with `extern`: their calling convention differs from fixed-parameter functions on several platforms. Wrap them in a small C function with fixed parameters, or use the Lowlight standard library (`std/strfmt` formats numbers).

## 4. Linking libraries

Functions outside the C standard library need their library linked. In a project, list it in the manifest:

```toml
[build]
c_libs = ["z"]

[target.linux]
c_libs = ["m"]
```

For a one-off build, pass flags through the C compiler with `--cc` and environment variables, or put the declarations in a package that your project imports.

## 5. What `unsafe` does and doesn't promise

Inside an `unsafe` expression, you take responsibility for:

- pointers being valid, aligned, and pointing to live memory of the right type;
- memory allocated with `alloc_raw` being freed exactly once;
- C functions being called according to their documentation.

Everything outside `unsafe` keeps Lowlight's guarantees. A wrapper like `c_length` is safe only if its `unsafe` part is correct for **every** input, which is why wrappers should be small enough to verify by reading.

Read [Unsafe code and C](/guides/unsafe-ffi.html) for pointers, packed structs, and the full list of intrinsics.
