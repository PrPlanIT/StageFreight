package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseSync(t *testing.T, y string) SyncConfig {
	t.Helper()
	var s SyncConfig
	if err := yaml.Unmarshal([]byte(y), &s); err != nil {
		t.Fatalf("unmarshal %q: %v", y, err)
	}
	return s
}

func syncErrContains(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

// TestSyncWholeHogExact: `sync: exact` applies the exact preset to every facet.
func TestSyncWholeHogExact(t *testing.T) {
	s := parseSync(t, "exact")
	for name, f := range map[string]*FacetSpec{"branches": s.Branches, "tags": s.Tags, "releases": s.Releases} {
		if f == nil {
			t.Fatalf("whole-hog exact left %s unset", name)
		}
		if f.Scope != scopeAll || !f.Prune {
			t.Fatalf("%s = %+v, want {all, prune}", name, f)
		}
	}
}

// TestSyncPerFacetScalar: each facet's scalar is its own scope preset.
func TestSyncPerFacetScalar(t *testing.T) {
	s := parseSync(t, "branches: current\ntags: all\nreleases: exact\n")
	if s.Branches.Scope != scopeCurrent || s.Branches.Prune {
		t.Fatalf("branches = %+v, want {current}", s.Branches)
	}
	if s.Tags.Scope != scopeAll || s.Tags.Prune {
		t.Fatalf("tags = %+v, want {all}", s.Tags)
	}
	if s.Releases.Scope != scopeAll || !s.Releases.Prune {
		t.Fatalf("releases = %+v, want {all, prune}", s.Releases)
	}
}

// TestSyncFacetMapOptions: a facet map seeds from scope, then overlays options;
// drafts is carried only when explicit (never by a preset).
func TestSyncFacetMapOptions(t *testing.T) {
	s := parseSync(t, "releases:\n  scope: all\n  drafts: true\n  assets: link\n")
	if s.Releases == nil || s.Releases.Scope != scopeAll {
		t.Fatalf("releases = %+v", s.Releases)
	}
	if !s.Releases.Drafts || s.Releases.Assets != "link" {
		t.Fatalf("options not decoded: %+v", s.Releases)
	}
	if s.Branches != nil || s.Tags != nil {
		t.Fatal("omitted facets must be nil (not synced)")
	}
}

// TestSyncOmittedFacetNotSynced: absence of a facet key means don't sync it.
func TestSyncOmittedFacetNotSynced(t *testing.T) {
	s := parseSync(t, "branches: all\n")
	if s.Tags != nil || s.Releases != nil {
		t.Fatal("omitted facet must be nil")
	}
	if !s.SyncsGit() || s.SyncsReleases() {
		t.Fatalf("adapter mismatch: SyncsGit=%v SyncsReleases=%v", s.SyncsGit(), s.SyncsReleases())
	}
}

// TestSyncLegacyBoolForm: the retired git/releases bools translate to the
// behavior-preserving facets (git had unconditional prune → exact; releases was
// add-only → all) and mark the block legacy.
func TestSyncLegacyBoolForm(t *testing.T) {
	s := parseSync(t, "git: true\nreleases: true\n")
	if !s.IsLegacyForm() {
		t.Fatal("expected legacy marker")
	}
	if s.Branches == nil || s.Branches.Scope != scopeAll || !s.Branches.Prune {
		t.Fatalf("git:true → branches exact, got %+v", s.Branches)
	}
	if s.Tags == nil || !s.Tags.Prune {
		t.Fatalf("git:true → tags exact, got %+v", s.Tags)
	}
	if s.Releases == nil || s.Releases.Scope != scopeAll || s.Releases.Prune || !s.Releases.Drafts {
		t.Fatalf("releases:true → releases all (add-only, drafts preserved), got %+v", s.Releases)
	}
}

// TestSyncLegacyDocsDropped: docs:true was inert; it drops to no facets but still
// marks legacy so the deprecation surfaces.
func TestSyncLegacyDocsDropped(t *testing.T) {
	s := parseSync(t, "docs: true\n")
	if s.Active() {
		t.Fatal("docs:true is inert — no facets should sync")
	}
	if !s.IsLegacyForm() {
		t.Fatal("docs should mark legacy")
	}
}

func TestSyncUnknownKeyErrors(t *testing.T) {
	var s SyncConfig
	if err := yaml.Unmarshal([]byte("wiki: all\n"), &s); err == nil {
		t.Fatal("expected error for unknown facet key")
	}
}

func TestSyncBadScopeErrors(t *testing.T) {
	var s SyncConfig
	if err := yaml.Unmarshal([]byte("branches: sometimes\n"), &s); err == nil {
		t.Fatal("expected error for bad scope word")
	}
}

// TestSyncValidationRequiresMirrorRole: sync is one-directional, so it only makes
// sense on a mirror.
func TestSyncValidationRequiresMirrorRole(t *testing.T) {
	forges := []ForgeConfig{{ID: "f", Provider: "github", URL: "https://github.com"}}
	repos := []RepoConfig{
		{ID: "primary", Forge: "f", Project: "p", Roles: []string{"primary"}, Branches: BranchesConfig{Default: "main"}},
		{ID: "weird", Forge: "f", Project: "p2", Roles: []string{}, Sync: SyncConfig{Branches: &FacetSpec{Scope: scopeAll}}},
	}
	errs := ValidateIdentityGraph(forges, repos, nil)
	if !syncErrContains(errs, "sync is only valid on a mirror") {
		t.Fatalf("expected mirror-role error, got %v", errs)
	}
}

// TestFacetSummary: a facet renders back to its canonical scope word + options.
func TestFacetSummary(t *testing.T) {
	cases := []struct {
		spec *FacetSpec
		want string
	}{
		{nil, ""},
		{&FacetSpec{Scope: "current"}, "current"},
		{&FacetSpec{Scope: "all"}, "all"},
		{&FacetSpec{Scope: "all", Prune: true}, "exact"},
		{&FacetSpec{Scope: "all", Drafts: true, Assets: "link"}, "all (drafts,assets:link)"},
	}
	for _, c := range cases {
		if got := c.spec.Summary(); got != c.want {
			t.Errorf("Summary(%+v) = %q, want %q", c.spec, got, c.want)
		}
	}
}

// TestBuildSyncTopology: only mirrors with an active sync appear; the primary is
// the source; facets render as canonical scope words.
func TestBuildSyncTopology(t *testing.T) {
	cfg := &Config{Repos: []RepoConfig{
		{ID: "primary", Roles: []string{"primary"}},
		{ID: "gh", Forge: "github", Roles: []string{"mirror"}, Sync: SyncConfig{
			Branches: &FacetSpec{Scope: "all", Prune: true},
			Tags:     &FacetSpec{Scope: "all", Prune: true},
			Releases: &FacetSpec{Scope: "all"},
		}},
		{ID: "idle", Forge: "gitea", Roles: []string{"mirror"}}, // no sync → excluded
	}}
	topo := BuildSyncTopology(cfg)
	if topo.PrimaryID != "primary" {
		t.Fatalf("primary = %q", topo.PrimaryID)
	}
	if len(topo.Mirrors) != 1 {
		t.Fatalf("expected 1 active mirror, got %d (%+v)", len(topo.Mirrors), topo.Mirrors)
	}
	m := topo.Mirrors[0]
	if m.MirrorID != "gh" || m.Branches != "exact" || m.Tags != "exact" || m.Releases != "all" {
		t.Fatalf("mirror row = %+v", m)
	}
}

// TestSyncValidationDraftsOnBranches: drafts/assets are releases-only.
func TestSyncValidationDraftsOnBranches(t *testing.T) {
	forges := []ForgeConfig{{ID: "f", Provider: "github", URL: "https://github.com"}}
	repos := []RepoConfig{
		{ID: "primary", Forge: "f", Project: "p", Roles: []string{"primary"}, Branches: BranchesConfig{Default: "main"}},
		{ID: "m", Forge: "f", Project: "p2", Roles: []string{"mirror"}, Sync: SyncConfig{Branches: &FacetSpec{Scope: scopeAll, Drafts: true}}},
	}
	errs := ValidateIdentityGraph(forges, repos, nil)
	if !syncErrContains(errs, "drafts is only valid on releases") {
		t.Fatalf("expected drafts placement error, got %v", errs)
	}
}
