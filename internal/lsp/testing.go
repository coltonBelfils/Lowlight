package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/driver"
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
	for _, d := range mod.File.Decls {
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

// computeCodeLenses generates Code Lens annotations for all test functions in the file.
func (s *Server) computeCodeLenses(filePath string, src string) []CodeLens {
	tests := s.findTestsInDocument(filePath, src)
	if len(tests) == 0 {
		return []CodeLens{}
	}

	var lenses []CodeLens

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

	// If cursor is on a non-test function, suggest generating a test for it!
	prog := s.getOrCheckProgram(filePath, src)
	mod := findModuleForPath(prog, filePath)
	if mod != nil && mod.File != nil {
		for _, d := range mod.File.Decls {
			vd, ok := d.(*ast.VarDecl)
			if !ok || vd.Name == "main" || vd.Name == "test_main" {
				continue
			}
			// Check if already a test
			isTest := false
			for _, attr := range vd.Attributes {
				if attr.Name == "test" {
					isTest = true
					break
				}
			}
			if isTest {
				continue
			}

			fnRange := SpanToLSP(src, vd.Span)
			if rangesOverlap(fnRange, cursor) || cursor.Start.Line == vd.Span.Start.Line-1 {
				testStub := fmt.Sprintf("\n\n@test(\"test %s\")\nbind test_%s = () -> {\n    // TODO: test %s\n}\n", vd.Name, vd.Name, vd.Name)
				docEnd := OffsetToLSP(src, len(src))
				actions = append(actions, CodeAction{
					Title: fmt.Sprintf("Generate @test for '%s'", vd.Name),
					Kind:  "refactor",
					Edit: &WorkspaceEdit{
						Changes: map[string][]TextEdit{
							uri: {{
								Range:   Range{Start: docEnd, End: docEnd},
								NewText: testStub,
							}},
						},
					},
				})
				break
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

	return map[string]any{
		"success":  success,
		"exitCode": code,
		"output":   output,
		"filePath": filePath,
		"testName": testName,
	}, nil
}
