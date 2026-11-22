package sema_test

import (
	"strings"
	"testing"

	"lowlight/internal/diag"
	"lowlight/internal/sema"
)

func checkSrc(src string) *diag.Reporter {
	rep := diag.NewReporter()
	_ = sema.Check("test.ll", src, "", rep)
	return rep
}

func TestSemaValidFeatures(t *testing.T) {
	src := `
define Status enum [
    Pending,
    Active,
    Closed,
]

define Node struct [
    val: int,
    next?: ?Node,
]

define Pair[A, B] struct [
    first: A,
    second: B,
]

bind Pair[A, B].into_second = fn(own self) B: {
    self.second
}

bind push_twice = fn(xs: &[int], v: int): {
    xs.push(v)
    xs.push(v + 1)
}

bind main = fn() int: {
    let list = [1, 2, 3]
    push_twice(&list, 4)

    let head?: ?Node = Node[val: 10, next?: none]
    let n2 = Node[val: 20, next?: own head?]
    if (n2.next? as child): {
        println("child = ${child.val}")
    }

    let p = Pair[first: 7, second: "seven".copy()]
    let s = p.into_second()
    println(s)

    let st = Status.Active
    let code = match (st):
        when Status.Pending: 1
        when Status.Active: 2
        default Status.Closed: 3

    code
}
`
	rep := checkSrc(src)
	if rep.HasErrors() {
		t.Fatalf("expected valid program, got errors:\n%s", rep.Format())
	}
}

func TestSemaSafetyRulesRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "missing ? on optional variable",
			src:  `bind main = fn(): { let bad = parse_int("42") }`,
			want: "must end with `?`",
		},
		{
			name: "extra ? on non-optional variable",
			src:  `bind main = fn(): { let bad?: int = 42 }`,
			want: "non-optional type",
		},
		{
			name: "deep immutability of bind map",
			src:  `bind main = fn(): { bind m = #["a": 1]; m.insert("b", 2) }`,
			want: "deeply immutable `bind`",
		},
		{
			name: "missing & at mutable call site",
			src: `
bind bump = fn(x: &int): { x += 1 }
bind main = fn(): { let a = 5; bump(a) }
`,
			want: "requires `&` at the call site",
		},
		{
			name: "missing own at consuming call site",
			src: `
bind sink = fn(s: own string): { println(s) }
bind main = fn(): { let s = "hi"; sink(s) }
`,
			want: "requires `own s` at the call site",
		},
		{
			name: "use after move",
			src: `
bind sink = fn(s: own string): { println(s) }
bind main = fn(): { let s = "hi"; sink(own s); println(s) }
`,
			want: "use of moved variable",
		},
		{
			name: "conditional move then read",
			src: `
bind sink = fn(s: own string): { println(s) }
bind main = fn(): {
    let s = "hi"
    if (true): { sink(own s) }
    println(s)
}
`,
			want: "may have been moved",
		},
		{
			name: "borrow conflict &x and x in same call",
			src: `
bind bad = fn(a: &[int], b: [int]): { a.push(b.len()) }
bind main = fn(): { let xs = [1, 2]; bad(&xs, xs) }
`,
			want: "mutably borrowed",
		},
		{
			name: "mutate vector while iterating",
			src: `
bind main = fn(): {
    let xs = [1, 2, 3]
    for (x in xs): {
        xs.push(x)
    }
}
`,
			want: "while it is borrowed",
		},
		{
			name: "arena ref escaping outer scope",
			src: `
define BoxedInt struct [ v: int ]
bind main = fn(): {
    let escaped: [ref[BoxedInt]] = []
    {
        let arena = Arena.new()
        let r = arena.alloc(BoxedInt[v: 1])
        escaped.push(r)
    }
}
`,
			want: "cannot escape",
		},
		{
			name: "arena ref returned without &Arena param",
			src: `
define BoxedInt struct [ v: int ]
bind leak = fn() ref[BoxedInt]: {
    let arena = Arena.new()
    arena.alloc(BoxedInt[v: 1])
}
bind main = fn(): {}
`,
			want: "unless it takes an `&Arena` parameter",
		},
		{
			name: "unwind across lambda boundary",
			src: `
bind main = fn() int: {
    let f = cl() int: {
        if (true): {
            for <- 5
        }
        1
    }
    for (i in 0..3): {
        let v = f()
    }
    0
}
`,
			want: "lambda boundary",
		},
		{
			name: "non-exhaustive enum match",
			src: `
define Color enum [ Red, Green, Blue ]
bind main = fn() int: {
    let c = Color.Red
    match (c):
        when Color.Red: 1
        default Color.Green: 2
}
`,
			want: "missing variant(s): Color.Blue",
		},
		{
			name: "non-value block assigned to variable",
			src: `
bind main = fn(): {
    let x = {
        let a = 1
        let b = 2
    }
}
`,
			want: "does not evaluate to a value",
		},
		{
			name: "unsafe pointer op outside unsafe block",
			src: `
bind main = fn(): {
    let p = null_ptr[u8]()
}
`,
			want: "unsafe",
		},
		{
			name: "duplicate string pattern in match",
			src: `
bind main = fn(): {
    let s = "hello"
    match (s):
        when "hello": 1
        when "hello": 2
        default: 0
}
`,
			want: `duplicate string pattern "hello" in ` + "`match`",
		},
		{
			name: "duplicate int pattern in match",
			src: `
bind main = fn(): {
    let x = 42
    match (x):
        when 42: 1
        when 42: 2
        default: 0
}
`,
			want: "duplicate integer pattern `42` in `match`",
		},
		{
			name: "recursive function with omitted return type rejected",
			src: `
bind count_down = fn(n: int): {
    if (n <= 0): func <- 0
    count_down(n - 1)
}
bind main = fn(): {}
`,
			want: `cannot infer return type of recursive function "count_down"`,
		},
		{
			name: "void cannot be used as variable type",
			src: `
bind main = fn(): {
    let x: void = 1
}
`,
			want: "`void`",
		},
		{
			name: "if without else rejects non-optional eval type",
			src: `
bind main = fn(): {
    let x = if (true) int: 42
}
`,
			want: "must have an optional evaluation type",
		},
		{
			name: "while without else rejects non-optional eval type",
			src: `
bind main = fn(): {
    let i = 0
    let x = while (i < 3) int: { i += 1 }
}
`,
			want: "must have an optional evaluation type",
		},
		{
			name: "mixed bare func <- and value func <- rejected",
			src: `
bind f = fn(b: bool): {
    if (b): func <-
    42
}
bind main = fn(): {}
`,
			want: "mixes bare `func <-` (void) with non-void return value",
		},
		{
			name: "void function rejects func <- value",
			src: `
bind f = fn() void: {
    func <- 1
}
bind main = fn(): {}
`,
			want: "void function cannot unwind with a value",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := checkSrc(tc.src)
			if !rep.HasErrors() {
				t.Fatalf("expected compile error containing %q, but succeeded!", tc.want)
			}
			got := rep.Format()
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected diagnostic containing %q, got:\n%s", tc.want, got)
			}
		})
	}
}

func TestOption2InferenceAndUnifiedSyntaxValid(t *testing.T) {
	src := `
bind double = fn(x: int): x * 2
bind greet = fn(s: string): { println(s) }
bind with_void_semi = fn(x: int): { x + 1; }
bind early_ret = fn(b: bool): {
    if (b): func <- 10
    20
}
bind early_void = fn(b: bool): {
    if (b): func <-
    println("ok")
}
bind explicit_void = fn() void: {
    println("explicit")
}

define Box[T] struct [ val: T ]
bind make_list[T] = fn(item: T): {
    for (i in 0..2) [T]: item.copy()
}

bind main = fn() int: {
    let bad_if?: ?int = if (true) ?int: 42
    let no_else?: ?int = if (false): 7
    let else_if_val = if (false): 1 else if (true): 2 else: 3
    let m_val = match (else_if_val) int:
        when 2: 20
        default: 0
    let l1: [int] = make_list[int](double(3))
    let inc = cl(n: int): n + 1
    let fn_arg: cl(int) int = cl(k: int): k + 5
    let fn_void: cl(int) void = cl(k: int): { if (k > 0): println("ok") }
    fn_void(1)
    greet("hi")
    with_void_semi(5)
    early_void(true)
    explicit_void()
    early_ret(false) + inc(fn_arg(l1[0])) + (bad_if? ?? 0) + (no_else? ?? 0) + m_val
}
`
	rep := checkSrc(src)
	if rep.HasErrors() {
		t.Fatalf("expected valid program, got errors:\n%s", rep.Format())
	}
}

func TestCoalesceRightAssociativeTypeInferenceAndMoves(t *testing.T) {
	src := `
bind pick = fn(a?: ?int, b?: ?int) int: {
    let s = "alive".copy()
    let x = a? ?? none ?? 0
    let y = a? ?? none ?? func <- {
        let moved = s
        println(moved)
        -1
    }
    let z = a? ?? b? ?? func <- -2
    println(s)
    x + y + z
}

bind main = fn(): {
    println("${pick(1, 2)}")
}
`
	rep := checkSrc(src)
	if rep.HasErrors() {
		t.Fatalf("expected valid program, got errors:\n%s", rep.Format())
	}
}

func TestPrivateGenericTypeArgsBeforeDot(t *testing.T) {
	src := `
define _Box[T] struct [
    value: T,
]

bind _Box[T].wrap = fn(v: own T) _Box[T]: {
    _Box[T][value: own v]
}

define _Pair[A, B] struct [
    first: A,
    second: B,
]

bind _Pair[A, B].make = fn(a: own A, b: own B) _Pair[A, B]: {
    _Pair[A, B][first: own a, second: own b]
}

define _Maybe[T] enum [
    _Some: T,
    _None,
]

bind main = fn(): {
    let b = _Box[int].wrap(42)
    let p = _Pair[int, string].make(b.value, "ok".copy())
    let m = _Maybe[int]._Some(p.first)
    let n = _Maybe[int]._None
    let v = match (m):
        when _Maybe[int]._Some(x): x
        default _Maybe[int]._None: 0
    let _items = ["hello", "world"]
    let idx = 0
    let l = _items[idx].len()
    let z = match (n):
        when _Maybe[int]._Some(x): x
        default _Maybe[int]._None: 0
    println("${v + l + z}")
}
`
	rep := checkSrc(src)
	if rep.HasErrors() {
		t.Fatalf("expected valid program, got errors:\n%s", rep.Format())
	}
}
