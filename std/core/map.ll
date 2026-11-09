// =============================================================================
// Lowlight Core Map[K, V] Dynamic Hash Map
// =============================================================================

import "./interfaces.ll"
import "./hash.ll"
import "./list.ll"

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

bind Map[K, V].new = (): Map[K, V] -> {
    Map[K, V][
        buckets: unsafe null_ptr[int](),
        buck_cap: 0,
        entries: unsafe null_ptr[MapSlot[K, V]](),
        ent_cap: 0,
        ent_len: 0,
        len: 0,
    ]
}

bind Map[K, V].with_capacity = (cap: int): Map[K, V] -> {
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

bind Map[K, V].len = (self): int -> { self.len }
bind Map[K, V].is_empty = (self): bool -> { self.len == 0 }

bind Map[K, V].find_entry = (self, needle: K, h: u64): int -> {
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

bind Map[K, V].has = (self, key: K): bool -> {
    let h = key.hash()
    self.find_entry(key, h) >= 0
}

bind Map[K, V].contains = (self, key: K): bool -> {
    self.has(key)
}

bind Map[K, V].get = (self, key: K): ?V -> {
    let h = key.hash()
    let idx = self.find_entry(key, h)
    if (idx < 0) func <- none
    unsafe {
        self.entries[idx].val.copy()
    }
}

bind Map[K, V].rehash = (&self, new_bcap: int) -> {
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

bind Map[K, V].insert = (&self, key: own K, val: own V) -> {
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

bind Map[K, V].push = (&self, entry: own MapEntry[K, V]) -> {
    self.insert(entry.key.copy(), entry.value.copy())
}

bind Map[K, V].remove = (&self, key: K): ?V -> {
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

bind Map[K, V].clear = (&self) -> {
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

bind Map[K, V].keys = (self): List[K] -> {
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

bind Map[K, V].values = (self): List[V] -> {
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

bind Map[K, V].copy = (self): Map[K, V] -> {
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

bind Map[K, V].to_string = (self): string -> {
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

bind MapIterator[K, V].next = (&self): ?MapEntry[K, V] -> {
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

bind Map[K, V].iter = (self): Iterator[MapEntry[K, V]] -> {
    MapIterator[K, V][
        entries: self.entries,
        ent_len: self.ent_len,
        idx: 0,
    ]
}

bind Map[K, V].drop = (&self) -> {
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
