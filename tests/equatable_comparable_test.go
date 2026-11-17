package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// =============================================================================
// Comprehensive Test Suite for Equatable and Comparable Interfaces & Operators
// =============================================================================

func TestCustomEquatableStruct(t *testing.T) {
	src := `
define Point struct [
    x: int,
    y: int,
]

bind Point.copy = (self) Point: {
    Point[x: self.x, y: self.y]
}

bind Point.eq = (self, other: Point) bool: {
    self.x == other.x and self.y == other.y
}

bind are_equal[T: Equatable] = (a: T, b: T) bool: {
    a == b
}

bind are_not_equal[T: Equatable] = (a: T, b: T) bool: {
    a != b
}

bind main = (): {
    let p1 = Point[x: 10, y: 20]
    let p2 = Point[x: 10, y: 20]
    let p3 = Point[x: 10, y: 25]

    // Direct operator usage on custom struct
    assert(p1 == p2)
    assert(p1 != p3)
    assert(not (p1 == p3))

    // Method call usage
    assert(p1.eq(p2))
    assert(not p1.eq(p3))

    // Generic function usage bounded by [T: Equatable]
    assert(are_equal(p1, p2))
    assert(not are_equal(p1, p3))
    assert(are_not_equal(p1, p3))
    assert(not are_not_equal(p1, p2))
}
`
	runInlineStrict(t, "custom_equatable_struct", src, false)
	runInlineStrict(t, "custom_equatable_struct_release", src, true)
}

func TestCustomComparableStruct(t *testing.T) {
	src := `
define Score struct [
    pts: int,
]

bind Score.copy = (self) Score: {
    Score[pts: self.pts]
}

bind Score.cmp = (self, other: Score) int: {
    if (self.pts < other.pts): -1
    else if (self.pts > other.pts): 1
    else: 0
}

bind is_less[T: Comparable] = (a: T, b: T) bool: {
    a < b
}

bind is_greater[T: Comparable] = (a: T, b: T) bool: {
    a > b
}

bind is_equal_via_cmp[T: Comparable] = (a: T, b: T) bool: {
    a == b
}

bind main = (): {
    let s1 = Score[pts: 100]
    let s2 = Score[pts: 200]
    let s3 = Score[pts: 100]

    // Method call
    assert(s1.cmp(s2) < 0)
    assert(s2.cmp(s1) > 0)
    assert(s1.cmp(s3) == 0)

    // Direct relational operators
    assert(s1 < s2)
    assert(s1 <= s2)
    assert(s1 <= s3)
    assert(s2 > s1)
    assert(s2 >= s1)
    assert(s1 >= s3)

    // Equality derived from cmp (Comparable satisfies Equatable)
    assert(s1 == s3)
    assert(s1 != s2)

    // Generic comparison functions
    assert(is_less(s1, s2))
    assert(not is_less(s2, s1))
    assert(is_greater(s2, s1))
    assert(is_equal_via_cmp(s1, s3))
    assert(not is_equal_via_cmp(s1, s2))

    // Prelude min, max, clamp on custom Comparable struct
    let m = min(s1, s2)
    assert(m.pts == 100)

    let mx = max(s1, s2)
    assert(mx.pts == 200)

    let cl1 = clamp(Score[pts: 50], s1, s2)
    assert(cl1.pts == 100)

    let cl2 = clamp(Score[pts: 250], s1, s2)
    assert(cl2.pts == 200)

    let cl3 = clamp(Score[pts: 150], s1, s2)
    assert(cl3.pts == 150)
}
`
	runInlineStrict(t, "custom_comparable_struct", src, false)
	runInlineStrict(t, "custom_comparable_struct_release", src, true)
}

func TestPrimitiveEquatableAndComparable(t *testing.T) {
	src := `
bind test_generic_eq[T: Equatable] = (a: T, b: T) bool: {
    a == b
}

bind test_generic_cmp[T: Comparable] = (a: T, b: T) int: {
    a.cmp(b)
}

bind main = (): {
    // 1. bool (Equatable only)
    assert(true.eq(true))
    assert(not true.eq(false))
    assert(test_generic_eq(true, true))
    assert(not test_generic_eq(true, false))

    // 2. int
    assert((42).eq(42))
    assert(not (42).eq(43))
    assert((10).cmp(20) < 0)
    assert((20).cmp(10) > 0)
    assert((15).cmp(15) == 0)
    assert(min(10, 20) == 10)
    assert(max(10, 20) == 20)
    assert(clamp(5, 10, 20) == 10)
    assert(clamp(25, 10, 20) == 20)
    assert(clamp(15, 10, 20) == 15)
    assert(test_generic_cmp(10, 20) < 0)

    // 3. float
    assert((3.14).eq(3.14))
    assert(not (3.14).eq(2.71))
    assert((1.5).cmp(2.5) < 0)
    assert((2.5).cmp(1.5) > 0)
    assert((2.0).cmp(2.0) == 0)
    assert(min(1.5, 2.5) == 1.5)
    assert(max(1.5, 2.5) == 2.5)
    assert(clamp(0.5, 1.0, 3.0) == 1.0)

    // 4. char
    assert(('a').eq('a'))
    assert(not ('a').eq('b'))
    assert(('a').cmp('z') < 0)
    assert(('z').cmp('a') > 0)
    assert(('c').cmp('c') == 0)
    assert(min('a', 'z') == 'a')
    assert(max('a', 'z') == 'z')

    // 5. Fixed-width integers: u8, u16, u32, u64, i8, i16, i32
    let u8_a: u8 = 10
    let u8_b: u8 = 20
    assert(u8_a.eq(u8_a))
    assert(u8_a.cmp(u8_b) < 0)

    let u16_a: u16 = 1000
    let u16_b: u16 = 2000
    assert(u16_a.eq(u16_a))
    assert(u16_a.cmp(u16_b) < 0)

    let u32_a: u32 = 100000
    let u32_b: u32 = 200000
    assert(u32_a.eq(u32_a))
    assert(u32_a.cmp(u32_b) < 0)

    let u64_a: u64 = 1000000000
    let u64_b: u64 = 2000000000
    assert(u64_a.eq(u64_a))
    assert(u64_a.cmp(u64_b) < 0)

    let i8_a: i8 = -10
    let i8_b: i8 = 10
    assert(i8_a.eq(i8_a))
    assert(i8_a.cmp(i8_b) < 0)

    let i16_a: i16 = -1000
    let i16_b: i16 = 1000
    assert(i16_a.eq(i16_a))
    assert(i16_a.cmp(i16_b) < 0)

    let i32_a: i32 = -100000
    let i32_b: i32 = 100000
    assert(i32_a.eq(i32_a))
    assert(i32_a.cmp(i32_b) < 0)

    // 6. string
    let s_alpha = "apple"
    let s_beta = "banana"
    let s_alpha2 = "apple"
    assert(s_alpha.eq(s_alpha2))
    assert(not s_alpha.eq(s_beta))
    assert(s_alpha.cmp(s_beta) < 0)
    assert(s_beta.cmp(s_alpha) > 0)
    assert(s_alpha.cmp(s_alpha2) == 0)
    assert(s_alpha < s_beta)
    assert(s_beta > s_alpha)
    assert(min(s_alpha, s_beta) == "apple")
    assert(max(s_alpha, s_beta) == "banana")
    assert(clamp("cherry", "apple", "banana") == "banana")
    assert(clamp("aardvark", "apple", "banana") == "apple")
    assert(clamp("avocado", "apple", "banana") == "avocado")
}
`
	runInlineStrict(t, "primitive_equatable_comparable", src, false)
	runInlineStrict(t, "primitive_equatable_comparable_release", src, true)
}

func TestDistinctComparableAndEquatable(t *testing.T) {
	src := `
define Meters int
define Seconds int

bind main = (): {
    let m1 = Meters(10)
    let m2 = Meters(25)
    let m3 = Meters(10)

    assert(m1 == m3)
    assert(m1 != m2)
    assert(m1 < m2)
    assert(m2 > m1)

    let lowest = min(m1, m2)
    assert(lowest == m1)

    let highest = max(m1, m2)
    assert(highest == m2)

    let cl = clamp(Meters(50), m1, m2)
    assert(cl == m2)
}
`
	runInlineStrict(t, "distinct_comparable_equatable", src, false)
	runInlineStrict(t, "distinct_comparable_equatable_release", src, true)
}

func TestCustomEquatableAndComparableTaggedEnum(t *testing.T) {
	src := `
define Val enum [
    Empty,
    Str: string,
    Obj: #[string: Val],
]

bind Val.eq = (self, other: Val) bool: {
    match (self, other):
        when Val.Empty, Val.Empty: true
        when Val.Str(a), Val.Str(b): a == b
        when Val.Obj(a), Val.Obj(b): {
            if (a.len() != b.len()): <- false
            while (e in a): {
                if ((not b.contains(e.key)) or e.value != b[e.key]): while <- false
            } else: true
        }
        default: false
}

define Rank enum [
    Low: int,
    High: int,
]

bind Rank.cmp = (self, other: Rank) int: {
    let lv = match (self):
        when Rank.Low(n): n
        default Rank.High(n): n + 1000
    let rv = match (other):
        when Rank.Low(n): n
        default Rank.High(n): n + 1000
    if (lv < rv): -1
    else if (lv > rv): 1
    else: 0
}

bind eq_generic[T: Equatable] = (a: T, b: T) bool: { a == b }
bind lt_generic[T: Comparable] = (a: T, b: T) bool: { a < b }

bind main = (): {
    // 1. Tagged enum with non-equatable Map payload + custom .eq
    let v1 = Val.Obj(#["x": Val.Str("hello")])
    let v2 = Val.Obj(#["x": Val.Str("hello")])
    let v3 = Val.Obj(#["x": Val.Str("world")])
    assert(v1 == v2, "enum custom eq maps")
    assert(v1 != v3, "enum custom eq differs")
    assert(eq_generic(v1, v2), "generic eq enum")

    // Wrapper types (optional & list containment) go through synthesized ll_eq__ll_e_Val
    let ov1?: ?Val = Val.Obj(#["k": Val.Empty])
    let ov2?: ?Val = Val.Obj(#["k": Val.Empty])
    assert(ov1? == ov2?, "optional enum custom eq")
    let vals = [v1.copy(), Val.Empty]
    let in_vals = match (v2):
        when in vals: true
        default: false
    assert(in_vals, "list contains custom eq enum")

    // 2. Tagged enum with custom .cmp (derives both Ordering and Equatable)
    let r1 = Rank.Low(50)
    let r2 = Rank.High(1)
    let r3 = Rank.High(1)
    assert(r1 < r2, "enum custom cmp lt")
    assert(r2 > r1, "enum custom cmp gt")
    assert(r2 == r3, "enum custom cmp eq")
    assert(r1 != r2, "enum custom cmp neq")
    assert(lt_generic(r1, r2), "generic cmp enum")
    assert(eq_generic(r2, r3), "generic eq via cmp enum")

    let or1?: ?Rank = Rank.High(1)
    let or2?: ?Rank = Rank.High(1)
    assert(or1? == or2?, "optional enum custom cmp eq")
    let ranks = [Rank.High(1), Rank.Low(10)]
    let in_ranks = match (r2):
        when in ranks: true
        default: false
    assert(in_ranks, "list contains custom cmp enum")
}
`
	runInlineStrict(t, "custom_eq_cmp_tagged_enum", src, false)
	runInlineStrict(t, "custom_eq_cmp_tagged_enum_release", src, true)
}

func TestCompileTimeNegativeEquatableComparable(t *testing.T) {
	root := projectRoot(t)

	tests := []struct {
		name        string
		src         string
		expectedErr string
	}{
		{
			name: "bool_ordering_disallowed",
			src: `
bind main = (): {
    let a = true
    let b = false
    let c = a < b
}
`,
			expectedErr: "does not implement Comparable",
		},
		{
			name: "bool_not_comparable_for_min",
			src: `
bind main = (): {
    let m = min(true, false)
}
`,
			expectedErr: "does not satisfy interface Comparable",
		},
		{
			name: "uncomparable_struct_disallowed_relational",
			src: `
define Pair struct [
    a: int,
    b: int,
]
bind main = (): {
    let p1 = Pair[a: 1, b: 2]
    let p2 = Pair[a: 2, b: 3]
    let check = p1 < p2
}
`,
			expectedErr: "does not implement Comparable",
		},
		{
			name: "uncomparable_struct_rejected_by_generic_bound",
			src: `
define Pair struct [
    a: int,
    b: int,
]
bind find_min[T: Comparable] = (x: T, y: T) T: {
    if (x < y): x else: y
}
bind main = (): {
    let p1 = Pair[a: 1, b: 2]
    let p2 = Pair[a: 2, b: 3]
    find_min(p1, p2)
}
`,
			expectedErr: "does not satisfy interface Comparable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			rc, err := driver.Run(driver.Options{
				EntryPath:   filepath.Join(t.TempDir(), tc.name+".ll"),
				SrcOverride: tc.src,
				ProjectRoot: root,
			}, nil, &stdout, &stderr)
			if err == nil && rc == 0 {
				t.Fatalf("expected compile error containing %q, got success", tc.expectedErr)
			}
			combined := stderr.String()
			if err != nil {
				combined += err.Error()
			}
			if !strings.Contains(combined, tc.expectedErr) {
				t.Fatalf("expected error containing %q, got: %s", tc.expectedErr, combined)
			}
		})
	}
}
