package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sofmeright/stagefreight/src/build"
	"github.com/sofmeright/stagefreight/src/output"
	"github.com/sofmeright/stagefreight/src/security"
)

var (
	secScanImage      string
	secScanOutputDir  string
	secScanSBOM       bool
	secScanFailCrit   bool
	secScanSkip       bool
	secScanDetail     string
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

	securityCmd.AddCommand(securityScanCmd)
}

func runSecurityScan(cmd *cobra.Command, args []string) error {
	if secScanSkip {
		fmt.Println("  security scan skipped")
		return nil
	}

	imageRef := secScanImage
	if imageRef == "" && len(args) > 0 {
		imageRef = args[0]
	}
	if imageRef == "" {
		// Auto-resolve from publish manifest (written by docker build)
		rootDir, _ := os.Getwd()
		if manifest, err := build.ReadPublishManifest(rootDir); err == nil && len(manifest.Published) > 0 {
			imageRef = manifest.Published[0].Ref
			fmt.Fprintf(os.Stderr, "security: resolved scan target from publish manifest: %s\n", imageRef)
		}
	}
	if imageRef == "" {
		return fmt.Errorf("--image is required (or pass image ref as argument, or run after docker build)")
	}

	// Merge CLI flags with config defaults
	scanCfg := security.ScanConfig{
		Enabled:        !secScanSkip,
		TrivyEnabled:   cfg.Security.Scanners.TrivyEnabled(),
		GrypeEnabled:   cfg.Security.Scanners.GrypeEnabled(),
		SBOMEnabled:    secScanSBOM,
		FailOnCritical: secScanFailCrit || cfg.Security.FailOnCritical,
		ImageRef:       imageRef,
		OutputDir:      secScanOutputDir,
	}
	if scanCfg.OutputDir == "" {
		scanCfg.OutputDir = cfg.Security.OutputDir
	}

	// Ensure output directory exists
	if err := os.MkdirAll(scanCfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	color := output.UseColor()
	w := os.Stdout

	ctx := context.Background()

	// Collapse raw scanner output in GitLab CI.
	output.SectionStartCollapsed(os.Stderr, "sf_security_raw", "Security scanners (raw)")

	start := time.Now()
	result, err := security.Scan(ctx, scanCfg)
	elapsed := time.Since(start)

	output.SectionEnd(os.Stderr, "sf_security_raw")

	if err != nil {
		return fmt.Errorf("security scan: %w", err)
	}

	// Collect artifacts
	artifacts := append([]string{}, result.Artifacts...)

	// Resolve detail level from rules (CLI override > tag/branch rules > default)
	detail := security.ResolveDetailLevel(cfg.Security, secScanDetail, cfg.Policies)

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
	ux := buildSecurityUX(cfg.Security.OverwhelmMessage, cfg.Security.OverwhelmLink)

	// ── Security Scan section ──
	output.SectionStart(w, "sf_security", "Security Scan")
	sec := output.NewSection(w, "Security Scan", elapsed, color)

	sec.Row("%-16s%s", "image", imageRef)
	output.ScanAuditRows(sec, output.ScanAudit{
		Engine: result.EngineVersion,
		OS:     result.OS,
	})

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
	if verbose && summaryBody != "" {
		fmt.Println()
		fmt.Print(summaryBody)
	}

	// Fail if configured and critical vulns found
	if scanCfg.FailOnCritical && result.Critical > 0 {
		return fmt.Errorf("security scan failed: %d critical vulnerabilities", result.Critical)
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
