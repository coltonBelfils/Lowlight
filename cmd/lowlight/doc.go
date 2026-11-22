package main

import (
	"fmt"
	"os"
	"strings"

	"lowlight/internal/docgen"
	"lowlight/internal/site"
)

func runDoc(args []string) int {
	opts := docgen.Options{
		OutputDir: "docs",
		Format:    "html",
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-o", "--out", "--output":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: -o expects an output directory path")
				return 1
			}
			i++
			opts.OutputDir = args[i]
		case "--format":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --format expects \"html\" or \"md\"")
				return 1
			}
			i++
			opts.Format = args[i]
		case "--check-links":
			opts.CheckLinks = true
		case "--project-root":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --project-root expects a directory path")
				return 1
			}
			i++
			opts.ProjectRoot = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "error: unknown doc flag %q\n", arg)
				return 1
			}
			opts.Paths = append(opts.Paths, arg)
		}
	}

	if opts.CheckLinks && !hasOutFlag(args) {
		// Allow `--check-links` to verify links cleanly without polluting workspace unless `-o` was passed.
		opts.OutputDir = ""
	}

	// Markdown output and link checking are handled by docgen; HTML by the shared site builder.
	format := strings.ToLower(opts.Format)
	htmlOut := ""
	if (format == "" || format == "html") && opts.OutputDir != "" {
		htmlOut = opts.OutputDir
		opts.OutputDir = ""
	}
	res, err := docgen.Generate(opts)
	if res != nil {
		for _, bl := range res.BrokenLinks {
			fmt.Fprintln(os.Stderr, bl.String())
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if htmlOut != "" {
		ld, lerr := docgen.Load(opts.Paths, opts.ProjectRoot)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", lerr)
			return 1
		}
		s, probs, berr := site.BuildAPIDocs(site.APIDocsConfig{Loaded: ld, OutDir: htmlOut})
		if berr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", berr)
			return 1
		}
		for _, p := range probs {
			fmt.Fprintln(os.Stderr, "warning:", p)
		}
		pkgs := 0
		for _, p := range s.Pages {
			if p.Layout == "api" {
				pkgs++
			}
		}
		fmt.Fprintf(os.Stdout, "Generated documentation for %d package(s) (%d symbols) in %s\n", pkgs, res.SymbolsCount, htmlOut)
		return 0
	}
	if opts.OutputDir != "" {
		fmt.Fprintf(os.Stdout, "Generated %s documentation for %d module(s) (%d symbols) in %s\n",
			opts.Format, res.ModulesCount, res.SymbolsCount, opts.OutputDir)
	} else {
		fmt.Fprintf(os.Stdout, "Verified doc links across %d module(s) (%d symbols): all links valid\n",
			res.ModulesCount, res.SymbolsCount)
	}
	return 0
}

func hasOutFlag(args []string) bool {
	for _, a := range args {
		if a == "-o" || a == "--out" || a == "--output" {
			return true
		}
	}
	return false
}
