package lsp

import (
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/types"
)

// computeInlayHints produces type and parameter name hints for the requested range.
func (s *Server) computeInlayHints(filePath string, src string, visRange Range) []InlayHint {
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return []InlayHint{}
	}

	var hints []InlayHint
	seenPositions := make(map[string]bool)

	addHint := func(h InlayHint) {
		key := strings.Join([]string{
			h.Label,
			string(rune(h.Position.Line)),
			string(rune(h.Position.Character)),
		}, "_")
		if seenPositions[key] {
			return
		}
		seenPositions[key] = true
		hints = append(hints, h)
	}

	// 1. Traverse function instances defined in this module
	for _, fi := range prog.Funcs {
		if fi == nil || fi.Mod != mod || fi.BodyMaps == nil {
			continue
		}
		bm := fi.BodyMaps

		// Variable type hints: `let x = ...` without explicit type annotation
		for vStmt, sym := range bm.VarSymbols {
			if vStmt == nil || sym == nil || vStmt.Type != nil || vStmt.Name == "_" {
				continue
			}
			if sym.Type == nil || sym.Type == types.TypeInvalid || sym.Type == types.TypeVoid || sym.Type == types.TypeNever {
				continue
			}

			// Find variable name range in line
			vLine := vStmt.Span.Start.Line - 1
			if vLine < 0 {
				continue
			}
			lineRng := Range{
				Start: Position{Line: vLine, Character: 0},
				End:   Position{Line: vLine + 1, Character: 0},
			}
			nameRng, ok := findWordRangeInSpan(src, lineRng, vStmt.Name)
			if !ok {
				continue
			}

			addHint(InlayHint{
				Position:     nameRng.End,
				Label:        ": " + sym.Type.String(),
				Kind:         InlayHintType,
				PaddingLeft:  false,
				PaddingRight: true,
			})
		}

		// Unpacked variable type hints: `let x, y = unpack ...` without explicit type annotations
		for vStmt, syms := range bm.UnpackVarSymbols {
			if vStmt == nil {
				continue
			}
			for i, uv := range vStmt.UnpackVars {
				if i >= len(syms) || syms[i] == nil || uv.Type != nil || uv.Name == "_" {
					continue
				}
				sym := syms[i]
				if sym.Type == nil || sym.Type == types.TypeInvalid || sym.Type == types.TypeVoid || sym.Type == types.TypeNever {
					continue
				}
				vLine := uv.Span.Start.Line - 1
				if vLine < 0 {
					continue
				}
				lineRng := Range{
					Start: Position{Line: vLine, Character: 0},
					End:   Position{Line: vLine + 1, Character: 0},
				}
				nameRng, ok := findWordRangeInSpan(src, lineRng, uv.Name)
				if !ok {
					continue
				}
				addHint(InlayHint{
					Position:     nameRng.End,
					Label:        ": " + sym.Type.String(),
					Kind:         InlayHintType,
					PaddingLeft:  false,
					PaddingRight: true,
				})
			}
		}

		// Parameter name hints: `foo(a, b)` -> `foo(x: a, y: b)`
		for expr, inf := range bm.ExprInfos {
			call, ok := expr.(*ast.CallExpr)
			if !ok || inf == nil || inf.TargetFunc == nil || len(call.Args) == 0 {
				continue
			}
			tf := inf.TargetFunc
			dropSelf := 0
			if tf.IsMethod && !tf.IsStaticAssoc {
				dropSelf = 1
			}

			for idx, arg := range call.Args {
				pIdx := idx + dropSelf
				if pIdx >= len(tf.ParamNames) {
					break
				}
				pName := tf.ParamNames[pIdx]
				if pName == "" || pName == "_" || pName == "self" {
					continue
				}

				// Elide hint if argument is already an identifier with matching name `foo(bar: bar)`
				if id, isId := arg.(*ast.IdentExpr); isId && (id.Name == pName || id.Name == pName+"?") {
					continue
				}

				argSpan := arg.GetSpan()
				if argSpan.Start.Line <= 0 {
					continue
				}
				argPos := SpanToLSP(src, argSpan).Start

				addHint(InlayHint{
					Position:     argPos,
					Label:        pName + ": ",
					Kind:         InlayHintParameter,
					PaddingRight: true,
				})
			}
		}
	}

	// Filter by visRange if non-empty
	if visRange.Start != visRange.End {
		var filtered []InlayHint
		for _, h := range hints {
			if posLess(h.Position, visRange.Start) || posLess(visRange.End, h.Position) {
				continue
			}
			filtered = append(filtered, h)
		}
		hints = filtered
	}

	sort.Slice(hints, func(i, j int) bool {
		if hints[i].Position.Line != hints[j].Position.Line {
			return hints[i].Position.Line < hints[j].Position.Line
		}
		return hints[i].Position.Character < hints[j].Position.Character
	})

	return hints
}
