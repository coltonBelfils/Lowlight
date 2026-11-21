package lsp

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/token"
)

// computePrepareRename verifies that the element at `pos` can be renamed and returns its Range.
func (s *Server) computePrepareRename(filePath string, src string, pos Position) (*Range, error) {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}

	tok, ok := s.findTokenAtOffset(filePath, src, offset)
	if !ok || (tok.Kind != token.Ident && tok.Kind != token.OptIdent) {
		return nil, fmt.Errorf("cannot rename: position is not an identifier")
	}

	if LookupKeywordDoc(tok.Text) != nil || LookupBuiltinFunc(tok.Text) != nil || LookupBuiltinTypeDoc(tok.Text) != nil {
		return nil, fmt.Errorf("cannot rename built-in symbol %q", tok.Text)
	}

	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod != nil && mod.File != nil {
		if tgt := resolveRefTargetAt(prog, mod, filePath, src, offset, tok); tgt != nil && tgt.isBuiltin {
			return nil, fmt.Errorf("cannot rename built-in symbol %q", tok.Text)
		}
	}

	rng := ExactSpanToLSP(src, tok.Span)
	return &rng, nil
}

// computeRename performs a scope- and symbol-aware rename (single- or cross-file) of `pos` to `newName`.
func (s *Server) computeRename(filePath string, src string, pos Position, newName string) (*WorkspaceEdit, error) {
	return s.renameSymbol(filePath, src, pos, newName, true)
}

// renameSymbol renames the symbol at pos. With keepSuffix, the `?` (optional) or `!` (result)
// suffix of the old name is preserved, as editor-driven renames must keep Lowlight's naming
// invariants; quick fixes that repair a wrong suffix pass keepSuffix=false.
func (s *Server) renameSymbol(filePath string, src string, pos Position, newName string, keepSuffix bool) (*WorkspaceEdit, error) {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}

	tok, ok := s.findTokenAtOffset(filePath, src, offset)
	if !ok || (tok.Kind != token.Ident && tok.Kind != token.OptIdent) {
		return nil, fmt.Errorf("cannot rename: position is not an identifier")
	}

	if LookupKeywordDoc(tok.Text) != nil || LookupBuiltinFunc(tok.Text) != nil || LookupBuiltinTypeDoc(tok.Text) != nil {
		return nil, fmt.Errorf("cannot rename built-in symbol %q", tok.Text)
	}

	newName = strings.TrimSpace(newName)
	if newName == "" {
		return nil, fmt.Errorf("new name cannot be empty")
	}

	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)

	var tgt *refTarget
	if mod != nil && mod.File != nil {
		tgt = resolveRefTargetAt(prog, mod, filePath, src, offset, tok)
		if tgt != nil && tgt.isBuiltin {
			return nil, fmt.Errorf("cannot rename built-in symbol %q", tok.Text)
		}
	}

	oldName := tok.Text
	isOpt := strings.HasSuffix(oldName, "?")
	if tgt != nil && tgt.isOptionalName {
		isOpt = true
	}
	isRes := strings.HasSuffix(oldName, "!")

	// Preserve Lowlight's strict optional (`?`) and result (`!`) identifier invariants.
	baseNew := strings.TrimSuffix(strings.TrimSuffix(newName, "?"), "!")
	if !isValidIdentName(baseNew) {
		return nil, fmt.Errorf("invalid Lowlight identifier %q", newName)
	}
	if LookupKeywordDoc(baseNew) != nil {
		return nil, fmt.Errorf("cannot rename to reserved keyword %q", baseNew)
	}

	if keepSuffix {
		switch {
		case isOpt:
			newName = baseNew + "?"
		case isRes:
			newName = baseNew + "!"
		default:
			newName = baseNew
		}
	}

	if tgt != nil && tgt.kind != refNone {
		if tgt.name == newName {
			return &WorkspaceEdit{Changes: map[string][]TextEdit{}}, nil
		}
		occs := s.collectTargetOccurrences(prog, mod, filePath, src, tgt)
		if len(occs) > 0 {
			changes := make(map[string][]TextEdit)
			srcCache := map[string]string{absClean(filePath): src}
			for _, occ := range occs {
				fSrc, has := srcCache[occ.filePath]
				if !has {
					fSrc = s.readSourceForPath(occ.filePath)
					srcCache[occ.filePath] = fSrc
				}
				if fSrc == "" || occ.endOff <= occ.startOff || occ.endOff > len(fSrc) {
					continue
				}
				replacement := newName
				if occ.isUnwrap {
					replacement = baseNew
				}
				uri := PathToURI(occ.filePath)
				changes[uri] = append(changes[uri], TextEdit{
					Range: Range{
						Start: OffsetToLSP(fSrc, occ.startOff),
						End:   OffsetToLSP(fSrc, occ.endOff),
					},
					NewText: replacement,
				})
			}
			for uri, list := range changes {
				sort.Slice(list, func(i, j int) bool {
					if list[i].Range.Start.Line != list[j].Range.Start.Line {
						return list[i].Range.Start.Line > list[j].Range.Start.Line
					}
					return list[i].Range.Start.Character > list[j].Range.Start.Character
				})
				changes[uri] = list
			}
			return &WorkspaceEdit{Changes: changes}, nil
		}
	}

	// Fallback if AST couldn't resolve the token (e.g., partial/broken file during typing):
	// safely restrict to exact identifier tokens inside the current file only.
	if oldName == newName {
		return &WorkspaceEdit{Changes: map[string][]TextEdit{}}, nil
	}
	silentRep := diag.NewReporter()
	toks := lexer.Lex(filePath, src, silentRep)
	var edits []TextEdit
	seenOffsets := make(map[int]bool)
	for _, tk := range toks {
		if (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == oldName {
			if seenOffsets[tk.Span.Start.Offset] {
				continue
			}
			seenOffsets[tk.Span.Start.Offset] = true
			edits = append(edits, TextEdit{
				Range:   ExactSpanToLSP(src, tk.Span),
				NewText: newName,
			})
		}
	}
	return &WorkspaceEdit{
		Changes: map[string][]TextEdit{
			PathToURI(filePath): edits,
		},
	}, nil
}

func isValidIdentName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
		} else {
			if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}
