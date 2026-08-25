package runtime

import (
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// ValidateCapabilities checks that the backend supports all required capabilities.
// Called during the Validate phase — fails early, not at runtime.
func ValidateCapabilities(backend LifecycleBackend, required []Capability) error {
	have := map[Capability]bool{}
	for _, c := range backend.Capabilities() {
		have[c] = true
	}

	var missing []string
	for _, c := range required {
		if !have[c] {
			missing = append(missing, string(c))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("backend %q missing required capabilities: %s",
			backend.Name(), strings.Join(missing, ", "))
	}
	return nil
}

// DeriveRequired determines which capabilities the RUNNING mode needs, given config and context.
// Phase ↔ Capability binding is enforced here.
func DeriveRequired(mode string, cfg *config.Config, rctx *RuntimeContext) []Capability {
	var required []Capability

	// All backends must support plan/execute separation.
	required = append(required, CapPlanExecute)

	// Requirements derive from the mode being RUN, never the repo's primary
	// lifecycle.mode — coexisting subsystems (ansible beside gitops) validate
	// against their own contract, not the host repo's.
	switch mode {
	case config.ModeGitops:
		required = append(required, CapReconcile)
		required = append(required, CapImpactAnalysis)
		if len(cfg.GitOps) > 0 {
			required = append(required, CapClusterAuth)
		}
	case config.ModeDocker, "ansible":
		required = append(required, CapReconcile)
	}

	// Dry-run requested → backend must support it.
	if rctx.DryRun {
		required = append(required, CapDryRun)
	}

	return required
}

// HasCapability checks if a backend declares a specific capability.
func HasCapability(backend LifecycleBackend, cap Capability) bool {
	for _, c := range backend.Capabilities() {
		if c == cap {
			return true
		}
	}
	return false
}
