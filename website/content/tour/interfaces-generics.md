---
title: Interfaces and generics
summary: Structural interfaces, generic functions and types, and bounds.
order: 10
---

# Interfaces and generics

## Interfaces

An interface lists methods. Any type that has those methods satisfies it: there is no `implements` declaration.

```lowlight
define Shape interface [
	area: (self) float,
	name: (self) string,
]

define Circle struct [
	r: float,
]

define Square struct [
	side: float,
]

bind Circle.area = (self) float: 3.0 * self.r * self.r
bind Circle.name = (self) string: "circle"
bind Square.area = (self) float: self.side * self.side
bind Square.name = (self) string: "square"

bind main = (): {
	let shapes: [Shape] = [Shape(Circle[r: 1.0]), Shape(Square[side: 2.0])]
	for(s in shapes): println("${s.name()}: ${s.area()}")
}
```

```output
circle: 3
square: 4
```

Converting a value to an interface type, `Shape(circle)`, boxes it so values of different types can share a list. Method calls on a box go through a table of function pointers.

## Built-in interfaces

The prelude defines interfaces that give meaning to operators and language features. Implement the method and the feature works for your type:

| Interface | Method | Enables |
|---|---|---|
| `Printable` | `to_string(self) string` | `println`, `"$x"` |
| `Equatable` | `eq(self, other: Self) bool` | `==`, `!=` |
| `Comparable` | `cmp(self, other: Self) int` | `<`, `<=`, `>`, `>=` |
| `Hashable` | `hash(self) u64` | set elements, map keys |
| `Drop` | `drop(&self)` | cleanup on destruction |
| `Iterable[T]` | `iter(self) Iterator[T]` | `for(x in v)` |

```lowlight
define Money struct [
	cents: int,
]

bind Money.to_string = (self) string: "\$${self.cents / 100}.${self.cents % 100}"

bind main = (): {
	let price = Money[cents: 1999]
	println("costs $price")
}
```

```output
costs $19.99
```

Plain structs and enums also get `==` automatically, comparing field by field.

## Generic functions

Type parameters go in square brackets after the name:

```lowlight
bind largest[T: Comparable + Copyable] = (xs: [T]) ?T: {
	if(xs.is_empty()): func <- none
	let best = xs[0].copy()
	for(x in xs): if(x > best): best = x.copy()
	best
}

bind main = (): {
	println(largest([3, 9, 2]) ?? 0)
	println(largest(["pear", "apple"]) ?? "")
}
```

```output
9
pear
```

`T: Comparable + Copyable` is a **bound**: it lets the body use `>` and `.copy()` on `T` values. The copy is needed because `T` might be a resource such as `string`, and the list only lends its elements. Type arguments are usually inferred.

## Generic types

```lowlight
define Stack[T] struct [
	items: [T],
]

bind Stack[T].new = () Stack[T]: Stack[T][items: []]
bind Stack[T].push = (&self, x: own T): self.items.push(own x)
bind Stack[T].pop = (&self) ?T: self.items.pop()

bind main = (): {
	let s = Stack[string].new()
	s.push("a")
	s.push("b")
	println(s.pop() ?? "empty")
}
```

```output
b
```

Generics are compiled by **monomorphization**: each set of type arguments gets its own specialized copy of the code, so generic code is as fast as hand-written code and needs no runtime support.

One restriction: a type argument is never an optional. Write `?T` in the signature where absence is meant, as `pop` does above.

## Next

[Packages and tests →](packages-tests.html)
