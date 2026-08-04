package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("ansible", func() lint.Module { return &ansibleModule{run: runAnsibleLint} })
}

// ansibleModule wraps ansible-lint, run from the repo's ansible EXECUTION IMAGE
// (the same image that converges — so lint sees the exact collections and
// ansible-core the plays run with). Whole-repo: ansible-lint boots once over
// the DECLARED play library, not per file — undeclared ansible files are not
// linted, which keeps legacy trees quiet until their plays are declared.
//
// Severity posture is deliberately graduated for v1: ansible-lint blocker/
// critical map to Warning and everything else to Info, so adopting the module
// never hard-fails an audition on style rules. Tightening is a future knob.
type ansibleModule struct {
	cfg config.AnsibleConfig
	run func(ctx context.Context, image, rootDir string, playbooks []string) ([]byte, error)
}

func (m *ansibleModule) Name() string         { return "ansible" }
func (m *ansibleModule) DefaultEnabled() bool { return true }
func (m *ansibleModule) AutoDetect() []string { return nil }

func (m *ansibleModule) SetAnsibleConfig(cfg config.AnsibleConfig) { m.cfg = cfg }

// Check is the mis-dispatch guard — the engine routes whole-repo modules to
// CheckAll and never calls Check.
func (m *ansibleModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	return nil, fmt.Errorf("ansible is a whole-repo module; the engine must call CheckAll, not Check")
}

// CheckAll lints the declared play library in one containerized ansible-lint
// run. No declared playbooks → inert. A missing substrate (no docker, image
// unavailable) degrades to a single visible Info finding instead of failing
// the audition — the converge phase, not lint, owns hard substrate errors.
func (m *ansibleModule) CheckAll(ctx context.Context, files []lint.FileInfo) ([]lint.Finding, error) {
	if len(m.cfg.Playbooks) == 0 {
		return nil, nil
	}
	rootDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	var plays []string
	for _, p := range m.cfg.Playbooks {
		plays = append(plays, p.Path)
	}

	out, err := m.run(ctx, m.cfg.Image, rootDir, plays)
	if err != nil {
		return []lint.Finding{{
			File: ".stagefreight.yml", Line: 1, Module: m.Name(),
			Severity: lint.SeverityInfo,
			Message:  fmt.Sprintf("ansible-lint skipped: %v", err),
		}}, nil
	}
	return parseAnsibleLintJSON(out)
}

// runAnsibleLint executes ansible-lint from the execution image. Exit code 2
// means "violations found" — a successful run with content; other non-zero
// codes are execution failures.
func runAnsibleLint(ctx context.Context, image, rootDir string, playbooks []string) ([]byte, error) {
	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("docker unavailable (execution image %s)", image)
	}
	args := []string{
		"run", "--rm",
		"-v", rootDir + ":/work",
		"-w", "/work",
		"--entrypoint", "ansible-lint",
		image,
		"--format", "json", "--nocolor",
	}
	args = append(args, playbooks...)
	out, err := exec.CommandContext(ctx, dockerBin, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 2 {
			return out, nil // violations found — the output IS the result
		}
		return nil, fmt.Errorf("ansible-lint run failed: %v", err)
	}
	return out, nil
}

// ansibleLintIssue is one entry of ansible-lint's codeclimate-style JSON output.
type ansibleLintIssue struct {
	CheckName   string `json:"check_name"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Location    struct {
		Path  string `json:"path"`
		Lines struct {
			Begin int `json:"begin"`
		} `json:"lines"`
	} `json:"location"`
}

// parseAnsibleLintJSON maps ansible-lint's codeclimate severities onto SF's
// graduated posture: blocker/critical → Warning, the rest → Info.
func parseAnsibleLintJSON(out []byte) ([]lint.Finding, error) {
	var issues []ansibleLintIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing ansible-lint output: %w", err)
	}
	var findings []lint.Finding
	for _, is := range issues {
		sev := lint.SeverityInfo
		switch is.Severity {
		case "blocker", "critical":
			sev = lint.SeverityWarning
		}
		line := is.Location.Lines.Begin
		if line == 0 {
			line = 1
		}
		findings = append(findings, lint.Finding{
			File:     is.Location.Path,
			Line:     line,
			Module:   "ansible",
			Severity: sev,
			Message:  fmt.Sprintf("%s: %s", is.CheckName, is.Description),
		})
	}
	return findings, nil
}
