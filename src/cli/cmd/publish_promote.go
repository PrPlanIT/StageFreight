package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/promote"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// promoteArtifacts is the publish-phase distribution step: it pushes each
// content-store artifact to its configured registry targets WITHOUT rebuilding,
// preserving the exact digest perform recorded and review verified.
//
// This is where distribution happens in the publish phase rather than perform:
// publish resolves the carried OCI layout from the content store, then promotes
// it (digest-preserving) to each target tag. The bytes published are provably
// the bytes reviewed.
//
// It is a no-op (returning ok=false, nil) when there is nothing to promote this
// way — no outputs manifest, or no artifact carries a persistence handle (e.g.
// transport not active). In that case the caller's existing distribution path
// remains responsible. This is the explicit fallback condition, staged toward
// removing the perform-time rebuild-push once promotion is proven in production.
func promoteArtifacts(ctx context.Context, appCfg *config.Config, rootDir string, w io.Writer) (promoted int, err error) {
	outputs, readErr := artifact.ReadOutputsManifest(rootDir)
	if readErr != nil {
		// No outputs manifest = nothing perform recorded to promote. Not an error.
		return 0, nil
	}

	var failures []string
	for _, a := range outputs.Artifacts {
		if a.Kind != "docker" || a.Digest == "" {
			continue
		}
		if a.Persistence.Kind != artifact.PersistenceOCILayout || a.Persistence.OCILayout == nil {
			continue
		}
		layoutDir := a.Persistence.OCILayout.Path
		if layoutDir == "" {
			continue
		}
		// Re-hash before distributing: never push bytes we cannot verify against
		// the recorded digest. A handle that fails verification is skipped, not
		// trusted.
		if vErr := cas.VerifyLayoutAt(layoutDir, cas.Digest(a.Digest)); vErr != nil {
			fmt.Fprintf(w, "    publish: content-store layout for %s failed verification, skipping promotion: %v\n", a.Name, vErr)
			continue
		}

		for _, t := range a.Targets {
			if t.Kind != "registry" || t.Registry == nil {
				continue
			}
			auth := resolvePromoteAuth(appCfg, t.Registry.Host)
			for _, tag := range t.Registry.Tags {
				ref := t.Registry.Host + "/" + t.Registry.Path + ":" + tag
				res, pErr := promote.LayoutToRegistry(ctx, layoutDir, ref, string(a.Digest), auth)
				if pErr != nil {
					// Continue past a per-tag failure rather than abandoning the
					// rest half-distributed. Promotion is digest-preserving and
					// idempotent (re-pushing the same digest to the same tag is a
					// no-op), so a later retry safely converges — but only if we
					// don't silently leave some tags done and others not. Record
					// every failure and surface them all at the end.
					fmt.Fprintf(w, "    publish: FAILED to promote %s → %s: %v\n", a.Name, ref, pErr)
					failures = append(failures, fmt.Sprintf("%s→%s: %v", a.Name, ref, pErr))
					continue
				}
				fmt.Fprintf(w, "    publish: promoted %s → %s @ %s (digest preserved, no rebuild)\n", a.Name, res.Ref, res.Digest)
				promoted++
			}
		}
	}
	if len(failures) > 0 {
		// Partial distribution: report exactly which tags succeeded (promoted)
		// and which failed, so an operator can re-run (idempotent) to converge
		// rather than guess at a half-published state.
		return promoted, fmt.Errorf("publish promotion: %d tag(s) succeeded, %d failed: %v",
			promoted, len(failures), failures)
	}
	return promoted, nil
}

// resolvePromoteAuth resolves registry credentials for a target host by matching
// it against the configured registries' credential prefixes, returning a
// go-containerregistry Authenticator. Returns nil (anonymous / ambient
// keychain) when no matching credentials are configured.
func resolvePromoteAuth(appCfg *config.Config, host string) authn.Authenticator {
	normHost := registry.NormalizeHost(host)
	for _, reg := range appCfg.Registries {
		if reg.Credentials == "" {
			continue
		}
		if registry.NormalizeHost(reg.URL) != normHost {
			continue
		}
		cred := credentials.ResolvePrefix(reg.Credentials)
		if !cred.IsSet() {
			continue
		}
		return authn.FromConfig(authn.AuthConfig{
			Username: cred.User,
			Password: cred.Secret,
		})
	}
	return nil
}
