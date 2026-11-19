// std/pool — Generational arena [`Pool`] and stale-safe [`Handle`] identifiers.
//
// Enables cyclic graphs, entity-component tables, and UI node trees in safe Lowlight
// with O(1) slot reuse and automatic generation checks without garbage collection.

// Generational slot identifier referencing a value inside a [`Pool`].
define Handle[T] struct [
	// Zero-based backing slot index inside the pool.
	index: int,
	// Monotonic slot generation used to reject stale handles after [`Pool.free`].
	generation: int,
]

// Reports whether `self` and `other` point to the same slot index and generation.
bind Handle[T].equals = (self, other: Handle[T]) bool:
	self.index == other.index and self.generation == other.generation

define _PoolSlot[T] struct [
	value?: ?T,
	generation: int,
	alive: bool,
]

// Safe generational arena managing values of type [`T`] addressed by [`Handle`].
//
// # Example
// ```lowlight
// let pool = Pool[string].new()
// let h = pool.alloc("node")
// assert(pool.get(h) == "node")
// assert(pool.free(h))
// assert(pool.get(h) == none)
// ```
define Pool[T] struct [
	_slots: [_PoolSlot[T]],
	_free_indices: [int],
	_live_count: int,
]

// Constructs an empty [`Pool`] with no pre-allocated slots.
bind Pool[T].new = () Pool[T]:
	Pool[T][
		_slots: [],
		_free_indices: [],
		_live_count: 0,
	]

// Returns the number of currently live values in the pool.
bind Pool[T].len = (self) int: self._live_count

// Reports whether the pool contains zero currently live values.
bind Pool[T].is_empty = (self) bool: self._live_count == 0

// Returns the total number of slots allocated by the pool (live plus free).
bind Pool[T].capacity = (self) int: self._slots.len()

// Inserts `val` into the pool (reusing a freed slot when available) and returns its [`Handle`].
//
// # Ownership
// Takes ownership of `val`.
bind Pool[T].alloc = (&self, val: own T) Handle[T]: {
	if(self._free_indices.len() > 0): {
		let idx = self._free_indices.pop() ?? 0
		let gen = self._slots[idx].generation
		self._slots[idx].value? = own val
		self._slots[idx].alive = true
		self._live_count += 1
		func <- Handle[T][index: idx, generation: gen]
	}
	let idx = self._slots.len()
	self._slots.push(_PoolSlot[T][
		value?: own val,
		generation: 1,
		alive: true,
	])
	self._live_count += 1
	func <- Handle[T][index: idx, generation: 1]
}

// Reports whether `h` points to a currently live entry matching its generation.
bind Pool[T].has = (self, h: Handle[T]) bool: {
	if(h.index < 0 or h.index >= self._slots.len()): func <- false
	func <- self._slots[h.index].alive and self._slots[h.index].generation == h.generation
}

// Returns a copy of the value referenced by `h`, or `none` if `h` is out of bounds or stale.
//
// Requires `T` to be copyable; for non-copyable or custom-`Drop` resource types, borrow in place
// with [`Pool.with`], [`Pool.with_mut`], `for (v in pool)` / `for (&v in pool)`, or extract ownership
// via [`Pool.take`].
bind Pool[T].get = (self, h: Handle[T]) ?T: {
	if(h.index < 0 or h.index >= self._slots.len()): func <- none
	if(not self._slots[h.index].alive or self._slots[h.index].generation != h.generation):
		func <- none
	if(self._slots[h.index].value? as val): func <- val.copy()
	func <- none
}

// Invokes `reader` with the live value referenced by `h` without copying it, returning `none` if `h` is stale.
//
// - `reader`: Read-only projection function receiving the stored value.
bind Pool[T].with[R] = (self, h: Handle[T], reader: (T) R) ?R: {
	if(h.index < 0 or h.index >= self._slots.len()): func <- none
	if(not self._slots[h.index].alive or self._slots[h.index].generation != h.generation):
		func <- none
	if(self._slots[h.index].value? as val): func <- reader(val)
	func <- none
}

// Invokes `mutator` with a mutable borrow of the live value referenced by `h` in place.
//
// Works with non-copyable and custom-`Drop` resource types without copying `T`.
// Returns `true` if `h` was valid and `mutator` ran, or `false` if `h` is out of bounds or stale.
bind Pool[T].with_mut = (&self, h: Handle[T], mutator: (&T)) bool: {
	if(not self.has(h)): func <- false
	if(&self._slots[h.index].value? as val): {
		mutator(&val)
		func <- true
	}
	func <- false
}

// Replaces the value stored at `h` with `val`, returning `false` if `h` is invalid or freed.
//
// # Ownership
// Consumes `val` on success (or drops it if `h` is invalid).
bind Pool[T].set = (&self, h: Handle[T], val: own T) bool: {
	if(h.index < 0 or h.index >= self._slots.len()): func <- false
	if(not self._slots[h.index].alive or self._slots[h.index].generation != h.generation):
		func <- false
	self._slots[h.index].value? = own val
	func <- true
}

// Removes and returns the owned value at `h` without requiring `T` to be copyable.
//
// Advances the slot generation so `h` becomes stale and recycles the slot index.
// Returns `none` if `h` is out of bounds or already freed.
bind Pool[T].take = (&self, h: Handle[T]) ?T: {
	if(not self.has(h)): func <- none
	let out? = unsafe {
		let p = addr_of(self._slots[h.index].value?)
		let v? = ptr_read(p)
		zero_bytes(ptr_cast[u8](p), size_of[?T]())
		v?
	}
	self._slots[h.index].alive = false
	self._slots[h.index].generation += 1
	self._free_indices.push(h.index)
	self._live_count -= 1
	func <- own out?
}

// Drops the value at `h`, increments its slot generation, and marks it reusable.
//
// Returns `false` if `h` is already freed or out of bounds.
bind Pool[T].free = (&self, h: Handle[T]) bool: {
	if(h.index < 0 or h.index >= self._slots.len()): func <- false
	if(not self._slots[h.index].alive or self._slots[h.index].generation != h.generation):
		func <- false
	self._slots[h.index].value? = none
	self._slots[h.index].alive = false
	self._slots[h.index].generation += 1
	self._free_indices.push(h.index)
	self._live_count -= 1
	func <- true
}

// Drops all live values in the pool, invalidates all existing handles by incrementing their
// slot generations, and recycles their slot indices while retaining backing capacity.
bind Pool[T].clear = (&self): {
	let i = 0
	for(i < self._slots.len()): {
		if(self._slots[i].alive): {
			self._slots[i].value? = none
			self._slots[i].alive = false
			self._slots[i].generation += 1
			self._free_indices.push(i)
		}
		i += 1
	}
	self._live_count = 0
}

// Returns a list of [`Handle`] identifiers for all currently live entries in slot order.
//
// Works for all element types `T`, including non-copyable and custom-`Drop` resource types.
bind Pool[T].handles = (self) [Handle[T]]: {
	let out: [Handle[T]] = []
	let i = 0
	for(i < self._slots.len()): {
		if(self._slots[i].alive):
			out.push(Handle[T][index: i, generation: self._slots[i].generation])
		i += 1
	}
	func <- own out
}

// Returns a list containing copies of all currently live values in slot order.
//
// Requires `T` to be copyable; for non-copyable types use `for (v in pool)` or [`Pool.iter_handles`].
bind Pool[T].values = (self) [T]: {
	let out: [T] = []
	let i = 0
	for(i < self._slots.len()): {
		if(self._slots[i].alive): {
			if(self._slots[i].value? as val): out.push(val.copy())
		}
		i += 1
	}
	func <- own out
}

// Stateful forward iterator yielding copied elements from an owned snapshot of a [`Pool`].
//
// Owning a snapshot guards iteration against mutations or freeing on the source pool.
define PoolIterator[T] struct [
	_items: [T],
	_index: int,
]

// Advances the iterator and returns a copy of the next live element, or `none` when finished.
bind PoolIterator[T].next = (&self) ?T: {
	if(self._index >= self._items.len()): func <- none
	let i = self._index
	self._index += 1
	func <- self._items.get(i)
}

// Returns a forward [`Iterator`] yielding copies of all currently live values in slot order.
bind Pool[T].iter = (self) Iterator[T]:
	PoolIterator[T][
		_items: self.values(),
		_index: 0,
	]

// Stateful forward iterator yielding currently live [`Handle`] entries from a [`Pool`].
//
// Works for all element types `T`, including non-copyable and custom-`Drop` resource types.
define PoolHandleIterator[T] struct [
	_handles: [Handle[T]],
	_index: int,
]

// Advances the iterator and returns the next live handle, or `none` when finished.
bind PoolHandleIterator[T].next = (&self) ?Handle[T]: {
	if(self._index >= self._handles.len()): func <- none
	let i = self._index
	self._index += 1
	func <- self._handles.get(i)
}

// Returns a forward [`Iterator`] over all currently live [`Handle`] entries in slot order.
//
// Works for all element types `T`, including non-copyable and custom-`Drop` resource types.
bind Pool[T].iter_handles = (self) Iterator[Handle[T]]:
	PoolHandleIterator[T][
		_handles: self.handles(),
		_index: 0,
	]

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("generational pool allocation, retrieval, mutation, and len")
bind test_pool_alloc_and_get = (): {
	let pool = Pool[string].new()
	assert(pool.len() == 0, "initial len 0")

	let h1 = pool.alloc("first")
	let h2 = pool.alloc("second")
	assert(pool.len() == 2, "len after 2 allocs")

	assert(pool.has(h1), "pool has h1")
	assert(pool.has(h2), "pool has h2")

	let v1? = pool.get(h1)
	assert(v1? == "first", "v1 matches")
	let v2? = pool.get(h2)
	assert(v2? == "second", "v2 matches")

	assert(pool.set(h1, "first_updated"), "set succeeds")
	let v1_up? = pool.get(h1)
	assert(v1_up? == "first_updated", "v1 updated matches")

	let read_len? = pool.with[int](h2, (s: string) int: s.len())
	assert(read_len? == 6, "with reader returned string length")
}

@test("generational pool free, reuse, and stale handle invalidation")
bind test_pool_free_and_reuse = (): {
	let pool = Pool[string].new()
	let h1 = pool.alloc("slot_0")
	let h2 = pool.alloc("slot_1")

	assert(pool.free(h1), "free h1 succeeds")
	assert(not pool.free(h1), "double free h1 returns false")
	assert(pool.len() == 1, "len after 1 free")

	// Stale handle detection
	assert(not pool.has(h1), "has stale h1 returns false")
	let stale? = pool.get(h1)
	assert(stale? == none, "get stale h1 returns none")

	// Allocate again: should reuse slot index of h1 with incremented generation
	let h3 = pool.alloc("slot_0_reused")
	assert(pool.len() == 2, "len after reusing slot")
	assert(h3.index == h1.index, "reused same slot index")
	assert(h3.generation > h1.generation, "generation incremented")

	// Original handle h1 still cannot access the new item in the same slot
	assert(not pool.has(h1), "old handle h1 still invalid")
	let old_v? = pool.get(h1)
	assert(old_v? == none, "old handle get returns none")

	// New handle h3 can access it cleanly
	assert(pool.has(h3), "new handle h3 valid")
	let new_v? = pool.get(h3)
	assert(new_v? == "slot_0_reused", "new handle get succeeds")
}

@test("generational handle equality and out of bounds checks")
bind test_pool_handle_equality = (): {
	let pool = Pool[int].new()
	let h1 = pool.alloc(100)
	let h2 = pool.alloc(200)

	assert(h1.equals(h1), "h1 equals h1")
	assert(not h1.equals(h2), "h1 not equals h2")

	let out_of_bounds = Handle[int][index: 9999, generation: 1]
	assert(not pool.has(out_of_bounds), "out of bounds has is false")
	let oob? = pool.get(out_of_bounds)
	assert(oob? == none, "out of bounds get is none")
	assert(not pool.free(out_of_bounds), "out of bounds free is false")
}

@test("pool with_mut, is_empty, clear, and slot recycling")
bind test_pool_with_mut_and_clear = (): {
	let pool = Pool[int].new()
	assert(pool.is_empty(), "new pool is empty")
	pool.clear()
	assert(pool.is_empty() and pool.len() == 0, "clear on empty pool stays empty")

	let h1 = pool.alloc(10)
	let h2 = pool.alloc(20)
	let h3 = pool.alloc(30)
	assert(not pool.is_empty(), "pool with items is not empty")

	assert(pool.with_mut(h2, (v: &int): { v += 25 }), "with_mut on live handle returns true")
	assert(pool.get(h2) == 45, "with_mut mutated value in place")

	assert(pool.free(h3), "free h3")
	assert(not pool.with_mut(h3, (v: &int): { v += 1 }), "with_mut on freed handle returns false")
	let oob = Handle[int][index: -1, generation: 1]
	assert(not pool.with_mut(oob, (v: &int): { v += 1 }), "with_mut on out of bounds handle returns false")

	let cap_before = pool.capacity()
	pool.clear()
	assert(pool.is_empty(), "pool is empty after clear")
	assert(pool.len() == 0, "len is 0 after clear")
	assert(pool.capacity() == cap_before, "clear retains backing slot capacity")
	assert(not pool.has(h1) and not pool.has(h2) and not pool.has(h3), "all handles stale after clear")
	assert(pool.get(h1) == none and pool.get(h2) == none, "get stale handles returns none after clear")
	assert(not pool.with_mut(h1, (v: &int): { v += 1 }), "with_mut stale after clear")

	let h4 = pool.alloc(99)
	assert(pool.len() == 1 and not pool.is_empty(), "len 1 after realloc following clear")
	assert(pool.capacity() == cap_before, "reused existing cleared slot without growing capacity")
	assert(pool.get(h4) == 99, "new allocation after clear returns stored value")
	assert(not pool.has(h1) and not pool.has(h2), "previous handles remain invalid after reuse")
}

@test("pool iterators, snapshots, and native for loops")
bind test_pool_iterators_and_for_loops = (): {
	let pool = Pool[int].new()
	let h1 = pool.alloc(10)
	let h2 = pool.alloc(20)
	let h3 = pool.alloc(30)
	assert(pool.free(h2), "free middle slot")
	let h4 = pool.alloc(40)
	let h5 = pool.alloc(50)
	assert(pool.free(h4), "leave a freed middle hole")

	// Snapshot lists and explicit iterators skip freed slots.
	let hs = pool.handles()
	let vs = pool.values()
	assert(hs.len() == 3 and vs.len() == 3, "handles and values match live count")
	assert(hs[0].equals(h1) and hs[1].equals(h3) and hs[2].equals(h5), "handles in slot order")
	assert(vs.get(0) == 10 and vs.get(1) == 30 and vs.get(2) == 50, "values in slot order")

	let it = pool.iter()
	let it_sum = 0
	for(true): {
		let next? = it.next()
		if(next? as val): it_sum += val else: for <-
	}
	assert(it_sum == 90, "PoolIterator sums live values")

	let hit = pool.iter_handles()
	let hit_count = 0
	for(true): {
		let next? = hit.next()
		if(next? as h): {
			assert(pool.has(h), "PoolHandleIterator yields live handle")
			hit_count += 1
		} else: for <-
	}
	assert(hit_count == 3, "PoolHandleIterator visits exact live count")

	// Native 1-var read-only and mutable for loops.
	let sum_ro = 0
	for(v in pool): sum_ro += v
	assert(sum_ro == 90, "for(v in pool) visits all live values")

	for(&v in pool): v += 5
	assert(pool.get(h1) == 15 and pool.get(h3) == 35 and pool.get(h5) == 55, "for(&v in pool) mutates slots in place")

	// Native 2-var (handle, value) and (handle, &mut value) loops.
	let pair_count = 0
	let pair_sum = 0
	for(h, v in pool): {
		assert(pool.has(h), "2-var loop handle is valid")
		pair_sum += v
		pair_count += 1
	}
	assert(pair_count == 3 and pair_sum == 105, "for(h, v in pool) visits all live pairs")

	for(h, &v in pool): {
		if(h.equals(h3)): v += 100
	}
	assert(pool.get(h1) == 15 and pool.get(h3) == 135 and pool.get(h5) == 55, "for(h, &v in pool) mutates matching handle")
}

define _OwnedBuf struct [
	raw: ptr[int],
	len: int,
]

bind _OwnedBuf.new = (len: int, start: int) _OwnedBuf: {
	let p = unsafe alloc_uninit[int](len)
	let i = 0
	for(i < len): {
		unsafe ptr_write(ptr_add(p, i), start + i)
		i += 1
	}
	func <- _OwnedBuf[raw: p, len: len]
}

bind _OwnedBuf.sum = (self) int: {
	let acc = 0
	let i = 0
	for(i < self.len): {
		acc += unsafe ptr_read(ptr_add(self.raw, i))
		i += 1
	}
	func <- acc
}

bind _OwnedBuf.add_offset = (&self, delta: int): {
	let i = 0
	for(i < self.len): {
		unsafe {
			let cur = ptr_read(ptr_add(self.raw, i))
			ptr_write(ptr_add(self.raw, i), cur + delta)
		}
		i += 1
	}
}

bind _OwnedBuf.drop = (&self): {
	if(self.len > 0): {
		unsafe free_raw(self.raw)
		self.raw = unsafe null_ptr[int]()
		self.len = 0
	}
}

@test("pool with non-copyable custom Drop resource handles with, with_mut, for loops, take, and clear")
bind test_pool_non_copyable_resource = (): {
	let pool = Pool[_OwnedBuf].new()
	let h1 = pool.alloc(_OwnedBuf.new(3, 10))
	let h2 = pool.alloc(_OwnedBuf.new(2, 100))

	// Inferred method type parameter `R` on `Pool.with`!
	let s1? = pool.with(h1, (b: _OwnedBuf) int: b.sum())
	assert(s1? == 33, "with on non-copyable resource with inferred return type")

	assert(pool.with_mut(h1, (b: &_OwnedBuf): b.add_offset(5)), "with_mut on non-copyable resource")
	assert(pool.with(h1, (b: _OwnedBuf) int: b.sum()) == 48, "with_mut updated custom Drop buffer in place")

	// Non-copyable iteration via native `for` loops (no copy of _OwnedBuf).
	let total = 0
	for(b in pool): total += b.sum()
	assert(total == 249, "for(b in pool) borrows non-copyable resources without copying")

	for(&b in pool): b.add_offset(1)
	for(h, &b in pool): {
		if(h.equals(h2)): b.add_offset(10)
	}
	assert(pool.with(h1, (b: _OwnedBuf) int: b.sum()) == 51, "h1 updated via mutable for loops")
	assert(pool.with(h2, (b: _OwnedBuf) int: b.sum()) == 223, "h2 updated via 2-var mutable for loop")

	let seen_handles = 0
	let hit = pool.iter_handles()
	for(true): {
		let next? = hit.next()
		if(next? as h): {
			assert(pool.has(h), "iter_handles works on non-copyable Pool")
			seen_handles += 1
		} else: for <-
	}
	assert(seen_handles == 2 and pool.handles().len() == 2, "handle snapshot works on non-copyable Pool")

	let ro_sum = 0
	for(h, b in pool): {
		assert(pool.has(h), "2-var read-only loop handle valid on non-copyable Pool")
		ro_sum += b.sum()
	}
	assert(ro_sum == 274, "2-var read-only loop sums non-copyable Pool")

	// Extract ownership with take() without Copyable.
	let taken? = pool.take(h1)
	assert(pool.len() == 1, "take decremented pool len")
	assert(not pool.has(h1), "taken handle is invalidated")
	assert(pool.take(h1) == none, "second take on stale handle returns none")
	if(&taken? as b): {
		assert(b.sum() == 51, "taken non-copyable resource preserves value")
	} else: {
		assert(false, "take returned none for valid handle")
	}

	// Reallocate into taken slot and verify generation bump + clear() leak-free cleanup.
	let h3 = pool.alloc(_OwnedBuf.new(2, 5))
	assert(h3.index == h1.index and h3.generation > h1.generation, "take recycled slot and bumped generation")
	assert(pool.with(h3, (b: _OwnedBuf) int: b.sum()) == 11, "new non-copyable value in recycled slot")

	pool.clear()
	assert(pool.is_empty(), "clear empties non-copyable pool")
	assert(not pool.has(h2) and not pool.has(h3), "clear invalidates remaining handles")
}
