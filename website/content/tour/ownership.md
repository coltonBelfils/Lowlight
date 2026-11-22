---
title: Ownership
summary: How Lowlight manages memory without a garbage collector.
order: 9
---

# Ownership

Lowlight frees memory automatically and deterministically, without a garbage collector, by tracking **who owns each value**. The rules are few, they are checked at compile time, and every ownership change is visible in the source.

## One owner, freed at end of scope

A resource (a string, list, map, set, or anything containing one) has exactly one owner. When the owner's scope ends, the resource is destroyed:

```lowlight
define Noisy struct [
	name: string,
]

bind Noisy.drop = (&self): println("dropping ${self.name}")

bind main = (): {
	let a = Noisy[name: "a"]
	{
		let b = Noisy[name: "b"]
		println("inner scope ends")
	}
	println("main ends")
}
```

```output
inner scope ends
dropping b
main ends
dropping a
```

Destruction happens in reverse order of declaration. A type can run code when it is destroyed by defining `drop`, which is how files close and sockets disconnect without you asking.

## Moving with `own`

Plain values copy freely. A resource **moves**: giving it to a new owner leaves the old variable empty. Moving an existing variable must be written `own`, so moves are never silent:

```lowlight
bind main = (): {
	let a = "hello" + " world"
	let b = own a              // `a` is moved into `b`
	// println(a)              // error: use of moved variable "a"
	println(b)
	let c = b.copy()           // an independent copy; `b` is still usable
	println(b == c)
}
```

```output
hello world
true
```

Temporaries need no `own`: `let s = "a" + "b"` and `consume(make_list())` move values that have no other name.

## Three ways to pass a value

| Parameter | Call | The function | Afterwards, the caller |
|---|---|---|---|
| `x: T` | `f(v)` | reads `v` (a copy if plain, a borrow otherwise) | has `v`, unchanged |
| `x: &T` | `f(&v)` | may change `v` | has `v`, possibly changed |
| `x: own T` | `f(own v)` | owns `v` | no longer has `v` |

```lowlight
bind total = (xs: [int]) int: {
	let t = 0
	for(x in xs): t += x
	t
}

bind add_zero = (xs: &[int]): xs.push(0)

bind into_set = (xs: own [int]) #[int]: for(x in xs) #[int]: x

bind main = (): {
	let xs = [3, 1, 2]
	println(total(xs))
	add_zero(&xs)
	println(xs)
	let s = into_set(own xs)
	println(s.len())
}
```

```output
6
[3, 1, 2, 0]
4
```

The marker at the call is required: you always see which calls can change or take your variables.

## Borrowing rules

Because a function might change what you pass with `&`, Lowlight checks that nothing else touches it at the same time:

- You can't pass the same variable as `&` twice in one call, or pass it as `&` and also read it in the same call.
- You can't modify a list while a `for` loop is iterating over it.
- A borrowed parameter can't be returned or stored somewhere that outlives the call.

That's the whole borrow system. Borrows never last longer than one call or one loop, which is why Lowlight needs no lifetime annotations.

## Returning values

Functions return owned values freely; returning is a move:

```lowlight
bind make_greeting = (name: string) string: "Hello, " + name

bind main = (): {
	let g = make_greeting("Ada")
	println(g)
}
```

```output
Hello, Ada
```

## When you need many values with one lifetime

For graphs, trees with parent links, and other structures where many values point at each other, Lowlight offers **arenas**, which own many values and free them together, and **pools** with generational handles. See [Memory: arenas and pools](/guides/memory.html).

## Next

[Interfaces and generics →](interfaces-generics.html)
