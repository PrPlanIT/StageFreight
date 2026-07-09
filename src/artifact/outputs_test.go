package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validDockerArtifact returns a minimal valid Artifact for tests.
func validDockerArtifact() Artifact {
	return Artifact{
		Kind: "docker",
		Name: "stagefreight",
		Docker: &DockerDescriptor{
			Dockerfile: "Dockerfile",
			Context:    ".",
			Platforms:  []string{"linux/amd64"},
		},
		Targets: []Target{
			{
				Kind: "registry",
				Registry: &RegistryTarget{
					Host: "docker.io",
					Path: "prplanit/stagefreight",
					Tags: []string{"latest-dev", "dev-30d3da2d"},
				},
				Requirements: Requirements{Sign: true},
			},
		},
	}
}

func TestOutputsManifestAcceptsTargetlessDockerArtifact(t *testing.T) {
	// Produced != published: a docker image produced on a ref that no publish
	// target matches carries zero distribution targets. The truth model MUST
	// accept it — it is a legitimate "produced but not distributed" record that
	// review scans and publish discloses. (Binary/archive still forbid targets;
	// only docker's ≥1-target minimum is relaxed.)
	dir := t.TempDir()
	a := validDockerArtifact()
	a.Targets = nil
	if err := WriteOutputsManifest(dir, OutputsManifest{Artifacts: []Artifact{a}}); err != nil {
		t.Fatalf("WriteOutputsManifest rejected a targetless docker artifact: %v", err)
	}
	got, err := ReadOutputsManifest(dir)
	if err != nil {
		t.Fatalf("ReadOutputsManifest rejected a targetless docker artifact: %v", err)
	}
	if len(got.Artifacts) != 1 || len(got.Artifacts[0].Targets) != 0 {
		t.Fatalf("round-trip altered the untargeted docker artifact: %+v", got.Artifacts)
	}
}

func TestOutputsManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	manifest := OutputsManifest{
		Commit:   "30d3da2d",
		Pipeline: &Pipeline{ID: "7847", Provider: "gitlab"},
		Artifacts: []Artifact{validDockerArtifact()},
	}

	if err := WriteOutputsManifest(dir, manifest); err != nil {
		t.Fatalf("WriteOutputsManifest: %v", err)
	}

	got, err := ReadOutputsManifest(dir)
	if err != nil {
		t.Fatalf("ReadOutputsManifest: %v", err)
	}

	if got.SchemaVersion != OutputsSchemaVersion {
		t.Fatalf("schema_version: got %q want %q", got.SchemaVersion, OutputsSchemaVersion)
	}
	if got.GeneratedAt == "" {
		t.Fatal("generated_at not populated")
	}
	if len(got.Artifacts) != 1 {
		t.Fatalf("artifacts: got %d want 1", len(got.Artifacts))
	}
	a := got.Artifacts[0]
	if a.ID != "docker:stagefreight" {
		t.Fatalf("artifact ID: got %q want %q", a.ID, "docker:stagefreight")
	}
	if a.Docker == nil || a.Docker.Dockerfile != "Dockerfile" {
		t.Fatal("docker descriptor not preserved")
	}
}

func TestOutputsManifestDeterministicChecksum(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	mk := func() OutputsManifest {
		return OutputsManifest{
			GeneratedAt: "2026-05-30T02:15:00Z", // fixed to remove time dependency
			Commit:      "30d3da2d",
			Artifacts:   []Artifact{validDockerArtifact()},
		}
	}

	if err := WriteOutputsManifest(dir1, mk()); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := WriteOutputsManifest(dir2, mk()); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	data1, _ := os.ReadFile(filepath.Join(dir1, OutputsManifestPath))
	data2, _ := os.ReadFile(filepath.Join(dir2, OutputsManifestPath))
	if !bytes.Equal(data1, data2) {
		t.Fatalf("identical inputs produced different bytes:\n--- a ---\n%s\n--- b ---\n%s",
			data1, data2)
	}
}

func TestOutputsManifestChecksumMismatchFails(t *testing.T) {
	dir := t.TempDir()
	if err := WriteOutputsManifest(dir, OutputsManifest{
		Artifacts: []Artifact{validDockerArtifact()},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	path := filepath.Join(dir, OutputsManifestPath)
	data, _ := os.ReadFile(path)
	tampered := bytes.Replace(data, []byte("stagefreight"), []byte("tampered    "), 1)
	if err := os.WriteFile(path, tampered, 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}

	_, err := ReadOutputsManifest(dir)
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected ErrOutputsManifestInvalid, got %v", err)
	}
}

func TestOutputsManifestMissingChecksumFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, OutputsManifestPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Manually construct without checksum
	raw := map[string]any{
		"schema_version": OutputsSchemaVersion,
		"generated_at":   "2026-05-30T02:15:00Z",
		"artifacts": []map[string]any{
			{
				"id":   "docker:stagefreight",
				"kind": "docker",
				"name": "stagefreight",
				"docker": map[string]any{
					"dockerfile": "Dockerfile",
					"context":    ".",
					"platforms":  []string{"linux/amd64"},
				},
				"targets": []map[string]any{
					{
						"kind": "registry",
						"registry": map[string]any{
							"host": "docker.io", "path": "prplanit/sf", "tags": []string{"latest-dev"},
						},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadOutputsManifest(dir)
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected ErrOutputsManifestInvalid, got %v", err)
	}
}

func TestOutputsManifestNotFound(t *testing.T) {
	_, err := ReadOutputsManifest(t.TempDir())
	if !errors.Is(err, ErrOutputsManifestNotFound) {
		t.Fatalf("expected ErrOutputsManifestNotFound, got %v", err)
	}
}

func TestOutputsManifestRejectsBadTime(t *testing.T) {
	dir := t.TempDir()
	err := WriteOutputsManifest(dir, OutputsManifest{
		GeneratedAt: "yesterday",
		Artifacts:   []Artifact{validDockerArtifact()},
	})
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected ErrOutputsManifestInvalid for bad time, got %v", err)
	}
}

func TestOutputsManifestRejectsBadSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	err := WriteOutputsManifest(dir, OutputsManifest{
		SchemaVersion: "99",
		Artifacts:     []Artifact{validDockerArtifact()},
	})
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected ErrOutputsManifestInvalid for bad schema, got %v", err)
	}
}

func TestArtifactIDAutoPopulated(t *testing.T) {
	dir := t.TempDir()
	a := validDockerArtifact()
	a.ID = "" // leave empty; Write should populate
	if err := WriteOutputsManifest(dir, OutputsManifest{Artifacts: []Artifact{a}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadOutputsManifest(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Artifacts[0].ID != "docker:stagefreight" {
		t.Fatalf("ID not auto-populated: got %q", got.Artifacts[0].ID)
	}
}

func TestArtifactIDMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	a := validDockerArtifact()
	a.ID = "docker:wrong-name"
	err := WriteOutputsManifest(dir, OutputsManifest{Artifacts: []Artifact{a}})
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected invalid ID error, got %v", err)
	}
}

func TestDuplicateArtifactIDRejected(t *testing.T) {
	dir := t.TempDir()
	err := WriteOutputsManifest(dir, OutputsManifest{
		Artifacts: []Artifact{validDockerArtifact(), validDockerArtifact()},
	})
	if !errors.Is(err, ErrOutputsManifestInvalid) || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate ID error, got %v", err)
	}
}

func TestDescriptorMustMatchKind(t *testing.T) {
	dir := t.TempDir()
	a := Artifact{
		Kind: "docker",
		Name: "x",
		// No docker descriptor, but binary descriptor set — mismatched
		Binary: &BinaryDescriptor{OS: "linux", Arch: "amd64", Path: "x"},
		Targets: []Target{{
			Kind:     "registry",
			Registry: &RegistryTarget{Host: "docker.io", Path: "x", Tags: []string{"v1"}},
		}},
	}
	err := WriteOutputsManifest(dir, OutputsManifest{Artifacts: []Artifact{a}})
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected invalid descriptor error, got %v", err)
	}
}

func TestMultipleDescriptorsRejected(t *testing.T) {
	dir := t.TempDir()
	a := validDockerArtifact()
	a.Binary = &BinaryDescriptor{OS: "linux", Arch: "amd64", Path: "x"}
	err := WriteOutputsManifest(dir, OutputsManifest{Artifacts: []Artifact{a}})
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected multi-descriptor error, got %v", err)
	}
}

func TestTargetMustMatchKind(t *testing.T) {
	dir := t.TempDir()
	a := validDockerArtifact()
	a.Targets = []Target{{
		Kind:              "registry",
		ForgeReleaseAsset: &ForgeReleaseAssetTarget{AssetName: "foo"}, // wrong kind
	}}
	err := WriteOutputsManifest(dir, OutputsManifest{Artifacts: []Artifact{a}})
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected target-kind mismatch error, got %v", err)
	}
}

func TestEmptyArtifactRejected(t *testing.T) {
	dir := t.TempDir()
	err := WriteOutputsManifest(dir, OutputsManifest{
		Artifacts: []Artifact{{Kind: "", Name: "x"}},
	})
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected kind-required error, got %v", err)
	}
}

// Note: a targetless docker artifact is now VALID (produced != published) —
// see TestOutputsManifestAcceptsTargetlessDockerArtifact. Only binary/archive
// forbid targets; that rule is exercised by the results/boundary tests.

func TestRegistryTargetRequiredFields(t *testing.T) {
	dir := t.TempDir()
	a := validDockerArtifact()
	a.Targets[0].Registry.Tags = nil // empty tags
	err := WriteOutputsManifest(dir, OutputsManifest{Artifacts: []Artifact{a}})
	if !errors.Is(err, ErrOutputsManifestInvalid) {
		t.Fatalf("expected tags-required error, got %v", err)
	}
}

func TestHostNormalization(t *testing.T) {
	dir := t.TempDir()
	a := validDockerArtifact()
	a.Targets[0].Registry.Host = "https://Docker.IO/"
	if err := WriteOutputsManifest(dir, OutputsManifest{Artifacts: []Artifact{a}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadOutputsManifest(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if h := got.Artifacts[0].Targets[0].Registry.Host; h != "docker.io" {
		t.Fatalf("host not normalized: got %q", h)
	}
}

func TestArtifactIDHelper(t *testing.T) {
	if got := NewArtifactID("docker", "x"); got != "docker:x" {
		t.Fatalf("ArtifactID: got %q want %q", got, "docker:x")
	}
}
