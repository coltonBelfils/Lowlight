// Lowlight Standard Library: std/fs.ll
// Robust file I/O operations, stream handling, and filesystem manipulation.

extern "stdio.h"

extern fopen as c_fopen: (path: ptr[u8], mode: ptr[u8]): ptr[u8]
extern fclose as c_fclose: (fp: ptr[u8]): i32
extern fread as c_fread: (buf: ptr[u8], size: int, count: int, fp: ptr[u8]): int
extern fwrite as c_fwrite: (buf: ptr[u8], size: int, count: int, fp: ptr[u8]): int
extern fseek as c_fseek: (fp: ptr[u8], offset: int, whence: i32): i32
extern ftell as c_ftell: (fp: ptr[u8]): int
extern fflush as c_fflush: (fp: ptr[u8]): i32
extern remove as c_remove: (path: ptr[u8]): i32
extern rename as c_rename: (old_path: ptr[u8], new_path: ptr[u8]): i32
extern ll_str_from_raw: (data: ptr[u8], len: int): string

// -----------------------------------------------------------------------------
// Seek Constants
// -----------------------------------------------------------------------------

bind SEEK_SET: i32 = 0
bind SEEK_CUR: i32 = 1
bind SEEK_END: i32 = 2

// -----------------------------------------------------------------------------
// File Stream Type with RAII Drop
// -----------------------------------------------------------------------------

define File struct [
    handle: ptr[u8],
    is_open: bool,
]

// Automatically closes the file stream when it goes out of scope.
bind File.drop = (&self) {
    if (self.is_open and unsafe (self.handle != null_ptr[u8]())) {
        unsafe c_fclose(self.handle)
        self.is_open = false
        self.handle = unsafe null_ptr[u8]()
    }
}

// Opens a file stream with the given C mode (e.g. "rb", "wb", "ab", "r+b").
bind File.open = (path: string, mode: string): Result[File, string] {
    unsafe {
        let fp = c_fopen(path.c_str(), mode.c_str())
        if (fp == null_ptr[u8]()) {
            Result[File, string].fail("failed to open file \"$path\" with mode \"$mode\"")
        } else {
            Result[File, string].ok(File[handle: fp, is_open: true])
        }
    }
}

// Opens an existing file for reading in binary mode ("rb").
bind File.open_read = (path: string): Result[File, string] {
    File.open(path, "rb")
}

// Opens a file for writing in binary mode ("wb"), creating or truncating it.
bind File.open_write = (path: string): Result[File, string] {
    File.open(path, "wb")
}

// Opens a file for appending in binary mode ("ab"), creating it if it does not exist.
bind File.open_append = (path: string): Result[File, string] {
    File.open(path, "ab")
}

// Opens a file for both reading and writing in binary mode ("r+b").
bind File.open_read_write = (path: string): Result[File, string] {
    File.open(path, "r+b")
}

// Returns true if the file stream is currently open and valid.
bind File.is_valid = (self): bool {
    self.is_open and unsafe (self.handle != null_ptr[u8]())
}

// Closes the file stream explicitly. Returns true if successfully closed.
bind File.close = (&self): bool {
    if (not self.is_open or unsafe (self.handle == null_ptr[u8]())) {
        func <- false
    }
    let res = unsafe c_fclose(self.handle) == 0
    self.is_open = false
    self.handle = unsafe null_ptr[u8]()
    res
}

// Flushes any buffered data to the underlying filesystem.
bind File.flush = (&self): bool {
    if (not self.is_valid()) func <- false
    (unsafe c_fflush(self.handle)) == 0
}

// Sets the file position indicator.
// `whence` must be SEEK_SET (0), SEEK_CUR (1), or SEEK_END (2).
// Returns the new byte offset from the start of the file.
bind File.seek = (&self, offset: int, whence: i32): Result[int, string] {
    if (not self.is_valid()) func <- Result[int, string].fail("file is not open")
    unsafe {
        let rc = c_fseek(self.handle, offset, whence)
        if (rc != 0) {
            Result[int, string].fail("fseek failed")
        } else {
            let pos = c_ftell(self.handle)
            Result[int, string].ok(pos)
        }
    }
}

// Returns the current byte offset of the file position indicator.
bind File.tell = (&self): Result[int, string] {
    if (not self.is_valid()) func <- Result[int, string].fail("file is not open")
    unsafe {
        let pos = c_ftell(self.handle)
        if (pos < 0) {
            Result[int, string].fail("ftell failed")
        } else {
            Result[int, string].ok(pos)
        }
    }
}

// Returns the total size of the file in bytes, preserving the current seek position.
bind File.size = (&self): Result[int, string] {
    if (not self.is_valid()) func <- Result[int, string].fail("file is not open")
    unsafe {
        let saved = c_ftell(self.handle)
        if (saved < 0) func <- Result[int, string].fail("failed to query file position")
        if (c_fseek(self.handle, 0, SEEK_END) != 0) {
            func <- Result[int, string].fail("failed to seek to end")
        }
        let total = c_ftell(self.handle)
        c_fseek(self.handle, saved, SEEK_SET)
        Result[int, string].ok(total)
    }
}

// Writes a string to the file. Returns the number of bytes written.
bind File.write_string = (&self, s: string): Result[int, string] {
    if (not self.is_valid()) func <- Result[int, string].fail("file is not open")
    if (s.len() == 0) func <- Result[int, string].ok(0)
    unsafe {
        let written = c_fwrite(s.c_str(), 1, s.len(), self.handle)
        if (written < s.len()) {
            Result[int, string].fail("short write: wrote $written of ${s.len()} bytes")
        } else {
            Result[int, string].ok(written)
        }
    }
}

// Writes a byte slice/list to the file. Returns the number of bytes written.
bind File.write_bytes = (&self, data: [u8]): Result[int, string] {
    if (not self.is_valid()) func <- Result[int, string].fail("file is not open")
    if (data.len() == 0) func <- Result[int, string].ok(0)
    unsafe {
        let raw = alloc_raw[u8](data.len())
        for (i in 0..data.len()) {
            ptr_write(ptr_add(raw, i), data[i])
        }
        let written = c_fwrite(raw, 1, data.len(), self.handle)
        free_raw(raw)
        if (written < data.len()) {
            Result[int, string].fail("short write: wrote $written of ${data.len()} bytes")
        } else {
            Result[int, string].ok(written)
        }
    }
}

// Reads up to `max_bytes` from the current file position into a List[u8].
bind File.read_bytes = (&self, max_bytes: int): Result[[u8], string] {
    if (not self.is_valid()) func <- Result[[u8], string].fail("file is not open")
    if (max_bytes <= 0) func <- Result[[u8], string].ok([])
    unsafe {
        let raw = alloc_raw[u8](max_bytes)
        let n = c_fread(raw, 1, max_bytes, self.handle)
        let out: [u8] = []
        for (i in 0..n) {
            out.push(raw[i])
        }
        free_raw(raw)
        Result[[u8], string].ok(own out)
    }
}

// Reads all remaining data from the file position into a string.
bind File.read_to_string = (&self): Result[string, string] {
    if (not self.is_valid()) func <- Result[string, string].fail("file is not open")
    unsafe {
        let chunk_size = 8192
        let raw = alloc_raw[u8](chunk_size)
        let out = ""
        let n = c_fread(raw, 1, chunk_size, self.handle)
        while (n > 0) {
            let chunk = ll_str_from_raw(raw, n)
            out.push(chunk)
            n = c_fread(raw, 1, chunk_size, self.handle)
        }
        free_raw(raw)
        Result[string, string].ok(own out)
    }
}

// -----------------------------------------------------------------------------
// Top-Level Convenience Functions
// -----------------------------------------------------------------------------

// Reads the entire contents of a file into a string.
bind read_file = (path: string): Result[string, string] {
    let f_res = File.open_read(path)
    if (f_res.is_err()) {
        func <- Result[string, string].fail("failed to open file \"$path\"")
    }
    let f = f_res.unwrap()
    f.read_to_string()
}

// Alias for `read_file`.
bind read_to_string = (path: string): Result[string, string] {
    read_file(path)
}

// Backward-compatible alias for `read_file`.
bind read_text = (path: string): Result[string, string] {
    read_file(path)
}

// Writes the given string contents to a file, replacing any existing content.
bind write_file = (path: string, content: string): Result[bool, string] {
    let f_res = File.open_write(path)
    if (f_res.is_err()) {
        func <- Result[bool, string].fail("failed to open file \"$path\" for writing")
    }
    let f = f_res.unwrap()
    let w_res = f.write_string(content)
    if (w_res.is_err()) {
        Result[bool, string].fail("failed to write to \"$path\"")
    } else {
        Result[bool, string].ok(true)
    }
}

// Alias for `write_file`.
bind write_to_file = (path: string, content: string): Result[bool, string] {
    write_file(path, content)
}

// Backward-compatible alias for `write_file`.
bind write_text = (path: string, content: string): Result[bool, string] {
    write_file(path, content)
}

// Appends the given string contents to the end of a file.
bind append_file = (path: string, content: string): Result[bool, string] {
    let f_res = File.open_append(path)
    if (f_res.is_err()) {
        func <- Result[bool, string].fail("failed to open file \"$path\" for appending")
    }
    let f = f_res.unwrap()
    let w_res = f.write_string(content)
    if (w_res.is_err()) {
        Result[bool, string].fail("failed to append to \"$path\"")
    } else {
        Result[bool, string].ok(true)
    }
}

// Reads the entire contents of a file into a List[u8] of bytes.
bind read_bytes = (path: string): Result[[u8], string] {
    let f_res = File.open_read(path)
    if (f_res.is_err()) {
        func <- Result[[u8], string].fail("failed to open file \"$path\"")
    }
    let f = f_res.unwrap()
    let sz_res = f.size()
    let sz = if (sz_res.is_ok()) sz_res.unwrap() else 8192
    f.read_bytes(sz)
}

// Writes a List[u8] of bytes to a file.
bind write_bytes = (path: string, data: [u8]): Result[bool, string] {
    let f_res = File.open_write(path)
    if (f_res.is_err()) {
        func <- Result[bool, string].fail("failed to open file \"$path\" for writing")
    }
    let f = f_res.unwrap()
    let w_res = f.write_bytes(data)
    if (w_res.is_err()) {
        Result[bool, string].fail("failed to write bytes to \"$path\"")
    } else {
        Result[bool, string].ok(true)
    }
}

// Returns true if a file exists and can be opened for reading.
bind exists = (path: string): bool {
    let f_res = File.open_read(path)
    f_res.is_ok()
}

// Returns the size of a file in bytes.
bind file_size = (path: string): Result[int, string] {
    let f_res = File.open_read(path)
    if (f_res.is_err()) func <- Result[int, string].fail("failed to open file \"$path\"")
    let f = f_res.unwrap()
    f.size()
}

// Deletes a file from the filesystem. Returns true if successful.
bind remove_file = (path: string): bool {
    unsafe {
        c_remove(path.c_str()) == 0
    }
}

// Renames or moves a file from `old_path` to `new_path`. Returns true if successful.
bind rename_file = (old_path: string, new_path: string): bool {
    unsafe {
        c_rename(old_path.c_str(), new_path.c_str()) == 0
    }
}

// Copies a file from `src_path` to `dst_path`. Returns the total number of bytes copied.
bind copy_file = (src_path: string, dst_path: string): Result[int, string] {
    let src_res = File.open_read(src_path)
    if (src_res.is_err()) func <- Result[int, string].fail("failed to open source \"$src_path\"")
    let dst_res = File.open_write(dst_path)
    if (dst_res.is_err()) func <- Result[int, string].fail("failed to open destination \"$dst_path\"")
    let src = src_res.unwrap()
    let dst = dst_res.unwrap()

    unsafe {
        let chunk_size = 16384
        let raw = alloc_raw[u8](chunk_size)
        let total_copied = 0
        let n = c_fread(raw, 1, chunk_size, src.handle)
        while (n > 0) {
            let written = c_fwrite(raw, 1, n, dst.handle)
            if (written < n) {
                free_raw(raw)
                func <- Result[int, string].fail("write failure during copy")
            }
            total_copied += written
            n = c_fread(raw, 1, chunk_size, src.handle)
        }
        free_raw(raw)
        Result[int, string].ok(total_copied)
    }
}
