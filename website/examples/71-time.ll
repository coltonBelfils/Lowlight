// Title: Durations and dates
// Category: Standard library
// Order: 71

import "std/time"

bind main = fn(): {
	// Durations are exact nanosecond counts that format themselves.
	let d = time.Duration.from_secs(1.5)
	println(d)
	println(d.to_millis())

	// Dates are validated: `new` returns a result, `must_new` panics.
	// The last argument chooses local time instead of UTC.
	let launch = time.DateTime.must_new(2026, 1, 15, 9, 30, 0, false)
	println(launch.format("%A, %B %d, %Y at %H:%M"))
	println(launch.format_go(time.RFC3339))

	let bad! = time.DateTime.new(2026, 2, 30, 0, 0, 0, false)
	println(bad!.is_err())
}
