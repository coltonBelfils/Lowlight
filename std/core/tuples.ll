// std/core/tuples — Generic equality, ordering, hashing, copying, and
// formatting implementations for tuple types (`+[A, B, ...]`) over compile-time
// reflection (`comp.elems_of[T]()`).

// Reports whether two tuples of the same type are structurally equal.
const _tuple_eq[T] = fn(a: T, b: T) bool: {
	comptime for(e in comp.elems_of[T]()): {
		if(a.elem(e) != b.elem(e)): func <- false
	}
	true
}

// Compares two tuples lexicographically by element order, returning -1, 0, or 1.
const _tuple_cmp[T] = fn(a: T, b: T) int: {
	comptime for(e in comp.elems_of[T]()): {
		if(a.elem(e) < b.elem(e)): func <- -1
		if(a.elem(e) > b.elem(e)): func <- 1
	}
	0
}

// Computes a deterministic 64-bit hash combining all elements of a tuple.
const _tuple_hash[T] = fn(v: T) u64: {
	let h: u64 = 14695981039346656037
	comptime for(e in comp.elems_of[T]()): {
		let k = v.elem(e).hash()
		h = h ^ (k + u64(0x9e3779b97f4a7c15) + (h << 6) + (h >> 2))
	}
	h
}

// Returns a deep copy of the tuple by copying each element.
const _tuple_copy[T] = fn(v: T) T: {
	comptime for(e in comp.elems_of[T]()) T: {
		v.elem(e).copy()
	}
}

// Formats the tuple using canonical Lowlight tuple syntax (`"+[a, b, ...]"`).
const _tuple_to_string[T] = fn(v: T) string: {
	let out = "+["
	comptime for(e in comp.elems_of[T]()): {
		comptime if(e.index > 0): {
			out.push(", ")
		}
		out.push(v.elem(e).to_string())
	}
	out.push("]")
	out
}
