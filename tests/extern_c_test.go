package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lowlight/internal/driver"
	"lowlight/internal/format"
)

// A user `extern` of a libc function whose Lowlight signature differs from the system header's
// prototype (`write` takes `const void*` and `size_t` in <unistd.h>) builds under -Werror, and the
// `@test_only` externs in `std/fs` don't leak into a normal build of a program that imports it.
func TestUserExternOfHeaderFunctionBuildsStrict(t *testing.T) {
	t.Parallel()
	src := `import "std/fs"

extern write: fn(fd: i32, buf: ptr[u8], n: int) int
extern getpid: fn() i32

bind main = fn(): {
	let msg = "via write\n"
	let n = unsafe write(i32(1), msg.c_str(), msg.len())
	println("wrote $n ${unsafe getpid() > i32(0)} ${fs.exists("/")}")
}
`
	out := runInlineStrict(t, "user_extern_write", src, false)
	if !strings.Contains(out, "via write\nwrote 10 true true") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// `syscall` makes raw system calls, and reports failure as `-errno` (macOS's carry-flag convention
// is normalized to Linux's). Call numbers are the macOS ones, so this runs only there.
func TestSyscallBuiltin(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("uses macOS system call numbers")
	}
	src := `bind main = fn(): {
	let msg = "raw write\n"
	let n = unsafe syscall(4, 1, int(ptr_addr(msg.c_str())), msg.len())
	let pid = unsafe syscall(20)
	let bad = unsafe syscall(4, -1, 0, 0)
	println("n=$n pid=${pid > 0} bad=$bad")
}
`
	out := runInlineStrict(t, "syscall_builtin", src, false)
	if !strings.Contains(out, "raw write\nn=10 pid=true bad=-9") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// `extern fn` values: a Lowlight function C calls back (top-level and inline), a C function
// pointer from `dlsym` made callable with `fn_from_ptr`, and its address back with `ptr_addr`.
func TestExternFnCallbacksAndPointers(t *testing.T) {
	t.Parallel()
	rtldDefault := "0"
	if runtime.GOOS == "darwin" {
		rtldDefault = "-2"
	}
	src := `extern qsort: fn(base: ptr[u8], n: int, size: int, cmp: extern fn(ptr[u8], ptr[u8]) i32)
extern dlsym: fn(handle: ptr[u8], name: ptr[u8]) ptr[u8]

bind ascending = extern fn(a: ptr[u8], b: ptr[u8]) i32: {
	let x = unsafe ptr_read(ptr_cast[int](a))
	let y = unsafe ptr_read(ptr_cast[int](b))
	i32(x.cmp(y))
}

bind sorted = fn(data: ptr[int], cmp: extern fn(ptr[u8], ptr[u8]) i32) [int]: {
	unsafe qsort(ptr_cast[u8](data), 5, 8, cmp)
	for(i in 0..5) [int]: unsafe ptr_read(ptr_add(data, i))
}

bind main = fn(): {
	let data = unsafe alloc_raw[int](5)
	for(i, v in [5, 3, 9, 1, 7]): unsafe ptr_write(ptr_add(data, i), v)
	println(sorted(data, ascending))
	println(sorted(data, extern fn(a: ptr[u8], b: ptr[u8]) i32: {
		let x = unsafe ptr_read(ptr_cast[int](a))
		let y = unsafe ptr_read(ptr_cast[int](b))
		i32(y.cmp(x))
	}))
	unsafe free_raw(data)
	let sym = unsafe dlsym(ptr_cast[u8](` + rtldDefault + `), "labs".c_str())
	let c_labs = unsafe fn_from_ptr[extern fn(int) int](sym)
	println(unsafe c_labs(-42))
	println(unsafe (ptr_addr(c_labs) == ptr_addr(sym)))
}
`
	out := runInlineStrict(t, "extern_fn", src, false)
	if want := "[1, 3, 5, 7, 9]\n[9, 7, 5, 3, 1]\n42\ntrue"; strings.TrimSpace(out) != want {
		t.Fatalf("unexpected output:\n%s\nwant:\n%s", out, want)
	}
}

// The rules for `extern fn`: it can't capture, calling a value is unsafe, its signature must have
// a C representation, it never becomes a closure, and `fn_from_ptr` needs an `extern fn` type.
func TestExternFnDiagnostics(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, src, want string }{
		{"capture", "bind main = fn(): {\n\tlet k = 2\n\tlet f = extern fn(x: int) int: x * k\n}\n", "an `extern fn` can't capture variables"},
		{"unsafe_call", "bind twice = extern fn(x: int) int: x * 2\nbind main = fn(): {\n\tlet f = twice\n\tprintln(f(3))\n}\n", "calling an `extern fn` value requires `unsafe`"},
		{"c_type", "bind f = extern fn(s: string) int: s.len()\nbind main = fn(): {}\n", "an `extern fn` can't take string"},
		{"to_closure", "bind twice = extern fn(x: int) int: x * 2\nbind main = fn(): {\n\tlet c = cl(twice)\n}\n", "an `extern fn` can't become a closure"},
		{"from_ptr_type", "bind main = fn(): {\n\tlet f = unsafe fn_from_ptr[fn(int) int](null_ptr[u8]())\n}\n", "needs an `extern fn(...)` type"},
	}
	for _, tc := range cases {
		_, _, err := driver.Check(driver.Options{
			EntryPath:   filepath.Join(t.TempDir(), tc.name+".ll"),
			SrcOverride: tc.src,
			ProjectRoot: projectRoot(t),
		})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: expected an error containing %q, got %v", tc.name, tc.want, err)
		}
	}
	// A direct call of a top-level `extern fn` by name runs Lowlight code, so it's safe.
	_, _, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "direct.ll"),
		SrcOverride: "bind twice = extern fn(x: int) int: x * 2\nbind main = fn(): println(twice(3))\n",
		ProjectRoot: projectRoot(t),
	})
	if err != nil {
		t.Errorf("a direct call of a top-level `extern fn` should check: %v", err)
	}
}

// `lowlight fmt` keeps `extern fn` types and literals.
func TestExternFnFormats(t *testing.T) {
	src := "extern qsort: fn(base: ptr[u8], n: int, size: int, cmp: extern fn(ptr[u8], ptr[u8]) i32)\n\nbind cmp = extern fn(a: ptr[u8], b: ptr[u8]) i32: i32(0)\n"
	out, err := format.Format("x.ll", src, format.Options{})
	if err != nil || out != src {
		t.Fatalf("format changed extern fn:\n%s\nerr: %v", out, err)
	}
}

// In a test build, a panic inside an `extern fn` that C is running (here a `qsort` comparator)
// ends the test run, as in a normal build, rather than jumping back to the test runner across the
// C frames. A panic in a direct call of the same function still fails only its own test.
func TestPanicInsideCCallbackStopsTestRun(t *testing.T) {
	t.Parallel()
	src := `extern qsort: fn(base: ptr[u8], n: int, size: int, cmp: extern fn(ptr[u8], ptr[u8]) i32)

bind exploding = extern fn(a: ptr[u8], b: ptr[u8]) i32: {
	panic("boom inside qsort")
}

@test("a panic in a direct call fails just this test")
bind test_a_direct = fn(): {
	let x = 1
	let p = unsafe ptr_cast[u8](addr_of(x))
	let _ = exploding(p, p)
}

@test("a panic inside a C callback stops the run")
bind test_b_callback = fn(): {
	let data = unsafe alloc_raw[int](2)
	unsafe qsort(ptr_cast[u8](data), 2, 8, exploding)
	unsafe free_raw(data)
}
`
	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		dir := t.TempDir()
		entry := filepath.Join(dir, "cb_test.ll")
		if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code, err := driver.Test(driver.Options{EntryPath: entry, ProjectRoot: projectRoot(t), Backend: backend, TestMode: true}, &stdout, &stderr)
		out := stdout.String() + stderr.String()
		if err != nil || code == 0 {
			t.Fatalf("[%s] want a failing run, got code %d, err %v:\n%s", backend, code, err, out)
		}
		for _, want := range []string{
			"--- FAIL: a panic in a direct call fails just this test",
			"=== RUN   a panic inside a C callback stops the run",
			"lowlight panic at cb_test.ll:4:2: boom inside qsort",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("[%s] output is missing %q:\n%s", backend, want, out)
			}
		}
	}
}
