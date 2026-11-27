// std/core/comp — Compile-time type reflection descriptor types in the `comp` namespace.

@comptime_only
@lang("comp_type_kind")
@derive(Equatable, Comparable, Hashable, Printable)
define TypeKind enum [
	primitive_kind,
	string_kind,
	optional_kind,
	list_kind,
	array_kind,
	set_kind,
	map_kind,
	tuple_kind,
	struct_kind,
	tag_enum_kind,
	tagged_union_kind,
	result_kind,
	distinct_kind,
	interface_kind,
	fn_kind,
	cl_kind,
	extern_fn_kind,
	ptr_kind,
	ref_kind,
	arena_kind,
	range_kind,
]

@comptime_only
@lang("comp_attr_info")
@derive(Copyable, Printable)
define AttrInfo struct [
	name: string,
	args: [string],
]

@comptime_only
@lang("comp_field_info")
@derive(Copyable, Printable)
define FieldInfo struct [
	name: string,
	type_name: string,
	is_opt: bool,
	is_public: bool,
	has_default: bool,
	attrs: [AttrInfo],
]

@comptime_only
@lang("comp_variant_info")
@derive(Copyable, Printable)
define VariantInfo struct [
	name: string,
	index: int,
	tag_value?: ?int,
	has_payload: bool,
	is_public: bool,
	attrs: [AttrInfo],
]

@comptime_only
@lang("comp_elem_info")
@derive(Equatable, Copyable, Printable)
define ElemInfo struct [
	index: int,
	type_name: string,
]

@comptime_only
@lang("comp_distinct_info")
@derive(Equatable, Copyable, Printable)
define DistinctInfo struct [
	type_name: string,
]
