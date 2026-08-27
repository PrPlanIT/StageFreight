package docker

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// With several Dockerfiles present and none declared, StageFreight must NOT guess.
// The lexical first-pick silently built weave-gitops' dev stub (ADD bin build,
// expecting a binary the pipeline never produces) instead of its server image.
func TestPlanDockerBuild_AmbiguousDockerfileIsAnError(t *testing.T) {
	det := &build.Detection{Dockerfiles: []build.DockerfileInfo{
		{Path: "dev.dockerfile"},
		{Path: "gitops-server.dockerfile"},
		{Path: "gitops.dockerfile"},
	}}
	_, err := planDockerBuild(config.BuildConfig{ID: "weave-gitops", Kind: "docker"}, &config.Config{}, det, nil, "", "")
	if err == nil {
		t.Fatal("multiple Dockerfiles with none declared must be an error, not a silent pick")
	}
	msg := err.Error()
	for _, want := range []string{"dockerfile:", "dev.dockerfile", "gitops-server.dockerfile"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must name the remedy and the candidates; missing %q in: %v", want, err)
		}
	}
}

// One candidate is unambiguous — adopting it is the whole point of detection.
func TestPlanDockerBuild_SingleDockerfileIsAdopted(t *testing.T) {
	det := &build.Detection{Dockerfiles: []build.DockerfileInfo{{Path: "Dockerfile"}}}
	step, err := planDockerBuild(config.BuildConfig{ID: "app", Kind: "docker"}, &config.Config{}, det, nil, "", "")
	if err != nil {
		t.Fatalf("a single Dockerfile must be adopted, got %v", err)
	}
	if step.Dockerfile != "Dockerfile" {
		t.Errorf("Dockerfile = %q, want the detected one", step.Dockerfile)
	}
}

// An explicit declaration always wins, ambiguity or not.
func TestPlanDockerBuild_DeclaredDockerfileWins(t *testing.T) {
	det := &build.Detection{Dockerfiles: []build.DockerfileInfo{
		{Path: "dev.dockerfile"}, {Path: "gitops-server.dockerfile"},
	}}
	step, err := planDockerBuild(
		config.BuildConfig{ID: "weave-gitops", Kind: "docker", Dockerfile: "gitops-server.dockerfile"},
		&config.Config{}, det, nil, "", "")
	if err != nil {
		t.Fatalf("declared dockerfile must resolve, got %v", err)
	}
	if step.Dockerfile != "gitops-server.dockerfile" {
		t.Errorf("Dockerfile = %q, want the declared one", step.Dockerfile)
	}
}
