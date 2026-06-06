package artifact

// distribution.go holds the shared archive-resolution plumbing used by every
// binary distribution backend — forge release assets, generic package registries,
// and (future) OCI artifacts. The backends differ in their *identity model* (a
// release tag vs a package version vs a digest) and protocol, but they all
// distribute the SAME archive bytes, resolved the SAME way. That resolution lives
// here, once.

// ResolvedArchiveAsset is a successfully-built archive ready for distribution.
// Every field is sourced from the v2 manifests (outputs + results): ArtifactID is
// the identity, Path/SHA256/Size come from the build result, Sources lists the
// binary ArtifactIDs the archive wraps. Name is display-only.
type ResolvedArchiveAsset struct {
	ArtifactID ArtifactID
	Name       string
	Path       string
	SHA256     string
	Size       int64
	Sources    []ArtifactID
}

// SuccessfulArchiveAssets filters built archive views to those that built
// successfully, preserving input order. Pure — operates on already-built views,
// so a caller that already holds the views (e.g. the release path, which also
// needs binary/publication views from the same manifests) reuses them without a
// re-read.
func SuccessfulArchiveAssets(views []ArchiveExecutionView) []ResolvedArchiveAsset {
	out := make([]ResolvedArchiveAsset, 0, len(views))
	for _, av := range views {
		if av.BuildStatus != OutcomeSuccess {
			continue
		}
		out = append(out, ResolvedArchiveAsset{
			ArtifactID: av.ArtifactID,
			Name:       av.ArtifactName,
			Path:       av.Path,
			SHA256:     av.SHA256,
			Size:       av.Size,
			Sources:    av.Sources,
		})
	}
	return out
}

// ResolveSuccessfulArchiveAssets reads the v2 manifests and returns the archives
// that built successfully. This is the shared archive-resolution entry point for
// distribution backends that don't already hold the views.
//
// NON-NEGOTIABLE INVARIANT: assets are derived SOLELY from the manifests
// (results → archive execution view → resolved assets). This function never globs
// the filesystem, never parses an artifact name to infer type/platform/identity,
// and never reconstructs an ArtifactID. ArtifactID is the only identity. Any
// future change that reaches outside the manifests to find or name an asset is a
// regression — see TestArchiveResolutionIsManifestSourced.
func ResolveSuccessfulArchiveAssets(rootDir string) ([]ResolvedArchiveAsset, error) {
	outputs, err := ReadOutputsManifest(rootDir)
	if err != nil {
		return nil, err
	}
	results, err := ReadResultsManifest(rootDir)
	if err != nil {
		return nil, err
	}
	return SuccessfulArchiveAssets(BuildArchiveExecutionViews(outputs, results)), nil
}
