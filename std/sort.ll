// Lowlight Standard Library: std/sort.ll
// In-place sorting, custom comparator sorting, binary search, and Sortable interface.

// -----------------------------------------------------------------------------
// Sortable Interface
// -----------------------------------------------------------------------------

// Interface implemented by collections that can be sorted in place.
define Sortable interface [
    sort: (&self),
]

// -----------------------------------------------------------------------------
// Internal Swap & Quicksort Helper Algorithms
// -----------------------------------------------------------------------------

// In-place bitwise slot swap without invoking copy constructors or destructors.
bind swap_elements[T] = (list: &List[T], i: int, j: int) {
    if (i == j) func <-
    unsafe {
        let pi = ptr_add(list.data, i)
        let pj = ptr_add(list.data, j)
        let tmp = ptr_read(pi)
        ptr_write(pi, ptr_read(pj))
        ptr_write(pj, own tmp)
    }
}

// In-place insertion sort for small partitions (<= 16 elements).
bind sort_insertion[T] = (list: &List[T], lo: int, hi: int) {
    let i = lo + 1
    while (i <= hi) {
        let j = i
        while (j > lo) {
            let less = unsafe { list.data[j] < list.data[j - 1] }
            if (not less) {
                while <-
            }
            swap_elements(&list, j, j - 1)
            j -= 1
        }
        i += 1
    }
}

// 3-way median-of-three Lomuto-style partition.
bind partition[T] = (list: &List[T], lo: int, hi: int): int {
    let mid = lo + (hi - lo) / 2
    unsafe {
        if (list.data[mid] < list.data[lo]) swap_elements(&list, lo, mid)
        if (list.data[hi] < list.data[lo]) swap_elements(&list, lo, hi)
        if (list.data[mid] < list.data[hi]) swap_elements(&list, mid, hi)
    }

    let pivot_idx = hi
    let i = lo
    let j = hi - 1
    while (true) {
        while (unsafe { list.data[i] < list.data[pivot_idx] }) {
            i += 1
        }
        while (j > lo and unsafe { list.data[pivot_idx] < list.data[j] }) {
            j -= 1
        }
        if (i >= j) {
            while <-
        }
        swap_elements(&list, i, j)
        i += 1
        j -= 1
    }
    swap_elements(&list, i, hi)
    i
}

// Recursive Quicksort with insertion sort fallback.
bind sort_range[T] = (list: &List[T], lo: int, hi: int) {
    if (hi - lo <= 16) {
        sort_insertion(&list, lo, hi)
        func <-
    }
    let p = partition(&list, lo, hi)
    if (p > lo) {
        sort_range(&list, lo, p - 1)
    }
    if (p < hi) {
        sort_range(&list, p + 1, hi)
    }
}

// -----------------------------------------------------------------------------
// Comparator-based Quicksort Helpers
// -----------------------------------------------------------------------------

bind sort_by_insertion[T] = (list: &List[T], lo: int, hi: int, cmp_fn: (T, T): int) {
    let i = lo + 1
    while (i <= hi) {
        let j = i
        while (j > lo) {
            let cmp = unsafe { cmp_fn(list.data[j].copy(), list.data[j - 1].copy()) }
            if (cmp >= 0) {
                while <-
            }
            swap_elements(&list, j, j - 1)
            j -= 1
        }
        i += 1
    }
}

bind partition_by[T] = (list: &List[T], lo: int, hi: int, cmp_fn: (T, T): int): int {
    let mid = lo + (hi - lo) / 2
    unsafe {
        if (cmp_fn(list.data[mid].copy(), list.data[lo].copy()) < 0) swap_elements(&list, lo, mid)
        if (cmp_fn(list.data[hi].copy(), list.data[lo].copy()) < 0) swap_elements(&list, lo, hi)
        if (cmp_fn(list.data[mid].copy(), list.data[hi].copy()) < 0) swap_elements(&list, mid, hi)
    }

    let pivot_idx = hi
    let i = lo
    let j = hi - 1
    while (true) {
        while (unsafe { cmp_fn(list.data[i].copy(), list.data[pivot_idx].copy()) < 0 }) {
            i += 1
        }
        while (j > lo and unsafe { cmp_fn(list.data[pivot_idx].copy(), list.data[j].copy()) < 0 }) {
            j -= 1
        }
        if (i >= j) {
            while <-
        }
        swap_elements(&list, i, j)
        i += 1
        j -= 1
    }
    swap_elements(&list, i, hi)
    i
}

bind sort_by_range[T] = (list: &List[T], lo: int, hi: int, cmp_fn: (T, T): int) {
    if (hi - lo <= 16) {
        sort_by_insertion(&list, lo, hi, cmp_fn)
        func <-
    }
    let p = partition_by(&list, lo, hi, cmp_fn)
    if (p > lo) {
        sort_by_range(&list, lo, p - 1, cmp_fn)
    }
    if (p < hi) {
        sort_by_range(&list, p + 1, hi, cmp_fn)
    }
}

// -----------------------------------------------------------------------------
// Methods Extending Collections
// -----------------------------------------------------------------------------

// Sorts the list in place in ascending order using Quicksort with insertion sort optimization.
bind List[T].sort = (&self) {
    if (self.len <= 1) func <-
    sort_range(&self, 0, self.len - 1)
}

// Sorts the list in place using a custom comparator function `cmp_fn(a, b): int`.
// The comparator should return < 0 for a < b, 0 for a == b, and > 0 for a > b.
bind List[T].sort_by = (&self, cmp_fn: (T, T): int) {
    if (self.len <= 1) func <-
    sort_by_range(&self, 0, self.len - 1, cmp_fn)
}

// Returns true if the list is sorted in ascending order.
bind List[T].is_sorted = (self): bool {
    if (self.len <= 1) func <- true
    let i = 0
    while (i < self.len - 1) {
        let disordered = unsafe { self.data[i + 1] < self.data[i] }
        if (disordered) {
            func <- false
        }
        i += 1
    }
    true
}

// Performs a binary search on a sorted list for `target`.
// Returns the index of `target` if found, or `none` if absent.
bind List[T].binary_search = (self, target: T): ?int {
    if (self.len == 0) func <- none
    let lo = 0
    let hi = self.len - 1
    while (lo <= hi) {
        let mid = lo + (hi - lo) / 2
        let mid_val = unsafe { self.data[mid].copy() }
        if (mid_val == target) {
            func <- mid
        } else if (mid_val < target) {
            lo = mid + 1
        } else {
            hi = mid - 1
        }
    }
    none
}

// Reverses the elements of the list in place.
bind List[T].reverse = (&self) {
    if (self.len <= 1) func <-
    let i = 0
    let j = self.len - 1
    while (i < j) {
        swap_elements(&self, i, j)
        i += 1
        j -= 1
    }
}

// Converts a Set[T] to a sorted List[T].
bind Set[T].to_sorted_list = (self): List[T] {
    let out = self.to_list()
    out.sort()
    out
}

// Returns a sorted List of keys from a Map[K, V].
bind Map[K, V].to_sorted_keys = (self): List[K] {
    let out = self.keys()
    out.sort()
    out
}

// -----------------------------------------------------------------------------
// Standalone Functions
// -----------------------------------------------------------------------------

bind sort[T] = (list: &List[T]) {
    list.sort()
}

bind sort_by[T] = (list: &List[T], cmp_fn: (T, T): int) {
    list.sort_by(cmp_fn)
}

bind is_sorted[T] = (list: List[T]): bool {
    list.is_sorted()
}

bind binary_search[T] = (list: List[T], target: T): ?int {
    list.binary_search(target)
}
