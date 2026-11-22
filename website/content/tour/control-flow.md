---
title: Control flow
summary: if, while, for, and leaving early with unwinds and labels.
order: 5
---

# Control flow

Lowlight's control constructs all have the same shape, `keyword(header): body`, and they are all **expressions**: they produce values.

## if

```lowlight
bind main = fn(): {
	let temp = 23
	if(temp > 30): println("hot")
	else if(temp > 15): println("pleasant")
	else: println("cold")

	let label = if(temp > 20): "warm" else: "cool"
	println(label)
}
```

```output
pleasant
warm
```

The condition is always in parentheses, the body follows a colon, and the body is one expression or a `{ }` block. There's no ternary operator because `if` already is one.

## while

`while(condition)` repeats as long as the condition holds:

```lowlight
bind main = fn(): {
	let n = 27
	let steps = 0
	while(n != 1): {
		n = if(n % 2 == 0): n / 2 else: 3 * n + 1
		steps += 1
	}
	println(steps)
}
```

```output
111
```

## for

`for(x in collection)` visits every element of a list, array, map, set, range, or string:

```lowlight
bind main = fn(): {
	for(i in 0..3): print("$i ")
	println()
	for(i in 1..=3): print("$i ")
	println()
	for(i, name in ["ada", "grace"]): println("$i: $name")
}
```

```output
0 1 2 
1 2 3 
0: ada
1: grace
```

`a..b` is the range from `a` up to but excluding `b`; `a..=b` includes `b`. With two loop variables, the first is the index (or the key, for a map).

### Loops that produce values

Give a `for` loop a result type and it **collects** each value its body produces:

```lowlight
bind main = fn(): {
	let squares = for(i in 1..=5) [int]: i * i
	println(squares)
	let evens = for(i in 1..=10) [int]: {
		if(i % 2 != 0): next <-
		i
	}
	println(evens)
}
```

```output
[1, 4, 9, 16, 25]
[2, 4, 6, 8, 10]
```

## Leaving early: unwinds

There is no `return`, `break`, or `continue`. Instead there is one operator, `<-`, which **unwinds** out of a construct. The word in front says which:

| Write | Leaves | |
|---|---|---|
| `func <- v` | the function | like `return v` |
| `for <-` | the innermost `for` | like `break` |
| `while <- v` | the innermost `while`, giving it a value | `break` with a value |
| `next <-` | the current iteration | like `continue` |
| `<- v` | the innermost `{ }` block, giving it a value | |
| `name <- v` | the block or loop labeled `name` | labeled `break` |

```lowlight
bind main = fn(): {
	let words = ["alpha", "beta", "stop", "gamma"]
	for(w in words): {
		if(w == "stop"): for <-
		println(w)
	}
	let found = while(true) int: while <- 7
	println(found)
}
```

```output
alpha
beta
7
```

### Labels

Label a loop or block to leave it from inside a nested one:

```lowlight
bind main = fn(): {
	let grid = [[1, 2, 3], [4, 5, 6]]
	let where = search: while(r, row in grid) string: {
		for(c, v in row): if(v == 5): search <- "row $r, column $c"
	} else: "not found"
	println(where)
}
```

```output
row 1, column 1
```

`while` can iterate too (`while(x in xs)`); unlike `for`, it never collects, so its value comes only from its unwinds and its optional `else:` arm, which runs when the loop finishes without being unwound. That makes it the natural loop for searching.

Unwinding always cleans up: every value owned by a scope you leave is freed on the way out.

## Next

[Pattern matching →](matching.html)
