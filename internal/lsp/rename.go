package lsp

import (
	"fmt"
	"strings"

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

	tok, ok := findTokenAtOffset(filePath, src, offset)
	if !ok || (tok.Kind != token.Ident && tok.Kind != token.OptIdent) {
		return nil, fmt.Errorf("cannot rename: position is not an identifier")
	}

	rng := ExactSpanToLSP(src, tok.Span)
	return &rng, nil
}

// computeRename performs a rename of the symbol under `pos` to `newName`.
func (s *Server) computeRename(filePath string, src string, pos Position, newName string) (*WorkspaceEdit, error) {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}

	tok, ok := findTokenAtOffset(filePath, src, offset)
	if !ok || (tok.Kind != token.Ident && tok.Kind != token.OptIdent) {
		return nil, fmt.Errorf("cannot rename: position is not an identifier")
	}

	oldName := tok.Text
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return nil, fmt.Errorf("new name cannot be empty")
	}

	// Preserve Lowlight's strict optional identifier invariant:
	// If the existing name has a `?` suffix, the new name must also end with `?`.
	if strings.HasSuffix(oldName, "?") && !strings.HasSuffix(newName, "?") {
		newName += "?"
	} else if !strings.HasSuffix(oldName, "?") && strings.HasSuffix(newName, "?") {
		newName = strings.TrimSuffix(newName, "?")
	}

	if oldName == newName {
		return &WorkspaceEdit{Changes: map[string][]TextEdit{}}, nil
	}

	// Lex the source file to find all occurrences of `oldName` as an identifier
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

	uri := PathToURI(filePath)
	return &WorkspaceEdit{
		Changes: map[string][]TextEdit{
			uri: edits,
		},
	}, nil
}
