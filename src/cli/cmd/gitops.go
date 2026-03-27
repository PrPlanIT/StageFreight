package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/gitops"
)

var gitopsCmd = &cobra.Command{
	Use:   "gitops",
	Short: "GitOps intelligence — inspect, impact, reconcile",
}

var gitopsInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Discover and display the Flux dependency graph",
	Long: `Walk the repository and discover all Flux Kustomization objects.
Display the dependency graph, paths, orphans, and bootstrap state.

No configuration needed — everything is derived from actual manifests.`,
	RunE: runGitopsInspect,
}

func init() {
	gitopsCmd.AddCommand(gitopsInspectCmd)
	rootCmd.AddCommand(gitopsCmd)
}

func runGitopsInspect(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	graph, err := gitops.DiscoverFluxGraph(rootDir)
	if err != nil {
		return fmt.Errorf("discovering flux graph: %w", err)
	}

	if len(graph.Kustomizations) == 0 {
		fmt.Println("  No Flux Kustomizations discovered.")
		return nil
	}

	// Collect and sort for deterministic output
	var keys []gitops.KustomizationKey
	for k := range graph.Kustomizations {
		keys = append(keys, k)
	}
	gitops.SortKeys(keys)

	fmt.Printf("Kustomizations: %d\n\n", len(keys))

	for _, key := range keys {
		node := graph.Kustomizations[key]
		fmt.Printf("  %s\n", key)
		if node.Path != "" {
			fmt.Printf("    path: %s\n", node.Path)
		}
		if node.SourceRef != "" {
			fmt.Printf("    source: %s\n", node.SourceRef)
		}
		if len(node.DependsOn) > 0 {
			deps := make([]string, len(node.DependsOn))
			for i, d := range node.DependsOn {
				deps[i] = d.String()
			}
			fmt.Printf("    dependsOn: [%s]\n", strings.Join(deps, ", "))
		}
		// Show reverse deps (dependents)
		if revDeps := graph.ReverseDeps[key]; len(revDeps) > 0 {
			deps := make([]string, len(revDeps))
			for i, d := range revDeps {
				deps[i] = d.String()
			}
			fmt.Printf("    dependents: [%s]\n", strings.Join(deps, ", "))
		}
		fmt.Println()
	}

	// Duplicate path detection
	dupes := gitops.DuplicatePaths(graph)
	if len(dupes) > 0 {
		fmt.Println("Warnings:")
		for path, owners := range dupes {
			names := make([]string, len(owners))
			for i, o := range owners {
				names[i] = o.String()
			}
			fmt.Printf("  duplicate path owners: %s → %s\n", path, strings.Join(names, ", "))
		}
		fmt.Println()
	}

	// Orphans
	orphans := gitops.Orphans(graph)
	if len(orphans) > 0 {
		fmt.Println("Orphans (no deps, no dependents):")
		for _, o := range orphans {
			fmt.Printf("  %s\n", o)
		}
		fmt.Println()
	}

	// Bootstrap
	bootstrap := gitops.DetectBootstrapRequired(graph)
	if bootstrap.Required {
		fmt.Printf("Bootstrap: REQUIRED — %s\n", bootstrap.Reason)
	} else {
		fmt.Println("Bootstrap: not required")
	}

	return nil
}
