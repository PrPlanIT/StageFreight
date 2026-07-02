package test

import (
	"context"
	"io"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Verify resolves the configured suites, runs them, and renders the result under
// the given intent. It returns whether all GATING (non-advisory) suites passed.
//
// This is the single behavioral-verification entry point every caller shares — the
// audition correctness gate (IntentCorrectness: "is the committed tree healthy?")
// and dependency mutation re-verification (IntentDepReverify: "did the mutation
// preserve health?"). One execution model, one renderer, one gate, one suite
// identity — deps never shells its own `go test`. No suites resolve → passed=true.
func Verify(ctx context.Context, cfg *config.Config, rootDir string, w io.Writer, intent Intent) (bool, error) {
	suites, err := Resolve(cfg, rootDir)
	if err != nil {
		return false, err
	}
	if len(suites) == 0 {
		return true, nil
	}
	res := RunRender(ctx, suites, rootDir, cfg.Toolchains.Desired, w, intent)
	return !res.FailedNonAdvisory(), nil
}
