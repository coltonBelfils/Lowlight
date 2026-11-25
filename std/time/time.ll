// std/time — Monotonic clocks, durations, civil calendar [`DateTime`], and formatting/parsing.
//
// Provides [`Duration`], [`Instant`], [`sleep_ms`], and UTC/local [`DateTime`] supporting
// both C/POSIX `strftime` (`%Y-%m-%d`) and Go reference layout formats (`2006-01-02`).

import "std/strconv"
import "std/strfmt"

comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	extern clock_gettime as _time_clock_gettime: fn(clk_id: i32, ts: ptr[u8]) i32
	extern nanosleep as _time_nanosleep: fn(req: ptr[u8], rem: ptr[u8]) i32
	extern localtime_r as _time_localtime_r: fn(t: ptr[int], tm: ptr[u8]) ptr[u8]
} else comptime if(comp.TARGET.os == comp.TargetOs.linux): {
	extern localtime_r as _time_localtime_r: fn(t: ptr[int], tm: ptr[u8]) ptr[u8]
}

bind _clock_nanos = fn(linux_clk: int, darwin_clk: int) int: {
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_clock_gettime = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 113 else: 228
		unsafe {
			let ts = alloc_raw[int](2)
			ptr_write(ts, 0)
			ptr_write(ptr_add(ts, 1), 0)
			let _ = syscall(sys_clock_gettime, linux_clk, int(ptr_addr(ts)), 0, 0, 0, 0)
			let ns = ptr_read(ts) * 1000000000 + ptr_read(ptr_add(ts, 1))
			free_raw(ts)
			ns
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let ts = alloc_raw[int](2)
			ptr_write(ts, 0)
			ptr_write(ptr_add(ts, 1), 0)
			let _ = _time_clock_gettime(i32(darwin_clk), ptr_cast[u8](ts))
			let ns = ptr_read(ts) * 1000000000 + ptr_read(ptr_add(ts, 1))
			free_raw(ts)
			ns
		}
	} else: {
		let _ = linux_clk + darwin_clk
		0
	}
}

bind _monotonic_nanos = fn() int: _clock_nanos(1, 6)

bind _epoch_nanos_now = fn() int: _clock_nanos(0, 0)

define _LocalTzInfo struct [
	offset: int,
	name: string,
]

bind _query_local_tz = fn(epoch_sec: int) _LocalTzInfo: {
	comptime if(comp.TARGET.os == comp.TargetOs.darwin or comp.TARGET.os == comp.TargetOs.linux): {
		unsafe {
			let sec_slot = alloc_raw[int](1)
			ptr_write(sec_slot, epoch_sec)
			let tm_buf = alloc_raw[u8](64)
			fill_bytes(tm_buf, u8(0), 64)
			let tm_p = _time_localtime_r(sec_slot, tm_buf)
			let off = 0
			let zone = ""
			if(not is_null(tm_p)): {
				off = ptr_read(ptr_cast[int](ptr_add(tm_buf, 40)))
				let z_cstr = ptr_read(ptr_cast[ptr[u8]](ptr_add(tm_buf, 48)))
				zone = str_from_cstr(z_cstr)
			}
			free_raw(tm_buf)
			free_raw(sec_slot)
			_LocalTzInfo[offset: off, name: own zone]
		}
	} else: {
		let _ = epoch_sec
		_LocalTzInfo[offset: 0, name: ""]
	}
}

// -----------------------------------------------------------------------------
// Duration & Instant
// -----------------------------------------------------------------------------

// Signed elapsed nanosecond span.
//
// Implements [`Printable`].
define Duration struct [
	// Signed duration magnitude in nanoseconds.
	nanos: int
]

// Constructs a [`Duration`] from an exact nanosecond count.
bind Duration.from_nanos = fn(nanos: int) Duration: Duration[nanos: nanos]

// Constructs a [`Duration`] from `micros` microseconds.
bind Duration.from_micros = fn(micros: int) Duration: Duration[nanos: micros * 1000]

// Constructs a [`Duration`] from fractional milliseconds `millis`.
bind Duration.from_millis = fn(millis: float) Duration: Duration[nanos: int(millis * 1000000.0)]

// Constructs a [`Duration`] from fractional seconds `secs`.
bind Duration.from_secs = fn(secs: float) Duration: Duration[nanos: int(secs * 1000000000.0)]

// Returns the duration truncated/stored in whole nanoseconds.
bind Duration.to_nanos = fn(self) int: self.nanos

// Returns the whole microsecond count contained in this duration.
bind Duration.to_micros = fn(self) int: self.nanos / 1000

// Converts the duration to floating-point milliseconds.
bind Duration.to_millis = fn(self) float: float(self.nanos) / 1000000.0

// Converts the duration to floating-point seconds.
bind Duration.to_secs = fn(self) float: float(self.nanos) / 1000000000.0

// Formats the duration automatically using `ns`, `µs`, `ms`, or `s` units.
bind Duration.to_string = fn(self) string: {
	let sign = if(self.nanos < 0): "-" else: ""

	// Use a float magnitude so negating the minimum `int` cannot overflow.
	let mag = if(self.nanos < 0): -float(self.nanos) else: float(self.nanos)
	if(mag < 1000.0): "$sign${int(mag)} ns"
	else if(mag < 1000000.0): "$sign${strconv.float_to_str(mag / 1000.0, 2)} µs"
	else if(mag < 1000000000.0): "$sign${strconv.float_to_str(mag / 1000000.0, 2)} ms"
	else: "$sign${strconv.float_to_str(mag / 1000000000.0, 3)} s"
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
	nanos: int
]

// Captures the current monotonic timestamp via the OS clock.
bind Instant.now = fn() Instant: Instant[nanos: _monotonic_nanos()]

// Returns the [`Duration`] elapsed between `self` and the current monotonic instant.
bind Instant.elapsed = fn(self) Duration: {
	let now = _monotonic_nanos()
	Duration[nanos: now - self.nanos]
}

// Returns elapsed time since `self` in floating-point milliseconds.
bind Instant.elapsed_millis = fn(self) float: {
	let d = self.elapsed()
	d.to_millis()
}

// Returns elapsed time since `self` in whole nanoseconds.
bind Instant.elapsed_nanos = fn(self) int: {
	let d = self.elapsed()
	d.to_nanos()
}

// Returns the raw current monotonic clock value in nanoseconds.
bind now_nanos = fn() int: _monotonic_nanos()

// Returns the current monotonic clock value in floating-point milliseconds.
bind now_millis = fn() float: float(_monotonic_nanos()) / 1000000.0

// Suspends execution of the calling thread for at least `ms` milliseconds.
bind sleep_ms = fn(ms: int): {
	if(ms <= 0): func <-
	let sec = ms / 1000
	let nsec = (ms % 1000) * 1000000
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_nanosleep = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 101 else: 35
		unsafe {
			let req = alloc_raw[int](2)
			let rem = alloc_raw[int](2)
			ptr_write(req, sec)
			ptr_write(ptr_add(req, 1), nsec)
			for(true): {
				ptr_write(rem, 0)
				ptr_write(ptr_add(rem, 1), 0)
				let rc = syscall(sys_nanosleep, int(ptr_addr(req)), int(ptr_addr(rem)), 0, 0, 0, 0)
				if(rc != -4): for <-
				ptr_write(req, ptr_read(rem))
				ptr_write(ptr_add(req, 1), ptr_read(ptr_add(rem, 1)))
			}
			free_raw(rem)
			free_raw(req)
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let req = alloc_raw[int](2)
			ptr_write(req, sec)
			ptr_write(ptr_add(req, 1), nsec)
			let _ = _time_nanosleep(ptr_cast[u8](req), null_ptr[u8]())
			free_raw(req)
		}
	} else: {
		let _ = sec + nsec
	}
}

// Returns the three-letter English month abbreviation for `1..=12` (or `""` if out of range).
bind month_abbr = fn(m: int) string:
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
bind month_full = fn(m: int) string:
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
bind weekday_abbr = fn(w: int) string:
	if(w == 0): "Sun"
	else if(w == 1): "Mon"
	else if(w == 2): "Tue"
	else if(w == 3): "Wed"
	else if(w == 4): "Thu"
	else if(w == 5): "Fri"
	else if(w == 6): "Sat"
	else: ""

// Returns the full English weekday name for `0..=6` (`0` = Sunday, or `""` if out of range).
bind weekday_full = fn(w: int) string:
	if(w == 0): "Sunday"
	else if(w == 1): "Monday"
	else if(w == 2): "Tuesday"
	else if(w == 3): "Wednesday"
	else if(w == 4): "Thursday"
	else if(w == 5): "Friday"
	else if(w == 6): "Saturday"
	else: ""

// Formats a UTC timezone offset in seconds as `+HH:MM` (when `with_colon` is `true`) or `+HHMM`.
bind format_tz_offset = fn(offset_secs: int, with_colon: bool) string: {
	let is_neg = offset_secs < 0
	let total_mins = (if(is_neg): -offset_secs else: offset_secs) / 60
	let hours = total_mins / 60
	let mins = total_mins % 60
	let sign = if(is_neg): "-" else: "+"
	let h_str = strfmt.pad_int_zeros(hours, 2)
	let m_str = strfmt.pad_int_zeros(mins, 2)
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
// let dt = DateTime.must_new(2026, 1, 15, 9, 30, 0, false)
// assert(dt.to_date_only() == "2026-01-15")
// ```
@derive(Copyable)
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

bind _days_from_civil = fn(y: int, m: int, d: int) int: {
	let yr = if(m <= 2): y - 1 else: y
	let era = if(yr >= 0): yr / 400 else: (yr - 399) / 400
	let yoe = yr - era * 400
	let mp = if(m > 2): m - 3 else: m + 9
	let doy = (153 * mp + 2) / 5 + d - 1
	let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy
	era * 146097 + doe - 719468
}

// Floor division for positive `d`: rounds toward negative infinity so `n - q * d` is in `0..d`.
bind _floor_div = fn(n: int, d: int) int: {
	let q = n / d
	if(n % d < 0): q - 1 else: q
}

bind _build_datetime_from_epoch = fn(epoch_sec: int, nano: int, is_local: bool) DateTime: {
	if(not is_local): func <- _build_datetime(epoch_sec, nano, 0, "UTC", false)
	let info = _query_local_tz(epoch_sec)
	let zname = if(info.name.len() > 0): info.name.copy() else: format_tz_offset(info.offset, false)
	_build_datetime(epoch_sec, nano, info.offset, own zname, true)
}

// Returns the host timezone abbreviation (e.g. `"PST"`) at `epoch_sec`, falling back to the
// numeric offset (`"+0530"`) when the platform provides no name.
bind _local_zone_name = fn(epoch_sec: int, off: int) string: {
	let info = _query_local_tz(epoch_sec)
	if(info.name.len() > 0): info.name.copy() else: format_tz_offset(off, false)
}

// Builds a [`DateTime`] for `epoch_sec` (+ `nano`) viewed at a fixed UTC offset `off` with zone label `zone`.
bind _build_datetime = fn(epoch_sec: int, nano: int, off: int, zone: own string, is_local: bool) DateTime: {
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
		zone_name: own zone,
		unix_secs: epoch_sec,
		is_local: is_local,
	]
}

// Returns the current UTC [`DateTime`] read from the host OS real-time clock.
bind DateTime.now_utc = fn() DateTime: {
	let now_nanos = _epoch_nanos_now()
	let sec = now_nanos / 1000000000
	let nano = now_nanos % 1000000000
	_build_datetime_from_epoch(sec, nano, false)
}

// Returns the current local [`DateTime`] adjusted by the host system timezone offset.
bind DateTime.now_local = fn() DateTime: {
	let now_nanos = _epoch_nanos_now()
	let sec = now_nanos / 1000000000
	let nano = now_nanos % 1000000000
	_build_datetime_from_epoch(sec, nano, true)
}

// Constructs a [`DateTime`] from Unix epoch seconds, in local time when `is_local` is `true` or UTC otherwise.
bind DateTime.from_unix_secs = fn(secs: int, is_local: bool) DateTime: _build_datetime_from_epoch(
	secs,
	0,
	is_local,
)

// Constructs a [`DateTime`] from Unix epoch milliseconds, preserving millisecond sub-second precision.
bind DateTime.from_unix_millis = fn(ms: int, is_local: bool) DateTime: {
	let sec = _floor_div(ms, 1000)
	let nano = (ms - sec * 1000) * 1000000
	_build_datetime_from_epoch(sec, nano, is_local)
}

// Constructs a [`DateTime`] from Unix epoch nanoseconds.
bind DateTime.from_unix_nanos = fn(nanos: int, is_local: bool) DateTime: {
	let sec = _floor_div(nanos, 1000000000)
	let nano = nanos - sec * 1000000000
	_build_datetime_from_epoch(sec, nano, is_local)
}

// Smallest and largest years accepted by [`DateTime.new`] / [`DateTime.must_new`].
bind MIN_YEAR: int = -999999
bind MAX_YEAR: int = 999999

// Reports whether `year` is a Gregorian leap year.
bind is_leap_year = fn(year: int) bool: (year % 4 == 0 and year % 100 != 0) or year % 400 == 0

// Returns the number of days in `month` (`1..=12`) of `year`, or `0` for an invalid month.
bind days_in_month = fn(year: int, month: int) int:
	if(month == 2):
		if(is_leap_year(year)): 29 else: 28
	else if(month == 4 or month == 6 or month == 9 or month == 11): 30
	else if(month >= 1 and month <= 12): 31
	else: 0

// Returns a description of the first out-of-range civil component, or `none` if all are valid.
bind _civil_error = fn(year: int, month: int, day: int, hour: int, minute: int, second: int) ?string:
	if(year < MIN_YEAR or year > MAX_YEAR): "year $year out of range $MIN_YEAR..=$MAX_YEAR"
	else if(month < 1 or month > 12): "month $month out of range 1..=12"
	else if(day < 1 or day > days_in_month(year, month)): "day $day out of range 1..=${days_in_month(
		year,
		month,
	)} for $year-${strfmt.pad_int_zeros(month, 2)}"
	else if(hour < 0 or hour > 23): "hour $hour out of range 0..=23"
	else if(minute < 0 or minute > 59): "minute $minute out of range 0..=59"
	else if(second < 0 or second > 59): "second $second out of range 0..=59"
	else: none

// Constructs a [`DateTime`] from civil date/time components in UTC (`is_local == false`) or local time.
//
// # Errors
// Fails if any component is out of range (`month` outside `1..=12`, `day` past the end of the
// month, `hour > 23`, `minute > 59`, `second > 59`, or `year` outside [`MIN_YEAR`]`..=`[`MAX_YEAR`]).
bind DateTime.new = fn(
	year: int,
	month: int,
	day: int,
	hour: int,
	minute: int,
	second: int,
	is_local: bool,
) Result[DateTime]: {
	let err? = _civil_error(year, month, day, hour, minute, second)
	if(err? as msg): func <- err(Error.coded("invalid_datetime", msg))
	let utc_sec = _days_from_civil(year, month, day) * 86400 + hour * 3600 + minute * 60 + second
	let off = if(is_local): _query_local_tz(utc_sec).offset else: 0
	let sec = utc_sec - off
	value(_build_datetime_from_epoch(sec, 0, is_local))
}

// Constructs a [`DateTime`] from civil date/time components in UTC (`is_local == false`) or local time.
//
// Panics if any component is out of range; use [`DateTime.new`] to handle invalid input.
bind DateTime.must_new = fn(
	year: int,
	month: int,
	day: int,
	hour: int,
	minute: int,
	second: int,
	is_local: bool,
) DateTime: {
	let err? = _civil_error(year, month, day, hour, minute, second)
	if(err? as msg): panic("DateTime.must_new: $msg")
	DateTime.new(year, month, day, hour, minute, second, is_local): default panic
}

// Reports whether `self.year` is a Gregorian leap year.
bind DateTime.is_leap_year = fn(self) bool: is_leap_year(self.year)

// -----------------------------------------------------------------------------
// Time Format Constants (Mirroring Go Reference Formats & Standard Conventions)
// -----------------------------------------------------------------------------

// Go reference layout constant (`"01/02 03:04:05PM '06 -0700"`).
bind LAYOUT = "01/02 03:04:05PM '06 -0700"

// ANSI C date-time layout for [`DateTime.format_go`].
bind ANSIC = "Mon Jan _2 15:04:05 2006"

// Unix `date(1)` layout for [`DateTime.format_go`].
bind UNIX_DATE = "Mon Jan _2 15:04:05 MST 2006"

// Ruby date-time layout for [`DateTime.format_go`].
bind RUBY_DATE = "Mon Jan 02 15:04:05 -0700 2006"

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
bind RFC3339_NANO = "2006-01-02T15:04:05.999999999Z07:00"

// Short 12-hour clock layout (`"3:04PM"`).
bind KITCHEN = "3:04PM"

// Syslog-style short timestamp layout (`"Jan _2 15:04:05"`).
bind STAMP = "Jan _2 15:04:05"

// Syslog timestamp layout with milliseconds.
bind STAMP_MILLI = "Jan _2 15:04:05.000"

// Syslog timestamp layout with microseconds.
bind STAMP_MICRO = "Jan _2 15:04:05.000000"

// Syslog timestamp layout with nanoseconds.
bind STAMP_NANO = "Jan _2 15:04:05.000000000"

// Common SQL / log timestamp layout (`"2006-01-02 15:04:05"`).
bind DATETIME_FORMAT = "2006-01-02 15:04:05"

// Calendar date layout (`"2006-01-02"`).
bind DATE_ONLY = "2006-01-02"

// Wall clock time-of-day layout (`"15:04:05"`).
bind TIME_ONLY = "15:04:05"

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
bind DateTime.format = fn(self, pattern: string) string: {
	let out = ""
	let n = pattern.len()
	let i = 0

	for(i < n): {
		let ch = pattern[i..i + 1]
		if(ch == "%" and i + 1 < n): {
			let spec = pattern[i + 1..i + 2]
			if(spec == "Y"): {
				out.push(strfmt.pad_int_zeros(self.year, 4))
				i += 2
			} else if(spec == "y"): {
				out.push(strfmt.pad_int_zeros(self.year % 100, 2))
				i += 2
			} else if(spec == "m"): {
				out.push(strfmt.pad_int_zeros(self.month, 2))
				i += 2
			} else if(spec == "d"): {
				out.push(strfmt.pad_int_zeros(self.day, 2))
				i += 2
			} else if(spec == "e"): {
				out.push(strfmt.pad_int_spaces(self.day, 2))
				i += 2
			} else if(spec == "H"): {
				out.push(strfmt.pad_int_zeros(self.hour, 2))
				i += 2
			} else if(spec == "I"): {
				let h12 = if(self.hour == 0): 12
				else if(self.hour > 12): self.hour - 12
				else: self.hour
				out.push(strfmt.pad_int_zeros(h12, 2))
				i += 2
			} else if(spec == "M"): {
				out.push(strfmt.pad_int_zeros(self.minute, 2))
				i += 2
			} else if(spec == "S"): {
				out.push(strfmt.pad_int_zeros(self.second, 2))
				i += 2
			} else if(spec == "L"): {
				out.push(strfmt.pad_int_zeros(self.nanosecond / 1000000, 3))
				i += 2
			} else if(spec == "f"): {
				out.push(strfmt.pad_int_zeros(self.nanosecond / 1000, 6))
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
					"${strfmt.pad_int_zeros(self.year, 4)}-${strfmt.pad_int_zeros(self.month, 2)}-${strfmt.pad_int_zeros(
						self.day,
						2,
					)}"
				)
				i += 2
			} else if(spec == "T"): {
				out.push(
					"${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}:${strfmt.pad_int_zeros(
						self.second,
						2,
					)}"
				)
				i += 2
			} else if(spec == "R"): {
				out.push(
					"${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}"
				)
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

// Returns the length of a Go fractional-second token (`.000`, `.999`, `,000`, ...) starting at
// `layout[i]`, counting the repeated `0`/`9` digits, or `0` if none starts there. Like Go, the
// run must not be followed by another digit.
bind _frac_run_len = fn(layout: string, i: int) int: {
	let n = layout.len()
	if(i + 1 >= n): func <- 0
	let sep = layout.byte_at(i)
	if(sep != u8(46) and sep != u8(44)): func <- 0
	let digit = layout.byte_at(i + 1)
	if(digit != u8(48) and digit != u8(57)): func <- 0
	let j = i + 1
	for(j < n and layout.byte_at(j) == digit): j += 1
	if(j < n and layout.byte_at(j) >= u8(48) and layout.byte_at(j) <= u8(57)): func <- 0
	j - i - 1
}

// Formats `nanos` as a fractional-second suffix with `digits` digits (capped at 9) after `sep`.
// When `trim` is `true` (Go `.999`), trailing zeros are removed and an all-zero fraction is omitted
// entirely, including `sep`.
bind _format_frac = fn(nanos: int, digits: int, trim: bool, sep: string) string: {
	let width = if(digits > 9): 9 else: digits
	let frac = strfmt.pad_int_zeros(nanos, 9)[0..width].to_string()
	if(trim): {
		let end = frac.len()
		for(end > 0 and frac.byte_at(end - 1) == u8(48)): end -= 1
		if(end == 0): func <- ""
		func <- "$sep${frac[0..end]}"
	}
	"$sep$frac"
}

// Formats this [`DateTime`] using Go reference-time layout tokens (`"2006-01-02 15:04:05Z07:00"`).
//
// Fractional seconds use Go semantics: `.000`/`,000` print exactly that many digits, while
// `.999`/`,999` drop trailing zeros and omit the fraction (and separator) when it is zero.
bind DateTime.format_go = fn(self, layout: string) string: {
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
			out.push(strfmt.pad_int_zeros(self.year, 4))
			i += 4
		} else if(rem.starts_with("06")): {
			out.push(strfmt.pad_int_zeros(self.year % 100, 2))
			i += 2
		} else if(rem.starts_with("01")): {
			out.push(strfmt.pad_int_zeros(self.month, 2))
			i += 2
		} else if(rem.starts_with("02")): {
			out.push(strfmt.pad_int_zeros(self.day, 2))
			i += 2
		} else if(rem.starts_with("_2")): {
			out.push(strfmt.pad_int_spaces(self.day, 2))
			i += 2
		} else if(rem.starts_with("15")): {
			out.push(strfmt.pad_int_zeros(self.hour, 2))
			i += 2
		} else if(rem.starts_with("03")): {
			let h12 = if(self.hour == 0): 12
			else if(self.hour > 12): self.hour - 12
			else: self.hour
			out.push(strfmt.pad_int_zeros(h12, 2))
			i += 2
		} else if(rem.starts_with("04")): {
			out.push(strfmt.pad_int_zeros(self.minute, 2))
			i += 2
		} else if(rem.starts_with("05")): {
			out.push(strfmt.pad_int_zeros(self.second, 2))
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
		} else if(_frac_run_len(layout, i) > 0): {
			let run = _frac_run_len(layout, i)
			let trim = layout.byte_at(i + 1) == u8(57)
			out.push(_format_frac(self.nanosecond, run, trim, layout[i..i + 1]))
			i += run + 1
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

// Formats this [`DateTime`] using the Go reference [`LAYOUT`] convention (`"MM/DD hh:mm:ssAM 'YY ±HHMM"`).
bind DateTime.to_layout = fn(self) string: {
	let mo = strfmt.pad_int_zeros(self.month, 2)
	let dy = strfmt.pad_int_zeros(self.day, 2)
	let h12 = if(self.hour == 0): 12
	else if(self.hour > 12): self.hour - 12
	else: self.hour
	let hr = strfmt.pad_int_zeros(h12, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let sc = strfmt.pad_int_zeros(self.second, 2)
	let ampm = if(self.hour < 12): "AM" else: "PM"
	let yr = strfmt.pad_int_zeros(self.year % 100, 2)
	let tz = format_tz_offset(self.offset_secs, false)
	"$mo/$dy $hr:$mi:$sc$ampm '$yr $tz"
}

// Formats this [`DateTime`] in [`ANSIC`] layout (`"Mon Jan _2 15:04:05 2006"`).
bind DateTime.to_ansic = fn(self) string: {
	let w = weekday_abbr(self.weekday)
	let m = month_abbr(self.month)
	let d = strfmt.pad_int_spaces(self.day, 2)
	let h = strfmt.pad_int_zeros(self.hour, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let s = strfmt.pad_int_zeros(self.second, 2)
	let y = strfmt.pad_int_zeros(self.year, 4)
	"$w $m $d $h:$mi:$s $y"
}

// Formats this [`DateTime`] in [`UNIX_DATE`] layout with timezone label.
bind DateTime.to_unix_date = fn(self) string: {
	let w = weekday_abbr(self.weekday)
	let m = month_abbr(self.month)
	let d = strfmt.pad_int_spaces(self.day, 2)
	let h = strfmt.pad_int_zeros(self.hour, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let s = strfmt.pad_int_zeros(self.second, 2)
	let y = strfmt.pad_int_zeros(self.year, 4)
	"$w $m $d $h:$mi:$s ${self.zone_name} $y"
}

// Formats this [`DateTime`] in [`RUBY_DATE`] layout with numeric timezone offset.
bind DateTime.to_ruby_date = fn(self) string: {
	let w = weekday_abbr(self.weekday)
	let m = month_abbr(self.month)
	let d = strfmt.pad_int_zeros(self.day, 2)
	let h = strfmt.pad_int_zeros(self.hour, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let s = strfmt.pad_int_zeros(self.second, 2)
	let tz = format_tz_offset(self.offset_secs, false)
	let y = strfmt.pad_int_zeros(self.year, 4)
	"$w $m $d $h:$mi:$s $tz $y"
}

// Formats this [`DateTime`] in [`RFC822`] layout with timezone label.
bind DateTime.to_rfc822 = fn(self) string: {
	let d = strfmt.pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = strfmt.pad_int_zeros(self.year % 100, 2)
	let h = strfmt.pad_int_zeros(self.hour, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	"$d $m $y $h:$mi ${self.zone_name}"
}

// Formats this [`DateTime`] in [`RFC822Z`] layout with numeric timezone offset.
bind DateTime.to_rfc822z = fn(self) string: {
	let d = strfmt.pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = strfmt.pad_int_zeros(self.year % 100, 2)
	let h = strfmt.pad_int_zeros(self.hour, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let tz = format_tz_offset(self.offset_secs, false)
	"$d $m $y $h:$mi $tz"
}

// Formats this [`DateTime`] in [`RFC850`] layout with full weekday name.
bind DateTime.to_rfc850 = fn(self) string: {
	let w = weekday_full(self.weekday)
	let d = strfmt.pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = strfmt.pad_int_zeros(self.year % 100, 2)
	let h = strfmt.pad_int_zeros(self.hour, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let s = strfmt.pad_int_zeros(self.second, 2)
	"$w, $d-$m-$y $h:$mi:$s ${self.zone_name}"
}

// Formats this [`DateTime`] in [`RFC1123`] HTTP header layout with timezone label.
bind DateTime.to_rfc1123 = fn(self) string: {
	let w = weekday_abbr(self.weekday)
	let d = strfmt.pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = strfmt.pad_int_zeros(self.year, 4)
	let h = strfmt.pad_int_zeros(self.hour, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let s = strfmt.pad_int_zeros(self.second, 2)
	"$w, $d $m $y $h:$mi:$s ${self.zone_name}"
}

// Formats this [`DateTime`] in [`RFC1123Z`] layout with numeric timezone offset.
bind DateTime.to_rfc1123z = fn(self) string: {
	let w = weekday_abbr(self.weekday)
	let d = strfmt.pad_int_zeros(self.day, 2)
	let m = month_abbr(self.month)
	let y = strfmt.pad_int_zeros(self.year, 4)
	let h = strfmt.pad_int_zeros(self.hour, 2)
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let s = strfmt.pad_int_zeros(self.second, 2)
	let tz = format_tz_offset(self.offset_secs, false)
	"$w, $d $m $y $h:$mi:$s $tz"
}

// Formats this [`DateTime`] in [`RFC3339`] / [`ISO8601`] layout (`"2006-01-02T15:04:05Z07:00"`).
bind DateTime.to_rfc3339 = fn(self) string: {
	let d = "${strfmt.pad_int_zeros(self.year, 4)}-${strfmt.pad_int_zeros(self.month, 2)}-${strfmt.pad_int_zeros(
		self.day,
		2,
	)}"
	let tm = "${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}:${strfmt.pad_int_zeros(
		self.second,
		2,
	)}"
	let tz = if(self.offset_secs == 0): "Z" else: format_tz_offset(self.offset_secs, true)
	"${d}T$tm$tz"
}

// Alias for [`DateTime.to_rfc3339`].
bind DateTime.to_iso = fn(self) string: self.to_rfc3339()

// Formats this [`DateTime`] in [`RFC3339_NANO`] layout, with up to 9 fractional digits and
// trailing zeros removed (the fraction is omitted when it is zero), matching Go.
bind DateTime.to_rfc3339_nano = fn(self) string: {
	let d = "${strfmt.pad_int_zeros(self.year, 4)}-${strfmt.pad_int_zeros(self.month, 2)}-${strfmt.pad_int_zeros(
		self.day,
		2,
	)}"
	let ns = _format_frac(self.nanosecond, 9, true, ".")
	let tm = "${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}:${strfmt.pad_int_zeros(
		self.second,
		2,
	)}$ns"
	let tz = if(self.offset_secs == 0): "Z" else: format_tz_offset(self.offset_secs, true)
	"${d}T$tm$tz"
}

// Formats this [`DateTime`] in 12-hour [`KITCHEN`] clock format (`"3:04PM"`).
bind DateTime.to_kitchen = fn(self) string: {
	let h12 = if(self.hour == 0): 12
	else if(self.hour > 12): self.hour - 12
	else: self.hour
	let mi = strfmt.pad_int_zeros(self.minute, 2)
	let ampm = if(self.hour < 12): "AM" else: "PM"
	"$h12:$mi$ampm"
}

// Formats this [`DateTime`] in [`STAMP`] layout (`"Jan _2 15:04:05"`).
bind DateTime.to_stamp = fn(self) string: {
	let m = month_abbr(self.month)
	let d = strfmt.pad_int_spaces(self.day, 2)
	let tm = "${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}:${strfmt.pad_int_zeros(
		self.second,
		2,
	)}"
	"$m $d $tm"
}

// Formats this [`DateTime`] in [`STAMP_MILLI`] layout with millisecond precision.
bind DateTime.to_stamp_milli = fn(self) string: {
	let m = month_abbr(self.month)
	let d = strfmt.pad_int_spaces(self.day, 2)
	let ms = strfmt.pad_int_zeros(self.nanosecond / 1000000, 3)
	let tm = "${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}:${strfmt.pad_int_zeros(
		self.second,
		2,
	)}.$ms"
	"$m $d $tm"
}

// Formats this [`DateTime`] in [`STAMP_MICRO`] layout with microsecond precision.
bind DateTime.to_stamp_micro = fn(self) string: {
	let m = month_abbr(self.month)
	let d = strfmt.pad_int_spaces(self.day, 2)
	let us = strfmt.pad_int_zeros(self.nanosecond / 1000, 6)
	let tm = "${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}:${strfmt.pad_int_zeros(
		self.second,
		2,
	)}.$us"
	"$m $d $tm"
}

// Formats this [`DateTime`] in [`STAMP_NANO`] layout with nanosecond precision.
bind DateTime.to_stamp_nano = fn(self) string: {
	let m = month_abbr(self.month)
	let d = strfmt.pad_int_spaces(self.day, 2)
	let ns = strfmt.pad_int_zeros(self.nanosecond, 9)
	let tm = "${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}:${strfmt.pad_int_zeros(
		self.second,
		2,
	)}.$ns"
	"$m $d $tm"
}

// Formats this [`DateTime`] as `"YYYY-MM-DD HH:MM:SS"`.
bind DateTime.to_datetime_string = fn(self) string: {
	let d = "${strfmt.pad_int_zeros(self.year, 4)}-${strfmt.pad_int_zeros(self.month, 2)}-${strfmt.pad_int_zeros(
		self.day,
		2,
	)}"
	let tm = "${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(self.minute, 2)}:${strfmt.pad_int_zeros(
		self.second,
		2,
	)}"
	"$d $tm"
}

// Returns the default string representation ([`DateTime.to_datetime_string`]).
bind DateTime.to_string = fn(self) string: self.to_datetime_string()

// Formats only the calendar date portion as `"YYYY-MM-DD"`.
bind DateTime.to_date_only = fn(self) string: "${strfmt.pad_int_zeros(self.year, 4)}-${strfmt.pad_int_zeros(
	self.month,
	2,
)}-${strfmt.pad_int_zeros(self.day, 2)}"

// Alias for [`DateTime.to_date_only`].
bind DateTime.to_date_string = fn(self) string: self.to_date_only()

// Formats only the wall-clock time portion as `"HH:MM:SS"`.
bind DateTime.to_time_only = fn(self) string: "${strfmt.pad_int_zeros(self.hour, 2)}:${strfmt.pad_int_zeros(
	self.minute,
	2,
)}:${strfmt.pad_int_zeros(self.second, 2)}"

// Alias for [`DateTime.to_time_only`].
bind DateTime.to_time_string = fn(self) string: self.to_time_only()

// Formats this [`DateTime`] in [`RFC2822`] email layout (`"Mon, 02 Jan 2006 15:04:05 -0700"`).
bind DateTime.to_rfc2822 = fn(self) string: self.to_rfc1123z()

// Returns `self` shifted by `dsec` seconds plus `dnano` nanoseconds (`|dnano| < 1e9`), keeping
// the same zone semantics (host local time, UTC, or the parsed fixed offset).
bind DateTime._shift = fn(self, dsec: int, dnano: int) DateTime: {
	let sec = self.unix_secs + dsec
	let n = self.nanosecond + dnano
	if(n >= 1000000000): {
		sec += 1
		n -= 1000000000
	} else if(n < 0): {
		sec -= 1
		n += 1000000000
	}
	if(self.is_local): _build_datetime_from_epoch(sec, n, true)
	else: _build_datetime(sec, n, self.offset_secs, self.zone_name.copy(), false)
}

// Returns a new [`DateTime`] offset forward by [`Duration`] `d`.
//
// Works in whole seconds plus nanoseconds, so it does not overflow for dates far from 1970.
bind DateTime.add_duration = fn(self, d: Duration) DateTime: {
	let dsec = _floor_div(d.nanos, 1000000000)
	self._shift(dsec, d.nanos - dsec * 1000000000)
}

// Returns a new [`DateTime`] offset backward by [`Duration`] `d`.
bind DateTime.sub_duration = fn(self, d: Duration) DateTime: {
	let dsec = _floor_div(d.nanos, 1000000000)
	self._shift(-dsec, -(d.nanos - dsec * 1000000000))
}

// Computes the signed elapsed [`Duration`] `self - other`, or `none` if it does not fit in a
// [`Duration`] (about ±292 years).
bind DateTime.diff = fn(self, other: DateTime) ?Duration: {
	let secs = self.unix_secs - other.unix_secs
	let ns = self.nanosecond - other.nanosecond
	let max_secs = 9223372036
	if(secs > max_secs or secs < -max_secs): func <- none
	let base = secs * 1000000000
	let max_int = 9223372036854775807
	if(base > 0 and ns > max_int - base): func <- none
	if(base < 0 and ns < -max_int - 1 - base): func <- none
	Duration[nanos: base + ns]
}

// Computes the signed elapsed [`Duration`] `self - other`.
//
// Saturates to the minimum/maximum [`Duration`] when the span exceeds about ±292 years; use
// [`DateTime.diff`] to detect that case.
bind DateTime.diff_saturating = fn(self, other: DateTime) Duration: {
	let d? = self.diff(other)
	if(d? as d): func <- d
	if(self.unix_secs > other.unix_secs): Duration[nanos: 9223372036854775807]
	else: Duration[nanos: -9223372036854775807 - 1]
}

// -----------------------------------------------------------------------------
// String Extension for Parsing DateTime
// -----------------------------------------------------------------------------

// Parses exactly `count` ASCII digits at `s[start..start + count]`, or returns `none`.
bind _parse_digits = fn(s: string, start: int, count: int) ?int: {
	if(start < 0 or start + count > s.len()): func <- none
	let v = 0
	for(i in start..start + count): {
		let b = s.byte_at(i)
		if(b < u8(48) or b > u8(57)): func <- none
		v = v * 10 + int(b) - 48
	}
	v
}

// Parses an ISO 8601 / RFC 3339 date or date-time string into a [`DateTime`], or returns `none`.
//
// Accepts `YYYY-MM-DD`, optionally followed by `T`, `t`, or a space and `HH:MM` or `HH:MM:SS`,
// an optional fraction (`.` or `,` plus 1+ digits; digits past nanoseconds are truncated), and an
// optional zone designator: `Z`/`z`, `±HH:MM`, `±HHMM`, or `±HH`. Times without a zone are UTC.
// Times with a non-zero offset keep that fixed offset (`offset_secs`, zone label `"+0530"`).
// Out-of-range components (e.g. month 13, `25:00`, offset `+24:00`) are rejected.
bind string.to_datetime = fn(self) ?DateTime: {
	let s = self.trim()
	let n = s.len()
	if(n < 10 or s.byte_at(4) != u8(45) or s.byte_at(7) != u8(45)): func <- none
	let y = _parse_digits(s, 0, 4) ?? func <- none
	let mo = _parse_digits(s, 5, 2) ?? func <- none
	let d = _parse_digits(s, 8, 2) ?? func <- none
	let h = 0
	let mi = 0
	let sc = 0
	let nano = 0
	let off = 0
	let i = 10
	if(i < n): {
		let sep = s.byte_at(i)

		// `T`, `t`, or space separates the date from the time.
		if(sep != u8(84) and sep != u8(116) and sep != u8(32)): func <- none
		h = _parse_digits(s, i + 1, 2) ?? func <- none
		if(i + 3 >= n or s.byte_at(i + 3) != u8(58)): func <- none
		mi = _parse_digits(s, i + 4, 2) ?? func <- none
		i += 6
		if(i < n and s.byte_at(i) == u8(58)): {
			sc = _parse_digits(s, i + 1, 2) ?? func <- none
			i += 3
		}
		if(i < n and (s.byte_at(i) == u8(46) or s.byte_at(i) == u8(44))): {
			i += 1
			let digits = 0
			let scale = 100000000
			for(i < n and s.byte_at(i) >= u8(48) and s.byte_at(i) <= u8(57)): {
				if(digits < 9): {
					nano += (int(s.byte_at(i)) - 48) * scale
					scale /= 10
				}
				digits += 1
				i += 1
			}
			if(digits == 0): func <- none
		}
		if(i < n): {
			let z = s.byte_at(i)
			if(z == u8(90) or z == u8(122)): i += 1
			else if(z == u8(43) or z == u8(45)): {
				let oh = _parse_digits(s, i + 1, 2) ?? func <- none
				i += 3
				let om = 0
				if(i < n and s.byte_at(i) == u8(58)): {
					om = _parse_digits(s, i + 1, 2) ?? func <- none
					i += 3
				} else if(i + 2 <= n): {
					om = _parse_digits(s, i, 2) ?? func <- none
					i += 2
				}
				if(oh > 23 or om > 59): func <- none
				off = oh * 3600 + om * 60
				if(z == u8(45)): off = -off
			} else: func <- none
		}
		if(i != n): func <- none
	}
	let err? = _civil_error(y, mo, d, h, mi, sc)
	if(err? != none): func <- none
	let local_sec = _days_from_civil(y, mo, d) * 86400 + h * 3600 + mi * 60 + sc
	let zone = if(off == 0): "UTC" else: format_tz_offset(off, false)
	_build_datetime(local_sec - off, nano, off, own zone, false)
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("time duration conversions, getters, and formatting")
bind test_time_duration = fn(): {
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
bind test_time_instant = fn(): {
	let start = Instant.now()
	assert(start.nanos > 0, "start nanos positive")

	let elapsed = start.elapsed()
	assert(elapsed.to_nanos() >= 0, "elapsed non-negative")
	assert(start.elapsed_millis() >= 0.0, "elapsed millis non-negative")
	assert(now_nanos() > 0, "now_nanos positive")
	assert(now_millis() > 0.0, "now_millis positive")
}

@test("time DateTime creation, formatting, duration arithmetic, and parsing")
bind test_time_datetime_and_parse = fn(): {
	let dt = DateTime.must_new(2026, 11, 4, 15, 30, 45, false)
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
	let diff = dt_plus.diff(dt) ?? panic("diff overflow")
	assert(diff.to_secs() == 15.0, "diff is 15 seconds")
}

@test("time pre-1970 millis/nanos normalize to a non-negative nanosecond field")
bind test_time_pre_epoch_subsecond = fn(): {
	let m1 = DateTime.from_unix_millis(-1, false)
	assert(m1.year == 1969 and m1.month == 12 and m1.day == 31, "-1 ms is 1969-12-31")
	assert(m1.hour == 23 and m1.minute == 59 and m1.second == 59, "-1 ms is 23:59:59")
	assert(m1.nanosecond == 999000000, "-1 ms has nanosecond 999_000_000")
	assert(m1.unix_secs == -1, "-1 ms floors to unix second -1")

	let m2 = DateTime.from_unix_millis(-1500, false)
	assert(m2.second == 58 and m2.nanosecond == 500000000, "-1500 ms is :58.500")
	assert(m2.unix_secs == -2, "-1500 ms floors to unix second -2")

	let m3 = DateTime.from_unix_millis(-2000, false)
	assert(m3.second == 58 and m3.nanosecond == 0, "-2000 ms is exactly :58")

	let n1 = DateTime.from_unix_nanos(-1, false)
	assert(n1.second == 59 and n1.nanosecond == 999999999, "-1 ns is :59.999999999")
	assert(n1.unix_secs == -1, "-1 ns floors to unix second -1")

	let p1 = DateTime.from_unix_millis(1500, false)
	assert(p1.second == 1 and p1.nanosecond == 500000000, "positive millis unchanged")

	let epoch = DateTime.from_unix_secs(0, false)
	let before = epoch.sub_duration(Duration.from_nanos(250))
	assert(
		before.unix_secs == -1 and before.nanosecond == 999999750,
		"sub_duration across the epoch",
	)
	let after = before.add_duration(Duration.from_nanos(250))
	assert(after.unix_secs == 0 and after.nanosecond == 0, "add_duration back to the epoch")
}

@test("time negative Duration.to_string picks units by magnitude")
bind test_time_negative_duration_string = fn(): {
	assert(Duration.from_nanos(-50).to_string() == "-50 ns", "negative ns")
	assert(Duration.from_nanos(-5000).to_string() == "-5.00 µs", "negative µs")
	assert(Duration.from_nanos(-2500000).to_string() == "-2.50 ms", "negative ms")
	assert(Duration.from_nanos(-1500000000).to_string() == "-1.500 s", "negative s")
	assert(Duration.from_nanos(5000).to_string() == "5.00 µs", "positive µs unchanged")
	assert(Duration.from_nanos(0).to_string() == "0 ns", "zero")
	let min_str = Duration.from_nanos(-9223372036854775807 - 1).to_string()
	assert(min_str.starts_with("-") and min_str.ends_with(" s"), "minimum int does not overflow")
}

@test("time arithmetic does not overflow for dates far from 1970")
bind test_time_arith_far_dates = fn(): {
	let far = DateTime.must_new(2400, 3, 1, 12, 0, 0, false)
	let later = far.add_duration(Duration.from_nanos(1500000000))
	assert(
		later.year == 2400 and later.second == 1 and later.nanosecond == 500000000,
		"add_duration in 2400",
	)
	let earlier = far.sub_duration(Duration.from_nanos(1))
	assert(
		earlier.hour == 11 and earlier.minute == 59 and earlier.second == 59,
		"sub_duration in 2400",
	)
	assert(earlier.nanosecond == 999999999, "sub_duration borrows a second")
	assert(
		(later.diff(far) ?? panic("diff overflow")).to_nanos() == 1500000000,
		"diff between 2400 dates",
	)

	let ancient = DateTime.must_new(1500, 1, 1, 0, 0, 0, false)
	assert(ancient.add_duration(Duration.from_secs(86400.0)).day == 2, "add a day in 1500")

	let near = DateTime.must_new(2000, 1, 1, 0, 0, 0, false)
	let mid = DateTime.must_new(2200, 1, 1, 0, 0, 0, false)
	let span? = mid.diff(near)
	assert(span? != none, "200-year span fits in a Duration")
	let huge? = far.diff(ancient)
	assert(huge? == none, "900-year span does not fit in a Duration")
	assert(
		far.diff_saturating(ancient).to_nanos() == 9223372036854775807,
		"diff_saturating saturates high",
	)
	assert(
		ancient.diff_saturating(far).to_nanos() == -9223372036854775807 - 1,
		"diff_saturating saturates low",
	)

	let min_d = Duration.from_nanos(-9223372036854775807 - 1)
	let shifted = mid.sub_duration(min_d)
	assert(shifted.year == 2492, "sub_duration of minimum Duration does not overflow")
}

@test("time format_go .999 trims trailing zeros and ,000 uses a comma")
bind test_time_format_go_fraction = fn(): {
	let dt = DateTime.from_unix_nanos(1700000000120000000, false)
	assert(dt.format_go("05.999") == "20.12", ".999 trims trailing zeros")
	assert(dt.format_go("05.000") == "20.120", ".000 keeps fixed digits")
	assert(dt.format_go("05.999999999") == "20.12", ".999999999 trims")
	assert(dt.format_go("05,000") == "20,120", ",000 uses a comma separator")
	assert(dt.format_go("05.9") == "20.1", "single 9 digit")
	assert(dt.format_go("05.0") == "20.1", "single 0 digit")
	let whole = DateTime.from_unix_secs(1700000000, false)
	assert(whole.format_go("05.999") == "20", "zero fraction omits separator")
	assert(whole.format_go("05.000") == "20.000", "zero fraction keeps fixed digits")
	assert(
		whole.format_go("15.0007") == "22.0007",
		"a 0-run followed by a digit is not a fraction token",
	)
	assert(dt.format_go(RFC3339_NANO) == "2023-11-14T22:13:20.12Z", "RFC3339_NANO trims")
	assert(dt.to_rfc3339_nano() == "2023-11-14T22:13:20.12Z", "to_rfc3339_nano matches Go")
}

@test("time local DateTime uses a real zone name")
bind test_time_local_zone_name = fn(): {
	let local = DateTime.from_unix_secs(1700000000, true)
	assert(local.zone_name.len() > 0, "local zone name is non-empty")
	assert(local.zone_name != "LOCAL", "local zone name is not the LOCAL placeholder")
	assert(local.format_go("MST") == local.zone_name, "MST prints the zone name")
	assert(local.format("%Z") == local.zone_name, "%Z prints the zone name")
	assert(DateTime.from_unix_secs(0, false).format_go("MST") == "UTC", "UTC zone name")
}

@test("time DateTime components are validated")
bind test_time_validation = fn(): {
	assert(DateTime.new(2024, 2, 29, 0, 0, 0, false).is_ok(), "leap day 2024 is valid")
	assert(DateTime.new(2023, 2, 29, 0, 0, 0, false).is_err(), "Feb 29 2023 is invalid")
	assert(DateTime.new(1900, 2, 29, 0, 0, 0, false).is_err(), "1900 is not a leap year")
	assert(DateTime.new(2000, 2, 29, 0, 0, 0, false).is_ok(), "2000 is a leap year")
	assert(DateTime.new(2024, 13, 1, 0, 0, 0, false).is_err(), "month 13")
	assert(DateTime.new(2024, 0, 1, 0, 0, 0, false).is_err(), "month 0")
	assert(DateTime.new(2024, 4, 31, 0, 0, 0, false).is_err(), "April 31")
	assert(DateTime.new(2024, 1, 0, 0, 0, 0, false).is_err(), "day 0")
	assert(DateTime.new(2024, 1, 1, 24, 0, 0, false).is_err(), "hour 24")
	assert(DateTime.new(2024, 1, 1, 0, 60, 0, false).is_err(), "minute 60")
	assert(DateTime.new(2024, 1, 1, 0, 0, 60, false).is_err(), "second 60")
	assert(DateTime.new(2024, 1, 1, -1, 0, 0, false).is_err(), "negative hour")
	assert(DateTime.new(MAX_YEAR + 1, 1, 1, 0, 0, 0, false).is_err(), "year above MAX_YEAR")
	let err = DateTime.new(2023, 2, 29, 0, 0, 0, false).unwrap_err()
	assert(err.has_code("invalid_datetime"), "error code")
	assert(err.message().contains("day 29"), "error names the bad component")
	assert(days_in_month(2024, 2) == 29 and days_in_month(2023, 2) == 28, "days_in_month February")
	assert(days_in_month(2024, 13) == 0, "days_in_month invalid month")
}

@test("time ISO 8601 parsing of Z, fractions, and offsets")
bind test_time_iso8601_parse = fn(): {
	let z = "2024-03-10T12:34:56Z".to_datetime() ?? DateTime.must_new(1, 1, 1, 0, 0, 0, false)
	assert(z.unix_secs == 1710074096 and z.offset_secs == 0, "Z suffix is UTC")

	let frac = "2024-03-10T12:34:56.123456789Z".to_datetime() ?? DateTime.must_new(
		1,
		1,
		1,
		0,
		0,
		0,
		false,
	)
	assert(frac.nanosecond == 123456789, "nanosecond fraction")
	let short = "2024-03-10T12:34:56.5Z".to_datetime() ?? DateTime.must_new(1, 1, 1, 0, 0, 0, false)
	assert(short.nanosecond == 500000000, "one-digit fraction")
	let long = "2024-03-10T12:34:56.1234567891234Z".to_datetime() ?? DateTime.must_new(
		1,
		1,
		1,
		0,
		0,
		0,
		false,
	)
	assert(long.nanosecond == 123456789, "fraction past nanoseconds is truncated")
	let comma = "2024-03-10T12:34:56,25Z".to_datetime() ?? DateTime.must_new(
		1,
		1,
		1,
		0,
		0,
		0,
		false,
	)
	assert(comma.nanosecond == 250000000, "comma fraction separator")

	let plus = "2024-03-10T18:04:56+05:30".to_datetime() ?? DateTime.must_new(
		1,
		1,
		1,
		0,
		0,
		0,
		false,
	)
	assert(plus.unix_secs == 1710074096, "+05:30 converts to the same instant")
	assert(
		plus.offset_secs == 19800 and plus.hour == 18 and plus.minute == 4,
		"+05:30 keeps local fields",
	)
	assert(
		plus.to_rfc3339() == "2024-03-10T18:04:56+05:30",
		"+05:30 round-trips through to_rfc3339",
	)
	assert(plus.zone_name == "+0530", "fixed offset zone label")

	let minus = "2024-03-10T07:34:56-0500".to_datetime() ?? DateTime.must_new(
		1,
		1,
		1,
		0,
		0,
		0,
		false,
	)
	assert(minus.unix_secs == 1710074096 and minus.offset_secs == -18000, "-0500 offset")
	let hours_only = "2024-03-10T14:34:56+02".to_datetime() ?? DateTime.must_new(
		1,
		1,
		1,
		0,
		0,
		0,
		false,
	)
	assert(hours_only.unix_secs == 1710074096, "+HH offset")
	let lower = "2024-03-10t12:34:56z".to_datetime() ?? DateTime.must_new(1, 1, 1, 0, 0, 0, false)
	assert(lower.unix_secs == 1710074096, "lowercase t and z")
	let no_secs = "2024-03-10T12:34Z".to_datetime() ?? DateTime.must_new(1, 1, 1, 0, 0, 0, false)
	assert(no_secs.unix_secs == 1710074040, "HH:MM without seconds")
	let shifted = plus.add_duration(Duration.from_secs(60.0))
	assert(
		shifted.offset_secs == 19800 and shifted.minute == 5,
		"arithmetic keeps the fixed offset",
	)

	let date_only = "2024-03-10".to_datetime() ?? DateTime.must_new(1, 1, 1, 0, 0, 0, false)
	assert(date_only.hour == 0 and date_only.offset_secs == 0, "date only is UTC midnight")
	let spaced = "2024-03-10 12:34:56".to_datetime() ?? DateTime.must_new(1, 1, 1, 0, 0, 0, false)
	assert(spaced.unix_secs == 1710074096, "space separator without zone is UTC")

	assert("2024-13-10T12:34:56Z".to_datetime() == none, "month 13 rejected")
	assert("2024-02-30".to_datetime() == none, "Feb 30 rejected")
	assert("2024-03-10T25:00:00Z".to_datetime() == none, "hour 25 rejected")
	assert("2024-03-10T12:34:56+24:00".to_datetime() == none, "offset hour 24 rejected")
	assert("2024-03-10T12:34:56.Z".to_datetime() == none, "empty fraction rejected")
	assert("2024-03-10T12:34:56Zjunk".to_datetime() == none, "trailing input rejected")
	assert("2024-03-10X12:34:56".to_datetime() == none, "bad separator rejected")
	assert("2024/03/10".to_datetime() == none, "slash date rejected")
	assert("+024-03-10".to_datetime() == none, "sign in year rejected")
}
