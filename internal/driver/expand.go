package driver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lowlight/internal/ast"
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
// module's (or multi-file package's) compile-time constants/control flow/`@derive`
// methods, and returns formatted Lowlight source.
func Expand(opts Options) (string, string, error) {
	opts, ctx, err := ResolveOptions(opts)
	if err != nil {
		return "", "", err
	}
	if opts.Workspace && opts.SrcOverride == "" && ctx != nil && len(ctx.WorkspaceMembers) > 0 {
		members := sortedWorkspaceMembers(ctx)
		var allOut strings.Builder
		var allWarns strings.Builder
		for i, member := range members {
			mOpts := opts
			mOpts.Workspace = false
			mOpts.Package = ""
			mOpts.EntryPath = member.Dir
			expanded, warns, err := Expand(mOpts)
			allWarns.WriteString(warns)
			if err != nil {
				return "", allWarns.String(), err
			}
			if i > 0 {
				allOut.WriteByte('\n')
			}
			if len(members) > 1 {
				pkgLabel := member.Dir
				if member.Package.Name != "" {
					pkgLabel = member.Package.Name
				}
				fmt.Fprintf(&allOut, "// === package %s ===\n", pkgLabel)
			}
			allOut.WriteString(expanded)
		}
		return allOut.String(), allWarns.String(), nil
	}
	checked, warnings, err := Check(opts)
	if err != nil {
		return "", warnings, err
	}
	if checked == nil || checked.EntryMod == nil {
		return "", warnings, errors.New("cannot expand: missing entry module")
	}

	// Collect target modules: if expanding a package directory (`-p` or dir) with multiple
	// non-test modules, emit all non-test modules; otherwise expand just EntryMod.
	entryIsDir := opts.Package != ""
	if !entryIsDir && opts.SrcOverride == "" && opts.EntryPath != "" {
		if st, statErr := os.Stat(opts.EntryPath); statErr == nil && st.IsDir() {
			entryIsDir = true
		}
	}
	var mods []*sema.Module
	if entryIsDir && opts.SrcOverride == "" && checked.EntryPkg != nil {
		for _, m := range checked.EntryPkg.Files {
			if m != nil && !m.IsTestFile {
				mods = append(mods, m)
			}
		}
	}
	if len(mods) == 0 {
		mods = []*sema.Module{checked.EntryMod}
	}

	var out strings.Builder
	for i, m := range mods {
		expandedFile := sema.ExpandModuleAST(checked, m)
		if expandedFile == nil {
			return "", warnings, errors.New("cannot expand module")
		}
		origSrc := ""
		if m == checked.EntryMod && opts.SrcOverride != "" {
			origSrc = opts.SrcOverride
		} else if m.Path != "" {
			if raw, readErr := os.ReadFile(m.Path); readErr == nil {
				origSrc = string(raw)
			}
		}
		formatted := FormatExpandedAST(m, origSrc, expandedFile)
		if len(mods) > 1 {
			if i > 0 {
				out.WriteByte('\n')
			}
			fmt.Fprintf(&out, "// === %s ===\n", filepath.Base(m.Path))
		}
		out.WriteString(formatted)
	}
	return out.String(), warnings, nil
}

// FormatExpandedAST formats an expanded AST file using the module's `.lowlightfmt` config.
func FormatExpandedAST(mod *sema.Module, origSrc string, expandedFile *ast.File) string {
	fmtOpts := format.DefaultOptions()
	if mod != nil {
		if mod.Dir != "" {
			fmtOpts = format.LoadConfig(mod.Dir)
		} else if mod.Path != "" {
			fmtOpts = format.LoadConfig(filepath.Dir(mod.Path))
		}
	}
	return format.FormatAST(origSrc, expandedFile, nil, fmtOpts)
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
		seen     map[string]bool
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
				b = &diagBucket{rendered: text, severity: d.Severity, seen: make(map[string]bool)}
				buckets[text] = b
				order = append(order, text)
			}
			if !b.seen[triple] {
				b.seen[triple] = true
				b.targets = append(b.targets, triple)
			}
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
