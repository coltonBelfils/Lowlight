// std/strings — Extended string search, padding, splitting, and manipulation helpers.
//
// Builds on the core prelude methods (`is_empty`, `starts_with`, `ends_with`, `index_of`,
// `contains`, `trim`, `to_upper`, `to_lower`, `replace`, `split`, `chars`, `bytes`,
// `join_strings`) from [`std/core/primitives`] and [`std/chars`].

import "std/chars"

// -----------------------------------------------------------------------------
// Inspection & Predicates
// -----------------------------------------------------------------------------

// Reports whether the string is non-empty and consists solely of whitespace characters.
bind string.is_whitespace = fn(self) bool: {
	if(self.len() == 0): func <- false
	for(ch in self.chars()): if(not ch.is_whitespace()): func <- false
	true
}

// Reports whether every byte in the string is valid 7-bit ASCII (`<= 127`).
bind string.is_ascii = fn(self) bool: {
	for(i in 0..self.len()): if(self[i] > 127): func <- false
	true
}

// -----------------------------------------------------------------------------
// Extended Substring Search
// -----------------------------------------------------------------------------

bind _matches_at = fn(s: string, pos: int, sub: string) bool: {
	let sub_len = sub.len()
	let j = 0
	for(j < sub_len): {
		if(s.byte_at(pos + j) != sub.byte_at(j)): func <- false
		j += 1
	}
	true
}

// Returns the byte offset of the last occurrence of `sub`, or `none` if absent (returns `self.len()` when `sub` is `""`).
bind string.last_index_of = fn(self, sub: string) ?int: {
	if(sub.len() == 0): func <- self.len()
	if(self.len() < sub.len()): func <- none
	let limit = self.len() - sub.len()
	let i = limit
	while(i >= 0): {
		if(_matches_at(self, i, sub)): func <- i
		i -= 1
	}
	none
}

// Counts non-overlapping occurrences of `sub` within the string (`0` if `sub` is empty).
bind string.count = fn(self, sub: string) int: {
	let sub_len = sub.len()
	if(sub_len == 0 or self.len() < sub_len): func <- 0
	let count = 0
	let i = 0
	let limit = self.len() - sub_len
	while(i <= limit):
		if(_matches_at(self, i, sub)): {
			count += 1
			i += sub_len
		} else: i += 1
	count
}

// -----------------------------------------------------------------------------
// Capitalization
// -----------------------------------------------------------------------------

// Returns a copy with the first ASCII byte uppercased and all subsequent ASCII letters lowercased.
bind string.capitalize = fn(self) string: {
	if(self.len() == 0): func <- ""
	let out = ""
	let first = self[0]
	if(first >= 97 and first <= 122): out.push_byte(first - 32) else: out.push_byte(first)
	for(i in 1..self.len()): {
		let b = self[i]
		if(b >= 65 and b <= 90): out.push_byte(b + 32) else: out.push_byte(b)
	}
	out
}

// -----------------------------------------------------------------------------
// Replacement & Manipulation
// -----------------------------------------------------------------------------

// Returns the string concatenated `count` times (empty when `count <= 0`).
bind string.repeat = fn(self, count: int) string: {
	if(count <= 0 or self.len() == 0): func <- ""
	let out = ""
	for(i in 0..count): out.push(self)
	out
}

// Returns a new string with Unicode codepoints reversed in order.
bind string.reverse = fn(self) string: {
	if(self.len() <= 1): func <- self.copy()
	let cps = self.chars()
	let out = ""
	let i = cps.len() - 1
	while(i >= 0): {
		out.push_char(cps[i])
		i -= 1
	}
	out
}

// -----------------------------------------------------------------------------
// Padding & Alignment
// -----------------------------------------------------------------------------

bind _char_count = fn(s: string) int: {
	let n = 0
	for(ch in s): n += 1
	n
}

// Left-pads the string with repeated `pad_char` until its Unicode codepoint count reaches at least `total_len`.
bind string.pad_left_char = fn(self, total_len: int, pad_char: char) string: {
	let char_len = _char_count(self)
	if(char_len >= total_len): func <- self.copy()
	let pad_count = total_len - char_len
	let pad_str = pad_char.to_string().repeat(pad_count)
	let out = ""
	out.push(pad_str)
	out.push(self)
	out
}

// Right-pads the string with repeated `pad_char` until its Unicode codepoint count reaches at least `total_len`.
bind string.pad_right_char = fn(self, total_len: int, pad_char: char) string: {
	let char_len = _char_count(self)
	if(char_len >= total_len): func <- self.copy()
	let pad_count = total_len - char_len
	let pad_str = pad_char.to_string().repeat(pad_count)
	let out = ""
	out.push(self)
	out.push(pad_str)
	out
}

// Centers the string within `total_len` Unicode codepoints by padding evenly on both sides with `pad_char`.
bind string.center_char = fn(self, total_len: int, pad_char: char) string: {
	let char_len = _char_count(self)
	if(char_len >= total_len): func <- self.copy()
	let total_pad = total_len - char_len
	let left_pad = total_pad / 2
	let right_pad = total_pad - left_pad
	let pad_unit = pad_char.to_string()
	let out = ""
	out.push(pad_unit.repeat(left_pad))
	out.push(self)
	out.push(pad_unit.repeat(right_pad))
	out
}

// -----------------------------------------------------------------------------
// Splitting, Tokenization, & Joining
// -----------------------------------------------------------------------------

// Splits the string on runs of ASCII whitespace into non-empty token substrings.
bind string.split_whitespace = fn(self) [string]: {
	let out: [string] = []
	let in_token = false
	let start = 0
	for(i in 0..self.len()): {
		let b = self[i]
		let is_ws = b < u8(128) and char(int(b)).is_whitespace()
		if(is_ws): {
			if(in_token): {
				out.push(self[start..i].to_string())
				in_token = false
			}
		} else if(not in_token): {
			start = i
			in_token = true
		}
	}
	if(in_token): out.push(self[start..self.len()].to_string())
	out
}

// Splits this string into lines, removing the line terminators (`\n` or `\r\n`).
//
// A final line terminator ends the last line rather than starting an empty one, so
// `"a\nb\n".lines()` is `["a", "b"]`, while `"a\nb\n\n".lines()` keeps the empty line
// (`["a", "b", ""]`). An empty string has no lines.
bind string.lines = fn(self) [string]: {
	if(self.is_empty()): func <- []
	let raw_lines = self.split("\n")
	let count = if(self.ends_with("\n")): raw_lines.len() - 1 else: raw_lines.len()
	for(i, line in raw_lines) [string]: {
		if(i >= count): next <-
		if(line.ends_with("\r")): line[0..line.len() - 1].to_string() else: line.copy()
	}
}

// Concatenates `list` elements separated by `sep` (convenience wrapper around [`join_strings`]).
bind join = fn(list: [string], sep: string) string: join_strings(list, sep)

// Returns all decoded Unicode [`char`] codepoints in this string (alias for [`string.chars`]).
bind string.chars_list = fn(self) [char]: self.chars()

// Returns all raw UTF-8 bytes in this string (alias for [`string.bytes`]).
bind string.bytes_list = fn(self) [u8]: self.bytes()

// -----------------------------------------------------------------------------
// ByteBuffer
// -----------------------------------------------------------------------------

// A growable byte buffer with a read cursor, for binary formats and protocols.
//
// `write_*` methods append to the end; `read_*` methods consume from the cursor and return
// `none` when too few bytes remain. Multi-byte integers are little-endian by default, with
// `_be` variants for big-endian (network byte order).
//
// ```lowlight
// let b = ByteBuffer.new()
// b.write_u16(0x1234)
// b.write_u32_be(7)
// let x = b.read_u16() ?? 0       // 0x1234
// let y = b.read_u32_be() ?? 0    // 7
// ```
define ByteBuffer struct [
	_data: [u8],
	_pos: int,
]

// Creates an empty [`ByteBuffer`].
bind ByteBuffer.new = fn() ByteBuffer: ByteBuffer[_data: [], _pos: 0]

// Creates a [`ByteBuffer`] holding `bytes`, with the cursor at the start.
bind ByteBuffer.from_bytes = fn(bytes: own [u8]) ByteBuffer: ByteBuffer[_data: own bytes, _pos: 0]

// Creates a [`ByteBuffer`] holding the UTF-8 bytes of `text`, with the cursor at the start.
bind ByteBuffer.from_string = fn(text: string) ByteBuffer: ByteBuffer[_data: text.bytes(), _pos: 0]

// Returns the total number of bytes, read or not.
bind ByteBuffer.len = fn(self) int: self._data.len()

// Reports whether the buffer holds no bytes.
bind ByteBuffer.is_empty = fn(self) bool: self._data.is_empty()

// Returns the cursor position: the index of the next byte to read.
bind ByteBuffer.position = fn(self) int: self._pos

// Returns how many bytes remain after the cursor.
bind ByteBuffer.remaining = fn(self) int: self._data.len() - self._pos

// Moves the cursor to `pos`, returning `false` (and leaving it unchanged) if out of range.
bind ByteBuffer.seek = fn(&self, pos: int) bool: {
	if(pos < 0 or pos > self._data.len()): func <- false
	self._pos = pos
	true
}

// Removes all bytes and resets the cursor.
bind ByteBuffer.clear = fn(&self): {
	self._data.clear()
	self._pos = 0
}

// Returns a copy of every byte in the buffer.
bind ByteBuffer.bytes = fn(self) [u8]: self._data.copy()

// Returns the bytes as a string (they are copied as-is, not validated as UTF-8).
bind ByteBuffer.to_string = fn(self) string: {
	let out = ""
	for(b in self._data): out.push_byte(b)
	out
}

// Appends the low `count` bytes of `v`, least significant first (or most significant first when `big_endian`).
bind ByteBuffer._write_uint = fn(&self, v: u64, count: int, big_endian: bool):
	for(i in 0..count): {
		let shift = if(big_endian): 8 * (count - 1 - i) else: 8 * i
		self._data.push(u8((v >> u64(shift)) & u64(0xff)))
	}

// Reads `count` bytes as an unsigned integer, or `none` if fewer remain.
bind ByteBuffer._read_uint = fn(&self, count: int, big_endian: bool) ?u64: {
	if(self.remaining() < count): func <- none
	let v = u64(0)
	for(i in 0..count): {
		let b = u64(self._data[self._pos + i])
		let shift = if(big_endian): 8 * (count - 1 - i) else: 8 * i
		v = v | (b << u64(shift))
	}
	self._pos += count
	v
}

// Appends one byte.
bind ByteBuffer.write_u8 = fn(&self, v: u8): self._data.push(v)

// Appends a 16-bit value, little-endian.
bind ByteBuffer.write_u16 = fn(&self, v: u16): self._write_uint(u64(v), 2, false)

// Appends a 32-bit value, little-endian.
bind ByteBuffer.write_u32 = fn(&self, v: u32): self._write_uint(u64(v), 4, false)

// Appends a 64-bit value, little-endian.
bind ByteBuffer.write_u64 = fn(&self, v: u64): self._write_uint(v, 8, false)

// Appends a 16-bit value, big-endian.
bind ByteBuffer.write_u16_be = fn(&self, v: u16): self._write_uint(u64(v), 2, true)

// Appends a 32-bit value, big-endian.
bind ByteBuffer.write_u32_be = fn(&self, v: u32): self._write_uint(u64(v), 4, true)

// Appends a 64-bit value, big-endian.
bind ByteBuffer.write_u64_be = fn(&self, v: u64): self._write_uint(v, 8, true)

// Appends every byte of `bytes`.
bind ByteBuffer.write_bytes = fn(&self, bytes: [u8]): for(b in bytes): self._data.push(b)

// Appends the UTF-8 bytes of `text`.
bind ByteBuffer.write_string = fn(&self, text: string): for(b in text.bytes()): self._data.push(b)

// Reads one byte, or `none` at the end.
bind ByteBuffer.read_u8 = fn(&self) ?u8: {
	if(self.remaining() < 1): func <- none
	let b = self._data[self._pos]
	self._pos += 1
	b
}

// Reads a little-endian 16-bit value, or `none` if fewer than 2 bytes remain.
bind ByteBuffer.read_u16 = fn(&self) ?u16: {
	let v = self._read_uint(2, false) ?? func <- none
	u16(v)
}

// Reads a little-endian 32-bit value, or `none` if fewer than 4 bytes remain.
bind ByteBuffer.read_u32 = fn(&self) ?u32: {
	let v = self._read_uint(4, false) ?? func <- none
	u32(v)
}

// Reads a little-endian 64-bit value, or `none` if fewer than 8 bytes remain.
bind ByteBuffer.read_u64 = fn(&self) ?u64: self._read_uint(8, false)

// Reads a big-endian 16-bit value, or `none` if fewer than 2 bytes remain.
bind ByteBuffer.read_u16_be = fn(&self) ?u16: {
	let v = self._read_uint(2, true) ?? func <- none
	u16(v)
}

// Reads a big-endian 32-bit value, or `none` if fewer than 4 bytes remain.
bind ByteBuffer.read_u32_be = fn(&self) ?u32: {
	let v = self._read_uint(4, true) ?? func <- none
	u32(v)
}

// Reads a big-endian 64-bit value, or `none` if fewer than 8 bytes remain.
bind ByteBuffer.read_u64_be = fn(&self) ?u64: self._read_uint(8, true)

// Reads the next `count` bytes, or `none` if fewer remain.
bind ByteBuffer.read_bytes = fn(&self, count: int) ?[u8]: {
	if(count < 0 or self.remaining() < count): func <- none
	let out = for(i in 0..count) [u8]: self._data[self._pos + i]
	self._pos += count
	out
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("string predicates: is_empty, is_whitespace, is_ascii")
bind test_strings_predicates = fn(): {
	assert("".is_empty(), "empty string is_empty")
	assert(not "hello".is_empty(), "non-empty string is not is_empty")

	assert(" \t\r\n ".is_whitespace(), "whitespace string")
	assert(not "".is_whitespace(), "empty is not whitespace")
	assert(not "  hello  ".is_whitespace(), "mixed is not whitespace")

	assert("hello 123!".is_ascii(), "ascii string")
	assert("".is_ascii(), "empty string is ascii")
}

@test("string search: starts_with, ends_with, index_of, last_index_of, contains, count")
bind test_strings_search = fn(): {
	assert("hello world".starts_with("hello"), "starts_with prefix")
	assert(not "hello world".starts_with("world"), "starts_with non-prefix")
	assert("hello".starts_with(""), "starts_with empty")

	assert("hello world".ends_with("world"), "ends_with suffix")
	assert(not "hello world".ends_with("hello"), "ends_with non-suffix")
	assert("hello".ends_with(""), "ends_with empty")

	assert("hello world".contains("lo wo"), "contains substring")
	assert(not "hello world".contains("xyz"), "contains non-substring")

	let idx1? = "banana".index_of("an")
	assert(idx1? == 1, "index_of an")
	let idx2? = "banana".index_of("xyz")
	assert(idx2? == none, "index_of missing")
	let idx3? = "banana".index_of("")
	assert(idx3? == 0, "index_of empty")

	let lidx1? = "banana".last_index_of("an")
	assert(lidx1? == 3, "last_index_of an")
	let lidx2? = "banana".last_index_of("xyz")
	assert(lidx2? == none, "last_index_of missing")
	let lidx3? = "banana".last_index_of("")
	assert(lidx3? == 6, "last_index_of empty")

	assert("banana".count("an") == 2, "count an")
	assert("banana".count("na") == 2, "count na")
	assert("banana".count("z") == 0, "count missing")
	assert("aaaa".count("aa") == 2, "count non-overlapping")
	assert("aaa".count("aa") == 1, "count odd non-overlapping")
	assert("abc".count("") == 0, "count empty needle")
	assert("ab".count("abcd") == 0, "count longer needle")

	let large = "a".repeat(10000)
	assert(large.count("a") == 10000, "count 10k single-byte")
	assert(large.count("aa") == 5000, "count 10k pair")
	assert(large.count("z") == 0, "count 10k absent")
}

@test("string trimming and padding")
bind test_strings_trim_pad = fn(): {
	assert("  \t hello \n ".trim_start() == "hello \n ", "trim_start")
	assert("  \t hello \n ".trim_end() == "  \t hello", "trim_end")
	assert("  \t hello \n ".trim() == "hello", "trim")

	assert("abc".pad_left_char(6, '0') == "000abc", "pad_left_char")
	assert("abc".pad_left_char(2, '0') == "abc", "pad_left_char no-op when shorter")
	assert("abc".pad_right_char(6, '-') == "abc---", "pad_right_char")
	assert("abc".pad_right_char(3, '-') == "abc", "pad_right_char no-op")
	assert("abc".center_char(7, '=') == "==abc==", "center_char odd")
	assert("abc".center_char(6, '=') == "=abc==", "center_char even")

	assert("café".pad_left_char(6, '0') == "00café", "pad_left_char utf8")
	assert("café".pad_left_char(4, '0') == "café", "pad_left_char utf8 exact")
	assert("café".pad_left_char(-2, '0') == "café", "pad_left_char utf8 negative")
	assert("café".pad_right_char(6, '·') == "café··", "pad_right_char utf8")
	assert("αβ".center_char(5, '★') == "★αβ★★", "center_char utf8")
	assert("😀".center_char(5, '=') == "==😀==", "center_char emoji")
	assert("".pad_left_char(3, 'é') == "ééé", "pad_left_char empty utf8")
	assert("".pad_left_char(0, 'é') == "", "pad_left_char empty zero")
}

@test("string transformations: case, repeat, reverse, replace")
bind test_strings_transform = fn(): {
	assert("Hello, World!".to_upper() == "HELLO, WORLD!", "to_upper")
	assert("Hello, World!".to_lower() == "hello, world!", "to_lower")
	assert("hello world".capitalize() == "Hello world", "capitalize lower")
	assert("HELLO WORLD".capitalize() == "Hello world", "capitalize upper")

	assert("ab".repeat(3) == "ababab", "repeat 3")
	assert("xyz".repeat(0) == "", "repeat 0")

	assert("hello".reverse() == "olleh", "reverse string")
	assert("".reverse() == "", "reverse empty")
	assert("a".reverse() == "a", "reverse single ascii")
	assert("é".reverse() == "é", "reverse single 2-byte char")
	assert("中".reverse() == "中", "reverse single 3-byte char")
	assert("😀".reverse() == "😀", "reverse single 4-byte emoji")
	assert("café".reverse() == "éfac", "reverse utf8")
	assert("aé中😀b".reverse() == "b😀中éa", "reverse mixed multibyte")
	assert("café 🌍 日本語".reverse().reverse() == "café 🌍 日本語", "reverse roundtrip utf8")

	assert("hello world world".replace("world", "there") == "hello there world", "replace first")
	assert("banana".replace_all("an", "XX") == "bXXXXa", "replace_all")
	assert("hello".replace("xyz", "abc") == "hello", "replace not found")
}

@test("string tokenization and joining: split, lines, join, chars/bytes")
bind test_strings_split_join = fn(): {
	let parts = "apple,banana,cherry".split(",")
	assert(parts.len() == 3, "split count")
	assert(parts[0] == "apple", "split 0")
	assert(parts[1] == "banana", "split 1")
	assert(parts[2] == "cherry", "split 2")

	let words = "  one \t two \r\n three  ".split_whitespace()
	assert(words.len() == 3, "split_whitespace count")
	assert(words[0] == "one", "word 0")
	assert(words[1] == "two", "word 1")
	assert(words[2] == "three", "word 2")

	let vf_ws = "a" + char(11).to_string() + "b" + char(12).to_string() + "c"
	let vf_words = vf_ws.split_whitespace()
	assert(vf_words.len() == 3, "split_whitespace splits vertical tab and form feed")
	assert(vf_words[0] == "a" and vf_words[1] == "b" and vf_words[2] == "c", "vt/ff words match")

	let only_ws = " \t\r\n" + char(11).to_string() + char(12).to_string()
	assert(only_ws.is_whitespace(), "all ascii whitespace chars match is_whitespace")
	assert(only_ws.split_whitespace().is_empty(), "all-whitespace splits to empty")
	assert("".split_whitespace().is_empty(), "empty splits to empty")

	let utf8_words = "  α\tβ  ".split_whitespace()
	assert(utf8_words.len() == 2 and utf8_words[0] == "α" and utf8_words[1] == "β", "utf8 tokens preserved")

	let lns = "line1\r\nline2\nline3".lines()
	assert(lns.len() == 3, "lines count")
	assert(lns[0] == "line1", "line 0")
	assert(lns[1] == "line2", "line 1")
	assert(lns[2] == "line3", "line 2")

	let joined = join(["a", "b", "c"], "::")
	assert(joined == "a::b::c", "join strings")

	let chars = "cat".chars_list()
	assert(chars.len() == 3, "chars_list len")
	assert(chars[0] == 'c' and chars[1] == 'a' and chars[2] == 't', "chars_list values")

	let bl = "ABC".bytes_list()
	assert(bl.len() == 3, "bytes_list len")
	assert(bl[0] == u8(65) and bl[1] == u8(66) and bl[2] == u8(67), "bytes_list values")
}

@test("ByteBuffer reads and writes little- and big-endian integers")
bind test_byte_buffer = fn(): {
	let b = ByteBuffer.new()
	b.write_u8(u8(0xAB))
	b.write_u16(u16(0x1234))
	b.write_u32(u32(0xDEADBEEF))
	b.write_u64(u64(0x0102030405060708))
	b.write_u16_be(u16(0x1234))
	b.write_u32_be(u32(7))
	b.write_string("hi")
	assert(b.len() == 1 + 2 + 4 + 8 + 2 + 4 + 2, "length")
	let raw = b.bytes()
	assert(raw[1] == u8(0x34) and raw[2] == u8(0x12), "u16 is little-endian")
	assert(raw[15] == u8(0x12) and raw[16] == u8(0x34), "u16_be is big-endian")

	assert(b.read_u8() == u8(0xAB), "u8")
	assert(b.read_u16() == u16(0x1234), "u16")
	assert(b.read_u32() == u32(0xDEADBEEF), "u32")
	assert(b.read_u64() == u64(0x0102030405060708), "u64")
	assert(b.read_u16_be() == u16(0x1234), "u16_be")
	assert(b.read_u32_be() == u32(7), "u32_be")
	assert(b.remaining() == 2, "two bytes left")
	assert(b.read_u32() == none, "not enough bytes for u32")
	assert(b.position() == 21, "a failed read does not move the cursor")
	let tail = b.read_bytes(2) ?? []
	assert(tail.len() == 2 and tail[0] == u8(104), "read_bytes")
	assert(b.read_u8() == none, "end of buffer")
	assert(b.seek(0) and b.read_u8() == u8(0xAB), "seek back to the start")
	assert(not b.seek(99), "seek out of range")

	let from = ByteBuffer.from_string("AB")
	assert(from.read_u16_be() == u16(0x4142), "from_string bytes")
}

@test("string.with_capacity and reserve preallocate without changing the text")
bind test_string_capacity = fn(): {
	let s = string.with_capacity(100)
	assert(s.is_empty() and s.cap() >= 100, "with_capacity reserves room")
	let before = s.cap()
	for(i in 0..100): s.push_byte(u8(97))
	assert(s.len() == 100 and s.cap() == before, "no reallocation within the reserved capacity")
	s.reserve(1000)
	assert(s.cap() >= 1100 and s.len() == 100, "reserve adds room after the current length")
	assert("literal".cap() == 0, "literals own no buffer")
	let t = "abc" + ""
	t.reserve(0)
	assert(t == "abc", "reserve keeps the contents")
}

@test("string.lines drops the final line terminator, not blank lines")
bind test_string_lines_trailing_newline = fn(): {
	assert(join_strings("a\nb\n".lines(), "|") == "a|b", "trailing newline ends the last line")
	assert(join_strings("a\r\nb\r\n".lines(), "|") == "a|b", "trailing CRLF too")
	assert(join_strings("a\nb".lines(), "|") == "a|b", "no trailing newline")
	assert(join_strings("a\nb\n\n".lines(), "|") == "a|b|", "a blank last line is kept")
	assert("".lines().is_empty(), "empty text has no lines")
	assert(join_strings("\n".lines(), "|") == "", "a lone newline is one empty line")
	assert("\n".lines().len() == 1, "a lone newline is one empty line")
}
