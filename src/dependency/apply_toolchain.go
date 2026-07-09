package dependency

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// applyToolchainDesiredUpdates updates toolchains.desired versions in .stagefreight.yml.
// Uses section-scoped line-level YAML editing to preserve file structure and comments.
// Only edits version lines within the toolchains.desired section — never touches
// identically-named keys elsewhere in the file.
func applyToolchainDesiredUpdates(deps []supplychain.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, error) {
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

		// Search ONLY within the desired section for this tool's block.
		for i := desiredStart; i <= desiredEnd && i < len(lines)-1; i++ {
			if strings.TrimSpace(lines[i]) != toolName+":" {
				continue
			}
			keyIndent := leadIndentWidth(lines[i])
			verIdx, shaIdx := findToolBlockLines(lines, i, keyIndent, desiredEnd)
			if verIdx < 0 {
				continue // this occurrence has no version line — keep looking
			}

			// A digest-pinned tool must update version AND sha256 TOGETHER, or neither
			// (transactional). Derive the new digest FIRST — a failure aborts the bump,
			// so we never leave a stale digest that would break verification.
			var newSHA string
			if shaIdx >= 0 {
				s, shaErr := toolchain.FetchArtifactSHA256(toolName, dep.Latest)
				if shaErr != nil {
					skipped = append(skipped, SkippedDep{Dep: dep, Reason: "could not derive pinned sha256: " + shaErr.Error()})
					found = true
					break
				}
				newSHA = s
			}

			lines[verIdx] = leadIndent(lines[verIdx]) + fmt.Sprintf(`version: "%s"`, dep.Latest)
			if shaIdx >= 0 {
				lines[shaIdx] = leadIndent(lines[shaIdx]) + fmt.Sprintf(`sha256: "%s"`, newSHA)
			}
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

func leadIndent(line string) string   { return line[:leadIndentWidth(line)] }
func leadIndentWidth(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

// findToolBlockLines returns the version and sha256 line indices within one tool's
// block — the lines indented under keyIdx up to sectionEnd — or -1 for each.
func findToolBlockLines(lines []string, keyIdx, keyIndent, sectionEnd int) (verIdx, shaIdx int) {
	verIdx, shaIdx = -1, -1
	for j := keyIdx + 1; j <= sectionEnd && j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if leadIndentWidth(lines[j]) <= keyIndent {
			break // dedent — left this tool's block
		}
		if strings.HasPrefix(t, "version:") {
			verIdx = j
		}
		if strings.HasPrefix(t, "sha256:") {
			shaIdx = j
		}
	}
	return
}
