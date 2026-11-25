// std/core — Standard core prelude automatically imported into every Lowlight module.
//
// Aggregates foundational primitives ([`std/core/primitives`]), language contract
// interfaces ([`std/core/interfaces`]), error handling ([`std/core/error`],
// [`std/core/result`]), collections ([`std/core/list`], [`std/core/array`],
// [`std/core/set`], [`std/core/map`]), intervals ([`std/core/range`]), and
// hashing ([`std/core/hash`]).

// =============================================================================
// Standard Core Inline Test Suite
// =============================================================================

@test("core primitive string conversions and hash")
bind test_core_primitives = fn(): {
	assert(0.to_string() == "0", "0 to_string")
	assert(42.to_string() == "42", "42 to_string")
	assert((-17).to_string() == "-17", "-17 to_string")
	assert(true.to_string() == "true", "true to_string")
	assert(false.to_string() == "false", "false to_string")
	assert('A'.to_string() == "A", "'A' to_string")
	assert(u8(255).to_string() == "255", "u8 to_string")
	assert(u16(65535).to_string() == "65535", "u16 to_string")
	assert(u32(4294967295).to_string() == "4294967295", "u32 to_string")
	assert(i8(-128).to_string() == "-128", "i8 to_string")
	assert(i16(-32768).to_string() == "-32768", "i16 to_string")
	assert(i32(-2147483648).to_string() == "-2147483648", "i32 to_string")
	assert("hello".hash() == "hello".hash(), "string hash deterministic")
	assert("hello".hash() != "world".hash(), "string hash diff")
	assert(42.hash() == 42.hash(), "int hash deterministic")
	assert(true.hash() != false.hash(), "bool hash diff")
	assert('x'.hash() == 'x'.hash(), "char hash deterministic")
}

@test_only
bind _core_bytes_str = fn(bs: [int]) string: {
	let s = ""
	for(b in bs): s.push_byte(u8(b))
	s
}

@test_only
bind _core_assert_utf8 = fn(bs: [int], expected_ch: char, expected_next: int, label: string): {
	let s = _core_bytes_str(bs)
	let next_i = -999
	let ch = str_utf8_next(s, 0, &next_i)
	assert(ch == expected_ch and next_i == expected_next, label)
}

@test("core string buffer methods")
bind test_core_string_buffer = fn(): {
	let s = "lit"
	assert(s.cap() == 0, "a literal owns no buffer")
	s.push("eral")
	assert(s == "literal" and s.cap() >= 7, "push onto a literal copies it into an owned buffer")
	s.push("")
	assert(s == "literal", "pushing nothing is a no-op")

	let b = string.with_capacity(4)
	assert(b.len() == 0 and b.cap() >= 4, "with_capacity reserves room")
	b.push("wxyz")
	let view = b[1..3]
	b.push(view)
	assert(b == "wxyzxy", "pushing a view of the string itself survives the buffer moving")

	let c = ""
	c.push_char('a')
	c.push_char('é')
	c.push_char('€')
	c.push_char('𝄞')
	assert(c == "aé€𝄞" and c.len() == 10, "push_char encodes one to four bytes")
	c.push_byte(u8(33))
	let first = "a"
	unsafe c.push_bytes(first.c_str(), 1)
	assert(c == "aé€𝄞!a", "push_byte and push_bytes")

	let cap = c.cap()
	c.clear()
	assert(c == "" and c.cap() == cap, "clear keeps the buffer")
	c.reserve(100)
	assert(c.cap() >= 100 and c.len() == 0, "reserve grows capacity")
	c.reserve(0)
	c.reserve(-3)
	assert(c.cap() >= 100, "reserving nothing keeps capacity")

	let v = "view"
	v.clear()
	assert(v == "" and v.cap() == 0, "clearing a literal leaves an empty literal")
	let r = "abc"
	r.reserve(0)
	assert(r == "abc" and r.cap() >= 3, "reserve turns a literal into an owned buffer")
	let n = string.with_capacity(-1)
	n.push("ok")
	assert(n == "ok", "a negative capacity is treated as zero")

	let nul = "ab"
	nul.push_byte(u8(0))
	nul.push("c")
	assert(nul.contains_byte(u8(0)), "contains_byte detects interior NUL")
	assert(not "abc".contains_byte(u8(0)), "contains_byte is false when absent")
	assert(core_read_file(nul) == none, "core_read_file rejects interior NUL path")
	assert(not core_write_file(nul, "x"), "core_write_file rejects interior NUL path")
}

@test("core str_utf8_next validation and decoding")
bind test_core_str_utf8_next = fn(): {
	// Valid boundary codepoints (1..4 bytes)
	_core_assert_utf8([0x00], '\0', 1, "valid 1-byte NUL")
	_core_assert_utf8([0x7F], char(0x7F), 1, "valid 1-byte max ASCII")
	_core_assert_utf8([0xC2, 0x80], char(0x80), 2, "valid 2-byte min")
	_core_assert_utf8([0xDF, 0xBF], char(0x7FF), 2, "valid 2-byte max")
	_core_assert_utf8([0xE0, 0xA0, 0x80], char(0x0800), 3, "valid 3-byte min")
	_core_assert_utf8([0xED, 0x9F, 0xBF], char(0xD7FF), 3, "valid 3-byte below surrogate")
	_core_assert_utf8([0xEE, 0x80, 0x80], char(0xE000), 3, "valid 3-byte above surrogate")
	_core_assert_utf8([0xEF, 0xBF, 0xBF], char(0xFFFF), 3, "valid 3-byte max")
	_core_assert_utf8([0xF0, 0x90, 0x80, 0x80], char(0x10000), 4, "valid 4-byte min")
	_core_assert_utf8([0xF4, 0x8F, 0xBF, 0xBF], char(0x10FFFF), 4, "valid 4-byte max")

	let fffd = char(0xFFFD)

	// Invalid continuation bytes
	let s2_bad = _core_bytes_str([0xC3, 0x41])
	let c2_bad = s2_bad.chars()
	assert(
		c2_bad.len() == 2 and c2_bad[0] == fffd and c2_bad[1] == 'A',
		"2-byte bad continuation preserves ASCII",
	)

	let s3_bad1 = _core_bytes_str([0xE2, 0x41, 0x80])
	let n3_0 = 0
	assert(str_utf8_next(s3_bad1, 0, &n3_0) == fffd and n3_0 == 1, "3-byte bad b1 yields FFFD")
	let n3_1 = 0
	assert(
		str_utf8_next(s3_bad1, 1, &n3_1) == 'A' and n3_1 == 2,
		"3-byte bad b1 preserves following A",
	)

	_core_assert_utf8([0xE2, 0x82, 0x41], fffd, 1, "3-byte bad b2")
	_core_assert_utf8([0xF0, 0x9F, 0x98, 0x41], fffd, 1, "4-byte bad b3")
	_core_assert_utf8([0xC2, 0xC0], fffd, 1, "2-byte non-continuation high byte")
	_core_assert_utf8([0xF0, 0xF0, 0x80, 0x80], fffd, 1, "4-byte non-continuation high byte")

	// Overlong encodings
	_core_assert_utf8([0xC0, 0x80], fffd, 1, "2-byte overlong NUL")
	_core_assert_utf8([0xC1, 0xBF], fffd, 1, "2-byte overlong 0x7F")
	_core_assert_utf8([0xE0, 0x80, 0x80], fffd, 1, "3-byte overlong NUL")
	_core_assert_utf8([0xE0, 0x9F, 0xBF], fffd, 1, "3-byte overlong 0x7FF")
	_core_assert_utf8([0xF0, 0x80, 0x80, 0x80], fffd, 1, "4-byte overlong NUL")
	_core_assert_utf8([0xF0, 0x8F, 0xBF, 0xBF], fffd, 1, "4-byte overlong 0xFFFF")

	// UTF-16 surrogates (U+D800..U+DFFF)
	_core_assert_utf8([0xED, 0xA0, 0x80], fffd, 1, "surrogate U+D800")
	_core_assert_utf8([0xED, 0xAF, 0xBF], fffd, 1, "surrogate U+DBFF")
	_core_assert_utf8([0xED, 0xB0, 0x80], fffd, 1, "surrogate U+DC00")
	_core_assert_utf8([0xED, 0xBF, 0xBF], fffd, 1, "surrogate U+DFFF")

	// Above U+10FFFF and invalid lead / stray continuation bytes
	_core_assert_utf8([0xF4, 0x90, 0x80, 0x80], fffd, 1, "above U+10FFFF (U+110000)")
	_core_assert_utf8([0xF5, 0x80, 0x80, 0x80], fffd, 1, "lead 0xF5")
	_core_assert_utf8([0xF7, 0xBF, 0xBF, 0xBF], fffd, 1, "lead 0xF7")
	_core_assert_utf8([0xFF], fffd, 1, "lead 0xFF")
	_core_assert_utf8([0x80], fffd, 1, "stray continuation 0x80")
	_core_assert_utf8([0xBF], fffd, 1, "stray continuation 0xBF")

	// Truncated sequences and out-of-bounds offsets
	_core_assert_utf8([0xC2], fffd, 1, "truncated 2-byte")
	_core_assert_utf8([0xE2, 0x82], fffd, 1, "truncated 3-byte")
	_core_assert_utf8([0xF0, 0x9F, 0x98], fffd, 1, "truncated 4-byte")

	let s_oob = "ab"
	let n_end = 0
	assert(str_utf8_next(s_oob, s_oob.len(), &n_end) == '\0' and n_end == 2, "offset == len")
	let n_neg = 99
	assert(str_utf8_next(s_oob, -1, &n_neg) == '\0' and n_neg == 0, "negative offset")

	// Codepoint iteration over mixed malformed + valid UTF-8
	let mixed = _core_bytes_str([0xC3, 0x41, 0xE2, 0x82, 0xAC])
	let collected: [char] = []
	for(ch in mixed): collected.push(ch)
	assert(
		collected.len() == 3 and collected[0] == fffd and collected[1] == 'A' and collected[2] == '€',
		"for-in recovers after malformed UTF-8",
	)
}

@test("core comparable and math helpers")
bind test_core_comparable_and_math = fn(): {
	assert(10.cmp(20) == -1, "10 < 20")
	assert(20.cmp(10) == 1, "20 > 10")
	assert(10.cmp(10) == 0, "10 == 10")
	assert('a'.cmp('b') == -1, "'a' < 'b'")
	assert("apple".cmp("banana") == -1, "apple < banana")
	assert("zebra".cmp("apple") == 1, "zebra > apple")
	assert("same".cmp("same") == 0, "same == same")

	assert(min[int](10, 20) == 10, "min(10, 20)")
	assert(min[int](30, 25) == 25, "min(30, 25)")
	assert(max[int](10, 20) == 20, "max(10, 20)")
	assert(max[int](30, 25) == 30, "max(30, 25)")
	assert(abs(-42) == 42, "abs(-42)")
	assert(abs(42) == 42, "abs(42)")
	assert(abs(0) == 0, "abs(0)")
	assert(clamp[int](5, 10, 20) == 10, "clamp lower")
	assert(clamp[int](15, 10, 20) == 15, "clamp within")
	assert(clamp[int](25, 10, 20) == 20, "clamp upper")
}

@test("core_sqrt accuracy and IEEE-754 edge cases")
bind test_core_sqrt = fn(): {
	let pos_inf = 1.0e308 * 10.0
	let neg_inf = -pos_inf
	let nan_val = pos_inf - pos_inf

	// Standard positive values
	assert(core_sqrt(1.0) == 1.0, "core_sqrt(1.0)")
	assert(core_sqrt(4.0) == 2.0, "core_sqrt(4.0)")
	assert(core_sqrt(9.0) == 3.0, "core_sqrt(9.0)")
	assert(core_sqrt(0.25) == 0.5, "core_sqrt(0.25)")
	let d2 = core_sqrt(2.0) - 1.4142135623730951
	assert(d2 > -1.0e-12 and d2 < 1.0e-12, "core_sqrt(2.0)")

	// Extreme finite values
	let r_big = core_sqrt(1.0e300)
	let d_big = r_big - 1.0e150
	assert(d_big > -1.0e138 and d_big < 1.0e138, "core_sqrt(1e300)")
	let r_max = core_sqrt(1.7976931348623157e308)
	assert(r_max == r_max and r_max < pos_inf and r_max > 1.3e154, "core_sqrt(DBL_MAX)")
	let r_sub = core_sqrt(5.0e-324)
	assert(r_sub > 0.0 and r_sub < 1.0e-160, "core_sqrt(subnormal)")

	// Signed zero preservation
	let z = 0.0
	let nz = -z
	let r_pz = core_sqrt(z)
	assert(r_pz == 0.0 and 1.0 / r_pz == pos_inf, "core_sqrt(+0.0) preserves +0.0")
	let r_nz = core_sqrt(nz)
	assert(r_nz == 0.0 and 1.0 / r_nz == neg_inf, "core_sqrt(-0.0) preserves -0.0")

	// +Inf
	assert(core_sqrt(pos_inf) == pos_inf, "core_sqrt(+Inf) == +Inf")

	// Negative inputs and NaN return NaN
	let r_n1 = core_sqrt(-1.0)
	assert(not (r_n1 == r_n1), "core_sqrt(-1.0) is NaN")
	let r_nsmall = core_sqrt(-1.0e-300)
	assert(not (r_nsmall == r_nsmall), "core_sqrt(-1e-300) is NaN")
	let r_nbig = core_sqrt(-1.0e300)
	assert(not (r_nbig == r_nbig), "core_sqrt(-1e300) is NaN")
	let r_ninf = core_sqrt(neg_inf)
	assert(not (r_ninf == r_ninf), "core_sqrt(-Inf) is NaN")
	let r_nan = core_sqrt(nan_val)
	assert(not (r_nan == r_nan), "core_sqrt(NaN) is NaN")
}

@test("core float formatting edge cases: inf, nan, -0.0, >= 2^64, large exponents")
bind test_core_float_formatting = fn(): {
	let pos_inf = 1.0e308 * 10.0
	let neg_inf = -pos_inf
	let nan_val = pos_inf - pos_inf

	// Non-finite values across fixed, general/compact, and scientific formatters
	assert(_format_float_fixed(pos_inf, 2) == "inf", "fixed +inf")
	assert(_format_float_fixed(neg_inf, 2) == "-inf", "fixed -inf")
	assert(_format_float_fixed(nan_val, 2) == "nan", "fixed nan")
	assert(format_float_prec(pos_inf, -1) == "inf", "compact +inf")
	assert(format_float_prec(neg_inf, -1) == "-inf", "compact -inf")
	assert(format_float_prec(nan_val, -1) == "nan", "compact nan")
	assert(format_float_prec(pos_inf, 0) == "inf", "prec 0 +inf")
	assert(format_float_prec(neg_inf, 4) == "-inf", "prec 4 -inf")
	assert(format_float_prec(nan_val, 4) == "nan", "prec 4 nan")
	assert(pos_inf.to_exp(2) == "inf", "exp +inf")
	assert(neg_inf.to_exp(2) == "-inf", "exp -inf")
	assert(nan_val.to_exp(2) == "nan", "exp nan")

	// Signed zero (+0.0 and -0.0)
	let z = 0.0
	let nz = -z
	assert(format_float_prec(z, -1) == "0", "compact +0.0")
	assert(format_float_prec(z, 0) == "0", "prec 0 +0.0")
	assert(format_float_prec(z, 2) == "0.00", "prec 2 +0.0")
	assert(z.to_exp(2) == "0.00e+00", "exp +0.0")
	assert(format_float_prec(nz, -1) == "-0", "compact -0.0")
	assert(format_float_prec(nz, 0) == "-0", "prec 0 -0.0")
	assert(format_float_prec(nz, 2) == "-0.00", "prec 2 -0.0")
	assert(_format_float_fixed(nz, 2) == "-0.00", "fixed -0.0")
	assert(nz.to_exp(2) == "-0.00e+00", "exp -0.0")

	// Negative values & rounding boundaries
	assert(format_float_prec(-3.14159, 2) == "-3.14", "neg prec 2")
	assert(format_float_prec(-3.14159, -1) == "-3.14159", "neg compact")
	assert(format_float_prec(-0.0001, -1) == "-0.0001", "neg 1e-4 compact")
	assert(format_float_prec(-0.00001, -1) == "-1e-05", "neg 1e-5 compact")
	assert(format_float_prec(-0.0001, 2) == "-0.00", "small neg fixed rounds to -0.00")
	assert(format_float_prec(999999.5, -1) == "1e+06", "carry into 1e+06")
	assert(format_float_prec(-999999.5, -1) == "-1e+06", "neg carry into -1e+06")
	assert(format_float_prec(100.0, -1) == "100", "compact integer preserves trailing zeros")
	assert(
		_strip_trailing_frac_zeros("100") == "100",
		"strip_trailing_frac_zeros skips integer without dot",
	)
	assert(9.999e20.to_exp(2) == "1.00e+21", "to_exp carry increments exponent")

	// Values near and >= 2^64 (exact fixed & compact formatting)
	assert(format_float_prec(4503599627370495.5, 0) == "4503599627370496", "2^52 - 0.5 fixed 0")
	assert(format_float_prec(4503599627370495.5, 1) == "4503599627370495.5", "2^52 - 0.5 fixed 1")
	assert(
		format_float_prec(18446744073709549568.0, 0) == "18446744073709549568",
		"2^64 - 2048 fixed 0",
	)
	assert(
		format_float_prec(18446744073709549568.0, 2) == "18446744073709549568.00",
		"2^64 - 2048 fixed 2",
	)
	assert(format_float_prec(18446744073709551616.0, 0) == "18446744073709551616", "2^64 fixed 0")
	assert(
		format_float_prec(18446744073709551616.0, 2) == "18446744073709551616.00",
		"2^64 fixed 2",
	)
	assert(format_float_prec(18446744073709551616.0, -1) == "1.84467e+19", "2^64 compact")
	assert(format_float_prec(1.0e20, 2) == "100000000000000000000.00", "1e20 fixed 2")
	assert(format_float_prec(-1.0e20, 2) == "-100000000000000000000.00", "-1e20 fixed 2")
	assert(format_float_prec(1.0e20, -1) == "1e+20", "1e20 compact")
	assert(format_float_prec(-1.0e20, -1) == "-1e+20", "-1e20 compact")

	// Large & tiny exponents (1e308, DBL_MAX, 1e-308, subnormal 5e-324)
	assert(format_float_prec(1.0e308, -1) == "1e+308", "1e308 compact")
	assert(format_float_prec(-1.0e308, -1) == "-1e+308", "-1e308 compact")
	assert(1.0e308.to_exp(2) == "1.00e+308", "1e308 to_exp")
	assert((-1.0e308).to_exp(2) == "-1.00e+308", "-1e308 to_exp")
	let big_fixed = format_float_prec(1.0e308, 0)
	assert(big_fixed.len() == 309 and big_fixed.starts_with("10000000000000000"), "1e308 fixed 0")
	let big_fixed2 = format_float_prec(1.0e308, 2)
	assert(big_fixed2.len() == 312 and big_fixed2.ends_with(".00"), "1e308 fixed 2")
	assert(format_float_prec(-1.7976931348623157e308, -1) == "-1.79769e+308", "-DBL_MAX compact")
	assert(format_float_prec(1.0e-308, -1) == "1e-308", "1e-308 compact")
	assert(format_float_prec(5.0e-324, -1) == "4.94066e-324", "subnormal 5e-324 compact")
	assert(format_float_prec(-5.0e-324, -1) == "-4.94066e-324", "neg subnormal -5e-324 compact")
	assert(5.0e-324.to_exp(2) == "4.94e-324", "subnormal 5e-324 to_exp")
	assert(format_float_prec(-5.0e-324, 2) == "-0.00", "neg subnormal fixed 2")
}

@test_only
bind _core_assert_parse_f64_bits = fn(s: string, expected_bits: u64, label: string): {
	let p? = parse_float(s)
	if(p? as v): {
		let got = _float_bits(v)
		assert(got == expected_bits, label)
	} else: assert(false, label)
}

@test("core parse_float edge cases: inf, nan, -0.0, >= 2^64, extreme exponents")
bind test_core_parse_float = fn(): {
	let pos_inf_bits = u64(0x7FF0000000000000)
	let neg_zero_bits = u64(1) << u64(63)
	let neg_inf_bits = neg_zero_bits | pos_inf_bits

	// Special values: inf, infinity, nan (case-insensitive & signed)
	_core_assert_parse_f64_bits("inf", pos_inf_bits, "parse inf")
	_core_assert_parse_f64_bits("+inf", pos_inf_bits, "parse +inf")
	_core_assert_parse_f64_bits("INF", pos_inf_bits, "parse INF")
	_core_assert_parse_f64_bits("Infinity", pos_inf_bits, "parse Infinity")
	_core_assert_parse_f64_bits("+Infinity", pos_inf_bits, "parse +Infinity")
	assert(parse_float("  +Infinity  ") == none, "surrounding whitespace is rejected")
	_core_assert_parse_f64_bits("-inf", neg_inf_bits, "parse -inf")
	_core_assert_parse_f64_bits("-INF", neg_inf_bits, "parse -INF")
	_core_assert_parse_f64_bits("-Infinity", neg_inf_bits, "parse -Infinity")

	let nan_strs = ["nan", "NaN", "NAN", "+nan", "-nan"]
	for(ns in nan_strs): {
		let p? = parse_float(ns)
		if(p? as v): assert(not (v == v), "parse nan") else: assert(false, "parse nan not none")
	}

	// Signed zero (+0.0 vs -0.0)
	_core_assert_parse_f64_bits("0", u64(0), "parse 0")
	_core_assert_parse_f64_bits("+0", u64(0), "parse +0")
	_core_assert_parse_f64_bits("0.0", u64(0), "parse 0.0")
	_core_assert_parse_f64_bits("0e999999999999", u64(0), "parse 0e999999999999")
	_core_assert_parse_f64_bits("1e-400", u64(0), "parse 1e-400 underflow to +0.0")
	_core_assert_parse_f64_bits("2.4e-324", u64(0), "parse 2.4e-324 rounds to +0.0")
	_core_assert_parse_f64_bits("-0", neg_zero_bits, "parse -0")
	_core_assert_parse_f64_bits("-0.0", neg_zero_bits, "parse -0.0")
	_core_assert_parse_f64_bits("-0e999999999999", neg_zero_bits, "parse -0e999999999999")
	_core_assert_parse_f64_bits("-1e-400", neg_zero_bits, "parse -1e-400 underflow to -0.0")
	_core_assert_parse_f64_bits("-2.4e-324", neg_zero_bits, "parse -2.4e-324 rounds to -0.0")

	// Standard values, dot forms, and exact precision
	_core_assert_parse_f64_bits(".5", _float_bits(0.5), "parse .5")
	_core_assert_parse_f64_bits("5.", _float_bits(5.0), "parse 5.")
	_core_assert_parse_f64_bits("-3.14159", _float_bits(-3.14159), "parse -3.14159")
	assert(parse_float("  -3.14159  ") == none and parse_float("1.5\n") == none, "whitespace is rejected")
	_core_assert_parse_f64_bits("  -3.14159  ".trim(), _float_bits(-3.14159), "trim first to accept whitespace")
	_core_assert_parse_f64_bits("+42.25", _float_bits(42.25), "parse +42.25")
	_core_assert_parse_f64_bits("0.1", _float_bits(0.1), "parse 0.1 exact")
	_core_assert_parse_f64_bits(
		"3.141592653589793",
		_float_bits(3.141592653589793),
		"parse pi exact",
	)

	// Values >= 2^64
	_core_assert_parse_f64_bits(
		"18446744073709551616",
		_float_bits(18446744073709551616.0),
		"parse 2^64",
	)
	_core_assert_parse_f64_bits(
		"-18446744073709551616",
		_float_bits(-18446744073709551616.0),
		"parse -2^64",
	)
	_core_assert_parse_f64_bits("1e20", _float_bits(1.0e20), "parse 1e20")
	_core_assert_parse_f64_bits("1e23", _float_bits(1.0e23), "parse 1e23 exact")

	// Large/tiny exponents, DBL_MAX/DBL_MIN, subnormals, tie-to-even, huge clamped exponents
	_core_assert_parse_f64_bits("1e308", _float_bits(1.0e308), "parse 1e308 exact")
	_core_assert_parse_f64_bits(
		"1.7976931348623157e308",
		_float_bits(1.7976931348623157e308),
		"parse DBL_MAX",
	)
	// Finite numbers too large for a float are out of range, not infinity.
	assert(parse_float("1.7976931348623159e308") == none, "above DBL_MAX is out of range")
	assert(parse_float("1e309") == none and parse_float("1e400") == none, "1e309 and 1e400 are out of range")
	assert(parse_float("1e99999999999999999999") == none, "huge exponent is out of range")
	assert(parse_float("-1e99999999999999999999") == none, "huge negative is out of range")
	_core_assert_parse_f64_bits(
		"2.2250738585072014e-308",
		_float_bits(2.2250738585072014e-308),
		"parse DBL_MIN",
	)
	_core_assert_parse_f64_bits(
		"2.2250738585072011e-308",
		u64(0x000fffffffffffff),
		"parse largest subnormal",
	)
	_core_assert_parse_f64_bits(
		"2.2250738585072012e-308",
		_float_bits(2.2250738585072014e-308),
		"parse subnormal rounding to DBL_MIN",
	)
	_core_assert_parse_f64_bits("5e-324", u64(1), "parse min subnormal")
	_core_assert_parse_f64_bits("-5e-324", neg_zero_bits | u64(1), "parse neg min subnormal")
	_core_assert_parse_f64_bits(
		"2.4703282292062327e-324",
		u64(0),
		"parse subnormal half tie-to-even",
	)
	_core_assert_parse_f64_bits(
		"2.4703282292062328e-324",
		u64(1),
		"parse just above subnormal midpoint",
	)

	// Long digit strings with compensating exponents
	let long_one = "1"
	for(k in 0..400): long_one.push_byte(u8(48))
	long_one.push("e-400")
	_core_assert_parse_f64_bits(long_one, _float_bits(1.0), "parse 401-digit 1e400*e-400")

	let long_frac = "0."
	for(k in 0..400): long_frac.push_byte(u8(48))
	long_frac.push("1e402")
	_core_assert_parse_f64_bits(long_frac, _float_bits(10.0), "parse 400 leading zero frac * e402")

	// Invalid inputs return none
	let bad_cases = [
		"",
		"   ",
		"+",
		"-",
		".",
		"+.",
		"-.",
		"e5",
		".e5",
		"1e",
		"1e+",
		"1e-",
		"1e+a",
		"1e99999999999999999999x",
		"1.2.3",
		"12abc",
		"infin",
		"infinity2",
		"nana",
		"++1",
	]
	for(bc in bad_cases): assert(parse_float(bc) == none, "invalid parse_float input")
}

@test("core Result[T] construction and unwrapping")
bind test_core_result = fn(): {
	let r_ok! = Result[int].value(100)
	assert(r_ok!.is_ok(), "is_ok")
	assert(not r_ok!.is_err(), "not is_err")
	assert((r_ok!.copy() ?? 0) == 100, "?? on a value")
	assert(if(r_ok! as v): v == 100 else: false, "if-as binds the value")
	assert(r_ok!.peek_err() == none, "peek_err on ok")
	let matched_int = match(r_ok!.copy()):
		when value(v): v
		default err(_): -1
	assert(matched_int == 100, "match Result.value")
	assert(r_ok!.unwrap() == 100, "unwrap on ok")

	let r_str! = Result[string].value("hello_" + "world")
	let matched_str = match(own r_str!):
		when value(own s): s
		default err(_): ""
	assert(matched_str == "hello_world", "match Result.value on Resource string")

	let r_err! = Result[int].fail_msg("failure")
	assert(r_err!.is_err(), "is_err")
	assert(not r_err!.is_ok(), "not is_ok")
	assert(r_err!.copy().peek_err() != none, "peek_err on err")
	let r_wrapped! = r_err!.copy().wrap_err("outer context").trace_err()
	assert(r_wrapped!.copy().unwrap_err().summary() == "outer context: failure", "wrap_err summary")
	assert(r_wrapped!.copy().unwrap_err().frames().len() == 2, "wrap_err + trace_err frames")
	assert((r_err! ?? 999) == 999, "?? on a failure")
}

@test("core Error creation, wrapping, sentinels, and trace")
bind test_core_error = fn(): {
	let sentinel_nf = Error.coded("not_found", "resource not found")
	let e0 = Error.coded("not_found", "missing config.toml")
	let e1 = e0.wrap("loading config")
	let e1_snapshot = e1.copy()
	let e2 = e1.trace()
	let e3 = e2.wrap("booting server")

	assert(e3.message() == "booting server", "outermost message")
	assert(e3.summary() == "booting server: loading config: missing config.toml", "summary chain")
	assert(e3.root().message() == "missing config.toml", "root message")
	assert(e3.has_code("not_found"), "has_code walks chain")
	assert(e3.code() == "not_found", "code walks chain")
	assert(e3.is(sentinel_nf), "is matches sentinel code across wrap layers")
	assert(not e3.is(Error.coded("timeout", "timed out")), "is rejects different code")
	assert(e3.is(Error.new("loading config")), "is matches intermediate layer message without code")

	if(e3.cause() as c1): {
		assert(c1.message() == "loading config", "cause returns direct wrapped error")
		assert(c1.frames().len() == 2, "traced layer has 2 frames")
	} else: panic("expected e3.cause() to be present")

	let formatted = e3.to_string()
	assert(formatted.contains("[not_found]"), "to_string includes code")
	assert(
		formatted.contains("booting server: loading config: missing config.toml"),
		"to_string includes summary",
	)
	assert(formatted.contains("trace:"), "to_string includes trace section")
	assert(formatted.contains("core.ll:"), "to_string includes caller file location")

	let e3_copy = e3.copy()
	assert(e3 == e3_copy, "Error equality matches identical copies")
	assert(e3 != e1_snapshot, "Error equality distinguishes different errors")
}

@test("core List[T] operations")
bind test_core_list = fn(): {
	let list = List[int].new()
	assert(list.len() == 0, "initial len 0")
	assert(list.is_empty(), "initial is_empty")

	list.push(10)
	list.push(20)
	list.push(30)
	assert(list.len() == 3, "len after pushes")
	assert(not list.is_empty(), "not is_empty after push")
	assert(list.get(0) == 10, "get(0)")
	assert(list.get(1) == 20, "get(1)")
	assert(list.get(2) == 30, "get(2)")
	assert(list.get(3) == none, "get(3) out of bounds")

	let sum = 0
	let it = list.iter()
	for(true): {
		let next? = it.next()
		if(next? as val): sum += val else: for <-
	}
	assert(sum == 60, "list iteration sum")

	let copy_list = list.copy()
	assert(copy_list.len() == 3, "copy len")
	assert(copy_list.get(1) == 20, "copy get")

	let p? = list.pop()
	assert(p? == 30, "pop last")
	assert(list.len() == 2, "len after pop")

	list.clear()
	assert(list.len() == 0, "len after clear")
	assert(list.is_empty(), "is_empty after clear")

	let neg_cap = List[int].with_capacity(-5)
	assert(neg_cap.len() == 0, "negative with_capacity len is 0")
	assert(neg_cap.cap() == 0, "negative with_capacity clamps cap to 0")
	assert(neg_cap.is_empty(), "negative with_capacity is empty")
	neg_cap.push(42)
	assert(neg_cap.len() == 1 and neg_cap.cap() >= 1, "push after negative with_capacity")
	assert(neg_cap.get(0) == 42, "get after negative with_capacity push")

	let zero_cap = List[int].with_capacity(0)
	assert(zero_cap.len() == 0 and zero_cap.cap() == 0, "zero with_capacity")
	zero_cap.push(7)
	assert(zero_cap.get(0) == 7, "push after zero with_capacity")

	let pos_cap = List[int].with_capacity(4)
	assert(pos_cap.len() == 0 and pos_cap.cap() == 4, "positive with_capacity preserves cap")
}

@test("core Set[T] operations")
bind test_core_set = fn(): {
	let s = Set[string].new()
	assert(s.len() == 0, "set initial len")
	assert(s.is_empty(), "set initial is_empty")

	assert(s.insert("first"), "insert first")
	assert(s.insert("second"), "insert second")
	assert(not s.insert("first"), "insert duplicate returns false")
	assert(s.len() == 2, "set len after insertions")

	assert(s.contains("first"), "contains first")
	assert(s.contains("second"), "contains second")
	assert(not s.contains("third"), "does not contain third")

	let count = 0
	let sit = s.iter()
	for(true): {
		let next? = sit.next()
		if(next? as item): {
			assert(item == "first" or item == "second", "valid item in iter")
			count += 1
		} else: for <-
	}
	assert(count == 2, "iterated count")

	let s_copy = s.copy()
	assert(s_copy.len() == 2, "s_copy len")
	assert(s_copy.contains("first"), "s_copy contains")

	assert(s.remove("first"), "remove first")
	assert(not s.contains("first"), "contains after remove")
	assert(s.len() == 1, "len after remove")
	assert(not s.remove("first"), "remove again returns false")

	s.clear()
	assert(s.len() == 0, "len after clear")
	assert(s.is_empty(), "is_empty after clear")
}

@test("core Map[K, V] operations")
bind test_core_map = fn(): {
	let m = Map[string, int].new()
	assert(m.len() == 0, "map initial len")
	assert(m.is_empty(), "map initial is_empty")

	m.insert("k1", 100)
	m.insert("k2", 200)
	assert(m.len() == 2, "map len after inserts")
	assert(m.contains("k1"), "map contains k1")
	assert(m.contains("k2"), "map contains k2")
	assert(not m.contains("k3"), "map does not contain k3")

	assert(m.get("k1") == 100, "get k1")
	assert(m.get("k2") == 200, "get k2")
	assert(m.get("k3") == none, "get k3 is none")

	// Overwrite existing key
	m.insert("k1", 150)
	assert(m.len() == 2, "len after overwrite")
	assert(m.get("k1") == 150, "updated value")

	let keys = m.keys()
	assert(keys.len() == 2, "keys len")
	let vals = m.values()
	assert(vals.len() == 2, "vals len")

	let sum = 0
	let mit = m.iter()
	for(true): {
		let next? = mit.next()
		if(next? as entry): sum += entry.value else: for <-
	}
	assert(sum == 350, "map iteration sum")

	let m_copy = m.copy()
	assert(m_copy.len() == 2, "copy len")
	assert(m_copy.get("k1") == 150, "copy get")

	let rem? = m.remove("k1")
	assert(rem? == 150, "remove k1 returns removed value")
	assert(not m.contains("k1"), "does not contain k1")
	assert(m.len() == 1, "len after remove")
	let rem2? = m.remove("k1")
	assert(rem2? == none, "remove non-existent returns none")

	m.clear()
	assert(m.len() == 0, "len after clear")
	assert(m.is_empty(), "is_empty after clear")
}

@derive(Equatable, Hashable)
define _CoreColor enum [red, green]
define _CoreId int
define _CoreRes struct [name: string]
bind _CoreRes.drop = fn(&self): {}

@test("core sets and maps are the native Set and Map")
bind test_core_set_map_native = fn(): {
	// `#[T]` and `#[K: V]` are `Set[T]` and `Map[K, V]`.
	let s: Set[int] = #[1, 2, 2, 3]
	assert(s.len() == 3, "duplicates collapse")
	assert(s.to_string() == "#[1, 2, 3]", "set to_string")
	let m: #[string: int] = Map[string, int].new()
	m["a"] = 1
	m["a"] = 2
	assert(m.len() == 1 and (m["a"] ?? 0) == 2, "m[k] = v replaces")
	assert(m.to_string() == "#[a: 2]", "map to_string")

	// Keys without a `hash` method of their own.
	let t = #[+[1, "a"], +[2, "b"], +[1, "a"]]
	assert(t.len() == 2 and t.has(+[2, "b"]), "tuple keys")
	let c = #[_CoreColor.red, _CoreColor.green, _CoreColor.red]
	assert(c.len() == 2 and c.has(_CoreColor.green), "enum keys")
	let d = #[_CoreId(3): "three"]
	assert((d[_CoreId(3)] ?? "") == "three", "distinct keys")

	// Replacing and removing a resource value drops it exactly once (checked by the leak counter).
	let r = #["k": _CoreRes[name: "one".copy()]]
	r["k"] = _CoreRes[name: "two".copy()]
	let gone? = r.remove("k")
	assert(gone?.name == "two", "remove returns the value")

	// Iteration, `when in`, and comprehensions.
	let sum = 0
	for(k, v in #["x": 1, "y": 2]): sum += v + k.len()
	assert(sum == 5, "map iteration")
	let hit = match(2):
		when in #[1, 2]: true
		default: false
	assert(hit, "when in a set")
	let halves = for(i in 0..6) #[int]: i / 2
	assert(halves.len() == 3, "set comprehension")
}

@test("core literals and comprehensions start empty on every loop iteration")
bind test_core_literal_in_loop = fn(): {
	for(r in 0..3): {
		let xs = [r]
		let ys = for(i in 0..2): i + r
		let zs = for(i in 0..2) #[int]: i + r
		let ms = #["r": r]
		assert(xs.len() == 1 and xs[0] == r, "list literal")
		assert(ys.len() == 2 and ys[0] == r, "list comprehension")
		assert(zs.len() == 2 and zs.has(r + 1), "set comprehension")
		assert(ms.len() == 1 and (ms["r"] ?? -1) == r, "map literal")
	}
}

@test("core string concatenation, slicing, and copy")
bind test_core_string_ops = fn(): {
	let a = "foo"
	let b = a + "bar"
	assert(b == "foobar" and b.cap() > 0, "concat owns its buffer")
	assert(("" + "").len() == 0, "empty concat")
	assert(b[1..4] == "oob" and b[..2] + b[4..] == "foar", "slices")
	assert(b[2..2].len() == 0, "empty slice")
	b += "!"
	assert(b == "foobar!", "+=")
	let lit = "lit"
	let c = lit.copy()
	assert(c == "lit" and c.cap() > 0 and lit.cap() == 0, "copy of a literal owns its buffer")
	assert("".copy().cap() == 0, "copying an empty string allocates nothing")
}

define _CoreV2 struct [x: int, y: int]
bind _CoreV2.add = fn(self, other: _CoreV2) _CoreV2: _CoreV2[x: self.x + other.x, y: self.y + other.y]

define _CoreBag struct [items: [string]]
bind _CoreBag.add = fn(self, other: _CoreBag) _CoreBag: {
	let out = self.items.copy()
	for(s in other.items): out.push(s.copy())
	_CoreBag[items: out]
}
bind _CoreBag.add_assign = fn(&self, other: _CoreBag): for(s in other.items): self.items.push(s.copy())

bind _core_sum[T: Addable] = fn(xs: [T], zero: own T) T: {
	let acc = zero
	for(x in xs): acc = acc + x
	acc
}

@test("core + and += call Addable and AddAssignable")
bind test_core_add_operators = fn(): {
	let a = _CoreV2[x: 1, y: 2] + _CoreV2[x: 10, y: 20]
	assert(a.x == 11 and a.y == 22, "struct add")
	let p = _CoreV2[x: 0, y: 0]
	p += _CoreV2[x: 3, y: 4]
	assert(p.x == 3 and p.y == 4, "+= via add")
	let b = _CoreBag[items: ["a"]]
	b += _CoreBag[items: ["b", "c"]]
	let c = b + _CoreBag[items: ["d"]]
	assert(b.items.len() == 3 and c.items.len() == 4, "bag")
	assert(_core_sum([1, 2, 3], 0) == 6, "generic int")
	assert(_core_sum(["x", "y"], "") == "xy", "generic string")
	assert(_core_sum([_CoreV2[x: 1, y: 1], _CoreV2[x: 2, y: 2]], _CoreV2[x: 0, y: 0]).y == 3, "generic struct")
	let s = "a"
	s += "b"
	assert(s + "c" == "abc", "string")
	assert(5.add(6) == 11, "int.add")
}

@derive(Equatable, Comparable, Hashable, Copyable, Printable)
define _CorePt struct [x: int, name: string]

@derive(Equatable, Copyable, Printable)
define _CoreOptPt struct [x: int, tag?: ?string]

@derive(Equatable, Comparable, Hashable, Printable)
define _CoreLevel enum [low, mid = 5, high]

@derive(Equatable, Comparable, Hashable, Copyable, Printable)
define _CoreShape enum [circle: float, label: string, empty]

@derive(Equatable, Copyable, Printable)
define _CoreBox[T] struct [item: T]

@derive(Copyable, Printable)
define _CoreTree enum [leaf: int, node: [_CoreTree]]

@test("core @derive generates eq, cmp, hash, copy, and to_string")
bind test_core_derive = fn(): {
	let a = _CorePt[x: 1, name: "a".copy()]
	let b = a.copy()
	assert(a == b and not (a != b), "struct eq and copy")
	assert(a.hash() == b.hash(), "equal values hash equally")
	assert(_CorePt[x: 1, name: "b"] > a and _CorePt[x: 0, name: "z"] < a, "struct cmp in field order")
	assert(a.to_string() == "_CorePt[x: 1, name: a]", "struct to_string")
	let o = _CoreOptPt[x: 2, tag?: "t"]
	assert(o == o.copy() and "$o" == "_CoreOptPt[x: 2, tag?: t]", "optional fields")
	assert(#[a.copy(), b.copy()].len() == 1, "struct as set element")

	assert(_CoreLevel.mid == _CoreLevel.mid and _CoreLevel.low < _CoreLevel.high, "tag enum eq and cmp")
	assert("${_CoreLevel.high}" == "_CoreLevel.high", "tag enum to_string")
	assert(#[_CoreLevel.low: 1][_CoreLevel.low] == 1, "tag enum as map key")

	let c = _CoreShape.circle(1.5)
	assert(c == _CoreShape.circle(1.5) and c != _CoreShape.circle(2.0) and c != _CoreShape.empty, "tagged eq")
	assert(c < _CoreShape.label("x") and _CoreShape.label("a") < _CoreShape.label("b"), "tagged cmp")
	assert(c.hash() == _CoreShape.circle(1.5).hash(), "tagged hash")
	assert(_CoreShape.label("hi".copy()).copy() == _CoreShape.label("hi"), "tagged copy")
	assert("$c ${_CoreShape.empty}" == "_CoreShape.circle(1.5) _CoreShape.empty", "tagged to_string")

	assert(_CoreBox[int][item: 3] == _CoreBox[int][item: 3], "generic struct eq")
	assert("${_CoreBox[string][item: "s"]}" == "_CoreBox[item: s]", "generic struct to_string")
	let t = _CoreTree.node([_CoreTree.leaf(1), _CoreTree.node([])])
	assert("${t.copy()}" == "_CoreTree.node([_CoreTree.leaf(1), _CoreTree.node([])])", "recursive enum")
}
