// Command lowlight is the Lowlight v1.0 compiler and package manager CLI.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/driver"
	"lowlight/internal/lsp"
	"lowlight/internal/manifest"
)

func main() {
	os.Exit(runCLI(os.Args[1:]))
}

func runCLI(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 1
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return 0

	case "version", "-v", "--version":
		backendDesc := "direct LLVM IR backend"
		if (driver.Options{}).EffectiveBackend() == driver.BackendC11 {
			backendDesc = "C11 backend"
		}
		fmt.Printf("lowlight %s (v1.0 specification, %s)\n", driver.Version, backendDesc)
		return 0

	case "check":
		opts, _, err := parseCommonFlags(rest, false, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if opts.Workspace {
			return runWorkspaceCheck(opts)
		}
		_, warnings, err := driver.Check(opts)
		if err != nil {
			fmt.Fprint(os.Stderr, err.Error())
			if !strings.HasSuffix(err.Error(), "\n") {
				fmt.Fprintln(os.Stderr)
			}
			return 1
		}
		if warnings != "" {
			fmt.Fprint(os.Stderr, warnings)
		}
		return 0

	case "emit-c":
		opts, _, err := parseCommonFlags(rest, true, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		cSrc, _, err := driver.EmitC(opts)
		if err != nil {
			fmt.Fprint(os.Stderr, err.Error())
			if !strings.HasSuffix(err.Error(), "\n") {
				fmt.Fprintln(os.Stderr)
			}
			return 1
		}
		if opts.OutPath != "" {
			if err := os.WriteFile(opts.OutPath, []byte(cSrc), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error writing %s: %v\n", opts.OutPath, err)
				return 1
			}
		} else {
			fmt.Print(cSrc)
		}
		return 0

	case "emit-llvm":
		opts, _, err := parseCommonFlags(rest, true, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		opts.Backend = driver.BackendLLVM
		llvmSrc, _, err := driver.EmitLLVM(opts)
		if err != nil {
			fmt.Fprint(os.Stderr, err.Error())
			if !strings.HasSuffix(err.Error(), "\n") {
				fmt.Fprintln(os.Stderr)
			}
			return 1
		}
		if opts.OutPath != "" {
			if err := os.WriteFile(opts.OutPath, []byte(llvmSrc), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error writing %s: %v\n", opts.OutPath, err)
				return 1
			}
		} else {
			fmt.Print(llvmSrc)
		}
		return 0

	case "build":
		opts, _, err := parseCommonFlags(rest, true, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if opts.Workspace {
			return runWorkspaceBuild(opts)
		}
		if _, err := driver.Build(opts); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		return 0

	case "run":
		opts, progArgs, err := parseCommonFlags(rest, false, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		code, err := driver.Run(opts, progArgs, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		return code

	case "test":
		opts, err := parseTestFlags(rest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		code, err := driver.Test(opts, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		return code

	case "init":
		return runInit(rest, os.Stdout, os.Stderr)

	case "new":
		return runNew(rest, os.Stdout, os.Stderr)

	case "add":
		return runAdd(rest, os.Stdout, os.Stderr)

	case "remove":
		return runRemove(rest, os.Stdout, os.Stderr)

	case "fetch":
		return runFetch(rest, os.Stdout, os.Stderr)

	case "update":
		return runUpdate(rest, os.Stdout, os.Stderr)

	case "vendor":
		return runVendor(rest, os.Stdout, os.Stderr)

	case "tree":
		return runTree(rest, os.Stdout, os.Stderr)

	case "lsp":
		return runLSP(rest)

	case "fmt":
		return runFmt(rest)

	case "doc":
		return runDoc(rest)

	default:
		fmt.Fprintf(os.Stderr, "error: unknown subcommand %q\n\n", sub)
		printUsage(os.Stderr)
		return 1
	}
}

func sortedMembers(ctx *manifest.ProjectContext) []*manifest.Manifest {
	if ctx == nil {
		return nil
	}
	if len(ctx.WorkspaceMembers) == 0 {
		if ctx.ActiveManifest != nil {
			return []*manifest.Manifest{ctx.ActiveManifest}
		}
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

func runWorkspaceCheck(opts driver.Options) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	ctx, err := manifest.DiscoverProject(cwd)
	if err != nil || ctx == nil {
		fmt.Fprintf(os.Stderr, "error: --workspace requires a lowlight.toml workspace\n")
		return 1
	}
	members := sortedMembers(ctx)
	exitCode := 0
	for _, m := range members {
		mOpts := opts
		mOpts.Workspace = false
		mOpts.Package = m.Package.Name
		mOpts.EntryPath = m.Dir
		mOpts.ManifestCtx = &manifest.ProjectContext{
			ActiveManifest:    m,
			WorkspaceManifest: ctx.WorkspaceManifest,
			WorkspaceMembers:  ctx.WorkspaceMembers,
		}
		_, warnings, checkErr := driver.Check(mOpts)
		if warnings != "" {
			fmt.Fprint(os.Stderr, warnings)
		}
		if checkErr != nil {
			fmt.Fprintln(os.Stderr, checkErr.Error())
			exitCode = 1
		}
	}
	return exitCode
}

func runWorkspaceBuild(opts driver.Options) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	ctx, err := manifest.DiscoverProject(cwd)
	if err != nil || ctx == nil {
		fmt.Fprintf(os.Stderr, "error: --workspace requires a lowlight.toml workspace\n")
		return 1
	}
	members := sortedMembers(ctx)
	exitCode := 0
	for _, m := range members {
		mOpts := opts
		mOpts.Workspace = false
		mOpts.Package = m.Package.Name
		mOpts.EntryPath = m.Dir
		mOpts.OutPath = ""
		mOpts.ManifestCtx = &manifest.ProjectContext{
			ActiveManifest:    m,
			WorkspaceManifest: ctx.WorkspaceManifest,
			WorkspaceMembers:  ctx.WorkspaceMembers,
		}
		checked, warnings, checkErr := driver.Check(mOpts)
		if warnings != "" {
			fmt.Fprint(os.Stderr, warnings)
		}
		if checkErr != nil {
			fmt.Fprintln(os.Stderr, checkErr.Error())
			exitCode = 1
			continue
		}
		if checked.MainFunc != nil {
			if _, buildErr := driver.Build(mOpts); buildErr != nil {
				fmt.Fprintln(os.Stderr, buildErr.Error())
				exitCode = 1
			}
		}
	}
	return exitCode
}

func parseCommonFlags(args []string, allowOut bool, allowProgArgs bool) (driver.Options, []string, error) {
	var opts driver.Options
	if cflags := os.Getenv("LOWLIGHT_CFLAGS"); cflags != "" {
		opts.ExtraCFlags = append(opts.ExtraCFlags, strings.Fields(cflags)...)
	}
	var progArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && allowProgArgs {
			progArgs = append(progArgs, args[i+1:]...)
			break
		}
		switch arg {
		case "--release":
			opts.Release = true
			opts.ReleaseSet = true
		case "--workspace":
			opts.Workspace = true
		case "--locked":
			opts.Locked = true
		case "--offline":
			opts.Offline = true
		case "-p", "--package":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing argument for %s", arg)
			}
			i++
			opts.Package = args[i]
		case "--backend":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing argument for --backend")
			}
			i++
			switch strings.ToLower(args[i]) {
			case "llvm", "ll":
				opts.Backend = driver.BackendLLVM
			case "c", "c11":
				opts.Backend = driver.BackendC11
			default:
				return opts, nil, fmt.Errorf("unknown backend %q (expected 'c11' or 'llvm')", args[i])
			}
		case "--cc":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing argument for --cc")
			}
			i++
			opts.CC = args[i]
		case "--target":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing argument for --target")
			}
			i++
			opts.TargetTriple = args[i]
		case "-o":
			if !allowOut {
				return opts, nil, fmt.Errorf("-o is not supported for this command")
			}
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing argument for -o")
			}
			i++
			opts.OutPath = args[i]
		default:
			if strings.HasPrefix(arg, "--backend=") {
				val := strings.TrimPrefix(arg, "--backend=")
				switch strings.ToLower(val) {
				case "llvm", "ll":
					opts.Backend = driver.BackendLLVM
				case "c", "c11":
					opts.Backend = driver.BackendC11
				default:
					return opts, nil, fmt.Errorf("unknown backend %q (expected 'c11' or 'llvm')", val)
				}
				continue
			}
			if strings.HasPrefix(arg, "--cc=") {
				opts.CC = strings.TrimPrefix(arg, "--cc=")
				continue
			}
			if strings.HasPrefix(arg, "--target=") {
				opts.TargetTriple = strings.TrimPrefix(arg, "--target=")
				continue
			}
			if strings.HasPrefix(arg, "--package=") {
				opts.Package = strings.TrimPrefix(arg, "--package=")
				continue
			}
			if strings.HasPrefix(arg, "-p=") {
				opts.Package = strings.TrimPrefix(arg, "-p=")
				continue
			}
			if len(arg) > 0 && arg[0] == '-' {
				return opts, nil, fmt.Errorf("unknown flag %q", arg)
			}
			if opts.EntryPath == "" {
				opts.EntryPath = arg
			} else if allowProgArgs {
				// Any positional args after <path> in `lowlight run` are forwarded to the program
				progArgs = append(progArgs, args[i:]...)
				i = len(args)
			} else {
				return opts, nil, fmt.Errorf("unexpected extra argument %q", arg)
			}
		}
	}

	if opts.EntryPath == "" && opts.Package == "" && !opts.Workspace {
		// Allow zero-arg invocation inside a `lowlight.toml` project or workspace (§12.6).
		if cwd, err := os.Getwd(); err == nil {
			if ctx, derr := manifest.DiscoverProject(cwd); derr == nil && ctx != nil {
				return opts, progArgs, nil
			}
		}
		return opts, nil, fmt.Errorf("missing <file.ll|dir> path (or run inside a directory containing lowlight.toml)")
	}
	return opts, progArgs, nil
}

func parseTestFlags(args []string) (driver.Options, error) {
	var opts driver.Options
	if cflags := os.Getenv("LOWLIGHT_CFLAGS"); cflags != "" {
		opts.ExtraCFlags = append(opts.ExtraCFlags, strings.Fields(cflags)...)
	}
	opts.TestMode = true
	var targets []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--release":
			opts.Release = true
			opts.ReleaseSet = true
		case "--workspace":
			opts.Workspace = true
		case "--locked":
			opts.Locked = true
		case "--offline":
			opts.Offline = true
		case "-p", "--package":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing argument for %s", arg)
			}
			i++
			opts.Package = args[i]
		case "--backend":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing argument for --backend")
			}
			i++
			switch strings.ToLower(args[i]) {
			case "llvm", "ll":
				opts.Backend = driver.BackendLLVM
			case "c", "c11":
				opts.Backend = driver.BackendC11
			default:
				return opts, fmt.Errorf("unknown backend %q (expected 'c11' or 'llvm')", args[i])
			}
		case "--cc":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing argument for --cc")
			}
			i++
			opts.CC = args[i]
		case "--target":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing argument for --target")
			}
			i++
			opts.TargetTriple = args[i]
		case "-run", "--run":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing argument for %s", arg)
			}
			i++
			opts.TestFilter = args[i]
		default:
			if strings.HasPrefix(arg, "--backend=") {
				val := strings.TrimPrefix(arg, "--backend=")
				switch strings.ToLower(val) {
				case "llvm", "ll":
					opts.Backend = driver.BackendLLVM
				case "c", "c11":
					opts.Backend = driver.BackendC11
				default:
					return opts, fmt.Errorf("unknown backend %q (expected 'c11' or 'llvm')", val)
				}
				continue
			}
			if strings.HasPrefix(arg, "--cc=") {
				opts.CC = strings.TrimPrefix(arg, "--cc=")
				continue
			}
			if strings.HasPrefix(arg, "-run=") {
				opts.TestFilter = strings.TrimPrefix(arg, "-run=")
				continue
			}
			if strings.HasPrefix(arg, "--run=") {
				opts.TestFilter = strings.TrimPrefix(arg, "--run=")
				continue
			}
			if strings.HasPrefix(arg, "--target=") {
				opts.TargetTriple = strings.TrimPrefix(arg, "--target=")
				continue
			}
			if strings.HasPrefix(arg, "--package=") {
				opts.Package = strings.TrimPrefix(arg, "--package=")
				continue
			}
			if strings.HasPrefix(arg, "-p=") {
				opts.Package = strings.TrimPrefix(arg, "-p=")
				continue
			}
			if len(arg) > 0 && arg[0] == '-' {
				return opts, fmt.Errorf("unknown flag %q", arg)
			}
			targets = append(targets, arg)
		}
	}

	if len(targets) == 0 && opts.Package == "" && !opts.Workspace {
		targets = []string{"."}
	}
	opts.Targets = targets
	if len(targets) == 1 {
		opts.EntryPath = targets[0]
	}
	return opts, nil
}

func runLSP(args []string) int {
	var logW io.Writer
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--stdio":
			// Default transport; accepted for compatibility with VS Code / LSP clients
		case "--log":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: missing file path for --log")
				return 1
			}
			i++
			f, err := os.OpenFile(args[i], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error opening LSP log %s: %v\n", args[i], err)
				return 1
			}
			defer f.Close()
			logW = f
		default:
			fmt.Fprintf(os.Stderr, "error: unknown `lowlight lsp` flag %q\n", args[i])
			return 1
		}
	}
	srv := lsp.NewServer(os.Stdin, os.Stdout, logW)
	return srv.Serve()
}

func printUsage(w *os.File) {
	// Keep filepath referenced if needed in future expansions.
	_ = filepath.Separator
	fmt.Fprint(w, `Lowlight v1.0 Compiler & Package Manager

Usage:
  lowlight build     [-o <bin>] [-p <pkg>|--workspace] [--locked] [--offline] [--backend <llvm|c11>] [--target <triple>] [--release] [--cc <compiler>] [<path>]
  lowlight run       [-p <pkg>] [--locked] [--offline] [--backend <llvm|c11>] [--target <triple>] [--release] [--cc <compiler>] [<path>] [-- <args...>]
  lowlight test      [-p <pkg>|--workspace] [--locked] [--offline] [-run <regex>] [--backend <llvm|c11>] [--target <triple>] [--release] [--cc <compiler>] [<path...>]
  lowlight check     [-p <pkg>|--workspace] [--locked] [--offline] [--target <triple>] [<path>]
  lowlight emit-llvm [-o <file.ll>] [-p <pkg>] [--locked] [--offline] [--target <triple>] [--release] [<path>]
  lowlight emit-c    [-o <file.c>]  [-p <pkg>] [--locked] [--offline] [--target <triple>] [--release] [<path>]

Package Management:
  lowlight init      [--lib|--bin] [--name <name>] [<dir>]
  lowlight new       <dir> [--lib|--bin] [--name <name>]
  lowlight add       <name> (--path <dir> | --url <url> [--hash <sha256>] | --index <url> --version <constraint>) [--dev] [--override] [--offline]
  lowlight remove    <name> [--dev] [--override] [--offline]
  lowlight fetch     [--locked] [--offline] [--target <triple>]
  lowlight update    [<pkg>...] [--offline] [--target <triple>]
  lowlight vendor    [--offline] [--target <triple>]
  lowlight tree      [--locked] [--offline] [--target <triple>]

Tooling:
  lowlight fmt       [-w] [--check] [--indent tabs|spaces] [--tab-width N] [--line-width N] [<path...>]
  lowlight doc       [-o <dir>] [--format html|md] [--check-links] [<path...>]
  lowlight lsp       [--stdio] [--log <file>]
  lowlight version
  lowlight help

Flags:
  -o <path>         Output executable, LLVM IR, or C11 source path
  -p, --package <p> Select a workspace member package
  --workspace       Operate across all workspace member packages
  --locked          Require lowlight.lock to match lowlight.toml without modification
  --offline         Disallow network requests (use local cache and/or ./vendor/)
  --backend <kind>  Codegen backend: "llvm" (default) or "c11" (ISO C11 backend)
  --target <triple> Target triple for cross-compilation (e.g. "aarch64-unknown-linux-gnu")
  --release         Optimize (-O2) and elide runtime bounds/shift checks
  --cc <compiler>   C/LLVM compiler executable (default: $LOWLIGHT_CC, $CC, or clang/cc)
  --stdio           Run Language Server Protocol (LSP 3.17) over stdin/stdout
  --log <file>      Append JSON-RPC LSP trace logs to <file>
`)
}
