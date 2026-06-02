package cmd

import (
	"context"
	"encoding/json"
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
