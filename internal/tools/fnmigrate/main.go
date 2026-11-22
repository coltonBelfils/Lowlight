// Command fnmigrate rewrites Lowlight source from the old function syntax (`(x: int) int: ...`,
// `(int) int`) to `fn(...)` / `cl(...)`. It is a one-off migration tool.
//
// Usage: go run ./internal/tools/fnmigrate <file>...  (.ll, .md code blocks, and Go string literals)
//
// Every old-form function literal and type gets `cl`, except top-level functions, methods,
// extern declarations, and interface methods, which get `fn`. Closures that capture nothing then
// draw a warning ("could be an `fn(...)`"); change those to `fn` by hand.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lowlight/internal/diag"
	llparser "lowlight/internal/parser"
)

// migrate inserts `fn`/`cl` at every old-form site in a Lowlight source.
func migrate(src string) string {
	var sites []*llparser.LegacyFuncSite
	llparser.AcceptLegacyFuncSyntax = true
	llparser.RecordLegacyFuncSites = &sites
	rep := diag.NewReporter()
	llparser.ParseFile("x.ll", src, rep)
	llparser.RecordLegacyFuncSites = nil
	sort.Slice(sites, func(i, j int) bool { return sites[i].Pos.Offset > sites[j].Pos.Offset })
	out := src
	seen := map[int]bool{}
	for _, s := range sites {
		off := s.Pos.Offset
		if seen[off] || off < 0 || off >= len(out) || out[off] != '(' {
			continue
		}
		seen[off] = true
		kw := "fn"
		if s.Closure {
			kw = "cl"
		}
		out = out[:off] + kw + out[off:]
	}
	return out
}

var fence = regexp.MustCompile("(?s)(```lowlight[^\n]*\n)(.*?)(\n```)")

func looksLikeLowlight(s string) bool {
	return strings.Contains(s, "bind ") || strings.Contains(s, "define ") || strings.Contains(s, "extern ")
}

func migrateGo(path, src string) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, path, err)
		return src
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil || !looksLikeLowlight(val) {
			return true
		}
		nv := migrate(val)
		if nv == val {
			return true
		}
		var text string
		if strings.HasPrefix(lit.Value, "`") && !strings.Contains(nv, "`") {
			text = "`" + nv + "`"
		} else {
			text = strconv.Quote(nv)
		}
		edits = append(edits, edit{fset.Position(lit.Pos()).Offset, fset.Position(lit.End()).Offset, text})
		return true
	})
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		src = src[:e.start] + e.text + src[e.end:]
	}
	return src
}

func main() {
	for _, path := range os.Args[1:] {
		b, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		src := string(b)
		var out string
		switch {
		case strings.HasSuffix(path, ".ll"):
			out = migrate(src)
		case strings.HasSuffix(path, ".md"):
			out = fence.ReplaceAllStringFunc(src, func(m string) string {
				p := fence.FindStringSubmatch(m)
				return p[1] + migrate(p[2]) + p[3]
			})
		case strings.HasSuffix(path, ".go"):
			out = migrateGo(path, src)
		default:
			continue
		}
		if out != src {
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println("migrated", path)
		}
	}
}
