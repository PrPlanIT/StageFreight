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
// with no active rule for a tool falls back to the engine default keep_last=2.
//
// The cache is shared by every repo on the runner, so the policy is applied to when
// each version was last USED (by any repo), not when it was installed. It is applied a
// second time to the versions some repo declares exactly, ranked by when that pinned
// use last happened, and a version either pass keeps survives — so an older pin lives
// beside the newest release a tracked line moved to. pins (tool → constraint) of the
// running repo are always protected, as are versions matching policy.Protect (against
// "tool" or "tool/version"). Versions with no readable metadata are SKIPPED (unknown
// provenance → leave alone).
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
		keep := keptBy(versions, eff, func(v versionEntry) time.Time { return v.LastUsed })
		pinnedKeep := keptBy(versions, eff, func(v versionEntry) time.Time { return v.PinnedUsed })
		for _, v := range versions {
			if keep[v.Version] || pinnedKeep[v.Version] || v.Version == pins[tool] {
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

// keptBy applies policy to the versions ranked newest-first by at, skipping versions
// with no such time, and returns the versions it keeps.
func keptBy(versions []versionEntry, policy config.RetentionPolicy, at func(versionEntry) time.Time) map[string]bool {
	var ranked []versionEntry
	for _, v := range versions {
		if !at(v).IsZero() {
			ranked = append(ranked, v)
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return at(ranked[i]).After(at(ranked[j])) })
	items := make([]retention.Item, len(ranked))
	for i, v := range ranked {
		items[i] = retention.Item{Name: v.Version, CreatedAt: at(v)}
	}
	kept := map[string]bool{}
	for i, k := range retention.ApplyPolicies(items, policy) {
		if k {
			kept[ranked[i].Version] = true
		}
	}
	return kept
}

// versionEntry is one installed tool version discovered on disk.
type versionEntry struct {
	Tool        string
	Version     string
	Dir         string
	InstalledAt time.Time
	LastUsed    time.Time // last use by any run; the install time when never marked
	PinnedUsed  time.Time // last use by a run declaring this exact version; zero if none
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
			dir := filepath.Join(installRoot, toolName, verDir.Name())
			lastUsed := usedAt(dir, lastUsedFile)
			if lastUsed.Before(installedAt) {
				lastUsed = installedAt
			}
			byTool[toolName] = append(byTool[toolName], versionEntry{
				Tool: toolName, Version: verDir.Name(), Dir: dir,
				InstalledAt: installedAt,
				LastUsed:    lastUsed,
				PinnedUsed:  usedAt(dir, pinnedUsedFile),
			})
		}
	}
	return byTool, nil
}
