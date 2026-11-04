// Package security provides vulnerability scanning and SBOM generation.
// Orchestrates external tools (Trivy, Grype, Syft) and produces structured
// results that feed into release notes and forge uploads.
package security

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// ScanConfig holds security scan configuration.
type ScanConfig struct {
	Enabled          bool                             // run vulnerability scan
	TrivyEnabled     bool                             // run Trivy scanner
	GrypeEnabled     bool                             // run Grype scanner
	SBOMEnabled      bool                             // generate SBOM
	FailOnCritical   bool                             // fail if critical vulns found
	ImageRef         string                           // image reference or tarball path to scan
	BuildID          string                           // the build that produced ImageRef, when known
	FromRegistry     bool                             // ImageRef is a published reference: scan it from its registry, never a local copy
	OCILayoutDir     string                           // OCI layout DIRECTORY to scan (content-store path). When set, takes precedence over ImageRef: scanners read the layout directly (trivy --input, grype oci-dir:) with no daemon or registry. This is how review scans the exact bytes carried from perform via the content store.
	OutputDir        string                           // directory for scan artifacts
	RootDir          string                           // workspace root for toolchain resolution
	ToolchainDesired map[string]config.ToolConstraint // from appCfg.Toolchains
	SectionWriter    io.Writer                        // writer for CI section markers (nil = os.Stderr)
	TrivyCacheMax    string                           // max_size for Trivy DB cache (full-clear when exceeded)
	TrivyCacheMaxAge string                           // max_age for Trivy DB cache (full-clear when oldest file exceeds)
	GrypeCacheMax    string                           // max_size for Grype DB cache (full-clear when exceeded)
	GrypeCacheMaxAge string                           // max_age for Grype DB cache (full-clear when oldest file exceeds)
}

// Vulnerability is a single parsed vulnerability from the scan.
type Vulnerability struct {
	ID          string // CVE ID (e.g., "CVE-2026-1234")
	Severity    string // CRITICAL, HIGH, MEDIUM, LOW
	Package     string // affected package name
	Installed   string // installed version
	FixedIn     string // version that fixes the vuln
	Description string // one-line description
	Source      string // scanner provenance: "trivy" or "grype"

	// FixedInConflict is set when scanners report DIFFERENT non-empty fix versions for
	// this same (ID, Package): the remediation claim is source-specific and must not be
	// reduced to a single canonical version. FixedInBySource records each source's fix so
	// the ambiguity is preserved (rendered as "source-specific"; detail kept here + JSON).
	FixedInConflict bool              `json:"fixed_in_conflict,omitempty"`
	FixedInBySource map[string]string `json:"fixed_in_by_source,omitempty"`
}

// RefStability classifies how stable/immutable a reference is.
type RefStability string

const (
	StabilityDigest        RefStability = "digest"          // @sha256: — content-addressed, immutable
	StabilityTagWithDigest RefStability = "tag_with_digest" // tag + known digest — resolved instance
	StabilityTag           RefStability = "tag"             // bare tag — always mutable, even semver
)

// TargetSource describes how the scan target was selected.
type TargetSource string

const (
	TargetExplicit        TargetSource = "explicit"
	TargetPositionalArg   TargetSource = "positional_arg"
	TargetPublishManifest TargetSource = "publish_manifest"
)

// CandidateInfo describes a potential scan target candidate.
type CandidateInfo struct {
	Ref               string       `json:"ref"`
	Digest            string       `json:"digest,omitempty"`
	ObservedDigest    string       `json:"observed_digest,omitempty"`
	ObservedDigestAlt string       `json:"observed_digest_alt,omitempty"`
	Stability         RefStability `json:"stability"`
}

// ScanTarget describes the resolved scan target with full provenance.
type ScanTarget struct {
	Ref               string          `json:"ref"`
	DiscoveredTag     string          `json:"discovered_tag,omitempty"` // original tag before digest resolution
	Digest            string          `json:"digest,omitempty"`
	ObservedDigest    string          `json:"observed_digest,omitempty"`
	ObservedDigestAlt string          `json:"observed_digest_alt,omitempty"`
	DigestMatch       *bool           `json:"digest_match,omitempty"`
	Source            TargetSource    `json:"source"`
	SelectionReason   string          `json:"selection_reason"`
	Stability         RefStability    `json:"stability"`
	Candidates        []CandidateInfo `json:"candidates,omitempty"`
	BuildID           string          `json:"build_id,omitempty"` // the build that produced the image, when known
	ExpectedTags      []string        `json:"expected_tags,omitempty"`
	ExpectedCommit    string          `json:"expected_commit,omitempty"`
	SigningAttempted  bool            `json:"signing_attempted,omitempty"`
}

// ScannerInfo describes a scanner that was run or attempted.
type ScannerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ScanResult holds the outcome of a security scan.
type ScanResult struct {
	Critical        int             // count of critical vulnerabilities
	High            int             // count of high vulnerabilities
	Medium          int             // count of medium vulnerabilities
	Low             int             // count of low vulnerabilities
	Vulnerabilities []Vulnerability // parsed vulnerability details (for detailed/full output)
	Status          string          // "passed", "warning", "critical"
	Artifacts       []string        // paths to generated files (JSON, SARIF, SBOM)
	Summary         string          // markdown summary for embedding in release notes
	EngineVersion   string          // best-effort: from `trivy --version` or empty
	OS              string          // "alpine 3.21.3" (from Trivy JSON Metadata.OS)
	Target          ScanTarget      // resolved scan target with provenance
	ScannersRun     []ScannerInfo   // scanners that completed successfully
	ScannersFailed  []ScannerInfo   // scanners that failed or were unavailable
	Partial         bool            // true if any enabled scanner failed
	CacheMode       string          // "persistent" or "ephemeral" — vuln DB cache location
	CacheCleared    []string        // tool names whose cache was full-cleared (over size cap)
}

// ClassifyRefStability determines the stability classification of an image reference.
func ClassifyRefStability(ref string, knownDigest string) RefStability {
	if strings.Contains(ref, "@sha256:") {
		return StabilityDigest
	}
	if knownDigest != "" {
		return StabilityTagWithDigest
	}
	return StabilityTag
}

// Scan runs vulnerability scans (Trivy + Grype when available),
// deduplicates results, and optionally generates SBOMs.
func Scan(ctx context.Context, cfg ScanConfig) (*ScanResult, error) {
	if !cfg.Enabled {
		return &ScanResult{Status: "skipped"}, nil
	}

	result := &ScanResult{}

	if cfg.OutputDir == "" {
		cfg.OutputDir = "."
	}

	// Scan target: an OCI layout directory takes precedence over an image
	// reference. When set, scanners read the exact bytes carried from perform
	// via the content store — no daemon, no registry. The ociLayoutPrefix marks
	// the target so the run* helpers select each scanner's native layout mode.
	if cfg.OCILayoutDir != "" {
		cfg.ImageRef = ociLayoutPrefix + cfg.OCILayoutDir
	}
	scanTarget := cfg.ImageRef
	if cfg.FromRegistry && cfg.OCILayoutDir == "" {
		scanTarget = registryRefPrefix + cfg.ImageRef
	}

	// Best-effort engine version (silent capture, no stdout/stderr connection).
	result.EngineVersion = buildEngineVersion()

	// Section writer for CI collapsed sections (default: os.Stderr).
	sw := cfg.SectionWriter
	if sw == nil {
		sw = os.Stderr
	}

	trivyCacheDir := resolveScannerCacheDir("trivy")
	grypeCacheDir := resolveScannerCacheDir("grype")
	if trivyCacheDir != "" || grypeCacheDir != "" {
		result.CacheMode = "persistent"
	} else {
		result.CacheMode = "ephemeral"
	}
	for _, c := range []struct{ tool, dir, maxAge, maxSize string }{
		{"trivy", trivyCacheDir, cfg.TrivyCacheMaxAge, cfg.TrivyCacheMax},
		{"grype", grypeCacheDir, cfg.GrypeCacheMaxAge, cfg.GrypeCacheMax},
	} {
		if reason := scannerCacheOverPolicy(c.dir, c.maxAge, c.maxSize); reason != "" {
			if resetScannerCache(c.dir, reason) {
				result.CacheCleared = append(result.CacheCleared, c.tool+":"+reason)
			}
		}
	}

	// Run Trivy if enabled.
	if cfg.TrivyEnabled {
		output.SectionStartCollapsed(sw, "sf_trivy_raw", "Trivy scanner (raw)")
		trivySel, trivySelErr := toolchain.Select(cfg.RootDir, "trivy", cfg.ToolchainDesired)
		trivyResult, trivyErr := provision.ResolveSelection(ctx, cfg.RootDir, "trivy", trivySel, trivySelErr, "vulnerability scan (trivy)")
		if trivyErr != nil {
			if trivySel.Pinned || trivySelErr != nil {
				return nil, fmt.Errorf("trivy pinned version %s failed to resolve: %w", trivySel.Version, trivyErr)
			}
			result.ScannersFailed = append(result.ScannersFailed, ScannerInfo{Name: "trivy"})
			result.Partial = true
			diag.Warn("trivy toolchain resolve failed: %v", trivyErr)
		} else {
			toolchain.Report(sw, trivyResult)
			// Trivy JSON scan
			jsonPath := cfg.OutputDir + "/" + ScanTrivyFile
			if err := runTrivy(ctx, trivyResult.Path, scanTarget, "json", jsonPath, trivyCacheDir); err != nil {
				result.ScannersFailed = append(result.ScannersFailed, ScannerInfo{Name: "trivy", Version: trivyResult.Version})
				result.Partial = true
				diag.Warn("trivy scan failed: %v", err)
			} else {
				// Trivy SARIF scan
				sarifPath := cfg.OutputDir + "/vulnerability-report.sarif"
				if err := runTrivy(ctx, trivyResult.Path, scanTarget, "sarif", sarifPath, trivyCacheDir); err != nil {
					diag.Warn("trivy SARIF generation failed (continuing): %v", err)
				} else {
					result.Artifacts = append(result.Artifacts, sarifPath)
				}
				result.Artifacts = append(result.Artifacts, jsonPath)
				result.ScannersRun = append(result.ScannersRun, ScannerInfo{Name: "trivy", Version: trivyResult.Version})

				// Parse Trivy vulnerabilities
				if err := parseTrivyVulnerabilities(jsonPath, result); err != nil {
					diag.Warn("parsing trivy results: %v", err)
				}
			}
		}
		output.SectionEnd(sw, "sf_trivy_raw")
	}

	// Run Grype if enabled.
	if cfg.GrypeEnabled {
		output.SectionStartCollapsed(sw, "sf_grype_raw", "Grype scanner (raw)")
		grypeSel, grypeSelErr := toolchain.Select(cfg.RootDir, "grype", cfg.ToolchainDesired)
		grypeResult, grypeErr := provision.ResolveSelection(ctx, cfg.RootDir, "grype", grypeSel, grypeSelErr, "vulnerability scan (grype)")
		if grypeErr != nil {
			if grypeSel.Pinned || grypeSelErr != nil {
				return nil, fmt.Errorf("grype pinned version %s failed to resolve: %w", grypeSel.Version, grypeErr)
			}
			result.ScannersFailed = append(result.ScannersFailed, ScannerInfo{Name: "grype"})
			result.Partial = true
			diag.Warn("grype toolchain resolve failed: %v", grypeErr)
		} else {
			toolchain.Report(sw, grypeResult)
			grypeJSON := cfg.OutputDir + "/security-scan-grype.json"
			if err := runGrype(ctx, grypeResult.Path, scanTarget, "json", grypeJSON, grypeCacheDir); err != nil {
				result.ScannersFailed = append(result.ScannersFailed, ScannerInfo{Name: "grype", Version: grypeResult.Version})
				result.Partial = true
				diag.Warn("grype scan failed (continuing without Grype): %v", err)
			} else {
				result.Artifacts = append(result.Artifacts, grypeJSON)
				result.ScannersRun = append(result.ScannersRun, ScannerInfo{Name: "grype", Version: grypeResult.Version})
				grypeVulns, parseErr := parseGrypeVulnerabilities(grypeJSON)
				if parseErr != nil {
					diag.Warn("grype parse failed (continuing without Grype): %v", parseErr)
				} else {
					result.Vulnerabilities = append(result.Vulnerabilities, grypeVulns...)
				}
			}
		}
		output.SectionEnd(sw, "sf_grype_raw")
	}

	// Deduplicate across scanners and recount.
	if len(result.Vulnerabilities) > 0 {
		result.Vulnerabilities = deduplicateVulnerabilities(result.Vulnerabilities)
		result.Critical, result.High, result.Medium, result.Low = countSeverities(result.Vulnerabilities)
	}

	// Determine status
	switch {
	case result.Critical > 0:
		result.Status = "critical"
	case result.High > 0:
		result.Status = "warning"
	default:
		result.Status = "passed"
	}

	// Generate SBOM if enabled
	if cfg.SBOMEnabled {
		output.SectionStartCollapsed(sw, "sf_syft_raw", "Syft SBOM (raw)")
		syftSel, syftSelErr := toolchain.Select(cfg.RootDir, "syft", cfg.ToolchainDesired)
		syftResult, syftErr := provision.ResolveSelection(ctx, cfg.RootDir, "syft", syftSel, syftSelErr, "SBOM generation (syft)")
		if syftErr != nil {
			output.SectionEnd(sw, "sf_syft_raw")
			if syftSel.Pinned || syftSelErr != nil {
				return nil, fmt.Errorf("syft pinned version %s failed to resolve: %w", syftSel.Version, syftErr)
			}
			return nil, fmt.Errorf("syft toolchain resolve failed: %w", syftErr)
		}
		toolchain.Report(sw, syftResult)
		spdxPath := cfg.OutputDir + "/" + SBOMSPDXFile
		spdxErr := runSyft(ctx, syftResult.Path, scanTarget, "spdx-json", spdxPath)
		if spdxErr == nil {
			result.Artifacts = append(result.Artifacts, spdxPath)
			if err := writeSBOMSubject(cfg.OutputDir, SBOMSubject{Build: cfg.BuildID, Ref: cfg.ImageRef}); err != nil {
				diag.Warn("could not record the SBOM's build: %v", err)
			}
			cdxPath := cfg.OutputDir + "/" + SBOMCycloneDXFile
			if err := runSyft(ctx, syftResult.Path, scanTarget, "cyclonedx-json", cdxPath); err != nil {
				output.SectionEnd(sw, "sf_syft_raw")
				return nil, fmt.Errorf("syft cyclonedx: %w", err)
			}
			result.Artifacts = append(result.Artifacts, cdxPath)
		}
		output.SectionEnd(sw, "sf_syft_raw")
		if spdxErr != nil {
			return nil, fmt.Errorf("syft spdx: %w", spdxErr)
		}
	}

	// Write advisory bridge artifact for dependency enrichment.
	if err := WriteAdvisories(cfg.OutputDir, cfg.ImageRef, result.Vulnerabilities); err != nil {
		diag.Warn("could not write security advisories: %v", err)
	} else {
		result.Artifacts = append(result.Artifacts, cfg.OutputDir+"/advisories.json")
	}

	return result, nil
}

// BuildSummary generates a markdown summary at the specified detail level.
// Returns (tile, body):
//   - tile: single-line status for hero area (e.g., "✅ **Passed** — no critical or high vulnerabilities")
//   - body: full section content (status line + optional <details> block with CVE data)
//
// Detail levels: "none", "counts", "detailed", "full".
//
// maxRows bounds the "full" level's vulnerability table; 0 means unbounded.
func BuildSummary(result *ScanResult, detail string, maxRows int) (tile, body string) {
	if result.Status == "skipped" || detail == "none" {
		return "", ""
	}

	tile = buildStatusTile(result)

	switch detail {
	case "full":
		body = buildFullBody(result, tile, maxRows)
	case "detailed":
		body = buildDetailedBody(result, tile)
	default: // "counts" or unrecognized
		body = tile + "\n"
	}
	return tile, body
}

// buildStatusTile produces the one-line security status.
//
// One emoji only, and it is the one that VARIES. A constant shield rendered ahead of
// the status carried no information — identical on every release — and sat adjacent to
// the status emoji, so the eye stopped twice for one meaning. The notes already label
// the line "**Security:**".
func buildStatusTile(result *ScanResult) string {
	return fmt.Sprintf("%s — %s", statusEmoji(result.Status), statusDetail(result))
}

func statusEmoji(status string) string {
	switch status {
	case "passed":
		return "✅ **Passed**"
	case "warning":
		return "⚠️ **Warning**"
	case "critical":
		return "❌ **Critical**"
	case "skipped":
		return "⏭️ **Skipped**"
	default:
		return status
	}
}

func statusDetail(result *ScanResult) string {
	total := result.Critical + result.High + result.Medium + result.Low
	if total == 0 {
		return "no vulnerabilities found"
	}
	switch {
	case result.Critical > 0 && result.High > 0:
		return fmt.Sprintf("%d critical and %d high vulnerabilities detected", result.Critical, result.High)
	case result.Critical > 0:
		return fmt.Sprintf("%d critical vulnerabilities detected", result.Critical)
	case result.High > 0:
		return fmt.Sprintf("%d high vulnerabilities detected", result.High)
	default:
		return fmt.Sprintf("%d vulnerabilities (%d medium, %d low)", total, result.Medium, result.Low)
	}
}

// vulnCountsSuffix builds a compact counts string for <summary> tags.
// Only includes non-zero severities.
func vulnCountsSuffix(result *ScanResult) string {
	var parts []string
	if result.Critical > 0 {
		parts = append(parts, fmt.Sprintf("%d critical", result.Critical))
	}
	if result.High > 0 {
		parts = append(parts, fmt.Sprintf("%d high", result.High))
	}
	if result.Medium > 0 {
		parts = append(parts, fmt.Sprintf("%d medium", result.Medium))
	}
	if result.Low > 0 {
		parts = append(parts, fmt.Sprintf("%d low", result.Low))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// mdText neutralizes scanner-supplied text for embedding in markdown bodies:
// angle brackets become entities so tag-shaped content (a CVE description
// quoting a <script> tag, say) renders as literal text instead of live HTML,
// pipes are escaped so table rows keep their columns, and newlines collapse to
// spaces so one value stays one row.
func mdText(s string) string {
	return strings.NewReplacer(
		"\r", " ", "\n", " ",
		"|", "\\|",
		"<", "&lt;", ">", "&gt;",
	).Replace(s)
}

func buildDetailedBody(result *ScanResult, tile string) string {
	var b strings.Builder
	b.WriteString(tile)
	b.WriteString("\n")

	total := result.Critical + result.High + result.Medium + result.Low
	if total == 0 {
		return b.String()
	}

	// Collapsible CVE lists for critical and high
	b.WriteString(fmt.Sprintf("\n<details>\n<summary>Vulnerability details %s</summary>\n", vulnCountsSuffix(result)))

	maxPerSeverity := 5
	for _, sev := range []string{"CRITICAL", "HIGH"} {
		vulns := filterBySeverity(result.Vulnerabilities, sev)
		if len(vulns) == 0 {
			continue
		}

		b.WriteString(fmt.Sprintf("\n#### %s Vulnerabilities\n", titleCase(sev)))
		shown := 0
		for _, v := range vulns {
			if shown >= maxPerSeverity {
				remaining := len(vulns) - maxPerSeverity
				b.WriteString(fmt.Sprintf("- ... and %d more (see full report in release assets)\n", remaining))
				break
			}
			desc := clip(v.Description, 200)
			b.WriteString(fmt.Sprintf("- **%s** — %s (%s)\n", mdText(v.ID), mdText(desc), mdText(v.Package)))
			shown++
		}
	}

	b.WriteString("\n</details>\n")
	return b.String()
}

func buildFullBody(result *ScanResult, tile string, maxRows int) string {
	var b strings.Builder
	b.WriteString(tile)
	b.WriteString("\n")

	total := result.Critical + result.High + result.Medium + result.Low
	if total == 0 {
		return b.String()
	}

	b.WriteString(fmt.Sprintf("\n<details>\n<summary>Vulnerability details %s</summary>\n\n", vulnCountsSuffix(result)))

	b.WriteString("| Severity | CVE | Package | Installed | Fixed | Description |\n")
	b.WriteString("|---|---|---|---|---|---|\n")

	// Worst-first, so a bound keeps what a reader acts on and drops the tail. Every forge
	// caps a release body and rejects an over-long one outright, so an unbounded table on
	// a large image costs the whole release rather than the rows past the limit.
	written, omitted := 0, 0
	for _, sev := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW"} {
		vulns := filterBySeverity(result.Vulnerabilities, sev)
		for _, v := range vulns {
			if maxRows > 0 && written >= maxRows {
				omitted++
				continue
			}
			written++
			sevDisplay := titleCase(sev)
			if sev == "CRITICAL" {
				sevDisplay = "**Critical**"
			}
			desc := clip(v.Description, 200)
			fixedIn := v.FixedIn
			if fixedIn == "" {
				fixedIn = "—"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
				sevDisplay, mdText(v.ID), mdText(v.Package), mdText(v.Installed), mdText(fixedIn), mdText(desc)))
		}
	}

	if omitted > 0 {
		b.WriteString(fmt.Sprintf("\n... and %d more of lower severity (see full report in release assets)\n", omitted))
	}

	b.WriteString("\n</details>\n")
	return b.String()
}

func filterBySeverity(vulns []Vulnerability, severity string) []Vulnerability {
	var out []Vulnerability
	for _, v := range vulns {
		if strings.EqualFold(v.Severity, severity) {
			out = append(out, v)
		}
	}
	return out
}

// resolveScannerCacheDir returns the leased persistent cache unit for a scanner's
// vulnerability DB, or "" when there is none (the scanner then uses its default).
func resolveScannerCacheDir(tool string) string {
	return paths.CacheUnit(tool)
}

// scannerCacheOverPolicy names the policy a scanner DB cache breaks — "age" when its
// oldest file is older than maxAge, "size" when it is larger than maxSize — or "".
// These are opaque tool-managed DBs: a cache over policy is cleared whole.
func scannerCacheOverPolicy(dir, maxAge, maxSize string) string {
	if dir == "" {
		return ""
	}
	var oldest time.Time
	var total int64
	if err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Name() == paths.UnitLeaseName {
			return nil
		}
		if oldest.IsZero() || info.ModTime().Before(oldest) {
			oldest = info.ModTime()
		}
		total += info.Size()
		return nil
	}); err != nil {
		diag.Warn("scanner cache %s: walk failed: %v", dir, err)
		return ""
	}
	if d, err := config.ParseDuration(maxAge); err == nil && d > 0 && !oldest.IsZero() && oldest.Before(time.Now().Add(-d)) {
		return "age"
	}
	if n, err := config.ParseSize(maxSize); err == nil && n > 0 && total > n {
		return "size"
	}
	return ""
}

// resetScannerCache clears dir unless another run is using it, reporting whether it did.
func resetScannerCache(dir, reason string) bool {
	done, err := paths.ResetUnit(dir)
	switch {
	case err != nil:
		diag.Warn("scanner cache %s: clear failed: %v", dir, err)
	case done:
		diag.Info("scanner cache %s over its %s policy, cleared", dir, reason)
	default:
		diag.Info("scanner cache %s over its %s policy, kept: another run is using it", dir, reason)
	}
	return done
}

func titleCase(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

func runTrivy(ctx context.Context, binPath, imageRef, format, output, cacheDir string) error {
	args := []string{"image", "--format", format, "--output", output}
	if cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}
	args = appendTrivyTarget(args, imageRef)
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Env = scannerEnv(imageRef)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// scannerEnv is CleanEnv, plus registry credentials when the target is an image reference.
func scannerEnv(target string) []string {
	env := toolchain.CleanEnv()
	if !strings.HasPrefix(target, ociLayoutPrefix) {
		env = append(env, toolchain.RegistryAuthEnv()...)
	}
	return env
}

// ociLayoutPrefix marks a scan target as an OCI layout directory rather than an
// image reference. Scan sets it on the target string when OCILayoutDir is
// configured; the run* helpers translate it to each scanner's native layout
// input mode. This keeps the scanner-input format a property of the target, not
// a separate flag threaded through every call site.
const ociLayoutPrefix = "oci-layout:"

// registryRefPrefix marks a scan target as a published reference, read from its registry.
const registryRefPrefix = "registry-ref:"

// appendTrivyTarget appends the scan target to trivy args: --input <dir> for an OCI
// layout, --image-src remote for a published reference, otherwise the target as given
// (trivy resolves a tarball, a local image or a registry ref itself).
func appendTrivyTarget(args []string, target string) []string {
	if dir, ok := strings.CutPrefix(target, ociLayoutPrefix); ok {
		return append(args, "--input", dir)
	}
	if ref, ok := strings.CutPrefix(target, registryRefPrefix); ok {
		return append(args, "--image-src", "remote", ref)
	}
	return append(args, target)
}

// grypeTarget translates a scan target to grype's (and syft's) source syntax: oci-dir:<dir>
// for an OCI layout, registry:<ref> for a published reference, otherwise as given.
func grypeTarget(target string) string {
	if dir, ok := strings.CutPrefix(target, ociLayoutPrefix); ok {
		return "oci-dir:" + dir
	}
	if ref, ok := strings.CutPrefix(target, registryRefPrefix); ok {
		return "registry:" + ref
	}
	return target
}

func runSyft(ctx context.Context, binPath, imageRef, format, output string) error {
	cmd := exec.CommandContext(ctx, binPath, grypeTarget(imageRef), "-o", format, "-v")
	cmd.Env = scannerEnv(imageRef)
	outFile, err := os.Create(output)
	if err != nil {
		return err
	}
	defer outFile.Close()
	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func parseTrivyVulnerabilities(jsonPath string, result *ScanResult) error {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}

	// Trivy JSON structure
	var report struct {
		Metadata struct {
			OS struct {
				Family string `json:"Family"`
				Name   string `json:"Name"`
			} `json:"OS"`
		} `json:"Metadata"`
		Results []struct {
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				Severity         string `json:"Severity"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
				FixedVersion     string `json:"FixedVersion"`
				Title            string `json:"Title"`
				Description      string `json:"Description"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return err
	}

	// Extract OS metadata (best-effort).
	family := strings.TrimSpace(report.Metadata.OS.Family)
	name := strings.TrimSpace(report.Metadata.OS.Name)
	if family != "" && name != "" {
		result.OS = family + " " + name
	} else if family != "" {
		result.OS = family
	} else if name != "" {
		result.OS = name
	}

	for _, r := range report.Results {
		for _, v := range r.Vulnerabilities {
			sev := strings.ToUpper(v.Severity)
			switch sev {
			case "CRITICAL":
				result.Critical++
			case "HIGH":
				result.High++
			case "MEDIUM":
				result.Medium++
			case "LOW":
				result.Low++
			}

			// Use Title if available, fall back to truncated Description
			desc := v.Title
			if desc == "" && v.Description != "" {
				desc = clip(v.Description, 200)
			}

			result.Vulnerabilities = append(result.Vulnerabilities, Vulnerability{
				ID:          v.VulnerabilityID,
				Severity:    sev,
				Package:     v.PkgName,
				Installed:   v.InstalledVersion,
				FixedIn:     v.FixedVersion,
				Description: desc,
				Source:      "trivy",
			})
		}
	}
	return nil
}

func runGrype(ctx context.Context, binPath, imageRef, format, output, cacheDir string) error {
	args := []string{grypeTarget(imageRef), "-o", format, "--file", output, "-v"}
	cmd := exec.CommandContext(ctx, binPath, args...)
	env := scannerEnv(imageRef)
	if cacheDir != "" {
		env = append(env, "GRYPE_DB_CACHE_DIR="+cacheDir)
	}
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		// Grype exits 1 when vulnerabilities are found — output is still valid.
		if _, statErr := os.Stat(output); statErr == nil {
			return nil
		}
	}
	return err
}

func parseGrypeVulnerabilities(jsonPath string) ([]Vulnerability, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}

	var report struct {
		Matches []struct {
			Vulnerability struct {
				ID          string `json:"id"`
				Severity    string `json:"severity"`
				Description string `json:"description"`
				Fix         struct {
					Versions []string `json:"versions"`
					State    string   `json:"state"`
				} `json:"fix"`
			} `json:"vulnerability"`
			Artifact struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"artifact"`
			RelatedVulnerabilities []struct {
				ID          string `json:"id"`
				Description string `json:"description"`
			} `json:"relatedVulnerabilities"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}

	var vulns []Vulnerability
	for _, m := range report.Matches {
		sev := strings.ToUpper(m.Vulnerability.Severity)

		fixedIn := ""
		if len(m.Vulnerability.Fix.Versions) > 0 {
			fixedIn = m.Vulnerability.Fix.Versions[0]
		}

		desc := m.Vulnerability.Description
		if desc == "" && len(m.RelatedVulnerabilities) > 0 {
			desc = m.RelatedVulnerabilities[0].Description
		}
		desc = clip(desc, 200)

		vulns = append(vulns, Vulnerability{
			ID:          m.Vulnerability.ID,
			Severity:    sev,
			Package:     m.Artifact.Name,
			Installed:   m.Artifact.Version,
			FixedIn:     fixedIn,
			Description: desc,
			Source:      "grype",
		})
	}
	return vulns, nil
}

// deduplicateVulnerabilities merges findings from multiple scanners.
// Key: (ID, Package). When duplicated, keeps the entry with more detail.
func deduplicateVulnerabilities(vulns []Vulnerability) []Vulnerability {
	type key struct {
		ID      string
		Package string
	}
	seen := make(map[key]int)
	var result []Vulnerability

	for _, v := range vulns {
		k := key{v.ID, v.Package}
		if idx, ok := seen[k]; ok {
			existing := result[idx]
			switch {
			case v.FixedIn != "" && existing.FixedIn == "":
				result[idx].FixedIn = v.FixedIn
			case v.FixedIn != "" && existing.FixedIn != "" && v.FixedIn != existing.FixedIn:
				// Two scanners claim DIFFERENT fixes for the same (ID, Package). These are
				// competing advisory claims from different databases, not one canonical
				// version — do not pick a winner. Preserve both per source so it renders
				// as "source-specific" rather than silently presenting one as the fix.
				result[idx].FixedInConflict = true
				if result[idx].FixedInBySource == nil {
					result[idx].FixedInBySource = map[string]string{}
					for _, s := range strings.Split(existing.Source, "+") {
						if s = strings.TrimSpace(s); s != "" {
							result[idx].FixedInBySource[s] = existing.FixedIn
						}
					}
				}
				if v.Source != "" {
					result[idx].FixedInBySource[v.Source] = v.FixedIn
				}
			}
			if len(v.Description) > len(existing.Description) {
				result[idx].Description = v.Description
			}
			// Merge source provenance with stable output.
			if v.Source != "" {
				result[idx].Source = mergeSources(existing.Source, v.Source)
			}
			continue
		}
		seen[k] = len(result)
		result = append(result, v)
	}
	return result
}

// mergeSources combines source provenance strings with dedup and stable ordering.
func mergeSources(a, b string) string {
	seen := make(map[string]bool)
	var sources []string
	for _, s := range strings.Split(a, "+") {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			sources = append(sources, s)
		}
	}
	for _, s := range strings.Split(b, "+") {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			sources = append(sources, s)
		}
	}
	sort.Strings(sources)
	return strings.Join(sources, "+")
}

// countSeverities tallies severity counts from a vulnerability slice.
func countSeverities(vulns []Vulnerability) (critical, high, medium, low int) {
	for _, v := range vulns {
		switch strings.ToUpper(v.Severity) {
		case "CRITICAL":
			critical++
		case "HIGH":
			high++
		case "MEDIUM":
			medium++
		case "LOW":
			low++
		}
	}
	return
}

// trivyVersion returns the Trivy version string, or empty if unavailable.
func trivyVersion() string {
	out, err := exec.Command("trivy", "--version").Output()
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(strings.TrimRight(ln, "\r"))
		if ln == "" {
			continue
		}
		for _, tok := range strings.Fields(ln) {
			t := strings.TrimPrefix(tok, "v")
			if strings.Count(t, ".") >= 2 && len(t) >= 5 {
				return t
			}
		}
		break
	}
	return ""
}

// grypeVersion returns the Grype version string, or empty if unavailable.
func grypeVersion() string {
	out, err := exec.Command("grype", "version", "-o", "json").Output()
	if err != nil {
		return ""
	}
	var ver struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(out, &ver) == nil {
		return ver.Version
	}
	return ""
}

// buildEngineVersion returns a version string listing available scanners.
func buildEngineVersion() string {
	var parts []string
	if v := trivyVersion(); v != "" {
		parts = append(parts, "Trivy "+v)
	}
	if v := grypeVersion(); v != "" {
		parts = append(parts, "Grype "+v)
	}
	return strings.Join(parts, " + ")
}

// clip shortens s to at most max characters, preferring to end on a complete thought.
//
// Counted in RUNES, not bytes: advisory text carries non-ASCII routinely — curly quotes,
// dashes, names — and a byte slice lands mid-rune, emitting an invalid sequence that
// renders as a replacement character exactly at the cut.
//
// Where it breaks is then chosen in tiers, because a fragment ending mid-word is a teaser
// rather than information — the reader must go and find the real text either way:
//
//  1. the last sentence end, when one is far enough in to leave something worth reading
//  2. otherwise the last word boundary
//  3. otherwise the rune cut
//
// A sentence end is taken when it retains half the budget. Rounding back further trades
// readable text for tidiness, and a clause that trails off still carries information — the
// objection was to fragments that say nothing, not to every incomplete sentence. A word
// break needs half as well, being the lesser improvement over a rune cut. A run-on with no sentence end degrades to a word break rather
// than a stub, and the ellipsis is always added — the reader is told it was shortened
// regardless of how cleanly it broke.
func clip(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max-3])

	if i := lastSentenceEnd(cut); i > len(cut)/2 {
		cut = cut[:i]
	} else if i := strings.LastIndexAny(cut, " \t\n"); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \t\n,;:.") + "..."
}

// lastSentenceEnd reports the index just past the final sentence-ending punctuation in s,
// or -1. A sentence end is [.!?] followed by whitespace and then an upper-case letter.
//
// That shape is what separates a real sentence break from the dots advisory text is full
// of: a version ("prior to 2.1.4. Fixed in…") ends a sentence and is matched, an
// abbreviation ("e.g. the") is followed by lower case and is not, and a module path
// ("golang.org/x/net") has no whitespace and is not.
func lastSentenceEnd(s string) int {
	r := []rune(s)
	for i := len(r) - 3; i >= 0; i-- {
		if r[i] != '.' && r[i] != '!' && r[i] != '?' {
			continue
		}
		if !unicode.IsSpace(r[i+1]) || !unicode.IsUpper(r[i+2]) {
			continue
		}
		return len(string(r[:i+1]))
	}
	return -1
}
