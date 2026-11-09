package lsp

import (
	"strings"
)

// computeDocumentFormatting produces a TextEdit replacing the entire document
// with formatted Lowlight source code according to the given options.
func (s *Server) computeDocumentFormatting(src string, opts FormattingOptions) []TextEdit {
	tabSize := opts.TabSize
	if tabSize <= 0 {
		tabSize = 4
	}
	indentUnit := strings.Repeat(" ", tabSize)
	if !opts.InsertSpaces && tabSize == 4 {
		indentUnit = "\t"
	}

	lines := strings.Split(src, "\n")
	var formattedLines []string

	indentLevel := 0

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			formattedLines = append(formattedLines, "")
			continue
		}

		// Adjust indent before writing line if it starts with closing delimiter
		closeCount := countLeadingClosers(trimmed)
		currIndent := indentLevel - closeCount
		if currIndent < 0 {
			currIndent = 0
		}

		lineIndentStr := strings.Repeat(indentUnit, currIndent)
		formattedLines = append(formattedLines, lineIndentStr+trimmed)

		// Update indentLevel for subsequent lines
		openCount := countDelims(trimmed, '{') + countDelims(trimmed, '[')
		closeCountAll := countDelims(trimmed, '}') + countDelims(trimmed, ']')
		indentLevel += (openCount - closeCountAll)
		if indentLevel < 0 {
			indentLevel = 0
		}
	}

	result := strings.Join(formattedLines, "\n")
	if opts.InsertFinalNewline || !strings.HasSuffix(result, "\n") {
		result += "\n"
	}

	if result == src {
		return []TextEdit{}
	}

	fullRange := Range{
		Start: Position{Line: 0, Character: 0},
		End:   OffsetToLSP(src, len(src)),
	}
	return []TextEdit{{Range: fullRange, NewText: result}}
}

func countLeadingClosers(s string) int {
	count := 0
	for _, r := range s {
		if r == '}' || r == ']' {
			count++
		} else {
			break
		}
	}
	return count
}

func countDelims(s string, target rune) int {
	inString := false
	inChar := false
	escaped := false
	count := 0

	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && (inString || inChar) {
			escaped = true
			continue
		}
		if r == '"' && !inChar {
			inString = !inString
			continue
		}
		if r == '\'' && !inString {
			inChar = !inChar
			continue
		}
		if inString || inChar {
			continue
		}
		if r == target {
			count++
		}
	}
	return count
}
