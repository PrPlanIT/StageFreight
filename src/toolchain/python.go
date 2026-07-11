package toolchain

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

// Python is provisioned from python-build-standalone (astral) install_only builds —
// checksum-verified, prefix-relocatable CPython tarballs. Unlike node, pbs couples the
// Python version WITH a release-date tag in the URL, so both are pinned together (a bot
// never floats its own toolchain); bump the pair deliberately.
const (
	defaultPythonVersion = "3.12.7"
	pbsReleaseTag        = "20241016" // the python-build-standalone release providing defaultPythonVersion
)

// pythonArch maps GOARCH to python-build-standalone's arch token.
func pythonArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return goarch
	}
}

// pythonAsset builds the pbs download filename, URL, and per-asset .sha256 URL. Pure —
// separated so URL/arch construction is unit-testable without the network.
func pythonAsset(version, arch string) (name, url, checksumURL string) {
	name = fmt.Sprintf("cpython-%s+%s-%s-unknown-linux-gnu-install_only.tar.gz", version, pbsReleaseTag, arch)
	url = fmt.Sprintf("https://github.com/astral-sh/python-build-standalone/releases/download/%s/%s", pbsReleaseTag, name)
	return name, url, url + ".sha256"
}

// resolvePython provisions a checksum-verified CPython DISTRIBUTION (tree — python3 +
// bundled pip at bin/, lib/), mirroring resolveGo/resolveNode. The install_only tarball
// has a single top-level python/ dir → strip 1 → bin/python3 at the cache binPath.
func resolvePython(rootDir, version string) (Result, error) {
	if version == "" {
		version = defaultPythonVersion
	}
	arch := pythonArch(runtime.GOARCH)
	downloadName, sourceURL, checksumURL := pythonAsset(version, arch)

	for _, root := range ReadRoots(rootDir) {
		binPath := CacheBinPathIn(root, "python", version, "python3")
		if _, err := os.Stat(binPath); err != nil {
			continue
		}
		meta, metaErr := readMetadataFrom(root, "python", version)
		if metaErr != nil || meta.BinSHA256 == "" {
			continue
		}
		if actual, hashErr := fileSHA256(binPath); hashErr != nil || actual != meta.BinSHA256 {
			continue
		}
		return Result{Tool: "python", Version: version, Path: binPath, CacheHit: true, SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256, Trust: TrustChecksum}, nil
	}

	installRoot := InstallRoot(rootDir)
	installDir := CacheDirIn(installRoot, "python", version)
	lock, err := AcquireInstallLock(installDir, 5*time.Minute)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain python %s: %w", version, err)
	}
	defer ReleaseInstallLock(lock)

	binPath := CacheBinPathIn(installRoot, "python", version, "python3")
	if _, err := os.Stat(binPath); err == nil {
		if meta, metaErr := readMetadataFrom(installRoot, "python", version); metaErr == nil && meta.BinSHA256 != "" {
			if actual, hashErr := fileSHA256(binPath); hashErr == nil && actual == meta.BinSHA256 {
				return Result{Tool: "python", Version: version, Path: binPath, CacheHit: true, SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256, Trust: TrustChecksum}, nil
			}
		}
	}

	expectedSHA, err := fetchChecksumFromURL(checksumURL, downloadName)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain python %s: fetching checksum: %w", version, err)
	}
	archivePath, err := downloadToTemp(sourceURL)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain python %s: download failed: %w", version, err)
	}
	defer os.Remove(archivePath)
	archiveSHA, err := fileSHA256(archivePath)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain python %s: checksum computation failed: %w", version, err)
	}
	if archiveSHA != expectedSHA {
		return Result{}, fmt.Errorf("toolchain python %s: archive checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", version, expectedSHA, archiveSHA, sourceURL)
	}

	if err := extractTarGzTree(archivePath, installDir, 1); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain python %s: extraction failed: %w", version, err)
	}
	if _, err := os.Stat(binPath); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain python %s: binary not found after extraction at %s", version, binPath)
	}
	binSHA, err := fileSHA256(binPath)
	if err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain python %s: binary checksum failed: %w", version, err)
	}
	meta := Metadata{Tool: "python", Version: version, Platform: fmt.Sprintf("linux/%s", arch), SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA, Trust: TrustChecksum}
	if err := writeMetadataTo(installRoot, "python", version, meta); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain python %s: metadata write failed (install aborted): %w", version, err)
	}
	return Result{Tool: "python", Version: version, Path: binPath, CacheHit: false, SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA, Trust: TrustChecksum}, nil
}
