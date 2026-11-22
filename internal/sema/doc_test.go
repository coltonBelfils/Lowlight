package sema_test

import (
	"testing"

	"lowlight/internal/diag"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

func TestSemaDocPropagation(t *testing.T) {
	src := `// Module doc comment.

// Constant doc.
bind MAGIC = 42

// Distinct doc.
define Meters int

// Boxed pair doc.
define Pair[A, B] struct [
    // First value.
    first: A,
    second: B, // Second value.
]

// Status doc.
define Status enum [
    // Idle state.
    Idle,
    Done, // Done state.
]

// Outcome doc.
define Outcome[T] enum [
    // Success payload.
    Ok: T,
    Err: string, // Error payload.
]

// Interface doc.
define Greeter interface [
    // Greet caller.
    greet: fn(self, name: string) string,
]

// Computes sum.
bind add = fn(a: int, b: int) int: a + b

// Swap pair.
bind Pair[A, B].swap = fn(own self) Pair[B, A]: {
    Pair[B, A][first: self.second, second: self.first]
}

// Idle label.
bind Status.label = fn(self) string: "ok"

bind main = fn() int: {
    let p = Pair[int, string][first: MAGIC, second: "hi"]
    let s = Status.Idle
    let o = Outcome[int].Ok(1)
    let _ = add(p.first, 1)
    let _ = s.label()
    match (o):
        when Outcome.Ok(v): v
        default: 0
}
`
	rep := diag.NewReporter()
	prog := sema.CheckForIDE("doc_prop.ll", map[string]string{"doc_prop.ll": src}, "", rep)
	if rep.HasErrors() {
		t.Fatalf("unexpected sema errors:\n%s", rep.Format())
	}
	if prog == nil || prog.EntryMod == nil {
		t.Fatalf("expected non-nil CheckedProgram and EntryMod")
	}
	if prog.EntryMod.Doc != "Module doc comment." {
		t.Fatalf("expected EntryMod.Doc %q, got %q", "Module doc comment.", prog.EntryMod.Doc)
	}
	if sym := prog.EntryMod.Symbols["MAGIC"]; sym == nil || sym.Doc != "Constant doc." {
		t.Fatalf("unexpected MAGIC doc: %+v", sym)
	}
	if sym := prog.EntryMod.Symbols["add"]; sym == nil || sym.Doc != "Computes sum." || sym.FuncInst == nil || sym.FuncInst.Doc != "Computes sum." {
		t.Fatalf("unexpected add doc: %+v", sym)
	}
	if dt, ok := prog.EntryMod.Types["Meters"].(*types.Distinct); !ok || dt.Doc != "Distinct doc." {
		t.Fatalf("unexpected Meters distinct doc: %#v", prog.EntryMod.Types["Meters"])
	}
	if st, ok := prog.EntryMod.Types["Status"].(*types.Enum); !ok || st.Doc != "Status doc." || st.Variants[0].Doc != "Idle state." || st.Variants[1].Doc != "Done state." {
		t.Fatalf("unexpected Status enum docs: %#v", prog.EntryMod.Types["Status"])
	}
	if ifc, ok := prog.EntryMod.Types["Greeter"].(*types.Interface); !ok || ifc.Doc != "Interface doc." || len(ifc.Methods) != 1 || ifc.Methods[0].Doc != "Greet caller." {
		t.Fatalf("unexpected Greeter interface docs: %#v", prog.EntryMod.Types["Greeter"])
	}
	// Monomorphized Pair[int, string]
	var foundPair *types.Struct
	for _, s := range prog.Structs {
		if s.Name == "Pair" {
			foundPair = s
			break
		}
	}
	if foundPair == nil || foundPair.Doc != "Boxed pair doc." || foundPair.Fields[0].Doc != "First value." || foundPair.Fields[1].Doc != "Second value." {
		t.Fatalf("unexpected Pair struct docs: %#v", foundPair)
	}
	// Monomorphized Outcome[int]
	var foundOutcome *types.Enum
	for _, e := range prog.Enums {
		if e.Name == "Outcome" {
			foundOutcome = e
			break
		}
	}
	if foundOutcome == nil || foundOutcome.Doc != "Outcome doc." || foundOutcome.Variants[0].Doc != "Success payload." || foundOutcome.Variants[1].Doc != "Error payload." {
		t.Fatalf("unexpected Outcome enum docs: %#v", foundOutcome)
	}
	// Method doc on Status.label
	var foundLabelDoc string
	for _, f := range prog.Funcs {
		if f.Name == "Status.label" {
			foundLabelDoc = f.Doc
		}
	}
	if foundLabelDoc != "Idle label." {
		t.Fatalf("unexpected Status.label doc: %q", foundLabelDoc)
	}
}
