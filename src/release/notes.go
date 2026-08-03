// Package release handles release notes generation, release creation,
// and cross-platform sync.
package release

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/release/trustdisclosure"
	"github.com/PrPlanIT/StageFreight/src/sign/provision"
)

// CommitCategory represents a group of commits by type.
type CommitCategory struct {
	Title   string // display title (e.g., "Features", "Bug Fixes")
	Prefix  string // conventional commit prefix (e.g., "feat", "fix")
	Commits []Commit
}

// Commit is a parsed conventional commit.
type Commit struct {
	Hash     string
	Type     string // feat, fix, chore, etc.
	Scope    string // optional scope in parens
	Summary  string
	Body     string
	Author   string
	Breaking bool
}

var conventionalRe = regexp.MustCompile(`^(\w+)(?:\(([^)]+)\))?(!)?\s*:\s*(.+)`)

// categoryOrder defines the display order for release notes.
var categoryOrder = []struct {
	prefix string
	title  string
}{
	{"BREAKING", "Breaking Changes"},
	{"feat", "Features"},
	{"fix", "Bug Fixes"},
	{"perf", "Performance"},
	{"security", "Security"},
	{"refactor", "Refactoring"},
	{"docs", "Documentation"},
	{"test", "Tests"},
	{"ci", "CI/CD"},
	{"chore", "Maintenance"},
	{"style", "Style"},
	{"migration", "Migrations"},
	{"hotfix", "Hotfixes"},
}

// ResolvedTag is a single tag with its deterministic UI URL.
type ResolvedTag struct {
	Name string // e.g., "1.0.0"
	URL  string // provider-derived tag page URL
}

// ImageRow is a single registry/image row for the Image Availability table.
type ImageRow struct {
	RegistryLabel string        // human label (e.g., "Docker Hub")
	RegistryURL   string        // provider-derived repo page URL
	ImageRef      string        // full image ref (e.g., "docker.io/prplanit/stagefreight")
	Tags          []ResolvedTag // resolved tags with URLs
	DigestRef     string        // host/path@sha256:... (for pull command)
	SBOM          string        // pull ref for SBOM artifact
	Provenance    string        // pull ref for provenance artifact
	Signature     string        // pull ref for signature artifact
}

// BinaryRow is a single binary or archive artifact for the Downloads table.
type BinaryRow struct {
	Name     string // filename (e.g., "stagefreight-linux-amd64.tar.gz")
	Platform string // "linux/amd64", "darwin/arm64"
	Size     int64  // bytes
	SHA256   string // hex-encoded checksum
}

// The structured trust disclosure lives in package trustdisclosure (interpretation,
// pure); this file is the PROSE FORMATTER over it — it decides only how to render the
// typed facts as markdown, never what the facts are.

// NotesInput holds all data needed to render release notes.
type NotesInput struct {
	RepoDir      string                      // git repository directory
	FromRef      string                      // start ref (empty = auto-detect previous tag)
	ToRef        string                      // end ref (default: HEAD)
	TagPatterns  []string                    // regex patterns for release tags (from versioning.tag_sources)
	Config       *config.Config              // config for auto-detect version (nil = skip auto-detect)
	SecurityTile string                      // one-line status (e.g., "🛡️ ✅ **Passed** — no vulnerabilities")
	SecurityBody string                      // full section: status line + optional <details> CVE block
	TagMessage   string                      // annotated tag message (optional, auto-detected if empty)
	ProjectName  string                      // project name (auto-detected if empty)
	Version      string                      // version string (auto-detected if empty)
	SHA          string                      // short commit hash (auto-detected if empty)
	IsPrerelease bool                        // true if version has prerelease suffix
	ReleaseType  string                      // resolved semantic type label (latest/prerelease/stable); overrides IsPrerelease for the "Release type:" line
	Images       []ImageRow                  // resolved registry image rows for availability table
	Downloads    []BinaryRow                 // binary/archive artifacts for downloads table
	Verify       *trustdisclosure.Disclosure // signing/verification disclosure (nil = nothing signed)

	// NotesBody is the release-notes stencil body composing this document —
	// resolved by the caller from the release target's notes: reference.
	// Empty = SF's shipped default body.
	NotesBody string

	// ResolveStencil renders a declared stencil by id for body embeds beyond the
	// release elements — text compositions, AI stencils, whatever the author
	// chose. The release elements are passed through so an embedded stencil (an
	// AI reword of {release.changes}, say) can ingest them. Supplied by the
	// caller (the stencil library lives in config); nil leaves non-release
	// tokens literal.
	ResolveStencil func(id string, elements map[string]string) (string, bool)
}

// GenerateNotes produces markdown release notes from git log between two refs.
func GenerateNotes(input NotesInput) (string, error) {
	if input.ToRef == "" {
		input.ToRef = "HEAD"
	}

	// Find previous tag if not specified
	if input.FromRef == "" {
		prev, err := PreviousReleaseTag(input.RepoDir, input.ToRef, input.TagPatterns)
		if err != nil || prev == "" {
			input.FromRef = ""
		} else {
			input.FromRef = prev
		}
	}

	// Auto-detect project metadata if not provided.
	// Requires input.Config — without it, auto-detect is skipped and the
	// caller is responsible for supplying Version/SHA directly.
	if (input.ProjectName == "" || input.Version == "" || input.SHA == "") && input.Config != nil {
		if vi, err := build.DetectVersion(input.RepoDir, input.Config); err == nil {
			if input.Version == "" {
				input.Version = vi.Version
			}
			if input.SHA == "" {
				input.SHA = vi.SHA
				if len(input.SHA) > 8 {
					input.SHA = input.SHA[:8]
				}
			}
			if !input.IsPrerelease {
				input.IsPrerelease = vi.IsPrerelease
			}
		}
		if input.ProjectName == "" {
			pm := gitver.DetectProject(input.RepoDir)
			if pm != nil {
				input.ProjectName = pm.Name
			}
		}
	}

	// Auto-detect tag message
	if input.TagMessage == "" {
		input.TagMessage = tagMessage(input.RepoDir, input.ToRef)
	}

	// Get commits
	commits, err := ParseCommits(input.RepoDir, input.FromRef, input.ToRef)
	if err != nil {
		return "", err
	}

	// Categorize
	categories := categorize(commits)

	return renderNotes(input, categories, commits), nil
}

// previousReleaseTag finds the most recent release tag that is an ancestor of
// currentRef and matches the configured tag patterns. It replaces the naive
// git-describe approach which matched any tag (including rolling aliases like
// "latest" or bare-version aliases like "0.1.0").
func PreviousReleaseTag(repoDir, currentRef string, tagPatterns []string) (string, error) {
	currentVersion := normalizeReleaseVersion(currentRef)

	matchers, err := compileReleaseTagMatchers(tagPatterns)
	if err != nil {
		return "", err
	}

	tags, err := listTagsByVersion(repoDir)
	if err != nil {
		return "", err
	}

	for _, tag := range tags {
		if !matchesAnyTagPattern(tag, matchers) {
			continue
		}

		// Do not treat same-version aliases as the "previous" release.
		// Example: currentRef=v0.1.0, stale alias tag=0.1.0
		if normalizeReleaseVersion(tag) == currentVersion {
			continue
		}

		ok, err := isAncestor(repoDir, tag, currentRef)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}

		return tag, nil
	}

	return "", nil
}

// compileReleaseTagMatchers compiles tag patterns into regex matchers.
// Falls back to a default semver pattern when no patterns are provided.
func compileReleaseTagMatchers(patterns []string) ([]*regexp.Regexp, error) {
	if len(patterns) == 0 {
		patterns = []string{`^v?\d+\.\d+\.\d+$`}
	}

	matchers := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile release tag pattern %q: %w", pattern, err)
		}
		matchers = append(matchers, re)
	}

	if len(matchers) == 0 {
		re, err := regexp.Compile(`^v?\d+\.\d+\.\d+$`)
		if err != nil {
			return nil, fmt.Errorf("compile default release tag pattern: %w", err)
		}
		matchers = append(matchers, re)
	}

	return matchers, nil
}

// matchesAnyTagPattern returns true if the tag matches at least one pattern.
func matchesAnyTagPattern(tag string, matchers []*regexp.Regexp) bool {
	for _, re := range matchers {
		if re.MatchString(tag) {
			return true
		}
	}
	return false
}

// normalizeReleaseVersion strips refs/tags/ prefix and optional v-prefix
// for same-version comparison.
func normalizeReleaseVersion(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "refs/tags/")
	ref = strings.TrimPrefix(ref, "v")
	return ref
}

// listTagsByVersion returns all git tags sorted by version descending.
func listTagsByVersion(repoDir string) ([]string, error) {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return nil, fmt.Errorf("opening repo: %w", err)
	}
	return gitstate.ListTagsSorted(repo)
}

// isAncestor returns true if ancestorRef is an ancestor of descendantRef.
func isAncestor(repoDir, ancestorRef, descendantRef string) (bool, error) {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return false, fmt.Errorf("opening repo: %w", err)
	}
	return gitstate.IsAncestor(repo, ancestorRef, descendantRef)
}

// tagMessage extracts the annotation message from an annotated tag.
// Returns empty for lightweight tags or on error.
func tagMessage(repoDir, ref string) string {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return ""
	}
	return gitstate.TagMessage(repo, ref)
}

// bulletize converts a multi-line text into markdown bullets.
// Lines already starting with "- " are kept as-is.
func bulletize(text string) string {
	lines := strings.Split(text, "\n")
	var bullets []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			line = "- " + line
		}
		bullets = append(bullets, line)
	}
	return strings.Join(bullets, "\n")
}

// renderVerification renders the "## Verification" section from the STRUCTURED
// disclosure — the assurance tier stated plainly plus a class-appropriate verify
// recipe, so a consumer knows exactly what guarantees they receive (not just
// "signed"). This is pure presentation: it formats typed facts, it never interprets
// which signature is primary or which layers matter (that is trustdisclosure's job).
func renderVerification(d *trustdisclosure.Disclosure) string {
	yn := func(t bool) string {
		if t {
			return "yes"
		}
		return "no"
	}
	var p trustdisclosure.SignatureFact
	if d.Primary != nil {
		p = *d.Primary
	}
	var b strings.Builder
	b.WriteString("## Verification\n\n")
	b.WriteString("| Property | Value |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| Signing tier | %s |\n", signerTierLabel(p)))
	if p.TrustDomain != "" {
		b.WriteString(fmt.Sprintf("| Trust domain | %s |\n", p.TrustDomain))
	}
	if d.Anchor != nil && d.Anchor.Fingerprint != "" {
		b.WriteString(fmt.Sprintf("| Public key fingerprint | `%s` |\n", d.Anchor.Fingerprint))
	}
	b.WriteString(fmt.Sprintf("| Transparency log | %s |\n", yn(p.Transparency)))
	// Continuity is meaningful only for a persistent anchor identity; for other
	// signers it is simply not asserted (not "unknown trust").
	if d.Anchor != nil {
		b.WriteString("| Signer continuity | stable |\n")
	}
	b.WriteString(fmt.Sprintf("| Human authorization required | %s |\n", yn(p.PhysicalPresence)))
	b.WriteString(fmt.Sprintf("| Non-exportable key | %s |\n\n", yn(p.NonExportable)))

	// Verify recipe — evidence-driven, NOT gated on a Tier-0 anchor. A continuity
	// anchor (a published cosign.pub) gives the pinnable --key recipe; otherwise the
	// recipe is class-appropriate so oidc/kms/hardware-only releases still disclose
	// how to verify.
	switch {
	case d.Anchor != nil && d.ChecksumSig() != "":
		tlog := " \\\n  --insecure-ignore-tlog=true"
		if p.Transparency {
			tlog = ""
		}
		b.WriteString("Verify the release checksums against the published public key:\n\n")
		b.WriteString(fmt.Sprintf("```\ncosign verify-blob \\\n  --key %s \\\n  --signature %s%s \\\n  SHA256SUMS\n```\n\n",
			d.Anchor.Asset, d.ChecksumSig(), tlog))
		if d.Anchor.Fingerprint != "" {
			b.WriteString(fmt.Sprintf("Pin the key by its fingerprint `%s` — it is stable across releases; see `SECURITY.md` for the canonical trust anchor.\n\n", d.Anchor.Fingerprint))
		}
	case p.Class == "oidc":
		domain := p.TrustDomain
		if domain == "" {
			domain = "the Sigstore"
		}
		b.WriteString(fmt.Sprintf("This release is keyless-signed in **%s** trust domain — the signature is bound to an OIDC identity by Fulcio and (when transparency is on) logged in Rekor. Verify the checksums against the expected identity:\n\n", domain))
		b.WriteString("```\ncosign verify-blob \\\n  --certificate-oidc-issuer <issuer> \\\n  --certificate-identity <signer> \\\n  --signature SHA256SUMS.sig \\\n  SHA256SUMS\n```\n\n")
		if p.SignerRef != "" {
			b.WriteString(fmt.Sprintf("Expected signer: `%s`. ", p.SignerRef))
		}
		b.WriteString("Obtain the issuer URL and (for a self-hosted domain) the Fulcio trusted-root from `SECURITY.md`.\n\n")
	case p.SignerRef != "":
		b.WriteString(fmt.Sprintf("This release is signed by `%s` (trust class **%s**). Verify the checksums against that signer's published public key:\n\n", p.SignerRef, p.Class))
		b.WriteString("```\ncosign verify-blob \\\n  --key <signer-public-key> \\\n  --signature SHA256SUMS.sig \\\n  SHA256SUMS\n```\n\n")
		b.WriteString("Obtain the public key from `SECURITY.md` / the maintainer.\n\n")
	}
	if len(d.Layers) > 0 {
		b.WriteString("This release also carries additional signatures on the same artifacts:\n\n")
		for _, l := range d.Layers {
			b.WriteString(fmt.Sprintf("- %s\n", describeSignatureFact(l)))
		}
		b.WriteString("\nObtain those signers' public keys / identities from the maintainer / `SECURITY.md`.\n\n")
	}
	// Provenance attestations are disclosed SEPARATELY from signatures: a signature
	// vouches for the artifact's bytes; a provenance attestation vouches for HOW it
	// was built, authorized by a named tier. Conflating them would overstate trust.
	if len(d.Attestations) > 0 {
		b.WriteString("Build provenance is cryptographically attested on the published image(s) — authenticated by the trust tier shown, not merely generated:\n\n")
		for _, a := range d.Attestations {
			b.WriteString(fmt.Sprintf("- %s\n", describeAttestationFact(a)))
		}
		b.WriteString("\nVerify with `cosign verify-attestation --type slsaprovenance --key <key> <image>@<digest>`.\n\n")
	}
	return b.String()
}

// signerTierLabel is the human headline for the primary signer: its assurance tier
// when recorded (e.g. Tier-0), else a class-based label so non-tiered signers read
// meaningfully. Prose lives here, in the formatter — never in trustdisclosure.
func signerTierLabel(f trustdisclosure.SignatureFact) string {
	if f.Tier != "" {
		return tierLabel(f.Tier)
	}
	switch f.Class {
	case "oidc":
		return "keyless (OIDC identity)"
	case "kms":
		return "KMS / managed key"
	case "hardware":
		return "hardware (operator-held key)"
	case "key":
		return "key (operator-supplied)"
	}
	return "signed"
}

func tierLabel(tier string) string {
	if tier == provision.TierSoftware {
		return "Tier-0 (persistent software key)"
	}
	return tier
}

// describeSignatureFact renders one additional-layer signature line.
func describeSignatureFact(f trustdisclosure.SignatureFact) string {
	cls := f.Class
	if cls == "" {
		cls = "signature"
	}
	desc := cls
	if f.Tier != "" {
		desc += " (" + tierLabel(f.Tier) + ")"
	}
	if f.TrustDomain != "" {
		desc += " (trust domain: " + f.TrustDomain + ")"
	}
	if f.PhysicalPresence {
		desc += " (human-authorized)"
	}
	if f.NonExportable {
		desc += ", non-exportable"
	}
	if f.Asset != "" {
		desc += " · " + f.Asset
	}
	return desc
}

// describeAttestationFact renders one provenance-attestation line.
func describeAttestationFact(a trustdisclosure.AttestationFact) string {
	pt := a.PredicateType
	if pt == "" {
		pt = "provenance"
	}
	cls := a.Class
	if cls == "" {
		cls = "signature"
	}
	desc := pt + " · " + cls
	if a.TrustDomain != "" {
		desc += " (trust domain: " + a.TrustDomain + ")"
	}
	if a.Tier != "" {
		desc += " (" + tierLabel(a.Tier) + ")"
	}
	if a.PhysicalPresence {
		desc += ", human-authorized"
	}
	if a.NonExportable {
		desc += ", non-exportable"
	}
	if a.VerifiedDigest != "" {
		desc += " · " + a.VerifiedDigest
	}
	return desc
}

// formatBytes formats a byte count as a human-readable size.

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// truncHash returns the first 12 chars of a hex hash for compact display.
func truncHash(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

// releaseType returns a human-readable release type.
func releaseType(isPrerelease bool) string {
	if isPrerelease {
		return "prerelease"
	}
	return "stable"
}

// ParseCommits extracts conventional commits from a git log range.
func ParseCommits(repoDir, fromRef, toRef string) ([]Commit, error) {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return nil, fmt.Errorf("opening repo: %w", err)
	}

	rawCommits, err := gitstate.ParseCommitLog(repo, fromRef, toRef)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	var commits []Commit
	for _, raw := range rawCommits {
		subject, body := splitCommitMessage(raw.Message)
		hash := raw.Hash.String()
		if len(hash) > 7 {
			hash = hash[:7]
		}
		c := Commit{
			Hash:    hash,
			Summary: subject,
			Body:    body,
			Author:  raw.Author.Name,
		}

		// Parse conventional commit
		if m := conventionalRe.FindStringSubmatch(c.Summary); m != nil {
			c.Type = strings.ToLower(m[1])
			c.Scope = m[2]
			c.Breaking = m[3] == "!" || strings.Contains(strings.ToUpper(c.Body), "BREAKING CHANGE")
			c.Summary = m[4]
		}

		// Detect breaking change from body even without prefix
		if strings.Contains(strings.ToUpper(c.Body), "BREAKING CHANGE") {
			c.Breaking = true
		}

		commits = append(commits, c)
	}

	return commits, nil
}

// splitCommitMessage splits a raw git commit message into subject and body.
func splitCommitMessage(msg string) (subject, body string) {
	msg = strings.TrimSpace(msg)
	parts := strings.SplitN(msg, "\n", 2)
	subject = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		body = strings.TrimSpace(parts[1])
	}
	return
}

func categorize(commits []Commit) []CommitCategory {
	buckets := make(map[string][]Commit)
	for _, c := range commits {
		key := c.Type
		if c.Breaking {
			key = "BREAKING"
		}
		if key == "" {
			key = "other"
		}
		buckets[key] = append(buckets[key], c)
	}

	var categories []CommitCategory
	for _, cat := range categoryOrder {
		if cs, ok := buckets[cat.prefix]; ok {
			categories = append(categories, CommitCategory{
				Title:   cat.title,
				Prefix:  cat.prefix,
				Commits: cs,
			})
			delete(buckets, cat.prefix)
		}
	}

	// Any remaining uncategorized commits
	var otherCommits []Commit
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		otherCommits = append(otherCommits, buckets[k]...)
	}
	if len(otherCommits) > 0 {
		categories = append(categories, CommitCategory{
			Title:   "Other Changes",
			Prefix:  "other",
			Commits: otherCommits,
		})
	}

	return categories
}

// Categorize groups parsed conventional commits into ordered display categories
// (Features, Bug Fixes, …) — the same grouping release notes use. Exported so
// narrate can build its Changes section from ONE changelog source: generated once,
// rendered many ways (summary / release body / community note).
func Categorize(commits []Commit) []CommitCategory {
	return categorize(commits)
}

// defaultReleaseNotesBody is the shipped release-notes stencil body: the
// natural language of the notes — hero lines, the security tile label, the
// rule, the changelog wrapper — authored here, with {} filled by facts
// ({project}, {version}, {sha}, {release.type}) and the conditional section
// widgets (which carry their own headings/wrappers and elide wholly with
// their data). Override it by declaring a `release-notes` text stencil in
// stencils: — reword the language, reorder or delete sections, add your own
// markdown between them.
const defaultReleaseNotesBody = "## 📦 {project} — `v{version}`\n" +
	"> **Release type:** {release.type} • **Commit:** `{sha}`\n" +
	"\n" +
	"**Security:** {release.security-tile}\n" +
	"\n" +
	"{release.images}\n" +
	"{release.downloads}\n" +
	"{release.verification}\n" +
	"{release.highlights}\n" +
	"{release.changes}\n" +
	"{release.security}\n" +
	"---\n" +
	"\n" +
	"<details>\n" +
	"<summary>Full changelog</summary>\n" +
	"\n" +
	"{release.changelog}\n" +
	"\n" +
	"</details>\n"

// renderNotes composes the release body from facts + section widgets through
// the release-notes stencil body (shipped default, or the config's override).
func renderNotes(input NotesInput, categories []CommitCategory, allCommits []Commit) string {
	project := input.ProjectName
	if project == "" {
		project = "release"
	}
	version := input.Version
	if version == "" {
		version = "unreleased"
	}
	rtLabel := input.ReleaseType
	if rtLabel == "" {
		rtLabel = releaseType(input.IsPrerelease)
	}

	elements := map[string]string{
		// Facts — data, one render.
		"project":               project,
		"version":               version,
		"sha":                   input.SHA,
		"release.type":          rtLabel,
		"release.security-tile": input.SecurityTile,
		// Conditional section widgets — heading/wrapper + data as one unit, so
		// the whole section vanishes when its data is absent.
		"release.images":       sectionImages(input),
		"release.downloads":    sectionDownloads(input),
		"release.verification": sectionVerification(input),
		"release.highlights":   sectionHighlights(input),
		"release.changes":      sectionChanges(categories),
		"release.security":     sectionSecurity(input),
		"release.changelog":    sectionChangelog(allCommits),
	}

	body := input.NotesBody
	if body == "" {
		body = defaultReleaseNotesBody
	}
	resolveStencil := func(id string) (string, bool) {
		if input.ResolveStencil == nil {
			return "", false
		}
		return input.ResolveStencil(id, elements)
	}
	return composeNotesBody(body, elements, resolveStencil)
}

// composeNotesBody renders a release-notes body line by line. A line consisting
// of exactly one {element} places that element's bytes VERBATIM (the element
// carries its own newlines — generated tables and code fences survive
// byte-exact). Any other line is authored markdown with known {element} tokens
// substituted inline (trimmed) and unknown tokens left literal. Elision matches
// the stencil engine's law at this format's granularity: a line whose every
// known element resolved empty drops whole — and takes ONE immediately
// following blank body line (its separator) with it.
func composeNotesBody(body string, elements map[string]string, resolveStencil func(string) (string, bool)) string {
	resolve := func(name string) (string, bool) {
		if content, ok := elements[name]; ok {
			return content, true
		}
		if resolveStencil != nil {
			return resolveStencil(name)
		}
		return "", false
	}

	var b strings.Builder
	lines := strings.Split(body, "\n")
	eatBlank := false
	for i, line := range lines {
		last := i == len(lines)-1
		if eatBlank {
			eatBlank = false
			if line == "" {
				continue
			}
		}
		if name, ok := blockElementName(line); ok {
			if content, known := resolve(name); known {
				if content == "" {
					eatBlank = true
					continue
				}
				b.WriteString(content)
				continue
			}
		}
		out, nonEmpty, empty := substituteNotesLine(line, resolve)
		if empty > 0 && nonEmpty == 0 {
			eatBlank = true
			continue
		}
		b.WriteString(out)
		if !last {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// substituteNotesLine replaces resolvable {element} tokens inline (trimmed),
// leaving unknown tokens literal, and returns the elision accounting.
func substituteNotesLine(line string, resolve func(string) (string, bool)) (out string, nonEmpty, empty int) {
	// Scan tokens left to right; resolved output is spliced in and never re-scanned.
	var b strings.Builder
	rest := line
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			b.WriteString(rest)
			break
		}
		close := strings.IndexByte(rest[open:], '}')
		if close < 0 {
			b.WriteString(rest)
			break
		}
		close += open
		name := rest[open+1 : close]
		content, ok := resolve(name)
		if !ok {
			b.WriteString(rest[:close+1])
			rest = rest[close+1:]
			continue
		}
		if content == "" {
			empty++
		} else {
			nonEmpty++
		}
		b.WriteString(rest[:open])
		b.WriteString(strings.TrimRight(content, "\n"))
		rest = rest[close+1:]
	}
	return b.String(), nonEmpty, empty
}

// blockElementName reports whether a body line is exactly one {element} token.
func blockElementName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if len(t) > 2 && strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") && !strings.ContainsAny(t[1:len(t)-1], "{}") {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// sectionImages renders the Image Availability table with its supply-chain extras.
func sectionImages(input NotesInput) string {
	if len(input.Images) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Image Availability\n\n")
	b.WriteString("| Registry | Image | Tags |\n")
	b.WriteString("|----------|-------|------|\n")
	for _, img := range input.Images {
		// Registry cell: linked label or plain text
		var regCell string
		if img.RegistryURL != "" {
			regCell = fmt.Sprintf("[%s](%s)", img.RegistryLabel, img.RegistryURL)
		} else {
			regCell = img.RegistryLabel
		}

		// Tags cell: linked code spans or plain code
		tagParts := make([]string, 0, len(img.Tags))
		for _, t := range img.Tags {
			if t.URL != "" {
				tagParts = append(tagParts, fmt.Sprintf("[`%s`](%s)", t.Name, t.URL))
			} else {
				tagParts = append(tagParts, fmt.Sprintf("`%s`", t.Name))
			}
		}

		b.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n", regCell, img.ImageRef, strings.Join(tagParts, " ")))
	}
	b.WriteString("\n")

	// Digest pull commands and artifact links
	hasExtras := false
	for _, img := range input.Images {
		if img.DigestRef != "" || img.SBOM != "" || img.Provenance != "" || img.Signature != "" {
			hasExtras = true
			break
		}
	}
	if hasExtras {
		b.WriteString("<details>\n<summary>Digest pull commands & supply chain artifacts</summary>\n\n")
		for _, img := range input.Images {
			if img.DigestRef == "" && img.SBOM == "" && img.Provenance == "" && img.Signature == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("**%s**\n", img.ImageRef))
			if img.DigestRef != "" {
				b.WriteString(fmt.Sprintf("```\ndocker pull %s\n```\n", img.DigestRef))
			}
			if img.SBOM != "" {
				b.WriteString(fmt.Sprintf("- SBOM: `%s`\n", img.SBOM))
			}
			if img.Provenance != "" {
				b.WriteString(fmt.Sprintf("- Provenance: `%s`\n", img.Provenance))
			}
			if img.Signature != "" {
				b.WriteString(fmt.Sprintf("- Signature: `%s`\n", img.Signature))
			}
			b.WriteString("\n")
		}
		b.WriteString("</details>\n\n")
	}
	return b.String()
}

// sectionDownloads renders the Downloads table + full checksums.
func sectionDownloads(input NotesInput) string {
	if len(input.Downloads) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Downloads\n\n")
	b.WriteString("| Platform | File | Size | SHA-256 |\n")
	b.WriteString("|----------|------|------|---------|\n")
	for _, dl := range input.Downloads {
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | `%s` |\n",
			dl.Platform, dl.Name, formatBytes(dl.Size), truncHash(dl.SHA256)))
	}
	b.WriteString("\n")

	// Full checksums in collapsible block
	b.WriteString("<details>\n<summary>Full checksums</summary>\n\n")
	b.WriteString("```\n")
	for _, dl := range input.Downloads {
		b.WriteString(fmt.Sprintf("%s  %s\n", dl.SHA256, dl.Name))
	}
	b.WriteString("```\n</details>\n\n")
	return b.String()
}

// sectionVerification renders the signing-tier disclosure — the tier stated
// plainly plus the verify recipe, so "signed" never collapses distinct trust
// models into one badge.
func sectionVerification(input NotesInput) string {
	if input.Verify == nil {
		return ""
	}
	return renderVerification(input.Verify)
}

// sectionHighlights renders the annotated-tag message as bullets.
func sectionHighlights(input NotesInput) string {
	if input.TagMessage == "" {
		return ""
	}
	return "## Highlights\n" + bulletize(input.TagMessage) + "\n\n"
}

// sectionChanges renders Notable Changes (H2 wrapper, H4 categories),
// deduplicating commits within each category by summary+scope+author.
func sectionChanges(categories []CommitCategory) string {
	if len(categories) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Notable Changes\n\n")
	for _, cat := range categories {
		b.WriteString(fmt.Sprintf("#### %s\n", cat.Title))
		type dedupKey struct{ scope, summary, author string }
		seen := make(map[dedupKey]int)
		type dedupEntry struct {
			key   dedupKey
			count int
		}
		var entries []dedupEntry
		for _, c := range cat.Commits {
			k := dedupKey{c.Scope, c.Summary, c.Author}
			if idx, ok := seen[k]; ok {
				entries[idx].count++
			} else {
				seen[k] = len(entries)
				entries = append(entries, dedupEntry{key: k, count: 1})
			}
		}
		for _, e := range entries {
			scope := ""
			if e.key.scope != "" {
				scope = fmt.Sprintf("**%s**: ", e.key.scope)
			}
			author := ""
			if e.key.author != "" {
				author = fmt.Sprintf(" (%s)", e.key.author)
			}
			countSuffix := ""
			if e.count > 1 {
				countSuffix = fmt.Sprintf(" ×%d", e.count)
			}
			b.WriteString(fmt.Sprintf("- %s%s%s%s\n", scope, e.key.summary, author, countSuffix))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// sectionSecurity renders the Security section body.
func sectionSecurity(input NotesInput) string {
	if input.SecurityBody == "" {
		return ""
	}
	return "## Security\n\n" + input.SecurityBody + "\n"
}

// sectionChangelog renders the full-changelog entry lines — the looping content
// only; the collapsible wrapper is the body's authored markdown.
func sectionChangelog(allCommits []Commit) string {
	if len(allCommits) == 0 {
		return "No changes found.\n"
	}
	var b strings.Builder
	for _, c := range allCommits {
		author := ""
		if c.Author != "" {
			author = fmt.Sprintf(" (%s)", c.Author)
		}
		b.WriteString(fmt.Sprintf("- [`%s`] %s%s\n", c.Hash, c.Summary, author))
	}
	return b.String()
}
