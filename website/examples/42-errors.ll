// Title: Errors and context
// Category: Failures
// Order: 42

// `Result[T]` has members `value: T` and `err: Error`. An Error
// carries a message, an optional code, and a chain of causes.
bind read_age = (s: string) Result[int]: {
	let n = parse_int(s) ?? func <- err(Error.coded("syntax", "'$s' is not a number"))
	if(n < 0): func <- err(Error.coded("range", "age $n is negative"))
	value(n)
}

// Passing a failure on adds context with `wrap`.
bind load_profile = (age_text: string) Result[string]: {
	let age = read_age(age_text):
		default err(own e): func <- err(e.wrap("loading profile"))
	value("age $age")
}

bind main = (): {
	println(load_profile("36") ?? "?")
	let r! = load_profile("-3")
	if(r!.peek_err() as e): {
		println(e.summary())
		println(e.has_code("range"))
		println(e.root().message())
	}
}
