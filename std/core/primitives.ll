// std/core/primitives — Core conversions, comparisons, string utilities, and parsers.
//
// Provides [`Printable`], [`Equatable`], and [`Comparable`] implementations on
// scalar primitives and [`string`], low-level C-string/raw-buffer helpers, numeric
// parsers ([`parse_int`], [`parse_float`]), and comparison helpers ([`min`], [`max`],
// [`abs`], [`clamp`]).


// -----------------------------------------------------------------------------
// Float & Number Formatting Helpers
// -----------------------------------------------------------------------------

bind _float_bits = (v: float) u64: {
	unsafe {
		let vp = addr_of(v)
		let up = ptr_cast[u64](vp)
		*up
	}
}

bind _pow10_u64 = (exp: int) u64: {
	let res: u64 = u64(1)
	let i = 0
	for(i < exp): {
		res *= u64(10)
		i += 1
	}
	res
}

bind _strip_trailing_frac_zeros = (s: string) string: {
	let has_dot = false
	let k = 0
	let slen = s.len()
	for(k < slen): {
		if(s.byte_at(k) == u8(46)): {
			has_dot = true
			for <-
		}
		k += 1
	}
	if(not has_dot): func <- s.copy()
	let end = slen
	for(end > 0 and s.byte_at(end - 1) == u8(48)): end -= 1
	if(end > 0 and s.byte_at(end - 1) == u8(46)): end -= 1
	let out = ""
	let i = 0
	for(i < end): {
		out.push_byte(s.byte_at(i))
		i += 1
	}
	out
}

bind _append_zero_padded_u64 = (out: &string, val: u64, width: int): {
	let s = val.to_string()
	let pad = width - s.len()
	for(pad > 0): {
		out.push_byte(u8(48))
		pad -= 1
	}
	out.push(s)
}

bind _append_exp_suffix = (out: &string, exp10: int): {
	if(exp10 < 0): {
		out.push("e-")
		let ae = -exp10
		if(ae < 10): out.push_byte(u8(48))
		out.push(ae.to_string())
	} else: {
		out.push("e+")
		if(exp10 < 10): out.push_byte(u8(48))
		out.push(exp10.to_string())
	}
}

// Formats exact integer |v| >= 2^64 (IEEE-754 biased exponent >= 1087, < 0x7ff) via base-10^9 limbs.
bind _format_large_float_int = (mant: u64, exp: u64) string: {
	let base = u64(1000000000)
	let m = (u64(1) << u64(52)) | mant
	let limbs: [u64] = []
	let lo = m % base
	let hi = m / base
	limbs.push(lo)
	if(hi > u64(0)): limbs.push(hi)
	let shift = int(exp) - 1075
	for(shift > 0): {
		let step = if(shift >= 29): 29 else: shift
		let mult = u64(1) << u64(step)
		let carry = u64(0)
		let i = 0
		let n = limbs.len()
		for(i < n): {
			let cur = limbs[i] * mult + carry
			limbs[i] = cur % base
			carry = cur / base
			i += 1
		}
		for(carry > u64(0)): {
			limbs.push(carry % base)
			carry /= base
		}
		shift -= step
	}
	let out = ""
	let idx = limbs.len() - 1
	out.push(limbs[idx].to_string())
	idx -= 1
	for(idx >= 0): {
		_append_zero_padded_u64(&out, limbs[idx], 9)
		idx -= 1
	}
	out
}

// Scales finite positive `abs_v` into `[1.0, 10.0)` using binary powers of 10, writing base-10 exponent to `exp_out`.
bind _norm_f64_exp10 = (abs_v: float, exp_out: &int) float: {
	let norm = abs_v
	let e = 0
	if(norm >= 1.0e256): {
		norm /= 1.0e256
		e += 256
	}
	if(norm >= 1.0e128): {
		norm /= 1.0e128
		e += 128
	}
	if(norm >= 1.0e64): {
		norm /= 1.0e64
		e += 64
	}
	if(norm >= 1.0e32): {
		norm /= 1.0e32
		e += 32
	}
	if(norm >= 1.0e16): {
		norm /= 1.0e16
		e += 16
	}
	if(norm >= 1.0e8): {
		norm /= 1.0e8
		e += 8
	}
	if(norm >= 1.0e4): {
		norm /= 1.0e4
		e += 4
	}
	if(norm >= 1.0e2): {
		norm /= 1.0e2
		e += 2
	}
	if(norm >= 10.0): {
		norm /= 10.0
		e += 1
	}
	if(norm < 1.0e-255): {
		norm *= 1.0e256
		e -= 256
	}
	if(norm < 1.0e-127): {
		norm *= 1.0e128
		e -= 128
	}
	if(norm < 1.0e-63): {
		norm *= 1.0e64
		e -= 64
	}
	if(norm < 1.0e-31): {
		norm *= 1.0e32
		e -= 32
	}
	if(norm < 1.0e-15): {
		norm *= 1.0e16
		e -= 16
	}
	if(norm < 1.0e-7): {
		norm *= 1.0e8
		e -= 8
	}
	if(norm < 1.0e-3): {
		norm *= 1.0e4
		e -= 4
	}
	if(norm < 1.0e-1): {
		norm *= 1.0e2
		e -= 2
	}
	if(norm < 1.0): {
		norm *= 10.0
		e -= 1
	}
	if(norm >= 10.0): {
		norm /= 10.0
		e += 1
	}
	if(norm < 1.0): {
		norm *= 10.0
		e -= 1
	}
	exp_out = e
	norm
}

bind _format_float_fixed = (v: float, prec: int) string: {
	let bits = _float_bits(v)
	let sign = (bits >> 63) & u64(1)
	let exp = (bits >> 52) & u64(0x7ff)
	let mant = bits & u64(0x000fffffffffffff)
	if(exp == u64(0x7ff)): {
		if(mant != u64(0)): func <- "nan"
		if(sign != u64(0)): func <- "-inf"
		func <- "inf"
	}
	let p = if(prec < 0): 0
	else if(prec > 15): 15
	else: prec
	let is_neg = sign != u64(0)
	let abs_v = if(is_neg): -v else: v
	let out = if(is_neg): "-" else: ""
	if(exp >= u64(1075)): {
		// |v| >= 2^52 is an exact integer with zero fractional part.
		if(exp < u64(1087)): out.push(u64(abs_v).to_string())
		else: out.push(_format_large_float_int(mant, exp))
		if(p > 0): {
			out.push_byte(u8(46)) // '.'
			let z = 0
			for(z < p): {
				out.push_byte(u8(48))
				z += 1
			}
		}
		func <- out
	}
	let scale = _pow10_u64(p)
	let ip = u64(abs_v)
	let rem = abs_v - float(ip)
	let frac = u64(rem * float(scale) + 0.5)
	if(frac >= scale): {
		ip += u64(1)
		frac -= scale
	}
	out.push(ip.to_string())
	if(p > 0): {
		out.push_byte(u8(46)) // '.'
		_append_zero_padded_u64(&out, frac, p)
	}
	out
}

bind _format_exp_f64 = (v: float, prec: int) string: {
	let bits = _float_bits(v)
	let sign = (bits >> 63) & u64(1)
	let exp = (bits >> 52) & u64(0x7ff)
	let mant = bits & u64(0x000fffffffffffff)
	if(exp == u64(0x7ff)): {
		if(mant != u64(0)): func <- "nan"
		if(sign != u64(0)): func <- "-inf"
		func <- "inf"
	}
	let p = if(prec < 0): 6
	else if(prec > 15): 15
	else: prec
	let is_neg = sign != u64(0)
	let abs_v = if(is_neg): -v else: v
	let out = if(is_neg): "-" else: ""
	if(abs_v == 0.0): {
		out.push_byte(u8(48))
		if(p > 0): {
			out.push_byte(u8(46))
			let z = 0
			for(z < p): {
				out.push_byte(u8(48))
				z += 1
			}
		}
		out.push("e+00")
		func <- out
	}
	let exp10 = 0
	let norm = _norm_f64_exp10(abs_v, &exp10)
	let scale = _pow10_u64(p)
	let ip = u64(norm)
	let rem = norm - float(ip)
	let frac = u64(rem * float(scale) + 0.5)
	if(frac >= scale): {
		ip += u64(1)
		frac -= scale
	}
	if(ip >= u64(10)): {
		ip = u64(1)
		frac = u64(0)
		exp10 += 1
	}
	out.push(ip.to_string())
	if(p > 0): {
		out.push_byte(u8(46))
		_append_zero_padded_u64(&out, frac, p)
	}
	_append_exp_suffix(&out, exp10)
	out
}

// Formats finite, zero, `inf`, `-inf`, and `nan` [`float`] values using fixed `prec`
// decimals when `prec >= 0`, or compact precision-6/scientific notation when `prec < 0`.
bind format_float_prec = (v: float, prec: int) string: {
	let bits = _float_bits(v)
	let sign = (bits >> 63) & u64(1)
	let exp = (bits >> 52) & u64(0x7ff)
	let mant = bits & u64(0x000fffffffffffff)
	if(exp == u64(0x7ff)): {
		if(mant != u64(0)): func <- "nan"
		if(sign != u64(0)): func <- "-inf"
		func <- "inf"
	}
	if(prec >= 0): func <- _format_float_fixed(v, prec)
	let is_neg = sign != u64(0)
	if(v == 0.0):
		if(is_neg): func <- "-0" else: func <- "0"
	let abs_v = if(is_neg): -v else: v
	let exp10 = 0
	let norm = _norm_f64_exp10(abs_v, &exp10)
	let scale = u64(100000)
	let ip = u64(norm)
	let rem = norm - float(ip)
	let frac = u64(rem * float(scale) + 0.5)
	if(frac >= scale): {
		ip += u64(1)
		frac -= scale
	}
	if(ip >= u64(10)): {
		ip = u64(1)
		frac = u64(0)
		exp10 += 1
	}
	if(exp10 < -4 or exp10 >= 6): {
		let m_raw = ""
		m_raw.push(ip.to_string())
		if(frac > u64(0)): {
			m_raw.push_byte(u8(46))
			_append_zero_padded_u64(&m_raw, frac, 5)
		}
		let m_str = _strip_trailing_frac_zeros(m_raw)
		let out = if(is_neg): "-" else: ""
		out.push(m_str)
		_append_exp_suffix(&out, exp10)
		func <- out
	}
	let frac_prec = 5 - exp10
	if(frac_prec <= 0): func <- _format_float_fixed(v, 0)
	_strip_trailing_frac_zeros(_format_float_fixed(v, frac_prec))
}

// Formats this [`float`] in scientific exponential notation (`e+NN`/`e-NN`) with `precision` fractional digits.
bind float.to_exp = (self, precision: int) string: _format_exp_f64(self, precision)

// -----------------------------------------------------------------------------
// Low-Level String & Memory Operations
// -----------------------------------------------------------------------------

// Copies `len` bytes from raw pointer `data` into a new owned [`string`], returning `""` if null or `len <= 0`.
bind str_from_raw = (data: ptr[u8], len: int) string: {
	if(len <= 0 or unsafe is_null(data)): func <- ""
	let out = ""
	out.push_bytes(data, len)
	out
}

// Computes the byte length of null-terminated C string `s`, or `0` if `s` is null.
bind raw_strlen = (s: ptr[u8]) int: {
	if(unsafe is_null(s)): func <- 0
	let n = 0
	unsafe {
		for(*ptr_add(s, n) != u8(0)): n += 1
	}
	n
}

// Lexicographically compares the first `n` bytes at `a` and `b`, returning `-1`, `0`, or `1`.
bind raw_memcmp = (a: ptr[u8], b: ptr[u8], n: int) i32: {
	let i = 0
	unsafe {
		for(i < n): {
			let ba = *ptr_add(a, i)
			let bb = *ptr_add(b, i)
			if(ba < bb): func <- i32(-1)
			if(ba > bb): func <- i32(1)
			i += 1
		}
	}
	i32(0)
}

// Converts null-terminated C string pointer `s` into a newly owned [`string`] (empty if null).
bind str_from_cstr = (s: ptr[u8]) string: {
	if(unsafe is_null(s)): func <- ""
	let n = raw_strlen(s)
	str_from_raw(s, n)
}

// -----------------------------------------------------------------------------
// Core Math & File I/O Universe Primitives
// -----------------------------------------------------------------------------

// Computes the IEEE-754 square root of `x` (`NaN` for `x < 0.0` or `NaN`, `+Inf` for `+Inf`, preserving `±0.0`).
bind core_sqrt = (x: float) float: {
	let inf = 1.0e308 * 10.0
	if(not (x == x)): func <- x
	if(x < 0.0): func <- inf - inf
	if(x == 0.0 or x == inf): func <- x
	let scale = 1.0
	let m = x
	for(m >= 4.0): {
		m *= 0.25
		scale *= 2.0
	}
	for(m < 0.25): {
		m *= 4.0
		scale *= 0.5
	}
	let g = 0.5 * (1.0 + m)
	let i = 0
	for(i < 8): {
		g = 0.5 * (g + m / g)
		i += 1
	}
	g * scale
}

// Reads the entire contents of file `path` as a [`string`], or returns `none` on I/O failure.
bind core_read_file = (path: string) ?string:
	unsafe {
		let fp = sys_fopen(path.c_str(), "rb".c_str())
		if(is_null(fp)): func <- none
		if(sys_fseek(fp, 0, i32(2)) != i32(0)): {
			let _ = sys_fclose(fp)
			func <- none
		}
		let sz = sys_ftell(fp)
		if(sz < 0): {
			let _ = sys_fclose(fp)
			func <- none
		}
		let _ = sys_fseek(fp, 0, i32(0))
		if(sz == 0): {
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

// Writes `content` to `path` (truncating/creating the file) and returns `true` on success.
bind core_write_file = (path: string, content: string) bool:
	unsafe {
		let fp = sys_fopen(path.c_str(), "wb".c_str())
		if(is_null(fp)): func <- false
		let n = content.len()
		let wr = if(n > 0): sys_fwrite(content.c_str(), 1, n, fp) else: 0
		let rc = sys_fclose(fp)
		wr == n and rc == i32(0)
	}

// Returns the process command-line arguments (`argv`) as a list of owned strings.
bind core_cli_args = () [string]: {
	let count = unsafe sys_argc()
	let out: [string] = []
	let i = 0
	for(i < count): {
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
	if(self == 0): func <- "0"
	if(self == -9223372036854775807 - 1): func <- "-9223372036854775808"
	let is_neg = self < 0
	let n = if(is_neg): -self else: self
	let reversed = ""
	for(n > 0): {
		let digit = n % 10
		let b = u8(48 + digit)
		reversed.push_byte(b)
		n /= 10
	}
	let out = if(is_neg): "-" else: ""
	let i = reversed.len() - 1
	for(i >= 0): {
		out.push_byte(reversed.byte_at(i))
		i -= 1
	}
	out
}

// Converts a boolean to "true" or "false".
bind bool.to_string = (self) string:
	if(self): "true" else: "false"

// String itself satisfies Printable by returning a copy of itself.
bind string.to_string = (self) string: self.copy()

// Converts a unicode character code point into a single-character string.
bind char.to_string = (self) string: {
	let s = ""
	s.push_char(self)
	s
}

// Converts a 64-bit floating point number to its standard decimal string representation.
bind float.to_string = (self) string: format_float_prec(self, -1)

// Converts `self` to its decimal string representation.
bind f32.to_string = (self) string: format_float_prec(float(self), -1)

// -----------------------------------------------------------------------------
// Fixed-Width Integer String Conversions
// -----------------------------------------------------------------------------

// Converts this unsigned 8-bit integer to its decimal string representation.
bind u8.to_string = (self) string: int(self).to_string()

// Converts this unsigned 16-bit integer to its decimal string representation.
bind u16.to_string = (self) string: int(self).to_string()

// Converts this unsigned 32-bit integer to its decimal string representation.
bind u32.to_string = (self) string: int(self).to_string()

// Converts this unsigned 64-bit integer to its decimal string representation.
bind u64.to_string = (self) string: {
	if(self == 0): func <- "0"
	let n = self
	let reversed = ""
	for(n > 0): {
		let digit = n % 10
		let b = u8(48 + digit)
		reversed.push_byte(b)
		n /= 10
	}
	let out = ""
	let i = reversed.len() - 1
	for(i >= 0): {
		out.push_byte(reversed.byte_at(i))
		i -= 1
	}
	out
}

// Converts this signed 8-bit integer to its decimal string representation.
bind i8.to_string = (self) string: int(self).to_string()

// Converts this signed 16-bit integer to its decimal string representation.
bind i16.to_string = (self) string: int(self).to_string()

// Converts this signed 32-bit integer to its decimal string representation.
bind i32.to_string = (self) string: int(self).to_string()

// -----------------------------------------------------------------------------
// Equatable & Comparable Implementations on Primitives
// -----------------------------------------------------------------------------

// Reports structural equality between two booleans (`self == other`).
bind bool.eq = (self, other: bool) bool: self == other

// Reports equality between two [`int`] values (`self == other`).
bind int.eq = (self, other: int) bool: self == other

// Compares two [`int`] values, returning `-1`, `0`, or `1`.
bind int.cmp = (self, other: int) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`char`] values (`self == other`).
bind char.eq = (self, other: char) bool: self == other

// Compares two Unicode scalar [`char`] codepoints, returning `-1`, `0`, or `1`.
bind char.cmp = (self, other: char) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`float`] values (`self == other`).
bind float.eq = (self, other: float) bool: self == other

// Compares two [`float`] values, returning `-1`, `0`, or `1`.
bind float.cmp = (self, other: float) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`f32`] values (`self == other`).
bind f32.eq = (self, other: f32) bool: self == other

// Compares two [`f32`] values, returning `-1`, `0`, or `1`.
bind f32.cmp = (self, other: f32) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`u8`] values.
bind u8.eq = (self, other: u8) bool: self == other

// Compares two [`u8`] values, returning `-1`, `0`, or `1`.
bind u8.cmp = (self, other: u8) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`u16`] values.
bind u16.eq = (self, other: u16) bool: self == other

// Compares two [`u16`] values, returning `-1`, `0`, or `1`.
bind u16.cmp = (self, other: u16) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`u32`] values.
bind u32.eq = (self, other: u32) bool: self == other

// Compares two [`u32`] values, returning `-1`, `0`, or `1`.
bind u32.cmp = (self, other: u32) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`u64`] values.
bind u64.eq = (self, other: u64) bool: self == other

// Compares two [`u64`] values, returning `-1`, `0`, or `1`.
bind u64.cmp = (self, other: u64) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`i8`] values.
bind i8.eq = (self, other: i8) bool: self == other

// Compares two [`i8`] values, returning `-1`, `0`, or `1`.
bind i8.cmp = (self, other: i8) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`i16`] values.
bind i16.eq = (self, other: i16) bool: self == other

// Compares two [`i16`] values, returning `-1`, `0`, or `1`.
bind i16.cmp = (self, other: i16) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports equality between two [`i32`] values.
bind i32.eq = (self, other: i32) bool: self == other

// Compares two [`i32`] values, returning `-1`, `0`, or `1`.
bind i32.cmp = (self, other: i32) int:
	if(self < other): -1
	else if(self > other): 1
	else: 0

// Reports byte-for-byte equality between `self` and `other`.
bind string.eq = (self, other: string) bool: {
	let n = self.len()
	if(n != other.len()): func <- false
	let i = 0
	for(i < n): {
		if(self.byte_at(i) != other.byte_at(i)): func <- false
		i += 1
	}
	true
}

// Performs unsigned byte lexicographical comparison against `other`, returning `-1`, `0`, or `1`.
bind string.cmp = (self, other: string) int: {
	let n1 = self.len()
	let n2 = other.len()
	let min_n = if(n1 < n2): n1 else: n2
	let i = 0
	for(i < min_n): {
		let b1 = self.byte_at(i)
		let b2 = other.byte_at(i)
		if(b1 < b2): func <- -1
		if(b1 > b2): func <- 1
		i += 1
	}
	if(n1 < n2): -1
	else if(n1 > n2): 1
	else: 0
}

// -----------------------------------------------------------------------------
// Core Character & String Methods and Parsers
// -----------------------------------------------------------------------------

// Reports whether the character is an ASCII decimal digit (`'0'..'9'`).
bind char.is_digit = (self) bool: self >= '0' and self <= '9'

// Reports whether the character is an ASCII alphabetic letter (`'a'..'z'` or `'A'..'Z'`).
bind char.is_alpha = (self) bool: (self >= 'a' and self <= 'z') or (self >= 'A' and self <= 'Z')

// Reports whether the character is an ASCII letter or decimal digit.
bind char.is_alnum = (self) bool: self.is_alpha() or self.is_digit()

// Reports whether the character is ASCII whitespace (space, `\t`, `\n`, `\r`, vertical tab, or form feed).
bind char.is_whitespace = (self) bool:
	self == ' ' or self == '\t' or self == '\n' or self == '\r' or int(self) == 11 or int(self) == 12

// Returns the Unicode scalar codepoint of `self` as an [`int`].
bind char.to_int = (self) int: int(self)

// Reports whether the string contains zero bytes.
bind string.is_empty = (self) bool: self.len() == 0

// Returns the byte at zero-based offset `idx`, or `none` if out of bounds.
bind string.get_byte = (self, idx: int) ?u8: {
	if(idx < 0 or idx >= self.len()): func <- none
	self.byte_at(idx)
}

// Returns the byte at zero-based offset `idx`, or `none` if out of bounds (satisfies [`Indexable`]).
bind string.get = (self, idx: int) ?u8: self.get_byte(idx)

// Copies all UTF-8 bytes of the string into a new [`List`] of [`u8`].
bind string.bytes = (self) [u8]: {
	let out: [u8] = []
	let n = self.len()
	let i = 0
	for(i < n): {
		out.push(self.byte_at(i))
		i += 1
	}
	out
}

// Decodes one UTF-8 scalar codepoint starting at byte offset `off` in `s`, writing the next offset to `next_off`.
//
// Returns `'\0'` at end of string (or when `off < 0`), or replacement character `U+FFFD` (advancing by 1 byte) on malformed UTF-8 sequences (invalid continuation bytes, overlong encodings, UTF-16 surrogates, or codepoints above `U+10FFFF`).
bind str_utf8_next = (s: string, off: int, next_off: &int) char: {
	let n = s.len()
	if(off < 0): {
		next_off = 0
		func <- '\0'
	}
	if(off >= n): {
		next_off = n
		func <- '\0'
	}
	let b0 = int(s.byte_at(off))
	if(b0 < 128): {
		next_off = off + 1
		func <- char(b0)
	}
	if(b0 >= 0xC2 and b0 <= 0xDF and off + 1 < n): {
		let b1 = int(s.byte_at(off + 1))
		if((b1 & 0xC0) == 0x80): {
			next_off = off + 2
			func <- char(((b0 & 0x1F) << 6) | (b1 & 0x3F))
		}
	}
	if(b0 >= 0xE0 and b0 <= 0xEF and off + 2 < n): {
		let b1 = int(s.byte_at(off + 1))
		let b2 = int(s.byte_at(off + 2))
		if((b1 & 0xC0) == 0x80 and (b2 & 0xC0) == 0x80): {
			let cp = ((b0 & 0x0F) << 12) | ((b1 & 0x3F) << 6) | (b2 & 0x3F)
			if(cp >= 0x0800 and (cp < 0xD800 or cp > 0xDFFF)): {
				next_off = off + 3
				func <- char(cp)
			}
		}
	}
	if(b0 >= 0xF0 and b0 <= 0xF4 and off + 3 < n): {
		let b1 = int(s.byte_at(off + 1))
		let b2 = int(s.byte_at(off + 2))
		let b3 = int(s.byte_at(off + 3))
		if((b1 & 0xC0) == 0x80 and (b2 & 0xC0) == 0x80 and (b3 & 0xC0) == 0x80): {
			let cp = ((b0 & 0x07) << 18) | ((b1 & 0x3F) << 12) | ((b2 & 0x3F) << 6) | (b3 & 0x3F)
			if(cp >= 0x10000 and cp <= 0x10FFFF): {
				next_off = off + 4
				func <- char(cp)
			}
		}
	}
	next_off = off + 1
	char(0xFFFD)
}

// Decodes all UTF-8 scalar codepoints in the string into a new [`List`] of [`char`].
bind string.chars = (self) [char]: {
	let out: [char] = []
	let n = self.len()
	let i = 0
	for(i < n): {
		let next_i = 0
		let ch = str_utf8_next(self, i, &next_i)
		out.push(ch)
		i = next_i
	}
	out
}

// Reports whether the string begins with byte sequence `prefix`.
bind string.starts_with = (self, prefix: string) bool: {
	let plen = prefix.len()
	if(self.len() < plen): func <- false
	let i = 0
	for(i < plen): {
		if(self.byte_at(i) != prefix.byte_at(i)): func <- false
		i += 1
	}
	true
}

// Reports whether the string ends with byte sequence `suffix`.
bind string.ends_with = (self, suffix: string) bool: {
	let slen = suffix.len()
	let n = self.len()
	if(n < slen): func <- false
	let off = n - slen
	let i = 0
	for(i < slen): {
		if(self.byte_at(off + i) != suffix.byte_at(i)): func <- false
		i += 1
	}
	true
}

// Returns the zero-based byte offset of the first occurrence of `sub`, or `none` if absent.
bind string.index_of = (self, sub: string) ?int: {
	let sub_len = sub.len()
	let n = self.len()
	if(sub_len == 0): func <- 0
	if(n < sub_len): func <- none
	let limit = n - sub_len
	let i = 0
	for(i <= limit): {
		let matched = true
		let j = 0
		for(j < sub_len): {
			if(self.byte_at(i + j) != sub.byte_at(j)): {
				matched = false
				for <-
			}
			j += 1
		}
		if(matched): func <- i
		i += 1
	}
	none
}

// Reports whether `sub` appears anywhere within this string.
bind string.contains = (self, sub: string) bool: self.index_of(sub) != none

// Returns a copy of the string with leading ASCII whitespace removed.
bind string.trim_start = (self) string: {
	let n = self.len()
	let start = 0
	for(start < n): {
		let b = self.byte_at(start)
		let is_ws = b == u8(32) or b == u8(9) or b == u8(10) or b == u8(13) or b == u8(11) or b == u8(
			12,
		)
		if(not is_ws): for <-
		start += 1
	}
	self[start..n].to_string()
}

// Returns a copy of the string with trailing ASCII whitespace removed.
bind string.trim_end = (self) string: {
	let end = self.len()
	for(end > 0): {
		let b = self.byte_at(end - 1)
		let is_ws = b == u8(32) or b == u8(9) or b == u8(10) or b == u8(13) or b == u8(11) or b == u8(
			12,
		)
		if(not is_ws): for <-
		end -= 1
	}
	self[0..end].to_string()
}

// Returns a copy of the string with both leading and trailing ASCII whitespace removed.
bind string.trim = (self) string: self.trim_start().trim_end()

// Returns a copy of the string with ASCII lowercase bytes `'a'..'z'` converted to uppercase `'A'..'Z'`.
bind string.to_upper = (self) string: {
	let out = ""
	let n = self.len()
	let i = 0
	for(i < n): {
		let b = self.byte_at(i)
		if(b >= u8(97) and b <= u8(122)): out.push_byte(b - u8(32)) else: out.push_byte(b)
		i += 1
	}
	out
}

// Returns a copy of the string with ASCII uppercase bytes `'A'..'Z'` converted to lowercase `'a'..'z'`.
bind string.to_lower = (self) string: {
	let out = ""
	let n = self.len()
	let i = 0
	for(i < n): {
		let b = self.byte_at(i)
		if(b >= u8(65) and b <= u8(90)): out.push_byte(b + u8(32)) else: out.push_byte(b)
		i += 1
	}
	out
}

// Returns a new string with the first occurrence of `old_s` replaced by `new_s`.
bind string.replace = (self, old_s: string, new_s: string) string: {
	if(old_s.len() == 0 or self.len() < old_s.len()): func <- self.copy()
	let out = ""
	let i = 0
	let limit = self.len() - old_s.len()
	for(i <= limit): {
		if(self[i..i + old_s.len()] == old_s): {
			out.push(self[0..i])
			out.push(new_s)
			out.push(self[i + old_s.len()..self.len()])
			func <- out
		}
		i += 1
	}
	self.copy()
}

// Returns a new string with every non-overlapping occurrence of `old_s` replaced by `new_s`.
bind string.replace_all = (self, old_s: string, new_s: string) string: {
	if(old_s.len() == 0 or self.len() < old_s.len()): func <- self.copy()
	let out = ""
	let start = 0
	let i = 0
	let limit = self.len() - old_s.len()
	for(i <= limit):
		if(self[i..i + old_s.len()] == old_s): {
			out.push(self[start..i])
			out.push(new_s)
			i += old_s.len()
			start = i
		} else: i += 1
	out.push(self[start..self.len()])
	out
}

// Splits the string on delimiter `sep`, or splits into individual UTF-8 character strings when `sep` is empty.
bind string.split = (self, sep: string) [string]: {
	let out: [string] = []
	if(sep.len() == 0): {
		for(ch in self.chars()): out.push(ch.to_string())
		func <- out
	}
	let start = 0
	let i = 0
	let limit = self.len() - sep.len()
	for(i <= limit):
		if(self[i..i + sep.len()] == sep): {
			out.push(self[start..i].to_string())
			i += sep.len()
			start = i
		} else: i += 1
	out.push(self[start..self.len()].to_string())
	out
}

// Concatenates all elements of `list` with `sep` inserted between adjacent elements.
bind join_strings = (list: [string], sep: string) string: {
	if(list.len() == 0): func <- ""
	let out = ""
	let i = 0
	for(i < list.len()): {
		if(i > 0): out.push(sep)
		out.push(list[i])
		i += 1
	}
	out
}

// Parses a signed 64-bit integer from `s` in base `radix` (`2..=36`), or returns `none` if invalid or out of range.
//
// Strips surrounding whitespace and accepts optional `+`/`-` signs plus base prefixes (`0x`, `0b`, `0o`) matching `radix`.
bind parse_int_radix = (s: string, radix: int) ?int: {
	if(radix < 2 or radix > 36): func <- none
	let trimmed = s.trim()
	let n = trimmed.len()
	if(n == 0): func <- none

	let i = 0
	let is_neg = false
	let b0 = trimmed.byte_at(0)
	if(b0 == u8(45)): { // '-'
		is_neg = true
		i += 1
	} else if(b0 == u8(43)): { // '+'
		i += 1
	}
	if(i >= n): func <- none

	if(i + 2 <= n and trimmed.byte_at(i) == u8(48)): {
		let p = trimmed.byte_at(i + 1)
		if(radix == 16 and (p == u8(120) or p == u8(88))): i += 2
		else if(radix == 2 and (p == u8(98) or p == u8(66))): i += 2
		else if(radix == 8 and (p == u8(111) or p == u8(79))): i += 2
	}
	if(i >= n): func <- none

	let cutoff: u64 = if(is_neg): u64(9223372036854775807) + u64(1) else: u64(9223372036854775807)
	let r_u64 = u64(radix)
	let max_div = cutoff / r_u64
	let max_rem = cutoff % r_u64

	let acc: u64 = u64(0)
	for(i < n): {
		let b = trimmed.byte_at(i)
		let digit = -1
		if(b >= u8(48) and b <= u8(57)): digit = int(b) - 48
		else if(b >= u8(97) and b <= u8(122)): digit = int(b) - 97 + 10
		else if(b >= u8(65) and b <= u8(90)): digit = int(b) - 65 + 10
		if(digit < 0 or digit >= radix): func <- none
		let d_u64 = u64(digit)
		if(acc > max_div or (acc == max_div and d_u64 > max_rem)): func <- none
		acc = acc * r_u64 + d_u64
		i += 1
	}

	if(is_neg): {
		if(acc == u64(9223372036854775807) + u64(1)): func <- -9223372036854775807 - 1
		func <- -int(acc)
	}
	int(acc)
}

// Parses a signed 64-bit integer from `s`, automatically recognizing `0x` (hex), `0b` (binary), `0o` (octal), or decimal.
bind parse_int = (s: string) ?int: {
	let trimmed = s.trim()
	if(trimmed.starts_with("0x") or trimmed.starts_with("0X") or trimmed.starts_with("-0x") or trimmed.starts_with(
		"-0X",
	) or trimmed.starts_with("+0x") or trimmed.starts_with("+0X")):
		func <- parse_int_radix(trimmed, 16)
	if(trimmed.starts_with("0b") or trimmed.starts_with("0B") or trimmed.starts_with("-0b") or trimmed.starts_with(
		"-0B",
	) or trimmed.starts_with("+0b") or trimmed.starts_with("+0B")):
		func <- parse_int_radix(trimmed, 2)
	if(trimmed.starts_with("0o") or trimmed.starts_with("0O") or trimmed.starts_with("-0o") or trimmed.starts_with(
		"-0O",
	) or trimmed.starts_with("+0o") or trimmed.starts_with("+0O")):
		func <- parse_int_radix(trimmed, 8)
	parse_int_radix(trimmed, 10)
}

// Parses a decimal floating-point number with optional sign, fraction, and scientific exponent from `s`, or returns `none`.
bind parse_float = (s: string) ?float: {
	let trimmed = s.trim()
	let n = trimmed.len()
	if(n == 0): func <- none

	let i = 0
	let is_neg = false
	let b0 = trimmed.byte_at(0)
	if(b0 == u8(45)): { // '-'
		is_neg = true
		i += 1
	} else if(b0 == u8(43)): { // '+'
		i += 1
	}
	if(i >= n): func <- none

	let val = 0.0
	let has_digits = false
	for(i < n): {
		let b = trimmed.byte_at(i)
		if(b >= u8(48) and b <= u8(57)): {
			val = val * 10.0 + float(int(b) - 48)
			has_digits = true
			i += 1
		} else: for <-
	}

	if(i < n and trimmed.byte_at(i) == u8(46)): { // '.'
		i += 1
		let frac_scale = 0.1
		for(i < n): {
			let b = trimmed.byte_at(i)
			if(b >= u8(48) and b <= u8(57)): {
				val += float(int(b) - 48) * frac_scale
				frac_scale *= 0.1
				has_digits = true
				i += 1
			} else: for <-
		}
	}

	if(not has_digits): func <- none

	if(i < n and (trimmed.byte_at(i) == u8(101) or trimmed.byte_at(i) == u8(69))): { // 'e' or 'E'
		i += 1
		if(i >= n): func <- none
		let exp_neg = false
		let eb = trimmed.byte_at(i)
		if(eb == u8(45)): {
			exp_neg = true
			i += 1
		} else if(eb == u8(43)): i += 1
		if(i >= n): func <- none
		let exp_val = 0
		let has_exp_digits = false
		for(i < n): {
			let b = trimmed.byte_at(i)
			if(b >= u8(48) and b <= u8(57)): {
				exp_val = exp_val * 10 + (int(b) - 48)
				has_exp_digits = true
				i += 1
			} else: func <- none
		}
		if(not has_exp_digits): func <- none
		let e = 0
		for(e < exp_val): {
			if(exp_neg): val /= 10.0 else: val *= 10.0
			e += 1
		}
	}

	if(i != n): func <- none
	if(is_neg): -val else: val
}

// -----------------------------------------------------------------------------
// Core Utility Primitives
// -----------------------------------------------------------------------------

// Returns the smaller of two comparable values.
bind min[T: Comparable + Copyable] = (a: T, b: T) T:
	if(a < b): a.copy() else: b.copy()

// Returns the larger of two comparable values.
bind max[T: Comparable + Copyable] = (a: T, b: T) T:
	if(a > b): a.copy() else: b.copy()

// Returns the absolute value of a signed integer.
bind abs = (x: int) int:
	if(x < 0): -x else: x

// Clamps a value within the range [low, high].
bind clamp[T: Comparable + Copyable] = (val: T, low: T, high: T) T:
	if(val < low): low.copy()
	else if(val > high): high.copy()
	else: val.copy()
