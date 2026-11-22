// Title: Functions
// Category: Basics
// Order: 7

// A function's parameters have types, and its return type
// follows the parameter list.
bind add = (a: int, b: int) int: a + b

// A body with several statements is a block; its last
// expression is the result.
bind hypot2 = (a: int, b: int) int: {
	let aa = a * a
	let bb = b * b
	aa + bb
}

// A function that returns nothing omits the return type.
bind greet = (name: string): println("hi $name")

// `func <-` returns early.
bind sign = (n: int) string: {
	if(n < 0): func <- "negative"
	if(n == 0): func <- "zero"
	"positive"
}

bind main = (): {
	println(add(2, 3))
	println(hypot2(3, 4))
	greet("Grace")
	println(sign(-5))
}
