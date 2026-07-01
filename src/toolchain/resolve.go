// Package toolchain provides a governed execution substrate for external tools.
//
// StageFreight owns its toolchains. Every tool used is resolved, downloaded,
// verified, cached, and reported. No silent host fallback. No DinD. No
// containers-for-tools. No environment luck.
//
// Contract properties:
//   - Immutable installs — once cached, a version directory is never mutated
//   - Checksum verification required — every download verified against official checksums
//   - Explicit provenance — .metadata.json records source URL, checksum, install time
//   - Deterministic resolution — same version = same binary, always
//   - No silent host fallback — system binaries in PATH are not used
//   - Hard failure on verification miss — checksum mismatch = error, not warning
package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Result is the outcome of a toolchain resolution. Every field is populated.
// Callers use Path for execution and can report provenance from the rest.
type Result struct {
	Tool      string // "go", "trivy", etc.
	Version   string // "1.26.1", "0.69.3", etc.
	Path      string // absolute path to binary
	CacheHit  bool   // true if served from cache, false if downloaded
	SourceURL string // where it was (or would be) fetched from
	SHA256    string // verified archive/binary checksum (provenance)
	BinSHA256 string // extracted binary checksum (cache validation)
}

// Resolve ensures a tool at the requested version is available and verified.
// Returns the binary path and provenance. Downloads if not cached.
// Hard-fails on checksum mismatch, download error, or metadata write failure.
// Empty version uses ToolDef.DefaultVer.
// No fallback. No stderr output — callers own presentation.
func Resolve(rootDir, tool, version string) (Result, error) {
	// Go and Rust are full DISTRIBUTIONS (not single binaries), so each has its own
	// resolution path: verified official artifact, explicit install layout, no host
	// fallback. Empty version defers to the language-specific default.
	if tool == "go" {
		return resolveGo(rootDir, version)
	}
	if tool == "rust" {
		if version == "" {
			version = defaultRustChannel
		}
		return resolveRust(rootDir, version)
	}

	def, ok := LookupTool(tool)
	if !ok {
		return Result{}, fmt.Errorf("unsupported toolchain %q", tool)
	}

	if version == "" {
		version = def.DefaultVer
	}

	return resolveWithDef(rootDir, def, version, "")
}

// ResolvePinned is Resolve for tools whose integrity is a project-pinned SHA256
// (from toolchains.desired.<tool>.sha256), not an upstream checksum manifest —
// e.g. cargo-llvm-cov, whose upstream ships BLAKE3. The downloaded artifact is
// verified against sha256. An empty sha256 falls back to the tool's ChecksumURL,
// so this is a strict superset of Resolve.
func ResolvePinned(rootDir, tool, version, sha256 string) (Result, error) {
	if tool == "go" || tool == "rust" {
		return Resolve(rootDir, tool, version) // distributions verify their own way
	}
	def, ok := LookupTool(tool)
	if !ok {
		return Result{}, fmt.Errorf("unsupported toolchain %q", tool)
	}
	if version == "" {
		version = def.DefaultVer
	}
	return resolveWithDef(rootDir, def, version, sha256)
}

// FetchArtifactSHA256 downloads a tool's release artifact and returns its SHA256 —
// the onboarding/deps derivation step that makes a pinned digest ecosystem-agnostic
// (works whether upstream publishes SHA256, BLAKE3, or nothing at all).
func FetchArtifactSHA256(tool, version string) (string, error) {
	def, ok := LookupTool(tool)
	if !ok {
		return "", fmt.Errorf("unsupported toolchain %q", tool)
	}
	if version == "" {
		version = def.DefaultVer
	}
	archivePath, err := downloadToTemp(def.DownloadURL(version, runtime.GOOS, runtime.GOARCH))
	if err != nil {
		return "", fmt.Errorf("fetch %s %s: %w", tool, version, err)
	}
	defer os.Remove(archivePath)
	return fileSHA256(archivePath)
}

// resolveWithDef is the generic resolver for all non-Go/Rust tools. pinnedSHA, when
// non-empty, is the project-pinned artifact digest (config); otherwise integrity
// comes from the tool's upstream ChecksumURL. One MUST be present.
func resolveWithDef(rootDir string, def ToolDef, version, pinnedSHA string) (Result, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	sourceURL := def.DownloadURL(version, goos, goarch)

	// Search all read roots for a valid cached install.
	for _, root := range ReadRoots(rootDir) {
		binPath := CacheBinPathIn(root, def.Name, version, def.BinaryName)
		if _, err := os.Stat(binPath); err != nil {
			continue
		}
		meta, metaErr := readMetadataFrom(root, def.Name, version)
		if metaErr != nil || meta.BinSHA256 == "" {
			continue // no valid metadata — skip, don't delete (may be read-only)
		}
		actual, hashErr := fileSHA256(binPath)
		if hashErr != nil || actual != meta.BinSHA256 {
			continue // corrupt — skip, try next root
		}
		return Result{
			Tool:      def.Name,
			Version:   version,
			Path:      binPath,
			CacheHit:  true,
			SourceURL: meta.SourceURL,
			SHA256:    meta.SHA256,
			BinSHA256: meta.BinSHA256,
		}, nil
	}

	// No valid cache hit — download and install.
	installRoot := InstallRoot(rootDir)
	installDir := CacheDirIn(installRoot, def.Name, version)

	// Acquire install lock to prevent concurrent downloads
	lock, err := AcquireInstallLock(installDir, 5*time.Minute)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain %s %s: %w", def.Name, version, err)
	}
	defer ReleaseInstallLock(lock)

	// Re-check cache after acquiring lock (another process may have installed)
	binPath := CacheBinPathIn(installRoot, def.Name, version, def.BinaryName)
	if _, err := os.Stat(binPath); err == nil {
		meta, metaErr := readMetadataFrom(installRoot, def.Name, version)
		if metaErr == nil && meta.BinSHA256 != "" {
			actual, hashErr := fileSHA256(binPath)
			if hashErr == nil && actual == meta.BinSHA256 {
				return Result{
					Tool:      def.Name,
					Version:   version,
					Path:      binPath,
					CacheHit:  true,
					SourceURL: meta.SourceURL,
					SHA256:    meta.SHA256,
					BinSHA256: meta.BinSHA256,
				}, nil
			}
		}
	}

	// Determine the expected artifact digest. A pinned SHA256 (the project trust
	// root, from toolchains.desired.<tool>.sha256) wins; otherwise fetch it from the
	// upstream SHA256 manifest. One MUST be present — no unverified path in this layer.
	downloadFilename := filepath.Base(sourceURL)
	var expectedSHA string
	switch {
	case pinnedSHA != "":
		expectedSHA = pinnedSHA
	case def.ChecksumURL != nil:
		expectedSHA, err = fetchChecksumFromURL(def.ChecksumURL(version, goos, goarch), downloadFilename)
		if err != nil {
			return Result{}, fmt.Errorf("toolchain %s %s: %w", def.Name, version, err)
		}
	default:
		return Result{}, fmt.Errorf("toolchain %s %s: no integrity source — pin toolchains.desired.%s.sha256 or provide a checksum URL", def.Name, version, def.Name)
	}

	// Download
	archivePath, err := downloadToTemp(sourceURL)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain %s %s: download failed: %w", def.Name, version, err)
	}
	defer os.Remove(archivePath)

	// Verify archive/binary checksum
	actualSHA, err := fileSHA256(archivePath)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain %s %s: checksum computation failed: %w", def.Name, version, err)
	}
	if actualSHA != expectedSHA {
		return Result{}, fmt.Errorf("toolchain %s %s: checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", def.Name, version, expectedSHA, actualSHA, sourceURL)
	}

	// Install based on format
	if err := os.MkdirAll(filepath.Join(installDir, "bin"), 0755); err != nil {
		return Result{}, fmt.Errorf("toolchain %s %s: creating install dir: %w", def.Name, version, err)
	}

	switch def.Format {
	case "binary":
		if err := installStandaloneBinary(archivePath, binPath); err != nil {
			os.RemoveAll(installDir)
			return Result{}, fmt.Errorf("toolchain %s %s: install failed: %w", def.Name, version, err)
		}
	case "tar.gz":
		if err := installFromArchive(archivePath, binPath, def.BinaryName); err != nil {
			os.RemoveAll(installDir)
			return Result{}, fmt.Errorf("toolchain %s %s: install failed: %w", def.Name, version, err)
		}
	default:
		return Result{}, fmt.Errorf("toolchain %s %s: unsupported format %q", def.Name, version, def.Format)
	}

	// Verify binary exists
	if _, err := os.Stat(binPath); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain %s %s: binary not found after install at %s", def.Name, version, binPath)
	}

	// Compute binary checksum
	binSHA, err := fileSHA256(binPath)
	if err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain %s %s: binary checksum failed: %w", def.Name, version, err)
	}

	// Write metadata — hard failure
	meta := Metadata{
		Tool:      def.Name,
		Version:   version,
		Platform:  fmt.Sprintf("%s/%s", goos, goarch),
		SourceURL: sourceURL,
		SHA256:    actualSHA,
		BinSHA256: binSHA,
	}
	if err := writeMetadataTo(installRoot, def.Name, version, meta); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain %s %s: metadata write failed (install aborted): %w", def.Name, version, err)
	}

	return Result{
		Tool:      def.Name,
		Version:   version,
		Path:      binPath,
		CacheHit:  false,
		SourceURL: sourceURL,
		SHA256:    actualSHA,
		BinSHA256: binSHA,
	}, nil
}

// resolveGo ensures a Go toolchain is cached and verified.
// Go is special: uses go.dev JSON API for checksums, extracts full distribution.
func resolveGo(rootDir, version string) (Result, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// go.dev/dl publishes only full patch versions; a bare major.minor (a
	// `go 1.24` directive, or the resolver fallback) must be resolved to its
	// latest stable patch before the URL and checksum are computed. The resolver
	// returns that patch's checksum from the same index document (preChecksum),
	// so we don't fetch the index twice.
	var preChecksum string
	if isGoMajorMinor(version) {
		full, sum, err := resolveLatestGoPatch(version, goos, goarch)
		if err != nil {
			return Result{}, fmt.Errorf("toolchain go %s: resolving latest patch: %w", version, err)
		}
		version, preChecksum = full, sum
	}

	sourceURL := goDownloadURL(version, goos, goarch)

	// Search all read roots for a valid cached install.
	for _, root := range ReadRoots(rootDir) {
		binPath := CacheBinPathIn(root, "go", version, "go")
		if _, err := os.Stat(binPath); err != nil {
			continue
		}
		meta, metaErr := readMetadataFrom(root, "go", version)
		if metaErr != nil || meta.BinSHA256 == "" {
			continue
		}
		actual, hashErr := fileSHA256(binPath)
		if hashErr != nil || actual != meta.BinSHA256 {
			continue
		}
		return Result{
			Tool:      "go",
			Version:   version,
			Path:      binPath,
			CacheHit:  true,
			SourceURL: meta.SourceURL,
			SHA256:    meta.SHA256,
			BinSHA256: meta.BinSHA256,
		}, nil
	}

	// No valid cache hit — download and install.
	installRoot := InstallRoot(rootDir)
	installDir := CacheDirIn(installRoot, "go", version)

	lock, err := AcquireInstallLock(installDir, 5*time.Minute)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain go %s: %w", version, err)
	}
	defer ReleaseInstallLock(lock)

	// Re-check after lock
	binPath := CacheBinPathIn(installRoot, "go", version, "go")
	if _, err := os.Stat(binPath); err == nil {
		meta, metaErr := readMetadataFrom(installRoot, "go", version)
		if metaErr == nil && meta.BinSHA256 != "" {
			actual, hashErr := fileSHA256(binPath)
			if hashErr == nil && actual == meta.BinSHA256 {
				return Result{
					Tool: "go", Version: version, Path: binPath, CacheHit: true,
					SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256,
				}, nil
			}
		}
	}

	// Reuse the checksum already pulled during major.minor normalization; only
	// fetch the index again when we didn't (an exact full version was supplied).
	expectedSHA := preChecksum
	if expectedSHA == "" {
		var err error
		if expectedSHA, err = fetchGoChecksum(version, goos, goarch); err != nil {
			return Result{}, fmt.Errorf("toolchain go %s: fetching checksum: %w", version, err)
		}
	}

	archivePath, err := downloadToTemp(sourceURL)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain go %s: download failed: %w", version, err)
	}
	defer os.Remove(archivePath)

	archiveSHA, err := fileSHA256(archivePath)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain go %s: checksum computation failed: %w", version, err)
	}
	if archiveSHA != expectedSHA {
		return Result{}, fmt.Errorf("toolchain go %s: archive checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", version, expectedSHA, archiveSHA, sourceURL)
	}

	if err := extractGoArchive(archivePath, installDir); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain go %s: extraction failed: %w", version, err)
	}

	if _, err := os.Stat(binPath); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain go %s: binary not found after extraction at %s", version, binPath)
	}

	binSHA, err := fileSHA256(binPath)
	if err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain go %s: binary checksum failed: %w", version, err)
	}

	meta := Metadata{
		Tool: "go", Version: version, Platform: fmt.Sprintf("%s/%s", goos, goarch),
		SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA,
	}
	if err := writeMetadataTo(installRoot, "go", version, meta); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain go %s: metadata write failed (install aborted): %w", version, err)
	}

	return Result{
		Tool: "go", Version: version, Path: binPath, CacheHit: false,
		SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA,
	}, nil
}

// readMetadataFrom reads metadata from a specific cache root.
func readMetadataFrom(root, tool, version string) (Metadata, error) {
	path := MetadataPathIn(root, tool, version)
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, err
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Metadata{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

// writeMetadataTo writes metadata to a specific cache root atomically.
func writeMetadataTo(root, tool, version string, m Metadata) error {
	StampMetadata(&m)
	dir := CacheDirIn(root, tool, version)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	target := filepath.Join(dir, ".metadata.json")
	tmp := target + ".tmp"
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
