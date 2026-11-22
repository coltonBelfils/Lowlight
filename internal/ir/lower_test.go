package ir

import (
	"os"
	"path/filepath"
	"testing"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
)

func TestFormatSpanPosRelativeAndFallback(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	wsDir := t.TempDir()
	subDir := filepath.Join(wsDir, "pkg", "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	insideFile := filepath.Join(subDir, "json.ll")
	if err := os.WriteFile(insideFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := os.Chdir(wsDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	exprInside := &ast.IdentExpr{
		Name: "x",
		Span: diag.Span{Start: diag.Pos{File: insideFile, Line: 541, Col: 2}},
	}
	file, line, col := formatSpanPos(exprInside)
	if file != "pkg/sub/json.ll" || line != 541 || col != 2 {
		t.Fatalf("inside workspace: got (%q, %d, %d), want (%q, 541, 2)", file, line, col, "pkg/sub/json.ll")
	}

	outsideFile := filepath.Join(t.TempDir(), "external.ll")
	exprOutside := &ast.IdentExpr{
		Name: "y",
		Span: diag.Span{Start: diag.Pos{File: outsideFile, Line: 12, Col: 7}},
	}
	fileOut, lineOut, colOut := formatSpanPos(exprOutside)
	if fileOut != "external.ll" || lineOut != 12 || colOut != 7 {
		t.Fatalf("outside workspace: got (%q, %d, %d), want (%q, 12, 7)", fileOut, lineOut, colOut, "external.ll")
	}

	exprRel := &ast.IdentExpr{
		Name: "z",
		Span: diag.Span{Start: diag.Pos{File: "a\\b\\c.ll", Line: 3, Col: 4}},
	}
	if runtimeFile, _, _ := formatSpanPos(exprRel); runtimeFile == "" {
		t.Fatalf("expected non-empty path for relative input")
	}
}
