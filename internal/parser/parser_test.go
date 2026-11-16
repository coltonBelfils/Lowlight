package parser_test

import (
	"strings"
	"testing"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/parser"
)

func TestParseComprehensiveLanguageConstructs(t *testing.T) {
	src := `
import "std/pool.ll" as pool
extern "math.h"
extern cos: (x: float) float
extern free_ptr: (p: int)

define UserID int

define Status enum u8 [
    Pending = 1,
    Active = 2,
    Closed = 3,
]

define Printable interface [
    to_string: (self) string,
    noop: (self),
]

define Pair[A: Printable, B] struct [
    first: A,
    second: B,
    note?: ?string,
]

bind Pair[A, B].swap = (own self) Pair[B, A]: {
    Pair[B, A][
        first: self.second,
        second: self.first,
    ]
}

bind main = () int: {
    let p = Pair[int, string][
        first: 42,
        second: "answer",
    ]
    let nums: [3: int] = [1, 2, 3]
    let zeros = [16: 0]
    let ages = #[
        "Alice": 30,
        "Bob": 25,
    ]

    let age = ages["Alice"] ?? func <- 1
    if (ages["Bob"] as b): {
        println("Bob is $b")
    }

    let status = Status.Active
    let code = match (status) int:
        when Status.Pending: if (age > 20): 10 else if (age > 10): 11 else: 12
        when Status.Active: 20
        default Status.Closed: 30

    let total = outer: for (i, x in nums) int: {
        if (x < 0): next outer <-
        if (x == 99): outer <- 999
        x + i
    } else: {
        0
    }

    let c = unsafe {
        cos(0.0)
    }

    code + total + int(c) + p.first + zeros[0]
}
`
	rep := diag.NewReporter()
	file := parser.ParseFile("comprehensive.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected parse errors:\n%s", rep.Format())
	}
	if len(file.Decls) != 10 {
		t.Fatalf("expected 10 top-level declarations, got %d", len(file.Decls))
	}
}

func TestMultilineTrailingCommaRequired(t *testing.T) {
	src := `
define Bad struct [
    x: int,
    y: int
]
`
	rep := diag.NewReporter()
	_ = parser.ParseFile("bad_comma.ll", src, rep)
	if !rep.HasErrors() {
		t.Fatalf("expected error for missing trailing comma in multiline struct")
	}
	if !strings.Contains(rep.Format(), "missing trailing comma") {
		t.Fatalf("expected trailing comma error message, got:\n%s", rep.Format())
	}
}

func TestForbidNestedUnwindInArgList(t *testing.T) {
	src := `
bind main = (): {
    println(func <-)
}
`
	rep := diag.NewReporter()
	_ = parser.ParseFile("bad_unwind.ll", src, rep)
	if !rep.HasErrors() {
		t.Fatalf("expected error for unwind inside function argument list")
	}
}

func TestExpectPreservesSyncTokens(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		wantWantMsg string
		wantDiags   int
		checkAST    func(t *testing.T, f *ast.File)
	}{
		{
			name: "missing rparen before rbrace preserves closing brace and next decl",
			src: `
bind first = (): { let x = foo(1 }
bind second = () int: { 2 }
`,
			wantWantMsg: "expected )",
			wantDiags:   1,
			checkAST: func(t *testing.T, f *ast.File) {
				if len(f.Decls) != 2 {
					t.Fatalf("expected 2 decls, got %d", len(f.Decls))
				}
				d2, ok := f.Decls[1].(*ast.VarDecl)
				if !ok || d2.Name != "second" {
					t.Fatalf("expected second decl to be VarDecl 'second', got %#v", f.Decls[1])
				}
			},
		},
		{
			name: "missing rparen before let preserves next statement",
			src: `
bind main = () int: {
    let x = foo(1
    let y = 2
    y
}
`,
			wantWantMsg: "expected )",
			wantDiags:   1,
			checkAST: func(t *testing.T, f *ast.File) {
				d := f.Decls[0].(*ast.VarDecl)
				fn := d.Value.(*ast.FuncLit)
				blk, ok := fn.Body.(*ast.BlockExpr)
				if !ok || len(blk.Stmts) != 2 || blk.Tail == nil {
					t.Fatalf("expected 2 stmts + tail in recovered block, got %#v", fn.Body)
				}
			},
		},
		{
			name: "missing colon before lbrace preserves function block body",
			src: `
bind main = () int {
    42
}
`,
			wantWantMsg: "expected :",
			wantDiags:   1,
			checkAST: func(t *testing.T, f *ast.File) {
				d := f.Decls[0].(*ast.VarDecl)
				fn := d.Value.(*ast.FuncLit)
				blk, ok := fn.Body.(*ast.BlockExpr)
				if !ok || blk.Tail == nil {
					t.Fatalf("expected BlockExpr with tail 42, got %#v", fn.Body)
				}
			},
		},
		{
			name: "missing rbracket before define preserves next struct decl",
			src: `
define Bad struct [
    x: int
define Good struct [
    y: int,
]
`,
			wantWantMsg: "expected ]",
			wantDiags:   1,
			checkAST: func(t *testing.T, f *ast.File) {
				if len(f.Decls) != 2 {
					t.Fatalf("expected 2 decls, got %d", len(f.Decls))
				}
				s2, ok := f.Decls[1].(*ast.StructDecl)
				if !ok || s2.Name != "Good" || len(s2.Fields) != 1 || s2.Fields[0].Name != "y" {
					t.Fatalf("expected recovered StructDecl 'Good', got %#v", f.Decls[1])
				}
			},
		},
		{
			name: "missing import path before @ attribute preserves attributed decl",
			src: `
import
@test
bind ok = () int: { 1 }
`,
			wantWantMsg: "expected string literal",
			wantDiags:   1,
			checkAST: func(t *testing.T, f *ast.File) {
				if len(f.Decls) != 2 {
					t.Fatalf("expected 2 decls, got %d", len(f.Decls))
				}
				v, ok := f.Decls[1].(*ast.VarDecl)
				if !ok || v.Name != "ok" || len(v.Attributes) != 1 || v.Attributes[0].Name != "test" {
					t.Fatalf("expected recovered @test decl 'ok', got %#v", f.Decls[1])
				}
			},
		},
		{
			name: "missing parameter ident before colon preserves colon and type",
			src: `
bind f = (a: int, : int) int: { 1 }
`,
			wantWantMsg: "expected identifier",
			wantDiags:   1,
			checkAST: func(t *testing.T, f *ast.File) {
				v := f.Decls[0].(*ast.VarDecl)
				fn := v.Value.(*ast.FuncLit)
				if len(fn.Params) != 2 || fn.Ret == nil {
					t.Fatalf("expected 2 params and return type, got params=%d ret=%v", len(fn.Params), fn.Ret)
				}
			},
		},
		{
			name: "missing assign before lparen preserves function literal",
			src: `
bind f (x: int) int: { x }
`,
			wantWantMsg: "expected =",
			wantDiags:   1,
			checkAST: func(t *testing.T, f *ast.File) {
				v, ok := f.Decls[0].(*ast.VarDecl)
				if !ok || v.Name != "f" {
					t.Fatalf("expected VarDecl 'f', got %#v", f.Decls[0])
				}
				if _, ok := v.Value.(*ast.FuncLit); !ok {
					t.Fatalf("expected FuncLit value, got %T", v.Value)
				}
			},
		},
		{
			name: "missing member ident before InterpEnd preserves string terminator",
			src: `
bind f = () string: {
    let s = "v=${foo.}"
    s
}
`,
			wantWantMsg: "expected identifier",
			wantDiags:   1,
			checkAST: func(t *testing.T, f *ast.File) {
				v := f.Decls[0].(*ast.VarDecl)
				fn := v.Value.(*ast.FuncLit)
				blk, ok := fn.Body.(*ast.BlockExpr)
				if !ok || len(blk.Stmts) != 1 || blk.Tail == nil {
					t.Fatalf("expected 1 stmt + tail in block, got %#v", fn.Body)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := diag.NewReporter()
			f := parser.ParseFile("sync_test.ll", tc.src, rep)
			diags := rep.Diagnostics()
			if len(diags) != tc.wantDiags {
				t.Fatalf("expected %d diagnostic(s), got %d:\n%s", tc.wantDiags, len(diags), rep.Format())
			}
			if !strings.Contains(diags[0].Message, tc.wantWantMsg) {
				t.Fatalf("expected message containing %q, got %q", tc.wantWantMsg, diags[0].Message)
			}
			tc.checkAST(t, f)
		})
	}
}

func TestEmptyBracketTypeRejectionAndValuePreservation(t *testing.T) {
	rejected := []struct {
		name    string
		src     string
		wantMsg string
	}{
		{
			name:    "top-level var type []",
			src:     "bind xs: [] = []\n",
			wantMsg: "untyped `[]` is not a valid type",
		},
		{
			name:    "function param type []",
			src:     "bind f = (xs: []) int: { 0 }\n",
			wantMsg: "untyped `[]` is not a valid type",
		},
		{
			name:    "function return type []",
			src:     "bind f = () []: { [] }\n",
			wantMsg: "untyped `[]` is not a valid type",
		},
		{
			name:    "for eval type []",
			src:     "bind f = () [int]: { for (x in 0..10) []: x }\n",
			wantMsg: "untyped `[]` is not a valid type",
		},
		{
			name:    "empty generic type arguments Box[]",
			src:     "bind x: Box[] = Box[val: 1]\n",
			wantMsg: "empty generic type argument list `[]` is not allowed",
		},
		{
			name:    "empty generic type parameters Foo[]",
			src:     "define Foo[] struct [x: int]\n",
			wantMsg: "empty generic type parameter list `[]` is not allowed",
		},
	}

	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			rep := diag.NewReporter()
			_ = parser.ParseFile("empty_bracket_neg.ll", tc.src, rep)
			diags := rep.Diagnostics()
			if len(diags) == 0 {
				t.Fatalf("expected parse error containing %q, got none", tc.wantMsg)
			}
			if !strings.Contains(diags[0].Message, tc.wantMsg) {
				t.Fatalf("expected diagnostic %q, got:\n%s", tc.wantMsg, rep.Format())
			}
		})
	}

	t.Run("preserves empty list literal and zero-field struct construction", func(t *testing.T) {
		src := `
define Empty struct []

bind main = (): {
    let xs: [int] = []
    let e = Empty[]
    let typed = for (x in 0..5) [int]: x
}
`
		rep := diag.NewReporter()
		f := parser.ParseFile("empty_bracket_pos.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected parse errors:\n%s", rep.Format())
		}
		if len(f.Decls) != 2 {
			t.Fatalf("expected 2 decls, got %d", len(f.Decls))
		}
	})
}

func TestMatchContextualDefaultArm(t *testing.T) {
	t.Run("default as variable, field, unwind value, and closing arm", func(t *testing.T) {
		src := `
define Opts struct [
    default: int,
]

bind f = (flag: bool, x: int) int: {
    let default = 42
    let opts = Opts[default: default]
    let a = match (x):
        when 1: if (flag): opts.default
        default: default
    let b = match (x):
        when 2: {
            if (flag): <- default
            0
        }
        default: 9
    let c = match (x):
        when 3: <- default: 7
    a + b + c
}
`
		rep := diag.NewReporter()
		_ = parser.ParseFile("match_default.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected parse errors:\n%s", rep.Format())
		}
	})

	t.Run("old else arm on match is rejected cleanly", func(t *testing.T) {
		src := `
bind f = (x: int) int: {
    match (x):
        when 1: 10
        else: 20
}
`
		rep := diag.NewReporter()
		_ = parser.ParseFile("match_old_else.ll", src, rep)
		diags := rep.Diagnostics()
		if len(diags) == 0 {
			t.Fatalf("expected parse error for `else:` in `match`, got none")
		}
		if !strings.Contains(diags[0].Message, "default") {
			t.Fatalf("expected error mentioning `default`, got:\n%s", rep.Format())
		}
	})

	t.Run("enclosing if/for/while else after containerless match", func(t *testing.T) {
		src := `
bind f = (flag: bool, x: int) int: {
    let a = if (flag): match (x):
        when 1: 10
        default: 20
    else: 30
    let b = for (i in 0..3): match (i):
        when 1: for <- 10
        default: continue_val(i)
    else: 40
    let c = while (flag): match (x):
        when 1: while <- 10
        default: 20
    else: 50
    let d = if (flag): match (x): when 1: 1 default: 2 else: 3
    a + b + c + d
}
`
		rep := diag.NewReporter()
		f := parser.ParseFile("match_dangling_else.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected parse errors:\n%s", rep.Format())
		}
		fn := f.Decls[0].(*ast.VarDecl).Value.(*ast.FuncLit)
		blk := fn.Body.(*ast.BlockExpr)
		if len(blk.Stmts) != 4 {
			t.Fatalf("expected 4 let stmts, got %d", len(blk.Stmts))
		}
		aIf := blk.Stmts[0].(*ast.VarStmt).Value.(*ast.IfExpr)
		if _, ok := aIf.Then.(*ast.MatchExpr); !ok || aIf.Else == nil {
			t.Fatalf("expected multi-line if(flag) match ... default ... else to bind else to if, got %#v", aIf)
		}
		bFor := blk.Stmts[1].(*ast.VarStmt).Value.(*ast.ForExpr)
		if _, ok := bFor.Body.(*ast.MatchExpr); !ok || bFor.Else == nil {
			t.Fatalf("expected for(...) match ... default ... else to bind else to for, got %#v", bFor)
		}
		cWhile := blk.Stmts[2].(*ast.VarStmt).Value.(*ast.WhileExpr)
		if _, ok := cWhile.Body.(*ast.MatchExpr); !ok || cWhile.Else == nil {
			t.Fatalf("expected while(...) match ... default ... else to bind else to while, got %#v", cWhile)
		}
		dIf := blk.Stmts[3].(*ast.VarStmt).Value.(*ast.IfExpr)
		if _, ok := dIf.Then.(*ast.MatchExpr); !ok || dIf.Else == nil {
			t.Fatalf("expected single-line if(flag) match ... default ... else to bind else to if, got %#v", dIf)
		}
	})
}

func TestUnifiedEvalTypeAndVoidSignatures(t *testing.T) {
	src := `
define Iface interface [
    a: (self),
    b: (self) int,
    c: (self, cb: (int) bool) (int),
]

bind f = () void: { println("hi") }
bind ho = () (int) bool: (x: int) bool: x > 0
bind nested = () (int): (y: int): println("$y")

bind main = (x: int) int: {
    let a = if (x > 0) int: 1 else if (x == 0) int: 2 else: 3
    let b = if (x > 0) ?int: x
    let c = while (x > 0) ?int: while <- 7
    let d = for (i in 0..4) #[int]: i
    let e = match (x) int:
        when 1: 10
        default: 20
    let g = match () int:
        when x > 0: 1
        default: 2
    let h = match:
        when x > 0: 1
        default: 2
    let _ = match (x):
        when 1: 1
        default: 2
    a + (b ?? 0) + (c ?? 0) + len(d) + e + g + h
}
`
	rep := diag.NewReporter()
	file := parser.ParseFile("unified_syntax.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected parse errors:\n%s", rep.Format())
	}
	if len(file.Decls) != 5 {
		t.Fatalf("expected 5 decls, got %d", len(file.Decls))
	}
}
