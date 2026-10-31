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

    // 4. ISO & RFC standards
    let iso = dt.to_iso()
    assert(iso == "2026-10-31T15:30:45Z")

    let rfc2822 = dt.to_rfc2822()
    assert(rfc2822 == "Sat, 31 Oct 2026 15:30:45 +0000")

    // 5. Durations & Arithmetic
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

    println("All time formatting tests (C-standard & Go-standard) passed successfully!")
}
