package modules

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("direct-output", func() lint.Module {
		return &directOutputModule{cfg: defaultDirectOutputConfig(), sev: lint.SeverityWarning}
	})
}

type directOutputConfig struct {
	Allowlist []string `json:"allowlist"`
	Severity  string   `json:"severity"`
}

func defaultDirectOutputConfig() directOutputConfig {
	return directOutputConfig{
		Allowlist: []string{
			"src/cli/**",
			"cmd/**",
			"src/output/**",
			"src/diag/**",
		},
	}
}

type directOutputModule struct {
	cfg directOutputConfig
	sev lint.Severity
}

func (m *directOutputModule) Name() string         { return "direct-output" }
func (m *directOutputModule) DefaultEnabled() bool { return true }
func (m *directOutputModule) AutoDetect() []string { return []string{"**/*.go"} }

// Configure implements lint.ConfigurableModule.
func (m *directOutputModule) Configure(opts map[string]any) error {
	cfg := defaultDirectOutputConfig()
	if len(opts) != 0 {
		b, err := json.Marshal(opts)
		if err != nil {
			return fmt.Errorf("direct-output: marshal options: %w", err)
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("direct-output: unmarshal options: %w", err)
		}
	}
	m.cfg = cfg
	sev, err := parseSeverity(cfg.Severity)
	if err != nil {
		return fmt.Errorf("direct-output: %w", err)
	}
	m.sev = sev
	return nil
}

func parseSeverity(s string) (lint.Severity, error) {
	switch s {
	case "warning", "":
		return lint.SeverityWarning, nil
	case "critical":
		return lint.SeverityCritical, nil
	case "info":
		return lint.SeverityInfo, nil
	default:
		return 0, fmt.Errorf("invalid severity %q (must be \"warning\", \"critical\", or \"info\")", s)
	}
}

var directOutputRe = regexp.MustCompile(
	`fmt\.(Print|Println|Printf)\(` +
		`|fmt\.(Fprint|Fprintln|Fprintf)\(os\.(Stdout|Stderr)` +
		`|log\.(Print|Println|Printf|Fatal|Fatalln|Fatalf)\(` +
		`|os\.(Stdout|Stderr)\.(Write|WriteString)\(` +
		`|(?:^|[^.a-zA-Z_])(print|println)\(`,
)

func (m *directOutputModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	// Skip allowlisted packages.
	for _, pattern := range m.cfg.Allowlist {
		if lint.MatchGlob(pattern, file.Path) {
			return nil, nil
		}
	}

	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var findings []lint.Finding
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if directOutputRe.MatchString(line) {
			findings = append(findings, lint.Finding{
				File:     file.Path,
				Line:     lineNum,
				Module:   m.Name(),
				Severity: m.sev,
				Message:  "direct output call outside presentation layer; use diag.Warn/diag.Debug or return structured data",
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return findings, nil
}
