package manifest_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/format"
	"lowlight/internal/manifest"
)

type memTransport map[string][]byte

func (m memTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, ok := m[req.URL.String()]
	if !ok {
		return &http.Response{
			StatusCode: 404,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
		}, nil
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestTOMLAndManifestParsing(t *testing.T) {
	raw := `
# Top comment
[package]
name = "my-app"
version = "0.2.0"
edition = "2026"
description = "Demo service"
license = "MIT"
authors = ["Alice", "Bob"]

[dependencies]
local_utils = { path = "../local_utils" }
jsonx = { url = "https://example.org/jsonx-1.2.0.tar.gz", hash = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" }

[dependencies.http_ext]
index = "https://acme.dev/pkgs/http_ext.toml"
version = "^0.3"

[dev-dependencies]
bench = { path = "../bench" }

[overrides]
jsonx = { path = "../my_jsonx_fork" }

[build]
backend = "llvm"
release = true
c_flags = ["-O2"]
c_libs = ["m"]

[format]
indent_style = "spaces"
tab_width = 2
line_width = 88

[target.linux]
c_libs = ["ssl", "crypto"]

[target."aarch64-unknown-linux-gnu"]
c_flags = ["-march=armv8-a"]
`
	m, err := manifest.ParseManifest("/tmp/myapp/lowlight.toml", raw)
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if m.Package.Name != "my-app" || manifest.NormalizeQualifier(m.Package.Name) != "my_app" {
		t.Fatalf("unexpected package name: %+v", m.Package)
	}
	if len(m.Dependencies) != 3 {
		t.Fatalf("expected 3 dependencies (inline + sub-table), got %d", len(m.Dependencies))
	}
	if m.Dependencies["http_ext"].Kind != manifest.DepIndex || m.Dependencies["http_ext"].Version != "^0.3" {
		t.Fatalf("unexpected sub-table dep http_ext: %+v", m.Dependencies["http_ext"])
	}
	if m.Overrides["jsonx"].Kind != manifest.DepPath {
		t.Fatalf("unexpected override: %+v", m.Overrides["jsonx"])
	}
	if m.Build.Backend != "llvm" || m.Build.Release == nil || !*m.Build.Release {
		t.Fatalf("unexpected build config: %+v", m.Build)
	}
	if !m.Format.HasSection || m.Format.IndentStyle != "spaces" || m.Format.TabWidth != 2 || m.Format.LineWidth != 88 {
		t.Fatalf("unexpected format config: %+v", m.Format)
	}
	linuxArm := manifest.ParseTarget("aarch64-unknown-linux-gnu")
	cflags, clibs := m.EffectiveTargetFlags(linuxArm)
	if len(cflags) != 2 || len(clibs) != 3 {
		t.Fatalf("unexpected target flags/libs for linux-arm64: cflags=%v clibs=%v", cflags, clibs)
	}
}

func TestSurgicalManifestEditsPreserveComments(t *testing.T) {
	orig := `# Header comment
[package]
name = "demo"
version = "0.1.0"

[dependencies]
# Keep this note!
zebra = { path = "../zebra" } # inline note
alpha = { path = "../alpha" }
`
	updated := manifest.UpsertTableEntry(orig, "dependencies", "zebra", `{ path = "../zebra2" }`)
	if !strings.Contains(updated, "# Header comment") || !strings.Contains(updated, "# Keep this note!") || !strings.Contains(updated, "# inline note") {
		t.Fatalf("UpsertTableEntry lost comments:\n%s", updated)
	}
	if !strings.Contains(updated, `zebra = { path = "../zebra2" } # inline note`) {
		t.Fatalf("UpsertTableEntry failed to update entry:\n%s", updated)
	}

	added := manifest.UpsertTableEntry(updated, "dev-dependencies", "helper", `{ path = "../helper" }`)
	if !strings.Contains(added, "[dev-dependencies]\nhelper = { path = \"../helper\" }") {
		t.Fatalf("UpsertTableEntry failed to append section:\n%s", added)
	}

	removed, ok := manifest.RemoveTableEntry(added, "dependencies", "alpha")
	if !ok || strings.Contains(removed, "alpha =") {
		t.Fatalf("RemoveTableEntry failed:\n%s", removed)
	}

	formatted, err := manifest.FormatManifestSource(orig)
	if err != nil {
		t.Fatalf("FormatManifestSource failed: %v", err)
	}
	if strings.Index(formatted, "alpha =") > strings.Index(formatted, "zebra =") {
		t.Fatalf("expected FormatManifestSource to sort dependency keys:\n%s", formatted)
	}
}

func TestSemVerAndMVS(t *testing.T) {
	c1, err := manifest.ParseVersionConstraint("^0.3.1")
	if err != nil {
		t.Fatalf("parse ^0.3.1: %v", err)
	}
	c2, err := manifest.ParseVersionConstraint(">=0.3.3, <0.4.0")
	if err != nil {
		t.Fatalf("parse range: %v", err)
	}
	idxRaw := `
[package]
name = "http_ext"

[versions."0.3.2"]
url = "https://example.org/0.3.2.tar.gz"
hash = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

[versions."0.3.3"]
url = "https://example.org/0.3.3.tar.gz"
hash = "sha256:0000000000000000000000000000000000000000000000000000000000000002"
yanked = true

[versions."0.3.4"]
url = "https://example.org/0.3.4.tar.gz"
hash = "sha256:0000000000000000000000000000000000000000000000000000000000000003"

[versions."0.3.5"]
url = "https://example.org/0.3.5.tar.gz"
hash = "sha256:0000000000000000000000000000000000000000000000000000000000000004"
`
	idx, err := manifest.ParseStaticIndex("test", idxRaw)
	if err != nil {
		t.Fatalf("ParseStaticIndex: %v", err)
	}
	// MVS picks smallest non-yanked version satisfying both ^0.3.1 and >=0.3.3 (<0.4.0) => 0.3.4 (skipping yanked 0.3.3, not jumping to 0.3.5).
	sel, ok := idx.SelectMinimalVersion([]manifest.VersionConstraint{c1, c2}, "")
	if !ok || sel.Version.String() != "0.3.4" {
		t.Fatalf("expected MVS selection 0.3.4, got ok=%v ver=%s", ok, sel.Version.String())
	}
	// If already locked to yanked 0.3.3, allowYanked preserves reproducibility.
	selYanked, ok := idx.SelectMinimalVersion([]manifest.VersionConstraint{c1, c2}, "0.3.3")
	if !ok || selYanked.Version.String() != "0.3.3" {
		t.Fatalf("expected locked yanked 0.3.3 to remain selectable, got ok=%v ver=%s", ok, selYanked.Version.String())
	}
}

func TestTargetSuffixMatching(t *testing.T) {
	linuxAmd := manifest.ParseTarget("x86_64-unknown-linux-gnu")
	darwinArm := manifest.ParseTarget("aarch64-apple-darwin")
	winAmd := manifest.ParseTarget("x86_64-pc-windows-msvc")

	cases := []struct {
		file   string
		target manifest.TargetInfo
		want   bool
	}{
		{"fs.ll", linuxAmd, true},
		{"fs_unix.ll", linuxAmd, true},
		{"fs_unix.ll", darwinArm, true},
		{"fs_unix.ll", winAmd, false},
		{"fs_windows.ll", winAmd, true},
		{"fs_windows.ll", linuxAmd, false},
		{"asm_arm64.ll", darwinArm, true},
		{"asm_arm64.ll", linuxAmd, false},
		{"sys_linux_amd64.ll", linuxAmd, true},
		{"sys_linux_amd64.ll", darwinArm, false},
		{"fs_unix_test.ll", darwinArm, true},
		{"fs_unix_test.ll", winAmd, false},
	}
	for _, tc := range cases {
		if got := manifest.MatchesTargetSuffix(tc.file, tc.target); got != tc.want {
			t.Errorf("MatchesTargetSuffix(%q, %+v) = %v, want %v", tc.file, tc.target, got, tc.want)
		}
	}
	if !manifest.IsTestFile("foo_test.ll") || manifest.IsTestFile("foo.ll") {
		t.Fatalf("IsTestFile mismatch")
	}
}

func TestArchiveExtractionTraversalRejected(t *testing.T) {
	badTar := makeTarGz(t, map[string]string{
		"../evil.txt": "pwned",
	})
	if err := manifest.ExtractArchiveSafe("evil.tar.gz", badTar, t.TempDir()); err == nil {
		t.Fatal("expected traversal tar.gz to be rejected")
	}

	goodZip := makeZip(t, map[string]string{
		"pkg-1.0.0/lib.ll": "bind answer = () int: 42\n",
	})
	dest := t.TempDir()
	if err := manifest.ExtractArchiveSafe("pkg.zip", goodZip, dest); err != nil {
		t.Fatalf("ExtractArchiveSafe zip failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "lib.ll"))
	if err != nil || !strings.Contains(string(data), "42") {
		t.Fatalf("expected unwrapped lib.ll from zip, got err=%v data=%q", err, string(data))
	}
}

func TestDecentralizedIndexMVSFetchLockVendorAndOverride(t *testing.T) {
	// Build tarballs for jsonx@1.2.0, jsonx@1.3.0, and http_ext@0.3.4 (which depends on jsonx ^1.2.0).
	jsonx120 := makeTarGz(t, map[string]string{
		"jsonx-1.2.0/lowlight.toml": "[package]\nname = \"jsonx\"\nversion = \"1.2.0\"\n",
		"jsonx-1.2.0/jsonx.ll":      "bind ver = () string: \"1.2.0\"\n",
	})
	jsonx120Hash := manifest.ComputeSHA256(jsonx120)

	jsonx130 := makeTarGz(t, map[string]string{
		"jsonx-1.3.0/lowlight.toml": "[package]\nname = \"jsonx\"\nversion = \"1.3.0\"\n",
		"jsonx-1.3.0/jsonx.ll":      "bind ver = () string: \"1.3.0\"\n",
	})
	jsonx130Hash := manifest.ComputeSHA256(jsonx130)

	srvURL := "https://pkgs.test"
	httpExt034 := makeTarGz(t, map[string]string{
		"http_ext-0.3.4/router.ll": "bind route = () int: 7\n",
	})
	httpExt034Hash := manifest.ComputeSHA256(httpExt034)

	jsonxIdx := fmt.Sprintf(`
[package]
name = "jsonx"

[versions."1.2.0"]
url = "%s/tarballs/jsonx-1.2.0.tar.gz"
hash = "%s"

[versions."1.3.0"]
url = "%s/tarballs/jsonx-1.3.0.tar.gz"
hash = "%s"
`, srvURL, jsonx120Hash, srvURL, jsonx130Hash)

	httpExtIdx := fmt.Sprintf(`
[package]
name = "http_ext"

[versions."0.3.4"]
url = "%s/tarballs/http_ext-0.3.4.tar.gz"
hash = "%s"

[versions."0.3.4".dependencies]
jsonx = { index = "%s/pkgs/jsonx.toml", version = "^1.3.0" }
`, srvURL, httpExt034Hash, srvURL)

	routes := map[string][]byte{
		srvURL + "/tarballs/jsonx-1.2.0.tar.gz":    jsonx120,
		srvURL + "/tarballs/jsonx-1.3.0.tar.gz":    jsonx130,
		srvURL + "/tarballs/http_ext-0.3.4.tar.gz": httpExt034,
		srvURL + "/pkgs/jsonx.toml":                []byte(jsonxIdx),
		srvURL + "/pkgs/http_ext.toml":             []byte(httpExtIdx),
	}
	client := &http.Client{Transport: memTransport(routes)}

	projDir := t.TempDir()
	cacheDir := t.TempDir()
	fetcher := &manifest.Fetcher{Client: client, CacheRoot: cacheDir}
	manifestSrc := fmt.Sprintf(`
[package]
name = "myapp"
version = "0.1.0"

[dependencies]
jsonx = { index = "%s/pkgs/jsonx.toml", version = "^1.2.0" }
http_ext = { index = "%s/pkgs/http_ext.toml", version = "^0.3.0" }
`, srvURL, srvURL)
	if err := os.WriteFile(filepath.Join(projDir, manifest.ManifestFilename), []byte(manifestSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := manifest.DiscoverProject(projDir)
	if err != nil {
		t.Fatalf("DiscoverProject: %v", err)
	}

	// 1. Offline build before fetch must fail loudly without network access.
	if _, err := manifest.Resolve(ctx, manifest.ResolveOptions{AllowNetwork: false, CacheRoot: cacheDir}); err == nil {
		t.Fatal("expected uncached Resolve with AllowNetwork=false to fail")
	}

	// 2. Explicit SyncLockfile (`lowlight fetch`) resolves diamond via MVS: jsonx ^1.2.0 + ^1.3.0 -> 1.3.0!
	graph, err := manifest.SyncLockfile(ctx, manifest.ResolveOptions{CacheRoot: cacheDir, Fetcher: fetcher})
	if err != nil {
		t.Fatalf("SyncLockfile: %v", err)
	}
	if graph.Packages["jsonx"].Version != "1.3.0" {
		t.Fatalf("expected MVS diamond version 1.3.0 for jsonx, got %s", graph.Packages["jsonx"].Version)
	}
	if graph.Packages["http_ext"].Version != "0.3.4" {
		t.Fatalf("expected http_ext 0.3.4, got %s", graph.Packages["http_ext"].Version)
	}

	// 3. Subsequent Resolve with AllowNetwork=false + Locked=true succeeds purely from lockfile + local cache!
	offlineGraph, err := manifest.Resolve(ctx, manifest.ResolveOptions{AllowNetwork: false, Locked: true, CacheRoot: cacheDir})
	if err != nil {
		t.Fatalf("locked offline resolve failed: %v", err)
	}
	if offlineGraph.Packages["jsonx"].Dir == "" {
		t.Fatal("expected cached dir for jsonx")
	}

	// 4. VendorDependencies populates ./vendor/ and makes ./vendor/ authoritative even with empty cache!
	if _, err := manifest.VendorDependencies(ctx, manifest.ResolveOptions{CacheRoot: cacheDir, Fetcher: fetcher}); err != nil {
		t.Fatalf("VendorDependencies: %v", err)
	}
	emptyCache := t.TempDir()
	vendoredGraph, err := manifest.Resolve(ctx, manifest.ResolveOptions{AllowNetwork: false, Offline: true, CacheRoot: emptyCache})
	if err != nil {
		t.Fatalf("vendored offline resolve failed: %v", err)
	}
	if vendoredGraph.Packages["jsonx"].Source != "vendor" {
		t.Fatalf("expected source 'vendor', got %q", vendoredGraph.Packages["jsonx"].Source)
	}

	// 5. Root [overrides] supersedes even ./vendor/!
	forkDir := filepath.Join(t.TempDir(), "my_jsonx_fork")
	if err := os.MkdirAll(forkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(forkDir, "jsonx.ll"), []byte("bind ver = () string: \"fork\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overridden := manifest.UpsertTableEntry(manifestSrc, "overrides", "jsonx", fmt.Sprintf(`{ path = %q }`, forkDir))
	if err := os.WriteFile(filepath.Join(projDir, manifest.ManifestFilename), []byte(overridden), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx2, err := manifest.DiscoverProject(projDir)
	if err != nil {
		t.Fatal(err)
	}
	ovGraph, err := manifest.Resolve(ctx2, manifest.ResolveOptions{AllowNetwork: false, Offline: true, CacheRoot: emptyCache})
	if err != nil {
		t.Fatalf("override resolve failed: %v", err)
	}
	if ovGraph.Packages["jsonx"].Source != "override" || ovGraph.Packages["jsonx"].Dir != forkDir {
		t.Fatalf("expected jsonx override to %q, got %+v", forkDir, ovGraph.Packages["jsonx"])
	}
}

func TestWorkspaceDiscoveryAndFormatConfigFallback(t *testing.T) {
	wsDir := t.TempDir()
	sharedDir := filepath.Join(wsDir, "libs", "shared")
	appDir := filepath.Join(wsDir, "apps", "api")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	wsToml := `
[workspace]
members = ["libs/*", "apps/api"]

[format]
indent_style = "spaces"
tab_width = 2
line_width = 80
`
	if err := os.WriteFile(filepath.Join(wsDir, manifest.ManifestFilename), []byte(wsToml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, manifest.ManifestFilename), []byte("[package]\nname = \"shared\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, manifest.ManifestFilename), []byte("[package]\nname = \"api\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := manifest.DiscoverProject(appDir)
	if err != nil {
		t.Fatalf("DiscoverProject workspace member: %v", err)
	}
	if ctx.ActiveManifest.Package.Name != "api" || ctx.WorkspaceManifest == nil || ctx.WorkspaceMembers["shared"] == nil {
		t.Fatalf("unexpected workspace context: %+v", ctx)
	}

	// [format] from workspace lowlight.toml applies to member directory.
	fmtOpts, found := format.LoadConfigFound(appDir)
	if !found || fmtOpts.IndentStyle != "spaces" || fmtOpts.TabWidth != 2 || fmtOpts.LineWidth != 80 {
		t.Fatalf("expected [format] from workspace lowlight.toml, got found=%v opts=%+v", found, fmtOpts)
	}

	// Local .lowlightfmt in subdirectory overrides workspace [format].
	if err := os.WriteFile(filepath.Join(appDir, ".lowlightfmt"), []byte("indent_style = tabs\ntab_width = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fmtOpts2, found2 := format.LoadConfigFound(appDir)
	if !found2 || fmtOpts2.IndentStyle != "tabs" || fmtOpts2.TabWidth != 4 {
		t.Fatalf("expected local .lowlightfmt override, got found=%v opts=%+v", found2, fmtOpts2)
	}
}
