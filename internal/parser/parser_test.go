package parser_test

import (
	"strings"
	"testing"

	"lowlight/internal/diag"
	"lowlight/internal/parser"
)

func TestParseComprehensiveLanguageConstructs(t *testing.T) {
	src := `
import "std/pool.ll" as pool
extern "math.h"
extern cos: (x: float): float

define UserID int

define Status enum u8 [
    Pending = 1,
    Active = 2,
    Closed = 3,
]

define Printable interface [
    to_string: (self): string,
]

define Pair[A: Printable, B] struct [
    first: A,
    second: B,
    note?: ?string,
]

bind Pair[A, B].swap = (own self): Pair[B, A] {
    Pair[B, A][
        first: self.second,
        second: self.first,
    ]
}

bind main = (): int {
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
    if (ages["Bob"] as b) {
        println("Bob is $b")
    }

    let status = Status.Active
    let code = match (status)
        when Status.Pending: if (age > 20) 10 else 11
        when Status.Active: 20
        else Status.Closed: 30

    let total = outer: for (i, x in nums) {
        if (x < 0) next outer <-
        if (x == 99) outer <- 999
        x + i
    } else {
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
	if len(file.Decls) != 9 {
		t.Fatalf("expected 9 top-level declarations, got %d", len(file.Decls))
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
bind main = () {
    println(func <-)
}
`
	rep := diag.NewReporter()
	_ = parser.ParseFile("bad_unwind.ll", src, rep)
	if !rep.HasErrors() {
		t.Fatalf("expected error for unwind inside function argument list")
	}
}
