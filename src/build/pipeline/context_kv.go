package pipeline

import (
	"os"

	"github.com/PrPlanIT/StageFreight/src/output"
)

// CIContextKV returns the code identity KV pairs for the ContextBlock.
// Exactly two items: Commit SHA and Branch or Tag.
// Pipeline, Runner, Platforms, and Registries are NOT here — they belong
// to their owning domain panels (DomainExecution, DomainPlan, DomainResult).
func CIContextKV() []output.DomainKV {
	var kv []output.DomainKV

	if sha := os.Getenv("CI_COMMIT_SHORT_SHA"); sha != "" {
		kv = append(kv, output.CodeKV("Commit", sha))
	} else if sha := os.Getenv("CI_COMMIT_SHA"); sha != "" && len(sha) >= 8 {
		kv = append(kv, output.CodeKV("Commit", sha[:8]))
	}

	if branch := os.Getenv("CI_COMMIT_BRANCH"); branch != "" {
		kv = append(kv, output.CodeKV("Branch", branch))
	} else if tag := os.Getenv("CI_COMMIT_TAG"); tag != "" {
		kv = append(kv, output.CodeKV("Tag", tag))
	}

	return kv
}
