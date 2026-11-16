// Lowlight Standard Library: std/strconv.ll
// Type conversions between strings, numbers, and booleans with visual optional safety (?T).
// Implemented 100% in pure native Lowlight.

// -----------------------------------------------------------------------------
// Parsing Functions (String -> Value)
// -----------------------------------------------------------------------------

bind parse_int_safe = (s: string) ?int: {
    parse_int_radix(s, 10)
}

bind parse_float_safe = (s: string) ?float: {
    parse_float(s)
}

bind parse_bool = (s: string) ?bool: {
    let trimmed = s.trim()
    if (trimmed == "true" or trimmed == "1" or trimmed == "yes" or trimmed == "on"): {
        true
    } else if (trimmed == "false" or trimmed == "0" or trimmed == "no" or trimmed == "off"): {
        false
    } else: {
        none
    }
}

// -----------------------------------------------------------------------------
// Formatting Methods on Primitive Types (int, float, bool)
// -----------------------------------------------------------------------------

bind _int_to_radix_with_case = (v: int, radix: int, upper: bool) string: {
    if (radix < 2 or radix > 36): func <- ""
    if (v == 0): func <- "0"
    let is_neg = v < 0
    let mag: u64 = if (is_neg): (u64(0) - u64(v)) else: u64(v)
    let r: u64 = u64(radix)
    let chars = if (upper): "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" else: "0123456789abcdefghijklmnopqrstuvwxyz"
    let rev = ""
    for (mag > u64(0)): {
        let rem = int(mag % r)
        rev.push_byte(chars.byte_at(rem))
        mag /= r
    }
    if (is_neg): rev.push("-")
    let out = ""
    let n_rev = rev.len()
    let i = n_rev - 1
    for (i >= 0): {
        out.push(rev[i..(i + 1)])
        i -= 1
    }
    out
}

bind int.to_radix = (self, radix: int) string: {
    _int_to_radix_with_case(self, radix, false)
}

bind int.to_radix_upper = (self, radix: int) string: {
    _int_to_radix_with_case(self, radix, true)
}

bind int.to_hex = (self, upper: bool) string: {
    _int_to_radix_with_case(self, 16, upper)
}

bind int.to_bin = (self) string: {
    _int_to_radix_with_case(self, 2, false)
}

bind int.to_oct = (self) string: {
    _int_to_radix_with_case(self, 8, false)
}

// Standalone function forwarders for backwards-compatibility
bind int_to_str = (v: int) string: {
    "$v"
}

bind int_to_radix = (v: int, radix: int) string: {
    v.to_radix(radix)
}

bind int_to_radix_upper = (v: int, radix: int) string: {
    v.to_radix_upper(radix)
}

bind int_to_hex = (v: int, upper: bool) string: {
    v.to_hex(upper)
}

bind int_to_bin = (v: int) string: {
    v.to_bin()
}

bind int_to_oct = (v: int) string: {
    v.to_oct()
}

bind float_to_str = (v: float, precision: int) string: {
    if (precision < 0): {
        "$v"
    } else: {
        format_float_prec(v, precision)
    }
}

bind float_to_exp = (v: float, precision: int) string: {
    v.to_exp(precision)
}

bind bool_to_str = (v: bool) string: {
    if (v): "true" else: "false"
}

// -----------------------------------------------------------------------------
// String Extension Methods (Uniform Method Call Syntax)
// -----------------------------------------------------------------------------

bind string.to_int = (self) ?int: {
    parse_int_radix(self, 10)
}

bind string.to_int_radix = (self, radix: int) ?int: {
    parse_int_radix(self, radix)
}

bind string.to_float = (self) ?float: {
    parse_float_safe(self)
}

bind string.to_bool = (self) ?bool: {
    parse_bool(self)
}

bind string.is_numeric = (self) bool: {
    let f? = parse_float_safe(self)
    if (f?): true else: false
}

bind string.is_int = (self) bool: {
    let n? = parse_int_radix(self, 10)
    if (n?): true else: false
}

bind string.is_float = (self) bool: {
    let f? = parse_float_safe(self)
    if (f?): true else: false
}

bind string.is_hex = (self) bool: {
    let h? = parse_int_radix(self, 16)
    if (h?): true else: false
}

bind string.is_bin = (self) bool: {
    let b? = parse_int_radix(self, 2)
    if (b?): true else: false
}

bind string.is_oct = (self) bool: {
    let o? = parse_int_radix(self, 8)
    if (o?): true else: false
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("strconv integer parsing (decimal and boundaries)")
bind test_strconv_parse_int = (): {
    let n0? = "0".to_int()
    assert(n0? == 0, "0 to_int")

    let n1? = "42".to_int()
    assert(n1? == 42, "42 to_int")

    let n2? = "-123".to_int()
    assert(n2? == -123, "-123 to_int")

    let n3? = "+77".to_int()
    assert(n3? == 77, "+77 to_int")

    let n4? = "   999 \t".to_int()
    assert(n4? == 999, "trimmed to_int")

    let bad1? = "".to_int()
    assert(bad1? == none, "empty is none")

    let bad2? = "abc".to_int()
    assert(bad2? == none, "alpha is none")

    let bad3? = "12a34".to_int()
    assert(bad3? == none, "mixed is none")

    let max_int? = "9223372036854775807".to_int()
    assert(max_int? == 9223372036854775807, "INT64_MAX to_int")

    let min_int? = "-9223372036854775808".to_int()
    assert(min_int? == -9223372036854775807 - 1, "INT64_MIN to_int")
}

@test("strconv radix parsing (binary, octal, hex, base36)")
bind test_strconv_radix = (): {
    let b1? = "1011".to_int_radix(2)
    assert(b1? == 11, "bin 1011")

    let b2? = "0b1011".to_int_radix(2)
    assert(b2? == 11, "bin 0b1011")

    let o1? = "77".to_int_radix(8)
    assert(o1? == 63, "oct 77")

    let o2? = "0o77".to_int_radix(8)
    assert(o2? == 63, "oct 0o77")

    let h1? = "FF".to_int_radix(16)
    assert(h1? == 255, "hex FF")

    let h2? = "0x1A".to_int_radix(16)
    assert(h2? == 26, "hex 0x1A")

    let h3? = "-0xff".to_int_radix(16)
    assert(h3? == -255, "hex -0xff")

    let z? = "z".to_int_radix(36)
    assert(z? == 35, "base36 z")

    let inv1? = "10".to_int_radix(1)
    assert(inv1? == none, "radix 1 is none")

    let inv2? = "10".to_int_radix(37)
    assert(inv2? == none, "radix 37 is none")
}

@test("strconv integer formatting: to_bin, to_oct, to_hex, to_radix")
bind test_strconv_format_int = (): {
    assert(42.to_bin() == "101010", "42 to_bin")
    assert(63.to_oct() == "77", "63 to_oct")
    assert(255.to_hex(false) == "ff", "255 to_hex lower")
    assert(255.to_hex(true) == "FF", "255 to_hex upper")
    assert((-255).to_hex(true) == "-FF", "-255 to_hex upper")
    assert(35.to_radix(36) == "z", "35 to_radix 36")
    assert(0.to_bin() == "0", "0 to_bin")
    assert(0.to_hex(false) == "0", "0 to_hex")
}

@test("strconv float and boolean parsing/formatting")
bind test_strconv_float_and_bool = (): {
    let f? = "3.1415".to_float()
    assert(f? != none, "parse valid float")

    let bad_f? = "not_a_float".to_float()
    assert(bad_f? == none, "parse invalid float is none")

    assert(float_to_str(3.14159, 2) == "3.14", "float_to_str precision 2")

    let b_true? = "true".to_bool()
    assert(b_true? == true, "parse true")

    let b_yes? = "yes".to_bool()
    assert(b_yes? == true, "parse yes")

    let b_one? = "1".to_bool()
    assert(b_one? == true, "parse 1")

    let b_false? = "false".to_bool()
    assert(b_false? == false, "parse false")

    let b_no? = "no".to_bool()
    assert(b_no? == false, "parse no")

    let b_zero? = "0".to_bool()
    assert(b_zero? == false, "parse 0")

    let b_invalid? = "maybe".to_bool()
    assert(b_invalid? == none, "parse maybe is none")
}

@test("strconv string type check predicates")
bind test_strconv_predicates = (): {
    assert("12345".is_int(), "12345 is_int")
    assert(not "123.45".is_int(), "123.45 is not is_int")

    assert("123.45".is_float(), "123.45 is_float")
    assert(not "abc".is_float(), "abc is not is_float")

    assert("10101".is_bin(), "10101 is_bin")
    assert(not "10201".is_bin(), "10201 is not is_bin")

    assert("1a2f".is_hex(), "1a2f is_hex")
    assert(not "1a2g".is_hex(), "1a2g is not is_hex")

    assert("77".is_oct(), "77 is_oct")
    assert(not "88".is_oct(), "88 is not is_oct")
}

