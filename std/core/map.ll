// std/core/map — Open-addressed hash map ([`Map`]) backed by linear probing.
//
// Requires key types implementing [`Hashable`] (and [`Equatable`] / [`Copyable`] for
// lookup and iteration). Backed by `#[K: V]` type and literal syntax.


// Internal entry slot stored in a [`Map`]'s entry table.
define MapSlot[K: Hashable, V] struct [
	// Whether this slot holds a live key-value pair (`false` when tombstoned).
	alive: bool,
	// Cached 64-bit hash digest of `key`.
	hash: u64,
	// Stored key when `alive` is `true`.
	key: K,
	// Stored value when `alive` is `true`.
	val: V,
]

// Dynamic open-addressed hash map from hashable keys [`K`] to values [`V`].
//
// Backed by map syntax `#[K: V]` and literals (`#["a": 1, "b": 2]`). Implements
// [`KeyIndexable`], [`Containable`], [`Collectable`], [`Iterable`], [`Copyable`],
// [`Printable`], and [`Drop`].
//
// # Example
// ```lowlight
// let ages = #["alice": 30]
// ages.insert("bob", 25)
// assert(ages.get("alice") == 30)
// ```
@lang("map")
define Map[K: Hashable, V] struct [
	// Bucket index table mapping hashes to entry indices (`-1` empty, `-2` tombstone).
	buckets: ptr[int],
	// Allocated bucket table size (power of two).
	buck_cap: int,
	// Dense entry array holding key-value slots and cached hashes.
	entries: ptr[MapSlot[K, V]],
	// Allocated capacity of `entries`.
	ent_cap: int,
	// High-water mark of used/tombstoned slots in `entries`.
	ent_len: int,
	// Number of live entries in the map.
	len: int,
]

// Constructs an empty [`Map`] with zero heap allocation.
const Map[K, V].new = fn() Map[K, V]:
	Map[K, V][
		buckets: unsafe null_ptr[int](),
		buck_cap: 0,
		entries: unsafe null_ptr[MapSlot[K, V]](),
		ent_cap: 0,
		ent_len: 0,
		len: 0,
	]

// Constructs an empty [`Map`] pre-sized for at least `cap` key-value entries.
const Map[K, V].with_capacity = fn(cap: int) Map[K, V]: {
	if(cap <= 0): func <- Map[K, V].new()
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
	let e = unsafe alloc_raw[MapSlot[K, V]](cap)
	Map[K, V][
		buckets: b,
		buck_cap: bcap,
		entries: e,
		ent_cap: cap,
		ent_len: 0,
		len: 0,
	]
}

// Returns the number of live key-value entries currently in the map.
const Map[K, V].len = fn(self) int: self.len

// Reports whether the map contains zero entries.
const Map[K, V].is_empty = fn(self) bool: self.len == 0

// Internal probe lookup returning the entry index of `needle` with hash `h`, or `-1`.
const Map[K, V].find_entry = fn(self, needle: K, h: u64) int: {
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

// Reports whether `key` exists in the map.
const Map[K, V].has = fn(self, key: K) bool: {
	let h = key.hash()
	self.find_entry(key, h) >= 0
}

// Reports whether `key` exists in the map (satisfies [`Containable`]).
const Map[K, V].contains = fn(self, key: K) bool: self.has(key)

// Returns a copy of the value mapped to `key`, or `none` if `key` is absent.
const Map[K, V].get = fn(self, key: K) ?V: {
	let h = key.hash()
	let idx = self.find_entry(key, h)
	if(idx < 0): func <- none
	unsafe self.entries[idx].val.copy()
}

// Internal helper that compacts tombstoned slots and rebuilds the bucket table at `new_bcap`.
const Map[K, V].rehash = fn(&self, new_bcap: int): {
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
							type.size_of[MapSlot[K, V]](),
						)
					w += 1
				}
			}
			r += 1
		}
		if(w < self.ent_len): {
			unsafe zero_bytes(
				ptr_cast[u8](ptr_add(self.entries, w)),
				(self.ent_len - w) * type.size_of[MapSlot[K, V]](),
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

// Inserts `key: val` or overwrites the existing value if `key` is already present.
//
// # Ownership
// Consumes ownership of both `key` and `val`.
const Map[K, V].insert = fn(&self, key: own K, val: own V): {
	let h = key.hash()
	let existing = self.find_entry(key, h)
	if(existing >= 0): {
		// Writing through a raw pointer does not drop what it overwrites, so drop the old value first.
		unsafe {
			let vp = addr_of(self.entries[existing].val)
			let _ = ptr_read(vp)
			ptr_write(vp, own val)
		}
		func <-
	}

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
				alloc_raw[MapSlot[K, V]](ncap)
			} else: {
				let p = realloc_raw(self.entries, ncap)
				zero_bytes(
					ptr_cast[u8](ptr_add(p, old_cap)),
					(ncap - old_cap) * type.size_of[MapSlot[K, V]](),
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
	unsafe ptr_write(ptr_add(self.entries, idx), MapSlot[K, V][alive: true, hash: h, key: own key, val: own val])
	let mask = self.buck_cap - 1
	let slot = int(h & u64(mask))
	for(unsafe self.buckets[slot] >= 0): slot = slot + 1 & mask
	unsafe {
		self.buckets[slot] = idx
	}
	self.len += 1
}

// Inserts a [`MapEntry`] pair into the map (satisfies [`Collectable`] for map comprehensions).
// The key and value are moved out of `entry`, not copied.
const Map[K, V].push = fn(&self, entry: own MapEntry[K, V]): {
	let key = unsafe ptr_read(addr_of(entry.key))
	let val = unsafe ptr_read(addr_of(entry.value))
	// All-zero bytes are the moved-from state: `entry` now drops as a no-op.
	unsafe zero_bytes(ptr_cast[u8](addr_of(entry)), type.size_of[MapEntry[K, V]]())
	self.insert(own key, own val)
}

// Removes `key` and returns the value it mapped to, or `none` if `key` is absent.
const Map[K, V].remove = fn(&self, key: K) ?V: {
	if(self.buck_cap == 0 or unsafe is_null(self.buckets)): func <- none
	let h = key.hash()
	let mask = self.buck_cap - 1
	let slot = int(h & u64(mask))
	for(true): {
		let idx = unsafe self.buckets[slot]
		if(idx == -1): func <- none
		if(idx >= 0):
			unsafe {
				if(self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == key): {
					// Move the value out and drop the key, so `V` need not be copyable.
					let out = ptr_read(addr_of(self.entries[idx].val))
					let _ = ptr_read(addr_of(self.entries[idx].key))
					zero_bytes(ptr_cast[u8](ptr_add(self.entries, idx)), type.size_of[MapSlot[K, V]]())
					self.buckets[slot] = -2
					self.len -= 1
					func <- out
				}
			}
		slot = slot + 1 & mask
	}
	none
}

// Drops all stored entries and resets the map to empty while retaining allocated capacity.
const Map[K, V].clear = fn(&self): {
	if(not unsafe is_null(self.entries)): {
		let i = 0
		for(i < self.ent_len): {
			unsafe {
				if(self.entries[i].alive): {
					let _ = ptr_read(ptr_add(self.entries, i))
				}
				zero_bytes(ptr_cast[u8](ptr_add(self.entries, i)), type.size_of[MapSlot[K, V]]())
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

// Returns a copy of the value mapped to `key`, first inserting `val` for it if `key` is absent.
// `val` is dropped when `key` is already present.
//
// ```lowlight
// let port = settings.get_or_insert("port", 8080)
// ```
const Map[K, V].get_or_insert = fn(&self, key: own K, val: own V) V: {
	let idx = self.find_entry(key, key.hash())
	if(idx >= 0): func <- unsafe self.entries[idx].val.copy()
	let out = val.copy()
	self.insert(own key, own val)
	out
}

// Runs `f` on the value mapped to `key`, in place, and reports whether `key` was present. The
// value isn't copied, so this works for values that can't be.
//
// ```lowlight
// counts.update(word, cl(n: &int): { n += 1 })
// groups.update(key, cl(names: &[string]): names.push(name.copy()))
// ```
const Map[K, V].update = fn(&self, key: K, f: cl(&V)) bool: {
	let idx = self.find_entry(key, key.hash())
	if(idx < 0): func <- false
	unsafe f(&self.entries[idx].val)
	true
}

// Lends the value mapped to `key` to `f`, read-only and without copying it, and returns what `f`
// returns, or `none` if `key` is absent. Use it to read values that can't be copied, which `m[k]`
// and `get` would have to copy.
//
// ```lowlight
// let user = sessions.with(id, cl(s: Session) string: s.user.copy()) ?? "nobody"
// ```
const Map[K, V].with[R] = fn(self, key: K, f: cl(V) R) ?R: {
	let idx = self.find_entry(key, key.hash())
	if(idx < 0): func <- none
	unsafe f(self.entries[idx].val)
}

// Returns a new [`List`] of [`MapEntry`] copies of every key-value pair, in insertion order.
const Map[K, V].entries = fn(self) List[MapEntry[K, V]]: {
	let out = List[MapEntry[K, V]].with_capacity(self.len)
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive): out.push(MapEntry[K, V][
				key: self.entries[i].key.copy(),
				value: self.entries[i].val.copy(),
			])
		}
		i += 1
	}
	out
}

// Returns a new [`List`] containing copies of all keys in insertion order.
const Map[K, V].keys = fn(self) List[K]: {
	let out = List[K].with_capacity(self.len)
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive): out.push(self.entries[i].key.copy())
		}
		i += 1
	}
	out
}

// Returns a new [`List`] containing copies of all values in insertion order.
const Map[K, V].values = fn(self) List[V]: {
	let out = List[V].with_capacity(self.len)
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive): out.push(self.entries[i].val.copy())
		}
		i += 1
	}
	out
}

// Returns a deep copy of the map with copies of all keys and values.
const Map[K, V].copy = fn(self) Map[K, V]: {
	let out = Map[K, V].with_capacity(self.len)
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive):
				out.insert(self.entries[i].key.copy(), self.entries[i].val.copy())
		}
		i += 1
	}
	out
}

// Formats the map as `"#[k1: v1, k2: v2, ...]"`.
const Map[K, V].to_string = fn(self) string: {
	let out = "#["
	let first = true
	let i = 0
	for(i < self.ent_len): {
		unsafe {
			if(self.entries[i].alive): {
				if(not first): out.push(", ")
				out.push(self.entries[i].key.to_string())
				out.push(": ")
				out.push(self.entries[i].val.to_string())
				first = false
			}
		}
		i += 1
	}
	out.push("]")
	out
}

// Stateful forward iterator yielding copied [`MapEntry`] pairs from a [`Map`].
define MapIterator[K: Hashable, V] struct [
	// Pointer to the backing map slot array.
	entries: ptr[MapSlot[K, V]],
	// Number of entry slots to scan.
	ent_len: int,
	// Next slot index to inspect.
	idx: int,
]

// Advances the iterator and returns a copy of the next live [`MapEntry`], or `none` when finished.
const MapIterator[K, V].next = fn(&self) ?MapEntry[K, V]: {
	for(self.idx < self.ent_len): {
		let i = self.idx
		self.idx += 1
		unsafe {
			if(self.entries[i].alive):
				func <- MapEntry[K, V][
					key: self.entries[i].key.copy(),
					value: self.entries[i].val.copy(),
				]
		}
	}
	none
}

// Returns a forward [`Iterator`] yielding [`MapEntry`] copies in insertion order.
const Map[K, V].iter = fn(self) Iterator[MapEntry[K, V]]:
	MapIterator[K, V][
		entries: self.entries,
		ent_len: self.ent_len,
		idx: 0,
	]

// Drops all remaining keys/values and frees the entry and bucket tables.
const Map[K, V].drop = fn(&self): {
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
	self.entries = unsafe null_ptr[MapSlot[K, V]]()
	self.buckets = unsafe null_ptr[int]()
	self.buck_cap = 0
	self.ent_cap = 0
	self.ent_len = 0
	self.len = 0
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("Map[K, V] realloc_raw growth, compaction, and clear with Resource keys and values")
bind test_map_realloc_and_slot_zeroing = fn(): {
	// Churn dirty heap blocks filled with 0xFF before Map grows via realloc_raw.
	for(c in 0..8): {
		let dirty = List[u8].filled(512, u8(255))
		assert(dirty[0] == u8(255), "dirty block populated")
	}

	let m = Map[string, List[string]].new()
	for(i in 0..64): {
		let v = List[string].new()
		v.push("val_${i}_a")
		v.push("val_${i}_b")
		m.insert("k_$i", own v)
	}
	assert(m.len() == 64, "len == 64 after inserts")

	// Overwrite every 8th key with a fresh Resource value.
	for(i in 0..8): {
		let k_idx = i * 8
		let upd = List[string].new()
		upd.push("updated_$k_idx")
		m.insert("k_$k_idx", own upd)
	}
	assert(m.len() == 64, "len still 64 after overwrites")

	// Remove half the entries, then insert 40 more to trigger rehash compaction & slot reuse.
	for(i in 0..32): {
		let even = i * 2
		let rem? = m.remove("k_$even")
		assert(rem? != none, "removed even key")
	}
	assert(m.len() == 32, "len == 32 after removes")

	for(i in 0..40): {
		let rv = List[string].new()
		rv.push("rehashed_$i")
		m.insert("rehashed_$i", own rv)
	}
	assert(m.len() == 72, "len == 72 after rehash inserts")

	for(i in 0..32): {
		let even = i * 2
		let odd = i * 2 + 1
		assert(not m.has("k_$even"), "removed key absent")
		let got? = m.get("k_$odd")
		if(got? as lst): {
			assert(lst.len() == 2 and lst[0] == "val_${odd}_a", "odd key value intact")
		} else: {
			assert(false, "missing odd key")
		}
	}
	for(i in 0..40): {
		let got? = m.get("rehashed_$i")
		if(got? as lst): {
			assert(lst.len() == 1 and lst[0] == "rehashed_$i", "rehashed key value intact")
		} else: {
			assert(false, "missing rehashed key")
		}
	}

	// Clear and re-insert into zeroed slots.
	m.clear()
	assert(m.is_empty(), "empty after clear")
	for(i in 0..20): {
		let pv = List[string].new()
		pv.push("post_$i")
		m.insert("post_clear_$i", own pv)
	}
	assert(m.len() == 20, "len == 20 after clear + reinsert")
	for(i in 0..20): {
		let got? = m.get("post_clear_$i")
		if(got? as lst): {
			assert(lst.len() == 1 and lst[0] == "post_$i", "post-clear value intact")
		} else: {
			assert(false, "missing post-clear key")
		}
	}
}

@test("Map[K, V] rehashes in place when tombstones accumulate at low load")
bind test_map_in_place_rehash_low_load = fn(): {
	let m = Map[string, string].new()
	m.insert("anchor", "stay")
	let compactions = 0
	let prev_ent_len = m.ent_len
	for(i in 0..40): {
		m.insert("temp_$i", "val_$i")
		if(m.ent_len < prev_ent_len): compactions += 1
		prev_ent_len = m.ent_len
		let rem? = m.remove("temp_$i")
		assert(rem? == "val_$i", "remove returns val_$i")
	}
	assert(m.len() == 1, "only anchor remains")
	assert(m.get("anchor") == "stay", "anchor value survived in-place compaction")
	assert(m.buck_cap == 16, "buck_cap stays 16 at low load")
	assert(compactions >= 3, "tombstones compacted in place multiple times")
	assert(m.ent_len <= 11, "ent_len bounded by load factor")
}

@test("Map[K, V] get_or_insert, update, with, entries, and push moving its entry")
bind test_map_helpers = fn(): {
	let m: #[string: int] = #["a": 1]
	assert(m.get_or_insert("a", 99) == 1, "get_or_insert returns the existing value")
	assert(m.get_or_insert("b", 2) == 2 and m["b"] == 2, "get_or_insert inserts when absent")

	assert(
		m.update(
			"a",
			fn(n: &int): {
				n += 10
			},
		),
		"update reports a present key",
	)
	assert(m["a"] == 11, "update changes the value in place")
	assert(
		not m.update(
			"zzz",
			fn(n: &int): {
				n += 1
			},
		),
		"update reports an absent key",
	)

	let groups: #[string: [string]] = #[]
	for(w in ["apple", "avocado", "banana"]): {
		let k = w[0..1]
		let _ = groups.get_or_insert(k.copy(), [])
		let item = w.copy()
		let _ = groups.update(k, cl(xs: &[string]): xs.push(item.copy()))
	}
	assert(groups.to_string() == "#[a: [apple, avocado], b: [banana]]", "grouping with update")

	let es = m.entries()
	assert(es.len() == 2 and es[0].key == "a" and es[1].value == 2, "entries in insertion order")

	// `push` moves the entry, so values that can't be copied still collect into a map.
	let handles = for(i in 0..3) #[int: _NoCopy]: MapEntry[key: i, value: _NoCopy[name: "h$i"]]
	assert(handles.len() == 3 and handles.has(2), "push moves non-copyable values in")
	assert(
		handles.update(1, fn(h: &_NoCopy): h.name.push("!")),
		"update reaches a non-copyable value",
	)
	assert(
		handles.with(1, fn(h: _NoCopy) string: h.name.copy()) == "h1!",
		"with reads a non-copyable value in place",
	)
	assert(
		handles.with(9, fn(h: _NoCopy) int: h.name.len()) == none,
		"with on an absent key is none",
	)
	assert(handles.with(0, fn(h: _NoCopy) int: h.name.len()) == 2, "with returns f's result")
	assert(handles.remove(1)?.name == "h1!", "the change stuck")
}

@test_only
define _NoCopy struct [
	name: string
]
