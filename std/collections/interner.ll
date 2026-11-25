// Gives each distinct string a small integer id, so repeated strings (identifiers in a compiler,
// tags, words) can be stored and compared as `int`s. Ids count up from `0` in the order strings
// are first interned, and never change.
//
// # Example
// ```lowlight
// let names = collections.StringInterner.new()
// let a = names.intern("alpha")
// assert(names.intern("alpha") == a)
// assert(names.name_of(a) == "alpha")
// ```
define StringInterner struct [
	_ids: #[string: int],
	_names: [string],
]

// Constructs an empty [`StringInterner`].
bind StringInterner.new = fn() StringInterner: StringInterner[_ids: #[], _names: []]

// Returns the number of distinct strings interned.
bind StringInterner.len = fn(self) int: self._names.len()

// Reports whether nothing has been interned.
bind StringInterner.is_empty = fn(self) bool: self._names.is_empty()

// Returns the id of `s`, giving it the next id if it's new.
bind StringInterner.intern = fn(&self, s: string) int: {
	if(self._ids[s] as id): func <- id
	let id = self._names.len()
	self._names.push(s.copy())
	self._ids.insert(s.copy(), id)
	id
}

// Returns the id of `s` without interning it, or `none` if it hasn't been interned.
bind StringInterner.id_of = fn(self, s: string) ?int: self._ids[s]

// Reports whether `s` has been interned.
bind StringInterner.contains = fn(self, s: string) bool: self._ids.has(s)

// Returns a copy of the string with id `id`, or `none` if no string has that id.
bind StringInterner.name_of = fn(self, id: int) ?string: self._names.get(id)

// Returns copies of the interned strings, indexed by id.
bind StringInterner.names = fn(self) [string]: self._names.copy()

// Returns a copy of the interner, with the same ids.
bind StringInterner.copy = fn(self) StringInterner: StringInterner[
	_ids: self._ids.copy(),
	_names: self._names.copy(),
]

@test("StringInterner gives each string a stable id")
bind test_string_interner = fn(): {
	let n = StringInterner.new()
	let a = n.intern("alpha")
	let b = n.intern("beta")
	assert(a == 0 and b == 1, "ids count up from 0")
	assert(n.intern("alpha") == a and n.len() == 2, "interning again returns the same id")
	assert(n.id_of("beta") == b and n.id_of("gamma") == none, "id_of doesn't intern")
	assert(not n.contains("gamma") and n.len() == 2, "id_of left the interner unchanged")
	assert(n.name_of(b) == "beta" and n.name_of(9) == none and n.name_of(-1) == none, "name_of")
	assert(n.names().join(",") == "alpha,beta", "names by id")
}
