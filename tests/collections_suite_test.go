package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// =============================================================================
// Comprehensive Test Suite for Lowlight Standard Prelude Collections:
// 1. List[T] (@lang("list"))
// 2. Set[T]  (@lang("set"))
// 3. Map[K, V] (@lang("map"))
// =============================================================================

func TestListComprehensive(t *testing.T) {
	src := `
bind print_val = (p: Printable): string {
    p.to_string()
}

bind sum_iter = (it_obj: Iterable[int]): int {
    let it = it_obj.iter()
    let sum = 0
    let cur? = it.next()
    for (cur? != none) {
        sum += cur? ?? 0
        cur? = it.next()
    }
    sum
}

bind main = () {
    // 1. Creation & initial state
    let list = List[int].new()
    assert(list.len == 0)
    assert(list.is_empty())

    // 2. Push & dynamic reallocation
    let i = 1
    for (i <= 20) {
        list.push(i * 10)
        i += 1
    }
    assert(list.len == 20)
    assert(not list.is_empty())

    // 3. Get with bounds check
    let g0? = list.get(0)
    assert((g0? ?? 0) == 10)
    let g19? = list.get(19)
    assert((g19? ?? 0) == 200)
    let g_out? = list.get(100)
    assert(g_out? == none)
    let g_neg? = list.get(-1)
    assert(g_neg? == none)

    // 4. Pop LIFO order
    let p1? = list.pop()
    assert((p1? ?? 0) == 200)
    let p2? = list.pop()
    assert((p2? ?? 0) == 190)
    assert(list.len == 18)

    // 5. Deep Copy
    let list2 = list.copy()
    assert(list2.len == 18)
    let l2_pop? = list2.pop()
    assert((l2_pop? ?? 0) == 180)
    assert(list2.len == 17)
    assert(list.len == 18) // original unchanged

    // 6. Printable interface & to_string
    let short_list = List[int].new()
    short_list.push(1)
    short_list.push(2)
    short_list.push(3)
    assert(short_list.to_string() == "[1, 2, 3]")
    assert(print_val(short_list) == "[1, 2, 3]")

    // 7. Iterable[int] & Iterator[int]
    let it = short_list.iter()
    let it1? = it.next()
    assert((it1? ?? 0) == 1)
    let it2? = it.next()
    assert((it2? ?? 0) == 2)
    let it3? = it.next()
    assert((it3? ?? 0) == 3)
    let it4? = it.next()
    assert(it4? == none)
    assert(sum_iter(short_list) == 6)

    // 8. Collection comprehension (Collectable[int])
    let collected: List[int] = for (x in 1..=5): List[int] x * 100
    assert(collected.len == 5)
    assert((collected.get(0) ?? 0) == 100)
    assert((collected.get(4) ?? 0) == 500)

    // 9. Clear
    short_list.clear()
    assert(short_list.len == 0)
    assert(short_list.is_empty())
    assert(short_list.pop() == none)

    // 10. List of Strings (Resource ownership)
    let str_list = List[string].new()
    str_list.push("apple")
    str_list.push("banana")
    str_list.push("cherry")
    assert(str_list.len == 3)
    assert((str_list.get(1) ?? "") == "banana")
    let str_cp = str_list.copy()
    assert(str_cp.len == 3)
    assert(str_cp.to_string() == "[apple, banana, cherry]")
}
`
	runInlineStrict(t, "list_comprehensive", src, false)
}

func TestSetComprehensive(t *testing.T) {
	src := `
bind print_val = (p: Printable): string {
    p.to_string()
}

bind sum_iter = (it_obj: Iterable[int]): int {
    let it = it_obj.iter()
    let sum = 0
    let cur? = it.next()
    for (cur? != none) {
        sum += cur? ?? 0
        cur? = it.next()
    }
    sum
}

bind main = () {
    // 1. Creation & initial state
    let s = Set[int].new()
    assert(s.len == 0)
    assert(s.is_empty())

    // 2. Insertion & Deduplication
    assert(s.insert(10))
    assert(s.insert(20))
    assert(s.insert(30))
    assert(s.len == 3)
    assert(not s.insert(20)) // Duplicate rejected
    assert(not s.insert(10)) // Duplicate rejected
    assert(s.len == 3)

    // 3. Query Membership
    assert(s.has(10))
    assert(s.has(20))
    assert(s.has(30))
    assert(s.contains(20))
    assert(not s.has(40))
    assert(not s.contains(999))

    // 4. Removal (tombstone preservation)
    assert(s.remove(20))
    assert(s.len == 2)
    assert(not s.has(20))
    assert(not s.remove(20)) // Already removed
    assert(not s.remove(999))
    assert(s.len == 2)
    assert(s.has(10))
    assert(s.has(30))

    // Re-inserting previously removed item succeeds
    assert(s.insert(20))
    assert(s.len == 3)
    assert(s.has(20))

    // 5. Growth & Rehash across many elements
    let set_large = Set[int].new()
    let i = 1
    for (i <= 50) {
        assert(set_large.insert(i))
        i += 1
    }
    assert(set_large.len == 50)
    let j = 1
    for (j <= 50) {
        assert(set_large.has(j))
        j += 1
    }
    assert(not set_large.has(51))
    assert(not set_large.has(0))

    // 6. Deep Copy
    let s_copy = s.copy()
    assert(s_copy.len == 3)
    assert(s_copy.has(10))
    assert(s_copy.has(20))
    assert(s_copy.has(30))
    assert(s_copy.remove(10))
    assert(not s_copy.has(10))
    assert(s.has(10)) // original unaffected

    // 7. Conversion to List
    let list_view = s.to_list()
    assert(list_view.len == 3)

    // 8. Printable interface
    let s_print = Set[int].new()
    s_print.insert(42)
    assert(s_print.to_string() == "#[42]")
    assert(print_val(s_print) == "#[42]")

    // 9. Iterable & Iterator
    let s_iter = Set[int].new()
    s_iter.insert(10)
    s_iter.insert(20)
    s_iter.insert(30)
    let it = s_iter.iter()
    let count = 0
    let item? = it.next()
    for (item? != none) {
        count += 1
        item? = it.next()
    }
    assert(count == 3)
    assert(sum_iter(s_iter) == 60)

    // 10. Collection Comprehension (Collectable[int])
    let collected: Set[int] = for (x in 1..=4): Set[int] x * 5
    assert(collected.len == 4)
    assert(collected.has(5))
    assert(collected.has(10))
    assert(collected.has(15))
    assert(collected.has(20))

    // 11. Clear
    s.clear()
    assert(s.len == 0)
    assert(s.is_empty())
    assert(not s.has(10))

    // 12. Set of Strings (Resource ownership)
    let str_set = Set[string].new()
    assert(str_set.insert("alpha"))
    assert(str_set.insert("beta"))
    assert(str_set.insert("gamma"))
    assert(str_set.len == 3)
    assert(str_set.has("beta"))
    assert(not str_set.has("delta"))
    assert(not str_set.insert("beta"))
    assert(str_set.remove("beta"))
    assert(not str_set.has("beta"))
    assert(str_set.len == 2)
}
`
	runInlineStrict(t, "set_comprehensive", src, false)
}

func TestMapComprehensive(t *testing.T) {
	src := `
bind print_val = (p: Printable): string {
    p.to_string()
}

bind count_iter = (it_obj: Iterable[MapEntry[string, int]]): int {
    let it = it_obj.iter()
    let count = 0
    let cur? = it.next()
    for (cur? != none) {
        count += 1
        cur? = it.next()
    }
    count
}

bind main = () {
    // 1. Creation & initial state
    let m = Map[string, int].new()
    assert(m.len == 0)
    assert(m.is_empty())

    // 2. Insertion & Membership
    m.insert("Alice", 100)
    m.insert("Bob", 200)
    m.insert("Charlie", 300)
    assert(m.len == 3)
    assert(not m.is_empty())
    assert(m.has("Alice"))
    assert(m.has("Bob"))
    assert(m.has("Charlie"))
    assert(m.contains("Bob"))
    assert(not m.has("Dave"))

    // 3. Retrieval with .get()
    let val_a? = m.get("Alice")
    assert((val_a? ?? 0) == 100)
    let val_b? = m.get("Bob")
    assert((val_b? ?? 0) == 200)
    let val_d? = m.get("Dave")
    assert(val_d? == none)

    // 4. Overwrite existing key in-place
    m.insert("Alice", 999)
    assert(m.len == 3) // length does not change
    let val_a2? = m.get("Alice")
    assert((val_a2? ?? 0) == 999)

    // 5. Removal
    let rem_b? = m.remove("Bob")
    assert((rem_b? ?? 0) == 200)
    assert(m.len == 2)
    assert(not m.has("Bob"))
    let rem_b_again? = m.remove("Bob")
    assert(rem_b_again? == none)
    assert(m.len == 2)

    // Re-inserting previously removed key
    m.insert("Bob", 250)
    assert(m.len == 3)
    assert(m.has("Bob"))
    assert((m.get("Bob") ?? 0) == 250)

    // 6. Growth & Rehash across many keys
    let map_large = Map[int, int].new()
    let i = 1
    for (i <= 40) {
        map_large.insert(i, i * 10)
        i += 1
    }
    assert(map_large.len == 40)
    let j = 1
    for (j <= 40) {
        assert(map_large.has(j))
        assert((map_large.get(j) ?? 0) == j * 10)
        j += 1
    }
    assert(not map_large.has(41))

    // 7. Keys & Values projection
    let keys = m.keys()
    assert(keys.len == 3)
    let values = m.values()
    assert(values.len == 3)

    // 8. Deep Copy
    let m_copy = m.copy()
    assert(m_copy.len == 3)
    assert(m_copy.has("Alice"))
    assert(m_copy.has("Bob"))
    assert(m_copy.has("Charlie"))
    let rem_cp? = m_copy.remove("Alice")
    assert((rem_cp? ?? 0) == 999)
    assert(not m_copy.has("Alice"))
    assert(m.has("Alice")) // original unaffected

    // 9. Printable interface
    let m_single = Map[string, int].new()
    m_single.insert("key", 42)
    assert(m_single.to_string() == "#[key: 42]")
    assert(print_val(m_single) == "#[key: 42]")

    // 10. Iterable & Iterator
    let count = count_iter(m)
    assert(count == 3)
    let it = m.iter()
    let ent1? = it.next()
    assert(ent1? != none)

    // 11. Collection Comprehension (Collectable[MapEntry[string, int]])
    let collected: Map[string, int] = for (x in 1..=3): Map[string, int] MapEntry[string, int][
        key: if (x == 1) "one" else if (x == 2) "two" else "three",
        value: x * 10,
    ]
    assert(collected.len == 3)
    assert((collected.get("one") ?? 0) == 10)
    assert((collected.get("two") ?? 0) == 20)
    assert((collected.get("three") ?? 0) == 30)

    // 12. Clear
    m.clear()
    assert(m.len == 0)
    assert(m.is_empty())
    assert(not m.has("Alice"))
}
`
	runInlineStrict(t, "map_comprehensive", src, false)
}

func TestCustomHashableWithSetAndMap(t *testing.T) {
	src := `
define UserId struct [
    id: int,
]

bind UserId.hash = (self): u64 {
    self.id.hash()
}

bind UserId.copy = (self): UserId {
    UserId[id: self.id]
}

bind UserId.to_string = (self): string {
    "User(${self.id})"
}

bind main = () {
    let u1 = UserId[id: 101]
    let u2 = UserId[id: 202]
    let u3 = UserId[id: 303]

    // 1. Set with custom Hashable key
    let s = Set[UserId].new()
    assert(s.insert(u1))
    assert(s.insert(u2))
    assert(s.len == 2)
    assert(s.has(u1))
    assert(s.has(u2))
    assert(not s.has(u3))
    assert(not s.insert(u1)) // duplicate rejected
    assert(s.remove(u1))
    assert(not s.has(u1))
    assert(s.len == 1)

    // 2. Map with custom Hashable key
    let m = Map[UserId, string].new()
    m.insert(u1, "Alice")
    m.insert(u2, "Bob")
    assert(m.len == 2)
    assert(m.has(u1))
    assert((m.get(u1) ?? "") == "Alice")
    assert((m.get(u2) ?? "") == "Bob")
    assert(m.get(u3) == none)
    let rem? = m.remove(u1)
    assert((rem? ?? "") == "Alice")
    assert(not m.has(u1))
    assert(m.len == 1)
}
`
	runInlineStrict(t, "custom_hashable", src, false)
}

func TestNonHashableRejected(t *testing.T) {
	root := projectRoot(t)

	// Set with non-hashable type
	srcSet := `
define NoHash struct [
    x: int,
]
bind main = () {
    let s = Set[NoHash].new()
}
`
	var stdoutSet, stderrSet bytes.Buffer
	rcSet, errSet := driver.Run(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "set_nohash.ll"),
		SrcOverride: srcSet,
		ProjectRoot: root,
		Release:     false,
	}, nil, &stdoutSet, &stderrSet)
	if errSet == nil && rcSet == 0 {
		t.Fatalf("expected compile error when using Set with non-Hashable type, got success")
	}
	combinedSet := stderrSet.String()
	if errSet != nil {
		combinedSet += errSet.Error()
	}
	if !strings.Contains(combinedSet, "Hashable") {
		t.Fatalf("expected error mentioning 'Hashable', got: %s", combinedSet)
	}

	// Map with non-hashable key
	srcMap := `
define NoHash struct [
    x: int,
]
bind main = () {
    let m = Map[NoHash, int].new()
}
`
	var stdoutMap, stderrMap bytes.Buffer
	rcMap, errMap := driver.Run(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "map_nohash.ll"),
		SrcOverride: srcMap,
		ProjectRoot: root,
		Release:     false,
	}, nil, &stdoutMap, &stderrMap)
	if errMap == nil && rcMap == 0 {
		t.Fatalf("expected compile error when using Map with non-Hashable key, got success")
	}
	combinedMap := stderrMap.String()
	if errMap != nil {
		combinedMap += errMap.Error()
	}
	if !strings.Contains(combinedMap, "Hashable") {
		t.Fatalf("expected error mentioning 'Hashable', got: %s", combinedMap)
	}
}

func TestVectorRemoved(t *testing.T) {
	root := projectRoot(t)

	// 1. Vector[int] is unknown type
	srcVec := `
bind main = () {
    let v = Vector[int].new()
}
`
	var stdoutVec, stderrVec bytes.Buffer
	rcVec, errVec := driver.Run(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "vec_removed.ll"),
		SrcOverride: srcVec,
		ProjectRoot: root,
		Release:     false,
	}, nil, &stdoutVec, &stderrVec)
	if errVec == nil && rcVec == 0 {
		t.Fatalf("expected compile error when using Vector (should be removed), got success")
	}
	combinedVec := stderrVec.String()
	if errVec != nil {
		combinedVec += errVec.Error()
	}
	if !strings.Contains(combinedVec, "unknown") && !strings.Contains(combinedVec, "Vector") {
		t.Fatalf("expected unknown type error for Vector, got: %s", combinedVec)
	}

	// 2. @lang("vector") is unknown struct lang item
	srcLangVec := `
@lang("vector")
define MyVec[T] struct [
    data: ptr[T],
]
bind main = () {}
`
	var stdoutLV, stderrLV bytes.Buffer
	rcLV, errLV := driver.Run(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "lang_vec_removed.ll"),
		SrcOverride: srcLangVec,
		ProjectRoot: root,
		Release:     false,
	}, nil, &stdoutLV, &stderrLV)
	if errLV == nil && rcLV == 0 {
		t.Fatalf("expected compile error for @lang(\"vector\"), got success")
	}
	combinedLV := stderrLV.String()
	if errLV != nil {
		combinedLV += errLV.Error()
	}
	if !strings.Contains(combinedLV, "unknown struct lang item") {
		t.Fatalf("expected error for unknown struct lang item, got: %s", combinedLV)
	}
}

func TestFixedArrayAndDynamicList(t *testing.T) {
	src := `
bind sum_fixed = (arr: [4: int]): int {
    let sum = 0
    let i = 0
    for (i < 4) {
        sum += arr[i]
        i += 1
    }
    sum
}

bind sum_list = (lst: List[int]): int {
    let sum = 0
    let i = 0
    for (i < lst.len) {
        sum += lst.get(i) ?? 0
        i += 1
    }
    sum
}

bind main = () {
    // 1. Fixed Array: explicit elements
    let arr1: [4: int] = [4: 1, 2, 3, 4]
    assert(sum_fixed(arr1) == 10)

    // 2. Fixed Array: repeat constructor
    let arr2: [4: int] = [4: 7]
    assert(arr2[0] == 7)
    assert(arr2[1] == 7)
    assert(arr2[2] == 7)
    assert(arr2[3] == 7)
    assert(sum_fixed(arr2) == 28)

    // 3. Dynamic List (List[T] & [T])
    let l1: List[int] = List[int].new()
    l1.push(10)
    l1.push(20)
    l1.push(30)
    assert(l1.len == 3)
    assert(sum_list(l1) == 60)

    // 4. Dynamic List syntax shorthand [int]
    let l2: [int] = [10, 20, 30]
    assert(l2.len() == 3)
    assert(l2[0] == 10)
    assert(l2[1] == 20)
    assert(l2[2] == 30)
}
`
	runInlineStrict(t, "array_and_list", src, false)
}

func TestHashingStackPureLowlight(t *testing.T) {
	src := `
bind main = () {
    // 1. Primitive integer hashes
    let i1 = 42
    let i2 = 42
    let i3 = 43
    assert(i1.hash() == i2.hash())
    assert(i1.hash() != i3.hash())

    // 2. Boolean hashes
    let b1 = true
    let b2 = false
    assert(b1.hash() != b2.hash())
    assert(true.hash() == b1.hash())

    // 3. Char hashes
    let c1 = 'A'
    let c2 = 'B'
    assert(c1.hash() == 'A'.hash())
    assert(c1.hash() != c2.hash())

    // 4. String FNV-1a pure Lowlight hashing
    let s0 = ""
    // 64-bit FNV offset basis: 14695981039346656037
    assert(s0.hash() == 14695981039346656037)
    let s1 = "hello"
    let s2 = "hello"
    let s3 = "world"
    assert(s1.hash() == s2.hash())
    assert(s1.hash() != s3.hash())
    assert(s1.hash() != s0.hash())

    // 5. Float normalization (-0.0 vs +0.0) and distinct floats
    let f1 = 0.0
    let f2 = -0.0
    assert(f1.hash() == f2.hash())
    let f3 = 3.141592653589793
    let f4 = 2.718281828459045
    assert(f3.hash() != f4.hash())

    // 6. Fixed-width integer types
    let u8_a: u8 = 10
    let u8_b: u8 = 10
    assert(u8_a.hash() == u8_b.hash())

    let u64_a: u64 = 999999999
    let u64_b: u64 = 999999999
    assert(u64_a.hash() == u64_b.hash())

    let i32_a: i32 = -500
    let i32_b: i32 = -500
    assert(i32_a.hash() == i32_b.hash())
}
`
	runInlineStrict(t, "hashing_stack_pure_lowlight", src, false)
}

func TestHashOfIntrinsicRetired(t *testing.T) {
	root := projectRoot(t)

	// Attempting to call retired intrinsic hash_of(x)
	src := `
bind main = () {
    let x = 42
    let h = hash_of(x)
}
`
	var stdout, stderr bytes.Buffer
	rc, err := driver.Run(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "hash_of_retired.ll"),
		SrcOverride: src,
		ProjectRoot: root,
		Release:     false,
	}, nil, &stdout, &stderr)
	if err == nil && rc == 0 {
		t.Fatalf("expected compile error when calling retired 'hash_of(...)', got success")
	}
	combined := stderr.String()
	if err != nil {
		combined += err.Error()
	}
	if !strings.Contains(combined, "hash_of") {
		t.Fatalf("expected error mentioning 'hash_of', got: %s", combined)
	}
}
