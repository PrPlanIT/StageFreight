package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/opentofu"
)

func init() {
	lint.Register("opentofu", func() lint.Module {
		return &openTofuModule{fmt: opentofu.FmtCheck, validate: opentofu.Validate}
	})
}

// openTofuModule runs the backend's fmt -check over every OpenTofu/Terraform file in the
// lint set, and validate over each DECLARED root (validate needs a root; undeclared roots
// stay quiet until declared).
//
// Severity posture mirrors the ansible module's graduated v1: validate errors map to
// Warning and everything else to Info, so adopting the module never hard-fails an
// audition. Tightening is a future knob.
type openTofuModule struct {
	cfg      config.OpenTofuConfig
	want     map[string]config.ToolConstraint
	fmt      func(ctx context.Context, tool string, want map[string]config.ToolConstraint, rootDir string, files []string) ([]string, error)
	validate func(ctx context.Context, tool string, want map[string]config.ToolConstraint, rootDir, rootPath string) ([]byte, error)
}

func (m *openTofuModule) Name() string         { return "opentofu" }
func (m *openTofuModule) DefaultEnabled() bool { return true }
func (m *openTofuModule) AutoDetect() []string { return nil }

func (m *openTofuModule) SetOpenTofuConfig(cfg config.OpenTofuConfig) { m.cfg = cfg }

func (m *openTofuModule) SetToolchainDesired(desired map[string]config.ToolConstraint) {
	m.want = desired
}

// Check is the mis-dispatch guard — the engine routes whole-repo modules to CheckAll.
func (m *openTofuModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	return nil, fmt.Errorf("opentofu is a whole-repo module; the engine must call CheckAll, not Check")
}

// CheckAll checks formatting and validates the declared roots. No declared roots → inert.
// When a check cannot run, one Warning names why.
func (m *openTofuModule) CheckAll(ctx context.Context, files []lint.FileInfo) ([]lint.Finding, error) {
	if len(m.cfg.Roots) == 0 {
		return nil, nil
	}
	rootDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	tool := m.cfg.Backend

	var findings []lint.Finding
	var hcl []string
	for _, f := range files {
		if isOpenTofuSource(f.Path) {
			hcl = append(hcl, f.Path)
		}
	}
	if len(hcl) > 0 {
		unformatted, err := m.fmt(ctx, tool, m.want, rootDir, hcl)
		if err != nil {
			findings = append(findings, m.notRun("fmt", err))
		}
		for _, f := range unformatted {
			findings = append(findings, lint.Finding{
				File: f, Line: 1, Module: m.Name(), Severity: lint.SeverityInfo,
				Message: fmt.Sprintf("not in canonical format — run `%s fmt`", binaryName(tool)),
			})
		}
	}

	for _, r := range m.cfg.Roots {
		out, err := m.validate(ctx, tool, m.want, rootDir, r.Path)
		if err != nil {
			findings = append(findings, m.notRun("validate "+r.ID, err))
			continue
		}
		fs, err := parseValidateJSON(r.Path, out)
		if err != nil {
			findings = append(findings, m.notRun("validate "+r.ID, err))
			continue
		}
		findings = append(findings, fs...)
	}
	return findings, nil
}

func (m *openTofuModule) notRun(check string, err error) lint.Finding {
	return lint.Finding{
		File: ".stagefreight.yml", Line: 1, Module: m.Name(), Severity: lint.SeverityWarning,
		Message: fmt.Sprintf("%s %s did not run: %v", binaryName(m.cfg.Backend), check, err),
	}
}

// isOpenTofuSource reports whether fmt formats the file.
func isOpenTofuSource(path string) bool {
	return strings.HasSuffix(path, ".tf") || strings.HasSuffix(path, ".tfvars") || strings.HasSuffix(path, ".tftest.hcl")
}

func binaryName(tool string) string {
	if tool == config.OpenTofuBackendTerraform {
		return "terraform"
	}
	return "tofu"
}

// parseValidateJSON maps `validate -json` diagnostics onto findings: error → Warning,
// warning → Info. Diagnostic filenames are relative to the root.
func parseValidateJSON(rootPath string, out []byte) ([]lint.Finding, error) {
	var doc struct {
		Diagnostics []struct {
			Severity string `json:"severity"`
			Summary  string `json:"summary"`
			Detail   string `json:"detail"`
			Range    *struct {
				Filename string `json:"filename"`
				Start    struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parsing validate output: %w", err)
	}
	var findings []lint.Finding
	for _, d := range doc.Diagnostics {
		sev := lint.SeverityInfo
		if d.Severity == "error" {
			sev = lint.SeverityWarning
		}
		file, line := rootPath, 1
		if d.Range != nil {
			file = filepath.Join(rootPath, d.Range.Filename)
			if d.Range.Start.Line > 0 {
				line = d.Range.Start.Line
			}
		}
		msg := d.Summary
		if d.Detail != "" {
			msg += ": " + d.Detail
		}
		findings = append(findings, lint.Finding{File: file, Line: line, Module: "opentofu", Severity: sev, Message: msg})
	}
	return findings, nil
}
