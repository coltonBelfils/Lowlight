package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// 1. Multi-file directory-as-package sharing: sibling mutual recursion, private `_` sharing,
// and duplicate top-level declaration detection across sibling files (`SPECIFICATION.md §4.1`).
func TestPackageDirectoryMultiFileAndMutualRecursion(t *testing.T) {
	tmp := t.TempDir()
	root := projectRoot(t)

	writeTestFile(t, tmp, "lowlight.toml", `
[package]
name = "myapp"
version = "0.1.0"
`)
	writeTestFile(t, tmp, "parity/even.ll", `
bind is_even = (n: int) bool: {
	if (n == 0): true
	else: _is_odd(n - 1)
}
`)
	writeTestFile(t, tmp, "parity/odd.ll", `
bind _is_odd = (n: int) bool: {
	if (n == 0): false
	else: is_even(n - 1)
}

bind is_odd = (n: int) bool: _is_odd(n)
`)
	writeTestFile(t, tmp, "main.ll", `
import "myapp/parity"

bind main = (): {
	assert(parity.is_even(10))
	assert(not parity.is_even(7))
	assert(parity.is_odd(9))
	println("parity_ok")
}
`)

	out := runFileStrict(t, "pkg_multifile_mutual", tmp, false)
	if strings.TrimSpace(out) != "parity_ok" {
		t.Fatalf("unexpected stdout: %q", out)
	}

	// Duplicate top-level declaration across two sibling files in the same package directory must fail.
	dupDir := t.TempDir()
	writeTestFile(t, dupDir, "lowlight.toml", "[package]\nname = \"dupapp\"\n")
	writeTestFile(t, dupDir, "a.ll", "bind shared = () int: 1\nbind main = (): {}\n")
	writeTestFile(t, dupDir, "b.ll", "bind shared = () int: 2\n")
	_, _, err := driver.Check(driver.Options{EntryPath: dupDir, ProjectRoot: root})
	if err == nil {
		t.Fatal("expected duplicate declaration error across sibling files, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate declaration") {
		t.Fatalf("expected duplicate declaration error, got: %v", err)
	}
}

// 2. Strict qualified imports, hyphen normalization, `.ll` rejection + migration hint,
// qualifier collision hint, relative import rejection inside manifests, inter-package cycles,
// and binary `main` import rejection (`SPECIFICATION.md §4.3, §4.5`).
func TestPackageStrictQualifiedImportsAndErrors(t *testing.T) {
	root := projectRoot(t)

	// 2a. Hyphen normalization (`my-util` -> `my_util`) and explicit `as` alias
	tmp := t.TempDir()
	writeTestFile(t, tmp, "lowlight.toml", "[package]\nname = \"demo\"\n")
	writeTestFile(t, tmp, "my-util/util.ll", "bind answer = () int: 42\n")
	writeTestFile(t, tmp, "main.ll", `
import "demo/my-util"

bind main = (): {
	assert(my_util.answer() == 42)
	println("hyphen_ok")
}
`)
	if out := runFileStrict(t, "pkg_hyphen_norm", tmp, false); strings.TrimSpace(out) != "hyphen_ok" {
		t.Fatalf("unexpected stdout: %q", out)
	}

	// 2b. Legacy `.ll` import rejected with migration hint
	tmpLL := t.TempDir()
	writeTestFile(t, tmpLL, "main.ll", "import \"std/math.ll\"\nbind main = (): {}\n")
	_, _, err := driver.Check(driver.Options{EntryPath: filepath.Join(tmpLL, "main.ll"), ProjectRoot: root})
	if err == nil || !strings.Contains(err.Error(), "without the `.ll` extension") || !strings.Contains(err.Error(), `import "std/math"`) {
		t.Fatalf("expected .ll migration error+hint, got: %v", err)
	}

	// 2c. Duplicate default qualifier collision error + `as` hint
	tmpCol := t.TempDir()
	writeTestFile(t, tmpCol, "lowlight.toml", "[package]\nname = \"col\"\n")
	writeTestFile(t, tmpCol, "a/util/u.ll", "bind x = () int: 1\n")
	writeTestFile(t, tmpCol, "b/util/u.ll", "bind y = () int: 2\n")
	writeTestFile(t, tmpCol, "main.ll", "import \"col/a/util\"\nimport \"col/b/util\"\nbind main = (): {}\n")
	_, _, err = driver.Check(driver.Options{EntryPath: tmpCol, ProjectRoot: root})
	if err == nil || !strings.Contains(err.Error(), "qualifier") || !strings.Contains(err.Error(), "as ") {
		t.Fatalf("expected qualifier collision diagnostic with `as` hint, got: %v", err)
	}

	// 2d. Relative `./` import inside a `lowlight.toml` project rejected
	tmpRel := t.TempDir()
	writeTestFile(t, tmpRel, "lowlight.toml", "[package]\nname = \"relproj\"\n")
	writeTestFile(t, tmpRel, "sub/s.ll", "bind x = () int: 1\n")
	writeTestFile(t, tmpRel, "main.ll", "import \"./sub\"\nbind main = (): {}\n")
	_, _, err = driver.Check(driver.Options{EntryPath: tmpRel, ProjectRoot: root})
	if err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("expected relative import rejection inside lowlight.toml, got: %v", err)
	}

	// 2e. Inter-package import cycle chain diagnostic (`cyc/a -> cyc/b -> cyc/a`)
	tmpCyc := t.TempDir()
	writeTestFile(t, tmpCyc, "lowlight.toml", "[package]\nname = \"cyc\"\n")
	writeTestFile(t, tmpCyc, "a/a.ll", "import \"cyc/b\"\nbind fa = () int: b.fb()\n")
	writeTestFile(t, tmpCyc, "b/b.ll", "import \"cyc/a\"\nbind fb = () int: a.fa()\n")
	writeTestFile(t, tmpCyc, "main.ll", "import \"cyc/a\"\nbind main = (): { println(a.fa()) }\n")
	_, _, err = driver.Check(driver.Options{EntryPath: tmpCyc, ProjectRoot: root})
	if err == nil || !strings.Contains(err.Error(), "cyclic package import") || !strings.Contains(err.Error(), "cyc/a -> cyc/b -> cyc/a") {
		t.Fatalf("expected cyclic package import chain diagnostic, got: %v", err)
	}

	// 2f. Importing a binary package that defines `main` is rejected
	tmpBin := t.TempDir()
	writeTestFile(t, tmpBin, "lowlight.toml", "[package]\nname = \"binimp\"\n")
	writeTestFile(t, tmpBin, "app/main.ll", "bind main = (): {}\nbind hi = () int: 1\n")
	writeTestFile(t, tmpBin, "main.ll", "import \"binimp/app\"\nbind main = (): { println(app.hi()) }\n")
	_, _, err = driver.Check(driver.Options{EntryPath: tmpBin, ProjectRoot: root})
	if err == nil || !strings.Contains(err.Error(), "main") {
		t.Fatalf("expected error importing binary package defining main, got: %v", err)
	}
}

// 3. Canonical PackageID symbol mangling: two identically named subpackages `a/util` and `b/util`
// with identical struct/function names compile and execute together with zero C/LLVM symbol collisions!
func TestPackageCanonicalSymbolManglingNoCollision(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, tmp, "lowlight.toml", "[package]\nname = \"mangle\"\n")
	writeTestFile(t, tmp, "a/util/util.ll", `
define Box struct [val: int]
bind make_box = (x: int) Box: Box[val: x + 10]
`)
	writeTestFile(t, tmp, "b/util/util.ll", `
define Box struct [val: int]
bind make_box = (x: int) Box: Box[val: x * 10]
`)
	writeTestFile(t, tmp, "main.ll", `
import "mangle/a/util" as au
import "mangle/b/util" as bu

bind main = (): {
	let x = au.make_box(5)
	let y = bu.make_box(5)
	assert(x.val == 15)
	assert(y.val == 50)
	println("mangle_${x.val}_${y.val}")
}
`)
	out := runFileStrict(t, "pkg_canonical_mangle", tmp, false)
	if strings.TrimSpace(out) != "mangle_15_50" {
		t.Fatalf("unexpected stdout: %q", out)
	}
}

// 4. `*_test.ll` isolation, `[dev-dependencies]` isolation, `tests/` integration packages,
// and target-conditional OS/arch filename suffixes (`SPECIFICATION.md §4.1, §9.7, §12.2`).
func TestPackageTestFilesDevDepsAndTargetSuffixes(t *testing.T) {
	tmp := t.TempDir()
	root := projectRoot(t)

	// External dev-dependency package
	writeTestFile(t, tmp, "mockkit/lowlight.toml", "[package]\nname = \"mockkit\"\n")
	writeTestFile(t, tmp, "mockkit/mock.ll", "bind magic = () int: 777\n")

	// Main project using `mockkit` strictly in [dev-dependencies]
	projDir := filepath.Join(tmp, "svc")
	writeTestFile(t, projDir, "lowlight.toml", `
[package]
name = "svc"
version = "0.1.0"

[dev-dependencies]
mockkit = { path = "../mockkit" }
`)
	writeTestFile(t, projDir, "corelib/corelib.ll", `
bind _lib_base = () int: 700
bind compute = (delta: int) int: _lib_base() + delta
`)
	writeTestFile(t, projDir, "svc.ll", `
import "svc/corelib"

bind _secret_base = () int: 700
bind main = (): {
	println("svc=${corelib.compute(77)}")
}
`)
	writeTestFile(t, projDir, "svc_test.ll", `
import "mockkit"
import "std/test"

@test("white-box unit test uses private helper and dev-dependency")
bind test_whitebox = (): {
	test.assert_eq(_secret_base() + 77, mockkit.magic())
}
`)
	writeTestFile(t, projDir, "tests/integ/api_test.ll", `
import "svc/corelib"
import "mockkit"
import "std/test"

@test("black-box integration test imports dev-dependency")
bind test_integ = (): {
	test.assert_eq(corelib.compute(77), mockkit.magic())
}
`)

	// Normal build succeeds cleanly (omitting *_test.ll and dev-deps)
	if out := runFileStrict(t, "pkg_devdep_prod_build", projDir, false); strings.TrimSpace(out) != "svc=777" {
		t.Fatalf("unexpected stdout: %q", out)
	}

	// `driver.Test` runs both root white-box `svc_test.ll` AND `tests/integ/api_test.ll`!
	var stdout, stderr bytes.Buffer
	code, err := driver.Test(driver.Options{
		EntryPath:   projDir,
		ProjectRoot: root,
	}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("driver.Test failed (code=%d, err=%v)\nstdout:\n%s\nstderr:\n%s", code, err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "white-box unit test") || !strings.Contains(stdout.String(), "black-box integration test") {
		t.Fatalf("expected both unit and integration tests to run, got:\n%s", stdout.String())
	}

	// Production file importing a `[dev-dependencies]` package must fail!
	badProdDir := filepath.Join(tmp, "badsvc")
	writeTestFile(t, badProdDir, "lowlight.toml", `
[package]
name = "badsvc"

[dev-dependencies]
mockkit = { path = "../mockkit" }
`)
	writeTestFile(t, badProdDir, "main.ll", `
import "mockkit"

bind main = (): {
	println(mockkit.magic())
}
`)
	_, _, err = driver.Check(driver.Options{EntryPath: badProdDir, ProjectRoot: root})
	if err == nil || !strings.Contains(err.Error(), "dev-dependenc") {
		t.Fatalf("expected [dev-dependencies] isolation error in production code, got: %v", err)
	}

	// OS / Arch filename suffix filtering (`_linux.ll`, `_darwin.ll`, `_amd64.ll`, `_arm64.ll`)
	platDir := filepath.Join(tmp, "plat")
	writeTestFile(t, platDir, "lowlight.toml", "[package]\nname = \"plat\"\n")
	writeTestFile(t, platDir, "os_linux.ll", "bind os_tag = () string: \"linux\"\n")
	writeTestFile(t, platDir, "os_darwin.ll", "bind os_tag = () string: \"darwin\"\n")
	writeTestFile(t, platDir, "os_windows.ll", "bind os_tag = () string: \"windows\"\n")
	writeTestFile(t, platDir, "arch_amd64.ll", "bind arch_tag = () string: \"amd64\"\n")
	writeTestFile(t, platDir, "arch_arm64.ll", "bind arch_tag = () string: \"arm64\"\n")
	writeTestFile(t, platDir, "main.ll", `
bind main = (): {
	println("${os_tag()}-${arch_tag()}")
}
`)
	for _, triple := range []string{"x86_64-unknown-linux-gnu", "aarch64-apple-darwin", "x86_64-pc-windows-msvc"} {
		if _, _, err := driver.Check(driver.Options{
			EntryPath:    platDir,
			ProjectRoot:  root,
			TargetTriple: triple,
		}); err != nil {
			t.Fatalf("target suffix check failed for %s: %v", triple, err)
		}
	}
	// Run on host platform and verify exact host OS + arch selection!
	out := runFileStrict(t, "pkg_target_suffix_host", platDir, false)
	want := runtime.GOOS + "-" + runtime.GOARCH
	if strings.TrimSpace(out) != want {
		t.Fatalf("expected host target suffix output %q, got %q", want, strings.TrimSpace(out))
	}
}

// 5. Permissive global extension methods (`bind pkg.Type.method = ...`), encapsulation
// preservation (`_field` still private), and duplicate cross-package method collision diagnostics.
func TestPackagePermissiveExtensionMethodsAndCollision(t *testing.T) {
	tmp := t.TempDir()
	root := projectRoot(t)

	writeTestFile(t, tmp, "lowlight.toml", "[package]\nname = \"extdemo\"\n")
	writeTestFile(t, tmp, "geom/vec.ll", `
define Vec2 struct [
	x: int,
	y: int,
	_secret: int = 99,
]
`)
	writeTestFile(t, tmp, "ext/ops.ll", `
import "extdemo/geom"

bind geom.Vec2.manhattan = (self) int: {
	self.x + self.y
}
`)
	writeTestFile(t, tmp, "main.ll", `
import "extdemo/geom"
import "extdemo/ext"

bind main = (): {
	let v = geom.Vec2[x: 3, y: 4]
	assert(v.manhattan() == 7)
	println("ext_ok=${v.manhattan()}")
}
`)
	out := runFileStrict(t, "pkg_ext_method", tmp, false)
	if strings.TrimSpace(out) != "ext_ok=7" {
		t.Fatalf("unexpected stdout: %q", out)
	}

	// Extension method in external package CANNOT access private `_secret` field
	badPriv := t.TempDir()
	writeTestFile(t, badPriv, "lowlight.toml", "[package]\nname = \"badpriv\"\n")
	writeTestFile(t, badPriv, "geom/vec.ll", "define Vec2 struct [x: int, _secret: int = 1]\n")
	writeTestFile(t, badPriv, "ext/ops.ll", "import \"badpriv/geom\"\nbind geom.Vec2.leak = (self) int: self._secret\n")
	writeTestFile(t, badPriv, "main.ll", "import \"badpriv/geom\"\nimport \"badpriv/ext\"\nbind main = (): { let v = geom.Vec2[x: 1]; println(v.leak()) }\n")
	_, _, err := driver.Check(driver.Options{EntryPath: badPriv, ProjectRoot: root})
	if err == nil || !strings.Contains(err.Error(), "unexported field") {
		t.Fatalf("expected unexported field error inside extension method, got: %v", err)
	}

	// Cross-package duplicate method collision must name both binding sites
	badDup := t.TempDir()
	writeTestFile(t, badDup, "lowlight.toml", "[package]\nname = \"baddup\"\n")
	writeTestFile(t, badDup, "geom/vec.ll", "define Vec2 struct [x: int, y: int]\n")
	writeTestFile(t, badDup, "ext1/a.ll", "import \"baddup/geom\"\nbind geom.Vec2.norm = (self) int: self.x\n")
	writeTestFile(t, badDup, "ext2/b.ll", "import \"baddup/geom\"\nbind geom.Vec2.norm = (self) int: self.y\n")
	writeTestFile(t, badDup, "main.ll", "import \"baddup/geom\"\nimport \"baddup/ext1\"\nimport \"baddup/ext2\"\nbind main = (): { let v = geom.Vec2[x: 1, y: 2]; println(v.norm()) }\n")
	_, _, err = driver.Check(driver.Options{EntryPath: badDup, ProjectRoot: root})
	if err == nil || !strings.Contains(err.Error(), "duplicate method") || !strings.Contains(err.Error(), "already defined at") {
		t.Fatalf("expected duplicate method collision naming both sites, got: %v", err)
	}
}

// 6. Full CLI workflow: `new`, `init --lib`, `add --path` (comment-preserving), `tree`,
// `vendor --offline`, `--locked --offline`, `remove`, `fmt lowlight.toml`, zero-arg commands,
// and virtual `[workspace]` with `-p`, `--workspace`, `[overrides]`, and `[target]`.
func TestPackageCLIEndToEndWorkspaceLockVendorTree(t *testing.T) {
	root := projectRoot(t)
	cliBin := filepath.Join(t.TempDir(), "lowlight")
	buildCmd := exec.Command("go", "build", "-o", cliBin, "./cmd/lowlight")
	buildCmd.Dir = root
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build lowlight CLI: %v\n%s", err, string(out))
	}

	cacheDir := t.TempDir()
	runCLI := func(workDir string, args ...string) (string, error) {
		cmd := exec.Command(cliBin, args...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(),
			"LOWLIGHT_ROOT="+root,
			"LOWLIGHT_CACHE="+cacheDir,
		)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	base := t.TempDir()

	// 1. `lowlight new demo_app` + zero-arg `check`, `test`, `run`, `build`
	if out, err := runCLI(base, "new", "demo_app"); err != nil {
		t.Fatalf("lowlight new failed: %v\n%s", err, out)
	}
	appDir := filepath.Join(base, "demo_app")

	if out, err := runCLI(appDir, "check"); err != nil {
		t.Fatalf("zero-arg check failed: %v\n%s", err, out)
	}
	if out, err := runCLI(appDir, "test"); err != nil || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("zero-arg test failed: %v\n%s", err, out)
	}
	if out, err := runCLI(appDir, "run"); err != nil || !strings.Contains(out, "Hello, Lowlight!") {
		t.Fatalf("zero-arg run failed: %v\n%s", err, out)
	}

	// 2. `lowlight init --lib` for a local dependency `mathlib`
	libDir := filepath.Join(base, "mathlib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(libDir, "init", "--lib", "--name", "mathlib"); err != nil {
		t.Fatalf("lowlight init --lib failed: %v\n%s", err, out)
	}
	writeTestFile(t, libDir, "mathlib.ll", "bind triple = (x: int) int: x * 3\n")

	// Add a comment to demo_app/lowlight.toml and verify `lowlight add` preserves it surgically!
	mPath := filepath.Join(appDir, "lowlight.toml")
	rawM, err := os.ReadFile(mPath)
	if err != nil {
		t.Fatal(err)
	}
	commented := "# keep this user comment!\n" + string(rawM)
	if err := os.WriteFile(mPath, []byte(commented), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runCLI(appDir, "add", "mathlib", "--path", "../mathlib", "--offline"); err != nil {
		t.Fatalf("lowlight add failed: %v\n%s", err, out)
	}
	afterAdd, _ := os.ReadFile(mPath)
	if !strings.Contains(string(afterAdd), "# keep this user comment!") || !strings.Contains(string(afterAdd), `mathlib = { path = "../mathlib" }`) {
		t.Fatalf("expected comment-preserving add edit in lowlight.toml, got:\n%s", string(afterAdd))
	}

	// Use `mathlib` inside `demo_app/main.ll`
	writeTestFile(t, appDir, "main.ll", `
import "mathlib"

bind main = (): {
	println("triple=${mathlib.triple(14)}")
}
`)
	if out, err := runCLI(appDir, "run", "--locked", "--offline"); err != nil || strings.TrimSpace(out) != "triple=42" {
		t.Fatalf("run --locked --offline failed: %v\n%s", err, out)
	}

	// 3. `lowlight tree`
	if out, err := runCLI(appDir, "tree"); err != nil || !strings.Contains(out, "mathlib") {
		t.Fatalf("lowlight tree failed: %v\n%s", err, out)
	}

	// 4. `lowlight vendor --offline` -> delete original `../mathlib`, verify vendored copy compiles offline!
	if out, err := runCLI(appDir, "vendor", "--offline"); err != nil {
		t.Fatalf("lowlight vendor failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(appDir, "vendor", "mathlib", "mathlib.ll")); err != nil {
		t.Fatalf("expected vendored mathlib.ll: %v", err)
	}
	if err := os.RemoveAll(libDir); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(appDir, "run", "--offline"); err != nil || strings.TrimSpace(out) != "triple=42" {
		t.Fatalf("hermetic vendored offline run failed after removing original dep: %v\n%s", err, out)
	}

	// 5. `lowlight fmt -w lowlight.toml`
	if out, err := runCLI(appDir, "fmt", "-w", "lowlight.toml"); err != nil {
		t.Fatalf("lowlight fmt lowlight.toml failed: %v\n%s", err, out)
	}

	// 6. `lowlight remove mathlib --offline`
	if out, err := runCLI(appDir, "remove", "mathlib", "--offline"); err != nil {
		t.Fatalf("lowlight remove failed: %v\n%s", err, out)
	}
	afterRemove, _ := os.ReadFile(mPath)
	if strings.Contains(string(afterRemove), "mathlib =") {
		t.Fatalf("expected mathlib removed from lowlight.toml, got:\n%s", string(afterRemove))
	}

	// 7. Virtual `[workspace]` with `-p`, `--workspace`, and `[overrides]`
	wsDir := filepath.Join(base, "ws")
	writeTestFile(t, wsDir, "lowlight.toml", `
[workspace]
members = ["libs/*", "cmd/api"]
`)
	writeTestFile(t, wsDir, "libs/shared/lowlight.toml", "[package]\nname = \"shared\"\nversion = \"0.1.0\"\n")
	writeTestFile(t, wsDir, "libs/shared/shared.ll", `
bind badge = () string: "ws-shared"

@test("shared test")
bind test_shared = (): { assert(badge() == "ws-shared") }
`)
	writeTestFile(t, wsDir, "cmd/api/lowlight.toml", `
[package]
name = "api"
version = "0.1.0"

[dependencies]
shared = { path = "../../libs/shared" }
`)
	writeTestFile(t, wsDir, "cmd/api/main.ll", `
import "shared"

bind main = (): {
	println("api:${shared.badge()}")
}

@test("api test")
bind test_api = (): { assert(shared.badge() == "ws-shared") }
`)

	if out, err := runCLI(wsDir, "check", "--workspace"); err != nil {
		t.Fatalf("workspace check failed: %v\n%s", err, out)
	}
	if out, err := runCLI(wsDir, "test", "--workspace"); err != nil || !strings.Contains(out, "across 2 package(s)") {
		t.Fatalf("workspace test failed: %v\n%s", err, out)
	}
	if out, err := runCLI(wsDir, "run", "-p", "api"); err != nil || strings.TrimSpace(out) != "api:ws-shared" {
		t.Fatalf("workspace run -p api failed: %v\n%s", err, out)
	}
}

// 7. Two-phase incremental per-package `.o` compilation & caching on BOTH LLVM and C11:
// - Cold build compiles library package into Phase 1 `.o` (cache miss)
// - Editing ONLY `main.ll` (adding struct, function, string literal) gets a 100% Phase 1 cache HIT!
// - Editing library package invalidates Phase 1 cache cleanly and emits updated output!
func TestTwoPhaseIncrementalObjectCachingHitAndInvalidation(t *testing.T) {
	root := projectRoot(t)

	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		backend := backend
		t.Run(string(backend), func(t *testing.T) {
			proj := t.TempDir()
			cacheRoot := t.TempDir()
			outBin := filepath.Join(t.TempDir(), "app")

			writeTestFile(t, proj, "lowlight.toml", "[package]\nname = \"incapp\"\nversion = \"0.1.0\"\n")
			writeTestFile(t, proj, "calc/calc.ll", `
bind BASE = 100

define Tally struct [
	value: int,
]

bind Tally.add = (&self, delta: int): {
	self.value += delta
}

bind compute = (x: int) int: {
	let t = Tally[value: BASE]
	t.add(x)
	t.value
}
`)
			writeTestFile(t, proj, "main.ll", `
import "incapp/calc"

bind main = (): {
	println("v1:${calc.compute(5)}")
}
`)

			runBin := func() string {
				cmd := exec.Command(outBin)
				cmd.Env = append(os.Environ(), "LOWLIGHT_CHECK_LEAKS=1", "ASAN_OPTIONS=detect_leaks=0")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("binary run failed: %v\n%s", err, string(out))
				}
				return strings.TrimSpace(string(out))
			}

			// Build 1: cold cache -> Phase 1 miss populates `.o` cache
			_, st1, err := driver.BuildWithStats(driver.Options{
				EntryPath:   proj,
				ProjectRoot: root,
				Backend:     backend,
				OutPath:     outBin,
				CacheRoot:   cacheRoot,
				ExtraCFlags: strictCFlags,
			})
			if err != nil {
				t.Fatalf("Build 1 failed: %v", err)
			}
			if !st1.TwoPhase || st1.PackageUnits < 1 || st1.CacheMisses != st1.PackageUnits || st1.CacheHits != 0 {
				t.Fatalf("Build 1 expected cold two-phase miss, got %+v", st1)
			}
			if got := runBin(); got != "v1:105" {
				t.Fatalf("Build 1 unexpected output: %q", got)
			}

			// Build 2: edit ONLY `main.ll` (add a new struct, new function, new string literal!)
			// Phase 1 for `incapp/calc` MUST get a 100% cache hit!
			writeTestFile(t, proj, "main.ll", `
import "incapp/calc"

define MainOnlyStruct struct [
	label: string,
	n: int,
]

bind main_only_helper = (s: MainOnlyStruct) string: {
	"${s.label}:${s.n}"
}

bind main = (): {
	let s = MainOnlyStruct[label: "v2", n: calc.compute(7)]
	println(main_only_helper(s))
}
`)
			_, st2, err := driver.BuildWithStats(driver.Options{
				EntryPath:   proj,
				ProjectRoot: root,
				Backend:     backend,
				OutPath:     outBin,
				CacheRoot:   cacheRoot,
				ExtraCFlags: strictCFlags,
			})
			if err != nil {
				t.Fatalf("Build 2 failed: %v", err)
			}
			if !st2.TwoPhase || st2.CacheHits != st2.PackageUnits || st2.CacheMisses != 0 {
				t.Fatalf("Build 2 expected 100%% Phase 1 cache hit after main-only edit, got %+v", st2)
			}
			if got := runBin(); got != "v2:107" {
				t.Fatalf("Build 2 unexpected output: %q", got)
			}

			// Build 3: edit `calc/calc.ll` -> Phase 1 cache MUST invalidate and recompile!
			writeTestFile(t, proj, "calc/calc.ll", `
bind BASE = 200

define Tally struct [
	value: int,
]

bind Tally.add = (&self, delta: int): {
	self.value += delta * 2
}

bind compute = (x: int) int: {
	let t = Tally[value: BASE]
	t.add(x)
	t.value
}
`)
			_, st3, err := driver.BuildWithStats(driver.Options{
				EntryPath:   proj,
				ProjectRoot: root,
				Backend:     backend,
				OutPath:     outBin,
				CacheRoot:   cacheRoot,
				ExtraCFlags: strictCFlags,
			})
			if err != nil {
				t.Fatalf("Build 3 failed: %v", err)
			}
			if !st3.TwoPhase || st3.CacheMisses < 1 {
				t.Fatalf("Build 3 expected Phase 1 cache invalidation after upstream edit, got %+v", st3)
			}
			if got := runBin(); got != "v2:214" {
				t.Fatalf("Build 3 unexpected output: %q", got)
			}
		})
	}
}
