// std/core/optional — Generic equality, copying, and formatting implementations
// for optional types (`?T`), including both flat value optionals and recursive
// boxed optionals.

// Reports whether two optional values are both `none` or hold equal values.
const _opt_eq[T] = fn(a?: ?T, b?: ?T) bool: {
	if(a? as va): {
		if(b? as vb): {
			func <- va == vb
		}
		func <- false
	}
	if(b? as _vb): {
		func <- false
	}
	true
}

// Returns an independent deep copy of an optional value (preserving `none`).
const _opt_copy[T] = fn(v?: ?T) ?T: {
	if(v? as inner): {
		let res?: ?T = inner.copy()
		func <- res?
	}
	none
}

// Formats an optional value (`"none"` when absent, or the inner value's string).
const _opt_to_string[T] = fn(v?: ?T) string: {
	if(v? as inner): {
		func <- inner.to_string()
	}
	"none"
}
