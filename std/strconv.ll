// Lowlight Standard Library: std/strconv.ll
// Type conversions between strings, numbers, and booleans with visual optional safety (?T).

extern ll_str_parse_int_radix: (s: ptr[u8], len: int, radix: int, out_val: ptr[int]): bool
extern ll_str_parse_float_strict: (s: ptr[u8], len: int, out_val: ptr[float]): bool
extern ll_str_to_radix: (val: int, radix: int, upper: bool): string
extern ll_fmt_exp_f64: (v: float, prec: int): string
extern ll_fmt_float_prec: (v: float, prec: int): string

// -----------------------------------------------------------------------------
// Parsing Functions (String -> Value)
// -----------------------------------------------------------------------------

bind parse_int_radix = (s: string, radix: int): ?int {
    let out_val = 0
    let ok = unsafe ll_str_parse_int_radix(s.c_str(), s.len(), radix, addr_of(out_val))
    if (ok) out_val else none
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

bind int_to_str = (v: int): string {
    unsafe ll_str_to_radix(v, 10, false)
}

bind int_to_radix = (v: int, radix: int): string {
    unsafe ll_str_to_radix(v, radix, false)
}

bind int_to_radix_upper = (v: int, radix: int): string {
    unsafe ll_str_to_radix(v, radix, true)
}

bind int_to_hex = (v: int, upper: bool): string {
    unsafe ll_str_to_radix(v, 16, upper)
}

bind int_to_bin = (v: int): string {
    unsafe ll_str_to_radix(v, 2, false)
}

bind int_to_oct = (v: int): string {
    unsafe ll_str_to_radix(v, 8, false)
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
