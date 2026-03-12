package modules

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("yaml", func() lint.Module { return &yamlModule{} })
}

type yamlModule struct{}

func (m *yamlModule) Name() string        { return "yaml" }
func (m *yamlModule) DefaultEnabled() bool { return true }
func (m *yamlModule) AutoDetect() []string { return []string{"*.yml", "*.yaml"} }

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

	var findings []lint.Finding

	// Parse YAML — checks syntax
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc any
		err := decoder.Decode(&doc)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			findings = append(findings, lint.Finding{
				File:     file.Path,
				Line:     1,
				Module:   m.Name(),
				Severity: lint.SeverityCritical,
				Message:  fmt.Sprintf("YAML parse error: %v", err),
			})
			break
		}
	}

	// Check for duplicate keys
	dupFindings := m.checkDuplicateKeys(file, data)
	findings = append(findings, dupFindings...)

	return findings, nil
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
