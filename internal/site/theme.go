package site

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lowlight/internal/markdown"
)

//go:embed assets/*
var assetFS embed.FS

func writeAssets(outDir string) error {
	entries, err := assetFS.ReadDir("assets")
	if err != nil {
		return err
	}
	dir := filepath.Join(outDir, "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		b, err := assetFS.ReadFile("assets/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

const logoSVG = `<svg class="logo" viewBox="0 0 32 32" aria-hidden="true"><defs><linearGradient id="lg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#f6b44b"/><stop offset="1" stop-color="#e2763a"/></linearGradient></defs><rect x="2" y="2" width="28" height="28" rx="8" fill="#1b1f3b"/><path d="M10 8v16h12" fill="none" stroke="url(#lg)" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"/><circle cx="22" cy="11" r="3" fill="url(#lg)"/></svg>`

func (s *Site) renderShell(p *Page) string {
	esc := markdown.Escape
	rel := func(to string) string { return relURL(p.Path, to) }
	var b strings.Builder
	title := p.Title
	if p.Layout != "home" {
		title += " · " + s.Title
	}
	desc := p.Summary
	if desc == "" {
		desc = s.Tagline
	}
	fmt.Fprintf(&b, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<meta name="description" content="%s">
<link rel="stylesheet" href="%s">
<script>(function(){var t=localStorage.getItem("ll-theme");if(t)document.documentElement.dataset.theme=t;})();</script>
</head>
<body class="layout-%s" data-root="%s">
`, esc(title), esc(desc), rel("assets/style.css"), p.Layout, esc(rel("index.html")[:len(rel("index.html"))-len("index.html")]))

	// Header.
	b.WriteString(`<header class="site-header"><div class="header-inner">`)
	b.WriteString(`<button class="menu-toggle" aria-label="Open navigation" data-toggle="sidebar">☰</button>`)
	fmt.Fprintf(&b, `<a class="brand" href="%s">%s<span>%s</span></a>`, rel("index.html"), logoSVG, esc(s.Title))
	b.WriteString(`<nav class="top-nav">`)
	for _, sec := range s.Sections {
		if !sec.InNav {
			continue
		}
		cls := ""
		if sec.ID == p.Section {
			cls = ` class="active"`
		}
		fmt.Fprintf(&b, `<a%s href="%s">%s</a>`, cls, rel(sec.Index), esc(sec.Title))
	}
	b.WriteString(`</nav><div class="header-tools">`)
	b.WriteString(`<button class="search-button" data-open-search aria-label="Search"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg><span>Search</span><kbd>/</kbd></button>`)
	b.WriteString(`<button class="theme-toggle" data-theme-toggle aria-label="Toggle dark mode"><span class="sun">☀</span><span class="moon">☾</span></button>`)
	b.WriteString(`</div></div></header>`)

	if p.Layout == "home" {
		b.WriteString(`<main class="home">`)
		b.WriteString(p.Body)
		b.WriteString(`</main>`)
	} else {
		b.WriteString(`<div class="page">`)
		b.WriteString(s.renderSidebar(p))
		b.WriteString(`<main class="content"><article class="doc">`)
		if crumbs := s.breadcrumbs(p); crumbs != "" {
			b.WriteString(crumbs)
		}
		b.WriteString(p.Body)
		b.WriteString(`</article>`)
		b.WriteString(s.renderPrevNext(p))
		b.WriteString(`</main>`)
		b.WriteString(renderTOC(p))
		b.WriteString(`</div>`)
	}

	fmt.Fprintf(&b, `<footer class="site-footer"><div class="footer-inner">%s</div></footer>`, s.renderFooter(p))
	b.WriteString(`<div class="search-overlay" hidden><div class="search-dialog" role="dialog" aria-label="Search the documentation"><div class="search-field"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg><input type="search" placeholder="Search guides, the spec, and packages…" autocomplete="off" spellcheck="false"><kbd>esc</kbd></div><ul class="search-results"></ul><div class="search-hint">↑↓ to move · ↵ to open</div></div></div>`)
	fmt.Fprintf(&b, "\n<script src=\"%s\"></script>\n<script src=\"%s\"></script>\n</body>\n</html>\n", rel("search-index.js"), rel("assets/site.js"))
	return b.String()
}

func (s *Site) sectionByID(id string) *Section {
	for i := range s.Sections {
		if s.Sections[i].ID == id {
			return &s.Sections[i]
		}
	}
	return nil
}

func (s *Site) breadcrumbs(p *Page) string {
	sec := s.sectionByID(p.Section)
	if sec == nil || sec.Index == p.Path {
		return ""
	}
	var parts []string
	parts = append(parts, fmt.Sprintf(`<a href="%s">%s</a>`, relURL(p.Path, sec.Index), markdown.Escape(sec.Title)))
	if p.Group != "" {
		parts = append(parts, `<span>`+markdown.Escape(p.Group)+`</span>`)
	}
	return `<nav class="crumbs">` + strings.Join(parts, `<span class="sep">/</span>`) + `</nav>`
}

func (s *Site) renderSidebar(p *Page) string {
	pages := s.SectionPages(p.Section)
	if len(pages) == 0 {
		return `<aside class="sidebar"></aside>`
	}
	var b strings.Builder
	b.WriteString(`<aside class="sidebar"><nav>`)
	sec := s.sectionByID(p.Section)
	if sec != nil {
		fmt.Fprintf(&b, `<div class="sidebar-title"><a href="%s">%s</a></div>`, relURL(p.Path, sec.Index), markdown.Escape(sec.Title))
	}
	group := "\x00"
	open := false
	for _, q := range pages {
		if sec != nil && q.Path == sec.Index {
			continue
		}
		if q.Group != group {
			if open {
				b.WriteString(`</ul>`)
			}
			group = q.Group
			if group != "" {
				fmt.Fprintf(&b, `<div class="sidebar-group">%s</div>`, markdown.Escape(group))
			}
			b.WriteString(`<ul>`)
			open = true
		}
		label := q.NavTitle
		if label == "" {
			label = q.Title
		}
		cls := ""
		if q == p {
			cls = ` class="active"`
		}
		fmt.Fprintf(&b, `<li><a%s href="%s">%s</a></li>`, cls, relURL(p.Path, q.Path), markdown.Escape(label))
	}
	if open {
		b.WriteString(`</ul>`)
	}
	// Section links for small screens, where the header navigation is hidden.
	b.WriteString(`<div class="sidebar-sections"><div class="sidebar-group">Sections</div><ul>`)
	for _, sec := range s.Sections {
		if sec.InNav {
			fmt.Fprintf(&b, `<li><a href="%s">%s</a></li>`, relURL(p.Path, sec.Index), markdown.Escape(sec.Title))
		}
	}
	b.WriteString(`</ul></div>`)
	b.WriteString(`</nav></aside>`)
	return b.String()
}

func renderTOC(p *Page) string {
	var items []markdown.Heading
	for i, h := range p.TOC {
		if i == 0 && (h.Level == 1 || (h.Level <= 2 && p.Layout != "api")) {
			continue // the page title
		}
		items = append(items, h)
	}
	if len(items) < 2 {
		return `<aside class="toc"></aside>`
	}
	minLv := 9
	for _, h := range items {
		if h.Level < minLv {
			minLv = h.Level
		}
	}
	var b strings.Builder
	b.WriteString(`<aside class="toc"><div class="toc-title">On this page</div><ul>`)
	for _, h := range items {
		if h.Level > minLv+1 {
			continue
		}
		cls := ""
		if h.Level > minLv {
			cls = ` class="sub"`
		}
		fmt.Fprintf(&b, `<li%s><a href="#%s">%s</a></li>`, cls, markdown.Escape(h.ID), markdown.Escape(h.Text))
	}
	b.WriteString(`</ul></aside>`)
	return b.String()
}

func (s *Site) renderPrevNext(p *Page) string {
	if p.prev == nil && p.next == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<nav class="prev-next">`)
	if p.prev != nil {
		fmt.Fprintf(&b, `<a class="prev" href="%s"><span>Previous</span>%s</a>`, relURL(p.Path, p.prev.Path), markdown.Escape(p.prev.Title))
	} else {
		b.WriteString(`<span></span>`)
	}
	if p.next != nil {
		fmt.Fprintf(&b, `<a class="next" href="%s"><span>Next</span>%s</a>`, relURL(p.Path, p.next.Path), markdown.Escape(p.next.Title))
	}
	b.WriteString(`</nav>`)
	return b.String()
}

func (s *Site) renderFooter(p *Page) string {
	var cols []string
	for _, sec := range s.Sections {
		pages := s.SectionPages(sec.ID)
		if len(pages) == 0 {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, `<div class="footer-col"><a class="footer-head" href="%s">%s</a>`, relURL(p.Path, sec.Index), markdown.Escape(sec.Title))
		n := 0
		for _, q := range pages {
			if q.Path == sec.Index || n >= 5 {
				continue
			}
			label := q.NavTitle
			if label == "" {
				label = q.Title
			}
			fmt.Fprintf(&b, `<a href="%s">%s</a>`, relURL(p.Path, q.Path), markdown.Escape(label))
			n++
		}
		b.WriteString(`</div>`)
		cols = append(cols, b.String())
	}
	out := `<div class="footer-cols">` + strings.Join(cols, "") + `</div>`
	if s.Footer != "" {
		out += `<div class="footer-note">` + s.Footer + `</div>`
	}
	return out
}
