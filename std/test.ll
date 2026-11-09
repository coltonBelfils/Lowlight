// Lowlight Standard Library: std/test.ll
// Testing assertions, test utilities, verification helpers, and rich diagnostics diffing.

// -----------------------------------------------------------------------------
// Failure Diagnostics & Diff Formatting Helpers
// -----------------------------------------------------------------------------

@test_only
bind _str_has_newline = (s: string): bool -> {
    for (ch in s) {
        if (ch == '\n') func <- true
    }
    false
}

@test_only
bind _repeat_space = (n: int): string -> {
    let out = ""
    for (i in 0..n) {
        out.push(" ")
    }
    out
}

@test_only
bind _split_lines = (s: string): [string] -> {
    let out: [string] = []
    let cur = ""
    for (ch in s) {
        if (ch == '\n') {
            out.push(own cur)
            cur = ""
        } else {
            cur.push_char(ch)
        }
    }
    out.push(own cur)
    out
}

@test_only
bind _str_contains_sub = (haystack: string, needle: string): bool -> {
    if (needle.len() == 0) func <- true
    if (needle.len() > haystack.len()) func <- false
    let max_start = haystack.len() - needle.len()
    for (i in 0..=max_start) {
        if (haystack[i..(i + needle.len())] == needle) func <- true
    }
    false
}

// Formats expected vs actual values with aligned headers and character diff pointer.
@test_only
@track_caller
bind format_diff = (actual_str: string, expected_str: string): string -> {
    if (_str_has_newline(actual_str) or _str_has_newline(expected_str)) {
        _format_multiline_diff(actual_str, expected_str)
    } else {
        _format_single_line_diff(actual_str, expected_str)
    }
}

// Formats single-line got vs expected with aligned columns and a pointer at the first mismatch.
@test_only
@track_caller
bind _format_single_line_diff = (actual_str: string, expected_str: string): string -> {
    let act_len = actual_str.len()
    let exp_len = expected_str.len()
    let min_len = if (act_len < exp_len) act_len else exp_len

    let diff_idx = -1
    for (i in 0..min_len) {
        if (actual_str[i] != expected_str[i]) {
            diff_idx = i
            for <-
        }
    }

    let out = "  expected: " + expected_str + "\n       got: " + actual_str
    if (diff_idx >= 0) {
        let pointer = _repeat_space(12 + diff_idx) + "^ diff at index " + diff_idx.to_string()
        out = out + "\n" + pointer
    } else if (act_len > exp_len) {
        let extra = act_len - exp_len
        let pointer = _repeat_space(12 + exp_len) + "^ unexpected " + extra.to_string() + " extra byte(s) in got"
        out = out + "\n" + pointer
    } else if (exp_len > act_len) {
        let missing = exp_len - act_len
        let pointer = _repeat_space(12 + act_len) + "^ missing " + missing.to_string() + " expected byte(s)"
        out = out + "\n" + pointer
    }
    out
}

// Formats multiline got vs expected showing the mismatched line number, aligned lines, and character pointer.
@test_only
@track_caller
bind _format_multiline_diff = (actual_str: string, expected_str: string): string -> {
    let act_lines = _split_lines(actual_str)
    let exp_lines = _split_lines(expected_str)
    let min_count = if (act_lines.len() < exp_lines.len()) act_lines.len() else exp_lines.len()

    let diff_line = -1
    for (i in 0..min_count) {
        if (act_lines[i] != exp_lines[i]) {
            diff_line = i
            for <-
        }
    }
    if (diff_line == -1) {
        diff_line = min_count
    }

    let out = "  first mismatch at line " + (diff_line + 1).to_string() + ":\n"
    if (diff_line < exp_lines.len() and diff_line < act_lines.len()) {
        let exp_line = exp_lines[diff_line].copy()
        let act_line = act_lines[diff_line].copy()
        out = out + "    expected: " + exp_line + "\n"
        out = out + "         got: " + act_line

        // Find character mismatch within the line
        let line_min = if (act_line.len() < exp_line.len()) act_line.len() else exp_line.len()
        let char_idx = -1
        for (c in 0..line_min) {
            if (act_line[c] != exp_line[c]) {
                char_idx = c
                for <-
            }
        }
        if (char_idx == -1 and act_line.len() != exp_line.len()) {
            char_idx = line_min
        }
        if (char_idx >= 0) {
            let pointer = _repeat_space(14 + char_idx) + "^"
            out = out + "\n" + pointer
        }
    } else if (diff_line < exp_lines.len()) {
        out = out + "    expected line " + (diff_line + 1).to_string() + ": " + exp_lines[diff_line] + "\n"
        out = out + "         got: <end of input>"
    } else if (diff_line < act_lines.len()) {
        out = out + "    expected: <end of input>\n"
        out = out + "         got line " + (diff_line + 1).to_string() + ": " + act_lines[diff_line]
    }

    if (act_lines.len() != exp_lines.len()) {
        out = out + "\n  (total line count: expected " + exp_lines.len().to_string() + ", got " + act_lines.len().to_string() + ")"
    }
    out
}

// -----------------------------------------------------------------------------
// Assertions
// -----------------------------------------------------------------------------

// Asserts that `actual` equals `expected`. Panics with an aligned visual diff if not equal.
@test_only
@track_caller
bind assert_eq[T: Equatable + Printable] = (actual: T, expected: T) -> {
    if (actual != expected) {
        let diff = format_diff(actual.to_string(), expected.to_string())
        panic("assertion failed: values are not equal\n" + diff)
    }
}

// Asserts that `actual` equals `expected` with a custom failure message.
@test_only
@track_caller
bind assert_eq_msg[T: Equatable + Printable] = (actual: T, expected: T, msg: string) -> {
    if (actual != expected) {
        let diff = format_diff(actual.to_string(), expected.to_string())
        panic("assertion failed: " + msg + "\n" + diff)
    }
}

// Asserts that `actual` does not equal `unexpected`. Panics if they are equal.
@test_only
@track_caller
bind assert_ne[T: Equatable + Printable] = (actual: T, unexpected: T) -> {
    if (actual == unexpected) {
        let s = unexpected.to_string()
        panic("assertion failed: values must not be equal\n  unexpected: " + s + "\n         got: " + s)
    }
}

// Asserts that `actual` does not equal `unexpected` with a custom failure message.
@test_only
@track_caller
bind assert_ne_msg[T: Equatable + Printable] = (actual: T, unexpected: T, msg: string) -> {
    if (actual == unexpected) {
        let s = unexpected.to_string()
        panic("assertion failed: " + msg + " (values must not be equal)\n  unexpected: " + s + "\n         got: " + s)
    }
}

// Asserts that `cond` is true.
@test_only
@track_caller
bind assert_true = (cond: bool) -> {
    if (not cond) {
        panic("assertion failed: expected true, but condition evaluated to false")
    }
}

// Asserts that `cond` is true with a custom failure message.
@test_only
@track_caller
bind assert_true_msg = (cond: bool, msg: string) -> {
    if (not cond) {
        panic("assertion failed: " + msg + " (expected true, but got false)")
    }
}

// Asserts that `cond` is false.
@test_only
@track_caller
bind assert_false = (cond: bool) -> {
    if (cond) {
        panic("assertion failed: expected false, but condition evaluated to true")
    }
}

// Asserts that `cond` is false with a custom failure message.
@test_only
@track_caller
bind assert_false_msg = (cond: bool, msg: string) -> {
    if (cond) {
        panic("assertion failed: " + msg + " (expected false, but got true)")
    }
}

// Asserts that `actual` is strictly greater than `expected`.
@test_only
@track_caller
bind assert_gt[T: Comparable + Printable] = (actual: T, expected: T) -> {
    if (actual <= expected) {
        panic("assertion failed: expected actual > threshold\n     actual: " + actual.to_string() + "\n  threshold: " + expected.to_string())
    }
}

// Asserts that `actual` is strictly less than `expected`.
@test_only
@track_caller
bind assert_lt[T: Comparable + Printable] = (actual: T, expected: T) -> {
    if (actual >= expected) {
        panic("assertion failed: expected actual < threshold\n     actual: " + actual.to_string() + "\n  threshold: " + expected.to_string())
    }
}

// Asserts that `actual` is greater than or equal to `expected`.
@test_only
@track_caller
bind assert_ge[T: Comparable + Printable] = (actual: T, expected: T) -> {
    if (actual < expected) {
        panic("assertion failed: expected actual >= threshold\n     actual: " + actual.to_string() + "\n  threshold: " + expected.to_string())
    }
}

// Asserts that `actual` is less than or equal to `expected`.
@test_only
@track_caller
bind assert_le[T: Comparable + Printable] = (actual: T, expected: T) -> {
    if (actual > expected) {
        panic("assertion failed: expected actual <= threshold\n     actual: " + actual.to_string() + "\n  threshold: " + expected.to_string())
    }
}

// Asserts that `res` is Ok.
@test_only
@track_caller
bind assert_is_ok[T, E: Printable] = (res: &Result[T, E]) -> {
    if (res.is_err()) {
        let err_str = if (res.err? as err) err.to_string() else "<unknown error>"
        panic("assertion failed: expected Result to be Ok, but got Err\n  error: " + err_str)
    }
}

// Asserts that `res` is Ok with a custom failure message.
@test_only
@track_caller
bind assert_is_ok_msg[T, E: Printable] = (res: &Result[T, E], msg: string) -> {
    if (res.is_err()) {
        let err_str = if (res.err? as err) err.to_string() else "<unknown error>"
        panic("assertion failed: " + msg + " (expected Result to be Ok, got Err)\n  error: " + err_str)
    }
}

// Asserts that `res` is Err.
@test_only
@track_caller
bind assert_is_err[T, E] = (res: &Result[T, E]) -> {
    if (res.is_ok()) {
        panic("assertion failed: expected Result to be Err, but got Ok")
    }
}

// Asserts that `res` is Err with a custom failure message.
@test_only
@track_caller
bind assert_is_err_msg[T, E] = (res: &Result[T, E], msg: string) -> {
    if (res.is_ok()) {
        panic("assertion failed: " + msg + " (expected Result to be Err, but got Ok)")
    }
}

// Asserts that an optional has a value (`val? != none`).
@test_only
@track_caller
bind assert_some[T] = (val?: ?T) -> {
    if (not (val? != none)) {
        panic("assertion failed: expected optional to have a value (?T), but got none")
    }
}

// Asserts that an optional is absent (`val? == none`).
@test_only
@track_caller
bind assert_none[T] = (val?: ?T) -> {
    if (val? != none) {
        panic("assertion failed: expected optional to be none, but got a value")
    }
}

// -----------------------------------------------------------------------------
// Inline Tests for std/test.ll
// -----------------------------------------------------------------------------

@test("diff formatting helpers")
bind test_diff_formatting = () -> {
    let diff = _format_single_line_diff("hello world", "hello there")
    assert_true(_str_contains_sub(diff, "expected: hello there"))
    assert_true(_str_contains_sub(diff, "got: hello world"))
    assert_true(_str_contains_sub(diff, "^ diff at index 6"))

    let multi_diff = _format_multiline_diff("line1\nline2_bad\nline3", "line1\nline2_good\nline3")
    assert_true(_str_contains_sub(multi_diff, "first mismatch at line 2"))
    assert_true(_str_contains_sub(multi_diff, "expected: line2_good"))
    assert_true(_str_contains_sub(multi_diff, "got: line2_bad"))
}

@test("assert_eq and assert_ne basic assertions")
bind test_equality_assertions = () -> {
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
bind test_boolean_assertions = () -> {
    assert_true(1 < 2)
    assert_true_msg(10 > 5, "ten is greater than five")
    assert_false(1 > 2)
    assert_false_msg(5 > 10, "five is not greater than ten")
}

@test("assert comparisons (gt, lt, ge, le)")
bind test_comparison_assertions = () -> {
    assert_gt(10, 5)
    assert_lt(5, 10)
    assert_ge(10, 10)
    assert_ge(11, 10)
    assert_le(10, 10)
    assert_le(9, 10)
}

@test("assert_is_ok and assert_is_err result assertions")
bind test_result_assertions = () -> {
    let ok_res = Result[int, string].ok(123)
    assert_is_ok(&ok_res)
    assert_is_ok_msg(&ok_res, "result should be ok")

    let err_res = Result[int, string].fail("failure")
    assert_is_err(&err_res)
    assert_is_err_msg(&err_res, "result should be err")
}

@test("assert_some and assert_none optional assertions")
bind test_optional_assertions = () -> {
    let val?: ?int = 42
    assert_some(val?)

    let empty?: ?int = none
    assert_none(empty?)
}
