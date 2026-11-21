package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func TestRangeTypeAndIterationE2E(t *testing.T) {
	t.Parallel()
	src := `
define Step int

bind make_range = (lo: int, hi: int) Range[int]: {
    lo..hi
}

bind contains_via_iface = (c: Containable[int], x: int) bool: {
    c.contains(x)
}

bind format_printable = (p: Printable) string: {
    p.to_string()
}

bind sum_iterable = (it_obj: Iterable[int]) int: {
    let it = it_obj.iter()
    let total = 0
    let cur? = it.next()
    for (cur? != none): {
        total += cur? ?? 0
        cur? = it.next()
    }
    total
}

bind main = (): {
    // 1. Half-open and closed int ranges
    let r = 1..5
    let ri = 1..=5
    assert(r.contains(1), "1 in 1..5")
    assert(r.contains(4), "4 in 1..5")
    assert(not r.contains(5), "5 not in 1..5")
    assert(not r.contains(0), "0 not in 1..5")
    assert(ri.contains(5), "5 in 1..=5")
    assert(r.len() == 4, "len 1..5")
    assert(ri.len() == 5, "len 1..=5")
    assert(not r.is_empty(), "1..5 not empty")
    assert(r.to_string() == "1..5", "r to_string")
    assert(ri.to_string() == "1..=5", "ri to_string")
    assert(format_printable(ri) == "1..=5", "Range Printable")
    assert(contains_via_iface(r, 3), "Range Containable true")
    assert(not contains_via_iface(r, 5), "Range Containable false")
    assert(sum_iterable(r) == 10, "Range Iterable")

    // 2. Structural equality
    assert(r == (1..5), "same range eq")
    assert(r != ri, "exclusive vs inclusive neq")
    assert(r != (1..6), "different end neq")

    // 3. First-class passing/returning & loops
    let made = make_range(10, 14)
    let gathered = for (x in made): x
    assert(gathered.to_string() == "[10, 11, 12, 13]", "for over Range variable")

    let found: int = while (x in ri) int: {
        if (x == 4): while <- (x * 10)
    } else: -1
    assert(found == 40, "while over Range variable")

    // 4. Empty ranges
    let e1 = 5..5
    let e2 = 6..=5
    assert(e1.is_empty(), "5..5 empty")
    assert(e1.len() == 0, "5..5 len 0")
    assert(not e1.contains(5), "5..5 does not contain 5")
    assert(e2.is_empty(), "6..=5 empty")
    assert(e2.len() == 0, "6..=5 len 0")
    assert(not e2.contains(5), "6..=5 does not contain 5")
    let e1_vals = for (x in e1): x
    let e2_vals = for (x in e2): x
    assert(e1_vals.len() == 0, "empty 5..5 loop 0 iters")
    assert(e2_vals.len() == 0, "empty 6..=5 loop 0 iters")

    // 5. Fixed-width integers including u8 max bound
    let ru8: Range[u8] = u8(253)..=u8(255)
    assert(ru8.len() == 3, "u8 max range len")
    assert(ru8.contains(u8(255)), "u8 max range contains 255")
    let u8_vals = for (x in ru8): int(x)
    assert(u8_vals.to_string() == "[253, 254, 255]", "u8 inclusive max bound no overflow")

    // Exercise RangeIterator[u8] directly at 255u8 boundary
    let u8_it = (u8(254)..=u8(255)).iter()
    assert((u8_it.next() ?? u8(0)) == u8(254), "u8 iter 254")
    assert((u8_it.next() ?? u8(0)) == u8(255), "u8 iter 255")
    assert(u8_it.next() == none, "u8 iter exhausted cleanly")

    let ri8 = i8(-2)..=i8(2)
    assert(ri8.len() == 5, "i8 range len")
    assert(ri8.contains(i8(-2)), "i8 range contains -2")
    let i8_vals = for (x in ri8): int(x)
    assert(i8_vals.to_string() == "[-2, -1, 0, 1, 2]", "i8 negative-to-positive loop")

    let ru64: Range[u64] = u64(10)..u64(13)
    assert(ru64.len() == 3, "u64 range len")
    let u64_vals = for (x in ru64): x
    assert(u64_vals.len() == 3 and u64_vals[0] == u64(10) and u64_vals[2] == u64(12), "u64 loop")

    // 6. Char ranges (inline and first-class)
    let cr = 'a'..='d'
    assert(cr.contains('a'), "char contains a")
    assert(cr.contains('d'), "char contains d")
    assert(not cr.contains('e'), "char not contains e")
    assert(cr.len() == 4, "char len 4")
    assert(cr.to_string() == "a..=d", "char range to_string")
    let chars_inline = for (ch in 'x'..='z'): ch
    assert(chars_inline.len() == 3 and chars_inline[0] == 'x' and chars_inline[2] == 'z', "inline char range loop")
    let chars_var = for (ch in cr): ch
    assert(chars_var.len() == 4 and chars_var[0] == 'a' and chars_var[3] == 'd', "var char range loop")
    let found_ch? = while (ch in 'm'..='p'): {
        if (ch == 'o'): while <- ch
    }
    assert((found_ch? ?? '_') == 'o', "while char range")

    // 7. Distinct int range
    let dr = Step(10)..=Step(12)
    assert(dr.len() == 3, "distinct range len")
    assert(dr.contains(Step(11)), "distinct range contains")
    let dvals = for (s in dr): int(s)
    assert(dvals.to_string() == "[10, 11, 12]", "distinct range loop")
}
`
	runInlineStrict(t, "range_type_and_iter", src, false)
}

func TestWhenInContainmentPatternsE2E(t *testing.T) {
	t.Parallel()
	src := `
import "std/core"

define Pt struct [
    x: int,
    y: int,
]

define CustomBox struct [
    lo: int,
    hi: int,
]

bind CustomBox.contains = (self, item: int) bool: {
    item >= self.lo and item <= self.hi
}

define ResourceBag struct [
    items: [string],
]

bind ResourceBag.contains = (self, item: string) bool: {
    self.items.contains(item)
}

bind classify_int = (n: int, rng: &Range[int], lst: &List[int], arr: &[3: int], set_col: &#[int], map_col: &#[int: string], box_iface: &Containable[int]) string: {
    match (n):
        when in rng: "rng_var"
        when in 1..4: "range_lit"
        when in lst: "list_var"
        when in [20, 21]: "list_lit"
        when in arr: "arr_var"
        when in [2: 40, 41]: "arr_lit"
        when in set_col: "set_var"
        when in #[60, 61]: "set_lit"
        when in map_col: "map_var"
        when in #[80: "eighty", 81: "eighty-one"]: "map_lit"
        when in CustomBox[lo: 90, hi: 95]: "custom"
        when in box_iface: "boxed"
        default: "other"
}

bind classify_char = (ch: char) string: {
    match (ch):
        when in '0'..='9': "digit"
        when in ['a', 'e', 'i', 'o', 'u']: "vowel"
        default: "other"
}

bind classify_mixed = (n: int) string: {
    match (n):
        when 0, in 1..=3, in [10, 20]: "hit"
        default: "miss"
}

bind classify_pair = (a: int, b: char) string: {
    match (a, b):
        when in 1..=3, in ['x', 'y']: "first"
        when (in [10, 20], 'z'), (99, in 'a'..='c'): "combo"
        default: "none"
}

bind has_containable_generic[T] = (c: Containable[T], target: T) bool: {
    let via_method = c.contains(target)
    let via_match = match (target):
        when in c: true
        default: false
    via_method and via_match
}

bind check_opt_int = (o?: ?int) string: {
    let opt_list: [?int] = [10, none]
    match (o?):
        when in [1, 2, 3]: "unwrapped_in_list"
        when in opt_list: "opt_in_opt_list"
        default: "opt_miss"
}

bind main = (): {
    // 1. All built-in & custom Containable[int] types
    let rng = 100..=105
    let lst: List[int] = [10, 11]
    let arr: [3: int] = [3: 30, 31, 32]
    let s: #[int] = #[50, 51]
    let m: #[int: string] = #[70: "a", 71: "b"]
    let boxed: Containable[int] = [200, 201]

    assert(classify_int(103, &rng, &lst, &arr, &s, &m, &boxed) == "rng_var", "rng var")
    assert(classify_int(2, &rng, &lst, &arr, &s, &m, &boxed) == "range_lit", "range lit")
    assert(classify_int(11, &rng, &lst, &arr, &s, &m, &boxed) == "list_var", "list var")
    assert(classify_int(21, &rng, &lst, &arr, &s, &m, &boxed) == "list_lit", "list lit")
    assert(classify_int(31, &rng, &lst, &arr, &s, &m, &boxed) == "arr_var", "arr var")
    assert(classify_int(41, &rng, &lst, &arr, &s, &m, &boxed) == "arr_lit", "arr lit")
    assert(classify_int(50, &rng, &lst, &arr, &s, &m, &boxed) == "set_var", "set var")
    assert(classify_int(61, &rng, &lst, &arr, &s, &m, &boxed) == "set_lit", "set lit")
    assert(classify_int(71, &rng, &lst, &arr, &s, &m, &boxed) == "map_var", "map var")
    assert(classify_int(80, &rng, &lst, &arr, &s, &m, &boxed) == "map_lit", "map lit")
    assert(classify_int(92, &rng, &lst, &arr, &s, &m, &boxed) == "custom", "custom struct")
    assert(classify_int(201, &rng, &lst, &arr, &s, &m, &boxed) == "boxed", "boxed Containable")
    assert(classify_int(999, &rng, &lst, &arr, &s, &m, &boxed) == "other", "fallback")

    // Boxed Containable[int] backed by Range[int], Set[int], Array [N: int], Map[int, string], CustomBox
    let boxed_range: Containable[int] = 1..=5
    let boxed_set: Containable[int] = #[42, 43]
    let boxed_arr: Containable[int] = [2: 500, 501]
    let boxed_map: Containable[int] = #[600: "six-hundred"]
    let boxed_custom: Containable[int] = CustomBox[lo: 700, hi: 710]
    let r_hit = match (4):
        when in boxed_range: true
        default: false
    let s_hit = match (42):
        when in boxed_set: true
        default: false
    let a_hit = match (501):
        when in boxed_arr: true
        default: false
    let m_hit = match (600):
        when in boxed_map: true
        default: false
    let c_hit = match (705):
        when in boxed_custom: true
        default: false
    assert(r_hit, "boxed range")
    assert(s_hit, "boxed set")
    assert(a_hit, "boxed stack array")
    assert(m_hit, "boxed map")
    assert(c_hit, "boxed custom")

    // Generic Containable[T] function
    assert(has_containable_generic[int](1..=5, 3), "generic Containable Range")
    assert(has_containable_generic([10, 20], 20), "generic Containable List")
    assert(has_containable_generic([2: 30, 31], 31), "generic Containable StackArray")
    assert(has_containable_generic(#[40, 41], 40), "generic Containable Set")
    assert(has_containable_generic(#[50: "fifty"], 50), "generic Containable Map")
    assert(not has_containable_generic(1..=5, 99), "generic Containable miss")
    assert(has_containable_generic("alpha beta", "beta"), "generic Containable string")

    // Resource element stack array & List[string] containment
    let str_arr = [2: "red".copy(), "blue".copy()]
    let str_arr_hit = match ("blue"):
        when in str_arr: true
        default: false
    assert(str_arr_hit, "StackArray[string] when in")

    // Array[3, int] / Array[T] view & List[Pt] plain struct equality
    let arr_alias: Array[3, int] = [3: 7, 8, 9]
    let arr_res = match (8):
        when in arr_alias: true
        default: false
    assert(arr_res, "Array[3, int] contains")

    bind check_arr_view = (av: Array[int], target: int) bool: {
        match (target):
            when in av: true
            default: false
    }
    assert(check_arr_view(arr_alias, 9), "Array[T] view contains")

    let pts = [Pt[x: 1, y: 2], Pt[x: 3, y: 4]]
    let pt_hit = match (Pt[x: 3, y: 4]):
        when in pts: true
        default: false
    assert(pt_hit, "List[Pt] contains")

    // 2. Char ranges and list literals
    assert(classify_char('5') == "digit", "char digit")
    assert(classify_char('e') == "vowel", "char vowel")
    assert(classify_char('q') == "other", "char other")

    // 3. String substring containment + Resource container temporary cleanup
    let haystack = "alpha beta gamma"
    let str_res = match ("beta"):
        when in haystack: "found_in_var"
        default: "no"
    assert(str_res == "found_in_var", "string haystack var")

    let str_lit_res = match ("world"):
        when in "hello world": "sub_match"
        default: "no"
    assert(str_lit_res == "sub_match", "string lit contains")

    let bag_res = match ("two"):
        when in ResourceBag[items: ["one".copy(), "two".copy()]]: "bag_hit"
        default: "no"
    assert(bag_res == "bag_hit", "resource bag temporary drops cleanly")

    // 4. Empty list / non-matching temporary heap lists (leak check!)
    let empty_hit = match ("a"):
        when in ["x", "y"]: 1
        default: 0
    assert(empty_hit == 0, "miss heap list lit drops cleanly")

    let empty_list: [int] = []
    let zero_hit = match (1):
        when in empty_list: 1
        default: 0
    assert(zero_hit == 0, "empty list miss")

    // 5. Mixed patterns & multi-arg match
    assert(classify_mixed(0) == "hit", "mixed scalar")
    assert(classify_mixed(2) == "hit", "mixed range")
    assert(classify_mixed(20) == "hit", "mixed list")
    assert(classify_mixed(7) == "miss", "mixed miss")

    assert(classify_pair(2, 'y') == "first", "multi-arg both in")
    assert(classify_pair(20, 'z') == "combo", "multi-arg combo 1")
    assert(classify_pair(99, 'b') == "combo", "multi-arg combo 2")
    assert(classify_pair(99, 'z') == "none", "multi-arg miss")

    // 6. Optional scrutinee ?T against [T] and [?T]
    let opt_some?: ?int = 2
    let opt_some_opt?: ?int = 10
    let opt_none?: ?int = none
    let opt_miss?: ?int = 55
    assert(check_opt_int(opt_some?) == "unwrapped_in_list", "?int matches [int]")
    assert(check_opt_int(opt_some_opt?) == "opt_in_opt_list", "?int matches [?int]")
    assert(check_opt_int(opt_none?) == "opt_in_opt_list", "none matches [?int] containing none")
    assert(check_opt_int(opt_miss?) == "opt_miss", "?int miss")

    // 7. Short-circuit side-effect verification (earlier match skips later container eval)
    let eval_hits: [int] = []
    bind mk_list = (hits: &[int]) [int]: {
        hits.push(1)
        [100, 200]
    }
    let sc = match (1):
        when in 1..=5: "early"
        when in mk_list(&eval_hits): "late"
        default: "def"
    assert(sc == "early", "short-circuit hit")
    assert(eval_hits.len() == 0, "short-circuit did not evaluate later arm container")

    let no_sc = match (200):
        when in 1..=5: "early"
        when in mk_list(&eval_hits): "late"
        default: "def"
    assert(no_sc == "late", "later arm hit")
    assert(eval_hits.len() == 1, "evaluated later arm container once")

    // 8. Slice syntax regressions
    let s2 = "abcdef"
    assert(s2[1..] == "bcdef", "slice open end")
    assert(s2[..3] == "abc", "slice open start")
    assert(s2[..] == "abcdef", "slice full")
    assert(s2[1..=3] == "bcd", "slice inclusive")
    let v2 = [10, 20, 30, 40]
    assert(v2[1..=2].len() == 2, "list slice inclusive len")
}
`
	runInlineStrict(t, "when_in_containment", src, false)
}

func TestRangeAndWhenInNegativeDiagnostics(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tests := []struct {
		name        string
		src         string
		expectedErr string
	}{
		{
			name: "float_range_rejected",
			src: `
bind main = (): {
    let r = 1.0..2.0
}
`,
			expectedErr: "Range",
		},
		{
			name: "bare_range_pattern_hint",
			src: `
bind main = (): {
    let x = 3
    let y = match (x):
        when 1..5: 1
        default: 0
}
`,
			expectedErr: "use `when in ...:`",
		},
		{
			name: "non_containable_int",
			src: `
bind main = (): {
    let y = match (1):
        when in 42: 1
        default: 0
}
`,
			expectedErr: "Containable",
		},
		{
			name: "list_of_non_eq_struct_rejected",
			src: `
define NoEqRes struct [
    vals: [int],
]
bind main = (): {
    let probe = NoEqRes[vals: [1]]
    let list = [NoEqRes[vals: [1]]]
    let hit = match (probe):
        when in list: true
        default: false
}
`,
			expectedErr: "equality",
		},
		{
			name: "wrong_contains_signature_return_type",
			src: `
define BadContains struct [
    x: int,
]
bind BadContains.contains = (self, item: int) string: { "nope" }
bind main = (): {
    let b = BadContains[x: 1]
    let y = match (1):
        when in b: true
        default: false
}
`,
			expectedErr: "must return `bool`",
		},
		{
			name: "wrong_contains_signature_mut_self",
			src: `
define MutContains struct [
    x: int,
]
bind MutContains.contains = (&self, item: int) bool: { true }
bind main = (): {
    let b = MutContains[x: 1]
    let y = match (1):
        when in b: true
        default: false
}
`,
			expectedErr: "must have signature `(self, item: T) bool`",
		},
		{
			name: "scrutinee_container_item_type_mismatch",
			src: `
bind main = (): {
    let s = "abc"
    let y = match (s):
        when in [1, 2, 3]: true
        default: false
}
`,
			expectedErr: "mismatch",
		},
		{
			name: "optional_range_container_rejected",
			src: `
bind main = (): {
    let r?: ?Range[int] = 1..5
    let y = match (2):
        when in r?: true
        default: false
}
`,
			expectedErr: "cannot use optional value of type `?Range[int]`",
		},
		{
			name: "optional_list_container_rejected",
			src: `
bind main = (): {
    let l?: ?[int] = [1, 2]
    let y = match (2):
        when in l?: true
        default: false
}
`,
			expectedErr: "cannot use optional value of type `?[int]`",
		},
		{
			name: "when_in_zero_arg_match_rejected",
			src: `
bind main = (): {
    let y = match ():
        when in [1, 2]: 1
        default: 0
}
`,
			expectedErr: "0-argument `match`",
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
				combined += "\n" + err.Error()
			}
			if !strings.Contains(strings.ToLower(combined), strings.ToLower(tc.expectedErr)) {
				t.Fatalf("expected error containing %q, got:\n%s", tc.expectedErr, combined)
			}
		})
	}
}
