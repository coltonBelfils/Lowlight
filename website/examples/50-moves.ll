// Title: Moves
// Category: Ownership
// Order: 50

// Plain values (numbers, bools, plain structs) copy. Resources
// (strings, lists, maps) have one owner and move.
bind keep = fn(items: own [string]) int: items.len()

bind main = fn(): {
	let a = 5
	let b = a          // a copy: both are usable
	println(a + b)

	let names = ["ada", "grace"]
	// Moving an existing variable is written `own`.
	let n = keep(own names)
	println(n)
	// println(names) would not compile: `names` was moved.

	// `.copy()` makes an independent duplicate.
	let s = "shared" + ""
	let t = s.copy()
	println(s == t)

	// A moved `let` variable can be given a new value.
	names = ["linus"]
	println(names)
}
