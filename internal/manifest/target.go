package manifest

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// TargetInfo identifies the normalized OS, architecture, endianness, pointer width, and triple for a compilation target.
type TargetInfo struct {
	OS      string // "linux", "darwin", "windows", "freebsd", "openbsd", "netbsd", "wasm", "unknown"
	Arch    string // "amd64", "arm64", "riscv64", "x86", "wasm32", "wasm64", "unknown"
	Endian  string // "little" or "big"
	Triple  string // Target triple (explicit or canonical host default)
	IsUnix  bool
	PtrSize int // 4 or 8
	PtrBits int // 32 or 64
}

var knownOS = map[string]bool{
	"linux":   true,
	"darwin":  true,
	"windows": true,
	"freebsd": true,
	"openbsd": true,
	"netbsd":  true,
	"wasm":    true,
}

var knownOSGroups = map[string]bool{
	"unix": true,
}

var knownArch = map[string]bool{
	"amd64":   true,
	"arm64":   true,
	"riscv64": true,
	"x86":     true,
	"386":     true,
	"wasm32":  true,
	"wasm64":  true,
}

// ParseTarget converts an optional target triple (or empty for host defaults) into canonical TargetInfo.
func ParseTarget(triple string) TargetInfo {
	t := strings.TrimSpace(triple)
	osName := normalizeOS(runtime.GOOS)
	if osName == "" {
		osName = "unknown"
	}
	archName := normalizeArch(runtime.GOARCH)
	if archName == "" {
		archName = "unknown"
	}
	endian := "little"
	if t != "" {
		lower := strings.ToLower(t)
		parts := strings.Split(lower, "-")
		foundArch := false
		foundOS := false
		for _, part := range parts {
			if !foundArch {
				if a := normalizeArch(part); a != "" {
					archName = a
					foundArch = true
				}
			}
			if !foundOS {
				if o := normalizeOS(part); o != "" {
					osName = o
					foundOS = true
				}
			}
		}
		if !foundArch {
			// Single-token OS specifiers like ParseTarget("linux") preserve host arch; full triples mark unknown.
			if len(parts) > 1 || !foundOS {
				archName = "unknown"
			}
		}
		if !foundOS {
			if archName == "wasm32" || archName == "wasm64" {
				osName = "wasm"
			} else if len(parts) > 1 || !foundArch {
				osName = "unknown"
			}
		}
		if isBigEndianTriple(parts) {
			endian = "big"
		}
	}
	if t == "" {
		t = canonicalTriple(osName, archName)
	}
	ptrSize := 8
	if archName == "x86" || archName == "wasm32" {
		ptrSize = 4
	}
	return TargetInfo{
		OS:      osName,
		Arch:    archName,
		Endian:  endian,
		Triple:  t,
		IsUnix:  isUnixOS(osName),
		PtrSize: ptrSize,
		PtrBits: ptrSize * 8,
	}
}

func canonicalTriple(osName, archName string) string {
	arch := archName
	switch archName {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "x86":
		arch = "i686"
	}
	switch osName {
	case "darwin":
		return arch + "-apple-darwin"
	case "linux":
		return arch + "-unknown-linux-gnu"
	case "windows":
		return arch + "-pc-windows-msvc"
	case "freebsd":
		return arch + "-unknown-freebsd"
	case "openbsd":
		return arch + "-unknown-openbsd"
	case "netbsd":
		return arch + "-unknown-netbsd"
	case "wasm":
		return arch + "-unknown-wasi"
	default:
		return arch + "-unknown-" + osName
	}
}

func isBigEndianTriple(parts []string) bool {
	for _, p := range parts {
		if strings.HasSuffix(p, "le") || strings.HasSuffix(p, "el") {
			continue
		}
		switch p {
		case "s390x", "mips", "mips64", "powerpc", "ppc", "ppc64", "powerpc64", "sparc", "sparc64":
			return true
		}
		if strings.HasSuffix(p, "_be") || strings.HasSuffix(p, "eb") {
			return true
		}
	}
	return false
}

func normalizeOS(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(lower, "darwin"), strings.HasPrefix(lower, "macos"), strings.HasPrefix(lower, "macosx"):
		return "darwin"
	case strings.HasPrefix(lower, "linux"):
		return "linux"
	case strings.HasPrefix(lower, "win"), strings.HasPrefix(lower, "mingw"), strings.HasPrefix(lower, "msvc"):
		return "windows"
	case strings.HasPrefix(lower, "freebsd"):
		return "freebsd"
	case strings.HasPrefix(lower, "openbsd"):
		return "openbsd"
	case strings.HasPrefix(lower, "netbsd"):
		return "netbsd"
	case strings.HasPrefix(lower, "wasi"), lower == "wasm":
		return "wasm"
	default:
		return ""
	}
}

func normalizeArch(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	switch lower {
	case "amd64", "x86_64", "x64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "riscv64":
		return "riscv64"
	case "x86", "386", "i386", "i686":
		return "x86"
	case "wasm32":
		return "wasm32"
	case "wasm64":
		return "wasm64"
	default:
		return ""
	}
}

func isUnixOS(osName string) bool {
	switch osName {
	case "linux", "darwin", "freebsd", "openbsd", "netbsd":
		return true
	default:
		return false
	}
}

// TargetSelectorSpecificity ranks target selectors from broadest (0/1) to most
// specific (5) so more specific `[target.<selector>]` entries override broader ones.
func TargetSelectorSpecificity(selector string) int {
	sel := strings.ToLower(strings.TrimSpace(selector))
	if sel == "" {
		return 0
	}
	if sel == "unix" {
		return 1
	}
	if !strings.Contains(sel, "-") {
		if normalizeArch(sel) != "" {
			return 2
		}
		if normalizeOS(sel) != "" {
			return 3
		}
		return 0
	}
	parts := strings.Split(sel, "-")
	if len(parts) >= 3 {
		return 5
	}
	return 4
}

// SortTargetSelectors orders selectors from least specific to most specific
// (breaking ties lexicographically) so later entries override earlier ones cleanly.
func SortTargetSelectors(selectors []string) {
	sort.Slice(selectors, func(i, j int) bool {
		si := TargetSelectorSpecificity(selectors[i])
		sj := TargetSelectorSpecificity(selectors[j])
		if si != sj {
			return si < sj
		}
		return selectors[i] < selectors[j]
	})
}

// MatchesSelector checks whether a `[target.<selector>]` selector matches target.
func (ti TargetInfo) MatchesSelector(selector string) bool {
	sel := strings.ToLower(strings.TrimSpace(selector))
	if sel == "" {
		return false
	}
	if sel == "unix" {
		return ti.IsUnix
	}
	if sel == ti.OS || sel == ti.Arch {
		return true
	}
	if ti.Triple != "" && strings.EqualFold(sel, ti.Triple) {
		return true
	}
	// Support `os_arch` or triple-like selectors (`linux-amd64`, `aarch64-unknown-linux-gnu`).
	if strings.Contains(sel, "-") {
		parsed := ParseTarget(sel)
		if parsed.OS == ti.OS && parsed.Arch == ti.Arch {
			return true
		}
	}
	return false
}

// IsTestFile reports whether filename is a package-scoped test file (`*_test.ll`) per SPECIFICATION.md §4.1.
func IsTestFile(filename string) bool {
	base := filepath.Base(filename)
	return strings.HasSuffix(base, "_test.ll")
}

// MatchesTargetSuffix checks Go-style OS/arch filename suffixes per SPECIFICATION.md §4.1:
//   - `<stem>_<os>.ll` (e.g. `fs_linux.ll`, `fs_darwin.ll`, `fs_windows.ll`)
//   - `<stem>_<osgroup>.ll` (e.g. `fs_unix.ll`)
//   - `<stem>_<arch>.ll` (e.g. `asm_arm64.ll`, `asm_amd64.ll`)
//   - `<stem>_<os>_<arch>.ll` (e.g. `sys_linux_amd64.ll`)
// Test suffix `_test.ll` is stripped before checking platform tags (`fs_unix_test.ll`).
// Files without recognized platform suffixes match all targets unconditionally.
func MatchesTargetSuffix(filename string, target TargetInfo) bool {
	base := filepath.Base(filename)
	if !strings.HasSuffix(base, ".ll") {
		return false
	}
	stem := strings.TrimSuffix(base, ".ll")
	stem = strings.TrimSuffix(stem, "_test")
	parts := strings.Split(stem, "_")
	if len(parts) < 2 {
		return true
	}

	last := strings.ToLower(parts[len(parts)-1])
	prev := ""
	if len(parts) >= 3 {
		prev = strings.ToLower(parts[len(parts)-2])
	}

	// Check `<stem>_<os>_<arch>` or `<stem>_<osgroup>_<arch>`.
	if knownArch[last] {
		if !archMatches(last, target.Arch) {
			return false
		}
		if prev != "" && (knownOS[prev] || knownOSGroups[prev]) {
			return osOrGroupMatches(prev, target)
		}
		return true
	}
	// Check `<stem>_<os>` or `<stem>_<osgroup>`.
	if knownOS[last] || knownOSGroups[last] {
		return osOrGroupMatches(last, target)
	}
	return true
}

func archMatches(tag, targetArch string) bool {
	if tag == "386" {
		tag = "x86"
	}
	return tag == targetArch
}

func osOrGroupMatches(tag string, target TargetInfo) bool {
	if tag == "unix" {
		return target.IsUnix
	}
	return tag == target.OS
}
