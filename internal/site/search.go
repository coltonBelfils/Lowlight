package site

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SearchEntry is one item in the client-side search index.
type SearchEntry struct {
	Title   string `json:"t"`
	URL     string `json:"u"` // site-root-relative
	Section string `json:"s"`
	Kind    string `json:"k,omitempty"` // "page", "heading", or an API kind
	Desc    string `json:"d,omitempty"`
}

func (s *Site) writeSearchIndex(outDir string) error {
	var entries []SearchEntry
	for _, p := range s.Pages {
		if p.Layout == "home" {
			continue
		}
		secTitle := ""
		if sec := s.sectionByID(p.Section); sec != nil {
			secTitle = sec.Title
		}
		entries = append(entries, SearchEntry{Title: p.Title, URL: p.Path, Section: secTitle, Kind: "page", Desc: p.Summary})
		if p.Layout == "api" {
			continue // API symbols come from SearchExtra
		}
		for i, h := range p.TOC {
			if i == 0 || h.Level > 3 {
				continue
			}
			entries = append(entries, SearchEntry{Title: h.Text, URL: p.Path + "#" + h.ID, Section: secTitle + " › " + p.Title, Kind: "heading"})
		}
	}
	entries = append(entries, s.SearchExtra...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Kind == "page" && entries[j].Kind != "page" })
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	js := "window.LL_SEARCH=" + string(data) + ";\n"
	return os.WriteFile(filepath.Join(outDir, "search-index.js"), []byte(js), 0o644)
}

var (
	reHref = regexp.MustCompile(`(?:href|src)="([^"]*)"`)
	reID   = regexp.MustCompile(`\sid="([^"]*)"`)
)

// checkLinks verifies every relative href and src in the output, including fragments.
func checkLinks(outDir string) []Problem {
	ids := map[string]map[string]bool{}
	pages := map[string]string{}
	_ = filepath.WalkDir(outDir, func(fp string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(fp, ".html") {
			return nil
		}
		b, rerr := os.ReadFile(fp)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(outDir, fp)
		rel = filepath.ToSlash(rel)
		pages[rel] = string(b)
		m := map[string]bool{}
		for _, mm := range reID.FindAllStringSubmatch(string(b), -1) {
			m[htmlUnescape(mm[1])] = true
		}
		ids[rel] = m
		return nil
	})
	var probs []Problem
	seen := map[string]bool{}
	names := make([]string, 0, len(pages))
	for k := range pages {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, rel := range names {
		html := pages[rel]
		for _, m := range reHref.FindAllStringSubmatch(html, -1) {
			u := htmlUnescape(m[1])
			if u == "" || reScheme.MatchString(u) || strings.HasPrefix(u, "//") {
				continue
			}
			target, frag, _ := strings.Cut(u, "#")
			var tp string
			if target == "" {
				tp = rel
			} else {
				tp = path.Clean(path.Join(path.Dir(rel), target))
			}
			key := rel + "→" + u
			if seen[key] {
				continue
			}
			seen[key] = true
			if strings.HasSuffix(tp, ".html") {
				idset, ok := ids[tp]
				if !ok {
					probs = append(probs, Problem{rel, "broken link " + u})
					continue
				}
				if frag != "" && !idset[frag] {
					probs = append(probs, Problem{rel, "missing anchor " + u})
				}
				continue
			}
			if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(tp))); err != nil {
				probs = append(probs, Problem{rel, "broken link " + u})
			}
		}
	}
	return probs
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
}
