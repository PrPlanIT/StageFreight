package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sofmeright/stagefreight/src/props"
)

var (
	propsRenderType   string
	propsRenderParams []string
)

var propsRenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Resolve and render a prop as markdown",
	Long: `Resolve a prop type with the given parameters and print the resulting markdown.

Example:
  stagefreight props render --type docker-pulls --param image=prplanit/stagefreight`,
	RunE: runPropsRender,
}

func init() {
	propsRenderCmd.Flags().StringVar(&propsRenderType, "type", "", "prop type ID (required)")
	propsRenderCmd.Flags().StringArrayVar(&propsRenderParams, "param", nil, "param in key=value format (repeatable)")
	_ = propsRenderCmd.MarkFlagRequired("type")
	propsCmd.AddCommand(propsRenderCmd)
}

func runPropsRender(cmd *cobra.Command, args []string) error {
	def, ok := props.Get(propsRenderType)
	if !ok {
		return fmt.Errorf("unknown prop type %q (use 'props list' to see available types)", propsRenderType)
	}

	// Parse key=value params.
	params := make(map[string]string)
	for _, p := range propsRenderParams {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid param format %q (expected key=value)", p)
		}
		params[parts[0]] = parts[1]
	}

	resolved, err := props.ResolveDefinition(def, params, props.RenderOptions{})
	if err != nil {
		return err
	}

	md := props.FormatMarkdown(resolved, props.VariantClassic)
	fmt.Println(md)
	return nil
}
