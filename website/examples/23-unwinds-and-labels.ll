// Title: Unwinds and labels
// Category: Control flow
// Order: 23

// `<-` leaves a construct early. The word before it names the
// construct: `func`, `for`, `while`, `next`, or a label.
bind first_pair = (xs: [int], target: int) string: {
	let found = outer: while(i, a in xs) string: {
		for(b in xs[i + 1..]): if(a + b == target): outer <- "$a + $b"
	} else: "none"
	found
}

bind main = (): {
	println(first_pair([1, 4, 6, 9], 10))
	println(first_pair([1, 2], 10))

	// A bare `<-` leaves the innermost block with a value.
	let grade = {
		let score = 83
		if(score >= 90): <- "A"
		if(score >= 80): <- "B"
		"C"
	}
	println(grade)
}
