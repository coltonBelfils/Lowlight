package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
	"lowlight/internal/format"
)

func TestMatchMethodPatternsE2E(t *testing.T) {
	src := `
import "std/core"

define Checker interface [
    is_ok: (self) bool,
]

define Box struct [
    val: int,
]

bind Box.in_range = (self, lo: int, hi: int) bool: {
    self.val >= lo and self.val <= hi
}

bind Box.matches_eq[T] = (self, target: T) bool: {
    false
}

bind Box.is_even = (self) bool: {
    self.val % 2 == 0
}

bind Box.is_ok = (self) bool: {
    self.val > 0
}

bind Box.has_int[T] = (self, x: int) bool: {
    self.val == x
}

define Token enum [
    Num: int,
    Word: string,
    Empty,
]

bind Token.is_empty_tok = (self) bool: {
    match (self):
        when Token.Empty: true
        default: false
}

bind bool.is_false = (self) bool: not self

bind scan_number = (str: string) string: {
    let idx = 0
    let num = ""
    while (idx < str.len()): {
        match (char(str[idx])):
            when is_digit(), '.': num += string(char(str[idx]))
            default: while <-
        idx += 1
    }
    num
}

bind classify_char = (ch: char) string: {
    match (ch):
        when is_digit(): "digit"
        when is_alpha(): "alpha"
        when is_whitespace(): "ws"
        default: "other"
}

bind classify_list = (xs: [int]) string: {
    match (xs):
        when is_empty(): "empty"
        when contains(42): "has_42"
        default: "other"
}

bind classify_set_map = (s: #[int], m: #[string: int]) string: {
    match (s, m):
        when is_empty(), is_empty(): "both_empty"
        when (has(1), has("a")), (contains(2), contains("b")): "pair_hit"
        default: "miss"
}

bind check_iface = (c: Checker) bool: {
    match (c):
        when is_ok(): true
        default: false
}

bind free_flag = () bool: true

bind main = (): {
    // 1. User's motivating char loop pattern
    assert(scan_number("12.34abc") == "12.34", "scan_number stops at non-digit/dot")
    assert(scan_number("xyz") == "", "scan_number empty on leading alpha")
    assert(classify_char('7') == "digit", "char is_digit")
    assert(classify_char('Z') == "alpha", "char is_alpha")
    assert(classify_char(' ') == "ws", "char is_whitespace")
    assert(classify_char('#') == "other", "char other")

    // 2. User struct methods, arguments, and generic method call
    let b = Box[val: 14]
    let b_kind = match (b):
        when has_int[string](99): "ninety_nine"
        when in_range(10, 20): "in_10_20"
        when is_even(): "even"
        default: "other"
    assert(b_kind == "in_10_20", "struct method with args")

    let b2 = Box[val: 99]
    let b2_kind = match (b2):
        when has_int[string](99): "ninety_nine"
        default: "other"
    assert(b2_kind == "ninety_nine", "generic method pattern")

    // 3. Built-in collection methods
    let empty_xs: [int] = []
    let hit_xs = [1, 42, 3]
    let miss_xs = [1, 2, 3]
    assert(classify_list(empty_xs) == "empty", "vec is_empty")
    assert(classify_list(hit_xs) == "has_42", "vec contains")
    assert(classify_list(miss_xs) == "other", "vec other")

    let s_empty: #[int] = #[]
    let m_empty: #[string: int] = #[]
    let s1 = #[1, 9]
    let m1 = #["a": 10]
    let s2 = #[2]
    let m2 = #["b": 20]
    assert(classify_set_map(s_empty, m_empty) == "both_empty", "set/map is_empty")
    assert(classify_set_map(s1, m1) == "pair_hit", "set/map has")
    assert(classify_set_map(s2, m2) == "pair_hit", "set/map contains")
    assert(classify_set_map(s1, m2) == "miss", "set/map miss")

    // 4. Interface dynamic dispatch and arena ref[T]
    let ok_box: Checker = Box[val: 5]
    let bad_box: Checker = Box[val: -1]
    assert(check_iface(ok_box), "interface method pattern true")
    assert(not check_iface(bad_box), "interface method pattern false")

    let arena = Arena.new()
    let bref = arena.alloc(Box[val: 8])
    let ref_hit = match (bref):
        when is_even(): true
        default: false
    assert(ref_hit, "ref[Box] method pattern")

    // 5. Enum with both VariantPattern and MethodPattern
    let t1 = Token.Num(7)
    let t2 = Token.Empty
    let r1 = match (t1):
        when Token.Num(n): n
        when is_empty_tok(): 0
        default: -1
    let r2 = match (t2):
        when Token.Num(n): n
        when is_empty_tok(): 0
        default: -1
    assert(r1 == 7 and r2 == 0, "enum VariantPattern + MethodPattern")

    // 6. Bool method pattern vs bool literal
    let f = false
    let f_res = match (f):
        when is_false(): "was_false"
        default: "was_true"
    assert(f_res == "was_false", "bool method pattern")

    // 7. 0-arg match continues to call free functions
    let z_res = match ():
        when free_flag(): 1
        default: 0
    assert(z_res == 1, "0-arg match free function")

    // 8. Short-circuiting: later arm args are not evaluated if earlier arm matches
    let arg_calls = 0
    bind bump = (cnt: &int) int: {
        cnt += 1
        99
    }
    let sc = match (Box[val: 4]):
        when is_even(): "early"
        when has_int[int](bump(&arg_calls)): "late"
        default: "def"
    assert(sc == "early" and arg_calls == 0, "short-circuit skips later method pattern args")
}
`
	runInlineStrict(t, "match_method_patterns", src, false)
}

func TestMatchMethodPatternNegativeDiagnostics(t *testing.T) {
	root := projectRoot(t)
	tests := []struct {
		name        string
		src         string
		expectedErr string
	}{
		{
			name: "non_bool_return_type",
			src: `
define Box struct [ v: int ]
bind Box.get_v = (self) int: self.v
bind main = (): {
    let b = Box[v: 1]
    let x = match (b):
        when get_v(): 1
        default: 0
}
`,
			expectedErr: "must return `bool`",
		},
		{
			name: "mutating_self_method_rejected",
			src: `
define Box struct [ v: int ]
bind Box.bump = (&self) bool: {
    self.v += 1
    true
}
bind main = (): {
    let b = Box[v: 1]
    let x = match (b):
        when bump(): 1
        default: 0
}
`,
			expectedErr: "must take read-only `self`",
		},
		{
			name: "consuming_self_method_rejected",
			src: `
define Box struct [ v: int ]
bind Box.consume = (own self) bool: true
bind main = (): {
    let b = Box[v: 1]
    let x = match (b):
        when consume(): 1
        default: 0
}
`,
			expectedErr: "must take read-only `self`",
		},
		{
			name: "builtin_mutating_method_rejected",
			src: `
bind main = (): {
    let s = #[1, 2]
    let x = match (s):
        when insert(3): 1
        default: 0
}
`,
			expectedErr: "must take read-only `self`",
		},
		{
			name: "optional_scrutinee_rejected",
			src: `
bind main = (): {
    let ch?: ?char = '5'
    let x = match (ch?):
        when is_digit(): 1
        default: 0
}
`,
			expectedErr: "optional scrutinee",
		},
		{
			name: "missing_method_rejected",
			src: `
bind main = (): {
    let x = match ('a'):
        when no_such_method(): 1
        default: 0
}
`,
			expectedErr: "has no method",
		},
		{
			name: "wrong_arg_count_rejected",
			src: `
bind main = (): {
    let x = match ('a'):
        when is_digit(1): 1
        default: 0
}
`,
			expectedErr: "expected 0",
		},
		{
			name: "non_exhaustive_bool_with_method_pattern",
			src: `
bind bool.is_false = (self) bool: not self
bind main = (): {
    let b = true
    let x = match (b):
        when true: 1
        default is_false(): 0
}
`,
			expectedErr: "non-exhaustive",
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

func TestFormatMatchMethodPattern(t *testing.T) {
	src := `bind check = (ch: char) bool: {
	match(ch):
		when is_digit(), '.': true
		when is_ascii_hex[int](16): true
		default: false
}
`
	out, err := format.Format("test.ll", src, format.DefaultOptions())
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if !strings.Contains(out, "when is_digit(), '.': true") {
		t.Fatalf("unexpected formatted output:\n%s", out)
	}
	if !strings.Contains(out, "when is_ascii_hex[int](16): true") {
		t.Fatalf("unexpected generic method pattern formatted output:\n%s", out)
	}
}
