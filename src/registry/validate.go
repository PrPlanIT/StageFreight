package registry

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/sofmeright/stagefreight/src/config"
)

// Validation regexes based on OCI Distribution Spec.
var (
	// OCI repository path: lowercase, digits, separators (-, _, ., /), max 256 chars.
	ociPathRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

	// OCI tag: alphanumeric, -, _, ., max 128 chars. Must start with alphanumeric.
	ociTagRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

	// Env var prefix: uppercase letters, digits, underscore. Must start with letter.
	envPrefixRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// Known provider values (canonical + aliases).
var knownProviders = map[string]bool{
	"local":     true, // local Docker daemon
	"docker":    true, // canonical
	"dockerhub": true, // alias → docker
	"github":    true, // canonical
	"ghcr":      true, // alias → github
	"gitlab":    true,
	"quay":      true,
	"jfrog":     true,
	"harbor":    true,
	"gitea":     true,
	"forgejo":   true,
	"generic":   true,
	"":          true, // empty = auto-detect
}

// ValidateRegistryURL checks that a registry URL is well-formed.
// Rejects strings with spaces, control characters, or invalid structure.
func ValidateRegistryURL(u string) error {
	if u == "" {
		return fmt.Errorf("registry URL is empty")
	}
	if containsControlChars(u) {
		return fmt.Errorf("registry URL %q contains control characters", u)
	}
	if strings.ContainsAny(u, " \t\n\r") {
		return fmt.Errorf("registry URL %q contains whitespace", u)
	}

	// Strip scheme for host validation
	host := u
	if idx := strings.Index(host, "://"); idx >= 0 {
		scheme := host[:idx]
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("registry URL %q has invalid scheme %q (expected http or https)", u, scheme)
		}
		host = host[idx+3:]
	}

	// Must have at least a host
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	if host == "" {
		return fmt.Errorf("registry URL %q has empty host", u)
	}

	// Basic host validation: no spaces, has at least one dot or is localhost/IP
	if strings.ContainsAny(host, " \t{}[]<>\"'`") {
		return fmt.Errorf("registry URL %q has invalid host characters", u)
	}

	return nil
}

// ValidateImagePath checks that a repository/image path conforms to OCI spec.
func ValidateImagePath(path string) error {
	if path == "" {
		return fmt.Errorf("image path is empty")
	}
	if containsControlChars(path) {
		return fmt.Errorf("image path %q contains control characters", path)
	}
	if len(path) > 256 {
		return fmt.Errorf("image path %q exceeds 256 characters", path)
	}

	// Allow template variables — strip {…} blocks before validating the literal parts.
	// This lets "prplanit/{env:REPO}" pass through.
	literal := stripTemplates(path)
	if literal != "" && !ociPathRe.MatchString(literal) {
		return fmt.Errorf("image path %q contains invalid characters (OCI spec: lowercase, digits, -, _, ., /)", path)
	}

	return nil
}

// ValidateTag checks that a resolved tag conforms to OCI spec.
func ValidateTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("tag is empty")
	}
	if containsControlChars(tag) {
		return fmt.Errorf("tag %q contains control characters", tag)
	}
	if len(tag) > 128 {
		return fmt.Errorf("tag %q exceeds 128 characters", tag)
	}
	if !ociTagRe.MatchString(tag) {
		return fmt.Errorf("tag %q contains invalid characters (OCI spec: alphanumeric, -, _, .)", tag)
	}
	return nil
}

// ValidateTagTemplate checks that an unresolved tag template is structurally valid.
// Allows {var} and {var:param} syntax. Rejects unclosed braces, spaces, control chars.
func ValidateTagTemplate(tmpl string) error {
	if tmpl == "" {
		return fmt.Errorf("tag template is empty")
	}
	if containsControlChars(tmpl) {
		return fmt.Errorf("tag template %q contains control characters", tmpl)
	}
	if strings.ContainsAny(tmpl, " \t\n\r") {
		return fmt.Errorf("tag template %q contains whitespace", tmpl)
	}

	// Check balanced braces
	depth := 0
	for i, c := range tmpl {
		switch c {
		case '{':
			depth++
			if depth > 1 {
				return fmt.Errorf("tag template %q has nested braces at position %d", tmpl, i)
			}
		case '}':
			depth--
			if depth < 0 {
				return fmt.Errorf("tag template %q has unmatched closing brace at position %d", tmpl, i)
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("tag template %q has unclosed brace", tmpl)
	}

	return nil
}

// ValidateCredentials checks that a credential prefix is a valid env var name.
func ValidateCredentials(prefix string) error {
	if prefix == "" {
		return nil // empty = no credentials
	}
	upper := strings.ToUpper(prefix)
	if !envPrefixRe.MatchString(upper) {
		return fmt.Errorf("credentials prefix %q is not a valid env var name (expected: [A-Z][A-Z0-9_]*)", prefix)
	}
	return nil
}

// ValidateProvider normalizes and checks that the provider is a known value.
func ValidateProvider(provider string) error {
	_, err := CanonicalProvider(provider)
	return err
}

// CanonicalProvider normalizes a provider string to its canonical form and
// validates it. Returns the canonical name or an error for unknown values.
func CanonicalProvider(provider string) (string, error) {
	canonical := NormalizeProvider(provider)
	if !knownProviders[canonical] {
		return "", fmt.Errorf("unknown provider %q (valid: docker, github, gitlab, quay, jfrog, harbor, gitea, forgejo, local, generic)", provider)
	}
	return canonical, nil
}

// ValidateRetention checks that all retention policy values are non-negative.
func ValidateRetention(r config.RetentionPolicy) error {
	if r.KeepLast < 0 {
		return fmt.Errorf("retention keep_last must be >= 0, got %d", r.KeepLast)
	}
	if r.KeepDaily < 0 {
		return fmt.Errorf("retention keep_daily must be >= 0, got %d", r.KeepDaily)
	}
	if r.KeepWeekly < 0 {
		return fmt.Errorf("retention keep_weekly must be >= 0, got %d", r.KeepWeekly)
	}
	if r.KeepMonthly < 0 {
		return fmt.Errorf("retention keep_monthly must be >= 0, got %d", r.KeepMonthly)
	}
	if r.KeepYearly < 0 {
		return fmt.Errorf("retention keep_yearly must be >= 0, got %d", r.KeepYearly)
	}
	return nil
}

// ValidatePattern checks that a regex pattern compiles.
func ValidatePattern(pattern string) error {
	p := pattern
	if strings.HasPrefix(p, "!") {
		p = p[1:]
	}
	if p == "" {
		return nil
	}
	_, err := regexp.Compile(p)
	if err != nil {
		return fmt.Errorf("pattern %q is not valid regex: %w", pattern, err)
	}
	return nil
}

// ValidateRegistryConfig runs all validation checks against a registry config entry.
// Returns all errors found (not just the first).
func ValidateRegistryConfig(url, path string, tags []string, credentials, provider string, branches, gitTags []string, retention config.RetentionPolicy) []error {
	var errs []error

	// URL is optional for local provider (Docker daemon, no remote registry)
	if provider != "local" {
		if err := ValidateRegistryURL(url); err != nil {
			errs = append(errs, err)
		}
	}
	if err := ValidateImagePath(path); err != nil {
		errs = append(errs, err)
	}
	for _, t := range tags {
		if err := ValidateTagTemplate(t); err != nil {
			errs = append(errs, err)
		}
	}
	if err := ValidateCredentials(credentials); err != nil {
		errs = append(errs, err)
	}
	if err := ValidateProvider(provider); err != nil {
		errs = append(errs, err)
	}
	for _, p := range branches {
		if err := ValidatePattern(p); err != nil {
			errs = append(errs, fmt.Errorf("branches: %w", err))
		}
	}
	for _, p := range gitTags {
		if err := ValidatePattern(p); err != nil {
			errs = append(errs, fmt.Errorf("git_tags: %w", err))
		}
	}
	if err := ValidateRetention(retention); err != nil {
		errs = append(errs, err)
	}

	return errs
}

// containsControlChars returns true if the string has any ASCII control characters.
func containsControlChars(s string) bool {
	for _, r := range s {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' {
			return true
		}
		if r == unicode.ReplacementChar {
			return true
		}
	}
	return false
}

// stripTemplates removes all {…} blocks from a string, returning only literal parts.
// Used to validate the non-template portions of paths/tags.
func stripTemplates(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '{' {
			j := i + 1
			for j < len(s) && s[j] != '}' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
