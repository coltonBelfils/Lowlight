// Lowlight Standard Library: std/os.ll
// Process execution, file metadata, system environment, and OS introspection.

extern ll_os_popen: (cmd: ptr[u8]): ptr[u8]
extern ll_os_pclose: (stream: ptr[u8]): int
extern ll_os_pread: (stream: ptr[u8], buf: ptr[u8], max_len: int): int
extern ll_os_system: (cmd: ptr[u8]): int
extern ll_os_file_size: (path: ptr[u8]): int
extern ll_os_file_exists: (path: ptr[u8]): bool
extern ll_os_mkdir: (path: ptr[u8]): int
extern ll_os_mkdir_p: (path: ptr[u8]): int
extern ll_os_remove_all: (path: ptr[u8]): int
extern ll_os_num_cpus: (): int
extern ll_os_name: (): string
extern ll_os_arch: (): string
extern ll_os_argc: (): int
extern ll_os_argv: (idx: int): string
extern ll_str_from_raw: (data: ptr[u8], len: int): string

define ExecResult struct [
    stdout: string,
    exit_code: int,
]

bind ExecResult.ok = (self): bool {
    self.exit_code == 0
}

bind exec = (cmd: string): ExecResult {
    unsafe {
        let stream = ll_os_popen(cmd.c_str())
        if (stream == null_ptr[u8]()) {
            func <- ExecResult[stdout: "", exit_code: -1]
        }
        let chunk_size = 4096
        let raw = alloc_raw[u8](chunk_size)
        let out_str = ""
        let n = ll_os_pread(stream, raw, chunk_size)
        for (n > 0) {
            let chunk = ll_str_from_raw(raw, n)
            out_str.push(chunk)
            n = ll_os_pread(stream, raw, chunk_size)
        }
        free_raw(raw)
        let code = ll_os_pclose(stream)
        ExecResult[stdout: own out_str, exit_code: code]
    }
}

bind system_run = (cmd: string): int {
    unsafe ll_os_system(cmd.c_str())
}

bind file_size = (path: string): int {
    unsafe ll_os_file_size(path.c_str())
}

bind file_exists = (path: string): bool {
    unsafe ll_os_file_exists(path.c_str())
}

bind mkdir = (path: string): bool {
    (unsafe ll_os_mkdir(path.c_str())) == 0
}

bind mkdir_p = (path: string): bool {
    (unsafe ll_os_mkdir_p(path.c_str())) == 0
}

bind remove_all = (path: string): bool {
    (unsafe ll_os_remove_all(path.c_str())) == 0
}

bind num_cpus = (): int {
    unsafe ll_os_num_cpus()
}

bind os_name = (): string {
    unsafe ll_os_name()
}

bind arch = (): string {
    unsafe ll_os_arch()
}

// Returns the command-line arguments passed to the program as a List[string].
bind args = (): [string] {
    let count = unsafe ll_os_argc()
    let out: [string] = []
    for (i in 0..count) {
        out.push(unsafe ll_os_argv(i))
    }
    out
}
