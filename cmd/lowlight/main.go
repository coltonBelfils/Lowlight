// Command lowlight is the Lowlight v1.0 compiler CLI.
package main

import (
	"fmt"
	"io"
	"os"

	"lowlight/internal/driver"
	"lowlight/internal/lsp"
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
		fmt.Printf("lowlight %s (v1.0 specification, ISO C11 backend)\n", driver.Version)
		return 0

	case "check":
		opts, _, err := parseCommonFlags(rest, false, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		_, warnings, err := driver.Check(opts)
		if err != nil {
			fmt.Fprint(os.Stderr, err.Error())
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

	case "build":
		opts, _, err := parseCommonFlags(rest, true, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
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

	case "lsp":
		return runLSP(rest)

	default:
		fmt.Fprintf(os.Stderr, "error: unknown subcommand %q\n\n", sub)
		printUsage(os.Stderr)
		return 1
	}
}

func parseCommonFlags(args []string, allowOut bool, allowProgArgs bool) (driver.Options, []string, error) {
	var opts driver.Options
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
		case "--cc":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing argument for --cc")
			}
			i++
			opts.CC = args[i]
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
			if len(arg) > 0 && arg[0] == '-' {
				return opts, nil, fmt.Errorf("unknown flag %q", arg)
			}
			if opts.EntryPath == "" {
				opts.EntryPath = arg
			} else if allowProgArgs {
				// Any positional args after <file.ll> in `lowlight run` are forwarded to the program
				progArgs = append(progArgs, args[i:]...)
				i = len(args)
			} else {
				return opts, nil, fmt.Errorf("unexpected extra argument %q", arg)
			}
		}
	}

	if opts.EntryPath == "" {
		return opts, nil, fmt.Errorf("missing <file.ll> path")
	}
	return opts, progArgs, nil
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
	fmt.Fprint(w, `Lowlight v1.0 Compiler

Usage:
  lowlight build [-o <bin>] [--release] [--cc <compiler>] <file.ll>
  lowlight run   [--release] [--cc <compiler>] <file.ll> [-- <args...>]
  lowlight emit-c [-o <file.c>] [--release] <file.ll>
  lowlight check <file.ll>
  lowlight lsp   [--stdio] [--log <file>]
  lowlight version
  lowlight help

Flags:
  -o <path>        Output executable or C11 source path
  --release        Optimize generated C (-O2) and elide runtime bounds/shift checks
  --cc <compiler>  C11 compiler executable (default: $LOWLIGHT_CC, $CC, or cc)
  --stdio          Run Language Server Protocol (LSP 3.17) over stdin/stdout
  --log <file>     Append JSON-RPC LSP trace logs to <file>
`)
}
