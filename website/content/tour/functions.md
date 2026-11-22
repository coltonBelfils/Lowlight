---
title: Functions
summary: Declaring functions, parameters and return types, closures, and methods.
order: 2
---

# Functions

## Declaring functions

A function is a value bound to a name:

```lowlight
bind add = (a: int, b: int) int: a + b

bind describe = (n: int) string: {
	let kind = if(n % 2 == 0): "even" else: "odd"
	"$n is $kind"
}

bind main = (): {
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
bind first_negative = (xs: [int]) int: {
	for(x in xs): if(x < 0): func <- x
	0
}

bind main = (): println(first_negative([3, 9, -4, -1]))
```

```output
-4
```

`<-` is Lowlight's single "leave" operator. The word in front of it says what to leave: `func <-` the function, `for <-` the loop, `next <-` the current iteration. You'll see the rest in [Control flow](control-flow.html).

## Function values and closures

Functions are values, and a function literal can capture variables from where it is written:

```lowlight
bind apply_twice = (f: (int) int, x: int) int: f(f(x))

bind main = (): {
	let step = 5
	let add_step = (x: int) int: x + step
	println(apply_twice(add_step, 1))
	println(apply_twice((x: int) int: x * x, 3))
}
```

```output
11
81
```

A function type is written like a signature without names: `(int) int`. Captured plain values are copied into the closure; captured strings and other resources are moved in.

## Methods

A method is a function attached to a type. Declare it with `bind Type.name`, and give it a `self` parameter:

```lowlight
define Counter struct [
	count: int,
]

bind Counter.new = () Counter: Counter[count: 0]

bind Counter.bump = (&self): { self.count += 1 }

bind Counter.value = (self) int: self.count

bind main = (): {
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
- You can add methods to any type, including built-in ones: `bind int.double = (self) int: self * 2`.

A function body that is just an assignment needs braces, as in `bump` above, so that `=` is never mistaken for the end of a declaration.

## Parameters borrow by default

A parameter normally gives the function a read-only view of the argument. To let a function change the caller's variable, both sides say so with `&`:

```lowlight
bind add_bonus = (score: &int, bonus: int): { score += bonus }

bind main = (): {
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
