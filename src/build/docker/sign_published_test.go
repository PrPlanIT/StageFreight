package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
)

// signCall is one cosign operation a recordingSigner saw.
type signCall struct {
	op, ref string
	opts    sign.SignOptions
}

// recordingSigner records each operation and fails the refs in fail.
func recordingSigner(calls *[]signCall, fail map[string]bool) ImageSigner {
	op := func(name string) func(context.Context, string, map[string]config.ToolConstraint, string, sign.SignPlan, cosign.Env, sign.SignOptions) error {
		return func(_ context.Context, _ string, _ map[string]config.ToolConstraint, ref string, _ sign.SignPlan, _ cosign.Env, opts sign.SignOptions) error {
			*calls = append(*calls, signCall{op: name, ref: ref, opts: opts})
			if fail[ref] {
				return errors.New("registry refused the signature")
			}
			return nil
		}
	}
	return ImageSigner{SignImage: op("sign"), Attest: op("attest")}
}

// keyProfile is a key profile whose key file resolves.
func keyProfile(t *testing.T, attest, enforce bool) *config.ResolvedSigningProfile {
	t.Helper()
	key := filepath.Join(t.TempDir(), "cosign.key")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &config.ResolvedSigningProfile{ID: "ci", Class: "key", KeyRef: key, Attestation: attest, Enforce: enforce}
}

func outcomesOf(t *testing.T, rb *build.ResultsBuilder, id artifact.ArtifactID, typ artifact.OutcomeType) []artifact.Outcome {
	t.Helper()
	intent := artifact.OutputsManifest{Artifacts: []artifact.Artifact{{
		ID: id, Kind: "docker", Name: "app",
		Docker: &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
		Targets: []artifact.Target{{Kind: "registry", Registry: &artifact.RegistryTarget{
			Host: "cr.optcp.com", Path: "org/app", Tags: []string{"v1"}}}},
	}}}
	if err := intent.Finalize(); err != nil {
		t.Fatal(err)
	}
	m, err := rb.Build(&intent)
	if err != nil {
		t.Fatal(err)
	}
	var out []artifact.Outcome
	for _, r := range m.Results {
		if r.ArtifactID != id {
			continue
		}
		for _, o := range r.Outcomes {
			if o.Type == typ {
				out = append(out, o)
			}
		}
	}
	return out
}

func provenanceFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker-abc.json")
	stmt := `{"_type":"https://in-toto.io/Statement/v1","predicateType":"https://slsa.dev/provenance/v1","subject":[{"name":"x"}],"predicate":{"buildDefinition":{}}}`
	if err := os.WriteFile(p, []byte(stmt), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// One signature per endpoint (--recursive for an index, credentials passed), one outcome per tag.
func TestSignPublished_SignsEachEndpointAndRecordsEveryTag(t *testing.T) {
	var calls []signCall
	s := recordingSigner(&calls, nil)
	id := artifact.NewArtifactID("docker", "app")
	rb := build.NewResultsBuilder()
	im := PublishedImage{ArtifactID: id, Host: "cr.optcp.com", Path: "org/app", Tags: []string{"v1.2.3", "latest"},
		Digest: "sha256:aaa", MultiArch: true, Profile: keyProfile(t, false, false), AuthDir: "/creds"}

	res, err := s.SignPublished(context.Background(), &config.Config{}, t.TempDir(), rb, []PublishedImage{im})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].ref != "cr.optcp.com/org/app@sha256:aaa" || !calls[0].opts.MultiArch || calls[0].opts.RegistryAuthDir != "/creds" {
		t.Fatalf("cosign calls = %+v, want one recursive sign of the digest with the registry credentials", calls)
	}
	if len(res) != 1 || !res[0].Signed {
		t.Fatalf("results = %+v", res)
	}
	outs := outcomesOf(t, rb, id, artifact.OutcomeTypeAttestation)
	if len(outs) != 2 || outs[0].Target.Tag != "v1.2.3" || outs[1].Target.Tag != "latest" || outs[0].Attestation.Status != artifact.OutcomeSuccess {
		t.Fatalf("attestation outcomes = %+v, want one success per tag", outs)
	}
}

// A failed signature is fatal under enforce, recorded and continued otherwise.
func TestSignPublished_EnforceIsFatalBestEffortIsNot(t *testing.T) {
	ref := "cr.optcp.com/org/app@sha256:aaa"
	for _, enforce := range []bool{true, false} {
		var calls []signCall
		s := recordingSigner(&calls, map[string]bool{ref: true})
		id := artifact.NewArtifactID("docker", "app")
		rb := build.NewResultsBuilder()
		im := PublishedImage{ArtifactID: id, Host: "cr.optcp.com", Path: "org/app", Tags: []string{"v1"},
			Digest: "sha256:aaa", Profile: keyProfile(t, false, enforce)}
		_, err := s.SignPublished(context.Background(), &config.Config{}, t.TempDir(), rb, []PublishedImage{im})
		if enforce && (err == nil || !strings.Contains(err.Error(), "enforce")) {
			t.Fatalf("enforce: err = %v, want a fatal enforce error", err)
		}
		if !enforce && err != nil {
			t.Fatalf("best-effort: err = %v, want nil", err)
		}
		outs := outcomesOf(t, rb, id, artifact.OutcomeTypeAttestation)
		if len(outs) != 1 || outs[0].Attestation.Status != artifact.OutcomeFailed {
			t.Fatalf("enforce=%v: outcomes = %+v, want the failure recorded", enforce, outs)
		}
	}
}

// Only opted-in profiles attest; missing provenance for one is fatal.
func TestAttestPublished_OptInAndFailLoud(t *testing.T) {
	var calls []signCall
	s := recordingSigner(&calls, nil)
	id := artifact.NewArtifactID("docker", "app")
	optedIn := PublishedImage{ArtifactID: id, Host: "cr.optcp.com", Path: "org/app", Tags: []string{"v1"},
		Digest: "sha256:aaa", MultiArch: true, Profile: keyProfile(t, true, false)}
	optedOut := PublishedImage{ArtifactID: id, Host: "ghcr.io", Path: "org/app", Tags: []string{"v1"},
		Digest: "sha256:aaa", Profile: keyProfile(t, false, false)}

	rb := build.NewResultsBuilder()
	attested, err := s.AttestPublished(context.Background(), &config.Config{}, t.TempDir(), rb,
		[]PublishedImage{optedIn, optedOut}, provenanceFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].op != "attest" || calls[0].ref != optedIn.Ref() || !calls[0].opts.MultiArch ||
		calls[0].opts.PredicateType != "slsaprovenance" || calls[0].opts.PredicatePath == "" {
		t.Fatalf("attest calls = %+v, want only the opted-in endpoint, recursive, with the predicate", calls)
	}
	if len(attested) != 1 {
		t.Fatalf("attested = %v", attested)
	}

	if _, err := s.AttestPublished(context.Background(), &config.Config{}, t.TempDir(), build.NewResultsBuilder(),
		[]PublishedImage{optedIn}, filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("an opted-in attestation with no provenance must be fatal")
	}

	want, err := AttestationRequested(context.Background(), &config.Config{}, t.TempDir(), []PublishedImage{optedOut})
	if err != nil || want {
		t.Fatalf("AttestationRequested(opted out) = %v, %v; want false", want, err)
	}
}

// Perform signs in-build pushes without transport, and nothing under transport.
func TestSignImages_PerformPathByTransport(t *testing.T) {
	old := CosignSigner
	t.Cleanup(func() { CosignSigner = old })
	var calls []signCall
	CosignSigner = recordingSigner(&calls, nil)

	profile := keyProfile(t, false, false)
	plan := &build.BuildPlan{Steps: []build.BuildStep{{
		Name: "app", Output: build.OutputImage, Platforms: []string{"linux/amd64"},
		Registries: []build.RegistryTarget{{URL: "cr.optcp.com", Path: "org/app", SigningProfile: profile}},
	}}}
	pushed := []build.StepResult{{Name: "app", Publications: []build.PushObservation{
		{Host: "cr.optcp.com", Path: "org/app", Tag: "v1", Digest: "sha256:aaa"},
	}}}
	transport := []build.StepResult{{Name: "app"}} // retained, not pushed

	rc := &domains.RunContext{Ctx: context.Background(), Config: &config.Config{}, RootDir: t.TempDir(), RB: build.NewResultsBuilder()}
	if err := signImages(rc, plan, pushed); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].ref != "cr.optcp.com/org/app@sha256:aaa" {
		t.Fatalf("in-build push: calls = %+v, want the pushed digest signed", calls)
	}
	calls = nil
	if err := signImages(rc, plan, transport); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("transport: calls = %+v, want nothing signed in perform", calls)
	}
}
