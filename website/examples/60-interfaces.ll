// Title: Interfaces
// Category: Abstraction
// Order: 60

// An interface lists methods. Any type with those methods
// satisfies it; there is no `implements`.
define Animal interface [
	name: (self) string,
	sound: (self) string,
]

define Dog struct [
	name_: string,
]

define Cat struct []

bind Dog.name = (self) string: self.name_.copy()
bind Dog.sound = (self) string: "woof"
bind Cat.name = (self) string: "cat"
bind Cat.sound = (self) string: "meow"

bind main = (): {
	// Converting to the interface type boxes each value, so
	// different types can share a list.
	let zoo: [Animal] = [Animal(Dog[name_: "Rex"]), Animal(Cat[])]
	for(a in zoo): println("${a.name()} says ${a.sound()}")
}
