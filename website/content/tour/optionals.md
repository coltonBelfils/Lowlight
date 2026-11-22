---
title: Optionals
summary: Values that may be absent, without null.
order: 4
---

# Optionals

Lowlight has no `null`. A value that might be missing has an **optional type**, `?T`, and the name that holds it **must end in `?`**:

```lowlight
bind main = (): {
	let age? = parse_int("42")       // ?int
	let bad? = parse_int("forty")    // none
	println(age?)
	println(bad?)
}
```

```output
42
none
```

The `?` is part of the name, and the compiler enforces it in both directions: a name ending in `?` must hold an optional, and an optional must be held by a name ending in `?`. So whenever you read `x?`, you know it might be absent, and whenever you read `x`, you know it isn't.

## Getting the value out

**Supply a fallback** with `??`:

```lowlight
bind main = (): {
	let port? = parse_int("80a")
	let p = port? ?? 8080
	println(p)
}
```

```output
8080
```

**Check and use it** with `if(x?)`. Inside the branch, the plain name refers to the value:

```lowlight
bind main = (): {
	let count? = parse_int("12")
	if(count?): println(count + 1)
	else: println("not a number")
}
```

```output
13
```

**Bind an expression's value** with `if(expr as name)`:

```lowlight
bind main = (): {
	let ages = #["ada": 36, "grace": 45]
	if(ages["grace"] as a): println("grace is $a")
	if(ages["linus"] as a): println("linus is $a")
	else: println("no linus")
}
```

```output
grace is 45
no linus
```

**Leave early if it's missing.** The right side of `??` may be an unwind, so a guard and the unwrap are one line:

```lowlight
bind double = (s: string) int: {
	let n = parse_int(s) ?? func <- 0
	n * 2
}

bind main = (): println(double("21") + double("x"))
```

```output
42
```

## Optional fields and chaining

Struct fields can be optional too, with the same naming rule. `?.` reaches through an optional, producing `none` if anything along the way is absent:

```lowlight
define Person struct [
	name: string,
	manager?: ?Person,
]

bind main = (): {
	let ada = Person[name: "Ada", manager?: Person[name: "Grace"]]
	println(ada.manager?.name ?? "nobody")
	println(ada.manager?.manager?.name ?? "nobody")
}
```

```output
Grace
nobody
```

An optional field is `none` when omitted from a literal. A struct may contain an optional of its own type, which is how linked structures like trees are built.

## Rules worth knowing

- Optionals never nest: there is no `??int`.
- `none` needs to know its type: `let x?: ?int = none`.
- A value is wrapped into an optional automatically: `let x?: ?int = 5` works.

## Next

[Control flow →](control-flow.html)
