package toolchain

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Rust mirrors the Go toolchain model: StageFreight owns the toolchain — official
// distribution artifact, verified checksum, explicit install layout, no rustup as
// the authoritative state, no ambient `rust:` container. The combined standalone
// tarball (rustc + cargo + std) is installed to a pinned prefix via its bundled
// install.sh; cargo at <prefix>/bin/cargo is the resolved binary.

// rustHostTriple projects the host GOOS/GOARCH to the Rust host target triple for
// the TOOLCHAIN download. (Target-triple projection for cross-compilation is the
// engine adapter's concern, later.)
func rustHostTriple(goos, goarch string) string {
	arch := map[string]string{
		"amd64": "x86_64", "arm64": "aarch64", "arm": "armv7", "386": "i686",
	}[goarch]
	if arch == "" {
		arch = goarch
	}
	switch goos {
	case "linux":
		return arch + "-unknown-linux-gnu"
	case "darwin":
		return arch + "-apple-darwin"
	case "windows":
		return arch + "-pc-windows-msvc"
	default:
		return arch + "-unknown-" + goos
	}
}

func rustDownloadURL(version, triple string) string {
	return fmt.Sprintf("https://static.rust-lang.org/dist/rust-%s-%s.tar.gz", version, triple)
}

// ResolveRustVersion reads the pinned channel from rust-toolchain.toml / rust-toolchain
// (the Rust equivalent of go.mod's toolchain directive), falling back to a recent
// stable. Only an explicit numeric version is honored — "stable"/"nightly" channels
// are NOT pinned identities and fall back to the default (a build must be reproducible).
func ResolveRustVersion(dir, repoRoot string) string {
	for _, p := range []string{
		filepath.Join(dir, "rust-toolchain.toml"),
		filepath.Join(dir, "rust-toolchain"),
		filepath.Join(repoRoot, "rust-toolchain.toml"),
		filepath.Join(repoRoot, "rust-toolchain"),
	} {
		if v := parseRustChannel(p); v != "" {
			return v
		}
	}
	return defaultRustVersion
}

const defaultRustVersion = "1.83.0"

// parseRustChannel extracts a pinned numeric version from a rust-toolchain[.toml] file.
// Handles both `channel = "1.83.0"` (toml) and a bare `1.83.0` (legacy plain file).
func parseRustChannel(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "channel") {
			if i := strings.IndexByte(line, '='); i >= 0 {
				line = strings.TrimSpace(line[i+1:])
			}
		}
		v := strings.Trim(line, `"'`)
		if isNumericVersion(v) {
			return v
		}
	}
	return ""
}

func isNumericVersion(s string) bool {
	if s == "" || (s[0] < '0' || s[0] > '9') {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// resolveRust ensures a verified Rust toolchain is installed and returns cargo's path.
// Special-cased like Go (full distribution + bundled installer, not a single binary).
func resolveRust(rootDir, version string) (Result, error) {
	triple := rustHostTriple(runtime.GOOS, runtime.GOARCH)
	sourceURL := rustDownloadURL(version, triple)

	// Cache search across read roots.
	for _, root := range ReadRoots(rootDir) {
		binPath := CacheBinPathIn(root, "rust", version, "cargo")
		if _, err := os.Stat(binPath); err != nil {
			continue
		}
		meta, metaErr := readMetadataFrom(root, "rust", version)
		if metaErr != nil || meta.BinSHA256 == "" {
			continue
		}
		if actual, hashErr := fileSHA256(binPath); hashErr == nil && actual == meta.BinSHA256 {
			return Result{Tool: "rust", Version: version, Path: binPath, CacheHit: true,
				SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256}, nil
		}
	}

	installRoot := InstallRoot(rootDir)
	installDir := CacheDirIn(installRoot, "rust", version)
	lock, err := AcquireInstallLock(installDir, 15*time.Minute) // a Rust dist is large
	if err != nil {
		return Result{}, fmt.Errorf("toolchain rust %s: %w", version, err)
	}
	defer ReleaseInstallLock(lock)

	binPath := CacheBinPathIn(installRoot, "rust", version, "cargo")
	if _, err := os.Stat(binPath); err == nil {
		if meta, metaErr := readMetadataFrom(installRoot, "rust", version); metaErr == nil && meta.BinSHA256 != "" {
			if actual, hashErr := fileSHA256(binPath); hashErr == nil && actual == meta.BinSHA256 {
				return Result{Tool: "rust", Version: version, Path: binPath, CacheHit: true,
					SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256}, nil
			}
		}
	}

	expectedSHA, err := fetchChecksumFromURL(sourceURL+".sha256", filepath.Base(sourceURL))
	if err != nil {
		return Result{}, fmt.Errorf("toolchain rust %s: fetching checksum: %w", version, err)
	}
	archivePath, err := downloadToTemp(sourceURL)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain rust %s: download failed: %w", version, err)
	}
	defer os.Remove(archivePath)
	archiveSHA, err := fileSHA256(archivePath)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain rust %s: checksum computation failed: %w", version, err)
	}
	if archiveSHA != expectedSHA {
		return Result{}, fmt.Errorf("toolchain rust %s: archive checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", version, expectedSHA, archiveSHA, sourceURL)
	}

	if err := installRustDist(archivePath, installDir, version, triple); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain rust %s: install failed: %w", version, err)
	}
	if _, err := os.Stat(binPath); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain rust %s: cargo not found after install at %s", version, binPath)
	}
	binSHA, err := fileSHA256(binPath)
	if err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain rust %s: binary checksum failed: %w", version, err)
	}
	meta := Metadata{Tool: "rust", Version: version, Platform: fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA}
	if err := writeMetadataTo(installRoot, "rust", version, meta); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain rust %s: metadata write failed (install aborted): %w", version, err)
	}
	return Result{Tool: "rust", Version: version, Path: binPath, CacheHit: false,
		SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA}, nil
}

// installRustDist extracts the combined Rust tarball and runs its bundled install.sh
// to a pinned prefix (no ldconfig, no docs) — a POSIX-sh installer that copies rustc,
// cargo, and the host std into <installDir>/{bin,lib}.
func installRustDist(archivePath, installDir, version, triple string) error {
	tmp, err := os.MkdirTemp("", "sf-rust-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := extractTarGzTo(archivePath, tmp); err != nil {
		return fmt.Errorf("extracting rust dist: %w", err)
	}
	script := filepath.Join(tmp, fmt.Sprintf("rust-%s-%s", version, triple), "install.sh")
	cmd := exec.Command("sh", script,
		"--prefix="+installDir, "--disable-ldconfig", "--without=rust-docs")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install.sh: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// extractTarGzTo extracts a .tar.gz tree verbatim into destDir (no prefix stripping).
func extractTarGzTo(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// Reject path traversal.
		target := filepath.Join(destDir, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return err
			}
			w.Close()
		case tar.TypeSymlink:
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
}

// CargoCacheDir returns the persistent CARGO_HOME on the /stagefreight mount, or "" when
// not writable — the Rust analog of GoCacheDirs, for cross-run registry/build reuse.
func CargoCacheDir() string {
	gomod, _ := GoCacheDirs()
	if gomod == "" {
		return ""
	}
	// gomod is <persistRoot>/gomodcache; place cargo home as a sibling.
	return filepath.Join(filepath.Dir(gomod), "cargo")
}
