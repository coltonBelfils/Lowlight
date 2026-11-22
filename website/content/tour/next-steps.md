---
title: Next steps
summary: Unsafe code, calling C, and where to go from here.
order: 12
---

# Next steps

## Below the safe language

Lowlight is a systems language, so the machine is never out of reach. `unsafe` marks the operations the compiler cannot check: raw pointers, manual allocation, and calls to C.

```lowlight
extern "stdio.h"
extern puts: fn(s: ptr[u8]) i32

bind main = fn(): {
	let msg = "Hello from C"
	let _ = unsafe puts(msg.c_str())
	let n = 42
	let p = unsafe addr_of(n)
	println(unsafe ptr_read(p))
	println(size_of[int]())
}
```

```output
Hello from C
42
8
```

`unsafe` is an expression prefix, so it covers exactly the operation that needs it, not a whole region of code. See [Unsafe code and C](/guides/unsafe-ffi.html).

## You've seen the whole language

That's all of Lowlight's core: values and functions, types, optionals and results, control flow and patterns, ownership, interfaces and generics, packages, and an escape hatch.

From here:

- **Build something.** The [tutorials](/learn/index.html) walk through complete programs: a command-line tool, a calculator, a config loader, a web service.
- **Read programs.** [Lowlight by Example](/examples/index.html) has short, annotated programs for each feature.
- **Go deeper.** The [guides](/guides/index.html) explain ownership, failure handling, generics, testing, and performance in depth.
- **Write it well.** [Effective Lowlight](/guides/effective-lowlight.html) is the style guide.
- **Know it exactly.** The [specification](/spec/index.html) defines every rule.
