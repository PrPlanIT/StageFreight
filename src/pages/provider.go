// Package pages holds the static-site deploy providers for the `kind: pages` target.
// The lifecycle is provider-agnostic — resolve a build's transport artifact, extract it
// into a publish workspace, Prepare the workspace, then Deploy — which generalizes to a
// future PublishProvider (Cloudflare Pages, GitHub Pages, S3 website, Netlify, …). Only
// method shapes that stay vendor-neutral live on the interface.
package pages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Provider deploys a prepared publish workspace to a static hosting provider.
type Provider interface {
	// Prepare finalizes the workspace in place before deploy: applies include/exclude
	// and writes any provider-specific metadata (e.g. GitHub's CNAME/.nojekyll). Keeps
	// provider quirks out of the publish runner.
	Prepare(ws string, opts DeployOpts) error
	// Deploy publishes the workspace. A returned error means the SITE deploy itself
	// failed (the critical operation). Custom-domain configuration is an enhancement
	// carried in DeployResult.Domains and never surfaces as this error — a domain
	// problem must not retroactively fail a successful deploy.
	Deploy(ctx context.Context, ws string, opts DeployOpts) (DeployResult, error)
	// Credentials declares the env vars this provider needs (for diagnostics + forwarding).
	Credentials() []CredentialRequirement
}

// DeployResult separates the two distinct outcomes of a pages deploy: the site
// reaching the host (critical — an error on Deploy) and the custom-domain
// configuration (an enhancement — best-effort, reported as data, never fatal).
type DeployResult struct {
	URL     string          // deployment URL (e.g. https://abc.project.pages.dev)
	Domains []DomainOutcome // one per requested custom domain; empty when none requested
	Notes   []string        // non-fatal provider notes (e.g. "Pages enabled from gh-pages")
}

// DNSProvider classifies where a domain's AUTHORITATIVE nameservers live (an NS lookup,
// not the registrar) — the signal for whether the host can auto-configure DNS. Purely
// informational: used to tailor diagnostics, never to gate the attach.
type DNSProvider string

const (
	DNSCloudflare DNSProvider = "cloudflare" // every authoritative NS ends in .cloudflare.com
	DNSExternal   DNSProvider = "external"   // authoritative DNS is hosted elsewhere
	DNSUnknown    DNSProvider = "unknown"    // NS lookup failed or was inconclusive
)

// DomainOutcome is the DATA of a custom-domain attach attempt — the cli/cmd layer turns
// it into tailored guidance (this package never renders). The Pages API is the authority
// for whether the hostname is attached; DNSProvider only colors the advice.
type DomainOutcome struct {
	Name        string      // the requested custom domain
	Attached    bool        // the host accepted the custom-domain association
	DNSProvider DNSProvider // authoritative-NS classification (informational)
	Err         string      // non-fatal attach error text; empty on success
}

// CredentialRequirement describes one credential a provider needs. Returning
// structured requirements (not raw names) makes missing-credential diagnostics precise.
type CredentialRequirement struct {
	Name        string
	Required    bool
	Description string
}

// DeployOpts carries the vendor-neutral inputs a deploy needs.
type DeployOpts struct {
	Project string            // cloudflare project/site name (default: target id)
	Repo    string            // github OWNER/REPO (default: current repo)
	Domains []string          // custom domain(s) to attach, if any
	Include []string          // workspace allowlist globs (empty = keep everything)
	Exclude []string          // workspace denylist globs
	Env     map[string]string // resolved credentials to forward into the deploy
	DryRun  bool              // stage but do not externalize
}

// Get returns the provider for a name. Cloudflare and GitHub are co-equal — there is
// no default, so the caller must name one (a pages target requires provider:).
func Get(name string) (Provider, error) {
	switch name {
	case "cloudflare":
		return &cloudflareProvider{}, nil
	case "github":
		return &githubProvider{}, nil
	default:
		return nil, fmt.Errorf("pages requires provider: cloudflare or github (got %q)", name)
	}
}

// FilterWorkspace prunes a publish workspace in place: removes files matching any
// Exclude glob, then (if Include is non-empty) removes anything NOT matching an Include
// glob. Patterns match against workspace-relative slash paths. Shared by providers'
// Prepare so filtering semantics stay identical across vendors.
func FilterWorkspace(ws string, opts DeployOpts) error {
	if len(opts.Include) == 0 && len(opts.Exclude) == 0 {
		return nil
	}
	var toRemove []string
	err := filepath.Walk(ws, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(ws, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if matchesAny(rel, opts.Exclude) {
			toRemove = append(toRemove, path)
			return nil
		}
		if len(opts.Include) > 0 && !matchesAny(rel, opts.Include) {
			toRemove = append(toRemove, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range toRemove {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

// matchesAny reports whether rel matches any glob. A pattern ending in "/**" (or a bare
// dir prefix) matches everything under that directory; otherwise filepath.Match on the
// full relative path, with a basename fallback so "*.map" matches at any depth.
func matchesAny(rel string, globs []string) bool {
	for _, g := range globs {
		g = filepath.ToSlash(g)
		if suffix, ok := trimRecursive(g); ok { // "dir/**" → everything under dir
			if rel == suffix || hasPathPrefix(rel, suffix) {
				return true
			}
			continue
		}
		if rest, ok := strings.CutPrefix(g, "**/"); ok { // "**/*.map" → any depth
			if ok, _ := filepath.Match(rest, filepath.Base(rel)); ok {
				return true
			}
			if ok, _ := filepath.Match(rest, rel); ok {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(g, rel); ok { // exact / single-segment
			return true
		}
		if ok, _ := filepath.Match(g, filepath.Base(rel)); ok { // basename ("*.map")
			return true
		}
	}
	return false
}

func trimRecursive(g string) (string, bool) {
	switch {
	case len(g) >= 3 && g[len(g)-3:] == "/**":
		return g[:len(g)-3], true
	case len(g) >= 4 && g[len(g)-4:] == "/**/":
		return g[:len(g)-4], true
	default:
		return "", false
	}
}

func hasPathPrefix(rel, dir string) bool {
	return len(rel) > len(dir) && rel[:len(dir)] == dir && rel[len(dir)] == '/'
}
