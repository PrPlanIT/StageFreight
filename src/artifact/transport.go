package artifact

import (
	"compress/gzip"
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/unpack"
)

// ArtifactTransport is a successful build's publish-transport representation inside
// ManagedRoot — today always an archive (tar.gz/zip). It is the ONLY thing the publish
// phase consumes for a build: publish never reaches back to an original build output
// directory (which lives outside ManagedRoot and does not cross the perform→publish
// job boundary). Extract materializes it into a publish workspace.
//
// Returning a value with Extract (rather than a bare path) keeps resolution and
// materialization as separate responsibilities, leaving room for other transports
// (zip/OCI/CAS/remote) without changing callers.
type ArtifactTransport struct {
	Path   string // archive file, under ManagedRoot
	Format string // "tar.gz" | "zip"
}

// ResolveSuccessfulBuildOutput returns the transport archive for a successful build,
// sourced SOLELY from the manifests — never globbing the filesystem (the archive
// resolution invariant, see distribution.go). A build's binary/tree output is wrapped
// by an archive whose Sources reference the build's artifact; that archive is the
// transport. Callers (e.g. the pages publish runner) depend only on this seam, not on
// how a build's output is currently modeled (today a Kind:"binary" artifact whose Path
// is a directory) — so a future first-class tree artifact is an internal change here.
func ResolveSuccessfulBuildOutput(rootDir, buildID string) (ArtifactTransport, error) {
	outputs, err := ReadOutputsManifest(rootDir)
	if err != nil {
		return ArtifactTransport{}, err
	}
	results, err := ReadResultsManifest(rootDir)
	if err != nil {
		return ArtifactTransport{}, err
	}
	return resolveBuildTransport(outputs, results, buildID)
}

// resolveBuildTransport is the pure manifest join (testable without disk).
func resolveBuildTransport(outputs *OutputsManifest, results *ResultsManifest, buildID string) (ArtifactTransport, error) {
	binIDs := map[ArtifactID]bool{}
	for _, v := range BuildBinaryExecutionViews(outputs, results) {
		if v.BuildID == buildID && v.BuildStatus == OutcomeSuccess {
			binIDs[v.ArtifactID] = true
		}
	}
	if len(binIDs) == 0 {
		return ArtifactTransport{}, fmt.Errorf("build %q produced no successful output", buildID)
	}
	for _, av := range SuccessfulArchiveAssets(BuildArchiveExecutionViews(outputs, results)) {
		for _, src := range av.Sources {
			if binIDs[src] {
				return ArtifactTransport{Path: av.Path, Format: archiveFormatOf(av.Path)}, nil
			}
		}
	}
	return ArtifactTransport{}, fmt.Errorf(
		"build %q has no archive under %s — a pages build must be archived (a binary-archive target) so its output crosses the perform→publish boundary",
		buildID, ManagedRoot)
}

func archiveFormatOf(path string) string {
	if strings.HasSuffix(path, ".zip") {
		return "zip"
	}
	return "tar.gz"
}

// Extract materializes the transport archive into destDir (created if needed).
func (t ArtifactTransport) Extract(destDir string) error {
	if t.Path == "" {
		return fmt.Errorf("artifact transport: empty path")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	if t.Format == "zip" {
		return unpack.Zip(t.Path, destDir)
	}
	f, err := os.Open(t.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	return unpack.Tar(gz, destDir, 0)
}
