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
	Severity Severity
	Span     Span
	Message  string
	Hint     string
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
func (r *Reporter) Errorf(span Span, format string, args ...any) {
	r.diags = append(r.diags, Diagnostic{
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
		fmt.Fprintf(&sb, "%s: %s: %s\n", d.Span.Start.String(), d.Severity.String(), d.Message)
		if lines, ok := r.sources[d.Span.Start.File]; ok {
			lineIdx := d.Span.Start.Line - 1
			if lineIdx >= 0 && lineIdx < len(lines) {
				srcLine := lines[lineIdx]
				expanded := strings.ReplaceAll(srcLine, "\t", "    ")
				fmt.Fprintf(&sb, "  %4d | %s\n", d.Span.Start.Line, expanded)

				// Compute caret alignment accounting for tabs.
				col := d.Span.Start.Col - 1
				if col < 0 {
					col = 0
				}
				visualCol := 0
				for j := 0; j < col && j < len(srcLine); j++ {
					if srcLine[j] == '\t' {
						visualCol += 4
					} else {
						visualCol++
					}
				}
				caretLen := 1
				if d.Span.End.Line == d.Span.Start.Line && d.Span.End.Col > d.Span.Start.Col {
					caretLen = d.Span.End.Col - d.Span.Start.Col
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
