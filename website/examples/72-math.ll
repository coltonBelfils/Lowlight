// Title: Math
// Category: Standard library
// Order: 72

import "std/math"

bind main = (): {
	println(math.sqrt(2.0))
	println(math.pow(2.0, 10.0))
	println(math.floor(-1.5))
	println(math.round(2.5))
	println(math.clamp_int(15, 0, 10))
	println(math.PI)

	// Integer helpers are built in.
	println(min(3, 9))
	println(max(3, 9))
	println(abs(-4))
}
