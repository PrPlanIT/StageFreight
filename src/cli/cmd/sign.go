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

		sumsPath := filepath.Join(distDir, "SHA256SUMS")
		if _, statErr := os.Stat(sumsPath); statErr != nil {
			return fmt.Errorf("no SHA256SUMS to sign in %s — does this target produce checksums?", distDir)
		}

		plan := sign.Compile(profile)
		if !sign.Enabled(plan) {
			return fmt.Errorf("profile %q does not resolve to a usable signer (e.g. an unset key reference)", signProfileID)
		}

		// Distinct output preserves any lower-tier signature (e.g. SHA256SUMS.sig).
		outSig := filepath.Join(distDir, "SHA256SUMS."+signProfileID+".sig")
		fmt.Printf("Signing SHA256SUMS under profile %q (class %s)…\n", signProfileID, plan.TrustClass)
		if plan.TrustClass == sign.ClassHardware {
			fmt.Println("  → touch your security key when it blinks (and enter the PIN if prompted).")
		}
		if err := cosign.SignBlob(cmd.Context(), rootDir, cfg.Toolchains.Desired, sumsPath, outSig, plan, envForClass(plan)); err != nil {
			return err
		}

		// Additive recording: EXTEND the manifest with new evidence, never replace.
		signerRef := sign.SignerRef(plan)
		if signerRef == "" {
			signerRef = "profile:" + signProfileID
		}
		appendOutcome(results, artifact.NewArtifactID("checksums", "SHA256SUMS"), "SHA256SUMS", "checksums",
			artifact.Outcome{
				Type: artifact.OutcomeTypeBlobSignature,
				BlobSignature: &artifact.BlobSignatureOutcome{
					Status: artifact.OutcomeSuccess, Kind: "cosign",
					BlobPath: sumsPath, SignaturePath: outSig,
					TrustEvidence: artifact.TrustEvidence{
						TrustClass:       string(plan.TrustClass),
						PhysicalPresence: plan.RequiresPhysicalPresence,
						NonExportable:    plan.RequiresNonExportableKey,
						Transparency:     plan.TransparencyRequired,
						SignerRef:        signerRef,
						SignedAt:         time.Now().UTC().Format(time.RFC3339),
					},
				},
			})
		if err := artifact.WriteResultsManifest(rootDir, *results); err != nil {
			return fmt.Errorf("recording signature: %w", err)
		}

		fmt.Printf("✓ attached %s signature → %s\n", plan.TrustClass, filepath.Base(outSig))
		fmt.Println("  (lower-tier signatures preserved; results manifest extended)")
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

func init() {
	signCmd.Flags().StringVar(&signProfileID, "profile", "", "signing_profile id to sign under (required)")
	signCmd.Flags().StringVar(&signConfigFile, "config", ".stagefreight.yml", "config file")
	rootCmd.AddCommand(signCmd)
}
