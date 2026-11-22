---
title: Testing
summary: Writing tests, assertions, test helpers and caller locations, the test runner, and editor integration.
order: 21
group: Working with code
---

# Testing

Testing is built into the language and toolchain. A test is a function marked `@test`; `lowlight test` finds and runs every test, checks each one for memory leaks, and isolates failures so one broken test doesn't stop the rest.

## Writing tests

```lowlight
import "std/test"

bind slugify = fn(s: string) string: {
	let out = ""
	for(c in s.to_lower().chars()): {
		if(c.is_alnum()): out.push_char(c)
		else if(not out.is_empty() and not out.ends_with("-")): out.push_char('-')
	}
	if(out.ends_with("-")): out = out[..out.len() - 1].copy()
	out
}

@test("lowercases and joins words with dashes")
bind test_slugify_words = fn(): test.assert_eq(slugify("Hello World"), "hello-world")

@test("collapses punctuation")
bind test_slugify_punct = fn(): test.assert_eq(slugify("Ready, set... go!"), "ready-set-go")

bind main = fn(): println(slugify("Lowlight by Example"))
```

```output
lowlight-by-example
```

- A test takes no parameters and returns nothing. The description in `@test("...")` is what the runner prints.
- Tests can live in any file, but usually go in `name_test.ll` files beside the code. Test files are compiled only by `lowlight test`, and they belong to the package, so they can test private (`_`) functions.
- Black-box tests that use only a package's exported API go in a separate package under `tests/`.

## Assertions

`std/test` provides assertions that print both sides of a failed comparison:

| Assertion | Checks |
|---|---|
| `assert_eq(a, b)` · `assert_ne(a, b)` | equality |
| `assert_true(c)` · `assert_false(c)` | a condition |
| `assert_gt` · `assert_lt` · `assert_ge` · `assert_le` | ordering |
| `assert_approx_eq(a, b, eps)` | floats within `eps` |
| `assert_contains(s, sub)` · `assert_starts_with` · `assert_ends_with` | strings |
| `assert_some(x?)` · `assert_none(x?)` | optionals |
| `assert_is_ok(r!)` · `assert_is_err(r!)` · `assert_ok_eq(r!, v)` · `assert_err_contains(r!, s)` | results |
| `assert_all_eq(cases)` and friends | a table of cases, reporting every failing case |

Every assertion has a `_msg` variant taking a message as its last argument. The built-in `assert(cond, msg)` works too.

## Helpers that report the right line

When a helper function calls an assertion, the failure should point at the test that called the helper, not at the helper itself. Mark the helper `@track_caller`:

```lowlight fragment
@track_caller
bind assert_valid_slug = fn(s: string): {
	test.assert_eq_msg(slugify(s), slugify(slugify(s)), "slugify should be idempotent")
}
```

Inside a `@track_caller` function, panics, `Error.new`, and `caller_location()` report the location of the call. All `std/test` assertions are marked this way.

## Test-only code

Mark helpers, fixtures, and types used only by tests with `@test_only`. Normal code can't refer to them, and normal builds leave them out. Dependencies needed only by tests go in `[dev-dependencies]`.

## Running tests

```bash
lowlight test                     # every package in the project
lowlight test ./lexer             # one package
lowlight test -run slug           # tests whose names match a regular expression
lowlight test --list              # list tests without running them
lowlight test --json              # machine-readable events, one JSON object per line
lowlight test --release           # with optimizations
lowlight test --backend c11       # with the C backend
```

The runner compiles one test program per package. For each test it reports pass or fail and the duration, and at the end a summary; the exit status is non-zero if anything failed.

A test **fails** when:

- an assertion fails or the test panics (the runner records the message and location, then continues with the next test);
- the test **leaks** heap memory: anything it allocated and didn't free is reported as `leaked N heap allocation(s)`.

Leak checking is on for every test, so tests double as memory-safety checks for unsafe code and C bindings.

## Editor integration

The [editor integrations](/start/editors.html) list tests in the editor's test explorer (VS Code's Testing panel, JetBrains' test runner, and Neovim's neotest), run single tests or whole files, and show failures at the line where they happened. They use `lowlight test --json`, which you can use for other tools as well.

## Tips

- Test behavior through the public API where you can; use private access for tricky internals.
- Use table-driven tests with `assert_all_*` when checking many inputs: every failing case is reported, not just the first.
- Keep tests deterministic. Map and set iteration order is insertion order, so output is stable without sorting.
