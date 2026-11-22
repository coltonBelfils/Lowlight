// std/core/list — Growable heap-allocated contiguous sequence ([`List`]).
//
// Provides amortized O(1) push/pop, bounds-checked lookup, deep copy, deterministic
// [`Drop`] cleanup, and forward [`Iterator`] traversal.


// Growable heap-allocated array of elements of type [`T`].
//
// Can be constructed via vector literal syntax (`[1, 2, 3]`) or [`List.new`].
// Implements [`Indexable`], [`Collectable`], [`Iterable`], [`Containable`],
// [`Copyable`], [`Printable`], and [`Drop`].
//
// # Example
// ```lowlight
// let xs = List[int].new()
// xs.push(10)
// xs.push(20)
// assert(xs.pop() == 20)
// ```
@lang("list")
define List[T] struct [
	// Raw pointer to the heap-allocated element storage (null when capacity is 0).
	data: ptr[T],
	// Number of initialized elements currently stored.
	len: int,
	// Allocated slot capacity in elements.
	cap: int,
]

// Constructs an empty [`List`] with zero allocated capacity.
bind List[T].new = () List[T]:
	List[T][
		data: unsafe null_ptr[T](),
		len: 0,
		cap: 0,
	]

// Constructs an empty [`List`] with space pre-allocated for at least `cap` elements.
//
// - `cap`: Number of element slots to allocate immediately when positive.
bind List[T].with_capacity = (cap: int) List[T]: {
	if(cap <= 0): func <- List[T].new()
	let p = unsafe alloc_raw[T](cap)
	List[T][
		data: p,
		len: 0,
		cap: cap,
	]
}

// Constructs a [`List`] of length `len` filled with copies of `val`.
//
// Uses `alloc_uninit` + `fill_bytes` (`memset`) for 1-byte Plain types (`bool`, `u8`, `i8`)
// and a capacity-check-free fill loop for all other types.
bind List[T].filled = (len: int, val: T) List[T]: {
	if(len <= 0): func <- List[T].new()
	let p = unsafe alloc_uninit[T](len)
	if(is_plain[T]() and size_of[T]() == 1): {
		unsafe {
			let b = ptr_read(ptr_cast[u8](addr_of(val)))
			fill_bytes(ptr_cast[u8](p), b, len)
		}
	} else: {
		let i = 0
		for(i < len): {
			unsafe ptr_write(ptr_add(p, i), val.copy())
			i += 1
		}
	}
	List[T][
		data: p,
		len: len,
		cap: len,
	]
}

// Returns the number of elements currently in the list.
bind List[T].len = (self) int: self.len

// Returns the number of element slots currently allocated on the heap.
bind List[T].cap = (self) int: self.cap

// Reports whether the list contains zero elements.
bind List[T].is_empty = (self) bool: self.len == 0

// Ensures the list has allocated capacity for at least `min_cap` elements without changing its length.
bind List[T].reserve = (&self, min_cap: int): {
	if(min_cap <= self.cap): func <-
	let ncap = if(self.cap > 0): self.cap else: 4
	for(ncap < min_cap): {
		if(ncap >= 4611686018427387904): ncap = min_cap else: ncap *= 2
	}
	let nd = unsafe {
		if(self.cap <= 0): alloc_uninit[T](ncap) else: realloc_raw(self.data, ncap)
	}
	if(self.cap <= 0 and self.len > 0): {
		unsafe copy_bytes(ptr_cast[u8](nd), ptr_cast[u8](self.data), self.len * size_of[T]())
	}
	self.data = nd
	self.cap = ncap
}

// Appends `item` to the end of the list, reallocating geometrically when full.
//
// # Ownership
// Takes ownership of `item`.
bind List[T].push = (&self, item: own T): {
	if(self.len + 1 > self.cap): self.reserve(self.len + 1)
	unsafe ptr_write(ptr_add(self.data, self.len), own item)
	self.len += 1
}

// Removes and returns the last element from the list, or `none` if the list is empty.
bind List[T].pop = (&self) ?T: {
	if(self.len <= 0): func <- none
	self.len -= 1
	unsafe {
		let val = ptr_read(ptr_add(self.data, self.len))
		val
	}
}

// Returns a copy of the element at `idx`, or `none` if out of bounds.
bind List[T].get = (self, idx: int) ?T: {
	if(idx < 0 or idx >= self.len): func <- none
	unsafe self.data[idx].copy()
}

// Replaces the element at `idx` with `val`, dropping the previous element.
//
// Returns `false` (dropping `val`) if `idx` is out of bounds.
bind List[T].set = (&self, idx: int, val: own T) bool: {
	if(idx < 0 or idx >= self.len): func <- false
	unsafe {
		let _ = ptr_read(ptr_add(self.data, idx))
		ptr_write(ptr_add(self.data, idx), own val)
	}
	true
}

// Inserts `item` at position `idx`, shifting elements at `idx..len` one slot right.
//
// Panics if `idx < 0` or `idx > self.len`.
bind List[T].insert = (&self, idx: int, item: own T): {
	if(idx < 0 or idx > self.len):
		panic("List.insert: index $idx out of bounds for length ${self.len}")
	if(self.len + 1 > self.cap): self.reserve(self.len + 1)
	let tail = self.len - idx
	if(tail > 0): {
		unsafe copy_bytes(
			ptr_cast[u8](ptr_add(self.data, idx + 1)),
			ptr_cast[u8](ptr_add(self.data, idx)),
			tail * size_of[T](),
		)
	}
	unsafe ptr_write(ptr_add(self.data, idx), own item)
	self.len += 1
}

// Removes and returns the element at `idx`, shifting elements after `idx` one slot left.
//
// Panics if `idx < 0` or `idx >= self.len`.
bind List[T].remove = (&self, idx: int) T: {
	if(idx < 0 or idx >= self.len):
		panic("List.remove: index $idx out of bounds for length ${self.len}")
	unsafe {
		let out = ptr_read(ptr_add(self.data, idx))
		let tail = self.len - idx - 1
		if(tail > 0): {
			copy_bytes(
				ptr_cast[u8](ptr_add(self.data, idx)),
				ptr_cast[u8](ptr_add(self.data, idx + 1)),
				tail * size_of[T](),
			)
		}
		self.len -= 1
		out
	}
}

// Removes and returns the element at `idx` in O(1) time by moving the last element into its slot.
//
// Does not preserve element ordering. Panics if `idx < 0` or `idx >= self.len`.
bind List[T].swap_remove = (&self, idx: int) T: {
	if(idx < 0 or idx >= self.len):
		panic("List.swap_remove: index $idx out of bounds for length ${self.len}")
	unsafe {
		let out = ptr_read(ptr_add(self.data, idx))
		self.len -= 1
		if(idx < self.len): {
			let last = ptr_read(ptr_add(self.data, self.len))
			ptr_write(ptr_add(self.data, idx), own last)
		}
		out
	}
}

// Resizes the list in place so its length equals `new_len` (clamped to `0` if negative).
//
// When shrinking, trailing elements are dropped in reverse order. When growing, new slots
// are filled using `fill_bytes` (`memset`) for 1-byte Plain types (`bool`, `u8`, `i8`) or a
// capacity-check-free fill loop for other types.
bind List[T].resize = (&self, new_len: int, val: T): {
	let target = if(new_len < 0): 0 else: new_len
	if(target < self.len): {
		if(self.cap > 0 and not is_plain[T]()): {
			let i = self.len - 1
			for(i >= target): {
				unsafe {
					let _ = ptr_read(ptr_add(self.data, i))
				}
				i -= 1
			}
		}
		self.len = target
		func <-
	}
	if(target == self.len): func <-
	if(target > self.cap): self.reserve(target)
	let count = target - self.len
	if(is_plain[T]() and size_of[T]() == 1): {
		unsafe {
			let b = ptr_read(ptr_cast[u8](addr_of(val)))
			fill_bytes(ptr_cast[u8](ptr_add(self.data, self.len)), b, count)
		}
	} else: {
		let i = self.len
		for(i < target): {
			unsafe ptr_write(ptr_add(self.data, i), val.copy())
			i += 1
		}
	}
	self.len = target
}

// Reports whether any element in the list equals `item`.
bind List[T].contains = (self, item: T) bool: {
	let i = 0
	for(i < self.len): {
		unsafe {
			if(self.data[i] == item): func <- true
		}
		i += 1
	}
	false
}

// Returns a copy of the first element, or `none` if the list is empty.
bind List[T].first = (self) ?T: self.get(0)

// Returns a copy of the last element, or `none` if the list is empty.
bind List[T].last = (self) ?T: self.get(self.len - 1)

// Returns the index of the first element equal to `item`, or `none`.
bind List[T].index_of = (self, item: T) ?int: {
	let i = 0
	while(i < self.len): {
		unsafe {
			if(self.data[i] == item): func <- i
		}
		i += 1
	}
	none
}

// Exchanges the elements at indexes `i` and `j`.
//
// # Panics
// If either index is out of range.
bind List[T].swap = (&self, i: int, j: int): {
	if(i < 0 or i >= self.len): panic("List.swap: index $i out of bounds for length ${self.len}")
	if(j < 0 or j >= self.len): panic("List.swap: index $j out of bounds for length ${self.len}")
	if(i == j): func <-
	unsafe {
		let a = ptr_read(ptr_add(self.data, i))
		let b = ptr_read(ptr_add(self.data, j))
		ptr_write(ptr_add(self.data, i), own b)
		ptr_write(ptr_add(self.data, j), own a)
	}
}

// Shortens the list to `new_len` elements, destroying the rest in reverse order. Does nothing
// when `new_len` is at least the current length; a negative `new_len` empties the list.
// Capacity is kept.
bind List[T].truncate = (&self, new_len: int): {
	let keep = if(new_len < 0): 0 else: new_len
	if(keep >= self.len): func <-
	if(self.cap > 0): {
		let i = self.len - 1
		while(i >= keep): {
			unsafe {
				let _ = ptr_read(ptr_add(self.data, i))
			}
			i -= 1
		}
	}
	self.len = keep
}

// Moves every element of `other` onto the end of this list, in order, without copying the
// elements: `other` is consumed.
bind List[T].extend = (&self, other: own List[T]): {
	let src = own other
	if(src.len == 0): func <-
	self.reserve(self.len + src.len)
	unsafe copy_bytes(
		ptr_cast[u8](ptr_add(self.data, self.len)),
		ptr_cast[u8](src.data),
		src.len * size_of[T](),
	)
	self.len += src.len
	// The elements now belong to `self`; `src` frees its buffer without destroying them.
	src.len = 0
}

// Drops all elements in reverse order and resets length to `0` while keeping capacity allocated.
bind List[T].clear = (&self): {
	if(self.cap > 0): {
		let i = self.len - 1
		for(i >= 0): {
			unsafe {
				let _ = ptr_read(ptr_add(self.data, i))
			}
			i -= 1
		}
	}
	self.len = 0
}

// Returns a deep copy of the list by copying every element in order.
bind List[T].copy = (self) List[T]: {
	if(self.len <= 0): func <- List[T].new()
	let p = unsafe alloc_uninit[T](self.len)
	if(is_plain[T]()): {
		unsafe copy_bytes(ptr_cast[u8](p), ptr_cast[u8](self.data), self.len * size_of[T]())
	} else: {
		let i = 0
		for(i < self.len): {
			unsafe ptr_write(ptr_add(p, i), self.data[i].copy())
			i += 1
		}
	}
	List[T][
		data: p,
		len: self.len,
		cap: self.len,
	]
}

// Formats the list as a bracketed, comma-separated string (`"[a, b, ...]"`).
bind List[T].to_string = (self) string: {
	let out = "["
	let i = 0
	for(i < self.len): {
		if(i > 0): out.push(", ")
		unsafe out.push(self.data[i].to_string())
		i += 1
	}
	out.push("]")
	out
}

// Stateful forward iterator yielding elements from an owned snapshot of a [`List`].
//
// Owning a snapshot guards iteration against mutations (`push`, `pop`, `set`, `insert`,
// `remove`, `swap_remove`, `resize`, `clear`, reallocation, or `drop`) on the source list.
define ListIterator[T] struct [
	// Owned snapshot of elements captured when the iterator was created.
	items: List[T],
	// Next index to yield.
	idx: int,
]

// Advances the iterator and returns a copy of the next element, or `none` when finished.
bind ListIterator[T].next = (&self) ?T: {
	if(self.idx >= self.items.len): func <- none
	let i = self.idx
	self.idx += 1
	unsafe self.items.data[i].copy()
}

// Returns a forward [`Iterator`] over an owned snapshot of the list's elements.
bind List[T].iter = (self) Iterator[T]:
	ListIterator[T][
		items: self.copy(),
		idx: 0,
	]

// Drops all remaining elements in reverse order and frees the backing heap allocation.
bind List[T].drop = (&self): {
	if(self.cap > 0): {
		let i = self.len - 1
		for(i >= 0): {
			unsafe {
				let _ = ptr_read(ptr_add(self.data, i))
			}
			i -= 1
		}
		unsafe free_raw(self.data)
	}
	self.data = unsafe null_ptr[T]()
	self.len = 0
	self.cap = 0
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("List set and reserve (Plain and Resource)")
bind test_list_set_and_reserve = (): {
	let xs = List[int].new()
	xs.reserve(-5)
	assert(xs.cap() == 0 and xs.len() == 0, "reserve negative on empty")
	xs.reserve(0)
	assert(xs.cap() == 0 and xs.len() == 0, "reserve 0 on empty")
	xs.reserve(10)
	assert(xs.cap() >= 10 and xs.len() == 0, "reserve 10 grows cap")

	xs.push(1)
	xs.push(2)
	xs.push(3)
	let old_cap = xs.cap()
	xs.reserve(5)
	assert(xs.cap() == old_cap and xs.len() == 3, "smaller reserve is no-op")
	xs.reserve(50)
	assert(xs.cap() >= 50 and xs.len() == 3, "larger reserve grows cap")
	assert(xs.get(0) == 1 and xs.get(1) == 2 and xs.get(2) == 3, "reserve preserves elements")

	assert(xs.set(0, 100), "set index 0")
	assert(xs.set(2, 300), "set index 2")
	assert(not xs.set(-1, 999), "set negative returns false")
	assert(not xs.set(3, 999), "set len returns false")
	assert(xs.get(0) == 100 and xs.get(1) == 2 and xs.get(2) == 300, "set updated values")

	// Resource elements: verify replaced & rejected owned values do not leak
	let ss = List[string].new()
	ss.reserve(6)
	assert(ss.cap() >= 6, "List[string] reserve")
	ss.push("alpha")
	ss.push("beta")
	assert(ss.set(0, "ALPHA"), "List[string] set(0)")
	assert(ss.set(1, "BETA"), "List[string] set(1)")
	assert(not ss.set(-1, "OOB_NEG"), "List[string] set(-1) drops val cleanly")
	assert(not ss.set(2, "OOB_HI"), "List[string] set(2) drops val cleanly")
	assert(ss.get(0) == "ALPHA" and ss.get(1) == "BETA", "List[string] set values")
}

@test("List insert, remove, and swap_remove (Plain and Resource)")
bind test_list_insert_remove_swap_remove = (): {
	// Plain int list: insert at front, middle, end across reallocations
	let xs = List[int].new()
	xs.insert(0, 20)
	xs.insert(0, 10)
	xs.insert(2, 40)
	xs.insert(2, 30)
	xs.insert(0, 5)
	xs.insert(5, 50)
	assert(xs.len() == 6, "insert len == 6")
	assert(xs.to_string() == "[5, 10, 20, 30, 40, 50]", "insert ordering")

	assert(xs.remove(0) == 5, "remove front")
	assert(xs.remove(2) == 30, "remove middle")
	assert(xs.remove(3) == 50, "remove end")
	assert(xs.to_string() == "[10, 20, 40]", "remove remaining")
	assert(xs.swap_remove(0) == 10, "swap_remove front moves last")
	assert(xs.to_string() == "[40, 20]", "swap_remove state")
	assert(xs.swap_remove(1) == 20, "swap_remove last")
	assert(xs.swap_remove(0) == 40, "swap_remove single")
	assert(xs.is_empty(), "empty after removals")

	// Resource string list: verify 0 leaks and proper element ownership
	let words = List[string].new()
	words.insert(0, "mid")
	words.insert(0, "head")
	words.insert(2, "tail")
	words.insert(1, "two")
	words.insert(4, "end")
	assert(words.to_string() == "[head, two, mid, tail, end]", "string insert order")

	assert(words.remove(1) == "two", "string remove middle")
	assert(words.remove(0) == "head", "string remove front")
	assert(words.remove(2) == "end", "string remove end")
	assert(words.to_string() == "[mid, tail]", "string remove state")
	assert(words.swap_remove(0) == "mid", "string swap_remove front")
	assert(words.to_string() == "[tail]", "string swap_remove leaves tail")
	assert(words.swap_remove(0) == "tail", "string swap_remove single")
	assert(words.is_empty(), "string list empty")

	// Shorthand [int] parity with List[int] methods
	let sh: [int] = [1, 2, 3]
	sh.reserve(16)
	assert(sh.cap() >= 16, "[int] reserve")
	assert(sh.set(1, 20), "[int] set")
	sh.insert(1, 15)
	assert(sh.to_string() == "[1, 15, 20, 3]", "[int] insert")
	assert(sh.remove(1) == 15, "[int] remove")
	assert(sh.swap_remove(0) == 1, "[int] swap_remove")
	assert(sh.to_string() == "[3, 20]", "[int] after swap_remove")
	sh.resize(4, 99)
	assert(sh.to_string() == "[3, 20, 99, 99]", "[int] resize")
}

@test("List filled and resize (1-byte Plain, multi-byte Plain, and Resource)")
bind test_list_filled_and_resize = (): {
	// Empty/negative filled
	let b0 = List[bool].filled(0, true)
	assert(b0.len() == 0 and b0.cap() == 0 and b0.is_empty(), "filled(0)")
	let bneg = List[bool].filled(-3, true)
	assert(bneg.len() == 0 and bneg.cap() == 0 and bneg.is_empty(), "filled(-3)")

	// 1-byte Plain types (bool, u8, i8) -> alloc_uninit + fill_bytes
	let bt = List[bool].filled(17, true)
	assert(bt.len() == 17 and bt.cap() == 17, "List[bool].filled true len/cap")
	for(i in 0..17): assert(bt[i] == true, "List[bool].filled true elem")

	let bf = List[bool].filled(17, false)
	assert(bf.len() == 17 and bf.cap() == 17, "List[bool].filled false len/cap")
	for(i in 0..17): assert(bf[i] == false, "List[bool].filled false elem")

	let bu = List[u8].filled(64, u8(171))
	assert(bu.len() == 64 and bu.cap() == 64, "List[u8].filled len/cap")
	for(i in 0..64): assert(bu[i] == u8(171), "List[u8].filled elem")

	let bi = List[i8].filled(33, i8(-7))
	assert(bi.len() == 33 and bi.cap() == 33, "List[i8].filled len/cap")
	for(i in 0..33): assert(bi[i] == i8(-7), "List[i8].filled elem")

	// Multi-byte Plain types
	let ints = List[int].filled(12, 99)
	assert(ints.len() == 12 and ints.cap() == 12, "List[int].filled len/cap")
	for(i in 0..12): assert(ints[i] == 99, "List[int].filled elem")

	let floats = List[float].filled(5, 3.25)
	assert(floats.len() == 5 and floats.cap() == 5, "List[float].filled len/cap")
	for(i in 0..5): assert(floats[i] == 3.25, "List[float].filled elem")

	// Resource type: independent deep copies
	let strs = List[string].filled(4, "hello")
	assert(strs.len() == 4 and strs.cap() == 4, "List[string].filled len/cap")
	assert(strs.set(1, "world"), "mutate slot 1 only")
	assert(strs[0] == "hello" and strs[1] == "world" and strs[2] == "hello" and strs[3] == "hello", "independent copies")

	// resize: 1-byte Plain (bool, u8, i8)
	let rb = List[bool].new()
	rb.resize(10, true)
	rb.resize(15, false)
	assert(rb.len() == 15, "resize bool len")
	for(i in 0..10): assert(rb[i] == true, "resize bool first 10 true")
	for(i in 10..15): assert(rb[i] == false, "resize bool next 5 false")
	rb.resize(4, true)
	assert(rb.len() == 4 and rb[3] == true, "resize bool shrink")

	let ru = List[u8].new()
	ru.resize(8, u8(42))
	assert(ru.len() == 8 and ru[7] == u8(42), "resize u8 grow")

	let rii = List[i8].new()
	rii.resize(8, i8(-12))
	assert(rii.len() == 8 and rii[7] == i8(-12), "resize i8 grow")

	// resize: multi-byte Plain
	let ri = List[int].new()
	ri.resize(5, 7)
	ri.resize(5, 88) // same length -> no-op
	assert(ri.len() == 5 and ri[4] == 7, "resize same len is no-op")
	ri.resize(8, 9)
	assert(ri.to_string() == "[7, 7, 7, 7, 7, 9, 9, 9]", "resize grow int")
	ri.resize(3, 0)
	assert(ri.to_string() == "[7, 7, 7]", "resize shrink int")
	ri.resize(-10, 0)
	assert(ri.is_empty(), "resize negative clamps to 0")

	// resize: Resource type (grow, shrink drops trailing elements cleanly, re-grow)
	let rs = List[string].new()
	rs.resize(3, "item")
	assert(rs.set(0, "keep"), "set slot 0 before shrink")
	rs.resize(6, "tail")
	assert(rs.to_string() == "[keep, item, item, tail, tail, tail]", "resize grow string")
	rs.resize(1, "unused")
	assert(rs.to_string() == "[keep]", "resize shrink string drops tail cleanly")
	rs.resize(3, "again")
	assert(rs.to_string() == "[keep, again, again]", "resize re-grow string")
}

@test("ListIterator guarded against source mutations and drop")
bind test_list_iterator_guarded = (): {
	// Plain int iterator survives push/realloc, set, insert, remove, swap_remove, resize, clear
	let xs = List[int].new()
	xs.push(10)
	xs.push(20)
	xs.push(30)
	let it = xs.iter()
	assert(it.next() == 10, "first item before mutations")

	assert(xs.set(1, 999), "mutate set")
	xs.insert(0, 111)
	let _ = xs.remove(2)
	let _ = xs.swap_remove(0)
	for(k in 0..100): xs.push(k)
	xs.resize(2, -1)
	xs.clear()

	assert(it.next() == 20, "snapshot yields 20")
	assert(it.next() == 30, "snapshot yields 30")
	assert(it.next() == none, "snapshot ends cleanly")

	// Resource iterator survives source mutation and source drop with 0 leaks
	let it_outer = {
		let src = List[string].new()
		src.push("first")
		src.push("second")
		src.push("third")
		let i_res = src.iter()
		assert(i_res.next() == "first", "resource iter first")
		let _ = src.set(1, "mutated")
		src.insert(0, "prepended")
		for(k in 0..20): src.push("realloc_$k")
		src.clear()
		i_res
	}
	assert(it_outer.next() == "second", "resource iter survives src drop: second")
	assert(it_outer.next() == "third", "resource iter survives src drop: third")
	assert(it_outer.next() == none, "resource iter ends")

	// Early stop & empty iterator leak checks
	let partial = List[string].filled(5, "leak_free")
	let pit = partial.iter()
	assert(pit.next() == "leak_free", "partial iter step 1")

	let empty_s = List[string].new()
	let eit = empty_s.iter()
	assert(eit.next() == none, "empty iter none")
}

@test("List first, last, index_of, swap, truncate, and extend")
bind test_list_helpers = (): {
	let xs = [10, 20, 30, 20]
	assert(xs.first() == 10 and xs.last() == 20, "first and last")
	let empty: [int] = []
	assert(empty.first() == none and empty.last() == none, "empty list")
	assert(xs.index_of(20) == 1 and xs.index_of(99) == none, "index_of finds the first match")

	xs.swap(0, 2)
	assert(xs[0] == 30 and xs[2] == 10, "swap")
	xs.swap(1, 1)
	assert(xs[1] == 20, "swap with itself")

	let words = ["a" + "1", "b" + "2", "c" + "3"]
	words.swap(0, 2)
	assert(words[0] == "c3" and words[2] == "a1", "swap resources")
	words.truncate(1)
	assert(words.len() == 1 and words[0] == "c3", "truncate drops the tail")
	words.truncate(5)
	assert(words.len() == 1, "truncate longer is a no-op")

	words.extend(["d" + "4", "e" + "5"])
	assert(words.len() == 3 and words[1] == "d4" and words[2] == "e5", "extend moves elements in order")
	let more = ["f" + "6"]
	words.extend(own more)
	assert(words.last() == "f6", "extend from a variable")
	words.truncate(-1)
	assert(words.is_empty(), "negative truncate empties")
}
