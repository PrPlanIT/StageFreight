package dependency

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	version "github.com/PrPlanIT/StageFreight/src/supplychain/version"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// applyToolchainUpdates moves toolchains to their update targets. What a tool wants comes
// from the resolved config, carried on the dependency (a wildcard's range is its
// Constraint), so a tool declared by a preset is updated like one written locally. A
// wildcard moves the lock only; an exact pin is also rewritten in .stagefreight.yml, by
// section-scoped line editing that keeps structure and comments — which is possible only
// when the pin is written there and not in a preset.
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

	sectionStart, sectionEnd := findToolchainsSection(lines)

	for _, dep := range deps {
		toolName := dep.Name

		if version.IsWildcardConstraint(dep.Constraint) {
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
			if lock.Set(toolName, dep.Latest, toolchain.HostPlatformKey(toolName), sha) {
				lockModified = true
				applied = append(applied, AppliedUpdate{Dep: dep, OldVer: prior, NewVer: dep.Latest, UpdateType: updateType(prior, dep.Latest), CVEsFixed: fixedBy(dep, dep.Latest)})
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
		// The pin's line: scalar shorthand (`trivy: "0.69.3"`) or a block map whose
		// `version:` key carries it.
		verIdx, verKey := -1, ""
		if sectionStart >= 0 {
			verIdx, verKey, _ = findToolEntry(lines, sectionStart, sectionEnd, toolName)
		}
		if verIdx < 0 {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceMismatch, Reason: "pinned to " + dep.Current + " by a preset — the preset owns that version"})
			continue
		}
		sha, keep := resolveLockDigest(lock, toolName, dep.Latest)
		if !keep {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipOther, Reason: "could not derive pinned sha256 for " + dep.Latest})
			continue
		}
		lines[verIdx] = rewriteVersion(lines[verIdx], verKey, dep.Latest)
		configModified = true
		lock.Set(toolName, dep.Latest, toolchain.HostPlatformKey(toolName), sha)
		lockModified = true
		applied = append(applied, AppliedUpdate{Dep: dep, OldVer: dep.Current, NewVer: dep.Latest, UpdateType: updateType(dep.Current, dep.Latest), CVEsFixed: fixedBy(dep, dep.Latest)})
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

// flowVersionRE matches the version entry of an inline flow map, whatever its sibling keys.
var flowVersionRE = regexp.MustCompile(`(\bversion:\s*)("[^"]*"|'[^']*'|[^,}\s]+)`)

// flowMapVersion unwraps an inline `{version: X, …}` flow map to X; any other value
// passes through as the constraint itself.
func flowMapVersion(v string) string {
	if !strings.HasPrefix(v, "{") {
		return v
	}
	m := flowVersionRE.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	return strings.Trim(m[2], `"'`)
}

// rewriteVersion replaces only the version value on a toolchain line — scalar, block
// `version:` or inline flow map — keeping sibling keys and any trailing comment.
func rewriteVersion(line, key, ver string) string {
	indent := leadIndent(line)
	body := strings.TrimPrefix(line, indent)
	comment := ""
	if c := strings.Index(body, " #"); c >= 0 {
		comment = body[c:]
		body = body[:c]
	}
	value := strings.TrimSpace(strings.TrimPrefix(body, key+":"))
	if strings.HasPrefix(value, "{") {
		value = flowVersionRE.ReplaceAllString(value, `${1}"`+ver+`"`)
		return indent + key + ": " + value + comment
	}
	return indent + fmt.Sprintf(`%s: "%s"`, key, ver) + comment
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

// fetchArtifactSHA256 downloads a release to digest it; tests replace it.
var fetchArtifactSHA256 = toolchain.FetchArtifactSHA256

// resolveLockDigest fetches the artifact digest for tool@ver to record in the lock.
// keep=false means the fetch failed AND the tool was already digest-pinned — the caller
// must abort rather than drop the pin (transactional). keep=true (sha may be "") means
// proceed: either the fetch succeeded, or the tool was never digest-pinned and verifies
// via its upstream checksum manifest instead of a recorded digest.
func resolveLockDigest(lock *toolchain.Lock, tool, ver string) (sha string, keep bool) {
	if s, err := fetchArtifactSHA256(tool, ver); err == nil {
		return s, true
	}
	if e, ok := lock.Get(tool); ok && len(e.SHA256) > 0 {
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

// fixedBy lists dep's advisories that version ver fixes.
func fixedBy(dep supplychain.Dependency, ver string) []string {
	var ids []string
	for _, v := range dep.Vulnerabilities {
		if v.FixedIn != "" && version.CompareVersions(ver, v.FixedIn, dep.Ecosystem) >= 0 {
			ids = append(ids, v.ID)
		}
	}
	return ids
}
