package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/security"
)

// SecurityScanRequest is the explicit input contract for RunSecurityScan.
// Cobra command fills this from flags; CI runner fills it from config/ciCtx.
// Ctx is inside the request (matches docker.Request pattern).
type SecurityScanRequest struct {
	Ctx            context.Context
	RootDir        string
	Config         *config.Config
	Image          string  // explicit image ref; empty = auto-resolve from manifest
	OutputDir      string  // empty = from Config.Security.OutputDir
	SBOM           bool
	FailOnCritical bool
	Skip           bool
	Detail         string  // none|counts|detailed|full; empty = from config
	Strict         bool
	Verbose        bool
	Writer         io.Writer
}

var (
	secScanImage      string
	secScanOutputDir  string
	secScanSBOM       bool
	secScanFailCrit   bool
	secScanSkip       bool
	secScanDetail     string
	secScanStrict     bool
)

var securityScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Run vulnerability scan and generate SBOM",
	Long: `Scan a container image for vulnerabilities using Trivy and Grype,
then deduplicate results and optionally generate SBOM artifacts using Syft.

Individual scanners can be toggled via security.scanners in .stagefreight.yml.
Results are written to the output directory as JSON, SARIF, and SBOM files.
A markdown summary is generated at the configured detail level for embedding
in release notes.`,
	RunE: runSecurityScan,
}

func init() {
	securityScanCmd.Flags().StringVar(&secScanImage, "image", "", "image reference or tarball to scan (required)")
	securityScanCmd.Flags().StringVarP(&secScanOutputDir, "output", "o", "", "output directory for artifacts (default: from config)")
	securityScanCmd.Flags().BoolVar(&secScanSBOM, "sbom", true, "generate SBOM artifacts")
	securityScanCmd.Flags().BoolVar(&secScanFailCrit, "fail-on-critical", false, "exit non-zero if critical vulnerabilities found")
	securityScanCmd.Flags().BoolVar(&secScanSkip, "skip", false, "skip scan (for pipeline control)")
	securityScanCmd.Flags().StringVar(&secScanDetail, "security-detail", "", "override detail level for summary: none, counts, detailed, full")
	securityScanCmd.Flags().BoolVar(&secScanStrict, "strict", false,
		"fail if scan is partial, target lacks digest identity, or artifact verification fails")

	securityCmd.AddCommand(securityScanCmd)
}

// resolveCASTarget is the Phase 3 review-inversion resolver: it scans the exact
// bytes perform carried forward in the content store, not a published image.
//
// It reads outputs.json (intent + identity), picks the first docker artifact
// that has both a content digest AND a persistence handle pointing at a stored
// OCI layout, then RE-HASHES that layout before trusting it (cas.VerifyLayoutAt).
// Only a digest whose bytes are present and verify is returned — identity is
// never trusted as a bare claim. Returns ok=false (no error) when there is
// nothing to resolve this way (no store active, no handle, or verification
// fails), so the caller falls back to the legacy publication-derived path
// without changing existing behavior.
func resolveCASTarget(rootDir string, w io.Writer) (security.ScanTarget, string, bool) {
	outputs, err := artifact.ReadOutputsManifest(rootDir)
	if err != nil {
		return security.ScanTarget{}, "", false
	}
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
		// Re-hash the carried bytes before trusting them. A handle that cannot
		// be verified is treated as absent — never scanned on faith.
		if err := cas.VerifyLayoutAt(layoutDir, cas.Digest(a.Digest)); err != nil {
			fmt.Fprintf(w, "  security: content-store layout for %s failed verification, falling back: %v\n", a.Name, err)
			continue
		}
		fmt.Fprintf(w, "  security: scanning content-store artifact %s @ %s (carried from perform, re-hash verified)\n", a.Name, a.Digest)
		return security.ScanTarget{
			Ref:             string(a.Digest),
			Digest:          string(a.Digest),
			Source:          security.TargetSource("content_store"),
			SelectionReason: "content-store layout carried from perform (re-hash verified)",
			Stability:       security.StabilityDigest,
		}, layoutDir, true
	}
	return security.ScanTarget{}, "", false
}

// resolveTarget determines the scan target with full provenance tracking.
func resolveTarget(rootDir string, explicitImage string, positionalArgs []string) (security.ScanTarget, error) {
	// Priority 1: explicit --image flag
	if explicitImage != "" {
		stability := security.ClassifyRefStability(explicitImage, "")
		if stability == security.StabilityTag {
			fmt.Fprintf(os.Stderr, "security: explicit target %s is a tag reference — mutable, no digest guarantee\n", explicitImage)
		}
		return security.ScanTarget{
			Ref:             explicitImage,
			Source:          security.TargetExplicit,
			SelectionReason: "explicit --image flag",
			Stability:       stability,
		}, nil
	}

	// Priority 2: positional argument
	if len(positionalArgs) > 0 {
		ref := positionalArgs[0]
		stability := security.ClassifyRefStability(ref, "")
		if stability == security.StabilityTag {
			fmt.Fprintf(os.Stderr, "security: positional target %s is a tag reference — mutable, no digest guarantee\n", ref)
		}
		return security.ScanTarget{
			Ref:             ref,
			Source:          security.TargetPositionalArg,
			SelectionReason: "positional argument",
			Stability:       stability,
		}, nil
	}

	// Priority 3: auto-resolve from v2 manifests (outputs + results).
	// PublicationView is the consumer-side join over intent + observations;
	// selection logic operates on views, not on a v1 dual-purpose manifest.
	outputs, err := artifact.ReadOutputsManifest(rootDir)
	if err != nil {
		if errors.Is(err, artifact.ErrOutputsManifestNotFound) {
			return security.ScanTarget{}, fmt.Errorf(
				"--image is required: no outputs manifest at %s (build may not have produced any artifacts for this ref)",
				artifact.OutputsManifestPath)
		}
		return security.ScanTarget{}, fmt.Errorf("--image is required: outputs manifest unreadable: %w", err)
	}
	results, err := artifact.ReadResultsManifest(rootDir)
	if err != nil {
		if errors.Is(err, artifact.ErrResultsManifestNotFound) {
			return security.ScanTarget{}, fmt.Errorf(
				"--image is required: no results manifest at %s (build did not complete or no pushes occurred)",
				artifact.ResultsManifestPath)
		}
		return security.ScanTarget{}, fmt.Errorf("--image is required: results manifest unreadable: %w", err)
	}

	// Successful publications only. Failed push outcomes also surface in
	// BuildPublicationViews but are not scannable — the image was never
	// pushed. Filtering here keeps selection logic narrow.
	allViews := artifact.BuildPublicationViews(outputs, results)
	var pubViews []artifact.PublicationView
	for _, v := range allViews {
		if v.PushStatus == artifact.OutcomeSuccess {
			pubViews = append(pubViews, v)
		}
	}
	if len(pubViews) == 0 {
		return security.ScanTarget{}, fmt.Errorf(
			"--image is required: no successful publication outcomes in results manifest")
	}

	// Build candidate list for UX. ObservedDigestAlt is intentionally
	// absent in v2 — the cross-check warning (buildx vs registry API) is
	// emitted at record time in the docker push helper, not preserved as
	// dual-observer state in the results model.
	var candidates []security.CandidateInfo
	for _, v := range pubViews {
		candidates = append(candidates, security.CandidateInfo{
			Ref:            v.Ref(),
			Digest:         v.Digest,
			ObservedDigest: v.ObservedDigest,
			Stability:      security.ClassifyRefStability(v.Ref(), v.Digest),
		})
	}

	// Selection rules (preserved from v1):
	//   1. Prefer the first view with a non-empty digest (immutable target)
	//   2. Fall back to the first view by recorded order (bare tag)
	var selected *artifact.PublicationView
	var reason string
	for i := range pubViews {
		if pubViews[i].Digest != "" {
			selected = &pubViews[i]
			reason = fmt.Sprintf("first digest-resolved candidate (%d candidates)", len(candidates))
			break
		}
	}
	if selected == nil {
		selected = &pubViews[0]
		reason = fmt.Sprintf("first candidate by manifest order (%d candidates, all bare tags)", len(candidates))
	}

	// Build execution ref — digest ref if known, mutable tag otherwise.
	execRef := selected.Ref()
	stability := security.ClassifyRefStability(selected.Ref(), selected.Digest)
	if selected.Digest != "" {
		execRef = selected.DigestRef()
		stability = security.StabilityDigest
	}

	// Digest match check — same single-observation comparison the docker
	// helper already warned about at record time, re-surfaced at scan time
	// in case the registry has since drifted.
	var digestMatch *bool
	if selected.Digest != "" && selected.ObservedDigest != "" {
		match := selected.Digest == selected.ObservedDigest
		digestMatch = &match
		if !match {
			fmt.Fprintf(os.Stderr, "security: registry propagation lag detected: expected %s, registry served %s\n",
				selected.Digest, selected.ObservedDigest)
		}
	}

	if selected.Digest != "" {
		fmt.Fprintf(os.Stderr, "security: resolved immutable scan target from publication views\n")
	} else {
		fmt.Fprintf(os.Stderr, "security: falling back to mutable tag target from publication views\n")
	}
	for _, c := range candidates {
		marker := "   "
		if c.Ref == selected.Ref() {
			marker = " → "
		}
		fmt.Fprintf(os.Stderr, "%s%s (%s)\n", marker, c.Ref, c.Stability)
	}
	fmt.Fprintf(os.Stderr, "  selected: %s\n", reason)

	return security.ScanTarget{
		Ref:              execRef,
		DiscoveredTag:    selected.Tag,
		Digest:           selected.Digest,
		ObservedDigest:   selected.ObservedDigest,
		DigestMatch:      digestMatch,
		Source:           security.TargetPublishManifest,
		SelectionReason:  reason,
		Stability:        stability,
		Candidates:       candidates,
		ExpectedTags:     selected.ExpectedTags,
		ExpectedCommit:   selected.ExpectedCommit,
		SigningAttempted: selected.SigningAttempted,
	}, nil
}

func runSecurityScan(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	image := secScanImage
	if image == "" && len(args) > 0 {
		image = args[0]
	}
	return RunSecurityScan(SecurityScanRequest{
		Ctx:            cmd.Context(),
		RootDir:        rootDir,
		Config:         cfg,
		Image:          image,
		OutputDir:      secScanOutputDir,
		SBOM:           secScanSBOM,
		FailOnCritical: secScanFailCrit,
		Skip:           secScanSkip,
		Detail:         secScanDetail,
		Strict:         secScanStrict,
		Verbose:        verbose,
		Writer:         os.Stdout,
	})
}

// RunSecurityScan executes the full security scan pipeline from an explicit request.
// All inputs are taken from req — no package-level vars are referenced.
func RunSecurityScan(req SecurityScanRequest) error {
	if req.Skip {
		fmt.Println("  security scan skipped")
		return nil
	}

	w := req.Writer
	if w == nil {
		w = os.Stdout
	}
	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Review inversion (Phase 3): when no explicit --image is given, prefer the
	// content-store path — scan the exact bytes perform carried forward, proven
	// by re-hash, rather than depending on a successful publication. This is the
	// trust-correct target: review approves the same bytes that will be
	// published, with no requirement that a push has happened.
	//
	// Falls back to the legacy publication-derived resolveTarget when there is
	// no persisted layout to scan (e.g. content store not active, or running
	// against an already-published image by --image). The fallback preserves
	// existing behavior exactly.
	var target security.ScanTarget
	var ociLayoutDir string
	if req.Image == "" {
		if t, dir, ok := resolveCASTarget(req.RootDir, w); ok {
			target, ociLayoutDir = t, dir
		}
	}
	if ociLayoutDir == "" {
		var err error
		target, err = resolveTarget(req.RootDir, req.Image, nil)
		if err != nil {
			return err
		}
	}
	imageRef := target.Ref

	// Merge request fields with config defaults
	scanCfg := security.ScanConfig{
		Enabled:        !req.Skip,
		TrivyEnabled:   req.Config.Security.Scanners.TrivyEnabled(),
		GrypeEnabled:   req.Config.Security.Scanners.GrypeEnabled(),
		SBOMEnabled:    req.SBOM,
		FailOnCritical: req.FailOnCritical || req.Config.Security.FailOnCritical,
		ImageRef:       imageRef,
		OCILayoutDir:   ociLayoutDir,
		OutputDir:      req.OutputDir,
		RootDir:              req.RootDir,
		ToolchainDesired:     req.Config.Toolchains.Desired,
		TrivyCacheMax:    req.Config.Security.Cache.Trivy.MaxSize,
		TrivyCacheMaxAge: req.Config.Security.Cache.Trivy.MaxAge,
		GrypeCacheMax:    req.Config.Security.Cache.Grype.MaxSize,
		GrypeCacheMaxAge: req.Config.Security.Cache.Grype.MaxAge,
	}
	if scanCfg.OutputDir == "" {
		scanCfg.OutputDir = req.Config.Security.OutputDir
	}

	// Ensure output directory exists
	if err := os.MkdirAll(scanCfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	color := output.UseColor()

	scanCfg.SectionWriter = os.Stderr

	start := time.Now()
	result, err := security.Scan(ctx, scanCfg)
	elapsed := time.Since(start)

	if err != nil {
		return fmt.Errorf("security scan: %w", err)
	}

	// Set target on result
	result.Target = target

	// Run verification if target has a digest
	var verifyResult *security.VerificationResult
	if target.Digest != "" {
		// VerifyOpts.ObservedDigestAlt is intentionally omitted: v2 does not
		// preserve a second observation. The buildx-vs-registry-API
		// cross-check was already emitted at record time in the docker
		// push helper. Verify's consistency-check path now no-ops, which
		// matches the v2 single-observation model.
		verifyResult = security.Verify(ctx, security.VerifyOpts{
			ExpectedDigest:   target.Digest,
			ActualRef:        target.Ref,
			ActualTag:        target.DiscoveredTag,
			ObservedDigest:   target.ObservedDigest,
			ExpectedTags:     target.ExpectedTags,
			ExpectedCommit:   target.ExpectedCommit,
			SigningAttempted: target.SigningAttempted,
		})
	}

	// Collect artifacts
	artifacts := append([]string{}, result.Artifacts...)

	// Cross-surface reconciliation (strictly additive): collapse this image scan
	// with the audition's source Assessment by advisory ID so each vulnerability
	// records the surface(s) it was seen on. Reads the optional source catalogue,
	// writes a supplementary artifact, and prints one disclosure line — it never
	// alters result.Vulnerabilities, the counts, the gate, or existing artifacts.
	cs := security.CrossSurface(req.RootDir, result.Vulnerabilities)
	if cs != nil {
		if data, mErr := cs.Marshal(); mErr == nil {
			csPath := scanCfg.OutputDir + "/cross-surface.json"
			if os.WriteFile(csPath, data, 0o644) == nil {
				artifacts = append(artifacts, csPath)
			}
		}
	}

	// Resolve detail level from rules (CLI override > tag/branch rules > default)
	detail := security.ResolveDetailLevel(req.Config.Security, req.Detail, req.Config.Matchers)

	// Build and write summary
	_, summaryBody := security.BuildSummary(result, detail)
	var summaryPath string
	if summaryBody != "" {
		summaryPath = scanCfg.OutputDir + "/summary.md"
		if wErr := os.WriteFile(summaryPath, []byte(summaryBody), 0o644); wErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write summary: %v\n", wErr)
			summaryPath = ""
		} else {
			artifacts = append(artifacts, fmt.Sprintf("%s (detail: %s)", summaryPath, detail))
		}
	}

	// Determine status
	var status, statusDetail string
	switch result.Status {
	case "passed":
		status = "success"
		statusDetail = "passed"
	case "warning":
		status = "skipped" // yellow icon
		statusDetail = fmt.Sprintf("%d high vulnerabilities", result.High)
	case "critical":
		status = "failed"
		statusDetail = fmt.Sprintf("%d critical vulnerabilities", result.Critical)
	default:
		status = "success"
		statusDetail = result.Status
	}

	// Build SecurityUX from config + env overrides + defaults.
	ux := buildSecurityUX(req.Config.Security.OverwhelmMessage, req.Config.Security.OverwhelmLink)

	// ── Security Scan section ──
	output.SectionStart(w, "sf_security", "Security Scan")
	sec := output.NewSection(w, "Security Scan", elapsed, color)

	sec.Row("%-16s%s", "image", imageRef)
	sec.Row("%-16s%s", "source", target.Source)
	sec.Row("%-16s%s", "selection", target.SelectionReason)
	sec.Row("%-16s%s", "stability", stabilityLabel(target.Stability))
	if result.CacheMode != "" {
		cacheDetail := result.CacheMode
		if len(result.CacheCleared) > 0 {
			cacheDetail += " (cleared: " + strings.Join(result.CacheCleared, ", ") + ")"
		}
		sec.Row("%-16s%s", "db-cache", cacheDetail)
	}

	if target.Digest != "" {
		sec.Row("%-16s%s", "digest", target.Digest)
		if target.ObservedDigest != "" {
			if target.DigestMatch != nil && *target.DigestMatch {
				sec.Row("%-16s%s (match)", "observed", target.ObservedDigest)
			} else if target.DigestMatch != nil && !*target.DigestMatch {
				sec.Row("%-16s%s \u26a0 mismatch", "observed", target.ObservedDigest)
			} else {
				sec.Row("%-16s%s", "observed", target.ObservedDigest)
			}
		}
	}

	if verifyResult != nil {
		sec.Row("%-16s%s", "verification", security.ConfidenceLabel(verifyResult.Confidence))
		for _, f := range verifyResult.Failures {
			sec.Row("%-16s%s", "", "\u26a0 "+f)
		}
	}

	output.ScanAuditRows(sec, output.ScanAudit{
		Engine: result.EngineVersion,
		OS:     result.OS,
	})

	// Scanner tracking
	if len(result.ScannersRun) > 0 {
		var scannerNames []string
		for _, s := range result.ScannersRun {
			if s.Version != "" {
				scannerNames = append(scannerNames, s.Name+" "+s.Version)
			} else {
				scannerNames = append(scannerNames, s.Name)
			}
		}
		sec.Row("%-16s%s", "scanners", strings.Join(scannerNames, ", "))
	}
	if len(result.ScannersFailed) > 0 {
		var failedNames []string
		for _, s := range result.ScannersFailed {
			if s.Version != "" {
				failedNames = append(failedNames, s.Name+" "+s.Version)
			} else {
				failedNames = append(failedNames, s.Name)
			}
		}
		sec.Row("%-16s%s", "failed", strings.Join(failedNames, ", "))
		sec.Row("%-16s\u26a0 scan incomplete \u2014 %d scanner(s) failed; results may under-report", "", len(result.ScannersFailed))
	}

	// Cross-surface reconciliation (additive): how this image's vulnerabilities
	// line up with the audition's source findings, collapsed by advisory id —
	// the [source+image] overlap is where a vulnerable dependency is both
	// declared and compiled in, with reachability carried from source.
	if cs != nil {
		sec.Row("")
		sec.Row("%-16s%d advisories — %d source-only, %d image-only, %d on both",
			"cross-surface", len(cs.Vulnerabilities), cs.SourceOnly, cs.ImageOnly, cs.Both)
		// In CI the audition should have produced the source catalogue; if it's
		// missing, the artifact forwarding likely broke — surface that instead of
		// silently degrading to an image-only view.
		if !cs.SourceFound && os.Getenv("CI") == "true" {
			sec.Row("%-16s⚠ source catalogue not found — image-only; audition artifact may not have forwarded", "")
		}
		for _, line := range cs.DisclosureLines() {
			sec.Row("%-16s%s", "", line)
		}
	}

	// Vuln table gated on detail level.
	switch detail {
	case "none":
		// skip entirely
	case "counts":
		total := result.Critical + result.High + result.Medium + result.Low
		if total > 0 {
			sec.Row("")
			sec.Row("%-16s%d total (%d critical, %d high, %d medium, %d low)",
				"vulnerabilities", total, result.Critical, result.High, result.Medium, result.Low)
		}
	case "detailed":
		vulnRows := toVulnRows(result.Vulnerabilities)
		output.SectionVulns(sec, vulnRows, color, output.SoftBudget, ux)
	case "full":
		vulnRows := toVulnRows(result.Vulnerabilities)
		output.SectionVulns(sec, vulnRows, color, output.HardBudget, ux)
	default:
		// unrecognized → treat as counts
		total := result.Critical + result.High + result.Medium + result.Low
		if total > 0 {
			sec.Row("")
			sec.Row("%-16s%d total (%d critical, %d high, %d medium, %d low)",
				"vulnerabilities", total, result.Critical, result.High, result.Medium, result.Low)
		}
	}

	sec.Separator()
	output.RowStatus(sec, "status", statusDetail, status, color)
	sec.Separator()

	for _, a := range artifacts {
		sec.Row("artifact  %s", a)
	}

	sec.Close()
	output.SectionEnd(w, "sf_security")

	// Print verbose summary to stdout
	if req.Verbose && summaryBody != "" {
		fmt.Println()
		fmt.Print(summaryBody)
	}

	// Fail if configured and critical vulns found
	if scanCfg.FailOnCritical && result.Critical > 0 {
		return fmt.Errorf("security scan failed: %d critical vulnerabilities", result.Critical)
	}

	// Strict mode checks
	if req.Strict && result.Partial {
		return fmt.Errorf("strict mode: scan is partial — %d scanner(s) failed", len(result.ScannersFailed))
	}
	if req.Strict && result.Target.Stability == security.StabilityTag {
		return fmt.Errorf("strict mode: scan target is a bare tag reference — cannot guarantee artifact identity")
	}
	if req.Strict && result.Target.DigestMatch != nil && !*result.Target.DigestMatch {
		return fmt.Errorf("strict mode: registry digest mismatch — expected %s, observed %s",
			result.Target.Digest, result.Target.ObservedDigest)
	}
	if verifyResult != nil {
		if req.Strict && result.Target.SigningAttempted && verifyResult.SignatureValid == nil {
			return fmt.Errorf("strict mode: signing was configured but failed — artifact is unsigned despite key availability")
		}
		if req.Strict && verifyResult.Confidence == security.ConfidenceNone {
			return fmt.Errorf("strict mode: artifact verification failed — confidence: none (%s)",
				strings.Join(verifyResult.Failures, "; "))
		}
	}

	return nil
}

// toVulnRows converts security.Vulnerability slice to output.VulnRow slice.
func toVulnRows(vulns []security.Vulnerability) []output.VulnRow {
	rows := make([]output.VulnRow, len(vulns))
	for i, v := range vulns {
		rows[i] = output.VulnRow{
			ID:        v.ID,
			Severity:  v.Severity,
			Package:   v.Package,
			Installed: v.Installed,
			FixedIn:   v.FixedIn,
			Title:     v.Description,
		}
	}
	return rows
}

// stabilityLabel returns a human-readable label for a ref stability level.
func stabilityLabel(s security.RefStability) string {
	switch s {
	case security.StabilityDigest:
		return "digest (content-addressed, immutable)"
	case security.StabilityTagWithDigest:
		return "tag_with_digest (resolved immutable instance)"
	case security.StabilityTag:
		return "tag (mutable — tag references can be repushed)"
	default:
		return string(s)
	}
}

// extractTagFromRef extracts the tag component from an image reference.
func extractTagFromRef(ref string) string {
	// Digest refs have no tag
	if strings.Contains(ref, "@") {
		return ""
	}
	if idx := strings.LastIndex(ref, ":"); idx >= 0 {
		slash := strings.LastIndex(ref, "/")
		if idx > slash {
			return ref[idx+1:]
		}
	}
	return ""
}

// buildSecurityUX resolves overwhelm message/link from config + env overrides + defaults.
func buildSecurityUX(cfgMessage []string, cfgLink string) output.SecurityUX {
	ux := output.SecurityUX{
		OverwhelmMessage: cfgMessage,
		OverwhelmLink:    cfgLink,
	}

	// Env overrides (LookupEnv — empty string = explicit disable).
	envMsg, envMsgSet := os.LookupEnv("STAGEFREIGHT_SECURITY_OVERWHELM_MESSAGE")
	if envMsgSet {
		if envMsg == "" {
			ux.OverwhelmMessage = nil
		} else {
			lines := strings.Split(envMsg, "\n")
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], "\r")
			}
			for len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			ux.OverwhelmMessage = lines
		}
	}

	envLink, envLinkSet := os.LookupEnv("STAGEFREIGHT_SECURITY_OVERWHELM_LINK")
	if envLinkSet {
		ux.OverwhelmLink = envLink
	}

	// Defaults only when nothing configured AND nothing overridden.
	if !envMsgSet && !envLinkSet && len(cfgMessage) == 0 && cfgLink == "" {
		ux.OverwhelmMessage = output.DefaultOverwhelmMessage
		ux.OverwhelmLink = output.DefaultOverwhelmLink
	}

	return ux
}
