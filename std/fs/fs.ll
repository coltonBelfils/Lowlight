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
bind File.drop = fn(&self):
	if(self._is_open and unsafe (self._handle != null_ptr[u8]())): {
		let _ = unsafe sys_fclose(self._handle)
		self._is_open = false
		self._handle = unsafe null_ptr[u8]()
	}

// Opens a [`File`] handle at `path` using C stdio mode string `mode` (such as `"rb"` or `"wb"`).
//
// # Errors
// Returns [`Result.err`] if the file cannot be opened with the requested mode.
bind File.open = fn(path: string, mode: string) Result[File]:
	unsafe {
		let fp = sys_fopen(path.c_str(), mode.c_str())
		if(fp == null_ptr[u8]()): err(
			Error.new("failed to open file \"$path\" with mode \"$mode\"")
		)
		else: value(File[_handle: fp, _is_open: true])
	}

// Opens an existing file for reading in binary mode (`"rb"`).
bind File.open_read = fn(path: string) Result[File]: File.open(path, "rb")

// Opens a file for writing in binary mode (`"wb"`), creating it or truncating existing contents.
bind File.open_write = fn(path: string) Result[File]: File.open(path, "wb")

// Opens a file for appending in binary mode (`"ab"`), creating it if missing.
bind File.open_append = fn(path: string) Result[File]: File.open(path, "ab")

// Opens a file for reading and writing in binary mode (`"r+b"`).
bind File.open_read_write = fn(path: string) Result[File]: File.open(path, "r+b")

// Reports whether this file handle is open and non-null.
bind File.is_valid = fn(self) bool: self._is_open and unsafe (self._handle != null_ptr[u8]())

// Explicitly closes the file stream and marks it closed, returning `true` if `fclose` succeeded.
bind File.close = fn(&self) bool: {
	if(not self._is_open or unsafe (self._handle == null_ptr[u8]())): func <- false
	let res = unsafe sys_fclose(self._handle) == 0
	self._is_open = false
	self._handle = unsafe null_ptr[u8]()
	res
}

// Flushes any buffered output bytes to the underlying filesystem, returning `true` on success.
bind File.flush = fn(&self) bool: {
	if(not self.is_valid()): func <- false
	unsafe sys_fflush(self._handle) == 0
}

// Moves the file cursor to `offset` relative to `whence` ([`SEEK_SET`], [`SEEK_CUR`], or [`SEEK_END`]).
//
// Returns the new byte offset from the start of the file.
//
// # Errors
// Fails if the stream is closed or the underlying seek fails.
bind File.seek = fn(&self, offset: int, whence: i32) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	unsafe {
		let rc = sys_fseek(self._handle, offset, whence)
		if(rc != 0): err(Error.new("fseek failed"))
		else: {
			let pos = sys_ftell(self._handle)
			if(pos < 0): err(Error.new("ftell failed after seek")) else: value(pos)
		}
	}
}

// Returns the current byte cursor position of the open file stream.
bind File.tell = fn(&self) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	unsafe {
		let pos = sys_ftell(self._handle)
		if(pos < 0): err(Error.new("ftell failed")) else: value(pos)
	}
}

// Queries the total byte size of the file while preserving the current cursor position.
//
// # Errors
// Fails on non-seekable streams (pipes, FIFOs, terminals) or if the cursor cannot be restored.
bind File.size = fn(&self) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	unsafe {
		let saved = sys_ftell(self._handle)
		if(saved < 0): func <- err(Error.new("failed to query file position"))
		if(sys_fseek(self._handle, 0, SEEK_END) != 0): func <- err(
			Error.new("failed to seek to end")
		)
		let total = sys_ftell(self._handle)
		if(sys_fseek(self._handle, saved, SEEK_SET) != 0): func <- err(
			Error.new("failed to restore file position")
		)
		if(total < 0): err(Error.new("failed to query file size")) else: value(total)
	}
}

// Writes all UTF-8 bytes of `s` to the stream and returns the number of bytes written.
//
// # Errors
// Fails if the stream is closed or fewer than `s.len()` bytes were written.
bind File.write_string = fn(&self, s: string) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	if(s.len() == 0): func <- value(0)
	unsafe {
		let written = sys_fwrite(s.c_str(), 1, s.len(), self._handle)
		if(written < s.len()): err(Error.new("short write: wrote $written of ${s.len()} bytes"))
		else: value(written)
	}
}

// Writes all bytes of `data` to the stream and returns the byte count written.
//
// # Errors
// Fails if the stream is closed or a short write occurs.
bind File.write_bytes = fn(&self, data: [u8]) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	if(data.len() == 0): func <- value(0)
	unsafe {
		let raw = alloc_raw[u8](data.len())
		for(i in 0..data.len()): ptr_write(ptr_add(raw, i), data[i])
		let written = sys_fwrite(raw, 1, data.len(), self._handle)
		free_raw(raw)
		if(written < data.len()): err(
			Error.new("short write: wrote $written of ${data.len()} bytes")
		)
		else: value(written)
	}
}

// Reads up to `max_bytes` from the current stream offset into a byte list (`[u8]`).
//
// Returns fewer than `max_bytes` bytes only when EOF is reached first.
//
// # Errors
// Fails if the stream is closed or the underlying read reports an I/O error (`ferror`).
bind File.read_bytes = fn(&self, max_bytes: int) Result[[u8]]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	if(max_bytes <= 0): func <- value([])
	unsafe {
		let raw = alloc_raw[u8](max_bytes)
		let n = sys_fread(raw, 1, max_bytes, self._handle)
		if(n < max_bytes and sys_ferror(self._handle) != i32(0)): {
			free_raw(raw)
			func <- err(Error.new("read error after $n bytes"))
		}
		let out: [u8] = []
		for(i in 0..n): out.push(raw[i])
		free_raw(raw)
		value(own out)
	}
}

// Reads all remaining bytes from the current stream cursor to EOF into a byte list (`[u8]`).
//
// Reads in fixed-size chunks without querying the file size, so it works on
// non-seekable streams such as pipes, FIFOs, and character devices.
//
// # Errors
// Fails if the stream is closed or the underlying read reports an I/O error (`ferror`).
bind File.read_all_bytes = fn(&self) Result[[u8]]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	unsafe {
		let chunk_size = 16384
		let raw = alloc_raw[u8](chunk_size)
		let out: [u8] = []
		for(true): {
			let n = sys_fread(raw, 1, chunk_size, self._handle)
			for(i in 0..n): out.push(raw[i])
			if(n < chunk_size): for <-
		}
		let had_err = sys_ferror(self._handle) != i32(0)
		free_raw(raw)
		if(had_err): err(Error.new("read error after ${out.len()} bytes")) else: value(own out)
	}
}

// Reads all remaining bytes from the current stream cursor to EOF as a [`string`].
//
// Works on non-seekable streams (pipes, FIFOs, character devices).
//
// # Errors
// Fails if the stream is closed or the underlying read reports an I/O error (`ferror`).
bind File.read_to_string = fn(&self) Result[string]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	unsafe {
		let chunk_size = 8192
		let raw = alloc_raw[u8](chunk_size)
		let out = ""
		for(true): {
			let n = sys_fread(raw, 1, chunk_size, self._handle)
			if(n > 0): out.push_bytes(raw, n)
			if(n < chunk_size): for <-
		}
		let had_err = sys_ferror(self._handle) != i32(0)
		free_raw(raw)
		if(had_err): err(Error.new("read error after ${out.len()} bytes")) else: value(own out)
	}
}

// Closes the stream and converts a failed `fclose` (including a failed final flush) into an error.
bind File._close_checked = fn(&self) Result[bool]:
	if(self.close()): value(true) else: err(Error.new("failed to flush and close file"))

// -----------------------------------------------------------------------------
// Top-Level Convenience Functions
// -----------------------------------------------------------------------------

// Reads the entire contents of `path` into an owned [`string`], wrapping any I/O error with context.
bind read_file = fn(path: string) Result[string]: {
	let f_res! = File.open_read(path)
	if(f_res!.is_err()): func <- err(f_res!.unwrap_err().wrap("failed to open file \"$path\""))
	let f = f_res!.unwrap()
	f.read_to_string().wrap_err("failed to read file \"$path\"")
}

// Alias for [`read_file`].
bind read_to_string = fn(path: string) Result[string]: read_file(path)

// Legacy alias for [`read_file`].
bind read_text = fn(path: string) Result[string]: read_file(path)

// Writes `content` to `path`, creating the file or truncating existing contents.
bind write_file = fn(path: string, content: string) Result[bool]: {
	let f_res! = File.open_write(path)
	if(f_res!.is_err()): func <- err(
		f_res!.unwrap_err().wrap("failed to open file \"$path\" for writing")
	)
	let f = f_res!.unwrap()
	let w_res! = f.write_string(content)
	if(w_res!.is_err()): func <- err(w_res!.unwrap_err().wrap("failed to write to \"$path\""))
	f._close_checked().wrap_err("failed to write to \"$path\"")
}

// Alias for [`write_file`].
bind write_to_file = fn(path: string, content: string) Result[bool]: write_file(path, content)

// Legacy alias for [`write_file`].
bind write_text = fn(path: string, content: string) Result[bool]: write_file(path, content)

// Appends `content` to the end of `path`, creating the file if it does not yet exist.
bind append_file = fn(path: string, content: string) Result[bool]: {
	let f_res! = File.open_append(path)
	if(f_res!.is_err()): func <- err(
		f_res!.unwrap_err().wrap("failed to open file \"$path\" for appending")
	)
	let f = f_res!.unwrap()
	let w_res! = f.write_string(content)
	if(w_res!.is_err()): func <- err(w_res!.unwrap_err().wrap("failed to append to \"$path\""))
	f._close_checked().wrap_err("failed to append to \"$path\"")
}

// Reads the entire file at `path` into a raw byte list (`[u8]`).
//
// Reads until EOF rather than trusting the reported size, so non-seekable
// sources (pipes, FIFOs, `/dev/stdin`, procfs files) are read completely.
bind read_bytes = fn(path: string) Result[[u8]]: {
	let f_res! = File.open_read(path)
	if(f_res!.is_err()): func <- err(f_res!.unwrap_err().wrap("failed to open file \"$path\""))
	let f = f_res!.unwrap()
	f.read_all_bytes().wrap_err("failed to read bytes from \"$path\"")
}

// Writes byte sequence `data` to `path`, creating or overwriting the target file.
bind write_bytes = fn(path: string, data: [u8]) Result[bool]: {
	let f_res! = File.open_write(path)
	if(f_res!.is_err()): func <- err(
		f_res!.unwrap_err().wrap("failed to open file \"$path\" for writing")
	)
	let f = f_res!.unwrap()
	let w_res! = f.write_bytes(data)
	if(w_res!.is_err()): func <- err(w_res!.unwrap_err().wrap("failed to write bytes to \"$path\""))
	f._close_checked().wrap_err("failed to write bytes to \"$path\"")
}

// Reports whether a file or directory exists at `path`.
bind exists = fn(path: string) bool: {
	if(path.len() == 0): func <- false
	unsafe sys_file_exists(path.c_str())
}

// Queries the total byte size of the file at `path`.
bind file_size = fn(path: string) Result[int]: {
	let f_res! = File.open_read(path)
	if(f_res!.is_err()): func <- err(f_res!.unwrap_err().wrap("failed to open file \"$path\""))
	let f = f_res!.unwrap()
	f.size().wrap_err("failed to query size of \"$path\"")
}

// Deletes the file at `path`, returning `true` on success.
bind remove_file = fn(path: string) bool:
	unsafe {
		sys_remove(path.c_str()) == 0
	}

// Renames or moves `old_path` to `new_path`, returning `true` on success.
bind rename_file = fn(old_path: string, new_path: string) bool:
	unsafe {
		sys_rename(old_path.c_str(), new_path.c_str()) == 0
	}

// Copies bytes from `src_path` to `dst_path` in 16 KiB chunks and returns total bytes copied.
bind copy_file = fn(src_path: string, dst_path: string) Result[int]: {
	let src_res! = File.open_read(src_path)
	if(src_res!.is_err()): func <- err(
		src_res!.unwrap_err().wrap("failed to open source \"$src_path\"")
	)
	let dst_res! = File.open_write(dst_path)
	if(dst_res!.is_err()): func <- err(
		dst_res!.unwrap_err().wrap("failed to open destination \"$dst_path\"")
	)
	let src = src_res!.unwrap()
	let dst = dst_res!.unwrap()

	let total_copied = 0
	unsafe {
		let chunk_size = 16384
		let raw = alloc_raw[u8](chunk_size)
		let n = sys_fread(raw, 1, chunk_size, src._handle)
		while(n > 0): {
			let written = sys_fwrite(raw, 1, n, dst._handle)
			if(written < n): {
				free_raw(raw)
				func <- err(Error.new("write failure during copy"))
			}
			total_copied += written
			n = sys_fread(raw, 1, chunk_size, src._handle)
		}
		free_raw(raw)
		if(sys_ferror(src._handle) != i32(0)): func <- err(
			Error.new("read failure during copy from \"$src_path\"")
		)
	}
	let close_res! = dst._close_checked()
	if(close_res!.is_err()): func <- err(
		close_res!.unwrap_err().wrap("failed to finish \"$dst_path\"")
	)
	value(total_copied)
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test_only
extern getenv as _test_getenv: fn(name: ptr[u8]) ptr[u8]

@test_only
bind _test_tmp_path = fn(name: string) string: {
	let base = str_from_cstr(unsafe _test_getenv("TMPDIR".c_str()))
	if(base.len() == 0): base = "/tmp"
	if(base.len() > 1 and base.byte_at(base.len() - 1) == u8(47)): base = base[0..base.len() - 1]
	let mono = unsafe sys_monotonic_nanos()
	let epoch = unsafe sys_epoch_nanos_now()
	"$base/lowlight_test_${name}_${mono}_$epoch"
}

@test("filesystem high-level operations: write, read, append, copy, rename, remove")
bind test_fs_high_level = fn(): {
	let prefix = _test_tmp_path("fs_hl")
	let p1 = "$prefix.txt"
	let p2 = "${prefix}_copy.txt"
	let p3 = "${prefix}_renamed.txt"

	// Clean up any stale files from previous runs
	let _ = remove_file(p1)
	let _ = remove_file(p2)
	let _ = remove_file(p3)

	assert(write_file(p1, "hello lowlight\n").is_ok(), "write_file succeeds")
	assert(exists(p1), "file exists after write")

	let sz_res! = file_size(p1)
	assert(sz_res!.is_ok(), "file_size succeeds")
	assert(sz_res!.unwrap() == 15, "file_size is 15")

	let content_res! = read_file(p1)
	assert(content_res!.is_ok(), "read_file succeeds")
	assert(content_res!.unwrap() == "hello lowlight\n", "content matches")

	assert(append_file(p1, "appended line\n").is_ok(), "append_file succeeds")
	let app_res! = read_file(p1)
	assert(app_res!.is_ok(), "read_file after append succeeds")
	assert(app_res!.unwrap() == "hello lowlight\nappended line\n", "appended content matches")

	let cp_res! = copy_file(p1, p2)
	assert(cp_res!.is_ok(), "copy_file succeeds")
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
	assert(not exists(""), "empty path does not exist")

	let tmp_parent = str_from_cstr(unsafe _test_getenv("TMPDIR".c_str()))
	if(tmp_parent.len() == 0): tmp_parent = "/tmp"
	assert(exists(tmp_parent), "exists returns true for directory")
}

@test("filesystem binary byte I/O: write_bytes, read_bytes")
bind test_fs_bytes = fn(): {
	let path = "${_test_tmp_path("fs_bytes")}.bin"
	let _ = remove_file(path)

	let test_data: [u8] = [u8(0), u8(127), u8(128), u8(255), u8(42)]
	let w_res! = write_bytes(path, test_data)
	assert(w_res!.is_ok(), "write_bytes succeeds")

	let r_res! = read_bytes(path)
	assert(r_res!.is_ok(), "read_bytes succeeds")
	let read_data = r_res!.unwrap()
	assert(read_data.len() == 5, "read_data length matches")
	assert(read_data[0] == u8(0), "byte 0 matches")
	assert(read_data[1] == u8(127), "byte 1 matches")
	assert(read_data[2] == u8(128), "byte 2 matches")
	assert(read_data[3] == u8(255), "byte 3 matches")
	assert(read_data[4] == u8(42), "byte 4 matches")

	assert(remove_file(path), "remove binary test file")
}

@test("filesystem File stream operations: seek, tell, read, write, flush, close")
bind test_fs_file_stream = fn(): {
	let path = "${_test_tmp_path("fs_stream")}.txt"
	let _ = remove_file(path)

	let f_write_res! = File.open_write(path)
	assert(f_write_res!.is_ok(), "File.open_write succeeds")
	let f_w = f_write_res!.unwrap()
	assert(f_w.is_valid(), "file stream is valid")
	let w1_res! = f_w.write_string("ABCDEF012345")
	assert(w1_res!.is_ok(), "write_string succeeds")
	assert(f_w.flush(), "flush succeeds")
	assert(f_w.close(), "close succeeds")

	let f_rw_res! = File.open_read_write(path)
	assert(f_rw_res!.is_ok(), "File.open_read_write succeeds")
	let f_rw = f_rw_res!.unwrap()

	let seek_res! = f_rw.seek(6, SEEK_SET)
	assert(seek_res!.is_ok(), "seek to offset 6")
	let tell_res! = f_rw.tell()
	assert(tell_res!.is_ok(), "tell succeeds")
	assert(tell_res!.unwrap() == 6, "position is 6")

	let rem_res! = f_rw.read_to_string()
	assert(rem_res!.is_ok(), "read_to_string succeeds")
	assert(rem_res!.unwrap() == "012345", "read from offset matches")

	let _ = f_rw.close()
	assert(remove_file(path), "remove stream test file")
}

@test_only
extern pipe as _test_pipe: fn(fds: ptr[i32]) i32

@test_only
extern close as _test_close: fn(fd: i32) i32

@test("filesystem non-seekable streams: read_bytes and read_to_string read a pipe to EOF")
bind test_fs_non_seekable = fn(): {
	// Larger than the old 8 KiB fallback but within the minimum pipe buffer (16 KiB on macOS).
	let payload_len = 12000
	let run = cl(as_string: bool): {
		unsafe {
			let fds = alloc_raw[i32](2)
			assert(_test_pipe(fds) == i32(0), "pipe() succeeds")
			let payload: [u8] = []
			for(i in 0..payload_len): payload.push(u8(65 + i % 26))
			let w = File.open_write("/dev/fd/${fds[1]}").unwrap()
			assert(w.write_bytes(payload).unwrap() == payload_len, "pipe write succeeds")
			assert(w.close(), "pipe writer stream closes")

			// The stream may hold a dup of the fd; close the original so the reader sees EOF.
			let _ = _test_close(fds[1])
			let path = "/dev/fd/${fds[0]}"
			if(as_string): {
				let f = File.open_read(path).unwrap()
				assert(f.size().is_err(), "size fails on a pipe")
				let s! = f.read_to_string()
				assert(s!.is_ok(), "read_to_string on pipe succeeds")
				assert(s!.unwrap().len() == payload_len, "read_to_string reads the whole pipe")
			} else: {
				let r! = read_bytes(path)
				assert(r!.is_ok(), "read_bytes on pipe succeeds")
				let data = r!.unwrap()
				assert(data.len() == payload_len, "read_bytes reads the whole pipe")
				assert(
					data[payload_len - 1] == u8(65 + (payload_len - 1) % 26),
					"last byte matches",
				)
			}
			let _ = _test_close(fds[0])
			free_raw(fds)
		}
	}
	run(false)
	run(true)
}

@test("filesystem read errors are reported instead of treated as EOF")
bind test_fs_read_errors = fn(): {
	let path = "${_test_tmp_path("fs_ferror")}.txt"
	assert(write_file(path, "data").is_ok(), "write_file succeeds")

	// Reading from a write-only stream sets the stream error flag.
	let f = File.open_append(path).unwrap()
	assert(f.read_to_string().is_err(), "read_to_string reports ferror")
	assert(f.read_bytes(4).is_err(), "read_bytes reports ferror")
	assert(f.read_all_bytes().is_err(), "read_all_bytes reports ferror")
	let _ = f.close()

	let closed = File.open_read(path).unwrap()
	assert(closed.close(), "close succeeds")
	assert(not closed.close(), "second close reports failure")
	assert(not closed.flush(), "flush on closed file reports failure")
	assert(closed.read_all_bytes().is_err(), "read on closed file fails")

	assert(remove_file(path), "remove ferror test file")
}
