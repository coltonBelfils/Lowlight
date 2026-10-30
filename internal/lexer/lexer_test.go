package lexer_test

import (
	"testing"

	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/token"
)

func TestLexerUnwindVsLessThanNeg(t *testing.T) {
	src := `
if (x < -5) {
    <- 10
}
if (x<-5) {
    func <-
}
`
	rep := diag.NewReporter()
	toks := lexer.Lex("test.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
	}

	var kinds []token.Kind
	for _, tk := range toks {
		kinds = append(kinds, tk.Kind)
	}

	// Verify first `if (x < -5)` lexes Lt then Minus, while `<- 10` lexes Unwind
	foundLtMinus := false
	unwindCount := 0
	for i := 0; i < len(kinds); i++ {
		if i+1 < len(kinds) && kinds[i] == token.Lt && kinds[i+1] == token.Minus {
			foundLtMinus = true
		}
		if kinds[i] == token.Unwind {
			unwindCount++
		}
	}
	if !foundLtMinus {
		t.Fatalf("expected Lt followed by Minus for `x < -5` / `x<-5`")
	}
	if unwindCount != 2 {
		t.Fatalf("expected 2 Unwind tokens, got %d", unwindCount)
	}
}

func TestLexerStringInterpolationAndOptIdent(t *testing.T) {
	src := `let user? = find_user(id)
let s = "Are you $ready? User: ${user?.name ?? "Guest"}"
`
	rep := diag.NewReporter()
	toks := lexer.Lex("interp.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
	}

	// Verify `user?` is OptIdent, `$ready?` splits into Ident("ready") + "? User: "
	sawOptUser := false
	sawReadyPlain := false
	sawQuestionLiteral := false
	for _, tk := range toks {
		if tk.Kind == token.OptIdent && tk.Text == "user?" {
			sawOptUser = true
		}
		if tk.Kind == token.Ident && tk.Text == "ready" {
			sawReadyPlain = true
		}
		if tk.Kind == token.InterpMid && tk.Text == "? User: " {
			sawQuestionLiteral = true
		}
	}
	if !sawOptUser {
		t.Errorf("expected OptIdent `user?`")
	}
	if !sawReadyPlain {
		t.Errorf("expected `$ready?` to lex `ready` as plain Ident")
	}
	if !sawQuestionLiteral {
		t.Errorf("expected `?` after `$ready` to remain literal text `? User: `")
	}
}

func TestSemicolonInsertionContinuationRules(t *testing.T) {
	src := `let x = if (c == 3) 4
else 3
let m = match (code)
    when 200: "OK"
    else: "ERR"
let chain = items
    .copy()
`
	rep := diag.NewReporter()
	toks := lexer.Lex("semi.ll", src, rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
	}

	semiCount := 0
	for _, tk := range toks {
		if tk.Kind == token.Semi {
			semiCount++
		}
	}
	// Exactly 3 top-level statements -> 3 semicolons!
	if semiCount != 3 {
		t.Fatalf("expected 3 semicolons for 3 multiline statements, got %d: %+v", semiCount, toks)
	}
}
