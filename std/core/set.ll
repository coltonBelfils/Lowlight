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
const Set[T].new = fn() Set[T]:
	Set[T][
		buckets: unsafe null_ptr[int](),
		buck_cap: 0,
		entries: unsafe null_ptr[SetEntry[T]](),
		ent_cap: 0,
		ent_len: 0,
		len: 0,
	]

// Constructs an empty [`Set`] pre-sized for at least `cap` distinct elements.
const Set[T].with_capacity = fn(cap: int) Set[T]: {
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
const Set[T].len = fn(self) int: self.len

// Reports whether the set contains zero elements.
const Set[T].is_empty = fn(self) bool: self.len == 0

// Internal probe lookup returning the entry index of `needle` with hash `h`, or `-1`.
const Set[T].find_entry = fn(self, needle: T, h: u64) int: {
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
const Set[T].has = fn(self, val: T) bool: {
	let h = val.hash()
	self.find_entry(val, h) >= 0
}

// Reports whether `val` is present in the set (satisfies [`Containable`]).
const Set[T].contains = fn(self, val: T) bool: self.has(val)

// Internal helper that compacts tombstoned entries and rebuilds the hash bucket table at `new_bcap`.
const Set[T].rehash = fn(&self, new_bcap: int): {
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
							type.size_of[SetEntry[T]](),
						)
					w += 1
				}
			}
			r += 1
		}
		if(w < self.ent_len): {
			unsafe zero_bytes(
				ptr_cast[u8](ptr_add(self.entries, w)),
				(self.ent_len - w) * type.size_of[SetEntry[T]](),
			)
		}
		self.ent_len = w
	}
	let in_place = new_bcap == self.buck_cap and not unsafe is_null(self.buckets)
	let nb = if(in_place): self.buckets else: unsafe alloc_raw[int](new_bcap)
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
	if(not in_place): {
		unsafe {
			if(not is_null(self.buckets)): free_raw(self.buckets)
		}
		self.buckets = nb
		self.buck_cap = new_bcap
	}
}

// Inserts `val` if absent and returns `true`, or leaves the set unchanged and returns `false`.
//
// # Ownership
// Consumes `val` into the set's entry storage.
const Set[T].insert = fn(&self, val: own T) bool: {
	let h = val.hash()
	let existing = self.find_entry(val, h)
	if(existing >= 0): func <- false

	if(self.buck_cap == 0 or (self.ent_len + 1) * 10 >= self.buck_cap * 7): {
		let target_bcap = if(self.buck_cap == 0): 16 else: {
			if(self.len * 2 >= self.buck_cap): self.buck_cap * 2 else: self.buck_cap
		}
		self.rehash(target_bcap)
	}
	if(self.ent_len + 1 > self.ent_cap): {
		let old_cap = self.ent_cap
		let ncap = if(old_cap > 0): old_cap * 2 else: 8
		let ne = unsafe {
			if(old_cap == 0): {
				alloc_raw[SetEntry[T]](ncap)
			} else: {
				let p = realloc_raw(self.entries, ncap)
				zero_bytes(
					ptr_cast[u8](ptr_add(p, old_cap)),
					(ncap - old_cap) * type.size_of[SetEntry[T]](),
				)
				p
			}
		}
		self.entries = ne
		self.ent_cap = ncap
	}
	let idx = self.ent_len
	self.ent_len += 1
	// `ptr_write` fills the empty slot without dropping its old bytes as a value.
	unsafe ptr_write(ptr_add(self.entries, idx), SetEntry[T][alive: true, hash: h, key: own val])
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
const Set[T].push = fn(&self, item: own T): {
	let _ = self.insert(own item)
}

// Removes and drops `val` if present, returning `true`, or returns `false` if absent.
const Set[T].remove = fn(&self, val: T) bool: {
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
					zero_bytes(ptr_cast[u8](ptr_add(self.entries, idx)), type.size_of[SetEntry[T]]())
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
const Set[T].clear = fn(&self): {
	if(not unsafe is_null(self.entries)): {
		let i = 0
		for(i < self.ent_len): {
			unsafe {
				if(self.entries[i].alive): {
					let _ = ptr_read(ptr_add(self.entries, i))
				}
				zero_bytes(ptr_cast[u8](ptr_add(self.entries, i)), type.size_of[SetEntry[T]]())
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
const Set[T].to_list = fn(self) List[T]: {
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
const Set[T].copy = fn(self) Set[T]: {
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

// A new set holding every element that is in `self`, `other`, or both: `#[1, 2].union(#[2, 3])`
// is `#[1, 2, 3]`. Elements are copied; `self`'s come first, then `other`'s new ones.
const Set[T].union = fn(self, other: Set[T]) Set[T]: {
	let out = self.copy()
	let i = 0
	for(i < other.ent_len): {
		unsafe {
			if(other.entries[i].alive): {
				let _ = out.insert(other.entries[i].key.copy())
			}
		}
		i += 1
	}
	out
}

// A new set holding the elements of `self` that are also in `other`:
// `#[1, 2, 3].intersection(#[2, 3, 4])` is `#[2, 3]`. Elements are copied, in `self`'s order.
const Set[T].intersection = fn(self, other: Set[T]) Set[T]: {
	let out = Set[T].new()
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive and other.has(self.entries[i].key)): {
				let _ = out.insert(self.entries[i].key.copy())
			}
		}
		i += 1
	}
	out
}

// A new set holding the elements of `self` that are not in `other`:
// `#[1, 2, 3].difference(#[2])` is `#[1, 3]`. Elements are copied, in `self`'s order.
const Set[T].difference = fn(self, other: Set[T]) Set[T]: {
	let out = Set[T].new()
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive and not other.has(self.entries[i].key)): {
				let _ = out.insert(self.entries[i].key.copy())
			}
		}
		i += 1
	}
	out
}

// Reports whether every element of `self` is also in `other`. The empty set is a subset of
// every set, and every set is a subset of itself.
const Set[T].is_subset = fn(self, other: Set[T]) bool: {
	if(self.len > other.len): func <- false
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive and not other.has(self.entries[i].key)): func <- false
		}
		i += 1
	}
	true
}

// Formats the set as `"#[a, b, ...]"`.
const Set[T].to_string = fn(self) string: {
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
const SetIterator[T].next = fn(&self) ?T: {
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
const Set[T].iter = fn(self) Iterator[T]:
	SetIterator[T][
		entries: self.entries,
		ent_len: self.ent_len,
		idx: 0,
	]

// Drops all remaining live elements and frees the entry and bucket tables.
const Set[T].drop = fn(&self): {
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

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("Set[T] realloc_raw growth, compaction, and clear with Resource elements")
bind test_set_realloc_and_slot_zeroing = fn(): {
	// Churn dirty heap blocks filled with 0xFF before Set grows via realloc_raw.
	for(c in 0..8): {
		let dirty = List[u8].filled(512, u8(255))
		assert(dirty[0] == u8(255), "dirty block populated")
	}

	let s = Set[string].new()
	for(i in 0..64): {
		assert(s.insert("entry_key_$i"), "insert entry_key_$i")
	}
	assert(s.len() == 64, "len after 64 inserts")

	// Remove even keys, then insert 40 more to trigger rehash compaction & slot reuse.
	for(i in 0..32): {
		let even = i * 2
		assert(s.remove("entry_key_$even"), "remove even entry_key")
	}
	assert(s.len() == 32, "len after 32 removes")

	for(i in 0..40): {
		assert(s.insert("compacted_key_$i"), "insert compacted_key_$i")
	}
	assert(s.len() == 72, "len after 40 compacted inserts")

	for(i in 0..32): {
		let even = i * 2
		let odd = i * 2 + 1
		assert(not s.has("entry_key_$even"), "removed even key stays absent")
		assert(s.has("entry_key_$odd"), "remaining odd key present")
	}
	for(i in 0..40): {
		assert(s.has("compacted_key_$i"), "compacted key present")
	}

	// Clear and re-insert into previously occupied/zeroed slots.
	s.clear()
	assert(s.is_empty(), "empty after clear")
	for(i in 0..20): {
		assert(s.insert("cleared_key_$i"), "reinsert after clear")
	}
	assert(s.len() == 20, "len == 20 after clear + reinsert")
	for(i in 0..20): {
		assert(s.has("cleared_key_$i"), "cleared_key present")
	}
}

@test("Set[T] rehashes in place when tombstones accumulate at low load")
bind test_set_in_place_rehash_low_load = fn(): {
	let s = Set[string].new()
	assert(s.insert("anchor"), "insert anchor")
	let compactions = 0
	let prev_ent_len = s.ent_len
	for(i in 0..40): {
		assert(s.insert("temp_$i"), "insert temp_$i")
		if(s.ent_len < prev_ent_len): compactions += 1
		prev_ent_len = s.ent_len
		assert(s.remove("temp_$i"), "remove temp_$i")
	}
	assert(s.len() == 1, "only anchor remains")
	assert(s.has("anchor"), "anchor survived in-place compaction")
	assert(s.buck_cap == 16, "buck_cap stays 16 at low load")
	assert(compactions >= 3, "tombstones compacted in place multiple times")
	assert(s.ent_len <= 11, "ent_len bounded by load factor")
}

@test("Set[T] union, intersection, difference, and is_subset")
bind test_set_algebra = fn(): {
	let a = #[1, 2, 3]
	let b = #[2, 3, 4]
	assert(a.union(b).to_string() == "#[1, 2, 3, 4]", "union keeps self's order, then other's")
	assert(a.intersection(b).to_string() == "#[2, 3]", "intersection")
	assert(a.difference(b).to_string() == "#[1]", "difference")
	assert(b.difference(a).to_string() == "#[4]", "difference is not symmetric")
	assert(#[2, 3].is_subset(a) and a.is_subset(a), "subsets")
	assert(not a.is_subset(b) and not b.is_subset(#[2]), "not subsets")
	let empty: #[int] = #[]
	assert(
		empty.is_subset(a) and a.union(empty).len() == 3 and a.intersection(empty).is_empty(),
		"empty set",
	)

	let words = #["red" + "", "green" + ""]
	let more = #["green" + "", "blue" + ""]
	assert(words.union(more).len() == 3, "Resource elements are copied")
	assert(words.intersection(more).contains("green"), "Resource intersection")
	assert(words.len() == 2 and more.len() == 2, "operands are unchanged")
}
