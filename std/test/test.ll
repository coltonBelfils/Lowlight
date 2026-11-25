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
bind _str_has_newline = fn(s: string) bool: {
	for(ch in s): if(ch == '\n'): func <- true
	false
}

@test_only
bind _repeat_space = fn(n: int) string: {
	let out = ""
	for(i in 0..n): out.push(" ")
	out
}

@test_only
bind _split_lines = fn(s: string) [string]: {
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
bind _str_contains_sub = fn(haystack: string, needle: string) bool: {
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
bind format_diff = fn(actual_str: string, expected_str: string) string:
	if(_str_has_newline(actual_str) or _str_has_newline(expected_str)): _format_multiline_diff(
		actual_str,
		expected_str,
	)
	else: _format_single_line_diff(actual_str, expected_str)

@test_only
bind _find_char_diff = fn(act_chars: [char], exp_chars: [char]) int: {
	let min_len = if(act_chars.len() < exp_chars.len()): act_chars.len() else: exp_chars.len()
	for(i in 0..min_len): if(act_chars[i] != exp_chars[i]): func <- i
	-1
}

@test_only
bind _diff_pointer_prefix = fn(base_spaces: int, chars: [char], count: int) string: {
	let out = _repeat_space(base_spaces)
	let limit = if(count < chars.len()): count else: chars.len()
	for(i in 0..limit):
		if(chars[i] == '\t'): out.push("\t") else: out.push(" ")
	if(count > limit): out.push(_repeat_space(count - limit))
	out
}

@test_only
bind _format_diff_pointer = fn(base_spaces: int, act_chars: [char], exp_chars: [char]) string: {
	let act_len = act_chars.len()
	let exp_len = exp_chars.len()
	let diff_idx = _find_char_diff(act_chars, exp_chars)
	if(diff_idx >= 0): func <- _diff_pointer_prefix(
		base_spaces,
		exp_chars,
		diff_idx,
	) + "^ diff at index " + diff_idx.to_string()
	if(act_len > exp_len): {
		let extra = act_len - exp_len
		func <- _diff_pointer_prefix(
			base_spaces,
			exp_chars,
			exp_len,
		) + "^ unexpected " + extra.to_string() + " extra char(s) in got"
	}
	if(exp_len > act_len): {
		let missing = exp_len - act_len
		func <- _diff_pointer_prefix(
			base_spaces,
			act_chars,
			act_len,
		) + "^ missing " + missing.to_string() + " expected char(s)"
	}
	""
}

// Formats single-line got vs expected with aligned columns and a pointer at the first mismatch.
@test_only
@track_caller
bind _format_single_line_diff = fn(actual_str: string, expected_str: string) string: {
	let act_chars = actual_str.chars()
	let exp_chars = expected_str.chars()

	let out = "  expected: " + expected_str + "\n       got: " + actual_str
	let pointer = _format_diff_pointer(12, act_chars, exp_chars)
	if(pointer.len() > 0): out = out + "\n" + pointer
	out
}

// Formats multiline got vs expected showing the mismatched line number, aligned lines, and character pointer.
@test_only
@track_caller
bind _format_multiline_diff = fn(actual_str: string, expected_str: string) string: {
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
		let pointer = _format_diff_pointer(14, act_chars, exp_chars)
		if(pointer.len() > 0): out = out + "\n" + pointer
	} else if(diff_line < exp_lines.len()): {
		out = out + "    expected: " + exp_lines[diff_line] + "\n"
		out = out + "         got: <end of input>"
	} else if(diff_line < act_lines.len()): {
		out = out + "    expected: <end of input>\n"
		out = out + "         got: " + act_lines[diff_line]
	}

	if(act_lines.len() != exp_lines.len()): out = out + "\n  (total line count: expected " + exp_lines
		.len()
		.to_string() + ", got " + act_lines
		.len()
		.to_string() + ")"
	out
}

// -----------------------------------------------------------------------------
// Assertions
// -----------------------------------------------------------------------------

@test_only
bind _check_approx_eq = fn(actual: float, expected: float, eps: float) ?string: {
	if(not (eps == eps) or eps < 0.0): func <- "tolerance must be non-negative and not NaN (got " + eps.to_string() + ")"
	if(not (actual == actual) or not (expected == expected)): func <- "expected approximately " + expected.to_string() + " (+/- " + eps.to_string() + "), got " + actual.to_string() + " (NaN is never approximately equal)"
	if(actual == expected): func <- none
	let diff = if(actual > expected): actual - expected else: expected - actual
	if(not (diff <= eps)): func <- "expected approximately " + expected.to_string() + " (+/- " + eps.to_string() + "), got " + actual.to_string() + " (diff: " + diff.to_string() + ")"
	none
}

@test_only
bind _format_labeled_value = fn(label: string, val: string) string: {
	if(not _str_has_newline(val)): func <- label + ": " + val
	let lines = _split_lines(val)
	let cont_indent = _repeat_space(label.len() + 2)
	let out = ""
	for(i, line in lines):
		if(i == 0): {
			out.push(label)
			out.push(": ")
			out.push(line)
		} else: {
			out.push("\n")
			if(line.len() > 0): {
				out.push(cont_indent)
				out.push(line)
			}
		}
	out
}

@test_only
bind _format_error_block = fn(err_str: string) string: {
	let lines = _split_lines(err_str)
	let out = ""
	let in_trace = false
	let pending_blank = false
	for(i, line in lines):
		if(i == 0): {
			out.push("  error: ")
			out.push(line)
		} else if(line == "trace:"): {
			in_trace = true
			pending_blank = false
			out.push("\n  trace:")
		} else if(line.len() == 0): pending_blank = true
		else: {
			if(pending_blank): {
				out.push("\n")
				pending_blank = false
			}
			if(in_trace): {
				out.push("\n  ")
				out.push(line)
			} else: {
				out.push("\n         ")
				out.push(line)
			}
		}
	out
}

@test_only
bind _format_str_match_err = fn(
	expectation: string,
	actual: string,
	target_label: string,
	target: string,
) string: "expected string to " + expectation + "\n" + _format_labeled_value(
	"     actual",
	actual,
) + "\n" + _format_labeled_value(target_label, target)

@test_only
bind _indent_block = fn(s: string, indent: string) string: {
	let lines = _split_lines(s)
	let out = ""
	for(i, line in lines): {
		if(i > 0): out.push("\n")
		if(line.len() > 0): {
			out.push(indent)
			out.push(line)
		}
	}
	out
}

@test_only
bind _append_batch_failure = fn(failures: &[string], idx: int, label: string, detail: string): {
	let header = if(label.len() > 0): "✗ case [" + idx.to_string() + "] (" + label + "):"
	else: "✗ case [" + idx.to_string() + "]:"
	failures.push(header + "\n" + _indent_block(detail, "  "))
}

@test_only
@track_caller
bind _panic_batch_if_failed = fn(total: int, failures: [string], msg: string):
	if(failures.len() > 0): {
		let failed = failures.len()
		let passed = total - failed
		let counts = failed.to_string() + " of " + total.to_string() + " case(s) failed (" + passed.to_string() + " passed)"
		let header = if(msg.len() > 0): "assertion failed: " + msg + ": " + counts
		else: "assertion failed: " + counts
		panic(header + "\n\n" + failures.join("\n\n"))
	}

@test_only
bind _check_eq[T: Equatable + Printable] = fn(actual: T, expected: T) ?string:
	if(actual != expected): "values are not equal\n" + format_diff(
		actual.to_string(),
		expected.to_string(),
	)
	else: none

@test_only
bind _check_ne[T: Equatable + Printable] = fn(actual: T, unexpected: T) ?string:
	if(actual == unexpected): {
		let s = unexpected.to_string()
		"values must not be equal\n" + _format_labeled_value(
			"  unexpected",
			s,
		) + "\n" + _format_labeled_value("         got", s)
	} else: none

@test_only
bind _format_cmp_detail[T: Printable] = fn(actual: T, expected: T, op: string) string: "expected actual " + op + " threshold\n" + _format_labeled_value(
	"     actual",
	actual.to_string(),
) + "\n" + _format_labeled_value("  threshold", expected.to_string())

@test_only
bind _check_gt[T: Comparable + Printable] = fn(actual: T, expected: T) ?string:
	if(actual <= expected): _format_cmp_detail(actual, expected, ">") else: none

@test_only
bind _check_lt[T: Comparable + Printable] = fn(actual: T, expected: T) ?string:
	if(actual >= expected): _format_cmp_detail(actual, expected, "<") else: none

@test_only
bind _check_ge[T: Comparable + Printable] = fn(actual: T, expected: T) ?string:
	if(actual < expected): _format_cmp_detail(actual, expected, ">=") else: none

@test_only
bind _check_le[T: Comparable + Printable] = fn(actual: T, expected: T) ?string:
	if(actual > expected): _format_cmp_detail(actual, expected, "<=") else: none

@test_only
bind _check_contains = fn(actual: string, substring: string) ?string:
	if(not actual.contains(substring)): _format_str_match_err(
		"contain substring",
		actual,
		"  substring",
		substring,
	)
	else: none

@test_only
bind _check_starts_with = fn(actual: string, prefix: string) ?string:
	if(not actual.starts_with(prefix)): _format_str_match_err(
		"start with prefix",
		actual,
		"     prefix",
		prefix,
	)
	else: none

@test_only
bind _check_ends_with = fn(actual: string, suffix: string) ?string:
	if(not actual.ends_with(suffix)): _format_str_match_err(
		"end with suffix",
		actual,
		"     suffix",
		suffix,
	)
	else: none

@test_only
bind _format_result_not_ok_detail = fn(err: Error) string: "expected a value, but the Result holds a failure\n" + _format_error_block(
	err.to_string()
)

// Asserts that `actual` is within `eps` of `expected` (`|actual - expected| <= eps`).
//
// # Panics
// Panics if `eps` is negative or `NaN`, either value is `NaN`, or `|actual - expected| > eps`.
@test_only
@track_caller
bind assert_approx_eq = fn(actual: float, expected: float, eps: float): if(_check_approx_eq(
	actual,
	expected,
	eps,
) as detail): panic("assertion failed: " + detail)

// Asserts that `actual` is within `eps` of `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `eps` is negative or `NaN`, either value is `NaN`, or `|actual - expected| > eps`.
@test_only
@track_caller
bind assert_approx_eq_msg = fn(actual: float, expected: float, eps: float, msg: string): if(_check_approx_eq(
	actual,
	expected,
	eps,
) as detail): panic("assertion failed: " + msg + " (" + detail + ")")

// Asserts that `actual` equals `expected` (`actual == expected`).
//
// # Panics
// Panics with an aligned visual diff produced by [`format_diff`] when the
// two values differ.
@test_only
@track_caller
bind assert_eq[T: Equatable + Printable] = fn(actual: T, expected: T): if(_check_eq(actual, expected) as detail): panic(
	"assertion failed: " + detail
)

// Asserts that `actual` equals `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` and a [`format_diff`] output if `actual != expected`.
@test_only
@track_caller
bind assert_eq_msg[T: Equatable + Printable] = fn(actual: T, expected: T, msg: string): if(_check_eq(
	actual,
	expected,
) as detail): panic(
	"assertion failed: " + msg + "\n" + format_diff(
		actual.to_string(),
		expected.to_string(),
	)
)

// Asserts that `actual` does not equal `unexpected` (`actual != unexpected`).
//
// # Panics
// Panics if `actual == unexpected`.
@test_only
@track_caller
bind assert_ne[T: Equatable + Printable] = fn(actual: T, unexpected: T): if(_check_ne(
	actual,
	unexpected,
) as detail): panic("assertion failed: " + detail)

// Asserts that `actual` does not equal `unexpected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual == unexpected`.
@test_only
@track_caller
bind assert_ne_msg[T: Equatable + Printable] = fn(actual: T, unexpected: T, msg: string):
	if(actual == unexpected): {
		let s = unexpected.to_string()
		panic(
			"assertion failed: " + msg + " (values must not be equal)\n" + _format_labeled_value(
				"  unexpected",
				s,
			) + "\n" + _format_labeled_value("         got", s)
		)
	}

// Asserts that `cond` evaluates to `true`.
//
// # Panics
// Panics if `cond` is `false`.
@test_only
@track_caller
bind assert_true = fn(cond: bool): if(not cond): panic(
	"assertion failed: expected true, but condition evaluated to false"
)

// Asserts that `cond` is `true`, failing with custom context `msg`.
//
// # Panics
// Panics with `msg` if `cond` is `false`.
@test_only
@track_caller
bind assert_true_msg = fn(cond: bool, msg: string): if(not cond): panic(
	"assertion failed: " + msg + " (expected true, but got false)"
)

// Asserts that `cond` evaluates to `false`.
//
// # Panics
// Panics if `cond` is `true`.
@test_only
@track_caller
bind assert_false = fn(cond: bool): if(cond): panic(
	"assertion failed: expected false, but condition evaluated to true"
)

// Asserts that `cond` is `false`, failing with custom context `msg`.
//
// # Panics
// Panics with `msg` if `cond` is `true`.
@test_only
@track_caller
bind assert_false_msg = fn(cond: bool, msg: string): if(cond): panic(
	"assertion failed: " + msg + " (expected false, but got true)"
)

@test_only
@track_caller
bind _panic_cmp[T: Printable] = fn(actual: T, expected: T, op: string, msg: string):
	if(msg.len() > 0): panic(
		"assertion failed: " + msg + " (expected actual " + op + " threshold)\n" + _format_labeled_value(
			"     actual",
			actual.to_string(),
		) + "\n" + _format_labeled_value("  threshold", expected.to_string())
	)
	else: panic("assertion failed: " + _format_cmp_detail(actual, expected, op))

// Asserts that `actual` is strictly greater than `expected` (`actual > expected`).
//
// # Panics
// Panics if `actual <= expected`.
@test_only
@track_caller
bind assert_gt[T: Comparable + Printable] = fn(actual: T, expected: T): if(actual <= expected): _panic_cmp(
	actual,
	expected,
	">",
	"",
)

// Asserts that `actual` is strictly greater than `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual <= expected`.
@test_only
@track_caller
bind assert_gt_msg[T: Comparable + Printable] = fn(actual: T, expected: T, msg: string): if(actual <= expected): _panic_cmp(
	actual,
	expected,
	">",
	msg,
)

// Asserts that `actual` is strictly less than `expected` (`actual < expected`).
//
// # Panics
// Panics if `actual >= expected`.
@test_only
@track_caller
bind assert_lt[T: Comparable + Printable] = fn(actual: T, expected: T): if(actual >= expected): _panic_cmp(
	actual,
	expected,
	"<",
	"",
)

// Asserts that `actual` is strictly less than `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual >= expected`.
@test_only
@track_caller
bind assert_lt_msg[T: Comparable + Printable] = fn(actual: T, expected: T, msg: string): if(actual >= expected): _panic_cmp(
	actual,
	expected,
	"<",
	msg,
)

// Asserts that `actual` is greater than or equal to `expected` (`actual >= expected`).
//
// # Panics
// Panics if `actual < expected`.
@test_only
@track_caller
bind assert_ge[T: Comparable + Printable] = fn(actual: T, expected: T): if(actual < expected): _panic_cmp(
	actual,
	expected,
	">=",
	"",
)

// Asserts that `actual` is greater than or equal to `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual < expected`.
@test_only
@track_caller
bind assert_ge_msg[T: Comparable + Printable] = fn(actual: T, expected: T, msg: string): if(actual < expected): _panic_cmp(
	actual,
	expected,
	">=",
	msg,
)

// Asserts that `actual` is less than or equal to `expected` (`actual <= expected`).
//
// # Panics
// Panics if `actual > expected`.
@test_only
@track_caller
bind assert_le[T: Comparable + Printable] = fn(actual: T, expected: T): if(actual > expected): _panic_cmp(
	actual,
	expected,
	"<=",
	"",
)

// Asserts that `actual` is less than or equal to `expected`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual > expected`.
@test_only
@track_caller
bind assert_le_msg[T: Comparable + Printable] = fn(actual: T, expected: T, msg: string): if(actual > expected): _panic_cmp(
	actual,
	expected,
	"<=",
	msg,
)

// Asserts that `actual` contains `substring`.
//
// # Panics
// Panics if `actual` does not contain `substring`.
@test_only
@track_caller
bind assert_contains = fn(actual: string, substring: string): if(not actual.contains(substring)): panic(
	"assertion failed: " + _format_str_match_err(
		"contain substring",
		actual,
		"  substring",
		substring,
	)
)

// Asserts that `actual` contains `substring`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual` does not contain `substring`.
@test_only
@track_caller
bind assert_contains_msg = fn(actual: string, substring: string, msg: string): if(not actual.contains(
	substring
)): panic(
	"assertion failed: " + msg + "\n" + _format_str_match_err(
		"contain substring",
		actual,
		"  substring",
		substring,
	)
)

// Asserts that `actual` starts with `prefix`.
//
// # Panics
// Panics if `actual` does not start with `prefix`.
@test_only
@track_caller
bind assert_starts_with = fn(actual: string, prefix: string): if(not actual.starts_with(prefix)): panic(
	"assertion failed: " + _format_str_match_err(
		"start with prefix",
		actual,
		"     prefix",
		prefix,
	)
)

// Asserts that `actual` starts with `prefix`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual` does not start with `prefix`.
@test_only
@track_caller
bind assert_starts_with_msg = fn(actual: string, prefix: string, msg: string): if(not actual.starts_with(
	prefix
)): panic(
	"assertion failed: " + msg + "\n" + _format_str_match_err(
		"start with prefix",
		actual,
		"     prefix",
		prefix,
	)
)

// Asserts that `actual` ends with `suffix`.
//
// # Panics
// Panics if `actual` does not end with `suffix`.
@test_only
@track_caller
bind assert_ends_with = fn(actual: string, suffix: string): if(not actual.ends_with(suffix)): panic(
	"assertion failed: " + _format_str_match_err(
		"end with suffix",
		actual,
		"     suffix",
		suffix,
	)
)

// Asserts that `actual` ends with `suffix`, prefixing failures with `msg`.
//
// # Panics
// Panics with `msg` if `actual` does not end with `suffix`.
@test_only
@track_caller
bind assert_ends_with_msg = fn(actual: string, suffix: string, msg: string): if(not actual.ends_with(
	suffix
)): panic(
	"assertion failed: " + msg + "\n" + _format_str_match_err(
		"end with suffix",
		actual,
		"     suffix",
		suffix,
	)
)

// Asserts that [`Result`] `res` holds a value.
//
// # Panics
// Panics with the failure's error and trace if `res` holds a failure.
@test_only
@track_caller
bind assert_is_ok[T] = fn(res!: &Result[T]):
	res!:
		default err(e): panic(
			"assertion failed: expected a value, but the Result holds a failure\n" + _format_error_block(
				e.to_string()
			)
		)

// Asserts that [`Result`] `res` holds a value, failing with context `msg`.
//
// # Panics
// Panics with `msg` and the failure's error if `res` holds a failure.
@test_only
@track_caller
bind assert_is_ok_msg[T] = fn(res!: &Result[T], msg: string):
	res!:
		default err(e): panic(
			"assertion failed: " + msg + " (expected a value, but the Result holds a failure)\n" + _format_error_block(
				e.to_string()
			)
		)

// Asserts that [`Result`] `res` holds a failure.
//
// # Panics
// Panics if `res` holds a value.
@test_only
@track_caller
bind assert_is_err[T] = fn(res!: &Result[T]): if(res!.is_ok()): panic(
	"assertion failed: expected a failure, but the Result holds a value"
)

// Asserts that [`Result`] `res` holds a failure, failing with context `msg`.
//
// # Panics
// Panics with `msg` if `res` holds a value.
@test_only
@track_caller
bind assert_is_err_msg[T] = fn(res!: &Result[T], msg: string): if(res!.is_ok()): panic(
	"assertion failed: " + msg + " (expected a failure, but the Result holds a value)"
)

@test_only
@track_caller
bind _panic_result_not_ok = fn(err: Error, msg: string):
	if(msg.len() > 0): panic(
		"assertion failed: " + msg + " (expected a value, but the Result holds a failure)\n" + _format_error_block(
			err.to_string()
		)
	)
	else: panic(
		"assertion failed: expected a value, but the Result holds a failure\n" + _format_error_block(
			err.to_string()
		)
	)

// Asserts that [`Result`] `res` holds a value and its value equals `expected` (`val == expected`).
//
// # Panics
// Panics with the underlying error if `res` holds a failure, or a formatted diff if `val != expected`.
@test_only
@track_caller
bind assert_ok_eq[T: Equatable + Printable] = fn(res!: Result[T], expected: T):
	match(res!):
		when value(val): assert_eq(val, expected)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its value equals `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_eq_msg[T: Equatable + Printable] = fn(res!: Result[T], expected: T, msg: string):
	match(res!):
		when value(val): assert_eq_msg(val, expected, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its value does not equal `unexpected` (`val != unexpected`).
@test_only
@track_caller
bind assert_ok_ne[T: Equatable + Printable] = fn(res!: Result[T], unexpected: T):
	match(res!):
		when value(val): assert_ne(val, unexpected)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its value does not equal `unexpected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_ne_msg[T: Equatable + Printable] = fn(res!: Result[T], unexpected: T, msg: string):
	match(res!):
		when value(val): assert_ne_msg(val, unexpected, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its float value is within `eps` of `expected`.
@test_only
@track_caller
bind assert_ok_approx_eq = fn(res!: Result[float], expected: float, eps: float):
	match(res!):
		when value(val): assert_approx_eq(val, expected, eps)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its float value is within `eps` of `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_approx_eq_msg = fn(res!: Result[float], expected: float, eps: float, msg: string):
	match(res!):
		when value(val): assert_approx_eq_msg(val, expected, eps, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` is `Ok(true)`.
@test_only
@track_caller
bind assert_ok_true = fn(res!: Result[bool]):
	match(res!):
		when value(val): assert_true(val)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` is `Ok(true)`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_true_msg = fn(res!: Result[bool], msg: string):
	match(res!):
		when value(val): assert_true_msg(val, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` is `Ok(false)`.
@test_only
@track_caller
bind assert_ok_false = fn(res!: Result[bool]):
	match(res!):
		when value(val): assert_false(val)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` is `Ok(false)`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_false_msg = fn(res!: Result[bool], msg: string):
	match(res!):
		when value(val): assert_false_msg(val, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its value is strictly greater than `expected` (`val > expected`).
@test_only
@track_caller
bind assert_ok_gt[T: Comparable + Printable] = fn(res!: Result[T], expected: T):
	match(res!):
		when value(val): assert_gt(val, expected)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its value is strictly greater than `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_gt_msg[T: Comparable + Printable] = fn(res!: Result[T], expected: T, msg: string):
	match(res!):
		when value(val): assert_gt_msg(val, expected, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its value is strictly less than `expected` (`val < expected`).
@test_only
@track_caller
bind assert_ok_lt[T: Comparable + Printable] = fn(res!: Result[T], expected: T):
	match(res!):
		when value(val): assert_lt(val, expected)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its value is strictly less than `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_lt_msg[T: Comparable + Printable] = fn(res!: Result[T], expected: T, msg: string):
	match(res!):
		when value(val): assert_lt_msg(val, expected, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its value is greater than or equal to `expected` (`val >= expected`).
@test_only
@track_caller
bind assert_ok_ge[T: Comparable + Printable] = fn(res!: Result[T], expected: T):
	match(res!):
		when value(val): assert_ge(val, expected)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its value is greater than or equal to `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_ge_msg[T: Comparable + Printable] = fn(res!: Result[T], expected: T, msg: string):
	match(res!):
		when value(val): assert_ge_msg(val, expected, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its value is less than or equal to `expected` (`val <= expected`).
@test_only
@track_caller
bind assert_ok_le[T: Comparable + Printable] = fn(res!: Result[T], expected: T):
	match(res!):
		when value(val): assert_le(val, expected)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its value is less than or equal to `expected`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_le_msg[T: Comparable + Printable] = fn(res!: Result[T], expected: T, msg: string):
	match(res!):
		when value(val): assert_le_msg(val, expected, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its string value contains `substring`.
@test_only
@track_caller
bind assert_ok_contains = fn(res!: Result[string], substring: string):
	match(res!):
		when value(val): assert_contains(val, substring)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its string value contains `substring`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_contains_msg = fn(res!: Result[string], substring: string, msg: string):
	match(res!):
		when value(val): assert_contains_msg(val, substring, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its string value starts with `prefix`.
@test_only
@track_caller
bind assert_ok_starts_with = fn(res!: Result[string], prefix: string):
	match(res!):
		when value(val): assert_starts_with(val, prefix)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its string value starts with `prefix`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_starts_with_msg = fn(res!: Result[string], prefix: string, msg: string):
	match(res!):
		when value(val): assert_starts_with_msg(val, prefix, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and its string value ends with `suffix`.
@test_only
@track_caller
bind assert_ok_ends_with = fn(res!: Result[string], suffix: string):
	match(res!):
		when value(val): assert_ends_with(val, suffix)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and its string value ends with `suffix`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_ends_with_msg = fn(res!: Result[string], suffix: string, msg: string):
	match(res!):
		when value(val): assert_ends_with_msg(val, suffix, msg)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a value and satisfies predicate `check(val)`.
@test_only
@track_caller
bind assert_ok_with[T: Printable] = fn(res!: Result[T], check: cl(T) bool):
	match(res!):
		when value(val): if(not check(val)): panic(
			"assertion failed: Ok value did not satisfy predicate\n" + _format_labeled_value(
				"  actual",
				val.to_string(),
			)
		)
		default err(e): _panic_result_not_ok(e, "")

// Asserts that [`Result`] `res` holds a value and satisfies predicate `check(val)`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_ok_with_msg[T: Printable] = fn(res!: Result[T], check: cl(T) bool, msg: string):
	match(res!):
		when value(val): if(not check(val)): panic(
			"assertion failed: " + msg + " (Ok value did not satisfy predicate)\n" + _format_labeled_value(
				"  actual",
				val.to_string(),
			)
		)
		default err(e): _panic_result_not_ok(e, msg)

// Asserts that [`Result`] `res` holds a failure and its formatted error contains `substring`.
@test_only
@track_caller
bind assert_err_contains[T] = fn(res!: Result[T], substring: string):
	match(res!):
		when err(e): assert_contains(e.to_string(), substring)
		default value(_): panic("assertion failed: expected a failure, but the Result holds a value")

// Asserts that [`Result`] `res` holds a failure and its formatted error contains `substring`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_err_contains_msg[T] = fn(res!: Result[T], substring: string, msg: string):
	match(res!):
		when err(e): assert_contains_msg(e.to_string(), substring, msg)
		default value(_): panic(
			"assertion failed: " + msg + " (expected a failure, but the Result holds a value)"
		)

// Asserts that an optional value is present (`val? != none`).
//
// # Panics
// Panics if `val?` is `none`.
@test_only
@track_caller
bind assert_some[T] = fn(val?: ?T): if(not (val? != none)): panic(
	"assertion failed: expected optional to have a value (?T), but got none"
)

// Asserts that an optional value is absent (`val? == none`).
//
// # Panics
// Panics if `val?` holds a value.
@test_only
@track_caller
bind assert_none[T] = fn(val?: ?T): if(val? != none): panic(
	"assertion failed: expected optional to be none, but got a value"
)

// -----------------------------------------------------------------------------
// Batch Assertions (`[+[expected, actual]]` and `[T]`)
// -----------------------------------------------------------------------------
//
// Every `assert_all_*` function evaluates all cases without stopping at the
// first failure, collecting every failing index and formatted diff/error into a
// single summary diagnostic. Pair inputs use `.0` for `expected` (or threshold /
// substring / prefix / suffix) and `.1` for `actual` (or `Result`).

@test_only
@track_caller
bind _assert_all_eq_impl[T: Equatable + Printable] = fn(cases: [+[T, T]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_eq(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected, actual)` pairs in `cases`, failing with a summary of every mismatch.
@test_only
@track_caller
bind assert_all_eq[T: Equatable + Printable] = fn(cases: [+[T, T]]): _assert_all_eq_impl(cases, "")

// Evaluates all `(expected, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_eq_msg[T: Equatable + Printable] = fn(cases: [+[T, T]], msg: string): _assert_all_eq_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_eq_named_impl[T: Equatable + Printable] = fn(cases: [+[string, T, T]], msg: string): {
	let failures: [string] = []
	for(i, item in cases): if(_check_eq(item.2, item.1) as detail): _append_batch_failure(
		&failures,
		i,
		item.0,
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(label, expected, actual)` cases, reporting case labels on every mismatch.
@test_only
@track_caller
bind assert_all_eq_named[T: Equatable + Printable] = fn(cases: [+[string, T, T]]): _assert_all_eq_named_impl(
	cases,
	"",
)

// Evaluates all `(label, expected, actual)` cases, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_eq_named_msg[T: Equatable + Printable] = fn(cases: [+[string, T, T]], msg: string): _assert_all_eq_named_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_eq_by_impl[T: Printable] = fn(cases: [+[T, T]], eq_fn: cl(T, T) bool, msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		if(not eq_fn(pair.1, pair.0)): {
			let detail = "values failed custom equality check\n" + format_diff(
				pair.1.to_string(),
				pair.0.to_string(),
			)
			_append_batch_failure(&failures, i, "", detail)
		}
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected, actual)` pairs using custom comparator `eq_fn(actual, expected)`.
@test_only
@track_caller
bind assert_all_eq_by[T: Printable] = fn(cases: [+[T, T]], eq_fn: cl(T, T) bool): _assert_all_eq_by_impl(
	cases,
	eq_fn,
	"",
)

// Evaluates all `(expected, actual)` pairs using `eq_fn(actual, expected)`, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_all_eq_by_msg[T: Printable] = fn(cases: [+[T, T]], eq_fn: cl(T, T) bool, msg: string): _assert_all_eq_by_impl(
	cases,
	eq_fn,
	msg,
)

@test_only
@track_caller
bind _assert_any_eq_impl[T: Equatable + Printable] = fn(cases: [+[T, T]], msg: string): {
	for(pair in cases): if(pair.1 == pair.0): func <-
	let lines: [string] = []
	for(i, pair in cases): lines.push(
		"  [" + i.to_string() + "] expected: " + pair.0.to_string() + ", got: " + pair
			.1
			.to_string()
	)
	let prefix = if(msg.len() > 0): "assertion failed: " + msg + " (no (expected, actual) pair matched)"
	else: "assertion failed: no (expected, actual) pair matched"
	if(lines.len() == 0): panic(prefix + " (empty case list)")
	panic(prefix + "\n\n" + lines.join("\n"))
}

// Asserts that at least one `(expected, actual)` pair in `cases` is equal.
@test_only
@track_caller
bind assert_any_eq[T: Equatable + Printable] = fn(cases: [+[T, T]]): _assert_any_eq_impl(cases, "")

// Asserts that at least one `(expected, actual)` pair in `cases` is equal, prefixing failures with `msg`.
@test_only
@track_caller
bind assert_any_eq_msg[T: Equatable + Printable] = fn(cases: [+[T, T]], msg: string): _assert_any_eq_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ne_impl[T: Equatable + Printable] = fn(cases: [+[T, T]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_ne(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(unexpected, actual)` pairs in `cases`, failing if any pair is equal.
@test_only
@track_caller
bind assert_all_ne[T: Equatable + Printable] = fn(cases: [+[T, T]]): _assert_all_ne_impl(cases, "")

// Evaluates all `(unexpected, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ne_msg[T: Equatable + Printable] = fn(cases: [+[T, T]], msg: string): _assert_all_ne_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_approx_eq_impl = fn(cases: [+[float, float]], eps: float, msg: string): {
	if(not (eps == eps) or eps < 0.0): {
		let detail = "tolerance must be non-negative and not NaN (got " + eps.to_string() + ")"
		if(msg.len() > 0): panic("assertion failed: " + msg + " (" + detail + ")")
		else: panic("assertion failed: " + detail)
	}
	let failures: [string] = []
	for(i, pair in cases): if(_check_approx_eq(pair.1, pair.0, eps) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected, actual)` float pairs in `cases` within tolerance `eps`.
@test_only
@track_caller
bind assert_all_approx_eq = fn(cases: [+[float, float]], eps: float): _assert_all_approx_eq_impl(
	cases,
	eps,
	"",
)

// Evaluates all `(expected, actual)` float pairs within `eps`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_approx_eq_msg = fn(cases: [+[float, float]], eps: float, msg: string): _assert_all_approx_eq_impl(
	cases,
	eps,
	msg,
)

@test_only
@track_caller
bind _assert_all_true_impl = fn(cases: [bool], msg: string): {
	let failures: [string] = []
	for(i, cond in cases): if(not cond): _append_batch_failure(
		&failures,
		i,
		"",
		"expected true, but condition evaluated to false",
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all boolean conditions in `cases`, reporting every index that evaluated to `false`.
@test_only
@track_caller
bind assert_all_true = fn(cases: [bool]): _assert_all_true_impl(cases, "")

// Evaluates all boolean conditions in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_true_msg = fn(cases: [bool], msg: string): _assert_all_true_impl(cases, msg)

@test_only
@track_caller
bind _assert_all_false_impl = fn(cases: [bool], msg: string): {
	let failures: [string] = []
	for(i, cond in cases): if(cond): _append_batch_failure(
		&failures,
		i,
		"",
		"expected false, but condition evaluated to true",
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all boolean conditions in `cases`, reporting every index that evaluated to `true`.
@test_only
@track_caller
bind assert_all_false = fn(cases: [bool]): _assert_all_false_impl(cases, "")

// Evaluates all boolean conditions in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_false_msg = fn(cases: [bool], msg: string): _assert_all_false_impl(cases, msg)

@test_only
@track_caller
bind _assert_all_with_impl[T: Printable] = fn(cases: [T], check: cl(T) bool, msg: string): {
	let failures: [string] = []
	for(i, val in cases): if(not check(val)): _append_batch_failure(
		&failures,
		i,
		"",
		"value did not satisfy predicate\n" + _format_labeled_value("  actual", val.to_string()),
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates `check(val)` for every item in `cases`, reporting every failing index and value.
@test_only
@track_caller
bind assert_all_with[T: Printable] = fn(cases: [T], check: cl(T) bool): _assert_all_with_impl(
	cases,
	check,
	"",
)

// Evaluates `check(val)` for every item in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_with_msg[T: Printable] = fn(cases: [T], check: cl(T) bool, msg: string): _assert_all_with_impl(
	cases,
	check,
	msg,
)

@test_only
@track_caller
bind _assert_all_gt_impl[T: Comparable + Printable] = fn(cases: [+[T, T]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_gt(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(threshold, actual)` pairs in `cases`, asserting `actual > threshold` for every item.
@test_only
@track_caller
bind assert_all_gt[T: Comparable + Printable] = fn(cases: [+[T, T]]): _assert_all_gt_impl(cases, "")

// Evaluates all `(threshold, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_gt_msg[T: Comparable + Printable] = fn(cases: [+[T, T]], msg: string): _assert_all_gt_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_lt_impl[T: Comparable + Printable] = fn(cases: [+[T, T]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_lt(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(threshold, actual)` pairs in `cases`, asserting `actual < threshold` for every item.
@test_only
@track_caller
bind assert_all_lt[T: Comparable + Printable] = fn(cases: [+[T, T]]): _assert_all_lt_impl(cases, "")

// Evaluates all `(threshold, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_lt_msg[T: Comparable + Printable] = fn(cases: [+[T, T]], msg: string): _assert_all_lt_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ge_impl[T: Comparable + Printable] = fn(cases: [+[T, T]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_ge(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(threshold, actual)` pairs in `cases`, asserting `actual >= threshold` for every item.
@test_only
@track_caller
bind assert_all_ge[T: Comparable + Printable] = fn(cases: [+[T, T]]): _assert_all_ge_impl(cases, "")

// Evaluates all `(threshold, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ge_msg[T: Comparable + Printable] = fn(cases: [+[T, T]], msg: string): _assert_all_ge_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_le_impl[T: Comparable + Printable] = fn(cases: [+[T, T]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_le(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(threshold, actual)` pairs in `cases`, asserting `actual <= threshold` for every item.
@test_only
@track_caller
bind assert_all_le[T: Comparable + Printable] = fn(cases: [+[T, T]]): _assert_all_le_impl(cases, "")

// Evaluates all `(threshold, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_le_msg[T: Comparable + Printable] = fn(cases: [+[T, T]], msg: string): _assert_all_le_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_contains_impl = fn(cases: [+[string, string]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_contains(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected_substring, actual)` pairs in `cases`, asserting `actual.contains(expected_substring)`.
@test_only
@track_caller
bind assert_all_contains = fn(cases: [+[string, string]]): _assert_all_contains_impl(cases, "")

// Evaluates all `(expected_substring, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_contains_msg = fn(cases: [+[string, string]], msg: string): _assert_all_contains_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_starts_with_impl = fn(cases: [+[string, string]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_starts_with(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected_prefix, actual)` pairs in `cases`, asserting `actual.starts_with(expected_prefix)`.
@test_only
@track_caller
bind assert_all_starts_with = fn(cases: [+[string, string]]): _assert_all_starts_with_impl(cases, "")

// Evaluates all `(expected_prefix, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_starts_with_msg = fn(cases: [+[string, string]], msg: string): _assert_all_starts_with_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ends_with_impl = fn(cases: [+[string, string]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases): if(_check_ends_with(pair.1, pair.0) as detail): _append_batch_failure(
		&failures,
		i,
		"",
		detail,
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected_suffix, actual)` pairs in `cases`, asserting `actual.ends_with(expected_suffix)`.
@test_only
@track_caller
bind assert_all_ends_with = fn(cases: [+[string, string]]): _assert_all_ends_with_impl(cases, "")

// Evaluates all `(expected_suffix, actual)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ends_with_msg = fn(cases: [+[string, string]], msg: string): _assert_all_ends_with_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_is_ok_impl[T] = fn(cases: [Result[T]], msg: string): {
	let failures: [string] = []
	for(i, res! in cases):
		match(res!):
			when value(_): {}
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all [`Result`] values in `cases`, reporting every index that is `Err`.
@test_only
@track_caller
bind assert_all_is_ok[T] = fn(cases: [Result[T]]): _assert_all_is_ok_impl(cases, "")

// Evaluates all [`Result`] values in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_is_ok_msg[T] = fn(cases: [Result[T]], msg: string): _assert_all_is_ok_impl(cases, msg)

@test_only
@track_caller
bind _assert_all_is_err_impl[T] = fn(cases: [Result[T]], msg: string): {
	let failures: [string] = []
	for(i, res! in cases): if(res!.is_ok()): _append_batch_failure(
		&failures,
		i,
		"",
		"expected a failure, but the Result holds a value",
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all [`Result`] values in `cases`, reporting every index that is `Ok`.
@test_only
@track_caller
bind assert_all_is_err[T] = fn(cases: [Result[T]]): _assert_all_is_err_impl(cases, "")

// Evaluates all [`Result`] values in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_is_err_msg[T] = fn(cases: [Result[T]], msg: string): _assert_all_is_err_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_eq_impl[T: Equatable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_eq(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected, actual_result)` pairs in `cases`, asserting `Ok(val)` equals `expected`.
@test_only
@track_caller
bind assert_all_ok_eq[T: Equatable + Printable] = fn(cases: [+[T, Result[T]]]): _assert_all_ok_eq_impl(
	cases,
	"",
)

// Evaluates all `(expected, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_eq_msg[T: Equatable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): _assert_all_ok_eq_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_eq_named_impl[T: Equatable + Printable] = fn(
	cases: [+[string, T, Result[T]]],
	msg: string,
): {
	let failures: [string] = []
	for(i, item in cases):
		match(item.2):
			when value(val): if(_check_eq(val, item.1) as detail): _append_batch_failure(
				&failures,
				i,
				item.0,
				detail,
			)
			default err(e): _append_batch_failure(
				&failures,
				i,
				item.0,
				_format_result_not_ok_detail(e),
			)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(label, expected, actual_result)` cases, reporting case labels on every failure.
@test_only
@track_caller
bind assert_all_ok_eq_named[T: Equatable + Printable] = fn(cases: [+[string, T, Result[T]]]): _assert_all_ok_eq_named_impl(
	cases,
	"",
)

// Evaluates all `(label, expected, actual_result)` cases, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_eq_named_msg[T: Equatable + Printable] = fn(
	cases: [+[string, T, Result[T]]],
	msg: string,
): _assert_all_ok_eq_named_impl(cases, msg)

@test_only
@track_caller
bind _assert_all_ok_ne_impl[T: Equatable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_ne(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(unexpected, actual_result)` pairs in `cases`, asserting `Ok(val)` does not equal `unexpected`.
@test_only
@track_caller
bind assert_all_ok_ne[T: Equatable + Printable] = fn(cases: [+[T, Result[T]]]): _assert_all_ok_ne_impl(
	cases,
	"",
)

// Evaluates all `(unexpected, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_ne_msg[T: Equatable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): _assert_all_ok_ne_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_approx_eq_impl = fn(cases: [+[float, Result[float]]], eps: float, msg: string): {
	if(not (eps == eps) or eps < 0.0): {
		let detail = "tolerance must be non-negative and not NaN (got " + eps.to_string() + ")"
		if(msg.len() > 0): panic("assertion failed: " + msg + " (" + detail + ")")
		else: panic("assertion failed: " + detail)
	}
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_approx_eq(val, pair.0, eps) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected, actual_result)` float pairs in `cases` within `eps`.
@test_only
@track_caller
bind assert_all_ok_approx_eq = fn(cases: [+[float, Result[float]]], eps: float): _assert_all_ok_approx_eq_impl(
	cases,
	eps,
	"",
)

// Evaluates all `(expected, actual_result)` float pairs within `eps`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_approx_eq_msg = fn(cases: [+[float, Result[float]]], eps: float, msg: string): _assert_all_ok_approx_eq_impl(
	cases,
	eps,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_true_impl = fn(cases: [Result[bool]], msg: string): {
	let failures: [string] = []
	for(i, res! in cases):
		match(res!):
			when value(val): if(not val): _append_batch_failure(
				&failures,
				i,
				"",
				"expected true, but condition evaluated to false",
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `Result[bool]` values in `cases`, asserting every item is `Ok(true)`.
@test_only
@track_caller
bind assert_all_ok_true = fn(cases: [Result[bool]]): _assert_all_ok_true_impl(cases, "")

// Evaluates all `Result[bool]` values in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_true_msg = fn(cases: [Result[bool]], msg: string): _assert_all_ok_true_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_false_impl = fn(cases: [Result[bool]], msg: string): {
	let failures: [string] = []
	for(i, res! in cases):
		match(res!):
			when value(val): if(val): _append_batch_failure(
				&failures,
				i,
				"",
				"expected false, but condition evaluated to true",
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `Result[bool]` values in `cases`, asserting every item is `Ok(false)`.
@test_only
@track_caller
bind assert_all_ok_false = fn(cases: [Result[bool]]): _assert_all_ok_false_impl(cases, "")

// Evaluates all `Result[bool]` values in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_false_msg = fn(cases: [Result[bool]], msg: string): _assert_all_ok_false_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_gt_impl[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_gt(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(threshold, actual_result)` pairs in `cases`, asserting `Ok(val) > threshold`.
@test_only
@track_caller
bind assert_all_ok_gt[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]]): _assert_all_ok_gt_impl(
	cases,
	"",
)

// Evaluates all `(threshold, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_gt_msg[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): _assert_all_ok_gt_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_lt_impl[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_lt(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(threshold, actual_result)` pairs in `cases`, asserting `Ok(val) < threshold`.
@test_only
@track_caller
bind assert_all_ok_lt[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]]): _assert_all_ok_lt_impl(
	cases,
	"",
)

// Evaluates all `(threshold, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_lt_msg[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): _assert_all_ok_lt_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_ge_impl[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_ge(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(threshold, actual_result)` pairs in `cases`, asserting `Ok(val) >= threshold`.
@test_only
@track_caller
bind assert_all_ok_ge[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]]): _assert_all_ok_ge_impl(
	cases,
	"",
)

// Evaluates all `(threshold, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_ge_msg[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): _assert_all_ok_ge_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_le_impl[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_le(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(threshold, actual_result)` pairs in `cases`, asserting `Ok(val) <= threshold`.
@test_only
@track_caller
bind assert_all_ok_le[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]]): _assert_all_ok_le_impl(
	cases,
	"",
)

// Evaluates all `(threshold, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_le_msg[T: Comparable + Printable] = fn(cases: [+[T, Result[T]]], msg: string): _assert_all_ok_le_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_contains_impl = fn(cases: [+[string, Result[string]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_contains(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected_substring, actual_result)` pairs in `cases`, asserting `Ok(val).contains(expected_substring)`.
@test_only
@track_caller
bind assert_all_ok_contains = fn(cases: [+[string, Result[string]]]): _assert_all_ok_contains_impl(
	cases,
	"",
)

// Evaluates all `(expected_substring, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_contains_msg = fn(cases: [+[string, Result[string]]], msg: string): _assert_all_ok_contains_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_starts_with_impl = fn(cases: [+[string, Result[string]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_starts_with(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected_prefix, actual_result)` pairs in `cases`, asserting `Ok(val).starts_with(expected_prefix)`.
@test_only
@track_caller
bind assert_all_ok_starts_with = fn(cases: [+[string, Result[string]]]): _assert_all_ok_starts_with_impl(
	cases,
	"",
)

// Evaluates all `(expected_prefix, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_starts_with_msg = fn(cases: [+[string, Result[string]]], msg: string): _assert_all_ok_starts_with_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_ends_with_impl = fn(cases: [+[string, Result[string]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when value(val): if(_check_ends_with(val, pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected_suffix, actual_result)` pairs in `cases`, asserting `Ok(val).ends_with(expected_suffix)`.
@test_only
@track_caller
bind assert_all_ok_ends_with = fn(cases: [+[string, Result[string]]]): _assert_all_ok_ends_with_impl(
	cases,
	"",
)

// Evaluates all `(expected_suffix, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_ends_with_msg = fn(cases: [+[string, Result[string]]], msg: string): _assert_all_ok_ends_with_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_ok_with_impl[T: Printable] = fn(cases: [Result[T]], check: cl(T) bool, msg: string): {
	let failures: [string] = []
	for(i, res! in cases):
		match(res!):
			when value(val): if(not check(val)): _append_batch_failure(
				&failures,
				i,
				"",
				"Ok value did not satisfy predicate\n" + _format_labeled_value(
					"  actual",
					val.to_string(),
				),
			)
			default err(e): _append_batch_failure(&failures, i, "", _format_result_not_ok_detail(e))
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates `check(val)` for every [`Result`] in `cases`, reporting every `Err` or predicate failure.
@test_only
@track_caller
bind assert_all_ok_with[T: Printable] = fn(cases: [Result[T]], check: cl(T) bool): _assert_all_ok_with_impl(
	cases,
	check,
	"",
)

// Evaluates `check(val)` for every [`Result`] in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_ok_with_msg[T: Printable] = fn(cases: [Result[T]], check: cl(T) bool, msg: string): _assert_all_ok_with_impl(
	cases,
	check,
	msg,
)

@test_only
@track_caller
bind _assert_all_err_contains_impl[T] = fn(cases: [+[string, Result[T]]], msg: string): {
	let failures: [string] = []
	for(i, pair in cases):
		match(pair.1):
			when err(e): if(_check_contains(e.to_string(), pair.0) as detail): _append_batch_failure(
				&failures,
				i,
				"",
				detail,
			)
			default value(_): _append_batch_failure(
				&failures,
				i,
				"",
				"expected a failure, but the Result holds a value",
			)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all `(expected_substring, actual_result)` pairs in `cases`, asserting `Err` contains `expected_substring`.
@test_only
@track_caller
bind assert_all_err_contains[T] = fn(cases: [+[string, Result[T]]]): _assert_all_err_contains_impl(
	cases,
	"",
)

// Evaluates all `(expected_substring, actual_result)` pairs in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_err_contains_msg[T] = fn(cases: [+[string, Result[T]]], msg: string): _assert_all_err_contains_impl(
	cases,
	msg,
)

@test_only
@track_caller
bind _assert_all_some_impl[T] = fn(cases: [?T], msg: string): {
	let failures: [string] = []
	for(i, val? in cases): if(not (val? != none)): _append_batch_failure(
		&failures,
		i,
		"",
		"expected optional to have a value (?T), but got none",
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all optional values in `cases`, reporting every index that is `none`.
@test_only
@track_caller
bind assert_all_some[T] = fn(cases: [?T]): _assert_all_some_impl(cases, "")

// Evaluates all optional values in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_some_msg[T] = fn(cases: [?T], msg: string): _assert_all_some_impl(cases, msg)

@test_only
@track_caller
bind _assert_all_none_impl[T] = fn(cases: [?T], msg: string): {
	let failures: [string] = []
	for(i, val? in cases): if(val? != none): _append_batch_failure(
		&failures,
		i,
		"",
		"expected optional to be none, but got a value",
	)
	_panic_batch_if_failed(cases.len(), failures, msg)
}

// Evaluates all optional values in `cases`, reporting every index that holds a value.
@test_only
@track_caller
bind assert_all_none[T] = fn(cases: [?T]): _assert_all_none_impl(cases, "")

// Evaluates all optional values in `cases`, prefixing any batch failure with `msg`.
@test_only
@track_caller
bind assert_all_none_msg[T] = fn(cases: [?T], msg: string): _assert_all_none_impl(cases, msg)

// -----------------------------------------------------------------------------
// Inline Tests for std/test.ll
// -----------------------------------------------------------------------------

@test("diff formatting helpers")
bind test_diff_formatting = fn(): {
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
	assert_true(
		_str_contains_sub(
			extra_utf8,
			"\n" + _repeat_space(14) + "^ unexpected 1 extra char(s) in got",
		)
	)

	let missing_utf8 = _format_single_line_diff("αβ", "αβγ")
	assert_true(
		_str_contains_sub(missing_utf8, "\n" + _repeat_space(14) + "^ missing 1 expected char(s)")
	)

	let multi_utf8 = _format_multiline_diff("top\nαβδ\nbot", "top\nαβγ\nbot")
	assert_true(_str_contains_sub(multi_utf8, "\n" + _repeat_space(16) + "^ diff at index 2"))

	let tab_diff = _format_multiline_diff("top\n\t\t\"abc\": []", "top\n\t\t\"abc\": [1]")
	assert_true(
		_str_contains_sub(
			tab_diff,
			"\n" + _repeat_space(14) + "\t\t        ^ diff at index 10",
		)
	)

	let eof_diff = _format_multiline_diff("a", "a\nb")
	assert_true(_str_contains_sub(eof_diff, "    expected: b\n         got: <end of input>"))
}

@test("failure formatting readability helpers")
bind test_failure_formatting_readability = fn(): {
	let raw_err = "outer: bad value\n\ntrace:\n  at a.ll:10:2 — outer\n  at b.ll:20:4 — bad value\n"
	let err_block = _format_error_block(raw_err)
	assert_eq(
		err_block,
		"  error: outer: bad value\n  trace:\n    at a.ll:10:2 — outer\n    at b.ll:20:4 — bad value",
	)

	let multi_err = _format_error_block("first line\nsecond line\ntrace:\n  at c.ll:1:1")
	assert_eq(
		multi_err,
		"  error: first line\n         second line\n  trace:\n    at c.ll:1:1",
	)

	let labeled = _format_labeled_value("     actual", "line1\n\nline3")
	assert_eq(labeled, "     actual: line1\n\n             line3")

	let indented = _indent_block("line1\n\nline3", "  ")
	assert_eq(indented, "  line1\n\n  line3")
}

@test("assert_eq and assert_ne basic assertions")
bind test_equality_assertions = fn(): {
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
bind test_boolean_assertions = fn(): {
	assert_true(1 < 2)
	assert_true_msg(10 > 5, "ten is greater than five")
	assert_false(1 > 2)
	assert_false_msg(5 > 10, "five is not greater than ten")
}

@test("assert comparisons (gt, lt, ge, le)")
bind test_comparison_assertions = fn(): {
	assert_gt(10, 5)
	assert_lt(5, 10)
	assert_ge(10, 10)
	assert_ge(11, 10)
	assert_le(10, 10)
	assert_le(9, 10)
}

@test("assert_is_ok and assert_is_err result assertions")
bind test_result_assertions = fn(): {
	let ok_res! = Result[int].value(123)
	assert_is_ok(&ok_res!)
	assert_is_ok_msg(&ok_res!, "result should be ok")

	let err_res! = Result[int].fail_msg("failure")
	assert_is_err(&err_res!)
	assert_is_err_msg(&err_res!, "result should be err")
}

@test("assert_some and assert_none optional assertions")
bind test_optional_assertions = fn(): {
	let val?: ?int = 42
	assert_some(val?)

	let empty?: ?int = none
	assert_none(empty?)
}

@test("assert_approx_eq float assertions and edge cases")
bind test_approx_eq_assertions = fn(): {
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
bind test_string_match_assertions = fn(): {
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
bind test_combined_result_assertions = fn(): {
	let r_int! = Result[int].value(42)
	assert_ok_eq(r_int!, 42)
	assert_ok_eq_msg(Result[int].value(7), 7, "inline rvalue ok_eq")
	assert_ok_ne(r_int!, 0)
	assert_ok_ne_msg(r_int!, 99, "not ninety-nine")
	assert_ok_gt(r_int!, 41)
	assert_ok_gt_msg(r_int!, 10, "greater than ten")
	assert_ok_lt(r_int!, 100)
	assert_ok_lt_msg(r_int!, 50, "less than fifty")
	assert_ok_ge(r_int!, 42)
	assert_ok_ge_msg(r_int!, 40, "at least forty")
	assert_ok_le(r_int!, 42)
	assert_ok_le_msg(r_int!, 45, "at most forty-five")
	assert_ok_with(r_int!, fn(n: int) bool: n % 2 == 0)
	assert_ok_with_msg(r_int!, fn(n: int) bool: n > 0, "positive integer")

	assert_ok_approx_eq(Result[float].value(0.1 + 0.2), 0.3, 1.0e-9)
	assert_ok_approx_eq_msg(Result[float].value(3.1415), 3.14, 0.01, "pi approx")

	assert_ok_true(Result[bool].value(true))
	assert_ok_true_msg(Result[bool].value(true), "ok true")
	assert_ok_false(Result[bool].value(false))
	assert_ok_false_msg(Result[bool].value(false), "ok false")

	let r_str! = Result[string].value("lowlight/std/test.ll")
	assert_ok_contains(r_str!, "std/test")
	assert_ok_contains_msg(r_str!, "lowlight", "contains package")
	assert_ok_starts_with(r_str!, "lowlight/")
	assert_ok_starts_with_msg(r_str!, "lowlight/", "starts with repo")
	assert_ok_ends_with(r_str!, ".ll")
	assert_ok_ends_with_msg(r_str!, "test.ll", "ends with file")

	let r_err! = Result[int].fail_msg("boom on port 8080")
	assert_err_contains(r_err!, "port 8080")
	assert_err_contains_msg(r_err!, "boom", "error mentions boom")

	// Existing comparison _msg variants
	assert_gt_msg(10, 5, "10 > 5")
	assert_lt_msg(5, 10, "5 < 10")
	assert_ge_msg(10, 10, "10 >= 10")
	assert_le_msg(10, 10, "10 <= 10")
}

@test("assert_all_* batch pair and unary assertions")
bind test_batch_assertions = fn(): {
	// Empty batches pass vacuously
	let empty_int: [+[int, int]] = []
	assert_all_eq(empty_int)

	// (expected, actual) pairs
	assert_all_eq([+[42, 40 + 2], +[0, 5 - 5], +[-3, -3]])
	assert_all_eq_msg([+["hello", "hel" + "lo"], +["", ""]], "string concat")
	assert_all_eq_named([
		+["zero", 0, 0],
		+["sum", 10, 4 + 6],
	])
	assert_all_eq_named_msg([+["ok", 1, 1]], "named batch")
	assert_all_eq_by([+[10, 12], +[20, 23]], fn(a: int, b: int) bool: a - b <= 5 and b - a <= 5)
	assert_all_eq_by_msg(
		[+["ABC", "abc"]],
		fn(a: string, b: string) bool: a.len() == b.len(),
		"same len",
	)
	assert_any_eq([+[1, 9], +[2, 2], +[3, 8]])
	assert_any_eq_msg([+["a", "b"], +["ok", "ok"]], "one matches")

	assert_all_ne([+[1, 2], +[3, 4]])
	assert_all_ne_msg([+["foo", "bar"], +["x", "y"]], "distinct strings")

	assert_all_approx_eq([+[0.3, 0.1 + 0.2], +[1.0, 1.0005]], 0.001)
	assert_all_approx_eq_msg([+[3.14, 3.141]], 0.01, "pi approx batch")

	assert_all_true([1 < 2, "abc".len() == 3])
	assert_all_true_msg([true, 2 + 2 == 4], "all true")
	assert_all_false([1 > 2, false])
	assert_all_false_msg([false, 1 == 2], "all false")
	assert_all_with([2, 4, 6], fn(n: int) bool: n % 2 == 0)
	assert_all_with_msg([1, 3, 5], fn(n: int) bool: n > 0, "positive")

	// (threshold, actual) comparisons
	assert_all_gt([+[5, 10], +[0, 1]])
	assert_all_gt_msg([+[10, 20]], "gt batch")
	assert_all_lt([+[10, 5], +[1, 0]])
	assert_all_lt_msg([+[20, 10]], "lt batch")
	assert_all_ge([+[10, 10], +[10, 11]])
	assert_all_ge_msg([+[5, 5]], "ge batch")
	assert_all_le([+[10, 10], +[10, 9]])
	assert_all_le_msg([+[5, 4]], "le batch")

	// (expected_match, actual) string checks
	assert_all_contains([+["lowlight", "hello lowlight"], +["", "abc"]])
	assert_all_contains_msg([+[":beta:", "alpha:beta:gamma"]], "contains batch")
	assert_all_starts_with([+["https://", "https://example.com"], +["", "abc"]])
	assert_all_starts_with_msg([+["std/", "std/test"]], "starts_with batch")
	assert_all_ends_with([+[".ll", "std/test.ll"], +["", "abc"]])
	assert_all_ends_with_msg([+[".tar.gz", "a.tar.gz"]], "ends_with batch")

	// Result and optional batches
	assert_all_is_ok([Result[int].value(1), Result[int].value(2)])
	assert_all_is_ok_msg([Result[int].value(3)], "all ok")
	assert_all_is_err([Result[int].fail_msg("e1"), Result[int].fail_msg("e2")])
	assert_all_is_err_msg([Result[int].fail_msg("e3")], "all err")

	assert_all_ok_eq([+[42, Result[int].value(42)], +[7, Result[int].value(7)]])
	assert_all_ok_eq_msg([+["hi", Result[string].value("hi")]], "ok strings")
	assert_all_ok_eq_named([
		+["first", 10, Result[int].value(10)],
		+["second", 20, Result[int].value(20)],
	])
	assert_all_ok_eq_named_msg([+["only", 1, Result[int].value(1)]], "named ok")
	assert_all_ok_ne([+[0, Result[int].value(1)], +[2, Result[int].value(3)]])
	assert_all_ok_ne_msg([+[99, Result[int].value(1)]], "ok ne")
	assert_all_ok_approx_eq([+[0.3, Result[float].value(0.1 + 0.2)]], 1.0e-9)
	assert_all_ok_approx_eq_msg([+[3.14, Result[float].value(3.1415)]], 0.01, "ok approx")
	assert_all_ok_true([Result[bool].value(true), Result[bool].value(true)])
	assert_all_ok_true_msg([Result[bool].value(true)], "ok true batch")
	assert_all_ok_false([Result[bool].value(false), Result[bool].value(false)])
	assert_all_ok_false_msg([Result[bool].value(false)], "ok false batch")
	assert_all_ok_gt([+[10, Result[int].value(11)], +[0, Result[int].value(5)]])
	assert_all_ok_gt_msg([+[10, Result[int].value(12)]], "ok gt")
	assert_all_ok_lt([+[10, Result[int].value(9)], +[5, Result[int].value(1)]])
	assert_all_ok_lt_msg([+[10, Result[int].value(8)]], "ok lt")
	assert_all_ok_ge([+[10, Result[int].value(10)], +[10, Result[int].value(15)]])
	assert_all_ok_ge_msg([+[10, Result[int].value(10)]], "ok ge")
	assert_all_ok_le([+[10, Result[int].value(10)], +[10, Result[int].value(5)]])
	assert_all_ok_le_msg([+[10, Result[int].value(9)]], "ok le")
	assert_all_ok_contains([+["std", Result[string].value("lowlight/std/test.ll")]])
	assert_all_ok_contains_msg([+["test", Result[string].value("test.ll")]], "ok contains")
	assert_all_ok_starts_with([+["low", Result[string].value("lowlight")]])
	assert_all_ok_starts_with_msg([+["low", Result[string].value("lowlight")]], "ok starts_with")
	assert_all_ok_ends_with([+[".ll", Result[string].value("test.ll")]])
	assert_all_ok_ends_with_msg([+[".ll", Result[string].value("test.ll")]], "ok ends_with")
	assert_all_ok_with([Result[int].value(2), Result[int].value(4)], fn(n: int) bool: n % 2 == 0)
	assert_all_ok_with_msg([Result[int].value(6)], fn(n: int) bool: n > 0, "ok with")
	assert_all_err_contains([+["port 8080", Result[int].fail_msg("boom on port 8080")]])
	assert_all_err_contains_msg([+["boom", Result[int].fail_msg("boom")]], "err contains")

	let o1?: ?int = 1
	let o2?: ?int = 2
	assert_all_some([o1?, o2?])
	assert_all_some_msg([o1?], "some batch")

	let n1?: ?int = none
	let n2?: ?int = none
	assert_all_none([n1?, n2?])
	assert_all_none_msg([n1?], "none batch")
}
