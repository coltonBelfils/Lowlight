package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	t.Parallel()
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
					t.Parallel()
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
	t.Parallel()
	src := `
@derive(Equatable, Printable)
define Color enum [Red, Green, Blue]
define UserId int

bind UserId.is_admin = fn(self) bool: {
    int(self) == 1
}

// Order-independent top-level mutual recursion with expression bodies
bind is_even = fn(n: int) bool: if (n == 0): true else: is_odd(n - 1)bind is_odd  = fn(n: int) bool: if (n == 0): false else: is_even(n - 1)
bind main = fn(): {
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
	t.Parallel()
	src := `
bind sum_slice = fn(xs: [int]) int: {
    let total = 0
    for (x in xs): {
        total += x
    }
    total
}

bind main = fn(): {
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
	t.Parallel()
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

bind Account.bonus = fn(self) int: {
    (self.profile? ?.score ?? 0) + 10
}

bind parse_pair = fn(a: string, b: string) ?int: {
    let x = parse_int(a) ?? func <- none
    let y = parse_int(b) ?? func <- none
    x + y
}

bind main = fn(): {
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
    let wres! = fs.write_text(p, "line1\nline2")
    assert(wres!.is_ok())
    assert(fs.exists(p))

    let body = fs.read_text(p): default panic
    assert(body == "line1\nline2")
    fs.remove_file(p): default panic
    assert(not fs.exists(p))

    println("optionals_fs_ok")
}
`
	out := runInlineStrict(t, "optionals_and_fs", src, false)
	if strings.TrimSpace(out) != "optionals_fs_ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCoalesceChainsAndUnwindE2E(t *testing.T) {
	t.Parallel()
	src := `
bind make_pair = fn(flag: int) ?+[int, string]: {
    if (flag == 1): +[10, "one".copy()]
    else: none
}

bind make_user_opt = fn(ok: bool, name: string) ?string: {
    if (ok): name.copy()
    else: none
}

bind resolve_num = fn(a?: ?int, b?: ?int) int: {
    let v = a? ?? b? ?? func <- -1
    v * 2
}

bind resolve_str = fn(a?: own ?string, b?: own ?string) string: {
    let keep = "kept".copy()
    let res = a? ?? b? ?? func <- keep
    "${res}:${keep}"
}

bind resolve_str_rval = fn(ok1: bool, ok2: bool) string: {
    let keep = "kept".copy()
    let res = make_user_opt(ok1, "r1") ?? make_user_opt(ok2, "r2") ?? func <- keep
    "${res}:${keep}"
}

bind resolve_unpack = fn(f1: int, f2: int) int: {
    let n, s = unpack make_pair(f1) ?? make_pair(f2) ?? func <- -99
    let mut_n = 0
    let m1?: ?int = if (f1 == 1): 5 else: none
    let m2?: ?int = if (f2 == 1): 7 else: none
    mut_n = m1? ?? m2? ?? func <- -88
    n + s.len() + mut_n
}

bind resolve_assign = fn(ok1: bool, ok2: bool) int: {
    let mut_n = 0
    let m1?: ?int = if (ok1): 5 else: none
    let m2?: ?int = if (ok2): 7 else: none
    mut_n = m1? ?? m2? ?? func <- -88
    mut_n * 10
}

bind main = fn(): {
    let n1?: ?int = 3
    let n2?: ?int = 4
    let no_n?: ?int = none
    let c = n1? ?? n2? ?? 99
    let c2 = no_n? ?? n2? ?? 99
    let c3 = no_n? ?? no_n? ?? 99
    let c4 = n1? ?? none ?? 99
    assert(c == 3 and c2 == 4 and c3 == 99 and c4 == 3)

    assert(resolve_num(3, 4) == 6)
    assert(resolve_num(none, 4) == 8)
    assert(resolve_num(none, none) == -1)

    let s1?: ?string = make_user_opt(true, "alpha")
    let s2?: ?string = make_user_opt(true, "beta")
    assert(resolve_str(own s1?, own s2?) == "alpha:kept")
    assert(resolve_str(none, make_user_opt(true, "beta")) == "beta:kept")
    assert(resolve_str(none, none) == "kept")

    assert(resolve_str_rval(true, true) == "r1:kept")
    assert(resolve_str_rval(false, true) == "r2:kept")
    assert(resolve_str_rval(false, false) == "kept")

    assert(resolve_unpack(1, 0) == 18)
    assert(resolve_unpack(0, 1) == 20)
    assert(resolve_unpack(0, 0) == -99)

    assert(resolve_assign(true, false) == 50)
    assert(resolve_assign(false, true) == 70)
    assert(resolve_assign(false, false) == -88)

    println("coalesce_chains_ok")
}
`
	out := runInlineStrict(t, "coalesce_chains", src, false)
	if strings.TrimSpace(out) != "coalesce_chains_ok" {
		t.Fatalf("unexpected output: %q", out)
	}
	outRel := runInlineStrict(t, "coalesce_chains_release", src, true)
	if strings.TrimSpace(outRel) != "coalesce_chains_ok" {
		t.Fatalf("unexpected release output: %q", outRel)
	}
}

// -----------------------------------------------------------------------------
// 5. First-Class Closures (Plain Copy Capture, Resource Move Capture, Fn Coercion)
// -----------------------------------------------------------------------------

func TestClosuresAndResourceCaptures(t *testing.T) {
	t.Parallel()
	src := `
import "std/math" as math

bind double_it = fn(x: int) int: { x * 2 }

bind apply_int = fn(f: cl(int) int, v: int) int: {
    f(v)
}

bind make_greeter = fn(prefix: own string) cl(string) string: {
    // Captures Resource 'prefix' by move into heap closure environment;
    // automatically freed when the closure value itself is dropped!
    cl(name: string) string: {
        "$prefix, $name!"
    }
}

bind main = fn(): {
    // 1. Coercing static 'bind' function into closure value
    assert(apply_int(double_it, 21) == 42)

    // 2. Capturing Plain value by copy & returning closure
    let factor = 10
    let mul = cl(x: int) int: { x * factor }
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
	t.Parallel()
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "use_after_move_resource",
			src: `
bind consume = fn(s: own string): {}
bind main = fn(): {
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
bind main = fn(): {
    bind x = 10
    x = 20
}
`,
			wantErrPart: "immutable",
		},
		{
			name: "mutable_borrow_aliasing_conflict",
			src: `
bind swap_ints = fn(a: &int, b: &int): {
    let t = a
    a = b
    b = t
}
bind main = fn(): {
    let v = 1
    swap_ints(&v, &v)
}
`,
			wantErrPart: "borrow",
		},
		{
			name: "missing_amp_at_mut_call_site",
			src: `
bind bump = fn(x: &int): { x += 1 }
bind main = fn(): {
    let n = 5
    bump(n)
}
`,
			wantErrPart: "requires `&`",
		},
		{
			name: "optional_identifier_missing_question_mark",
			src: `
bind main = fn(): {
    let bad: ?int = 5
}
`,
			wantErrPart: "must end with `?`",
		},
		{
			name: "arena_ref_escapes_inner_scope",
			src: `
define Node struct [val: int]
bind main = fn(): {
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
bind read_ptr = fn(p: ptr[int]) int: {
    *p
}
bind main = fn(): {}
`,
			wantErrPart: "unsafe",
		},
		{
			name: "non_exhaustive_match_missing_else",
			src: `
define Color enum [Red, Green, Blue]
bind main = fn(): {
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
	t.Parallel()
	root := projectRoot(t)
	src := `
bind main = fn(): {
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
	t.Parallel()
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
bind main = fn() int: {
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
	t.Parallel()
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
	t.Parallel()
	root := projectRoot(t)

	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		t.Run("backtick_test_"+string(backend), func(t *testing.T) {
			t.Parallel()
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
				t.Parallel()
				out := runInlineStrict(t, strings.TrimSuffix(name, ".ll")+"_"+mode, string(srcBytes), release)
				if strings.TrimSpace(out) == "" {
					t.Fatalf("%s (%s) produced empty stdout", name, mode)
				}
			})
		}
	}
}

func TestNetworkExamplesBuildClean(t *testing.T) {
	t.Parallel()
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

func resolveStrictCC() string {
	cc := driver.ResolveCC("")
	if cc == "cc" {
		if clangPath, err := exec.LookPath("clang"); err == nil && clangPath != "" {
			return clangPath
		}
	}
	return cc
}

func TestFormatLocHandlesLongPathWithoutTruncation(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	llSrc := `
@track_caller
bind get_loc = fn() string: caller_location()

bind main = fn(): {
    println(get_loc())
}
`
	entry := filepath.Join(t.TempDir(), "format_loc.ll")
	opts := driver.Options{
		EntryPath:   entry,
		SrcOverride: llSrc,
		ProjectRoot: root,
	}

	cSrc, _, err := driver.EmitC(opts)
	if err != nil {
		t.Fatalf("EmitC failed: %v", err)
	}
	llvmSrc, _, err := driver.EmitLLVM(opts)
	if err != nil {
		t.Fatalf("EmitLLVM failed: %v", err)
	}

	cc := resolveStrictCC()

	sharedChecksC := `
static int run_format_loc_checks(void) {
    char a600[601];
    memset(a600, 'a', 600);
    a600[600] = '\0';

    char b510[511];
    memset(b510, 'b', 510);
    b510[510] = '\0';

    char buf[700];

    int64_t n1 = test_format_loc_case(a600, 600, 12, 5, buf);
    if (n1 != 605 || memcmp(buf, a600, 600) != 0 || memcmp(buf + 600, ":12:5", 5) != 0) {
        fprintf(stderr, "s1 mismatch: len=%lld\n", (long long)n1);
        return 1;
    }

    int64_t n2 = test_format_loc_case(a600, 600, 12, 0, buf);
    if (n2 != 603 || memcmp(buf, a600, 600) != 0 || memcmp(buf + 600, ":12", 3) != 0) {
        fprintf(stderr, "s2 mismatch: len=%lld\n", (long long)n2);
        return 2;
    }

    int64_t n3 = test_format_loc_case(b510, 510, 1, 2, buf);
    if (n3 != 514 || memcmp(buf, b510, 510) != 0 || memcmp(buf + 510, ":1:2", 4) != 0) {
        fprintf(stderr, "s3 mismatch: len=%lld\n", (long long)n3);
        return 3;
    }

    int64_t n4 = test_format_loc_case("main.ll", 7, 12, 5, buf);
    if (n4 != 12 || memcmp(buf, "main.ll:12:5", 12) != 0) {
        fprintf(stderr, "s4 mismatch: len=%lld\n", (long long)n4);
        return 4;
    }

    int64_t n5 = test_format_loc_case("main.ll", 7, 12, 0, buf);
    if (n5 != 10 || memcmp(buf, "main.ll:12", 10) != 0) {
        fprintf(stderr, "s5 mismatch: len=%lld\n", (long long)n5);
        return 5;
    }

    return 0;
}
`

	cMainHarness := `
static int64_t test_format_loc_case(const char* data, int64_t len, int64_t line, int64_t col, char* out_buf) {
    ll_string s = ll_fn__format_loc(ll_str_lit(data, len), line, col);
    int64_t rlen = s.len;
    int64_t cp = rlen > 700 ? 700 : rlen;
    if (cp > 0) memcpy(out_buf, s.data, (size_t)cp);
    ll_drop__string(&s);
    return rlen;
}
` + sharedChecksC + `
int main(void) {
    int rc = run_format_loc_checks();
    if (rc != 0) return rc;
    if (ll_rt_live_allocs != 0) {
        fprintf(stderr, "leak count=%lld\n", (long long)ll_rt_live_allocs);
        return 6;
    }
    return 0;
}
`

	t.Run("C11", func(t *testing.T) {
		tmp := t.TempDir()
		patchedC := strings.Replace(cSrc, "int main(int argc, char** argv)", "int ll_unused_main(int argc, char** argv)", 1)
		if patchedC == cSrc {
			t.Fatal("failed to locate C11 main entrypoint in emitted C source")
		}
		patchedC += cMainHarness
		cPath := filepath.Join(tmp, "loc_c11.c")
		binPath := filepath.Join(tmp, "loc_c11_bin")
		if err := os.WriteFile(cPath, []byte(patchedC), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"-std=c11", "-g", "-O0", "-o", binPath, cPath}, strictCFlags...)
		args = append(args, "-lm", "-ldl")
		if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("C11 compile failed: %v\n%s", err, string(out))
		}
		runCmd := exec.Command(binPath)
		runCmd.Env = append(os.Environ(), "LOWLIGHT_CHECK_LEAKS=1", "ASAN_OPTIONS=detect_leaks=0")
		if out, err := runCmd.CombinedOutput(); err != nil {
			t.Fatalf("C11 run failed: %v\n%s", err, string(out))
		}
	})

	t.Run("LLVM", func(t *testing.T) {
		tmp := t.TempDir()
		patchedLL := strings.Replace(llvmSrc, "define i32 @main(i32 %argc, ptr %argv)", "define i32 @ll_unused_main(i32 %argc, ptr %argv)", 1)
		if patchedLL == llvmSrc {
			t.Fatal("failed to locate LLVM main entrypoint in emitted IR")
		}
		if !strings.Contains(patchedLL, "@llvm.memcpy.p0.p0.i64") {
			patchedLL += "\ndeclare void @llvm.memcpy.p0.p0.i64(ptr, ptr, i64, i1)\n"
		}
		patchedLL += `
define i64 @test_format_loc_case(ptr %data, i64 %len, i64 %line, i64 %col, ptr %out_buf) {
entry:
    %in_s0 = insertvalue %struct.ll_string zeroinitializer, ptr %data, 0
    %in_s1 = insertvalue %struct.ll_string %in_s0, i64 %len, 1
    %res = call %struct.ll_string @ll_fn__format_loc(%struct.ll_string %in_s1, i64 %line, i64 %col)
    %r_data = extractvalue %struct.ll_string %res, 0
    %r_len = extractvalue %struct.ll_string %res, 1
    %over = icmp sgt i64 %r_len, 700
    %cp_len = select i1 %over, i64 700, i64 %r_len
    %pos = icmp sgt i64 %cp_len, 0
    br i1 %pos, label %do_cp, label %do_drop
do_cp:
    call void @llvm.memcpy.p0.p0.i64(ptr %out_buf, ptr %r_data, i64 %cp_len, i1 false)
    br label %do_drop
do_drop:
    %slot = alloca %struct.ll_string, align 8
    store %struct.ll_string %res, ptr %slot, align 8
    call void @ll_drop__string(ptr %slot)
    ret i64 %r_len
}
`
		cDriver := `
#include <stdint.h>
#include <stdio.h>
#include <string.h>

extern int64_t test_format_loc_case(const char* data, int64_t len, int64_t line, int64_t col, char* out_buf);
` + sharedChecksC + `
int main(void) {
    return run_format_loc_checks();
}
`
		llPath := filepath.Join(tmp, "loc_llvm.ll")
		drvPath := filepath.Join(tmp, "loc_driver.c")
		binPath := filepath.Join(tmp, "loc_llvm_bin")
		if err := os.WriteFile(llPath, []byte(patchedLL), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(drvPath, []byte(cDriver), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"-Wno-override-module", "-g", "-O0", "-o", binPath, llPath, drvPath}, strictCFlags...)
		args = append(args, "-lm", "-ldl")
		if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("LLVM compile failed: %v\n%s", err, string(out))
		}
		runCmd := exec.Command(binPath)
		runCmd.Env = append(os.Environ(), "LOWLIGHT_CHECK_LEAKS=1", "ASAN_OPTIONS=detect_leaks=0")
		if out, err := runCmd.CombinedOutput(); err != nil {
			t.Fatalf("LLVM run failed: %v\n%s", err, string(out))
		}
	})
}

func TestSocketSendAfterPeerCloseNoSigpipe(t *testing.T) {
	t.Parallel()
	src := `import "std/net"

bind main = fn(): {
    let p1 = net.socketpair().unwrap()
    p1.second.close()
    assert(p1.first.send("hello after second closed").is_err(), "expected send after peer close to return Err")
    p1.first.close()

    let p2 = net.socketpair().unwrap()
    p2.first.close()
    assert(p2.second.send("hello after first closed").is_err(), "expected reverse send after peer close to return Err")
    p2.second.close()

    println("no_sigpipe_ok")
}
`
	for _, release := range []bool{false, true} {
		out := runInlineStrict(t, "socket_no_sigpipe", src, release)
		if strings.TrimSpace(out) != "no_sigpipe_ok" {
			t.Fatalf("[release=%v] unexpected stdout: %q", release, out)
		}
	}

	// Also verify std/net raw_send_all itself prevents SIGPIPE on an unconfigured raw socketpair fd
	// (e.g. from external C / extern calls) on both C11 and LLVM backends.
	root := projectRoot(t)
	opts := driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "raw_sigpipe.ll"),
		SrcOverride: src,
		ProjectRoot: root,
	}
	cSrc, _, err := driver.EmitC(opts)
	if err != nil {
		t.Fatalf("EmitC failed: %v", err)
	}
	llvmSrc, _, err := driver.EmitLLVM(opts)
	if err != nil {
		t.Fatalf("EmitLLVM failed: %v", err)
	}
	cc := resolveStrictCC()

	t.Run("RawFD_C11", func(t *testing.T) {
		tmp := t.TempDir()
		rawSendRe := regexp.MustCompile(`\b(ll_fn_[A-Za-z0-9_]*raw_send_all)\(`)
		m := rawSendRe.FindStringSubmatch(cSrc)
		if len(m) < 2 {
			t.Fatal("failed to locate C11 raw_send_all function")
		}
		rawSendSym := m[1]
		patchedC := strings.Replace(cSrc, "int main(int argc, char** argv)", "int ll_unused_main(int argc, char** argv)", 1)
		if patchedC == cSrc {
			t.Fatal("failed to locate C11 main entrypoint")
		}
		patchedC += fmt.Sprintf(`
int main(void) {
    int fds[2];
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds) != 0) return 1;
    close(fds[1]);
    int64_t rc = %s((int64_t)fds[0], (uint8_t*)"raw_sigpipe", 11);
    close(fds[0]);
    return (rc == -1LL) ? 0 : 2;
}
`, rawSendSym)
		cPath := filepath.Join(tmp, "raw_sigpipe.c")
		binPath := filepath.Join(tmp, "raw_sigpipe_c11")
		if err := os.WriteFile(cPath, []byte(patchedC), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"-std=c11", "-g", "-O0", "-o", binPath, cPath}, strictCFlags...)
		args = append(args, "-lm", "-ldl")
		if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("RawFD C11 compile failed: %v\n%s", err, string(out))
		}
		if out, err := exec.Command(binPath).CombinedOutput(); err != nil {
			t.Fatalf("RawFD C11 run failed: %v\n%s", err, string(out))
		}
	})

	t.Run("RawFD_LLVM", func(t *testing.T) {
		tmp := t.TempDir()
		rawSendRe := regexp.MustCompile(`define i64 @(ll_fn_[A-Za-z0-9_]*raw_send_all)\(`)
		m := rawSendRe.FindStringSubmatch(llvmSrc)
		if len(m) < 2 {
			t.Fatal("failed to locate LLVM raw_send_all definition")
		}
		rawSendSym := m[1]
		patchedLL := strings.Replace(llvmSrc, "define i32 @main(i32 %argc, ptr %argv)", "define i32 @ll_unused_main(i32 %argc, ptr %argv)", 1)
		if patchedLL == llvmSrc {
			t.Fatal("failed to locate LLVM main entrypoint")
		}
		patchedLL += fmt.Sprintf(`
define i64 @test_raw_send_all(i64 %%fd, ptr %%data, i64 %%len) {
entry:
    %%rc = call i64 @%s(i64 %%fd, ptr %%data, i64 %%len)
    ret i64 %%rc
}
`, rawSendSym)
		cDriver := `
#include <stdint.h>
#include <sys/socket.h>
#include <unistd.h>

extern int64_t test_raw_send_all(int64_t fd, const uint8_t* data, int64_t len);

int main(void) {
    int fds[2];
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds) != 0) return 1;
    close(fds[1]);
    int64_t rc = test_raw_send_all((int64_t)fds[0], (const uint8_t*)"raw_sigpipe", 11);
    close(fds[0]);
    return (rc == -1LL) ? 0 : 2;
}
`
		llPath := filepath.Join(tmp, "raw_sigpipe.ll")
		drvPath := filepath.Join(tmp, "raw_sigpipe_drv.c")
		binPath := filepath.Join(tmp, "raw_sigpipe_llvm")
		if err := os.WriteFile(llPath, []byte(patchedLL), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(drvPath, []byte(cDriver), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"-Wno-override-module", "-g", "-O0", "-o", binPath, llPath, drvPath}, strictCFlags...)
		args = append(args, "-lm", "-ldl")
		if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("RawFD LLVM compile failed: %v\n%s", err, string(out))
		}
		if out, err := exec.Command(binPath).CombinedOutput(); err != nil {
			t.Fatalf("RawFD LLVM run failed: %v\n%s", err, string(out))
		}
	})
}

func TestLLVMTlsInitClosesHandleOnDlsymFailure(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	src := `
bind main = fn(): {
    let a = unsafe sys_tls_available()
    println("$a")
}
`
	llvmSrc, _, err := driver.EmitLLVM(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "tls_dlclose.ll"),
		SrcOverride: src,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("EmitLLVM failed: %v", err)
	}

	patchedLL := strings.Replace(llvmSrc, "define i32 @main(i32 %argc, ptr %argv)", "define i32 @ll_unused_main(i32 %argc, ptr %argv)", 1)
	if patchedLL == llvmSrc {
		t.Fatal("failed to locate LLVM main entrypoint")
	}
	for _, pair := range [][2]string{
		{"call ptr @dlopen(", "call ptr @mock_dlopen("},
		{"call ptr @dlsym(", "call ptr @mock_dlsym("},
		{"call i32 @dlclose(", "call i32 @mock_dlclose("},
	} {
		replaced := strings.ReplaceAll(patchedLL, pair[0], pair[1])
		if replaced == patchedLL {
			t.Fatalf("expected emitted LLVM IR to contain %q", pair[0])
		}
		patchedLL = replaced
	}
	patchedLL += `
declare ptr @mock_dlopen(ptr, i32)
declare ptr @mock_dlsym(ptr, ptr)
declare i32 @mock_dlclose(ptr)

define i64 @test_call_tls_available() {
entry:
    %r = call i64 @ll_tls_available()
    ret i64 %r
}
`
	cDriver := `
#include <stdint.h>
#include <stdio.h>
#include <string.h>

extern int64_t test_call_tls_available(void);

static const char* g_fail_sym = "";
static int g_open_count = 0;
static int g_close_count = 0;
static void* const g_dummy_handle = (void*)(uintptr_t)0x1234;
static void* g_closed_handle = NULL;

static int mock_openssl_init(uint64_t opts, const void* settings) {
    (void)opts;
    (void)settings;
    return 1;
}

static void dummy_fn(void) {}

void* mock_dlopen(const char* path, int mode) {
    (void)path;
    (void)mode;
    g_open_count++;
    return g_dummy_handle;
}

void* mock_dlsym(void* handle, const char* sym) {
    if (handle != g_dummy_handle || !sym) return NULL;
    if (strcmp(sym, g_fail_sym) == 0) return NULL;
    if (strcmp(sym, "OPENSSL_init_ssl") == 0) return (void*)mock_openssl_init;
    return (void*)dummy_fn;
}

int mock_dlclose(void* handle) {
    g_close_count++;
    g_closed_handle = handle;
    return 0;
}

int main(int argc, char** argv) {
    if (argc < 2) return 10;
    g_fail_sym = argv[1];
    int64_t avail = test_call_tls_available();
    int expect_avail = (strcmp(g_fail_sym, "NONE") == 0) ? 1 : 0;
    int expect_close = (expect_avail == 1) ? 0 : 1;
    if (avail != expect_avail || g_open_count != 1 || g_close_count != expect_close) {
        fprintf(stderr, "fail_sym=%s avail=%lld open=%d close=%d\n",
                g_fail_sym, (long long)avail, g_open_count, g_close_count);
        return 1;
    }
    if (expect_close == 1 && g_closed_handle != g_dummy_handle) {
        fprintf(stderr, "wrong handle closed: %p vs %p\n", g_closed_handle, g_dummy_handle);
        return 2;
    }
    return 0;
}
`

	tmp := t.TempDir()
	llPath := filepath.Join(tmp, "tls_dlclose.ll")
	drvPath := filepath.Join(tmp, "tls_dlclose_drv.c")
	binPath := filepath.Join(tmp, "tls_dlclose_bin")
	if err := os.WriteFile(llPath, []byte(patchedLL), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drvPath, []byte(cDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	cc := resolveStrictCC()
	args := append([]string{"-Wno-override-module", "-g", "-O0", "-o", binPath, llPath, drvPath}, strictCFlags...)
	args = append(args, "-lm", "-ldl")
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile failed: %v\n%s", err, string(out))
	}

	for _, sym := range []string{"SSL_connect", "SSL_free", "SSL_CTX_free", "SSL_set_fd", "NONE"} {
		t.Run("missing_"+sym, func(t *testing.T) {
			if out, err := exec.Command(binPath, sym).CombinedOutput(); err != nil {
				t.Fatalf("run(%s) failed: %v\n%s", sym, err, string(out))
			}
		})
	}
}
