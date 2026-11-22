package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/docgen"
)

func TestRelURL(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{"index.html", "spec/a.html", "spec/a.html"},
		{"spec/a.html", "spec/b.html#x", "b.html#x"},
		{"spec/a.html", "index.html", "../index.html"},
		{"std/src/f.html", "std/fs.html", "../fs.html"},
		{"std/fs.html", "guides/x.html", "../guides/x.html"},
	}
	for _, c := range cases {
		if got := relURL(c.from, c.to); got != c.want {
			t.Errorf("relURL(%q, %q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestSplitAndCrossPageLinks(t *testing.T) {
	src := "# Title\n\nSee [types](#2-types).\n\n## 1. Intro\n\nText [back](#title).\n\n## 2. Types\n\nMore.\n"
	chunks := SplitMarkdown(src, func(level int, title string) bool { return level == 2 })
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks, got %d", len(chunks))
	}
	s := &Site{Title: "T", Sections: []Section{{ID: "d", Title: "Doc", Index: "d/index.html"}}}
	paths := []string{"d/index.html", "d/intro.html", "d/types.html"}
	for i, c := range chunks {
		s.AddPage(&Page{Section: "d", Path: paths[i], Markdown: c.Markdown, DocKey: "DOC.md", Order: float64(i)})
	}
	s.AddPage(&Page{Path: "index.html", Title: "Home", Layout: "home", Body: "<p>home</p>"})
	out := t.TempDir()
	probs, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(probs) != 0 {
		t.Fatalf("unexpected problems: %v", probs)
	}
	b, _ := os.ReadFile(filepath.Join(out, "d", "index.html"))
	if !strings.Contains(string(b), `href="types.html#2-types"`) {
		t.Fatalf("cross-page anchor not rewritten:\n%s", b)
	}
}

func TestBuildAPIDocs(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "geo"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "// Package geo has points.\n\n// A point. See [`Point.mag2`].\ndefine Point struct [\n\t// X coordinate.\n\tx: int,\n\ty: int,\n]\n\n// Squared length.\nbind Point.mag2 = fn(self) int: self.x * self.x + self.y * self.y\n\n// Makes a [`Point`].\nbind make = fn(x: int, y: int) Point: Point[x: x, y: y]\n\nbind _hidden = fn() int: 1\n"
	if err := os.WriteFile(filepath.Join(tmp, "geo", "geo.ll"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "lowlight.toml"), []byte("[package]\nname = \"demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ld, err := docgen.Load([]string{tmp}, tmp)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "out")
	_, probs, err := BuildAPIDocs(APIDocsConfig{Loaded: ld, OutDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if len(probs) != 0 {
		t.Fatalf("broken links: %v", probs)
	}
	page, err := os.ReadFile(filepath.Join(out, "pkg", "geo.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, want := range []string{`id="Point"`, `id="Point.mag2"`, `href="#Point.mag2"`, `"demo/geo"`, `Package geo has points`, `../src/geo_geo.html#L`} {
		if !strings.Contains(html, want) {
			t.Errorf("geo.html missing %q", want)
		}
	}
	if strings.Contains(html, "_hidden") {
		t.Error("private declarations must not be documented")
	}
	for _, f := range []string{"index.html", "search-index.js", "assets/style.css", "assets/site.js"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
}
