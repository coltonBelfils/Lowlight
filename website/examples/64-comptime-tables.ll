// Title: Compile-time lookup tables
// Category: Compile time
// Order: 64

import "std/strconv"

// `@comptime_only` helpers run only inside the compiler and are
// stripped from the final executable. Here we generate a 256-entry
// IEEE CRC32 lookup table at compile time.
@comptime_only
const _build_crc32_table = fn() [256: u32]: {
	let table = [256: u32(0)]
	for(i in 0..256): {
		let c = u32(i)
		for(_ in 0..8): {
			if((c & u32(1)) != u32(0)): c = u32(0xedb88320) ^ (c >> 1)
			else: c = c >> 1
		}
		table[i] = c
	}
	table
}

// Baked directly into read-only data (`.rodata`): zero startup cost.
const CRC32_TABLE = _build_crc32_table()

// Marked `const`, so `crc32` works both in `const` initializers and at runtime.
const crc32 = fn(data: string) u32: {
	let crc = u32(0xffffffff)
	for(i in 0..data.len()): {
		let idx = int((crc ^ u32(data.byte_at(i))) & u32(0xff))
		crc = CRC32_TABLE[idx] ^ (crc >> 8)
	}
	crc ^ u32(0xffffffff)
}

const CHECK_VECTOR = crc32("123456789")

bind main = fn(): {
	let rt = crc32("hello lowlight")
	println("check=0x${strconv.int_to_hex(int(CHECK_VECTOR), false)}")
	println("runtime=0x${strconv.int_to_hex(int(rt), false)}")
}
