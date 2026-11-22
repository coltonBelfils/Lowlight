package docgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
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
