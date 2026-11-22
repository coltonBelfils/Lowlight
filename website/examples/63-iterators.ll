// Title: Iterators
// Category: Abstraction
// Order: 63

// An iterator has a `next` method returning the next value, or
// `none` when it is finished.
define Fib struct [
	a: int,
	b: int,
	left: int,
]

bind Fib.next = (&self) ?int: {
	if(self.left == 0): func <- none
	self.left -= 1
	let out = self.a
	self.a, self.b = unpack +[self.b, self.a + self.b]
	out
}

bind main = (): {
	let it = Fib[a: 0, b: 1, left: 10]
	// Pull values until `next` returns none.
	while(true): {
		let n = it.next() ?? while <-
		print("$n ")
	}
	println()
}
