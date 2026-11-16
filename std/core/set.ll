// =============================================================================
// Lowlight Core Set[T] Dynamic Hash Set
// =============================================================================

import "./interfaces.ll"
import "./hash.ll"
import "./list.ll"

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

bind Set[T].new = () Set[T]: {
    Set[T][
        buckets: unsafe null_ptr[int](),
        buck_cap: 0,
        entries: unsafe null_ptr[SetEntry[T]](),
        ent_cap: 0,
        ent_len: 0,
        len: 0,
    ]
}

bind Set[T].with_capacity = (cap: int) Set[T]: {
    if (cap <= 0): func <- Set[T].new()
    let bcap = 8
    for (bcap < cap * 2): {
        bcap *= 2
    }
    let b = unsafe alloc_raw[int](bcap)
    let i = 0
    for (i < bcap): {
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

bind Set[T].len = (self) int: { self.len }
bind Set[T].is_empty = (self) bool: { self.len == 0 }

bind Set[T].find_entry = (self, needle: T, h: u64) int: {
    if (self.buck_cap == 0 or unsafe is_null(self.buckets)): func <- -1
    let mask = self.buck_cap - 1
    let slot = int(h & u64(mask))
    for (true): {
        let idx = unsafe self.buckets[slot]
        if (idx == -1): func <- -1
        if (idx >= 0): {
            unsafe {
                if (self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == needle): {
                    func <- idx
                }
            }
        }
        slot = (slot + 1) & mask
    }
    -1
}

bind Set[T].has = (self, val: T) bool: {
    let h = val.hash()
    self.find_entry(val, h) >= 0
}

bind Set[T].contains = (self, val: T) bool: {
    self.has(val)
}

bind Set[T].rehash = (&self, new_bcap: int): {
    if (self.len < self.ent_len): {
        let w = 0
        let r = 0
        for (r < self.ent_len): {
            unsafe {
                if (self.entries[r].alive): {
                    if (w != r): {
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
    for (bi < new_bcap): {
        unsafe { nb[bi] = -1 }
        bi += 1
    }
    let mask = new_bcap - 1
    let i = 0
    for (i < self.ent_len): {
        unsafe {
            if (self.entries[i].alive): {
                let slot = int(self.entries[i].hash & u64(mask))
                for (nb[slot] >= 0): {
                    slot = (slot + 1) & mask
                }
                nb[slot] = i
            }
        }
        i += 1
    }
    unsafe {
        if (not is_null(self.buckets)): {
            free_raw(self.buckets)
        }
    }
    self.buckets = nb
    self.buck_cap = new_bcap
}

bind Set[T].insert = (&self, val: own T) bool: {
    let h = val.hash()
    let existing = self.find_entry(val, h)
    if (existing >= 0): func <- false

    if (self.buck_cap == 0 or (self.ent_len + 1) * 10 >= self.buck_cap * 7): {
        let target_bcap = if (self.buck_cap > 0): self.buck_cap * 2 else: 16
        self.rehash(target_bcap)
    }
    if (self.ent_len + 1 > self.ent_cap): {
        let ncap = if (self.ent_cap > 0): self.ent_cap * 2 else: 8
        let ne = unsafe {
            if (self.ent_cap == 0): alloc_raw[SetEntry[T]](ncap)
            else: realloc_raw(self.entries, ncap)
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
    for (unsafe self.buckets[slot] >= 0): {
        slot = (slot + 1) & mask
    }
    unsafe {
        self.buckets[slot] = idx
    }
    self.len += 1
    true
}

bind Set[T].push = (&self, item: own T): {
    let _ = self.insert(own item)
}

bind Set[T].remove = (&self, val: T) bool: {
    if (self.buck_cap == 0 or unsafe is_null(self.buckets)): func <- false
    let h = val.hash()
    let mask = self.buck_cap - 1
    let slot = int(h & u64(mask))
    for (true): {
        let idx = unsafe self.buckets[slot]
        if (idx == -1): func <- false
        if (idx >= 0): {
            unsafe {
                if (self.entries[idx].alive and self.entries[idx].hash == h and self.entries[idx].key == val): {
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

bind Set[T].clear = (&self): {
    if (not unsafe is_null(self.entries)): {
        let i = 0
        for (i < self.ent_len): {
            unsafe {
                if (self.entries[i].alive): {
                    let _ = ptr_read(ptr_add(self.entries, i))
                    self.entries[i].alive = false
                }
            }
            i += 1
        }
    }
    if (self.buck_cap > 0 and unsafe not is_null(self.buckets)): {
        let i = 0
        for (i < self.buck_cap): {
            unsafe { self.buckets[i] = -1 }
            i += 1
        }
    }
    self.ent_len = 0
    self.len = 0
}

bind Set[T].to_list = (self) List[T]: {
    let out = List[T].with_capacity(self.len)
    let i = 0
    for (i < self.ent_len): {
        unsafe {
            if (self.entries[i].alive): {
                out.push(self.entries[i].key.copy())
            }
        }
        i += 1
    }
    out
}

bind Set[T].copy = (self) Set[T]: {
    let out = Set[T].with_capacity(self.len)
    let i = 0
    for (i < self.ent_len): {
        unsafe {
            if (self.entries[i].alive): {
                out.insert(self.entries[i].key.copy())
            }
        }
        i += 1
    }
    out
}

bind Set[T].to_string = (self) string: {
    let out = "#["
    let first = true
    let i = 0
    for (i < self.ent_len): {
        unsafe {
            if (self.entries[i].alive): {
                if (not first): out.push(", ")
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

bind SetIterator[T].next = (&self) ?T: {
    for (self.idx < self.ent_len): {
        let i = self.idx
        self.idx += 1
        unsafe {
            if (self.entries[i].alive): {
                func <- self.entries[i].key.copy()
            }
        }
    }
    none
}

bind Set[T].iter = (self) Iterator[T]: {
    SetIterator[T][
        entries: self.entries,
        ent_len: self.ent_len,
        idx: 0,
    ]
}

bind Set[T].drop = (&self): {
    if (not unsafe is_null(self.entries)): {
        let i = 0
        for (i < self.ent_len): {
            unsafe {
                if (self.entries[i].alive): {
                    let _ = ptr_read(ptr_add(self.entries, i))
                }
            }
            i += 1
        }
        unsafe free_raw(self.entries)
    }
    if (not unsafe is_null(self.buckets)): {
        unsafe free_raw(self.buckets)
    }
    self.entries = unsafe null_ptr[SetEntry[T]]()
    self.buckets = unsafe null_ptr[int]()
    self.buck_cap = 0
    self.ent_cap = 0
    self.ent_len = 0
    self.len = 0
}
