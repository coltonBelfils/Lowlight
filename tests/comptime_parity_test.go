package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"lowlight/internal/driver"
)

func TestComptimeRuntimeParityAndSortCoW(t *testing.T) {
	t.Parallel()
	src := `
import "std/sort"

@derive(Copyable)
define Acc struct [
	sum: int,
	tag: string,
]

const bump = fn(x: &int): { x += 5 }
const bump_acc = fn(a: &Acc, delta: int): {
	a.sum += delta
	a.tag.push("!")
}

@comptime_only
const compute_map_set_summary = fn() string: {
	let m = #["alpha": 10, "beta": 20]
	let _ = m.update("alpha", cl(v: &int): { v *= 3 })
	let s1 = #[1, 2, 3]
	let s2 = #[2, 3, 4]
	let u = s1.union(s2).to_list()
	let d = s1.difference(s2).to_list()
	let i = s1.intersection(s2).to_list()
	"${m["alpha"] ?? 0}:${u.len()}:${d.len()}:${i.len()}"
}

const compute_shared = fn() string: {
	let n = 10
	bump(&n)
	let a = Acc[sum: 100, tag: "hi"]
	bump_acc(&a, 23)

	let f1 = 0.1 + 0.2
	let f2 = 1.0 / 3.0
	let f3 = f32(0.5)
	let finf = 1.0 / 0.0
	let fnan = 0.0 / 0.0

	let chars = "héllo 世界".chars()
	let evens = for(x in 0..6) [int]: if(x % 2 == 0): x * 10 else: -x

	"$n|${a.sum}:${a.tag}|$f1|$f2|$f3|$finf|$fnan|${chars.len()}:${chars[1]}:$evens"
}

const make_nums = fn() [int]: [5, 1, 4, 2, 3]

const CT_RES = compute_shared()
const CT_MS = compute_map_set_summary()
const BAKED_NUMS = [5, 1, 4, 2, 3]

bind main = fn(): {
	let rt_res = compute_shared()
	assert(CT_RES == rt_res, "comptime/runtime parity mismatch: ct=${CT_RES} rt=${rt_res}")
	println("$CT_RES|$CT_MS")

	// Static baked Lists sorted in place must CoW-promote before mutating .rodata.
	let nums1 = comptime [5, 1, 4, 2, 3]
	let nums2 = comptime make_nums()
	nums1.sort()
	nums2.stable_sort()
	nums2.reverse()

	let words = comptime ["pear", "apple", "fig"]
	words.sort_by(cl(a: string, b: string) int: a.len() - b.len())

	println("$BAKED_NUMS|$nums1|$nums2|$words")
}
`
	want := "15|123:hi!|0.3|0.333333|0.5|inf|nan|8:é:[0, -1, 20, -3, 40, -5]|30:4:1:2\n[5, 1, 4, 2, 3]|[1, 2, 3, 4, 5]|[5, 4, 3, 2, 1]|[fig, pear, apple]\n"
	for _, release := range []bool{false, true} {
		got := runInlineStrict(t, "comptime_parity", src, release)
		if got != want {
			t.Fatalf("release=%v mismatch:\ngot:\n%s\nwant:\n%s", release, got, want)
		}
	}
}

func TestComptimeStaticRodataWriteTraps(t *testing.T) {
	t.Parallel()
	// Intentional unsafe write into the static .rodata backing of a baked List (cap == 0)
	// must compile cleanly and fault at runtime when dereferencing read-only static storage.
	src := `
bind main = fn(): {
	let xs = comptime [10, 20, 30]
	unsafe {
		ptr_write(xs.data, 999)
	}
	println(xs[0])
}
`
	root := projectRoot(t)
	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		dir := t.TempDir()
		entry := filepath.Join(dir, "rodata_trap_"+string(backend)+".ll")
		outBin := filepath.Join(dir, "rodata_trap_"+string(backend))
		binPath, err := driver.Build(driver.Options{
			EntryPath:   entry,
			OutPath:     outBin,
			SrcOverride: src,
			ProjectRoot: root,
			Backend:     backend,
			ExtraCFlags: strictCFlags,
		})
		if err != nil {
			t.Fatalf("[%s] unexpected compile error: %v", backend, err)
		}
		var outBuf bytes.Buffer
		cmd := exec.Command(binPath)
		cmd.Env = append(cmd.Environ(), "ASAN_OPTIONS=detect_leaks=0")
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		runErr := cmd.Run()
		if runErr == nil {
			t.Fatalf("[%s] expected writing through .rodata backing pointer to fault/crash, but exited 0 with output %q", backend, outBuf.String())
		}
	}
}

func TestComptimeStdCoreSimulatedHeapRuntimeParity(t *testing.T) {
	t.Parallel()
	src := `
@derive(Copyable)
define Key struct [ k: int ]
const Key.hash = fn(self) u64: u64(self.k * 2654435761)
const Key.eq = fn(self, other: Key) bool: self.k == other.k

const compute_summary = fn() string: {
	let m: Map[Key, [string]] = #[]
	m.insert(Key[k: 2], ["two", "zwei"])
	m.insert(Key[k: 1], ["one", "eins"])
	let removed = m.remove(Key[k: 2]) ?? []
	let f_str = (0.1 + 0.2).to_string()
	let sz = type.size_of[Key]()
	let al = type.align_of[Key]()
	let pl = type.is_plain[Key]()
	let err_msg = Error.new("bad").wrap("io").summary()
	let rem_str = removed.to_string()
	let m_str = m[Key[k: 1]] ?? []
	// B10: Map[string, string] remove, push, overwrite
	let sm = #["a": "one", "b": "two"]
	sm.insert("a", "ONE")
	sm.push(MapEntry[string, string][key: "c", value: "three"])
	let srem? = sm.remove("b")
	let sm_out = "${sm["a"] ?? ""}:${srem? ?? ""}:${sm["c"] ?? ""}:${sm.len()}"
	"$f_str|$rem_str|$m_str|$sz:$al:$pl|$err_msg|$sm_out"
}

const CT_SUMMARY = compute_summary()

bind main = fn(): {
	let rt_summary = compute_summary()
	assert(CT_SUMMARY == rt_summary, "comptime summary != runtime summary")
	println(CT_SUMMARY)
}
`
	want := "0.3|[two, zwei]|[one, eins]|8:8:true|io: bad|ONE:two:three:2\n"
	for _, release := range []bool{false, true} {
		got := runInlineStrict(t, "comptime_simheap_parity", src, release)
		if got != want {
			t.Fatalf("release=%v mismatch:\ngot:\n%s\nwant:\n%s", release, got, want)
		}
	}
}

func TestComptimePhase4ConstGenericsAndTargetParity(t *testing.T) {
	t.Parallel()
	src := `
const TARGET_PTR_BITS = comp.TARGET.ptr_bits
const HOST_WORD_BYTES: int = comptime if(comp.TARGET.ptr_size == 8): 8 else: 4

comptime if(comp.TARGET.ptr_size == 8): {
	const WORD_TAG = "64bit"
} else: {
	const WORD_TAG = "32bit"
}

@derive(Printable)
define Matrix[R: const int, C: const int] struct [
	data: [R * C: int],
]

const Matrix[R, C].eq = fn(self, other: Matrix[R, C]) bool: {
	for(i in 0..(R * C)): {
		if(self.data[i] != other.data[i]): func <- false
	}
	true
}

const Matrix[R, C].sum = fn(self) int: {
	let s = 0
	for(v in self.data): s += v
	s
}

@derive(Equatable)
define Op enum [ add, mul ]

const eval_op[O: const Op, NEG: const int, CH: const char, FLAG: const bool, TAG: const string] = fn(x: int) string: {
	let base = if(FLAG): {
		if(O == Op.add): x + NEG else: x * NEG
	} else: 0
	"$base:$CH:$TAG"
}

const dot[N: const int] = fn(a: [N: int], b: [N: int]) int: {
	let acc = 0
	for(i in 0..N): acc += a[i] * b[i]
	acc
}

const CT_DOT = dot([3: 2, 3, 4], [3: 5, 6, 7])
const CT_OP1 = eval_op[Op.add, -5, 'X', true, "a b"](12)
const CT_OP2 = eval_op[Op.mul, -5, 'Y', true, "a_b"](3)

bind main = fn(): {
	let m1: Matrix[1 + 1, 2] = Matrix[2, 2][data: [4: 10, 20, 30, 40]]
	let m2: Matrix[2, 2] = m1
	assert(m1 == m2 and m1.sum() == 100)
	let rt_dot = dot([3: 2, 3, 4], [3: 5, 6, 7])
	let rt_op1 = eval_op[Op.add, -5, 'X', true, "a b"](12)
	let rt_op2 = eval_op[Op.mul, -5, 'Y', true, "a_b"](3)
	assert(CT_DOT == rt_dot and CT_OP1 == rt_op1 and CT_OP2 == rt_op2)
	println("$TARGET_PTR_BITS:$HOST_WORD_BYTES:$WORD_TAG:$CT_DOT:${m1.sum()}:$CT_OP1|$CT_OP2")
}
`
	want := "64:8:64bit:56:100:7:X:a b|-15:Y:a_b\n"
	for _, release := range []bool{false, true} {
		got := runInlineStrict(t, "comptime_phase4_const_generics", src, release)
		if got != want {
			t.Fatalf("release=%v mismatch:\ngot:\n%s\nwant:\n%s", release, got, want)
		}
	}
}

func TestComptimePhase4LocalControlFlowParity(t *testing.T) {
	src := `
define Pair struct [
	a: int,
	b: int,
]

define Val enum [
	num: int,
	pair: Pair,
	empty,
]

const choose_size[T] = fn() int: {
	comptime if(type.size_of[T]() == 8):
		800
	else comptime if(type.size_of[T]() == 4):
		400
	else:
		100
}

const ct_eval = fn() int: {
	let sum = 0
	comptime for(i in 0..5): {
		comptime if(i == 2): {
			next <-
		} else: {
			comptime for(j in 0..=1): {
				sum += i * 10 + j
			}
		}
	}
	sum
}

const CT_RES = ct_eval()
const CT_LIST = comptime for(i in 0..5) [int]: {
	comptime if(i % 2 == 1): {
		next <-
	} else: {
		i * 3
	}
}

const CT_EMPTY = comptime for(i in 0..0) [int]: i else: [42, 43]

@comptime_only
const CT_MAP = {
	let mp = Map[string, int].new()
	mp.insert("k1", 10)
	mp.insert("k2", 20)
	mp
}

@comptime_only
const TAGS = ["alpha", "beta", "gamma"]

bind main = fn(): {
	comptime if(false): {
		nonexistent_runtime_call()
	}

	let sz64 = choose_size[int]()
	let sz32 = choose_size[i32]()
	let sz8 = choose_size[bool]()

	let matched = comptime match(Val.pair(Pair[a: 9, b: 4])):
		when Val.num(n): n * 10
		when Val.pair(p): p.a * p.b + 1
		when Val.empty: bad_untaken()
		default: 0

	let rt_sum = 0
	comptime for(i in 0..5): {
		if(i == 2): {
			next <-
		}
		comptime for(j in 0..=1): {
			rt_sum += i * 10 + j
		}
	}

	let rt_list = comptime for(i in 0..5) [int]: {
		if(i % 2 == 1): {
			next <-
		}
		i * 3
	}

	let rt_empty = comptime for(i in 0..0) [int]: i else: [42, 43]

	let early = 0
	comptime for(i in 1..10): {
		if(i == 4): for <-
		early += i
	} else: {
		early += 1000
	}

	let completed = 0
	comptime for(i in 1..=3): {
		completed += i
	} else: {
		completed += 100
	}

	let str_sum = 0
	comptime for(i, ch in "ABC"): {
		str_sum += i + int(ch)
	}

	let map_sum = 0
	comptime for(k, v in CT_MAP): {
		map_sum += k.len() + v
	}

	let collected_tags = comptime for(idx, tg in TAGS) [string]: "$idx:$tg"
	let rt_set = comptime for(x in [1, 2, 2, 3]) #[int]: x * 10
	let if_unwind = comptime if(true): {
		if <- 77
		0
	} else: 0

	assert(CT_RES == rt_sum)
	assert(CT_LIST.len() == 3 and rt_list.len() == 3)
	assert(CT_LIST[0] == 0 and CT_LIST[1] == 6 and CT_LIST[2] == 12)
	assert(rt_list[0] == 0 and rt_list[1] == 6 and rt_list[2] == 12)
	assert(CT_EMPTY.len() == 2 and rt_empty.len() == 2 and CT_EMPTY[0] == 42 and rt_empty[1] == 43)
	assert(early == 6 and completed == 106)
	assert(collected_tags.len() == 3 and collected_tags[1] == "1:beta" and rt_set.len() == 3 and if_unwind == 77)
	println("$sz64:$sz32:$sz8:$matched:$CT_RES:$rt_sum:$early:$completed:$str_sum:$map_sum")
}
`
	want := "800:400:100:37:164:164:6:106:201:34\n"
	for _, release := range []bool{false, true} {
		got := runInlineStrict(t, "comptime_phase4_local_cf", src, release)
		if got != want {
			t.Fatalf("release=%v mismatch:\ngot:\n%s\nwant:\n%s", release, got, want)
		}
	}
}

func TestComptimeParityEmbedAssets(t *testing.T) {
	dir := t.TempDir()
	// Include NUL followed by ASCII digit '7' to exercise C11 octal-escape correctness (\000 vs \0).
	strBytes := []byte("alpha\x007-utf8-\xc3\xa9")
	if err := os.WriteFile(filepath.Join(dir, "msg.txt"), strBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	rawBytes := []byte{0x00, 0x37, 0xff, 0xfe, 0x10}
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), rawBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.bin"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	mainPath := filepath.Join(dir, "main.ll")
	src := `
const MSG = comp.embed_str("msg.txt")
const BIN = comp.embed_bytes("data.bin")
const EMPTY = comp.embed_bytes("empty.bin")

@comptime_only
const first_plus_last = fn() int: {
	let b = comp.embed_bytes("data.bin")
	b[0] = u8(42)
	b.push(u8(7))
	int(b[0]) + int(b[b.len() - 1])
}
const CT_SUM = first_plus_last()

bind main = fn(): {
	assert(MSG.len() == 15)
	assert(MSG.byte_at(5) == u8(0) and MSG.byte_at(6) == u8('7'))
	assert(BIN.len() == 5 and BIN[0] == u8(0) and BIN[1] == u8(55) and BIN[2] == u8(255) and BIN[3] == u8(254) and BIN[4] == u8(16))
	assert(EMPTY.len() == 0)

	// CoW mutation from .rodata static backing (cap == 0 -> heap copy on write/push, no-op drop on consts)
	let mut_bin = BIN.copy()
	mut_bin[0] = u8(99)
	mut_bin.push(u8(77))
	assert(BIN[0] == u8(0) and BIN.len() == 5)
	assert(mut_bin[0] == u8(99) and mut_bin[5] == u8(77) and mut_bin.len() == 6)

	let empty_copy = EMPTY.copy()
	empty_copy.push(u8(11))
	assert(empty_copy.len() == 1 and empty_copy[0] == u8(11))

	println("${MSG.len()}:${BIN.len()}:$CT_SUM:${mut_bin[0]}:${mut_bin[5]}")
}
`
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "15:5:49:99:77\n"
	root := projectRoot(t)
	for _, release := range []bool{false, true} {
		for _, backend := range []driver.BackendKind{driver.BackendC11, driver.BackendLLVM} {
			var stdout, stderr bytes.Buffer
			rc, err := driver.Run(driver.Options{
				EntryPath:   mainPath,
				ProjectRoot: root,
				Backend:     backend,
				Release:     release,
				ExtraCFlags: strictCFlags,
				Env: []string{
					"LOWLIGHT_CHECK_LEAKS=1",
					"ASAN_OPTIONS=detect_leaks=0",
				},
			}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("backend=%s release=%v error:\n%v\nstderr:\n%s", backend, release, err, stderr.String())
			}
			if rc != 0 {
				t.Fatalf("backend=%s release=%v rc=%d\nstdout:\n%s\nstderr:\n%s", backend, release, rc, stdout.String(), stderr.String())
			}
			got := stdout.String()
			if got != want {
				t.Fatalf("backend=%s release=%v mismatch:\ngot:\n%s\nwant:\n%s", backend, release, got, want)
			}
		}
	}
}

func TestComptimeParityFieldElemAndCollectFor(t *testing.T) {
	src := `
@table("items")
define Entry struct [
	@col("id")
	id: int,
	@col("name")
	name: string = "anon",
	note?: ?string,
]

define CustomBox struct [
	label: string,
	drops: ptr[int],
]

bind CustomBox.drop = fn(&self): {
	if(unsafe ptr_addr(self.drops) != u64(0)): {
		unsafe { self.drops[0] += 1 }
	}
}

bind CustomBox.take_label = fn(own self) string:
	own self.field(comp.fields_of[CustomBox]()[0])

define DropPair struct [
	a: CustomBox,
	b: CustomBox,
	c: CustomBox,
]

define Bag struct [
	first: string,
	second: string,
	def: string = "fallback_" + "str",
]

@comptime_only
const ct_work = fn() string: {
	let e = Entry[id: 1, name: "ct", note?: "yes"]
	const fs = comp.fields_of[Entry]()
	e.field(fs[0]) += 9
	let bump = fn(r: &int): { r += 5 }
	bump(&e.field("id"))

	let built = comptime for(f in fs) Entry: {
		comptime if(f.name == "id"): 77
		else: { next <- }
	}
	let t = comptime for(el in comp.elems_of[+[int, string]]()) +[int, string]: {
		comptime if(el.index == 0): 42
		else: el.type_name.copy()
	}
	let arr = comptime for(el in comp.elems_of[[3: int]]()) [3: int]: el.index * 3 + 1
	"${e.id}:${e.field("name")}:${built.id}:${built.name}:${t.0}:${t.1}:${arr[2]}"
}

const CT_VAL = ct_work()

bind main = fn(): {
	let drop_cnt = 0
	// 1. Runtime field/elem read, mutation, & borrow, ref, ?.field, own self.field
	let e = Entry[id: 1, name: "ct", note?: "yes"]
	e.field(comp.fields_of[Entry]()[0]) += 9
	let bump = fn(r: &int): { r += 5 }
	bump(&e.field("id"))

	let opt?: ?Entry = Entry[id: 88, name: "optn"]
	let opt_id = opt?.field(comp.fields_of[Entry]()[0]) ?? -1
	assert(opt_id == 88)

	let ar = Arena.new()
	let re = ar.alloc(Entry[id: 100, name: "arena"])
	re.field(comp.fields_of[Entry]()[0]) += 23
	assert(re.id == 123)

	let cbox = CustomBox[label: "box_lbl", drops: unsafe addr_of(drop_cnt)]
	let taken = cbox.take_label()
	assert(taken == "box_lbl")
	assert(drop_cnt == 1)

	// Verify early break mid-collecting comptime for drops ALREADY populated fields (0 & 1) and NOT unpopulated field 2!
	// Check both enclosing block unwind ('dp_brk <-') and loop break ('for <-').
	let early_drops = 0
	{
		let dp_res = dp_brk: {
			comptime for(f in comp.fields_of[DropPair]()) DropPair: {
				if(f.name == "c"): {
					dp_brk <- DropPair[
						a: CustomBox[label: "fb_a", drops: unsafe addr_of(early_drops)],
						b: CustomBox[label: "fb_b", drops: unsafe addr_of(early_drops)],
						c: CustomBox[label: "fb_c", drops: unsafe addr_of(early_drops)],
					]
				}
				CustomBox[label: "iter_${f.name}", drops: unsafe addr_of(early_drops)]
			}
		}
		assert(dp_res.a.label == "fb_a")
		assert(early_drops == 2, "expected 2 partial iter drops on block unwind, got $early_drops")
	}
	assert(early_drops == 5, "expected 5 total drops after dp_res scope exit, got $early_drops")

	let early_drops2 = 0
	{
		let lp_res = comptime for(f in comp.fields_of[DropPair]()) DropPair: {
			if(f.name == "c"): {
				for <- DropPair[
					a: CustomBox[label: "lp_a", drops: unsafe addr_of(early_drops2)],
					b: CustomBox[label: "lp_b", drops: unsafe addr_of(early_drops2)],
					c: CustomBox[label: "lp_c", drops: unsafe addr_of(early_drops2)],
				]
			}
			CustomBox[label: "iter_${f.name}", drops: unsafe addr_of(early_drops2)]
		}
		assert(lp_res.a.label == "lp_a")
		assert(early_drops2 == 2, "expected 2 partial iter drops on for<- break, got $early_drops2")
	}
	assert(early_drops2 == 5, "expected 5 total drops after lp_res scope exit, got $early_drops2")

	// 2. Runtime struct/tuple/array collecting comptime for + Resource values + defaults + early break
	let bag = comptime for(f in comp.fields_of[Bag]()) Bag: {
		comptime if(f.has_default): next <-
		else: "v_${f.name}"
	}
	assert(bag.first == "v_first" and bag.second == "v_second" and bag.def == "fallback_str")

	// Early break mid-collecting comptime for after iteration 0 & 1 allocated Resource strings:
	// elems 0 and 1 must drop cleanly in LIFO order with 0 leaked bytes!
	let early_bag = out: {
		comptime for(f in comp.fields_of[Bag]()) Bag: {
			if(f.name == "def"): {
				out <- Bag[first: "early1", second: "early2", def: "early3"]
			}
			"tmp_${f.name}_alloc"
		}
		Bag[first: "x", second: "y"]
	}
	assert(early_bag.first == "early1" and early_bag.def == "early3")

	// Tuple and StackArray with Resource elements + early break cleanup
	let rtuple = comptime for(el in comp.elems_of[+[string, int, string]]()) +[string, int, string]: {
		comptime if(el.index == 1): 303
		else: "s_${el.index}"
	}
	assert(rtuple.0 == "s_0" and rtuple.1 == 303 and rtuple.2 == "s_2")

	let rarr = comptime for(el in comp.elems_of[[3: string]]()) [3: string]: "el_${el.index}"
	assert(rarr[0] == "el_0" and rarr[2] == "el_2")
	rarr.elem(1) = "el_mod"
	rtuple.elem(1) += 1
	assert(rarr.elem(1) == "el_mod" and rtuple.elem(1) == 304)
	rtuple.elem(1) -= 1

	// Crossable projection at runtime
	let f0_name = comp.fields_of[Entry]()[0].name
	let f0_col = comp.fields_of[Entry]()[0].attrs[0].args[0]
	let triple = comp.TARGET.triple
	assert(f0_name == "id" and f0_col == "id" and triple.len() > 0)

	let built = comptime for(f in comp.fields_of[Entry]()) Entry: {
		comptime if(f.name == "id"): 77
		else: { next <- }
	}
	let t = comptime for(el in comp.elems_of[+[int, string]]()) +[int, string]: {
		comptime if(el.index == 0): 42
		else: el.type_name.copy()
	}
	let arr = comptime for(el in comp.elems_of[[3: int]]()) [3: int]: el.index * 3 + 1
	let rt_val = "${e.id}:${e.field("name")}:${built.id}:${built.name}:${t.0}:${t.1}:${arr[2]}"
	assert(CT_VAL == rt_val, "parity mismatch: $CT_VAL vs $rt_val")
	println("$CT_VAL|${bag.def}|${early_bag.first}|${rtuple.1}|${rarr[1]}|$f0_col")
}
`
	want := "15:ct:77:anon:42:string:7|fallback_str|early1|303|el_mod|id\n"
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.ll")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	root := projectRoot(t)
	for _, release := range []bool{false, true} {
		for _, backend := range []driver.BackendKind{driver.BackendC11, driver.BackendLLVM} {
			var stdout, stderr bytes.Buffer
			rc, err := driver.Run(driver.Options{
				EntryPath:   mainPath,
				ProjectRoot: root,
				Backend:     backend,
				Release:     release,
				ExtraCFlags: strictCFlags,
				Env: []string{
					"LOWLIGHT_CHECK_LEAKS=1",
					"ASAN_OPTIONS=detect_leaks=0",
				},
			}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("backend=%s release=%v error:\n%v\nstderr:\n%s", backend, release, err, stderr.String())
			}
			if rc != 0 {
				t.Fatalf("backend=%s release=%v rc=%d\nstdout:\n%s\nstderr:\n%s", backend, release, rc, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != want {
				t.Fatalf("backend=%s release=%v mismatch:\ngot:\n%s\nwant:\n%s", backend, release, got, want)
			}
		}
	}
}

func TestComptimeFieldPrivacyMultiPkg(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "secret")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	modSrc := `
define Vault struct [
	pub_v: int = 1,
	_priv_v: int = 42,
]
`
	mainSrc := `
import "secret"

bind main = fn(): {
	let v = secret.Vault[]
	let _ = v.field(comp.fields_of[secret.Vault]()[1])
}
`
	if err := os.WriteFile(filepath.Join(sub, "secret.ll"), []byte(modSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "main.ll")
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	_, err := driver.Run(driver.Options{
		EntryPath:   mainPath,
		ProjectRoot: projectRoot(t),
		Backend:     driver.BackendC11,
	}, nil, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected unexported field access error across packages, succeeded cleanly")
	}
	if !bytes.Contains([]byte(err.Error()+stderr.String()), []byte("cannot access unexported field \"_priv_v\"")) {
		t.Fatalf("unexpected error for private field access:\nerr=%v\nstderr=%s", err, stderr.String())
	}
}

func TestComptimeParityVariantIntrinsics(t *testing.T) {
	src := `
define Sparse enum [
	Zero = 3,
	One = 9,
	Two = 20,
]

define CustomPay struct [
	txt: string,
	drops: ptr[int],
]

bind CustomPay.drop = fn(&self): {
	if(unsafe ptr_addr(self.drops) != u64(0)): {
		unsafe { self.drops[0] += 1 }
	}
}

define Msg enum [
	Ping,
	Text: string,
	Custom: CustomPay,
	Num: int,
]

const Msg.take_text = fn(own self) string:
	own self.payload(comp.variants_of[Msg]()[1])

define ParseRes result [
	value: int,
	ErrMsg: string,
	Done,
]

@comptime_only
const ct_variants = fn() string: {
	let sp = make_variant[Sparse](comp.variants_of[Sparse]()[1])
	assert(variant_index(sp) == 1)
	assert(sp.is_variant(comp.variants_of[Sparse]()[1]))

	let m = make_variant[Msg](comp.variants_of[Msg]()[3], 10)
	m.payload(comp.variants_of[Msg]()[3]) += 5
	assert(m.payload("Num") == 15)

	let ms = make_variant[Msg](comp.variants_of[Msg]()[1], "ct")
	if(ms.as_variant(comp.variants_of[Msg]()[1]) as &s): {
		s.push("_edit")
	}

	let r! = make_variant[ParseRes](comp.variants_of[ParseRes]()[1], "bad")
	let ri = variant_index(r!)
	let taken = Msg.Text("moved").take_text()
	"${variant_index(sp)}:${m.payload(comp.variants_of[Msg]()[3])}:${ms.payload("Text")}:$ri:$taken"
}

const CT_VAR = ct_variants()

bind make_temp_msg = fn() Msg:
	make_variant[Msg](comp.variants_of[Msg]()[1], "temp_" + "msg")

bind main = fn(): {
	let sp = make_variant[Sparse](comp.variants_of[Sparse]()[1])
	assert(variant_index(sp) == 1)
	assert(sp.is_variant(comp.variants_of[Sparse]()[1]))

	let m = make_variant[Msg](comp.variants_of[Msg]()[3], 10)
	m.payload(comp.variants_of[Msg]()[3]) += 5
	let bump = fn(x: &int): { x += 0 }
	bump(&m.payload(comp.variants_of[Msg]()[3]))
	assert(m.payload("Num") == 15)

	let ms = make_variant[Msg](comp.variants_of[Msg]()[1], "ct".copy())
	if(&ms.as_variant(comp.variants_of[Msg]()[1]) as s): {
		s.push("_edit")
	}

	// Non-Copyable Resource (CustomPay) zero-copy borrow + mutation via as_variant & ref[Msg]
	let drop_cnt = 0
	{
		let cm = make_variant[Msg](comp.variants_of[Msg]()[2], CustomPay[txt: "noncopy".copy(), drops: unsafe addr_of(drop_cnt)])
		if(cm.as_variant(comp.variants_of[Msg]()[2]) as &cp): {
			cp.txt.push("!")
		} else: {
			panic("expected Custom")
		}
		assert(cm.payload(comp.variants_of[Msg]()[2]).txt == "noncopy!")
	}
	assert(drop_cnt == 1, "expected 1 CustomPay drop, got $drop_cnt")

	// Temp rvalue enum inside if(make_temp_msg().as_variant(vr) as s): must not leak!
	if(make_temp_msg().as_variant(comp.variants_of[Msg]()[1]) as s): {
		assert(s == "temp_msg")
	} else: {
		panic("expected temp_msg")
	}

	// Arena ref[Msg] + optional chaining ?.is_variant / ?.payload
	let ar = Arena.new()
	let rm = ar.alloc(make_variant[Msg](comp.variants_of[Msg]()[3], 41))
	rm.payload(comp.variants_of[Msg]()[3]) += 1
	assert(variant_index(rm) == 3 and rm.payload("Num") == 42)
	let opt_m?: ?Msg = make_variant[Msg](comp.variants_of[Msg]()[1], "opt".copy())
	assert((opt_m?.is_variant(comp.variants_of[Msg]()[1]) ?? false))
	assert((opt_m?.payload(comp.variants_of[Msg]()[1]) ?? "") == "opt")

	let r! = make_variant[ParseRes](comp.variants_of[ParseRes]()[1], "bad")
	let ri = variant_index(r!)
	let taken = Msg.Text("moved".copy()).take_text()
	let rt_var = "${variant_index(sp)}:${m.payload(comp.variants_of[Msg]()[3])}:${ms.payload("Text")}:$ri:$taken"
	assert(CT_VAR == rt_var, "parity mismatch: $CT_VAR vs $rt_var")
	println(rt_var)
}
`
	want := "1:15:ct_edit:1:moved\n"
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.ll")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	root := projectRoot(t)
	for _, release := range []bool{false, true} {
		for _, backend := range []driver.BackendKind{driver.BackendC11, driver.BackendLLVM} {
			var stdout, stderr bytes.Buffer
			rc, err := driver.Run(driver.Options{
				EntryPath:   mainPath,
				ProjectRoot: root,
				Backend:     backend,
				Release:     release,
				ExtraCFlags: strictCFlags,
				Env: []string{
					"LOWLIGHT_CHECK_LEAKS=1",
					"ASAN_OPTIONS=detect_leaks=0",
				},
			}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("backend=%s release=%v error:\n%v\nstderr:\n%s", backend, release, err, stderr.String())
			}
			if rc != 0 {
				t.Fatalf("backend=%s release=%v rc=%d\nstdout:\n%s\nstderr:\n%s", backend, release, rc, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != want {
				t.Fatalf("backend=%s release=%v mismatch:\ngot:\n%s\nwant:\n%s", backend, release, got, want)
			}
		}
	}
}

func TestVariantPayloadWrongTagTrap(t *testing.T) {
	src := `
define E enum [
	A: int,
	B: string,
]
bind main = fn(): {
	let v = E.A(12)
	let _ = v.payload(comp.variants_of[E]()[1]).len()
}
`
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.ll")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	root := projectRoot(t)
	for _, release := range []bool{false, true} {
		for _, backend := range []driver.BackendKind{driver.BackendC11, driver.BackendLLVM} {
			var stdout, stderr bytes.Buffer
			rc, err := driver.Run(driver.Options{
				EntryPath:   mainPath,
				ProjectRoot: root,
				Backend:     backend,
				Release:     release,
			}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("backend=%s release=%v build/run error: %v\nstderr:\n%s", backend, release, err, stderr.String())
			}
			if rc == 0 || !bytes.Contains(stderr.Bytes(), []byte("variant payload access failed")) {
				t.Fatalf("backend=%s release=%v expected trap with variant mismatch message, rc=%d stderr=%q",
					backend, release, rc, stderr.String())
			}
		}
	}
}

func TestComptimeVariantPrivacyMultiPkg(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "secret")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	modSrc := `
define VaultE enum [
	Pub: int,
	_Priv: int,
]
const make_priv = fn() VaultE: VaultE._Priv(99)
`
	if err := os.WriteFile(filepath.Join(sub, "secret.ll"), []byte(modSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		code string
	}{
		{
			name: "is_variant",
			code: "import \"secret\"\nbind main = fn(): { let v = secret.make_priv(); let _ = v.is_variant(comp.variants_of[secret.VaultE]()[1]) }\n",
		},
		{
			name: "payload",
			code: "import \"secret\"\nbind main = fn(): { let v = secret.make_priv(); let _ = v.payload(comp.variants_of[secret.VaultE]()[1]) }\n",
		},
		{
			name: "as_variant",
			code: "import \"secret\"\nbind main = fn(): { let v = secret.make_priv(); if(v.as_variant(comp.variants_of[secret.VaultE]()[1]) as p): { let _ = p } }\n",
		},
		{
			name: "make_variant",
			code: "import \"secret\"\nbind main = fn(): { let _ = make_variant[secret.VaultE](comp.variants_of[secret.VaultE]()[1], 5) }\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mainPath := filepath.Join(dir, tc.name+".ll")
			if err := os.WriteFile(mainPath, []byte(tc.code), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			_, err := driver.Run(driver.Options{
				EntryPath:   mainPath,
				ProjectRoot: projectRoot(t),
				Backend:     driver.BackendC11,
			}, nil, &stdout, &stderr)
			if err == nil {
				t.Fatalf("expected private variant error for %s", tc.name)
			}
			if !bytes.Contains([]byte(err.Error()+stderr.String()), []byte("cannot access unexported variant")) {
				t.Fatalf("unexpected error for %s:\nerr=%v\nstderr=%s", tc.name, err, stderr.String())
			}
		})
	}
}

