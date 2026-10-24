package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("links", func() lint.Module { return &linksModule{cfg: defaultLinksConfig()} })
}

// linksModule checks that every Markdown link resolves, so a moved or renamed page cannot
// silently break the pages that point at it. Three link classes, each with its own
// configurable severity (error / warn / off):
//
//   - internal: repo-relative (resolved beside the file) and wiki-root (`/a/b`, resolved
//     against the configured root as `<path>.md`).
//   - anchors:  `#section` checked against the target page's headings.
//   - external: http(s) URLs, reachability by network fetch.
//
// internal and anchors default to warn and external to off, so enabling the module only
// reports (and makes no network call) until a config deliberately escalates a class to error
// (the docs preset escalates `internal`, the one filesystem-certain class).
//
// Opt-in by design: DefaultEnabled is false and AutoDetect is nil, so the engine runs this
// only where a config enables it (a docs lint preset) — never fleet-wide. It is a whole-repo
// module because a moved page breaks links in files that did not themselves change, so
// link-vs-file consistency can only be judged over the full set at once.
type linksModule struct {
	cfg     linksConfig
	include []*regexp.Regexp
	exclude []*regexp.Regexp
	extSkip []*regexp.Regexp
}

type linksConfig struct {
	Internal        string   `json:"internal"`            // error | warn | off
	Anchors         string   `json:"anchors"`             // error | warn | off
	External        string   `json:"external"`            // error | warn | off
	Root            string   `json:"root"`                // wiki root for absolute links (default: repo root)
	Include         []string `json:"include"`             // scope globs (default: all markdown)
	Exclude         []string `json:"exclude"`             // skip globs
	ExternalSkip    []string `json:"external_skip"`       // URL globs to leave unchecked
	ExternalTimeout int      `json:"external_timeout_ms"` // per-URL timeout (default 5000)
}

// defaultLinksConfig keeps enabling the module safe and local: the two filesystem-only
// classes (internal, anchors) default to warn — they report, never break a build — and
// external defaults to off, so flipping the module on makes no surprise network call.
// Enforcement is a deliberate per-class escalation to `error` (the docs preset escalates
// `internal`, the one filesystem-certain class); external reachability is an explicit opt-in.
func defaultLinksConfig() linksConfig {
	return linksConfig{Internal: "warn", Anchors: "warn", External: "off", ExternalTimeout: 5000}
}

func (m *linksModule) Name() string         { return "links" }
func (m *linksModule) DefaultEnabled() bool { return false }
func (m *linksModule) AutoDetect() []string { return nil }

// NeedsFullRepo makes the engine check links against the whole collected repo, not just the
// scanned delta: moving or renaming a page breaks inbound links in pages that did not
// themselves change, so under `--level changed` those stale links are only visible when the
// module sees every file, not the changeset.
func (m *linksModule) NeedsFullRepo() bool { return true }

// CacheTTL: external checks hit the network, so the module's result is never cached forever.
// Whole-repo modules bypass the per-file cache anyway; a negative TTL documents the intent.
func (m *linksModule) CacheTTL() time.Duration { return -1 }

func (m *linksModule) Configure(opts map[string]any) error {
	cfg := defaultLinksConfig()
	if len(opts) != 0 {
		b, err := json.Marshal(opts)
		if err != nil {
			return fmt.Errorf("links: marshal options: %w", err)
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("links: unmarshal options: %w", err)
		}
	}
	for name, v := range map[string]string{"internal": cfg.Internal, "anchors": cfg.Anchors, "external": cfg.External} {
		if _, _, ok := severityOf(v); !ok {
			return fmt.Errorf("links: %s severity %q must be error|warn|off", name, v)
		}
	}
	inc, err := compileGlobs(cfg.Include)
	if err != nil {
		return fmt.Errorf("links: include: %w", err)
	}
	exc, err := compileGlobs(cfg.Exclude)
	if err != nil {
		return fmt.Errorf("links: exclude: %w", err)
	}
	skip, err := compileGlobs(cfg.ExternalSkip)
	if err != nil {
		return fmt.Errorf("links: external_skip: %w", err)
	}
	m.cfg, m.include, m.exclude, m.extSkip = cfg, inc, exc, skip
	return nil
}

// Check is a mis-dispatch guard: the engine routes whole-repo modules to CheckAll, so a call
// here means dispatch was bypassed — fail loud rather than silently emit nothing.
func (m *linksModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	return nil, fmt.Errorf("links: Check called on a whole-repo module (engine dispatch bug)")
}

// repoRootOf recovers the repository root from a FileInfo. The engine sets AbsPath to the
// root joined with the repo-relative Path, so trimming Path off AbsPath yields the root —
// link targets then resolve against the scanned repo, not the process working directory
// (which is not the repo root during the baseline re-lint, or when lint is run from a subdir).
// Falls back to "" (→ working-directory-relative) when AbsPath does not carry Path as expected.
func repoRootOf(f lint.FileInfo) string {
	suffix := filepath.FromSlash(f.Path)
	if suffix == "" || !strings.HasSuffix(f.AbsPath, suffix) {
		return ""
	}
	return strings.TrimRight(strings.TrimSuffix(f.AbsPath, suffix), string(filepath.Separator))
}

func (m *linksModule) CheckAll(ctx context.Context, files []lint.FileInfo) ([]lint.Finding, error) {
	slugCache := map[string]map[string]bool{} // repo-rel md path -> heading slugs
	extCache := map[string]*extResult{}       // url -> result (dedup within the run)

	var findings []lint.Finding
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return findings, err
		}
		if !isMarkdown(f.Path) || !m.scoped(f.Path) {
			continue
		}
		// Resolve link targets against the repo root recovered from this file (AbsPath is
		// the root joined with the repo-relative Path), NOT os.Getwd(): the baseline
		// re-lint runs over a staged tree elsewhere, and lint may be invoked from a subdir.
		root := repoRootOf(f)
		data, err := os.ReadFile(f.AbsPath)
		if err != nil {
			return nil, err
		}
		for _, lk := range extractLinks(string(data)) {
			if fs := m.checkLink(ctx, root, f, lk, slugCache, extCache); fs != nil {
				findings = append(findings, fs...)
			}
		}
	}
	return findings, nil
}

func (m *linksModule) checkLink(ctx context.Context, root string, f lint.FileInfo, lk mdLink,
	slugCache map[string]map[string]bool, extCache map[string]*extResult) []lint.Finding {

	raw := strings.TrimSpace(lk.target)
	if raw == "" {
		return nil
	}

	// External URL (http/https, or protocol-relative) — reachability check.
	if isExternalURL(raw) {
		sev, on, _ := severityOf(m.cfg.External)
		if !on {
			return nil
		}
		fetch := strings.SplitN(raw, "#", 2)[0]
		if strings.HasPrefix(fetch, "//") {
			fetch = "https:" + fetch
		}
		if m.externalSkipped(fetch) {
			return nil
		}
		res, ok := extCache[fetch]
		if !ok {
			ok2, reason := externalCheck(ctx, fetch, time.Duration(m.cfg.ExternalTimeout)*time.Millisecond)
			res = &extResult{ok: ok2, reason: reason}
			extCache[fetch] = res
		}
		if res.ok {
			return nil
		}
		return []lint.Finding{m.finding(f, lk, sev, lint.ConfidenceProbable, "external-link",
			fmt.Sprintf("unreachable external link: %s (%s)", raw, res.reason))}
	}

	// Mail/other non-fetchable schemes — not ours to check.
	if schemeRe.MatchString(raw) {
		return nil
	}

	linkPath, frag := splitFragment(raw)
	if i := strings.IndexByte(linkPath, '?'); i >= 0 {
		linkPath = linkPath[:i]
	}

	// Pure in-page anchor (#section with no path): check against this file's own headings.
	if linkPath == "" {
		return m.checkAnchor(f, lk, f.Path, f.AbsPath, frag, slugCache)
	}

	rel := m.resolveRel(f.Path, linkPath)
	if rel == "" {
		return nil // escapes the repo tree — not ours to resolve
	}
	targetFile := resolveTargetFile(root, rel)
	if targetFile == "" {
		sev, on, _ := severityOf(m.cfg.Internal)
		if !on {
			return nil
		}
		return []lint.Finding{m.finding(f, lk, sev, lint.ConfidenceConfirmed, "unresolved-link",
			fmt.Sprintf("unresolved internal link: %s (no such file)", lk.target))}
	}
	if frag != "" {
		relTarget, _ := filepath.Rel(root, targetFile)
		return m.checkAnchor(f, lk, filepath.ToSlash(relTarget), targetFile, frag, slugCache)
	}
	return nil
}

func (m *linksModule) checkAnchor(f lint.FileInfo, lk mdLink, targetRel, targetAbs, frag string,
	slugCache map[string]map[string]bool) []lint.Finding {

	sev, on, _ := severityOf(m.cfg.Anchors)
	if !on {
		return nil
	}
	slugs, ok := slugCache[targetRel]
	if !ok {
		data, err := os.ReadFile(targetAbs)
		if err != nil {
			slugs = map[string]bool{}
		} else {
			slugs = headingSlugs(string(data))
		}
		slugCache[targetRel] = slugs
	}
	if slugs[strings.ToLower(frag)] {
		return nil
	}
	return []lint.Finding{m.finding(f, lk, sev, lint.ConfidenceConfirmed, "unresolved-anchor",
		fmt.Sprintf("unresolved anchor: %s (no heading #%s in %s)", lk.target, frag, targetRel))}
}

func (m *linksModule) finding(f lint.FileInfo, lk mdLink, sev lint.Severity, conf lint.Confidence, ruleID, msg string) lint.Finding {
	return lint.Finding{
		File: f.Path, Line: lk.line, Module: m.Name(),
		Severity: sev, Confidence: conf, Message: msg, RuleID: ruleID, Anchor: lk.target,
	}
}

// resolveRel maps a link target to a repo-relative slash path, or "" if it escapes the repo.
// Absolute (`/a/b`) links resolve against the configured wiki root; relative links resolve
// beside the source file.
func (m *linksModule) resolveRel(srcPath, target string) string {
	var rel string
	if strings.HasPrefix(target, "/") {
		rel = path.Join(m.cfg.Root, strings.TrimPrefix(target, "/"))
	} else {
		rel = path.Join(path.Dir(srcPath), target)
	}
	rel = path.Clean(rel)
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}

func (m *linksModule) scoped(p string) bool {
	for _, re := range m.exclude {
		if re.MatchString(p) {
			return false
		}
	}
	if len(m.include) == 0 {
		return true
	}
	for _, re := range m.include {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

func (m *linksModule) externalSkipped(u string) bool {
	for _, re := range m.extSkip {
		if re.MatchString(u) {
			return true
		}
	}
	return false
}

type extResult struct {
	ok     bool
	reason string
}

type mdLink struct {
	target string
	line   int
}

var (
	inlineLinkRe = regexp.MustCompile(`\]\(([^)\s]+)`)
	refDefRe     = regexp.MustCompile(`^\s*\[[^\]]+\]:\s+(\S+)`)
	fenceRe      = regexp.MustCompile("^\\s*(```|~~~)")
	schemeRe     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
	atxHeadingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*)$`)
	explicitIDRe = regexp.MustCompile(`\{#([A-Za-z0-9_-]+)\}\s*$`)
	htmlAnchorRe = regexp.MustCompile(`(?i)(?:id|name)\s*=\s*"([^"]+)"`)
)

// extractLinks pulls inline `](target)` and reference-style `[id]: target` links, line by
// line, skipping fenced code blocks (``` / ~~~) so example links in code are not flagged.
func extractLinks(content string) []mdLink {
	var out []mdLink
	inFence := false
	for i, line := range strings.Split(content, "\n") {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lineNo := i + 1
		for _, mm := range inlineLinkRe.FindAllStringSubmatch(line, -1) {
			out = append(out, mdLink{target: cleanTarget(mm[1]), line: lineNo})
		}
		if mm := refDefRe.FindStringSubmatch(line); mm != nil {
			out = append(out, mdLink{target: cleanTarget(mm[1]), line: lineNo})
		}
	}
	return out
}

// cleanTarget strips the angle-bracket form (<url>) and any trailing "title".
func cleanTarget(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<") {
		if j := strings.Index(s, ">"); j >= 0 {
			return strings.TrimSpace(s[1:j])
		}
	}
	if j := strings.IndexAny(s, " \t"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

func isExternalURL(raw string) bool {
	if strings.HasPrefix(raw, "//") {
		return true
	}
	low := strings.ToLower(raw)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")
}

func splitFragment(raw string) (pathPart, frag string) {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		return raw[:i], raw[i+1:]
	}
	return raw, ""
}

// resolveTargetFile returns the on-disk path a repo-relative link resolves to, trying the
// wiki conventions: the bare path, a `.md`/`.markdown` extension, a directory index, or an
// existing directory. Returns "" when nothing resolves.
func resolveTargetFile(root, rel string) string {
	osRel := filepath.FromSlash(rel)
	candidates := []string{
		osRel,
		osRel + ".md",
		osRel + ".markdown",
		filepath.Join(osRel, "index.md"),
		filepath.Join(osRel, "README.md"),
	}
	for _, c := range candidates {
		abs := filepath.Join(root, c)
		st, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if st.IsDir() {
			if c == osRel {
				return abs // a link to an existing directory is allowed
			}
			continue
		}
		return abs
	}
	return ""
}

// headingSlugs returns the set of anchor slugs a Markdown document exposes: GitHub-style
// slugs of its ATX headings (with duplicate-suffixing), explicit `{#id}` heading attributes,
// and HTML `id=`/`name=` anchors. Fenced code is skipped.
func headingSlugs(content string) map[string]bool {
	slugs := map[string]bool{}
	counts := map[string]int{}
	inFence := false
	for _, line := range strings.Split(content, "\n") {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, hm := range htmlAnchorRe.FindAllStringSubmatch(line, -1) {
			slugs[strings.ToLower(hm[1])] = true
		}
		hm := atxHeadingRe.FindStringSubmatch(line)
		if hm == nil {
			continue
		}
		text := strings.TrimRight(strings.TrimSpace(hm[2]), "# ")
		if em := explicitIDRe.FindStringSubmatch(text); em != nil {
			slugs[strings.ToLower(em[1])] = true
			text = strings.TrimSpace(explicitIDRe.ReplaceAllString(text, ""))
		}
		base := githubSlug(text)
		if base == "" {
			continue
		}
		slug := base
		if n := counts[base]; n > 0 {
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		counts[base]++
		slugs[slug] = true
	}
	return slugs
}

// githubSlug lowercases, drops punctuation other than hyphen/underscore, and turns spaces
// into hyphens — the common-denominator heading-slug rule.
func githubSlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// severityOf maps a configured level to (severity, enabled, valid). "off" is valid-but-disabled;
// an unrecognized level is invalid (valid=false), distinct from "off".
func severityOf(level string) (sev lint.Severity, enabled, valid bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off":
		return 0, false, true
	case "warn", "warning":
		return lint.SeverityWarning, true, true
	case "", "error", "critical":
		return lint.SeverityCritical, true, true
	}
	return 0, false, false
}

// externalCheck reports whether an http(s) URL is reachable. It is a package var so tests can
// substitute a hermetic checker instead of hitting the network.
var externalCheck = func(ctx context.Context, rawurl string, timeout time.Duration) (bool, string) {
	client := &http.Client{Timeout: timeout}
	do := func(method string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, method, rawurl, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("User-Agent", "stagefreight-lint-links/1")
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}
	code, err := do(http.MethodHead)
	if err != nil || code == http.StatusMethodNotAllowed || code == http.StatusForbidden {
		if code2, err2 := do(http.MethodGet); err2 == nil {
			code = code2
		} else if err != nil {
			return false, "unreachable"
		}
	}
	if code < 400 {
		return true, fmt.Sprintf("HTTP %d", code)
	}
	return false, fmt.Sprintf("HTTP %d", code)
}

func compileGlobs(patterns []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, p := range patterns {
		re, err := regexp.Compile(globToRegex(p))
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// globToRegex anchors a glob and maps `**` to any run of characters, `*` to a run within a
// path segment, and `?` to a single non-separator character.
func globToRegex(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return b.String()
}
