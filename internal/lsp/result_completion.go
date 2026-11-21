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
//   - inside a result chain's arms, the subject's failure names (`when Missing(why):`);
//   - where a result is the expected value, the result's bare members (`value(...)`, `Err(...)`);
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

// chainHeadLine finds the line (0-based) that opens the result chain whose arms the cursor's line
// belongs to, when that line is `when ...` / `default ...` (or the bare words being typed).
func chainHeadLine(lines []string, line int) (int, bool) {
	cur := strings.TrimSpace(lines[line])
	isArm := cur == "" || strings.HasPrefix(cur, "when") || strings.HasPrefix(cur, "default") ||
		strings.HasPrefix("when", cur) || strings.HasPrefix("default", cur)
	if !isArm {
		return 0, false
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
	return 0, false
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

// resultMemberItems completes the members of result `en`. For chain patterns (`forPattern`), only
// failures are offered, as `Missing(${1:_})` / `Forbidden`; as constructors, `value(...)` comes
// first.
func resultMemberItems(en *types.Enum, prefix string, replaceRange Range, forPattern bool) []CompletionItem {
	var items []CompletionItem
	for i, v := range en.Variants {
		if forPattern && v.Name == types.ResultValueName {
			continue
		}
		if !matchesPrefix(v.Name, prefix) {
			continue
		}
		insert := v.Name
		detail := fmt.Sprintf("%s.%s", en.String(), v.Name)
		if v.Kind == types.VariantTuple && v.Payload != nil {
			if forPattern {
				insert = v.Name + "(${1:_})"
			} else {
				insert = v.Name + "($1)"
			}
			detail = fmt.Sprintf("%s.%s(%s)", en.String(), v.Name, v.Payload.String())
		}
		if forPattern {
			insert += ": "
		}
		doc := variantKindNote(en, &en.Variants[i])
		if v.Doc != "" {
			doc = strings.TrimSpace(v.Doc) + "\n\n" + doc
		}
		items = append(items, CompletionItem{
			Label:            v.Name,
			Kind:             KindEnumMember,
			Detail:           detail,
			SortText:         fmt.Sprintf("0_%02d_%s", i, v.Name),
			FilterText:       v.Name,
			InsertTextFormat: InsertSnippet,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: insert},
			Documentation:    &MarkupContent{Kind: "markdown", Value: doc},
		})
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
				"`unhandled failure " + en.Name + ".Variant` at this line."},
		})
	}
	return items
}

// defineKindItems completes the declaration kind after `define Name`.
func defineKindItems(prefix string, replaceRange Range) []CompletionItem {
	kinds := []struct{ word, detail, snippet string }{
		{"struct", "value struct `[field: T, ...]`", "struct [\n\t${1:field}: ${2:int},\n]"},
		{"enum", "tag enum or tagged union `[A, B: T, ...]`", "enum [\n\t${1:A},\n\t${2:B},\n]"},
		{"result", "result type `[value: T, Failure: P, ...]`", "result [\n\tvalue: ${1:T},\n\t${2:Err}: ${3:Error},\n]"},
		{"interface", "structural interface `[method: (self) T, ...]`", "interface [\n\t${1:method}: (self) ${2:string},\n]"},
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
