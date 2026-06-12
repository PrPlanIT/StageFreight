package docker

import (
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
