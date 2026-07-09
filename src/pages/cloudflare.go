package pages

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// cloudflareProvider deploys a publish workspace to Cloudflare Pages via
// `wrangler pages deploy` run in a node container — a direct upload of the pre-built
// directory (no Cloudflare-side build, so it doesn't consume the CF build budget).
type cloudflareProvider struct{}

func (c *cloudflareProvider) Credentials() []CredentialRequirement {
	return []CredentialRequirement{
		{Name: "CLOUDFLARE_API_TOKEN", Required: true, Description: "Cloudflare API token with Pages:Edit"},
		{Name: "CLOUDFLARE_ACCOUNT_ID", Required: true, Description: "Cloudflare account ID"},
	}
}

// Prepare: Cloudflare serves at the domain root and needs no injected metadata, so it's
// just the shared workspace filter.
func (c *cloudflareProvider) Prepare(ws string, opts DeployOpts) error {
	return FilterWorkspace(ws, opts)
}

func (c *cloudflareProvider) Deploy(ctx context.Context, ws string, opts DeployOpts) (string, error) {
	if opts.Project == "" {
		return "", fmt.Errorf("cloudflare pages: project name required (target id or project:)")
	}
	if opts.DryRun {
		// Safe first pass: validate creds + report, without externalizing.
		if _, err := c.env(opts); err != nil {
			return "", err
		}
		return fmt.Sprintf("[dry-run] would deploy %s to cloudflare project %q", ws, opts.Project), nil
	}
	env, err := c.env(opts)
	if err != nil {
		return "", err
	}

	args := []string{"run", "--rm", "-v", ws + ":/out:ro", "-w", "/out"}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, "node:20", "sh", "-c",
		"npx --yes wrangler@3 pages deploy /out --project-name "+shellSingleQuote(opts.Project))

	out, runErr := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if runErr != nil {
		return "", fmt.Errorf("wrangler pages deploy failed: %w\n%s", runErr, string(out))
	}
	if url := firstPagesURL(string(out)); url != "" {
		return url, nil
	}
	return fmt.Sprintf("https://%s.pages.dev", opts.Project), nil
}

// env resolves the required credentials, erroring on any missing required one.
func (c *cloudflareProvider) env(opts DeployOpts) (map[string]string, error) {
	env := map[string]string{}
	for _, cr := range c.Credentials() {
		v, ok := opts.Env[cr.Name]
		if !ok || v == "" {
			if cr.Required {
				return nil, fmt.Errorf("cloudflare pages: missing required credential %s (%s)", cr.Name, cr.Description)
			}
			continue
		}
		env[cr.Name] = v
	}
	return env, nil
}

var pagesURLRe = regexp.MustCompile(`https://[a-zA-Z0-9.-]+\.pages\.dev\S*`)

func firstPagesURL(out string) string {
	return pagesURLRe.FindString(out)
}

// shellSingleQuote wraps s in single quotes, escaping embedded single quotes, so a
// project name is passed to `sh -c` safely.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
