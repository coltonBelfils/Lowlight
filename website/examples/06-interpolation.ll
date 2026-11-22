// Title: String interpolation
// Category: Basics
// Order: 6

import "std/strfmt"

bind main = (): {
	let name = "Ada"
	let items = 3

	// `$name` inserts a variable; `${expr}` inserts any expression.
	println("$name has $items items")
	println("next year: ${2026 + 1}")

	// Write `\$` for a literal dollar sign.
	println("costs \$5")

	// `std/strfmt` adds number and padding helpers.
	println(1234567.format_int_commas())
	println("[${"left".pad_right(8)}]")
	println("[${"right".pad_left(8)}]")

	// Backtick strings span lines and need no quote escaping.
	let poem = `roses are "red",
violets are blue`
	println(poem)
}
