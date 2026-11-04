// Lowlight Standard Library: std/time.ll
// Robust monotonic timing, durations, DateTime manipulation, and multi-standard formatting.

import "std/strconv.ll"
import "std/strfmt.ll"

extern ll_time_monotonic_nanos: (): int
extern ll_time_sleep_ms: (ms: int)
extern ll_time_epoch_nanos_now: (): int
extern ll_time_breakdown: (epoch_sec: int, is_local: bool, out_fields: ptr[int], out_zone: ptr[u8], zone_len: int)
extern ll_time_make_epoch: (year: int, month: int, day: int, hour: int, minute: int, second: int, is_local: bool): int

// -----------------------------------------------------------------------------
// Duration & Instant
// -----------------------------------------------------------------------------

define Duration struct [
    nanos: int,
]

bind Duration.from_nanos = (nanos: int): Duration {
    Duration[nanos: nanos]
}

bind Duration.from_micros = (micros: int): Duration {
    Duration[nanos: micros * 1000]
}

bind Duration.from_millis = (millis: float): Duration {
    Duration[nanos: int(millis * 1000000.0)]
}

bind Duration.from_secs = (secs: float): Duration {
    Duration[nanos: int(secs * 1000000000.0)]
}

bind Duration.to_nanos = (self): int {
    self.nanos
}

bind Duration.to_micros = (self): int {
    self.nanos / 1000
}

bind Duration.to_millis = (self): float {
    float(self.nanos) / 1000000.0
}

bind Duration.to_secs = (self): float {
    float(self.nanos) / 1000000000.0
}

bind Duration.to_string = (self): string {
    if (self.nanos < 1000) {
        "${self.nanos} ns"
    } else if (self.nanos < 1000000) {
        let us = float(self.nanos) / 1000.0
        "${float_to_str(us, 2)} µs"
    } else if (self.nanos < 1000000000) {
        let ms = float(self.nanos) / 1000000.0
        "${float_to_str(ms, 2)} ms"
    } else {
        let s = float(self.nanos) / 1000000000.0
        "${float_to_str(s, 3)} s"
    }
}

define Instant struct [
    nanos: int,
]

bind Instant.now = (): Instant {
    Instant[nanos: unsafe ll_time_monotonic_nanos()]
}

bind Instant.elapsed = (self): Duration {
    let now = unsafe ll_time_monotonic_nanos()
    Duration[nanos: now - self.nanos]
}

bind Instant.elapsed_millis = (self): float {
    let d = self.elapsed()
    d.to_millis()
}

bind Instant.elapsed_nanos = (self): int {
    let d = self.elapsed()
    d.to_nanos()
}

bind now_nanos = (): int {
    unsafe ll_time_monotonic_nanos()
}

bind now_millis = (): float {
    float(unsafe ll_time_monotonic_nanos()) / 1000000.0
}

bind sleep_ms = (ms: int) {
    unsafe ll_time_sleep_ms(ms)
}

bind month_abbr = (m: int): string {
    if (m == 1) "Jan"
    else if (m == 2) "Feb"
    else if (m == 3) "Mar"
    else if (m == 4) "Apr"
    else if (m == 5) "May"
    else if (m == 6) "Jun"
    else if (m == 7) "Jul"
    else if (m == 8) "Aug"
    else if (m == 9) "Sep"
    else if (m == 10) "Oct"
    else if (m == 11) "Nov"
    else if (m == 12) "Dec"
    else ""
}

bind month_full = (m: int): string {
    if (m == 1) "January"
    else if (m == 2) "February"
    else if (m == 3) "March"
    else if (m == 4) "April"
    else if (m == 5) "May"
    else if (m == 6) "June"
    else if (m == 7) "July"
    else if (m == 8) "August"
    else if (m == 9) "September"
    else if (m == 10) "October"
    else if (m == 11) "November"
    else if (m == 12) "December"
    else ""
}

bind weekday_abbr = (w: int): string {
    if (w == 0) "Sun"
    else if (w == 1) "Mon"
    else if (w == 2) "Tue"
    else if (w == 3) "Wed"
    else if (w == 4) "Thu"
    else if (w == 5) "Fri"
    else if (w == 6) "Sat"
    else ""
}

bind weekday_full = (w: int): string {
    if (w == 0) "Sunday"
    else if (w == 1) "Monday"
    else if (w == 2) "Tuesday"
    else if (w == 3) "Wednesday"
    else if (w == 4) "Thursday"
    else if (w == 5) "Friday"
    else if (w == 6) "Saturday"
    else ""
}

bind format_tz_offset = (offset_secs: int, with_colon: bool): string {
    let is_neg = offset_secs < 0
    let total_mins = (if (is_neg) -offset_secs else offset_secs) / 60
    let hours = total_mins / 60
    let mins = total_mins % 60
    let sign = if (is_neg) "-" else "+"
    let h_str = pad_int_zeros(hours, 2)
    let m_str = pad_int_zeros(mins, 2)
    if (with_colon) {
        "$sign$h_str:$m_str"
    } else {
        "$sign$h_str$m_str"
    }
}

// -----------------------------------------------------------------------------
// DateTime Struct & Methods
// -----------------------------------------------------------------------------

define DateTime struct [
    year: int,
    month: int,        // 1..12
    day: int,          // 1..31
    hour: int,         // 0..23
    minute: int,       // 0..59
    second: int,       // 0..59
    nanosecond: int,   // 0..999,999,999
    weekday: int,      // 0=Sun..6=Sat
    yday: int,         // 1..366
    offset_secs: int,  // UTC offset in seconds
    zone_name: string, // e.g. "UTC", "PDT"
    unix_secs: int,    // seconds since Unix epoch
    is_local: bool,
]

bind build_datetime_from_epoch = (epoch_sec: int, nano: int, is_local: bool): DateTime {
    unsafe {
        let raw_fields = alloc_raw[int](9)
        let raw_zone = alloc_raw[u8](32)
        zero_bytes(raw_zone, 32)
        ll_time_breakdown(epoch_sec, is_local, raw_fields, raw_zone, 32)

        let yr = *ptr_add(raw_fields, 0)
        let mo = *ptr_add(raw_fields, 1)
        let dy = *ptr_add(raw_fields, 2)
        let hr = *ptr_add(raw_fields, 3)
        let mi = *ptr_add(raw_fields, 4)
        let sc = *ptr_add(raw_fields, 5)
        let wd = *ptr_add(raw_fields, 6)
        let yd = *ptr_add(raw_fields, 7)
        let off = *ptr_add(raw_fields, 8)

        // Read zone name string
        let zone = ""
        let zi = 0
        for (zi < 32) {
            let b = *ptr_add(raw_zone, zi)
            if (b == u8(0)) {
                for <-
            }
            zone.push_byte(b)
            zi += 1
        }
        if (zone.len() == 0) {
            zone = if (is_local) "LOCAL" else "UTC"
        }

        free_raw(raw_fields)
        free_raw(raw_zone)

        DateTime[
            year: yr,
            month: mo,
            day: dy,
            hour: hr,
            minute: mi,
            second: sc,
            nanosecond: nano,
            weekday: wd,
            yday: yd,
            offset_secs: off,
            zone_name: zone,
            unix_secs: epoch_sec,
            is_local: is_local,
        ]
    }
}

bind DateTime.now_utc = (): DateTime {
    let now_nanos = unsafe ll_time_epoch_nanos_now()
    let sec = now_nanos / 1000000000
    let nano = now_nanos % 1000000000
    build_datetime_from_epoch(sec, nano, false)
}

bind DateTime.now_local = (): DateTime {
    let now_nanos = unsafe ll_time_epoch_nanos_now()
    let sec = now_nanos / 1000000000
    let nano = now_nanos % 1000000000
    build_datetime_from_epoch(sec, nano, true)
}

bind DateTime.from_unix_secs = (secs: int, is_local: bool): DateTime {
    build_datetime_from_epoch(secs, 0, is_local)
}

bind DateTime.from_unix_millis = (ms: int, is_local: bool): DateTime {
    let sec = ms / 1000
    let nano = (ms % 1000) * 1000000
    build_datetime_from_epoch(sec, nano, is_local)
}

bind DateTime.from_unix_nanos = (nanos: int, is_local: bool): DateTime {
    let sec = nanos / 1000000000
    let nano = nanos % 1000000000
    build_datetime_from_epoch(sec, nano, is_local)
}

bind DateTime.new = (year: int, month: int, day: int, hour: int, minute: int, second: int, is_local: bool): DateTime {
    let sec = unsafe ll_time_make_epoch(year, month, day, hour, minute, second, is_local)
    build_datetime_from_epoch(sec, 0, is_local)
}

bind DateTime.is_leap_year = (self): bool {
    let y = self.year
    (y % 4 == 0 and y % 100 != 0) or (y % 400 == 0)
}

// -----------------------------------------------------------------------------
// Time Format Constants (Mirroring Go Reference Formats & Standard Conventions)
// -----------------------------------------------------------------------------

bind Layout = "01/02 03:04:05PM '06 -0700"
bind ANSIC = "Mon Jan _2 15:04:05 2006"
bind UnixDate = "Mon Jan _2 15:04:05 MST 2006"
bind RubyDate = "Mon Jan 02 15:04:05 -0700 2006"
bind RFC822 = "02 Jan 06 15:04 MST"
bind RFC822Z = "02 Jan 06 15:04 -0700"
bind RFC850 = "Monday, 02-Jan-06 15:04:05 MST"
bind RFC1123 = "Mon, 02 Jan 2006 15:04:05 MST"
bind RFC1123Z = "Mon, 02 Jan 2006 15:04:05 -0700"
bind RFC3339 = "2006-01-02T15:04:05Z07:00"
bind RFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
bind Kitchen = "3:04PM"
bind Stamp = "Jan _2 15:04:05"
bind StampMilli = "Jan _2 15:04:05.000"
bind StampMicro = "Jan _2 15:04:05.000000"
bind StampNano = "Jan _2 15:04:05.000000000"
bind DateTimeFormat = "2006-01-02 15:04:05"
bind DateOnly = "2006-01-02"
bind TimeOnly = "15:04:05"
bind ISO8601 = "2006-01-02T15:04:05Z07:00"
bind RFC2822 = "Mon, 02 Jan 2006 15:04:05 -0700"

// POSIX / C (strftime) Format Constants
bind C_FMT_ISO8601 = "%Y-%m-%dT%H:%M:%SZ"
bind C_FMT_DATETIME = "%Y-%m-%d %H:%M:%S"
bind C_FMT_DATE_ONLY = "%Y-%m-%d"
bind C_FMT_TIME_ONLY = "%H:%M:%S"
bind C_FMT_RFC2822 = "%a, %d %b %Y %H:%M:%S %z"
bind C_FMT_ANSIC = "%a %b %e %H:%M:%S %Y"
bind C_FMT_KITCHEN = "%I:%M %p"
bind C_FMT_12HOUR = "%I:%M:%S %p"

// -----------------------------------------------------------------------------
// Standard 1: C / POSIX / strftime Format Standard (`%` specifiers)
// -----------------------------------------------------------------------------

bind DateTime.format = (self, pattern: string): string {
    let out = ""
    let n = pattern.len()
    let i = 0

    for (i < n) {
        let ch = pattern[i..(i + 1)]
        if (ch == "%" and i + 1 < n) {
            let spec = pattern[(i + 1)..(i + 2)]
            if (spec == "Y") {
                out.push(pad_int_zeros(self.year, 4))
                i += 2
            } else if (spec == "y") {
                out.push(pad_int_zeros(self.year % 100, 2))
                i += 2
            } else if (spec == "m") {
                out.push(pad_int_zeros(self.month, 2))
                i += 2
            } else if (spec == "d") {
                out.push(pad_int_zeros(self.day, 2))
                i += 2
            } else if (spec == "e") {
                out.push(pad_int_spaces(self.day, 2))
                i += 2
            } else if (spec == "H") {
                out.push(pad_int_zeros(self.hour, 2))
                i += 2
            } else if (spec == "I") {
                let h12 = if (self.hour == 0) 12 else if (self.hour > 12) self.hour - 12 else self.hour
                out.push(pad_int_zeros(h12, 2))
                i += 2
            } else if (spec == "M") {
                out.push(pad_int_zeros(self.minute, 2))
                i += 2
            } else if (spec == "S") {
                out.push(pad_int_zeros(self.second, 2))
                i += 2
            } else if (spec == "L") {
                out.push(pad_int_zeros(self.nanosecond / 1000000, 3))
                i += 2
            } else if (spec == "f") {
                out.push(pad_int_zeros(self.nanosecond / 1000, 6))
                i += 2
            } else if (spec == "p") {
                out.push(if (self.hour < 12) "AM" else "PM")
                i += 2
            } else if (spec == "P") {
                out.push(if (self.hour < 12) "am" else "pm")
                i += 2
            } else if (spec == "z") {
                out.push(format_tz_offset(self.offset_secs, false))
                i += 2
            } else if (spec == "Z") {
                out.push(self.zone_name)
                i += 2
            } else if (spec == "a") {
                out.push(weekday_abbr(self.weekday))
                i += 2
            } else if (spec == "A") {
                out.push(weekday_full(self.weekday))
                i += 2
            } else if (spec == "b" or spec == "h") {
                out.push(month_abbr(self.month))
                i += 2
            } else if (spec == "B") {
                out.push(month_full(self.month))
                i += 2
            } else if (spec == "F") {
                out.push("${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(self.day, 2)}")
                i += 2
            } else if (spec == "T") {
                out.push("${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}")
                i += 2
            } else if (spec == "R") {
                out.push("${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}")
                i += 2
            } else if (spec == "%") {
                out.push("%")
                i += 2
            } else {
                out.push(ch)
                i += 1
            }
        } else {
            out.push(ch)
            i += 1
        }
    }
    out
}

// -----------------------------------------------------------------------------
// Standard 2: Go Reference Layout Format Standard (`Mon Jan 2 15:04:05 MST 2006`)
// -----------------------------------------------------------------------------

bind DateTime.format_go = (self, layout: string): string {
    let out = ""
    let n = layout.len()
    let i = 0

    for (i < n) {
        let rem = layout[i..n]
        if (rem.starts_with("January")) {
            out.push(month_full(self.month))
            i += 7
        } else if (rem.starts_with("Jan")) {
            out.push(month_abbr(self.month))
            i += 3
        } else if (rem.starts_with("Monday")) {
            out.push(weekday_full(self.weekday))
            i += 6
        } else if (rem.starts_with("Mon")) {
            out.push(weekday_abbr(self.weekday))
            i += 3
        } else if (rem.starts_with("2006")) {
            out.push(pad_int_zeros(self.year, 4))
            i += 4
        } else if (rem.starts_with("06")) {
            out.push(pad_int_zeros(self.year % 100, 2))
            i += 2
        } else if (rem.starts_with("01")) {
            out.push(pad_int_zeros(self.month, 2))
            i += 2
        } else if (rem.starts_with("02")) {
            out.push(pad_int_zeros(self.day, 2))
            i += 2
        } else if (rem.starts_with("_2")) {
            out.push(pad_int_spaces(self.day, 2))
            i += 2
        } else if (rem.starts_with("15")) {
            out.push(pad_int_zeros(self.hour, 2))
            i += 2
        } else if (rem.starts_with("03")) {
            let h12 = if (self.hour == 0) 12 else if (self.hour > 12) self.hour - 12 else self.hour
            out.push(pad_int_zeros(h12, 2))
            i += 2
        } else if (rem.starts_with("04")) {
            out.push(pad_int_zeros(self.minute, 2))
            i += 2
        } else if (rem.starts_with("05")) {
            out.push(pad_int_zeros(self.second, 2))
            i += 2
        } else if (rem.starts_with("PM")) {
            out.push(if (self.hour < 12) "AM" else "PM")
            i += 2
        } else if (rem.starts_with("pm")) {
            out.push(if (self.hour < 12) "am" else "pm")
            i += 2
        } else if (rem.starts_with("MST")) {
            out.push(self.zone_name)
            i += 3
        } else if (rem.starts_with("Z07:00")) {
            if (self.offset_secs == 0) {
                out.push("Z")
            } else {
                out.push(format_tz_offset(self.offset_secs, true))
            }
            i += 6
        } else if (rem.starts_with("Z0700")) {
            if (self.offset_secs == 0) {
                out.push("Z")
            } else {
                out.push(format_tz_offset(self.offset_secs, false))
            }
            i += 5
        } else if (rem.starts_with("Z07")) {
            if (self.offset_secs == 0) {
                out.push("Z")
            } else {
                out.push(format_tz_offset(self.offset_secs, false)[0..3])
            }
            i += 3
        } else if (rem.starts_with("-07:00")) {
            out.push(format_tz_offset(self.offset_secs, true))
            i += 6
        } else if (rem.starts_with("-0700")) {
            out.push(format_tz_offset(self.offset_secs, false))
            i += 5
        } else if (rem.starts_with("-07")) {
            let full_off = format_tz_offset(self.offset_secs, false)
            out.push(full_off[0..3])
            i += 3
        } else if (rem.starts_with(".000000000") or rem.starts_with(".999999999")) {
            out.push("." + pad_int_zeros(self.nanosecond, 9))
            i += 10
        } else if (rem.starts_with(".000000") or rem.starts_with(".999999")) {
            out.push("." + pad_int_zeros(self.nanosecond / 1000, 6))
            i += 7
        } else if (rem.starts_with(".000") or rem.starts_with(".999")) {
            out.push("." + pad_int_zeros(self.nanosecond / 1000000, 3))
            i += 4
        } else if (rem.starts_with("1")) {
            out.push("${self.month}")
            i += 1
        } else if (rem.starts_with("2")) {
            out.push("${self.day}")
            i += 1
        } else if (rem.starts_with("3")) {
            let h12 = if (self.hour == 0) 12 else if (self.hour > 12) self.hour - 12 else self.hour
            out.push("$h12")
            i += 1
        } else if (rem.starts_with("4")) {
            out.push("${self.minute}")
            i += 1
        } else if (rem.starts_with("5")) {
            out.push("${self.second}")
            i += 1
        } else {
            out.push(layout[i..(i + 1)])
            i += 1
        }
    }
    out
}

// -----------------------------------------------------------------------------
// Pure Lowlight Companion Formatting Methods (Zero GC, Native Speed)
// -----------------------------------------------------------------------------

bind DateTime.to_layout = (self): string {
    let mo = pad_int_zeros(self.month, 2)
    let dy = pad_int_zeros(self.day, 2)
    let h12 = if (self.hour == 0) 12 else if (self.hour > 12) self.hour - 12 else self.hour
    let hr = pad_int_zeros(h12, 2)
    let mi = pad_int_zeros(self.minute, 2)
    let sc = pad_int_zeros(self.second, 2)
    let ampm = if (self.hour < 12) "AM" else "PM"
    let yr = pad_int_zeros(self.year % 100, 2)
    let tz = format_tz_offset(self.offset_secs, false)
    "$mo/$dy $hr:$mi:$sc$ampm '$yr $tz"
}

bind DateTime.to_ansic = (self): string {
    let w = weekday_abbr(self.weekday)
    let m = month_abbr(self.month)
    let d = pad_int_spaces(self.day, 2)
    let h = pad_int_zeros(self.hour, 2)
    let mi = pad_int_zeros(self.minute, 2)
    let s = pad_int_zeros(self.second, 2)
    let y = pad_int_zeros(self.year, 4)
    "$w $m $d $h:$mi:$s $y"
}

bind DateTime.to_unix_date = (self): string {
    let w = weekday_abbr(self.weekday)
    let m = month_abbr(self.month)
    let d = pad_int_spaces(self.day, 2)
    let h = pad_int_zeros(self.hour, 2)
    let mi = pad_int_zeros(self.minute, 2)
    let s = pad_int_zeros(self.second, 2)
    let y = pad_int_zeros(self.year, 4)
    "$w $m $d $h:$mi:$s ${self.zone_name} $y"
}

bind DateTime.to_ruby_date = (self): string {
    let w = weekday_abbr(self.weekday)
    let m = month_abbr(self.month)
    let d = pad_int_zeros(self.day, 2)
    let h = pad_int_zeros(self.hour, 2)
    let mi = pad_int_zeros(self.minute, 2)
    let s = pad_int_zeros(self.second, 2)
    let tz = format_tz_offset(self.offset_secs, false)
    let y = pad_int_zeros(self.year, 4)
    "$w $m $d $h:$mi:$s $tz $y"
}

bind DateTime.to_rfc822 = (self): string {
    let d = pad_int_zeros(self.day, 2)
    let m = month_abbr(self.month)
    let y = pad_int_zeros(self.year % 100, 2)
    let h = pad_int_zeros(self.hour, 2)
    let mi = pad_int_zeros(self.minute, 2)
    "$d $m $y $h:$mi ${self.zone_name}"
}

bind DateTime.to_rfc822z = (self): string {
    let d = pad_int_zeros(self.day, 2)
    let m = month_abbr(self.month)
    let y = pad_int_zeros(self.year % 100, 2)
    let h = pad_int_zeros(self.hour, 2)
    let mi = pad_int_zeros(self.minute, 2)
    let tz = format_tz_offset(self.offset_secs, false)
    "$d $m $y $h:$mi $tz"
}

bind DateTime.to_rfc850 = (self): string {
    let w = weekday_full(self.weekday)
    let d = pad_int_zeros(self.day, 2)
    let m = month_abbr(self.month)
    let y = pad_int_zeros(self.year % 100, 2)
    let h = pad_int_zeros(self.hour, 2)
    let mi = pad_int_zeros(self.minute, 2)
    let s = pad_int_zeros(self.second, 2)
    "$w, $d-$m-$y $h:$mi:$s ${self.zone_name}"
}

bind DateTime.to_rfc1123 = (self): string {
    let w = weekday_abbr(self.weekday)
    let d = pad_int_zeros(self.day, 2)
    let m = month_abbr(self.month)
    let y = pad_int_zeros(self.year, 4)
    let h = pad_int_zeros(self.hour, 2)
    let mi = pad_int_zeros(self.minute, 2)
    let s = pad_int_zeros(self.second, 2)
    "$w, $d $m $y $h:$mi:$s ${self.zone_name}"
}

bind DateTime.to_rfc1123z = (self): string {
    let w = weekday_abbr(self.weekday)
    let d = pad_int_zeros(self.day, 2)
    let m = month_abbr(self.month)
    let y = pad_int_zeros(self.year, 4)
    let h = pad_int_zeros(self.hour, 2)
    let mi = pad_int_zeros(self.minute, 2)
    let s = pad_int_zeros(self.second, 2)
    let tz = format_tz_offset(self.offset_secs, false)
    "$w, $d $m $y $h:$mi:$s $tz"
}

bind DateTime.to_rfc3339 = (self): string {
    let d = "${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(self.day, 2)}"
    let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}"
    let tz = if (self.offset_secs == 0) "Z" else format_tz_offset(self.offset_secs, true)
    "${d}T${tm}${tz}"
}

bind DateTime.to_iso = (self): string {
    self.to_rfc3339()
}

bind DateTime.to_rfc3339_nano = (self): string {
    let d = "${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(self.day, 2)}"
    let ns = pad_int_zeros(self.nanosecond, 9)
    let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}.${ns}"
    let tz = if (self.offset_secs == 0) "Z" else format_tz_offset(self.offset_secs, true)
    "${d}T${tm}${tz}"
}

bind DateTime.to_kitchen = (self): string {
    let h12 = if (self.hour == 0) 12 else if (self.hour > 12) self.hour - 12 else self.hour
    let mi = pad_int_zeros(self.minute, 2)
    let ampm = if (self.hour < 12) "AM" else "PM"
    "$h12:$mi$ampm"
}

bind DateTime.to_stamp = (self): string {
    let m = month_abbr(self.month)
    let d = pad_int_spaces(self.day, 2)
    let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}"
    "$m $d $tm"
}

bind DateTime.to_stamp_milli = (self): string {
    let m = month_abbr(self.month)
    let d = pad_int_spaces(self.day, 2)
    let ms = pad_int_zeros(self.nanosecond / 1000000, 3)
    let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}.$ms"
    "$m $d $tm"
}

bind DateTime.to_stamp_micro = (self): string {
    let m = month_abbr(self.month)
    let d = pad_int_spaces(self.day, 2)
    let us = pad_int_zeros(self.nanosecond / 1000, 6)
    let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}.$us"
    "$m $d $tm"
}

bind DateTime.to_stamp_nano = (self): string {
    let m = month_abbr(self.month)
    let d = pad_int_spaces(self.day, 2)
    let ns = pad_int_zeros(self.nanosecond, 9)
    let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}.$ns"
    "$m $d $tm"
}

bind DateTime.to_datetime_string = (self): string {
    let d = "${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(self.day, 2)}"
    let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}"
    "$d $tm"
}

bind DateTime.to_string = (self): string {
    self.to_datetime_string()
}

bind DateTime.to_date_only = (self): string {
    "${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(self.day, 2)}"
}

bind DateTime.to_date_string = (self): string {
    self.to_date_only()
}

bind DateTime.to_time_only = (self): string {
    "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}"
}

bind DateTime.to_time_string = (self): string {
    self.to_time_only()
}

bind DateTime.to_rfc2822 = (self): string {
    self.to_rfc1123z()
}

bind DateTime.add_duration = (self, d: Duration): DateTime {
    let new_nanos = (self.unix_secs * 1000000000 + self.nanosecond) + d.nanos
    DateTime.from_unix_nanos(new_nanos, self.is_local)
}

bind DateTime.sub_duration = (self, d: Duration): DateTime {
    let new_nanos = (self.unix_secs * 1000000000 + self.nanosecond) - d.nanos
    DateTime.from_unix_nanos(new_nanos, self.is_local)
}

bind DateTime.diff = (self, other: DateTime): Duration {
    let t1 = self.unix_secs * 1000000000 + self.nanosecond
    let t2 = other.unix_secs * 1000000000 + other.nanosecond
    Duration[nanos: t1 - t2]
}

// -----------------------------------------------------------------------------
// String Extension for Parsing DateTime
// -----------------------------------------------------------------------------

bind string.to_datetime = (self): ?DateTime {
    let trimmed = self.trim()
    if (trimmed.len() < 10) func <- none
    let y = trimmed[0..4].to_int() ?? func <- none
    let m = trimmed[5..7].to_int() ?? func <- none
    let d = trimmed[8..10].to_int() ?? func <- none

    if (trimmed.len() == 10) {
        DateTime.new(y, m, d, 0, 0, 0, false)
    } else if (trimmed.len() >= 19) {
        let h = trimmed[11..13].to_int() ?? func <- none
        let min = trimmed[14..16].to_int() ?? func <- none
        let s = trimmed[17..19].to_int() ?? func <- none
        DateTime.new(y, m, d, h, min, s, false)
    } else {
        none
    }
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("time duration conversions, getters, and formatting")
bind test_time_duration = () {
    let d_ns = Duration.from_nanos(500)
    assert(d_ns.to_nanos() == 500, "duration nanos")

    let d_us = Duration.from_micros(5)
    assert(d_us.to_nanos() == 5000, "duration micros to nanos")
    assert(d_us.to_micros() == 5, "duration to_micros")

    let d_ms = Duration.from_millis(2.5)
    assert(d_ms.to_nanos() == 2500000, "duration millis to nanos")
    assert(d_ms.to_millis() == 2.5, "duration to_millis")

    let d_s = Duration.from_secs(1.5)
    assert(d_s.to_nanos() == 1500000000, "duration secs to nanos")
    assert(d_s.to_secs() == 1.5, "duration to_secs")

    assert(Duration.from_nanos(50).to_string() == "50 ns", "duration string ns")
}

@test("time Instant and monotonic clock")
bind test_time_instant = () {
    let start = Instant.now()
    assert(start.nanos > 0, "start nanos positive")

    let elapsed = start.elapsed()
    assert(elapsed.to_nanos() >= 0, "elapsed non-negative")
    assert(start.elapsed_millis() >= 0.0, "elapsed millis non-negative")
    assert(now_nanos() > 0, "now_nanos positive")
    assert(now_millis() > 0.0, "now_millis positive")
}

@test("time DateTime creation, formatting, duration arithmetic, and parsing")
bind test_time_datetime_and_parse = () {
    let dt = DateTime.new(2026, 11, 4, 15, 30, 45, false)
    assert(dt.year == 2026, "year matches")
    assert(dt.month == 11, "month matches")
    assert(dt.day == 4, "day matches")
    assert(dt.hour == 15, "hour matches")
    assert(dt.minute == 30, "minute matches")
    assert(dt.second == 45, "second matches")

    assert(dt.to_date_string() == "2026-11-04", "to_date_string")
    assert(dt.to_time_string() == "15:30:45", "to_time_string")
    assert(dt.to_datetime_string() == "2026-11-04 15:30:45", "to_datetime_string")

    let parsed? = "2026-11-04 15:30:45".to_datetime()
    assert(parsed? != none, "to_datetime succeeds")
    let p = parsed? ?? dt.copy()
    assert(p.year == 2026 and p.month == 11 and p.day == 4, "parsed date matches")
    assert(p.hour == 15 and p.minute == 30 and p.second == 45, "parsed time matches")

    let dt_plus = dt.copy().add_duration(Duration.from_secs(15.0))
    let diff = dt_plus.diff(dt)
    assert(diff.to_secs() == 15.0, "diff is 15 seconds")
}

