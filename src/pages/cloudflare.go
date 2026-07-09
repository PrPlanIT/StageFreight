package pages

import (
	"context"
	"fmt"
)

// cloudflareProvider deploys a publish workspace to Cloudflare Pages via the native
// Direct Upload client (cloudflare_api.go) — a faithful port of wrangler's protocol.
// No wrangler, no npm, no docker: SF makes the CF Pages API calls itself, so it
// controls exactly what runs with the account credentials (consistent with the
// forge-layer and go-git GitHub path).
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

func (c *cloudflareProvider) Deploy(ctx context.Context, ws string, opts DeployOpts) (DeployResult, error) {
	if opts.Project == "" {
		return DeployResult{}, fmt.Errorf("cloudflare pages: project name required (target id or project:)")
	}
	token := opts.Env["CLOUDFLARE_API_TOKEN"]
	account := opts.Env["CLOUDFLARE_ACCOUNT_ID"]
	if token == "" || account == "" {
		return DeployResult{}, fmt.Errorf("cloudflare pages: missing required credential CLOUDFLARE_API_TOKEN and/or CLOUDFLARE_ACCOUNT_ID")
	}

	client := newCFPagesClient(token, account, opts.Project, opts.Domain)
	if opts.DryRun {
		// Safe first pass: hash the workspace (exercising the full asset pipeline)
		// without any external call.
		assets, err := client.collectAssets(ws)
		if err != nil {
			return DeployResult{}, err
		}
		return DeployResult{URL: fmt.Sprintf("[dry-run] would deploy %d file(s) to cloudflare project %q", len(assets), opts.Project)}, nil
	}
	return client.deploy(ctx, ws)
}
