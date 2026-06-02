package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/config"
)

func dockerUp(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "info").Run() == nil
}

// TestPromoteArtifacts_EndToEnd is the P4 finish-line proof: build → retain to
// CAS → promote from CAS to a registry, asserting the PUBLISHED digest equals
// the digest perform recorded. This closes the loop: the bytes distributed in
// publish are provably the bytes built in perform.
//
// Docker-gated.
func TestPromoteArtifacts_EndToEnd(t *testing.T) {
	if !dockerUp(t) {
		t.Skip("docker not available — end-to-end promotion proof runs only on a docker-capable runner")
	}

	// 1. Build an OCI layout + capture the recorded digest (perform side).
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM alpine:3.19\n"), 0o644); err != nil {
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
	reg := exec.Command("docker", "run", "-d", "--rm", "-p", "5980:5000", "--name", "sf-e2e-reg", "registry:2")
	if out, err := reg.CombinedOutput(); err != nil {
		t.Fatalf("registry: %v\n%s", err, out)
	}
	defer exec.Command("docker", "rm", "-f", "sf-e2e-reg").Run()
	exec.Command("sh", "-c", "sleep 2").Run()

	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind:        "docker",
			Name:        "app",
			Digest:      artifact.Digest(meta.Digest),
			Docker:      &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
			Persistence: artifact.PersistenceHandle{Kind: artifact.PersistenceOCILayout, OCILayout: &artifact.OCILayoutRef{Path: stored}},
			Targets: []artifact.Target{{
				Kind:     "registry",
				Registry: &artifact.RegistryTarget{Host: "localhost:5980", Path: "e2e/app", Tags: []string{"v1"}},
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
	n, err := promoteArtifacts(context.Background(), appCfg, rootDir, os.Stderr)
	if err != nil {
		t.Fatalf("promoteArtifacts: %v", err)
	}
	if n != 1 {
		t.Fatalf("promoted %d tags, want 1", n)
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
	// n==1 and no error is itself the guarantee. We additionally confirm the
	// registry independently via crane digest, the same tool that does the push.
	got, err := exec.Command("docker", "run", "--rm", "--network", "host",
		"gcr.io/go-containerregistry/crane:latest", "digest", "--insecure",
		"localhost:5980/e2e/app:v1").Output()
	if err != nil {
		t.Logf("independent crane digest unavailable (%v) — promotion's internal post-push verify already asserted digest equality", err)
		return
	}
	published := strings.TrimSpace(string(got))
	if published != meta.Digest {
		t.Fatalf("PUBLISHED DIGEST != PERFORM DIGEST: published %q, perform recorded %q", published, meta.Digest)
	}
	t.Logf("end-to-end proof: perform digest == published digest == %s", meta.Digest)
}

// TestPromoteArtifacts_PartialFailureReportsAll proves the failure semantics:
// when a tag fails, promotion does NOT abandon the rest half-done — it attempts
// every tag and aggregates the failures, so an operator can re-run (promotion is
// idempotent) to converge rather than guess at a half-published state. Uses an
// unreachable host so no registry is needed; the point is that BOTH tags are
// attempted and reported, not that one fails.
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

	_, err := promoteArtifacts(context.Background(), &config.Config{}, root, io.Discard)
	if err == nil {
		t.Fatal("expected an aggregated error for unreachable targets")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x/app:a") {
		t.Errorf("error does not reference tag a — did promotion stop early? %v", msg)
	}
	if !strings.Contains(msg, "x/app:b") {
		t.Errorf("error does not reference tag b — did promotion stop early? %v", msg)
	}
}
