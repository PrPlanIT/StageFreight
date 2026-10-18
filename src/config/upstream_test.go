package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func decodeUpstream(t *testing.T, src string) (UpstreamConfig, error) {
	t.Helper()
	var holder struct {
		Upstream UpstreamConfig `yaml:"upstream"`
	}
	err := yaml.Unmarshal([]byte(src), &holder)
	return holder.Upstream, err
}

func TestUpstream_DecodesKeyedSources(t *testing.T) {
	u, err := decodeUpstream(t, `
upstream:
  sources:
    core:
      git: https://github.com/ansible/ansible
      select: { exclude: ['^v2-preview'] }
    base:
      image: docker.io/library/python
  version: { track: core, since: v2.21.5 }
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Sources) != 2 || u.Sources[0].ID != "core" || u.Sources[1].ID != "base" {
		t.Fatalf("sources = %+v", u.Sources)
	}
	if kind, loc := u.Sources[1].Kind(); kind != UpstreamKindImage || loc != "docker.io/library/python" {
		t.Fatalf("kind = %s %s", kind, loc)
	}
	if mode, src := u.Version.Mode(); mode != UpstreamVersionTrack || src != "core" {
		t.Fatalf("mode = %s %s", mode, src)
	}
	if errs := validateUpstream(u); len(errs) != 0 {
		t.Fatalf("valid config rejected: %v", errs)
	}
	port := UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Image: "registry.local:5000/team/app"}}}
	if errs := validateUpstream(port); len(errs) != 0 {
		t.Fatalf("a registry port is not a tag: %v", errs)
	}
}

func TestUpstream_RetiredShapeErrorsWithTheNewOne(t *testing.T) {
	for src, want := range map[string]string{
		"upstream:\n  git:\n    - repo: https://x\n": "upstream.sources.<id>",
		"upstream:\n  version: track\n":              "version: {track: <source id>}",
	} {
		if _, err := decodeUpstream(t, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want it to name %q", src, err, want)
		}
	}
}

func TestUpstream_UnknownFieldRejected(t *testing.T) {
	if _, err := decodeUpstream(t, "upstream:\n  sources:\n    core: { git: https://x, match: v }\n"); err == nil {
		t.Fatal("an unknown source field must be rejected")
	}
}

func TestUpstream_Validation(t *testing.T) {
	cases := map[string]struct {
		u    UpstreamConfig
		want string
	}{
		"no kind":        {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a"}}}, "set exactly one kind"},
		"two kinds":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Image: "i"}}}, "found both"},
		"image tag":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Image: "ghcr.io/x/y:1.0"}}}, "names a tag or digest"},
		"bad regex":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Select: UpstreamSelect{Exclude: []string{"("}}}}}, "select.exclude[0]"},
		"unknown source": {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "b"}}, `"b" is not a declared source`},
		"two modes":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a", Continue: "a"}}, "at most one"},
		"since on own":   {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Since: "v1.0.0"}}, "only meaningful"},
		"bad since":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a", Since: "soon"}}, "is not a version"},
	}
	for name, c := range cases {
		errs := strings.Join(validateUpstream(c.u), "\n")
		if !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}
}
