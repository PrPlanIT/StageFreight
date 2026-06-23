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
				// Package-registry push: each forge auto-wires the entry it owns
				// (github→ghcr, gitea/forgejo→gitea) with its auto-token; others ignored.
				Capabilities: model.CapabilitySpec{PackageRegistries: []model.PackageRegistry{
					{Provider: "ghcr", CredPrefix: "GHCR"},
					{Provider: "gitea", CredPrefix: "GITEA"},
				}},
				Policy: model.PolicySpec{AllowFailure: true},
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
	// Pin a uniform dind transport so the forge-specific dind DEFAULT (github
	// non-TLS vs gitea/forgejo TLS — a sanctioned Dialect divergence) doesn't read
	// as backend drift. This isolates the test to its purpose: catching UNsanctioned
	// per-forge differences beyond identity + package-registry credentials.
	tlsOn := true
	pin := canonicalPipeline()
	pin.Defaults.DindTLS = &tlsOn

	ref, err := Emit("github", pin)
	if err != nil {
		t.Fatal(err)
	}
	refLines := strings.Split(string(ref), "\n")

	for _, forge := range forgesSharingActionsBackendToday {
		got, err := Emit(forge, pin)
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

// TestDindTLS pins the dind transport matrix: per-forge defaults (github non-TLS
// since hosted runners can't share the cert volume; gitlab/gitea/forgejo TLS since
// their runners do) and the ci.docker.tls override winning over the default both
// directions.
func TestDindTLS(t *testing.T) {
	on, off := true, false
	cases := []struct {
		forge   string
		tls     *bool
		wantTLS bool
	}{
		{"github", nil, false},  // hosted default → non-TLS
		{"github", &on, true},   // self-hosted with certs → override TLS
		{"gitea", nil, true},    // runner shares /certs → TLS
		{"gitea", &off, false},  // operator opts out → non-TLS
		{"forgejo", nil, true},  // runner shares /certs → TLS
		{"gitlab", nil, true},   // built-in /certs → TLS
		{"gitlab", &off, false}, // operator opts out → non-TLS
	}
	for _, c := range cases {
		p := canonicalPipeline()
		p.Defaults.DindTLS = c.tls
		out, err := Emit(c.forge, p)
		if err != nil {
			t.Fatalf("%s tls=%v: %v", c.forge, c.tls, err)
		}
		s := string(out)
		tls := strings.Contains(s, "dind:2376")
		nonTLS := strings.Contains(s, "dind:2375")
		if c.wantTLS && (!tls || nonTLS) {
			t.Errorf("%s tls=%v: want TLS (2376); got 2376=%v 2375=%v", c.forge, c.tls, tls, nonTLS)
		}
		if !c.wantTLS && (!nonTLS || tls) {
			t.Errorf("%s tls=%v: want non-TLS (2375); got 2376=%v 2375=%v", c.forge, c.tls, tls, nonTLS)
		}
	}
}
