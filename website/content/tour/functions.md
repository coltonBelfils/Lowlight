---
title: Functions
summary: Declaring functions, parameters and return types, closures, and methods.
order: 2
---

# Functions

## Declaring functions

A function is a value bound to a name:

```lowlight
bind add = fn(a: int, b: int) int: a + b

bind describe = fn(n: int) string: {
	let kind = if(n % 2 == 0): "even" else: "odd"
	"$n is $kind"
}

bind main = fn(): {
	println(add(2, 3))
	println(describe(7))
}
```

```output
5
7 is odd
```

The shape is always `(parameters) ReturnType: body`. The body is one expression, or a block whose **last expression** is the result. There is no `return` keyword for the normal case: the value of the body is the result. A function that returns nothing omits the return type.

Functions are declared in any order. `main` can call a function defined below it.

## Returning early

`func <- value` leaves the function immediately with `value`:

```lowlight
bind first_negative = fn(xs: [int]) int: {
	for(x in xs): if(x < 0): func <- x
	0
}

bind main = fn(): println(first_negative([3, 9, -4, -1]))
```

```output
-4
```

`<-` is Lowlight's single "leave" operator. The word in front of it says what to leave: `func <-` the function, `for <-` the loop, `next <-` the current iteration. You'll see the rest in [Control flow](control-flow.html).

## Function values and closures

Functions are values. There are two kinds: an `fn` captures nothing and is just a pointer to code, so it copies freely; a `cl` (closure) may capture variables from where it is written, and owns what it captures:

```lowlight
bind apply_twice = fn(f: cl(int) int, x: int) int: f(f(x))

bind main = fn(): {
	let step = 5
	let add_step = cl(x: int) int: x + step
	println(apply_twice(add_step, 1))
	println(apply_twice(fn(x: int) int: x * x, 3))
}
```

```output
11
81
```

A function type is written like a signature without names, with its kind: `fn(int) int` or `cl(int) int`. An `fn` goes anywhere a `cl` with the same signature is expected, as `fn(x: int) int: x * x` does above; `cl(f)` writes that conversion explicitly. Captured plain values are copied into the closure; captured strings and other resources are moved in. Writing `cl` for a closure that captures nothing draws a warning, since it could be an `fn`.

## Methods

A method is a function attached to a type. Declare it with `bind Type.name`, and give it a `self` parameter:

```lowlight
define Counter struct [
	count: int,
]

bind Counter.new = fn() Counter: Counter[count: 0]

bind Counter.bump = fn(&self): { self.count += 1 }

bind Counter.value = fn(self) int: self.count

bind main = fn(): {
	let c = Counter.new()
	c.bump()
	c.bump()
	println(c.value())
}
```

```output
2
```

- `self` reads the receiver; `&self` may change it; `own self` consumes it.
- A function without `self`, like `Counter.new`, is an **associated function**, called on the type.
- You can add methods to any type, including built-in ones: `bind int.double = fn(self) int: self * 2`.

A function body that is just an assignment needs braces, as in `bump` above, so that `=` is never mistaken for the end of a declaration.

## Parameters borrow by default

A parameter normally gives the function a read-only view of the argument. To let a function change the caller's variable, both sides say so with `&`:

```lowlight
bind add_bonus = fn(score: &int, bonus: int): { score += bonus }

bind main = fn(): {
	let score = 10
	add_bonus(&score, 5)
	println(score)
}
```

```output
15
```

There is no way for a function to change your variable without an `&` at the call. The full story is in [Ownership](ownership.html).

## Next

[Your own types →](types.html)
