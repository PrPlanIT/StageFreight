package toolchain

import (
	"os"
	"path/filepath"
)

const (
	cacheSubdir    = ".stagefreight/toolchains"
	persistentRoot = "/stagefreight/toolchains"
)

// PersistentCacheRoot returns the persistent cache root path constant.
func PersistentCacheRoot() string { return persistentRoot }

// ReadRoots returns cache roots to search for existing toolchain installs,
// in priority order. Persistent mount is checked first (operator-preseeded
// or previously written), then workspace-local.
func ReadRoots(rootDir string) []string {
	var roots []string
	if info, err := os.Stat(persistentRoot); err == nil && info.IsDir() {
		roots = append(roots, persistentRoot)
	}
	roots = append(roots, filepath.Join(rootDir, cacheSubdir))
	return roots
}

// InstallRoot returns the directory where new toolchain installs are written.
// Prefers persistent mount if writable, otherwise workspace-local.
//
// The persistent mount (/stagefreight) is supplied to every CI job container
// empty, so its toolchains/ subdir does not pre-exist — we create it on first
// use. Without that, the resolver fell back to the workspace-local cache, which
// the git checkout wipes every job, forcing a fresh Go SDK download each run.
func InstallRoot(rootDir string) string {
	if ensureWritableDir(persistentRoot) {
		return persistentRoot
	}
	return filepath.Join(rootDir, cacheSubdir)
}

// GoCacheDirs returns absolute GOMODCACHE and GOCACHE paths on the persistent
// runner mount, creating them, so a binary build reuses downloaded modules and
// compiled packages across CI jobs instead of paying the full cold-cache cost
// (module download + cross-compile, 170s+) every run. The Dockerfile reserves
// /stagefreight/cache for exactly this ("mount a volume here for cross-run
// reuse"); the container filesystem and the git workspace are both ephemeral.
//
// Returns ("", "") when the mount is absent or unwritable (local dev), which
// signals the caller to leave Go's default $HOME-based caches untouched — those
// already persist across local runs.
func GoCacheDirs() (gomod, gocache string) {
	root := filepath.Join(persistentRoot, "..", "cache") // /stagefreight/cache
	if !ensureWritableDir(root) {
		return "", ""
	}
	gomod = filepath.Join(root, "go-mod")
	gocache = filepath.Join(root, "go-build")
	if os.MkdirAll(gomod, 0o755) != nil || os.MkdirAll(gocache, 0o755) != nil {
		return "", ""
	}
	return gomod, gocache
}

// CacheBinPathIn returns the binary path for a tool within a specific cache root.
func CacheBinPathIn(root, tool, version, binary string) string {
	return filepath.Join(root, tool, version, "bin", binary)
}

// CacheDirIn returns the versioned install directory within a specific cache root.
func CacheDirIn(root, tool, version string) string {
	return filepath.Join(root, tool, version)
}

// MetadataPathIn returns the metadata file path within a specific cache root.
func MetadataPathIn(root, tool, version string) string {
	return filepath.Join(root, tool, version, ".metadata.json")
}

// isWritable returns true if the directory exists, is a directory, and is writable.
func isWritable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	tmp := filepath.Join(dir, ".sf-probe")
	f, err := os.Create(tmp)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(tmp)
	return true
}

// GoCacheStatus returns the resolved GOMODCACHE/GOCACHE plus whether the build
// cache is already warm (a prior run populated it). gomod=="" means no
// persistent mount is available, so the build will fall back to Go's ephemeral
// $HOME caches — surfaced as an explicit "cache off" row. warm=true means the
// persistent GOCACHE already holds compiled packages, so this build reuses them
// (the fast path); cold means the build is populating the cache for next time.
func GoCacheStatus() (gomod, gocache string, warm bool) {
	gomod, gocache = GoCacheDirs()
	if gocache == "" {
		return "", "", false
	}
	// Go's build cache stores compiled objects under hashed subdirectories; the
	// presence of any subdir means a previous build wrote into this cache.
	entries, _ := os.ReadDir(gocache)
	for _, e := range entries {
		if e.IsDir() {
			return gomod, gocache, true
		}
	}
	return gomod, gocache, false
}

// ensureWritableDir creates dir (and parents) and reports whether it is
// writable. Unlike isWritable it does NOT require dir to pre-exist: the
// persistent /stagefreight volume is mounted empty, so its subdirs must be
// created on first use — otherwise every job falls back to ephemeral storage
// and re-pays the cold-cache cost. On a host without the mount (local dev,
// unprivileged) MkdirAll fails and we fall back, leaving behaviour unchanged.
func ensureWritableDir(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	return isWritable(dir)
}
