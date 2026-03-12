package component

import (
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/registry"
)

// inputGroup is an ordered grouping of inputs for rendering.
type inputGroup struct {
	Name   string
	Desc   string
	Inputs []SpecInput
}

// GenerateDocs renders markdown documentation for one or more parsed spec files.
// Each spec file gets its own section with grouped input tables.
func GenerateDocs(specs []*SpecFile) string {
	var b strings.Builder

	for i, spec := range specs {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		b.WriteString(fmt.Sprintf("## `%s`\n\n", spec.Name))
		b.WriteString(renderInputs(spec.Inputs))
	}

	return b.String()
}

// renderInputs generates grouped markdown tables for a set of inputs.
func renderInputs(inputs []SpecInput) string {
	if len(inputs) == 0 {
		return "_No inputs defined._\n"
	}

	groups := groupInputs(inputs)

	var b strings.Builder
	for _, g := range groups {
		b.WriteString(fmt.Sprintf("### %s\n", g.Name))
		if g.Desc != "" {
			b.WriteString(g.Desc + "\n")
		}
		b.WriteString("| Name | Required | Default | Description |\n")
		b.WriteString("|------|----------|---------|-------------|\n")

		for _, inp := range g.Inputs {
			required := "\u274c" // cross mark (not required)
			if inp.Required {
				required = "\u2705" // check mark
			}

			def := inp.Default
			if def == "" {
				def = "-"
			} else {
				def = "`" + def + "`"
			}

			desc := inp.Description
			// Escape pipes in description for markdown tables.
			desc = strings.ReplaceAll(desc, "|", "\\|")

			b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s |\n", inp.Name, required, def, desc))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// groupInputs collects inputs into ordered groups. Inputs without a group
// are placed in an "Ungrouped" bucket at the end.
func groupInputs(inputs []SpecInput) []inputGroup {
	var groups []inputGroup
	seen := make(map[string]int) // group name → index in groups slice

	for _, inp := range inputs {
		name := inp.Group
		if name == "" {
			name = "Ungrouped"
		}

		idx, ok := seen[name]
		if !ok {
			idx = len(groups)
			seen[name] = idx
			groups = append(groups, inputGroup{
				Name: name,
				Desc: inp.GroupDesc,
			})
		}
		groups[idx].Inputs = append(groups[idx].Inputs, inp)
	}

	return groups
}

// InjectIntoReadme reads a README file and replaces the named sf:section
// with the provided content. Returns the updated README text.
func InjectIntoReadme(readmePath, section, content string) (string, error) {
	data, err := os.ReadFile(readmePath)
	if err != nil {
		return "", fmt.Errorf("reading README: %w", err)
	}

	original := string(data)
	updated, found := registry.ReplaceSection(original, section, content)
	if !found {
		return "", fmt.Errorf("section %q (markers <!-- sf:%s:start --> / <!-- sf:%s:end -->) not found in %s",
			section, section, section, readmePath)
	}
	return updated, nil
}
