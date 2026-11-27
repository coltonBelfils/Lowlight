package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/driver"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

// TestCaseInfo describes an automated test function detected in a Lowlight document.
type TestCaseInfo struct {
	Name   string
	Desc   string
	Line   int // 0-based
	Span   diag.Span
	IsMain bool
}

// findTestsInDocument discovers all `@test` functions and `test_main` in the file.
func (s *Server) findTestsInDocument(filePath string, src string) []TestCaseInfo {
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return []TestCaseInfo{}
	}

	var tests []TestCaseInfo
	for _, d := range mod.ActiveDecls() {
		switch decl := d.(type) {
		case *ast.VarDecl:
			isTest := false
			desc := ""
			for _, attr := range decl.Attributes {
				if attr.Name == "test" {
					isTest = true
					if len(attr.Args) > 0 {
						desc = attr.Args[0]
					}
					break
				}
			}
			if decl.Name == "test_main" {
				isTest = true
			}
			if isTest {
				line := decl.Span.Start.Line - 1
				if line < 0 {
					line = 0
				}
				tests = append(tests, TestCaseInfo{
					Name:   decl.Name,
					Desc:   desc,
					Line:   line,
					Span:   decl.Span,
					IsMain: decl.Name == "test_main",
				})
			}
		case *ast.MethodDecl:
			isTest := false
			desc := ""
			for _, attr := range decl.Attributes {
				if attr.Name == "test" {
					isTest = true
					if len(attr.Args) > 0 {
						desc = attr.Args[0]
					}
					break
				}
			}
			if isTest {
				line := decl.Span.Start.Line - 1
				if line < 0 {
					line = 0
				}
				tests = append(tests, TestCaseInfo{
					Name: decl.Name,
					Desc: desc,
					Line: line,
					Span: decl.Span,
				})
			}
		}
	}

	return tests
}

// findMainInDocument returns TestCaseInfo for `main` function if present in the document.
func (s *Server) findMainInDocument(filePath string, src string) *TestCaseInfo {
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod == nil || mod.File == nil {
		return nil
	}
	for _, d := range mod.ActiveDecls() {
		if vd, ok := d.(*ast.VarDecl); ok && vd.Name == "main" {
			line := vd.Span.Start.Line - 1
			if line < 0 {
				line = 0
			}
			return &TestCaseInfo{
				Name:   "main",
				Line:   line,
				Span:   vd.Span,
				IsMain: true,
			}
		}
	}
	return nil
}

// QuickPickLocationItem represents an item in an interactive VS Code dropdown menu.
type QuickPickLocationItem struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Detail      string `json:"detail,omitempty"`
	URI         string `json:"uri,omitempty"`
	Line        int    `json:"line"`
	Character   int    `json:"character"`
}

func (s *Server) buildQuickPickItems(targets []sema.ImplementationTarget, isInterface bool) []QuickPickLocationItem {
	var items []QuickPickLocationItem
	for _, tgt := range targets {
		icon := "$(symbol-class) "
		if isInterface {
			icon = "$(symbol-interface) "
		}
		item := QuickPickLocationItem{
			Label: icon + tgt.Name,
		}
		if tgt.Span.Start.File != "" {
			relPath := tgt.Span.Start.File
			s.mu.RLock()
			rootDir := s.rootDir
			s.mu.RUnlock()
			if rootDir != "" {
				if rel, err := filepath.Rel(rootDir, tgt.Span.Start.File); err == nil && !strings.HasPrefix(rel, "..") {
					relPath = rel
				}
			}
			item.Description = fmt.Sprintf("%s:%d", relPath, tgt.Span.Start.Line)
			item.Detail = tgt.Span.Start.File
			item.URI = PathToURI(tgt.Span.Start.File)
			line := tgt.Span.Start.Line - 1
			if line < 0 {
				line = 0
			}
			item.Line = line
			col := tgt.Span.Start.Col - 1
			if col < 0 {
				col = 0
			}
			item.Character = col
		} else {
			item.Description = "(built-in language primitive)"
		}
		items = append(items, item)
	}
	return items
}

// loadedInterfaceImplementingTargets collects implementing targets for `ifcName`
// from `prog`, currently cached programs, and open editor buffers without walking disk files.
func (s *Server) loadedInterfaceImplementingTargets(ifcName string, prog *sema.CheckedProgram, mod *sema.Module) []sema.ImplementationTarget {
	seen := make(map[string]bool)
	var res []sema.ImplementationTarget
	add := func(tgt sema.ImplementationTarget) {
		key := tgt.Name
		if tgt.Span.Start.File != "" {
			key = fmt.Sprintf("%s:%s:%d:%d", tgt.Name, tgt.Span.Start.File, tgt.Span.Start.Line, tgt.Span.Start.Col)
		}
		if !seen[key] {
			seen[key] = true
			res = append(res, tgt)
		}
	}

	if prog != nil {
		for _, tgt := range prog.InterfaceImplementingTargets(ifcName, mod) {
			add(tgt)
		}
	}

	s.mu.RLock()
	var otherProgs []*sema.CheckedProgram
	for _, p := range s.cachedProgs {
		if p != prog {
			otherProgs = append(otherProgs, p)
		}
	}
	var uninspectedDocs []struct{ path, src string }
	for docPath, docSrc := range s.docs {
		if s.cachedProgs[docPath] == nil {
			uninspectedDocs = append(uninspectedDocs, struct{ path, src string }{docPath, docSrc})
		}
	}
	s.mu.RUnlock()

	for _, p := range otherProgs {
		for _, tgt := range p.InterfaceImplementingTargets(ifcName, nil) {
			add(tgt)
		}
	}

	for _, doc := range uninspectedDocs {
		p := s.getOrCheckProgram(doc.path, doc.src)
		if p != nil {
			for _, tgt := range p.InterfaceImplementingTargets(ifcName, nil) {
				add(tgt)
			}
		}
	}

	return res
}

func interfaceRequiredMethods(ifcName string, prog *sema.CheckedProgram, mod *sema.Module) ([]string, bool) {
	baseIfc := ifcName
	if idx := strings.IndexByte(baseIfc, '['); idx >= 0 {
		baseIfc = baseIfc[:idx]
	}
	if mod != nil {
		if t, ok := mod.Types[baseIfc]; ok {
			if ifc, ok2 := t.(*types.Interface); ok2 {
				names := make([]string, 0, len(ifc.Methods))
				for _, m := range ifc.Methods {
					names = append(names, m.Name)
				}
				return names, true
			}
		}
		if gi, ok := mod.GenericInterfaces[baseIfc]; ok && gi != nil && gi.Decl != nil {
			names := make([]string, 0, len(gi.Decl.Methods))
			for _, m := range gi.Decl.Methods {
				names = append(names, m.Name)
			}
			return names, true
		}
	}
	if prog != nil {
		for _, ifc := range prog.AllInterfaces() {
			if ifc != nil && ifc.Name == baseIfc {
				names := make([]string, 0, len(ifc.Methods))
				for _, m := range ifc.Methods {
					names = append(names, m.Name)
				}
				return names, true
			}
		}
		for _, gi := range prog.AllGenericInterfaces() {
			if gi != nil && gi.Decl != nil && gi.Decl.Name == baseIfc {
				names := make([]string, 0, len(gi.Decl.Methods))
				for _, m := range gi.Decl.Methods {
					names = append(names, m.Name)
				}
				return names, true
			}
		}
	}
	return nil, false
}

func (s *Server) allInterfaceImplementingTargets(ifcName string, prog *sema.CheckedProgram, mod *sema.Module) []sema.ImplementationTarget {
	res := s.loadedInterfaceImplementingTargets(ifcName, prog, mod)
	seen := make(map[string]bool, len(res))
	for _, tgt := range res {
		key := tgt.Name
		if tgt.Span.Start.File != "" {
			key = fmt.Sprintf("%s:%s:%d:%d", tgt.Name, tgt.Span.Start.File, tgt.Span.Start.Line, tgt.Span.Start.Col)
		}
		seen[key] = true
	}
	add := func(tgt sema.ImplementationTarget) {
		key := tgt.Name
		if tgt.Span.Start.File != "" {
			key = fmt.Sprintf("%s:%s:%d:%d", tgt.Name, tgt.Span.Start.File, tgt.Span.Start.Line, tgt.Span.Start.Col)
		}
		if !seen[key] {
			seen[key] = true
			res = append(res, tgt)
		}
	}

	reqMethods, hasMethods := interfaceRequiredMethods(ifcName, prog, mod)

	s.mu.RLock()
	rootDir := s.rootDir
	s.mu.RUnlock()

	// Scan workspace root for any other `.ll` files that define matching methods.
	if rootDir != "" {
		_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				if shouldSkipWorkspaceDir(info.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(info.Name(), ".ll") {
				abs, err := filepath.Abs(path)
				if err != nil {
					abs = path
				}
				s.mu.RLock()
				alreadyCached := s.cachedProgs[abs] != nil
				s.mu.RUnlock()
				if !alreadyCached {
					content := s.readSourceForPath(abs)
					if !strings.Contains(content, "define ") {
						return nil
					}
					if hasMethods && len(reqMethods) > 0 {
						if !strings.Contains(content, "bind ") {
							return nil
						}
						allPresent := true
						for _, mName := range reqMethods {
							if !strings.Contains(content, "."+mName) {
								allPresent = false
								break
							}
						}
						if !allPresent {
							return nil
						}
					} else if !strings.Contains(content, "bind ") {
						return nil
					}
					p := s.getOrCheckProgram(abs, content)
					if p != nil {
						for _, tgt := range p.InterfaceImplementingTargets(ifcName, nil) {
							add(tgt)
						}
					}
				}
			}
			return nil
		})
	}

	return res
}

// computeCodeLenses generates Code Lens annotations for all test functions and interfaces/implementations in the file.
func (s *Server) computeCodeLenses(filePath string, src string) []CodeLens {
	var lenses []CodeLens

	tests := s.findTestsInDocument(filePath, src)
	if len(tests) > 0 {
		// 1. Top of file CodeLens: Run All Tests in File
		lenses = append(lenses, CodeLens{
			Range: Range{
				Start: Position{Line: 0, Character: 0},
				End:   Position{Line: 0, Character: 0},
			},
			Command: &Command{
				Title:     fmt.Sprintf("▶ Run All Tests in File (%d)", len(tests)),
				Command:   "lowlight.runAllTests",
				Arguments: []any{filePath},
			},
		})

		// 2. Per-test CodeLens: Run Single Test
		for _, t := range tests {
			title := "▶ Run Test"
			if t.Desc != "" {
				title = fmt.Sprintf("▶ Run Test (%s)", t.Desc)
			}
			lenses = append(lenses, CodeLens{
				Range: Range{
					Start: Position{Line: t.Line, Character: 0},
					End:   Position{Line: t.Line, Character: 0},
				},
				Command: &Command{
					Title:     title,
					Command:   "lowlight.runTest",
					Arguments: []any{filePath, t.Name},
				},
			})
		}
	}

	// 2b. Main entry point CodeLens: Run Main
	if mainInfo := s.findMainInDocument(filePath, src); mainInfo != nil {
		lenses = append(lenses, CodeLens{
			Range: Range{
				Start: Position{Line: mainInfo.Line, Character: 0},
				End:   Position{Line: mainInfo.Line, Character: 0},
			},
			Command: &Command{
				Title:     "▶ Run",
				Command:   "lowlight.runFile",
				Arguments: []any{filePath},
			},
		})
	}

	// 3. Interface and nominal type implementation lenses
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod != nil && mod.File != nil {
		for _, d := range mod.ActiveDecls() {
			switch decl := d.(type) {
			case *ast.InterfaceDecl:
				line := decl.Span.Start.Line - 1
				if line < 0 {
					line = 0
				}
				targets := s.loadedInterfaceImplementingTargets(decl.Name, prog, mod)
				if len(targets) > 0 {
					items := s.buildQuickPickItems(targets, false)
					title := fmt.Sprintf("▶ %d implementations", len(targets))
					if len(targets) == 1 {
						title = "▶ 1 implementation"
					}
					pickTitle := fmt.Sprintf("Implementations of %s", decl.Name)
					lenses = append(lenses, CodeLens{
						Range: Range{
							Start: Position{Line: line, Character: 0},
							End:   Position{Line: line, Character: 0},
						},
						Command: &Command{
							Title:     title,
							Command:   "lowlight.showImplementationsQuickPick",
							Arguments: []any{pickTitle, items},
						},
					})
				}

			case *ast.StructDecl:
				line := decl.Span.Start.Line - 1
				if line < 0 {
					line = 0
				}
				targets := prog.TypeImplementedInterfaceTargets(decl.Name, mod)
				if len(targets) > 0 {
					items := s.buildQuickPickItems(targets, true)
					title := fmt.Sprintf("▶ implements: %d interfaces", len(targets))
					if len(targets) == 1 {
						title = fmt.Sprintf("▶ implements: %s", targets[0].Name)
					}
					pickTitle := fmt.Sprintf("Interfaces implemented by %s", decl.Name)
					lenses = append(lenses, CodeLens{
						Range: Range{
							Start: Position{Line: line, Character: 0},
							End:   Position{Line: line, Character: 0},
						},
						Command: &Command{
							Title:     title,
							Command:   "lowlight.showImplementationsQuickPick",
							Arguments: []any{pickTitle, items},
						},
					})
				}

			case *ast.DistinctTypeDecl:
				line := decl.Span.Start.Line - 1
				if line < 0 {
					line = 0
				}
				targets := prog.TypeImplementedInterfaceTargets(decl.Name, mod)
				if len(targets) > 0 {
					items := s.buildQuickPickItems(targets, true)
					title := fmt.Sprintf("▶ implements: %d interfaces", len(targets))
					if len(targets) == 1 {
						title = fmt.Sprintf("▶ implements: %s", targets[0].Name)
					}
					pickTitle := fmt.Sprintf("Interfaces implemented by %s", decl.Name)
					lenses = append(lenses, CodeLens{
						Range: Range{
							Start: Position{Line: line, Character: 0},
							End:   Position{Line: line, Character: 0},
						},
						Command: &Command{
							Title:     title,
							Command:   "lowlight.showImplementationsQuickPick",
							Arguments: []any{pickTitle, items},
						},
					})
				}

			case *ast.EnumDecl:
				line := decl.Span.Start.Line - 1
				if line < 0 {
					line = 0
				}
				targets := prog.TypeImplementedInterfaceTargets(decl.Name, mod)
				if len(targets) > 0 {
					items := s.buildQuickPickItems(targets, true)
					title := fmt.Sprintf("▶ implements: %d interfaces", len(targets))
					if len(targets) == 1 {
						title = fmt.Sprintf("▶ implements: %s", targets[0].Name)
					}
					pickTitle := fmt.Sprintf("Interfaces implemented by %s", decl.Name)
					lenses = append(lenses, CodeLens{
						Range: Range{
							Start: Position{Line: line, Character: 0},
							End:   Position{Line: line, Character: 0},
						},
						Command: &Command{
							Title:     title,
							Command:   "lowlight.showImplementationsQuickPick",
							Arguments: []any{pickTitle, items},
						},
					})
				}
			}
		}
	}

	return lenses
}

// computeTestCodeActions adds testing-related code actions to the code action list.
func (s *Server) computeTestCodeActions(filePath string, src string, cursor Range) []CodeAction {
	tests := s.findTestsInDocument(filePath, src)
	var actions []CodeAction

	uri := PathToURI(filePath)

	// Check if cursor is on a test
	for _, t := range tests {
		tRange := SpanToLSP(src, t.Span)
		if rangesOverlap(tRange, cursor) || cursor.Start.Line == t.Line {
			title := fmt.Sprintf("▶ Run test '%s'", t.Name)
			if t.Desc != "" {
				title = fmt.Sprintf("▶ Run test '%s' (%s)", t.Name, t.Desc)
			}
			actions = append(actions, CodeAction{
				Title: title,
				Kind:  "run",
				Command: &Command{
					Title:     title,
					Command:   "lowlight.runTest",
					Arguments: []any{filePath, t.Name},
				},
			})
			break
		}
	}

	// Action to run all tests in file
	if len(tests) > 0 {
		actions = append(actions, CodeAction{
			Title: "▶ Run all tests in this file",
			Kind:  "run",
			Command: &Command{
				Title:     "Run all tests",
				Command:   "lowlight.runAllTests",
				Arguments: []any{filePath},
			},
		})
	}

	// If cursor is on a non-test function or method, suggest generating a test for it!
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod != nil && mod.File != nil {
		ind := detectFileIndent(filePath, src)
		for _, d := range mod.ActiveDecls() {
			switch decl := d.(type) {
			case *ast.VarDecl:
				if decl.Name == "main" || decl.Name == "test_main" {
					continue
				}
				if _, isFn := decl.Value.(*ast.FuncLit); !isFn {
					continue
				}
				isTest := false
				for _, attr := range decl.Attributes {
					if attr.Name == "test" {
						isTest = true
						break
					}
				}
				if isTest {
					continue
				}
				fnRange := SpanToLSP(src, decl.Span)
				if rangesOverlap(fnRange, cursor) || cursor.Start.Line == decl.Span.Start.Line-1 {
					testStub := fmt.Sprintf("@test(\"%s\")\nbind test_%s = fn(): {\n%s// TODO: add assertions\n}", decl.Name, decl.Name, ind)
					actions = append(actions, CodeAction{
						Title: fmt.Sprintf("Generate @test for '%s'", decl.Name),
						Kind:  "refactor",
						Edit: &WorkspaceEdit{
							Changes: map[string][]TextEdit{
								uri: {appendDeclsEdit(src, []string{testStub})},
							},
						},
					})
					return actions
				}
			case *ast.MethodDecl:
				qualName := decl.RecvType + "." + decl.Name
				fnRange := SpanToLSP(src, decl.Span)
				if rangesOverlap(fnRange, cursor) || cursor.Start.Line == decl.Span.Start.Line-1 {
					testStub := fmt.Sprintf("@test(\"%s\")\nbind test_%s_%s = fn(): {\n%s// TODO: add assertions\n}", qualName, decl.RecvType, decl.Name, ind)
					actions = append(actions, CodeAction{
						Title: fmt.Sprintf("Generate @test for '%s'", qualName),
						Kind:  "refactor",
						Edit: &WorkspaceEdit{
							Changes: map[string][]TextEdit{
								uri: {appendDeclsEdit(src, []string{testStub})},
							},
						},
					})
					return actions
				}
			}
		}
	}

	return actions
}

// executeTestCommand runs a test execution command requested via `workspace/executeCommand`.
func (s *Server) executeTestCommand(cmd string, rawArgs []json.RawMessage) (any, error) {
	if len(rawArgs) == 0 {
		return nil, fmt.Errorf("command %q requires arguments", cmd)
	}

	var filePath string
	if err := json.Unmarshal(rawArgs[0], &filePath); err != nil {
		return nil, fmt.Errorf("invalid file path argument: %w", err)
	}
	filePath = URIToPath(filePath)

	var testName string
	if len(rawArgs) > 1 {
		_ = json.Unmarshal(rawArgs[1], &testName)
	}

	projectRoot := s.projectRootFor(filePath)
	if projectRoot == "" {
		projectRoot = filepath.Dir(filePath)
	}

	opts := driver.Options{
		Targets:     []string{filePath},
		ProjectRoot: projectRoot,
		TestMode:    true,
	}

	var outBuf bytes.Buffer
	code, err := driver.Test(opts, &outBuf, &outBuf)

	output := outBuf.String()
	success := (code == 0 && err == nil)

	s.conn.logf("Test command %s executed: exit=%d, success=%v\n%s", cmd, code, success, output)

	msgType := MessageTypeInfo
	if !success {
		msgType = MessageTypeError
	}
	_ = s.conn.SendNotification("window/showMessage", ShowMessageParams{
		Type:    msgType,
		Message: output,
	})

	return map[string]any{
		"success":  success,
		"exitCode": code,
		"output":   output,
		"filePath": filePath,
		"testName": testName,
	}, nil
}

// executeRunCommand runs a file execution command requested via `workspace/executeCommand`.
func (s *Server) executeRunCommand(cmd string, rawArgs []json.RawMessage) (any, error) {
	if len(rawArgs) == 0 {
		return nil, fmt.Errorf("command %q requires arguments", cmd)
	}

	var filePath string
	if err := json.Unmarshal(rawArgs[0], &filePath); err != nil {
		return nil, fmt.Errorf("invalid file path argument: %w", err)
	}
	filePath = URIToPath(filePath)

	projectRoot := s.projectRootFor(filePath)
	if projectRoot == "" {
		projectRoot = filepath.Dir(filePath)
	}

	opts := driver.Options{
		Targets:     []string{filePath},
		ProjectRoot: projectRoot,
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	code, err := driver.Run(opts, nil, &outBuf, &errBuf)

	output := outBuf.String()
	if errBuf.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += errBuf.String()
	}
	success := (code == 0 && err == nil)

	s.conn.logf("Run command %s executed: exit=%d, success=%v\n%s", cmd, code, success, output)

	msgType := MessageTypeInfo
	if !success {
		msgType = MessageTypeError
	}
	_ = s.conn.SendNotification("window/showMessage", ShowMessageParams{
		Type:    msgType,
		Message: fmt.Sprintf("[%s] exit code %d:\n%s", filepath.Base(filePath), code, output),
	})

	return map[string]any{
		"success":  success,
		"exitCode": code,
		"output":   output,
		"filePath": filePath,
	}, nil
}

// executeGoToLocation handles client navigation requests via `window/showDocument`.
func (s *Server) executeGoToLocation(rawArgs []json.RawMessage) (any, error) {
	if len(rawArgs) == 0 {
		return map[string]any{"success": false}, nil
	}
	var uriStr string
	var line, char int
	var loc Location
	if err := json.Unmarshal(rawArgs[0], &loc); err == nil && loc.URI != "" {
		uriStr = loc.URI
		line = loc.Range.Start.Line
		char = loc.Range.Start.Character
	} else if err := json.Unmarshal(rawArgs[0], &uriStr); err == nil && uriStr != "" {
		if len(rawArgs) > 1 {
			_ = json.Unmarshal(rawArgs[1], &line)
		}
		if len(rawArgs) > 2 {
			_ = json.Unmarshal(rawArgs[2], &char)
		}
	}
	if uriStr != "" {
		_ = s.conn.SendRequest(time.Now().UnixNano(), "window/showDocument", ShowDocumentParams{
			URI:       uriStr,
			TakeFocus: true,
			Selection: &Range{
				Start: Position{Line: line, Character: char},
				End:   Position{Line: line, Character: char},
			},
		})
	}
	return map[string]any{"success": true, "uri": uriStr, "line": line, "character": char}, nil
}
