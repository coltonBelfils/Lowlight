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
