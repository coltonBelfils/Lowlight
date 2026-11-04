// Lowlight Standard Library: std/strfmt.ll
// String formatting, alignment, padding, case conversions, and tabular layout.
// Written predominantly in pure Lowlight for zero GC overhead and native C speed.

extern ll_fmt_float_prec: (v: float, prec: int): string

// -----------------------------------------------------------------------------
// Number & Data Formatting Methods on Primitive Types (float & int)
// -----------------------------------------------------------------------------

bind float.format_float = (self, precision: int): string {
    if (precision < 0) {
        "$self"
    } else {
        unsafe ll_fmt_float_prec(self, precision)
    }
}

bind float.format = (self, precision: int): string {
    self.format_float(precision)
}

bind int.format_int = (self): string {
    "$self"
}

bind int.format_commas = (self): string {
    if (self == -9223372036854775807 - 1) {
        func <- "-9,223,372,036,854,775,808"
    }
    let is_neg = self < 0
    let abs_val = if (is_neg) -self else self
    let s = "$abs_val"
    let n = s.len()
    if (n <= 3) {
        if (is_neg) "-$s" else s
    } else {
        let out = if (is_neg) "-" else ""
        let rem = n % 3
        if (rem > 0) {
            out.push(s[0..rem])
            if (rem < n) out.push(",")
        }
        let i = rem
        for (i < n) {
            out.push(s[i..(i + 3)])
            i += 3
            if (i < n) out.push(",")
        }
        out
    }
}

bind int.format_int_commas = (self): string {
    self.format_commas()
}

bind float.format_commas = (self, precision: int): string {
    let s = self.format_float(precision)
    let dot_pos? = s.index_of(".")
    if (dot_pos? as dot) {
        let int_part = s[0..dot]
        let frac_part = s[dot..s.len()]
        let is_neg = int_part.starts_with("-")
        let digits = if (is_neg) int_part[1..int_part.len()] else int_part
        let n = digits.len()
        let out = if (is_neg) "-" else ""
        let rem = n % 3
        if (rem > 0) {
            out.push(digits[0..rem])
            if (rem < n) out.push(",")
        }
        let i = rem
        for (i < n) {
            out.push(digits[i..(i + 3)])
            i += 3
            if (i < n) out.push(",")
        }
        out.push(frac_part)
        out
    } else {
        let is_neg = s.starts_with("-")
        let digits = if (is_neg) s[1..s.len()] else s
        let n = digits.len()
        let out = if (is_neg) "-" else ""
        let rem = n % 3
        if (rem > 0) {
            out.push(digits[0..rem])
            if (rem < n) out.push(",")
        }
        let i = rem
        for (i < n) {
            out.push(digits[i..(i + 3)])
            i += 3
            if (i < n) out.push(",")
        }
        out
    }
}

bind float.format_float_commas = (self, precision: int): string {
    self.format_commas(precision)
}

bind int.format_bytes = (self): string {
    if (self < 1024) {
        "${self} B"
    } else if (self < 1048576) {
        let kb = float(self) / 1024.0
        "${kb.format_float(2)} KB"
    } else if (self < 1073741824) {
        let mb = float(self) / 1048576.0
        "${mb.format_float(2)} MB"
    } else {
        let gb = float(self) / 1073741824.0
        "${gb.format_float(2)} GB"
    }
}

bind float.format_percent = (self, precision: int): string {
    let pct = self * 100.0
    "${pct.format_float(precision)}%"
}

bind float.format_currency = (self, symbol: string, precision: int): string {
    let formatted = self.format_commas(precision)
    "$symbol$formatted"
}

// Standalone function wrappers for backwards-compatibility
bind format_float = (v: float, precision: int): string {
    v.format_float(precision)
}

bind format_int = (v: int): string {
    v.format_int()
}

bind format_int_commas = (v: int): string {
    v.format_commas()
}

bind format_float_commas = (v: float, precision: int): string {
    v.format_commas(precision)
}

bind format_bytes = (bytes: int): string {
    bytes.format_bytes()
}

bind format_percent = (v: float, precision: int): string {
    v.format_percent(precision)
}

bind format_currency = (amount: float, symbol: string, precision: int): string {
    amount.format_currency(symbol, precision)
}

// -----------------------------------------------------------------------------
// Integer Padding Functions & Methods
// -----------------------------------------------------------------------------

bind pad_int_zeros = (val: int, width: int): string {
    let s = "$val"
    let diff = width - s.len()
    if (diff <= 0) func <- s
    let out = ""
    for (i in 0..diff) {
        out.push("0")
    }
    out.push(s)
    out
}

bind pad_int_spaces = (val: int, width: int): string {
    let s = "$val"
    let diff = width - s.len()
    if (diff <= 0) func <- s
    let out = ""
    for (i in 0..diff) {
        out.push(" ")
    }
    out.push(s)
    out
}

bind int.pad_zeros = (self, width: int): string {
    pad_int_zeros(self, width)
}

bind int.pad_spaces = (self, width: int): string {
    pad_int_spaces(self, width)
}

// -----------------------------------------------------------------------------
// String Extension Methods (Uniform Method Call Syntax)
// -----------------------------------------------------------------------------

bind string.pad_left_fill = (self, width: int, fill: string): string {
    let diff = width - self.len()
    if (diff <= 0) func <- self.copy()
    let fill_char = if (fill.len() > 0) fill[0..1] else " "
    let out = ""
    for (i in 0..diff) {
        out.push(fill_char)
    }
    out.push(self)
    out
}

bind string.pad_left = (self, width: int): string {
    self.pad_left_fill(width, " ")
}

bind string.pad_right_fill = (self, width: int, fill: string): string {
    let diff = width - self.len()
    if (diff <= 0) func <- self.copy()
    let fill_char = if (fill.len() > 0) fill[0..1] else " "
    let out = self.copy()
    for (i in 0..diff) {
        out.push(fill_char)
    }
    out
}

bind string.pad_right = (self, width: int): string {
    self.pad_right_fill(width, " ")
}

bind string.center_fill = (self, width: int, fill: string): string {
    let diff = width - self.len()
    if (diff <= 0) func <- self.copy()
    let left_pad = diff / 2
    let right_pad = diff - left_pad
    let fill_char = if (fill.len() > 0) fill[0..1] else " "
    let out = ""
    for (i in 0..left_pad) {
        out.push(fill_char)
    }
    out.push(self)
    for (i in 0..right_pad) {
        out.push(fill_char)
    }
    out
}

bind string.center = (self, width: int): string {
    self.center_fill(width, " ")
}

bind string.truncate_with = (self, max_len: int, ellipsis: string): string {
    if (self.len() <= max_len) {
        self.copy()
    } else if (max_len <= ellipsis.len()) {
        self[0..max_len]
    } else {
        let keep = max_len - ellipsis.len()
        let out = self[0..keep]
        out.push(ellipsis)
        out
    }
}

bind string.truncate = (self, max_len: int): string {
    self.truncate_with(max_len, "...")
}

bind string.repeat = (self, count: int): string {
    if (count <= 0) func <- ""
    let out = ""
    for (i in 0..count) {
        out.push(self)
    }
    out
}

bind string.reverse = (self): string {
    let out = ""
    let n = self.len()
    let i = n - 1
    for (i >= 0) {
        out.push(self[i..(i + 1)])
        i -= 1
    }
    out
}

bind string.to_upper = (self): string {
    let out = ""
    for (ch in self) {
        if (ch >= 'a' and ch <= 'z') {
            out.push_char(char(int(ch) - 32))
        } else {
            out.push_char(ch)
        }
    }
    out
}

bind string.to_lower = (self): string {
    let out = ""
    for (ch in self) {
        if (ch >= 'A' and ch <= 'Z') {
            out.push_char(char(int(ch) + 32))
        } else {
            out.push_char(ch)
        }
    }
    out
}

bind string.to_title = (self): string {
    let out = ""
    let capitalize_next = true
    for (ch in self) {
        if (ch == ' ' or ch == '\t' or ch == '\n' or ch == '_' or ch == '-') {
            capitalize_next = true
            out.push_char(ch)
        } else if (capitalize_next) {
            capitalize_next = false
            let c = if (ch >= 'a' and ch <= 'z') char(int(ch) - 32) else ch
            out.push_char(c)
        } else {
            let c = if (ch >= 'A' and ch <= 'Z') char(int(ch) + 32) else ch
            out.push_char(c)
        }
    }
    out
}

bind string.to_snake_case = (self): string {
    let out = ""
    let prev_was_sep = true
    for (i, ch in self) {
        if (ch == ' ' or ch == '-' or ch == '_') {
            if (not prev_was_sep and out.len() > 0) {
                out.push("_")
            }
            prev_was_sep = true
        } else if (ch >= 'A' and ch <= 'Z') {
            if (not prev_was_sep and out.len() > 0) {
                out.push("_")
            }
            out.push_char(char(int(ch) + 32))
            prev_was_sep = false
        } else {
            out.push_char(ch)
            prev_was_sep = false
        }
    }
    out
}

bind string.to_kebab_case = (self): string {
    let s = self.to_snake_case()
    s.replace("_", "-")
}

bind string.to_camel_case = (self): string {
    let out = ""
    let upper_next = false
    let is_first = true
    for (ch in self) {
        if (ch == ' ' or ch == '_' or ch == '-') {
            upper_next = true
        } else if (is_first) {
            is_first = false
            upper_next = false
            let c = if (ch >= 'A' and ch <= 'Z') char(int(ch) + 32) else ch
            out.push_char(c)
        } else if (upper_next) {
            upper_next = false
            let c = if (ch >= 'a' and ch <= 'z') char(int(ch) - 32) else ch
            out.push_char(c)
        } else {
            out.push_char(ch)
        }
    }
    out
}

bind string.quote = (self): string {
    let out = "\""
    for (ch in self) {
        if (ch == '"') {
            out.push("\\\"")
        } else if (ch == '\\') {
            out.push("\\\\")
        } else if (ch == '\n') {
            out.push("\\n")
        } else if (ch == '\t') {
            out.push("\\t")
        } else if (ch == '\r') {
            out.push("\\r")
        } else {
            out.push_char(ch)
        }
    }
    out.push("\"")
    out
}

bind string.unquote = (self): ?string {
    let trimmed = self.trim()
    if (trimmed.len() < 2) func <- none
    let first = trimmed[0..1]
    let last = trimmed[(trimmed.len() - 1)..trimmed.len()]
    if (first != "\"" or last != "\"") func <- none
    let inner = trimmed[1..(trimmed.len() - 1)]
    let out = ""
    let escape = false
    for (ch in inner) {
        if (escape) {
            escape = false
            if (ch == 'n') {
                out.push("\n")
            } else if (ch == 't') {
                out.push("\t")
            } else if (ch == 'r') {
                out.push("\r")
            } else if (ch == '"') {
                out.push("\"")
            } else if (ch == '\\') {
                out.push("\\")
            } else {
                out.push_char(ch)
            }
        } else if (ch == '\\') {
            escape = true
        } else {
            out.push_char(ch)
        }
    }
    out
}

bind string.indent = (self, spaces: int): string {
    if (spaces <= 0) func <- self.copy()
    let prefix = ""
    for (i in 0..spaces) {
        prefix.push(" ")
    }
    let lines = self.split("\n")
    let out_lines: [string] = []
    for (line in lines) {
        if (line.len() > 0) {
            out_lines.push("$prefix$line")
        } else {
            out_lines.push("")
        }
    }
    out_lines.join("\n")
}

bind string.count_occurrences = (self, sub: string): int {
    if (sub.len() == 0 or self.len() < sub.len()) func <- 0
    let count = 0
    let cur = self.copy()
    for (true) {
        let idx? = cur.index_of(sub)
        if (idx? as i) {
            count += 1
            let next_start = i + sub.len()
            if (next_start >= cur.len()) {
                for <- count
            }
            cur = cur[next_start..cur.len()]
        } else {
            for <- count
        }
    }
    count
}
