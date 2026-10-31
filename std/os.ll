// Lowlight Standard Library: std/os.ll
// Process execution, file metadata, system environment, and OS introspection.

import "std/fs.ll"

extern ll_os_popen: (cmd: ptr[u8]): ptr[u8]
extern ll_os_pclose: (stream: ptr[u8]): int
extern ll_os_pread: (stream: ptr[u8], buf: ptr[u8], max_len: int): int
extern ll_os_system: (cmd: ptr[u8]): int
extern ll_os_file_size: (path: ptr[u8]): int
extern ll_os_file_exists: (path: ptr[u8]): bool
extern ll_os_mkdir: (path: ptr[u8]): int
extern ll_os_num_cpus: (): int
extern ll_os_name: (): string
extern ll_os_arch: (): string
extern ll_str_from_raw: (data: ptr[u8], len: int): string
extern ll_fmt_float_prec: (v: float, prec: int): string

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
    let cmd = "mkdir -p \"$path\""
    (unsafe ll_os_system(cmd.c_str())) == 0
}

bind remove_all = (path: string): bool {
    let cmd = "rm -rf \"$path\""
    (unsafe ll_os_system(cmd.c_str())) == 0
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

bind format_float = (v: float, precision: int): string {
    unsafe ll_fmt_float_prec(v, precision)
}
