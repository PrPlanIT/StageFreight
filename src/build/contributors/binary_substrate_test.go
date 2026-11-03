package contributors

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/config"
)

const ciImg = "docker.cr.optcp.com/prplanit/stagefreight:latest-dev"

// Outside CI there is no job image; in CI, CI_JOB_IMAGE wins over ci.image.
func TestJobImage(t *testing.T) {
	cfg := &config.Config{CI: config.CIConfig{Image: ciImg}}
	cases := []struct {
		name, provider, jobImage, want string
	}{
		{"outside CI", "", "", ""},
		{"outside CI ignores a stray CI_JOB_IMAGE", "", "other:1", ""},
		{"CI without a forge report", "github", "", ciImg},
		{"GitLab reports the job image", "gitlab", "other:1", "other:1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SF_CI_PROVIDER", c.provider)
			t.Setenv("CI_JOB_IMAGE", c.jobImage)
			if got := jobImage(cfg); got != c.want {
				t.Errorf("jobImage = %q, want %q", got, c.want)
			}
		})
	}
}

// Only builds that run as a BuildKit build (not in the job, not native) need a builder.
func TestNeedsBuildKit(t *testing.T) {
	cases := []struct {
		name string
		bld  config.BuildConfig
		job  string
		want bool
	}{
		{"go compiles natively", config.BuildConfig{Kind: "binary", Builder: "go"}, ciImg, false},
		{"rust compiles natively", config.BuildConfig{Kind: "binary", Builder: "rust"}, "", false},
		{"image build is not this contributor's", config.BuildConfig{Kind: "docker"}, "", false},
		{"command on ci.image runs in the job", config.BuildConfig{Kind: "command"}, ciImg, false},
		{"command on ci.image outside CI", config.BuildConfig{Kind: "command"}, "", true},
		{"command on another image", config.BuildConfig{Kind: "command", Image: "python:3.12-slim"}, ciImg, true},
		{"node infers a toolchain image", config.BuildConfig{Kind: "binary", Builder: "node"}, ciImg, true},
		{"builder pinned to the job image", config.BuildConfig{Kind: "binary", Builder: "c", Image: ciImg}, ciImg, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsBuildKit(c.bld, ciImg, c.job); got != c.want {
				t.Errorf("needsBuildKit = %v, want %v", got, c.want)
			}
		})
	}
}

// A builder is required exactly when a selected build runs on BuildKit, on either runner kind; a daemon never is.
func TestBinaryContributor_SubstrateNeeds_BothRunnerKinds(t *testing.T) {
	builds := []config.BuildConfig{
		{ID: "reference", Kind: "command"},
		{ID: "bin", Kind: "binary", Builder: "go"},
		{ID: "docs-site", Kind: "command", Image: "docker.io/library/python:3.12-slim"},
	}
	cases := []struct {
		name, buildID, provider string
		wantBuild               bool
	}{
		{"CI, whole run", "", "gitlab", true},
		{"CI, only the in-job build", "reference", "gitlab", false},
		{"CI, only the go build", "bin", "gitlab", false},
		{"local, in-job build has no job to run in", "reference", "", true},
	}
	kinds := []map[string]string{
		{"DOCKER_HOST": "unix:///var/run/docker.sock", "BUILDKIT_HOST": ""},
		{"DOCKER_HOST": "", "BUILDKIT_HOST": "tcp://buildkitd:1234"},
	}
	for _, kind := range kinds {
		for _, c := range cases {
			t.Run(kind["BUILDKIT_HOST"]+"/"+c.name, func(t *testing.T) {
				for k, v := range kind {
					t.Setenv(k, v)
				}
				t.Setenv("SF_CI_PROVIDER", c.provider)
				t.Setenv("CI_JOB_IMAGE", "")
				b := &binaryContributor{}
				rc := &domains.RunContext{BuildID: c.buildID, Config: &config.Config{
					CI: config.CIConfig{Image: ciImg}, Builds: builds,
				}}
				if !b.Applies(rc) {
					t.Fatal("binary contributor should apply")
				}
				if b.NeedsBuild() != c.wantBuild {
					t.Errorf("NeedsBuild = %v, want %v", b.NeedsBuild(), c.wantBuild)
				}
				if b.NeedsDocker() {
					t.Error("NeedsDocker = true; no binary build runs a Docker daemon")
				}
			})
		}
	}
}
