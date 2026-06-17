package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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

// ResolvedSignatureAsset is a successfully-produced detached signature (e.g.
// SHA256SUMS.sig) ready for distribution. Manifest-sourced: ArtifactID + Path come
// from the results manifest's blob_signature outcomes — never a filesystem glob.
type ResolvedSignatureAsset struct {
	ArtifactID ArtifactID
	Path       string // the detached signature (.sig) path
	BlobPath   string // the signed blob it covers (e.g. SHA256SUMS)
	TrustClass string // resolved trust class that signed it (display/provenance)
}

// SuccessfulBlobSignatureAssets extracts the successful detached blob signatures
// from a results manifest, preserving order. Pure + manifest-sourced — same
// non-globbing invariant as SuccessfulArchiveAssets; the .sig path is recorded by
// the signer, never reconstructed from a name.
func SuccessfulBlobSignatureAssets(results *ResultsManifest) []ResolvedSignatureAsset {
	var out []ResolvedSignatureAsset
	if results == nil {
		return out
	}
	for _, r := range results.Results {
		for _, o := range r.Outcomes {
			if o.Type != OutcomeTypeBlobSignature || o.BlobSignature == nil {
				continue
			}
			if o.BlobSignature.Status != OutcomeSuccess || o.BlobSignature.SignaturePath == "" {
				continue
			}
			out = append(out, ResolvedSignatureAsset{
				ArtifactID: r.ArtifactID,
				Path:       o.BlobSignature.SignaturePath,
				BlobPath:   o.BlobSignature.BlobPath,
				TrustClass: o.BlobSignature.TrustClass,
			})
		}
	}
	return out
}

// ValidateRecordedDigests recomputes each successfully-built archive's SHA-256 and
// confirms it matches the results manifest — refusing to attach a signature to an
// artifact that drifted (or vanished) since the build. Files are located by base
// name under distDir, so it works whether signing runs on the build host or a
// separate signer host. This is the "refuse unsigned artifact drift" guard for the
// additive `stagefreight sign` flow.
func ValidateRecordedDigests(results *ResultsManifest, distDir string) error {
	if results == nil {
		return nil
	}
	for _, r := range results.Results {
		for _, o := range r.Outcomes {
			if o.Archive == nil || o.Archive.Status != OutcomeSuccess || o.Archive.SHA256 == "" {
				continue
			}
			path := filepath.Join(distDir, filepath.Base(o.Archive.Path))
			sum, err := sha256File(path)
			if err != nil {
				return fmt.Errorf("artifact %s: %v (drift: missing or unreadable since build)", r.ArtifactID, err)
			}
			if sum != strings.TrimPrefix(o.Archive.SHA256, "sha256:") {
				return fmt.Errorf("artifact %s digest drift: %s on disk != %s recorded at build — refusing to sign", r.ArtifactID, sum, o.Archive.SHA256)
			}
		}
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
