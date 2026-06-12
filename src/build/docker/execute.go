package docker

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/cas"
)

// extractExitCode extracts the process exit code from an error.
// Returns 1 if the error is not an exec.ExitError.
func extractExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

// transportActive reports whether cross-phase artifact transport is active. It
// keys on the store's Transport policy: when active, perform retains the bytes and
// publish is the sole distributor. A nil store (non-lifecycle callers) is no transport.
func transportActive(req Request) bool {
	return req.Store != nil && req.Store.Transport()
}

// captureArtifactDigests writes each image step's OCI index digest onto the
// matching outputs artifact, keyed by step name — materializing identity at build
// completion. Sourced ONLY from containerimage.digest (never docker inspect {{.Id}},
// which diverges on multi-platform). Best-effort: missing metadata leaves Digest empty.
func captureArtifactDigests(plan *build.BuildPlan, outputs *artifact.OutputsManifest) {
	if plan == nil || outputs == nil {
		return
	}
	digestByName := make(map[string]artifact.Digest)
	for _, step := range plan.Steps {
		if step.Output != build.OutputImage || step.MetadataFile == "" {
			continue
		}
		if d, err := ParseMetadataDigest(step.MetadataFile); err == nil && d != "" {
			digestByName[step.Name] = artifact.Digest(d)
		}
	}
	for i := range outputs.Artifacts {
		if outputs.Artifacts[i].Kind != "docker" {
			continue
		}
		if d, ok := digestByName[outputs.Artifacts[i].Name]; ok {
			outputs.Artifacts[i].Digest = d
		}
	}
}

// RetainedArtifact is the evidence that perform retained an artifact's bytes to
// the content store (and therefore did not distribute them): name → exact digest
// now stored → on-disk handle publish will resolve.
type RetainedArtifact struct {
	Name, Digest, Path, Failure string
}

// persistArtifactsRecords retains each docker artifact's OCI layout to the
// content store and records the persistence handle on the manifest. It returns
// one record per retained artifact and does NOT render — callers fold the
// records as rows under the Publish domain box (the crucible contributor).
func persistArtifactsRecords(plan *build.BuildPlan, outputs *artifact.OutputsManifest, store cas.Store) []RetainedArtifact {
	if plan == nil || outputs == nil || store == nil || !store.RequiresOCIExport() {
		return nil
	}
	layoutByName := make(map[string]string)
	for _, step := range plan.Steps {
		if step.Output == build.OutputImage && step.OCILayoutDir != "" {
			layoutByName[step.Name] = step.OCILayoutDir
		}
	}
	var records []RetainedArtifact
	for i := range outputs.Artifacts {
		a := &outputs.Artifacts[i]
		if a.Kind != "docker" || a.Digest == "" {
			continue
		}
		layoutDir, ok := layoutByName[a.Name]
		if !ok {
			continue
		}
		storedDir, err := store.Put(cas.Digest(a.Digest), layoutDir)
		if err != nil {
			// Persistence failure is loud but non-fatal to the build: the image
			// was built and (if push/load) is available; what's lost is the
			// transport guarantee for this artifact. Surface it so it is never
			// silently assumed present downstream.
			records = append(records, RetainedArtifact{Name: a.Name, Failure: err.Error()})
			continue
		}
		a.Persistence = artifact.PersistenceHandle{
			Kind:      artifact.PersistenceOCILayout,
			OCILayout: &artifact.OCILayoutRef{Path: storedDir},
		}
		records = append(records, RetainedArtifact{Name: a.Name, Digest: string(a.Digest), Path: storedDir})
	}
	return records
}

// isCacheExportError returns true if the build failure is caused by cache export
// (auth, network, permission) rather than the actual build. Cache export failures
// should never break builds — the build is retried without --cache-to.
func isCacheExportError(err error, combinedOutput string) bool {
	lower := strings.ToLower(combinedOutput)
	return strings.Contains(lower, "exporting cache") ||
		strings.Contains(lower, "failed to export cache") ||
		strings.Contains(lower, "error writing layer blob") ||
		strings.Contains(lower, "insufficient_scope")
}

// setupTransportPlan stages digest-capture metadata and, when the store requires
// OCI export, a content-store layout dir onto EVERY image step; returns a cleanup
// func to defer. It sets only MetadataFile/OCILayoutDir, never Push/Load — the
// retain-vs-push strategy stays with the caller, and the digest is sourced only
// from containerimage.digest. Unconditional by design: no predicate to drift.
func setupTransportPlan(plan *build.BuildPlan, store cas.Store, rootDir string) func() {
	var metaCleanup, layoutCleanup []string
	for i := range plan.Steps {
		if plan.Steps[i].Output != build.OutputImage {
			continue
		}
		if metaFile, err := os.CreateTemp("", "buildx-metadata-*.json"); err == nil {
			plan.Steps[i].MetadataFile = metaFile.Name()
			metaFile.Close()
			metaCleanup = append(metaCleanup, metaFile.Name())
		}
	}
	if store != nil && store.RequiresOCIExport() {
		for i := range plan.Steps {
			if plan.Steps[i].Output != build.OutputImage {
				continue
			}
			// Stage on the same filesystem as the store (workspace .stagefreight/)
			// so persistArtifactsRecords hardlinks rather than copies.
			stage := filepath.Join(rootDir, ".stagefreight")
			_ = os.MkdirAll(stage, 0o755)
			if layoutDir, err := os.MkdirTemp(stage, "oci-layout-*"); err == nil {
				plan.Steps[i].OCILayoutDir = layoutDir
				layoutCleanup = append(layoutCleanup, layoutDir)
			}
		}
	}
	return func() {
		for _, f := range metaCleanup {
			os.Remove(f)
		}
		for _, d := range layoutCleanup {
			os.RemoveAll(d)
		}
	}
}
