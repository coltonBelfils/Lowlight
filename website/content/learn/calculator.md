---
title: "Tutorial: a calculator"
nav: A calculator
summary: Write a tokenizer, a recursive-descent parser, and an evaluator, using enums, match, methods, and Result.
order: 2
group: Tutorials
---

# Tutorial: a calculator

In this tutorial you'll build a calculator that evaluates expressions like `(1 + 2) * -3`. Along the way you'll use enums with payloads, `match`, a struct with `&self` methods, recursion, and `Result` for reporting errors such as division by zero.

The program has three parts:

1. a **tokenizer** that turns text into a list of tokens;
2. a **parser** that reads the tokens according to the grammar and computes the value as it goes;
3. a small **driver** that prints results and errors.

## Tokens

A token is a number, an operator, or a parenthesis. That's an enum with payloads:

```lowlight fragment
define Token enum [
	num: float,
	op: char,
	lparen,
	rparen,
]
```

`Token` holds only plain values, so it is itself plain: tokens copy freely. (To compare tokens with `==`, it would add `@derive(Equatable)`.)

## The tokenizer

The tokenizer walks the input byte by byte. It returns `Result[[Token]]`: a list of tokens, or an error.

```lowlight fragment
bind tokenize = (src: string) Result[[Token]]: {
	let tokens: [Token] = []
	let i = 0
	while(i < src.len()): {
		let c = char(src[i])
		if(c.is_whitespace()): {
			i += 1
			next <-
		}
		if(c.is_digit() or c == '.'): {
			let start = i
			while(i < src.len() and (char(src[i]).is_digit() or char(src[i]) == '.')): i += 1
			let text = src[start..i]
			let n = parse_float(text) ?? func <- err(Error.new("bad number '$text'"))
			tokens.push(Token.num(n))
			next <-
		}
		match(c):
			when '+', '-', '*', '/': tokens.push(Token.op(c))
			when '(': tokens.push(Token.lparen)
			when ')': tokens.push(Token.rparen)
			default: func <- err(Error.new("unexpected '$c' at offset $i"))
		i += 1
	}
	value(tokens)
}
```

Things to notice:

- `src[i]` is a byte (`u8`); `char(...)` converts it. The input here is ASCII, so bytes and characters coincide.
- `next <-` skips to the next iteration of the `while` loop, like `continue`.
- `parse_float(text) ?? func <- err(...)` either produces the number or leaves the function with an error, in one line.
- `value(tokens)` and `err(...)` construct the two members of `Result`. Since the function's return type is a `Result`, the members can be named bare.

## The grammar

Operator precedence comes from the structure of the grammar. Multiplication binds tighter than addition because a *term* is made of *factors*:

```ebnf
expr   = term { ("+" | "-") term } ;
term   = factor { ("*" | "/") factor } ;
factor = number | "-" factor | "(" expr ")" ;
```

Each rule becomes one method, and each method returns `Result[float]`.

## The parser

The parser keeps the tokens and a position:

```lowlight fragment
define Parser struct [
	tokens: [Token],
	pos: int,
]

bind Parser.peek = (self) ?Token: self.tokens.get(self.pos)
```

`peek` returns an optional because there may be no token left.

`expr` parses one term and then keeps consuming `+` or `-` followed by another term:

```lowlight fragment
bind Parser.expr = (&self) Result[float]: {
	let left = self.term(): default err(own e): func <- err(e)
	while(true): {
		let t = self.peek() ?? while <-
		let o = match(t):
			when op(c): c
			default: while <-
		if(o != '+' and o != '-'): while <-
		self.pos += 1
		let right = self.term(): default err(own e): func <- err(e)
		left = if(o == '+'): left + right else: left - right
	}
	value(left)
}
```

The line `self.term(): default err(own e): func <- err(e)` is a **result chain**. It evaluates to the term's value, or, if `term` failed, passes the same error on to our caller. You'll write this line a lot; it's Lowlight's explicit form of "propagate the error".

`while <-` leaves the loop when there is no operator to consume. Notice that `match` is an expression here: it produces the operator character, or leaves the loop.

`term` has the same shape, with one extra check:

```lowlight fragment
bind Parser.term = (&self) Result[float]: {
	let left = self.factor(): default err(own e): func <- err(e)
	while(true): {
		let t = self.peek() ?? while <-
		let o = match(t):
			when op(c): c
			default: while <-
		if(o != '*' and o != '/'): while <-
		self.pos += 1
		let right = self.factor(): default err(own e): func <- err(e)
		if(o == '/' and right == 0.0): func <- err(Error.coded("div_zero", "division by zero"))
		left = if(o == '*'): left * right else: left / right
	}
	value(left)
}
```

`Error.coded` attaches a machine-readable code to the error, which callers can test with `e.has_code("div_zero")`.

`factor` decides by the next token:

```lowlight fragment
bind Parser.factor = (&self) Result[float]: {
	let t = self.peek() ?? func <- err(Error.new("unexpected end of input"))
	self.pos += 1
	match(t):
		when num(n): value(n)
		when op(c): {
			if(c != '-'): func <- err(Error.new("unexpected '$c'"))
			let v = self.factor(): default err(own e): func <- err(e)
			value(-v)
		}
		when lparen: {
			let v = self.expr(): default err(own e): func <- err(e)
			if(self.peek() != Token.rparen): func <- err(Error.new("missing ')'"))
			self.pos += 1
			value(v)
		}
		default rparen: err(Error.new("unexpected ')'"))
}
```

The `match` is exhaustive: its last arm names the last variant, `rparen`, so adding a token type later would make this function fail to compile until it handles the new token. `self.peek() != Token.rparen` compares an optional with a value: it is true when the optional is `none` or holds something else.

## Putting it together

`calc` runs both stages and checks that every token was used. Here is the complete program:

```lowlight
define Token enum [
	num: float,
	op: char,
	lparen,
	rparen,
]

bind tokenize = (src: string) Result[[Token]]: {
	let tokens: [Token] = []
	let i = 0
	while(i < src.len()): {
		let c = char(src[i])
		if(c.is_whitespace()): {
			i += 1
			next <-
		}
		if(c.is_digit() or c == '.'): {
			let start = i
			while(i < src.len() and (char(src[i]).is_digit() or char(src[i]) == '.')): i += 1
			let text = src[start..i]
			let n = parse_float(text) ?? func <- err(Error.new("bad number '$text'"))
			tokens.push(Token.num(n))
			next <-
		}
		match(c):
			when '+', '-', '*', '/': tokens.push(Token.op(c))
			when '(': tokens.push(Token.lparen)
			when ')': tokens.push(Token.rparen)
			default: func <- err(Error.new("unexpected '$c' at offset $i"))
		i += 1
	}
	value(tokens)
}

define Parser struct [
	tokens: [Token],
	pos: int,
]

bind Parser.peek = (self) ?Token: self.tokens.get(self.pos)

bind Parser.expr = (&self) Result[float]: {
	let left = self.term(): default err(own e): func <- err(e)
	while(true): {
		let t = self.peek() ?? while <-
		let o = match(t):
			when op(c): c
			default: while <-
		if(o != '+' and o != '-'): while <-
		self.pos += 1
		let right = self.term(): default err(own e): func <- err(e)
		left = if(o == '+'): left + right else: left - right
	}
	value(left)
}

bind Parser.term = (&self) Result[float]: {
	let left = self.factor(): default err(own e): func <- err(e)
	while(true): {
		let t = self.peek() ?? while <-
		let o = match(t):
			when op(c): c
			default: while <-
		if(o != '*' and o != '/'): while <-
		self.pos += 1
		let right = self.factor(): default err(own e): func <- err(e)
		if(o == '/' and right == 0.0): func <- err(Error.coded("div_zero", "division by zero"))
		left = if(o == '*'): left * right else: left / right
	}
	value(left)
}

bind Parser.factor = (&self) Result[float]: {
	let t = self.peek() ?? func <- err(Error.new("unexpected end of input"))
	self.pos += 1
	match(t):
		when num(n): value(n)
		when op(c): {
			if(c != '-'): func <- err(Error.new("unexpected '$c'"))
			let v = self.factor(): default err(own e): func <- err(e)
			value(-v)
		}
		when lparen: {
			let v = self.expr(): default err(own e): func <- err(e)
			if(self.peek() != Token.rparen): func <- err(Error.new("missing ')'"))
			self.pos += 1
			value(v)
		}
		default rparen: err(Error.new("unexpected ')'"))
}

bind calc = (src: string) Result[float]: {
	let tokens = tokenize(src): default err(own e): func <- err(e)
	let p = Parser[tokens: own tokens, pos: 0]
	let v = p.expr(): default err(own e): func <- err(e)
	if(p.pos < p.tokens.len()): func <- err(Error.new("unexpected input after the expression"))
	value(v)
}

bind main = (): {
	let inputs = ["1 + 2 * 3", "(1 + 2) * -3", "10 / 4", "1 / (2 - 2)", "2 * (3 + 4", "7 % 2", "1 2"]
	for(src in inputs): {
		let result = calc(src):
			default err(own e): {
				println("$src  =>  error: ${e.message()}")
				next <-
			}
		println("$src  =>  $result")
	}
}
```

```output
1 + 2 * 3  =>  7
(1 + 2) * -3  =>  -9
10 / 4  =>  2.5
1 / (2 - 2)  =>  error: division by zero
2 * (3 + 4  =>  error: missing ')'
7 % 2  =>  error: unexpected '%' at offset 2
1 2  =>  error: unexpected input after the expression
```

## What you practiced

- **Enums with payloads** model data that comes in several shapes, and `match` takes them apart exhaustively.
- **`&self` methods** let the parser advance its own position.
- **`Result` and result chains** report errors without exceptions: every place an error can pass through is visible in the code.
- **Unwinds** (`next <-`, `while <-`, `func <-`) replace `continue`, `break`, and `return`, and say exactly what they leave.

## Exercises

1. Add the `%` operator to `term`.
2. Add exponentiation, `^`, which binds tighter than `*` and groups to the right: `2 ^ 3 ^ 2` is `2 ^ 9`. (Hint: add a `power` rule between `term` and `factor` that calls itself for its right operand.)
3. Build a tree instead of computing as you parse: define `Expr` as an enum with `num: float` and `binary: Binary`, where `Binary` is a struct holding the operator and a list of two operands. Then write `eval(e: Expr) Result[float]`.
4. Write tests for `calc` in `calc_test.ll` using `std/test`, including the error cases (`test.assert_err_contains`).
