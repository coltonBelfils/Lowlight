// std/core/result — Recoverable error handling via the [`Result`] tagged union.
//
// Used throughout the standard library for operations that can fail with a rich
// [`Error`] stack trace instead of panicking.


// Represents either a successful value ([`Result.Ok`]) or a failure ([`Result.Err`]).
//
// Annotated `@must_use` so callers cannot silently discard failures.
//
// # Example
// ```lowlight
// bind divide = (a: int, b: int) Result[int]:
// 	if(b == 0): Result[int].fail_msg("division by zero")
// 	else: Result[int].ok(a / b)
// ```
@must_use
define Result[T] enum [
	// Success value of type [`T`].
	Ok: T,
	// Failure carrying a contextual [`Error`].
	Err: Error,
]

// Constructs a successful [`Result`] containing `val`.
//
// # Ownership
// Consumes ownership of `val`.
bind Result[T].ok = (val: own T) Result[T]: Result[T].Ok(own val)

// Constructs a failed [`Result`] wrapping an existing [`Error`].
//
// # Ownership
// Consumes ownership of `e`.
bind Result[T].fail = (e: own Error) Result[T]: Result[T].Err(own e)

// Constructs a failed [`Result`] containing a new [`Error`] recorded at the call site.
//
// - `msg`: Human-readable description of the failure.
@track_caller
bind Result[T].fail_msg = (msg: string) Result[T]: Result[T].Err(Error.new(msg))

// Reports whether this result is [`Result.Ok`].
bind Result[T].is_ok = (self) bool:
	match(self):
		when Result.Ok(_): true
		default: false

// Reports whether this result is [`Result.Err`].
bind Result[T].is_err = (self) bool:
	match(self):
		when Result.Err(_): true
		default: false

// Consumes `self` and returns the inner [`Ok`] value, or `fallback` if it is [`Err`].
//
// # Ownership
// Consumes both `self` and `fallback`.
bind Result[T].unwrap_or = (own self, fallback: own T) T:
	match(own self):
		when Result.Ok(own val): val
		default Result.Err(_): own fallback

// Consumes `self` and extracts the contained [`Ok`] value.
//
// # Panics
// If `self` is [`Result.Err`], panics with the error wrapped as
// `"called Result.unwrap on an error"`, attributed to the caller, with the full [`Error`] trace.
@track_caller
bind Result[T].unwrap = (own self) T:
	match(own self):
		when Result.Ok(own val): val
		default Result.Err(own err): panic(err.wrap("called Result.unwrap on an error"))

// Consumes `self` and extracts the contained [`Ok`] value with a caller-attributed message.
//
// - `msg`: Context message the error is wrapped with if this panics.
//
// # Panics
// If `self` is [`Result.Err`], panics with the error wrapped as `msg`, attributed to the caller,
// with the full [`Error`] trace.
@track_caller
bind Result[T].expect = (own self, msg: string) T:
	match(own self):
		when Result.Ok(own val): val
		default Result.Err(own err): panic(err.wrap(msg))

// Consumes `self` and extracts the contained [`Error`].
//
// # Panics
// Calls [`panic`] (attributed to the caller) if `self` is [`Result.Ok`].
@track_caller
bind Result[T].unwrap_err = (own self) Error:
	match(own self):
		when Result.Err(own err): err
		default Result.Ok(_): panic("called Result.unwrap_err on an ok result")

// Consumes `self` and returns the [`Ok`] value as `?T`, discarding any [`Error`].
bind Result[T].into_ok = (own self) ?T:
	match(own self):
		when Result.Ok(own val): val
		default Result.Err(_): none

// Consumes `self` and returns the [`Err`] value as `?Error`, discarding any [`Ok`] value.
bind Result[T].into_err = (own self) ?Error:
	match(own self):
		when Result.Err(own err): err
		default Result.Ok(_): none

// Returns a copy of the [`Error`] if `self` is [`Result.Err`], or `none` without consuming `self`.
bind Result[T].peek_err = (self) ?Error:
	match(self):
		when Result.Err(err): err.copy()
		default Result.Ok(_): none

// Wraps any [`Result.Err`] with an additional contextual error message, passing [`Result.Ok`] through unchanged.
//
// - `msg`: Contextual failure message recorded with caller source coordinates.
@track_caller
bind Result[T].wrap_err = (own self, msg: string) Result[T]:
	match(own self):
		when Result.Ok(own val): Result[T].Ok(own val)
		default Result.Err(own err): Result[T].Err(err.wrap(msg))

// Appends the caller location as a trace frame if `self` is [`Result.Err`], passing [`Result.Ok`] through unchanged.
@track_caller
bind Result[T].trace_err = (own self) Result[T]:
	match(own self):
		when Result.Ok(own val): Result[T].Ok(own val)
		default Result.Err(own err): Result[T].Err(err.trace())
