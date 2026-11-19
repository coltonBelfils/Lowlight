import "std/fs"

bind test_unsafe_call = (): {
    let msg = "Hello from Lowlight!"
    // unsafe expression reading raw null-terminated byte buffer length
    let len = unsafe raw_strlen(msg.c_str())
    assert(len == 20)
}

bind test_unsafe_alloc_and_deref = (): {
    // unsafe expression initializing a pointer
    let p = unsafe alloc_raw[int](4)
    
    // unsafe expression assigning to dereferenced pointer
    unsafe *p = 100
    unsafe *ptr_add(p, 1) = 200
    unsafe *ptr_add(p, 2) = 300
    unsafe *ptr_add(p, 3) = 400

    // unsafe expression reading dereferenced pointer
    let v0 = unsafe *p
    let v1 = unsafe *ptr_add(p, 1)
    let v2 = unsafe *ptr_add(p, 2)
    let v3 = unsafe *ptr_add(p, 3)

    assert(v0 == 100)
    assert(v1 == 200)
    assert(v2 == 300)
    assert(v3 == 400)

    // unsafe indexing
    let idx_val = unsafe p[2]
    assert(idx_val == 300)

    // unsafe expression inside condition
    if (unsafe *p == 100): {
        unsafe *p = 999
    }
    assert(unsafe *p == 999)

    // unsafe expression statement
    unsafe free_raw(p)
}

bind test_unsafe_parenthesized_and_binary = (): {
    let p = unsafe alloc_raw[int](1)
    unsafe *p = 42

    let matches = unsafe (*p == 42)
    assert(matches)

    let sum = (unsafe *p) + 8
    assert(sum == 50)

    unsafe free_raw(p)
}

bind test_unsafe_block = (): {
    // Traditional unsafe block expression still works seamlessly
    let res = unsafe {
        let p = alloc_raw[int](1)
        *p = 77
        let v = *p
        free_raw(p)
        v
    }
    assert(res == 77)
}

bind main = (): {
    test_unsafe_call()
    test_unsafe_alloc_and_deref()
    test_unsafe_parenthesized_and_binary()
    test_unsafe_block()
    println("All unsafe expression tests passed successfully!")
}
