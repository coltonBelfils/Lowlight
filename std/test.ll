// Lowlight Standard Library: std/test.ll
// Testing assertions, test utilities, and verification helpers.

// Asserts that `actual` equals `expected`. Panics with a diagnostic message if not equal.
@test_only
bind assert_eq[T: Equatable + Printable] = (actual: T, expected: T) {
    if (actual != expected) {
        panic("assertion failed: expected " + expected.to_string() + ", but got " + actual.to_string())
    }
}

// Asserts that `actual` equals `expected` with a custom failure message.
@test_only
bind assert_eq_msg[T: Equatable + Printable] = (actual: T, expected: T, msg: string) {
    if (actual != expected) {
        panic(msg + " (expected " + expected.to_string() + ", but got " + actual.to_string() + ")")
    }
}

// Asserts that `actual` does not equal `unexpected`. Panics if they are equal.
@test_only
bind assert_ne[T: Equatable + Printable] = (actual: T, unexpected: T) {
    if (actual == unexpected) {
        panic("assertion failed: expected value not to equal " + unexpected.to_string())
    }
}

// Asserts that `actual` does not equal `unexpected` with a custom failure message.
@test_only
bind assert_ne_msg[T: Equatable + Printable] = (actual: T, unexpected: T, msg: string) {
    if (actual == unexpected) {
        panic(msg + " (expected value not to equal " + unexpected.to_string() + ")")
    }
}

// Asserts that `cond` is true.
@test_only
bind assert_true = (cond: bool) {
    if (not cond) {
        panic("assertion failed: expected condition to be true")
    }
}

// Asserts that `cond` is true with a custom failure message.
@test_only
bind assert_true_msg = (cond: bool, msg: string) {
    if (not cond) {
        panic(msg + " (expected condition to be true)")
    }
}

// Asserts that `cond` is false.
@test_only
bind assert_false = (cond: bool) {
    if (cond) {
        panic("assertion failed: expected condition to be false")
    }
}

// Asserts that `cond` is false with a custom failure message.
@test_only
bind assert_false_msg = (cond: bool, msg: string) {
    if (cond) {
        panic(msg + " (expected condition to be false)")
    }
}

// Asserts that `actual` is strictly greater than `expected`.
@test_only
bind assert_gt[T: Comparable + Printable] = (actual: T, expected: T) {
    if (actual <= expected) {
        panic("assertion failed: expected " + actual.to_string() + " > " + expected.to_string())
    }
}

// Asserts that `actual` is strictly less than `expected`.
@test_only
bind assert_lt[T: Comparable + Printable] = (actual: T, expected: T) {
    if (actual >= expected) {
        panic("assertion failed: expected " + actual.to_string() + " < " + expected.to_string())
    }
}

// Asserts that `actual` is greater than or equal to `expected`.
@test_only
bind assert_ge[T: Comparable + Printable] = (actual: T, expected: T) {
    if (actual < expected) {
        panic("assertion failed: expected " + actual.to_string() + " >= " + expected.to_string())
    }
}

// Asserts that `actual` is less than or equal to `expected`.
@test_only
bind assert_le[T: Comparable + Printable] = (actual: T, expected: T) {
    if (actual > expected) {
        panic("assertion failed: expected " + actual.to_string() + " <= " + expected.to_string())
    }
}

// Asserts that `res` is Ok.
@test_only
bind assert_is_ok[T, E: Printable] = (res: &Result[T, E]) {
    if (res.is_err()) {
        let err_str = if (res.err? as err) err.to_string() else "<unknown error>"
        panic("assertion failed: expected Result to be Ok, got Err(" + err_str + ")")
    }
}

// Asserts that `res` is Ok with a custom failure message.
@test_only
bind assert_is_ok_msg[T, E: Printable] = (res: &Result[T, E], msg: string) {
    if (res.is_err()) {
        let err_str = if (res.err? as err) err.to_string() else "<unknown error>"
        panic(msg + " (expected Result to be Ok, got Err(" + err_str + "))")
    }
}

// Asserts that `res` is Err.
@test_only
bind assert_is_err[T, E] = (res: &Result[T, E]) {
    if (res.is_ok()) {
        panic("assertion failed: expected Result to be Err, but it was Ok")
    }
}

// Asserts that `res` is Err with a custom failure message.
@test_only
bind assert_is_err_msg[T, E] = (res: &Result[T, E], msg: string) {
    if (res.is_ok()) {
        panic(msg + " (expected Result to be Err, but it was Ok)")
    }
}

// Asserts that an optional has a value (`val? != none`).
@test_only
bind assert_some[T] = (val?: ?T) {
    if (not (val? != none)) {
        panic("assertion failed: expected optional to have a value, got none")
    }
}

// Asserts that an optional is absent (`val? == none`).
@test_only
bind assert_none[T] = (val?: ?T) {
    if (val? != none) {
        panic("assertion failed: expected optional to be none, got value")
    }
}

// -----------------------------------------------------------------------------
// Inline Tests for std/test.ll
// -----------------------------------------------------------------------------

@test("assert_eq and assert_ne basic assertions")
bind test_equality_assertions = () {
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
bind test_boolean_assertions = () {
    assert_true(1 < 2)
    assert_true_msg(10 > 5, "ten is greater than five")
    assert_false(1 > 2)
    assert_false_msg(5 > 10, "five is not greater than ten")
}

@test("assert comparisons (gt, lt, ge, le)")
bind test_comparison_assertions = () {
    assert_gt(10, 5)
    assert_lt(5, 10)
    assert_ge(10, 10)
    assert_ge(11, 10)
    assert_le(10, 10)
    assert_le(9, 10)
}

@test("assert_is_ok and assert_is_err result assertions")
bind test_result_assertions = () {
    let ok_res = Result[int, string].ok(123)
    assert_is_ok(&ok_res)
    assert_is_ok_msg(&ok_res, "result should be ok")

    let err_res = Result[int, string].fail("failure")
    assert_is_err(&err_res)
    assert_is_err_msg(&err_res, "result should be err")
}

@test("assert_some and assert_none optional assertions")
bind test_optional_assertions = () {
    let val?: ?int = 42
    assert_some(val?)

    let empty?: ?int = none
    assert_none(empty?)
}
