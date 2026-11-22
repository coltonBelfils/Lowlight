package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/manifest"
)

func TestMultiFileTestFailureReturnsNonZero(t *testing.T) {
	tmpDir := t.TempDir()
	badTest := filepath.Join(tmpDir, "bad_test.ll")
	err := os.WriteFile(badTest, []byte("@test(\"broken\")\nbind test_bad = fn(): {\n\tlet x: int = \"not_an_int\"\n}\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code, err := Test(Options{Targets: []string{tmpDir}}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code == 0 {
		t.Fatalf("expected non-zero exit code for failing multi-file test, got 0 (stdout=%q, stderr=%q)", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "error testing") {
		t.Fatalf("expected compile error message on stderr, got %q", stderr.String())
	}
}

func TestTestFilterRunsMatchingTestOnly(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "filter_test.ll")
	src := `@test("alpha test")
bind test_alpha = fn(): {
	assert(true, "alpha")
}

@test("beta test")
bind test_beta = fn(): {
	assert(false, "beta should be filtered out")
}
`
	if err := os.WriteFile(testFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code, err := Test(Options{
		Targets:    []string{testFile},
		TestFilter: "alpha",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%q)", err, stderr.String())
	}
	if code != 0 {
		t.Fatalf("expected exit code 0 when beta is filtered out, got %d (stdout=%q, stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "alpha test") || strings.Contains(stdout.String(), "beta test") {
		t.Fatalf("unexpected filtered output:\n%s", stdout.String())
	}
}

func TestEmitAndBuildForwardWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "warn.ll")
	src := `@must_use
bind get_val = fn() int: { 42 }

bind main = fn() void: {
	get_val()
}
`
	if err := os.WriteFile(srcFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var warnC bytes.Buffer
	if _, _, err := EmitC(Options{EntryPath: srcFile, WarnWriter: &warnC}); err != nil {
		t.Fatalf("EmitC failed: %v", err)
	}
	if !strings.Contains(warnC.String(), "warning:") {
		t.Fatalf("expected warning from EmitC, got %q", warnC.String())
	}

	var warnLLVM bytes.Buffer
	if _, _, err := EmitLLVM(Options{EntryPath: srcFile, WarnWriter: &warnLLVM}); err != nil {
		t.Fatalf("EmitLLVM failed: %v", err)
	}
	if !strings.Contains(warnLLVM.String(), "warning:") {
		t.Fatalf("expected warning from EmitLLVM, got %q", warnLLVM.String())
	}

	var warnBuildLLVM bytes.Buffer
	if _, err := Build(Options{
		EntryPath:  srcFile,
		Backend:    BackendLLVM,
		OutPath:    filepath.Join(tmpDir, "warn_llvm"),
		WarnWriter: &warnBuildLLVM,
	}); err != nil {
		t.Fatalf("Build LLVM failed: %v", err)
	}
	if !strings.Contains(warnBuildLLVM.String(), "warning:") {
		t.Fatalf("expected warning from Build LLVM, got %q", warnBuildLLVM.String())
	}

	var warnBuildC bytes.Buffer
	if _, err := Build(Options{
		EntryPath:  srcFile,
		Backend:    BackendC11,
		OutPath:    filepath.Join(tmpDir, "warn_c"),
		WarnWriter: &warnBuildC,
	}); err != nil {
		t.Fatalf("Build C11 failed: %v", err)
	}
	if !strings.Contains(warnBuildC.String(), "warning:") {
		t.Fatalf("expected warning from Build C11, got %q", warnBuildC.String())
	}
}

func TestDiscoverTestablePackageDirsSkipsExcludedDirsRecursively(t *testing.T) {
	tmpDir := t.TempDir()
	keep := filepath.Join(tmpDir, "src", "main_test.ll")
	skipEditors := filepath.Join(tmpDir, "sub", "editors", "bundled_test.ll")
	skipNode := filepath.Join(tmpDir, "node_modules", "pkg_test.ll")
	skipVendor := filepath.Join(tmpDir, "sub", "vendor", "dep_test.ll")
	for _, p := range []string{keep, skipEditors, skipNode, skipVendor} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("@test(\"ok\")\nbind t = fn(): {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dirs, err := discoverTestablePackageDirs(tmpDir, manifest.ParseTarget(""))
	if err != nil {
		t.Fatalf("discoverTestablePackageDirs failed: %v", err)
	}
	wantDir := filepath.Dir(keep)
	if len(dirs) != 1 || dirs[0] != wantDir {
		t.Fatalf("expected only [%q], got %v", wantDir, dirs)
	}
}
