package docgen

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/manifest"
	"lowlight/internal/sema"
)

// Options configures documentation generation and link checking.
type Options struct {
	Paths       []string
	OutputDir   string
	Format      string // "html" (default) or "md"
	CheckLinks  bool
	ProjectRoot string
}

// Result summarizes a docgen run.
type Result struct {
	ModulesCount int
	SymbolsCount int
	BrokenLinks  []BrokenLink
	OutputDir    string
}

// Generate parses/typechecks all Lowlight files in opts.Paths, checks intra-doc links,
// and writes HTML (or Markdown) documentation into opts.OutputDir when non-empty.
func Generate(opts Options) (*Result, error) {
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "html"
	}
	if format != "html" && format != "md" && format != "markdown" {
		return nil, fmt.Errorf("unsupported doc format %q (expected \"html\" or \"md\")", opts.Format)
	}
	if format == "markdown" {
		format = "md"
	}

	loaded, err := Load(opts.Paths, opts.ProjectRoot)
	if err != nil {
		return nil, err
	}
	files, rootDir, overlays, idx := loaded.Files, loaded.RootDir, loaded.Overlays, loaded.Index
	requestedSet := make(map[string]bool, len(files))
	for _, f := range files {
		requestedSet[f] = true
	}

	broken := idx.ValidateLinks(requestedSet)
	res := &Result{
		ModulesCount: len(files),
		SymbolsCount: len(idx.SortedEntries()),
		BrokenLinks:  broken,
		OutputDir:    opts.OutputDir,
	}

	if opts.CheckLinks && len(broken) > 0 {
		return res, fmt.Errorf("found %d unresolved intra-doc link(s)", len(broken))
	}

	if opts.OutputDir != "" {
		if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
			return res, err
		}
		switch format {
		case "html":
			return res, fmt.Errorf("HTML output is written by internal/site; call site.BuildAPIDocs")
		case "md":
			if err := writeMarkdownDocs(opts.OutputDir, rootDir, files, overlays, idx); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// Loaded is a parsed and indexed set of Lowlight source files.
type Loaded struct {
	Files    []string          // documented files (absolute, sorted, no test files)
	RootDir  string            // project root used for display names
	Overlays map[string]string // file contents
	Index    *SymbolIndex
}

// Load collects the .ll files under paths (skipping tests, hidden directories, editors/,
// vendor/, node_modules/, and docs/), type-checks them tolerantly, and indexes every
// documented declaration.
func Load(paths []string, projectRoot string) (*Loaded, error) {
	files, rootDir, err := collectInputFiles(paths, projectRoot)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .ll files found in specified paths")
	}
	overlays := make(map[string]string, len(files))
	for _, f := range files {
		raw, rErr := os.ReadFile(f)
		if rErr != nil {
			return nil, rErr
		}
		overlays[f] = string(raw)
	}
	// Run tolerant semantic analysis starting from the first file, passing all files as overlays
	// and also indexing every requested file directly so disconnected modules are all documented.
	rep := diag.NewReporter()
	prog := sema.CheckForIDE(files[0], overlays, rootDir, rep)
	idx := BuildSymbolIndex(rootDir, prog, files, overlays)
	return &Loaded{Files: files, RootDir: rootDir, Overlays: overlays, Index: idx}, nil
}

func collectInputFiles(paths []string, projectRoot string) ([]string, string, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	seen := make(map[string]bool)
	var files []string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, "", err
		}
		if !info.IsDir() {
			if !seen[abs] {
				seen[abs] = true
				files = append(files, abs)
			}
			continue
		}
		err = filepath.WalkDir(abs, func(fp string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				base := d.Name()
				if (strings.HasPrefix(base, ".") && base != ".") || base == "vendor" || base == "editors" || base == "node_modules" || base == "docs" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), ".ll") {
				if manifest.IsTestFile(d.Name()) {
					return nil
				}
				// Leave std/json.ll excluded when walking `std/` cleanly (unless passed explicitly).
				if filepath.Base(fp) == "json.ll" && filepath.Base(filepath.Dir(fp)) == "std" {
					return nil
				}
				if !seen[fp] {
					seen[fp] = true
					files = append(files, fp)
				}
			}
			return nil
		})
		if err != nil {
			return nil, "", err
		}
	}
	sort.Strings(files)

	root := projectRoot
	if root == "" && len(files) > 0 {
		root = detectRootForFiles(files)
	}
	if root != "" {
		if absR, err := filepath.Abs(root); err == nil {
			root = absR
		}
	}
	return files, root, nil
}

func detectRootForFiles(files []string) string {
	if ctx, err := manifest.DiscoverProject(files[0]); err == nil && ctx != nil && ctx.RootDir() != "" {
		return ctx.RootDir()
	}
	dir := filepath.Dir(files[0])
	for {
		if st, err := os.Stat(filepath.Join(dir, manifest.ManifestFilename)); err == nil && !st.IsDir() {
			return dir
		}
		if st, err := os.Stat(filepath.Join(dir, "std")); err == nil && st.IsDir() {
			return dir
		}
		if st, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if len(files) == 1 {
		return filepath.Dir(files[0])
	}
	common := filepath.Dir(files[0])
	for _, f := range files[1:] {
		for !strings.HasPrefix(f, common+string(filepath.Separator)) && common != "/" {
			parent := filepath.Dir(common)
			if parent == common {
				break
			}
			common = parent
		}
	}
	return common
}

// ValidateLinks validates all `[`Symbol`]` intra-doc links across the selected modules.
func (idx *SymbolIndex) ValidateLinks(targetMods map[string]bool) []BrokenLink {
	var broken []BrokenLink
	var paths []string
	for p := range idx.ModFiles {
		if len(targetMods) == 0 || targetMods[p] {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	checkText := func(file string, startLine int, itemName, enclosingType string, typeParams map[string]bool, doc string) {
		if doc == "" {
			return
		}
		links := ParseDocLinks(doc)
		for _, lk := range links {
			target := strings.TrimSpace(lk.Target)
			if typeParams[target] {
				continue
			}
			entry, isBuiltin := idx.ResolveLink(target, file, enclosingType)
			if entry == nil && !isBuiltin {
				line := startLine
				if line <= 0 {
					line = 1
				}
				broken = append(broken, BrokenLink{
					File:     file,
					Line:     line,
					ItemName: itemName,
					Target:   lk.Target,
				})
			}
		}
	}

	tpSet := func(tps []ast.TypeParam, extra ...string) map[string]bool {
		m := make(map[string]bool)
		for _, tp := range tps {
			m[tp.Name] = true
		}
		for _, ex := range extra {
			m[ex] = true
		}
		return m
	}

	for _, p := range paths {
		f := idx.ModFiles[p]
		if f == nil {
			continue
		}
		modLine := 1
		if f.DocSpan.Start.Line > 0 {
			modLine = f.DocSpan.Start.Line
		}
		checkText(p, modLine, ModuleDisplayName(idx.RootDir, p), "", nil, f.Doc)

		var walkDecls func(decls []ast.Decl)
		walkDecls = func(decls []ast.Decl) {
			for _, d := range decls {
				switch v := d.(type) {
				case *ast.StructDecl:
					if isPrivateOrTestOnly(v.Name, v.Attributes) {
						continue
					}
					tps := tpSet(v.TypeParams, "Self")
					line := v.Span.Start.Line
					if v.DocSpan.Start.Line > 0 {
						line = v.DocSpan.Start.Line
					}
					checkText(p, line, v.Name, v.Name, tps, v.Doc)
					for _, fld := range v.Fields {
						if !strings.HasPrefix(fld.Name, "_") {
							checkText(p, fld.Span.Start.Line, v.Name+"."+fld.Name, v.Name, tps, fld.Doc)
						}
					}
				case *ast.EnumDecl:
					if isPrivateOrTestOnly(v.Name, v.Attributes) {
						continue
					}
					tps := tpSet(v.TypeParams, "Self")
					line := v.Span.Start.Line
					if v.DocSpan.Start.Line > 0 {
						line = v.DocSpan.Start.Line
					}
					checkText(p, line, v.Name, v.Name, tps, v.Doc)
					for _, ev := range v.Variants {
						checkText(p, ev.Span.Start.Line, v.Name+"."+ev.Name, v.Name, tps, ev.Doc)
					}
				case *ast.InterfaceDecl:
					if isPrivateOrTestOnly(v.Name, v.Attributes) {
						continue
					}
					tps := tpSet(v.TypeParams, "Self")
					line := v.Span.Start.Line
					if v.DocSpan.Start.Line > 0 {
						line = v.DocSpan.Start.Line
					}
					checkText(p, line, v.Name, v.Name, tps, v.Doc)
					for _, m := range v.Methods {
						checkText(p, m.Span.Start.Line, v.Name+"."+m.Name, v.Name, tps, m.Doc)
					}
				case *ast.DistinctTypeDecl:
					if isPrivateOrTestOnly(v.Name, v.Attributes) {
						continue
					}
					line := v.Span.Start.Line
					if v.DocSpan.Start.Line > 0 {
						line = v.DocSpan.Start.Line
					}
					checkText(p, line, v.Name, v.Name, map[string]bool{"Self": true}, v.Doc)
				case *ast.ExternFnDecl:
					if isPrivateOrTestOnly(v.Name, v.Attributes) {
						continue
					}
					checkText(p, v.Span.Start.Line, v.Name, "", nil, v.Doc)
				case *ast.MethodDecl:
					if isPrivateOrTestOnly(v.Name, v.Attributes) || strings.HasPrefix(v.RecvType, "_") {
						continue
					}
					tps := tpSet(v.TypeParams, v.RecvTypeParams...)
					tps["Self"] = true
					checkText(p, v.Span.Start.Line, v.RecvType+"."+v.Name, v.RecvType, tps, v.Doc)
				case *ast.VarDecl:
					if (!v.IsBind && !v.IsConst) || isPrivateOrTestOnly(v.Name, v.Attributes) {
						continue
					}
					tps := tpSet(v.TypeParams)
					checkText(p, v.Span.Start.Line, v.Name, "", tps, v.Doc)
				case *ast.TopComptimeIfDecl:
					walkDecls(v.ThenDecls)
					if v.ElseIf != nil {
						walkDecls([]ast.Decl{v.ElseIf})
					}
					walkDecls(v.ElseDecls)
				}
			}
		}
		walkDecls(f.Decls)
	}
	return broken
}
