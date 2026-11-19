// std/test — Test assertion functions and structured diff helpers (`@test_only`).
//
// Provides aligned single-line and multi-line visual diffs via [`format_diff`] along
// with typed assertions ([`assert_eq`], [`assert_approx_eq`], [`assert_ne`], [`assert_gt`],
// [`assert_contains`], [`assert_starts_with`], [`assert_ends_with`], [`assert_is_ok`],
// [`assert_ok_eq`], [`assert_ok_gt`], [`assert_ok_contains`], [`assert_some`], [`assert_none`],
// and `_msg` variants).

// -----------------------------------------------------------------------------
// Failure Diagnostics & Diff Formatting Helpers
// -----------------------------------------------------------------------------

@test_only
bind _str_has_newline = (s: string) bool: {
	for(ch in s): if(ch == '\n'): func <- true
	false
}

@test_only
bind _repeat_space = (n: int) string: {
	let out = ""
	for(i in 0..n): out.push(" ")
	out
}

@test_only
bind _split_lines = (s: string) [string]: {
	let out: [string] = []
	let cur = ""
	for(ch in s):
		if(ch == '\n'): {
			out.push(own cur)
			cur = ""
		} else: cur.push_char(ch)
	out.push(own cur)
	out
}

@test_only
bind _str_contains_sub = (haystack: string, needle: string) bool: {
	if(needle.len() == 0): func <- true
	if(needle.len() > haystack.len()): func <- false
	let max_start = haystack.len() - needle.len()
	for(i in 0..=max_start): if(haystack[i..i + needle.len()] == needle): func <- true
	false
}

// Formats `actual_str` vs `expected_str` with aligned headers and a mismatch pointer.
//
// Automatically delegates to single-line or line-by-line diff formatting when
// either input contains a newline.
@test_only
@track_caller
bind format_diff = (actual_str: string, expected_str: string) string:
	if(_str_has_newline(actual_str) or _str_has_newline(expected_str)):
		_format_multiline_diff(actual_str, expected_str)
	else: _format_single_line_diff(actual_str, expected_str)

@test_only
bind _find_char_diff = (act_chars: [char], exp_chars: [char]) int: {
	let min_len = if(act_chars.len() < exp_chars.len()): act_chars.len() else: exp_chars.len()
	for(i in 0..min_len):
		if(act_chars[i] != exp_chars[i]): func <- i
	-1
}

// Formats single-line got vs expected with aligned columns and a pointer at the first mismatch.
@test_only
@track_caller
bind _format_single_line_diff = (actual_str: string, expected_str: string) string: {
	let act_chars = actual_str.chars()
	let exp_chars = expected_str.chars()
	let act_len = act_chars.len()
	let exp_len = exp_chars.len()
	let diff_idx = _find_char_diff(act_chars, exp_chars)

	let out = "  expected: " + expected_str + "\n       got: " + actual_str
	if(diff_idx >= 0): {
		let pointer = _repeat_space(12 + diff_idx) + "^ diff at index " + diff_idx.to_string()
		out = out + "\n" + pointer
	} else if(act_len > exp_len): {
		let extra = act_len - exp_len
		let pointer = _repeat_space(12 + exp_len) + "^ unexpected " + extra.to_string() + " extra char(s) in got"
		out = out + "\n" + pointer
	} else if(exp_len > act_len): {
		let missing = exp_len - act_len
		let pointer = _repeat_space(12 + act_len) + "^ missing " + missing.to_string() + " expected char(s)"
		out = out + "\n" + pointer
	}
	out
}

// Formats multiline got vs expected showing the mismatched line number, aligned lines, and character pointer.
@test_only
@track_caller
bind _format_multiline_diff = (actual_str: string, expected_str: string) string: {
	let act_lines = _split_lines(actual_str)
	let exp_lines = _split_lines(expected_str)
	let min_count = if(act_lines.len() < exp_lines.len()): act_lines.len() else: exp_lines.len()

	let diff_line = -1
	for(i in 0..min_count):
		if(act_lines[i] != exp_lines[i]): {
			diff_line = i
			for <-
		}
	if(diff_line == -1): diff_line = min_count

	let out = "  first mismatch at line " + (diff_line + 1).to_string() + ":\n"
	if(diff_line < exp_lines.len() and diff_line < act_lines.len()): {
		let exp_line = exp_lines[diff_line].copy()
		let act_line = act_lines[diff_line].copy()
		out = out + "    expected: " + exp_line + "\n"
		out = out + "         got: " + act_line

		// Find character mismatch within the line
		let act_chars = act_line.chars()
		let exp_chars = exp_line.chars()
		let char_idx = _find_char_diff(act_chars, exp_chars)
		if(char_idx == -1 and act_chars.len() != exp_chars.len()):
			char_idx = if(act_chars.len() < exp_chars.len()): act_chars.len() else: exp_chars.len()
		if(char_idx >= 0): {
			let pointer = _repeat_space(14 + char_idx) + "^"
			out = out + "\n" + pointer
		}
	} else if(diff_line < exp_lines.len()): {
		out = out + "    expected line " + (diff_line + 1).to_string() + ": " + exp_lines[diff_line] + "\n"
		out = out + "         got: <end of input>"
	} else if(diff_line < act_lines.len()): {
		out = out + "    expected: <end of input>\n"
		out = out + "         got line " + (diff_line + 1).to_string() + ": " + act_lines[diff_line]
	}

	if(act_lines.len() != exp_lines.len()):
		out = out + "\n  (total line count: expected " + exp_lines.len().to_string() + ", got " + act_lines
			.len()
			.to_string() + ")"
	out
}

// -----------------------------------------------------------------------------
// Assertions
// -----------------------------------------------------------------------------

@test_only
bind _check_approx_eq = (actual: float, expected: float, eps: float) ?string: {
	if(not (eps == eps) or eps < 0.0):
		func <- "tolerance must be non-negative and not NaN (got " + eps.to_string() + ")"
	if(not (actual == actual) or not (expected == expected)):
		func <- "expected approximately " + expected.to_string() + " (+/- " + eps.to_string() + "), got " + actual.to_string() + " (NaN is never approximately equal)"
	if(actual == expected): func <- none
	let diff = if(actual > expected): actual - expected else: expected - actual
	if(not (diff <= eps)):
		func <- "expected approximately " + expected.to_string() + " (+/- " + eps.to_string() + "), got " + actual.to_string() + " (diff: " + diff.to_string() + ")"
	none
}

@test_only
bind _format_str_match_err = (expectation: string, actual: string, target_label: string, target: string) string:
	"expected string to " + expectation + "\n     actual: " + actual + "\n" + target_label + ": " + target

// Asserts that `actual` is within `eps` of `expected` (`|actual - expected| <= eps`).
//
// # Panics
// Panics if `eps` is negative or `NaN`, either value is `NaN`, or `|actual - expected| > eps`.
@test_only
@track_caller
bind assert_approx_eq = (actual: float, expected: float, eps: float):
	if(_check_approx_eq(actual, expected, eps) as detail):
		panic("assertion failed: " + detail)

// Asserts that `actual` is within `eps` of `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `eps` is negative or `NaN`, either value is `NaN`, or `|actual - expected| > eps`.
@test_only
@track_caller
bind assert_approx_eq_msg = (actual: float, expected: float, eps: float, msg: string):
	if(_check_approx_eq(actual, expected, eps) as detail):
		panic("assertion failed: " + msg + " (" + detail + ")")

// Asserts that `actual` equals `expected` (`actual == expected`).
//
// # Panics
// Panics with an aligned visual diff produced by [`format_diff`] when the
// two values differ.
@test_only
@track_caller
bind assert_eq[T: Equatable + Printable] = (actual: T, expected: T):
	if(actual != expected): {
		let diff = format_diff(actual.to_string(), expected.to_string())
		panic("assertion failed: values are not equal\n" + diff)
	}

// Asserts that `actual` equals `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` and a [`format_diff`] output if `actual != expected`.
@test_only
@track_caller
bind assert_eq_msg[T: Equatable + Printable] = (actual: T, expected: T, msg: string):
	if(actual != expected): {
		let diff = format_diff(actual.to_string(), expected.to_string())
		panic("assertion failed: " + msg + "\n" + diff)
	}

// Asserts that `actual` does not equal `unexpected` (`actual != unexpected`).
//
// # Panics
// Panics if `actual == unexpected`.
@test_only
@track_caller
bind assert_ne[T: Equatable + Printable] = (actual: T, unexpected: T):
	if(actual == unexpected): {
		let s = unexpected.to_string()
		panic(
			"assertion failed: values must not be equal\n  unexpected: " + s + "\n         got: " + s,
		)
	}

// Asserts that `actual` does not equal `unexpected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual == unexpected`.
@test_only
@track_caller
bind assert_ne_msg[T: Equatable + Printable] = (actual: T, unexpected: T, msg: string):
	if(actual == unexpected): {
		let s = unexpected.to_string()
		panic(
			"assertion failed: " + msg + " (values must not be equal)\n  unexpected: " + s + "\n         got: " + s,
		)
	}

// Asserts that `cond` evaluates to `true`.
//
// # Panics
// Panics if `cond` is `false`.
@test_only
@track_caller
bind assert_true = (cond: bool):
	if(not cond): panic("assertion failed: expected true, but condition evaluated to false")

// Asserts that `cond` is `true`, failing with custom context `msg`.
//
// # Panics
// Panics with `msg` if `cond` is `false`.
@test_only
@track_caller
bind assert_true_msg = (cond: bool, msg: string):
	if(not cond): panic("assertion failed: " + msg + " (expected true, but got false)")

// Asserts that `cond` evaluates to `false`.
//
// # Panics
// Panics if `cond` is `true`.
@test_only
@track_caller
bind assert_false = (cond: bool):
	if(cond): panic("assertion failed: expected false, but condition evaluated to true")

// Asserts that `cond` is `false`, failing with custom context `msg`.
//
// # Panics
// Panics with `msg` if `cond` is `true`.
@test_only
@track_caller
bind assert_false_msg = (cond: bool, msg: string):
	if(cond): panic("assertion failed: " + msg + " (expected false, but got true)")

@test_only
@track_caller
bind _panic_cmp[T: Printable] = (actual: T, expected: T, op: string, msg: string):
	if(msg.len() > 0):
		panic(
			"assertion failed: " + msg + " (expected actual " + op + " threshold)\n     actual: " + actual
				.to_string() + "\n  threshold: " + expected.to_string(),
		)
	else:
		panic(
			"assertion failed: expected actual " + op + " threshold\n     actual: " + actual.to_string() + "\n  threshold: " + expected
				.to_string(),
		)

// Asserts that `actual` is strictly greater than `expected` (`actual > expected`).
//
// # Panics
// Panics if `actual <= expected`.
@test_only
@track_caller
bind assert_gt[T: Comparable + Printable] = (actual: T, expected: T):
	if(actual <= expected): _panic_cmp(actual, expected, ">", "")

// Asserts that `actual` is strictly greater than `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual <= expected`.
@test_only
@track_caller
bind assert_gt_msg[T: Comparable + Printable] = (actual: T, expected: T, msg: string):
	if(actual <= expected): _panic_cmp(actual, expected, ">", msg)

// Asserts that `actual` is strictly less than `expected` (`actual < expected`).
//
// # Panics
// Panics if `actual >= expected`.
@test_only
@track_caller
bind assert_lt[T: Comparable + Printable] = (actual: T, expected: T):
	if(actual >= expected): _panic_cmp(actual, expected, "<", "")

// Asserts that `actual` is strictly less than `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual >= expected`.
@test_only
@track_caller
bind assert_lt_msg[T: Comparable + Printable] = (actual: T, expected: T, msg: string):
	if(actual >= expected): _panic_cmp(actual, expected, "<", msg)

// Asserts that `actual` is greater than or equal to `expected` (`actual >= expected`).
//
// # Panics
// Panics if `actual < expected`.
@test_only
@track_caller
bind assert_ge[T: Comparable + Printable] = (actual: T, expected: T):
	if(actual < expected): _panic_cmp(actual, expected, ">=", "")

// Asserts that `actual` is greater than or equal to `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual < expected`.
@test_only
@track_caller
bind assert_ge_msg[T: Comparable + Printable] = (actual: T, expected: T, msg: string):
	if(actual < expected): _panic_cmp(actual, expected, ">=", msg)

// Asserts that `actual` is less than or equal to `expected` (`actual <= expected`).
//
// # Panics
// Panics if `actual > expected`.
@test_only
@track_caller
bind assert_le[T: Comparable + Printable] = (actual: T, expected: T):
	if(actual > expected): _panic_cmp(actual, expected, "<=", "")

// Asserts that `actual` is less than or equal to `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual > expected`.
@test_only
@track_caller
bind assert_le_msg[T: Comparable + Printable] = (actual: T, expected: T, msg: string):
	if(actual > expected): _panic_cmp(actual, expected, "<=", msg)

// Asserts that `actual` contains `substring`.
//
// # Panics
// Panics if `actual` does not contain `substring`.
@test_only
@track_caller
bind assert_contains = (actual: string, substring: string):
	if(not actual.contains(substring)):
		panic("assertion failed: " + _format_str_match_err("contain substring", actual, "  substring", substring))

// Asserts that `actual` contains `substring`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual` does not contain `substring`.
@test_only
@track_caller
bind assert_contains_msg = (actual: string, substring: string, msg: string):
	if(not actual.contains(substring)):
		panic("assertion failed: " + msg + "\n" + _format_str_match_err("contain substring", actual, "  substring", substring))

// Asserts that `actual` starts with `prefix`.
//
// # Panics
// Panics if `actual` does not start with `prefix`.
@test_only
@track_caller
bind assert_starts_with = (actual: string, prefix: string):
	if(not actual.starts_with(prefix)):
		panic("assertion failed: " + _format_str_match_err("start with prefix", actual, "     prefix", prefix))

// Asserts that `actual` starts with `prefix`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual` does not start with `prefix`.
@test_only
@track_caller
bind assert_starts_with_msg = (actual: string, prefix: string, msg: string):
	if(not actual.starts_with(prefix)):
		panic("assertion failed: " + msg + "\n" + _format_str_match_err("start with prefix", actual, "     prefix", prefix))

// Asserts that `actual` ends with `suffix`.
//
// # Panics
// Panics if `actual` does not end with `suffix`.
@test_only
@track_caller
bind assert_ends_with = (actual: string, suffix: string):
	if(not actual.ends_with(suffix)):
		panic("assertion failed: " + _format_str_match_err("end with suffix", actual, "     suffix", suffix))

// Asserts that `actual` ends with `suffix`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual` does not end with `suffix`.
@test_only
@track_caller
bind assert_ends_with_msg = (actual: string, suffix: string, msg: string):
	if(not actual.ends_with(suffix)):
		panic("assertion failed: " + msg + "\n" + _format_str_match_err("end with suffix", actual, "     suffix", suffix))

// Asserts that [`Result`] `res` holds an `Ok` value.
//
// # Panics
// Panics with the formatted error description if `res` is `Err`.
@test_only
@track_caller
bind assert_is_ok[T] = (res: &Result[T]):
	if(res.is_err()): {
		let err_str = if(res.peek_err() as err): err.to_string() else: "<unknown error>"
		panic("assertion failed: expected Result to be Ok, but got Err\n  error: " + err_str)
	}

// Asserts that [`Result`] `res` holds an `Ok` value, failing with context `msg`.
//
// # Panics
// Panics with `msg` and the underlying error if `res` is `Err`.
@test_only
@track_caller
bind assert_is_ok_msg[T] = (res: &Result[T], msg: string):
	if(res.is_err()): {
		let err_str = if(res.peek_err() as err): err.to_string() else: "<unknown error>"
		panic(
			"assertion failed: " + msg + " (expected Result to be Ok, got Err)\n  error: " + err_str,
		)
	}

// Asserts that [`Result`] `res` holds an `Err` value.
//
// # Panics
// Panics if `res` is `Ok`.
@test_only
@track_caller
bind assert_is_err[T] = (res: &Result[T]):
	if(res.is_ok()): panic("assertion failed: expected Result to be Err, but got Ok")

// Asserts that [`Result`] `res` holds an `Err` value, failing with context `msg`.
//
// # Panics
// Panics with `msg` if `res` is `Ok`.
@test_only
@track_caller
bind assert_is_err_msg[T] = (res: &Result[T], msg: string):
	if(res.is_ok()): panic("assertion failed: " + msg + " (expected Result to be Err, but got Ok)")

@test_only
@track_caller
bind _panic_result_not_ok = (err: Error, msg: string):
	if(msg.len() > 0):
		panic(
			"assertion failed: " + msg + " (expected Result to be Ok, got Err)\n  error: " + err.to_string(),
		)
	else:
		panic("assertion failed: expected Result to be Ok, but got Err\n  error: " + err.to_string())

// Asserts that [`Result`] `res` is `Ok` and its value equals `expected` (`val == expected`).
//
// # Panics
// Panics with the underlying error if `res` is `Err`, or a formatted diff if `val != expected`.
@test_only
@track_caller
bind assert_ok_eq[T: Equatable + Printable] = (res: Result[T], expected: T):
	match(res):
		when Result.Ok(val): assert_eq(val, expected)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its value equals `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_eq_msg[T: Equatable + Printable] = (res: Result[T], expected: T, msg: string):
	match(res):
		when Result.Ok(val): assert_eq_msg(val, expected, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its value does not equal `unexpected` (`val != unexpected`).
@test_only
@track_caller
bind assert_ok_ne[T: Equatable + Printable] = (res: Result[T], unexpected: T):
	match(res):
		when Result.Ok(val): assert_ne(val, unexpected)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its value does not equal `unexpected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_ne_msg[T: Equatable + Printable] = (res: Result[T], unexpected: T, msg: string):
	match(res):
		when Result.Ok(val): assert_ne_msg(val, unexpected, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its float value is within `eps` of `expected`.
@test_only
@track_caller
bind assert_ok_approx_eq = (res: Result[float], expected: float, eps: float):
	match(res):
		when Result.Ok(val): assert_approx_eq(val, expected, eps)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its float value is within `eps` of `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_approx_eq_msg = (res: Result[float], expected: float, eps: float, msg: string):
	match(res):
		when Result.Ok(val): assert_approx_eq_msg(val, expected, eps, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok(true)`.
@test_only
@track_caller
bind assert_ok_true = (res: Result[bool]):
	match(res):
		when Result.Ok(val): assert_true(val)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok(true)`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_true_msg = (res: Result[bool], msg: string):
	match(res):
		when Result.Ok(val): assert_true_msg(val, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok(false)`.
@test_only
@track_caller
bind assert_ok_false = (res: Result[bool]):
	match(res):
		when Result.Ok(val): assert_false(val)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok(false)`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_false_msg = (res: Result[bool], msg: string):
	match(res):
		when Result.Ok(val): assert_false_msg(val, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its value is strictly greater than `expected` (`val > expected`).
@test_only
@track_caller
bind assert_ok_gt[T: Comparable + Printable] = (res: Result[T], expected: T):
	match(res):
		when Result.Ok(val): assert_gt(val, expected)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its value is strictly greater than `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_gt_msg[T: Comparable + Printable] = (res: Result[T], expected: T, msg: string):
	match(res):
		when Result.Ok(val): assert_gt_msg(val, expected, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its value is strictly less than `expected` (`val < expected`).
@test_only
@track_caller
bind assert_ok_lt[T: Comparable + Printable] = (res: Result[T], expected: T):
	match(res):
		when Result.Ok(val): assert_lt(val, expected)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its value is strictly less than `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_lt_msg[T: Comparable + Printable] = (res: Result[T], expected: T, msg: string):
	match(res):
		when Result.Ok(val): assert_lt_msg(val, expected, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its value is greater than or equal to `expected` (`val >= expected`).
@test_only
@track_caller
bind assert_ok_ge[T: Comparable + Printable] = (res: Result[T], expected: T):
	match(res):
		when Result.Ok(val): assert_ge(val, expected)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its value is greater than or equal to `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_ge_msg[T: Comparable + Printable] = (res: Result[T], expected: T, msg: string):
	match(res):
		when Result.Ok(val): assert_ge_msg(val, expected, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its value is less than or equal to `expected` (`val <= expected`).
@test_only
@track_caller
bind assert_ok_le[T: Comparable + Printable] = (res: Result[T], expected: T):
	match(res):
		when Result.Ok(val): assert_le(val, expected)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its value is less than or equal to `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_le_msg[T: Comparable + Printable] = (res: Result[T], expected: T, msg: string):
	match(res):
		when Result.Ok(val): assert_le_msg(val, expected, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its string value contains `substring`.
@test_only
@track_caller
bind assert_ok_contains = (res: Result[string], substring: string):
	match(res):
		when Result.Ok(val): assert_contains(val, substring)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its string value contains `substring`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_contains_msg = (res: Result[string], substring: string, msg: string):
	match(res):
		when Result.Ok(val): assert_contains_msg(val, substring, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its string value starts with `prefix`.
@test_only
@track_caller
bind assert_ok_starts_with = (res: Result[string], prefix: string):
	match(res):
		when Result.Ok(val): assert_starts_with(val, prefix)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its string value starts with `prefix`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_starts_with_msg = (res: Result[string], prefix: string, msg: string):
	match(res):
		when Result.Ok(val): assert_starts_with_msg(val, prefix, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and its string value ends with `suffix`.
@test_only
@track_caller
bind assert_ok_ends_with = (res: Result[string], suffix: string):
	match(res):
		when Result.Ok(val): assert_ends_with(val, suffix)
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and its string value ends with `suffix`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_ends_with_msg = (res: Result[string], suffix: string, msg: string):
	match(res):
		when Result.Ok(val): assert_ends_with_msg(val, suffix, msg)
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Ok` and satisfies predicate `check(val)`.
@test_only
@track_caller
bind assert_ok_with[T: Printable] = (res: Result[T], check: (T) bool):
	match(res):
		when Result.Ok(val):
			if(not check(val)):
				panic("assertion failed: Ok value did not satisfy predicate\n  actual: " + val.to_string())
		default Result.Err(err): _panic_result_not_ok(err, "")

// Asserts that [`Result`] `res` is `Ok` and satisfies predicate `check(val)`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_with_msg[T: Printable] = (res: Result[T], check: (T) bool, msg: string):
	match(res):
		when Result.Ok(val):
			if(not check(val)):
				panic("assertion failed: " + msg + " (Ok value did not satisfy predicate)\n  actual: " + val.to_string())
		default Result.Err(err): _panic_result_not_ok(err, msg)

// Asserts that [`Result`] `res` is `Err` and its formatted error contains `substring`.
@test_only
@track_caller
bind assert_err_contains[T] = (res: Result[T], substring: string):
	match(res):
		when Result.Err(err): assert_contains(err.to_string(), substring)
		default Result.Ok(_): panic("assertion failed: expected Result to be Err, but got Ok")

// Asserts that [`Result`] `res` is `Err` and its formatted error contains `substring`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_err_contains_msg[T] = (res: Result[T], substring: string, msg: string):
	match(res):
		when Result.Err(err): assert_contains_msg(err.to_string(), substring, msg)
		default Result.Ok(_): panic("assertion failed: " + msg + " (expected Result to be Err, but got Ok)")

// Asserts that an optional value is present (`val? != none`).
//
// # Panics
// Panics if `val?` is `none`.
@test_only
@track_caller
bind assert_some[T] = (val?: ?T):
	if(not (val? != none)):
		panic("assertion failed: expected optional to have a value (?T), but got none")

// Asserts that an optional value is absent (`val? == none`).
//
// # Panics
// Panics if `val?` holds a value.
@test_only
@track_caller
bind assert_none[T] = (val?: ?T):
	if(val? != none): panic("assertion failed: expected optional to be none, but got a value")

// -----------------------------------------------------------------------------
// Inline Tests for std/test.ll
// -----------------------------------------------------------------------------

@test("diff formatting helpers")
bind test_diff_formatting = (): {
	let diff = _format_single_line_diff("hello world", "hello there")
	assert_true(_str_contains_sub(diff, "expected: hello there"))
	assert_true(_str_contains_sub(diff, "got: hello world"))
	assert_true(_str_contains_sub(diff, "^ diff at index 6"))

	let multi_diff = _format_multiline_diff("line1\nline2_bad\nline3", "line1\nline2_good\nline3")
	assert_true(_str_contains_sub(multi_diff, "first mismatch at line 2"))
	assert_true(_str_contains_sub(multi_diff, "expected: line2_good"))
	assert_true(_str_contains_sub(multi_diff, "got: line2_bad"))

	let utf8_diff = _format_single_line_diff("αβδ", "αβγ")
	assert_true(_str_contains_sub(utf8_diff, "\n" + _repeat_space(14) + "^ diff at index 2"))

	let accent_diff = _format_single_line_diff("café", "cafè")
	assert_true(_str_contains_sub(accent_diff, "\n" + _repeat_space(15) + "^ diff at index 3"))

	let extra_utf8 = _format_single_line_diff("αβγ", "αβ")
	assert_true(_str_contains_sub(extra_utf8, "\n" + _repeat_space(14) + "^ unexpected 1 extra char(s) in got"))

	let missing_utf8 = _format_single_line_diff("αβ", "αβγ")
	assert_true(_str_contains_sub(missing_utf8, "\n" + _repeat_space(14) + "^ missing 1 expected char(s)"))

	let multi_utf8 = _format_multiline_diff("top\nαβδ\nbot", "top\nαβγ\nbot")
	assert_true(_str_contains_sub(multi_utf8, "\n" + _repeat_space(16) + "^"))
}

@test("assert_eq and assert_ne basic assertions")
bind test_equality_assertions = (): {
	assert_eq(42, 42)
	assert_eq("hello", "hello")
	assert_eq(true, true)
	assert_eq('A', 'A')
	assert_eq_msg(100, 100, "hundred equals hundred")

	assert_ne(1, 2)
	assert_ne("foo", "bar")
	assert_ne(true, false)
	assert_ne('a', 'b')
	assert_ne_msg(10, 20, "ten not equal twenty")
}

@test("assert_true and assert_false boolean assertions")
bind test_boolean_assertions = (): {
	assert_true(1 < 2)
	assert_true_msg(10 > 5, "ten is greater than five")
	assert_false(1 > 2)
	assert_false_msg(5 > 10, "five is not greater than ten")
}

@test("assert comparisons (gt, lt, ge, le)")
bind test_comparison_assertions = (): {
	assert_gt(10, 5)
	assert_lt(5, 10)
	assert_ge(10, 10)
	assert_ge(11, 10)
	assert_le(10, 10)
	assert_le(9, 10)
}

@test("assert_is_ok and assert_is_err result assertions")
bind test_result_assertions = (): {
	let ok_res = Result[int].ok(123)
	assert_is_ok(&ok_res)
	assert_is_ok_msg(&ok_res, "result should be ok")

	let err_res = Result[int].fail_msg("failure")
	assert_is_err(&err_res)
	assert_is_err_msg(&err_res, "result should be err")
}

@test("assert_some and assert_none optional assertions")
bind test_optional_assertions = (): {
	let val?: ?int = 42
	assert_some(val?)

	let empty?: ?int = none
	assert_none(empty?)
}

@test("assert_approx_eq float assertions and edge cases")
bind test_approx_eq_assertions = (): {
	assert_approx_eq(0.1 + 0.2, 0.3, 1.0e-9)
	assert_approx_eq(-3.14159, -3.1416, 0.0001)
	assert_approx_eq(42.0, 42.0, 0.0)
	assert_approx_eq_msg(1.00001, 1.0, 0.001, "within tolerance")

	let pos_inf = 1.0 / 0.0
	let neg_inf = -1.0 / 0.0
	let nan_val = 0.0 / 0.0
	assert_approx_eq(pos_inf, pos_inf, 0.001)
	assert_approx_eq(neg_inf, neg_inf, 0.001)

	assert_some(_check_approx_eq(1.0, 1.1, 0.01))
	assert_some(_check_approx_eq(1.0, 1.0, -0.01))
	assert_some(_check_approx_eq(1.0, 1.0, nan_val))
	assert_some(_check_approx_eq(nan_val, nan_val, 1.0))
	assert_some(_check_approx_eq(nan_val, 1.0, 1.0))
	assert_some(_check_approx_eq(pos_inf, neg_inf, 1.0))
	assert_some(_check_approx_eq(pos_inf, 1.0, 1.0))
}

@test("assert_contains, assert_starts_with, and assert_ends_with assertions")
bind test_string_match_assertions = (): {
	assert_contains("hello lowlight world", "lowlight")
	assert_contains("abc", "")
	assert_contains_msg("alpha:beta:gamma", ":beta:", "middle token")

	assert_starts_with("lowlight/std/test.ll", "lowlight/")
	assert_starts_with("abc", "")
	assert_starts_with_msg("https://example.com", "https://", "https scheme")

	assert_ends_with("lowlight/std/test.ll", ".ll")
	assert_ends_with("abc", "")
	assert_ends_with_msg("archive.tar.gz", ".tar.gz", "tarball extension")
}

@test("assert_ok_* combined Result[T] assertions")
bind test_combined_result_assertions = (): {
	let r_int = Result[int].ok(42)
	assert_ok_eq(r_int, 42)
	assert_ok_eq_msg(Result[int].ok(7), 7, "inline rvalue ok_eq")
	assert_ok_ne(r_int, 0)
	assert_ok_ne_msg(r_int, 99, "not ninety-nine")
	assert_ok_gt(r_int, 41)
	assert_ok_gt_msg(r_int, 10, "greater than ten")
	assert_ok_lt(r_int, 100)
	assert_ok_lt_msg(r_int, 50, "less than fifty")
	assert_ok_ge(r_int, 42)
	assert_ok_ge_msg(r_int, 40, "at least forty")
	assert_ok_le(r_int, 42)
	assert_ok_le_msg(r_int, 45, "at most forty-five")
	assert_ok_with(r_int, (n: int) bool: n % 2 == 0)
	assert_ok_with_msg(r_int, (n: int) bool: n > 0, "positive integer")

	assert_ok_approx_eq(Result[float].ok(0.1 + 0.2), 0.3, 1.0e-9)
	assert_ok_approx_eq_msg(Result[float].ok(3.1415), 3.14, 0.01, "pi approx")

	assert_ok_true(Result[bool].ok(true))
	assert_ok_true_msg(Result[bool].ok(true), "ok true")
	assert_ok_false(Result[bool].ok(false))
	assert_ok_false_msg(Result[bool].ok(false), "ok false")

	let r_str = Result[string].ok("lowlight/std/test.ll")
	assert_ok_contains(r_str, "std/test")
	assert_ok_contains_msg(r_str, "lowlight", "contains package")
	assert_ok_starts_with(r_str, "lowlight/")
	assert_ok_starts_with_msg(r_str, "lowlight/", "starts with repo")
	assert_ok_ends_with(r_str, ".ll")
	assert_ok_ends_with_msg(r_str, "test.ll", "ends with file")

	let r_err = Result[int].fail_msg("boom on port 8080")
	assert_err_contains(r_err, "port 8080")
	assert_err_contains_msg(r_err, "boom", "error mentions boom")

	// Existing comparison _msg variants
	assert_gt_msg(10, 5, "10 > 5")
	assert_lt_msg(5, 10, "5 < 10")
	assert_ge_msg(10, 10, "10 >= 10")
	assert_le_msg(10, 10, "10 <= 10")
}
