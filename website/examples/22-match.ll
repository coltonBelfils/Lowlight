// Title: Match
// Category: Control flow
// Order: 22

bind classify = (n: int) string:
	match(n):
		// Several patterns can share an arm.
		when 0: "zero"
		when 1, 2, 3: "small"
		// `in` matches anything a container contains.
		when in 4..10: "medium"
		// Every match ends with `default`.
		default: "large"

// `match:` with no subject picks the first true condition.
bind fizz = (n: int) string:
	match:
		when n % 15 == 0: "FizzBuzz"
		when n % 3 == 0: "Fizz"
		when n % 5 == 0: "Buzz"
		default: n.to_string()

bind main = (): {
	for(n in [0, 2, 7, 50]): println(classify(n))
	let line = for(i in 1..=15) [string]: fizz(i)
	println(line)
}
