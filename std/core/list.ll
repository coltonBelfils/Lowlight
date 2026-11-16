// =============================================================================
// Lowlight Core List[T] Dynamic Heap-Allocated Contiguous Array
// =============================================================================

import "./interfaces.ll"

@lang("list")
define List[T] struct [
    data: ptr[T],
    len: int,
    cap: int,
]

bind List[T].new = () List[T]: {
    List[T][
        data: unsafe null_ptr[T](),
        len: 0,
        cap: 0,
    ]
}

bind List[T].with_capacity = (cap: int) List[T]: {
    let p = if (cap > 0): unsafe alloc_raw[T](cap) else: unsafe null_ptr[T]()
    List[T][
        data: p,
        len: 0,
        cap: cap,
    ]
}

bind List[T].len = (self) int: { self.len }
bind List[T].cap = (self) int: { self.cap }
bind List[T].is_empty = (self) bool: { self.len == 0 }

bind List[T].push = (&self, item: own T): {
    if (self.len + 1 > self.cap): {
        let ncap = if (self.cap > 0): self.cap * 2 else: 4
        let nd = unsafe {
            if (self.cap == 0): alloc_raw[T](ncap)
            else: realloc_raw(self.data, ncap)
        }
        self.data = nd
        self.cap = ncap
    }
    unsafe ptr_write(ptr_add(self.data, self.len), own item)
    self.len += 1
}

bind List[T].pop = (&self) ?T: {
    if (self.len <= 0): func <- none
    self.len -= 1
    unsafe {
        let val = ptr_read(ptr_add(self.data, self.len))
        val
    }
}

bind List[T].get = (self, idx: int) ?T: {
    if (idx < 0 or idx >= self.len): func <- none
    unsafe {
        self.data[idx].copy()
    }
}

bind List[T].contains = (self, item: T) bool: {
    let i = 0
    for (i < self.len): {
        unsafe {
            if (self.data[i] == item): func <- true
        }
        i += 1
    }
    false
}

bind List[T].clear = (&self): {
    if (self.cap > 0): {
        let i = self.len - 1
        for (i >= 0): {
            unsafe {
                let _ = ptr_read(ptr_add(self.data, i))
            }
            i -= 1
        }
    }
    self.len = 0
}

bind List[T].copy = (self) List[T]: {
    let cp = List[T].with_capacity(self.len)
    let i = 0
    for (i < self.len): {
        unsafe {
            cp.push(self.data[i].copy())
        }
        i += 1
    }
    cp
}

bind List[T].to_string = (self) string: {
    let out = "["
    let i = 0
    for (i < self.len): {
        if (i > 0): out.push(", ")
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

bind ListIterator[T].next = (&self) ?T: {
    if (self.idx >= self.len): func <- none
    let i = self.idx
    self.idx += 1
    unsafe {
        self.data[i].copy()
    }
}

bind List[T].iter = (self) Iterator[T]: {
    ListIterator[T][
        data: self.data,
        len: self.len,
        idx: 0,
    ]
}

bind List[T].drop = (&self): {
    if (self.cap > 0): {
        let i = self.len - 1
        for (i >= 0): {
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
