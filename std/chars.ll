// Lowlight Standard Library: std/chars.ll
// Unicode character (codepoint) classification, inspection, and case transformations.

// -----------------------------------------------------------------------------
// Character Classification Methods
// -----------------------------------------------------------------------------

// Returns true if the character is an ASCII decimal digit ('0'..'9').
bind char.is_digit = (self): bool {
    self >= '0' and self <= '9'
}

// Returns true if the character is an ASCII alphabetic letter ('a'..'z' or 'A'..'Z').
bind char.is_alpha = (self): bool {
    (self >= 'a' and self <= 'z') or (self >= 'A' and self <= 'Z')
}

// Returns true if the character is an ASCII alphanumeric character (letter or digit).
bind char.is_alnum = (self): bool {
    self.is_alpha() or self.is_digit()
}

// Returns true if the character is an ASCII whitespace character (space, tab, newline, carriage return, vertical tab, form feed).
bind char.is_whitespace = (self): bool {
    self == ' ' or self == '\t' or self == '\n' or self == '\r' or int(self) == 11 or int(self) == 12
}

// Returns true if the character is within the 7-bit ASCII range (0..127).
bind char.is_ascii = (self): bool {
    int(self) >= 0 and int(self) <= 127
}

// Returns true if the character is an ASCII uppercase letter ('A'..'Z').
bind char.is_ascii_upper = (self): bool {
    self >= 'A' and self <= 'Z'
}

// Returns true if the character is an ASCII lowercase letter ('a'..'z').
bind char.is_ascii_lower = (self): bool {
    self >= 'a' and self <= 'z'
}

// Converts an ASCII lowercase letter ('a'..'z') to uppercase ('A'..'Z'). Non-lowercase characters are returned unchanged.
bind char.to_ascii_upper = (self): char {
    if (self.is_ascii_lower()) {
        char(int(self) - 32)
    } else {
        self
    }
}

// Converts an ASCII uppercase letter ('A'..'Z') to lowercase ('a'..'z'). Non-uppercase characters are returned unchanged.
bind char.to_ascii_lower = (self): char {
    if (self.is_ascii_upper()) {
        char(int(self) + 32)
    } else {
        self
    }
}

// Returns true if the character is an ASCII control character (0..31 or 127 DEL).
bind char.is_control = (self): bool {
    int(self) >= 0 and (int(self) < 32 or int(self) == 127)
}

// Returns true if the character is an ASCII punctuation mark or symbol.
bind char.is_punctuation = (self): bool {
    let cp = int(self)
    (cp >= 33 and cp <= 47) or (cp >= 58 and cp <= 64) or (cp >= 91 and cp <= 96) or (cp >= 123 and cp <= 126)
}

// Converts the character to an integer digit in the given base (2..36).
// Supports '0'..'9' for bases <= 10, and 'a'..'z' / 'A'..'Z' for bases up to 36.
// Returns `none` if the radix is invalid or the character is not a valid digit in that base.
bind char.to_digit = (self, radix: int): ?int {
    if (radix < 2 or radix > 36) func <- none
    let val = if (self >= '0' and self <= '9') {
        int(self) - 48
    } else if (self >= 'a' and self <= 'z') {
        int(self) - 97 + 10
    } else if (self >= 'A' and self <= 'Z') {
        int(self) - 65 + 10
    } else {
        func <- none
    }
    if (val < radix) val else none
}

// Converts an integer digit value (0..35) to its ASCII character representation in the given base.
// Returns `none` if the radix is invalid (< 2 or > 36) or the digit is out of range.
bind from_digit = (digit: int, radix: int): ?char {
    if (radix < 2 or radix > 36 or digit < 0 or digit >= radix) func <- none
    if (digit < 10) {
        char(48 + digit)
    } else {
        char(97 + digit - 10)
    }
}

// Returns the number of bytes required to encode this Unicode codepoint in UTF-8 (1, 2, 3, or 4).
// Returns 0 if the codepoint is outside the valid Unicode range (0..0x10FFFF) or is a surrogate (0xD800..0xDFFF).
bind char.len_utf8 = (self): int {
    let cp = int(self)
    if (cp < 0 or (cp >= 55296 and cp <= 57343) or cp > 1114111) {
        0
    } else if (cp <= 127) {
        1
    } else if (cp <= 2047) {
        2
    } else if (cp <= 65535) {
        3
    } else {
        4
    }
}

// -----------------------------------------------------------------------------
// Standalone Companion Functions
// -----------------------------------------------------------------------------

bind is_digit = (c: char): bool c.is_digit()
bind is_alpha = (c: char): bool c.is_alpha()
bind is_alnum = (c: char): bool c.is_alnum()
bind is_whitespace = (c: char): bool c.is_whitespace()
bind is_ascii = (c: char): bool c.is_ascii()
bind is_ascii_upper = (c: char): bool c.is_ascii_upper()
bind is_ascii_lower = (c: char): bool c.is_ascii_lower()
bind to_ascii_upper = (c: char): char c.to_ascii_upper()
bind to_ascii_lower = (c: char): char c.to_ascii_lower()
bind is_control = (c: char): bool c.is_control()
bind is_punctuation = (c: char): bool c.is_punctuation()

// -----------------------------------------------------------------------------
// Inline Tests for std/chars.ll
// -----------------------------------------------------------------------------

@test("character classification: digits, letters, alnum, whitespace, control, punct")
bind test_char_classification = () {
    // Digits
    assert(('0').is_digit())
    assert(('5').is_digit())
    assert(('9').is_digit())
    assert(not ('/').is_digit())
    assert(not (':').is_digit())
    assert(not ('a').is_digit())
    assert(is_digit('7'))
    assert(not is_digit('x'))

    // Alphabetic
    assert(('a').is_alpha())
    assert(('z').is_alpha())
    assert(('A').is_alpha())
    assert(('Z').is_alpha())
    assert(not ('0').is_alpha())
    assert(not ('@').is_alpha())
    assert(not ('[').is_alpha())
    assert(is_alpha('M'))
    assert(not is_alpha('!'))

    // Alphanumeric
    assert(('a').is_alnum())
    assert(('Z').is_alnum())
    assert(('3').is_alnum())
    assert(not ('_').is_alnum())
    assert(not (' ').is_alnum())
    assert(is_alnum('9'))

    // Whitespace
    assert((' ').is_whitespace())
    assert(('\t').is_whitespace())
    assert(('\n').is_whitespace())
    assert(('\r').is_whitespace())
    assert(char(11).is_whitespace())
    assert(char(12).is_whitespace())
    assert(not ('a').is_whitespace())
    assert(not ('_').is_whitespace())
    assert(is_whitespace(' '))

    // ASCII range
    assert(('A').is_ascii())
    assert(char(0).is_ascii())
    assert(char(127).is_ascii())
    assert(not char(128).is_ascii())
    assert(not char(256).is_ascii())

    // ASCII case predicates
    assert(('A').is_ascii_upper())
    assert(('Z').is_ascii_upper())
    assert(not ('a').is_ascii_upper())
    assert(not ('0').is_ascii_upper())
    assert(('a').is_ascii_lower())
    assert(('z').is_ascii_lower())
    assert(not ('A').is_ascii_lower())
    assert(not ('0').is_ascii_lower())

    // Control & Punctuation
    assert(char(0).is_control())
    assert(char(31).is_control())
    assert(char(127).is_control())
    assert(not (' ').is_control())
    assert(not ('A').is_control())
    assert(('!').is_punctuation())
    assert(('.').is_punctuation())
    assert((',').is_punctuation())
    assert((';').is_punctuation())
    assert(not ('a').is_punctuation())
    assert(not ('1').is_punctuation())
    assert(not (' ').is_punctuation())
}

@test("character case transformations")
bind test_char_case_conversion = () {
    assert(('a').to_ascii_upper() == 'A')
    assert(('z').to_ascii_upper() == 'Z')
    assert(('A').to_ascii_upper() == 'A')
    assert(('1').to_ascii_upper() == '1')
    assert(('!').to_ascii_upper() == '!')
    assert(to_ascii_upper('k') == 'K')

    assert(('A').to_ascii_lower() == 'a')
    assert(('Z').to_ascii_lower() == 'z')
    assert(('a').to_ascii_lower() == 'a')
    assert(('9').to_ascii_lower() == '9')
    assert(('?').to_ascii_lower() == '?')
    assert(to_ascii_lower('G') == 'g')
}

@test("character radix and digit conversion")
bind test_char_radix_conversion = () {
    // char.to_digit
    assert(('0').to_digit(10) == 0)
    assert(('5').to_digit(10) == 5)
    assert(('9').to_digit(10) == 9)
    assert(('a').to_digit(10) == none)
    assert(('a').to_digit(16) == 10)
    assert(('f').to_digit(16) == 15)
    assert(('F').to_digit(16) == 15)
    assert(('g').to_digit(16) == none)
    assert(('z').to_digit(36) == 35)
    assert(('Z').to_digit(36) == 35)
    assert(('0').to_digit(1) == none)
    assert(('0').to_digit(37) == none)

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
bind test_char_utf8_length = () {
    assert(('a').len_utf8() == 1)
    assert(char(0).len_utf8() == 1)
    assert(char(127).len_utf8() == 1)

    assert(char(128).len_utf8() == 2)
    assert(char(2047).len_utf8() == 2)

    assert(char(2048).len_utf8() == 3)
    assert(char(65535).len_utf8() == 3)

    assert(char(65536).len_utf8() == 4)
    assert(char(1114111).len_utf8() == 4)

    // Surrogates return 0
    assert(char(55296).len_utf8() == 0)
    assert(char(57343).len_utf8() == 0)

    // Out-of-bounds return 0
    assert(char(1114112).len_utf8() == 0)
    assert(char(-1).len_utf8() == 0)
}
