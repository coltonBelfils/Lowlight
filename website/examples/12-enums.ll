// Title: Enums
// Category: Types
// Order: 12

// A tag enum lists named alternatives. Variants are lowercase.
// `@derive(Equatable)` gives it `==`.
@derive(Equatable)
define Level enum [
	low,
	medium,
	high,
]

// Variants may have explicit integer values and a storage type.
define HttpStatus enum i32 [
	ok = 200,
	not_found = 404,
]

bind describe = (l: Level) string:
	match(l):
		when low: "fine"
		when medium: "watch it"
		default high: "act now"

bind main = (): {
	let l = Level.medium
	println(describe(l))
	println(l == Level.medium)
	println(int(HttpStatus.not_found))
}
