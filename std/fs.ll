// std/fs — RAII file stream I/O ([`File`]) and whole-file filesystem helpers.
//
// Provides [`File`] with automatic [`Drop`] descriptor cleanup along with
// convenience wrappers ([`read_file`], [`write_file`], [`read_bytes`], [`copy_file`]).

// -----------------------------------------------------------------------------
// Seek Constants
// -----------------------------------------------------------------------------

// Seek origin from the start of the file (`0`), used with [`File.seek`].
bind SEEK_SET: i32 = 0

// Seek origin relative to the current cursor offset (`1`), used with [`File.seek`].
bind SEEK_CUR: i32 = 1

// Seek origin relative to the end of the file (`2`), used with [`File.seek`].
bind SEEK_END: i32 = 2

// -----------------------------------------------------------------------------
// File Stream Type with RAII Drop
// -----------------------------------------------------------------------------

// Buffered OS file stream supporting binary/text reads, writes, and seeking.
//
// Implements [`Drop`] to automatically close the file descriptor at scope exit.
//
// # Example
// ```lowlight
// let f = File.open_write("/tmp/hello.txt").unwrap()
// let _ = f.write_string("hello").unwrap()
// ```
define File struct [
	_handle: ptr[u8],
	_is_open: bool,
]

// Closes the underlying file stream automatically when leaving scope if still open.
bind File.drop = (&self):
	if(self._is_open and unsafe (self._handle != null_ptr[u8]())): {
		let _ = unsafe sys_fclose(self._handle)
		self._is_open = false
		self._handle = unsafe null_ptr[u8]()
	}

// Opens a [`File`] handle at `path` using C stdio mode string `mode` (such as `"rb"` or `"wb"`).
//
// # Errors
// Returns [`Result.Err`] if the file cannot be opened with the requested mode.
bind File.open = (path: string, mode: string) Result[File]:
	unsafe {
		let fp = sys_fopen(path.c_str(), mode.c_str())
		if(fp == null_ptr[u8]()):
			Result[File].fail(Error.new("failed to open file \"$path\" with mode \"$mode\""))
		else: Result[File].ok(File[_handle: fp, _is_open: true])
	}

// Opens an existing file for reading in binary mode (`"rb"`).
bind File.open_read = (path: string) Result[File]: File.open(path, "rb")

// Opens a file for writing in binary mode (`"wb"`), creating it or truncating existing contents.
bind File.open_write = (path: string) Result[File]: File.open(path, "wb")

// Opens a file for appending in binary mode (`"ab"`), creating it if missing.
bind File.open_append = (path: string) Result[File]: File.open(path, "ab")

// Opens a file for reading and writing in binary mode (`"r+b"`).
bind File.open_read_write = (path: string) Result[File]: File.open(path, "r+b")

// Reports whether this file handle is open and non-null.
bind File.is_valid = (self) bool: self._is_open and unsafe (self._handle != null_ptr[u8]())

// Explicitly closes the file stream and marks it closed, returning `true` if `fclose` succeeded.
bind File.close = (&self) bool: {
	if(not self._is_open or unsafe (self._handle == null_ptr[u8]())): func <- false
	let res = unsafe sys_fclose(self._handle) == 0
	self._is_open = false
	self._handle = unsafe null_ptr[u8]()
	res
}

// Flushes any buffered output bytes to the underlying filesystem, returning `true` on success.
bind File.flush = (&self) bool: {
	if(not self.is_valid()): func <- false
	unsafe sys_fflush(self._handle) == 0
}

// Moves the file cursor to `offset` relative to `whence` ([`SEEK_SET`], [`SEEK_CUR`], or [`SEEK_END`]).
//
// Returns the new byte offset from the start of the file.
//
// # Errors
// Fails if the stream is closed or the underlying seek fails.
bind File.seek = (&self, offset: int, whence: i32) Result[int]: {
	if(not self.is_valid()): func <- Result[int].fail(Error.new("file is not open"))
	unsafe {
		let rc = sys_fseek(self._handle, offset, whence)
		if(rc != 0): Result[int].fail(Error.new("fseek failed"))
		else: {
			let pos = sys_ftell(self._handle)
			Result[int].ok(pos)
		}
	}
}

// Returns the current byte cursor position of the open file stream.
bind File.tell = (&self) Result[int]: {
	if(not self.is_valid()): func <- Result[int].fail(Error.new("file is not open"))
	unsafe {
		let pos = sys_ftell(self._handle)
		if(pos < 0): Result[int].fail(Error.new("ftell failed")) else: Result[int].ok(pos)
	}
}

// Queries the total byte size of the file while preserving the current cursor position.
bind File.size = (&self) Result[int]: {
	if(not self.is_valid()): func <- Result[int].fail(Error.new("file is not open"))
	unsafe {
		let saved = sys_ftell(self._handle)
		if(saved < 0): func <- Result[int].fail(Error.new("failed to query file position"))
		if(sys_fseek(self._handle, 0, SEEK_END) != 0):
			func <- Result[int].fail(Error.new("failed to seek to end"))
		let total = sys_ftell(self._handle)
		let _ = sys_fseek(self._handle, saved, SEEK_SET)
		Result[int].ok(total)
	}
}

// Writes all UTF-8 bytes of `s` to the stream and returns the number of bytes written.
//
// # Errors
// Fails if the stream is closed or fewer than `s.len()` bytes were written.
bind File.write_string = (&self, s: string) Result[int]: {
	if(not self.is_valid()): func <- Result[int].fail(Error.new("file is not open"))
	if(s.len() == 0): func <- Result[int].ok(0)
	unsafe {
		let written = sys_fwrite(s.c_str(), 1, s.len(), self._handle)
		if(written < s.len()):
			Result[int].fail(Error.new("short write: wrote $written of ${s.len()} bytes"))
		else: Result[int].ok(written)
	}
}

// Writes all bytes of `data` to the stream and returns the byte count written.
//
// # Errors
// Fails if the stream is closed or a short write occurs.
bind File.write_bytes = (&self, data: [u8]) Result[int]: {
	if(not self.is_valid()): func <- Result[int].fail(Error.new("file is not open"))
	if(data.len() == 0): func <- Result[int].ok(0)
	unsafe {
		let raw = alloc_raw[u8](data.len())
		for(i in 0..data.len()): ptr_write(ptr_add(raw, i), data[i])
		let written = sys_fwrite(raw, 1, data.len(), self._handle)
		free_raw(raw)
		if(written < data.len()):
			Result[int].fail(Error.new("short write: wrote $written of ${data.len()} bytes"))
		else: Result[int].ok(written)
	}
}

// Reads up to `max_bytes` from the current stream offset into a byte list (`[u8]`).
bind File.read_bytes = (&self, max_bytes: int) Result[[u8]]: {
	if(not self.is_valid()): func <- Result[[u8]].fail(Error.new("file is not open"))
	if(max_bytes <= 0): func <- Result[[u8]].ok([])
	unsafe {
		let raw = alloc_raw[u8](max_bytes)
		let n = sys_fread(raw, 1, max_bytes, self._handle)
		let out: [u8] = []
		for(i in 0..n): out.push(raw[i])
		free_raw(raw)
		Result[[u8]].ok(own out)
	}
}

// Reads all remaining bytes from the current stream cursor to EOF as a [`string`].
bind File.read_to_string = (&self) Result[string]: {
	if(not self.is_valid()): func <- Result[string].fail(Error.new("file is not open"))
	unsafe {
		let chunk_size = 8192
		let raw = alloc_raw[u8](chunk_size)
		let out = ""
		let n = sys_fread(raw, 1, chunk_size, self._handle)
		while(n > 0): {
			let chunk = str_from_raw(raw, n)
			out.push(chunk)
			n = sys_fread(raw, 1, chunk_size, self._handle)
		}
		free_raw(raw)
		Result[string].ok(own out)
	}
}

// -----------------------------------------------------------------------------
// Top-Level Convenience Functions
// -----------------------------------------------------------------------------

// Reads the entire contents of `path` into an owned [`string`], wrapping any I/O error with context.
bind read_file = (path: string) Result[string]: {
	let f_res = File.open_read(path)
	if(f_res.is_err()):
		func <- Result[string].fail(f_res.unwrap_err().wrap("failed to open file \"$path\""))
	let f = f_res.unwrap()
	f.read_to_string().wrap_err("failed to read file \"$path\"")
}

// Alias for [`read_file`].
bind read_to_string = (path: string) Result[string]: read_file(path)

// Legacy alias for [`read_file`].
bind read_text = (path: string) Result[string]: read_file(path)

// Writes `content` to `path`, creating the file or truncating existing contents.
bind write_file = (path: string, content: string) Result[bool]: {
	let f_res = File.open_write(path)
	if(f_res.is_err()):
		func <- Result[bool].fail(
			f_res.unwrap_err().wrap("failed to open file \"$path\" for writing"),
		)
	let f = f_res.unwrap()
	let w_res = f.write_string(content)
	if(w_res.is_err()): Result[bool].fail(w_res.unwrap_err().wrap("failed to write to \"$path\""))
	else: Result[bool].ok(true)
}

// Alias for [`write_file`].
bind write_to_file = (path: string, content: string) Result[bool]: write_file(path, content)

// Legacy alias for [`write_file`].
bind write_text = (path: string, content: string) Result[bool]: write_file(path, content)

// Appends `content` to the end of `path`, creating the file if it does not yet exist.
bind append_file = (path: string, content: string) Result[bool]: {
	let f_res = File.open_append(path)
	if(f_res.is_err()):
		func <- Result[bool].fail(
			f_res.unwrap_err().wrap("failed to open file \"$path\" for appending"),
		)
	let f = f_res.unwrap()
	let w_res = f.write_string(content)
	if(w_res.is_err()): Result[bool].fail(w_res.unwrap_err().wrap("failed to append to \"$path\""))
	else: Result[bool].ok(true)
}

// Reads the entire file at `path` into a raw byte list (`[u8]`).
bind read_bytes = (path: string) Result[[u8]]: {
	let f_res = File.open_read(path)
	if(f_res.is_err()):
		func <- Result[[u8]].fail(f_res.unwrap_err().wrap("failed to open file \"$path\""))
	let f = f_res.unwrap()
	let sz_res = f.size()
	let sz = if(sz_res.is_ok()): sz_res.unwrap() else: 8192
	f.read_bytes(sz).wrap_err("failed to read bytes from \"$path\"")
}

// Writes byte sequence `data` to `path`, creating or overwriting the target file.
bind write_bytes = (path: string, data: [u8]) Result[bool]: {
	let f_res = File.open_write(path)
	if(f_res.is_err()):
		func <- Result[bool].fail(
			f_res.unwrap_err().wrap("failed to open file \"$path\" for writing"),
		)
	let f = f_res.unwrap()
	let w_res = f.write_bytes(data)
	if(w_res.is_err()):
		Result[bool].fail(w_res.unwrap_err().wrap("failed to write bytes to \"$path\""))
	else: Result[bool].ok(true)
}

// Reports whether `path` exists and can be opened for reading.
bind exists = (path: string) bool: {
	let f_res = File.open_read(path)
	f_res.is_ok()
}

// Queries the total byte size of the file at `path`.
bind file_size = (path: string) Result[int]: {
	let f_res = File.open_read(path)
	if(f_res.is_err()):
		func <- Result[int].fail(f_res.unwrap_err().wrap("failed to open file \"$path\""))
	let f = f_res.unwrap()
	f.size().wrap_err("failed to query size of \"$path\"")
}

// Deletes the file at `path`, returning `true` on success.
bind remove_file = (path: string) bool:
	unsafe {
		sys_remove(path.c_str()) == 0
	}

// Renames or moves `old_path` to `new_path`, returning `true` on success.
bind rename_file = (old_path: string, new_path: string) bool:
	unsafe {
		sys_rename(old_path.c_str(), new_path.c_str()) == 0
	}

// Copies bytes from `src_path` to `dst_path` in 16 KiB chunks and returns total bytes copied.
bind copy_file = (src_path: string, dst_path: string) Result[int]: {
	let src_res = File.open_read(src_path)
	if(src_res.is_err()):
		func <- Result[int].fail(src_res.unwrap_err().wrap("failed to open source \"$src_path\""))
	let dst_res = File.open_write(dst_path)
	if(dst_res.is_err()):
		func <- Result[int].fail(
			dst_res.unwrap_err().wrap("failed to open destination \"$dst_path\""),
		)
	let src = src_res.unwrap()
	let dst = dst_res.unwrap()

	unsafe {
		let chunk_size = 16384
		let raw = alloc_raw[u8](chunk_size)
		let total_copied = 0
		let n = sys_fread(raw, 1, chunk_size, src._handle)
		while(n > 0): {
			let written = sys_fwrite(raw, 1, n, dst._handle)
			if(written < n): {
				free_raw(raw)
				func <- Result[int].fail(Error.new("write failure during copy"))
			}
			total_copied += written
			n = sys_fread(raw, 1, chunk_size, src._handle)
		}
		free_raw(raw)
		Result[int].ok(total_copied)
	}
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("filesystem high-level operations: write, read, append, copy, rename, remove")
bind test_fs_high_level = (): {
	let p1 = "/tmp/lowlight_test_fs_hl.txt"
	let p2 = "/tmp/lowlight_test_fs_hl_copy.txt"
	let p3 = "/tmp/lowlight_test_fs_hl_renamed.txt"

	// Clean up any stale files from previous runs
	let _ = remove_file(p1)
	let _ = remove_file(p2)
	let _ = remove_file(p3)

	assert(write_file(p1, "hello lowlight\n").is_ok(), "write_file succeeds")
	assert(exists(p1), "file exists after write")

	let sz_res = file_size(p1)
	assert(sz_res.is_ok(), "file_size succeeds")
	assert(sz_res.unwrap() == 15, "file_size is 15")

	let content_res = read_file(p1)
	assert(content_res.is_ok(), "read_file succeeds")
	assert(content_res.unwrap() == "hello lowlight\n", "content matches")

	assert(append_file(p1, "appended line\n").is_ok(), "append_file succeeds")
	let app_res = read_file(p1)
	assert(app_res.is_ok(), "read_file after append succeeds")
	assert(app_res.unwrap() == "hello lowlight\nappended line\n", "appended content matches")

	let cp_res = copy_file(p1, p2)
	assert(cp_res.is_ok(), "copy_file succeeds")
	assert(exists(p2), "copied file exists")
	let cp_content = read_file(p2).unwrap()
	assert(cp_content == "hello lowlight\nappended line\n", "copied content matches")

	assert(rename_file(p2, p3), "rename_file succeeds")
	assert(not exists(p2), "old path does not exist after rename")
	assert(exists(p3), "new path exists after rename")

	assert(remove_file(p1), "remove_file p1 succeeds")
	assert(remove_file(p3), "remove_file p3 succeeds")
	assert(not exists(p1), "p1 does not exist after remove")
	assert(not exists(p3), "p3 does not exist after remove")
}

@test("filesystem binary byte I/O: write_bytes, read_bytes")
bind test_fs_bytes = (): {
	let path = "/tmp/lowlight_test_fs_bytes.bin"
	let _ = remove_file(path)

	let test_data: [u8] = [u8(0), u8(127), u8(128), u8(255), u8(42)]
	let w_res = write_bytes(path, test_data)
	assert(w_res.is_ok(), "write_bytes succeeds")

	let r_res = read_bytes(path)
	assert(r_res.is_ok(), "read_bytes succeeds")
	let read_data = r_res.unwrap()
	assert(read_data.len() == 5, "read_data length matches")
	assert(read_data[0] == u8(0), "byte 0 matches")
	assert(read_data[1] == u8(127), "byte 1 matches")
	assert(read_data[2] == u8(128), "byte 2 matches")
	assert(read_data[3] == u8(255), "byte 3 matches")
	assert(read_data[4] == u8(42), "byte 4 matches")

	assert(remove_file(path), "remove binary test file")
}

@test("filesystem File stream operations: seek, tell, read, write, flush, close")
bind test_fs_file_stream = (): {
	let path = "/tmp/lowlight_test_fs_stream.txt"
	let _ = remove_file(path)

	let f_write_res = File.open_write(path)
	assert(f_write_res.is_ok(), "File.open_write succeeds")
	let f_w = f_write_res.unwrap()
	assert(f_w.is_valid(), "file stream is valid")
	let w1_res = f_w.write_string("ABCDEF012345")
	assert(w1_res.is_ok(), "write_string succeeds")
	assert(f_w.flush(), "flush succeeds")
	assert(f_w.close(), "close succeeds")

	let f_rw_res = File.open_read_write(path)
	assert(f_rw_res.is_ok(), "File.open_read_write succeeds")
	let f_rw = f_rw_res.unwrap()

	let seek_res = f_rw.seek(6, SEEK_SET)
	assert(seek_res.is_ok(), "seek to offset 6")
	let tell_res = f_rw.tell()
	assert(tell_res.is_ok(), "tell succeeds")
	assert(tell_res.unwrap() == 6, "position is 6")

	let rem_res = f_rw.read_to_string()
	assert(rem_res.is_ok(), "read_to_string succeeds")
	assert(rem_res.unwrap() == "012345", "read from offset matches")

	let _ = f_rw.close()
	assert(remove_file(path), "remove stream test file")
}
