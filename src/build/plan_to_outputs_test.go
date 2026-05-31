package build

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

func TestPlanToOutputsDockerStep(t *testing.T) {
	plan := &BuildPlan{
		Steps: []BuildStep{
			{
				Name:       "stagefreight",
				Dockerfile: "Dockerfile",
				Context:    ".",
				Platforms:  []string{"linux/amd64", "linux/arm64"},
				Tags:       []string{"latest-dev"},
				Output:     OutputImage,
				Registries: []RegistryTarget{
					{
						URL:  "docker.io",
						Path: "prplanit/stagefreight",
						Tags: []string{"latest-dev", "dev-abc"},
					},
				},
			},
		},
	}
	out, err := PlanToOutputs(plan, PlanToOutputsOpts{
		Commit:   "30d3da2d",
		Pipeline: &artifact.Pipeline{ID: "7847", Provider: "gitlab"},
	})
	if err != nil {
		t.Fatalf("PlanToOutputs: %v", err)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("artifacts: got %d want 1", len(out.Artifacts))
	}
	a := out.Artifacts[0]
	if a.Kind != "docker" || a.Name != "stagefreight" {
		t.Fatalf("artifact identity: %+v", a)
	}
	if a.Docker == nil {
		t.Fatal("Docker descriptor nil")
	}
	if a.Docker.Dockerfile != "Dockerfile" || a.Docker.Context != "." {
		t.Fatalf("docker descriptor: %+v", a.Docker)
	}
	if len(a.Docker.Platforms) != 2 {
		t.Fatalf("platforms: got %d want 2", len(a.Docker.Platforms))
	}
	if len(a.Targets) != 1 {
		t.Fatalf("targets: got %d want 1", len(a.Targets))
	}
	tg := a.Targets[0]
	if tg.Registry == nil || tg.Registry.Host != "docker.io" || tg.Registry.Path != "prplanit/stagefreight" {
		t.Fatalf("registry target: %+v", tg)
	}
	if len(tg.Registry.Tags) != 2 {
		t.Fatalf("tags: got %d want 2", len(tg.Registry.Tags))
	}
}

func TestPlanToOutputsRoundTripWritesValidManifest(t *testing.T) {
	// The whole point: PlanToOutputs → WriteOutputsManifest must succeed
	// without further mutation. If WriteOutputsManifest's validation
	// rejects, PlanToOutputs is producing structurally-broken output.
	plan := &BuildPlan{
		Steps: []BuildStep{
			{
				Name:       "sf",
				Dockerfile: "Dockerfile",
				Context:    ".",
				Platforms:  []string{"linux/amd64"},
				Output:     OutputImage,
				Registries: []RegistryTarget{
					{URL: "docker.io", Path: "org/sf", Tags: []string{"v1"}},
				},
			},
		},
	}
	out, err := PlanToOutputs(plan, PlanToOutputsOpts{Commit: "abc"})
	if err != nil {
		t.Fatalf("PlanToOutputs: %v", err)
	}
	dir := t.TempDir()
	if err := artifact.WriteOutputsManifest(dir, out); err != nil {
		t.Fatalf("WriteOutputsManifest: %v", err)
	}
	got, err := artifact.ReadOutputsManifest(dir)
	if err != nil {
		t.Fatalf("ReadOutputsManifest: %v", err)
	}
	if got.Artifacts[0].ID != "docker:sf" {
		t.Fatalf("ID not auto-populated: %q", got.Artifacts[0].ID)
	}
}

func TestPlanToOutputsSkipsNonImageSteps(t *testing.T) {
	plan := &BuildPlan{
		Steps: []BuildStep{
			{Name: "extract", Output: OutputLocal},
			{Name: "tar", Output: OutputTar},
			{
				Name: "img", Output: OutputImage,
				Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"},
				Registries: []RegistryTarget{{URL: "docker.io", Path: "org/x", Tags: []string{"v1"}}},
			},
		},
	}
	out, err := PlanToOutputs(plan, PlanToOutputsOpts{})
	if err != nil {
		t.Fatalf("PlanToOutputs: %v", err)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("expected only image steps, got %d artifacts", len(out.Artifacts))
	}
	if out.Artifacts[0].Name != "img" {
		t.Fatalf("wrong artifact selected: %q", out.Artifacts[0].Name)
	}
}

func TestPlanToOutputsSkipsImageStepsWithNoRegistry(t *testing.T) {
	// Steps configured for local-only builds (no remote distribution) have
	// no place in OutputsManifest — there's nothing to externalize.
	plan := &BuildPlan{
		Steps: []BuildStep{
			{
				Name: "local-only", Output: OutputImage,
				Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"},
				Registries: nil,
			},
		},
	}
	out, err := PlanToOutputs(plan, PlanToOutputsOpts{})
	if err != nil {
		t.Fatalf("PlanToOutputs: %v", err)
	}
	if len(out.Artifacts) != 0 {
		t.Fatalf("expected 0 artifacts for local-only step, got %d", len(out.Artifacts))
	}
}

func TestPlanToOutputsBinaryAndArchive(t *testing.T) {
	// Per Q2 of Phase 4 design, binary and archive artifacts are
	// un-targeted: the build artifact IS the truth, distribution
	// destinations are decided later. Plans intentionally carry no
	// Targets field.
	out, err := PlanToOutputs(nil, PlanToOutputsOpts{
		BinaryPlans: []BinaryArtifactPlan{
			{
				Name: "sf-cli", OS: "linux", Arch: "amd64",
				Path: "dist/sf-cli", Toolchain: "go1.24.1", Version: "1.0.0",
			},
		},
		ArchivePlans: []ArchiveArtifactPlan{
			{
				Name: "sf-cli", Format: "tar.gz", Path: "dist/sf-cli.tar.gz", Version: "1.0.0",
			},
		},
	})
	if err != nil {
		t.Fatalf("PlanToOutputs: %v", err)
	}

	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts: got %d want 2", len(out.Artifacts))
	}
	// Sort order is (Kind, Name): "archive" < "binary".
	arch, bin := out.Artifacts[0], out.Artifacts[1]
	if arch.Kind != "archive" || arch.Archive == nil || arch.Archive.Format != "tar.gz" {
		t.Fatalf("archive descriptor at index 0: %+v", arch)
	}
	if bin.Kind != "binary" || bin.Binary == nil || bin.Binary.OS != "linux" || bin.Binary.Toolchain != "go1.24.1" {
		t.Fatalf("binary descriptor at index 1: %+v", bin)
	}

	// Roundtrip through Write/Read to confirm validity.
	dir := t.TempDir()
	if err := artifact.WriteOutputsManifest(dir, out); err != nil {
		t.Fatalf("WriteOutputsManifest: %v", err)
	}
	if _, err := artifact.ReadOutputsManifest(dir); err != nil {
		t.Fatalf("ReadOutputsManifest: %v", err)
	}
}

func TestPlanToOutputsDeterministicOrder(t *testing.T) {
	// Three independent input sources (docker steps, binary plans, archive
	// plans) must collapse into a single deterministic artifact ordering
	// based on (Kind, Name) — independent of input source order.
	plan := &BuildPlan{
		Steps: []BuildStep{
			{
				Name: "z-image", Output: OutputImage,
				Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"},
				Registries: []RegistryTarget{{URL: "docker.io", Path: "org/z", Tags: []string{"v1"}}},
			},
			{
				Name: "a-image", Output: OutputImage,
				Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"},
				Registries: []RegistryTarget{{URL: "docker.io", Path: "org/a", Tags: []string{"v1"}}},
			},
		},
	}
	out, err := PlanToOutputs(plan, PlanToOutputsOpts{
		BinaryPlans: []BinaryArtifactPlan{
			{Name: "z-bin", OS: "linux", Arch: "amd64", Path: "dist/z"},
			{Name: "a-bin", OS: "linux", Arch: "amd64", Path: "dist/a"},
		},
		ArchivePlans: []ArchiveArtifactPlan{
			{Name: "z-arch", Format: "tar.gz", Path: "dist/z.tar.gz"},
			{Name: "a-arch", Format: "tar.gz", Path: "dist/a.tar.gz"},
		},
	})
	if err != nil {
		t.Fatalf("PlanToOutputs: %v", err)
	}
	wantOrder := []artifact.ArtifactID{
		"archive:a-arch", "archive:z-arch",
		"binary:a-bin", "binary:z-bin",
		"docker:a-image", "docker:z-image",
	}
	for i, want := range wantOrder {
		if got := out.Artifacts[i].ID; got != want {
			t.Errorf("artifact[%d]: got %q want %q", i, got, want)
		}
	}
}

func TestPlanToOutputsBuildArgsDigestStable(t *testing.T) {
	args1 := map[string]string{"FOO": "bar", "BAZ": "qux"}
	args2 := map[string]string{"BAZ": "qux", "FOO": "bar"} // same content, different insertion
	h1 := hashBuildArgs(args1)
	h2 := hashBuildArgs(args2)
	if h1 != h2 {
		t.Fatalf("hashBuildArgs not key-order independent: %s vs %s", h1, h2)
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Fatalf("digest prefix missing: %s", h1)
	}
	// Changing a value changes the hash.
	args3 := map[string]string{"FOO": "DIFFERENT", "BAZ": "qux"}
	h3 := hashBuildArgs(args3)
	if h1 == h3 {
		t.Fatal("hashBuildArgs collided across different values")
	}
}

func TestPlanToOutputsPure(t *testing.T) {
	// Two invocations with identical inputs must produce identical outputs.
	// PlanToOutputs is allocation-only — GeneratedAt is filled by Write, not
	// by the constructor, so this test compares the allocator output
	// directly (pre-Write) to isolate purity from time injection.
	plan := &BuildPlan{
		Steps: []BuildStep{
			{
				Name: "sf", Output: OutputImage,
				Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"},
				BuildArgs:  map[string]string{"FOO": "bar"},
				Registries: []RegistryTarget{{URL: "docker.io", Path: "org/sf", Tags: []string{"v1"}}},
			},
		},
	}
	opts := PlanToOutputsOpts{
		GeneratedAt: "2026-05-30T02:15:00Z", // explicit injection — no nowUTC() leak
		Commit:      "abc",
		Pipeline:    &artifact.Pipeline{ID: "1", Provider: "gitlab"},
	}
	a, err := PlanToOutputs(plan, opts)
	if err != nil {
		t.Fatalf("PlanToOutputs a: %v", err)
	}
	b, err := PlanToOutputs(plan, opts)
	if err != nil {
		t.Fatalf("PlanToOutputs b: %v", err)
	}

	aBytes, _ := json.Marshal(a)
	bBytes, _ := json.Marshal(b)
	if !bytes.Equal(aBytes, bBytes) {
		t.Fatalf("non-deterministic allocation:\n--- a ---\n%s\n--- b ---\n%s", aBytes, bBytes)
	}
	if a.Checksum == "" {
		t.Fatal("PlanToOutputs must return a finalized (checksummed) manifest")
	}
	if a.Checksum != b.Checksum {
		t.Fatalf("checksum non-deterministic: %s vs %s", a.Checksum, b.Checksum)
	}
}
