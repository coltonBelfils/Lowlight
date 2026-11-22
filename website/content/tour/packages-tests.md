---
title: Packages and tests
summary: Imports, visibility, and writing tests.
order: 11
---

# Packages and tests

## Imports

A package is a directory of `.ll` files. Import one by its path and use its names through the last segment of the path:

```lowlight
import "std/strings"
import "std/math"

bind main = fn(): {
	println("ab".repeat(3))
	println(strings.join(["a", "b", "c"], "-"))
	println(math.sqrt(16.0))
}
```

```output
ababab
a-b-c
4
```

Imports are always qualified, except for methods: importing a package also makes the methods it adds to existing types (like `string.repeat` from `std/strings`) available. The prelude, `std/core`, is imported everywhere automatically.

Inside a project, your own packages are imported by the project name followed by the directory: `import "myapp/parser"`. See [Your first project](/start/projects.html).

## Visibility

Everything is public unless its name starts with `_`:

```lowlight fragment
define Account struct [
	owner: string,
	_balance: int,          // other packages can't read or set this
]

bind Account.balance = fn(self) int: self._balance

bind _audit = fn(a: Account): println(a.owner)   // private to this package
```

A struct with a private field can't be built with a literal outside its package, so packages export a constructor function instead. That is how a type protects its invariants.

## Tests

Tests are functions marked `@test`, in files ending in `_test.ll`:

```lowlight fragment
// account_test.ll
import "std/test"

@test("a new account is empty")
bind test_new_account = fn(): {
	let a = Account.new("ada")
	test.assert_eq(a.balance(), 0)
}
```

Run them with `lowlight test`. Each test runs in isolation: a failed assertion or a panic fails that test only, and the runner reports the file and line. `std/test` has assertions for equality, ordering, approximate floats, strings, optionals, and results, each with a `_msg` variant for a custom message.

A helper marked `@track_caller` reports failures at its caller's location, so assertion helpers point at the test that failed rather than at themselves.

```bash
lowlight test                 # all packages
lowlight test -run balance    # only tests whose names match
lowlight test --list          # list tests without running them
```

## Next

[Next steps →](next-steps.html)
