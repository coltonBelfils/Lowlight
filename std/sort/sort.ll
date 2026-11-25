// std/sort — Depth-bounded Introsort, bottom-up Merge Sort (stable), custom comparator
// sorting, binary search, lower/upper bounds, and collection ordering helpers.
//
// Extends [`List`] (`[T]`), [`Array`] (`[N: T]`), [`Set`], and [`Map`] with sorting and
// searching operations plus the [`Sortable`] contract.

// -----------------------------------------------------------------------------
// Sortable Interface
// -----------------------------------------------------------------------------

// Interface implemented by collections that support in-place sorting.
define Sortable interface [
	// Sorts the elements of `self` in ascending order in place.
	sort: fn(&self),
]

// -----------------------------------------------------------------------------
// Internal Swap, Depth, and Bitwise Move Helpers
// -----------------------------------------------------------------------------

// In-place bitwise slot swap without invoking copy constructors or destructors.
bind _swap_ptr[T] = fn(data: ptr[T], i: int, j: int): {
	if(i == j): func <-
	unsafe {
		let pi = ptr_add(data, i)
		let pj = ptr_add(data, j)
		let tmp = ptr_read(pi)
		ptr_write(pi, ptr_read(pj))
		ptr_write(pj, own tmp)
	}
}

// Reverses elements in `[0..len)` bitwise without requiring `Copyable`.
bind _reverse_ptr[T] = fn(data: ptr[T], len: int): {
	if(len <= 1): func <-
	let i = 0
	let j = len - 1
	while(i < j): {
		_swap_ptr(data, i, j)
		i += 1
		j -= 1
	}
}

// Computes `2 * floor(log2(n))` for Introsort's Heapsort fallback depth limit.
bind _max_depth = fn(n: int) int: {
	let depth = 0
	let rem = n
	while(rem > 1): {
		rem /= 2
		depth += 1
	}
	depth * 2
}

// -----------------------------------------------------------------------------
// Introsort (Comparable)
// -----------------------------------------------------------------------------

// In-place insertion sort for small partitions (`hi - lo <= 16`). Stable on equal keys.
bind _sort_insertion[T] = fn(data: ptr[T], lo: int, hi: int): {
	let i = lo + 1
	while(i <= hi): {
		let j = i
		while(j > lo): {
			let less = unsafe {
				data[j] < data[j - 1]
			}
			if(not less): while <-
			_swap_ptr(data, j, j - 1)
			j -= 1
		}
		i += 1
	}
}

// Restores max-heap invariant at `root_in` over `data[lo .. lo + count)`.
bind _sift_down[T] = fn(data: ptr[T], lo: int, root_in: int, count: int): {
	let root = root_in
	while(true): {
		let left = 2 * root + 1
		if(left >= count): while <-
		let best = left
		let right = left + 1
		if(right < count): {
			let right_greater = unsafe {
				data[lo + left] < data[lo + right]
			}
			if(right_greater): best = right
		}
		let need_swap = unsafe {
			data[lo + root] < data[lo + best]
		}
		if(not need_swap): while <-
		_swap_ptr(data, lo + root, lo + best)
		root = best
	}
}

// Worst-case O(n log n) in-place Heapsort fallback for `data[lo..=hi]`.
bind _heapsort_range[T] = fn(data: ptr[T], lo: int, hi: int): {
	let count = hi - lo + 1
	if(count <= 1): func <-
	let start = count / 2 - 1
	while(start >= 0): {
		_sift_down(data, lo, start, count)
		start -= 1
	}
	let end = count - 1
	while(end > 0): {
		_swap_ptr(data, lo, lo + end)
		_sift_down(data, lo, 0, end)
		end -= 1
	}
}

// Median-of-three Hoare/Lomuto partition on `data[lo..=hi]`.
bind _partition[T] = fn(data: ptr[T], lo: int, hi: int) int: {
	let mid = lo + (hi - lo) / 2
	unsafe {
		if(data[mid] < data[lo]): _swap_ptr(data, lo, mid)
		if(data[hi] < data[lo]): _swap_ptr(data, lo, hi)
		if(data[mid] < data[hi]): _swap_ptr(data, mid, hi)
	}

	let pivot_idx = hi
	let i = lo
	let j = hi - 1
	while(true): {
		while(i < hi and unsafe {
			data[i] < data[pivot_idx]
		}): i += 1
		while(j > lo and unsafe {
			data[pivot_idx] < data[j]
		}): j -= 1
		if(i >= j): while <-
		_swap_ptr(data, i, j)
		i += 1
		j -= 1
	}
	_swap_ptr(data, i, hi)
	i
}

// Depth-bounded Introsort with tail recursion elimination onto the larger subrange.
bind _sort_range[T] = fn(data: ptr[T], lo_in: int, hi_in: int, depth_in: int) void: {
	let lo = lo_in
	let hi = hi_in
	let depth_limit = depth_in
	while(hi - lo > 16): {
		if(depth_limit <= 0): {
			_heapsort_range(data, lo, hi)
			func <-
		}
		depth_limit -= 1
		let p = _partition(data, lo, hi)
		if(p - lo < hi - p): {
			if(p - 1 > lo): _sort_range(data, lo, p - 1, depth_limit)
			lo = p + 1
		} else: {
			if(p + 1 < hi): _sort_range(data, p + 1, hi, depth_limit)
			hi = p - 1
		}
	}
	if(hi > lo): _sort_insertion(data, lo, hi)
}

// -----------------------------------------------------------------------------
// Introsort (Custom Comparator)
// -----------------------------------------------------------------------------

bind _sort_by_insertion[T] = fn(data: ptr[T], lo: int, hi: int, cmp_fn: cl(T, T) int): {
	let i = lo + 1
	while(i <= hi): {
		let j = i
		while(j > lo): {
			let cmp = unsafe cmp_fn(data[j], data[j - 1])
			if(cmp >= 0): while <-
			_swap_ptr(data, j, j - 1)
			j -= 1
		}
		i += 1
	}
}

bind _sift_down_by[T] = fn(data: ptr[T], lo: int, root_in: int, count: int, cmp_fn: cl(T, T) int): {
	let root = root_in
	while(true): {
		let left = 2 * root + 1
		if(left >= count): while <-
		let best = left
		let right = left + 1
		if(right < count): {
			let right_greater = unsafe cmp_fn(data[lo + left], data[lo + right]) < 0
			if(right_greater): best = right
		}
		let need_swap = unsafe cmp_fn(data[lo + root], data[lo + best]) < 0
		if(not need_swap): while <-
		_swap_ptr(data, lo + root, lo + best)
		root = best
	}
}

bind _heapsort_by_range[T] = fn(data: ptr[T], lo: int, hi: int, cmp_fn: cl(T, T) int): {
	let count = hi - lo + 1
	if(count <= 1): func <-
	let start = count / 2 - 1
	while(start >= 0): {
		_sift_down_by(data, lo, start, count, cmp_fn)
		start -= 1
	}
	let end = count - 1
	while(end > 0): {
		_swap_ptr(data, lo, lo + end)
		_sift_down_by(data, lo, 0, end, cmp_fn)
		end -= 1
	}
}

bind _partition_by[T] = fn(data: ptr[T], lo: int, hi: int, cmp_fn: cl(T, T) int) int: {
	let mid = lo + (hi - lo) / 2
	unsafe {
		if(cmp_fn(data[mid], data[lo]) < 0): _swap_ptr(data, lo, mid)
		if(cmp_fn(data[hi], data[lo]) < 0): _swap_ptr(data, lo, hi)
		if(cmp_fn(data[mid], data[hi]) < 0): _swap_ptr(data, mid, hi)
	}

	let pivot_idx = hi
	let i = lo
	let j = hi - 1
	while(true): {
		while(i < hi and unsafe {
			cmp_fn(data[i], data[pivot_idx]) < 0
		}): i += 1
		while(j > lo and unsafe {
			cmp_fn(data[pivot_idx], data[j]) < 0
		}): j -= 1
		if(i >= j): while <-
		_swap_ptr(data, i, j)
		i += 1
		j -= 1
	}
	_swap_ptr(data, i, hi)
	i
}

bind _sort_by_range[T] = fn(data: ptr[T], lo_in: int, hi_in: int, depth_in: int, cmp_fn: cl(T, T) int) void: {
	let lo = lo_in
	let hi = hi_in
	let depth_limit = depth_in
	while(hi - lo > 16): {
		if(depth_limit <= 0): {
			_heapsort_by_range(data, lo, hi, cmp_fn)
			func <-
		}
		depth_limit -= 1
		let p = _partition_by(data, lo, hi, cmp_fn)
		if(p - lo < hi - p): {
			if(p - 1 > lo): _sort_by_range(data, lo, p - 1, depth_limit, cmp_fn)
			lo = p + 1
		} else: {
			if(p + 1 < hi): _sort_by_range(data, p + 1, hi, depth_limit, cmp_fn)
			hi = p - 1
		}
	}
	if(hi > lo): _sort_by_insertion(data, lo, hi, cmp_fn)
}

// -----------------------------------------------------------------------------
// Stable Bottom-Up Merge Sort (Comparable & Custom Comparator)
// -----------------------------------------------------------------------------

bind _stable_sort_ptr[T] = fn(data: ptr[T], len: int): {
	if(len <= 1): func <-
	if(len <= 16): {
		_sort_insertion(data, 0, len - 1)
		func <-
	}

	// Sort initial 16-element runs using stable insertion sort.
	let run = 0
	while(run < len): {
		let r_end = run + 15
		if(r_end >= len): r_end = len - 1
		if(r_end > run): _sort_insertion(data, run, r_end)
		run += 16
	}

	let buf = unsafe alloc_uninit[T](len)
	let width = 16
	while(width < len): {
		let left = 0
		while(left + width < len): {
			let mid = left + width
			let right = mid + width
			if(right > len): right = len

			let need_merge = unsafe {
				data[mid] < data[mid - 1]
			}
			if(need_merge): {
				let li = left
				let ri = mid
				let k = 0
				while(li < mid and ri < right): {
					let take_right = unsafe {
						data[ri] < data[li]
					}
					if(take_right): {
						unsafe ptr_write(ptr_add(buf, k), ptr_read(ptr_add(data, ri)))
						ri += 1
					} else: {
						unsafe ptr_write(ptr_add(buf, k), ptr_read(ptr_add(data, li)))
						li += 1
					}
					k += 1
				}
				while(li < mid): {
					unsafe ptr_write(ptr_add(buf, k), ptr_read(ptr_add(data, li)))
					li += 1
					k += 1
				}
				while(ri < right): {
					unsafe ptr_write(ptr_add(buf, k), ptr_read(ptr_add(data, ri)))
					ri += 1
					k += 1
				}
				unsafe copy_bytes(
					ptr_cast[u8](ptr_add(data, left)),
					ptr_cast[u8](buf),
					(right - left) * type.size_of[T](),
				)
			}
			left += width * 2
		}
		width *= 2
	}
	unsafe free_raw(buf)
}

bind _stable_sort_by_ptr[T] = fn(data: ptr[T], len: int, cmp_fn: cl(T, T) int): {
	if(len <= 1): func <-
	if(len <= 16): {
		_sort_by_insertion(data, 0, len - 1, cmp_fn)
		func <-
	}

	let run = 0
	while(run < len): {
		let r_end = run + 15
		if(r_end >= len): r_end = len - 1
		if(r_end > run): _sort_by_insertion(data, run, r_end, cmp_fn)
		run += 16
	}

	let buf = unsafe alloc_uninit[T](len)
	let width = 16
	while(width < len): {
		let left = 0
		while(left + width < len): {
			let mid = left + width
			let right = mid + width
			if(right > len): right = len

			let need_merge = unsafe cmp_fn(data[mid], data[mid - 1]) < 0
			if(need_merge): {
				let li = left
				let ri = mid
				let k = 0
				while(li < mid and ri < right): {
					let take_right = unsafe cmp_fn(data[ri], data[li]) < 0
					if(take_right): {
						unsafe ptr_write(ptr_add(buf, k), ptr_read(ptr_add(data, ri)))
						ri += 1
					} else: {
						unsafe ptr_write(ptr_add(buf, k), ptr_read(ptr_add(data, li)))
						li += 1
					}
					k += 1
				}
				while(li < mid): {
					unsafe ptr_write(ptr_add(buf, k), ptr_read(ptr_add(data, li)))
					li += 1
					k += 1
				}
				while(ri < right): {
					unsafe ptr_write(ptr_add(buf, k), ptr_read(ptr_add(data, ri)))
					ri += 1
					k += 1
				}
				unsafe copy_bytes(
					ptr_cast[u8](ptr_add(data, left)),
					ptr_cast[u8](buf),
					(right - left) * type.size_of[T](),
				)
			}
			left += width * 2
		}
		width *= 2
	}
	unsafe free_raw(buf)
}

// -----------------------------------------------------------------------------
// Search & Order Verification Helpers
// -----------------------------------------------------------------------------

bind _is_sorted_ptr[T] = fn(data: ptr[T], len: int) bool: {
	if(len <= 1): func <- true
	let i = 0
	while(i < len - 1): {
		let disordered = unsafe {
			data[i + 1] < data[i]
		}
		if(disordered): func <- false
		i += 1
	}
	true
}

bind _is_sorted_by_ptr[T] = fn(data: ptr[T], len: int, cmp_fn: cl(T, T) int) bool: {
	if(len <= 1): func <- true
	let i = 0
	while(i < len - 1): {
		let disordered = unsafe {
			cmp_fn(data[i + 1], data[i]) < 0
		}
		if(disordered): func <- false
		i += 1
	}
	true
}

bind _binary_search_ptr[T] = fn(data: ptr[T], len: int, target: T) ?int: {
	if(len <= 0): func <- none
	let lo = 0
	let hi = len - 1
	while(lo <= hi): {
		let mid = lo + (hi - lo) / 2
		let less = unsafe {
			data[mid] < target
		}
		if(less): lo = mid + 1
		else: {
			let greater = unsafe {
				target < data[mid]
			}
			if(greater): hi = mid - 1
			else: func <- mid
		}
	}
	none
}

bind _binary_search_by_ptr[T] = fn(data: ptr[T], len: int, cmp_fn: cl(T) int) ?int: {
	if(len <= 0): func <- none
	let lo = 0
	let hi = len - 1
	while(lo <= hi): {
		let mid = lo + (hi - lo) / 2
		let c = unsafe cmp_fn(data[mid])
		if(c == 0): func <- mid
		else if(c < 0): lo = mid + 1
		else: hi = mid - 1
	}
	none
}

bind _lower_bound_ptr[T] = fn(data: ptr[T], len: int, target: T) int: {
	let lo = 0
	let hi = len
	while(lo < hi): {
		let mid = lo + (hi - lo) / 2
		let less = unsafe {
			data[mid] < target
		}
		if(less): lo = mid + 1
		else: hi = mid
	}
	lo
}

bind _upper_bound_ptr[T] = fn(data: ptr[T], len: int, target: T) int: {
	let lo = 0
	let hi = len
	while(lo < hi): {
		let mid = lo + (hi - lo) / 2
		let gt = unsafe {
			target < data[mid]
		}
		if(gt): hi = mid
		else: lo = mid + 1
	}
	lo
}

// -----------------------------------------------------------------------------
// Methods Extending List[T] (`[T]`)
// -----------------------------------------------------------------------------

bind _list_data_mut[T] = fn(xs: &List[T]) ptr[T]: {
	if(xs.cap <= 0 and xs.len > 0): xs.reserve(xs.len)
	xs.data
}

// Sorts the list in place in ascending order using depth-bounded Introsort.
bind List[T].sort = fn(&self): {
	if(self.len <= 1): func <-
	_sort_range(_list_data_mut(&self), 0, self.len - 1, _max_depth(self.len))
}

// Sorts the list in place using custom comparator `cmp_fn` via depth-bounded Introsort.
//
// - `cmp_fn`: Returns `< 0` when `a < b`, `0` when equal, or `> 0` when `a > b`.
bind List[T].sort_by = fn(&self, cmp_fn: cl(T, T) int): {
	if(self.len <= 1): func <-
	_sort_by_range(_list_data_mut(&self), 0, self.len - 1, _max_depth(self.len), cmp_fn)
}

// Sorts the list in place in ascending order while preserving the relative order of equal keys.
bind List[T].stable_sort = fn(&self): {
	if(self.len <= 1): func <-
	_stable_sort_ptr(_list_data_mut(&self), self.len)
}

// Sorts the list in place using `cmp_fn` while preserving the relative order of equal keys.
//
// - `cmp_fn`: Returns `< 0` when `a < b`, `0` when equal, or `> 0` when `a > b`.
bind List[T].stable_sort_by = fn(&self, cmp_fn: cl(T, T) int): {
	if(self.len <= 1): func <-
	_stable_sort_by_ptr(_list_data_mut(&self), self.len, cmp_fn)
}

// Reports whether elements in the list are sorted in non-decreasing ascending order.
bind List[T].is_sorted = fn(self) bool:
	_is_sorted_ptr(self.data, self.len)

// Reports whether elements in the list are sorted according to comparator `cmp_fn`.
bind List[T].is_sorted_by = fn(self, cmp_fn: cl(T, T) int) bool:
	_is_sorted_by_ptr(self.data, self.len, cmp_fn)

// Searches a sorted list for `target` via binary search, returning its zero-based index or `none`.
bind List[T].binary_search = fn(self, target: T) ?int:
	_binary_search_ptr(self.data, self.len, target)

// Searches a sorted list using predicate comparator `cmp_fn`, returning a matching index or `none`.
//
// - `cmp_fn`: Receives an element and returns `< 0` if too small, `0` if matching, or `> 0` if too large.
bind List[T].binary_search_by = fn(self, cmp_fn: cl(T) int) ?int:
	_binary_search_by_ptr(self.data, self.len, cmp_fn)

// Returns the first index `i` in `0..=self.len` where `self[i] >= target` (first element not less than `target`).
bind List[T].lower_bound = fn(self, target: T) int:
	_lower_bound_ptr(self.data, self.len, target)

// Returns the first index `i` in `0..=self.len` where `self[i] > target` (first element strictly greater than `target`).
bind List[T].upper_bound = fn(self, target: T) int:
	_upper_bound_ptr(self.data, self.len, target)

// Reverses the order of elements in the list in place.
bind List[T].reverse = fn(&self): {
	if(self.len <= 1): func <-
	_reverse_ptr(_list_data_mut(&self), self.len)
}

// -----------------------------------------------------------------------------
// Methods Extending Array[T] (`[N: T]`)
// -----------------------------------------------------------------------------

// Sorts the fixed array or array view in place in ascending order using depth-bounded Introsort.
bind Array[T].sort = fn(&self): {
	if(self.len <= 1): func <-
	_sort_range(self.data, 0, self.len - 1, _max_depth(self.len))
}

// Sorts the fixed array or array view in place using custom comparator `cmp_fn`.
//
// - `cmp_fn`: Returns `< 0` when `a < b`, `0` when equal, or `> 0` when `a > b`.
bind Array[T].sort_by = fn(&self, cmp_fn: cl(T, T) int): {
	if(self.len <= 1): func <-
	_sort_by_range(self.data, 0, self.len - 1, _max_depth(self.len), cmp_fn)
}

// Sorts the fixed array or array view in place while preserving the relative order of equal keys.
bind Array[T].stable_sort = fn(&self):
	_stable_sort_ptr(self.data, self.len)

// Sorts the fixed array or array view in place using `cmp_fn` while preserving relative order of equal keys.
//
// - `cmp_fn`: Returns `< 0` when `a < b`, `0` when equal, or `> 0` when `a > b`.
bind Array[T].stable_sort_by = fn(&self, cmp_fn: cl(T, T) int):
	_stable_sort_by_ptr(self.data, self.len, cmp_fn)

// Reports whether elements in the array are sorted in non-decreasing ascending order.
bind Array[T].is_sorted = fn(self) bool:
	_is_sorted_ptr(self.data, self.len)

// Reports whether elements in the array are sorted according to comparator `cmp_fn`.
bind Array[T].is_sorted_by = fn(self, cmp_fn: cl(T, T) int) bool:
	_is_sorted_by_ptr(self.data, self.len, cmp_fn)

// Searches a sorted array for `target` via binary search, returning its zero-based index or `none`.
bind Array[T].binary_search = fn(self, target: T) ?int:
	_binary_search_ptr(self.data, self.len, target)

// Searches a sorted array using predicate comparator `cmp_fn`, returning a matching index or `none`.
//
// - `cmp_fn`: Receives an element and returns `< 0` if too small, `0` if matching, or `> 0` if too large.
bind Array[T].binary_search_by = fn(self, cmp_fn: cl(T) int) ?int:
	_binary_search_by_ptr(self.data, self.len, cmp_fn)

// Returns the first index `i` in `0..=self.len` where `self[i] >= target`.
bind Array[T].lower_bound = fn(self, target: T) int:
	_lower_bound_ptr(self.data, self.len, target)

// Returns the first index `i` in `0..=self.len` where `self[i] > target`.
bind Array[T].upper_bound = fn(self, target: T) int:
	_upper_bound_ptr(self.data, self.len, target)

// Reverses the order of elements in the fixed array or array view in place.
bind Array[T].reverse = fn(&self):
	_reverse_ptr(self.data, self.len)

// -----------------------------------------------------------------------------
// Collection Extensions (Set & Map)
// -----------------------------------------------------------------------------

// Collects all elements of this [`Set`] into a new sorted [`List`].
bind Set[T].to_sorted_list = fn(self) List[T]: {
	let out = self.to_list()
	out.sort()
	out
}

// Collects all keys of this [`Map`] into a new sorted [`List`].
bind Map[K, V].to_sorted_keys = fn(self) List[K]: {
	let out = self.keys()
	out.sort()
	out
}

// -----------------------------------------------------------------------------
// Standalone Functions
// -----------------------------------------------------------------------------

// Function form of [`List.sort`].
bind sort[T] = fn(list: &List[T]): list.sort()

// Function form of [`List.sort_by`].
bind sort_by[T] = fn(list: &List[T], cmp_fn: cl(T, T) int): list.sort_by(cmp_fn)

// Function form of [`List.stable_sort`].
bind stable_sort[T] = fn(list: &List[T]): list.stable_sort()

// Function form of [`List.stable_sort_by`].
bind stable_sort_by[T] = fn(list: &List[T], cmp_fn: cl(T, T) int): list.stable_sort_by(cmp_fn)

// Function form of [`List.is_sorted`] (accepts [`List`], `[T]`, `[N: T]`, and [`Array`] views).
bind is_sorted[T] = fn(list: List[T]) bool: list.is_sorted()

// Function form of [`List.is_sorted_by`] (accepts [`List`], `[T]`, `[N: T]`, and [`Array`] views).
bind is_sorted_by[T] = fn(list: List[T], cmp_fn: cl(T, T) int) bool: list.is_sorted_by(cmp_fn)

// Function form of [`List.binary_search`] (accepts [`List`], `[T]`, `[N: T]`, and [`Array`] views).
bind binary_search[T] = fn(list: List[T], target: T) ?int: list.binary_search(target)

// Function form of [`List.binary_search_by`] (accepts [`List`], `[T]`, `[N: T]`, and [`Array`] views).
bind binary_search_by[T] = fn(list: List[T], cmp_fn: cl(T) int) ?int: list.binary_search_by(cmp_fn)

// Function form of [`List.lower_bound`] (accepts [`List`], `[T]`, `[N: T]`, and [`Array`] views).
bind lower_bound[T] = fn(list: List[T], target: T) int: list.lower_bound(target)

// Function form of [`List.upper_bound`] (accepts [`List`], `[T]`, `[N: T]`, and [`Array`] views).
bind upper_bound[T] = fn(list: List[T], target: T) int: list.upper_bound(target)

// Function form of [`List.reverse`].
bind reverse[T] = fn(list: &List[T]): list.reverse()

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test_only
define _StablePair struct [
	key: int,
	order: int,
]

bind _StablePair.cmp = fn(self, other: _StablePair) int:
	if(self.key < other.key): -1
	else if(self.key > other.key): 1
	else: 0

@test_only
define _NonCopyRecord struct [
	id: int,
	name: string,
	tag: ptr[int],
]

bind _NonCopyRecord.drop = fn(&self): {
	if(not unsafe is_null(self.tag)): {
		unsafe free_raw(self.tag)
		self.tag = unsafe null_ptr[int]()
	}
}

bind _NonCopyRecord.cmp = fn(self, other: _NonCopyRecord) int:
	if(self.id < other.id): -1
	else if(self.id > other.id): 1
	else: 0

@test("sort list in-place and verify is_sorted, reverse, and large introsort")
bind test_sort_list_comparable = fn(): {
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

	// Larger reversed + sawtooth input to exercise multi-level Introsort partitioning
	let large = List[int].new()
	let i = 150
	while(i > 0): {
		large.push(i % 29 * 7 + i)
		i -= 1
	}
	assert(not large.is_sorted(), "large initially unsorted")
	large.sort()
	assert(large.is_sorted(), "large sorted")

	// Exercise explicit Heapsort fallback path (depth_limit == 0)
	let heap_nums = List[int].new()
	let h = 40
	while(h > 0): {
		heap_nums.push(h * 13 % 37)
		h -= 1
	}
	_sort_range(heap_nums.data, 0, heap_nums.len() - 1, 0)
	assert(heap_nums.is_sorted(), "introsort heapsort fallback sorts completely")

	// Exercise comparator Heapsort fallback path (depth_limit == 0)
	let cmp_desc = fn(a: int, b: int) int:
		if(a > b): -1
		else if(a < b): 1
		else: 0
	_sort_by_range(heap_nums.data, 0, heap_nums.len() - 1, 0, cmp_desc)
	assert(heap_nums.is_sorted_by(cmp_desc), "introsort comparator heapsort fallback sorts completely")
}

@test("sort list with custom comparator (descending)")
bind test_sort_custom_comparator = fn(): {
	let nums = List[int].new()
	nums.push(15)
	nums.push(3)
	nums.push(99)
	nums.push(42)

	let cmp_desc = fn(a: int, b: int) int:
		if(a > b): -1
		else if(a < b): 1
		else: 0

	assert(not nums.is_sorted_by(cmp_desc), "unsorted before sort_by")
	nums.sort_by(cmp_desc)
	assert(nums.is_sorted_by(cmp_desc), "sorted after sort_by")
	assert(nums.get(0) == 99, "desc[0] == 99")
	assert(nums.get(1) == 42, "desc[1] == 42")
	assert(nums.get(2) == 15, "desc[2] == 15")
	assert(nums.get(3) == 3, "desc[3] == 3")
}

@test("stable_sort and stable_sort_by preserve equal-key ordering")
bind test_sort_stable = fn(): {
	let small = [
		_StablePair[key: 3, order: 0],
		_StablePair[key: 1, order: 1],
		_StablePair[key: 3, order: 2],
		_StablePair[key: 2, order: 3],
		_StablePair[key: 1, order: 4],
		_StablePair[key: 3, order: 5],
	]
	small.stable_sort()
	assert(small.is_sorted(), "small stable_sort sorted")
	assert(small[0].key == 1 and small[0].order == 1, "small equal key 1 first")
	assert(small[1].key == 1 and small[1].order == 4, "small equal key 1 second")
	assert(small[2].key == 2 and small[2].order == 3, "small key 2")
	assert(small[3].key == 3 and small[3].order == 0, "small equal key 3 first")
	assert(small[4].key == 3 and small[4].order == 2, "small equal key 3 second")
	assert(small[5].key == 3 and small[5].order == 5, "small equal key 3 third")

	// Multi-run merge (> 16 elements) testing both ascending and custom descending stability
	let pairs = List[_StablePair].new()
	for(i in 0..75): {
		pairs.push(_StablePair[key: (74 - i) % 7, order: i])
	}
	pairs.stable_sort()
	assert(pairs.is_sorted(), "75-element stable_sort sorted")
	let idx = 0
	for(idx + 1 < pairs.len()): {
		assert(pairs[idx].key <= pairs[idx + 1].key, "keys non-decreasing")
		if(pairs[idx].key == pairs[idx + 1].key): {
			assert(pairs[idx].order < pairs[idx + 1].order, "equal-key order preserved across merges")
		}
		idx += 1
	}

	let rev_cmp = fn(a: _StablePair, b: _StablePair) int:
		if(a.key > b.key): -1
		else if(a.key < b.key): 1
		else: 0
	// Reset original order sequence sorted by order field before descending stable_sort_by
	let ordered = List[_StablePair].new()
	for(i in 0..50): {
		ordered.push(_StablePair[key: i * 3 % 5, order: i])
	}
	stable_sort_by(&ordered, rev_cmp)
	assert(is_sorted_by(ordered, rev_cmp), "stable_sort_by sorted")
	let k = 0
	for(k + 1 < ordered.len()): {
		if(ordered[k].key == ordered[k + 1].key): {
			assert(ordered[k].order < ordered[k + 1].order, "stable_sort_by preserves order on ties")
		}
		k += 1
	}

	let fixed_pairs = [
		4: _StablePair[key: 2, order: 0],
		_StablePair[key: 1, order: 1],
		_StablePair[key: 2, order: 2],
		_StablePair[key: 1, order: 3],
	]
	fixed_pairs.stable_sort()
	assert(fixed_pairs[0].order == 1 and fixed_pairs[1].order == 3, "Array stable_sort key 1 order")
	assert(fixed_pairs[2].order == 0 and fixed_pairs[3].order == 2, "Array stable_sort key 2 order")
	fixed_pairs.stable_sort_by(rev_cmp)
	assert(fixed_pairs[0].order == 0 and fixed_pairs[1].order == 2, "Array stable_sort_by key 2 order")
	assert(fixed_pairs[2].order == 1 and fixed_pairs[3].order == 3, "Array stable_sort_by key 1 order")
}

@test("binary_search, binary_search_by, lower_bound, and upper_bound on List, [T], [N: T], and Array[T]")
bind test_sort_binary_search_and_bounds = fn(): {
	let list = [10, 20, 20, 20, 30, 50]

	let idx1? = list.binary_search(30)
	assert(idx1? == 4, "binary_search 30 is index 4")
	let idx2? = list.binary_search(10)
	assert(idx2? == 0, "binary_search 10 is index 0")
	let idx3? = list.binary_search(25)
	assert(idx3? == none, "binary_search 25 is none")

	assert(list.lower_bound(5) == 0, "lower_bound below min")
	assert(list.lower_bound(20) == 1, "lower_bound duplicate hit")
	assert(list.lower_bound(25) == 4, "lower_bound between elements")
	assert(list.lower_bound(99) == 6, "lower_bound above max")

	assert(list.upper_bound(5) == 0, "upper_bound below min")
	assert(list.upper_bound(20) == 4, "upper_bound past duplicates")
	assert(list.upper_bound(25) == 4, "upper_bound between elements")
	assert(list.upper_bound(50) == 6, "upper_bound at max")

	let hit_by? = list.binary_search_by(fn(x: int) int: x - 30)
	assert(hit_by? == 4, "binary_search_by finds 30")
	let miss_by? = binary_search_by(list, fn(x: int) int: x - 35)
	assert(miss_by? == none, "binary_search_by misses 35")

	// Fixed stack array [N: T] methods and standalone views
	let fixed = [6: 50, 20, 10, 20, 40, 20]
	assert(not fixed.is_sorted(), "fixed array initially unsorted")
	assert(not is_sorted(fixed), "standalone is_sorted accepts [N: T]")
	fixed.sort()
	assert(fixed.is_sorted(), "fixed array sorted in place")
	assert(is_sorted(fixed), "standalone is_sorted confirms [N: T]")
	assert(fixed[0] == 10 and fixed[5] == 50, "fixed sorted bounds")
	assert(fixed.lower_bound(20) == 1, "fixed array lower_bound(20)")
	assert(fixed.upper_bound(20) == 4, "fixed array upper_bound(20)")
	assert(lower_bound(fixed, 20) == 1, "standalone lower_bound accepts [N: T]")
	assert(upper_bound(fixed, 20) == 4, "standalone upper_bound accepts [N: T]")
	assert(fixed.binary_search(40) == 4, "fixed array binary_search(40)")
	assert(binary_search(fixed, 40) == 4, "standalone binary_search accepts [N: T]")
	assert(fixed.binary_search_by(fn(x: int) int: x - 40) == 4, "fixed array binary_search_by(40)")
	assert(binary_search_by(fixed, fn(x: int) int: x - 40) == 4, "standalone binary_search_by accepts [N: T]")

	fixed.reverse()
	assert(fixed[0] == 50 and fixed[5] == 10, "fixed array reversed")
	let desc = fn(a: int, b: int) int: if(a > b): -1 else if(a < b): 1 else: 0
	assert(fixed.is_sorted_by(desc), "fixed array is_sorted_by desc")
	fixed.sort_by(fn(a: int, b: int) int: if(a < b): -1 else if(a > b): 1 else: 0)
	assert(fixed.is_sorted(), "fixed array sort_by ascending")

	// Array[T] slice-view struct methods and standalone views
	let view = Array[int][data: unsafe ptr_add(list.data, 1), len: 4]
	assert(view.is_sorted(), "Array[int] view is_sorted")
	assert(is_sorted(view), "standalone is_sorted accepts Array[int]")
	assert(view.lower_bound(20) == 0, "Array[int] lower_bound")
	assert(view.upper_bound(20) == 3, "Array[int] upper_bound")
	assert(lower_bound(view, 30) == 3, "standalone lower_bound accepts Array[int]")
	assert(upper_bound(view, 20) == 3, "standalone upper_bound accepts Array[int]")
	assert(view.binary_search(30) == 3, "Array[int] binary_search")
	assert(binary_search_by(view, fn(x: int) int: x - 30) == 3, "standalone binary_search_by accepts Array[int]")
}

@test("non-Copyable Resource structs sort, stable_sort, sort_by, and binary search with zero leaks")
bind test_sort_non_copyable_resources = fn(): {
	let items = List[_NonCopyRecord].new()
	for(i in 0..24): {
		let p = unsafe alloc_raw[int](1)
		unsafe ptr_write(p, i)
		let key = (23 - i) / 3
		items.push(_NonCopyRecord[id: key, name: "item_$i", tag: p])
	}

	assert(not items.is_sorted(), "non-copyable resources initially unsorted")
	items.stable_sort()
	assert(items.is_sorted(), "non-copyable resources sorted after stable_sort")

	let k = 0
	for(k + 1 < items.len()): {
		if(items[k].id == items[k + 1].id): {
			let cur_seq = unsafe ptr_read(items[k].tag)
			let next_seq = unsafe ptr_read(items[k + 1].tag)
			assert(cur_seq < next_seq, "stable_sort preserved equal-key order on non-Copyable resources")
		}
		k += 1
	}

	let empty_tag = unsafe null_ptr[int]()
	let probe = _NonCopyRecord[id: 5, name: "probe", tag: empty_tag]
	let found? = items.binary_search(probe)
	assert(found? != none, "binary_search without Copyable found matching id")
	let lb = items.lower_bound(probe)
	let ub = items.upper_bound(probe)
	assert(ub - lb == 3, "lower_bound..upper_bound covers all 3 records with id 5")

	let by_id? = items.binary_search_by(fn(r: _NonCopyRecord) int: r.id - 6)
	assert(by_id? != none and items[by_id? ?? 0].id == 6, "binary_search_by without Copyable works")

	let cmp_desc = fn(a: _NonCopyRecord, b: _NonCopyRecord) int:
		if(a.id > b.id): -1
		else if(a.id < b.id): 1
		else: 0
	items.sort_by(cmp_desc)
	assert(items.is_sorted_by(cmp_desc), "sort_by without Copyable sorts descending")
	items.sort()
	assert(items.is_sorted(), "introsort without Copyable sorts ascending")
}

@test("collection sorting extensions: Set.to_sorted_list and Map.to_sorted_keys")
bind test_sort_collections = fn(): {
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
