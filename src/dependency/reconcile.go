package dependency

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/reconcile"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// reconcileRepository runs repository reconcilers over the resolved dependency set
// AFTER dependency updates have been applied. Reconciliation is not an update: it
// makes the repository internally consistent with constraints it already encodes —
// here, the go.mod `go` directive floor that the golang builder image must satisfy.
// It never flows through candidate/policy evaluation and is not bounded by
// max_update. Derived mutations are applied to the working tree and recorded on
// result so they ride the same commit as the updates that necessitated them;
// unreconcilable inconsistencies are collected on result as configuration errors
// that fail the run.
//
// When dryRun is set the pass DETECTS and reports (a validation check) but applies
// nothing — the config errors still fail the run, because an inconsistent
// repository is invalid whether or not this pass is permitted to mutate it.
func reconcileRepository(repoRoot string, deps []supplychain.Dependency, result *UpdateResult, dryRun bool) {
	obs := goBuilderObservations(repoRoot, deps)
	if len(obs) == 0 {
		return
	}
	res := reconcile.ReconcileGoToolchain(obs)

	if !dryRun && len(res.Mutations) > 0 {
		applied, touched, unapplied := applyReconcileMutations(repoRoot, res.Mutations)
		result.Applied = append(result.Applied, applied...)
		result.FilesChanged = deduplicateAndSort(append(result.FilesChanged, touched...))
		// A mutation that failed to write (hash/source mismatch) leaves the
		// inconsistency in place — surface it rather than reporting success.
		res.ConfigErrors = append(res.ConfigErrors, unapplied...)
	}

	result.ReconcileErrors = append(result.ReconcileErrors, res.ConfigErrors...)
}

// goBuilderObservations assembles the pure inputs for go-toolchain reconciliation
// from the resolved dependency set. For every golang builder image it reads the
// CURRENT FROM tag from disk (post any intentful bump this run), resolves the
// governing module's `go` directive floor, and attaches the registry tag list
// retained by discovery. Builders that cannot be compared (digest/ARG-pinned,
// non-versioned, no governing go.mod) are omitted — never a false positive.
func goBuilderObservations(repoRoot string, deps []supplychain.Dependency) []reconcile.GoBuilderObservation {
	var obs []reconcile.GoBuilderObservation
	for _, dep := range deps {
		if dep.Ecosystem != supplychain.EcosystemDockerImage || !isGolangImage(dep.Name) {
			continue
		}
		absPath := filepath.Join(repoRoot, dep.File)
		line, err := readLineAt(absPath, dep.Line)
		if err != nil {
			continue
		}
		image, tag, ok := parseFromImageTag(line)
		if !ok {
			continue // digest/ARG/unparseable — not a determinable violation
		}
		moduleDir := findNearestGoMod(repoRoot, filepath.Dir(dep.File))
		if moduleDir == "" {
			continue // no governing module
		}
		floor := parseGoDirectiveFromFile(filepath.Join(repoRoot, moduleDir, "go.mod"))
		if floor == "" {
			continue
		}
		obs = append(obs, reconcile.GoBuilderObservation{
			File:          dep.File,
			Line:          dep.Line,
			Image:         image,
			CurrentTag:    tag,
			Floor:         floor,
			AvailableTags: dep.AvailableVersions,
			FloorSource:   fmt.Sprintf("%s: go %s", moduleGoModPath(moduleDir), floor),
		})
	}
	return obs
}

// applyReconcileMutations writes each derived mutation to the working tree via the
// hash-guarded Dockerfile editor, reusing the intentful path's write primitive (the
// DECISION was already made by the reconciler; only the file write is shared).
// Returns the applied records, the touched files, and config errors for any
// mutation that could not be written.
func applyReconcileMutations(repoRoot string, muts []reconcile.Mutation) ([]AppliedUpdate, []string, []reconcile.ConfigError) {
	synth := make([]supplychain.Dependency, 0, len(muts))
	byKey := make(map[string]reconcile.Mutation, len(muts))
	for _, m := range muts {
		_, curTag := splitImageTag(m.From)
		_, newTag := splitImageTag(m.To)
		synth = append(synth, supplychain.Dependency{
			Name:           m.From, // full image:tag reference
			Current:        curTag,
			ResolvedTarget: newTag, // UpdateTarget() honors this → minimal-diff FROM edit
			Ecosystem:      supplychain.EcosystemDockerImage,
			File:           m.File,
			Line:           m.Line,
		})
		byKey[mutationKey(m.File, m.Line)] = m
	}

	applied, skipped, touched, err := applyDockerfileUpdates(synth, repoRoot)
	if err != nil {
		// A file-write failure is not silently swallowed: every mutation for a file
		// that failed to write stays unreconciled, so surface them all.
		var cerrs []reconcile.ConfigError
		for _, m := range muts {
			cerrs = append(cerrs, reconcile.ConfigError{
				Reconciler: m.Reconciler,
				File:       m.File,
				Line:       m.Line,
				Message:    fmt.Sprintf("could not write reconciliation of %s → %s: %v", m.From, m.To, err),
			})
		}
		return nil, nil, cerrs
	}

	// Mark applied records as reconciliation for reporting clarity.
	for i := range applied {
		applied[i].Remediation = "reconcile:" + reconcileNameFor(byKey, applied[i].Dep)
	}

	// Any skipped synthetic dep means its reconciliation did not land — report it.
	var cerrs []reconcile.ConfigError
	for _, s := range skipped {
		if m, ok := byKey[mutationKey(s.Dep.File, s.Dep.Line)]; ok {
			cerrs = append(cerrs, reconcile.ConfigError{
				Reconciler: m.Reconciler,
				File:       m.File,
				Line:       m.Line,
				Message:    fmt.Sprintf("could not reconcile %s → %s: %s", m.From, m.To, s.Reason),
			})
		}
	}
	return applied, touched, cerrs
}

func mutationKey(file string, line int) string { return fmt.Sprintf("%s:%d", file, line) }

func reconcileNameFor(byKey map[string]reconcile.Mutation, dep supplychain.Dependency) string {
	if m, ok := byKey[mutationKey(dep.File, dep.Line)]; ok {
		return m.Reconciler
	}
	return "repository"
}

// parseFromImageTag extracts (image, tag) from a Dockerfile FROM line, rejecting
// digest- and ARG-pinned references (which cannot be deterministically rewritten).
func parseFromImageTag(line string) (image, tag string, ok bool) {
	m := fromRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	token := m[2]
	if strings.Contains(token, "@sha256:") || strings.ContainsAny(token, "$") {
		return "", "", false
	}
	img, t := splitImageTag(token)
	if t == "" {
		return "", "", false
	}
	return img, t, true
}

// splitImageTag splits an image reference into (image, tag) on the last colon that
// follows the last slash, so a registry host:port prefix is not mistaken for a tag.
func splitImageTag(token string) (image, tag string) {
	nameStart := 0
	if lastSlash := strings.LastIndex(token, "/"); lastSlash >= 0 {
		nameStart = lastSlash + 1
	}
	colon := strings.LastIndex(token[nameStart:], ":")
	if colon < 0 {
		return token, ""
	}
	idx := nameStart + colon
	return token[:idx], token[idx+1:]
}
