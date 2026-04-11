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
	"fmt"
	"os"
	"runtime"
)

// Result is the outcome of a toolchain resolution. Every field is populated.
// Callers use Path for execution and can report provenance from the rest.
type Result struct {
	Tool      string // "go"
	Version   string // "1.26.1"
	Path      string // absolute path to binary
	CacheHit  bool   // true if served from cache, false if downloaded
	SourceURL string // where it was (or would be) fetched from
	SHA256    string // verified archive checksum (provenance)
	BinSHA256 string // extracted binary checksum (cache validation)
}

// Resolve ensures a tool at the requested version is available and verified.
// Returns the binary path and provenance. Downloads if not cached.
// Hard-fails on checksum mismatch, download error, or metadata write failure.
// No fallback. No stderr output — callers own presentation.
func Resolve(rootDir, tool, version string) (Result, error) {
	switch tool {
	case "go":
		return resolveGo(rootDir, version)
	default:
		return Result{}, fmt.Errorf("unsupported toolchain %q", tool)
	}
}

// resolveGo ensures a Go toolchain is cached and verified.
func resolveGo(rootDir, version string) (Result, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	sourceURL := goDownloadURL(version, goos, goarch)
	binPath := CacheBinPath(rootDir, "go", version, "go")

	// Check cache — verify binary checksum against metadata
	if _, err := os.Stat(binPath); err == nil {
		meta, metaErr := ReadMetadata(rootDir, "go", version)
		if metaErr == nil && meta.BinSHA256 != "" {
			actual, hashErr := fileSHA256(binPath)
			if hashErr == nil && actual == meta.BinSHA256 {
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
		}
		// Corrupt or missing metadata — clean and re-download
		os.RemoveAll(CacheDir(rootDir, "go", version))
	}

	// Fetch official checksum for the archive
	expectedSHA, err := fetchGoChecksum(version, goos, goarch)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain go %s: fetching checksum: %w", version, err)
	}

	// Download archive
	archivePath, err := downloadToTemp(sourceURL)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain go %s: download failed: %w", version, err)
	}
	defer os.Remove(archivePath)

	// Verify archive checksum BEFORE extraction
	archiveSHA, err := fileSHA256(archivePath)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain go %s: checksum computation failed: %w", version, err)
	}
	if archiveSHA != expectedSHA {
		return Result{}, fmt.Errorf("toolchain go %s: archive checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", version, expectedSHA, archiveSHA, sourceURL)
	}

	// Extract
	destDir := CacheDir(rootDir, "go", version)
	if err := extractGoArchive(archivePath, destDir); err != nil {
		os.RemoveAll(destDir)
		return Result{}, fmt.Errorf("toolchain go %s: extraction failed: %w", version, err)
	}

	// Verify binary exists after extraction
	if _, err := os.Stat(binPath); err != nil {
		os.RemoveAll(destDir)
		return Result{}, fmt.Errorf("toolchain go %s: binary not found after extraction at %s", version, binPath)
	}

	// Compute binary checksum for future cache validation
	binSHA, err := fileSHA256(binPath)
	if err != nil {
		os.RemoveAll(destDir)
		return Result{}, fmt.Errorf("toolchain go %s: binary checksum failed: %w", version, err)
	}

	// Write metadata — hard failure, install is incomplete without provenance
	meta := Metadata{
		Tool:      "go",
		Version:   version,
		Platform:  fmt.Sprintf("%s/%s", goos, goarch),
		SourceURL: sourceURL,
		SHA256:    archiveSHA,
		BinSHA256: binSHA,
	}
	if err := WriteMetadata(rootDir, "go", version, meta); err != nil {
		os.RemoveAll(destDir)
		return Result{}, fmt.Errorf("toolchain go %s: metadata write failed (install aborted): %w", version, err)
	}

	return Result{
		Tool:      "go",
		Version:   version,
		Path:      binPath,
		CacheHit:  false,
		SourceURL: sourceURL,
		SHA256:    archiveSHA,
		BinSHA256: binSHA,
	}, nil
}
