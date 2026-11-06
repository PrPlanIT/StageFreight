package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/autosign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
)

// PublishedImage is an image digest a registry serves at Host/Path, with the profile it signs under.
type PublishedImage struct {
	ArtifactID artifact.ArtifactID
	Host, Path string
	Tags       []string // one outcome is recorded per tag
	Digest     string
	MultiArch  bool                           // an index: signed with --recursive
	Profile    *config.ResolvedSigningProfile // nil = the legacy default
	AuthDir    string                         // DOCKER_CONFIG dir with this registry's credentials ("" = ambient)
}

// Ref is the digest reference signed and attested: host/path@digest.
func (p PublishedImage) Ref() string { return p.Host + "/" + p.Path + "@" + p.Digest }

// ImageSigner performs the registry signing operations.
type ImageSigner struct {
	SignImage func(ctx context.Context, rootDir string, desired map[string]config.ToolConstraint, digestRef string, plan sign.SignPlan, env cosign.Env, opts sign.SignOptions) error
	Attest    func(ctx context.Context, rootDir string, desired map[string]config.ToolConstraint, digestRef string, plan sign.SignPlan, env cosign.Env, opts sign.SignOptions) error
}

// CosignSigner signs and attests with cosign, resolved through the toolchain.
var CosignSigner = ImageSigner{SignImage: cosign.SignImage, Attest: cosign.Attest}

// SignResult is what signing did to one PublishedImage, for the caller to render.
type SignResult struct {
	Ref     string
	Signed  bool
	Class   string // the trust class signed under
	Skipped string // no signer resolves for this profile (with the reason)
	Err     error  // the signature failed (non-fatal unless the profile enforces)
}

// outcomeTargets is one outcome target per published tag; the digest is signed once.
func (p PublishedImage) outcomeTargets() []*artifact.OutcomeTarget {
	tags := p.Tags
	if len(tags) == 0 {
		tags = []string{""}
	}
	out := make([]*artifact.OutcomeTarget, 0, len(tags))
	for _, t := range tags {
		out = append(out, &artifact.OutcomeTarget{Kind: "registry", Host: p.Host, Path: p.Path, Tag: t})
	}
	return out
}

// SignPublished signs each image digest under its profile and records an outcome per tag.
// A failed signature is fatal only when the profile enforces; the caller surfaces unsigned
// images from the results. The legacy default signs images when COSIGN_KEY resolves.
func (s ImageSigner) SignPublished(ctx context.Context, cfg *config.Config, rootDir string, rb *build.ResultsBuilder, images []PublishedImage) ([]SignResult, error) {
	if !cfg.SigningSetup.SigningEnabled() {
		return nil, nil // global kill switch — no signing this run
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var results []SignResult
	for _, im := range images {
		ref := im.Ref()
		sc, serr := autosign.ResolveSigningContext(ctx, cfg.SigningSetup, cfg.Secrets.Providers, im.Profile, rootDir, rootDir, cfg.Toolchains.Want, now)
		if serr != nil {
			return results, serr // FATAL (continuity / state-dir guard)
		}
		if !sc.DoSign {
			results = append(results, SignResult{Ref: ref, Skipped: autosign.InactiveReason(cfg.SigningSetup)})
			continue
		}
		evidence := sc.Evidence(now)
		err := s.SignImage(ctx, rootDir, cfg.Toolchains.Want, ref, sc.Plan, sc.Env,
			sign.SignOptions{MultiArch: im.MultiArch, RegistryAuthDir: im.AuthDir})
		if err != nil {
			for _, t := range im.outcomeTargets() {
				rb.Record(im.ArtifactID, artifact.Outcome{
					Type: artifact.OutcomeTypeAttestation, Target: t,
					Attestation: &artifact.AttestationOutcome{
						Status: artifact.OutcomeFailed, Kind: "cosign",
						VerifiedDigest: im.Digest, TrustEvidence: evidence, Error: err.Error(),
					},
				})
			}
			results = append(results, SignResult{Ref: ref, Err: err})
			if im.Profile != nil && im.Profile.Enforce {
				return results, fmt.Errorf("signing image %s (enforce): %w", ref, err)
			}
			continue
		}
		for _, t := range im.outcomeTargets() {
			rb.Record(im.ArtifactID, artifact.Outcome{
				Type: artifact.OutcomeTypeAttestation, Target: t,
				Attestation: &artifact.AttestationOutcome{
					Status: artifact.OutcomeSuccess, Kind: "cosign",
					SignatureRef: ref, VerifiedDigest: im.Digest, TrustEvidence: evidence,
				},
			})
		}
		results = append(results, SignResult{Ref: ref, Signed: true, Class: string(sc.Plan.TrustClass)})
	}
	return results, nil
}

// AttestPublished attests provPath's predicate to each digest whose profile opted in,
// under the tier that signed it, and returns the refs attested. Missing provenance for an
// opted-in image is fatal; an attest failure is fatal only under enforce.
func (s ImageSigner) AttestPublished(ctx context.Context, cfg *config.Config, rootDir string, rb *build.ResultsBuilder, images []PublishedImage, provPath string) ([]string, error) {
	if !cfg.SigningSetup.SigningEnabled() {
		return nil, nil // global kill switch
	}
	predPath, provSHA, provExists := build.ExtractPredicate(provPath)
	now := time.Now().UTC().Format(time.RFC3339)
	var attested []string
	for _, im := range images {
		ref := im.Ref()
		sc, serr := autosign.ResolveSigningContext(ctx, cfg.SigningSetup, cfg.Secrets.Providers, im.Profile, rootDir, rootDir, cfg.Toolchains.Want, now)
		if serr != nil {
			return attested, serr // FATAL (continuity / state-dir guard)
		}
		if !sc.DoSign || !sc.Plan.Attestation {
			continue
		}
		if !provExists {
			return attested, fmt.Errorf("attestation is enabled for %s but build provenance %q is missing or unreadable — refusing to silently skip", ref, provPath)
		}
		evidence := sc.Evidence(now)
		opts := sign.SignOptions{MultiArch: im.MultiArch, PredicatePath: predPath, PredicateType: "slsaprovenance", RegistryAuthDir: im.AuthDir}
		err := s.Attest(ctx, rootDir, cfg.Toolchains.Want, ref, sc.Plan, sc.Env, opts)
		status, errMsg, attRef := artifact.OutcomeSuccess, "", ref
		if err != nil {
			diag.Warn("provenance attestation %s: %v", ref, err)
			status, errMsg, attRef = artifact.OutcomeFailed, err.Error(), ""
		}
		for _, t := range im.outcomeTargets() {
			rb.Record(im.ArtifactID, artifact.Outcome{
				Type: artifact.OutcomeTypeProvenanceAttestation, Target: t,
				ProvenanceAttestation: &artifact.ProvenanceAttestationOutcome{
					Status: status, Kind: "cosign", PredicateType: "slsaprovenance",
					ProvenancePath: provPath, ProvenanceSHA: provSHA, AttestationRef: attRef,
					VerifiedDigest: im.Digest, TrustEvidence: evidence, Error: errMsg,
				},
			})
		}
		if err != nil {
			if im.Profile != nil && im.Profile.Enforce {
				return attested, fmt.Errorf("attesting provenance %s (enforce): %w", ref, err)
			}
			continue
		}
		attested = append(attested, ref)
	}
	return attested, nil
}

// AttestationRequested reports whether any image will be attested (opted in, signer resolves).
func AttestationRequested(ctx context.Context, cfg *config.Config, rootDir string, images []PublishedImage) (bool, error) {
	if !cfg.SigningSetup.SigningEnabled() {
		return false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, im := range images {
		sc, err := autosign.ResolveSigningContext(ctx, cfg.SigningSetup, cfg.Secrets.Providers, im.Profile, rootDir, rootDir, cfg.Toolchains.Want, now)
		if err != nil {
			return false, err
		}
		if sc.DoSign && sc.Plan.Attestation {
			return true, nil
		}
	}
	return false, nil
}

// Perform rows under transport: publish signs and attests images as it promotes them.
var (
	deferredSigningRow     = fmt.Sprintf("%-9s signing deferred to publish — images are signed as they are promoted", "image")
	deferredAttestationRow = fmt.Sprintf("%-9s provenance attestation deferred to publish — attested as images are promoted", "docker")
)
