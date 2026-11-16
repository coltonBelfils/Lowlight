// =============================================================================
// Lowlight Core Primitives, Conversions, Math & String Manipulation
// =============================================================================

import "./interfaces.ll"

// -----------------------------------------------------------------------------
// Float & Number Formatting Helpers
// -----------------------------------------------------------------------------

bind _pow10_u64 = (exp: int) u64: {
    let res: u64 = u64(1)
    let i = 0
    for (i < exp): {
        res *= u64(10)
        i += 1
    }
    res
}

bind _strip_trailing_frac_zeros = (s: string) string: {
    let end = s.len()
    for (end > 0 and s.byte_at(end - 1) == u8(48)): {
        end -= 1
    }
    if (end > 0 and s.byte_at(end - 1) == u8(46)): {
        end -= 1
    }
    let out = ""
    let i = 0
    for (i < end): {
        out.push_byte(s.byte_at(i))
        i += 1
    }
    out
}

bind _format_float_fixed = (v: float, prec: int) string: {
    let p = if (prec < 0): 0 else if (prec > 15): 15 else: prec
    let is_neg = v < 0.0
    let abs_v = if (is_neg): -v else: v
    let scale = _pow10_u64(p)
    let ip = u64(abs_v)
    let rem = abs_v - float(ip)
    let frac = u64(rem * float(scale) + 0.5)
    if (frac >= scale): {
        ip += u64(1)
        frac -= scale
    }
    let out = if (is_neg): "-" else: ""
    out.push(ip.to_string())
    if (p > 0): {
        out.push_byte(u8(46)) // '.'
        let f_str = frac.to_string()
        let pad = p - f_str.len()
        for (pad > 0): {
            out.push_byte(u8(48))
            pad -= 1
        }
        out.push(f_str)
    }
    out
}

bind _format_exp_f64 = (v: float, prec: int) string: {
    let bits: u64 = unsafe {
        let vp = addr_of(v)
        let up = ptr_cast[u64](vp)
        *up
    }
    let sign = (bits >> 63) & u64(1)
    let exp = (bits >> 52) & u64(0x7ff)
    let mant = bits & u64(0x000fffffffffffff)
    if (exp == u64(0x7ff)): {
        if (mant != u64(0)): func <- "nan"
        if (sign != u64(0)): func <- "-inf"
        func <- "inf"
    }
    let p = if (prec < 0): 6 else if (prec > 15): 15 else: prec
    let is_neg = v < 0.0
    let abs_v = if (is_neg): -v else: v
    let exp10 = 0
    let norm = abs_v
    if (norm > 0.0): {
        for (norm >= 10.0): {
            norm /= 10.0
            exp10 += 1
        }
        for (norm < 1.0): {
            norm *= 10.0
            exp10 -= 1
        }
        let scale = _pow10_u64(p)
        if (norm + 0.5 / float(scale) >= 10.0): {
            norm /= 10.0
            exp10 += 1
        }
    }
    let out = if (is_neg): "-" else: ""
    out.push(_format_float_fixed(norm, p))
    if (exp10 < 0): {
        out.push("e-")
        let ae = -exp10
        if (ae < 10): out.push_byte(u8(48))
        out.push(ae.to_string())
    } else: {
        out.push("e+")
        if (exp10 < 10): out.push_byte(u8(48))
        out.push(exp10.to_string())
    }
    out
}

bind format_float_prec = (v: float, prec: int) string: {
    let bits: u64 = unsafe {
        let vp = addr_of(v)
        let up = ptr_cast[u64](vp)
        *up
    }
    let sign = (bits >> 63) & u64(1)
    let exp = (bits >> 52) & u64(0x7ff)
    let mant = bits & u64(0x000fffffffffffff)
    if (exp == u64(0x7ff)): {
        if (mant != u64(0)): func <- "nan"
        if (sign != u64(0)): func <- "-inf"
        func <- "inf"
    }
    if (prec >= 0): {
        func <- _format_float_fixed(v, prec)
    }
    if (v == 0.0): func <- "0"
    let is_neg = v < 0.0
    let abs_v = if (is_neg): -v else: v
    let exp10 = 0
    let norm = abs_v
    for (norm >= 10.0): {
        norm /= 10.0
        exp10 += 1
    }
    for (norm < 1.0): {
        norm *= 10.0
        exp10 -= 1
    }
    if (norm + 0.000005 >= 10.0): {
        norm /= 10.0
        exp10 += 1
    }
    if (exp10 < -4 or exp10 >= 6): {
        let m_str = _strip_trailing_frac_zeros(_format_float_fixed(norm, 5))
        let out = if (is_neg): "-" else: ""
        out.push(m_str)
        if (exp10 < 0): {
            out.push("e-")
            let ae = -exp10
            if (ae < 10): out.push_byte(u8(48))
            out.push(ae.to_string())
        } else: {
            out.push("e+")
            if (exp10 < 10): out.push_byte(u8(48))
            out.push(exp10.to_string())
        }
        func <- out
    }
    let frac_prec = 5 - exp10
    if (frac_prec <= 0): {
        func <- _format_float_fixed(v, 0)
    }
    _strip_trailing_frac_zeros(_format_float_fixed(v, frac_prec))
}

bind float.to_exp = (self, precision: int) string: {
    _format_exp_f64(self, precision)
}

// -----------------------------------------------------------------------------
// Low-Level String & Memory Operations
// -----------------------------------------------------------------------------

bind str_from_raw = (data: ptr[u8], len: int) string: {
    if (len <= 0 or unsafe is_null(data)): func <- ""
    let out = ""
    out.push_bytes(data, len)
    out
}

bind raw_strlen = (s: ptr[u8]) int: {
    if (unsafe is_null(s)): func <- 0
    let n = 0
    unsafe {
        for (*ptr_add(s, n) != u8(0)): {
            n += 1
        }
    }
    n
}

bind raw_memcmp = (a: ptr[u8], b: ptr[u8], n: int) i32: {
    let i = 0
    unsafe {
        for (i < n): {
            let ba = *ptr_add(a, i)
            let bb = *ptr_add(b, i)
            if (ba < bb): func <- i32(-1)
            if (ba > bb): func <- i32(1)
            i += 1
        }
    }
    i32(0)
}

bind str_from_cstr = (s: ptr[u8]) string: {
    if (unsafe is_null(s)): func <- ""
    let n = raw_strlen(s)
    str_from_raw(s, n)
}

// -----------------------------------------------------------------------------
// Core Math & File I/O Universe Primitives
// -----------------------------------------------------------------------------

bind core_sqrt = (x: float) float: {
    if (x <= 0.0): func <- 0.0
    let scale = 1.0
    let m = x
    for (m >= 4.0): {
        m *= 0.25
        scale *= 2.0
    }
    for (m < 0.25): {
        m *= 4.0
        scale *= 0.5
    }
    let g = 0.5 * (1.0 + m)
    let i = 0
    for (i < 8): {
        g = 0.5 * (g + m / g)
        i += 1
    }
    g * scale
}

bind core_read_file = (path: string) ?string: {
    unsafe {
        let fp = sys_fopen(path.c_str(), "rb".c_str())
        if (is_null(fp)): func <- none
        if (sys_fseek(fp, 0, i32(2)) != i32(0)): {
            let _ = sys_fclose(fp)
            func <- none
        }
        let sz = sys_ftell(fp)
        if (sz < 0): {
            let _ = sys_fclose(fp)
            func <- none
        }
        let _ = sys_fseek(fp, 0, i32(0))
        if (sz == 0): {
            let _ = sys_fclose(fp)
            func <- ""
        }
        let buf = alloc_raw[u8](sz)
        let rd = sys_fread(buf, 1, sz, fp)
        let _ = sys_fclose(fp)
        let out = str_from_raw(buf, rd)
        free_raw(buf)
        out
    }
}

bind core_write_file = (path: string, content: string) bool: {
    unsafe {
        let fp = sys_fopen(path.c_str(), "wb".c_str())
        if (is_null(fp)): func <- false
        let n = content.len()
        let wr = if (n > 0): sys_fwrite(content.c_str(), 1, n, fp) else: 0
        let rc = sys_fclose(fp)
        wr == n and rc == i32(0)
    }
}

bind core_cli_args = () [string]: {
    let count = unsafe sys_argc()
    let out: [string] = []
    let i = 0
    for (i < count): {
        let raw = unsafe sys_argv(i)
        out.push(str_from_cstr(raw))
        i += 1
    }
    out
}

// -----------------------------------------------------------------------------
// Primitive String Conversions (Printable Implementations)
// -----------------------------------------------------------------------------

// Converts an integer to its decimal string representation in 100% pure Lowlight.
bind int.to_string = (self) string: {
    if (self == 0): func <- "0"
    if (self == -9223372036854775807 - 1): {
        func <- "-9223372036854775808"
    }
    let is_neg = self < 0
    let n = if (is_neg): -self else: self
    let reversed = ""
    for (n > 0): {
        let digit = n % 10
        let b = u8(48 + digit)
        reversed.push_byte(b)
        n /= 10
    }
    let out = if (is_neg): "-" else: ""
    let i = reversed.len() - 1
    for (i >= 0): {
        out.push_byte(reversed.byte_at(i))
        i -= 1
    }
    out
}

// Converts a boolean to "true" or "false".
bind bool.to_string = (self) string: {
    if (self): "true" else: "false"
}

// String itself satisfies Printable by returning a copy of itself.
bind string.to_string = (self) string: {
    self.copy()
}

// Converts a unicode character code point into a single-character string.
bind char.to_string = (self) string: {
    let s = ""
    s.push_char(self)
    s
}

// Converts a 64-bit floating point number to its standard decimal string representation.
bind float.to_string = (self) string: {
    format_float_prec(self, -1)
}

bind f32.to_string = (self) string: {
    format_float_prec(float(self), -1)
}

// Fixed-width integer conversions to decimal strings.
bind u8.to_string = (self) string: {
    int(self).to_string()
}

bind u16.to_string = (self) string: {
    int(self).to_string()
}

bind u32.to_string = (self) string: {
    int(self).to_string()
}

bind u64.to_string = (self) string: {
    if (self == 0): func <- "0"
    let n = self
    let reversed = ""
    for (n > 0): {
        let digit = n % 10
        let b = u8(48 + digit)
        reversed.push_byte(b)
        n /= 10
    }
    let out = ""
    let i = reversed.len() - 1
    for (i >= 0): {
        out.push_byte(reversed.byte_at(i))
        i -= 1
    }
    out
}

bind i8.to_string = (self) string: {
    int(self).to_string()
}

bind i16.to_string = (self) string: {
    int(self).to_string()
}

bind i32.to_string = (self) string: {
    int(self).to_string()
}

// -----------------------------------------------------------------------------
// Equatable & Comparable Implementations on Primitives
// -----------------------------------------------------------------------------

bind bool.eq = (self, other: bool) bool: { self == other }

bind int.eq = (self, other: int) bool: { self == other }
bind int.cmp = (self, other: int) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind char.eq = (self, other: char) bool: { self == other }
bind char.cmp = (self, other: char) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind float.eq = (self, other: float) bool: { self == other }
bind float.cmp = (self, other: float) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind f32.eq = (self, other: f32) bool: { self == other }
bind f32.cmp = (self, other: f32) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind u8.eq = (self, other: u8) bool: { self == other }
bind u8.cmp = (self, other: u8) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind u16.eq = (self, other: u16) bool: { self == other }
bind u16.cmp = (self, other: u16) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind u32.eq = (self, other: u32) bool: { self == other }
bind u32.cmp = (self, other: u32) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind u64.eq = (self, other: u64) bool: { self == other }
bind u64.cmp = (self, other: u64) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind i8.eq = (self, other: i8) bool: { self == other }
bind i8.cmp = (self, other: i8) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind i16.eq = (self, other: i16) bool: { self == other }
bind i16.cmp = (self, other: i16) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind i32.eq = (self, other: i32) bool: { self == other }
bind i32.cmp = (self, other: i32) int: {
    if (self < other): -1
    else if (self > other): 1
    else: 0
}

bind string.eq = (self, other: string) bool: {
    let n = self.len()
    if (n != other.len()): func <- false
    let i = 0
    for (i < n): {
        if (self.byte_at(i) != other.byte_at(i)): func <- false
        i += 1
    }
    true
}

bind string.cmp = (self, other: string) int: {
    let n1 = self.len()
    let n2 = other.len()
    let min_n = if (n1 < n2): n1 else: n2
    let i = 0
    for (i < min_n): {
        let b1 = self.byte_at(i)
        let b2 = other.byte_at(i)
        if (b1 < b2): func <- -1
        if (b1 > b2): func <- 1
        i += 1
    }
    if (n1 < n2): -1
    else if (n1 > n2): 1
    else: 0
}

// -----------------------------------------------------------------------------
// Core Character & String Methods and Parsers
// -----------------------------------------------------------------------------

bind char.is_digit = (self) bool: {
    self >= '0' and self <= '9'
}

bind char.is_alpha = (self) bool: {
    (self >= 'a' and self <= 'z') or (self >= 'A' and self <= 'Z')
}

bind char.is_alnum = (self) bool: {
    self.is_alpha() or self.is_digit()
}

bind char.is_whitespace = (self) bool: {
    self == ' ' or self == '\t' or self == '\n' or self == '\r' or int(self) == 11 or int(self) == 12
}

bind char.to_int = (self) int: {
    int(self)
}

bind string.is_empty = (self) bool: {
    self.len() == 0
}

bind string.get_byte = (self, idx: int) ?u8: {
    if (idx < 0 or idx >= self.len()): func <- none
    self.byte_at(idx)
}

bind string.get = (self, idx: int) ?u8: {
    self.get_byte(idx)
}

bind string.bytes = (self) [u8]: {
    let out: [u8] = []
    let n = self.len()
    let i = 0
    for (i < n): {
        out.push(self.byte_at(i))
        i += 1
    }
    out
}

bind str_utf8_next = (s: string, off: int, next_off: &int) char: {
    let n = s.len()
    if (off >= n): {
        next_off = n
        func <- '\0'
    }
    let b0 = int(s.byte_at(off))
    if (b0 < 128): {
        next_off = off + 1
        func <- char(b0)
    }
    if ((b0 & 0xE0) == 0xC0 and off + 1 < n): {
        let b1 = int(s.byte_at(off + 1)) & 0x3F
        next_off = off + 2
        func <- char(((b0 & 0x1F) << 6) | b1)
    }
    if ((b0 & 0xF0) == 0xE0 and off + 2 < n): {
        let b1 = int(s.byte_at(off + 1)) & 0x3F
        let b2 = int(s.byte_at(off + 2)) & 0x3F
        next_off = off + 3
        func <- char(((b0 & 0x0F) << 12) | (b1 << 6) | b2)
    }
    if ((b0 & 0xF8) == 0xF0 and off + 3 < n): {
        let b1 = int(s.byte_at(off + 1)) & 0x3F
        let b2 = int(s.byte_at(off + 2)) & 0x3F
        let b3 = int(s.byte_at(off + 3)) & 0x3F
        next_off = off + 4
        func <- char(((b0 & 0x07) << 18) | (b1 << 12) | (b2 << 6) | b3)
    }
    next_off = off + 1
    char(0xFFFD)
}

bind string.chars = (self) [char]: {
    let out: [char] = []
    let n = self.len()
    let i = 0
    for (i < n): {
        let next_i = 0
        let ch = str_utf8_next(self, i, &next_i)
        out.push(ch)
        i = next_i
    }
    out
}

bind string.starts_with = (self, prefix: string) bool: {
    let plen = prefix.len()
    if (self.len() < plen): func <- false
    let i = 0
    for (i < plen): {
        if (self.byte_at(i) != prefix.byte_at(i)): func <- false
        i += 1
    }
    true
}

bind string.ends_with = (self, suffix: string) bool: {
    let slen = suffix.len()
    let n = self.len()
    if (n < slen): func <- false
    let off = n - slen
    let i = 0
    for (i < slen): {
        if (self.byte_at(off + i) != suffix.byte_at(i)): func <- false
        i += 1
    }
    true
}

bind string.index_of = (self, sub: string) ?int: {
    let sub_len = sub.len()
    let n = self.len()
    if (sub_len == 0): func <- 0
    if (n < sub_len): func <- none
    let limit = n - sub_len
    let i = 0
    for (i <= limit): {
        let matched = true
        let j = 0
        for (j < sub_len): {
            if (self.byte_at(i + j) != sub.byte_at(j)): {
                matched = false
                for <-
            }
            j += 1
        }
        if (matched): func <- i
        i += 1
    }
    none
}

bind string.contains = (self, sub: string) bool: {
    self.index_of(sub) != none
}

bind string.trim_start = (self) string: {
    let n = self.len()
    let start = 0
    for (start < n): {
        let b = self.byte_at(start)
        let is_ws = b == u8(32) or b == u8(9) or b == u8(10) or b == u8(13) or b == u8(11) or b == u8(12)
        if (not is_ws): {
            for <-
        }
        start += 1
    }
    self[start..n].to_string()
}

bind string.trim_end = (self) string: {
    let end = self.len()
    for (end > 0): {
        let b = self.byte_at(end - 1)
        let is_ws = b == u8(32) or b == u8(9) or b == u8(10) or b == u8(13) or b == u8(11) or b == u8(12)
        if (not is_ws): {
            for <-
        }
        end -= 1
    }
    self[0..end].to_string()
}

bind string.trim = (self) string: {
    self.trim_start().trim_end()
}

bind string.to_upper = (self) string: {
    let out = ""
    let n = self.len()
    let i = 0
    for (i < n): {
        let b = self.byte_at(i)
        if (b >= u8(97) and b <= u8(122)): {
            out.push_byte(b - u8(32))
        } else: {
            out.push_byte(b)
        }
        i += 1
    }
    out
}

bind string.to_lower = (self) string: {
    let out = ""
    let n = self.len()
    let i = 0
    for (i < n): {
        let b = self.byte_at(i)
        if (b >= u8(65) and b <= u8(90)): {
            out.push_byte(b + u8(32))
        } else: {
            out.push_byte(b)
        }
        i += 1
    }
    out
}

bind string.replace = (self, old_s: string, new_s: string) string: {
    if (old_s.len() == 0 or self.len() < old_s.len()): func <- self.copy()
    let out = ""
    let i = 0
    let limit = self.len() - old_s.len()
    for (i <= limit): {
        if (self[i..i + old_s.len()] == old_s): {
            out.push(self[0..i])
            out.push(new_s)
            out.push(self[i + old_s.len()..self.len()])
            func <- out
        }
        i += 1
    }
    self.copy()
}

bind string.replace_all = (self, old_s: string, new_s: string) string: {
    if (old_s.len() == 0 or self.len() < old_s.len()): func <- self.copy()
    let out = ""
    let start = 0
    let i = 0
    let limit = self.len() - old_s.len()
    for (i <= limit): {
        if (self[i..i + old_s.len()] == old_s): {
            out.push(self[start..i])
            out.push(new_s)
            i += old_s.len()
            start = i
        } else: {
            i += 1
        }
    }
    out.push(self[start..self.len()])
    out
}

bind string.split = (self, sep: string) [string]: {
    let out: [string] = []
    if (sep.len() == 0): {
        for (ch in self.chars()): {
            out.push(ch.to_string())
        }
        func <- out
    }
    let start = 0
    let i = 0
    let limit = self.len() - sep.len()
    for (i <= limit): {
        if (self[i..i + sep.len()] == sep): {
            out.push(self[start..i].to_string())
            i += sep.len()
            start = i
        } else: {
            i += 1
        }
    }
    out.push(self[start..self.len()].to_string())
    out
}

bind join_strings = (list: [string], sep: string) string: {
    if (list.len() == 0): func <- ""
    let out = ""
    let i = 0
    for (i < list.len()): {
        if (i > 0): {
            out.push(sep)
        }
        out.push(list[i])
        i += 1
    }
    out
}

bind parse_int_radix = (s: string, radix: int) ?int: {
    if (radix < 2 or radix > 36): func <- none
    let trimmed = s.trim()
    let n = trimmed.len()
    if (n == 0): func <- none

    let i = 0
    let is_neg = false
    let b0 = trimmed.byte_at(0)
    if (b0 == u8(45)): { // '-'
        is_neg = true
        i += 1
    } else if (b0 == u8(43)): { // '+'
        i += 1
    }
    if (i >= n): func <- none

    if (i + 2 <= n and trimmed.byte_at(i) == u8(48)): {
        let p = trimmed.byte_at(i + 1)
        if (radix == 16 and (p == u8(120) or p == u8(88))): {
            i += 2
        } else if (radix == 2 and (p == u8(98) or p == u8(66))): {
            i += 2
        } else if (radix == 8 and (p == u8(111) or p == u8(79))): {
            i += 2
        }
    }
    if (i >= n): func <- none

    let cutoff: u64 = if (is_neg): (u64(9223372036854775807) + u64(1)) else: u64(9223372036854775807)
    let r_u64 = u64(radix)
    let max_div = cutoff / r_u64
    let max_rem = cutoff % r_u64

    let acc: u64 = u64(0)
    for (i < n): {
        let b = trimmed.byte_at(i)
        let digit = -1
        if (b >= u8(48) and b <= u8(57)): {
            digit = int(b) - 48
        } else if (b >= u8(97) and b <= u8(122)): {
            digit = int(b) - 97 + 10
        } else if (b >= u8(65) and b <= u8(90)): {
            digit = int(b) - 65 + 10
        }
        if (digit < 0 or digit >= radix): func <- none
        let d_u64 = u64(digit)
        if (acc > max_div or (acc == max_div and d_u64 > max_rem)): {
            func <- none
        }
        acc = acc * r_u64 + d_u64
        i += 1
    }

    if (is_neg): {
        if (acc == u64(9223372036854775807) + u64(1)): {
            func <- -9223372036854775807 - 1
        }
        func <- -int(acc)
    }
    int(acc)
}

bind parse_int = (s: string) ?int: {
    let trimmed = s.trim()
    if (trimmed.starts_with("0x") or trimmed.starts_with("0X") or trimmed.starts_with("-0x") or trimmed.starts_with("-0X") or trimmed.starts_with("+0x") or trimmed.starts_with("+0X")): {
        func <- parse_int_radix(trimmed, 16)
    }
    if (trimmed.starts_with("0b") or trimmed.starts_with("0B") or trimmed.starts_with("-0b") or trimmed.starts_with("-0B") or trimmed.starts_with("+0b") or trimmed.starts_with("+0B")): {
        func <- parse_int_radix(trimmed, 2)
    }
    if (trimmed.starts_with("0o") or trimmed.starts_with("0O") or trimmed.starts_with("-0o") or trimmed.starts_with("-0O") or trimmed.starts_with("+0o") or trimmed.starts_with("+0O")): {
        func <- parse_int_radix(trimmed, 8)
    }
    parse_int_radix(trimmed, 10)
}

bind parse_float = (s: string) ?float: {
    let trimmed = s.trim()
    let n = trimmed.len()
    if (n == 0): func <- none

    let i = 0
    let is_neg = false
    let b0 = trimmed.byte_at(0)
    if (b0 == u8(45)): { // '-'
        is_neg = true
        i += 1
    } else if (b0 == u8(43)): { // '+'
        i += 1
    }
    if (i >= n): func <- none

    let val = 0.0
    let has_digits = false
    for (i < n): {
        let b = trimmed.byte_at(i)
        if (b >= u8(48) and b <= u8(57)): {
            val = val * 10.0 + float(int(b) - 48)
            has_digits = true
            i += 1
        } else: {
            for <-
        }
    }

    if (i < n and trimmed.byte_at(i) == u8(46)): { // '.'
        i += 1
        let frac_scale = 0.1
        for (i < n): {
            let b = trimmed.byte_at(i)
            if (b >= u8(48) and b <= u8(57)): {
                val += float(int(b) - 48) * frac_scale
                frac_scale *= 0.1
                has_digits = true
                i += 1
            } else: {
                for <-
            }
        }
    }

    if (not has_digits): func <- none

    if (i < n and (trimmed.byte_at(i) == u8(101) or trimmed.byte_at(i) == u8(69))): { // 'e' or 'E'
        i += 1
        if (i >= n): func <- none
        let exp_neg = false
        let eb = trimmed.byte_at(i)
        if (eb == u8(45)): {
            exp_neg = true
            i += 1
        } else if (eb == u8(43)): {
            i += 1
        }
        if (i >= n): func <- none
        let exp_val = 0
        let has_exp_digits = false
        for (i < n): {
            let b = trimmed.byte_at(i)
            if (b >= u8(48) and b <= u8(57)): {
                exp_val = exp_val * 10 + (int(b) - 48)
                has_exp_digits = true
                i += 1
            } else: {
                func <- none
            }
        }
        if (not has_exp_digits): func <- none
        let e = 0
        for (e < exp_val): {
            if (exp_neg): {
                val /= 10.0
            } else: {
                val *= 10.0
            }
            e += 1
        }
    }

    if (i != n): func <- none
    if (is_neg): -val else: val
}

// -----------------------------------------------------------------------------
// Core Utility Primitives
// -----------------------------------------------------------------------------

// Returns the smaller of two comparable values.
bind min[T: Comparable + Copyable] = (a: T, b: T) T: {
    if (a < b): a.copy() else: b.copy()
}

// Returns the larger of two comparable values.
bind max[T: Comparable + Copyable] = (a: T, b: T) T: {
    if (a > b): a.copy() else: b.copy()
}

// Returns the absolute value of a signed integer.
bind abs = (x: int) int: {
    if (x < 0): -x else: x
}

// Clamps a value within the range [low, high].
bind clamp[T: Comparable + Copyable] = (val: T, low: T, high: T) T: {
    if (val < low): low.copy()
    else if (val > high): high.copy()
    else: val.copy()
}
