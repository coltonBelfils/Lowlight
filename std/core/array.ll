// std/core/array — Fixed-size stack-allocated contiguous sequence (`[N: T]`) support.
//
// Provides indexing, containment checks, formatting, and value iteration via [`Array`]
// and [`ArrayIterator`].


// Fixed-size stack-allocated sequence of elements backed by a contiguous buffer.
//
// Usually written using array type syntax `[N: T]` and literal syntax `[4: 1, 2, 3, 4]`.
// Implements [`Printable`], [`Containable`], and [`Iterable`].
@lang("array")
define Array[T] struct [
	// Raw pointer to the contiguous element buffer.
	data: ptr[T],
	// Fixed number of elements in the array.
	len: int,
]

// Returns the fixed number of elements in the array.
const Array[T].len = fn(self) int: self.len

// Reports whether the array has zero elements.
const Array[T].is_empty = fn(self) bool: self.len == 0

// Returns a copy of the element at `idx`, or `none` if out of bounds.
//
// - `idx`: Zero-based element index.
const Array[T].get = fn(self, idx: int) ?T: {
	if(idx < 0 or idx >= self.len): func <- none
	unsafe self.data[idx].copy()
}

// Reports whether any element in the array equals `item`.
//
// - `item`: Value to search for using `==`.
const Array[T].contains = fn(self, item: T) bool: {
	let i = 0
	for(i < self.len): {
		unsafe {
			if(self.data[i] == item): func <- true
		}
		i += 1
	}
	false
}

// Formats the array as a bracketed, comma-separated string (`"[a, b, ...]"`).
const Array[T].to_string = fn(self) string: {
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

// Stateful forward iterator yielding copied elements from an [`Array`].
define ArrayIterator[T] struct [
	// Pointer to the backing array elements.
	data: ptr[T],
	// Total element count.
	len: int,
	// Next index to yield.
	idx: int,
]

// Advances the iterator and returns a copy of the next element, or `none` when exhausted.
const ArrayIterator[T].next = fn(&self) ?T: {
	if(self.idx >= self.len): func <- none
	let i = self.idx
	self.idx += 1
	unsafe self.data[i].copy()
}

// Returns a forward [`Iterator`] over copied elements of the array.
const Array[T].iter = fn(self) Iterator[T]:
	ArrayIterator[T][
		data: self.data,
		len: self.len,
		idx: 0,
	]

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test_only
bind _array_view_sum = fn(av: Array[int]) int: {
	assert(av.len() == 4, "Array[int] view len")
	assert(not av.is_empty(), "Array[int] view not is_empty")
	assert(av.get(0) == 10, "Array[int] view get(0)")
	assert(av.get(3) == 40, "Array[int] view get(3)")
	assert(av.get(-1) == none, "Array[int] view get(-1)")
	assert(av.get(4) == none, "Array[int] view get(4)")
	assert(av.contains(30), "Array[int] view contains hit")
	assert(not av.contains(99), "Array[int] view contains miss")
	assert(av.to_string() == "[10, 20, 30, 40]", "Array[int] view to_string")

	let sum = 0
	let it = av.iter()
	for(true): {
		let next? = it.next()
		if(next? as val): sum += val else: for <-
	}
	sum
}

@test("core Array[T] operations and views")
bind test_core_array = fn(): {
	let fixed: Array[4, int] = [4: 10, 20, 30, 40]
	assert(fixed.len() == 4, "fixed array len")
	assert(not fixed.is_empty(), "fixed array not is_empty")
	assert(fixed.get(1) == 20, "fixed array get(1)")
	assert(fixed.get(-1) == none, "fixed array get(-1)")
	assert(fixed.get(4) == none, "fixed array get(4)")
	assert(fixed.contains(40), "fixed array contains")
	assert(not fixed.contains(5), "fixed array contains miss")
	assert(fixed.to_string() == "[10, 20, 30, 40]", "fixed array to_string")
	assert(_array_view_sum(fixed) == 100, "Array[int] view sum via Iterator")

	let for_sum = 0
	for(x in fixed): for_sum += x
	assert(for_sum == 100, "for-in fixed array sum")

	let strs = [3: "alpha".copy(), "beta".copy(), "gamma".copy()]
	assert(strs.len() == 3, "string array len")
	assert(strs.get(1) == "beta", "string array get(1)")
	assert(strs.get(3) == none, "string array get(3)")
	assert(strs.contains("gamma"), "string array contains")
	assert(not strs.contains("delta"), "string array contains miss")
	assert(strs.to_string() == "[alpha, beta, gamma]", "string array to_string")

	let empty = Array[int][data: unsafe null_ptr[int](), len: 0]
	assert(empty.len() == 0, "empty Array len")
	assert(empty.is_empty(), "empty Array is_empty")
	assert(empty.get(0) == none, "empty Array get(0)")
	assert(not empty.contains(1), "empty Array contains")
	assert(empty.to_string() == "[]", "empty Array to_string")
	let eit = empty.iter()
	assert(eit.next() == none, "empty ArrayIterator next")
}
