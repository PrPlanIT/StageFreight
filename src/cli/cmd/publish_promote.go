package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/output"
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

	// Publish owns publication outcome records: it is the only phase that mutates
	// registries, so it is the only phase that can truthfully record what was
	// distributed. Promotion outcomes are accumulated here and written to
	// published.json (the results manifest) from the publish phase — replacing
	// the empty results perform writes under transport. Build() binds these
	// observations to the reviewed intent via the outputs checksum.
	rb := build.NewResultsBuilder()
	recordedResults := false

	// Per-artifact distribution evidence, collected then rendered as a first-class
	// section. Publish is now the sole phase that mutates registries, so it must
	// emit the STRONGEST distribution evidence in the pipeline — not bury it in
	// informational log lines. Each block states the digest distributed, every
	// registry ref it reached (✓/✗), and that the bytes were promoted from the
	// content store with no rebuild and the digest preserved.
	type distTag struct {
		ref    string
		digest string // digest the registry served on post-push re-read (verified == recorded)
		ok     bool
		errMsg string
	}
	type distArtifact struct {
		name, digest, verifySkip string
		tags                     []distTag
	}
	var dists []distArtifact

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
			dists = append(dists, distArtifact{
				name:       a.Name,
				digest:     string(a.Digest),
				verifySkip: vErr.Error(),
			})
			continue
		}

		dist := distArtifact{name: a.Name, digest: string(a.Digest)}
		artifactID := artifact.NewArtifactID(a.Kind, a.Name)
		for _, t := range a.Targets {
			if t.Kind != "registry" || t.Registry == nil {
				continue
			}
			auth := resolvePromoteAuth(appCfg, t.Registry.Host)
			for _, tag := range t.Registry.Tags {
				ref := t.Registry.Host + "/" + t.Registry.Path + ":" + tag
				target := &artifact.OutcomeTarget{
					Kind: "registry", Host: t.Registry.Host, Path: t.Registry.Path, Tag: tag,
				}
				res, pErr := promote.LayoutToRegistry(ctx, layoutDir, ref, string(a.Digest), auth)
				if pErr != nil {
					// Continue past a per-tag failure rather than abandoning the
					// rest half-distributed. Promotion is digest-preserving and
					// idempotent (re-pushing the same digest to the same tag is a
					// no-op), so a later retry safely converges — but only if we
					// don't silently leave some tags done and others not. Record
					// every failure and surface them all at the end.
					dist.tags = append(dist.tags, distTag{ref: ref, ok: false, errMsg: pErr.Error()})
					failures = append(failures, fmt.Sprintf("%s→%s: %v", a.Name, ref, pErr))
					rb.Record(artifactID, artifact.Outcome{
						Type:   artifact.OutcomeTypePush,
						Target: target,
						Push: &artifact.PushOutcome{
							Status: artifact.OutcomeFailed,
							Digest: string(a.Digest),
							Error:  pErr.Error(),
						},
					})
					recordedResults = true
					continue
				}
				dist.tags = append(dist.tags, distTag{ref: res.Ref, digest: res.Digest, ok: true})
				rb.Record(artifactID, artifact.Outcome{
					Type:   artifact.OutcomeTypePush,
					Target: target,
					Push: &artifact.PushOutcome{
						Status:         artifact.OutcomeSuccess,
						Digest:         res.Digest,
						ObservedDigest: res.Digest,
						ObservedBy:     "promote",
					},
				})
				recordedResults = true
				promoted++
			}
		}
		dists = append(dists, dist)
	}

	// Render the distribution as an EVENT, not a state readout. Publish is the only
	// phase authorized to mutate external systems, so the log must answer "what was
	// published?" first and carry the digest proof beneath it — not present a digest
	// and leave the operator to infer that a mutation occurred. The headline is the
	// mutation (PUBLISHED); each tag states the external change ("registry now serves
	// <digest>"), which is a fact, not a hope: promote.LayoutToRegistry re-reads the
	// tag off the registry after writing and errors unless the remote resolves to
	// exactly the recorded digest, so a ✓ is the registry's own acknowledgement.
	color := output.UseColor()
	for _, d := range dists {
		sec := output.NewSection(w, "Distribution (publish)", 0, color)
		sec.Row("%-10s%s", "artifact", d.name)
		sec.Row("%-10s%s", "digest", d.digest)

		if d.verifySkip != "" {
			sec.Separator()
			sec.Row("NOT DISTRIBUTED")
			sec.Row("%s  content-store layout failed verification", output.StatusIcon("failed", color))
			sec.Row("%-10s%s", "reason", d.verifySkip)
			sec.Close()
			continue
		}

		published, failed := 0, 0
		for _, tg := range d.tags {
			if tg.ok {
				published++
			} else {
				failed++
			}
		}

		sec.Separator()
		switch {
		case failed == 0:
			sec.Row("PUBLISHED")
		case published == 0:
			sec.Row("PUBLISH FAILED")
		default:
			sec.Row("PARTIALLY PUBLISHED")
		}
		for _, tg := range d.tags {
			if tg.ok {
				sec.Row("%s  %s", output.StatusIcon("success", color), tg.ref)
				sec.Row("     registry now serves %s", tg.digest)
			} else {
				sec.Row("%s  %s", output.StatusIcon("failed", color), tg.ref)
				sec.Row("     NOT published — %s", tg.errMsg)
			}
		}

		sec.Separator()
		sec.Row("summary")
		sec.Row("  %d of %d tag(s) published", published, len(d.tags))
		sec.Row("  %d tag(s) verified against recorded digest", published)
		sec.Row("  digest preserved")
		sec.Row("  rebuild not required")
		sec.Close()
	}

	// Write published.json from the publish phase — the observed publication
	// outcome, owned by the phase that performed the distribution. Only when
	// promotion actually recorded outcomes (transport active); otherwise the
	// legacy perform-written results manifest stands.
	if recordedResults {
		results, bErr := rb.Build(outputs)
		if bErr != nil {
			return promoted, fmt.Errorf("building publication results manifest: %w", bErr)
		}
		if wErr := artifact.WriteResultsManifest(rootDir, results); wErr != nil {
			return promoted, fmt.Errorf("writing publication results manifest: %w", wErr)
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
