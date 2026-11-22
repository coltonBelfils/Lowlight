// Title: Variables
// Category: Basics
// Order: 3

bind main = fn(): {
	// `bind` introduces a name that never changes.
	bind answer = 42
	println(answer)

	// `let` introduces a variable that can be reassigned.
	let count = 1
	count += 2
	count = count * 10
	println(count)

	// Types are inferred, but can be written after a colon.
	let ratio: float = 0.5
	let small: u8 = 200
	println(ratio)
	println(small)

	// Several values can be unpacked from a tuple at once.
	let x, y = unpack +[3, 4]
	println(x * y)
}
