package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/component"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

var (
	cdSpecs      []string
	cdOutputFile string
	cdReadme     string
)

var componentDocsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Generate input documentation from component spec files",
	Long: `Parse GitLab CI component spec files and generate markdown
documentation tables for their inputs.

Supports custom group metadata via comments:
  # input_section_name- Group Title
  # input_section_desc- Group description text

Output modes:
  - Default: print markdown to stdout
  - --output: write markdown to a file
  - --readme: inject docs between markers in target file`,
	RunE: runComponentDocs,
}

func init() {
	componentDocsCmd.Flags().StringSliceVar(&cdSpecs, "spec", nil, "component spec file(s) to parse (repeatable)")
	componentDocsCmd.Flags().StringVarP(&cdOutputFile, "output", "o", "", "write docs to file")
	componentDocsCmd.Flags().StringVar(&cdReadme, "readme", "", "inject docs between <!-- sf:<name> --> markers in target file (section name from narrator config)")

	componentCmd.AddCommand(componentDocsCmd)
}

func runComponentDocs(cmd *cobra.Command, args []string) error {
	// Resolve spec files: CLI flags → gitlab-component target → error.
	specFiles := cdSpecs
	if len(specFiles) == 0 {
		for _, t := range cfg.Targets {
			if t.Kind == "gitlab-component" && len(t.SpecFiles) > 0 {
				specFiles = t.SpecFiles
				break
			}
		}
	}
	if len(specFiles) == 0 {
		return fmt.Errorf("no spec files specified; use --spec or configure a gitlab-component target in .stagefreight.yml")
	}

	if cdOutputFile != "" && cdReadme != "" {
		return fmt.Errorf("--output cannot be combined with --readme")
	}

	start := time.Now()

	// Parse all spec files.
	var specs []*component.SpecFile
	totalInputs := 0
	for _, path := range specFiles {
		spec, err := component.ParseSpec(path)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		specs = append(specs, spec)
		totalInputs += len(spec.Inputs)
		if verbose {
			fmt.Fprintf(os.Stderr, "  parsed %s: %d inputs\n", path, len(spec.Inputs))
		}
	}

	// Generate markdown.
	docs := component.GenerateDocs(specs)

	// Resolve target and section name once from narrator config.
	cfgTarget, cfgSection := resolveComponentTarget()

	// Resolve target file: --readme CLI flag → narrator config lookup
	target := cdReadme
	if target == "" {
		target = cfgTarget
	}

	// Output mode: --output file (raw write)
	if cdOutputFile != "" {
		if err := os.WriteFile(cdOutputFile, []byte(docs), 0o644); err != nil {
			return fmt.Errorf("writing docs: %w", err)
		}
		elapsed := time.Since(start)
		useColor := output.UseColor()
		w := os.Stdout
		sec := output.NewSection(w, "Component Docs", elapsed, useColor)
		sec.Row("%-16s%d inputs from %d spec(s)", "parsed", totalInputs, len(specs))
		output.RowStatus(sec, cdOutputFile, "", "success", useColor)
		sec.Close()
		return nil
	}

	// Output mode: --readme (marker injection)
	if target != "" {
		sectionName := cfgSection
		if sectionName == "" {
			return fmt.Errorf("cannot determine section name: no component item found in narrator config")
		}

		existing, err := os.ReadFile(target)
		if err != nil {
			return fmt.Errorf("reading target file %s: %w", target, err)
		}

		updated, found := registry.ReplaceSection(string(existing), sectionName, strings.TrimSpace(docs))
		if !found {
			return fmt.Errorf("section markers <!-- sf:%s:start --> not found in %s", sectionName, target)
		}

		// Normalize line endings and trailing newline for comparison.
		existingNorm := normalizeContent(string(existing))
		updatedNorm := normalizeContent(updated)
		unchanged := existingNorm == updatedNorm

		if !unchanged {
			if err := os.WriteFile(target, []byte(updated), 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", target, err)
			}
		}

		elapsed := time.Since(start)
		useColor := output.UseColor()
		w := os.Stdout
		sec := output.NewSection(w, "Component Docs", elapsed, useColor)
		sec.Row("%-16s%d inputs from %d spec(s)", "parsed", totalInputs, len(specs))
		var status, detail string
		if unchanged {
			status = "skipped"
			detail = "unchanged"
		} else {
			status = "success"
			detail = "updated"
		}
		output.RowStatus(sec, target, detail, status, useColor)
		sec.Close()
		return nil
	}

	// Default: stdout only (pipeable, no section frame).
	fmt.Print(docs)
	return nil
}

// resolveComponentTarget walks narrator config to find the file path and
// section name for the first component item.
// In v2, the section name is derived from the placement between markers.
// Returns ("", "") if not found.
func resolveComponentTarget() (filePath, sectionName string) {
	for _, f := range cfg.Narrator {
		for _, item := range f.Items {
			if item.Kind == "component" {
				// Derive section name from between markers: ["<!-- sf:X:start -->", ...]
				// Extract the section identifier from the start marker.
				if item.Placement.Between != [2]string{} {
					marker := item.Placement.Between[0]
					// Parse "<!-- sf:NAME:start -->" to extract NAME
					name := extractSectionName(marker)
					if name != "" {
						return f.File, name
					}
				}
				return f.File, ""
			}
		}
	}
	return "", ""
}

// extractSectionName extracts the section name from a marker like "<!-- sf:NAME:start -->".
func extractSectionName(marker string) string {
	// Strip comment delimiters and whitespace
	s := strings.TrimSpace(marker)
	s = strings.TrimPrefix(s, "<!--")
	s = strings.TrimSuffix(s, "-->")
	s = strings.TrimSpace(s)

	// Expect "sf:NAME:start" or "sf:NAME"
	if !strings.HasPrefix(s, "sf:") {
		return ""
	}
	s = s[3:] // strip "sf:"

	// Strip ":start" suffix if present
	s = strings.TrimSuffix(s, ":start")

	return s
}

// normalizeContent normalizes line endings (CRLF → LF) and ensures a trailing newline
// for reliable content comparison.
func normalizeContent(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}
