package toolchain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// PruneCandidate is one toolchain version selected for removal by retention.
type PruneCandidate struct {
	Tool        string
	Version     string
	Dir         string
	InstalledAt time.Time
	Reason      string
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

	type versionEntry struct {
		Tool        string
		Version     string
		Dir         string
		InstalledAt time.Time
	}
	byTool := make(map[string][]versionEntry)

	entries, err := os.ReadDir(installRoot)
	if err != nil {
		return nil, err // no toolchain cache — caller decides whether that matters
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
