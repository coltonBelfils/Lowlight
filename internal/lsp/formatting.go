package lsp

import (
	"strings"
)

type matchScope struct {
	braceDepth int
	baseIndent int
	exitIndent int
	sawElse    bool
}

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
	braceDepth := 0
	var matchStack []matchScope
	inBlockComment := false
	hangingArmIndent := false
	hangingElse := false

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			formattedLines = append(formattedLines, "")
			continue
		}

		clean := stripCommentsAndStrings(rawLine, &inBlockComment)
		cleanTrimmed := strings.TrimSpace(clean)

		if cleanTrimmed == "" {
			commentIndent := indentLevel
			if len(matchStack) > 0 && !matchStack[len(matchStack)-1].sawElse {
				commentIndent = matchStack[len(matchStack)-1].baseIndent + 1
			}
			formattedLines = append(formattedLines, strings.Repeat(indentUnit, commentIndent)+trimmed)
			continue
		}

		closeCount := countLeadingClosers(cleanTrimmed)

		isWhen := startsWithWhen(cleanTrimmed) && len(matchStack) > 0
		isDefaultArm := isMatchDefaultArm(cleanTrimmed) && len(matchStack) > 0

		var currIndent int
		if isWhen || isDefaultArm {
			top := matchStack[len(matchStack)-1]
			currIndent = top.baseIndent + 1
			hangingArmIndent = false
			hangingElse = false
		} else if hangingArmIndent && len(matchStack) > 0 {
			top := matchStack[len(matchStack)-1]
			currIndent = top.baseIndent + 2
			hangingArmIndent = false
		} else {
			currIndent = indentLevel - closeCount
			if currIndent < 0 {
				currIndent = 0
			}
		}

		formattedLines = append(formattedLines, strings.Repeat(indentUnit, currIndent)+trimmed)

		openCount := countDelims(cleanTrimmed, '{') + countDelims(cleanTrimmed, '[')
		closeCountAll := countDelims(cleanTrimmed, '}') + countDelims(cleanTrimmed, ']')
		braceDepth += (openCount - closeCountAll)
		if braceDepth < 0 {
			braceDepth = 0
		}

		for len(matchStack) > 0 {
			top := matchStack[len(matchStack)-1]
			if braceDepth <= top.braceDepth && top.sawElse {
				matchStack = matchStack[:len(matchStack)-1]
				indentLevel = top.exitIndent
			} else if braceDepth < top.braceDepth {
				matchStack = matchStack[:len(matchStack)-1]
				indentLevel = top.exitIndent
			} else {
				break
			}
		}

		endsInColon := strings.HasSuffix(cleanTrimmed, ":")
		hasNestedMatch := containsMatchExpr(cleanTrimmed)

		if hangingElse && len(matchStack) > 0 && matchStack[len(matchStack)-1].sawElse {
			popped := matchStack[len(matchStack)-1]
			matchStack = matchStack[:len(matchStack)-1]
			indentLevel = popped.exitIndent
			hangingElse = false
		}

		if isDefaultArm && len(matchStack) > 0 {
			topIdx := len(matchStack) - 1
			matchStack[topIdx].sawElse = true
			if openCount == 0 && !endsInColon {
				popped := matchStack[topIdx]
				matchStack = matchStack[:topIdx]
				indentLevel = popped.exitIndent
			} else if openCount == 0 && endsInColon && !hasNestedMatch {
				hangingArmIndent = true
				hangingElse = true
			}
		} else if isWhen && endsInColon && openCount == 0 && !hasNestedMatch {
			hangingArmIndent = true
		}

		if hasNestedMatch {
			matchStack = append(matchStack, matchScope{
				braceDepth: braceDepth,
				baseIndent: currIndent,
				exitIndent: indentLevel,
				sawElse:    false,
			})
		}

		if isWhen || isDefaultArm {
			if openCount > closeCountAll {
				indentLevel = currIndent + (openCount - closeCountAll)
			}
		} else if openCount > 0 || closeCountAll > 0 {
			indentLevel += (openCount - closeCountAll)
			if indentLevel < 0 {
				indentLevel = 0
			}
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

func stripCommentsAndStrings(s string, inBlockComment *bool) string {
	var sb strings.Builder
	runes := []rune(s)
	n := len(runes)
	i := 0

	for i < n {
		if *inBlockComment {
			if i+1 < n && runes[i] == '*' && runes[i+1] == '/' {
				*inBlockComment = false
				i += 2
				continue
			}
			i++
			continue
		}

		r := runes[i]
		if i+1 < n && r == '/' && runes[i+1] == '*' {
			*inBlockComment = true
			i += 2
			continue
		}
		if i+1 < n && r == '/' && runes[i+1] == '/' {
			break
		}
		if r == '"' || r == '`' {
			quote := r
			i++
			escaped := false
			for i < n {
				if escaped {
					escaped = false
					i++
					continue
				}
				if runes[i] == '\\' && quote == '"' {
					escaped = true
					i++
					continue
				}
				if runes[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		if r == '\'' {
			i++
			escaped := false
			for i < n {
				if escaped {
					escaped = false
					i++
					continue
				}
				if runes[i] == '\\' {
					escaped = true
					i++
					continue
				}
				if runes[i] == '\'' {
					i++
					break
				}
				i++
			}
			continue
		}

		sb.WriteRune(r)
		i++
	}

	return sb.String()
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func startsWithWhen(clean string) bool {
	trimmed := strings.TrimSpace(clean)
	if strings.HasPrefix(trimmed, "when") {
		rest := trimmed[4:]
		if len(rest) == 0 || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '(' {
			return true
		}
	}
	return false
}

func isMatchDefaultArm(clean string) bool {
	trimmed := strings.TrimSpace(clean)
	if !strings.HasPrefix(trimmed, "default") {
		return false
	}
	rest := trimmed[len("default"):]
	if len(rest) == 0 {
		return false
	}
	if rest[0] != ' ' && rest[0] != '\t' && rest[0] != ':' {
		return false
	}
	for _, r := range rest {
		if r == ':' {
			return true
		}
		if r == '{' || r == ';' {
			return false
		}
	}
	return false
}

func containsMatchExpr(code string) bool {
	for len(code) > 0 {
		idx := strings.Index(code, "match")
		if idx == -1 {
			break
		}
		if idx > 0 && isIdentByte(code[idx-1]) {
			code = code[idx+5:]
			continue
		}
		after := code[idx+5:]
		if len(after) > 0 && isIdentByte(after[0]) {
			code = code[idx+5:]
			continue
		}
		rest := strings.TrimSpace(after)
		if strings.HasPrefix(rest, "<-") {
			code = code[idx+5:]
			continue
		}
		if len(rest) == 0 || rest[0] == '(' || rest[0] == ':' || strings.HasPrefix(rest, "when") || strings.HasPrefix(rest, "//") || strings.HasPrefix(rest, "/*") {
			return true
		}
		code = code[idx+5:]
	}
	return false
}

func countLeadingClosers(s string) int {
	count := 0
	for _, r := range s {
		if r == '}' || r == ']' {
			count++
		} else if r == ')' || r == ',' || r == '.' {
			continue
		} else {
			break
		}
	}
	return count
}

func countDelims(s string, target rune) int {
	count := 0
	for _, r := range s {
		if r == target {
			count++
		}
	}
	return count
}
