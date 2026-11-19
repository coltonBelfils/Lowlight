package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ManifestFilename = "lowlight.toml"
	LockFilename     = "lowlight.lock"
	VendorDirName    = "vendor"
)

// DepKind distinguishes the three mutually exclusive dependency source forms in SPECIFICATION.md §12.2.
type DepKind int

const (
	DepInvalid DepKind = iota
	DepPath            // { path = "../local_lib" }
	DepURL             // { url = "https://...", hash = "sha256:..." }
	DepIndex           // { index = "https://.../pkg.toml", version = "^0.3" }
)

// DepSpec represents one dependency or override specification entry.
type DepSpec struct {
	Kind    DepKind
	Path    string // DepPath
	URL     string // DepURL
	Hash    string // DepURL ("sha256:<64-hex>")
	Index   string // DepIndex
	Version string // DepIndex (SemVer constraint)
}

// InlineTOML formats d in canonical single-line inline-table syntax (`SPECIFICATION.md §12.2`).
func (d DepSpec) InlineTOML() string {
	switch d.Kind {
	case DepPath:
		return fmt.Sprintf("{ path = %s }", FormatTOMLString(d.Path))
	case DepURL:
		return fmt.Sprintf("{ url = %s, hash = %s }", FormatTOMLString(d.URL), FormatTOMLString(d.Hash))
	case DepIndex:
		return fmt.Sprintf("{ index = %s, version = %s }", FormatTOMLString(d.Index), FormatTOMLString(d.Version))
	default:
		return "{}"
	}
}

// PackageInfo corresponds to `[package]` in `lowlight.toml`.
type PackageInfo struct {
	Name        string
	Version     string
	Edition     string
	Description string
	License     string
	Authors     []string
	Repository  string
	Homepage    string
	Keywords    []string
	Bin         string
}

// WorkspaceConfig corresponds to `[workspace]` in `lowlight.toml`.
type WorkspaceConfig struct {
	Members []string
	Exclude []string
}

// BuildConfig corresponds to `[build]` in `lowlight.toml`.
type BuildConfig struct {
	Backend string // "", "llvm", or "c11"
	Release *bool
	Target  string
	CC      string
	CFlags  []string
	CLibs   []string
}

// FormatConfig corresponds to `[format]` in `lowlight.toml`.
type FormatConfig struct {
	HasSection  bool
	IndentStyle string
	TabWidth    int
	LineWidth   int
}

// TargetConfig corresponds to `[target.<selector>]` in `lowlight.toml`.
type TargetConfig struct {
	Dependencies    map[string]DepSpec
	DevDependencies map[string]DepSpec
	CLibs           []string
	CFlags          []string
}

// Manifest represents a decoded and validated `lowlight.toml`.
type Manifest struct {
	Path            string // Absolute path to `lowlight.toml`
	Dir             string // Directory containing `lowlight.toml`
	HasPackage      bool
	Package         PackageInfo
	HasWorkspace    bool
	Workspace       WorkspaceConfig
	Dependencies    map[string]DepSpec
	DevDependencies map[string]DepSpec
	Overrides       map[string]DepSpec
	Build           BuildConfig
	Format          FormatConfig
	Targets         map[string]TargetConfig
}

// ProjectContext holds discovered manifest and optional enclosing workspace metadata.
type ProjectContext struct {
	// ActiveManifest is the nearest `lowlight.toml` for the target path (nil if standalone).
	ActiveManifest *Manifest
	// WorkspaceManifest is the root `[workspace]` manifest if active belongs to a workspace
	// (may be identical to ActiveManifest for hybrid root package + workspace).
	WorkspaceManifest *Manifest
	// WorkspaceMembers maps canonical member package name -> member Manifest.
	WorkspaceMembers map[string]*Manifest
}

// RootDir returns the canonical root directory owning `lowlight.lock`, `[overrides]`, and `./vendor/`.
func (pc *ProjectContext) RootDir() string {
	if pc == nil {
		return ""
	}
	if pc.WorkspaceManifest != nil {
		return pc.WorkspaceManifest.Dir
	}
	if pc.ActiveManifest != nil {
		return pc.ActiveManifest.Dir
	}
	return ""
}

// RootManifest returns the workspace root manifest if present, else the active manifest.
func (pc *ProjectContext) RootManifest() *Manifest {
	if pc == nil {
		return nil
	}
	if pc.WorkspaceManifest != nil {
		return pc.WorkspaceManifest
	}
	return pc.ActiveManifest
}

// LockPath returns the path to the governing `lowlight.lock`.
func (pc *ProjectContext) LockPath() string {
	r := pc.RootDir()
	if r == "" {
		return ""
	}
	return filepath.Join(r, LockFilename)
}

// VendorDir returns the path to the governing `./vendor/` directory.
func (pc *ProjectContext) VendorDir() string {
	r := pc.RootDir()
	if r == "" {
		return ""
	}
	return filepath.Join(r, VendorDirName)
}

// NormalizeQualifier converts a directory basename or package slug into a valid Lowlight
// import qualifier identifier (`SPECIFICATION.md §4.3`: runs of non-identifier chars become `_`).
func NormalizeQualifier(name string) string {
	var sb strings.Builder
	prevUnderscore := false
	for i := 0; i < len(name); i++ {
		ch := name[i]
		isAlpha := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
		isDigit := ch >= '0' && ch <= '9'
		if isAlpha || (isDigit && sb.Len() > 0) {
			sb.WriteByte(ch)
			prevUnderscore = ch == '_'
		} else {
			if !prevUnderscore {
				sb.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	out := sb.String()
	if out == "" {
		return "pkg"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "_" + out
	}
	return out
}

// IsValidPackageName checks SPECIFICATION.md §12.2 `[package].name` rules.
func IsValidPackageName(name string) bool {
	if name == "" {
		return false
	}
	c0 := name[0]
	if !((c0 >= 'a' && c0 <= 'z') || (c0 >= 'A' && c0 <= 'Z') || c0 == '_') {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// IsValidSHA256Hash checks `"sha256:<64-lowercase-hex>"`.
func IsValidSHA256Hash(h string) bool {
	if !strings.HasPrefix(h, "sha256:") {
		return false
	}
	hex := strings.TrimPrefix(h, "sha256:")
	if len(hex) != 64 {
		return false
	}
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// LoadManifest reads and validates a `lowlight.toml` file from disk.
func LoadManifest(path string) (*Manifest, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	return ParseManifest(abs, string(raw))
}

// ParseManifest parses and validates `lowlight.toml` content.
func ParseManifest(path, raw string) (*Manifest, error) {
	tbl, err := ParseTOML(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m := &Manifest{
		Path:            path,
		Dir:             filepath.Dir(path),
		Dependencies:    make(map[string]DepSpec),
		DevDependencies: make(map[string]DepSpec),
		Overrides:       make(map[string]DepSpec),
		Targets:         make(map[string]TargetConfig),
	}

	if pkgRaw, ok := tbl["package"]; ok {
		pkgTbl, ok := pkgRaw.(TOMLTable)
		if !ok {
			return nil, fmt.Errorf("%s: [package] must be a table", path)
		}
		m.HasPackage = true
		m.Package = PackageInfo{
			Name:        getString(pkgTbl, "name"),
			Version:     getString(pkgTbl, "version"),
			Edition:     getString(pkgTbl, "edition"),
			Description: getString(pkgTbl, "description"),
			License:     getString(pkgTbl, "license"),
			Authors:     getStringSlice(pkgTbl, "authors"),
			Repository:  getString(pkgTbl, "repository"),
			Homepage:    getString(pkgTbl, "homepage"),
			Keywords:    getStringSlice(pkgTbl, "keywords"),
			Bin:         getString(pkgTbl, "bin"),
		}
		if m.Package.Version == "" {
			m.Package.Version = "0.1.0"
		}
		if m.Package.Edition == "" {
			m.Package.Edition = "2026"
		}
	}

	if wsRaw, ok := tbl["workspace"]; ok {
		wsTbl, ok := wsRaw.(TOMLTable)
		if !ok {
			return nil, fmt.Errorf("%s: [workspace] must be a table", path)
		}
		m.HasWorkspace = true
		m.Workspace = WorkspaceConfig{
			Members: getStringSlice(wsTbl, "members"),
			Exclude: getStringSlice(wsTbl, "exclude"),
		}
	}

	if !m.HasPackage && !m.HasWorkspace {
		return nil, fmt.Errorf("%s: manifest must contain [package] and/or [workspace]", path)
	}
	if m.HasPackage {
		if !IsValidPackageName(m.Package.Name) {
			return nil, fmt.Errorf("%s: invalid [package].name %q (must start with ASCII letter or '_' and contain only letters, digits, '_', or '-')", path, m.Package.Name)
		}
		if m.Package.Name == "std" {
			return nil, fmt.Errorf("%s: [package].name cannot be reserved name \"std\"", path)
		}
		if _, err := ParseSemVer(m.Package.Version); err != nil {
			return nil, fmt.Errorf("%s: invalid [package].version: %w", path, err)
		}
	}

	if err := parseDepTable(path, tbl, "dependencies", m.Dependencies, false); err != nil {
		return nil, err
	}
	if err := parseDepTable(path, tbl, "dev-dependencies", m.DevDependencies, false); err != nil {
		return nil, err
	}
	if err := parseDepTable(path, tbl, "overrides", m.Overrides, true); err != nil {
		return nil, err
	}

	if buildRaw, ok := tbl["build"]; ok {
		bt, ok := buildRaw.(TOMLTable)
		if !ok {
			return nil, fmt.Errorf("%s: [build] must be a table", path)
		}
		backend := strings.ToLower(strings.TrimSpace(getString(bt, "backend")))
		if backend != "" && backend != "llvm" && backend != "c11" {
			return nil, fmt.Errorf("%s: invalid [build].backend %q (expected \"llvm\" or \"c11\")", path, backend)
		}
		var relPtr *bool
		if rv, exists := bt["release"]; exists {
			b, ok := rv.(bool)
			if !ok {
				return nil, fmt.Errorf("%s: [build].release must be a boolean", path)
			}
			relPtr = &b
		}
		m.Build = BuildConfig{
			Backend: backend,
			Release: relPtr,
			Target:  getString(bt, "target"),
			CC:      getString(bt, "cc"),
			CFlags:  getStringSlice(bt, "c_flags"),
			CLibs:   getStringSlice(bt, "c_libs"),
		}
	}

	if fmtRaw, ok := tbl["format"]; ok {
		ft, ok := fmtRaw.(TOMLTable)
		if !ok {
			return nil, fmt.Errorf("%s: [format] must be a table", path)
		}
		m.Format = FormatConfig{
			HasSection:  true,
			IndentStyle: getString(ft, "indent_style"),
			TabWidth:    int(getInt(ft, "tab_width")),
			LineWidth:   int(getInt(ft, "line_width")),
		}
	}

	if tgtRaw, ok := tbl["target"]; ok {
		tt, ok := tgtRaw.(TOMLTable)
		if !ok {
			return nil, fmt.Errorf("%s: [target] must be a table", path)
		}
		for sel, v := range tt {
			sub, ok := v.(TOMLTable)
			if !ok {
				return nil, fmt.Errorf("%s: [target.%s] must be a table", path, sel)
			}
			tc := TargetConfig{
				Dependencies:    make(map[string]DepSpec),
				DevDependencies: make(map[string]DepSpec),
				CLibs:           getStringSlice(sub, "c_libs"),
				CFlags:          getStringSlice(sub, "c_flags"),
			}
			if err := parseDepTable(path, sub, "dependencies", tc.Dependencies, false); err != nil {
				return nil, err
			}
			if err := parseDepTable(path, sub, "dev-dependencies", tc.DevDependencies, false); err != nil {
				return nil, err
			}
			m.Targets[sel] = tc
		}
	}

	return m, nil
}

func parseDepTable(path string, parent TOMLTable, key string, out map[string]DepSpec, isOverride bool) error {
	raw, ok := parent[key]
	if !ok {
		return nil
	}
	tbl, ok := raw.(TOMLTable)
	if !ok {
		return fmt.Errorf("%s: [%s] must be a table", path, key)
	}
	for depName, v := range tbl {
		if !IsValidPackageName(depName) || depName == "std" {
			return fmt.Errorf("%s: invalid dependency key %q in [%s]", path, depName, key)
		}
		dt, ok := v.(TOMLTable)
		if !ok {
			return fmt.Errorf("%s: [%s].%s must be an inline table or sub-table", path, key, depName)
		}
		spec, err := decodeDepSpec(dt, isOverride)
		if err != nil {
			return fmt.Errorf("%s: [%s].%s: %w", path, key, depName, err)
		}
		out[depName] = spec
	}
	return nil
}

func decodeDepSpec(dt TOMLTable, isOverride bool) (DepSpec, error) {
	pathVal := getString(dt, "path")
	urlVal := getString(dt, "url")
	hashVal := getString(dt, "hash")
	indexVal := getString(dt, "index")
	verVal := getString(dt, "version")

	kinds := 0
	if pathVal != "" {
		kinds++
	}
	if urlVal != "" {
		kinds++
	}
	if indexVal != "" {
		kinds++
	}
	if kinds != 1 {
		return DepSpec{}, fmt.Errorf("must specify exactly one of `path`, `url`+`hash`, or `index`+`version`")
	}

	switch {
	case pathVal != "":
		if hashVal != "" || verVal != "" {
			return DepSpec{}, fmt.Errorf("`path` dependency cannot also set `hash` or `version`")
		}
		return DepSpec{Kind: DepPath, Path: pathVal}, nil
	case urlVal != "":
		if !IsValidSHA256Hash(hashVal) {
			return DepSpec{}, fmt.Errorf("`url` dependency requires `hash = \"sha256:<64-lowercase-hex>\"`")
		}
		return DepSpec{Kind: DepURL, URL: urlVal, Hash: hashVal}, nil
	default:
		if isOverride {
			return DepSpec{}, fmt.Errorf("[overrides] entries must pin a local `path` or immutable `url`+`hash`")
		}
		if verVal == "" {
			return DepSpec{}, fmt.Errorf("`index` dependency requires a `version` SemVer constraint")
		}
		if _, err := ParseVersionConstraint(verVal); err != nil {
			return DepSpec{}, err
		}
		return DepSpec{Kind: DepIndex, Index: indexVal, Version: verVal}, nil
	}
}

// EffectiveDeps merges top-level dependencies with any matching `[target.<selector>]` dependencies.
func (m *Manifest) EffectiveDeps(target TargetInfo, includeDev bool) (deps map[string]DepSpec, devDeps map[string]DepSpec) {
	deps = make(map[string]DepSpec, len(m.Dependencies))
	for k, v := range m.Dependencies {
		deps[k] = v
	}
	devDeps = make(map[string]DepSpec)
	if includeDev {
		for k, v := range m.DevDependencies {
			devDeps[k] = v
		}
	}
	selectors := make([]string, 0, len(m.Targets))
	for sel := range m.Targets {
		selectors = append(selectors, sel)
	}
	sort.Strings(selectors)
	for _, sel := range selectors {
		if !target.MatchesSelector(sel) {
			continue
		}
		tc := m.Targets[sel]
		for k, v := range tc.Dependencies {
			deps[k] = v
		}
		if includeDev {
			for k, v := range tc.DevDependencies {
				devDeps[k] = v
			}
		}
	}
	return deps, devDeps
}

// EffectiveTargetFlags collects `[build]` and matching `[target.<selector>]` C flags and libraries.
func (m *Manifest) EffectiveTargetFlags(target TargetInfo) (cFlags []string, cLibs []string) {
	cFlags = append(cFlags, m.Build.CFlags...)
	cLibs = append(cLibs, m.Build.CLibs...)
	selectors := make([]string, 0, len(m.Targets))
	for sel := range m.Targets {
		selectors = append(selectors, sel)
	}
	sort.Strings(selectors)
	for _, sel := range selectors {
		if !target.MatchesSelector(sel) {
			continue
		}
		tc := m.Targets[sel]
		cFlags = append(cFlags, tc.CFlags...)
		cLibs = append(cLibs, tc.CLibs...)
	}
	return cFlags, cLibs
}

// ExpandWorkspaceMembers expands `[workspace].members` globs (minus `exclude`) into sorted member directories.
func (m *Manifest) ExpandWorkspaceMembers() ([]string, error) {
	if !m.HasWorkspace {
		return nil, nil
	}
	excluded := make(map[string]bool)
	for _, pat := range m.Workspace.Exclude {
		matches, _ := filepath.Glob(filepath.Join(m.Dir, pat))
		for _, mm := range matches {
			if abs, err := filepath.Abs(mm); err == nil {
				excluded[filepath.Clean(abs)] = true
			}
		}
		if abs, err := filepath.Abs(filepath.Join(m.Dir, pat)); err == nil {
			excluded[filepath.Clean(abs)] = true
		}
	}

	seen := make(map[string]bool)
	var dirs []string
	for _, pat := range m.Workspace.Members {
		fullPat := filepath.Join(m.Dir, pat)
		var cands []string
		if strings.ContainsAny(pat, "*?[") {
			matches, err := filepath.Glob(fullPat)
			if err != nil {
				return nil, fmt.Errorf("%s: invalid workspace member glob %q: %w", m.Path, pat, err)
			}
			cands = matches
		} else {
			cands = []string{fullPat}
		}
		for _, c := range cands {
			abs, err := filepath.Abs(c)
			if err != nil {
				continue
			}
			clean := filepath.Clean(abs)
			if excluded[clean] || seen[clean] {
				continue
			}
			if st, err := os.Stat(filepath.Join(clean, ManifestFilename)); err == nil && !st.IsDir() {
				seen[clean] = true
				dirs = append(dirs, clean)
			}
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// DiscoverProject walks upward from startPath (file or directory) to locate the nearest
// `lowlight.toml` and any enclosing `[workspace]` root (`SPECIFICATION.md §12.1`).
// Returns `(nil, nil)` for standalone manifest-free scripts/directories.
func DiscoverProject(startPath string) (*ProjectContext, error) {
	if startPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, nil
		}
		startPath = cwd
	}
	abs, err := filepath.Abs(startPath)
	if err != nil {
		abs = startPath
	}
	dir := abs
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		dir = filepath.Dir(abs)
	} else if strings.HasSuffix(abs, ".ll") {
		dir = filepath.Dir(abs)
	}

	// Collect all manifests walking upward to the filesystem root.
	var found []*Manifest
	cur := dir
	for cur != "" {
		cand := filepath.Join(cur, ManifestFilename)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			m, err := LoadManifest(cand)
			if err != nil {
				return nil, err
			}
			found = append(found, m)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	if len(found) == 0 {
		return nil, nil
	}

	active := found[0]
	ctx := &ProjectContext{
		ActiveManifest:   active,
		WorkspaceMembers: make(map[string]*Manifest),
	}

	// Check if active itself or an ancestor is a `[workspace]` containing active.
	var wsRoot *Manifest
	if active.HasWorkspace {
		wsRoot = active
	} else {
		activeClean := filepath.Clean(active.Dir)
		for _, anc := range found[1:] {
			if !anc.HasWorkspace {
				continue
			}
			memberDirs, err := anc.ExpandWorkspaceMembers()
			if err != nil {
				return nil, err
			}
			for _, md := range memberDirs {
				if filepath.Clean(md) == activeClean {
					wsRoot = anc
					break
				}
			}
			if wsRoot != nil {
				break
			}
		}
	}

	if wsRoot != nil {
		ctx.WorkspaceManifest = wsRoot
		if wsRoot.HasPackage {
			ctx.WorkspaceMembers[wsRoot.Package.Name] = wsRoot
		}
		memberDirs, err := wsRoot.ExpandWorkspaceMembers()
		if err != nil {
			return nil, err
		}
		for _, md := range memberDirs {
			if filepath.Clean(md) == filepath.Clean(wsRoot.Dir) {
				continue
			}
			mm, err := LoadManifest(filepath.Join(md, ManifestFilename))
			if err != nil {
				return nil, err
			}
			if mm.HasPackage {
				ctx.WorkspaceMembers[mm.Package.Name] = mm
			}
		}
	}

	return ctx, nil
}

// FindStdDir locates the toolchain's `std/` directory cleanly decoupled from user project roots
// (`SPECIFICATION.md §12.1`).
func FindStdDir(explicitRoot, startDir string) string {
	isStdDir := func(dir string) bool {
		if dir == "" {
			return false
		}
		if st, err := os.Stat(filepath.Join(dir, "core", "core.ll")); err == nil && !st.IsDir() {
			return true
		}
		if st, err := os.Stat(filepath.Join(dir, "core", "interfaces.ll")); err == nil && !st.IsDir() {
			return true
		}
		if st, err := os.Stat(filepath.Join(dir, "core.ll")); err == nil && !st.IsDir() {
			return true
		}
		return false
	}
	searchUp := func(from string) string {
		dir := from
		for dir != "" {
			cand := filepath.Join(dir, "std")
			if isStdDir(cand) {
				return cand
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		return ""
	}
	if env := os.Getenv("LOWLIGHT_STD"); isStdDir(env) {
		return env
	}
	if env := os.Getenv("LOWLIGHT_ROOT"); env != "" {
		if cand := filepath.Join(env, "std"); isStdDir(cand) {
			return cand
		}
	}
	if explicitRoot != "" {
		if cand := filepath.Join(explicitRoot, "std"); isStdDir(cand) {
			return cand
		}
		if isStdDir(explicitRoot) {
			return explicitRoot
		}
	}
	if r := searchUp(startDir); r != "" {
		return r
	}
	if cwd, err := os.Getwd(); err == nil {
		if r := searchUp(cwd); r != "" {
			return r
		}
	}
	if exe, err := os.Executable(); err == nil {
		if r := searchUp(filepath.Dir(exe)); r != "" {
			return r
		}
	}
	return filepath.Join(explicitRoot, "std")
}

func getString(tbl TOMLTable, key string) string {
	if v, ok := tbl[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getBool(tbl TOMLTable, key string) bool {
	if v, ok := tbl[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func getInt(tbl TOMLTable, key string) int64 {
	if v, ok := tbl[key]; ok {
		if n, ok := v.(int64); ok {
			return n
		}
	}
	return 0
}

func getStringSlice(tbl TOMLTable, key string) []string {
	v, ok := tbl[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, elem := range arr {
		if s, ok := elem.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
