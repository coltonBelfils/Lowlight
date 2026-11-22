package site

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lowlight/internal/driver"
)

// RunExample compiles and runs one example program and returns its standard output.
func RunExample(root string, ex *Example) (string, error) {
	var stdout, stderr bytes.Buffer
	rc, err := driver.Run(driver.Options{EntryPath: ex.Path, ProjectRoot: root}, nil, &stdout, &stderr)
	if err != nil {
		return "", fmt.Errorf("%s: %v\n%s", filepath.Base(ex.Path), err, stderr.String())
	}
	if rc != 0 && !strings.Contains(ex.Source, "expect-panic") {
		return "", fmt.Errorf("%s exited with status %d\n%s", filepath.Base(ex.Path), rc, stderr.String())
	}
	return stdout.String(), nil
}

// UpdateExampleOutputs runs every example and rewrites its .out file.
func UpdateExampleOutputs(root string) (int, error) {
	exs, err := LoadExamples(filepath.Join(root, "website", "examples"))
	if err != nil {
		return 0, err
	}
	for _, ex := range exs {
		out, err := RunExample(root, ex)
		if err != nil {
			return 0, err
		}
		if err := os.WriteFile(strings.TrimSuffix(ex.Path, ".ll")+".out", []byte(out), 0o644); err != nil {
			return 0, err
		}
	}
	return len(exs), nil
}
