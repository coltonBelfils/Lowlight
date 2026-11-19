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
bind string.is_whitespace = (self) bool: {
	if(self.len() == 0): func <- false
	for(ch in self.chars()): if(not ch.is_whitespace()): func <- false
	true
}

// Reports whether every byte in the string is valid 7-bit ASCII (`<= 127`).
bind string.is_ascii = (self) bool: {
	for(i in 0..self.len()): if(self[i] > 127): func <- false
	true
}

// -----------------------------------------------------------------------------
// Extended Substring Search
// -----------------------------------------------------------------------------

bind _matches_at = (s: string, pos: int, sub: string) bool: {
	let sub_len = sub.len()
	let j = 0
	for(j < sub_len): {
		if(s.byte_at(pos + j) != sub.byte_at(j)): func <- false
		j += 1
	}
	true
}

// Returns the byte offset of the last occurrence of `sub`, or `none` if absent (returns `self.len()` when `sub` is `""`).
bind string.last_index_of = (self, sub: string) ?int: {
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
bind string.count = (self, sub: string) int: {
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
bind string.capitalize = (self) string: {
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
bind string.repeat = (self, count: int) string: {
	if(count <= 0 or self.len() == 0): func <- ""
	let out = ""
	for(i in 0..count): out.push(self)
	out
}

// Returns a new string with bytes reversed in order.
bind string.reverse = (self) string: {
	if(self.len() <= 1): func <- self.copy()
	let out = ""
	let i = self.len() - 1
	while(i >= 0): {
		out.push_byte(self[i])
		i -= 1
	}
	out
}

// -----------------------------------------------------------------------------
// Padding & Alignment
// -----------------------------------------------------------------------------

// Left-pads the string with repeated `pad_char` until its byte length reaches at least `total_len`.
bind string.pad_left_char = (self, total_len: int, pad_char: char) string: {
	if(self.len() >= total_len): func <- self.copy()
	let pad_count = total_len - self.len()
	let pad_str = pad_char.to_string().repeat(pad_count)
	let out = ""
	out.push(pad_str)
	out.push(self)
	out
}

// Right-pads the string with repeated `pad_char` until its byte length reaches at least `total_len`.
bind string.pad_right_char = (self, total_len: int, pad_char: char) string: {
	if(self.len() >= total_len): func <- self.copy()
	let pad_count = total_len - self.len()
	let pad_str = pad_char.to_string().repeat(pad_count)
	let out = ""
	out.push(self)
	out.push(pad_str)
	out
}

// Centers the string within `total_len` bytes by padding evenly on both sides with `pad_char`.
bind string.center_char = (self, total_len: int, pad_char: char) string: {
	if(self.len() >= total_len): func <- self.copy()
	let total_pad = total_len - self.len()
	let left_pad = total_pad / 2
	let right_pad = total_pad - left_pad
	let out = ""
	out.push(pad_char.to_string().repeat(left_pad))
	out.push(self)
	out.push(pad_char.to_string().repeat(right_pad))
	out
}

// -----------------------------------------------------------------------------
// Splitting, Tokenization, & Joining
// -----------------------------------------------------------------------------

// Splits the string on runs of ASCII whitespace into non-empty token substrings.
bind string.split_whitespace = (self) [string]: {
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

// Splits the string on `\n` line endings while stripping any trailing `\r` from CRLF lines.
bind string.lines = (self) [string]: {
	let raw_lines = self.split("\n")
	let out: [string] = []
	for(line in raw_lines):
		if(line.ends_with("\r")): out.push(line[0..line.len() - 1].to_string())
		else: out.push(line.copy())
	out
}

// Concatenates `list` elements separated by `sep` (convenience wrapper around [`join_strings`]).
bind join = (list: [string], sep: string) string: join_strings(list, sep)

// Returns all decoded Unicode [`char`] codepoints in this string (alias for [`string.chars`]).
bind string.chars_list = (self) [char]: self.chars()

// Returns all raw UTF-8 bytes in this string (alias for [`string.bytes`]).
bind string.bytes_list = (self) [u8]: self.bytes()

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("string predicates: is_empty, is_whitespace, is_ascii")
bind test_strings_predicates = (): {
	assert("".is_empty(), "empty string is_empty")
	assert(not "hello".is_empty(), "non-empty string is not is_empty")

	assert(" \t\r\n ".is_whitespace(), "whitespace string")
	assert(not "".is_whitespace(), "empty is not whitespace")
	assert(not "  hello  ".is_whitespace(), "mixed is not whitespace")

	assert("hello 123!".is_ascii(), "ascii string")
	assert("".is_ascii(), "empty string is ascii")
}

@test("string search: starts_with, ends_with, index_of, last_index_of, contains, count")
bind test_strings_search = (): {
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
bind test_strings_trim_pad = (): {
	assert("  \t hello \n ".trim_start() == "hello \n ", "trim_start")
	assert("  \t hello \n ".trim_end() == "  \t hello", "trim_end")
	assert("  \t hello \n ".trim() == "hello", "trim")

	assert("abc".pad_left_char(6, '0') == "000abc", "pad_left_char")
	assert("abc".pad_left_char(2, '0') == "abc", "pad_left_char no-op when shorter")
	assert("abc".pad_right_char(6, '-') == "abc---", "pad_right_char")
	assert("abc".pad_right_char(3, '-') == "abc", "pad_right_char no-op")
	assert("abc".center_char(7, '=') == "==abc==", "center_char odd")
	assert("abc".center_char(6, '=') == "=abc==", "center_char even")
}

@test("string transformations: case, repeat, reverse, replace")
bind test_strings_transform = (): {
	assert("Hello, World!".to_upper() == "HELLO, WORLD!", "to_upper")
	assert("Hello, World!".to_lower() == "hello, world!", "to_lower")
	assert("hello world".capitalize() == "Hello world", "capitalize lower")
	assert("HELLO WORLD".capitalize() == "Hello world", "capitalize upper")

	assert("ab".repeat(3) == "ababab", "repeat 3")
	assert("xyz".repeat(0) == "", "repeat 0")

	assert("hello".reverse() == "olleh", "reverse string")
	assert("".reverse() == "", "reverse empty")

	assert("hello world world".replace("world", "there") == "hello there world", "replace first")
	assert("banana".replace_all("an", "XX") == "bXXXXa", "replace_all")
	assert("hello".replace("xyz", "abc") == "hello", "replace not found")
}

@test("string tokenization and joining: split, lines, join, chars/bytes")
bind test_strings_split_join = (): {
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

	let cl = "cat".chars_list()
	assert(cl.len() == 3, "chars_list len")
	assert(cl[0] == 'c' and cl[1] == 'a' and cl[2] == 't', "chars_list values")

	let bl = "ABC".bytes_list()
	assert(bl.len() == 3, "bytes_list len")
	assert(bl[0] == u8(65) and bl[1] == u8(66) and bl[2] == u8(67), "bytes_list values")
}
