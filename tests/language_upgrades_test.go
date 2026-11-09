package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// =============================================================================
// 1. Tagged Unions (Option A): End-to-End Positive & Negative Tests
// =============================================================================

func TestTaggedUnionsEndToEnd(t *testing.T) {
	src := `
// Plain C enum still works and casts to int
define Color enum [Red, Green, Blue]

// Generic tagged union with unit, single-payload, and multi-field variants
@must_use
define Outcome[T, E] enum [
    Empty,
    Ok: T,
    Err: E,
    Detail: [code: int, msg: E],
]

bind Outcome[T, E].is_ok = (self): bool -> {
    match (self)
        when Outcome.Ok(_): true
        else: false
}

// Recursive AST enum using ref[Expr] (Arena) and [Expr] (Vector)
define Expr enum [
    Null,
    Num: int,
    Neg: ref[Expr],
    Add: [left: ref[Expr], right: ref[Expr]],
    Seq: [Expr],
]

// Match directly on ref[Expr] scrutinee!
bind eval = (e: ref[Expr]): int -> {
    match (e)
        when Expr.Null: 0
        when Expr.Num(n): n
        when Expr.Neg(inner): -eval(inner)
        when Expr.Add[left, right]: eval(left) + eval(right)
        when Expr.Seq(items): {
            let sum = 0
            for (item in items) {
                let tmp = match (item)
                    when Expr.Num(v): v
                    else: 0
                sum += tmp
            }
            sum
        }
}

// Mutate payload in place directly through ref[Expr] using & binding!
bind scale_leaf = (e: ref[Expr], factor: int) -> {
    match (e)
        when Expr.Num(&n): {
            n = n * factor
        }
        when Expr.Add[left, right]: {
            scale_leaf(left, factor)
            scale_leaf(right, factor)
        }
        when Expr.Neg(inner): {
            scale_leaf(inner, factor)
        }
        else: {}
}

// Multi-field resource enum to verify partial own-move + automatic drop of unmoved fields
define Event enum [
    Logout,
    Login: [user: string, token: string, secret: string],
    Packet: [count: int, label: string],
]

bind make_event = (): Event -> {
    Event.Login[
        user: "alice_" + "admin",
        token: "tok_" + "12345",
        secret: "sec_" + "99999",
    ]
}

bind main = () -> {
    // 1. Plain C enum compatibility
    let c = Color.Green
    assert(int(c) == 1)

    // 2. Generic tagged union construction (explicit & inferred)
    let o1 = Outcome[int, string].Ok(42)
    let o2: Outcome[int, string] = Outcome.Empty
    let o3: Outcome[int, string] = Outcome.Err("bad_" + "input")
    let o4: Outcome[int, string] = Outcome.Detail[code: 404, msg: "not_" + "found"]

    assert(o1.is_ok())
    assert(not o2.is_ok())
    assert(not o3.is_ok())
    assert(not o4.is_ok())

    // 3. Auto-derived ==, !=, .copy(), and .to_string()
    let o4_copy = o4.copy()
    assert(o4 == o4_copy)
    assert(o1 != o4)
    assert(o2.to_string() == "Outcome.Empty")
    assert(o1.to_string() == "Outcome.Ok(42)")
    assert(o4.to_string() == "Outcome.Detail[code: 404, msg: not_found]")

    // Exhaustive ` + "`else Variant[...]:`" + ` final arm with destructuring
    let summary = match (o4_copy)
        when Outcome.Empty: "empty"
        when Outcome.Ok(v): "ok:$v"
        when Outcome.Err(e): "err:$e"
        else Outcome.Detail[code, msg]: "$code:$msg"
    assert(summary == "404:not_found")

    // 4. Mutable & destructuring on local enum variable
    let pkt = Event.Packet[count: 10, label: "hello"]
    match (pkt)
        when Event.Packet[&count, &label]: {
            count += 5
            label += "_world"
        }
        else: {}
    let pkt_check = match (pkt)
        when Event.Packet[count, label: lbl]: "$count:$lbl"
        else: "bad"
    assert(pkt_check == "15:hello_world")

    // 5. Full & partial own destructuring (from local variable AND rvalue!)
    //    token is matched with wildcard _, secret is omitted:
    //    both heap strings MUST be dropped cleanly without leak or double-free!
    let ev1 = make_event()
    let extracted_user1 = match (ev1)
        when Event.Logout: "none"
        when Event.Packet[_]: "pkt"
        when Event.Login[own user, token: _]: user
    assert(extracted_user1 == "alice_admin")

    // Rvalue scrutinee partial own-move
    let extracted_user2 = match (make_event())
        when Event.Login[own user]: user
        else: "other"
    assert(extracted_user2 == "alice_admin")

    // 6. Recursive AST evaluation & in-place mutation inside scoped Arena block
    let arena = Arena.new()
    let total_scaled = {
        let n10 = arena.alloc(Expr.Num(10))
        let n20 = arena.alloc(Expr.Num(20))
        let neg5 = arena.alloc(Expr.Neg(arena.alloc(Expr.Num(5))))
        let sum_ab = arena.alloc(Expr.Add[left: n10, right: n20])
        let root = arena.alloc(Expr.Add[left: sum_ab, right: neg5])
        assert(eval(root) == 25)
        scale_leaf(root, 3)
        eval(root)
    }
    assert(total_scaled == 75)
    arena.reset()

    let seq_expr = Expr.Seq([Expr.Num(3), Expr.Num(4), Expr.Null, Expr.Num(5)])
    let seq_arena = Arena.new()
    {
        let seq_ref = seq_arena.alloc(own seq_expr)
        assert(eval(seq_ref) == 12)
    }
    seq_arena.reset()
}
`
	for _, release := range []bool{false, true} {
		runInlineStrict(t, "tagged_unions_e2e", src, release)
	}
}

func TestTaggedUnionsMustUseWarning(t *testing.T) {
	src := `
@must_use
define Status enum [
    Ok,
    Err: string,
]

bind check_it = (): Status -> {
    Status.Err("problem")
}

bind main = () -> {
    check_it()
}
`
	root := projectRoot(t)
	_, warnings, err := driver.Check(driver.Options{
		EntryPath:   filepath.Join(root, "tagged_must_use.ll"),
		SrcOverride: src,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected error on @must_use warning test: %v", err)
	}
	if !strings.Contains(warnings, "@must_use") {
		t.Fatalf("expected @must_use warning for discarded Status enum, got warnings: %q", warnings)
	}
}

func TestTaggedUnionsNegative(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "non_exhaustive_tagged_match",
			src: `
define Res enum [Ok: int, Err: string, Pending]
bind main = () -> {
    let r = Res.Ok(1)
    let x = match (r)
        when Res.Ok(v): v
        when Res.Err(_): 0
    println(x)
}
`,
			wantErrPart: "exhaustive",
		},
		{
			name: "direct_unboxed_recursive_enum",
			src: `
define Bad enum [
    End,
    Loop: Bad,
]
bind main = () -> {}
`,
			wantErrPart: "recursive",
		},
		{
			name: "mutual_unboxed_recursive_enum_struct",
			src: `
define A enum [
    Done,
    More: B,
]
define B struct [
    inner: A,
]
bind main = () -> {}
`,
			wantErrPart: "recursive",
		},
		{
			name: "int_cast_on_tagged_enum_rejected",
			src: `
define Msg enum [Ping, Text: string]
bind main = () -> {
    let m = Msg.Ping
    let x = int(m)
    println(x)
}
`,
			wantErrPart: "cannot convert",
		},
		{
			name: "tagged_enum_not_hashable_in_set",
			src: `
define Msg enum [Ping, Code: int]
bind main = () -> {
    let s = #[Msg.Ping]
    println(s.len())
}
`,
			wantErrPart: "hashable",
		},
		{
			name: "use_after_own_move_in_match",
			src: `
define BoxedStr enum [Empty, Val: string]
bind main = () -> {
    let b = BoxedStr.Val("hello")
    let s = match (b)
        when BoxedStr.Empty: ""
        when BoxedStr.Val(own text): text
    println(s)
    println(b)
}
`,
			wantErrPart: "moved",
		},
		{
			name: "own_move_from_borrowed_enum_rejected",
			src: `
define BoxedStr enum [Empty, Val: string]
bind inspect = (b: BoxedStr): string -> {
    match (b)
        when BoxedStr.Empty: ""
        when BoxedStr.Val(own text): text
}
bind main = () -> {}
`,
			wantErrPart: "cannot move",
		},
		{
			name: "own_move_from_arena_ref_enum_rejected",
			src: `
define BoxedStr enum [Empty, Val: string]
bind inspect = (b: ref[BoxedStr]): string -> {
    match (b)
        when BoxedStr.Empty: ""
        when BoxedStr.Val(own text): text
}
bind main = () -> {}
`,
			wantErrPart: "cannot move",
		},
		{
			name: "mix_mut_and_own_in_single_variant_pattern_rejected",
			src: `
define PairMsg enum [Both: [a: string, b: string]]
bind main = () -> {
    let p = PairMsg.Both[a: "x", b: "y"]
    match (p)
        when PairMsg.Both[&a, own b]: {
            a.push_str(b)
        }
}
`,
			wantErrPart: "cannot mix",
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
// 2. Arena Pinning (Borrow-Locking) & Escape Hole Fixes
// =============================================================================

func TestArenaScopedPinningPositive(t *testing.T) {
	src := `
define Node struct [
    id: int,
    name: string,
    edges: [ref[Node]],
]

bind alloc_helper = (a: &Arena, id: int, name: string): ref[Node] -> {
    a.alloc(Node[id: id, name: name.copy(), edges: []])
}

bind main = () -> {
    let arena = Arena.new()

    // Phase 1: allocate and wire nodes inside a block, extract Plain/String summary
    let phase1_summary = {
        let n1 = alloc_helper(&arena, 1, "alpha")
        let n2 = arena.alloc(Node[id: 2, name: "beta", edges: []])
        n1.edges.push(n2)
        "${n1.name}->${n1.edges[0].name}:${n1.edges[0].id}"
    }
    assert(phase1_summary == "alpha->beta:2")

    // Block exited -> arena is unpinned! reset(), reassignment, and own-move are legal again!
    arena.reset()

    // Phase 2: reuse reset arena in another block
    {
        let n3 = arena.alloc(Node[id: 3, name: "gamma", edges: []])
        assert(n3.id == 3)
        assert(n3.name == "gamma")
    }
    arena.reset()

    // Reassign and own-move when unpinned
    arena = Arena.new()
    let consume = (a: own Arena) -> {}
    consume(own arena)
}
`
	for _, release := range []bool{false, true} {
		runInlineStrict(t, "arena_scoped_pinning_ok", src, release)
	}
}

func TestArenaPinningAndEscapeHolesNegative(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "reset_while_pinned_in_same_scope",
			src: `
define Node struct [val: int]
bind main = () -> {
    let arena = Arena.new()
    let n = arena.alloc(Node[val: 10])
    arena.reset()
    println(n.val)
}
`,
			wantErrPart: "cannot call `.reset()`",
		},
		{
			name: "reassign_while_pinned",
			src: `
define Node struct [val: int]
bind main = () -> {
    let arena = Arena.new()
    let n = arena.alloc(Node[val: 10])
    arena = Arena.new()
    println(n.val)
}
`,
			wantErrPart: "cannot reassign",
		},
		{
			name: "move_own_arena_while_pinned",
			src: `
define Node struct [val: int]
bind drop_arena = (a: own Arena) -> {}
bind main = () -> {
    let arena = Arena.new()
    let n = arena.alloc(Node[val: 10])
    drop_arena(own arena)
    println(n.val)
}
`,
			wantErrPart: "cannot move",
		},
		{
			name: "closure_capture_move_arena_while_pinned",
			src: `
define Node struct [val: int]
bind main = () -> {
    let arena = Arena.new()
    let n = arena.alloc(Node[val: 10])
    let f = (): Arena -> {
        own arena
    }
    println(n.val)
}
`,
			wantErrPart: "cannot move",
		},
		{
			name: "inner_alloc_pushed_to_outer_vec_keeps_arena_pinned",
			src: `
define Node struct [val: int]
bind main = () -> {
    let arena = Arena.new()
    let keep: [ref[Node]] = []
    {
        let r = arena.alloc(Node[val: 99])
        keep.push(r)
    }
    // keep is still alive in outer scope, so arena MUST remain pinned here!
    arena.reset()
    println(keep[0].val)
}
`,
			wantErrPart: "cannot call `.reset()`",
		},
		{
			name: "borrowed_mut_arena_param_reset_forbidden",
			src: `
bind wipe = (a: &Arena) -> {
    a.reset()
}
bind main = () -> {}
`,
			wantErrPart: "cannot call `.reset()` on borrowed",
		},
		{
			name: "borrowed_mut_arena_param_reassign_forbidden",
			src: `
bind swap_out = (a: &Arena) -> {
    a = Arena.new()
}
bind main = () -> {}
`,
			wantErrPart: "cannot reassign borrowed `&Arena`",
		},
		{
			name: "unwind_escape_from_block_with_local_arena",
			src: `
define Node struct [val: int]
bind main = () -> {
    let escaped = {
        let a = Arena.new()
        let n = a.alloc(Node[val: 7])
        <- n
    }
    println(escaped.val)
}
`,
			wantErrPart: "cannot escape",
		},
		{
			name: "return_local_arena_ref_even_when_unrelated_arena_param_exists",
			src: `
define Node struct [val: int]
bind sneaky = (caller_arena: &Arena): ref[Node] -> {
    let local_arena = Arena.new()
    local_arena.alloc(Node[val: 42])
}
bind main = () -> {}
`,
			wantErrPart: "cannot escape",
		},
		{
			name: "closure_capturing_inner_arena_ref_escaping_block",
			src: `
define Node struct [val: int]
bind main = () -> {
    let reader = {
        let a = Arena.new()
        let n = a.alloc(Node[val: 123])
        (): int -> { n.val }
    }
    println(reader())
}
`,
			wantErrPart: "cannot escape",
		},
		{
			name: "map_index_assign_inner_arena_escape",
			src: `
define Node struct [val: int]
bind main = () -> {
    let a1 = Arena.new()
    let m = #["root": a1.alloc(Node[val: 1])]
    {
        let a2 = Arena.new()
        m["inner"] = a2.alloc(Node[val: 2])
    }
}
`,
			wantErrPart: "cannot escape",
		},
		{
			name: "mutating_method_on_outer_ref_field_inner_arena_escape",
			src: `
define Node struct [val: int, edges: [ref[Node]]]
bind main = () -> {
    let a1 = Arena.new()
    let root = a1.alloc(Node[val: 1, edges: []])
    {
        let a2 = Arena.new()
        let child = a2.alloc(Node[val: 2, edges: []])
        root.edges.push(child)
    }
}
`,
			wantErrPart: "cannot escape",
		},
		{
			name: "helper_call_mixing_outer_and_inner_arena_refs",
			src: `
define Node struct [val: int, edges: [ref[Node]]]
bind connect = (parent: ref[Node], child: ref[Node]) -> {
    parent.edges.push(child)
}
bind main = () -> {
    let a1 = Arena.new()
    let root = a1.alloc(Node[val: 1, edges: []])
    {
        let a2 = Arena.new()
        let child = a2.alloc(Node[val: 2, edges: []])
        connect(root, child)
    }
}
`,
			wantErrPart: "cannot be passed alongside outer",
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
// 3. Optional Chaining (3A), Contextual `next`, & Non-Optional Generics (3C)
// =============================================================================

func TestOptionalChainingContextualNextAndGenerics(t *testing.T) {
	src := `
define Profile struct [
    nick?: ?string,
    score: int,
]

define Account struct [
    name: string,
    profile?: ?Profile,
]

bind find_account = (id: int): ?Account -> {
    if (id == 1) {
        Account[
            name: "Ada",
            profile?: Profile[nick?: "ada_lovelace", score: 99],
        ]
    } else if (id == 2) {
        Account[
            name: "Bob",
            profile?: Profile[nick?: none, score: 50],
        ]
    } else {
        none
    }
}

// Contextual next as field name, variable name, optional next?, and method .next()!
define LinkNode struct [
    val: int,
    next?: ?ref[LinkNode],
]

define Counter struct [
    cur: int,
]

bind Counter.next = (&self): int -> {
    self.cur += 1
    self.cur
}

// 3C: T is strictly non-optional; ?T accepts both T and ?T!
define MaybeBox[T] struct [
    item?: ?T,
    fallback: T,
]

bind unwrap_or_default[T: Copyable] = (val?: ?T, fallback: T): T -> {
    if (val?) {
        val.copy()
    } else {
        fallback
    }
}

bind main = () -> {
    // 1. Item 3A: . optional chaining without spaces (plus ?. on calls)
    let a1? = find_account(1)
    let a2? = find_account(2)
    let a3? = find_account(3)

    let s1? = a1?.profile?.score
    let s2? = a2?.profile?.score
    let s3? = a3?.profile?.score
    assert((s1? ?? -1) == 99)
    assert((s2? ?? -1) == 50)
    assert(s3? == none)

    // Method call chaining on x?.field and x?.method().method()
    let nlen1? = a1?.profile?.nick?.len()
    let nlen2? = a2?.profile?.nick?.len()
    assert((nlen1? ?? -1) == 12)
    assert(nlen2? == none)

    let raw?: ?string = "  hello world  "
    let trimmed_len? = raw?.trim().len()
    assert((trimmed_len? ?? 0) == 11)

    let empty_raw?: ?string = none
    let empty_len? = empty_raw?.trim().len()
    assert(empty_len? == none)

    let direct_name? = find_account(1)?.name.len()
    assert((direct_name? ?? 0) == 3)

    // 2. Contextual next everywhere + next <- and next outer <- in loops!
    let c = Counter[cur: 0]
    let next = c.next()
    assert(next == 1)
    assert(c.next() == 2)

    let arena = Arena.new()
    {
        let tail = arena.alloc(LinkNode[val: 20, next?: none])
        let head = arena.alloc(LinkNode[val: 10, next?: tail])
        let next? = head.next?
        let tail_val? = next?.val
        assert((tail_val? ?? 0) == 20)
    }
    arena.reset()

    // next <- and labeled next outer <- still work as unwinds!
    let sum = 0
    for (i in 0..6) {
        if (i % 2 == 0) {
            next <-
        }
        sum += i
    }
    assert(sum == 9)

    let grid_sum = 0
    outer: for (r in 0..3) {
        for (col in 0..3) {
            if (col == 1) {
                next outer <-
            }
            grid_sum += 10
        }
    }
    assert(grid_sum == 30)

    // 3. Item 3C: ` + "`?T`" + ` parameter accepts both non-optional ` + "`int`" + ` and optional ` + "`?int`" + `!
    let opt_num?: ?int = 77
    let none_num?: ?int = none
    assert(unwrap_or_default(opt_num?, 10) == 77)
    assert(unwrap_or_default(none_num?, 10) == 10)
    assert(unwrap_or_default(123, 10) == 123)

    let mb1 = MaybeBox[int][item?: 55, fallback: 0]
    let mb2 = MaybeBox[int][item?: none, fallback: 9]
    assert((mb1.item? ?? mb1.fallback) == 55)
    assert((mb2.item? ?? mb2.fallback) == 9)
}
`
	for _, release := range []bool{false, true} {
		runInlineStrict(t, "opt_chain_next_generics", src, release)
	}
}

func TestStrictlyNonOptionalGenericsNegative(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "generic_struct_explicit_optional_type_arg",
			src: `
define Box[T] struct [val: T]
bind main = () -> {
    let b = Box[?int][val: 1]
}
`,
			wantErrPart: "non-optional",
		},
		{
			name: "generic_enum_explicit_optional_type_arg",
			src: `
define Maybe[T] enum [None, Some: T]
bind main = () -> {
    let m = Maybe[?string].None
}
`,
			wantErrPart: "non-optional",
		},
		{
			name: "generic_fn_explicit_optional_type_arg",
			src: `
bind id[T] = (x: T): T -> { x }
bind main = () -> {
    let x?: ?int = 5
    let y? = id[?int](x?)
}
`,
			wantErrPart: "non-optional",
		},
		{
			name: "generic_fn_inferred_optional_for_T_rejected",
			src: `
bind take_non_opt[T] = (x: T): int -> { 1 }
bind main = () -> {
    let x?: ?int = 5
    println(take_non_opt(x?))
}
`,
			wantErrPart: "non-optional",
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
// 4. Runtime Safety Checks Preserved in `--release` Mode
// =============================================================================

func TestReleaseModePreservesAllSafetyTraps(t *testing.T) {
	cases := []struct {
		name           string
		src            string
		wantStderrPart string
	}{
		{
			name: "vec_index_out_of_bounds_in_release",
			src: `
bind main = () -> {
    let xs = [10, 20, 30]
    let idx = 3
    println(xs[idx])
}
`,
			wantStderrPart: "out of bounds",
		},
		{
			name: "vec_slice_out_of_bounds_in_release",
			src: `
bind main = () -> {
    let xs = [1, 2, 3, 4]
    let hi = 10
    let sub = xs[1..hi]
    println(sub.len())
}
`,
			wantStderrPart: "slice bounds",
		},
		{
			name: "string_slice_out_of_bounds_in_release",
			src: `
bind main = () -> {
    let s = "hello"
    let hi = 9
    println(s[0..hi])
}
`,
			wantStderrPart: "slice bounds",
		},
		{
			name: "u64_division_by_zero_in_release",
			src: `
bind main = () -> {
    let a = u64(100)
    let z = u64(0)
    println(a / z)
}
`,
			wantStderrPart: "division by zero",
		},
		{
			name: "shift_count_out_of_range_in_release",
			src: `
bind main = () -> {
    let x = 1
    let amt = 64
    println(x << amt)
}
`,
			wantStderrPart: "shift count",
		},
	}

	root := projectRoot(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			rc, err := driver.Run(driver.Options{
				EntryPath:   filepath.Join(t.TempDir(), tc.name+".ll"),
				SrcOverride: tc.src,
				ProjectRoot: root,
				Release:     true, // Explicitly test --release!
			}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected build error in --release: %v", err)
			}
			if rc == 0 {
				t.Fatalf("[%s] expected non-zero trap exit code in --release, got 0 (stdout=%q)", tc.name, stdout.String())
			}
			if !strings.Contains(strings.ToLower(stderr.String()), strings.ToLower(tc.wantStderrPart)) {
				t.Fatalf("[%s] expected stderr to contain %q in --release, got:\n%s", tc.name, tc.wantStderrPart, stderr.String())
			}
		})
	}
}
