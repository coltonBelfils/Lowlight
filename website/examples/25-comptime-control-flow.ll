// Title: Compile-time control flow
// Category: Control flow
// Order: 25

// `comptime if` chooses one branch while compiling based on `const`
// values or type queries (`type.size_of[T]`). Untaken branches are pruned.
const word_kind[T] = fn() string:
	comptime if(type.size_of[T]() == 8): "64-bit"
	else: "${type.size_of[T]() * 8}-bit"

// `comptime match` checks every arm statically for exhaustiveness, then
// compiles only the matching arm.
const KIND = "fast"

const pick_factor = fn() int:
	comptime match(KIND):
		when "fast": 4
		when "safe": 1
		default: 2

// Collecting `comptime for` unrolls at compile time to initialize
// a fixed-size array directly from `0..5`.
const POWERS_OF_TWO = comptime for(i in 0..5) [5: int]: 1 << i

bind main = fn(): {
	println("${word_kind[i32]()} vs ${word_kind[int]()}")
	println("factor=${pick_factor()}")
	println(POWERS_OF_TWO)
}
