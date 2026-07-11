package dependency

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// applyPipUpdates bumps Python requirements with the same hash-guarded line-edit
// discipline as the Dockerfile/Cargo updaters. Scope of this slice: requirements.txt
// EXACT pins (name==version) — the common reproducible-build form, and a bumpable
// lockpin (unlike a deliberate cargo `=`). Range specifiers (>=, ~=, etc.) are floors,
// not lockpins, and are left alone; Pipfile/poetry are skipped (no lockfile sync here,
// so no package manager is required). max_update still governs how far a pin moves.
func applyPipUpdates(deps []supplychain.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, error) {
	var applied []AppliedUpdate
	var skipped []SkippedDep

	type fileEdits struct {
		absPath string
		edits   []dockerfileEdit // generic {dep,line,origHash,newLine} edit, reused
	}
	byFile := make(map[string]*fileEdits)

	for _, dep := range deps {
		absPath := filepath.Join(repoRoot, dep.File)
		origLine, err := readLineAt(absPath, dep.Line)
		if err != nil {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceUnresolvable, Reason: fmt.Sprintf("cannot read line %d: %v", dep.Line, err)})
			continue
		}
		newLine, cat, reason := buildPipReplacement(dep, origLine)
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
	var touchedFiles []string
	for file, fe := range byFile {
		if err := applyFileEdits(fe.absPath, fe.edits); err != nil {
			return applied, skipped, nil, fmt.Errorf("editing %s: %w", file, err)
		}
		touchedFiles = append(touchedFiles, file)
	}
	return applied, skipped, deduplicateAndSort(touchedFiles), nil
}

// buildPipReplacement computes the new requirements line for an exact pin, or a
// (category, reason) skip. Only name==version is edited; the `==<current>` token is
// replaced with `==<target>` (env markers / inline comments on the line are preserved).
func buildPipReplacement(dep supplychain.Dependency, origLine string) (newLine string, cat SkipCategory, reason string) {
	if filepath.Base(dep.File) == "Pipfile" {
		return "", SkipOther, "Pipfile update not yet supported"
	}
	target := dep.UpdateTarget()
	if target == "" || target == dep.Current {
		return "", SkipNoChange, "no change after replacement"
	}
	// Exact pins only. A range specifier (>=, ~=, <, !=, etc.) is a floor, not a lockpin.
	if !strings.Contains(origLine, "==") {
		return "", SkipOther, "non-exact pin (range) — not auto-bumped"
	}
	token := "==" + dep.Current
	if !strings.Contains(origLine, token) {
		return "", SkipSourceMismatch, "current version not found in requirement"
	}
	return strings.Replace(origLine, token, "=="+target, 1), SkipNone, ""
}
