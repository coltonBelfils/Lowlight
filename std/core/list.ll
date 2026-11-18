// std/core/list — Growable heap-allocated contiguous sequence ([`List`]).
//
// Provides amortized O(1) push/pop, bounds-checked lookup, deep copy, deterministic
// [`Drop`] cleanup, and forward [`Iterator`] traversal.

import "./interfaces.ll"

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

// Returns the number of elements currently in the list.
bind List[T].len = (self) int: self.len

// Returns the number of element slots currently allocated on the heap.
bind List[T].cap = (self) int: self.cap

// Reports whether the list contains zero elements.
bind List[T].is_empty = (self) bool: self.len == 0

// Appends `item` to the end of the list, reallocating geometrically when full.
//
// # Ownership
// Takes ownership of `item`.
bind List[T].push = (&self, item: own T): {
	if(self.len + 1 > self.cap): {
		let ncap = if(self.cap > 0): self.cap * 2 else: 4
		let nd = unsafe {
			if(self.cap <= 0): alloc_raw[T](ncap) else: realloc_raw(self.data, ncap)
		}
		self.data = nd
		self.cap = ncap
	}
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
	let cp = List[T].with_capacity(self.len)
	let i = 0
	for(i < self.len): {
		unsafe cp.push(self.data[i].copy())
		i += 1
	}
	cp
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

// Stateful forward iterator yielding copied elements from a [`List`].
define ListIterator[T] struct [
	// Pointer to the source list's contiguous buffer.
	data: ptr[T],
	// Element count captured when the iterator was created.
	len: int,
	// Next index to yield.
	idx: int,
]

// Advances the iterator and returns a copy of the next element, or `none` when finished.
bind ListIterator[T].next = (&self) ?T: {
	if(self.idx >= self.len): func <- none
	let i = self.idx
	self.idx += 1
	unsafe self.data[i].copy()
}

// Returns a forward [`Iterator`] over copied elements of the list.
bind List[T].iter = (self) Iterator[T]:
	ListIterator[T][
		data: self.data,
		len: self.len,
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
