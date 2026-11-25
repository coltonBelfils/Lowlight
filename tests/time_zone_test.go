package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

// Local DateTimes must report the host zone abbreviation (via `std/time` localtime_r) instead of a
// `"LOCAL"` placeholder, including DST transitions, on both backends.
func TestLocalTimeZoneNamesFollowTZ(t *testing.T) {
	t.Parallel()
	src := `
import "std/time"

bind main = fn(): {
	let winter = time.DateTime.from_unix_secs(1700000000, true)
	let summer = time.DateTime.from_unix_secs(1690000000, true)
	println("${winter.zone_name} ${winter.offset_secs}")
	println("${summer.zone_name} ${summer.offset_secs}")
	println(winter.format_go("2006-01-02 15:04 MST"))
	println(summer.format("%H:%M %Z %z"))
}
`
	root := projectRoot(t)
	want := "PST -28800\nPDT -25200\n2023-11-14 14:13 PST\n21:26 PDT -0700"
	for _, backend := range []driver.BackendKind{driver.BackendLLVM, driver.BackendC11} {
		entry := filepath.Join(t.TempDir(), "tz_names.ll")
		var stdout, stderr bytes.Buffer
		rc, err := driver.Run(driver.Options{
			EntryPath:   entry,
			SrcOverride: src,
			ProjectRoot: root,
			Backend:     backend,
			ExtraCFlags: strictCFlags,
			Env: []string{
				"TZ=America/Los_Angeles",
				"LOWLIGHT_CHECK_LEAKS=1",
				"ASAN_OPTIONS=detect_leaks=0",
			},
		}, nil, &stdout, &stderr)
		if err != nil || rc != 0 {
			t.Fatalf("[%s] rc=%d err=%v\nstdout:\n%s\nstderr:\n%s", backend, rc, err, stdout.String(), stderr.String())
		}
		if got := strings.TrimSpace(stdout.String()); got != want {
			t.Fatalf("[%s] unexpected output:\n%s\nwant:\n%s", backend, got, want)
		}
	}
}
