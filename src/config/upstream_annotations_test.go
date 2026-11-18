package config

import (
	"io/fs"
	"strings"
	"testing"
)

func files(m map[string]string) RepoFileReader {
	return func(rel string) ([]byte, error) {
		if s, ok := m[rel]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}
}

const annotatedDockerfile = `FROM docker.io/library/alpine:3.22.2
# renovate: datasource=pypi depName=ansible-core
ARG ANSIBLE_CORE_VERSION=2.21.5
# renovate: datasource=apk depName=isync registryUrl=https://dl-cdn.alpinelinux.org/alpine/v3.22/community
ARG ISYNC_VERSION=1.5.1
# renovate: datasource=deb depName=apt-cacher-ng registryUrl=https://deb.debian.org/debian?suite=trixie&components=main&binaryArch=amd64
ENV APT_CACHER_NG_VERSION=3.7.5
# renovate: datasource=github-tags depName=osTicket/osTicket extractVersion=^v(?<version>\d+\.\d+\.\d+)$
ARG OSTICKET_VERSION=1.18.4
# renovate: datasource=docker depName=ghcr.io/element-hq/synapse
COPY --from=ghcr.io/element-hq/synapse:v1.140.0 /x /x
`

func TestUpstreamAnnotations_DeclareSourcesAndPins(t *testing.T) {
	cfg := &Config{Builds: []BuildConfig{{ID: "image", Kind: "docker"}}}
	if err := applyUpstreamAnnotations(cfg, files(map[string]string{"Dockerfile": annotatedDockerfile})); err != nil {
		t.Fatal(err)
	}
	by := map[string]UpstreamSource{}
	for _, s := range cfg.Upstream.Sources {
		by[s.ID] = s
	}
	if len(by) != 4 {
		t.Fatalf("sources = %+v; the image reference stays with dependency.scope.source_images", cfg.Upstream.Sources)
	}
	if s := by["ansible-core"]; s.PyPI != "ansible-core" || s.Pins[0] != (UpstreamPin{File: "Dockerfile", Arg: "ANSIBLE_CORE_VERSION"}) || s.Annotation != "Dockerfile:2" {
		t.Errorf("pypi: %+v", s)
	}
	s := by["isync"]
	if s.Apk != "isync" || s.Repository != "community" || s.Suite != "" {
		t.Errorf("apk: the registryUrl names the repository; the release follows the FROM: %+v", s)
	}
	if rr := s.RegistryRelease(); rr == nil || rr.Line != 4 || annotatedDockerfile[strings.Index(annotatedDockerfile, "v3.22/community"):][:5] != rr.Value || !strings.HasPrefix(strings.Split(annotatedDockerfile, "\n")[3][rr.Column:], "v3.22/") {
		t.Errorf("registry release position: %+v", rr)
	}
	if s := by["apt-cacher-ng"]; s.Apt != "apt-cacher-ng" || s.Suite != "trixie/main" || s.Pins[0].Arg != "APT_CACHER_NG_VERSION" {
		t.Errorf("deb: %+v", s)
	}
	if s := by["osticket"]; s.Git != "https://github.com/osTicket/osTicket" || s.Select.Tags != `^v(?<version>\d+\.\d+\.\d+)$` {
		t.Errorf("github-tags: %+v", s)
	}
	if errs := validateUpstream(cfg); len(errs) != 0 {
		t.Errorf("annotated sources validate: %v", errs)
	}
}

func TestUpstreamAnnotations_ApkDefaultsToMain(t *testing.T) {
	cfg := &Config{Builds: []BuildConfig{{ID: "image", Kind: "docker"}}}
	df := "FROM alpine:3.22.2\n# renovate: datasource=apk depName=nginx\nARG NGINX_VERSION=1.30.6\n"
	if err := applyUpstreamAnnotations(cfg, files(map[string]string{"Dockerfile": df})); err != nil {
		t.Fatal(err)
	}
	if s := cfg.Upstream.Sources[0]; s.Repository != "main" || s.RegistryRelease() != nil {
		t.Errorf("apk without registryUrl: %+v", s)
	}
}

func TestUpstreamAnnotations_Refused(t *testing.T) {
	for df, want := range map[string]string{
		"# renovate: datasource=repology depName=alpine_3_22/isync\nARG ISYNC_VERSION=1.5.1\n":              "use datasource=apk or datasource=deb",
		"# renovate: datasource=maven depName=x:y\nARG Y_VERSION=1\n":                                       "not an upstream StageFreight lists",
		"# renovate: datasource=deb depName=curl\nARG CURL_VERSION=8\n":                                     "deb needs registryUrl",
		"# renovate: datasource=pypi depName=a/x\nARG A=1\n# renovate: datasource=npm depName=x\nARG B=1\n": "already declared by the annotation at Dockerfile:1",
	} {
		cfg := &Config{Builds: []BuildConfig{{ID: "image", Kind: "docker"}}}
		err := applyUpstreamAnnotations(cfg, files(map[string]string{"Dockerfile": df}))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", df, err, want)
		}
	}
}

// TestUpstreamAnnotations_ConfigWins: a declared source keeps what it sets; the annotation
// adds its pin.
func TestUpstreamAnnotations_ConfigWins(t *testing.T) {
	cfg := &Config{
		Builds:   []BuildConfig{{ID: "image", Kind: "docker"}},
		Upstream: UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "core", Git: "https://example.com/core.git", MirrorOf: "https://origin/core.git"}}},
	}
	df := "# renovate: datasource=git-tags depName=https://elsewhere/core\nARG CORE_REF=v11.4.2\n"
	if err := applyUpstreamAnnotations(cfg, files(map[string]string{"Dockerfile": df})); err != nil {
		t.Fatal(err)
	}
	s := cfg.Upstream.Sources[0]
	if len(cfg.Upstream.Sources) != 1 || s.Git != "https://example.com/core.git" || s.MirrorOf == "" || len(s.Pins) != 1 || s.Pins[0].Arg != "CORE_REF" {
		t.Errorf("merged: %+v", cfg.Upstream.Sources)
	}
}

func TestUpstreamDefaults(t *testing.T) {
	cfg := &Config{
		Builds: []BuildConfig{{ID: "image", Kind: "docker"}, {ID: "login", Kind: "docker"}, {ID: "bin", Kind: "binary"}},
		Upstream: UpstreamConfig{
			Sources:  OrderedUpstreamSources{{ID: "isync", Apk: "isync", Repository: "community"}},
			Version:  UpstreamVersion{Track: "isync"},
			Evidence: OrderedUpstreamEvidence{{ID: "login", Probe: UpstreamProbe{File: "/v"}}},
		},
	}
	applyUpstreamDefaults(cfg)
	if ev := cfg.Upstream.Evidence; len(ev) != 2 || ev[1].ID != "image" || ev[1].Probe != (UpstreamProbe{Package: "isync", Manager: "apk"}) || !ev[1].Derived {
		t.Errorf("evidence: %+v", ev)
	}

	fork := &Config{Upstream: UpstreamConfig{
		Sources: OrderedUpstreamSources{{ID: "nut", Git: "https://github.com/networkupstools/nut"}, {ID: "base", Image: "docker.io/library/debian"}},
		Sync:    UpstreamSync{Strategy: "rebase"},
	}}
	applyUpstreamDefaults(fork)
	if fork.Upstream.Sync.From != "nut" {
		t.Errorf("sync.from defaults to the only git source: %q", fork.Upstream.Sync.From)
	}
}
