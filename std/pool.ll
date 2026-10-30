// Lowlight Standard Library: std/pool.ll
// Generational handle arena Pool[T] and Handle[T] in 100% safe Lowlight.
// Solves cyclic graph / entity-component / UI tree references with O(1) slot
// reuse and automatic stale-handle detection without GC or reference counting.

define Handle[T] struct [
    index: int,
    generation: int,
]

bind Handle[T].equals = (self, other: Handle[T]): bool {
    self.index == other.index and self.generation == other.generation
}

define PoolSlot[T] struct [
    value?: ?T,
    generation: int,
    alive: bool,
]

define Pool[T] struct [
    slots: [PoolSlot[T]],
    free_indices: [int],
    live_count: int,
]

bind Pool[T].new = (): Pool[T] {
    Pool[T][
        slots: [],
        free_indices: [],
        live_count: 0,
    ]
}

bind Pool[T].len = (self): int { self.live_count }

bind Pool[T].capacity = (self): int { self.slots.len() }

bind Pool[T].alloc = (&self, val: own T): Handle[T] {
    if (self.free_indices.len() > 0) {
        let idx = self.free_indices.pop() ?? 0
        let gen = self.slots[idx].generation
        self.slots[idx].value? = own val
        self.slots[idx].alive = true
        self.live_count += 1
        func <- Handle[T][index: idx, generation: gen]
    }
    let idx = self.slots.len()
    self.slots.push(PoolSlot[T][
        value?: own val,
        generation: 1,
        alive: true,
    ])
    self.live_count += 1
    func <- Handle[T][index: idx, generation: 1]
}

bind Pool[T].has = (self, h: Handle[T]): bool {
    if (h.index < 0 or h.index >= self.slots.len()) {
        func <- false
    }
    func <- self.slots[h.index].alive and self.slots[h.index].generation == h.generation
}

bind Pool[T].get = (self, h: Handle[T]): ?T {
    if (h.index < 0 or h.index >= self.slots.len()) {
        func <- none
    }
    if (not self.slots[h.index].alive or self.slots[h.index].generation != h.generation) {
        func <- none
    }
    if (self.slots[h.index].value? as val) {
        func <- val.copy()
    }
    func <- none
}

bind Pool[T].with[R] = (self, h: Handle[T], reader: (T): R): ?R {
    if (h.index < 0 or h.index >= self.slots.len()) {
        func <- none
    }
    if (not self.slots[h.index].alive or self.slots[h.index].generation != h.generation) {
        func <- none
    }
    if (self.slots[h.index].value? as val) {
        func <- reader(val)
    }
    func <- none
}

bind Pool[T].set = (&self, h: Handle[T], val: own T): bool {
    if (h.index < 0 or h.index >= self.slots.len()) {
        func <- false
    }
    if (not self.slots[h.index].alive or self.slots[h.index].generation != h.generation) {
        func <- false
    }
    self.slots[h.index].value? = own val
    func <- true
}

bind Pool[T].free = (&self, h: Handle[T]): bool {
    if (h.index < 0 or h.index >= self.slots.len()) {
        func <- false
    }
    if (not self.slots[h.index].alive or self.slots[h.index].generation != h.generation) {
        func <- false
    }
    self.slots[h.index].value? = none
    self.slots[h.index].alive = false
    self.slots[h.index].generation += 1
    self.free_indices.push(h.index)
    self.live_count -= 1
    func <- true
}
