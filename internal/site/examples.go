package site

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lowlight/internal/markdown"
)

// Example is one annotated program of Lowlight by Example.
//
// The source format is an ordinary .ll file. Comment blocks that start at column 0
// are prose (Markdown); the code that follows each block is shown beside it. The
// header block holds "Title:", "Category:", and "Order:" lines.
//
//	// Title: Hello, world
//	// Category: Basics
//	// Order: 1
//
//	// Every program starts at `main`.
//	bind main = fn(): println("hello")
//
// The expected output lives next to the program in a file with the same name and
// the extension .out; tests run every example and compare.
type Example struct {
	Slug     string
	Title    string
	Category string
	Order    float64
	Source   string
	Output   string
	Path     string // absolute path of the .ll file
	segments []exSegment
}

type exSegment struct{ doc, code string }

var reExHeader = regexp.MustCompile(`^//\s*(Title|Category|Order|Summary):\s*(.*)$`)

// LoadExamples reads every example in dir.
func LoadExamples(dir string) ([]*Example, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.ll"))
	if err != nil {
		return nil, err
	}
	var out []*Example
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		ex := parseExample(string(b))
		ex.Path = f
		ex.Slug = strings.TrimSuffix(filepath.Base(f), ".ll")
		ex.Slug = regexp.MustCompile(`^\d+[-_]`).ReplaceAllString(ex.Slug, "")
		if ex.Title == "" {
			ex.Title = ex.Slug
		}
		if o, err := os.ReadFile(strings.TrimSuffix(f, ".ll") + ".out"); err == nil {
			ex.Output = string(o)
		}
		out = append(out, ex)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

func parseExample(src string) *Example {
	ex := &Example{}
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	i := 0
	// Header.
	for i < len(lines) {
		m := reExHeader.FindStringSubmatch(lines[i])
		if m == nil {
			break
		}
		switch m[1] {
		case "Title":
			ex.Title = strings.TrimSpace(m[2])
		case "Category":
			ex.Category = strings.TrimSpace(m[2])
		case "Order":
			ex.Order, _ = strconv.ParseFloat(strings.TrimSpace(m[2]), 64)
		}
		i++
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	ex.Source = strings.Join(lines[i:], "\n") + "\n"
	var cur exSegment
	var doc, code []string
	flush := func() {
		cur.doc = strings.TrimSpace(strings.Join(doc, "\n"))
		cur.code = strings.Trim(strings.Join(code, "\n"), "\n")
		if cur.doc != "" || cur.code != "" {
			ex.segments = append(ex.segments, cur)
		}
		cur = exSegment{}
		doc, code = nil, nil
	}
	for ; i < len(lines); i++ {
		ln := lines[i]
		if strings.HasPrefix(ln, "//") {
			if len(code) > 0 && strings.TrimSpace(strings.Join(code, "")) != "" {
				flush()
			}
			text := strings.TrimPrefix(ln, "//")
			text = strings.TrimPrefix(text, " ")
			doc = append(doc, text)
			continue
		}
		code = append(code, ln)
	}
	flush()
	return ex
}

func (s *Site) addExamplePages(examples []*Example, section string) {
	for i, ex := range examples {
		var b strings.Builder
		fmt.Fprintf(&b, `<h1 id="top">%s</h1>`+"\n", markdown.Escape(ex.Title))
		for _, seg := range ex.segments {
			cls := "ex-row"
			if seg.doc == "" {
				cls += " ex-empty-doc"
			}
			b.WriteString(`<div class="` + cls + `"><div class="ex-doc">`)
			if seg.doc != "" {
				b.WriteString(markdown.Render(seg.doc, markdown.Options{Highlight: Highlight}).HTML)
			}
			b.WriteString(`</div><div class="ex-code">`)
			if seg.code != "" {
				b.WriteString(`<pre><code>` + HighlightLowlight(seg.code) + `</code></pre>`)
			}
			b.WriteString(`</div></div>` + "\n")
		}
		b.WriteString(`<div class="ex-run"><h3 id="running">Running it</h3>`)
		fmt.Fprintf(&b, `<pre data-lang="console"><code>%s</code></pre>`, highlightRegex("$ lowlight run "+ex.Slug+".ll", shellRules))
		if ex.Output != "" {
			b.WriteString(`<pre class="output"><code>` + markdown.Escape(strings.TrimRight(ex.Output, "\n")) + `</code></pre>`)
		}
		b.WriteString(`<details><summary>Full source</summary><pre data-lang="lowlight"><code>` + HighlightLowlight(ex.Source) + `</code></pre></details>`)
		b.WriteString(`</div>`)
		s.AddPage(&Page{
			Section: section,
			Group:   ex.Category,
			Path:    "examples/" + ex.Slug + ".html",
			Title:   ex.Title,
			Order:   10 + float64(i),
			Layout:  "example",
			Body:    b.String(),
		})
	}
}

func examplesIndexBody(examples []*Example) string {
	var b strings.Builder
	b.WriteString(`<h1 id="top">Lowlight by Example</h1>
<p>Short, complete, annotated programs, one idea each. Read the notes on the left and the code on the right; every program runs as shown, and its output is at the bottom of the page. The examples are tested with the compiler, so they always match the language.</p>
`)
	cat := "\x00"
	for _, ex := range examples {
		if ex.Category != cat {
			if cat != "\x00" {
				b.WriteString(`</ul>`)
			}
			cat = ex.Category
			fmt.Fprintf(&b, `<h2 class="ex-cat" id="%s">%s</h2><ul class="ex-index">`, markdown.Slug(cat), markdown.Escape(cat))
		}
		fmt.Fprintf(&b, `<li><a href="%s.html">%s</a></li>`, ex.Slug, markdown.Escape(ex.Title))
	}
	if cat != "\x00" {
		b.WriteString(`</ul>`)
	}
	return b.String()
}
