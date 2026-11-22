// Title: Sets
// Category: Collections
// Order: 32

bind main = fn(): {
	// `#[T]` is a hash set.
	let seen = #[1, 2, 3]

	// `insert` reports whether the value was new.
	let added = seen.insert(4)
	let again = seen.insert(2)
	println("$added $again")
	println(seen.has(3))
	println(seen.len())

	seen.remove(1)
	println(seen)

	// Collect into a set to remove duplicates.
	let unique = for(c in "mississippi".chars()) #[char]: c
	println(unique.len())
}
