// =============================================================================
// Lowlight Core Interfaces & Language Contract Definitions
// =============================================================================

@lang("printable")
define Printable interface [
    to_string: (self) string,
]

@lang("copyable")
define Copyable interface [
    copy: (self) Self,
]

@lang("drop")
define Drop interface [
    drop: (&self),
]

@lang("hashable")
define Hashable interface [
    hash: (self) u64,
]

@lang("equatable")
define Equatable interface [
    eq: (self, other: Self) bool,
]

@lang("comparable")
define Comparable interface [
    cmp: (self, other: Self) int,
]

@lang("iterator")
define Iterator[T] interface [
    next: (&self) ?T,
]

@lang("iterable")
define Iterable[T] interface [
    iter: (self) Iterator[T],
]

@lang("collectable")
define Collectable[T] interface [
    push: (&self, item: own T),
]

@lang("indexable")
define Indexable[T] interface [
    len: (self) int,
    get: (self, idx: int) ?T,
]

@lang("key_indexable")
define KeyIndexable[K, V] interface [
    len: (self) int,
    has: (self, key: K) bool,
    get: (self, key: K) ?V,
]

@lang("containable")
define Containable[T] interface [
    contains: (self, item: T) bool,
]

define MapEntry[K: Hashable, V] struct [
    key: K,
    value: V,
]
