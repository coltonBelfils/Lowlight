// std/core/error — Structured error chains and call-site stack traces.
//
// Provides [`Error`] and [`ErrorFrame`] for composing causal error chains with
// optional machine-readable error codes and `@track_caller` source locations.


// Single source-location frame inside an [`Error`] propagation trace.
@derive(Copyable)
define ErrorFrame struct [
	_file: string,
	_line: int,
	_col: int,
	_note?: ?string,
]

// Constructs an [`ErrorFrame`] with explicit coordinates and an optional contextual note.
//
// - `file`: Source file path.
// - `line`: 1-based line number.
// - `col`: 1-based column number.
// - `note?`: Optional contextual message attached at this frame.
const ErrorFrame.new = fn(file: string, line: int, col: int, note?: ?string) ErrorFrame:
	ErrorFrame[
		_file: file.copy(),
		_line: line,
		_col: col,
		_note?: if(note? as n): n.copy() else: none,
	]

// Returns a copy of the source file path recorded on this frame.
const ErrorFrame.file = fn(self) string: self._file.copy()

// Returns the 1-based line number recorded on this frame.
const ErrorFrame.line = fn(self) int: self._line

// Returns the 1-based column number recorded on this frame.
const ErrorFrame.col = fn(self) int: self._col

// Returns a copy of the optional note attached to this frame, or `none`.
const ErrorFrame.note = fn(self) ?string:
	if(self._note? as n): n.copy() else: none

// Formats the frame coordinates as `"file:line:col"`.
const ErrorFrame.location = fn(self) string: "${self._file}:${self._line}:${self._col}"

// Formats the frame as `"file:line:col — note"` (or `"file:line:col"` when no note is present).
const ErrorFrame.to_string = fn(self) string:
	if(self._note? as n): "${self._file}:${self._line}:${self._col} — $n"
	else: "${self._file}:${self._line}:${self._col}"

// Reports structural equality between two [`ErrorFrame`] values.
const ErrorFrame.eq = fn(self, other: ErrorFrame) bool:
	self._file == other._file and self._line == other._line and self._col == other._col and self._note? == other._note?

// Heap-safe contextual error value supporting wrapped causal chains, optional error codes,
// and accumulated [`ErrorFrame`] traces.
//
// Implements [`Printable`] and [`Equatable`].
//
// # Example
// ```lowlight
// let err = Error.coded("E_IO", "open failed").wrap("loading config")
// assert(err.has_code("E_IO"))
// ```
@derive(Copyable)
define Error struct [
	_message: string,
	_code?: ?string,
	_cause?: ?Error,
	_frames: [ErrorFrame],
]

// Creates a new root [`Error`] with `msg` and an initial [`ErrorFrame`] captured at the call site.
//
// - `msg`: Primary description of the failure.
@track_caller
const Error.new = fn(msg: string) Error: {
	let f = ErrorFrame[
		_file: caller_file(),
		_line: caller_line(),
		_col: caller_col(),
		_note?: msg.copy(),
	]
	Error[
		_message: msg.copy(),
		_code?: none,
		_cause?: none,
		_frames: [own f],
	]
}

// Creates a new root [`Error`] carrying an explicit `code` and initial call-site frame.
//
// - `code`: Stable symbolic identifier checked via [`Error.is`] and [`Error.has_code`].
// - `msg`: Primary human-readable error message.
@track_caller
const Error.coded = fn(code: string, msg: string) Error: {
	let f = ErrorFrame[
		_file: caller_file(),
		_line: caller_line(),
		_col: caller_col(),
		_note?: msg.copy(),
	]
	Error[
		_message: msg.copy(),
		_code?: code.copy(),
		_cause?: none,
		_frames: [own f],
	]
}

// Consumes `self` and returns a new [`Error`] wrapping it as the cause, with contextual `msg`
// and a caller frame.
//
// Moves the existing chain into the new layer (no copy), so wrapping is O(1) per layer.
//
// - `msg`: Outer context added at the wrapping call site.
@track_caller
const Error.wrap = fn(own self, msg: string) Error: {
	let f = ErrorFrame[
		_file: caller_file(),
		_line: caller_line(),
		_col: caller_col(),
		_note?: msg.copy(),
	]
	Error[
		_message: msg.copy(),
		_code?: none,
		_cause?: own self,
		_frames: [own f],
	]
}

// Consumes `self` and returns it with an additional caller-attributed [`ErrorFrame`] appended.
@track_caller
const Error.trace = fn(own self) Error: {
	let cp = own self
	cp._frames.push(ErrorFrame[
		_file: caller_file(),
		_line: caller_line(),
		_col: caller_col(),
		_note?: none,
	])
	cp
}

// Consumes `self` and returns it with its machine-readable code set to `code`.
const Error.with_code = fn(own self, code: string) Error: {
	let cp = own self
	cp._code? = code.copy()
	cp
}

// Returns a copy of the message stored directly on this error node (without causes).
const Error.message = fn(self) string: self._message.copy()

// Returns the first error code found walking down `self` through its cause chain, or `none`.
const Error.code = fn(self) ?string: {
	if(self._code? as c): func <- c.copy()
	if(self._cause? as cause): func <- cause.code()
	none
}

// Returns a copy of the wrapped inner cause [`Error`], or `none` if `self` is a root error.
const Error.cause = fn(self) ?Error:
	if(self._cause? as c): c.copy() else: none

// Walks the cause chain and returns a copy of the innermost root [`Error`].
const Error.root = fn(self) Error:
	if(self._cause? as c): c.root() else: self.copy()

// Returns a copy of the [`ErrorFrame`] list recorded directly on this error node.
const Error.frames = fn(self) [ErrorFrame]: self._frames.copy()

// Reports whether `self` or any wrapped cause in its chain matches `target`.
//
// Matches by code when `target` carries a code, otherwise by exact message equality
// on uncoded error nodes.
const Error.is = fn(self, target: Error) bool: {
	if(target._code? as tcode): {
		if(self._code? as ccode): if(ccode == tcode): func <- true
	} else if(self._code? == none and self._message == target._message): func <- true
	if(self._cause? as cause): func <- cause.is(target)
	false
}

// Reports whether `self` or any wrapped cause in its chain has error code `code`.
const Error.has_code = fn(self, code: string) bool: {
	if(self._code? as c): if(c == code): func <- true
	if(self._cause? as cause): func <- cause.has_code(code)
	false
}

// Formats the full causal message chain joined by `": "` without stack trace lines.
const Error.summary = fn(self) string: {
	let out = self._message.copy()
	if(self._cause? as c): {
		out.push(": ")
		out.push(c.summary())
	}
	out
}

const Error._append_trace = fn(self, out: &string) void: {
	let i = self._frames.len() - 1
	for(i >= 0): {
		out.push("\n  at ")
		out.push(self._frames[i].to_string())
		i -= 1
	}
	if(self._cause? as c): c._append_trace(&out)
}

// Formats the full error summary (prefixed with `[code]` when present) followed by the stack trace.
const Error.to_string = fn(self) string: {
	let out = ""
	if(self.code() as c): {
		out.push("[")
		out.push(c)
		out.push("] ")
	}
	out.push(self.summary())
	out.push("\ntrace:")
	self._append_trace(&out)
	out
}

// Reports structural equality across messages, codes, causes, and trace frames.
const Error.eq = fn(self, other: Error) bool: {
	if(self._message != other._message): func <- false
	if(self._code? != other._code?): func <- false
	if(self._cause? != other._cause?): func <- false
	if(self._frames.len() != other._frames.len()): func <- false
	for(i in 0..self._frames.len()): if(self._frames[i] != other._frames[i]): func <- false
	true
}
