// Title: Closures
// Category: Basics
// Order: 8

// Functions are values. A function type lists parameter types
// and the result type.
bind apply = (f: (int) int, x: int) int: f(x)

// A function can return a function.
bind adder = (n: int) (int) int: (x: int) int: x + n

bind main = (): {
	// A function literal has the same shape as a declaration.
	let square = (x: int) int: x * x
	println(apply(square, 7))

	// Literals capture variables from their surroundings.
	let add5 = adder(5)
	println(add5(10))

	// Captured strings move into the closure.
	let tag = "[log] " + ""
	let log = (msg: string): println(tag + msg)
	log("started")
	log("done")
}
