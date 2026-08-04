package modules

import (
	"context"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

const ansibleLintFixture = `[
  {"check_name": "risky-file-permissions", "severity": "blocker",
   "description": "File permissions unset or incorrect",
   "location": {"path": "ansible/k8s/provision-hosts.yml", "lines": {"begin": 42}}},
  {"check_name": "name[missing]", "severity": "minor",
   "description": "All tasks should be named",
   "location": {"path": "ansible/k8s/provision-hosts.yml", "lines": {"begin": 7}}}
]`

// TestAnsibleLint_GraduatedSeverity pins the v1 posture: blocker/critical map
// to Warning (never hard-fails an audition), everything else to Info.
func TestAnsibleLint_GraduatedSeverity(t *testing.T) {
	findings, err := parseAnsibleLintJSON([]byte(ansibleLintFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(findings))
	}
	if findings[0].Severity != lint.SeverityWarning || findings[0].Line != 42 {
		t.Errorf("blocker: %+v", findings[0])
	}
	if findings[1].Severity != lint.SeverityInfo {
		t.Errorf("minor: %+v", findings[1])
	}
	for _, f := range findings {
		if f.Severity == lint.SeverityCritical {
			t.Errorf("graduated posture must not emit Critical: %+v", f)
		}
	}
}

// TestAnsibleLint_InertWithoutLibrary pins presence gating: no declared
// playbooks → the module contributes nothing (legacy ansible trees stay quiet
// until their plays are declared).
func TestAnsibleLint_InertWithoutLibrary(t *testing.T) {
	m := &ansibleModule{run: func(context.Context, string, string, []string) ([]byte, error) {
		t.Fatal("runner must not execute without declared playbooks")
		return nil, nil
	}}
	findings, err := m.CheckAll(context.Background(), nil)
	if err != nil || findings != nil {
		t.Fatalf("inert module: findings=%v err=%v", findings, err)
	}
}

// TestAnsibleLint_SubstrateDegradesToInfo pins the fail-soft contract: a
// missing substrate is one visible Info finding, never an audition failure —
// hard substrate errors belong to the converge phase.
func TestAnsibleLint_SubstrateDegradesToInfo(t *testing.T) {
	m := &ansibleModule{run: func(context.Context, string, string, []string) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}}
	m.SetAnsibleConfig(config.AnsibleConfig{
		Image:     "docker.io/hlhd/ansible:v2.20.4",
		Playbooks: config.OrderedAnsiblePlaybooks{{ID: "p", Path: "p.yml", Converge: true}},
	})
	findings, err := m.CheckAll(context.Background(), nil)
	if err != nil {
		t.Fatalf("substrate failure must not error the lint: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != lint.SeverityInfo || !strings.Contains(findings[0].Message, "skipped") {
		t.Fatalf("want one Info skip finding, got %+v", findings)
	}
}

// TestAnsibleLint_LintsDeclaredPlaysOnly pins the scoping: the runner receives
// exactly the library's paths — runbooks included (they deserve lint even
// though CI never executes them).
func TestAnsibleLint_LintsDeclaredPlaysOnly(t *testing.T) {
	var got []string
	m := &ansibleModule{run: func(_ context.Context, image, _ string, plays []string) ([]byte, error) {
		if image != "docker.io/hlhd/ansible:v2.20.4" {
			t.Errorf("image = %q", image)
		}
		got = plays
		return []byte("[]"), nil
	}}
	m.SetAnsibleConfig(config.AnsibleConfig{
		Image: "docker.io/hlhd/ansible:v2.20.4",
		Playbooks: config.OrderedAnsiblePlaybooks{
			{ID: "converge", Path: "ansible/a.yml", Converge: true},
			{ID: "runbook", Path: "ansible/b.yml", Converge: false},
		},
	})
	if _, err := m.CheckAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "ansible/a.yml" || got[1] != "ansible/b.yml" {
		t.Errorf("linted plays = %v", got)
	}
}
