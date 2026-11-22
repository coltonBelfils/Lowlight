---
title: Collections
summary: Lists, arrays, maps, sets, ranges, and iteration.
order: 7
---

# Collections

| Type | Literal | Is |
|---|---|---|
| `[T]` | `[1, 2, 3]` | a growable list |
| `[N: T]` | `[3: 0]`, `[3: 1, 2, 3]` | a fixed-size array |
| `#[K: V]` | `#["a": 1]` | a hash map |
| `#[T]` | `#[1, 2]` | a hash set |
| `Range[T]` | `0..10`, `0..=9` | a range of integers or characters |

## Lists

```lowlight
bind main = fn(): {
	let xs = [3, 1, 4]
	xs.push(1)
	xs.push(5)
	println(xs)
	println(xs.len())
	println(xs[0])
	println(xs.get(10) ?? -1)
	println(xs.contains(4))
	let last? = xs.pop()
	println(last? ?? 0)
	println(xs[1..3])
}
```

```output
[3, 1, 4, 1, 5]
5
3
-1
true
5
[1, 4]
```

`xs[i]` stops the program if `i` is out of range; `xs.get(i)` returns an optional instead. Pick whichever matches what an invalid index means in your code: a bug, or a normal case.

## Maps

```lowlight
bind main = fn(): {
	let ages = #["ada": 36, "grace": 45]
	ages["linus"] = 21
	println(ages.len())
	println(ages["ada"] ?? 0)
	println(ages.has("bob"))
	for(name, age in ages): println("$name is $age")
	let removed? = ages.remove("grace")
	println(removed? ?? 0)
}
```

```output
3
36
false
ada is 36
grace is 45
linus is 21
45
```

Reading `m[k]` always gives an optional, because the key may be missing. Maps and sets iterate in **insertion order**, so programs behave the same on every run.

## Sets

```lowlight
bind main = fn(): {
	let seen: #[string] = #[]
	for(w in ["a", "b", "a", "c", "b"]): seen.insert(w.copy())
	println(seen.len())
	println(seen.has("c"))
}
```

```output
3
true
```

A loop variable borrows each element, so storing it in another collection takes a `.copy()` (or `own`, when you are consuming the source). An empty literal such as `#[]` or `[]` needs its type from context: an annotation, a parameter type, or a return type. `#[]` alone could be either a set or a map.

## Arrays

Arrays have a fixed size, known at compile time, and live inline (on the stack, or inside a struct):

```lowlight
bind main = fn(): {
	let grid = [4: 0]
	grid[2] = 7
	println(grid)
	let primes = [3: 2, 3, 5]
	println(primes.len())
}
```

```output
[0, 0, 7, 0]
3
```

## Loops that collect

A `for` loop with a result type builds a collection from its body's values. The result type can be a list, set, map, or any type with a `push` method:

```lowlight
bind main = fn(): {
	let words = ["apple", "Bob", "kiwi", "Bob"]
	let lengths = for(w in words) [int]: w.len()
	let unique = for(w in words) #[string]: w.to_lower()
	let index = for(i, w in words) #[string: int]: MapEntry[key: w.copy(), value: i]
	println(lengths)
	println(unique.len())
	println(index["Bob"] ?? -1)
}
```

```output
[5, 3, 4, 3]
3
3
```

`next <-` skips an element, as in the `evens` example in [Control flow](control-flow.html).

## Sorting and more

The prelude covers the basics. More operations live in packages: `std/sort` adds `sort`, `sort_by`, and `binary_search` to lists, and `std/strings` adds string utilities.

```lowlight
import "std/sort"

bind main = fn(): {
	let xs = [5, 2, 8, 1]
	xs.sort()
	println(xs)
	xs.sort_by(fn(a: int, b: int) int: b - a)
	println(xs)
}
```

```output
[1, 2, 5, 8]
[8, 5, 2, 1]
```

See the [standard library](/std/index.html) for everything available.

## Next

[Results and errors →](results.html)
