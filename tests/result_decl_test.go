package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

const resultDeclPrelude = `
define Lookup result [
	value: string,
	Missing: string,
	Forbidden,
	Expired: int,
]

define Res result [
	value: int,
	Err: string,
]

bind find = (k: string) Lookup:
	if(k == "a"): Lookup.value("found-a")
	else if(k == "b"): Lookup.Forbidden
	else if(k == "c"): Lookup.Expired(7)
	else: Lookup.Missing(k.copy())
`

// runResultProgram builds and runs `src` on both backends with leak checking and returns
// (exit code, stdout, stderr) per backend after asserting the build succeeded.
func runResultProgram(t *testing.T, name, src string, backend driver.BackendKind) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rc, err := driver.Run(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), name+".ll"),
		SrcOverride: src,
		ProjectRoot: projectRoot(t),
		Backend:     backend,
		ExtraCFlags: strictCFlags,
		Env:         []string{"LOWLIGHT_CHECK_LEAKS=1", "ASAN_OPTIONS=detect_leaks=0"},
	}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("[%s %s] build failed: %v\nstderr:\n%s", name, backend, err, stderr.String())
	}
	return rc, stdout.String(), stderr.String()
}

// Result chains, `??`, `?? <unwind>`, `if (r! as v)`, and bare member constructors (`value(x)`,
// `Err(e)`, `Forbidden`) on user-declared results, on both backends without leaks
// (SPECIFICATION.md §3.8.3, §7.6.5).
func TestResultDeclarationsAndChains(t *testing.T) {
	t.Parallel()
	src := resultDeclPrelude + `
bind describe = (k: string) string: {
	let s = find(k):
		when Missing(why): "missing:" + why
		when Forbidden: func <- "denied"
		default Expired(n): "expired:$n"
	"got " + s
}

bind first_found = (ks: [string]) string: {
	for(k in ks): {
		let v = find(k) ?? next <-
		func <- v
	}
	"none"
}

bind parse = (s: string) Res:
	if(s == "1"): value(1)
	else: Err("bad: " + s)

bind double = (s: string) Res: {
	let n = parse(s):
		default Err(e): func <- Err("while doubling: " + e)
	value(n * 2)
}

bind classify = (n: int) Lookup:
	if(n == 0): Forbidden
	else if(n < 0): Missing("negative")
	else: value("n=$n")

bind label = (r!: own Lookup) string: r!:
	when Missing(m): "missing:" + m
	when Forbidden: "forbidden"
	default Expired(_): "expired"

bind load = (ok: bool) Result[string]: {
	let s = find(if(ok): "a" else: "q"):
		default: func <- err(Error.new("lookup failed"))
	value(s + "!")
}

bind main = (): {
	println(describe("a"))
	println(describe("b"))
	println(describe("c"))
	println(describe("zz"))
	println(first_found(["q", "b", "a"]))
	println(find("zz") ?? "fallback")
	let r! = find("a")
	if(r! as v): println("as $v") else: println("no value")
	if(find("b") as v): println("as $v") else: println("no value")
	let m! = find("a")
	if(&m! as v): v += "!"
	println(m! ?? "x")
	let rest = find("c"):
		when Missing(_): "m"
		default: "other"
	println(rest)
	println(double("1") ?? -1)
	let d! = double("x")
	let dv = d!:
		default Err(e): {
			println(e)
			0
		}
	println(dv)
	let ok = find("a"): default panic
	println(ok)
	println(classify(3) ?? "x")
	println(label(classify(0)))
	println(label(classify(-1)))
	let annotated!: Lookup = Expired(1)
	println(label(own annotated!))
	println(label(Forbidden))
	println(load(true) ?? "f")
	println(load(false).unwrap_err().message())
}
`
	want := strings.Join([]string{
		"got found-a", "denied", "got expired:7", "got missing:zz", "found-a", "fallback",
		"as found-a", "no value", "found-a!", "other", "2", "while doubling: bad: x", "0", "found-a",
		"n=3", "forbidden", "missing:negative", "expired", "forbidden", "found-a!", "lookup failed",
	}, "\n")
	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		rc, stdout, stderr := runResultProgram(t, "result_chains", src, backend)
		if rc != 0 {
			t.Fatalf("[%s] exit code %d\nstdout:\n%s\nstderr:\n%s", backend, rc, stdout, stderr)
		}
		if got := strings.TrimSpace(stdout); got != want {
			t.Fatalf("[%s] unexpected output:\n%s\nwant:\n%s", backend, got, want)
		}
	}
}

// `default panic` panics on any failure the chain does not handle, naming the variant and the
// `default panic` line.
func TestResultChainDefaultPanic(t *testing.T) {
	t.Parallel()
	src := resultDeclPrelude + `
bind main = (): {
	println("before")
	let v = find("c"):
		when Missing(_): "m"
		default panic
	println(v)
}
`
	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		rc, stdout, stderr := runResultProgram(t, "result_panic", src, backend)
		if rc != 1 || stdout != "before\n" {
			t.Fatalf("[%s] rc=%d stdout=%q, want rc=1 stdout=%q\nstderr:\n%s", backend, rc, stdout, "before\n", stderr)
		}
		if !strings.Contains(stderr, "lowlight panic at result_panic.ll:24:3: unhandled failure Lookup.Expired") {
			t.Fatalf("[%s] unexpected panic message:\n%s", backend, stderr)
		}
	}
}

// Static rules for result declarations, the `!` naming invariant, and result chains.
func TestResultDeclarationDiagnostics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no_value", "define Bad result [Oops: string]\nbind main = (): {}", "result Bad has no `value` member"},
		{"no_failures", "define Bad result [value: int]\nbind main = (): {}", "result Bad has no failure variants"},
		{"untyped_value", "define Bad result [value, Oops]\nbind main = (): {}", "the `value` member of result Bad must declare its type"},
		{"missing_bang", "bind main = (): {\n\tlet r = find(\"a\")\n}", "has result type Lookup and must end with `!`"},
		{"stray_bang", "bind main = (): {\n\tlet n! = 3\n}", "ends with `!` but has non-result type int"},
		{"bang_param", "bind show = (r: Lookup) int: 0\nbind main = (): {}", "has result type Lookup and must end with `!`"},
		{"needs_default", "bind main = (): {\n\tlet v = find(\"a\"):\n\t\twhen Forbidden: \"f\"\n\tprintln(v)\n}", "result chain must terminate with a `default` arm"},
		{"non_exhaustive", "bind main = (): {\n\tlet v = find(\"a\"):\n\t\twhen Forbidden: \"f\"\n\t\tdefault Missing(_): \"m\"\n\tprintln(v)\n}", "non-exhaustive"},
		{"matches_value", "bind main = (): {\n\tlet v = find(\"a\"):\n\t\twhen value(x): x\n\t\tdefault: \"f\"\n\tprintln(v)\n}", "a result chain cannot match `value`"},
		{"unknown_failure", "bind main = (): {\n\tlet v = find(\"a\"):\n\t\twhen Gone: \"g\"\n\t\tdefault: \"f\"\n\tprintln(v)\n}", "result Lookup has no failure variant \"Gone\""},
		{"unreachable_panic", "bind main = (): {\n\tlet v = find(\"a\"):\n\t\twhen Missing(_), Forbidden, Expired(_): \"x\"\n\t\tdefault panic\n\tprintln(v)\n}", "`default panic` is unreachable"},
		{"non_result_subject", "bind main = (): {\n\tlet x = 3\n\tlet v = x: default panic\n}", "result chain subject must be a result type, got int"},
		{"bare_unit_called", "bind f = () Lookup: Forbidden()\nbind main = (): {}", "member `Forbidden` of Lookup has no payload"},
		{"bare_ambiguous", "bind Missing = (s: string) Lookup: Lookup.Forbidden\nbind f = () Lookup: Missing(\"x\")\nbind main = (): {}", "`Missing(...)` is ambiguous"},
		{"no_implicit_wrap", "bind f = () Res: {\n\tfunc <- \"oops\"\n}\nbind main = (): {}", "type mismatch"},
		{"bare_needs_result_context", "bind main = (): {\n\tlet x = Forbidden\n}", "undeclared"},
		{"payload_needs_binding", "bind main = (): {\n\tlet v = find(\"a\"):\n\t\twhen Missing: \"m\"\n\t\tdefault: \"f\"\n\tprintln(v)\n}", "failure `Missing` carries a string payload"},
	}
	for _, tc := range cases {
		src := resultDeclPrelude + tc.body + "\n"
		_, _, err := driver.Check(driver.Options{
			EntryPath:   filepath.Join(t.TempDir(), tc.name+".ll"),
			SrcOverride: src,
			ProjectRoot: projectRoot(t),
		})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: expected error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}
