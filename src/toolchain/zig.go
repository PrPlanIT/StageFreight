package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/PrPlanIT/StageFreight/src/atomicfile"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/substrate"
)

// defaultZigVersion is the pinned Zig StageFreight provisions; bump deliberately.
const defaultZigVersion = "0.17.1"

// zigIndexURL lists every Zig release's tarball URL and sha256 per platform.
var zigIndexURL = "https://ziglang.org/download/index.json"

func init() {
	substrate.RegisterCapability("c-toolchain", realizeCToolchain)
}

// zigPlatform is Zig's download key for the host, or "" when Zig publishes none.
func zigPlatform(goos, goarch string) string {
	if goos != "linux" {
		return ""
	}
	switch goarch {
	case "amd64":
		return "x86_64-linux"
	case "arm64":
		return "aarch64-linux"
	}
	return ""
}

type zigArtifact struct {
	Tarball string `json:"tarball"`
	Shasum  string `json:"shasum"`
}

// zigRelease reads the tarball URL and sha256 for version on platform from the index.
func zigRelease(index []byte, version, platform string) (zigArtifact, error) {
	var releases map[string]map[string]json.RawMessage
	if err := json.Unmarshal(index, &releases); err != nil {
		return zigArtifact{}, fmt.Errorf("parsing zig release index: %w", err)
	}
	rel, ok := releases[version]
	if !ok {
		return zigArtifact{}, fmt.Errorf("zig %s is not in the release index", version)
	}
	raw, ok := rel[platform]
	if !ok {
		return zigArtifact{}, fmt.Errorf("zig %s publishes no %s build", version, platform)
	}
	var a zigArtifact
	if err := json.Unmarshal(raw, &a); err != nil || a.Tarball == "" || a.Shasum == "" {
		return zigArtifact{}, fmt.Errorf("zig %s %s: malformed release index entry", version, platform)
	}
	return a, nil
}

// zigBinPathIn is the zig binary in an install; Zig keeps its lib/ beside it.
func zigBinPathIn(root, version string) string {
	return filepath.Join(CacheDirIn(root, "zig", version), "zig")
}

// resolveZig provisions a checksum-verified Zig distribution (the zig binary and its
// lib/ tree), verified against the sha256 ziglang.org publishes in its release index.
func resolveZig(rootDir, version string) (Result, error) {
	if version == "" {
		version = defaultZigVersion
	}
	platform := zigPlatform(runtime.GOOS, runtime.GOARCH)
	if platform == "" {
		return Result{}, fmt.Errorf("toolchain zig %s: no build for %s/%s", version, runtime.GOOS, runtime.GOARCH)
	}

	cached := func(root string) (Result, bool) {
		binPath := zigBinPathIn(root, version)
		meta, err := readMetadataFrom(root, "zig", version)
		if err != nil || meta.BinSHA256 == "" {
			return Result{}, false
		}
		if actual, err := fileSHA256(binPath); err != nil || actual != meta.BinSHA256 {
			return Result{}, false
		}
		return Result{Tool: "zig", Version: version, Path: binPath, CacheHit: true, SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256, Trust: TrustChecksum}, true
	}
	for _, root := range ReadRoots(rootDir) {
		if r, ok := cached(root); ok {
			return r, nil
		}
	}

	installRoot := InstallRoot(rootDir)
	installDir := CacheDirIn(installRoot, "zig", version)
	lock, err := paths.AcquireInstallLock(installDir, 5*time.Minute)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain zig %s: %w", version, err)
	}
	defer paths.ReleaseInstallLock(lock)
	if r, ok := cached(installRoot); ok {
		return r, nil
	}

	index, err := fetchBytes(zigIndexURL)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain zig %s: fetching release index: %w", version, err)
	}
	art, err := zigRelease(index, version, platform)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain zig: %w", err)
	}
	archivePath, err := downloadToTemp(art.Tarball)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain zig %s: download failed: %w", version, err)
	}
	defer os.Remove(archivePath)
	archiveSHA, err := fileSHA256(archivePath)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain zig %s: checksum computation failed: %w", version, err)
	}
	if archiveSHA != art.Shasum {
		return Result{}, fmt.Errorf("toolchain zig %s: archive checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", version, art.Shasum, archiveSHA, art.Tarball)
	}
	if err := extractTarXzTree(archivePath, installDir, 1); err != nil {
		paths.ClearUnit(installDir)
		return Result{}, fmt.Errorf("toolchain zig %s: extraction failed: %w", version, err)
	}
	binPath := zigBinPathIn(installRoot, version)
	binSHA, err := fileSHA256(binPath)
	if err != nil {
		paths.ClearUnit(installDir)
		return Result{}, fmt.Errorf("toolchain zig %s: binary not found after extraction at %s", version, binPath)
	}
	meta := Metadata{Tool: "zig", Version: version, Platform: platform, SourceURL: art.Tarball, SHA256: archiveSHA, BinSHA256: binSHA, Trust: TrustChecksum}
	if err := writeMetadataTo(installRoot, "zig", version, meta); err != nil {
		paths.ClearUnit(installDir)
		return Result{}, fmt.Errorf("toolchain zig %s: metadata write failed (install aborted): %w", version, err)
	}
	return Result{Tool: "zig", Version: version, Path: binPath, SourceURL: art.Tarball, SHA256: archiveSHA, BinSHA256: binSHA, Trust: TrustChecksum}, nil
}

// fetchBytes GETs url with the toolchain HTTP client.
func fetchBytes(url string) ([]byte, error) {
	resp, err := httpGet(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// zigWrappers are the compiler driver names a build looks up on PATH, each mapped to
// the zig subcommand it stands for.
var zigWrappers = map[string]string{
	"cc": "cc", "gcc": "cc", "c++": "c++", "g++": "c++", "ar": "ar", "ranlib": "ranlib",
}

// writeZigWrappers writes a driver script for each zigWrappers name into dir, which
// sits beside the zig binary; each finds zig from its own path, so the cache can be
// mounted anywhere.
func writeZigWrappers(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, sub := range zigWrappers {
		body := []byte("#!/bin/sh\nexec \"${0%/*}/../zig\" " + sub + " \"$@\"\n")
		if err := atomicfile.WriteFileIfChanged(filepath.Join(dir, name), body, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// realizeCToolchain realizes the c-toolchain capability with the provisioned Zig,
// presented as cc/c++/gcc/ar on PATH; on a platform Zig publishes no build for it
// reports substrate.ErrUnavailable.
func realizeCToolchain(_ context.Context, rootDir string, n substrate.Need) (substrate.Realized, error) {
	if zigPlatform(runtime.GOOS, runtime.GOARCH) == "" {
		return substrate.Realized{}, substrate.ErrUnavailable
	}
	res, err := Resolve(rootDir, "zig", "")
	if err != nil {
		return substrate.Realized{}, err
	}
	wrappers := filepath.Join(filepath.Dir(res.Path), "wrappers")
	if err := writeZigWrappers(wrappers); err != nil {
		return substrate.Realized{}, fmt.Errorf("zig compiler wrappers: %w", err)
	}
	r := substrate.Realized{
		Need: n, Backend: "zig", Present: res.CacheHit,
		Packages: []substrate.PackageVersion{{Name: "zig", Version: res.Version}},
		PathDirs: []string{wrappers},
		Env:      map[string]string{"CC": "cc", "CXX": "c++"},
	}
	if d := cacheDir("zig"); d != "" {
		r.Env["ZIG_GLOBAL_CACHE_DIR"] = d
	}
	return r, nil
}
