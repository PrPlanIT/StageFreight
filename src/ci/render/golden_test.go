package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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
				Capabilities: model.CapabilitySpec{Docker: true, ForgeAPI: true},
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
				// Package-registry push: each forge auto-wires the entry it owns
				// (github→ghcr, gitea/forgejo→gitea) with its auto-token; others ignored.
				Capabilities: model.CapabilitySpec{ForgeAPI: true, PackageRegistries: []model.PackageRegistry{
					{Provider: "ghcr", CredPrefix: "GHCR"},
					{Provider: "gitea", CredPrefix: "GITEA"},
				}},
				Policy: model.PolicySpec{AllowFailure: true},
			},
			{
				Name: "narrate", Stage: "narrate", Needs: []string{"perform", "publish"},
				Commands:     []string{"stagefreight ci run narrate"},
				Source:       model.SourceSpec{FullClone: true},
				Capabilities: model.CapabilitySpec{ForgeAPI: true},
				Policy:       model.PolicySpec{AllowFailure: true, WhenAlways: true},
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
// forgesSharingActionsBackendToday lists Actions-family forges that still render
// LINE-IDENTICAL to github (modulo identity + package-cred lines). gitea is NOT here:
// its forge-API token and its package-registry token are BOTH GITEA_TOKEN, so publish's
// env dedups one line — a legitimate per-forge OUTPUT difference (the backend is still
// shared; gitea's Dialect values simply collide). forgejo stays: FORGEJO_TOKEN differs
// from its GITEA package token, so nothing dedups. (The duplicate-key class this guards
// against is now caught directly for every forge by TestGoldensValidYAML.)
var forgesSharingActionsBackendToday = []string{"forgejo"}

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
			// The only differences expected today carry the provider identity, or are
			// forge-specific package-registry credential env vars (GHCR_USER vs
			// GITEA_USER, …) — each forge supplies its own registry + auto-token via
			// its Dialect's PackageAuth, a sanctioned per-forge value (not backend
			// drift, and not an `if provider==` in the backend).
			if strings.Contains(gotLines[i], forge) || strings.Contains(refLines[i], "github") {
				continue
			}
			if isPackageCredLine(refLines[i]) && isPackageCredLine(gotLines[i]) {
				continue
			}
			t.Errorf("%s differs from github on a non-identity line %d — accidental leak, or an "+
				"intentional divergence that should move %s off the shared backend?\n  github: %q\n  %s: %q",
				forge, i, forge, refLines[i], forge, gotLines[i])
		}
	}
}

// isPackageCredLine reports whether a line is a package-registry credential env var
// (<PREFIX>_USER / <PREFIX>_TOKEN). The prefix is forge-specific — each forge's
// Dialect supplies its own — so these lines legitimately differ across forges that
// share the actions backend.
func isPackageCredLine(s string) bool {
	t := strings.TrimSpace(s)
	return strings.Contains(t, "_USER: ") || strings.Contains(t, "_TOKEN: ")
}

// TestGoldensValidYAML guards the renderer's hardest output contract: every golden MUST
// parse as YAML. It catches the duplicate-key class directly — e.g. a forge whose
// forge-API token and package-registry token share an env var (gitea's GITEA_TOKEN)
// emitting the key twice — at unit-test time, instead of letting it surface as a
// downstream YAML-lint failure in CI (which is exactly how it was found).
func TestGoldensValidYAML(t *testing.T) {
	files, err := filepath.Glob("testdata/*.golden.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden files found: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("read %s: %v", f, err)
			continue
		}
		var v map[string]any
		if err := yaml.Unmarshal(data, &v); err != nil {
			t.Errorf("%s is not valid YAML (duplicate key?): %v", f, err)
		}
	}
}

// TestActionsNoInjectedDind asserts the Actions family does NOT inject a dind
// service or DOCKER_HOST — the build engine is deferred to the runner. GitLab keeps
// its own transport anchor.
func TestActionsNoInjectedDind(t *testing.T) {
	for _, forge := range []string{"github", "gitea", "forgejo"} {
		out, err := Emit(forge, canonicalPipeline())
		if err != nil {
			t.Fatalf("%s: %v", forge, err)
		}
		s := string(out)
		if strings.Contains(s, "docker:dind") {
			t.Errorf("%s renders an injected dind service — should defer to the runner", forge)
		}
		if strings.Contains(s, "DOCKER_HOST") {
			t.Errorf("%s injects DOCKER_HOST — should defer to the runner's auto-detected engine", forge)
		}
	}
	// GitLab still provisions its transport (its runner exposes a resolvable dind).
	gl, err := Emit("gitlab", canonicalPipeline())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gl), "DOCKER_HOST") {
		t.Error("gitlab should keep its transport anchor")
	}
}
