package manifest

import (
	"fmt"
	"strconv"
	"strings"
)

// SemVer represents a parsed Semantic Version `MAJOR.MINOR.PATCH[-prerelease][+build]`.
type SemVer struct {
	Major int
	Minor int
	Patch int
	Pre   string
	Build string
	Raw   string
}

// ParseSemVer parses a SemVer string (optionally stripping a leading `v`).
func ParseSemVer(raw string) (SemVer, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return SemVer{}, fmt.Errorf("empty semantic version")
	}
	orig := s
	s = strings.TrimPrefix(s, "v")
	var build string
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		build = s[idx+1:]
		s = s[:idx]
	}
	var pre string
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return SemVer{}, fmt.Errorf("invalid semantic version %q", orig)
	}
	parsePart := func(part string) (int, error) {
		if part == "" {
			return 0, fmt.Errorf("invalid semantic version %q", orig)
		}
		if len(part) > 1 && part[0] == '0' {
			return 0, fmt.Errorf("leading zero in semantic version %q", orig)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid numeric component in semantic version %q", orig)
		}
		return n, nil
	}
	maj, err := parsePart(parts[0])
	if err != nil {
		return SemVer{}, err
	}
	var min, pat int
	if len(parts) >= 2 {
		min, err = parsePart(parts[1])
		if err != nil {
			return SemVer{}, err
		}
	}
	if len(parts) == 3 {
		pat, err = parsePart(parts[2])
		if err != nil {
			return SemVer{}, err
		}
	}
	return SemVer{Major: maj, Minor: min, Patch: pat, Pre: pre, Build: build, Raw: orig}, nil
}

// String formats canonical SemVer without build metadata unless set.
func (v SemVer) String() string {
	base := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		base += "-" + v.Pre
	}
	if v.Build != "" {
		base += "+" + v.Build
	}
	return base
}

// Compare returns -1 if v < other, 0 if equal (ignoring build metadata), +1 if v > other.
func (v SemVer) Compare(other SemVer) int {
	if v.Major != other.Major {
		return cmpInt(v.Major, other.Major)
	}
	if v.Minor != other.Minor {
		return cmpInt(v.Minor, other.Minor)
	}
	if v.Patch != other.Patch {
		return cmpInt(v.Patch, other.Patch)
	}
	if v.Pre == other.Pre {
		return 0
	}
	// Release > prerelease.
	if v.Pre == "" {
		return 1
	}
	if other.Pre == "" {
		return -1
	}
	aParts := strings.Split(v.Pre, ".")
	bParts := strings.Split(other.Pre, ".")
	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		if aParts[i] == bParts[i] {
			continue
		}
		ai, aErr := strconv.Atoi(aParts[i])
		bi, bErr := strconv.Atoi(bParts[i])
		switch {
		case aErr == nil && bErr == nil:
			return cmpInt(ai, bi)
		case aErr == nil:
			return -1 // numeric < alphanumeric per SemVer 2.0
		case bErr == nil:
			return 1
		default:
			if aParts[i] < bParts[i] {
				return -1
			}
			return 1
		}
	}
	return cmpInt(len(aParts), len(bParts))
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// VersionConstraint represents an intersection of SemVer comparators
// (e.g. `^1.2`, `~0.3.1`, `>=1.0.0, <2.0.0`, `=1.2.3`, or `*`).
type VersionConstraint struct {
	Raw   string
	Clauses []versionClause
}

type versionClause struct {
	op       string // "^", "~", "=", "<", "<=", ">", ">=", "*"
	ver      SemVer
	partsCnt int // 1, 2, or 3 specified parts
}

// ParseVersionConstraint parses a comma- or whitespace-separated SemVer constraint string.
func ParseVersionConstraint(raw string) (VersionConstraint, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return VersionConstraint{}, fmt.Errorf("empty version constraint")
	}
	if s == "*" {
		return VersionConstraint{Raw: s, Clauses: []versionClause{{op: "*"}}}, nil
	}
	// Split on commas first, then spaces for clauses like ">=1.0.0 <2.0.0".
	rawParts := strings.Split(s, ",")
	var tokens []string
	for _, rp := range rawParts {
		for _, tok := range strings.Fields(rp) {
			if tok != "" {
				tokens = append(tokens, tok)
			}
		}
	}
	if len(tokens) == 0 {
		return VersionConstraint{}, fmt.Errorf("invalid version constraint %q", raw)
	}

	var clauses []versionClause
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "*" {
			clauses = append(clauses, versionClause{op: "*"})
			continue
		}
		op := ""
		for _, prefix := range []string{">=", "<=", "^", "~", "=", ">", "<"} {
			if strings.HasPrefix(tok, prefix) {
				op = prefix
				tok = strings.TrimSpace(strings.TrimPrefix(tok, prefix))
				break
			}
		}
		if tok == "" && i+1 < len(tokens) {
			i++
			tok = tokens[i]
		}
		if op == "" {
			// Bare version e.g. "1.2.3" acts as exact match (or caret? Spec §12.2 lists `^`, `~`, `=`; bare exact/prefix matches).
			op = "="
		}
		core := strings.TrimPrefix(tok, "v")
		if idx := strings.IndexAny(core, "-+"); idx >= 0 {
			core = core[:idx]
		}
		partsCnt := len(strings.Split(core, "."))
		ver, err := ParseSemVer(tok)
		if err != nil {
			return VersionConstraint{}, fmt.Errorf("invalid version constraint %q: %w", raw, err)
		}
		clauses = append(clauses, versionClause{op: op, ver: ver, partsCnt: partsCnt})
	}
	return VersionConstraint{Raw: s, Clauses: clauses}, nil
}

// Matches reports whether candidate satisfies every comparator in c.
func (c VersionConstraint) Matches(candidate SemVer) bool {
	for _, cl := range c.Clauses {
		if !cl.matches(candidate) {
			return false
		}
	}
	return true
}

func (cl versionClause) matches(cand SemVer) bool {
	if cl.op == "*" {
		return cand.Pre == ""
	}
	// Do not match pre-release versions unless the comparator explicitly references the same major.minor.patch with a prerelease.
	if cand.Pre != "" && cl.ver.Pre == "" {
		return false
	}
	cmp := cand.Compare(cl.ver)
	switch cl.op {
	case "=":
		switch cl.partsCnt {
		case 1:
			return cand.Major == cl.ver.Major
		case 2:
			return cand.Major == cl.ver.Major && cand.Minor == cl.ver.Minor
		default:
			return cmp == 0
		}
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case "^":
		if cmp < 0 {
			return false
		}
		var upper SemVer
		switch {
		case cl.ver.Major > 0:
			upper = SemVer{Major: cl.ver.Major + 1}
		case cl.ver.Minor > 0 || cl.partsCnt == 2:
			upper = SemVer{Major: 0, Minor: cl.ver.Minor + 1}
		default:
			upper = SemVer{Major: 0, Minor: 0, Patch: cl.ver.Patch + 1}
		}
		return cand.Compare(upper) < 0
	case "~":
		if cmp < 0 {
			return false
		}
		var upper SemVer
		if cl.partsCnt == 1 {
			upper = SemVer{Major: cl.ver.Major + 1}
		} else {
			upper = SemVer{Major: cl.ver.Major, Minor: cl.ver.Minor + 1}
		}
		return cand.Compare(upper) < 0
	default:
		return false
	}
}
