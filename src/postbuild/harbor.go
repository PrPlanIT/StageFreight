// Package postbuild contains post-build hook adapters and pipeline↔registry
// coordination glue. Harbor functions sit here because they coordinate between
// build plan types and registry clients — same layer as the hooks.
package postbuild

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// EnsureHarborProjects pre-creates Harbor projects for all Harbor registry targets
// that have credentials configured. Must be called after Login() and only when
// a real remote push will occur. Dedupes by (registryURL, project).
func EnsureHarborProjects(ctx context.Context, registries []build.RegistryTarget) error {
	seen := map[string]struct{}{}
	for _, reg := range registries {
		if registry.NormalizeProvider(reg.Provider) != "harbor" || reg.Credentials == "" {
			continue
		}
		// Trim leading slash defensively — config validation should prevent it,
		// but a stray "/" would make the first segment empty and silently misbehave.
		project := strings.TrimPrefix(reg.Path, "/")
		if idx := strings.IndexByte(project, '/'); idx >= 0 {
			project = project[:idx]
		}
		if project == "" {
			return fmt.Errorf("harbor %s: registry target has empty path — check config", reg.URL)
		}
		key := reg.URL + "|" + project
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		cred := credentials.ResolvePrefix(reg.Credentials)
		if !cred.IsSet() {
			upper := strings.ToUpper(reg.Credentials)
			return fmt.Errorf("harbor %s: credentials %q configured but %s_USER and %s_TOKEN/%s_PASS/%s_PASSWORD are not set",
				reg.URL, reg.Credentials, upper, upper, upper, upper)
		}
		h := registry.NewHarbor(reg.URL, cred.User, cred.Secret)
		if err := h.EnsureProject(ctx, project); err != nil {
			upper := strings.ToUpper(reg.Credentials)
			var httpErr *registry.HTTPError
			if errors.As(err, &httpErr) {
				switch httpErr.StatusCode {
				case 401:
					return fmt.Errorf("harbor %s: authentication failed while ensuring project %q — check %s_USER and %s_TOKEN/%s_PASS/%s_PASSWORD: %w",
						reg.URL, project, upper, upper, upper, upper, err)
				case 403:
					return fmt.Errorf("harbor %s: account %q lacks 'Create Project' permission for project %q — grant it or pre-create the project: %w",
						reg.URL, cred.User, project, err)
				}
			}
			return fmt.Errorf("harbor %s: %w", reg.URL, err)
		}
	}
	return nil
}

// IsHarborProjectMissingPushError reports whether the given push stderr
// appears to indicate a Harbor "project not found" failure.
// Returns false immediately if no Harbor registries are present.
//
// This is a narrow heuristic used only for recovery (auto-create + retry),
// not a definitive classification of Harbor errors.
func IsHarborProjectMissingPushError(registries []build.RegistryTarget, stderr string) bool {
	hasHarbor := false
	for _, reg := range registries {
		if registry.NormalizeProvider(reg.Provider) == "harbor" {
			hasHarbor = true
			break
		}
	}
	if !hasHarbor {
		return false
	}

	lower := strings.ToLower(stderr)
	hasProject := strings.Contains(lower, "project")
	hasMissing := strings.Contains(lower, "not found") ||
		strings.Contains(lower, "does not exist")
	hasContext := strings.Contains(lower, "harbor") ||
		strings.Contains(lower, "denied") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "manifest") ||
		strings.Contains(lower, "repository")
	return hasProject && hasMissing && hasContext
}

// TriggerHarborScans fires a vulnerability scan on Harbor for each pushed tag
// where native_scan: true is configured. Best-effort — scan failures are warned,
// never fail the build. Must be called after push. Dedupes by (registryURL, path, tag).
func TriggerHarborScans(ctx context.Context, registries []build.RegistryTarget) {
	seen := map[string]struct{}{}
	for _, reg := range registries {
		if !reg.NativeScan || registry.NormalizeProvider(reg.Provider) != "harbor" || reg.Credentials == "" {
			continue
		}
		cred := credentials.ResolvePrefix(reg.Credentials)
		if !cred.IsSet() {
			continue
		}
		h := registry.NewHarbor(reg.URL, cred.User, cred.Secret)
		for _, tag := range reg.Tags {
			key := reg.URL + "|" + reg.Path + ":" + tag
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			if err := h.TriggerScan(ctx, reg.Path, tag); err != nil {
				diag.Warn("harbor scan trigger %s/%s:%s: %v", reg.URL, reg.Path, tag, err)
			}
		}
	}
}
