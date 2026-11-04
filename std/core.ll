// Lowlight Standard Library: std/core.ll
// The foundational core prelude: interfaces, primitive methods, and essential utilities.
// This module is implicitly imported into every Lowlight source file.

// -----------------------------------------------------------------------------
// Core Interfaces
// -----------------------------------------------------------------------------

// Printable: Universal interface for types that can produce a human-readable string.
@lang("printable")
define Printable interface [
    to_string: (self): string,
]

// Copyable: Universal interface for types that can explicitly duplicate their state.
@lang("copyable")
define Copyable interface [
    copy: (self): Self,
]

// Drop: Universal interface for types requiring custom deterministic cleanup when falling out of scope.
@lang("drop")
define Drop interface [
    drop: (&self),
]

// Hashable: Universal interface for types that can produce a 64-bit hash code.
@lang("hashable")
define Hashable interface [
    hash: (self): u64,
]

// Equatable: Universal interface for types supporting equivalence relation checking (== and !=).
@lang("equatable")
define Equatable interface [
    eq: (self, other: Self): bool,
]

// Comparable: Universal interface for types supporting total ordering (<, <=, >, >=).
// Returns a negative integer if self < other, 0 if self == other, and a positive integer if self > other.
@lang("comparable")
define Comparable interface [
    cmp: (self, other: Self): int,
]

// Iterator: Universal interface for pulling values sequentially.
@lang("iterator")
define Iterator[T] interface [
    next: (&self): ?T,
]

// Iterable: Universal interface for types that can produce an iterator.
@lang("iterable")
define Iterable[T] interface [
    iter: (self): Iterator[T],
]

// Collectable: Universal interface for types that can accumulate items into a collection.
@lang("collectable")
define Collectable[T] interface [
    push: (&self, item: own T),
]

// MapEntry: Key-value pair for uniform map iteration and collection.
define MapEntry[K: Hashable, V] struct [
    key: K,
    value: V,
]

// -----------------------------------------------------------------------------
// Primitive String Conversions (Printable Implementations)
// -----------------------------------------------------------------------------

extern ll_fmt_float_prec: (v: float, prec: int): string

// Converts an integer to its decimal string representation in 100% pure Lowlight.
bind int.to_string = (self): string {
    if (self == 0) func <- "0"
    if (self == -9223372036854775807 - 1) {
        func <- "-9223372036854775808"
    }
    let is_neg = self < 0
    let n = if (is_neg) -self else self
    let reversed = ""
    for (n > 0) {
        let digit = n % 10
        let b = u8(48 + digit)
        reversed.push_byte(b)
        n /= 10
    }
    let out = if (is_neg) "-" else ""
    let i = reversed.len() - 1
    for (i >= 0) {
        out.push_byte(reversed.byte_at(i))
        i -= 1
    }
    out
}

// Converts a boolean to "true" or "false".
bind bool.to_string = (self): string {
    if (self) "true" else "false"
}

// String itself satisfies Printable by returning a copy of itself.
bind string.to_string = (self): string {
    self.copy()
}

// Converts a unicode character code point into a single-character string.
bind char.to_string = (self): string {
    let s = ""
    s.push_char(self)
    s
}

// Converts a 64-bit floating point number to its standard decimal string representation.
bind float.to_string = (self): string {
    unsafe ll_fmt_float_prec(self, -1)
}

// Fixed-width integer conversions to decimal strings.
bind u8.to_string = (self): string {
    int(self).to_string()
}

bind u16.to_string = (self): string {
    int(self).to_string()
}

bind u32.to_string = (self): string {
    int(self).to_string()
}

bind u64.to_string = (self): string {
    if (self == 0) func <- "0"
    let n = self
    let reversed = ""
    for (n > 0) {
        let digit = n % 10
        let b = u8(48 + digit)
        reversed.push_byte(b)
        n /= 10
    }
    let out = ""
    let i = reversed.len() - 1
    for (i >= 0) {
        out.push_byte(reversed.byte_at(i))
        i -= 1
    }
    out
}

bind i8.to_string = (self): string {
    int(self).to_string()
}

bind i16.to_string = (self): string {
    int(self).to_string()
}

bind i32.to_string = (self): string {
    int(self).to_string()
}

// -----------------------------------------------------------------------------
// Primitive Hash Code Implementations (Hashable Implementations)
// -----------------------------------------------------------------------------

// SplitMix64 64-bit integer hash mixer implemented in pure Lowlight.
bind hash_mix_u64 = (x: u64): u64 {
    let m1: u64 = 0xbf58476d1ce4e5b9
    let m2: u64 = 0x94d049bb133111eb
    let v: u64 = (x ^ (x >> 30)) * m1
    v = (v ^ (v >> 27)) * m2
    v ^ (v >> 31)
}

bind u64.hash = (self): u64 {
    hash_mix_u64(self)
}

bind u32.hash = (self): u64 {
    hash_mix_u64(u64(self))
}

bind u16.hash = (self): u64 {
    hash_mix_u64(u64(self))
}

bind u8.hash = (self): u64 {
    hash_mix_u64(u64(self))
}

bind int.hash = (self): u64 {
    hash_mix_u64(u64(self))
}

bind i32.hash = (self): u64 {
    hash_mix_u64(u64(self))
}

bind i16.hash = (self): u64 {
    hash_mix_u64(u64(self))
}

bind i8.hash = (self): u64 {
    hash_mix_u64(u64(self))
}

bind bool.hash = (self): u64 {
    hash_mix_u64(if (self) u64(1) else u64(0))
}

bind char.hash = (self): u64 {
    hash_mix_u64(u64(self))
}

bind float.hash = (self): u64 {
    let f = if (self == 0.0) 0.0 else self
    let u: u64 = unsafe {
        let p = addr_of(f)
        let up = ptr_cast[u64](p)
        *up
    }
    hash_mix_u64(u)
}

bind string.hash = (self): u64 {
    let h: u64 = 14695981039346656037
    let prime: u64 = 1099511628211
    let n = self.len()
    let i = 0
    for (i < n) {
        let b = u64(self[i])
        h = (h ^ b) * prime
        i += 1
    }
    h
}

// -----------------------------------------------------------------------------
// Equatable & Comparable Implementations on Primitives
// -----------------------------------------------------------------------------

bind bool.eq = (self, other: bool): bool { self == other }

bind int.eq = (self, other: int): bool { self == other }
bind int.cmp = (self, other: int): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind char.eq = (self, other: char): bool { self == other }
bind char.cmp = (self, other: char): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind float.eq = (self, other: float): bool { self == other }
bind float.cmp = (self, other: float): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind u8.eq = (self, other: u8): bool { self == other }
bind u8.cmp = (self, other: u8): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind u16.eq = (self, other: u16): bool { self == other }
bind u16.cmp = (self, other: u16): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind u32.eq = (self, other: u32): bool { self == other }
bind u32.cmp = (self, other: u32): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind u64.eq = (self, other: u64): bool { self == other }
bind u64.cmp = (self, other: u64): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind i8.eq = (self, other: i8): bool { self == other }
bind i8.cmp = (self, other: i8): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind i16.eq = (self, other: i16): bool { self == other }
bind i16.cmp = (self, other: i16): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind i32.eq = (self, other: i32): bool { self == other }
bind i32.cmp = (self, other: i32): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

bind string.eq = (self, other: string): bool { self == other }
bind string.cmp = (self, other: string): int {
    if (self < other) -1
    else if (self > other) 1
    else 0
}

// -----------------------------------------------------------------------------
// Core Utility Primitives
// -----------------------------------------------------------------------------

// Returns the smaller of two comparable values.
bind min[T: Comparable + Copyable] = (a: T, b: T): T {
    if (a < b) a.copy() else b.copy()
}

// Returns the larger of two comparable values.
bind max[T: Comparable + Copyable] = (a: T, b: T): T {
    if (a > b) a.copy() else b.copy()
}

// Returns the absolute value of a signed integer.
bind abs = (x: int): int {
    if (x < 0) -x else x
}

// Clamps a value within the range [low, high].
bind clamp[T: Comparable + Copyable] = (val: T, low: T, high: T): T {
    if (val < low) low.copy()
    else if (val > high) high.copy()
    else val.copy()
}

// -----------------------------------------------------------------------------
// Result[T, E] Error Handling Primitive
// -----------------------------------------------------------------------------

define Result[T, E] struct [
    value?: ?T,
    err?: ?E,
    ok_flag: bool,
]

bind Result[T, E].ok = (val: own T): Result[T, E] {
    Result[T, E][
        value?: own val,
        err?: none,
        ok_flag: true,
    ]
}

bind Result[T, E].fail = (e: own E): Result[T, E] {
    Result[T, E][
        value?: none,
        err?: own e,
        ok_flag: false,
    ]
}

bind Result[T, E].is_ok = (self): bool { self.ok_flag }

bind Result[T, E].is_err = (self): bool { not self.ok_flag }

bind Result[T, E].unwrap_or = (own self, fallback: own T): T {
    self.value? ?? own fallback
}

bind Result[T, E].unwrap = (own self): T {
    self.value? ?? panic("called Result.unwrap on an error")
}


// -----------------------------------------------------------------------------
// List[T] Dynamic Heap-Allocated Contiguous Array
// -----------------------------------------------------------------------------

@lang("list")
define List[T] struct [
    data: ptr[T],
    len: int,
    cap: int,
]

bind List[T].new = (): List[T] {
    List[T][
        data: unsafe null_ptr[T](),
        len: 0,
        cap: 0,
    ]
}

bind List[T].with_capacity = (cap: int): List[T] {
    let p = if (cap > 0) unsafe alloc_raw[T](cap) else unsafe null_ptr[T]()
    List[T][
        data: p,
        len: 0,
        cap: cap,
    ]
}

bind List[T].len = (self): int { self.len }
bind List[T].cap = (self): int { self.cap }
bind List[T].is_empty = (self): bool { self.len == 0 }

bind List[T].push = (&self, item: own T) {
    if (self.len + 1 > self.cap) {
        let ncap = if (self.cap > 0) self.cap * 2 else 4
        let nd = unsafe {
            if (self.cap == 0) alloc_raw[T](ncap)
            else realloc_raw(self.data, ncap)
        }
        self.data = nd
        self.cap = ncap
    }
    unsafe ptr_write(ptr_add(self.data, self.len), own item)
    self.len += 1
}

bind List[T].pop = (&self): ?T {
    if (self.len <= 0) func <- none
    self.len -= 1
    unsafe {
        let val = ptr_read(ptr_add(self.data, self.len))
        val
    }
}

bind List[T].get = (self, idx: int): ?T {
    if (idx < 0 or idx >= self.len) func <- none
    unsafe {
        self.data[idx].copy()
    }
}

bind List[T].clear = (&self) {
    if (self.cap > 0) {
        let i = self.len - 1
        for (i >= 0) {
            unsafe {
                let _ = ptr_read(ptr_add(self.data, i))
            }
            i -= 1
        }
    }
    self.len = 0
}

bind List[T].copy = (self): List[T] {
    let cp = List[T].with_capacity(self.len)
    let i = 0
    for (i < self.len) {
        unsafe {
            cp.push(self.data[i].copy())
        }
        i += 1
    }
    cp
}

bind List[T].to_string = (self): string {
    let out = "["
    let i = 0
    for (i < self.len) {
        if (i > 0) out.push(", ")
        unsafe {
            out.push(self.data[i].to_string())
        }
        i += 1
    }
    out.push("]")
    out
}

define ListIterator[T] struct [
    data: ptr[T],
    len: int,
    idx: int,
]

bind ListIterator[T].next = (&self): ?T {
    if (self.idx >= self.len) func <- none
    let i = self.idx
    self.idx += 1
    unsafe {
        self.data[i].copy()
    }
}

bind List[T].iter = (self): Iterator[T] {
    ListIterator[T][
        data: self.data,
        len: self.len,
        idx: 0,
    ]
}

bind List[T].drop = (&self) {
    if (self.cap > 0) {
        let i = self.len - 1
        for (i >= 0) {
            unsafe {
                let _ = ptr_read(ptr_add(self.data, i))
            }
            i -= 1
        }
        unsafe free_raw(self.data)
    }
    self.data = unsafe null_ptr[T]()
    self.len = 0
    self.cap = 0
}

// -----------------------------------------------------------------------------
// Set[T] Dynamic Hash Set
// -----------------------------------------------------------------------------

define SetEntry[T: Hashable] struct [
    alive: bool,
    hash: u64,
    key: T,
]

@lang("set")
define Set[T: Hashable] struct [
    buckets: ptr[int],
    buck_cap: int,
    entries: ptr[SetEntry[T]],
    ent_cap: int,
    ent_len: int,
    len: int,
]

bind Set[T].new = (): Set[T] {
    Set[T][
        buckets: unsafe null_ptr[int](),
        buck_cap: 0,
        entries: unsafe null_ptr[SetEntry[T]](),
        ent_cap: 0,
        ent_len: 0,
        len: 0,
    ]
}

bind Set[T].with_capacity = (cap: int): Set[T] {
    if (cap <= 0) func <- Set[T].new()
    let bcap = 8
    for (bcap < cap * 2) {
        bcap *= 2
    }
    let b = unsafe alloc_raw[int](bcap)
    let i = 0
    for (i < bcap) {
        unsafe { b[i] = -1 }
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

bind Set[T].len = (self): int { self.len }
bind Set[T].is_empty = (self): bool { self.len == 0 }

bind Set[T].find_entry = (self, needle: T, h: u64): int {
    if (self.buck_cap == 0 or unsafe is_null(self.buckets)) func <- -1
    let mask = self.buck_cap - 1
    let slot = int(h & u64(mask))
    for (true) {
        let idx = unsafe self.buckets[slot]
        if (idx == -1) func <- -1
        if (idx >= 0) {
            unsafe {
                if (self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == needle) {
                    func <- idx
                }
            }
        }
        slot = (slot + 1) & mask
    }
    -1
}

bind Set[T].has = (self, val: T): bool {
    let h = val.hash()
    self.find_entry(val, h) >= 0
}

bind Set[T].contains = (self, val: T): bool {
    self.has(val)
}

bind Set[T].rehash = (&self, new_bcap: int) {
    if (self.len < self.ent_len) {
        let w = 0
        let r = 0
        for (r < self.ent_len) {
            unsafe {
                if (self.entries[r].alive) {
                    if (w != r) {
                        copy_bytes(ptr_cast[u8](ptr_add(self.entries, w)), ptr_cast[u8](ptr_add(self.entries, r)), size_of[SetEntry[T]]())
                    }
                    w += 1
                }
            }
            r += 1
        }
        self.ent_len = w
    }
    let nb = unsafe alloc_raw[int](new_bcap)
    let bi = 0
    for (bi < new_bcap) {
        unsafe { nb[bi] = -1 }
        bi += 1
    }
    let mask = new_bcap - 1
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                let slot = int(self.entries[i].hash & u64(mask))
                for (nb[slot] >= 0) {
                    slot = (slot + 1) & mask
                }
                nb[slot] = i
            }
        }
        i += 1
    }
    unsafe {
        if (not is_null(self.buckets)) {
            free_raw(self.buckets)
        }
    }
    self.buckets = nb
    self.buck_cap = new_bcap
}

bind Set[T].insert = (&self, val: own T): bool {
    let h = val.hash()
    let existing = self.find_entry(val, h)
    if (existing >= 0) func <- false

    if (self.buck_cap == 0 or (self.ent_len + 1) * 10 >= self.buck_cap * 7) {
        let target_bcap = if (self.buck_cap > 0) self.buck_cap * 2 else 16
        self.rehash(target_bcap)
    }
    if (self.ent_len + 1 > self.ent_cap) {
        let ncap = if (self.ent_cap > 0) self.ent_cap * 2 else 8
        let ne = unsafe {
            if (self.ent_cap == 0) alloc_raw[SetEntry[T]](ncap)
            else realloc_raw(self.entries, ncap)
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
    for (unsafe self.buckets[slot] >= 0) {
        slot = (slot + 1) & mask
    }
    unsafe {
        self.buckets[slot] = idx
    }
    self.len += 1
    true
}

bind Set[T].push = (&self, item: own T) {
    let _ = self.insert(own item)
}

bind Set[T].remove = (&self, val: T): bool {
    if (self.buck_cap == 0 or unsafe is_null(self.buckets)) func <- false
    let h = val.hash()
    let mask = self.buck_cap - 1
    let slot = int(h & u64(mask))
    for (true) {
        let idx = unsafe self.buckets[slot]
        if (idx == -1) func <- false
        if (idx >= 0) {
            unsafe {
                if (self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == val) {
                    let _ = ptr_read(ptr_add(self.entries, idx))
                    self.entries[idx].alive = false
                    self.buckets[slot] = -2
                    self.len -= 1
                    func <- true
                }
            }
        }
        slot = (slot + 1) & mask
    }
    false
}

bind Set[T].clear = (&self) {
    if (not unsafe is_null(self.entries)) {
        let i = 0
        for (i < self.ent_len) {
            unsafe {
                if (self.entries[i].alive) {
                    let _ = ptr_read(ptr_add(self.entries, i))
                    self.entries[i].alive = false
                }
            }
            i += 1
        }
    }
    if (self.buck_cap > 0 and unsafe not is_null(self.buckets)) {
        let i = 0
        for (i < self.buck_cap) {
            unsafe { self.buckets[i] = -1 }
            i += 1
        }
    }
    self.ent_len = 0
    self.len = 0
}

bind Set[T].to_list = (self): List[T] {
    let out = List[T].with_capacity(self.len)
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                out.push(self.entries[i].key.copy())
            }
        }
        i += 1
    }
    out
}

bind Set[T].copy = (self): Set[T] {
    let out = Set[T].with_capacity(self.len)
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                out.insert(self.entries[i].key.copy())
            }
        }
        i += 1
    }
    out
}

bind Set[T].to_string = (self): string {
    let out = "#["
    let first = true
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                if (not first) out.push(", ")
                out.push(self.entries[i].key.to_string())
                first = false
            }
        }
        i += 1
    }
    out.push("]")
    out
}

define SetIterator[T: Hashable] struct [
    entries: ptr[SetEntry[T]],
    ent_len: int,
    idx: int,
]

bind SetIterator[T].next = (&self): ?T {
    for (self.idx < self.ent_len) {
        let i = self.idx
        self.idx += 1
        unsafe {
            if (self.entries[i].alive) {
                func <- self.entries[i].key.copy()
            }
        }
    }
    none
}

bind Set[T].iter = (self): Iterator[T] {
    SetIterator[T][
        entries: self.entries,
        ent_len: self.ent_len,
        idx: 0,
    ]
}

bind Set[T].drop = (&self) {
    if (not unsafe is_null(self.entries)) {
        let i = 0
        for (i < self.ent_len) {
            unsafe {
                if (self.entries[i].alive) {
                    let _ = ptr_read(ptr_add(self.entries, i))
                }
            }
            i += 1
        }
        unsafe free_raw(self.entries)
    }
    if (not unsafe is_null(self.buckets)) {
        unsafe free_raw(self.buckets)
    }
    self.entries = unsafe null_ptr[SetEntry[T]]()
    self.buckets = unsafe null_ptr[int]()
    self.buck_cap = 0
    self.ent_cap = 0
    self.ent_len = 0
    self.len = 0
}

// -----------------------------------------------------------------------------
// Map[K, V] Dynamic Hash Map
// -----------------------------------------------------------------------------

define MapSlot[K: Hashable, V] struct [
    alive: bool,
    hash: u64,
    key: K,
    val: V,
]

@lang("map")
define Map[K: Hashable, V] struct [
    buckets: ptr[int],
    buck_cap: int,
    entries: ptr[MapSlot[K, V]],
    ent_cap: int,
    ent_len: int,
    len: int,
]

bind Map[K, V].new = (): Map[K, V] {
    Map[K, V][
        buckets: unsafe null_ptr[int](),
        buck_cap: 0,
        entries: unsafe null_ptr[MapSlot[K, V]](),
        ent_cap: 0,
        ent_len: 0,
        len: 0,
    ]
}

bind Map[K, V].with_capacity = (cap: int): Map[K, V] {
    if (cap <= 0) func <- Map[K, V].new()
    let bcap = 8
    for (bcap < cap * 2) {
        bcap *= 2
    }
    let b = unsafe alloc_raw[int](bcap)
    let i = 0
    for (i < bcap) {
        unsafe { b[i] = -1 }
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

bind Map[K, V].len = (self): int { self.len }
bind Map[K, V].is_empty = (self): bool { self.len == 0 }

bind Map[K, V].find_entry = (self, needle: K, h: u64): int {
    if (self.buck_cap == 0 or unsafe is_null(self.buckets)) func <- -1
    let mask = self.buck_cap - 1
    let slot = int(h & u64(mask))
    for (true) {
        let idx = unsafe self.buckets[slot]
        if (idx == -1) func <- -1
        if (idx >= 0) {
            unsafe {
                if (self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == needle) {
                    func <- idx
                }
            }
        }
        slot = (slot + 1) & mask
    }
    -1
}

bind Map[K, V].has = (self, key: K): bool {
    let h = key.hash()
    self.find_entry(key, h) >= 0
}

bind Map[K, V].contains = (self, key: K): bool {
    self.has(key)
}

bind Map[K, V].get = (self, key: K): ?V {
    let h = key.hash()
    let idx = self.find_entry(key, h)
    if (idx < 0) func <- none
    unsafe {
        self.entries[idx].val.copy()
    }
}

bind Map[K, V].rehash = (&self, new_bcap: int) {
    if (self.len < self.ent_len) {
        let w = 0
        let r = 0
        for (r < self.ent_len) {
            unsafe {
                if (self.entries[r].alive) {
                    if (w != r) {
                        copy_bytes(ptr_cast[u8](ptr_add(self.entries, w)), ptr_cast[u8](ptr_add(self.entries, r)), size_of[MapSlot[K, V]]())
                    }
                    w += 1
                }
            }
            r += 1
        }
        self.ent_len = w
    }
    let nb = unsafe alloc_raw[int](new_bcap)
    let bi = 0
    for (bi < new_bcap) {
        unsafe { nb[bi] = -1 }
        bi += 1
    }
    let mask = new_bcap - 1
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                let slot = int(self.entries[i].hash & u64(mask))
                for (nb[slot] >= 0) {
                    slot = (slot + 1) & mask
                }
                nb[slot] = i
            }
        }
        i += 1
    }
    unsafe {
        if (not is_null(self.buckets)) {
            free_raw(self.buckets)
        }
    }
    self.buckets = nb
    self.buck_cap = new_bcap
}

bind Map[K, V].insert = (&self, key: own K, val: own V) {
    let h = key.hash()
    let existing = self.find_entry(key, h)
    if (existing >= 0) {
        unsafe {
            self.entries[existing].val = own val
        }
        func <-
    }

    if (self.buck_cap == 0 or (self.ent_len + 1) * 10 >= self.buck_cap * 7) {
        let target_bcap = if (self.buck_cap > 0) self.buck_cap * 2 else 16
        self.rehash(target_bcap)
    }
    if (self.ent_len + 1 > self.ent_cap) {
        let ncap = if (self.ent_cap > 0) self.ent_cap * 2 else 8
        let ne = unsafe {
            if (self.ent_cap == 0) alloc_raw[MapSlot[K, V]](ncap)
            else realloc_raw(self.entries, ncap)
        }
        self.entries = ne
        self.ent_cap = ncap
    }
    let idx = self.ent_len
    self.ent_len += 1
    unsafe {
        self.entries[idx].alive = true
        self.entries[idx].hash = h
        self.entries[idx].key = own key
        self.entries[idx].val = own val
    }
    let mask = self.buck_cap - 1
    let slot = int(h & u64(mask))
    for (unsafe self.buckets[slot] >= 0) {
        slot = (slot + 1) & mask
    }
    unsafe {
        self.buckets[slot] = idx
    }
    self.len += 1
}

bind Map[K, V].push = (&self, entry: own MapEntry[K, V]) {
    self.insert(entry.key.copy(), entry.value.copy())
}

bind Map[K, V].remove = (&self, key: K): ?V {
    if (self.buck_cap == 0 or unsafe is_null(self.buckets)) func <- none
    let h = key.hash()
    let mask = self.buck_cap - 1
    let slot = int(h & u64(mask))
    for (true) {
        let idx = unsafe self.buckets[slot]
        if (idx == -1) func <- none
        if (idx >= 0) {
            unsafe {
                if (self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == key) {
                    let out = self.entries[idx].val.copy()
                    let _ = ptr_read(ptr_add(self.entries, idx))
                    self.entries[idx].alive = false
                    self.buckets[slot] = -2
                    self.len -= 1
                    func <- out
                }
            }
        }
        slot = (slot + 1) & mask
    }
    none
}

bind Map[K, V].clear = (&self) {
    if (not unsafe is_null(self.entries)) {
        let i = 0
        for (i < self.ent_len) {
            unsafe {
                if (self.entries[i].alive) {
                    let _ = ptr_read(ptr_add(self.entries, i))
                    self.entries[i].alive = false
                }
            }
            i += 1
        }
    }
    if (self.buck_cap > 0 and unsafe not is_null(self.buckets)) {
        let i = 0
        for (i < self.buck_cap) {
            unsafe { self.buckets[i] = -1 }
            i += 1
        }
    }
    self.ent_len = 0
    self.len = 0
}

bind Map[K, V].keys = (self): List[K] {
    let out = List[K].with_capacity(self.len)
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                out.push(self.entries[i].key.copy())
            }
        }
        i += 1
    }
    out
}

bind Map[K, V].values = (self): List[V] {
    let out = List[V].with_capacity(self.len)
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                out.push(self.entries[i].val.copy())
            }
        }
        i += 1
    }
    out
}

bind Map[K, V].copy = (self): Map[K, V] {
    let out = Map[K, V].with_capacity(self.len)
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                out.insert(self.entries[i].key.copy(), self.entries[i].val.copy())
            }
        }
        i += 1
    }
    out
}

bind Map[K, V].to_string = (self): string {
    let out = "#["
    let first = true
    let i = 0
    for (i < self.ent_len) {
        unsafe {
            if (self.entries[i].alive) {
                if (not first) out.push(", ")
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

define MapIterator[K: Hashable, V] struct [
    entries: ptr[MapSlot[K, V]],
    ent_len: int,
    idx: int,
]

bind MapIterator[K, V].next = (&self): ?MapEntry[K, V] {
    for (self.idx < self.ent_len) {
        let i = self.idx
        self.idx += 1
        unsafe {
            if (self.entries[i].alive) {
                func <- MapEntry[K, V][
                    key: self.entries[i].key.copy(),
                    value: self.entries[i].val.copy(),
                ]
            }
        }
    }
    none
}

bind Map[K, V].iter = (self): Iterator[MapEntry[K, V]] {
    MapIterator[K, V][
        entries: self.entries,
        ent_len: self.ent_len,
        idx: 0,
    ]
}

bind Map[K, V].drop = (&self) {
    if (not unsafe is_null(self.entries)) {
        let i = 0
        for (i < self.ent_len) {
            unsafe {
                if (self.entries[i].alive) {
                    let _ = ptr_read(ptr_add(self.entries, i))
                }
            }
            i += 1
        }
        unsafe free_raw(self.entries)
    }
    if (not unsafe is_null(self.buckets)) {
        unsafe free_raw(self.buckets)
    }
    self.entries = unsafe null_ptr[MapSlot[K, V]]()
    self.buckets = unsafe null_ptr[int]()
    self.buck_cap = 0
    self.ent_cap = 0
    self.ent_len = 0
    self.len = 0
}
