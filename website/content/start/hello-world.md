---
title: Your first program
nav: First program
summary: Write, run, build, format, and test a Lowlight program.
order: 2
---

# Your first program

## Hello, world

Create a file named `hello.ll`:

```lowlight
bind main = fn(): println("Hello, world!")
```

```output
Hello, world!
```

Run it:

```bash
lowlight run hello.ll
```

That's the whole program. `bind` declares a name, `main` is where execution starts, `()` means it takes no parameters, and the part after `:` is its body: here, a single expression.

## A slightly bigger program

```lowlight
bind greet = fn(name: string) string: "Hello, $name!"

bind main = fn(): {
	let names = ["Ada", "Grace", "Linus"]
	for(n in names): println(greet(n))
	println("${names.len()} greetings")
}
```

```output
Hello, Ada!
Hello, Grace!
Hello, Linus!
3 greetings
```

A few things to notice:

- `greet` declares its parameter type and return type: `(name: string) string`.
- A body with several statements is a block in braces. Statements end at the end of the line; there are no semicolons to type.
- `$name` and `${expr}` interpolate values into strings.
- `[...]` is a list literal, and `for(n in names)` loops over it.

## Building an executable

`run` compiles to a temporary executable and runs it. To keep the executable:

```bash
lowlight build hello.ll        # writes ./hello
./hello
lowlight build -o greet --release hello.ll
```

`--release` turns on optimizations. Safety checks (array bounds, division by zero, shift counts) stay on in every build.

## Checking without running

```bash
lowlight check hello.ll
```

`check` reports every error without generating code. It is fast enough to run on every save, and it is what your editor's language server does as you type.

## Formatting

Lowlight has one standard format. Format a file in place:

```bash
lowlight fmt -w hello.ll
```

Use `lowlight fmt --check` in CI to fail on unformatted files. See [Formatting](/ref/formatter.html) for the few settings there are.

## Command-line arguments and exit codes

```lowlight no-run
bind main = fn() int: {
	let argv = args()
	if(argv.len() < 2): {
		eprintln("usage: ${argv[0]} <name>")
		func <- 2
	}
	println("hi, ${argv[1]}")
	0
}
```

`args()` returns the program name followed by its arguments. Pass arguments after `--` when using `run`:

```bash
lowlight run greet.ll -- Ada
```

`main` may return an `int`, which becomes the exit status. `func <- 2` returns early from the function: Lowlight's `return`.

## A first test

Put tests in a file whose name ends in `_test.ll`, next to the code they test:

```lowlight fragment
// greet_test.ll
import "std/test"

@test("greet includes the name")
bind test_greet = fn(): test.assert_eq(greet("Ada"), "Hello, Ada!")
```

```bash
lowlight test
```

Tests can call everything in their package, including private names. See [Testing](/guides/testing.html).

## Next

[Create a project →](projects.html)
