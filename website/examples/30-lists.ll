// Title: Lists
// Category: Collections
// Order: 30

bind main = fn(): {
	// `[T]` is a growable list.
	let xs = [10, 20, 30]
	xs.push(40)
	println(xs)
	println(xs.len())

	// Indexing panics when out of range; `get` returns an optional.
	println(xs[1])
	println(xs.get(9) ?? 0)

	// Insert, remove, and pop.
	xs.insert(0, 5)
	let removed = xs.remove(2)
	let last? = xs.pop()
	println("$removed ${last? ?? 0} $xs")

	// Slices are views of a range of elements.
	println(xs[1..])

	// Typed empty lists and capacity.
	let names: [string] = []
	names.push("ada")
	println(names.is_empty())
}
