package toolchain

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

// defaultNodeVersion is the pinned Node.js the deps updater provisions to touch npm.
// An active LTS; bump deliberately (a bot never floats its own toolchain).
const defaultNodeVersion = "22.11.0"

// nodeArch maps GOARCH to Node.js's dist arch token.
func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return goarch
	}
}

// isMuslLibc reports whether the host uses musl libc (e.g. Alpine) rather than glibc.
// It matters because the official Node.js builds at nodejs.org/dist are glibc-linked
// and will NOT execute on a musl system — the glibc ELF interpreter is absent, so the
// kernel returns ENOENT ("cannot execute: No such file or directory") on exec of node.
// Detected by the presence of the musl dynamic loader, which exists on musl systems
// and not on glibc ones.
func isMuslLibc() bool {
	for _, loader := range []string{
		"/lib/ld-musl-x86_64.so.1",
		"/lib/ld-musl-aarch64.so.1",
	} {
		if _, err := os.Stat(loader); err == nil {
			return true
		}
	}
	return false
}

// nodeArtifact selects the download name, source + checksum URLs, cache-key version,
// and platform label for a Node.js distribution. On musl hosts it uses the musl builds
// from unofficial-builds.nodejs.org (nodejs.org/dist has none); otherwise the official
// glibc dist. The cache-key version carries a "-musl" suffix so a glibc tree and a musl
// tree for the same Node version are distinct cache entries and never collide.
func nodeArtifact(version, arch string, musl bool) (downloadName, sourceURL, checksumURL, cacheVer, platform string) {
	platform = "linux/" + arch
	cacheVer = version
	if musl {
		downloadName = fmt.Sprintf("node-v%s-linux-%s-musl.tar.gz", version, arch)
		base := fmt.Sprintf("https://unofficial-builds.nodejs.org/download/release/v%s", version)
		sourceURL = base + "/" + downloadName
		checksumURL = base + "/SHASUMS256.txt"
		cacheVer = version + "-musl"
		platform += "-musl"
		return
	}
	downloadName = fmt.Sprintf("node-v%s-linux-%s.tar.gz", version, arch)
	sourceURL = fmt.Sprintf("https://nodejs.org/dist/v%s/%s", version, downloadName)
	checksumURL = fmt.Sprintf("https://nodejs.org/dist/v%s/SHASUMS256.txt", version)
	return
}

// resolveNode provisions a checksum-verified Node.js DISTRIBUTION (a directory tree —
// node + the bundled npm at bin/npm, which is a symlink into lib/node_modules/npm),
// mirroring resolveGo/resolveRust. node ships a .tar.gz with a per-version SHASUMS256.txt;
// the archive is verified before extraction. The whole tree is kept (npm needs its
// siblings); Result.Path is <cache>/node/<ver>/bin/node, and bin/npm sits beside it.
// On musl hosts the musl build is used (see nodeArtifact) so node actually executes.
func resolveNode(rootDir, version string) (Result, error) {
	if version == "" {
		version = defaultNodeVersion
	}
	arch := nodeArch(runtime.GOARCH)
	downloadName, sourceURL, checksumURL, cacheVer, platform := nodeArtifact(version, arch, isMuslLibc())

	// Cache hit across read roots — the node binary present + metadata-verified. Keyed
	// by cacheVer (libc-variant-aware) so a glibc tree can't be served on a musl host.
	for _, root := range ReadRoots(rootDir) {
		binPath := CacheBinPathIn(root, "node", cacheVer, "node")
		if _, err := os.Stat(binPath); err != nil {
			continue
		}
		meta, metaErr := readMetadataFrom(root, "node", cacheVer)
		if metaErr != nil || meta.BinSHA256 == "" {
			continue
		}
		if actual, hashErr := fileSHA256(binPath); hashErr != nil || actual != meta.BinSHA256 {
			continue
		}
		return Result{Tool: "node", Version: version, Path: binPath, CacheHit: true, SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256, Trust: TrustChecksum}, nil
	}

	installRoot := InstallRoot(rootDir)
	installDir := CacheDirIn(installRoot, "node", cacheVer)
	lock, err := AcquireInstallLock(installDir, 5*time.Minute)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain node %s: %w", version, err)
	}
	defer ReleaseInstallLock(lock)

	binPath := CacheBinPathIn(installRoot, "node", cacheVer, "node")
	// Re-check after acquiring the lock (another process may have installed it).
	if _, err := os.Stat(binPath); err == nil {
		if meta, metaErr := readMetadataFrom(installRoot, "node", cacheVer); metaErr == nil && meta.BinSHA256 != "" {
			if actual, hashErr := fileSHA256(binPath); hashErr == nil && actual == meta.BinSHA256 {
				return Result{Tool: "node", Version: version, Path: binPath, CacheHit: true, SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256, Trust: TrustChecksum}, nil
			}
		}
	}

	expectedSHA, err := fetchChecksumFromURL(checksumURL, downloadName)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain node %s: fetching checksum: %w", version, err)
	}
	archivePath, err := downloadToTemp(sourceURL)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain node %s: download failed: %w", version, err)
	}
	defer os.Remove(archivePath)
	archiveSHA, err := fileSHA256(archivePath)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain node %s: checksum computation failed: %w", version, err)
	}
	if archiveSHA != expectedSHA {
		return Result{}, fmt.Errorf("toolchain node %s: archive checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", version, expectedSHA, archiveSHA, sourceURL)
	}

	if err := extractTarGzTree(archivePath, installDir, 1); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain node %s: extraction failed: %w", version, err)
	}
	if _, err := os.Stat(binPath); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain node %s: binary not found after extraction at %s", version, binPath)
	}
	binSHA, err := fileSHA256(binPath)
	if err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain node %s: binary checksum failed: %w", version, err)
	}
	meta := Metadata{Tool: "node", Version: version, Platform: platform, SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA, Trust: TrustChecksum}
	if err := writeMetadataTo(installRoot, "node", cacheVer, meta); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain node %s: metadata write failed (install aborted): %w", version, err)
	}
	return Result{Tool: "node", Version: version, Path: binPath, CacheHit: false, SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA, Trust: TrustChecksum}, nil
}
