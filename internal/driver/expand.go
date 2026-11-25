package driver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lowlight/internal/diag"
	"lowlight/internal/format"
	"lowlight/internal/manifest"
	"lowlight/internal/sema"
)

// CanonicalTargets lists the representative cross-platform target triples checked
// by `lowlight check --all-targets` so platform branches under `comptime if(comp.TARGET...)`
// are verified without requiring every target OS to execute them.
var CanonicalTargets = []string{
	"aarch64-apple-darwin",
	"x86_64-unknown-linux-gnu",
	"aarch64-unknown-linux-gnu",
	"x86_64-pc-windows-msvc",
	"wasm32-unknown-unknown",
}

// Expand runs semantic checking and comptime evaluation on opts, expands the entry
// module's compile-time constants/control flow, and returns formatted Lowlight source.
func Expand(opts Options) (string, string, error) {
	opts, _, err := ResolveOptions(opts)
	if err != nil {
		return "", "", err
	}
	checked, warnings, err := Check(opts)
	if err != nil {
		return "", warnings, err
	}
	if checked == nil || checked.EntryMod == nil {
		return "", warnings, errors.New("cannot expand: missing entry module")
	}
	expandedFile := sema.ExpandModuleAST(checked, checked.EntryMod)
	if expandedFile == nil {
		return "", warnings, errors.New("cannot expand entry module")
	}
	origSrc := opts.SrcOverride
	if origSrc == "" && checked.EntryMod.Path != "" {
		if raw, readErr := os.ReadFile(checked.EntryMod.Path); readErr == nil {
			origSrc = string(raw)
		}
	}
	fmtOpts := format.DefaultOptions()
	if checked.EntryMod.Dir != "" {
		fmtOpts = format.LoadConfig(checked.EntryMod.Dir)
	} else if checked.EntryMod.Path != "" {
		fmtOpts = format.LoadConfig(filepath.Dir(checked.EntryMod.Path))
	}
	return format.FormatAST(origSrc, expandedFile, nil, fmtOpts), warnings, nil
}

// CheckAllTargets runs semantic checking across CanonicalTargets, merging and deduplicating
// diagnostics cleanly while labeling diagnostics that only occur on specific targets.
func CheckAllTargets(opts Options) (string, error) {
	var ctx *manifest.ProjectContext
	var err error
	opts, ctx, err = ResolveOptions(opts)
	if err != nil {
		return "", err
	}
	if opts.EntryPath == "" {
		return "", errors.New("missing input .ll source file or package directory")
	}

	type diagBucket struct {
		rendered string
		severity diag.Severity
		targets  []string
	}
	var order []string
	buckets := make(map[string]*diagBucket)
	var hasError bool

	for _, triple := range CanonicalTargets {
		rep := diag.NewReporter()
		sema.CheckWithOptions(sema.CheckOptions{
			EntryPath:    opts.EntryPath,
			SrcOverride:  opts.SrcOverride,
			ProjectRoot:  opts.ProjectRoot,
			TargetTriple: triple,
			TestMode:     opts.TestMode,
			ManifestCtx:  ctx,
			CacheRoot:    opts.CacheRoot,
			Locked:       opts.Locked,
			Offline:      opts.Offline,
		}, rep)
		if rep.HasErrors() {
			hasError = true
		}
		for _, d := range rep.Diagnostics() {
			text := rep.FormatOne(d)
			b, exists := buckets[text]
			if !exists {
				b = &diagBucket{rendered: text, severity: d.Severity}
				buckets[text] = b
				order = append(order, text)
			}
			b.targets = append(b.targets, triple)
		}
	}

	var warnBuf strings.Builder
	var errBuf strings.Builder
	totalTargets := len(CanonicalTargets)

	for _, key := range order {
		b := buckets[key]
		dst := &warnBuf
		if b.severity == diag.Error {
			dst = &errBuf
		}
		if dst.Len() > 0 {
			dst.WriteByte('\n')
		}
		if len(b.targets) < totalTargets {
			fmt.Fprintf(dst, "[targets: %s]\n", strings.Join(b.targets, ", "))
		}
		dst.WriteString(b.rendered)
	}

	warnings := warnBuf.String()
	if hasError {
		return warnings, &CompileError{Formatted: errBuf.String()}
	}
	return warnings, nil
}
