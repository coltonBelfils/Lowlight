package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func TestPhase6RegressionsDualBackend(t *testing.T) {
	t.Parallel()
	src := `
import "std/core"
import "std/os"
import "std/fs"
import "std/net"

// D1: Optional Comparable & Hashable (?T in derived structs/Set/Map, < / > ordering)
@derive(Equatable, Comparable, Hashable, Copyable)
define OptKey struct [
	val?: ?int,
]

const check_optional_cmp_hash = fn() string: {
	let a?: ?int = none
	let b?: ?int = 5
	let c?: ?int = 9
	assert(a? < b? and b? < c? and c? > a?)
	let k0 = OptKey[val?: none]
	let k1 = OptKey[val?: 5]
	let k2 = OptKey[val?: 9]
	assert(k0 < k1 and k1 < k2)
	let s: #[OptKey] = #[OptKey[val?: none], OptKey[val?: 5], OptKey[val?: 9]]
	assert(s.contains(k0) and s.contains(k1) and s.len() == 3)
	let m: #[OptKey: int] = #[OptKey[val?: none]: 1, OptKey[val?: 5]: 2]
	assert((m[k0] ?? 0) == 1 and (m[k1] ?? 0) == 2)
	"${s.len()}:${m[k0] ?? 0}"
}

// D2: Distinct reflection (comp.underlying_of[D](), .implements, .underlying) + @derive
@derive(Equatable, Comparable, Hashable, Copyable, Printable)
define Meter int

@derive(Equatable, Printable)
define Label string

const CT_DISTINCT = comptime {
	let info = comp.underlying_of[Meter]()
	let m = Meter(7)
	assert(comp.type_name[Meter]() == "Meter")
	assert(info.type_name == "int")
	assert(info.implements(Equatable))
	assert(comp.implements[Meter, Comparable]())
	let raw = m.underlying()
	raw + 3
}

// C5 & C6: Negative enum discriminants + Drop tag guards
@derive(Equatable)
define NegCode enum i16 [
	Ok = 1,
	Eof = -1,
	Fail = -128,
]

define GuardedRes enum [
	Empty,
	Held: [u8],
]

// H1-H5: StackArray type checking, NaN ordering, UTF-8 RuneStart slicing, non-fixed collecting comptime for, f32 fmt
const _CT_FOR_ITEMS = [10, 20, 30]

const take_arr3 = fn(a: [3: int]) int: a[0] + a[1] + a[2]

const CT_UTF8_AND_FOR = comptime {
	let s = "aé🚀z"
	let sub = s[1..7]
	let dyn_col = comptime for(x in _CT_FOR_ITEMS) [int]: {
		if(x == 20): next <- x * 2
		if(x == 30): for <- [1, 2, 3, 4]
		x
	} else: [0]
	"$sub|${dyn_col.len()}|${dyn_col[3]}"
}

const CT_NAN_OK = comptime {
	let n = 0.0 / 0.0
	not (n == n) and (n != n) and not (n < 1.0) and not (n > 1.0)
}

comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
	extern ll_rt_fcntl as test_getfd_fcntl: fn(fd: i32, cmd: i32, arg: i32) i32
}

bind main = fn(): {
	// D1
	assert(check_optional_cmp_hash() == "3:1")

	// D2 runtime + comptime
	assert(CT_DISTINCT == 10)
	assert(Meter(3) == Meter(3) and Meter(2) < Meter(5))
	assert("${Label("hi")}" == "Label(hi)")
	let dmap: #[Meter: string] = #[Meter(4): "four".copy()]
	assert((dmap[Meter(4)] ?? "") == "four")

	// C5 & C6
	let nc = NegCode.Fail
	assert(int(nc) == -128 and nc != NegCode.Ok and nc == NegCode.Fail)
	let g = GuardedRes.Empty
	match(g):
		when GuardedRes.Empty: {}
		default: assert(false)

	// StackArray param/type identity + NaN + runtime collecting comptime for + f32
	let arr3: [3: int] = [1, 2, 3]
	assert(take_arr3(arr3) == 6)
	let rt_col = comptime for(x in _CT_FOR_ITEMS) [int]: {
		if(x == 20): next <- x * 2
		if(x == 30): for <- [1, 2, 3, 4]
		x
	} else: [0]
	assert(rt_col.len() == 4 and rt_col[3] == 4)
	let nan = 0.0 / 0.0
	assert(CT_NAN_OK, "comptime NaN ordering/eq")
	assert(not (nan < 1.0) and not (nan > 1.0) and not (nan == nan) and nan != nan, "runtime NaN ordering/eq")
	let f32s = "${f32(1.5)}"
	assert(f32s == "1.5", "f32 interpolation")
	assert(CT_UTF8_AND_FOR == "é🚀|4|4", "UTF-8 slice + collecting comptime for")

	// D5: List.pop & List.truncate on static zero-cap slices without heap churn
	let v = comptime { [10, 20, 30, 40] }
	assert(v.cap() == 0 and (v.pop() ?? 0) == 40 and v.len() == 3 and v.cap() == 0)
	v.truncate(1)
	assert(v.len() == 1 and v[0] == 10 and v.cap() == 0)
	let vs = comptime { ["alpha", "beta", "gamma"] }
	assert(vs.cap() == 0)
	vs.truncate(1)
	assert(vs.len() == 1 and vs[0] == "alpha" and vs.cap() == 0)

	// D6: os_name, arch, error_from_errno (strerror_r), deep remove_all
	assert(os.os_name() != "unknown" and os.arch() != "unknown")
	let e = os.error_from_errno(2, "missing")
	assert(e.message().contains("missing"))
	let root_dir = "/tmp/ll_phase6_remove_all_test"
	os.remove_all(root_dir): default panic
	os.mkdir_p("$root_dir/sub1/sub2"): default panic
	fs.write_file("$root_dir/sub1/sub2/leaf.txt", "ok"): default panic
	os.remove_all(root_dir): default panic
	assert(not os.file_exists(root_dir))

	// H12: CLOEXEC on sockets & open files via ll_rt_fcntl(F_GETFD=1)
	comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
		let pair = net.socketpair().unwrap()
		let fl0 = unsafe test_getfd_fcntl(i32(pair.first.raw_fd()), i32(1), i32(0))
		assert((int(fl0) & 1) == 1, "socketpair must set FD_CLOEXEC")
	}

	println("PHASE6_E2E_OK")
}
`
	for _, release := range []bool{false, true} {
		got := runInlineStrict(t, "phase6_e2e", src, release)
		if got != "PHASE6_E2E_OK\n" {
			t.Fatalf("release=%v unexpected stdout: %q", release, got)
		}
	}
}

func TestPhase6UnsupportedLinuxArchCompileError(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	entry := filepath.Join(t.TempDir(), "unsupported_arch.ll")
	src := "import \"std/time\"\nbind main = fn(): { let _ = time.monotonic_nanos() }\n"
	var stdout, stderr bytes.Buffer
	_, err := driver.Run(driver.Options{
		EntryPath:    entry,
		SrcOverride:  src,
		ProjectRoot:  root,
		TargetTriple: "riscv64-unknown-linux-gnu",
	}, nil, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected compile error on riscv64-unknown-linux-gnu for std/time")
	}
	msg := err.Error() + stderr.String()
	if !strings.Contains(msg, "unsupported Linux architecture") {
		t.Fatalf("expected explicit unsupported Linux architecture error, got:\n%s", msg)
	}
}
