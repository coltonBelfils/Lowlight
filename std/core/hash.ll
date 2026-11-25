// std/core/hash — Built-in [`Hashable`] implementations for Lowlight primitive types.
//
// Scalar primitives use SplitMix64 mixing, normalized floating-point values map `-0.0`
// and `+0.0` identically, and [`string`] uses 64-bit FNV-1a over UTF-8 bytes.

// SplitMix64 64-bit integer hash mixer implemented in pure Lowlight.
const _hash_mix_u64 = fn(x: u64) u64: {
	let m1: u64 = 0xbf58476d1ce4e5b9
	let m2: u64 = 0x94d049bb133111eb
	let v: u64 = (x ^ x >> 30) * m1
	v = (v ^ v >> 27) * m2
	v ^ v >> 31
}

// Computes the SplitMix64 hash digest of this [`u64`] value.
const u64.hash = fn(self) u64: _hash_mix_u64(self)

// Computes the SplitMix64 hash digest of this [`u32`] value.
const u32.hash = fn(self) u64: _hash_mix_u64(u64(self))

// Computes the SplitMix64 hash digest of this [`u16`] value.
const u16.hash = fn(self) u64: _hash_mix_u64(u64(self))

// Computes the SplitMix64 hash digest of this [`u8`] byte value.
const u8.hash = fn(self) u64: _hash_mix_u64(u64(self))

// Computes the SplitMix64 hash digest of this [`int`] value.
const int.hash = fn(self) u64: _hash_mix_u64(u64(self))

// Computes the SplitMix64 hash digest of this [`i32`] value.
const i32.hash = fn(self) u64: _hash_mix_u64(u64(self))

// Computes the SplitMix64 hash digest of this [`i16`] value.
const i16.hash = fn(self) u64: _hash_mix_u64(u64(self))

// Computes the SplitMix64 hash digest of this [`i8`] value.
const i8.hash = fn(self) u64: _hash_mix_u64(u64(self))

// Computes a deterministic 64-bit hash digest for `true` (`1`) or `false` (`0`).
const bool.hash = fn(self) u64: _hash_mix_u64(if(self): u64(1) else: u64(0))

// Computes the SplitMix64 hash digest of this Unicode scalar [`char`].
const char.hash = fn(self) u64: _hash_mix_u64(u64(self))

// Computes a 64-bit hash digest from the IEEE-754 representation of this [`float`],
// normalizing `-0.0` to `+0.0`.
const float.hash = fn(self) u64: {
	let f = if(self == 0.0): 0.0 else: self
	_hash_mix_u64(f.to_bits())
}

// Computes a 64-bit hash digest for this [`f32`] value by widening to [`float`].
const f32.hash = fn(self) u64: float(self).hash()

// Computes a 64-bit FNV-1a hash digest across the raw UTF-8 bytes of the string.
const string.hash = fn(self) u64: {
	let h: u64 = 14695981039346656037
	let prime: u64 = 1099511628211
	let n = self.len()
	let i = 0
	for(i < n): {
		let b = u64(self.byte_at(i))
		h = (h ^ b) * prime
		i += 1
	}
	h
}
