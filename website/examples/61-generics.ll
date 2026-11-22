// Title: Generics
// Category: Abstraction
// Order: 61

// Type parameters go in brackets. A bound says what the type
// must support; here, `<` and `.copy()`.
bind smallest[T: Comparable + Copyable] = (xs: [T]) ?T: {
	if(xs.is_empty()): func <- none
	let best = xs[0].copy()
	for(x in xs): if(x < best): best = x.copy()
	best
}

// Generic types work the same way.
define Box[T] struct [
	item: T,
	label: string,
]

bind Box[T].describe = (self) string: "box: ${self.label}"

bind main = (): {
	println(smallest([5, 2, 8]) ?? 0)
	println(smallest(["pear", "fig", "plum"]) ?? "")
	let b = Box[int][item: 7, label: "lucky"]
	println(b.describe())
	println(b.item)
}
