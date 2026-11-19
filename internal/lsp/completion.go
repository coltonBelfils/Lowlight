package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/manifest"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

const completionSentinel = "__ll_complete__"

func (s *Server) computeCompletions(filePath string, src string, pos Position) *CompletionList {
	offset := LSPToOffset(src, pos)
	if offset > len(src) {
		offset = len(src)
	}

	// 1. Check `import "..."` path completion first
	if impList := s.completeImportPath(filePath, src, offset); impList != nil {
		return impList
	}

	// Extract any identifier prefix immediately before the cursor (`foo` in `x.foo|` or `us` in `us|`).
	prefixStart := offset
	for prefixStart > 0 && isIdentOrQuestChar(src[prefixStart-1]) {
		// Stop before `?.`! If `src[prefixStart-1] == '?'` and `src[prefixStart] == '.'`, `?` belongs to `?.`, not prefix.
		if src[prefixStart-1] == '?' && prefixStart == offset {
			// Check if `?` is a prefix type `?int` (preceded by `:`, `[`, `(`, `,`, whitespace) vs suffix `user?`!
			if prefixStart == 1 || !isIdentChar(src[prefixStart-2]) {
				break
			}
		}
		prefixStart--
	}
	prefix := src[prefixStart:offset]
	replaceRange := Range{
		Start: OffsetToLSP(src, prefixStart),
		End:   OffsetToLSP(src, offset),
	}

	// 2. Check `.` / `?.` member access completion (`expr.|`, `expr.pre|`, `opt?.|`, `Enum.|`, `mod.|`, `Arena.|`)
	if prefixStart >= 1 && src[prefixStart-1] == '.' {
		// Ensure it's not a `..` range operator (`0..10`) or float literal (`3.`)
		isDotDot := prefixStart >= 2 && src[prefixStart-2] == '.'
		isFloatDot := prefixStart >= 2 && src[prefixStart-2] >= '0' && src[prefixStart-2] <= '9'
		if !isDotDot && !isFloatDot {
			dotOffset := prefixStart - 1
			return s.completeDotMember(filePath, src, dotOffset, prefixStart, offset, prefix, replaceRange)
		}
	}

	// Ensure we have an up-to-date CheckedProgram for the current buffer
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)

	// Tokenize `src[:prefixStart]` to determine syntactic context (struct literal field vs type annotation vs expression)
	silentRep := diag.NewReporter()
	rawToks := lexer.Lex(filePath, src[:prefixStart], silentRep)
	var toks []token.Token
	for _, tk := range rawToks {
		if tk.Kind == token.EOF || tk.AutoSemi {
			continue
		}
		toks = append(toks, tk)
	}

	// 3. Check Struct Literal field completion (`Point[|]` or `Point[x: 1, |]`)
	if stItems, ok := s.tryCompleteStructLitFields(prog, mod, toks, prefix, replaceRange); ok {
		return &CompletionList{IsIncomplete: false, Items: stItems}
	}

	// 4. Check Type Annotation position (`let x: |`, `(a: |)`, `define Foo |`, `[T: |]`)
	if isTypeAnnotationContext(toks) {
		items := s.completeTypeNames(prog, mod, prefix, replaceRange, isParamTypeContext(toks))
		return &CompletionList{IsIncomplete: false, Items: items}
	}

	// 5. General expression / statement / top-level completion
	items := s.completeScopedSymbols(prog, mod, src, offset, prefix, replaceRange, len(toks) == 0 || isTopLevelContext(toks))
	return &CompletionList{IsIncomplete: false, Items: items}
}

// -----------------------------------------------------------------------------
// 1. Import Path Completion (`import "std/..."` and relative `.ll` modules)
// -----------------------------------------------------------------------------

func (s *Server) completeImportPath(filePath string, src string, offset int) *CompletionList {
	lineStart := offset
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	before := src[lineStart:offset]
	trimmed := strings.TrimLeft(before, " \t")
	if !strings.HasPrefix(trimmed, "import") {
		return nil
	}
	afterKw := strings.TrimLeft(trimmed[len("import"):], " \t")
	if !strings.HasPrefix(afterKw, "\"") {
		return nil
	}
	typedInsideQuote := afterKw[1:]
	if strings.Contains(typedInsideQuote, "\"") {
		return nil // already past closing quote
	}

	quoteContentStart := offset - len(typedInsideQuote)
	quoteContentEnd := offset
	if quoteContentEnd < len(src) && src[quoteContentEnd] != '"' && src[quoteContentEnd] != '\n' {
		for quoteContentEnd < len(src) && src[quoteContentEnd] != '"' && src[quoteContentEnd] != '\n' {
			quoteContentEnd++
		}
	}
	editRange := Range{
		Start: OffsetToLSP(src, quoteContentStart),
		End:   OffsetToLSP(src, quoteContentEnd),
	}

	seen := make(map[string]bool)
	var items []CompletionItem

	addCandidate := func(relPath, desc string, sortGroup string) {
		if seen[relPath] {
			return
		}
		seen[relPath] = true
		if typedInsideQuote != "" && !strings.HasPrefix(strings.ToLower(relPath), strings.ToLower(typedInsideQuote)) {
			return
		}
		items = append(items, CompletionItem{
			Label:      relPath,
			Kind:       KindModule,
			Detail:     desc,
			SortText:   sortGroup + "_" + relPath,
			FilterText: relPath,
			TextEdit: &TextEdit{
				Range:   editRange,
				NewText: relPath,
			},
		})
	}

	// Standard library packages (`std/<pkg>`)
	stdDescriptions := map[string]string{
		"std/chars":   "Character classification, ASCII case folding, radix digits, and UTF-8 lengths",
		"std/fs":      "Lowlight standard filesystem package (`File`, `read_file`, `write_file`, `exists`, ...)",
		"std/http":    "HTTP/1.1 client, server, router (`ServeMux`), URL parser, and test helpers",
		"std/math":    "Lowlight standard math package (`PI`, `sin`, `cos`, `clamp_int`, `lerp`, ...)",
		"std/net":     "Lowlight TCP networking package (`TcpStream`, `TcpListener`, `socketpair`)",
		"std/os":      "OS environment, paths, processes (`exec`), and system info",
		"std/pool":    "Generational handle pool (`Pool[T]`, `Handle`)",
		"std/sort":    "In-place sorting, comparators, binary search, and collection helpers",
		"std/strconv": "Integer, float, radix, and boolean string parsing and formatting",
		"std/strfmt":  "Number formatting, padding, alignment, case conversions, and quoting",
		"std/strings": "Extended string searching, splitting, trimming, and transformation helpers",
		"std/test":    "Test assertions (`assert_eq`, `assert_is_ok`, `format_diff`) for `@test` suites",
		"std/time":    "Monotonic `Instant`, `Duration`, wall-clock `DateTime`, and sleep helpers",
	}
	for _, stdMod := range []string{"std/math", "std/fs", "std/pool", "std/net", "std/http", "std/strings", "std/time", "std/os", "std/test", "std/chars", "std/sort", "std/strconv", "std/strfmt"} {
		addCandidate(stdMod, stdDescriptions[stdMod], "0")
	}
	dir := filepath.Dir(filePath)
	if stdDir := manifest.FindStdDir(s.projectRootFor(filePath), dir); stdDir != "" {
		if entries, err := os.ReadDir(stdDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "core" {
					rel := "std/" + e.Name()
					desc := stdDescriptions[rel]
					if desc == "" {
						desc = "Standard library package"
					}
					addCandidate(rel, desc, "0")
				}
			}
		}
	}

	// Project subpackages, workspace members, and manifest dependencies (or sibling `./<pkg>` dirs when standalone)
	if ctx, err := manifest.DiscoverProject(filePath); err == nil && ctx != nil {
		if ctx.ActiveManifest != nil && ctx.ActiveManifest.HasPackage {
			pkgName := ctx.ActiveManifest.Package.Name
			pkgRoot := ctx.ActiveManifest.Dir
			if entries, err := os.ReadDir(pkgRoot); err == nil {
				for _, e := range entries {
					if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "vendor" && e.Name() != "tests" {
						addCandidate(pkgName+"/"+e.Name(), "Local project package", "1")
					}
				}
			}
			for dep := range ctx.ActiveManifest.Dependencies {
				addCandidate(dep, "Manifest dependency", "1")
			}
			for dep := range ctx.ActiveManifest.DevDependencies {
				addCandidate(dep, "Manifest dev-dependency", "1")
			}
		}
		for memberName := range ctx.WorkspaceMembers {
			addCandidate(memberName, "Workspace member package", "1")
		}
	} else {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "vendor" && e.Name() != "editors" && e.Name() != "node_modules" {
					addCandidate("./"+e.Name(), "Local Lowlight package", "1")
				}
			}
		}
	}

	return &CompletionList{IsIncomplete: false, Items: items}
}

// -----------------------------------------------------------------------------
// 2. Dot & Optional-Chain (`expr.`, `opt?.`, `Type.`, `mod.`) Member Completion
// -----------------------------------------------------------------------------

func (s *Server) completeDotMember(
	filePath string,
	src string,
	dotOffset int,
	prefixStart int,
	offset int,
	prefix string,
	replaceRange Range,
) *CompletionList {
	// Splice phantom identifier `__ll_complete__` at `[prefixStart:offset]` so the parser
	// builds a valid `*ast.MemberExpr{Base: ..., Member: "__ll_complete__"}` and sema resolves `Base`'s exact type!
	shadowSrc := src[:prefixStart] + completionSentinel + src[offset:]
	overlays := s.snapshotOverlays()
	overlays[filePath] = shadowSrc

	rep := diag.NewReporter()
	prog := sema.CheckForIDE(filePath, overlays, s.projectRootFor(filePath), rep)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return &CompletionList{Items: nil}
	}

	targetMem := findSentinelMemberExpr(mod.File)
	if targetMem == nil {
		return &CompletionList{Items: nil}
	}

	hasVisualQuestionBeforeDot := dotOffset >= 1 && src[dotOffset-1] == '?'

	// Case 2A: Check if `targetMem.Base` is a module alias (`fs.`), `Arena.`, `Enum.`, `Struct.`, or `GenStruct.`!
	if baseId, isId := targetMem.Base.(*ast.IdentExpr); isId && !baseId.IsOpt && !targetMem.QuestDot {
		baseInfo := lookupExprInfo(prog, targetMem.Base)
		isLocalOrGlobalVar := baseInfo != nil && baseInfo.Sym != nil
		if !isLocalOrGlobalVar {
			// Module alias `mod.`
			if targetMod, isMod := mod.Aliased[baseId.Name]; isMod {
				return &CompletionList{
					IsIncomplete: false,
					Items:        s.completeModuleExports(prog, mod, targetMod, prefix, replaceRange),
				}
			}
			// `Arena.` static constructors
			if baseId.Name == "Arena" {
				return &CompletionList{
					IsIncomplete: false,
					Items:        completeArenaStatics(prefix, replaceRange),
				}
			}
			// Generic struct template `Pool.` or `Pool[int].`
			if genSt := lookupGenStruct(mod, "", baseId.Name); genSt != nil {
				return &CompletionList{
					IsIncomplete: false,
					Items:        s.completeTypeNamespace(prog, mod, nil, genSt.Decl.Name, nil, prefix, replaceRange),
				}
			}
			// Generic enum template `Result.` or `Result[int, string].`
			if genEn := lookupGenEnum(mod, "", baseId.Name); genEn != nil {
				items := s.completeGenericEnumVariants(prog, mod, genEn.Mod, genEn.Decl, prefix, replaceRange)
				items = append(items, s.completeTypeNamespace(prog, mod, nil, genEn.Decl.Name, nil, prefix, replaceRange)...)
				return &CompletionList{
					IsIncomplete: false,
					Items:        items,
				}
			}
			// Concrete nominal type (`Enum.`, `Point.`, `UserID.`)
			if nomTy := lookupNominalType(mod, "", baseId.Name); nomTy != nil {
				if en, isEn := nomTy.(*types.Enum); isEn {
					items := s.completeEnumVariants(prog, mod, en, prefix, replaceRange)
					items = append(items, s.completeTypeNamespace(prog, mod, en, baseId.Name, nil, prefix, replaceRange)...)
					return &CompletionList{
						IsIncomplete: false,
						Items:        items,
					}
				}
				return &CompletionList{
					IsIncomplete: false,
					Items:        s.completeTypeNamespace(prog, mod, nomTy, baseId.Name, nil, prefix, replaceRange),
				}
			}
		}
	}

	// Case 2B: `Pool[int].` (where parser built `IndexExpr{Base: Ident("Pool"), Index: ...}` before `.`)
	if idxBase, isIdx := targetMem.Base.(*ast.IndexExpr); isIdx {
		if id, isId := idxBase.Base.(*ast.IdentExpr); isId {
			if genSt := lookupGenStruct(mod, "", id.Name); genSt != nil {
				targs := tryResolveIndexAsTypeArgs(mod, idxBase)
				return &CompletionList{
					IsIncomplete: false,
					Items:        s.completeTypeNamespace(prog, mod, nil, genSt.Decl.Name, targs, prefix, replaceRange),
				}
			}
			if genEn := lookupGenEnum(mod, "", id.Name); genEn != nil {
				targs := tryResolveIndexAsTypeArgs(mod, idxBase)
				items := s.completeGenericEnumVariants(prog, mod, genEn.Mod, genEn.Decl, prefix, replaceRange)
				items = append(items, s.completeTypeNamespace(prog, mod, nil, genEn.Decl.Name, targs, prefix, replaceRange)...)
				return &CompletionList{
					IsIncomplete: false,
					Items:        items,
				}
			}
		}
		if bm, isBM := idxBase.Base.(*ast.MemberExpr); isBM {
			if modId, isId := bm.Base.(*ast.IdentExpr); isId {
				if targetMod, isMod := mod.Aliased[modId.Name]; isMod {
					if genSt, ok := targetMod.GenericStructs[bm.Member]; ok {
						targs := tryResolveIndexAsTypeArgs(mod, idxBase)
						return &CompletionList{
							IsIncomplete: false,
							Items:        s.completeTypeNamespace(prog, mod, nil, genSt.Decl.Name, targs, prefix, replaceRange),
						}
					}
					if genEn, ok := targetMod.GenericEnums[bm.Member]; ok {
						targs := tryResolveIndexAsTypeArgs(mod, idxBase)
						items := s.completeGenericEnumVariants(prog, mod, targetMod, genEn.Decl, prefix, replaceRange)
						items = append(items, s.completeTypeNamespace(prog, mod, nil, genEn.Decl.Name, targs, prefix, replaceRange)...)
						return &CompletionList{
							IsIncomplete: false,
							Items:        items,
						}
					}
				}
			}
		}
	}

	// Case 2C: `mod.Enum.` (`MemberExpr{Base: Ident("mod"), Member: "Enum"}`)
	if bm, isBM := targetMem.Base.(*ast.MemberExpr); isBM {
		if modId, isId := bm.Base.(*ast.IdentExpr); isId {
			if targetMod, isMod := mod.Aliased[modId.Name]; isMod {
				if en, isEn := targetMod.Types[bm.Member].(*types.Enum); isEn {
					items := s.completeEnumVariants(prog, mod, en, prefix, replaceRange)
					items = append(items, s.completeTypeNamespace(prog, mod, en, bm.Member, nil, prefix, replaceRange)...)
					return &CompletionList{
						IsIncomplete: false,
						Items:        items,
					}
				}
				if genSt, ok := targetMod.GenericStructs[bm.Member]; ok {
					return &CompletionList{
						IsIncomplete: false,
						Items:        s.completeTypeNamespace(prog, mod, nil, genSt.Decl.Name, nil, prefix, replaceRange),
					}
				}
				if genEn, ok := targetMod.GenericEnums[bm.Member]; ok {
					items := s.completeGenericEnumVariants(prog, mod, targetMod, genEn.Decl, prefix, replaceRange)
					items = append(items, s.completeTypeNamespace(prog, mod, nil, genEn.Decl.Name, nil, prefix, replaceRange)...)
					return &CompletionList{
						IsIncomplete: false,
						Items:        items,
					}
				}
			}
		}
	}

	// Case 2D: Value receiver expression (`pt.`, `v.`, `user?.`, `find(1)?.`, `arena_ref.`)
	baseInfo := lookupExprInfo(prog, targetMem.Base)
	var recvTy types.Type
	if baseInfo != nil {
		recvTy = baseInfo.Type
	}
	if recvTy == nil || recvTy == types.TypeInvalid {
		return &CompletionList{Items: nil}
	}

	isOptChain := false
	var autoInsertQuestEdit []TextEdit
	if optTy, isOpt := recvTy.(*types.Optional); isOpt {
		isOptChain = true
		recvTy = optTy.Elem
		if !hasVisualQuestionBeforeDot {
			dotPos := OffsetToLSP(src, dotOffset)
			autoInsertQuestEdit = []TextEdit{{
				Range:   Range{Start: dotPos, End: dotPos},
				NewText: "?",
			}}
		}
	}
	// Unwrap `ref[T]` so arena references expose all fields & methods of `T`
	isArenaRef := false
	if refTy, isRef := recvTy.(*types.Ref); isRef {
		isArenaRef = true
		recvTy = refTy.Elem
	}

	items := s.completeValueMembers(prog, mod, recvTy, isOptChain, isArenaRef, prefix, replaceRange, autoInsertQuestEdit)
	return &CompletionList{IsIncomplete: false, Items: items}
}

func (s *Server) completeValueMembers(
	prog *sema.CheckedProgram,
	curMod *sema.Module,
	recvTy types.Type,
	isOptChain bool,
	isArenaRef bool,
	prefix string,
	replaceRange Range,
	extraEdits []TextEdit,
) []CompletionItem {
	var items []CompletionItem
	seen := make(map[string]bool)
	curModPath := ""
	if curMod != nil {
		curModPath = curMod.Path
	}

	wrapOptTypeStr := func(t types.Type) string {
		if t == nil {
			return ""
		}
		if isOptChain && !t.Equals(types.TypeVoid) {
			return types.NewOptional(t).String()
		}
		return t.String()
	}

	// 1. Struct fields
	if st, isSt := recvTy.(*types.Struct); isSt {
		samePkg := curMod == nil || prog == nil || prog.SamePackageDir(curMod, st.PkgDir, "") || prog.SamePackage(curMod, prog.StructMods[st])
		stModPath := curModPath
		if prog != nil && prog.StructMods[st] != nil && prog.StructMods[st].Path != "" {
			stModPath = prog.StructMods[st].Path
		}
		for idx, f := range st.Fields {
			if !samePkg && !sema.IsExported(f.Name) {
				continue
			}
			if !matchesPrefix(f.Name, prefix) {
				continue
			}
			seen[f.Name] = true
			fieldTyStr := wrapOptTypeStr(f.Type)
			docVal := fmt.Sprintf("```lowlight\n%s.%s: %s\n```", st.String(), f.Name, fieldTyStr)
			if rendered := s.renderDocMarkdown(prog, stModPath, st.Name, f.Doc, "", 0, false); rendered != "" {
				docVal = appendRenderedDoc(docVal, rendered)
			}
			items = append(items, CompletionItem{
				Label:               f.Name,
				Kind:                KindField,
				Detail:              fmt.Sprintf("field %s: %s", f.Name, fieldTyStr),
				SortText:            fmt.Sprintf("0_%03d_%s", idx, f.Name),
				FilterText:          f.Name,
				InsertTextFormat:    InsertPlainText,
				TextEdit:            &TextEdit{Range: replaceRange, NewText: f.Name},
				AdditionalTextEdits: extraEdits,
				Documentation: &MarkupContent{
					Kind:  "markdown",
					Value: docVal,
				},
			})
		}
	}

	// 1b. Tuple elements (e.g. .0, .1, ...)
	var tup *types.Tuple
	typeName := recvTy.String()
	if t, isTup := recvTy.(*types.Tuple); isTup {
		tup = t
	} else if d, isDist := recvTy.(*types.Distinct); isDist {
		if dtTup, isTup := d.Base.(*types.Tuple); isTup {
			tup = dtTup
			typeName = d.Name
		}
	}
	if tup != nil {
		for idx, el := range tup.Elems {
			idxStr := strconv.Itoa(idx)
			if !matchesPrefix(idxStr, prefix) {
				continue
			}
			seen[idxStr] = true
			elemTyStr := wrapOptTypeStr(el)
			items = append(items, CompletionItem{
				Label:               idxStr,
				Kind:                KindField,
				Detail:              fmt.Sprintf("element %s: %s", idxStr, elemTyStr),
				SortText:            fmt.Sprintf("0_%03d_%s", idx, idxStr),
				FilterText:          idxStr,
				InsertTextFormat:    InsertPlainText,
				TextEdit:            &TextEdit{Range: replaceRange, NewText: idxStr},
				AdditionalTextEdits: extraEdits,
				Documentation: &MarkupContent{
					Kind:  "markdown",
					Value: fmt.Sprintf("```lowlight\n%s.%s: %s\n```", typeName, idxStr, elemTyStr),
				},
			})
		}
	}

	// 2. Interface methods
	if ifc, isIfc := recvTy.(*types.Interface); isIfc {
		samePkg := curMod == nil || prog == nil || prog.SamePackageDir(curMod, ifc.PkgDir, "") || prog.SamePackage(curMod, prog.InterfaceMods[ifc])
		ifcModPath := curModPath
		if prog != nil && prog.InterfaceMods[ifc] != nil && prog.InterfaceMods[ifc].Path != "" {
			ifcModPath = prog.InterfaceMods[ifc].Path
		}
		for _, m := range ifc.Methods {
			if !samePkg && !sema.IsExported(m.Name) {
				continue
			}
			if isOptChain && m.RecvMode != types.ModeRead {
				continue
			}
			if !matchesPrefix(m.Name, prefix) {
				continue
			}
			seen[m.Name] = true
			sigStr := formatInterfaceMethodSig(m, isOptChain)
			snip := m.Name + "()"
			if len(m.Params) > 0 {
				snip = m.Name + "($1)"
			}
			docVal := fmt.Sprintf("```lowlight\n%s.%s: %s\n```", ifc.Name, m.Name, sigStr)
			if rendered := s.renderDocMarkdown(prog, ifcModPath, ifc.Name, m.Doc, "", 0, false); rendered != "" {
				docVal = appendRenderedDoc(docVal, rendered)
			}
			items = append(items, CompletionItem{
				Label:               m.Name,
				Kind:                KindMethod,
				Detail:              sigStr,
				SortText:            "1_" + m.Name,
				FilterText:          m.Name,
				InsertTextFormat:    InsertSnippet,
				TextEdit:            &TextEdit{Range: replaceRange, NewText: snip},
				AdditionalTextEdits: extraEdits,
				Documentation: &MarkupContent{
					Kind:  "markdown",
					Value: docVal,
				},
			})
		}
	}

	// 3. Concrete user methods on `recvTy`
	if prog != nil {
		if mset, ok := prog.MethodTable[sema.TypeMethodKey(recvTy)]; ok {
			for mName, fi := range mset {
				if curMod != nil && !sema.IsExported(mName) && !prog.SamePackage(curMod, fi.Mod) && !prog.SamePackageDir(curMod, types.TypePkgDir(recvTy), "") {
					continue
				}
				if fi.IsStaticAssoc || seen[mName] || !matchesPrefix(mName, prefix) {
					continue
				}
				if len(fi.Sig.Params) > 0 {
					rmode := fi.Sig.Params[0].Mode
					if isOptChain && rmode != types.ModeRead {
						continue
					}
					if isArenaRef && rmode == types.ModeOwn {
						continue
					}
				}
				seen[mName] = true
				sigStr := formatMethodInstanceSig(fi, isOptChain)
				snip := mName + "()"
				if len(fi.Sig.Params) > 1 {
					snip = mName + "($1)"
				}
				docVal := fmt.Sprintf("```lowlight\nbind %s = %s\n```", fi.Name, sigStr)
				fModPath := curModPath
				if fi.Mod != nil && fi.Mod.Path != "" {
					fModPath = fi.Mod.Path
				}
				recvName := ""
				if dot := strings.IndexByte(fi.Name, '.'); dot > 0 {
					recvName = fi.Name[:dot]
				}
				if rendered := s.renderDocMarkdown(prog, fModPath, recvName, fi.Doc, "", 0, false); rendered != "" {
					docVal = appendRenderedDoc(docVal, rendered)
				}
				items = append(items, CompletionItem{
					Label:               mName,
					Kind:                KindMethod,
					Detail:              sigStr,
					SortText:            "1_" + mName,
					FilterText:          mName,
					InsertTextFormat:    InsertSnippet,
					TextEdit:            &TextEdit{Range: replaceRange, NewText: snip},
					AdditionalTextEdits: extraEdits,
					Documentation: &MarkupContent{
						Kind:  "markdown",
						Value: docVal,
					},
				})
			}
		}

		// 4. Uninstantiated generic methods (`bind Pair[A, B].swap = ...`, `bind Pool[T].alloc = ...`)
		baseName, recvTypeArgs := baseTypeNameAndArgs(recvTy)
		if baseName != "" {
			if gset, ok := prog.GenMethods[baseName]; ok {
				for mName, gtmpl := range gset {
					if curMod != nil && !sema.IsExported(mName) && !prog.SamePackage(curMod, gtmpl.Mod) {
						continue
					}
					decl := gtmpl.MethodDecl
					if decl == nil || decl.RecvMode == ast.ReceiverMode(-1) || seen[mName] || !matchesPrefix(mName, prefix) {
						continue
					}
					if isOptChain && decl.RecvMode != ast.RecvRead {
						continue
					}
					if isArenaRef && decl.RecvMode == ast.RecvOwn {
						continue
					}
					seen[mName] = true
					sigStr := formatGenericMethodDeclSig(decl, recvTypeArgs, isOptChain)
					snip := mName + "()"
					if len(decl.Params) > 0 {
						snip = mName + "($1)"
					}
					docVal := fmt.Sprintf("```lowlight\nbind %s.%s = %s\n```", recvTy.String(), mName, sigStr)
					gModPath := curModPath
					if gtmpl.Mod != nil && gtmpl.Mod.Path != "" {
						gModPath = gtmpl.Mod.Path
					}
					if rendered := s.renderDocMarkdown(prog, gModPath, decl.RecvType, decl.Doc, "", 0, false); rendered != "" {
						docVal = appendRenderedDoc(docVal, rendered)
					}
					items = append(items, CompletionItem{
						Label:               mName,
						Kind:                KindMethod,
						Detail:              sigStr,
						SortText:            "1_" + mName,
						FilterText:          mName,
						InsertTextFormat:    InsertSnippet,
						TextEdit:            &TextEdit{Range: replaceRange, NewText: snip},
						AdditionalTextEdits: extraEdits,
						Documentation: &MarkupContent{
							Kind:  "markdown",
							Value: docVal,
						},
					})
				}
			}
		}
	}

	// 5. Built-in compiler methods (`len`, `push`, `pop`, `insert`, `alloc`, `copy`, `to_string`, ...)
	for idx, bm := range BuiltinMethodsForType(recvTy, isOptChain) {
		if seen[bm.Name] || !matchesPrefix(bm.Name, prefix) {
			continue
		}
		seen[bm.Name] = true
		docMD := fmt.Sprintf("```lowlight\n(%s) %s.%s%s\n```\n%s", bm.RecvMode, recvTy.String(), bm.Name, bm.Signature, bm.Summary)
		items = append(items, CompletionItem{
			Label:               bm.Name,
			Kind:                KindMethod,
			Detail:              bm.Signature,
			SortText:            fmt.Sprintf("2_%03d_%s", idx, bm.Name),
			FilterText:          bm.Name,
			InsertTextFormat:    InsertSnippet,
			TextEdit:            &TextEdit{Range: replaceRange, NewText: bm.Snippet},
			AdditionalTextEdits: extraEdits,
			Documentation: &MarkupContent{
				Kind:  "markdown",
				Value: docMD,
			},
		})
	}

	sortCompletionItems(items)
	return items
}

func (s *Server) completeEnumVariants(prog *sema.CheckedProgram, curMod *sema.Module, en *types.Enum, prefix string, replaceRange Range) []CompletionItem {
	var items []CompletionItem
	samePkg := curMod == nil || en == nil || prog == nil || prog.SamePackageDir(curMod, en.PkgDir, "") || prog.SamePackage(curMod, prog.EnumMods[en])
	enModPath := ""
	if prog != nil && en != nil && prog.EnumMods[en] != nil {
		enModPath = prog.EnumMods[en].Path
	} else if curMod != nil {
		enModPath = curMod.Path
	}
	for idx, v := range en.Variants {
		if !samePkg && !sema.IsExported(v.Name) {
			continue
		}
		if !matchesPrefix(v.Name, prefix) {
			continue
		}
		insFmt := InsertPlainText
		newText := v.Name
		detail := fmt.Sprintf("%s.%s", en.String(), v.Name)
		docCode := detail
		if !en.IsTagged {
			detail = fmt.Sprintf("%s.%s = %d (%s)", en.Name, v.Name, v.Value, en.Underlying.String())
			docCode = fmt.Sprintf("%s.%s // discriminant = %d", en.Name, v.Name, v.Value)
		} else {
			if v.Kind == types.VariantTuple {
				insFmt = InsertSnippet
				newText = v.Name + "($1)"
				if v.Payload != nil {
					detail = fmt.Sprintf("%s.%s(%s)", en.String(), v.Name, v.Payload.String())
					docCode = detail
				}
			}
		}
		docVal := fmt.Sprintf("```lowlight\n%s\n```", docCode)
		if rendered := s.renderDocMarkdown(prog, enModPath, en.Name, v.Doc, "", 0, false); rendered != "" {
			docVal = appendRenderedDoc(docVal, rendered)
		}
		items = append(items, CompletionItem{
			Label:            v.Name,
			Kind:             KindEnumMember,
			Detail:           detail,
			SortText:         fmt.Sprintf("0_%03d_%s", idx, v.Name),
			FilterText:       v.Name,
			InsertTextFormat: insFmt,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: newText},
			Documentation: &MarkupContent{
				Kind:  "markdown",
				Value: docVal,
			},
		})
	}
	return items
}

func (s *Server) completeGenericEnumVariants(prog *sema.CheckedProgram, curMod, targetMod *sema.Module, decl *ast.EnumDecl, prefix string, replaceRange Range) []CompletionItem {
	if decl == nil {
		return nil
	}
	samePkg := curMod == nil || targetMod == nil || prog == nil || prog.SamePackage(curMod, targetMod)
	modPath := ""
	if targetMod != nil {
		modPath = targetMod.Path
	} else if curMod != nil {
		modPath = curMod.Path
	}
	var items []CompletionItem
	for idx, v := range decl.Variants {
		if !samePkg && !sema.IsExported(v.Name) {
			continue
		}
		if !matchesPrefix(v.Name, prefix) {
			continue
		}
		insFmt := InsertPlainText
		newText := v.Name
		detail := fmt.Sprintf("%s.%s", decl.Name, v.Name)
		if v.PayloadType != nil {
			insFmt = InsertSnippet
			newText = v.Name + "($1)"
			detail = fmt.Sprintf("%s.%s(%s)", decl.Name, v.Name, formatTypeExpr(v.PayloadType))
		}
		var doc *MarkupContent
		if rendered := s.renderDocMarkdown(prog, modPath, decl.Name, v.Doc, "", 0, false); rendered != "" {
			docVal := fmt.Sprintf("```lowlight\n%s\n```", detail)
			doc = &MarkupContent{Kind: "markdown", Value: appendRenderedDoc(docVal, rendered)}
		}
		items = append(items, CompletionItem{
			Label:            v.Name,
			Kind:             KindEnumMember,
			Detail:           detail,
			SortText:         fmt.Sprintf("0_%03d_%s", idx, v.Name),
			FilterText:       v.Name,
			InsertTextFormat: insFmt,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: newText},
			Documentation:    doc,
		})
	}
	return items
}

func completeArenaStatics(prefix string, replaceRange Range) []CompletionItem {
	var items []CompletionItem
	if matchesPrefix("new", prefix) {
		items = append(items, CompletionItem{
			Label:            "new",
			Kind:             KindConstructor,
			Detail:           "() Arena",
			SortText:         "0_new",
			FilterText:       "new",
			InsertTextFormat: InsertSnippet,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: "new()"},
			Documentation: &MarkupContent{
				Kind:  "markdown",
				Value: "```lowlight\nArena.new() Arena\n```\nCreates a new bump-allocated `Arena` region with default chunk capacity (64 KiB).",
			},
		})
	}
	if matchesPrefix("with_capacity", prefix) {
		items = append(items, CompletionItem{
			Label:            "with_capacity",
			Kind:             KindConstructor,
			Detail:           "(bytes: int) Arena",
			SortText:         "1_with_capacity",
			FilterText:       "with_capacity",
			InsertTextFormat: InsertSnippet,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: "with_capacity(${1:65536})"},
			Documentation: &MarkupContent{
				Kind:  "markdown",
				Value: "```lowlight\nArena.with_capacity(bytes: int) Arena\n```\nCreates a new `Arena` pre-sized with initial chunk capacity `bytes`.",
			},
		})
	}
	return items
}

func (s *Server) completeTypeNamespace(
	prog *sema.CheckedProgram,
	curMod *sema.Module,
	nomTy types.Type,
	typeName string,
	typeArgs []types.Type,
	prefix string,
	replaceRange Range,
) []CompletionItem {
	var items []CompletionItem
	seen := make(map[string]bool)
	if prog == nil {
		return nil
	}
	curModPath := ""
	if curMod != nil {
		curModPath = curMod.Path
	}
	if nomTy != nil {
		if mset, ok := prog.MethodTable[sema.TypeMethodKey(nomTy)]; ok {
			for mName, fi := range mset {
				if curMod != nil && !sema.IsExported(mName) && !prog.SamePackage(curMod, fi.Mod) && !prog.SamePackageDir(curMod, types.TypePkgDir(nomTy), "") {
					continue
				}
				if !matchesPrefix(mName, prefix) {
					continue
				}
				seen[mName] = true
				kind := KindMethod
				if fi.IsStaticAssoc {
					kind = KindConstructor
				}
				snip := mName + "()"
				if len(fi.Sig.Params) > 0 {
					snip = mName + "($1)"
				}
				sig := formatFullFuncSig(fi)
				docVal := fmt.Sprintf("```lowlight\nbind %s = %s\n```", fi.Name, sig)
				fModPath := curModPath
				if fi.Mod != nil && fi.Mod.Path != "" {
					fModPath = fi.Mod.Path
				}
				if rendered := s.renderDocMarkdown(prog, fModPath, typeName, fi.Doc, "", 0, false); rendered != "" {
					docVal = appendRenderedDoc(docVal, rendered)
				}
				items = append(items, CompletionItem{
					Label:            mName,
					Kind:             kind,
					Detail:           sig,
					SortText:         "0_" + mName,
					FilterText:       mName,
					InsertTextFormat: InsertSnippet,
					TextEdit:         &TextEdit{Range: replaceRange, NewText: snip},
					Documentation:    &MarkupContent{Kind: "markdown", Value: docVal},
				})
			}
		}
	}
	if gset, ok := prog.GenMethods[typeName]; ok {
		for mName, gtmpl := range gset {
			if curMod != nil && !sema.IsExported(mName) && !prog.SamePackage(curMod, gtmpl.Mod) {
				continue
			}
			if seen[mName] || gtmpl.MethodDecl == nil || !matchesPrefix(mName, prefix) {
				continue
			}
			seen[mName] = true
			decl := gtmpl.MethodDecl
			isStatic := decl.RecvMode == ast.ReceiverMode(-1)
			kind := KindMethod
			if isStatic {
				kind = KindConstructor
			}
			totalParams := len(decl.Params)
			if !isStatic {
				totalParams++
			}
			snip := mName + "()"
			if totalParams > 0 {
				snip = mName + "($1)"
			}
			sig := formatGenericMethodDeclSig(decl, typeArgs, false)
			docVal := fmt.Sprintf("```lowlight\nbind %s.%s = %s\n```", decl.RecvType, mName, sig)
			gModPath := curModPath
			if gtmpl.Mod != nil && gtmpl.Mod.Path != "" {
				gModPath = gtmpl.Mod.Path
			}
			if rendered := s.renderDocMarkdown(prog, gModPath, decl.RecvType, decl.Doc, "", 0, false); rendered != "" {
				docVal = appendRenderedDoc(docVal, rendered)
			}
			items = append(items, CompletionItem{
				Label:            mName,
				Kind:             kind,
				Detail:           sig,
				SortText:         "0_" + mName,
				FilterText:       mName,
				InsertTextFormat: InsertSnippet,
				TextEdit:         &TextEdit{Range: replaceRange, NewText: snip},
				Documentation:    &MarkupContent{Kind: "markdown", Value: docVal},
			})
		}
	}
	sortCompletionItems(items)
	return items
}

func (s *Server) completeModuleExports(prog *sema.CheckedProgram, curMod, targetMod *sema.Module, prefix string, replaceRange Range) []CompletionItem {
	var items []CompletionItem
	seen := make(map[string]bool)
	samePkg := curMod != nil && targetMod != nil && prog != nil && prog.SamePackage(curMod, targetMod)
	tModPath := ""
	if targetMod != nil {
		tModPath = targetMod.Path
	}

	for name, sym := range targetMod.Symbols {
		if !samePkg && !sema.IsExported(name) {
			continue
		}
		if !matchesPrefix(name, prefix) {
			continue
		}
		seen[name] = true
		if sym.FuncInst != nil {
			snip := name + "()"
			if len(sym.FuncInst.Sig.Params) > 0 {
				snip = name + "($1)"
			}
			sig := formatFullFuncSig(sym.FuncInst)
			doc := sym.FuncInst.Doc
			if doc == "" {
				doc = sym.Doc
			}
			docVal := fmt.Sprintf("```lowlight\nbind %s = %s\n```", name, sig)
			if rendered := s.renderDocMarkdown(prog, tModPath, "", doc, "", 0, false); rendered != "" {
				docVal = appendRenderedDoc(docVal, rendered)
			}
			items = append(items, CompletionItem{
				Label:            name,
				Kind:             KindFunction,
				Detail:           sig,
				SortText:         "0_" + name,
				FilterText:       name,
				InsertTextFormat: InsertSnippet,
				TextEdit:         &TextEdit{Range: replaceRange, NewText: snip},
				Documentation:    &MarkupContent{Kind: "markdown", Value: docVal},
			})
		} else if sym.ExternFn != nil {
			snip := name + "()"
			if len(sym.ExternFn.Sig.Params) > 0 {
				snip = name + "($1)"
			}
			doc := sym.ExternFn.Doc
			if doc == "" {
				doc = sym.Doc
			}
			docVal := fmt.Sprintf("```lowlight\nextern %s: %s\n```", name, sym.ExternFn.Sig.String())
			if rendered := s.renderDocMarkdown(prog, tModPath, "", doc, "", 0, false); rendered != "" {
				docVal = appendRenderedDoc(docVal, rendered)
			}
			items = append(items, CompletionItem{
				Label:            name,
				Kind:             KindFunction,
				Detail:           "extern " + sym.ExternFn.Sig.String(),
				SortText:         "0_" + name,
				FilterText:       name,
				InsertTextFormat: InsertSnippet,
				TextEdit:         &TextEdit{Range: replaceRange, NewText: snip},
				Documentation:    &MarkupContent{Kind: "markdown", Value: docVal},
			})
		} else {
			tyStr := "<unknown>"
			if sym.Type != nil {
				tyStr = sym.Type.String()
			}
			var doc *MarkupContent
			if rendered := s.renderDocMarkdown(prog, tModPath, "", sym.Doc, "", 0, false); rendered != "" {
				docVal := fmt.Sprintf("```lowlight\nbind %s: %s\n```", name, tyStr)
				doc = &MarkupContent{Kind: "markdown", Value: appendRenderedDoc(docVal, rendered)}
			}
			items = append(items, CompletionItem{
				Label:            name,
				Kind:             KindConstant,
				Detail:           "bind " + name + ": " + tyStr,
				SortText:         "1_" + name,
				FilterText:       name,
				InsertTextFormat: InsertPlainText,
				TextEdit:         &TextEdit{Range: replaceRange, NewText: name},
				Documentation:    doc,
			})
		}
	}

	for name, gfn := range targetMod.GenericFuncs {
		if !samePkg && !sema.IsExported(name) {
			continue
		}
		if seen[name] || !matchesPrefix(name, prefix) {
			continue
		}
		seen[name] = true
		snip := name + "($1)"
		sig := formatGenericVarDeclSig(gfn.VarDecl)
		docVal := fmt.Sprintf("```lowlight\nbind %s%s\n```", name, sig)
		if gfn.VarDecl != nil {
			if rendered := s.renderDocMarkdown(prog, tModPath, "", gfn.VarDecl.Doc, "", 0, false); rendered != "" {
				docVal = appendRenderedDoc(docVal, rendered)
			}
		}
		items = append(items, CompletionItem{
			Label:            name,
			Kind:             KindFunction,
			Detail:           sig,
			SortText:         "0_" + name,
			FilterText:       name,
			InsertTextFormat: InsertSnippet,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: snip},
			Documentation:    &MarkupContent{Kind: "markdown", Value: docVal},
		})
	}

	for name, ty := range targetMod.Types {
		if !samePkg && !sema.IsExported(name) {
			continue
		}
		if name == "Printable" || name == "Copyable" || name == "Drop" {
			continue
		}
		if seen[name] || !matchesPrefix(name, prefix) {
			continue
		}
		seen[name] = true
		items = append(items, s.makeUserTypeCompletionItem(prog, tModPath, name, ty, replaceRange, "2_"))
	}

	for name, gst := range targetMod.GenericStructs {
		if !samePkg && !sema.IsExported(name) {
			continue
		}
		if seen[name] || !matchesPrefix(name, prefix) {
			continue
		}
		seen[name] = true
		items = append(items, s.makeGenericStructCompletionItem(prog, tModPath, gst.Decl, replaceRange, "2_"))
	}

	for name, gen := range targetMod.GenericEnums {
		if !samePkg && !sema.IsExported(name) {
			continue
		}
		if seen[name] || !matchesPrefix(name, prefix) {
			continue
		}
		seen[name] = true
		items = append(items, s.makeGenericEnumCompletionItem(prog, tModPath, gen.Decl, replaceRange, "2_"))
	}

	sortCompletionItems(items)
	return items
}

// -----------------------------------------------------------------------------
// 3. Struct Literal Field Completion (`Point[x: 1, |]`)
// -----------------------------------------------------------------------------

func (s *Server) tryCompleteStructLitFields(
	prog *sema.CheckedProgram,
	mod *sema.Module,
	toks []token.Token,
	prefix string,
	replaceRange Range,
) ([]CompletionItem, bool) {
	if mod == nil || len(toks) == 0 {
		return nil, false
	}

	// Build delimiter stack over `toks`
	type openDelim struct {
		idx  int
		kind token.Kind
	}
	var stack []openDelim
	for i, tk := range toks {
		switch tk.Kind {
		case token.LParen, token.LBracket, token.HashBrack, token.PlusBrack, token.LBrace:
			stack = append(stack, openDelim{idx: i, kind: tk.Kind})
		case token.RParen, token.RBracket, token.RBrace:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) == 0 {
		return nil, false
	}
	inner := stack[len(stack)-1]
	if inner.kind != token.LBracket || inner.idx == 0 {
		return nil, false
	}

	// Must be at field-name position: immediately after `[` OR immediately after a top-level `,` inside `[`!
	lastTok := toks[len(toks)-1]
	if lastTok.Kind != token.LBracket && lastTok.Kind != token.Comma {
		return nil, false
	}

	// Inspect tokens preceding `toks[inner.idx]` (`[`):
	// Make sure it's NOT `define Foo struct [` or `define Foo interface [` or `define Foo enum [`!
	beforeIdx := inner.idx - 1
	if toks[beforeIdx].Kind == token.KwStruct || toks[beforeIdx].Kind == token.KwInterface || toks[beforeIdx].Kind == token.KwEnum {
		return nil, false
	}

	// Either `TypeName[` or `mod.TypeName[` or `GenericName[T...][`
	var modAlias, typeName string
	if toks[beforeIdx].Kind == token.RBracket {
		// Find matching `[` for generic type arguments `Pair[int, string][`
		depth := 0
		matchOpen := -1
		for j := beforeIdx; j >= 0; j-- {
			if toks[j].Kind == token.RBracket {
				depth++
			} else if toks[j].Kind == token.LBracket {
				depth--
				if depth == 0 {
					matchOpen = j
					break
				}
			}
		}
		if matchOpen <= 0 || toks[matchOpen-1].Kind != token.Ident {
			return nil, false
		}
		typeName = toks[matchOpen-1].Text
		if matchOpen >= 3 && toks[matchOpen-2].Kind == token.Dot && toks[matchOpen-3].Kind == token.Ident {
			modAlias = toks[matchOpen-3].Text
		}
	} else if toks[beforeIdx].Kind == token.Ident {
		// Make sure this isn't `define Foo[` (generic type parameter list in `define`!)
		if beforeIdx >= 1 && toks[beforeIdx-1].Kind == token.KwDefine {
			return nil, false
		}
		if beforeIdx >= 2 && toks[beforeIdx-1].Text == "packed" && toks[beforeIdx-2].Kind == token.KwDefine {
			return nil, false
		}
		typeName = toks[beforeIdx].Text
		if beforeIdx >= 2 && toks[beforeIdx-1].Kind == token.Dot && toks[beforeIdx-2].Kind == token.Ident {
			modAlias = toks[beforeIdx-2].Text
		}
	} else {
		return nil, false
	}

	// Resolve `typeName` to concrete `*types.Struct` or `*GenericStructTemplate`
	var fields []structFieldCandidate
	if nom := lookupNominalType(mod, modAlias, typeName); nom != nil {
		if st, ok := nom.(*types.Struct); ok {
			samePkg := mod == nil || prog == nil || prog.SamePackageDir(mod, st.PkgDir, "") || prog.SamePackage(mod, prog.StructMods[st])
			for _, f := range st.Fields {
				if !samePkg && !sema.IsExported(f.Name) {
					continue
				}
				fields = append(fields, structFieldCandidate{
					Name:       f.Name,
					TypeStr:    f.Type.String(),
					HasDefault: f.HasDefault,
				})
			}
		}
	} else if gst := lookupGenStruct(mod, modAlias, typeName); gst != nil {
		samePkg := mod == nil || prog == nil || prog.SamePackage(mod, gst.Mod)
		for _, f := range gst.Decl.Fields {
			if !samePkg && !sema.IsExported(f.Name) {
				continue
			}
			fields = append(fields, structFieldCandidate{
				Name:       f.Name,
				TypeStr:    formatTypeExpr(f.Type),
				HasDefault: f.Default != nil,
			})
		}
	}

	if len(fields) == 0 {
		return nil, false
	}

	// Collect fields already initialized inside `[...]` at depth 0
	usedFields := make(map[string]bool)
	d := 0
	for k := inner.idx + 1; k < len(toks); k++ {
		switch toks[k].Kind {
		case token.LParen, token.LBracket, token.HashBrack, token.PlusBrack, token.LBrace:
			d++
		case token.RParen, token.RBracket, token.RBrace:
			d--
		default:
			if d == 0 && (toks[k].Kind == token.Ident || toks[k].Kind == token.OptIdent) {
				if k+1 < len(toks) && toks[k+1].Kind == token.Colon {
					usedFields[toks[k].Text] = true
				}
			}
		}
	}

	var items []CompletionItem
	for idx, f := range fields {
		if usedFields[f.Name] || !matchesPrefix(f.Name, prefix) {
			continue
		}
		detail := f.TypeStr
		if f.HasDefault {
			detail += " (has default)"
		} else if strings.HasSuffix(f.Name, "?") {
			detail += " (optional, defaults to none)"
		}
		items = append(items, CompletionItem{
			Label:            f.Name,
			Kind:             KindField,
			Detail:           detail,
			SortText:         fmt.Sprintf("0_%03d_%s", idx, f.Name),
			FilterText:       f.Name,
			InsertTextFormat: InsertSnippet,
			TextEdit: &TextEdit{
				Range:   replaceRange,
				NewText: f.Name + ": $1",
			},
		})
	}
	return items, true
}

type structFieldCandidate struct {
	Name       string
	TypeStr    string
	HasDefault bool
}

// -----------------------------------------------------------------------------
// 4. Type Annotation Completion (`let x: |`, `(p: |)`, `define X |`, `[T: |]`)
// -----------------------------------------------------------------------------

func isTypeAnnotationContext(toks []token.Token) bool {
	n := len(toks)
	if n == 0 {
		return false
	}
	last := toks[n-1]
	// Right after `?`, `&`, or `own` in a type context
	if (last.Kind == token.Question || last.Kind == token.Amp || last.Kind == token.KwOwn) && n >= 2 {
		return isTypeAnnotationContext(toks[:n-1])
	}
	// Right after `define Name` (`struct`, `enum`, `interface`, or distinct base type)
	if n >= 2 && toks[n-2].Kind == token.KwDefine && last.Kind == token.Ident {
		return true
	}
	// Inside generic type arguments or vector type `let v: [|`
	if last.Kind == token.LBracket && n >= 2 && isTypeAnnotationContext(toks[:n-1]) {
		return true
	}
	// Right after `)` of a function/lambda/signature or `if (...)` / `for (...)` / `while (...)` / `match (...)` header
	if last.Kind == token.RParen {
		if openIdx, ok := matchingLParenIdx(toks, n-1); ok {
			return isReturnOrEvalTypeHead(toks, openIdx, n-1)
		}
		return false
	}
	if last.Kind != token.Colon {
		return false
	}
	if n < 2 {
		return false
	}
	prev := toks[n-2]
	// `let x: |` or `bind x: |`
	if n >= 3 && (prev.Kind == token.Ident || prev.Kind == token.OptIdent) &&
		(toks[n-3].Kind == token.KwLet || toks[n-3].Kind == token.KwBind) {
		return true
	}
	// Check enclosing delimiter for `:` -> parameter list `(x: |` or `define S struct [x: |` or `[T: |`!
	type delim struct {
		idx  int
		kind token.Kind
	}
	var stack []delim
	for i := 0; i < n-1; i++ {
		switch toks[i].Kind {
		case token.LParen, token.LBracket, token.HashBrack, token.PlusBrack, token.LBrace:
			stack = append(stack, delim{idx: i, kind: toks[i].Kind})
		case token.RParen, token.RBracket, token.RBrace:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) == 0 {
		return false
	}
	top := stack[len(stack)-1]
	if top.kind == token.LParen && (prev.Kind == token.Ident || prev.Kind == token.OptIdent) {
		return true // `(param: |`
	}
	if top.kind == token.LBracket && top.idx >= 1 {
		beforeBr := toks[top.idx-1]
		if beforeBr.Kind == token.KwStruct || beforeBr.Kind == token.KwInterface {
			return true // `define Foo struct [ field: |`
		}
		if beforeBr.Kind == token.Ident && top.idx >= 2 &&
			(toks[top.idx-2].Kind == token.KwDefine || toks[top.idx-2].Kind == token.KwBind) {
			return true // `define Box[T: |` or `bind fn[T: |`
		}
	}
	return false
}

func matchingLParenIdx(toks []token.Token, closeIdx int) (int, bool) {
	depth := 0
	for i := closeIdx; i >= 0; i-- {
		switch toks[i].Kind {
		case token.RParen:
			depth++
		case token.LParen:
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return -1, false
}

func isReturnOrEvalTypeHead(toks []token.Token, openIdx, closeIdx int) bool {
	if openIdx > 0 {
		prev := toks[openIdx-1].Kind
		if prev == token.KwIf || prev == token.KwFor || prev == token.KwWhile || prev == token.KwMatch {
			return true
		}
		if isTypeAnnotationContext(toks[:openIdx]) {
			return true
		}
		if openIdx >= 3 && prev == token.Colon && toks[openIdx-2].Kind == token.Ident && toks[openIdx-3].Kind == token.KwExtern {
			return true
		}
		switch prev {
		case token.Assign, token.Comma, token.Colon, token.LParen, token.LBracket, token.LBrace:
		default:
			return false
		}
	}
	if closeIdx == openIdx+1 {
		return true
	}
	for i := openIdx + 1; i < closeIdx; i++ {
		if toks[i].Kind == token.Colon || toks[i].Kind == token.KwSelf {
			return true
		}
	}
	return false
}

func isParamTypeContext(toks []token.Token) bool {
	n := len(toks)
	if n < 3 {
		return false
	}
	last := toks[n-1]
	if last.Kind != token.Colon {
		return false
	}
	// Enclosing `(`
	depth := 0
	for i := n - 2; i >= 0; i-- {
		switch toks[i].Kind {
		case token.RParen, token.RBracket, token.RBrace:
			depth++
		case token.LParen:
			if depth == 0 {
				return true
			}
			depth--
		case token.LBracket, token.HashBrack, token.PlusBrack, token.LBrace:
			if depth == 0 {
				return false
			}
			depth--
		}
	}
	return false
}

func (s *Server) completeTypeNames(
	prog *sema.CheckedProgram,
	mod *sema.Module,
	prefix string,
	replaceRange Range,
	inParam bool,
) []CompletionItem {
	var items []CompletionItem
	seen := make(map[string]bool)

	if inParam && matchesPrefix("own", prefix) {
		items = append(items, CompletionItem{
			Label:            "own",
			Kind:             KindKeyword,
			Detail:           "consuming ownership parameter (`own T`)",
			SortText:         "0_own",
			FilterText:       "own",
			InsertTextFormat: InsertPlainText,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: "own "},
		})
	}

	// Built-in types & interfaces
	for idx, bt := range BuiltinTypes {
		if !matchesPrefix(bt.Name, prefix) {
			continue
		}
		seen[bt.Name] = true
		insert := bt.Name
		fmtKind := InsertPlainText
		if bt.Snippet != "" {
			insert = bt.Snippet
			fmtKind = InsertSnippet
		}
		items = append(items, CompletionItem{
			Label:            bt.Name,
			Kind:             bt.Kind,
			Detail:           bt.Detail,
			SortText:         fmt.Sprintf("1_%02d_%s", idx, bt.Name),
			FilterText:       bt.Name,
			InsertTextFormat: fmtKind,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: insert},
			Documentation: &MarkupContent{
				Kind:  "markdown",
				Value: bt.Summary,
			},
		})
	}

	// User types in current module and unqualified imports
	if mod != nil {
		addTypesFromMod := func(m *sema.Module, sortPrefix string) {
			samePkg := m == mod || prog == nil || prog.SamePackage(mod, m)
			for name, ty := range m.Types {
				if !samePkg && !sema.IsExported(name) {
					continue
				}
				if seen[name] || !matchesPrefix(name, prefix) {
					continue
				}
				seen[name] = true
				items = append(items, s.makeUserTypeCompletionItem(prog, m.Path, name, ty, replaceRange, sortPrefix))
			}
			for name, gst := range m.GenericStructs {
				if !samePkg && !sema.IsExported(name) {
					continue
				}
				if seen[name] || !matchesPrefix(name, prefix) {
					continue
				}
				seen[name] = true
				items = append(items, s.makeGenericStructCompletionItem(prog, m.Path, gst.Decl, replaceRange, sortPrefix))
			}
			for name, gen := range m.GenericEnums {
				if !samePkg && !sema.IsExported(name) {
					continue
				}
				if seen[name] || !matchesPrefix(name, prefix) {
					continue
				}
				seen[name] = true
				items = append(items, s.makeGenericEnumCompletionItem(prog, m.Path, gen.Decl, replaceRange, sortPrefix))
			}
		}
		addTypesFromMod(mod, "0_")
		for _, imp := range mod.Imports {
			addTypesFromMod(imp, "0_")
		}
		for alias := range mod.Aliased {
			if matchesPrefix(alias, prefix) {
				items = append(items, CompletionItem{
					Label:            alias,
					Kind:             KindModule,
					Detail:           "imported module alias",
					SortText:         "2_" + alias,
					FilterText:       alias,
					InsertTextFormat: InsertPlainText,
					TextEdit:         &TextEdit{Range: replaceRange, NewText: alias},
				})
			}
		}
	}

	sortCompletionItems(items)
	return items
}

// -----------------------------------------------------------------------------
// 5. Scope-Aware Identifier, Function, Type, Built-in & Keyword Completion
// -----------------------------------------------------------------------------

func isTopLevelContext(toks []token.Token) bool {
	braceDepth := 0
	for _, tk := range toks {
		if tk.Kind == token.LBrace {
			braceDepth++
		} else if tk.Kind == token.RBrace && braceDepth > 0 {
			braceDepth--
		}
	}
	return braceDepth == 0
}

func (s *Server) completeScopedSymbols(
	prog *sema.CheckedProgram,
	mod *sema.Module,
	_ string,
	offset int,
	prefix string,
	replaceRange Range,
	atTopLevel bool,
) []CompletionItem {
	var items []CompletionItem
	seen := make(map[string]bool)

	curModPath := ""
	if mod != nil {
		curModPath = mod.Path
	}

	addSym := func(sym *sema.Symbol, sortPrefix string, badge string, ownerModPath string) {
		if sym == nil || sym.Name == "" || seen[sym.Name] || !matchesPrefix(sym.Name, prefix) {
			return
		}
		seen[sym.Name] = true
		if ownerModPath == "" {
			ownerModPath = curModPath
		}
		if sym.FuncInst != nil {
			snip := sym.Name + "()"
			if len(sym.FuncInst.Sig.Params) > 0 {
				snip = sym.Name + "($1)"
			}
			sig := formatFullFuncSig(sym.FuncInst)
			doc := sym.FuncInst.Doc
			if doc == "" {
				doc = sym.Doc
			}
			docVal := fmt.Sprintf("```lowlight\nbind %s = %s\n```", sym.Name, sig)
			if rendered := s.renderDocMarkdown(prog, ownerModPath, "", doc, "", 0, false); rendered != "" {
				docVal = appendRenderedDoc(docVal, rendered)
			}
			items = append(items, CompletionItem{
				Label:            sym.Name,
				Kind:             KindFunction,
				Detail:           sig,
				SortText:         sortPrefix + "_" + sym.Name,
				FilterText:       sym.Name,
				InsertTextFormat: InsertSnippet,
				TextEdit:         &TextEdit{Range: replaceRange, NewText: snip},
				Documentation:    &MarkupContent{Kind: "markdown", Value: docVal},
			})
			return
		}
		if sym.ExternFn != nil {
			snip := sym.Name + "()"
			if len(sym.ExternFn.Sig.Params) > 0 {
				snip = sym.Name + "($1)"
			}
			doc := sym.ExternFn.Doc
			if doc == "" {
				doc = sym.Doc
			}
			docVal := fmt.Sprintf("```lowlight\nextern %s: %s\n```", sym.Name, sym.ExternFn.Sig.String())
			if rendered := s.renderDocMarkdown(prog, ownerModPath, "", doc, "", 0, false); rendered != "" {
				docVal = appendRenderedDoc(docVal, rendered)
			}
			items = append(items, CompletionItem{
				Label:            sym.Name,
				Kind:             KindFunction,
				Detail:           "extern " + sym.ExternFn.Sig.String(),
				SortText:         sortPrefix + "_" + sym.Name,
				FilterText:       sym.Name,
				InsertTextFormat: InsertSnippet,
				TextEdit:         &TextEdit{Range: replaceRange, NewText: snip},
				Documentation:    &MarkupContent{Kind: "markdown", Value: docVal},
			})
			return
		}
		tyStr := "<unknown>"
		if sym.Type != nil {
			tyStr = sym.Type.String()
		}
		mutPrefix := "let "
		if sym.IsBind {
			mutPrefix = "bind "
		}
		if badge != "" {
			mutPrefix = badge + " "
		}
		kind := KindVariable
		if sym.IsTopLevel && sym.IsBind {
			kind = KindConstant
		}
		var doc *MarkupContent
		if rendered := s.renderDocMarkdown(prog, ownerModPath, "", sym.Doc, "", 0, false); rendered != "" {
			docVal := fmt.Sprintf("```lowlight\n%s%s: %s\n```", mutPrefix, sym.Name, tyStr)
			doc = &MarkupContent{Kind: "markdown", Value: appendRenderedDoc(docVal, rendered)}
		}
		items = append(items, CompletionItem{
			Label:            sym.Name,
			Kind:             kind,
			Detail:           mutPrefix + sym.Name + ": " + tyStr,
			SortText:         sortPrefix + "_" + sym.Name,
			FilterText:       sym.Name,
			InsertTextFormat: InsertPlainText,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: sym.Name},
			Documentation:    doc,
		})
	}

	// 5A. Walk enclosing function/method/lambda & AST blocks containing `offset` to surface inner-most locals FIRST!
	if prog != nil && mod != nil && mod.File != nil && !atTopLevel {
		localSyms := collectEnclosingLocals(prog, mod, offset)
		// Reverse order so innermost bindings (`if (user?)`, loop vars, inner `let`) win shadowing!
		for i := len(localSyms) - 1; i >= 0; i-- {
			addSym(localSyms[i].sym, "0_local", localSyms[i].badge, curModPath)
		}
	}

	// 5B. Top-level functions, constants, types, generic functions & module aliases
	if mod != nil {
		addModDecls := func(m *sema.Module, sortGroup string) {
			samePkg := m == mod || prog == nil || prog.SamePackage(mod, m)
			for _, sym := range m.Symbols {
				if !samePkg && !sema.IsExported(sym.Name) {
					continue
				}
				addSym(sym, sortGroup, "", m.Path)
			}
			for name, gfn := range m.GenericFuncs {
				if !samePkg && !sema.IsExported(name) {
					continue
				}
				if seen[name] || !matchesPrefix(name, prefix) {
					continue
				}
				seen[name] = true
				sig := formatGenericVarDeclSig(gfn.VarDecl)
				docVal := fmt.Sprintf("```lowlight\nbind %s%s\n```", name, sig)
				if gfn.VarDecl != nil {
					if rendered := s.renderDocMarkdown(prog, m.Path, "", gfn.VarDecl.Doc, "", 0, false); rendered != "" {
						docVal = appendRenderedDoc(docVal, rendered)
					}
				}
				items = append(items, CompletionItem{
					Label:            name,
					Kind:             KindFunction,
					Detail:           sig,
					SortText:         sortGroup + "_" + name,
					FilterText:       name,
					InsertTextFormat: InsertSnippet,
					TextEdit:         &TextEdit{Range: replaceRange, NewText: name + "($1)"},
					Documentation:    &MarkupContent{Kind: "markdown", Value: docVal},
				})
			}
			for name, ty := range m.Types {
				if !samePkg && !sema.IsExported(name) {
					continue
				}
				if name == "Printable" || name == "Copyable" || name == "Drop" {
					continue
				}
				if seen[name] || !matchesPrefix(name, prefix) {
					continue
				}
				seen[name] = true
				items = append(items, s.makeUserTypeCompletionItem(prog, m.Path, name, ty, replaceRange, "2_type"))
			}
			for name, gst := range m.GenericStructs {
				if !samePkg && !sema.IsExported(name) {
					continue
				}
				if seen[name] || !matchesPrefix(name, prefix) {
					continue
				}
				seen[name] = true
				items = append(items, s.makeGenericStructCompletionItem(prog, m.Path, gst.Decl, replaceRange, "2_type"))
			}
			for name, gen := range m.GenericEnums {
				if !samePkg && !sema.IsExported(name) {
					continue
				}
				if seen[name] || !matchesPrefix(name, prefix) {
					continue
				}
				seen[name] = true
				items = append(items, s.makeGenericEnumCompletionItem(prog, m.Path, gen.Decl, replaceRange, "2_type"))
			}
		}

		addModDecls(mod, "1_top")
		for _, imp := range mod.Imports {
			addModDecls(imp, "1_imp")
		}
		for alias, target := range mod.Aliased {
			if !seen[alias] && matchesPrefix(alias, prefix) {
				seen[alias] = true
				var doc *MarkupContent
				if rendered := s.renderDocMarkdown(prog, target.Path, "", target.Doc, "", 0, false); rendered != "" {
					doc = &MarkupContent{Kind: "markdown", Value: rendered}
				}
				items = append(items, CompletionItem{
					Label:            alias,
					Kind:             KindModule,
					Detail:           fmt.Sprintf("module %s (%s)", alias, filepath.Base(target.Path)),
					SortText:         "1_mod_" + alias,
					FilterText:       alias,
					InsertTextFormat: InsertPlainText,
					TextEdit:         &TextEdit{Range: replaceRange, NewText: alias},
					Documentation:    doc,
				})
			}
		}
	}

	// 5C. Universe built-in functions & intrinsics (when not at file top-level)
	if !atTopLevel {
		for idx, bf := range BuiltinFunctions {
			if seen[bf.Name] || !matchesPrefix(bf.Name, prefix) {
				continue
			}
			seen[bf.Name] = true
			detail := bf.Signature
			if bf.NeedsUnsafe {
				detail += " [unsafe]"
			}
			items = append(items, CompletionItem{
				Label:            bf.Name,
				Kind:             KindFunction,
				Detail:           detail,
				SortText:         fmt.Sprintf("3_builtin_%02d_%s", idx, bf.Name),
				FilterText:       bf.Name,
				InsertTextFormat: InsertSnippet,
				TextEdit:         &TextEdit{Range: replaceRange, NewText: bf.Snippet},
				Documentation: &MarkupContent{
					Kind:  "markdown",
					Value: fmt.Sprintf("```lowlight\n%s\n```\n%s", bf.Signature, bf.Summary),
				},
			})
		}

		// Built-in types usable in expressions (`Arena`, `int(...)`, `float(...)`, `u8(...)`)
		for _, bt := range BuiltinTypes {
			if seen[bt.Name] || !matchesPrefix(bt.Name, prefix) {
				continue
			}
			seen[bt.Name] = true
			items = append(items, CompletionItem{
				Label:            bt.Name,
				Kind:             bt.Kind,
				Detail:           bt.Detail,
				SortText:         "4_btype_" + bt.Name,
				FilterText:       bt.Name,
				InsertTextFormat: InsertPlainText,
				TextEdit:         &TextEdit{Range: replaceRange, NewText: bt.Name},
				Documentation:    &MarkupContent{Kind: "markdown", Value: bt.Summary},
			})
		}
	}

	// 5D. Snippets & Keywords
	for idx, sn := range BuiltinSnippets {
		if !matchesPrefix(sn.Label, prefix) {
			continue
		}
		if atTopLevel && !strings.HasPrefix(sn.Label, "bind") && !strings.HasPrefix(sn.Label, "define") && !strings.HasPrefix(sn.Label, "test") {
			continue
		}
		items = append(items, CompletionItem{
			Label:            sn.Label,
			Kind:             KindSnippet,
			Detail:           sn.Detail,
			SortText:         fmt.Sprintf("5_snip_%02d", idx),
			FilterText:       sn.Label,
			InsertTextFormat: InsertSnippet,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: sn.Snippet},
			Documentation:    &MarkupContent{Kind: "markdown", Value: sn.Summary},
		})
	}

	for _, kw := range BuiltinKeywords {
		if seen[kw.Word] || !matchesPrefix(kw.Word, prefix) {
			continue
		}
		if atTopLevel {
			switch kw.Word {
			case "import", "extern", "define", "bind", "struct", "enum", "interface":
			default:
				continue
			}
		}
		seen[kw.Word] = true
		items = append(items, CompletionItem{
			Label:            kw.Word,
			Kind:             KindKeyword,
			Detail:           kw.Detail,
			SortText:         "6_kw_" + kw.Word,
			FilterText:       kw.Word,
			InsertTextFormat: InsertPlainText,
			TextEdit:         &TextEdit{Range: replaceRange, NewText: kw.Word},
			Documentation:    &MarkupContent{Kind: "markdown", Value: kw.Summary},
		})
	}

	sortCompletionItems(items)
	return items
}

// -----------------------------------------------------------------------------
// Scope & AST Lookup Helpers
// -----------------------------------------------------------------------------

type scopedSymEntry struct {
	sym   *sema.Symbol
	badge string
}

func collectEnclosingLocals(prog *sema.CheckedProgram, mod *sema.Module, offset int) []scopedSymEntry {
	var out []scopedSymEntry

	for _, d := range mod.File.Decls {
		if !spanContainsOffset(d.GetSpan(), offset) {
			continue
		}
		switch decl := d.(type) {
		case *ast.VarDecl:
			if fl, ok := decl.Value.(*ast.FuncLit); ok {
				fi := findFuncInstanceForDecl(prog, mod, decl.Span)
				var bm *sema.BodyMaps
				if fi != nil {
					bm = fi.BodyMaps
					for _, ps := range fi.ParamSyms {
						out = append(out, scopedSymEntry{sym: ps, badge: paramBadge(ps)})
					}
				} else {
					for _, p := range fl.Params {
						out = append(out, scopedSymEntry{
							sym:   &sema.Symbol{Name: p.Name, Type: resolveFallbackTypeExpr(mod, p.Type), IsBind: true},
							badge: "param",
						})
					}
				}
				walkExprScopes(prog, mod, bm, fl.Body, offset, &out)
			}

		case *ast.MethodDecl:
			fi := findFuncInstanceForDecl(prog, mod, decl.Span)
			var bm *sema.BodyMaps
			if fi != nil {
				bm = fi.BodyMaps
				for _, ps := range fi.ParamSyms {
					out = append(out, scopedSymEntry{sym: ps, badge: paramBadge(ps)})
				}
			} else {
				// Uninstantiated generic method (`bind Pool[T].alloc = ...`): synthesize `self` & param symbols!
				if decl.RecvMode != ast.ReceiverMode(-1) {
					var selfTy types.Type = types.TypeInvalid
					if t := lookupNominalType(mod, "", decl.RecvType); t != nil {
						selfTy = t
					} else if gst := lookupGenStruct(mod, "", decl.RecvType); gst != nil {
						var targs []types.Type
						for _, tp := range gst.Decl.TypeParams {
							targs = append(targs, &types.TypeParamVar{Name: tp.Name})
						}
						selfTy = &types.Struct{Name: gst.Decl.Name, TypeArgs: targs}
					}
					out = append(out, scopedSymEntry{
						sym:   &sema.Symbol{Name: "self", Type: selfTy, IsBind: decl.RecvMode == ast.RecvRead},
						badge: "self",
					})
				}
				for _, p := range decl.Params {
					out = append(out, scopedSymEntry{
						sym:   &sema.Symbol{Name: p.Name, Type: resolveFallbackTypeExpr(mod, p.Type), IsBind: true},
						badge: "param",
					})
				}
			}
			walkExprScopes(prog, mod, bm, decl.Body, offset, &out)
		}
	}

	return out
}

func walkExprScopes(prog *sema.CheckedProgram, mod *sema.Module, bm *sema.BodyMaps, expr ast.Expr, offset int, out *[]scopedSymEntry) {
	if expr == nil || !spanContainsOffset(expr.GetSpan(), offset) {
		return
	}
	switch e := expr.(type) {
	case *ast.BlockExpr:
		for _, st := range e.Stmts {
			if spanContainsOffset(st.GetSpan(), offset) {
				switch s := st.(type) {
				case *ast.VarStmt:
					walkExprScopes(prog, mod, bm, s.Value, offset, out)
				case *ast.AssignStmt:
					if s.IsUnpack {
						for _, tgt := range s.UnpackTargets {
							walkExprScopes(prog, mod, bm, tgt, offset, out)
						}
					} else {
						walkExprScopes(prog, mod, bm, s.Target, offset, out)
					}
					walkExprScopes(prog, mod, bm, s.Value, offset, out)
				case *ast.UnwindStmt:
					walkExprScopes(prog, mod, bm, s.Value, offset, out)
				case *ast.ExprStmt:
					walkExprScopes(prog, mod, bm, s.Expr, offset, out)
				}
				return
			}
			// Statement ended strictly before `offset` -> if it's a `VarStmt`, its variable is in scope!
			if vs, ok := st.(*ast.VarStmt); ok && vs.Span.End.Offset <= offset {
				if vs.IsUnpack {
					if bm != nil {
						if syms := bm.UnpackVarSymbols[vs]; len(syms) > 0 {
							for _, sym := range syms {
								if sym != nil {
									*out = append(*out, scopedSymEntry{sym: sym})
								}
							}
							continue
						}
					}
					for _, uv := range vs.UnpackVars {
						if uv.Name == "_" {
							continue
						}
						*out = append(*out, scopedSymEntry{
							sym: &sema.Symbol{
								Name:   uv.Name,
								Type:   resolveFallbackTypeExpr(mod, uv.Type),
								IsBind: vs.IsBind,
							},
						})
					}
					continue
				}
				if bm != nil {
					if sym := bm.VarSymbols[vs]; sym != nil {
						*out = append(*out, scopedSymEntry{sym: sym})
						continue
					}
				}
				*out = append(*out, scopedSymEntry{
					sym: &sema.Symbol{
						Name:   vs.Name,
						Type:   resolveFallbackTypeExpr(mod, vs.Type),
						IsBind: vs.IsBind,
					},
				})
			}
		}
		walkExprScopes(prog, mod, bm, e.Tail, offset, out)

	case *ast.IfExpr:
		walkExprScopes(prog, mod, bm, e.Cond, offset, out)
		if e.Then != nil && spanContainsOffset(e.Then.GetSpan(), offset) {
			if bm != nil {
				if uw := bm.IfUnwrapSyms[e]; uw != nil {
					*out = append(*out, scopedSymEntry{sym: uw, badge: "narrowed"})
				}
			} else if e.BindName != "" {
				*out = append(*out, scopedSymEntry{
					sym:   &sema.Symbol{Name: e.BindName, Type: types.TypeInvalid, IsBind: !e.MutUnwrap},
					badge: "narrowed",
				})
			}
			walkExprScopes(prog, mod, bm, e.Then, offset, out)
		}
		if e.Else != nil && spanContainsOffset(e.Else.GetSpan(), offset) {
			walkExprScopes(prog, mod, bm, e.Else, offset, out)
		}

	case *ast.ForExpr:
		walkExprScopes(prog, mod, bm, e.CondOrIter, offset, out)
		walkExprScopes(prog, mod, bm, e.RangeEnd, offset, out)
		if e.Body != nil && spanContainsOffset(e.Body.GetSpan(), offset) {
			if bm != nil {
				pair := bm.ForSymbols[e]
				if pair[0] != nil {
					*out = append(*out, scopedSymEntry{sym: pair[0], badge: "loop"})
				}
				if pair[1] != nil {
					*out = append(*out, scopedSymEntry{sym: pair[1], badge: "loop"})
				}
			}
			walkExprScopes(prog, mod, bm, e.Body, offset, out)
		}
		if e.Else != nil && spanContainsOffset(e.Else.GetSpan(), offset) {
			walkExprScopes(prog, mod, bm, e.Else, offset, out)
		}

	case *ast.WhileExpr:
		walkExprScopes(prog, mod, bm, e.CondOrIter, offset, out)
		walkExprScopes(prog, mod, bm, e.RangeEnd, offset, out)
		if e.Body != nil && spanContainsOffset(e.Body.GetSpan(), offset) {
			if bm != nil {
				pair := bm.WhileSymbols[e]
				if pair[0] != nil {
					*out = append(*out, scopedSymEntry{sym: pair[0], badge: "loop"})
				}
				if pair[1] != nil {
					*out = append(*out, scopedSymEntry{sym: pair[1], badge: "loop"})
				}
			}
			walkExprScopes(prog, mod, bm, e.Body, offset, out)
		}
		if e.Else != nil && spanContainsOffset(e.Else.GetSpan(), offset) {
			walkExprScopes(prog, mod, bm, e.Else, offset, out)
		}

	case *ast.MatchExpr:
		for _, s := range e.Scrutinees {
			walkExprScopes(prog, mod, bm, s, offset, out)
		}
		for _, arm := range e.Arms {
			if arm.Body != nil && spanContainsOffset(arm.Body.GetSpan(), offset) {
				if bm != nil {
					for _, pat := range arm.AllPatterns() {
						if vp, isVP := pat.(*ast.VariantPattern); isVP {
							for _, sym := range bm.PatternSyms[vp] {
								if sym != nil {
									*out = append(*out, scopedSymEntry{sym: sym, badge: "pattern"})
								}
							}
						}
					}
				}
				walkExprScopes(prog, mod, bm, arm.Body, offset, out)
			}
		}

	case *ast.FuncLit:
		// Lambda nested inside outer function: switch `bm` to lambda's own `BodyMaps`!
		lbm := bm
		if prog != nil {
			for _, li := range prog.Lambdas {
				if li.Lit == e {
					lbm = li.BodyMaps
					for _, ps := range li.ParamSyms {
						*out = append(*out, scopedSymEntry{sym: ps, badge: paramBadge(ps)})
					}
					break
				}
			}
		}
		walkExprScopes(prog, mod, lbm, e.Body, offset, out)

	case *ast.CallExpr:
		walkExprScopes(prog, mod, bm, e.Callee, offset, out)
		for _, a := range e.Args {
			walkExprScopes(prog, mod, bm, a, offset, out)
		}

	case *ast.BinaryExpr:
		walkExprScopes(prog, mod, bm, e.Left, offset, out)
		walkExprScopes(prog, mod, bm, e.Right, offset, out)

	case *ast.UnaryExpr:
		walkExprScopes(prog, mod, bm, e.Operand, offset, out)

	case *ast.UnsafeExpr:
		walkExprScopes(prog, mod, bm, e.Expr, offset, out)

	case *ast.StmtBodyExpr:
		if s, ok := e.Stmt.(*ast.ExprStmt); ok {
			walkExprScopes(prog, mod, bm, s.Expr, offset, out)
		}
	}
}

func paramBadge(sym *sema.Symbol) string {
	switch sym.BindKind {
	case sema.BindMutBorrow:
		return "param &"
	case sema.BindOwned:
		return "param own"
	default:
		return "param"
	}
}

func findFuncInstanceForDecl(prog *sema.CheckedProgram, mod *sema.Module, span diag.Span) *sema.FuncInstance {
	if prog == nil {
		return nil
	}
	for _, fi := range prog.Funcs {
		if fi.Mod == mod && fi.DeclSpan == span {
			return fi
		}
	}
	return nil
}

func findModuleForPath(prog *sema.CheckedProgram, filePath string) *sema.Module {
	if prog == nil {
		return nil
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	for _, m := range prog.Modules {
		if m.Path == abs || m.Path == filePath {
			return m
		}
	}
	return prog.EntryMod
}

func lookupExprInfo(prog *sema.CheckedProgram, target ast.Expr) *sema.ExprInfo {
	if prog == nil || target == nil {
		return nil
	}
	for _, fi := range prog.Funcs {
		if fi.BodyMaps != nil {
			if inf, ok := fi.BodyMaps.ExprInfos[target]; ok && inf.Type != nil && inf.Type != types.TypeInvalid {
				return inf
			}
		}
	}
	for _, li := range prog.Lambdas {
		if li.BodyMaps != nil {
			if inf, ok := li.BodyMaps.ExprInfos[target]; ok && inf.Type != nil && inf.Type != types.TypeInvalid {
				return inf
			}
		}
	}
	if prog.TopBodyMaps != nil {
		if inf, ok := prog.TopBodyMaps.ExprInfos[target]; ok {
			return inf
		}
	}
	for _, bm := range prog.StructBodyMaps {
		if inf, ok := bm.ExprInfos[target]; ok {
			return inf
		}
	}
	// Second pass allowing TypeInvalid if Sym was set
	for _, fi := range prog.Funcs {
		if fi.BodyMaps != nil {
			if inf, ok := fi.BodyMaps.ExprInfos[target]; ok {
				return inf
			}
		}
	}
	return nil
}

func findSentinelMemberExpr(file *ast.File) *ast.MemberExpr {
	for _, d := range file.Decls {
		switch decl := d.(type) {
		case *ast.VarDecl:
			if m := findSentinelInExpr(decl.Value); m != nil {
				return m
			}
		case *ast.MethodDecl:
			if m := findSentinelInExpr(decl.Body); m != nil {
				return m
			}
		case *ast.StructDecl:
			for _, f := range decl.Fields {
				if m := findSentinelInExpr(f.Default); m != nil {
					return m
				}
			}
		}
	}
	return nil
}

func findSentinelInExpr(e ast.Expr) *ast.MemberExpr {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *ast.MemberExpr:
		if strings.HasPrefix(v.Member, completionSentinel) {
			return v
		}
		return findSentinelInExpr(v.Base)
	case *ast.CallExpr:
		if m := findSentinelInExpr(v.Callee); m != nil {
			return m
		}
		for _, a := range v.Args {
			if m := findSentinelInExpr(a); m != nil {
				return m
			}
		}
	case *ast.BlockExpr:
		for _, st := range v.Stmts {
			if m := findSentinelInStmt(st); m != nil {
				return m
			}
		}
		return findSentinelInExpr(v.Tail)
	case *ast.StmtBodyExpr:
		return findSentinelInStmt(v.Stmt)
	case *ast.FuncLit:
		return findSentinelInExpr(v.Body)
	case *ast.IfExpr:
		if m := findSentinelInExpr(v.Cond); m != nil {
			return m
		}
		if m := findSentinelInExpr(v.Then); m != nil {
			return m
		}
		return findSentinelInExpr(v.Else)
	case *ast.ForExpr:
		if m := findSentinelInExpr(v.CondOrIter); m != nil {
			return m
		}
		if m := findSentinelInExpr(v.RangeEnd); m != nil {
			return m
		}
		if m := findSentinelInExpr(v.Body); m != nil {
			return m
		}
		return findSentinelInExpr(v.Else)
	case *ast.WhileExpr:
		if m := findSentinelInExpr(v.CondOrIter); m != nil {
			return m
		}
		if m := findSentinelInExpr(v.RangeEnd); m != nil {
			return m
		}
		if m := findSentinelInExpr(v.Body); m != nil {
			return m
		}
		return findSentinelInExpr(v.Else)
	case *ast.MatchExpr:
		for _, s := range v.Scrutinees {
			if m := findSentinelInExpr(s); m != nil {
				return m
			}
		}
		for _, arm := range v.Arms {
			for _, pat := range arm.AllPatterns() {
				switch p := pat.(type) {
				case *ast.ExprPattern:
					if m := findSentinelInExpr(p.Expr); m != nil {
						return m
					}
				case *ast.ContainsPattern:
					if m := findSentinelInExpr(p.Container); m != nil {
						return m
					}
				}
			}
			if m := findSentinelInExpr(arm.Body); m != nil {
				return m
			}
		}
	case *ast.BinaryExpr:
		if m := findSentinelInExpr(v.Left); m != nil {
			return m
		}
		return findSentinelInExpr(v.Right)
	case *ast.UnaryExpr:
		return findSentinelInExpr(v.Operand)
	case *ast.UnsafeExpr:
		return findSentinelInExpr(v.Expr)
	case *ast.IndexExpr:
		if m := findSentinelInExpr(v.Base); m != nil {
			return m
		}
		return findSentinelInExpr(v.Index)
	case *ast.SliceExpr:
		if m := findSentinelInExpr(v.Base); m != nil {
			return m
		}
		if m := findSentinelInExpr(v.Low); m != nil {
			return m
		}
		return findSentinelInExpr(v.High)
	case *ast.RangeExpr:
		if m := findSentinelInExpr(v.Low); m != nil {
			return m
		}
		return findSentinelInExpr(v.High)
	case *ast.TupleLit:
		for _, el := range v.Elems {
			if m := findSentinelInExpr(el); m != nil {
				return m
			}
		}
	case *ast.ArrayLit:
		for _, el := range v.Elems {
			if m := findSentinelInExpr(el); m != nil {
				return m
			}
		}
	case *ast.VecLit:
		for _, el := range v.Elems {
			if m := findSentinelInExpr(el); m != nil {
				return m
			}
		}
	case *ast.SetLit:
		for _, el := range v.Elems {
			if m := findSentinelInExpr(el); m != nil {
				return m
			}
		}
	case *ast.MapLit:
		for _, ent := range v.Entries {
			if m := findSentinelInExpr(ent.Key); m != nil {
				return m
			}
			if m := findSentinelInExpr(ent.Value); m != nil {
				return m
			}
		}
	case *ast.StructLit:
		for _, f := range v.Fields {
			if m := findSentinelInExpr(f.Value); m != nil {
				return m
			}
		}
	case *ast.InterpStringExpr:
		for _, sub := range v.Exprs {
			if m := findSentinelInExpr(sub); m != nil {
				return m
			}
		}
	}
	return nil
}

func findSentinelInStmt(st ast.Stmt) *ast.MemberExpr {
	switch s := st.(type) {
	case *ast.VarStmt:
		if m := findSentinelInExpr(s.Value); m != nil {
			return m
		}
		if s.FallbackUnwind != nil {
			return findSentinelInExpr(s.FallbackUnwind.Value)
		}
	case *ast.AssignStmt:
		if s.IsUnpack {
			for _, tgt := range s.UnpackTargets {
				if m := findSentinelInExpr(tgt); m != nil {
					return m
				}
			}
		} else if m := findSentinelInExpr(s.Target); m != nil {
			return m
		}
		return findSentinelInExpr(s.Value)
	case *ast.UnwindStmt:
		return findSentinelInExpr(s.Value)
	case *ast.ExprStmt:
		return findSentinelInExpr(s.Expr)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Formatting & Lookup Utilities
// -----------------------------------------------------------------------------

func lookupNominalType(mod *sema.Module, alias string, name string) types.Type {
	if mod == nil {
		return nil
	}
	if alias != "" {
		if tm, ok := mod.Aliased[alias]; ok {
			return tm.Types[name]
		}
		return nil
	}
	if p := types.LookupPrimitive(name); p != nil {
		return p
	}
	if t, ok := mod.Types[name]; ok {
		return t
	}
	for _, imp := range mod.Imports {
		if t, ok := imp.Types[name]; ok {
			return t
		}
	}
	return nil
}

func lookupGenStruct(mod *sema.Module, alias string, name string) *sema.GenericStructTemplate {
	if mod == nil {
		return nil
	}
	if alias != "" {
		if tm, ok := mod.Aliased[alias]; ok {
			return tm.GenericStructs[name]
		}
		return nil
	}
	if g, ok := mod.GenericStructs[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if g, ok := imp.GenericStructs[name]; ok {
			return g
		}
	}
	return nil
}

func lookupGenEnum(mod *sema.Module, alias string, name string) *sema.GenericEnumTemplate {
	if mod == nil {
		return nil
	}
	if alias != "" {
		if tm, ok := mod.Aliased[alias]; ok {
			return tm.GenericEnums[name]
		}
		return nil
	}
	if g, ok := mod.GenericEnums[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if g, ok := imp.GenericEnums[name]; ok {
			return g
		}
	}
	return nil
}

func lookupGenInterface(mod *sema.Module, alias string, name string) *sema.GenericInterfaceTemplate {
	if mod == nil {
		return nil
	}
	if alias != "" {
		if tm, ok := mod.Aliased[alias]; ok {
			return tm.GenericInterfaces[name]
		}
		return nil
	}
	if g, ok := mod.GenericInterfaces[name]; ok {
		return g
	}
	for _, imp := range mod.Imports {
		if g, ok := imp.GenericInterfaces[name]; ok {
			return g
		}
	}
	return nil
}

func tryResolveIndexAsTypeArgs(mod *sema.Module, idx *ast.IndexExpr) []types.Type {
	if id, ok := idx.Index.(*ast.IdentExpr); ok {
		if t := lookupNominalType(mod, "", id.Name); t != nil {
			return []types.Type{t}
		}
	}
	return nil
}

func resolveFallbackTypeExpr(mod *sema.Module, te ast.TypeExpr) types.Type {
	if te == nil {
		return types.TypeInvalid
	}
	switch v := te.(type) {
	case *ast.NamedType:
		if t := lookupNominalType(mod, v.Module, v.Name); t != nil {
			return t
		}
	case *ast.OptionalType:
		sub := resolveFallbackTypeExpr(mod, v.Elem)
		if sub != types.TypeInvalid {
			return types.NewOptional(sub)
		}
	case *ast.VectorType:
		sub := resolveFallbackTypeExpr(mod, v.Elem)
		if sub != types.TypeInvalid {
			return &types.Vector{Elem: sub}
		}
	case *ast.TupleType:
		elems := make([]types.Type, len(v.Elems))
		for i, el := range v.Elems {
			elems[i] = resolveFallbackTypeExpr(mod, el)
		}
		return types.NewTuple(elems)
	case *ast.MutBorrowType:
		return resolveFallbackTypeExpr(mod, v.Elem)
	case *ast.OwnType:
		return resolveFallbackTypeExpr(mod, v.Elem)
	}
	return types.TypeInvalid
}

func baseTypeNameAndArgs(t types.Type) (string, []types.Type) {
	switch v := t.(type) {
	case *types.Struct:
		return v.Name, v.TypeArgs
	case *types.Distinct:
		return v.Name, nil
	case *types.Enum:
		return v.Name, v.TypeArgs
	case *types.Primitive:
		return v.String(), nil
	default:
		return "", nil
	}
}

func formatFullFuncSig(fi *sema.FuncInstance) string {
	if fi == nil || fi.Sig == nil {
		return "()"
	}
	var ps []string
	for i, p := range fi.Sig.Params {
		pname := p.Name
		if pname == "" && i < len(fi.ParamNames) {
			pname = fi.ParamNames[i]
		}
		if pname == "self" {
			switch p.Mode {
			case types.ModeMut:
				ps = append(ps, "&self")
			case types.ModeOwn:
				ps = append(ps, "own self")
			default:
				ps = append(ps, "self")
			}
			continue
		}
		ps = append(ps, fmt.Sprintf("%s: %s%s", pname, p.Mode.Prefix(), p.Type.String()))
	}
	if fi.Sig.Ret == nil || fi.Sig.Ret.Equals(types.TypeVoid) {
		return "(" + strings.Join(ps, ", ") + ")"
	}
	return fmt.Sprintf("(%s) %s", strings.Join(ps, ", "), fi.Sig.Ret.String())
}

func formatMethodInstanceSig(fi *sema.FuncInstance, optChain bool) string {
	if fi == nil || fi.Sig == nil {
		return "()"
	}
	var ps []string
	for i, p := range fi.Sig.Params {
		if i == 0 && fi.IsMethod && !fi.IsStaticAssoc {
			switch p.Mode {
			case types.ModeMut:
				ps = append(ps, "&self")
			case types.ModeOwn:
				ps = append(ps, "own self")
			default:
				ps = append(ps, "self")
			}
			continue
		}
		ps = append(ps, fmt.Sprintf("%s: %s%s", p.Name, p.Mode.Prefix(), p.Type.String()))
	}
	ret := fi.Sig.Ret
	if optChain && ret != nil && !ret.Equals(types.TypeVoid) {
		ret = types.NewOptional(ret)
	}
	if ret == nil || ret.Equals(types.TypeVoid) {
		return "(" + strings.Join(ps, ", ") + ")"
	}
	return fmt.Sprintf("(%s) %s", strings.Join(ps, ", "), ret.String())
}

func formatInterfaceMethodSig(m types.InterfaceMethod, optChain bool) string {
	var ps []string
	switch m.RecvMode {
	case types.ModeMut:
		ps = append(ps, "&self")
	case types.ModeOwn:
		ps = append(ps, "own self")
	default:
		ps = append(ps, "self")
	}
	for _, p := range m.Params {
		ps = append(ps, fmt.Sprintf("%s: %s%s", p.Name, p.Mode.Prefix(), p.Type.String()))
	}
	ret := m.Ret
	if optChain && ret != nil && !ret.Equals(types.TypeVoid) {
		ret = types.NewOptional(ret)
	}
	if ret == nil || ret.Equals(types.TypeVoid) {
		return "(" + strings.Join(ps, ", ") + ")"
	}
	return fmt.Sprintf("(%s) %s", strings.Join(ps, ", "), ret.String())
}

func formatGenericMethodDeclSig(decl *ast.MethodDecl, recvTypeArgs []types.Type, optChain bool) string {
	subst := make(map[string]string)
	for i, tp := range decl.RecvTypeParams {
		if i < len(recvTypeArgs) && recvTypeArgs[i] != nil {
			subst[tp] = recvTypeArgs[i].String()
		}
	}
	var ps []string
	if decl.RecvMode != ast.ReceiverMode(-1) {
		switch decl.RecvMode {
		case ast.RecvMut:
			ps = append(ps, "&self")
		case ast.RecvOwn:
			ps = append(ps, "own self")
		default:
			ps = append(ps, "self")
		}
	}
	for _, p := range decl.Params {
		ps = append(ps, fmt.Sprintf("%s: %s", p.Name, formatTypeExprSubst(p.Type, subst)))
	}
	tparamsPrefix := ""
	if len(decl.TypeParams) > 0 {
		var tps []string
		for _, tp := range decl.TypeParams {
			tps = append(tps, tp.Name)
		}
		tparamsPrefix = "[" + strings.Join(tps, ", ") + "]"
	}
	if decl.Ret == nil {
		return fmt.Sprintf("%s(%s)", tparamsPrefix, strings.Join(ps, ", "))
	}
	retStr := formatTypeExprSubst(decl.Ret, subst)
	if optChain && !strings.HasPrefix(retStr, "?") {
		retStr = "?" + retStr
	}
	return fmt.Sprintf("%s(%s) %s", tparamsPrefix, strings.Join(ps, ", "), retStr)
}

func formatGenericVarDeclSig(vd *ast.VarDecl) string {
	if vd == nil {
		return "()"
	}
	var tps []string
	for _, tp := range vd.TypeParams {
		if len(tp.Bounds) > 0 {
			tps = append(tps, fmt.Sprintf("%s: %s", tp.Name, strings.Join(tp.Bounds, " + ")))
		} else {
			tps = append(tps, tp.Name)
		}
	}
	fl, ok := vd.Value.(*ast.FuncLit)
	if !ok {
		return "[" + strings.Join(tps, ", ") + "]"
	}
	var ps []string
	for _, p := range fl.Params {
		ps = append(ps, fmt.Sprintf("%s: %s", p.Name, formatTypeExpr(p.Type)))
	}
	if fl.Ret == nil {
		return fmt.Sprintf("[%s](%s)", strings.Join(tps, ", "), strings.Join(ps, ", "))
	}
	return fmt.Sprintf("[%s](%s) %s", strings.Join(tps, ", "), strings.Join(ps, ", "), formatTypeExpr(fl.Ret))
}

func formatTypeExpr(te ast.TypeExpr) string {
	return formatTypeExprSubst(te, nil)
}

func formatTypeExprSubst(te ast.TypeExpr, subst map[string]string) string {
	if te == nil {
		return "void"
	}
	switch v := te.(type) {
	case *ast.NamedType:
		if v.Module == "" && len(v.TypeArgs) == 0 && subst != nil {
			if s, ok := subst[v.Name]; ok {
				return s
			}
		}
		prefix := ""
		if v.Module != "" {
			prefix = v.Module + "."
		}
		if len(v.TypeArgs) == 0 {
			return prefix + v.Name
		}
		var args []string
		for _, ta := range v.TypeArgs {
			args = append(args, formatTypeExprSubst(ta, subst))
		}
		return fmt.Sprintf("%s%s[%s]", prefix, v.Name, strings.Join(args, ", "))
	case *ast.OptionalType:
		sub := formatTypeExprSubst(v.Elem, subst)
		if strings.HasPrefix(sub, "?") {
			return sub
		}
		return "?" + sub
	case *ast.MutBorrowType:
		return "&" + formatTypeExprSubst(v.Elem, subst)
	case *ast.OwnType:
		return "own " + formatTypeExprSubst(v.Elem, subst)
	case *ast.VectorType:
		return "[" + formatTypeExprSubst(v.Elem, subst) + "]"
	case *ast.StackArrayType:
		return "[N: " + formatTypeExprSubst(v.Elem, subst) + "]"
	case *ast.TupleType:
		var elems []string
		for _, el := range v.Elems {
			elems = append(elems, formatTypeExprSubst(el, subst))
		}
		return "+[" + strings.Join(elems, ", ") + "]"
	case *ast.SetType:
		return "#[" + formatTypeExprSubst(v.Elem, subst) + "]"
	case *ast.MapType:
		return fmt.Sprintf("#[%s: %s]", formatTypeExprSubst(v.Key, subst), formatTypeExprSubst(v.Value, subst))
	case *ast.FuncType:
		var ps []string
		for _, p := range v.Params {
			ps = append(ps, formatTypeExprSubst(p.Type, subst))
		}
		if v.Ret == nil {
			return "(" + strings.Join(ps, ", ") + ")"
		}
		return fmt.Sprintf("(%s) %s", strings.Join(ps, ", "), formatTypeExprSubst(v.Ret, subst))
	default:
		return "any"
	}
}

func (s *Server) makeUserTypeCompletionItem(prog *sema.CheckedProgram, modPath string, name string, ty types.Type, replaceRange Range, sortPrefix string) CompletionItem {
	kind := KindStruct
	detail := "type " + name
	rawDoc := ""
	switch v := ty.(type) {
	case *types.Struct:
		kind = KindStruct
		detail = fmt.Sprintf("struct %s (%d fields)", name, len(v.Fields))
		rawDoc = v.Doc
	case *types.Enum:
		kind = KindEnum
		detail = fmt.Sprintf("enum %s (%d variants)", name, len(v.Variants))
		rawDoc = v.Doc
	case *types.Interface:
		kind = KindInterface
		detail = fmt.Sprintf("interface %s", name)
		rawDoc = v.Doc
	case *types.Distinct:
		kind = KindStruct
		detail = fmt.Sprintf("define %s %s", name, v.Base.String())
		rawDoc = v.Doc
	}
	var doc *MarkupContent
	if rendered := s.renderDocMarkdown(prog, modPath, name, rawDoc, "", 0, false); rendered != "" {
		docVal := fmt.Sprintf("```lowlight\n%s\n```", detail)
		doc = &MarkupContent{Kind: "markdown", Value: appendRenderedDoc(docVal, rendered)}
	}
	return CompletionItem{
		Label:            name,
		Kind:             kind,
		Detail:           detail,
		SortText:         sortPrefix + "_" + name,
		FilterText:       name,
		InsertTextFormat: InsertPlainText,
		TextEdit:         &TextEdit{Range: replaceRange, NewText: name},
		Documentation:    doc,
	}
}

func (s *Server) makeGenericStructCompletionItem(prog *sema.CheckedProgram, modPath string, decl *ast.StructDecl, replaceRange Range, sortPrefix string) CompletionItem {
	var tps []string
	for _, tp := range decl.TypeParams {
		tps = append(tps, tp.Name)
	}
	detail := fmt.Sprintf("struct %s[%s]", decl.Name, strings.Join(tps, ", "))
	var doc *MarkupContent
	if rendered := s.renderDocMarkdown(prog, modPath, decl.Name, decl.Doc, "", 0, false); rendered != "" {
		docVal := fmt.Sprintf("```lowlight\n%s\n```", detail)
		doc = &MarkupContent{Kind: "markdown", Value: appendRenderedDoc(docVal, rendered)}
	}
	return CompletionItem{
		Label:            decl.Name,
		Kind:             KindStruct,
		Detail:           detail,
		SortText:         sortPrefix + "_" + decl.Name,
		FilterText:       decl.Name,
		InsertTextFormat: InsertPlainText,
		TextEdit:         &TextEdit{Range: replaceRange, NewText: decl.Name},
		Documentation:    doc,
	}
}

func (s *Server) makeGenericEnumCompletionItem(prog *sema.CheckedProgram, modPath string, decl *ast.EnumDecl, replaceRange Range, sortPrefix string) CompletionItem {
	var tps []string
	for _, tp := range decl.TypeParams {
		tps = append(tps, tp.Name)
	}
	detail := fmt.Sprintf("enum %s[%s]", decl.Name, strings.Join(tps, ", "))
	var doc *MarkupContent
	if rendered := s.renderDocMarkdown(prog, modPath, decl.Name, decl.Doc, "", 0, false); rendered != "" {
		docVal := fmt.Sprintf("```lowlight\n%s\n```", detail)
		doc = &MarkupContent{Kind: "markdown", Value: appendRenderedDoc(docVal, rendered)}
	}
	return CompletionItem{
		Label:            decl.Name,
		Kind:             KindEnum,
		Detail:           detail,
		SortText:         sortPrefix + "_" + decl.Name,
		FilterText:       decl.Name,
		InsertTextFormat: InsertPlainText,
		TextEdit:         &TextEdit{Range: replaceRange, NewText: decl.Name},
		Documentation:    doc,
	}
}

func matchesPrefix(candidate, prefix string) bool {
	if prefix == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(prefix))
}

func sortCompletionItems(items []CompletionItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SortText != items[j].SortText {
			return items[i].SortText < items[j].SortText
		}
		return items[i].Label < items[j].Label
	})
}

func spanContainsOffset(sp diag.Span, offset int) bool {
	return sp.Start.Offset <= offset && offset <= sp.End.Offset
}

func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

func isIdentOrQuestChar(ch byte) bool {
	return isIdentChar(ch) || ch == '?'
}
