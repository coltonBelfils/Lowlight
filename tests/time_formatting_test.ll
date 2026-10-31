import "std/time.ll" as time

bind main = () {
    // 1. Fixed test date: 2026-10-31 15:30:45 UTC
    let dt = time.DateTime.new(2026, 10, 31, 15, 30, 45, false)

    assert(dt.year == 2026)
    assert(dt.month == 10)
    assert(dt.day == 31)
    assert(dt.hour == 15)
    assert(dt.minute == 30)
    assert(dt.second == 45)
    assert(not dt.is_leap_year())

    // 2. Standard 1: C / POSIX / strftime format (% specifiers)
    let c_fmt1 = dt.format("%Y-%m-%d %H:%M:%S")
    assert(c_fmt1 == "2026-10-31 15:30:45")

    let c_fmt2 = dt.format("%A, %B %d, %Y")
    assert(c_fmt2 == "Saturday, October 31, 2026")

    let c_fmt3 = dt.format("%F %T")
    assert(c_fmt3 == "2026-10-31 15:30:45")

    let c_fmt4 = dt.format("%I:%M %p")
    assert(c_fmt4 == "03:30 PM")

    // 3. Standard 2: Go reference date format ("2006-01-02 15:04:05")
    let go_fmt1 = dt.format_go("2006-01-02 15:04:05")
    assert(go_fmt1 == "2026-10-31 15:30:45")

    let go_fmt2 = dt.format_go("Monday, January 02, 2006")
    assert(go_fmt2 == "Saturday, October 31, 2026")

    let go_fmt3 = dt.format_go("03:04 PM")
    assert(go_fmt3 == "03:30 PM")

    let go_fmt4 = dt.format_go("Jan 2, 2006 at 3:04pm")
    assert(go_fmt4 == "Oct 31, 2026 at 3:30pm")

    // 4. ISO & RFC standards & Companion Methods
    let iso = dt.to_iso()
    assert(iso == "2026-10-31T15:30:45Z")

    let rfc2822 = dt.to_rfc2822()
    assert(rfc2822 == "Sat, 31 Oct 2026 15:30:45 +0000")

    // 5. All Go-mirrored format constants and pure Lowlight companion methods
    assert(dt.to_kitchen() == "3:30PM")
    assert(dt.to_date_only() == "2026-10-31")
    assert(dt.to_time_only() == "15:30:45")
    assert(dt.to_datetime_string() == "2026-10-31 15:30:45")
    assert(dt.to_ansic() == "Sat Oct 31 15:30:45 2026")
    assert(dt.to_unix_date() == "Sat Oct 31 15:30:45 UTC 2026")
    assert(dt.to_ruby_date() == "Sat Oct 31 15:30:45 +0000 2026")
    assert(dt.to_rfc822() == "31 Oct 26 15:30 UTC")
    assert(dt.to_rfc822z() == "31 Oct 26 15:30 +0000")
    assert(dt.to_rfc850() == "Saturday, 31-Oct-26 15:30:45 UTC")
    assert(dt.to_rfc1123() == "Sat, 31 Oct 2026 15:30:45 UTC")
    assert(dt.to_rfc1123z() == "Sat, 31 Oct 2026 15:30:45 +0000")
    assert(dt.to_rfc3339() == "2026-10-31T15:30:45Z")
    assert(dt.to_rfc3339_nano() == "2026-10-31T15:30:45.000000000Z")
    assert(dt.to_stamp() == "Oct 31 15:30:45")
    assert(dt.to_stamp_milli() == "Oct 31 15:30:45.000")
    assert(dt.to_stamp_micro() == "Oct 31 15:30:45.000000")
    assert(dt.to_stamp_nano() == "Oct 31 15:30:45.000000000")
    assert(dt.to_layout() == "10/31 03:30:45PM '26 +0000")

    // Verify format constants work interchangeably with format_go
    assert(dt.format_go(time.Kitchen) == dt.to_kitchen())
    assert(dt.format_go(time.DateOnly) == dt.to_date_only())
    assert(dt.format_go(time.TimeOnly) == dt.to_time_only())
    assert(dt.format_go(time.DateTimeFormat) == dt.to_datetime_string())
    assert(dt.format_go(time.ANSIC) == dt.to_ansic())
    assert(dt.format_go(time.UnixDate) == dt.to_unix_date())
    assert(dt.format_go(time.RubyDate) == dt.to_ruby_date())
    assert(dt.format_go(time.RFC822) == dt.to_rfc822())
    assert(dt.format_go(time.RFC822Z) == dt.to_rfc822z())
    assert(dt.format_go(time.RFC850) == dt.to_rfc850())
    assert(dt.format_go(time.RFC1123) == dt.to_rfc1123())
    assert(dt.format_go(time.RFC1123Z) == dt.to_rfc1123z())
    assert(dt.format_go(time.RFC3339) == dt.to_rfc3339())
    assert(dt.format_go(time.RFC3339Nano) == dt.to_rfc3339_nano())
    assert(dt.format_go(time.Stamp) == dt.to_stamp())
    assert(dt.format_go(time.StampMilli) == dt.to_stamp_milli())
    assert(dt.format_go(time.StampMicro) == dt.to_stamp_micro())
    assert(dt.format_go(time.StampNano) == dt.to_stamp_nano())
    assert(dt.format_go(time.Layout) == dt.to_layout())

    // Verify POSIX format constants work interchangeably with format
    assert(dt.format(time.C_FMT_DATETIME) == "2026-10-31 15:30:45")
    assert(dt.format(time.C_FMT_DATE_ONLY) == "2026-10-31")
    assert(dt.format(time.C_FMT_TIME_ONLY) == "15:30:45")
    assert(dt.format(time.C_FMT_KITCHEN) == "03:30 PM")

    // 6. Durations & Arithmetic
    let d = time.Duration.from_secs(3600.0) // +1 hour
    let dt_later = dt.add_duration(d)
    assert(dt_later.hour == 16)
    assert(dt_later.minute == 30)

    let diff = dt_later.diff(dt)
    assert(diff.to_secs() == 3600.0)

    // 6. Parsing ISO strings using string.to_datetime()
    let parsed? = "2026-10-31 15:30:45".to_datetime()
    if (parsed? as p) {
        assert(p.year == 2026)
        assert(p.month == 10)
        assert(p.day == 31)
        assert(p.hour == 15)
        assert(p.minute == 30)
        assert(p.second == 45)
    } else {
        assert(false)
    }

    // 7. Methods on int/float in time library
    let d_from_int = 3600.secs()
    assert(d_from_int.to_secs() == 3600.0)

    let d_from_float = (1.5).secs()
    assert(d_from_float.to_secs() == 1.5)

    let d_millis = 250.millis()
    assert(d_millis.to_nanos() == 250000000)

    assert((7).pad_zeros(3) == "007")
    assert((7).pad_spaces(3) == "  7")
    assert((10).to_month_abbr() == "Oct")
    assert((10).to_month_full() == "October")
    assert((6).to_weekday_abbr() == "Sat")
    assert((6).to_weekday_full() == "Saturday")

    println("All time formatting tests (C-standard & Go-standard) passed successfully!")
}
