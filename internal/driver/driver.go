// Package driver coordinates the end-to-end Lowlight compilation pipeline:
// parsing -> semantic/ownership analysis -> MIR lowering -> ISO C11 emission -> C compiler invocation.
package driver

import (
	"bytes"
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

	"lowlight/internal/codegen"
	"lowlight/internal/diag"
	"lowlight/internal/ir"
	"lowlight/internal/sema"
)

// Version is the semantic version of this Lowlight compiler implementation.
const Version = "1.0.0"

// Options configures a compilation, emission, build, or run invocation.
type Options struct {
	EntryPath   string
	Targets     []string // Optional target files or directories for test execution
	SrcOverride string
	ProjectRoot string
	OutPath     string
	CC          string
	Release     bool
	ExtraCFlags []string
	Env         []string // Optional extra environment variables when running
	TestMode    bool     // Compile and run test entry point / test harness
}

// ErrNoTests indicates that an entry module defines no @test functions or custom test_main.
var ErrNoTests = errors.New("no @test functions or 'test_main' found")

// CompileError wraps formatted Lowlight compiler diagnostics.
type CompileError struct {
	Formatted string
}

func (e *CompileError) Error() string {
	return e.Formatted
}

// Check runs lexing, parsing, import resolution, type checking, monomorphization,
// ownership/move analysis, borrow checking, and arena escape verification.
func Check(opts Options) (*sema.CheckedProgram, string, error) {
	if opts.EntryPath == "" {
		return nil, "", errors.New("missing input .ll source file")
	}
	rep := diag.NewReporter()
	prog := sema.Check(opts.EntryPath, opts.SrcOverride, opts.ProjectRoot, rep)
	formatted := rep.Format()
	if rep.HasErrors() {
		return nil, "", &CompileError{Formatted: formatted}
	}
	return prog, formatted, nil
}

// EmitC runs semantic checking, lowers to MIR, and emits portable ISO C11 source code.
func EmitC(opts Options) (string, *sema.CheckedProgram, error) {
	checked, _, err := Check(opts)
	if err != nil {
		return "", nil, err
	}
	mirProg := ir.Lower(checked, opts.Release, opts.TestMode)
	cSource := codegen.EmitC(mirProg)
	return cSource, checked, nil
}

// ResolveCC selects the C11 compiler executable to invoke.
func ResolveCC(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("LOWLIGHT_CC"); env != "" {
		return env
	}
	if env := os.Getenv("CC"); env != "" {
		return env
	}
	return "cc"
}

// DefaultOutputBin derives an executable path from `entryPath` (e.g. `foo/hello.ll` -> `hello`).
func DefaultOutputBin(entryPath string) string {
	base := filepath.Base(entryPath)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		base = "a.out"
	}
	return base
}

// Build compiles a Lowlight entry module into a native executable via the system C11 compiler.
func Build(opts Options) (string, error) {
	cSource, checked, err := EmitC(opts)
	if err != nil {
		return "", err
	}
	if opts.TestMode {
		if checked.TestMainFunc == nil && len(checked.TestFuncs) == 0 {
			return "", ErrNoTests
		}
	} else {
		if checked.MainFunc == nil {
			return "", fmt.Errorf("%s: cannot build executable: entry module defines no 'main' function", opts.EntryPath)
		}
	}

	outBin := opts.OutPath
	if outBin == "" {
		outBin = DefaultOutputBin(opts.EntryPath)
	}
	absOut, err := filepath.Abs(outBin)
	if err != nil {
		absOut = outBin
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "lowlight-build-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp build directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cFile := filepath.Join(tmpDir, "program.c")
	if err := os.WriteFile(cFile, []byte(cSource), 0o644); err != nil {
		return "", fmt.Errorf("failed to write temporary C file: %w", err)
	}

	cc := ResolveCC(opts.CC)
	args := []string{"-std=c11"}
	if opts.Release {
		args = append(args, "-O2")
	} else {
		args = append(args, "-g", "-O0")
	}
	// Suppress benign parenthesization/unused-parameter noise by default while keeping user ExtraCFlags authoritative.
	args = append(args, opts.ExtraCFlags...)
	args = append(args, "-o", absOut, cFile, "-lm", "-ldl")

	var ccStderr bytes.Buffer
	cmd := exec.Command(cc, args...)
	cmd.Stderr = &ccStderr
	cmd.Stdout = &ccStderr
	if runErr := cmd.Run(); runErr != nil {
		return "", fmt.Errorf("C11 backend compilation failed (%s %s):\n%s\n%w",
			cc, strings.Join(args, " "), strings.TrimSpace(ccStderr.String()), runErr)
	}
	return absOut, nil
}

// Run compiles a Lowlight program into a temporary executable and runs it immediately.
func Run(opts Options, progArgs []string, stdout, stderr io.Writer) (int, error) {
	tmpDir, err := os.MkdirTemp("", "lowlight-run-*")
	if err != nil {
		return 1, fmt.Errorf("failed to create temp run directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	buildOpts := opts
	buildOpts.OutPath = filepath.Join(tmpDir, "app")
	binPath, err := Build(buildOpts)
	if err != nil {
		return 1, err
	}

	cmd := exec.Command(binPath, progArgs...)
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = os.Stdout
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

	if runErr := cmd.Run(); runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, runErr
	}
	return 0, nil
}

// Test compiles and runs Lowlight tests for specified files or directories, streaming results.
// If no target is given (or empty), it recursively tests all .ll files in the working directory.
func Test(opts Options, stdout, stderr io.Writer) (int, error) {
	opts.TestMode = true

	// 1. Resolve raw targets
	rawTargets := opts.Targets
	if len(rawTargets) == 0 {
		if opts.EntryPath != "" {
			rawTargets = []string{opts.EntryPath}
		} else {
			rawTargets = []string{"."}
		}
	}

	// 2. Discover test candidate files
	type fileCandidate struct {
		path                 string
		isExplicitSingleFile bool
	}
	var candidates []fileCandidate
	visited := make(map[string]bool)

	isSingleExplicit := len(rawTargets) == 1

	for _, target := range rawTargets {
		cleanTarget := filepath.Clean(target)
		st, err := os.Stat(cleanTarget)
		if err != nil {
			if !strings.HasSuffix(cleanTarget, ".ll") {
				if st2, err2 := os.Stat(cleanTarget + ".ll"); err2 == nil && !st2.IsDir() {
					cleanTarget = cleanTarget + ".ll"
					st = st2
					err = nil
				}
			}
			if err != nil {
				return 1, fmt.Errorf("cannot find file or directory %q: %w", target, err)
			}
		}

		if st.IsDir() {
			isSingleExplicit = false
			files, err := discoverLLFiles(cleanTarget)
			if err != nil {
				return 1, fmt.Errorf("failed to scan directory %q: %w", target, err)
			}
			for _, f := range files {
				abs, _ := filepath.Abs(f)
				if !visited[abs] {
					visited[abs] = true
					candidates = append(candidates, fileCandidate{path: f, isExplicitSingleFile: false})
				}
			}
		} else {
			abs, _ := filepath.Abs(cleanTarget)
			if !visited[abs] {
				visited[abs] = true
				candidates = append(candidates, fileCandidate{path: cleanTarget, isExplicitSingleFile: isSingleExplicit})
			}
		}
	}

	if len(candidates) == 0 {
		targetName := "."
		if len(rawTargets) == 1 {
			targetName = rawTargets[0]
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "?   \t%s\t[no tests to run]\n", targetName)
		}
		return 0, nil
	}

	// 3. Single explicit file mode:
	if len(candidates) == 1 && candidates[0].isExplicitSingleFile {
		cand := candidates[0]
		if !fileMayHaveTests(cand.path) {
			if stdout != nil {
				fmt.Fprintf(stdout, "?   \t%s\t[no tests to run]\n", cand.path)
			}
			return 0, nil
		}
		fileOpts := opts
		fileOpts.EntryPath = cand.path
		fileOpts.Targets = nil
		code, err := runSingleTest(fileOpts, stdout, stderr)
		if err != nil {
			if errors.Is(err, ErrNoTests) {
				if stdout != nil {
					fmt.Fprintf(stdout, "?   \t%s\t[no tests to run]\n", cand.path)
				}
				return 0, nil
			}
			return 1, err
		}
		return code, nil
	}

	// 4. Directory or multi-file mode:
	var totalPassed int
	var totalFailed int
	var totalTests int
	var totalFilesTested int
	var anyFailed bool

	reSummary := regexp.MustCompile(`Test results: (\d+) passed, (\d+) failed \(total (\d+)\)`)

	for _, cand := range candidates {
		if !fileMayHaveTests(cand.path) {
			continue
		}

		fileOpts := opts
		fileOpts.EntryPath = cand.path
		fileOpts.Targets = nil

		var buf bytes.Buffer
		var outWriter io.Writer = &buf
		if stdout != nil {
			outWriter = io.MultiWriter(stdout, &buf)
		}

		code, err := runSingleTest(fileOpts, outWriter, stderr)
		if err != nil {
			if errors.Is(err, ErrNoTests) {
				continue
			}
			anyFailed = true
			if stderr != nil {
				fmt.Fprintf(stderr, "error testing %s: %v\n", cand.path, err)
			}
			continue
		}

		totalFilesTested++
		if code != 0 {
			anyFailed = true
		}

		m := reSummary.FindStringSubmatch(buf.String())
		if len(m) == 4 {
			p, _ := strconv.Atoi(m[1])
			f, _ := strconv.Atoi(m[2])
			t, _ := strconv.Atoi(m[3])
			totalPassed += p
			totalFailed += f
			totalTests += t
		}
	}

	if totalFilesTested == 0 {
		targetName := "."
		if len(rawTargets) == 1 {
			targetName = rawTargets[0]
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "?   \t%s\t[no tests to run]\n", targetName)
		}
		return 0, nil
	}

	if totalFilesTested > 1 && stdout != nil {
		fmt.Fprintf(stdout, "\nTotal test results: %d passed, %d failed across %d file(s) (total %d)\n",
			totalPassed, totalFailed, totalFilesTested, totalTests)
	}

	if anyFailed {
		return 1, nil
	}
	return 0, nil
}

func runSingleTest(opts Options, stdout, stderr io.Writer) (int, error) {
	opts.TestMode = true
	tmpDir, err := os.MkdirTemp("", "lowlight-test-*")
	if err != nil {
		return 1, fmt.Errorf("failed to create temp test directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	buildOpts := opts
	buildOpts.OutPath = filepath.Join(tmpDir, "test_app")
	binPath, err := Build(buildOpts)
	if err != nil {
		return 1, err
	}

	cmd := exec.Command(binPath)
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = os.Stdout
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

	if runErr := cmd.Run(); runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, runErr
	}
	return 0, nil
}

func discoverLLFiles(dir string) ([]string, error) {
	cleanDir := filepath.Clean(dir)
	var files []string
	err := filepath.WalkDir(cleanDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && name != "." && name != ".." {
				return filepath.SkipDir
			}
			// Skip bundled/vendor/editor directories when scanning root directory
			if cleanDir == "." && (name == "editors" || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".ll") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func fileMayHaveTests(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "@test") || strings.Contains(content, "test_main")
}

