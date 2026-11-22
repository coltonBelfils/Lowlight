// examples/unsafe_and_ffi.ll
// Showcases Lowlight's low-level systems capabilities:
// - `define packed` binary wire-header layout (`size_of` / `align_of`)
// - Explicit `unsafe { ... }` blocks with typed raw pointers (`ptr[T]`)
// - `alloc_raw`, `ptr_write`, `ptr_read`, `ptr_add`, `ptr_cast`, `copy_bytes`, `zero_bytes`, `free_raw`
// - Pure Lowlight raw byte-buffer operations (`raw_strlen` / `raw_memcmp`)

define packed WireHeader struct [
    magic: u16,
    version: u8,
    flags: u8,
    payload_len: u32,
]

bind checksum_bytes = fn(buf: ptr[u8], count: int) u32: {
    unsafe {
        let sum: u32 = 0
        for (i in 0..count): {
            let b = *ptr_add(buf, i)
            sum = (sum << 3) ^ u32(b)
        }
        sum
    }
}

bind main = fn(): {
    // 1. Packed wire layout verification: 2 + 1 + 1 + 4 == 8 bytes exact, alignment == 1!
    assert(size_of[WireHeader]() == 8)
    assert(align_of[WireHeader]() == 1)

    // 2. Manual raw buffer serialization & deserialization inside `unsafe`
    let payload = "LOWLIGHT_PACKET"
    let total_len = size_of[WireHeader]() + payload.len()

    unsafe {
        let raw_buf = alloc_raw[u8](total_len)
        zero_bytes(raw_buf, total_len)

        let hdr_ptr = ptr_cast[WireHeader](raw_buf)
        let hdr = WireHeader[
            magic: 0x4C4C,
            version: 1,
            flags: 0x07,
            payload_len: u32(payload.len()),
        ]
        ptr_write(hdr_ptr, hdr)

        let body_ptr = ptr_add(raw_buf, size_of[WireHeader]())
        copy_bytes(body_ptr, payload.c_str(), payload.len())

        // Read back header & verify C FFI `strlen` / `memcmp`
        let decoded = ptr_read(hdr_ptr)
        assert(decoded.magic == u16(0x4C4C))
        assert(decoded.version == u8(1))
        assert(decoded.flags == u8(0x07))
        assert(int(decoded.payload_len) == payload.len())

        let c_len = raw_strlen(payload.c_str())
        assert(c_len == payload.len())
        assert(raw_memcmp(body_ptr, payload.c_str(), c_len) == i32(0))

        let csum = checksum_bytes(raw_buf, total_len)
        assert(csum > u32(0))

        free_raw(raw_buf)
        println("unsafe_and_ffi: WireHeader=${size_of[WireHeader]()}B, payload=${c_len}B, checksum=$csum")
    }
}
