package docgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
)

type modulePageData struct {
	AbsPath    string
	ModName    string
	Slug       string
	Doc        string
	File       *ast.File
	Consts     []*SymbolEntry
	Externs    []*SymbolEntry
	Funcs      []*SymbolEntry
	Structs    []typeGroup
	Enums      []typeGroup
	Interfaces []ifaceGroup
	Distincts  []typeGroup
}

type typeGroup struct {
	Type    *SymbolEntry
	Members []*SymbolEntry // fields or variants
	Methods []*SymbolEntry
}

type ifaceGroup struct {
	Iface   *SymbolEntry
	Methods []*SymbolEntry
}

func collectModulePages(rootDir string, files []string, idx *SymbolIndex) []modulePageData {
	var pages []modulePageData
	for _, abs := range files {
		f := idx.ModFiles[abs]
		modEntry := idx.Modules[abs]
		if f == nil || modEntry == nil {
			continue
		}
		page := modulePageData{
			AbsPath: abs,
			ModName: modEntry.ModuleName,
			Slug:    modEntry.ModuleSlug,
			Doc:     modEntry.Doc,
			File:    f,
		}

		// Collect methods by receiver type for this module.
		methodsByRecv := make(map[string][]*SymbolEntry)
		membersByOwner := make(map[string][]*SymbolEntry)
		for _, e := range idx.SortedEntries() {
			if e.FilePath != abs {
				continue
			}
			switch e.Kind {
			case KindConst:
				page.Consts = append(page.Consts, e)
			case KindExtern:
				page.Externs = append(page.Externs, e)
			case KindFunc:
				page.Funcs = append(page.Funcs, e)
			case KindMethod:
				if dot := strings.IndexByte(e.FullName, '.'); dot > 0 {
					recv := e.FullName[:dot]
					methodsByRecv[recv] = append(methodsByRecv[recv], e)
				}
			case KindField, KindVariant, KindInterfaceMethod:
				if dot := strings.IndexByte(e.FullName, '.'); dot > 0 {
					owner := e.FullName[:dot]
					membersByOwner[owner] = append(membersByOwner[owner], e)
				}
			}
		}

		for _, e := range idx.SortedEntries() {
			if e.FilePath != abs {
				continue
			}
			switch e.Kind {
			case KindStruct:
				page.Structs = append(page.Structs, typeGroup{
					Type:    e,
					Members: membersByOwner[e.Name],
					Methods: methodsByRecv[e.Name],
				})
			case KindEnum:
				page.Enums = append(page.Enums, typeGroup{
					Type:    e,
					Members: membersByOwner[e.Name],
					Methods: methodsByRecv[e.Name],
				})
			case KindDistinct:
				page.Distincts = append(page.Distincts, typeGroup{
					Type:    e,
					Methods: methodsByRecv[e.Name],
				})
			case KindInterface:
				page.Interfaces = append(page.Interfaces, ifaceGroup{
					Iface:   e,
					Methods: membersByOwner[e.Name],
				})
			}
		}

		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].ModName < pages[j].ModName
	})
	return pages
}

func writeHTMLDocs(outDir, rootDir string, files []string, overlays map[string]string, idx *SymbolIndex) error {
	srcDir := filepath.Join(outDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		return err
	}

	pages := collectModulePages(rootDir, files, idx)

	if err := os.WriteFile(filepath.Join(outDir, "style.css"), []byte(docCSS), 0644); err != nil {
		return err
	}
	if err := writeSearchAssets(outDir, pages, idx); err != nil {
		return err
	}

	for _, pg := range pages {
		srcHTML := renderSourceHTMLPage(pg, overlays[pg.AbsPath], pages)
		if err := os.WriteFile(filepath.Join(srcDir, pg.Slug+".html"), []byte(srcHTML), 0644); err != nil {
			return err
		}
		modHTML := renderModuleHTMLPage(pg, pages, idx)
		if err := os.WriteFile(filepath.Join(outDir, pg.Slug+".html"), []byte(modHTML), 0644); err != nil {
			return err
		}
	}

	indexHTML := renderIndexHTMLPage(pages, idx)
	return os.WriteFile(filepath.Join(outDir, "index.html"), []byte(indexHTML), 0644)
}

type searchItem struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Module string `json:"module"`
	Sig    string `json:"sig"`
	Desc   string `json:"desc"`
	Href   string `json:"href"`
}

func writeSearchAssets(outDir string, pages []modulePageData, idx *SymbolIndex) error {
	allowedMods := make(map[string]bool, len(pages))
	for _, pg := range pages {
		allowedMods[pg.ModName] = true
	}
	var items []searchItem
	for _, e := range idx.SortedEntries() {
		if !allowedMods[e.ModuleName] {
			continue
		}
		items = append(items, searchItem{
			Name:   e.FullName,
			Kind:   string(e.Kind),
			Module: e.ModuleName,
			Sig:    e.Signature,
			Desc:   e.SearchDesc,
			Href:   e.HTMLHref,
		})
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	js := "window.LOWLIGHT_SEARCH_INDEX = " + string(raw) + ";\n" + docSearchJS
	return os.WriteFile(filepath.Join(outDir, "search-index.js"), []byte(js), 0644)
}

func renderSidebarHTML(pages []modulePageData, activeSlug string, relPrefix string) string {
	var sb strings.Builder
	sb.WriteString(`<aside class="sidebar">`)
	sb.WriteString(fmt.Sprintf(`<div class="brand"><a href="%sindex.html">Lowlight Docs</a></div>`, relPrefix))
	sb.WriteString(`<div class="sidebar-tools">`)
	sb.WriteString(`<input id="doc-search" type="search" placeholder="Search symbols (press /)" autocomplete="off" />`)
	sb.WriteString(`<button id="theme-toggle" type="button" title="Toggle theme">Theme</button>`)
	sb.WriteString(`</div>`)
	sb.WriteString(`<div id="search-results" class="search-results hidden"></div>`)
	sb.WriteString(`<nav class="mod-nav"><h3>Modules</h3><ul>`)
	for _, pg := range pages {
		cls := ""
		if pg.Slug == activeSlug {
			cls = ` class="active"`
		}
		sb.WriteString(fmt.Sprintf(`<li%s><a href="%s%s.html">%s</a></li>`, cls, relPrefix, pg.Slug, htmlEscape(pg.ModName)))
	}
	sb.WriteString(`</ul></nav></aside>`)
	return sb.String()
}

func renderHTMLShell(title, activeSlug, relPrefix string, pages []modulePageData, bodyHTML string) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>`)
	sb.WriteString(htmlEscape(title))
	sb.WriteString(` — Lowlight Documentation</title>
<link rel="stylesheet" href="`)
	sb.WriteString(relPrefix)
	sb.WriteString(`style.css" />
</head>
<body data-rel-prefix="`)
	sb.WriteString(relPrefix)
	sb.WriteString(`">
<div class="layout">
`)
	sb.WriteString(renderSidebarHTML(pages, activeSlug, relPrefix))
	sb.WriteString(`<main class="content">
`)
	sb.WriteString(bodyHTML)
	sb.WriteString(`</main>
</div>
<script src="`)
	sb.WriteString(relPrefix)
	sb.WriteString(`search-index.js"></script>
</body>
</html>
`)
	return sb.String()
}

func renderIndexHTMLPage(pages []modulePageData, idx *SymbolIndex) string {
	var body strings.Builder
	body.WriteString(`<header class="page-header"><h1>Lowlight Documentation</h1>`)
	body.WriteString(fmt.Sprintf(`<p class="subtitle">%d modules &middot; %d indexed items</p></header>`, len(pages), len(idx.SortedEntries())))

	body.WriteString(`<section class="module-grid">`)
	for _, pg := range pages {
		summary := firstSentence(pg.Doc)
		if summary == "" {
			summary = "No module summary."
		}
		body.WriteString(`<div class="module-card">`)
		body.WriteString(fmt.Sprintf(`<h2><a href="%s.html">%s</a> <a class="src-link" href="src/%s.html#L1">[src]</a></h2>`,
			pg.Slug, htmlEscape(pg.ModName), pg.Slug))
		body.WriteString(fmt.Sprintf(`<p>%s</p>`, htmlEscape(summary)))
		body.WriteString(`<div class="card-counts">`)
		counts := []string{}
		if len(pg.Structs) > 0 {
			counts = append(counts, fmt.Sprintf("%d structs", len(pg.Structs)))
		}
		if len(pg.Enums) > 0 {
			counts = append(counts, fmt.Sprintf("%d enums", len(pg.Enums)))
		}
		if len(pg.Interfaces) > 0 {
			counts = append(counts, fmt.Sprintf("%d interfaces", len(pg.Interfaces)))
		}
		if len(pg.Funcs) > 0 {
			counts = append(counts, fmt.Sprintf("%d functions", len(pg.Funcs)))
		}
		body.WriteString(htmlEscape(strings.Join(counts, " · ")))
		body.WriteString(`</div></div>`)
	}
	body.WriteString(`</section>`)
	return renderHTMLShell("Overview", "", "", pages, body.String())
}

func renderModuleHTMLPage(pg modulePageData, pages []modulePageData, idx *SymbolIndex) string {
	var body strings.Builder
	body.WriteString(fmt.Sprintf(`<header class="page-header"><div class="header-row"><h1>Module <code>%s</code></h1><a class="src-link" href="src/%s.html#L1">[src]</a></div>`,
		htmlEscape(pg.ModName), pg.Slug))

	// On-page quick jump links
	var toc []string
	if len(pg.Consts) > 0 {
		toc = append(toc, `<a href="#sec-consts">Constants</a>`)
	}
	if len(pg.Structs) > 0 {
		toc = append(toc, `<a href="#sec-structs">Structs</a>`)
	}
	if len(pg.Enums) > 0 {
		toc = append(toc, `<a href="#sec-enums">Enums</a>`)
	}
	if len(pg.Interfaces) > 0 {
		toc = append(toc, `<a href="#sec-interfaces">Interfaces</a>`)
	}
	if len(pg.Distincts) > 0 {
		toc = append(toc, `<a href="#sec-distinct">Distinct Types</a>`)
	}
	if len(pg.Funcs) > 0 {
		toc = append(toc, `<a href="#sec-funcs">Functions</a>`)
	}
	if len(pg.Externs) > 0 {
		toc = append(toc, `<a href="#sec-externs">Externs</a>`)
	}
	if len(toc) > 0 {
		body.WriteString(`<div class="toc">`)
		body.WriteString(strings.Join(toc, " &middot; "))
		body.WriteString(`</div>`)
	}
	body.WriteString(`</header>`)

	if pg.Doc != "" {
		body.WriteString(`<section class="doc-block module-doc">`)
		body.WriteString(RenderDocHTML(pg.Doc, pg.AbsPath, "", idx))
		body.WriteString(`</section>`)
	}

	renderItemHeader := func(tag string, e *SymbolEntry) {
		body.WriteString(fmt.Sprintf(`<div class="item-header" id="%s"><span class="badge badge-%s">%s</span><code class="sig">%s</code><a class="src-link" href="src/%s.html#L%d">[src]</a></div>`,
			htmlEscape(e.Anchor), htmlEscape(string(e.Kind)), htmlEscape(tag), HighlightLowlightHTML(e.Signature), pg.Slug, e.Line))
	}

	if len(pg.Consts) > 0 {
		body.WriteString(`<section id="sec-consts"><h2>Constants</h2>`)
		for _, c := range pg.Consts {
			body.WriteString(`<article class="doc-item">`)
			renderItemHeader("const", c)
			if c.Doc != "" {
				body.WriteString(`<div class="doc-block">`)
				body.WriteString(RenderDocHTML(c.Doc, pg.AbsPath, "", idx))
				body.WriteString(`</div>`)
			}
			body.WriteString(`</article>`)
		}
		body.WriteString(`</section>`)
	}

	if len(pg.Structs) > 0 {
		body.WriteString(`<section id="sec-structs"><h2>Structs</h2>`)
		for _, sg := range pg.Structs {
			body.WriteString(`<article class="doc-item">`)
			renderItemHeader("struct", sg.Type)
			if sg.Type.Doc != "" {
				body.WriteString(`<div class="doc-block">`)
				body.WriteString(RenderDocHTML(sg.Type.Doc, pg.AbsPath, sg.Type.Name, idx))
				body.WriteString(`</div>`)
			}
			if len(sg.Members) > 0 {
				body.WriteString(`<h4>Fields</h4><ul class="member-list">`)
				for _, m := range sg.Members {
					body.WriteString(fmt.Sprintf(`<li id="%s"><code>%s</code> <a class="src-link" href="src/%s.html#L%d">[src]</a>`,
						htmlEscape(m.Anchor), HighlightLowlightHTML(m.Signature), pg.Slug, m.Line))
					if m.Doc != "" {
						body.WriteString(`<div class="doc-block inline-doc">`)
						body.WriteString(RenderDocHTML(m.Doc, pg.AbsPath, sg.Type.Name, idx))
						body.WriteString(`</div>`)
					}
					body.WriteString(`</li>`)
				}
				body.WriteString(`</ul>`)
			}
			if len(sg.Methods) > 0 {
				body.WriteString(`<h4>Methods</h4><div class="methods-group">`)
				for _, m := range sg.Methods {
					body.WriteString(`<div class="method-item">`)
					renderItemHeader("method", m)
					if m.Doc != "" {
						body.WriteString(`<div class="doc-block">`)
						body.WriteString(RenderDocHTML(m.Doc, pg.AbsPath, sg.Type.Name, idx))
						body.WriteString(`</div>`)
					}
					body.WriteString(`</div>`)
				}
				body.WriteString(`</div>`)
			}
			body.WriteString(`</article>`)
		}
		body.WriteString(`</section>`)
	}

	if len(pg.Enums) > 0 {
		body.WriteString(`<section id="sec-enums"><h2>Enums</h2>`)
		for _, eg := range pg.Enums {
			body.WriteString(`<article class="doc-item">`)
			renderItemHeader("enum", eg.Type)
			if eg.Type.Doc != "" {
				body.WriteString(`<div class="doc-block">`)
				body.WriteString(RenderDocHTML(eg.Type.Doc, pg.AbsPath, eg.Type.Name, idx))
				body.WriteString(`</div>`)
			}
			if len(eg.Members) > 0 {
				body.WriteString(`<h4>Variants</h4><ul class="member-list">`)
				for _, v := range eg.Members {
					body.WriteString(fmt.Sprintf(`<li id="%s"><code>%s</code> <a class="src-link" href="src/%s.html#L%d">[src]</a>`,
						htmlEscape(v.Anchor), HighlightLowlightHTML(v.Signature), pg.Slug, v.Line))
					if v.Doc != "" {
						body.WriteString(`<div class="doc-block inline-doc">`)
						body.WriteString(RenderDocHTML(v.Doc, pg.AbsPath, eg.Type.Name, idx))
						body.WriteString(`</div>`)
					}
					body.WriteString(`</li>`)
				}
				body.WriteString(`</ul>`)
			}
			if len(eg.Methods) > 0 {
				body.WriteString(`<h4>Methods</h4><div class="methods-group">`)
				for _, m := range eg.Methods {
					body.WriteString(`<div class="method-item">`)
					renderItemHeader("method", m)
					if m.Doc != "" {
						body.WriteString(`<div class="doc-block">`)
						body.WriteString(RenderDocHTML(m.Doc, pg.AbsPath, eg.Type.Name, idx))
						body.WriteString(`</div>`)
					}
					body.WriteString(`</div>`)
				}
				body.WriteString(`</div>`)
			}
			body.WriteString(`</article>`)
		}
		body.WriteString(`</section>`)
	}

	if len(pg.Interfaces) > 0 {
		body.WriteString(`<section id="sec-interfaces"><h2>Interfaces</h2>`)
		for _, ig := range pg.Interfaces {
			body.WriteString(`<article class="doc-item">`)
			renderItemHeader("interface", ig.Iface)
			if ig.Iface.Doc != "" {
				body.WriteString(`<div class="doc-block">`)
				body.WriteString(RenderDocHTML(ig.Iface.Doc, pg.AbsPath, ig.Iface.Name, idx))
				body.WriteString(`</div>`)
			}
			if len(ig.Methods) > 0 {
				body.WriteString(`<h4>Required Methods</h4><ul class="member-list">`)
				for _, m := range ig.Methods {
					body.WriteString(fmt.Sprintf(`<li id="%s"><code>%s</code> <a class="src-link" href="src/%s.html#L%d">[src]</a>`,
						htmlEscape(m.Anchor), HighlightLowlightHTML(m.Signature), pg.Slug, m.Line))
					if m.Doc != "" {
						body.WriteString(`<div class="doc-block inline-doc">`)
						body.WriteString(RenderDocHTML(m.Doc, pg.AbsPath, ig.Iface.Name, idx))
						body.WriteString(`</div>`)
					}
					body.WriteString(`</li>`)
				}
				body.WriteString(`</ul>`)
			}
			body.WriteString(`</article>`)
		}
		body.WriteString(`</section>`)
	}

	if len(pg.Distincts) > 0 {
		body.WriteString(`<section id="sec-distinct"><h2>Distinct Types</h2>`)
		for _, dg := range pg.Distincts {
			body.WriteString(`<article class="doc-item">`)
			renderItemHeader("type", dg.Type)
			if dg.Type.Doc != "" {
				body.WriteString(`<div class="doc-block">`)
				body.WriteString(RenderDocHTML(dg.Type.Doc, pg.AbsPath, dg.Type.Name, idx))
				body.WriteString(`</div>`)
			}
			if len(dg.Methods) > 0 {
				body.WriteString(`<h4>Methods</h4><div class="methods-group">`)
				for _, m := range dg.Methods {
					body.WriteString(`<div class="method-item">`)
					renderItemHeader("method", m)
					if m.Doc != "" {
						body.WriteString(`<div class="doc-block">`)
						body.WriteString(RenderDocHTML(m.Doc, pg.AbsPath, dg.Type.Name, idx))
						body.WriteString(`</div>`)
					}
					body.WriteString(`</div>`)
				}
				body.WriteString(`</div>`)
			}
			body.WriteString(`</article>`)
		}
		body.WriteString(`</section>`)
	}

	if len(pg.Funcs) > 0 {
		body.WriteString(`<section id="sec-funcs"><h2>Functions</h2>`)
		for _, f := range pg.Funcs {
			body.WriteString(`<article class="doc-item">`)
			renderItemHeader("fn", f)
			if f.Doc != "" {
				body.WriteString(`<div class="doc-block">`)
				body.WriteString(RenderDocHTML(f.Doc, pg.AbsPath, "", idx))
				body.WriteString(`</div>`)
			}
			body.WriteString(`</article>`)
		}
		body.WriteString(`</section>`)
	}

	if len(pg.Externs) > 0 {
		body.WriteString(`<section id="sec-externs"><h2>Extern Functions</h2>`)
		for _, f := range pg.Externs {
			body.WriteString(`<article class="doc-item">`)
			renderItemHeader("extern", f)
			if f.Doc != "" {
				body.WriteString(`<div class="doc-block">`)
				body.WriteString(RenderDocHTML(f.Doc, pg.AbsPath, "", idx))
				body.WriteString(`</div>`)
			}
			body.WriteString(`</article>`)
		}
		body.WriteString(`</section>`)
	}

	return renderHTMLShell(pg.ModName, pg.Slug, "", pages, body.String())
}

func renderSourceHTMLPage(pg modulePageData, src string, pages []modulePageData) string {
	var body strings.Builder
	body.WriteString(fmt.Sprintf(`<header class="page-header"><div class="header-row"><h1>Source: <code>%s.ll</code></h1><a class="src-link" href="../%s.html">[docs]</a></div></header>`,
		htmlEscape(pg.ModName), pg.Slug))

	lines := strings.Split(src, "\n")
	body.WriteString(`<div class="source-view"><table class="src-table"><tbody>`)
	for i, line := range lines {
		ln := i + 1
		highlighted := highlightLineSafely(line)
		body.WriteString(fmt.Sprintf(`<tr id="L%d"><td class="ln"><a href="#L%d">%d</a></td><td class="code">%s</td></tr>`,
			ln, ln, ln, highlighted))
	}
	body.WriteString(`</tbody></table></div>`)
	return renderHTMLShell(pg.ModName+" (source)", pg.Slug, "../", pages, body.String())
}

func highlightLineSafely(line string) string {
	if line == "" {
		return " "
	}
	rep := diag.NewReporter()
	toks, comments := lexer.LexWithComments("<line>", line, rep)
	return highlightSourceWithLexer(line, toks, comments)
}

func writeMarkdownDocs(outDir, rootDir string, files []string, overlays map[string]string, idx *SymbolIndex) error {
	pages := collectModulePages(rootDir, files, idx)

	var indexBuf strings.Builder
	indexBuf.WriteString("# Lowlight API Documentation\n\n")
	for _, pg := range pages {
		summary := firstSentence(pg.Doc)
		indexBuf.WriteString(fmt.Sprintf("- [%s](%s.md): %s\n", pg.ModName, pg.Slug, summary))
		if err := os.WriteFile(filepath.Join(outDir, pg.Slug+".md"), []byte(renderModuleMarkdown(pg, idx)), 0644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(outDir, "INDEX.md"), []byte(indexBuf.String()), 0644)
}

func renderMarkdownWithLinks(doc, currentModPath, currentType string, idx *SymbolIndex) string {
	if doc == "" {
		return ""
	}
	links := ParseDocLinks(doc)
	if len(links) == 0 {
		return doc
	}
	var sb strings.Builder
	pos := 0
	for _, lk := range links {
		if lk.Start > pos {
			sb.WriteString(doc[pos:lk.Start])
		}
		entry, _ := idx.ResolveLink(lk.Target, currentModPath, currentType)
		if entry != nil {
			sb.WriteString(fmt.Sprintf("[`%s`](%s.md#%s)", lk.Label, entry.ModuleSlug, entry.Anchor))
		} else {
			sb.WriteString(fmt.Sprintf("`%s`", lk.Label))
		}
		pos = lk.End
	}
	if pos < len(doc) {
		sb.WriteString(doc[pos:])
	}
	return sb.String()
}

func renderModuleMarkdown(pg modulePageData, idx *SymbolIndex) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Module `%s`\n\n", pg.ModName))
	if pg.Doc != "" {
		sb.WriteString(renderMarkdownWithLinks(pg.Doc, pg.AbsPath, "", idx))
		sb.WriteString("\n\n")
	}

	writeEntry := func(level string, e *SymbolEntry, enclosingType string) {
		sb.WriteString(fmt.Sprintf("%s `%s` ([src](%s#L%d))\n\n", level, e.FullName, pg.ModName+".ll", e.Line))
		sb.WriteString("```lowlight\n" + e.Signature + "\n```\n\n")
		if e.Doc != "" {
			sb.WriteString(renderMarkdownWithLinks(e.Doc, pg.AbsPath, enclosingType, idx))
			sb.WriteString("\n\n")
		}
	}

	if len(pg.Consts) > 0 {
		sb.WriteString("## Constants\n\n")
		for _, c := range pg.Consts {
			writeEntry("###", c, "")
		}
	}
	if len(pg.Structs) > 0 {
		sb.WriteString("## Structs\n\n")
		for _, sg := range pg.Structs {
			writeEntry("###", sg.Type, sg.Type.Name)
			if len(sg.Members) > 0 {
				sb.WriteString("**Fields:**\n")
				for _, m := range sg.Members {
					sb.WriteString(fmt.Sprintf("- `%s`: %s\n", m.Signature, renderMarkdownWithLinks(firstSentence(m.Doc), pg.AbsPath, sg.Type.Name, idx)))
				}
				sb.WriteString("\n")
			}
			for _, m := range sg.Methods {
				writeEntry("####", m, sg.Type.Name)
			}
		}
	}
	if len(pg.Enums) > 0 {
		sb.WriteString("## Enums\n\n")
		for _, eg := range pg.Enums {
			writeEntry("###", eg.Type, eg.Type.Name)
			if len(eg.Members) > 0 {
				sb.WriteString("**Variants:**\n")
				for _, v := range eg.Members {
					sb.WriteString(fmt.Sprintf("- `%s`: %s\n", v.Signature, renderMarkdownWithLinks(firstSentence(v.Doc), pg.AbsPath, eg.Type.Name, idx)))
				}
				sb.WriteString("\n")
			}
			for _, m := range eg.Methods {
				writeEntry("####", m, eg.Type.Name)
			}
		}
	}
	if len(pg.Interfaces) > 0 {
		sb.WriteString("## Interfaces\n\n")
		for _, ig := range pg.Interfaces {
			writeEntry("###", ig.Iface, ig.Iface.Name)
			for _, m := range ig.Methods {
				writeEntry("####", m, ig.Iface.Name)
			}
		}
	}
	if len(pg.Distincts) > 0 {
		sb.WriteString("## Distinct Types\n\n")
		for _, dg := range pg.Distincts {
			writeEntry("###", dg.Type, dg.Type.Name)
			for _, m := range dg.Methods {
				writeEntry("####", m, dg.Type.Name)
			}
		}
	}
	if len(pg.Funcs) > 0 {
		sb.WriteString("## Functions\n\n")
		for _, f := range pg.Funcs {
			writeEntry("###", f, "")
		}
	}
	if len(pg.Externs) > 0 {
		sb.WriteString("## Extern Functions\n\n")
		for _, f := range pg.Externs {
			writeEntry("###", f, "")
		}
	}
	return sb.String()
}

const docCSS = `:root {
  --bg: #ffffff;
  --surface: #f7f8fa;
  --border: #e1e4e8;
  --text: #1f2328;
  --muted: #656d76;
  --accent: #0969da;
  --code-bg: #f1f3f5;
  --kw: #cf222e;
  --type: #8250df;
  --str: #0a3069;
  --num: #0550ae;
  --cmt: #6e7781;
  --attr: #953800;
}
:root.dark {
  --bg: #0d1117;
  --surface: #161b22;
  --border: #30363d;
  --text: #e6edf3;
  --muted: #8b949e;
  --accent: #58a6ff;
  --code-bg: #1c2128;
  --kw: #ff7b72;
  --type: #d2a8ff;
  --str: #a5d6ff;
  --num: #79c0ff;
  --cmt: #8b949e;
  --attr: #ffa657;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  background: var(--bg);
  color: var(--text);
  line-height: 1.6;
}
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }
code, pre { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.9em; }
.layout { display: flex; min-height: 100vh; }
.sidebar {
  width: 280px;
  flex-shrink: 0;
  background: var(--surface);
  border-right: 1px solid var(--border);
  padding: 1rem;
  position: sticky;
  top: 0;
  height: 100vh;
  overflow-y: auto;
}
.brand { font-size: 1.15rem; font-weight: 700; margin-bottom: 0.75rem; }
.sidebar-tools { display: flex; gap: 0.4rem; margin-bottom: 0.75rem; }
#doc-search {
  flex: 1;
  padding: 0.4rem 0.6rem;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
  color: var(--text);
}
#theme-toggle {
  padding: 0.35rem 0.6rem;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
  color: var(--text);
  cursor: pointer;
}
.search-results {
  margin-bottom: 1rem;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
  max-height: 360px;
  overflow-y: auto;
}
.search-results.hidden { display: none; }
.search-hit { display: block; padding: 0.45rem 0.6rem; border-bottom: 1px solid var(--border); }
.search-hit:last-child { border-bottom: none; }
.search-hit small { display: block; color: var(--muted); }
.mod-nav h3 { font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.05em; color: var(--muted); margin: 0.5rem 0; }
.mod-nav ul { list-style: none; padding: 0; margin: 0; }
.mod-nav li a { display: block; padding: 0.25rem 0.5rem; border-radius: 4px; }
.mod-nav li.active a { background: var(--code-bg); font-weight: 600; }
.content { flex: 1; max-width: 960px; padding: 2rem 2.5rem; }
.page-header { border-bottom: 1px solid var(--border); padding-bottom: 1rem; margin-bottom: 1.5rem; }
.header-row { display: flex; align-items: baseline; justify-content: space-between; gap: 1rem; }
.toc { font-size: 0.9rem; color: var(--muted); margin-top: 0.5rem; }
.module-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 1rem; }
.module-card { border: 1px solid var(--border); border-radius: 8px; padding: 1rem; background: var(--surface); }
.module-card h2 { margin-top: 0; font-size: 1.05rem; display: flex; justify-content: space-between; }
.card-counts { font-size: 0.8rem; color: var(--muted); margin-top: 0.5rem; }
.doc-item { margin: 1.5rem 0; padding: 1rem 1.25rem; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
.item-header { display: flex; align-items: baseline; gap: 0.6rem; flex-wrap: wrap; }
.sig { background: var(--code-bg); padding: 0.2rem 0.5rem; border-radius: 4px; flex: 1; overflow-x: auto; }
.badge { font-size: 0.72rem; font-weight: 600; text-transform: uppercase; padding: 0.15rem 0.45rem; border-radius: 4px; background: var(--code-bg); color: var(--accent); }
.src-link { font-size: 0.8rem; color: var(--muted); font-family: ui-monospace, monospace; }
.doc-block { margin-top: 0.75rem; }
.doc-block p:first-child { margin-top: 0; }
.doc-section { margin: 0.85rem 0 0.35rem; font-size: 0.95rem; }
.doc-code { background: var(--code-bg); padding: 0.75rem 1rem; border-radius: 6px; overflow-x: auto; border: 1px solid var(--border); }
.doc-link code { border-bottom: 1px dashed var(--accent); }
.member-list { padding-left: 1.25rem; }
.member-list li { margin: 0.4rem 0; }
.inline-doc { margin-top: 0.25rem; color: var(--muted); font-size: 0.92rem; }
.methods-group { margin-left: 0.5rem; border-left: 2px solid var(--border); padding-left: 0.9rem; }
.method-item { margin: 1rem 0; }
.source-view { border: 1px solid var(--border); border-radius: 8px; overflow-x: auto; background: var(--code-bg); }
.src-table { width: 100%; border-collapse: collapse; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.88rem; line-height: 1.45; }
.src-table td.ln { width: 48px; text-align: right; padding: 0 0.75rem; color: var(--muted); user-select: none; border-right: 1px solid var(--border); }
.src-table td.code { padding: 0 1rem; white-space: pre; }
.src-table tr:target { background: rgba(9, 105, 218, 0.16); }
.tok-kw { color: var(--kw); font-weight: 600; }
.tok-type { color: var(--type); }
.tok-str { color: var(--str); }
.tok-num { color: var(--num); }
.tok-cmt { color: var(--cmt); font-style: italic; }
.tok-attr { color: var(--attr); }
`

const docSearchJS = `
(function () {
  var root = document.documentElement;
  var saved = localStorage.getItem("lowlight_doc_theme");
  if (saved === "dark" || (!saved && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches)) {
    root.classList.add("dark");
  }
  var toggle = document.getElementById("theme-toggle");
  if (toggle) {
    toggle.addEventListener("click", function () {
      root.classList.toggle("dark");
      localStorage.setItem("lowlight_doc_theme", root.classList.contains("dark") ? "dark" : "light");
    });
  }
  var input = document.getElementById("doc-search");
  var box = document.getElementById("search-results");
  var prefix = document.body.getAttribute("data-rel-prefix") || "";
  var idx = window.LOWLIGHT_SEARCH_INDEX || [];
  function renderHits(q) {
    if (!q) {
      box.classList.add("hidden");
      box.innerHTML = "";
      return;
    }
    var ql = q.toLowerCase();
    var hits = idx.filter(function (it) {
      return it.name.toLowerCase().indexOf(ql) >= 0 || (it.desc && it.desc.toLowerCase().indexOf(ql) >= 0);
    }).slice(0, 30);
    if (hits.length === 0) {
      box.innerHTML = "<div class='search-hit'><small>No matches</small></div>";
      box.classList.remove("hidden");
      return;
    }
    function esc(s) {
      return String(s || "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
    }
    box.innerHTML = hits.map(function (h) {
      return "<a class='search-hit' href='" + prefix + esc(h.href) + "'><strong>" + esc(h.name) + "</strong> <em>(" + esc(h.kind) + " · " + esc(h.module) + ")</em><small>" + esc(h.desc || h.sig) + "</small></a>";
    }).join("");
    box.classList.remove("hidden");
  }
  if (input && box) {
    input.addEventListener("input", function () { renderHits(input.value.trim()); });
    document.addEventListener("keydown", function (e) {
      if (e.key === "/" && document.activeElement !== input) {
        e.preventDefault();
        input.focus();
      } else if (e.key === "Escape" && document.activeElement === input) {
        input.blur();
        box.classList.add("hidden");
      }
    });
  }
})();
`
