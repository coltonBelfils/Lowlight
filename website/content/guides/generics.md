---
title: Interfaces and generics
summary: Structural interfaces, interface values, generic functions and types, bounds, and monomorphization.
order: 13
group: Core ideas
---

# Interfaces and generics

Lowlight has two ways to write code that works with many types:

- **Generics** (`[T: Bound]`) are resolved at compile time. Each use gets specialized code, with no runtime cost.
- **Interface values** (`Shape(x)`) are resolved at run time through a table of methods, so one list can hold values of different types.

Both are built on **interfaces**: named sets of methods.

## Interfaces are structural

```lowlight fragment
define Shape interface [
	area: fn(self) float,
	describe: fn(self) string,
]
```

A type satisfies `Shape` simply by having those methods, with the same receiver and parameter modes. There is no `implements` clause, so you can make a type from another package satisfy your interface by adding methods to it.

## Interface values

Converting a value to an interface type boxes it:

```lowlight
define Shape interface [
	area: fn(self) float,
]

define Circle struct [
	r: float,
]

define Rect struct [
	w: float,
	h: float,
]

bind Circle.area = fn(self) float: 3.0 * self.r * self.r
bind Rect.area = fn(self) float: self.w * self.h

bind total = fn(shapes: [Shape]) float: {
	let t = 0.0
	for(s in shapes): t += s.area()
	t
}

bind main = fn(): println(total([Shape(Circle[r: 1.0]), Shape(Rect[w: 2.0, h: 5.0])]))
```

```output
13
```

An interface value owns its boxed value and destroys it with the box. It is a resource, and `.copy()` copies the value inside.

Interfaces that mention `Self` other than as the receiver, such as `Equatable` (`eq: fn(self, other: Self) bool`), can't be interface values, because the method needs to know the concrete type. They are used only as bounds.

## Generic functions

```lowlight
bind count_if[T] = fn(xs: [T], pred: cl(T) bool) int: {
	let n = 0
	for(x in xs): if(pred(x)): n += 1
	n
}

bind main = fn(): {
	println(count_if([1, 5, 8, 2], fn(x: int) bool: x > 3))
	println(count_if(["a", "", "b"], fn(s: string) bool: s.is_empty()))
}
```

```output
2
1
```

Type arguments are inferred from the arguments when possible. When they aren't, write them: `size_of[int]()`, `List[string].new()`.

## Bounds

A bound says what a type parameter can do. Inside the function, only the bound's methods and operators are available:

| Bound | Allows |
|---|---|
| `T: Equatable` | `==`, `!=` |
| `T: Comparable` | `<`, `<=`, `>`, `>=`, `==` |
| `T: Hashable` | use as a map key or set element |
| `T: Printable` | `"$x"`, `println(x)` |
| `T: Copyable` | `x.copy()` |
| `T: MyInterface` | the interface's methods |

Combine bounds with `+`: `[K: Hashable + Printable, V]`.

```lowlight
bind histogram[T: Hashable + Copyable] = fn(xs: [T]) #[T: int]: {
	let counts: #[T: int] = #[]
	for(x in xs): {
		let n = counts[x] ?? 0
		counts[x.copy()] = n + 1
	}
	counts
}

bind main = fn(): {
	let h = histogram(["a", "b", "a"])
	println(h["a"] ?? 0)
}
```

```output
2
```

## Generic types

```lowlight
define Pair[A, B] struct [
	first: A,
	second: B,
]

bind Pair[A, B].first_ref = fn(self) A: self.first

define Tree[T] enum [
	leaf,
	node: [Tree[T]],
]

bind main = fn(): {
	let p = Pair[int, string][first: 1, second: "one"]
	println("${p.second} ${p.first_ref()}")
}
```

```output
one 1
```

Methods on a generic type repeat its parameters: `bind Pair[A, B].first_ref`. A method can add its own parameters too: `bind List[T].map[U] = fn(self, f: (T) U) [U]: ...`.

## Monomorphization

Generic code is compiled once per distinct set of type arguments. `count_if[int]` and `count_if[string]` are two separate functions in the executable, each as efficient as if written by hand. The cost is code size, and compile time for heavily generic code. Instantiations that would never end (a function calling itself with `List[T]`) stop at a nesting depth of 128 with an error.

Two rules follow from this design:

- **A type argument is never optional.** `Box[?int]` isn't allowed; write `?T` inside the generic declaration where absence is meant. This keeps optionals from nesting.
- **Generic code is checked once**, in its generic form, against its bounds. An error in a generic function is reported where it's written, not at each use.

## Choosing between generics and interface values

| Use generics when | Use interface values when |
|---|---|
| all values in a call have the same type | one collection holds values of different types |
| performance matters (calls are direct and inlinable) | the set of types is open-ended, like plugins |
| you need `Self`-based interfaces (`==`, `<`, `.copy()`) | you want to hide the concrete type |

## Built-in interfaces

The prelude defines the interfaces behind operators, loops, and printing. [The prelude chapter](/spec/the-prelude.html#121-core-interfaces) lists them all with the features they enable.
