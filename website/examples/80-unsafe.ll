// Title: Raw pointers
// Category: Systems
// Order: 80

// Raw pointers and manual memory are available inside `unsafe`,
// which marks exactly the operations the compiler cannot check.
bind main = fn(): {
	let n = 10
	let p = unsafe addr_of(n)
	unsafe ptr_write(p, 11)
	println(n)

	// Allocate, use, and free raw memory.
	let buf = unsafe alloc_raw[i32](4)
	for(i in 0..4): unsafe ptr_write(unsafe ptr_add(buf, i), i32(i * i))
	let sum = 0
	for(i in 0..4): sum += int(unsafe ptr_read(unsafe ptr_add(buf, i)))
	unsafe free_raw(buf)
	println(sum)

	// Layout queries are safe.
	println(size_of[i32]())
	println(align_of[f64]())
}
