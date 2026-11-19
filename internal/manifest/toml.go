// Package manifest implements Lowlight's zero-dependency TOML parser/serializer,
// `lowlight.toml` manifest loader, `lowlight.lock` lockfile, decentralized static
// package index protocol, SemVer/MVS dependency resolution, caching, and vendoring
// as specified in SPECIFICATION.md §12.
package manifest

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// TOMLTable represents a decoded TOML table mapping key names to values.
// Value types are: string, int64, bool, []any, TOMLTable, or []TOMLTable.
type TOMLTable map[string]any

// ParseTOML parses a UTF-8 TOML document supporting comments (`#`), `[tables]`,
// `[[array_of_tables]]`, quoted/bare dotted keys, inline tables `{ ... }`,
// single/multi-line arrays `[...]`, basic/literal/multi-line strings, ints, and bools.
func ParseTOML(src string) (TOMLTable, error) {
	p := &tomlParser{
		src:  strings.ReplaceAll(src, "\r\n", "\n"),
		line: 1,
	}
	root := make(TOMLTable)
	cur := root
	for {
		p.skipWSAndComments(true)
		if p.eof() {
			break
		}
		if p.peek() == '[' {
			isArray := p.peekAt(1) == '['
			p.pos++
			if isArray {
				p.pos++
			}
			p.skipInlineWS()
			path, err := p.parseKeyPath()
			if err != nil {
				return nil, err
			}
			p.skipInlineWS()
			if isArray {
				if p.peek() != ']' || p.peekAt(1) != ']' {
					return nil, p.errorf("expected `]]` closing array-of-tables header")
				}
				p.pos += 2
			} else {
				if p.peek() != ']' {
					return nil, p.errorf("expected `]` closing table header")
				}
				p.pos++
			}
			if err := p.expectLineEnd(); err != nil {
				return nil, err
			}
			if isArray {
				tbl, err := ensureArrayTable(root, path)
				if err != nil {
					return nil, p.errorf("%v", err)
				}
				cur = tbl
			} else {
				tbl, err := ensureTablePath(root, path)
				if err != nil {
					return nil, p.errorf("%v", err)
				}
				cur = tbl
			}
			continue
		}

		path, err := p.parseKeyPath()
		if err != nil {
			return nil, err
		}
		p.skipInlineWS()
		if p.peek() != '=' {
			return nil, p.errorf("expected `=` after key %q", strings.Join(path, "."))
		}
		p.pos++
		p.skipInlineWS()
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if err := p.expectLineEnd(); err != nil {
			return nil, err
		}
		if err := setValueAtPath(cur, path, val); err != nil {
			return nil, p.errorf("%v", err)
		}
	}
	return root, nil
}

type tomlParser struct {
	src  string
	pos  int
	line int
}

func (p *tomlParser) eof() bool { return p.pos >= len(p.src) }

func (p *tomlParser) peek() byte {
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *tomlParser) peekAt(delta int) byte {
	idx := p.pos + delta
	if idx < 0 || idx >= len(p.src) {
		return 0
	}
	return p.src[idx]
}

func (p *tomlParser) errorf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", p.line, fmt.Sprintf(format, args...))
}

func (p *tomlParser) skipInlineWS() {
	for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *tomlParser) skipWSAndComments(skipNewlines bool) {
	for !p.eof() {
		ch := p.src[p.pos]
		if ch == ' ' || ch == '\t' {
			p.pos++
			continue
		}
		if ch == '\n' {
			if !skipNewlines {
				break
			}
			p.pos++
			p.line++
			continue
		}
		if ch == '#' {
			for !p.eof() && p.src[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		break
	}
}

func (p *tomlParser) expectLineEnd() error {
	p.skipInlineWS()
	if p.eof() {
		return nil
	}
	if p.peek() == '#' {
		for !p.eof() && p.src[p.pos] != '\n' {
			p.pos++
		}
	}
	if p.eof() {
		return nil
	}
	if p.peek() == '\n' {
		p.pos++
		p.line++
		return nil
	}
	return p.errorf("unexpected trailing character %q", p.peek())
}

func (p *tomlParser) parseKeyPath() ([]string, error) {
	var parts []string
	for {
		p.skipInlineWS()
		k, err := p.parseKeyPart()
		if err != nil {
			return nil, err
		}
		parts = append(parts, k)
		p.skipInlineWS()
		if p.peek() == '.' {
			p.pos++
			continue
		}
		break
	}
	return parts, nil
}

func (p *tomlParser) parseKeyPart() (string, error) {
	if p.eof() {
		return "", p.errorf("unexpected EOF expecting key")
	}
	switch p.peek() {
	case '"':
		return p.parseBasicString(false)
	case '\'':
		return p.parseLiteralString(false)
	default:
		start := p.pos
		for !p.eof() && isBareTOMLKeyChar(p.src[p.pos]) {
			p.pos++
		}
		if p.pos == start {
			return "", p.errorf("invalid key starting with %q", p.peek())
		}
		return p.src[start:p.pos], nil
	}
}

func isBareTOMLKeyChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') || ch == '_' || ch == '-'
}

func (p *tomlParser) parseValue() (any, error) {
	if p.eof() {
		return nil, p.errorf("unexpected EOF expecting value")
	}
	switch p.peek() {
	case '"':
		if p.peekAt(1) == '"' && p.peekAt(2) == '"' {
			return p.parseBasicString(true)
		}
		return p.parseBasicString(false)
	case '\'':
		if p.peekAt(1) == '\'' && p.peekAt(2) == '\'' {
			return p.parseLiteralString(true)
		}
		return p.parseLiteralString(false)
	case '[':
		return p.parseArray()
	case '{':
		return p.parseInlineTable()
	default:
		start := p.pos
		for !p.eof() {
			ch := p.src[p.pos]
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '#' || ch == ',' || ch == ']' || ch == '}' {
				break
			}
			p.pos++
		}
		tok := p.src[start:p.pos]
		if tok == "" {
			return nil, p.errorf("unexpected token %q in value", p.peek())
		}
		if tok == "true" {
			return true, nil
		}
		if tok == "false" {
			return false, nil
		}
		clean := strings.ReplaceAll(tok, "_", "")
		if iv, err := strconv.ParseInt(clean, 0, 64); err == nil {
			return iv, nil
		}
		return nil, p.errorf("unsupported or invalid TOML value %q", tok)
	}
}

func (p *tomlParser) parseBasicString(multiline bool) (string, error) {
	if multiline {
		p.pos += 3
		if p.peek() == '\n' {
			p.pos++
			p.line++
		}
	} else {
		p.pos++
	}
	var sb strings.Builder
	for !p.eof() {
		ch := p.src[p.pos]
		if multiline && ch == '"' && p.peekAt(1) == '"' && p.peekAt(2) == '"' {
			p.pos += 3
			return sb.String(), nil
		}
		if !multiline && ch == '"' {
			p.pos++
			return sb.String(), nil
		}
		if !multiline && ch == '\n' {
			return "", p.errorf("unterminated string literal")
		}
		if ch == '\\' {
			p.pos++
			if p.eof() {
				return "", p.errorf("unterminated string escape")
			}
			esc := p.src[p.pos]
			p.pos++
			switch esc {
			case 'b':
				sb.WriteByte('\b')
			case 't':
				sb.WriteByte('\t')
			case 'n':
				sb.WriteByte('\n')
			case 'f':
				sb.WriteByte('\f')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			case '\n':
				if !multiline {
					return "", p.errorf("invalid newline escape in single-line string")
				}
				p.line++
				for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\n') {
					if p.src[p.pos] == '\n' {
						p.line++
					}
					p.pos++
				}
			case 'u', 'U':
				n := 4
				if esc == 'U' {
					n = 8
				}
				if p.pos+n > len(p.src) {
					return "", p.errorf("short unicode escape")
				}
				code, err := strconv.ParseUint(p.src[p.pos:p.pos+n], 16, 32)
				if err != nil {
					return "", p.errorf("invalid unicode escape %q", p.src[p.pos:p.pos+n])
				}
				p.pos += n
				sb.WriteRune(rune(code))
			default:
				return "", p.errorf("unknown string escape \\%c", esc)
			}
			continue
		}
		if ch == '\n' {
			p.line++
		}
		sb.WriteByte(ch)
		p.pos++
	}
	return "", p.errorf("unterminated string literal")
}

func (p *tomlParser) parseLiteralString(multiline bool) (string, error) {
	if multiline {
		p.pos += 3
		if p.peek() == '\n' {
			p.pos++
			p.line++
		}
	} else {
		p.pos++
	}
	start := p.pos
	for !p.eof() {
		ch := p.src[p.pos]
		if multiline && ch == '\'' && p.peekAt(1) == '\'' && p.peekAt(2) == '\'' {
			res := p.src[start:p.pos]
			p.pos += 3
			return res, nil
		}
		if !multiline && ch == '\'' {
			res := p.src[start:p.pos]
			p.pos++
			return res, nil
		}
		if !multiline && ch == '\n' {
			return "", p.errorf("unterminated literal string")
		}
		if ch == '\n' {
			p.line++
		}
		p.pos++
	}
	return "", p.errorf("unterminated literal string")
}

func (p *tomlParser) parseArray() ([]any, error) {
	p.pos++ // consume '['
	var out []any
	for {
		p.skipWSAndComments(true)
		if p.eof() {
			return nil, p.errorf("unterminated array `[`")
		}
		if p.peek() == ']' {
			p.pos++
			return out, nil
		}
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.skipWSAndComments(true)
		if p.peek() == ',' {
			p.pos++
			continue
		}
		if p.peek() == ']' {
			p.pos++
			return out, nil
		}
		return nil, p.errorf("expected `,` or `]` in array, got %q", p.peek())
	}
}

func (p *tomlParser) parseInlineTable() (TOMLTable, error) {
	p.pos++ // consume '{'
	tbl := make(TOMLTable)
	for {
		p.skipInlineWS()
		if p.eof() {
			return nil, p.errorf("unterminated inline table `{`")
		}
		if p.peek() == '}' {
			p.pos++
			return tbl, nil
		}
		path, err := p.parseKeyPath()
		if err != nil {
			return nil, err
		}
		p.skipInlineWS()
		if p.peek() != '=' {
			return nil, p.errorf("expected `=` in inline table after %q", strings.Join(path, "."))
		}
		p.pos++
		p.skipInlineWS()
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if err := setValueAtPath(tbl, path, val); err != nil {
			return nil, p.errorf("%v", err)
		}
		p.skipInlineWS()
		if p.peek() == ',' {
			p.pos++
			continue
		}
		if p.peek() == '}' {
			p.pos++
			return tbl, nil
		}
		return nil, p.errorf("expected `,` or `}` in inline table, got %q", p.peek())
	}
}

func ensureTablePath(root TOMLTable, path []string) (TOMLTable, error) {
	cur := root
	for i, seg := range path {
		existing, ok := cur[seg]
		if !ok {
			next := make(TOMLTable)
			cur[seg] = next
			cur = next
			continue
		}
		switch v := existing.(type) {
		case TOMLTable:
			cur = v
		case []TOMLTable:
			if len(v) == 0 {
				return nil, fmt.Errorf("empty array-of-tables %q", strings.Join(path[:i+1], "."))
			}
			cur = v[len(v)-1]
		default:
			return nil, fmt.Errorf("key %q already defined as non-table", strings.Join(path[:i+1], "."))
		}
	}
	return cur, nil
}

func ensureArrayTable(root TOMLTable, path []string) (TOMLTable, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("empty array-of-tables path")
	}
	parent := root
	if len(path) > 1 {
		var err error
		parent, err = ensureTablePath(root, path[:len(path)-1])
		if err != nil {
			return nil, err
		}
	}
	last := path[len(path)-1]
	elem := make(TOMLTable)
	existing, ok := parent[last]
	if !ok {
		parent[last] = []TOMLTable{elem}
		return elem, nil
	}
	arr, ok := existing.([]TOMLTable)
	if !ok {
		return nil, fmt.Errorf("key %q already defined as non-array-of-tables", strings.Join(path, "."))
	}
	parent[last] = append(arr, elem)
	return elem, nil
}

func setValueAtPath(tbl TOMLTable, path []string, val any) error {
	if len(path) == 0 {
		return fmt.Errorf("empty key path")
	}
	cur := tbl
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		existing, ok := cur[seg]
		if !ok {
			next := make(TOMLTable)
			cur[seg] = next
			cur = next
			continue
		}
		sub, ok := existing.(TOMLTable)
		if !ok {
			return fmt.Errorf("cannot assign dotted key under non-table %q", strings.Join(path[:i+1], "."))
		}
		cur = sub
	}
	last := path[len(path)-1]
	if _, dup := cur[last]; dup {
		return fmt.Errorf("duplicate key %q", strings.Join(path, "."))
	}
	cur[last] = val
	return nil
}

// FormatTOMLKey formats a single TOML key segment, quoting it only when needed.
func FormatTOMLKey(k string) string {
	if k == "" {
		return `""`
	}
	for i := 0; i < len(k); i++ {
		if !isBareTOMLKeyChar(k[i]) {
			return strconv.Quote(k)
		}
	}
	return k
}

// FormatTOMLString formats a basic TOML quoted string.
func FormatTOMLString(s string) string {
	return strconv.Quote(s)
}

// FormatTOMLStringArray formats a string slice as a compact TOML array.
func FormatTOMLStringArray(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(FormatTOMLString(item))
	}
	sb.WriteByte(']')
	return sb.String()
}

// UpsertTableEntry performs a comment-preserving surgical update of `key = valueExpr`
// inside `[section]` of `raw` TOML text (SPECIFICATION.md §12.6).
// If `[section]` does not exist, it is appended cleanly at the end of the file.
func UpsertTableEntry(raw, section, key, valueExpr string) string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	sectionHeader := "[" + section + "]"
	secStart := -1
	secEnd := len(lines)

	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && !strings.HasPrefix(trim, "[[") {
			hdr := stripTOMLComment(trim)
			if hdr == sectionHeader {
				secStart = i
				continue
			}
			if secStart >= 0 {
				secEnd = i
				break
			}
		} else if strings.HasPrefix(trim, "[[") && secStart >= 0 {
			secEnd = i
			break
		}
	}

	newLine := FormatTOMLKey(key) + " = " + valueExpr

	if secStart < 0 {
		// Append new section cleanly.
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, sectionHeader, newLine)
		return strings.Join(lines, "\n") + "\n"
	}

	// Look for existing `key = ...` line inside [secStart+1, secEnd).
	for i := secStart + 1; i < secEnd; i++ {
		if k, ok := parseSimpleAssignmentKey(lines[i]); ok && k == key {
			comment := trailingComment(lines[i])
			if comment != "" {
				lines[i] = newLine + " " + comment
			} else {
				lines[i] = newLine
			}
			return strings.Join(lines, "\n") + "\n"
		}
	}

	// Insert right after the last non-blank entry in the section.
	insertAt := secStart + 1
	for i := secStart + 1; i < secEnd; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			insertAt = i + 1
		}
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, newLine)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n") + "\n"
}

// RemoveTableEntry removes `key` from `[section]` (or a `[section.<key>]` sub-table)
// while preserving surrounding comments and unrelated lines.
func RemoveTableEntry(raw, section, key string) (string, bool) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	sectionHeader := "[" + section + "]"
	subHeader := "[" + section + "." + FormatTOMLKey(key) + "]"
	inSection := false
	inSub := false
	removed := false
	var out []string

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			hdr := stripTOMLComment(trim)
			inSection = hdr == sectionHeader
			if hdr == subHeader {
				inSub = true
				removed = true
				continue
			}
			inSub = false
			out = append(out, line)
			continue
		}
		if inSub {
			continue
		}
		if inSection {
			if k, ok := parseSimpleAssignmentKey(line); ok && k == key {
				removed = true
				continue
			}
		}
		out = append(out, line)
	}
	if !removed {
		return raw, false
	}
	return strings.Join(out, "\n") + "\n", true
}

// FormatManifestSource normalizes spacing and sorts dependency keys inside
// `[dependencies]`, `[dev-dependencies]`, and `[overrides]` while preserving comments.
func FormatManifestSource(raw string) (string, error) {
	if _, err := ParseTOML(raw); err != nil {
		return "", err
	}
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	type entry struct {
		key   string
		lines []string
	}

	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		trim := strings.TrimSpace(line)
		hdr := stripTOMLComment(trim)
		out = append(out, line)
		i++
		if hdr != "[dependencies]" && hdr != "[dev-dependencies]" && hdr != "[overrides]" {
			continue
		}
		// Collect entries until next table header.
		var entries []entry
		var pendingComments []string
		var trailing []string
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if strings.HasPrefix(t, "[") {
				break
			}
			if t == "" {
				if len(pendingComments) > 0 {
					trailing = append(trailing, pendingComments...)
					pendingComments = nil
				}
				trailing = append(trailing, lines[i])
				i++
				continue
			}
			if strings.HasPrefix(t, "#") {
				pendingComments = append(pendingComments, lines[i])
				i++
				continue
			}
			if k, ok := parseSimpleAssignmentKey(lines[i]); ok {
				var blk []string
				blk = append(blk, pendingComments...)
				pendingComments = nil
				blk = append(blk, lines[i])
				entries = append(entries, entry{key: k, lines: blk})
			} else {
				if len(pendingComments) > 0 {
					trailing = append(trailing, pendingComments...)
					pendingComments = nil
				}
				trailing = append(trailing, lines[i])
			}
			i++
		}
		if len(pendingComments) > 0 {
			trailing = append(trailing, pendingComments...)
		}
		sort.SliceStable(entries, func(a, b int) bool {
			return entries[a].key < entries[b].key
		})
		for _, e := range entries {
			out = append(out, e.lines...)
		}
		out = append(out, trailing...)
	}
	return strings.Join(out, "\n") + "\n", nil
}

func stripTOMLComment(s string) string {
	inBasic := false
	inLit := false
	esc := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inBasic {
			if esc {
				esc = false
				continue
			}
			if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inBasic = false
			}
			continue
		}
		if inLit {
			if ch == '\'' {
				inLit = false
			}
			continue
		}
		if ch == '"' {
			inBasic = true
		} else if ch == '\'' {
			inLit = true
		} else if ch == '#' {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

func trailingComment(s string) string {
	inBasic := false
	inLit := false
	esc := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inBasic {
			if esc {
				esc = false
				continue
			}
			if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inBasic = false
			}
			continue
		}
		if inLit {
			if ch == '\'' {
				inLit = false
			}
			continue
		}
		if ch == '"' {
			inBasic = true
		} else if ch == '\'' {
			inLit = true
		} else if ch == '#' {
			return strings.TrimSpace(s[i:])
		}
	}
	return ""
}

func parseSimpleAssignmentKey(line string) (string, bool) {
	trim := strings.TrimSpace(line)
	if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "[") {
		return "", false
	}
	p := &tomlParser{src: trim, line: 1}
	k, err := p.parseKeyPart()
	if err != nil {
		return "", false
	}
	p.skipInlineWS()
	if p.peek() != '=' {
		return "", false
	}
	return k, true
}
