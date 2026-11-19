// std/core/set — Open-addressed hash set ([`Set`]) backed by linear probing.
//
// Requires element types implementing [`Hashable`] (and [`Equatable`] / [`Copyable`] for
// containment and iteration). Backed by `#[T]` type and literal syntax.


// Internal entry slot stored in a [`Set`]'s entry table.
define SetEntry[T: Hashable] struct [
	// Whether this slot holds a live element (`false` when removed tombstone).
	alive: bool,
	// Cached 64-bit hash digest of `key`.
	hash: u64,
	// Stored element key when `alive` is `true`.
	key: T,
]

// Dynamic open-addressed hash set of unique elements of type [`T`].
//
// Backed by set type syntax `#[T]` and literals (`#[1, 2, 3]`). Implements
// [`Containable`], [`Collectable`], [`Iterable`], [`Copyable`], [`Printable`], and [`Drop`].
//
// # Example
// ```lowlight
// let seen = #[ "alpha", "beta" ]
// assert(seen.insert("gamma"))
// assert(seen.contains("alpha"))
// ```
@lang("set")
define Set[T: Hashable] struct [
	// Bucket index table mapping hashes to entry indices (`-1` empty, `-2` tombstone).
	buckets: ptr[int],
	// Allocated bucket table size (power of two).
	buck_cap: int,
	// Dense entry array holding element keys and cached hashes.
	entries: ptr[SetEntry[T]],
	// Allocated capacity of `entries`.
	ent_cap: int,
	// High-water mark of used/tombstoned slots in `entries`.
	ent_len: int,
	// Number of live elements in the set.
	len: int,
]

// Constructs an empty [`Set`] with zero heap allocation.
bind Set[T].new = () Set[T]:
	Set[T][
		buckets: unsafe null_ptr[int](),
		buck_cap: 0,
		entries: unsafe null_ptr[SetEntry[T]](),
		ent_cap: 0,
		ent_len: 0,
		len: 0,
	]

// Constructs an empty [`Set`] pre-sized for at least `cap` distinct elements.
bind Set[T].with_capacity = (cap: int) Set[T]: {
	if(cap <= 0): func <- Set[T].new()
	let bcap = 8
	for(bcap < cap * 2): bcap *= 2
	let b = unsafe alloc_raw[int](bcap)
	let i = 0
	for(i < bcap): {
		unsafe {
			b[i] = -1
		}
		i += 1
	}
	let e = unsafe alloc_raw[SetEntry[T]](cap)
	Set[T][
		buckets: b,
		buck_cap: bcap,
		entries: e,
		ent_cap: cap,
		ent_len: 0,
		len: 0,
	]
}

// Returns the number of live elements currently in the set.
bind Set[T].len = (self) int: self.len

// Reports whether the set contains zero elements.
bind Set[T].is_empty = (self) bool: self.len == 0

// Internal probe lookup returning the entry index of `needle` with hash `h`, or `-1`.
bind Set[T].find_entry = (self, needle: T, h: u64) int: {
	if(self.buck_cap == 0 or unsafe is_null(self.buckets)): func <- -1
	let mask = self.buck_cap - 1
	let slot = int(h & u64(mask))
	for(true): {
		let idx = unsafe self.buckets[slot]
		if(idx == -1): func <- -1
		if(idx >= 0):
			unsafe {
				if(self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == needle):
					func <- idx
			}
		slot = slot + 1 & mask
	}
	-1
}

// Reports whether `val` is present in the set.
bind Set[T].has = (self, val: T) bool: {
	let h = val.hash()
	self.find_entry(val, h) >= 0
}

// Reports whether `val` is present in the set (satisfies [`Containable`]).
bind Set[T].contains = (self, val: T) bool: self.has(val)

// Internal helper that compacts tombstoned entries and rebuilds the hash bucket table at `new_bcap`.
bind Set[T].rehash = (&self, new_bcap: int): {
	if(self.len < self.ent_len): {
		let w = 0
		let r = 0
		for(r < self.ent_len): {
			unsafe {
				if(self.entries[r].alive): {
					if(w != r):
						copy_bytes(
							ptr_cast[u8](ptr_add(self.entries, w)),
							ptr_cast[u8](ptr_add(self.entries, r)),
							size_of[SetEntry[T]](),
						)
					w += 1
				}
			}
			r += 1
		}
		self.ent_len = w
	}
	let nb = unsafe alloc_raw[int](new_bcap)
	let bi = 0
	for(bi < new_bcap): {
		unsafe {
			nb[bi] = -1
		}
		bi += 1
	}
	let mask = new_bcap - 1
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive): {
				let slot = int(self.entries[i].hash & u64(mask))
				for(nb[slot] >= 0): slot = slot + 1 & mask
				nb[slot] = i
			}
		}
		i += 1
	}
	unsafe {
		if(not is_null(self.buckets)): free_raw(self.buckets)
	}
	self.buckets = nb
	self.buck_cap = new_bcap
}

// Inserts `val` if absent and returns `true`, or leaves the set unchanged and returns `false`.
//
// # Ownership
// Consumes `val` into the set's entry storage.
bind Set[T].insert = (&self, val: own T) bool: {
	let h = val.hash()
	let existing = self.find_entry(val, h)
	if(existing >= 0): func <- false

	if(self.buck_cap == 0 or (self.ent_len + 1) * 10 >= self.buck_cap * 7): {
		let target_bcap = if(self.buck_cap > 0): self.buck_cap * 2 else: 16
		self.rehash(target_bcap)
	}
	if(self.ent_len + 1 > self.ent_cap): {
		let ncap = if(self.ent_cap > 0): self.ent_cap * 2 else: 8
		let ne = unsafe {
			if(self.ent_cap == 0): alloc_raw[SetEntry[T]](ncap)
			else: realloc_raw(self.entries, ncap)
		}
		self.entries = ne
		self.ent_cap = ncap
	}
	let idx = self.ent_len
	self.ent_len += 1
	unsafe {
		self.entries[idx].alive = true
		self.entries[idx].hash = h
		self.entries[idx].key = own val
	}
	let mask = self.buck_cap - 1
	let slot = int(h & u64(mask))
	for(unsafe self.buckets[slot] >= 0): slot = slot + 1 & mask
	unsafe {
		self.buckets[slot] = idx
	}
	self.len += 1
	true
}

// Inserts `item` into the set (satisfies [`Collectable`] for set comprehensions).
bind Set[T].push = (&self, item: own T): {
	let _ = self.insert(own item)
}

// Removes and drops `val` if present, returning `true`, or returns `false` if absent.
bind Set[T].remove = (&self, val: T) bool: {
	if(self.buck_cap == 0 or unsafe is_null(self.buckets)): func <- false
	let h = val.hash()
	let mask = self.buck_cap - 1
	let slot = int(h & u64(mask))
	for(true): {
		let idx = unsafe self.buckets[slot]
		if(idx == -1): func <- false
		if(idx >= 0):
			unsafe {
				if(self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == val): {
					let _ = ptr_read(ptr_add(self.entries, idx))
					self.entries[idx].alive = false
					self.buckets[slot] = -2
					self.len -= 1
					func <- true
				}
			}
		slot = slot + 1 & mask
	}
	false
}

// Drops all stored elements and resets the set to empty while retaining allocated capacity.
bind Set[T].clear = (&self): {
	if(not unsafe is_null(self.entries)): {
		let i = 0
		for(i < self.ent_len): {
			unsafe {
				if(self.entries[i].alive): {
					let _ = ptr_read(ptr_add(self.entries, i))
					self.entries[i].alive = false
				}
			}
			i += 1
		}
	}
	if(self.buck_cap > 0 and unsafe not is_null(self.buckets)): {
		let i = 0
		for(i < self.buck_cap): {
			unsafe {
				self.buckets[i] = -1
			}
			i += 1
		}
	}
	self.ent_len = 0
	self.len = 0
}

// Collects copies of all live elements into a new [`List`] in insertion order.
bind Set[T].to_list = (self) List[T]: {
	let out = List[T].with_capacity(self.len)
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive): out.push(self.entries[i].key.copy())
		}
		i += 1
	}
	out
}

// Returns a deep copy of the set containing copies of every live element.
bind Set[T].copy = (self) Set[T]: {
	let out = Set[T].with_capacity(self.len)
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive): out.insert(self.entries[i].key.copy())
		}
		i += 1
	}
	out
}

// Formats the set as `"#[a, b, ...]"`.
bind Set[T].to_string = (self) string: {
	let out = "#["
	let first = true
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive): {
				if(not first): out.push(", ")
				out.push(self.entries[i].key.to_string())
				first = false
			}
		}
		i += 1
	}
	out.push("]")
	out
}

// Stateful forward iterator yielding copies of live elements from a [`Set`].
define SetIterator[T: Hashable] struct [
	// Pointer to the backing set entry array.
	entries: ptr[SetEntry[T]],
	// Number of entry slots to scan.
	ent_len: int,
	// Next slot index to check.
	idx: int,
]

// Advances the iterator and returns a copy of the next live element, or `none` when finished.
bind SetIterator[T].next = (&self) ?T: {
	for(self.idx < self.ent_len): {
		let i = self.idx
		self.idx += 1
		unsafe {
			if(self.entries[i].alive): func <- self.entries[i].key.copy()
		}
	}
	none
}

// Returns a forward [`Iterator`] yielding copies of all live elements in the set.
bind Set[T].iter = (self) Iterator[T]:
	SetIterator[T][
		entries: self.entries,
		ent_len: self.ent_len,
		idx: 0,
	]

// Drops all remaining live elements and frees the entry and bucket tables.
bind Set[T].drop = (&self): {
	if(not unsafe is_null(self.entries)): {
		let i = 0
		for(i < self.ent_len): {
			unsafe {
				if(self.entries[i].alive): {
					let _ = ptr_read(ptr_add(self.entries, i))
				}
			}
			i += 1
		}
		unsafe free_raw(self.entries)
	}
	if(not unsafe is_null(self.buckets)): unsafe free_raw(self.buckets)
	self.entries = unsafe null_ptr[SetEntry[T]]()
	self.buckets = unsafe null_ptr[int]()
	self.buck_cap = 0
	self.ent_cap = 0
	self.ent_len = 0
	self.len = 0
}
