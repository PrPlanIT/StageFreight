package postbuild

import (
	"context"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/diag"
)

// PushFailure carries structured context about a failed push operation.
// Both multi-platform (BuildWithLayers) and single-platform (PushTags)
// paths produce this type for recovery classification.
type PushFailure struct {
	Err      error  // the original error
	ExitCode int    // process exit code (1 if not determinable)
	Stderr   string // stderr from the failed operation
	Tag      string // specific ref that failed (empty for multi-platform)
}

// PushRecoveryResult tells the caller whether a push failure was recoverable.
type PushRecoveryResult struct {
	Retry   bool   // true = recovery action succeeded, caller should retry
	Message string // diagnostic message for the caller to log
}

// RecoverPushFailure inspects a push failure and attempts vendor-specific
// recovery (e.g. creating a missing Harbor project). Returns whether the
// caller should retry the failed operation.
//
// execute.go owns retry mechanics (which tags, stderr reset). This function
// owns the vendor decision (is this recoverable? what action to take?).
func RecoverPushFailure(ctx context.Context, registries []build.RegistryTarget, failure PushFailure) PushRecoveryResult {
	// Harbor: project-not-found → auto-create project
	if IsHarborProjectMissingPushError(registries, failure) {
		if err := EnsureHarborProjects(ctx, registries); err != nil {
			diag.Warn("harbor: auto-create failed: %v", err)
			return PushRecoveryResult{Retry: false, Message: "harbor: project auto-create failed"}
		}
		return PushRecoveryResult{Retry: true, Message: "harbor: created missing project, retrying push"}
	}

	return PushRecoveryResult{Retry: false}
}

// ClassifyPushFailure returns a short operator-meaningful reason for a push
// failure, with an owner tag in parentheses. Classification uses keyword
// heuristics — push CLI exit codes don't differentiate error types.
func ClassifyPushFailure(failure PushFailure) string {
	s := strings.ToLower(failure.Stderr + "\n" + failure.Err.Error())
	switch {
	case strings.Contains(s, "500 internal server error"):
		return "HTTP 500 (registry)"
	case strings.Contains(s, "401") || strings.Contains(s, "unauthorized"):
		return "authentication failed (credentials)"
	case strings.Contains(s, "403") || strings.Contains(s, "denied"):
		return "permission denied (credentials)"
	case strings.Contains(s, "404") || strings.Contains(s, "not found"):
		return "repository not found (registry)"
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "connection timed out (network)"
	case strings.Contains(s, "no such host") || strings.Contains(s, "lookup"):
		return "DNS resolution failed (network)"
	case strings.Contains(s, "certificate") || strings.Contains(s, "x509"):
		return "TLS certificate error (network)"
	default:
		return "push failed"
	}
}

// PostPushHooks runs vendor-specific post-push actions (e.g. scan triggers).
// Best-effort — failures are warned, never fatal.
func PostPushHooks(ctx context.Context, registries []build.RegistryTarget) {
	TriggerHarborScans(ctx, registries)
}
