---
title: Results and errors
summary: Failures as values, result types, result chains, and panics.
order: 8
---

# Results and errors

Lowlight separates two kinds of trouble:

- **Failures** are things that go wrong in a working program: a missing file, bad input, a refused connection. They are ordinary values that the caller handles.
- **Bugs** are things that should never happen: an index out of range, a broken invariant. They **panic**: the program stops immediately with a message and location.

There are no exceptions, and nothing can be thrown past you unseen.

## Result types

An operation that can fail returns a **result type**. You declare one with `result`, listing a `value` member for success and one member for each way it can fail:

```lowlight
define Withdrawal result [
	value: int,
	insufficient: int,
	frozen,
]

bind withdraw = (balance: int, amount: int, frozen: bool) Withdrawal:
	if(frozen): Withdrawal.frozen
	else if(amount > balance): insufficient(amount - balance)
	else: value(balance - amount)

bind main = (): {
	let r! = withdraw(100, 30, false)
	println(r! ?? -1)
}
```

```output
70
```

Two things to notice:

- Where a result is expected, its members can be named **bare**: `value(...)`, `insufficient(...)`.
- A name that holds a result must end in `!`, just as optionals end in `?`. You can see at a glance which values might be failures.

## Result chains

The main way to handle a result is a **result chain**: write a colon after the call and handle each failure by name. The chain evaluates to the success value:

```lowlight
define Withdrawal result [
	value: int,
	insufficient: int,
	frozen,
]

bind withdraw = (balance: int, amount: int, frozen: bool) Withdrawal:
	if(frozen): Withdrawal.frozen
	else if(amount > balance): insufficient(amount - balance)
	else: value(balance - amount)

bind report = (amount: int, frozen: bool) string: {
	let left = withdraw(100, amount, frozen):
		when insufficient(short): func <- "short by $short"
		default frozen: func <- "account frozen"
	"left: $left"
}

bind main = (): {
	println(report(30, false))
	println(report(130, false))
	println(report(10, true))
}
```

```output
left: 70
short by 30
account frozen
```

Each arm either produces a replacement value or leaves with an unwind. The chain must handle every failure: it ends with `default failure:` for the last one, `default:` for all the rest, or `default panic` to declare that the remaining failures are bugs.

## Result[T] and Error

For everyday code you don't need to declare a result type. The prelude's `Result[T]` has two members, `value: T` and `err: Error`, and `Error` carries a message, an optional code, a chain of causes, and a trace of where it passed:

```lowlight
bind parse_port = (s: string) Result[int]: {
	let n = parse_int(s) ?? func <- err(Error.new("'$s' is not a number"))
	if(n < 1 or n > 65535): func <- err(Error.new("port $n out of range"))
	value(n)
}

bind load = (text: string) Result[int]: {
	let port = parse_port(text):
		default err(own e): func <- err(e.wrap("loading config"))
	value(port)
}

bind main = (): {
	println(load("8080") ?? 0)
	let r! = load("http")
	if(r!.peek_err() as e): println(e.summary())
}
```

```output
8080
loading config: 'http' is not a number
```

`e.wrap("context")` adds a layer of explanation while keeping the original error as the cause, so messages read from the outside in.

## Other ways to use a result

```lowlight fragment
let port = parse_port(s) ?? 80                  // a fallback for any failure
if(parse_port(s) as p): use(p)                  // only the success
let p = parse_port(s): default panic            // failure here is a bug
match(parse_port(s)):                           // full control
	when Result.value(p): ...
	default Result.err(e): ...
```

## Panics

A panic is for bugs. It prints a message with the source location, flushes output, and exits with status 1:

```lowlight panics
bind main = (): {
	println("starting")
	let xs = [1, 2, 3]
	let i = 7
	println(xs[i])
}
```

```output
starting
lowlight panic at main.ll:5: index 7 out of bounds for length 3
```

Panics cannot be caught. Use `assert(condition, "message")` to check your own invariants.

## Next

[Ownership →](ownership.html)
