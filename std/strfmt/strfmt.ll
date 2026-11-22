// std/strfmt — Number/currency/byte formatting, string padding, truncation, and case conversions.
//
// Implemented in pure Lowlight for human-readable numbers, aligned tables, and identifier normalization.

// -----------------------------------------------------------------------------
// Number & Data Formatting Methods on Primitive Types (float & int)
// -----------------------------------------------------------------------------

// Formats this [`float`] with `precision` decimal digits (or general format when `precision < 0`).
bind float.format_float = fn(self, precision: int) string:
	if(precision < 0): "$self" else: format_float_prec(self, precision)

// Shorthand alias for [`float.format_float`].
bind float.format = fn(self, precision: int) string: self.format_float(precision)

// Formats this [`int`] as a base-10 decimal string.
bind int.format_int = fn(self) string: "$self"

// Formats this [`int`] with ASCII comma thousands separators (e.g. `"1,234,567"`).
bind int.format_commas = fn(self) string: {
	if(self == -9223372036854775807 - 1): func <- "-9,223,372,036,854,775,808"
	let is_neg = self < 0
	let abs_val = if(is_neg): -self else: self
	let s = "$abs_val"
	let n = s.len()
	if(n <= 3):
		if(is_neg): "-$s" else: s
	else: {
		let out = if(is_neg): "-" else: ""
		let rem = n % 3
		if(rem > 0): {
			out.push(s[0..rem])
			if(rem < n): out.push(",")
		}
		let i = rem
		for(i < n): {
			out.push(s[i..i + 3])
			i += 3
			if(i < n): out.push(",")
		}
		out
	}
}

// Alias for [`int.format_commas`].
bind int.format_int_commas = fn(self) string: self.format_commas()

// Formats this [`float`] with `precision` fractional digits and comma thousands separators.
bind float.format_commas = fn(self, precision: int) string: {
	let s = self.format_float(precision)
	let dot_pos? = s.index_of(".")
	if(dot_pos? as dot): {
		let int_part = s[0..dot]
		let frac_part = s[dot..s.len()]
		let is_neg = int_part.starts_with("-")
		let digits = if(is_neg): int_part[1..int_part.len()] else: int_part
		let n = digits.len()
		let out = if(is_neg): "-" else: ""
		let rem = n % 3
		if(rem > 0): {
			out.push(digits[0..rem])
			if(rem < n): out.push(",")
		}
		let i = rem
		for(i < n): {
			out.push(digits[i..i + 3])
			i += 3
			if(i < n): out.push(",")
		}
		out.push(frac_part)
		out
	} else: {
		let is_neg = s.starts_with("-")
		let digits = if(is_neg): s[1..s.len()] else: s
		let n = digits.len()
		let out = if(is_neg): "-" else: ""
		let rem = n % 3
		if(rem > 0): {
			out.push(digits[0..rem])
			if(rem < n): out.push(",")
		}
		let i = rem
		for(i < n): {
			out.push(digits[i..i + 3])
			i += 3
			if(i < n): out.push(",")
		}
		out
	}
}

// Alias for [`float.format_commas`].
bind float.format_float_commas = fn(self, precision: int) string: self.format_commas(precision)

// Formats this byte size in human-readable units (`B`, `KB`, `MB`, or `GB`).
bind int.format_bytes = fn(self) string:
	if(self < 1024): "$self B"
	else if(self < 1048576): {
		let kb = float(self) / 1024.0
		"${kb.format_float(2)} KB"
	} else if(self < 1073741824): {
		let mb = float(self) / 1048576.0
		"${mb.format_float(2)} MB"
	} else: {
		let gb = float(self) / 1073741824.0
		"${gb.format_float(2)} GB"
	}

// Scales this fraction (`0.25` -> `"25%"`) and formats it as a percentage with `precision` decimals.
bind float.format_percent = fn(self, precision: int) string: {
	let pct = self * 100.0
	"${pct.format_float(precision)}%"
}

// Formats this numeric amount prefixed with `symbol` and comma-grouped to `precision` decimal places.
bind float.format_currency = fn(self, symbol: string, precision: int) string: {
	let formatted = self.format_commas(precision)
	"$symbol$formatted"
}

// Function form of [`float.format_float`].
bind format_float = fn(v: float, precision: int) string: v.format_float(precision)

// Function form of [`int.format_int`].
bind format_int = fn(v: int) string: v.format_int()

// Function form of [`int.format_commas`].
bind format_int_commas = fn(v: int) string: v.format_commas()

// Function form of [`float.format_commas`].
bind format_float_commas = fn(v: float, precision: int) string: v.format_commas(precision)

// Function form of [`int.format_bytes`].
bind format_bytes = fn(bytes: int) string: bytes.format_bytes()

// Function form of [`float.format_percent`].
bind format_percent = fn(v: float, precision: int) string: v.format_percent(precision)

// Function form of [`float.format_currency`].
bind format_currency = fn(amount: float, symbol: string, precision: int) string:
	amount.format_currency(symbol, precision)

// -----------------------------------------------------------------------------
// Integer Padding Functions & Methods
// -----------------------------------------------------------------------------

// Formats signed integer `val` left-padded with `'0'` to at least `width` characters.
bind pad_int_zeros = fn(val: int, width: int) string: {
	let s = "$val"
	let diff = width - s.len()
	if(diff <= 0): func <- s
	let out = ""
	for(i in 0..diff): out.push("0")
	out.push(s)
	out
}

// Formats signed integer `val` left-padded with spaces to at least `width` characters.
bind pad_int_spaces = fn(val: int, width: int) string: {
	let s = "$val"
	let diff = width - s.len()
	if(diff <= 0): func <- s
	let out = ""
	for(i in 0..diff): out.push(" ")
	out.push(s)
	out
}

// Method form of [`pad_int_zeros`].
bind int.pad_zeros = fn(self, width: int) string: pad_int_zeros(self, width)

// Method form of [`pad_int_spaces`].
bind int.pad_spaces = fn(self, width: int) string: pad_int_spaces(self, width)

// -----------------------------------------------------------------------------
// String Extension Methods (Uniform Method Call Syntax)
// -----------------------------------------------------------------------------

bind _strfmt_char_count = fn(s: string) int: {
	let n = 0
	for(ch in s): n += 1
	n
}

bind _strfmt_first_char = fn(fill: string) char: {
	if(fill.len() == 0): func <- ' '
	let next_i = 0
	str_utf8_next(fill, 0, &next_i)
}

// Left-pads the string to at least `width` Unicode codepoints using the first codepoint of `fill` (or space if empty).
bind string.pad_left_fill = fn(self, width: int, fill: string) string: {
	let diff = width - _strfmt_char_count(self)
	if(diff <= 0): func <- self.copy()
	let fill_ch = _strfmt_first_char(fill)
	let out = ""
	for(i in 0..diff): out.push_char(fill_ch)
	out.push(self)
	out
}

// Left-pads the string with spaces to at least `width` Unicode codepoints.
bind string.pad_left = fn(self, width: int) string: self.pad_left_fill(width, " ")

// Right-pads the string to at least `width` Unicode codepoints using the first codepoint of `fill` (or space if empty).
bind string.pad_right_fill = fn(self, width: int, fill: string) string: {
	let diff = width - _strfmt_char_count(self)
	if(diff <= 0): func <- self.copy()
	let fill_ch = _strfmt_first_char(fill)
	let out = self.copy()
	for(i in 0..diff): out.push_char(fill_ch)
	out
}

// Right-pads the string with spaces to at least `width` Unicode codepoints.
bind string.pad_right = fn(self, width: int) string: self.pad_right_fill(width, " ")

// Centers the string within `width` Unicode codepoints using the first codepoint of `fill` (or space if empty).
bind string.center_fill = fn(self, width: int, fill: string) string: {
	let diff = width - _strfmt_char_count(self)
	if(diff <= 0): func <- self.copy()
	let left_pad = diff / 2
	let right_pad = diff - left_pad
	let fill_ch = _strfmt_first_char(fill)
	let out = ""
	for(i in 0..left_pad): out.push_char(fill_ch)
	out.push(self)
	for(i in 0..right_pad): out.push_char(fill_ch)
	out
}

// Centers the string with spaces within `width` Unicode codepoints.
bind string.center = fn(self, width: int) string: self.center_fill(width, " ")

// Truncates the string to `max_len` bytes, appending `ellipsis` when shortened.
bind string.truncate_with = fn(self, max_len: int, ellipsis: string) string:
	if(max_len <= 0): ""
	else if(self.len() <= max_len): self.copy()
	else if(max_len <= ellipsis.len()): self[0..max_len]
	else: {
		let keep = max_len - ellipsis.len()
		let out = self[0..keep]
		out.push(ellipsis)
		out
	}

// Truncates the string to `max_len` bytes using `"..."` as the ellipsis.
bind string.truncate = fn(self, max_len: int) string: self.truncate_with(max_len, "...")

// Converts words delimited by whitespace, `'_'`, or `'-'` to Title Case.
bind string.to_title = fn(self) string: {
	let out = ""
	let capitalize_next = true
	for(ch in self):
		if(ch == ' ' or ch == '\t' or ch == '\n' or ch == '_' or ch == '-'): {
			capitalize_next = true
			out.push_char(ch)
		} else if(capitalize_next): {
			capitalize_next = false
			let c = if(ch >= 'a' and ch <= 'z'): char(int(ch) - 32) else: ch
			out.push_char(c)
		} else: {
			let c = if(ch >= 'A' and ch <= 'Z'): char(int(ch) + 32) else: ch
			out.push_char(c)
		}
	out
}

// Converts camelCase, spaces, or hyphens into `snake_case`.
bind string.to_snake_case = fn(self) string: {
	let out = ""
	let prev_was_sep = true
	for(i, ch in self):
		if(ch == ' ' or ch == '-' or ch == '_'): {
			if(not prev_was_sep and out.len() > 0): out.push("_")
			prev_was_sep = true
		} else if(ch >= 'A' and ch <= 'Z'): {
			if(not prev_was_sep and out.len() > 0): out.push("_")
			out.push_char(char(int(ch) + 32))
			prev_was_sep = false
		} else: {
			out.push_char(ch)
			prev_was_sep = false
		}
	out
}

// Converts camelCase, spaces, or underscores into `kebab-case`.
bind string.to_kebab_case = fn(self) string: {
	let s = self.to_snake_case()
	s.replace_all("_", "-")
}

// Converts snake_case, kebab-case, or spaced words into `camelCase`.
bind string.to_camel_case = fn(self) string: {
	let out = ""
	let upper_next = false
	let is_first = true
	for(ch in self):
		if(ch == ' ' or ch == '_' or ch == '-'): upper_next = true
		else if(is_first): {
			is_first = false
			upper_next = false
			let c = if(ch >= 'A' and ch <= 'Z'): char(int(ch) + 32) else: ch
			out.push_char(c)
		} else if(upper_next): {
			upper_next = false
			let c = if(ch >= 'a' and ch <= 'z'): char(int(ch) - 32) else: ch
			out.push_char(c)
		} else: out.push_char(ch)
	out
}

// Wraps the string in double quotes and escapes `\"`, `\\`, `\n`, `\t`, and `\r`.
bind string.quote = fn(self) string: {
	let out = "\""
	for(ch in self):
		if(ch == '"'): out.push("\\\"")
		else if(ch == '\\'): out.push("\\\\")
		else if(ch == '\n'): out.push("\\n")
		else if(ch == '\t'): out.push("\\t")
		else if(ch == '\r'): out.push("\\r")
		else: out.push_char(ch)
	out.push("\"")
	out
}

bind _strfmt_hex_digit = fn(b: u8) int: {
	let v = int(b)
	if(v >= 48 and v <= 57): func <- v - 48
	if(v >= 65 and v <= 70): func <- v - 65 + 10
	if(v >= 97 and v <= 102): func <- v - 97 + 10
	-1
}

bind _strfmt_parse_hex4 = fn(s: string, pos: int) int: {
	if(pos + 4 > s.len()): func <- -1
	let d0 = _strfmt_hex_digit(s.byte_at(pos))
	let d1 = _strfmt_hex_digit(s.byte_at(pos + 1))
	let d2 = _strfmt_hex_digit(s.byte_at(pos + 2))
	let d3 = _strfmt_hex_digit(s.byte_at(pos + 3))
	if(d0 < 0 or d1 < 0 or d2 < 0 or d3 < 0): func <- -1
	(d0 << 12) | (d1 << 8) | (d2 << 4) | d3
}

// Unquotes a trimmed double-quoted string and decodes escape sequences (`\n`, `\t`, `\r`, `\"`, `\\`, `\/`, `\b`, `\f`, `\0`, `\'`, and `\uXXXX` including UTF-16 surrogate pairs), returning `none` if unquoted or malformed.
bind string.unquote = fn(self) ?string: {
	let trimmed = self.trim()
	let tlen = trimmed.len()
	if(tlen < 2): func <- none
	if(trimmed.byte_at(0) != u8(34) or trimmed.byte_at(tlen - 1) != u8(34)): func <- none
	let inner = trimmed[1..tlen - 1]
	let n = inner.len()
	let out = ""
	let i = 0
	while(i < n): {
		let b = inner.byte_at(i)
		if(b == u8(34)): func <- none
		if(b == u8(92)): {
			i += 1
			if(i >= n): func <- none
			let esc = char(int(inner.byte_at(i)))
			i += 1
			if(esc == 'n'): out.push_byte(u8(10))
			else if(esc == 't'): out.push_byte(u8(9))
			else if(esc == 'r'): out.push_byte(u8(13))
			else if(esc == '"'): out.push_byte(u8(34))
			else if(esc == '\\'): out.push_byte(u8(92))
			else if(esc == '/'): out.push_byte(u8(47))
			else if(esc == 'b'): out.push_byte(u8(8))
			else if(esc == 'f'): out.push_byte(u8(12))
			else if(esc == '0'): out.push_byte(u8(0))
			else if(esc == '\''): out.push_byte(u8(39))
			else if(esc == 'u'): {
				let u1 = _strfmt_parse_hex4(inner, i)
				if(u1 < 0): func <- none
				i += 4
				if(u1 >= 0xD800 and u1 <= 0xDBFF): {
					if(i + 6 > n or inner.byte_at(i) != u8(92) or inner.byte_at(i + 1) != u8(117)): func <- none
					let u2 = _strfmt_parse_hex4(inner, i + 2)
					if(u2 < 0xDC00 or u2 > 0xDFFF): func <- none
					i += 6
					let cp = 0x10000 + ((u1 - 0xD800) << 10) + (u2 - 0xDC00)
					out.push_char(char(cp))
				} else if(u1 >= 0xDC00 and u1 <= 0xDFFF): func <- none
				else: out.push_char(char(u1))
			} else: func <- none
		} else: {
			out.push_byte(b)
			i += 1
		}
	}
	out
}

// Prefixes every non-empty line in the string with `spaces` ASCII spaces.
bind string.indent = fn(self, spaces: int) string: {
	if(spaces <= 0): func <- self.copy()
	let prefix = ""
	for(i in 0..spaces): prefix.push(" ")
	let lines = self.split("\n")
	let out_lines: [string] = []
	for(line in lines):
		if(line.len() > 0): out_lines.push("$prefix$line") else: out_lines.push("")
	out_lines.join("\n")
}

bind _str_matches_at = fn(s: string, pos: int, sub: string) bool: {
	let j = 0
	let n = sub.len()
	for(j < n): {
		if(s.byte_at(pos + j) != sub.byte_at(j)): func <- false
		j += 1
	}
	true
}

// Counts non-overlapping occurrences of `sub` in the string (`0` if `sub` is empty).
bind string.count_occurrences = fn(self, sub: string) int: {
	let sub_len = sub.len()
	if(sub_len == 0 or self.len() < sub_len): func <- 0
	let count = 0
	let i = 0
	let limit = self.len() - sub_len
	while(i <= limit):
		if(_str_matches_at(self, i, sub)): {
			count += 1
			i += sub_len
		} else: i += 1
	count
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("strfmt numeric formatting: commas, bytes, percent, currency")
bind test_strfmt_numeric_formatting = fn(): {
	assert(1000000.format_commas() == "1,000,000", "1,000,000 commas")
	assert((-1234567).format_commas() == "-1,234,567", "negative commas")
	assert(123.format_commas() == "123", "short number commas")

	assert(500.format_bytes() == "500 B", "format bytes B")
	assert(1048576.format_bytes() == "1.00 MB", "format bytes MB")

	assert(0.125.format_percent(1) == "12.5%", "format percent")
	assert(49.99.format_currency("\$", 2) == "\$49.99", "format currency")
}

@test("strfmt padding, alignment, and truncation")
bind test_strfmt_padding_and_truncation = fn(): {
	assert(pad_int_zeros(42, 5) == "00042", "pad_int_zeros")
	assert(pad_int_spaces(42, 5) == "   42", "pad_int_spaces")

	assert("hello".pad_left(8) == "   hello", "pad_left")
	assert("hello".pad_right(8) == "hello   ", "pad_right")
	assert("hello".center(9) == "  hello  ", "center")

	assert("café".pad_left(6) == "  café", "pad_left utf8")
	assert("café".pad_right(6) == "café  ", "pad_right utf8")
	assert("😀".center(5) == "  😀  ", "center emoji")
	assert("αβ".pad_left_fill(5, "·") == "···αβ", "pad_left_fill utf8")
	assert("αβ".pad_right_fill(5, "★☆") == "αβ★★★", "pad_right_fill utf8 first codepoint")
	assert("αβ".center_fill(5, "—") == "—αβ——", "center_fill utf8")
	assert("αβ".pad_left_fill(4, "") == "  αβ", "pad_left_fill empty fill defaults to space")
	assert("café".pad_left(4) == "café", "pad_left utf8 exact")
	assert("café".pad_left(-1) == "café", "pad_left utf8 negative")

	assert("very long string here".truncate(10) == "very lo...", "truncate long")
	assert("short".truncate(10) == "short", "truncate short")
	assert("hello".truncate(0) == "", "truncate zero")
	assert("hello".truncate(-5) == "", "truncate negative")
	assert("".truncate(-1) == "", "truncate empty negative")
	assert("hello".truncate_with(2, "...") == "he", "truncate smaller than ellipsis")
	assert("hello".truncate_with(-3, "") == "", "truncate empty ellipsis negative")
}

@test("strfmt case conversions: title, snake, kebab, camel")
bind test_strfmt_casing_and_transforms = fn(): {
	assert("hello world".to_title() == "Hello World", "to_title")
	assert("helloWorld".to_snake_case() == "hello_world", "to_snake_case")
	assert("helloWorld".to_kebab_case() == "hello-world", "to_kebab_case")
	assert("hello_world".to_camel_case() == "helloWorld", "to_camel_case")
}

@test("strfmt quote, unquote, and indentation")
bind test_strfmt_quote_and_indent = fn(): {
	let quoted = "hello\nworld".quote()
	assert(quoted == "\"hello\\nworld\"", "quote")

	let unquoted? = quoted.unquote()
	assert(unquoted? == "hello\nworld", "unquote")

	let indented = "line1\nline2".indent(2)
	assert(indented == "  line1\n  line2", "indent")
}

@test("strfmt unquote escapes and validation")
bind test_strfmt_unquote_escapes = fn(): {
	assert("\"\"".unquote() == "", "unquote empty quoted")
	assert("  \"hi\"  ".unquote() == "hi", "unquote trimmed")
	assert("\"héllo 🌍\"".unquote() == "héllo 🌍", "unquote utf8 passthrough")

	let std_esc? = "\"\\n\\t\\r\\\"\\\\\\/\\b\\f\\0\\'\"".unquote()
	assert(std_esc? == "\n\t\r\"\\/\x08\x0c\0'", "unquote standard escapes")

	// Trailing backslash validation
	assert("\"abc\\\"".unquote() == none, "unquote trailing backslash after chars")
	assert("\"\\\"".unquote() == none, "unquote lone backslash")
	assert("\"\\\\\"".unquote() == "\\", "unquote escaped backslash")
	assert("\"\\\\\\\"".unquote() == none, "unquote odd trailing backslashes")

	// Missing quotes, unescaped inner quotes, and unknown escapes
	assert("".unquote() == none, "unquote empty")
	assert("\"".unquote() == none, "unquote single quote")
	assert("hello".unquote() == none, "unquote no quotes")
	assert("\"hello".unquote() == none, "unquote missing closing quote")
	assert("hello\"".unquote() == none, "unquote missing opening quote")
	assert("\"a\"b\"".unquote() == none, "unquote unescaped inner quote")
	assert("\"\\q\"".unquote() == none, "unquote unknown escape")
	assert("\"prefix\\z\"".unquote() == none, "unquote unknown escape after prefix")

	// Valid \uXXXX BMP escapes
	assert("\"\\u0041\"".unquote() == "A", "unquote u0041")
	assert("\"\\u00e9\"".unquote() == "é", "unquote u00e9 lowercase")
	assert("\"\\u03B1\"".unquote() == "α", "unquote u03B1")
	assert("\"\\u4E2D\"".unquote() == "中", "unquote u4E2D")
	assert("\"a\\u0041b\\u0042c\"".unquote() == "aAbBc", "unquote mixed uXXXX")
	let nul? = "\"\\u0000\"".unquote()
	if(nul? as n): {
		assert(n.len() == 1 and n.byte_at(0) == u8(0), "unquote u0000 byte")
	} else: assert(false, "unquote u0000 should not be none")
	assert(nul? == "\0", "unquote u0000")
	assert("\"\\uD7FF\"".unquote() == char(0xD7FF).to_string(), "unquote uD7FF")
	assert("\"\\uE000\"".unquote() == char(0xE000).to_string(), "unquote uE000")

	// Valid UTF-16 surrogate pairs
	assert("\"\\uD83D\\uDE00\"".unquote() == "😀", "unquote emoji surrogate pair")
	assert("\"\\ud83d\\ude00\"".unquote() == "😀", "unquote lowercase surrogate pair")
	assert("\"\\uD800\\uDC00\"".unquote() == char(0x10000).to_string(), "unquote min surrogate pair")
	assert("\"\\uDBFF\\uDFFF\"".unquote() == char(0x10FFFF).to_string(), "unquote max surrogate pair")

	// Invalid \uXXXX escapes: truncated, non-hex, lone/mismatched surrogates
	assert("\"\\u\"".unquote() == none, "unquote truncated u")
	assert("\"\\u1\"".unquote() == none, "unquote truncated u1")
	assert("\"\\u12\"".unquote() == none, "unquote truncated u12")
	assert("\"\\u123\"".unquote() == none, "unquote truncated u123")
	assert("\"abc\\u12\"".unquote() == none, "unquote truncated u after prefix")
	assert("\"\\u004G\"".unquote() == none, "unquote non-hex G")
	assert("\"\\uZZZZ\"".unquote() == none, "unquote non-hex ZZZZ")
	assert("\"\\u12 4\"".unquote() == none, "unquote space in hex")
	assert("\"\\u{41}\"".unquote() == none, "unquote braced unicode escape")
	assert("\"\\uD83D\"".unquote() == none, "unquote lone high surrogate")
	assert("\"\\uD83Dabc\"".unquote() == none, "unquote high surrogate followed by text")
	assert("\"\\uD83D\\n\"".unquote() == none, "unquote high surrogate followed by \\n")
	assert("\"\\uD83D\\uDE0\"".unquote() == none, "unquote high surrogate short low")
	assert("\"\\uD83D\\uDE0G\"".unquote() == none, "unquote high surrogate invalid hex low")
	assert("\"\\uD83D\\u0041\"".unquote() == none, "unquote high surrogate followed by BMP")
	assert("\"\\uD83D\\uD83D\"".unquote() == none, "unquote two high surrogates")
	assert("\"\\uDC00\"".unquote() == none, "unquote lone low surrogate")
	assert("\"\\uDE00\\uD83D\"".unquote() == none, "unquote reversed surrogate pair")
}

@test("strfmt count_occurrences edge cases and large input")
bind test_strfmt_count_occurrences = fn(): {
	assert("banana".count_occurrences("an") == 2, "count_occurrences an")
	assert("banana".count_occurrences("na") == 2, "count_occurrences na")
	assert("aaa".count_occurrences("aa") == 1, "count_occurrences non-overlapping")
	assert("aaaa".count_occurrences("aa") == 2, "count_occurrences even non-overlapping")
	assert("abc".count_occurrences("") == 0, "count_occurrences empty needle")
	assert("".count_occurrences("a") == 0, "count_occurrences empty haystack")
	assert("ab".count_occurrences("abcd") == 0, "count_occurrences longer needle")
	assert("αβγαβ".count_occurrences("αβ") == 2, "count_occurrences utf8")

	let large = ""
	for(i in 0..10000): large.push_byte(u8(97))
	assert(large.count_occurrences("a") == 10000, "count_occurrences 10k single-byte")
	assert(large.count_occurrences("aa") == 5000, "count_occurrences 10k pair")
	assert(large.count_occurrences("b") == 0, "count_occurrences 10k absent")
}

@test("format_float honors precision beyond 15 digits exactly")
bind test_strfmt_high_precision = fn(): {
	assert((0.1).format_float(20) == "0.10000000000000000555", "0.1 to 20 places")
	assert((1.0 / 3.0).format_float(25) == "0.3333333333333333148296163", "1/3 to 25 places")
	assert((2.5).format_float(17) == "2.50000000000000000", "exact value padded")
	assert((-0.000123).format_float(20) == "-0.00012300000000000001", "negative")
	assert((1e22).format_float(16) == "10000000000000000000000.0000000000000000", "large integer")
	assert((0.1).format_float(2) == "0.10", "short precisions unchanged")
}
