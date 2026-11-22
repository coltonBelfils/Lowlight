// std/chars — ASCII/Unicode scalar [`char`] classification, case folding, and radix conversion.
//
// Core classification helpers (`char.is_digit`, `char.is_alpha`, `char.is_alnum`,
// `char.is_whitespace`, and `char.to_int`) live in the [`std/core/primitives`] prelude.

// Reports whether this character lies within the 7-bit ASCII range (`0..=127`).
bind char.is_ascii = fn(self) bool: int(self) >= 0 and int(self) <= 127

// Reports whether this character is an uppercase ASCII letter (`'A'..='Z'`).
bind char.is_ascii_upper = fn(self) bool: self >= 'A' and self <= 'Z'

// Reports whether this character is a lowercase ASCII letter (`'a'..='z'`).
bind char.is_ascii_lower = fn(self) bool: self >= 'a' and self <= 'z'

// Converts an ASCII lowercase letter (`'a'..='z'`) to uppercase (`'A'..='Z'`), leaving other characters unchanged.
bind char.to_ascii_upper = fn(self) char:
	if(self.is_ascii_lower()): char(int(self) - 32) else: self

// Converts an ASCII uppercase letter (`'A'..='Z'`) to lowercase (`'a'..='z'`), leaving other characters unchanged.
bind char.to_ascii_lower = fn(self) char:
	if(self.is_ascii_upper()): char(int(self) + 32) else: self

// Reports whether this character is an ASCII control codepoint (`0..=31` or `127` DEL).
bind char.is_control = fn(self) bool: int(self) >= 0 and (int(self) < 32 or int(self) == 127)

// Reports whether this character is an ASCII punctuation mark or symbol.
bind char.is_punctuation = fn(self) bool: {
	let cp = int(self)
	(cp >= 33 and cp <= 47) or (cp >= 58 and cp <= 64) or (cp >= 91 and cp <= 96) or (cp >= 123 and cp <= 126)
}

// Converts this character to an integer digit (`0..radix`) in base `radix` (`2..=36`), or returns `none`.
//
// Accepts `'0'..'9'`, `'a'..'z'`, and `'A'..'Z'`. Returns `none` if `radix` is out of `2..=36`
// or the character does not represent a digit smaller than `radix`.
bind char.to_digit = fn(self, radix: int) ?int: {
	if(radix < 2 or radix > 36): func <- none
	let val = if(self >= '0' and self <= '9'): int(self) - 48
	else if(self >= 'a' and self <= 'z'): int(self) - 97 + 10
	else if(self >= 'A' and self <= 'Z'): int(self) - 65 + 10
	else: func <- none
	if(val < radix): val else: none
}

// Converts a numeric digit value (`0..radix`) into its lowercase ASCII [`char`] representation.
//
// Returns `none` if `radix` is outside `2..=36` or `digit` is negative / `>= radix`.
bind from_digit = fn(digit: int, radix: int) ?char: {
	if(radix < 2 or radix > 36 or digit < 0 or digit >= radix): func <- none
	if(digit < 10): char(48 + digit) else: char(97 + digit - 10)
}

// Returns the UTF-8 encoded byte length (`1..=4`) of this codepoint, or `0` for surrogates/out-of-range values.
bind char.len_utf8 = fn(self) int: {
	let cp = int(self)
	if(cp < 0 or (cp >= 55296 and cp <= 57343) or cp > 1114111): 0
	else if(cp <= 127): 1
	else if(cp <= 2047): 2
	else if(cp <= 65535): 3
	else: 4
}

// -----------------------------------------------------------------------------
// Standalone Companion Functions
// -----------------------------------------------------------------------------

// Function form of [`char.is_digit`].
bind is_digit = fn(c: char) bool: c.is_digit()

// Function form of [`char.is_alpha`].
bind is_alpha = fn(c: char) bool: c.is_alpha()

// Function form of [`char.is_alnum`].
bind is_alnum = fn(c: char) bool: c.is_alnum()

// Function form of [`char.is_whitespace`].
bind is_whitespace = fn(c: char) bool: c.is_whitespace()

// Function form of [`char.is_ascii`].
bind is_ascii = fn(c: char) bool: c.is_ascii()

// Function form of [`char.is_ascii_upper`].
bind is_ascii_upper = fn(c: char) bool: c.is_ascii_upper()

// Function form of [`char.is_ascii_lower`].
bind is_ascii_lower = fn(c: char) bool: c.is_ascii_lower()

// Function form of [`char.to_ascii_upper`].
bind to_ascii_upper = fn(c: char) char: c.to_ascii_upper()

// Function form of [`char.to_ascii_lower`].
bind to_ascii_lower = fn(c: char) char: c.to_ascii_lower()

// Function form of [`char.is_control`].
bind is_control = fn(c: char) bool: c.is_control()

// Function form of [`char.is_punctuation`].
bind is_punctuation = fn(c: char) bool: c.is_punctuation()

// -----------------------------------------------------------------------------
// Inline Tests for std/chars.ll
// -----------------------------------------------------------------------------

@test("character classification: digits, letters, alnum, whitespace, control, punct")
bind test_char_classification = fn(): {
	// Digits
	assert('0'.is_digit())
	assert('5'.is_digit())
	assert('9'.is_digit())
	assert(not '/'.is_digit())
	assert(not ':'.is_digit())
	assert(not 'a'.is_digit())
	assert(is_digit('7'))
	assert(not is_digit('x'))

	// Alphabetic
	assert('a'.is_alpha())
	assert('z'.is_alpha())
	assert('A'.is_alpha())
	assert('Z'.is_alpha())
	assert(not '0'.is_alpha())
	assert(not '@'.is_alpha())
	assert(not '['.is_alpha())
	assert(is_alpha('M'))
	assert(not is_alpha('!'))

	// Alphanumeric
	assert('a'.is_alnum())
	assert('Z'.is_alnum())
	assert('3'.is_alnum())
	assert(not '_'.is_alnum())
	assert(not ' '.is_alnum())
	assert(is_alnum('9'))

	// Whitespace
	assert(' '.is_whitespace())
	assert('\t'.is_whitespace())
	assert('\n'.is_whitespace())
	assert('\r'.is_whitespace())
	assert(char(11).is_whitespace())
	assert(char(12).is_whitespace())
	assert(not 'a'.is_whitespace())
	assert(not '_'.is_whitespace())
	assert(is_whitespace(' '))

	// ASCII range
	assert('A'.is_ascii())
	assert(char(0).is_ascii())
	assert(char(127).is_ascii())
	assert(not char(128).is_ascii())
	assert(not char(256).is_ascii())

	// ASCII case predicates
	assert('A'.is_ascii_upper())
	assert('Z'.is_ascii_upper())
	assert(not 'a'.is_ascii_upper())
	assert(not '0'.is_ascii_upper())
	assert('a'.is_ascii_lower())
	assert('z'.is_ascii_lower())
	assert(not 'A'.is_ascii_lower())
	assert(not '0'.is_ascii_lower())

	// Control & Punctuation
	assert(char(0).is_control())
	assert(char(31).is_control())
	assert(char(127).is_control())
	assert(not ' '.is_control())
	assert(not 'A'.is_control())
	assert('!'.is_punctuation())
	assert('.'.is_punctuation())
	assert(','.is_punctuation())
	assert(';'.is_punctuation())
	assert(not 'a'.is_punctuation())
	assert(not '1'.is_punctuation())
	assert(not ' '.is_punctuation())
}

@test("character case transformations")
bind test_char_case_conversion = fn(): {
	assert('a'.to_ascii_upper() == 'A')
	assert('z'.to_ascii_upper() == 'Z')
	assert('A'.to_ascii_upper() == 'A')
	assert('1'.to_ascii_upper() == '1')
	assert('!'.to_ascii_upper() == '!')
	assert(to_ascii_upper('k') == 'K')

	assert('A'.to_ascii_lower() == 'a')
	assert('Z'.to_ascii_lower() == 'z')
	assert('a'.to_ascii_lower() == 'a')
	assert('9'.to_ascii_lower() == '9')
	assert('?'.to_ascii_lower() == '?')
	assert(to_ascii_lower('G') == 'g')
}

@test("character radix and digit conversion")
bind test_char_radix_conversion = fn(): {
	// char.to_digit
	assert('0'.to_digit(10) == 0)
	assert('5'.to_digit(10) == 5)
	assert('9'.to_digit(10) == 9)
	assert('a'.to_digit(10) == none)
	assert('a'.to_digit(16) == 10)
	assert('f'.to_digit(16) == 15)
	assert('F'.to_digit(16) == 15)
	assert('g'.to_digit(16) == none)
	assert('z'.to_digit(36) == 35)
	assert('Z'.to_digit(36) == 35)
	assert('0'.to_digit(1) == none)
	assert('0'.to_digit(37) == none)

	// from_digit
	assert(from_digit(0, 10) == '0')
	assert(from_digit(7, 10) == '7')
	assert(from_digit(9, 10) == '9')
	assert(from_digit(10, 16) == 'a')
	assert(from_digit(15, 16) == 'f')
	assert(from_digit(35, 36) == 'z')
	assert(from_digit(10, 10) == none)
	assert(from_digit(2, 2) == none)
	assert(from_digit(-1, 10) == none)
	assert(from_digit(5, 1) == none)
	assert(from_digit(5, 37) == none)
}

@test("character UTF-8 encoded byte lengths")
bind test_char_utf8_length = fn(): {
	assert('a'.len_utf8() == 1)
	assert(char(0).len_utf8() == 1)
	assert(char(127).len_utf8() == 1)

	assert(char(128).len_utf8() == 2)
	assert(char(2047).len_utf8() == 2)

	assert(char(2048).len_utf8() == 3)
	assert(char(65535).len_utf8() == 3)

	assert(char(65536).len_utf8() == 4)
	assert(char(1114111).len_utf8() == 4)

	// Surrogates (0xD800..=0xDFFF) and values above 0x10FFFF cannot be chars: `char(n)` panics
	// for them, so every char has a UTF-8 length of 1 to 4.
	assert(char(55295).len_utf8() == 3 and char(57344).len_utf8() == 3, "neighbors of the surrogate range")
}
