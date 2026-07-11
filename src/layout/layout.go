// Package layout is the single source of truth for where StageFreight writes on
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
package layout

import "path/filepath"

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

// ResolveCacheRoot picks the Cache bucket's backing: an explicit path (a flag) wins;
// otherwise the default mount. A hosted runner passes the path its forge cache restores
// to; a bare invocation gets the default. Kept trivial on purpose — the resolution
// policy lives with the caller that knows the runner, not in the leaf.
func ResolveCacheRoot(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return DefaultCacheRoot
}

func under(rootDir string, parts []string) string {
	p := filepath.Join(parts...)
	if rootDir == "" {
		return p
	}
	return filepath.Join(rootDir, p)
}
