package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"lowlight/internal/driver"
)

func TestStandardLibraryInlineSuites(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	stdDir := filepath.Join(root, "std")

	stdPackages := []string{
		"test",
		"chars",
		"core",
		"strings",
		"fs",
		"sort",
		"strconv",
		"strfmt",
		"math",
		"time",
		"os",
		"pool",
		"net",
		"http",
		"unicode",
		"collections",
		"json",
	}

	for _, pkg := range stdPackages {
		pkg := pkg
		for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
			backend := backend
			t.Run(string(backend)+"/"+pkg, func(t *testing.T) {
				t.Parallel()
				tmpDir := t.TempDir()
				path := filepath.Join(stdDir, pkg)
				opts := driver.Options{
					EntryPath:   path,
					ProjectRoot: root,
					Backend:     backend,
					TestMode:    true,
					Env:         []string{"TMPDIR=" + tmpDir},
				}

				var stdout, stderr bytes.Buffer
				code, err := driver.Test(opts, &stdout, &stderr)
				out := stdout.String() + stderr.String()

				if err != nil {
					t.Fatalf("driver.Test error on %s (%s): %v\nOutput:\n%s", pkg, backend, err, out)
				}
				if code != 0 {
					t.Fatalf("driver.Test exit code %d on %s (%s)\nOutput:\n%s", code, pkg, backend, out)
				}
				if !strings.Contains(out, "0 failed") {
					t.Fatalf("expected '0 failed' in %s (%s) output:\n%s", pkg, backend, out)
				}
				if strings.Contains(out, "FAIL") {
					t.Fatalf("detected unexpected test failure in %s (%s):\n%s", pkg, backend, out)
				}
				if strings.Contains(out, "leak") && !strings.Contains(out, "0 leaks") {
					t.Fatalf("detected memory leak in %s (%s):\n%s", pkg, backend, out)
				}
				entries, readErr := os.ReadDir(tmpDir)
				if readErr != nil {
					t.Fatalf("failed to read TMPDIR %s: %v", tmpDir, readErr)
				}
				if len(entries) != 0 {
					t.Fatalf("expected %s (%s) to clean up TMPDIR %s, found %d leftover entry(ies)", pkg, backend, tmpDir, len(entries))
				}
			})
		}
	}
}

func TestStdFsAndOsQualifiedImportNoConflict(t *testing.T) {
	t.Parallel()
	src := `
import "std/fs"
import "std/os"

bind main = fn(): {
    let p = "${os.os_name()}:${os.arch()}"
    assert(p.len() > 1, "os metadata present")
    assert(not fs.exists("/nonexistent_lowlight_check_path_xyz"), "exists false")
    println("qualified_import_ok")
}
`
	out := runInlineStrict(t, "std_fs_os_qualified_import", src, false)
	if strings.TrimSpace(out) != "qualified_import_ok" {
		t.Fatalf("unexpected stdout: %q", out)
	}
}

func TestStdMathNanInfStrict(t *testing.T) {
	t.Parallel()
	src := `
import "std/math"

bind main = fn(): {
    assert(math.is_inf(math.INF) and math.is_inf(-math.INF), "inf check")
    assert(math.is_nan(math.NAN) and not math.is_inf(math.NAN), "nan check")
    assert(not math.is_nan(math.INF) and not math.is_nan(0.0), "finite/inf not nan")
    println("math_nan_inf_ok")
}
`
	for _, release := range []bool{false, true} {
		out := runInlineStrict(t, "std_math_nan_inf", src, release)
		if strings.TrimSpace(out) != "math_nan_inf_ok" {
			t.Fatalf("[release=%v] unexpected stdout: %q", release, out)
		}
	}
}

func TestStdFsExistsAndMkdirPEdgeCases(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	unreadable := filepath.Join(tmp, "unreadable.txt")
	if err := os.WriteFile(unreadable, []byte("secret"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })

	regFile := filepath.Join(tmp, "regular.txt")
	if err := os.WriteFile(regFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	existingDir := filepath.Join(tmp, "existing_dir")
	if err := os.Mkdir(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	missingDir := filepath.Join(tmp, "nested", "a", "b")

	src := `
import "std/fs"
import "std/os"

bind main = fn(): {
    let tmp = ` + "`" + tmp + "`" + `
    let unreadable = ` + "`" + unreadable + "`" + `
    let reg = ` + "`" + regFile + "`" + `
    let existing_dir = ` + "`" + existingDir + "`" + `
    let missing_dir = ` + "`" + missingDir + "`" + `

    assert(fs.exists(tmp), "exists true on directory")
    assert(fs.exists(reg), "exists true on regular file")
    assert(fs.exists(unreadable), "exists true on unreadable file")
    assert(not fs.exists(""), "exists false on empty string")
    assert(not fs.exists(missing_dir), "exists false before mkdir_p")

    assert(os.mkdir_p(reg).is_err(), "mkdir_p fails on regular file")
    assert(os.mkdir_p("${reg}/").is_err(), "mkdir_p fails on regular file with trailing slash")
    assert(os.mkdir_p("${reg}/sub/dir").is_err(), "mkdir_p fails through regular file parent")
    assert(os.mkdir_p(existing_dir).is_ok(), "mkdir_p succeeds on existing directory")
    assert(os.mkdir_p(missing_dir).is_ok(), "mkdir_p creates nested dirs")
    assert(fs.exists(missing_dir), "exists true on created dir")
    assert(os.remove_all("${tmp}/nested").is_ok(), "cleanup nested")

    println("fs_os_edge_ok")
}
`
	for _, release := range []bool{false, true} {
		out := runInlineStrict(t, "std_fs_exists_mkdir_p", src, release)
		if strings.TrimSpace(out) != "fs_os_edge_ok" {
			t.Fatalf("[release=%v] unexpected stdout: %q", release, out)
		}
	}
}

func TestCoreReadFileNonSeekable(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX FIFO test skipped on Windows")
	}
	root := projectRoot(t)
	tmp := t.TempDir()

	emptyFile := filepath.Join(tmp, "empty.txt")
	if err := os.WriteFile(emptyFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	binFile := filepath.Join(tmp, "binary.bin")
	if err := os.WriteFile(binFile, []byte("ab\x00cd\x00ef"), 0o644); err != nil {
		t.Fatal(err)
	}
	missingFile := filepath.Join(tmp, "does_not_exist.txt")

	payload := make([]byte, 20000)
	for i := range payload {
		if i < 5000 {
			payload[i] = 'A'
		} else if i < 14000 {
			payload[i] = 'B'
		} else {
			payload[i] = 'C'
		}
	}

	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		for _, release := range []bool{false, true} {
			name := fmt.Sprintf("%s_release_%v", backend, release)
			t.Run(name, func(t *testing.T) {
				subTmp := t.TempDir()
				fifoPath := filepath.Join(subTmp, "stream.fifo")
				if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
					t.Fatalf("mkfifo failed: %v", err)
				}

				srcPath := filepath.Join(subTmp, "read_stream.ll")
				src := fmt.Sprintf(`
bind main = fn(): {
	let fifo_res? = core_read_file(%q)
	if(fifo_res? as s): {
		assert(s.len() == 20000, "fifo length")
		for(i in 0..20000): {
			let expected = if(i < 5000): u8(65) else if(i < 14000): u8(66) else: u8(67)
			assert(s.byte_at(i) == expected, "fifo byte mismatch")
		}
	} else: assert(false, "fifo read failed")

	let devnull_res? = core_read_file("/dev/null")
	assert(devnull_res? == "", "dev null empty")

	let dir_res? = core_read_file(%q)
	assert(dir_res? == none, "directory returns none")

	let missing_res? = core_read_file(%q)
	assert(missing_res? == none, "missing file returns none")

	let empty_res? = core_read_file(%q)
	assert(empty_res? == "", "empty file")

	let bin_res? = core_read_file(%q)
	if(bin_res? as b): {
		assert(b.len() == 8, "binary file length")
		assert(b.byte_at(0) == u8(97) and b.byte_at(2) == u8(0) and b.byte_at(5) == u8(0) and b.byte_at(7) == u8(102), "binary file bytes")
	} else: assert(false, "binary file read failed")

	println("core_read_file_ok")
}
`, fifoPath, tmp, missingFile, emptyFile, binFile)
				if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}

				outBin := filepath.Join(subTmp, "read_stream_bin")
				if _, err := driver.Build(driver.Options{
					EntryPath:   srcPath,
					ProjectRoot: root,
					Backend:     backend,
					Release:     release,
					OutPath:     outBin,
				}); err != nil {
					t.Fatalf("driver.Build failed (%s, release=%v): %v", backend, release, err)
				}

				writeErrCh := make(chan error, 1)
				go func() {
					f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
					if err != nil {
						writeErrCh <- err
						return
					}
					chunks := [][]byte{
						payload[:5000],
						payload[5000:14000],
						payload[14000:],
					}
					for _, c := range chunks {
						if _, err := f.Write(c); err != nil {
							_ = f.Close()
							writeErrCh <- err
							return
						}
					}
					writeErrCh <- f.Close()
				}()

				cmd := exec.Command(outBin)
				cmd.Env = append(os.Environ(), "LOWLIGHT_CHECK_LEAKS=1", "ASAN_OPTIONS=detect_leaks=0")
				var outBuf bytes.Buffer
				cmd.Stdout = &outBuf
				cmd.Stderr = &outBuf
				runErr := cmd.Run()
				if werr := <-writeErrCh; werr != nil {
					t.Fatalf("fifo writer failed: %v (cmd err=%v, output=%q)", werr, runErr, outBuf.String())
				}
				if runErr != nil {
					t.Fatalf("binary execution failed: %v\noutput:\n%s", runErr, outBuf.String())
				}
				if strings.TrimSpace(outBuf.String()) != "core_read_file_ok" {
					t.Fatalf("unexpected stdout: %q", outBuf.String())
				}
			})
		}
	}
}

func extractCFunctionBody(cSrc, fnName string) string {
	marker := " " + fnName + "("
	pos := 0
	for {
		idx := strings.Index(cSrc[pos:], marker)
		if idx == -1 {
			return ""
		}
		start := pos + idx
		rest := cSrc[start:]
		braceStart := strings.IndexByte(rest, '{')
		semi := strings.IndexByte(rest, ';')
		if braceStart != -1 && (semi == -1 || braceStart < semi) {
			depth := 0
			for i := braceStart; i < len(rest); i++ {
				switch rest[i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						return rest[:i+1]
					}
				}
			}
			return ""
		}
		pos = start + len(marker)
	}
}

func extractLLVMFunctionBody(llSrc, fnName string) string {
	marker := "@" + fnName + "("
	pos := 0
	for {
		idx := strings.Index(llSrc[pos:], marker)
		if idx == -1 {
			return ""
		}
		abs := pos + idx
		lineStart := strings.LastIndexByte(llSrc[:abs], '\n') + 1
		if strings.HasPrefix(llSrc[lineStart:], "define ") {
			rest := llSrc[lineStart:]
			endIdx := strings.Index(rest, "\n}")
			if endIdx == -1 {
				return rest
			}
			return rest[:endIdx+2]
		}
		pos = abs + len(marker)
	}
}

func TestListFilledCodegenAndBoundsPanics(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tmp := t.TempDir()

	codegenSrcPath := filepath.Join(tmp, "list_filled_codegen.ll")
	codegenSrc := `
bind main = fn(): {
	let bu = List[u8].filled(16, u8(7))
	let bb = List[bool].filled(16, true)
	let bi = List[i8].filled(16, i8(-3))
	let bn = List[int].filled(16, 42)
	bu.resize(24, u8(9))
	bb.resize(24, false)
	bi.resize(24, i8(5))
	bn.resize(24, 99)
}
`
	if err := os.WriteFile(codegenSrcPath, []byte(codegenSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cSrc, checkedProg, err := driver.EmitC(driver.Options{
		EntryPath:   codegenSrcPath,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("EmitC failed: %v", err)
	}
	llSrc, _, err := driver.EmitLLVM(driver.Options{
		EntryPath:   codegenSrcPath,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("EmitLLVM failed: %v", err)
	}

	findCName := func(wantName string) string {
		for _, fi := range checkedProg.Funcs {
			if fi.Name == wantName {
				return fi.CName
			}
		}
		return ""
	}

	// Verify 1-byte Plain vs multi-byte Plain codegen paths in both backends:
	// - `filled` uses `ll_alloc_uninit` (not `ll_alloc`) and avoids `_push__` / `_reserve__`
	// - `filled` & `resize` guard the `ll_fill_bytes` memset fast path with `sizeof(T) == 1` (`icmp eq i64 <byteSize>, 1` in LLVM IR)
	for _, tc := range []struct {
		suffix   string
		cType    string
		byteSize int
	}{
		{"u8", "uint8_t", 1},
		{"bool", "bool", 1},
		{"i8", "int8_t", 1},
		{"int", "int64_t", 8},
	} {
		filledCName := findCName("List[" + tc.suffix + "].filled")
		if filledCName == "" {
			t.Fatalf("missing checked function for List[%s].filled", tc.suffix)
		}
		cFilled := extractCFunctionBody(cSrc, filledCName)
		if cFilled == "" {
			t.Fatalf("missing C function %s for List[%s].filled", filledCName, tc.suffix)
		}
		if !strings.Contains(cFilled, "ll_alloc_uninit") || strings.Contains(cFilled, "ll_alloc(") {
			t.Fatalf("C List[%s].filled should use ll_alloc_uninit and not ll_alloc:\n%s", tc.suffix, cFilled)
		}
		if !strings.Contains(cFilled, "ll_fill_bytes") || !strings.Contains(cFilled, "sizeof("+tc.cType+")") {
			t.Fatalf("C List[%s].filled missing ll_fill_bytes or sizeof(%s):\n%s", tc.suffix, tc.cType, cFilled)
		}
		if strings.Contains(cFilled, "_push__") || strings.Contains(cFilled, "_reserve__") {
			t.Fatalf("C List[%s].filled should avoid push/reserve calls:\n%s", tc.suffix, cFilled)
		}

		llFilled := extractLLVMFunctionBody(llSrc, filledCName)
		if llFilled == "" {
			t.Fatalf("missing LLVM function %s for List[%s].filled", filledCName, tc.suffix)
		}
		if !strings.Contains(llFilled, "@ll_alloc_uninit") || strings.Contains(llFilled, "@ll_alloc(") {
			t.Fatalf("LLVM List[%s].filled should use @ll_alloc_uninit and not @ll_alloc:\n%s", tc.suffix, llFilled)
		}
		sizeCmp := fmt.Sprintf("icmp eq i64 %d, 1", tc.byteSize)
		if !strings.Contains(llFilled, "@ll_fill_bytes") || !strings.Contains(llFilled, sizeCmp) {
			t.Fatalf("LLVM List[%s].filled missing @ll_fill_bytes or %q:\n%s", tc.suffix, sizeCmp, llFilled)
		}
		if strings.Contains(llFilled, "_push__") || strings.Contains(llFilled, "_reserve__") {
			t.Fatalf("LLVM List[%s].filled should avoid push/reserve calls:\n%s", tc.suffix, llFilled)
		}

		resizeCName := findCName("List[" + tc.suffix + "].resize")
		if resizeCName == "" {
			t.Fatalf("missing checked function for List[%s].resize", tc.suffix)
		}
		cResize := extractCFunctionBody(cSrc, resizeCName)
		if cResize == "" {
			t.Fatalf("missing C function %s for List[%s].resize", resizeCName, tc.suffix)
		}
		if !strings.Contains(cResize, "ll_fill_bytes") || !strings.Contains(cResize, "sizeof("+tc.cType+")") || strings.Contains(cResize, "_push__") {
			t.Fatalf("C List[%s].resize unexpected body:\n%s", tc.suffix, cResize)
		}

		llResize := extractLLVMFunctionBody(llSrc, resizeCName)
		if llResize == "" {
			t.Fatalf("missing LLVM function %s for List[%s].resize", resizeCName, tc.suffix)
		}
		if !strings.Contains(llResize, "@ll_fill_bytes") || !strings.Contains(llResize, sizeCmp) || strings.Contains(llResize, "_push__") {
			t.Fatalf("LLVM List[%s].resize unexpected body:\n%s", tc.suffix, llResize)
		}
	}

	// Verify out-of-bounds panics across LLVM and C11 for insert, remove, and swap_remove.
	panicCases := []struct {
		name      string
		callStmt  string
		wantPanic string
	}{
		{
			name:      "insert_neg",
			callStmt:  `xs.must_insert(-1, 99)`,
			wantPanic: "List.must_insert: index -1 out of bounds for length 2",
		},
		{
			name:      "insert_oob",
			callStmt:  `xs.must_insert(3, 99)`,
			wantPanic: "List.must_insert: index 3 out of bounds for length 2",
		},
		{
			name:      "remove_oob",
			callStmt:  `let _ = xs.must_remove(2)`,
			wantPanic: "List.must_remove: index 2 out of bounds for length 2",
		},
		{
			name:      "swap_remove_neg",
			callStmt:  `let _ = xs.must_swap_remove(-1)`,
			wantPanic: "List.must_swap_remove: index -1 out of bounds for length 2",
		},
	}

	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		for _, pc := range panicCases {
			t.Run(fmt.Sprintf("%s/%s", backend, pc.name), func(t *testing.T) {
				subTmp := t.TempDir()
				srcFile := filepath.Join(subTmp, "panic_case.ll")
				src := fmt.Sprintf("bind main = fn(): {\n\tlet xs: List[int] = [10, 20]\n\t%s\n}\n", pc.callStmt)
				if err := os.WriteFile(srcFile, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
				outBin := filepath.Join(subTmp, "panic_bin")
				if _, err := driver.Build(driver.Options{
					EntryPath:   srcFile,
					ProjectRoot: root,
					Backend:     backend,
					OutPath:     outBin,
				}); err != nil {
					t.Fatalf("driver.Build failed (%s): %v", backend, err)
				}
				cmd := exec.Command(outBin)
				var outBuf bytes.Buffer
				cmd.Stdout = &outBuf
				cmd.Stderr = &outBuf
				err := cmd.Run()
				if err == nil {
					t.Fatalf("expected non-zero panic exit for %s (%s), got exit 0", pc.name, backend)
				}
				if !strings.Contains(outBuf.String(), pc.wantPanic) {
					t.Fatalf("expected panic %q, got output:\n%s", pc.wantPanic, outBuf.String())
				}
			})
		}
	}
}
