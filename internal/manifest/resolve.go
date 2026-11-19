package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResolveOptions controls dependency resolution behavior (`SPECIFICATION.md §12.5`).
type ResolveOptions struct {
	Target       TargetInfo
	IncludeDev   bool
	AllowNetwork bool   // false for check/build/run/test/lsp; true for fetch/add/update/vendor
	Locked       bool   // fail if lowlight.lock is missing or out of sync with lowlight.toml
	Offline      bool   // hard forbid all network I/O
	UpdatePkgs   []string // non-empty during `lowlight update [pkg...]` (or UpdateAll=true)
	UpdateAll    bool
	CacheRoot    string
	Fetcher      *Fetcher
}

// ResolvedPackage represents one resolved external/workspace dependency in the build graph.
type ResolvedPackage struct {
	Name         string
	Version      string
	Dir          string // Canonical local filesystem directory
	Source       string // "override", "workspace", "vendor", "path", "cache"
	Index        string
	URL          string
	Hash         string
	Dependencies []string
	IsDevOnly    bool
}

// ResolvedGraph is the complete unified single-version dependency graph for a project or workspace.
type ResolvedGraph struct {
	Packages map[string]*ResolvedPackage // dep name -> resolved package
	DevOnly  map[string]bool             // true if dep is only reachable via [dev-dependencies]
	Lockfile *Lockfile                   // resulting deterministic lockfile snapshot
}

// Resolve computes the unified dependency graph for `ctx` following `SPECIFICATION.md §12.5`:
//   1. Root `[overrides]` (`path` or pinned `url`+`hash`)
//   2. Matching `[workspace]` member packages
//   3. `./vendor/<dep>/` (authoritative when `./vendor/` exists at project/workspace root)
//   4. `lowlight.lock` + `$LOWLIGHT_CACHE` / `~/.lowlight/cache`
//
// When `opts.AllowNetwork` is false (default for compiler/LSP builds), Resolve performs zero
// network calls and fails loudly with actionable guidance if a remote dependency is uncached.
func Resolve(ctx *ProjectContext, opts ResolveOptions) (*ResolvedGraph, error) {
	if ctx == nil || ctx.ActiveManifest == nil {
		return &ResolvedGraph{
			Packages: make(map[string]*ResolvedPackage),
			DevOnly:  make(map[string]bool),
			Lockfile: &Lockfile{LockVersion: CurrentLockVersion},
		}, nil
	}
	if opts.Offline {
		opts.AllowNetwork = false
	}
	if opts.Target.OS == "" {
		opts.Target = ParseTarget("")
	}
	cacheRoot := opts.CacheRoot
	if cacheRoot == "" {
		cacheRoot = DefaultCacheRoot()
	}
	fetcher := opts.Fetcher
	if fetcher == nil {
		fetcher = NewFetcher(cacheRoot)
	}

	rootManifest := ctx.RootManifest()
	rootDir := ctx.RootDir()
	lockPath := ctx.LockPath()
	vendorRoot := ctx.VendorDir()
	hasVendor := false
	if st, err := os.Stat(vendorRoot); err == nil && st.IsDir() {
		hasVendor = true
	}

	var existingLock *Lockfile
	if st, err := os.Stat(lockPath); err == nil && !st.IsDir() {
		lf, err := LoadLockfile(lockPath)
		if err != nil {
			return nil, err
		}
		existingLock = lf
	} else if opts.Locked && !hasVendor {
		// Only fail --locked if there actually are remote/non-workspace dependencies.
		if projectHasAnyDeps(ctx, opts.Target, opts.IncludeDev) {
			return nil, fmt.Errorf("missing %s under --locked; run `lowlight fetch` first", lockPath)
		}
	}

	updateSet := make(map[string]bool, len(opts.UpdatePkgs))
	for _, p := range opts.UpdatePkgs {
		updateSet[p] = true
	}

	overrides := make(map[string]DepSpec)
	if rootManifest != nil {
		for k, v := range rootManifest.Overrides {
			overrides[k] = v
		}
	}

	// Collect all root requirements from active manifest + workspace members.
	type depReq struct {
		fromPkg  string
		fromDir  string
		name     string
		spec     DepSpec
		isDev    bool
	}
	var initialReqs []depReq
	addManifestReqs := func(m *Manifest, includeDev bool) {
		if m == nil {
			return
		}
		label := m.Package.Name
		if label == "" {
			label = filepath.Base(m.Dir)
		}
		deps, devDeps := m.EffectiveDeps(opts.Target, includeDev)
		for _, k := range sortedDepKeys(deps) {
			initialReqs = append(initialReqs, depReq{fromPkg: label, fromDir: m.Dir, name: k, spec: deps[k], isDev: false})
		}
		for _, k := range sortedDepKeys(devDeps) {
			initialReqs = append(initialReqs, depReq{fromPkg: label, fromDir: m.Dir, name: k, spec: devDeps[k], isDev: true})
		}
	}
	if ctx.WorkspaceManifest != nil && len(ctx.WorkspaceMembers) > 0 {
		names := make([]string, 0, len(ctx.WorkspaceMembers))
		for n := range ctx.WorkspaceMembers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			mm := ctx.WorkspaceMembers[n]
			addManifestReqs(mm, opts.IncludeDev)
		}
		if !ctx.WorkspaceManifest.HasPackage {
			addManifestReqs(ctx.WorkspaceManifest, opts.IncludeDev)
		}
	} else {
		addManifestReqs(ctx.ActiveManifest, opts.IncludeDev)
	}

	// Track per-dep accumulated constraints / exact pins for MVS + conflict detection.
	type depState struct {
		name           string
		nonDevReach    bool
		reqs           []depReq
		resolved       *ResolvedPackage
		expanded       bool
		indexCache     *StaticIndex
	}
	states := make(map[string]*depState)
	indexMemo := make(map[string]*StaticIndex)

	// Seed workspace members directly so cross-member imports always resolve even without explicit [dependencies].
	for memberName, mm := range ctx.WorkspaceMembers {
		if ctx.ActiveManifest != nil && filepath.Clean(mm.Dir) == filepath.Clean(ctx.ActiveManifest.Dir) {
			continue
		}
		states[memberName] = &depState{
			name:        memberName,
			nonDevReach: true,
			resolved: &ResolvedPackage{
				Name:    memberName,
				Version: mm.Package.Version,
				Dir:     mm.Dir,
				Source:  "workspace",
			},
		}
	}

	var queue []string
	enqueue := func(req depReq) {
		st, ok := states[req.name]
		if !ok {
			st = &depState{name: req.name}
			states[req.name] = st
		}
		if !req.isDev {
			st.nonDevReach = true
		}
		st.reqs = append(st.reqs, req)
		queue = append(queue, req.name)
	}
	for _, r := range initialReqs {
		enqueue(r)
	}

	// Iterative MVS fixpoint loop.
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		st := states[name]
		if st == nil || len(st.reqs) == 0 {
			continue
		}

		// Precedence 1: Root [overrides].
		if ov, hasOv := overrides[name]; hasOv {
			if st.resolved == nil {
				rp, err := resolveOverride(name, ov, rootDir, cacheRoot, fetcher, opts.AllowNetwork)
				if err != nil {
					return nil, err
				}
				st.resolved = rp
			}
		} else if mm, isWS := ctx.WorkspaceMembers[name]; isWS {
			// Precedence 2: Local [workspace] member package.
			st.resolved = &ResolvedPackage{
				Name:    name,
				Version: mm.Package.Version,
				Dir:     mm.Dir,
				Source:  "workspace",
			}
		} else if hasVendor {
			// Precedence 3: ./vendor/<dep>/ is authoritative when ./vendor/ exists.
			vDir := filepath.Join(vendorRoot, name)
			if stDir, err := os.Stat(vDir); err == nil && stDir.IsDir() {
				ver := "0.0.0"
				var locked LockPackage
				if existingLock != nil {
					if lp, ok := existingLock.FindByName(name); ok {
						locked = lp
						if lp.Version != "" {
							ver = lp.Version
						}
					}
				}
				if vm, err := LoadManifest(filepath.Join(vDir, ManifestFilename)); err == nil && vm.HasPackage && vm.Package.Version != "" {
					ver = vm.Package.Version
				}
				st.resolved = &ResolvedPackage{
					Name:    name,
					Version: ver,
					Dir:     vDir,
					Source:  "vendor",
					Index:   locked.Index,
					URL:     locked.URL,
					Hash:    locked.Hash,
				}
			} else if isAllPathReqs(st.reqs) {
				rp, err := resolvePathReqs(name, st.reqs)
				if err != nil {
					return nil, err
				}
				st.resolved = rp
			} else {
				return nil, fmt.Errorf("vendored dependency %q missing in %s (run `lowlight vendor` to refresh)", name, vendorRoot)
			}
		} else {
			// Precedence 4: Local path, immutable URL+hash, or static index (MVS) with lockfile + cache.
			rp, err := resolveStandardDep(name, st.reqs, existingLock, indexMemo, cacheRoot, fetcher, opts, updateSet)
			if err != nil {
				return nil, err
			}
			if st.resolved != nil && (st.resolved.Version != rp.Version || st.resolved.Hash != rp.Hash || st.resolved.Dir != rp.Dir) {
				st.expanded = false // Re-expand transitive deps if MVS bumped the minimal version!
			}
			st.resolved = rp
		}

		// Expand transitive dependencies from the resolved package's manifest (or index version entry).
		if st.resolved != nil && !st.expanded {
			st.expanded = true
			var transDeps map[string]DepSpec
			var transFromDir string
			if st.resolved.Dir != "" {
				if subM, err := LoadManifest(filepath.Join(st.resolved.Dir, ManifestFilename)); err == nil {
					transDeps, _ = subM.EffectiveDeps(opts.Target, false)
					transFromDir = subM.Dir
				}
			}
			// Fallback to static index embedded dependencies if present in memory.
			if len(transDeps) == 0 && st.resolved.Index != "" {
				if idx, ok := indexMemo[st.resolved.Index]; ok {
					if iv, ok := idx.Versions[st.resolved.Version]; ok {
						transDeps = iv.EffectiveDeps(opts.Target)
					}
				}
			}
			// Fallback to existing lowlight.lock transitive edges when resolving offline without a bundled manifest.
			var depNames []string
			if len(transDeps) == 0 && existingLock != nil {
				if lp, ok := existingLock.FindByName(name); ok && len(lp.Dependencies) > 0 {
					for _, dk := range lp.Dependencies {
						depNames = append(depNames, dk)
						if tlp, ok2 := existingLock.FindByName(dk); ok2 {
							var spec DepSpec
							switch {
							case tlp.Index != "":
								spec = DepSpec{Kind: DepIndex, Index: tlp.Index, Version: "=" + tlp.Version}
							case tlp.URL != "":
								spec = DepSpec{Kind: DepURL, URL: tlp.URL, Hash: tlp.Hash}
							case tlp.Path != "":
								spec = DepSpec{Kind: DepPath, Path: tlp.Path}
							}
							if spec.Kind != DepInvalid {
								enqueue(depReq{
									fromPkg: name,
									fromDir: rootDir,
									name:    dk,
									spec:    spec,
									isDev:   !st.nonDevReach,
								})
							}
						}
					}
				}
			}
			for _, dk := range sortedDepKeys(transDeps) {
				depNames = append(depNames, dk)
				enqueue(depReq{
					fromPkg: name,
					fromDir: transFromDir,
					name:    dk,
					spec:    transDeps[dk],
					isDev:   !st.nonDevReach,
				})
			}
			sort.Strings(depNames)
			st.resolved.Dependencies = depNames
		}
	}

	// Build final ResolvedGraph + deterministic Lockfile snapshot.
	graph := &ResolvedGraph{
		Packages: make(map[string]*ResolvedPackage, len(states)),
		DevOnly:  make(map[string]bool),
		Lockfile: &Lockfile{LockVersion: CurrentLockVersion},
	}
	for name, st := range states {
		if st.resolved == nil {
			continue
		}
		st.resolved.IsDevOnly = !st.nonDevReach
		graph.Packages[name] = st.resolved
		if st.resolved.IsDevOnly {
			graph.DevOnly[name] = true
		}
		if st.resolved.Source == "workspace" {
			continue
		}
		lp := LockPackage{
			Name:         st.resolved.Name,
			Version:      st.resolved.Version,
			Index:        st.resolved.Index,
			URL:          st.resolved.URL,
			Hash:         st.resolved.Hash,
			Dependencies: st.resolved.Dependencies,
		}
		if st.resolved.Source == "path" || (st.resolved.Source == "override" && st.resolved.URL == "") {
			if rel, err := filepath.Rel(rootDir, st.resolved.Dir); err == nil {
				lp.Path = filepath.ToSlash(rel)
			} else {
				lp.Path = st.resolved.Dir
			}
		}
		graph.Lockfile.Packages = append(graph.Lockfile.Packages, lp)
	}
	graph.Lockfile.Sort()

	// Under --locked, verify computed lockfile matches existing lowlight.lock verbatim.
	if opts.Locked && !hasVendor && len(graph.Lockfile.Packages) > 0 {
		if existingLock == nil {
			return nil, fmt.Errorf("missing %s under --locked; run `lowlight fetch` first", lockPath)
		}
		if existingLock.Format() != graph.Lockfile.Format() {
			return nil, fmt.Errorf("%s is out of date with lowlight.toml under --locked; run `lowlight fetch` or `lowlight update`", lockPath)
		}
	}

	return graph, nil
}

func resolveOverride(name string, ov DepSpec, rootDir, cacheRoot string, fetcher *Fetcher, allowNetwork bool) (*ResolvedPackage, error) {
	switch ov.Kind {
	case DepPath:
		dir := ov.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(rootDir, dir)
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("[overrides].%s path %q does not exist", name, abs)
		}
		ver := "0.0.0"
		if subM, err := LoadManifest(filepath.Join(abs, ManifestFilename)); err == nil && subM.HasPackage {
			ver = subM.Package.Version
		}
		return &ResolvedPackage{Name: name, Version: ver, Dir: abs, Source: "override"}, nil
	case DepURL:
		dir, err := ensureOrRequireCache(name, ov.URL, ov.Hash, cacheRoot, fetcher, allowNetwork)
		if err != nil {
			return nil, err
		}
		ver := "0.0.0"
		if subM, err := LoadManifest(filepath.Join(dir, ManifestFilename)); err == nil && subM.HasPackage {
			ver = subM.Package.Version
		}
		return &ResolvedPackage{Name: name, Version: ver, Dir: dir, Source: "override", URL: ov.URL, Hash: ov.Hash}, nil
	default:
		return nil, fmt.Errorf("[overrides].%s must specify `path` or `url`+`hash`", name)
	}
}

func isAllPathReqs[T interface{ ~struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool } }](reqs []T) bool {
	for _, r := range reqs {
		if struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool }(r).spec.Kind != DepPath {
			return false
		}
	}
	return len(reqs) > 0
}

func resolvePathReqs[T interface{ ~struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool } }](name string, reqs []T) (*ResolvedPackage, error) {
	var canonical string
	for _, raw := range reqs {
		r := struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool }(raw)
		p := r.spec.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.fromDir, p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		clean := filepath.Clean(abs)
		if canonical == "" {
			canonical = clean
		} else if canonical != clean {
			return nil, fmt.Errorf("conflicting `path` dependencies for %q (%q vs %q); pin one via [overrides] in root lowlight.toml", name, canonical, clean)
		}
	}
	if st, err := os.Stat(canonical); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("path dependency %q directory %q does not exist", name, canonical)
	}
	ver := "0.0.0"
	if subM, err := LoadManifest(filepath.Join(canonical, ManifestFilename)); err == nil && subM.HasPackage {
		ver = subM.Package.Version
	}
	return &ResolvedPackage{Name: name, Version: ver, Dir: canonical, Source: "path"}, nil
}

func resolveStandardDep[T interface{ ~struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool } }](
	name string,
	reqs []T,
	existingLock *Lockfile,
	indexMemo map[string]*StaticIndex,
	cacheRoot string,
	fetcher *Fetcher,
	opts ResolveOptions,
	updateSet map[string]bool,
) (*ResolvedPackage, error) {
	// Ensure all requirements agree on source kind (no mixing raw URL and different index/path without [overrides]).
	first := struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool }(reqs[0])
	for _, raw := range reqs[1:] {
		r := struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool }(raw)
		if r.spec.Kind != first.spec.Kind {
			return nil, fmt.Errorf("conflicting dependency sources for %q (%s vs %s); resolve via [overrides] in root lowlight.toml",
				name, first.fromPkg, r.fromPkg)
		}
	}

	switch first.spec.Kind {
	case DepPath:
		return resolvePathReqs(name, reqs)

	case DepURL:
		urlVal := first.spec.URL
		hashVal := first.spec.Hash
		for _, raw := range reqs[1:] {
			r := struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool }(raw)
			if r.spec.URL != urlVal || r.spec.Hash != hashVal {
				return nil, fmt.Errorf("conflicting `url`/`hash` for dependency %q between %q (%s) and %q (%s); resolve via [overrides] in root lowlight.toml",
					name, first.fromPkg, hashVal, r.fromPkg, r.spec.Hash)
			}
		}
		dir, err := ensureOrRequireCache(name, urlVal, hashVal, cacheRoot, fetcher, opts.AllowNetwork)
		if err != nil {
			return nil, err
		}
		ver := "0.0.0"
		if subM, err := LoadManifest(filepath.Join(dir, ManifestFilename)); err == nil && subM.HasPackage {
			ver = subM.Package.Version
		}
		return &ResolvedPackage{Name: name, Version: ver, Dir: dir, Source: "cache", URL: urlVal, Hash: hashVal}, nil

	case DepIndex:
		indexURL := first.spec.Index
		var constraints []VersionConstraint
		for _, raw := range reqs {
			r := struct{ fromPkg, fromDir, name string; spec DepSpec; isDev bool }(raw)
			if r.spec.Index != indexURL {
				return nil, fmt.Errorf("conflicting static `index` URLs for dependency %q between %q (%s) and %q (%s); resolve via [overrides] in root lowlight.toml",
					name, first.fromPkg, indexURL, r.fromPkg, r.spec.Index)
			}
			c, err := ParseVersionConstraint(r.spec.Version)
			if err != nil {
				return nil, fmt.Errorf("invalid constraint %q for %q from %q: %w", r.spec.Version, name, r.fromPkg, err)
			}
			constraints = append(constraints, c)
		}

		shouldUpdate := opts.UpdateAll || updateSet[name]
		// If locked and NOT explicitly updating, honor the locked version if it satisfies every constraint!
		if existingLock != nil && !shouldUpdate {
			if lp, ok := existingLock.FindByName(name); ok && lp.Index == indexURL && lp.URL != "" && lp.Hash != "" {
				if sv, err := ParseSemVer(lp.Version); err == nil {
					allMatch := true
					for _, c := range constraints {
						if !c.Matches(sv) {
							allMatch = false
							break
						}
					}
					if allMatch {
						dir, err := ensureOrRequireCache(name, lp.URL, lp.Hash, cacheRoot, fetcher, opts.AllowNetwork)
						if err != nil {
							return nil, err
						}
						return &ResolvedPackage{
							Name:         name,
							Version:      lp.Version,
							Dir:          dir,
							Source:       "cache",
							Index:        lp.Index,
							URL:          lp.URL,
							Hash:         lp.Hash,
							Dependencies: lp.Dependencies,
						}, nil
					}
				}
			}
		}

		// Need to resolve from remote static index via MVS.
		if !opts.AllowNetwork {
			return nil, fmt.Errorf("dependency %q (index %s) is not locked/cached locally; run `lowlight fetch` or `lowlight update`", name, indexURL)
		}
		idx, ok := indexMemo[indexURL]
		if !ok {
			var err error
			idx, err = fetcher.FetchIndex(indexURL)
			if err != nil {
				return nil, err
			}
			indexMemo[indexURL] = idx
		}

		allowYanked := ""
		if existingLock != nil && !shouldUpdate {
			if lp, ok := existingLock.FindByName(name); ok && lp.Index == indexURL {
				allowYanked = lp.Version
			}
		}
		selected, found := idx.SelectMinimalVersion(constraints, allowYanked)
		if !found {
			var parts []string
			for _, c := range constraints {
				parts = append(parts, c.Raw)
			}
			return nil, fmt.Errorf("no version of %q in index %s satisfies constraints [%s]; pin a version via [overrides] in root lowlight.toml",
				name, indexURL, strings.Join(parts, ", "))
		}

		dir, err := ensureOrRequireCache(name, selected.URL, selected.Hash, cacheRoot, fetcher, true)
		if err != nil {
			return nil, err
		}
		return &ResolvedPackage{
			Name:    name,
			Version: selected.Version.String(),
			Dir:     dir,
			Source:  "cache",
			Index:   indexURL,
			URL:     selected.URL,
			Hash:    selected.Hash,
		}, nil
	default:
		return nil, fmt.Errorf("invalid dependency specification for %q", name)
	}
}

func ensureOrRequireCache(name, archiveURL, hash, cacheRoot string, fetcher *Fetcher, allowNetwork bool) (string, error) {
	cacheDir, err := CachePackageDir(cacheRoot, hash)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(cacheDir); err == nil && st.IsDir() {
		return cacheDir, nil
	}
	if !allowNetwork {
		return "", fmt.Errorf("dependency %q (%s) is missing from local cache %s; run `lowlight fetch`", name, hash, cacheDir)
	}
	dir, _, err := fetcher.EnsureArchiveCached(archiveURL, hash)
	return dir, err
}

// SyncLockfile resolves dependencies (allowing network fetch unless `opts.Offline`),
// populates the local cache, and writes deterministic `lowlight.lock` (`lowlight fetch` / `lowlight update`).
func SyncLockfile(ctx *ProjectContext, opts ResolveOptions) (*ResolvedGraph, error) {
	if !opts.Offline {
		opts.AllowNetwork = true
	}
	opts.IncludeDev = true
	graph, err := Resolve(ctx, opts)
	if err != nil {
		return nil, err
	}
	if lp := ctx.LockPath(); lp != "" {
		if err := WriteLockfile(lp, graph.Lockfile); err != nil {
			return nil, err
		}
	}
	return graph, nil
}

// VendorDependencies materializes all resolved external dependencies into `./vendor/<name>/`
// at the project/workspace root (`SPECIFICATION.md §12.5–§12.6`).
func VendorDependencies(ctx *ProjectContext, opts ResolveOptions) (*ResolvedGraph, error) {
	if ctx == nil || ctx.RootDir() == "" {
		return nil, fmt.Errorf("cannot run vendor outside a lowlight.toml project or workspace")
	}
	// Resolve ignoring any stale ./vendor/ temporarily by resolving from lock/cache/network, then writing fresh ./vendor/.
	vendorDir := ctx.VendorDir()
	tmpVendor, err := os.MkdirTemp(ctx.RootDir(), ".vendor-tmp-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpVendor)

	// Temporarily move existing vendor aside if present so SyncLockfile reads canonical cache/lock/network.
	backupVendor := ""
	if st, err := os.Stat(vendorDir); err == nil && st.IsDir() {
		backupVendor = vendorDir + ".bak-tmp"
		_ = os.RemoveAll(backupVendor)
		if err := os.Rename(vendorDir, backupVendor); err != nil {
			return nil, err
		}
	}
	restoreBackup := func() {
		if backupVendor != "" {
			_ = os.RemoveAll(vendorDir)
			_ = os.Rename(backupVendor, vendorDir)
		}
	}

	graph, err := SyncLockfile(ctx, opts)
	if err != nil {
		restoreBackup()
		return nil, err
	}

	names := make([]string, 0, len(graph.Packages))
	for n := range graph.Packages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		rp := graph.Packages[name]
		if rp.Source == "workspace" {
			continue
		}
		dst := filepath.Join(tmpVendor, name)
		if err := copyDirTree(rp.Dir, dst); err != nil {
			restoreBackup()
			return nil, fmt.Errorf("vendoring %q: %w", name, err)
		}
	}

	if err := os.RemoveAll(vendorDir); err != nil {
		restoreBackup()
		return nil, err
	}
	if err := os.Rename(tmpVendor, vendorDir); err != nil {
		restoreBackup()
		return nil, err
	}
	if backupVendor != "" {
		_ = os.RemoveAll(backupVendor)
	}
	return graph, nil
}

func projectHasAnyDeps(ctx *ProjectContext, target TargetInfo, includeDev bool) bool {
	check := func(m *Manifest) bool {
		if m == nil {
			return false
		}
		deps, devDeps := m.EffectiveDeps(target, includeDev)
		return len(deps) > 0 || len(devDeps) > 0
	}
	if check(ctx.ActiveManifest) || check(ctx.WorkspaceManifest) {
		return true
	}
	for _, mm := range ctx.WorkspaceMembers {
		if check(mm) {
			return true
		}
	}
	return false
}

func sortedDepKeys(m map[string]DepSpec) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
