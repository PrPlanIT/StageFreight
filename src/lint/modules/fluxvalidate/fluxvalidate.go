// Package fluxvalidate is the audition-side consumer of GitOps (Flux) manifest
// validation. The validation itself — and its unit of truth, the per-Kustomization
// verdict — lives in the gitops domain (src/gitops/validate.go,
// docs/architecture/gitops-fluxcd-validation.md). This module is a thin adapter:
// it runs the validation and maps the verdict map into lint findings so the
// result renders in audition. It activates on content (inert with no Flux CRs)
// and is advisory in this phase.
package fluxvalidate

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitops"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

const moduleName = "flux-validate"

func init() {
	lint.RegisterRepository(moduleName, func() lint.RepositoryModule { return &module{} })
}

type module struct {
	desired map[string]config.ToolPinConfig
}

func (m *module) Name() string        { return moduleName }
func (m *module) DefaultEnabled() bool { return true }

func (m *module) SetToolchainDesired(desired map[string]config.ToolPinConfig) {
	m.desired = desired
}

func (m *module) CheckRepository(ctx context.Context, root string) ([]lint.Finding, error) {
	verdicts, meta, err := gitops.ValidateManifests(ctx, root, m.desired)
	if err != nil {
		return nil, err
	}
	if len(verdicts) == 0 {
		return nil, nil // no Flux content — inert
	}
	if meta.Skipped != "" {
		return []lint.Finding{{
			Module:   moduleName,
			Severity: lint.SeverityInfo,
			Message:  fmt.Sprintf("skipped: %s — Flux manifests not validated", meta.Skipped),
		}}, nil
	}

	var findings []lint.Finding

	// Per-Kustomization Fail verdicts → critical findings, keyed by the
	// Kustomization identity (the unit of truth), in deterministic order.
	keys := make([]gitops.KustomizationKey, 0, len(verdicts))
	for k := range verdicts {
		keys = append(keys, k)
	}
	gitops.SortKeys(keys)
	for _, key := range keys {
		v := verdicts[key]
		if v.Status != gitops.Fail {
			continue
		}
		for _, reason := range v.Reasons {
			findings = append(findings, lint.Finding{
				File:     key.String(),
				Module:   moduleName,
				Severity: lint.SeverityCritical,
				Message:  reason,
			})
		}
	}

	// Coverage gaps — advisory, aggregated by kind across the repo.
	for _, kind := range gitops.SortedKinds(meta.NoSchema) {
		findings = append(findings, lint.Finding{
			Module:   moduleName,
			Severity: lint.SeverityInfo,
			Message:  fmt.Sprintf("no schema for %s (%d) — validation coverage gap", kind, meta.NoSchema[kind]),
		})
	}

	// Positive confirmation of what was checked.
	validated := 0
	for _, n := range meta.Validated {
		validated += n
	}
	if validated > 0 {
		findings = append(findings, lint.Finding{
			Module:   moduleName,
			Severity: lint.SeverityInfo,
			Message:  fmt.Sprintf("validated %d resource(s) across %d build root(s)", validated, meta.Roots),
		})
	}

	return findings, nil
}
