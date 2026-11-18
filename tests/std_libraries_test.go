package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func TestStandardLibraryInlineSuites(t *testing.T) {
	root := projectRoot(t)
	stdDir := filepath.Join(root, "std")

	stdFiles := []string{
		"test.ll",
		"chars.ll",
		"core/core.ll",
		"core/array.ll",
		"strings.ll",
		"fs.ll",
		"sort.ll",
		"strconv.ll",
		"strfmt.ll",
		"math.ll",
		"time.ll",
		"os.ll",
		"pool.ll",
		"net.ll",
		"http.ll",
		"json.ll",
	}

	for _, file := range stdFiles {
		file := file
		for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
			backend := backend
			t.Run(string(backend)+"/"+file, func(t *testing.T) {
				tmpDir := t.TempDir()
				path := filepath.Join(stdDir, file)
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
					t.Fatalf("driver.Test error on %s (%s): %v\nOutput:\n%s", file, backend, err, out)
				}
				if code != 0 {
					t.Fatalf("driver.Test exit code %d on %s (%s)\nOutput:\n%s", code, file, backend, out)
				}
				if !strings.Contains(out, "0 failed") {
					t.Fatalf("expected '0 failed' in %s (%s) output:\n%s", file, backend, out)
				}
				if strings.Contains(out, "FAIL") {
					t.Fatalf("detected unexpected test failure in %s (%s):\n%s", file, backend, out)
				}
				if strings.Contains(out, "leak") && !strings.Contains(out, "0 leaks") {
					t.Fatalf("detected memory leak in %s (%s):\n%s", file, backend, out)
				}
				entries, readErr := os.ReadDir(tmpDir)
				if readErr != nil {
					t.Fatalf("failed to read TMPDIR %s: %v", tmpDir, readErr)
				}
				if len(entries) != 0 {
					t.Fatalf("expected %s (%s) to clean up TMPDIR %s, found %d leftover entry(ies)", file, backend, tmpDir, len(entries))
				}
			})
		}
	}
}

func TestStdFsAndOsOpenImportNoConflict(t *testing.T) {
	src := `
import "std/fs.ll"
import "std/os.ll"

bind main = (): {
    let p = "${os_name()}:${arch()}"
    assert(p.len() > 1, "os metadata present")
    assert(not exists("/nonexistent_lowlight_check_path_xyz"), "exists false")
    println("open_import_ok")
}
`
	out := runInlineStrict(t, "std_fs_os_open_import", src, false)
	if strings.TrimSpace(out) != "open_import_ok" {
		t.Fatalf("unexpected stdout: %q", out)
	}
}

func TestStdMathNanInfStrict(t *testing.T) {
	src := `
import "std/math.ll" as math

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
import "std/fs.ll" as fs
import "std/os.ll" as os

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
