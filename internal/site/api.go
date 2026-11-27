package site

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/docgen"
	"lowlight/internal/markdown"
)

// APIOptions configures package API pages.
type APIOptions struct {
	Loaded  *docgen.Loaded
	Section string
	Group   string
	Order   float64
	// PagePath returns the output path for a package directory (relative to the root,
	// "." for the root package), e.g. "std/fs.html".
	PagePath func(pkgDir string) string
	// ImportPath returns the import path shown for a package directory.
	ImportPath func(pkgDir string) string
	// SourceDir is the output directory for highlighted source pages ("std/src").
	SourceDir string
}

// Package is the documentation model of one package (all files in a directory).
type Package struct {
	Dir        string // relative to the root, forward slashes
	ImportPath string
	Name       string
	Doc        string
	Summary    string
	Files      []string // absolute paths
	Path       string   // output page path

	consts, funcs, externs []*docgen.SymbolEntry
	types                  []*apiType
	foreign                map[string][]*docgen.SymbolEntry // methods on types declared elsewhere
}

type apiType struct {
	entry   *docgen.SymbolEntry
	members []*docgen.SymbolEntry
	methods []*docgen.SymbolEntry
}

// BuildPackages groups an index's declarations into packages.
func BuildPackages(opts APIOptions) []*Package {
	ld := opts.Loaded
	idx := ld.Index
	byDir := map[string]*Package{}
	var pkgs []*Package
	relDir := func(abs string) string {
		d := filepath.Dir(abs)
		if ld.RootDir != "" {
			if r, err := filepath.Rel(ld.RootDir, d); err == nil {
				return filepath.ToSlash(r)
			}
		}
		return filepath.ToSlash(d)
	}
	for _, f := range ld.Files {
		dir := relDir(f)
		p := byDir[dir]
		if p == nil {
			p = &Package{Dir: dir, ImportPath: opts.ImportPath(dir), Path: opts.PagePath(dir), foreign: map[string][]*docgen.SymbolEntry{}}
			p.Name = p.ImportPath[strings.LastIndex(p.ImportPath, "/")+1:]
			byDir[dir] = p
			pkgs = append(pkgs, p)
		}
		p.Files = append(p.Files, f)
	}
	// Package documentation: the doc of the file named after the package, then others.
	for _, p := range pkgs {
		var docs []string
		seen := map[string]bool{}
		sort.SliceStable(p.Files, func(i, j int) bool {
			bi := strings.TrimSuffix(filepath.Base(p.Files[i]), ".ll") == p.Name
			bj := strings.TrimSuffix(filepath.Base(p.Files[j]), ".ll") == p.Name
			if bi != bj {
				return bi
			}
			return p.Files[i] < p.Files[j]
		})
		for _, f := range p.Files {
			if m := idx.Modules[f]; m != nil {
				d := strings.TrimSpace(m.Doc)
				if d != "" && !seen[d] && !strings.Contains(strings.ToLower(d), "please leave it as is") {
					seen[d] = true
					docs = append(docs, d)
				}
			}
		}
		p.Doc = strings.Join(docs, "\n\n")
		p.Summary = firstSentence(p.Doc)
	}

	// Assign entries to packages and point their hrefs at the site pages.
	declared := map[string]map[string]*apiType{} // dir -> type name -> type
	entries := idx.SortedEntries()
	for _, e := range entries {
		p := byDir[relDir(e.FilePath)]
		if p == nil {
			continue
		}
		if e.Kind == docgen.KindModule {
			e.HTMLHref = p.Path
			continue
		}
		e.HTMLHref = p.Path + "#" + e.Anchor
		switch e.Kind {
		case docgen.KindConst:
			p.consts = append(p.consts, e)
		case docgen.KindFunc:
			p.funcs = append(p.funcs, e)
		case docgen.KindExtern:
			p.externs = append(p.externs, e)
		case docgen.KindStruct, docgen.KindEnum, docgen.KindInterface, docgen.KindDistinct:
			t := &apiType{entry: e}
			p.types = append(p.types, t)
			if declared[p.Dir] == nil {
				declared[p.Dir] = map[string]*apiType{}
			}
			declared[p.Dir][e.Name] = t
		}
	}
	for _, e := range entries {
		p := byDir[relDir(e.FilePath)]
		if p == nil {
			continue
		}
		owner, _, ok := strings.Cut(e.FullName, ".")
		switch e.Kind {
		case docgen.KindField, docgen.KindVariant, docgen.KindInterfaceMethod:
			if ok {
				if t := declared[p.Dir][owner]; t != nil {
					t.members = append(t.members, e)
				}
			}
		case docgen.KindMethod:
			if !ok {
				continue
			}
			if t := declared[p.Dir][owner]; t != nil {
				t.methods = append(t.methods, e)
			} else {
				p.foreign[owner] = append(p.foreign[owner], e)
			}
		}
	}
	for _, p := range pkgs {
		sort.Slice(p.types, func(i, j int) bool { return p.types[i].entry.Name < p.types[j].entry.Name })
		for _, t := range p.types {
			sort.SliceStable(t.methods, func(i, j int) bool { return t.methods[i].Name < t.methods[j].Name })
		}
		sortByName(p.consts)
		sortByName(p.funcs)
		sortByName(p.externs)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ImportPath < pkgs[j].ImportPath })
	return pkgs
}

func sortByName(es []*docgen.SymbolEntry) {
	sort.SliceStable(es, func(i, j int) bool { return es[i].Name < es[j].Name })
}

func firstSentence(doc string) string {
	d := strings.TrimSpace(doc)
	if i := strings.Index(d, "\n\n"); i >= 0 {
		d = d[:i]
	}
	d = strings.Join(strings.Fields(d), " ")
	if i := strings.Index(d, ". "); i >= 0 {
		d = d[:i+1]
	}
	if len(d) > 180 {
		d = d[:177] + "..."
	}
	return markdown.PlainText(markdown.RenderInline(d, markdown.Options{}))
}

// AddAPIPages adds one page per package plus highlighted source pages, and returns
// search entries for every documented symbol.
func AddAPIPages(s *Site, pkgs []*Package, opts APIOptions) {
	idx := opts.Loaded.Index
	for i, p := range pkgs {
		pg := &Page{
			Section:  opts.Section,
			Group:    opts.Group,
			Path:     p.Path,
			Title:    p.ImportPath,
			NavTitle: p.ImportPath,
			Summary:  p.Summary,
			Order:    opts.Order + float64(i),
			Layout:   "api",
		}
		pg.Body, pg.TOC = renderPackage(p, pg, idx, opts)
		s.AddPage(pg)
		for _, f := range p.Files {
			s.AddPage(sourcePage(p, f, opts))
		}
		s.SearchExtra = append(s.SearchExtra, packageSearchEntries(p)...)
	}
}

func srcPagePath(opts APIOptions, abs string, root string) string {
	rel := filepath.Base(abs)
	if root != "" {
		if r, err := filepath.Rel(root, abs); err == nil {
			rel = filepath.ToSlash(r)
		}
	}
	return opts.SourceDir + "/" + strings.ReplaceAll(strings.TrimSuffix(rel, ".ll"), "/", "_") + ".html"
}

func packageSearchEntries(p *Package) []SearchEntry {
	var out []SearchEntry
	add := func(e *docgen.SymbolEntry, kind string) {
		out = append(out, SearchEntry{Title: e.FullName, URL: e.HTMLHref, Section: "Package " + p.ImportPath, Kind: kind, Desc: firstSentence(e.Doc)})
	}
	for _, e := range p.consts {
		add(e, "const")
	}
	for _, e := range p.funcs {
		add(e, "func")
	}
	for _, e := range p.externs {
		add(e, "extern")
	}
	for _, t := range p.types {
		add(t.entry, string(t.entry.Kind))
		for _, m := range t.methods {
			add(m, "method")
		}
		for _, m := range t.members {
			if m.Kind == docgen.KindVariant {
				add(m, "variant")
			}
		}
	}
	for _, ms := range p.foreign {
		for _, m := range ms {
			add(m, "method")
		}
	}
	return out
}

func renderPackage(p *Package, pg *Page, idx *docgen.SymbolIndex, opts APIOptions) (string, []markdown.Heading) {
	var b strings.Builder
	var toc []markdown.Heading
	esc := markdown.Escape
	doc := func(text, file, typ string) string {
		if strings.TrimSpace(text) == "" {
			return ""
		}
		return markdown.Render(text, markdown.Options{
			Highlight:     Highlight,
			ShiftHeadings: 2,
			ResolveRef: func(target string) (string, string, bool) {
				e, _ := idx.ResolveLink(target, file, typ)
				if e == nil || e.HTMLHref == "" || !strings.HasSuffix(strings.SplitN(e.HTMLHref, "#", 2)[0], ".html") {
					return "", "", false
				}
				return relURL(pg.Path, e.HTMLHref), e.Signature, true
			},
		}).HTML
	}
	src := func(e *docgen.SymbolEntry) string {
		return fmt.Sprintf(`<a class="src" href="%s#L%d">source</a>`, relURL(pg.Path, srcPagePath(opts, e.FilePath, opts.Loaded.RootDir)), e.Line)
	}
	heading := func(level int, id, text string) {
		toc = append(toc, markdown.Heading{Level: level, Text: text, ID: id})
		fmt.Fprintf(&b, `<h%d id="%s"><a class="anchor" href="#%s" aria-hidden="true">#</a>%s</h%d>`+"\n", level, esc(id), esc(id), esc(text), level)
	}
	entryBadges := func(e *docgen.SymbolEntry, defaultBadge string) string {
		if e == nil {
			return fmt.Sprintf(`<span class="api-badge">%s</span>`, esc(defaultBadge))
		}
		var out strings.Builder
		b := defaultBadge
		switch e.Kind {
		case docgen.KindConst:
			if e.IsConst {
				b = "const"
			} else {
				b = "bind"
			}
		case docgen.KindFunc:
			if e.IsConst {
				b = "const fn"
			}
		case docgen.KindMethod:
			if e.IsConst {
				b = "const method"
			} else {
				b = ""
			}
		}
		if b != "" {
			fmt.Fprintf(&out, `<span class="api-badge">%s</span>`, esc(b))
		}
		if e.ComptimeOnly {
			out.WriteString(`<span class="api-badge">@comptime_only</span>`)
		}
		return out.String()
	}
	item := func(e *docgen.SymbolEntry, badge, typ string) {
		fmt.Fprintf(&b, `<div class="api-item" id="%s"><pre class="api-sig">%s<code>%s</code>%s</pre><div class="api-body">%s</div></div>`+"\n",
			esc(e.Anchor), entryBadges(e, badge), HighlightLowlight(e.Signature), src(e), doc(e.Doc, e.FilePath, typ))
	}
	method := func(m *docgen.SymbolEntry, recv string) {
		fmt.Fprintf(&b, `<div class="api-method" id="%s"><pre>%s<code>%s</code></pre><div class="api-mdoc">%s</div></div>`+"\n",
			esc(m.Anchor), entryBadges(m, ""), HighlightLowlight(m.Signature), doc(m.Doc, m.FilePath, recv))
	}

	toc = append(toc, markdown.Heading{Level: 1, Text: p.ImportPath, ID: "top"})
	fmt.Fprintf(&b, `<h1 id="top">Package <code>%s</code></h1>`+"\n", esc(p.Name))
	fmt.Fprintf(&b, `<div class="api-import"><span class="k">import</span> <span class="s">"%s"</span></div>`+"\n", esc(p.ImportPath))
	if p.Doc != "" {
		heading(2, "overview", "Overview")
		b.WriteString(doc(p.Doc, p.Files[0], ""))
	}

	// Index.
	heading(2, "index", "Index")
	b.WriteString(`<ul class="api-index">`)
	link := func(e *docgen.SymbolEntry, label string) {
		fmt.Fprintf(&b, `<li><a href="#%s"><code>%s</code></a></li>`, esc(e.Anchor), esc(label))
	}
	for _, e := range p.consts {
		link(e, e.Name)
	}
	for _, e := range p.funcs {
		link(e, e.Name+"(…)")
	}
	for _, t := range p.types {
		link(t.entry, string(t.entry.Kind)+" "+t.entry.Name)
		for _, m := range t.methods {
			fmt.Fprintf(&b, `<li style="margin-left:1.2em"><a href="#%s"><code>%s</code></a></li>`, esc(m.Anchor), esc(m.FullName))
		}
	}
	for _, recv := range sortedKeys(p.foreign) {
		for _, m := range p.foreign[recv] {
			link(m, m.FullName)
		}
	}
	for _, e := range p.externs {
		link(e, "extern "+e.Name)
	}
	b.WriteString(`</ul>`)

	if len(p.consts) > 0 {
		heading(2, "constants", "Constants")
		for _, e := range p.consts {
			item(e, "const", "")
		}
	}
	if len(p.funcs) > 0 {
		heading(2, "functions", "Functions")
		for _, e := range p.funcs {
			item(e, "func", "")
		}
	}
	if len(p.types) > 0 {
		heading(2, "types", "Types")
		for _, t := range p.types {
			e := t.entry
			toc = append(toc, markdown.Heading{Level: 3, Text: e.Name, ID: e.Anchor})
			fmt.Fprintf(&b, `<div class="api-item" id="%s"><pre class="api-sig">%s<code>%s</code>%s</pre><div class="api-body">`,
				esc(e.Anchor), entryBadges(e, string(e.Kind)), HighlightLowlight(e.Signature), src(e))
			b.WriteString(doc(e.Doc, e.FilePath, e.Name))
			if len(t.members) > 0 {
				label := map[docgen.ItemKind]string{docgen.KindField: "Fields", docgen.KindVariant: "Variants", docgen.KindInterfaceMethod: "Methods"}[t.members[0].Kind]
				fmt.Fprintf(&b, `<div class="api-sub">%s</div><ul class="api-members">`, label)
				for _, m := range t.members {
					fmt.Fprintf(&b, `<li id="%s"><code>%s</code>`, esc(m.Anchor), HighlightLowlight(m.Signature))
					if d := doc(m.Doc, m.FilePath, e.Name); d != "" {
						b.WriteString(`<div class="api-mdoc">` + d + `</div>`)
					}
					b.WriteString(`</li>`)
				}
				b.WriteString(`</ul>`)
			}
			if len(t.methods) > 0 {
				b.WriteString(`<div class="api-sub">Methods</div>`)
				for _, m := range t.methods {
					method(m, e.Name)
				}
			}
			b.WriteString(`</div></div>` + "\n")
		}
	}
	if len(p.foreign) > 0 {
		heading(2, "extensions", "Methods on other types")
		b.WriteString(`<p>This package adds these methods to types declared elsewhere.</p>`)
		for _, recv := range sortedKeys(p.foreign) {
			fmt.Fprintf(&b, `<h3 id="ext-%s">%s</h3>`, esc(recv), esc(recv))
			toc = append(toc, markdown.Heading{Level: 3, Text: recv, ID: "ext-" + recv})
			for _, m := range p.foreign[recv] {
				method(m, recv)
			}
		}
	}
	if len(p.externs) > 0 {
		heading(2, "externs", "Foreign functions")
		for _, e := range p.externs {
			item(e, "extern", "")
		}
	}
	heading(2, "source-files", "Source files")
	b.WriteString(`<ul>`)
	for _, f := range p.Files {
		fmt.Fprintf(&b, `<li><a href="%s">%s</a></li>`, relURL(pg.Path, srcPagePath(opts, f, opts.Loaded.RootDir)), esc(filepath.Base(f)))
	}
	b.WriteString(`</ul>`)
	return b.String(), toc
}

func sortedKeys(m map[string][]*docgen.SymbolEntry) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sourcePage(p *Package, abs string, opts APIOptions) *Page {
	src := opts.Loaded.Overlays[abs]
	hl := HighlightLowlight(src)
	// Split highlighted HTML into lines without breaking spans that cross lines.
	var b strings.Builder
	b.WriteString(`<div class="src-view">`)
	fmt.Fprintf(&b, `<h1>%s</h1><p>Source of package <a href="%s"><code>%s</code></a>.</p><pre><code>`,
		markdown.Escape(filepath.Base(abs)), relURL(srcPagePath(opts, abs, opts.Loaded.RootDir), p.Path), markdown.Escape(p.ImportPath))
	open := []string{}
	lines := strings.Split(hl, "\n")
	for i, ln := range lines {
		fmt.Fprintf(&b, `<span class="src-line" id="L%d">`, i+1)
		b.WriteString(strings.Join(open, ""))
		b.WriteString(ln)
		// Track spans still open at the end of the line.
		open = openSpans(open, ln)
		for range open {
			b.WriteString(`</span>`)
		}
		b.WriteString("</span>\n")
	}
	b.WriteString(`</code></pre></div>`)
	return &Page{
		Section: opts.Section,
		Path:    srcPagePath(opts, abs, opts.Loaded.RootDir),
		Title:   filepath.Base(abs) + " source",
		Layout:  "wide",
		Hidden:  true,
		Body:    b.String(),
	}
}

func openSpans(open []string, ln string) []string {
	out := append([]string(nil), open...)
	for i := 0; i < len(ln); {
		switch {
		case strings.HasPrefix(ln[i:], "<span"):
			end := strings.IndexByte(ln[i:], '>')
			out = append(out, ln[i:i+end+1])
			i += end + 1
		case strings.HasPrefix(ln[i:], "</span>"):
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			i += len("</span>")
		default:
			i++
		}
	}
	return out
}

// PackageIndexBody renders a table of packages for a landing page.
func PackageIndexBody(fromPath string, pkgs []*Package) string {
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="pkg-table"><thead><tr><th>Package</th><th>Summary</th></tr></thead><tbody>`)
	for _, p := range pkgs {
		fmt.Fprintf(&b, `<tr><td><a href="%s"><code>%s</code></a></td><td>%s</td></tr>`, relURL(fromPath, p.Path), markdown.Escape(p.ImportPath), markdown.Escape(p.Summary))
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}
