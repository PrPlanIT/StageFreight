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

// IdentityInfo builds the StageFreight identity (version + commit + branch) used
// by both the full banner (audition / standalone commands) and the slim
// per-phase identity line. SHA and branch come from the CI environment when
// present, falling back to the build-time commit for local/standalone runs so
// the identity is never blank. The single source keeps every phase's stamp
// consistent.
func IdentityInfo() output.BannerInfo {
	return output.NewBannerInfo(version.Version, identitySHA(), identityBranch())
}

// identitySHA returns the short commit SHA from the CI environment, falling back
// to the build-time injected commit when not running in CI.
func identitySHA() string {
	if sha := os.Getenv("CI_COMMIT_SHORT_SHA"); sha != "" {
		return sha
	}
	if sha := os.Getenv("CI_COMMIT_SHA"); len(sha) >= 8 {
		return sha[:8]
	}
	return version.Commit
}

// identityBranch returns the branch (or tag) from the CI environment, or empty
// when neither is set (standalone/local runs).
func identityBranch() string {
	if branch := os.Getenv("CI_COMMIT_BRANCH"); branch != "" {
		return branch
	}
	return os.Getenv("CI_COMMIT_TAG")
}
