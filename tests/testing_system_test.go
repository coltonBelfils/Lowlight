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
bind add = (a: int, b: int): int a + b
bind mul = (a: int, b: int): int a * b

bind main = () {
    println("production main")
}

@test("addition works")
bind test_add = () {
    assert(add(2, 3) == 5)
}

@test
bind test_multiplication = () {
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
bind test_assertions = () {
    test.assert_eq(10 + 20, 30)
    test.assert_ne(10, 20)
    test.assert_true(true)
    test.assert_false(false)
    test.assert_gt(50, 10)
    test.assert_lt(10, 50)
    test.assert_ge(50, 50)
    test.assert_le(50, 50)

    let ok_res = Result[int, string].ok(42)
    test.assert_is_ok(&ok_res)

    let err_res = Result[int, string].fail("boom")
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
bind my_test = () {
    assert(1 == 1)
}

bind production_func = () {
    my_test()
}

bind main = () {
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
bind setup_database_fixture = (): int {
    42
}

bind calculate_metrics = (): int {
    setup_database_fixture()
}

bind main = () {
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

bind create_admin = () {
    let u = MockUser[id: 1, name: "Admin"]
}

bind main = () {
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
bind helper = (): int 123

@test("should not be compiled in release")
bind test_secret_logic = () {
    assert(helper() == 123)
}

bind main = (): int {
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
bind test_one = () {
    assert(1 == 1)
}

@test("second test fails")
bind test_two = () {
    assert(1 == 2, "intentional failure")
}

@test("third test passes")
bind test_three = () {
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
bind test_clean = () {
    let lst = [1, 2, 3, 4, 5]
    assert(lst.len() == 5)
}

@test("leaky test")
bind test_leaky = () {
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
bind square = (x: int): int x * x

@test("optimized math in release mode")
bind test_square = () {
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
bind test_main = () {
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
