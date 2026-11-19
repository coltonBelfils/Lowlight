package manifest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultCacheRoot returns `$LOWLIGHT_CACHE` or `~/.lowlight/cache` (`SPECIFICATION.md §12.5`).
func DefaultCacheRoot() string {
	if env := strings.TrimSpace(os.Getenv("LOWLIGHT_CACHE")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "lowlight-cache")
	}
	return filepath.Join(home, ".lowlight", "cache")
}

// CachePackageDir returns `<cacheRoot>/sha256/<hex>/` for an expected `"sha256:<hex>"` digest.
func CachePackageDir(cacheRoot, hash string) (string, error) {
	if !IsValidSHA256Hash(hash) {
		return "", fmt.Errorf("invalid sha256 hash %q", hash)
	}
	if cacheRoot == "" {
		cacheRoot = DefaultCacheRoot()
	}
	hexPart := strings.TrimPrefix(hash, "sha256:")
	return filepath.Join(cacheRoot, "sha256", hexPart), nil
}

// ValidateRemoteURL enforces HTTPS for remote endpoints while allowing loopback HTTP for local testing (`SPECIFICATION.md §12.4`).
func ValidateRemoteURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		host := strings.ToLower(u.Hostname())
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		return fmt.Errorf("remote URL %q must use https:// (insecure http:// is only allowed for loopback hosts)", rawURL)
	default:
		return fmt.Errorf("unsupported URL scheme %q in %q (expected https://)", u.Scheme, rawURL)
	}
}

// Fetcher performs HTTP fetches for static indexes and package archives.
type Fetcher struct {
	Client    *http.Client
	CacheRoot string
}

// NewFetcher returns a Fetcher with sensible timeouts and default cache root.
func NewFetcher(cacheRoot string) *Fetcher {
	if cacheRoot == "" {
		cacheRoot = DefaultCacheRoot()
	}
	return &Fetcher{
		Client:    &http.Client{Timeout: 30 * time.Second},
		CacheRoot: cacheRoot,
	}
}

// FetchBytes downloads a remote URL enforcing HTTPS rules and returning raw bytes.
func (f *Fetcher) FetchBytes(rawURL string) ([]byte, error) {
	if err := ValidateRemoteURL(rawURL); err != nil {
		return nil, err
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s failed: %w", rawURL, err)
	}
	return data, nil
}

// FetchIndex downloads and parses a decentralized static TOML index (`SPECIFICATION.md §12.4`).
func (f *Fetcher) FetchIndex(indexURL string) (*StaticIndex, error) {
	data, err := f.FetchBytes(indexURL)
	if err != nil {
		return nil, err
	}
	return ParseStaticIndex(indexURL, string(data))
}

// ComputeSHA256 returns `"sha256:<64-lowercase-hex>"` for data.
func ComputeSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EnsureArchiveCached downloads `archiveURL`, verifies `expectedHash` (or computes it when
// `expectedHash == ""` for `lowlight add --url`), safely extracts into the content-addressed
// cache directory, and returns `(cacheDir, actualHash, err)`.
func (f *Fetcher) EnsureArchiveCached(archiveURL, expectedHash string) (string, string, error) {
	if expectedHash != "" {
		cacheDir, err := CachePackageDir(f.CacheRoot, expectedHash)
		if err != nil {
			return "", "", err
		}
		if st, err := os.Stat(cacheDir); err == nil && st.IsDir() {
			return cacheDir, expectedHash, nil
		}
	}

	data, err := f.FetchBytes(archiveURL)
	if err != nil {
		return "", "", err
	}
	actualHash := ComputeSHA256(data)
	if expectedHash != "" && actualHash != expectedHash {
		return "", "", fmt.Errorf("sha256 mismatch for %s: expected %s, got %s", archiveURL, expectedHash, actualHash)
	}

	cacheDir, err := CachePackageDir(f.CacheRoot, actualHash)
	if err != nil {
		return "", "", err
	}
	if st, err := os.Stat(cacheDir); err == nil && st.IsDir() {
		return cacheDir, actualHash, nil
	}
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return "", "", fmt.Errorf("create cache parent dir: %w", err)
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(cacheDir), ".unpack-*")
	if err != nil {
		return "", "", fmt.Errorf("create temp unpack dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := ExtractArchiveSafe(archiveURL, data, tmpDir); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmpDir, cacheDir); err != nil {
		// Handle concurrent race where another process created cacheDir simultaneously.
		if st, stErr := os.Stat(cacheDir); stErr == nil && st.IsDir() {
			return cacheDir, actualHash, nil
		}
		return "", "", fmt.Errorf("commit cached package dir: %w", err)
	}
	return cacheDir, actualHash, nil
}

// ExtractArchiveSafe extracts a `.tar.gz`, `.tgz`, or `.zip` archive into `destDir`,
// enforcing `SPECIFICATION.md §12.4` safety checks (no `..` traversal, no absolute paths,
// no symlinks/device nodes) and unwrapping a uniform single top-level wrapper directory.
func ExtractArchiveSafe(archiveName string, data []byte, destDir string) error {
	lower := strings.ToLower(archiveName)
	if idx := strings.IndexByte(lower, '?'); idx >= 0 {
		lower = lower[:idx]
	}

	stageDir := filepath.Join(destDir, ".__stage__")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return err
	}

	var err error
	switch {
	case strings.HasSuffix(lower, ".zip") || (len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 3 && data[3] == 4):
		err = extractZipSafe(data, stageDir)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || (len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b):
		err = extractTarGzSafe(data, stageDir)
	default:
		return fmt.Errorf("unsupported archive format for %q (expected .tar.gz, .tgz, or .zip)", archiveName)
	}
	if err != nil {
		return err
	}

	// Unwrap a uniform single top-level directory wrapper (standard release tarball layout).
	srcRoot := stageDir
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return err
	}
	var nonMeta []os.DirEntry
	for _, e := range entries {
		if e.Name() == "__MACOSX" || e.Name() == ".DS_Store" {
			continue
		}
		nonMeta = append(nonMeta, e)
	}
	if len(nonMeta) == 1 && nonMeta[0].IsDir() {
		srcRoot = filepath.Join(stageDir, nonMeta[0].Name())
	}

	// Move extracted contents from srcRoot into destDir.
	moveEntries, err := os.ReadDir(srcRoot)
	if err != nil {
		return err
	}
	for _, e := range moveEntries {
		if err := os.Rename(filepath.Join(srcRoot, e.Name()), filepath.Join(destDir, e.Name())); err != nil {
			return err
		}
	}
	return os.RemoveAll(stageDir)
}

func validateArchiveMemberPath(destRoot, rawName string) (string, error) {
	if rawName == "" {
		return "", fmt.Errorf("empty archive member path")
	}
	if strings.HasPrefix(rawName, "/") || strings.HasPrefix(rawName, "\\") {
		return "", fmt.Errorf("archive member %q uses forbidden absolute path", rawName)
	}
	cleanRel := filepath.Clean(filepath.FromSlash(rawName))
	if cleanRel == "." {
		return destRoot, nil
	}
	if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("archive member %q escapes destination directory", rawName)
	}
	absRoot, err := filepath.Abs(destRoot)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absRoot, cleanRel)
	if target != absRoot && !strings.HasPrefix(target, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("archive member %q escapes destination directory", rawName)
	}
	return target, nil
}

func extractTarGzSafe(data []byte, destRoot string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid gzip archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("corrupt tar archive: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("archive member %q contains forbidden symlink/hardlink", hdr.Name)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return fmt.Errorf("archive member %q contains forbidden special device entry", hdr.Name)
		case tar.TypeDir:
			target, err := validateArchiveMemberPath(destRoot, hdr.Name)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			target, err := validateArchiveMemberPath(destRoot, hdr.Name)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

func extractZipSafe(data []byte, destRoot string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("invalid zip archive: %w", err)
	}
	for _, zf := range zr.File {
		if zf.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive member %q contains forbidden symlink", zf.Name)
		}
		target, err := validateArchiveMemberPath(destRoot, zf.Name)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(f, rc)
		rc.Close()
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// copyDirTree copies all regular files and directories from srcDir into dstDir (skipping `.git` / `vendor`).
func copyDirTree(srcDir, dstDir string) error {
	if err := os.RemoveAll(dstDir); err != nil {
		return err
	}
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dstDir, 0o755)
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == ".lowlight" || name == "vendor" || name == "node_modules") {
			return filepath.SkipDir
		}
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
}
