# Lowlight Style Guide & Idiomatic Conventions

This document defines the canonical coding style, naming conventions, and design idioms for writing clear, idiomatic **Lowlight**. For formal language rules and semantics, see [`SPECIFICATION.md`](./SPECIFICATION.md).

Lowlight's design centers on **call-site explicitness without syntactic clutter**: mutations (`&`), ownership transfers (`own`), nullability (`name?`), recoverable failure bindings (`name!`), early exits (`<-`), and manual memory (`unsafe`) are always visible where you read code, while type inference, bare variant patterns, bare result constructors, and braceless single expressions keep everyday code light.

---

## Table of Contents

1. [Naming Conventions](#1-naming-conventions)
2. [Privacy & Compiler-Enforced Sigils](#2-privacy--compiler-enforced-sigils)
3. [Bindings & Mutability (`bind` vs `let`)](#3-bindings--mutability-bind-vs-let)
4. [Error Handling & Optionals](#4-error-handling--optionals)
5. [Control Flow, Loops, and Pattern Matching](#5-control-flow-loops-and-pattern-matching)
6. [Ownership, Memory, and Data Structures](#6-ownership-memory-and-data-structures)
7. [API & Package Design](#7-api--package-design)
8. [Formatting & Layout (`lowlight fmt`)](#8-formatting--layout-lowlight-fmt)
9. [Documentation Comments (`lowlight doc`)](#9-documentation-comments-lowlight-doc)
10. [Testing](#10-testing)

---

## 1. Naming Conventions

### Summary Table

| Construct | Convention | Examples |
| :--- | :--- | :--- |
| Packages & directories | `snake_case` (short nouns) | `std/http`, `std/strfmt`, `my_lexer` |
| Types (`struct`, `enum`, `result`, `interface`, distinct) | `PascalCase` (title-case acronyms) | `TcpStream`, `HttpClient`, `Url`, `UserId`, `BstNode` |
| Generic type parameters | Concise `PascalCase` | `T`, `K`, `V`, `Item` |
| Functions, methods, locals, parameters, fields | `snake_case` | `parse_url`, `is_empty`, `max_bytes` |
| **Enum variants & `result` members** | **lowercase `snake_case`** | `pending`, `in_progress`, `value`, `err`, `missing`, `forbidden` |
| **Top-level constants** (`bind` values) | **`SCREAMING_SNAKE_CASE`** | `PI`, `STATUS_OK`, `MAX_RETRIES`, `RFC3339_NANO` |
| Optional identifiers (compiler-enforced) | `snake_case` + `?` suffix | `user?`, `port?`, `left?` |
| Result identifiers (compiler-enforced) | `snake_case` + `!` suffix | `cfg!`, `res!`, `conn!` |
| Package-private symbols & sealed methods | Leading underscore `_` | `_parse_header`, `_handle`, `_internal`, `_seal` |

### Title-Case Acronyms in `PascalCase` Type Names

Treat acronyms and initialisms like ordinary words inside `PascalCase` type names:

- **Do:** `Url`, `HttpClient`, `TcpStream`, `JsonValue`, `UserId`, `BstNode`, `IoError`
- **Avoid:** `URL`, `HTTPClient`, `TCPStream`, `JSONValue`, `UserID`, `BSTNode`, `IOError`

```lowlight
define Url struct [
	scheme: string,
	host: string,
	port: int,
	path: string,
]

define UserId int
```

### Lowercase `snake_case` Enum Variants & Result Members

All enum variants and `result` failure members use lowercase `snake_case` so they read cleanly in both qualified expressions (`Status.pending`, `Grade.needs_work`) and bare `match` / result-chain patterns (`when pending:`, `default err(own e):`). The success member of every `result` type is always `value`.

```lowlight
define Status enum [
	pending,
	in_progress,
	completed,
	failed: string,
]

define Lookup result [
	value: string,
	missing: string,
	forbidden,
	expired: int,
]
```

### `SCREAMING_SNAKE_CASE` Constants

Top-level constant `bind` values use `SCREAMING_SNAKE_CASE`:

```lowlight
bind MAX_RETRIES: int = 3
bind DEFAULT_PORT: int = 8080
bind DATE_ONLY = "2006-01-02"
bind RFC3339_NANO = "2006-01-02T15:04:05.999999999Z07:00"
```

Local non-mutated values use normal `snake_case` (`bind timeout_ms = 500`), unless you want a module-level constant look at top level.

---

## 2. Privacy & Compiler-Enforced Sigils

### Package-Private Encapsulation (`_`)

Anything starting with `_` is private to its declaring package (all `.ll` files in the same directory):

- Functions and constants: `bind _parse_digits = ...`, `bind _BUFFER_SIZE = 4096`
- Types: `define _Route struct [...]`
- Struct fields and methods: `_handle: ptr[u8]`, `bind File._close_checked = ...`
- Enum variants: `define Token enum [ ident: string, _eof ]`
- Sealed interfaces: include an unexported method such as `_seal: (self) int` to prevent external packages from implementing the interface.

Avoid getters/setters that merely expose a `_field` unchanged without enforcing an invariant; make plain data fields public directly, and privatize `_field` only when a type maintains internal invariants or owns raw handles.

### Optional (`?`) and Result (`!`) Name Sigils

Lowlight enforces `?` on every binding, parameter, or struct field of type `?T`, and `!` on every binding, parameter, or struct field of type `define ... result [...]`:

- Work **with** the sigils — never strip or work around them with awkward names:
  - Flow-narrow `?` optionals cleanly with `if(port?): ...` (inside the branch, `port` loses its `?` and has type `int`), or destructure with `if(parse_port(s) as p): ...`.
  - Unwrap or propagate `!` results directly at the call site (`load_config(path): default err(own e): ...`) so `!` variables only appear when you genuinely hold onto an uninspected `Result` value (e.g., inside test assertions or deferring handling).

---

## 3. Bindings & Mutability (`bind` vs `let`)

Lowlight distinguishes deep immutability (`bind`) from mutability (`let`):

- **Top-level:** Always use `bind` (functions, methods, constants). Top-level mutable globals (`let`) are discouraged outside tightly scoped tests or process singletons.
- **Locals:** **Prefer `bind` by default** for any local that is never reassigned or mutably borrowed. Use `let` only when the variable is actually reassigned (`count += 1`) or passed as a mutable borrow (`&buf`, `items.push(...)`).

```lowlight
bind sum_evens = (items: [int]) int: {
	bind n = items.len()
	let total = 0
	for(i in 0..n): {
		bind x = items[i]
		if(x % 2 == 0): total += x
	}
	total
}
```

Making `bind` your default choice documents at a glance which bindings can change inside a function and pairs naturally with deep immutability checking.

---

## 4. Error Handling & Optionals

### Choosing Between `?T`, Custom `result`, `Result[T]`, and `panic`

| Return Type | When to Use |
| :--- | :--- |
| `?T` | Absence is the only non-success outcome (`map[k]`, `parse_int`, `next()`). |
| Custom `result` | Callers are expected to branch on specific domain failure cases (`missing`, `forbidden`, `expired`). |
| `Result[T]` | General recoverable I/O, parsing, validation, or OS failure with an [`Error`] context chain. |
| `panic(...)` | Violated precondition or impossible internal invariant (e.g., out-of-bounds index, bug). Never for bad external input. |

### Constructing Results (`value(...)` and `err(...)`)

Wherever a result type is expected (function return body, `func <-`, typed `if`/`match` branches, annotated `bind`/`let`, typed call argument), **prefer bare `value(...)` and failure variant names** (`err(...)`, `missing(...)`, `forbidden`):

```lowlight
bind parse_port = (raw: string) Result[int]: {
	bind port = parse_int(raw) ?? func <- err(Error.coded("invalid_port", "expected integer port"))
	if(port < 1 or port > 65535):
		func <- err(Error.coded("invalid_port", "port $port out of range 1..=65535"))
	value(port)
}
```

- When type context is **absent** (such as an unannotated `let r! = ...`, an untyped collection literal `[Result[int].value(1)]`, or a chained call), qualify with `Result[T].value(...)` or `Result[T].err(...)`.
- `Result[T].fail_msg("...")` is available as a convenient shorthand for `err(Error.new("..."))` when an expected `Result[T]` type is absent or when returning an uncoded site-tracked message. (`Result.ok` and `Result.fail` do not exist — `value` and `err` are the single canonical names.)

### Propagating and Asserting Results

1. **Propagate with context via a result chain** (name the bound error `e` rather than `err` so it never shadows the bare constructor `err(...)`):
   ```lowlight
   bind cfg = load_config(path):
   	default err(own e): func <- err(e.wrap("loading server config"))
   ```
2. **Domain-specific recovery with a result chain:**
   ```lowlight
   bind item = cache.lookup(key):
   	when missing(why): func <- missing(why)
   	when forbidden: func <- forbidden
   	default expired(_): cache.refresh(key)
   ```
3. **Asserting / panicking on a result:** **Prefer `expr: default panic`** uniformly for both `Result[T]` and custom `result` types when a failure indicates a violated invariant or setup error in a test/script:
   ```lowlight
   bind cfg = load_config(path): default panic
   ```
   When you want a custom panic message with the full `Error` trace, handle `default err(own e)` explicitly:
   ```lowlight
   bind cfg = load_config(path):
   	default err(own e): panic(e.wrap("failed to initialize server"))
   ```
4. **Simple fallbacks and optional-style discarding:**
   - Use `??` to provide a fallback or unwind on failure: `find(key) ?? "default"` or `find(key) ?? next <-`.
   - Use `if(r! as v): ...` when you only care whether it succeeded and not why it failed.

### Fallible vs Panicking API Naming (No `try_` Prefix)

Do **not** prefix fallible functions or methods with `try_`. Fallibility is already unmistakable from the return type (`Result[T]`, `?T`, or custom `result`), the caller's `!`/`?` suffix rules, and `: default panic`:

- **Do:** `DateTime.new(...) Result[DateTime]`, `DateTime.diff(other) ?Duration`, `File.open(...) Result[File]`
- **Avoid:** `try_new`, `try_diff`, `try_open`

When an asserting/panicking convenience twin is genuinely useful (such as constructing known-valid literals in tests or constants), give the fallible operation the plain name and prefix the panicking twin with `must_*`:

```lowlight
bind dt! = time.DateTime.new(y, m, d, 0, 0, 0, false)   // fallible: returns Result[DateTime]
bind epoch = time.DateTime.must_new(1970, 1, 1, 0, 0, 0, false) // panics if out of range
```

When an alternative behavior avoids failure without panicking (like saturating arithmetic), name the behavior explicitly with a descriptive suffix (e.g., `DateTime.diff_saturating`).

---

## 5. Control Flow, Loops, and Pattern Matching

### Statement Loops (`for` vs `while`)

Lowlight distinguishes loops cleanly by both **purpose** and **value semantics**:

- **In statement (void) position:**
  - Use **`for`** for iteration over collections and ranges: `for(item in items): ...`, `for(i, item in items): ...`, `for(i in 0..n): ...`.
  - Use **`while`** for condition loops: `while(i < n): ...`, `while(running): ...`.
- **In expression (value) position:**
  - **`for`** always evaluates to a `Collectable` (`[T]` by default, or an explicit `#[T]` / `#[K: V]`):
    ```lowlight
    bind evens = for(x in 0..10): {
    	if(x % 2 != 0): next <-
    	x
    }
    ```
  - **`while`** always evaluates to a scalar (`T` with `else:`, or `?T` without `else:`):
    ```lowlight
    bind found? = while(i in 0..items.len()): {
    	if(items[i] == target): while <- i
    }
    ```

### Pattern Matching (`match`)

- **Prefer bare variant patterns** (`when pending:`, `when text(own s):`, `default err(own e):`). Qualify (`when Status.pending:`) only when a bare name collides with an in-scope variable or an instance method on the enum.
- Put the last covered variant on **`default variant:`** rather than adding an unreachable catch-all `default: panic(...)` — exhaustive `default variant:` turns missing future variants into compile errors.
- Use `when in lo..hi:` for containment, method predicates like `when is_digit():` for classification, and multi-argument `match(a, b):` instead of nested matches.

```lowlight
bind eval_bin = (op: TokKind, lhs: int, rhs: int) Result[int]:
	match(op):
		when plus: value(lhs + rhs)
		when minus: value(lhs - rhs)
		when star: value(lhs * rhs)
		when slash:
			if(rhs == 0): err(Error.new("division by zero"))
			else: value(lhs / rhs)
		default: err(Error.new("expected binary operator"))
```

### Braceless Bodies vs `{ ... }`

- **Prefer braceless bodies** for concise single-expression functions, `match`/`if`-bodied functions, one-line `if`/`else`, `match` arms, and single-statement loops:
  ```lowlight
  bind Response.is_success = (self) bool: self.status_code >= 200 and self.status_code < 300

  bind clamp_nonneg = (n: int) int:
  	if(n < 0): 0 else: n
  ```
- **Use `{ ... }` blocks** for multi-statement bodies or deeply nested control flow where explicit delimiters make scopes and unwinding targets easier to scan.

### Targeted Unwinding (`<-`)

- Use `func <- val` for early guards at the top of a function, avoiding deep `else` pyramids.
- Use `next <-` to filter/skip loop iterations and `for <-` / `while <-` to break out cleanly.
- Use labeled unwinds (`outer <- val`, `next outer <-`) only when breaking across multiple nested loops or blocks; keep label names short and descriptive.

---

## 6. Ownership, Memory, and Data Structures

Lowlight uses **Model 2A** ownership: every value is either **Plain** (implicitly copied: integers, floats, `bool`, `char`, `ptr[T]`, `ref[T]`, pure tag enums, structs of Plain fields) or a **Resource** (single-owner with deterministic LIFO `Drop`: `string`, `[T]`, `#[T]`, `#[K: V]`, structs/unions containing Resources).

### Parameter & Receiver Modes

Choose the weakest mode that does the job:

1. **Read-only borrow (`x: T`, `self`) — default:** Zero-copy! Even `string`, `[T]`, and structs are borrowed without cloning or transferring ownership. Always prefer `(self)` and `(s: string)` for inspection and formatting.
2. **Mutable borrow (`x: &T`, `&self`):** Mutates in place. Callers must pass `&arg` at direct call sites so side effects are visible.
3. **Owned transfer (`x: own T`, `own self`):** Use when the function stores the value into a struct/collection, consumes/destroys it (like `Result.unwrap`), or moves a payload into a new `Error`/enum wrapper. Callers pass `own var` (moving without copy) or `var.copy()`.

```lowlight
// Reads without copying `msg`; mutates `errors` visibly at call sites (`&errors`).
bind record_if_bad = (errors: &[string], ok: bool, msg: string):
	if(not ok): errors.push(msg.copy())
```

### Avoiding Unnecessary `.copy()`

- Borrow instead of copying in helper signatures (`(s: string)`, not `(s: own string)` when you only inspect `s`).
- Inside loops and builders, transfer ownership with `own` when the local binding is at the end of its lifetime (`items.push(own line)`, `Token[kind: TokKind.ident, text: own name]`).

### `Arena` + `ref[T]` vs `Pool[T]` vs `unsafe`

- **`Arena` + `ref[T]`:** Ideal for ASTs, IR graphs, compilers, and per-request scratch allocation. Nodes allocate at bump-pointer speed, cycles are allowed, destructors of fields run on reset/exit, and the compiler statically prevents `ref[T]` from escaping the arena's scope.
- **`std/pool` (`Pool[T]` + `Handle[T]`):** Ideal for long-lived game entities, caches, or graphs needing arbitrary individual deletion and O(1) slot reuse with stale-handle detection.
- **`unsafe { ... }`:** Keep `unsafe` blocks as small as possible, wrap raw pointer arithmetic behind safe abstractions, and document the upheld invariants under a `// # Safety` doc section.
- **`Drop`:** Implement `bind Type.drop = (&self): ...` only when a type owns non-Lowlight resources (OS handles, FFI memory, custom buffers). Plain composite Resources free their fields automatically in reverse declaration order.

---

## 7. API & Package Design

- **Constructors & Conversions:**
  - Primary constructor: `Type.new(...)` (returning `Result[Type]` when validation can fail, with `Type.must_new(...)` only when needed).
  - Typed constructors: `Duration.from_millis(...)`, `DateTime.from_unix_secs(...)`.
  - Conversion prefixes:
    - `to_*` borrows `(self)` and allocates/formats a new representation (`to_string()`, `to_rfc3339()`).
    - `into_*` consumes `(own self)` and transfers ownership (`into_ok()`, `into_err()`, `into_bytes()`).
    - `as_*` returns a cheap zero-copy view or borrow.
- **No `get_` prefix on accessors:** Name accessors after the property (`list.len()`, `req.header("host")`, `err.message()`, `resp.is_success()`).
- **Attributes:**
  - Mark any function whose ignored return value is almost always a bug (results, error values, important handles) with `@must_use`.
  - Mark assertion helpers, error constructors (`Error.new`), and unwrapping helpers with `@track_caller` so panics and error traces report the caller's line.
- **Methods vs Package Functions:**
  - Attach methods (`bind Type.method = ...`) when an operation clearly belongs to a type's noun-verb API (`url.query_param(k)`, `dt.add_duration(d)`).
  - Use package functions (`http.parse_url`, `math.clamp_int`, `time.month_abbr`) for general utilities or operations where adding methods to primitive types like `int` or `string` would pollute completion.
- **Package Structure:** Keep `main` exclusively in binary entrypoint packages (`./` or `cmd/<app>/`). Library packages must omit `main` so they remain importable.

---

## 8. Formatting & Layout (`lowlight fmt`)

Always run `lowlight fmt` on your code before committing. The canonical formatting rules enforced by the formatter are:

- **Indentation:** Hard tabs (`\t`), 1 tab per nesting level.
- **Line width:** Soft limit of 100 columns.
- **Spacing:** No space before `(` after control-flow keywords (`if(cond):`, `for(x in xs):`, `while(cond):`, `match(val):`). Single spaces around binary operators (`a + b`, `x == y`).
- **Trailing commas:** Multiline struct definitions, enum/result definitions, interface definitions, list/set/map literals, parameter lists, and call arguments require trailing commas on the last item (so diffs stay one line per addition).
- **Single-line vs multiline blocks:** The formatter unwraps single-expression `{ expr }` bodies automatically when appropriate and keeps top-level declarations separated by one blank line.
- **Optional overrides:** A project can customize `indent_style`, `tab_width`, and `line_width` via `.lowlightfmt` or `[format]` in `lowlight.toml`, though canonical defaults (`"tabs"`, `4`, `100`) are strongly recommended for shared code and libraries.

---

## 9. Documentation Comments (`lowlight doc`)

Write doc comments with `//` directly above exported declarations and as a module banner at the top of each `.ll` file:

- **Summary line first:** Start with a concise, present-tense third-person sentence (`// Parses a target string into a [`Url`].`), followed by a blank `//` line before details or sections.
- **Intra-doc links:** Link to types, methods, functions, and modules with backtick brackets: ``[`Url`]``, ``[`DateTime.new`]``, ``[`Result.err`]``, ``[`std/time`]``. Validate links with `lowlight doc --check`.
- **Parameters & sections:** Document non-obvious parameters using bulleted `- \`name\`: ...` items (or `# Arguments`), and use standard section headings where applicable:
  - `# Errors` — Conditions under which a `Result` / `result` returns a failure.
  - `# Panics` — Preconditions or invariants whose violation triggers `panic`.
  - `# Ownership` — Notable move/borrow semantics (`Consumes ownership of ...`).
  - `# Safety` — Required invariants for `unsafe` callers or pointer parameters.
  - `# Example` — A `lowlight` fenced code block showing typical usage.

```lowlight
// std/net — Blocking TCP networking primitives.
//
// Provides [`TcpStream`] and [`TcpListener`] built on POSIX sockets.

// Connects to the remote `host:port` TCP endpoint.
//
// - `host`: Hostname or IP literal (for example `"127.0.0.1"`).
// - `port`: TCP port number in `1..=65535`.
//
// # Errors
// Returns [`Result.err`] if DNS resolution fails or the connection is refused.
bind TcpStream.connect = (host: string, port: int) Result[TcpStream]: {
	// ...
}
```

---

## 10. Testing

Lowlight organizes tests into three tiers:

1. **Inline `@test("description")` in the source file:** Best for small, focused unit tests and white-box tests exercising package-private `_helpers` right next to their implementation (usually placed at the bottom of the module).
2. **Sibling `<name>_test.ll` white-box files:** Move tests into `<module>_test.ll` in the same package directory once the test suite grows large. Like inline `@test`s, `<name>_test.ll` sees unexported `_` declarations and can use `[dev-dependencies]`, while staying completely excluded from normal `lowlight build` / `run` / imports.
3. **External integration packages (`tests/`):** Put black-box, multi-package, and end-to-end tests in `tests/`.

Use `std/test` helpers (`test.assert_eq`, `test.assert_ne`, `test.assert_ok_eq`, `test.assert_contains`, etc.) for clear diff output with caller locations (`@track_caller`), and run `lowlight test` regularly (leak checking via `LOWLIGHT_CHECK_LEAKS=1` is enabled automatically so tests verify zero heap leaks as well as functional correctness).
