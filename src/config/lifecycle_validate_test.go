package config

import "testing"

// baseCfg starts from defaults() — the SAME base loadResolved uses — so tests carry the
// unconditional gitops.backend=flux / docker.backend=compose defaults. That is exactly the
// state that must NOT produce a false positive (the regression the naïve design would have hit).
func baseCfg() *Config { return defaults() }

func TestValidateLifecycle_ImageDefault_WithBuildsPublish(t *testing.T) {
	// SF's shape: no mode (empty ≡ image), authored builds + publish. Must pass.
	cfg := baseCfg()
	cfg.Builds = OrderedBuilds{{ID: "app"}}
	cfg.Targets = OrderedTargets{{ID: "stable"}}
	if errs := validateLifecycle(cfg); len(errs) != 0 {
		t.Fatalf("image config with builds+publish should pass, got %v", errs)
	}
}

func TestValidateLifecycle_DefaultedBackends_NoFalsePositive(t *testing.T) {
	// The regression guard: an image config carrying ONLY the defaulted gitops/docker
	// backends (flux/compose from defaults(), no builds/publish) must pass — proving the
	// cross-mode default pollution does not trip the check.
	cfg := baseCfg() // empty mode ≡ image; gitops.backend=flux, docker.backend=compose from defaults()
	if cfg.GitOps.Backend == "" || cfg.Docker.Backend == "" {
		t.Fatalf("precondition: defaults() should seed gitops/docker backends (flux/compose); got %q/%q",
			cfg.GitOps.Backend, cfg.Docker.Backend)
	}
	if errs := validateLifecycle(cfg); len(errs) != 0 {
		t.Fatalf("defaulted gitops/docker backends must not trip legality, got %v", errs)
	}
}

func TestValidateLifecycle_GitopsClean(t *testing.T) {
	// dungeon's shape: gitops mode, gitops.backend set, no builds/publish. Must pass —
	// including the defaulted docker.backend=compose that defaults() left behind.
	cfg := baseCfg()
	cfg.Lifecycle.Mode = "gitops"
	cfg.GitOps.Backend = "flux"
	if errs := validateLifecycle(cfg); len(errs) != 0 {
		t.Fatalf("clean gitops config should pass, got %v", errs)
	}
}

func TestValidateLifecycle_UnknownMode(t *testing.T) {
	cfg := baseCfg()
	cfg.Lifecycle.Mode = "gitpos" // typo — today silently runs the build body
	errs := validateLifecycle(cfg)
	if !hasErr(errs, "unknown mode") {
		t.Fatalf("typo'd mode should error with 'unknown mode', got %v", errs)
	}
}

func TestValidateLifecycle_BuildsInNonImage(t *testing.T) {
	for _, mode := range []string{"gitops", "governance", "docker"} {
		cfg := baseCfg()
		cfg.Lifecycle.Mode = mode
		cfg.Builds = OrderedBuilds{{ID: "app"}}
		if errs := validateLifecycle(cfg); !hasErr(errs, "builds: valid only in lifecycle.mode: image") {
			t.Fatalf("builds in mode %q should error, got %v", mode, errs)
		}
	}
}

func TestValidateLifecycle_PublishInNonImage(t *testing.T) {
	cfg := baseCfg()
	cfg.Lifecycle.Mode = "gitops"
	cfg.Targets = OrderedTargets{{ID: "stable"}}
	if errs := validateLifecycle(cfg); !hasErr(errs, "publish: valid only in lifecycle.mode: image") {
		t.Fatalf("publish in gitops should error, got %v", errs)
	}
}

func TestValidateLifecycle_GovernanceBlockGating(t *testing.T) {
	// governance clusters in an image config → error; in governance mode → passes.
	img := baseCfg()
	img.Governance.Clusters = []GovernanceCluster{{ID: "c"}}
	if errs := validateLifecycle(img); !hasErr(errs, "governance: valid only in lifecycle.mode: governance") {
		t.Fatalf("governance block in image mode should error, got %v", errs)
	}

	gov := baseCfg()
	gov.Lifecycle.Mode = "governance"
	gov.Governance.Clusters = []GovernanceCluster{{ID: "c"}}
	if errs := validateLifecycle(gov); len(errs) != 0 {
		t.Fatalf("governance block in governance mode should pass, got %v", errs)
	}
}
