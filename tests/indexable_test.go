package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// =============================================================================
// Comprehensive Test Suite for Indexable and KeyIndexable Interfaces
// =============================================================================

func TestIndexableList(t *testing.T) {
	t.Parallel()
	src := `
bind sum_indexable = fn(col: Indexable[int]) int: {
    let total = 0
    let n = col.len()
    let i = 0
    for (i < n): {
        if (col.get(i) as val): {
            total += val
        }
        i += 1
    }
    total
}

bind sum_via_indexing = fn(col: Indexable[int]) int: {
    let total = 0
    let n = col.len()
    let i = 0
    for (i < n): {
        let val = col[i] ?? 0
        total += val
        i += 1
    }
    total
}

bind main = fn(): {
    let lst = [10, 20, 30, 40]

    assert(sum_indexable(lst) == 100)
    assert(sum_via_indexing(lst) == 100)

    let empty: [int] = []
    assert(sum_indexable(empty) == 0)
    assert(sum_via_indexing(empty) == 0)
}
`
	runInlineStrict(t, "indexable_list", src, false)
}

func TestIndexableArray(t *testing.T) {
	t.Parallel()
	src := `
bind first_and_last = fn(col: Indexable[int]) int: {
    let n = col.len()
    let first = col.get(0) ?? 0
    let last = col[n - 1] ?? 0
    first + last
}

bind main = fn(): {
    let arr = [4: 100, 200, 300, 400]

    assert(first_and_last(arr) == 500)
}
`
	runInlineStrict(t, "indexable_array", src, false)
}

func TestIndexableString(t *testing.T) {
	t.Parallel()
	src := `
bind count_matching_bytes = fn(s: Indexable[u8], target: u8) int: {
    let count = 0
    let n = s.len()
    let i = 0
    for (i < n): {
        if (s[i] as b): {
            if (b == target): count += 1
        }
        i += 1
    }
    count
}

bind main = fn(): {
    let text = "hello world"
    assert(count_matching_bytes(text, u8('l')) == 3)
    assert(count_matching_bytes(text, u8('o')) == 2)
    assert(count_matching_bytes(text, u8('z')) == 0)
}
`
	runInlineStrict(t, "indexable_string", src, false)
}

func TestIndexableCustomStruct(t *testing.T) {
	t.Parallel()
	src := `
define CustomSeq struct [
    first: int,
    second: int,
    third: int,
]

bind CustomSeq.len = fn(self) int: { 3 }

bind CustomSeq.get = fn(self, idx: int) ?int: {
    match (idx):
        when 0: self.first
        when 1: self.second
        when 2: self.third
        default: none
}

bind get_second = fn(col: Indexable[int]) ?int: {
    col[1]
}

bind main = fn(): {
    let seq = CustomSeq[first: 7, second: 14, third: 21]

    assert(seq.len() == 3)
    assert(seq.get(0) == 7)
    assert(seq.get(1) == 14)
    assert(seq.get(2) == 21)
    assert(seq.get(3) == none)

    assert(get_second(seq) == 14)
}
`
	runInlineStrict(t, "indexable_custom_struct", src, false)
}

func TestIndexableGenericBounds(t *testing.T) {
	t.Parallel()
	src := `
bind first_element[T: Copyable] = fn(col: Indexable[T]) ?T: {
    col.get(0)
}

bind element_at[T: Copyable] = fn(col: Indexable[T], idx: int) ?T: {
    col[idx]
}

bind main = fn(): {
    let lst = ["apple", "banana", "cherry"]
    assert(first_element(lst) == "apple")
    assert(element_at(lst, 1) == "banana")
    assert(element_at(lst, 5) == none)

    let nums = [3: 42, 43, 44]
    assert(first_element(nums) == 42)
    assert(element_at(nums, 2) == 44)
}
`
	runInlineStrict(t, "indexable_generic_bounds", src, false)
}

func TestKeyIndexableMap(t *testing.T) {
	t.Parallel()
	src := `
bind lookup_or_zero = fn(m: KeyIndexable[string, int], key: string) int: {
    m.get(key) ?? 0
}

bind lookup_via_bracket = fn(m: KeyIndexable[string, int], key: string) int: {
    m[key] ?? 0
}

bind main = fn(): {
    let ages: #[string: int] = #["Alice": 30, "Bob": 25]

    assert(lookup_or_zero(ages, "Alice") == 30)
    assert(lookup_or_zero(ages, "Bob") == 25)
    assert(lookup_or_zero(ages, "Charlie") == 0)

    assert(lookup_via_bracket(ages, "Alice") == 30)
    assert(lookup_via_bracket(ages, "Bob") == 25)
    assert(lookup_via_bracket(ages, "Charlie") == 0)
}
`
	runInlineStrict(t, "key_indexable_map", src, false)
}

func TestIndexableNegativeTypeChecks(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	tests := []struct {
		name        string
		src         string
		expectedErr string
	}{
		{
			name: "unindexable_int_as_argument",
			src: `
bind takes_indexable = fn(col: Indexable[int]): {}
bind main = fn(): {
    takes_indexable(123)
}
`,
			expectedErr: "argument 1 expects `Indexable[int]`, but this is `int`",
		},
		{
			name: "unindexable_struct_as_argument",
			src: `
define NotIndexable struct [
    value: int,
]
bind find_first = fn(col: Indexable[int]) ?int: {
    col.get(0)
}
bind main = fn(): {
    let item = NotIndexable[value: 42]
    find_first(item)
}
`,
			expectedErr: "argument 1 expects `Indexable[int]`, but this is `NotIndexable`",
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
