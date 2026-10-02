package dependency

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// outdatedDockerDep is an eligible docker-image candidate: a golang base pinned
// behind an available newer tag. Used to prove the apply path runs (control) and
// that remediate:false suppresses it (subject).
func outdatedDockerDep() supplychain.Dependency {
	return supplychain.Dependency{
		Name:              "golang:1.24.13",
		Current:           "1.24.13",
		Latest:            "1.25.7",
		Ecosystem:         supplychain.EcosystemDockerImage,
		File:              "Dockerfile",
		Line:              1,
		AvailableVersions: []string{"1.24.13", "1.25.7"},
	}
}

func dockerRepoFiles() map[string]string {
	return map[string]string{
		"go.mod":     "module example.com/x\n\ngo 1.24\n",
		"Dockerfile": "FROM golang:1.24.13 AS build\n",
	}
}

// TestUpdate_RemediateFalseEvaluatesWithoutApplying locks the remediate:false
// (DryRun) contract at the Update() orchestration layer: an eligible ecosystem
// candidate is EVALUATED but no writer runs — the working tree is not mutated and
// nothing is recorded as applied. The control case proves the candidate is genuinely
// eligible, so the subject's no-mutation is the gate working, not an inert dep.
//
// Regression guard for the bug where DryRun gated only repository reconciliation
// (step 5c), leaving every ecosystem apply (go get, Dockerfile rewrite, …) to run
// despite the documented "only evaluates them without changing anything".
func TestUpdate_RemediateFalseEvaluatesWithoutApplying(t *testing.T) {
	ctx := context.Background()

	// Control: remediate on (DryRun:false) MUST apply the bump — establishes eligibility.
	ctrl := initTestRepo(t, dockerRepoFiles())
	ctrlRes, err := Update(ctx, UpdateConfig{
		RootDir:    ctrl,
		DryRun:     false,
		Ecosystems: []string{supplychain.EcosystemDockerImage},
	}, []supplychain.Dependency{outdatedDockerDep()})
	if err != nil {
		t.Fatalf("control update: %v", err)
	}
	ctrlDockerfile, _ := os.ReadFile(filepath.Join(ctrl, "Dockerfile"))
	if !strings.Contains(string(ctrlDockerfile), "1.25.7") {
		t.Fatalf("control: remediate should have bumped the Dockerfile, got:\n%s", ctrlDockerfile)
	}
	if len(ctrlRes.Applied) == 0 {
		t.Fatalf("control: remediate recorded no applied changes — dep was not an eligible candidate")
	}

	// Subject: remediate off (DryRun:true) MUST NOT mutate the tree or record an apply.
	subj := initTestRepo(t, dockerRepoFiles())
	subjRes, err := Update(ctx, UpdateConfig{
		RootDir:    subj,
		DryRun:     true,
		Ecosystems: []string{supplychain.EcosystemDockerImage},
	}, []supplychain.Dependency{outdatedDockerDep()})
	if err != nil {
		t.Fatalf("subject update: %v", err)
	}
	subjDockerfile, _ := os.ReadFile(filepath.Join(subj, "Dockerfile"))
	if strings.Contains(string(subjDockerfile), "1.25.7") {
		t.Fatalf("remediate:false must not rewrite the Dockerfile, got:\n%s", subjDockerfile)
	}
	if len(subjRes.Applied) != 0 {
		t.Fatalf("remediate:false must not record applied changes, got %d", len(subjRes.Applied))
	}
	if len(subjRes.FilesChanged) != 0 {
		t.Fatalf("remediate:false must not report changed files, got %v", subjRes.FilesChanged)
	}
}
