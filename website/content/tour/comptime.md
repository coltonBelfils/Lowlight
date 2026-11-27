---
title: Compile-time evaluation
summary: const functions and lookup tables, comptime blocks and loops, const generics, reflection, and comp.TARGET.
order: 11
---

# Compile-time evaluation

Lowlight runs pure `const` code inside the compiler itself. Instead of macros or separate code-generation scripts, you write ordinary Lowlight using `const`, `comptime`, const generics, and type reflection.

## `const` values and functions

A `const` binding is evaluated at compile time, and a `const` function can be called both at compile time and at runtime:

```lowlight
const square = fn(x: int) int: x * x

@comptime_only
const _build_squares = fn() [6: int]: {
	let out = [6: 0]
	for(i in 0..6): out[i] = square(i)
	out
}

// Computed once while compiling and stored directly in read-only data.
const SQUARES = _build_squares()

bind main = fn(): {
	let runtime_n = 4
	println(SQUARES[3])
	println(square(runtime_n))
}
```

```output
9
16
```

`@comptime_only` marks helpers that exist strictly during compilation—they are never emitted into the final binary and may freely use compile-time `Map`s, `Set`s, and reflection queries.

## Inline `comptime` and unrolled loops

Write `comptime expr` (or `comptime { ... }`) to evaluate a pure computation at compile time inside runtime code. Use `comptime if`, `comptime match`, and `comptime for` to select branches and unroll loops statically:

```lowlight
const describe_size[T] = fn() string:
	comptime if(type.size_of[T]() <= 4): "small (${type.size_of[T]()} bytes)"
	else: "word (${type.size_of[T]()} bytes)"

bind main = fn(): {
	let greeting = comptime "hello, " + "world".to_upper()
	println(greeting)
	println(describe_size[i32]())
	println(describe_size[int]())
	comptime for(i in 0..3): println("unrolled $i")
}
```

```output
hello, WORLD
small (4 bytes)
word (8 bytes)
unrolled 0
unrolled 1
unrolled 2
```

## Const generic parameters

Types and functions can parameterize over compile-time values (`[N: const int]`, booleans, chars, tag enums, or strings). `N` is inferred automatically from fixed-size array arguments:

```lowlight
const dot[N: const int] = fn(a: [N: int], b: [N: int]) int: {
	let acc = 0
	for(i in 0..N): acc += a[i] * b[i]
	acc
}

bind main = fn(): {
	let v1 = [3: 1, 2, 3]
	let v2 = [1 + 2: 4, 5, 6]
	println(dot(v1, v2))
}
```

```output
32
```

## Static reflection

Inside a generic or `const` function, `comp.fields_of[T]()`, `comp.variants_of[T]()`, `comp.elems_of[T]()`, `comp.kind_of[T]()`, `comp.type_name[T]()`, and `comp.attrs_of[T]()` inspect types statically without runtime type information:

```lowlight
define Point struct [
	x: int,
	y: int,
]

const show_fields[T] = fn(v: T) string: {
	const name = comp.type_name[T]()
	let out = "$name:"
	comptime for(f in comp.fields_of[T]()):
		out.push(" ${f.name}=${v.field(f)}")
	out
}

bind main = fn(): println(show_fields(Point[x: 3, y: 7]))
```

```output
Point: x=3 y=7
```

You can also inspect the active compilation target via `comp.TARGET` inside `comptime if(comp.TARGET.ptr_bits == 64): ...` or embed package-local assets with `comp.embed_str("path")`.

## Next

[Packages and tests →](packages-tests.html)
