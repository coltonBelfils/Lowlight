package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func TestPhase6DerivableStructsEnumsResultsZeroField(t *testing.T) {
	t.Parallel()
	src := `
import "std/sort"

@derive(Equatable, Comparable, Hashable, Copyable, Printable)
define Empty struct []

@derive(Equatable, Comparable, Hashable, Copyable, Printable)
define Color enum [Red, Green, Blue]

@derive(Equatable, Copyable, Printable)
define TreeNode struct [
	val: int,
	tag: string,
	next?: ?TreeNode,
]

@derive(Copyable, Printable)
define AstNode enum [
	Leaf: string,
	Group: [AstNode],
]

@derive(Equatable, Comparable, Hashable, Copyable, Printable)
define TaggedVal enum [
	Nil,
	Num: int,
	Pair: +[int, string],
	Text: string,
]

@derive(Equatable, Comparable, Hashable, Copyable, Printable)
define ParseRes[T] result [
	value: T,
	Bad: string,
]

const CT_RES = comptime {
	let e1 = Empty[]
	let e2 = e1.copy()
	assert(e1 == e2 and e1.cmp(e2) == 0, "ct zero-field eq/cmp")
	assert(e1.to_string() == "Empty[]", "ct zero-field to_string")

	let a = AstNode.Group([AstNode.Leaf("x".copy()), AstNode.Leaf("y".copy())])
	let b = a.copy()
	assert(b.to_string() == "AstNode.Group([AstNode.Leaf(x), AstNode.Leaf(y)])", "ct enum")

	let tv1 = TaggedVal.Pair(+[1, "a".copy()])
	let tv2 = TaggedVal.Pair(+[1, "b".copy()])
	assert(tv1 != tv2 and tv1 < tv2 and tv1 == tv1.copy(), "ct tagged enum")

	let r1! = ParseRes[int].value(42)
	let r2! = ParseRes[int].Bad("err".copy())
	assert(r1! == ParseRes[int].value(42) and r1! < r2!, "ct result cmp")
	assert(r2!.to_string() == "ParseRes.Bad(err)", "ct result to_string")
	e1.hash() ^ tv1.hash() ^ r1!.hash()
}

bind main = fn(): {
	assert(CT_RES != 0 or CT_RES == 0, "ct hash ran")

	// Recursive BoxedOptional structs (eq, copy, to_string, zero leaks)
	let n3 = TreeNode[val: 3, tag: "tail".copy(), next?: none]
	let n2 = TreeNode[val: 2, tag: "mid".copy(), next?: n3]
	let n1 = TreeNode[val: 1, tag: "head".copy(), next?: n2]
	let c1 = n1.copy()
	assert(n1 == c1, "recursive boxed optional eq")
	assert(c1.to_string() == "TreeNode[val: 1, tag: head, next?: TreeNode[val: 2, tag: mid, next?: TreeNode[val: 3, tag: tail, next?: none]]]", "recursive to_string")

	let other = TreeNode[val: 1, tag: "head".copy(), next?: TreeNode[val: 2, tag: "diff".copy(), next?: none]]
	assert(n1 != other, "recursive neq")

	// Zero-field, Simple Enum, Tagged Enum, Recursive Enum, Result in Set/Map & Sort
	let em = Empty[]
	assert(em.to_string() == "Empty[]", "rt empty")
	let eset: #[Empty] = #[Empty[], Empty[]]
	assert(eset.len() == 1, "empty set")

	let colors = [Color.Blue, Color.Red, Color.Green]
	sort.sort(&colors)
	assert(colors[0] == Color.Red and colors[1] == Color.Green and colors[2] == Color.Blue, "sorted simple enum")

	let rec_a = AstNode.Group([AstNode.Leaf("k".copy())]).copy()
	assert(rec_a.to_string() == "AstNode.Group([AstNode.Leaf(k)])", "rt recursive enum")

	let tset: #[TaggedVal] = #[
		TaggedVal.Pair(+[2, "b".copy()]),
		TaggedVal.Nil,
		TaggedVal.Pair(+[2, "b".copy()]),
	]
	assert(tset.len() == 2, "tagged enum set")

	let rmap: #[ParseRes[int]: string] = #[
		ParseRes[int].value(1): "one".copy(),
		ParseRes[int].Bad("x".copy()): "bad".copy(),
	]
	assert((rmap[ParseRes[int].value(1)] ?? "") == "one", "result map lookup")
	println("derives_ok")
}
`
	out := runInlineStrict(t, "phase6_derivable_types", src, false)
	if strings.TrimSpace(out) != "derives_ok" {
		t.Fatalf("unexpected stdout: %q", out)
	}
}

func TestPhase6CustomDerivableCrossPackageAndH16Privacy(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	writeTestFile(t, tmp, "lowlight.toml", `
[package]
name = "customapp"
version = "0.1.0"
`)
	// Package `traits`: user-defined custom @derivable interfaces (including &self and extra args).
	writeTestFile(t, tmp, "traits/traits.ll", `
import "customapp/handlers"

@derivable(summarize: handlers.summarize, reset: handlers.reset, tagged: handlers.with_tag)
define Auditable interface [
	summarize: fn(self) string,
	reset: fn(&self),
	tagged: fn(self, tag: string) Self,
]
`)
	// Package `handlers`: generic comptime handler using reflection + attrs + v.field(f) + collecting constructor.
	writeTestFile(t, tmp, "handlers/handlers.ll", `
const summarize[T] = fn(v: T) string: {
	const name = comptime comp.type_name[T]()
	let out = "${name}["
	comptime for(f in comp.fields_of[T]()): {
		comptime if(f.name != comp.fields_of[T]()[0].name): out.push(", ")
		out.push(f.name)
		comptime if(f.has_attr("redacted")): {
			out.push(": <redacted>")
		} else: comptime if(f.implements(Printable)): {
			out.push(": ")
			out.push(v.field(f).to_string())
		} else: {
			out.push(": <opaque>")
		}
	}
	out.push("]")
	out
}

const reset[T] = fn(v: &T): {
	comptime for(f in comp.fields_of[T]()): {
		comptime if(not f.has_attr("keep")): {
			comptime if(f.type_name == "int"): v.field(f) = 0
			else: comptime if(f.type_name == "string"): v.field(f) = ""
		}
	}
}

const with_tag[T] = fn(v: T, tag: string) T: {
	comptime for(f in comp.fields_of[T]()) T: {
		comptime if(f.name == "label" or f.name == "_label"): tag.copy()
		else: v.field(f).copy()
	}
}
`)
	// Package `models`: derives core + custom user interface on struct with `_` private fields.
	writeTestFile(t, tmp, "models/models.ll", `
import "customapp/traits"

@derive(Equatable, Copyable, Printable, traits.Auditable)
define Account struct [
	id: int,
	@keep
	_label: string,
	@redacted
	_token: string,
]

const make_account = fn(id: int, label: string, tok: string) Account:
	Account[id: id, _label: label.copy(), _token: tok.copy()]
`)
	writeTestFile(t, tmp, "main.ll", `
import "customapp/models"
import "customapp/traits"

const _CT_SUMMARY = comptime {
	let a = models.make_account(7, "ops".copy(), "secret-xyz".copy())
	a.summarize()
}

bind main = fn(): {
	assert(_CT_SUMMARY == "Account[id: 7, _label: ops, _token: <redacted>]", "ct custom derive")

	let a = models.make_account(42, "dev".copy(), "tok-123".copy())
	let b = a.copy()
	assert(a == b, "h16 private eq")
	assert(a.summarize() == "Account[id: 42, _label: dev, _token: <redacted>]", "custom summarize")

	let c = b.tagged("prod".copy())
	assert(c.summarize() == "Account[id: 42, _label: prod, _token: <redacted>]", "custom tagged")
	c.reset()
	assert(c.id == 0 and c.summarize() == "Account[id: 0, _label: prod, _token: <redacted>]", "custom mut reset kept _label")
	println("custom_derivable_ok")
}
`)

	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		out := runPkgProjectBackend(t, tmp, root, backend)
		if strings.TrimSpace(out) != "custom_derivable_ok" {
			t.Fatalf("[%s] unexpected stdout: %q", backend, out)
		}
	}

	// Verify caller outside `models` still CANNOT access `_token` directly even with `@derive` active.
	negDir := filepath.Join(t.TempDir(), "neg_priv")
	writeTestFile(t, negDir, "lowlight.toml", "[package]\nname = \"negapp\"\n")
	writeTestFile(t, negDir, "m/m.ll", `
@derive(Equatable)
define Secret struct [ _val: int ]
const make = fn(v: int) Secret: Secret[_val: v]
`)
	writeTestFile(t, negDir, "main.ll", `
import "negapp/m"
bind main = fn(): {
	let s = m.make(1)
	let _ = s._val
}
`)
	_, _, err := driver.Check(driver.Options{EntryPath: negDir, ProjectRoot: root})
	if err == nil {
		t.Fatal("expected private field access error outside declaring package, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unexported") {
		t.Fatalf("expected unexported field error, got: %v", err)
	}
}

func TestPhase6TuplesOptionalsConditionalDowngradeAndDiagnostics(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)

	src := `
import "std/sort"

define NoTraits struct [ x: int ]

// Has custom Printable, no Copyable/Equatable
define OnlyPrint struct [ n: int ]
bind OnlyPrint.to_string = fn(self) string: "OP(${self.n})"

// Runtime Eq triggers conditional const -> bind downgrade on @derive(Equatable) Wrap[T]
define RtEq struct [ v: int ]
bind RtEq.eq = fn(self, o: RtEq) bool: self.v == o.v

@derive(Equatable, Printable)
define Wrap[T] struct [ item: T ]

define TupKey +[int, string]

@derive(Equatable, Copyable, Printable)
define TupWrap struct [
	tup: +[int, string],
	opt?: ?+[int, string],
]

const _CT_TUP = comptime {
	let t1: +[int, string, ?int] = +[1, "alpha", 10]
	let t2: +[int, string, ?int] = +[1, "beta", 10]
	assert(t1 != t2 and t1 == t1.copy(), "ct tuple eq/copy")
	assert(+[1, "a"] < +[1, "b"], "ct tuple cmp")
	let w1 = Wrap[int][item: 5]
	assert(w1 == Wrap[int][item: 5], "const generic derive")
	t1.to_string()
}

bind print_vtable = fn(p: Printable) string: p.to_string()

bind main = fn(): {
	assert(_CT_TUP == "+[1, alpha, 10]", "ct tuple to_string")

	// Tuples in List, Set, Map, Distinct, Sort, and Interface VTable
	let tk1 = TupKey(+[2, "b".copy()])
	let tk2 = TupKey(+[1, "a".copy()])
	assert(tk2 < tk1 and tk1 != tk2, "distinct tuple cmp/eq")

	let kset: #[TupKey] = #[tk1.copy(), tk2.copy(), tk1.copy()]
	assert(kset.len() == 2, "distinct tuple set")

	let tlist = [+[3, "z".copy()], +[1, "x".copy()], +[2, "y".copy()]]
	sort.sort(&tlist)
	assert(tlist[0] == +[1, "x"] and tlist[2] == +[3, "z"], "tuple sort")

	let m: #[+[int, string]: TupWrap] = #[
		+[1, "k".copy()]: TupWrap[tup: +[9, "v".copy()], opt?: +[9, "v".copy()]],
		+[2, "none".copy()]: TupWrap[tup: +[0, "".copy()], opt?: none],
	]
	let g1? = m.get(+[1, "k"])
	let g2? = m.get(+[2, "none"])
	assert(g1?.opt? == g1?.tup and (g2?.opt? == none), "map of tuple -> struct with opt tuple")
	let g_miss? = m.get(+[9, "missing"])
	assert(g_miss? == none, "map miss none")

	// Printable VTable boxing a Tuple and a conditionally derived Wrap
	let vt1 = print_vtable(+[7, "vt".copy()])
	assert(vt1 == "+[7, vt]", "tuple Printable vtable")
	let vt2 = print_vtable(Wrap[OnlyPrint][item: OnlyPrint[n: 3]])
	assert(vt2 == "Wrap[item: OP(3)]", "conditional generic Printable vtable")

	// Conditional const downgrade verified at runtime
	assert(Wrap[RtEq][item: RtEq[v: 9]] == Wrap[RtEq][item: RtEq[v: 9]], "downgraded runtime eq")

	// Unused instances with non-Equatable fields compile cleanly
	let _unused_wrap = Wrap[NoTraits][item: NoTraits[x: 1]]
	let _unused_opt?: ?NoTraits = NoTraits[x: 2]
	println("tuples_downgrade_ok")
}
`
	out := runInlineStrict(t, "phase6_tuples_downgrade", src, false)
	if strings.TrimSpace(out) != "tuples_downgrade_ok" {
		t.Fatalf("unexpected stdout: %q", out)
	}

	// Negative 1: runtime-downgraded Wrap[RtEq].eq called in comptime must fail cleanly.
	negDowngrade := `
define RtEq struct [ v: int ]
bind RtEq.eq = fn(self, o: RtEq) bool: self.v == o.v
@derive(Equatable)
define Wrap[T] struct [ item: T ]
const BAD = comptime { Wrap[RtEq][item: RtEq[v: 1]] == Wrap[RtEq][item: RtEq[v: 2]] }
bind main = fn(): {}
`
	if _, _, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "neg_dg.ll"),
		SrcOverride: negDowngrade,
		ProjectRoot: root,
	}); err == nil {
		t.Fatal("expected comptime error when calling runtime-downgraded derived eq")
	}

	// Negative 2: concrete @derive(Equatable) with non-Equatable field points error at @derive line.
	negField := strings.Join([]string{
		"define Opaque struct [ v: int ]",
		"@derive(Equatable)",
		"define Holder struct [ o: Opaque ]",
		"bind main = fn(): {}",
	}, "\n")
	if _, _, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "neg_derive.ll"),
		SrcOverride: negField,
		ProjectRoot: root,
	}); err == nil {
		t.Fatal("expected @derive failure for non-Equatable field")
	} else if !strings.Contains(err.Error(), "cannot derive") || !strings.Contains(err.Error(), ":2:") {
		t.Fatalf("expected error on @derive line 2 with `cannot derive`, got: %v", err)
	}

	// Negative 3: generic @derivable interface must be rejected.
	negGenDerivable := `
const my_eq[T] = fn(a: T, b: T) bool: true
@derivable(eq: my_eq)
define BadIface[U] interface [ eq: fn(self, other: Self) bool ]
bind main = fn(): {}
`
	if _, _, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "neg_gen_derivable.ll"),
		SrcOverride: negGenDerivable,
		ProjectRoot: root,
	}); err == nil {
		t.Fatal("expected generic @derivable interface to be rejected")
	} else if !strings.Contains(strings.ToLower(err.Error()), "generic") {
		t.Fatalf("expected generic @derivable rejection message, got: %v", err)
	}
}

func runPkgProjectBackend(t *testing.T, projDir, root string, backend driver.BackendKind) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "app_"+string(backend))
	if _, err := driver.Build(driver.Options{
		EntryPath:   projDir,
		ProjectRoot: root,
		Backend:     backend,
		OutPath:     bin,
		ExtraCFlags: strictCFlags,
	}); err != nil {
		t.Fatalf("[%s] build failed: %v", backend, err)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "LOWLIGHT_CHECK_LEAKS=1", "ASAN_OPTIONS=detect_leaks=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("[%s] run failed: %v\noutput:\n%s", backend, err, string(out))
	}
	return string(out)
}
