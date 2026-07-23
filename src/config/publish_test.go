package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOrderedTargetsMapForm: the publish: map decodes in document order with each
// target's ID stamped from its key.
func TestOrderedTargetsMapForm(t *testing.T) {
	y := "b-target: { kind: registry, build: x }\n" +
		"a-target: { kind: release }\n" +
		"c-target: { kind: pages }\n"
	var ot OrderedTargets
	if err := yaml.Unmarshal([]byte(y), &ot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ot) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(ot))
	}
	wantIDs := []string{"b-target", "a-target", "c-target"} // document order, NOT sorted
	for i, want := range wantIDs {
		if ot[i].ID != want {
			t.Fatalf("target[%d].ID = %q, want %q (order/id from key)", i, ot[i].ID, want)
		}
	}
	if ot[0].Kind != "registry" || ot[1].Kind != "release" || ot[2].Kind != "pages" {
		t.Fatalf("kinds not decoded: %+v", ot)
	}
}

// TestOrderedTargetsRejectsList: the retired list form is not accepted by the
// publish grammar (map-only).
func TestOrderedTargetsRejectsList(t *testing.T) {
	var ot OrderedTargets
	if err := yaml.Unmarshal([]byte("- id: x\n  kind: registry\n"), &ot); err == nil {
		t.Fatal("publish must reject the list form")
	}
}

// TestRegistryAcceptsScalarAndList: registry: accepts a single id or a list.
func TestRegistryAcceptsScalarAndList(t *testing.T) {
	var ot OrderedTargets
	y := "a: { kind: registry, build: b, registry: harbor }\n" +
		"m: { kind: registry, build: b, registry: [dockerhub, ghcr] }\n"
	if err := yaml.Unmarshal([]byte(y), &ot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ot[0].Registry) != 1 || ot[0].Registry[0] != "harbor" {
		t.Fatalf("scalar registry = %v", ot[0].Registry)
	}
	if len(ot[1].Registry) != 2 || ot[1].Registry[0] != "dockerhub" || ot[1].Registry[1] != "ghcr" {
		t.Fatalf("list registry = %v", ot[1].Registry)
	}
}

// TestExpandMultiRegistryTargets: registry: [a,b,c] fans into one target per id
// (id "<id>-<reg>"), single-registry targets pass through, fields are carried.
func TestExpandMultiRegistryTargets(t *testing.T) {
	in := OrderedTargets{
		{ID: "stable", Kind: "registry", Build: "b", Registry: StringOrList{"dockerhub", "ghcr", "harbor"}, Tags: []string{"v{version}"}},
		{ID: "harbor-test", Kind: "registry", Build: "b", Registry: StringOrList{"harbor"}},
	}
	out := expandMultiRegistryTargets(in)
	if len(out) != 4 {
		t.Fatalf("expected 4 targets (3 fanned + 1 passthrough), got %d", len(out))
	}
	wantIDs := []string{"stable-dockerhub", "stable-ghcr", "stable-harbor", "harbor-test"}
	for i, want := range wantIDs {
		if out[i].ID != want {
			t.Fatalf("out[%d].ID = %q, want %q", i, out[i].ID, want)
		}
		if len(out[i].Registry) != 1 {
			t.Fatalf("out[%d] not single-registry: %v", i, out[i].Registry)
		}
	}
	if out[0].Registry[0] != "dockerhub" || out[0].Tags[0] != "v{version}" || out[0].Build != "b" {
		t.Fatalf("fanned target lost fields: %+v", out[0])
	}
}

// TestPublishFanFiresOnLoad: the fan runs inside the real load path (loadResolved),
// not just the unit helper — a registry: list config yields fanned targets.
func TestPublishFanFiresOnLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".stagefreight.yml")
	cfg := "version: 1\n" +
		"versioning:\n" +
		"  tag_sources:\n" +
		"    - { id: stable, pattern: \"^v.*\" }\n" +
		"registries:\n" +
		"  - { id: dockerhub, provider: docker, url: docker.io, default_path: o/r }\n" +
		"  - { id: ghcr, provider: ghcr, url: ghcr.io, default_path: o/r }\n" +
		"builds:\n" +
		"  - { id: img, kind: docker }\n" +
		"publish:\n" +
		"  stable:\n" +
		"    kind: registry\n" +
		"    registry: [dockerhub, ghcr]\n" +
		"    build: img\n" +
		"    tags: [\"v{version}\"]\n" +
		"    when: { git_tags: [stable], events: [tag] }\n"
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadWithWarnings(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ids := map[string]bool{}
	for _, tc := range loaded.Targets {
		ids[tc.ID] = true
	}
	if !ids["stable-dockerhub"] || !ids["stable-ghcr"] {
		t.Fatalf("fan did not fire on load; target ids = %v", ids)
	}
	if ids["stable"] {
		t.Fatal("authored multi-registry id should be replaced by the fanned ids")
	}
}

// TestReleaseSyncXOR: a repo that is a kind:release destination must NOT also sync
// releases — this is the config-time XOR that retires the double-projection 422.
func TestReleaseSyncXOR(t *testing.T) {
	repos := []RepoConfig{
		{ID: "primary", Roles: []string{"primary"}},
		{ID: "gh", Roles: []string{"mirror"}, Sync: SyncConfig{Releases: &FacetSpec{Scope: "all"}}},
	}
	targets := []TargetConfig{{ID: "rel", Kind: "release", Repos: StringOrList{"gh"}}}
	if errs := ValidateTargetRepoRefs(targets, repos); !syncErrContains(errs, "release destination AND syncs releases") {
		t.Fatalf("expected release/sync XOR error, got %v", errs)
	}
}

// TestReleaseDestinationCanSyncNonReleaseFacets: the XOR is releases-specific — a
// release destination may still sync branches/tags (SF's github-mirror case).
func TestReleaseDestinationCanSyncNonReleaseFacets(t *testing.T) {
	repos := []RepoConfig{
		{ID: "primary", Roles: []string{"primary"}},
		{ID: "gh", Roles: []string{"mirror"}, Sync: SyncConfig{
			Branches: &FacetSpec{Scope: "all", Prune: true},
			Tags:     &FacetSpec{Scope: "all", Prune: true},
		}},
	}
	targets := []TargetConfig{{ID: "rel", Kind: "release", Repos: StringOrList{"gh"}}}
	if errs := ValidateTargetRepoRefs(targets, repos); syncErrContains(errs, "syncs releases") {
		t.Fatalf("branches/tags sync must not trip the releases XOR: %v", errs)
	}
}

// TestReleaseRepoMustExist: a release destination must resolve to a declared repo.
func TestReleaseRepoMustExist(t *testing.T) {
	repos := []RepoConfig{{ID: "primary", Roles: []string{"primary"}}}
	targets := []TargetConfig{{ID: "rel", Kind: "release", Repos: StringOrList{"nope"}}}
	if errs := ValidateTargetRepoRefs(targets, repos); !syncErrContains(errs, "not found in repos") {
		t.Fatalf("expected repo-not-found error, got %v", errs)
	}
}

// TestPublishHardBreaksTargets: the retired targets: key no longer parses — a
// config using it fails at decode (KnownFields), the hard break.
func TestPublishHardBreaksTargets(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(p, []byte("version: 1\ntargets:\n  - id: x\n    kind: registry\n    build: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadWithWarnings(p); err == nil {
		t.Fatal("expected load to fail on the retired targets: key (hard break)")
	}
}
