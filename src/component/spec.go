// Package component provides GitLab CI component spec parsing and
// documentation generation for the `stagefreight component` command family.
package component

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SpecInput represents a single input extracted from a GitLab CI component
// spec file, including group metadata from custom comments.
type SpecInput struct {
	Name        string // input key name
	Type        string // "string", "boolean", "number", "array" (default: "string")
	Default     string // default value; empty means required
	Description string
	Required    bool   // true if no default was specified
	Group       string // from # input_section_name- comment
	GroupDesc   string // from # input_section_desc- comment
}

// SpecFile bundles a parsed component spec with its source filename.
type SpecFile struct {
	Name   string      // display name (filename without extension)
	Path   string      // original file path
	Inputs []SpecInput // parsed inputs in order
}

// inputKeyRe matches an input key definition line (indented under spec.inputs).
var inputKeyRe = regexp.MustCompile(`^(\s{4})([a-zA-Z][a-zA-Z0-9_-]*):\s*(.*)$`)

// inputPropRe matches a property line under an input key (deeper indentation).
var inputPropRe = regexp.MustCompile(`^(\s{6,})([a-zA-Z][a-zA-Z0-9_-]*):\s*(.*)$`)

// ParseSpec reads a GitLab CI component spec YAML file and extracts inputs
// with group metadata from custom comments.
//
// The parser is line-oriented (not full YAML) to preserve comment-based group
// metadata that standard YAML parsers discard. It understands the custom
// comment conventions:
//
//	# input_section_name- Group Title
//	# input_section_desc- Group description text
func ParseSpec(path string) (*SpecFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening spec file: %w", err)
	}
	defer f.Close()

	// Derive display name from filename.
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))

	var (
		inputs       []SpecInput
		lines        []string
		inSpec       bool
		inInputs     bool
		currentGroup string
		currentDesc  string
	)

	// Read all lines.
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading spec file: %w", err)
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Detect spec: section.
		if trimmed == "spec:" {
			inSpec = true
			i++
			continue
		}

		// Detect inputs: within spec.
		if inSpec && trimmed == "inputs:" && leadingSpaces(line) >= 2 {
			inInputs = true
			i++
			continue
		}

		// Stop at document separator or non-indented key after spec.
		if inSpec && (trimmed == "---" || (len(line) > 0 && line[0] != ' ' && line[0] != '#')) {
			break
		}

		if !inInputs {
			i++
			continue
		}

		// Stop inputs section if we hit a line at spec-level indentation (2 spaces)
		// that's a new key (not a comment).
		if inInputs && leadingSpaces(line) <= 2 && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}

		// Group metadata comments.
		if strings.Contains(line, "# input_section_name-") {
			parts := strings.SplitN(line, "# input_section_name-", 2)
			if len(parts) == 2 {
				currentGroup = strings.TrimSpace(parts[1])
			}
			i++
			continue
		}
		if strings.Contains(line, "# input_section_desc-") {
			parts := strings.SplitN(line, "# input_section_desc-", 2)
			if len(parts) == 2 {
				currentDesc = strings.TrimSpace(parts[1])
			}
			i++
			continue
		}

		// Skip other comments and blank lines.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}

		// Match input key (4-space indent under inputs).
		m := inputKeyRe.FindStringSubmatch(line)
		if m == nil {
			i++
			continue
		}

		input := SpecInput{
			Name:      m[2],
			Type:      "string", // GitLab default
			Required:  true,
			Group:     currentGroup,
			GroupDesc: currentDesc,
		}

		// Check for inline value (rarely used but possible).
		inlineVal := strings.TrimSpace(m[3])
		if inlineVal != "" && !strings.HasPrefix(inlineVal, "#") {
			input.Default = unquote(inlineVal)
			input.Required = false
		}

		// Parse sub-properties (description, default, type).
		i++
		keyIndent := leadingSpaces(line)
		for i < len(lines) {
			propLine := lines[i]
			propTrimmed := strings.TrimSpace(propLine)

			// Blank lines within properties — skip.
			if propTrimmed == "" {
				i++
				continue
			}

			propIndent := leadingSpaces(propLine)
			if propIndent <= keyIndent {
				break // Back to same or outer level.
			}

			pm := inputPropRe.FindStringSubmatch(propLine)
			if pm == nil {
				// Could be array items or continuation — skip.
				i++
				continue
			}

			propKey := pm[2]
			propVal := strings.TrimSpace(pm[3])

			switch propKey {
			case "description":
				input.Description = unquote(propVal)
			case "default":
				input.Default = unquote(propVal)
				input.Required = false
			case "type":
				input.Type = unquote(propVal)
			}
			i++
		}
		// Continue without incrementing i (already advanced).

		inputs = append(inputs, input)
		continue
	}

	return &SpecFile{
		Name:   name,
		Path:   path,
		Inputs: inputs,
	}, nil
}

// leadingSpaces returns the number of leading space characters in a line.
func leadingSpaces(s string) int {
	for i, c := range s {
		if c != ' ' {
			return i
		}
	}
	return len(s)
}

// unquote strips surrounding single or double quotes from a string.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
