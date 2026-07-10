package config

import (
	"strings"
	"testing"
)

func TestNarrateConfig_IsZero(t *testing.T) {
	if !(NarrateConfig{}).IsZero() {
		t.Error("empty NarrateConfig should be zero")
	}
	if (NarrateConfig{Badges: []BadgeConfig{{ID: "x"}}}).IsZero() {
		t.Error("NarrateConfig with badges is not zero")
	}
	if (NarrateConfig{Commit: NarrateCommitConfig{Message: "m"}}).IsZero() {
		t.Error("NarrateConfig with a commit message is not zero")
	}
}

// TestValidate_NarrateCommitBuilds covers the build-binding safeguard: a narrate commit
// may only land a kind: command build's tree, and only to a repo-relative destination.
func TestValidate_NarrateCommitBuilds(t *testing.T) {
	cmdBuild := BuildConfig{ID: "ref", Kind: "command", Command: "x", Outputs: []OutputSpec{{Type: "tree", Source: "docs/generated"}}}
	dockerBuild := BuildConfig{ID: "img", Kind: "docker"}

	errStr := func(builds []BuildConfig, bindings []NarrateBuildBinding) string {
		cfg := &Config{Version: 1, Builds: builds, Narrate: NarrateConfig{Commit: NarrateCommitConfig{Builds: bindings}}}
		_, err := Validate(cfg)
		if err == nil {
			return ""
		}
		return err.Error()
	}

	// Valid: binds a command build to a repo-relative destination.
	if e := errStr([]BuildConfig{cmdBuild}, []NarrateBuildBinding{{Build: "ref", Destination: "docs/reference"}}); strings.Contains(e, "narrate.commit.builds") {
		t.Errorf("valid binding produced an error: %s", e)
	}

	check := func(name string, builds []BuildConfig, b NarrateBuildBinding, want string) {
		t.Run(name, func(t *testing.T) {
			if e := errStr(builds, []NarrateBuildBinding{b}); !strings.Contains(e, want) {
				t.Errorf("want %q, got: %s", want, e)
			}
		})
	}
	check("missing build", []BuildConfig{cmdBuild}, NarrateBuildBinding{Destination: "docs/reference"}, "build is required")
	check("unknown build", []BuildConfig{cmdBuild}, NarrateBuildBinding{Build: "nope", Destination: "docs/reference"}, "unknown build")
	check("non-command build", []BuildConfig{dockerBuild}, NarrateBuildBinding{Build: "img", Destination: "docs/reference"}, "not a kind: command build")
	check("missing destination", []BuildConfig{cmdBuild}, NarrateBuildBinding{Build: "ref"}, "destination is required")
	check("absolute destination", []BuildConfig{cmdBuild}, NarrateBuildBinding{Build: "ref", Destination: "/etc/x"}, "narrate.commit.builds[0].destination")
}
