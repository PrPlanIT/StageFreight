package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/props"
	"github.com/spf13/cobra"
)

var stCategory string

var scribeTypesCmd = &cobra.Command{
	Use:   "types [type]",
	Short: "Browse stencil producer types, or detail one",
	Long: `Without an argument, list every available stencil producer type (the type: values
usable in the stencils library), grouped by category. With a <type> argument, show that type's
description, parameters, example config, and a rendered preview.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runScribeTypes,
}

func init() {
	scribeTypesCmd.Flags().StringVar(&stCategory, "category", "", "filter the list by category")
	scribeCmd.AddCommand(scribeTypesCmd)
}

func runScribeTypes(cmd *cobra.Command, args []string) error {
	if len(args) == 1 {
		return showScribeType(args[0])
	}
	return listScribeTypes(stCategory)
}

func listScribeTypes(category string) error {
	defs := props.List(category)
	if len(defs) == 0 {
		if category != "" {
			return fmt.Errorf("no content types in category %q", category)
		}
		return fmt.Errorf("no content types registered")
	}

	groups := map[string][]props.Definition{}
	for _, d := range defs {
		groups[d.Category] = append(groups[d.Category], d)
	}
	cats := make([]string, 0, len(groups))
	for c := range groups {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	w := os.Stdout
	for i, cat := range cats {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s:\n", cat)
		for _, d := range groups[cat] {
			fmt.Fprintf(w, "  %-28s %s  [%s]\n", d.ID, d.Description, d.Provider)
		}
	}
	return nil
}

func showScribeType(typeID string) error {
	def, ok := props.Get(typeID)
	if !ok {
		return fmt.Errorf("unknown content type %q (run 'stagefreight scribe types' to list them)", typeID)
	}

	w := os.Stdout
	schema := def.Resolver.Schema()

	fmt.Fprintf(w, "Type:        %s\n", def.ID)
	fmt.Fprintf(w, "Format:      %s\n", def.Format)
	fmt.Fprintf(w, "Category:    %s\n", def.Category)
	fmt.Fprintf(w, "Provider:    %s\n", def.Provider)
	fmt.Fprintf(w, "Description: %s\n", def.Description)
	fmt.Fprintln(w)

	if len(schema.Params) > 0 {
		fmt.Fprintln(w, "Parameters:")
		for _, p := range schema.Params {
			req := ""
			if p.Required {
				req = " (required)"
			} else if p.Default != "" {
				req = fmt.Sprintf(" (default: %s)", p.Default)
			}
			fmt.Fprintf(w, "  %-20s %s%s\n", p.Name, p.Help, req)
		}
		fmt.Fprintln(w)
	}

	// Example config — the scribe.content shape.
	fmt.Fprintln(w, "Example config:")
	fmt.Fprintln(w, "  scribe:")
	fmt.Fprintln(w, "    content:")
	fmt.Fprintf(w, "      %s:\n", def.ID)
	fmt.Fprintf(w, "        type: %s\n", def.ID)
	if len(schema.Example) > 0 {
		fmt.Fprintln(w, "        params:")
		for _, p := range schema.Params {
			if v, ok := schema.Example[p.Name]; ok {
				fmt.Fprintf(w, "          %s: %s\n", p.Name, yamlQuoteIfNeeded(v))
			}
		}
	}

	// Rendered preview from the example params (URL forms resolve without a live fetch).
	resolved, err := props.ResolveDefinition(def, schema.Example, props.RenderOptions{})
	if err == nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Preview:")
		fmt.Fprintf(w, "  Image:    %s\n", resolved.ImageURL)
		if resolved.LinkURL != "" {
			fmt.Fprintf(w, "  Link:     %s\n", resolved.LinkURL)
		}
		fmt.Fprintf(w, "  Markdown: %s\n", props.FormatMarkdown(resolved, props.VariantClassic))
	}
	return nil
}

// yamlQuoteIfNeeded wraps a value in quotes if it contains special YAML characters.
func yamlQuoteIfNeeded(s string) string {
	if strings.ContainsAny(s, ": {}[]#&*!|>'\"%@`") || s == "" {
		return fmt.Sprintf("%q", s)
	}
	return s
}
