// std/derive — Compile-time `@derivable` implementations for `Equatable`,
// `Comparable`, `Hashable`, `Copyable`, and `Printable`.

// Combines an accumulated hash with the hash of the next field or payload.
@inline
const _hash_combine = fn(h: u64, k: u64) u64:
	h ^ (k + u64(0x9e3779b97f4a7c15) + (h << 6) + (h >> 2))

// Strips generic type arguments (`Box[int]` -> `Box`) for `to_string` output.
@comptime_only
const _base_type_name = fn(full: string) string: {
	let i = 0
	for(i < full.len()): {
		if(full.byte_at(i) == u8('[')): func <- full[0..i].to_string()
		i += 1
	}
	full.copy()
}

// Generic structural equality derivation for structs, ranges, tag enums, tagged unions, and results.
const eq[T] = fn(a: T, b: T) bool: {
	comptime if(comp.kind_of[T]() == comp.TypeKind.struct_kind or comp.kind_of[T]() == comp.TypeKind.range_kind): {
		comptime for(f in comp.fields_of[T]()): {
			if(a.field(f) != b.field(f)): func <- false
		}
		true
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tag_enum_kind): {
		variant_index(a) == variant_index(b)
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tagged_union_kind or comp.kind_of[T]() == comp.TypeKind.result_kind): {
		if(variant_index(a) != variant_index(b)): func <- false
		comptime for(vr in comp.variants_of[T]()): {
			comptime if(vr.has_payload): {
				if(a.as_variant(vr) as pa): {
					if(b.as_variant(vr) as pb): {
						func <- pa == pb
					}
					func <- false
				}
			} else: {
				if(a.is_variant(vr)): func <- true
			}
		}
		false
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.distinct_kind): {
		a.underlying() == b.underlying()
	} else: {
		comp.compile_error("`@derive(Equatable)` is only supported on structs, enums, and distinct types")
	}
}

// Generic lexicographic ordering derivation for structs, ranges, and enums.
const cmp[T] = fn(a: T, b: T) int: {
	comptime if(comp.kind_of[T]() == comp.TypeKind.struct_kind or comp.kind_of[T]() == comp.TypeKind.range_kind): {
		comptime for(f in comp.fields_of[T]()): {
			if(a.field(f) < b.field(f)): func <- -1
			if(a.field(f) > b.field(f)): func <- 1
		}
		0
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tag_enum_kind): {
		let ia = variant_index(a)
		let ib = variant_index(b)
		if(ia < ib): func <- -1
		if(ia > ib): func <- 1
		0
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tagged_union_kind or comp.kind_of[T]() == comp.TypeKind.result_kind): {
		let ia = variant_index(a)
		let ib = variant_index(b)
		if(ia < ib): func <- -1
		if(ia > ib): func <- 1
		comptime for(vr in comp.variants_of[T]()): {
			comptime if(vr.has_payload): {
				if(a.as_variant(vr) as pa): {
					if(b.as_variant(vr) as pb): {
						if(pa < pb): func <- -1
						if(pa > pb): func <- 1
						func <- 0
					}
				}
			}
		}
		0
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.distinct_kind): {
		if(a.underlying() < b.underlying()): func <- -1
		if(a.underlying() > b.underlying()): func <- 1
		0
	} else: {
		comp.compile_error("`@derive(Comparable)` is only supported on structs, enums, and distinct types")
	}
}

// Generic 64-bit hash derivation for structs, ranges, and enums.
const hash[T] = fn(v: T) u64: {
	comptime if(comp.kind_of[T]() == comp.TypeKind.struct_kind or comp.kind_of[T]() == comp.TypeKind.range_kind): {
		let h: u64 = 14695981039346656037
		comptime for(f in comp.fields_of[T]()): {
			h = _hash_combine(h, v.field(f).hash())
		}
		h
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tag_enum_kind): {
		u64(variant_index(v)).hash()
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tagged_union_kind or comp.kind_of[T]() == comp.TypeKind.result_kind): {
		let h = u64(variant_index(v)).hash()
		comptime for(vr in comp.variants_of[T]()): {
			comptime if(vr.has_payload): {
				if(v.as_variant(vr) as p): {
					func <- _hash_combine(h, p.hash())
				}
			}
		}
		h
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.distinct_kind): {
		v.underlying().hash()
	} else: {
		comp.compile_error("`@derive(Hashable)` is only supported on structs, enums, and distinct types")
	}
}

// Generic deep copy derivation for structs, ranges, and enums.
const copy[T] = fn(v: T) T: {
	comptime if(comp.kind_of[T]() == comp.TypeKind.struct_kind or comp.kind_of[T]() == comp.TypeKind.range_kind): {
		comptime for(f in comp.fields_of[T]()) T: {
			comptime if(f.is_opt): {
				v.field(f)?.copy()
			} else: {
				v.field(f).copy()
			}
		}
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tag_enum_kind): {
		v
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tagged_union_kind or comp.kind_of[T]() == comp.TypeKind.result_kind): {
		comptime if(comp.variants_of[T]().len() == 0): {
			comp.compile_error("`@derive(Copyable)` requires at least one enum variant")
		} else: {
			comptime for(vr in comp.variants_of[T]()): {
				comptime if(vr.index < comp.variants_of[T]().len() - 1): {
					comptime if(vr.has_payload): {
						if(v.as_variant(vr) as p): func <- make_variant[T](vr, p.copy())
					} else: {
						if(v.is_variant(vr)): func <- make_variant[T](vr)
					}
				}
			}
			comptime if(comp.variants_of[T]()[comp.variants_of[T]().len() - 1].has_payload): {
				make_variant[T](comp.variants_of[T]()[comp.variants_of[T]().len() - 1], v.payload(comp.variants_of[T]()[comp.variants_of[T]().len() - 1]).copy())
			} else: {
				make_variant[T](comp.variants_of[T]()[comp.variants_of[T]().len() - 1])
			}
		}
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.distinct_kind): {
		T(v.underlying().copy())
	} else: {
		comp.compile_error("`@derive(Copyable)` is only supported on structs, enums, and distinct types")
	}
}

// Generic debug/display formatting derivation for structs, ranges, and enums.
const to_string[T] = fn(v: T) string: {
	let base = comptime _base_type_name(comp.type_name[T]())
	comptime if(comp.kind_of[T]() == comp.TypeKind.struct_kind or comp.kind_of[T]() == comp.TypeKind.range_kind): {
		let out = base
		out.push("[")
		comptime for(f in comp.fields_of[T]()): {
			comptime if(f.name != comp.fields_of[T]()[0].name): {
				out.push(", ")
			}
			out.push(f.name)
			out.push(": ")
			comptime if(f.is_opt): {
				if(v.field(f) as inner): {
					out.push(inner.to_string())
				} else: {
					out.push("none")
				}
			} else: {
				out.push(v.field(f).to_string())
			}
		}
		out.push("]")
		out
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tag_enum_kind): {
		comptime for(vr in comp.variants_of[T]()): {
			if(v.is_variant(vr)): func <- "${base}.${vr.name}"
		}
		base
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.tagged_union_kind or comp.kind_of[T]() == comp.TypeKind.result_kind): {
		comptime for(vr in comp.variants_of[T]()): {
			comptime if(vr.has_payload): {
				if(v.as_variant(vr) as p): {
					func <- "${base}.${vr.name}(${p.to_string()})"
				}
			} else: {
				if(v.is_variant(vr)): func <- "${base}.${vr.name}"
			}
		}
		base
	} else comptime if(comp.kind_of[T]() == comp.TypeKind.distinct_kind): {
		"${base}(${v.underlying().to_string()})"
	} else: {
		comp.compile_error("`@derive(Printable)` is only supported on structs, enums, and distinct types")
	}
}
