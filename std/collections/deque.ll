// std/collections — Collections beyond the prelude's `[T]`, `#[T]`, and `#[K: V]`: [`Deque`],
// [`Queue`], [`BitSet`], [`PriorityQueue`], [`StringInterner`], and [`BTreeMap`].

// A double-ended queue: a growable ring buffer with O(1) pushes and pops at both ends.
//
// # Example
// ```lowlight
// let d = collections.Deque[int].new()
// d.push_back(2)
// d.push_front(1)
// assert(d.pop_back() == 2 and d.pop_front() == 1)
// ```
define Deque[T] struct [
	_data: ptr[T],
	_cap: int,
	// Slot of the front element.
	_head: int,
	_len: int,
]

// Constructs an empty [`Deque`] without allocating.
bind Deque[T].new = fn() Deque[T]: Deque[T][_data: unsafe null_ptr[T](), _cap: 0, _head: 0, _len: 0]

// Constructs an empty [`Deque`] with room for `cap` elements before it grows.
bind Deque[T].with_capacity = fn(cap: int) Deque[T]: {
	if(cap <= 0): func <- Deque[T].new()
	Deque[T][_data: unsafe alloc_raw[T](cap), _cap: cap, _head: 0, _len: 0]
}

// Returns the number of elements.
bind Deque[T].len = fn(self) int: self._len

// Reports whether the deque has no elements.
bind Deque[T].is_empty = fn(self) bool: self._len == 0

// The slot holding the element at position `i` (front is `0`).
bind Deque[T]._slot = fn(self, i: int) int: (self._head + i) % self._cap

// Doubles the capacity (at least 8), moving the elements to the start of the new buffer.
bind Deque[T]._grow = fn(&self): {
	let ncap = if(self._cap < 4): 8 else: self._cap * 2
	unsafe {
		let nd = alloc_raw[T](ncap)
		for(i in 0..self._len): ptr_write(
			ptr_add(nd, i),
			ptr_read(ptr_add(self._data, self._slot(i))),
		)
		if(self._cap > 0): free_raw(self._data)
		self._data = nd
	}
	self._cap = ncap
	self._head = 0
}

// Adds `item` at the back.
bind Deque[T].push_back = fn(&self, item: own T): {
	if(self._len == self._cap): self._grow()
	unsafe ptr_write(ptr_add(self._data, self._slot(self._len)), own item)
	self._len += 1
}

// Adds `item` at the front.
bind Deque[T].push_front = fn(&self, item: own T): {
	if(self._len == self._cap): self._grow()
	self._head = if(self._head == 0): self._cap - 1 else: self._head - 1
	unsafe ptr_write(ptr_add(self._data, self._head), own item)
	self._len += 1
}

// Removes and returns the front element, or `none` if the deque is empty.
bind Deque[T].pop_front = fn(&self) ?T: {
	if(self._len == 0): func <- none
	let out = unsafe ptr_read(ptr_add(self._data, self._head))
	self._head = (self._head + 1) % self._cap
	self._len -= 1
	out
}

// Removes and returns the back element, or `none` if the deque is empty.
bind Deque[T].pop_back = fn(&self) ?T: {
	if(self._len == 0): func <- none
	self._len -= 1
	unsafe ptr_read(ptr_add(self._data, self._slot(self._len)))
}

// Returns a copy of the element at position `i` (the front is `0`), or `none` if `i` is out of
// range.
bind Deque[T].get = fn(self, i: int) ?T:
	if(i < 0 or i >= self._len): none else: unsafe self._data[self._slot(i)].copy()

// Returns a copy of the front element, or `none` if the deque is empty.
bind Deque[T].front = fn(self) ?T: self.get(0)

// Returns a copy of the back element, or `none` if the deque is empty.
bind Deque[T].back = fn(self) ?T: self.get(self._len - 1)

// Drops every element, keeping the allocated capacity.
bind Deque[T].clear = fn(&self): {
	for(i in 0..self._len):
		unsafe {
			let _ = ptr_read(ptr_add(self._data, self._slot(i)))
		}
	self._head = 0
	self._len = 0
}

// Returns copies of the elements, front to back.
bind Deque[T].to_list = fn(self) [T]: {
	let out = List[T].with_capacity(self._len)
	for(i in 0..self._len): out.push(unsafe self._data[self._slot(i)].copy())
	out
}

// Returns an iterator over copies of the elements, front to back.
bind Deque[T].iter = fn(self) Iterator[T]: self.to_list().iter()

// Returns a deep copy of the deque.
bind Deque[T].copy = fn(self) Deque[T]: {
	let out = Deque[T].with_capacity(self._len)
	for(i in 0..self._len): out.push_back(unsafe self._data[self._slot(i)].copy())
	out
}

// Formats the deque as `Deque[a, b, ...]`, front to back.
bind Deque[T].to_string = fn(self) string: {
	let out = "Deque["
	for(i in 0..self._len): {
		if(i > 0): out.push(", ")
		out.push(unsafe self._data[self._slot(i)].to_string())
	}
	out + "]"
}

// Adds `item` at the back (satisfies [`Collectable`], for comprehensions).
bind Deque[T].push = fn(&self, item: own T): self.push_back(own item)

// Drops every element and frees the buffer.
bind Deque[T].drop = fn(&self): {
	self.clear()
	if(self._cap > 0): unsafe free_raw(self._data)
	self._cap = 0
}

// A first-in, first-out queue: [`Queue.push`] adds at the back, [`Queue.pop`] takes from the front.
// A [`Deque`] that only allows those operations.
//
// # Example
// ```lowlight
// let jobs = collections.Queue[string].new()
// jobs.push("build")
// jobs.push("test")
// assert(jobs.pop() == "build")
// ```
define Queue[T] struct [
	_items: Deque[T]
]

// Constructs an empty [`Queue`].
bind Queue[T].new = fn() Queue[T]: Queue[T][_items: Deque[T].new()]

// Returns the number of queued elements.
bind Queue[T].len = fn(self) int: self._items.len()

// Reports whether the queue is empty.
bind Queue[T].is_empty = fn(self) bool: self._items.is_empty()

// Adds `item` at the back of the queue (also satisfies [`Collectable`]).
bind Queue[T].push = fn(&self, item: own T): self._items.push_back(own item)

// Removes and returns the element at the front, or `none` if the queue is empty.
bind Queue[T].pop = fn(&self) ?T: self._items.pop_front()

// Returns a copy of the element at the front without removing it, or `none` if the queue is empty.
bind Queue[T].peek = fn(self) ?T: self._items.front()

// Drops every queued element.
bind Queue[T].clear = fn(&self): self._items.clear()

// Returns copies of the elements, front to back.
bind Queue[T].to_list = fn(self) [T]: self._items.to_list()

// Returns an iterator over copies of the elements, front to back.
bind Queue[T].iter = fn(self) Iterator[T]: self._items.iter()

// Returns a deep copy of the queue.
bind Queue[T].copy = fn(self) Queue[T]: Queue[T][_items: self._items.copy()]

// Formats the queue as `Queue[a, b, ...]`, front to back.
bind Queue[T].to_string = fn(self) string: "Queue" + self._items.to_string()[5..]

@test("Deque pushes and pops at both ends across growth")
bind test_deque = fn(): {
	let d = Deque[int].new()
	assert(d.pop_front() == none and d.pop_back() == none, "empty pops are none")
	for(i in 0..20):
		if(i % 2 == 0): d.push_back(i) else: d.push_front(i)
	assert(d.len() == 20, "len after mixed pushes")
	assert(d.front() == 19 and d.back() == 18, "front and back")
	assert(d.get(0) == 19 and d.get(19) == 18 and d.get(20) == none, "get by position")
	let order = for(x in d.to_list()) [int]: x
	assert(order.len() == 20 and order[9] == 1 and order[10] == 0, "iteration goes front to back")
	assert(d.pop_front() == 19 and d.pop_back() == 18, "pops")
	d.clear()
	assert(d.is_empty() and d.pop_front() == none, "clear")

	let words = Deque[string].new()
	words.push_back("b" + "")
	words.push_front("a" + "")
	words.push_back("c" + "")
	let copy = words.copy()
	assert(words.pop_front() == "a", "string pop_front")
	assert(
		words.to_string() == "Deque[b, c]" and copy.to_string() == "Deque[a, b, c]",
		"copies are independent",
	)
	let wrapped = Deque[string].with_capacity(2)
	for(i in 0..5): {
		wrapped.push_back("x$i")
		if(i % 2 == 1): {
			let _ = wrapped.pop_front()
		}
	}
	assert(wrapped.to_list().join(",") == "x2,x3,x4", "growth while wrapped keeps order")
	let built = for(i in 0..3) Deque[int]: i * i
	assert(built.to_string() == "Deque[0, 1, 4]", "comprehension into a Deque")
}

@test("Queue is first in, first out")
bind test_queue = fn(): {
	let q = Queue[string].new()
	q.push("build")
	q.push("test")
	q.push("ship")
	assert(q.peek() == "build" and q.len() == 3, "peek leaves the front in place")
	assert(q.pop() == "build" and q.pop() == "test", "pops in order")
	assert(q.to_string() == "Queue[ship]", "to_string")
	assert(q.pop() == "ship" and q.pop() == none and q.is_empty(), "drains")
}
