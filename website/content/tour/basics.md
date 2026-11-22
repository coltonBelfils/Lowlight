---
title: Basics
summary: Values, variables, printing, numbers, and strings.
order: 1
---

# Basics

## Printing

```lowlight
bind main = fn(): {
	println("Hello!")
	print("no newline, ")
	println(42)
	println(3.5)
	println(true)
	println()
	eprintln("this goes to standard error")
}
```

```output
Hello!
no newline, 42
3.5
true

```

`println` writes any value that can be shown as text, followed by a newline. `print` omits the newline, and `eprint`/`eprintln` write to standard error.

## Variables: `bind` and `let`

```lowlight
bind main = fn(): {
	bind greeting = "hello"   // immutable
	let count = 0             // mutable
	count += 1
	count = count * 10
	println("$greeting $count")
}
```

```output
hello 10
```

`bind` introduces a name that never changes. `let` introduces a variable you can assign to again. Prefer `bind`; reach for `let` when the value really changes. Types are inferred, but you can write them: `let count: int = 0`.

`bind` is deep: you cannot change anything reachable through a `bind`, not even an element of a list.

## Numbers

```lowlight
bind main = fn(): {
	let a = 7
	let b = 2
	println(a / b)            // integer division truncates
	println(a % b)
	println(float(a) / 2.0)   // convert explicitly
	let small: u8 = 250
	println(small + 10)       // integers wrap around
	println(0xFF + 0b101)
	println(1_000_000)
}
```

```output
3
1
3.5
4
260
1000000
```

`int` is a 64-bit signed integer and `float` is a 64-bit double. Sized types exist too: `i8` to `i64`, `u8` to `u64`, `f32`, `f64`. Numbers **never convert implicitly**: `int + float` is an error, and you write `float(a)` or `int(f)` to convert. Integer arithmetic wraps on overflow, and dividing by zero stops the program with a clear message instead of doing something undefined.

## Booleans and characters

```lowlight
bind main = fn(): {
	let ok = 3 > 2 and not false
	println(ok or false)
	let c = 'λ'
	println(c)
	println(int('A'))
	println('7'.is_digit())
}
```

```output
true
λ
65
true
```

The logical operators are words: `and`, `or`, `not`. A `char` is one Unicode character, in single quotes.

## Strings

```lowlight
bind main = fn(): {
	let name = "Ada"
	let year = 1843
	println("$name wrote the first program in $year.")
	println("In ${2026 - year} years, a lot has changed.")
	let s = "Hello, " + name
	println(s.len())
	println(s.to_upper())
	println(s.contains("Ada"))
	println(s[0..5])
	let raw = `multi-line strings
use backticks, and "quotes" need no escaping`
	println(raw)
}
```

```output
Ada wrote the first program in 1843.
In 183 years, a lot has changed.
10
HELLO, ADA
true
Hello
multi-line strings
use backticks, and "quotes" need no escaping
```

Strings are UTF-8. `s.len()` is the length in **bytes**, `s[i]` is a byte, and `s[a..b]` is a substring by byte offsets. Use `s.chars()` to work with characters. `$name` and `${expression}` interpolate any printable value.

## Comments

```lowlight fragment
// A line comment.
/* A block comment. */

// Comments directly above a declaration are its documentation.
// They may use Markdown, and [`println`] links to other symbols.
bind answer = 42
```

## Statements and lines

Each statement ends at the end of its line; you never type a semicolon. A line continues when it obviously isn't finished: inside parentheses or brackets, after a binary operator, or when the next line starts with `.`:

```lowlight
bind main = fn(): {
	let total = 1 +
		2 +
		3
	let shout = "quiet"
		.to_upper()
	println("$total $shout")
}
```

```output
6 QUIET
```

## Next

[Functions →](functions.html)
