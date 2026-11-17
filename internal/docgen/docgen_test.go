package docgen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/docgen"
)

func TestGenerateHTMLAndMarkdownAndCheckLinks(t *testing.T) {
	tmp := t.TempDir()
	aPath := filepath.Join(tmp, "alpha.ll")
	bPath := filepath.Join(tmp, "beta.ll")

	alphaSrc := `// Alpha module documentation.
// Exposes [` + "`Point`" + `] and [` + "`make_point`" + `].

// Represents a 2D point.
define Point struct [
	// X coordinate.
	x: int,
	y: int, // Y coordinate.
]

// Returns origin distance squared.
// See [` + "`make_point`" + `] for construction.
bind Point.mag2 = (self) int: self.x * self.x + self.y * self.y

// Constructs a [` + "`Point`" + `] with given coordinates.
//
// # Example
// ` + "```lowlight" + `
// let p = make_point(3, 4)
// ` + "```" + `
bind make_point = (x: int, y: int) Point: Point[x: x, y: y]

// Internal helper should not appear in public docs.
bind _secret = () int: 1

@test("smoke")
bind test_alpha = (): {
	assert(make_point(1, 2).mag2() == 5)
}
`
	betaSrc := `// Beta module linking to [` + "`alpha.Point`" + `] and [` + "`Point.mag2`" + `] and [` + "`int`" + `].

import "./alpha.ll" as alpha

// Converts a [` + "`Point`" + `] via [` + "`Point.mag2`" + `].
bind use_point = (p: alpha.Point) int: p.mag2()
`
	if err := os.WriteFile(aPath, []byte(alphaSrc), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(betaSrc), 0644); err != nil {
		t.Fatal(err)
	}

	htmlOut := filepath.Join(tmp, "html_docs")
	res, err := docgen.Generate(docgen.Options{
		Paths:       []string{aPath, bPath},
		OutputDir:   htmlOut,
		Format:      "html",
		CheckLinks:  true,
		ProjectRoot: tmp,
	})
	if err != nil {
		t.Fatalf("Generate HTML failed: %v (broken: %v)", err, res.BrokenLinks)
	}
	if res.ModulesCount != 2 || len(res.BrokenLinks) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}

	for _, rel := range []string{
		"index.html",
		"style.css",
		"search-index.js",
		"alpha.html",
		"beta.html",
		filepath.Join("src", "alpha.html"),
		filepath.Join("src", "beta.html"),
	} {
		if _, statErr := os.Stat(filepath.Join(htmlOut, rel)); statErr != nil {
			t.Fatalf("expected generated file %s: %v", rel, statErr)
		}
	}

	alphaHTML, _ := os.ReadFile(filepath.Join(htmlOut, "alpha.html"))
	alphaStr := string(alphaHTML)
	if !strings.Contains(alphaStr, `src/alpha.html#L`) {
		t.Fatalf("expected [src] source code line links in alpha.html")
	}
	if !strings.Contains(alphaStr, `<a class="doc-link" href="alpha.html#Point"`) {
		t.Fatalf("expected resolved intra-doc link to Point in alpha.html:\n%s", alphaStr)
	}
	if strings.Contains(alphaStr, "_secret") || strings.Contains(alphaStr, "test_alpha") {
		t.Fatalf("expected private/test items excluded from public docs")
	}

	betaHTML, _ := os.ReadFile(filepath.Join(htmlOut, "beta.html"))
	if !strings.Contains(string(betaHTML), `<a class="doc-link" href="alpha.html#Point.mag2"`) {
		t.Fatalf("expected cross-module intra-doc link in beta.html:\n%s", string(betaHTML))
	}

	// Markdown generation
	mdOut := filepath.Join(tmp, "md_docs")
	if _, err := docgen.Generate(docgen.Options{
		Paths:       []string{tmp},
		OutputDir:   mdOut,
		Format:      "md",
		CheckLinks:  true,
		ProjectRoot: tmp,
	}); err != nil {
		t.Fatalf("Generate Markdown failed: %v", err)
	}
	alphaMD, _ := os.ReadFile(filepath.Join(mdOut, "alpha.md"))
	if !strings.Contains(string(alphaMD), "[`Point`](alpha.md#Point)") {
		t.Fatalf("expected markdown link in alpha.md:\n%s", string(alphaMD))
	}

	// Broken link verification
	brokenPath := filepath.Join(tmp, "broken.ll")
	brokenSrc := `// References [` + "`NonExistentType`" + `].
bind broken_fn = () int: 0
`
	if err := os.WriteFile(brokenPath, []byte(brokenSrc), 0644); err != nil {
		t.Fatal(err)
	}
	brokenRes, err := docgen.Generate(docgen.Options{
		Paths:       []string{brokenPath},
		CheckLinks:  true,
		ProjectRoot: tmp,
	})
	if err == nil || len(brokenRes.BrokenLinks) != 1 {
		t.Fatalf("expected 1 broken link error, got err=%v links=%+v", err, brokenRes.BrokenLinks)
	}
	if brokenRes.BrokenLinks[0].Target != "NonExistentType" {
		t.Fatalf("unexpected broken link target: %+v", brokenRes.BrokenLinks[0])
	}
}
