// Title: Tuples
// Category: Types
// Order: 14

// A tuple groups a fixed number of values of any types.
// Its literal starts with `+[`.
bind divmod = fn(a: int, b: int) +[int, int]: +[a / b, a % b]

bind main = fn(): {
	let qr = divmod(17, 5)
	println(qr.0)
	println(qr.1)

	// `unpack` splits a tuple into variables.
	let q, r = unpack divmod(20, 6)
	println("$q remainder $r")

	// Swapping without a temporary.
	let a = 1
	let b = 2
	a, b = unpack +[b, a]
	println("$a $b")
}
