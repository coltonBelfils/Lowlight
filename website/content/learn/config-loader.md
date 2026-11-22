---
title: "Tutorial: a config loader"
nav: Handling errors
summary: Design result types for a config file parser, handle each failure precisely, and add context as errors cross layers.
order: 3
group: Tutorials
---

# Tutorial: a config loader

Error handling is where Lowlight differs most from languages with exceptions. In this tutorial you'll write a parser for a small configuration format and practice every tool Lowlight gives you for failures:

- custom **result types** that name each way an operation can fail;
- **result chains** that handle failures by name;
- the general-purpose **`Result[T]` and `Error`**, with wrapping and codes;
- choosing between a **result**, an **optional**, and a **panic**.

The format is lines of `key = value`, with `#` comments:

```text
# server settings
host = example.org
port = 8080
debug = true
```

## 1. The configuration type

```lowlight fragment
define Config struct [
	host: string,
	port: int,
	debug: bool,
]
```

## 2. Name the failures

Parsing can fail in three distinct ways, and a caller may want to treat each differently: point at the bad line, report the missing key, or explain the bad value. A **result type** lists them:

```lowlight fragment
define ParseResult result [
	value: Config,
	syntax: int,               // the line number that isn't `key = value`
	missing: string,           // a required key that never appeared
	bad_value: +[string, string],
]
```

`value` is the success member. The others are failures, each carrying just the data needed to explain it.

## 3. Parse one value at a time

Parsing a single value can fail in only one way, so an optional is enough:

```lowlight fragment
bind parse_bool = fn(s: string) ?bool:
	match(s):
		when "true", "yes", "on": true
		when "false", "no", "off": false
		default: none
```

This is the first rule of failure design in Lowlight: **use the smallest tool that fits**. A lookup that may find nothing returns `?T`. Reach for a result when the caller needs to know *why*.

## 4. Parse the file

```lowlight fragment
bind parse = fn(text: string) ParseResult: {
	let host?: ?string = none
	let port = 80
	let debug = false
	for(i, raw in text.split("\n")): {
		let line = raw.trim()
		if(line.is_empty() or line.starts_with("#")): next <-
		let eq = line.index_of("=") ?? func <- syntax(i + 1)
		let key = line[..eq].trim()
		let val = line[eq + 1..].trim()
		match(key):
			when "host": host? = val.copy()
			when "port": port = parse_int(val) ?? func <- bad_value(+["port", val.copy()])
			when "debug": debug = parse_bool(val) ?? func <- bad_value(+["debug", val.copy()])
			default: func <- bad_value(+["key", key.copy()])
	}
	let host = host? ?? func <- missing("host")
	value(Config[host: own host, port: port, debug: debug])
}
```

Each failure is one expression: `?? func <- syntax(i + 1)` means "use the value, or return this failure". An unwind after `??` must be the whole right-hand side of a `let`, `bind`, or assignment, which keeps these exits easy to spot at the start of a line. `syntax(...)`, `bad_value(...)`, and `value(...)` are the result's members, named bare because the function returns a `ParseResult`.

`host?` and `port?` start as `none` and get filled in. A bare `none` has no type of its own, so they are annotated. At the end, a missing `host` is a failure while a missing `port` defaults to 80.

## 5. Handle each failure

A caller handles the failures by name with a result chain:

```lowlight
define Config struct [
	host: string,
	port: int,
	debug: bool,
]

define ParseResult result [
	value: Config,
	syntax: int,
	missing: string,
	bad_value: +[string, string],
]

bind parse_bool = fn(s: string) ?bool:
	match(s):
		when "true", "yes", "on": true
		when "false", "no", "off": false
		default: none

bind parse = fn(text: string) ParseResult: {
	let host?: ?string = none
	let port = 80
	let debug = false
	for(i, raw in text.split("\n")): {
		let line = raw.trim()
		if(line.is_empty() or line.starts_with("#")): next <-
		let eq = line.index_of("=") ?? func <- syntax(i + 1)
		let key = line[..eq].trim()
		let val = line[eq + 1..].trim()
		match(key):
			when "host": host? = val.copy()
			when "port": port = parse_int(val) ?? func <- bad_value(+["port", val.copy()])
			when "debug": debug = parse_bool(val) ?? func <- bad_value(+["debug", val.copy()])
			default: func <- bad_value(+["key", key.copy()])
	}
	let host = host? ?? func <- missing("host")
	value(Config[host: own host, port: port, debug: debug])
}

bind describe = fn(text: string) string: {
	let cfg = parse(text):
		when syntax(line): func <- "line $line: expected `key = value`"
		when missing(key): func <- "missing required key `$key`"
		default bad_value(kv): func <- "invalid ${kv.0}: '${kv.1}'"
	"${cfg.host}:${cfg.port} (debug ${cfg.debug})"
}

bind main = fn(): {
	println(describe("# server\nhost = example.org\nport = 8080\ndebug = yes"))
	println(describe("host = localhost"))
	println(describe("port = 80"))
	println(describe("host = x\nport = eighty"))
	println(describe("host = x\nverbose"))
}
```

```output
example.org:8080 (debug true)
localhost:80 (debug false)
missing required key `host`
invalid port: 'eighty'
line 2: expected `key = value`
```

The chain must handle every failure. Because the last arm *names* `bad_value` (rather than a catch-all `default:`), adding a fourth failure to `ParseResult` later would make this chain fail to compile until it handles the new case.

## 6. Crossing a layer: `Result[T]` and `Error`

Precise result types are ideal right next to the operation. Further up, in code that just needs to load a config and report problems, the general `Result[T]` is more convenient. Its failure is an `Error`, which carries a message, an optional code, a cause, and a trace.

Convert at the boundary, adding context:

```lowlight fragment
bind load = fn(name: string, text: string) Result[Config]: {
	let cfg = parse(text):
		when syntax(line): func <- err(Error.coded("syntax", "$name:$line: expected `key = value`"))
		when missing(key): func <- err(Error.coded("missing", "$name: missing `$key`"))
		default bad_value(kv): func <- err(Error.coded("invalid", "$name: invalid ${kv.0} '${kv.1}'"))
	value(cfg)
}

bind start_server = fn(text: string) Result[int]: {
	let cfg = load("server.conf", text):
		default err(own e): func <- err(e.wrap("starting server"))
	value(cfg.port)
}
```

- `Error.coded(code, message)` gives the error a code that callers can test with `e.has_code("missing")`, without parsing the message.
- `e.wrap("starting server")` makes a new error whose cause is `e`. Messages read from the outside in: `starting server: server.conf: missing `host``.
- `own e` moves the error out of the result so it can be wrapped; errors are resources, and the compiler makes sure each one is either handled or passed on.

At the top of the program, report and decide:

```lowlight fragment
bind main = fn() int: {
	let port = start_server("port = 1"):
		default err(own e): {
			eprintln(e.summary())
			func <- if(e.has_code("missing")): 2 else: 1
		}
	println("listening on $port")
	0
}
```

## 7. When to panic instead

Sometimes a failure can only mean the program itself is wrong. If the config is built into the program, a parse error is a bug, not a user mistake. Say so with `default panic`:

```lowlight fragment
bind DEFAULTS = "host = localhost\nport = 8080"

bind defaults = fn() Config: parse(DEFAULTS): default panic
```

If it ever fails, the program stops with `unhandled failure ParseResult.syntax` and the source location, which is exactly what you want from a bug.

## What you practiced

| Situation | Tool |
|---|---|
| a value may legitimately be absent | `?T`, with `??` and `if(x as v)` |
| the caller must distinguish ways of failing | a custom result type and a result chain |
| failures pass through layers that add context | `Result[T]`, `Error.wrap`, `Error.coded` |
| failure means the program is wrong | `default panic`, `assert`, `panic` |

Read [Failure handling](/guides/errors.html) for the complete guide.

## Exercises

1. Report *all* syntax errors at once instead of stopping at the first. (Hint: change the `syntax` member to carry a `[int]`.)
2. Add a `timeout` key whose value is a duration such as `30s` or `5m`, with its own failure member.
3. Write tests for each failure using `test.assert_true` and a `match` on the result.
