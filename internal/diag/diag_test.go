package diag_test

import (
	"strings"
	"testing"

	"lowlight/internal/diag"
)

func TestFormatUTF8AndTabColumnAlignment(t *testing.T) {
	// "é" is 2 bytes in UTF-8, 1 rune; "\t" expands to 4 spaces.
	src := "let x = \"é\" + \tbad"
	rep := diag.NewReporter()
	rep.AddSource("utf8.ll", src)
	// "bad" starts at 1-based rune column 16 (len("let x = \"é\" + \t") = 15 runes) and ends at 19.
	rep.Errorf(diag.Span{
		Start: diag.Pos{File: "utf8.ll", Line: 1, Col: 16},
		End:   diag.Pos{File: "utf8.ll", Line: 1, Col: 19},
	}, "bad token")

	out := rep.Format()
	// Expected expanded line: "  1 | let x = "é" +     bad"
	// Visual offset before "bad": 14 runes + 4 spaces for tab = 18 spaces.
	wantCaret := "       | " + strings.Repeat(" ", 18) + "^~~"
	if !strings.Contains(out, wantCaret) {
		t.Fatalf("caret misaligned for UTF-8 + tab line:\ngot:\n%s\nwant line containing:\n%q", out, wantCaret)
	}
}

func TestFormatMultilineSpanClampedToLineEnd(t *testing.T) {
	src := "let s = \"unterminated\nnext_line"
	rep := diag.NewReporter()
	rep.AddSource("ml.ll", src)
	rep.Errorf(diag.Span{
		Start: diag.Pos{File: "ml.ll", Line: 1, Col: 9},
		End:   diag.Pos{File: "ml.ll", Line: 2, Col: 5},
	}, "unterminated string")

	out := rep.Format()
	// Line 1 has 21 runes; starting at col 9 leaves 13 runes ("\"unterminated").
	wantCaret := "       | " + strings.Repeat(" ", 8) + "^" + strings.Repeat("~", 12)
	if !strings.Contains(out, wantCaret) {
		t.Fatalf("expected multi-line underline clamped to end of line 1:\ngot:\n%s\nwant line containing:\n%q", out, wantCaret)
	}
}

func TestFormatZeroPosAndRelatedSnippets(t *testing.T) {
	if got := (diag.Pos{}).String(); got != "" {
		t.Fatalf("expected empty Pos.String(), got %q", got)
	}
	rep := diag.NewReporter()
	rep.AddSource("a.ll", "const X = 1\nconst Y = 2\n")
	rep.Report(diag.Diagnostic{
		Severity: diag.Error,
		Message:  "global target error",
	})
	rep.Report(diag.Diagnostic{
		Severity: diag.Error,
		Span:     diag.Span{Start: diag.Pos{File: "a.ll", Line: 2, Col: 7}, End: diag.Pos{File: "a.ll", Line: 2, Col: 8}},
		Message:  "redefined symbol",
		Related: []diag.RelatedInfo{
			{
				Span:    diag.Span{Start: diag.Pos{File: "a.ll", Line: 1, Col: 7}, End: diag.Pos{File: "a.ll", Line: 1, Col: 8}},
				Message: "previous definition here",
			},
		},
	})
	out := rep.Format()
	if strings.Contains(out, "0:0:") {
		t.Fatalf("did not expect 0:0: prefix for zero span:\n%s", out)
	}
	if !strings.Contains(out, "error: global target error") {
		t.Fatalf("expected global target error line:\n%s", out)
	}
	if !strings.Contains(out, "1 | const X = 1") {
		t.Fatalf("expected related note source snippet in output:\n%s", out)
	}
}
