// Title: Constants
// Category: Basics
// Order: 4

// `const` declares compile-time constants and pure functions
// evaluated while compiling. By convention, top-level constants
// use `SCREAMING_SNAKE_CASE`.
const MAX_USERS = 100
const GREETING = "welcome"
const RATE = 1.5

// `const` functions run both at compile time and at runtime.
const double = fn(n: int) int: n * 2

const DOUBLE_MAX = double(MAX_USERS)

// `bind` declares a runtime immutable binding when a value
// is fixed once the program runs.
bind RUNTIME_LABEL = "$GREETING at rate $RATE"

bind main = fn(): {
	println(MAX_USERS)
	println(DOUBLE_MAX)
	println(RUNTIME_LABEL)
}
