// Lowlight Standard Library: std/pool.ll
// Generational handle arena Pool[T] and Handle[T] in 100% safe Lowlight.
// Solves cyclic graph / entity-component / UI tree references with O(1) slot
// reuse and automatic stale-handle detection without GC or reference counting.

define Handle[T] struct [
    index: int,
    generation: int,
]

bind Handle[T].equals = (self, other: Handle[T]): bool -> {
    self.index == other.index and self.generation == other.generation
}

define _PoolSlot[T] struct [
    value?: ?T,
    generation: int,
    alive: bool,
]

define Pool[T] struct [
    _slots: [_PoolSlot[T]],
    _free_indices: [int],
    _live_count: int,
]

bind Pool[T].new = (): Pool[T] -> {
    Pool[T][
        _slots: [],
        _free_indices: [],
        _live_count: 0,
    ]
}

bind Pool[T].len = (self): int -> { self._live_count }

bind Pool[T].capacity = (self): int -> { self._slots.len() }

bind Pool[T].alloc = (&self, val: own T): Handle[T] -> {
    if (self._free_indices.len() > 0) {
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

bind Pool[T].has = (self, h: Handle[T]): bool -> {
    if (h.index < 0 or h.index >= self._slots.len()) {
        func <- false
    }
    func <- self._slots[h.index].alive and self._slots[h.index].generation == h.generation
}

bind Pool[T].get = (self, h: Handle[T]): ?T -> {
    if (h.index < 0 or h.index >= self._slots.len()) {
        func <- none
    }
    if (not self._slots[h.index].alive or self._slots[h.index].generation != h.generation) {
        func <- none
    }
    if (self._slots[h.index].value? as val) {
        func <- val.copy()
    }
    func <- none
}

bind Pool[T].with[R] = (self, h: Handle[T], reader: (T): R): ?R -> {
    if (h.index < 0 or h.index >= self._slots.len()) {
        func <- none
    }
    if (not self._slots[h.index].alive or self._slots[h.index].generation != h.generation) {
        func <- none
    }
    if (self._slots[h.index].value? as val) {
        func <- reader(val)
    }
    func <- none
}

bind Pool[T].set = (&self, h: Handle[T], val: own T): bool -> {
    if (h.index < 0 or h.index >= self._slots.len()) {
        func <- false
    }
    if (not self._slots[h.index].alive or self._slots[h.index].generation != h.generation) {
        func <- false
    }
    self._slots[h.index].value? = own val
    func <- true
}

bind Pool[T].free = (&self, h: Handle[T]): bool -> {
    if (h.index < 0 or h.index >= self._slots.len()) {
        func <- false
    }
    if (not self._slots[h.index].alive or self._slots[h.index].generation != h.generation) {
        func <- false
    }
    self._slots[h.index].value? = none
    self._slots[h.index].alive = false
    self._slots[h.index].generation += 1
    self._free_indices.push(h.index)
    self._live_count -= 1
    func <- true
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("generational pool allocation, retrieval, mutation, and len")
bind test_pool_alloc_and_get = () -> {
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

    let read_len? = pool.with[int](h2, (s: string): int -> { s.len() })
    assert(read_len? == 6, "with reader returned string length")
}

@test("generational pool free, reuse, and stale handle invalidation")
bind test_pool_free_and_reuse = () -> {
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
bind test_pool_handle_equality = () -> {
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

