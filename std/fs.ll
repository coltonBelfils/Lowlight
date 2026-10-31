// Lowlight Standard Library: std/fs.ll
// Generic Result[T, E] error-handling value type and safe file I/O helpers.

extern "stdio.h"

extern fopen as c_fopen: (path: ptr[u8], mode: ptr[u8]): ptr[u8]
extern fclose as c_fclose: (fp: ptr[u8]): i32
extern remove as c_remove: (path: ptr[u8]): i32

define Result[T, E] struct [
    value?: ?T,
    err?: ?E,
    ok_flag: bool,
]

bind Result[T, E].ok = (val: own T): Result[T, E] {
    Result[T, E][
        value?: own val,
        err?: none,
        ok_flag: true,
    ]
}

bind Result[T, E].fail = (e: own E): Result[T, E] {
    Result[T, E][
        value?: none,
        err?: own e,
        ok_flag: false,
    ]
}

bind Result[T, E].is_ok = (self): bool { self.ok_flag }

bind Result[T, E].is_err = (self): bool { not self.ok_flag }

bind Result[T, E].unwrap_or = (own self, fallback: own T): T {
    self.value? ?? own fallback
}

bind Result[T, E].unwrap = (own self): T {
    self.value? ?? panic("called Result.unwrap on an error")
}

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
