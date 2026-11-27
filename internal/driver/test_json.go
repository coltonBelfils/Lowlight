package driver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/manifest"
	"lowlight/internal/parser"
	"lowlight/internal/token"
)

// DiscoveredTest describes a single `@test` or `test_main` discovered by AST inspection.
type DiscoveredTest struct {
	Event      string `json:"event"`
	Package    string `json:"package"`
	File       string `json:"file"`
	RelFile    string `json:"relFile"`
	Line       int    `json:"line"`
	Col        int    `json:"col"`
	Name       string `json:"name"`
	Desc       string `json:"desc"`
	IsTestMain bool   `json:"isTestMain,omitempty"`
}

// BatchCaseFailure captures a structured individual case failure inside `assert_all_*`.
type BatchCaseFailure struct {
	Index    int     `json:"index"`
	Label    string  `json:"label,omitempty"`
	Message  string  `json:"message"`
	Expected *string `json:"expected,omitempty"`
	Actual   *string `json:"actual,omitempty"`
	FailFile string  `json:"failFile,omitempty"`
	FailLine int     `json:"failLine,omitempty"`
	FailCol  int     `json:"failCol,omitempty"`
}

// TestEvent represents one NDJSON line emitted by `lowlight test --json`.
type TestEvent struct {
	Event      string             `json:"event"`
	Package    string             `json:"package,omitempty"`
	File       string             `json:"file,omitempty"`
	RelFile    string             `json:"relFile,omitempty"`
	Line       int                `json:"line,omitempty"`
	Col        int                `json:"col,omitempty"`
	Name       string             `json:"name,omitempty"`
	Desc       string             `json:"desc,omitempty"`
	DurationUs int64              `json:"durationUs,omitempty"`
	Leaks      int64              `json:"leaks,omitempty"`
	FailFile   string             `json:"failFile,omitempty"`
	FailLine   int                `json:"failLine,omitempty"`
	FailCol    int                `json:"failCol,omitempty"`
	Message    string             `json:"message,omitempty"`
	Expected   *string            `json:"expected,omitempty"`
	Actual     *string            `json:"actual,omitempty"`
	Cases      []BatchCaseFailure `json:"cases,omitempty"`
	Output     string             `json:"output,omitempty"`
	Passed     int                `json:"passed,omitempty"`
	Failed     int                `json:"failed,omitempty"`
	Total      int                `json:"total,omitempty"`
	Packages   int                `json:"packages,omitempty"`
}

type testListTarget struct {
	path                 string
	isDir                bool
	isExplicitSingleFile bool
}

func emitTestJSONEvent(w io.Writer, ev any) {
	if w == nil {
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(ev)
	_, _ = w.Write(buf.Bytes())
}

func relativeToBase(absPath string, baseDir string) string {
	slashAbs := filepath.ToSlash(filepath.Clean(absPath))
	if baseDir == "" {
		return slashAbs
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return slashAbs
	}
	if rel, err := filepath.Rel(absBase, absPath); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	if realBase, err1 := filepath.EvalSymlinks(absBase); err1 == nil {
		if realPath, err2 := filepath.EvalSymlinks(absPath); err2 == nil {
			if rel, err := filepath.Rel(realBase, realPath); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
	}
	return slashAbs
}

func compileTestFilterRegex(opts Options) (*regexp.Regexp, error) {
	if opts.TestFilter == "" {
		return nil, nil
	}
	re, err := regexp.Compile(opts.TestFilter)
	if err != nil {
		return nil, fmt.Errorf("invalid -run filter %q: %w", opts.TestFilter, err)
	}
	return re, nil
}

func discoverTestsInFile(filePath string, srcOverride string, target manifest.TargetInfo, baseDir string, filterRe *regexp.Regexp) []DiscoveredTest {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filepath.Clean(filePath)
	}
	var src string
	if srcOverride != "" {
		src = srcOverride
	} else {
		if !fileMayHaveTests(absPath) {
			return nil
		}
		raw, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		src = string(raw)
	}

	rep := diag.NewReporter()
	af := parser.ParseFile(absPath, src, rep)
	if af == nil {
		return nil
	}

	slashAbs := filepath.ToSlash(absPath)
	relFile := relativeToBase(absPath, baseDir)
	pkgDir := filepath.Dir(absPath)
	pkgLabel := relativeToBase(pkgDir, baseDir)
	if pkgLabel == "." || pkgLabel == "" {
		pkgLabel = "."
	}

	var out []DiscoveredTest
	var visitDecl func(d ast.Decl)
	visitDecl = func(d ast.Decl) {
		switch node := d.(type) {
		case *ast.TopComptimeIfDecl:
			for cur := node; cur != nil; cur = cur.ElseIf {
				if val, known := evalStaticTargetBool(cur.Cond, target); known {
					if val {
						for _, td := range cur.ThenDecls {
							visitDecl(td)
						}
						return
					}
					if cur.ElseIf == nil && cur.HasElse {
						for _, ed := range cur.ElseDecls {
							visitDecl(ed)
						}
					}
					continue
				}
				for _, td := range cur.ThenDecls {
					visitDecl(td)
				}
				for _, ed := range cur.ElseDecls {
					visitDecl(ed)
				}
			}
			return
		case *ast.VarDecl:
			vd := node
			if vd.Name == "test_main" {
				line := vd.Span.Start.Line
				col := vd.Span.Start.Col
				if line <= 0 {
					line = 1
				}
				if col <= 0 {
					col = 1
				}
				out = append(out, DiscoveredTest{
					Event:      "test",
					Package:    pkgLabel,
					File:       slashAbs,
					RelFile:    relFile,
					Line:       line,
					Col:        col,
					Name:       vd.Name,
					Desc:       vd.Name,
					IsTestMain: true,
				})
				return
			}

			testAttr := ast.FindAttribute(vd.Attributes, "test")
			if testAttr == nil {
				return
			}
			desc := vd.Name
			if len(testAttr.Args) > 0 {
				if trimmed := strings.Trim(testAttr.Args[0], "\""); trimmed != "" {
					desc = trimmed
				}
			}
			if filterRe != nil && !filterRe.MatchString(vd.Name) && !filterRe.MatchString(desc) {
				return
			}

			// Point test markers at the declaration binding line when valid, matching runtime line numbers cleanly.
			span := vd.Span
			if span.Start.Line <= 0 {
				span = testAttr.Span
			}
			line := span.Start.Line
			col := span.Start.Col
			if line <= 0 {
				line = 1
			}
			if col <= 0 {
				col = 1
			}
			out = append(out, DiscoveredTest{
				Event:   "test",
				Package: pkgLabel,
				File:    slashAbs,
				RelFile: relFile,
				Line:    line,
				Col:     col,
				Name:    vd.Name,
				Desc:    desc,
			})
		}
	}
	for _, d := range af.Decls {
		visitDecl(d)
	}
	return out
}

func evalStaticTargetBool(e ast.Expr, target manifest.TargetInfo) (bool, bool) {
	switch v := e.(type) {
	case *ast.BoolLit:
		return v.Val, true
	case *ast.UnaryExpr:
		if v.Op == token.KwNot {
			b, ok := evalStaticTargetBool(v.Operand, target)
			return !b, ok
		}
	case *ast.MemberExpr:
		if isCompTargetField(v, "is_unix") {
			return target.IsUnix, true
		}
	case *ast.BinaryExpr:
		switch v.Op {
		case token.KwAnd:
			lb, lok := evalStaticTargetBool(v.Left, target)
			if lok && !lb {
				return false, true
			}
			rb, rok := evalStaticTargetBool(v.Right, target)
			if rok && !rb {
				return false, true
			}
			if lok && rok {
				return lb && rb, true
			}
		case token.KwOr:
			lb, lok := evalStaticTargetBool(v.Left, target)
			if lok && lb {
				return true, true
			}
			rb, rok := evalStaticTargetBool(v.Right, target)
			if rok && rb {
				return true, true
			}
			if lok && rok {
				return lb || rb, true
			}
		case token.Eq, token.Neq:
			if ls, lok := evalStaticTargetScalar(v.Left, target); lok {
				if rs, rok := evalStaticTargetScalar(v.Right, target); rok {
					eq := ls == rs
					if v.Op == token.Neq {
						return !eq, true
					}
					return eq, true
				}
			}
			if li, lok := evalStaticTargetInt(v.Left, target); lok {
				if ri, rok := evalStaticTargetInt(v.Right, target); rok {
					eq := li == ri
					if v.Op == token.Neq {
						return !eq, true
					}
					return eq, true
				}
			}
			if lb, lok := evalStaticTargetBool(v.Left, target); lok {
				if rb, rok := evalStaticTargetBool(v.Right, target); rok {
					eq := lb == rb
					if v.Op == token.Neq {
						return !eq, true
					}
					return eq, true
				}
			}
		}
	}
	return false, false
}

func isCompTargetField(m *ast.MemberExpr, field string) bool {
	if m == nil || m.Member != field {
		return false
	}
	base, ok := m.Base.(*ast.MemberExpr)
	if !ok || base.Member != "TARGET" {
		return false
	}
	root, ok := base.Base.(*ast.IdentExpr)
	return ok && root.Name == "comp"
}

func evalStaticTargetScalar(e ast.Expr, target manifest.TargetInfo) (string, bool) {
	switch v := e.(type) {
	case *ast.StringLit:
		return "str:" + v.Val, true
	case *ast.MemberExpr:
		base, ok := v.Base.(*ast.MemberExpr)
		if !ok {
			return "", false
		}
		root, ok := base.Base.(*ast.IdentExpr)
		if !ok || root.Name != "comp" {
			return "", false
		}
		switch base.Member {
		case "TARGET":
			switch v.Member {
			case "os":
				return "os:" + target.OS, true
			case "arch":
				return "arch:" + target.Arch, true
			case "endian":
				return "endian:" + target.Endian, true
			case "triple":
				return "str:" + target.Triple, true
			}
		case "TargetOs":
			return "os:" + v.Member, true
		case "TargetArch":
			return "arch:" + v.Member, true
		case "TargetEndian":
			return "endian:" + v.Member, true
		}
	}
	return "", false
}

func evalStaticTargetInt(e ast.Expr, target manifest.TargetInfo) (uint64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return v.Val, true
	case *ast.MemberExpr:
		if isCompTargetField(v, "ptr_size") && target.PtrSize >= 0 {
			return uint64(target.PtrSize), true
		}
		if isCompTargetField(v, "ptr_bits") && target.PtrBits >= 0 {
			return uint64(target.PtrBits), true
		}
	}
	return 0, false
}

func discoverTestsInCandidate(opts Options, cand testListTarget, target manifest.TargetInfo, baseDir string, filterRe *regexp.Regexp) []DiscoveredTest {
	if !cand.isDir {
		override := ""
		if opts.SrcOverride != "" && len(opts.Targets) <= 1 {
			override = opts.SrcOverride
		}
		return discoverTestsInFile(cand.path, override, target, baseDir, filterRe)
	}

	entries, err := os.ReadDir(cand.path)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var out []DiscoveredTest
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ll") {
			continue
		}
		if e.Name() == "json.ll" && filepath.Base(cand.path) == "std" {
			continue
		}
		if !manifest.MatchesTargetSuffix(e.Name(), target) {
			continue
		}
		out = append(out, discoverTestsInFile(filepath.Join(cand.path, e.Name()), "", target, baseDir, filterRe)...)
	}
	return out
}

func runTestList(opts Options, candidates []testListTarget, target manifest.TargetInfo, stdout io.Writer) (int, error) {
	filterRe, err := compileTestFilterRegex(opts)
	if err != nil {
		return 1, err
	}
	baseDir, _ := os.Getwd()
	if baseDir == "" && opts.ProjectRoot != "" {
		baseDir = opts.ProjectRoot
	}

	for _, cand := range candidates {
		tests := discoverTestsInCandidate(opts, cand, target, baseDir, filterRe)
		for _, tc := range tests {
			if opts.TestJSON {
				emitTestJSONEvent(stdout, tc)
			} else if stdout != nil {
				fmt.Fprintf(stdout, "%s:%d:%d\t%s\t%s\n", tc.RelFile, tc.Line, tc.Col, tc.Name, tc.Desc)
			}
		}
	}
	return 0, nil
}

func runTestsJSON(opts Options, candidates []testListTarget, target manifest.TargetInfo, stdout, stderr io.Writer) (int, error) {
	filterRe, err := compileTestFilterRegex(opts)
	if err != nil {
		return 1, err
	}
	baseDir, _ := os.Getwd()
	if baseDir == "" && opts.ProjectRoot != "" {
		baseDir = opts.ProjectRoot
	}

	var totalPassed, totalFailed, totalTests, totalPackages int
	anyFailed := false

	for _, cand := range candidates {
		if !cand.isDir && opts.SrcOverride == "" && !fileMayHaveTests(cand.path) {
			continue
		}

		discovered := discoverTestsInCandidate(opts, cand, target, baseDir, filterRe)

		unitOpts := opts
		unitOpts.EntryPath = cand.path
		unitOpts.Targets = nil
		unitOpts.Package = ""
		unitOpts.Workspace = false
		unitOpts.ManifestCtx = nil

		p, f, t, code, runErr := runSingleTestJSON(unitOpts, cand.path, discovered, baseDir, stdout, stderr)
		if runErr != nil {
			if errors.Is(runErr, ErrNoTests) {
				continue
			}
			absCand, _ := filepath.Abs(cand.path)
			anyFailed = true
			totalPackages++
			totalFailed++
			emitTestJSONEvent(stdout, TestEvent{
				Event:   "build_error",
				Package: relativeToBase(absCand, baseDir),
				File:    filepath.ToSlash(absCand),
				Message: runErr.Error(),
			})
			continue
		}

		totalPackages++
		totalPassed += p
		totalFailed += f
		totalTests += t
		if code != 0 || f > 0 {
			anyFailed = true
		}
	}

	emitTestJSONEvent(stdout, TestEvent{
		Event:    "summary",
		Passed:   totalPassed,
		Failed:   totalFailed,
		Total:    totalTests,
		Packages: totalPackages,
	})
	if anyFailed {
		return 1, nil
	}
	return 0, nil
}

var (
	rePassLine    = regexp.MustCompile(`^--- PASS: (.+) \((\d+)µs, 0 leaks\)$`)
	reLeakLine    = regexp.MustCompile(`^--- FAIL: (.+) \(leaked (\d+) heap allocation\(s\)\)$`)
	reSummaryLine = regexp.MustCompile(`^Test results: (\d+) passed, (\d+) failed \(total (\d+)\)$`)
	reFileLineCol = regexp.MustCompile(`^((?:[A-Za-z]:)?[^\r\n:]+):(\d+):(?:(\d+):)?\s*(.*)$`)
	reBatchHeader = regexp.MustCompile(`^✗ case \[(\d+)\](?: \((.+)\))?:$`)
	reTraceAtLine = regexp.MustCompile(`at\s+((?:[A-Za-z]:)?[^\s:]+):(\d+):(\d+)`)
)

func runSingleTestJSON(
	opts Options,
	candPath string,
	discovered []DiscoveredTest,
	baseDir string,
	stdout io.Writer,
	stderr io.Writer,
) (int, int, int, int, error) {
	opts.TestMode = true
	tmpDir, err := os.MkdirTemp("", "lowlight-test-*")
	if err != nil {
		return 0, 0, 0, 1, fmt.Errorf("failed to create temp test directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	var warnBuf bytes.Buffer
	buildOpts := opts
	buildOpts.OutPath = filepath.Join(tmpDir, "test_app")
	buildOpts.WarnWriter = &warnBuf
	binPath, err := Build(buildOpts)
	if err != nil {
		return 0, 0, 0, 1, err
	}
	if stderr != nil && warnBuf.Len() > 0 {
		_, _ = stderr.Write(warnBuf.Bytes())
	}

	cmd := exec.Command(binPath)
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, 0, 0, 1, err
	}
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	cmd.Stdin = os.Stdin
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}

	if err := cmd.Start(); err != nil {
		return 0, 0, 0, 1, err
	}

	absCand, _ := filepath.Abs(candPath)
	candDir := absCand
	if st, statErr := os.Stat(absCand); statErr == nil && !st.IsDir() {
		candDir = filepath.Dir(absCand)
	}
	pkgLabel := relativeToBase(candDir, baseDir)
	searchDirs := uniqueNonEmpty([]string{baseDir, candDir, opts.ProjectRoot, os.Getenv("LOWLIGHT_ROOT")})

	claimed := make([]bool, len(discovered))
	findMeta := func(desc string) DiscoveredTest {
		for i, d := range discovered {
			if !claimed[i] && d.Desc == desc {
				claimed[i] = true
				return d
			}
		}
		for i, d := range discovered {
			if !claimed[i] && d.Name == desc {
				claimed[i] = true
				return d
			}
		}
		return DiscoveredTest{
			Package: pkgLabel,
			File:    filepath.ToSlash(absCand),
			RelFile: relativeToBase(absCand, baseDir),
			Name:    desc,
			Desc:    desc,
		}
	}

	type activeTestState struct {
		meta       DiscoveredTest
		outLines   []string
		inFailBody bool
		failLines  []string
	}
	var current *activeTestState
	var passedCount, failedCount int

	flushFailure := func(st *activeTestState) {
		if st == nil || !st.inFailBody {
			return
		}
		msg, failFile, failLine, failCol, expVal, actVal, cases := parseMultiLineFailure(st.failLines, searchDirs)
		failedCount++
		emitTestJSONEvent(stdout, TestEvent{
			Event:    "fail",
			Package:  st.meta.Package,
			File:     st.meta.File,
			RelFile:  st.meta.RelFile,
			Line:     st.meta.Line,
			Col:      st.meta.Col,
			Name:     st.meta.Name,
			Desc:     st.meta.Desc,
			FailFile: failFile,
			FailLine: failLine,
			FailCol:  failCol,
			Message:  msg,
			Expected: expVal,
			Actual:   actVal,
			Cases:    cases,
			Output:   strings.Join(st.outLines, "\n"),
		})
	}

	scanner := bufio.NewScanner(outPipe)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")

		if strings.HasPrefix(line, "=== RUN   ") {
			flushFailure(current)
			desc := strings.TrimPrefix(line, "=== RUN   ")
			meta := findMeta(desc)
			current = &activeTestState{meta: meta}
			emitTestJSONEvent(stdout, TestEvent{
				Event:   "run",
				Package: meta.Package,
				File:    meta.File,
				RelFile: meta.RelFile,
				Line:    meta.Line,
				Col:     meta.Col,
				Name:    meta.Name,
				Desc:    meta.Desc,
			})
			continue
		}

		if reSummaryLine.MatchString(line) {
			flushFailure(current)
			current = nil
			continue
		}

		if current != nil && !current.inFailBody {
			if m := rePassLine.FindStringSubmatch(line); len(m) == 3 && m[1] == current.meta.Desc {
				durUs, _ := strconv.ParseInt(m[2], 10, 64)
				passedCount++
				emitTestJSONEvent(stdout, TestEvent{
					Event:      "pass",
					Package:    current.meta.Package,
					File:       current.meta.File,
					RelFile:    current.meta.RelFile,
					Line:       current.meta.Line,
					Col:        current.meta.Col,
					Name:       current.meta.Name,
					Desc:       current.meta.Desc,
					DurationUs: durUs,
					Leaks:      0,
					Output:     strings.Join(current.outLines, "\n"),
				})
				current = nil
				continue
			}
			if m := reLeakLine.FindStringSubmatch(line); len(m) == 3 && m[1] == current.meta.Desc {
				leaks, _ := strconv.ParseInt(m[2], 10, 64)
				failedCount++
				emitTestJSONEvent(stdout, TestEvent{
					Event:    "fail",
					Package:  current.meta.Package,
					File:     current.meta.File,
					RelFile:  current.meta.RelFile,
					Line:     current.meta.Line,
					Col:      current.meta.Col,
					Name:     current.meta.Name,
					Desc:     current.meta.Desc,
					Leaks:    leaks,
					FailFile: current.meta.File,
					FailLine: current.meta.Line,
					FailCol:  current.meta.Col,
					Message:  fmt.Sprintf("leaked %d heap allocation(s)", leaks),
					Output:   strings.Join(current.outLines, "\n"),
				})
				current = nil
				continue
			}
			singlePrefix := "--- FAIL: " + current.meta.Desc + " ("
			if strings.HasPrefix(line, singlePrefix) && strings.HasSuffix(line, ")") {
				rawPanic := line[len(singlePrefix) : len(line)-1]
				msg, failFile, failLine, failCol := parsePanicHeader(rawPanic, searchDirs)
				failedCount++
				emitTestJSONEvent(stdout, TestEvent{
					Event:    "fail",
					Package:  current.meta.Package,
					File:     current.meta.File,
					RelFile:  current.meta.RelFile,
					Line:     current.meta.Line,
					Col:      current.meta.Col,
					Name:     current.meta.Name,
					Desc:     current.meta.Desc,
					FailFile: failFile,
					FailLine: failLine,
					FailCol:  failCol,
					Message:  msg,
					Output:   strings.Join(current.outLines, "\n"),
				})
				current = nil
				continue
			}
			if line == "--- FAIL: "+current.meta.Desc {
				current.inFailBody = true
				continue
			}
			current.outLines = append(current.outLines, line)
			continue
		}

		if current != nil && current.inFailBody {
			current.failLines = append(current.failLines, line)
		}
	}

	flushFailure(current)

	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return passedCount, failedCount, passedCount + failedCount, 1, scanErr
	}
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return passedCount, failedCount, passedCount + failedCount, 1, waitErr
		}
	}

	// Handle custom `test_main` programs, which execute directly without `@test` runner banners.
	if passedCount == 0 && failedCount == 0 {
		for _, d := range discovered {
			if d.IsTestMain {
				emitTestJSONEvent(stdout, TestEvent{
					Event:   "run",
					Package: d.Package,
					File:    d.File,
					RelFile: d.RelFile,
					Line:    d.Line,
					Col:     d.Col,
					Name:    d.Name,
					Desc:    d.Desc,
				})
				if exitCode == 0 {
					passedCount = 1
					emitTestJSONEvent(stdout, TestEvent{
						Event:   "pass",
						Package: d.Package,
						File:    d.File,
						RelFile: d.RelFile,
						Line:    d.Line,
						Col:     d.Col,
						Name:    d.Name,
						Desc:    d.Desc,
					})
				} else {
					failedCount = 1
					emitTestJSONEvent(stdout, TestEvent{
						Event:    "fail",
						Package:  d.Package,
						File:     d.File,
						RelFile:  d.RelFile,
						Line:     d.Line,
						Col:      d.Col,
						Name:     d.Name,
						Desc:     d.Desc,
						FailFile: d.File,
						FailLine: d.Line,
						FailCol:  d.Col,
						Message:  fmt.Sprintf("test_main exited with code %d", exitCode),
					})
				}
				break
			}
		}
	}

	return passedCount, failedCount, passedCount + failedCount, exitCode, nil
}

func uniqueNonEmpty(dirs []string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, d := range dirs {
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			abs = filepath.Clean(d)
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	return out
}

func resolveSourceFile(rawPath string, searchDirs []string) string {
	clean := filepath.Clean(rawPath)
	if filepath.IsAbs(clean) {
		return filepath.ToSlash(clean)
	}
	for _, dir := range searchDirs {
		cand := filepath.Join(dir, clean)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			if abs, err := filepath.Abs(cand); err == nil {
				return filepath.ToSlash(abs)
			}
			return filepath.ToSlash(cand)
		}
	}
	return filepath.ToSlash(clean)
}

func parsePanicHeader(raw string, searchDirs []string) (msg string, failFile string, failLine int, failCol int) {
	if m := reFileLineCol.FindStringSubmatch(raw); len(m) == 5 {
		failFile = resolveSourceFile(m[1], searchDirs)
		failLine, _ = strconv.Atoi(m[2])
		if m[3] != "" {
			failCol, _ = strconv.Atoi(m[3])
		}
		return strings.TrimSpace(m[4]), failFile, failLine, failCol
	}
	return raw, "", 0, 0
}

func stripIndentPrefix(line string, spaces int) string {
	n := 0
	for n < spaces && n < len(line) && line[n] == ' ' {
		n++
	}
	return line[n:]
}

func extractDiffPair(lines []string) (expVal, actVal string, ok bool) {
	var hasExp, hasAct bool
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		switch {
		case strings.HasPrefix(trimmed, "expected: "):
			val := strings.TrimPrefix(trimmed, "expected: ")
			if val == "<end of input>" {
				return "", "", false
			}
			expVal = val
			hasExp = true
		case strings.HasPrefix(trimmed, "got: "):
			val := strings.TrimPrefix(trimmed, "got: ")
			if val == "<end of input>" {
				return "", "", false
			}
			actVal = val
			hasAct = true
		}
	}
	if hasExp && hasAct {
		return expVal, actVal, true
	}
	return "", "", false
}

func parseMultiLineFailure(rawLines []string, searchDirs []string) (
	msg string,
	failFile string,
	failLine int,
	failCol int,
	expVal *string,
	actVal *string,
	cases []BatchCaseFailure,
) {
	dedented := make([]string, len(rawLines))
	for i, l := range rawLines {
		dedented[i] = stripIndentPrefix(l, 4)
	}
	for len(dedented) > 0 && dedented[len(dedented)-1] == "" {
		dedented = dedented[:len(dedented)-1]
	}
	if len(dedented) == 0 {
		return "", "", 0, 0, nil, nil, nil
	}

	_, failFile, failLine, failCol = parsePanicHeader(dedented[0], searchDirs)
	fullMsg := strings.Join(dedented, "\n")

	// Extract batch cases when present.
	hasCases := false
	for _, l := range dedented[1:] {
		if reBatchHeader.MatchString(l) {
			hasCases = true
			break
		}
	}

	if hasCases {
		var cur *BatchCaseFailure
		var curBody []string
		flushCase := func() {
			if cur == nil {
				return
			}
			for len(curBody) > 0 && curBody[len(curBody)-1] == "" {
				curBody = curBody[:len(curBody)-1]
			}
			cur.Message = strings.Join(curBody, "\n")
			if e, a, ok := extractDiffPair(curBody); ok {
				cur.Expected = &e
				cur.Actual = &a
			}
			// Prefer inner trace frame for case-specific Err location when present.
			var firstTraceFile string
			var firstTraceLine, firstTraceCol int
			for _, bl := range curBody {
				if m := reTraceAtLine.FindStringSubmatch(bl); len(m) == 4 {
					f := resolveSourceFile(m[1], searchDirs)
					ln, _ := strconv.Atoi(m[2])
					cl, _ := strconv.Atoi(m[3])
					if firstTraceFile == "" {
						firstTraceFile, firstTraceLine, firstTraceCol = f, ln, cl
					}
					// Use the deepest (root cause) frame if available.
					cur.FailFile, cur.FailLine, cur.FailCol = f, ln, cl
				}
			}
			if cur.FailFile == "" {
				cur.FailFile, cur.FailLine, cur.FailCol = failFile, failLine, failCol
			} else if firstTraceFile != "" && len(curBody) > 0 && strings.Contains(curBody[0], "Result to be Ok") {
				// Keep deepest frame (root cause) already assigned above.
				_ = firstTraceLine
				_ = firstTraceCol
			}
			cases = append(cases, *cur)
		}

		for _, l := range dedented[1:] {
			if m := reBatchHeader.FindStringSubmatch(l); len(m) >= 2 {
				flushCase()
				idx, _ := strconv.Atoi(m[1])
				label := ""
				if len(m) >= 3 {
					label = m[2]
				}
				cur = &BatchCaseFailure{Index: idx, Label: label}
				curBody = nil
				continue
			}
			if cur != nil {
				curBody = append(curBody, stripIndentPrefix(l, 2))
			}
		}
		flushCase()
		return fullMsg, failFile, failLine, failCol, nil, nil, cases
	}

	if e, a, ok := extractDiffPair(dedented[1:]); ok {
		expVal = &e
		actVal = &a
	}
	return fullMsg, failFile, failLine, failCol, expVal, actVal, nil
}
