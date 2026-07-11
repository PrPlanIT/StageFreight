package dependency

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/layout"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	version "github.com/PrPlanIT/StageFreight/src/supplychain/version"
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

	// The resolved versions + digests live in .stagefreight/toolchains.lock, not the config.
	// A wildcard update moves the lock only; an exact update bumps the config constraint AND
	// syncs the lock. A missing lock reads as empty (first-lock fills it).
	lock, err := toolchain.ReadLock(repoRoot)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading toolchain lock: %w", err)
	}

	var applied []AppliedUpdate
	var skipped []SkippedDep
	configModified := false
	lockModified := false

	// Find the toolchains.desired section boundaries.
	// We need to be inside both "toolchains:" and "desired:" subsection.
	desiredStart, desiredEnd := findDesiredSection(lines)
	if desiredStart < 0 {
		// No toolchains.desired section — can't update
		for _, dep := range deps {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceUnresolvable, Reason: "no toolchains.desired section in config"})
		}
		return nil, skipped, nil, nil
	}

	for _, dep := range deps {
		toolName := dep.Name

		// Locate the tool's constraint line within the desired section.
		verIdx, verKey := -1, ""
		for i := desiredStart; i <= desiredEnd && i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) != toolName+":" {
				continue
			}
			if vi, vk := findToolConstraintLine(lines, i, leadIndentWidth(lines[i]), desiredEnd); vi >= 0 {
				verIdx, verKey = vi, vk
				break
			}
		}
		if verIdx < 0 {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceMismatch, Reason: "version line not found in toolchains.desired"})
			continue
		}

		if version.IsWildcardConstraint(lineValue(lines[verIdx])) {
			// The constraint (a range) stays in the config; only the resolved-LOCK moves.
			// dep.Latest is the newest in-line member (the target); dep.Current is the
			// current lock. First lock is just an empty lock being filled.
			if dep.Latest == "" {
				skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipUpToDate, Reason: "wildcard unresolved — nothing to lock"})
				continue
			}
			if lock.Resolved(toolName) == dep.Latest {
				skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipUpToDate, Reason: "lock already at newest in-line"})
				continue
			}
			sha, keep := resolveLockDigest(lock, toolName, dep.Latest)
			if !keep {
				skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipOther, Reason: "could not derive pinned sha256 for " + dep.Latest})
				continue
			}
			if lock.Set(toolName, dep.Latest, sha) {
				lockModified = true
				applied = append(applied, AppliedUpdate{Dep: dep, OldVer: dep.Current, NewVer: dep.Latest, UpdateType: updateType(dep.Current, dep.Latest)})
			}
			continue
		}

		// EXACT constraint: the constraint IS the version — bump it in the config, and sync
		// the lock's resolved+digest to match. Transactional: a digest that was pinned and
		// now fails to derive aborts the bump rather than dropping the pin.
		if dep.Latest == "" || dep.Latest == dep.Current {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipUpToDate, Reason: "up to date"})
			continue
		}
		sha, keep := resolveLockDigest(lock, toolName, dep.Latest)
		if !keep {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipOther, Reason: "could not derive pinned sha256 for " + dep.Latest})
			continue
		}
		lines[verIdx] = leadIndent(lines[verIdx]) + fmt.Sprintf(`%s: "%s"`, verKey, dep.Latest)
		configModified = true
		lock.Set(toolName, dep.Latest, sha)
		lockModified = true
		applied = append(applied, AppliedUpdate{Dep: dep, OldVer: dep.Current, NewVer: dep.Latest, UpdateType: updateType(dep.Current, dep.Latest)})
	}

	var changedFiles []string
	if configModified {
		if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			return nil, nil, nil, fmt.Errorf("writing %s: %w", configPath, err)
		}
		changedFiles = append(changedFiles, ".stagefreight.yml")
	}
	if lockModified {
		if err := toolchain.WriteLock(repoRoot, lock); err != nil {
			return nil, nil, nil, fmt.Errorf("writing toolchain lock: %w", err)
		}
		changedFiles = append(changedFiles, layout.Durable("", "toolchains.lock"))
	}
	return applied, skipped, changedFiles, nil
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

// findToolConstraintLine returns the `version:` line index within one tool's block —
// the lines indented under keyIdx up to sectionEnd — or -1. `version` is the sole
// constraint key (the Cargo/Go convention); the resolved version + digest live in
// .stagefreight/toolchains.lock, so only this line is edited here.
func findToolConstraintLine(lines []string, keyIdx, keyIndent, sectionEnd int) (verIdx int, verKey string) {
	for j := keyIdx + 1; j <= sectionEnd && j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if leadIndentWidth(lines[j]) <= keyIndent {
			break // dedent — left this tool's block
		}
		if strings.HasPrefix(t, "version:") {
			return j, "version"
		}
	}
	return -1, ""
}

// resolveLockDigest fetches the artifact digest for tool@ver to record in the lock.
// keep=false means the fetch failed AND the tool was already digest-pinned — the caller
// must abort rather than drop the pin (transactional). keep=true (sha may be "") means
// proceed: either the fetch succeeded, or the tool was never digest-pinned and verifies
// via its upstream checksum manifest instead of a recorded digest.
func resolveLockDigest(lock *toolchain.Lock, tool, ver string) (sha string, keep bool) {
	if s, err := toolchain.FetchArtifactSHA256(tool, ver); err == nil {
		return s, true
	}
	if e, ok := lock.Get(tool); ok && e.SHA256 != "" {
		return "", false // was pinned — do not silently drop the digest
	}
	return "", true
}

// lineValue extracts the (unquoted) value of a `key: value` YAML line.
func lineValue(line string) string {
	t := strings.TrimSpace(line)
	if idx := strings.Index(t, ":"); idx >= 0 {
		return strings.Trim(strings.TrimSpace(t[idx+1:]), `"'`)
	}
	return ""
}
