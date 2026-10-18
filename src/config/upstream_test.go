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
	if errs := validateUpstream(&Config{Upstream: u}); len(errs) != 0 {
		t.Fatalf("valid config rejected: %v", errs)
	}
	port := UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Image: "registry.local:5000/team/app"}}}
	if errs := validateUpstream(&Config{Upstream: port}); len(errs) != 0 {
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
		"bad since":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a", Since: "soon"}}, "is not a semver version"},
		"bad scheme":     {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Scheme: "calver"}}}, "unknown version scheme"},
		"bad rewrite":    {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Select: UpstreamSelect{Rewrite: []UpstreamRewrite{{From: "("}}}}}}, "select.rewrite[0].from"},
	}
	for name, c := range cases {
		errs := strings.Join(validateUpstream(&Config{Upstream: c.u}), "\n")
		if !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}
}

func TestUpstream_EvidenceValidation(t *testing.T) {
	builds := OrderedBuilds{{ID: "image", Kind: "docker"}, {ID: "cli", Kind: "binary"}}
	src := OrderedUpstreamSources{{ID: "core", Git: "https://github.com/ansible/ansible"}}
	track := UpstreamVersion{Track: "core"}
	cases := map[string]struct {
		u    UpstreamConfig
		want string // "" = valid
	}{
		"track without evidence": {UpstreamConfig{Sources: src, Version: track}, "upstream.evidence.image: version.track needs proof"},
		"track with a probe": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Package: "ansible-core", Manager: "pip"}}}}, ""},
		"track with ancestry":         {UpstreamConfig{Sources: src, Version: UpstreamVersion{Track: "core", Ancestry: true}}, ""},
		"ancestry on an image source": {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "core", Image: "ghcr.io/a/b"}}, Version: UpstreamVersion{Track: "core", Ancestry: true}}, "needs track naming a git source"},
		"evidence on a binary build": {UpstreamConfig{Sources: src, Version: UpstreamVersion{Track: "core", Ancestry: true}, Evidence: OrderedUpstreamEvidence{
			{ID: "cli", Probe: UpstreamProbe{Label: "x"}}}}, "not a docker build"},
		"two probe kinds": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Label: "x", File: "/v"}}}}, "exactly one of label | file | package"},
		"package without manager": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Package: "nut"}}}}, "package needs manager"},
		"relative file": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{File: "VERSION"}}}}, "must be an absolute path"},
		"report without reason": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Label: "v"}, OnMismatch: "report"}}}, "reason: required"},
		"version report with reason": {UpstreamConfig{Sources: src, Version: UpstreamVersion{Continue: "core", OnMismatch: "report", Reason: "shared lineage with riffcc"}}, ""},
		"evidence under own": {UpstreamConfig{Sources: src, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Label: "v"}}}}, "set version.track or version.continue"},
	}
	for name, c := range cases {
		errs := strings.Join(validateUpstream(&Config{Builds: builds, Upstream: c.u}), "\n")
		if (c.want == "") != (errs == "") || !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}
}
