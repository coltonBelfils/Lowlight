package sema_test

import (
	"strings"
	"testing"
)

func TestAttributesValid(t *testing.T) {
	src := `
@doc("Point structure in 2D space")
define Point struct [
    x: int,
    y: int,
]

@inline
bind add = (a: int, b: int): int -> {
    a + b
}

bind main = () -> {
    let p = Point[x: 1, y: 2]
    let res = add(p.x, p.y)
    assert(res == 3)
}
`
	rep := checkSrc(src)
	if rep.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", rep.Format())
	}
}

func TestAttributesLangItemDiagnostics(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		expectedErr string
	}{
		{
			name: "unknown lang item",
			src: `
@lang("nonexistent_item")
define CustomIf interface [
    foo: (self),
]
bind main = () -> {}
`,
			expectedErr: `unknown interface lang item @lang("nonexistent_item")`,
		},
		{
			name: "lang on struct error",
			src: `
@lang("printable")
define MyStruct struct [
    x: int,
]
bind main = () -> {}
`,
			expectedErr: `unknown struct lang item @lang("printable")`,
		},
		{
			name: "lang missing argument",
			src: `
@lang()
define MyIface interface [
    bar: (self),
]
bind main = () -> {}
`,
			expectedErr: `@lang attribute requires a string argument`,
		},
		{
			name: "duplicate lang item",
			src: `
@lang("printable")
define AnotherPrintable interface [
    to_string: (self): string,
]
bind main = () -> {}
`,
			expectedErr: `duplicate lang item @lang("printable")`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := checkSrc(tc.src)
			if !rep.HasErrors() {
				t.Fatalf("expected error containing %q, got none", tc.expectedErr)
			}
			out := rep.Format()
			if !strings.Contains(out, tc.expectedErr) {
				t.Fatalf("expected error containing %q, got:\n%s", tc.expectedErr, out)
			}
		})
	}
}
