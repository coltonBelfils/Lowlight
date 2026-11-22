// Title: Interfaces
// Category: Abstraction
// Order: 60

// An interface lists methods. Any type with those methods
// satisfies it; there is no `implements`.
define Animal interface [
	name: fn(self) string,
	sound: fn(self) string,
]

define Dog struct [
	name_: string,
]

define Cat struct []

bind Dog.name = fn(self) string: self.name_.copy()
bind Dog.sound = fn(self) string: "woof"
bind Cat.name = fn(self) string: "cat"
bind Cat.sound = fn(self) string: "meow"

bind main = fn(): {
	// Converting to the interface type boxes each value, so
	// different types can share a list.
	let zoo: [Animal] = [Animal(Dog[name_: "Rex"]), Animal(Cat[])]
	for(a in zoo): println("${a.name()} says ${a.sound()}")
}
