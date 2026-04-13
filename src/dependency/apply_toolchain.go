package dependency

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
)

// applyToolchainDesiredUpdates updates toolchains.desired versions in .stagefreight.yml.
// Uses section-scoped line-level YAML editing to preserve file structure and comments.
// Only edits version lines within the toolchains.desired section — never touches
// identically-named keys elsewhere in the file.
func applyToolchainDesiredUpdates(deps []freshness.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, error) {
	configPath := filepath.Join(repoRoot, ".stagefreight.yml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading %s: %w", configPath, err)
	}

	lines := strings.Split(string(data), "\n")
	var applied []AppliedUpdate
	var skipped []SkippedDep
	modified := false

	// Find the toolchains.desired section boundaries.
	// We need to be inside both "toolchains:" and "desired:" subsection.
	desiredStart, desiredEnd := findDesiredSection(lines)
	if desiredStart < 0 {
		// No toolchains.desired section — can't update
		for _, dep := range deps {
			skipped = append(skipped, SkippedDep{Dep: dep, Reason: "no toolchains.desired section in config"})
		}
		return nil, skipped, nil, nil
	}

	for _, dep := range deps {
		if dep.Latest == "" || dep.Latest == dep.Current {
			skipped = append(skipped, SkippedDep{Dep: dep, Reason: "up to date"})
			continue
		}

		toolName := dep.Name
		found := false

		// Search ONLY within the desired section for this tool's version line
		for i := desiredStart; i <= desiredEnd && i < len(lines)-1; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == toolName+":" {
				// Check next line for version
				nextTrimmed := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(nextTrimmed, "version:") {
					indent := lines[i+1][:len(lines[i+1])-len(strings.TrimLeft(lines[i+1], " "))]
					lines[i+1] = indent + fmt.Sprintf(`version: "%s"`, dep.Latest)
					found = true
					modified = true
					applied = append(applied, AppliedUpdate{
						Dep:        dep,
						OldVer:     dep.Current,
						NewVer:     dep.Latest,
						UpdateType: updateType(dep.Current, dep.Latest),
					})
					break
				}
			}
		}

		if !found {
			skipped = append(skipped, SkippedDep{Dep: dep, Reason: "version line not found in toolchains.desired"})
		}
	}

	if modified {
		output := strings.Join(lines, "\n")
		if err := os.WriteFile(configPath, []byte(output), 0644); err != nil {
			return nil, nil, nil, fmt.Errorf("writing %s: %w", configPath, err)
		}
		return applied, skipped, []string{".stagefreight.yml"}, nil
	}

	return applied, skipped, nil, nil
}

// findDesiredSection locates the line range of the toolchains.desired section.
// Returns (startLine, endLine) inclusive, or (-1, -1) if not found.
// The section ends when indentation returns to or above the desired: level.
func findDesiredSection(lines []string) (int, int) {
	inToolchains := false
	toolchainsIndent := -1
	desiredStart := -1
	desiredIndent := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))

		// Detect "toolchains:" at top level
		if trimmed == "toolchains:" && !inToolchains {
			inToolchains = true
			toolchainsIndent = indent
			continue
		}

		if inToolchains && desiredStart < 0 {
			// Looking for "desired:" within toolchains
			if indent <= toolchainsIndent && trimmed != "" {
				// Left the toolchains section without finding desired
				return -1, -1
			}
			if trimmed == "desired:" {
				desiredStart = i + 1
				desiredIndent = indent
				continue
			}
		}

		if desiredStart >= 0 {
			// Inside desired section — check if we've left it
			if indent <= desiredIndent && trimmed != "" {
				return desiredStart, i - 1
			}
		}
	}

	if desiredStart >= 0 {
		return desiredStart, len(lines) - 1
	}
	return -1, -1
}
