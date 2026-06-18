package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
)

var (
	signProfileID  string
	signConfigFile string
	signSkipImages bool
)

var signCmd = &cobra.Command{
	Use:   "sign",
	Short: "Attach an additional signature to already-built release artifacts",
	Long: `Layers an additional signature onto the immutable artifacts a build already
produced — a human publication act, separate from CI artifact production. The
canonical use is hardware (YubiKey) authorization of an official release: CI builds
and records the artifacts; a maintainer, on a machine with the token, runs this and
physically touches the key.

It is strictly ADDITIVE and manifest-sourced:
  - never rebuilds, republishes, or mutates artifact contents
  - validates recorded digests first (refuses to sign drifted artifacts)
  - writes a distinct signature file, preserving lower-tier signatures
  - extends the results manifest with new trust evidence (never replaces)

The operation is generic — interactivity emerges from the selected profile's trust
class (hardware prompts for touch/PIN; key/kms/oidc are non-interactive). Today it
signs the release SHA256SUMS; image attestation is a follow-up.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return err
		}
		if signProfileID == "" {
			return fmt.Errorf("--profile is required (the trust profile to sign under)")
		}
		cfg, err := config.Load(signConfigFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		profile, err := config.ResolveSigningProfileByID(cfg.Signing, signProfileID)
		if err != nil {
			return err
		}

		results, err := artifact.ReadResultsManifest(rootDir)
		if err != nil {
			return fmt.Errorf("reading results manifest (run a build first): %w", err)
		}
		distDir := filepath.Join(rootDir, build.DistDir)

		// Refuse to sign artifacts that drifted since the build.
		if err := artifact.ValidateRecordedDigests(results, distDir); err != nil {
			return err
		}

		plan := sign.Compile(profile)
		if !sign.Enabled(plan) {
			return fmt.Errorf("profile %q does not resolve to a usable signer (e.g. an unset key reference)", signProfileID)
		}
		env := envForClass(plan)

		signerRef := sign.SignerRef(plan)
		if signerRef == "" {
			signerRef = "profile:" + signProfileID
		}
		evidence := func() artifact.TrustEvidence {
			return artifact.TrustEvidence{
				TrustClass:       string(plan.TrustClass),
				PhysicalPresence: plan.RequiresPhysicalPresence,
				NonExportable:    plan.RequiresNonExportableKey,
				Transparency:     plan.TransparencyRequired,
				SignerRef:        signerRef,
				SignedAt:         time.Now().UTC().Format(time.RFC3339),
			}
		}

		sumsPath := filepath.Join(distDir, "SHA256SUMS")
		_, sumsErr := os.Stat(sumsPath)
		if sumsErr == nil {
			// The checksum file is the thing we sign — confirm it still describes
			// its artifacts before signing (don't sign a drifted/tampered manifest).
			if err := artifact.ValidateChecksumsFile(sumsPath); err != nil {
				return err
			}
		}
		var images []imageTarget
		if !signSkipImages {
			images = imageTargets(results)
		}
		total := len(images)
		if sumsErr == nil {
			total++
		}
		if total == 0 {
			return fmt.Errorf("nothing to sign — no SHA256SUMS and no published images in the manifest")
		}
		if plan.TrustClass == sign.ClassHardware {
			fmt.Printf("Profile %q is hardware-backed: you will be prompted to TOUCH your key for each of %d artifact(s).\n", signProfileID, total)
		}

		// 1. Release checksums — distinct sig file preserves the lower-tier signature.
		if sumsErr == nil {
			outSig := filepath.Join(distDir, "SHA256SUMS."+signProfileID+".sig")
			fmt.Printf("Signing SHA256SUMS (class %s)…\n", plan.TrustClass)
			if err := cosign.SignBlob(cmd.Context(), rootDir, cfg.Toolchains.Desired, sumsPath, outSig, plan, env); err != nil {
				return err
			}
			appendOutcome(results, artifact.NewArtifactID("checksums", "SHA256SUMS"), "SHA256SUMS", "checksums",
				artifact.Outcome{Type: artifact.OutcomeTypeBlobSignature, BlobSignature: &artifact.BlobSignatureOutcome{
					Status: artifact.OutcomeSuccess, Kind: "cosign", BlobPath: sumsPath, SignaturePath: outSig, TrustEvidence: evidence(),
				}})
			fmt.Printf("  ✓ %s\n", filepath.Base(outSig))
		}

		// 2. Published images — cosign attaches ANOTHER signature to the same
		//    immutable digest (the registry holds multiple); record an additional
		//    attestation outcome. The recorded digest is signed verbatim, so there is
		//    no drift surface (the digest is the content identity).
		for _, im := range images {
			fmt.Printf("Signing image %s (class %s)…\n", im.digestRef, plan.TrustClass)
			if err := cosign.SignImage(cmd.Context(), rootDir, cfg.Toolchains.Desired, im.digestRef, plan, env, sign.SignOptions{}); err != nil {
				return err
			}
			appendOutcome(results, im.artifactID, im.name, "docker",
				artifact.Outcome{Type: artifact.OutcomeTypeAttestation, Target: im.target,
					Attestation: &artifact.AttestationOutcome{
						Status: artifact.OutcomeSuccess, Kind: "cosign",
						SignatureRef: im.digestRef, VerifiedDigest: im.digest, TrustEvidence: evidence(),
					}})
			fmt.Printf("  ✓ %s\n", im.digestRef)
		}

		if err := artifact.WriteResultsManifest(rootDir, *results); err != nil {
			return fmt.Errorf("recording signatures: %w", err)
		}
		fmt.Printf("✓ attached %d %s signature(s) — lower-tier signatures preserved, manifest extended\n", total, plan.TrustClass)
		return nil
	},
}

// envForClass builds the declared capability Env the renderer consumes. key/kms/oidc
// need none (resolved from refs); hardware declares a single presence-required,
// non-exportable token for the --sk path — cosign enforces the actual touch/PIN, so
// declaring it is the operator's assertion that such a token is attached.
func envForClass(plan sign.SignPlan) cosign.Env {
	if plan.TrustClass == sign.ClassHardware {
		return cosign.Env{FIDO2: []cosign.FIDO2Device{{
			Principal:        cosign.Principal("hardware-token"),
			PhysicalPresence: true,
			NonExportable:    true,
		}}}
	}
	return cosign.Env{}
}

// appendOutcome extends the results manifest additively — appending to the matching
// artifact's outcomes (preserving existing ones) or adding a new artifact. Never
// replaces; this is the load-bearing "layer assurance onto immutable outputs" rule.
func appendOutcome(results *artifact.ResultsManifest, id artifact.ArtifactID, name, kind string, o artifact.Outcome) {
	for i := range results.Results {
		if results.Results[i].ArtifactID == id {
			results.Results[i].Outcomes = append(results.Results[i].Outcomes, o)
			return
		}
	}
	results.Results = append(results.Results, artifact.Result{
		ArtifactID: id, ArtifactName: name, Kind: kind,
		Outcomes: []artifact.Outcome{o},
	})
}

// imageTarget is a published image digest to sign, extracted from a push outcome.
type imageTarget struct {
	artifactID artifact.ArtifactID
	name       string
	target     *artifact.OutcomeTarget
	digest     string
	digestRef  string
}

// imageTargets extracts the unique published image digests from the manifest's push
// outcomes. A digest is content-addressed, so signing it verbatim is inherently
// drift-proof; deduplicated by digest ref (a digest pushed under several tags is
// signed once).
func imageTargets(results *artifact.ResultsManifest) []imageTarget {
	seen := map[string]bool{}
	var out []imageTarget
	for _, r := range results.Results {
		for _, o := range r.Outcomes {
			if o.Type != artifact.OutcomeTypePush || o.Push == nil || o.Push.Status != artifact.OutcomeSuccess || o.Push.Digest == "" || o.Target == nil {
				continue
			}
			ref := o.Target.Host + "/" + o.Target.Path + "@" + o.Push.Digest
			if seen[ref] {
				continue
			}
			seen[ref] = true
			out = append(out, imageTarget{
				artifactID: r.ArtifactID, name: r.ArtifactName, target: o.Target,
				digest: o.Push.Digest, digestRef: ref,
			})
		}
	}
	return out
}

func init() {
	signCmd.Flags().StringVar(&signProfileID, "profile", "", "signing_profile id to sign under (required)")
	signCmd.Flags().StringVar(&signConfigFile, "config", ".stagefreight.yml", "config file")
	signCmd.Flags().BoolVar(&signSkipImages, "skip-images", false, "sign only release blobs, not published image digests")
	rootCmd.AddCommand(signCmd)
}
