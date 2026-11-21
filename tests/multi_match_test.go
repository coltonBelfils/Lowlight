package tests

import (
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func TestZeroArgMatch(t *testing.T) {
	t.Parallel()
	src := `
bind test_cond = (x: int, y: int) string: {
    match ():
        when x > 10 and y > 10: "both big"
        when x == 1, y == 2: "or branch"
        when x < 0: "x negative"
        default: "fallback"
}

bind test_bare_match = (val: int) int: {
    match:
        when val == 42: 100
        when val > 100, val < 0: 200
        default: 300
}

bind main = () int: {
    println(test_cond(15, 20))
    println(test_cond(1, 99))
    println(test_cond(99, 2))
    println(test_cond(-5, 0))
    println(test_cond(5, 5))

    println("${test_bare_match(42)}")
    println("${test_bare_match(150)}")
    println("${test_bare_match(-10)}")
    println("${test_bare_match(50)}")
    0
}
`
	out := runInlineStrict(t, "zero_arg_match", src, false)
	expected := `both big
or branch
or branch
x negative
fallback
100
200
200
300
`
	if strings.TrimSpace(out) != strings.TrimSpace(expected) {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, out)
	}
}

func TestMultiArgMatchEndToEnd(t *testing.T) {
	t.Parallel()
	src := `
define Color enum [ Red, Green, Blue ]
define Status enum [ Pending, Active, Done ]

define ExprPair struct [ a: int, b: int ]

define Expr enum [
    Num: int,
    Pair: ExprPair,
]

bind test_pairs = (x: int, y: int) string: {
    match (x, y):
        when 0, 0: "origin"
        when (0, 1), (1, 0): "unit step"
        when 0, _: "on y-axis"
        when _, 0: "on x-axis"
        when in 1..=5, in 10..=20: "in ranges"
        default: "other"
}

bind test_enums = (c: Color, s: Status) string: {
    match (c, s):
        when Color.Red, Status.Pending: "red pending"
        when (Color.Green, Status.Active), (Color.Blue, Status.Done): "green active or blue done"
        when Color.Red, _: "red other"
        default: "fallback"
}

bind test_destructure = (e1: Expr, e2: Expr) int: {
    match (e1, e2):
        when Expr.Num(a), Expr.Num(b): a + b
        when Expr.Pair(p), Expr.Num(n): p.a + p.b + n
        when Expr.Num(n), Expr.Pair(p): n * (p.a + p.b)
        default: -1
}

bind test_three_args = (a: int, b: string, c: bool) string: {
    match (a, b, c):
        when 1, "hello", true: "all match"
        when 2, _, false: "two false"
        when _, "skip", _: "skip middle"
        default: "none"
}

bind main = () int: {
    // 1. Primitive pairs
    println(test_pairs(0, 0))
    println(test_pairs(0, 1))
    println(test_pairs(1, 0))
    println(test_pairs(0, 99))
    println(test_pairs(77, 0))
    println(test_pairs(3, 15))
    println(test_pairs(100, 100))

    // 2. Enum combinations
    println(test_enums(Color.Red, Status.Pending))
    println(test_enums(Color.Green, Status.Active))
    println(test_enums(Color.Blue, Status.Done))
    println(test_enums(Color.Red, Status.Done))
    println(test_enums(Color.Blue, Status.Pending))

    // 3. Tagged union destructuring
    println("${test_destructure(Expr.Num(10), Expr.Num(20))}")
    println("${test_destructure(Expr.Pair(ExprPair[a: 3, b: 4]), Expr.Num(5))}")
    println("${test_destructure(Expr.Num(2), Expr.Pair(ExprPair[a: 10, b: 20]))}")
    println("${test_destructure(Expr.Pair(ExprPair[a: 1, b: 2]), Expr.Pair(ExprPair[a: 3, b: 4]))}")

    // 4. Three arguments
    println(test_three_args(1, "hello", true))
    println(test_three_args(2, "whatever", false))
    println(test_three_args(99, "skip", true))
    println(test_three_args(0, "other", false))
    0
}
`
	out := runInlineStrict(t, "multi_arg_match_e2e", src, false)
	expected := `origin
unit step
unit step
on y-axis
on x-axis
in ranges
other
red pending
green active or blue done
green active or blue done
red other
fallback
30
12
60
-1
all match
two false
skip middle
none
`
	if strings.TrimSpace(out) != strings.TrimSpace(expected) {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, out)
	}
}

func TestMultiArgMatchExhaustiveness(t *testing.T) {
	t.Parallel()
	src := `
define Color enum [ Red, Blue ]
define Status enum [ Open, Closed ]

bind test_exhaustive_cartesian = (c: Color, s: Status) int: {
    match (c, s):
        when Color.Red, Status.Open: 1
        when Color.Red, Status.Closed: 2
        when Color.Blue, Status.Open: 3
        default Color.Blue, Status.Closed: 4
}

bind test_exhaustive_multi_case_else = (c: Color, s: Status) int: {
    match (c, s):
        when Color.Red, Status.Open: 10
        when Color.Red, Status.Closed: 20
        default (Color.Blue, Status.Open), (Color.Blue, Status.Closed): 30
}

bind test_exhaustive_bools = (b1: bool, b2: bool) int: {
    match (b1, b2):
        when true, true: 100
        when (true, false), (false, true): 200
        default false, false: 300
}

bind main = () int: {
    println("${test_exhaustive_cartesian(Color.Red, Status.Open)}")
    println("${test_exhaustive_cartesian(Color.Red, Status.Closed)}")
    println("${test_exhaustive_cartesian(Color.Blue, Status.Open)}")
    println("${test_exhaustive_cartesian(Color.Blue, Status.Closed)}")

    println("${test_exhaustive_multi_case_else(Color.Red, Status.Open)}")
    println("${test_exhaustive_multi_case_else(Color.Blue, Status.Closed)}")

    println("${test_exhaustive_bools(true, true)}")
    println("${test_exhaustive_bools(true, false)}")
    println("${test_exhaustive_bools(false, true)}")
    println("${test_exhaustive_bools(false, false)}")
    0
}
`
	out := runInlineStrict(t, "multi_arg_exhaustive", src, false)
	expected := `1
2
3
4
10
30
100
200
200
300
`
	if strings.TrimSpace(out) != strings.TrimSpace(expected) {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, out)
	}
}

func TestMultiArgMatchSemanticsErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		err  string
	}{
		{
			name: "missing_cartesian_combination",
			src: `
define Color enum [ Red, Blue ]
define Status enum [ Open, Closed ]
bind main = (): {
    let c = Color.Red
    let s = Status.Open
    match (c, s):
        when Color.Red, Status.Open: 1
        when Color.Red, Status.Closed: 2
        default Color.Blue, Status.Open: 3
}
`,
			err: "missing combination(s): Color.Blue, Status.Closed",
		},
		{
			name: "zero_arg_match_disallows_condition_in_else",
			src: `
bind main = (): {
    match ():
        when true: 1
        default false: 2
}
`,
			err: "0-argument `match` expression must terminate with a bare `default:` arm",
		},
		{
			name: "multi_arg_non_enum_without_catch_all_else",
			src: `
bind main = (): {
    let x = 1
    let y = 2
    match (x, y):
        when 1, 2: 10
        default 3, 4: 20
}
`,
			err: "multi-argument `match` on non-enum/bool types must end with an open catch-all `default:` arm",
		},
		{
			name: "bindings_in_multi_case_arm_rejected",
			src: `
define Expr enum [ Num: int ]
bind main = (): {
    let e1 = Expr.Num(1)
    let e2 = Expr.Num(2)
    match (e1, e2):
        when (Expr.Num(a), _), (Expr.Num(b), _): a
        default: 0
}
`,
			err: "cannot bind variables in a multi-pattern arm",
		},
		{
			name: "conflicting_mutable_borrows_on_same_root",
			src: `
define Box enum [ Item: int ]
bind main = (): {
    let b = Box.Item(42)
    match (b, b):
        when Box.Item(&x), Box.Item(&y): { x += y }
        default: {}
}
`,
			err: "conflicting borrows/moves of variable \"b\" in `match` arm",
		},
		{
			name: "too_many_patterns_in_arm",
			src: `
bind main = (): {
    let a = 1
    let b = 2
    match (a, b):
        when 1, 2, 3: 10
        default: 0
}
`,
			err: "too many patterns for 2-argument match",
		},
		{
			name: "too_few_patterns_in_arm",
			src: `
bind main = (): {
    let a = 1
    let b = 2
    match (a, b):
        when 1: 10
        default: 0
}
`,
			err: "expected , separating patterns for match arguments",
		},
		{
			name: "too_few_patterns_in_tuple",
			src: `
bind main = (): {
    let a = 1
    let b = 2
    match (a, b):
        when (1, 2), (3): 10
        default: 0
}
`,
			err: "separating patterns in pattern tuple",
		},
	}

	root := projectRoot(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := driver.Check(driver.Options{
				EntryPath:   tc.name + ".ll",
				SrcOverride: tc.src,
				ProjectRoot: root,
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
