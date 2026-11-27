package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"lowlight/internal/codegen"
	"lowlight/internal/ir"
	"lowlight/internal/manifest"
	"lowlight/internal/sema"
)

// BuildStats reports two-phase incremental package compilation metrics (`SPECIFICATION.md §12.6`).
type BuildStats struct {
	TwoPhase       bool
	ObjectCacheDir string
	PackageUnits   int
	CacheHits      int
	CacheMisses    int
}

func resolveObjCacheDir(opts Options, ctx *manifest.ProjectContext) string {
	if opts.CacheRoot != "" {
		return filepath.Join(opts.CacheRoot, "obj")
	}
	if ctx != nil && ctx.RootDir() != "" {
		return filepath.Join(ctx.RootDir(), ".lowlight", "cache", "obj")
	}
	return filepath.Join(manifest.DefaultCacheRoot(), "obj")
}

// filterCompileFlags strips linker-only flags so `cc -c` compiles cleanly under `-Werror`.
func filterCompileFlags(flags []string) []string {
	var out []string
	skipNext := false
	for _, f := range flags {
		if skipNext {
			skipNext = false
			continue
		}
		if f == "-framework" || f == "-rpath" || f == "-L" || f == "-l" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(f, "-l") || strings.HasPrefix(f, "-L") || strings.HasPrefix(f, "-Wl,") {
			continue
		}
		out = append(out, f)
	}
	return out
}

var (
	ccVersionMu    sync.Mutex
	ccVersionCache = map[string]string{}
)

func ccVersionFingerprint(cc string) string {
	ccVersionMu.Lock()
	if fp, ok := ccVersionCache[cc]; ok {
		ccVersionMu.Unlock()
		return fp
	}
	ccVersionMu.Unlock()

	out, err := exec.Command(cc, "--version").CombinedOutput()
	fp := "unknown"
	if err == nil {
		fp = strings.TrimSpace(string(out))
	}
	ccVersionMu.Lock()
	ccVersionCache[cc] = fp
	ccVersionMu.Unlock()
	return fp
}

func collectObjCacheInputs(checked *sema.CheckedProgram, compileFlags []string) []string {
	if checked == nil {
		return nil
	}
	var inputs []string

	// Include hashes of all comptime-embedded files.
	embeds := append([]sema.EmbeddedFileInfo(nil), checked.EmbeddedFiles...)
	sort.Slice(embeds, func(i, j int) bool {
		return embeds[i].Path < embeds[j].Path
	})
	for _, ef := range embeds {
		inputs = append(inputs, fmt.Sprintf("embed:%s=%s", ef.Path, ef.SHA256))
	}

	// Include resolved external header hashes for local headers (and names for system headers).
	searchDirs := make([]string, 0, len(checked.Modules)+4)
	seenDir := map[string]bool{}
	addDir := func(d string) {
		if d == "" {
			return
		}
		clean := filepath.Clean(d)
		if !seenDir[clean] {
			seenDir[clean] = true
			searchDirs = append(searchDirs, clean)
		}
	}
	for _, m := range checked.Modules {
		if m != nil && m.Path != "" {
			addDir(filepath.Dir(m.Path))
		}
	}
	for i := 0; i < len(compileFlags); i++ {
		f := compileFlags[i]
		if f == "-I" && i+1 < len(compileFlags) {
			addDir(compileFlags[i+1])
			i++
		} else if strings.HasPrefix(f, "-I") && len(f) > 2 {
			addDir(f[2:])
		}
	}
	hdrs := append([]string(nil), checked.ExternHeaders...)
	sort.Strings(hdrs)
	seenHdr := map[string]bool{}
	for _, hdr := range hdrs {
		if hdr == "" || seenHdr[hdr] {
			continue
		}
		seenHdr[hdr] = true
		var digest string
		if filepath.IsAbs(hdr) {
			if b, err := os.ReadFile(hdr); err == nil {
				sum := sha256.Sum256(b)
				digest = hex.EncodeToString(sum[:])
			}
		} else {
			for _, d := range searchDirs {
				cand := filepath.Join(d, hdr)
				if b, err := os.ReadFile(cand); err == nil {
					sum := sha256.Sum256(b)
					digest = hex.EncodeToString(sum[:])
					break
				}
			}
		}
		if digest != "" {
			inputs = append(inputs, fmt.Sprintf("hdr:%s=%s", hdr, digest))
		} else {
			inputs = append(inputs, "hdr:"+hdr)
		}
	}
	return inputs
}

func computeObjCacheKey(backend BackendKind, cc, ccVer, targetTriple string, release, testMode bool, compileFlags, extraInputs []string, pu codegen.PackageUnit) string {
	h := sha256.New()
	fmt.Fprintf(h, "lowlight-obj-v2\ncompiler=%s\nbackend=%s\ncc=%s\nccver=%s\ntarget=%s\nrelease=%v\ntest=%v\npkg=%s\n",
		Version, backend, cc, ccVer, targetTriple, release, testMode, pu.PkgID)
	for _, f := range compileFlags {
		h.Write([]byte("flag="))
		h.Write([]byte(f))
		h.Write([]byte{'\n'})
	}
	for _, in := range extraInputs {
		h.Write([]byte("input="))
		h.Write([]byte(in))
		h.Write([]byte{'\n'})
	}
	h.Write([]byte("---\n"))
	h.Write([]byte(pu.Source))
	return hex.EncodeToString(h.Sum(nil))
}

func buildTwoPhase(
	backend BackendKind,
	opts Options,
	ctx *manifest.ProjectContext,
	checked *sema.CheckedProgram,
	mirProg *ir.Program,
	absOut string,
) (BuildStats, error) {
	var stats BuildStats

	var split codegen.SplitUnits
	if backend == BackendLLVM {
		split = codegen.EmitLLVMSplit(mirProg)
	} else {
		split = codegen.EmitCSplit(mirProg)
	}
	if len(split.PackageUnits) == 0 {
		return stats, nil
	}

	cc := ResolveCC(opts.CC)
	if cc == "cc" {
		if clangPath, err := exec.LookPath("clang"); err == nil && clangPath != "" {
			cc = clangPath
		}
	}
	if backend == BackendC11 && opts.TargetTriple != "" {
		baseCC := strings.ToLower(filepath.Base(cc))
		if strings.Contains(baseCC, "gcc") && !strings.Contains(baseCC, "clang") {
			return stats, fmt.Errorf("--target=%s requires clang (current C compiler is %s; pass --cc=clang)", opts.TargetTriple, cc)
		}
	}

	tmpDir, err := os.MkdirTemp("", "lowlight-split-build-*")
	if err != nil {
		return stats, fmt.Errorf("failed to create temp build directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mFlags, mLibs := collectManifestLinkFlags(ctx, checked.ResolvedDeps, checked.Target)
	allFlags := make([]string, 0, len(mFlags)+len(opts.ExtraCFlags))
	allFlags = append(allFlags, mFlags...)
	allFlags = append(allFlags, opts.ExtraCFlags...)
	compileFlags := filterCompileFlags(allFlags)

	cacheDir := resolveObjCacheDir(opts, ctx)
	cacheWritable := os.MkdirAll(cacheDir, 0o755) == nil

	stats.TwoPhase = true
	stats.ObjectCacheDir = cacheDir
	stats.PackageUnits = len(split.PackageUnits)

	baseCompileArgs := func() []string {
		var a []string
		if backend == BackendLLVM {
			a = append(a, "-Wno-override-module")
		} else {
			a = append(a, "-std=c11", "-fno-strict-aliasing") // see buildC11
		}
		if opts.TargetTriple != "" {
			a = append(a, "--target="+opts.TargetTriple)
		}
		if opts.Release {
			a = append(a, "-O2")
		} else {
			a = append(a, "-g", "-O0")
		}
		return a
	}

	srcExt := ".ll"
	if backend == BackendC11 {
		srcExt = ".c"
	}

	targetKey := opts.TargetTriple
	if checked != nil && checked.Target.Triple != "" {
		targetKey = checked.Target.Triple
	}
	ccVer := ccVersionFingerprint(cc)
	extraInputs := collectObjCacheInputs(checked, compileFlags)
	var objFiles []string
	for idx, pu := range split.PackageUnits {
		key := computeObjCacheKey(backend, cc, ccVer, targetKey, opts.Release, opts.TestMode, compileFlags, extraInputs, pu)
		cacheObj := filepath.Join(cacheDir, fmt.Sprintf("%s-%s.o", pu.PkgTag, key[:32]))
		if cacheWritable {
			if st, statErr := os.Stat(cacheObj); statErr == nil && !st.IsDir() && st.Size() > 0 {
				stats.CacheHits++
				objFiles = append(objFiles, cacheObj)
				continue
			}
		}

		stats.CacheMisses++
		unitSrcPath := filepath.Join(tmpDir, fmt.Sprintf("pkg_%d_%s%s", idx, pu.PkgTag, srcExt))
		if err := os.WriteFile(unitSrcPath, []byte(pu.Source), 0o644); err != nil {
			return stats, fmt.Errorf("failed to write Phase 1 unit %s: %w", pu.PkgID, err)
		}

		tmpObj := filepath.Join(tmpDir, fmt.Sprintf("pkg_%d_%s.o", idx, pu.PkgTag))
		args := baseCompileArgs()
		args = append(args, "-c", "-o", tmpObj, unitSrcPath)
		args = append(args, compileFlags...)

		var ccStderr bytes.Buffer
		cmd := exec.Command(cc, args...)
		cmd.Stderr = &ccStderr
		cmd.Stdout = &ccStderr
		if runErr := cmd.Run(); runErr != nil {
			return stats, fmt.Errorf("%s Phase 1 compilation failed for package %q (%s %s):\n%s\n%w",
				strings.ToUpper(string(backend)), pu.PkgID, cc, strings.Join(args, " "), strings.TrimSpace(ccStderr.String()), runErr)
		}

		finalObj := tmpObj
		if cacheWritable {
			// Atomically populate cache entry using a temp file inside cacheDir so concurrent builds never observe partial `.o` files.
			if stage, createErr := os.CreateTemp(cacheDir, ".tmp-obj-*.o"); createErr == nil {
				stagePath := stage.Name()
				rawObj, readErr := os.ReadFile(tmpObj)
				if readErr == nil {
					_, writeErr := stage.Write(rawObj)
					closeErr := stage.Close()
					if writeErr == nil && closeErr == nil {
						if renErr := os.Rename(stagePath, cacheObj); renErr == nil {
							finalObj = cacheObj
						} else {
							_ = os.Remove(stagePath)
						}
					} else {
						_ = os.Remove(stagePath)
					}
				} else {
					_ = stage.Close()
					_ = os.Remove(stagePath)
				}
			}
		}
		objFiles = append(objFiles, finalObj)
	}

	// Phase 2: Compile shared specialization/runtime/entry link unit and link with cached Phase 1 `.o` objects.
	linkSrcPath := filepath.Join(tmpDir, "program_link"+srcExt)
	if err := os.WriteFile(linkSrcPath, []byte(split.LinkUnit), 0o644); err != nil {
		return stats, fmt.Errorf("failed to write Phase 2 link unit: %w", err)
	}

	linkArgs := baseCompileArgs()
	linkArgs = append(linkArgs, "-o", absOut, linkSrcPath)
	linkArgs = append(linkArgs, objFiles...)
	linkArgs = append(linkArgs, allFlags...)
	linkArgs = append(linkArgs, mLibs...)
	linkArgs = append(linkArgs, targetSystemLibs(checked.Target)...)

	var linkStderr bytes.Buffer
	cmd := exec.Command(cc, linkArgs...)
	cmd.Stderr = &linkStderr
	cmd.Stdout = &linkStderr
	if runErr := cmd.Run(); runErr != nil {
		return stats, fmt.Errorf("%s Phase 2 link failed (%s %s):\n%s\n%w",
			strings.ToUpper(string(backend)), cc, strings.Join(linkArgs, " "), strings.TrimSpace(linkStderr.String()), runErr)
	}
	return stats, nil
}
