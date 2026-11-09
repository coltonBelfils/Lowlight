package tests

import (
	"bytes"
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
				path := filepath.Join(stdDir, file)
				opts := driver.Options{
					EntryPath:   path,
					ProjectRoot: root,
					Backend:     backend,
					TestMode:    true,
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
			})
		}
	}
}
