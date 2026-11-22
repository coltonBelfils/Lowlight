// Command lowlight-site builds the Lowlight website: guides, tutorials, the tour,
// Lowlight by Example, the specification, standard library reference, and internals.
//
// Usage:
//
//	go run ./cmd/lowlight-site [-o docs] [-root .] [-update-examples] [-strict]
//
// -update-examples runs every program in website/examples and rewrites its .out file.
// -strict exits with an error when any internal link is broken.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"lowlight/internal/site"
)

func main() {
	out := flag.String("o", "docs", "output directory")
	root := flag.String("root", ".", "repository root")
	update := flag.Bool("update-examples", false, "run website/examples and rewrite their .out files")
	strict := flag.Bool("strict", false, "fail on broken internal links")
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fatal(err)
	}
	if *update {
		n, err := site.UpdateExampleOutputs(absRoot)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Updated output of %d examples\n", n)
	}
	s, probs, err := site.BuildWebsite(site.WebsiteConfig{Root: absRoot, OutDir: *out})
	if err != nil {
		fatal(err)
	}
	for _, p := range probs {
		fmt.Fprintln(os.Stderr, "warning:", p)
	}
	fmt.Printf("Built %d pages into %s\n", len(s.Pages), *out)
	if *strict && len(probs) > 0 {
		fatal(fmt.Errorf("%d broken internal link(s)", len(probs)))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
