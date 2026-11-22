---
title: Ownership and borrowing
nav: Ownership and borrowing
summary: How Lowlight manages memory at compile time, and how to design code around ownership.
order: 10
group: Core ideas
---

# Ownership and borrowing

Lowlight has no garbage collector and no reference counting, and yet you never free memory by hand. The compiler knows, at every point in the program, which variable owns each value, and it inserts the code that destroys values when their owner goes away. This guide explains the model, the rules, and how to write code that works with it rather than against it.

The rules are precise and short. For the normative version, see [chapter 7 of the specification](/spec/ownership-borrowing-and-destruction.html).

## Plain values and resources

Every type is in one of two categories:

| | Plain | Resource |
|---|---|---|
| Examples | `int`, `float`, `bool`, `char`, tag enums, `+[int, int]`, structs of plain fields | `string`, `[T]`, `#[K: V]`, `#[T]`, closures, interface values, anything containing a resource, anything with `drop` |
| Assigning or passing | copies | moves |
| Cleanup | none needed | destroyed when the owner's scope ends |
| Duplicating | automatic | explicit `.copy()` |

Whether a struct is plain is decided by its fields, so changing one field from `int` to `string` changes how the whole struct behaves. The compiler will point out every place that needs adjusting.

## Moves are written down

Moving an existing variable requires `own`. A move is the only way a variable can lose its value, so `own` marks every point where that happens:

```lowlight
bind main = (): {
	let names = ["ada", "grace"]
	let team = own names          // `names` is now empty
	println(team.len())
	names = ["linus"]             // a moved `let` variable can be given a new value
	println(names)
}
```

```output
2
[linus]
```

Moves that need no `own` are the ones where the source has no name or is going away anyway: temporaries (`f("a" + "b")`), a function's return value, the value of an unwind, and captures by a closure.

## Parameter modes

| Declared | Called | Meaning | Typical use |
|---|---|---|---|
| `x: T` | `f(v)` | read-only; the caller keeps `v` | most parameters |
| `x: &T` | `f(&v)` | the function may change `v` | in-place updates, "out" parameters |
| `x: own T` | `f(own v)` | the function takes `v` | storing the value, consuming builders |

Choose the weakest mode that works. A read-only parameter is the most flexible for callers: they can pass anything, including a `bind` constant, and keep it afterwards.

Methods use the same modes on `self`: `(self)`, `(&self)`, `(own self)`. For an `&self` method, the receiver is lent automatically: you write `list.push(x)`, not `(&list).push(x)`. The receiver must still be a mutable place, such as a `let` variable.

## Why borrows are so simple

In Lowlight, a borrow lasts **at most one call** (a parameter), **one loop** (iterating a collection), or **one branch** (`if(x?)`). Borrows can't be stored in structs or returned. Because no borrow outlives the statement that created it, the compiler never needs lifetime annotations, and the rules fit in a few lines:

1. While a value is lent with `&`, nothing else in the same call may use it.
2. While a collection is being iterated, it can't be modified or moved.
3. A borrowed parameter can't escape: it can't be returned, stored, or captured.

When you need a value to outlive the call that received it, take it with `own`, or make a `.copy()`.

## Destruction

When a scope ends, its resources are destroyed in reverse order of declaration. Destroying a struct first runs its `drop` method, if it has one, and then destroys its fields:

```lowlight
define Resource struct [
	name: string,
]

bind Resource.drop = (&self): println("close ${self.name}")

define Pair struct [
	a: Resource,
	b: Resource,
]

bind main = (): {
	let p = Pair[a: Resource[name: "a"], b: Resource[name: "b"]]
	let r = Resource[name: "r"]
	println("end of main")
}
```

```output
end of main
close r
close b
close a
```

This is deterministic: the same program always destroys values at the same points. Leaving a scope early with any unwind (`func <-`, `for <-`, a labeled unwind) destroys everything owned by the scopes it leaves. A **panic**, on the other hand, stops the program immediately without running destruction.

## Moving inside control flow

If a variable is moved on some paths but not others, it can't be used afterwards, but the compiler still destroys it on the paths where it wasn't moved. A path that ends by leaving (`func <-`, `panic`) doesn't count as continuing:

```lowlight
bind consume = (s: own string): println("consumed $s")

bind maybe_consume = (s: own string, really: bool): {
	if(really): {
		consume(own s)
		func <-
	}
	println("kept $s")        // fine: on this path, `s` was not moved
}

bind main = (): {
	maybe_consume("a" + "", true)
	maybe_consume("b" + "", false)
}
```

```output
consumed a
kept b
```

Inside a loop, moving a variable declared outside the loop is allowed only if every path assigns it a new value before the next iteration.

## Closures

A closure captures the variables it uses when it is created. Plain values are copied in; resources are **moved** in, and the outer variable becomes unusable. Inside the closure, captures are read-only.

```lowlight
bind main = (): {
	let prefix = "> " + ""
	let show = (s: string) string: prefix + s     // `prefix` moves into `show`
	println(show("hello"))
	// println(prefix)                            // error: moved into the closure
}
```

```output
> hello
```

Copy first (`let p = prefix.copy()`) if you need the original afterwards.

## Designing APIs around ownership

- **Take parameters read-only** unless you need to change or keep them.
- **Return owned values.** Returning is always a move, and the caller decides what to do next.
- **Take `own` for values you store.** A constructor or `push` that keeps its argument should say so, and callers pass temporaries without ceremony: `list.push(make_item())`.
- **Use `&self` for in-place updates and `own self` for consuming transformations**, such as builders: `Request.new(...).with_header(...)`.
- **Don't fight the borrow rules with copies everywhere.** If you find yourself copying a lot, a different ownership structure, such as an [arena or a pool](/guides/memory.html), may fit the data better.

## Common errors and what they mean

| Error | Meaning | Usual fix |
|---|---|---|
| use of moved variable | the value was moved earlier | `.copy()` before moving, or restructure so the move comes last |
| requires `own` at the call site | the parameter takes ownership | write `own x`, or pass `x.copy()` |
| requires `&` at the call site | the parameter may change the argument | write `&x` (and make `x` a `let`) |
| cannot move borrowed variable | a loop variable or parameter only borrows | `.copy()` the value you want to keep |
| moved inside a loop body without being reassigned | the next iteration would see an empty variable | reassign on every path, or move a copy |
