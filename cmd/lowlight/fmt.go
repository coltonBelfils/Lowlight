package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lowlight/internal/format"
)

func runFmt(args []string) int {
	writeBack := false
	checkOnly := false
	var indentOverride string
	tabWidthOverride := 0
	lineWidthOverride := -1
	var paths []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-w", "--write":
			writeBack = true
		case "--check", "-l", "--list":
			checkOnly = true
		case "--indent":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --indent expects `tabs` or `spaces`")
				return 1
			}
			i++
			indentOverride = args[i]
		case "--tab-width":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --tab-width expects a positive integer")
				return 1
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "error: invalid --tab-width %q\n", args[i])
				return 1
			}
			tabWidthOverride = n
		case "--line-width":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --line-width expects an integer (0 disables wrapping)")
				return 1
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintf(os.Stderr, "error: invalid --line-width %q\n", args[i])
				return 1
			}
			lineWidthOverride = n
		default:
			if strings.HasPrefix(arg, "-") && arg != "-" {
				fmt.Fprintf(os.Stderr, "error: unknown fmt flag %q\n", arg)
				return 1
			}
			paths = append(paths, arg)
		}
	}

	applyOverrides := func(opts format.Options) format.Options {
		if indentOverride != "" {
			opts.IndentStyle = indentOverride
		}
		if tabWidthOverride > 0 {
			opts.TabWidth = tabWidthOverride
		}
		if lineWidthOverride >= 0 {
			opts.LineWidth = lineWidthOverride
		}
		return opts.Normalized()
	}

	if len(paths) == 1 && paths[0] == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
			return 1
		}
		cwd, _ := os.Getwd()
		opts := applyOverrides(format.LoadConfig(cwd))
		out, err := format.Format("<stdin>", string(raw), opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if checkOnly {
			if out != string(raw) {
				fmt.Fprintln(os.Stdout, "<stdin>")
				return 1
			}
			return 0
		}
		fmt.Fprint(os.Stdout, out)
		return 0
	}

	if len(paths) == 0 {
		paths = []string{"."}
	}

	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		err = filepath.WalkDir(p, func(fp string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				base := d.Name()
				if strings.HasPrefix(base, ".") && base != "." {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), ".ll") {
				files = append(files, fp)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error walking %s: %v\n", p, err)
			return 1
		}
	}

	unformatted := 0
	hadErr := false
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			hadErr = true
			continue
		}
		opts := applyOverrides(format.LoadConfig(filepath.Dir(file)))
		out, err := format.Format(file, string(raw), opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			hadErr = true
			continue
		}
		changed := out != string(raw)
		if checkOnly {
			if changed {
				fmt.Fprintln(os.Stdout, file)
				unformatted++
			}
			continue
		}
		if writeBack {
			if changed {
				if err := os.WriteFile(file, []byte(out), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "error writing %s: %v\n", file, err)
					hadErr = true
				}
			}
			continue
		}
		fmt.Fprint(os.Stdout, out)
	}

	if hadErr || (checkOnly && unformatted > 0) {
		return 1
	}
	return 0
}
