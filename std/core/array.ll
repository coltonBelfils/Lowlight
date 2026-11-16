// =============================================================================
// Lowlight Core Array Fixed-Size Stack-Allocated Contiguous Sequence
// =============================================================================

import "./interfaces.ll"

@lang("array")
define Array[T] struct [
    data: ptr[T],
    len: int,
]

bind Array[T].len = (self) int: { self.len }
bind Array[T].is_empty = (self) bool: { self.len == 0 }

bind Array[T].get = (self, idx: int) ?T: {
    if (idx < 0 or idx >= self.len): func <- none
    unsafe {
        self.data[idx].copy()
    }
}

bind Array[T].contains = (self, item: T) bool: {
    let i = 0
    for (i < self.len): {
        unsafe {
            if (self.data[i] == item): func <- true
        }
        i += 1
    }
    false
}

bind Array[T].to_string = (self) string: {
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

define ArrayIterator[T] struct [
    data: ptr[T],
    len: int,
    idx: int,
]

bind ArrayIterator[T].next = (&self) ?T: {
    if (self.idx >= self.len): func <- none
    let i = self.idx
    self.idx += 1
    unsafe {
        self.data[i].copy()
    }
}

bind Array[T].iter = (self) Iterator[T]: {
    ArrayIterator[T][
        data: self.data,
        len: self.len,
        idx: 0,
    ]
}
