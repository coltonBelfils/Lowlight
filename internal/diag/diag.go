// Package diag provides source positions, spans, and formatted compiler diagnostics.
package diag

import (
	"fmt"
	"strings"
)

// Pos represents a 1-based line and column location inside a source file.
type Pos struct {
	File   string
	Offset int // 0-based byte offset in source
	Line   int // 1-based line number
	Col    int // 1-based column number
}

func (p Pos) String() string {
	if p.File == "" {
		if p.Line <= 0 && p.Col <= 0 {
			return ""
		}
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Span marks a half-open range [Start, End) in a source file.
type Span struct {
	Start Pos
	End   Pos
}

func (s Span) Pos() Pos { return s.Start }
func (s Span) String() string { return s.Start.String() }

// Severity indicates whether a diagnostic is an error or a warning.
type Severity int

const (
	Error Severity = iota
	Warning
	Note
)

func (s Severity) String() string {
	switch s {
	case Warning:
		return "warning"
	case Note:
		return "note"
	default:
		return "error"
	}
}

// RelatedInfo attaches a secondary source location and note to a Diagnostic.
type RelatedInfo struct {
	Span    Span
	Message string
}

// Diagnostic represents a single compiler error or warning with source context.
type Diagnostic struct {
	Code     string // e.g. "E0001", "W0001"
	Severity Severity
	Span     Span // what is underlined
	Message  string
	Hint     string
	Related  []RelatedInfo
	// Full is the whole construct the diagnostic is about when Span underlines only part of it
	// (a mismatched call underlines its name; Full covers the call), for quick fixes.
	Full Span
	// Data carries machine-readable details for editor quick fixes (e.g. "fix": "to_string").
	Data map[string]string
}

// Reporter collects diagnostics and formats them with source snippets.
type Reporter struct {
	sources map[string][]string
	diags   []Diagnostic
}

// NewReporter creates an empty diagnostic reporter.
func NewReporter() *Reporter {
	return &Reporter{
		sources: make(map[string][]string),
	}
}

// AddSource registers the text of a file so snippets can be rendered on errors.
func (r *Reporter) AddSource(filename, content string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	r.sources[filename] = strings.Split(normalized, "\n")
}

// Errorf records an error diagnostic at the given span.
// Report appends a fully specified diagnostic.
func (r *Reporter) Report(d Diagnostic) {
	r.diags = append(r.diags, d)
}

// LineLen returns the byte length of 1-based line `line` in a registered source file.
func (r *Reporter) LineLen(file string, line int) (int, bool) {
	lines, ok := r.sources[file]
	if !ok || line < 1 || line > len(lines) {
		return 0, false
	}
	return len(lines[line-1]), true
}

func (r *Reporter) Errorf(span Span, format string, args ...any) {
	r.diags = append(r.diags, Diagnostic{
		Severity: Error,
		Span:     span,
		Message:  fmt.Sprintf(format, args...),
	})
}

// ErrorCode records an error diagnostic with an explicit error code.
func (r *Reporter) ErrorCode(span Span, code, format string, args ...any) {
	r.diags = append(r.diags, Diagnostic{
		Code:     code,
		Severity: Error,
		Span:     span,
		Message:  fmt.Sprintf(format, args...),
	})
}

// ErrorWithHint records an error diagnostic with an actionable hint.
func (r *Reporter) ErrorWithHint(span Span, msg, hint string) {
	r.diags = append(r.diags, Diagnostic{
		Severity: Error,
		Span:     span,
		Message:  msg,
		Hint:     hint,
	})
}

// ErrorCodeWithHint records an error diagnostic with an error code and an actionable hint.
func (r *Reporter) ErrorCodeWithHint(span Span, code, msg, hint string) {
	r.diags = append(r.diags, Diagnostic{
		Code:     code,
		Severity: Error,
		Span:     span,
		Message:  msg,
		Hint:     hint,
	})
}

// Warnf records a non-fatal warning diagnostic.
func (r *Reporter) Warnf(span Span, format string, args ...any) {
	r.diags = append(r.diags, Diagnostic{
		Severity: Warning,
		Span:     span,
		Message:  fmt.Sprintf(format, args...),
	})
}

// Notef records an informational compile-time note diagnostic (e.g. `comp.log`).
func (r *Reporter) Notef(span Span, format string, args ...any) {
	r.diags = append(r.diags, Diagnostic{
		Severity: Note,
		Span:     span,
		Message:  fmt.Sprintf(format, args...),
	})
}

// HasErrors returns true if at least one Error diagnostic has been recorded.
func (r *Reporter) HasErrors() bool {
	for _, d := range r.diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Diagnostics returns all collected diagnostics.
func (r *Reporter) Diagnostics() []Diagnostic {
	return r.diags
}

// Len returns the current number of recorded diagnostics (including warnings/notes).
func (r *Reporter) Len() int {
	return len(r.diags)
}

// HasErrorSince reports whether any Error diagnostic was recorded at index >= start.
func (r *Reporter) HasErrorSince(start int) bool {
	if start < 0 {
		start = 0
	}
	for i := start; i < len(r.diags); i++ {
		if r.diags[i].Severity == Error {
			return true
		}
	}
	return false
}

// RewriteErrorsSince maps every Error diagnostic at index >= start through fn.
func (r *Reporter) RewriteErrorsSince(start int, fn func(Diagnostic) Diagnostic) {
	if start < 0 {
		start = 0
	}
	for i := start; i < len(r.diags); i++ {
		if r.diags[i].Severity == Error {
			r.diags[i] = fn(r.diags[i])
		}
	}
}

// Format renders all collected diagnostics into a human-readable string.
func (r *Reporter) Format() string {
	var sb strings.Builder
	for i, d := range r.diags {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(r.FormatOne(d))
	}
	return sb.String()
}

func (r *Reporter) formatSourceSnippet(sb *strings.Builder, sp Span, prefix string) {
	lines, ok := r.sources[sp.Start.File]
	if !ok {
		return
	}
	lineIdx := sp.Start.Line - 1
	if lineIdx < 0 || lineIdx >= len(lines) {
		return
	}
	srcLine := lines[lineIdx]
	expanded := strings.ReplaceAll(srcLine, "\t", "    ")
	fmt.Fprintf(sb, "%s  %4d | %s\n", prefix, sp.Start.Line, expanded)

	// Compute caret alignment accounting for tabs and UTF-8 runes.
	col := sp.Start.Col - 1
	if col < 0 {
		col = 0
	}
	visualCol := 0
	caretLen := 0
	hasEndCol := sp.End.Line == sp.Start.Line
	endCol := -1
	if hasEndCol {
		endCol = sp.End.Col - 1
	}
	runeIdx := 0
	for _, rn := range srcLine {
		w := 1
		if rn == '\t' {
			w = 4
		}
		if runeIdx < col {
			visualCol += w
		} else if !hasEndCol || runeIdx < endCol {
			caretLen += w
		} else {
			break
		}
		runeIdx++
	}
	if caretLen < 1 {
		caretLen = 1
	}
	fmt.Fprintf(sb, "%s       | %s^%s\n", prefix, strings.Repeat(" ", visualCol), strings.Repeat("~", caretLen-1))
}

// FormatOne renders a single diagnostic into a human-readable string.
func (r *Reporter) FormatOne(d Diagnostic) string {
	var sb strings.Builder
	posStr := d.Span.Start.String()
	prefix := ""
	if posStr != "" {
		prefix = posStr + ": "
	}
	if d.Code != "" {
		fmt.Fprintf(&sb, "%s[%s] %s: %s\n", prefix, d.Code, d.Severity.String(), d.Message)
	} else {
		fmt.Fprintf(&sb, "%s%s: %s\n", prefix, d.Severity.String(), d.Message)
	}
	r.formatSourceSnippet(&sb, d.Span, "")
	if d.Hint != "" {
		fmt.Fprintf(&sb, "       = hint: %s\n", d.Hint)
	}
	for _, rel := range d.Related {
		if relPos := rel.Span.Start.String(); relPos != "" {
			fmt.Fprintf(&sb, "       = note: %s: %s\n", relPos, rel.Message)
			if rel.Span.Start.File != "" {
				r.formatSourceSnippet(&sb, rel.Span, "       ")
			}
		} else {
			fmt.Fprintf(&sb, "       = note: %s\n", rel.Message)
		}
	}
	return sb.String()
}
