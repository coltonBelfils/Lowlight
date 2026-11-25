package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func TestComptimeStaticBakingAndListCoWDifferential(t *testing.T) {
	t.Parallel()
	src := `
@derive(Copyable)
define Entry struct [
	key: string,
	vals: [int],
	opt?: ?string,
]

define ParseRes result [
	value: [string],
	Bad: string,
]

define Node struct [
	val: int,
	next?: ?Node,
]

define TempGuard struct [ id: int ]
const TempGuard.drop = fn(&self): {}

// @comptime_only symbols can freely hold Map/Set/custom Drop and are omitted from runtime codegen.
@comptime_only
const CT_DICT = #["alpha": 10, "beta": 20]

@comptime_only
const ct_secret_helper = fn(k: string) [string]: {
	let tmp = TempGuard[id: 1]
	let v = CT_DICT[k] ?? 0
	[k.copy(), "$k:$v:${tmp.id}"]
}

const double_it = fn(x: int) int: x * 2
const STATIC_FN = double_it

const STATIC_ENTRIES: [Entry] = [
	Entry[key: "one", vals: [1, 2], opt?: "tag1"],
	Entry[key: "two", vals: [3, 4, 5], opt?: none],
]

const STATIC_NESTED: [[string]] = [
	["a", "bb"],
	["ccc"],
]

const STATIC_RES! = ParseRes.value(ct_secret_helper("alpha"))
const STATIC_NONE_NODE?: ?Node = none
const STATIC_ARR: [2: Entry] = [
	Entry[key: "p", vals: [7], opt?: "P"],
	Entry[key: "q", vals: [], opt?: none],
]

const build_items = fn(n: int) [string]: {
	let out: [string] = []
	for(i in 0..n): out.push("s$i")
	out
}

bind main = fn(): {
	// 1. Read baked static globals directly (zero init, cap == 0, zero leaks on exit)
	assert(STATIC_ENTRIES.len() == 2, "entries len")
	assert(STATIC_ENTRIES[0].key == "one" and STATIC_ENTRIES[0].vals[1] == 2, "entry 0")
	assert(STATIC_ENTRIES[0].opt? == "tag1" and STATIC_ENTRIES[1].opt? == none, "entry opt")
	assert(STATIC_NESTED[0][1] == "bb" and STATIC_NESTED[1][0] == "ccc", "nested list")
	assert(STATIC_NONE_NODE? == none, "none boxed opt")
	assert(STATIC_FN(21) == 42, "static fn")
	assert(STATIC_ARR[0].key == "p" and STATIC_ARR[1].vals.len() == 0, "static array")

	match(STATIC_RES!):
		when value(items): assert(items.len() == 2 and items[1] == "alpha:10:1", "baked result")
		default Bad(msg): panic("unexpected: $msg")

	// 2. Unmodified let xs = comptime ... (remains cap == 0 static slice cleanly dropped at exit)
	let ro_ints = comptime [10, 20, 30]
	assert(ro_ints.len() == 3 and ro_ints[2] == 30, "unmodified comptime int list")
	let ro_strs = comptime build_items(3)
	assert(ro_strs.join(",") == "s0,s1,s2", "unmodified comptime string list")

	// 3. Mutate let copies initialized from comptime via push / set / pop / truncate / remove / swap / indexing
	let a = comptime [1, 2, 3, 4]
	a.push(5)
	assert(a.set(0, 99).is_ok(), "CoW set")
	assert(a.pop() == 5, "CoW pop")
	a.truncate(3)
	a[1] = 88
	assert(a.to_string() == "[99, 88, 3]", "CoW int mutations")

	let b = comptime [10, 20, 30, 40]
	b.swap(0, 3)
	assert(b.remove(1) == 20, "CoW remove")
	assert(b.swap_remove(0) == 40, "CoW swap_remove")
	assert(b.to_string() == "[10, 30]", "CoW remove/swap_remove ints")

	let c = comptime ["aa", "bb", "cc", "dd"]
	c[0].push("!")
	assert(c.set(1, "BB").is_ok(), "CoW List[string] set")
	assert(c.pop() == "dd", "CoW List[string] pop")
	c.truncate(2)
	c.push("EE")
	assert(c.to_string() == "[aa!, BB, EE]", "CoW List[string] mutations")

	let d = comptime build_items(4)
	assert(d.remove(1) == "s1", "CoW List[string] remove")
	assert(d.swap_remove(0) == "s0", "CoW List[string] swap_remove")
	assert(d.join("|") == "s3|s2", "CoW List[string] rem result")

	let e = comptime STATIC_ENTRIES.copy()
	e[0].vals.push(99)
	e[0].key.push("_mod")
	assert(e[0].vals.len() == 3 and e[0].key == "one_mod", "CoW List[Struct] nested mutation")

	// 4. .copy() on static comptime value
	let copied = (comptime ["x", "y"]).copy()
	copied.push("z")
	assert(copied.to_string() == "[x, y, z]", "copied comptime")

	// 5. Verify original baked static global stayed untouched
	assert(STATIC_ENTRIES[0].key == "one" and STATIC_ENTRIES[0].vals.len() == 2, "static unchanged")

	// 6. Runtime local const using compile-time only map via comptime folding
	const LOCAL_SUM = comptime {
		let m = #["k1": 5, "k2": 7]
		(m["k1"] ?? 0) + (m["k2"] ?? 0)
	}
	assert(LOCAL_SUM == 12, "local const fold")

	println("static_baking_ok")
}
`
	for _, release := range []bool{false, true} {
		out := runInlineStrict(t, "comptime_static_baking", src, release)
		if got := strings.TrimSpace(out); got != "static_baking_ok" {
			t.Fatalf("release=%v: got %q, want %q", release, got, "static_baking_ok")
		}
	}

	// Verify emitted LLVM & C11 omit @comptime_only symbols and CT_DICT
	root := projectRoot(t)
	entry := filepath.Join(t.TempDir(), "omit_check.ll")
	opts := driver.Options{EntryPath: entry, SrcOverride: src, ProjectRoot: root}
	llvmOut, _, err := driver.EmitLLVM(opts)
	if err != nil {
		t.Fatalf("EmitLLVM failed: %v", err)
	}
	for _, forbidden := range []string{"ct_secret_helper", "CT_DICT"} {
		if strings.Contains(llvmOut, forbidden) {
			t.Fatalf("expected %q to be omitted from LLVM IR", forbidden)
		}
	}
	cOut, _, err := driver.EmitC(opts)
	if err != nil {
		t.Fatalf("EmitC failed: %v", err)
	}
	for _, forbidden := range []string{"ct_secret_helper", "CT_DICT"} {
		if strings.Contains(cOut, forbidden) {
			t.Fatalf("expected %q to be omitted from C11", forbidden)
		}
	}
}

func TestComptimeStaticBakingSplitPackages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := projectRoot(t)
	libDir := filepath.Join(dir, "tbl")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	libSrc := `
const WORDS: [string] = ["north", "south", "east", "west"]

bind pick_mutated = fn(idx: int) string: {
	let xs = comptime ["zero", "one", "two"]
	xs[idx] = "${xs[idx]}*"
	xs[idx].copy()
}
`
	mainSrc := `
import "./tbl" as tbl

bind main = fn(): {
	let w = tbl.WORDS[2]
	let m = tbl.pick_mutated(1)
	assert(w == "east" and m == "one*", "split pkg static baking")
	println("split_ok")
}
`
	if err := os.WriteFile(filepath.Join(libDir, "tbl.ll"), []byte(libSrc), 0644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "main.ll")
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}

	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		var stdout, stderr bytes.Buffer
		rc, err := driver.Run(driver.Options{
			EntryPath:   mainPath,
			ProjectRoot: root,
			Backend:     backend,
			ExtraCFlags: strictCFlags,
			Env: []string{
				"LOWLIGHT_CHECK_LEAKS=1",
				"ASAN_OPTIONS=detect_leaks=0",
			},
		}, nil, &stdout, &stderr)
		if err != nil {
			t.Fatalf("[%s] split package run failed: %v\nstderr:\n%s", backend, err, stderr.String())
		}
		if rc != 0 || strings.TrimSpace(stdout.String()) != "split_ok" {
			t.Fatalf("[%s] unexpected result rc=%d out=%q stderr=%s", backend, rc, stdout.String(), stderr.String())
		}
	}
}
