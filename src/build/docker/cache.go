package docker

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// CacheResolution records what was checked during cache setup.
// Populated by the executor after probing, not by flag building.
type CacheResolution struct {
	Mode     string // off | local | shared | hybrid
	Local    string // hit | miss | skipped
	External string // hit | miss | skipped
	Fallback string // hit | miss | skipped
	Result   string // "using local" | "using external (branch)" | "cold build"
	Builder  string // "reused (sf-abc123)" | "created (sf-abc123)"
}

// BuildCacheFlags computes --cache-from and --cache-to flags from config.
// Does NOT determine hits/misses — that's the executor's job after probing.
//
// Invariants enforced here:
//   - Fallback never in cache-to (read-only)
//   - Ref canonicalization: normalized prefix + hash suffix
//   - Precedence ordering: local before external in cache-from list
func BuildCacheFlags(cfg config.BuildCacheConfig, repoID, branch string, registries []config.RegistryConfig, vars map[string]string) (cacheFrom, cacheTo []build.CacheRef) {
	if !cfg.IsActive() {
		return nil, nil
	}

	switch cfg.Mode {
	case "local":
		return localFlags(repoID, cfg.Local)

	case "shared":
		return externalFlags(cfg.External, repoID, branch, registries, vars)

	case "hybrid":
		localFrom, localTo := localFlags(repoID, cfg.Local)
		extFrom, extTo := externalFlags(cfg.External, repoID, branch, registries, vars)
		return append(localFrom, extFrom...), append(localTo, extTo...)
	}

	return nil, nil
}

// localFlags returns BuildKit local cache refs: import from the repo's current cache
// generation, export a new one (promoted by promoteLocalCache after the build). Nil when
// the local cache isn't available.
func localFlags(repoID string, localCfg config.LocalCacheConfig) (cacheFrom, cacheTo []build.CacheRef) {
	if !LocalCacheAvailable(repoID, localCfg) {
		return nil, nil
	}
	dir := LocalCacheDir(repoID, localCfg)
	if _, err := os.Stat(filepath.Join(dir, localCurrent, "index.json")); err == nil {
		cacheFrom = []build.CacheRef{{Type: "local", Ref: filepath.Join(dir, localCurrent), Direction: "import"}}
	}
	cacheTo = []build.CacheRef{{Type: "local", Ref: localNext(dir), Mode: "max", Direction: "export"}}
	return cacheFrom, cacheTo
}

// externalFlags returns BuildKit registry cache refs.
func externalFlags(ext config.ExternalCacheConfig, repoID, branch string, registries []config.RegistryConfig, vars map[string]string) (cacheFrom, cacheTo []build.CacheRef) {
	if ext.Registry == "" {
		return nil, nil
	}

	targetRef := resolveRegistryRef(ext.Registry, registries, vars)
	if targetRef == "" {
		return nil, nil
	}

	// Cache tags use the canonical CacheTag constructor — single source of truth.
	// Format: <prefix>-<repo-hash-8>-<branch-canonical>
	// Repo hash prevents cross-repo collision on shared cache targets.
	mode := ext.Mode
	if mode == "" {
		mode = "max"
	}

	tag := BuildCacheTag(ext.Path, repoID, branch)
	branchRef := fmt.Sprintf("%s:%s", targetRef, tag.String())

	// cache-from: branch first, then fallback.
	cacheFrom = []build.CacheRef{{Type: "registry", Ref: branchRef, Direction: "import"}}
	if ext.Fallback != "" && ext.Fallback != branch {
		fbTag := BuildCacheTag(ext.Path, repoID, ext.Fallback)
		fallbackRef := fmt.Sprintf("%s:%s", targetRef, fbTag.String())
		cacheFrom = append(cacheFrom, build.CacheRef{Type: "registry", Ref: fallbackRef, Direction: "import"})
	}

	// cache-to: branch only. Never fallback. Caller gates on build success.
	cacheTo = []build.CacheRef{{Type: "registry", Ref: branchRef, Mode: mode, Direction: "export"}}

	return cacheFrom, cacheTo
}

// localCurrent is the cache generation builds import from, inside a repo's local cache unit.
const localCurrent = "current"

// localNextPrefix names the generation a running build exports, one per process.
const localNextPrefix = "next-"

func localNext(dir string) string {
	return filepath.Join(dir, localNextPrefix+strconv.Itoa(os.Getpid()))
}

// LocalCacheDir is the repo's local BuildKit cache unit: <path>/<repo-hash> when
// build_cache.local.path is set, else cache/buildkit/<repo-hash> under the cache root;
// "" when neither is available.
func LocalCacheDir(repoID string, cfg config.LocalCacheConfig) string {
	hash := repoHash(repoID)
	if cfg.Path != "" {
		return filepath.Join(cfg.Path, hash)
	}
	if root := paths.ResolveCacheRoot(""); root != "" {
		return paths.Cache(root, "cache", "buildkit", hash)
	}
	return ""
}

// LocalCacheAvailable leases the repo's local cache unit, creating it. A configured path
// is used only when its parent exists, so an unmounted path never fills the container.
func LocalCacheAvailable(repoID string, cfg config.LocalCacheConfig) bool {
	dir := LocalCacheDir(repoID, cfg)
	if dir == "" {
		return false
	}
	if cfg.Path != "" {
		if info, err := os.Stat(filepath.Dir(cfg.Path)); err != nil || !info.IsDir() {
			return false
		}
	}
	return paths.LeaseUnit(dir) == nil
}

// BuilderName returns a deterministic, repo-scoped builder name.
// Prevents cross-repo cache pollution on shared DinD.
func BuilderName(repoID string) string {
	hash := repoHash(repoID)
	return "sf-" + hash[:8]
}

// CanonicalizeRef normalizes a repo or branch name for registry ref construction.
// Uses a normalized prefix (lowercase, safe chars) plus a hash suffix to prevent collisions.
// "feature/a-b" and "feature-a/b" produce different refs because the hash includes the original.
func CanonicalizeRef(s string) string {
	// Normalized human-readable prefix.
	prefix := strings.ToLower(s)
	prefix = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			return r
		}
		return '-'
	}, prefix)
	// Collapse repeated dashes.
	for strings.Contains(prefix, "--") {
		prefix = strings.ReplaceAll(prefix, "--", "-")
	}
	prefix = strings.Trim(prefix, "-")

	// Hash suffix from original (pre-normalization) to prevent collisions.
	h := sha256.Sum256([]byte(s))
	suffix := fmt.Sprintf("%x", h[:4])

	// Registry tags: max 128 chars. Reserve 9 for "-" + 8-char hash.
	if len(prefix) > 119 {
		prefix = prefix[:119]
	}

	return prefix + "-" + suffix
}

// CacheInfo holds resolved cache state for rendering.
// Computed once, rendered by execute.go — cache.go does not render.
type CacheInfo struct {
	Mode     string
	Branch   string
	Fallback string
	Imports  []string // deduped, ordered
	Exports  []string // deduped, ordered
}

// ResolveCacheInfo computes the cache rendering info from the pipeline context.
func ResolveCacheInfo(pc *pipeline.PipelineContext) CacheInfo {
	cfg := pc.Config.BuildCache
	info := CacheInfo{
		Mode: cfg.Mode,
	}

	if !cfg.IsActive() {
		info.Mode = "off"
		return info
	}

	// Branch context from CI env.
	info.Branch = os.Getenv("SF_CI_BRANCH")
	if info.Branch == "" {
		info.Branch = os.Getenv("CI_COMMIT_BRANCH")
	}
	info.Fallback = cfg.External.Fallback
	if info.Fallback == "" {
		info.Fallback = os.Getenv("SF_CI_DEFAULT_BRANCH")
	}

	// Collect and dedupe refs from all steps.
	importSeen := map[string]struct{}{}
	exportSeen := map[string]struct{}{}

	if pc.BuildPlan != nil {
		for _, step := range pc.BuildPlan.Steps {
			for _, cf := range step.CacheFrom {
				if _, dup := importSeen[cf.Ref]; !dup {
					importSeen[cf.Ref] = struct{}{}
					info.Imports = append(info.Imports, cf.Ref)
				}
			}
			for _, ct := range step.CacheTo {
				if _, dup := exportSeen[ct.Ref]; !dup {
					exportSeen[ct.Ref] = struct{}{}
					info.Exports = append(info.Exports, ct.Ref)
				}
			}
		}
	}

	sort.Strings(info.Imports)
	sort.Strings(info.Exports)

	return info
}

// RenderCacheInfo prints structured cache resolution output.
// Called from execute.go — cache.go only resolves, execute.go renders.
func RenderCacheInfo(w io.Writer, color bool, info CacheInfo) {
	sec := output.NewSection(w, "Cache", 0, color)
	sec.Row("%-14s%s", "mode", info.Mode)

	if info.Mode == "off" {
		sec.Close()
		return
	}

	if info.Branch != "" {
		sec.Row("%-14s%s", "branch", info.Branch)
	}
	if info.Fallback != "" {
		sec.Row("%-14s%s", "fallback", info.Fallback)
	}

	for _, ref := range info.Imports {
		sec.Row("%-14s%s", "import", ref)
	}
	for _, ref := range info.Exports {
		sec.Row("%-14s%s", "export", ref)
	}

	sec.Close()
}

// repoHash returns a hex-encoded hash of a repo identity.
func repoHash(repoID string) string {
	h := sha256.Sum256([]byte(repoID))
	return fmt.Sprintf("%x", h[:8])
}

// resolveRegistryRef resolves a registries[].id to its "url/path" cache ref. The registry's
// URL + default_path locate the cache; a synthetic registry-referencing target reuses the
// standard registry resolution (var substitution, provider inference).
func resolveRegistryRef(
	registryID string,
	registries []config.RegistryConfig,
	vars map[string]string,
) string {
	reg, err := config.ResolveRegistryForTarget(config.TargetConfig{Registry: config.StringOrList{registryID}}, registries, vars)
	if err != nil || reg == nil {
		return ""
	}
	url := strings.TrimSuffix(reg.URL, "/")
	path := strings.Trim(reg.Path, "/")
	if path != "" {
		return url + "/" + path
	}
	return url
}
