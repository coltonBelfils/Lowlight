# Review of the `comptime` Plan and Stages 1–3

**Reviewed:** 2026-11-24, against [`COMPTIME_DESIGN.md`](./COMPTIME_DESIGN.md), [`COMPTIME_IMPLEMENTATION_PLAN.md`](./COMPTIME_IMPLEMENTATION_PLAN.md), and the uncommitted stage 1–3 code (mainly [`internal/sema/comptime.go`](./internal/sema/comptime.go), about 5,300 lines).

This review has three parts:

1. **Bugs** in what stages 1–3 built.
2. **Holes** in the design and plan.
3. **Additions** I'd make to the plan.

A suggested order of work comes after them, then an appendix with every repro program. [Part 4](#part-4--second-review-after-phase-35) is a second review, done after the Phase 3.5 fixes, with the bugs still open.

## How this was checked

- **Tests.** `go build`, `go vet` on sema, ir and codegen, and `go test ./internal/...` all pass. `go test ./tests/` passes except `TestStandardLibraryInlineSuites/{llvm,c11}/json` ("tests for string.to_json"), which fails the same way at HEAD and has nothing to do with comptime.
- **Probe programs.** I built the compiler into a scratch directory and ran small programs on both backends (`--backend llvm` and `--backend c11`), some with `LOWLIGHT_CHECK_LEAKS=1`. Each program compares compile-time results with runtime results, or tries to break something.
- **Confidence labels.** Every finding is marked either:
  - **Reproduced**: I ran a program that shows it.
  - **From the code**: I read it in the source but didn't run it, usually because running it could exhaust the machine's memory.

## What already works

These are worth keeping as they are:

- **Parity that holds.** Compile-time and runtime give identical results for:
  - integer arithmetic at every width, including wrapping, `INT_MIN`, signed `/` and `%`, and shifts;
  - `"abc".hash()`, `12345.hash()` and `1.5.hash()`;
  - Map and Set iteration order (both are insertion order);
  - byte indexing on UTF-8 strings.
- **Layout queries.** `size_of` and `align_of` at compile time match what both backends report for structs with padding, optional fields, tuples, enums with payloads, results, tag enums, `?u8`, fixed arrays, `fn`, `cl` and `Map`. This also holds inside generic functions.
- **Diagnostics.** The step limit, the depth limit, division by zero, list index out of bounds, literal overflow (`const C: u8 = 256`), and calling a `bind` function from `const` code all give clear errors.
- **Calls through function values.** `const apply = fn(f: fn(int) int, x: int) int: f(x)` called at compile time with a `bind` function gives a clear error at the point of evaluation.
- **Baking.** Mutating a baked value from runtime code works on both backends with no leaks:
  - lists of ints and lists of strings, using `xs[i] = ...`, `push`, `set`, `pop`, `truncate`, `remove`, `swap_remove`, `+=` on elements, and moving into an `own` parameter;
  - strings, using `push`, `+=` and `clear`.
- **Cache invalidation.** In a project where `main` imports `inc/tbl`, editing a `const` in `tbl` updates what `main` prints on the next `lowlight run`. That covers both a `const` read directly and one used inside `comptime`.
- **Speed.** A CRC-32 table builds instantly. About 800k interpreter steps take about 1.2 s.

---

# Part 1 — Bugs in stages 1–3

They're listed in order of severity.

## B1. The compiler crashes on `to_string` of any list, map or set at compile time

**Reproduced.**

```lowlight
const opt = fn() string: "${[3, 1]}"        // also "${[3, 1].to_string()}", "${#["a": 1]}"
bind main = fn(): println(comptime opt())
```

The Go runtime aborts with `goroutine stack exceeds 1000000000-byte limit` and `fatal error: stack overflow`. There is no diagnostic; the compiler process just dies.

**Cause.** Two functions call each other with no limit:

- `ctState.valToString` (`comptime.go:4209`) looks up the value's `to_string` method. It calls the method if `isComptimeCallable` is true, which it always is for List, Map and Set, because of the std exemption (see H3).
- The List hook's `to_string` case (`comptime.go:4607`, and the same pattern at 4754 and 4870) calls `s.valToString(args[0], ...)` on the same value.

The result is `valToString → callFuncInstance → tryCallStdlibHook → valToString → …`.

**Fix.**

- Short term: in the collection hooks, format the elements by calling `valToString` on each *element*, never on the collection itself.
- Root fix: don't hook `to_string` at all; run the Lowlight `List.to_string` (see H1).

**Also add** a guard that counts Go-level recursion depth on every path that re-enters the evaluator (hooks, `valToString`, `equals`, `hash`, `clone`). A Go stack overflow can't be recovered from, so every recursive path needs its own guard.

## B2. Writes through `&` parameters are lost

**Reproduced.**

```lowlight
define P struct [x: int]
const bump   = fn(n: &int): { n += 1 }
const bump_p = fn(p: &P): { p.x += 10 }
const grow   = fn(xs: &[int]): xs.push(7)
const f = fn() string: {
	let a = 1
	bump(&a)
	let p = P[x: 1]
	bump_p(&p)
	let xs = [1]
	grow(&xs)
	"$a ${p.x} ${xs.len()}"
}
// prints "1 11 2"; the runtime equivalent prints "2 11 2"
```

**Cause.** `evalCallArg` (`comptime.go:3801`) evaluates an `&` argument as a place, then immediately calls `pl.get()` and passes the *value*. The callee binds that value into a fresh slot.

- **Mutating inside the value works by accident.** A field write or a list push changes a `*ctStructBox` or `*ctListBox` that the caller's value shares through a Go pointer.
- **Reassigning the parameter itself is lost.** `n += 1`, `n = …`, or `p = P[...]` replaces the callee's slot and is never written back.

**This bug causes other failures.** Two of them reproduced:

1. `"héllo".chars()` in `const` code runs until the step limit. `string.chars` calls `str_utf8_next(self, i, &next_i)`, the `&int` write is lost, `next_i` stays `0`, and the loop never ends. The error points inside `string.chars` in `std/core/primitives.ll`, which tells the user nothing.
2. `m.update("b", cl(v: &int): { v += 100 })` leaves `b` at `2` at compile time; at runtime it becomes `102`.

Any std or user code that returns results through an `&` scalar, `&string` or `&` optional is wrong at compile time. Any code that reassigns a whole `&` struct is wrong too.

**Fix.** Make `&` parameters real references. Either:

- bind the callee's parameter slot to the caller's `ctPlace`, so reads go through `get` and writes through `set`; or
- give `ctSlot` an `alias *ctSlot` (or `place *ctPlace`) and resolve it in `lookupSlot`.

The same fix is needed for:

- `&self` receivers;
- closure parameters (`callLambda`);
- `for(&x in xs)`-style bindings, if the language has them;
- Phase 6's `&v.field(f)` and `v.field(f) = val`.

See H7: this is a gap in the value model, not a one-line patch.

## B3. Floats format differently at compile time

**Reproduced.**

| Expression | Compile time | Runtime |
| :--- | :--- | :--- |
| `"${0.1 + 0.2}"` | `0.30000000000000004` | `0.3` |
| `"${1.0 / 3.0}"` | `0.3333333333333333` | `0.333333` |
| `"${f32(0.1) + f32(0.2)}"` | `0.30000001192092896` | `0.3` |
| `"${1e300 * 1e10}"` | `+Inf` | `inf` |

**Cause.** `ctVal.toString` (`comptime.go:758`) uses Go's `strconv.FormatFloat(v.f, 'g', -1, 64)`. The runtime uses `float.to_string` → `format_float_prec(self, -1)` (`std/core/primitives.ll:587`). That function isn't marked `const` and isn't in the std exemption list, so `valToString` falls back to the Go formatting.

Two further problems:

- **`f32` isn't handled separately.** It's formatted as a widened `f64`.
- **Results are silently wrong.** A `const` string built from floats differs from the runtime string. Hashes, keys and lookup tables built from it differ too, and nothing reports an error.

**Fix.** Format floats only by interpreting the Lowlight `format_float_prec`. Delete the Go fallback for floats, or make it an internal compiler error. More generally, `ctVal.toString` should only be used for debugging output, never for values the program can see.

## B4. Sorting a baked list crashes the program

**Reproduced on both backends.**

```lowlight
import "std/sort"
bind main = fn(): {
	let xs = comptime [3, 1, 2]
	sort.sort(&xs)
	println(xs)
}
// exit code 255, no output, no panic message
```

**Cause.** Phase 3 added copy-on-write guards (`if(self.cap <= 0): self.reserve(self.len)`) to some `List` methods in `std/core/list.ll`. But `List[T].sort`, `sort_by`, `stable_sort` and `stable_sort_by` (`std/sort/sort.ll:502–523`) pass `self.data` straight to unsafe pointer helpers that call `ptr_write`. On a baked list, `data` points into read-only memory.

**Other code that may have the same problem** (it writes through `ptr_write`, `copy_raw` or `zero_bytes`, possibly on list or string buffers; not yet checked one by one):

- `std/core/list.ll`, `std/core/map.ll`, `std/core/set.ll`, `std/core/primitives.ll`, `std/core/arena.ll`
- `std/sort/sort.ll`, `std/fs/fs.ll`, `std/pool/pool.ll`
- `std/collections/deque.ll`, `std/collections/btree_map.ll`

User code with `unsafe` can do the same thing.

**Fix.**

- Add one private helper, say `List[T]._data_mut(&self) ptr[T]`, that promotes the buffer when `cap <= 0` and returns `data`. Do the same for `string`.
- Change every method that writes through `data` to use it.
- Check each file above.
- Write the rule into SPECIFICATION.md's unsafe section: a list or string with `cap <= 0` doesn't own its buffer, so unsafe code must promote it before writing.

**A separate issue:** the program died with exit 255 and printed nothing. A write to read-only memory should at least print a fatal-signal message. If the runtime has a signal handler, check why it didn't run.

## B5. The 64 MB memory budget is barely enforced

**From the code** (not run, because running it could exhaust the machine's memory).

`allocMem` is called at only 8 sites in `comptime.go`:

- list, array and struct literals;
- list `push` / `extend`;
- `string.add`.

It is **not** called for:

- `string.add_assign`, `string.push`, `push_char`, `push_byte` (line 4412);
- map and set `insert`;
- `clone`, `copy` and slicing;
- string interpolation.

Each list element is charged 24 bytes, but a `*ctVal` in Go is about 230 bytes (around 25 fields, padded). The total is never reduced when values are dropped, either.

So `let s = "x"` followed by `for(_ in 0..40): s += s` is an 8-step loop that would try to allocate a terabyte. The 64 MB cap never triggers; the compiler simply runs out of memory.

**Fix.**

- Charge every hook that grows a value, at the size it really takes in Go.
- Credit the budget back when a value is dropped, or treat the budget as a limit on total allocation and document it that way.
- A much smaller `ctVal` would help too (see A15).

## B6. Calling a `const` generic at runtime fails if a method it uses is `bind`

**Reproduced.**

```lowlight
define U struct [n: int]
bind U.to_string = fn(self) string: "U${self.n}"
const show[T: Printable] = fn(x: T) string: "<${x.to_string()}>"
bind main = fn(): {
	println(show(U[n: 3]))   // error: cannot call runtime-only (`bind`) function "U.to_string"
	println(show(5))         // error: cannot call runtime-only (`bind`) function "int.to_string"
}
```

Neither call is at compile time, yet both are compile errors. Under this rule, no generic in std that calls an interface method could ever be `const`. This is mainly a design hole (H2), but the behavior in stages 1–3 is wrong either way.

**Also inconsistent:** `"$x"` interpolation of an `int` inside `const` code works, but calling `x.to_string()` explicitly on the same `int` is rejected. That's because interpolation has its own path through `valToString`.

## B7. `@comptime_limit` rejects `_` digit separators

**Reproduced.** `@comptime_limit(50_000_000)` gives `step count must be a positive integer, got "50_000_000"`. The design doc's own example is `@comptime_limit(10_000_000)`.

**Cause.** `types_pass.go:204` calls `strconv.ParseInt` on the raw attribute text. Fix: strip the `_` characters, or better, parse the argument as an integer literal with the lexer's own rules and check its range.

## B8. Compile-time error traces are hard to read

**Reproduced.**

1. **No collapsing.** Exceeding the depth limit prints all 256 `note: called from \`deep\`` lines. Collapse repeated frames, e.g. `note: … 250 more calls to \`deep\` …`.
2. **Runtime caller listed as a frame.** For a `comptime` expression inside `main`, the trace ends with `note: called from \`main\` at …`, but `main` was never evaluated at compile time. The bottom frame should name the `comptime` expression or the `const` declaration.
3. **Std location instead of user location.** When the failure is in std code, as in B2, the main span points into `std/core/primitives.ll`. Point the main span at the first frame in user code and put the std frames in notes. The design already plans this for `@track_caller` (§9.1).
4. **Notes are part of the message text.** `reportAbort` (`comptime.go:269`) joins them into the message string instead of attaching them as structured notes. Phase 7 adds `Diagnostic.Related`. Until then, the LSP shows the whole trace as one long message.

## B9. The cycle error is mislabeled and incomplete

**Reproduced.** `const A = B + 1` / `const B = A + 1` reports `cyclic dependency detected in top-level bind constant "A"` (`check_stmt_expr.go:215`).

- It says `bind` for a `const` declaration.
- It doesn't name the cycle, which design §1 rule 4 requires.

The message should list the whole cycle as notes: `A` → `B` → `A`, each with its span.

## Not yet verified

- **Split units.** Baking in `UnitPackage` split units and `split.go` is covered only by `TestComptimeStaticBakingSplitPackages`. I didn't probe it further.
- **Cross-target baking.** Not checked; see H13.
- **Phase 1 parsing of value generic arguments** (`Matrix[2 + 2, 4]`, `zeros[8]()`). These have parser tests, but nothing uses them yet. They can't be exercised end to end until Phase 4.

---

# Part 2 — Holes in the design and plan

## H1. std/core is implemented twice, and the copies are already drifting apart

**This is the biggest structural issue.** B1, B2's effect on `Map.update`, and B3 all come from it.

The evaluator doesn't run std/core's Lowlight code for strings and collections. Instead, `tryCallStdlibHook` (`comptime.go:4376–4880`, about 500 lines) re-implements string, `List`, `Map`, `Set`, `Range` and the iterators in Go, method by method. `isStdHookedComptimeFunc` (`comptime.go:1302`) then marks every method on those types as callable at compile time.

**Consequences:**

- **Drift.** Every std change has to be made twice, and nothing checks that the two copies agree. B3 is a drift bug that already exists.
- **Different semantics.**
  - The comptime Map and Set are linear lists of entries.
  - Keys are compared with `ctVal.equals`, which is structural. The key type's own `eq` and `hash` are never used, so a key type whose `eq` ignores case, or ignores a cache field, behaves differently at compile time.
  - Removing from the middle of a map uses Go's ordered `append(entries[:i], entries[i+1:]...)`. If the runtime Map ever changes how it removes or reorders (swap-remove, tombstones, compaction), iteration order will silently diverge.
- **Speed.** Lookups are O(n), so building a map is O(n²). 20,000 inserts take about 1 s; 100,000 would take about 25 s. Phase 8 plans to build Unicode tables at compile time, which is exactly this workload.
- **Methods that aren't hooked.** Any std method without a hook falls back to interpreting the Lowlight body. That body uses `unsafe` pointer intrinsics on a `ctList`, which the evaluator can't run, so it either errors or behaves unpredictably. Nothing guarantees every method on these types has a hook.
- **Hidden constness.** The exemption makes all these methods callable at compile time without being marked `const`, which undermines the opt-in rule (H3).

**Recommendation: interpret the real std/core and hook only the unsafe primitives.**

- Model a small heap in the evaluator: allocations are typed cell arrays, and a pointer is `(allocation, index)`.
- Implement `alloc_raw`, `alloc_uninit`, `realloc_raw`, `free_raw`, `ptr_read`, `ptr_write`, `ptr_add`, `copy_raw`, `zero_bytes`, `is_null` and `ptr_cast` on that model. That's about 15 functions instead of 500 lines.
- Allow std/core's own `unsafe` blocks at compile time, and keep rejecting user-written `unsafe` (design §4.1).
- Map and Set then use their real hash tables, call the key type's real `eq` and `hash`, and give the runtime's iteration order. `float.to_string` uses the real `format_float_prec`. Parity follows from running the same code.
- Speed: hash lookups become O(1). Each step does more work, but the O(n²) behavior goes away. Measure with the A1 corpus before and after.
- Values crossing to runtime (`exportCtVal`) read the simulated `List` or `string` struct and its cell array. That's no harder than reading a `ctList`.

**If that's too big a change now,** at least keep the hooks honest with a parity harness (A1). Run every hooked method on a corpus of inputs at both compile time and runtime, and fail CI on any difference. Without that, every std edit can silently break compile-time results.

## H2. Generics and function values need conditional constness

**The design says** (§3.1) that a `const` function is "validated at its declaration (or monomorphization for generics)". Stages 1–3 apply that to every instance, including ones used only at runtime (B6). Since `int.to_string`, every user `to_string`, `eq` and `hash`, and most interface methods are `bind`, almost no useful generic can be `const`. That includes:

- std's own `List[T].contains`, `Map[K, V].get` and `join`, once they lose the std exemption;
- every generic in a user library.

**The design already solves this for `@derive`** (§6.2): an attached method is `const` if every field operation it uses is `const`, otherwise `bind`. **Generalize that rule:**

1. `const name[T…] = fn…` declares a generic whose instances are *as const as they can be*.
2. Check each instance against the coloring rules as it's instantiated.
   - If the body passes, the instance can be called at compile time.
   - If it fails, the instance quietly becomes runtime-only, and the failure reason is kept.
3. An error appears only when compile-time code actually calls a runtime-only instance. The error says why, using the reason kept in step 2, e.g. "`show[U]` can't run at compile time because it calls `U.to_string`, which is `bind`".
4. A non-generic `const` function is still checked strictly where it's declared. That's the point of the keyword.
5. Calls through `fn` values are already checked when evaluated (`apply(rt_inc, 1)` gives a good error). Write that into the spec as the rule for indirect calls, so "checked where declared" isn't claimed for calls that can't be checked there.
6. Interface method calls in generic bodies follow from rule 2.

This matches C++ `constexpr` templates and Zig's lazy analysis. It's the only version of opt-in constness that composes with generics. **This is a language decision for you**; the alternative is to make most of std `const` and accept that user types must mark their `to_string`, `eq` and `hash` as `const` to be usable in generic `const` code.

## H3. The std exemption makes constness invisible and inconsistent

`isStdHookedComptimeFunc` makes these callable at compile time:

- every method on `string`, `List`, `Map`, `Set`, `Range`, `Array` and their iterators;
- `str_utf8_next`, `join_strings`, `parse_int`, `parse_int_radix`, `parse_float`, `min`, `max`, `abs` and `clamp`.

But `int.to_string`, `float.to_string`, the `char` methods and the rest of `primitives.ll` aren't covered. To a user, which std functions work at compile time looks random, and hover and docs show them all as plain `bind`.

**Recommendation.**

- Remove the exemption.
- Mark std functions `const` in their source wherever they qualify. With H1's approach, most of std/core qualifies as written.
- Let the normal checks that run where a `const` is declared verify them.
- Show const-ness in hover, the doc generator and completion.

Phase 8.1 plans to migrate std to `const` anyway. Do it for std/core *before* removing the exemption, so nothing that works today breaks in between.

## H4. Top-level `comptime if` can't prune `import`s in sema

The design (§2.2) allows `import` inside a top-level `comptime if`, and plan step 4.3 prunes untaken branches in sema. But **the driver resolves, fetches and loads every imported package before sema runs**: `manifest`, `lowlight fetch`, `vendor`, `tree`, `--locked`, and the per-package work in `driver`. By the time sema prunes, an import used only on Windows has already been resolved on macOS. If it can't be fetched or doesn't build on this target, the build has already failed.

**Options:**

1. **A small evaluator before loading.** The driver parses each file and evaluates top-level `comptime if` conditions with a restricted evaluator that sees only `target`, literals and earlier top-level `const`s with literal values. It drops untaken imports before resolving packages. Sema then re-evaluates the same conditions and checks that the results agree.
2. **Dependencies per target in the manifest** (e.g. `[target.'cfg(os = "windows")'.dependencies]`, as Cargo does). `import` under `comptime if` is then only allowed for packages that are always present.
3. **Ban `import` under `comptime if`.** Keep platform-specific code in the same package, behind `comptime if` around declarations, which the design already supports.

Option 3 is the simplest and loses little, because the design's platform story is "single-file `comptime if` everywhere" within std/os. **This is your decision.**

**Related rules that need writing down whichever option is chosen:**

- **What a condition may reference.** If a condition reads a `const` that is itself declared under another top-level `comptime if`, the order of evaluation matters, and there can be cycles. Either restrict conditions to `target` and literals, or define a fixpoint and report an error when a condition depends on its own branch.
- **Duplicate names.** A name declared in both arms (one per platform) is normal; a name declared twice in the *taken* arm is an error. Check this after pruning, not before.
- **Docs.** `lowlight doc`, docgen and the website index work from source. Decide whether they show all arms or only the host's.

## H5. Code in untaken branches is never checked

The design says (§2.2) that untaken branches only need to parse. That's correct for compiling, but it means code behind `target.os == windows` gets no type checking at all on a Mac or Linux machine. Nobody sees its errors until someone builds on Windows, and std/os will have three branches in most functions.

**Recommendation:**

- Add `lowlight check --all-targets`, or a list of targets: check each target's selected branches, and merge and deduplicate the diagnostics.
- Run it over std in CI (Phase 5.2 already plans cross-target emit and check tests; make this a standing check rather than a one-off).
- Add an LSP setting that checks an extra target in the background, or at least resolves names in untaken branches, so typos show up.
- Have the formatter format untaken branches too. It probably already does, since it works from the parse tree; confirm with a test.

## H6. `target` arrives after the feature that needs it

Plan step 4.3 (top-level `comptime if`) comes before 5.1 (`target`). But the main reason for a top-level `comptime if` is a condition on `target`, and the design's own examples use it. Without `target`, step 4.3 can only be tested with made-up constant conditions.

**Recommendation:** move 5.1 (the `target` types, manifest `TargetInfo` fields, and the `const target`) into Phase 4, before 4.3. It has no dependencies on the rest of Phase 4.

## H7. The value model has no references

Design §4.1 says compile-time code uses "ownership, moves, `&` borrows, and LIFO `Drop` (using the exact same Model 2A rules)". The evaluator works differently:

- **Values are Go pointers to boxes** (`*ctListBox`, `*ctStructBox`), shared or not depending on whether `clone()` was called on that path.
- **References don't exist.** An `&` argument is turned into a value (B2).
- **Copying is ad hoc.** `evalCallArg` clones only for `own`. Reads borrow by sharing the pointer. Plain-struct assignment copies (`let b = a; b.x = 99` leaves `a` alone, which I checked). Whether a value is copied or shared depends on which Go function handled it.

Sema's checks prevent most observable aliasing today, so the program-level results mostly hold. But Phase 6 needs real references:

- `&v.field(f)`, `v.field(f) = val`, `own v.field(f)`;
- `v.payload(vr)`, which borrows when `v` is borrowed and moves when `v` is owned.

Phase 4's `comptime for` will bind loop items over lists of descriptors and values, too.

**Recommendation:** before Phase 4, write down the evaluator's model in a short design note and make the code match it:

- what a slot is;
- what a place is;
- when a value is shared and when it's copied;
- how `&` binds to a place.

The simulated heap from H1 helps here: List and string become ordinary structs whose contents are shared on purpose, through a `ptr` cell.

## H8. The budgets need more thought

- **Per root only.** The step limit (1,000,000) applies to each root evaluation. A package with 1,000 `const`s may legally take 1,000,000,000 steps, about 25 minutes at the speed measured. Add a total budget per package, or per build, that can be raised with a manifest setting.
- **Memory** (B5) needs real accounting.
- **The LSP needs a time limit.** Design §9.2 plans a lower step limit in the IDE. Step counts are deterministic, which is right for builds, but a single step can be expensive (a big `clone`, a big string concatenation). The LSP also needs a wall-clock timeout that cancels evaluation, reported as a warning, not an error. Timeouts must never apply to `lowlight build`, or builds stop being reproducible.
- **`@comptime_limit` lacks a ceiling.** Decide whether it can raise memory and depth as well as steps, and whether a project or manifest can set a maximum that libraries can't exceed. Otherwise a dependency can make your build arbitrarily slow.
- **Depth 256 is low for recursive-descent code** (a JSON or config parser run at compile time, B-tree building). The evaluator's own Go recursion probably costs several Go frames per Lowlight frame; measure that, then consider 1,024 with a Go stack check (B1) as the real safety net.

## H9. The copy-on-write rule is enforced by convention only

Phase 3's approach: a baked list or string has `cap = 0`, lives in read-only memory, and every mutating method promotes it to the heap first. That's sound if every write path checks `cap`. Today only some methods do (B4), and nothing enforces it for new code.

**Recommendation:**

- The single helper from B4 (`_data_mut`).
- The unsafe-section rule in SPECIFICATION.md.
- A test that, for every public method on `List`, `string`, `Map` and `Set` that mutates, starts from a baked value, calls the method, and runs under ASan (the 3.5 test does this for a hand-picked list).
- **Maps and sets.** A `Map` or `Set` can't currently cross to runtime. If that's ever relaxed, they need the same treatment, and their bucket arrays are harder: `buck_cap == 0` already means "empty", so it can't also mean "static".

## H10. Diagnostics arrive in the wrong order

Phase 2 produces compile-time traces, but structured `Related` notes come in Phase 7. Everything between produces traces as joined text (B8.4), and Phases 4–6 will add many new trace-producing errors: unrolling, instantiation, `compile_error`, `@derive`.

**Recommendation:** move 7.1 (`Diagnostic.Related`, CLI rendering, mapping to LSP related information) right after Phase 3. It's small, and the work after it is much easier to debug with it. Do the frame collapsing and user-frame selection from B8 at the same time.

## H11. Questions the design leaves open

Each needs a decision and a line in the design:

1. **`comptime if` in expression position.** §2.2 says it yields "`?T` only if all arms match — note statement/block forms are primary". That sentence is unclear. Proposal: with an `else`, it works like `if`/`else` and has the arms' common type. Without an `else`, it's statement-only, as runtime `if` is today.
2. **Exhaustiveness of `comptime match`.** Must a `comptime match` over an enum still cover every variant, or only the one actually selected? Untaken arms aren't type-checked, but whether arms are *missing* can still be checked. Proposal: require exhaustiveness, as runtime `match` does; a `default: compile_error(...)` arm is the usual way to cover the rest.
3. **A `const` local inside a runtime function** that refers to a runtime local (`let n = …; const M = n + 1`). It should be an error. Specify the message, and hint `comptime` versus `bind`.
4. **Why `target` can be shadowed.** The design makes `const target` shadowable. That lets a local `target` variable (a common name: build targets, network targets) silently replace it in conditions further down the file. Consider making it a universe `const` that can't be shadowed, or putting it in a namespace (`build.target`, `comptime.target`).
5. **Using a `@comptime_only` function as a value.** Taking `@comptime_only` functions as `fn` values (`let f = helper`) in runtime code, and storing them in a baked `const` table of `fn` values, must both be rejected; nothing gets emitted for them, so the pointer would dangle. The 3.x tests cover calls, not values.
6. **`@comptime_only` types in runtime signatures.** A runtime `bind` function whose parameter or return type is `FieldInfo` (Phase 6) must be an error.
7. **`Drop` on a value that crosses to runtime.** A comptime local is dropped in LIFO order at the end of evaluation, but a value that *crosses* to runtime is moved out first. Specify that it's moved, not dropped. Crossing values have no custom `Drop` today, but the order matters once Phase 6 adds descriptors.
8. **`const` methods satisfying interfaces.** Can `const T.eq` satisfy `Equatable.eq`? Yes; specify it. Does calling it through an interface keep its const-ness? Yes, under H2.
9. **Naming in `TypeKind`.** Variants mix `_kind` suffixes (`struct_kind`, `list_kind`) with others (`tag_enum`, `tagged_union`). Use one rule. If the suffix exists only because `struct` and `enum` are keywords, use it on all of them, or quote keyword variants some other way.
10. **Mangling of `string` const generic arguments.** The design says "hex or SHA-256 tag". Choose one now. Use a stable hash with a readable prefix, so symbol names don't change between compiler versions.

## H12. Reproducibility

A compile-time result must not depend on the host, the Go version, or anything not in the source.

- **Float formatting** (B3) is one way that can fail today.
- **Hash seeds.** Compile-time and runtime hashes match today because runtime hashing is deterministic. If the runtime ever adds random hash seeds (a common defense against HashDoS), any `const` computed from `.hash()` will differ from runtime. Either rule out random seeds in the spec, or treat `hash()` at compile time as allowed with values that may differ from runtime, and say so.
- **Map iteration.** Order matches only because both implementations happen to keep insertion order (H1).

**Recommendation:** add a reproducibility section to the spec. A compile-time result is a pure function of the source, the target and the compiler version, and it equals what the same code computes at runtime on that target.

## H13. Cross-compilation and byte order of baked data

`typeSize` uses the target's pointer size, which is right. But check how Phase 3 emits baked arrays:

- **Typed constants** (LLVM `[N x i32] [...]`, C `{1, 2, 3}`): the target compiler handles byte order and nothing more is needed.
- **Raw byte arrays** for some types: they must be written in the target's byte order, and floats in the target's format.

Add a check-only test with `--target` set to a big-endian triple once Phase 5 has `Endian`, even if it can't run.

## H14. Building compile-time values in dependent packages

When `main` uses `tbl.WORDS`, does sema re-evaluate `tbl`'s `const`s on every build of `main`? It probably does; that needs checking. For a big Unicode table in std, every program that imports it would pay the cost on every build.

**Recommendation:** store each package's evaluated `const` values in its cached interface (next to the `.o` cache), keyed by the package's source hash, target and compiler version. The incremental test I ran shows invalidation already works; this would be about speed.

## H15. Unrolling and const generics can make code size explode

`comptime for` unrolling and `[N: const int]` generics multiply code. `Matrix[R, C]` creates a type per size, and a `comptime for` over 1,000 elements emits 1,000 copies of its body. Nothing in the plan limits this.

**Recommendation:**

- Limit the total number of statements unrolled per function, adjustable with `@comptime_limit`.
- Report the number of instantiations in `lowlight build -v`.
- For `comptime for` over plain data (not descriptors), give a warning that suggests an ordinary loop when the body doesn't depend on the item's type.

## H16. Phase 6: how far `@derive` authorization reaches, and what deriving costs

- **Transitivity.** `@derive(I)` lets the derived method read `_` fields. When `derive.eq[Point]` calls a helper generic `_field_eq[Point]` that also reads private fields, is the helper allowed too?
  - If authorization is tied to the instance, the answer depends on who instantiated it first.
  - If the helper is instantiated both through derive and directly by user code, the two must not share an authorized instance.

  Recommendation: authorization applies to the derived function and to calls *inside the derive package* that it makes, with the same `T`. Key cached instances on authorization as well as type arguments.
- **Speed of derived code.** Replacing the compiler-made helpers in `cgen.go` and `llvmgen_runtime.go` with Lowlight generics must not slow down `eq`, `hash` or `copy` in release builds. Add a benchmark before Phase 6.6 and compare afterwards (`benchmarks/runner.ll` already exists).
- **Compile time of deriving.** Every `@derive` instantiates and unrolls generics, and std uses `@derive` widely (`Result`, `Status`, the `fs`, `net` and `http` result types). Measure the time to check std before and after.

## H17. Gaps in the verification checklist

The plan's checklist is `go vet`, `go test`, `lowlight test std/`, and both backends under ASan, UBSan and leak checks. It doesn't cover:

- **Parity between compile time and runtime** (A1). This is the single most important test for an evaluator that re-implements the language.
- **Negative tests** for each rule in the design (every "forbidden" item in §4.1 and §4.3, each restriction on const generic parameter types, each embed path rule).
- **Fuzzing.** At least run every program in `tests/` and `examples/` with `main`'s body wrapped in `comptime`, where the body is evaluable, and compare the output. This will find many hook and value-model bugs cheaply.
- **LSP speed** on files with heavy `const`s: keystroke latency with memoization on and off.
- **Checking all targets** (H5).

---

# Part 3 — Additions to the plan

## A1. A test harness for parity between compile time and runtime

Make the harness part of the test system, not a one-off script, so std's own tests use it:

- A test attribute or helper that evaluates an expression at compile time and at runtime and asserts they're equal, e.g. `assert_comptime_parity(expr)`, or a `@test(parity)` mode that runs a `const` function both ways.
- A corpus in `tests/` that covers every std/core method on strings, lists, maps, sets, ranges, chars, ints and floats. Cover edge cases: empty values, UTF-8, `NaN`, `-0.0`, `inf`, `INT_MIN`, shifts at the full width.
- Run it on both backends in CI.

## A2. A simulated heap for the evaluator

As described in H1: typed cell allocations and pointers made of an allocation and an index. Only the unsafe primitives are implemented in Go, and std/core's own `unsafe` code is allowed at compile time. This replaces most of `tryCallStdlibHook` and the std exemption.

## A3. A single model of places and references

As described in H7 and B2. Write the note, then implement it. Phase 6 builds on it.

## A4. `List._data_mut` and `string._data_mut`

As described in B4 and H9: one helper that promotes a baked buffer before any write, used by every method that writes through `data`, plus the unsafe rule in the spec.

## A5. Compile-time assertions and tests that expect a compile error

- **A top-level static assertion.** Something like `comptime assert(size_of[Header]() == 16, "Header layout changed")`, or the idiom `const _ = assert(...)`. Today you can't write an assertion at top level without a dummy binding, and size and layout checks are one of the most common uses of compile-time code.
- **Tests that expect a compile error.** For example `@test_compile_error("expected text")` on a function, or a `tests/` helper. Without it, `compile_error` and every coloring rule can only be tested from Go. Library authors (std/derive, user `@derivable` packages) need it to test their own error messages.

## A6. `lowlight check --all-targets`

As described in H5, and run in CI over std.

## A7. Real memory accounting, a total budget, and a time limit for the LSP

As described in B5 and H8.

## A8. Structured diagnostics earlier, with readable traces

As described in H10 and B8: move 7.1 up, collapse repeated frames, point the main span at user code, and don't list runtime callers as frames.

## A9. Limits on unrolling and instantiation

As described in H15.

## A10. Caching `const` values per package

As described in H14.

## A11. Move `lowlight expand` earlier

`lowlight expand` (Phase 7.2) is the best debugging tool for Phases 4–6: it shows what `comptime if`, `comptime for` and `@derive` actually produced. Build a basic version, without LSP integration, right after Phase 4.4. Then extend it as each later phase lands.

## A12. Fold finished stages into the spec as you go

The plan folds the design into SPECIFICATION.md only in Phase 8. By then the design doc and the code will have drifted, as B6 shows: what was built differs from §3.1. The project's practice has been to update the spec with each feature.

**Recommendation:** fold §1 (bindings), §2.1, §3.1 and §4 into SPECIFICATION.md now, with the corrections from this review (H2, H3, H11). Fold the rest as each phase lands. Keep COMPTIME_DESIGN.md as the record of decisions only.

## A13. Show const-ness in the tools

- **Hover:** say whether a function can be called at compile time and, for a generic instance, why not (the reason from H2).
- **docgen:** mark `const` functions in the std docs.
- **Completion:** inside a `const` or `comptime` context, rank callable functions first, or dim the others.

## A14. Benchmark the evaluator and shrink `ctVal`

The speed measured is about 1.5 µs per step. `ctVal` has about 25 fields (about 230 bytes), and most values use two or three of them.

**Recommendation:** add evaluator benchmarks to `benchmarks/` (a CRC table, Unicode-style range tables, map-heavy code, string building). Then consider one of:

- a tagged-union representation: a small header plus a pointer to data specific to each kind;
- separate Go types behind an interface.

Do this after A2, which changes what values exist.

## A15. A test that ASan catches writes to baked data

B4 died without a message. Add a test that deliberately writes to a baked buffer from `unsafe` code and asserts the run fails *loudly*, with ASan or a fatal-signal message. That way a future CoW gap shows up as a clear failure, not exit code 255.

---

# Suggested order of work

**Before Phase 4:**

1. Fix B1 and add the Go recursion guards. This crash is easy to trigger.
2. Write the place and reference model (H7 / A3). That fixes B2 properly.
3. Decide on H1: build the simulated heap (A2), or keep the hooks and build the parity harness (A1).
   - Either way, add A1. It's needed in both cases.
   - Under A2, B3 and the effects of B2 on std methods go away with the hooks.
   - Under the hook approach, fix B3 by interpreting `format_float_prec`.
4. A4 / B4: the `_data_mut` helper and the check of every `.data` writer. Add A15.
5. Make the language decisions: H2 (conditional constness), H3 (removing the std exemption), and H4 (`import` under `comptime if`). Then fix B6 to match.
6. Small fixes: B5, B7, B8 and B9.
7. Move `Diagnostic.Related` (7.1) here (H10 / A8).
8. Fold the finished parts into SPECIFICATION.md (A12).

**Then:** Phase 4 with `target` (5.1) moved ahead of 4.3 (H6), plus A9, a basic `lowlight expand` (A11) and A5. Then the rest of Phase 5 with A6, then Phase 6 with H16's benchmarks and authorization rule. The LSP and budget work (A7, A13) goes with Phase 7 as planned.

---

# Appendix — Repro programs

To run one, build the compiler, put a link to `std/` next to the binary, and use `lowlight run --backend llvm|c11 file.ll`.

**B1 — compiler crash**

```lowlight
const opt = fn() string: "${[3, 1]}"
bind main = fn(): println(comptime opt())
```

**B2 — `&` writes lost, and the effects on std**

```lowlight
const bump = fn(n: &int): { n += 1 }
const f = fn() int: {
	let a = 1
	bump(&a)
	a
}
const chars = fn() int: "héllo".chars().len()      // hits the step limit
const upd = fn() int: {
	let m = #["b": 2]
	let _ = m.update("b", cl(v: &int): { v += 100 })
	m["b"] ?? 0                                      // 2; runtime gives 102
}
bind main = fn(): println("${comptime f()} ${comptime upd()}")   // "1 2"; expected "2 102"
```

**B3 — float formatting**

```lowlight
const s = fn() string: "${0.1 + 0.2} ${f32(0.1) + f32(0.2)} ${1e300 * 1e10}"
bind main = fn(): {
	println(comptime s())                                    // 0.30000000000000004 0.30000001192092896 +Inf
	println("${0.1 + 0.2} ${f32(0.1) + f32(0.2)} ${1e300 * 1e10}")  // 0.3 0.3 inf
}
```

**B4 — writing to read-only memory**

```lowlight
import "std/sort"
bind main = fn(): {
	let xs = comptime [3, 1, 2]
	sort.sort(&xs)            // exit 255, no output
	println(xs)
}
```

**B6 — const generic used at runtime**

```lowlight
define U struct [n: int]
bind U.to_string = fn(self) string: "U${self.n}"
const show[T: Printable] = fn(x: T) string: "<${x.to_string()}>"
bind main = fn(): println(show(U[n: 3]))   // compile error, though nothing runs at compile time
```

**B7 — digit separators**

```lowlight
@comptime_limit(50_000_000)
const big = fn() int: 1
bind main = fn(): println(comptime big())
```

**B8 / B9 — trace and cycle messages**

```lowlight
const deep = fn(n: int) int: if(n == 0): 0 else: deep(n - 1) + 1
const DEEP = deep(100000)      // 256 identical note lines
const A = B + 1
const B = A + 1                // "top-level bind constant", cycle not named
bind main = fn(): println(A)
```

---

# Part 4 — Second review, after Phase 3.5

**Reviewed:** 2026-11-24, after the fixes for Parts 1–3. The fixes replaced the std hooks with a simulated heap and added conditional `const` generics, the `comp` and `type` namespaces, `Diagnostic.Related`, and `_data_mut`.

## What was checked

- `go build` and `go vet ./internal/...` pass, and so does `go test ./internal/...`.
- `go test ./tests/` passes except `TestStandardLibraryInlineSuites/{llvm,c11}/json` (the `string.to_json` case), which also fails at HEAD.
- I re-ran every repro from the appendix and from the first review's probe programs.

## Fixed

All of B1–B9 are fixed:

- **B1, B3:** list and map `to_string`, and float formatting, now match runtime exactly (`0.3 0.333333 0.3 inf 2.5`).
- **B2:** `&` writes now reach the caller: `bump(&a)`, `.chars()` on UTF-8 text, and `Map.update` with a closure that takes `&int`.
- **B4:** sorting a baked list works on both backends.
- **B5:** memory is now charged; a doubling string loop stops at 64 MB with a clear error, peaking at about 87 MB resident.
- **B6:** a `const` generic falls back to runtime-only per type, and calling it at compile time explains the reason with notes.
- **B7:** `@comptime_limit` accepts `_` digit separators.
- **B8, B9:** repeated trace frames collapse, and the cycle error names `const` and lists the whole cycle.

Also checked:

- **`@comptime_only`:** a function can't be used as a value in runtime code, and a type can't appear in a runtime signature.
- **Old names:** bare `size_of` gives a migration error pointing to `type.size_of`.
- **Layout:** `type.size_of` and `type.align_of` still match both backends.

## B10. Compile-time `Map` operations fail when a key or value is a `string`

**Reproduced.** Each of these fails at compile time with `compile-time pointer byte offset 0 out of bounds (len 1)`:

```lowlight
const a = fn() int: {
	let m = #["a": 1]
	let _ = m.remove("a")          // string key
	m.len()
}
const b = fn() int: {
	let m = #[1: "x"]
	let _ = m.remove(1)            // string value
	m.len()
}
const c = fn() int: {
	let m = #["a": "x"]
	m.insert("a", "y")             // overwriting a string value
	m.len()
}
const d = fn() int: {
	let m = Map[string, int].new()
	m.push(MapEntry[string, int][key: "k", value: 1])   // string key through push
	m.len()
}
bind main = fn(): println(comptime a())
```

**These work:**

- `Map[int, int]` and `Map[int, [int]]` with `remove` and `push`;
- `Map[string, string]` with `insert` of a new key, `clear` and `update`;
- `Map[string, int]` with `get_or_insert` and `entries`;
- `Set[string]` with `remove` and `clear`;
- `List[string].must_remove`.

**Likely cause.** Every failing path does `ptr_read(addr_of(<place>.<field>))` on a field whose type is `string`:

| Function | Line in `std/core/map.ll` |
| :--- | :--- |
| `Map.remove` | `let _ = ptr_read(addr_of(self.entries[idx].key))` (and `.val` on the line before) |
| `Map.insert`, when the key already exists | `let vp = addr_of(self.entries[existing].val)` followed by `ptr_read(vp)` |
| `Map.push` | `ptr_read(addr_of(entry.key))` and `ptr_read(addr_of(entry.value))` |

The error's `len 1` matches the one-byte strings in the repro (`"a"`, `"x"`, `"k"`).

So in the simulated heap, `addr_of` on a `string`-typed place seems to return a pointer into the string's byte buffer, not a pointer to the 24-byte `{ data, len, cap }` value stored in the field. The following `ptr_read` then reads a 24-byte `string` from offset 0 of a 1-byte buffer.

The `[int]` field works because lists are stored as cell arrays, not byte buffers, so there's no separate byte buffer to point into by mistake.

**Where to look.** `evalPlace` / `addr_of` handling in `internal/sema/comptime.go`, and the byte-buffer branch of pointer places around the `compile-time pointer byte offset %d out of bounds` check. `addr_of` on a place must point at the place itself, whatever the field's type.

**Why the parity test missed it.** `tests/comptime_parity_test.go` uses `Map[string, int]` only with `update`, and never `remove`, `push`, or `insert` over an existing key, and never a `string` value. Add those cases, with `string` both as key and as value. Add the same operations on a struct field of type `string` reached through `addr_of`, since user `@derive` code in Phase 6 will do the same thing.

**Impact.** `Map` is the most common intermediate structure in compile-time table builders, and `string` keys are the most common keys. Phase 4's `comptime for` and Phase 6's `std/derive` will hit this path immediately: `Map.push` is how map comprehensions collect.

## B11. The memory budget counts far more than a program actually uses

**Reproduced.**

```lowlight
@comptime_limit(50_000_000)
const big = fn() int: {
	let xs: [int] = []
	for(i in 0..200000): xs.push(i * 3)
	let m = Map[int, int].new()
	for(i in 0..20000): m.insert(i, i)
	xs.len() + m.len()
}
const BIG = big()
bind main = fn(): println(BIG)
// error: compile-time execution exceeded memory budget (64 MB)
// (aborted inside List.reserve, std/core/list.ll:94)
```

At runtime this data takes about 2 MB: 200k `int`s is 1.6 MB, plus a 20k-entry map.

**Cause** (`internal/sema/comptime.go`):

1. **Every heap cell costs 232 bytes.** `ctValMemCost = 232` is charged per cell (`alloc` at about line 1301, slicing at about 6514). That's the size of a Go `ctVal`, not of the Lowlight value: an `int` cell costs 29× its real 8 bytes.
2. **Freed memory is never credited back.** `allocMem` only adds to `s.memBytes`; nothing subtracts on `free_raw` or `realloc_raw`. When a `List` grows by doubling, each old buffer stays charged after it's freed. A list that ends with n slots has been charged for about 2n.
3. **The budget can't be raised.** `@comptime_limit` raises only the step budget, so a program that legitimately needs more memory has no way to ask for it.

So any compile-time list of more than about 140k elements fails, whatever `@comptime_limit` says. That blocks the CRC and Unicode tables planned for Phase 8.

**Fix:**

- Credit the budget on `free_raw`, and on `realloc_raw` for the old size.
- Charge the target's size for each cell (`type.size_of` of the element type), so the 64 MB limit means 64 MB of Lowlight data, as the design says. Or keep charging Go-side cost, but say so in the spec and raise the default to match.
- Let `@comptime_limit` raise the memory budget, either with a second argument or a named form such as `@comptime_limit(steps: N, memory: M)`. **That's a syntax decision for you.**
- Add a test that builds a 200k-element list and a 50k-entry map within the default budget.

## B12. Errors for conditional generics show internal mangled names

**Reproduced.**

```lowlight
define U struct [n: int]
bind U.to_string = fn(self) string: "U${self.n}"
const show[T: Printable] = fn(x: T) string: "<${x.to_string()}>"
const V = show(U[n: 3])
bind main = fn(): println(V)
```

The error says `cannot call generic instantiation "show[ll_s_U]" …`, and the hint and notes repeat `show[ll_s_U]`. The message is built from `fi.Name` (`internal/sema/check_calls.go:60`), which holds the mangled name. Use the name users write (`show[U]`), as type mismatch messages already do.

## Docs that were out of sync

These were fixed in the same pass as this write-up:

- **TODO.md:**
  - Phase 3.5's items were unchecked, though the plan marks Phase 3.5 done.
  - TODO used a `_meta` namespace, while the code, plan, design and spec all use `comp` (`comp.compile_error`, `comp.log`, `comp.TARGET`).
  - Phase 2's entries still said call-depth `256`, "evaluator hooks", and plain `size_of`.
- **COMPTIME_DESIGN.md:**
  - The summary table and §7.1 described a shadowable prelude `const target`.
  - §3.1 described checking every generic instance strictly.
  - §4.1 said std/core runs "via built-in evaluator hooks".
  - §4.3 listed only `set`, `pop` and `truncate` for the CoW rule.
  - The reflection and embedding rows didn't use `comp.`.
- **COMPTIME_IMPLEMENTATION_PLAN.md:**
  - `comp.TARGET` was still in Phase 5, after the top-level `comptime if` that needs it (TODO had already moved it, per H6).
  - The plan lacked several items TODO had picked up from this review, such as instantiation and unroll limits, `check --all-targets`, an early `expand`, and the test helpers.
  - Phase 3.5's leftover work and B10–B12 weren't listed.
