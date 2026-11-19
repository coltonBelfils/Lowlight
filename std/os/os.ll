// std/os — Process execution, filesystem queries, and host platform introspection.

// Captured output and exit status returned by [`exec`].
define ExecResult struct [
	// Captured standard output produced by the command.
	stdout: string,
	// Process exit code (`0` on success, `-1` if launching the process failed).
	exit_code: int,
]

// Reports whether the command exited cleanly with status `0`.
bind ExecResult.ok = (self) bool: self.exit_code == 0

// Executes shell command `cmd` via a pipe, capturing stdout and returning an [`ExecResult`].
//
// - `cmd`: Shell command line string executed via `popen`.
bind exec = (cmd: string) ExecResult:
	unsafe {
		let stream = sys_popen(cmd.c_str())
		if(stream == null_ptr[u8]()): func <- ExecResult[stdout: "", exit_code: -1]
		let chunk_size = 4096
		let raw = alloc_raw[u8](chunk_size)
		let out_str = ""
		let n = sys_pread(stream, raw, chunk_size)
		for(n > 0): {
			let chunk = str_from_raw(raw, n)
			out_str.push(chunk)
			n = sys_pread(stream, raw, chunk_size)
		}
		free_raw(raw)
		let code = sys_pclose(stream)
		ExecResult[stdout: own out_str, exit_code: code]
	}

// Executes `cmd` through the host shell (`system`) and returns its integer exit code.
bind system_run = (cmd: string) int: unsafe sys_system(cmd.c_str())

// Reports whether a file or directory exists at `path`.
bind file_exists = (path: string) bool: unsafe sys_file_exists(path.c_str())

// Creates a single directory at `path`, returning `true` on success.
bind mkdir = (path: string) bool: unsafe sys_mkdir(path.c_str()) == 0

bind _is_dir = (path: string) bool:
	if(path.len() == 0): false
	else if(path == "/"): file_exists("/")
	else: file_exists("${path}/.")

// Recursively creates `path` and any missing parent directories, returning `true` if `path` exists as a directory afterward.
bind mkdir_p = (path: string) bool: {
	if(path.len() == 0): func <- false
	let target = path.copy()
	for(target.len() > 1 and target.byte_at(target.len() - 1) == u8(47)):
		target = target[0..target.len() - 1]
	if(_is_dir(target)): func <- true
	let n = target.len()
	let i = 1
	for(i < n): {
		if(target.byte_at(i) == u8(47)): {
			let sub = target[0..i]
			if(not _is_dir(sub)): {
				if(file_exists(sub) or not mkdir(sub)): {
					if(not _is_dir(sub)): func <- false
				}
			}
		}
		i += 1
	}
	if(_is_dir(target)): func <- true
	if(file_exists(target)): func <- false
	if(mkdir(target)): func <- true
	_is_dir(target)
}

// Recursively removes `path` and any nested contents, returning `true` on success.
bind remove_all = (path: string) bool: unsafe sys_remove_all(path.c_str()) == 0

// Returns the number of logical CPU cores available on the host system.
bind num_cpus = () int: unsafe sys_num_cpus()

// Returns the host operating system name string (for example `"darwin"` or `"linux"`).
bind os_name = () string: str_from_cstr(unsafe sys_os_name())

// Returns the host CPU architecture identifier string (for example `"arm64"` or `"x86_64"`).
bind arch = () string: str_from_cstr(unsafe sys_os_arch())

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test_only
extern getenv as _test_getenv: (name: ptr[u8]) ptr[u8]

@test_only
bind _test_tmp_path = (name: string) string: {
	let base = str_from_cstr(unsafe _test_getenv("TMPDIR".c_str()))
	if(base.len() == 0): base = "/tmp"
	if(base.len() > 1 and base.byte_at(base.len() - 1) == u8(47)):
		base = base[0..base.len() - 1]
	let mono = unsafe sys_monotonic_nanos()
	let epoch = unsafe sys_epoch_nanos_now()
	"${base}/lowlight_test_${name}_${mono}_${epoch}"
}

@test("os system metadata: num_cpus, os_name, arch, and args")
bind test_os_metadata = (): {
	assert(num_cpus() >= 1, "num_cpus >= 1")
	let os = os_name()
	assert(os.len() > 0, "os_name non-empty")
	let a = arch()
	assert(a.len() > 0, "arch non-empty")

	let cmd_args = args()
	assert(cmd_args.len() >= 1, "args contains at least argv[0]")
}

@test("os process execution: exec and ExitResult")
bind test_os_exec = (): {
	let res = exec("echo hello_os_test")
	assert(res.ok(), "exec ok")
	assert(res.exit_code == 0, "exit code 0")
	assert(res.stdout.contains("hello_os_test"), "stdout contains expected string")
}

@test("os directory operations: mkdir_p, file_exists, remove_all")
bind test_os_filesystem = (): {
	let base = _test_tmp_path("os_dir")
	let nested = "${base}/sub1/sub2"

	let _ = remove_all(base)
	assert(not file_exists(nested), "nested does not exist before mkdir_p")

	assert(mkdir_p(nested), "mkdir_p succeeds")
	assert(file_exists(nested), "nested exists after mkdir_p")
	assert(mkdir_p(nested), "mkdir_p on existing dir is idempotent")
	assert(mkdir_p("${base}/trailing/"), "mkdir_p handles trailing slash")
	assert(not mkdir_p(""), "mkdir_p rejects empty path")

	let reg = "${base}/reg_file.txt"
	unsafe {
		let fp = sys_fopen(reg.c_str(), "wb".c_str())
		assert(fp != null_ptr[u8](), "create regular file succeeds")
		let _ = sys_fclose(fp)
	}
	assert(file_exists(reg), "regular file exists")
	assert(not mkdir_p(reg), "mkdir_p fails when target is regular file")
	assert(not mkdir_p("${reg}/"), "mkdir_p fails when target is regular file with trailing slash")
	assert(not mkdir_p("${reg}/child/dir"), "mkdir_p fails when ancestor is regular file")

	assert(remove_all(base), "remove_all succeeds")
	assert(not file_exists(base), "base does not exist after remove_all")
}
