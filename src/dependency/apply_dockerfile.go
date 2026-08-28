package dependency

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// NormalizeSkipReason maps internal parser-detail skip reasons to operator-facing language.
// Reasons that are already operator-friendly pass through unchanged.
func NormalizeSkipReason(reason string) string {
	switch {
	case reason == "line does not match ENV VERSION pattern":
		return "version not resolvable from source"
	case reason == "line does not match FROM pattern":
		return "version not resolvable from source"
	case strings.HasPrefix(reason, "ENV value "):
		return "source value mismatch"
	case reason == "current version not found in image token":
		return "source value mismatch"
	default:
		return reason
	}
}

// corruptPinPrefix marks a skip reason that describes a pin the engine can neither use
// nor repair. It is a distinct condition from an ordinary mismatch: the file is WRONG,
// not merely unrecognized, and no future run will fix it.
const corruptPinPrefix = "corrupt pin: "

// versionHasPathSeparator reports whether a version-shaped value carries a "/".
//
// No version string contains one. Its presence means the value was written from an
// un-normalized upstream release tag — monorepos tag component releases as
// "kustomize/v5.8.1" or "api/v1.2.3" — which discovery is supposed to reduce to a bare
// version (see versionFromTag). This is the last line of defence: if a mangled value
// reaches the writer, refuse rather than commit it to someone's Dockerfile.
//
// The failure this prevents is silent and durable. Writing "vkustomize/v5.8.1" produces
// a 404 download that only surfaces on the next build, and the pin can never self-heal:
// on the following run Current parses to "kustomize/v5.8.1" while Latest resolves to
// "5.8.1", they disagree, and the writer declines every subsequent update. One bad write
// wedges the dependency permanently, so the write is the right place to stop it.
func versionHasPathSeparator(v string) bool { return strings.Contains(v, "/") }

// bareVersion is the repair suggestion for a mangled pin: the segment after the last "/".
func bareVersion(v string) string {
	if i := strings.LastIndex(v, "/"); i >= 0 {
		return v[i+1:]
	}
	return v
}

// Dockerfile regexes: capture prefix/token/suffix groups for minimal diffs.
var (
	// FROM prefix(group1) image-token(group2) suffix(group3)
	fromRe = regexp.MustCompile(`^(FROM\s+(?:--platform=\S+\s+)?)(\S+)(.*)$`)

	// ENV prefix(group1) version-value(group2) suffix(group3) — single-var fallback
	envVersionRe = regexp.MustCompile(`^(ENV\s+[A-Z0-9_]+_VERSION[= ])(\S+)(.*)$`)
)

// dockerfileEdit represents a pending line replacement with hash guard.
type dockerfileEdit struct {
	dep      supplychain.Dependency
	line     int // 1-based line number
	origHash [32]byte
	newLine  string
}

// applyDockerfileUpdates applies Dockerfile dependency updates.
// Returns touchedFiles (repo-root-relative Dockerfile paths) as the 3rd value.
func applyDockerfileUpdates(deps []supplychain.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, error) {
	var applied []AppliedUpdate
	var skipped []SkippedDep

	// Group deps by file, build edits
	type fileEdits struct {
		absPath string
		edits   []dockerfileEdit
	}
	byFile := make(map[string]*fileEdits)

	for _, dep := range deps {
		absPath := filepath.Join(repoRoot, dep.File)

		// Resolve the actual physical line to edit. For deps with a Binding
		// (e.g. ENV var name), find the physical line containing the binding
		// key. Multi-var ENV lines with continuations share the same dep.Line
		// (endLine of the block), but each var is on its own physical line.
		lineNum := dep.Line
		if dep.Binding != "" {
			found, err := findBindingLine(absPath, dep.Binding)
			if err != nil {
				skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceUnresolvable, Reason: "version not resolvable from source"})
				continue
			}
			lineNum = found
		}

		// Read the specific line to compute hash and build replacement
		origLine, err := readLineAt(absPath, lineNum)
		if err != nil {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceUnresolvable, Reason: fmt.Sprintf("cannot read line %d: %v", lineNum, err)})
			continue
		}

		newLine, skip := buildReplacement(dep, origLine)
		if skip != "" {
			// A corrupt pin is not a mismatch the next run might resolve — it is a file
			// that needs a human edit, and it must not be filed under the same category
			// as the transient cases or it disappears into them.
			cat := SkipSourceMismatch
			if strings.HasPrefix(skip, corruptPinPrefix) {
				cat = SkipCorruptPin
			}
			skipped = append(skipped, SkippedDep{Dep: dep, Category: cat, Reason: skip})
			continue
		}
		if newLine == origLine {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipNoChange, Reason: "no change after replacement"})
			continue
		}

		fe, ok := byFile[dep.File]
		if !ok {
			fe = &fileEdits{absPath: absPath}
			byFile[dep.File] = fe
		}
		fe.edits = append(fe.edits, dockerfileEdit{
			dep:      dep,
			line:     lineNum,
			origHash: sha256.Sum256([]byte(origLine)),
			newLine:  newLine,
		})

		update := AppliedUpdate{
			Dep:        dep,
			OldVer:     dep.Current,
			NewVer:     dep.UpdateTarget(),
			UpdateType: updateType(dep.Current, dep.UpdateTarget()),
		}
		for _, v := range dep.Vulnerabilities {
			update.CVEsFixed = append(update.CVEsFixed, v.ID)
		}
		applied = append(applied, update)
	}

	// Apply edits file by file, track touched files
	var touchedFiles []string
	for file, fe := range byFile {
		if err := applyFileEdits(fe.absPath, fe.edits); err != nil {
			return applied, skipped, nil, fmt.Errorf("editing %s: %w", file, err)
		}
		touchedFiles = append(touchedFiles, file)
	}
	sort.Strings(touchedFiles)

	return applied, skipped, touchedFiles, nil
}

// buildReplacement constructs the replacement line for a Dockerfile dependency.
// Returns the new line and a skip reason (empty if eligible).
func buildReplacement(dep supplychain.Dependency, origLine string) (string, string) {
	switch dep.Ecosystem {
	case supplychain.EcosystemDockerImage:
		// A base image whose version is interpolated from an ARG/ENV (FROM …${VAR})
		// carries the variable name as its Binding — the update lands on the
		// `ARG VAR=…` / `ENV VAR=…` value line, leaving the FROM as-is. A literal or
		// inline-default FROM has no Binding and is edited on the FROM line itself.
		if dep.Binding != "" {
			return buildEnvReplacement(dep, origLine)
		}
		return buildFromReplacement(dep, origLine)
	case supplychain.EcosystemGitHubRelease:
		return buildEnvReplacement(dep, origLine)
	case supplychain.EcosystemDebianAPT, supplychain.EcosystemAlpineAPK:
		// A distro package pinned through an ARG/ENV (pkg="${PKG_VERSION}") is edited on
		// that declaration, exactly as a bound base image or pinned tool is. Without a
		// binding the version sits literally on the install line, which may pin several
		// packages at once, so the edit is targeted by name there.
		if dep.Binding != "" {
			return buildEnvReplacement(dep, origLine)
		}
		// Unbound: the version sits literally on a RUN line that may pin several
		// packages and span continuations. Reported rather than edited, so an operator
		// sees the available update instead of it being silently dropped.
		return origLine, "version pinned inline on the install line; move it to an ARG to auto-update"
	default:
		return origLine, "unsupported ecosystem for Dockerfile edit"
	}
}

// buildFromReplacement handles FROM line image tag replacement.
func buildFromReplacement(dep supplychain.Dependency, origLine string) (string, string) {
	m := fromRe.FindStringSubmatch(origLine)
	if m == nil {
		return origLine, "line does not match FROM pattern"
	}

	token := m[2] // the image token

	// A base image whose version is interpolated from an external ARG/ENV is anchored on
	// that definition line and routed to buildEnvReplacement (via Binding) before reaching
	// here, so it never appears as a FROM edit. A "$" that survives to this point is an
	// inline default (FROM …${VAR:-3.23.5}) whose version is embedded in the token — the
	// strings.Replace below bumps it in place; a token with no concrete current version
	// produces a no-op replace, reported as "current version not found" below.

	// Digest-pinned base (image:tag@sha256:…): bump the tag within the ref AND swap
	// in the re-resolved digest (Renovate pinDigests parity). ResolvedDigest is set by
	// discovery for the update target; when it's empty (registry miss) fall back to a
	// tag-only bump, keeping the old digest rather than skipping.
	if at := strings.Index(token, "@sha256:"); at >= 0 {
		ref, oldDigest := token[:at], token[at+1:]
		newRef := strings.Replace(ref, dep.Current, dep.UpdateTarget(), 1)
		newDigest := oldDigest
		if dep.ResolvedDigest != "" {
			newDigest = dep.ResolvedDigest
		}
		if newRef == ref && newDigest == oldDigest {
			return origLine, "current version not found in image token"
		}
		return m[1] + newRef + "@" + newDigest + m[3], ""
	}

	// Replace the current version tag with the eligible bump target.
	// dep.Current is the current tag/version; UpdateTarget() is the safe in-line
	// target (LatestEligible, falling back to Latest when no compatibility model).
	newToken := strings.Replace(token, dep.Current, dep.UpdateTarget(), 1)
	if newToken == token {
		return origLine, "current version not found in image token"
	}

	return m[1] + newToken + m[3], ""
}

// buildEnvReplacement handles ENV VERSION line replacement.
// Supports both single-var (ENV KEY=VALUE) and multi-var (ENV K1=V1 K2=V2) lines.
// Uses dep.Binding (the ENV var name) to locate the specific key=value pair.
func buildEnvReplacement(dep supplychain.Dependency, origLine string) (string, string) {
	// If Binding is set, use it for targeted replacement within multi-var lines.
	if dep.Binding != "" {
		// Build a regex that finds this specific binding anywhere in the line:
		// (prefix...BINDING[= ])(value)(suffix...)
		pattern := regexp.MustCompile(`((?:^|\s)` + regexp.QuoteMeta(dep.Binding) + `[= ])(\S+)`)
		m := pattern.FindStringSubmatchIndex(origLine)
		if m == nil {
			return origLine, "version not resolvable from source"
		}
		// m[4]:m[5] is the value capture group
		foundValue := origLine[m[4]:m[5]]
		// The pin already in the file is itself mangled — report it as the distinct,
		// unrecoverable condition it is, naming the repair, rather than letting it read
		// as an ordinary mismatch that a later run might resolve. It never will.
		if versionHasPathSeparator(foundValue) {
			return origLine, fmt.Sprintf("%s%s=%s is not a version; set it to %q by hand",
				corruptPinPrefix, dep.Binding, foundValue, bareVersion(foundValue))
		}
		// Normalize v-prefix: freshness strips "v" from Current/Latest,
		// but the raw file may have it (e.g. "v0.32.0" vs "0.32.0").
		normalizedFound := strings.TrimPrefix(foundValue, "v")
		if normalizedFound != dep.Current {
			return origLine, "source value mismatch"
		}
		// Preserve original prefix when writing replacement
		target := dep.UpdateTarget()
		if versionHasPathSeparator(target) {
			return origLine, fmt.Sprintf("%sresolved target %q is not a version — refusing to write it",
				corruptPinPrefix, target)
		}
		replacement := target
		if strings.HasPrefix(foundValue, "v") && !strings.HasPrefix(target, "v") {
			replacement = "v" + target
		}
		return origLine[:m[4]] + replacement + origLine[m[5]:], ""
	}

	// Fallback: single-var ENV line via anchored regex.
	m := envVersionRe.FindStringSubmatch(origLine)
	if m == nil {
		return origLine, "version not resolvable from source"
	}
	if versionHasPathSeparator(m[2]) {
		return origLine, fmt.Sprintf("%s%s is not a version; set it to %q by hand",
			corruptPinPrefix, m[2], bareVersion(m[2]))
	}
	normalizedFound := strings.TrimPrefix(m[2], "v")
	if normalizedFound != dep.Current {
		return origLine, "source value mismatch"
	}
	target := dep.UpdateTarget()
	if versionHasPathSeparator(target) {
		return origLine, fmt.Sprintf("%sresolved target %q is not a version — refusing to write it",
			corruptPinPrefix, target)
	}
	replacement := target
	if strings.HasPrefix(m[2], "v") && !strings.HasPrefix(target, "v") {
		replacement = "v" + target
	}
	return m[1] + replacement + m[3], ""
}

// applyFileEdits writes the edited lines back to a file, verifying hashes.
func applyFileEdits(absPath string, edits []dockerfileEdit) error {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")

	// Build edit map by line number
	editMap := make(map[int]*dockerfileEdit, len(edits))
	for i := range edits {
		editMap[edits[i].line] = &edits[i]
	}

	// Apply edits with hash verification
	for lineNum, edit := range editMap {
		if lineNum < 1 || lineNum > len(lines) {
			return fmt.Errorf("line %d out of range (file has %d lines)", lineNum, len(lines))
		}

		currentLine := lines[lineNum-1]
		currentHash := sha256.Sum256([]byte(currentLine))
		if currentHash != edit.origHash {
			return fmt.Errorf("line %d has been modified since resolution (hash mismatch)", lineNum)
		}

		lines[lineNum-1] = edit.newLine
	}

	return os.WriteFile(absPath, []byte(strings.Join(lines, "\n")), 0o644)
}

// readLineAt reads a specific 1-based line number from a file.
func readLineAt(absPath string, lineNum int) (string, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return "", fmt.Errorf("line %d out of range (file has %d lines)", lineNum, len(lines))
	}

	return lines[lineNum-1], nil
}

// findBindingLine scans a file for the physical line containing a binding key
// as an ENV assignment token (e.g. "BUILDX_VERSION=..." or "BUILDX_VERSION ...").
// Returns the 1-based line number nearest to hintLine. Exact token match avoids
// false positives from comments, URLs, or similarly named vars.
func findBindingLine(absPath, binding string) (int, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return 0, err
	}
	pattern := regexp.MustCompile(`(^|\s)` + regexp.QuoteMeta(binding) + `(=| )`)
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if pattern.MatchString(line) {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("binding %q not found in %s", binding, absPath)
}
