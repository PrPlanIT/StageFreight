package config

import "testing"

// baseCfg starts from defaults() — the SAME base loadResolved uses — so tests carry the
// unconditional gitops.backend=flux / docker.backend=compose defaults.
func baseCfg() *Config { return defaults() }

// ── validateLifecycle: the mode ALLOWLIST (the only lifecycle validation) ──

func TestValidateLifecycle_KnownModesPass(t *testing.T) {
	for _, m := range []string{"", "image", "gitops", "docker", "governance", "GitOps", "  docker  "} {
		cfg := baseCfg()
		cfg.Lifecycle.Mode = m
		if errs := validateLifecycle(cfg); len(errs) != 0 {
			t.Errorf("mode %q should be allowed, got %v", m, errs)
		}
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

// Inert cross-mode blocks are NOT gated (they do nothing — perform reconciles, publish is
// not-applicable). A gitops repo carrying builds/publish loads fine; gating it would bake in
// single-mode exclusivity and reject legitimate/future multi-mode configs.
func TestValidateLifecycle_InertBlocksNotGated(t *testing.T) {
	cfg := baseCfg()
	cfg.Lifecycle.Mode = "gitops"
	cfg.Builds = OrderedBuilds{{ID: "app"}}
	cfg.Targets = OrderedTargets{{ID: "stable"}}
	if errs := validateLifecycle(cfg); len(errs) != 0 {
		t.Fatalf("inert builds/publish in gitops must NOT be gated, got %v", errs)
	}
}

func TestValidateLifecycle_DefaultedBackends_NoFalsePositive(t *testing.T) {
	// Regression guard: defaults() seeds docker.backend=compose on every config; that must
	// never be mistaken for an authored block. (gitops no longer defaults a backend — it is
	// per-cluster under gitops.<name>.backend, absent until a cluster is declared.)
	cfg := baseCfg()
	if cfg.Docker.Backend == "" {
		t.Fatalf("precondition: defaults() should seed docker backend; got %q", cfg.Docker.Backend)
	}
	if errs := validateLifecycle(cfg); len(errs) != 0 {
		t.Fatalf("defaulted backends must not error, got %v", errs)
	}
}

// ── mode table: the single source of truth dispatch reads (B4 equivalence gate) ──

func TestLifecycleTable_ReproducesLegacyPredicates(t *testing.T) {
	// PhaseReconcile must equal the old `case "gitops","governance":` set in the phase
	// runners — and NOT include docker (docker reconciles via CLI, hit the build default:).
	wantPhaseReconcile := map[string]bool{
		ModeImage: false, ModeGitops: true, ModeDocker: false, ModeGovernance: true,
	}
	for mode, want := range wantPhaseReconcile {
		lm, ok := LookupMode(mode)
		if !ok {
			t.Fatalf("%q should resolve", mode)
		}
		if lm.PhaseReconcile != want {
			t.Errorf("PhaseReconcile[%s] = %v, want %v", mode, lm.PhaseReconcile, want)
		}
	}

	// Backend (RunLifecycle) must be non-nil ONLY for gitops+docker, and resolve the same
	// field the old switch read.
	cfg := baseCfg()
	cfg.GitOps = OrderedClusters{{Name: "dungeon", Backend: "flux"}}
	cfg.Docker.Backend = "compose"
	cases := map[string]string{ // mode → expected backend name ("" ⇒ Backend must be nil)
		ModeImage: "", ModeGitops: "flux", ModeDocker: "compose", ModeGovernance: "",
	}
	for mode, want := range cases {
		lm, _ := LookupMode(mode)
		got := ""
		if lm.Backend != nil {
			got = lm.Backend(cfg)
		}
		if got != want {
			t.Errorf("Backend[%s] = %q, want %q (nil=%v)", mode, got, want, lm.Backend == nil)
		}
		if (mode == ModeImage || mode == ModeGovernance) && lm.Backend != nil {
			t.Errorf("%s must have no RunLifecycle backend (nil)", mode)
		}
	}
}

func TestLifecycleTable_Canonicalizes(t *testing.T) {
	lm, ok := LookupMode("  GitOps ")
	if !ok || lm.Name != ModeGitops {
		t.Fatalf("case/space should canonicalize to gitops, got %q ok=%v", lm.Name, ok)
	}
	if _, ok := LookupMode("nope"); ok {
		t.Fatal("unknown mode must not resolve")
	}
	// Config.Mode() falls back to image for an unknown mode (dispatch default).
	cfg := baseCfg()
	cfg.Lifecycle.Mode = "nope"
	if cfg.Mode().Name != ModeImage {
		t.Fatalf("unknown mode dispatch should fall back to image, got %q", cfg.Mode().Name)
	}
}
