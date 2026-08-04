package dependency

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	version "github.com/PrPlanIT/StageFreight/src/supplychain/version"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// applyToolchainUpdates updates toolchain constraints in .stagefreight.yml.
// Uses section-scoped line-level YAML editing to preserve file structure and comments.
// Only edits constraint lines within the flat toolchains: section — never touches
// identically-named keys elsewhere in the file.
func applyToolchainUpdates(deps []supplychain.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, error) {
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

	// Find the flat toolchains: section boundaries.
	sectionStart, sectionEnd := findToolchainsSection(lines)
	if sectionStart < 0 {
		// No toolchains section — can't update
		for _, dep := range deps {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceUnresolvable, Reason: "no toolchains section in config"})
		}
		return nil, skipped, nil, nil
	}

	for _, dep := range deps {
		toolName := dep.Name

		// Locate the tool's constraint line within the toolchains section. The
		// entry is either scalar shorthand (`trivy: "0.69.3"`) or a block map
		// whose `version:` key carries the constraint.
		verIdx, verKey, constraint := findToolEntry(lines, sectionStart, sectionEnd, toolName)
		if verIdx < 0 {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceMismatch, Reason: "constraint line not found in toolchains section"})
			continue
		}

		if version.IsWildcardConstraint(constraint) {
			// The constraint (a range) stays in the config; only the resolved-LOCK moves.
			// dep.Latest is the newest in-line member (the target). The PRIOR lock value is
			// what moves — "" on a first lock, which reads as a birth, not a bump.
			if dep.Latest == "" {
				skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipUpToDate, Reason: "wildcard unresolved — nothing to lock"})
				continue
			}
			prior := lock.Resolved(toolName)
			if prior == dep.Latest {
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
				applied = append(applied, AppliedUpdate{Dep: dep, OldVer: prior, NewVer: dep.Latest, UpdateType: updateType(prior, dep.Latest)})
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
		changedFiles = append(changedFiles, paths.Durable("", "toolchains.lock"))
	}
	return applied, skipped, changedFiles, nil
}

// findToolchainsSection locates the line range of the flat top-level toolchains:
// section — the tool entries indented under it. Returns (startLine, endLine)
// inclusive, or (-1, -1) when the config declares no toolchains. The indent-0
// requirement keeps identically-named nested keys elsewhere from matching.
func findToolchainsSection(lines []string) (int, int) {
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadIndentWidth(line)
		if start < 0 {
			if indent == 0 && trimmed == "toolchains:" {
				start = i + 1
			}
			continue
		}
		if indent == 0 {
			return start, i - 1
		}
	}
	if start >= 0 {
		return start, len(lines) - 1
	}
	return -1, -1
}

// findToolEntry locates a tool's constraint within the toolchains section and
// returns the line to edit on a bump, the key to write there, and the constraint
// the config expresses. Scalar shorthand (`trivy: "0.69.3"`) and the inline flow
// map (`trivy: {version: "0.69.3"}`) carry the constraint on the tool's own line;
// the block-map form carries it on a nested `version:` line.
func findToolEntry(lines []string, start, end int, toolName string) (verIdx int, verKey, constraint string) {
	for i := start; i <= end && i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon < 0 || trimmed[:colon] != toolName {
			continue
		}
		value := strings.TrimSpace(trimmed[colon+1:])
		if c := strings.Index(value, " #"); c >= 0 {
			value = strings.TrimSpace(value[:c])
		}
		if value != "" {
			return i, toolName, flowMapVersion(strings.Trim(value, `"'`))
		}
		if vi, vk := findToolConstraintLine(lines, i, leadIndentWidth(lines[i]), end); vi >= 0 {
			return vi, vk, lineValue(lines[vi])
		}
		return -1, "", ""
	}
	return -1, "", ""
}

// flowMapVersion unwraps an inline `{version: X}` flow map to X; any other value
// passes through as the constraint itself.
func flowMapVersion(v string) string {
	if !strings.HasPrefix(v, "{") {
		return v
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(v, "{"), "}")
	if idx := strings.Index(inner, ":"); idx >= 0 {
		return strings.Trim(strings.TrimSpace(inner[idx+1:]), `"' `)
	}
	return ""
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
