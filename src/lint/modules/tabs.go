package modules

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("tabs", func() lint.Module { return &tabsModule{} })
}

type tabsModule struct{}

func (m *tabsModule) Name() string        { return "tabs" }
func (m *tabsModule) DefaultEnabled() bool { return true }
func (m *tabsModule) AutoDetect() []string { return nil }

// yamlTemplateSuffixes lists compound extensions where the inner layer is YAML.
// Only high-confidence YAML-template conventions are included.
// Bare .tpl is intentionally omitted — it's ambiguous (Helm uses it for non-YAML too).
// Compound .yaml.tpl / .yml.tpl are included — the inner YAML extension makes them unambiguous.
var yamlTemplateSuffixes = []string{
	".yaml.gotmpl", ".yml.gotmpl",
	".yaml.tmpl", ".yml.tmpl",
	".yaml.tpl", ".yml.tpl",
	".yaml.j2", ".yml.j2",
}

func (m *tabsModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	if !m.tabsForbidden(file.Path) {
		return nil, nil
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

		// Only flag leading tabs (indentation), not tabs inside content
		if strings.HasPrefix(line, "\t") {
			findings = append(findings, lint.Finding{
				File:     file.Path,
				Line:     lineNum,
				Module:   m.Name(),
				Severity: lint.SeverityInfo,
				Message:  "tab indentation (spaces expected)",
			})
		}
	}

	return findings, scanner.Err()
}

// tabsForbidden returns true for file types where tab indentation breaks
// syntax or is strongly discouraged by convention.
// YAML (.yml, .yaml) and YAML templates (.yml.gotmpl, .yaml.tmpl, etc.).
func (m *tabsModule) tabsForbidden(path string) bool {
	base := filepath.Base(path)
	ext := filepath.Ext(path)

	// Direct YAML extensions
	if ext == ".yml" || ext == ".yaml" {
		return true
	}

	// YAML template files — match on full basename, not last extension
	// (e.g. "values.yaml.gotmpl" has ext=".gotmpl" but is YAML)
	for _, suffix := range yamlTemplateSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}

	return false
}
