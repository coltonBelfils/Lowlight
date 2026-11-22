// Title: Distinct types
// Category: Types
// Order: 15

// `define Name Base` creates a new type that shares its base's
// representation but never mixes with it.
define Meters float
define Seconds float

bind speed = (d: Meters, t: Seconds) float: float(d) / float(t)

// Distinct types can have their own methods.
bind Meters.to_km = (self) float: float(self) / 1000.0

bind main = (): {
	let d = Meters(1500.0)
	let t = Seconds(300.0)
	println(speed(d, t))
	println(d.to_km())
	// speed(t, d) does not compile: the arguments are swapped.
}
