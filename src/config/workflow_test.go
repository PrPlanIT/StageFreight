package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every common branching model is expressible as a workflow: over the same fields, and each
// one loads (strict decoding, presets, validation) without error.
func TestWorkflow_BranchingModelsLoad(t *testing.T) {
	models := map[string]string{
		"trunk": `
git:
  branches:
    main: ^main$
    feature: ^feature/
    release: ^release-\d+\.\d+$
workflow:
  branches:
    main:
      match: main
      landing: merge_request
      protect: { force_push: false, deletion: false }
      review: { approvals: 1, author_cannot_approve: true }
      checks: { required: [audition, perform, review], up_to_date: true }
      merge: squash
      queue: true
    feature:
      match: feature
      lifetime: short
      max_age: 14d
      landing: direct
    release:
      match: release
      lifetime: on_demand
      created_from: tag
      landing: merge_request
      bypass:
        - { actor: stagefreight, mode: merge_request, reason: "backports open their own merge requests" }
  promote:
    land:     { from: feature, to: main, method: merge_request, require: { checks: [audition, perform], approvals: 1 } }
    backport: { from: main, to: release, method: cherry_pick }
  release:
    from:
      main:    [prerelease, major, minor, patch]
      release: [prerelease, patch]
`,
		"github-flow": `
git:
  branches: { main: ^main$, feature: ^feature/ }
workflow:
  branches:
    main:    { match: main, landing: merge_request, review: { approvals: 1 } }
    feature: { match: feature, lifetime: short }
  promote:
    land: { from: feature, to: main, method: merge_request }
  release:
    from: { main: [major, minor, patch] }
`,
		"gitflow": `
git:
  branches: { main: ^main$, develop: ^develop$, feature: ^feature/, release: ^release/, hotfix: ^hotfix/ }
workflow:
  branches:
    main:    { match: main, landing: merge_request }
    develop: { match: develop, landing: merge_request }
    feature: { match: feature, lifetime: short }
    release: { match: release, lifetime: on_demand, created_from: develop }
    hotfix:  { match: hotfix, lifetime: on_demand, created_from: main }
  promote:
    integrate:       { from: feature, to: develop, method: merge_request }
    ship:            { from: release, to: main, method: merge_request }
    ship-back:       { from: release, to: develop, method: merge_request }
    hotfix:          { from: hotfix, to: main, method: merge_request }
    hotfix-back:     { from: hotfix, to: develop, method: merge_request }
  release:
    from: { main: [major, minor, patch], release: [prerelease] }
`,
		"release-train": `
git:
  branches: { master: ^master$, beta: ^beta$, stable: ^stable$ }
schedules:
  train: { cron: "0 6 * * 1" }
workflow:
  branches:
    master: { match: master, landing: merge_request }
    beta:   { match: beta }
    stable: { match: stable }
  promote:
    to-beta:   { from: master, to: beta, method: fast_forward, schedule: train }
    to-stable: { from: beta, to: stable, method: fast_forward, schedule: train }
  release:
    from: { beta: [prerelease], stable: [major, minor, patch] }
`,
		"environment-flow": `
git:
  branches: { main: ^main$, preprod: ^pre-production$, prod: ^production$ }
workflow:
  branches:
    main:       { match: main, landing: merge_request }
    preprod:    { match: preprod, landing: direct }
    production: { match: prod, landing: direct, enforcement: evaluate }
  promote:
    stage:  { from: main, to: preprod, method: fast_forward }
    deploy: { from: preprod, to: production, method: fast_forward, require: { approvals: 1 } }
`,
	}
	for name, body := range models {
		t.Run(name, func(t *testing.T) {
			cfg, _, err := LoadWithWarnings(writeCfg(t, "version: 1\n"+body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !cfg.Workflow.Present() {
				t.Fatal("a declared workflow must report present")
			}
		})
	}
}

// Absent, nothing is governed.
func TestWorkflow_AbsentIsNotPresent(t *testing.T) {
	cfg, _, err := LoadWithWarnings(writeCfg(t, "version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workflow.Present() {
		t.Error("no workflow: block must not report present")
	}
}

// Each mistake is refused with an error naming where it is and what is allowed.
func TestWorkflow_Validation(t *testing.T) {
	const branches = "git:\n  branches: { main: ^main$, feature: ^feature/, release: ^release-, other: ^x$ }\n"
	cases := map[string]struct{ body, want string }{
		"match missing":        {"workflow:\n  branches:\n    main: { landing: merge_request }\n", "workflow.branches.main.match: required"},
		"match unknown":        {"workflow:\n  branches:\n    main: { match: trunk }\n", `"trunk" is not a git.branches matcher`},
		"match shared":         {"workflow:\n  branches:\n    a: { match: main }\n    b: { match: main }\n", "already selects class"},
		"lifetime unknown":     {"workflow:\n  branches:\n    main: { match: main, lifetime: forever }\n", `lifetime: unknown "forever"`},
		"max_age on long":      {"workflow:\n  branches:\n    main: { match: main, max_age: 14d }\n", "only a short-lived class has a maximum age"},
		"max_age invalid":      {"workflow:\n  branches:\n    f: { match: feature, lifetime: short, max_age: soon }\n", "not a positive duration"},
		"on_demand no source":  {"workflow:\n  branches:\n    r: { match: release, lifetime: on_demand }\n", "required for an on_demand class"},
		"created_from on long": {"workflow:\n  branches:\n    r: { match: release, created_from: tag }\n", "only an on_demand class is cut from something"},
		"created_from unknown": {"workflow:\n  branches:\n    r: { match: release, lifetime: on_demand, created_from: trunk }\n", "neither tag nor a declared class"},
		"created_from self":    {"workflow:\n  branches:\n    r: { match: release, lifetime: on_demand, created_from: r }\n", "can't be cut from itself"},
		"landing unknown":      {"workflow:\n  branches:\n    main: { match: main, landing: pull_request }\n", `landing: unknown "pull_request"`},
		"merge unknown":        {"workflow:\n  branches:\n    main: { match: main, merge: octopus }\n", `merge: unknown "octopus"`},
		"check not pre-merge":  {"workflow:\n  branches:\n    main: { match: main, checks: { required: [publish] } }\n", `"publish" is not a phase that runs before a merge`},
		"review on direct":     {"workflow:\n  branches:\n    main: { match: main, landing: direct, review: { approvals: 1 } }\n", "review applies to merge requests"},
		"bypass no reason":     {"workflow:\n  branches:\n    main: { match: main, bypass: [{ actor: bot, mode: always }] }\n", "bypass[0].reason: required"},
		"bypass mode unknown":  {"workflow:\n  branches:\n    main: { match: main, bypass: [{ actor: bot, mode: sometimes, reason: x }] }\n", `mode: unknown "sometimes"`},
		"promote unknown from": {"workflow:\n  branches:\n    main: { match: main }\n  promote:\n    p: { from: feature, to: main, method: merge_request }\n", `from: "feature" is not a workflow.branches class`},
		"promote same class":   {"workflow:\n  branches:\n    main: { match: main }\n  promote:\n    p: { from: main, to: main, method: tag }\n", "from and to are the same class"},
		"promote no method":    {"workflow:\n  branches:\n    main: { match: main }\n    f: { match: feature }\n  promote:\n    p: { from: f, to: main }\n", "method: required"},
		"promote schedule":     {"workflow:\n  branches:\n    main: { match: main }\n    f: { match: feature }\n  promote:\n    p: { from: f, to: main, method: fast_forward, schedule: weekly }\n", `"weekly" is not a declared schedule`},
		"release unknown":      {"workflow:\n  branches:\n    main: { match: main }\n  release:\n    from: { trunk: [patch] }\n", `"trunk" is not a workflow.branches class`},
		"release empty":        {"workflow:\n  branches:\n    main: { match: main }\n  release:\n    from: { main: [] }\n", "list the release kinds"},
		"release kind unknown": {"workflow:\n  branches:\n    main: { match: main }\n  release:\n    from: { main: [ga] }\n", `unknown "ga"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := LoadWithWarnings(writeCfg(t, "version: 1\n"+branches+c.body))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v\nwant it to contain %q", err, c.want)
			}
		})
	}
}

// A field the schema doesn't know is refused, not silently ignored.
func TestWorkflow_UnknownFieldRefused(t *testing.T) {
	body := "version: 1\ngit:\n  branches: { main: ^main$ }\nworkflow:\n  branches:\n    main: { match: main, protected: true }\n"
	if _, _, err := LoadWithWarnings(writeCfg(t, body)); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Errorf("err = %v, want the unknown field named", err)
	}
}

// workflow: takes a preset like every section; the repo's own keys merge over it.
func TestWorkflow_Preset(t *testing.T) {
	dir := t.TempDir()
	preset := "workflow:\n  branches:\n    main: { match: main, landing: merge_request, review: { approvals: 1 } }\n"
	if err := os.WriteFile(filepath.Join(dir, "workflow-trunk.yml"), []byte(preset), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "version: 1\ngit:\n  branches: { main: ^main$ }\nworkflow:\n  preset: workflow-trunk.yml\n  release:\n    from: { main: [minor, patch] }\n"
	cfgPath := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadWithWarnings(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Workflow.Branches) != 1 || cfg.Workflow.Branches[0].Review.Approvals != 1 {
		t.Errorf("preset classes not applied: %+v", cfg.Workflow.Branches)
	}
	if got := cfg.Workflow.Release.From["main"]; len(got) != 2 {
		t.Errorf("repo keys not merged over the preset: %v", got)
	}
}
