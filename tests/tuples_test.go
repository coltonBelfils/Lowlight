package tests

import (
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func TestTuplesEndToEnd(t *testing.T) {
	t.Parallel()
	src := `
bind make_pair = fn(x: int, y: string) +[int, string]: {
    +[x, y.copy()]
}

bind sum_pair = fn(p: +[int, int]) int: {
    p.0 + p.1
}

bind main = fn() int: {
    // 1. Creation and type annotation
    let t: +[int, string] = +[5, "abc"]
    println("${t}")
    println("${t.0}")
    println(t.1)

    // 2. Calling function in tuple
    let fn_tup = +[cl(x: int) int: x * 2, cl(msg: string) string: msg.copy()]
    let res = fn_tup.0(21)
    println("fn_call=${res}")
    println(fn_tup.1("world"))

    // 3. Passing to and returning from function
    let returned = make_pair(42, "hello")
    println("${returned.0}: ${returned.1}")

    let sum = sum_pair(+[10, 20])
    println("sum=${sum}")

    // 4. Equality, inequality, and ordering
    let t1 = +[1, "x"]
    let t2 = +[1, "x"]
    let t3 = +[2, "x"]
    if (t1 == t2): {
        println("t1 == t2")
    }
    if (t1 != t3): {
        println("t1 != t3")
    }
    if (t1 < t3): {
        println("t1 < t3")
    }

    // 5. Mutation
    let mut_t = +[100, 200]
    mut_t.0 = 500
    mut_t.1 = 600
    println("mutated: ${mut_t.0}, ${mut_t.1}")

    // 6. Nested tuples
    let nested: +[int, +[string, bool]] = +[1, +["inner", true]]
    println("nested: ${nested.0}, ${nested.1.0}, ${nested.1.1}")
    println("nested to_string: ${nested}")

    // 7. Empty tuple
    let empty: +[] = +[]
    println("empty: ${empty}")
    if (empty == +[]): {
        println("empty == +[]")
    }

    0
}
`
	out := runInlineStrict(t, "tuples_e2e", src, false)
	expected := `+[5, abc]
5
abc
fn_call=42
world
42: hello
sum=30
t1 == t2
t1 != t3
t1 < t3
mutated: 500, 600
nested: 1, inner, true
nested to_string: +[1, +[inner, true]]
empty: +[]
empty == +[]
`
	if strings.TrimSpace(out) != strings.TrimSpace(expected) {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, out)
	}
}

func TestTuplesRelease(t *testing.T) {
	t.Parallel()
	src := `
bind main = fn() int: {
    let t = +[123, "release_test", true]
    println("${t}")
    0
}
`
	out := runInlineStrict(t, "tuples_release", src, true)
	expected := "+[123, release_test, true]\n"
	if out != expected {
		t.Fatalf("expected %q, got %q", expected, out)
	}
}

func TestTuplesInCollections(t *testing.T) {
	t.Parallel()
	src := `
bind main = fn() int: {
    // 1. Vector of tuples
    let pairs = [+[1, "one"], +[2, "two"]]
    println("${pairs.len()}: ${pairs[0]}")

    // 2. Map with tuple keys
    let mut_map = #[
        +[1, 2]: "origin",
        +[3, 4]: "target",
    ]
    let has = mut_map.has(+[1, 2])
    println("has: ${has}")
    let val? = mut_map[+[1, 2]]
    let fallback = "none"
    println("map get: ${val? ?? fallback}")

    0
}
`
	out := runInlineStrict(t, "tuples_collections", src, false)
	expected := `2: +[1, one]
has: true
map get: origin
`
	if strings.TrimSpace(out) != strings.TrimSpace(expected) {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, out)
	}
}

func TestTuplesSemanticsErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		err  string
	}{
		{
			name: "index_out_of_bounds",
			src: `
bind main = fn() int: {
    let t = +[1, 2]
    let x = t.2
    0
}
`,
			err: "has no element 2",
		},
		{
			name: "bracket_index_disallowed",
			src: `
bind main = fn() int: {
    let t = +[1, 2]
    let x = t[0]
    0
}
`,
			err: "tuple indexing with brackets is not supported",
		},
		{
			name: "bracket_negative_index_disallowed",
			src: `
bind main = fn() int: {
    let t = +[1, 2]
    let x = t[-1]
    0
}
`,
			err: "tuple indexing with brackets is not supported",
		},
		{
			name: "dot_negative_index_disallowed",
			src: `
bind main = fn() int: {
    let t = +[1, 2]
    let x = t.-1
    0
}
`,
			err: "negative index is not allowed in dot access",
		},
		{
			name: "dot_leading_zero_disallowed",
			src: `
bind main = fn() int: {
    let t = +[1, 2]
    let x = t.00
    0
}
`,
			err: "invalid tuple element index \"00\": must be a non-negative canonical integer",
		},
		{
			name: "dot_underscore_disallowed",
			src: `
bind main = fn() int: {
    let t = +[1, 2]
    let x = t.0_1
    0
}
`,
			err: "invalid tuple element index \"0_1\": must be a non-negative canonical integer",
		},
		{
			name: "dot_hex_disallowed",
			src: `
bind main = fn() int: {
    let t = +[1, 2]
    let x = t.0x1
    0
}
`,
			err: "invalid tuple element index \"0x1\": must be a non-negative canonical integer",
		},
		{
			name: "type_mismatch",
			src: `
bind main = fn() int: {
    let t: +[int, string] = +[1, 2]
    0
}
`,
			err: "type mismatch",
		},
		{
			name: "recursive_struct_in_tuple",
			src: `
define Bad struct [
    x: +[int, Bad],
]
bind main = fn() int: 0
`,
			err: "infinite size",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := driver.Check(driver.Options{
				EntryPath:   "test.ll",
				SrcOverride: tc.src,
				ProjectRoot: projectRoot(t),
			})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.err)
			}
			if !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("expected error containing %q, got:\n%v", tc.err, err)
			}
		})
	}
}

func TestTuplesBugsFixes(t *testing.T) {
	t.Parallel()
	src := `
bind main = fn() int: {
    // 1. Function in tuple call
    let fns = +[cl(x: int) int: x + 10, cl(y: int) int: y * 3]
    println("f0: ${fns.0(5)}")
    println("f1: ${fns.1(5)}")

    // 2. Optional chaining on tuple
    let opt_tup?: ?+[int, string] = +[42, "hello"]
    println("opt_chain: ${opt_tup?.0}")
    println("opt_chain_str: ${opt_tup?.1}")
    let none_tup?: ?+[int, string] = none
    println("opt_none: ${none_tup?.0 ?? -1}")

    // 3. Coercion in tuple literal: T -> ?T and Concrete -> Interface
    let coerced: +[?string, int] = +["coerced_str", 99]
    let fb = "none"
    println("coerced: ${coerced.0?.copy() ?? fb}, ${coerced.1}")

    let boxed_ifc: +[Printable, int] = +["ifc_str", 100]
    println("boxed_ifc: ${boxed_ifc.0}, ${boxed_ifc.1}")

    // 4. ?Tuple expected context
    let opt_lit?: ?+[int, ?string] = +[123, none]
    println("opt_lit: ${opt_lit?.0}")

    // 5. Arena ref to tuple and mutation
    let a = Arena.new()
    let r = a.alloc(+[10, 20])
    r.0 = 55
    println("arena_tuple: ${r.0}, ${r.1}")

    // 6. Relational ordering comparison
    let a_tup = +[1, 10]
    let b_tup = +[1, 20]
    let c_tup = +[2, 5]
    if (a_tup < b_tup): {
        println("a < b")
    }
    if (b_tup < c_tup): {
        println("b < c")
    }
    if (a_tup <= a_tup): {
        println("a <= a")
    }
    if (c_tup > a_tup): {
        println("c > a")
    }

    // 7. Boxing plain tuple into Printable interface
    let p: Printable = +[7, 8]
    println("boxed_plain: ${p}")

    0
}
`
	out := runInlineStrict(t, "tuples_bug_fixes", src, false)
	expected := `f0: 15
f1: 15
opt_chain: 42
opt_chain_str: hello
opt_none: -1
coerced: coerced_str, 99
boxed_ifc: ifc_str, 100
opt_lit: 123
arena_tuple: 55, 20
a < b
b < c
a <= a
c > a
boxed_plain: +[7, 8]
`
	if strings.TrimSpace(out) != strings.TrimSpace(expected) {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, out)
	}
}

func TestTuplesPlainBoxRelease(t *testing.T) {
	t.Parallel()
	src := `
bind main = fn() int: {
    let p: Printable = +[10, 20]
    println("${p}")
    0
}
`
	out := runInlineStrict(t, "tuples_plain_box_release", src, true)
	expected := "+[10, 20]\n"
	if out != expected {
		t.Fatalf("expected %q, got %q", expected, out)
	}
}

func TestDistinctTuples(t *testing.T) {
	t.Parallel()
	src := `
define Pair +[int, string]
define FnPair +[cl(int) int, string]

bind Pair.first = fn(self) int: self.0
bind Pair.second = fn(self) string: self.1.copy()
bind Pair.summary = fn(self) string: "${self.0}: ${self.1}"

bind mutate_pair = fn(p: &Pair): {
    p.0 = p.0 + 100
}

bind main = fn() int: {
    // 1. Construction via Constructor Call: let f = foo(+[10, "abc"])
    let p1 = Pair(+[10, "alpha"])
    println("p1.0=${p1.0}")
    println("p1.1=${p1.1}")

    // 2. Construction via Type Annotation: let f: foo = +[10, "abc"]
    let p2: Pair = +[20, "beta"]
    println("p2.0=${p2.0}")
    println("p2.1=${p2.1}")

    // 3. Inherent custom methods on distinct tuple
    println("first=${p1.first()}")
    println("second=${p1.second()}")
    println("summary=${p2.summary()}")

    // 4. Calling closures in distinct tuple
    let fp: FnPair = +[cl(n: int) int: n * 3, "tripler"]
    println("fn_call=${fp.0(14)}")
    println("fn_name=${fp.1}")

    // 5. Mutating distinct tuple fields through mutable borrow
    let mut_p = Pair(+[5, "mutable"])
    mutate_pair(&mut_p)
    println("mutated=${mut_p.0}")

    // 6. Equality and comparison
    let a: Pair = +[1, "x"]
    let b: Pair = +[1, "x"]
    let c: Pair = +[2, "x"]
    if (a == b): { println("a == b") }
    if (a != c): { println("a != c") }
    if (a < c): { println("a < c") }

    0
}
`
	out := runInlineStrict(t, "distinct_tuples", src, false)
	expected := `p1.0=10
p1.1=alpha
p2.0=20
p2.1=beta
first=10
second=alpha
summary=20: beta
fn_call=42
fn_name=tripler
mutated=105
a == b
a != c
a < c
`
	if strings.TrimSpace(out) != strings.TrimSpace(expected) {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, out)
	}
}

func TestDistinctTuplesSemanticsErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "bracket_index_disallowed_on_distinct_tuple",
			src: `
define Pair +[int, string]
bind main = fn() int: {
    let p: Pair = +[10, "abc"]
    p[0]
}
`,
			wantErr: "tuple indexing with brackets is not supported; use dot access (e.g. '.0') instead",
		},
		{
			name: "assign_bare_tuple_without_annotation_rejected",
			src: `
define Pair +[int, string]
bind takes_pair = fn(p: Pair) int: p.0
bind main = fn() int: {
    let bare = +[10, "abc"]
    takes_pair(bare)
}
`,
			wantErr: "type mismatch: argument 1 expects `Pair`, but this is `+[int, string]`",
		},
		{
			name: "distinct_tuple_dot_out_of_bounds",
			src: `
define Pair +[int, string]
bind main = fn() int: {
    let p: Pair = +[10, "abc"]
    p.2
}
`,
			wantErr: "tuple Pair has no element 2 (index out of bounds)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := driver.Check(driver.Options{
				EntryPath:   "test.ll",
				SrcOverride: tc.src,
				ProjectRoot: projectRoot(t),
			})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got:\n%v", tc.wantErr, err)
			}
		})
	}
}

func TestTupleUnpack(t *testing.T) {
	t.Parallel()
	src := `
define Pair +[int, string]

bind make_triple = fn() +[int, string, bool]: +[42, "owned_str", true]

bind maybe_pair = fn(ok: bool) ?+[int, string]: {
    if (ok): +[99, "from_opt"] else: none
}

bind unwrap_or_early = fn(ok: bool) string: {
    let n, s = unpack maybe_pair(ok) ?? func <- "early_none"
    "${n}:${s}"
}

bind main = fn() int: {
    // 1. Basic let x, y = unpack +[5, 7]
    let x, y = unpack +[5, 7]
    println("xy=${x},${y}")

    // 2. bind unpacking with resource strings and wildcard _
    bind a, b, _ = unpack make_triple()
    println("ab=${a},${b}")

    // 3. Unpacking with per-variable type annotations & optional wrapping
    let u: i64, opt_s?: ?string = unpack +[100, "wrapped"]
    println("typed=${u},${opt_s? ?? "none"}")

    // 4. Unpacking a distinct tuple type
    let dp: Pair = +[77, "distinct_val"]
    let dx, ds = unpack dp
    println("distinct=${dx},${ds}")

    // 5. Reassignment unpacking & swap (including Resource strings!)
    let s1 = "first"
    let s2 = "second"
    s1, s2 = unpack +[s2, s1]
    println("swapped=${s1},${s2}")

    // 6. Reassignment with wildcard discard of Resource
    let k = 0
    k, _ = unpack +[321, "discarded_resource"]
    println("k=${k}")

    // 7. Unpacking with ?? fallback unwind
    println("opt_ok=${unwrap_or_early(true)}")
    println("opt_no=${unwrap_or_early(false)}")

    0
}
`
	out := runInlineStrict(t, "tuple_unpack", src, false)
	expected := `xy=5,7
ab=42,owned_str
typed=100,wrapped
distinct=77,distinct_val
swapped=second,first
k=321
opt_ok=99:from_opt
opt_no=early_none
`
	if strings.TrimSpace(out) != strings.TrimSpace(expected) {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, out)
	}
}

func TestTupleUnpackErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "missing_unpack_keyword_in_let",
			src: `
bind main = fn() int: {
    let x, y = +[5, 7]
    x + y
}
`,
			wantErr: "unpacking into multiple variables requires the 'unpack' keyword after '='",
		},
		{
			name: "missing_unpack_keyword_in_assign",
			src: `
bind main = fn() int: {
    let x = 1
    let y = 2
    x, y = +[5, 7]
    x + y
}
`,
			wantErr: "unpacking into multiple targets requires the 'unpack' keyword after '='",
		},
		{
			name: "arity_mismatch_in_let_unpack",
			src: `
bind main = fn() int: {
    let x, y, z = unpack +[5, 7]
    0
}
`,
			wantErr: "cannot unpack 2-element tuple +[int, int] into 3 variables",
		},
		{
			name: "arity_mismatch_in_assign_unpack",
			src: `
bind main = fn() int: {
    let x = 1
    let y = 2
    x, y = unpack +[5, 7, 9]
    0
}
`,
			wantErr: "cannot unpack 3-element tuple +[int, int, int] into 2 targets",
		},
		{
			name: "unpack_non_tuple",
			src: `
bind main = fn() int: {
    let x, y = unpack 42
    0
}
`,
			wantErr: "cannot unpack non-tuple type int",
		},
		{
			name: "reassign_bind_in_unpack",
			src: `
bind main = fn() int: {
    bind x = 1
    let y = 2
    x, y = unpack +[5, 7]
    0
}
`,
			wantErr: "cannot reassign deeply immutable `bind` variable \"x\"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := driver.Check(driver.Options{
				EntryPath:   "test.ll",
				SrcOverride: tc.src,
				ProjectRoot: projectRoot(t),
			})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got:\n%v", tc.wantErr, err)
			}
		})
	}
}

