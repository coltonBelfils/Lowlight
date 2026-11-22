// Title: Optionals
// Category: Types
// Order: 16

// A value that may be absent has type `?T`, and the name that
// holds it ends in `?`. There is no null.
bind find = (xs: [string], target: string) ?int: {
	for(i, x in xs): if(x == target): func <- i
	none
}

bind main = (): {
	let names = ["ada", "grace", "linus"]

	// `??` supplies a fallback.
	println(find(names, "grace") ?? -1)
	println(find(names, "bob") ?? -1)

	// `if(x?)` checks and unwraps a variable.
	let pos? = find(names, "linus")
	if(pos?): println("linus at $pos")

	// `if(expr as name)` binds the value of an expression.
	if(find(names, "bob") as i): println(i)
	else: println("no bob")

	// `?.` reaches through an optional.
	let first? = names.get(0)
	println(first?.len() ?? 0)
}
