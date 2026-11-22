package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/format"
	"lowlight/internal/manifest"
	"lowlight/internal/parser"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

func detectFileIndent(filePath, src string) string {
	if filePath != "" {
		if opts, ok := format.LoadConfigFound(filepath.Dir(filePath)); ok {
			return opts.IndentString(1)
		}
	}
	for _, line := range strings.Split(src, "\n") {
		if len(line) == 0 {
			continue
		}
		if line[0] == '\t' {
			return "\t"
		}
		if line[0] == ' ' {
			trimmed := strings.TrimLeft(line, " ")
			n := len(line) - len(trimmed)
			if n >= 2 && n <= 8 && trimmed != "" {
				return line[:n]
			}
		}
	}
	return "\t"
}

// formatOptionsForIndent returns formatter options whose indentation matches `ind` (one level of
// the file's detected indentation).
func formatOptionsForIndent(ind string) format.Options {
	opts := format.DefaultOptions()
	if ind != "" && ind[0] == ' ' {
		opts.IndentStyle = "spaces"
		opts.TabWidth = len(ind)
	}
	return opts
}

// formatGeneratedDecl runs generated top-level source through the formatter so code actions emit
// exactly what `lowlight fmt` would (line wrapping, trailing commas, spacing). Source that does
// not parse on its own is returned unchanged.
func formatGeneratedDecl(text, ind string) string {
	out, err := format.Format("generated.ll", text+"\n", formatOptionsForIndent(ind))
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}

// wrapBodyInBlockTitle names the one action whose output is intentionally not formatter-stable:
// it opens a `{ ... }` block for the user to add statements to.
const wrapBodyInBlockTitle = "Wrap body in '{ ... }' block"

// isTopLevelStart reports whether `line` begins a top-level declaration (or its doc comment or
// attributes) at column 0.
func isTopLevelStart(line string) bool {
	for _, p := range []string{"bind ", "define ", "let ", "import ", "extern ", "@", "//"} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// declRegion expands the byte range [lo, hi) of src to whole top-level declarations: from the
// first line of the declaration block containing `lo` (including its doc comments and
// attributes) to the end of the declaration containing `hi`.
func declRegion(src string, lo, hi int) (int, int) {
	lines := strings.Split(src, "\n")
	starts := make([]int, len(lines)+1)
	for i, l := range lines {
		starts[i+1] = starts[i] + len(l) + 1
	}
	lineOf := func(off int) int {
		for i := 0; i < len(lines); i++ {
			if off < starts[i+1] {
				return i
			}
		}
		return len(lines) - 1
	}
	first, last := lineOf(lo), lineOf(hi)
	for first > 0 && !(isTopLevelStart(lines[first]) && strings.TrimSpace(lines[first-1]) == "") {
		first--
	}
	for last+1 < len(lines) && !(strings.TrimSpace(lines[last]) == "" && isTopLevelStart(lines[last+1])) {
		last++
	}
	for last > first && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	end := starts[last] + len(lines[last])
	if end > len(src) {
		end = len(src)
	}
	return starts[first], end
}

// formatActionEdits keeps formatted code formatted: when every declaration an action edits was
// already formatter-stable, the edited declarations are re-formatted and the action's edits are
// replaced by a single edit producing exactly what `lowlight fmt` would. Actions on code that was
// not formatted, or whose result does not parse, are left as generated.
func formatActionEdits(uri, src, ind string, act *CodeAction) {
	if act.Edit == nil || len(act.Edit.Changes[uri]) == 0 || len(act.Edit.Changes) != 1 {
		return
	}
	edits := act.Edit.Changes[uri]
	lo, hi := len(src), 0
	delta := 0
	for _, e := range edits {
		a, b := LSPToOffset(src, e.Range.Start), LSPToOffset(src, e.Range.End)
		if a < lo {
			lo = a
		}
		if b > hi {
			hi = b
		}
		delta += len(e.NewText) - (b - a)
	}
	if hi < lo {
		hi = lo
	}
	rs, re := declRegion(src, lo, hi)
	opts := formatOptionsForIndent(ind)
	orig := src[rs:re]
	if f, err := format.Format("region.ll", orig+"\n", opts); err != nil || strings.TrimRight(f, "\n") != orig {
		return // not formatted before the action: don't reformat the user's code
	}
	newSrc := applyEditsToSource(src, edits)
	newRegion := newSrc[rs : re+delta]
	f, err := format.Format("region.ll", newRegion+"\n", opts)
	if err != nil {
		return
	}
	formatted := strings.TrimRight(f, "\n")
	if formatted == newRegion {
		return
	}
	act.Edit.Changes[uri] = []TextEdit{{
		Range:   Range{Start: OffsetToLSP(src, rs), End: OffsetToLSP(src, re)},
		NewText: formatted,
	}}
}

// applyEditsToSource applies non-overlapping LSP edits to src.
func applyEditsToSource(src string, edits []TextEdit) string {
	type span struct {
		a, b int
		text string
	}
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		spans = append(spans, span{LSPToOffset(src, e.Range.Start), LSPToOffset(src, e.Range.End), e.NewText})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].a > spans[j].a })
	out := src
	for _, sp := range spans {
		out = out[:sp.a] + sp.text + out[sp.b:]
	}
	return out
}

func (s *Server) computeCodeActions(filePath string, src string, params CodeActionParams) []CodeAction {
	uri := PathToURI(filePath)
	diags := params.Context.Diagnostics
	latest := s.latestDiagnosticsFor(filePath)
	if len(diags) == 0 {
		for _, d := range latest {
			if rangesOverlap(d.Range, params.Range) {
				diags = append(diags, d)
			}
		}
	}
	// Clients that drop `data` from echoed diagnostics get it back from the published set.
	for i := range diags {
		if diags[i].Data != nil {
			continue
		}
		for _, l := range latest {
			if l.Range == diags[i].Range && l.Message == diags[i].Message {
				diags[i].Data = l.Data
				break
			}
		}
	}

	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	ind := detectFileIndent(filePath, src)

	var actions []CodeAction
	seenTitles := make(map[string]bool)
	addAction := func(act CodeAction) {
		if act.Title == "" || seenTitles[act.Title] {
			return
		}
		seenTitles[act.Title] = true
		actions = append(actions, act)
	}

	for _, d := range diags {
		s.collectDiagnosticQuickFixes(filePath, uri, src, ind, prog, mod, d, addAction)
	}

	if mod != nil && mod.File != nil {
		s.collectProactiveGenerators(filePath, uri, src, ind, prog, mod, params.Range, addAction)
		s.collectRefactorActions(filePath, uri, src, ind, prog, mod, params.Range, addAction)
		s.expandCaseActions(uri, src, ind, prog, mod, LSPToOffset(src, params.Range.Start), addAction)
	}

	for _, tAct := range s.computeTestCodeActions(filePath, src, params.Range) {
		addAction(tAct)
	}

	for i := range actions {
		if actions[i].Title == wrapBodyInBlockTitle {
			continue // the formatter would collapse the new single-expression block straight back
		}
		formatActionEdits(uri, src, ind, &actions[i])
	}

	if len(params.Context.Only) > 0 {
		var filtered []CodeAction
		for _, a := range actions {
			if matchesCodeActionOnly(a.Kind, params.Context.Only) {
				filtered = append(filtered, a)
			}
		}
		return filtered
	}
	return actions
}

func matchesCodeActionOnly(kind string, only []string) bool {
	if kind == "" {
		return true
	}
	for _, o := range only {
		if o == "" || kind == o || strings.HasPrefix(kind, o+".") || strings.HasPrefix(o, kind+".") {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// Diagnostic Quick Fixes
// -----------------------------------------------------------------------------

func (s *Server) collectDiagnosticQuickFixes(
	filePath, uri, src, ind string,
	prog *sema.CheckedProgram,
	mod *sema.Module,
	d Diagnostic,
	addAction func(CodeAction),
) {
	msg := d.Message

	// 0. Fixes from the compiler's structured diagnostic data
	s.collectDataQuickFixes(filePath, uri, src, d, addAction)

	// 0b. `fn`/`cl` literals and missing `@derive` interfaces
	s.collectFnClQuickFixes(uri, src, d, addAction)
	s.collectDeriveQuickFixes(filePath, src, prog, d, addAction)

	// 1. Missing `&` on mutable borrow parameter
	if strings.Contains(msg, "borrows mutably") && strings.Contains(msg, "requires `&` at the call site") {
		insertAt := Range{Start: d.Range.Start, End: d.Range.Start}
		addAction(CodeAction{
			Title:       "Prefix argument with '&' (mutable borrow)",
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			IsPreferred: true,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					uri: {{Range: insertAt, NewText: "&"}},
				},
			},
		})
	}

	// 2. Unexpected `&` on read-only parameter
	if strings.Contains(msg, "unexpected `&` on read-only parameter") {
		startOff := LSPToOffset(src, d.Range.Start)
		if startOff < len(src) && src[startOff] == '&' {
			ampRange := Range{Start: d.Range.Start, End: OffsetToLSP(src, startOff+1)}
			addAction(CodeAction{
				Title:       "Remove unnecessary '&'",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: ampRange, NewText: ""}},
					},
				},
			})
		}
	}

	// 3. Missing `own` on consuming parameter
	if strings.Contains(msg, "to a consuming `own` parameter") {
		insertStart := Range{Start: d.Range.Start, End: d.Range.Start}
		insertEnd := Range{Start: d.Range.End, End: d.Range.End}
		addAction(CodeAction{
			Title:       "Prefix argument with 'own ' (transfer ownership)",
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			IsPreferred: true,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					uri: {{Range: insertStart, NewText: "own "}},
				},
			},
		})
		addAction(CodeAction{
			Title:       "Append '.copy()' (keep original valid)",
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					uri: {{Range: insertEnd, NewText: ".copy()"}},
				},
			},
		})
	}

	// 4–5. Optional (`?`) and result (`!`) naming invariants: rename the declaration and every use.
	suffixFix := func(name, newName, title string) {
		editRng, ok := findWordRangeInSpan(src, d.Range, name)
		if !ok {
			return
		}
		we, err := s.renameSymbol(filePath, src, editRng.Start, newName, false)
		if err != nil || we == nil || len(we.Changes) == 0 {
			we = &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{Range: editRng, NewText: newName}}}}
		}
		addAction(CodeAction{
			Title:       title,
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			IsPreferred: true,
			Edit:        we,
		})
	}
	if strings.Contains(msg, "has optional type") && strings.Contains(msg, "must end with `?`") {
		if name := extractQuotedWord(msg); name != "" {
			suffixFix(name, name+"?", fmt.Sprintf("Rename '%s' to '%s?'", name, name))
		}
	}
	if strings.Contains(msg, "ends with `?` but has non-optional type") {
		if optName := extractQuotedWord(msg); strings.HasSuffix(optName, "?") {
			plain := strings.TrimSuffix(optName, "?")
			suffixFix(optName, plain, fmt.Sprintf("Rename '%s' to '%s' (remove '?')", optName, plain))
		}
	}
	if strings.Contains(msg, "has result type") && strings.Contains(msg, "must end with `!`") {
		if name := extractQuotedWord(msg); name != "" {
			suffixFix(name, name+"!", fmt.Sprintf("Rename '%s' to '%s!'", name, name))
		}
	}
	if strings.Contains(msg, "ends with `!` but has non-result type") {
		if resName := extractQuotedWord(msg); strings.HasSuffix(resName, "!") {
			plain := strings.TrimSuffix(resName, "!")
			suffixFix(resName, plain, fmt.Sprintf("Rename '%s' to '%s' (remove '!')", resName, plain))
		}
	}

	// 6. `!` instead of `not`
	if strings.Contains(msg, "logical negation in Lowlight uses 'not'") {
		addAction(CodeAction{
			Title:       "Replace '!' with 'not '",
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			IsPreferred: true,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					uri: {{Range: d.Range, NewText: "not "}},
				},
			},
		})
	}

	// 7. Missing trailing comma in multiline `[...]` / `(...)`
	if strings.Contains(msg, "missing trailing comma in multiline") {
		insOff := LSPToOffset(src, d.Range.Start)
		if insOff < len(src) && src[insOff] != '\n' && src[insOff] != '\r' && src[insOff] != ' ' && src[insOff] != '\t' && src[insOff] != ']' && src[insOff] != ')' {
			insOff = LSPToOffset(src, d.Range.End)
		}
		insPos := OffsetToLSP(src, insOff)
		addAction(CodeAction{
			Title:       "Insert trailing comma ','",
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			IsPreferred: true,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					uri: {{Range: Range{Start: insPos, End: insPos}, NewText: ","}},
				},
			},
		})
	}

	// 8. Optional member access missing `?` before `.`
	if strings.Contains(msg, "on optional value of type") && strings.Contains(msg, "without `?`") {
		sOff := LSPToOffset(src, d.Range.Start)
		eOff := LSPToOffset(src, d.Range.End)
		if eOff > len(src) {
			eOff = len(src)
		}
		dotIdx := strings.LastIndexByte(src[sOff:eOff], '.')
		if dotIdx >= 0 {
			dotPos := OffsetToLSP(src, sOff+dotIdx)
			addAction(CodeAction{
				Title:       "Use '?.' optional chaining",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: Range{Start: dotPos, End: dotPos}, NewText: "?"}},
					},
				},
			})
		}
	}

	// 9. Match exhaustiveness quick fixes (`enum`, `bool`, missing `default:`)
	s.collectMatchQuickFixes(uri, src, ind, prog, mod, d, addAction)

	// 10. Top-level `let` -> `bind`
	if strings.Contains(msg, "mutable global `let` variables are not allowed at top level") {
		if letRng, ok := findWordRangeInSpan(src, d.Range, "let"); ok {
			addAction(CodeAction{
				Title:       "Convert top-level 'let' to 'bind'",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: letRng, NewText: "bind"}},
					},
				},
			})
		}
	}

	// 11. Wrap unsafe operation in `unsafe`
	if strings.Contains(msg, "requires `unsafe`") {
		sOff := LSPToOffset(src, d.Range.Start)
		eOff := LSPToOffset(src, d.Range.End)
		if eOff > sOff && eOff <= len(src) {
			exprText := strings.TrimSpace(src[sOff:eOff])
			wrapped := "unsafe " + exprText
			if strings.Contains(exprText, "\n") || strings.ContainsAny(exprText, " =+-*/") {
				wrapped = "unsafe (" + exprText + ")"
			}
			addAction(CodeAction{
				Title:       "Wrap in 'unsafe'",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: d.Range, NewText: wrapped}},
					},
				},
			})
		}
	}

	// 12. Discarded `@must_use` return value
	if strings.Contains(msg, "marked @must_use") {
		sOff := LSPToOffset(src, d.Range.Start)
		if sOff >= 0 && sOff <= len(src) {
			insRng := Range{Start: d.Range.Start, End: d.Range.Start}
			resName := "res"
			addAction(CodeAction{
				Title:       fmt.Sprintf("Bind return value ('let %s = ...')", resName),
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: insRng, NewText: fmt.Sprintf("let %s = ", resName)}},
					},
				},
			})
			addAction(CodeAction{
				Title:       "Explicitly ignore return value ('let _ = ...')",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: insRng, NewText: "let _ = "}},
					},
				},
			})
		}
	}

	// 13. Missing required struct literal fields
	if strings.Contains(msg, "missing required field") && mod != nil && mod.File != nil {
		if act, ok := s.buildPopulateStructLitAction(uri, src, ind, prog, mod, d.Range, &d); ok {
			addAction(act)
		}
	}

	// 14. Auto-import missing symbol / type
	if strings.Contains(msg, "undeclared function") ||
		strings.Contains(msg, "undeclared identifier") ||
		strings.Contains(msg, "unknown identifier") ||
		strings.Contains(msg, "unknown type") {
		s.collectAutoImportQuickFixes(filePath, uri, src, prog, mod, d, addAction)
	}

	// 15. Implement missing interface methods from diagnostic
	if strings.Contains(msg, "does not implement") && mod != nil && mod.File != nil {
		s.collectInterfaceQuickFixFromDiag(uri, src, prog, mod, d, addAction)
	}
}

func (s *Server) collectMatchQuickFixes(
	uri, src, ind string,
	prog *sema.CheckedProgram,
	mod *sema.Module,
	d Diagnostic,
	addAction func(CodeAction),
) {
	msg := d.Message

	if strings.Contains(msg, "non-exhaustive `match` on enum") && strings.Contains(msg, "missing variant(s):") {
		missingIdx := strings.Index(msg, "missing variant(s):")
		rawMissing := strings.TrimSpace(msg[missingIdx+len("missing variant(s):"):])
		if nl := strings.IndexByte(rawMissing, '\n'); nl >= 0 {
			rawMissing = strings.TrimSpace(rawMissing[:nl])
		}
		me := findMatchExprAt(prog, LSPToOffset(src, d.Range.Start))
		useBare := me == nil || me.ResultChain || !matchUsesQualifiedVariants(me)
		var whenArms []string
		for _, vn := range strings.Split(rawMissing, ", ") {
			if vn = strings.TrimSpace(vn); vn == "" {
				continue
			}
			pat := formatVariantPatternForMatch(prog, mod, vn)
			if useBare {
				// Result chains and bare-style enum matches name variants without the type prefix.
				if dot := strings.Index(pat, "."); dot >= 0 && !strings.Contains(pat[:dot], "(") {
					pat = pat[dot+1:]
				}
			}
			whenArms = append(whenArms, pat)
		}
		if len(whenArms) > 0 {
			s.addMissingArmActions(uri, src, ind, me, d, whenArms, addAction)
		}
	}

	if strings.Contains(msg, "non-exhaustive `match` on `bool`") {
		indent := getLineIndent(src, d.Range.Start.Line)
		armIndent := indent + ind
		insertPos, hasExistingDefault := findMatchInsertionPoint(src, fullMatchRange(prog, src, d.Range))
		if !hasExistingDefault {
			addAction(CodeAction{
				Title:       "Generate missing boolean match arms",
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: Range{Start: insertPos, End: insertPos}, NewText: "\n" + armIndent + "when true: panic(\"todo\")\n" + armIndent + "default: panic(\"todo\")"}},
					},
				},
			})
		}
	}

	if strings.Contains(msg, "must end with an open catch-all `default:` arm") {
		indent := getLineIndent(src, d.Range.Start.Line)
		armIndent := indent + ind
		insertPos, _ := findMatchInsertionPoint(src, fullMatchRange(prog, src, d.Range))
		addAction(CodeAction{
			Title:       "Add catch-all 'default:' arm",
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			IsPreferred: true,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					uri: {{Range: Range{Start: insertPos, End: insertPos}, NewText: "\n" + armIndent + "default: panic(\"todo\")"}},
				},
			},
		})
	}
}

// fullMatchRange widens a diagnostic range on a `match` header (`match(x):`) to the whole match.
func fullMatchRange(prog *sema.CheckedProgram, src string, r Range) Range {
	if me := findMatchExprAt(prog, LSPToOffset(src, r.Start)); me != nil {
		return Range{Start: OffsetToLSP(src, me.Span.Start.Offset), End: OffsetToLSP(src, me.Span.End.Offset)}
	}
	return r
}

// matchUsesQualifiedVariants reports whether an enum `match` explicitly writes any qualified
// variant pattern (`when Status.active:`). Empty matches and bare matches default to bare patterns.
func matchUsesQualifiedVariants(me *ast.MatchExpr) bool {
	if me == nil {
		return false
	}
	for _, arm := range me.Arms {
		if arm.Synthetic {
			continue
		}
		for _, pat := range arm.AllPatterns() {
			switch p := pat.(type) {
			case *ast.ExprPattern:
				if _, isMem := p.Expr.(*ast.MemberExpr); isMem {
					return true
				}
			case *ast.VariantPattern:
				if !p.Contextual {
					return true
				}
			}
		}
	}
	return false
}

// findMatchExprAt returns the innermost checked `match` or result chain containing offset.
func findMatchExprAt(prog *sema.CheckedProgram, offset int) *ast.MatchExpr {
	if prog == nil {
		return nil
	}
	var best *ast.MatchExpr
	consider := func(bm *sema.BodyMaps) {
		if bm == nil {
			return
		}
		for e := range bm.ExprInfos {
			me, ok := e.(*ast.MatchExpr)
			if !ok || !spanContainsOffset(me.Span, offset) {
				continue
			}
			if best == nil || me.Span.Start.Offset >= best.Span.Start.Offset {
				best = me
			}
		}
	}
	for _, fi := range prog.Funcs {
		consider(fi.BodyMaps)
	}
	for _, li := range prog.Lambdas {
		consider(li.BodyMaps)
	}
	return best
}

// addMissingArmActions offers to complete a non-exhaustive `match` or result chain with `when`
// arms for `missing` patterns. When the last arm is an exhaustive `default Pattern:`, it becomes
// a `when` arm so the new last pattern can take `default`. An open `default:` arm stays last.
func (s *Server) addMissingArmActions(uri, src, ind string, me *ast.MatchExpr, d Diagnostic, missing []string, addAction func(CodeAction)) {
	var last *ast.MatchArm
	if me != nil {
		for i := range me.Arms {
			if !me.Arms[i].Synthetic {
				last = &me.Arms[i]
			}
		}
	}
	armIndent := getLineIndent(src, d.Range.Start.Line) + ind
	if last != nil {
		armIndent = getLineIndent(src, OffsetToLSP(src, last.Span.Start.Offset).Line)
	}
	arm := func(kw, pat string) string { return armIndent + kw + pat + ": panic(\"todo\")" }

	// An open catch-all `default:` keeps its place; the new arms go before it.
	if last != nil && last.IsDefault && len(last.Cases) == 0 {
		lineStart := OffsetToLSP(src, last.Span.Start.Offset)
		lineStart.Character = 0
		var sb strings.Builder
		for _, pat := range missing {
			sb.WriteString(arm("when ", pat) + "\n")
		}
		addAction(CodeAction{
			Title: "Generate missing match arms", Kind: "quickfix", Diagnostics: []Diagnostic{d}, IsPreferred: true,
			Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{Range: Range{Start: lineStart, End: lineStart}, NewText: sb.String()}}}},
		})
		return
	}

	// Replace the existing last arm (demoting an exhaustive `default Pattern:` to `when`) and append.
	var rng Range
	prefix := ""
	if last != nil {
		rng = Range{Start: OffsetToLSP(src, last.Span.Start.Offset), End: OffsetToLSP(src, last.Span.End.Offset)}
		prefix = src[last.Span.Start.Offset:last.Span.End.Offset]
		if last.IsDefault {
			prefix = "when" + strings.TrimPrefix(prefix, "default")
		}
	} else {
		insertPos, _ := findMatchInsertionPoint(src, d.Range)
		if me != nil {
			insertPos = OffsetToLSP(src, me.Span.End.Offset)
		}
		rng = Range{Start: insertPos, End: insertPos}
	}
	build := func(fallback bool) string {
		var parts []string
		if prefix != "" {
			parts = append(parts, prefix)
		}
		for i, pat := range missing {
			if i == len(missing)-1 && !fallback {
				parts = append(parts, arm("default ", pat))
			} else {
				parts = append(parts, arm("when ", pat))
			}
		}
		if fallback {
			parts = append(parts, armIndent+"default: panic(\"todo\")")
		}
		text := strings.Join(parts, "\n")
		if prefix == "" {
			return "\n" + text // appended after the match header
		}
		return text // replaces the old last arm in place, which keeps its line's indentation
	}
	exhaustive := build(false)
	fallback := build(true)
	addAction(CodeAction{
		Title: "Generate missing match arms (exhaustive 'default Pattern')", Kind: "quickfix", Diagnostics: []Diagnostic{d}, IsPreferred: true,
		Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{Range: rng, NewText: exhaustive}}}},
	})
	addAction(CodeAction{
		Title: "Generate missing match arms (with fallback 'default:')", Kind: "quickfix", Diagnostics: []Diagnostic{d},
		Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{Range: rng, NewText: fallback}}}},
	})
}

func (s *Server) collectAutoImportQuickFixes(
	filePath, uri, src string,
	prog *sema.CheckedProgram,
	mod *sema.Module,
	d Diagnostic,
	addAction func(CodeAction),
) {
	missingSym := extractQuotedWord(d.Message)
	if missingSym == "" {
		sOff := LSPToOffset(src, d.Range.Start)
		if tok, ok := s.findTokenAtOffset(filePath, src, sOff); ok && (tok.Kind == token.Ident || tok.Kind == token.OptIdent) {
			missingSym = tok.Text
		}
	}
	if missingSym == "" || strings.HasPrefix(missingSym, "_") {
		return
	}

	// Collect candidate packages from `std/<pkg>/`, local subpackages, workspace members, and manifest dependencies.
	type candPkg struct {
		importStr string
		dirPath   string
		alias     string
		isStd     bool
	}
	var candidates []candPkg
	seenDirs := make(map[string]bool)

	alreadyImportedDir := make(map[string]bool)
	curDir := filepath.Dir(absClean(filePath))
	alreadyImportedDir[curDir] = true
	if mod != nil {
		for _, imp := range mod.Imports {
			if imp != nil {
				alreadyImportedDir[filepath.Dir(absClean(imp.Path))] = true
			}
		}
		for _, imp := range mod.Aliased {
			if imp != nil {
				alreadyImportedDir[filepath.Dir(absClean(imp.Path))] = true
			}
		}
	}

	dirHasPkgLL := func(dPath string) bool {
		entries, err := os.ReadDir(dPath)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".ll") && !manifest.IsTestFile(e.Name()) {
				return true
			}
		}
		return false
	}

	addCandidate := func(importStr, dirPath, alias string, isStd bool) {
		cleanD := absClean(dirPath)
		if alreadyImportedDir[cleanD] || seenDirs[cleanD] || !dirHasPkgLL(cleanD) {
			return
		}
		seenDirs[cleanD] = true
		candidates = append(candidates, candPkg{
			importStr: importStr,
			dirPath:   cleanD,
			alias:     manifest.NormalizeQualifier(alias),
			isStd:     isStd,
		})
	}

	// 1. Project manifest subpackages, workspace members, and resolved dependencies
	if prog != nil && prog.ManifestCtx != nil {
		ctx := prog.ManifestCtx
		if ctx.ActiveManifest != nil && ctx.ActiveManifest.HasPackage {
			pkgRoot := ctx.ActiveManifest.Dir
			pkgName := ctx.ActiveManifest.Package.Name
			addCandidate(pkgName, pkgRoot, pkgName, false)
			if entries, err := os.ReadDir(pkgRoot); err == nil {
				for _, e := range entries {
					if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "vendor" || e.Name() == "tests" {
						continue
					}
					sub := filepath.Join(pkgRoot, e.Name())
					addCandidate(pkgName+"/"+e.Name(), sub, e.Name(), false)
				}
			}
		}
		if prog.ResolvedDeps != nil {
			for depName, rp := range prog.ResolvedDeps.Packages {
				if rp != nil && rp.Dir != "" {
					addCandidate(depName, rp.Dir, depName, false)
				}
			}
		}
	} else {
		// Standalone script: check sibling subdirectories as relative `./<pkg>` packages
		if entries, err := os.ReadDir(curDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "vendor" || e.Name() == "editors" || e.Name() == "node_modules" {
					continue
				}
				sub := filepath.Join(curDir, e.Name())
				addCandidate("./"+e.Name(), sub, e.Name(), false)
			}
		}
	}

	// 2. `std/<pkg>/` package directories (excluding `core` which is implicitly preloaded)
	stdDir := ""
	if prog != nil && prog.StdDir != "" {
		stdDir = prog.StdDir
	} else {
		stdDir = manifest.FindStdDir(s.projectRootFor(filePath), curDir)
	}
	if stdDir != "" {
		if entries, err := os.ReadDir(stdDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "core" {
					continue
				}
				sub := filepath.Join(stdDir, e.Name())
				addCandidate("std/"+e.Name(), sub, e.Name(), true)
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].isStd != candidates[j].isStd {
			return candidates[i].isStd
		}
		return candidates[i].importStr < candidates[j].importStr
	})

	insertPos := importInsertionPosition(src, mod)
	for _, c := range candidates {
		// Case 1: missingSym is a package qualifier itself (e.g., `math` in `math.sin`)
		if c.alias == missingSym {
			line := fmt.Sprintf("import %q\n", c.importStr)
			if insertPos.Line == 0 && len(src) > 0 && !strings.HasPrefix(src, "import ") {
				line += "\n"
			}
			addAction(CodeAction{
				Title:       fmt.Sprintf("Import %q", c.importStr),
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d},
				IsPreferred: true,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: Range{Start: insertPos, End: insertPos}, NewText: line}},
					},
				},
			})
			continue
		}

		// Case 2: missingSym is an exported symbol or type declared in any non-test `.ll` file of `c.dirPath`
		foundInPkg := false
		if entries, err := os.ReadDir(c.dirPath); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".ll") || manifest.IsTestFile(e.Name()) {
					continue
				}
				fPath := filepath.Join(c.dirPath, e.Name())
				candSrc := s.readSourceForPath(fPath)
				if candSrc == "" || !strings.Contains(candSrc, missingSym) {
					continue
				}
				rep := diag.NewReporter()
				af, _ := parser.ParseFileWithComments(fPath, candSrc, rep)
				if af != nil && fileExportsSymbol(af, missingSym) {
					foundInPkg = true
					break
				}
			}
		}
		if !foundInPkg {
			continue
		}
		line := fmt.Sprintf("import %q\n", c.importStr)
		if insertPos.Line == 0 && len(src) > 0 && !strings.HasPrefix(src, "import ") {
			line += "\n"
		}
		edits := []TextEdit{
			{Range: Range{Start: insertPos, End: insertPos}, NewText: line},
			{Range: Range{Start: d.Range.Start, End: d.Range.Start}, NewText: c.alias + "."},
		}
		addAction(CodeAction{
			Title:       fmt.Sprintf("Import %q (qualify as '%s.%s')", c.importStr, c.alias, missingSym),
			Kind:        "quickfix",
			Diagnostics: []Diagnostic{d},
			IsPreferred: true,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					uri: edits,
				},
			},
		})
	}
}

func importInsertionPosition(src string, mod *sema.Module) Position {
	lastImpLine := -1
	if mod != nil && mod.File != nil {
		for _, d := range mod.File.Decls {
			if imp, ok := d.(*ast.ImportDecl); ok && imp.Span.End.Line > 0 {
				if imp.Span.End.Line > lastImpLine {
					lastImpLine = imp.Span.End.Line
				}
			}
		}
	}
	if lastImpLine > 0 {
		lines := strings.Split(src, "\n")
		if lastImpLine <= len(lines) {
			return Position{Line: lastImpLine, Character: 0}
		}
	}
	return Position{Line: 0, Character: 0}
}

func fileExportsSymbol(af *ast.File, name string) bool {
	if !sema.IsExported(name) {
		return false
	}
	for _, d := range af.Decls {
		switch v := d.(type) {
		case *ast.StructDecl:
			if v.Name == name {
				return true
			}
		case *ast.EnumDecl:
			if v.Name == name {
				return true
			}
		case *ast.InterfaceDecl:
			if v.Name == name {
				return true
			}
		case *ast.DistinctTypeDecl:
			if v.Name == name {
				return true
			}
		case *ast.ExternFnDecl:
			n := v.Name
			if v.Alias != "" {
				n = v.Alias
			}
			if n == name {
				return true
			}
		case *ast.VarDecl:
			if v.Name == name {
				for _, attr := range v.Attributes {
					if attr.Name == "test" {
						return false
					}
				}
				return true
			}
		}
	}
	return false
}

func (s *Server) collectInterfaceQuickFixFromDiag(
	uri, src string,
	prog *sema.CheckedProgram,
	mod *sema.Module,
	d Diagnostic,
	addAction func(CodeAction),
) {
	msg := d.Message
	var typeName, ifcName string

	// Patterns produced by sema:
	// - "type <T> does not implement interface <I>"
	// - "type <T> does not implement `Copyable` (`.copy()`)"
	// - "type <T> does not implement `Printable` (`.to_string()`)"
	// - "... argument of type <T> does not implement `Printable`"
	// - "... not defined on <T> (type does not implement Comparable)"
	if idx := strings.Index(msg, "type "); idx >= 0 {
		rest := msg[idx+len("type "):]
		if sp := strings.IndexByte(rest, ' '); sp > 0 {
			typeName = strings.Trim(rest[:sp], "`'\"")
		}
	}
	if idx := strings.Index(msg, "does not implement interface "); idx >= 0 {
		rest := strings.TrimSpace(msg[idx+len("does not implement interface "):])
		ifcName = strings.Fields(rest)[0]
		ifcName = strings.TrimRight(ifcName, ".,:;)`'\"")
	} else if idx := strings.Index(msg, "does not implement "); idx >= 0 {
		rest := strings.TrimSpace(msg[idx+len("does not implement "):])
		if len(rest) > 0 {
			f := strings.Fields(rest)[0]
			ifcName = strings.Trim(f, "`'\".,:()")
		}
	}
	if typeName == "" || ifcName == "" {
		return
	}
	// Strip generic type arguments like `Box[int]` -> `Box`
	if br := strings.IndexByte(typeName, '['); br > 0 {
		typeName = typeName[:br]
	}

	ifc := lookupInterfaceType(prog, mod, ifcName)
	if ifc == nil {
		return
	}
	if act, ok := buildImplementInterfaceAction(uri, src, mod, typeName, ifc, &d); ok {
		addAction(act)
	}
}

// -----------------------------------------------------------------------------
// Proactive Code Generators (Interfaces, Constructors, Struct Fields, Docs)
// -----------------------------------------------------------------------------

func (s *Server) collectProactiveGenerators(
	filePath, uri, src, ind string,
	prog *sema.CheckedProgram,
	mod *sema.Module,
	cursor Range,
	addAction func(CodeAction),
) {
	cursorOff := LSPToOffset(src, cursor.Start)

	// 1. Struct literal field completion under cursor
	if act, ok := s.buildPopulateStructLitAction(uri, src, ind, prog, mod, cursor, nil); ok {
		addAction(act)
	}

	// 2. Declaration-level generators (`define ... struct/enum/distinct/interface`, `bind ...`)
	for _, d := range mod.File.Decls {
		dRng := SpanToLSP(src, d.GetSpan())
		if !rangesOverlap(dRng, cursor) && cursor.Start.Line != d.GetSpan().Start.Line-1 {
			continue
		}

		switch decl := d.(type) {
		case *ast.StructDecl:
			if decl.Doc == "" {
				addAction(buildDocCommentAction(uri, src, decl.Span, decl.Attributes, decl.Name, nil, ""))
			}
			if !hasTypeMethodDecl(mod.File, decl.Name, "new") {
				if act, ok := buildStructConstructorAction(uri, src, ind, prog, mod, decl); ok {
					addAction(act)
				}
			}
			for _, act := range deriveActions(uri, src, mod, decl, decl.Name, decl.Attributes) {
				addAction(act)
			}
			for _, ifc := range availableInterfaces(prog, mod) {
				if derivesInterface(decl.Attributes, ifc.Name) {
					continue
				}
				if act, ok := buildImplementInterfaceAction(uri, src, mod, decl.Name, ifc, nil); ok {
					addAction(act)
				}
			}

		case *ast.EnumDecl:
			if decl.Doc == "" {
				addAction(buildDocCommentAction(uri, src, decl.Span, decl.Attributes, decl.Name, nil, ""))
			}
			for _, act := range deriveActions(uri, src, mod, decl, decl.Name, decl.Attributes) {
				addAction(act)
			}
			for _, ifc := range availableInterfaces(prog, mod) {
				if derivesInterface(decl.Attributes, ifc.Name) {
					continue
				}
				if act, ok := buildImplementInterfaceAction(uri, src, mod, decl.Name, ifc, nil); ok {
					addAction(act)
				}
			}

		case *ast.DistinctTypeDecl:
			if decl.Doc == "" {
				addAction(buildDocCommentAction(uri, src, decl.Span, decl.Attributes, decl.Name, nil, ""))
			}
			for _, ifc := range availableInterfaces(prog, mod) {
				if act, ok := buildImplementInterfaceAction(uri, src, mod, decl.Name, ifc, nil); ok {
					addAction(act)
				}
			}

		case *ast.InterfaceDecl:
			if decl.Doc == "" {
				addAction(buildDocCommentAction(uri, src, decl.Span, decl.Attributes, decl.Name, nil, ""))
			}

		case *ast.MethodDecl:
			if decl.Doc == "" {
				var pNames []string
				for _, p := range decl.Params {
					pNames = append(pNames, p.Name)
				}
				retName := ""
				if decl.Ret != nil {
					retName = formatTypeExpr(decl.Ret)
				}
				addAction(buildDocCommentAction(uri, src, decl.Span, decl.Attributes, decl.RecvType+"."+decl.Name, pNames, retName))
			}

		case *ast.ExternFnDecl:
			if decl.Doc == "" {
				name := decl.Name
				if decl.Alias != "" {
					name = decl.Alias
				}
				var pNames []string
				for _, p := range decl.Params {
					pNames = append(pNames, p.Name)
				}
				retName := ""
				if decl.Ret != nil {
					retName = formatTypeExpr(decl.Ret)
				}
				addAction(buildDocCommentAction(uri, src, decl.Span, nil, name, pNames, retName))
			}

		case *ast.VarDecl:
			if decl.Doc == "" && decl.IsBind {
				if fl, ok := decl.Value.(*ast.FuncLit); ok {
					isTest := false
					for _, a := range decl.Attributes {
						if a.Name == "test" {
							isTest = true
						}
					}
					if !isTest {
						var pNames []string
						for _, p := range fl.Params {
							pNames = append(pNames, p.Name)
						}
						retName := ""
						if fl.Ret != nil {
							retName = formatTypeExpr(fl.Ret)
						}
						addAction(buildDocCommentAction(uri, src, decl.Span, decl.Attributes, decl.Name, pNames, retName))
					}
				} else {
					addAction(buildDocCommentAction(uri, src, decl.Span, decl.Attributes, decl.Name, nil, ""))
				}
			}
		}
		_ = cursorOff
		break
	}
}

func availableInterfaces(prog *sema.CheckedProgram, mod *sema.Module) []*types.Interface {
	var out []*types.Interface
	seen := make(map[string]bool)
	addIfc := func(ifc *types.Interface) {
		if ifc == nil || seen[ifc.Name] {
			return
		}
		seen[ifc.Name] = true
		out = append(out, ifc)
	}
	if mod != nil {
		for _, ty := range mod.Types {
			if ifc, ok := ty.(*types.Interface); ok {
				addIfc(ifc)
			}
		}
		for _, imp := range mod.Imports {
			if imp == nil {
				continue
			}
			for _, ty := range imp.Types {
				if ifc, ok := ty.(*types.Interface); ok {
					addIfc(ifc)
				}
			}
		}
	}
	if prog != nil {
		addIfc(prog.IfacePrintable)
		addIfc(prog.IfaceEquatable)
		addIfc(prog.IfaceHashable)
		addIfc(prog.IfaceComparable)
		addIfc(prog.IfaceCopyable)
		addIfc(prog.IfaceDrop)
	}
	return out
}

func lookupInterfaceType(prog *sema.CheckedProgram, mod *sema.Module, name string) *types.Interface {
	for _, ifc := range availableInterfaces(prog, mod) {
		if ifc.Name == name {
			return ifc
		}
	}
	if prog != nil {
		for _, m := range prog.Modules {
			if m == nil {
				continue
			}
			if ifc, ok := m.Types[name].(*types.Interface); ok {
				return ifc
			}
		}
	}
	return nil
}

func hasTypeMethodDecl(f *ast.File, typeName, methName string) bool {
	if f == nil {
		return false
	}
	for _, d := range f.Decls {
		if md, ok := d.(*ast.MethodDecl); ok && md.RecvType == typeName && md.Name == methName {
			return true
		}
	}
	return false
}

func substSelfTypeString(t types.Type, typeName string) string {
	if t == nil {
		return "void"
	}
	if t == types.TypeSelf || t.String() == "Self" {
		return typeName
	}
	switch v := types.CollView(t).(type) {
	case *types.Optional:
		return "?" + substSelfTypeString(v.Elem, typeName)
	case *types.Vector:
		return "[" + substSelfTypeString(v.Elem, typeName) + "]"
	case *types.StackArray:
		return fmt.Sprintf("[%d: %s]", v.Size, substSelfTypeString(v.Elem, typeName))
	case *types.Set:
		return "#[" + substSelfTypeString(v.Elem, typeName) + "]"
	case *types.Map:
		return fmt.Sprintf("#[%s: %s]", substSelfTypeString(v.Key, typeName), substSelfTypeString(v.Value, typeName))
	case *types.Tuple:
		var elems []string
		for _, el := range v.Elems {
			elems = append(elems, substSelfTypeString(el, typeName))
		}
		return "+[" + strings.Join(elems, ", ") + "]"
	default:
		return t.String()
	}
}

// typeDeclEndLine returns the last (1-based) line of `typeName`'s declaration or of its last
// method, whichever comes later, or 0 when the type is not declared in `mod`.
func typeDeclEndLine(mod *sema.Module, typeName string) int {
	if mod == nil || mod.File == nil {
		return 0
	}
	lastLine := 0
	for _, d := range mod.File.Decls {
		end := 0
		switch decl := d.(type) {
		case *ast.StructDecl:
			if decl.Name == typeName {
				end = decl.Span.End.Line
			}
		case *ast.EnumDecl:
			if decl.Name == typeName {
				end = decl.Span.End.Line
			}
		case *ast.DistinctTypeDecl:
			if decl.Name == typeName {
				end = decl.Span.End.Line
			}
		case *ast.MethodDecl:
			if decl.RecvType == typeName {
				end = decl.Span.End.Line
			}
		}
		if end > lastLine {
			lastLine = end
		}
	}
	return lastLine
}

// insertDeclsAfterLineEdit inserts top-level declarations after (1-based) line `line`, separated
// from their neighbors and from each other by exactly one blank line, as the formatter lays out
// top-level declarations. A `line` of 0 or past the end appends at the end of the file.
func insertDeclsAfterLineEdit(src string, line int, decls []string) TextEdit {
	lines := strings.Split(src, "\n")
	if line <= 0 || line >= len(lines) {
		return appendDeclsEdit(src, decls)
	}
	off := 0
	for i := 0; i < line; i++ {
		off += len(lines[i]) + 1
	}
	off-- // end of `line`, before its newline
	text := "\n\n" + strings.Join(decls, "\n\n")
	if after := src[off:]; !strings.HasPrefix(after, "\n\n") && strings.TrimSpace(after) != "" {
		text += "\n"
	}
	pos := OffsetToLSP(src, off)
	return TextEdit{Range: Range{Start: pos, End: pos}, NewText: text}
}

// appendDeclsEdit appends top-level declarations at the end of the file after one blank line,
// normalizing trailing whitespace so the file ends in a single newline.
func appendDeclsEdit(src string, decls []string) TextEdit {
	trimmed := strings.TrimRight(src, " \t\r\n")
	start := OffsetToLSP(src, len(trimmed))
	end := OffsetToLSP(src, len(src))
	text := strings.Join(decls, "\n\n") + "\n"
	if trimmed != "" {
		text = "\n\n" + text
	}
	return TextEdit{Range: Range{Start: start, End: end}, NewText: text}
}

func buildImplementInterfaceAction(
	uri, src string,
	mod *sema.Module,
	typeName string,
	ifc *types.Interface,
	diagPtr *Diagnostic,
) (CodeAction, bool) {
	if mod == nil || mod.File == nil || ifc == nil || typeName == "" || len(ifc.Methods) == 0 {
		return CodeAction{}, false
	}

	var stubs []string
	for _, m := range ifc.Methods {
		if hasTypeMethodDecl(mod.File, typeName, m.Name) {
			continue
		}
		var params []string
		switch m.RecvMode {
		case types.ModeMut:
			params = append(params, "&self")
		case types.ModeOwn:
			params = append(params, "own self")
		default:
			params = append(params, "self")
		}
		for _, p := range m.Params {
			pType := substSelfTypeString(p.Type, typeName)
			params = append(params, fmt.Sprintf("%s: %s%s", p.Name, p.Mode.Prefix(), pType))
		}
		retStr := ""
		if m.Ret != nil && !m.Ret.Equals(types.TypeVoid) {
			retStr = " " + substSelfTypeString(m.Ret, typeName)
		}
		body := derivedMethodBody(mod, typeName, ifc.Name, m.Name)
		if body == "" {
			body = "panic(\"todo\")"
		}
		stubs = append(stubs, formatGeneratedDecl(fmt.Sprintf("bind %s.%s = fn(%s)%s: %s", typeName, m.Name, strings.Join(params, ", "), retStr, body), "\t"))
	}

	if len(stubs) == 0 {
		return CodeAction{}, false
	}

	act := CodeAction{
		Title: fmt.Sprintf("Implement %s for %s", ifc.Name, typeName),
		Kind:  "quickfix",
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				uri: {insertDeclsAfterLineEdit(src, typeDeclEndLine(mod, typeName), stubs)},
			},
		},
	}
	if diagPtr == nil {
		act.Kind = "refactor.rewrite"
	} else {
		act.Diagnostics = []Diagnostic{*diagPtr}
		act.IsPreferred = true
	}
	return act, true
}

func buildStructConstructorAction(
	uri, src, ind string,
	prog *sema.CheckedProgram,
	mod *sema.Module,
	decl *ast.StructDecl,
) (CodeAction, bool) {
	if decl == nil {
		return CodeAction{}, false
	}
	st, _ := lookupNominalType(mod, "", decl.Name).(*types.Struct)
	var paramStrs []string
	var fieldInits []string

	for _, f := range decl.Fields {
		if strings.HasPrefix(f.Name, "_") {
			continue
		}
		tyStr := formatTypeExpr(f.Type)
		isRes := false
		if st != nil {
			if sf, _ := st.FieldByName(f.Name); sf != nil && sf.Type != nil && !sf.Type.IsPlain() {
				isRes = true
			}
		}
		if isRes {
			paramStrs = append(paramStrs, fmt.Sprintf("%s: own %s", f.Name, tyStr))
			fieldInits = append(fieldInits, fmt.Sprintf("%s: own %s", f.Name, f.Name))
		} else {
			paramStrs = append(paramStrs, fmt.Sprintf("%s: %s", f.Name, tyStr))
			fieldInits = append(fieldInits, fmt.Sprintf("%s: %s", f.Name, f.Name))
		}
	}

	stub := formatGeneratedDecl(fmt.Sprintf("bind %s.new = fn(%s) %s: %s[%s]", decl.Name, strings.Join(paramStrs, ", "), decl.Name, decl.Name, strings.Join(fieldInits, ", ")), ind)
	_ = prog

	return CodeAction{
		Title: fmt.Sprintf("Generate constructor '%s.new'", decl.Name),
		Kind:  "refactor.rewrite",
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				uri: {insertDeclsAfterLineEdit(src, typeDeclEndLine(mod, decl.Name), []string{stub})},
			},
		},
	}, true
}

func (s *Server) buildPopulateStructLitAction(
	uri, src, ind string,
	prog *sema.CheckedProgram,
	mod *sema.Module,
	targetRng Range,
	diagPtr *Diagnostic,
) (CodeAction, bool) {
	if mod == nil || mod.File == nil {
		return CodeAction{}, false
	}
	off := LSPToOffset(src, targetRng.Start)

	var foundLit *ast.StructLit
	var foundStruct *types.Struct
	inspectExpr := func(bm *sema.BodyMaps, e ast.Expr) {
		var walk func(ex ast.Expr)
		walk = func(ex ast.Expr) {
			if ex == nil || !spanContainsOffset(ex.GetSpan(), off) {
				return
			}
			switch v := ex.(type) {
			case *ast.BlockExpr:
				for _, st := range v.Stmts {
					switch s := st.(type) {
					case *ast.VarStmt:
						walk(s.Value)
					case *ast.AssignStmt:
						walk(s.Target)
						walk(s.Value)
					case *ast.UnwindStmt:
						walk(s.Value)
					case *ast.ExprStmt:
						walk(s.Expr)
					}
				}
				walk(v.Tail)
			case *ast.StmtBodyExpr:
				switch s := v.Stmt.(type) {
				case *ast.AssignStmt:
					walk(s.Value)
				case *ast.UnwindStmt:
					walk(s.Value)
				case *ast.ExprStmt:
					walk(s.Expr)
				}
			case *ast.IfExpr:
				walk(v.Cond)
				walk(v.Then)
				walk(v.Else)
			case *ast.ForExpr:
				walk(v.CondOrIter)
				walk(v.RangeEnd)
				walk(v.Body)
				walk(v.Else)
			case *ast.WhileExpr:
				walk(v.CondOrIter)
				walk(v.RangeEnd)
				walk(v.Body)
				walk(v.Else)
			case *ast.MatchExpr:
				for _, arm := range v.Arms {
					if arm.Synthetic {
						continue
					}
					walk(arm.Body)
				}
			case *ast.FuncLit:
				lbm := bm
				for _, li := range prog.Lambdas {
					if li != nil && li.Lit == v {
						lbm = li.BodyMaps
						break
					}
				}
				_ = lbm
				walk(v.Body)
			case *ast.CallExpr:
				for _, a := range v.Args {
					walk(a)
				}
			case *ast.StructLit:
				for _, f := range v.Fields {
					walk(f.Value)
				}
				if foundLit == nil {
					foundLit = v
					if inf := exprInfoFromBM(prog, bm, v); inf != nil && inf.StructType != nil {
						foundStruct = inf.StructType
					} else if st, ok := lookupNominalType(mod, v.Module, v.TypeName).(*types.Struct); ok {
						foundStruct = st
					}
				}
			case *ast.BinaryExpr:
				walk(v.Left)
				walk(v.Right)
			case *ast.UnaryExpr:
				walk(v.Operand)
			case *ast.UnsafeExpr:
				walk(v.Expr)
			}
		}
		walk(e)
	}

	for _, d := range mod.File.Decls {
		if !spanContainsOffset(d.GetSpan(), off) {
			continue
		}
		switch decl := d.(type) {
		case *ast.MethodDecl:
			var bm *sema.BodyMaps
			if fi := findFuncInstanceForDecl(prog, mod, decl.Span); fi != nil {
				bm = fi.BodyMaps
			}
			inspectExpr(bm, decl.Body)
		case *ast.VarDecl:
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				var bm *sema.BodyMaps
				if fi := findFuncInstanceForDecl(prog, mod, decl.Span); fi != nil {
					bm = fi.BodyMaps
				}
				inspectExpr(bm, fl.Body)
			} else if prog != nil {
				inspectExpr(prog.TopBodyMaps, decl.Value)
			}
		}
	}

	if foundLit == nil || foundStruct == nil {
		return CodeAction{}, false
	}

	present := make(map[string]bool, len(foundLit.Fields))
	for _, f := range foundLit.Fields {
		present[f.Name] = true
	}
	samePkg := prog == nil || prog.SamePackageDir(mod, foundStruct.PkgDir, "") || prog.SamePackage(mod, prog.StructMods[foundStruct])

	var missingFields []string
	for _, sf := range foundStruct.Fields {
		if present[sf.Name] {
			continue
		}
		if !samePkg && !sema.IsExported(sf.Name) {
			continue
		}
		if sf.HasDefault && diagPtr != nil {
			continue
		}
		missingFields = append(missingFields, sf.Name)
	}
	if len(missingFields) == 0 {
		return CodeAction{}, false
	}

	litStart := foundLit.Span.Start.Offset
	litEnd := foundLit.Span.End.Offset
	if litStart < 0 || litEnd <= litStart || litEnd > len(src) || src[litEnd-1] != ']' {
		return CodeAction{}, false
	}
	litText := src[litStart:litEnd]
	isMultiline := strings.ContainsRune(litText, '\n')
	closePos := OffsetToLSP(src, litEnd-1)

	var newText string
	var insPos Position
	if isMultiline {
		baseIndent := getLineIndent(src, foundLit.Span.Start.Line-1)
		fieldIndent := baseIndent + ind
		var sb strings.Builder
		for _, fn := range missingFields {
			sb.WriteString(fieldIndent)
			sb.WriteString(fn)
			sb.WriteString(": panic(\"todo\"),\n")
		}
		// Insert at start of closing `]` line cleanly
		insPos = Position{Line: closePos.Line, Character: 0}
		// Ensure previous field had trailing comma if needed
		beforeClose := strings.TrimRight(src[litStart:litEnd-1], " \t\r\n")
		if len(foundLit.Fields) > 0 && !strings.HasSuffix(beforeClose, ",") {
			lastEndOff := litStart + len(beforeClose)
			commaPos := OffsetToLSP(src, lastEndOff)
			edits := []TextEdit{
				{Range: Range{Start: commaPos, End: commaPos}, NewText: ","},
				{Range: Range{Start: insPos, End: insPos}, NewText: sb.String()},
			}
			act := CodeAction{
				Title: fmt.Sprintf("Populate missing fields for '%s'", foundLit.TypeName),
				Kind:  "quickfix",
				Edit:  &WorkspaceEdit{Changes: map[string][]TextEdit{uri: edits}},
			}
			if diagPtr != nil {
				act.Diagnostics = []Diagnostic{*diagPtr}
				act.IsPreferred = true
			} else {
				act.Kind = "refactor.rewrite"
			}
			return act, true
		}
		newText = sb.String()
	} else {
		var parts []string
		for _, fn := range missingFields {
			parts = append(parts, fn+": panic(\"todo\")")
		}
		insPos = closePos
		inner := strings.TrimSpace(src[litStart+strings.IndexByte(litText, '[')+1 : litEnd-1])
		if inner == "" {
			newText = strings.Join(parts, ", ")
		} else if strings.HasSuffix(inner, ",") {
			newText = " " + strings.Join(parts, ", ")
		} else {
			newText = ", " + strings.Join(parts, ", ")
		}
	}

	act := CodeAction{
		Title: fmt.Sprintf("Populate missing fields for '%s'", foundLit.TypeName),
		Kind:  "quickfix",
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				uri: {{Range: Range{Start: insPos, End: insPos}, NewText: newText}},
			},
		},
	}
	if diagPtr != nil {
		act.Diagnostics = []Diagnostic{*diagPtr}
		act.IsPreferred = true
	} else {
		act.Kind = "refactor.rewrite"
	}
	return act, true
}

func buildDocCommentAction(
	uri, src string,
	declSpan diag.Span,
	attrs []ast.Attribute,
	titleName string,
	paramNames []string,
	retType string,
) CodeAction {
	insertLine := declSpan.Start.Line - 1
	for _, a := range attrs {
		if a.Span.Start.Line > 0 && a.Span.Start.Line-1 < insertLine {
			insertLine = a.Span.Start.Line - 1
		}
	}
	if insertLine < 0 {
		insertLine = 0
	}
	indent := getLineIndent(src, insertLine)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s// %s TODO: summarize behavior.\n", indent, titleName))
	if len(paramNames) > 0 {
		sb.WriteString(indent + "//\n")
		for _, pn := range paramNames {
			sb.WriteString(fmt.Sprintf("%s// - `%s`: TODO.\n", indent, pn))
		}
	}
	if strings.HasPrefix(retType, "Result[") {
		sb.WriteString(indent + "//\n")
		sb.WriteString(indent + "// # Errors\n")
		sb.WriteString(indent + "// Returns [`Result.err`] if TODO.\n")
	}

	pos := Position{Line: insertLine, Character: 0}
	return CodeAction{
		Title: fmt.Sprintf("Generate doc comment for '%s'", titleName),
		Kind:  "refactor.rewrite",
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				uri: {{Range: Range{Start: pos, End: pos}, NewText: sb.String()}},
			},
		},
	}
}

// -----------------------------------------------------------------------------
// Refactorings (Extract var/fn, Toggle block braces, Flip if/else, Add type)
// -----------------------------------------------------------------------------

func (s *Server) collectRefactorActions(
	filePath, uri, src, ind string,
	prog *sema.CheckedProgram,
	mod *sema.Module,
	selRange Range,
	addAction func(CodeAction),
) {
	selStart := LSPToOffset(src, selRange.Start)
	selEnd := LSPToOffset(src, selRange.End)
	if selEnd < selStart {
		selStart, selEnd = selEnd, selStart
	}

	// Trim leading/trailing whitespace inside explicit selection
	trimmedStart, trimmedEnd := selStart, selEnd
	for trimmedStart < trimmedEnd && (src[trimmedStart] == ' ' || src[trimmedStart] == '\t' || src[trimmedStart] == '\n' || src[trimmedStart] == '\r') {
		trimmedStart++
	}
	for trimmedEnd > trimmedStart && (src[trimmedEnd-1] == ' ' || src[trimmedEnd-1] == '\t' || src[trimmedEnd-1] == '\n' || src[trimmedEnd-1] == '\r') {
		trimmedEnd--
	}

	type funcCtx struct {
		decl     ast.Decl
		body     ast.Expr
		bm       *sema.BodyMaps
		insertAt Position
		declEnd  Position
	}
	var enclosing *funcCtx
	for _, d := range mod.File.Decls {
		sp := d.GetSpan()
		if selStart < sp.Start.Offset || selEnd > sp.End.Offset {
			continue
		}
		switch decl := d.(type) {
		case *ast.VarDecl:
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				var bm *sema.BodyMaps
				if fi := findFuncInstanceForDecl(prog, mod, decl.Span); fi != nil {
					bm = fi.BodyMaps
				}
				enclosing = &funcCtx{
					decl:    d,
					body:    fl.Body,
					bm:      bm,
					declEnd: OffsetToLSP(src, decl.Span.End.Offset),
				}
			}
		case *ast.MethodDecl:
			var bm *sema.BodyMaps
			if fi := findFuncInstanceForDecl(prog, mod, decl.Span); fi != nil {
				bm = fi.BodyMaps
			}
			enclosing = &funcCtx{
				decl:    d,
				body:    decl.Body,
				bm:      bm,
				declEnd: OffsetToLSP(src, decl.Span.End.Offset),
			}
		}
		break
	}
	if enclosing == nil || enclosing.body == nil {
		return
	}

	// 1. Add explicit type annotation on untyped `let x = ...` / `bind x = ...`
	s.tryAddExplicitTypeAction(uri, src, enclosing.bm, selStart, addAction)

	// 2. Toggle single-expression `{ expr }` <-> `expr`
	s.tryToggleBlockBracesAction(uri, src, ind, enclosing.body, selStart, addAction)

	// 3. Flip `if (cond): A else: B`
	s.tryFlipIfElseAction(uri, src, enclosing.body, selStart, addAction)

	// 4. Extract expression into `let` / `bind` (when a valid sub-expression inside a statement in a BlockExpr is targeted)
	if trimmedEnd > trimmedStart {
		s.tryExtractVariableAction(uri, src, enclosing.body, trimmedStart, trimmedEnd, addAction)
		s.tryExtractFunctionAction(uri, src, ind, prog, enclosing.bm, enclosing.body, trimmedStart, trimmedEnd, enclosing.declEnd, addAction)
	}
	_ = filePath
}

func (s *Server) tryAddExplicitTypeAction(uri, src string, bm *sema.BodyMaps, off int, addAction func(CodeAction)) {
	if bm == nil {
		return
	}
	for vs, sym := range bm.VarSymbols {
		if vs == nil || sym == nil || vs.Type != nil || vs.Name == "_" {
			continue
		}
		if off < vs.Span.Start.Offset || off > vs.Span.End.Offset {
			continue
		}
		if sym.Type == nil || sym.Type == types.TypeInvalid || sym.Type == types.TypeVoid || sym.Type == types.TypeNever {
			continue
		}
		rng, ok := findDeclIdentRange("", src, vs.Span, vs.Name)
		if !ok {
			continue
		}
		ins := Range{Start: rng.End, End: rng.End}
		tyStr := sym.Type.String()
		addAction(CodeAction{
			Title: fmt.Sprintf("Add explicit type annotation ': %s'", tyStr),
			Kind:  "refactor.rewrite",
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					uri: {{Range: ins, NewText: ": " + tyStr}},
				},
			},
		})
		return
	}
}

func (s *Server) tryToggleBlockBracesAction(uri, src, ind string, body ast.Expr, off int, addAction func(CodeAction)) {
	checkBody := func(parentKind string, bodyExpr ast.Expr) bool {
		if bodyExpr == nil {
			return false
		}
		bsp := bodyExpr.GetSpan()
		if off < bsp.Start.Offset || off > bsp.End.Offset {
			return false
		}
		if blk, isBlk := bodyExpr.(*ast.BlockExpr); isBlk {
			if len(blk.Stmts) == 0 && blk.Tail != nil && blk.Label == "" {
				innerSp := blk.Tail.GetSpan()
				if innerSp.Start.Offset >= 0 && innerSp.End.Offset <= len(src) && innerSp.End.Offset > innerSp.Start.Offset {
					innerTxt := strings.TrimSpace(src[innerSp.Start.Offset:innerSp.End.Offset])
					var replacement string
					if strings.ContainsRune(innerTxt, '\n') {
						lineInd := getLineIndent(src, blk.Span.Start.Line-1)
						lines := strings.Split(innerTxt, "\n")
						for i, l := range lines {
							lines[i] = lineInd + ind + strings.TrimLeft(l, " \t")
						}
						replacement = "\n" + strings.Join(lines, "\n")
					} else {
						replacement = innerTxt
					}
					blkRng := ExactSpanToLSP(src, blk.Span)
					addAction(CodeAction{
						Title: "Unwrap single-expression '{ ... }' block",
						Kind:  "refactor.rewrite",
						Edit: &WorkspaceEdit{
							Changes: map[string][]TextEdit{
								uri: {{Range: blkRng, NewText: replacement}},
							},
						},
					})
					return true
				}
			}
			return false
		}
		// Non-block body expression -> offer wrapping in `{ ... }`
		if bsp.Start.Offset >= 0 && bsp.End.Offset <= len(src) && bsp.End.Offset > bsp.Start.Offset {
			bodyTxt := strings.TrimSpace(src[bsp.Start.Offset:bsp.End.Offset])
			lineInd := getLineIndent(src, bsp.Start.Line-1)
			wrapped := fmt.Sprintf("{\n%s%s%s\n%s}", lineInd, ind, strings.ReplaceAll(bodyTxt, "\n", "\n"+ind), lineInd)
			_ = parentKind
			addAction(CodeAction{
				Title: wrapBodyInBlockTitle,
				Kind:  "refactor.rewrite",
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						uri: {{Range: ExactSpanToLSP(src, bsp), NewText: wrapped}},
					},
				},
			})
			return true
		}
		return false
	}

	var walk func(e ast.Expr) bool
	walk = func(e ast.Expr) bool {
		if e == nil || !spanContainsOffset(e.GetSpan(), off) {
			return false
		}
		switch v := e.(type) {
		case *ast.BlockExpr:
			for _, st := range v.Stmts {
				if !spanContainsOffset(st.GetSpan(), off) {
					continue
				}
				switch s := st.(type) {
				case *ast.VarStmt:
					if walk(s.Value) {
						return true
					}
				case *ast.AssignStmt:
					if walk(s.Value) {
						return true
					}
				case *ast.UnwindStmt:
					if walk(s.Value) {
						return true
					}
				case *ast.ExprStmt:
					if walk(s.Expr) {
						return true
					}
				}
			}
			return walk(v.Tail)
		case *ast.IfExpr:
			if v.Then != nil && spanContainsOffset(v.Then.GetSpan(), off) {
				if walk(v.Then) {
					return true
				}
				return checkBody("if", v.Then)
			}
			if v.Else != nil && spanContainsOffset(v.Else.GetSpan(), off) {
				if walk(v.Else) {
					return true
				}
				if _, isElseIf := v.Else.(*ast.IfExpr); !isElseIf {
					return checkBody("else", v.Else)
				}
			}
		case *ast.ForExpr:
			if v.Body != nil && spanContainsOffset(v.Body.GetSpan(), off) {
				if walk(v.Body) {
					return true
				}
				return checkBody("for", v.Body)
			}
		case *ast.WhileExpr:
			if v.Body != nil && spanContainsOffset(v.Body.GetSpan(), off) {
				if walk(v.Body) {
					return true
				}
				return checkBody("while", v.Body)
			}
		case *ast.MatchExpr:
			for _, arm := range v.Arms {
				if arm.Synthetic {
					continue
				}
				if arm.Body != nil && spanContainsOffset(arm.Body.GetSpan(), off) {
					if walk(arm.Body) {
						return true
					}
					return checkBody("match", arm.Body)
				}
			}
		case *ast.FuncLit:
			if v.Body != nil && spanContainsOffset(v.Body.GetSpan(), off) {
				if walk(v.Body) {
					return true
				}
				return checkBody("func", v.Body)
			}
		}
		return false
	}

	if !walk(body) {
		checkBody("func", body)
	}
}

func (s *Server) tryFlipIfElseAction(uri, src string, body ast.Expr, off int, addAction func(CodeAction)) {
	var found *ast.IfExpr
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if e == nil || !spanContainsOffset(e.GetSpan(), off) {
			return
		}
		switch v := e.(type) {
		case *ast.BlockExpr:
			for _, st := range v.Stmts {
				if es, ok := st.(*ast.ExprStmt); ok {
					walk(es.Expr)
				} else if vs, ok := st.(*ast.VarStmt); ok {
					walk(vs.Value)
				}
			}
			walk(v.Tail)
		case *ast.IfExpr:
			walk(v.Then)
			walk(v.Else)
			if found == nil && v.CondKind == ast.IfCondBool && v.Cond != nil && v.Then != nil && v.Else != nil {
				if _, isElseIf := v.Else.(*ast.IfExpr); !isElseIf {
					found = v
				}
			}
		case *ast.ForExpr:
			walk(v.Body)
		case *ast.WhileExpr:
			walk(v.Body)
		case *ast.MatchExpr:
			for _, arm := range v.Arms {
				if arm.Synthetic {
					continue
				}
				walk(arm.Body)
			}
		}
	}
	walk(body)
	if found == nil {
		return
	}

	csp := found.Cond.GetSpan()
	tsp := found.Then.GetSpan()
	esp := found.Else.GetSpan()
	if csp.Start.Offset < 0 || tsp.Start.Offset < 0 || esp.Start.Offset < 0 || esp.End.Offset > len(src) {
		return
	}

	newCond := invertConditionText(found.Cond, src, src[csp.Start.Offset:csp.End.Offset])
	thenTxt := src[tsp.Start.Offset:tsp.End.Offset]
	elseTxt := src[esp.Start.Offset:esp.End.Offset]

	edits := []TextEdit{
		{Range: ExactSpanToLSP(src, esp), NewText: thenTxt},
		{Range: ExactSpanToLSP(src, tsp), NewText: elseTxt},
		{Range: ExactSpanToLSP(src, csp), NewText: newCond},
	}
	addAction(CodeAction{
		Title: "Invert 'if' / 'else' branches",
		Kind:  "refactor.rewrite",
		Edit:  &WorkspaceEdit{Changes: map[string][]TextEdit{uri: edits}},
	})
}

func invertConditionText(cond ast.Expr, src, rawCond string) string {
	switch c := cond.(type) {
	case *ast.UnaryExpr:
		if c.Op == token.KwNot && c.Operand != nil {
			opTxt := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawCond), "not"))
			return strings.TrimSpace(opTxt)
		}
	case *ast.BinaryExpr:
		invOp := ""
		switch c.Op {
		case token.Eq:
			invOp = "!="
		case token.Neq:
			invOp = "=="
		case token.Lt:
			invOp = ">="
		case token.Lte:
			invOp = ">"
		case token.Gt:
			invOp = "<="
		case token.Gte:
			invOp = "<"
		}
		if invOp != "" && c.Left != nil && c.Right != nil {
			lsp := c.Left.GetSpan()
			rsp := c.Right.GetSpan()
			if lsp.Start.Offset >= 0 && rsp.End.Offset <= len(src) && lsp.End.Offset > lsp.Start.Offset && rsp.End.Offset > rsp.Start.Offset {
				return src[lsp.Start.Offset:lsp.End.Offset] + " " + invOp + " " + src[rsp.Start.Offset:rsp.End.Offset]
			}
		}
	}
	if strings.ContainsAny(rawCond, " ") && !strings.HasPrefix(rawCond, "(") {
		return "not (" + rawCond + ")"
	}
	return "not " + rawCond
}

func (s *Server) tryExtractVariableAction(uri, src string, body ast.Expr, selStart, selEnd int, addAction func(CodeAction)) {
	var targetExpr ast.Expr
	var targetStmt ast.Stmt
	var tailParent *ast.BlockExpr

	var findInStmt func(st ast.Stmt) bool
	var findExactExpr func(e ast.Expr) ast.Expr

	findExactExpr = func(e ast.Expr) ast.Expr {
		if e == nil {
			return nil
		}
		sp := e.GetSpan()
		if sp.Start.Offset == selStart && sp.End.Offset == selEnd {
			return e
		}
		if !spanContainsOffset(sp, selStart) || selEnd > sp.End.Offset {
			return nil
		}
		switch v := e.(type) {
		case *ast.CallExpr:
			for _, a := range v.Args {
				if r := findExactExpr(a); r != nil {
					return r
				}
			}
		case *ast.BinaryExpr:
			if r := findExactExpr(v.Left); r != nil {
				return r
			}
			if r := findExactExpr(v.Right); r != nil {
				return r
			}
		case *ast.UnaryExpr:
			return findExactExpr(v.Operand)
		case *ast.UnsafeExpr:
			return findExactExpr(v.Expr)
		case *ast.MemberExpr:
			return findExactExpr(v.Base)
		case *ast.IndexExpr:
			if r := findExactExpr(v.Base); r != nil {
				return r
			}
			return findExactExpr(v.Index)
		case *ast.StructLit:
			for _, f := range v.Fields {
				if r := findExactExpr(f.Value); r != nil {
					return r
				}
			}
		case *ast.TupleLit:
			for _, el := range v.Elems {
				if r := findExactExpr(el); r != nil {
					return r
				}
			}
		case *ast.VecLit:
			for _, el := range v.Elems {
				if r := findExactExpr(el); r != nil {
					return r
				}
			}
		}
		return nil
	}

	findInStmt = func(st ast.Stmt) bool {
		if st == nil {
			return false
		}
		if selStart < st.GetSpan().Start.Offset || selEnd > st.GetSpan().End.Offset {
			return false
		}
		switch vs := st.(type) {
		case *ast.VarStmt:
			if r := findExactExpr(vs.Value); r != nil {
				// Avoid extracting an entire `let x = rhs` initializer redundantly unless nested
				if !(vs.Value.GetSpan().Start.Offset == selStart && vs.Value.GetSpan().End.Offset == selEnd) {
					targetExpr = r
					targetStmt = st
					return true
				}
			}
		case *ast.AssignStmt:
			if r := findExactExpr(vs.Value); r != nil {
				targetExpr = r
				targetStmt = st
				return true
			}
		case *ast.UnwindStmt:
			if r := findExactExpr(vs.Value); r != nil {
				targetExpr = r
				targetStmt = st
				return true
			}
		case *ast.ExprStmt:
			if r := findExactExpr(vs.Expr); r != nil {
				targetExpr = r
				targetStmt = st
				return true
			}
		}
		return false
	}

	var walkBlk func(e ast.Expr) bool
	walkBlk = func(e ast.Expr) bool {
		if e == nil {
			return false
		}
		if blk, ok := e.(*ast.BlockExpr); ok {
			for _, st := range blk.Stmts {
				if findInStmt(st) {
					return true
				}
				switch s := st.(type) {
				case *ast.ExprStmt:
					if walkBlk(s.Expr) {
						return true
					}
				case *ast.VarStmt:
					if walkBlk(s.Value) {
						return true
					}
				}
			}
			if blk.Tail != nil {
				if r := findExactExpr(blk.Tail); r != nil && !(blk.Tail.GetSpan().Start.Offset == selStart && blk.Tail.GetSpan().End.Offset == selEnd) {
					targetExpr = r
					tailParent = blk
					return true
				}
				if walkBlk(blk.Tail) {
					return true
				}
			}
		}
		switch v := e.(type) {
		case *ast.IfExpr:
			return walkBlk(v.Then) || walkBlk(v.Else)
		case *ast.ForExpr:
			return walkBlk(v.Body)
		case *ast.WhileExpr:
			return walkBlk(v.Body)
		case *ast.MatchExpr:
			for _, arm := range v.Arms {
				if arm.Synthetic {
					continue
				}
				if walkBlk(arm.Body) {
					return true
				}
			}
		}
		return false
	}

	walkBlk(body)
	if targetExpr == nil {
		return
	}

	exprText := src[selStart:selEnd]
	var stmtStartOff int
	var stmtLine int
	if targetStmt != nil {
		stmtStartOff = targetStmt.GetSpan().Start.Offset
		stmtLine = targetStmt.GetSpan().Start.Line - 1
	} else if tailParent != nil && tailParent.Tail != nil {
		stmtStartOff = tailParent.Tail.GetSpan().Start.Offset
		stmtLine = tailParent.Tail.GetSpan().Start.Line - 1
	} else {
		return
	}

	indent := getLineIndent(src, stmtLine)
	lineStart := stmtStartOff
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	insPos := OffsetToLSP(src, lineStart)
	selRng := Range{Start: OffsetToLSP(src, selStart), End: OffsetToLSP(src, selEnd)}

	addAction(CodeAction{
		Title: "Extract expression into 'let extracted'",
		Kind:  "refactor.extract",
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				uri: {
					{Range: selRng, NewText: "extracted"},
					{Range: Range{Start: insPos, End: insPos}, NewText: fmt.Sprintf("%slet extracted = %s\n", indent, exprText)},
				},
			},
		},
	})
	addAction(CodeAction{
		Title: "Extract expression into 'bind extracted'",
		Kind:  "refactor.extract",
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				uri: {
					{Range: selRng, NewText: "extracted"},
					{Range: Range{Start: insPos, End: insPos}, NewText: fmt.Sprintf("%sbind extracted = %s\n", indent, exprText)},
				},
			},
		},
	})
}

func (s *Server) tryExtractFunctionAction(
	uri, src, ind string,
	prog *sema.CheckedProgram,
	bm *sema.BodyMaps,
	body ast.Expr,
	selStart, selEnd int,
	declEnd Position,
	addAction func(CodeAction),
) {
	if bm == nil || selEnd <= selStart {
		return
	}
	blk, ok := body.(*ast.BlockExpr)
	if !ok {
		return
	}

	// Support extracting a single expression inside the block OR a contiguous slice of statements
	var retTypeStr string
	selectedText := strings.TrimSpace(src[selStart:selEnd])
	if selectedText == "" {
		return
	}

	// Collect free local symbols referenced inside [selStart, selEnd] whose declarations live outside [selStart, selEnd]
	type freeParam struct {
		name  string
		tyStr string
		isMut bool
	}
	seenFree := make(map[*sema.Symbol]bool)
	var freeParams []*freeParam

	recordSym := func(sym *sema.Symbol, isMut bool) {
		if sym == nil || sym.IsTopLevel || sym.Span.Start.Offset < 0 {
			return
		}
		if sym.Span.Start.Offset >= selStart && sym.Span.End.Offset <= selEnd {
			return // declared inside the extracted slice
		}
		if sym.Type == nil || sym.Type == types.TypeInvalid {
			return
		}
		if seenFree[sym] {
			if isMut {
				for _, fp := range freeParams {
					if fp.name == sym.Name {
						fp.isMut = true
					}
				}
			}
			return
		}
		seenFree[sym] = true
		freeParams = append(freeParams, &freeParam{
			name:  sym.Name,
			tyStr: sym.Type.String(),
			isMut: isMut || sym.BindKind == sema.BindMutBorrow,
		})
	}

	// Scan ExprInfos overlapping [selStart, selEnd]
	var matchingExpr ast.Expr
	for expr, inf := range bm.ExprInfos {
		if expr == nil || inf == nil {
			continue
		}
		sp := expr.GetSpan()
		if sp.Start.Offset == selStart && sp.End.Offset == selEnd {
			matchingExpr = expr
		}
		if sp.Start.Offset >= selStart && sp.End.Offset <= selEnd && inf.Sym != nil {
			recordSym(inf.Sym, false)
		}
	}
	// Mark mutated parameters if any AssignStmt inside selection mutates an identifier
	var scanMutations func(st ast.Stmt)
	scanMutations = func(st ast.Stmt) {
		if st == nil || st.GetSpan().End.Offset < selStart || st.GetSpan().Start.Offset > selEnd {
			return
		}
		if as, ok := st.(*ast.AssignStmt); ok {
			if id, okId := as.Target.(*ast.IdentExpr); okId {
				if inf := exprInfoFromBM(prog, bm, id); inf != nil && inf.Sym != nil {
					recordSym(inf.Sym, true)
				}
			}
		}
	}
	for _, st := range blk.Stmts {
		scanMutations(st)
	}

	if matchingExpr != nil {
		if inf := bm.ExprInfos[matchingExpr]; inf != nil && inf.Type != nil && !inf.Type.Equals(types.TypeVoid) && inf.Type != types.TypeInvalid {
			retTypeStr = inf.Type.String()
		}
	} else {
		// Check if extracting statements that don't overlap cleanly at all -> require clean AST expression or statement boundary match
		// The selection must cover whole statements: start where one begins and end where one ends.
		startsOK, endsOK := false, false
		trimmedEnd := selStart + len(strings.TrimRight(src[selStart:selEnd], " \t\r\n"))
		for _, st := range blk.Stmts {
			if st.GetSpan().Start.Offset == selStart {
				startsOK = true
			}
			if startsOK && st.GetSpan().End.Offset == trimmedEnd {
				endsOK = true
			}
		}
		if !startsOK || !endsOK {
			return
		}
	}

	var sigParams []string
	var callArgs []string
	for _, fp := range freeParams {
		if fp.isMut {
			sigParams = append(sigParams, fmt.Sprintf("%s: &%s", fp.name, fp.tyStr))
			callArgs = append(callArgs, "&"+fp.name)
		} else {
			sigParams = append(sigParams, fmt.Sprintf("%s: %s", fp.name, fp.tyStr))
			callArgs = append(callArgs, fp.name)
		}
	}

	retSuffix := ""
	if retTypeStr != "" {
		retSuffix = " " + retTypeStr
	}

	// Indent extracted body lines relative to 1 tab inside extracted function
	bodyLines := strings.Split(selectedText, "\n")
	minIndent := ""
	for _, l := range bodyLines {
		if strings.TrimSpace(l) != "" {
			minIndent = l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			break
		}
	}
	var cleanedBody []string
	for _, l := range bodyLines {
		cleanedBody = append(cleanedBody, ind+strings.TrimPrefix(l, minIndent))
	}

	var fnStub string
	if len(cleanedBody) == 1 && !strings.ContainsRune(selectedText, ';') && !strings.HasPrefix(strings.TrimSpace(selectedText), "let ") {
		fnStub = fmt.Sprintf("\n\nbind extracted_fn = fn(%s)%s: %s", strings.Join(sigParams, ", "), retSuffix, strings.TrimSpace(selectedText))
	} else {
		fnStub = fmt.Sprintf("\n\nbind extracted_fn = fn(%s)%s: {\n%s\n}", strings.Join(sigParams, ", "), retSuffix, strings.Join(cleanedBody, "\n"))
	}

	// Only offer the extraction when the new function parses on its own (a selection such as a
	// chain's `default panic` arm is not an expression).
	checkRep := diag.NewReporter()
	_ = parser.ParseFile("extracted.ll", strings.TrimSpace(fnStub)+"\n", checkRep)
	if checkRep.HasErrors() {
		return
	}

	selRng := Range{Start: OffsetToLSP(src, selStart), End: OffsetToLSP(src, selEnd)}
	callTxt := fmt.Sprintf("extracted_fn(%s)", strings.Join(callArgs, ", "))

	addAction(CodeAction{
		Title: "Extract selection into function 'extracted_fn'",
		Kind:  "refactor.extract",
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				uri: {
					{Range: selRng, NewText: callTxt},
					{Range: Range{Start: declEnd, End: declEnd}, NewText: fnStub},
				},
			},
		},
	})
}
