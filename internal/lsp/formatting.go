package lsp

import (
	"path/filepath"

	"lowlight/internal/format"
)

// computeDocumentFormatting produces a TextEdit replacing the entire document
// with AST-formatted Lowlight source code according to workspace config and LSP options.
func (s *Server) computeDocumentFormatting(path, src string, opts FormattingOptions) []TextEdit {
	var fmtOpts format.Options
	var hasCfg bool
	if path != "" {
		fmtOpts, hasCfg = format.LoadConfigFound(filepath.Dir(path))
	} else {
		fmtOpts = format.DefaultOptions()
	}

	if !hasCfg {
		cfg := s.getFormatConfig()
		if cfg.Indent != "" {
			fmtOpts.IndentStyle = cfg.Indent
		} else if opts.InsertSpaces {
			fmtOpts.IndentStyle = "spaces"
		} else {
			fmtOpts.IndentStyle = "tabs"
		}
		if cfg.TabWidth > 0 {
			fmtOpts.TabWidth = cfg.TabWidth
		} else if opts.TabSize > 0 {
			fmtOpts.TabWidth = opts.TabSize
		}
		if cfg.LineWidthSet {
			fmtOpts.LineWidth = cfg.LineWidth
		}
	}

	result, err := format.Format(path, src, fmtOpts)
	if err != nil {
		// Do not clobber incomplete code while the user is typing.
		return []TextEdit{}
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
