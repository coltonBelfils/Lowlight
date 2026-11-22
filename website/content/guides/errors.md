---
title: Failure handling
summary: Choosing between optionals, results, and panics; designing result types; adding context to errors.
order: 11
group: Core ideas
---

# Failure handling

Lowlight has three tools for things that don't go as planned, and choosing the right one is most of the skill:

| Situation | Tool | The caller sees |
|---|---|---|
| A value may legitimately be absent: a map lookup, parsing a number, the end of an iterator | `?T` | `x?`, `??`, `if(x?)` |
| An operation can fail for reasons outside the program: I/O, user input, the network | a result type | `x!`, result chains |
| Something that should be impossible happened: a broken invariant, an index the program computed wrongly | a panic | a crash with a location |

There are no exceptions. Every failure a function can produce is in its signature, and every place a failure is handled or passed on is visible in the code.

## Optionals for absence

When "not found" is a normal answer, return `?T`. The caller can't forget to check, because a `?T` can't be used as a `T`:

```lowlight
bind find_user = (ids: [int], id: int) ?int: {
	for(i, x in ids): if(x == id): func <- i
	none
}

bind main = (): {
	let ids = [10, 20, 30]
	let at = find_user(ids, 20) ?? -1
	println(at)
	if(find_user(ids, 99) as i): println(i)
	else: println("not found")
}
```

```output
1
not found
```

## Result types for failures

When the caller needs to know **why** something failed, return a result. A result type has a `value` member for success and one member per kind of failure:

```lowlight fragment
define Fetch result [
	value: string,
	not_found: string,      // the missing path
	timed_out: int,         // seconds waited
	refused,
]
```

Design result types around **what the caller will do differently**. If two failures are always handled the same way, they can be one member. If a caller might retry on `timed_out` but not on `not_found`, keep them apart. Each payload should carry what's needed to explain or act on the failure, and no more.

Names that hold results end in `!` (`let page! = fetch(url)`), so a reader always knows a value may be a failure.

### Handling failures: result chains

A result chain handles failures by name and evaluates to the success value:

```lowlight fragment
let page = fetch(url):
	when not_found(path): func <- "missing: $path"
	when timed_out(secs): retry(url, secs * 2)
	default refused: func <- "connection refused"
```

Each arm either produces a replacement value or unwinds. The chain must handle every failure, and its last arm starts with `default`:

- `default failure(x):` handles the last failure by name. Prefer this: adding a failure to the type later makes every chain that misses it a compile error.
- `default:` handles all remaining failures the same way.
- `default panic` declares that the remaining failures can't happen here. If one does, the program stops with `unhandled failure Fetch.refused` and the location.

### Other ways in

| Write | Gives |
|---|---|
| `r! ?? fallback` | the value, or `fallback` for any failure |
| `if(r! as v): ...` | runs the branch only on success |
| `match(r!): when Fetch.value(v): ... ` | full control, all members |
| `r!.is_ok()`, `r!.is_err()` | a `bool` (for `Result[T]`) |

## Result[T] and Error

Most code doesn't need a custom result type. The prelude's `Result[T]` has `value: T` and `err: Error`. An `Error` carries:

- a **message**, `e.message()`;
- an optional machine-readable **code**, `Error.coded("not_found", "...")`, tested with `e.has_code(...)`;
- a **cause**, the error it wraps, so a chain of context builds up;
- a **trace** of source locations where it was created and wrapped.

```lowlight
bind read_port = (text: string) Result[int]: {
	let n = parse_int(text) ?? func <- err(Error.coded("syntax", "'$text' is not a number"))
	value(n)
}

bind load = (text: string) Result[int]: {
	let port = read_port(text):
		default err(own e): func <- err(e.wrap("reading the port"))
	value(port)
}

bind main = (): {
	let r! = load("eighty")
	if(r!.peek_err() as e): {
		println(e.summary())
		println(e.has_code("syntax"))
		println(e.root().message())
	}
}
```

```output
reading the port: 'eighty' is not a number
true
'eighty' is not a number
```

### Passing failures up

The propagation idiom is a one-line chain:

```lowlight fragment
let cfg = load_config(path): default err(own e): func <- err(e.wrap("starting server"))
```

There is deliberately no shorter operator: passing a failure on is a decision, and the line shows where it happens and what context it adds. Add context that the inner function couldn't know ("starting server", "for user 42"), not a restatement of the error.

`own e` moves the error out so it can be wrapped. Errors are resources, so the compiler makes sure every error is handled, wrapped, or deliberately dropped.

### Converting between result types

Use a precise result type next to the operation, and convert to `Result[T]` where callers no longer care about the distinctions:

```lowlight fragment
bind fetch_text = (url: string) Result[string]: {
	let body = fetch(url):
		when not_found(p): func <- err(Error.coded("not_found", "no page at $p"))
		when timed_out(s): func <- err(Error.coded("timeout", "gave up after ${s}s"))
		default refused: func <- err(Error.new("connection refused"))
	value(body)
}
```

## Panics for bugs

A panic means "the program is wrong". It prints `lowlight panic at file:line:col: message` (plus the error's trace for `panic(error)`), flushes standard output, and exits with status 1. Destructors don't run, and panics can't be caught, so a panic is never a way to handle an expected failure.

Use:

- `assert(condition, "message")` to check invariants;
- `panic("message")` when control reaches a place it never should;
- `xs[i]` (which panics when out of range) when an invalid index would be a bug, and `xs.get(i)` when it's a normal case;
- `default panic` in a chain when the remaining failures are impossible in context.

## Naming: fallible and panicking twins

When an API offers both forms, the fallible one gets the plain name and the panicking one is prefixed with `must_`:

```lowlight fragment
let dt! = DateTime.new(2026, 2, 30, 0, 0, 0)        // a result: the date may be invalid
let launch = DateTime.must_new(2026, 1, 15, 9, 0, 0) // panics if invalid: for constants you know are valid
```

## In tests

`std/test` has assertions for results: `test.assert_is_ok(r!)`, `test.assert_is_err(r!)`, `test.assert_ok_eq(r!, expected)`, and `test.assert_err_contains(r!, "fragment")`. See [Testing](testing.html).
