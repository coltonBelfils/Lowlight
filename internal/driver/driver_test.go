package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/codegen"
	"lowlight/internal/ir"
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

func TestExpandAndCheckAllTargets(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Expand prunes top-level/local comptime if, unrolls comptime for, folds const, and formats cleanly.
	expandFile := filepath.Join(tmpDir, "expand.ll")
	expandSrc := `comptime if(comp.TARGET.os == comp.TargetOs.linux): {
	const PLAT_CODE = 10
} else: {
	const PLAT_CODE = 20
}

const FOLDED = 6 * 7
const CT_SUM = {
	let s = 0
	comptime for(i in 1..=3): {
		s += i
	}
	s
}

define Box[T] struct [ val: T ]

const pick[T] = fn() int: {
	comptime if(type.size_of[T]() == 8): 88 else: 11
}

bind main = fn(): {
	comptime if(false): {
		missing_fn()
	}
	let total = 0
	comptime for(k in 0..3): {
		total += k
	}
	let x = pick[int]()
	let y = pick[bool]()
	let z = comptime { FOLDED + CT_SUM }
	println("$PLAT_CODE:$FOLDED:$CT_SUM:$total:$x:$y:$z")
}
`
	if err := os.WriteFile(expandFile, []byte(expandSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	expanded, _, err := Expand(Options{
		EntryPath:    expandFile,
		TargetTriple: "x86_64-unknown-linux-gnu",
	})
	if err != nil {
		t.Fatalf("Expand failed: %v", err)
	}
	for _, want := range []string{
		"const PLAT_CODE = 10",
		"const FOLDED = 42",
		"const CT_SUM = 6",
		"const pick__ll_int = fn() int: 88",
		"const pick__ll_bool = fn() int: 11",
		"const k = 0",
		"const k = 1",
		"const k = 2",
		"48",
	} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded output missing %q:\n%s", want, expanded)
		}
	}
	for _, unwanted := range []string{
		"const PLAT_CODE = 20",
		"missing_fn()",
		"comptime for",
	} {
		if strings.Contains(expanded, unwanted) {
			t.Fatalf("expanded output should not contain %q:\n%s", unwanted, expanded)
		}
	}

	// Verify expanded output parses cleanly via Check on expanded non-generic snippet.
	if _, _, checkErr := Check(Options{
		EntryPath:    expandFile,
		SrcOverride:  expanded,
		TargetTriple: "x86_64-unknown-linux-gnu",
	}); checkErr != nil {
		t.Fatalf("expanded source should remain valid Lowlight:\n%s\nerr: %v", expanded, checkErr)
	}

	// 2. CheckAllTargets succeeds across all 5 canonical targets when every platform branch is valid.
	okMultiFile := filepath.Join(tmpDir, "multi_ok.ll")
	okMultiSrc := `comptime if(comp.TARGET.os == comp.TargetOs.windows): {
	const OS_TAG = "win"
} else comptime if(comp.TARGET.os == comp.TargetOs.wasm): {
	const OS_TAG = "wasm"
} else: {
	const OS_TAG = "unix"
}

bind main = fn(): {
	let n = comptime match(comp.TARGET.endian):
		when comp.Endian.little: 1
		when comp.Endian.big: 2
		default: 0
	assert(OS_TAG.len() > 0 and n > 0)
}
`
	if err := os.WriteFile(okMultiFile, []byte(okMultiSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckAllTargets(Options{EntryPath: okMultiFile}); err != nil {
		t.Fatalf("CheckAllTargets failed on valid multi-platform code: %v", err)
	}

	// 3. CheckAllTargets catches target-specific errors in untaken host branches and labels only failing target(s).
	badWinFile := filepath.Join(tmpDir, "multi_bad_win.ll")
	badWinSrc := `comptime if(comp.TARGET.os == comp.TargetOs.windows): {
	const BAD_WIN: int = "wrong_type_only_on_windows"
} else: {
	const OK = 1
}

bind main = fn(): {}
`
	if err := os.WriteFile(badWinFile, []byte(badWinSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(Options{EntryPath: badWinFile, TargetTriple: "aarch64-apple-darwin"}); err != nil {
		t.Fatalf("single host darwin check unexpectedly failed: %v", err)
	}
	_, allErr := CheckAllTargets(Options{EntryPath: badWinFile})
	if allErr == nil {
		t.Fatal("expected CheckAllTargets to catch Windows-only type error")
	}
	msg := allErr.Error()
	if !strings.Contains(msg, "[targets: x86_64-pc-windows-msvc]") || !strings.Contains(msg, "BAD_WIN") {
		t.Fatalf("expected target-labeled diagnostic for x86_64-pc-windows-msvc, got:\n%s", msg)
	}
	if strings.Contains(msg, "darwin") || strings.Contains(msg, "linux") || strings.Contains(msg, "wasm") {
		t.Fatalf("unrelated clean targets should not appear in failure label:\n%s", msg)
	}
}

func TestExpandPreservesTypesAndReChecks(t *testing.T) {
	tmpDir := t.TempDir()
	entryPath := filepath.Join(tmpDir, "expand_types.ll")
	src := `define Port u16
define PairBox[T] struct [ item: T ]
define Res[T, E] enum [ Ok: T, Err: E ]

const C_U8 = u8(42)
const C_I32 = i32(-7)
const C_U64 = u64(18446744073709551615)
const C_F32 = f32(1.5)
const C_PORT = Port(8080)
const C_EMPTY = List[u16].new()
const C_BYTES = [u8(1), u8(2), u8(3)]
const C_ARR: [2: u16] = [u16(10), u16(20)]
const C_NONE? = { let x?: ?int = none; x? }
const C_SOME? = { let x?: ?int = 99; x? }
const C_BOX = PairBox[u8][item: u8(7)]
const C_RES = Res[u16, string].Ok(u16(123))

bind check_res = fn(r: Res[u16, string]) u16: {
	match(r):
		when Res.Ok(v): v
		default: u16(0)
}

bind main = fn(): {
	let a: u8 = C_U8
	let b: i32 = C_I32
	let c: u64 = C_U64
	let d: f32 = C_F32
	let e: Port = C_PORT
	let f: [u16] = C_EMPTY.copy()
	let g: [u8] = C_BYTES.copy()
	let h: [2: u16] = C_ARR
	let i?: ?int = C_NONE?
	let j?: ?int = C_SOME?
	let k: PairBox[u8] = C_BOX
	assert(check_res(C_RES) == u16(123))

	let lx = comptime { u8(5) }
	let lu = comptime { u64(18446744073709551615) }
	let lf = comptime { f32(2.25) }
	let le = comptime { List[u16].new() }
	let lo? = comptime { let x?: ?int = none; x? }
	let tx: u8 = lx
	let tu: u64 = lu
	let tf: f32 = lf
	let te: [u16] = le
	let to?: ?int = lo?
	assert(a == u8(42) and b == i32(-7) and c == u64(18446744073709551615))
	assert(d == f32(1.5) and int(e) == 8080 and f.len() == 0 and g.len() == 3)
	assert(h[1] == u16(20) and i? == none and (j? ?? 0) == 99 and k.item == u8(7))
	let _ = +[tx, tu, tf, te.len(), to? ?? 0]
}
`
	if err := os.WriteFile(entryPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	expanded, _, err := Expand(Options{
		EntryPath:    entryPath,
		TargetTriple: "aarch64-apple-darwin",
	})
	if err != nil {
		t.Fatalf("Expand failed: %v", err)
	}
	for _, want := range []string{
		"const C_U8 = u8(42)",
		"const C_I32 = i32(-7)",
		"const C_U64 = u64(18446744073709551615)",
		"const C_F32 = f32(1.5)",
		"const C_PORT = Port(8080)",
		"const C_EMPTY = List[u16].new()",
		"const C_BYTES = [u8(1), u8(2), u8(3)]",
		"const C_ARR: [2: u16] = [2: u16(10), u16(20)]",
		"const C_NONE?: ?int = none",
		"const C_SOME?: ?int = 99",
		"const C_BOX = PairBox[u8][item: u8(7)]",
		"const C_RES = Res[u16, string].Ok(u16(123))",
	} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded output missing %q:\n%s", want, expanded)
		}
	}
	if _, _, checkErr := Check(Options{
		EntryPath:    entryPath,
		SrcOverride:  expanded,
		TargetTriple: "aarch64-apple-darwin",
	}); checkErr != nil {
		t.Fatalf("expanded source failed Check:\n%s\nerr: %v", expanded, checkErr)
	}
}


func TestTargetSystemLibsDuplicateExternAndSplitIsolation(t *testing.T) {
	if libs := targetSystemLibs(manifest.ParseTarget("aarch64-apple-darwin")); len(libs) != 2 || libs[0] != "-lm" || libs[1] != "-ldl" {
		t.Fatalf("expected darwin system libs [-lm -ldl], got %v", libs)
	}
	if libs := targetSystemLibs(manifest.ParseTarget("x86_64-unknown-linux-gnu")); len(libs) != 2 || libs[0] != "-lm" || libs[1] != "-ldl" {
		t.Fatalf("expected linux system libs [-lm -ldl], got %v", libs)
	}
	if libs := targetSystemLibs(manifest.ParseTarget("x86_64-pc-windows-msvc")); len(libs) != 0 {
		t.Fatalf("expected no POSIX system libs on windows, got %v", libs)
	}
	if libs := targetSystemLibs(manifest.ParseTarget("wasm32-unknown-unknown")); len(libs) != 0 {
		t.Fatalf("expected no POSIX system libs on wasm, got %v", libs)
	}

	tmpDir := t.TempDir()
	writePkg := func(dir, tomlSrc, llName, llSrc string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, manifest.ManifestFilename), []byte(tomlSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, llName), []byte(llSrc), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writePkg(filepath.Join(tmpDir, "dep_a"),
		"[package]\nname = \"dep_a\"\nversion = \"0.1.0\"\n",
		"dep_a.ll",
		"extern \"limits.h\"\nextern \"test_dup_sym\" as dup_i32: fn(x: i32) i32\nbind call_a = fn() i32: unsafe { dup_i32(i32(7)) }\n",
	)
	writePkg(filepath.Join(tmpDir, "dep_b"),
		"[package]\nname = \"dep_b\"\nversion = \"0.1.0\"\n",
		"dep_b.ll",
		"extern \"float.h\"\nextern \"test_dup_sym\" as dup_ptr: fn(p: ptr[u8]) ptr[u8]\nbind call_b = fn() ptr[u8]: unsafe {\n\tlet p = null_ptr[u8]()\n\tdup_ptr(p)\n}\n",
	)
	writePkg(filepath.Join(tmpDir, "dep_c"),
		"[package]\nname = \"dep_c\"\nversion = \"0.1.0\"\n",
		"dep_c.ll",
		"bind add_c = fn(x: int) int: x + 1\n",
	)

	appDir := filepath.Join(tmpDir, "app")
	writePkg(appDir,
		"[package]\nname = \"app\"\nversion = \"0.1.0\"\n\n[dependencies]\ndep_a = { path = \"../dep_a\" }\ndep_b = { path = \"../dep_b\" }\ndep_c = { path = \"../dep_c\" }\n",
		"main.ll",
		"import \"dep_a\" as a\nimport \"dep_b\" as b\nimport \"dep_c\" as c\n\nbind main = fn(): {\n\tlet _ = a.call_a()\n\tlet _ = b.call_b()\n\tlet _ = c.add_c(41)\n}\n",
	)

	entryPath := filepath.Join(appDir, "main.ll")
	cSrc, checked, err := EmitC(Options{EntryPath: entryPath})
	if err != nil {
		t.Fatalf("EmitC failed: %v", err)
	}
	llvmSrc, _, err := EmitLLVM(Options{EntryPath: entryPath})
	if err != nil {
		t.Fatalf("EmitLLVM failed: %v", err)
	}
	if strings.Count(llvmSrc, "test_dup_sym(") == 0 && !strings.Contains(llvmSrc, "@test_dup_sym") {
		t.Fatalf("expected test_dup_sym in LLVM output")
	}
	// Only one declare line for @test_dup_sym should be emitted in LLVM IR.
	declareCount := 0
	for _, line := range strings.Split(llvmSrc, "\n") {
		if strings.HasPrefix(line, "declare ") && strings.Contains(line, "@test_dup_sym(") {
			declareCount++
		}
	}
	if declareCount != 1 {
		t.Fatalf("expected exactly 1 declare for @test_dup_sym, got %d", declareCount)
	}

	mirProg := ir.Lower(checked, false, false)
	cSplit := codegen.EmitCSplit(mirProg)
	llvmSplit := codegen.EmitLLVMSplit(mirProg)
	if len(cSplit.PackageUnits) != 3 || len(llvmSplit.PackageUnits) != 3 {
		t.Fatalf("expected 3 package units in split output, got c=%d llvm=%d", len(cSplit.PackageUnits), len(llvmSplit.PackageUnits))
	}

	unitByTag := func(units []codegen.PackageUnit, name string) string {
		for _, u := range units {
			if strings.Contains(u.PkgID, name) || strings.Contains(u.PkgTag, name) {
				return u.Source
			}
		}
		t.Fatalf("missing split unit %q", name)
		return ""
	}
	cA, cB, cC := unitByTag(cSplit.PackageUnits, "dep_a"), unitByTag(cSplit.PackageUnits, "dep_b"), unitByTag(cSplit.PackageUnits, "dep_c")
	if !strings.Contains(cA, "#include <limits.h>") || strings.Contains(cA, "<float.h>") {
		t.Fatalf("dep_a C unit header isolation failed:\n%s", cA)
	}
	if !strings.Contains(cB, "#include <float.h>") || strings.Contains(cB, "<limits.h>") {
		t.Fatalf("dep_b C unit header isolation failed:\n%s", cB)
	}
	if strings.Contains(cC, "<limits.h>") || strings.Contains(cC, "<float.h>") || strings.Contains(cC, "test_dup_sym") {
		t.Fatalf("dep_c C unit leaked foreign extern headers/symbols:\n%s", cC)
	}

	lA, lB, lC := unitByTag(llvmSplit.PackageUnits, "dep_a"), unitByTag(llvmSplit.PackageUnits, "dep_b"), unitByTag(llvmSplit.PackageUnits, "dep_c")
	if !strings.Contains(lA, "!{!\"limits.h\"}") || strings.Contains(lA, "!\"float.h\"") {
		t.Fatalf("dep_a LLVM unit header isolation failed:\n%s", lA)
	}
	if !strings.Contains(lB, "!{!\"float.h\"}") || strings.Contains(lB, "!\"limits.h\"") {
		t.Fatalf("dep_b LLVM unit header isolation failed:\n%s", lB)
	}
	if strings.Contains(lC, "limits.h") || strings.Contains(lC, "float.h") || strings.Contains(lC, "test_dup_sym") {
		t.Fatalf("dep_c LLVM unit leaked foreign extern headers/symbols:\n%s", lC)
	}

	if _, err := exec.LookPath("clang"); err == nil {
		checkClang := func(lang string, extra []string, src string, label string) {
			args := append([]string{"-x", lang}, extra...)
			args = append(args, "-", "-fsyntax-only")
			cmd := exec.Command("clang", args...)
			cmd.Stdin = strings.NewReader(src)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("clang rejected %s: %v\n%s", label, err, string(out))
			}
		}
		checkClang("c", []string{"-std=c11", "-Wall", "-Werror"}, cSrc, "standalone C11")
		checkClang("ir", []string{"-Wno-override-module"}, llvmSrc, "standalone LLVM")
		for _, u := range cSplit.PackageUnits {
			checkClang("c", []string{"-std=c11"}, u.Source, "split C "+u.PkgID)
		}
		checkClang("c", []string{"-std=c11"}, cSplit.LinkUnit, "split C link")
		for _, u := range llvmSplit.PackageUnits {
			checkClang("ir", []string{"-Wno-override-module"}, u.Source, "split LLVM "+u.PkgID)
		}
		checkClang("ir", []string{"-Wno-override-module"}, llvmSplit.LinkUnit, "split LLVM link")
	}
}
