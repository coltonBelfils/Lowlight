package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func runLowlightTest(t *testing.T, src string, release bool) (string, int, error) {
	t.Helper()
	tmpDir := t.TempDir()
	entryPath := filepath.Join(tmpDir, "test_file.ll")
	if err := os.WriteFile(entryPath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	opts := driver.Options{
		EntryPath:   entryPath,
		ProjectRoot: projectRoot(t),
		Release:     release,
		TestMode:    true,
	}

	var stdout, stderr bytes.Buffer
	code, err := driver.Test(opts, &stdout, &stderr)
	out := stdout.String() + stderr.String()
	return out, code, err
}

func checkCompile(t *testing.T, src string, testMode bool) error {
	t.Helper()
	tmpDir := t.TempDir()
	entryPath := filepath.Join(tmpDir, "check_file.ll")
	if err := os.WriteFile(entryPath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write check file: %v", err)
	}

	opts := driver.Options{
		EntryPath:   entryPath,
		ProjectRoot: projectRoot(t),
		TestMode:    testMode,
	}
	_, _, err := driver.Check(opts)
	return err
}

func emitC(t *testing.T, src string, testMode bool, release bool) (string, error) {
	t.Helper()
	tmpDir := t.TempDir()
	entryPath := filepath.Join(tmpDir, "emit_file.ll")
	if err := os.WriteFile(entryPath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write emit file: %v", err)
	}

	opts := driver.Options{
		EntryPath:   entryPath,
		ProjectRoot: projectRoot(t),
		Release:     release,
		TestMode:    testMode,
	}
	cSrc, _, err := driver.EmitC(opts)
	return cSrc, err
}

// 1. Tests in the same file as production code
func TestInlineUnitTests(t *testing.T) {
	t.Parallel()
	src := `
bind add = fn(a: int, b: int) int: a + b
bind mul = fn(a: int, b: int) int: a * b
bind main = fn(): {
    println("production main")
}

@test("addition works")
bind test_add = fn(): {
    assert(add(2, 3) == 5)
}

@test
bind test_multiplication = fn(): {
    assert(mul(4, 5) == 20)
}
`
	out, code, err := runLowlightTest(t, src, false)
	if err != nil {
		t.Fatalf("test run failed: %v\nOutput: %s", err, out)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput: %s", code, out)
	}
	if !strings.Contains(out, "=== RUN   addition works") {
		t.Errorf("missing RUN addition works in output:\n%s", out)
	}
	if !strings.Contains(out, "--- PASS: addition works") {
		t.Errorf("missing PASS addition works in output:\n%s", out)
	}
	if !strings.Contains(out, "=== RUN   test_multiplication") {
		t.Errorf("missing RUN test_multiplication in output:\n%s", out)
	}
	if !strings.Contains(out, "--- PASS: test_multiplication") {
		t.Errorf("missing PASS test_multiplication in output:\n%s", out)
	}
	if !strings.Contains(out, "Test results: 2 passed, 0 failed") {
		t.Errorf("missing summary in output:\n%s", out)
	}
	// Verify that production `main` was NOT run during `lowlight test`
	if strings.Contains(out, "production main") {
		t.Errorf("production main was executed during test run:\n%s", out)
	}
}

// 2. Dedicated test file without any `main` function
func TestDedicatedTestFileWithoutMain(t *testing.T) {
	t.Parallel()
	src := `
import "std/test"

@test("testing assertion library")
bind test_assertions = fn(): {
    test.assert_eq(10 + 20, 30)
    test.assert_ne(10, 20)
    test.assert_true(true)
    test.assert_false(false)
    test.assert_gt(50, 10)
    test.assert_lt(10, 50)
    test.assert_ge(50, 50)
    test.assert_le(50, 50)

    let ok_res! = Result[int].value(42)
    test.assert_is_ok(&ok_res!)

    let err_res! = Result[int].fail_msg("boom")
    test.assert_is_err(&err_res!)

    let opt_some?: ?int = 99
    test.assert_some(opt_some?)

    let opt_none?: ?int = none
    test.assert_none(opt_none?)
}
`
	out, code, err := runLowlightTest(t, src, false)
	if err != nil {
		t.Fatalf("test run failed: %v\nOutput: %s", err, out)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput: %s", code, out)
	}
	if !strings.Contains(out, "--- PASS: testing assertion library") {
		t.Errorf("missing PASS in output:\n%s", out)
	}
}

// 3. Compile-time isolation: Non-test function calling test function is rejected
func TestNonTestCallingTestFunctionRejected(t *testing.T) {
	t.Parallel()
	src := `
@test("secret test")
bind my_test = fn(): {
    assert(1 == 1)
}

bind production_func = fn(): {
    my_test()
}

bind main = fn(): {
    production_func()
}
`
	err := checkCompile(t, src, false)
	if err == nil {
		t.Fatalf("expected compile error when non-test function calls test function, but compiled successfully")
	}
	if !strings.Contains(err.Error(), "cannot call test-only function") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// 4. Compile-time isolation: Non-test function calling @test_only helper is rejected
func TestNonTestCallingTestOnlyHelperRejected(t *testing.T) {
	t.Parallel()
	src := `
@test_only
bind setup_database_fixture = fn() int: {
    42
}

bind calculate_metrics = fn() int: {
    setup_database_fixture()
}

bind main = fn(): {
    calculate_metrics()
}
`
	err := checkCompile(t, src, false)
	if err == nil {
		t.Fatalf("expected compile error when non-test function calls @test_only helper, but compiled successfully")
	}
	if !strings.Contains(err.Error(), "cannot call test-only function") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// 5. Compile-time isolation: Non-test function using @test_only struct is rejected
func TestNonTestUsingTestOnlyStructRejected(t *testing.T) {
	t.Parallel()
	src := `
@test_only
define MockUser struct [
    id: int,
    name: string,
]

bind create_admin = fn(): {
    let u = MockUser[id: 1, name: "Admin"]
}

bind main = fn(): {
    create_admin()
}
`
	err := checkCompile(t, src, false)
	if err == nil {
		t.Fatalf("expected compile error when non-test function instantiates @test_only struct, but compiled successfully")
	}
	if !strings.Contains(err.Error(), "cannot use test-only struct") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// 6. Test exclusion in non-test / release builds
func TestTestsOmittedFromNonTestBuild(t *testing.T) {
	t.Parallel()
	src := `
bind helper = fn() int: 123
@test("should not be compiled in release")
bind test_secret_logic = fn(): {
    assert(helper() == 123)
}

bind main = fn() int: {
    helper()
}
`
	// Emit C with TestMode = false (normal application build)
	cSrc, err := emitC(t, src, false, true)
	if err != nil {
		t.Fatalf("emitC failed: %v", err)
	}
	if strings.Contains(cSrc, "test_secret_logic") {
		t.Errorf("test function 'test_secret_logic' was compiled into C source in normal build:\n%s", cSrc)
	}
	if strings.Contains(cSrc, "should not be compiled in release") {
		t.Errorf("test description was compiled into C source in normal build:\n%s", cSrc)
	}

	// Now emit C with TestMode = true
	cTestSrc, err := emitC(t, src, true, false)
	if err != nil {
		t.Fatalf("emitC test failed: %v", err)
	}
	if !strings.Contains(cTestSrc, "test_secret_logic") {
		t.Errorf("test function was missing in test build:\n%s", cTestSrc)
	}
}

// 7. Test runner continues after test assertion failure
func TestTestRunnerContinuesAfterFailure(t *testing.T) {
	t.Parallel()
	src := `
@test("first test passes")
bind test_one = fn(): {
    assert(1 == 1)
}

@test("second test fails")
bind test_two = fn(): {
    assert(1 == 2, "intentional failure")
}

@test("third test passes")
bind test_three = fn(): {
    assert(3 == 3)
}
`
	out, code, err := runLowlightTest(t, src, false)
	// Code should be 1 because at least one test failed
	if code != 1 {
		t.Errorf("expected exit code 1 on failure, got %d (err: %v)\nOutput: %s", code, err, out)
	}
	if !strings.Contains(out, "--- PASS: first test passes") {
		t.Errorf("first test should have passed:\n%s", out)
	}
	if !strings.Contains(out, "--- FAIL: second test fails") {
		t.Errorf("second test should have failed:\n%s", out)
	}
	if !strings.Contains(out, "intentional failure") {
		t.Errorf("panic message missing in failure output:\n%s", out)
	}
	if !strings.Contains(out, "--- PASS: third test passes") {
		t.Errorf("third test should have executed and passed after failure:\n%s", out)
	}
	if !strings.Contains(out, "Test results: 2 passed, 1 failed (total 3)") {
		t.Errorf("summary incorrect in output:\n%s", out)
	}
}

// 8. Test runner detects memory leaks per test
func TestTestRunnerDetectsMemoryLeaks(t *testing.T) {
	t.Parallel()
	src := `
@test("clean test")
bind test_clean = fn(): {
    let lst = [1, 2, 3, 4, 5]
    assert(lst.len() == 5)
}

@test("leaky test")
bind test_leaky = fn(): {
    // Manually allocate raw heap memory without freeing to simulate leak
    unsafe {
        let _p = alloc_raw[u8](64)
    }
}
`
	out, code, err := runLowlightTest(t, src, false)
	if err != nil {
		t.Fatalf("test run failed with err: %v, output: %s", err, out)
	}
	if code != 1 && code != 99 {
		t.Errorf("expected exit code 1 or 99 when test leaks, got %d (err: %v)\nOutput: %s", code, err, out)
	}
	if !strings.Contains(out, "--- PASS: clean test") {
		t.Errorf("clean test should have passed:\n%s", out)
	}
	if !strings.Contains(out, "--- FAIL: leaky test (leaked 1 heap allocation(s))") {
		t.Errorf("leaky test should report heap allocation leak:\n%s", out)
	}
}

// 9. Tests run and pass in release mode with optimizations
func TestTestsRunInReleaseMode(t *testing.T) {
	t.Parallel()
	src := `
bind square = fn(x: int) int: x * x
@test("optimized math in release mode")
bind test_square = fn(): {
    assert(square(12) == 144)
}
`
	out, code, err := runLowlightTest(t, src, true)
	if err != nil {
		t.Fatalf("test run failed: %v\nOutput: %s", err, out)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput: %s", code, out)
	}
	if !strings.Contains(out, "--- PASS: optimized math in release mode") {
		t.Errorf("missing PASS in output:\n%s", out)
	}
}

// 10. Custom test_main entry point
func TestCustomTestMain(t *testing.T) {
	t.Parallel()
	src := `
bind test_main = fn(): {
    println("custom test_main entry point executed")
}
`
	out, code, err := runLowlightTest(t, src, false)
	if err != nil {
		t.Fatalf("test run failed: %v\nOutput: %s", err, out)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput: %s", code, out)
	}
	if !strings.Contains(out, "custom test_main entry point executed") {
		t.Errorf("custom test_main was not executed:\n%s", out)
	}
}

// 11. Test scoping: Single file without tests outputs [no tests to run] and exits 0
func TestTestScopeSingleFileWithoutTests(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	filePath := filepath.Join(root, "examples", "hello.ll")

	opts := driver.Options{
		EntryPath:   filePath,
		ProjectRoot: root,
		TestMode:    true,
	}

	var stdout, stderr bytes.Buffer
	code, err := driver.Test(opts, &stdout, &stderr)
	if err != nil {
		t.Fatalf("driver.Test on hello.ll returned error: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0 on file without tests, got %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "[no tests to run]") {
		t.Errorf("expected '[no tests to run]' in output:\n%s", out)
	}
}

// 11b. Test scoping: Duplicate test declaration produces a Lowlight semantic error
func TestDuplicateTestDeclarationError(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tmpDir := t.TempDir()
	dupPath := filepath.Join(tmpDir, "dup_test.ll")
	src := `import "std/test"

@test("pass")
bind t = fn(): test.assert_eq(1, 1)
@test("fail")
bind t = fn(): test.assert_eq(1, 2)`
	if err := os.WriteFile(dupPath, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	opts := driver.Options{
		EntryPath:   dupPath,
		ProjectRoot: root,
		TestMode:    true,
	}

	var stdout, stderr bytes.Buffer
	code, err := driver.Test(opts, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected compilation error due to duplicate 't' declaration, got nil (code=%d)", code)
	}
	errStr := err.Error()
	if !strings.Contains(errStr, `duplicate declaration of "t"`) {
		t.Errorf("expected error message to contain 'duplicate declaration of \"t\"', got:\n%s", errStr)
	}
}

// 11b2. Test scoping: Duplicate local declaration in a function produces a Lowlight semantic error
func TestDuplicateLocalDeclarationError(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tmpDir := t.TempDir()
	dupPath := filepath.Join(tmpDir, "dup_local.ll")
	src := `bind main = fn(): {
	bind a = 1
	bind a = 2
	let b = 1
	let b = 2
}
`
	if err := os.WriteFile(dupPath, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	opts := driver.Options{
		EntryPath:   dupPath,
		ProjectRoot: root,
	}

	_, err := driver.Build(opts)
	if err == nil {
		t.Fatalf("expected compilation error due to duplicate 'a' and 'b' declarations, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, `duplicate declaration of "a"`) {
		t.Errorf("expected error message to contain 'duplicate declaration of \"a\"', got:\n%s", errStr)
	}
	if !strings.Contains(errStr, `duplicate declaration of "b"`) {
		t.Errorf("expected error message to contain 'duplicate declaration of \"b\"', got:\n%s", errStr)
	}
}

// 11c. Test scoping: Multiple tests with distinct names compile and run successfully
func TestMultipleTestsWithDistinctNames(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "distinct_tests.ll")
	src := `import "std/test"

@test("pass")
bind test_pass = fn(): test.assert_eq(1, 1)
@test("fail")
bind test_fail = fn(): test.assert_eq(1, 2)`
	if err := os.WriteFile(filePath, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	opts := driver.Options{
		EntryPath:   filePath,
		ProjectRoot: root,
		TestMode:    true,
	}

	var stdout, stderr bytes.Buffer
	code, err := driver.Test(opts, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected successful compilation, got error: %v", err)
	}
	if code != 1 {
		t.Fatalf("expected exit code 1 due to 1 test failure, got %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "=== RUN   pass") || !strings.Contains(out, "--- PASS: pass") {
		t.Errorf("expected pass test to run and pass, got:\n%s", out)
	}
	if !strings.Contains(out, "=== RUN   fail") || !strings.Contains(out, "--- FAIL: fail") {
		t.Errorf("expected fail test to run and fail, got:\n%s", out)
	}
	if !strings.Contains(out, "Test results: 1 passed, 1 failed (total 2)") {
		t.Errorf("expected summary '1 passed, 1 failed', got:\n%s", out)
	}
}

// 12. Test scoping: Single file with tests only runs its own tests (does NOT run imported tests)
func TestTestScopeSingleFileWithTestsOnlyRunsItsOwnTests(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "scoped_test.ll")

	src := `
import "std/core"
import "std/strings"
import "std/fs"

@test("isolated local test only")
bind test_isolated = fn(): {
    assert(10 + 5 == 15)
}
`
	if err := os.WriteFile(testPath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	opts := driver.Options{
		EntryPath:   testPath,
		ProjectRoot: root,
		TestMode:    true,
	}

	var stdout, stderr bytes.Buffer
	code, err := driver.Test(opts, &stdout, &stderr)
	if err != nil {
		t.Fatalf("driver.Test error: %v\nOutput: %s\nStderr: %s", err, stdout.String(), stderr.String())
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput: %s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "=== RUN   isolated local test only") {
		t.Errorf("expected local test to run:\n%s", out)
	}
	if !strings.Contains(out, "Test results: 1 passed, 0 failed (total 1)") {
		t.Errorf("expected exactly 1 test result in summary:\n%s", out)
	}
	if strings.Contains(out, "core primitive string conversions") {
		t.Errorf("should NOT have run tests from imported std/core.ll:\n%s", out)
	}
	if strings.Contains(out, "string predicates: is_empty") {
		t.Errorf("should NOT have run tests from imported std/strings.ll:\n%s", out)
	}
}

// 13. Test scoping: Directory without tests outputs [no tests to run] and exits 0
func TestTestScopeDirectoryWithoutTests(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	noTestsDir := t.TempDir()
	dummyFile := filepath.Join(noTestsDir, "app.ll")
	if err := os.WriteFile(dummyFile, []byte("bind main = fn(): {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := driver.Options{
		EntryPath:   noTestsDir,
		ProjectRoot: root,
		TestMode:    true,
	}

	var stdout, stderr bytes.Buffer
	code, err := driver.Test(opts, &stdout, &stderr)
	if err != nil {
		t.Fatalf("driver.Test on examples dir returned error: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0 on directory without tests, got %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "[no tests to run]") {
		t.Errorf("expected '[no tests to run]' in output:\n%s", out)
	}
}

// 14. Test scoping: Directory with multiple packages runs all tests across packages and prints total summary
func TestTestScopeDirectoryWithMultipleTestFiles(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tmpDir := t.TempDir()

	pkgA := filepath.Join(tmpDir, "pkg_a")
	pkgB := filepath.Join(tmpDir, "pkg_b")
	if err := os.MkdirAll(pkgA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgB, 0o755); err != nil {
		t.Fatal(err)
	}

	file1 := filepath.Join(pkgA, "suite_a_test.ll")
	src1 := `
@test("suite A test 1")
bind test_a1 = fn(): { assert(helper_fn() == 42) }
@test("suite A test 2")
bind test_a2 = fn(): { assert(true) }
`
	if err := os.WriteFile(file1, []byte(src1), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}

	file3NoTests := filepath.Join(pkgA, "helper.ll")
	src3 := `
bind helper_fn = fn() int: { 42 }
`
	if err := os.WriteFile(file3NoTests, []byte(src3), 0o644); err != nil {
		t.Fatalf("write file3: %v", err)
	}

	file2 := filepath.Join(pkgB, "suite_b_test.ll")
	src2 := `
@test("suite B test 1")
bind test_b1 = fn(): { assert(1 == 1) }
`
	if err := os.WriteFile(file2, []byte(src2), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	opts := driver.Options{
		EntryPath:   tmpDir,
		ProjectRoot: root,
		TestMode:    true,
	}

	var stdout, stderr bytes.Buffer
	code, err := driver.Test(opts, &stdout, &stderr)
	if err != nil {
		t.Fatalf("driver.Test error: %v\nOutput: %s", err, stdout.String())
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nOutput: %s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "suite A test 1") || !strings.Contains(out, "suite A test 2") {
		t.Errorf("missing suite A tests in output:\n%s", out)
	}
	if !strings.Contains(out, "suite B test 1") {
		t.Errorf("missing suite B tests in output:\n%s", out)
	}
	if !strings.Contains(out, "Total test results: 3 passed, 0 failed across 2 package(s) (total 3)") {
		t.Errorf("expected 3 passed across 2 packages in summary:\n%s", out)
	}
}

// 15. Rich assertion failure diagnostics: Caller location (file, line, col), aligned values, and diff markers
func TestAssertionFailureCallerLocationAndDiff(t *testing.T) {
	t.Parallel()
	src := `import "std/test"

@test("equality failure with diff")
bind test_diff = fn(): {
    test.assert_eq("hello world", "hello there")
}
`
	out, code, err := runLowlightTest(t, src, false)
	if code != 1 {
		t.Fatalf("expected code 1 on assertion failure, got %d (err: %v)\nOutput: %s", code, err, out)
	}
	// Caller location must point to test_file.ll:5:5 (not test.ll)
	if !strings.Contains(out, "test_file.ll:5:5:") {
		t.Errorf("missing caller location test_file.ll:5:5: in output:\n%s", out)
	}
	if strings.Contains(out, "test.ll:8:") {
		t.Errorf("failure message erroneously pointed to std/test.ll instead of caller:\n%s", out)
	}
	// Verify aligned got and expected
	if !strings.Contains(out, "expected: hello there") {
		t.Errorf("missing expected: hello there in output:\n%s", out)
	}
	if !strings.Contains(out, "got: hello world") {
		t.Errorf("missing got: hello world in output:\n%s", out)
	}
	// Verify diff pointer
	if !strings.Contains(out, "^ diff at index 6") {
		t.Errorf("missing '^ diff at index 6' in output:\n%s", out)
	}
}

// 16. Multiline diffing in assertion failure
func TestAssertionFailureMultilineDiff(t *testing.T) {
	t.Parallel()
	src := `import "std/test"

@test("multiline failure diff")
bind test_multi = fn(): {
    let act = "line1\nline2_bad\nline3"
    let exp = "line1\nline2_good\nline3"
    test.assert_eq(act, exp)
}
`
	out, code, err := runLowlightTest(t, src, false)
	if code != 1 {
		t.Fatalf("expected code 1 on assertion failure, got %d (err: %v)\nOutput: %s", code, err, out)
	}
	if !strings.Contains(out, "first mismatch at line 2:") {
		t.Errorf("missing 'first mismatch at line 2:' in output:\n%s", out)
	}
	if !strings.Contains(out, "expected: line2_good") {
		t.Errorf("missing 'expected: line2_good' in output:\n%s", out)
	}
	if !strings.Contains(out, "got: line2_bad") {
		t.Errorf("missing 'got: line2_bad' in output:\n%s", out)
	}
}

// 17. Custom @track_caller helper forwards caller location
func TestTrackCallerHelperForwarding(t *testing.T) {
	t.Parallel()
	src := `import "std/test"

@test_only
@track_caller
bind assert_even = fn(x: int): {
    test.assert_eq(x % 2, 0)
}

@test("track caller through custom helper")
bind test_even = fn(): {
    assert_even(3)
}
`
	out, code, err := runLowlightTest(t, src, false)
	if code != 1 {
		t.Fatalf("expected code 1 on assertion failure, got %d (err: %v)\nOutput: %s", code, err, out)
	}
	// Caller location must point to line 11 (the call site in test_even), not line 6 (inside assert_even)
	if !strings.Contains(out, "test_file.ll:11:5:") {
		t.Errorf("expected caller location test_file.ll:11:5: in output, got:\n%s", out)
	}
}

// 18. Multiline backtick strings with interpolation and escapes
func TestMultilineBacktickStringsEndToEnd(t *testing.T) {
	t.Parallel()
	src := "import \"std/test\"\n\n" +
		"@test(\"backtick multiline, interpolation, and escapes\")\n" +
		"bind test_backtick = fn(): {\n" +
		"    let name = \"World\"\n" +
		"    let val = 42\n" +
		"    let s = `line 1: $name\n" +
		"line 2: ${val * 2}\n" +
		"\"quotes\" and 'apostrophes'\n" +
		"\\`escaped backticks\\`\n" +
		"\\$100`\n\n" +
		"    let expected = \"line 1: World\\nline 2: 84\\n\\\"quotes\\\" and 'apostrophes'\\n`escaped backticks`\\n\\$100\"\n" +
		"    test.assert_eq(s, expected)\n" +
		"}\n"
	out, code, err := runLowlightTest(t, src, false)
	if code != 0 || err != nil {
		t.Fatalf("expected code 0, got %d (err: %v)\nOutput: %s", code, err, out)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Errorf("expected test to pass, got:\n%s", out)
	}
}

func TestAssertionApproxEqAndStringMatchFailureDiagnostics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		callLine string
		want     []string
	}{
		{
			name:     "assert_approx_eq_out_of_tolerance",
			callLine: "    test.assert_approx_eq(1.5, 1.0, 0.1)",
			want:     []string{"test_file.ll:5:5:", "expected approximately 1", "(+/- 0.1)", "got 1.5"},
		},
		{
			name:     "assert_approx_eq_negative_tolerance",
			callLine: "    test.assert_approx_eq(1.0, 1.0, -0.01)",
			want:     []string{"test_file.ll:5:5:", "tolerance must be non-negative"},
		},
		{
			name:     "assert_contains_failure",
			callLine: `    test.assert_contains("hello world", "xyz")`,
			want:     []string{"test_file.ll:5:5:", "contain substring", "actual: hello world", "substring: xyz"},
		},
		{
			name:     "assert_starts_with_failure",
			callLine: `    test.assert_starts_with("hello world", "world")`,
			want:     []string{"test_file.ll:5:5:", "start with prefix", "actual: hello world", "prefix: world"},
		},
		{
			name:     "assert_ends_with_failure",
			callLine: `    test.assert_ends_with("hello world", "hello")`,
			want:     []string{"test_file.ll:5:5:", "end with suffix", "actual: hello world", "suffix: hello"},
		},
		{
			name:     "assert_ok_eq_when_err",
			callLine: `    test.assert_ok_eq(Result[int].fail_msg("bad parse"), 42)`,
			want:     []string{"test_file.ll:5:5:", "expected a value, but the Result holds a failure", "bad parse"},
		},
		{
			name:     "assert_ok_eq_when_value_mismatch",
			callLine: `    test.assert_ok_eq(Result[int].value(7), 42)`,
			want:     []string{"test_file.ll:5:5:", "values are not equal", "expected: 42", "got: 7"},
		},
		{
			name:     "assert_ok_gt_when_err",
			callLine: `    test.assert_ok_gt(Result[int].fail_msg("too small"), 10)`,
			want:     []string{"test_file.ll:5:5:", "expected a value, but the Result holds a failure", "too small"},
		},
		{
			name:     "assert_ok_gt_when_value_fails",
			callLine: `    test.assert_ok_gt(Result[int].value(5), 10)`,
			want:     []string{"test_file.ll:5:5:", "expected actual > threshold", "actual: 5", "threshold: 10"},
		},
		{
			name:     "assert_ok_contains_when_mismatch",
			callLine: `    test.assert_ok_contains(Result[string].value("hello"), "xyz")`,
			want:     []string{"test_file.ll:5:5:", "contain substring", "actual: hello", "substring: xyz"},
		},
		{
			name:     "assert_err_contains_when_ok",
			callLine: `    test.assert_err_contains(Result[int].value(1), "boom")`,
			want:     []string{"test_file.ll:5:5:", "expected a failure, but the Result holds a value"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import \"std/test\"\n\n@test(\"failure case\")\nbind test_case = fn(): {\n" + tc.callLine + "\n}\n"
			out, code, err := runLowlightTest(t, src, false)
			if code != 1 {
				t.Fatalf("expected exit code 1, got %d (err: %v)\nOutput:\n%s", code, err, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("expected output to contain %q\nOutput:\n%s", w, out)
				}
			}
		})
	}
}

func TestBatchAssertionsReportAllFailuresAndCallerLocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		callLine string
		want     []string
		notWant  []string
	}{
		{
			name:     "assert_all_eq_collects_all_failures_without_short_circuit",
			callLine: `    test.assert_all_eq([+["alpha", "alpxa"], +["beta", "beta"], +["gamma", "gamna"], +["delta", "delta"]])`,
			want: []string{
				"test_file.ll:5:5:",
				"2 of 4 case(s) failed (2 passed)",
				"case [0]:",
				"expected: alpha",
				"got: alpxa",
				"case [2]:",
				"expected: gamma",
				"got: gamna",
			},
			notWant: []string{
				"std/test/test.ll",
				"case [1]:",
				"case [3]:",
				"beta",
				"delta",
			},
		},
		{
			name:     "assert_all_eq_named_includes_labels",
			callLine: `    test.assert_all_eq_named([+["first", 10, 99], +["second", 20, 20], +["third", 30, 88]])`,
			want: []string{
				"test_file.ll:5:5:",
				"2 of 3 case(s) failed (1 passed)",
				"case [0] (first):",
				"case [2] (third):",
			},
			notWant: []string{
				"case [1]",
				"second",
			},
		},
		{
			name:     "assert_all_ok_eq_reports_both_err_and_value_mismatch",
			callLine: `    test.assert_all_ok_eq_msg([+[10, Result[int].fail_msg("boom")], +[20, Result[int].value(20)], +[30, Result[int].value(99)]], "parse batch")`,
			want: []string{
				"test_file.ll:5:5:",
				"parse batch: 2 of 3 case(s) failed (1 passed)",
				"case [0]:",
				"expected a value, but the Result holds a failure",
				"boom",
				"case [2]:",
				"expected: 30",
				"got: 99",
			},
			notWant: []string{
				"case [1]:",
			},
		},
		{
			name:     "assert_all_approx_eq_collects_multiple_failures",
			callLine: `    test.assert_all_approx_eq([+[1.0, 1.5], +[2.0, 2.01], +[3.0, 3.8]], 0.1)`,
			want: []string{
				"test_file.ll:5:5:",
				"2 of 3 case(s) failed (1 passed)",
				"case [0]:",
				"case [2]:",
			},
			notWant: []string{
				"case [1]:",
			},
		},
		{
			name:     "assert_any_eq_lists_all_candidates_when_none_match",
			callLine: `    test.assert_any_eq([+[1, 2], +[3, 4]])`,
			want: []string{
				"test_file.ll:5:5:",
				"no (expected, actual) pair matched\n\n      [0] expected: 1, got: 2",
				"[0] expected: 1, got: 2",
				"[1] expected: 3, got: 4",
			},
		},
		{
			name:     "assert_all_ok_eq_named_err_and_value_mismatch_spacing_and_alignment",
			callLine: `    test.assert_all_ok_eq_named_msg([+["bad err", 10, Result[int].fail_msg("boom")], +["passes", 20, Result[int].value(20)], +["wrong val", 30, Result[int].value(99)]], "parse batch")`,
			want: []string{
				"test_file.ll:5:5:",
				"parse batch: 2 of 3 case(s) failed (1 passed)\n\n    ✗ case [0] (bad err):",
				"      expected a value, but the Result holds a failure\n        error: boom\n        trace:\n          at ",
				"\n\n    ✗ case [2] (wrong val):\n      values are not equal\n        expected: 30\n             got: 99",
			},
			notWant: []string{
				"case [1]",
				"passes",
				"\n    \n",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import \"std/test\"\n\n@test(\"batch failure case\")\nbind test_case = fn(): {\n" + tc.callLine + "\n}\n"
			out, code, err := runLowlightTest(t, src, false)
			if code != 1 {
				t.Fatalf("expected exit code 1, got %d (err: %v)\nOutput:\n%s", code, err, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("expected output to contain %q\nOutput:\n%s", w, out)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(out, nw) {
					t.Errorf("did not expect output to contain %q\nOutput:\n%s", nw, out)
				}
			}
		})
	}
}

func TestBatchFailureFormattingBackendParity(t *testing.T) {
	t.Parallel()
	src := "import \"std/test\"\n\n" +
		"bind make_err = fn() Result[string]: Result[string].fail_msg(\"outer: inner failure\\n\")\n\n" +
		"@test(\"batch readability parity\")\n" +
		"bind test_batch_parity = fn(): {\n" +
		"    test.assert_all_ok_eq_named([\n" +
		"        +[\"err case\", \"ok\", make_err()],\n" +
		"        +[\"passing\", \"same\", Result[string].value(\"same\")],\n" +
		"        +[\"tab diff\", \"top\\n\\t\\t\\\"k\\\": [1]\", Result[string].value(\"top\\n\\t\\t\\\"k\\\": []\")],\n" +
		"    ])\n" +
		"}\n"

	tmpDir := t.TempDir()
	entryPath := filepath.Join(tmpDir, "test_file.ll")
	if err := os.WriteFile(entryPath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	root := projectRoot(t)

	runBackend := func(backend driver.BackendKind) string {
		opts := driver.Options{
			EntryPath:   entryPath,
			ProjectRoot: root,
			Backend:     backend,
			TestMode:    true,
		}
		var stdout, stderr bytes.Buffer
		code, err := driver.Test(opts, &stdout, &stderr)
		out := stdout.String() + stderr.String()
		if code != 1 {
			t.Fatalf("[%s] expected exit code 1, got %d (err: %v)\nOutput:\n%s", backend, code, err, out)
		}
		return out
	}

	c11Out := runBackend(driver.BackendC11)
	llvmOut := runBackend(driver.BackendLLVM)

	if c11Out != llvmOut {
		t.Fatalf("C11 and LLVM batch failure output diverged:\n--- C11 ---\n%s\n--- LLVM ---\n%s", c11Out, llvmOut)
	}
	for _, want := range []string{
		"2 of 3 case(s) failed (1 passed)\n\n    ✗ case [0] (err case):",
		"        error: outer: inner failure\n        trace:\n          at ",
		"\n\n    ✗ case [2] (tab diff):\n      values are not equal\n        first mismatch at line 2:\n          expected: \t\t\"k\": [1]\n               got: \t\t\"k\": []\n                    \t\t      ^ diff at index 8",
	} {
		if !strings.Contains(c11Out, want) {
			t.Errorf("expected output to contain %q\nOutput:\n%s", want, c11Out)
		}
	}
	if strings.Contains(c11Out, "\n    \n") {
		t.Errorf("unexpected whitespace-only blank line in output:\n%s", c11Out)
	}
}

func TestClickableRelativePathsInTestOutput(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tmpBinDir := t.TempDir()
	binPath := filepath.Join(tmpBinDir, "lowlight")

	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/lowlight")
	buildCmd.Dir = root
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}

	workDir := t.TempDir()
	subDir := filepath.Join(workDir, "pkg", "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	src := `import "std/test"

bind fail_err = fn() Result[int]:
    Result[int].fail_msg("boom")

@test("relative clickable paths")
bind test_rel = fn(): {
    test.assert_all_ok_eq_named([
        +["err", 1, fail_err()],
    ])
}
`
	testPath := filepath.Join(subDir, "clickable_test.ll")
	if err := os.WriteFile(testPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	runCmd := exec.Command(binPath, "test", "pkg/nested/clickable_test.ll")
	runCmd.Dir = workDir
	env := os.Environ()
	env = append(env, "LOWLIGHT_ROOT="+root)
	runCmd.Env = env
	outBytes, err := runCmd.CombinedOutput()
	out := string(outBytes)
	if err == nil {
		t.Fatalf("expected non-zero exit code for failing test\nOutput:\n%s", out)
	}
	if !strings.Contains(out, "pkg/nested/clickable_test.ll:8:5:") {
		t.Errorf("expected workspace-relative clickable caller location in output:\n%s", out)
	}
	if !strings.Contains(out, "at pkg/nested/clickable_test.ll:4:") {
		t.Errorf("expected workspace-relative clickable stack trace location in output:\n%s", out)
	}
}

func TestTestListAndJSONOutput(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tmpDir := t.TempDir()

	src := `import "std/test"

bind helper_err = fn() Result[int]:
    Result[int].fail_msg("inner error")

@test("passing math")
bind test_pass = fn(): {
    test.assert_eq(2 + 2, 4)
}

@test("failing eq diff")
bind test_diff = fn(): {
    test.assert_eq("actual_v", "expect_v")
}

@test("failing batch")
bind test_batch = fn(): {
    test.assert_all_ok_eq_named([
        +["err_case", 1, helper_err()],
        +["val_case", 2, Result[int].value(9)],
    ])
}
`
	filePath := filepath.Join(tmpDir, "json_suite.ll")
	if err := os.WriteFile(filePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 1. Test --list plain and --list --json (with and without -run filter)
	{
		var out bytes.Buffer
		code, err := driver.Test(driver.Options{
			Targets:     []string{filePath},
			ProjectRoot: root,
			TestList:    true,
		}, &out, &out)
		if err != nil || code != 0 {
			t.Fatalf("--list failed: code=%d err=%v\n%s", code, err, out.String())
		}
		if !strings.Contains(out.String(), "test_pass\tpassing math") || !strings.Contains(out.String(), "test_batch\tfailing batch") {
			t.Errorf("unexpected --list output:\n%s", out.String())
		}
	}

	{
		var out bytes.Buffer
		code, err := driver.Test(driver.Options{
			Targets:     []string{filePath},
			ProjectRoot: root,
			TestList:    true,
			TestJSON:    true,
			TestFilter:  "diff",
		}, &out, &out)
		if err != nil || code != 0 {
			t.Fatalf("--list --json failed: code=%d err=%v\n%s", code, err, out.String())
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("expected 1 filtered test event, got %d:\n%s", len(lines), out.String())
		}
		var disc driver.DiscoveredTest
		if err := json.Unmarshal([]byte(lines[0]), &disc); err != nil {
			t.Fatalf("unmarshal list event: %v", err)
		}
		if disc.Name != "test_diff" || disc.Desc != "failing eq diff" || disc.Line != 12 {
			t.Errorf("unexpected discovered metadata: %+v", disc)
		}
	}

	// 2. Test --json streaming output across both C11 and LLVM backends
	for _, backend := range []driver.BackendKind{driver.BackendC11, driver.BackendLLVM} {
		var stdout, stderr bytes.Buffer
		code, err := driver.Test(driver.Options{
			Targets:     []string{filePath},
			ProjectRoot: root,
			Backend:     backend,
			TestJSON:    true,
		}, &stdout, &stderr)
		if err != nil || code != 1 {
			t.Fatalf("[%s] expected code 1 err nil, got code=%d err=%v\nstdout:\n%s\nstderr:\n%s",
				backend, code, err, stdout.String(), stderr.String())
		}

		var events []driver.TestEvent
		for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var ev driver.TestEvent
			if uErr := json.Unmarshal([]byte(line), &ev); uErr != nil {
				t.Fatalf("[%s] invalid JSON line %q: %v", backend, line, uErr)
			}
			events = append(events, ev)
		}

		var sawPass, sawDiffFail, sawBatchFail, sawSummary bool
		for _, ev := range events {
			switch {
			case ev.Event == "pass" && ev.Name == "test_pass":
				sawPass = true
			case ev.Event == "fail" && ev.Name == "test_diff":
				sawDiffFail = true
				if ev.FailLine != 13 || ev.Expected == nil || *ev.Expected != "expect_v" || ev.Actual == nil || *ev.Actual != "actual_v" {
					t.Errorf("[%s] unexpected diff fail fields: %+v", backend, ev)
				}
			case ev.Event == "fail" && ev.Name == "test_batch":
				sawBatchFail = true
				if len(ev.Cases) != 2 {
					t.Errorf("[%s] expected 2 batch cases, got %+v", backend, ev.Cases)
				} else {
					if ev.Cases[0].Label != "err_case" || ev.Cases[0].FailLine != 4 {
						t.Errorf("[%s] expected err_case trace at line 4, got %+v", backend, ev.Cases[0])
					}
					if ev.Cases[1].Label != "val_case" || ev.Cases[1].Expected == nil || *ev.Cases[1].Expected != "2" || ev.Cases[1].Actual == nil || *ev.Cases[1].Actual != "9" {
						t.Errorf("[%s] expected val_case diff 2 vs 9, got %+v", backend, ev.Cases[1])
					}
				}
			case ev.Event == "summary":
				sawSummary = true
				if ev.Passed != 1 || ev.Failed != 2 || ev.Total != 3 {
					t.Errorf("[%s] unexpected summary: %+v", backend, ev)
				}
			}
		}
		if !sawPass || !sawDiffFail || !sawBatchFail || !sawSummary {
			t.Errorf("[%s] missing expected events (pass=%v diff=%v batch=%v summary=%v)\nstdout:\n%s",
				backend, sawPass, sawDiffFail, sawBatchFail, sawSummary, stdout.String())
		}
	}

	// 3. Test build_error emission in --json mode
	badFile := filepath.Join(tmpDir, "bad_test.ll")
	if err := os.WriteFile(badFile, []byte("@test\nbind bad = fn(): unknown_symbol_xyz()\n"), 0o644); err != nil {
		t.Fatalf("write badFile: %v", err)
	}
	var badOut, badErr bytes.Buffer
	code, err := driver.Test(driver.Options{
		Targets:     []string{badFile},
		ProjectRoot: root,
		TestJSON:    true,
	}, &badOut, &badErr)
	if err != nil || code != 1 {
		t.Fatalf("expected code 1 on build_error, got %d (err: %v)", code, err)
	}
	if !strings.Contains(badOut.String(), `"event":"build_error"`) {
		t.Errorf("expected build_error event in stdout:\n%s", badOut.String())
	}
}
