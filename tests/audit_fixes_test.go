package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// 1. Bug 1 & Bug 2: Top-level constants order resolution & cycle detection
func TestTopLevelConstantsOrderAndCycles(t *testing.T) {
	// Out-of-order forward references must initialize correctly in C
	srcOk := `
bind C = B + 1
bind B = A * 2
bind A = 10

bind main = () {
    assert(A == 10)
    assert(B == 20)
    assert(C == 21)
}
`
	runInlineStrict(t, "top_level_out_of_order", srcOk, false)

	// Cyclic dependencies must produce clean compile-time diagnostic error and NEVER crash/segfault
	srcCycle := `
bind A = B + 1
bind B = A + 1

bind main = () {
    println(A)
}
`
	root := projectRoot(t)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(root, "test_cycle.ll"),
		SrcOverride: srcCycle,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected cycle error, but check succeeded")
	}
	if !strings.Contains(err.Error(), "cyclic dependency") {
		t.Fatalf("expected error mentioning cyclic dependency, got: %v", err)
	}
}

// 2. Bug 3: Vector containing non-copyable resource types
func TestVectorNonCopyableResource(t *testing.T) {
	src := `
define NonCopyable struct [
    id: int,
]

bind NonCopyable.drop = (&self) {
    self.id = 0
}

bind main = () {
    let v: [NonCopyable] = []
    // Push 10 elements to trigger dynamic reallocations in ll_vec_reserve
    for (i in 0..10) {
        v.push(NonCopyable[id: i])
    }
    assert(v.len() == 10)
    assert(v[0].id == 0)
    assert(v[9].id == 9)
}
`
	runInlineStrict(t, "vec_non_copyable", src, false)
}

// 3. Bug 4: INT64_MIN arithmetic wrap in std/strconv.ll and std/strfmt.ll
func TestInt64MinFormatting(t *testing.T) {
	src := `
import "std/strconv.ll" as strconv
import "std/strfmt.ll" as strfmt

bind main = () {
    let min_i64 = -9223372036854775807 - 1

    // to_radix in base 10
    let s10 = min_i64.to_radix(10)
    assert(s10 == "-9223372036854775808")

    // to_hex
    let shex = min_i64.to_hex(false)
    assert(shex == "-8000000000000000")

    // to_bin
    let sbin = min_i64.to_bin()
    assert(sbin.starts_with("-1000000000000000000000000000000000000000000000000000000000000000"))

    // format_commas
    let scommas = min_i64.format_commas()
    assert(scommas == "-9,223,372,036,854,775,808")

    // parse_int_safe for INT64_MIN
    let parsed? = strconv.parse_int_safe("-9223372036854775808")
    assert(parsed? != none)
    assert((parsed? ?? 0) == min_i64)
}
`
	runInlineStrict(t, "int64_min_formatting", src, false)
}

// 4. Security Hole 6: Integer overflow check in std/strconv.ll
func TestParseIntOverflow(t *testing.T) {
	src := `
import "std/strconv.ll" as strconv

bind main = () {
    // Exact bounds
    let max_ok? = strconv.parse_int_safe("9223372036854775807")
    assert(max_ok? != none)
    assert((max_ok? ?? 0) == 9223372036854775807)

    let min_ok? = strconv.parse_int_safe("-9223372036854775808")
    assert(min_ok? != none)

    // Overflows must return none
    let over_pos? = strconv.parse_int_safe("9223372036854775808")
    assert(over_pos? == none)

    let over_pos2? = strconv.parse_int_safe("99999999999999999999")
    assert(over_pos2? == none)

    let over_neg? = strconv.parse_int_safe("-9223372036854775809")
    assert(over_neg? == none)

    let over_neg2? = strconv.parse_int_safe("-99999999999999999999")
    assert(over_neg2? == none)
}
`
	runInlineStrict(t, "parse_int_overflow", src, false)
}

// 5. Security Hole 1: Arena escape analysis recursion
func TestArenaEscapeAnalysisRecursion(t *testing.T) {
	root := projectRoot(t)

	// Escape via struct field member expression
	srcMember := `
define Node struct [ val: int ]
define Holder struct [ node: ref[Node] ]

bind main = () {
    let outer_ref: ?ref[Node] = none
    {
        let a = Arena.new()
        let n = a.alloc(Node[val: 42])
        let h = Holder[node: n]
        outer_ref = h.node
    }
}
`
	_, _, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(root, "arena_member_escape.ll"),
		SrcOverride: srcMember,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected arena escape error on struct field, but check succeeded")
	}
	if !strings.Contains(err.Error(), "cannot escape") {
		t.Fatalf("expected error to mention 'cannot escape', got: %v", err)
	}

	// Escape via if-expression
	srcIf := `
define Node struct [ val: int ]

bind main = () {
    let outer_ref: ?ref[Node] = none
    let a1 = Arena.new()
    let n1 = a1.alloc(Node[val: 1])
    {
        let a2 = Arena.new()
        let n2 = a2.alloc(Node[val: 2])
        outer_ref = if (true) n2 else n1
    }
}
`
	_, _, err2 := driver.Check(driver.Options{
		EntryPath:   filepath.Join(root, "arena_if_escape.ll"),
		SrcOverride: srcIf,
		ProjectRoot: root,
	})
	if err2 == nil {
		t.Fatal("expected arena escape error on if-expr, but check succeeded")
	}
	if !strings.Contains(err2.Error(), "cannot escape") {
		t.Fatalf("expected error to mention 'cannot escape', got: %v", err2)
	}
}

// 6. Security Hole 3: Unbounded Generic Monomorphization Limit
func TestGenericRecursionLimit(t *testing.T) {
	src := `
bind recurse[T] = () {
    recurse[[T]]()
}

bind main = () {
    recurse[int]()
}
`
	root := projectRoot(t)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(root, "generic_bomb.ll"),
		SrcOverride: src,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected generic recursion depth error, but check succeeded")
	}
	if !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("expected error mentioning depth limit, got: %v", err)
	}
}

// 7. Security Hole 4: Tombstone memory compaction in Maps and Sets
func TestMapAndSetTombstoneCompaction(t *testing.T) {
	src := `
bind main = () {
    let m = #[ "key": 0 ]
    m.clear()

    // Repeated inserts and removes to generate tombstones
    for (i in 0..100) {
        m.insert("k$i", i)
        m.remove("k$i")
    }
    assert(m.len() == 0)

    // Insert 5 active keys
    for (i in 0..5) {
        m.insert("active$i", i)
    }
    assert(m.len() == 5)
    for (i in 0..5) {
        let v?: ?int = m["active$i"]
        assert(v? != none)
        assert((v? ?? 0) == i)
    }

    let s = #[1]
    s.clear()
    for (i in 0..100) {
        s.insert(i)
        s.remove(i)
    }
    assert(s.len() == 0)
    for (i in 0..5) {
        s.insert(i * 10)
    }
    assert(s.len() == 5)
    for (i in 0..5) {
        assert(s.has(i * 10))
    }
}
`
	runInlineStrict(t, "map_set_compaction", src, false)
}

// 8. Security Hole 5: POSIX mkdir_p and remove_all
func TestPosixDirectoryOperations(t *testing.T) {
	src := `
import "std/os.ll" as os

bind main = () {
    let test_dir = "/tmp/ll_test_mkdir_p/nested/dir/level3"
    let ok = os.mkdir_p(test_dir)
    assert(ok)
    assert(os.file_exists(test_dir))

    let rm_ok = os.remove_all("/tmp/ll_test_mkdir_p")
    assert(rm_ok)
    assert(not os.file_exists("/tmp/ll_test_mkdir_p"))
}
`
	runInlineStrict(t, "posix_dir_ops", src, false)
}

// 9. Security Hole 7: Raw pointer assignment without dropping uninitialized memory
func TestRawPointerAssignmentNoPriorDrop(t *testing.T) {
	src := `
define Res struct [
    name: string,
]

bind Res.drop = (&self) {
    self.name = ""
}

bind main = () {
    unsafe {
        let p = alloc_raw[Res](1)
        // Assigning to raw uninitialized memory *p must NOT attempt to drop uninitialized *p!
        *p = Res[name: "hello from raw memory"]
        assert((*p).name == "hello from raw memory")
        // Manual cleanup
        free_raw(p)
    }
}
`
	runInlineStrict(t, "raw_ptr_assign_no_drop", src, false)
}

// 10. Item 6: Result[T, E] in prelude without import
func TestResultInPrelude(t *testing.T) {
	src := `
bind divide = (a: int, b: int): Result[int, string] {
    if (b == 0) {
        Result[int, string].fail("division by zero")
    } else {
        Result[int, string].ok(a / b)
    }
}

bind main = () {
    let ok_res = divide(10, 2)
    assert(ok_res.is_ok())
    assert(not ok_res.is_err())
    assert(ok_res.unwrap() == 5)

    let err_res = divide(10, 0)
    assert(err_res.is_err())
    assert(err_res.unwrap_or(42) == 42)
}
`
	runInlineStrict(t, "result_in_prelude", src, false)
}

// 11. Item 7: Attributes (@inline, @noinline, @must_use, @nodiscard, @deprecated, @packed, @doc)
func TestAttributesSupport(t *testing.T) {
	src := `
@doc("2D packed coordinate")
@packed
define Coord struct [
    x: i32,
    y: i32,
]

@must_use
bind compute_val = (x: int): int {
    x * 2
}

@inline
bind fast_add = (a: int, b: int): int {
    a + b
}

@noinline
bind slow_add = (a: int, b: int): int {
    a + b
}

@deprecated("use compute_val instead")
bind old_calc = (x: int): int {
    x * 2
}

bind main = () {
    let c = Coord[x: i32(10), y: i32(20)]
    assert(int(c.x) == 10)
    assert(int(c.y) == 20)

    let v1 = fast_add(5, 10)
    assert(v1 == 15)

    let v2 = slow_add(5, 10)
    assert(v2 == 15)

    let v3 = compute_val(21)
    assert(v3 == 42)
}
`
	runInlineStrict(t, "attributes_support", src, false)

	// Conflicting @inline and @noinline must fail check
	srcConflict := `
@inline
@noinline
bind bad_fn = () {}
bind main = () {}
`
	root := projectRoot(t)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(root, "attr_conflict.ll"),
		SrcOverride: srcConflict,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected conflict error for @inline and @noinline, but check succeeded")
	}
	if !strings.Contains(err.Error(), "both @inline and @noinline") {
		t.Fatalf("expected error mentioning both @inline and @noinline, got: %v", err)
	}
}

// 12. Item 8: Drop interface return signature drop: (&self)
func TestDropSignatureVerification(t *testing.T) {
	src := `
define CustomFile struct [
    fd: int,
]

bind CustomFile.drop = (&self) {
    self.fd = -1
}

bind test_drop[T: Drop] = (item: own T) {
    // item is dropped at the end of this scope
}

bind main = () {
    let f = CustomFile[fd: 10]
    test_drop[CustomFile](own f)
}
`
	runInlineStrict(t, "drop_signature_check", src, false)
}

// 13. Item 9: string.push_bytes(ptr[u8], int)
func TestStringPushBytes(t *testing.T) {
	src := `
bind main = () {
    let s = "Hello"
    unsafe {
        let raw = alloc_raw[u8](7)
        *ptr_add(raw, 0) = u8(44)  // ','
        *ptr_add(raw, 1) = u8(32)  // ' '
        *ptr_add(raw, 2) = u8(87)  // 'W'
        *ptr_add(raw, 3) = u8(111) // 'o'
        *ptr_add(raw, 4) = u8(114) // 'r'
        *ptr_add(raw, 5) = u8(108) // 'l'
        *ptr_add(raw, 6) = u8(100) // 'd'
        s.push_bytes(raw, 7)
        free_raw(raw)
    }
    assert(s == "Hello, World")
    assert(s.len() == 12)
}
`
	runInlineStrict(t, "string_push_bytes", src, false)
}

// 12. Top-level complex constants (e.g., vector literals) requiring temporary local slots
func TestTopLevelComplexConstants(t *testing.T) {
	src := `
bind l = [1, 2, 3, 4, 5, 6]

bind main = () {
    let ll? = while (i in l) i + 1
    if (ll?) {
        assert(ll == 7)
    }
}
`
	runInlineStrict(t, "top_level_vector_constant", src, false)
}
