package sema

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/manifest"
	"lowlight/internal/types"
)

const (
	ideProgCacheMaxEntries = 32
	pkgConstCacheMax       = 1024
)

// CheckIDEOptions configures IDE/LSP semantic analysis and IDE comptime budgets.
type CheckIDEOptions struct {
	EntryPath          string
	Overlays           map[string]string
	ProjectRoot        string
	TargetTriple       string
	TestMode           bool
	StdDir             string
	IDEComptimeTimeout time.Duration // 0 => defaultIDEComptimeWallTimeout; < 0 => disable wall-clock timeout
}

// IDECacheStats reports hit/miss counters for whole-program IDE checks and per-package const memoization.
type IDECacheStats struct {
	ProgHits    int64
	ProgMisses  int64
	ConstHits   int64
	ConstMisses int64
}

type ideProgBucketKey struct {
	entryAbs     string
	projectRoot  string
	targetTriple string
	testMode     bool
	stdDirOpt    string
	ideTimeout   time.Duration
}

type ideCachedFileHash struct {
	path   string
	sha256 string
	source string
}

type ideCachedPkgDir struct {
	dir          string
	includeTests bool
	diskFiles    []string
	overlayFiles []string
}

type ideProgEntry struct {
	key                  ideProgBucketKey
	discoveredRoot       string
	resolvedStdDir       string
	resolvedTargetTriple string
	loadedFiles          []ideCachedFileHash
	loadedDirs           []ideCachedPkgDir
	manifestFiles        []ideCachedFileHash
	missedDirs           []string
	diagnostics          []diag.Diagnostic
	prog                 *CheckedProgram
}

type pkgConstKey struct {
	pkgFP   string
	symName string
}

type cachedConstVal struct {
	isRange     bool
	rngLow      uint64
	rngHigh     uint64
	rngInc      bool
	rngPrimKind types.PrimKind
	sd          *StaticData
}

type pkgConstEntry struct {
	isComptimeOnly  bool
	typeStr         string
	val             *cachedConstVal
	embeds          []EmbeddedFileInfo
	attemptedEmbeds []string
}

var (
	ideProgMu    sync.Mutex
	ideProgCache []*ideProgEntry

	pkgConstMu    sync.Mutex
	pkgConstCache = make(map[pkgConstKey]pkgConstEntry)

	statProgHits    atomic.Int64
	statProgMisses  atomic.Int64
	statConstHits   atomic.Int64
	statConstMisses atomic.Int64
)

// GetIDECacheStats returns cumulative cache hit and miss counts for IDE memoization.
func GetIDECacheStats() IDECacheStats {
	return IDECacheStats{
		ProgHits:    statProgHits.Load(),
		ProgMisses:  statProgMisses.Load(),
		ConstHits:   statConstHits.Load(),
		ConstMisses: statConstMisses.Load(),
	}
}

// ResetIDECaches clears the in-memory IDE program cache, per-package const cache, and stats counters.
func ResetIDECaches() {
	ideProgMu.Lock()
	ideProgCache = nil
	ideProgMu.Unlock()

	pkgConstMu.Lock()
	pkgConstCache = make(map[pkgConstKey]pkgConstEntry)
	pkgConstMu.Unlock()

	statProgHits.Store(0)
	statProgMisses.Store(0)
	statConstHits.Store(0)
	statConstMisses.Store(0)
}

func normCleanAbs(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

func sha256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func sha256HexString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func lookupOverlayByPath(overlays map[string]string, target string) (string, bool) {
	if len(overlays) == 0 || target == "" {
		return "", false
	}
	cleanTarget := filepath.Clean(target)
	if v, ok := overlays[cleanTarget]; ok {
		return v, true
	}
	absTarget := normCleanAbs(cleanTarget)
	if v, ok := overlays[absTarget]; ok {
		return v, true
	}
	canonTarget, hasCanon := evalSymlinksBestEffort(cleanTarget)
	for k, v := range overlays {
		cleanK := filepath.Clean(k)
		if equalPathOS(cleanK, cleanTarget) || equalPathOS(normCleanAbs(cleanK), absTarget) {
			return v, true
		}
		if hasCanon {
			if equalPathOS(cleanK, canonTarget) {
				return v, true
			}
			if canonK, ok := evalSymlinksBestEffort(cleanK); ok && equalPathOS(canonK, canonTarget) {
				return v, true
			}
		}
	}
	return "", false
}

func currentFileHashAndText(overlays map[string]string, path string) (digest, text string, exists bool) {
	if s, ok := lookupOverlayByPath(overlays, path); ok {
		return sha256HexString(s), s, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	text = string(data)
	return sha256HexBytes(data), text, true
}

func currentFileExists(overlays map[string]string, path string) bool {
	if _, ok := lookupOverlayByPath(overlays, path); ok {
		return true
	}
	_, err := os.Stat(path)
	return err == nil
}

func normalizeIDETimeout(d time.Duration) time.Duration {
	if d == 0 {
		return defaultIDEComptimeWallTimeout
	}
	if d < 0 {
		return -1
	}
	return d
}

func normalizeTargetKey(triple string) string {
	if strings.TrimSpace(triple) == "" {
		return "default:" + manifest.ParseTarget("").Triple
	}
	ti := manifest.ParseTarget(triple)
	if ti.Triple != "" {
		return ti.Triple
	}
	return strings.TrimSpace(triple)
}

func collectOverlayFilesInDir(overlays map[string]string, pkgDir string, target manifest.TargetInfo, includeTests bool) []string {
	if len(overlays) == 0 || pkgDir == "" {
		return nil
	}
	cleanDir := normCleanAbs(pkgDir)
	canonDir, hasCanonDir := evalSymlinksBestEffort(cleanDir)
	seen := make(map[string]bool)
	var out []string
	for k := range overlays {
		absK := normCleanAbs(k)
		base := filepath.Base(absK)
		if !isPackageSourceFileName(base, target, includeTests) {
			continue
		}
		dirK := filepath.Dir(absK)
		match := equalPathOS(dirK, cleanDir)
		if !match && hasCanonDir {
			if canonKDir, ok := evalSymlinksBestEffort(dirK); ok && equalPathOS(canonKDir, canonDir) {
				match = true
			}
		}
		if match && !seen[absK] {
			seen[absK] = true
			out = append(out, absK)
		}
	}
	sort.Strings(out)
	return out
}

func collectManifestFilePaths(ctx *manifest.ProjectContext) []string {
	if ctx == nil {
		return nil
	}
	seen := make(map[string]bool)
	var paths []string
	add := func(p string) {
		p = normCleanAbs(p)
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	if ctx.ActiveManifest != nil {
		add(ctx.ActiveManifest.Path)
	}
	if ctx.WorkspaceManifest != nil {
		add(ctx.WorkspaceManifest.Path)
	}
	add(ctx.LockPath())
	for _, m := range ctx.WorkspaceMembers {
		if m != nil {
			add(m.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

// CheckForIDEWithOptions runs tolerant, memoized semantic analysis for LSP / IDE tooling.
func CheckForIDEWithOptions(opts CheckIDEOptions, rep *diag.Reporter) (prog *CheckedProgram) {
	defer func() {
		if r := recover(); r != nil && prog == nil {
			prog = &CheckedProgram{
				PackageByID:    make(map[string]*Package),
				StructDecls:    make(map[*types.Struct]*ast.StructDecl),
				StructMods:     make(map[*types.Struct]*Module),
				EnumDecls:      make(map[*types.Enum]*ast.EnumDecl),
				EnumMods:       make(map[*types.Enum]*Module),
				InterfaceMods:  make(map[*types.Interface]*Module),
				DistinctMods:   make(map[*types.Distinct]*Module),
				VTables:        make(map[string]*VTableImpl),
				TopBodyMaps:    NewBodyMaps(),
				StructBodyMaps: make(map[*types.Struct]*BodyMaps),
				EnumBodyMaps:   make(map[*types.Enum]*BodyMaps),
				MethodTable:    make(map[string]map[string]*FuncInstance),
				GenMethods:     make(map[string]map[string]*GenericFuncTemplate),
				TypeSpans:      make(map[types.Type]diag.Span),
			}
		}
	}()
	if rep == nil {
		rep = diag.NewReporter()
	}
	normOverlays := normalizeOverlays(opts.Overlays)
	entryAbs := normCleanAbs(opts.EntryPath)
	projectRootAbs := normCleanAbs(opts.ProjectRoot)
	normStd := opts.StdDir
	if normStd != "" {
		normStd = normCleanAbs(normStd)
	}
	bucketKey := ideProgBucketKey{
		entryAbs:     entryAbs,
		projectRoot:  projectRootAbs,
		targetTriple: normalizeTargetKey(opts.TargetTriple),
		testMode:     opts.TestMode,
		stdDirOpt:    normStd,
		ideTimeout:   normalizeIDETimeout(opts.IDEComptimeTimeout),
	}

	if cached := lookupIDEProgCache(bucketKey, normOverlays); cached != nil {
		statProgHits.Add(1)
		for _, lf := range cached.loadedFiles {
			rep.AddSource(lf.path, lf.source)
		}
		for _, d := range cached.diagnostics {
			rep.Report(cloneDiagnostic(d))
		}
		return cached.prog
	}
	statProgMisses.Add(1)

	checkedProg, a := runCheckWithOptionsAndAnalyzer(CheckOptions{
		EntryPath:          opts.EntryPath,
		Overlays:           normOverlays,
		ProjectRoot:        opts.ProjectRoot,
		StdDir:             opts.StdDir,
		TargetTriple:       opts.TargetTriple,
		TestMode:           opts.TestMode,
		IDEMode:            true,
		IDEComptimeTimeout: opts.IDEComptimeTimeout,
	}, rep)
	if checkedProg != nil && a != nil && !a.hasUnresolvedImport && len(a.loadedFileSources) > 0 {
		storeIDEProgCache(bucketKey, checkedProg, a, normOverlays, rep.Diagnostics())
	}
	return checkedProg
}

func cloneDiagnostic(d diag.Diagnostic) diag.Diagnostic {
	if len(d.Related) > 0 {
		d.Related = slices.Clone(d.Related)
	}
	return d
}

func lookupIDEProgCache(key ideProgBucketKey, overlays map[string]string) *ideProgEntry {
	ideProgMu.Lock()
	defer ideProgMu.Unlock()

	for i := len(ideProgCache) - 1; i >= 0; i-- {
		ent := ideProgCache[i]
		if ent.key != key {
			continue
		}
		if !ideEntryValid(ent, overlays) {
			ideProgCache = append(ideProgCache[:i], ideProgCache[i+1:]...)
			continue
		}
		// Move hit entry to MRU back.
		if i != len(ideProgCache)-1 {
			copy(ideProgCache[i:], ideProgCache[i+1:])
			ideProgCache[len(ideProgCache)-1] = ent
		}
		return ent
	}
	return nil
}

func ideEntryValid(ent *ideProgEntry, overlays map[string]string) bool {
	// Check root discovery in case a lowlight.toml was created or removed.
	startDir := filepath.Dir(ent.key.entryAbs)
	if startDir == "" || startDir == "." {
		startDir = "."
	}
	curRoot := ent.key.projectRoot
	if curRoot == "" {
		curRoot = findProjectRoot(startDir)
	}
	if curRoot != ent.discoveredRoot {
		return false
	}
	if ent.key.stdDirOpt == "" {
		if findStdDir(curRoot, startDir) != ent.resolvedStdDir {
			return false
		}
	}

	// Verify all manifest / lock files.
	for _, mf := range ent.manifestFiles {
		sha, _, exists := currentFileHashAndText(overlays, mf.path)
		if !exists {
			sha = ""
		}
		if sha != mf.sha256 {
			return false
		}
	}

	// Verify every loaded package directory's disk and overlay file membership.
	target := manifest.ParseTarget(ent.resolvedTargetTriple)
	for _, ld := range ent.loadedDirs {
		diskNow := scanPackageDirFiles(ld.dir, target, ld.includeTests)
		if !slices.Equal(diskNow, ld.diskFiles) {
			return false
		}
		ovNow := collectOverlayFilesInDir(overlays, ld.dir, target, ld.includeTests)
		if !slices.Equal(ovNow, ld.overlayFiles) {
			return false
		}
	}

	// Verify every loaded source file hash.
	for _, lf := range ent.loadedFiles {
		sha, _, exists := currentFileHashAndText(overlays, lf.path)
		if !exists || sha != lf.sha256 {
			return false
		}
	}

	// Verify embedded files and previously-missing embed targets.
	for _, ef := range ent.prog.EmbeddedFiles {
		sha, _, exists := currentFileHashAndText(overlays, ef.Path)
		if !exists || sha != ef.SHA256 {
			return false
		}
	}
	for _, att := range ent.prog.AttemptedEmbedPaths {
		if currentFileExists(overlays, att) {
			return false
		}
	}

	// Verify previously-missing import directories did not materialize on disk.
	for _, md := range ent.missedDirs {
		if dirExists(md) {
			return false
		}
	}
	return true
}

func storeIDEProgCache(key ideProgBucketKey, prog *CheckedProgram, a *Analyzer, overlays map[string]string, diags []diag.Diagnostic) {
	filePaths := make([]string, 0, len(a.loadedFileSources))
	for p := range a.loadedFileSources {
		filePaths = append(filePaths, p)
	}
	sort.Strings(filePaths)
	loadedFiles := make([]ideCachedFileHash, len(filePaths))
	for i, p := range filePaths {
		src := a.loadedFileSources[p]
		loadedFiles[i] = ideCachedFileHash{
			path:   p,
			sha256: sha256HexString(src),
			source: src,
		}
	}

	loadedDirs := make([]ideCachedPkgDir, 0, len(a.loadedPkgDirs))
	for _, ld := range a.loadedPkgDirs {
		loadedDirs = append(loadedDirs, ideCachedPkgDir{
			dir:          ld.dir,
			includeTests: ld.includeTests,
			diskFiles:    scanPackageDirFiles(ld.dir, a.target, ld.includeTests),
			overlayFiles: collectOverlayFilesInDir(overlays, ld.dir, a.target, ld.includeTests),
		})
	}

	manPaths := collectManifestFilePaths(prog.ManifestCtx)
	manifestFiles := make([]ideCachedFileHash, len(manPaths))
	for i, mp := range manPaths {
		sha, _, exists := currentFileHashAndText(overlays, mp)
		if !exists {
			sha = ""
		}
		manifestFiles[i] = ideCachedFileHash{
			path:   mp,
			sha256: sha,
		}
	}

	clonedDiags := make([]diag.Diagnostic, len(diags))
	for i, d := range diags {
		clonedDiags[i] = cloneDiagnostic(d)
	}

	ent := &ideProgEntry{
		key:                  key,
		discoveredRoot:       normCleanAbs(a.projectRoot),
		resolvedStdDir:       normCleanAbs(prog.StdDir),
		resolvedTargetTriple: a.target.Triple,
		loadedFiles:          loadedFiles,
		loadedDirs:     loadedDirs,
		manifestFiles:  manifestFiles,
		missedDirs:     slices.Clone(a.missedImportDirs),
		diagnostics:    clonedDiags,
		prog:           prog,
	}

	ideProgMu.Lock()
	defer ideProgMu.Unlock()
	// Replace any older entry with the exact same bucket key so stale edit snapshots are dropped promptly.
	for i := len(ideProgCache) - 1; i >= 0; i-- {
		if ideProgCache[i].key == key {
			ideProgCache = append(ideProgCache[:i], ideProgCache[i+1:]...)
		}
	}
	if len(ideProgCache) >= ideProgCacheMaxEntries {
		ideProgCache = ideProgCache[1:]
	}
	ideProgCache = append(ideProgCache, ent)
}

// -----------------------------------------------------------------------------
// Per-Package Const Memoization (A10 / H14)
// -----------------------------------------------------------------------------

func (a *Analyzer) packageFingerprint(pkg *Package) string {
	if a == nil || pkg == nil {
		return ""
	}
	if a.pkgFingerprints == nil {
		a.pkgFingerprints = make(map[*Package]string)
	}
	if fp, ok := a.pkgFingerprints[pkg]; ok {
		return fp
	}
	visiting := make(map[*Package]bool)
	return a.packageFingerprintVisited(pkg, visiting)
}

func (a *Analyzer) packageFingerprintVisited(pkg *Package, visiting map[*Package]bool) string {
	if fp, ok := a.pkgFingerprints[pkg]; ok {
		return fp
	}
	if visiting[pkg] {
		return "cycle:" + pkg.ID
	}
	visiting[pkg] = true
	defer delete(visiting, pkg)

	h := sha256.New()
	writeField := func(k, v string) {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(v))
		_, _ = h.Write([]byte{0})
	}
	writeField("id", pkg.ID)
	writeField("name", pkg.Name)
	writeField("target", a.target.Triple)
	if a.testMode {
		writeField("test", "1")
	} else {
		writeField("test", "0")
	}
	if pkg.IsStd {
		writeField("std", "1")
	}
	if pkg.IsCore {
		writeField("core", "1")
	}
	if pkg.IsEntry {
		writeField("entry", "1")
	}

	mods := slices.Clone(pkg.Files)
	sort.Slice(mods, func(i, j int) bool {
		return mods[i].Path < mods[j].Path
	})
	childSet := make(map[*Package]bool)
	for _, m := range mods {
		if m == nil {
			continue
		}
		normPath := normCleanAbs(m.Path)
		writeField("file", normPath)
		if src, ok := a.loadedFileSources[normPath]; ok {
			writeField("sha", sha256HexString(src))
		} else {
			sha, _, exists := currentFileHashAndText(a.overlays, m.Path)
			if exists {
				writeField("sha", sha)
			} else {
				writeField("sha", "missing")
			}
		}
		for _, imp := range m.Imports {
			if imp != nil && imp.Pkg != nil && imp.Pkg != pkg {
				childSet[imp.Pkg] = true
			}
		}
		for _, imp := range m.Aliased {
			if imp != nil && imp.Pkg != nil && imp.Pkg != pkg {
				childSet[imp.Pkg] = true
			}
		}
	}

	children := make([]*Package, 0, len(childSet))
	for ch := range childSet {
		children = append(children, ch)
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].ID != children[j].ID {
			return children[i].ID < children[j].ID
		}
		return children[i].Dir < children[j].Dir
	})
	for _, ch := range children {
		writeField("dep", ch.ID+"="+a.packageFingerprintVisited(ch, visiting))
	}

	fp := hex.EncodeToString(h.Sum(nil))
	a.pkgFingerprints[pkg] = fp
	return fp
}

func dehydrateStaticData(sd *StaticData) (*StaticData, bool) {
	if sd == nil {
		return nil, false
	}
	if sd.Kind == StaticFn || sd.Fn != nil || sd.Lambda != nil {
		return nil, false
	}
	out := &StaticData{
		Kind:        sd.Kind,
		Uint:        sd.Uint,
		SignedVal:   sd.SignedVal,
		IsSigned:    sd.IsSigned,
		Float:       sd.Float,
		Bool:        sd.Bool,
		Char:        sd.Char,
		Str:         sd.Str,
		HasRawBytes: sd.HasRawBytes,
		VariantIdx:  sd.VariantIdx,
		HasOpt:      sd.HasOpt,
	}
	if len(sd.RawBytes) > 0 {
		out.RawBytes = slices.Clone(sd.RawBytes)
	}
	if len(sd.Elems) > 0 {
		out.Elems = make([]*StaticData, len(sd.Elems))
		for i, el := range sd.Elems {
			cel, ok := dehydrateStaticData(el)
			if !ok {
				return nil, false
			}
			out.Elems[i] = cel
		}
	}
	if len(sd.Fields) > 0 {
		out.Fields = make(map[string]*StaticData, len(sd.Fields))
		for k, fv := range sd.Fields {
			cfv, ok := dehydrateStaticData(fv)
			if !ok {
				return nil, false
			}
			out.Fields[k] = cfv
		}
	}
	if sd.Payload != nil {
		cp, ok := dehydrateStaticData(sd.Payload)
		if !ok {
			return nil, false
		}
		out.Payload = cp
	}
	if sd.OptElem != nil {
		co, ok := dehydrateStaticData(sd.OptElem)
		if !ok {
			return nil, false
		}
		out.OptElem = co
	}
	return out, true
}

func dehydrateConstVal(val *ctVal, ty types.Type) (*cachedConstVal, bool) {
	if val == nil || ty == nil {
		return nil, false
	}
	if val.kind == ctRange && val.rng != nil {
		var pk types.PrimKind
		if val.rng.prim != nil {
			pk = val.rng.prim.Kind
		}
		return &cachedConstVal{
			isRange:     true,
			rngLow:      val.rng.low,
			rngHigh:     val.rng.high,
			rngInc:      val.rng.inclusive,
			rngPrimKind: pk,
		}, true
	}
	sd := exportCtVal(val, ty)
	clean, ok := dehydrateStaticData(sd)
	if !ok {
		return nil, false
	}
	return &cachedConstVal{sd: clean}, true
}

func unwrapDistinctType(t types.Type) types.Type {
	for {
		dt, ok := t.(*types.Distinct)
		if !ok || dt == nil {
			return t
		}
		t = dt.Base
	}
}

func hydrateStaticToCtVal(st *ctState, sd *StaticData, ty types.Type, span diag.Span) (*ctVal, bool) {
	if st == nil || st.a == nil || sd == nil || ty == nil {
		return nil, false
	}
	baseTy := unwrapDistinctType(ty)
	switch sd.Kind {
	case StaticVoid:
		v := newCtVoid()
		v.ty = ty
		return v, true
	case StaticBool:
		v := newCtBool(sd.Bool)
		v.ty = ty
		return v, true
	case StaticChar:
		v := newCtChar(sd.Char)
		v.ty = ty
		return v, true
	case StaticString:
		v := newCtString(sd.Str)
		v.ty = ty
		return v, true
	case StaticInt:
		prim := types.UnderlyingPrimitive(baseTy)
		if prim == nil {
			if en, ok := baseTy.(*types.Enum); ok && !en.IsTagged {
				if en.Underlying != nil {
					prim = types.UnderlyingPrimitive(en.Underlying)
				}
				if prim == nil {
					prim = types.TypeInt
				}
			}
		}
		if prim == nil {
			return nil, false
		}
		v := newCtInt(sd.Uint, prim)
		v.ty = ty
		return v, true
	case StaticFloat:
		prim := types.UnderlyingPrimitive(baseTy)
		if prim == nil {
			return nil, false
		}
		v := newCtFloat(sd.Float, prim)
		v.ty = ty
		return v, true
	case StaticList:
		if sd.HasRawBytes {
			v, sig := st.makeByteListStruct(sd.RawBytes, span)
			if sig.kind != sigNone || v == nil {
				return nil, false
			}
			v.ty = ty
			return v, true
		}
		var elemTy types.Type
		if listSt, ok := st.a.isListStruct(baseTy); ok && len(listSt.TypeArgs) == 1 {
			elemTy = listSt.TypeArgs[0]
		} else if vec, ok := baseTy.(*types.Vector); ok {
			elemTy = vec.Elem
		} else {
			return nil, false
		}
		elems := make([]*ctVal, len(sd.Elems))
		for i, el := range sd.Elems {
			hv, ok := hydrateStaticToCtVal(st, el, elemTy, span)
			if !ok {
				return nil, false
			}
			elems[i] = hv
		}
		v, sig := st.makeListStruct(baseTy, elems, span)
		if sig.kind != sigNone || v == nil {
			return nil, false
		}
		v.ty = ty
		return v, true
	case StaticArray:
		sa, ok := baseTy.(*types.StackArray)
		if !ok || sa == nil {
			return nil, false
		}
		elems := make([]*ctVal, len(sd.Elems))
		for i, el := range sd.Elems {
			hv, ok := hydrateStaticToCtVal(st, el, sa.Elem, span)
			if !ok {
				return nil, false
			}
			elems[i] = hv
		}
		return &ctVal{kind: ctArray, ty: ty, arr: &ctArrayBox{elemTy: sa.Elem, elems: elems}}, true
	case StaticTuple:
		tup, ok := baseTy.(*types.Tuple)
		if !ok || tup == nil || len(tup.Elems) != len(sd.Elems) {
			return nil, false
		}
		elems := make([]*ctVal, len(sd.Elems))
		for i, el := range sd.Elems {
			hv, ok := hydrateStaticToCtVal(st, el, tup.Elems[i], span)
			if !ok {
				return nil, false
			}
			elems[i] = hv
		}
		return &ctVal{kind: ctTuple, ty: ty, tup: &ctTupleBox{elems: elems}}, true
	case StaticStruct:
		sTy, ok := baseTy.(*types.Struct)
		if !ok || sTy == nil {
			return nil, false
		}
		if !st.a.ensureStructFieldsForLayout(sTy, span) {
			return nil, false
		}
		fields := make(map[string]*ctVal, len(sTy.Fields))
		order := make([]string, len(sTy.Fields))
		for i, f := range sTy.Fields {
			order[i] = f.Name
			sf, hasF := sd.Fields[f.Name]
			if !hasF {
				return nil, false
			}
			hv, ok := hydrateStaticToCtVal(st, sf, f.Type, span)
			if !ok {
				return nil, false
			}
			fields[f.Name] = hv
		}
		return &ctVal{
			kind: ctStruct,
			ty:   ty,
			st:   &ctStructBox{ty: sTy, fields: fields, fieldOrder: order},
		}, true
	case StaticEnum:
		en, ok := baseTy.(*types.Enum)
		if !ok || en == nil || sd.VariantIdx < 0 || sd.VariantIdx >= len(en.Variants) {
			return nil, false
		}
		if !en.IsTagged {
			u := en.Underlying
			if u == nil {
				u = types.TypeInt
			}
			v := newCtInt(uint64(sd.VariantIdx), types.UnderlyingPrimitive(u))
			v.ty = ty
			return v, true
		}
		vr := en.Variants[sd.VariantIdx]
		var payload *ctVal
		if vr.Kind == types.VariantTuple && vr.Payload != nil {
			if sd.Payload == nil {
				return nil, false
			}
			hv, ok := hydrateStaticToCtVal(st, sd.Payload, vr.Payload, span)
			if !ok {
				return nil, false
			}
			payload = hv
		}
		return &ctVal{
			kind:       ctEnum,
			ty:         ty,
			en:         en,
			variantIdx: sd.VariantIdx,
			payload:    payload,
		}, true
	case StaticOpt:
		optTy, ok := baseTy.(*types.Optional)
		if !ok || optTy == nil {
			return nil, false
		}
		if !sd.HasOpt {
			return newCtNone(ty), true
		}
		inner, ok := hydrateStaticToCtVal(st, sd.OptElem, optTy.Elem, span)
		if !ok {
			return nil, false
		}
		return newCtSome(ty, inner), true
	default:
		return nil, false
	}
}

func hydrateConstVal(a *Analyzer, mod *Module, cached *cachedConstVal, ty types.Type, span diag.Span) (*ctVal, bool) {
	if a == nil || cached == nil || ty == nil {
		return nil, false
	}
	if cached.isRange {
		prim := primitiveFromKind(cached.rngPrimKind)
		if prim == nil {
			prim = types.TypeInt
		}
		return &ctVal{
			kind: ctRange,
			ty:   ty,
			rng: &ctRangeBox{
				low:       cached.rngLow,
				high:      cached.rngHigh,
				inclusive: cached.rngInc,
				prim:      prim,
			},
		}, true
	}
	if cached.sd == nil {
		return nil, false
	}
	st := newCtState(a, mod, 0)
	v, ok := hydrateStaticToCtVal(st, cached.sd, ty, span)
	if !ok || v == nil {
		return nil, false
	}
	freezeCtVal(v)
	return v, true
}

func primitiveFromKind(k types.PrimKind) *types.Primitive {
	switch k {
	case types.PrimI8:
		return types.TypeI8
	case types.PrimI16:
		return types.TypeI16
	case types.PrimI32:
		return types.TypeI32
	case types.PrimI64:
		return types.TypeInt
	case types.PrimU8:
		return types.TypeU8
	case types.PrimU16:
		return types.TypeU16
	case types.PrimU32:
		return types.TypeU32
	case types.PrimU64:
		return types.TypeU64
	default:
		return nil
	}
}

func (a *Analyzer) lookupCachedPkgConst(tv *TopVarInit) (*ctVal, bool) {
	if a == nil || !a.ideMode || tv == nil || tv.Decl == nil || !tv.Decl.IsConst {
		return nil, false
	}
	if tv.Sym == nil || tv.Sym.Type == nil || tv.Mod == nil || tv.Mod.Pkg == nil {
		return nil, false
	}
	fp := a.packageFingerprint(tv.Mod.Pkg)
	key := pkgConstKey{pkgFP: fp, symName: tv.Sym.Name}

	pkgConstMu.Lock()
	ent, ok := pkgConstCache[key]
	pkgConstMu.Unlock()
	if !ok {
		statConstMisses.Add(1)
		return nil, false
	}
	if ent.isComptimeOnly != tv.Sym.ComptimeOnly || ent.typeStr != tv.Sym.Type.String() {
		statConstMisses.Add(1)
		return nil, false
	}
	for _, ef := range ent.embeds {
		sha, _, exists := currentFileHashAndText(a.overlays, ef.Path)
		if !exists || sha != ef.SHA256 {
			pkgConstMu.Lock()
			delete(pkgConstCache, key)
			pkgConstMu.Unlock()
			statConstMisses.Add(1)
			return nil, false
		}
	}
	for _, att := range ent.attemptedEmbeds {
		if currentFileExists(a.overlays, att) {
			pkgConstMu.Lock()
			delete(pkgConstCache, key)
			pkgConstMu.Unlock()
			statConstMisses.Add(1)
			return nil, false
		}
	}

	val, ok := hydrateConstVal(a, tv.Mod, ent.val, tv.Sym.Type, tv.Decl.Span)
	if !ok || val == nil {
		statConstMisses.Add(1)
		return nil, false
	}
	for _, ef := range ent.embeds {
		a.recordEmbeddedFileHash(ef.Path, ef.SHA256)
	}
	for _, att := range ent.attemptedEmbeds {
		a.recordAttemptedEmbed(att)
	}
	statConstHits.Add(1)
	return val, true
}

func (a *Analyzer) storeCachedPkgConst(tv *TopVarInit, val *ctVal, embeds []EmbeddedFileInfo, attempted []string) {
	if a == nil || !a.ideMode || tv == nil || tv.Decl == nil || !tv.Decl.IsConst {
		return
	}
	if tv.Sym == nil || tv.Sym.Type == nil || tv.Mod == nil || tv.Mod.Pkg == nil || val == nil {
		return
	}
	dehydrated, ok := dehydrateConstVal(val, tv.Sym.Type)
	if !ok {
		return
	}
	fp := a.packageFingerprint(tv.Mod.Pkg)
	key := pkgConstKey{pkgFP: fp, symName: tv.Sym.Name}

	ent := pkgConstEntry{
		isComptimeOnly:  tv.Sym.ComptimeOnly,
		typeStr:         tv.Sym.Type.String(),
		val:             dehydrated,
		embeds:          dedupeEmbeddedInfos(embeds),
		attemptedEmbeds: dedupeStrings(attempted),
	}

	pkgConstMu.Lock()
	if len(pkgConstCache) >= pkgConstCacheMax {
		pkgConstCache = make(map[pkgConstKey]pkgConstEntry)
	}
	pkgConstCache[key] = ent
	pkgConstMu.Unlock()
}

func dedupeEmbeddedInfos(in []EmbeddedFileInfo) []EmbeddedFileInfo {
	if len(in) == 0 {
		return nil
	}
	idx := make(map[string]int, len(in))
	out := make([]EmbeddedFileInfo, 0, len(in))
	for _, ef := range in {
		if pos, ok := idx[ef.Path]; ok {
			out[pos].SHA256 = ef.SHA256
			continue
		}
		idx[ef.Path] = len(out)
		out = append(out, ef)
	}
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
