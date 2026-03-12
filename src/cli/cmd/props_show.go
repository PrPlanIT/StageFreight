package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/props"
)

var propsShowCmd = &cobra.Command{
	Use:   "show <type>",
	Short: "Show details for a prop type",
	Long:  "Show description, parameters, and example config for a prop type.",
	Args:  cobra.ExactArgs(1),
	RunE:  runPropsShow,
}

func init() {
	propsCmd.AddCommand(propsShowCmd)
}

func runPropsShow(cmd *cobra.Command, args []string) error {
	typeID := args[0]
	def, ok := props.Get(typeID)
	if !ok {
		return fmt.Errorf("unknown prop type %q (use 'props list' to see available types)", typeID)
	}

	w := os.Stdout
	schema := def.Resolver.Schema()

	fmt.Fprintf(w, "Type:        %s\n", def.ID)
	fmt.Fprintf(w, "Format:      %s\n", def.Format)
	fmt.Fprintf(w, "Category:    %s\n", def.Category)
	fmt.Fprintf(w, "Provider:    %s\n", def.Provider)
	fmt.Fprintf(w, "Description: %s\n", def.Description)
	fmt.Fprintln(w)

	// Parameters
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

	// Example config YAML
	fmt.Fprintln(w, "Example config:")
	fmt.Fprintln(w, "  - kind: props")
	fmt.Fprintf(w, "    type: %s\n", def.ID)
	if len(schema.Example) > 0 {
		fmt.Fprintln(w, "    params:")
		for _, p := range schema.Params {
			if v, ok := schema.Example[p.Name]; ok {
				fmt.Fprintf(w, "      %s: %s\n", p.Name, yamlQuoteIfNeeded(v))
			}
		}
	}

	// Example resolved output
	resolved, err := props.ResolveDefinition(def, schema.Example, props.RenderOptions{})
	if err == nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Resolved:")
		fmt.Fprintf(w, "  Image: %s\n", resolved.ImageURL)
		if resolved.LinkURL != "" {
			fmt.Fprintf(w, "  Link:  %s\n", resolved.LinkURL)
		}
		fmt.Fprintf(w, "  Alt:   %s\n", resolved.Alt)
		md := props.FormatMarkdown(resolved, props.VariantClassic)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Markdown:")
		fmt.Fprintf(w, "  %s\n", md)
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
