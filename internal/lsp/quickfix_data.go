package lsp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Quick fixes driven by the machine-readable `data` the compiler attaches to diagnostics
// (kind, expected/got types, suggested names, and the full range of the offending expression).

func diagString(d Diagnostic, key string) string {
	if d.Data == nil {
		return ""
	}
	s, _ := d.Data[key].(string)
	return s
}

// diagFullRange returns the whole expression a diagnostic is about (its underline may cover only
// part of it).
func diagFullRange(d Diagnostic) (Range, bool) {
	raw, ok := d.Data["fullRange"]
	if !ok {
		return Range{}, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return Range{}, false
	}
	var r Range
	if json.Unmarshal(b, &r) != nil {
		return Range{}, false
	}
	return r, true
}

// isSimplePostfix reports whether code can take a `.method()` or `: chain` suffix without
// parentheses: names, calls, indexing, and member access, with no operator at the top level.
func isSimplePostfix(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" || strings.HasPrefix(code, "not ") || strings.HasPrefix(code, "-") || strings.HasPrefix(code, "own ") {
		return false
	}
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case inStr != 0:
			if c == '\\' {
				i++
			} else if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '`' || c == '\'':
			inStr = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case depth == 0 && strings.ContainsRune(" +-*/%<>=&|^:", rune(c)):
			return false
		}
	}
	return true
}

func parenthesize(code string) string {
	if isSimplePostfix(code) {
		return code
	}
	return "(" + code + ")"
}

// zeroLiteral is a placeholder value of a primitive type, for `?? fallback` fixes.
func zeroLiteral(ty string) string {
	switch ty {
	case "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64":
		return "0"
	case "float", "f32", "f64":
		return "0.0"
	case "string":
		return `""`
	case "bool":
		return "false"
	}
	return ""
}

func (s *Server) collectDataQuickFixes(filePath, uri, src string, d Diagnostic, addAction func(CodeAction)) {
	replace := func(title string, rng Range, text string, preferred bool) {
		addAction(CodeAction{
			Title:       title,
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			IsPreferred: preferred,
			Edit:        &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{Range: rng, NewText: text}}}},
		})
	}
	full, hasFull := diagFullRange(d)
	fullText := ""
	if hasFull {
		a, b := LSPToOffset(src, full.Start), LSPToOffset(src, full.End)
		if a >= 0 && b <= len(src) && a <= b {
			fullText = src[a:b]
		} else {
			hasFull = false
		}
	}

	switch diagString(d, "kind") {
	case "mismatch":
		if !hasFull {
			return
		}
		fix := diagString(d, "fix")
		expected := diagString(d, "expected")
		switch {
		case strings.HasPrefix(fix, "convert:"):
			ty := strings.TrimPrefix(fix, "convert:")
			replace(fmt.Sprintf("Convert to `%s` with `%s(...)`", ty, ty), full, ty+"("+fullText+")", true)
		case fix == "to_string":
			replace("Convert with `.to_string()`", full, parenthesize(fullText)+".to_string()", true)
		case fix == "unwrap_optional":
			if z := zeroLiteral(expected); z != "" {
				replace(fmt.Sprintf("Unwrap with `?? %s`", z), full, fullText+" ?? "+z, true)
			}
		case fix == "handle_result":
			replace("Unwrap with `default panic`", full, parenthesize(fullText)+": default panic", true)
			if z := zeroLiteral(expected); z != "" {
				replace(fmt.Sprintf("Use a fallback: `?? %s`", z), full, fullText+" ?? "+z, false)
			}
		case fix == "wrap_value":
			replace("Wrap in `value(...)`", full, "value("+fullText+")", true)
		case fix == "nonzero":
			replace("Compare with `!= 0`", full, fullText+" != 0", true)
		case strings.HasPrefix(fix, "wrap_failure:"):
			name := strings.TrimPrefix(fix, "wrap_failure:")
			replace(fmt.Sprintf("Wrap in `%s(...)`", name), full, name+"("+fullText+")", true)
		}

	case "tuple_index":
		if !hasFull {
			return
		}
		if idx := diagString(d, "index"); idx != "" {
			replace(fmt.Sprintf("Replace `[%s]` with `.%s`", idx, idx), full, "."+idx, true)
		}

	case "drop_quest_dot":
		if !hasFull {
			return
		}
		if qIdx := strings.LastIndex(fullText, "?."); qIdx >= 0 {
			qOff := LSPToOffset(src, full.Start) + qIdx
			qRng := Range{Start: OffsetToLSP(src, qOff), End: OffsetToLSP(src, qOff+1)}
			replace("Replace `?.` with `.`", qRng, "", true)
		}

	case "remove_parens":
		if !hasFull {
			return
		}
		if openIdx := strings.LastIndexByte(fullText, '('); openIdx >= 0 && strings.HasSuffix(strings.TrimSpace(fullText), ")") {
			pOff := LSPToOffset(src, full.Start) + openIdx
			pRng := Range{Start: OffsetToLSP(src, pOff), End: full.End}
			replace("Remove `()`", pRng, "", true)
		}

	case "append_copy":
		if !hasFull {
			return
		}
		replace("Append `.copy()`", full, parenthesize(fullText)+".copy()", true)

	case "strip_ll_import":
		oldPath := diagString(d, "oldPath")
		newPath := diagString(d, "newPath")
		if oldPath == "" || newPath == "" {
			return
		}
		sOff, eOff := LSPToOffset(src, d.Range.Start), LSPToOffset(src, d.Range.End)
		if sOff >= 0 && eOff <= len(src) && sOff < eOff {
			if idx := strings.Index(src[sOff:eOff], oldPath); idx >= 0 {
				pOff := sOff + idx
				pRng := Range{Start: OffsetToLSP(src, pOff), End: OffsetToLSP(src, pOff+len(oldPath))}
				replace(fmt.Sprintf("Change import to %q", newPath), pRng, newPath, true)
			}
		}

	case "insert_colon":
		off := LSPToOffset(src, d.Range.Start)
		if off < 0 || off > len(src) {
			return
		}
		insOff := off
		for insOff > 0 && (src[insOff-1] == ' ' || src[insOff-1] == '\t') {
			insOff--
		}
		insText := ":"
		if insOff == off {
			insText = ": "
		}
		at := Range{Start: OffsetToLSP(src, insOff), End: OffsetToLSP(src, insOff)}
		replace("Insert `:` before `{`", at, insText, true)

	case "else_to_default":
		sOff, eOff := LSPToOffset(src, d.Range.Start), LSPToOffset(src, d.Range.End)
		if sOff >= 0 && eOff <= len(src) && src[sOff:eOff] == "else" {
			replace("Replace `else` with `default`", d.Range, "default", true)
		}

	case "strip_opt_suffix":
		name := diagString(d, "name")
		if !strings.HasSuffix(name, "?") {
			return
		}
		plain := strings.TrimSuffix(name, "?")
		sOff, eOff := LSPToOffset(src, d.Range.Start), LSPToOffset(src, d.Range.End)
		if sOff >= 0 && eOff <= len(src) && src[sOff:eOff] == name {
			replace(fmt.Sprintf("Remove `?` (`%s`)", plain), d.Range, plain, true)
		}

	case "rename":
		name := diagString(d, "name")
		rng, ok := findWordRangeInSpan(src, d.Range, name)
		if !ok {
			return
		}
		for i, c := range strings.Split(diagString(d, "candidates"), ",") {
			if c = strings.TrimSpace(c); c != "" {
				replace(fmt.Sprintf("Change to `%s`", c), rng, c, i == 0)
			}
		}

	case "bind_to_let":
		off, err := strconv.Atoi(diagString(d, "declOffset"))
		declFile := diagString(d, "declFile")
		if err != nil || off < 0 || off+4 > len(src) || (declFile != "" && absClean(declFile) != absClean(filePath)) {
			return
		}
		if src[off:off+4] != "bind" {
			return
		}
		name := extractQuotedWord(d.Message)
		start := OffsetToLSP(src, off)
		replace(fmt.Sprintf("Declare `%s` with `let`", name), Range{Start: start, End: OffsetToLSP(src, off+4)}, "let", true)

	case "truthiness":
		if !hasFull {
			return
		}
		got := diagString(d, "got")
		switch {
		case zeroLiteral(got) != "" && got != "string" && got != "bool":
			z := zeroLiteral(got)
			replace(fmt.Sprintf("Compare with `!= %s`", z), full, fullText+" != "+z, true)
		case got == "string" || strings.HasPrefix(got, "["):
			replace("Test `not ….is_empty()`", full, "not "+parenthesize(fullText)+".is_empty()", true)
		}
	}
}
