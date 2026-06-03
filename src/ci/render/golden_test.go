package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

var update = flag.Bool("update", false, "update golden render files")

// canonicalPipeline exercises every feature an emitter must lower: multi-stage
// ordering with implicit and explicit needs, OIDC, docker, artifacts with
// varying expiry, routing labels, allow-failure, when-always, and full clone.
// Golden output is the contract; the backend may be refactored as long as these
// bytes hold.
func canonicalPipeline() model.Pipeline {
	return model.Pipeline{
		Defaults: model.PipelineDefaults{
			Image:            "registry.example.com/acme/stagefreight:latest-dev",
			Interruptible:    true,
			CancelSuperseded: true,
			CIContext:        true,
		},
		Jobs: []model.Job{
			{
				Name: "audition", Stage: "audition",
				Commands:     []string{"stagefreight ci run audition"},
				Source:       model.SourceSpec{FullClone: true},
				Artifacts:    model.ArtifactSpec{Paths: []string{".stagefreight/"}, ExpireIn: "1 week"},
				Capabilities: model.CapabilitySpec{Docker: true},
				Policy:       model.PolicySpec{AllowFailure: true},
			},
			{
				Name: "perform", Stage: "perform",
				Commands:     []string{"stagefreight ci run perform"},
				Source:       model.SourceSpec{FullClone: true},
				Artifacts:    model.ArtifactSpec{Paths: []string{".stagefreight/"}, ExpireIn: "1 day"},
				Routing:      model.RoutingSpec{Labels: []string{"self-hosted", "docker"}},
				Capabilities: model.CapabilitySpec{Docker: true, OIDC: true},
			},
			{
				Name: "review", Stage: "review", Needs: []string{"perform"},
				Commands:     []string{"stagefreight ci run review"},
				Artifacts:    model.ArtifactSpec{Paths: []string{".stagefreight/security/"}, ExpireIn: "1 week"},
				Capabilities: model.CapabilitySpec{Docker: true},
				Policy:       model.PolicySpec{AllowFailure: true},
			},
			{
				Name: "publish", Stage: "publish", Needs: []string{"perform", "review"},
				Commands: []string{"stagefreight ci run publish"},
				Source:   model.SourceSpec{FullClone: true},
				Policy:   model.PolicySpec{AllowFailure: true},
			},
			{
				Name: "narrate", Stage: "narrate", Needs: []string{"perform", "publish"},
				Commands: []string{"stagefreight ci run narrate"},
				Source:   model.SourceSpec{FullClone: true},
				Policy:   model.PolicySpec{AllowFailure: true, WhenAlways: true},
			},
		},
	}
}

// TestGoldenRender pins byte-exact output per forge. Run `go test -update` to
// regenerate the golden files after an intended rendering change.
func TestGoldenRender(t *testing.T) {
	for _, forge := range SupportedForges {
		t.Run(forge, func(t *testing.T) {
			got, err := Emit(forge, canonicalPipeline())
			if err != nil {
				t.Fatalf("Emit(%s): %v", forge, err)
			}
			golden := filepath.Join("testdata", forge+".golden.yml")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("reading golden (run `go test -update`): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("render for %s does not match golden (run `go test -update`)\n--- got ---\n%s", forge, got)
			}
		})
	}
}

// TestRenderDeterministic asserts rendering is a pure function — identical input
// yields identical bytes. ci render --check (exact match, self-validated in
// audition) depends on this.
func TestRenderDeterministic(t *testing.T) {
	for _, forge := range SupportedForges {
		a, err := Emit(forge, canonicalPipeline())
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Emit(forge, canonicalPipeline())
		if !bytes.Equal(a, b) {
			t.Errorf("%s render is not deterministic", forge)
		}
	}
}

// forgesSharingActionsBackendToday lists the forges that CURRENTLY render through
// the same actions serialization backend, measured against github as reference.
// This is a snapshot of the implementation, not a law: ci-render.md reserves each
// forge's right to diverge (GitHub reusable workflows, Forgejo OIDC/federation,
// Gitea runner behavior). When a forge intentionally diverges, remove it from this
// list — it then renders on its own terms and is no longer expected to match.
var forgesSharingActionsBackendToday = []string{"gitea", "forgejo"}

// TestActionsForgesShareBackendToday is a current-implementation guard, NOT an
// architectural invariant. It asserts that the forges still sharing the actions
// backend differ from github ONLY on provider-identity lines (header banner,
// regenerate command, SF_CI_PROVIDER). Its purpose is to catch an ACCIDENTAL
// leak — identity bleeding into the shared backend, or shared rendering drifting
// per-forge by mistake.
//
// A failure here is a prompt, not a verdict: if a forge is INTENTIONALLY
// diverging, move it off the shared backend into its own rendering and drop it
// from forgesSharingActionsBackendToday. Do not suppress the difference inside
// the backend with an `if provider == ...` branch — that is exactly the leak the
// boundary (and this test) exists to prevent.
func TestActionsForgesShareBackendToday(t *testing.T) {
	ref, err := Emit("github", canonicalPipeline())
	if err != nil {
		t.Fatal(err)
	}
	refLines := strings.Split(string(ref), "\n")

	for _, forge := range forgesSharingActionsBackendToday {
		got, err := Emit(forge, canonicalPipeline())
		if err != nil {
			t.Fatal(err)
		}
		gotLines := strings.Split(string(got), "\n")
		if len(gotLines) != len(refLines) {
			t.Fatalf("%s no longer matches github's line count. If this divergence is intentional, "+
				"move %s off the shared backend and remove it from forgesSharingActionsBackendToday", forge, forge)
		}
		for i := range refLines {
			if refLines[i] == gotLines[i] {
				continue
			}
			// The only differences expected today carry the provider identity.
			if strings.Contains(gotLines[i], forge) || strings.Contains(refLines[i], "github") {
				continue
			}
			t.Errorf("%s differs from github on a non-identity line %d — accidental leak, or an "+
				"intentional divergence that should move %s off the shared backend?\n  github: %q\n  %s: %q",
				forge, i, forge, refLines[i], forge, gotLines[i])
		}
	}
}
