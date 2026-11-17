package parser_test

import (
	"testing"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/parser"
)

func TestDocCommentAttachment(t *testing.T) {
	src := `// Module level summary for math helpers.
// Provides vector and scalar utilities.

// ==========================================
// Vectors
// ==========================================

// Represents a 2D point.
@packed
define Point struct [
	// X coordinate.
	x: float,
	y: float, // Y coordinate inline.
]

/*
 * Status describes execution state.
 * Multi-line block comment.
 */
define Status enum [
	// Ready state.
	Ready,
	Failed: string, // Error payload inline.
]

// Interface doc.
define Show interface [
	// Render as string.
	show: (self) string,
	alt: (self) string, // Alt inline.
]

// Distinct doc.
define Meters float

// Extern fn doc.
extern sqrt: (x: float) float

// Function doc line 1.
// Function doc line 2.
bind add = (a: int, b: int) int: a + b

// Detached comment with blank line below.

bind detached = () int: 0

// Method doc.
bind Point.norm = (self) float: self.x + self.y
`

	rep := diag.NewReporter()
	f, comments := parser.ParseFileWithComments("doc_test.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected parse errors: %v", rep.Diagnostics())
	}
	if len(comments) == 0 {
		t.Fatalf("expected comments to be returned")
	}
	if f.Doc != "Module level summary for math helpers.\nProvides vector and scalar utilities." {
		t.Fatalf("unexpected file doc: %q", f.Doc)
	}

	var (
		pt       *ast.StructDecl
		st       *ast.EnumDecl
		iface    *ast.InterfaceDecl
		dt       *ast.DistinctTypeDecl
		extFn    *ast.ExternFnDecl
		addFn    *ast.VarDecl
		detachFn *ast.VarDecl
		meth     *ast.MethodDecl
	)
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.StructDecl:
			pt = v
		case *ast.EnumDecl:
			st = v
		case *ast.InterfaceDecl:
			iface = v
		case *ast.DistinctTypeDecl:
			dt = v
		case *ast.ExternFnDecl:
			extFn = v
		case *ast.VarDecl:
			if v.Name == "add" {
				addFn = v
			} else if v.Name == "detached" {
				detachFn = v
			}
		case *ast.MethodDecl:
			meth = v
		}
	}

	if pt == nil || pt.Doc != "Represents a 2D point." {
		t.Fatalf("unexpected struct doc: %+v", pt)
	}
	if pt.Fields[0].Doc != "X coordinate." {
		t.Fatalf("unexpected field[0] doc: %q", pt.Fields[0].Doc)
	}
	if pt.Fields[1].Doc != "Y coordinate inline." {
		t.Fatalf("unexpected field[1] doc: %q", pt.Fields[1].Doc)
	}
	if st == nil || st.Doc != "Status describes execution state.\nMulti-line block comment." {
		t.Fatalf("unexpected enum doc: %q", st.Doc)
	}
	if st.Variants[0].Doc != "Ready state." || st.Variants[1].Doc != "Error payload inline." {
		t.Fatalf("unexpected variant docs: %q, %q", st.Variants[0].Doc, st.Variants[1].Doc)
	}
	if iface == nil || iface.Doc != "Interface doc." {
		t.Fatalf("unexpected interface doc: %+v", iface)
	}
	if iface.Methods[0].Doc != "Render as string." || iface.Methods[1].Doc != "Alt inline." {
		t.Fatalf("unexpected interface method docs: %q, %q", iface.Methods[0].Doc, iface.Methods[1].Doc)
	}
	if dt == nil || dt.Doc != "Distinct doc." {
		t.Fatalf("unexpected distinct doc: %+v", dt)
	}
	if extFn == nil || extFn.Doc != "Extern fn doc." {
		t.Fatalf("unexpected extern fn doc: %+v", extFn)
	}
	if addFn == nil || addFn.Doc != "Function doc line 1.\nFunction doc line 2." {
		t.Fatalf("unexpected func doc: %+v", addFn)
	}
	if detachFn == nil || detachFn.Doc != "" {
		t.Fatalf("expected detached comment NOT to attach, got %q", detachFn.Doc)
	}
	if meth == nil || meth.Doc != "Method doc." {
		t.Fatalf("unexpected method doc: %+v", meth)
	}
}

func TestModuleDocSkipsBannerDivider(t *testing.T) {
	src := `// ==========================================
// std/demo — Demonstration Module
// ==========================================
//
// Core summary line.

bind f = () int: 1
`
	rep := diag.NewReporter()
	f, _ := parser.ParseFileWithComments("banner.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors: %v", rep.Diagnostics())
	}
	want := "std/demo — Demonstration Module\n\nCore summary line."
	if f.Doc != want {
		t.Fatalf("got module doc %q, want %q", f.Doc, want)
	}
}
