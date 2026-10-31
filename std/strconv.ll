// Lowlight Standard Library: std/strconv.ll
// Type conversions between strings, numbers, and booleans with visual optional safety (?T).
// Implemented predominantly in pure Lowlight for zero GC overhead and native C speed.

extern ll_str_parse_float_strict: (s: ptr[u8], len: int, out_val: ptr[float]): bool
extern ll_fmt_exp_f64: (v: float, prec: int): string
extern ll_fmt_float_prec: (v: float, prec: int): string

// -----------------------------------------------------------------------------
// Parsing Functions (String -> Value)
// -----------------------------------------------------------------------------

bind parse_int_radix = (s: string, radix: int): ?int {
    if (radix < 2 or radix > 36) func <- none
    let trimmed = s.trim()
    let n = trimmed.len()
    if (n == 0) func <- none

    let i = 0
    let is_neg = false
    let b0 = trimmed.byte_at(0)
    if (b0 == u8(45)) { // '-'
        is_neg = true
        i += 1
    } else if (b0 == u8(43)) { // '+'
        i += 1
    }
    if (i >= n) func <- none

    // Optional 0x / 0b / 0o prefix handling
    if (i + 2 <= n and trimmed.byte_at(i) == u8(48)) { // starts with '0'
        let p = trimmed.byte_at(i + 1)
        if (radix == 16 and (p == u8(120) or p == u8(88))) { // 'x' or 'X'
            i += 2
        } else if (radix == 2 and (p == u8(98) or p == u8(66))) { // 'b' or 'B'
            i += 2
        } else if (radix == 8 and (p == u8(111) or p == u8(79))) { // 'o' or 'O'
            i += 2
        }
    }
    if (i >= n) func <- none

    let acc = 0
    for (i < n) {
        let b = trimmed.byte_at(i)
        let digit = -1
        if (b >= u8(48) and b <= u8(57)) { // '0'..'9'
            digit = int(b) - 48
        } else if (b >= u8(97) and b <= u8(122)) { // 'a'..'z'
            digit = int(b) - 97 + 10
        } else if (b >= u8(65) and b <= u8(90)) { // 'A'..'Z'
            digit = int(b) - 65 + 10
        }
        if (digit < 0 or digit >= radix) func <- none
        acc = acc * radix + digit
        i += 1
    }

    if (is_neg) -acc else acc
}

bind parse_int_safe = (s: string): ?int {
    parse_int_radix(s, 10)
}

bind parse_float_safe = (s: string): ?float {
    let out_val = 0.0
    let ok = unsafe ll_str_parse_float_strict(s.c_str(), s.len(), addr_of(out_val))
    if (ok) out_val else none
}

bind parse_bool = (s: string): ?bool {
    let trimmed = s.trim()
    if (trimmed == "true" or trimmed == "1" or trimmed == "yes" or trimmed == "on") {
        true
    } else if (trimmed == "false" or trimmed == "0" or trimmed == "no" or trimmed == "off") {
        false
    } else {
        none
    }
}

// -----------------------------------------------------------------------------
// Formatting Functions (Value -> String)
// -----------------------------------------------------------------------------

bind int_to_radix_with_case = (v: int, radix: int, upper: bool): string {
    if (radix < 2 or radix > 36) func <- ""
    if (v == 0) func <- "0"
    let is_neg = v < 0
    let n = if (is_neg) -v else v
    let chars = if (upper) "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" else "0123456789abcdefghijklmnopqrstuvwxyz"
    let rev = ""
    for (n > 0) {
        let rem = n % radix
        rev.push_byte(chars.byte_at(rem))
        n /= radix
    }
    if (is_neg) rev.push("-")
    let out = ""
    let n_rev = rev.len()
    let i = n_rev - 1
    for (i >= 0) {
        out.push(rev[i..(i + 1)])
        i -= 1
    }
    out
}

bind int_to_str = (v: int): string {
    "$v"
}

bind int_to_radix = (v: int, radix: int): string {
    int_to_radix_with_case(v, radix, false)
}

bind int_to_radix_upper = (v: int, radix: int): string {
    int_to_radix_with_case(v, radix, true)
}

bind int_to_hex = (v: int, upper: bool): string {
    int_to_radix_with_case(v, 16, upper)
}

bind int_to_bin = (v: int): string {
    int_to_radix_with_case(v, 2, false)
}

bind int_to_oct = (v: int): string {
    int_to_radix_with_case(v, 8, false)
}

bind float_to_str = (v: float, precision: int): string {
    if (precision < 0) {
        "$v"
    } else {
        unsafe ll_fmt_float_prec(v, precision)
    }
}

bind float_to_exp = (v: float, precision: int): string {
    unsafe ll_fmt_exp_f64(v, precision)
}

bind bool_to_str = (v: bool): string {
    if (v) "true" else "false"
}

// -----------------------------------------------------------------------------
// String Extension Methods (Uniform Method Call Syntax)
// -----------------------------------------------------------------------------

bind string.to_int = (self): ?int {
    parse_int_radix(self, 10)
}

bind string.to_int_radix = (self, radix: int): ?int {
    parse_int_radix(self, radix)
}

bind string.to_float = (self): ?float {
    parse_float_safe(self)
}

bind string.to_bool = (self): ?bool {
    parse_bool(self)
}

bind string.is_numeric = (self): bool {
    let f? = parse_float_safe(self)
    if (f?) true else false
}

bind string.is_int = (self): bool {
    let n? = parse_int_radix(self, 10)
    if (n?) true else false
}

bind string.is_float = (self): bool {
    let f? = parse_float_safe(self)
    if (f?) true else false
}

bind string.is_hex = (self): bool {
    let h? = parse_int_radix(self, 16)
    if (h?) true else false
}

bind string.is_bin = (self): bool {
    let b? = parse_int_radix(self, 2)
    if (b?) true else false
}

bind string.is_oct = (self): bool {
    let o? = parse_int_radix(self, 8)
    if (o?) true else false
}
