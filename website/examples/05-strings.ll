// Title: Strings
// Category: Basics
// Order: 5

bind main = fn(): {
	let s = "Hello, Lowlight"

	// `len` counts bytes. Strings are UTF-8.
	println(s.len())

	// Searching and testing.
	println(s.contains("Low"))
	println(s.starts_with("Hello"))
	println(s.index_of("Low") ?? -1)

	// Transforming returns new strings.
	println(s.to_upper())
	println("  padded  ".trim())
	println(s.replace("Hello", "Goodbye"))

	// Slicing by byte offsets gives a substring.
	println(s[7..])

	// Splitting gives a list of strings.
	println("a,b,c".split(","))

	// A `let` string can grow in place.
	let b = ""
	b.push("abc")
	b.push_char('!')
	println(b)
}
