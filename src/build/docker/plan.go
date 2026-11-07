package docker

import (
	"github.com/PrPlanIT/StageFreight/src/build"
)

// applyImageBuildStrategy sets each image step's Load/Push from platform shape +
// flags. Invariant: under transport, publish is the sole distributor — steps never
// push, and a daemon copy is loaded only when the bytes aren't already CAS-retained
// (retainViaCAS), with or without an eligible registry. Without transport, a step with
// registries pushes in-build and a registry-less one loads into the daemon.
func applyImageBuildStrategy(plan *build.BuildPlan, transport, local, retainViaCAS bool) {
	for i := range plan.Steps {
		step := &plan.Steps[i]
		switch {
		case local:
			step.Load = true
			step.Push = false
			if len(step.Tags) == 0 {
				step.Tags = []string{"stagefreight:dev"}
			}
		case transport:
			step.Push = false
			if len(step.Registries) == 0 && len(step.Tags) == 0 {
				step.Tags = []string{"stagefreight:dev"}
			}
			// Load a single-platform daemon copy ONLY when the artifact is not
			// retained via the CAS layout — there the daemon copy IS the retained
			// bytes. With CAS retention active (multi-platform's path), nothing
			// reads the daemon copy (digest=metadata, scan=layout, publish=cache),
			// so loading it just creates a second, unconsumed copy.
			if !IsMultiPlatform(*step) && !retainViaCAS {
				step.Load = true
			}
		case len(step.Registries) == 0:
			step.Load = true
			if len(step.Tags) == 0 {
				step.Tags = []string{"stagefreight:dev"}
			}
		default:
			step.Push = true
		}
		step.CacheTo = exportableCacheTo(step.CacheTo, local)
	}
}

// exportableCacheTo keeps every cache export a CI build can write; a local build has no
// registry credentials, so it keeps local exports alone.
func exportableCacheTo(refs []build.CacheRef, local bool) []build.CacheRef {
	var out []build.CacheRef
	for _, r := range refs {
		if r.Type == "registry" && local {
			continue
		}
		out = append(out, r)
	}
	return out
}

// daemonLoadSteps returns the first tag of each image step that loads into a
// Docker daemon (--load). A runner without a daemon, building on a remote
// buildkitd, cannot run those; transport with content-store retention has none.
func daemonLoadSteps(plan *build.BuildPlan) []string {
	var tags []string
	for _, step := range plan.Steps {
		if !step.Load {
			continue
		}
		tag := "(untagged)"
		if len(step.Tags) > 0 {
			tag = step.Tags[0]
		}
		tags = append(tags, tag)
	}
	return tags
}
