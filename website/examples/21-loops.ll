// Title: Loops
// Category: Control flow
// Order: 21

bind main = fn(): {
	// `for` visits each element of a collection or range.
	for(i in 0..3): print("$i ")
	println()

	// With two variables, the first is the index.
	for(i, s in ["a", "b"]): println("$i=$s")

	// `while` repeats while a condition holds.
	let n = 1
	while(n < 100): n *= 3
	println(n)

	// `next <-` skips to the next iteration; `for <-` stops.
	for(i in 0..10): {
		if(i % 2 == 1): next <-
		if(i > 6): for <-
		print("$i ")
	}
	println()
}
