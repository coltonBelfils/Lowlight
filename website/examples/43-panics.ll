// Title: Panics
// Category: Failures
// Order: 43

// A panic is for bugs, not expected failures. It prints a message
// with the location to standard error and exits with status 1.
// Panics cannot be caught. (expect-panic: this program panics.)
bind average = fn(xs: [int]) int: {
	assert(not xs.is_empty(), "average of an empty list")
	let total = 0
	for(x in xs): total += x
	total / xs.len()
}

bind main = fn(): {
	println(average([2, 4, 9]))
	// Output written before a panic is flushed, then the program stops:
	// lowlight panic at 43-panics.ll:9:2: average of an empty list
	println(average([]))
	println("never printed")
}
