package types

// SetElem reports whether t is the set type `#[T]` (the `@lang("set")` struct
// `Set[T]` from std/core) and returns its element type.
func SetElem(t Type) (Type, bool) {
	if st, ok := t.(*Struct); ok && st.Lang == "set" && len(st.TypeArgs) == 1 {
		return st.TypeArgs[0], true
	}
	return nil, false
}

// MapKV reports whether t is the map type `#[K: V]` (the `@lang("map")` struct
// `Map[K, V]` from std/core) and returns its key and value types.
func MapKV(t Type) (Type, Type, bool) {
	if st, ok := t.(*Struct); ok && st.Lang == "map" && len(st.TypeArgs) == 2 {
		return st.TypeArgs[0], st.TypeArgs[1], true
	}
	return nil, nil, false
}

// CollView returns a structural *Set or *Map descriptor for the set and map
// structs, and t unchanged for every other type. Checks that only care about
// a collection's element types switch on the result.
func CollView(t Type) Type {
	if el, ok := SetElem(t); ok {
		return &Set{Elem: el}
	}
	if k, v, ok := MapKV(t); ok {
		return &Map{Key: k, Value: v}
	}
	return t
}
