// Lowlight Standard Library: std/fs.ll
// Safe file I/O helpers and filesystem operations.

extern "stdio.h"

extern fopen as c_fopen: (path: ptr[u8], mode: ptr[u8]): ptr[u8]
extern fclose as c_fclose: (fp: ptr[u8]): i32
extern remove as c_remove: (path: ptr[u8]): i32

bind exists = (path: string): bool {
    unsafe {
        let fp = c_fopen(path.c_str(), "rb".c_str())
        if (fp == null_ptr[u8]()) {
            func <- false
        }
        c_fclose(fp)
        func <- true
    }
}

bind remove_file = (path: string): bool unsafe {
    c_remove(path.c_str()) == i32(0)
}

bind read_text = (path: string): Result[string, string] {
    let data?: ?string = read_file(path)
    if (data?) {
        func <- Result[string, string].ok(data.copy())
    }
    func <- Result[string, string].fail("failed to read file: $path")
}

bind write_text = (path: string, content: string): Result[bool, string] {
    if (write_file(path, content)) {
        func <- Result[bool, string].ok(true)
    }
    func <- Result[bool, string].fail("failed to write file: $path")
}
