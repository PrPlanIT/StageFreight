package docker

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// A secret is mounted for a single RUN and leaves nothing in the image, unlike a build
// arg which is readable from the image's history — so a Dockerfile that wants one
// cannot be driven with the other.
func TestBuildArgsEmitsSecrets(t *testing.T) {
	bx := &Buildx{}
	args := bx.buildArgs(build.BuildStep{
		Secrets: map[string]string{"apps_json": "apps.json", "token": "secrets/tok"},
	})
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--secret id=apps_json,src=apps.json",
		"--secret id=token,src=secrets/tok",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n  %s", want, joined)
		}
	}

	// Deterministic order: a map's iteration is not, and an argument list that
	// shuffles between runs is a diff nobody can read.
	if strings.Index(joined, "id=apps_json") > strings.Index(joined, "id=token") {
		t.Error("secrets are not emitted in a stable order")
	}
}

func TestBuildArgsOmitsSecretFlagWhenNoneDeclared(t *testing.T) {
	bx := &Buildx{}
	if strings.Contains(strings.Join(bx.buildArgs(build.BuildStep{}), " "), "--secret") {
		t.Error("a build with no secrets must not pass --secret")
	}
}
