package docgen

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/parser"
	"lowlight/internal/sema"
	"lowlight/internal/token"
)

// DocLink represents one parsed `[`Symbol`]` or `[label][`Symbol`]` reference in a doc comment.
type DocLink struct {
	RawMatch string
	Label    string
	Target   string
	Start    int
	End      int
}

// ItemKind identifies the kind of documented entity.
type ItemKind string

const (
	KindModule          ItemKind = "module"
	KindStruct          ItemKind = "struct"
	KindEnum            ItemKind = "enum"
	KindInterface       ItemKind = "interface"
	KindDistinct        ItemKind = "distinct"
	KindFunc            ItemKind = "func"
	KindMethod          ItemKind = "method"
	KindConst           ItemKind = "const"
	KindExtern          ItemKind = "extern"
	KindField           ItemKind = "field"
	KindVariant         ItemKind = "variant"
	KindInterfaceMethod ItemKind = "interface_method"
	KindBuiltin         ItemKind = "builtin"
)

// SymbolEntry describes one indexed module, declaration, method, field, or variant.
type SymbolEntry struct {
	Name       string
	FullName   string // e.g., "List.push", "Error", or module slug name "std/fs"
	Kind       ItemKind
	ModuleSlug string
	ModuleName string // e.g., "std/fs" or "fs"
	Signature  string
	Doc        string
	FilePath   string
	Line       int
	Col        int
	HTMLHref   string // relative HTML href inside output root, e.g., "std_core_list.html#List.push"
	Anchor     string // anchor ID on module page, e.g., "List.push"
	SearchDesc string // short 1-line summary
}

// BrokenLink records an unresolved `[`Symbol`]` reference when running `--check-links`.
type BrokenLink struct {
	File     string
	Line     int
	ItemName string
	Target   string
}

func (b BrokenLink) String() string {
	if b.ItemName != "" {
		return fmt.Sprintf("%s:%d: unresolved doc link [`%s`] on %s", b.File, b.Line, b.Target, b.ItemName)
	}
	return fmt.Sprintf("%s:%d: unresolved doc link [`%s`]", b.File, b.Line, b.Target)
}

var docLinkPattern = regexp.MustCompile(`(?:\[([^\[\]]+)\])?\[` + "`" + `([^` + "`" + `\n\r]+)` + "`" + `\]`)

// ParseDocLinks extracts all `[`Symbol`]` and `[label][`Symbol`]` links outside fenced code blocks.
func ParseDocLinks(doc string) []DocLink {
	if doc == "" || !strings.Contains(doc, "[`") {
		return nil
	}
	type byteRange struct{ start, end int }
	var fencedRanges []byteRange

	// Find fenced code block byte spans so examples don't trigger link resolution.
	offset := 0
	inFence := false
	fenceStart := 0
	for _, line := range strings.SplitAfter(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				fenceStart = offset
			} else {
				inFence = false
				fencedRanges = append(fencedRanges, byteRange{fenceStart, offset + len(line)})
			}
		}
		offset += len(line)
	}
	if inFence {
		fencedRanges = append(fencedRanges, byteRange{fenceStart, len(doc)})
	}

	inFenced := func(pos int) bool {
		for _, r := range fencedRanges {
			if pos >= r.start && pos < r.end {
				return true
			}
		}
		return false
	}

	// Find inline code spans (`...`) excluding `[`...`]` patterns themselves by scanning backticks outside links.
	var links []DocLink
	matches := docLinkPattern.FindAllStringSubmatchIndex(doc, -1)
	for _, m := range matches {
		start, end := m[0], m[1]
		if inFenced(start) {
			continue
		}
		// Skip if preceded by an escaped backslash `\` or an unmatched backtick on the same line.
		if start > 0 && doc[start-1] == '\\' {
			continue
		}
		if hasOddBackticksBeforeOnLine(doc, start) {
			continue
		}
		target := strings.TrimSpace(doc[m[4]:m[5]])
		if target == "" {
			continue
		}
		label := target
		if m[2] >= 0 && m[3] >= 0 {
			label = doc[m[2]:m[3]]
		}
		links = append(links, DocLink{
			RawMatch: doc[start:end],
			Label:    label,
			Target:   target,
			Start:    start,
			End:      end,
		})
	}
	return links
}

func hasOddBackticksBeforeOnLine(s string, pos int) bool {
	lineStart := strings.LastIndexByte(s[:pos], '\n') + 1
	count := 0
	for i := lineStart; i < pos; i++ {
		if s[i] == '`' {
			count++
		}
	}
	return count%2 == 1
}

// SymbolIndex indexes all modules, declarations, methods, fields, and variants for link resolution and search.
type SymbolIndex struct {
	RootDir    string
	Modules    map[string]*SymbolEntry // module absPath -> module entry
	ByName     map[string][]*SymbolEntry
	ModAliases map[string]*SymbolEntry // "std/fs", "fs", "std/core/list", "list" -> module entry
	ModFiles   map[string]*ast.File    // module absPath -> AST file
}

var builtinUniverseTargets = map[string]string{
	"int": "Primitive integer type", "i8": "Signed 8-bit integer", "i16": "Signed 16-bit integer",
	"i32": "Signed 32-bit integer", "i64": "Signed 64-bit integer",
	"u8": "Unsigned 8-bit byte integer", "u16": "Unsigned 16-bit integer",
	"u32": "Unsigned 32-bit integer", "u64": "Unsigned 64-bit integer",
	"float": "Double-precision float (f64)", "f32": "Single-precision float", "f64": "Double-precision float",
	"bool": "Boolean type", "char": "Unicode scalar value", "string": "UTF-8 owned string",
	"void": "Unit void type", "none": "Empty optional value",
	"ptr": "Raw unsafe pointer ptr[T]", "ref": "Borrowed reference ref[T]",
	"println": "Built-in print with newline", "print": "Built-in print",
	"eprintln": "Built-in stderr print with newline", "eprint": "Built-in stderr print",
	"panic": "Unrecoverable abort", "assert": "Runtime assertion",
	"args": "CLI arguments", "env": "Environment variable lookup",
	"alloc": "Unsafe raw allocation", "free": "Unsafe deallocation",
	"sizeof": "Type size in bytes", "alignof": "Type alignment in bytes",
	"caller_file": "Track-caller file path", "caller_line": "Track-caller line number",
	"caller_col": "Track-caller column number", "exit": "Process exit",
}

// BuildSymbolIndex collects all declarations and doc comments from parsed/analyzed modules.
func BuildSymbolIndex(rootDir string, prog *sema.CheckedProgram, modPaths []string, overlays map[string]string) *SymbolIndex {
	idx := &SymbolIndex{
		RootDir:    rootDir,
		Modules:    make(map[string]*SymbolEntry),
		ByName:     make(map[string][]*SymbolEntry),
		ModAliases: make(map[string]*SymbolEntry),
		ModFiles:   make(map[string]*ast.File),
	}

	seenPaths := make(map[string]bool)
	addMod := func(absPath string, astFile *ast.File, src string) {
		if absPath == "" || seenPaths[absPath] || astFile == nil {
			return
		}
		seenPaths[absPath] = true
		idx.indexASTFile(absPath, astFile, src)
	}

	if prog != nil {
		for _, m := range prog.Modules {
			if m != nil {
				addMod(m.Path, m.File, overlays[m.Path])
			}
		}
	}

	for _, p := range modPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if seenPaths[abs] {
			continue
		}
		rep := diag.NewReporter()
		src := overlays[abs]
		if src == "" {
			continue
		}
		af, _ := parser.ParseFileWithComments(abs, src, rep)
		addMod(abs, af, src)
	}
	return idx
}

func ModuleDisplayName(rootDir, absPath string) string {
	if rootDir != "" {
		if rel, err := filepath.Rel(rootDir, absPath); err == nil && !strings.HasPrefix(rel, "..") {
			rel = filepath.ToSlash(rel)
			noExt := strings.TrimSuffix(rel, ".ll")
			parts := strings.Split(noExt, "/")
			if len(parts) >= 2 && parts[len(parts)-1] == parts[len(parts)-2] {
				return strings.Join(parts[:len(parts)-1], "/")
			}
			return noExt
		}
	}
	return strings.TrimSuffix(filepath.Base(absPath), ".ll")
}

func ModuleSlug(rootDir, absPath string) string {
	disp := ModuleDisplayName(rootDir, absPath)
	slug := strings.ReplaceAll(disp, "/", "_")
	slug = strings.ReplaceAll(slug, ".", "_")
	if slug == "" {
		slug = "module"
	}
	return slug
}

func (idx *SymbolIndex) addEntry(e *SymbolEntry) {
	idx.ByName[e.FullName] = append(idx.ByName[e.FullName], e)
	if e.Name != "" && e.Name != e.FullName {
		idx.ByName[e.Name] = append(idx.ByName[e.Name], e)
	}
}

func (idx *SymbolIndex) indexASTFile(absPath string, f *ast.File, src string) {
	modName := ModuleDisplayName(idx.RootDir, absPath)
	slug := ModuleSlug(idx.RootDir, absPath)
	htmlPage := slug + ".html"

	idx.ModFiles[absPath] = f
	modEntry := &SymbolEntry{
		Name:       filepath.Base(modName),
		FullName:   modName,
		Kind:       KindModule,
		ModuleSlug: slug,
		ModuleName: modName,
		Doc:        f.Doc,
		FilePath:   absPath,
		Line:       1,
		Col:        1,
		HTMLHref:   htmlPage,
		SearchDesc: firstSentence(f.Doc),
	}
	idx.Modules[absPath] = modEntry
	idx.ModAliases[modName] = modEntry
	idx.ModAliases[modName+".ll"] = modEntry
	base := strings.TrimSuffix(filepath.Base(absPath), ".ll")
	if _, exists := idx.ModAliases[base]; !exists {
		idx.ModAliases[base] = modEntry
	}

	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.StructDecl:
			if isPrivateOrTestOnly(v.Name, v.Attributes) {
				continue
			}
			sig := formatStructHeader(v)
			anchor := v.Name
			idx.addEntry(&SymbolEntry{
				Name:       v.Name,
				FullName:   v.Name,
				Kind:       KindStruct,
				ModuleSlug: slug,
				ModuleName: modName,
				Signature:  sig,
				Doc:        v.Doc,
				FilePath:   absPath,
				Line:       v.Span.Start.Line,
				Col:        v.Span.Start.Col,
				HTMLHref:   htmlPage + "#" + anchor,
				Anchor:     anchor,
				SearchDesc: firstSentence(v.Doc),
			})
			for _, fld := range v.Fields {
				if strings.HasPrefix(fld.Name, "_") {
					continue
				}
				fAnchor := v.Name + "." + fld.Name
				idx.addEntry(&SymbolEntry{
					Name:       fld.Name,
					FullName:   v.Name + "." + fld.Name,
					Kind:       KindField,
					ModuleSlug: slug,
					ModuleName: modName,
					Signature:  fmt.Sprintf("%s: %s", fld.Name, formatTypeStr(fld.Type)),
					Doc:        fld.Doc,
					FilePath:   absPath,
					Line:       fld.Span.Start.Line,
					Col:        fld.Span.Start.Col,
					HTMLHref:   htmlPage + "#" + fAnchor,
					Anchor:     fAnchor,
					SearchDesc: firstSentence(fld.Doc),
				})
			}

		case *ast.EnumDecl:
			if isPrivateOrTestOnly(v.Name, v.Attributes) {
				continue
			}
			sig := formatEnumHeader(v)
			anchor := v.Name
			idx.addEntry(&SymbolEntry{
				Name:       v.Name,
				FullName:   v.Name,
				Kind:       KindEnum,
				ModuleSlug: slug,
				ModuleName: modName,
				Signature:  sig,
				Doc:        v.Doc,
				FilePath:   absPath,
				Line:       v.Span.Start.Line,
				Col:        v.Span.Start.Col,
				HTMLHref:   htmlPage + "#" + anchor,
				Anchor:     anchor,
				SearchDesc: firstSentence(v.Doc),
			})
			for _, ev := range v.Variants {
				vAnchor := v.Name + "." + ev.Name
				vSig := ev.Name
				if ev.PayloadType != nil {
					vSig = fmt.Sprintf("%s: %s", ev.Name, formatTypeStr(ev.PayloadType))
				}
				idx.addEntry(&SymbolEntry{
					Name:       ev.Name,
					FullName:   v.Name + "." + ev.Name,
					Kind:       KindVariant,
					ModuleSlug: slug,
					ModuleName: modName,
					Signature:  vSig,
					Doc:        ev.Doc,
					FilePath:   absPath,
					Line:       ev.Span.Start.Line,
					Col:        ev.Span.Start.Col,
					HTMLHref:   htmlPage + "#" + vAnchor,
					Anchor:     vAnchor,
					SearchDesc: firstSentence(ev.Doc),
				})
			}

		case *ast.InterfaceDecl:
			if isPrivateOrTestOnly(v.Name, v.Attributes) {
				continue
			}
			sig := formatInterfaceHeader(v)
			anchor := v.Name
			idx.addEntry(&SymbolEntry{
				Name:       v.Name,
				FullName:   v.Name,
				Kind:       KindInterface,
				ModuleSlug: slug,
				ModuleName: modName,
				Signature:  sig,
				Doc:        v.Doc,
				FilePath:   absPath,
				Line:       v.Span.Start.Line,
				Col:        v.Span.Start.Col,
				HTMLHref:   htmlPage + "#" + anchor,
				Anchor:     anchor,
				SearchDesc: firstSentence(v.Doc),
			})
			for _, m := range v.Methods {
				mAnchor := v.Name + "." + m.Name
				idx.addEntry(&SymbolEntry{
					Name:       m.Name,
					FullName:   v.Name + "." + m.Name,
					Kind:       KindInterfaceMethod,
					ModuleSlug: slug,
					ModuleName: modName,
					Signature:  formatInterfaceMethodSig(m),
					Doc:        m.Doc,
					FilePath:   absPath,
					Line:       m.Span.Start.Line,
					Col:        m.Span.Start.Col,
					HTMLHref:   htmlPage + "#" + mAnchor,
					Anchor:     mAnchor,
					SearchDesc: firstSentence(m.Doc),
				})
			}

		case *ast.DistinctTypeDecl:
			if isPrivateOrTestOnly(v.Name, v.Attributes) {
				continue
			}
			anchor := v.Name
			idx.addEntry(&SymbolEntry{
				Name:       v.Name,
				FullName:   v.Name,
				Kind:       KindDistinct,
				ModuleSlug: slug,
				ModuleName: modName,
				Signature:  fmt.Sprintf("define %s %s", v.Name, formatTypeStr(v.Base)),
				Doc:        v.Doc,
				FilePath:   absPath,
				Line:       v.Span.Start.Line,
				Col:        v.Span.Start.Col,
				HTMLHref:   htmlPage + "#" + anchor,
				Anchor:     anchor,
				SearchDesc: firstSentence(v.Doc),
			})

		case *ast.ExternFnDecl:
			localName := v.Name
			if v.Alias != "" {
				localName = v.Alias
			}
			if isPrivateOrTestOnly(localName, v.Attributes) {
				continue
			}
			anchor := localName
			idx.addEntry(&SymbolEntry{
				Name:       localName,
				FullName:   localName,
				Kind:       KindExtern,
				ModuleSlug: slug,
				ModuleName: modName,
				Signature:  formatExternSig(v),
				Doc:        v.Doc,
				FilePath:   absPath,
				Line:       v.Span.Start.Line,
				Col:        v.Span.Start.Col,
				HTMLHref:   htmlPage + "#" + anchor,
				Anchor:     anchor,
				SearchDesc: firstSentence(v.Doc),
			})

		case *ast.MethodDecl:
			if isPrivateOrTestOnly(v.Name, v.Attributes) || strings.HasPrefix(v.RecvType, "_") {
				continue
			}
			fullName := v.RecvType + "." + v.Name
			idx.addEntry(&SymbolEntry{
				Name:       v.Name,
				FullName:   fullName,
				Kind:       KindMethod,
				ModuleSlug: slug,
				ModuleName: modName,
				Signature:  formatMethodSig(v),
				Doc:        v.Doc,
				FilePath:   absPath,
				Line:       v.Span.Start.Line,
				Col:        v.Span.Start.Col,
				HTMLHref:   htmlPage + "#" + fullName,
				Anchor:     fullName,
				SearchDesc: firstSentence(v.Doc),
			})

		case *ast.VarDecl:
			if !v.IsBind || isPrivateOrTestOnly(v.Name, v.Attributes) {
				continue
			}
			kind := KindConst
			sig := formatVarDeclSig(v, src)
			if _, isFn := v.Value.(*ast.FuncLit); isFn {
				kind = KindFunc
			}
			idx.addEntry(&SymbolEntry{
				Name:       v.Name,
				FullName:   v.Name,
				Kind:       kind,
				ModuleSlug: slug,
				ModuleName: modName,
				Signature:  sig,
				Doc:        v.Doc,
				FilePath:   absPath,
				Line:       v.Span.Start.Line,
				Col:        v.Span.Start.Col,
				HTMLHref:   htmlPage + "#" + v.Name,
				Anchor:     v.Name,
				SearchDesc: firstSentence(v.Doc),
			})
		}
	}
}

// ResolveLink resolves a doc link target from a given module and optional enclosing type name.
// Returns nil, true if it points to a known built-in universe type/function; nil, false if unresolved.
func (idx *SymbolIndex) ResolveLink(target, currentModPath, currentType string) (entry *SymbolEntry, isBuiltin bool) {
	t := strings.TrimSpace(target)
	if t == "" {
		return nil, false
	}
	// Strip generic brackets or trailing `()` for convenience (`List[T].push` -> `List.push`, `Error.new()` -> `Error.new`).
	clean := stripLinkDecorations(t)

	// 1. Direct module reference (`std/fs`, `std/core/list`, `fs`).
	if mod, ok := idx.ModAliases[clean]; ok {
		return mod, false
	}

	// 2. Module-qualified member (`fs.File`, `fs.File.read`, `strings.split`, `math.sqrt`).
	if dot := strings.IndexByte(clean, '.'); dot > 0 {
		prefix := clean[:dot]
		rest := clean[dot+1:]
		if mod, ok := idx.ModAliases[prefix]; ok {
			if found := idx.findInModule(mod.ModuleName, rest); found != nil {
				return found, false
			}
		}
	}

	currentModName := ""
	if cm, ok := idx.Modules[currentModPath]; ok {
		currentModName = cm.ModuleName
	}

	// 3. Relative to enclosing type (`push` inside `List` -> `List.push`).
	if currentType != "" && !strings.Contains(clean, ".") {
		if found := idx.findInModule(currentModName, currentType+"."+clean); found != nil {
			return found, false
		}
		if found := idx.findGlobal(currentType + "." + clean); found != nil {
			return found, false
		}
	}

	// 4. Current module exact `FullName` (e.g., `Error`, `List.push`, `Result.Err`).
	if currentModName != "" {
		if found := idx.findInModule(currentModName, clean); found != nil {
			return found, false
		}
	}

	// 5. Global exact `FullName` across all indexed modules.
	if found := idx.findGlobal(clean); found != nil {
		return found, false
	}

	// 6. Built-in universe type/function.
	if _, ok := builtinUniverseTargets[clean]; ok {
		return nil, true
	}

	return nil, false
}

func (idx *SymbolIndex) findInModule(modName, fullName string) *SymbolEntry {
	for _, e := range idx.ByName[fullName] {
		if e.ModuleName == modName && e.FullName == fullName {
			return e
		}
	}
	return nil
}

func (idx *SymbolIndex) findGlobal(fullName string) *SymbolEntry {
	candidates := idx.ByName[fullName]
	// Prefer exact FullName match first.
	for _, e := range candidates {
		if e.FullName == fullName {
			return e
		}
	}
	return nil
}

func stripLinkDecorations(s string) string {
	s = strings.TrimSuffix(s, "()")
	var sb strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				sb.WriteRune(r)
			}
		}
	}
	return sb.String()
}

func isPrivateOrTestOnly(name string, attrs []ast.Attribute) bool {
	if strings.HasPrefix(name, "_") {
		return true
	}
	if ast.FindAttribute(attrs, "test") != nil {
		return true
	}
	return false
}

func firstSentence(doc string) string {
	if doc == "" {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") {
			break
		}
		lines = append(lines, t)
	}
	s := strings.Join(lines, " ")
	if len(s) > 160 {
		return s[:157] + "..."
	}
	return s
}

// SortedEntries returns all indexed entries in deterministic module + line order.
func (idx *SymbolIndex) SortedEntries() []*SymbolEntry {
	seen := make(map[*SymbolEntry]bool)
	var out []*SymbolEntry
	for _, m := range idx.Modules {
		seen[m] = true
		out = append(out, m)
	}
	for _, list := range idx.ByName {
		for _, e := range list {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModuleName != out[j].ModuleName {
			return out[i].ModuleName < out[j].ModuleName
		}
		if out[i].Kind != out[j].Kind && (out[i].Kind == KindModule || out[j].Kind == KindModule) {
			return out[i].Kind == KindModule
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].FullName < out[j].FullName
	})
	return out
}

// -----------------------------------------------------------------------------
// Signature Formatting Helpers
// -----------------------------------------------------------------------------

func formatTypeParams(tps []ast.TypeParam) string {
	if len(tps) == 0 {
		return ""
	}
	parts := make([]string, len(tps))
	for i, tp := range tps {
		if len(tp.Bounds) > 0 {
			parts[i] = tp.Name + ": " + strings.Join(tp.Bounds, " + ")
		} else {
			parts[i] = tp.Name
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatTypeStr(t ast.TypeExpr) string {
	if t == nil {
		return ""
	}
	switch v := t.(type) {
	case *ast.NamedType:
		out := v.Name
		if v.Module != "" {
			out = v.Module + "." + out
		}
		if len(v.TypeArgs) > 0 {
			args := make([]string, len(v.TypeArgs))
			for i, a := range v.TypeArgs {
				args[i] = formatTypeStr(a)
			}
			out += "[" + strings.Join(args, ", ") + "]"
		}
		return out
	case *ast.OptionalType:
		return "?" + formatTypeStr(v.Elem)
	case *ast.MutBorrowType:
		return "&" + formatTypeStr(v.Elem)
	case *ast.OwnType:
		return "own " + formatTypeStr(v.Elem)
	case *ast.VectorType:
		return "[" + formatTypeStr(v.Elem) + "]"
	case *ast.StackArrayType:
		return "[N: " + formatTypeStr(v.Elem) + "]"
	case *ast.SetType:
		return "#[" + formatTypeStr(v.Elem) + "]"
	case *ast.MapType:
		return "#[" + formatTypeStr(v.Key) + ": " + formatTypeStr(v.Value) + "]"
	case *ast.TupleType:
		parts := make([]string, len(v.Elems))
		for i, el := range v.Elems {
			parts[i] = formatTypeStr(el)
		}
		return "+[" + strings.Join(parts, ", ") + "]"
	case *ast.FuncType:
		parts := make([]string, len(v.Params))
		for i, p := range v.Params {
			if p.Name != "" {
				parts[i] = p.Name + ": " + formatTypeStr(p.Type)
			} else {
				parts[i] = formatTypeStr(p.Type)
			}
		}
		out := "(" + strings.Join(parts, ", ") + ")"
		if v.Ret != nil {
			out += " " + formatTypeStr(v.Ret)
		}
		return out
	}
	return ""
}

func formatParamsStr(params []ast.Param) string {
	parts := make([]string, len(params))
	for i, p := range params {
		if p.Name != "" {
			parts[i] = p.Name + ": " + formatTypeStr(p.Type)
		} else {
			parts[i] = formatTypeStr(p.Type)
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func formatStructHeader(v *ast.StructDecl) string {
	packed := ""
	if v.Packed || ast.FindAttribute(v.Attributes, "packed") != nil {
		packed = "packed "
	}
	return fmt.Sprintf("define %s%s%s struct", packed, v.Name, formatTypeParams(v.TypeParams))
}

func formatEnumHeader(v *ast.EnumDecl) string {
	base := ""
	if v.BaseType != nil {
		base = " " + formatTypeStr(v.BaseType)
	}
	return fmt.Sprintf("define %s%s enum%s", v.Name, formatTypeParams(v.TypeParams), base)
}

func formatInterfaceHeader(v *ast.InterfaceDecl) string {
	return fmt.Sprintf("define %s%s interface", v.Name, formatTypeParams(v.TypeParams))
}

func formatMethodReceiver(mode ast.ReceiverMode, explicit ast.TypeExpr) string {
	if mode == ast.ReceiverMode(-1) {
		return ""
	}
	if explicit != nil {
		switch mode {
		case ast.RecvMut:
			return "self: &" + formatTypeStr(explicit)
		case ast.RecvOwn:
			return "self: own " + formatTypeStr(explicit)
		default:
			return "self: " + formatTypeStr(explicit)
		}
	}
	switch mode {
	case ast.RecvMut:
		return "&self"
	case ast.RecvOwn:
		return "own self"
	default:
		return "self"
	}
}

func formatInterfaceMethodSig(m ast.InterfaceMethod) string {
	var parts []string
	if r := formatMethodReceiver(m.RecvMode, nil); r != "" {
		parts = append(parts, r)
	}
	for _, p := range m.Params {
		parts = append(parts, p.Name+": "+formatTypeStr(p.Type))
	}
	ret := ""
	if m.Ret != nil {
		ret = " " + formatTypeStr(m.Ret)
	}
	return fmt.Sprintf("%s: (%s)%s", m.Name, strings.Join(parts, ", "), ret)
}

func formatMethodSig(v *ast.MethodDecl) string {
	var parts []string
	if r := formatMethodReceiver(v.RecvMode, v.RecvExplicitTy); r != "" {
		parts = append(parts, r)
	}
	for _, p := range v.Params {
		parts = append(parts, p.Name+": "+formatTypeStr(p.Type))
	}
	recvTParams := ""
	if len(v.RecvTypeParams) > 0 {
		recvTParams = "[" + strings.Join(v.RecvTypeParams, ", ") + "]"
	}
	ret := ""
	if v.Ret != nil {
		ret = " " + formatTypeStr(v.Ret)
	}
	return fmt.Sprintf("bind %s%s.%s%s = (%s)%s", v.RecvType, recvTParams, v.Name, formatTypeParams(v.TypeParams), strings.Join(parts, ", "), ret)
}

func formatExternSig(v *ast.ExternFnDecl) string {
	name := v.Name
	if v.Alias != "" {
		name += " as " + v.Alias
	}
	ret := ""
	if v.Ret != nil {
		ret = " " + formatTypeStr(v.Ret)
	}
	return fmt.Sprintf("extern %s: %s%s", name, formatParamsStr(v.Params), ret)
}

func formatVarDeclSig(v *ast.VarDecl, src string) string {
	if fl, isFn := v.Value.(*ast.FuncLit); isFn {
		ret := ""
		if fl.Ret != nil {
			ret = " " + formatTypeStr(fl.Ret)
		}
		return fmt.Sprintf("bind %s%s = %s%s", v.Name, formatTypeParams(v.TypeParams), formatParamsStr(fl.Params), ret)
	}
	ty := ""
	if v.Type != nil {
		ty = ": " + formatTypeStr(v.Type)
	}
	valSnippet := ""
	sp := v.Value.GetSpan()
	if src != "" && sp.Start.Offset >= 0 && sp.End.Offset <= len(src) && sp.Start.Offset < sp.End.Offset {
		raw := strings.TrimSpace(src[sp.Start.Offset:sp.End.Offset])
		if !strings.ContainsRune(raw, '\n') && len(raw) <= 60 {
			valSnippet = " = " + raw
		}
	}
	return fmt.Sprintf("bind %s%s%s", v.Name, ty, valSnippet)
}

// HighlightLowlightHTML tokenizes a Lowlight snippet/file into HTML spans by token kind.
func HighlightLowlightHTML(code string) string {
	rep := diag.NewReporter()
	toks, comments := lexer.LexWithComments("<highlight>", code, rep)
	return highlightSourceWithLexer(code, toks, comments)
}

func highlightSourceWithLexer(code string, toks []token.Token, comments []lexer.Comment) string {
	type spanStyle struct {
		start int
		end   int
		cls   string
	}
	var spans []spanStyle
	for _, tk := range toks {
		if tk.AutoSemi || tk.Span.End.Offset <= tk.Span.Start.Offset || tk.Span.End.Offset > len(code) {
			continue
		}
		var cls string
		switch {
		case tk.Kind >= token.KwLet && tk.Kind <= token.KwImport:
			cls = "tok-kw"
		case tk.Kind == token.KwSelf || tk.Kind == token.KwSelfType:
			cls = "tok-kw"
		case tk.Kind == token.IntLit || tk.Kind == token.FloatLit:
			cls = "tok-num"
		case tk.Kind == token.CharLit || tk.Kind == token.StringLit || tk.Kind == token.InterpStart || tk.Kind == token.InterpMid || tk.Kind == token.InterpEnd:
			cls = "tok-str"
		case tk.Kind == token.At:
			cls = "tok-attr"
		case tk.Kind == token.Ident && len(tk.Text) > 0 && tk.Text[0] >= 'A' && tk.Text[0] <= 'Z':
			cls = "tok-type"
		case isPrimitiveName(tk.Text):
			cls = "tok-type"
		}
		if cls != "" {
			s := tk.Span.Start.Offset
			e := tk.Span.End.Offset
			if tk.Kind == token.StringLit || tk.Kind == token.InterpStart {
				if s > 0 && (code[s-1] == '"' || code[s-1] == '`') {
					s--
				}
			}
			spans = append(spans, spanStyle{s, e, cls})
		}
	}
	for _, c := range comments {
		if c.Span.End.Offset > c.Span.Start.Offset && c.Span.End.Offset <= len(code) {
			spans = append(spans, spanStyle{c.Span.Start.Offset, c.Span.End.Offset, "tok-cmt"})
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	var sb strings.Builder
	pos := 0
	for _, sp := range spans {
		if sp.start < pos || sp.end > len(code) {
			continue
		}
		if sp.start > pos {
			sb.WriteString(htmlEscape(code[pos:sp.start]))
		}
		sb.WriteString(`<span class="`)
		sb.WriteString(sp.cls)
		sb.WriteString(`">`)
		sb.WriteString(htmlEscape(code[sp.start:sp.end]))
		sb.WriteString(`</span>`)
		pos = sp.end
	}
	if pos < len(code) {
		sb.WriteString(htmlEscape(code[pos:]))
	}
	return sb.String()
}

func isPrimitiveName(s string) bool {
	switch s {
	case "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64",
		"float", "f32", "f64", "bool", "char", "string", "void", "ptr", "ref":
		return true
	}
	return false
}

func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}
