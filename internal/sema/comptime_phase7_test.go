package sema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lowlight/internal/diag"
	"lowlight/internal/sema"
)

func TestPhase7IDECacheAndWallClockTimeout(t *testing.T) {
	sema.ResetIDECaches()
	root := filepath.Join("..", "..")
	ws := t.TempDir()

	// 1. Tight wall-clock timeout in IDEMode emits a non-fatal Warning, never blocks subsequent decls,
	// and never triggers in non-IDE build checking.
	spinSrc := `const spin_sum = fn(n: int) int: {
	let acc = 0
	let i = 0
	while(i < n): {
		acc += (i % 7)
		i += 1
	}
	acc
}

const TIMED_OUT = spin_sum(60_000)
const SURVIVING_CONST = 42

bind main = fn(): println(SURVIVING_CONST)
`
	spinPath := filepath.Join(ws, "spin.ll")
	if err := os.WriteFile(spinPath, []byte(spinSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ideRep := diag.NewReporter()
	ideProg := sema.CheckForIDEWithOptions(sema.CheckIDEOptions{
		EntryPath:          spinPath,
		ProjectRoot:        root,
		IDEComptimeTimeout: 5 * time.Millisecond,
	}, ideRep)
	if ideRep.HasErrors() {
		t.Fatalf("expected wall-clock timeout in IDEMode to emit a warning, not an error: %v", ideRep.Diagnostics())
	}
	foundTimeoutWarn := false
	for _, d := range ideRep.Diagnostics() {
		if d.Severity == diag.Warning && strings.Contains(d.Message, "exceeded IDE time budget") {
			foundTimeoutWarn = true
			break
		}
	}
	if !foundTimeoutWarn {
		t.Fatalf("expected IDE timeout warning diagnostic, got: %+v", ideRep.Diagnostics())
	}
	if sym := ideProg.EntryMod.Symbols["SURVIVING_CONST"]; sym == nil || !sym.IsConst || !sym.IsConstInt || sym.ConstIntVal != 42 {
		t.Fatalf("expected subsequent const decl SURVIVING_CONST == 42 after timeout, got: %+v", sym)
	}

	// Non-IDE build mode must NOT apply the IDE wall-clock timeout.
	buildRep := diag.NewReporter()
	buildProg := sema.CheckWithOptions(sema.CheckOptions{
		EntryPath:          spinPath,
		ProjectRoot:        root,
		IDEMode:            false,
		IDEComptimeTimeout: 5 * time.Millisecond,
	}, buildRep)
	if buildRep.HasErrors() {
		t.Fatalf("unexpected error in build mode: %v", buildRep.Diagnostics())
	}
	for _, d := range buildRep.Diagnostics() {
		if strings.Contains(d.Message, "exceeded IDE time budget") {
			t.Fatalf("non-IDE build mode must never trigger IDE wall-clock timeout: %+v", d)
		}
	}
	if sym := buildProg.EntryMod.Symbols["TIMED_OUT"]; sym == nil || sym.ConstVal == nil {
		t.Fatalf("expected non-IDE build mode to evaluate TIMED_OUT completely")
	}

	// 2. Per-package const memoization (pkgConstCache) and whole-program IDE cache invalidation.
	sema.ResetIDECaches()
	projDir := filepath.Join(ws, "proj")
	lutDir := filepath.Join(projDir, "lut")
	appDir := filepath.Join(projDir, "app")
	if err := os.MkdirAll(lutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "lowlight.toml"), []byte("[workspace]\nmembers = [\"lut\", \"app\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lutDir, "lowlight.toml"), []byte("[package]\nname = \"lut\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lutDir, "data.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	lutSrc := `const make_table = fn() [int]: {
	let out: [int] = []
	for(i in 0..8): out.push(i * i)
	out
}

const SQUARES = make_table()
const EMBED_VAL = comp.embed_str("data.txt")
const ARCH_NAME = comp.TARGET.triple
`
	if err := os.WriteFile(filepath.Join(lutDir, "lut.ll"), []byte(lutSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "lowlight.toml"), []byte("[package]\nname = \"app\"\nversion = \"0.1.0\"\n\n[dependencies]\nlut = { path = \"../lut\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(appDir, "main.ll")
	mainV1 := `import "lut" as l

const THIRD_SQ = l.SQUARES[3]
bind main = fn(): println(THIRD_SQ)
`
	if err := os.WriteFile(mainPath, []byte(mainV1), 0o644); err != nil {
		t.Fatal(err)
	}

	// Cold check: 1 prog miss, consts evaluated and stored.
	rep1 := diag.NewReporter()
	prog1 := sema.CheckForIDEWithOptions(sema.CheckIDEOptions{
		EntryPath:   mainPath,
		Overlays:    map[string]string{mainPath: mainV1},
		ProjectRoot: projDir,
		StdDir:      filepath.Join(root, "std"),
	}, rep1)
	if rep1.HasErrors() || prog1 == nil {
		t.Fatalf("cold IDE check failed: %v", rep1.Diagnostics())
	}
	st1 := sema.GetIDECacheStats()
	if st1.ProgMisses != 1 || st1.ProgHits != 0 || st1.ConstMisses == 0 {
		t.Fatalf("unexpected cold stats: %+v", st1)
	}

	// Repeat identical check: whole-program cache hit!
	rep2 := diag.NewReporter()
	prog2 := sema.CheckForIDEWithOptions(sema.CheckIDEOptions{
		EntryPath:   mainPath,
		Overlays:    map[string]string{mainPath: mainV1},
		ProjectRoot: projDir,
		StdDir:      filepath.Join(root, "std"),
	}, rep2)
	if rep2.HasErrors() || prog2 != prog1 {
		t.Fatalf("expected identical cached CheckedProgram on unchanged inputs")
	}
	st2 := sema.GetIDECacheStats()
	if st2.ProgHits != 1 {
		t.Fatalf("expected ProgHits == 1, got %+v", st2)
	}

	// Edit ONLY main.ll overlay: prog cache misses, imported package `lut` hits per-package const cache!
	mainV2 := `import "lut" as l

const FOURTH_SQ = l.SQUARES[4]
const CHK = comptime { assert(l.EMBED_VAL == "v1"); 1 }
bind main = fn(): println(FOURTH_SQ)
`
	rep3 := diag.NewReporter()
	prog3 := sema.CheckForIDEWithOptions(sema.CheckIDEOptions{
		EntryPath:   mainPath,
		Overlays:    map[string]string{mainPath: mainV2},
		ProjectRoot: projDir,
		StdDir:      filepath.Join(root, "std"),
	}, rep3)
	if rep3.HasErrors() {
		t.Fatalf("edit-main IDE check failed: %v", rep3.Diagnostics())
	}
	st3 := sema.GetIDECacheStats()
	if st3.ProgMisses != 2 || st3.ConstHits == 0 {
		t.Fatalf("expected prog miss and per-package const cache hits when only entry edited, got: %+v", st3)
	}
	if sym := prog3.EntryMod.Symbols["FOURTH_SQ"]; sym == nil || !sym.IsConstInt || sym.ConstIntVal != 16 {
		t.Fatalf("expected hydrated FOURTH_SQ == 16 from cached lut.SQUARES, got: %+v", sym)
	}

	// Modify embedded asset on disk -> invalidates both prog cache and cached embed const!
	if err := os.WriteFile(filepath.Join(lutDir, "data.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainV3 := `import "lut" as l

const CHK2 = comptime { assert(l.EMBED_VAL == "v2"); 1 }
bind main = fn(): println(CHK2)
`
	rep4 := diag.NewReporter()
	_ = sema.CheckForIDEWithOptions(sema.CheckIDEOptions{
		EntryPath:   mainPath,
		Overlays:    map[string]string{mainPath: mainV3},
		ProjectRoot: projDir,
		StdDir:      filepath.Join(root, "std"),
	}, rep4)
	if rep4.HasErrors() {
		t.Fatalf("expected updated embedded asset to re-evaluate to v2 cleanly, got: %v", rep4.Diagnostics())
	}

	// Switching target triple bypasses old target cache entry cleanly.
	repLinux := diag.NewReporter()
	progLinux := sema.CheckForIDEWithOptions(sema.CheckIDEOptions{
		EntryPath:    mainPath,
		Overlays:     map[string]string{mainPath: mainV3},
		ProjectRoot:  projDir,
		StdDir:       filepath.Join(root, "std"),
		TargetTriple: "x86_64-unknown-linux-gnu",
	}, repLinux)
	if repLinux.HasErrors() || progLinux == nil {
		t.Fatalf("linux target check failed: %v", repLinux.Diagnostics())
	}
	if lutMod := progLinux.PackageByID["lut"].PrimaryMod; lutMod != nil {
		if preview := sema.FormatComptimeValPreview(lutMod.Symbols["ARCH_NAME"].ConstVal, 120); preview != "\"x86_64-unknown-linux-gnu\"" {
			t.Fatalf("expected linux triple in cached/re-evaluated ARCH_NAME, got %q", preview)
		}
	}
}
