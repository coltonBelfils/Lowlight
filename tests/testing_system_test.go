package tests

import (
	"bytes"
	"os"
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
	src := `
bind add = (a: int, b: int) int: a + b
bind mul = (a: int, b: int) int: a * b
bind main = (): {
    println("production main")
}

@test("addition works")
bind test_add = (): {
    assert(add(2, 3) == 5)
}

@test
bind test_multiplication = (): {
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
	src := `
import "std/test.ll" as test

@test("testing assertion library")
bind test_assertions = (): {
    test.assert_eq(10 + 20, 30)
    test.assert_ne(10, 20)
    test.assert_true(true)
    test.assert_false(false)
    test.assert_gt(50, 10)
    test.assert_lt(10, 50)
    test.assert_ge(50, 50)
    test.assert_le(50, 50)

    let ok_res = Result[int].ok(42)
    test.assert_is_ok(&ok_res)

    let err_res = Result[int].fail_msg("boom")
    test.assert_is_err(&err_res)

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
	src := `
@test("secret test")
bind my_test = (): {
    assert(1 == 1)
}

bind production_func = (): {
    my_test()
}

bind main = (): {
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
	src := `
@test_only
bind setup_database_fixture = () int: {
    42
}

bind calculate_metrics = () int: {
    setup_database_fixture()
}

bind main = (): {
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
	src := `
@test_only
define MockUser struct [
    id: int,
    name: string,
]

bind create_admin = (): {
    let u = MockUser[id: 1, name: "Admin"]
}

bind main = (): {
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
	src := `
bind helper = () int: 123
@test("should not be compiled in release")
bind test_secret_logic = (): {
    assert(helper() == 123)
}

bind main = () int: {
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
	src := `
@test("first test passes")
bind test_one = (): {
    assert(1 == 1)
}

@test("second test fails")
bind test_two = (): {
    assert(1 == 2, "intentional failure")
}

@test("third test passes")
bind test_three = (): {
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
	src := `
@test("clean test")
bind test_clean = (): {
    let lst = [1, 2, 3, 4, 5]
    assert(lst.len() == 5)
}

@test("leaky test")
bind test_leaky = (): {
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
	src := `
bind square = (x: int) int: x * x
@test("optimized math in release mode")
bind test_square = (): {
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
	src := `
bind test_main = (): {
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
	root := projectRoot(t)
	tmpDir := t.TempDir()
	dupPath := filepath.Join(tmpDir, "dup_test.ll")
	src := `import "std/test.ll"

@test("pass")
bind t = (): assert_eq(1, 1)
@test("fail")
bind t = (): assert_eq(1, 2)`
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
	root := projectRoot(t)
	tmpDir := t.TempDir()
	dupPath := filepath.Join(tmpDir, "dup_local.ll")
	src := `bind main = (): {
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
	root := projectRoot(t)
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "distinct_tests.ll")
	src := `import "std/test.ll"

@test("pass")
bind test_pass = (): assert_eq(1, 1)
@test("fail")
bind test_fail = (): assert_eq(1, 2)`
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
	root := projectRoot(t)
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "scoped_test.ll")

	src := `
import "std/core.ll"
import "std/strings.ll"
import "std/fs.ll"

@test("isolated local test only")
bind test_isolated = (): {
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
	root := projectRoot(t)
	noTestsDir := t.TempDir()
	dummyFile := filepath.Join(noTestsDir, "app.ll")
	if err := os.WriteFile(dummyFile, []byte("bind main = (): {}\n"), 0644); err != nil {
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

// 14. Test scoping: Directory with multiple test files runs tests in all test files and prints total summary
func TestTestScopeDirectoryWithMultipleTestFiles(t *testing.T) {
	root := projectRoot(t)
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "test_suite_a.ll")
	src1 := `
@test("suite A test 1")
bind test_a1 = (): { assert(true) }
@test("suite A test 2")
bind test_a2 = (): { assert(true) }
`
	if err := os.WriteFile(file1, []byte(src1), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}

	file2 := filepath.Join(tmpDir, "test_suite_b.ll")
	src2 := `
@test("suite B test 1")
bind test_b1 = (): { assert(1 == 1) }
`
	if err := os.WriteFile(file2, []byte(src2), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	file3NoTests := filepath.Join(tmpDir, "helper.ll")
	src3 := `
bind helper_fn = () int: { 42 }
`
	if err := os.WriteFile(file3NoTests, []byte(src3), 0o644); err != nil {
		t.Fatalf("write file3: %v", err)
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
	if !strings.Contains(out, "Total test results: 3 passed, 0 failed across 2 file(s) (total 3)") {
		t.Errorf("expected 3 passed across 2 files in summary:\n%s", out)
	}
}

// 15. Rich assertion failure diagnostics: Caller location (file, line, col), aligned values, and diff markers
func TestAssertionFailureCallerLocationAndDiff(t *testing.T) {
	src := `import "std/test.ll" as test

@test("equality failure with diff")
bind test_diff = (): {
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
	src := `import "std/test.ll" as test

@test("multiline failure diff")
bind test_multi = (): {
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
	src := `import "std/test.ll" as test

@test_only
@track_caller
bind assert_even = (x: int): {
    test.assert_eq(x % 2, 0)
}

@test("track caller through custom helper")
bind test_even = (): {
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
	src := "import \"std/test.ll\" as test\n\n" +
		"@test(\"backtick multiline, interpolation, and escapes\")\n" +
		"bind test_backtick = (): {\n" +
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

