package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	builddocker "github.com/PrPlanIT/StageFreight/src/build/docker"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
	"github.com/PrPlanIT/StageFreight/src/promote"
	"github.com/PrPlanIT/StageFreight/src/registry"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
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
func promoteArtifacts(ctx context.Context, appCfg *config.Config, rootDir string, w io.Writer) (promoted int, untargeted int, err error) {
	outputs, readErr := artifact.ReadOutputsManifest(rootDir)
	if readErr != nil {
		// No outputs manifest = nothing perform recorded to promote. Not an error.
		return 0, 0, nil
	}

	// Publish owns publication outcome records: it is the only phase that mutates
	// registries, so it is the only phase that can truthfully record what was
	// distributed. Promotion outcomes are accumulated here and written to
	// published.json (the results manifest) from the publish phase — replacing
	// the empty results perform writes under transport. Build() binds these
	// observations to the reviewed intent via the outputs checksum.
	// Seed from the perform-written results so promotion EXTENDS recorded history
	// rather than replacing it. published.json is cumulative observed truth: perform
	// records binary/archive/docker-build outcomes there, and release-asset discovery
	// (BuildArchiveExecutionViews) reads the archive BUILD outcomes from it. Writing a
	// fresh docker-push-only manifest here clobbered those and dropped every release
	// asset (the binaries never attached). Seeding makes promotion ADD push outcomes
	// on top of the existing facts.
	existingResults, _ := artifact.ReadResultsManifest(rootDir)
	rb := build.ResultsBuilderFromManifest(existingResults)
	recordedResults := false
	var pubTags, pubHosts, pubDigests []string

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
		gate                     string // the gate that refused it, when not layout verification
		tags                     []distTag
	}
	var dists []distArtifact

	var failures []string
	var signPrepErrs []error

	// Provenance gate input: the commit the source under build resolves to — the value
	// StandardLabels stamped into org.opencontainers.image.revision at build time. promote
	// re-tags a digest-preserved image WITHOUT re-stamping, so a shipped image whose
	// revision label disagrees with this was baked from the wrong version (the version.*
	// orchestrator-stamp regression) and must not be distributed as-is. Empty (unversioned
	// or detached build) disables the gate rather than guessing.
	_, wantCommit := build.ResolveImageStamp(rootDir, appCfg)

	// Post-distribution native scans, collected per (host,path) and fired only for
	// tags that actually pushed. Publish owns post-distribution registry actions.
	type scanKey struct{ host, path string }
	scanTags := map[scanKey][]string{}

	evidence := newEvidenceGate(appCfg)
	defer evidence.record(rootDir)

	// Endpoints a tag actually reached, signed once promotion finishes.
	var toSign []builddocker.PublishedImage
	authDirs := newRegistryAuthDirs(appCfg)
	defer authDirs.cleanup()

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
		// Produced but not distributed on this ref: the image was built and
		// retained (review scanned it), but no publish target matches this
		// branch/tag. There is nothing to promote — disclose it rather than
		// render a misleading empty "PUBLISHED" box. (Produced != published.)
		if !hasRegistryTarget(a.Targets) {
			untargeted++
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

		// Provenance gate: the shipped image's revision label must name the source HEAD.
		// Only a definite mismatch blocks (a missing label, or an undeterminable source
		// commit, disables the check rather than fabricating a failure). A mismatch means
		// the image carries someone else's provenance — refuse to distribute it and fail
		// the run. A label-read hiccup is best-effort defense, not fatal: note it and push.
		if wantCommit != "" {
			rev, lErr := promote.OCILabel(layoutDir, string(a.Digest), build.LabelRevision)
			switch {
			case lErr != nil:
				fmt.Fprintf(os.Stderr, "warning: provenance gate could not read %s for %s: %v\n", build.LabelRevision, a.Name, lErr)
			case rev != "" && rev != wantCommit:
				reason := fmt.Sprintf("%s %s does not match source HEAD %s — provenance mismatch, not distributed", build.LabelRevision, rev, wantCommit)
				dists = append(dists, distArtifact{name: a.Name, digest: string(a.Digest), verifySkip: reason})
				failures = append(failures, fmt.Sprintf("%s: %s", a.Name, reason))
				continue
			}
		}

		// Evidence gate: the image must contain the version the release claims
		// (upstream.evidence). Probed on the exact bytes about to be distributed.
		if reason := evidence.check(a.Name, layoutDir, string(a.Digest)); reason != "" {
			dists = append(dists, distArtifact{name: a.Name, digest: string(a.Digest), verifySkip: reason, gate: "version evidence"})
			failures = append(failures, fmt.Sprintf("%s: %s", a.Name, reason))
			continue
		}

		dist := distArtifact{name: a.Name, digest: string(a.Digest)}
		artifactID := artifact.NewArtifactID(a.Kind, a.Name)
		var reached []promotedEndpoint
		for _, t := range a.Targets {
			if t.Kind != "registry" || t.Registry == nil {
				continue
			}
			auth := resolvePromoteAuth(appCfg, t.Registry.Host)
			ep := promotedEndpoint{host: t.Registry.Host, path: t.Registry.Path, profileID: t.Registry.SigningProfile}
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
				pubTags = appendDistinct(pubTags, tag)
				pubHosts = appendDistinct(pubHosts, t.Registry.Host)
				pubDigests = appendDistinct(pubDigests, shortDigest(res.Digest))
				if t.Registry.NativeScan {
					k := scanKey{t.Registry.Host, t.Registry.Path}
					scanTags[k] = append(scanTags[k], tag)
				}
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
				ep.tags = append(ep.tags, tag)
			}
			if len(ep.tags) > 0 {
				reached = append(reached, ep)
			}
		}
		dists = append(dists, dist)
		if len(reached) > 0 {
			images, sErr := signingImages(appCfg, artifactID, string(a.Digest), layoutDir, reached, authDirs.dirFor)
			if sErr != nil {
				signPrepErrs = append(signPrepErrs, fmt.Errorf("%s: preparing signing: %w", a.Name, sErr))
			}
			toSign = append(toSign, images...)
		}
	}

	signResults, attested, signErr := signPromoted(ctx, appCfg, rootDir, rb, toSign)
	if len(signResults) > 0 || len(attested) > 0 {
		recordedResults = true
	}

	// Post-distribution: trigger each successfully-pushed native-scan registry's
	// own scanner (Harbor's built-in Trivy). Best-effort — never fails publish.
	if len(scanTags) > 0 {
		var nativeScan []build.RegistryTarget
		for k, tags := range scanTags {
			reg := findRegistryByHost(appCfg, k.host)
			if reg == nil {
				continue
			}
			nativeScan = append(nativeScan, build.RegistryTarget{
				URL:         k.host,
				Path:        k.path,
				Tags:        tags,
				Provider:    reg.Provider,
				Credentials: reg.Credentials,
				NativeScan:  true,
			})
		}
		postbuild.PostPushHooks(ctx, nativeScan)
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
			if d.gate != "" {
				sec.Row("%s  refused by the %s gate", output.StatusIcon("failed", color), d.gate)
			} else {
				sec.Row("%s  content-store layout failed verification", output.StatusIcon("failed", color))
			}
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
		if published > 0 {
			sec.Row("  %d tag(s) verified against recorded digest", published)
			sec.Row("  digest preserved")
			sec.Row("  rebuild not required")
		}
		sec.Close()
	}

	renderSigning(w, signResults, attested, color)

	// Structured publish facts ({publish.tags}/{publish.registries}) for the stencil
	// language — recorded by the phase that performed the distribution.
	if len(pubTags) > 0 {
		if err := cistate.UpdateState(rootDir, func(s *cistate.State) {
			s.MergeSubsystemResults("publish", map[string]string{
				"tags":       strings.Join(pubTags, ", "),
				"registries": strings.Join(pubHosts, ", "),
				"digest":     strings.Join(pubDigests, ", "),
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
	}

	// Write published.json from the publish phase — the observed publication
	// outcome, owned by the phase that performed the distribution. Only when
	// promotion actually recorded outcomes (transport active); otherwise the
	// legacy perform-written results manifest stands.
	if recordedResults {
		results, bErr := rb.Build(outputs)
		if bErr != nil {
			return promoted, untargeted, fmt.Errorf("building publication results manifest: %w", bErr)
		}
		if wErr := artifact.WriteResultsManifest(rootDir, results); wErr != nil {
			return promoted, untargeted, fmt.Errorf("writing publication results manifest: %w", wErr)
		}
	}
	// The Distribution box already rendered the per-target evidence; the error carries
	// only the verdicts: failed tags, signing that could not be prepared, fatal signing.
	var errs []error
	if len(failures) > 0 {
		errs = append(errs, fmt.Errorf("%d of %d tag(s) failed to publish", len(failures), promoted+len(failures)))
	}
	errs = append(errs, signPrepErrs...)
	errs = append(errs, signErr)
	return promoted, untargeted, errors.Join(errs...)
}

// promotedEndpoint is a registry endpoint promotion reached, its tags and signing profile.
type promotedEndpoint struct {
	host, path, profileID string
	tags                  []string
}

// signingImages turns promoted endpoints into PublishedImages: profile from outputs,
// multi-arch from the layout, credentials from authDir.
func signingImages(appCfg *config.Config, id artifact.ArtifactID, digest, layoutDir string, eps []promotedEndpoint, authDir func(host string) (string, error)) ([]builddocker.PublishedImage, error) {
	multiArch, err := promote.IsIndex(layoutDir, digest)
	if err != nil {
		return nil, err
	}
	var out []builddocker.PublishedImage
	for _, ep := range eps {
		profile, err := config.ResolveSigningProfileForTarget(config.TargetConfig{SigningProfile: ep.profileID},
			appCfg.SigningSetup.Profiles, appCfg.SigningSetup.DefaultProfile)
		if err != nil {
			return out, fmt.Errorf("%s/%s: %w", ep.host, ep.path, err)
		}
		dir, err := authDir(ep.host)
		if err != nil {
			return out, fmt.Errorf("%s/%s: %w", ep.host, ep.path, err)
		}
		out = append(out, builddocker.PublishedImage{
			ArtifactID: id, Host: ep.host, Path: ep.path, Tags: ep.tags,
			Digest: digest, MultiArch: multiArch, Profile: profile, AuthDir: dir,
		})
	}
	return out, nil
}

// publishSigner signs and attests promoted images; tests substitute it.
var publishSigner = builddocker.CosignSigner

// signPromoted signs the promoted images, then attests the provenance perform wrote
// when a profile opted in; absent or ambiguous provenance is then fatal.
func signPromoted(ctx context.Context, appCfg *config.Config, rootDir string, rb *build.ResultsBuilder, images []builddocker.PublishedImage) ([]builddocker.SignResult, []string, error) {
	if len(images) == 0 {
		return nil, nil, nil
	}
	results, err := publishSigner.SignPublished(ctx, appCfg, rootDir, rb, images)
	if err != nil {
		return results, nil, err
	}
	want, err := builddocker.AttestationRequested(ctx, appCfg, rootDir, images)
	if err != nil || !want {
		return results, nil, err
	}
	stmt, _, _, err := resolveSignProvenance(rootDir)
	if err != nil {
		return results, nil, err
	}
	attested, err := publishSigner.AttestPublished(ctx, appCfg, rootDir, rb, images, stmt)
	return results, attested, err
}

// renderSigning shows what publish signed and attested, beneath the distribution.
func renderSigning(w io.Writer, results []builddocker.SignResult, attested []string, color bool) {
	if len(results) == 0 && len(attested) == 0 {
		return
	}
	sec := output.NewSection(w, "Signing (publish)", 0, color)
	var skipReasons []string
	for _, r := range results {
		switch {
		case r.Signed:
			sec.Row("%s  signed (%s) %s", output.StatusIcon("success", color), r.Class, r.Ref)
		case r.Err != nil:
			sec.Row("%s  NOT signed %s — %v", output.StatusIcon("failed", color), r.Ref, r.Err)
		default:
			sec.Row("%s  NOT signed %s", output.StatusIcon("warning", color), r.Ref)
			skipReasons = appendDistinct(skipReasons, r.Skipped)
		}
	}
	for _, reason := range skipReasons {
		sec.RowIndented(3, false, color, "%s", reason)
	}
	for _, ref := range attested {
		sec.Row("%s  provenance attested %s", output.StatusIcon("success", color), ref)
	}
	sec.Close()
}

// registryAuthDirs writes a DOCKER_CONFIG per registry host from promotion's credentials.
type registryAuthDirs struct {
	cfg      *config.Config
	dirs     map[string]string
	cleanups []func()
}

func newRegistryAuthDirs(cfg *config.Config) *registryAuthDirs {
	return &registryAuthDirs{cfg: cfg, dirs: map[string]string{}}
}

// dirFor returns host's auth dir, "" when none is configured.
func (r *registryAuthDirs) dirFor(host string) (string, error) {
	if d, ok := r.dirs[host]; ok {
		return d, nil
	}
	user, secret, ok := promoteCredential(r.cfg, host)
	if !ok {
		r.dirs[host] = ""
		return "", nil
	}
	dir, cleanup, err := cosign.WriteRegistryAuth(host, user, secret)
	if err != nil {
		return "", err
	}
	r.cleanups = append(r.cleanups, cleanup)
	r.dirs[host] = dir
	return dir, nil
}

func (r *registryAuthDirs) cleanup() {
	for _, c := range r.cleanups {
		c()
	}
}

// hasRegistryTarget reports whether an artifact carries at least one registry
// distribution target. A retained docker artifact with none is "produced but
// not distributed on this ref" — the publish phase discloses it, never pushes.
func hasRegistryTarget(targets []artifact.Target) bool {
	for _, t := range targets {
		if t.Kind == "registry" && t.Registry != nil {
			return true
		}
	}
	return false
}

// resolvePromoteAuth resolves registry credentials for a target host by matching
// it against the configured registries' credential prefixes, returning a
// go-containerregistry Authenticator. Returns nil (anonymous / ambient
// keychain) when no matching credentials are configured.
func resolvePromoteAuth(appCfg *config.Config, host string) authn.Authenticator {
	user, secret, ok := promoteCredential(appCfg, host)
	if !ok {
		return nil
	}
	return authn.FromConfig(authn.AuthConfig{Username: user, Password: secret})
}

// promoteCredential resolves host's registry credentials for promotion and signing alike.
func promoteCredential(appCfg *config.Config, host string) (user, secret string, ok bool) {
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
		return cred.User, cred.Secret, true
	}
	return "", "", false
}

// findRegistryByHost returns the configured registry whose URL matches host (or
// nil) — used to recover provider/credentials for post-distribution actions.
func findRegistryByHost(appCfg *config.Config, host string) *config.RegistryConfig {
	normHost := registry.NormalizeHost(host)
	for i := range appCfg.Registries {
		if registry.NormalizeHost(appCfg.Registries[i].URL) == normHost {
			return &appCfg.Registries[i]
		}
	}
	return nil
}
