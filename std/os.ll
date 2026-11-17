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

// Recursively creates `path` and any missing parent directories, returning `true` if `path` exists afterward.
bind mkdir_p = (path: string) bool: {
	if(path.len() == 0): func <- false
	let n = path.len()
	let i = 1
	for(i < n): {
		if(path.byte_at(i) == u8(47)): {
			let sub = path[0..i]
			if(not file_exists(sub)): {
				let _ = mkdir(sub)
			}
		}
		i += 1
	}
	if(file_exists(path)): func <- true
	mkdir(path)
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
	let base = "/tmp/lowlight_test_os_dir"
	let nested = "/tmp/lowlight_test_os_dir/sub1/sub2"

	let _ = remove_all(base)
	assert(not file_exists(nested), "nested does not exist before mkdir_p")

	assert(mkdir_p(nested), "mkdir_p succeeds")
	assert(file_exists(nested), "nested exists after mkdir_p")

	assert(remove_all(base), "remove_all succeeds")
	assert(not file_exists(base), "base does not exist after remove_all")
}
