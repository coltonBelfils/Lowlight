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

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("sort list in-place and verify is_sorted, reverse")
bind test_sort_list_comparable = () {
    let empty_list = List[int].new()
    empty_list.sort()
    assert(empty_list.is_sorted(), "empty list is sorted")

    let single = List[int].new()
    single.push(42)
    single.sort()
    assert(single.is_sorted(), "single element is sorted")

    let nums = List[int].new()
    nums.push(50)
    nums.push(20)
    nums.push(40)
    nums.push(10)
    nums.push(30)
    assert(not nums.is_sorted(), "nums initially not sorted")

    nums.sort()
    assert(nums.is_sorted(), "nums sorted")
    assert(nums.len() == 5, "nums len 5")
    assert(nums.get(0) == 10, "nums[0] == 10")
    assert(nums.get(1) == 20, "nums[1] == 20")
    assert(nums.get(2) == 30, "nums[2] == 30")
    assert(nums.get(3) == 40, "nums[3] == 40")
    assert(nums.get(4) == 50, "nums[4] == 50")

    nums.reverse()
    assert(nums.get(0) == 50, "reversed nums[0] == 50")
    assert(nums.get(4) == 10, "reversed nums[4] == 10")

    // Test larger list to exercise quicksort partitioning
    let large = List[int].new()
    let i = 25
    while (i > 0) {
        large.push(i)
        i -= 1
    }
    assert(not large.is_sorted(), "large initially reversed")
    large.sort()
    assert(large.is_sorted(), "large sorted")
    assert(large.get(0) == 1, "large[0] == 1")
    assert(large.get(24) == 25, "large[24] == 25")
}

@test("sort list with custom comparator (descending)")
bind test_sort_custom_comparator = () {
    let nums = List[int].new()
    nums.push(15)
    nums.push(3)
    nums.push(99)
    nums.push(42)

    let cmp_desc = (a: int, b: int): int {
        if (a > b) -1
        else if (a < b) 1
        else 0
    }

    nums.sort_by(cmp_desc)
    assert(nums.get(0) == 99, "desc[0] == 99")
    assert(nums.get(1) == 42, "desc[1] == 42")
    assert(nums.get(2) == 15, "desc[2] == 15")
    assert(nums.get(3) == 3, "desc[3] == 3")
}

@test("binary search on sorted list")
bind test_sort_binary_search = () {
    let list = List[int].new()
    list.push(10)
    list.push(20)
    list.push(30)
    list.push(40)
    list.push(50)

    let idx1? = list.binary_search(30)
    assert(idx1? == 2, "binary_search 30 is index 2")

    let idx2? = list.binary_search(10)
    assert(idx2? == 0, "binary_search 10 is index 0")

    let idx3? = list.binary_search(50)
    assert(idx3? == 4, "binary_search 50 is index 4")

    let idx4? = list.binary_search(25)
    assert(idx4? == none, "binary_search 25 is none")

    let idx5? = list.binary_search(0)
    assert(idx5? == none, "binary_search 0 is none")

    let idx6? = list.binary_search(100)
    assert(idx6? == none, "binary_search 100 is none")
}

@test("collection sorting extensions: Set.to_sorted_list and Map.to_sorted_keys")
bind test_sort_collections = () {
    let s = Set[int].new()
    s.insert(50)
    s.insert(10)
    s.insert(40)
    s.insert(20)

    let sorted_s = s.to_sorted_list()
    assert(sorted_s.is_sorted(), "sorted_s is sorted")
    assert(sorted_s.len() == 4, "sorted_s len is 4")
    assert(sorted_s.get(0) == 10, "sorted_s[0] == 10")
    assert(sorted_s.get(3) == 50, "sorted_s[3] == 50")

    let m = Map[string, int].new()
    m.insert("zebra", 1)
    m.insert("apple", 2)
    m.insert("mango", 3)

    let sorted_k = m.to_sorted_keys()
    assert(sorted_k.is_sorted(), "sorted_k is sorted")
    assert(sorted_k.len() == 3, "sorted_k len is 3")
    assert(sorted_k.get(0) == "apple", "sorted_k[0] == apple")
    assert(sorted_k.get(1) == "mango", "sorted_k[1] == mango")
    assert(sorted_k.get(2) == "zebra", "sorted_k[2] == zebra")
}

