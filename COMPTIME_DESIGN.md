# Lowlight Compile-Time Execution (`comptime`) Design Specification

**Status:** Finalized Design (2026-11-24) · Phases 1–3.5 implemented (see [`TODO.md`](./TODO.md) and [`COMPTIME_REVIEW.md`](./COMPTIME_REVIEW.md))  
**Companion Roadmap:** [`TODO.md`](./TODO.md) (Section 1)

This document records every finalized language design decision for compile-time execution (`comptime`), `const` bindings and parameters, type reflection, `@derive` / `@derivable`, `target` introspection, file embedding, diagnostics, and tooling in Lowlight. Once each milestone lands in the compiler, the normative sections below fold directly into [`SPECIFICATION.md`](./SPECIFICATION.md), [`CONVENTIONS.md`](./CONVENTIONS.md), [`IMPLEMENTATION.md`](./IMPLEMENTATION.md), and the website.

---

## Summary of Design Decisions

| Area | Decision | Key Details |
| :--- | :--- | :--- |
| **Core Model** | Values + code unrolling only | No first-class `type` values and no `comptime for` declaration synthesis. Generics stay `[T: Bounds]` with nominal `define` types. Top-level `comptime if` supports conditional declaration inclusion (`extern`, `define`, `const`, `bind`). |
| **Bindings (`let`/`bind`/`const`)** | Three-tier bindings | `const`: compile-time immutable `bind` everywhere (top-level & local values, `const` functions/methods, `fn` pointers). `bind`: runtime immutable (runtime globals, runtime functions/methods, immutable locals). `let`: mutable runtime local. |
| **Syntax & Triggering** | `const` + `comptime` | `comptime expr`, `comptime { ... }`, `comptime if`, `comptime for` (mirroring `unsafe`). Array sizes (`[type.size_of[T](): u8]`) and enum discriminants evaluate at compile time implicitly. |
| **Function Eligibility** | Opt-in `const` coloring + conditional generics + `@comptime_only` | Only `const` functions/methods run in `const`/`comptime` (validated at definition time; no `@const_eval` needed). Generic `const` fns/methods & `@derive` methods are conditionally `const`. `@comptime_only` restricts compile-time helpers/descriptors/types with zero runtime emission. |
| **Comptime Parameters** | `[N: const Type]` in brackets | Live in `[...]` alongside `[T: Bound]`. Allowed types: integers (`int`, `i8`–`i64`, `u8`–`u64`), `bool`, `char`, integer tag `enum`s, and `string` (syntax slated for future revisit). |
| **Evaluation Subset** | Pure Lowlight + simulated heap for `std/` `unsafe` | Real Lowlight `std/core` (`string`, `List[T]`, `Map[K, V]`, `Set[T]`, `Range`, `Array`, `Error`, `Result`), `Drop`, structs, enums, `Option`, non-closure `fn`, loops, recursion; user `unsafe`, `extern`, `syscall`, and host I/O are forbidden. |
| **Namespaces** | `comp.*` & `type.*` | `@comptime_only` `comp.*` namespace (`comp.compile_error`, `comp.log`, `comp.TARGET`, `comp.embed_*`, `comp.*_of`, `comp.TypeKind`, ...) vs always-available `const` `type.*` layout queries (`type.size_of`, `type.align_of`, `type.is_plain`). |
| **Static Crossing** | `Plain` + `string` + `List[T]` | Values escaping `const`/`comptime` to runtime must be `Plain`, `string`, `List[T]` (baked in `.rodata` with `cap = 0`), or composites of them (no non-`none` boxed optionals). `Map`/`Set`/custom `Drop`/`@comptime_only` types/fns stay internal to comptime. |
| **Budgets & Traps** | Default budgets + `@comptime_limit` | Step (`1_000_000`), depth (`1024`), and memory (`64 MB`) limits with `@comptime_limit(steps: N, memory: M)` (or `@comptime_limit(N)`) override. Wrapping arithmetic and IEEE-754 floats match runtime; traps (`panic`, `assert`, `/ 0`, bad shifts, OOB, UAF) become compile errors with traces; `comp.compile_error(msg)` emits clean user errors and `comp.log(val)` emits notes. |
| **Reflection API** | Typed `comp.*_of[T]()` descriptors | `comp.fields_of[T]()`, `comp.variants_of[T]()`, `comp.elems_of[T]()`, `comp.kind_of[T]()`, `comp.type_name[T]()`, `comp.implements[T, I]()` returning `comp.FieldInfo`, `comp.VariantInfo`, `comp.ElemInfo`, `comp.TypeKind`, `comp.AttrInfo`. |
| **Privacy (`_`)** | Enforced by default; `@derive` opt-in | `f.is_public` is readable everywhere, while accessing/constructing `_`-private fields/variants outside `T`'s package is an error unless authorized by `@derive(...)` on `T`'s declaration. |
| **Access & Construction** | `v.field(f)` / `t.elem(e)` + collecting `comptime for` + variant intrinsics | Struct/tuple reading, `&` borrowing, and assignment via `v.field(f)` / `t.elem(e)`; construction via collecting `comptime for(...) T:`; enums/results via `variant_index`, `is_variant`, `payload` / `as_variant`, `make_variant`. |
| **`@derive` & Tuples** | `@derivable(...)` + `std/derive` + tuple generics | Interfaces declare `@derivable(method: generic_fn, ...)`. `@derive(I)` attaches `const`/`bind Type.method = impl[Type]`. Tuples get Lowlight `eq`/`cmp`/`hash`/`copy`/`to_string` over `comp.elems_of[T]()`. |
| **Target & Platform Code** | `comp.TARGET` + single-file `comptime if` | `@comptime_only` `comp.TARGET: comp.Target` (not shadowable) with `comp.TargetOs`, `comp.TargetArch`, `comp.Endian` enums, `is_unix`, `ptr_size`, `ptr_bits`, `triple`. Platform `std` code uses single-file `comptime if` everywhere (including top-level declarations; `import` is not allowed there). |
| **File Embedding** | `comp.embed_str` & `comp.embed_bytes` | `@comptime_only` functions in the `comp` namespace returning static `string` / `[u8]` (`cap = 0`), relative to declaring `.ll` file, confined to package/project root. |
| **Diagnostics & Tooling** | `Related` traces, `expand`, bounded LSP | `diag.Diagnostic.Related` frames with `@track_caller`. Post-comptime `.o` hashing stays automatic; LSP runs bounded comptime with `sema` memoization + hover/expand; `lowlight expand` prints unrolled Lowlight. |

---

## 1. Bindings (`let`, `bind`, `const`) & Function Values

Lowlight separates **mutability** and **evaluation phase** across three binding keywords:

| Keyword | Scope | Mutability | Evaluation Phase | Allowed Initializers |
| :--- | :--- | :--- | :--- | :--- |
| `let` | Local only | Mutable | Runtime | Any value expression |
| `bind` | Top-level & local | Deeply immutable | Runtime (simple literals may fold) | Any value expression, `fn`, `cl`, runtime-initialized globals |
| `const` | Top-level & local | Deeply immutable | **Compile time (required)** | Comptime-evaluable expressions, `const` calls, `fn(...)` literals |

### Rules

1. **`const` requires compile-time evaluation.** Every `const NAME = expr` (at top level or inside a function) must evaluate completely during semantic analysis. If evaluation fails (calls a `bind`-only function, uses `unsafe`/`extern`, hits a trap, or returns a non-static-crossable type), compilation fails with an error.
2. **Top-level `bind` stays runtime-capable.** Top-level `bind` declarations keep pre-`main` initialization and reverse-order exit `Drop` semantics for runtime values and globals, and remain valid for runtime-only functions (`bind foo = fn(...)`) and methods (`bind Type.m = fn(...)`).
3. **Function literals (`fn` vs `cl`) with `const`:**
   * An `fn(...)` literal captures nothing and lowers to a static code pointer. Therefore `const` works naturally for top-level functions (`const add = fn(a: int, b: int) int: a + b`), generic functions (`const id[T] = fn(x: T) T: x`), methods (`const Point.len = fn(self) float: ...`), local compile-time helpers, and compile-time dispatch tables (`const OPS: [2: fn(int) int] = [...]`).
   * A `cl(...)` closure owns captures as a `Resource` and **cannot** initialize a `const` (compile error, hinting `fn` if capture-free or `bind` if runtime captures are required).
4. **Topological dependency order:** Top-level `const` (and `bind`) declarations remain order-independent and topologically sorted across files in a package and across imported packages. Dependency cycles between `const`s are compile errors naming the cycle.

```lowlight
const CRC_POLY: u32 = 0xEDB88320
const CRC_TABLE: [256: u32] = _build_crc_table(CRC_POLY)

const OPS: [2: fn(int) int] = [
	fn(x: int) int: x + 1,
	fn(x: int) int: x * 2,
]

bind runtime_err = Error.coded("not_found", "missing item") // runtime global stays `bind`

bind main = fn(): {
	const SCALE = OPS[1](4) // 8, computed at compile time
	let count = 0           // mutable runtime
	bind limit = SCALE + 1  // immutable runtime
}
```

---

## 2. `comptime` Expressions, Blocks, & Control Flow

`comptime` is a reserved keyword (32 total keywords with `const` and `comptime`) that explicitly marks **compile-time evaluation or unrolling**, mirroring `unsafe` in syntax and visual clarity.

### 2.1 `comptime expr` and `comptime { ... }`
* Prefix expression operator at the same precedence tier as `unsafe expr`.
* Forces compile-time evaluation of `expr` or the block `{ ... }` anywhere an expression is expected (inside a runtime function, as a call argument, inside a `bind`, etc.).
* Inside `comptime { ... }`, local `let` variables, loops, `if`/`match`, and `const` function calls run inside the compile-time evaluator; the resulting value is baked into the surrounding runtime expression (or rejected if it cannot cross to runtime).
* Inside a `const` initializer, `const fn` / `@comptime_only` body, `[N: T]` size expression, or enum tag expression, compile-time evaluation is already active, so nested `comptime` is optional.

```lowlight
bind hash_tag = fn() u64: comptime {
	let h: u64 = 14695981039346656037
	for(b in "HeaderV1".bytes()): h = (h ^ u64(b)) * 1099511628211
	h
}
```

### 2.2 `comptime if` (Expression, Statement, & Top-Level)
* Syntax: `comptime if(cond): body [else: body2]` and chained `else comptime if(cond2): ...`. In expression position, `comptime if` requires an `else` branch and never silently produces `?T` (statement/block form may omit `else`).
* `cond` must evaluate at compile time to `bool`.
* **Dead-branch pruning:** Only the selected branch is typechecked, checked for ownership/moves/`unsafe`, and lowered to MIR. The untaken branch must still parse cleanly, allowing platform-specific `extern`s, `syscall`s, or reflection accesses (`v.field(f)`) to live safely behind `comptime if` guards.
* **Top-level `comptime if`:** Permitted at file top level wrapping `extern`, `define`, `const`, and `bind` (or grouped inside `{ ... }` declaration blocks at top level under `comptime if`; `import` is forbidden inside top-level `comptime if` so the package graph stays static and cycle-free). Declarations inside an untaken top-level branch are excluded from the package's symbol and type tables.

```lowlight
comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	extern "unistd.h"
	extern _NSGetExecutablePath: fn(buf: ptr[u8], size: ptr[u32]) i32
} else comptime if(comp.TARGET.os == comp.TargetOs.linux): {
	const _SYS_READLINKAT = comptime match(comp.TARGET.arch):
		when comp.TargetArch.amd64: 267
		when comp.TargetArch.arm64: 78
		default: comp.compile_error("unsupported Linux arch: ${comp.TARGET.arch}")
}
```

### 2.3 `comptime for` (Unrolling & Collecting)
* Syntax: `comptime for(item in seq): body` or `comptime for(idx, item in seq): body`, plus collecting form `comptime for(item in seq) TargetType: body`.
* `seq` must be a compile-time iterable sequence (a `[N: T]` array, `[T]` list, `Range[int]`/`Range[char]`, or tuple/reflection list from `comp.fields_of[T]()`, `comp.variants_of[T]()`, `comp.elems_of[T]()`).
* **Unrolling semantics:** Unrolls `len(seq)` iterations at compile time during semantic analysis. Each unrolled iteration binds `item` (and `idx`) as compile-time constants—meaning `item` may hold a compile-time-only descriptor (`FieldInfo`, `VariantInfo`, `ElemInfo`) whose underlying field/variant type differs on every iteration.
* Inside an unrolled iteration, `next <-` skips that iteration's body (or falls back to the field's default/`none` in collecting struct construction if `f.is_opt or f.has_default`). `for <-` exits unrolling early; labeled unwinds (`outer <-`) work normally across the unrolled blocks.
* **Runtime `const` sequences vs unrolling:** A normal `for(x in table)` inside a `const` function executes as a loop inside the comptime interpreter; `comptime for(...)` *unrolls* into inline statements/expressions (and is required when iterating over heterogeneous `FieldInfo`/`VariantInfo`/`ElemInfo` lists or inside runtime code).

### 2.4 `comptime match`
* Syntax: `comptime match(expr): when pat: ... default: ...`
* Evaluates `expr` at compile time, selects the matching `when`/`default` arm, and typechecks/lowers **only** that selected arm (mirroring `comptime if` for multi-way enums like `TargetOs`, `TargetArch`, `TypeKind`).

---

## 3. Function Eligibility & Const Generic Parameters

### 3.1 Function Coloring (`const` vs `bind` vs `@comptime_only`)
* **Opt-in `const` coloring:** Only functions and methods declared with `const` (and universe `const` builtins) may be called from `const` initializers, `comptime` expressions, `[N: T]` sizes, or other `const` / `@comptime_only` functions.
* **Definition-time verification:** The compiler validates every non-generic `const` function/method at its declaration to ensure it does not call `bind`-only functions, `extern`s, `syscall`, or user `unsafe` blocks.
* **Conditional generics:** A generic `const` function or method (`const show[T: Printable] = ...`, `const Box[T].show = ...`) and every `@derive` method is checked per instantiation. An instantiation whose body only uses `const` operations on its type arguments is compile-time callable; otherwise that instantiation quietly becomes runtime-only, and runtime calls to it still compile. Calling a runtime-only instantiation from a `const` or `comptime` context is an error whose notes trace the chain to the `bind` operation that caused it.
* **Calls through `fn` values:** A function value's type doesn't record whether it is `const`, so a call through an `fn` value is checked when the evaluator makes it: calling a `bind` function through a pointer at compile time is an error at that call.
* **Methods:** `const Type.method = fn(...)` and `const Type[T].method = fn(...)` are first-class method declarations identical to `bind Type.method = ...`, except compile-time-callable.
* **`@comptime_only` attribute:**
  * Applies to `const` functions and methods (`@comptime_only const _helper = fn(...)`).
  * Can only be called inside `const`, `comptime`, or another `@comptime_only` function; calling it from runtime code is a compile error.
  * Allowed to accept and return `@comptime_only` types (`comp.FieldInfo`, `comp.VariantInfo`, `comp.ElemInfo`, `comp.TypeKind`, `comp.AttrInfo`) and invoke `@comptime_only` builtins (`comp.fields_of`, `comp.variants_of`, `comp.elems_of`, `comp.compile_error`, `comp.log`) directly without wrapping the body in `comptime { ... }`. `@comptime_only` types and functions are rejected in runtime signatures, non-`@comptime_only` globals, and baked tables.
  * Stripped completely from MIR, LLVM IR, and C11 codegen (zero bytes in runtime binaries).

### 3.2 Const Generic Parameters (`[N: const Type]`)
* **Syntax:** Written in `[...]` generic lists alongside type parameters: `Name: const Type` (e.g. `define Matrix[R: const int, C: const int] struct [...]`, `const zeros[N: const int] = fn() [N: int]: ...`).
* **Allowed parameter types:**
  * Signed and unsigned integers (`int`, `i8`, `i16`, `i32`, `i64`, `u8`, `u16`, `u32`, `u64`)
  * `bool` and `char`
  * Integer-backed tag `enum`s (e.g., `Endian`, `TargetOs`)
  * `string`
  * *(Disallowed: `float`/`f32`, `fn`/`cl`, `ptr`, structs, tagged unions, `List`, `Map`, `Set`, `?T`.)*
* **Type identity & mangling:** Arguments at instantiation are evaluated at compile time first, so `Matrix[2 + 2, 4]` and `Matrix[4, 4]` denote the exact same monomorphized type. Integer/bool/char/tag-enum arguments mangle directly by value; `string` arguments mangle deterministically with a sanitized human-readable prefix plus a fixed SHA-256 hex digest tag.
* **In array lengths:** `[N: T]` accepts any `const int` expression (including generic const params like `[R * C: float]` and layout queries like `[type.size_of[T](): u8]`), validated positive (`> 0`) at monomorphization/instantiation.
* **Inference:** When passing an array `[N: T]` to a parameter `xs: [N: T]` where `N: const int` is a generic parameter, `N` is inferred automatically from the argument's array length.

```lowlight
define Matrix[R: const int, C: const int] struct [
	data: [R * C: float],
]

const zeros[N: const int] = fn() [N: int]: [N: 0]

const first_or_zero[N: const int] = fn(xs: [N: int]) int:
	comptime if(N > 0): xs[0] else: 0
```

---

## 4. Compile-Time Evaluation Subset, Static Crossing, & Budgets

### 4.1 Supported Runtime Semantics at Compile Time
* **Allowed:**
  * All scalar primitives (`int`, sized ints, `float`, `f32`, `bool`, `char`), tag & tagged `enum`s, `result` types, `struct`s, `tuple`s, `?T` optionals, `[N: T]` fixed arrays, `Range[T]`, `fn` pointers, and `const`-bounded closures created purely within comptime (`cl` that never escapes).
  * Safe control flow: `{ ... }`, `if`/`else`, `for`, `while`, `match`, result chains, `??`, and all 8 targeted unwinds (`<-`, `func <-`, `for <-`, `next <-`, labels).
  * Ownership, moves, `&` borrows, and deterministic LIFO `Drop` destructors (using the exact same Model 2A rules).
  * The real `std/core` Lowlight code for `string`, `List[T]`, `Map[K, V]`, `Set[T]`, `Range`, `Array`, `Error`, and `Result` (plus bit-level helpers like `float.hash` / `to_bits`). `std/` code's own `unsafe` blocks run on the evaluator's bounds- and lifetime-checked simulated heap, so compile-time results match runtime by construction.
  * `caller_location()`, `caller_file()`, `caller_line()`, `caller_col()` (including `@track_caller` chains).
* **Forbidden:**
  * User-written `unsafe { ... }` blocks and raw pointer intrinsics (`ptr[T]`, `alloc_raw`, `addr_of`, `ptr_cast`, etc.).
  * Calling `extern` C functions or `extern fn` pointers.
  * `syscall(...)` and any OS/network/clock/process I/O (other than explicit `comp.embed_str`/`comp.embed_bytes`).
  * Calling `bind`-only (non-`const`) functions.

### 4.2 Arithmetic, Wrapping, & Traps
* **Exact runtime parity:**
  * `int` is 64-bit (`i64`) and `float` is 64-bit (`f64`), matching [SPECIFICATION.md §3.2](file:///Users/thekingoflimbs/Developer/Lowlight/SPECIFICATION.md#L711).
  * `+`, `-`, `*`, and unary `-` on integers wrap in two's complement at the operand type's bit width (`u8`, `i32`, `u64`, etc.).
  * Literal adaptation checks range at compile time (`const B: u8 = 256` is an overflow error).
  * IEEE-754 floating-point rules apply to `float`/`f32`.
* **Traps become compile errors:** Division/modulo by zero, invalid shift counts (`< 0` or `>= width`), out-of-bounds array/list/string indexing or slicing, invalid `char(int)`, `panic(...)`, and `assert(false, ...)` immediately abort compile-time evaluation and emit a compile error with the full comptime stack trace.
* **`comp.compile_error(msg: const string) never` & `comp.log(val: Printable)`:** `@comptime_only` builtins in the `comp` namespace: `comp.compile_error` emits a clean user-facing compile error without `"panic:"` framing (respecting `@track_caller`), and `comp.log` emits a compile-time `note:` diagnostic.
* **`type.size_of[T]()`, `type.align_of[T]()`, `type.is_plain[T]()`:** `const` layout queries in the `type` namespace usable in both compile-time (`const` / `comptime`) and runtime (`bind`) code.

### 4.3 Crossing from Compile Time to Runtime
When a `const` declaration or `comptime expr` returns a value into runtime code:
* **Allowed to cross:**
  * Any `Plain` value (scalars, tag/plain enums, `[N: T]`, plain structs/tuples, flat `?T` optionals, `fn` pointers).
  * `string` — baked into `.rodata` with `cap = 0`.
  * `List[T]` — baked into `.rodata` with `cap = 0`, recursively requiring elements of `T` to be crossable.
  * Structs, tagged unions, results, tuples, and flat `?T` composed purely of crossable types (boxed recursive `?T` is allowed only when `none`).
* **Forbidden from crossing directly into runtime:**
  * `Map[K, V]`, `Set[T]`, `Arena`, `ref[T]`, `ptr[T]`, `cl`, non-`none` boxed optionals, and structs with custom `Drop` implementations. *(These remain usable as intermediate state inside `const` and `@comptime_only` helpers, then converted to `[N: T]`, `[T]`, or `string` before returning.)*
* **Copy-on-write invariant:** Because `let xs = comptime _build()` can create a runtime-mutable local owning a static `cap = 0` buffer, a list or string with `cap <= 0` doesn't own its buffer. Every write through its `data` pointer must first promote it to the heap: safe mutating methods do this (`List.set`, `pop`, `truncate`, `swap`, `_remove_at`, `_swap_remove_at`, and `std/sort`'s `sort`, `sort_by`, `stable_sort`, `stable_sort_by`, `reverse`), and `unsafe` code must get a writable pointer through `List._data_mut()` rather than reading `data` directly. `List.reserve` and `List.drop` already treat `cap <= 0` this way.

### 4.4 Execution Budgets
* Default evaluator limits: step budget (`1_000_000` interpreter steps per root evaluation), call-stack depth (`1024`), and heap allocation memory cap (`64 MB`).
* **`@comptime_limit(steps: N, memory: M)`** (or single-argument **`@comptime_limit(steps)`**) attribute on a `const` declaration or `const` function raises the step and/or byte memory budget for legitimate heavy table generators (e.g., `@comptime_limit(steps: 10_000_000, memory: 256_000_000)`).
* Exceeding any budget reports a compile error naming the active function/loop and trace.

---

## 5. Reflection & Type-Description API

All reflection types and `*_of[T]()` builtins live in the `@comptime_only` `comp` namespace (not exposed bare).

### 5.1 Core Intrinsics & Descriptor Types

```lowlight
define TypeKind enum [
	primitive_kind,
	string_kind,
	optional_kind,
	list_kind,
	array_kind,
	set_kind,
	map_kind,
	tuple_kind,
	struct_kind,
	tag_enum_kind,
	tagged_union_kind,
	result_kind,
	distinct_kind,
	interface_kind,
	fn_kind,
	cl_kind,
	extern_fn_kind,
	ptr_kind,
	ref_kind,
	arena_kind,
	range_kind,
]

define AttrInfo struct [
	name: string,
	args: [string],
]

define FieldInfo struct [
	name: string,        // exact field name (with `?` if optional, or canonical field name)
	type_name: string,
	is_opt: bool,
	is_public: bool,
	has_default: bool,
	attrs: [AttrInfo],
	// opaque compiler field identity for `v.field(f)`, `f.has_attr(...)`, `f.implements(I)`
]

define VariantInfo struct [
	name: string,
	index: int,          // 0..n-1 declaration index
	tag_value?: ?int,    // present for tag_enum
	has_payload: bool,
	is_public: bool,
	attrs: [AttrInfo],
	// opaque compiler variant identity for `is_variant`, `payload`, `make_variant`, `v.payload_implements(I)`
]

define ElemInfo struct [
	index: int,
	type_name: string,
	// opaque compiler element identity for `t.elem(e)`, `e.implements(I)`
]
```

| Builtin | Meaning |
| :--- | :--- |
| `comp.type_name[T]() string` | Human-readable type name (e.g. `"Point"`, `"List[int]"`). |
| `comp.kind_of[T]() comp.TypeKind` | Classification of `T`. |
| `comp.fields_of[T]() [comp.FieldInfo]` | Struct fields in declaration order (compile error if `T` is not a struct). |
| `comp.variants_of[T]() [comp.VariantInfo]` | Enum or `result` variants in declaration order (compile error if not enum/result). |
| `comp.elems_of[T]() [comp.ElemInfo]` | Elements of a tuple `+[A, B, ...]` or `[N: T]` array. |
| `comp.implements[T, I]() bool` | Reports whether `T` satisfies interface `I` (also `f.implements(I)`, `vr.payload_implements(I)`, `e.implements(I)`). |
| `comp.attrs_of[T]() [comp.AttrInfo]` | Attributes attached to declaration `T` (`f.has_attr(name)`, `f.attr(name)` on descriptors). |

*(All `FieldInfo` / `VariantInfo` / `ElemInfo` methods like `.has_attr(...)`, `.attr(...)`, `.implements(I)`, and `.payload_implements(I)` are `@comptime_only`.)*

### 5.2 Visibility (`_`) & `@derive` Authorization
* `comp.fields_of[T]()` and `comp.variants_of[T]()` list all fields/variants (including private `_` ones, with `is_public == false`) so generic tools can check `f.is_public` or emit clear errors.
* Outside `T`'s declaring package, reading, borrowing, mutating, or constructing a private `_` field or variant via `v.field(f)`, `v.payload(vr)`, or `make_variant[T](vr, ...)` is a **compile error**—**except** inside a generic function/method instantiated by `@derive(...)` on `T`'s own declaration, where `T`'s package explicitly granted access.

### 5.3 Generic Access & Construction
* **Struct fields (`v.field(f)`):**
  * Accepts a compile-time `FieldInfo` (or `const string` field name).
  * Works in read (`v.field(f)`), mutable borrow (`&v.field(f)`), assignment (`v.field(f) = val`), and move (`own v.field(f)` when `v` is an `own self` or movable place) contexts, resolving to the exact concrete field type at unroll time.
* **Tuple / array elements (`t.elem(e)`):**
  * Accepts a compile-time `ElemInfo` (or `const int` index). Supports read, `&` borrow, assignment, and move just like `t.0` / `arr[i]`.
* **Struct & tuple construction (`comptime for(...) T:`):**
  * Collecting `comptime for(f in comp.fields_of[T]()) T: expr` unrolls into `T[f_0: expr_0, f_1: expr_1, ...]` (and `comptime for(e in comp.elems_of[T]()) T: expr` unrolls into `+[expr_0, expr_1, ...]`).
  * `next <-` inside a struct-collecting `comptime for` uses the field's default or `none` if `f.is_opt or f.has_default`, and is a compile error for required fields.
  * Early `func <-` unwinds cleanly drop any previously initialized Resource fields in LIFO order.
* **Enum & result variant operations:**
  * `variant_index(v) int` — returns `0..n-1` declaration index of the active variant.
  * `v.is_variant(vr) bool` — checks whether `v` holds variant `vr`.
  * `v.payload(vr)` — accesses `vr`'s payload (borrows read-only on borrowed `v`, moves on `own v`; compile error if `not vr.has_payload`; runtime trap if `v` does not hold `vr`). Also supports `if(v.as_variant(vr) as p):` narrowing syntax.
  * `make_variant[T](vr)` / `make_variant[T](vr, own payload)` — constructs variant `vr` of `T`.

---

## 6. `@derivable`, `std/derive`, & Tuple Generics

### 6.1 Interface-Declared Derivations (`@derivable`)
Any `interface` (in `std/core` or a user package) may specify generic derivation implementations via `@derivable`:

```lowlight
// In std/core/interfaces.ll (linked to std/derive):
@lang("equatable")
@derivable(eq: derive.eq)
define Equatable interface [
	eq: fn(self, other: Self) bool,
]

// In a user package (e.g. std/json):
@derivable(to_json: derive_to_json)
define ToJson interface [
	to_json: fn(self) Json,
]
```

### 6.2 `@derive(...)` Semantics
* Writing `@derive(Equatable, json.ToJson)` on a `struct` or `enum` (`result`) attaches `Type.method = impl[Type]` (e.g. `Point.eq = derive.eq[Point]`, `Point.to_json = json.derive_to_json[Point]`) with `@derive` private-field authorization.
* **`const` vs `bind` attachment:** The compiler binds the derived method as `const` when every field/variant operation invoked during unrolling is `const`, and falls back to `bind` if any field relies on a runtime `bind` implementation.
* **Generic types:** On `define Box[T] struct [item: T]` with `@derive(Equatable)`, `Box[U].eq` exists iff `derive.eq[Box[U]]` succeeds for concrete `U` (e.g. `Box[int]` gets `eq`, `Box[File]` does not; `SPECIFICATION.md` §12.2 rule 2).
* **Hand-written conflict:** Defining both `@derive(I)` and a hand-written method of `I` on the same type remains a compile error.
* **Built-in `std/derive` & tuples:** `Equatable`, `Comparable`, `Hashable`, `Copyable`, and `Printable` implementations for structs, enums, and results live in `std/derive` in pure Lowlight (eliminating [`internal/sema/derive.go`](file:///Users/thekingoflimbs/Developer/Lowlight/internal/sema/derive.go)). Tuple `eq`, `cmp`, `hash`, `copy`, and `to_string` move to Lowlight generics over `comp.elems_of[T]()` (and optionals to ordinary generic Lowlight code), removing backend helper synthesis in [`cgen.go`](file:///Users/thekingoflimbs/Developer/Lowlight/internal/codegen/cgen.go) and [`llvmgen_runtime.go`](file:///Users/thekingoflimbs/Developer/Lowlight/internal/codegen/llvmgen_runtime.go).

---

## 7. `comp.TARGET` Introspection & Platform Selection

### 7.1 `comp.TARGET` Description
The `@comptime_only` `comp` namespace defines `comp.TARGET: comp.Target`, populated from the resolved target triple (`--target`, `[build].target`, or host). It lives in a namespace rather than the prelude, so a local named `target` can't shadow it. The types below are declared in `std/core` and reached as `comp.TargetOs`, `comp.TargetArch`, `comp.Endian`, and `comp.Target`:

```lowlight
define TargetOs enum [
	linux,
	darwin,
	windows,
	freebsd,
	openbsd,
	netbsd,
	wasm,
	unknown,
]

define TargetArch enum [
	amd64,
	arm64,
	riscv64,
	x86,
	wasm32,
	wasm64,
	unknown,
]

define Endian enum [
	little,
	big,
]

define Target struct [
	os: TargetOs,
	arch: TargetArch,
	endian: Endian,
	is_unix: bool,
	ptr_size: int,   // 4 or 8
	ptr_bits: int,   // 32 or 64
	triple: string,  // canonical target triple string
]
```

### 7.2 Single-File `comptime if` Platform Organization
* Platform-specific standard library code (including the native OS `syscall` / `libSystem` / Win32 layer) uses **single-file `comptime if(comp.TARGET...)`** everywhere—both at top level (wrapping `extern`, `define`, `const`, `bind`) and inside functions—so all platform variants of a module live together with zero per-arch file duplication.

---

## 8. File Embedding (`comp.embed_str` & `comp.embed_bytes`)

Two `@comptime_only` functions in the `comp` namespace embed project assets at compile time:
* `comp.embed_str(path: const string) string`: reads the file, validates UTF-8 (compile error if invalid), and returns a static `string` (`cap = 0`).
* `comp.embed_bytes(path: const string) [u8]`: reads raw bytes and returns a static `[u8]` (`cap = 0`).

### Path & Caching Rules
1. `path` resolves relative to the directory of the `.ll` file containing the call.
2. **Sandbox confinement:** `path` must not be absolute and must resolve inside the declaring package's directory (or enclosing `lowlight.toml` project root); `..` traversal escaping the package/project root is a compile error.
3. **Incremental invalidation:** Embedded file contents are baked into post-lowering package source (`pu.Source`), and `sema`/LSP track embedded file paths & hashes so edits to embedded assets trigger re-checking and `.o` cache invalidation automatically.

---

## 9. Diagnostics, Expansion, Caching, & LSP

### 9.1 Structured Error Traces (`Related` + `@track_caller`)
* [`diag.Diagnostic`](file:///Users/thekingoflimbs/Developer/Lowlight/internal/diag/diag.go#L49-L60) gains `Related []RelatedInfo` (`Span`, `Message`), rendered as `= note:` lines in the CLI and `DiagnosticRelatedInformation` in the LSP.
* Traps and errors inside `const` / `comptime` evaluation report the primary underline at the failing expression with `Related` trace frames walking back to the root `const`, `comptime`, or generic instantiation site.
* Calls through `@track_caller` helpers (`assert`, `comp.compile_error`, `List.must_*`, and `@derive` entrypoints) lift the primary span to the caller / `@derive(...)` attribute while keeping the inner site in `Related`.

### 9.2 Incremental Compilation, `lowlight expand`, & LSP
* **Two-phase `.o` cache:** [`computeObjCacheKey`](file:///Users/thekingoflimbs/Developer/Lowlight/internal/driver/incremental.go#L59-L71) already hashes post-comptime `pu.Source`, automatically invalidating `.o` caches on `const`, `comp.TARGET`, `comptime if`, `comptime for`, or `comp.embed_*` changes.
* **`lowlight expand` CLI:** `lowlight expand [-p <pkg>] [<path>]` prints readable Lowlight showing evaluated `const`s, selected `comptime if` branches, unrolled `comptime for` loops, and `@derive`-attached method instantiations.
* **LSP (`CheckForIDE`):**
  * Runs full comptime evaluation on the active target (with optional target override in LSP settings / `lowlight.toml`) so `const` traps, `comptime for` errors, and `[N: const int]` mismatches surface live in the editor.
  * Uses a **tight IDE budget** (e.g., `100_000` steps / short timeout warning instead of hanging on mid-edit infinite loops or `@comptime_limit` builders) and **memoizes** `const` / `embed_*` results across keystrokes.
  * **Hover & Expand:** Hovering a `const`, `comptime`, or layout query shows its evaluated constant value/type; hovering `@derive(...)` or invoking expand shows the unrolled Lowlight expansion.

---

## 10. End-to-End Illustrative Examples

### 10.1 Struct & Enum `@derive` in `std/derive`

```lowlight
// std/derive/derive.ll
@track_caller
const eq[T] = fn(a: T, b: T) bool: {
	comptime match(comp.kind_of[T]()):
		when comp.TypeKind.struct_kind: {
			comptime for(f in comp.fields_of[T]()): {
				comptime if(not f.implements(Equatable)):
					comp.compile_error("cannot derive `Equatable` for `${comp.type_name[T]()}`: field `${f.name}` does not implement `Equatable`")
				if(a.field(f) != b.field(f)): func <- false
			}
			true
		}
		when comp.TypeKind.tag_enum_kind: int(a) == int(b)
		when comp.TypeKind.tagged_union_kind, comp.TypeKind.result_kind: {
			if(variant_index(a) != variant_index(b)): func <- false
			comptime for(vr in comp.variants_of[T]()): {
				if(a.is_variant(vr)): {
					comptime if(vr.has_payload): func <- a.payload(vr) == b.payload(vr)
					else: func <- true
				}
			}
			false
		}
		when comp.TypeKind.tuple_kind: {
			comptime for(e in comp.elems_of[T]()): {
				if(a.elem(e) != b.elem(e)): func <- false
			}
			true
		}
		default: comp.compile_error("`derive.eq` does not support `${comp.type_name[T]()}`")
}

@track_caller
const copy[T] = fn(v: T) T: {
	comptime match(comp.kind_of[T]()):
		when comp.TypeKind.struct_kind:
			comptime for(f in comp.fields_of[T]()) T: v.field(f).copy()
		when comp.TypeKind.tuple_kind:
			comptime for(e in comp.elems_of[T]()) T: v.elem(e).copy()
		when comp.TypeKind.tag_enum_kind: v
		when comp.TypeKind.tagged_union_kind, comp.TypeKind.result_kind: {
			comptime for(vr in comp.variants_of[T]()): {
				if(v.is_variant(vr)): {
					comptime if(vr.has_payload): func <- make_variant[T](vr, own v.payload(vr).copy())
					else: func <- make_variant[T](vr)
				}
			}
			panic("unreachable enum variant")
		}
		default: comp.compile_error("`derive.copy` does not support `${comp.type_name[T]()}`")
}
```

### 10.2 Compile-Time Lookup Table + File Embedding

```lowlight
@comptime_only
const _build_crc32_table = fn() [256: u32]: {
	let tbl: [256: u32] = [256: u32(0)]
	for(i in 0..256): {
		let c = u32(i)
		for(_ in 0..8): {
			if((c & u32(1)) != u32(0)): c = (c >> u32(1)) ^ u32(0xEDB88320)
			else: c = c >> u32(1)
		}
		tbl[i] = c
	}
	tbl
}

const CRC32_TABLE: [256: u32] = _build_crc32_table()
const RUNTIME_H: string = comp.embed_str("runtime_h.c")
```
