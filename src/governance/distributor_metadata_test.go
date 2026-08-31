package governance

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPlanDistribution_BrandedEntryDistributesMetadata proves the Phase B/C payoff:
// a BRANDED catalog entry (title/description/topics/license, anchored on a location)
// lands as the satellite's metadata: block in its sealed .stagefreight.yml, with org
// DERIVED from the location's group — the satellite never re-declares its identity.
// This mirrors MaintenancePolicy's prometheus-eaton-ups-exporter entry.
func TestPlanDistribution_BrandedEntryDistributesMetadata(t *testing.T) {
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID:          "rebaseable-fork-docker",
			Credentials: "GITLAB_HOMELABHD",
			Config: map[string]any{
				"version": 1,
				"forges": map[string]any{
					"gitlab": map[string]any{
						"provider": "gitlab",
						"url":      "https://gitlab.prplanit.com",
					},
					"github": map[string]any{
						"provider": "github",
						"url":      "https://github.com",
					},
				},
			},
			Repos: ProfileCatalog{{
				ID: "prometheus-eaton-ups-exporter",
				At: "HomeLabHD/prometheus-eaton-ups-exporter",
				Metadata: map[string]any{
					"title": "Eaton UPS Exporter",
					"description": []any{
						"Prometheus exporter for Eaton UPS devices over SNMP/HTTP.",
						"Rootless container exporting Eaton UPS load, runtime, battery, and I/O metrics to Prometheus.",
					},
					"topics":  []any{"prometheus", "exporter", "ups", "eaton", "monitoring", "snmp"},
					"license": "MIT",
				},
			}},
		}},
	}

	plans, err := PlanDistribution(
		gov,
		fakeLoader{},
		nil, // no assets
		nil, // no forge reader → every file is a create
		PresetSourceInfo{
			Provider:  "gitlab",
			ForgeURL:  "https://gitlab.prplanit.com",
			ProjectID: "PrPlanIT/MaintenancePolicy",
			Ref:       "deadbeef",
		},
		"PrPlanIT/MaintenancePolicy",
	)
	requireNoError(t, err)

	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	plan := plans[0]
	if plan.Repo != "HomeLabHD/prometheus-eaton-ups-exporter" {
		t.Fatalf("plan targets wrong repo: %q", plan.Repo)
	}
	if plan.Credentials != "GITLAB_HOMELABHD" {
		t.Fatalf("plan carries wrong credentials: %q", plan.Credentials)
	}

	// Find the sealed .stagefreight.yml and parse its metadata: block.
	var sealed []byte
	for _, f := range plan.Files {
		if f.Path == ".stagefreight.yml" {
			sealed = f.Content
		}
	}
	if sealed == nil {
		t.Fatal("no .stagefreight.yml in the plan")
	}

	var parsed struct {
		Metadata map[string]any `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(sealed, &parsed); err != nil {
		t.Fatalf("sealed config is not valid YAML: %v", err)
	}
	if parsed.Metadata == nil {
		t.Fatalf("branded entry did not distribute a metadata: block; got:\n%s", sealed)
	}

	// org is DERIVED from the location group — never re-declared by the satellite.
	if got := parsed.Metadata["org"]; got != "HomeLabHD" {
		t.Errorf("metadata.org: want HomeLabHD (derived from location), got %v", got)
	}
	if got := parsed.Metadata["title"]; got != "Eaton UPS Exporter" {
		t.Errorf("metadata.title: want %q, got %v", "Eaton UPS Exporter", got)
	}
	if got := parsed.Metadata["license"]; got != "MIT" {
		t.Errorf("metadata.license: want MIT, got %v", got)
	}
	topics, _ := parsed.Metadata["topics"].([]any)
	if len(topics) != 6 {
		t.Errorf("metadata.topics: want 6 topics, got %v", parsed.Metadata["topics"])
	}
	if !strings.Contains(string(sealed), "Rootless container exporting Eaton UPS") {
		t.Errorf("sealed config dropped the tiered description tail:\n%s", sealed)
	}
}

// TestPlanDistribution_ConcretizesRepos proves the vars→facts collapse's distribution
// half: the catalog entry's AUTHORITATIVE location becomes the satellite's CONCRETE
// repos section (no shared repos preset with var-holes, no slug circularity) — primary
// on the source provider's forge at the entry location, plus a github mirror derived
// from the org's github alias. metadata.org is always injected (a coordinate, not
// branding), so the satellite resolves {org.*}/{path.*}/{slug} at its own load.
func TestPlanDistribution_ConcretizesRepos(t *testing.T) {
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID:          "rebaseable-fork-docker",
			Credentials: "GITLAB_HOMELABHD",
			Config: map[string]any{
				"version": 1,
				"forges": map[string]any{
					"gitlab": map[string]any{
						"provider": "gitlab",
						"url":      "https://gitlab.prplanit.com",
					},
					"github": map[string]any{
						"provider": "github",
						"url":      "https://github.com",
					},
				},
				"orgs": map[string]any{
					"HomeLabHD": map[string]any{
						"maintainer": "HomeLabHD <homelabhelp@gmail.com>",
						"aliases":    map[string]any{"handle": "hlhd", "github": "PrPlanIT", "docker": "prplanit"},
					},
				},
			},
			Repos: ProfileCatalog{{
				ID: "prometheus-eaton-ups-exporter",
				At: "HomeLabHD/prometheus-eaton-ups-exporter",
			}},
		}},
	}

	plans, err := PlanDistribution(gov, fakeLoader{}, nil, nil,
		PresetSourceInfo{Provider: "gitlab", ForgeURL: "https://gitlab.prplanit.com", ProjectID: "PrPlanIT/MaintenancePolicy", Ref: "deadbeef"},
		"PrPlanIT/MaintenancePolicy")
	requireNoError(t, err)

	var sealed []byte
	for _, f := range plans[0].Files {
		if f.Path == ".stagefreight.yml" {
			sealed = f.Content
		}
	}
	var parsed struct {
		Metadata map[string]any            `yaml:"metadata"`
		Repos    map[string]map[string]any `yaml:"repos"`
	}
	if err := yaml.Unmarshal(sealed, &parsed); err != nil {
		t.Fatalf("sealed config invalid YAML: %v", err)
	}

	// Location-only entry STILL gets metadata.org — the coordinate every fact needs.
	if got := parsed.Metadata["org"]; got != "HomeLabHD" {
		t.Errorf("metadata.org: want HomeLabHD (derived), got %v", got)
	}

	primary := parsed.Repos["primary"]
	if primary == nil {
		t.Fatalf("sealed config must carry a concrete primary repo, got repos=%v", parsed.Repos)
	}
	if primary["project"] != "HomeLabHD/prometheus-eaton-ups-exporter" || primary["forge"] != "gitlab" {
		t.Errorf("primary must be the entry location on the source forge, got %v", primary)
	}
	if _, hasTemplate := primary["project"].(string); hasTemplate && strings.Contains(primary["project"].(string), "{") {
		t.Errorf("primary project must be CONCRETE, got %v", primary["project"])
	}

	mirror := parsed.Repos["github-mirror"]
	if mirror == nil {
		t.Fatalf("org with a github alias must yield a github mirror, got repos=%v", parsed.Repos)
	}
	if mirror["project"] != "PrPlanIT/prometheus-eaton-ups-exporter" || mirror["forge"] != "github" {
		t.Errorf("mirror must derive from the github alias + slug, got %v", mirror)
	}
}

// TestPlanDistribution_CachesComposedPresets proves a `presets: [a, b]` composition has
// BOTH files seeded into the satellite's preset-cache — else a tracking satellite fails to
// resolve the composed section ("open .../preset-cache/<path>: no such file").
func TestPlanDistribution_CachesComposedPresets(t *testing.T) {
	loader := fakeLoader{
		"preset/stencils-core.yml":   "stencils: { license: { label: license } }",
		"preset/stencils-docker.yml": "stencils: { docker: { render: shield, message: docker } }",
	}
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID: "p1",
			Config: map[string]any{
				"forges": map[string]any{
					"gitlab": map[string]any{
						"provider": "gitlab",
						"url":      "https://gitlab.prplanit.com",
					},
				},
				"stencils": map[string]any{
					"presets": []any{"preset/stencils-core.yml", "preset/stencils-docker.yml"},
				},
			},
			Repos: ProfileCatalog{{ID: "r1", At: "Org/some-repo"}},
		}},
	}

	plans, err := PlanDistribution(gov, loader, nil, nil, PresetSourceInfo{Provider: "gitlab", Ref: "deadbeef"}, "Org/policy")
	requireNoError(t, err)

	src := NewPresetQualifier(PresetSourceInfo{Provider: "gitlab", Ref: "deadbeef"})
	want := map[string]bool{
		cachePathFor(src, "preset/stencils-core.yml"):   false,
		cachePathFor(src, "preset/stencils-docker.yml"): false,
	}
	for _, f := range plans[0].Files {
		if _, ok := want[f.Path]; ok {
			want[f.Path] = true
		}
	}
	for path, cached := range want {
		if !cached {
			t.Errorf("composed preset %q was not seeded into the preset-cache", path)
		}
	}
}

// A deviating entry's per-repo config: override lives OUTSIDE cluster.Config, so the
// profile-level preset collection cannot see it. Its presets must still be seeded, or
// the satellite receives a config referencing a file that was never distributed and
// dies at load: `loading preset "preset/test-python.yml": no such file or directory`.
func TestPlanDistribution_CachesPerRepoOverridePresets(t *testing.T) {
	loader := fakeLoader{
		"preset/git.yml":         "git: { branches: { main: \"^main$\" } }",
		"preset/test-python.yml": "test: { suites: { unit: { tool: python } } }",
	}
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID: "p1",
			Config: map[string]any{
				"forges": map[string]any{
					"gitlab": map[string]any{
						"provider": "gitlab",
						"url":      "https://gitlab.prplanit.com",
					},
				},
				"git": map[string]any{"preset": "preset/git.yml"},
			},
			Repos: ProfileCatalog{
				{ID: "py", At: "Org/py-repo", Config: map[string]any{
					"test": map[string]any{"preset": "preset/test-python.yml"},
				}},
				{ID: "plain", At: "Org/plain-repo"},
			},
		}},
	}

	plans, err := PlanDistribution(gov, loader, nil, nil, PresetSourceInfo{Provider: "gitlab", Ref: "deadbeef"}, "Org/policy")
	requireNoError(t, err)

	has := func(p DistributionPlan, path string) bool {
		for _, f := range p.Files {
			if f.Path == path {
				return true
			}
		}
		return false
	}
	byRepo := map[string]DistributionPlan{}
	for _, p := range plans {
		byRepo[p.Repo] = p
	}

	src2 := NewPresetQualifier(PresetSourceInfo{Provider: "gitlab", Ref: "deadbeef"})
	py := byRepo["Org/py-repo"]
	if !has(py, cachePathFor(src2, "preset/test-python.yml")) {
		t.Error("the deviating entry's own preset must be seeded into ITS cache")
	}
	if !has(py, cachePathFor(src2, "preset/git.yml")) {
		t.Error("profile presets must still reach a deviating entry")
	}

	// The override is per-repo: another member must NOT receive it.
	plain := byRepo["Org/plain-repo"]
	if has(plain, cachePathFor(src2, "preset/test-python.yml")) {
		t.Error("one repo's override preset must not be distributed to every member")
	}
	if !has(plain, cachePathFor(src2, "preset/git.yml")) {
		t.Error("profile presets must reach every member")
	}
}

// A render that cannot load must never leave the control repo. Distribution is
// fleet-wide and simultaneous, so an invalid config fails EVERY governed repo at once,
// at audition, before anything runs — the reconcile plan cannot see this, because it
// reports which files change and not whether their contents load.
//
// This reproduces the real incident: a contents stencil naming a build the shared
// preset does not declare (the preset defines one build under the generic id "image").
func TestPlanDistribution_RejectsUnloadableRender(t *testing.T) {
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID:          "hlhd-docker",
			Credentials: "GITLAB_HOMELABHD",
			Config: map[string]any{
				"version": 1,
				"forges": map[string]any{
					"gitlab": map[string]any{
						"provider": "gitlab",
						"url":      "https://gitlab.prplanit.com",
					},
					"github": map[string]any{
						"provider": "github",
						"url":      "https://github.com",
					},
				},
				"builds": map[string]any{
					"image": map[string]any{"kind": "docker"},
				},
			},
			Repos: ProfileCatalog{{
				ID: "ansible",
				At: "HomeLabHD/ansible",
				Config: map[string]any{
					"stencils": map[string]any{
						"contents-base": map[string]any{
							"type":    "contents",
							"build":   "ansible", // no such build — the preset declares "image"
							"section": "inventories.versions",
							"render":  "badges",
						},
					},
				},
			}},
		}},
	}

	_, err := PlanDistribution(
		gov, fakeLoader{}, nil, nil,
		PresetSourceInfo{
			Provider: "gitlab", ForgeURL: "https://gitlab.prplanit.com",
			ProjectID: "PrPlanIT/MaintenancePolicy", Ref: "deadbeef",
		},
		"PrPlanIT/MaintenancePolicy",
	)
	if err == nil {
		t.Fatal("expected the reconcile to fail rather than distribute a config that cannot load")
	}
	// The operator must learn WHICH repo and WHAT is wrong without reading a satellite's
	// pipeline log — that log is the failure mode this check exists to prevent.
	if !strings.Contains(err.Error(), "HomeLabHD/ansible") {
		t.Errorf("error must name the offending repo, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ansible") || !strings.Contains(err.Error(), "build") {
		t.Errorf("error must surface the underlying validation failure, got: %v", err)
	}
}

// The gate must not reject a VALID render — a false positive would block every
// reconcile and be worse than the bug it guards.
func TestPlanDistribution_AcceptsValidRender(t *testing.T) {
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID:          "hlhd-docker",
			Credentials: "GITLAB_HOMELABHD",
			Config: map[string]any{
				"version": 1,
				"forges": map[string]any{
					"gitlab": map[string]any{
						"provider": "gitlab",
						"url":      "https://gitlab.prplanit.com",
					},
					"github": map[string]any{
						"provider": "github",
						"url":      "https://github.com",
					},
				},
				"builds": map[string]any{
					"image": map[string]any{"kind": "docker"},
				},
			},
			Repos: ProfileCatalog{{
				ID: "ansible",
				At: "HomeLabHD/ansible",
				Config: map[string]any{
					"stencils": map[string]any{
						"contents-base": map[string]any{
							"type":    "contents",
							"build":   "image",
							"section": "inventories.versions",
							"render":  "badges",
						},
					},
				},
			}},
		}},
	}

	if _, err := PlanDistribution(
		gov, fakeLoader{}, nil, nil,
		PresetSourceInfo{
			Provider: "gitlab", ForgeURL: "https://gitlab.prplanit.com",
			ProjectID: "PrPlanIT/MaintenancePolicy", Ref: "deadbeef",
		},
		"PrPlanIT/MaintenancePolicy",
	); err != nil {
		t.Fatalf("valid render must plan cleanly, got: %v", err)
	}
}

// ciGovFixture builds a profile whose satellites declare the given ci.forges.
func ciGovFixture(forges []any) *GovernanceConfig {
	ci := map[string]any{"image": "img:latest"}
	if forges != nil {
		ci["forge"] = forges
	}
	return &GovernanceConfig{
		Profiles: ProfileList{{
			ID:          "hlhd-docker",
			Credentials: "GITLAB_HOMELABHD",
			Config: map[string]any{
				"version": 1,
				"ci":      ci,
				"forges": map[string]any{
					"gitlab": map[string]any{"provider": "gitlab", "url": "https://gitlab.prplanit.com"},
					"github": map[string]any{"provider": "github", "url": "https://github.com"},
				},
				"builds": map[string]any{"image": map[string]any{"kind": "docker"}},
			},
			Repos: ProfileCatalog{{ID: "ansible", At: "HomeLabHD/ansible"}},
		}},
	}
}

func planCIFixture(t *testing.T, gov *GovernanceConfig) (DistributionPlan, error) {
	t.Helper()
	plans, err := PlanDistribution(
		gov, fakeLoader{}, nil, nil,
		PresetSourceInfo{
			Provider: "gitlab", ForgeURL: "https://gitlab.prplanit.com",
			ProjectID: "PrPlanIT/MaintenancePolicy", Ref: "deadbeef",
		},
		"PrPlanIT/MaintenancePolicy",
	)
	if err != nil {
		return DistributionPlan{}, err
	}
	return plans[0], nil
}

func planHasPath(p DistributionPlan, path string) bool {
	for _, f := range p.Files {
		if f.Path == path {
			return true
		}
	}
	return false
}

// The CI file is DERIVED from .stagefreight.yml, so governance must ship both. Shipping
// the config alone leaves the satellite self-inconsistent and audition rejects it as
// "CI is stale" — a fleet-wide breakage nobody can clear without re-rendering by hand.
func TestPlanDistribution_DistributesDeclaredCISkeleton(t *testing.T) {
	plan, err := planCIFixture(t, ciGovFixture([]any{"gitlab"}))
	requireNoError(t, err)
	if !planHasPath(plan, ".gitlab-ci.yml") {
		t.Fatalf("declared ci.forges must distribute the pipeline, got %v", plan.Files)
	}
}

// A repo that has not said where its CI runs gets NOTHING. Inferring from the primary
// forge would write a skeleton for a forge that may never run it — the mirror-CI case.
func TestPlanDistribution_NoCIForgeDistributesNoSkeleton(t *testing.T) {
	plan, err := planCIFixture(t, ciGovFixture(nil))
	requireNoError(t, err)
	for _, f := range plan.Files {
		if strings.Contains(f.Path, "gitlab-ci") || strings.Contains(f.Path, "workflows") {
			t.Fatalf("undeclared ci.forges must distribute no pipeline, got %q", f.Path)
		}
	}
}

// Several forges each write their own file, so declaring two is well-defined.
func TestPlanDistribution_DistributesMultipleCISkeletons(t *testing.T) {
	plan, err := planCIFixture(t, ciGovFixture([]any{"gitlab", "github"}))
	requireNoError(t, err)
	for _, want := range []string{".gitlab-ci.yml", ".github/workflows/stagefreight.yml"} {
		if !planHasPath(plan, want) {
			t.Errorf("expected %q among distributed files, got %v", want, plan.Files)
		}
	}
}

// An unsupported or repeated forge is a typo, and must fail the reconcile rather than
// silently distributing one pipeline or overwriting a path twice.
func TestPlanDistribution_RejectsBadCIForges(t *testing.T) {
	if _, err := planCIFixture(t, ciGovFixture([]any{"bitbucket"})); err == nil {
		t.Error("unsupported ci.forges entry must fail the reconcile")
	}
	if _, err := planCIFixture(t, ciGovFixture([]any{"gitlab", "gitlab"})); err == nil {
		t.Error("duplicate ci.forges entry must fail the reconcile")
	}
}

// cachePathFor derives where a preset is retained for a given governance source — the
// key the satellite's resolver will ask for. Asserting a literal path here would only
// re-state the layout; what matters is that retention and lookup agree.
func cachePathFor(src PresetQualifier, localPath string) string {
	p, err := sanitizePresetCachePath(retentionKey(src.Qualify(localPath)))
	if err != nil {
		panic(err)
	}
	return p
}
