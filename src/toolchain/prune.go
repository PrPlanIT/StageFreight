package toolchain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/retention"
)

// PruneCandidate is one toolchain version selected for removal by retention.
type PruneCandidate struct {
	Tool        string
	Version     string
	Dir         string
	InstalledAt time.Time
	Reason      string
}

// PlanVersionRetention applies the DECLARED toolchains.retention policy (the full
// grammar — keep_*, max_age, scoped refs:, protect:) to the installed versions under
// installRoot. THE single toolchain-retention model: `toolchain prune` and the disk-GC
// lifecycle both plan through it. Per tool, the effective policy resolves via
// retention.Effective (a ref matching the tool name overrides the default); a policy
// with no active rule for a tool falls back to the engine default keep_last=2. pins
// (tool → constraint) are always protected, as are versions matching policy.Protect
// (against "tool" or "tool/version"). Versions with no readable metadata are SKIPPED
// (unknown provenance → leave alone).
func PlanVersionRetention(installRoot string, policy config.RetentionPolicy, pins map[string]string) ([]PruneCandidate, error) {
	byTool, err := scanInstalledVersions(installRoot)
	if err != nil {
		return nil, err
	}
	protectPatterns := retention.TemplatesToPatterns(policy.Protect)

	var out []PruneCandidate
	for tool, versions := range byTool {
		if len(protectPatterns) > 0 && config.MatchPatterns(protectPatterns, tool) {
			continue // whole tool exempted
		}
		eff := retention.Effective(policy, tool)
		if !eff.Active() {
			eff.KeepLast = 2 // engine default: keep the 2 newest per tool
		}
		sort.Slice(versions, func(i, j int) bool {
			return versions[i].InstalledAt.After(versions[j].InstalledAt)
		})
		items := make([]retention.Item, len(versions))
		for i, v := range versions {
			items[i] = retention.Item{Name: v.Version, CreatedAt: v.InstalledAt}
		}
		keep := retention.ApplyPolicies(items, eff)
		for i, v := range versions {
			if keep[i] || v.Version == pins[tool] {
				continue
			}
			if len(protectPatterns) > 0 && config.MatchPatterns(protectPatterns, tool+"/"+v.Version) {
				continue
			}
			out = append(out, PruneCandidate{
				Tool: v.Tool, Version: v.Version, Dir: v.Dir,
				InstalledAt: v.InstalledAt, Reason: "beyond retention policy",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tool != out[j].Tool {
			return out[i].Tool < out[j].Tool
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// versionEntry is one installed tool version discovered on disk.
type versionEntry struct {
	Tool        string
	Version     string
	Dir         string
	InstalledAt time.Time
}

// scanInstalledVersions inventories installRoot's tool→versions, reading each
// version's InstalledAt from its .metadata.json. Versions with no readable metadata
// are SKIPPED (unknown provenance → leave alone).
func scanInstalledVersions(installRoot string) (map[string][]versionEntry, error) {
	byTool := make(map[string][]versionEntry)
	entries, err := os.ReadDir(installRoot)
	if err != nil {
		return nil, err
	}
	for _, toolDir := range entries {
		if !toolDir.IsDir() {
			continue
		}
		toolName := toolDir.Name()
		versions, err := os.ReadDir(filepath.Join(installRoot, toolName))
		if err != nil {
			continue
		}
		for _, verDir := range versions {
			if !verDir.IsDir() || verDir.Name() == ".lock" {
				continue
			}
			metaPath := filepath.Join(installRoot, toolName, verDir.Name(), ".metadata.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue // unknown provenance → leave alone
			}
			var meta Metadata
			if err := json.Unmarshal(data, &meta); err != nil {
				continue
			}
			installedAt, _ := time.Parse(time.RFC3339, meta.InstalledAt)
			byTool[toolName] = append(byTool[toolName], versionEntry{
				Tool: toolName, Version: verDir.Name(),
				Dir:         filepath.Join(installRoot, toolName, verDir.Name()),
				InstalledAt: installedAt,
			})
		}
	}
	return byTool, nil
}
