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
		"json.ll",
	}

	for _, pkg := range stdPackages {
		pkg := pkg
		for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
			backend := backend
			t.Run(string(backend)+"/"+pkg, func(t *testing.T) {
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
	src := `
import "std/fs"
import "std/os"

bind main = (): {
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
	src := `
import "std/math"

bind main = (): {
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

bind main = (): {
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

    assert(not os.mkdir_p(reg), "mkdir_p false on regular file")
    assert(not os.mkdir_p("${reg}/"), "mkdir_p false on regular file with trailing slash")
    assert(not os.mkdir_p("${reg}/sub/dir"), "mkdir_p false through regular file parent")
    assert(os.mkdir_p(existing_dir), "mkdir_p true on existing directory")
    assert(os.mkdir_p(missing_dir), "mkdir_p creates nested dirs")
    assert(fs.exists(missing_dir), "exists true on created dir")
    assert(os.remove_all("${tmp}/nested"), "cleanup nested")

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
bind main = (): {
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
