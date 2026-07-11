package dependency

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// applyNpmUpdates bumps package.json version ranges (operator-preserving, hash-guarded
// line edit — same discipline as Cargo) and then regenerates package-lock.json through
// the HARDENED npm runner (npm install --package-lock-only --ignore-scripts, scrubbed
// env). --package-lock-only re-resolves the lock WITHOUT populating node_modules, so no
// package tarball is executed and no lifecycle script runs — the sanitary way to touch
// npm. node is provisioned only when a lockfile actually needs syncing.
func applyNpmUpdates(ctx context.Context, deps []supplychain.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, error) {
	var applied []AppliedUpdate
	var skipped []SkippedDep

	type fileEdits struct {
		absPath string
		edits   []dockerfileEdit
	}
	byFile := make(map[string]*fileEdits)

	for _, dep := range deps {
		absPath := filepath.Join(repoRoot, dep.File)
		origLine, err := readLineAt(absPath, dep.Line)
		if err != nil {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceUnresolvable, Reason: fmt.Sprintf("cannot read line %d: %v", dep.Line, err)})
			continue
		}
		newLine, cat, reason := buildNpmReplacement(dep, origLine)
		if reason != "" {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: cat, Reason: reason})
			continue
		}
		fe, ok := byFile[dep.File]
		if !ok {
			fe = &fileEdits{absPath: absPath}
			byFile[dep.File] = fe
		}
		fe.edits = append(fe.edits, dockerfileEdit{dep: dep, line: dep.Line, origHash: sha256.Sum256([]byte(origLine)), newLine: newLine})
		target := dep.UpdateTarget()
		u := AppliedUpdate{Dep: dep, OldVer: dep.Current, NewVer: target, UpdateType: updateType(dep.Current, target)}
		for _, v := range dep.Vulnerabilities {
			u.CVEsFixed = append(u.CVEsFixed, v.ID)
		}
		applied = append(applied, u)
	}

	if len(byFile) == 0 {
		return applied, skipped, nil, nil
	}

	// Edit the manifests (hash-verified).
	var touchedFiles []string
	for file, fe := range byFile {
		if err := applyFileEdits(fe.absPath, fe.edits); err != nil {
			return applied, skipped, nil, fmt.Errorf("editing %s: %w", file, err)
		}
		touchedFiles = append(touchedFiles, file)
	}

	// Regenerate the lockfile (npm / yarn / pnpm) for each edited manifest that has one,
	// via the format-aware hardened runner. node (which bundles npm + corepack) is
	// provisioned lazily — only when a recognized lockfile is actually present.
	var tools *nodeToolRunner
	for file := range byFile {
		dir := filepath.Dir(filepath.Join(repoRoot, file))
		if _, _, _, _, ok := nodeLockCommand(dir); !ok {
			continue // no recognized lockfile — the manifest edit stands alone
		}
		if tools == nil {
			t, err := resolveNodeTools(repoRoot)
			if err != nil {
				return applied, skipped, deduplicateAndSort(touchedFiles), fmt.Errorf("provisioning node for lockfile sync: %w", err)
			}
			tools = t
		}
		lock, err := tools.syncLock(ctx, repoRoot, dir)
		if err != nil {
			return applied, skipped, deduplicateAndSort(touchedFiles), err
		}
		if lock != "" {
			touchedFiles = append(touchedFiles, lock)
		}
	}
	return applied, skipped, deduplicateAndSort(touchedFiles), nil
}

// buildNpmReplacement computes the new package.json line for a version-range dependency,
// preserving the operator (^/~/>=/exact). Non-version specs (git/url/file/workspace/tag/
// compound ranges) are left alone. The spec is parsed from the LINE (dep.Current is the
// lockfile-resolved version, not the manifest range).
func buildNpmReplacement(dep supplychain.Dependency, origLine string) (newLine string, cat SkipCategory, reason string) {
	target := dep.UpdateTarget()
	if target == "" {
		return "", SkipNoChange, "no change after replacement"
	}
	ci := strings.Index(origLine, ":")
	if ci < 0 {
		return "", SkipSourceMismatch, "not a package.json dependency line"
	}
	rest := origLine[ci+1:]
	q1 := strings.Index(rest, `"`)
	if q1 < 0 {
		return "", SkipSourceMismatch, "no version spec on line"
	}
	q2 := strings.Index(rest[q1+1:], `"`)
	if q2 < 0 {
		return "", SkipSourceMismatch, "unterminated version spec"
	}
	spec := rest[q1+1 : q1+1+q2]
	op, ok := parseNpmSpec(spec)
	if !ok {
		return "", SkipOther, "non-version spec (git/url/tag/range) — not auto-bumped"
	}
	newSpec := op + target
	if newSpec == spec {
		return "", SkipNoChange, "no change after replacement"
	}
	return strings.Replace(origLine, `"`+spec+`"`, `"`+newSpec+`"`, 1), SkipNone, ""
}

// parseNpmSpec returns the leading operator of a simple version range and reports
// whether it is a plain single-version spec this slice can bump. It rejects git/url/
// file/workspace/tag specs and compound ranges (which carry ":", "/", spaces, "|", "*").
func parseNpmSpec(spec string) (op string, ok bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "*" || spec == "latest" || strings.ContainsAny(spec, ":/ |") {
		return "", false
	}
	i := 0
	for i < len(spec) && strings.IndexByte("^~><=", spec[i]) >= 0 {
		i++
	}
	ver := spec[i:]
	if ver == "" || ver[0] < '0' || ver[0] > '9' {
		return "", false
	}
	return spec[:i], true
}
