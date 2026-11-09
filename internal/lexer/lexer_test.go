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

func TestLexerBacktickStrings(t *testing.T) {
	t.Run("basic and multiline", func(t *testing.T) {
		src := "let s = `line 1\nline 2\nline 3`\n"
		rep := diag.NewReporter()
		toks := lexer.Lex("test.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
		}
		var strTok *token.Token
		for i := range toks {
			if toks[i].Kind == token.StringLit {
				strTok = &toks[i]
				break
			}
		}
		if strTok == nil {
			t.Fatalf("expected StringLit token, got: %+v", toks)
		}
		expected := "line 1\nline 2\nline 3"
		if strTok.Text != expected {
			t.Errorf("expected text %q, got %q", expected, strTok.Text)
		}
	})

	t.Run("unescaped quotes and escaped backticks", func(t *testing.T) {
		src := "let json = `{\"name\": 'Alice', \\`quoted\\`: 42}`\n"
		rep := diag.NewReporter()
		toks := lexer.Lex("test.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
		}
		var strTok *token.Token
		for i := range toks {
			if toks[i].Kind == token.StringLit {
				strTok = &toks[i]
				break
			}
		}
		if strTok == nil {
			t.Fatalf("expected StringLit token, got: %+v", toks)
		}
		expected := `{"name": 'Alice', ` + "`quoted`" + `: 42}`
		if strTok.Text != expected {
			t.Errorf("expected text %q, got %q", expected, strTok.Text)
		}
	})

	t.Run("escapes and literal backslashes", func(t *testing.T) {
		src := "let s = `\\n\\t\\0\\$\\`\\\\\\x41\\u{1F600}\\d+\\s+`\n"
		rep := diag.NewReporter()
		toks := lexer.Lex("test.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
		}
		var strTok *token.Token
		for i := range toks {
			if toks[i].Kind == token.StringLit {
				strTok = &toks[i]
				break
			}
		}
		if strTok == nil {
			t.Fatalf("expected StringLit token, got: %+v", toks)
		}
		expected := "\n\t\x00$`\\A\U0001F600\\d+\\s+"
		if strTok.Text != expected {
			t.Errorf("expected text %q, got %q", expected, strTok.Text)
		}
	})

	t.Run("crlf normalization", func(t *testing.T) {
		src := "let s = `line 1\r\nline 2\r\nline 3`\n"
		rep := diag.NewReporter()
		toks := lexer.Lex("test.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
		}
		var strTok *token.Token
		for i := range toks {
			if toks[i].Kind == token.StringLit {
				strTok = &toks[i]
				break
			}
		}
		if strTok == nil {
			t.Fatalf("expected StringLit token, got: %+v", toks)
		}
		expected := "line 1\nline 2\nline 3"
		if strTok.Text != expected {
			t.Errorf("expected CRLF to be normalized to LF %q, got %q", expected, strTok.Text)
		}
	})

	t.Run("interpolation with ident and expr", func(t *testing.T) {
		src := "`Hello, $name! 1 + 2 = ${1 + 2} done`"
		rep := diag.NewReporter()
		toks := lexer.Lex("test.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
		}
		var kinds []token.Kind
		var texts []string
		for _, tk := range toks {
			if tk.Kind != token.EOF && tk.Kind != token.Semi {
				kinds = append(kinds, tk.Kind)
				texts = append(texts, tk.Text)
			}
		}
		// Expect: InterpStart("Hello, "), Ident("name"), InterpMid("! 1 + 2 = "), IntLit("1"), Plus("+"), IntLit("2"), InterpEnd(" done")
		if len(kinds) != 7 {
			t.Fatalf("unexpected token sequence: kinds=%v texts=%v", kinds, texts)
		}
		if texts[0] != "Hello, " || texts[1] != "name" || texts[2] != "! 1 + 2 = " || texts[6] != " done" {
			t.Errorf("unexpected texts: %v", texts)
		}
	})

	t.Run("nested interpolation backtick inside double quote and vice versa", func(t *testing.T) {
		src := "let s = `outer ${\"double \" + `${10}`} end`\n"
		rep := diag.NewReporter()
		toks := lexer.Lex("test.ll", src, rep)
		if rep.HasErrors() {
			t.Fatalf("unexpected lexer errors:\n%s", rep.Format())
		}
		var interpParts []string
		for _, tk := range toks {
			if tk.Kind == token.InterpStart || tk.Kind == token.InterpMid || tk.Kind == token.InterpEnd || tk.Kind == token.StringLit {
				interpParts = append(interpParts, tk.Text)
			}
		}
		expectedParts := []string{"outer ", "double ", "", "10", "", " end"}
		// outer InterpStart: "outer "
		// "double " StringLit: "double "
		// inner InterpStart: ""
		// inner InterpEnd: ""
		// outer InterpEnd: " end"
		if len(interpParts) != 5 {
			t.Fatalf("expected 5 string parts, got %d: %v", len(interpParts), interpParts)
		}
		if interpParts[0] != "outer " || interpParts[1] != "double " || interpParts[4] != " end" {
			t.Errorf("unexpected string parts: %v vs %v", interpParts, expectedParts)
		}
	})

	t.Run("bare dollar gives error with hint", func(t *testing.T) {
		src := "let s = `cost is $100`\n"
		rep := diag.NewReporter()
		lexer.Lex("test.ll", src, rep)
		if !rep.HasErrors() {
			t.Fatalf("expected error for unescaped bare $ in backtick string")
		}
	})
}
