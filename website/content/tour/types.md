---
title: Your own types
summary: Structs, enums, tuples, and distinct types.
order: 3
---

# Your own types

All new types are declared with `define`.

## Structs

A struct groups named fields:

```lowlight
define User struct [
	name: string,
	age: int,
	role: string = "viewer",
]

bind main = (): {
	let u = User[name: "Ada", age: 36]
	println("${u.name} (${u.age}) is a ${u.role}")
	let admin = User[name: "Grace", age: 45, role: "admin"]
	println(admin.role)
}
```

```output
Ada (36) is a viewer
admin
```

A struct literal is `Type[field: value, ...]`, naming each field. Fields with a default may be left out. Struct fields are read with `.`, and assigned with `.` too when the struct is in a `let` variable.

## Deriving methods

Structs and enums start with no `==`, `<`, hashing, `.copy()` (unless every field is plain), or printing. Either write the method, or ask for it with `@derive`, which writes it for you as ordinary Lowlight code:

```lowlight
@derive(Equatable, Hashable, Printable)
define Point struct [
	x: int,
	y: int,
]

bind main = (): {
	let p = Point[x: 1, y: 2]
	println(p == Point[x: 1, y: 2])
	println(#[p].has(Point[x: 1, y: 2]))
	println(p)
}
```

```output
true
true
Point[x: 1, y: 2]
```

The derivable interfaces are `Equatable` (`eq`, field by field), `Comparable` (`cmp`, by field order; enums by variant order, then payload), `Hashable` (`hash`), `Copyable` (`copy`), and `Printable` (`to_string`). Every field must support the interface. On a generic type, a derived method exists only for instances whose fields support it: `Box[int]` gets `eq`, `Box[File]` doesn't.

## Enums

An enum lists alternatives. Variants are lowercase. A **tag enum** has no data. `@derive(Equatable)` gives it `==` (see [Deriving methods](#deriving-methods)):

```lowlight
@derive(Equatable)
define Direction enum [
	north,
	east,
	south,
	west,
]

bind turn_right = (d: Direction) Direction:
	match(d):
		when north: Direction.east
		when east: Direction.south
		when south: Direction.west
		default west: Direction.north

bind main = (): println(turn_right(Direction.west) == Direction.north)
```

```output
true
```

Variants can also **carry a value**, making the enum a tagged union. Each variant carries at most one value; use a struct or tuple for more:

```lowlight
define Shape enum [
	circle: float,
	square: float,
	rect: +[float, float],
	point,
]

bind area = (s: Shape) float:
	match(s):
		when circle(r): 3.14159 * r * r
		when square(side): side * side
		when rect(wh): wh.0 * wh.1
		default point: 0.0

bind main = (): {
	let shapes = [Shape.circle(1.0), Shape.rect(+[2.0, 3.0]), Shape.point]
	for(s in shapes): println(area(s))
}
```

```output
3.14159
6
0
```

You build a variant with the type name, `Shape.circle(1.0)`, and take it apart with `match`, where variants are written bare. More on that in [Pattern matching](matching.html).

## Tuples

A tuple groups a fixed number of values without naming them. Its literal starts with `+[`:

```lowlight
bind min_max = (xs: [int]) +[int, int]: {
	let lo = xs[0]
	let hi = xs[0]
	for(x in xs): {
		lo = min(lo, x)
		hi = max(hi, x)
	}
	+[lo, hi]
}

bind main = (): {
	let pair = min_max([4, 1, 9, 3])
	println(pair.1)
	let lo, hi = unpack pair
	println("$lo..$hi")
}
```

```output
9
1..9
```

## Distinct types

`define Name Base` makes a new type with the same representation as `Base` that does not mix with it:

```lowlight
define UserId int
define OrderId int

bind lookup = (id: UserId) string: "user ${int(id)}"

bind main = (): {
	let u = UserId(7)
	println(lookup(u))
	// lookup(OrderId(7)) would not compile: an OrderId is not a UserId
}
```

```output
user 7
```

## Plain values and resources

Every type is either **plain** or a **resource**, and the difference matters throughout Lowlight:

- **Plain** values (numbers, `bool`, `char`, tag enums, and structs made only of plain fields) are copied whenever they are assigned or passed. Each copy is independent.
- **Resources** (strings, lists, maps, sets, and anything containing them) own memory. They have one owner at a time, they **move** instead of copying, and they are freed automatically when their owner goes out of scope.

The `User` above is a resource because it contains strings; `+[int, int]` is plain. [Ownership](ownership.html) explains what that means in practice.

## Next

[Optionals →](optionals.html)
