package docker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
)

// writeMetadataFile writes a buildx-style metadata JSON carrying the given
// containerimage.digest, and returns its path.
func writeMetadataFile(t *testing.T, dir, digest string) string {
	t.Helper()
	path := filepath.Join(dir, "meta-"+digest[len(digest)-6:]+".json")
	body := map[string]any{
		"containerimage.digest": digest,
		"image.name":            "docker.io/org/app:v1",
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	return path
}

// finalizedOutputs builds a frozen outputs manifest with one docker artifact
// named `name` (no digest yet — as produced at plan time).
func finalizedOutputs(t *testing.T, name string) *artifact.OutputsManifest {
	t.Helper()
	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind: "docker",
			Name: name,
			Docker: &artifact.DockerDescriptor{
				Dockerfile: "Dockerfile",
				Context:    ".",
				Platforms:  []string{"linux/amd64"},
			},
			Targets: []artifact.Target{{
				Kind: "registry",
				Registry: &artifact.RegistryTarget{
					Host: "docker.io", Path: "org/app", Tags: []string{"v1"},
				},
			}},
		}},
	}
	if err := m.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return &m
}

// TestCaptureArtifactDigests_SourcedFromMetadata is the provenance test: it
// proves Artifact.Digest is sourced from the buildx metadata file
// (containerimage.digest), NOT from any daemon inspection. The metadata digest
// is a value that could only come from the metadata file, so a passing assert
// is proof of provenance, not mere equality.
func TestCaptureArtifactDigests_SourcedFromMetadata(t *testing.T) {
	dir := t.TempDir()
	const metaDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	metaPath := writeMetadataFile(t, dir, metaDigest)

	plan := &build.BuildPlan{Steps: []build.BuildStep{{
		Name:         "app",
		Output:       build.OutputImage,
		Load:         true, // load-only build — NOT a push; identity must still be captured
		MetadataFile: metaPath,
	}}}
	outputs := finalizedOutputs(t, "app")

	captureArtifactDigests(plan, outputs)

	if got := outputs.Artifacts[0].Digest; got != artifact.Digest(metaDigest) {
		t.Fatalf("Digest = %q, want metadata digest %q (must be sourced from containerimage.digest, not daemon)", got, metaDigest)
	}
}

// TestCaptureArtifactDigests_IgnoresDaemonIdShape guards the {{.Id}} trap: even
// if a daemon-derived config ID would coincide with the manifest digest on a
// trivial image, capture must read the metadata file and nothing else. We prove
// this structurally — there is no daemon access path in capture — by giving the
// metadata a digest distinct from any plausible local image ID and asserting it
// wins. (capture reads only step.MetadataFile; it has no docker inspect call.)
func TestCaptureArtifactDigests_IgnoresDaemonIdShape(t *testing.T) {
	dir := t.TempDir()
	const metaDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	metaPath := writeMetadataFile(t, dir, metaDigest)

	plan := &build.BuildPlan{Steps: []build.BuildStep{{
		Name: "app", Output: build.OutputImage, Push: true, MetadataFile: metaPath,
	}}}
	outputs := finalizedOutputs(t, "app")

	captureArtifactDigests(plan, outputs)

	if outputs.Artifacts[0].Digest != artifact.Digest(metaDigest) {
		t.Fatalf("Digest = %q, want %q", outputs.Artifacts[0].Digest, metaDigest)
	}
}

// TestCaptureArtifactDigests_NoMetadataLeavesEmpty verifies best-effort
// semantics: a step with no metadata file leaves Digest empty rather than
// erroring or fabricating identity.
func TestCaptureArtifactDigests_NoMetadataLeavesEmpty(t *testing.T) {
	plan := &build.BuildPlan{Steps: []build.BuildStep{{
		Name: "app", Output: build.OutputImage, Load: true, MetadataFile: "",
	}}}
	outputs := finalizedOutputs(t, "app")

	captureArtifactDigests(plan, outputs)

	if outputs.Artifacts[0].Digest != "" {
		t.Fatalf("Digest = %q, want empty (no metadata → no identity claim)", outputs.Artifacts[0].Digest)
	}
}

// TestCaptureArtifactDigests_DigestEntersChecksum proves the captured digest is
// folded into the manifest checksum on re-finalize — so review approves an
// outputs.json whose checksum binds the identity.
func TestCaptureArtifactDigests_DigestEntersChecksum(t *testing.T) {
	dir := t.TempDir()
	const metaDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	metaPath := writeMetadataFile(t, dir, metaDigest)

	outputs := finalizedOutputs(t, "app")
	before := outputs.Checksum

	plan := &build.BuildPlan{Steps: []build.BuildStep{{
		Name: "app", Output: build.OutputImage, Load: true, MetadataFile: metaPath,
	}}}
	captureArtifactDigests(plan, outputs)
	if err := outputs.Finalize(); err != nil {
		t.Fatalf("re-finalize: %v", err)
	}

	if outputs.Checksum == before {
		t.Fatal("checksum unchanged after digest capture — identity is not bound into the manifest checksum")
	}
}
