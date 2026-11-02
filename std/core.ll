// Lowlight Standard Library: std/core.ll
// The foundational core prelude: interfaces, primitive methods, and essential utilities.
// This module is implicitly imported into every Lowlight source file.

// -----------------------------------------------------------------------------
// Core Interfaces
// -----------------------------------------------------------------------------

// Printable: Universal interface for types that can produce a human-readable string.
@lang("printable")
define Printable interface [
    to_string: (self): string,
]

// Copyable: Universal interface for types that can explicitly duplicate their state.
@lang("copyable")
define Copyable interface [
    copy: (self): Self,
]

// Drop: Universal interface for types requiring custom deterministic cleanup when falling out of scope.
@lang("drop")
define Drop interface [
    drop: (&self): void,
]

// -----------------------------------------------------------------------------
// Primitive String Conversions (Printable Implementations)
// -----------------------------------------------------------------------------

extern ll_fmt_float_prec: (v: float, prec: int): string

// Converts an integer to its decimal string representation in 100% pure Lowlight.
bind int.to_string = (self): string {
    if (self == 0) func <- "0"
    if (self == -9223372036854775807 - 1) {
        func <- "-9223372036854775808"
    }
    let is_neg = self < 0
    let n = if (is_neg) -self else self
    let reversed = ""
    for (n > 0) {
        let digit = n % 10
        let b = u8(48 + digit)
        reversed.push_byte(b)
        n /= 10
    }
    let out = if (is_neg) "-" else ""
    let i = reversed.len() - 1
    for (i >= 0) {
        out.push_byte(reversed.byte_at(i))
        i -= 1
    }
    out
}

// Converts a boolean to "true" or "false".
bind bool.to_string = (self): string {
    if (self) "true" else "false"
}

// String itself satisfies Printable by returning a copy of itself.
bind string.to_string = (self): string {
    self.copy()
}

// Converts a unicode character code point into a single-character string.
bind char.to_string = (self): string {
    let s = ""
    s.push_char(self)
    s
}

// Converts a 64-bit floating point number to its standard decimal string representation.
bind float.to_string = (self): string {
    unsafe ll_fmt_float_prec(self, -1)
}

// Fixed-width integer conversions to decimal strings.
bind u8.to_string = (self): string {
    int(self).to_string()
}

bind u16.to_string = (self): string {
    int(self).to_string()
}

bind u32.to_string = (self): string {
    int(self).to_string()
}

bind u64.to_string = (self): string {
    if (self == 0) func <- "0"
    let n = self
    let reversed = ""
    for (n > 0) {
        let digit = n % 10
        let b = u8(48 + digit)
        reversed.push_byte(b)
        n /= 10
    }
    let out = ""
    let i = reversed.len() - 1
    for (i >= 0) {
        out.push_byte(reversed.byte_at(i))
        i -= 1
    }
    out
}

bind i8.to_string = (self): string {
    int(self).to_string()
}

bind i16.to_string = (self): string {
    int(self).to_string()
}

bind i32.to_string = (self): string {
    int(self).to_string()
}

// -----------------------------------------------------------------------------
// Core Utility Primitives
// -----------------------------------------------------------------------------

// Returns the smaller of two comparable values.
bind min[T: Copyable] = (a: T, b: T): T {
    if (a < b) a.copy() else b.copy()
}

// Returns the larger of two comparable values.
bind max[T: Copyable] = (a: T, b: T): T {
    if (a > b) a.copy() else b.copy()
}

// Returns the absolute value of a signed integer.
bind abs = (x: int): int {
    if (x < 0) -x else x
}

// Clamps a value within the range [low, high].
bind clamp[T: Copyable] = (val: T, low: T, high: T): T {
    if (val < low) low.copy()
    else if (val > high) high.copy()
    else val.copy()
}
