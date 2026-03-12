package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/props"
)

var propsCategoriesCmd = &cobra.Command{
	Use:   "categories",
	Short: "List prop categories with type counts",
	RunE:  runPropsCategories,
}

func init() {
	propsCmd.AddCommand(propsCategoriesCmd)
}

func runPropsCategories(cmd *cobra.Command, args []string) error {
	cats := props.Categories()
	if len(cats) == 0 {
		return fmt.Errorf("no categories registered")
	}

	w := os.Stdout
	for _, c := range cats {
		fmt.Fprintf(w, "%-20s %d types\n", c.Name, c.Count)
	}
	return nil
}
