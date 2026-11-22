// Title: Borrowing
// Category: Ownership
// Order: 51

// A parameter normally borrows read-only. `&T` borrows mutably,
// and the caller must write `&` too, so changes are visible.
bind total = fn(xs: [int]) int: {
	let t = 0
	for(x in xs): t += x
	t
}

bind double_all = fn(xs: &[int]): {
	for(&x in xs): x *= 2
}

bind main = fn(): {
	let nums = [1, 2, 3]
	println(total(nums))
	double_all(&nums)
	println(nums)

	// `&self` methods borrow their receiver automatically.
	nums.push(7)
	println(nums.len())
}
