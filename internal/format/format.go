package format

import (
	"fmt"
	"os"
	"path/filepath"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/parser"
)

// Format parses and formats Lowlight source code according to opts.
// If the input contains syntax errors, it returns the original source and an error.
func Format(filename, src string, opts Options) (string, error) {
	rep := diag.NewReporter()
	file, comments := parser.ParseFileWithComments(filename, src, rep)
	if rep.HasErrors() {
		return src, fmt.Errorf("cannot format %s due to syntax errors:\n%s", filename, rep.Format())
	}
	return FormatAST(src, file, comments, opts), nil
}

// FormatFile reads path from disk, loads the nearest `.lowlightfmt` config when opts is nil,
// formats the source, and returns the formatted string and whether it changed.
func FormatFile(path string, opts *Options) (formatted string, changed bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	src := string(raw)
	effective := DefaultOptions()
	if opts != nil {
		effective = opts.Normalized()
	} else {
		effective = LoadConfig(filepath.Dir(path))
	}
	out, err := Format(path, src, effective)
	if err != nil {
		return src, false, err
	}
	return out, out != src, nil
}

// FormatAST formats a parsed *ast.File and its lexer comments according to opts.
func FormatAST(src string, file *ast.File, comments []lexer.Comment, opts Options) string {
	p := newPrinter(src, comments, opts.Normalized())
	p.printFile(file)
	return p.finish()
}
