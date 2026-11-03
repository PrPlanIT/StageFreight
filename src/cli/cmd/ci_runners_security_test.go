package cmd

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/runner"
)

// The security scan's substrate check passes with or without a Docker daemon and fails
// on a read-only workspace or a full disk.
func TestSecuritySubstrate_DockerAndDaemonlessRunners(t *testing.T) {
	healthy := runner.SubstrateFacts{
		WorkdirReadable: true, StagefreightWritable: true,
		DiskFreeMB: 10000, TmpFreeMB: 5000, MemAvailableMB: -1, MemLimitMB: -1, InodePctFree: -1,
	}
	cases := []struct {
		name string
		edit func(*runner.SubstrateFacts)
		want runner.HealthGrade
	}{
		{"docker runner", func(f *runner.SubstrateFacts) {
			f.DockerAvailable, f.DockerSocket, f.BuildKitAvailable = true, "/var/run/docker.sock", true
		}, runner.Healthy},
		{"daemonless k8s runner with remote BuildKit", func(f *runner.SubstrateFacts) {
			f.BuildKitHost, f.BuildKitAvailable, f.BuildxAvailable = "tcp://buildkitd:1234", true, true
		}, runner.Healthy},
		{"daemonless runner with no build engine at all", func(f *runner.SubstrateFacts) {}, runner.Healthy},
		{"read-only workspace fails on either kind", func(f *runner.SubstrateFacts) {
			f.StagefreightWritable = false
		}, runner.Unhealthy},
		{"full disk fails on either kind", func(f *runner.SubstrateFacts) {
			f.DiskFreeMB = 100
		}, runner.Unhealthy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := healthy
			c.edit(&f)
			if _, got := runner.EvaluateHealth(f, securitySubstrate); got != c.want {
				t.Fatalf("grade = %q, want %q", got, c.want)
			}
		})
	}
}

// A pipeline with nothing to scan records security not_applicable without needing a Docker
// daemon, and publish is then authorized — the docs-repo case (no builds configured).
func TestSecurityRunner_NothingToScanSkipsAndAuthorizesPublish(t *testing.T) {
	cases := []struct {
		name  string
		build cistate.SubsystemState
	}{
		{"no builds configured", cistate.SubsystemState{
			Name: "build", Attempted: true, Outcome: "not_applicable", Reason: "no builds configured",
		}},
		{"build produced no images", cistate.SubsystemState{
			Name: "build", Attempted: true, Outcome: "success",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			seedSubsystems(t, dir, tc.build)
			ciCtx := &ci.CIContext{Provider: "gitlab", Workspace: dir, Branch: "main"}
			appCfg := &config.Config{}
			appCfg.Security.Enabled = true

			if err := securityRunner(context.Background(), appCfg, ciCtx, ci.RunOptions{}); err != nil {
				t.Fatalf("securityRunner: %v", err)
			}
			st, err := cistate.ReadState(dir)
			if err != nil {
				t.Fatalf("read state: %v", err)
			}
			sec := st.GetSubsystem("security")
			if sec == nil || sec.Outcome != "not_applicable" {
				t.Fatalf("security outcome = %+v, want not_applicable", sec)
			}
			if err := authorizePhase(ciCtx, dir, "publish"); err != nil {
				t.Fatalf("publish should be authorized: %v", err)
			}
		})
	}
}

// Reconcile needs a Docker daemon only for ansible converge without a native ansible-playbook.
func TestReconcileSubstrate_DockerAndDaemonlessRunners(t *testing.T) {
	daemonless := runner.SubstrateFacts{
		WorkdirReadable: true, StagefreightWritable: true,
		DiskFreeMB: 10000, TmpFreeMB: 5000, MemAvailableMB: -1, MemLimitMB: -1, InodePctFree: -1,
	}
	docker := daemonless
	docker.DockerAvailable, docker.DockerSocket = true, "/var/run/docker.sock"
	yes := func() bool { return true }
	no := func() bool { return false }

	cases := []struct {
		name       string
		facts      runner.SubstrateFacts
		hasAnsible bool
		needsDock  func() bool
		want       runner.HealthGrade
	}{
		{"gitops only, daemonless", daemonless, false, yes, runner.Healthy},
		{"ansible in the job image, daemonless", daemonless, true, no, runner.Healthy},
		{"ansible only in the execution image, daemonless: refused", daemonless, true, yes, runner.Unhealthy},
		{"ansible only in the execution image, docker runner", docker, true, yes, runner.Healthy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got := runner.EvaluateHealth(c.facts, reconcileSubstrate(c.hasAnsible, c.needsDock)); got != c.want {
				t.Fatalf("grade = %q, want %q", got, c.want)
			}
		})
	}
}
