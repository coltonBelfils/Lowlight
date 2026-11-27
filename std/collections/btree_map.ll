// A key and its value, as [`BTreeMap`] returns them.
@derive(Equatable, Copyable, Printable)
define Entry[K, V] struct [
	key: K,
	value: V,
]

// A B-tree node. `keys` and `vals` are sorted pairs; `kids` holds the indexes of the child nodes
// (one more than the keys), or nothing for a leaf.
@derive(Copyable)
define _BNode[K, V] struct [
	keys: [K],
	vals: [V],
	kids: [int],
]

// The B-tree's minimum degree: every node but the root holds `_T - 1` to `2 * _T - 1` keys.
bind _T = 6

// A map that keeps its keys in sorted order, for ordered iteration, the smallest and largest
// keys, and range queries. A B-tree, so lookups, inserts, and removals take O(log n).
//
// Use the prelude's `#[K: V]` when order doesn't matter: it is faster and needs `Hashable` keys
// rather than [`Comparable`] ones.
//
// # Example
// ```lowlight
// let scores = collections.BTreeMap[string, int].new()
// scores.insert("bea", 7)
// scores.insert("ada", 9)
// assert(scores.first()?.key == "ada")
// assert(scores.get("bea") == 7)
// ```
define BTreeMap[K: Comparable, V] struct [
	// Every node; children refer to each other by index. Empty until the first insert, so an
	// all-zero map (as a comprehension starts with) is a valid empty map.
	_nodes: [_BNode[K, V]],
	// Indexes of released nodes, reused before the list grows.
	_free: [int],
	_root: int,
	_len: int,
]

// Constructs an empty [`BTreeMap`].
bind BTreeMap[K, V].new = fn() BTreeMap[K, V]: BTreeMap[K, V][
	_nodes: [],
	_free: [],
	_root: 0,
	_len: 0,
]

// Returns the number of keys.
bind BTreeMap[K, V].len = fn(self) int: self._len

// Reports whether the map has no keys.
bind BTreeMap[K, V].is_empty = fn(self) bool: self._len == 0

bind BTreeMap[K, V]._is_leaf = fn(self, n: int) bool: self._nodes[n].kids.is_empty()

bind BTreeMap[K, V]._nkeys = fn(self, n: int) int: self._nodes[n].keys.len()

// The position of the first key in node `n` that isn't less than `key`.
bind BTreeMap[K, V]._lower = fn(self, n: int, key: K) int: {
	let i = 0
	while(i < self._nodes[n].keys.len() and self._nodes[n].keys[i].cmp(key) < 0): i += 1
	i
}

// Reports whether node `n` holds `key` at position `i`.
bind BTreeMap[K, V]._holds = fn(self, n: int, i: int, key: K) bool: i < self._nodes[n].keys.len() and self._nodes[n].keys[i].cmp(
	key
) == 0

// The node and position holding `key`, or `-1` for both.
bind BTreeMap[K, V]._locate = fn(self, key: K) +[int, int]: {
	if(self._nodes.is_empty()): func <- +[-1, -1]
	let n = self._root
	while(true): {
		let i = self._lower(n, key)
		if(self._holds(n, i, key)): func <- +[n, i]
		if(self._is_leaf(n)): func <- +[-1, -1]
		n = self._nodes[n].kids[i]
	}
	+[-1, -1]
}

// Stores `node`, reusing a released index if there is one, and returns its index.
bind BTreeMap[K, V]._alloc = fn(&self, node: own _BNode[K, V]) int: {
	if(self._free.pop() as i): {
		self._nodes[i] = own node
		func <- i
	}
	self._nodes.push(own node)
	self._nodes.len() - 1
}

// Empties node `n` and marks its index for reuse.
bind BTreeMap[K, V]._release = fn(&self, n: int): {
	self._nodes[n].keys.clear()
	self._nodes[n].vals.clear()
	self._nodes[n].kids.clear()
	self._free.push(n)
}

// Reports whether `key` is in the map.
bind BTreeMap[K, V].has = fn(self, key: K) bool: self._locate(key).0 >= 0

// Returns a copy of the value mapped to `key`, or `none` if `key` is absent.
bind BTreeMap[K, V].get = fn(self, key: K) ?V: {
	let at = self._locate(key)
	if(at.0 < 0): none else: self._nodes[at.0].vals[at.1].copy()
}

// Lends the value mapped to `key` to `f`, read-only and without copying it, and returns what `f`
// returns, or `none` if `key` is absent.
bind BTreeMap[K, V].with[R] = fn(self, key: K, f: cl(V) R) ?R: {
	let at = self._locate(key)
	if(at.0 < 0): none else: f(self._nodes[at.0].vals[at.1])
}

// Runs `f` on the value mapped to `key`, in place, and reports whether `key` was present.
bind BTreeMap[K, V].update = fn(&self, key: K, f: cl(&V)) bool: {
	let at = self._locate(key)
	if(at.0 < 0): func <- false
	f(&self._nodes[at.0].vals[at.1])
	true
}

// Inserts `key: val`, or replaces the value if `key` is already present.
bind BTreeMap[K, V].insert = fn(&self, key: own K, val: own V): {
	let at = self._locate(key)
	if(at.0 >= 0): {
		self._nodes[at.0].vals[at.1] = own val
		func <-
	}
	if(self._nodes.is_empty()): self._root = self._alloc(_BNode[K, V][keys: [], vals: [], kids: []])
	if(self._nkeys(self._root) == 2 * _T - 1): {
		let kids = [self._root]
		self._root = self._alloc(_BNode[K, V][keys: [], vals: [], kids: own kids])
		self._split_child(self._root, 0)
	}
	self._insert_nonfull(self._root, own key, own val)
	self._len += 1
}

// Inserts into the subtree of node `n`, which isn't full, splitting full children on the way down.
bind BTreeMap[K, V]._insert_nonfull = fn(&self, n: int, key: own K, val: own V) void: {
	let i = self._lower(n, key)
	if(self._is_leaf(n)): {
		self._nodes[n].keys.must_insert(i, own key)
		self._nodes[n].vals.must_insert(i, own val)
		func <-
	}
	if(self._nkeys(self._nodes[n].kids[i]) == 2 * _T - 1): {
		self._split_child(n, i)

		// The child's median moved up to position `i`.
		if(self._nodes[n].keys[i].cmp(key) < 0): i += 1
	}
	let child = self._nodes[n].kids[i]
	self._insert_nonfull(child, own key, own val)
}

// Splits the full child `i` of node `x` around its median, which moves up into `x`.
bind BTreeMap[K, V]._split_child = fn(&self, x: int, i: int): {
	let y = self._nodes[x].kids[i]
	let leaf = self._is_leaf(y)
	let z = self._alloc(_BNode[K, V][keys: [], vals: [], kids: []])
	for(j in 0.._T - 1): {
		let k = self._nodes[y].keys.must_remove(_T)
		let v = self._nodes[y].vals.must_remove(_T)
		self._nodes[z].keys.push(own k)
		self._nodes[z].vals.push(own v)
	}
	if(not leaf):
		for(j in 0.._T): {
			let c = self._nodes[y].kids.must_remove(_T)
			self._nodes[z].kids.push(c)
		}
	let mk = self._nodes[y].keys.must_remove(_T - 1)
	let mv = self._nodes[y].vals.must_remove(_T - 1)
	self._nodes[x].keys.must_insert(i, own mk)
	self._nodes[x].vals.must_insert(i, own mv)
	self._nodes[x].kids.must_insert(i + 1, z)
}

// Removes `key` and returns the value it mapped to, or `none` if `key` is absent.
bind BTreeMap[K, V].remove = fn(&self, key: K) ?V: {
	if(not self.has(key)): func <- none
	let out = self._remove_from(self._root, key)
	let r = self._root
	if(self._nkeys(r) == 0 and not self._is_leaf(r)): {
		self._root = self._nodes[r].kids[0]
		self._release(r)
	}
	self._len -= 1
	out
}

// Removes `key`, which is in the subtree of node `x`, and returns its value. `x` is the root or
// has at least `_T` keys, so a key can be taken from it without underflow.
bind BTreeMap[K, V]._remove_from = fn(&self, x: int, key: K) V: {
	let i = self._lower(x, key)
	if(self._holds(x, i, key)): {
		if(self._is_leaf(x)): {
			let _ = self._nodes[x].keys.must_remove(i)
			func <- self._nodes[x].vals.must_remove(i)
		}
		let y = self._nodes[x].kids[i]
		let z = self._nodes[x].kids[i + 1]
		if(self._nkeys(y) >= _T): {
			// Replace the key with its predecessor, taken from the left subtree.
			let pk = self._max_key(y)
			let pv = self._remove_from(y, pk)
			func <- self._replace_at(x, i, own pk, own pv)
		}
		if(self._nkeys(z) >= _T): {
			// Replace the key with its successor, taken from the right subtree.
			let sk = self._min_key(z)
			let sv = self._remove_from(z, sk)
			func <- self._replace_at(x, i, own sk, own sv)
		}
		self._merge(x, i)
		func <- self._remove_from(y, key)
	}

	// The key is in child `i`'s subtree; make sure that child can spare a key first.
	let c = self._nodes[x].kids[i]
	if(self._nkeys(c) < _T): {
		let n = self._nkeys(x)
		if(i > 0 and self._nkeys(self._nodes[x].kids[i - 1]) >= _T): self._rotate_right(x, i)
		else if(i < n and self._nkeys(self._nodes[x].kids[i + 1]) >= _T): self._rotate_left(x, i)
		else if(i < n): self._merge(x, i)
		else: {
			self._merge(x, i - 1)
			i -= 1
		}
	}
	let child = self._nodes[x].kids[i]
	self._remove_from(child, key)
}

// Puts `key: val` at position `i` of node `x` and returns the value that was there.
bind BTreeMap[K, V]._replace_at = fn(&self, x: int, i: int, key: own K, val: own V) V: {
	self._nodes[x].keys[i] = own key
	let old = self._nodes[x].vals.must_remove(i)
	self._nodes[x].vals.must_insert(i, own val)
	old
}

// Copies the largest key in the subtree of node `n`.
bind BTreeMap[K, V]._max_key = fn(self, n: int) K: {
	let at = n
	while(not self._is_leaf(at)): at = self._nodes[at].kids[self._nodes[at].kids.len() - 1]
	self._nodes[at].keys[self._nkeys(at) - 1].copy()
}

// Copies the smallest key in the subtree of node `n`.
bind BTreeMap[K, V]._min_key = fn(self, n: int) K: {
	let at = n
	while(not self._is_leaf(at)): at = self._nodes[at].kids[0]
	self._nodes[at].keys[0].copy()
}

// Moves the separator `i - 1` of node `x` down to the front of child `i`, and the last key of
// child `i - 1` up to take its place.
bind BTreeMap[K, V]._rotate_right = fn(&self, x: int, i: int): {
	let c = self._nodes[x].kids[i]
	let l = self._nodes[x].kids[i - 1]
	let sk = self._nodes[x].keys.must_remove(i - 1)
	let sv = self._nodes[x].vals.must_remove(i - 1)
	self._nodes[c].keys.must_insert(0, own sk)
	self._nodes[c].vals.must_insert(0, own sv)
	let last = self._nkeys(l) - 1
	let lk = self._nodes[l].keys.must_remove(last)
	let lv = self._nodes[l].vals.must_remove(last)
	self._nodes[x].keys.must_insert(i - 1, own lk)
	self._nodes[x].vals.must_insert(i - 1, own lv)
	if(not self._is_leaf(l)): {
		let lc = self._nodes[l].kids.must_remove(self._nodes[l].kids.len() - 1)
		self._nodes[c].kids.must_insert(0, lc)
	}
}

// Moves the separator `i` of node `x` down to the end of child `i`, and the first key of child
// `i + 1` up to take its place.
bind BTreeMap[K, V]._rotate_left = fn(&self, x: int, i: int): {
	let c = self._nodes[x].kids[i]
	let r = self._nodes[x].kids[i + 1]
	let sk = self._nodes[x].keys.must_remove(i)
	let sv = self._nodes[x].vals.must_remove(i)
	self._nodes[c].keys.push(own sk)
	self._nodes[c].vals.push(own sv)
	let rk = self._nodes[r].keys.must_remove(0)
	let rv = self._nodes[r].vals.must_remove(0)
	self._nodes[x].keys.must_insert(i, own rk)
	self._nodes[x].vals.must_insert(i, own rv)
	if(not self._is_leaf(r)): {
		let rc = self._nodes[r].kids.must_remove(0)
		self._nodes[c].kids.push(rc)
	}
}

// Merges child `i + 1` of node `x` and the separator `i` into child `i`.
bind BTreeMap[K, V]._merge = fn(&self, x: int, i: int): {
	let y = self._nodes[x].kids[i]
	let z = self._nodes[x].kids[i + 1]
	let sk = self._nodes[x].keys.must_remove(i)
	let sv = self._nodes[x].vals.must_remove(i)
	self._nodes[y].keys.push(own sk)
	self._nodes[y].vals.push(own sv)
	while(self._nkeys(z) > 0): {
		let k = self._nodes[z].keys.must_remove(0)
		let v = self._nodes[z].vals.must_remove(0)
		self._nodes[y].keys.push(own k)
		self._nodes[y].vals.push(own v)
	}
	while(not self._nodes[z].kids.is_empty()): {
		let c = self._nodes[z].kids.must_remove(0)
		self._nodes[y].kids.push(c)
	}
	let _ = self._nodes[x].kids.must_remove(i + 1)
	self._release(z)
}

// Appends copies of the entries of node `n`'s subtree with `lo <= key < hi`, in order. A missing
// bound doesn't limit that side.
bind BTreeMap[K, V]._collect = fn(self, n: int, lo?: ?K, hi?: ?K, out: &[Entry[K, V]]) void: {
	let leaf = self._is_leaf(n)
	let count = self._nkeys(n)
	for(i in 0..count): {
		// Child `i` holds the keys below key `i`, so it can reach `lo` only if key `i` is above it.
		let left_reaches = if(lo? as l): self._nodes[n].keys[i].cmp(l) > 0 else: true
		if(not leaf and left_reaches): self._collect(self._nodes[n].kids[i], lo?, hi?, &out)
		let below_hi = if(hi? as h): self._nodes[n].keys[i].cmp(h) < 0 else: true
		if(not below_hi): func <-
		let from_lo = if(lo? as l): self._nodes[n].keys[i].cmp(l) >= 0 else: true
		if(from_lo): out.push(Entry[K, V][
			key: self._nodes[n].keys[i].copy(),
			value: self._nodes[n].vals[i].copy(),
		])
	}
	if(not leaf): self._collect(self._nodes[n].kids[count], lo?, hi?, &out)
}

// Returns copies of every entry, in key order.
bind BTreeMap[K, V].entries = fn(self) [Entry[K, V]]: {
	let out: [Entry[K, V]] = []
	if(not self._nodes.is_empty()): self._collect(self._root, none, none, &out)
	out
}

// Returns copies of the entries with `lo <= key < hi`, in key order.
bind BTreeMap[K, V].range = fn(self, lo: K, hi: K) [Entry[K, V]]: {
	let out: [Entry[K, V]] = []
	if(not self._nodes.is_empty()): self._collect(self._root, lo.copy(), hi.copy(), &out)
	out
}

// Returns copies of the keys, in order.
bind BTreeMap[K, V].keys = fn(self) [K]: for(e in self.entries()) [K]: e.key.copy()

// Returns copies of the values, in key order.
bind BTreeMap[K, V].values = fn(self) [V]: for(e in self.entries()) [V]: e.value.copy()

// Returns a copy of the entry with the smallest key, or `none` if the map is empty.
bind BTreeMap[K, V].first = fn(self) ?Entry[K, V]: {
	if(self._len == 0): func <- none
	let n = self._root
	while(not self._is_leaf(n)): n = self._nodes[n].kids[0]
	Entry[K, V][key: self._nodes[n].keys[0].copy(), value: self._nodes[n].vals[0].copy()]
}

// Returns a copy of the entry with the largest key, or `none` if the map is empty.
bind BTreeMap[K, V].last = fn(self) ?Entry[K, V]: {
	if(self._len == 0): func <- none
	let n = self._root
	while(not self._is_leaf(n)): n = self._nodes[n].kids[self._nodes[n].kids.len() - 1]
	let i = self._nkeys(n) - 1
	Entry[K, V][key: self._nodes[n].keys[i].copy(), value: self._nodes[n].vals[i].copy()]
}

// Returns an iterator over copies of the entries, in key order.
bind BTreeMap[K, V].iter = fn(self) Iterator[Entry[K, V]]: self.entries().iter()

// Removes every entry.
bind BTreeMap[K, V].clear = fn(&self): {
	self._nodes.clear()
	self._free.clear()
	self._root = 0
	self._len = 0
}

// Returns a deep copy of the map.
bind BTreeMap[K, V].copy = fn(self) BTreeMap[K, V]: BTreeMap[K, V][
	_nodes: self._nodes.copy(),
	_free: self._free.copy(),
	_root: self._root,
	_len: self._len,
]

// Formats the map as `BTreeMap[a: 1, b: 2]`, in key order.
bind BTreeMap[K, V].to_string = fn(self) string: {
	let out = "BTreeMap["
	for(i, e in self.entries()): {
		if(i > 0): out.push(", ")
		out.push("${e.key}: ${e.value}")
	}
	out + "]"
}

// Inserts the key and value of `entry`, moving them out of it (satisfies [`Collectable`], for
// comprehensions).
bind BTreeMap[K, V].push = fn(&self, entry: own Entry[K, V]): {
	let key = unsafe ptr_read(addr_of(entry.key))
	let val = unsafe ptr_read(addr_of(entry.value))
	unsafe forget(own entry)
	self.insert(own key, own val)
}

// Checks the B-tree invariants below node `n` at `depth`, returning the leaf depth.
@test_only
bind BTreeMap[K, V]._check = fn(self, n: int, depth: int) int: {
	let count = self._nkeys(n)
	assert(count <= 2 * _T - 1, "a node holds at most 2t - 1 keys")
	assert(n == self._root or count >= _T - 1, "a non-root node holds at least t - 1 keys")
	for(i in 1..count): assert(
		self._nodes[n].keys[i - 1].cmp(self._nodes[n].keys[i]) < 0,
		"keys are sorted",
	)
	if(self._is_leaf(n)): func <- depth
	assert(self._nodes[n].kids.len() == count + 1, "an internal node has one more child than keys")
	let leaf_depth = self._check(self._nodes[n].kids[0], depth + 1)
	for(i in 1..count + 1): assert(
		self._check(self._nodes[n].kids[i], depth + 1) == leaf_depth,
		"every leaf is at the same depth",
	)
	leaf_depth
}

@test("BTreeMap keeps keys in order through inserts and removals")
bind test_btree_map = fn(): {
	let m = BTreeMap[int, int].new()
	let reference: #[int: int] = #[]

	// A fixed pseudo-random order of the numbers 0..1000.
	let seed = 12345
	let order: [int] = []
	for(i in 0..1000): order.push(i)
	for(i in 0..1000): {
		seed = (seed * 1103515245 + 12345) % 2147483648
		order.swap(i, seed % 1000)
	}
	for(k in order): {
		m.insert(k, k * 10)
		reference[k] = k * 10
	}
	assert(m.len() == 1000, "len after inserts")
	let _ = m._check(m._root, 0)
	let keys = m.keys()
	for(i in 0..1000): assert(keys[i] == i, "keys come out sorted")
	assert(m.get(437) == 4370 and m.get(1000) == none, "get")
	m.insert(437, -1)
	reference[437] = -1
	assert(m.get(437) == -1 and m.len() == 1000, "insert replaces")

	for(j in 0..700): {
		let k = order[j]
		assert(m.remove(k) == reference.remove(k), "remove returns the value")
		if(j % 97 == 0): {
			let _ = m._check(m._root, 0)
		}
	}
	assert(m.len() == 300 and m.remove(order[0]) == none, "removed keys are gone")
	let _ = m._check(m._root, 0)
	let rest = for(k in order[700..]) [int]: k
	let sorted_rest = for(e in m.entries()) [int]: e.key
	assert(sorted_rest.len() == 300, "entries after removals")
	for(k in rest): assert(m.has(k), "survivors remain")
	for(i in 1..sorted_rest.len()): assert(sorted_rest[i - 1] < sorted_rest[i], "still sorted")

	for(k in rest): {
		let _ = m.remove(k)
	}
	assert(m.is_empty() and m.first() == none, "removing everything empties the map")
	m.insert(5, 50)
	assert(m.len() == 1 and m.get(5) == 50, "the emptied tree is reusable")
}

@test("BTreeMap first, last, range, with, update, and strings")
bind test_btree_map_api = fn(): {
	let m = BTreeMap[string, int].new()
	for(w in ["pear", "apple", "fig", "kiwi", "banana", "cherry"]): m.insert(w.copy(), w.len())
	assert(m.first()?.key == "apple" and m.last()?.key == "pear", "first and last")
	let mid = for(e in m.range("b", "g")) [string]: e.key.copy()
	assert(mid.join(",") == "banana,cherry,fig", "range is lo <= key < hi")
	assert(m.range("x", "z").is_empty(), "an empty range")
	assert(
		m.with("kiwi", fn(n: int) int: n * 2) == 8 and m.with("nope", fn(n: int) int: n) == none,
		"with",
	)
	assert(
		m.update(
			"fig",
			fn(n: &int): {
				n += 100
			},
		) and m.get("fig") == 103,
		"update",
	)
	let copy = m.copy()
	let _ = m.remove("fig")
	assert(copy.has("fig") and not m.has("fig"), "copies are independent")
	assert(
		m.to_string() == "BTreeMap[apple: 5, banana: 6, cherry: 6, kiwi: 4, pear: 4]",
		"to_string in key order",
	)
	let built = for(w in ["b", "a"]) BTreeMap[string, int]: Entry[key: w.copy(), value: 1]
	assert(built.keys().join(",") == "a,b", "comprehension into a BTreeMap")
	m.clear()
	assert(m.is_empty() and m.get("apple") == none, "clear")

	// String keys and values move through splits, rotations, and merges without leaking.
	let words = BTreeMap[string, string].new()
	for(i in 0..300): words.insert("k${i * 37 % 300}", "v$i")
	let _ = words._check(words._root, 0)
	for(i in 0..290): {
		let _ = words.remove("k${i * 53 % 300}")
	}
	let _ = words._check(words._root, 0)
	assert(words.len() == 10, "ten keys survive")
	let ks = words.keys()
	for(i in 1..ks.len()): assert(ks[i - 1] < ks[i], "string keys stay sorted")
}
