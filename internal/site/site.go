// Package site builds Lowlight's documentation as a static website.
//
// Two products share this machinery:
//
//   - The Lowlight website (cmd/lowlight-site): guides, the tour, tutorials,
//     Lowlight by Example, the specification, the standard library reference,
//     the implementation guide, and project pages. Content comes from
//     website/content (Markdown with front matter), website/examples (annotated
//     .ll programs), and the repository's own SPECIFICATION.md, CONVENTIONS.md,
//     IMPLEMENTATION.md, and VERSION_HISTORY.md, so the site never drifts from
//     its sources.
//   - Package API documentation for any Lowlight project (`lowlight doc`),
//     which uses only the package pages.
//
// Output is plain HTML, CSS, and JavaScript with no external requests. Every
// page works when opened directly from disk.
package site

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"lowlight/internal/markdown"
)

// Section is a top-level area of the site.
type Section struct {
	ID    string
	Title string
	Index string // path of the section's landing page
	InNav bool   // shown in the header navigation
}

// Page is one output HTML page.
type Page struct {
	Section  string
	Group    string // sidebar group heading
	Path     string // output path relative to the site root, with forward slashes
	Title    string
	NavTitle string // shorter title for the sidebar
	Summary  string
	Order    float64
	Layout   string // "doc" (default), "home", "example", "api", "wide"
	Hidden   bool   // omitted from the sidebar

	// Markdown source and the document it belongs to (for anchor resolution).
	Markdown string
	DocKey   string
	SrcDir   string // directory of the source file, repo-relative, for relative links

	// Prebuilt HTML for generated pages (API, examples). When set, Markdown is ignored.
	Body string
	TOC  []markdown.Heading

	prev, next *Page
	anchors    []string
}

// Site is a set of pages plus the configuration needed to render them.
type Site struct {
	Title    string
	Tagline  string
	Sections []Section
	Pages    []*Page
	Root     string // repository or project root (for resolving source links)
	Footer   string

	// ResolveRef resolves doc-comment style symbol references in prose, such as
	// [`List.push`]. It returns a site-relative path ("std/core.html#List.push").
	ResolveRef func(target string) (href, title string, ok bool)
	// SearchExtra adds entries (such as API symbols) to the search index.
	SearchExtra []SearchEntry

	postRender []func()

	byPath  map[string]*Page
	anchors map[string]map[string]string // docKey -> anchor -> page path
	docPage map[string]string            // docKey -> first page path
}

// Problem is a broken link or other issue found while building.
type Problem struct {
	Page string
	Msg  string
}

func (p Problem) String() string { return p.Page + ": " + p.Msg }

// AddPage registers a page.
func (s *Site) AddPage(p *Page) { s.Pages = append(s.Pages, p) }

// Build renders every page into outDir and returns any broken internal links.
func (s *Site) Build(outDir string) ([]Problem, error) {
	s.byPath = map[string]*Page{}
	s.anchors = map[string]map[string]string{}
	s.docPage = map[string]string{}
	for _, p := range s.Pages {
		if p.Layout == "" {
			p.Layout = "doc"
		}
		if prev, dup := s.byPath[p.Path]; dup {
			return nil, fmt.Errorf("two pages write %s (%q and %q)", p.Path, prev.Title, p.Title)
		}
		s.byPath[p.Path] = p
	}

	// Pass 1: collect each document's anchors so links can cross page boundaries.
	for _, p := range s.Pages {
		if p.DocKey == "" {
			p.DocKey = "page:" + p.Path
		}
		if _, ok := s.docPage[p.DocKey]; !ok {
			s.docPage[p.DocKey] = p.Path
		}
		m := s.anchors[p.DocKey]
		if m == nil {
			m = map[string]string{}
			s.anchors[p.DocKey] = m
		}
		if p.Markdown != "" && p.Body == "" {
			d := markdown.Render(p.Markdown, markdown.Options{})
			for _, h := range d.Headings {
				if _, exists := m[h.ID]; !exists {
					m[h.ID] = p.Path
				}
			}
			for _, mm := range reRawID.FindAllStringSubmatch(p.Markdown, -1) {
				if _, exists := m[mm[1]]; !exists {
					m[mm[1]] = p.Path
				}
			}
		}
		for _, h := range p.TOC {
			if _, exists := m[h.ID]; !exists {
				m[h.ID] = p.Path
			}
		}
	}

	// Pass 2: render Markdown with link rewriting.
	for _, p := range s.Pages {
		if p.Body != "" || p.Markdown == "" {
			continue
		}
		page := p
		d := markdown.Render(p.Markdown, markdown.Options{
			Highlight:   Highlight,
			RewriteLink: func(dest string) string { return s.rewriteLink(page, dest) },
			ResolveRef: func(target string) (string, string, bool) {
				if s.ResolveRef == nil {
					return "", "", false
				}
				href, title, ok := s.ResolveRef(target)
				if !ok {
					return "", "", false
				}
				return relURL(page.Path, href), title, true
			},
			ShiftHeadings: shiftFor(p.Markdown),
		})
		p.Body = reRepoLink.ReplaceAllString(d.HTML, `<span class="repo-link" title="In the Lowlight repository: $1">$2</span>`)
		p.TOC = d.Headings
		if p.Title == "" {
			if len(d.Headings) > 0 {
				p.Title = d.Headings[0].Text
			}
		}
	}

	for _, f := range s.postRender {
		f()
	}
	s.linkPrevNext()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	for _, p := range s.Pages {
		out := filepath.Join(outDir, filepath.FromSlash(p.Path))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(out, []byte(s.renderShell(p)), 0o644); err != nil {
			return nil, err
		}
	}
	if err := writeAssets(outDir); err != nil {
		return nil, err
	}
	if err := s.writeSearchIndex(outDir); err != nil {
		return nil, err
	}
	return checkLinks(outDir), nil
}

// shiftFor promotes headings so a page's first heading renders as <h1>.
func shiftFor(md string) int {
	_, body := markdown.SplitFrontMatter(md)
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimLeft(ln, " ")
		if strings.HasPrefix(t, "#") {
			n := 0
			for n < len(t) && t[n] == '#' {
				n++
			}
			if n < len(t) && t[n] == ' ' {
				return 1 - n
			}
		}
		if strings.HasPrefix(t, "```") {
			return 0
		}
	}
	return 0
}

func (s *Site) linkPrevNext() {
	bySec := map[string][]*Page{}
	for _, p := range s.Pages {
		if p.Hidden || p.Layout == "home" {
			continue
		}
		bySec[p.Section] = append(bySec[p.Section], p)
	}
	for _, ps := range bySec {
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].Order < ps[j].Order })
		for i := range ps {
			if i > 0 {
				ps[i].prev = ps[i-1]
			}
			if i+1 < len(ps) {
				ps[i].next = ps[i+1]
			}
		}
	}
}

// SectionPages returns the visible pages of a section in order.
func (s *Site) SectionPages(id string) []*Page {
	var out []*Page
	for _, p := range s.Pages {
		if p.Section == id && !p.Hidden && p.Layout != "home" {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

var reRawID = regexp.MustCompile(`<[a-z]+ id="([^"]+)"`)

var reScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// rewriteLink maps a Markdown link destination to an href relative to page p.
func (s *Site) rewriteLink(p *Page, dest string) string {
	if dest == "" || reScheme.MatchString(dest) {
		return dest
	}
	target, frag, _ := strings.Cut(dest, "#")
	if target == "" {
		// Same document: the anchor may live on another page of a split document.
		if pg, ok := s.anchors[p.DocKey][frag]; ok && pg != p.Path {
			return relURL(p.Path, pg) + "#" + frag
		}
		return "#" + frag
	}
	withFrag := func(u string) string {
		if frag != "" {
			return u + "#" + frag
		}
		return u
	}
	if strings.HasPrefix(target, "/") {
		return withFrag(relURL(p.Path, strings.TrimPrefix(target, "/")))
	}
	// A repository file: resolve against the source file's directory.
	repoRel := path.Clean(path.Join(p.SrcDir, target))
	if strings.HasSuffix(repoRel, ".md") {
		if m, ok := s.anchors[repoRel]; ok {
			if frag != "" {
				if pg, ok := m[frag]; ok {
					return relURL(p.Path, pg) + "#" + frag
				}
			}
			return withFrag(relURL(p.Path, s.docPage[repoRel]))
		}
	}
	if pg, ok := s.byPath[target]; ok {
		return withFrag(relURL(p.Path, pg.Path))
	}
	// A repository file that is not part of the site (or no longer exists): shown as a
	// reference to the repository rather than a link.
	if s.Root != "" && !strings.HasPrefix(repoRel, "..") {
		return "repo:" + repoRel
	}
	return dest
}

var reRepoLink = regexp.MustCompile(`<a href="repo:([^"]+)">(.*?)</a>`)

// relURL returns the relative URL from page `from` to site path `to`.
func relURL(from, to string) string {
	toPath, frag, hasFrag := strings.Cut(to, "#")
	fromDir := path.Dir(from)
	var rel string
	if fromDir == "." {
		rel = toPath
	} else {
		// Strip the common prefix.
		fparts := strings.Split(fromDir, "/")
		tparts := strings.Split(toPath, "/")
		common := 0
		for common < len(fparts) && common < len(tparts)-1 && fparts[common] == tparts[common] {
			common++
		}
		up := len(fparts) - common
		rel = strings.Repeat("../", up) + strings.Join(tparts[common:], "/")
	}
	if rel == "" {
		rel = path.Base(toPath)
	}
	if hasFrag {
		return rel + "#" + frag
	}
	return rel
}

// SplitDoc splits a Markdown document into pages at headings for which split returns
// a non-empty page slug. Content before the first split heading goes to the first page.
type Chunk struct {
	Title    string
	Level    int
	Markdown string
}

// SplitMarkdown splits src at headings accepted by keep (level, title).
func SplitMarkdown(src string, keep func(level int, title string) bool) []Chunk {
	var chunks []Chunk
	var cur *Chunk
	inFence := false
	lines := strings.Split(src, "\n")
	flush := func() {
		if cur != nil {
			cur.Markdown = strings.TrimSpace(cur.Markdown) + "\n"
			chunks = append(chunks, *cur)
		}
	}
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(ln, "#") {
			n := 0
			for n < len(ln) && ln[n] == '#' {
				n++
			}
			if n < len(ln) && ln[n] == ' ' {
				title := strings.TrimSpace(ln[n:])
				if keep(n, title) {
					flush()
					cur = &Chunk{Title: title, Level: n}
				}
			}
		}
		if cur == nil {
			cur = &Chunk{}
		}
		cur.Markdown += ln + "\n"
	}
	flush()
	return chunks
}
