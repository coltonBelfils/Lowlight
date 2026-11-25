// A priority queue: [`PriorityQueue.pop`] always returns the element that comes first in the
// queue's order. A binary heap, so `push` and `pop` take O(log n).
//
// The order is a closure `order(a, b)` that is negative when `a` should come out before `b`,
// positive when after, and zero when either may. [`min_first`] and [`max_first`] build the usual
// orders for [`Comparable`] elements.
//
// # Example
// ```lowlight
// let tasks = collections.PriorityQueue[Task].new(cl(a: Task, b: Task) int: b.urgency - a.urgency)
// let small = collections.min_first[int]()
// small.push(5)
// small.push(1)
// assert(small.pop() == 1)
// ```
define PriorityQueue[T] struct [
	_heap: [T],
	_order: cl(T, T) int,
]

// Constructs an empty [`PriorityQueue`] that pops elements in `order`.
bind PriorityQueue[T].new = fn(order: own cl(T, T) int) PriorityQueue[T]: PriorityQueue[T][
	_heap: [],
	_order: own order,
]

// An empty [`PriorityQueue`] that pops the smallest element first.
bind min_first[T: Comparable] = fn() PriorityQueue[T]: PriorityQueue[T].new(
	fn(a: T, b: T) int: a.cmp(b)
)

// An empty [`PriorityQueue`] that pops the largest element first.
bind max_first[T: Comparable] = fn() PriorityQueue[T]: PriorityQueue[T].new(
	fn(a: T, b: T) int: b.cmp(a)
)

// Returns the number of queued elements.
bind PriorityQueue[T].len = fn(self) int: self._heap.len()

// Reports whether the queue is empty.
bind PriorityQueue[T].is_empty = fn(self) bool: self._heap.is_empty()

// Reports whether the element at heap slot `i` comes out before the one at `j`.
bind PriorityQueue[T]._before = fn(self, i: int, j: int) bool: self._order(
	self._heap[i],
	self._heap[j],
) < 0

// Adds `item`.
bind PriorityQueue[T].push = fn(&self, item: own T): {
	self._heap.push(own item)
	let i = self._heap.len() - 1
	while(i > 0): {
		let parent = (i - 1) / 2
		if(not self._before(i, parent)): while <-
		self._heap.swap(i, parent)
		i = parent
	}
}

// Removes and returns the element that comes first, or `none` if the queue is empty.
bind PriorityQueue[T].pop = fn(&self) ?T: {
	let n = self._heap.len()
	if(n == 0): func <- none
	self._heap.swap(0, n - 1)
	let out? = self._heap.pop()
	let i = 0
	let len = n - 1
	while(true): {
		let left = 2 * i + 1
		if(left >= len): while <-
		let first = if(left + 1 < len and self._before(left + 1, left)): left + 1 else: left
		if(not self._before(first, i)): while <-
		self._heap.swap(i, first)
		i = first
	}
	out?
}

// Returns a copy of the element that comes first, without removing it, or `none` if the queue
// is empty.
bind PriorityQueue[T].peek = fn(self) ?T: self._heap.get(0)

// Drops every queued element.
bind PriorityQueue[T].clear = fn(&self): self._heap.clear()

// Removes every element and returns them in the order they would come out.
bind PriorityQueue[T].drain = fn(&self) [T]: {
	let out = List[T].with_capacity(self._heap.len())
	while(true): {
		let item = self.pop() ?? while <-
		out.push(own item)
	}
	out
}

// Returns a deep copy of the queue, with the same order.
bind PriorityQueue[T].copy = fn(self) PriorityQueue[T]: PriorityQueue[T][
	_heap: self._heap.copy(),
	_order: self._order.copy(),
]

@test("PriorityQueue pops in order")
bind test_priority_queue = fn(): {
	let small = min_first[int]()
	for(x in [5, 1, 8, 3, 9, 2, 7]): small.push(x)
	assert(small.peek() == 1 and small.len() == 7, "peek is the smallest")
	let copy = small.copy()
	assert(small.drain().to_string() == "[1, 2, 3, 5, 7, 8, 9]", "drain is in order")
	assert(small.is_empty() and small.pop() == none, "drained")
	assert(copy.pop() == 1 and copy.len() == 6, "copies are independent")

	let big = max_first[string]()
	for(w in ["pear", "apple", "zucchini", "fig"]): big.push(w.copy())
	assert(big.pop() == "zucchini" and big.pop() == "pear", "max_first")

	// A custom order that captures state: closest to a target first.
	let target = 10
	let near = PriorityQueue[int].new(cl(a: int, b: int) int: abs(a - target) - abs(b - target))
	for(x in [1, 25, 9, 14, 10]): near.push(x)
	assert(near.drain().to_string() == "[10, 9, 14, 1, 25]", "closure order")
}
