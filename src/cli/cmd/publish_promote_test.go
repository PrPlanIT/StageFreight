package cmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/internal/testdocker"
	"github.com/PrPlanIT/StageFreight/src/artifact"
	builddocker "github.com/PrPlanIT/StageFreight/src/build/docker"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/google/go-containerregistry/pkg/crane"
)

// TestPromoteArtifacts_EndToEnd is the P4 finish-line proof: build → retain to
// CAS → promote from CAS to a registry, asserting the PUBLISHED digest equals
// the digest perform recorded. This closes the loop: the bytes distributed in
// publish are provably the bytes built in perform.
//
// Docker-gated.
func TestPromoteArtifacts_EndToEnd(t *testing.T) {
	if !testdocker.Available() {
		t.Skip("docker not available — end-to-end promotion proof runs only on a docker-capable runner")
	}

	// 1. Build an OCI layout + capture the recorded digest (perform side).
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM docker.io/library/alpine:3.24.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layoutDir := filepath.Join(t.TempDir(), "layout")
	metaFile := filepath.Join(t.TempDir(), "m.json")
	b := exec.Command("docker", "buildx", "build",
		"--output", "type=oci,tar=false,dest="+layoutDir, "--metadata-file", metaFile, work)
	if out, err := b.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	var meta struct {
		Digest string `json:"containerimage.digest"`
	}
	data, _ := os.ReadFile(metaFile)
	if err := json.Unmarshal(data, &meta); err != nil || meta.Digest == "" {
		t.Fatalf("metadata digest: %v", err)
	}

	// 2. Retain to CAS (what perform's persistArtifacts does).
	rootDir := t.TempDir()
	store := cas.NewFSStore(filepath.Join(rootDir, ".stagefreight", "objects"))
	stored, err := store.Put(cas.Digest(meta.Digest), layoutDir)
	if err != nil {
		t.Fatalf("cas put: %v", err)
	}

	// 3. Write outputs.json with the persistence handle (what perform records).
	reg := testdocker.Registry(t)

	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind:        "docker",
			Name:        "app",
			Digest:      artifact.Digest(meta.Digest),
			Docker:      &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
			Persistence: artifact.PersistenceHandle{Kind: artifact.PersistenceOCILayout, OCILayout: &artifact.OCILayoutRef{Path: stored}},
			Targets: []artifact.Target{{
				Kind:     "registry",
				Registry: &artifact.RegistryTarget{Host: reg, Path: "e2e/app", Tags: []string{"v1"}},
			}},
		}},
	}
	if err := os.MkdirAll(filepath.Join(rootDir, ".stagefreight"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteOutputsManifest(rootDir, m); err != nil {
		t.Fatal(err)
	}

	// 4. Publish-side promotion (no creds for the local registry).
	appCfg := &config.Config{}
	n, untargeted, err := promoteArtifacts(context.Background(), appCfg, rootDir, os.Stderr)
	if err != nil {
		t.Fatalf("promoteArtifacts: %v", err)
	}
	if n != 1 {
		t.Fatalf("promoted %d tags, want 1", n)
	}
	if untargeted != 0 {
		t.Fatalf("untargeted %d, want 0 (this artifact carries a registry target)", untargeted)
	}

	// 4b. PUBLISH OWNS PUBLICATION OUTCOMES: published.json now records the
	// promotion result, written BY the publish phase (not perform). Confirm it
	// exists and records a successful push of the recorded digest.
	results, rErr := artifact.ReadResultsManifest(rootDir)
	if rErr != nil {
		t.Fatalf("published.json not written by publish promotion: %v", rErr)
	}
	if len(results.Results) != 1 || len(results.Results[0].Outcomes) != 1 {
		t.Fatalf("expected 1 result with 1 outcome, got %+v", results.Results)
	}
	po := results.Results[0].Outcomes[0]
	if po.Type != artifact.OutcomeTypePush || po.Push == nil {
		t.Fatalf("expected a push outcome, got %+v", po)
	}
	if po.Push.Status != artifact.OutcomeSuccess {
		t.Fatalf("push outcome status = %q, want success", po.Push.Status)
	}
	if po.Push.Digest != meta.Digest {
		t.Fatalf("recorded outcome digest %q != promoted digest %q", po.Push.Digest, meta.Digest)
	}
	if po.Push.ObservedBy != "promote" {
		t.Fatalf("outcome ObservedBy = %q, want promote (publish-phase observation)", po.Push.ObservedBy)
	}

	// 5. THE PROOF: registry serves exactly the digest perform recorded.
	// promoteArtifacts already performed an internal post-push verify (it errors
	// if the registry-served digest != recorded digest), so reaching here with
	// n==1 and no error is itself the guarantee. We additionally ask the registry
	// directly, through the library that does the push.
	published, err := crane.Digest(reg+"/e2e/app:v1", crane.Insecure)
	if err != nil {
		t.Fatalf("reading the published digest: %v", err)
	}
	if published != meta.Digest {
		t.Fatalf("PUBLISHED DIGEST != PERFORM DIGEST: published %q, perform recorded %q", published, meta.Digest)
	}
	t.Logf("end-to-end proof: perform digest == published digest == %s", meta.Digest)
}

// TestPromoteArtifacts_PartialFailureReportsAll proves the failure semantics:
// when a tag fails, promotion does NOT abandon the rest half-done — it attempts
// every tag, and the returned error carries the aggregate VERDICT (failed/total
// counts — a total of 2 proves both were attempted). Per-target evidence lives
// in the Distribution box and published.json, never duplicated into the error.
// Uses an unreachable host so no registry is needed.
func TestPromoteArtifacts_PartialFailureReportsAll(t *testing.T) {
	root := t.TempDir()
	layoutDir, digest := writeValidLayout(t, []byte("partial-failure-manifest"))
	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind:        "docker",
			Name:        "app",
			Digest:      digest,
			Docker:      &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
			Persistence: artifact.PersistenceHandle{Kind: artifact.PersistenceOCILayout, OCILayout: &artifact.OCILayoutRef{Path: layoutDir}},
			Targets: []artifact.Target{{
				Kind:     "registry",
				Registry: &artifact.RegistryTarget{Host: "127.0.0.1:1", Path: "x/app", Tags: []string{"a", "b"}},
			}},
		}},
	}
	if err := os.MkdirAll(filepath.Join(root, ".stagefreight"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteOutputsManifest(root, m); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	_, _, err := promoteArtifacts(context.Background(), &config.Config{}, root, &buf)
	if err == nil {
		t.Fatal("expected an aggregated error for unreachable targets")
	}
	msg := err.Error()
	if !strings.Contains(msg, "2 of 2 tag(s) failed to publish") {
		t.Errorf("error must carry the aggregate verdict proving both tags were attempted: %v", msg)
	}
	if out := buf.String(); strings.Contains(out, "digest preserved") || strings.Contains(out, "rebuild not required") {
		t.Errorf("nothing published, so no digest claim may render:\n%s", out)
	}
}

// One shared reason under the rows, and the class on a signed row.
func TestRenderSigning(t *testing.T) {
	var buf strings.Builder
	reason := "no usable signer: set signing.auto_provision + signing.state_dir, or supply a key/profile"
	renderSigning(&buf, []builddocker.SignResult{
		{Ref: "docker.io/a@sha256:1", Skipped: reason},
		{Ref: "ghcr.io/a@sha256:1", Skipped: reason},
		{Ref: "cr.optcp.com/a@sha256:1", Signed: true, Class: "key"},
	}, nil, false)
	out := buf.String()
	if strings.Count(out, "no usable signer") != 1 {
		t.Errorf("the reason must print once:\n%s", out)
	}
	for _, want := range []string{"⚠  NOT signed docker.io/a@sha256:1", "⚠  NOT signed ghcr.io/a@sha256:1", "✓  signed (key) cr.optcp.com/a@sha256:1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// TestPromoteArtifacts_UntargetedDiscloses proves the produce-vs-publish
// decoupling on the publish side: a retained docker artifact that carries NO
// registry target (no publish target matched this ref) is NOT distributed and
// NOT rendered as a misleading "PUBLISHED" event — it is counted as untargeted
// so the publish phase can disclose the no-op instead of going dark.
func TestPromoteArtifacts_UntargetedDiscloses(t *testing.T) {
	root := t.TempDir()
	layoutDir, digest := writeValidLayout(t, []byte("untargeted-manifest"))
	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind:        "docker",
			Name:        "app",
			Digest:      digest,
			Docker:      &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
			Persistence: artifact.PersistenceHandle{Kind: artifact.PersistenceOCILayout, OCILayout: &artifact.OCILayoutRef{Path: layoutDir}},
			Targets:     nil, // produced, but no publish target matches this ref
		}},
	}
	if err := os.MkdirAll(filepath.Join(root, ".stagefreight"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteOutputsManifest(root, m); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	promoted, untargeted, err := promoteArtifacts(context.Background(), &config.Config{}, root, &buf)
	if err != nil {
		t.Fatalf("promoteArtifacts: %v", err)
	}
	if promoted != 0 {
		t.Fatalf("promoted %d, want 0 (nothing to distribute for an untargeted artifact)", promoted)
	}
	if untargeted != 1 {
		t.Fatalf("untargeted %d, want 1 (the retained image has no target for this ref)", untargeted)
	}
	if strings.Contains(buf.String(), "PUBLISHED") {
		t.Fatalf("untargeted artifact must not render a PUBLISHED event, got:\n%s", buf.String())
	}
}
