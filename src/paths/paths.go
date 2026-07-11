// Package paths is the single source of truth for where StageFreight writes on
// disk. Every path StageFreight produces belongs to exactly one BUCKET with a
// distinct lifecycle, and all writes route through this package so a bucket's
// physical location is declared ONCE — renaming or re-backing a bucket is a change
// here, not a sweep across the codebase. This package imports only the standard
// library, so anything may depend on it without risking an import cycle.
//
// The four buckets, four lifecycles:
//
//	Durable — committed repo state (badges/, preset-cache/, toolchains.lock). Tracked;
//	          travels with the repo; reviewed. The ONLY bucket that is ever committed.
//	Scratch — per-run pipeline I/O (handoff artifacts, reports, dist). In-tree ONLY
//	          because forge artifact/report paths must live under the project dir;
//	          self-ignored, forwarded between CI stages, discarded each run.
//	Cache   — cross-run reusable cache (Go build caches, toolchain binaries). OUT of the
//	          repo tree; backed by a persistent volume (self-hosted), the forge cache
//	          mechanism (hosted), or nothing (cold). NEVER a correctness dependency.
//	State   — host/deployment persistent state (signing/KMS). Machine-scoped, not per-repo.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// CacheRootEnv overrides the Cache bucket's backing root. Set it when neither the
// default mount nor the XDG cache is right for the host — e.g. a hosted CI runner points
// it at a forge-cacheable in-workspace path so actions/cache or GitLab cache: can persist it.
const CacheRootEnv = "SF_CACHE_ROOT"

const (
	// Root is the StageFreight-owned in-tree namespace — the Durable bucket, and the
	// parent of Scratch. Both live under it because CI forwards only this one prefix
	// (GitLab artifacts/reports paths must resolve under $CI_PROJECT_DIR).
	Root = ".stagefreight"

	// ScratchName is the Scratch subdirectory under Root. Dot-prefixed so it is hidden
	// from a plain listing (Durable state stays prominent) and self-ignored via its own
	// .gitignore, so no root .gitignore edit is needed to keep run I/O out of commits.
	ScratchName = ".tmp"

	// DefaultCacheRoot is the default mount for the Cache bucket. Overridable per host
	// (a runner may mount elsewhere); on a hosted runner the forge cache maps onto it.
	// Correctness never depends on it — a cold run just recomputes.
	DefaultCacheRoot = "/stagefreight"

	// StateRoot is host/deployment state (signing/KMS): machine-scoped and persistent,
	// never per-repo.
	StateRoot = "/var/lib/stagefreight"
)

// Durable returns a path in the committed in-tree namespace. With rootDir set the path
// is anchored under the repo; with rootDir "" it is repo-relative — the form many call
// sites and CI specs use. sub segments are joined beneath Root.
func Durable(rootDir string, sub ...string) string {
	return under(rootDir, append([]string{Root}, sub...))
}

// Ephemeral returns a path for a per-run pipeline OUTPUT under the namespace (reports,
// scan results, manifests, the perform→publish handoff, dist). It resolves to the same
// place as Durable — flat under Root, kept top-level for investigatability — but names
// the opposite lifecycle: these are regenerated every run and gitignored (the workspace
// ignore is an allowlist, so anything NOT durable is ignored by default). The commit
// distinction is enforced by workspace.persistentEntries, not the path; this accessor is
// the single seam through which every ephemeral output flows, so relocating them later
// (e.g. under ScratchName) is one edit here.
func Ephemeral(rootDir string, sub ...string) string {
	return under(rootDir, append([]string{Root}, sub...))
}

// Scratch returns a path in the per-run scratch tree (Root/ScratchName). Same rootDir
// convention as Durable. Everything here is gitignored and safe to delete.
func Scratch(rootDir string, sub ...string) string {
	return under(rootDir, append([]string{Root, ScratchName}, sub...))
}

// Cache returns a path in the cross-run cache under cacheRoot (resolve it with
// ResolveCacheRoot). The path is absolute and never under the repo tree.
func Cache(cacheRoot string, sub ...string) string {
	if cacheRoot == "" {
		cacheRoot = DefaultCacheRoot
	}
	return filepath.Join(append([]string{cacheRoot}, sub...)...)
}

// State returns a path in host/deployment state. Absolute and machine-scoped.
func State(sub ...string) string {
	return filepath.Join(append([]string{StateRoot}, sub...)...)
}

// ScratchRelDir is the repo-relative scratch root (Root/ScratchName) — the prefix CI
// forwards and the directory whose self-ignoring .gitignore is planted.
func ScratchRelDir() string { return filepath.Join(Root, ScratchName) }

// ResolveCacheRoot picks the Cache bucket's backing root, in priority order:
//
//  1. explicit — a --cache flag value (honored verbatim, any environment)
//  2. the SF_CACHE_ROOT env (honored verbatim)
//  3. DefaultCacheRoot (/stagefreight) if writable — the dind mount convention (the host
//     path is bind-mounted here, so in-container it is always /stagefreight)
//  4. the XDG user cache (~/.cache/stagefreight) if writable — NATIVE execution, shared
//     across every repo instead of a per-repo in-tree fallback
//
// Returns "" when none is usable, so a caller falls back per its own policy (toolchains
// to a workspace dir, build caches to Go's ephemeral $HOME). Only the mount and XDG tiers
// touch the filesystem; an explicit path or env is trusted as-is (the operator asked for
// it). The probed dir is created — the mount is supplied empty and the XDG cache is the
// native home we want to establish.
func ResolveCacheRoot(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := strings.TrimSpace(os.Getenv(CacheRootEnv)); v != "" {
		return v
	}
	if writableDir(DefaultCacheRoot) {
		return DefaultCacheRoot
	}
	if xdg := xdgCacheDir(); xdg != "" && writableDir(xdg) {
		return xdg
	}
	return ""
}

// xdgCacheDir is the native user cache root: $XDG_CACHE_HOME/stagefreight, else
// ~/.cache/stagefreight. Empty when neither resolves (no HOME).
func xdgCacheDir() string {
	if x := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); x != "" {
		return filepath.Join(x, "stagefreight")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "stagefreight")
	}
	return ""
}

// writableDir creates dir (with parents) if absent and reports whether it can be written
// — so probing an empty mount or a fresh XDG cache establishes it.
func writableDir(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	probe := filepath.Join(dir, ".sf-probe")
	f, err := os.Create(probe)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}

func under(rootDir string, parts []string) string {
	p := filepath.Join(parts...)
	if rootDir == "" {
		return p
	}
	return filepath.Join(rootDir, p)
}
