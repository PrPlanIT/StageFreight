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
// installRoot. Per tool, the effective policy resolves via retention.Effective (a ref
// matching the tool name overrides the default); a policy with no active rule for a
// tool falls back to the engine default keep_last=2. pins (tool → constraint) are
// always protected, as are versions matching policy.Protect (against "tool" or
// "tool/version"). Shared by `toolchain prune` config-mode and the disk-GC lifecycle.
func PlanVersionRetention(installRoot string, policy config.RetentionPolicy, pins map[string]string) ([]PruneCandidate, error) {
	byTool, err := scanInstalledVersions(installRoot, "")
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
func scanInstalledVersions(installRoot, toolFilter string) (map[string][]versionEntry, error) {
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
		if toolFilter != "" && toolName != toolFilter {
			continue
		}
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

// PlanVersionPrune applies keep-latest-N retention to the installed toolchain versions
// under installRoot and returns the versions that fall out. Shared core of
// `toolchain prune` and the disk-GC lifecycle. Safety:
//   - a version equal to protected[tool] (the pinned/locked resolution) is never selected
//   - the keep newest are ranked by Metadata.InstalledAt
//   - olderThanDays > 0 additionally requires that age before selecting
//   - versions with no readable metadata are SKIPPED (unknown → leave alone)
func PlanVersionPrune(installRoot string, keep int, olderThanDays int, toolFilter string, protected map[string]string) ([]PruneCandidate, error) {
	if keep < 1 {
		keep = 1 // never plan a tool down to zero versions
	}
	byTool, err := scanInstalledVersions(installRoot, toolFilter)
	if err != nil {
		return nil, err // no toolchain cache — caller decides whether that matters
	}

	var out []PruneCandidate
	now := time.Now()
	for tool, versions := range byTool {
		sort.Slice(versions, func(i, j int) bool {
			return versions[i].InstalledAt.After(versions[j].InstalledAt)
		})
		pinnedVer := protected[tool]
		for i, v := range versions {
			if v.Version == pinnedVer {
				continue
			}
			if i < keep {
				continue
			}
			if olderThanDays > 0 && now.Sub(v.InstalledAt) < time.Duration(olderThanDays)*24*time.Hour {
				continue
			}
			reason := "older version"
			if olderThanDays > 0 {
				reason = "older version past age threshold"
			}
			out = append(out, PruneCandidate{
				Tool: v.Tool, Version: v.Version, Dir: v.Dir,
				InstalledAt: v.InstalledAt, Reason: reason,
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
