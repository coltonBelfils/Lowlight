// Title: Data layout
// Category: Systems
// Order: 82

// Structs are laid out in field order with C alignment.
define Header struct [
	tag: u8,
	length: u32,
]

// `packed` removes padding, for binary formats.
define packed PackedHeader struct [
	tag: u8,
	length: u32,
]

bind main = fn(): {
	println(type.size_of[Header]())
	println(type.size_of[PackedHeader]())
	println(type.is_plain[Header]())
	println(type.is_plain[string]())
}
