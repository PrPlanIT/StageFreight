package pipeline

import (
	"os"
	"runtime"

	"github.com/PrPlanIT/StageFreight/src/output"
)

// CIContextKV returns the generic CI context key-value pairs.
// Command-specific KVs (registries, platforms, etc.) are added via BannerPhase's extraKV callback.
func CIContextKV() []output.KV {
	var kv []output.KV

	if pipe := os.Getenv("CI_PIPELINE_ID"); pipe != "" {
		kv = append(kv, output.KV{Key: "Pipeline", Value: pipe})
	}
	if runner := os.Getenv("CI_RUNNER_DESCRIPTION"); runner != "" {
		kv = append(kv, output.KV{Key: "Runner", Value: runner})
	}

	if sha := os.Getenv("CI_COMMIT_SHORT_SHA"); sha != "" {
		kv = append(kv, output.KV{Key: "Commit", Value: sha})
	} else if sha := os.Getenv("CI_COMMIT_SHA"); sha != "" && len(sha) >= 8 {
		kv = append(kv, output.KV{Key: "Commit", Value: sha[:8]})
	}
	if branch := os.Getenv("CI_COMMIT_BRANCH"); branch != "" {
		kv = append(kv, output.KV{Key: "Branch", Value: branch})
	} else if tag := os.Getenv("CI_COMMIT_TAG"); tag != "" {
		kv = append(kv, output.KV{Key: "Tag", Value: tag})
	}

	// Default platform (pre-plan; command-specific extraKV can override)
	platforms := runtime.GOOS + "/" + runtime.GOARCH
	if p := os.Getenv("STAGEFREIGHT_PLATFORMS"); p != "" {
		platforms = p
	}
	if platforms != "" {
		kv = append(kv, output.KV{Key: "Platforms", Value: platforms})
	}

	return kv
}
