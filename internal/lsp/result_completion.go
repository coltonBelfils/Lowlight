package lsp

import (
	"fmt"
	"path/filepath"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

// Completion support for result declarations (SPECIFICATION.md §3.8.3, §7.6.5):
//   - inside a result chain's arms, the subject's failure names (`when missing(why):`);
//   - where a result is the expected value, the result's bare members (`value(...)`, `err(...)`);
//   - after `define Name`, the declaration kinds including `result`.

// lineInfo splits src into lines and returns the 0-based line containing offset and its start.
func lineInfo(src string, offset int) (lines []string, line, lineStart int) {
	lines = strings.Split(src, "\n")
	pos := 0
	for i, l := range lines {
		if offset <= pos+len(l) {
			return lines, i, pos
		}
		pos += len(l) + 1
	}
	return lines, len(lines) - 1, pos
}

func indentWidth(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}

// chainHeadKeywords start lines whose trailing `:` opens something other than a result chain.
var chainHeadKeywords = []string{"match", "if", "if(", "else", "for", "while", "bind ", "define ", "when ", "default", "unsafe"}

// armParentHeaderLine finds the line (0-based) and trimmed text of the enclosing `: `-terminated
// header above a `when`/`default`/empty arm line.
func armParentHeaderLine(lines []string, line int) (int, string, bool) {
	cur := strings.TrimSpace(lines[line])
	isArm := cur == "" || strings.HasPrefix(cur, "when") || strings.HasPrefix(cur, "default") ||
		strings.HasPrefix("when", cur) || strings.HasPrefix("default", cur)
	if !isArm {
		return 0, "", false
	}
	ind := indentWidth(lines[line])
	for i := line - 1; i >= 0; i-- {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		if indentWidth(l) >= ind {
			continue
		}
		head := strings.TrimSpace(l)
		if !strings.HasSuffix(head, ":") {
			return 0, "", false
		}
		return i, head, true
	}
	return 0, "", false
}

// chainHeadLine finds the line (0-based) that opens the result chain whose arms the cursor's line
// belongs to, when that line is `when ...` / `default ...` (or the bare words being typed).
func chainHeadLine(lines []string, line int) (int, bool) {
	i, head, ok := armParentHeaderLine(lines, line)
	if !ok {
		return 0, false
	}
	for _, kw := range chainHeadKeywords {
		if strings.HasPrefix(head, kw) {
			return 0, false
		}
	}
	if strings.Contains(head, "match(") || strings.Contains(head, "match (") || strings.Contains(head, "= (") {
		return 0, false // a `match` header or a lambda/function signature
	}
	return i, true
}

func looksLikeMatchHeader(head string) bool {
	if strings.HasPrefix(head, "match(") || strings.HasPrefix(head, "match (") {
		return true
	}
	for _, sep := range []string{"= match(", "= match (", "<- match(", "<- match (", ": match(", ": match ("} {
		if strings.Contains(head, sep) {
			return true
		}
	}
	return false
}

// matchHeadLine finds the line (0-based) of the `match(...):` header enclosing `line`.
func matchHeadLine(lines []string, line int) (int, bool) {
	i, head, ok := armParentHeaderLine(lines, line)
	if !ok || !looksLikeMatchHeader(head) {
		return 0, false
	}
	return i, true
}

// matchPatternSlot inspects the text before the cursor prefix on a match arm line (`when ...` or
// `default ...`) and returns whether it is a variant pattern position, the 0-based scrutinee slot
// index, and whether it is inside a multi-arg tuple `(a, b)`.
func matchPatternSlot(before string) (slotIdx int, inTuple bool, ok bool) {
	var rest string
	switch {
	case before == "when" || before == "default":
		return 0, false, true
	case strings.HasPrefix(before, "when") && len(before) > 4 && (before[4] == ' ' || before[4] == '\t' || before[4] == '('):
		rest = strings.TrimSpace(before[4:])
	case strings.HasPrefix(before, "default") && len(before) > 7 && (before[7] == ' ' || before[7] == '\t' || before[7] == '('):
		rest = strings.TrimSpace(before[7:])
	default:
		return 0, false, false
	}
	if rest == "" {
		return 0, false, true
	}
	if strings.HasSuffix(rest, "(") {
		preParen := strings.TrimSpace(rest[:len(rest)-1])
		if preParen == "" || strings.HasSuffix(preParen, ",") {
			return 0, true, true
		}
		return 0, false, false
	}
	if !strings.HasSuffix(rest, ",") {
		return 0, false, false
	}
	depth := 0
	depth0Commas := 0
	depth1Commas := 0
	inStr := byte(0)
	escaped := false
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
		if inStr != 0 {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == inStr {
				inStr = 0
			}
			continue
		}
		switch ch {
		case '"', '\'':
			inStr = ch
		case '(':
			if depth == 0 {
				depth1Commas = 0
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 1 {
				depth1Commas++
			} else if depth == 0 {
				depth0Commas++
			}
		}
	}
	if depth == 1 {
		return depth1Commas, true, true
	}
	if depth == 0 {
		return depth0Commas, false, true
	}
	return 0, false, false
}

func matchScrutineeEnumInProg(prog *sema.CheckedProgram, abs string, line, slotIdx int) (*types.Enum, int) {
	if prog == nil || slotIdx < 0 {
		return nil, 0
	}
	var bestME *ast.MatchExpr
	var bestBM *sema.BodyMaps
	consider := func(bm *sema.BodyMaps) {
		if bm == nil {
			return
		}
		for e := range bm.ExprInfos {
			me, ok := e.(*ast.MatchExpr)
			if !ok || me.ResultChain || len(me.Scrutinees) == 0 {
				continue
			}
			if me.Span.Start.Line != line || (me.Span.Start.File != "" && me.Span.Start.File != abs) {
				continue
			}
			if bestME == nil || me.Span.Start.Offset > bestME.Span.Start.Offset {
				bestME = me
				bestBM = bm
			}
		}
	}
	for _, fi := range prog.Funcs {
		consider(fi.BodyMaps)
	}
	for _, li := range prog.Lambdas {
		consider(li.BodyMaps)
	}
	if bestME == nil || bestBM == nil {
		return nil, 0
	}
	numScrut := len(bestME.Scrutinees)
	idx := slotIdx
	if numScrut == 1 {
		idx = 0
	} else if idx >= numScrut {
		return nil, 0
	}
	inf := bestBM.ExprInfos[bestME.Scrutinees[idx]]
	if inf == nil || inf.Type == nil {
		return nil, 0
	}
	ty := inf.Type
	if rf, ok := ty.(*types.Ref); ok {
		ty = rf.Elem
	}
	en, _ := ty.(*types.Enum)
	return en, numScrut
}

// matchArmEnum returns the enum type for the scrutinee slot at the cursor's `match` arm, whether
// unexported variants are visible in `filePath`, and the total number of scrutinees.
func (s *Server) matchArmEnum(filePath, src string, offset, slotIdx int) (*types.Enum, bool, int) {
	lines, line, _ := lineInfo(src, offset)
	head, ok := matchHeadLine(lines, line)
	if !ok {
		return nil, false, 0
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	prog := s.getOrCheckProgram(filePath, src)
	en, numScrut := matchScrutineeEnumInProg(prog, abs, head+1, slotIdx)
	if en == nil {
		end := line + 1
		ind := indentWidth(lines[line])
		for end < len(lines) && (strings.TrimSpace(lines[end]) == "" || indentWidth(lines[end]) >= ind) {
			end++
		}
		armIndent := lines[line][:indentWidth(lines[line])]
		repaired := append([]string{}, lines[:head+1]...)
		repaired = append(repaired, armIndent+`default: panic("")`)
		repaired = append(repaired, lines[end:]...)
		overlays := s.snapshotOverlays()
		overlays[abs] = strings.Join(repaired, "\n")
		prog = sema.CheckForIDE(abs, overlays, s.projectRootFor(abs), diag.NewReporter())
		en, numScrut = matchScrutineeEnumInProg(prog, abs, head+1, slotIdx)
	}
	if en == nil {
		return nil, false, 0
	}
	mod := findModuleForPath(prog, filePath)
	samePkg := mod == nil || prog == nil || prog.SamePackageDir(mod, en.PkgDir, "") || prog.SamePackage(mod, prog.EnumMods[en])
	return en, samePkg, numScrut
}

// completeMatchArmEnumPatterns offers bare enum variant patterns when the cursor is in a pattern
// position of an enum `match` arm. The boolean result reports whether the cursor is in such a
// position (even if `prefix` matched no variants).
func (s *Server) completeMatchArmEnumPatterns(filePath, src string, offset, prefixStart int, prefix string, replaceRange Range) ([]CompletionItem, bool) {
	_, _, lineStart := lineInfo(src, offset)
	before := strings.TrimSpace(src[lineStart:prefixStart])
	slotIdx, inTuple, ok := matchPatternSlot(before)
	if !ok {
		return nil, false
	}
	en, samePkg, numScrut := s.matchArmEnum(filePath, src, offset, slotIdx)
	if en == nil {
		return nil, false
	}
	appendColon := numScrut == 1 || (!inTuple && slotIdx == numScrut-1)
	return enumMatchPatternItems(en, samePkg, prefix, replaceRange, appendColon), true
}

// chainSubjectInProg finds the result type of a chain whose subject starts on 1-based line
// `line` of file `abs` in prog.
func chainSubjectInProg(prog *sema.CheckedProgram, abs string, line int) *types.Enum {
	if prog == nil {
		return nil
	}
	check := func(bm *sema.BodyMaps) *types.Enum {
		if bm == nil {
			return nil
		}
		for e := range bm.ExprInfos {
			me, ok := e.(*ast.MatchExpr)
			if !ok || !me.ResultChain || len(me.Scrutinees) == 0 {
				continue
			}
			sp := me.Scrutinees[0].GetSpan()
			if sp.Start.Line != line || (sp.Start.File != "" && sp.Start.File != abs) {
				continue
			}
			if inf := bm.ExprInfos[me.Scrutinees[0]]; inf != nil {
				if en := types.AsResult(unwrapOptAndRef(inf.Type)); en != nil {
					return en
				}
			}
		}
		return nil
	}
	for _, fi := range prog.Funcs {
		if en := check(fi.BodyMaps); en != nil {
			return en
		}
	}
	for _, li := range prog.Lambdas {
		if en := check(li.BodyMaps); en != nil {
			return en
		}
	}
	return nil
}

// chainArmResult returns the result type whose failures the cursor's chain arm can name. While
// an arm is half-typed the file rarely checks, so the chain is re-checked with its arms replaced
// by `default panic`.
func (s *Server) chainArmResult(filePath, src string, offset int) *types.Enum {
	lines, line, _ := lineInfo(src, offset)
	head, ok := chainHeadLine(lines, line)
	if !ok {
		return nil
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	if en := chainSubjectInProg(s.getOrCheckProgram(filePath, src), abs, head+1); en != nil {
		return en
	}
	// Repair: `subject: default panic`, dropping the chain's arms up to the cursor's line.
	end := line + 1
	ind := indentWidth(lines[line])
	for end < len(lines) && (strings.TrimSpace(lines[end]) == "" || indentWidth(lines[end]) >= ind) {
		end++
	}
	repaired := append([]string{}, lines[:head]...)
	repaired = append(repaired, strings.TrimRight(lines[head], " \t")+" default panic")
	repaired = append(repaired, lines[end:]...)
	overlays := s.snapshotOverlays()
	fixed := strings.Join(repaired, "\n")
	overlays[abs] = fixed
	prog := sema.CheckForIDE(abs, overlays, s.projectRootFor(abs), diag.NewReporter())
	return chainSubjectInProg(prog, abs, head+1)
}

// enclosingResultReturn returns the result type the function containing offset returns.
func enclosingResultReturn(prog *sema.CheckedProgram, mod *sema.Module, offset int) *types.Enum {
	if prog == nil || mod == nil {
		return nil
	}
	var best *sema.FuncInstance
	for _, fi := range prog.Funcs {
		if fi.Mod != mod || fi.Sig == nil || !spanContainsOffset(fi.DeclSpan, offset) {
			continue
		}
		if best == nil || fi.DeclSpan.Start.Offset > best.DeclSpan.Start.Offset {
			best = fi
		}
	}
	if best == nil {
		return nil
	}
	return types.AsResult(best.Sig.Ret)
}

func variantCompletionItem(en *types.Enum, idx int, prefix string, replaceRange Range, forPattern, appendColon bool) (CompletionItem, bool) {
	v := en.Variants[idx]
	if !matchesPrefix(v.Name, prefix) {
		return CompletionItem{}, false
	}
	insert := v.Name
	detail := fmt.Sprintf("%s.%s", en.String(), v.Name)
	var doc string
	if !en.IsTagged {
		detail = fmt.Sprintf("%s.%s = %d (%s)", en.Name, v.Name, v.Value, en.Underlying.String())
		doc = fmt.Sprintf("Enum variant of `%s`.", en.String())
	} else {
		doc = variantKindNote(en, &en.Variants[idx])
	}
	if v.Kind == types.VariantTuple && v.Payload != nil {
		if forPattern {
			insert = v.Name + "(${1:_})"
		} else {
			insert = v.Name + "($1)"
		}
		detail = fmt.Sprintf("%s.%s(%s)", en.String(), v.Name, v.Payload.String())
	}
	if appendColon {
		insert += ": "
	}
	if v.Doc != "" {
		doc = strings.TrimSpace(v.Doc) + "\n\n" + doc
	}
	return CompletionItem{
		Label:            v.Name,
		Kind:             KindEnumMember,
		Detail:           detail,
		SortText:         fmt.Sprintf("0_%02d_%s", idx, v.Name),
		FilterText:       v.Name,
		InsertTextFormat: InsertSnippet,
		TextEdit:         &TextEdit{Range: replaceRange, NewText: insert},
		Documentation:    &MarkupContent{Kind: "markdown", Value: doc},
	}, true
}

// enumMatchPatternItems completes bare enum variant patterns inside a `match` arm.
func enumMatchPatternItems(en *types.Enum, samePkg bool, prefix string, replaceRange Range, appendColon bool) []CompletionItem {
	var items []CompletionItem
	for i, v := range en.Variants {
		if !samePkg && !sema.IsExported(v.Name) {
			continue
		}
		if it, ok := variantCompletionItem(en, i, prefix, replaceRange, true, appendColon); ok {
			items = append(items, it)
		}
	}
	return items
}

// resultMemberItems completes the members of result `en`. For chain patterns (`forPattern`), only
// failures are offered, as `missing(${1:_})` / `forbidden`; as constructors, `value(...)` comes
// first.
func resultMemberItems(en *types.Enum, prefix string, replaceRange Range, forPattern bool) []CompletionItem {
	var items []CompletionItem
	for i, v := range en.Variants {
		if forPattern && v.Name == types.ResultValueName {
			continue
		}
		if it, ok := variantCompletionItem(en, i, prefix, replaceRange, forPattern, forPattern); ok {
			items = append(items, it)
		}
	}
	if forPattern && matchesPrefix("default", prefix) {
		items = append(items, CompletionItem{
			Label:            "default panic",
			Kind:             KindKeyword,
			Detail:           "panic on every failure not handled above",
			SortText:         "1_default_panic",
			FilterText:       "default panic",
			InsertTextFormat: InsertPlainText,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: "default panic"},
			Documentation: &MarkupContent{Kind: "markdown", Value: "Ends the chain: any failure no arm handles panics with " +
				"`unhandled failure " + en.Name + ".variant` at this line."},
		})
	}
	return items
}

// defineKindItems completes the declaration kind after `define Name`.
func defineKindItems(prefix string, replaceRange Range) []CompletionItem {
	kinds := []struct{ word, detail, snippet string }{
		{"struct", "value struct `[field: T, ...]`", "struct [\n\t${1:field}: ${2:int},\n]"},
		{"enum", "tag enum or tagged union `[a, b: T, ...]`", "enum [\n\t${1:a},\n\t${2:b},\n]"},
		{"result", "result type `[value: T, failure: P, ...]`", "result [\n\tvalue: ${1:T},\n\t${2:err}: ${3:Error},\n]"},
		{"interface", "structural interface `[method: fn(self) T, ...]`", "interface [\n\t${1:method}: fn(self) ${2:string},\n]"},
	}
	var items []CompletionItem
	for i, k := range kinds {
		if !matchesPrefix(k.word, prefix) {
			continue
		}
		items = append(items, CompletionItem{
			Label:            k.word,
			Kind:             KindKeyword,
			Detail:           k.detail,
			SortText:         fmt.Sprintf("0_%d_%s", i, k.word),
			FilterText:       k.word,
			InsertTextFormat: InsertSnippet,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: k.snippet},
		})
	}
	return items
}
