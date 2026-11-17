package lsp

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

// computeInlayHints produces type and parameter name hints for the requested range.
func (s *Server) computeInlayHints(filePath string, src string, visRange Range) []InlayHint {
	cfg := s.getInlayHintConfig()
	if !cfg.Types && !cfg.Parameters {
		return []InlayHint{}
	}

	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return []InlayHint{}
	}

	s.mu.RLock()
	entry, cached := s.inlayCache[prog]
	s.mu.RUnlock()
	if cached && entry.cfg == cfg {
		return filterInlayHintsByRange(entry.hints, visRange)
	}

	absTarget, err := filepath.Abs(filePath)
	if err != nil {
		absTarget = filePath
	}
	modPath := mod.Path
	inTargetFile := func(sp diag.Span) bool {
		f := sp.Start.File
		return f == "" || f == filePath || f == absTarget || f == modPath
	}

	var hints []InlayHint
	seenPositions := make(map[string]bool)

	addHint := func(h InlayHint) {
		key := fmt.Sprintf("%d:%d:%s", h.Position.Line, h.Position.Character, h.Label)
		if seenPositions[key] {
			return
		}
		seenPositions[key] = true
		hints = append(hints, h)
	}

	// Collect all BodyMaps belonging to this file: top-level, functions/methods, and lambdas.
	var bodyMaps []*sema.BodyMaps
	seenBM := make(map[*sema.BodyMaps]bool)
	addBM := func(bm *sema.BodyMaps) {
		if bm == nil || seenBM[bm] {
			return
		}
		seenBM[bm] = true
		bodyMaps = append(bodyMaps, bm)
	}
	addBM(prog.TopBodyMaps)
	for _, fi := range prog.Funcs {
		if fi != nil && fi.Mod == mod {
			addBM(fi.BodyMaps)
		}
	}
	for _, li := range prog.Lambdas {
		if li != nil && li.Lit != nil && inTargetFile(li.Lit.Span) {
			addBM(li.BodyMaps)
		}
	}

	extractParamName := func(declStr string) string {
		if colon := strings.IndexByte(declStr, ':'); colon > 0 {
			return strings.TrimSpace(declStr[:colon])
		}
		return strings.TrimSpace(declStr)
	}

	for _, bm := range bodyMaps {
		if cfg.Types {
			// Variable type hints: `let x = ...` without explicit type annotation
			for vStmt, sym := range bm.VarSymbols {
				if vStmt == nil || sym == nil || vStmt.Type != nil || vStmt.Name == "_" {
					continue
				}
				if !inTargetFile(vStmt.Span) || vStmt.Span.Start.Line <= 0 {
					continue
				}
				if _, isFn := vStmt.Value.(*ast.FuncLit); isFn {
					continue
				}
				if sym.Type == nil || sym.Type == types.TypeInvalid || sym.Type == types.TypeVoid || sym.Type == types.TypeNever {
					continue
				}
				nameRng, ok := findDeclIdentRange(filePath, src, vStmt.Span, vStmt.Name)
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
				if vStmt == nil || !inTargetFile(vStmt.Span) {
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
					if uv.Span.Start.Line <= 0 {
						continue
					}
					nameRng, ok := findDeclIdentRange(filePath, src, uv.Span, uv.Name)
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
		}

		if cfg.Parameters {
			// Parameter name hints: `foo(a, b)` -> `foo(x: a, y: b)`
			for expr, inf := range bm.ExprInfos {
				call, ok := expr.(*ast.CallExpr)
				if !ok || inf == nil || len(call.Args) == 0 || !inTargetFile(call.Span) {
					continue
				}

				var paramNames []string
				switch inf.CallKind {
				case sema.CallDirectFunc:
					if inf.TargetFunc != nil {
						start := 0
						if inf.TargetFunc.IsMethod && !inf.TargetFunc.IsStaticAssoc {
							start = 1
						}
						if start < len(inf.TargetFunc.ParamNames) {
							paramNames = inf.TargetFunc.ParamNames[start:]
						}
					}
				case sema.CallExternC:
					if inf.TargetExtern != nil {
						for _, p := range inf.TargetExtern.Sig.Params {
							paramNames = append(paramNames, p.Name)
						}
					}
				case sema.CallBuiltinFunc, sema.CallIntrinsic:
					switch inf.BuiltinName {
					case "println", "print", "eprintln", "eprint":
						// Single obvious output argument doesn't benefit from `val:`.
					default:
						if bf := LookupBuiltinFunc(inf.BuiltinName); bf != nil {
							for _, p := range bf.Params {
								paramNames = append(paramNames, extractParamName(p))
							}
						}
					}
				case sema.CallBuiltinMethod:
					if mem, isMem := call.Callee.(*ast.MemberExpr); isMem {
						if bInf := exprInfoFromBM(prog, bm, mem.Base); bInf != nil && bInf.Type != nil {
							recvTy := unwrapOptAndRef(bInf.Type)
							for _, mDoc := range BuiltinMethodsForType(recvTy, inf.IsOptChain) {
								if mDoc.Name == mem.Member {
									for _, p := range mDoc.Params {
										paramNames = append(paramNames, extractParamName(p))
									}
									break
								}
							}
						}
					}
				}

				if len(paramNames) == 0 {
					continue
				}
				for idx, arg := range call.Args {
					if idx >= len(paramNames) {
						break
					}
					pName := paramNames[idx]
					if pName == "" || pName == "_" || pName == "self" {
						continue
					}
					// Elide hint if argument is already an identifier with matching name
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
	}

	sort.Slice(hints, func(i, j int) bool {
		if hints[i].Position.Line != hints[j].Position.Line {
			return hints[i].Position.Line < hints[j].Position.Line
		}
		if hints[i].Position.Character != hints[j].Position.Character {
			return hints[i].Position.Character < hints[j].Position.Character
		}
		return hints[i].Label < hints[j].Label
	})

	s.mu.Lock()
	if len(s.inlayCache) > 64 {
		s.inlayCache = make(map[*sema.CheckedProgram]cachedInlayEntry)
	}
	s.inlayCache[prog] = cachedInlayEntry{cfg: cfg, hints: hints}
	s.mu.Unlock()

	return filterInlayHintsByRange(hints, visRange)
}

func filterInlayHintsByRange(hints []InlayHint, visRange Range) []InlayHint {
	if len(hints) == 0 {
		return []InlayHint{}
	}
	if visRange.Start == visRange.End {
		return hints
	}
	startIdx := sort.Search(len(hints), func(i int) bool {
		return !posLess(hints[i].Position, visRange.Start)
	})
	var filtered []InlayHint
	for i := startIdx; i < len(hints); i++ {
		if posLess(visRange.End, hints[i].Position) {
			break
		}
		filtered = append(filtered, hints[i])
	}
	if filtered == nil {
		return []InlayHint{}
	}
	return filtered
}
