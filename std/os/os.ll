// std/os — Process execution, filesystem queries, and host platform introspection.

// -----------------------------------------------------------------------------
// Target POSIX / Syscall Bindings
// -----------------------------------------------------------------------------

comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	extern __error as _os_errno_ptr: fn() ptr[i32]
	extern strerror_r as _os_strerror_r: fn(code: i32, buf: ptr[u8], len: int) i32
	extern openat as _os_openat: fn(dirfd: i32, path: ptr[u8], flags: i32) i32
	extern unlinkat as _os_unlinkat: fn(dirfd: i32, path: ptr[u8], flags: i32) i32
	extern dirfd as _os_dirfd: fn(dir: ptr[u8]) i32
	extern ll_rt_fcntl as _os_fcntl: fn(fd: i32, cmd: i32, arg: i32) i32
	extern popen as _os_popen: fn(cmd: ptr[u8], mode: ptr[u8]) ptr[u8]
	extern pclose as _os_pclose: fn(stream: ptr[u8]) i32
	extern fread as _os_fread: fn(buf: ptr[u8], size: int, count: int, stream: ptr[u8]) int
	extern ferror as _os_ferror: fn(stream: ptr[u8]) i32
	extern clearerr as _os_clearerr: fn(stream: ptr[u8])
	extern system as _os_system: fn(cmd: ptr[u8]) i32
	extern access as _os_access: fn(path: ptr[u8], mode: i32) i32
	extern mkdir as _os_mkdir: fn(path: ptr[u8], mode: i32) i32
	extern open as _os_open: fn(path: ptr[u8], flags: i32) i32
	extern close as _os_close: fn(fd: i32) i32
	extern unlink as _os_unlink: fn(path: ptr[u8]) i32
	extern rmdir as _os_rmdir: fn(path: ptr[u8]) i32
	extern closedir as _os_closedir: fn(dir: ptr[u8]) i32
	extern sysconf as _os_sysconf: fn(name: i32) int
	comptime if(comp.TARGET.arch == comp.TargetArch.amd64): {
		extern "stat\$INODE64" as _os_stat: fn(path: ptr[u8], buf: ptr[u8]) i32
		extern "fdopendir\$INODE64" as _os_fdopendir: fn(fd: i32) ptr[u8]
		extern "readdir\$INODE64" as _os_readdir: fn(dir: ptr[u8]) ptr[u8]
	} else comptime if(comp.TARGET.arch == comp.TargetArch.arm64): {
		extern stat as _os_stat: fn(path: ptr[u8], buf: ptr[u8]) i32
		extern fdopendir as _os_fdopendir: fn(fd: i32) ptr[u8]
		extern readdir as _os_readdir: fn(dir: ptr[u8]) ptr[u8]
	} else: {
		const _os_unsupported_darwin_arch = comp.compile_error("std/os: unsupported Darwin architecture")
	}
} else comptime if(comp.TARGET.os == comp.TargetOs.linux): {
	extern __errno_location as _os_errno_ptr: fn() ptr[i32]
	extern "__xpg_strerror_r" as _os_strerror_r: fn(code: i32, buf: ptr[u8], len: int) i32
	extern dirfd as _os_dirfd: fn(dir: ptr[u8]) i32
	extern popen as _os_popen: fn(cmd: ptr[u8], mode: ptr[u8]) ptr[u8]
	extern pclose as _os_pclose: fn(stream: ptr[u8]) i32
	extern fread as _os_fread: fn(buf: ptr[u8], size: int, count: int, stream: ptr[u8]) int
	extern ferror as _os_ferror: fn(stream: ptr[u8]) i32
	extern clearerr as _os_clearerr: fn(stream: ptr[u8])
	extern system as _os_system: fn(cmd: ptr[u8]) i32
}

// -----------------------------------------------------------------------------
// OS errors
// -----------------------------------------------------------------------------

// Returns the calling thread's current OS error number (`errno`).
bind last_errno = fn() int: {
	comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
		unsafe {
			let p = _os_errno_ptr()
			if(is_null(p)): 0 else: int(ptr_read(p))
		}
	} else: 0
}

// Updates the calling thread's OS error number (`errno`).
bind set_last_errno = fn(code: int): {
	comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
		unsafe {
			let p = _os_errno_ptr()
			if(not is_null(p)): ptr_write(p, i32(code))
		}
	} else: {
		let _ = code
	}
}

bind _decode_wait_status = fn(st: int) int: {
	if(st < 0): func <- -1
	let sig = st & 127
	if(sig == 0): (st >> 8) & 255
	else if(sig == 127): -1
	else: 256 + sig
}

// The symbolic name of OS error code `code` (an `errno` value), such as `"ENOENT"` or
// `"ECONNREFUSED"`. Names are the same on every platform even where the numbers differ, so
// compare names, not numbers. A code the runtime doesn't list is `"E<code>"`.
bind errno_name = fn(code: int) string: {
	let name = comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		match(code):
			when 1: "EPERM"
			when 2: "ENOENT"
			when 3: "ESRCH"
			when 4: "EINTR"
			when 5: "EIO"
			when 6: "ENXIO"
			when 7: "E2BIG"
			when 8: "ENOEXEC"
			when 9: "EBADF"
			when 10: "ECHILD"
			when 12: "ENOMEM"
			when 13: "EACCES"
			when 14: "EFAULT"
			when 16: "EBUSY"
			when 17: "EEXIST"
			when 18: "EXDEV"
			when 19: "ENODEV"
			when 20: "ENOTDIR"
			when 21: "EISDIR"
			when 22: "EINVAL"
			when 23: "ENFILE"
			when 24: "EMFILE"
			when 25: "ENOTTY"
			when 26: "ETXTBSY"
			when 27: "EFBIG"
			when 28: "ENOSPC"
			when 29: "ESPIPE"
			when 30: "EROFS"
			when 31: "EMLINK"
			when 32: "EPIPE"
			when 33: "EDOM"
			when 34: "ERANGE"
			when 35: "EAGAIN"
			when 36: "EINPROGRESS"
			when 37: "EALREADY"
			when 38: "ENOTSOCK"
			when 40: "EMSGSIZE"
			when 43: "EPROTONOSUPPORT"
			when 47: "EAFNOSUPPORT"
			when 48: "EADDRINUSE"
			when 49: "EADDRNOTAVAIL"
			when 50: "ENETDOWN"
			when 51: "ENETUNREACH"
			when 53: "ECONNABORTED"
			when 54: "ECONNRESET"
			when 55: "ENOBUFS"
			when 56: "EISCONN"
			when 57: "ENOTCONN"
			when 58: "ESHUTDOWN"
			when 60: "ETIMEDOUT"
			when 61: "ECONNREFUSED"
			when 62: "ELOOP"
			when 63: "ENAMETOOLONG"
			when 65: "EHOSTUNREACH"
			when 66: "ENOTEMPTY"
			when 69: "EDQUOT"
			when 78: "ENOSYS"
			when 89: "ECANCELED"
			default: ""
	} else comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		match(code):
			when 1: "EPERM"
			when 2: "ENOENT"
			when 3: "ESRCH"
			when 4: "EINTR"
			when 5: "EIO"
			when 6: "ENXIO"
			when 7: "E2BIG"
			when 8: "ENOEXEC"
			when 9: "EBADF"
			when 10: "ECHILD"
			when 11: "EAGAIN"
			when 12: "ENOMEM"
			when 13: "EACCES"
			when 14: "EFAULT"
			when 16: "EBUSY"
			when 17: "EEXIST"
			when 18: "EXDEV"
			when 19: "ENODEV"
			when 20: "ENOTDIR"
			when 21: "EISDIR"
			when 22: "EINVAL"
			when 23: "ENFILE"
			when 24: "EMFILE"
			when 25: "ENOTTY"
			when 26: "ETXTBSY"
			when 27: "EFBIG"
			when 28: "ENOSPC"
			when 29: "ESPIPE"
			when 30: "EROFS"
			when 31: "EMLINK"
			when 32: "EPIPE"
			when 33: "EDOM"
			when 34: "ERANGE"
			when 36: "ENAMETOOLONG"
			when 38: "ENOSYS"
			when 39: "ENOTEMPTY"
			when 40: "ELOOP"
			when 88: "ENOTSOCK"
			when 90: "EMSGSIZE"
			when 93: "EPROTONOSUPPORT"
			when 97: "EAFNOSUPPORT"
			when 98: "EADDRINUSE"
			when 99: "EADDRNOTAVAIL"
			when 100: "ENETDOWN"
			when 101: "ENETUNREACH"
			when 103: "ECONNABORTED"
			when 104: "ECONNRESET"
			when 105: "ENOBUFS"
			when 106: "EISCONN"
			when 107: "ENOTCONN"
			when 108: "ESHUTDOWN"
			when 110: "ETIMEDOUT"
			when 111: "ECONNREFUSED"
			when 113: "EHOSTUNREACH"
			when 114: "EALREADY"
			when 115: "EINPROGRESS"
			when 122: "EDQUOT"
			when 125: "ECANCELED"
			default: ""
	} else: ""
	if(name.is_empty()): "E$code" else: name
}

// An [`Error`] for OS error `code` (an `errno` value): the message is `what` followed by the OS's
// description, and the error's code is the errno name, so `e.has_code("EACCES")` works.
//
// ```lowlight
// let e = os.error_from_errno(2, "failed to open \"config.toml\"")
// // failed to open "config.toml": No such file or directory
// ```
@track_caller
bind error_from_errno = fn(code: int, what: string) Error: {
	let desc = comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
		unsafe {
			let sbuf = alloc_uninit[u8](256)
			ptr_write(sbuf, u8(0))
			let s_rc = _os_strerror_r(i32(code), sbuf, 256)
			let msg = if(s_rc == i32(0)): str_from_cstr(sbuf) else: "errno $code"
			free_raw(sbuf)
			msg
		}
	} else: {
		"errno $code"
	}
	Error.coded(errno_name(code), "$what: $desc")
}

// -----------------------------------------------------------------------------
// Processes
// -----------------------------------------------------------------------------

// Captured output and exit status returned by [`exec`] and [`system_run_status`].
define ExecResult struct [
	// Captured standard output produced by the command (empty for [`system_run_status`]).
	stdout: string,
	// Exit code (`0` to `255`), or `-1` if the command was killed by a signal.
	exit_code: int,
	// The signal that terminated the command (`9` for `SIGKILL`), or `0` if it exited normally.
	signal: int = 0,
]

// Reports whether the command exited cleanly with status `0`.
bind ExecResult.ok = fn(self) bool: self.exit_code == 0

// Reports whether the command was terminated by a signal rather than exiting.
bind ExecResult.signaled = fn(self) bool: self.signal != 0

// Builds an [`ExecResult`] from a runtime status: an exit code or `256 + signal`.
bind _exec_result = fn(out: own string, status: int) ExecResult:
	if(status >= 256): ExecResult[stdout: own out, exit_code: -1, signal: status - 256]
	else: ExecResult[stdout: own out, exit_code: status, signal: 0]

// Runs shell command `cmd` through `/bin/sh` (`popen`), capturing its standard output.
//
// A command that runs and fails is still a value: check [`ExecResult.ok`] or `exit_code` (the
// shell reports a command it can't find as exit code `127`).
//
// # Errors
// Fails if the shell can't be started at all (for example, no processes or file descriptors
// are left), with the OS's description.
bind exec = fn(cmd: string) Result[ExecResult]: {
	if(cmd.contains_byte(u8(0))): {
		set_last_errno(22)
		func <- err(error_from_errno(22, "invalid command: interior NUL byte"))
	}
	comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
		let stream = unsafe _os_popen(cmd.c_str(), "r".c_str())
		if(unsafe (stream == null_ptr[u8]())): func <- err(
			error_from_errno(last_errno(), "failed to run `$cmd`")
		)
		let out_str = ""
		let read_failed = false
		let read_err = 0
		unsafe {
			let chunk_size = 4096
			let raw = alloc_raw[u8](chunk_size)
			for(true): {
				set_last_errno(0)
				let n = _os_fread(raw, 1, chunk_size, stream)
				if(n > 0): out_str.push_bytes(raw, n)
				if(n < chunk_size): {
					if(_os_ferror(stream) != i32(0)): {
						let code = last_errno()
						if(code == 4): {
							_os_clearerr(stream)
							next <-
						}
						read_failed = true
						read_err = if(code != 0): code else: 5
					}
					for <-
				}
			}
			free_raw(raw)
		}
		let raw_close = int(unsafe _os_pclose(stream))
		if(read_failed): {
			set_last_errno(read_err)
			func <- err(error_from_errno(read_err, "failed reading output from `$cmd`"))
		}
		let status = _decode_wait_status(raw_close)
		if(status < 0): func <- err(error_from_errno(last_errno(), "failed to wait for `$cmd`"))
		value(_exec_result(own out_str, status))
	} else: {
		let _ = cmd.len()
		err(Error.new("process execution is unsupported on this target"))
	}
}

// Runs `cmd` through the host shell (`system`) and returns its exit code, or `-1` if it was
// killed by a signal. Use [`system_run_status`] to see the signal.
//
// # Errors
// Fails if the shell can't be started, with the OS's description.
bind system_run = fn(cmd: string) Result[int]: {
	if(cmd.contains_byte(u8(0))): {
		set_last_errno(22)
		func <- err(error_from_errno(22, "invalid command: interior NUL byte"))
	}
	comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
		let status = _decode_wait_status(int(unsafe _os_system(cmd.c_str())))
		if(status < 0): func <- err(error_from_errno(last_errno(), "failed to run `$cmd`"))
		value(if(status >= 256): -1 else: status)
	} else: {
		let _ = cmd.len()
		err(Error.new("process execution is unsupported on this target"))
	}
}

// Runs `cmd` through the host shell (`system`) and returns its full status, including the
// terminating signal, as an [`ExecResult`] with empty `stdout`.
//
// # Errors
// Fails if the shell can't be started, with the OS's description.
bind system_run_status = fn(cmd: string) Result[ExecResult]: {
	if(cmd.contains_byte(u8(0))): {
		set_last_errno(22)
		func <- err(error_from_errno(22, "invalid command: interior NUL byte"))
	}
	comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
		let status = _decode_wait_status(int(unsafe _os_system(cmd.c_str())))
		if(status < 0): func <- err(error_from_errno(last_errno(), "failed to run `$cmd`"))
		value(_exec_result("", status))
	} else: {
		let _ = cmd.len()
		err(Error.new("process execution is unsupported on this target"))
	}
}

// -----------------------------------------------------------------------------
// Files and directories
// -----------------------------------------------------------------------------

// Reports whether a file or directory exists at `path`.
bind file_exists = fn(path: string) bool: {
	if(path.len() == 0 or path.contains_byte(u8(0))): func <- false
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_faccessat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 48 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 269 else: comp.compile_error("std/os: unsupported Linux architecture for faccessat")
		unsafe syscall(sys_faccessat, -100, int(ptr_addr(path.c_str())), 0, 0, 0, 0) == 0
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe _os_access(path.c_str(), i32(0)) == i32(0)
	} else: false
}

// The result of [`mkdir`].
@must_use
define MkdirResult result [
	// The directory was created.
	value,
	// Something (a directory or a file) already exists at the path.
	exists: Error,
	// Any other failure, with the OS's description (a missing parent, permission denied, ...).
	err: Error,
]

// Creates a single directory at `path` (mode `0755`). Its parent must already exist; see
// [`mkdir_p`] to create the parents too.
//
// # Errors
// [`MkdirResult.exists`] if anything already exists at `path`; [`MkdirResult.err`] for any other
// failure, with the OS's description.
bind mkdir = fn(path: string) MkdirResult: {
	if(path.len() == 0 or path.contains_byte(u8(0))): {
		set_last_errno(22)
		func <- err(error_from_errno(22, "failed to create directory \"$path\""))
	}
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		let code = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
			let sys_mkdirat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 34 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 258 else: comp.compile_error("std/os: unsupported Linux architecture for mkdirat")
			let rc = unsafe syscall(sys_mkdirat, -100, int(ptr_addr(path.c_str())), 493, 0, 0, 0)
			if(rc == 0): func <- value
			let c = -rc
			set_last_errno(c)
			c
		} else: {
			if(unsafe _os_mkdir(path.c_str(), i32(493)) == i32(0)): func <- value
			last_errno()
		}
		let e = error_from_errno(code, "failed to create directory \"$path\"")
		if(errno_name(code) == "EEXIST"): exists(e) else: err(e)
	} else: err(Error.new("mkdir is unsupported on this target"))
}

bind _is_dir = fn(path: string) bool: {
	if(path.len() == 0 or path.contains_byte(u8(0))): func <- false
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_newfstatat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 79 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 262 else: comp.compile_error("std/os: unsupported Linux architecture for newfstatat")
		let mode_off = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 16 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 24 else: comp.compile_error("std/os: unsupported Linux architecture for stat layout")
		unsafe {
			let st = alloc_raw[u8](160)
			let rc = syscall(sys_newfstatat, -100, int(ptr_addr(path.c_str())), int(ptr_addr(st)), 0, 0, 0)
			let ok = rc == 0 and (int(ptr_read(ptr_cast[u32](ptr_add(st, mode_off)))) & 61440) == 16384
			free_raw(st)
			ok
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let st = alloc_raw[u8](144)
			let rc = _os_stat(path.c_str(), st)
			let ok = rc == i32(0) and (int(ptr_read(ptr_cast[u16](ptr_add(st, 4)))) & 61440) == 16384
			free_raw(st)
			ok
		}
	} else: false
}

// Creates the directory `path` and any missing parents. A directory that already exists is
// success.
//
// # Errors
// Fails if `path` is empty, if `path` or one of its parents exists but isn't a directory, or if
// a directory can't be created, with the OS's description.
bind mkdir_p = fn(path: string) Status: {
	if(path.len() == 0): func <- err(Error.new("cannot create a directory with an empty path"))
	if(path.contains_byte(u8(0))): {
		set_last_errno(22)
		func <- err(error_from_errno(22, "invalid path: interior NUL byte"))
	}
	let target = path.copy()
	for(target.len() > 1 and target.byte_at(target.len() - 1) == u8(47)):
		target = target[0..target.len() - 1]
	let n = target.len()
	// Create each prefix that ends at a `/`, then the target itself.
	for(i in 1..n + 1): {
		if(i < n and target.byte_at(i) != u8(47)): next <-
		let sub = target[0..i]
		if(_is_dir(sub)): next <-
		mkdir(sub):
			when exists(_): {
				if(_is_dir(sub)): next <-
				func <- err(
					Error.new(
						"failed to create directory \"$target\": \"$sub\" exists and is not a directory"
					)
				)
			}
			default err(own e): func <- err(e.wrap("failed to create directory \"$target\""))
	}
	value
}

bind _is_dot_or_dotdot = fn(clean: string) bool:
	clean == "." or clean == ".." or clean.ends_with("/.") or clean.ends_with("/..")

// Removes `path` and everything under it. Symbolic links are removed, not followed. A path that
// doesn't exist is success.
//
// # Errors
// Fails if `path` is empty, `/`, `.`, `..`, or if anything can't be removed, with the OS's
// description of the first failure. Whatever could be removed is gone.
// FD-anchored recursive removal helper so symlink replacements between directory
// traversal and unlinking cannot redirect operations outside `parent_fd`.
bind _remove_all_at = fn(parent_fd: int, name_ptr: ptr[u8], display_path: string) Status: {
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_openat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 56 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 257 else: comp.compile_error("std/os: unsupported Linux architecture for openat")
		let sys_close = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 57 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 3 else: comp.compile_error("std/os: unsupported Linux architecture for close")
		let sys_unlinkat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 35 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 263 else: comp.compile_error("std/os: unsupported Linux architecture for unlinkat")
		let sys_getdents64 = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 61 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 217 else: comp.compile_error("std/os: unsupported Linux architecture for getdents64")
		// O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC
		let open_dir_flags = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 573440 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 720896 else: comp.compile_error("std/os: unsupported Linux architecture for open flags")
		unsafe {
			let urc = syscall(sys_unlinkat, parent_fd, int(ptr_addr(name_ptr)), 0, 0, 0, 0)
			while(urc == -4): {
				urc = syscall(sys_unlinkat, parent_fd, int(ptr_addr(name_ptr)), 0, 0, 0, 0)
			}
			if(urc == 0 or urc == -2): func <- value
			let uerr = -urc
			let dfd = syscall(sys_openat, parent_fd, int(ptr_addr(name_ptr)), open_dir_flags, 0, 0, 0)
			while(dfd == -4): {
				dfd = syscall(sys_openat, parent_fd, int(ptr_addr(name_ptr)), open_dir_flags, 0, 0, 0)
			}
			if(dfd == -2): func <- value
			if(dfd < 0): {
				let code = if(dfd == -40): 40 else: uerr
				set_last_errno(code)
				func <- err(error_from_errno(code, "failed to remove \"$display_path\""))
			}
			let buf = alloc_raw[u8](4096)
			let read_err = 0
			let children: [string] = []
			for(true): {
				let n = syscall(sys_getdents64, dfd, int(ptr_addr(buf)), 4096, 0, 0, 0)
				if(n == -4): next <-
				if(n == 0): for <-
				if(n < 0): {
					if(read_err == 0): read_err = -n
					for <-
				}
				let off = 0
				for(off + 19 <= n): {
					let lo = int(ptr_read(ptr_add(buf, off + 16)))
					let hi = int(ptr_read(ptr_add(buf, off + 17)))
					let rlen = lo + (hi << 8)
					if(rlen <= 0 or off + rlen > n): for <-
					let name = str_from_cstr(ptr_add(buf, off + 19))
					if(name != "." and name != ".."): children.push(own name)
					off += rlen
				}
			}
			free_raw(buf)
			if(read_err != 0): {
				let _ = syscall(sys_close, dfd, 0, 0, 0, 0, 0)
				set_last_errno(read_err)
				func <- err(error_from_errno(read_err, "failed to remove \"$display_path\""))
			}
			let first_code = 0
			let first_msg = ""
			for(ch in children): {
				_remove_all_at(dfd, ch.c_str(), "$display_path/$ch"):
					default err(own e): {
						if(first_code == 0): {
							first_code = last_errno()
							if(first_code == 0): first_code = 5
							first_msg = e.message().copy()
						}
					}
			}
			let _ = syscall(sys_close, dfd, 0, 0, 0, 0, 0)
			if(first_code != 0): {
				set_last_errno(first_code)
				let name = errno_name(first_code)
				let out_err = Error.coded(own name, own first_msg)
				func <- err(own out_err)
			}
			let rrc = syscall(sys_unlinkat, parent_fd, int(ptr_addr(name_ptr)), 512, 0, 0, 0)
			while(rrc == -4): {
				rrc = syscall(sys_unlinkat, parent_fd, int(ptr_addr(name_ptr)), 512, 0, 0, 0)
			}
			if(rrc == 0 or rrc == -2): value
			else: {
				let c = -rrc
				set_last_errno(c)
				err(error_from_errno(c, "failed to remove \"$display_path\""))
			}
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let urc = _os_unlinkat(i32(parent_fd), name_ptr, i32(0))
			while(urc < i32(0) and last_errno() == 4): {
				urc = _os_unlinkat(i32(parent_fd), name_ptr, i32(0))
			}
			if(urc == i32(0)): func <- value
			let uerr = last_errno()
			if(uerr == 2): func <- value
			// O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC
			let dfd = _os_openat(i32(parent_fd), name_ptr, i32(17826048))
			while(dfd < i32(0) and last_errno() == 4): {
				dfd = _os_openat(i32(parent_fd), name_ptr, i32(17826048))
			}
			if(dfd < i32(0)): {
				let oerr = last_errno()
				if(oerr == 2): func <- value
				let code = if(oerr == 62): oerr else: uerr
				set_last_errno(code)
				func <- err(error_from_errno(code, "failed to remove \"$display_path\""))
			}
			// Duplicate dfd before fdopendir so closedir releases DIR buffers prior
			// to child recursion while dfd remains open as the inode anchor.
			let dup_fd = _os_fcntl(dfd, i32(67), i32(0))
			if(dup_fd < i32(0)): {
				let derr = last_errno()
				let _ = _os_close(dfd)
				set_last_errno(derr)
				func <- err(error_from_errno(derr, "failed to remove \"$display_path\""))
			}
			let d = _os_fdopendir(dup_fd)
			if(is_null(d)): {
				let derr = last_errno()
				let _ = _os_close(dup_fd)
				let _ = _os_close(dfd)
				set_last_errno(derr)
				func <- err(error_from_errno(derr, "failed to remove \"$display_path\""))
			}
			let children: [string] = []
			let read_err = 0
			for(true): {
				set_last_errno(0)
				let de = _os_readdir(d)
				if(is_null(de)): {
					read_err = last_errno()
					for <-
				}
				let name = str_from_cstr(ptr_add(de, 21))
				if(name != "." and name != ".."): children.push(own name)
			}
			let _ = _os_closedir(d)
			if(read_err != 0): {
				let _ = _os_close(dfd)
				set_last_errno(read_err)
				func <- err(error_from_errno(read_err, "failed to remove \"$display_path\""))
			}
			let first_code = 0
			let first_msg = ""
			for(ch in children): {
				_remove_all_at(int(dfd), ch.c_str(), "$display_path/$ch"):
					default err(own e): {
						if(first_code == 0): {
							first_code = last_errno()
							if(first_code == 0): first_code = 5
							first_msg = e.message().copy()
						}
					}
			}
			let _ = _os_close(dfd)
			if(first_code != 0): {
				set_last_errno(first_code)
				let name = errno_name(first_code)
				let out_err = Error.coded(own name, own first_msg)
				func <- err(own out_err)
			}
			let rrc = _os_unlinkat(i32(parent_fd), name_ptr, i32(128))
			while(rrc < i32(0) and last_errno() == 4): {
				rrc = _os_unlinkat(i32(parent_fd), name_ptr, i32(128))
			}
			if(rrc == i32(0)): value
			else: {
				let rerr = last_errno()
				if(rerr == 2): value
				else: {
					set_last_errno(rerr)
					err(error_from_errno(rerr, "failed to remove \"$display_path\""))
				}
			}
		}
	} else: {
		let _ = parent_fd
		let _ = name_ptr
		let _ = display_path.len()
		err(Error.new("remove_all is unsupported on this target"))
	}
}

bind _remove_all_impl = fn(path: string) Status: {
	let target = path.copy()
	for(target.len() > 1 and target.byte_at(target.len() - 1) == u8(47)):
		target = target[0..target.len() - 1]
	if(target == "/"): func <- err(Error.new("refusing to remove the root directory"))
	if(_is_dot_or_dotdot(target)): {
		set_last_errno(22)
		func <- err(Error.new("refusing to remove \".\" or \"..\" directory component in \"$path\""))
	}
	let at_fdcwd = comptime if(comp.TARGET.os == comp.TargetOs.darwin): -2 else: -100
	unsafe _remove_all_at(at_fdcwd, target.c_str(), path)
}

bind remove_all = fn(path: string) Status: {
	if(path.len() == 0): func <- err(Error.new("cannot remove an empty path"))
	if(path.contains_byte(u8(0))): {
		set_last_errno(22)
		func <- err(error_from_errno(22, "invalid path: interior NUL byte"))
	}
	let clean = path.copy()
	for(clean.len() > 1 and clean.byte_at(clean.len() - 1) == u8(47)):
		clean = clean[0..clean.len() - 1]
	if(clean == "/"): func <- err(Error.new("refusing to remove the root directory"))
	if(_is_dot_or_dotdot(clean)): {
		set_last_errno(22)
		func <- err(Error.new("refusing to remove \".\" or \"..\" directory component in \"$path\""))
	}
	_remove_all_impl(clean)
}

// Returns the number of logical CPU cores available on the host system.
bind num_cpus = fn() int: {
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_sched_getaffinity = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 123 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 204 else: comp.compile_error("std/os: unsupported Linux architecture for sched_getaffinity")
		unsafe {
			let mask = alloc_raw[u8](128)
			fill_bytes(mask, u8(0), 128)
			let n = syscall(sys_sched_getaffinity, 0, 128, int(ptr_addr(mask)), 0, 0, 0)
			let count = 0
			if(n > 0): {
				let bytes_n = if(n > 128): 128 else: n
				for(i in 0..bytes_n): {
					let b = int(ptr_read(ptr_add(mask, i)))
					for(bit in 0..8): {
						if(((b >> bit) & 1) != 0): count += 1
					}
				}
			}
			free_raw(mask)
			if(count >= 1): count else: 1
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		let n = unsafe _os_sysconf(i32(58))
		if(n >= 1): n else: 1
	} else: 1
}

// Returns the target operating system name string (`"macos"`, `"linux"`, `"windows"`, etc.).
const os_name = fn() string:
	comptime match(comp.TARGET.os):
		when comp.TargetOs.darwin: "macos"
		when comp.TargetOs.linux: "linux"
		when comp.TargetOs.windows: "windows"
		when comp.TargetOs.freebsd: "freebsd"
		when comp.TargetOs.openbsd: "openbsd"
		when comp.TargetOs.netbsd: "netbsd"
		when comp.TargetOs.wasm: "wasm"
		default: "unknown"

// Returns the target CPU architecture identifier string (`"arm64"`, `"x86_64"`, `"wasm32"`, etc.).
const arch = fn() string:
	comptime match(comp.TARGET.arch):
		when comp.TargetArch.amd64: "x86_64"
		when comp.TargetArch.arm64: "arm64"
		when comp.TargetArch.riscv64: "riscv64"
		when comp.TargetArch.x86: "x86"
		when comp.TargetArch.wasm32: "wasm32"
		when comp.TargetArch.wasm64: "wasm64"
		default: "unknown"

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test_only
extern getenv as _test_getenv: fn(name: ptr[u8]) ptr[u8]

@test_only
extern clock_gettime as _os_test_clock: fn(clk_id: i32, ts: ptr[u8]) i32

@test_only
bind _test_tmp_path = fn(name: string) string: {
	let base = str_from_cstr(unsafe _test_getenv("TMPDIR".c_str()))
	if(base.len() == 0): base = "/tmp"
	if(base.len() > 1 and base.byte_at(base.len() - 1) == u8(47)):
		base = base[0..base.len() - 1]
	let stamp = unsafe {
		let ts = alloc_raw[int](2)
		ptr_write(ts, 0)
		ptr_write(ptr_add(ts, 1), 0)
		let _ = _os_test_clock(i32(0), ptr_cast[u8](ts))
		let n = ptr_read(ts) * 1000000000 + ptr_read(ptr_add(ts, 1))
		free_raw(ts)
		n
	}
	"${base}/lowlight_test_${name}_${stamp}"
}

@test("os system metadata: num_cpus, os_name, arch, and args")
bind test_os_metadata = fn(): {
	assert(num_cpus() >= 1, "num_cpus >= 1")
	let os = os_name()
	assert(os.len() > 0, "os_name non-empty")
	let a = arch()
	assert(a.len() > 0, "arch non-empty")

	let cmd_args = args()
	assert(cmd_args.len() >= 1, "args contains at least argv[0]")
}

@test("os process execution: exec and ExitResult")
bind test_os_exec = fn(): {
	let res = exec("echo hello_os_test"): default panic
	assert(res.ok(), "exec ok")
	assert(res.exit_code == 0, "exit code 0")
	assert(res.stdout.contains("hello_os_test"), "stdout contains expected string")
	assert(not res.signaled(), "no signal")

	let exit9 = exec("exit 9"): default panic
	assert(exit9.exit_code == 9 and exit9.signal == 0, "exit 9 is an exit code")
	let killed = exec("kill -9 \$\$"): default panic
	assert(killed.exit_code == -1 and killed.signal == 9 and killed.signaled(), "SIGKILL is a signal")
	assert(not killed.ok(), "a killed command is not ok")

	assert(system_run("exit 3").unwrap() == 3, "system_run exit code")
	assert(system_run("kill -15 \$\$").unwrap() == -1, "system_run reports a signal as -1")
	let st = system_run_status("kill -15 \$\$"): default panic
	assert(st.signal == 15 and st.exit_code == -1, "system_run_status reports SIGTERM")
	assert(exec("exit 127").unwrap().exit_code == 127, "a failing command is a value, not an error")
	assert(errno_name(2) == "ENOENT" and errno_name(99999) == "E99999", "errno_name")
	let e = error_from_errno(13, "opening x")
	assert(e.has_code("EACCES") and e.message() == "opening x: Permission denied", "error_from_errno")
}

comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	@test_only
	extern symlink as _os_test_symlink: fn(target: ptr[u8], linkpath: ptr[u8]) i32
}

@test_only
bind _test_symlink = fn(target: string, linkpath: string) bool: {
	comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe (_os_test_symlink(target.c_str(), linkpath.c_str()) == i32(0))
	} else comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_symlinkat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 36 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 266 else: comp.compile_error("std/os: unsupported Linux architecture for symlinkat")
		unsafe syscall(
			sys_symlinkat,
			int(ptr_addr(target.c_str())),
			-100,
			int(ptr_addr(linkpath.c_str())),
			0,
			0,
			0,
		) == 0
	} else: {
		let _ = target.len() + linkpath.len()
		false
	}
}

@test("os directory operations: mkdir_p, file_exists, remove_all")
bind test_os_filesystem = fn(): {
	let base = _test_tmp_path("os_dir")
	let nested = "${base}/sub1/sub2"

	let _ = remove_all(base)
	assert(not file_exists(nested), "nested does not exist before mkdir_p")

	mkdir_p(nested): default panic
	assert(file_exists(nested), "nested exists after mkdir_p")
	assert(mkdir_p(nested).is_ok(), "mkdir_p on existing dir is idempotent")
	assert(mkdir_p("${base}/trailing/").is_ok(), "mkdir_p handles trailing slash")
	assert(mkdir_p("").is_err(), "mkdir_p rejects empty path")

	let seen = ""
	mkdir(nested):
		when exists(_): seen = "exists"
		default err(_): seen = "err"
	assert(seen == "exists", "mkdir on an existing directory is `exists`")
	mkdir("${base}/missing_parent/child"):
		when exists(_): seen = "exists"
		default err(e): seen = e.message()
	assert(
		seen.contains("No such file or directory"),
		"mkdir under a missing parent is `err` with the OS message",
	)

	let reg = "${base}/reg_file.txt"
	assert(core_write_file(reg, ""), "create regular file succeeds")
	assert(file_exists(reg), "regular file exists")
	assert(
		mkdir_p(reg).peek_err()?.message().contains("is not a directory") ?? false,
		"mkdir_p fails when target is regular file",
	)
	assert(mkdir_p("${reg}/").is_err(), "mkdir_p fails when target is regular file with trailing slash")
	assert(mkdir_p("${reg}/child/dir").is_err(), "mkdir_p fails when ancestor is regular file")

	// Deep nesting + symlinks: remove_all removes links without following them into external dirs.
	let deep = "${base}/deep"
	for(i in 0..25): deep = "${deep}/d${i}"
	mkdir_p(deep): default panic
	assert(core_write_file("${deep}/leaf.txt", "leaf"), "create deep leaf file")

	let keep_dir = _test_tmp_path("os_keep_sym")
	let _ = remove_all(keep_dir)
	mkdir_p(keep_dir): default panic
	let keep_file = "${keep_dir}/important.txt"
	assert(core_write_file(keep_file, "safe"), "create keep target file")
	assert(_test_symlink(keep_dir, "${base}/dir_link"), "create symlink to external dir")
	assert(_test_symlink("missing_target_entry", "${base}/dangling_link"), "create dangling symlink")

	assert(remove_all("").is_err() and remove_all("/").is_err(), "remove_all refuses empty and root paths")
	remove_all("${base}/"): default panic
	assert(remove_all(base).is_ok(), "remove_all on a missing path succeeds")
	assert(not file_exists(base), "base does not exist after remove_all")
	assert(core_read_file(keep_file) == "safe", "remove_all did not follow symlink into keep_dir")
	remove_all(keep_dir): default panic
}

comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	@test_only
	extern chmod as _os_test_chmod: fn(path: ptr[u8], mode: i32) i32
}

@test_only
bind _test_chmod = fn(path: string, mode: int) bool: {
	comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe (_os_test_chmod(path.c_str(), i32(mode)) == i32(0))
	} else comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_fchmodat = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 53 else comptime if(comp.TARGET.arch == comp.TargetArch.amd64): 268 else: comp.compile_error("std/os: unsupported Linux architecture for fchmodat")
		unsafe syscall(sys_fchmodat, -100, int(ptr_addr(path.c_str())), mode, 0, 0, 0) == 0
	} else: {
		let _ = path.len() + mode
		false
	}
}

@test("os removes symlinks with trailing slash safely, rejects dot/NUL paths, and handles chmod 000")
bind test_os_safety_and_chmod000 = fn(): {
	let nul_path = "prefix"
	nul_path.push_byte(u8(0))
	nul_path.push("suffix")
	assert(not file_exists(nul_path), "file_exists rejects interior NUL")
	let mk_nul_einval = false
	mkdir(nul_path):
		when err(e): mk_nul_einval = e.has_code("EINVAL")
		default: {}
	assert(mk_nul_einval, "mkdir rejects interior NUL with EINVAL")
	assert(mkdir_p(nul_path).is_err(), "mkdir_p rejects interior NUL")
	assert(remove_all(nul_path).is_err(), "remove_all rejects interior NUL")
	assert(exec(nul_path).is_err(), "exec rejects interior NUL")
	assert(system_run(nul_path).is_err(), "system_run rejects interior NUL")
	assert(system_run_status(nul_path).is_err(), "system_run_status rejects interior NUL")

	assert(remove_all("//").is_err(), "remove_all refuses //")
	assert(remove_all(".").is_err(), "remove_all refuses .")
	assert(remove_all("..").is_err(), "remove_all refuses ..")
	assert(remove_all("foo/.").is_err(), "remove_all refuses foo/.")
	assert(remove_all("foo/..").is_err(), "remove_all refuses foo/..")
	assert(remove_all("foo/./").is_err(), "remove_all refuses foo/./")

	let target_dir = _test_tmp_path("os_sym_target")
	let link_dir = _test_tmp_path("os_sym_link")
	let _ = remove_all(target_dir)
	let _ = remove_all(link_dir)
	mkdir_p(target_dir): default panic
	let target_file = "$target_dir/preserve.txt"
	assert(core_write_file(target_file, "keepme"), "create target file")
	assert(_test_symlink(target_dir, link_dir), "create symlink to directory")
	// Removing with trailing slash must unlink the symlink itself and leave target_dir untouched.
	remove_all("${link_dir}/"): default panic
	assert(core_read_file(target_file) == "keepme", "symlink target contents preserved")
	remove_all(target_dir): default panic

	let locked = _test_tmp_path("os_locked_dir")
	let _ = _test_chmod(locked, 493)
	let _ = remove_all(locked)
	mkdir_p(locked): default panic
	assert(_test_chmod(locked, 0), "chmod 000 succeeds")
	assert(mkdir_p(locked).is_ok(), "mkdir_p on existing chmod 000 dir succeeds")
	let child_res! = mkdir_p("${locked}/child")
	assert(child_res!.is_err(), "mkdir_p under chmod 000 dir fails")
	let child_msg = child_res!.peek_err()?.message() ?? ""
	assert(
		not child_msg.contains("is not a directory"),
		"mkdir_p under chmod 000 dir should not claim it is not a directory: $child_msg",
	)
	assert(_test_chmod(locked, 493), "restore permissions on locked dir")
	remove_all(locked): default panic
}
