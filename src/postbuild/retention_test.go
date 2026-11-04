package postbuild

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

func TestRetentionHook_SkipsWhenNoBuildPlan(t *testing.T) {
	hook := RetentionHook()
	pc := makePC()
	// pc.BuildPlan is nil by default
	if hook.Condition(pc) {
		t.Error("Condition() = true when BuildPlan is nil; want false")
	}
}

func TestRetentionHook_SkipsWhenNoActiveRetention(t *testing.T) {
	hook := RetentionHook()
	pc := makePC()
	pc.BuildPlan = &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load: true,
				Push: false,
				Registries: []build.RegistryTarget{
					{
						URL:      "docker.io",
						Path:     "prplanit/example",
						Provider: "docker",
						// Retention not configured — KeepLast == 0
					},
				},
			},
		},
	}
	if hook.Condition(pc) {
		t.Error("Condition() = true when no active retention policies; want false")
	}
}

func TestRetentionHook_ConditionTrueWhenActiveLocalRetention(t *testing.T) {
	hook := RetentionHook()
	pc := makePC()
	pc.BuildPlan = &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load: true,
				Push: false,
				Registries: []build.RegistryTarget{
					{
						URL:      "local",
						Path:     "prplanit/example",
						Provider: "local",
						Retention: config.RetentionPolicy{
							KeepLast: 5,
						},
					},
				},
			},
		},
	}
	if !hook.Condition(pc) {
		t.Error("Condition() = false when BuildPlan has active LOCAL retention; want true")
	}
}

// Remote registry retention is a publish-phase concern; perform must not run it.
func TestRetentionHook_RemoteRetentionNotCountedInPerform(t *testing.T) {
	hook := RetentionHook()
	pc := makePC()
	pc.BuildPlan = &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load: true,
				Registries: []build.RegistryTarget{
					{
						URL:       "docker.io",
						Path:      "prplanit/example",
						Provider:  "docker", // remote
						Retention: config.RetentionPolicy{KeepLast: 5},
					},
				},
			},
		},
	}
	if hook.Condition(pc) {
		t.Error("Condition() = true for REMOTE retention in perform; want false (publish owns it)")
	}
}

func TestHasRetention_LocalRegistry(t *testing.T) {
	plan := &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load: true,
				Push: false,
				Registries: []build.RegistryTarget{
					{
						URL:      "local",
						Path:     "prplanit/example",
						Provider: "local",
						Retention: config.RetentionPolicy{
							KeepLast: 5,
						},
					},
				},
			},
		},
	}

	if !HasRetention(plan) {
		t.Error("HasRetention() = false for local registry with retention; want true")
	}
}

func TestHasRetention_RemoteRegistryNotCounted(t *testing.T) {
	plan := &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load: true,
				Registries: []build.RegistryTarget{
					{
						URL:       "docker.io",
						Path:      "prplanit/example",
						Provider:  "docker",
						Retention: config.RetentionPolicy{KeepLast: 5},
					},
				},
			},
		},
	}

	if HasRetention(plan) {
		t.Error("HasRetention() = true for remote registry; want false (publish owns remote retention)")
	}
}

func TestHasRetention_NoRegistries(t *testing.T) {
	plan := &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load:       true,
				Push:       false,
				Registries: nil,
			},
		},
	}

	if HasRetention(plan) {
		t.Error("HasRetention() = true for step with no registries; want false")
	}
}

// TestRunRetentionSection_DaemonPaths pins local retention on both runner kinds:
// with a Docker daemon the local registry client runs, and on a daemonless runner the
// target is skipped by name with no client call and no error.
func TestRunRetentionSection_DaemonPaths(t *testing.T) {
	plan := &build.BuildPlan{Steps: []build.BuildStep{{
		Registries: []build.RegistryTarget{{
			URL:       "local",
			Path:      "prplanit/example",
			Provider:  "local",
			Retention: config.RetentionPolicy{KeepLast: 5},
		}},
	}}}

	cases := []struct {
		name               string
		daemon             bool
		wantSkip, wantCall bool
	}{
		{"daemon: local client runs", true, false, true},
		{"daemonless: skipped, no client call", false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			saved, savedClient := dockerDaemonAvailable, newRegistryClient
			t.Cleanup(func() { dockerDaemonAvailable, newRegistryClient = saved, savedClient })
			dockerDaemonAvailable = func() bool { return c.daemon }
			newRegistryClient = func(string, string, string) (registry.Registry, error) {
				return nil, errors.New("stand-in client: no daemon is touched")
			}

			var buf bytes.Buffer
			RunRetentionSection(context.Background(), &buf, false, false, plan)
			out := buf.String()
			if got := strings.Contains(out, "skipped prplanit/example: no Docker daemon"); got != c.wantSkip {
				t.Errorf("skip line present = %v, want %v\n%s", got, c.wantSkip, out)
			}
			if got := strings.Contains(out, "ERROR: local/prplanit/example"); got != c.wantCall {
				t.Errorf("local client called = %v, want %v\n%s", got, c.wantCall, out)
			}
		})
	}
}
