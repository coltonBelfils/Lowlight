// Title: Equality, ordering, and printing
// Category: Abstraction
// Order: 62

define Version struct [
	major: int,
	minor: int,
]

// `to_string` makes a type printable and usable in interpolation.
bind Version.to_string = (self) string: "v${self.major}.${self.minor}"

// `cmp` gives `<`, `<=`, `>`, `>=`, returning <0, 0, or >0.
bind Version.cmp = (self, other: Version) int:
	if(self.major != other.major): self.major - other.major
	else: self.minor - other.minor

bind main = (): {
	let a = Version[major: 1, minor: 4]
	let b = Version[major: 1, minor: 10]
	println("$a < $b: ${a < b}")
	// Plain structs get `==` automatically.
	println(a == Version[major: 1, minor: 4])
}
