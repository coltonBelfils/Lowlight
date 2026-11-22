// std/core/interfaces — Core language contract interfaces and shared data types.
//
// Defines the foundational interfaces wired to compiler hooks via `@lang(...)`,
// including [`Printable`], [`Copyable`], [`Drop`], [`Hashable`], [`Equatable`],
// [`Comparable`], [`Addable`], [`AddAssignable`], [`Iterator`], [`Iterable`], [`Collectable`], and indexing/containment
// contracts.

// Types that can format themselves as a human-readable [`string`].
//
// Required for string interpolation (`"$val"`) and `println`.
@lang("printable")
define Printable interface [
	// Returns a human-readable string representation of `self`.
	to_string: fn(self) string,
]

// Types that support explicit duplication via `.copy()`.
//
// Unlike Plain types (which are bitwise-copied implicitly), Resource types
// must satisfy [`Copyable`] to be duplicated without moving ownership.
@lang("copyable")
define Copyable interface [
	// Returns an independent duplicate of `self`.
	copy: fn(self) Self,
]

// Types with deterministic custom cleanup logic executed when a value leaves scope.
//
// # Ownership
// The compiler invokes `drop(&self)` automatically before reclaiming storage unless
// ownership was moved or the value reached its sentinel state.
@lang("drop")
define Drop interface [
	// Releases owned memory, file descriptors, or OS handles held by `self`.
	drop: fn(&self),
]

// Types capable of producing a 64-bit hash digest for use in [`Set`] and [`Map`].
@lang("hashable")
define Hashable interface [
	// Computes a 64-bit hash code for `self`. Equal values must produce equal hashes.
	hash: fn(self) u64,
]

// Types supporting structural equality comparisons (`==` and `!=`).
@lang("equatable")
define Equatable interface [
	// Reports whether `self` equals `other`.
	eq: fn(self, other: Self) bool,
]

// Types supporting total ordering (`<`, `<=`, `>`, `>=`, and sorting).
@lang("comparable")
define Comparable interface [
	// Compares `self` against `other`, returning `-1` if less, `0` if equal, or `1` if greater.
	cmp: fn(self, other: Self) int,
]

// Types supporting `+`. `a + b` calls `a.add(b)` for every type except the numeric types, whose
// `+` is built in (they implement `add` too, so generic code can require [`Addable`]).
@lang("addable")
define Addable interface [
	// Returns `self` combined with `other`; both are only read.
	add: fn(self, other: Self) Self,
]

// Types supporting `+=` in place. `a += b` calls `a.add_assign(b)`; a type that implements only
// [`Addable`] gets `a = a.add(b)` instead.
@lang("add_assignable")
define AddAssignable interface [
	// Adds `other` to `self` in place.
	add_assign: fn(&self, other: Self),
]

// Stateful stream producing a sequence of optional values of type [`T`].
@lang("iterator")
define Iterator[T] interface [
	// Advances the iterator and returns the next item, or `none` when exhausted.
	next: fn(&self) ?T,
]

// Containers that can construct a forward [`Iterator`] over their elements.
@lang("iterable")
define Iterable[T] interface [
	// Returns a new iterator positioned at the beginning of the collection.
	iter: fn(self) Iterator[T],
]

// Mutable containers that can accumulate owned elements appended during comprehension loops.
@lang("collectable")
define Collectable[T] interface [
	// Appends `item` into the collection, taking ownership of it.
	push: fn(&self, item: own T),
]

// Sequence containers indexable by non-negative integer positions.
@lang("indexable")
define Indexable[T] interface [
	// Returns the number of elements in the sequence.
	len: fn(self) int,
	// Returns a copy of the element at `idx`, or `none` if out of bounds.
	get: fn(self, idx: int) ?T,
]

// Associative containers indexable by hashable keys of type [`K`].
@lang("key_indexable")
define KeyIndexable[K, V] interface [
	// Returns the number of entries in the map.
	len: fn(self) int,
	// Reports whether `key` exists in the container.
	has: fn(self, key: K) bool,
	// Returns a copy of the value mapped to `key`, or `none` if absent.
	get: fn(self, key: K) ?V,
]

// Containers supporting `when in container` pattern matching and membership tests.
@lang("containable")
define Containable[T] interface [
	// Reports whether `item` is contained within `self`.
	contains: fn(self, item: T) bool,
]

// Key-value pair yielded while iterating over a [`Map`].
@derive(Equatable, Copyable, Printable)
define MapEntry[K: Hashable, V] struct [
	// Entry lookup key.
	key: K,
	// Entry associated value.
	value: V,
]
