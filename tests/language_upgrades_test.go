package tests

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// =============================================================================
// 1. Tagged Unions (Option A): End-to-End Positive & Negative Tests
// =============================================================================

func TestTaggedUnionsEndToEnd(t *testing.T) {
	t.Parallel()
	src := `
// Plain C enum still works and casts to int
define Color enum [Red, Green, Blue]

// Generic tagged union with unit and single-payload variants
@must_use
@derive(Equatable, Copyable, Printable)
define Outcome[T, E] enum [
    Empty,
    Ok: T,
    Err: E,
    Detail: +[int, E],
]

bind Outcome[T, E].is_ok = (self) bool: {
    match (self):
        when Outcome.Ok(_): true
        default: false
}

define ExprAdd struct [
    left: ref[Expr],
    right: ref[Expr],
]

// Recursive AST enum using ref[Expr] (Arena) and [Expr] (Vector)
define Expr enum [
    Null,
    Num: int,
    Neg: ref[Expr],
    Add: ExprAdd,
    Seq: [Expr],
]

// Match directly on ref[Expr] scrutinee!
bind eval = (e: ref[Expr]) int: {
    match (e):
        when Expr.Null: 0
        when Expr.Num(n): n
        when Expr.Neg(inner): -eval(inner)
        when Expr.Add(pair): eval(pair.left) + eval(pair.right)
        default Expr.Seq(items): {
            let sum = 0
            for (item in items): {
                let tmp = match (item):
                    when Expr.Num(v): v
                    default: 0
                sum += tmp
            }
            sum
        }
}

// Mutate payload in place directly through ref[Expr] using & binding!
bind scale_leaf = (e: ref[Expr], factor: int) void: {
    match (e):
        when Expr.Num(&n): {
            n = n * factor
        }
        when Expr.Add(pair): {
            scale_leaf(pair.left, factor)
            scale_leaf(pair.right, factor)
        }
        when Expr.Neg(inner): {
            scale_leaf(inner, factor)
        }
        default: {}
}

// Resource struct payloads inside enum variants to verify & mutation and own moves
define LoginData struct [
    user: string,
    token: string,
    secret: string,
]

define PacketData struct [
    count: int,
    label: string,
]

define Event enum [
    Logout,
    Login: LoginData,
    Packet: PacketData,
]

bind make_event = () Event: {
    Event.Login(LoginData[
        user: "alice_" + "admin",
        token: "tok_" + "12345",
        secret: "sec_" + "99999",
    ])
}

bind main = (): {
    // 1. Plain C enum compatibility
    let c = Color.Green
    assert(int(c) == 1)

    // 2. Generic tagged union construction (explicit & inferred)
    let o1 = Outcome[int, string].Ok(42)
    let o2: Outcome[int, string] = Outcome.Empty
    let o3: Outcome[int, string] = Outcome.Err("bad_" + "input")
    let o4: Outcome[int, string] = Outcome.Detail(+[404, "not_" + "found"])

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
    assert(o4.to_string() == "Outcome.Detail(+[404, not_found])")

    // Exhaustive ` + "`default Variant(...):`" + ` final arm with destructuring
    let summary = match (o4_copy):
        when Outcome.Empty: "empty"
        when Outcome.Ok(v): "ok:$v"
        when Outcome.Err(e): "err:$e"
        default Outcome.Detail(d): "${d.0}:${d.1}"
    assert(summary == "404:not_found")

    // 4. Mutable & destructuring on local enum variable
    let pkt = Event.Packet(PacketData[count: 10, label: "hello"])
    match (pkt):
        when Event.Packet(&p): {
            p.count += 5
            p.label += "_world"
        }
        default: {}
    let pkt_check = match (pkt):
        when Event.Packet(p): "${p.count}:${p.label}"
        default: "bad"
    assert(pkt_check == "15:hello_world")

    // 5. Own destructuring from local variable AND rvalue without leak or double-free!
    let ev1 = make_event()
    let extracted_user1 = match (ev1):
        when Event.Logout: "none"
        when Event.Packet(_): "pkt"
        default Event.Login(own data): data.user.copy()
    assert(extracted_user1 == "alice_admin")

    // Rvalue scrutinee own-move
    let extracted_user2 = match (make_event()):
        when Event.Login(own data): data.user.copy()
        default: "other"
    assert(extracted_user2 == "alice_admin")

    // 6. Recursive AST evaluation & in-place mutation inside scoped Arena block
    let arena = Arena.new()
    let total_scaled = {
        let n10 = arena.alloc(Expr.Num(10))
        let n20 = arena.alloc(Expr.Num(20))
        let neg5 = arena.alloc(Expr.Neg(arena.alloc(Expr.Num(5))))
        let sum_ab = arena.alloc(Expr.Add(ExprAdd[left: n10, right: n20]))
        let root = arena.alloc(Expr.Add(ExprAdd[left: sum_ab, right: neg5]))
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
	t.Parallel()
	src := `
@must_use
define Status enum [
    Ok,
    Err: string,
]

bind check_it = () Status: {
    Status.Err("problem")
}

bind main = (): {
    check_it();
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
	t.Parallel()
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "non_exhaustive_tagged_match",
			src: `
define Res enum [Ok: int, Err: string, Pending]
bind main = (): {
    let r = Res.Ok(1)
    let x = match (r):
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
bind main = (): {}
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
bind main = (): {}
`,
			wantErrPart: "recursive",
		},
		{
			name: "int_cast_on_tagged_enum_rejected",
			src: `
define Msg enum [Ping, Text: string]
bind main = (): {
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
bind main = (): {
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
bind main = (): {
    let b = BoxedStr.Val("hello")
    let s = match (b):
        when BoxedStr.Empty: ""
        default BoxedStr.Val(own text): text
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
bind inspect = (b: BoxedStr) string: {
    match (b):
        when BoxedStr.Empty: ""
        default BoxedStr.Val(own text): text
}
bind main = (): {}
`,
			wantErrPart: "cannot move",
		},
		{
			name: "own_move_from_arena_ref_enum_rejected",
			src: `
define BoxedStr enum [Empty, Val: string]
bind inspect = (b: ref[BoxedStr]) string: {
    match (b):
        when BoxedStr.Empty: ""
        default BoxedStr.Val(own text): text
}
bind main = (): {}
`,
			wantErrPart: "cannot move",
		},
		{
			name: "inline_struct_variant_in_enum_rejected",
			src: `
define PairMsg enum [Both: [a: string, b: string]]
bind main = (): {}
`,
			wantErrPart: "expected",
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
	t.Parallel()
	src := `
define Node struct [
    id: int,
    name: string,
    edges: [ref[Node]],
]

bind alloc_helper = (a: &Arena, id: int, name: string) ref[Node]: {
    a.alloc(Node[id: id, name: name.copy(), edges: []])
}

bind main = (): {
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
    let consume = (a: own Arena): {}
    consume(own arena)
}
`
	for _, release := range []bool{false, true} {
		runInlineStrict(t, "arena_scoped_pinning_ok", src, release)
	}
}

func TestArenaPinningAndEscapeHolesNegative(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "reset_while_pinned_in_same_scope",
			src: `
define Node struct [val: int]
bind main = (): {
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
bind main = (): {
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
bind drop_arena = (a: own Arena): {}
bind main = (): {
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
bind main = (): {
    let arena = Arena.new()
    let n = arena.alloc(Node[val: 10])
    let f = () Arena: {
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
bind main = (): {
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
bind wipe = (a: &Arena): {
    a.reset()
}
bind main = (): {}
`,
			wantErrPart: "cannot call `.reset()` on borrowed",
		},
		{
			name: "borrowed_mut_arena_param_reassign_forbidden",
			src: `
bind swap_out = (a: &Arena): {
    a = Arena.new()
}
bind main = (): {}
`,
			wantErrPart: "cannot reassign borrowed `&Arena`",
		},
		{
			name: "unwind_escape_from_block_with_local_arena",
			src: `
define Node struct [val: int]
bind main = (): {
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
bind sneaky = (caller_arena: &Arena) ref[Node]: {
    let local_arena = Arena.new()
    local_arena.alloc(Node[val: 42])
}
bind main = (): {}
`,
			wantErrPart: "cannot escape",
		},
		{
			name: "closure_capturing_inner_arena_ref_escaping_block",
			src: `
define Node struct [val: int]
bind main = (): {
    let reader = {
        let a = Arena.new()
        let n = a.alloc(Node[val: 123])
        () int: { n.val }
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
bind main = (): {
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
bind main = (): {
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
bind connect = (parent: ref[Node], child: ref[Node]): {
    parent.edges.push(child)
}
bind main = (): {
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
	t.Parallel()
	src := `
define Profile struct [
    nick?: ?string,
    score: int,
]

define Account struct [
    name: string,
    profile?: ?Profile,
]

bind find_account = (id: int) ?Account: {
    if (id == 1): {
        Account[
            name: "Ada",
            profile?: Profile[nick?: "ada_lovelace", score: 99],
        ]
    } else if (id == 2): {
        Account[
            name: "Bob",
            profile?: Profile[nick?: none, score: 50],
        ]
    } else: {
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

bind Counter.next = (&self) int: {
    self.cur += 1
    self.cur
}

// 3C: T is strictly non-optional; ?T accepts both T and ?T!
define MaybeBox[T] struct [
    item?: ?T,
    fallback: T,
]

bind unwrap_or_default[T: Copyable] = (val?: ?T, fallback: T) T: {
    if (val?): {
        val.copy()
    } else: {
        fallback
    }
}

bind main = (): {
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
    for (i in 0..6): {
        if (i % 2 == 0): {
            next <-
        }
        sum += i
    }
    assert(sum == 9)

    let grid_sum = 0
    outer: for (r in 0..3): {
        for (col in 0..3): {
            if (col == 1): {
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
	t.Parallel()
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "generic_struct_explicit_optional_type_arg",
			src: `
define Box[T] struct [val: T]
bind main = (): {
    let b = Box[?int][val: 1]
}
`,
			wantErrPart: "non-optional",
		},
		{
			name: "generic_enum_explicit_optional_type_arg",
			src: `
define Maybe[T] enum [None, Some: T]
bind main = (): {
    let m = Maybe[?string].None
}
`,
			wantErrPart: "non-optional",
		},
		{
			name: "generic_fn_explicit_optional_type_arg",
			src: `
bind id[T] = (x: T) T: { x }
bind main = (): {
    let x?: ?int = 5
    let y? = id[?int](x?)
}
`,
			wantErrPart: "non-optional",
		},
		{
			name: "generic_fn_inferred_optional_for_T_rejected",
			src: `
bind take_non_opt[T] = (x: T) int: { 1 }
bind main = (): {
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
	t.Parallel()
	cases := []struct {
		name           string
		src            string
		wantStderrPart string
	}{
		{
			name: "vec_index_out_of_bounds_in_release",
			src: `
bind main = (): {
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
bind main = (): {
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
bind main = (): {
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
bind main = (): {
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
bind main = (): {
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

// =============================================================================
// 5. Unified `<front>(<args>) [<type>]: <expr>` Syntax & Return Inference
// =============================================================================

func TestUnifiedSyntaxAndReturnInferenceE2E(t *testing.T) {
	t.Parallel()
	src := `
define Box struct [
    v: int,
]

define Accumulator interface [
    add: (&self, n: int) int,
    reset: (&self),
]

bind Box.new = (v: int): Box[v: v]
bind Box.get = (self): self.v
bind Box.add = (&self, n: int): {
    self.v += n
    self.v
}
bind Box.reset = (&self) void: {
    self.v = 0
}

bind double_val = (x: int): x * 2

bind clamp_pos = (n: int): {
    if (n < 0): func <- 0
    n
}

bind touch_void = (b: &Box): {
    b.add(1);
}

bind main = () void: {
    // Inferred method and function return types
    let bx = Box.new(10)
    assert(bx.get() == 10)
    assert(bx.add(5) == 15)
    touch_void(&bx)
    assert(bx.get() == 16)
    bx.reset()
    assert(bx.get() == 0)

    assert(double_val(21) == 42)
    assert(clamp_pos(-5) == 0)
    assert(clamp_pos(7) == 7)

    // Interface satisfaction via Accumulator
    let acc = Accumulator(bx)
    assert(acc.add(9) == 9)
    acc.reset()
    assert(acc.add(3) == 3)

    // Function type (ParamTypes) Ret and contextual + body lambda inference
    let apply: ((int) int, int) int = (f: (int) int, x: int): f(x)
    assert(apply((n: int): n + 100, 7) == 107)

    let mul = (a: int, b: int): a * b
    assert(mul(6, 7) == 42)

    let make_opt: (bool) ?int = (flag: bool): {
        if (flag): func <- 42
        none
    }
    assert((make_opt(true) ?? 0) == 42)
    assert(make_opt(false) == none)

    // if / else if / else and ?T optional if without else
    let grade = (s: int): if (s >= 90): "A" else if (s >= 80): "B" else: "C"
    assert(grade(95) == "A")
    assert(grade(85) == "B")
    assert(grade(50) == "C")

    let opt_hit? = if (double_val(3) == 6): 60
    let opt_miss? = if (double_val(3) == 99): 60
    assert((opt_hit? ?? 0) == 60)
    assert(opt_miss? == none)

    let typed_opt? = if (true) ?int: 123
    assert((typed_opt? ?? 0) == 123)
    let typed_opt_blk? = if (true) ?int: { 124 }
    assert((typed_opt_blk? ?? 0) == 124)

    // while with ?T (no else), with else, and infinite while (true) int:
    let found? = while (i in 1..6): {
        if (i == 4): while <- i * 10
    }
    let found_typed? = while (i in 1..6) ?int: {
        if (i == 2): while <- i * 10
    }
    assert((found_typed? ?? -1) == 20)
    let not_found? = while (i in 1..4): {
        if (i == 99): while <- i
    }
    assert((found? ?? -1) == 40)
    assert(not_found? == none)

    let found_or = while (i in 1..6) int: {
        if (i == 3): while <- 300
    } else: -1
    assert(found_or == 300)

    let inf_val = while (true) int: {
        while <- 99
    }
    assert(inf_val == 99)

    // for with default list inference and explicit [int] / #[int] eval types
    let xs = for (i in 0..4): i * 2
    assert(xs.len() == 4 and xs[0] == 0 and xs[1] == 2 and xs[2] == 4 and xs[3] == 6)
    let ys = for (i in 1..=3) [int]: i + 10
    assert(ys.len() == 3 and ys[0] == 11 and ys[1] == 12 and ys[2] == 13)
    let zs = for (i in 0..5) #[int]: i % 2
    assert(zs.len() == 2)

    // match (x) T: and bare match: with inferred/explicit eval types
    let label = match (2) string:
        when 1: "one"
        when 2: { "two" }
        default: "other"
    assert(label == "two")

    let bucket = match:
        when xs[0] > 10: 1
        when xs[3] == 6: 2
        default: 0
    assert(bucket == 2)
}
`
	for _, release := range []bool{false, true} {
		out := runInlineStrict(t, "unified_syntax_inference", src, release)
		if out != "" {
			t.Fatalf("expected empty stdout, got %q", out)
		}
	}
}

func TestUnifiedSyntaxAndInferenceNegative(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		src         string
		wantErrPart string
	}{
		{
			name: "recursive_omitted_return",
			src: `
bind rec = (n: int): {
    if (n <= 0): func <- 0
    rec(n - 1) + 1
}
bind main = (): {}
`,
			wantErrPart: "cannot infer return type of recursive function",
		},
		{
			name: "mutual_recursive_omitted_return",
			src: `
bind even_fn = (n: int): {
    if (n == 0): func <- true
    odd_fn(n - 1)
}
bind odd_fn = (n: int): {
    if (n == 0): func <- false
    even_fn(n - 1)
}
bind main = (): {}
`,
			wantErrPart: "cannot infer return type of recursive function",
		},
		{
			name: "void_not_allowed_in_let",
			src: `
bind main = (): {
    let x: void = {}
}
`,
			wantErrPart: "is not a value type and can only be used as a function return type",
		},
		{
			name: "void_not_allowed_in_param",
			src: `
bind f = (x: void) void: {}
bind main = (): {}
`,
			wantErrPart: "is not a value type and can only be used as a function return type",
		},
		{
			name: "void_not_allowed_in_if_eval_type",
			src: `
bind main = () void: {
    if (true) void: 1
}
`,
			wantErrPart: "is not a value type and can only be used as a function return type",
		},
		{
			name: "non_optional_if_without_else",
			src: `
bind main = (): {
    let x? = if (true) int: 1
}
`,
			wantErrPart: "must have an optional evaluation type",
		},
		{
			name: "non_optional_while_without_else",
			src: `
bind main = (): {
    let f = (c: bool) void: {
        let x? = while (c) int: { while <- 1 }
    }
}
`,
			wantErrPart: "must have an optional evaluation type",
		},
		{
			name: "old_arrow_syntax_rejected",
			src: `
bind f = (x: int): int -> x
bind main = (): {}
`,
			wantErrPart: "unexpected '->'",
		},
		{
			name: "if_missing_colon_rejected",
			src: `
bind main = (): {
    if (true) { 1; }
}
`,
			wantErrPart: "expected : before",
		},
		{
			name: "module_optional_chaining_val_rejected",
			src: `
import "std/math"
bind main = (): {
    let x? = math?.PI
}
`,
			wantErrPart: "cannot use optional chaining `?.` on module \"math\"",
		},
		{
			name: "module_optional_chaining_spaced_val_rejected",
			src: `
import "std/math"
bind main = (): {
    let x? = math ?. PI
}
`,
			wantErrPart: "cannot use optional chaining `?.` on module \"math\"",
		},
		{
			name: "module_optional_chaining_call_rejected",
			src: `
import "std/math"
bind main = (): {
    let x? = math?.clamp_int(5, 0, 10)
}
`,
			wantErrPart: "cannot use optional chaining `?.` on module \"math\"",
		},
		{
			name: "module_optional_chaining_type_assoc_rejected",
			src: `
import "std/http"
bind main = (): {
    let c? = http?.Client.new()
}
`,
			wantErrPart: "cannot use optional chaining `?.` on module \"http\"",
		},
		{
			name: "module_type_optional_chaining_assoc_rejected",
			src: `
import "std/http"
bind main = (): {
    let c? = http.Client?.new()
}
`,
			wantErrPart: "cannot use optional chaining `?.` on type `http.Client`",
		},
		{
			name: "unpack_ref_escape_still_rejected",
			src: `
define Node struct [val: int]
bind main = (): {
    let outer_arena = Arena.new()
    let outer_ref = outer_arena.alloc(Node[val: 1])
    {
        let inner = Arena.new()
        let escaped, count = unpack +[inner.alloc(Node[val: 2]), 99]
        outer_ref = escaped
        println("${count}")
    }
}
`,
			wantErrPart: "cannot escape",
		},
	}

	root := projectRoot(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := driver.Check(driver.Options{
				EntryPath:   filepath.Join(t.TempDir(), tc.name+".ll"),
				SrcOverride: tc.src,
				ProjectRoot: root,
			})
			if err == nil {
				t.Fatalf("[%s] expected compile error containing %q, got success", tc.name, tc.wantErrPart)
			}
			if !strings.Contains(err.Error(), tc.wantErrPart) {
				t.Fatalf("[%s] expected error containing %q, got:\n%v", tc.name, tc.wantErrPart, err)
			}
		})
	}
}

func TestQuickFixesE2ERuntimeAndSemaParity(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	mkTarget := filepath.Join(tmpDir, "created_dir")

	src := fmt.Sprintf(`
import "std/os"
import "std/time"
import "std/net"

extern "math.h"
extern cos: (x: float) float

define Node struct [
	val: int,
]

bind main = (): {
	// 1. Unpacked non-ref binding escapes inner Arena block cleanly
	let outer_count = 0
	let outer_node = Node[val: 0]
	let outer_str = ""
	{
		let a = Arena.new()
		let r = a.alloc(Node[val: 41])
		let ref_node, num, tag = unpack +[r, r.val + 1, "ok".copy()]
		outer_count = num
		outer_node = Node[val: 40 + 1]
		assert(ref_node.val == 41)
		outer_str = own tag
	}
	assert(outer_count == 42)
	assert(outer_node.val == 41)
	assert(outer_str == "ok")

	// 2. Unpacked ref from outer Arena alongside ref from inner Arena
	let a_outer = Arena.new()
	let r_outer = a_outer.alloc(Node[val: 100])
	let saved = a_outer.alloc(Node[val: 0])
	{
		let a_inner = Arena.new()
		let r_inner = a_inner.alloc(Node[val: 200])
		let keep_ref, tmp_ref = unpack +[r_outer, r_inner]
		saved = keep_ref
		assert(tmp_ref.val == 200)
	}
	assert(saved.val == 100)

	// 3. extern "math.h" + extern C function on LLVM & C11
	let c0 = unsafe cos(0.0)
	assert(c0 > 0.99 and c0 < 1.01)

	// 4. LLVM/C11 runtime parity for @ll_os_mkdir, @ll_time_local_offset(epoch_sec), @ll_net_shutdown(fd, how)
	let _ = os.remove_all(%q)
	assert(os.mkdir(%q))
	assert(os.file_exists(%q))
	let _ = os.remove_all(%q)

	let dt_win = time.DateTime.must_new(2024, 1, 15, 12, 0, 0, true)
	let dt_sum = time.DateTime.must_new(2024, 7, 15, 12, 0, 0, true)
	println("tz=${dt_win.offset_secs}:${dt_sum.offset_secs}")

	let sp = net.socketpair().unwrap()
	assert(sp.first.send("abc").is_ok())
	unsafe sys_net_shutdown(sp.first.raw_fd(), 1)
	assert(sp.second.recv(16, 1000).unwrap() == "abc")
	assert(sp.second.recv(16, 1000).unwrap_err().has_code("eof"))
}
`, mkTarget, mkTarget, mkTarget, mkTarget)

	out := runInlineStrict(t, "quick_fixes_runtime_and_sema", src, false)
	if !strings.Contains(out, "tz=") {
		t.Fatalf("unexpected stdout: %q", out)
	}
}

func TestQualifiedAndConstExprArraySizesE2E(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFile(t, dir, "consts/dims.ll", `
bind WIDTH = 3
bind HEIGHT = 1 + 1
bind _HIDDEN = 5

define Buf struct [
	data: [WIDTH + HEIGHT: int],
]
`)
	entry := writeTestFile(t, dir, "main.ll", `
import "./consts" as dims

bind LOCAL_N = dims.WIDTH + 1

bind sum_three = (xs: [1 + 2: int]) int: xs[0] + xs[1] + xs[2]
bind first_width = (xs: [dims.WIDTH: int]) int: xs[0]

bind pass_through[T] = (v: own T) T: own v

bind main = (): {
	let a: [1 + 2: int] = [10, 20, 30]
	assert(sum_three(a) == 60)

	let b: [dims.WIDTH: int] = [dims.WIDTH: 7]
	assert(first_width(b) == 7)
	assert(b[2] == 7)

	let c: [(1 + 2) * dims.HEIGHT: u8] = [dims.WIDTH * dims.HEIGHT: u8(9)]
	assert(c.len == 6)
	assert(c[5] == u8(9))

	let d: [LOCAL_N: int] = [LOCAL_N: 4]
	assert(d.len == 4)

	let nested: [[dims.HEIGHT: int]] = [[1, 2], [3, 4]]
	assert(nested[1][1] == 4)

	let generic_arr = pass_through[[dims.HEIGHT: int]]([5, 6])
	assert(generic_arr.len == 2)
	assert(generic_arr[1] == 6)

	let buf = dims.Buf[data: [dims.WIDTH + dims.HEIGHT: 11]]
	assert(buf.data.len == 5)
	assert(buf.data[4] == 11)

	println("ok_const_array_sizes")
}
`)
	out := runFileStrict(t, "qualified_const_expr_array_sizes", entry, false)
	if strings.TrimSpace(out) != "ok_const_array_sizes" {
		t.Fatalf("unexpected stdout: %q", out)
	}

	root := projectRoot(t)
	negCases := []struct {
		name string
		src  string
	}{
		{
			name: "cross_pkg_private_const_array_size_rejected",
			src: `
import "./consts" as dims
bind main = (): {
	let bad: [dims._HIDDEN: int] = [5: 0]
}
`,
		},
		{
			name: "zero_const_expr_array_size_rejected",
			src: `
bind main = (): {
	let bad: [1 - 1: int] = []
}
`,
		},
		{
			name: "negative_const_expr_array_size_rejected",
			src: `
bind main = (): {
	let bad: [-2: int] = [1, 2]
}
`,
		},
		{
			name: "out_of_range_shift_const_expr_array_size_rejected",
			src: `
bind main = (): {
	let bad: [1 << 64: int] = [1: 0]
}
`,
		},
	}

	for _, tc := range negCases {
		t.Run(tc.name, func(t *testing.T) {
			negEntry := writeTestFile(t, dir, tc.name+".ll", tc.src)
			_, _, err := driver.Check(driver.Options{
				EntryPath:   negEntry,
				ProjectRoot: root,
			})
			if err == nil {
				t.Fatalf("[%s] expected compile error, got success", tc.name)
			}
			if !strings.Contains(err.Error(), "positive compile-time integer constant") {
				t.Fatalf("[%s] expected constant-size diagnostic, got:\n%v", tc.name, err)
			}
		})
	}
}

func TestArrayRepeatAndMapBlockValuesE2E(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	entry := writeTestFile(t, dir, "lit_blocks_e2e.ll", `
bind N = 3

bind main = (): {
	let a: [3: int] = [N: { let base = 7; base + 5 }]
	assert(a.len == 3)
	assert(a[0] == 12 and a[1] == 12 and a[2] == 12)

	let b = [N: { 10 }, { 20 }, { 30 }]
	assert(b.len == 3)
	assert(b[0] + b[1] + b[2] == 60)

	let s_arr = [1 + 1: { "hi".copy() }]
	assert(s_arr.len == 2)
	assert(s_arr[0] == "hi" and s_arr[1] == "hi")

	let k1 = "alpha"
	let m1 = #[k1: { let x = 100; x + 1 }]
	assert((m1["alpha"] ?? -1) == 101)

	let ka = "first"
	let kb = "second"
	let m2 = #[ka: { 11 }, kb: { let y = 20; y + 2 }]
	assert((m2["first"] ?? -1) == 11 and (m2["second"] ?? -1) == 22)

	let ik = 9
	let m3 = #[5: 10, ik: { let n = 6; n * 7 }]
	assert((m3[5] ?? -1) == 10 and (m3[ik] ?? -1) == 42)

	let v_lbl = [(blk: { if (true): blk <- 77; 0 })]
	assert(v_lbl[0] == 77)

	let s_lbl = #[(sblk: { sblk <- 88 })]
	assert(s_lbl.contains(88))

	let v_lbl2 = [1, b2: { b2 <- 99 }]
	assert(v_lbl2[0] == 1 and v_lbl2[1] == 99)

	println("ok_lit_blocks_e2e")
}
`)
	out := runFileStrict(t, "array_repeat_map_block_values", entry, false)
	if strings.TrimSpace(out) != "ok_lit_blocks_e2e" {
		t.Fatalf("unexpected stdout: %q", out)
	}
}
