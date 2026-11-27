// Title: Const generics and targets
// Category: Compile time
// Order: 66

// Const generic value parameters (`[N: const int]`) check array sizes
// statically and monomorphize against canonical constant values.
define Vec[N: const int] struct [
	items: [N: int],
]

const Vec[N: const int].sum = fn(self) int: {
	let total = 0
	for(i in 0..N): total += self.items[i]
	total
}

// `comp.TARGET` exposes target properties at compile time inside
// `comptime if`, avoiding runtime branches or separate target files.
const endian_label = fn() string:
	comptime if(comp.TARGET.endian == comp.Endian.little): "little-endian"
	else: "big-endian"

bind main = fn(): {
	let v = Vec[2 + 2][items: [4: 1, 2, 3, 4]]
	println("sum=${v.sum()}")
	println("${comp.TARGET.ptr_bits}-bit ${endian_label()}")
}
