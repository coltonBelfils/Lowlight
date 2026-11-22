package docgen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/docgen"
)

func TestGenerateMarkdownAndCheckLinks(t *testing.T) {
	tmp := t.TempDir()
	alphaDir := filepath.Join(tmp, "alpha")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aPath := filepath.Join(alphaDir, "alpha.ll")
	aTestPath := filepath.Join(alphaDir, "alpha_test.ll")
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

import "./alpha"

// Converts a [` + "`Point`" + `] via [` + "`Point.mag2`" + `].
bind use_point = (p: alpha.Point) int: p.mag2()
`
	if err := os.WriteFile(aPath, []byte(alphaSrc), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aTestPath, []byte("@test(\"hidden\")\nbind test_hidden = (): {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(betaSrc), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := docgen.Generate(docgen.Options{
		Paths:       []string{aPath, bPath},
		CheckLinks:  true,
		ProjectRoot: tmp,
	})
	if err != nil {
		t.Fatalf("link check failed: %v (broken: %v)", err, res.BrokenLinks)
	}
	if res.ModulesCount != 2 || len(res.BrokenLinks) != 0 {
		t.Fatalf("unexpected result: %+v", res)
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
