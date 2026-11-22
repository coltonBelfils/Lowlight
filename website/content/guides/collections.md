---
title: Collections and iteration
summary: Lists, maps, sets, and arrays in depth; collecting loops; and writing your own iterators.
order: 12
group: Core ideas
---

# Collections and iteration

## Choosing a collection

| Need | Use | Notes |
|---|---|---|
| an ordered sequence that grows | `[T]` (`List[T]`) | `push`, `pop`, `insert`, `remove`; `xs[i]` panics out of range, `xs.get(i)` doesn't |
| a fixed number of elements, inline | `[N: T]` (`Array[T]`) | no allocation; size is part of the type |
| lookup by key | `#[K: V]` (`Map[K, V]`) | `K` must be `Hashable`; `m[k]` is `?V` |
| membership | `#[T]` (`Set[T]`) | `T` must be `Hashable` |
| a span of numbers or characters | `a..b`, `a..=b` (`Range[T]`) | plain value; no allocation |
| a fixed group of different types | `+[A, B]` (tuple) | access with `.0`, `.1`; unpack with `unpack` |

Maps and sets iterate in **insertion order**. Iteration is deterministic across runs and platforms.

## Ownership and elements

Collections own their elements. That has three consequences worth knowing:

- **Reading** an element (`xs[i]`, a loop variable) gives a read-only view. To keep it, `.copy()` it.
- **Adding** an element moves it in: `xs.push(item)` with a temporary, or `xs.push(own item)` with a variable.
- **Removing** an element (`pop`, `remove`, `take`) gives you ownership of it.

```lowlight
bind main = (): {
	let queue = ["a" + "", "b" + "", "c" + ""]
	let first = queue.remove(0)        // we own `first` now
	let copies = for(s in queue) [string]: s.copy()
	queue.clear()
	println("$first $copies ${queue.len()}")
}
```

```output
a [b, c] 0
```

## Loops that build collections

A `for` loop with a result type is Lowlight's `map`/`filter`/`collect`:

```lowlight
bind main = (): {
	let nums = [3, 8, 1, 9, 4]
	let big = for(n in nums) [int]: {
		if(n < 4): next <-
		n * 10
	}
	let set = for(n in nums) #[int]: n % 3
	let by_index = for(i, n in nums) #[int: int]: MapEntry[key: n, value: i]
	println(big)
	println(set.len())
	println(by_index[9] ?? -1)
}
```

```output
[80, 90, 40]
3
3
```

The result type may be any type whose `push` method accepts the body's value (the `Collectable` interface), including your own.

## Iterating

| Loop | Visits |
|---|---|
| `for(x in xs)` | each element (read-only) |
| `for(i, x in xs)` | index and element |
| `for(&x in xs)` | each element, mutably (when `xs` is mutable) |
| `for(k, v in m)` | each key and value of a map |
| `for(c in s.chars())` | each character of a string |
| `for(c in s)` · `for(i, c in s)` | each character of a string, with the byte offset where it starts |
| `while(x in it)` | like `for`, but never collects; leave with `while <- v` |

```lowlight
bind main = (): {
	let scores = [70, 85, 92]
	for(&s in scores): s += 5
	println(scores)
}
```

```output
[75, 90, 97]
```

While a loop iterates a collection, the collection can't be modified or moved. To build a changed version, collect into a new one.

## Your own iterators

An iterator is any type with a `next: (&self) ?T` method, which returns the next element or `none` at the end. Drive one with a `while` loop:

```lowlight
define Countdown struct [
	cur: int,
]

bind Countdown.next = (&self) ?int: {
	if(self.cur <= 0): func <- none
	self.cur -= 1
	self.cur + 1
}

bind main = (): {
	let it = Countdown[cur: 3]
	while(true): {
		let i = it.next() ?? while <-
		print("$i ")
	}
	println("liftoff")
}
```

```output
3 2 1 liftoff
```

`let i = it.next() ?? while <-` takes the next element or leaves the loop when there is none.

The prelude's `Iterator[T]` and `Iterable[T]` interfaces describe this protocol, and the standard collections implement them. Today `for` loops iterate the built-in collections, ranges, strings, and `Pool`; for other iterators, use the `while` pattern above.

## Equality, hashing, and printing

- `==` works on lists, maps, and sets only if you define `eq`; compare lengths and elements explicitly, or use `std/test`'s assertions in tests.
- Elements of sets and keys of maps must be `Hashable`. Numbers, `bool`, `char`, strings, tag enums, and tuples of those are; give your own types a `hash` method (and `eq`) to use them as keys.
- Every collection of printable elements is printable: `println([1, 2])` prints `[1, 2]`.

## More operations

- `std/sort`: `sort`, `sort_by`, `stable_sort`, `binary_search`, `lower_bound`, `upper_bound`, `reverse`, `is_sorted`, and sorted views of sets and map keys.
- `std/strings`: `join`, `split_whitespace`, `lines`, and more for lists of strings.
- [The prelude reference](/spec/the-prelude.html#125-collections) lists every built-in collection method.
