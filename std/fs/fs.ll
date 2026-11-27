// std/fs — RAII file stream I/O ([`File`]) and whole-file filesystem helpers.
//
// Provides [`File`] with automatic [`Drop`] descriptor cleanup along with
// convenience wrappers ([`read_file`], [`write_file`], [`read_bytes`], [`copy_file`]).
//
// Every failure carries the OS's description of what went wrong, and its errno name as the
// error's code (`e.has_code("EACCES")`). Failures callers commonly handle on their own, such as a
// missing file, are separate members of the result.

import "std/os"

comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	extern fopen as _fs_fopen: fn(path: ptr[u8], mode: ptr[u8]) ptr[u8]
	extern fclose as _fs_fclose: fn(fp: ptr[u8]) i32
	extern fread as _fs_fread: fn(buf: ptr[u8], size: int, count: int, fp: ptr[u8]) int
	extern fwrite as _fs_fwrite: fn(buf: ptr[u8], size: int, count: int, fp: ptr[u8]) int
	extern fseek as _fs_fseek: fn(fp: ptr[u8], offset: int, whence: i32) i32
	extern ftell as _fs_ftell: fn(fp: ptr[u8]) int
	extern fflush as _fs_fflush: fn(fp: ptr[u8]) i32
	extern ferror as _fs_ferror: fn(fp: ptr[u8]) i32
	extern clearerr as _fs_clearerr: fn(fp: ptr[u8])
	extern fileno as _fs_fileno: fn(fp: ptr[u8]) i32
	extern ll_rt_fcntl as _fs_fcntl: fn(fd: i32, cmd: i32, arg: i32) i32
	extern remove as _fs_remove: fn(path: ptr[u8]) i32
	extern rename as _fs_rename: fn(old_path: ptr[u8], new_path: ptr[u8]) i32
	comptime if(comp.TARGET.arch == comp.TargetArch.amd64): {
		extern "stat\$INODE64" as _fs_stat: fn(path: ptr[u8], buf: ptr[u8]) i32
		extern "fstat\$INODE64" as _fs_fstat: fn(fd: i32, buf: ptr[u8]) i32
	} else comptime if(comp.TARGET.arch == comp.TargetArch.arm64): {
		extern stat as _fs_stat: fn(path: ptr[u8], buf: ptr[u8]) i32
		extern fstat as _fs_fstat: fn(fd: i32, buf: ptr[u8]) i32
	} else: {
		const _fs_unsupported_darwin_arch = comp.compile_error("std/fs: unsupported Darwin architecture")
	}
}

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
// Results
// -----------------------------------------------------------------------------

// The result of opening a file by path ([`File.open`]) and of the functions built on it
// ([`read_file`], [`read_bytes`], [`file_size`], [`copy_file`]).
//
// ```lowlight
// let text = fs.read_file("settings.toml"):
// 	when not_found(_): ""
// 	default err(own e): func <- err(e)
// ```
@must_use
define FileResult[T] result [
	// The opened file, or what was read from it.
	value: T,
	// Nothing exists at the path (or a directory on the way to it doesn't).
	not_found: Error,
	// Any other failure, with the OS's description (permission denied, a directory, ...).
	err: Error,
]

// The result of [`File.read_bytes`], which reads a file a piece at a time.
@must_use
define ReadResult[T] result [
	// The bytes read: at least one, and at most the number asked for.
	value: T,
	// The end of the file was already reached; nothing was read.
	eof: Error,
	// A read error, with the OS's description.
	err: Error,
]

// The result of [`remove_file`].
@must_use
define RemoveResult result [
	// The file was removed.
	value,
	// Nothing exists at the path (or a directory on the way to it doesn't).
	not_found: Error,
	// Any other failure, with the OS's description (permission denied, a directory, ...).
	err: Error,
]

// The result of [`rename_file`].
@must_use
define RenameResult result [
	// The file was renamed.
	value,
	// Nothing exists at the old path.
	not_found: Error,
	// Any other failure, with the OS's description (permission denied, a missing destination
	// directory, a move across filesystems, ...).
	err: Error,
]

// -----------------------------------------------------------------------------
// File Stream Type with RAII Drop
// -----------------------------------------------------------------------------

// Buffered OS file stream supporting binary/text reads, writes, and seeking.
//
// Implements [`Drop`] to automatically close the file descriptor at scope exit.
//
// # Example
// ```lowlight
// let f = File.open_write("/tmp/hello.txt"): default panic
// f.write_string("hello"): default panic
// ```
define File struct [
	_handle: ptr[u8],
	_fd: int,
	_is_open: bool,
	_has_err: bool,
	_wbuf: ptr[u8],
	_wlen: int,
	_wcap: int,
]

bind File._write_all_fd = fn(&self, buf: ptr[u8], count: int) int: {
	if(count <= 0): func <- 0
	self._has_err = false
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_write = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 64 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 1 else: comp.compile_error("std/fs: unsupported Linux architecture for write")
		let off = 0
		for(off < count): {
			let n = unsafe syscall(
				sys_write,
				self._fd,
				int(ptr_addr(unsafe ptr_add(buf, off))),
				count - off,
				0,
				0,
				0,
			)
			if(n == -4): next <-
			if(n <= 0): {
				if(n < 0): os.set_last_errno(-n)
				self._has_err = true
				for <-
			}
			off += n
		}
		off
	} else: {
		let _ = buf
		0
	}
}

bind File._flush_write_buf = fn(&self) bool: {
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		if(self._wlen <= 0): func <- true
		let written = unsafe self._write_all_fd(self._wbuf, self._wlen)
		if(written == self._wlen): {
			self._wlen = 0
			func <- true
		}
		if(written > 0): {
			let rem = self._wlen - written
			unsafe copy_bytes(self._wbuf, ptr_add(self._wbuf, written), rem)
			self._wlen = rem
		}
		false
	} else: true
}

bind File._free_write_buf = fn(&self): {
	if(unsafe not is_null(self._wbuf)): {
		unsafe free_raw(self._wbuf)
		self._wbuf = unsafe null_ptr[u8]()
	}
	self._wlen = 0
	self._wcap = 0
}

// Closes the underlying file stream automatically when leaving scope if still open.
bind File.drop = fn(&self): {
	if(self.is_valid()): {
		comptime if(comp.TARGET.os == comp.TargetOs.linux): {
			let _ = self._flush_write_buf()
			self._free_write_buf()
			let sys_close = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 57 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 3 else: comp.compile_error("std/fs: unsupported Linux architecture for close")
			let _ = unsafe syscall(sys_close, self._fd, 0, 0, 0, 0, 0)
		} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
			let _ = unsafe _fs_fclose(self._handle)
		}
		self._is_open = false
		self._fd = -1
		self._handle = unsafe null_ptr[u8]()
	} else: {
		self._free_write_buf()
	}
}

// What opening with `mode` is for, for error messages.
bind _mode_purpose = fn(mode: string) string:
	match(mode):
		when "r", "rb": "for reading"
		when "w", "wb": "for writing"
		when "a", "ab": "for appending"
		when "r+", "r+b", "rb+", "w+", "w+b", "wb+", "a+", "a+b", "ab+": "for reading and writing"
		default: "with mode \"$mode\""

bind _linux_open_flags = fn(mode: string) int:
	// Every mode includes O_CLOEXEC (524288) to prevent descriptor leaks across exec.
	match(mode):
		when "r", "rb": 524288
		when "w", "wb": 524865
		when "a", "ab": 525377
		when "r+", "r+b", "rb+": 524290
		when "w+", "w+b", "wb+": 524866
		when "a+", "a+b", "ab+": 525378
		default: -1

// Opens a [`File`] at `path` using C stdio mode string `mode` (such as `"rb"` or `"wb"`).
//
// # Errors
// [`FileResult.not_found`] if nothing exists at `path` (or, when creating, its directory
// doesn't); [`FileResult.err`] for any other failure, with the OS's description.
bind File.open = fn(path: string, mode: string) FileResult[File]: {
	if(path.len() == 0 or path.contains_byte(u8(0))): {
		os.set_last_errno(22)
		func <- err(os.error_from_errno(22, "failed to open \"$path\" ${_mode_purpose(mode)}"))
	}
	if(mode.len() == 0 or mode.contains_byte(u8(0))): {
		os.set_last_errno(22)
		func <- err(os.error_from_errno(22, "invalid mode \"$mode\" for \"$path\""))
	}
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		let code = 0
		comptime if(comp.TARGET.os == comp.TargetOs.linux): {
			let flags = _linux_open_flags(mode)
			if(flags < 0): {
				os.set_last_errno(22)
				code = 22
			} else: {
				let sys_openat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 56 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 257 else: comp.compile_error("std/fs: unsupported Linux architecture for openat")
				let fd = unsafe syscall(
					sys_openat,
					-100,
					int(ptr_addr(path.c_str())),
					flags,
					420,
					0,
					0,
				)
				while(fd == -4): {
					fd = unsafe syscall(
						sys_openat,
						-100,
						int(ptr_addr(path.c_str())),
						flags,
						420,
						0,
						0,
					)
				}
				if(fd >= 0): func <- value(
					File[
						_handle: unsafe null_ptr[u8](),
						_fd: fd,
						_is_open: true,
						_has_err: false,
						_wbuf: unsafe null_ptr[u8](),
						_wlen: 0,
						_wcap: 0,
					]
				)
				code = -fd
				os.set_last_errno(code)
			}
		} else: {
			let fp = unsafe _fs_fopen(path.c_str(), mode.c_str())
			if(unsafe (fp != null_ptr[u8]())): {
				let cfd = unsafe _fs_fileno(fp)
				if(cfd >= i32(0)): {
					let _ = unsafe _fs_fcntl(cfd, i32(2), i32(1))
				}
				func <- value(
					File[
						_handle: fp,
						_fd: -1,
						_is_open: true,
						_has_err: false,
						_wbuf: unsafe null_ptr[u8](),
						_wlen: 0,
						_wcap: 0,
					]
				)
			}
			code = os.last_errno()
		}
		let e = os.error_from_errno(code, "failed to open \"$path\" ${_mode_purpose(mode)}")
		if(os.errno_name(code) == "ENOENT"): not_found(e) else: err(e)
	} else: {
		let _ = path.len()
		let _ = mode.len()
		err(Error.new("file I/O is unsupported on this target"))
	}
}

// Opens an existing file for reading in binary mode (`"rb"`).
bind File.open_read = fn(path: string) FileResult[File]: File.open(path, "rb")

// Opens a file for writing in binary mode (`"wb"`), creating it or truncating existing contents.
bind File.open_write = fn(path: string) FileResult[File]: File.open(path, "wb")

// Opens a file for appending in binary mode (`"ab"`), creating it if missing.
bind File.open_append = fn(path: string) FileResult[File]: File.open(path, "ab")

// Opens an existing file for reading and writing in binary mode (`"r+b"`).
bind File.open_read_write = fn(path: string) FileResult[File]: File.open(path, "r+b")

// Reports whether this file handle is open and non-null.
bind File.is_valid = fn(self) bool: {
	if(not self._is_open): func <- false
	comptime if(comp.TARGET.os == comp.TargetOs.linux): self._fd >= 0
	else comptime if(comp.TARGET.os == comp.TargetOs.darwin): unsafe (self._handle != null_ptr[u8]())
	else: false
}

// Flushes buffered output and closes the file stream. The handle is closed afterward even if
// this fails.
//
// # Errors
// Fails if the stream is already closed, or if the final flush or `fclose` fails (for example,
// the disk is full), with the OS's description.
bind File.close = fn(&self) Status: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	let rc = 0
	let code = 0
	let flush_ok = true
	let flush_code = 0
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		flush_ok = self._flush_write_buf()
		if(not flush_ok): flush_code = os.last_errno()
		self._free_write_buf()
		let sys_close = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 57 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 3 else: comp.compile_error("std/fs: unsupported Linux architecture for close")
		let r = unsafe syscall(sys_close, self._fd, 0, 0, 0, 0, 0)
		if(r < 0): {
			rc = -1
			code = -r
			os.set_last_errno(code)
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		rc = int(unsafe _fs_fclose(self._handle))
		code = os.last_errno()
	}
	self._is_open = false
	self._fd = -1
	self._handle = unsafe null_ptr[u8]()
	if(not flush_ok): err(os.error_from_errno(flush_code, "failed to flush file"))
	else if(rc != 0): err(os.error_from_errno(code, "failed to close file"))
	else: value
}

// Flushes buffered output bytes to the underlying file.
//
// # Errors
// Fails if the stream is closed or the write fails, with the OS's description.
bind File.flush = fn(&self) Status: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		if(not self._flush_write_buf()): err(os.error_from_errno(os.last_errno(), "failed to flush file"))
		else: value
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		if(unsafe _fs_fflush(self._handle) != i32(0)): err(
			os.error_from_errno(os.last_errno(), "failed to flush file")
		)
		else: value
	} else: err(Error.new("file I/O is unsupported on this target"))
}

// Moves the file cursor to `offset` relative to `whence` ([`SEEK_SET`], [`SEEK_CUR`], or
// [`SEEK_END`]) and returns the new byte offset from the start of the file.
//
// # Errors
// Fails if the stream is closed or the seek fails (for example, on a pipe), with the OS's
// description.
bind File.seek = fn(&self, offset: int, whence: i32) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		if(not self._flush_write_buf()): func <- err(
			os.error_from_errno(os.last_errno(), "failed to flush before seek")
		)
		let sys_lseek = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 62 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 8 else: comp.compile_error("std/fs: unsupported Linux architecture for lseek")
		let pos = unsafe syscall(sys_lseek, self._fd, offset, int(whence), 0, 0, 0)
		if(pos < 0): {
			let c = -pos
			os.set_last_errno(c)
			func <- err(os.error_from_errno(c, "failed to seek"))
		}
		value(pos)
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		if(unsafe _fs_fseek(self._handle, offset, whence) != i32(0)): func <- err(
			os.error_from_errno(os.last_errno(), "failed to seek")
		)
		self.tell()
	} else: {
		let _ = offset
		let _ = whence
		err(Error.new("file I/O is unsupported on this target"))
	}
}

// Returns the current byte cursor position of the open file stream.
//
// # Errors
// Fails if the stream is closed or the position can't be read, with the OS's description.
bind File.tell = fn(&self) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		if(not self._flush_write_buf()): func <- err(
			os.error_from_errno(os.last_errno(), "failed to flush before tell")
		)
		let sys_lseek = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 62 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 8 else: comp.compile_error("std/fs: unsupported Linux architecture for lseek")
		let pos = unsafe syscall(sys_lseek, self._fd, 0, 1, 0, 0, 0)
		if(pos < 0): {
			let c = -pos
			os.set_last_errno(c)
			func <- err(os.error_from_errno(c, "failed to read the file position"))
		}
		value(pos)
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		let pos = unsafe _fs_ftell(self._handle)
		if(pos < 0): err(os.error_from_errno(os.last_errno(), "failed to read the file position"))
		else: value(pos)
	} else: err(Error.new("file I/O is unsupported on this target"))
}

// Queries the total byte size of the file while preserving the current cursor position.
//
// # Errors
// Fails on non-seekable streams (pipes, FIFOs, terminals) or if the cursor cannot be restored.
bind File.size = fn(&self) Result[int]: {
	let saved = self.tell():
		default err(own e): func <- err(e)
	let total = self.seek(0, SEEK_END):
		default err(own e): func <- err(e.wrap("failed to find the file size"))
	self.seek(saved, SEEK_SET):
		default err(own e): func <- err(e.wrap("failed to restore the file position"))
	value(total)
}

bind File._write_raw = fn(&self, buf: ptr[u8], count: int) int: {
	if(count <= 0): func <- 0
	self._has_err = false
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		if(self._wlen == 0 and count >= 8192): func <- self._write_all_fd(buf, count)
		if(self._wlen > 0 and self._wlen + count > 8192): {
			if(not self._flush_write_buf()): func <- 0
			if(count >= 8192): func <- self._write_all_fd(buf, count)
		}
		if(unsafe is_null(self._wbuf)): {
			self._wbuf = unsafe alloc_raw[u8](8192)
			self._wcap = 8192
		}
		unsafe copy_bytes(ptr_add(self._wbuf, self._wlen), buf, count)
		self._wlen += count
		count
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		let n = unsafe _fs_fwrite(buf, 1, count, self._handle)
		if(n < count): self._has_err = true
		n
	} else: {
		let _ = buf
		0
	}
}

bind File._read_raw = fn(&self, buf: ptr[u8], count: int) int: {
	if(count <= 0): func <- 0
	self._has_err = false
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		if(self._wlen > 0 and not self._flush_write_buf()): func <- -1
		let sys_read = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 63 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 0 else: comp.compile_error("std/fs: unsupported Linux architecture for read")
		for(true): {
			let n = unsafe syscall(sys_read, self._fd, int(ptr_addr(buf)), count, 0, 0, 0)
			if(n == -4): next <-
			if(n < 0): {
				os.set_last_errno(-n)
				self._has_err = true
				func <- -1
			}
			func <- n
		}
		0
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		for(true): {
			let n = unsafe _fs_fread(buf, 1, count, self._handle)
			if(n < count and unsafe (_fs_ferror(self._handle) != i32(0))): {
				let e = os.last_errno()
				if(e == 4): {
					unsafe _fs_clearerr(self._handle)
					if(n > 0): func <- n
					next <-
				}
				self._has_err = true
				func <- -1
			}
			func <- n
		}
		0
	} else: {
		let _ = buf
		0
	}
}

// Writes all UTF-8 bytes of `s` to the stream and returns the number of bytes written.
//
// # Errors
// Fails if the stream is closed or not every byte was written, with the OS's description.
bind File.write_string = fn(&self, s: string) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	if(s.len() == 0): func <- value(0)
	let written = unsafe self._write_raw(s.c_str(), s.len())
	if(written < s.len()): err(
		os.error_from_errno(os.last_errno(), "short write: wrote $written of ${s.len()} bytes")
	)
	else: value(written)
}

// Writes all bytes of `data` to the stream and returns the byte count written.
//
// # Errors
// Fails if the stream is closed or not every byte was written, with the OS's description.
bind File.write_bytes = fn(&self, data: [u8]) Result[int]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	if(data.len() == 0): func <- value(0)
	let written = unsafe self._write_raw(data.data, data.len())
	if(written < data.len()): err(
		os.error_from_errno(
			os.last_errno(),
			"short write: wrote $written of ${data.len()} bytes",
		)
	)
	else: value(written)
}

// Reads the next piece of the file: at least one byte and at most `max_bytes` (which must be
// positive). Call it until it reports [`ReadResult.eof`].
//
// ```lowlight
// for(true): {
// 	let chunk = f.read_bytes(4096):
// 		when eof(_): for <-
// 		default err(own e): func <- err(e)
// 	consume(chunk)
// }
// ```
//
// # Errors
// [`ReadResult.eof`] once the whole file has been read; [`ReadResult.err`] if the stream is
// closed, `max_bytes` isn't positive, or the read fails, with the OS's description.
bind File.read_bytes = fn(&self, max_bytes: int) ReadResult[[u8]]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	if(max_bytes <= 0): func <- err(
		Error.new("read_bytes needs a positive byte count, got $max_bytes")
	)
	unsafe {
		let out = List[u8].with_capacity(max_bytes)
		let n = self._read_raw(out.data, max_bytes)
		if(n <= 0): {
			if(self._has_err): func <- err(os.error_from_errno(os.last_errno(), "read error"))
			func <- eof(Error.new("end of file"))
		}
		out.len = n
		if(max_bytes > 65536 and n * 4 < max_bytes): {
			let shrunk = out.copy()
			func <- value(own shrunk)
		}
		value(own out)
	}
}

// Reads all remaining bytes from the current stream cursor to EOF into a byte list (`[u8]`).
//
// Reads in fixed-size chunks without querying the file size, so it works on
// non-seekable streams such as pipes, FIFOs, and character devices.
//
// # Errors
// Fails if the stream is closed or a read fails, with the OS's description.
bind File.read_all_bytes = fn(&self) Result[[u8]]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	unsafe {
		let out: [u8] = []
		for(true): {
			out.reserve(out.len + 16384)
			let avail = out.cap - out.len
			let n = self._read_raw(ptr_add(out.data, out.len), avail)
			if(n <= 0): for <-
			out.len += n
		}
		if(self._has_err): err(
			os.error_from_errno(os.last_errno(), "read error after ${out.len()} bytes")
		)
		else: value(own out)
	}
}

// Reads all remaining bytes from the current stream cursor to EOF as a [`string`].
//
// Works on non-seekable streams (pipes, FIFOs, character devices).
//
// # Errors
// Fails if the stream is closed or a read fails, with the OS's description.
bind File.read_to_string = fn(&self) Result[string]: {
	if(not self.is_valid()): func <- err(Error.new("file is not open"))
	unsafe {
		let chunk_size = 8192
		let raw = alloc_raw[u8](chunk_size)
		let out = ""
		for(true): {
			let n = self._read_raw(raw, chunk_size)
			if(n <= 0): for <-
			out.push_bytes(raw, n)
		}
		free_raw(raw)
		if(self._has_err): err(
			os.error_from_errno(os.last_errno(), "read error after ${out.len()} bytes")
		)
		else: value(own out)
	}
}

// -----------------------------------------------------------------------------
// Top-Level Convenience Functions
// -----------------------------------------------------------------------------

// Reads the entire contents of `path` into a [`string`].
//
// # Errors
// [`FileResult.not_found`] if nothing exists at `path`; [`FileResult.err`] if it can't be opened
// or read, with the OS's description.
bind read_file = fn(path: string) FileResult[string]: {
	let f = File.open_read(path):
		when not_found(own e): func <- not_found(e)
		default err(own e): func <- err(e)
	let text = f.read_to_string():
		default err(own e): func <- err(e.wrap("failed to read \"$path\""))
	value(own text)
}

// Alias for [`read_file`].
bind read_to_string = fn(path: string) FileResult[string]: read_file(path)

// Legacy alias for [`read_file`].
bind read_text = fn(path: string) FileResult[string]: read_file(path)

// Writes `content` to `path`, creating the file or truncating existing contents.
//
// # Errors
// Fails if the file can't be opened, written, or closed (a failed close can mean the data never
// reached the disk), with the OS's description.
bind write_file = fn(path: string, content: string) Status: {
	let f = File.open_write(path):
		when not_found(own e): func <- err(e)
		default err(own e): func <- err(e)
	f.write_string(content):
		default err(own e): func <- err(e.wrap("failed to write to \"$path\""))
	f.close().wrap_err("failed to write to \"$path\"")
}

// Alias for [`write_file`].
bind write_to_file = fn(path: string, content: string) Status: write_file(path, content)

// Legacy alias for [`write_file`].
bind write_text = fn(path: string, content: string) Status: write_file(path, content)

// Appends `content` to the end of `path`, creating the file if it does not yet exist.
//
// # Errors
// Fails if the file can't be opened, written, or closed, with the OS's description.
bind append_file = fn(path: string, content: string) Status: {
	let f = File.open_append(path):
		when not_found(own e): func <- err(e)
		default err(own e): func <- err(e)
	f.write_string(content):
		default err(own e): func <- err(e.wrap("failed to append to \"$path\""))
	f.close().wrap_err("failed to append to \"$path\"")
}

// Reads the entire file at `path` into a raw byte list (`[u8]`).
//
// Reads until EOF rather than trusting the reported size, so non-seekable
// sources (pipes, FIFOs, `/dev/stdin`, procfs files) are read completely.
//
// # Errors
// [`FileResult.not_found`] if nothing exists at `path`; [`FileResult.err`] if it can't be opened
// or read, with the OS's description.
bind read_bytes = fn(path: string) FileResult[[u8]]: {
	let f = File.open_read(path):
		when not_found(own e): func <- not_found(e)
		default err(own e): func <- err(e)
	let data = f.read_all_bytes():
		default err(own e): func <- err(e.wrap("failed to read \"$path\""))
	value(own data)
}

// Writes byte sequence `data` to `path`, creating or overwriting the target file.
//
// # Errors
// Fails if the file can't be opened, written, or closed, with the OS's description.
bind write_bytes = fn(path: string, data: [u8]) Status: {
	let f = File.open_write(path):
		when not_found(own e): func <- err(e)
		default err(own e): func <- err(e)
	f.write_bytes(data):
		default err(own e): func <- err(e.wrap("failed to write bytes to \"$path\""))
	f.close().wrap_err("failed to write bytes to \"$path\"")
}

// Reports whether a file or directory exists at `path`.
bind exists = fn(path: string) bool: {
	if(path.len() == 0 or path.contains_byte(u8(0))): func <- false
	os.file_exists(path)
}

// Queries the total byte size of the file at `path`.
//
// # Errors
// [`FileResult.not_found`] if nothing exists at `path`; [`FileResult.err`] if it can't be opened
// or isn't seekable, with the OS's description.
bind file_size = fn(path: string) FileResult[int]: {
	let f = File.open_read(path):
		when not_found(own e): func <- not_found(e)
		default err(own e): func <- err(e)
	let n = f.size():
		default err(own e): func <- err(e.wrap("failed to query size of \"$path\""))
	value(n)
}

// Deletes the file (or empty directory) at `path`.
//
// ```lowlight
// fs.remove_file(tmp):
// 	when not_found(_): {}
// 	default panic
// ```
//
// # Errors
// [`RemoveResult.not_found`] if nothing exists at `path`; [`RemoveResult.err`] for any other
// failure, with the OS's description.
bind remove_file = fn(path: string) RemoveResult: {
	if(path.len() == 0 or path.contains_byte(u8(0))): {
		os.set_last_errno(22)
		func <- err(os.error_from_errno(22, "failed to remove \"$path\""))
	}
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		let code = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
			let sys_unlinkat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 35 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 263 else: comp.compile_error("std/fs: unsupported Linux architecture for unlinkat")
			let rc = unsafe syscall(sys_unlinkat, -100, int(ptr_addr(path.c_str())), 0, 0, 0, 0)
			if(rc == 0): func <- value
			if(rc == -21): {
				rc = unsafe syscall(sys_unlinkat, -100, int(ptr_addr(path.c_str())), 512, 0, 0, 0)
				if(rc == 0): func <- value
			}
			let c = -rc
			os.set_last_errno(c)
			c
		} else: {
			if(unsafe _fs_remove(path.c_str()) == i32(0)): func <- value
			os.last_errno()
		}
		let e = os.error_from_errno(code, "failed to remove \"$path\"")
		if(os.errno_name(code) == "ENOENT"): not_found(e) else: err(e)
	} else: {
		let _ = path.len()
		err(Error.new("remove_file is unsupported on this target"))
	}
}

// Renames or moves `old_path` to `new_path`, replacing any file already at `new_path`.
//
// # Errors
// [`RenameResult.not_found`] if nothing exists at `old_path`; [`RenameResult.err`] for any other
// failure (including a missing destination directory on the way to `new_path`), with the OS's description.
bind rename_file = fn(old_path: string, new_path: string) RenameResult: {
	if(old_path.len() == 0 or old_path.contains_byte(u8(0)) or new_path.len() == 0 or new_path.contains_byte(u8(0))): {
		os.set_last_errno(22)
		func <- err(os.error_from_errno(22, "failed to rename \"$old_path\" to \"$new_path\""))
	}
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		let code = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
			let sys_renameat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 38 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 264 else: comp.compile_error("std/fs: unsupported Linux architecture for renameat")
			let rc = unsafe syscall(
				sys_renameat,
				-100,
				int(ptr_addr(old_path.c_str())),
				-100,
				int(ptr_addr(new_path.c_str())),
				0,
				0,
			)
			if(rc == 0): func <- value
			let c = -rc
			os.set_last_errno(c)
			c
		} else: {
			if(unsafe _fs_rename(old_path.c_str(), new_path.c_str()) == i32(0)): func <- value
			os.last_errno()
		}
		let e = os.error_from_errno(code, "failed to rename \"$old_path\" to \"$new_path\"")

		// ENOENT also covers a missing destination directory; only a missing source is `not_found`.
		if(os.errno_name(code) == "ENOENT" and not exists(old_path)): not_found(e) else: err(e)
	} else: {
		let _ = old_path.len()
		let _ = new_path.len()
		err(Error.new("rename_file is unsupported on this target"))
	}
}

bind File._is_same_target = fn(self, dst_path: string) bool: {
	if(not self.is_valid()): func <- false
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_fstat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 80 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 5 else: comp.compile_error("std/fs: unsupported Linux architecture for fstat")
		let sys_newfstatat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 79 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 262 else: comp.compile_error("std/fs: unsupported Linux architecture for newfstatat")
		unsafe {
			let sb = alloc_raw[u8](160)
			let db = alloc_raw[u8](160)
			let same = false
			if(syscall(sys_fstat, self._fd, int(ptr_addr(sb)), 0, 0, 0, 0) == 0 and syscall(sys_newfstatat, -100, int(ptr_addr(dst_path.c_str())), int(ptr_addr(db)), 0, 0, 0) == 0): {
				let sdev = ptr_read(ptr_cast[u64](sb))
				let sino = ptr_read(ptr_cast[u64](ptr_add(sb, 8)))
				let ddev = ptr_read(ptr_cast[u64](db))
				let dino = ptr_read(ptr_cast[u64](ptr_add(db, 8)))
				same = sdev == ddev and sino == dino
			}
			free_raw(db)
			free_raw(sb)
			same
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let sfd = _fs_fileno(self._handle)
			if(sfd < i32(0)): func <- false
			let sb = alloc_raw[u8](144)
			let db = alloc_raw[u8](144)
			let same = false
			if(_fs_fstat(sfd, sb) == i32(0) and _fs_stat(dst_path.c_str(), db) == i32(0)): {
				let sdev = int(ptr_read(ptr_cast[i32](sb)))
				let sino = ptr_read(ptr_cast[u64](ptr_add(sb, 8)))
				let ddev = int(ptr_read(ptr_cast[i32](db)))
				let dino = ptr_read(ptr_cast[u64](ptr_add(db, 8)))
				same = sdev == ddev and sino == dino
			}
			free_raw(db)
			free_raw(sb)
			same
		}
	} else: {
		let _ = dst_path.len()
		false
	}
}

// Copies bytes from `src_path` to `dst_path` in 16 KiB chunks and returns total bytes copied.
//
// # Errors
// [`FileResult.not_found`] if nothing exists at `src_path`; [`FileResult.err`] if the source
// can't be read, source and destination refer to the same file, or the destination can't be
// created or written (including a missing destination directory), with the OS's description.
bind copy_file = fn(src_path: string, dst_path: string) FileResult[int]: {
	let src = File.open_read(src_path):
		when not_found(own e): func <- not_found(e)
		default err(own e): func <- err(e)
	if(dst_path.len() == 0 or dst_path.contains_byte(u8(0))): {
		os.set_last_errno(22)
		func <- err(os.error_from_errno(22, "invalid destination path \"$dst_path\""))
	}
	if(src._is_same_target(dst_path)): {
		os.set_last_errno(22)
		func <- err(
			Error.coded(
				"EINVAL",
				"cannot copy \"$src_path\" to \"$dst_path\": source and destination are the same file",
			)
		)
	}
	let dst = File.open_write(dst_path):
		when not_found(own e): func <- err(e)
		default err(own e): func <- err(e)

	let total_copied = 0
	unsafe {
		let chunk_size = 16384
		let raw = alloc_raw[u8](chunk_size)
		for(true): {
			let n = src._read_raw(raw, chunk_size)
			if(n <= 0): for <-
			let written = dst._write_raw(raw, n)
			if(written < n): {
				let code = os.last_errno()
				free_raw(raw)
				func <- err(os.error_from_errno(code, "failed to write to \"$dst_path\""))
			}
			total_copied += written
		}
		free_raw(raw)
		if(src._has_err): func <- err(
			os.error_from_errno(os.last_errno(), "failed to read \"$src_path\"")
		)
	}
	dst.close():
		default err(own e): func <- err(e.wrap("failed to finish \"$dst_path\""))
	value(total_copied)
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test_only
extern getenv as _test_getenv: fn(name: ptr[u8]) ptr[u8]

comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	@test_only
	extern clock_gettime as _fs_test_clock: fn(clk_id: i32, ts: ptr[u8]) i32
}

@test_only
bind _test_tmp_path = fn(name: string) string: {
	let base = str_from_cstr(unsafe _test_getenv("TMPDIR".c_str()))
	if(base.len() == 0): base = "/tmp"
	if(base.len() > 1 and base.byte_at(base.len() - 1) == u8(47)): base = base[0..base.len() - 1]
	let stamp = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_clock = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 113 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 228 else: comp.compile_error("std/fs: unsupported Linux architecture for clock_gettime")
		unsafe {
			let ts = alloc_raw[int](2)
			ptr_write(ts, 0)
			ptr_write(ptr_add(ts, 1), 0)
			let _ = syscall(sys_clock, 0, int(ptr_addr(ts)), 0, 0, 0, 0)
			let n = ptr_read(ts) * 1000000000 + ptr_read(ptr_add(ts, 1))
			free_raw(ts)
			n
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let ts = alloc_raw[int](2)
			ptr_write(ts, 0)
			ptr_write(ptr_add(ts, 1), 0)
			let _ = _fs_test_clock(i32(0), ptr_cast[u8](ts))
			let n = ptr_read(ts) * 1000000000 + ptr_read(ptr_add(ts, 1))
			free_raw(ts)
			n
		}
	} else: 1
	"$base/lowlight_test_${name}_$stamp"
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

	let sz = file_size(p1): default panic
	assert(sz == 15, "file_size is 15")

	let content = read_file(p1): default panic
	assert(content == "hello lowlight\n", "content matches")

	assert(append_file(p1, "appended line\n").is_ok(), "append_file succeeds")
	let appended = read_file(p1): default panic
	assert(appended == "hello lowlight\nappended line\n", "appended content matches")

	let copied = copy_file(p1, p2): default panic
	assert(copied == 29, "copy_file returns the byte count")
	assert(exists(p2), "copied file exists")
	let cp_content = read_file(p2): default panic
	assert(cp_content == "hello lowlight\nappended line\n", "copied content matches")

	rename_file(p2, p3): default panic
	assert(not exists(p2), "old path does not exist after rename")
	assert(exists(p3), "new path exists after rename")

	remove_file(p1): default panic
	remove_file(p3): default panic
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

	let read_data = read_bytes(path): default panic
	assert(read_data.len() == 5, "read_data length matches")
	assert(read_data[0] == u8(0), "byte 0 matches")
	assert(read_data[1] == u8(127), "byte 1 matches")
	assert(read_data[2] == u8(128), "byte 2 matches")
	assert(read_data[3] == u8(255), "byte 3 matches")
	assert(read_data[4] == u8(42), "byte 4 matches")

	remove_file(path): default panic
}

@test("filesystem File stream operations: seek, tell, read, write, flush, close")
bind test_fs_file_stream = fn(): {
	let path = "${_test_tmp_path("fs_stream")}.txt"
	let _ = remove_file(path)

	let f_w = File.open_write(path): default panic
	assert(f_w.is_valid(), "file stream is valid")
	let w1_res! = f_w.write_string("ABCDEF012345")
	assert(w1_res!.is_ok(), "write_string succeeds")
	assert(f_w.flush().is_ok(), "flush succeeds")
	assert(f_w.close().is_ok(), "close succeeds")
	assert(f_w.close().peek_err()?.message() == "file is not open", "closing twice fails")

	let f_rw = File.open_read_write(path): default panic

	let seek_res! = f_rw.seek(6, SEEK_SET)
	assert(seek_res!.is_ok(), "seek to offset 6")
	let tell_res! = f_rw.tell()
	assert(tell_res!.is_ok(), "tell succeeds")
	assert(tell_res!.unwrap() == 6, "position is 6")

	let rem_res! = f_rw.read_to_string()
	assert(rem_res!.is_ok(), "read_to_string succeeds")
	assert(rem_res!.unwrap() == "012345", "read from offset matches")

	f_rw.close(): default panic
	remove_file(path): default panic
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
			let w = File.open_write("/dev/fd/${fds[1]}"): default panic
			assert(w.write_bytes(payload).unwrap() == payload_len, "pipe write succeeds")
			assert(w.close().is_ok(), "pipe writer stream closes")

			// The stream may hold a dup of the fd; close the original so the reader sees EOF.
			let _ = _test_close(fds[1])
			let path = "/dev/fd/${fds[0]}"
			if(as_string): {
				let f = File.open_read(path): default panic
				assert(f.size().is_err(), "size fails on a pipe")
				let s! = f.read_to_string()
				assert(s!.is_ok(), "read_to_string on pipe succeeds")
				assert(s!.unwrap().len() == payload_len, "read_to_string reads the whole pipe")
			} else: {
				let data = read_bytes(path): default panic
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
	let f = File.open_append(path): default panic
	assert(f.read_to_string().is_err(), "read_to_string reports ferror")
	let seen = ""
	f.read_bytes(4):
		when eof(_): seen = "eof"
		default err(_): seen = "err"
	assert(seen == "err", "read_bytes reports ferror as `err`, not `eof`")
	assert(f.read_all_bytes().is_err(), "read_all_bytes reports ferror")
	f.close(): default panic

	let closed = File.open_read(path): default panic
	assert(closed.close().is_ok(), "close succeeds")
	assert(closed.close().is_err(), "second close reports failure")
	assert(closed.flush().is_err(), "flush on closed file reports failure")
	assert(closed.read_all_bytes().is_err(), "read on closed file fails")

	remove_file(path): default panic
}

@test("filesystem remove and rename tell a missing path from other failures")
bind test_fs_remove_rename_failures = fn(): {
	let missing = "${_test_tmp_path("fs_missing")}.txt"
	let seen = ""
	remove_file(missing):
		when not_found(e): seen = e.message()
		default err(_): seen = "err"
	assert(
		seen.contains("No such file or directory"),
		"a missing file is `not_found` with the OS message",
	)

	rename_file(missing, "$missing.new"):
		when not_found(_): seen = "not_found"
		default err(_): seen = "err"
	assert(seen == "not_found", "renaming a missing file is `not_found`")

	let real = "${_test_tmp_path("fs_real")}.txt"
	write_file(real, "x"): default panic
	rename_file(real, "/nonexistent_dir_lowlight/x.txt"):
		when not_found(_): seen = "not_found"
		default err(_): seen = "err"
	assert(seen == "err", "a missing destination directory is `err`, not `not_found`")

	let dir = _test_tmp_path("fs_dir")
	os.mkdir(dir): default panic
	write_file("$dir/f", "x"): default panic
	remove_file(dir):
		when not_found(_): seen = "not_found"
		default err(e): seen = e.message()
	assert(seen.starts_with("failed to remove"), "removing a non-empty directory is `err`")
	remove_file("$dir/f"): default panic
	remove_file(dir): default panic
	remove_file(real): default panic
}

@test("filesystem open and read report a missing file, end of file, and OS reasons")
bind test_fs_open_and_read_failures = fn(): {
	let missing = "${_test_tmp_path("fs_open_missing")}.txt"
	let seen = ""
	read_file(missing):
		when not_found(e): seen = e.message()
		default err(_): seen = "err"
	assert(
		seen == "failed to open \"$missing\" for reading: No such file or directory",
		"a missing file is `not_found` with the OS message",
	)
	File.open_write("/nonexistent_lowlight_dir/x.txt"):
		when not_found(e): seen = if(e.has_code("ENOENT")): "not_found" else: "no code"
		default err(_): seen = "err"
	assert(seen == "not_found", "a missing directory is `not_found`, coded with the errno name")
	let path = "${_test_tmp_path("fs_chunks")}.txt"
	write_file(path, "abcdefg"): default panic
	let f = File.open_read(path): default panic
	let chunks: [string] = []
	for(true): {
		let chunk = f.read_bytes(3):
			when eof(_): for <-
			default panic
		let s = ""
		for(b in chunk): s.push_byte(b)
		chunks.push(own s)
	}
	assert(chunks.join(",") == "abc,def,g", "read_bytes reads pieces until `eof`")
	assert(
		write_file("/nonexistent_lowlight_dir/x.txt", "x").is_err(),
		"write_file into a missing directory fails",
	)
	remove_file(path): default panic
}

comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	@test_only
	extern link as _fs_test_link: fn(existing: ptr[u8], linkpath: ptr[u8]) i32

	@test_only
	extern symlink as _fs_test_symlink: fn(target: ptr[u8], linkpath: ptr[u8]) i32
}

@test_only
bind _test_link_pair = fn(existing: string, linkpath: string, sym: bool) bool: {
	comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		if(sym): unsafe (_fs_test_symlink(existing.c_str(), linkpath.c_str()) == i32(0))
		else: unsafe (_fs_test_link(existing.c_str(), linkpath.c_str()) == i32(0))
	} else comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		if(sym): {
			let sys_sym = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 36 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 266 else: comp.compile_error("std/fs: unsupported Linux architecture for symlinkat")
			unsafe syscall(sys_sym, int(ptr_addr(existing.c_str())), -100, int(ptr_addr(linkpath.c_str())), 0, 0, 0) == 0
		} else: {
			let sys_lnk = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 37 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 265 else: comp.compile_error("std/fs: unsupported Linux architecture for linkat")
			unsafe syscall(sys_lnk, -100, int(ptr_addr(existing.c_str())), -100, int(ptr_addr(linkpath.c_str())), 0, 0) == 0
		}
	} else: {
		let _ = existing.len() + linkpath.len()
		false
	}
}

@test_only
bind _assert_file_einval = fn(r!: own FileResult[int], label: string): {
	let ok = false
	r!:
		when err(e): ok = e.has_code("EINVAL")
		default: {}
	assert(ok, label)
}

@test("filesystem copy_file self-copy guard, NUL byte checks, zero-copy bytes, and buffered coherence")
bind test_fs_safety_zero_copy_and_buffering = fn(): {
	let base = _test_tmp_path("fs_safe")
	let _ = os.remove_all(base)
	os.mkdir_p(base): default panic

	let p = "${base}/orig.txt"
	let payload = "keep this content intact!"
	write_file(p, payload): default panic

	_assert_file_einval(copy_file(p, p), "copy_file refuses identical path")
	_assert_file_einval(copy_file(p, "${base}/./orig.txt"), "copy_file refuses ./ alias")

	let hard = "${base}/hard.txt"
	let sym = "${base}/sym.txt"
	assert(_test_link_pair(p, hard, false), "hardlink succeeds")
	assert(_test_link_pair(p, sym, true), "symlink succeeds")
	_assert_file_einval(copy_file(p, hard), "copy_file refuses hardlink to same inode")
	_assert_file_einval(copy_file(p, sym), "copy_file refuses symlink to same inode")
	_assert_file_einval(copy_file(sym, p), "copy_file from symlink to target refuses self-copy")
	let kept = read_file(p): default panic
	assert(kept == payload, "original file content preserved after rejected self-copies")

	// Interior NUL rejection
	let nul_p = "${base}/bad"
	nul_p.push_byte(u8(0))
	nul_p.push(".txt")
	assert(not exists(nul_p), "exists rejects interior NUL")
	let got_open_r = false
	File.open_read(nul_p):
		when err(e): got_open_r = e.has_code("EINVAL")
		default: {}
	assert(got_open_r, "open_read rejects interior NUL")
	let got_open_w = false
	File.open_write(nul_p):
		when err(e): got_open_w = e.has_code("EINVAL")
		default: {}
	assert(got_open_w, "open_write rejects interior NUL")
	let got_rm = false
	remove_file(nul_p):
		when err(e): got_rm = e.has_code("EINVAL")
		default: {}
	assert(got_rm, "remove_file rejects interior NUL")
	let got_ren = false
	rename_file(p, nul_p):
		when err(e): got_ren = e.has_code("EINVAL")
		default: {}
	assert(got_ren, "rename_file rejects interior NUL dst")
	_assert_file_einval(copy_file(p, nul_p), "copy_file rejects interior NUL dst")
	let nul_m = "r"
	nul_m.push_byte(u8(0))
	let got_mode = false
	File.open(p, nul_m):
		when err(e): got_mode = e.has_code("EINVAL")
		default: {}
	assert(got_mode, "File.open rejects interior NUL mode")

	// Multi-chunk zero-copy write_bytes / read_bytes / read_all_bytes (> 32 KiB)
	let big_path = "${base}/big.bin"
	let big: [u8] = []
	for(i in 0..40000): big.push(u8((i * 13 + 7) % 251))
	write_bytes(big_path, big): default panic
	let got_big = read_bytes(big_path): default panic
	assert(got_big.len() == 40000, "zero-copy roundtrip len")
	assert(got_big[0] == u8(7) and got_big[39999] == big[39999], "zero-copy roundtrip bytes match")

	let f_big = File.open_read(big_path): default panic
	let chunk1 = f_big.read_bytes(100000):
		when eof(_): panic("unexpected eof")
		default panic
	assert(chunk1.len() == 40000 and chunk1[1234] == big[1234], "oversized max_bytes shrinks cleanly")
	f_big.close(): default panic

	// Small buffered writes + seek/tell/read coherence without explicit flush
	let rw_path = "${base}/rw_buf.txt"
	let rw = File.open(rw_path, "w+b"): default panic
	for(i in 0..200): rw.write_string("ab"): default panic
	assert(rw.tell().unwrap() == 400, "tell accounts for buffered writes")
	assert(rw.seek(0, SEEK_SET).unwrap() == 0, "seek flushes buffered writes")
	let back = rw.read_to_string(): default panic
	assert(back.len() == 400 and back.starts_with("abababab"), "read sees flushed writes")
	rw.close(): default panic

	let _ = os.remove_all(base)
}
