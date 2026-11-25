package parser_test

import (
	"strings"
	"testing"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/parser"
	"lowlight/internal/token"
)

func TestParseComprehensiveLanguageConstructs(t *testing.T) {
	src := `
import "std/pool.ll" as pool
extern "math.h"
extern cos: fn(x: float) float
extern free_ptr: fn(p: int)

define UserID int

define Status enum u8 [
    Pending = 1,
    Active = 2,
    Closed = 3,
]

define Printable interface [
    to_string: fn(self) string,
    noop: fn(self),
]

define Pair[A: Printable, B] struct [
    first: A,
    second: B,
    note?: ?string,
]

bind Pair[A, B].swap = fn(own self) Pair[B, A]: {
    Pair[B, A][
        first: self.second,
        second: self.first,
    ]
}

bind main = fn() int: {
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
bind main = fn(): {
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
bind first = fn(): { let x = foo(1 }
bind second = fn() int: { 2 }
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
bind main = fn() int: {
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
bind main = fn() int {
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
bind ok = fn() int: { 1 }
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
bind f = fn(a: int, : int) int: { 1 }
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
bind f fn(x: int) int: { x }
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
bind f = fn() string: {
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
			src:     "bind f = fn(xs: []) int: { 0 }\n",
			wantMsg: "untyped `[]` is not a valid type",
		},
		{
			name:    "function return type []",
			src:     "bind f = fn() []: { [] }\n",
			wantMsg: "untyped `[]` is not a valid type",
		},
		{
			name:    "for eval type []",
			src:     "bind f = fn() [int]: { for (x in 0..10) []: x }\n",
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
		{
			name:    "bare integer literal variable type",
			src:     "bind x: 123 = 1\n",
			wantMsg: "expected type, got integer literal (\"123\")",
		},
		{
			name:    "integer literal inside vector type [123]",
			src:     "bind xs: [123] = []\n",
			wantMsg: "expected type, got integer literal (\"123\")",
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

bind main = fn(): {
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

bind f = fn(flag: bool, x: int) int: {
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
bind f = fn(x: int) int: {
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
bind f = fn(flag: bool, x: int) int: {
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
    a: fn(self),
    b: fn(self) int,
    c: fn(self, cb: cl(int) bool) cl(int),
]

bind f = fn() void: { println("hi") }
bind ho = fn() cl(int) bool: cl(x: int) bool: x > 0
bind nested = fn() cl(int): cl(y: int): println("$y")

bind main = fn(x: int) int: {
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

func TestCoalesceRightAssociative(t *testing.T) {
	assertRightChain := func(t *testing.T, e ast.Expr, wantNames ...string) {
		t.Helper()
		cur := e
		for i := 0; i < len(wantNames)-1; i++ {
			bin, ok := cur.(*ast.BinaryExpr)
			if !ok || bin.Op != token.QuestQuest {
				t.Fatalf("step %d: expected QuestQuest BinaryExpr, got %T", i, cur)
			}
			lid, ok := bin.Left.(*ast.IdentExpr)
			if !ok || lid.Name != wantNames[i] {
				t.Fatalf("step %d: expected left ident %q, got %#v", i, wantNames[i], bin.Left)
			}
			cur = bin.Right
		}
		last := wantNames[len(wantNames)-1]
		if last == "<unwind>" {
			sb, ok := cur.(*ast.StmtBodyExpr)
			if !ok {
				t.Fatalf("expected final StmtBodyExpr unwind, got %T", cur)
			}
			if _, ok := sb.Stmt.(*ast.UnwindStmt); !ok {
				t.Fatalf("expected UnwindStmt inside StmtBodyExpr, got %T", sb.Stmt)
			}
		} else {
			rid, ok := cur.(*ast.IdentExpr)
			if !ok || rid.Name != last {
				t.Fatalf("expected final ident %q, got %#v", last, cur)
			}
		}
	}

	src := `
bind main = fn(): {
    let r1 = a? ?? b? ?? c
    bind r2 = a? ?? b? ?? c
    let r3 = 0
    r3 = a? ?? b? ?? c
    let e1 = id(a? ?? b? ?? c)
    let u1 = a? ?? b? ?? func <- 0
    let u2 = a? ?? b? ?? c? ?? func <- 0
    let l1 = (a? ?? b?) ?? c
    let l2 = (a? ?? b?) ?? func <- 0
}
`
	rep := diag.NewReporter()
	file := parser.ParseFile("coalesce_assoc.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected parse errors:\n%s", rep.Format())
	}
	fn := file.Decls[0].(*ast.VarDecl).Value.(*ast.FuncLit)
	blk := fn.Body.(*ast.BlockExpr)
	if len(blk.Stmts) != 9 {
		t.Fatalf("expected 9 stmts, got %d", len(blk.Stmts))
	}

	r1 := blk.Stmts[0].(*ast.VarStmt)
	if r1.FallbackUnwind != nil {
		t.Fatalf("expected nil FallbackUnwind on r1")
	}
	assertRightChain(t, r1.Value, "a?", "b?", "c")

	r2 := blk.Stmts[1].(*ast.VarStmt)
	if r2.FallbackUnwind != nil {
		t.Fatalf("expected nil FallbackUnwind on r2")
	}
	assertRightChain(t, r2.Value, "a?", "b?", "c")

	r3 := blk.Stmts[3].(*ast.AssignStmt)
	if r3.FallbackUnwind != nil {
		t.Fatalf("expected nil FallbackUnwind on r3")
	}
	assertRightChain(t, r3.Value, "a?", "b?", "c")

	e1 := blk.Stmts[4].(*ast.VarStmt).Value.(*ast.CallExpr)
	assertRightChain(t, e1.Args[0], "a?", "b?", "c")

	u1 := blk.Stmts[5].(*ast.VarStmt)
	if u1.FallbackUnwind != nil {
		t.Fatalf("expected chained unwind u1 to nest right-associatively in Value")
	}
	assertRightChain(t, u1.Value, "a?", "b?", "<unwind>")

	u2 := blk.Stmts[6].(*ast.VarStmt)
	if u2.FallbackUnwind != nil {
		t.Fatalf("expected chained unwind u2 to nest right-associatively in Value")
	}
	assertRightChain(t, u2.Value, "a?", "b?", "c?", "<unwind>")

	l1 := blk.Stmts[7].(*ast.VarStmt).Value.(*ast.BinaryExpr)
	if l1.Op != token.QuestQuest {
		t.Fatalf("expected QuestQuest on l1")
	}
	l1Left, ok := l1.Left.(*ast.BinaryExpr)
	if !ok || l1Left.Op != token.QuestQuest {
		t.Fatalf("expected explicit left-grouped BinaryExpr on l1.Left, got %T", l1.Left)
	}

	l2 := blk.Stmts[8].(*ast.VarStmt)
	if l2.FallbackUnwind == nil {
		t.Fatalf("expected FallbackUnwind on explicit left-grouped (a? ?? b?) ?? func <- 0")
	}
	if l2Left, ok := l2.Value.(*ast.BinaryExpr); !ok || l2Left.Op != token.QuestQuest {
		t.Fatalf("expected explicit left-grouped BinaryExpr on l2.Value, got %T", l2.Value)
	}

	// Verify non-top-level `?? <unwind>` remains rejected.
	badCases := []string{
		"bind f = fn() int: { let x = a? ?? (b? ?? func <- 0); x }\n",
		"bind f = fn() int: { id(a? ?? b? ?? func <- 0) }\n",
	}
	for _, bc := range badCases {
		badRep := diag.NewReporter()
		_ = parser.ParseFile("bad_coalesce_unwind.ll", bc, badRep)
		if !badRep.HasErrors() || !strings.Contains(badRep.Format(), "unwind statement after `??` is only allowed at the top level") {
			t.Fatalf("expected nested unwind error for %q, got:\n%s", bc, badRep.Format())
		}
	}
}

func TestNonAssociativeComparisonsAndRanges(t *testing.T) {
	rejected := []struct {
		name    string
		src     string
		wantMsg string
	}{
		{
			name:    "chained relational lt",
			src:     "bind ok = 1 < 2 < 3\n",
			wantMsg: "chained comparison operators",
		},
		{
			name:    "chained relational mixed",
			src:     "bind ok = 1 <= 2 > 0\n",
			wantMsg: "chained comparison operators",
		},
		{
			name:    "chained relational on right of equality",
			src:     "bind ok = true == 1 < 2 < 3\n",
			wantMsg: "chained comparison operators",
		},
		{
			name:    "chained equality eq",
			src:     "bind ok = 1 == 1 == true\n",
			wantMsg: "chained equality operators",
		},
		{
			name:    "chained equality neq and eq",
			src:     "bind ok = 1 != 2 == true\n",
			wantMsg: "chained equality operators",
		},
		{
			name:    "parenthesized equality followed by unparenthesized third",
			src:     "bind ok = (1 == 1) == true == false\n",
			wantMsg: "chained equality operators",
		},
		{
			name:    "chained range",
			src:     "bind r = 1..2..3\n",
			wantMsg: "chained range operators",
		},
	}

	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			rep := diag.NewReporter()
			_ = parser.ParseFile("non_assoc_neg.ll", tc.src, rep)
			if !rep.HasErrors() || !strings.Contains(rep.Format(), tc.wantMsg) {
				t.Fatalf("expected parse error containing %q for %q, got:\n%s", tc.wantMsg, tc.src, rep.Format())
			}
		})
	}

	valid := []string{
		"bind ok1 = (1 < 2) < 3\n",
		"bind ok2 = 1 < (2 < 3)\n",
		"bind ok3 = (1 == 1) == true\n",
		"bind ok4 = true == (1 == 1)\n",
		"bind ok5 = 1 < 2 == true\n",
		"bind ok6 = 1 < 2 == 3 < 4\n",
		"bind ok7 = 1 < 2 and 2 < 3\n",
		"bind ok8 = (1 == 2) != (3 == 4)\n",
	}
	for _, src := range valid {
		rep := diag.NewReporter()
		_ = parser.ParseFile("non_assoc_pos.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected parse error for %q:\n%s", src, rep.Format())
		}
	}
}

func TestPrivateTypeArgsBeforeDot(t *testing.T) {
	src := `
bind f = fn(mod_arg: int) int: {
    let a = _Private[int].m()
    let b = _Pair[int, string].make(1, "ok")
    let c = mod._Private[int].m()
    let d = _Maybe[int]._None
    let e = _items[idx].len()
    let g = _Private?[int].m()
    let h = match (d):
        when _Maybe[int]._Some(v): v
        default _Maybe[int]._None: 0
    h
}
`
	rep := diag.NewReporter()
	file := parser.ParseFile("private_targs.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected parse errors:\n%s", rep.Format())
	}
	blk := file.Decls[0].(*ast.VarDecl).Value.(*ast.FuncLit).Body.(*ast.BlockExpr)
	if len(blk.Stmts) != 7 {
		t.Fatalf("expected 7 stmts, got %d", len(blk.Stmts))
	}

	assertCallMemberTArgs := func(t *testing.T, stmt ast.Stmt, wantMember string, wantTArgs int) *ast.MemberExpr {
		t.Helper()
		call, ok := stmt.(*ast.VarStmt).Value.(*ast.CallExpr)
		if !ok {
			t.Fatalf("expected CallExpr, got %T", stmt.(*ast.VarStmt).Value)
		}
		mem, ok := call.Callee.(*ast.MemberExpr)
		if !ok {
			t.Fatalf("expected MemberExpr callee, got %T", call.Callee)
		}
		if mem.Member != wantMember {
			t.Fatalf("expected member %q, got %q", wantMember, mem.Member)
		}
		if len(mem.BaseTypeArgs) != wantTArgs {
			t.Fatalf("expected %d BaseTypeArgs on %q, got %d", wantTArgs, wantMember, len(mem.BaseTypeArgs))
		}
		return mem
	}

	memA := assertCallMemberTArgs(t, blk.Stmts[0], "m", 1)
	if id, ok := memA.Base.(*ast.IdentExpr); !ok || id.Name != "_Private" {
		t.Fatalf("expected _Private base, got %#v", memA.Base)
	}

	memB := assertCallMemberTArgs(t, blk.Stmts[1], "make", 2)
	if id, ok := memB.Base.(*ast.IdentExpr); !ok || id.Name != "_Pair" {
		t.Fatalf("expected _Pair base, got %#v", memB.Base)
	}

	memC := assertCallMemberTArgs(t, blk.Stmts[2], "m", 1)
	if qBase, ok := memC.Base.(*ast.MemberExpr); !ok || qBase.Member != "_Private" {
		t.Fatalf("expected mod._Private base, got %#v", memC.Base)
	}

	memD, ok := blk.Stmts[3].(*ast.VarStmt).Value.(*ast.MemberExpr)
	if !ok || memD.Member != "_None" || len(memD.BaseTypeArgs) != 1 {
		t.Fatalf("expected _Maybe[int]._None MemberExpr with 1 BaseTypeArg, got %#v", blk.Stmts[3].(*ast.VarStmt).Value)
	}

	// Lowercase `_`-prefixed variable and optional ident must still parse as indexing.
	memE := assertCallMemberTArgs(t, blk.Stmts[4], "len", 0)
	if _, ok := memE.Base.(*ast.IndexExpr); !ok {
		t.Fatalf("expected _items[idx] to remain IndexExpr, got %T", memE.Base)
	}
	memG := assertCallMemberTArgs(t, blk.Stmts[5], "m", 0)
	if _, ok := memG.Base.(*ast.IndexExpr); !ok {
		t.Fatalf("expected _Private?[int] to remain IndexExpr, got %T", memG.Base)
	}

	mExpr, ok := blk.Stmts[6].(*ast.VarStmt).Value.(*ast.MatchExpr)
	if !ok || len(mExpr.Arms) != 2 {
		t.Fatalf("expected 2 match arms, got %#v", blk.Stmts[6].(*ast.VarStmt).Value)
	}
	vp, ok := mExpr.Arms[0].Cases[0].Patterns[0].(*ast.VariantPattern)
	if !ok {
		t.Fatalf("expected VariantPattern in arm 0, got %T", mExpr.Arms[0].Cases[0].Patterns[0])
	}
	vpMem, ok := vp.Target.(*ast.MemberExpr)
	if !ok || vpMem.Member != "_Some" || len(vpMem.BaseTypeArgs) != 1 {
		t.Fatalf("expected _Maybe[int]._Some VariantPattern target, got %#v", vp.Target)
	}
	ep, ok := mExpr.Arms[1].Cases[0].Patterns[0].(*ast.ExprPattern)
	if !ok {
		t.Fatalf("expected ExprPattern in arm 1, got %T", mExpr.Arms[1].Cases[0].Patterns[0])
	}
	epMem, ok := ep.Expr.(*ast.MemberExpr)
	if !ok || epMem.Member != "_None" || len(epMem.BaseTypeArgs) != 1 {
		t.Fatalf("expected _Maybe[int]._None ExprPattern target, got %#v", ep.Expr)
	}
}

func TestQualifiedAndConstExprStackArrayTypes(t *testing.T) {
	src := `
bind wrap[T] = fn(v: T) T: v

bind test = fn(
    a: [mod.C: int],
    b: [1 + 2: int],
    c: [(1 + 2) * mod.C: ?string],
    d: [1 << 3: [2 + 2: u8]],
    e: [[mod.C: int]],
    f: [#[string: int]],
    g: [cl(x: int, y: [1 + 1: int]) [mod.C: bool]],
): {
    let local_a: [mod.C + 1: int] = [1, 2, 3, 4]
    let _ = wrap[[1 + 2: int]](local_a)
}
`
	rep := diag.NewReporter()
	file := parser.ParseFile("arr_const_size.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected parse errors:\n%s", rep.Format())
	}

	var fn *ast.FuncLit
	for _, d := range file.Decls {
		if vd, ok := d.(*ast.VarDecl); ok && vd.Name == "test" {
			fn = vd.Value.(*ast.FuncLit)
		}
	}
	if fn == nil || len(fn.Params) != 7 {
		t.Fatalf("expected test fn with 7 params, got %#v", fn)
	}

	if sa, ok := fn.Params[0].Type.(*ast.StackArrayType); !ok {
		t.Fatalf("param a: expected StackArrayType, got %T", fn.Params[0].Type)
	} else if _, ok := sa.Size.(*ast.MemberExpr); !ok {
		t.Fatalf("param a.Size: expected MemberExpr, got %T", sa.Size)
	}

	if sa, ok := fn.Params[1].Type.(*ast.StackArrayType); !ok {
		t.Fatalf("param b: expected StackArrayType, got %T", fn.Params[1].Type)
	} else if _, ok := sa.Size.(*ast.BinaryExpr); !ok {
		t.Fatalf("param b.Size: expected BinaryExpr, got %T", sa.Size)
	}

	if sa, ok := fn.Params[2].Type.(*ast.StackArrayType); !ok {
		t.Fatalf("param c: expected StackArrayType, got %T", fn.Params[2].Type)
	} else if _, ok := sa.Size.(*ast.BinaryExpr); !ok {
		t.Fatalf("param c.Size: expected BinaryExpr, got %T", sa.Size)
	} else if _, ok := sa.Elem.(*ast.OptionalType); !ok {
		t.Fatalf("param c.Elem: expected OptionalType, got %T", sa.Elem)
	}

	if sa, ok := fn.Params[3].Type.(*ast.StackArrayType); !ok {
		t.Fatalf("param d: expected StackArrayType, got %T", fn.Params[3].Type)
	} else if inner, ok := sa.Elem.(*ast.StackArrayType); !ok {
		t.Fatalf("param d.Elem: expected nested StackArrayType, got %T", sa.Elem)
	} else if _, ok := inner.Size.(*ast.BinaryExpr); !ok {
		t.Fatalf("param d.Elem.Size: expected BinaryExpr, got %T", inner.Size)
	}

	if vec, ok := fn.Params[4].Type.(*ast.VectorType); !ok {
		t.Fatalf("param e: expected VectorType, got %T", fn.Params[4].Type)
	} else if _, ok := vec.Elem.(*ast.StackArrayType); !ok {
		t.Fatalf("param e.Elem: expected StackArrayType, got %T", vec.Elem)
	}

	if vec, ok := fn.Params[5].Type.(*ast.VectorType); !ok {
		t.Fatalf("param f: expected VectorType, got %T", fn.Params[5].Type)
	} else if _, ok := vec.Elem.(*ast.MapType); !ok {
		t.Fatalf("param f.Elem: expected MapType, got %T", vec.Elem)
	}

	if vec, ok := fn.Params[6].Type.(*ast.VectorType); !ok {
		t.Fatalf("param g: expected VectorType, got %T", fn.Params[6].Type)
	} else if ft, ok := vec.Elem.(*ast.FuncType); !ok {
		t.Fatalf("param g.Elem: expected FuncType, got %T", vec.Elem)
	} else if _, ok := ft.Ret.(*ast.StackArrayType); !ok {
		t.Fatalf("param g.Elem.Ret: expected StackArrayType, got %T", ft.Ret)
	}
}

func TestDisambiguateArrayRepeatAndMapLiteralsWithBlocks(t *testing.T) {
	src := `
bind test = fn(k: string, k1: string, k2: string) void: {
    let arr_repeat = [N: { 42 }]
    let arr_expr_count = [1 + N: { let x = 2; x + 1 }]
    let arr_multi = [N: { 1 }, { 2 }]
    let arr_nested_label = [N: { blk: { blk <- 7 } }]
    let map_single = #[k: { 10 }]
    let map_both_blocks = #[k1: { 10 }, k2: { 20 }]
    let map_second_block = #[k1: 10, k2: { 20 }]
    let map_expr_key = #[1 + k_num: { 30 }]
    let map_for_val = #[k: for (x in [1, 2]): x]
    let vec_paren_label = [(blk: { blk <- 11 })]
    let set_paren_label = #[(blk: { blk <- 22 })]
    let vec_second_label = [1, blk: { blk <- 33 }]
    let set_second_label = #[1, blk: { blk <- 44 }]
    let vec_call_arg_label = [foo(blk: { blk <- 55 })]
    let vec_nested_map = [#[k: { 66 }]]
    let vec_block_let_label = [{ let t = blk: { blk <- 77 }; t }]
    let standalone_paren_block = (blk: { blk <- 88 })
    let standalone_paren_for = (outer: for (x in [1, 2]) int: { outer <- 99 })
}
`
	rep := diag.NewReporter()
	file := parser.ParseFile("disambiguate_lit_blocks.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected parse errors:\n%s", rep.Format())
	}

	fn := file.Decls[0].(*ast.VarDecl).Value.(*ast.FuncLit)
	body := fn.Body.(*ast.BlockExpr)
	vars := make(map[string]ast.Expr, len(body.Stmts))
	for _, st := range body.Stmts {
		vs := st.(*ast.VarStmt)
		vars[vs.Name] = vs.Value
	}

	arr1, ok := vars["arr_repeat"].(*ast.ArrayLit)
	if !ok || len(arr1.Elems) != 1 {
		t.Fatalf("arr_repeat: expected 1-elem ArrayLit, got %#v", vars["arr_repeat"])
	}
	if id, ok := arr1.Count.(*ast.IdentExpr); !ok || id.Name != "N" {
		t.Fatalf("arr_repeat.Count: expected IdentExpr N, got %#v", arr1.Count)
	}
	if blk, ok := arr1.Elems[0].(*ast.BlockExpr); !ok || blk.Label != "" {
		t.Fatalf("arr_repeat.Elems[0]: expected unlabeled BlockExpr, got %#v", arr1.Elems[0])
	}

	arrExpr, ok := vars["arr_expr_count"].(*ast.ArrayLit)
	if !ok || len(arrExpr.Elems) != 1 {
		t.Fatalf("arr_expr_count: expected ArrayLit, got %#v", vars["arr_expr_count"])
	}
	if _, ok := arrExpr.Count.(*ast.BinaryExpr); !ok {
		t.Fatalf("arr_expr_count.Count: expected BinaryExpr, got %#v", arrExpr.Count)
	}
	if _, ok := arrExpr.Elems[0].(*ast.BlockExpr); !ok {
		t.Fatalf("arr_expr_count.Elems[0]: expected BlockExpr, got %#v", arrExpr.Elems[0])
	}

	arrMulti, ok := vars["arr_multi"].(*ast.ArrayLit)
	if !ok || len(arrMulti.Elems) != 2 {
		t.Fatalf("arr_multi: expected 2-elem ArrayLit, got %#v", vars["arr_multi"])
	}

	arrNested, ok := vars["arr_nested_label"].(*ast.ArrayLit)
	if !ok || len(arrNested.Elems) != 1 {
		t.Fatalf("arr_nested_label: expected ArrayLit, got %#v", vars["arr_nested_label"])
	}
	outerBlk := arrNested.Elems[0].(*ast.BlockExpr)
	innerBlk, ok := outerBlk.Tail.(*ast.BlockExpr)
	if !ok || innerBlk.Label != "blk" {
		t.Fatalf("arr_nested_label inner tail: expected BlockExpr labeled 'blk', got %#v", outerBlk.Tail)
	}

	for _, name := range []string{"map_single", "map_expr_key", "map_for_val"} {
		m, ok := vars[name].(*ast.MapLit)
		if !ok || len(m.Entries) != 1 {
			t.Fatalf("%s: expected 1-entry MapLit, got %#v", name, vars[name])
		}
	}
	if _, ok := vars["map_for_val"].(*ast.MapLit).Entries[0].Value.(*ast.ForExpr); !ok {
		t.Fatalf("map_for_val value: expected ForExpr, got %#v", vars["map_for_val"].(*ast.MapLit).Entries[0].Value)
	}

	for _, name := range []string{"map_both_blocks", "map_second_block"} {
		m, ok := vars[name].(*ast.MapLit)
		if !ok || len(m.Entries) != 2 {
			t.Fatalf("%s: expected 2-entry MapLit, got %#v", name, vars[name])
		}
		if _, ok := m.Entries[1].Value.(*ast.BlockExpr); !ok {
			t.Fatalf("%s entry 1 value: expected BlockExpr, got %#v", name, m.Entries[1].Value)
		}
	}

	checkLabeledBlockElem := func(expr ast.Expr, isSet bool, idx int, name string) {
		var elem ast.Expr
		if isSet {
			s, ok := expr.(*ast.SetLit)
			if !ok || idx >= len(s.Elems) {
				t.Fatalf("%s: expected SetLit with idx %d, got %#v", name, idx, expr)
			}
			elem = s.Elems[idx]
		} else {
			v, ok := expr.(*ast.VecLit)
			if !ok || idx >= len(v.Elems) {
				t.Fatalf("%s: expected VecLit with idx %d, got %#v", name, idx, expr)
			}
			elem = v.Elems[idx]
		}
		blk, ok := elem.(*ast.BlockExpr)
		if !ok || blk.Label != "blk" {
			t.Fatalf("%s elem %d: expected BlockExpr labeled 'blk', got %#v", name, idx, elem)
		}
	}

	checkLabeledBlockElem(vars["vec_paren_label"], false, 0, "vec_paren_label")
	checkLabeledBlockElem(vars["set_paren_label"], true, 0, "set_paren_label")
	checkLabeledBlockElem(vars["vec_second_label"], false, 1, "vec_second_label")
	checkLabeledBlockElem(vars["set_second_label"], true, 1, "set_second_label")

	callVec := vars["vec_call_arg_label"].(*ast.VecLit)
	if blk, ok := callVec.Elems[0].(*ast.CallExpr).Args[0].(*ast.BlockExpr); !ok || blk.Label != "blk" {
		t.Fatalf("vec_call_arg_label arg 0: expected BlockExpr labeled 'blk', got %#v", callVec.Elems[0])
	}

	nestedMapVec := vars["vec_nested_map"].(*ast.VecLit)
	if innerMap, ok := nestedMapVec.Elems[0].(*ast.MapLit); !ok || len(innerMap.Entries) != 1 {
		t.Fatalf("vec_nested_map: expected VecLit containing 1-entry MapLit, got %#v", nestedMapVec.Elems[0])
	}

	blockLetVec := vars["vec_block_let_label"].(*ast.VecLit)
	blk0 := blockLetVec.Elems[0].(*ast.BlockExpr)
	letSt := blk0.Stmts[0].(*ast.VarStmt)
	if innerBlk, ok := letSt.Value.(*ast.BlockExpr); !ok || innerBlk.Label != "blk" {
		t.Fatalf("vec_block_let_label let value: expected BlockExpr labeled 'blk', got %#v", letSt.Value)
	}

	if sb, ok := vars["standalone_paren_block"].(*ast.BlockExpr); !ok || sb.Label != "blk" {
		t.Fatalf("standalone_paren_block: expected BlockExpr labeled 'blk', got %#v", vars["standalone_paren_block"])
	}
	if sf, ok := vars["standalone_paren_for"].(*ast.ForExpr); !ok || sf.Label != "outer" {
		t.Fatalf("standalone_paren_for: expected ForExpr labeled 'outer', got %#v", vars["standalone_paren_for"])
	}
}

func TestSingleItemMultilineTrailingCommaOptionalAndMultiItemRequired(t *testing.T) {
	okSrc := `
define OneField struct [
	x: int
]

define OneVariant enum [
	Only
]

define OneMethod interface [
	show: fn(
		self
	) string
]

bind single_param = fn(
	x: List[
		int
	]
) +[
	int
]: {
	let v = [
		1
	]
	let a = [1:
		2
	]
	let m = #[
		"k": 1
	]
	let s = #[
		"only"
	]
	let tp = +[
		1
	]
	let lit = OneField[
		x: 42
	]
	let _ = println(
		"jsoned: ${match(v.len()):
			when 1: "ok"
			default: "err"}"
	)
	match(
		v.len()
	):
		when 1: +[v[0] + a[0] + m.len() + s.len() + tp.0 + lit.x]
		default: +[0]
}
`
	rep := diag.NewReporter()
	file := parser.ParseFile("single_multiline_ok.ll", okSrc, rep)
	if rep.HasErrors() || file == nil {
		t.Fatalf("unexpected parse errors for single-item multiline constructs:\n%s", rep.Format())
	}

	// Trailing comma on a 2-item multiline argument list is still required.
	badCallSrc := `
bind main = fn(): {
	println(
		"a",
		"b"
	)
}
`
	rep2 := diag.NewReporter()
	_ = parser.ParseFile("bad_multi_call.ll", badCallSrc, rep2)
	if !rep2.HasErrors() || !strings.Contains(rep2.Format(), "missing trailing comma") {
		t.Fatalf("expected missing trailing comma error for 2-arg multiline call, got:\n%s", rep2.Format())
	}
}

func TestComptimeAndConstSyntax(t *testing.T) {
	src := `
comptime if (LINUX): {
	const SEP = '/'
	extern "unistd.h"
} else comptime if (MACOS): {
	const SEP = '/'
} else: {
	const SEP = '\\'
}

comptime if (DEBUG): const LOG_LEVEL = 3
else: const LOG_LEVEL = 1

const N = 4

define Matrix[T, R: const int, C: const int] struct [
	data: [R: [C: T]],
]

const Matrix[T, R: const int, C: const int].zeros = fn() Matrix[T, R, C]: {
	let m = Matrix[T, R, C][data: comptime make_zero()]
	m
}

const double_arr[N: const int] = fn(arr: [N: int]) [N: int]: {
	const LOCAL_FACTOR = 2
	comptime for (i in 0..N) [N: int]: arr[i] * LOCAL_FACTOR
}

const demo = fn(v: [int]) int: {
	let sliced = v[0..2].len()
	let z = zeros[8]()
	let x = comptime 1 + 2
	let y = comptime {
		const A = 10
		A + x
	}
	let picked = comptime if (N > 2): 100
	else comptime if (N == 2): 50
	else: 0
	let labeled = outer: comptime for (i in 0..3): {
		if (i == 1): next outer <-
	}
	let matched = comptime match (N):
		when 4: 40
		default: 0
	let m: Matrix[int, 2 + 2, 4] = Matrix[int, 2 + 2, 4][data: z]
	sliced + picked + matched + y + m.data.len()
}
`
	rep := diag.NewReporter()
	file := parser.ParseFile("comptime_syntax.ll", src, rep)
	if rep.HasErrors() || file == nil {
		t.Fatalf("unexpected parse errors:\n%s", rep.Format())
	}

	// Verify top-level comptime if decls & const decls
	topIf, ok := file.Decls[0].(*ast.TopComptimeIfDecl)
	if !ok || !topIf.ThenBraced || topIf.ElseIf == nil || topIf.ElseIf.ElseDecls == nil && topIf.ElseIf.ElseIf == nil && !topIf.ElseIf.HasElse {
		t.Fatalf("expected chained TopComptimeIfDecl with else branch, got %#v", file.Decls[0])
	}
	if len(topIf.ThenDecls) != 2 {
		t.Fatalf("expected 2 decls inside first comptime if branch, got %d", len(topIf.ThenDecls))
	}
	if vd, ok := topIf.ThenDecls[0].(*ast.VarDecl); !ok || !vd.IsConst {
		t.Fatalf("expected const VarDecl inside TopComptimeIfDecl, got %#v", topIf.ThenDecls[0])
	}

	singleTopIf, ok := file.Decls[1].(*ast.TopComptimeIfDecl)
	if !ok || singleTopIf.ThenBraced || !singleTopIf.HasElse || singleTopIf.ElseBraced {
		t.Fatalf("expected unbraced TopComptimeIfDecl with else, got %#v", file.Decls[1])
	}

	matDef := file.Decls[3].(*ast.StructDecl)
	if len(matDef.TypeParams) != 3 || matDef.TypeParams[0].IsConst || !matDef.TypeParams[1].IsConst || matDef.TypeParams[1].ConstType == nil {
		t.Fatalf("unexpected Matrix type params: %#v", matDef.TypeParams)
	}

	matMethod := file.Decls[4].(*ast.MethodDecl)
	if !matMethod.IsConst || len(matMethod.RecvTypeParams) != 3 {
		t.Fatalf("expected const MethodDecl with 3 recv params, got %#v", matMethod)
	}

	demoDecl := file.Decls[6].(*ast.VarDecl)
	if !demoDecl.IsConst {
		t.Fatalf("expected const VarDecl for demo")
	}
	demoFn := demoDecl.Value.(*ast.FuncLit)
	blk := demoFn.Body.(*ast.BlockExpr)

	// Verify `v[0..2].len()` stayed a SliceExpr (not ConstArgType)
	slicedStmt := blk.Stmts[0].(*ast.VarStmt)
	lenCall := slicedStmt.Value.(*ast.CallExpr)
	lenMem := lenCall.Callee.(*ast.MemberExpr)
	if _, ok := lenMem.Base.(*ast.SliceExpr); !ok {
		t.Fatalf("expected v[0..2] to remain SliceExpr, got %#v", lenMem.Base)
	}

	// Verify `zeros[8]()` parsed ConstArgType
	zStmt := blk.Stmts[1].(*ast.VarStmt)
	zCall := zStmt.Value.(*ast.CallExpr)
	if len(zCall.TypeArgs) != 1 {
		t.Fatalf("expected 1 generic arg on zeros[8](), got %d", len(zCall.TypeArgs))
	}
	if _, ok := zCall.TypeArgs[0].(*ast.ConstArgType); !ok {
		t.Fatalf("expected ConstArgType for zeros[8](), got %#v", zCall.TypeArgs[0])
	}

	// Verify `comptime 1 + 2` wraps `1` (unary precedence, not `comptime (1 + 2)` unless parenthesized)
	xStmt := blk.Stmts[2].(*ast.VarStmt)
	if bin, ok := xStmt.Value.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected BinaryExpr (comptime 1) + 2, got %#v", xStmt.Value)
	} else if _, ok := bin.Left.(*ast.ComptimeExpr); !ok {
		t.Fatalf("expected ComptimeExpr on left of +, got %#v", bin.Left)
	}

	// Verify `comptime { ... }`
	yStmt := blk.Stmts[3].(*ast.VarStmt)
	if ct, ok := yStmt.Value.(*ast.ComptimeExpr); !ok {
		t.Fatalf("expected ComptimeExpr for comptime { ... }, got %#v", yStmt.Value)
	} else if cblk, ok := ct.Expr.(*ast.BlockExpr); !ok {
		t.Fatalf("expected BlockExpr inside comptime { ... }, got %#v", ct.Expr)
	} else if vs, ok := cblk.Stmts[0].(*ast.VarStmt); !ok || !vs.IsConst {
		t.Fatalf("expected local const VarStmt inside block, got %#v", cblk.Stmts[0])
	}

	// Verify `comptime if` / `else comptime if`
	pStmt := blk.Stmts[4].(*ast.VarStmt)
	if ctIf, ok := pStmt.Value.(*ast.IfExpr); !ok || !ctIf.IsComptime {
		t.Fatalf("expected comptime IfExpr, got %#v", pStmt.Value)
	} else if elseIf, ok := ctIf.Else.(*ast.IfExpr); !ok || !elseIf.IsComptime {
		t.Fatalf("expected comptime else-if IfExpr, got %#v", ctIf.Else)
	}

	// Verify labeled `outer: comptime for`
	lStmt := blk.Stmts[5].(*ast.VarStmt)
	if ctFor, ok := lStmt.Value.(*ast.ForExpr); !ok || !ctFor.IsComptime || ctFor.Label != "outer" {
		t.Fatalf("expected labeled comptime ForExpr, got %#v", lStmt.Value)
	}

	// Verify `comptime match`
	mStmt := blk.Stmts[6].(*ast.VarStmt)
	if ctMatch, ok := mStmt.Value.(*ast.MatchExpr); !ok || !ctMatch.IsComptime {
		t.Fatalf("expected comptime MatchExpr, got %#v", mStmt.Value)
	}

	// Verify `Matrix[int, 2 + 2, 4]` type + struct lit generic args
	matStmt := blk.Stmts[7].(*ast.VarStmt)
	matNT := matStmt.Type.(*ast.NamedType)
	if len(matNT.TypeArgs) != 3 {
		t.Fatalf("expected 3 args on Matrix[int, 2 + 2, 4], got %d", len(matNT.TypeArgs))
	}
	if _, ok := matNT.TypeArgs[1].(*ast.ConstArgType); !ok {
		t.Fatalf("expected ConstArgType for `2 + 2`, got %#v", matNT.TypeArgs[1])
	}
	matLit := matStmt.Value.(*ast.StructLit)
	if len(matLit.TypeArgs) != 3 {
		t.Fatalf("expected 3 generic args on Matrix[...] struct lit, got %d", len(matLit.TypeArgs))
	}
	if _, ok := matLit.TypeArgs[1].(*ast.ConstArgType); !ok {
		t.Fatalf("expected ConstArgType on struct lit arg 1, got %#v", matLit.TypeArgs[1])
	}

	// Negative test: `comptime if (...) ... else if (...)` must error with hint
	badChain := "const f = fn() int: comptime if (true): 1 else if (false): 2 else: 3\n"
	badRep := diag.NewReporter()
	_ = parser.ParseFile("bad_comptime_if.ll", badChain, badRep)
	if !badRep.HasErrors() || !strings.Contains(badRep.Format(), "else comptime if") {
		t.Fatalf("expected `else comptime if` diagnostic, got:\n%s", badRep.Format())
	}
}

func TestParseDottedAndFieldVariantAttributes(t *testing.T) {
	src := `
@derivable(
    eq: derive.eq,
    cmp: mod.custom_cmp,
)
define MyIface interface [
    eq: fn(self, other: Self) bool,
    cmp: fn(self, other: Self) int,
]

@table("users")
define User struct [
    // Primary id
    @json("user_id")
    id: int = 0,
    @skip
    secret?: ?string,
]

define State enum [
    // Initial state
    @tag("INIT")
    Idle,
    @renamed("BUSY")
    Busy: int,
]
`
	rep := diag.NewReporter()
	f := parser.ParseFile("attrs_fields.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected parse errors:\n%s", rep.Format())
	}
	if len(f.Decls) != 3 {
		t.Fatalf("expected 3 decls, got %d", len(f.Decls))
	}
	ifc := f.Decls[0].(*ast.InterfaceDecl)
	if len(ifc.Attributes) != 1 || ifc.Attributes[0].Name != "derivable" {
		t.Fatalf("unexpected interface attrs: %+v", ifc.Attributes)
	}
	if len(ifc.Attributes[0].Args) != 2 || ifc.Attributes[0].Args[0] != "eq: derive.eq" || ifc.Attributes[0].Args[1] != "cmp: mod.custom_cmp" {
		t.Fatalf("unexpected dotted attribute args: %#v", ifc.Attributes[0].Args)
	}
	sd := f.Decls[1].(*ast.StructDecl)
	if len(sd.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sd.Fields))
	}
	if len(sd.Fields[0].Attributes) != 1 || sd.Fields[0].Attributes[0].Name != "json" || sd.Fields[0].Attributes[0].Args[0] != "user_id" {
		t.Fatalf("unexpected field[0] attrs: %+v", sd.Fields[0].Attributes)
	}
	if sd.Fields[0].Doc != "Primary id" {
		t.Fatalf("expected doc comment attached before field attribute, got %q", sd.Fields[0].Doc)
	}
	if len(sd.Fields[1].Attributes) != 1 || sd.Fields[1].Attributes[0].Name != "skip" {
		t.Fatalf("unexpected field[1] attrs: %+v", sd.Fields[1].Attributes)
	}
	ed := f.Decls[2].(*ast.EnumDecl)
	if len(ed.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(ed.Variants))
	}
	if ed.Variants[0].Doc != "Initial state" {
		t.Fatalf("expected doc comment attached before variant attribute, got %q", ed.Variants[0].Doc)
	}
	if len(ed.Variants[0].Attributes) != 1 || ed.Variants[0].Attributes[0].Name != "tag" {
		t.Fatalf("unexpected variant[0] attrs: %+v", ed.Variants[0].Attributes)
	}
	if len(ed.Variants[1].Attributes) != 1 || ed.Variants[1].Attributes[0].Name != "renamed" {
		t.Fatalf("unexpected variant[1] attrs: %+v", ed.Variants[1].Attributes)
	}
}
