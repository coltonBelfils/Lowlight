// Title: Loops that collect
// Category: Control flow
// Order: 24

bind main = fn(): {
	let words = ["apple", "fig", "banana", "kiwi"]

	// A `for` loop with a result type collects its body's values.
	let lengths = for(w in words) [int]: w.len()
	println(lengths)

	// `next <-` leaves an element out: a filter.
	let long = for(w in words) [string]: {
		if(w.len() < 5): next <-
		w.to_upper()
	}
	println(long)

	// Any collection type works as the result: here a map.
	let index = for(i, w in words) #[string: int]: MapEntry[key: w.copy(), value: i]
	println(index["kiwi"] ?? -1)
}
