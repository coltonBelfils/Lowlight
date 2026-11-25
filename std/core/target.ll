// std/core/target — Compile-time target platform introspection types for `comp.TARGET`.

@comptime_only
@lang("target_os")
@derive(Equatable, Comparable, Hashable, Printable)
define TargetOs enum [
	linux,
	darwin,
	windows,
	freebsd,
	openbsd,
	netbsd,
	wasm,
	unknown,
]

@comptime_only
@lang("target_arch")
@derive(Equatable, Comparable, Hashable, Printable)
define TargetArch enum [
	amd64,
	arm64,
	riscv64,
	x86,
	wasm32,
	wasm64,
	unknown,
]

@comptime_only
@lang("target_endian")
@derive(Equatable, Comparable, Hashable, Printable)
define Endian enum [
	little,
	big,
]

@comptime_only
@lang("target_info")
@derive(Equatable, Copyable, Printable)
define Target struct [
	os: TargetOs,
	arch: TargetArch,
	endian: Endian,
	is_unix: bool,
	ptr_size: int,
	ptr_bits: int,
	triple: string,
]
