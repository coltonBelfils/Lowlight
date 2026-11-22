// Title: Structs
// Category: Types
// Order: 10

// A struct groups named fields. Fields may have defaults.
// `@derive` writes methods for it: here `eq`, so points compare with `==`.
@derive(Equatable)
define Point struct [
	x: int,
	y: int,
]

define Config struct [
	name: string,
	retries: int = 3,
	verbose: bool = false,
]

bind main = fn(): {
	// A struct literal names its fields.
	let p = Point[x: 1, y: 2]
	println(p.x + p.y)

	// Fields with defaults may be omitted.
	let c = Config[name: "job"]
	println("${c.name} ${c.retries} ${c.verbose}")

	// A `let` struct's fields can be assigned.
	let q = Point[x: 0, y: 0]
	q.x = 10
	println(q.x)

	// `==` calls the derived `eq`, which compares field by field.
	println(p == Point[x: 1, y: 2])
}
