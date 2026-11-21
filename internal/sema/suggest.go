package sema

import (
	"fmt"
	"sort"
	"strings"

	"lowlight/internal/diag"
	"lowlight/internal/types"
)

// "Did you mean ...?" suggestions for unknown names, functions, methods, and fields.

var builtinFuncNames = []string{
	"print", "println", "eprint", "eprintln", "panic", "assert", "exit", "abs", "min", "max", "args",
	"caller_location", "caller_file", "caller_line", "caller_col", "parse_int", "parse_float",
	"size_of", "align_of", "is_plain", "alloc_raw", "alloc_uninit", "realloc_raw", "free_raw",
	"null_ptr", "is_null", "addr_of", "ptr_add", "ptr_read", "ptr_write", "ptr_cast", "ptr_addr",
	"copy_bytes", "zero_bytes", "fill_bytes",
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// closestName returns the candidates nearest to `name` within a small edit distance (at most
// two, joined as "`a` or `b`"), or "". Names shorter than three characters get no suggestion.
func closestName(name string, candidates []string) string {
	return strings.Join(closestNames(name, candidates), "` or `")
}

// closestNames returns up to two candidates nearest to `name` within a small edit distance.
func closestNames(name string, candidates []string) []string {
	if len(strings.TrimRight(name, "?!")) < 3 {
		return nil
	}
	limit := max(1, len(name)/3)
	bestD := limit + 1
	var best []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == name || c == "" || strings.HasPrefix(c, "<") || seen[c] {
			continue
		}
		seen[c] = true
		d := editDistance(strings.ToLower(name), strings.ToLower(c))
		if d < bestD {
			best, bestD = []string{c}, d
		} else if d == bestD {
			best = append(best, c)
		}
	}
	sort.Strings(best)
	if len(best) > 2 {
		best = best[:2]
	}
	return best
}

// visibleNames lists identifiers in scope at the current point: locals, the module's top-level
// symbols, exported symbols of imports, and built-in functions.
func (fc *funcChecker) visibleNames() []string {
	var out []string
	for i := len(fc.scopes) - 1; i >= 0; i-- {
		for n := range fc.scopes[i].vars {
			out = append(out, n)
		}
	}
	if fc.mod != nil {
		for n := range fc.mod.Symbols {
			out = append(out, n)
		}
		for _, imp := range fc.mod.Imports {
			for n := range imp.Symbols {
				if IsExported(n) {
					out = append(out, n)
				}
			}
		}
	}
	return append(out, builtinFuncNames...)
}

// unknownNameHint explains an unknown identifier: a declared `name?` / `name!` spelled without
// its suffix, or a likely typo.
func (fc *funcChecker) unknownNameHint(name string) (string, []string) {
	if sym := fc.lookupVar(name + "?"); sym != nil {
		return fmt.Sprintf("the variable is named `%s?` (it is optional); unwrap it with `if (%s?)`, `%s? ?? fallback`, or use `%s?` itself", name, name, name, name), []string{name + "?"}
	}
	if sym := fc.lookupVar(name + "!"); sym != nil {
		return fmt.Sprintf("the variable is named `%s!` (it holds a result); handle it with a result chain, `%s! ?? fallback`, or `if (%s! as v)`", name, name, name), []string{name + "!"}
	}
	if strings.HasSuffix(name, "?") || strings.HasSuffix(name, "!") {
		if sym := fc.lookupVar(name[:len(name)-1]); sym != nil {
			return fmt.Sprintf("the variable is named `%s` (type %s)", name[:len(name)-1], sym.Type.String()), []string{name[:len(name)-1]}
		}
	}
	if c := closestNames(name, fc.visibleNames()); len(c) > 0 {
		return fmt.Sprintf("did you mean `%s`?", strings.Join(c, "` or `")), c
	}
	return "", nil
}

// builtinMethodNames are methods the checker implements directly on strings and collections.
var builtinMethodNames = []string{
	"len", "cap", "is_empty", "push", "pop", "clear", "get", "set", "has", "insert", "remove",
	"keys", "values", "contains", "copy", "to_string", "byte_at", "get_byte", "bytes", "chars",
	"push_char", "push_byte", "index_of", "iter",
}

// methodNames lists the methods callable on `recv` that the analyzer knows about.
func (a *Analyzer) methodNames(recv types.Type) []string {
	out := append([]string(nil), builtinMethodNames...)
	if mset, ok := a.methods[TypeMethodKey(recv)]; ok {
		for n := range mset {
			out = append(out, n)
		}
	}
	base := ""
	switch v := recv.(type) {
	case *types.Struct:
		base = v.Name
	case *types.Vector:
		base = "List"
	case *types.StackArray:
		base = "Array"
	case *types.Primitive:
		base = v.String()
	case *types.Distinct:
		base = v.Name
	case *types.Enum:
		base = v.Name
	case *types.Map:
		base = "Map"
	case *types.Set:
		base = "Set"
	}
	if gset, ok := a.genMethods[base]; ok {
		for n := range gset {
			out = append(out, n)
		}
	}
	return out
}

// unknownMethodHint explains `x.name()` when `name` is not a method of `recv`.
func (a *Analyzer) unknownMethodHint(recv types.Type, name string) (string, []string) {
	if st, ok := recv.(*types.Struct); ok {
		if f, _ := st.FieldByName(name); f != nil {
			return fmt.Sprintf("`%s` is a field of type %s, not a method; drop the `()`", name, f.Type.String()), nil
		}
	}
	if c := closestNames(name, a.methodNames(recv)); len(c) > 0 {
		return fmt.Sprintf("did you mean `.%s()`?", strings.Join(c, "()` or `.")), c
	}
	return "", nil
}

// unknownFieldHint explains `x.name` when `name` is not a field of struct `st`.
func (a *Analyzer) unknownFieldHint(st *types.Struct, name string) (string, []string) {
	for _, n := range a.methodNames(st) {
		if n == name {
			return fmt.Sprintf("`%s` is a method; call it: `.%s()`", name, name), nil
		}
	}
	var fields []string
	for _, f := range st.Fields {
		fields = append(fields, f.Name)
	}
	if c := closestNames(name, fields); len(c) > 0 {
		return fmt.Sprintf("did you mean `.%s`?", strings.Join(c, "` or `.")), c
	}
	if len(fields) > 0 && len(fields) <= 8 {
		return fmt.Sprintf("%s has fields: %s", st.Name, "`"+strings.Join(fields, "`, `")+"`"), nil
	}
	return "", nil
}

// errorWithRename reports an unknown-name error. `candidates` (from the hint) become editor
// quick fixes that replace `name` with one of them.
func (a *Analyzer) errorWithRename(span diag.Span, msg, name string, hint string, candidates []string) {
	d := diag.Diagnostic{Severity: diag.Error, Span: span, Message: msg, Hint: hint}
	if len(candidates) > 0 {
		d.Data = map[string]string{"kind": "rename", "name": name, "candidates": strings.Join(candidates, ",")}
	}
	a.Rep.Report(d)
}
