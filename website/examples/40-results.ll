// Title: Results
// Category: Failures
// Order: 40

// A result type lists a `value` member and the ways an
// operation can fail.
define Parsed result [
	value: int,
	empty,
	not_a_number: string,
]

bind parse = fn(s: string) Parsed: {
	if(s.is_empty()): func <- empty
	if(parse_int(s) as n): func <- value(n)
	not_a_number(s.copy())
}

bind main = fn(): {
	// Names holding results end in `!`.
	let a! = parse("42")
	let b! = parse("x")

	// `??` gives a fallback for any failure.
	println(a! ?? 0)
	println(b! ?? 0)

	// `match` sees every member.
	match(parse("")):
		when Parsed.value(n): println(n)
		when Parsed.empty: println("empty input")
		default Parsed.not_a_number(s): println("bad: $s")
}
