// Package driver coordinates the end-to-end Lowlight compilation pipeline:
// parsing -> semantic/ownership analysis -> MIR lowering -> LLVM IR / ISO C11 emission -> compiler/linker invocation.
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
	"lowlight/internal/manifest"
	"lowlight/internal/sema"
)

// Version is the semantic version of this Lowlight compiler implementation.
const Version = "1.0.0"

// BackendKind specifies which codegen backend stack to target.
type BackendKind string

const (
	BackendC11  BackendKind = "c11"
	BackendLLVM BackendKind = "llvm"
)

// Options configures a compilation, emission, build, or run invocation.
type Options struct {
	EntryPath     string
	Targets       []string // Optional target files or directories for test execution
	SrcOverride   string
	ProjectRoot   string
	OutPath       string
	CC            string
	Backend       BackendKind // Target backend: "llvm" (default) or "c11"
	Release       bool
	ReleaseSet    bool // True when Release was explicitly set on CLI so [build].release doesn't override false/true ambiguously
	ExtraCFlags   []string
	Env           []string  // Optional extra environment variables when running
	WarnWriter    io.Writer // Optional writer for compiler warnings (defaults to os.Stderr)
	TestMode      bool      // Compile and run test entry point / test harness
	TestFilter    string    // Optional regex/substring filter for @test functions (-run)
	TestJSON      bool      // Emit structured NDJSON test events (--json)
	TestList      bool      // List discovered tests without compiling/running (--list)
	TargetTriple  string    // Target triple for cross-compilation (e.g. "aarch64-unknown-linux-gnu")
	AllTargets    bool      // Typecheck across all canonical targets (`lowlight check --all-targets`)
	Package       string    // Optional workspace member selector (`-p <pkg>`)
	Workspace     bool      // Operate across all workspace members (`--workspace`)
	Locked        bool      // Enforce exact `lowlight.lock` match (`--locked`)
	Offline       bool      // Disallow any network access (`--offline`)
	CacheRoot     string    // Optional override for dependency/object cache directory
	NoIncremental bool      // Disable two-phase per-package `.o` incremental caching
	ManifestCtx   *manifest.ProjectContext
}

func (o Options) warnOut() io.Writer {
	if o.WarnWriter != nil {
		return o.WarnWriter
	}
	return os.Stderr
}

// EffectiveBackend returns the active BackendKind considering options and LOWLIGHT_BACKEND env.
func (o Options) EffectiveBackend() BackendKind {
	if o.Backend != "" {
		return o.Backend
	}
	if env := os.Getenv("LOWLIGHT_BACKEND"); env != "" {
		switch strings.ToLower(strings.TrimSpace(env)) {
		case "llvm", "ll":
			return BackendLLVM
		case "c", "c11":
			return BackendC11
		}
	}
	return BackendLLVM
}

// ErrNoTests indicates that an entry module or package defines no @test functions or custom test_main.
var ErrNoTests = errors.New("no @test functions or 'test_main' found")

// CompileError wraps formatted Lowlight compiler diagnostics.
type CompileError struct {
	Formatted string
}

func (e *CompileError) Error() string {
	return e.Formatted
}

// ResolveOptions discovers any enclosing `lowlight.toml` / `[workspace]`, applies `[build]` defaults
// (while letting explicit CLI flags win), and resolves zero-arg or `-p <pkg>` entry paths.
func ResolveOptions(opts Options) (Options, *manifest.ProjectContext, error) {
	ctx := opts.ManifestCtx
	if ctx == nil && opts.SrcOverride == "" {
		searchPath := opts.EntryPath
		if searchPath == "" && len(opts.Targets) > 0 {
			searchPath = opts.Targets[0]
		}
		if searchPath == "" {
			if cwd, err := os.Getwd(); err == nil {
				searchPath = cwd
			}
		}
		if searchPath != "" {
			var err error
			ctx, err = manifest.DiscoverProject(searchPath)
			if err != nil {
				return opts, nil, fmt.Errorf("failed to load lowlight.toml: %w", err)
			}
		}
	}

	// Select workspace member if `-p <pkg>` was specified.
	if opts.Package != "" {
		if ctx == nil {
			return opts, nil, fmt.Errorf("-p %q requires a lowlight.toml project or workspace", opts.Package)
		}
		member, err := selectWorkspaceMember(ctx, opts.Package)
		if err != nil {
			return opts, nil, err
		}
		ctx = &manifest.ProjectContext{
			ActiveManifest:    member,
			WorkspaceManifest: ctx.WorkspaceManifest,
			WorkspaceMembers:  ctx.WorkspaceMembers,
		}
		if opts.EntryPath == "" {
			opts.EntryPath = member.Dir
		}
	}

	// Zero-arg entry path resolution inside a `lowlight.toml` project/workspace (§12.6).
	if opts.EntryPath == "" && len(opts.Targets) == 0 && ctx != nil {
		if ctx.ActiveManifest != nil && ctx.ActiveManifest.HasPackage {
			opts.EntryPath = defaultPackageEntryDir(ctx.ActiveManifest.Dir)
		} else if ctx.WorkspaceManifest != nil && len(ctx.WorkspaceMembers) == 1 {
			members := sortedWorkspaceMembers(ctx)
			m := members[0]
			ctx = &manifest.ProjectContext{
				ActiveManifest:    m,
				WorkspaceManifest: ctx.WorkspaceManifest,
				WorkspaceMembers:  ctx.WorkspaceMembers,
			}
			opts.EntryPath = defaultPackageEntryDir(m.Dir)
		} else if ctx.WorkspaceManifest != nil && !opts.Workspace {
			var names []string
			for _, m := range sortedWorkspaceMembers(ctx) {
				if m.HasPackage {
					names = append(names, m.Package.Name)
				}
			}
			return opts, ctx, fmt.Errorf("virtual workspace at %s has multiple members (%s); pass `-p <pkg>`, `--workspace`, or a package path",
				ctx.WorkspaceManifest.Dir, strings.Join(names, ", "))
		}
	}

	// Apply [build] defaults from active manifest (falling back to workspace root manifest).
	applyBuildManifestDefaults(&opts, ctx)
	if opts.ProjectRoot == "" && ctx != nil && ctx.RootDir() != "" {
		opts.ProjectRoot = ctx.RootDir()
	}
	opts.ManifestCtx = ctx
	return opts, ctx, nil
}

func sortedWorkspaceMembers(ctx *manifest.ProjectContext) []*manifest.Manifest {
	if ctx == nil || len(ctx.WorkspaceMembers) == 0 {
		return nil
	}
	names := make([]string, 0, len(ctx.WorkspaceMembers))
	for n := range ctx.WorkspaceMembers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*manifest.Manifest, 0, len(names))
	for _, n := range names {
		out = append(out, ctx.WorkspaceMembers[n])
	}
	return out
}

func selectWorkspaceMember(ctx *manifest.ProjectContext, selector string) (*manifest.Manifest, error) {
	if ctx.ActiveManifest != nil && ctx.ActiveManifest.HasPackage && ctx.ActiveManifest.Package.Name == selector {
		return ctx.ActiveManifest, nil
	}
	if m, ok := ctx.WorkspaceMembers[selector]; ok {
		return m, nil
	}
	for _, m := range sortedWorkspaceMembers(ctx) {
		if filepath.Base(m.Dir) == selector {
			return m, nil
		}
		if ctx.WorkspaceManifest != nil {
			if rel, err := filepath.Rel(ctx.WorkspaceManifest.Dir, m.Dir); err == nil && filepath.ToSlash(rel) == selector {
				return m, nil
			}
		}
	}
	var avail []string
	for _, m := range sortedWorkspaceMembers(ctx) {
		if m.HasPackage {
			avail = append(avail, m.Package.Name)
		}
	}
	if len(avail) > 0 {
		return nil, fmt.Errorf("package %q not found in workspace (available: %s)", selector, strings.Join(avail, ", "))
	}
	return nil, fmt.Errorf("package %q does not match manifest package", selector)
}

func defaultPackageEntryDir(pkgDir string) string {
	if dirHasLLFiles(pkgDir) {
		return pkgDir
	}
	// If root has no direct .ll files, check for a single binary under cmd/<app>/
	cmdDir := filepath.Join(pkgDir, "cmd")
	if entries, err := os.ReadDir(cmdDir); err == nil {
		var subDirs []string
		for _, e := range entries {
			if e.IsDir() {
				sub := filepath.Join(cmdDir, e.Name())
				if dirHasLLFiles(sub) {
					subDirs = append(subDirs, sub)
				}
			}
		}
		if len(subDirs) == 1 {
			return subDirs[0]
		}
	}
	return pkgDir
}

func dirHasLLFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ll") {
			return true
		}
	}
	return false
}

func applyBuildManifestDefaults(opts *Options, ctx *manifest.ProjectContext) {
	if ctx == nil {
		return
	}
	manifests := []*manifest.Manifest{}
	if ctx.ActiveManifest != nil {
		manifests = append(manifests, ctx.ActiveManifest)
	}
	if ctx.WorkspaceManifest != nil && ctx.WorkspaceManifest != ctx.ActiveManifest {
		manifests = append(manifests, ctx.WorkspaceManifest)
	}
	for _, m := range manifests {
		if opts.Backend == "" && os.Getenv("LOWLIGHT_BACKEND") == "" && m.Build.Backend != "" {
			switch strings.ToLower(m.Build.Backend) {
			case "llvm", "ll":
				opts.Backend = BackendLLVM
			case "c", "c11":
				opts.Backend = BackendC11
			}
		}
		if !opts.ReleaseSet && !opts.Release && m.Build.Release != nil {
			opts.Release = *m.Build.Release
		}
		if opts.TargetTriple == "" && m.Build.Target != "" {
			opts.TargetTriple = m.Build.Target
		}
		if opts.CC == "" && m.Build.CC != "" {
			opts.CC = m.Build.CC
		}
	}
}

func collectManifestLinkFlags(ctx *manifest.ProjectContext, resolved *manifest.ResolvedGraph, target manifest.TargetInfo) (cFlags []string, cLibs []string) {
	seenFlags := make(map[string]bool)
	seenLibs := make(map[string]bool)
	addFlag := func(f string) {
		f = strings.TrimSpace(f)
		if f != "" && !seenFlags[f] {
			seenFlags[f] = true
			cFlags = append(cFlags, f)
		}
	}
	addLib := func(l string) {
		l = strings.TrimSpace(l)
		if l == "" {
			return
		}
		if !strings.HasPrefix(l, "-") {
			l = "-l" + l
		}
		if !seenLibs[l] {
			seenLibs[l] = true
			cLibs = append(cLibs, l)
		}
	}
	collectFrom := func(m *manifest.Manifest) {
		if m == nil {
			return
		}
		for _, f := range m.Build.CFlags {
			addFlag(f)
		}
		for _, l := range m.Build.CLibs {
			addLib(l)
		}
		var selectors []string
		for sel := range m.Targets {
			selectors = append(selectors, sel)
		}
		manifest.SortTargetSelectors(selectors)
		for _, sel := range selectors {
			if !target.MatchesSelector(sel) {
				continue
			}
			tc := m.Targets[sel]
			for _, f := range tc.CFlags {
				addFlag(f)
			}
			for _, l := range tc.CLibs {
				addLib(l)
			}
		}
	}

	if ctx != nil {
		if ctx.WorkspaceManifest != nil && ctx.WorkspaceManifest != ctx.ActiveManifest {
			collectFrom(ctx.WorkspaceManifest)
		}
		collectFrom(ctx.ActiveManifest)
	}
	if resolved != nil {
		names := make([]string, 0, len(resolved.Packages))
		for n := range resolved.Packages {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			pkg := resolved.Packages[n]
			if pkg == nil || pkg.Dir == "" {
				continue
			}
			mPath := filepath.Join(pkg.Dir, manifest.ManifestFilename)
			if depM, err := manifest.LoadManifest(mPath); err == nil && depM != nil {
				collectFrom(depM)
			}
		}
	}
	return cFlags, cLibs
}

// Check runs lexing, parsing, import resolution, type checking, monomorphization,
// ownership/move analysis, borrow checking, and arena escape verification.
func Check(opts Options) (*sema.CheckedProgram, string, error) {
	var ctx *manifest.ProjectContext
	var err error
	opts, ctx, err = ResolveOptions(opts)
	if err != nil {
		return nil, "", err
	}
	if opts.EntryPath == "" {
		return nil, "", errors.New("missing input .ll source file or package directory")
	}
	rep := diag.NewReporter()
	prog := sema.CheckWithOptions(sema.CheckOptions{
		EntryPath:    opts.EntryPath,
		SrcOverride:  opts.SrcOverride,
		ProjectRoot:  opts.ProjectRoot,
		TargetTriple: opts.TargetTriple,
		TestMode:     opts.TestMode,
		ManifestCtx:  ctx,
		CacheRoot:    opts.CacheRoot,
		Locked:       opts.Locked,
		Offline:      opts.Offline,
	}, rep)
	formatted := rep.Format()
	if rep.HasErrors() {
		return nil, "", &CompileError{Formatted: formatted}
	}
	return prog, formatted, nil
}

// EmitC runs semantic checking, lowers to MIR, and emits portable ISO C11 source code.
func EmitC(opts Options) (string, *sema.CheckedProgram, error) {
	opts, _, err := ResolveOptions(opts)
	if err != nil {
		return "", nil, err
	}
	checked, warnings, err := Check(opts)
	if err != nil {
		return "", nil, err
	}
	if warnings != "" {
		fmt.Fprint(opts.warnOut(), warnings)
	}
	mirProg := ir.Lower(checked, opts.Release, opts.TestMode)
	if err := filterTestFuncs(mirProg, opts); err != nil {
		return "", nil, err
	}
	mirProg.TargetTriple = opts.TargetTriple
	cSource := codegen.EmitC(mirProg)
	return cSource, checked, nil
}

// EmitLLVM runs semantic checking, lowers to MIR, and emits pure, portable LLVM IR text (.ll).
func EmitLLVM(opts Options) (string, *sema.CheckedProgram, error) {
	opts, _, err := ResolveOptions(opts)
	if err != nil {
		return "", nil, err
	}
	checked, warnings, err := Check(opts)
	if err != nil {
		return "", nil, err
	}
	if warnings != "" {
		fmt.Fprint(opts.warnOut(), warnings)
	}
	mirProg := ir.Lower(checked, opts.Release, opts.TestMode)
	if err := filterTestFuncs(mirProg, opts); err != nil {
		return "", nil, err
	}
	mirProg.TargetTriple = opts.TargetTriple
	llvmSource := codegen.EmitLLVM(mirProg)
	return llvmSource, checked, nil
}

func filterTestFuncs(mirProg *ir.Program, opts Options) error {
	if !opts.TestMode || opts.TestFilter == "" || mirProg.TestMainFunc != nil {
		return nil
	}
	re, err := regexp.Compile(opts.TestFilter)
	if err != nil {
		return fmt.Errorf("invalid -run filter %q: %w", opts.TestFilter, err)
	}
	var filtered []*ir.Function
	for _, fn := range mirProg.TestFuncs {
		if re.MatchString(fn.Name) || (fn.TestDesc != "" && re.MatchString(fn.TestDesc)) {
			filtered = append(filtered, fn)
		}
	}
	mirProg.TestFuncs = filtered
	if len(filtered) == 0 {
		return ErrNoTests
	}
	return nil
}

// ResolveCC selects the C11/LLVM compiler executable to invoke.
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

// DefaultOutputBin derives an executable path from `entryPath` (e.g. `foo/hello.ll` -> `hello`,
// or a package directory -> `[package].bin` or directory name).
func DefaultOutputBin(entryPath string) string {
	return defaultOutputBinWithManifest(entryPath, nil)
}

func defaultOutputBinWithManifest(entryPath string, ctx *manifest.ProjectContext) string {
	if ctx != nil && ctx.ActiveManifest != nil && ctx.ActiveManifest.HasPackage {
		absEntry, err := filepath.Abs(entryPath)
		if err != nil {
			absEntry = entryPath
		}
		if filepath.Clean(absEntry) == filepath.Clean(ctx.ActiveManifest.Dir) {
			if ctx.ActiveManifest.Package.Bin != "" {
				return ctx.ActiveManifest.Package.Bin
			}
			if ctx.ActiveManifest.Package.Name != "" {
				return ctx.ActiveManifest.Package.Name
			}
		}
	}
	clean := filepath.Clean(entryPath)
	if clean == "." {
		if abs, err := filepath.Abs(clean); err == nil {
			clean = abs
		}
	}
	base := filepath.Base(clean)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "a.out"
	}
	return base
}

// Build compiles a Lowlight entry module or package into a native executable via LLVM IR or C11.
func Build(opts Options) (string, error) {
	out, _, err := BuildWithStats(opts)
	return out, err
}

// BuildWithStats compiles a Lowlight entry module or package and returns two-phase caching stats.
func BuildWithStats(opts Options) (string, BuildStats, error) {
	var ctx *manifest.ProjectContext
	var err error
	opts, ctx, err = ResolveOptions(opts)
	if err != nil {
		return "", BuildStats{}, err
	}
	if opts.EffectiveBackend() == BackendLLVM {
		return buildLLVM(opts, ctx)
	}
	return buildC11(opts, ctx)
}

func buildC11(opts Options, ctx *manifest.ProjectContext) (string, BuildStats, error) {
	checked, warnings, err := Check(opts)
	if err != nil {
		return "", BuildStats{}, err
	}
	if warnings != "" {
		fmt.Fprint(opts.warnOut(), warnings)
	}
	if opts.TestMode {
		if checked.TestMainFunc == nil && len(checked.TestFuncs) == 0 {
			return "", BuildStats{}, ErrNoTests
		}
	} else {
		if checked.MainFunc == nil {
			return "", BuildStats{}, fmt.Errorf("%s: cannot build executable: entry package defines no 'main' function", opts.EntryPath)
		}
	}

	mirProg := ir.Lower(checked, opts.Release, opts.TestMode)
	if err := filterTestFuncs(mirProg, opts); err != nil {
		return "", BuildStats{}, err
	}
	mirProg.TargetTriple = opts.TargetTriple

	outBin := opts.OutPath
	if outBin == "" {
		outBin = defaultOutputBinWithManifest(opts.EntryPath, ctx)
	}
	absOut, err := filepath.Abs(outBin)
	if err != nil {
		absOut = outBin
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
		return "", BuildStats{}, fmt.Errorf("failed to create output directory: %w", err)
	}

	if !opts.NoIncremental {
		stats, splitErr := buildTwoPhase(BackendC11, opts, ctx, checked, mirProg, absOut)
		if splitErr != nil {
			return "", stats, splitErr
		}
		if stats.TwoPhase {
			return absOut, stats, nil
		}
	}

	cSource := codegen.EmitC(mirProg)

	tmpDir, err := os.MkdirTemp("", "lowlight-build-*")
	if err != nil {
		return "", BuildStats{}, fmt.Errorf("failed to create temp build directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cFile := filepath.Join(tmpDir, "program.c")
	if err := os.WriteFile(cFile, []byte(cSource), 0o644); err != nil {
		return "", BuildStats{}, fmt.Errorf("failed to write temporary C file: %w", err)
	}

	cc := ResolveCC(opts.CC)
	if cc == "cc" {
		if clangPath, err := exec.LookPath("clang"); err == nil && clangPath != "" {
			cc = clangPath
		}
	}
	// `ptr_cast` lets Lowlight code view memory as another type (List and string do), which
	// C's strict-aliasing rules would let the optimizer assume never happens.
	args := []string{"-std=c11", "-fno-strict-aliasing"}
	if opts.TargetTriple != "" {
		baseCC := strings.ToLower(filepath.Base(cc))
		if strings.Contains(baseCC, "gcc") && !strings.Contains(baseCC, "clang") {
			return "", BuildStats{}, fmt.Errorf("--target=%s requires clang (current C compiler is %s; pass --cc=clang)", opts.TargetTriple, cc)
		}
		args = append(args, "--target="+opts.TargetTriple)
	}
	if opts.Release {
		args = append(args, "-O2")
	} else {
		args = append(args, "-g", "-O0")
	}
	mFlags, mLibs := collectManifestLinkFlags(ctx, checked.ResolvedDeps, checked.Target)
	args = append(args, "-o", absOut, cFile)
	args = append(args, mFlags...)
	args = append(args, opts.ExtraCFlags...)
	args = append(args, mLibs...)
	args = append(args, targetSystemLibs(checked.Target)...)

	var ccStderr bytes.Buffer
	cmd := exec.Command(cc, args...)
	cmd.Stderr = &ccStderr
	cmd.Stdout = &ccStderr
	if runErr := cmd.Run(); runErr != nil {
		return "", BuildStats{}, fmt.Errorf("C11 backend compilation failed (%s %s):\n%s\n%w",
			cc, strings.Join(args, " "), strings.TrimSpace(ccStderr.String()), runErr)
	}
	return absOut, BuildStats{}, nil
}

func targetSystemLibs(t manifest.TargetInfo) []string {
	if t.IsUnix {
		return []string{"-lm", "-ldl"}
	}
	return nil
}

func buildLLVM(opts Options, ctx *manifest.ProjectContext) (string, BuildStats, error) {
	checked, warnings, err := Check(opts)
	if err != nil {
		return "", BuildStats{}, err
	}
	if warnings != "" {
		fmt.Fprint(opts.warnOut(), warnings)
	}
	if opts.TestMode {
		if checked.TestMainFunc == nil && len(checked.TestFuncs) == 0 {
			return "", BuildStats{}, ErrNoTests
		}
	} else {
		if checked.MainFunc == nil {
			return "", BuildStats{}, fmt.Errorf("%s: cannot build executable: entry package defines no 'main' function", opts.EntryPath)
		}
	}

	mirProg := ir.Lower(checked, opts.Release, opts.TestMode)
	if err := filterTestFuncs(mirProg, opts); err != nil {
		return "", BuildStats{}, err
	}
	mirProg.TargetTriple = opts.TargetTriple

	outBin := opts.OutPath
	if outBin == "" {
		outBin = defaultOutputBinWithManifest(opts.EntryPath, ctx)
	}
	absOut, err := filepath.Abs(outBin)
	if err != nil {
		absOut = outBin
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
		return "", BuildStats{}, fmt.Errorf("failed to create output directory: %w", err)
	}

	if !opts.NoIncremental {
		stats, splitErr := buildTwoPhase(BackendLLVM, opts, ctx, checked, mirProg, absOut)
		if splitErr != nil {
			return "", stats, splitErr
		}
		if stats.TwoPhase {
			return absOut, stats, nil
		}
	}

	llvmSource := codegen.EmitLLVM(mirProg)

	tmpDir, err := os.MkdirTemp("", "lowlight-llvm-build-*")
	if err != nil {
		return "", BuildStats{}, fmt.Errorf("failed to create temp build directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	llFile := filepath.Join(tmpDir, "program.ll")
	if err := os.WriteFile(llFile, []byte(llvmSource), 0o644); err != nil {
		return "", BuildStats{}, fmt.Errorf("failed to write temporary LLVM file: %w", err)
	}

	cc := ResolveCC(opts.CC)
	if cc == "cc" {
		if clangPath, err := exec.LookPath("clang"); err == nil && clangPath != "" {
			cc = clangPath
		}
	}

	args := []string{"-Wno-override-module"}
	if opts.TargetTriple != "" {
		args = append(args, "--target="+opts.TargetTriple)
	}
	if opts.Release {
		args = append(args, "-O2")
	} else {
		args = append(args, "-g", "-O0")
	}
	mFlags, mLibs := collectManifestLinkFlags(ctx, checked.ResolvedDeps, checked.Target)
	args = append(args, "-o", absOut, llFile)
	args = append(args, mFlags...)
	args = append(args, opts.ExtraCFlags...)
	args = append(args, mLibs...)
	args = append(args, targetSystemLibs(checked.Target)...)

	var ccStderr bytes.Buffer
	cmd := exec.Command(cc, args...)
	cmd.Stderr = &ccStderr
	cmd.Stdout = &ccStderr
	if runErr := cmd.Run(); runErr != nil {
		return "", BuildStats{}, fmt.Errorf("LLVM backend compilation failed (%s %s):\n%s\n%w",
			cc, strings.Join(args, " "), strings.TrimSpace(ccStderr.String()), runErr)
	}
	return absOut, BuildStats{}, nil
}

// Run compiles a Lowlight program or package into a temporary executable and runs it immediately.
func Run(opts Options, progArgs []string, stdout, stderr io.Writer) (int, error) {
	tmpDir, err := os.MkdirTemp("", "lowlight-run-*")
	if err != nil {
		return 1, fmt.Errorf("failed to create temp run directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	buildOpts := opts
	buildOpts.OutPath = filepath.Join(tmpDir, "app")
	if buildOpts.WarnWriter == nil && stderr != nil {
		buildOpts.WarnWriter = stderr
	}
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

// Test compiles and runs Lowlight tests for specified package directories or explicit `.ll` files.
// Directory targets are discovered per-package (compiling all sibling `.ll` and `*_test.ll` files in each package together once).
func Test(opts Options, stdout, stderr io.Writer) (int, error) {
	opts.TestMode = true

	// Resolve enclosing manifest/workspace options (including `[build]` defaults like
	// target triple), preserving explicit targets when neither `-p` nor `--workspace` is set.
	rawTargets := opts.Targets
	if opts.Package != "" || opts.Workspace || (len(rawTargets) == 0 && opts.EntryPath == "") || (opts.ManifestCtx == nil && opts.SrcOverride == "") {
		resolvedOpts, ctx, err := ResolveOptions(opts)
		if err != nil {
			return 1, err
		}
		explicitTargets := len(rawTargets) > 0
		opts = resolvedOpts
		if opts.Workspace && ctx != nil && len(ctx.WorkspaceMembers) > 0 {
			rawTargets = nil
			for _, m := range sortedWorkspaceMembers(ctx) {
				rawTargets = append(rawTargets, m.Dir)
			}
		} else if opts.Package != "" && ctx != nil && ctx.ActiveManifest != nil {
			rawTargets = []string{ctx.ActiveManifest.Dir}
		} else if !explicitTargets && len(rawTargets) == 0 && opts.EntryPath != "" {
			rawTargets = []string{opts.EntryPath}
		}
	}

	if len(rawTargets) == 0 {
		if opts.EntryPath != "" {
			rawTargets = []string{opts.EntryPath}
		} else {
			rawTargets = []string{"."}
		}
	}

	type testCandidate struct {
		path                 string
		isDir                bool
		isExplicitSingleFile bool
	}
	var candidates []testCandidate
	visited := make(map[string]bool)

	isSingleExplicit := len(rawTargets) == 1
	targetInfo := manifest.ParseTarget(opts.TargetTriple)

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
			pkgDirs, err := discoverTestablePackageDirs(cleanTarget, targetInfo)
			if err != nil {
				return 1, fmt.Errorf("failed to scan directory %q: %w", target, err)
			}
			for _, d := range pkgDirs {
				abs, _ := filepath.Abs(d)
				if !visited[abs] {
					visited[abs] = true
					candidates = append(candidates, testCandidate{path: d, isDir: true})
				}
			}
		} else {
			abs, _ := filepath.Abs(cleanTarget)
			if !visited[abs] {
				visited[abs] = true
				candidates = append(candidates, testCandidate{path: cleanTarget, isDir: false, isExplicitSingleFile: isSingleExplicit})
			}
		}
	}

	if len(candidates) == 0 {
		if opts.TestList {
			return 0, nil
		}
		targetName := "."
		if len(rawTargets) == 1 {
			targetName = rawTargets[0]
		}
		if opts.TestJSON {
			if stdout != nil {
				emitTestJSONEvent(stdout, TestEvent{Event: "summary", Passed: 0, Failed: 0, Total: 0, Packages: 0})
			}
			return 0, nil
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "?   \t%s\t[no tests to run]\n", targetName)
		}
		return 0, nil
	}

	if opts.TestList {
		listInputs := make([]testListTarget, len(candidates))
		for i, c := range candidates {
			listInputs[i] = testListTarget{path: c.path, isDir: c.isDir}
		}
		return runTestList(opts, listInputs, targetInfo, stdout)
	}

	if opts.TestJSON {
		jsonInputs := make([]testListTarget, len(candidates))
		for i, c := range candidates {
			jsonInputs[i] = testListTarget{path: c.path, isDir: c.isDir, isExplicitSingleFile: c.isExplicitSingleFile}
		}
		return runTestsJSON(opts, jsonInputs, targetInfo, stdout, stderr)
	}

	// Single explicit file mode:
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
		fileOpts.Package = ""
		fileOpts.Workspace = false
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

	// Package directory or multi-target mode:
	var totalPassed int
	var totalFailed int
	var totalTests int
	var totalTargetsTested int
	var anyFailed bool

	reSummary := regexp.MustCompile(`Test results: (\d+) passed, (\d+) failed \(total (\d+)\)`)

	for _, cand := range candidates {
		if !cand.isDir && !fileMayHaveTests(cand.path) {
			continue
		}

		unitOpts := opts
		unitOpts.EntryPath = cand.path
		unitOpts.Targets = nil
		unitOpts.Package = ""
		unitOpts.Workspace = false
		unitOpts.ManifestCtx = nil

		var buf bytes.Buffer
		var outWriter io.Writer = &buf
		if stdout != nil {
			outWriter = io.MultiWriter(stdout, &buf)
		}

		code, err := runSingleTest(unitOpts, outWriter, stderr)
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

		totalTargetsTested++
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

	if totalTargetsTested == 0 {
		if anyFailed {
			return 1, nil
		}
		targetName := "."
		if len(rawTargets) == 1 {
			targetName = rawTargets[0]
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "?   \t%s\t[no tests to run]\n", targetName)
		}
		return 0, nil
	}

	if totalTargetsTested > 1 && stdout != nil {
		fmt.Fprintf(stdout, "\nTotal test results: %d passed, %d failed across %d package(s) (total %d)\n",
			totalPassed, totalFailed, totalTargetsTested, totalTests)
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
	if buildOpts.WarnWriter == nil && stderr != nil {
		buildOpts.WarnWriter = stderr
	}
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

// discoverTestablePackageDirs walks `dir` and returns every package directory that directly
// contains at least one target-matching `.ll` file with `@test` or `test_main`.
func discoverTestablePackageDirs(dir string, target manifest.TargetInfo) ([]string, error) {
	cleanDir := filepath.Clean(dir)
	var pkgDirs []string
	err := filepath.WalkDir(cleanDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != cleanDir {
			name := d.Name()
			if strings.HasPrefix(name, ".") && name != "." && name != ".." {
				return filepath.SkipDir
			}
			if name == "editors" || name == "node_modules" || name == "vendor" || name == "docs" {
				return filepath.SkipDir
			}
		}
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return nil
		}
		hasTest := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".ll") {
				continue
			}
			// Leave legacy WIP std/json.ll excluded when scanning std/ root cleanly.
			if e.Name() == "json.ll" && filepath.Base(path) == "std" {
				continue
			}
			if !manifest.MatchesTargetSuffix(e.Name(), target) {
				continue
			}
			if fileMayHaveTests(filepath.Join(path, e.Name())) {
				hasTest = true
				break
			}
		}
		if hasTest {
			pkgDirs = append(pkgDirs, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(pkgDirs)
	return pkgDirs, nil
}

func fileMayHaveTests(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "@test") || strings.Contains(content, "test_main")
}
