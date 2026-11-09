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

bind Pair[A, B].into_second = (own self): B -> {
    self.second
}

bind push_twice = (xs: &[int], v: int) -> {
    xs.push(v)
    xs.push(v + 1)
}

bind main = (): int -> {
    let list = [1, 2, 3]
    push_twice(&list, 4)

    let head?: ?Node = Node[val: 10, next?: none]
    let n2 = Node[val: 20, next?: own head?]
    if (n2.next? as child) {
        println("child = ${child.val}")
    }

    let p = Pair[first: 7, second: "seven".copy()]
    let s = p.into_second()
    println(s)

    let st = Status.Active
    let code = match (st)
        when Status.Pending: 1
        when Status.Active: 2
        else Status.Closed: 3

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
			src:  `bind main = () -> { let bad = parse_int("42") }`,
			want: "must end with `?`",
		},
		{
			name: "extra ? on non-optional variable",
			src:  `bind main = () -> { let bad?: int = 42 }`,
			want: "non-optional type",
		},
		{
			name: "deep immutability of bind map",
			src:  `bind main = () -> { bind m = #["a": 1]; m.insert("b", 2) }`,
			want: "deeply immutable `bind`",
		},
		{
			name: "missing & at mutable call site",
			src: `
bind bump = (x: &int) -> { x += 1 }
bind main = () -> { let a = 5; bump(a) }
`,
			want: "requires `&` at the call site",
		},
		{
			name: "missing own at consuming call site",
			src: `
bind sink = (s: own string) -> { println(s) }
bind main = () -> { let s = "hi"; sink(s) }
`,
			want: "requires `own s` at the call site",
		},
		{
			name: "use after move",
			src: `
bind sink = (s: own string) -> { println(s) }
bind main = () -> { let s = "hi"; sink(own s); println(s) }
`,
			want: "use of moved variable",
		},
		{
			name: "conditional move then read",
			src: `
bind sink = (s: own string) -> { println(s) }
bind main = () -> {
    let s = "hi"
    if (true) { sink(own s) }
    println(s)
}
`,
			want: "may have been moved",
		},
		{
			name: "borrow conflict &x and x in same call",
			src: `
bind bad = (a: &[int], b: [int]) -> { a.push(b.len()) }
bind main = () -> { let xs = [1, 2]; bad(&xs, xs) }
`,
			want: "mutably borrowed",
		},
		{
			name: "mutate vector while iterating",
			src: `
bind main = () -> {
    let xs = [1, 2, 3]
    for (x in xs) {
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
bind main = () -> {
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
bind leak = (): ref[BoxedInt] -> {
    let arena = Arena.new()
    arena.alloc(BoxedInt[v: 1])
}
bind main = () -> {}
`,
			want: "unless it takes an `&Arena` parameter",
		},
		{
			name: "unwind across lambda boundary",
			src: `
bind main = (): int -> {
    let f = (): int -> {
        if (true) {
            for <- 5
        }
        1
    }
    for (i in 0..3) {
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
bind main = (): int -> {
    let c = Color.Red
    match (c)
        when Color.Red: 1
        else Color.Green: 2
}
`,
			want: "missing variant(s): Color.Blue",
		},
		{
			name: "non-value block assigned to variable",
			src: `
bind main = () -> {
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
bind main = () -> {
    let p = null_ptr[u8]()
}
`,
			want: "unsafe",
		},
		{
			name: "duplicate string pattern in match",
			src: `
bind main = () -> {
    let s = "hello"
    match (s)
        when "hello": 1
        when "hello": 2
        else: 0
}
`,
			want: `duplicate string pattern "hello" in ` + "`match`",
		},
		{
			name: "duplicate int pattern in match",
			src: `
bind main = () -> {
    let x = 42
    match (x)
        when 42: 1
        when 42: 2
        else: 0
}
`,
			want: "duplicate integer pattern `42` in `match`",
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
