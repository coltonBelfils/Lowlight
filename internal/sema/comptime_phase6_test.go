package sema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/parser"
	"lowlight/internal/types"
)

func TestPhase6SemaAndParserRegressions(t *testing.T) {
	// B10: addr_of string materialization propagates pl.set abort instead of silent ignore.
	repB10 := diag.NewReporter()
	aB10 := &Analyzer{Rep: repB10, Prog: &CheckedProgram{}}
	stB10 := newCtState(aB10, nil, 0)
	aliveB10 := true
	immSym := &Symbol{Name: "imm", IsBind: true, Type: types.TypeString}
	roPlace := ctPlace{
		frameAlive: &aliveB10,
		get:        func() (*ctVal, ctSignal) { return newCtString("hi"), okSig() },
		set:        func(_ *ctVal) ctSignal { return abortSig(diag.Span{}, "immutable place") },
	}
	argId := &ast.IdentExpr{Name: "imm"}
	bmB10 := &BodyMaps{ExprInfos: map[ast.Expr]*ExprInfo{argId: {Sym: immSym, Type: types.TypeString}}}
	stB10.frames = []ctFrame{{
		mod:    &Module{Pkg: &Package{IsStd: true, ID: "std/core"}},
		bm:     bmB10,
		alive:  &aliveB10,
		locals: map[*Symbol]*ctSlot{immSym: {ty: types.TypeString, refPlace: &roPlace, frameAlive: &aliveB10}},
	}}
	callB10 := &ast.CallExpr{Args: []ast.Expr{argId}}
	infB10 := &ExprInfo{BuiltinName: "addr_of", Type: &types.Ptr{Elem: types.TypeString}, IntrinsicType: types.TypeString}
	if _, sig := stB10.evalIntrinsicCall(callB10, infB10); sig.kind != sigAbort || !strings.Contains(sig.errMsg, "immutable") {
		t.Fatalf("expected abort taking addr_of immutable string binding, got sig=%+v", sig)
	}

	// H6: closure in external module/method cannot inherit derive auth to read private `_val` field.
	tmpH6 := t.TempDir()
	privDir := filepath.Join(tmpH6, "priv")
	derDir := filepath.Join(tmpH6, "der")
	if err := os.MkdirAll(privDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(derDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privDir, "priv.ll"), []byte("define Box struct [ pub: int, _priv: int ]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(derDir, "der.ll"), []byte(`
import "../priv"
bind helper = fn(b: priv.Box) int: {
	let f = cl() int: b._priv
	f()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mainH6 := filepath.Join(tmpH6, "main.ll")
	if err := os.WriteFile(mainH6, []byte("import \"der\"\nbind main = fn(): {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repH6 := diag.NewReporter()
	_ = Check(mainH6, "", tmpH6, repH6)
	if !repH6.HasErrors() || !strings.Contains(repH6.Format(), "unexported") {
		t.Fatalf("expected unexported field access rejection inside closure, got:\n%s", repH6.Format())
	}

	// H8: recursive Self usage detection through tuple/func/generic args prevents boxing Self-using generic interface.
	_, repSelf := checkComptimeSrc(t, `
define Wrapper[T] struct [ item: T ]
define BadGenericIface[T] interface [
	check: fn(self, other: +[T, Wrapper[Self]]) bool,
]
define Impl struct [ x: int ]
bind Impl.check = fn(self, other: +[int, Wrapper[Impl]]) bool: true
bind take_box = fn(b: BadGenericIface[int]): {}
bind main = fn(): {
	let v = Impl[x: 1]
	take_box(v)
}
`)
	if !repSelf.HasErrors() || !strings.Contains(repSelf.Format(), "Self") {
		t.Fatalf("expected recursive Self rejection when boxing generic interface, got:\n%s", repSelf.Format())
	}

	// H10 & D3: @comptime_limit(steps: N) does not inflate unroll limit; @comptime_limit(unroll: N) does.
	assertComptimeError(t, "D3_steps_limit_does_not_inflate_unroll", `
@comptime_limit(steps: 5000000)
const f = fn() int: {
	let xs = comptime for(i in 0..1100) [int]: i
	xs.len()
}
`, "exceeds unroll limit of 1024")

	_, repUnroll := checkComptimeSrc(t, `
@comptime_limit(unroll: 1200)
const f = fn() int: {
	let xs = comptime for(i in 0..1050) [int]: i
	xs.len()
}
const X = f()
bind main = fn(): { assert(X == 1050) }
`)
	if repUnroll.HasErrors() {
		t.Fatalf("expected @comptime_limit(unroll: 1200) to succeed:\n%s", repUnroll.Format())
	}

	// H10: collecting comptime for with `for <-`, `next <- item`, `else`, and nested iterator arena escape rejection.
	_, repColFor := checkComptimeSrc(t, `
const collected = comptime {
	comptime for(i in 0..4) [int]: {
		if(i == 1): next <- 10
		if(i == 3): for <- [99]
		i
	} else: [0]
}
const empty_else = comptime {
	comptime for(i in 0..0) [int]: i else: [77]
}
bind main = fn(): {
	assert(collected.len() == 1 and collected[0] == 99)
	assert(empty_else.len() == 1 and empty_else[0] == 77)
}
`)
	if repColFor.HasErrors() {
		t.Fatalf("collecting comptime for valid cases failed:\n%s", repColFor.Format())
	}

	assertComptimeError(t, "H10_collecting_for_nested_arena_escape", `
bind main = fn() int: {
	let out = comptime for(i in 0..2) [ref[int]]: {
		let inner = Arena.new()
		inner.alloc(i)
	} else: []
	out.len()
}
`, "cannot escape")

	// M5: index-vs-generic call disambiguation on Distinct/Struct (`get`), Ref, and StackArray collections.
	_, repIdx := checkComptimeSrc(t, `
define FnBox [fn() int]
bind FnBox.get = fn(self, i: int) fn() int: self.underlying()[i]
bind call_distinct_vec = fn(box: FnBox) int: box[0]()
bind call_ref_vec = fn(r: ref[[fn() int]]) int: r[0]()
bind call_arr = fn(arr: [1: fn() int]) int: arr[0]()
bind main = fn(): {
	let b = FnBox([fn() int: 42])
	assert(call_distinct_vec(b) == 42)
	let a = Arena.new()
	let r = a.alloc([fn() int: 7])
	assert(call_ref_vec(r) == 7)
	let arr: [1: fn() int] = [fn() int: 9]
	assert(call_arr(arr) == 9)
}
`)
	if repIdx.HasErrors() {
		t.Fatalf("expected Distinct/Ref/StackArray indexed callable call to check cleanly:\n%s", repIdx.Format())
	}

	// M8: parser rejects interface bounds on receiver type parameters instead of silently dropping them.
	pRep := diag.NewReporter()
	_ = parser.ParseFile("bad_recv_bounds.ll", "define Box[T] struct [ v: T ]\nbind Box[T: Equatable].ok = fn(self) bool: true\n", pRep)
	if !pRep.HasErrors() || !strings.Contains(pRep.Format(), "interface bounds are not allowed on receiver type parameter") {
		t.Fatalf("expected parser error for receiver type param bounds, got:\n%s", pRep.Format())
	}
	_ = ast.IdentExpr{}
}

func TestPhase6EmbedFailClosedNonRegularAndIDEBudget(t *testing.T) {
	dir := t.TempDir()
	// Missing file inside sandbox records AttemptedEmbedPaths; unmatched non-empty caller fails closed.
	srcMissing := "const S = comp.embed_str(\"missing.txt\")\n"
	mainPath := filepath.Join(dir, "main.ll")
	if err := os.WriteFile(mainPath, []byte(srcMissing), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := diag.NewReporter()
	prog := Check(mainPath, "", dir, rep)
	if !rep.HasErrors() {
		t.Fatalf("expected embed error on missing file")
	}
	if len(prog.AttemptedEmbedPaths) == 0 {
		t.Fatalf("expected in-sandbox missing embed path in AttemptedEmbedPaths")
	}

	// H11 fail-closed on unmatched non-empty caller path via ctState directly.
	a := &Analyzer{Prog: prog, Rep: diag.NewReporter()}
	cs := newCtState(a, prog.EntryMod, 0)
	baseDir, rootDir := cs.resolveEmbedModuleAndRoot(filepath.Join(dir, "unregistered_other.ll"))
	if baseDir != "" || rootDir != "" {
		t.Fatalf("expected resolveEmbedModuleAndRoot to fail closed for unknown file, got base=%q root=%q", baseDir, rootDir)
	}

	// M4: IDEMode enforces bounded comptime step limit even with large @comptime_limit.
	infiniteSrc := `
@comptime_limit(steps: 50_000_000)
const spin = fn() int: {
	let n = 0
	for(true): { n += 1 }
	n
}
const X = spin()
`
	ideRep := diag.NewReporter()
	_ = CheckForIDE(mainPath, map[string]string{mainPath: infiniteSrc}, dir, ideRep)
	if !ideRep.HasErrors() || !strings.Contains(ideRep.Format(), "step limit (100000") {
		t.Fatalf("expected IDEMode to cap steps at 100000, got:\n%s", ideRep.Format())
	}
}
