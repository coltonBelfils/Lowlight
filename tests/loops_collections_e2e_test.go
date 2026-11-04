package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// =============================================================================
// 1. Stack Array Literal Tests: repeat constructor, explicit elements, single-elem, mismatch error
// =============================================================================

func TestStackArrayLiteralsPositive(t *testing.T) {
	src := `
bind main = () {
    // 1. Repeat constructor: [4: 0] -> [0, 0, 0, 0]
    let a1: [4: int] = [4: 0]
    assert(a1.len() == 4)
    assert(a1[0] == 0)
    assert(a1[1] == 0)
    assert(a1[2] == 0)
    assert(a1[3] == 0)

    // 2. Explicit elements: [4: 1, 2, 3, 4] -> [1, 2, 3, 4]
    let a2 = [4: 1, 2, 3, 4]
    assert(a2.len() == 4)
    assert(a2[0] == 1)
    assert(a2[1] == 2)
    assert(a2[2] == 3)
    assert(a2[3] == 4)

    // 3. Single-element array: [1: 99]
    let a3 = [1: 99]
    assert(a3.len() == 1)
    assert(a3[0] == 99)

    // 4. Repeat with non-zero scalar
    let a4 = [3: 7]
    assert(a4.len() == 3)
    assert(a4[0] == 7)
    assert(a4[1] == 7)
    assert(a4[2] == 7)
}
`
	for _, release := range []bool{false, true} {
		runInlineStrict(t, "stack_array_literals_positive", src, release)
	}
}

func TestStackArrayLiteralsMismatchNegative(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "stack_array_too_few_elements",
			src: `
bind main = () {
    let arr = [4: 1, 2]
}
`,
			wantErrPart: "expects 4 element(s), got 2",
		},
		{
			name: "stack_array_too_many_elements",
			src: `
bind main = () {
    let arr = [2: 1, 2, 3]
}
`,
			wantErrPart: "expects 2 element(s), got 3",
		},
	}

	root := projectRoot(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := driver.Check(driver.Options{
				EntryPath:   filepath.Join(root, tc.name+".ll"),
				SrcOverride: tc.src,
				ProjectRoot: root,
			})
			if err == nil {
				t.Fatalf("expected compile error containing %q, but check succeeded", tc.wantErrPart)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErrPart)) {
				t.Fatalf("expected error containing %q, got:\n%s", tc.wantErrPart, err.Error())
			}
		})
	}
}

// =============================================================================
// 2. While Loop Tests: scalar evaluation, optional/non-optional, while <- early exit
// =============================================================================

func TestWhileLoopPositive(t *testing.T) {
	src := `
bind main = () {
    // 1. while(true) with break produces ?int (scalar optional)
    let w1? = {
        let i = 0
        while (true) {
            i += 1
            if (i == 5) {
                while <- i
            }
        }
    }
    assert(w1? == 5)

    // 2. while(true): int with break produces int (scalar non-optional)
    let w2: int = {
        let count = 0
        while (true): int {
            count += 1
            if (count == 42) {
                while <- count
            }
            count
        }
    }
    assert(w2 == 42)

    // 3. User example: while(x in 1..10): bool {if(x == 5) while <- true} else false
    let found = while (x in 1..10): bool {
        if (x == 5) {
            while <- true
        }
    } else false
    assert(found == true)

    let not_found = while (x in 1..10): bool {
        if (x == 99) {
            while <- true
        }
    } else false
    assert(not_found == false)

    // 4. while without else produces ?T
    let search? = while (x in 0..5) {
        if (x == 3) {
            while <- x * 10
        }
    }
    assert(search? == 30)

    let miss? = while (x in 0..5) {
        if (x == 99) {
            while <- x
        }
    }
    assert(miss? == none)

    // 5. while for side-effects (void context)
    let sum = 0
    let k = 0
    while (k < 5) {
        sum += k
        k += 1
    }
    assert(sum == 10)
}
`
	for _, release := range []bool{false, true} {
		runInlineStrict(t, "while_loop_positive", src, release)
	}
}

// =============================================================================
// 3. For Loop Tests: Collectable comprehensions, shorthand : [], default vector
// =============================================================================

func TestForLoopComprehensionsPositive(t *testing.T) {
	src := `
bind main = () {
    // 1. Default dynamic vector collection: for(x in 1..=4) x
    let v1 = for (x in 1..=4) x
    assert(v1.len() == 4)
    assert(v1[0] == 1)
    assert(v1[1] == 2)
    assert(v1[2] == 3)
    assert(v1[3] == 4)

    // 2. Explicit shorthand: for(x in 0..10): [] x
    let v2 = for (x in 0..10): [] x
    assert(v2.len() == 10)
    for (i in 0..10) {
        assert(v2[i] == i)
    }

    // 3. Filtering in comprehension via ` + "`next <-`" + `!
    let evens = for (x in 0..10): [] {
        if (x % 2 != 0) {
            next <-
        }
        x
    }
    assert(evens.len() == 5)
    assert(evens[0] == 0)
    assert(evens[1] == 2)
    assert(evens[2] == 4)
    assert(evens[3] == 6)
    assert(evens[4] == 8)

    // 4. Early break in comprehension via ` + "`for <-`" + `!
    let first_three = for (x in 1..100) {
        if (x > 3) {
            for <-
        }
        x
    }
    assert(first_three.len() == 3)
    assert(first_three[0] == 1)
    assert(first_three[1] == 2)
    assert(first_three[2] == 3)

    // 5. Collecting into a Set #[int]
    let s = for (x in 0..5): #[int] (x % 3)
    assert(s.len() == 3)
    assert(s.contains(0))
    assert(s.contains(1))
    assert(s.contains(2))

    // 6. Side-effect for loop (void context) doesn't allocate
    let side_sum = 0
    for (x in 0..5) {
        side_sum += x
    }
    assert(side_sum == 10)
}
`
	for _, release := range []bool{false, true} {
		runInlineStrict(t, "for_comprehensions_positive", src, release)
	}
}

func TestForLoopCollectableNegative(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "for_annotated_with_non_collectable_int",
			src: `
bind main = () {
    let x = for (true): int 1
}
`,
			wantErrPart: "does not implement Collectable",
		},
		{
			name: "for_annotated_with_string_not_collectable",
			src: `
bind main = () {
    let x = for (x in 0..3): string "a"
}
`,
			wantErrPart: "does not implement Collectable",
		},
	}

	root := projectRoot(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := driver.Check(driver.Options{
				EntryPath:   filepath.Join(root, tc.name+".ll"),
				SrcOverride: tc.src,
				ProjectRoot: root,
			})
			if err == nil {
				t.Fatalf("expected compile error containing %q, but check succeeded", tc.wantErrPart)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErrPart)) {
				t.Fatalf("expected error containing %q, got:\n%s", tc.wantErrPart, err.Error())
			}
		})
	}
}

// =============================================================================
// 4. Map Iteration (MapEntry) and Collection via push(MapEntry)
// =============================================================================

func TestMapEntryIterationAndCollection(t *testing.T) {
	src := `
bind main = () {
    let m = #["alpha": 1, "beta": 2, "gamma": 3]

    // 1. Single-variable iteration binds entry as MapEntry[string, int]
    let key_sum = ""
    let val_sum = 0
    for (entry in m) {
        key_sum += entry.key
        val_sum += entry.value
    }
    assert(val_sum == 6)
    assert(key_sum.contains("alpha"))
    assert(key_sum.contains("beta"))
    assert(key_sum.contains("gamma"))

    // 2. Traditional two-variable iteration still works as before
    let two_val_sum = 0
    for (k, v in m) {
        two_val_sum += v
    }
    assert(two_val_sum == 6)

    // 3. Map collecting via MapEntry in for comprehension
    let doubled_map = for (entry in m): #[string: int] {
        MapEntry[string, int][key: entry.key.copy(), value: entry.value * 10]
    }
    assert(doubled_map.len() == 3)
    let val_alpha? = doubled_map["alpha"]
    let val_beta? = doubled_map["beta"]
    let val_gamma? = doubled_map["gamma"]
    assert(val_alpha? == 10)
    assert(val_beta? == 20)
    assert(val_gamma? == 30)

    // 4. Direct push on Map using MapEntry
    let fresh_map = #[ "init": 0 ]
    fresh_map.push(MapEntry[string, int][key: "pushed", value: 99])
    let val_pushed? = fresh_map["pushed"]
    assert(val_pushed? == 99)
}
`
	for _, release := range []bool{false, true} {
		runInlineStrict(t, "map_entry_iteration_collection", src, release)
	}
}

func TestPureLowlightList(t *testing.T) {
	src := `
bind print_anything = (p: Printable): string {
    p.to_string()
}

bind sum_iterable = (it_obj: Iterable[int]): int {
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
    let v = List[int].new()
    v.push(10)
    v.push(20)
    v.push(30)
    assert(v.len == 3)
    let p? = v.pop()
    assert((p? ?? 0) == 30)
    assert(v.len == 2)
    let v2 = v.copy()
    assert(v2.len == 2)
    let v2p? = v2.pop()
    assert((v2p? ?? 0) == 20)
    assert(v.to_string() == "[10, 20]")
    let it = v.iter()
    let it1? = it.next()
    assert((it1? ?? 0) == 10)
    let it2? = it.next()
    assert((it2? ?? 0) == 20)
    let it3? = it.next()
    assert(it3? == none)
    assert(print_anything(v) == "[10, 20]")
    assert(sum_iterable(v) == 30)

    let collected: List[int] = for (x in 1..=4): List[int] x * 10
    assert(collected.len == 4)
    let c1? = collected.pop()
    assert((c1? ?? 0) == 40)
    let c2? = collected.pop()
    assert((c2? ?? 0) == 30)
}
`
	runInlineStrict(t, "pure_lowlight_list", src, false)
}

func TestPtrFieldAccess(t *testing.T) {
	src := `
define Node struct [
    id: int,
    val: string,
]

bind main = () {
    let p = unsafe alloc_raw[Node](1)
    unsafe {
        ptr_write(p, Node[id: 42, val: "hello"])
        assert(p[0].id == 42)
        p[0].id = 100
        assert(p[0].id == 100)
        assert(p[0].val == "hello")
        let fortytwo = 42
        assert(fortytwo.hash() != 0)
        let hello = "hello"
        assert(hello.hash() != 0)
        assert(p[0].val == "hello")
        free_raw(p)
    }
}
`
	runInlineStrict(t, "ptr_field_access", src, false)
}

func TestPureLowlightSet(t *testing.T) {
	src := `
bind main = () {
    let s = Set[int].new()
    assert(s.len == 0)
    assert(s.is_empty())
    assert(s.insert(10))
    assert(s.insert(20))
    assert(s.insert(30))
    assert(s.len == 3)
    assert(not s.insert(20))
    assert(s.len == 3)
    assert(s.has(10))
    assert(s.has(20))
    assert(s.has(30))
    assert(not s.has(40))

    assert(s.remove(20))
    assert(s.len == 2)
    assert(not s.has(20))
    assert(s.has(10))
    assert(s.has(30))
}
`
	runInlineStrict(t, "pure_lowlight_set", src, false)
}

func TestPureLowlightMap(t *testing.T) {
	src := `
bind main = () {
    let m = Map[string, int].new()
    assert(m.len == 0)
    assert(m.is_empty())
    m.insert("Alice", 100)
    m.insert("Bob", 200)
    m.insert("Charlie", 300)
    assert(m.len == 3)
    assert(m.has("Alice"))
    assert(m.has("Bob"))
    assert(not m.has("Dave"))
    let v_alice? = m.get("Alice")
    assert((v_alice? ?? 0) == 100)
    let v_bob? = m.get("Bob")
    assert((v_bob? ?? 0) == 200)

    // Overwrite existing key
    m.insert("Alice", 999)
    assert(m.len == 3)
    let v_alice2? = m.get("Alice")
    assert((v_alice2? ?? 0) == 999)

    // Remove key
    let rem_bob? = m.remove("Bob")
    assert((rem_bob? ?? 0) == 200)
    assert(m.len == 2)
    assert(not m.has("Bob"))
}
`
	runInlineStrict(t, "pure_lowlight_map", src, false)
}
