package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/manifest"
)

func runInit(args []string, stdout, stderr io.Writer) int {
	isLib := false
	pkgName := ""
	targetDir := "."

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--lib":
			isLib = true
		case "--bin":
			isLib = false
		case "--name":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --name")
				return 1
			}
			i++
			pkgName = args[i]
		default:
			if strings.HasPrefix(arg, "--name=") {
				pkgName = strings.TrimPrefix(arg, "--name=")
				continue
			}
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "error: unknown init flag %q\n", arg)
				return 1
			}
			targetDir = arg
		}
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if pkgName == "" {
		pkgName = sanitizeStarterName(filepath.Base(absDir))
	}
	return scaffoldProject(absDir, pkgName, isLib, false, stdout, stderr)
}

func runNew(args []string, stdout, stderr io.Writer) int {
	isLib := false
	pkgName := ""
	targetDir := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--lib":
			isLib = true
		case "--bin":
			isLib = false
		case "--name":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --name")
				return 1
			}
			i++
			pkgName = args[i]
		default:
			if strings.HasPrefix(arg, "--name=") {
				pkgName = strings.TrimPrefix(arg, "--name=")
				continue
			}
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "error: unknown new flag %q\n", arg)
				return 1
			}
			if targetDir == "" {
				targetDir = arg
			} else {
				fmt.Fprintf(stderr, "error: unexpected extra argument %q\n", arg)
				return 1
			}
		}
	}

	if targetDir == "" {
		fmt.Fprintln(stderr, "error: usage: lowlight new <dir> [--lib|--bin] [--name <name>]")
		return 1
	}
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if pkgName == "" {
		pkgName = sanitizeStarterName(filepath.Base(absDir))
	}
	return scaffoldProject(absDir, pkgName, isLib, true, stdout, stderr)
}

func sanitizeStarterName(base string) string {
	var b strings.Builder
	for i := 0; i < len(base); i++ {
		c := base[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	res := b.String()
	if res == "" || (res[0] >= '0' && res[0] <= '9') || res[0] == '-' {
		res = "app_" + res
	}
	return res
}

func scaffoldProject(absDir, pkgName string, isLib, createNewDir bool, stdout, stderr io.Writer) int {
	if !manifest.IsValidPackageName(pkgName) {
		fmt.Fprintf(stderr, "error: invalid package name %q (must start with letter or '_' and contain only letters, digits, '_', or '-')\n", pkgName)
		return 1
	}
	if createNewDir {
		if entries, err := os.ReadDir(absDir); err == nil && len(entries) > 0 {
			fmt.Fprintf(stderr, "error: directory %q already exists and is not empty\n", absDir)
			return 1
		}
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "error: cannot create directory %q: %v\n", absDir, err)
		return 1
	}

	manifestPath := filepath.Join(absDir, manifest.ManifestFilename)
	if _, err := os.Stat(manifestPath); err == nil {
		fmt.Fprintf(stderr, "error: %s already exists\n", manifestPath)
		return 1
	}

	manifestContent := fmt.Sprintf(`[package]
name = %s
version = "0.1.0"
edition = "2026"

[dependencies]
`, manifest.FormatTOMLString(pkgName))

	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		fmt.Fprintf(stderr, "error: writing %s: %v\n", manifestPath, err)
		return 1
	}

	qualName := manifest.NormalizeQualifier(pkgName)
	var srcFile, srcContent, testFile, testContent string
	if isLib {
		srcFile = filepath.Join(absDir, qualName+".ll")
		srcContent = fmt.Sprintf("// Package %s.\n\nbind greet = (name: string) string: {\n\t\"Hello, ${name}!\"\n}\n", pkgName)
		testFile = filepath.Join(absDir, qualName+"_test.ll")
		testContent = "import \"std/test\"\n\n@test(\"greet formats message\")\nbind test_greet = (): {\n\ttest.assert_eq(greet(\"Lowlight\"), \"Hello, Lowlight!\")\n}\n"
	} else {
		srcFile = filepath.Join(absDir, "main.ll")
		srcContent = "bind greet = (name: string) string: {\n\t\"Hello, ${name}!\"\n}\n\nbind main = (): {\n\tprintln(greet(\"Lowlight\"))\n}\n"
		testFile = filepath.Join(absDir, qualName+"_test.ll")
		testContent = "import \"std/test\"\n\n@test(\"greet formats message\")\nbind test_greet = (): {\n\ttest.assert_eq(greet(\"world\"), \"Hello, world!\")\n}\n"
	}

	if _, err := os.Stat(srcFile); os.IsNotExist(err) {
		if err := os.WriteFile(srcFile, []byte(srcContent), 0o644); err != nil {
			fmt.Fprintf(stderr, "error: writing %s: %v\n", srcFile, err)
			return 1
		}
	}
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		if err := os.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
			fmt.Fprintf(stderr, "error: writing %s: %v\n", testFile, err)
			return 1
		}
	}

	kind := "binary"
	if isLib {
		kind = "library"
	}
	fmt.Fprintf(stdout, "Initialized Lowlight %s package %q in %s\n", kind, pkgName, absDir)
	return 0
}

func runAdd(args []string, stdout, stderr io.Writer) int {
	var depName, depPath, depURL, depHash, depIndex, depVersion string
	isDev := false
	isOverride := false
	offline := false
	cacheRoot := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--path":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --path")
				return 1
			}
			i++
			depPath = args[i]
		case "--url":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --url")
				return 1
			}
			i++
			depURL = args[i]
		case "--hash":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --hash")
				return 1
			}
			i++
			depHash = args[i]
		case "--index":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --index")
				return 1
			}
			i++
			depIndex = args[i]
		case "--version":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --version")
				return 1
			}
			i++
			depVersion = args[i]
		case "--dev":
			isDev = true
		case "--override":
			isOverride = true
		case "--offline":
			offline = true
		case "--cache":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --cache")
				return 1
			}
			i++
			cacheRoot = args[i]
		default:
			if strings.HasPrefix(arg, "--path=") {
				depPath = strings.TrimPrefix(arg, "--path=")
				continue
			}
			if strings.HasPrefix(arg, "--url=") {
				depURL = strings.TrimPrefix(arg, "--url=")
				continue
			}
			if strings.HasPrefix(arg, "--hash=") {
				depHash = strings.TrimPrefix(arg, "--hash=")
				continue
			}
			if strings.HasPrefix(arg, "--index=") {
				depIndex = strings.TrimPrefix(arg, "--index=")
				continue
			}
			if strings.HasPrefix(arg, "--version=") {
				depVersion = strings.TrimPrefix(arg, "--version=")
				continue
			}
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "error: unknown add flag %q\n", arg)
				return 1
			}
			if depName == "" {
				depName = arg
			} else {
				fmt.Fprintf(stderr, "error: unexpected extra argument %q\n", arg)
				return 1
			}
		}
	}

	if depName == "" {
		fmt.Fprintln(stderr, "error: usage: lowlight add <name> (--path <dir> | --url <url> [--hash <sha256>] | --index <url> --version <constraint>) [--dev] [--override]")
		return 1
	}
	if !manifest.IsValidPackageName(depName) {
		fmt.Fprintf(stderr, "error: invalid dependency name %q\n", depName)
		return 1
	}
	if isDev && isOverride {
		fmt.Fprintln(stderr, "error: --dev and --override cannot be combined")
		return 1
	}

	modes := 0
	if depPath != "" {
		modes++
	}
	if depURL != "" {
		modes++
	}
	if depIndex != "" {
		modes++
	}
	if modes != 1 {
		fmt.Fprintln(stderr, "error: specify exactly one source: --path <dir>, --url <url>, or --index <url> --version <constraint>")
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, err := manifest.DiscoverProject(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if ctx == nil {
		fmt.Fprintln(stderr, "error: no lowlight.toml found (run `lowlight init` first)")
		return 1
	}

	targetManifest := ctx.ActiveManifest
	section := "dependencies"
	if isOverride {
		section = "overrides"
		if ctx.WorkspaceManifest != nil {
			targetManifest = ctx.WorkspaceManifest
		}
	} else if isDev {
		section = "dev-dependencies"
	}
	if targetManifest == nil {
		targetManifest = ctx.WorkspaceManifest
	}

	fetcher := manifest.NewFetcher(cacheRoot)

	var valueExpr string
	switch {
	case depPath != "":
		valueExpr = fmt.Sprintf("{ path = %s }", manifest.FormatTOMLString(depPath))

	case depURL != "":
		if depHash == "" {
			if offline {
				fmt.Fprintln(stderr, "error: --hash is required with --url when --offline is set")
				return 1
			}
			raw, fetchErr := fetcher.FetchBytes(depURL)
			if fetchErr != nil {
				fmt.Fprintf(stderr, "error: failed to fetch %s to compute SHA-256: %v\n", depURL, fetchErr)
				return 1
			}
			depHash = manifest.ComputeSHA256(raw)
		} else if !manifest.IsValidSHA256Hash(depHash) {
			fmt.Fprintf(stderr, "error: invalid --hash %q (expected sha256:<64-hex>)\n", depHash)
			return 1
		}
		valueExpr = fmt.Sprintf("{ url = %s, hash = %s }", manifest.FormatTOMLString(depURL), manifest.FormatTOMLString(depHash))

	case depIndex != "":
		if isOverride {
			fmt.Fprintln(stderr, "error: [overrides] only supports --path or --url")
			return 1
		}
		if depVersion == "" {
			depVersion = "*"
		}
		if _, parseErr := manifest.ParseVersionConstraint(depVersion); parseErr != nil {
			fmt.Fprintf(stderr, "error: invalid --version constraint %q: %v\n", depVersion, parseErr)
			return 1
		}
		valueExpr = fmt.Sprintf("{ index = %s, version = %s }", manifest.FormatTOMLString(depIndex), manifest.FormatTOMLString(depVersion))
	}

	origRaw, err := os.ReadFile(targetManifest.Path)
	if err != nil {
		fmt.Fprintf(stderr, "error: reading %s: %v\n", targetManifest.Path, err)
		return 1
	}
	updatedRaw := manifest.UpsertTableEntry(string(origRaw), section, depName, valueExpr)
	if err := os.WriteFile(targetManifest.Path, []byte(updatedRaw), 0o644); err != nil {
		fmt.Fprintf(stderr, "error: writing %s: %v\n", targetManifest.Path, err)
		return 1
	}

	// Re-discover and sync lowlight.lock.
	ctx2, err := manifest.DiscoverProject(targetManifest.Dir)
	if err != nil {
		// Roll back invalid edit cleanly.
		_ = os.WriteFile(targetManifest.Path, origRaw, 0o644)
		fmt.Fprintf(stderr, "error: resulting lowlight.toml is invalid: %v\n", err)
		return 1
	}
	if _, err := manifest.SyncLockfile(ctx2, manifest.ResolveOptions{
		IncludeDev:   true,
		AllowNetwork: !offline,
		Offline:      offline,
		CacheRoot:    cacheRoot,
		Fetcher:      fetcher,
	}); err != nil {
		fmt.Fprintf(stderr, "warning: updated %s but lockfile sync failed: %v\n", targetManifest.Path, err)
		return 1
	}

	fmt.Fprintf(stdout, "Added %s to [%s] in %s\n", depName, section, targetManifest.Path)
	return 0
}

func runRemove(args []string, stdout, stderr io.Writer) int {
	var depName string
	isDev := false
	isOverride := false
	offline := false
	cacheRoot := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--dev":
			isDev = true
		case "--override":
			isOverride = true
		case "--offline":
			offline = true
		case "--cache":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --cache")
				return 1
			}
			i++
			cacheRoot = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "error: unknown remove flag %q\n", arg)
				return 1
			}
			if depName == "" {
				depName = arg
			} else {
				fmt.Fprintf(stderr, "error: unexpected extra argument %q\n", arg)
				return 1
			}
		}
	}
	if depName == "" {
		fmt.Fprintln(stderr, "error: usage: lowlight remove <name> [--dev] [--override]")
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, err := manifest.DiscoverProject(cwd)
	if err != nil || ctx == nil {
		fmt.Fprintf(stderr, "error: no lowlight.toml found\n")
		return 1
	}
	targetManifest := ctx.ActiveManifest
	section := "dependencies"
	if isOverride {
		section = "overrides"
		if ctx.WorkspaceManifest != nil {
			targetManifest = ctx.WorkspaceManifest
		}
	} else if isDev {
		section = "dev-dependencies"
	}
	if targetManifest == nil {
		targetManifest = ctx.WorkspaceManifest
	}

	origRaw, err := os.ReadFile(targetManifest.Path)
	if err != nil {
		fmt.Fprintf(stderr, "error: reading %s: %v\n", targetManifest.Path, err)
		return 1
	}
	updatedRaw, removed := manifest.RemoveTableEntry(string(origRaw), section, depName)
	if !removed {
		fmt.Fprintf(stderr, "error: %q not found in [%s] of %s\n", depName, section, targetManifest.Path)
		return 1
	}
	if err := os.WriteFile(targetManifest.Path, []byte(updatedRaw), 0o644); err != nil {
		fmt.Fprintf(stderr, "error: writing %s: %v\n", targetManifest.Path, err)
		return 1
	}

	if ctx2, err := manifest.DiscoverProject(targetManifest.Dir); err == nil && ctx2 != nil {
		_, _ = manifest.SyncLockfile(ctx2, manifest.ResolveOptions{
			IncludeDev:   true,
			AllowNetwork: !offline,
			Offline:      offline,
			CacheRoot:    cacheRoot,
		})
	}
	fmt.Fprintf(stdout, "Removed %s from [%s] in %s\n", depName, section, targetManifest.Path)
	return 0
}

func runFetch(args []string, stdout, stderr io.Writer) int {
	offline := false
	locked := false
	targetTriple := ""
	cacheRoot := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--offline":
			offline = true
		case "--locked":
			locked = true
		case "--target":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --target")
				return 1
			}
			i++
			targetTriple = args[i]
		case "--cache":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --cache")
				return 1
			}
			i++
			cacheRoot = args[i]
		default:
			if strings.HasPrefix(arg, "--target=") {
				targetTriple = strings.TrimPrefix(arg, "--target=")
				continue
			}
			fmt.Fprintf(stderr, "error: unknown fetch flag %q\n", arg)
			return 1
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, err := manifest.DiscoverProject(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if ctx == nil {
		fmt.Fprintln(stderr, "error: no lowlight.toml found")
		return 1
	}

	graph, err := manifest.SyncLockfile(ctx, manifest.ResolveOptions{
		Target:       manifest.ParseTarget(targetTriple),
		IncludeDev:   true,
		AllowNetwork: !offline,
		Offline:      offline,
		Locked:       locked,
		CacheRoot:    cacheRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: fetch failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Fetched and verified %d dependency package(s) (%s)\n", len(graph.Packages), ctx.LockPath())
	return 0
}

func runUpdate(args []string, stdout, stderr io.Writer) int {
	offline := false
	targetTriple := ""
	cacheRoot := ""
	var pkgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--offline":
			offline = true
		case "--target":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --target")
				return 1
			}
			i++
			targetTriple = args[i]
		case "--cache":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --cache")
				return 1
			}
			i++
			cacheRoot = args[i]
		default:
			if strings.HasPrefix(arg, "--target=") {
				targetTriple = strings.TrimPrefix(arg, "--target=")
				continue
			}
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "error: unknown update flag %q\n", arg)
				return 1
			}
			pkgs = append(pkgs, arg)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, err := manifest.DiscoverProject(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if ctx == nil {
		fmt.Fprintln(stderr, "error: no lowlight.toml found")
		return 1
	}

	graph, err := manifest.SyncLockfile(ctx, manifest.ResolveOptions{
		Target:       manifest.ParseTarget(targetTriple),
		IncludeDev:   true,
		AllowNetwork: !offline,
		Offline:      offline,
		UpdateAll:    len(pkgs) == 0,
		UpdatePkgs:   pkgs,
		CacheRoot:    cacheRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: update failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Updated %s (%d dependency package(s))\n", ctx.LockPath(), len(graph.Packages))
	return 0
}

func runVendor(args []string, stdout, stderr io.Writer) int {
	offline := false
	targetTriple := ""
	cacheRoot := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--offline":
			offline = true
		case "--target":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --target")
				return 1
			}
			i++
			targetTriple = args[i]
		case "--cache":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --cache")
				return 1
			}
			i++
			cacheRoot = args[i]
		default:
			if strings.HasPrefix(arg, "--target=") {
				targetTriple = strings.TrimPrefix(arg, "--target=")
				continue
			}
			fmt.Fprintf(stderr, "error: unknown vendor flag %q\n", arg)
			return 1
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, err := manifest.DiscoverProject(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if ctx == nil {
		fmt.Fprintln(stderr, "error: no lowlight.toml found")
		return 1
	}

	graph, err := manifest.VendorDependencies(ctx, manifest.ResolveOptions{
		Target:       manifest.ParseTarget(targetTriple),
		IncludeDev:   true,
		AllowNetwork: !offline,
		Offline:      offline,
		CacheRoot:    cacheRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: vendor failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Vendored %d dependency package(s) into %s\n", len(graph.Packages), ctx.VendorDir())
	return 0
}

func runTree(args []string, stdout, stderr io.Writer) int {
	offline := false
	locked := false
	targetTriple := ""
	cacheRoot := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--offline":
			offline = true
		case "--locked":
			locked = true
		case "--target":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --target")
				return 1
			}
			i++
			targetTriple = args[i]
		case "--cache":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: missing argument for --cache")
				return 1
			}
			i++
			cacheRoot = args[i]
		default:
			if strings.HasPrefix(arg, "--target=") {
				targetTriple = strings.TrimPrefix(arg, "--target=")
				continue
			}
			fmt.Fprintf(stderr, "error: unknown tree flag %q\n", arg)
			return 1
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, err := manifest.DiscoverProject(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if ctx == nil {
		fmt.Fprintln(stderr, "error: no lowlight.toml found")
		return 1
	}

	target := manifest.ParseTarget(targetTriple)
	graph, err := manifest.Resolve(ctx, manifest.ResolveOptions{
		Target:       target,
		IncludeDev:   true,
		AllowNetwork: false,
		Offline:      offline,
		Locked:       locked,
		CacheRoot:    cacheRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: dependency resolution failed: %v\n", err)
		return 1
	}

	var roots []*manifest.Manifest
	if ctx.ActiveManifest != nil && ctx.ActiveManifest.HasPackage {
		roots = []*manifest.Manifest{ctx.ActiveManifest}
	} else {
		roots = sortedMembers(ctx)
	}

	var printNode func(name, prefix string, isLast bool, visited map[string]bool)
	printNode = func(name, prefix string, isLast bool, visited map[string]bool) {
		branch := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			branch = "└── "
			childPrefix = prefix + "    "
		}
		rp := graph.Packages[name]
		if rp == nil {
			fmt.Fprintf(stdout, "%s%s%s (unresolved)\n", prefix, branch, name)
			return
		}
		var tags []string
		if rp.Version != "" {
			tags = append(tags, "v"+rp.Version)
		}
		if rp.Source != "" {
			tags = append(tags, rp.Source)
		}
		if rp.IsDevOnly {
			tags = append(tags, "dev")
		}
		suffix := ""
		if len(tags) > 0 {
			suffix = " (" + strings.Join(tags, ", ") + ")"
		}
		if visited[name] {
			fmt.Fprintf(stdout, "%s%s%s%s (*)\n", prefix, branch, name, suffix)
			return
		}
		fmt.Fprintf(stdout, "%s%s%s%s\n", prefix, branch, name, suffix)
		nextVisited := make(map[string]bool, len(visited)+1)
		for k, v := range visited {
			nextVisited[k] = v
		}
		nextVisited[name] = true
		deps := append([]string(nil), rp.Dependencies...)
		sort.Strings(deps)
		for idx, child := range deps {
			printNode(child, childPrefix, idx == len(deps)-1, nextVisited)
		}
	}

	for _, rm := range roots {
		ver := rm.Package.Version
		if ver == "" {
			ver = "0.1.0"
		}
		fmt.Fprintf(stdout, "%s v%s (%s)\n", rm.Package.Name, ver, rm.Dir)
		direct := make(map[string]bool)
		for k := range rm.Dependencies {
			direct[k] = true
		}
		for k := range rm.DevDependencies {
			direct[k] = true
		}
		for sel, tc := range rm.Targets {
			if target.MatchesSelector(sel) {
				for k := range tc.Dependencies {
					direct[k] = true
				}
				for k := range tc.DevDependencies {
					direct[k] = true
				}
			}
		}
		var names []string
		for k := range direct {
			names = append(names, k)
		}
		sort.Strings(names)
		for idx, name := range names {
			printNode(name, "", idx == len(names)-1, map[string]bool{rm.Package.Name: true})
		}
	}
	return 0
}
