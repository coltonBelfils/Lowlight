---
title: Pattern matching
summary: match, its patterns, and exhaustiveness.
order: 6
---

# Pattern matching

`match` compares a value against patterns and evaluates the first arm that matches. Every `match` ends with a `default` arm.

```lowlight
bind describe = fn(n: int) string:
	match(n):
		when 0: "zero"
		when 1, 2, 3: "a few"
		when in 4..100: "some"
		default: "lots"

bind main = fn(): {
	for(n in [0, 2, 50, 1000]): println(describe(n))
}
```

```output
zero
a few
some
lots
```

## Patterns

| Pattern | Matches |
|---|---|
| `42`, `'x'`, `"text"`, `true` | an equal value |
| `_` | anything |
| `pending`, `circle(r)` | an enum variant, binding its payload |
| `in container` | a value the container contains: a range, list, set, map key, or substring |
| `is_empty()` | a value whose method returns `true` |

Several patterns separated by commas form one arm that matches if any of them does.

## Matching enums

Variants are written **bare** in patterns. A payload pattern binds the variant's value:

```lowlight
define Token enum [
	number: int,
	word: string,
	plus,
	end,
]

bind show = fn(t: Token) string:
	match(t):
		when number(n): "number $n"
		when word(w): "word '$w'"
		when plus: "+"
		default end: "end of input"

bind main = fn(): {
	let tokens = [Token.number(7), Token.plus, Token.word("x"), Token.end]
	for(t in tokens): println(show(t))
}
```

```output
number 7
+
word 'x'
end of input
```

## Exhaustiveness

The last arm always starts with `default`. Write `default:` to catch everything else, or `default variant:` to name the last case. Naming it is the better habit for enums: when someone adds a variant later, every `match` that doesn't handle it stops compiling, instead of silently falling into a catch-all.

```lowlight fragment
match(t):
	when number(n): ...
	when word(w): ...
	default plus: ...      // error: `end` is not handled
```

## Several subjects

`match(a, b)` matches pairs. Parenthesize a combination to give one arm several alternatives:

```lowlight
bind fizz = fn(n: int) string:
	match(n % 3, n % 5):
		when 0, 0: "FizzBuzz"
		when 0, _: "Fizz"
		when _, 0: "Buzz"
		default: n.to_string()

bind main = fn(): {
	let out = for(i in 9..=15) [string]: fizz(i)
	println(out)
}
```

```output
[Fizz, Buzz, 11, Fizz, 13, 14, FizzBuzz]
```

## Matching conditions

`match:` with no subject chooses the first arm whose condition is true, a tidy replacement for long `if`/`else if` chains:

```lowlight
bind grade = fn(score: int) string:
	match:
		when score >= 90: "A"
		when score >= 80: "B"
		when score >= 70: "C"
		default: "F"

bind main = fn(): println(grade(85))
```

```output
B
```

## Moving payloads out

A payload pattern normally gives a read-only view. `own` moves it out of the value instead, and `&` borrows it mutably:

```lowlight
define Msg enum [
	text: string,
	quit,
]

bind take_text = fn(m: own Msg) string:
	match(m):
		when text(own s): s
		default quit: "(none)"

bind main = fn(): println(take_text(Msg.text("hi" + "!")))
```

```output
hi!
```

## Next

[Collections →](collections.html)
