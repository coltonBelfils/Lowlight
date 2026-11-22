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

bind main = (): {
	println(size_of[Header]())
	println(size_of[PackedHeader]())
	println(is_plain[Header]())
	println(is_plain[string]())
}
