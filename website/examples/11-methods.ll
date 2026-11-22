// Title: Methods
// Category: Types
// Order: 11

define Rect struct [
	w: int,
	h: int,
]

// Methods are declared as `Type.name`. The receiver mode says what
// the method may do: `self` reads, `&self` modifies.
bind Rect.area = (self) int: self.w * self.h

bind Rect.scale = (&self, k: int): {
	self.w *= k
	self.h *= k
}

// Without a receiver, it is an associated function: a constructor.
bind Rect.square = (side: int) Rect: Rect[w: side, h: side]

// Methods can be added to any type, including built-in ones.
bind int.is_even = (self) bool: self % 2 == 0

bind main = (): {
	let r = Rect.square(3)
	println(r.area())
	r.scale(2)
	println(r.area())
	println(10.is_even())
}
