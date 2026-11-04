package toolchain

import (
	"os"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/atomicfile"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// cacheSubdir is the workspace-local (repo-relative) fallback for toolchain installs,
// used only when no persistent cache root resolves. A git checkout wipes it every job,
// so it is genuinely a last resort. The persistent root is resolved via
// paths.ResolveCacheRoot (dind mount → native XDG → none).
const cacheSubdir = ".stagefreight/toolchains"

// PersistentCacheRoot returns the resolved out-of-tree toolchains cache root — the dind
// mount (/stagefreight/toolchains) or the native XDG cache — for display and du. Falls
// back to the nominal default when no persistent backing is resolved.
func PersistentCacheRoot() string {
	if r := paths.ResolveCacheRoot(""); r != "" {
		return paths.Cache(r, "toolchains")
	}
	return paths.Cache(paths.DefaultCacheRoot, "toolchains")
}

// ReadRoots returns cache roots to search for existing toolchain installs, in priority
// order: the resolved persistent root first (mount or XDG), then the workspace-local
// fallback.
func ReadRoots(rootDir string) []string {
	var roots []string
	if r := paths.ResolveCacheRoot(""); r != "" {
		roots = append(roots, paths.Cache(r, "toolchains"))
	}
	roots = append(roots, filepath.Join(rootDir, cacheSubdir))
	return roots
}

// InstallRoot returns the directory where new toolchain installs are written — the
// resolved persistent root when writable, else the workspace-local fallback. The
// persistent mount is supplied empty, so its toolchains/ subdir is created on first use.
// Without a persistent root the fallback is used, which the git checkout wipes every job,
// forcing a fresh download each run — hence the resolver prefers the mount/XDG.
func InstallRoot(rootDir string) string {
	if r := paths.ResolveCacheRoot(""); r != "" {
		if tc := paths.Cache(r, "toolchains"); ensureWritableDir(tc) {
			return tc
		}
	}
	return filepath.Join(rootDir, cacheSubdir)
}

// CacheRoot is the persistent build-cache root (<resolved>/cache), a sibling of the
// toolchains root — or "" when no persistent backing is available (the build then falls
// back to Go's ephemeral $HOME caches). For display; resolving may create the root dir.
func CacheRoot() string {
	if r := paths.ResolveCacheRoot(""); r != "" {
		return paths.Cache(r, "cache")
	}
	return ""
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

// cacheDir returns <CacheRoot>/<elem...>, created and leased (paths.LeaseUnit) — or ""
// when the mount is unavailable. The single place that resolves the root, joins, and mkdirs; every cache
// path flows through it, so each public accessor declares ONLY its own subdir names
// (the irreducible layout) with no repeated plumbing.
func cacheDir(elem ...string) string {
	root := ensureCacheRoot()
	if root == "" {
		return ""
	}
	dir := filepath.Join(append([]string{root}, elem...)...)
	if paths.LeaseUnit(dir) != nil {
		return ""
	}
	return dir
}

// ContainerCacheDir returns a persistent cache dir for a containerized builder — a
// sibling of the Go/Rust caches under the same mount (e.g. node/pnpm-store) — created,
// or "" when no persistent mount is available (local dev), in which case the containerized
// build runs cold. A step built on BuildKit keeps its store in a cache mount instead.
func ContainerCacheDir(elem ...string) string {
	return cacheDir(elem...)
}

// GoCacheDirs returns the persistent GOMODCACHE (go/downloads) and GOCACHE (go/build),
// so a build reuses downloaded modules and compiled packages across CI jobs instead of
// paying the full cold-cache cost every run. ("", "") on local dev (no mount) leaves
// Go's default $HOME caches untouched.
func GoCacheDirs() (gomod, gocache string) {
	return cacheDir("go", "downloads"), cacheDir("go", "build")
}

// writeCacheReadme keeps a README at the cache root so an operator browsing the mount
// knows what each directory is and how to reclaim it. It lives in the mount, not the
// repo: documenting the volume in place means the repo needs no doc for it.
func writeCacheReadme(root string) {
	_ = atomicfile.WriteFileIfChanged(filepath.Join(root, "README.md"), []byte(cacheReadme), 0o644)
}

const cacheReadme = `# StageFreight build caches

Persistent caches reused across CI runs. Reclaim space with
'stagefreight prune --confirm': it skips what a running job is using (a directory
holding a .lease is leased by the jobs using it). Deleting by hand while jobs run
can break them; with none running, anything here can go and is rebuilt cold.

  go/downloads        Go module downloads (GOMODCACHE)
  go/build            Go compiled-package cache (GOCACHE)
  rust/downloads      Rust crate sources + registry index (CARGO_HOME)
  rust/build/<proj>   Rust compiled artifacts (CARGO_TARGET_DIR), per project
  node/pnpm-store     pnpm content-addressed store (PNPM_STORE_DIR)
  node/npm-cache      npm cache (npm_config_cache)
  node/yarn-cache     yarn cache (YARN_CACHE_FOLDER)
  substrate/apk       Native build packages (cmake, perl, git, ...)
  zig                 Zig compiler cache (the provisioned C toolchain)
  trivy, grype        Vulnerability scanner databases
  lint/<proj>         Lint results, per project
  buildkit/<proj>     Local BuildKit cache, per project

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
