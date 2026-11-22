// Title: If and else
// Category: Control flow
// Order: 20

bind main = fn(): {
	let n = 7

	// The condition is in parentheses; the body follows a colon.
	if(n > 5): println("big")

	// `else if` and `else` continue the chain.
	if(n % 2 == 0): println("even")
	else if(n % 3 == 0): println("multiple of three")
	else: println("odd")

	// `if` is an expression, so it can produce a value.
	let kind = if(n > 0): "positive" else: "not positive"
	println(kind)

	// Bodies can be blocks.
	if(n == 7): {
		println("lucky")
		println("seven")
	}
}
