package build

import (
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// BuildOrder computes the topological execution order for a set of builds.
// Uses Kahn's algorithm. Returns an error on cycles or missing references.
func BuildOrder(builds []config.BuildConfig) ([]config.BuildConfig, error) {
	if len(builds) == 0 {
		return nil, nil
	}

	// Index builds by ID
	byID := make(map[string]*config.BuildConfig, len(builds))
	for i := range builds {
		byID[builds[i].ID] = &builds[i]
	}

	// Compute in-degree for each build
	inDegree := make(map[string]int, len(builds))
	dependents := make(map[string][]string, len(builds)) // dep → list of builds that depend on it
	for _, b := range builds {
		if _, ok := inDegree[b.ID]; !ok {
			inDegree[b.ID] = 0
		}
		if b.DependsOn != "" {
			if _, ok := byID[b.DependsOn]; !ok {
				return nil, fmt.Errorf("build %q depends on unknown build %q", b.ID, b.DependsOn)
			}
			inDegree[b.ID]++
			dependents[b.DependsOn] = append(dependents[b.DependsOn], b.ID)
		}
	}

	// Kahn's algorithm: start with builds that have no dependencies
	var queue []string
	for _, b := range builds {
		if inDegree[b.ID] == 0 {
			queue = append(queue, b.ID)
		}
	}

	var ordered []config.BuildConfig
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		ordered = append(ordered, *byID[id])

		for _, depID := range dependents[id] {
			inDegree[depID]--
			if inDegree[depID] == 0 {
				queue = append(queue, depID)
			}
		}
	}

	if len(ordered) != len(builds) {
		// Cycle detected — find the cycle members
		var cycleIDs []string
		for id, deg := range inDegree {
			if deg > 0 {
				cycleIDs = append(cycleIDs, id)
			}
		}
		return nil, fmt.Errorf("dependency cycle detected among builds: %s", strings.Join(cycleIDs, ", "))
	}

	return ordered, nil
}

// ValidateBuildGraph performs pre-execution validation on a set of universal steps.
// Checks: no duplicate step IDs, no duplicate output paths, all input refs satisfied.
func ValidateBuildGraph(steps []UniversalStep) error {
	stepIDs := make(map[string]bool, len(steps))
	outputPaths := make(map[string]string, len(steps)) // path → step ID

	// First pass: collect all step IDs and output paths
	for _, s := range steps {
		if stepIDs[s.StepID] {
			return fmt.Errorf("duplicate step ID: %s", s.StepID)
		}
		stepIDs[s.StepID] = true

		for _, out := range s.Outputs {
			if prev, exists := outputPaths[out.Path]; exists {
				return fmt.Errorf("duplicate output path %q: step %s and %s", out.Path, prev, s.StepID)
			}
			outputPaths[out.Path] = s.StepID
		}
	}

	// Second pass: verify all input refs are satisfied by earlier steps' outputs
	producedBefore := make(map[string]bool)
	for _, s := range steps {
		for _, in := range s.Inputs {
			if !producedBefore[in.Path] {
				return fmt.Errorf("step %s requires input %q which is not produced by any earlier step", s.StepID, in.Path)
			}
		}
		// Mark this step's outputs as available for subsequent steps
		for _, out := range s.Outputs {
			producedBefore[out.Path] = true
		}
	}

	return nil
}
