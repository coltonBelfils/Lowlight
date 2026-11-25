// std/core/result — the standard result types, [`Result`] and [`Status`].
//
// Both are ordinary `result` declarations. Handle them with result chains, `??`, and
// `if(r! as v)`; the methods here cover what those can't: inspecting a result without consuming
// it, adding context while passing it on, and asserting success inside a longer expression.

// Either a success value ([`Result.value`]) or a failure ([`Result.err`]).
//
// Names holding a `Result` end in `!`. Handle it with a result chain:
//
// ```lowlight
// let cfg = load(path): default err(own e): func <- err(e.wrap("loading settings"))
// let port = parse_port(raw) ?? 8080
// ```
//
// Annotated `@must_use`, so a result can't be silently discarded.
@must_use
@derive(Equatable, Copyable, Printable)
define Result[T] result [
	// Success value of type [`T`].
	value: T,
	// Failure carrying a contextual [`Error`].
	err: Error,
]

// A failed [`Result`] holding a new [`Error`] with message `msg`, recorded at the call site.
// Shorthand for `err(Error.new(msg))` where no `Result` type is expected.
@track_caller
const Result[T].fail_msg = fn(msg: string) Result[T]: err(Error.new(msg))

// Reports whether this result holds a value, without consuming it.
const Result[T].is_ok = fn(self) bool:
	match(self):
		when value(_): true
		default: false

// Reports whether this result holds a failure, without consuming it.
const Result[T].is_err = fn(self) bool:
	match(self):
		when err(_): true
		default: false

// A copy of the failure's [`Error`], or `none` if this result holds a value. Doesn't consume
// `self`.
const Result[T].peek_err = fn(self) ?Error:
	match(self):
		when err(e): e.copy()
		default value(_): none

// Consumes `self` and returns the value. The expression form of `r!: default panic`, for use
// inside a longer expression: `read_file(path).unwrap().len()`.
//
// # Panics
// If `self` is a failure, panics with its [`Error`] wrapped as
// `"called Result.unwrap on an error"`, attributed to the caller, with the full trace.
@track_caller
const Result[T].unwrap = fn(own self) T:
	match(own self):
		when value(own val): val
		default err(own e): panic(e.wrap("called Result.unwrap on an error"))

// Consumes `self` and returns the value, like [`Result.unwrap`] with a message of your own.
//
// # Panics
// If `self` is a failure, panics with its [`Error`] wrapped as `msg`, attributed to the caller,
// with the full trace.
@track_caller
const Result[T].expect = fn(own self, msg: string) T:
	match(own self):
		when value(own val): val
		default err(own e): panic(e.wrap(msg))

// Consumes `self` and returns the failure's [`Error`]. Mostly for tests that expect a failure.
//
// # Panics
// If `self` holds a value (attributed to the caller).
@track_caller
const Result[T].unwrap_err = fn(own self) Error:
	match(own self):
		when err(own e): e
		default value(_): panic("called Result.unwrap_err on a value")

// Wraps a failure's [`Error`] with the context `msg`, passing a value through unchanged:
// `func <- load(path).wrap_err("loading \"$path\"")`.
@track_caller
const Result[T].wrap_err = fn(own self, msg: string) Result[T]:
	match(own self):
		when value(own val): value(own val)
		default err(own e): err(e.wrap(msg))

// Adds the caller's location to a failure's trace, passing a value through unchanged.
@track_caller
const Result[T].trace_err = fn(own self) Result[T]:
	match(own self):
		when value(own val): value(own val)
		default err(own e): err(e.trace())

// The result of an operation that produces nothing when it succeeds, such as writing or
// removing a file: [`Status.value`] (no payload) or a failure ([`Status.err`]).
//
// `Status` is to `void` what [`Result`] is to a value type. A chain on it is a statement; its
// arms handle the failures:
//
// ```lowlight
// fs.write_file(path, text): default panic
// fs.write_file(path, text): default err(own e): func <- err(e.wrap("saving \"$path\""))
// ```
//
// Operations whose callers need to tell failures apart declare their own unit result instead,
// such as [`fs.RemoveResult`] with its `not_found`.
@must_use
@derive(Equatable, Copyable, Printable)
define Status result [
	// Success; there is no value.
	value,
	// Failure carrying a contextual [`Error`].
	err: Error,
]

// A failed [`Status`] holding a new [`Error`] with message `msg`, recorded at the call site.
@track_caller
const Status.fail_msg = fn(msg: string) Status: err(Error.new(msg))

// Reports whether this status is a success, without consuming it.
const Status.is_ok = fn(self) bool:
	match(self):
		when value: true
		default: false

// Reports whether this status is a failure, without consuming it.
const Status.is_err = fn(self) bool:
	match(self):
		when err(_): true
		default: false

// A copy of the failure's [`Error`], or `none` on success. Doesn't consume `self`.
const Status.peek_err = fn(self) ?Error:
	match(self):
		when err(e): e.copy()
		default value: none

// Consumes `self`, panicking on a failure. The expression form of `s: default panic`.
//
// # Panics
// If `self` is a failure, panics with its [`Error`] wrapped as
// `"called Status.unwrap on an error"`, attributed to the caller, with the full trace.
@track_caller
const Status.unwrap = fn(own self):
	match(own self):
		when value: {}
		default err(own e): panic(e.wrap("called Status.unwrap on an error"))

// Consumes `self`, panicking with `msg` on a failure.
//
// # Panics
// If `self` is a failure, panics with its [`Error`] wrapped as `msg`, attributed to the caller,
// with the full trace.
@track_caller
const Status.expect = fn(own self, msg: string):
	match(own self):
		when value: {}
		default err(own e): panic(e.wrap(msg))

// Consumes `self` and returns the failure's [`Error`]. Mostly for tests that expect a failure.
//
// # Panics
// If `self` is a success (attributed to the caller).
@track_caller
const Status.unwrap_err = fn(own self) Error:
	match(own self):
		when err(own e): e
		default value: panic("called Status.unwrap_err on a success")

// Wraps a failure's [`Error`] with the context `msg`, passing success through unchanged.
@track_caller
const Status.wrap_err = fn(own self, msg: string) Status:
	match(own self):
		when value: value
		default err(own e): err(e.wrap(msg))

// Adds the caller's location to a failure's trace, passing success through unchanged.
@track_caller
const Status.trace_err = fn(own self) Status:
	match(own self):
		when value: value
		default err(own e): err(e.trace())

@test("core Result: inspection, context, and assertions")
bind test_core_result_methods = fn(): {
	let ok!: Result[int] = value(21)
	let bad!: Result[int] = Result[int].fail_msg("no number")
	assert(ok!.is_ok() and not ok!.is_err(), "value is ok")
	assert(bad!.is_err() and not bad!.is_ok(), "err is an error")
	assert(ok!.peek_err() == none, "peek_err on a value is none")
	assert(bad!.peek_err()?.message() == "no number", "peek_err copies the error")
	assert(ok!.copy().unwrap() == 21, "unwrap returns the value")
	assert(Result[string].value("x").expect("needs x") == "x", "expect returns the value")
	assert(
		bad!.wrap_err("parsing").unwrap_err().summary() == "parsing: no number",
		"wrap_err adds context",
	)
	let one!: Result[int] = value(1)
	assert(one!.wrap_err("ctx").unwrap() == 1, "wrap_err keeps values")
	let traced! = Result[int].fail_msg("deep").trace_err()
	assert(traced!.unwrap_err().frames().len() == 2, "trace_err adds a frame")
	assert(ok! == Result[int].value(21), "Result is Equatable")
	assert(ok!.to_string() == "Result.value(21)", "Result is Printable")
}

@test("core Status: chains, inspection, and assertions")
bind test_core_status = fn(): {
	let ok!: Status = value
	let bad!: Status = Status.fail_msg("disk full")
	assert(ok!.is_ok() and not ok!.is_err(), "value is ok")
	assert(bad!.is_err() and not bad!.is_ok(), "err is an error")
	assert(bad!.peek_err()?.message() == "disk full", "peek_err copies the error")
	assert(
		bad!.wrap_err("saving").unwrap_err().summary() == "saving: disk full",
		"wrap_err adds context",
	)
	Status.value.unwrap()
	Status.value.expect("must succeed")
	let hits = 0
	Status.fail_msg("boom"):
		default err(own e): hits = hits + e.message().len()
	Status.value: default panic
	assert(hits == 4, "a chain handles the failure")
	assert(ok! == Status.value, "Status is Equatable")
	assert(ok!.to_string() == "Status.value", "Status is Printable")
}
