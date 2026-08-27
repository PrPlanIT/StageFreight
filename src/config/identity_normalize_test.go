package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The identity collapse: {org.*}/{orgs.*}/{metadata.*}/{slug}/{path.*} resolve at
// NORMALIZATION (pure functions of the config), so EVERY consumer — push refs, forge
// API, pages, badges, scribe — sees concrete strings from one model. This is the
// end-to-end proof through the real Load path (KnownFields + id-maps + validation).
func TestIdentityResolvesAtLoad(t *testing.T) {
	dir := t.TempDir()
	yml := `version: 1
forges:
  gitlab: { provider: gitlab, url: "https://gl.example", default_path: "{org}/{repo}" }
registries:
  dockerhub: { provider: docker, url: "docker.io", default_path: "{org.handle}/{repo}" }
  ghcr: { provider: ghcr, url: "ghcr.io", default_path: "{org.lower}/{repo}" }
orgs:
  HomeLabHD:
    maintainer: "HomeLabHD <ops@prplanit.com>"
    aliases: { handle: hlhd }
metadata:
  org: HomeLabHD
  title: "ARK Server"
  license: MIT
  names: { dockerhub: arkserver }
repos:
  primary: { forge: gitlab, project: "HomeLabHD/ark-se-server", roles: [primary], branches: { default: main } }
stencils:
  id-card:
    type: text
    body: "{metadata.title} · {metadata.license} · {org.maintainer} · {path.gitlab} · {path.dockerhub} · {path.ghcr} · {slug}"
`
	path := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// Config strings are concrete after load — the stencil body carried every family.
	want := "ARK Server · MIT · HomeLabHD <ops@prplanit.com> · HomeLabHD/ark-se-server · hlhd/arkserver · homelabhd/ark-se-server · ark-se-server"
	body := cfg.StencilsByID()["id-card"].Body
	if body != want {
		t.Errorf("stencil body after load:\n got  %q\n want %q", body, want)
	}

	// default_path FIELDS are concretized in place — the write side (push refs,
	// registry metadata, cache paths) reads these directly.
	if got := cfg.Registries[0].DefaultPath; got != "hlhd/arkserver" {
		t.Errorf("dockerhub default_path = %q, want concrete hlhd/arkserver", got)
	}
	if got := cfg.Forges[0].DefaultPath; got != "HomeLabHD/ark-se-server" {
		t.Errorf("gitlab default_path = %q, want concrete HomeLabHD/ark-se-server", got)
	}
}

// A repo's NAME is the operator's choice, and StageFreight must carry it to every
// surface — including registries, whose repository names are lowercase by specification.
// A repo legitimately named `SteamCMD` must therefore resolve to a pushable `hlhd/steamcmd`
// without a rename or a per-repo metadata.names: override, while the forge path keeps the
// case exactly as authored.
func TestIdentityRegistryPathFoldsCase(t *testing.T) {
	dir := t.TempDir()
	yml := `version: 1
forges:
  gitlab: { provider: gitlab, url: "https://gl.example", default_path: "{org}/{repo}" }
registries:
  dockerhub: { provider: docker, url: "docker.io", default_path: "{org.handle}/{repo}" }
orgs:
  HomeLabHD:
    maintainer: "HomeLabHD <ops@prplanit.com>"
    aliases: { handle: hlhd }
metadata:
  org: HomeLabHD
repos:
  primary: { forge: gitlab, project: "HomeLabHD/SteamCMD", roles: [primary], branches: { default: main } }
stencils:
  id-card: { type: text, body: "{path.dockerhub} · {path.gitlab} · {slug} · {slug.lower}" }
`
	path := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// The registry ref is pushable; the forge path and bare {slug} keep the authored case.
	want := "hlhd/steamcmd · HomeLabHD/SteamCMD · SteamCMD · steamcmd"
	if body := cfg.StencilsByID()["id-card"].Body; body != want {
		t.Errorf("stencil body after load:\n got  %q\n want %q", body, want)
	}
	if got := cfg.Registries[0].DefaultPath; got != "hlhd/steamcmd" {
		t.Errorf("dockerhub default_path = %q, want lowercased hlhd/steamcmd", got)
	}
	if got := cfg.Forges[0].DefaultPath; got != "HomeLabHD/SteamCMD" {
		t.Errorf("gitlab default_path = %q, want case preserved", got)
	}
}

// An explicit per-surface name is still bound by what the registry can express — the
// fold applies after metadata.names:, not only to the derived slug.
func TestIdentityRegistryPathFoldsExplicitName(t *testing.T) {
	dir := t.TempDir()
	yml := `version: 1
registries:
  dockerhub: { provider: docker, url: "docker.io", default_path: "{org.handle}/{repo}" }
forges:
  gitlab: { provider: gitlab, url: "https://gl.example" }
orgs:
  HomeLabHD: { maintainer: "x <x@y>", aliases: { handle: hlhd } }
metadata:
  org: HomeLabHD
  names: { dockerhub: SteamCMD }
repos:
  primary: { forge: gitlab, project: "HomeLabHD/steamcmd", roles: [primary], branches: { default: main } }
`
	path := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Registries[0].DefaultPath; got != "hlhd/steamcmd" {
		t.Errorf("dockerhub default_path = %q, want lowercased hlhd/steamcmd", got)
	}
}

// A typo'd alias must fail LOUDLY at load — never survive into a malformed push ref
// or a forge 404.
func TestIdentityUnresolvedTokenFailsLoad(t *testing.T) {
	dir := t.TempDir()
	yml := `version: 1
orgs:
  HomeLabHD: { maintainer: "x <x@y>", aliases: { handle: hlhd } }
metadata: { org: HomeLabHD }
repos:
  primary: { forge: gitlab, project: "HomeLabHD/thing", roles: [primary], branches: { default: main } }
forges:
  gitlab: { provider: gitlab, url: "https://gl.example" }
stencils:
  bad: { type: text, body: "{org.handel}" }
`
	path := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "{org.handel}") {
		t.Fatalf("typo'd identity token must fail load naming the token, got %v", err)
	}
}
