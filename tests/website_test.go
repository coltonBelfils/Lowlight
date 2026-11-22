package tests

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"lowlight/internal/driver"
	"lowlight/internal/site"
)

// websiteBlock is one fenced Lowlight block from the website's Markdown content.
type websiteBlock struct {
	file   string
	line   int
	info   string
	code   string
	output string // the ```output block that directly follows, if any
}

var reFenceOpen = regexp.MustCompile("^```+\\s*(\\S*)(.*)$")

func websiteBlocks(t *testing.T, root string) []websiteBlock {
	t.Helper()
	var blocks []websiteBlock
	content := filepath.Join(root, "website", "content")
	err := filepath.WalkDir(content, func(fp string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(fp, ".md") {
			return err
		}
		b, err := os.ReadFile(fp)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, fp)
		lines := strings.Split(string(b), "\n")
		for i := 0; i < len(lines); i++ {
			m := reFenceOpen.FindStringSubmatch(strings.TrimSpace(lines[i]))
			if m == nil {
				continue
			}
			lang, rest := m[1], m[2]
			var body []string
			j := i + 1
			for ; j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "```"); j++ {
				body = append(body, lines[j])
			}
			if lang == "lowlight" {
				blk := websiteBlock{file: rel, line: i + 1, info: rest, code: strings.Join(body, "\n") + "\n"}
				// An ```output block may follow after blank lines.
				k := j + 1
				for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
					k++
				}
				if k < len(lines) && strings.TrimSpace(lines[k]) == "```output" {
					var out []string
					k++
					for ; k < len(lines) && strings.TrimSpace(lines[k]) != "```"; k++ {
						out = append(out, lines[k])
					}
					blk.output = strings.Join(out, "\n")
				}
				blocks = append(blocks, blk)
			}
			i = j
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}

// TestWebsiteCodeBlocks compiles every complete program in the website's guides,
// tour, and tutorials. Programs run and must print exactly what a following
// ```output block shows. Blocks marked `no-run` (servers, programs that read
// files) are type-checked only. Blocks without `bind main` are fragments and are
// not checked on their own.
func TestWebsiteCodeBlocks(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	blocks := websiteBlocks(t, root)
	if len(blocks) == 0 {
		t.Fatal("no code blocks found in website/content")
	}
	for _, blk := range blocks {
		blk := blk
		if !strings.Contains(blk.code, "bind main") || strings.Contains(blk.info, "fragment") {
			continue
		}
		name := fmt.Sprintf("%s:%d", blk.file, blk.line)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			entry := filepath.Join(t.TempDir(), "main.ll")
			opts := driver.Options{EntryPath: entry, SrcOverride: blk.code, ProjectRoot: root}
			if strings.Contains(blk.info, "no-run") {
				if _, diags, err := driver.Check(opts); err != nil {
					t.Fatalf("%s does not compile:\n%v\n%s\n--- code ---\n%s", name, err, diags, blk.code)
				}
				return
			}
			var stdout, stderr bytes.Buffer
			rc, err := driver.Run(opts, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("%s does not compile:\n%v\n%s\n--- code ---\n%s", name, err, stderr.String(), blk.code)
			}
			panics := strings.Contains(blk.info, "panics")
			if rc != 0 && !panics {
				t.Fatalf("%s exited with %d\nstdout:\n%s\nstderr:\n%s", name, rc, stdout.String(), stderr.String())
			}
			if panics && rc == 0 {
				t.Fatalf("%s is marked `panics` but exited normally", name)
			}
			if blk.output != "" {
				got := strings.TrimRight(stdout.String(), "\n")
				if panics {
					got = strings.TrimRight(stdout.String()+stderr.String(), "\n")
				}
				if got != strings.TrimRight(blk.output, "\n") {
					t.Fatalf("%s printed:\n%s\n--- but its output block shows ---\n%s", name, got, blk.output)
				}
			}
		})
	}
}

// TestWebsiteExamples runs every Lowlight by Example program and compares its
// output with the checked-in .out file (regenerate with
// `go run ./cmd/lowlight-site -update-examples`).
func TestWebsiteExamples(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	exs, err := site.LoadExamples(filepath.Join(root, "website", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	if len(exs) == 0 {
		t.Fatal("no examples found")
	}
	for _, ex := range exs {
		ex := ex
		t.Run(ex.Slug, func(t *testing.T) {
			t.Parallel()
			got, err := site.RunExample(root, ex)
			if err != nil {
				t.Fatal(err)
			}
			if got != ex.Output {
				t.Fatalf("output of %s changed:\n%s\n--- expected (.out) ---\n%s", ex.Slug, got, ex.Output)
			}
		})
	}
}

// TestWebsiteBuilds builds the whole website and requires every internal link and
// anchor to resolve.
func TestWebsiteBuilds(t *testing.T) {
	t.Parallel()
	root := projectRoot(t)
	s, probs, err := site.BuildWebsite(site.WebsiteConfig{Root: root, OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		t.Error(p)
	}
	if len(s.Pages) < 100 {
		t.Errorf("expected a full site, got only %d pages", len(s.Pages))
	}
}
