// =============================================================================
// Lowlight Core Hash Code Implementations (SplitMix64)
// =============================================================================

// SplitMix64 64-bit integer hash mixer implemented in pure Lowlight.
bind _hash_mix_u64 = (x: u64) u64: {
    let m1: u64 = 0xbf58476d1ce4e5b9
    let m2: u64 = 0x94d049bb133111eb
    let v: u64 = (x ^ (x >> 30)) * m1
    v = (v ^ (v >> 27)) * m2
    v ^ (v >> 31)
}

bind u64.hash = (self) u64: {
    _hash_mix_u64(self)
}

bind u32.hash = (self) u64: {
    _hash_mix_u64(u64(self))
}

bind u16.hash = (self) u64: {
    _hash_mix_u64(u64(self))
}

bind u8.hash = (self) u64: {
    _hash_mix_u64(u64(self))
}

bind int.hash = (self) u64: {
    _hash_mix_u64(u64(self))
}

bind i32.hash = (self) u64: {
    _hash_mix_u64(u64(self))
}

bind i16.hash = (self) u64: {
    _hash_mix_u64(u64(self))
}

bind i8.hash = (self) u64: {
    _hash_mix_u64(u64(self))
}

bind bool.hash = (self) u64: {
    _hash_mix_u64(if (self): u64(1) else: u64(0))
}

bind char.hash = (self) u64: {
    _hash_mix_u64(u64(self))
}

bind float.hash = (self) u64: {
    let f = if (self == 0.0): 0.0 else: self
    let u: u64 = unsafe {
        let p = addr_of(f)
        let up = ptr_cast[u64](p)
        *up
    }
    _hash_mix_u64(u)
}

bind f32.hash = (self) u64: {
    float(self).hash()
}

bind string.hash = (self) u64: {
    let h: u64 = 14695981039346656037
    let prime: u64 = 1099511628211
    let n = self.len()
    let i = 0
    for (i < n): {
        let b = u64(self.byte_at(i))
        h = (h ^ b) * prime
        i += 1
    }
    h
}
