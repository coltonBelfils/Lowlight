// std/core/array — Fixed-size stack-allocated contiguous sequence (`[N: T]`) support.
//
// Provides indexing, containment checks, formatting, and value iteration via [`Array`]
// and [`ArrayIterator`].

import "./interfaces.ll"

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
bind Array[T].len = (self) int: self.len

// Reports whether the array has zero elements.
bind Array[T].is_empty = (self) bool: self.len == 0

// Returns a copy of the element at `idx`, or `none` if out of bounds.
//
// - `idx`: Zero-based element index.
bind Array[T].get = (self, idx: int) ?T: {
	if(idx < 0 or idx >= self.len): func <- none
	unsafe self.data[idx].copy()
}

// Reports whether any element in the array equals `item`.
//
// - `item`: Value to search for using `==`.
bind Array[T].contains = (self, item: T) bool: {
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
bind Array[T].to_string = (self) string: {
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
bind ArrayIterator[T].next = (&self) ?T: {
	if(self.idx >= self.len): func <- none
	let i = self.idx
	self.idx += 1
	unsafe self.data[i].copy()
}

// Returns a forward [`Iterator`] over copied elements of the array.
bind Array[T].iter = (self) Iterator[T]:
	ArrayIterator[T][
		data: self.data,
		len: self.len,
		idx: 0,
	]
