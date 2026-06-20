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

// CacheRoot is the persistent build-cache mount root (/stagefreight/cache), a sibling
// of the toolchains root. Path only — side-effect-free, for display. The Dockerfile
// reserves this mount for cross-run reuse; the container filesystem and the git
// workspace are both ephemeral.
func CacheRoot() string {
	return filepath.Join(persistentRoot, "..", "cache")
}

// ensureCacheRoot returns the cache root once writable, creating it and dropping the
// layout doc on first use — or "" when the mount is unavailable (local dev).
func ensureCacheRoot() string {
	root := CacheRoot()
	if !ensureWritableDir(root) {
		return ""
	}
	writeCacheReadme(root)
	return root
}

// cacheDir returns <CacheRoot>/<elem...>, created — or "" when the mount is
// unavailable. The single place that resolves the root, joins, and mkdirs; every cache
// path flows through it, so each public accessor declares ONLY its own subdir names
// (the irreducible layout) with no repeated plumbing.
func cacheDir(elem ...string) string {
	root := ensureCacheRoot()
	if root == "" {
		return ""
	}
	dir := filepath.Join(append([]string{root}, elem...)...)
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	return dir
}

// GoCacheDirs returns the persistent GOMODCACHE (go/downloads) and GOCACHE (go/build),
// so a build reuses downloaded modules and compiled packages across CI jobs instead of
// paying the full cold-cache cost every run. ("", "") on local dev (no mount) leaves
// Go's default $HOME caches untouched.
func GoCacheDirs() (gomod, gocache string) {
	return cacheDir("go", "downloads"), cacheDir("go", "build")
}

// writeCacheReadme drops a README at the cache root so an operator browsing the mount
// knows what each directory is — and that any of it is safe to delete (forcing a
// one-time cold rebuild). Written once, never overwritten. Lives in the MOUNT, not the
// repo: documenting the volume in place means the repo needs no doc for it.
func writeCacheReadme(root string) {
	path := filepath.Join(root, "README.md")
	if _, err := os.Stat(path); err == nil {
		return
	}
	_ = os.WriteFile(path, []byte(cacheReadme), 0o644)
}

const cacheReadme = `# StageFreight build caches

Persistent caches reused across CI runs (this mount is supplied empty each job).
Deleting any subdirectory is safe — it only forces a one-time cold rebuild.

  go/downloads        Go module downloads (GOMODCACHE)
  go/build            Go compiled-package cache (GOCACHE)
  rust/downloads      Rust crate sources + registry index (CARGO_HOME)
  rust/build/<proj>   Rust compiled artifacts (CARGO_TARGET_DIR), per project
  substrate/apk       Native build packages (cc, cmake, perl, git, ...)

NOT here: dependency-update results live in the repo at .stagefreight/deps
(a per-run, committed report) — unrelated to these download caches.
`

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
