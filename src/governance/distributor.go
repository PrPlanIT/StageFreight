package governance

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/presetref"

	"gopkg.in/yaml.v3"
)

// PlanDistribution computes what files need to change for each governed repo.
// Pure planning — does NOT write anything.
// Reads current state from forge to detect drift and determine actions.
// PresetSourceInfo holds the forge coordinates for preset resolution.
// Injected into satellite .stagefreight.yml so repos can resolve presets independently.
type PresetSourceInfo struct {
	Provider  string // "gitlab", "github", "gitea"
	ForgeURL  string // HTTPS base URL (e.g., "https://gitlab.prplanit.com")
	ProjectID string // "org/repo" or "org/group/repo"
	Ref       string // "" = the source default branch (tracked); a ref pins
}

// AssetFetcher fetches a file from a repo at a specific ref.
type AssetFetcher func(repoURL, ref, path string) ([]byte, error)

func PlanDistribution(
	gov *GovernanceConfig,
	presetLoader PresetLoader,
	assetFetcher AssetFetcher,
	forgeReader ForgeReader,
	presetSource PresetSourceInfo,
	sourceIdentity string, // for seal header display
) ([]DistributionPlan, error) {

	var plans []DistributionPlan

	for _, cluster := range gov.Profiles {
		baseConfig := deepCopyMap(cluster.Config)

		// Qualify unqualified preset references so the satellite resolves them from the
		// source each run, rather than reading whichever copy it was last handed. A
		// reference that already names its own source passes through untouched.
		govSource := NewPresetQualifier(presetSource)
		govSource.QualifyConfig(baseConfig)

		seal := SealMeta{
			SourceRepo: sourceIdentity,
			SourceRef:  presetSource.Ref,
			ProfileID:  cluster.ID,
		}

		// Collect preset files referenced in the profile config for cache distribution.
		// A catalog entry's per-repo config: override is collected separately, per repo
		// (below): its presets belong to that satellite alone, not to every member.
		presetFiles, err := loadPresetCache(collectPresetPaths(cluster.Config), presetLoader, cluster.ID, govSource)
		if err != nil {
			return nil, err
		}

		// Per-repo: merge satellite-owned vars, render sealed config, plan files.
		// Sealed content is per-repo because each satellite may have different local vars.
		for _, entry := range cluster.Repos {
			repo := entry.At
			plan := DistributionPlan{Repo: repo, Credentials: cluster.Credentials}

			// Merge satellite-owned vars into governance vars.
			// Governance keys are authoritative. Undeclared satellite keys are preserved.
			repoConfig := deepCopyMap(baseConfig)
			// A per-repo config override (a deviating catalog entry) merges over the base.
			if entry.Config != nil {
				// Qualified on a copy: the entry's own map is shared across the catalog,
				// and its preset paths are still needed unqualified to read the files
				// out of the control repo below.
				override := deepCopyMap(entry.Config)
				govSource.QualifyConfig(override)
				mergeConfigOverride(repoConfig, override)
			}
			// A branded catalog entry governs identity: carry its metadata into the
			// satellite config as the metadata: block. org ALWAYS derives from the
			// entry's location (a location-only entry still gets metadata.org — that is
			// a coordinate, not branding) so the satellite resolves {org.*}/{path.*}
			// at its own load without re-declaring anything.
			meta := entry.Metadata
			if meta == nil {
				meta = map[string]any{}
			}
			if _, ok := meta["org"]; !ok {
				if org := orgFromLocation(entry.At); org != "" {
					meta["org"] = org
				}
			}
			repoConfig["metadata"] = meta

			// The catalog entry's location is AUTHORITATIVE, so the satellite's repos
			// section is CONCRETIZED here — no shared repos preset with var-holes, no
			// slug circularity. The primary forge id defaults to the governance
			// source's provider; a mirror is emitted when the entry's org declares a
			// forge-named alias (e.g. github: PrPlanIT → a github mirror at
			// <alias>/<slug>). The default branch is read from the forge, never assumed.
			repoConfig["repos"] = concretizeRepos(entry, cluster.Config, presetSource.Provider, forgeReader, repo)

			mergeSatelliteVars(repoConfig, forgeReader, repo)

			sealedContent, err := RenderSealedConfig(seal, repoConfig)
			if err != nil {
				return nil, fmt.Errorf("cluster %q repo %q: rendering sealed config: %w", cluster.ID, repo, err)
			}

			// A deviating entry's own presets: distributed with the config that
			// references them. Without this the satellite receives a config pointing at
			// a preset that was never seeded, and fails at load — the entry's override
			// lives outside cluster.Config, so the profile-level collection cannot see it.
			//
			// Resolved BEFORE the config is planned because the validation below loads
			// the rendered config against this cache: a governed config references its
			// presets by path, so verifying it without them proves nothing.
			repoPresetFiles := presetFiles
			if entry.Config != nil {
				overrideFiles, oerr := loadPresetCache(collectPresetPaths(entry.Config), presetLoader, cluster.ID, govSource)
				if oerr != nil {
					return nil, oerr
				}
				if len(overrideFiles) > 0 {
					repoPresetFiles = make(map[string][]byte, len(presetFiles)+len(overrideFiles))
					for k, v := range presetFiles {
						repoPresetFiles[k] = v
					}
					for k, v := range overrideFiles {
						repoPresetFiles[k] = v
					}
				}
			}

			// Verify the render before planning it. A config that cannot load must not
			// leave the control repo: distribution is fleet-wide and simultaneous, so an
			// invalid render fails every governed repo at once, at audition, before
			// anything runs. Better one failed reconcile than nine broken satellites.
			satelliteCfg, err := loadSatelliteConfig(repo, sealedContent, repoPresetFiles)
			if err != nil {
				return nil, fmt.Errorf("cluster %q: %w", cluster.ID, err)
			}

			// Sealed .stagefreight.yml — section presets preserved, vars resolved.
			plan.Files = append(plan.Files, planFile(
				forgeReader, repo,
				".stagefreight.yml",
				sealedContent,
			))

			// The CI file is DERIVED from the config just planned, so it ships with it.
			// Distributing the config alone leaves the satellite self-inconsistent — its
			// committed pipeline no longer matches its source — and audition rejects that
			// as stale CI, which is a fleet-wide breakage nobody can fix without
			// re-rendering every repo by hand. Rendered from satelliteCfg so it is the
			// same config the satellite loads, never a re-derivation that could drift.
			// Skipped entirely when no ci.forge is declared: see renderSatelliteCI.
			ciFiles, err := renderSatelliteCI(repo, satelliteCfg)
			if err != nil {
				return nil, fmt.Errorf("cluster %q: %w", cluster.ID, err)
			}
			for _, f := range ciFiles {
				plan.Files = append(plan.Files, planFile(forgeReader, repo, f.Path, f.Content))
			}

			// Preset cache files — 1:1 copies for runtime resolution.
			for cachePath, cacheContent := range repoPresetFiles {
				plan.Files = append(plan.Files, planFile(
					forgeReader, repo,
					cachePath,
					cacheContent,
				))
			}

			// Resolve declared assets from the cluster's stagefreight config.
			if assetFetcher != nil {
				if assetList, ok := cluster.Config["assets"].([]any); ok {
					for _, raw := range assetList {
						asset, ok := raw.(map[string]any)
						if !ok {
							continue
						}
						target, _ := asset["target"].(string)
						source, _ := asset["source"].(map[string]any)
						if target == "" || source == nil {
							continue
						}
						repoURL, _ := source["repo_url"].(string)
						ref, _ := source["ref"].(string)
						srcPath, _ := source["path"].(string)
						if repoURL == "" || srcPath == "" {
							continue
						}
						if ref == "" {
							ref = "main"
						}
						content, err := assetFetcher(repoURL, ref, srcPath)
						if err != nil {
							return nil, fmt.Errorf("cluster %q: fetching asset %q from %s@%s:%s: %w",
								cluster.ID, target, repoURL, ref, srcPath, err)
						}
						plan.Files = append(plan.Files, planFile(
							forgeReader, repo,
							target,
							content,
						))
					}
				}
			}

			plans = append(plans, plan)
		}
	}

	return plans, nil
}

// loadPresetCache resolves preset reference paths into their cache-relative contents,
// so a satellite can resolve them offline. Shared by the profile config and each
// deviating entry's override — every place a preset reference can legitimately appear
// must be seeded, or the satellite loads a config it cannot resolve.
func loadPresetCache(paths []string, loader PresetLoader, clusterID string, src PresetQualifier) (map[string][]byte, error) {
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		// The file is read from the control repo by its LOCAL path, but retained under
		// the key the satellite's resolver will ask for — CacheKey of the qualified
		// reference. Storing it by local path instead would miss on every lookup, and a
		// miss is not cosmetic: the retained copy IS the fallback when the source is
		// unreachable.
		cachePath, err := sanitizePresetCachePath(retentionKey(src.Qualify(p)))
		if err != nil {
			return nil, fmt.Errorf("cluster %q: %w", clusterID, err)
		}
		data, err := loadPresetContent(p, loader)
		if err != nil {
			return nil, fmt.Errorf("cluster %q: loading preset %q for cache: %w", clusterID, p, err)
		}
		out[cachePath] = data
		// Retain what the source pointed at, beside the content. CI clones fresh every
		// job, so a revision recorded only at runtime is gone by the next one and the
		// satellite re-transfers every source to learn nothing changed. Seeding it is
		// what makes the cheap re-check work anywhere but a developer's laptop.
		if rev := materializedRevision(src.Qualify(p)); rev != "" {
			if rp, rerr := sanitizePresetCachePath(retentionKey(src.Qualify(p)) + presetref.RevisionSuffix); rerr == nil {
				out[rp] = []byte(rev)
			}
		}
	}
	return out, nil
}

// orgFromLocation derives the org id from a repo location — the immediate group segment
// (the part before the final /repo). "HomeLabHD/repo" → "HomeLabHD";
// "PrPlanIT/HomeLabHD/repo" → "HomeLabHD"; a bare "repo" → "".
func orgFromLocation(at string) string {
	i := strings.LastIndexByte(at, '/')
	if i <= 0 {
		return ""
	}
	group := at[:i]
	if j := strings.LastIndexByte(group, '/'); j >= 0 {
		return group[j+1:]
	}
	return group
}

// slugFromLocation returns the repo slug — the final path segment of a location.
// "HomeLabHD/prometheus-eaton-ups-exporter" → "prometheus-eaton-ups-exporter".
func slugFromLocation(at string) string {
	if i := strings.LastIndexByte(at, '/'); i >= 0 {
		return at[i+1:]
	}
	return at
}

// concretizeRepos builds the satellite's CONCRETE repos section from the catalog
// entry's authoritative location: primary = the location on the entry's forge (default:
// the governance source's provider), plus a mirror for each forge-named org alias
// (alias key = forge id, alias value = that forge's org path). The default branch is
// read from the forge when a reader is available; "main" otherwise.
func concretizeRepos(entry CatalogEntry, clusterConfig map[string]any, sourceProvider string, reader ForgeReader, repo string) map[string]any {
	forgeID := entry.Forge
	if forgeID == "" {
		forgeID = sourceProvider
	}
	branch := "main"
	if reader != nil {
		if b, err := reader.DefaultBranch(repo); err == nil && b != "" {
			branch = b
		}
	}
	repos := map[string]any{
		"primary": map[string]any{
			"forge":    forgeID,
			"project":  entry.At,
			"roles":    []any{"primary"},
			"branches": map[string]any{"default": branch},
			"worktree": ".",
		},
	}
	slug := slugFromLocation(entry.At)
	org := orgFromLocation(entry.At)
	for alias, val := range orgAliases(clusterConfig, org) {
		if alias != "github" || val == "" || slug == "" {
			continue // mirrors exist for forge-named aliases; github is the one we run
		}
		repos[alias+"-mirror"] = map[string]any{
			"forge":   alias,
			"project": val + "/" + slug,
			"roles":   []any{"mirror", "publish-origin"},
			"sync":    map[string]any{"git": true, "releases": true},
		}
	}
	return repos
}

// orgAliases reads an org's aliases out of the profile's raw config map
// (config["orgs"][org]["aliases"]) — the same orgs block the satellite receives.
func orgAliases(clusterConfig map[string]any, orgID string) map[string]string {
	out := map[string]string{}
	orgs, _ := clusterConfig["orgs"].(map[string]any)
	org, _ := orgs[orgID].(map[string]any)
	aliases, _ := org["aliases"].(map[string]any)
	for k, v := range aliases {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// mergeConfigOverride shallow-merges a catalog entry's per-repo config over the
// profile's base config: each top-level section the entry declares replaces the base's
// (the entry deviates for that section — e.g. a different forge via its own repos preset).
func mergeConfigOverride(base, override map[string]any) {
	for k, v := range override {
		base[k] = v
	}
}

// ForgeReader reads current file content from a remote repo.
// Used to detect drift and determine create vs update actions.
type ForgeReader interface {
	GetFileContent(repo, path, ref string) ([]byte, error)
	// DefaultBranch reports the repo's default branch — read from the forge so the
	// concretized repos section never assumes it.
	DefaultBranch(repo string) (string, error)
}

// planFile determines the action for a single file in a target repo.
func planFile(reader ForgeReader, repo, path string, newContent []byte) DistributedFile {
	f := DistributedFile{
		Path:    path,
		Content: newContent,
	}

	if reader == nil {
		// No reader available — assume create.
		f.Action = "create"
		return f
	}

	existing, err := reader.GetFileContent(repo, path, "HEAD")
	if err != nil {
		// File doesn't exist — create.
		f.Action = "create"
		return f
	}

	if bytes.Equal(existing, newContent) {
		f.Action = "unchanged"
		return f
	}

	// File exists but differs — governance replaces drifted files.
	f.Action = "replace"
	f.Drifted = true

	return f
}

// mergeSatelliteVars reads the satellite repo's existing .stagefreight.yml,
// extracts its vars, and merges satellite-owned keys into the governance config.
// Governance keys are authoritative (already in config). Satellite keys that
// governance does not declare are preserved. This implements the var ownership contract:
//   - governance-declared keys → governance-owned
//   - undeclared keys → satellite-owned, preserved
func mergeSatelliteVars(config map[string]any, reader ForgeReader, repo string) {
	if reader == nil {
		return
	}

	existing, err := reader.GetFileContent(repo, ".stagefreight.yml", "HEAD")
	if err != nil {
		return // no existing config — nothing to merge
	}

	var parsed struct {
		Vars map[string]any `yaml:"vars"`
	}
	if err := yaml.Unmarshal(existing, &parsed); err != nil {
		fmt.Fprintf(os.Stderr, "  warn: %s: failed to parse existing config for vars merge: %v\n", repo, err)
		return
	}
	if parsed.Vars == nil {
		return
	}

	// Get or create the governance vars map.
	govVars, _ := config["vars"].(map[string]any)
	if govVars == nil {
		govVars = make(map[string]any)
	}

	// Merge with ownership contract enforcement:
	// - governance-declared keys are authoritative
	// - undeclared satellite keys are preserved
	// - ownership takeover (governance now declares a key the satellite had) is logged
	for k, satelliteVal := range parsed.Vars {
		govVal, governed := govVars[k]
		if !governed {
			// Satellite-owned key — preserve.
			govVars[k] = satelliteVal
		} else if fmt.Sprintf("%v", govVal) != fmt.Sprintf("%v", satelliteVal) {
			// Ownership takeover — governance now declares a key that existed locally
			// with a different value. Governance wins, but log the takeover.
			fmt.Fprintf(os.Stderr, "  drift: %s: var %q governance=%v satellite=%v (governance wins)\n",
				repo, k, govVal, satelliteVal)
		}
	}

	config["vars"] = govVars
}

// collectPresetPaths recursively walks a config and returns all unique preset: reference paths.
func collectPresetPaths(config map[string]any) []string {
	seen := map[string]struct{}{}
	var paths []string

	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if p, ok := t["preset"].(string); ok && p != "" {
				if _, dup := seen[p]; !dup {
					seen[p] = struct{}{}
					paths = append(paths, p)
				}
			}
			// presets: [a, b] — ordered composition on a keyed section. Each entry is a
			// preset path that must be cached too, or a tracking satellite can't resolve
			// the composed section (open ...preset-cache/<path>: no such file).
			if list, ok := t["presets"].([]any); ok {
				for _, item := range list {
					if p, ok := item.(string); ok && p != "" {
						if _, dup := seen[p]; !dup {
							seen[p] = struct{}{}
							paths = append(paths, p)
						}
					}
				}
			}
			for _, v := range t {
				walk(v)
			}
		case []any:
			for _, v := range t {
				walk(v)
			}
		}
	}

	walk(config)
	return paths
}

// sanitizePresetCachePath validates and sanitizes a preset path for cache storage.
func sanitizePresetCachePath(p string) (string, error) {
	clean := path.Clean(p)
	if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("preset path %q escapes cache directory", p)
	}
	return path.Join(".stagefreight/preset-cache", clean), nil
}

// deepCopyMap returns a deep copy of a map to prevent cross-cluster mutation.
func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case map[string]any:
			out[k] = deepCopyMap(t)
		case []any:
			cp := make([]any, len(t))
			copy(cp, t)
			out[k] = cp
		default:
			out[k] = v
		}
	}
	return out
}

// HasChanges returns true if this plan has any files that need writing.
func (p DistributionPlan) HasChanges() bool {
	for _, f := range p.Files {
		if f.Action != "unchanged" {
			return true
		}
	}
	return false
}
