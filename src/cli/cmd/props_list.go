package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"github.com/sofmeright/stagefreight/src/props"
)

var propsListCategory string

var propsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available prop types",
	Long: `List all registered prop types, grouped by category.

Use --category to filter to a specific category.`,
	RunE: runPropsList,
}

func init() {
	propsListCmd.Flags().StringVar(&propsListCategory, "category", "", "filter by category")
	propsCmd.AddCommand(propsListCmd)
}

func runPropsList(cmd *cobra.Command, args []string) error {
	defs := props.List(propsListCategory)
	if len(defs) == 0 {
		if propsListCategory != "" {
			return fmt.Errorf("no prop types in category %q", propsListCategory)
		}
		return fmt.Errorf("no prop types registered")
	}

	// Group by category.
	groups := map[string][]props.Definition{}
	for _, d := range defs {
		groups[d.Category] = append(groups[d.Category], d)
	}

	// Sort category names.
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
