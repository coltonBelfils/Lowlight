// Title: Values
// Category: Basics
// Order: 2

// Lowlight has integers, floats, booleans, characters, and strings.
bind main = (): {
	// Integers are 64-bit by default. Arithmetic works as expected,
	// and integer division truncates.
	println(1 + 1)
	println(7 / 2)
	println(7 % 2)

	// Floats are 64-bit. Numbers never convert implicitly,
	// so mixing kinds needs an explicit conversion.
	println(7.0 / 2.0)
	println(float(7) / 2.0)

	// Booleans use the words `and`, `or`, and `not`.
	println(true and not false)

	// Characters are Unicode scalars in single quotes; strings are
	// UTF-8 in double quotes. `+` joins strings.
	println('λ')
	println("low" + "light")
}
