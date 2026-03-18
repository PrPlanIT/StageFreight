package docker

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// hasRemoteRegistries returns true if the registry list has any non-local providers.
func hasRemoteRegistries(registries []build.RegistryTarget) bool {
	for _, r := range registries {
		if r.Provider != "local" {
			return true
		}
	}
	return false
}

// collectRemoteTags returns fully qualified image refs for all remote registry
// tags in load-then-push steps (single-platform, Load=true, has remote registries).
func collectRemoteTags(plan *build.BuildPlan) []string {
	var tags []string
	for _, step := range plan.Steps {
		// Only for load-then-push (single-platform loaded into daemon)
		if !step.Load || step.Push {
			continue
		}
		for _, reg := range step.Registries {
			if reg.Provider == "local" {
				continue
			}
			for _, t := range reg.Tags {
				tags = append(tags, fmt.Sprintf("%s/%s:%s", reg.URL, reg.Path, t))
			}
		}
	}
	return tags
}

func formatPlatforms(steps []build.BuildStep) string {
	if len(steps) == 0 {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	// Collect unique platforms across all steps
	seen := make(map[string]bool)
	var platforms []string
	for _, s := range steps {
		for _, p := range s.Platforms {
			if !seen[p] {
				seen[p] = true
				platforms = append(platforms, p)
			}
		}
	}
	if len(platforms) == 0 {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return strings.Join(platforms, ",")
}

