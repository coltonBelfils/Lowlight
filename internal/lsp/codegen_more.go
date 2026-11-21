package lsp

import (
	"fmt"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

// Code generation beyond stubs: real method bodies for the core interfaces, and expanding a
// bare enum or result value into a `match` / result chain with every case listed.

// simpleFieldType reports field types whose values print, compare, and copy without help:
// primitives and strings.
func simpleFieldType(t types.Type) bool {
	if t == nil {
		return false
	}
	if t.Equals(types.TypeString) {
		return true
	}
	_, ok := t.(*types.Primitive)
	return ok
}

// derivedMethodBody writes a real body for `Printable.to_string`, `Equatable.eq`, and
// `Copyable.copy` on a struct with simple fields or a pure tag enum, or "" when it cannot.
func derivedMethodBody(mod *sema.Module, typeName, ifcName, method string) string {
	nom := lookupNominalType(mod, "", typeName)
	switch v := nom.(type) {
	case *types.Struct:
		if len(v.Fields) == 0 {
			return ""
		}
		for _, f := range v.Fields {
			if !simpleFieldType(f.Type) || strings.HasSuffix(f.Name, "?") {
				return ""
			}
		}
		var parts []string
		switch {
		case ifcName == "Printable" && method == "to_string":
			for _, f := range v.Fields {
				if f.Type.Equals(types.TypeString) {
					parts = append(parts, fmt.Sprintf("%s: \\\"${self.%s}\\\"", f.Name, f.Name))
				} else {
					parts = append(parts, fmt.Sprintf("%s: ${self.%s}", f.Name, f.Name))
				}
			}
			return fmt.Sprintf("\"%s[%s]\"", typeName, strings.Join(parts, ", "))
		case ifcName == "Equatable" && method == "eq":
			for _, f := range v.Fields {
				parts = append(parts, fmt.Sprintf("self.%s == other.%s", f.Name, f.Name))
			}
			return strings.Join(parts, " and ")
		case ifcName == "Copyable" && method == "copy":
			for _, f := range v.Fields {
				if f.Type.IsPlain() {
					parts = append(parts, fmt.Sprintf("%s: self.%s", f.Name, f.Name))
				} else {
					parts = append(parts, fmt.Sprintf("%s: self.%s.copy()", f.Name, f.Name))
				}
			}
			return fmt.Sprintf("%s[%s]", typeName, strings.Join(parts, ", "))
		}
	case *types.Enum:
		if v.IsTagged || len(v.Variants) == 0 || ifcName != "Printable" || method != "to_string" {
			return ""
		}
		lines := []string{"match(self):"}
		for i, ev := range v.Variants {
			kw := "when"
			if i == len(v.Variants)-1 {
				kw = "default"
			}
			lines = append(lines, fmt.Sprintf("\t%s %s.%s: \"%s\"", kw, typeName, ev.Name, ev.Name))
		}
		return "\n\t" + strings.Join(lines, "\n\t")
	}
	return ""
}

// expandCaseActions offers, on a line that is only an enum or result value, to expand it into a
// `match` (or a result chain) that lists every case.
func (s *Server) expandCaseActions(uri, src, ind string, prog *sema.CheckedProgram, mod *sema.Module, offset int, addAction func(CodeAction)) {
	if prog == nil || mod == nil {
		return
	}
	lineStart := strings.LastIndexByte(src[:offset], '\n') + 1
	lineEnd := len(src)
	if i := strings.IndexByte(src[offset:], '\n'); i >= 0 {
		lineEnd = offset + i
	}
	line := src[lineStart:lineEnd]
	code := strings.TrimSpace(line)
	if code == "" || strings.HasPrefix(code, "when ") || strings.HasPrefix(code, "default") || strings.HasSuffix(code, ":") {
		return // match / chain arms and headers are not values to expand
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	var en *types.Enum
	for _, fi := range prog.Funcs {
		if fi.Mod != mod || fi.BodyMaps == nil {
			continue
		}
		for e, inf := range fi.BodyMaps.ExprInfos {
			sp := e.GetSpan()
			if sp.Start.Offset != lineStart+len(indent) || sp.End.Offset != lineStart+len(indent)+len(code) || inf == nil {
				continue
			}
			if _, isMatch := e.(*ast.MatchExpr); isMatch {
				return
			}
			if m, isMem := e.(*ast.MemberExpr); isMem && isPlaceholderIdent(m.Base) {
				continue
			}
			if t, ok := unwrapOptAndRef(inf.Type).(*types.Enum); ok && len(t.Variants) > 0 {
				en = t
			}
		}
	}
	if en == nil {
		return
	}
	pattern := func(v types.EnumVariant, bare bool) string {
		name := v.Name
		if !bare {
			name = en.Name + "." + v.Name
		}
		if v.Kind == types.VariantTuple {
			return name + "(_)"
		}
		return name
	}
	var arms []string
	var title, head string
	if en.IsResult {
		failures := en.ResultFailures()
		for i, f := range failures {
			kw := "when"
			if i == len(failures)-1 {
				kw = "default"
			}
			arms = append(arms, fmt.Sprintf("%s%s%s %s: panic(\"todo\")", indent, ind, kw, pattern(*f, true)))
		}
		title = fmt.Sprintf("Handle every failure of `%s`", en.String())
		head = code + ":"
	} else {
		for i, v := range en.Variants {
			kw := "when"
			if i == len(en.Variants)-1 {
				kw = "default"
			}
			arms = append(arms, fmt.Sprintf("%s%s%s %s: panic(\"todo\")", indent, ind, kw, pattern(v, false)))
		}
		title = fmt.Sprintf("Expand into `match` on every variant of `%s`", en.String())
		head = "match(" + code + "):"
	}
	addAction(CodeAction{
		Title: title,
		Kind:  "refactor.rewrite",
		Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{
			Range:   Range{Start: OffsetToLSP(src, lineStart+len(indent)), End: OffsetToLSP(src, lineEnd)},
			NewText: head + "\n" + strings.Join(arms, "\n"),
		}}}},
	})
}
