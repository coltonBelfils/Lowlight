package lsp

import (
	"fmt"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/sema"
)

// AttributeDoc documents an `@attribute` for completion and hover.
type AttributeDoc struct {
	Name      string
	Snippet   string // inserted after `@`
	AppliesTo string
	Summary   string
}

// BuiltinAttributes lists the attributes the compiler understands. `@lang` is left out: it is
// reserved for std/core.
var BuiltinAttributes = []AttributeDoc{
	{Name: "derive", Snippet: "derive(${1:Equatable})", AppliesTo: "struct, enum, distinct",
		Summary: "Writes the listed interfaces' methods as ordinary Lowlight code: `Equatable` (`eq`), `Comparable` (`cmp`), `Hashable` (`hash`), `Copyable` (`copy`), `Printable` (`to_string`), plus custom `@derivable` interfaces. Every field must support the interface; on a generic type the method exists only for instances whose fields do."},
	{Name: "derivable", Snippet: "derivable", AppliesTo: "interface",
		Summary: "Marks an interface as derivable via `@derive(...)` using a compile-time `bind @derive(Iface)` generator in the same package."},
	{Name: "comptime_only", Snippet: "comptime_only", AppliesTo: "const declaration, type",
		Summary: "Restricts the declaration to compile-time contexts (`const`, `comptime`, or other `@comptime_only` functions) and omits it from runtime codegen."},
	{Name: "comptime_limit", Snippet: "comptime_limit(steps: ${1:5_000_000})", AppliesTo: "const declaration",
		Summary: "Raises or overrides compile-time execution budgets (`steps:`, `unroll:`, `memory:`) for the annotated compile-time function or constant."},
	{Name: "test", Snippet: "test(\"${1:description}\")", AppliesTo: "function",
		Summary: "Marks a top-level `fn()` as a test case run by `lowlight test`, with leak checking, panic recovery, and timing."},
	{Name: "test_only", Snippet: "test_only", AppliesTo: "any declaration",
		Summary: "Usable only from tests; stripped from normal and `--release` builds."},
	{Name: "track_caller", Snippet: "track_caller", AppliesTo: "function, method",
		Summary: "Panics, `Error.new`, and `caller_*()` inside report the caller's location; chains through nested `@track_caller` functions."},
	{Name: "must_use", Snippet: "must_use", AppliesTo: "function, method, type",
		Summary: "Discarding the returned value (or any value of the type) is an error; bind it or write `let _ = ...`."},
	{Name: "nodiscard", Snippet: "nodiscard", AppliesTo: "function, method, type",
		Summary: "Same as `@must_use`."},
	{Name: "deprecated", Snippet: "deprecated(\"${1:message}\")", AppliesTo: "declaration",
		Summary: "Each use produces a warning, with the message if given."},
	{Name: "inline", Snippet: "inline", AppliesTo: "function, method",
		Summary: "Requests inlining; no semantic effect."},
	{Name: "noinline", Snippet: "noinline", AppliesTo: "function, method",
		Summary: "Forbids inlining; no semantic effect."},
	{Name: "packed", Snippet: "packed", AppliesTo: "struct",
		Summary: "Lays fields out without padding (same as `define packed Name struct [...]`), following the C ABI."},
}

// DerivableInterfaces maps each interface `@derive` accepts to what it generates.
var DerivableInterfaces = []struct{ Name, Method, Summary string }{
	{"Equatable", "eq", "`eq`: every field (or the variant and its payload) compares equal; gives `==` and `!=`."},
	{"Comparable", "cmp", "`cmp`: fields in declaration order; for enums, variants in declaration order, then the payload; gives `<`, `<=`, `>`, `>=`."},
	{"Hashable", "hash", "`hash`: combines the hashes of every field (or the variant and its payload); lets the type be a set element or map key (with `Equatable`)."},
	{"Copyable", "copy", "`copy`: copies every field (or the payload); gives `.copy()`."},
	{"Printable", "to_string", "`to_string`: `Point[x: 1, y: 2]` for structs, `Shape.circle(1.5)` and `Level.high` for enums; gives interpolation and `println`."},
}

func lookupAttribute(name string) *AttributeDoc {
	for i := range BuiltinAttributes {
		if BuiltinAttributes[i].Name == name {
			return &BuiltinAttributes[i]
		}
	}
	return nil
}

func collectDerivableInterfaces(prog *sema.CheckedProgram, mod *sema.Module) []struct{ Name, Method, Summary string } {
	out := append([]struct{ Name, Method, Summary string }(nil), DerivableInterfaces...)
	seen := make(map[string]bool, len(out))
	for _, d := range out {
		seen[d.Name] = true
	}
	if prog == nil {
		return out
	}
	for _, m := range prog.Modules {
		if m == nil || m.File == nil {
			continue
		}
		samePkg := mod == nil || prog.SamePackage(mod, m)
		for _, d := range m.ActiveDecls() {
			ifc, ok := d.(*ast.InterfaceDecl)
			if !ok || seen[ifc.Name] || ast.FindAttribute(ifc.Attributes, "derivable") == nil {
				continue
			}
			if !samePkg && !sema.IsExported(ifc.Name) {
				continue
			}
			seen[ifc.Name] = true
			meth := "derived methods"
			if len(ifc.Methods) > 0 {
				var names []string
				for _, im := range ifc.Methods {
					names = append(names, "`"+im.Name+"`")
				}
				meth = strings.Join(names, ", ")
			}
			summary := strings.TrimSpace(ifc.Doc)
			if summary == "" {
				summary = "Custom `@derivable` interface generating " + meth + "."
			} else if nl := strings.IndexByte(summary, '\n'); nl >= 0 {
				summary = strings.TrimSpace(summary[:nl])
			}
			out = append(out, struct{ Name, Method, Summary string }{
				Name:    ifc.Name,
				Method:  meth,
				Summary: summary,
			})
		}
	}
	return out
}

func attributeHoverMarkdown(prog *sema.CheckedProgram, mod *sema.Module, a *AttributeDoc) string {
	md := fmt.Sprintf("### `@%s`\n*Applies to: %s*\n\n%s", a.Name, a.AppliesTo, a.Summary)
	if a.Name == "derive" {
		var b strings.Builder
		b.WriteString(md)
		b.WriteString("\n\n| Interface | Generates |\n|---|---|\n")
		for _, d := range collectDerivableInterfaces(prog, mod) {
			fmt.Fprintf(&b, "| `%s` | %s |\n", d.Name, d.Summary)
		}
		return b.String()
	}
	return md
}

// inDeriveArgs reports whether offset is inside the argument list of a `@derive(` on the same
// line, returning the names already listed.
func inDeriveArgs(src string, offset int) (bool, []string) {
	lineStart := strings.LastIndex(src[:offset], "\n") + 1
	line := src[lineStart:offset]
	i := strings.LastIndex(line, "@derive(")
	if i < 0 || strings.Contains(line[i:], ")") {
		return false, nil
	}
	var listed []string
	for _, part := range strings.Split(line[i+len("@derive("):], ",") {
		if p := strings.TrimSpace(part); p != "" {
			listed = append(listed, p)
		}
	}
	return true, listed
}

// attributeCompletions offers attribute names right after `@`, and interface names inside
// `@derive(...)`. It returns nil when neither applies.
func attributeCompletions(prog *sema.CheckedProgram, mod *sema.Module, src string, prefixStart, offset int, prefix string, replaceRange Range) *CompletionList {
	if ok, listed := inDeriveArgs(src, prefixStart); ok {
		have := make(map[string]bool)
		for _, l := range listed {
			have[l] = true
		}
		var items []CompletionItem
		for i, d := range collectDerivableInterfaces(prog, mod) {
			if have[d.Name] || !matchesPrefix(d.Name, prefix) {
				continue
			}
			items = append(items, CompletionItem{
				Label:         d.Name,
				Kind:          KindInterface,
				Detail:        "derive `" + d.Method + "`",
				SortText:      fmt.Sprintf("0_%02d", i),
				TextEdit:      &TextEdit{Range: replaceRange, NewText: d.Name},
				Documentation: &MarkupContent{Kind: "markdown", Value: d.Summary},
			})
		}
		return &CompletionList{Items: items}
	}
	if prefixStart < 1 || src[prefixStart-1] != '@' {
		return nil
	}
	var items []CompletionItem
	for i, a := range BuiltinAttributes {
		if !matchesPrefix(a.Name, prefix) {
			continue
		}
		items = append(items, CompletionItem{
			Label:            a.Name,
			Kind:             KindKeyword,
			Detail:           "attribute (" + a.AppliesTo + ")",
			SortText:         fmt.Sprintf("0_%02d", i),
			InsertTextFormat: InsertSnippet,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: a.Snippet},
			Documentation:    &MarkupContent{Kind: "markdown", Value: attributeHoverMarkdown(prog, mod, &a)},
		})
	}
	return &CompletionList{Items: items}
}

// attributeHover returns hover markdown for an attribute name (the word right after `@`) or for
// an interface name inside `@derive(...)`.
func attributeHover(prog *sema.CheckedProgram, mod *sema.Module, src string, start int, word string) string {
	if start > 0 && src[start-1] == '@' {
		if a := lookupAttribute(word); a != nil {
			return attributeHoverMarkdown(prog, mod, a)
		}
		return ""
	}
	if ok, _ := inDeriveArgs(src, start); ok {
		for _, d := range collectDerivableInterfaces(prog, mod) {
			if d.Name == word {
				return fmt.Sprintf("### `@derive(%s)`\nGenerates %s", d.Name, d.Summary)
			}
		}
	}
	return ""
}
