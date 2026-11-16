package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func projectRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), ".."))
}

var strictCFlags = []string{
	"-Wall",
	"-Wextra",
	"-Werror",
	"-fsanitize=address,undefined",
}

func runInlineStrictBackend(t *testing.T, name string, src string, release bool, backend driver.BackendKind) string {
	t.Helper()
	root := projectRoot(t)
	entry := filepath.Join(t.TempDir(), name+".ll")

	var stdout, stderr bytes.Buffer
	rc, err := driver.Run(driver.Options{
		EntryPath:   entry,
		SrcOverride: src,
		ProjectRoot: root,
		Backend:     backend,
		Release:     release,
		ExtraCFlags: strictCFlags,
		Env: []string{
			"LOWLIGHT_CHECK_LEAKS=1",
			"ASAN_OPTIONS=detect_leaks=0", // macOS Clang ASan doesn't support LSan; LOWLIGHT_CHECK_LEAKS=1 checks exact allocations!
		},
	}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("[%s backend=%s release=%v] compilation/execution error:\n%v\nstderr:\n%s", name, backend, release, err, stderr.String())
	}
	if rc != 0 {
		t.Fatalf("[%s backend=%s release=%v] exited with code %d\nstdout:\n%s\nstderr:\n%s", name, backend, release, rc, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func runInlineStrict(t *testing.T, name string, src string, release bool) string {
	t.Helper()
	outLLVM := runInlineStrictBackend(t, name+"_llvm", src, release, driver.BackendLLVM)
	outC11 := runInlineStrictBackend(t, name+"_c11", src, release, driver.BackendC11)
	if outLLVM != outC11 {
		t.Fatalf("[%s release=%v] backend parity mismatch between LLVM and C11:\nLLVM output:\n%s\nC11 output:\n%s", name, release, outLLVM, outC11)
	}
	return outLLVM
}

// -----------------------------------------------------------------------------
// 1. All Showcase Examples (`examples/*.ll`) under Strict LLVM + C11 + ASan/UBSan + Leak Check
// -----------------------------------------------------------------------------

func TestAllShowcaseExamplesStrict(t *testing.T) {
	root := projectRoot(t)
	showcaseNames := []string{
		"hello.ll",
		"control_flow_unwinds.ll",
		"ownership_and_borrows.ll",
		"interfaces_shapes.ll",
		"generics_and_pool.ll",
		"arena_graph.ll",
		"unsafe_and_ffi.ll",
		"mini_lexer_eval.ll",
	}

	for _, base := range showcaseNames {
		file := filepath.Join(root, "examples", base)
		for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
			for _, release := range []bool{false, true} {
				mode := "debug"
				if release {
					mode = "release"
				}
				testName := fmt.Sprintf("%s_%s_%s", base, backend, mode)
				t.Run(testName, func(t *testing.T) {
					var stdout, stderr bytes.Buffer
					rc, runErr := driver.Run(driver.Options{
						EntryPath:   file,
						ProjectRoot: root,
						Backend:     backend,
						Release:     release,
						ExtraCFlags: strictCFlags,
						Env: []string{
							"LOWLIGHT_CHECK_LEAKS=1",
							"ASAN_OPTIONS=detect_leaks=0",
						},
					}, nil, &stdout, &stderr)
					if runErr != nil {
						t.Fatalf("%s (%s %s) failed to build/run:\n%v\nstderr:\n%s", base, backend, mode, runErr, stderr.String())
					}
					if rc != 0 {
						t.Fatalf("%s (%s %s) exited with non-zero status %d\nstdout:\n%s\nstderr:\n%s",
							base, backend, mode, rc, stdout.String(), stderr.String())
					}
					if strings.TrimSpace(stdout.String()) == "" {
						t.Fatalf("%s (%s %s) produced empty stdout", base, backend, mode)
					}
				})
			}
		}
	}
}

// -----------------------------------------------------------------------------
// 2. Primitives, UB-Safe Bitwise/Overflow Math, Enums, & Distinct Types
// -----------------------------------------------------------------------------

func TestPrimitivesBitwiseEnumsDistinct(t *testing.T) {
	src := `
define Color enum [Red, Green, Blue]
define UserId int

bind UserId.is_admin = (self) bool: {
    int(self) == 1
}

// Order-independent top-level mutual recursion with expression bodies
bind is_even = (n: int) bool: if (n == 0): true else: is_odd(n - 1)bind is_odd  = (n: int) bool: if (n == 0): false else: is_even(n - 1)
bind main = (): {
    // Fixed-width integers & wrapping two's-complement corner cases (UBSan must NOT trap!)
    let min_i64: int = -9223372036854775807 - 1
    let neg_one: int = -1
    let div_wrap = min_i64 / neg_one
    let mod_wrap = min_i64 % neg_one
    assert(div_wrap == min_i64)
    assert(mod_wrap == 0)

    // Bitwise shifts & fixed-width unsigned byte operations (contextual untyped literals!)
    let shl = 1 << 5
    assert(shl == 32)
    let u: u8 = 0b1010_0101
    let inv: u8 = ~u
    assert(inv == 0b0101_1010)
    assert((u & 0x0F) == 0x05)
    assert((u | 0x0F) == 0xAF)
    assert((u ^ 0xFF) == inv)

    // Floats & conversions
    let f32v: f32 = 1.5
    let f64v: float = float(f32v) + 2.25
    assert(f64v == 3.75)

    // Enums ('==', 'match', 'to_string')
    let c = Color.Green
    assert(c.to_string() == "Color.Green")
    let code = match (c):
        when Color.Red:   10
        when Color.Green: 20
        default Color.Blue: 30
    assert(code == 20)

    // Distinct types ('T(base)', 'Base(v)', methods, hash keys)
    let root = UserId(1)
    let guest = UserId(42)
    assert(root.is_admin())
    assert(not guest.is_admin())
    assert(int(guest) == 42)

    let roles = #[root: "admin", guest: "viewer"]
    assert((roles[UserId(1)] ?? "") == "admin")
    assert(is_even(12) and not is_even(13))
    assert(is_odd(13) and not is_odd(12))
    println("primitives_ok")
}
`
	out := runInlineStrict(t, "primitives_enum_distinct", src, false)
	if strings.TrimSpace(out) != "primitives_ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

// -----------------------------------------------------------------------------
// 3. Strings (UTF-8, Slicing, CoW Promotion) & Built-in Collections
// -----------------------------------------------------------------------------

func TestStringsAndCollections(t *testing.T) {
	src := `
bind sum_slice = (xs: [int]) int: {
    let total = 0
    for (x in xs): {
        total += x
    }
    total
}

bind main = (): {
    // UTF-8 codepoint iteration + byte slicing + CoW promotion on mutation
    let greeting = "Hi 🌍!"
    let chars: [char] = []
    for (ch in greeting): {
        chars.push(ch)
    }
    assert(chars.len() == 5)
    assert(chars[0] == 'H')
    assert(chars[3] == '🌍')

    // Range slicing 's[lo..hi]' + owned string mutation
    let sub = greeting[0..2]
    assert(sub == "Hi")
    sub.push(" Lowlight")
    assert(sub == "Hi Lowlight")
    assert(greeting == "Hi 🌍!")

    // Stack arrays '[N: T]': repeat syntax '[N: val]', borrow-as-slice coercion
    let fixed: [4: int] = [4: 10]
    fixed[1] = 20
    fixed[3] = 40
    assert(fixed.len() == 4)
    assert(fixed.contains(20))
    assert(sum_slice(fixed) == 80)

    // List range slice 'v[lo..hi]' & inclusive 'v[lo..=hi]'
    let vec = [1, 2, 3, 4, 5]
    let mid = vec[1..4]
    assert(sum_slice(vec[1..=3]) == 9)
    assert(sum_slice(mid) == 9)
    mid.push(99)
    assert(mid.len() == 4 and mid[3] == 99)
    assert(vec.len() == 5 and vec[3] == 4)

    // Insertion-ordered Set '#[T]' with Resource ('string') keys
    let tags = #["alpha", "beta"]
    assert(tags.insert("gamma"))
    assert(not tags.insert("alpha"))
    assert(tags.has("beta"))
    assert(tags.remove("beta"))
    let ordered_tags: [string] = []
    for (t in tags): {
        ordered_tags.push(t.copy())
    }
    assert(ordered_tags.join(",") == "alpha,gamma")

    // Insertion-ordered Map '#[K: V]' with Resource keys & values, overwrite + remove
    let scores: #[string: string] = #["alice": "A", "bob": "B"]
    scores.insert("alice", "A+") // Overwrites in place without leaking old "A" or duplicate key!
    scores.insert("carol", "A")
    let removed? = scores.remove("bob")
    assert((removed? ?? "") == "B")

    let pairs: [string] = []
    for (k, v in scores): {
        pairs.push("$k=$v")
    }
    assert(pairs.join("; ") == "alice=A+; carol=A")
    println("collections_ok")
}
`
	out := runInlineStrict(t, "strings_and_collections", src, false)
	if strings.TrimSpace(out) != "collections_ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

// -----------------------------------------------------------------------------
// 4. Optionals (`?` Rule, `&x?`, `as`, `?.`, `??`) & `std/fs` File I/O
// -----------------------------------------------------------------------------

func TestOptionalsAndStdFs(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "roundtrip.txt")
	// Escape backslashes if any for Lowlight string literal
	escapedPath := strings.ReplaceAll(tmpFile, "\\", "\\\\")

	src := `
import "std/fs" as fs

define Profile struct [
    nick: string,
    score: int,
]

define Account struct [
    profile?: ?Profile,
]

bind Account.bonus = (self) int: {
    (self.profile? ?.score ?? 0) + 10
}

bind parse_pair = (a: string, b: string) ?int: {
    let x = parse_int(a) ?? func <- none
    let y = parse_int(b) ?? func <- none
    x + y
}

bind main = (): {
    // Mutable optional unwrap 'if (&opt?)' mutates inner value in place!
    let count?: ?int = 10
    if (&count?): {
        count += 5
    }
    assert((count? ?? 0) == 15)

    // Scoped optional binding 'if (expr as v)'
    if (parse_pair("12", "30") as total): {
        assert(total == 42)
    } else: {
        panic("expected Some(42)")
    }
    assert((parse_pair("12", "bad") ?? -1) == -1)

    // Optional chaining '?.' on structs
    let empty_acc = Account[profile?: none]
    let full_acc = Account[profile?: Profile[nick: "neo", score: 90]]
    assert(empty_acc.bonus() == 10)
    assert(full_acc.bonus() == 100)
    assert((full_acc.profile? ?.nick ?.len() ?? 0) == 3)

    // Standard library 'std/fs' roundtrip with generic 'Result[T, E]'
    let p = "` + escapedPath + `"
    assert(not fs.exists(p))
    let wres = fs.write_text(p, "line1\nline2")
    assert(wres.is_ok())
    assert(fs.exists(p))

    let rres = fs.read_text(p)
    assert(rres.is_ok())
    let body = rres.unwrap_or("")
    assert(body == "line1\nline2")
    assert(fs.remove_file(p))
    assert(not fs.exists(p))

    println("optionals_fs_ok")
}
`
	out := runInlineStrict(t, "optionals_and_fs", src, false)
	if strings.TrimSpace(out) != "optionals_fs_ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

// -----------------------------------------------------------------------------
// 5. First-Class Closures (Plain Copy Capture, Resource Move Capture, Fn Coercion)
// -----------------------------------------------------------------------------

func TestClosuresAndResourceCaptures(t *testing.T) {
	src := `
import "std/math" as math

bind double_it = (x: int) int: { x * 2 }

bind apply_int = (f: (int) int, v: int) int: {
    f(v)
}

bind make_greeter = (prefix: own string) (string) string: {
    // Captures Resource 'prefix' by move into heap closure environment;
    // automatically freed when the closure value itself is dropped!
    (name: string) string: {
        "$prefix, $name!"
    }
}

bind main = (): {
    // 1. Coercing static 'bind' function into closure value
    assert(apply_int(double_it, 21) == 42)

    // 2. Capturing Plain value by copy & returning closure
    let factor = 10
    let mul = (x: int) int: { x * factor }
    factor = 999 // Plain capture copied 10 at creation time!
    assert(apply_int(mul, 7) == 70)

    // 3. Capturing Resource ('string') into returned heap closure + cloning closure
    let greet = make_greeter(own "Welcome".copy())
    let greet_clone = greet.copy()
    assert(greet("Ada") == "Welcome, Ada!")
    assert(greet_clone("Grace") == "Welcome, Grace!")

    // 4. Exercise 'std/math'
    let hyp = math.hypot(3.0, 4.0)
    assert(math.abs_float(hyp - 5.0) < 0.000001)
    assert(math.clamp_int(150, 0, 100) == 100)
    let rad = math.deg_to_rad(180.0)
    assert(math.abs_float(rad - math.PI) < 0.000001)

    println("closures_math_ok")
}
`
	out := runInlineStrict(t, "closures_and_math", src, true)
	if strings.TrimSpace(out) != "closures_math_ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

// -----------------------------------------------------------------------------
// 6. Negative Compile-Time Safety Verification Suite
// -----------------------------------------------------------------------------

func TestCompileTimeSafetyRejections(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "use_after_move_resource",
			src: `
bind consume = (s: own string): {}
bind main = (): {
    let msg = "hello".copy()
    consume(own msg)
    println(msg)
}
`,
			wantErrPart: "moved",
		},
		{
			name: "mutate_immutable_bind",
			src: `
bind main = (): {
    bind x = 10
    x = 20
}
`,
			wantErrPart: "immutable",
		},
		{
			name: "mutable_borrow_aliasing_conflict",
			src: `
bind swap_ints = (a: &int, b: &int): {
    let t = a
    a = b
    b = t
}
bind main = (): {
    let v = 1
    swap_ints(&v, &v)
}
`,
			wantErrPart: "borrow",
		},
		{
			name: "missing_amp_at_mut_call_site",
			src: `
bind bump = (x: &int): { x += 1 }
bind main = (): {
    let n = 5
    bump(n)
}
`,
			wantErrPart: "requires `&`",
		},
		{
			name: "optional_identifier_missing_question_mark",
			src: `
bind main = (): {
    let bad: ?int = 5
}
`,
			wantErrPart: "must end with `?`",
		},
		{
			name: "arena_ref_escapes_inner_scope",
			src: `
define Node struct [val: int]
bind main = (): {
    let outer = Arena.new()
    let escaped = outer.alloc(Node[val: 1])
    {
        let inner = Arena.new()
        escaped = inner.alloc(Node[val: 2])
    }
}
`,
			wantErrPart: "escape",
		},
		{
			name: "raw_pointer_deref_outside_unsafe",
			src: `
bind read_ptr = (p: ptr[int]) int: {
    *p
}
bind main = (): {}
`,
			wantErrPart: "unsafe",
		},
		{
			name: "non_exhaustive_match_missing_else",
			src: `
define Color enum [Red, Green, Blue]
bind main = (): {
    let c = Color.Red
    let x = match (c):
        when Color.Red: 1
        when Color.Green: 2
    println(x)
}
`,
			wantErrPart: "exhaustive",
		},
	}

	root := projectRoot(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := driver.Check(driver.Options{
				EntryPath:   filepath.Join(root, tc.name+".ll"),
				SrcOverride: tc.src,
				ProjectRoot: root,
			})
			if err == nil {
				t.Fatalf("expected compile error containing %q, but check succeeded", tc.wantErrPart)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErrPart)) {
				t.Fatalf("expected error containing %q, got:\n%s", tc.wantErrPart, err.Error())
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 7. Debug Mode Bounds Panic Verification
// -----------------------------------------------------------------------------

func TestRuntimeBoundsPanicInDebug(t *testing.T) {
	root := projectRoot(t)
	src := `
bind main = (): {
    let xs = [10, 20, 30]
    let boom = xs[5]
    println(boom)
}
`
	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		t.Run(string(backend), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			rc, err := driver.Run(driver.Options{
				EntryPath:   filepath.Join(t.TempDir(), "bounds_panic.ll"),
				SrcOverride: src,
				ProjectRoot: root,
				Backend:     backend,
				Release:     false,
			}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("[%s] unexpected build error: %v", backend, err)
			}
			if rc == 0 {
				t.Fatalf("[%s] expected non-zero exit code on out-of-bounds index, got 0", backend)
			}
			if !strings.Contains(stderr.String(), "out of bounds") {
				t.Fatalf("[%s] expected bounds panic message on stderr, got: %q", backend, stderr.String())
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 8. CLI Binary Subcommands (`version`, `check`, `emit-c`, `build`, `run`)
// -----------------------------------------------------------------------------

func TestCLISubcommands(t *testing.T) {
	root := projectRoot(t)
	tmp := t.TempDir()
	cliBin := filepath.Join(tmp, "lowlight")

	buildCmd := exec.Command("go", "build", "-o", cliBin, "./cmd/lowlight")
	buildCmd.Dir = root
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build cmd/lowlight: %v\n%s", err, string(out))
	}

	// 1. `version`
	verOut, err := exec.Command(cliBin, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(verOut), "1.0.0") {
		t.Fatalf("unexpected `lowlight version` result: err=%v out=%q", err, string(verOut))
	}

	// 2. Write a small program using `args()`
	progPath := filepath.Join(tmp, "cli_prog.ll")
	progSrc := `
bind main = () int: {
    let a = args()
    println("argc=${a.len()} arg1=${a[1]}")
    0
}
`
	if err := os.WriteFile(progPath, []byte(progSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. `check`
	if out, err := exec.Command(cliBin, "check", progPath).CombinedOutput(); err != nil {
		t.Fatalf("`lowlight check` failed: %v\n%s", err, string(out))
	}

	// 4. `emit-c`
	cPath := filepath.Join(tmp, "cli_prog.c")
	if out, err := exec.Command(cliBin, "emit-c", "-o", cPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("`lowlight emit-c` failed: %v\n%s", err, string(out))
	}
	cBytes, err := os.ReadFile(cPath)
	if err != nil || !strings.Contains(string(cBytes), "int main(int argc, char** argv)") {
		t.Fatalf("expected emitted C11 main in %s", cPath)
	}

	// 5. `build` + execute (both default LLVM and --backend c11)
	for _, backend := range []string{"", "--backend=c11", "--backend=llvm"} {
		appBin := filepath.Join(tmp, "cli_prog_bin_"+backend)
		buildArgs := []string{"build", "-o", appBin}
		if backend != "" {
			buildArgs = append(buildArgs, backend)
		}
		buildArgs = append(buildArgs, progPath)
		if out, err := exec.Command(cliBin, buildArgs...).CombinedOutput(); err != nil {
			t.Fatalf("`lowlight build %s` failed: %v\n%s", backend, err, string(out))
		}
		appOut, err := exec.Command(appBin, "hello-arg").CombinedOutput()
		if err != nil || strings.TrimSpace(string(appOut)) != "argc=2 arg1=hello-arg" {
			t.Fatalf("built binary [%s] output mismatch: err=%v out=%q", backend, err, string(appOut))
		}
	}

	// 6. `run` with forwarded arguments (both default LLVM and --backend c11)
	for _, backend := range []string{"", "--backend=c11", "--backend=llvm"} {
		runArgs := []string{"run"}
		if backend != "" {
			runArgs = append(runArgs, backend)
		}
		runArgs = append(runArgs, progPath, "--", "world-arg")
		runCmd := exec.Command(cliBin, runArgs...)
		runCmd.Env = append(os.Environ(), "LOWLIGHT_CHECK_LEAKS=1")
		runOut, err := runCmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(runOut)) != "argc=2 arg1=world-arg" {
			t.Fatalf("`lowlight run %s` output mismatch: err=%v out=%q", backend, err, string(runOut))
		}
	}
}

// -----------------------------------------------------------------------------
// 9. Standard Library `std/http.ll` & `std/net.ll`
// -----------------------------------------------------------------------------

func TestStdHttpPackage(t *testing.T) {
	root := projectRoot(t)
	httpTestPath := filepath.Join(root, "tests", "http_test.ll")
	httpTestBytes, err := os.ReadFile(httpTestPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", httpTestPath, err)
	}

	for _, release := range []bool{false, true} {
		out := runInlineStrict(t, "http_test", string(httpTestBytes), release)
		if !strings.Contains(out, "All 7 std/http test suites passed with 0 leaks!") {
			t.Fatalf("[release=%v] expected test success message, got:\n%s", release, out)
		}
	}

	examplePath := filepath.Join(root, "examples", "http_server_and_client.ll")
	exampleBytes, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", examplePath, err)
	}
	outEx := runInlineStrict(t, "http_showcase", string(exampleBytes), false)
	if !strings.Contains(outEx, "All HTTP client and server showcase demonstrations completed successfully!") {
		t.Fatalf("showcase output mismatch:\n%s", outEx)
	}
}

// -----------------------------------------------------------------------------
// 10. Standalone `.ll` Test Files & Network Showcase Build/Check Coverage
// -----------------------------------------------------------------------------

func TestOrphanedLLTestFiles(t *testing.T) {
	root := projectRoot(t)

	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		t.Run("backtick_test_"+string(backend), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			rc, err := driver.Test(driver.Options{
				Targets:     []string{filepath.Join(root, "tests", "backtick_test.ll")},
				ProjectRoot: root,
				Backend:     backend,
				ExtraCFlags: strictCFlags,
				Env: []string{
					"LOWLIGHT_CHECK_LEAKS=1",
					"ASAN_OPTIONS=detect_leaks=0",
				},
			}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("backtick_test.ll (%s) failed: %v\nstdout:\n%s\nstderr:\n%s", backend, err, stdout.String(), stderr.String())
			}
			if rc != 0 {
				t.Fatalf("backtick_test.ll (%s) exited with %d\nstdout:\n%s\nstderr:\n%s", backend, rc, stdout.String(), stderr.String())
			}
		})
	}

	mainTests := []string{
		"core_test.ll",
		"strconv_strfmt_test.ll",
		"time_formatting_test.ll",
		"unsafe_expr_test.ll",
	}
	for _, name := range mainTests {
		srcBytes, err := os.ReadFile(filepath.Join(root, "tests", name))
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		for _, release := range []bool{false, true} {
			mode := "debug"
			if release {
				mode = "release"
			}
			t.Run(name+"_"+mode, func(t *testing.T) {
				out := runInlineStrict(t, strings.TrimSuffix(name, ".ll")+"_"+mode, string(srcBytes), release)
				if strings.TrimSpace(out) == "" {
					t.Fatalf("%s (%s) produced empty stdout", name, mode)
				}
			})
		}
	}
}

func TestNetworkExamplesBuildClean(t *testing.T) {
	root := projectRoot(t)
	examples := []string{
		"http_get_client.ll",
		"http_server.ll",
		"tcp_echo_server.ll",
	}

	for _, base := range examples {
		file := filepath.Join(root, "examples", base)
		t.Run(base+"_check", func(t *testing.T) {
			_, warnings, err := driver.Check(driver.Options{
				EntryPath:   file,
				ProjectRoot: root,
			})
			if err != nil {
				t.Fatalf("%s check failed:\n%v", base, err)
			}
			if strings.TrimSpace(warnings) != "" {
				t.Fatalf("%s emitted unexpected warnings:\n%s", base, warnings)
			}
		})
		for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
			t.Run(base+"_build_"+string(backend), func(t *testing.T) {
				outBin := filepath.Join(t.TempDir(), strings.TrimSuffix(base, ".ll"))
				_, err := driver.Build(driver.Options{
					EntryPath:   file,
					ProjectRoot: root,
					Backend:     backend,
					OutPath:     outBin,
					ExtraCFlags: strictCFlags,
				})
				if err != nil {
					t.Fatalf("%s (%s) build failed:\n%v", base, backend, err)
				}
			})
		}
	}
}
