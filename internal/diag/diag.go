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
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic represents a single compiler error or warning with source context.
type Diagnostic struct {
	Code     string // e.g. "E0001", "W0001"
	Severity Severity
	Span     Span // what is underlined
	Message  string
	Hint     string
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

// Format renders all collected diagnostics into a human-readable string.
func (r *Reporter) Format() string {
	var sb strings.Builder
	for i, d := range r.diags {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if d.Code != "" {
			fmt.Fprintf(&sb, "%s: [%s] %s: %s\n", d.Span.Start.String(), d.Code, d.Severity.String(), d.Message)
		} else {
			fmt.Fprintf(&sb, "%s: %s: %s\n", d.Span.Start.String(), d.Severity.String(), d.Message)
		}
		if lines, ok := r.sources[d.Span.Start.File]; ok {
			lineIdx := d.Span.Start.Line - 1
			if lineIdx >= 0 && lineIdx < len(lines) {
				srcLine := lines[lineIdx]
				expanded := strings.ReplaceAll(srcLine, "\t", "    ")
				fmt.Fprintf(&sb, "  %4d | %s\n", d.Span.Start.Line, expanded)

				// Compute caret alignment accounting for tabs and UTF-8 runes.
				col := d.Span.Start.Col - 1
				if col < 0 {
					col = 0
				}
				visualCol := 0
				caretLen := 0
				hasEndCol := d.Span.End.Line == d.Span.Start.Line
				endCol := -1
				if hasEndCol {
					endCol = d.Span.End.Col - 1
				}
				runeIdx := 0
				for _, r := range srcLine {
					w := 1
					if r == '\t' {
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
				fmt.Fprintf(&sb, "       | %s^%s\n", strings.Repeat(" ", visualCol), strings.Repeat("~", caretLen-1))
			}
		}
		if d.Hint != "" {
			fmt.Fprintf(&sb, "       = hint: %s\n", d.Hint)
		}
	}
	return sb.String()
}
