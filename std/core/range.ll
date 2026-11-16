// =============================================================================
// Lowlight Core Range[T] Sequence & Containment Interval
// =============================================================================

import "./interfaces.ll"

@lang("range")
define Range[T] struct [
    start: T,
    end: T,
    inclusive: bool,
]

bind Range[T].contains = (self, item: T) bool: {
    if (self.inclusive): {
        func <- item >= self.start and item <= self.end
    }
    item >= self.start and item < self.end
}

bind Range[T].is_empty = (self) bool: {
    if (self.inclusive): {
        func <- self.start > self.end
    }
    self.start >= self.end
}

bind Range[T].len = (self) int: {
    if (self.inclusive): {
        if (self.start > self.end): func <- 0
        func <- int(u64(self.end) - u64(self.start) + u64(1))
    }
    if (self.start >= self.end): func <- 0
    int(u64(self.end) - u64(self.start))
}

bind Range[T].to_string = (self) string: {
    if (self.inclusive): {
        func <- "${self.start}..=${self.end}"
    }
    "${self.start}..${self.end}"
}

define RangeIterator[T] struct [
    cur: T,
    end: T,
    inclusive: bool,
    done: bool,
]

bind RangeIterator[T].next = (&self) ?T: {
    if (self.done): func <- none
    if (not self.inclusive and self.cur >= self.end): {
        self.done = true
        func <- none
    }
    if (self.inclusive and self.cur > self.end): {
        self.done = true
        func <- none
    }
    let val = self.cur
    if (self.cur == self.end): {
        self.done = true
    } else: {
        self.cur = T(u64(self.cur) + u64(1))
    }
    val
}

bind Range[T].iter = (self) Iterator[T]: {
    let done = if (self.inclusive): self.start > self.end else: self.start >= self.end
    RangeIterator[T][
        cur: self.start,
        end: self.end,
        inclusive: self.inclusive,
        done: done,
    ]
}
