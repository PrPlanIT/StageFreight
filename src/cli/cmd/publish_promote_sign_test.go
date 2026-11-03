package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	builddocker "github.com/PrPlanIT/StageFreight/src/build/docker"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
)

type promoteSignCall struct {
	op, ref string
	opts    sign.SignOptions
	auth    string // the auths key in the DOCKER_CONFIG handed to cosign, read at call time
}

// stubPublishSigner records each op and its credentials' registry; refs in fail are refused.
func stubPublishSigner(t *testing.T, fail map[string]bool) *[]promoteSignCall {
	t.Helper()
	old := publishSigner
	t.Cleanup(func() { publishSigner = old })
	var calls []promoteSignCall
	op := func(name string) func(context.Context, string, map[string]config.ToolConstraint, string, sign.SignPlan, cosign.Env, sign.SignOptions) error {
		return func(_ context.Context, _ string, _ map[string]config.ToolConstraint, ref string, _ sign.SignPlan, _ cosign.Env, opts sign.SignOptions) error {
			c := promoteSignCall{op: name, ref: ref, opts: opts}
			if opts.RegistryAuthDir != "" {
				var cfg struct {
					Auths map[string]json.RawMessage `json:"auths"`
				}
				data, _ := os.ReadFile(filepath.Join(opts.RegistryAuthDir, "config.json"))
				_ = json.Unmarshal(data, &cfg)
				for k := range cfg.Auths {
					c.auth = k
				}
			}
			calls = append(calls, c)
			if fail[ref] {
				return errors.New("registry refused the signature")
			}
			return nil
		}
	}
	publishSigner = builddocker.ImageSigner{SignImage: op("sign"), Attest: op("attest")}
	return &calls
}

// promoteFixture retains a multi-platform image targeting an in-memory registry (and
// optionally an unreachable one); returns rootDir, the host and the digest.
func promoteFixture(t *testing.T, unreachableTarget bool, profileID string) (string, string, string) {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	host := u.Host

	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	sub := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img})
	rootDir := t.TempDir()
	layoutDir := filepath.Join(rootDir, ".stagefreight", "store", "layout")
	if _, err := layout.Write(layoutDir, mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: sub})); err != nil {
		t.Fatal(err)
	}
	dig, _ := sub.Digest()

	targets := []artifact.Target{{Kind: "registry", Registry: &artifact.RegistryTarget{
		Host: host, Path: "org/app", Tags: []string{"v1.2.3", "latest"}, SigningProfile: profileID}}}
	if unreachableTarget {
		targets = append(targets, artifact.Target{Kind: "registry", Registry: &artifact.RegistryTarget{
			Host: "127.0.0.1:1", Path: "org/app", Tags: []string{"v1.2.3"}, SigningProfile: profileID}})
	}
	out := artifact.OutputsManifest{Artifacts: []artifact.Artifact{{
		ID: artifact.NewArtifactID("docker", "app"), Kind: "docker", Name: "app", Digest: artifact.Digest(dig.String()),
		Docker:      &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64", "linux/arm64"}},
		Persistence: artifact.PersistenceHandle{Kind: artifact.PersistenceOCILayout, OCILayout: &artifact.OCILayoutRef{Path: layoutDir}},
		Targets:     targets,
	}}}
	if err := out.Finalize(); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteOutputsManifest(rootDir, out); err != nil {
		t.Fatal(err)
	}
	return rootDir, host, dig.String()
}

// signingConfig declares key profile "ci" and host's registry credentials.
func signingConfig(t *testing.T, host string, attest, enforce bool) *config.Config {
	t.Helper()
	key := filepath.Join(t.TempDir(), "cosign.key")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SFTEST_REG_USER", "robot")
	t.Setenv("SFTEST_REG_PASS", "s3cret")
	cfg := &config.Config{}
	cfg.Registries = []config.RegistryConfig{{URL: host, Credentials: "SFTEST_REG"}}
	cfg.SigningSetup.Profiles = config.OrderedSigningProfiles{{ID: "ci", Requires: config.StringOrList{"key"}, Key: &config.KeyTrust{Ref: key}, Attestation: attest, Enforce: enforce}}
	return cfg
}

// Publish signs each promoted endpoint once (recursive, promotion's credentials), never a failed one.
func TestPromoteArtifacts_SignsWhatItPromoted(t *testing.T) {
	calls := stubPublishSigner(t, nil)
	rootDir, host, digest := promoteFixture(t, true, "ci")
	cfg := signingConfig(t, host, false, false)

	_, _, err := promoteArtifacts(context.Background(), cfg, rootDir, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "failed to publish") {
		t.Fatalf("err = %v, want the unreachable target reported", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("cosign calls = %+v, want exactly one (the reachable endpoint)", *calls)
	}
	c := (*calls)[0]
	if c.op != "sign" || c.ref != host+"/org/app@"+digest {
		t.Fatalf("signed %q (%s), want %s/org/app@%s", c.ref, c.op, host, digest)
	}
	if !c.opts.MultiArch {
		t.Error("an index must be signed --recursive")
	}
	if c.auth != host {
		t.Errorf("cosign got credentials for %q, want %q", c.auth, host)
	}

	results, rErr := artifact.ReadResultsManifest(rootDir)
	if rErr != nil {
		t.Fatal(rErr)
	}
	var signedTags []string
	for _, r := range results.Results {
		for _, o := range r.Outcomes {
			if o.Type == artifact.OutcomeTypeAttestation && o.Attestation.Status == artifact.OutcomeSuccess {
				signedTags = append(signedTags, o.Target.Host+":"+o.Target.Tag)
			}
		}
	}
	if strings.Join(signedTags, ",") != host+":v1.2.3,"+host+":latest" {
		t.Fatalf("signature outcomes = %v, want one per promoted tag on the reachable endpoint only", signedTags)
	}
}

// An enforced signing failure fails publish; attestation follows the opt-in and needs provenance.
func TestPromoteArtifacts_EnforceAndAttestation(t *testing.T) {
	t.Run("enforce", func(t *testing.T) {
		rootDir, host, digest := promoteFixture(t, false, "ci")
		stubPublishSigner(t, map[string]bool{host + "/org/app@" + digest: true})
		_, _, err := promoteArtifacts(context.Background(), signingConfig(t, host, false, true), rootDir, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "enforce") {
			t.Fatalf("err = %v, want the enforced signing failure", err)
		}
	})
	t.Run("attestation opted in, provenance present", func(t *testing.T) {
		rootDir, host, _ := promoteFixture(t, false, "ci")
		calls := stubPublishSigner(t, nil)
		provDir := filepath.Join(rootDir, ".stagefreight", "provenance")
		if err := os.MkdirAll(provDir, 0o755); err != nil {
			t.Fatal(err)
		}
		stmt := `{"_type":"https://in-toto.io/Statement/v1","predicateType":"https://slsa.dev/provenance/v1","subject":[{"name":"x"}],"predicate":{"buildDefinition":{}}}`
		if err := os.WriteFile(filepath.Join(provDir, "docker-abc.json"), []byte(stmt), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := promoteArtifacts(context.Background(), signingConfig(t, host, true, false), rootDir, io.Discard); err != nil {
			t.Fatal(err)
		}
		var ops []string
		for _, c := range *calls {
			ops = append(ops, c.op)
		}
		if strings.Join(ops, ",") != "sign,attest" {
			t.Fatalf("ops = %v, want sign then attest", ops)
		}
	})
	t.Run("attestation opted in, provenance missing is fatal", func(t *testing.T) {
		rootDir, host, _ := promoteFixture(t, false, "ci")
		stubPublishSigner(t, nil)
		_, _, err := promoteArtifacts(context.Background(), signingConfig(t, host, true, false), rootDir, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "provenance") {
			t.Fatalf("err = %v, want the missing provenance to fail publish", err)
		}
	})
	t.Run("attestation not opted in", func(t *testing.T) {
		rootDir, host, _ := promoteFixture(t, false, "ci")
		calls := stubPublishSigner(t, nil)
		if _, _, err := promoteArtifacts(context.Background(), signingConfig(t, host, false, false), rootDir, io.Discard); err != nil {
			t.Fatal(err)
		}
		for _, c := range *calls {
			if c.op == "attest" {
				t.Fatalf("attested without the profile opting in: %+v", *calls)
			}
		}
	})
}

// The registry credentials cosign gets are base64 user:secret under the host's key.
func TestRegistryAuthDirs_ForConfiguredHostOnly(t *testing.T) {
	cfg := signingConfig(t, "cr.optcp.com", false, false)
	r := newRegistryAuthDirs(cfg)
	defer r.cleanup()
	dir, err := r.dirFor("cr.optcp.com")
	if err != nil || dir == "" {
		t.Fatalf("dirFor(configured) = %q, %v", dir, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte("robot:s3cret"))) {
		t.Fatalf("config.json = %s", data)
	}
	if d, err := r.dirFor("ghcr.io"); err != nil || d != "" {
		t.Fatalf("dirFor(unconfigured) = %q, %v; want the ambient config", d, err)
	}
}
