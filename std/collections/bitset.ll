// A set of non-negative integers stored as one bit each, for dense sets of small numbers (ids,
// indexes, flags). It grows to fit the largest member.
//
// # Example
// ```lowlight
// let seen = collections.BitSet.new()
// assert(seen.insert(3))
// assert(not seen.insert(3))
// assert(seen.contains(3) and not seen.contains(4))
// ```
define BitSet struct [
	_words: [u64],
	_count: int,
]

// Constructs an empty [`BitSet`].
bind BitSet.new = fn() BitSet: BitSet[_words: [], _count: 0]

// Constructs an empty [`BitSet`] with room for members below `bits` before it grows.
bind BitSet.with_capacity = fn(bits: int) BitSet: {
	let words: [u64] = []
	words.resize((max(bits, 0) + 63) / 64, u64(0))
	BitSet[_words: own words, _count: 0]
}

// Returns the number of members.
bind BitSet.len = fn(self) int: self._count

// Reports whether the set has no members.
bind BitSet.is_empty = fn(self) bool: self._count == 0

// The mask selecting bit `i` within its word.
bind _bit = fn(i: int) u64: u64(1) << u64(i % 64)

// Reports whether `i` is a member. Negative numbers never are.
bind BitSet.contains = fn(self, i: int) bool:
	if(i < 0 or i / 64 >= self._words.len()): false else: (self._words[i / 64] & _bit(i)) != u64(0)

// Adds `i`, returning `true` if it wasn't already a member.
//
// # Panics
// If `i` is negative (attributed to the caller).
@track_caller
bind BitSet.insert = fn(&self, i: int) bool: {
	if(i < 0): panic("BitSet.insert: negative member $i")
	if(self.contains(i)): func <- false
	if(i / 64 >= self._words.len()): self._words.resize(i / 64 + 1, u64(0))
	self._words[i / 64] = self._words[i / 64] | _bit(i)
	self._count += 1
	true
}

// Removes `i`, returning `true` if it was a member.
bind BitSet.remove = fn(&self, i: int) bool: {
	if(not self.contains(i)): func <- false
	self._words[i / 64] = self._words[i / 64] & ~_bit(i)
	self._count -= 1
	true
}

// Removes every member, keeping the allocated words.
bind BitSet.clear = fn(&self): {
	for(w in 0..self._words.len()): self._words[w] = u64(0)
	self._count = 0
}

// The number of set bits in `w`.
bind _popcount = fn(w: u64) int: {
	let n = 0
	let rest = w
	while(rest != u64(0)): {
		rest = rest & rest - u64(1)
		n += 1
	}
	n
}

// Builds a [`BitSet`] from `words`, counting its members and dropping trailing empty words.
bind _from_words = fn(words: own [u64]) BitSet: {
	while(words.len() > 0 and words[words.len() - 1] == u64(0)): {
		let _ = words.pop()
	}
	let count = 0
	for(w in words): count += _popcount(w)
	BitSet[_words: own words, _count: count]
}

// The word at index `w`, or `0` past the end.
bind BitSet._word = fn(self, w: int) u64:
	if(w < self._words.len()): self._words[w] else: u64(0)

// A new set holding every number in `self`, `other`, or both.
bind BitSet.union = fn(self, other: BitSet) BitSet: _from_words(
	for(w in 0..max(self._words.len(), other._words.len())) [u64]: self._word(w) | other._word(w)
)

// A new set holding the numbers in both `self` and `other`.
bind BitSet.intersection = fn(self, other: BitSet) BitSet: _from_words(
	for(w in 0..min(self._words.len(), other._words.len())) [u64]: self._word(w) & other._word(w)
)

// A new set holding the numbers in `self` that aren't in `other`.
bind BitSet.difference = fn(self, other: BitSet) BitSet: _from_words(
	for(w in 0..self._words.len()) [u64]: self._word(w) & ~other._word(w)
)

// Reports whether every member of `self` is also in `other`.
bind BitSet.is_subset = fn(self, other: BitSet) bool: {
	for(w in 0..self._words.len()): if((self._words[w] & ~other._word(w)) != u64(0)): func <- false
	true
}

// Returns the members in ascending order.
bind BitSet.to_list = fn(self) [int]: {
	let out: [int] = []
	for(w in 0..self._words.len()): {
		let word = self._words[w]
		if(word == u64(0)): next <-
		for(b in 0..64): if((word & u64(1) << u64(b)) != u64(0)): out.push(w * 64 + b)
	}
	out
}

// Returns an iterator over the members in ascending order.
bind BitSet.iter = fn(self) Iterator[int]: self.to_list().iter()

// Returns a copy of the set.
bind BitSet.copy = fn(self) BitSet: BitSet[_words: self._words.copy(), _count: self._count]

// Reports whether `self` and `other` have the same members.
bind BitSet.eq = fn(self, other: BitSet) bool: self._count == other._count and self.is_subset(other)

// Formats the set as `BitSet[1, 5, 64]`, in ascending order.
bind BitSet.to_string = fn(self) string: "BitSet" + self.to_list().to_string()

// Adds `i` (satisfies [`Collectable`], for comprehensions).
bind BitSet.push = fn(&self, item: own int): {
	let _ = self.insert(item)
}

@test("BitSet membership, algebra, and growth")
bind test_bitset = fn(): {
	let s = BitSet.new()
	assert(s.insert(3) and s.insert(64) and s.insert(200), "inserts")
	assert(not s.insert(64), "inserting twice reports false")
	assert(s.len() == 3 and s.contains(200) and not s.contains(199), "membership")
	assert(not s.contains(-1) and not s.contains(100000), "out-of-range numbers aren't members")
	assert(s.to_string() == "BitSet[3, 64, 200]", "ascending order")
	assert(s.remove(64) and not s.remove(64) and s.len() == 2, "remove")

	let a = for(i in [1, 2, 3, 70]) BitSet: i
	let b = for(i in [2, 3, 4]) BitSet: i
	assert(a.union(b).to_string() == "BitSet[1, 2, 3, 4, 70]", "union")
	assert(a.intersection(b).to_string() == "BitSet[2, 3]", "intersection")
	assert(a.difference(b).to_string() == "BitSet[1, 70]", "difference")
	assert(a.intersection(b).is_subset(a) and not a.is_subset(b), "is_subset")
	assert(a.difference(b).len() == 2 and a.union(b).len() == 5, "results count their members")
	assert(a == a.copy() and a != b, "equality")
	a.clear()
	assert(a.is_empty() and not a.contains(70), "clear")
}
