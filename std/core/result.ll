// std/core/result — Recoverable error handling via the [`Result`] tagged union.
//
// Used throughout the standard library for operations that can fail with a rich
// [`Error`] stack trace instead of panicking.

// Represents either a successful value ([`Result.value`]) or a failure ([`Result.err`]).
//
// `Result` is an ordinary `result` declaration: names holding it end in `!`, and failures are
// handled with result chains (`load(path): default err(own e): func <- err(e.wrap("loading"))`),
// `??`, and `if(r! as v)`. Annotated `@must_use` so callers cannot silently discard failures.
//
// # Example
// ```lowlight
// bind divide = (a: int, b: int) Result[int]:
// 	if(b == 0): Result[int].fail_msg("division by zero")
// 	else: value(a / b)
// ```
@must_use
define Result[T] result [
	// Success value of type [`T`].
	value: T,
	// Failure carrying a contextual [`Error`].
	err: Error,
]

// Constructs a failed [`Result`] containing a new [`Error`] recorded at the call site.
//
// - `msg`: Human-readable description of the failure.
@track_caller
bind Result[T].fail_msg = (msg: string) Result[T]: err(Error.new(msg))

// Reports whether this result is [`Result.value`].
bind Result[T].is_ok = (self) bool:
	match(self):
		when value(_): true
		default: false

// Reports whether this result is [`Result.err`].
bind Result[T].is_err = (self) bool:
	match(self):
		when err(_): true
		default: false

// Consumes `self` and returns the inner [`value`] value, or `fallback` if it is [`err`].
//
// # Ownership
// Consumes both `self` and `fallback`.
bind Result[T].unwrap_or = (own self, fallback: own T) T:
	match(own self):
		when value(own val): val
		default err(_): own fallback

// Consumes `self` and extracts the contained [`value`] value.
//
// # Panics
// If `self` is [`Result.err`], panics with the error wrapped as
// `"called Result.unwrap on an error"`, attributed to the caller, with the full [`Error`] trace.
@track_caller
bind Result[T].unwrap = (own self) T:
	match(own self):
		when value(own val): val
		default err(own e): panic(e.wrap("called Result.unwrap on an error"))

// Consumes `self` and extracts the contained [`value`] value with a caller-attributed message.
//
// - `msg`: Context message the error is wrapped with if this panics.
//
// # Panics
// If `self` is [`Result.err`], panics with the error wrapped as `msg`, attributed to the caller,
// with the full [`Error`] trace.
@track_caller
bind Result[T].expect = (own self, msg: string) T:
	match(own self):
		when value(own val): val
		default err(own e): panic(e.wrap(msg))

// Consumes `self` and extracts the contained [`Error`].
//
// # Panics
// Calls [`panic`] (attributed to the caller) if `self` is [`Result.value`].
@track_caller
bind Result[T].unwrap_err = (own self) Error:
	match(own self):
		when err(own e): e
		default value(_): panic("called Result.unwrap_err on an ok result")

// Consumes `self` and returns the [`value`] value as `?T`, discarding any [`Error`].
bind Result[T].into_ok = (own self) ?T:
	match(own self):
		when value(own val): val
		default err(_): none

// Consumes `self` and returns the [`err`] value as `?Error`, discarding any [`value`] value.
bind Result[T].into_err = (own self) ?Error:
	match(own self):
		when err(own e): e
		default value(_): none

// Returns a copy of the [`Error`] if `self` is [`Result.err`], or `none` without consuming `self`.
bind Result[T].peek_err = (self) ?Error:
	match(self):
		when err(e): e.copy()
		default value(_): none

// Wraps any [`Result.err`] with an additional contextual error message, passing [`Result.value`] through unchanged.
//
// - `msg`: Contextual failure message recorded with caller source coordinates.
@track_caller
bind Result[T].wrap_err = (own self, msg: string) Result[T]:
	match(own self):
		when value(own val): value(own val)
		default err(own e): err(e.wrap(msg))

// Appends the caller location as a trace frame if `self` is [`Result.err`], passing [`Result.value`] through unchanged.
@track_caller
bind Result[T].trace_err = (own self) Result[T]:
	match(own self):
		when value(own val): value(own val)
		default err(own e): err(e.trace())

// Applies `f` to the success value, passing any failure through unchanged.
//
// ```lowlight
// let doubled! = parse_port("80").map((n: int) int: n * 2)
// ```
bind Result[T].map[U] = (own self, f: (T) U) Result[U]:
	match(own self):
		when value(own val): value(f(val))
		default err(own e): err(e)

// Applies `f` to the failure's [`Error`], passing a success value through unchanged. Use it to
// add context or change error codes without unpacking the result. `f` takes ownership of the
// error, so it can use consuming methods such as [`Error.wrap`] and [`Error.with_code`]:
//
// ```lowlight
// let cfg! = load(path).map_err((e: own Error) Error: e.wrap("loading settings"))
// ```
bind Result[T].map_err = (own self, f: (own Error) Error) Result[T]:
	match(own self):
		when value(own val): value(own val)
		default err(own e): err(f(own e))

// Calls `f` with the success value and returns its result, passing any failure through
// unchanged. Chains steps that can each fail.
//
// ```lowlight
// let port! = read_text(path).and_then((text: string) Result[int]: parse_port(text))
// ```
bind Result[T].and_then[U] = (own self, f: (T) Result[U]) Result[U]:
	match(own self):
		when value(own val): f(val)
		default err(own e): err(e)

// Consumes `self` and returns the success value, or computes a replacement from the [`Error`]
// with `f`. Unlike [`Result.unwrap_or`], the fallback is only computed on failure.
bind Result[T].unwrap_or_else = (own self, f: (Error) T) T:
	match(own self):
		when value(own val): val
		default err(own e): f(e)

@test("core Result combinators: map, map_err, and_then, unwrap_or_else")
bind test_core_result_combinators = (): {
	let ok!: Result[int] = value(21)
	let bad!: Result[int] = Result[int].fail_msg("no number")
	assert(ok!.map((n: int) int: n * 2).unwrap() == 42, "map transforms the value")
	assert(bad!.map((n: int) int: n * 2).is_err(), "map passes failures through")
	let seven!: Result[int] = value(7)
	assert(seven!.map((n: int) string: "n=$n").unwrap() == "n=7", "map can change the type")

	let coded! = Result[int].fail_msg("disk").map_err((e: own Error) Error: e.with_code("io"))
	assert(coded!.peek_err()?.has_code("io") ?? false, "map_err rewrites the error")
	let one!: Result[int] = value(1)
	assert(one!.map_err((e: own Error) Error: e.wrap("ctx")).unwrap() == 1, "map_err keeps values")

	let half = (n: int) Result[int]: if(n % 2 == 0): value(n / 2) else: Result[int].fail_msg("odd")
	let eight!: Result[int] = value(8)
	assert(eight!.and_then(half).and_then(half).unwrap() == 2, "and_then chains successes")
	let six!: Result[int] = value(6)
	let odd! = six!.and_then(half).and_then(half)
	assert(odd!.peek_err()?.message() == "odd", "and_then stops at the first failure")

	let fallback = Result[int].fail_msg("x").unwrap_or_else((e: Error) int: e.message().len())
	assert(fallback == 1, "unwrap_or_else computes from the error")
	let present!: Result[string] = value("hi" + "")
	assert(present!.unwrap_or_else((e: Error) string: "fallback") == "hi", "unwrap_or_else keeps values")
}
