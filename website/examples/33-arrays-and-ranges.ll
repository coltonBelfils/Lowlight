// Title: Arrays and ranges
// Category: Collections
// Order: 33

bind main = fn(): {
	// `[N: T]` is a fixed-size array stored inline.
	let zeros = [4: 0]
	let primes = [4: 2, 3, 5, 7]
	zeros[1] = 9
	println(zeros)
	println(primes.len())

	// Ranges are values: half-open `a..b` and inclusive `a..=b`.
	let r = 1..5
	println(r.len())
	println(r.contains(5))
	println((1..=5).contains(5))

	// Character ranges work too.
	for(c in 'a'..='e'): print(c)
	println()
}
