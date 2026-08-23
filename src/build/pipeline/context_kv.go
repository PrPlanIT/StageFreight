package pipeline

import (
	"os"

	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/version"
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

// IdentityInfo builds the StageFreight tool identity from the orchestrator binary's
// OWN ldflags (version.Version already embeds the short build commit on dev builds).
// This is SF-only data — it deliberately does NOT read the built repo's version or the
// CI commit/branch. The repo's code identity (its commit + branch) is a separate concern
// rendered by CIContextKV in the ── Code ── block beneath the banner/stamp: the
// "StageFreight" line names the tool, the Code block names what it is operating on.
func IdentityInfo() output.BannerInfo {
	return output.NewBannerInfo(version.Version, "", "")
}
