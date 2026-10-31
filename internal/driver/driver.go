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
	SrcOverride string
	ProjectRoot string
	OutPath     string
	CC          string
	Release     bool
	ExtraCFlags []string
	Env         []string // Optional extra environment variables when running
}

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
	mirProg := ir.Lower(checked, opts.Release)
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
	if checked.MainFunc == nil {
		return "", fmt.Errorf("%s: cannot build executable: entry module defines no 'main' function", opts.EntryPath)
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
