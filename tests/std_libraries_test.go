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
		"core.ll",
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
	}

	for _, file := range stdFiles {
		file := file
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(stdDir, file)
			opts := driver.Options{
				EntryPath:   path,
				ProjectRoot: root,
				TestMode:    true,
			}

			var stdout, stderr bytes.Buffer
			code, err := driver.Test(opts, &stdout, &stderr)
			out := stdout.String() + stderr.String()

			if err != nil {
				t.Fatalf("driver.Test error on %s: %v\nOutput:\n%s", file, err, out)
			}
			if code != 0 {
				t.Fatalf("driver.Test exit code %d on %s\nOutput:\n%s", code, file, out)
			}
			if !strings.Contains(out, "0 failed") {
				t.Fatalf("expected '0 failed' in %s output:\n%s", file, out)
			}
			if strings.Contains(out, "FAIL") {
				t.Fatalf("detected unexpected test failure in %s:\n%s", file, out)
			}
			if strings.Contains(out, "leak") && !strings.Contains(out, "0 leaks") {
				t.Fatalf("detected memory leak in %s:\n%s", file, out)
			}
		})
	}
}
