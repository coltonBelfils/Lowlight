// std/time — Monotonic clocks, durations, civil calendar [`DateTime`], and formatting/parsing.
//
// Provides [`Duration`], [`Instant`], [`sleep_ms`], and UTC/local [`DateTime`] supporting
// both C/POSIX `strftime` (`%Y-%m-%d`) and Go reference layout formats (`2006-01-02`).

import "std/strconv.ll"
import "std/strfmt.ll"

// -----------------------------------------------------------------------------
// Duration & Instant
// -----------------------------------------------------------------------------

// Signed elapsed nanosecond span.
//
// Implements [`Printable`].
define Duration struct [
	// Signed duration magnitude in nanoseconds.
	nanos: int,
]

// Constructs a [`Duration`] from an exact nanosecond count.
bind Duration.from_nanos = (nanos: int) Duration: Duration[nanos: nanos]

// Constructs a [`Duration`] from `micros` microseconds.
bind Duration.from_micros = (micros: int) Duration: Duration[nanos: micros * 1000]

// Constructs a [`Duration`] from fractional milliseconds `millis`.
bind Duration.from_millis = (millis: float) Duration: Duration[nanos: int(millis * 1000000.0)]

// Constructs a [`Duration`] from fractional seconds `secs`.
bind Duration.from_secs = (secs: float) Duration: Duration[nanos: int(secs * 1000000000.0)]

// Returns the duration truncated/stored in whole nanoseconds.
bind Duration.to_nanos = (self) int: self.nanos

// Returns the whole microsecond count contained in this duration.
bind Duration.to_micros = (self) int: self.nanos / 1000

// Converts the duration to floating-point milliseconds.
bind Duration.to_millis = (self) float: float(self.nanos) / 1000000.0

// Converts the duration to floating-point seconds.
bind Duration.to_secs = (self) float: float(self.nanos) / 1000000000.0

// Formats the duration automatically using `ns`, `µs`, `ms`, or `s` units.
bind Duration.to_string = (self) string:
	if(self.nanos < 1000): "${self.nanos} ns"
	else if(self.nanos < 1000000): {
		let us = float(self.nanos) / 1000.0
		"${float_to_str(us, 2)} µs"
	} else if(self.nanos < 1000000000): {
		let ms = float(self.nanos) / 1000000.0
		"${float_to_str(ms, 2)} ms"
	} else: {
		let s = float(self.nanos) / 1000000000.0
		"${float_to_str(s, 3)} s"
	}

// Measurement from a monotonic OS clock, suitable for benchmarking and timeouts.
//
// # Example
// ```lowlight
// let start = Instant.now()
// let _ = sleep_ms(5)
// let elapsed = start.elapsed()
// ```
define Instant struct [
	// Monotonic timestamp in nanoseconds.
	nanos: int,
]

// Captures the current monotonic timestamp via the OS clock.
bind Instant.now = () Instant: Instant[nanos: unsafe sys_monotonic_nanos()]

// Returns the [`Duration`] elapsed between `self` and the current monotonic instant.
bind Instant.elapsed = (self) Duration: {
	let now = unsafe sys_monotonic_nanos()
	Duration[nanos: now - self.nanos]
}

// Returns elapsed time since `self` in floating-point milliseconds.
bind Instant.elapsed_millis = (self) float: {
	let d = self.elapsed()
	d.to_millis()
}

// Returns elapsed time since `self` in whole nanoseconds.
bind Instant.elapsed_nanos = (self) int: {
	let d = self.elapsed()
	d.to_nanos()
}

// Returns the raw current monotonic clock value in nanoseconds.
bind now_nanos = () int: unsafe sys_monotonic_nanos()

// Returns the current monotonic clock value in floating-point milliseconds.
bind now_millis = () float: float(unsafe sys_monotonic_nanos()) / 1000000.0

// Suspends execution of the calling thread for at least `ms` milliseconds.
bind sleep_ms = (ms: int): unsafe sys_sleep_ms(ms)

// Returns the three-letter English month abbreviation for `1..=12` (or `""` if out of range).
bind month_abbr = (m: int) string:
	if(m == 1): "Jan"
	else if(m == 2): "Feb"
	else if(m == 3): "Mar"
	else if(m == 4): "Apr"
	else if(m == 5): "May"
	else if(m == 6): "Jun"
	else if(m == 7): "Jul"
	else if(m == 8): "Aug"
	else if(m == 9): "Sep"
	else if(m == 10): "Oct"
	else if(m == 11): "Nov"
	else if(m == 12): "Dec"
	else: ""

// Returns the full English month name for `1..=12` (or `""` if out of range).
bind month_full = (m: int) string:
	if(m == 1): "January"
	else if(m == 2): "February"
	else if(m == 3): "March"
	else if(m == 4): "April"
	else if(m == 5): "May"
	else if(m == 6): "June"
	else if(m == 7): "July"
	else if(m == 8): "August"
	else if(m == 9): "September"
	else if(m == 10): "October"
	else if(m == 11): "November"
	else if(m == 12): "December"
	else: ""

// Returns the three-letter English weekday abbreviation for `0..=6` (`0` = Sunday, or `""` if out of range).
bind weekday_abbr = (w: int) string:
	if(w == 0): "Sun"
	else if(w == 1): "Mon"
	else if(w == 2): "Tue"
	else if(w == 3): "Wed"
	else if(w == 4): "Thu"
	else if(w == 5): "Fri"
	else if(w == 6): "Sat"
	else: ""

// Returns the full English weekday name for `0..=6` (`0` = Sunday, or `""` if out of range).
bind weekday_full = (w: int) string:
	if(w == 0): "Sunday"
	else if(w == 1): "Monday"
	else if(w == 2): "Tuesday"
	else if(w == 3): "Wednesday"
	else if(w == 4): "Thursday"
	else if(w == 5): "Friday"
	else if(w == 6): "Saturday"
	else: ""

// Formats a UTC timezone offset in seconds as `+HH:MM` (when `with_colon` is `true`) or `+HHMM`.
bind format_tz_offset = (offset_secs: int, with_colon: bool) string: {
	let is_neg = offset_secs < 0
	let total_mins = (if(is_neg): -offset_secs else: offset_secs) / 60
	let hours = total_mins / 60
	let mins = total_mins % 60
	let sign = if(is_neg): "-" else: "+"
	let h_str = pad_int_zeros(hours, 2)
	let m_str = pad_int_zeros(mins, 2)
	if(with_colon): "$sign$h_str:$m_str" else: "$sign$h_str$m_str"
}

// -----------------------------------------------------------------------------
// DateTime Struct & Methods
// -----------------------------------------------------------------------------

// Civil date-time calendar value with nanosecond precision and timezone offset metadata.
//
// Supports C `strftime` formats via [`DateTime.format`], Go reference layouts via
// [`DateTime.format_go`], and duration arithmetic via [`DateTime.add_duration`] / [`DateTime.sub_duration`].
//
// # Example
// ```lowlight
// let dt = DateTime.new(2026, 1, 15, 9, 30, 0, false)
// assert(dt.to_date_only() == "2026-01-15")
// ```
define DateTime struct [
	// Full calendar year (e.g. `2026`).
	year: int,
	// Month of year (`1..=12`).
	month: int,
	// Day of month (`1..=31`).
	day: int,
	// Hour of day (`0..=23`).
	hour: int,
	// Minute of hour (`0..=59`).
	minute: int,
	// Second of minute (`0..=59`).
	second: int,
	// Fractional nanoseconds within the second (`0..=999_999_999`).
	nanosecond: int,
	// Day of week (`0` = Sunday .. `6` = Saturday).
	weekday: int,
	// One-based ordinal day of the year (`1..=366`).
	yday: int,
	// Offset east of UTC in seconds.
	offset_secs: int,
	// Short timezone label such as `"UTC"` or `"LOCAL"`.
	zone_name: string,
	// Seconds since the Unix epoch (`1970-01-01T00:00:00Z`).
	unix_secs: int,
	// Whether this timestamp uses the host local timezone offset.
	is_local: bool,
]

bind _days_from_civil = (y: int, m: int, d: int) int: {
	let yr = if(m <= 2): y - 1 else: y
	let era = if(yr >= 0): yr / 400 else: (yr - 399) / 400
	let yoe = yr - era * 400
	let mp = if(m > 2): m - 3 else: m + 9
	let doy = (153 * mp + 2) / 5 + d - 1
	let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy
	era * 146097 + doe - 719468
}

bind _build_datetime_from_epoch = (epoch_sec: int, nano: int, is_local: bool) DateTime: {
	let off = if(is_local): unsafe sys_local_tz_offset(epoch_sec) else: 0
	let adj_sec = epoch_sec + off
	let days = if(adj_sec >= 0): adj_sec / 86400 else: (adj_sec - 86399) / 86400
	let rem_sec = adj_sec - days * 86400
	let hr = rem_sec / 3600
	let mi = rem_sec % 3600 / 60
	let sc = rem_sec % 60

	let wd_raw = (days + 4) % 7
	let wd = if(wd_raw < 0): wd_raw + 7 else: wd_raw

	let z = days + 719468
	let era = if(z >= 0): z / 146097 else: (z - 146096) / 146097
	let doe = z - era * 146097
	let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365
	let y = yoe + era * 400
	let doy = doe - (365 * yoe + yoe / 4 - yoe / 100)
	let mp = (5 * doy + 2) / 153
	let dy = doy - (153 * mp + 2) / 5 + 1
	let mo = if(mp < 10): mp + 3 else: mp - 9
	let yr = if(mo <= 2): y + 1 else: y
	let yd = days - _days_from_civil(yr, 1, 1) + 1
	let zone = if(is_local): "LOCAL" else: "UTC"

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

// Returns the current UTC [`DateTime`] read from the host OS real-time clock.
bind DateTime.now_utc = () DateTime: {
	let now_nanos = unsafe sys_epoch_nanos_now()
	let sec = now_nanos / 1000000000
	let nano = now_nanos % 1000000000
	_build_datetime_from_epoch(sec, nano, false)
}

// Returns the current local [`DateTime`] adjusted by the host system timezone offset.
bind DateTime.now_local = () DateTime: {
	let now_nanos = unsafe sys_epoch_nanos_now()
	let sec = now_nanos / 1000000000
	let nano = now_nanos % 1000000000
	_build_datetime_from_epoch(sec, nano, true)
}

// Constructs a [`DateTime`] from Unix epoch seconds, in local time when `is_local` is `true` or UTC otherwise.
bind DateTime.from_unix_secs = (secs: int, is_local: bool) DateTime:
	_build_datetime_from_epoch(secs, 0, is_local)

// Constructs a [`DateTime`] from Unix epoch milliseconds, preserving millisecond sub-second precision.
bind DateTime.from_unix_millis = (ms: int, is_local: bool) DateTime: {
	let sec = ms / 1000
	let nano = ms % 1000 * 1000000
	_build_datetime_from_epoch(sec, nano, is_local)
}

// Constructs a [`DateTime`] from Unix epoch nanoseconds.
bind DateTime.from_unix_nanos = (nanos: int, is_local: bool) DateTime: {
	let sec = nanos / 1000000000
	let nano = nanos % 1000000000
	_build_datetime_from_epoch(sec, nano, is_local)
}

// Constructs a [`DateTime`] from civil date/time components in UTC (`is_local == false`) or local time.
bind DateTime.new = (
	year: int,
	month: int,
	day: int,
	hour: int,
	minute: int,
	second: int,
	is_local: bool,
) DateTime: {
	let utc_sec = _days_from_civil(year, month, day) * 86400 + hour * 3600 + minute * 60 + second
	let off = if(is_local): unsafe sys_local_tz_offset(utc_sec) else: 0
	let sec = utc_sec - off
	_build_datetime_from_epoch(sec, 0, is_local)
}

// Reports whether `self.year` is a Gregorian leap year.
bind DateTime.is_leap_year = (self) bool: {
	let y = self.year
	(y % 4 == 0 and y % 100 != 0) or y % 400 == 0
}

// -----------------------------------------------------------------------------
// Time Format Constants (Mirroring Go Reference Formats & Standard Conventions)
// -----------------------------------------------------------------------------

// Go reference layout constant (`"01/02 03:04:05PM '06 -0700"`).
bind Layout = "01/02 03:04:05PM '06 -0700"

// ANSI C date-time layout for [`DateTime.format_go`].
bind ANSIC = "Mon Jan _2 15:04:05 2006"

// Unix `date(1)` layout for [`DateTime.format_go`].
bind UnixDate = "Mon Jan _2 15:04:05 MST 2006"

// Ruby date-time layout for [`DateTime.format_go`].
bind RubyDate = "Mon Jan 02 15:04:05 -0700 2006"

// RFC 822 date-time layout with named timezone.
bind RFC822 = "02 Jan 06 15:04 MST"

// RFC 822 date-time layout with numeric timezone offset.
bind RFC822Z = "02 Jan 06 15:04 -0700"

// RFC 850 verbose date-time layout.
bind RFC850 = "Monday, 02-Jan-06 15:04:05 MST"

// RFC 1123 HTTP date layout with named timezone.
bind RFC1123 = "Mon, 02 Jan 2006 15:04:05 MST"

// RFC 1123 HTTP date layout with numeric offset.
bind RFC1123Z = "Mon, 02 Jan 2006 15:04:05 -0700"

// RFC 3339 / ISO-8601 date-time layout (`Z07:00`).
bind RFC3339 = "2006-01-02T15:04:05Z07:00"

// RFC 3339 date-time layout including nanoseconds.
bind RFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

// Short 12-hour clock layout (`"3:04PM"`).
bind Kitchen = "3:04PM"

// Syslog-style short timestamp layout (`"Jan _2 15:04:05"`).
bind Stamp = "Jan _2 15:04:05"

// Syslog timestamp layout with milliseconds.
bind StampMilli = "Jan _2 15:04:05.000"

// Syslog timestamp layout with microseconds.
bind StampMicro = "Jan _2 15:04:05.000000"

// Syslog timestamp layout with nanoseconds.
bind StampNano = "Jan _2 15:04:05.000000000"

// Common SQL / log timestamp layout (`"2006-01-02 15:04:05"`).
bind DateTimeFormat = "2006-01-02 15:04:05"

// Calendar date layout (`"2006-01-02"`).
bind DateOnly = "2006-01-02"

// Wall clock time-of-day layout (`"15:04:05"`).
bind TimeOnly = "15:04:05"

// ISO-8601 date-time layout (`"2006-01-02T15:04:05Z07:00"`).
bind ISO8601 = "2006-01-02T15:04:05Z07:00"

// RFC 2822 email timestamp layout (`"Mon, 02 Jan 2006 15:04:05 -0700"`).
bind RFC2822 = "Mon, 02 Jan 2006 15:04:05 -0700"

// C `strftime`-style ISO-8601 UTC format constant (`"%Y-%m-%dT%H:%M:%SZ"`).
bind C_FMT_ISO8601 = "%Y-%m-%dT%H:%M:%SZ"

// C `strftime`-style date-time format constant (`"%Y-%m-%d %H:%M:%S"`).
bind C_FMT_DATETIME = "%Y-%m-%d %H:%M:%S"

// C `strftime`-style date-only format constant (`"%Y-%m-%d"`).
bind C_FMT_DATE_ONLY = "%Y-%m-%d"

// C `strftime`-style time-only format constant (`"%H:%M:%S"`).
bind C_FMT_TIME_ONLY = "%H:%M:%S"

// C `strftime`-style RFC 2822 format constant.
bind C_FMT_RFC2822 = "%a, %d %b %Y %H:%M:%S %z"

// C `strftime`-style ANSI C format constant.
bind C_FMT_ANSIC = "%a %b %e %H:%M:%S %Y"

// C `strftime`-style 12-hour kitchen clock format constant.
bind C_FMT_KITCHEN = "%I:%M %p"

// C `strftime`-style 12-hour clock with seconds format constant.
bind C_FMT_12HOUR = "%I:%M:%S %p"

// -----------------------------------------------------------------------------
// Standard 1: C / POSIX / strftime Format Standard (`%` specifiers)
// -----------------------------------------------------------------------------

// Formats this [`DateTime`] using C/POSIX `strftime` `%` format specifiers (`%Y`, `%m`, `%d`, `%H`, `%M`, `%S`, `%L`, `%f`, `%p`, `%P`, `%z`, `%Z`, `%a`, `%A`, `%b`, `%B`, `%F`, `%T`, `%R`).
bind DateTime.format = (self, pattern: string) string: {
	let out = ""
	let n = pattern.len()
	let i = 0

	for(i < n): {
		let ch = pattern[i..i + 1]
		if(ch == "%" and i + 1 < n): {
			let spec = pattern[i + 1..i + 2]
			if(spec == "Y"): {
				out.push(pad_int_zeros(self.year, 4))
				i += 2
			} else if(spec == "y"): {
				out.push(pad_int_zeros(self.year % 100, 2))
				i += 2
			} else if(spec == "m"): {
				out.push(pad_int_zeros(self.month, 2))
				i += 2
			} else if(spec == "d"): {
				out.push(pad_int_zeros(self.day, 2))
				i += 2
			} else if(spec == "e"): {
				out.push(pad_int_spaces(self.day, 2))
				i += 2
			} else if(spec == "H"): {
				out.push(pad_int_zeros(self.hour, 2))
				i += 2
			} else if(spec == "I"): {
				let h12 = if(self.hour == 0): 12
				else if(self.hour > 12): self.hour - 12
				else: self.hour
				out.push(pad_int_zeros(h12, 2))
				i += 2
			} else if(spec == "M"): {
				out.push(pad_int_zeros(self.minute, 2))
				i += 2
			} else if(spec == "S"): {
				out.push(pad_int_zeros(self.second, 2))
				i += 2
			} else if(spec == "L"): {
				out.push(pad_int_zeros(self.nanosecond / 1000000, 3))
				i += 2
			} else if(spec == "f"): {
				out.push(pad_int_zeros(self.nanosecond / 1000, 6))
				i += 2
			} else if(spec == "p"): {
				out.push(if(self.hour < 12): "AM" else: "PM")
				i += 2
			} else if(spec == "P"): {
				out.push(if(self.hour < 12): "am" else: "pm")
				i += 2
			} else if(spec == "z"): {
				out.push(format_tz_offset(self.offset_secs, false))
				i += 2
			} else if(spec == "Z"): {
				out.push(self.zone_name)
				i += 2
			} else if(spec == "a"): {
				out.push(weekday_abbr(self.weekday))
				i += 2
			} else if(spec == "A"): {
				out.push(weekday_full(self.weekday))
				i += 2
			} else if(spec == "b" or spec == "h"): {
				out.push(month_abbr(self.month))
				i += 2
			} else if(spec == "B"): {
				out.push(month_full(self.month))
				i += 2
			} else if(spec == "F"): {
				out.push(
					"${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(
						self.day,
						2,
					)}",
				)
				i += 2
			} else if(spec == "T"): {
				out.push(
					"${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(
						self.second,
						2,
					)}",
				)
				i += 2
			} else if(spec == "R"): {
				out.push("${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}")
				i += 2
			} else if(spec == "%"): {
				out.push("%")
				i += 2
			} else: {
				out.push(ch)
				i += 1
			}
		} else: {
			out.push(ch)
			i += 1
		}
	}
	out
}

// -----------------------------------------------------------------------------
// Standard 2: Go Reference Layout Format Standard (`Mon Jan 2 15:04:05 MST 2006`)
// -----------------------------------------------------------------------------

// Formats this [`DateTime`] using Go reference-time layout tokens (`"2006-01-02 15:04:05Z07:00"`).
bind DateTime.format_go = (self, layout: string) string: {
	let out = ""
	let n = layout.len()
	let i = 0

	for(i < n): {
		let rem = layout[i..n]
		if(rem.starts_with("January")): {
			out.push(month_full(self.month))
			i += 7
		} else if(rem.starts_with("Jan")): {
			out.push(month_abbr(self.month))
			i += 3
		} else if(rem.starts_with("Monday")): {
			out.push(weekday_full(self.weekday))
			i += 6
		} else if(rem.starts_with("Mon")): {
			out.push(weekday_abbr(self.weekday))
			i += 3
		} else if(rem.starts_with("2006")): {
			out.push(pad_int_zeros(self.year, 4))
			i += 4
		} else if(rem.starts_with("06")): {
			out.push(pad_int_zeros(self.year % 100, 2))
			i += 2
		} else if(rem.starts_with("01")): {
			out.push(pad_int_zeros(self.month, 2))
			i += 2
		} else if(rem.starts_with("02")): {
			out.push(pad_int_zeros(self.day, 2))
			i += 2
		} else if(rem.starts_with("_2")): {
			out.push(pad_int_spaces(self.day, 2))
			i += 2
		} else if(rem.starts_with("15")): {
			out.push(pad_int_zeros(self.hour, 2))
			i += 2
		} else if(rem.starts_with("03")): {
			let h12 = if(self.hour == 0): 12
			else if(self.hour > 12): self.hour - 12
			else: self.hour
			out.push(pad_int_zeros(h12, 2))
			i += 2
		} else if(rem.starts_with("04")): {
			out.push(pad_int_zeros(self.minute, 2))
			i += 2
		} else if(rem.starts_with("05")): {
			out.push(pad_int_zeros(self.second, 2))
			i += 2
		} else if(rem.starts_with("PM")): {
			out.push(if(self.hour < 12): "AM" else: "PM")
			i += 2
		} else if(rem.starts_with("pm")): {
			out.push(if(self.hour < 12): "am" else: "pm")
			i += 2
		} else if(rem.starts_with("MST")): {
			out.push(self.zone_name)
			i += 3
		} else if(rem.starts_with("Z07:00")): {
			if(self.offset_secs == 0): out.push("Z")
			else: out.push(format_tz_offset(self.offset_secs, true))
			i += 6
		} else if(rem.starts_with("Z0700")): {
			if(self.offset_secs == 0): out.push("Z")
			else: out.push(format_tz_offset(self.offset_secs, false))
			i += 5
		} else if(rem.starts_with("Z07")): {
			if(self.offset_secs == 0): out.push("Z")
			else: out.push(format_tz_offset(self.offset_secs, false)[0..3])
			i += 3
		} else if(rem.starts_with("-07:00")): {
			out.push(format_tz_offset(self.offset_secs, true))
			i += 6
		} else if(rem.starts_with("-0700")): {
			out.push(format_tz_offset(self.offset_secs, false))
			i += 5
		} else if(rem.starts_with("-07")): {
			let full_off = format_tz_offset(self.offset_secs, false)
			out.push(full_off[0..3])
			i += 3
		} else if(rem.starts_with(".000000000") or rem.starts_with(".999999999")): {
			out.push("." + pad_int_zeros(self.nanosecond, 9))
			i += 10
		} else if(rem.starts_with(".000000") or rem.starts_with(".999999")): {
			out.push("." + pad_int_zeros(self.nanosecond / 1000, 6))
			i += 7
		} else if(rem.starts_with(".000") or rem.starts_with(".999")): {
			out.push("." + pad_int_zeros(self.nanosecond / 1000000, 3))
			i += 4
		} else if(rem.starts_with("1")): {
			out.push("${self.month}")
			i += 1
		} else if(rem.starts_with("2")): {
			out.push("${self.day}")
			i += 1
		} else if(rem.starts_with("3")): {
			let h12 = if(self.hour == 0): 12
			else if(self.hour > 12): self.hour - 12
			else: self.hour
			out.push("$h12")
			i += 1
		} else if(rem.starts_with("4")): {
			out.push("${self.minute}")
			i += 1
		} else if(rem.starts_with("5")): {
			out.push("${self.second}")
			i += 1
		} else: {
			out.push(layout[i..i + 1])
			i += 1
		}
	}
	out
}

// -----------------------------------------------------------------------------
// Pure Lowlight Companion Formatting Methods (Zero GC, Native Speed)
// -----------------------------------------------------------------------------

// Formats this [`DateTime`] using the Go reference [`Layout`] convention (`"MM/DD hh:mm:ssAM 'YY ±HHMM"`).
bind DateTime.to_layout = (self) string: {
	let mo = pad_int_zeros(self.month, 2)
	let dy = pad_int_zeros(self.day, 2)
	let h12 = if(self.hour == 0): 12
	else if(self.hour > 12): self.hour - 12
	else: self.hour
	let hr = pad_int_zeros(h12, 2)
	let mi = pad_int_zeros(self.minute, 2)
	let sc = pad_int_zeros(self.second, 2)
	let ampm = if(self.hour < 12): "AM" else: "PM"
	let yr = pad_int_zeros(self.year % 100, 2)
	let tz = format_tz_offset(self.offset_secs, false)
	"$mo/$dy $hr:$mi:$sc$ampm '$yr $tz"
}

// Formats this [`DateTime`] in [`ANSIC`] layout (`"Mon Jan _2 15:04:05 2006"`).
bind DateTime.to_ansic = (self) string: {
	let w = weekday_abbr(self.weekday)
	let m = month_abbr(self.month)
	let d = pad_int_spaces(self.day, 2)
	let h = pad_int_zeros(self.hour, 2)
	let mi = pad_int_zeros(self.minute, 2)
	let s = pad_int_zeros(self.second, 2)
	let y = pad_int_zeros(self.year, 4)
	"$w $m $d $h:$mi:$s $y"
}

// Formats this [`DateTime`] in [`UnixDate`] layout with timezone label.
bind DateTime.to_unix_date = (self) string: {
	let w = weekday_abbr(self.weekday)
	let m = month_abbr(self.month)
	let d = pad_int_spaces(self.day, 2)
	let h = pad_int_zeros(self.hour, 2)
	let mi = pad_int_zeros(self.minute, 2)
	let s = pad_int_zeros(self.second, 2)
	let y = pad_int_zeros(self.year, 4)
	"$w $m $d $h:$mi:$s ${self.zone_name} $y"
}

// Formats this [`DateTime`] in [`RubyDate`] layout with numeric timezone offset.
bind DateTime.to_ruby_date = (self) string: {
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

// Formats this [`DateTime`] in [`RFC822`] layout with timezone label.
bind DateTime.to_rfc822 = (self) string: {
	let d = pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = pad_int_zeros(self.year % 100, 2)
	let h = pad_int_zeros(self.hour, 2)
	let mi = pad_int_zeros(self.minute, 2)
	"$d $m $y $h:$mi ${self.zone_name}"
}

// Formats this [`DateTime`] in [`RFC822Z`] layout with numeric timezone offset.
bind DateTime.to_rfc822z = (self) string: {
	let d = pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = pad_int_zeros(self.year % 100, 2)
	let h = pad_int_zeros(self.hour, 2)
	let mi = pad_int_zeros(self.minute, 2)
	let tz = format_tz_offset(self.offset_secs, false)
	"$d $m $y $h:$mi $tz"
}

// Formats this [`DateTime`] in [`RFC850`] layout with full weekday name.
bind DateTime.to_rfc850 = (self) string: {
	let w = weekday_full(self.weekday)
	let d = pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = pad_int_zeros(self.year % 100, 2)
	let h = pad_int_zeros(self.hour, 2)
	let mi = pad_int_zeros(self.minute, 2)
	let s = pad_int_zeros(self.second, 2)
	"$w, $d-$m-$y $h:$mi:$s ${self.zone_name}"
}

// Formats this [`DateTime`] in [`RFC1123`] HTTP header layout with timezone label.
bind DateTime.to_rfc1123 = (self) string: {
	let w = weekday_abbr(self.weekday)
	let d = pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = pad_int_zeros(self.year, 4)
	let h = pad_int_zeros(self.hour, 2)
	let mi = pad_int_zeros(self.minute, 2)
	let s = pad_int_zeros(self.second, 2)
	"$w, $d $m $y $h:$mi:$s ${self.zone_name}"
}

// Formats this [`DateTime`] in [`RFC1123Z`] layout with numeric timezone offset.
bind DateTime.to_rfc1123z = (self) string: {
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

// Formats this [`DateTime`] in [`RFC3339`] / [`ISO8601`] layout (`"2006-01-02T15:04:05Z07:00"`).
bind DateTime.to_rfc3339 = (self) string: {
	let d = "${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(
		self.day,
		2,
	)}"
	let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(
		self.second,
		2,
	)}"
	let tz = if(self.offset_secs == 0): "Z" else: format_tz_offset(self.offset_secs, true)
	"${d}T$tm$tz"
}

// Alias for [`DateTime.to_rfc3339`].
bind DateTime.to_iso = (self) string: self.to_rfc3339()

// Formats this [`DateTime`] in [`RFC3339Nano`] layout including 9 nanosecond digits.
bind DateTime.to_rfc3339_nano = (self) string: {
	let d = "${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(
		self.day,
		2,
	)}"
	let ns = pad_int_zeros(self.nanosecond, 9)
	let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(
		self.second,
		2,
	)}.$ns"
	let tz = if(self.offset_secs == 0): "Z" else: format_tz_offset(self.offset_secs, true)
	"${d}T$tm$tz"
}

// Formats this [`DateTime`] in 12-hour [`Kitchen`] clock format (`"3:04PM"`).
bind DateTime.to_kitchen = (self) string: {
	let h12 = if(self.hour == 0): 12
	else if(self.hour > 12): self.hour - 12
	else: self.hour
	let mi = pad_int_zeros(self.minute, 2)
	let ampm = if(self.hour < 12): "AM" else: "PM"
	"$h12:$mi$ampm"
}

// Formats this [`DateTime`] in [`Stamp`] layout (`"Jan _2 15:04:05"`).
bind DateTime.to_stamp = (self) string: {
	let m = month_abbr(self.month)
	let d = pad_int_spaces(self.day, 2)
	let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(
		self.second,
		2,
	)}"
	"$m $d $tm"
}

// Formats this [`DateTime`] in [`StampMilli`] layout with millisecond precision.
bind DateTime.to_stamp_milli = (self) string: {
	let m = month_abbr(self.month)
	let d = pad_int_spaces(self.day, 2)
	let ms = pad_int_zeros(self.nanosecond / 1000000, 3)
	let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(
		self.second,
		2,
	)}.$ms"
	"$m $d $tm"
}

// Formats this [`DateTime`] in [`StampMicro`] layout with microsecond precision.
bind DateTime.to_stamp_micro = (self) string: {
	let m = month_abbr(self.month)
	let d = pad_int_spaces(self.day, 2)
	let us = pad_int_zeros(self.nanosecond / 1000, 6)
	let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(
		self.second,
		2,
	)}.$us"
	"$m $d $tm"
}

// Formats this [`DateTime`] in [`StampNano`] layout with nanosecond precision.
bind DateTime.to_stamp_nano = (self) string: {
	let m = month_abbr(self.month)
	let d = pad_int_spaces(self.day, 2)
	let ns = pad_int_zeros(self.nanosecond, 9)
	let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(
		self.second,
		2,
	)}.$ns"
	"$m $d $tm"
}

// Formats this [`DateTime`] as `"YYYY-MM-DD HH:MM:SS"`.
bind DateTime.to_datetime_string = (self) string: {
	let d = "${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(
		self.day,
		2,
	)}"
	let tm = "${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(
		self.second,
		2,
	)}"
	"$d $tm"
}

// Returns the default string representation ([`DateTime.to_datetime_string`]).
bind DateTime.to_string = (self) string: self.to_datetime_string()

// Formats only the calendar date portion as `"YYYY-MM-DD"`.
bind DateTime.to_date_only = (self) string:
	"${pad_int_zeros(self.year, 4)}-${pad_int_zeros(self.month, 2)}-${pad_int_zeros(self.day, 2)}"

// Alias for [`DateTime.to_date_only`].
bind DateTime.to_date_string = (self) string: self.to_date_only()

// Formats only the wall-clock time portion as `"HH:MM:SS"`.
bind DateTime.to_time_only = (self) string:
	"${pad_int_zeros(self.hour, 2)}:${pad_int_zeros(self.minute, 2)}:${pad_int_zeros(self.second, 2)}"

// Alias for [`DateTime.to_time_only`].
bind DateTime.to_time_string = (self) string: self.to_time_only()

// Formats this [`DateTime`] in [`RFC2822`] email layout (`"Mon, 02 Jan 2006 15:04:05 -0700"`).
bind DateTime.to_rfc2822 = (self) string: self.to_rfc1123z()

// Returns a new [`DateTime`] offset forward by [`Duration`] `d`.
bind DateTime.add_duration = (self, d: Duration) DateTime: {
	let new_nanos = self.unix_secs * 1000000000 + self.nanosecond + d.nanos
	DateTime.from_unix_nanos(new_nanos, self.is_local)
}

// Returns a new [`DateTime`] offset backward by [`Duration`] `d`.
bind DateTime.sub_duration = (self, d: Duration) DateTime: {
	let new_nanos = self.unix_secs * 1000000000 + self.nanosecond - d.nanos
	DateTime.from_unix_nanos(new_nanos, self.is_local)
}

// Computes the signed elapsed [`Duration`] `self - other`.
bind DateTime.diff = (self, other: DateTime) Duration: {
	let t1 = self.unix_secs * 1000000000 + self.nanosecond
	let t2 = other.unix_secs * 1000000000 + other.nanosecond
	Duration[nanos: t1 - t2]
}

// -----------------------------------------------------------------------------
// String Extension for Parsing DateTime
// -----------------------------------------------------------------------------

// Parses an ISO-like date (`"YYYY-MM-DD"`) or date-time (`"YYYY-MM-DDTHH:MM:SS"` / `"YYYY-MM-DD HH:MM:SS"`) string into a UTC [`DateTime`], or returns `none`.
bind string.to_datetime = (self) ?DateTime: {
	let trimmed = self.trim()
	if(trimmed.len() < 10): func <- none
	let y = trimmed[0..4].to_int() ?? func <- none
	let m = trimmed[5..7].to_int() ?? func <- none
	let d = trimmed[8..10].to_int() ?? func <- none

	if(trimmed.len() == 10): DateTime.new(y, m, d, 0, 0, 0, false)
	else if(trimmed.len() >= 19): {
		let h = trimmed[11..13].to_int() ?? func <- none
		let min = trimmed[14..16].to_int() ?? func <- none
		let s = trimmed[17..19].to_int() ?? func <- none
		DateTime.new(y, m, d, h, min, s, false)
	} else: none
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("time duration conversions, getters, and formatting")
bind test_time_duration = (): {
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
bind test_time_instant = (): {
	let start = Instant.now()
	assert(start.nanos > 0, "start nanos positive")

	let elapsed = start.elapsed()
	assert(elapsed.to_nanos() >= 0, "elapsed non-negative")
	assert(start.elapsed_millis() >= 0.0, "elapsed millis non-negative")
	assert(now_nanos() > 0, "now_nanos positive")
	assert(now_millis() > 0.0, "now_millis positive")
}

@test("time DateTime creation, formatting, duration arithmetic, and parsing")
bind test_time_datetime_and_parse = (): {
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
