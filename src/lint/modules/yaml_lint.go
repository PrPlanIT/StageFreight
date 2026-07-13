package modules

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("yaml", func() lint.Module { return &yamlModule{} })
}

type yamlModule struct{}

func (m *yamlModule) Name() string         { return "yaml" }
func (m *yamlModule) DefaultEnabled() bool { return true }
func (m *yamlModule) AutoDetect() []string { return []string{"*.yml", "*.yaml"} }

// goTemplateSpan matches a single Go-template action on one line, e.g.
// `{{ .Values.x }}` or `{{- include "chart.name" . | nindent 4 -}}`.
var goTemplateSpan = regexp.MustCompile(`\{\{.*?\}\}`)

func (m *yamlModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	ext := fileExt(file.Path)
	if ext != ".yml" && ext != ".yaml" {
		return nil, nil
	}

	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}

	// Go-template files (Helm chart templates, and any other {{ }}-templated
	// manifest) are not literal YAML — a raw parse always fails on the template
	// actions. Neutralize the actions first so we validate the YAML *skeleton*
	// (structure + duplicate keys) without reporting template syntax as a defect.
	// Rendering a template correctly needs values we don't have (that is
	// `helm lint`'s job); when even the neutralized skeleton will not parse, the
	// file is too template-dynamic to validate statically, so we stay silent
	// rather than emit a false-positive parse error.
	templated := bytes.Contains(data, []byte("{{"))
	parseData := data
	if templated {
		parseData = neutralizeGoTemplate(data)
	}

	var findings []lint.Finding

	// Parse YAML — checks syntax. Templated files never emit a parse-error
	// finding: their template dynamics are not a real defect.
	decoder := yaml.NewDecoder(bytes.NewReader(parseData))
	for {
		var doc any
		err := decoder.Decode(&doc)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			if !templated {
				findings = append(findings, lint.Finding{
					File:     file.Path,
					Line:     1,
					Module:   m.Name(),
					Severity: lint.SeverityCritical,
					Message:  fmt.Sprintf("YAML parse error: %v", err),
				})
			}
			break
		}
	}

	// Duplicate-key detection runs on the (possibly neutralized) content via a
	// structural yaml.Node decode, which tolerates duplicate keys and template
	// placeholders. So genuine static duplicates are still reported even when the
	// strict decode above bailed — on a duplicate, or on a template too dynamic
	// to neutralize into parseable YAML (there it simply finds nothing).
	findings = append(findings, m.checkDuplicateKeys(file, parseData)...)

	return findings, nil
}

// neutralizeGoTemplate rewrites Go-template actions so the result parses as YAML
// for structural checks. A line that is *only* a template action — control flow
// (`{{- if }}` / `{{ end }}` / `{{ range }}`) or a whole-line render — is blanked
// (line numbers preserved), while inline value actions (`key: {{ .x }}`) become a
// unique scalar placeholder. Used only for parsing; the file on disk is untouched.
func neutralizeGoTemplate(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	var counter int
	for i, line := range lines {
		if !bytes.Contains(line, []byte("{{")) {
			continue
		}
		if len(bytes.TrimSpace(goTemplateSpan.ReplaceAll(line, nil))) == 0 {
			// Whole line is a template action — drop its content, keep the line.
			lines[i] = nil
			continue
		}
		lines[i] = goTemplateSpan.ReplaceAllFunc(line, func([]byte) []byte {
			counter++
			return []byte(fmt.Sprintf("sfTmpl%d", counter))
		})
	}
	return bytes.Join(lines, []byte("\n"))
}

func (m *yamlModule) checkDuplicateKeys(file lint.FileInfo, data []byte) []lint.Finding {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil // parse errors already caught above
	}

	var findings []lint.Finding
	m.walkNode(&node, file.Path, &findings)
	return findings
}

func (m *yamlModule) walkNode(node *yaml.Node, filePath string, findings *[]lint.Finding) {
	if node == nil {
		return
	}

	if node.Kind == yaml.DocumentNode {
		for _, child := range node.Content {
			m.walkNode(child, filePath, findings)
		}
		return
	}

	if node.Kind == yaml.MappingNode {
		seen := make(map[string]int) // key -> first line number
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			key := keyNode.Value

			if firstLine, exists := seen[key]; exists {
				*findings = append(*findings, lint.Finding{
					File:     filePath,
					Line:     keyNode.Line,
					Column:   keyNode.Column,
					Module:   "yaml",
					Severity: lint.SeverityWarning,
					Message:  fmt.Sprintf("duplicate key %q (first defined at line %d)", key, firstLine),
				})
			} else {
				seen[key] = keyNode.Line
			}

			m.walkNode(valNode, filePath, findings)
		}
		return
	}

	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			m.walkNode(child, filePath, findings)
		}
	}
}

func fileExt(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' || path[i] == '\\' {
			break
		}
	}
	return ""
}
