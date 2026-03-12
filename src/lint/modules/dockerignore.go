package modules

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/moby/patternmatcher"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("dockerignore", func() lint.Module {
		return &dockerignoreModule{cfg: defaultDockerignoreConfig()}
	})
}

// --------------------------------------------------------------------
// Configuration
// --------------------------------------------------------------------

type dockerignoreConfig struct {
	Context            string   `json:"context"`
	Severity           string   `json:"severity"`
	ExtraPatterns      []string `json:"extra_patterns"`
	SkipPatterns       []string `json:"skip_patterns"`
	ForbiddenNegations []string `json:"forbidden_negations"`
}

func defaultDockerignoreConfig() dockerignoreConfig {
	return dockerignoreConfig{
		Context:  ".",
		Severity: "warning",
	}
}

// --------------------------------------------------------------------
// Sensitive entry table
// --------------------------------------------------------------------

type sensitiveEntry struct {
	label     string
	testPaths []string
	reason    string
}

var defaultSensitiveEntries = []sensitiveEntry{
	{".env / .env.*", []string{".env", ".env.local"}, "environment secrets"},
	{".git", []string{".git", ".git/config"}, "repository history (large + may contain secrets)"},
	{".ssh", []string{".ssh", ".ssh/id_rsa"}, "SSH keys directory"},
	{"*.pem", []string{"server.pem"}, "TLS certificates/keys"},
	{"*.key", []string{"private.key"}, "TLS private keys"},
	{"*.p12 / *.pfx", []string{"keystore.p12", "cert.pfx"}, "PKCS#12 keystores"},
	{"*.jks", []string{"keystore.jks"}, "Java keystores"},
	{"*_rsa / *_ed25519", []string{"id_rsa", "id_ed25519"}, "SSH private keys"},
	{".npmrc", []string{".npmrc"}, "npm auth tokens"},
	{".pypirc", []string{".pypirc"}, "PyPI auth tokens"},
	{".netrc", []string{".netrc"}, "credential store"},
	{".aws", []string{".aws", ".aws/credentials"}, "AWS credentials"},
	{".kube", []string{".kube", ".kube/config"}, "Kubernetes credentials"},
}

// --------------------------------------------------------------------
// Module
// --------------------------------------------------------------------

type dockerignoreModule struct {
	cfg     dockerignoreConfig
	sev     lint.Severity
	checked sync.Map // resolved context root → bool
}

func (m *dockerignoreModule) Name() string        { return "dockerignore" }
func (m *dockerignoreModule) DefaultEnabled() bool { return true }
func (m *dockerignoreModule) AutoDetect() []string { return []string{"Dockerfile*", "*.dockerfile"} }

// Configure implements lint.ConfigurableModule.
func (m *dockerignoreModule) Configure(opts map[string]any) error {
	cfg := defaultDockerignoreConfig()
	if len(opts) != 0 {
		b, err := json.Marshal(opts)
		if err != nil {
			return fmt.Errorf("dockerignore: marshal options: %w", err)
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("dockerignore: unmarshal options: %w", err)
		}
	}

	switch cfg.Severity {
	case "warning":
		m.sev = lint.SeverityWarning
	case "critical":
		m.sev = lint.SeverityCritical
	default:
		return fmt.Errorf("dockerignore: invalid severity %q (must be \"warning\" or \"critical\")", cfg.Severity)
	}

	m.cfg = cfg
	return nil
}

// Check inspects Dockerfiles and validates the .dockerignore in the build context.
func (m *dockerignoreModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	if !isDockerfileName(filepath.Base(file.Path)) {
		return nil, nil
	}

	contextRoot := resolveContextRoot(file, m.cfg.Context)

	// Dedup: only check each context root once.
	if _, loaded := m.checked.LoadOrStore(contextRoot, true); loaded {
		return nil, nil
	}

	entries := m.buildEntryList()
	broad := copiesBroadContext(file.AbsPath)

	ignorePath := filepath.Join(contextRoot, ".dockerignore")
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		return []lint.Finding{{
			File:     file.Path,
			Line:     1,
			Module:   m.Name(),
			Severity: m.sev,
			Message:  "no .dockerignore found — build context may include sensitive files",
		}}, nil
	}

	lines, err := parseDockerignore(ignorePath)
	if err != nil {
		return nil, fmt.Errorf("dockerignore: reading %s: %w", ignorePath, err)
	}

	var findings []lint.Finding

	// .git exclusion check always runs (even for scoped Dockerfiles).
	gitEntry := sensitiveEntry{".git", []string{".git", ".git/config"}, "repository history (large + may contain secrets)"}
	if !isCovered(lines, gitEntry.testPaths) {
		findings = append(findings, lint.Finding{
			File:     file.Path,
			Line:     1,
			Module:   m.Name(),
			Severity: m.sev,
			Message:  fmt.Sprintf(".dockerignore may not exclude %s (%s) — heuristic check", gitEntry.label, gitEntry.reason),
		})
	}

	// Full coverage check only when Dockerfile uses broad context.
	if broad {
		for _, entry := range entries {
			// Skip .git — already checked above.
			if entry.label == ".git" {
				continue
			}
			if !isCovered(lines, entry.testPaths) {
				findings = append(findings, lint.Finding{
					File:     file.Path,
					Line:     1,
					Module:   m.Name(),
					Severity: m.sev,
					Message:  fmt.Sprintf(".dockerignore may not exclude %s (%s) — heuristic check", entry.label, entry.reason),
				})
			}
		}
	}

	// Negation warnings.
	findings = append(findings, m.checkNegations(file.Path, lines, entries)...)

	return findings, nil
}

// buildEntryList returns the sensitive entries after applying skip_patterns and extra_patterns.
func (m *dockerignoreModule) buildEntryList() []sensitiveEntry {
	skip := make(map[string]bool)
	for _, s := range m.cfg.SkipPatterns {
		skip[s] = true
	}

	var entries []sensitiveEntry
	for _, e := range defaultSensitiveEntries {
		if skip[e.label] {
			continue
		}
		skipped := false
		for _, tp := range e.testPaths {
			if skip[tp] {
				skipped = true
				break
			}
		}
		if !skipped {
			entries = append(entries, e)
		}
	}

	for _, extra := range m.cfg.ExtraPatterns {
		entries = append(entries, sensitiveEntry{
			label:     extra,
			testPaths: []string{extra},
			reason:    "user-configured",
		})
	}

	return entries
}

// checkNegations walks .dockerignore lines in order and warns when negation
// patterns re-include sensitive files.
func (m *dockerignoreModule) checkNegations(filePath string, lines []string, entries []sensitiveEntry) []lint.Finding {
	forbidden := make(map[string]bool)
	for _, f := range m.cfg.ForbiddenNegations {
		forbidden[f] = true
	}

	var findings []lint.Finding

	for i, line := range lines {
		if !strings.HasPrefix(line, "!") {
			continue
		}
		negPattern := line[1:]

		for _, entry := range entries {
			for _, tp := range entry.testPaths {
				if !lint.MatchGlob(negPattern, tp) {
					continue
				}

				// Forbidden negations always fire.
				if forbidden[negPattern] || forbidden[entry.label] {
					findings = append(findings, lint.Finding{
						File:     filePath,
						Line:     1,
						Module:   "dockerignore",
						Severity: m.sev,
						Message:  fmt.Sprintf(".dockerignore negation '!%s' re-includes %s (%s)", negPattern, entry.label, entry.reason),
					})
					break
				}

				// Default: only warn if a prior line would have excluded this path.
				if hasPriorExclude(lines[:i], tp) {
					findings = append(findings, lint.Finding{
						File:     filePath,
						Line:     1,
						Module:   "dockerignore",
						Severity: m.sev,
						Message:  fmt.Sprintf(".dockerignore negation '!%s' re-includes %s (%s)", negPattern, entry.label, entry.reason),
					})
				}
				break
			}
		}
	}

	return findings
}

// hasPriorExclude checks whether any non-negation line matches the test path.
func hasPriorExclude(priorLines []string, testPath string) bool {
	for _, l := range priorLines {
		if strings.HasPrefix(l, "!") {
			continue
		}
		if lint.MatchGlob(l, testPath) {
			return true
		}
	}
	return false
}

// --------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------

// isDockerfileName returns true for Dockerfile, Dockerfile.*, *.dockerfile (case-insensitive).
func isDockerfileName(basename string) bool {
	lower := strings.ToLower(basename)
	if lower == "dockerfile" {
		return true
	}
	if strings.HasPrefix(lower, "dockerfile.") {
		return true
	}
	if strings.HasSuffix(lower, ".dockerfile") {
		return true
	}
	return false
}

// resolveContextRoot derives the build context directory from the file info and config.
func resolveContextRoot(file lint.FileInfo, contextOpt string) string {
	repoRoot := deriveRepoRoot(file)
	if contextOpt == "" || contextOpt == "." {
		return repoRoot
	}
	if filepath.IsAbs(contextOpt) {
		return contextOpt
	}
	return filepath.Join(repoRoot, contextOpt)
}

// deriveRepoRoot infers the repository root from FileInfo fields.
func deriveRepoRoot(file lint.FileInfo) string {
	if file.AbsPath != "" && file.Path != "" {
		// AbsPath should end with Path — strip it to get the root.
		relNorm := filepath.FromSlash(file.Path)
		if strings.HasSuffix(file.AbsPath, relNorm) {
			root := strings.TrimSuffix(file.AbsPath, relNorm)
			root = strings.TrimRight(root, string(filepath.Separator))
			if root != "" {
				return root
			}
		}

		// Fallback: walk up from the Dockerfile looking for .git/.
		dir := filepath.Dir(file.AbsPath)
		for {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		return filepath.Dir(file.AbsPath)
	}

	// Last resort.
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// parseDockerignore reads a .dockerignore file and returns cleaned lines preserving order.
func parseDockerignore(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

// isCovered checks whether all test paths are matched by the .dockerignore
// rule set using Docker's own pattern semantics (directory coverage, globs,
// negation, root anchoring).
func isCovered(ignorePatterns []string, testPaths []string) bool {
	pm, err := patternmatcher.New(ignorePatterns)
	if err != nil {
		return false
	}
	for _, tp := range testPaths {
		matched, err := pm.MatchesOrParentMatches(tp)
		if err != nil || !matched {
			return false
		}
	}
	return true
}

// copiesBroadContext scans a Dockerfile for COPY/ADD instructions that reference
// the entire build context (. or ./).
func copiesBroadContext(dockerfilePath string) bool {
	f, err := os.Open(dockerfilePath)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		upper := strings.ToUpper(line)

		if !strings.HasPrefix(upper, "COPY") && !strings.HasPrefix(upper, "ADD") {
			continue
		}

		tokens := strings.Fields(line)
		if len(tokens) < 3 {
			continue
		}

		// Skip multi-stage COPY --from=...
		for _, t := range tokens[1:] {
			if strings.HasPrefix(strings.ToLower(t), "--from=") {
				goto nextLine
			}
		}

		// Find the source argument: skip the instruction and any --flag=value tokens.
		for _, t := range tokens[1:] {
			if strings.HasPrefix(t, "--") {
				continue
			}
			// First non-flag token is the source.
			if t == "." || t == "./" {
				return true
			}
			break
		}
	nextLine:
	}
	return false
}
